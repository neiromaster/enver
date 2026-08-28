.PHONY: build install test vet lint clean fmt hooks completions

BINARY := enver
PKG := github.com/neiromaster/enver

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
	go test -race ./...

vet:
	go vet ./...

lint:
	go tool golangci-lint run

fmt:
	go fmt ./...

clean:
	rm -rf bin

# Install git hooks via lefthook (a pinned Go tool dependency, no global install).
hooks:
	go tool lefthook install