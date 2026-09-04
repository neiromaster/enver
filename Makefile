.PHONY: build install test vet lint clean fmt hooks completions cover cover-gate

BINARY := enver
PKG := github.com/neiromaster/enver
COVDIR := $(CURDIR)/.coverage
MIN_COVER ?= 78
# Everything but test/e2e, which needs its own coverage leg (see `cover`).
UNIT_PKGS = $(shell go list ./... | grep -v '/test/e2e')

# Build the binary into ./bin, keeping the repo root clean.
build:
	mkdir -p bin
	go build -o bin/$(BINARY) ./cmd/enver

# Regenerate shell completion scripts into ./completions (needed before a local goreleaser run).
completions:
	bash scripts/generate-completions.sh

# Install into $GOBIN (usually on $PATH) — the canonical way to get enver.
install:
	go install $(PKG)/cmd/enver

test:
	go test -race -count=1 ./...

vet:
	go vet ./...

lint:
	go tool golangci-lint run

fmt:
	go fmt ./...

clean:
	rm -rf bin .coverage coverage.txt

# Install git hooks via lefthook (a pinned Go tool dependency, no global install).
hooks:
	go tool lefthook install

# Full suite under coverage: unit binaries and the instrumented e2e binary
# write covdata into separate legs, merged into coverage.txt. Prints the gate
# number (`total:` line); `cover-gate` enforces it against MIN_COVER.
#
# Two legs, because `go test -cover` overwrites GOCOVERDIR in the test
# binary's environment with a throwaway work dir. The unit leg pins its dir
# via -test.gocoverdir (a flag beats go's env override); the e2e leg builds
# its test binary un-instrumented, so TestMain sees the real GOCOVERDIR and
# the instrumented enver child inherits it. Separate dirs per leg let the
# gate verify that each leg actually produced covdata.
#
# Paths are quoted throughout: make expands $(COVDIR) verbatim into the
# recipe text, and a checkout path with spaces must survive the shell.
cover:
	rm -rf .coverage coverage.txt
	mkdir -p "$(COVDIR)/unit" "$(COVDIR)/e2e"
	go test -race -count=1 -cover -coverpkg=./... $(UNIT_PKGS) -args -test.gocoverdir="$(COVDIR)/unit"
	GOCOVERDIR="$(COVDIR)/e2e" go test -race -count=1 ./test/e2e
	go tool covdata textfmt -i="$(COVDIR)/unit,$(COVDIR)/e2e" -o coverage.txt
	go tool cover -func=coverage.txt | tail -1

# The gate CI runs (.github/workflows/ci.yml): fail below MIN_COVER, on a
# dead leg, or if the unit leg lost its exec_* spawn-failure covdata. The
# e2e x-children flush through the coverage fallback in internal/runner, so
# exec_unix.go's failure branch — exercised by clearing GOCOVERDIR in
# TestExecChildSpawnFailureReturnsOne — is pinnable only from the unit leg.
cover-gate: cover
	@test -n "$$(ls "$(COVDIR)/unit" 2>/dev/null)" || { echo "::error::unit covdata missing: $(COVDIR)/unit is empty"; exit 1; }
	@test -n "$$(ls "$(COVDIR)/e2e" 2>/dev/null)" || { echo "::error::e2e covdata missing: $(COVDIR)/e2e is empty"; exit 1; }
	go tool covdata textfmt -i="$(COVDIR)/unit" -o "$(COVDIR)/unit.txt"
	@grep -E 'exec_(windows|unix)\.go' "$(COVDIR)/unit.txt" | awk '$$NF > 0' | head -1 | grep -q . || { echo "::error::unit covdata missing: no covered exec_* spawn-failure line"; exit 1; }
	go tool cover -func=coverage.txt > "$(COVDIR)/func.txt"
	@total=$$(awk '/^total:/ { gsub("%", "", $$NF); print $$NF }' "$(COVDIR)/func.txt"); \
	test -n "$$total" || { echo "::error::coverage profile is empty: no total line"; exit 1; }; \
	echo "total coverage: $$total% (gate: $(MIN_COVER)%)"; \
	awk -v t="$$total" -v m="$(MIN_COVER)" 'BEGIN { exit !(t+0 >= m+0) }' \
		|| { echo "::error::coverage $$total% is below the gate $(MIN_COVER)%"; exit 1; }