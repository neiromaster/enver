.PHONY: build install test vet clean fmt hooks completions

BINARY := enver
PKG := github.com/neiromaster/enver

# Build the binary into ./bin, keeping the repo root clean.
build:
	mkdir -p bin
	go build -o bin/$(BINARY) ./cmd/enver

# Regenerate shell completion scripts into ./completions (needed before a local goreleaser run).
completions: build
	mkdir -p completions
	./bin/$(BINARY) completion bash > completions/enver.bash
	./bin/$(BINARY) completion zsh  > completions/enver.zsh
	./bin/$(BINARY) completion fish > completions/enver.fish

# Install into $GOBIN (usually on $PATH) — the canonical way to get enver.
install:
	go install $(PKG)/cmd/enver

test:
	go test -race ./...

vet:
	go vet ./...

fmt:
	go fmt ./...

clean:
	rm -rf bin

# Install git hooks via lefthook (a pinned Go tool dependency, no global install).
hooks:
	go tool lefthook install