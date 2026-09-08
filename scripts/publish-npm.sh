#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."

# Publish @neiromaster/enver and its 6 platform packages to npm.
# Usage: publish-npm.sh <version> [--pack-only]
#   <version>   semver without the v prefix, e.g. 0.8.4
#   --pack-only emit local .tgz tarballs into ./dist-npm instead of publishing

VERSION="${1:?usage: publish-npm.sh <version> [--pack-only]}"
PACK_ONLY=0
[[ "${2:-}" == "--pack-only" ]] && PACK_ONLY=1

REPO="neiromaster/enver"
BASE_URL="https://github.com/${REPO}/releases/download/v${VERSION}"
OUT_DIR="$(pwd)/dist-npm"

# platform-key:os_arch:ext:bin-name
PLATFORMS=(
  "darwin-arm64:darwin_arm64:tar.gz:enver"
  "darwin-x64:darwin_amd64:tar.gz:enver"
  "linux-arm64:linux_arm64:tar.gz:enver"
  "linux-x64:linux_amd64:tar.gz:enver"
  "win32-arm64:windows_arm64:zip:enver.exe"
  "win32-x64:windows_amd64:zip:enver.exe"
)

if [[ "$VERSION" == "0.0.0" ]]; then
  echo "refusing to publish placeholder version 0.0.0" >&2
  exit 1
fi

# A prerelease (e.g. 1.0.0-rc1) must not claim the `latest` dist-tag.
if [[ "$VERSION" == *-* ]]; then
  NPM_TAG="${VERSION#*-}"
  NPM_TAG="${NPM_TAG%%+*}"
else
  NPM_TAG="latest"
fi

sha256() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

publish_or_pack() {
  local dir="$1" pkg="$2"
  if [[ "$PACK_ONLY" == 1 ]]; then
    (cd "$dir" && npm pack --pack-destination "$OUT_DIR" >/dev/null)
  elif ! (cd "$dir" && npm publish --provenance --access public --tag "$NPM_TAG"); then
    # The npm view guards below can misread a transient registry error as
    # "not published"; a publish conflict re-checked with a fresh view is
    # the authoritative already-published signal.
    if npm view "$pkg" >/dev/null 2>&1; then
      echo "skip $pkg (published concurrently, or the earlier view check hit a registry error)"
    else
      echo "npm publish failed for $pkg and it is not on the registry" >&2
      return 1
    fi
  fi
}

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

[[ "$PACK_ONLY" == 1 ]] && mkdir -p "$OUT_DIR"

curl -fsSL "${BASE_URL}/checksums.txt" -o "$WORK/checksums.txt"

# 1. platform packages (before the meta, so the meta resolves cleanly)
for entry in "${PLATFORMS[@]}"; do
  IFS=: read -r pkg_key os_arch ext bin_name <<< "$entry"
  pkg="@neiromaster/enver-${pkg_key}"
  if [[ "$PACK_ONLY" == 0 ]] && npm view "$pkg@$VERSION" >/dev/null 2>&1; then
    echo "skip $pkg@$VERSION (already published)"
    continue
  fi
  archive="enver_${VERSION}_${os_arch}.${ext}"
  curl -fsSL "${BASE_URL}/${archive}" -o "$WORK/$archive"
  expected="$(awk -v a="$archive" '$2==a {print $1}' "$WORK/checksums.txt")"
  actual="$(sha256 "$WORK/$archive")"
  if [[ "$actual" != "$expected" ]]; then
    echo "checksum mismatch for $archive" >&2
    exit 1
  fi
  dir="$WORK/$pkg_key"
  mkdir -p "$dir/bin"
  if [[ "$ext" == "zip" ]]; then
    unzip -o "$WORK/$archive" -d "$dir" >/dev/null
  else
    tar -xzf "$WORK/$archive" -C "$dir"
  fi
  mv "$dir/$bin_name" "$dir/bin/$bin_name"
  chmod 755 "$dir/bin/$bin_name"
  cat > "$dir/package.json" <<EOF
{
  "name": "$pkg",
  "version": "$VERSION",
  "os": ["${pkg_key%%-*}"],
  "cpu": ["${pkg_key##*-}"],
  "files": ["bin"],
  "license": "MIT",
  "repository": { "type": "git", "url": "git+https://github.com/neiromaster/enver.git" }
}
EOF
  publish_or_pack "$dir" "$pkg@$VERSION"
done

# 2. meta package
if [[ "$PACK_ONLY" == 0 ]] && npm view "@neiromaster/enver@$VERSION" >/dev/null 2>&1; then
  echo "skip @neiromaster/enver@$VERSION (already published)"
  exit 0
fi
META="$WORK/meta"
mkdir -p "$META"
cp npm/package.json npm/README.md "$META/"
cp -r npm/bin "$META/bin"
npm --prefix "$META" pkg set version="$VERSION"
for entry in "${PLATFORMS[@]}"; do
  pkg_key="${entry%%:*}"
  npm --prefix "$META" pkg set "optionalDependencies.@neiromaster/enver-${pkg_key}=$VERSION"
done
publish_or_pack "$META" "@neiromaster/enver@$VERSION"
