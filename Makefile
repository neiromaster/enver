.PHONY: build install test vet lint clean fmt hooks completions cover

BINARY := enver
PKG := github.com/neiromaster/enver
COVDIR := $(CURDIR)/.coverage
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
# share one covdata dir. Prints the gate number (`total:` line).
#
# COVDIR must be absolute: each package's test binary runs with the package
# dir as cwd, so a relative GOCOVERDIR would resolve elsewhere and be skipped.
#
# Two legs, because `go test -cover` overwrites GOCOVERDIR in the test
# binary's environment with a throwaway work dir. The unit leg pins the dir
# via -test.gocoverdir (a flag beats go's env override); the e2e leg builds
# its test binary un-instrumented, so TestMain sees the real GOCOVERDIR and
# the instrumented enver child inherits it instead of the doomed temp path.
cover:
	rm -rf .coverage coverage.txt
	mkdir -p .coverage
	ENVER_E2E_COVER=1 GOCOVERDIR=$(COVDIR) go test -race -count=1 -cover -coverpkg=./... $(UNIT_PKGS) -args -test.gocoverdir=$(COVDIR)
	ENVER_E2E_COVER=1 GOCOVERDIR=$(COVDIR) go test -race -count=1 ./test/e2e
	go tool covdata textfmt -i=$(COVDIR) -o coverage.txt
	go tool cover -func=coverage.txt | tail -1