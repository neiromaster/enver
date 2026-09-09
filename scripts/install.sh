#!/usr/bin/env bash
set -euo pipefail

# One-line installer for enver (macOS/Linux). Review this script before
# piping it to bash:
#   https://github.com/neiromaster/enver/blob/main/scripts/install.sh
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/neiromaster/enver/main/scripts/install.sh | bash
#   curl -fsSL https://raw.githubusercontent.com/neiromaster/enver/main/scripts/install.sh | bash -s -- v0.9.1
#
# Env:
#   ENVER_VERSION      tag to install (default: latest release)
#   ENVER_INSTALL_DIR  install dir (default: ~/.local/bin)
#
# No sudo: installs into the user's own directory.

REPO="neiromaster/enver"
BASE="https://github.com/${REPO}/releases/download"

os="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "$os" in
  darwin|linux) ;;
  *) echo "enver: unsupported OS $os — use npm install -g @enver-go/enver or a release zip" >&2; exit 1 ;;
esac

arch="$(uname -m)"
case "$arch" in
  aarch64|arm64) arch="arm64" ;;
  x86_64|x86-64|amd64) arch="amd64" ;;
  *) echo "enver: unsupported arch $arch — use npm install -g @enver-go/enver or a release zip" >&2; exit 1 ;;
esac

version="${ENVER_VERSION:-${1:-}}"
if [[ -z "$version" ]]; then
  echo "enver: resolving the latest release..." >&2
  version="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" |
    grep -o '"tag_name":[[:space:]]*"[^"]*"' | head -1 | cut -d'"' -f4)"
fi

if [[ -z "$version" ]]; then
  echo "enver: could not resolve the latest release" >&2
  exit 1
fi
[[ "$version" == v* ]] || version="v$version"

install_dir="${ENVER_INSTALL_DIR:-${HOME:-}/.local/bin}"
if command -v realpath >/dev/null 2>&1 && realpath -m . >/dev/null 2>&1; then
  install_dir="$(realpath -m "$install_dir")"
elif [[ "$install_dir" != /* ]]; then
  install_dir="$PWD/$install_dir"
fi

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

archive="enver_${version#v}_${os}_${arch}.tar.gz"
echo "enver: downloading ${BASE}/${version}/${archive}..." >&2
curl -fsSL "${BASE}/${version}/${archive}" -o "$work/$archive"
curl -fsSL "${BASE}/${version}/checksums.txt" -o "$work/checksums.txt"

expected="$(awk -v a="$archive" '$2==a {print $1}' "$work/checksums.txt")"
if [[ -z "$expected" ]]; then
  echo "enver: checksums.txt has no entry for $archive" >&2
  exit 1
fi
actual="$(sha256sum "$work/$archive" 2>/dev/null || shasum -a 256 "$work/$archive")"
actual="${actual%% *}"
if [[ "$actual" != "$expected" ]]; then
  echo "enver: checksum mismatch for $archive" >&2
  exit 1
fi

mkdir -p "$install_dir"
tar -xzf "$work/$archive" -C "$install_dir" enver
chmod 755 "$install_dir/enver"

"$install_dir/enver" --version >&2
if ! command -v enver >/dev/null 2>&1; then
  echo "enver: add $install_dir to your PATH (export PATH=\"$install_dir:\$PATH\")" >&2
fi
