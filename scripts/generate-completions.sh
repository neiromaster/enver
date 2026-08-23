#!/usr/bin/env bash
# Generate shell completions for enver into ./completions (committed; the
# release archive and Homebrew cask consume them).
set -euo pipefail

cd "$(dirname "$0")/.."

bin=$(mktemp)
trap 'rm -f "$bin"' EXIT

go build -o "$bin" ./cmd/enver

mkdir -p completions

"$bin" completion bash > completions/enver.bash
"$bin" completion fish > completions/enver.fish
"$bin" completion zsh > completions/enver.zsh
