#!/usr/bin/env bash
# Generate shell completions for enver and enverx into ./completions.
#
# The Homebrew cask wires ONE completion file per shell but ships both binaries,
# so each file must register both. The scripts are concatenated; zsh compinit
# only reads #compdef from the first line, so the combined file declares both
# commands there.
set -euo pipefail

cd "$(dirname "$0")/.."

bin_enver=$(mktemp)
bin_enverx=$(mktemp)
trap 'rm -f "$bin_enver" "$bin_enverx"' EXIT

go build -o "$bin_enver" ./cmd/enver
go build -o "$bin_enverx" ./cmd/enverx

mkdir -p completions

"$bin_enver" completion bash > completions/enver.bash
"$bin_enverx" completion bash >> completions/enver.bash

"$bin_enver" completion fish > completions/enver.fish
"$bin_enverx" completion fish >> completions/enver.fish

{ "$bin_enver" completion zsh; "$bin_enverx" completion zsh; } \
  | sed '1s/^#compdef enver$/#compdef enver enverx/' > completions/enver.zsh
head -1 completions/enver.zsh | grep -q '^#compdef enver enverx' \
  || { echo "enver.zsh: expected combined #compdef header" >&2; exit 1; }
