#!/usr/bin/env bash
set -Eeuo pipefail
[[ $# == 2 ]] || { echo 'Usage: scripts/build-release.sh vX.Y.Z output-dir' >&2; exit 2; }
VERSION=$1
OUT=$2
[[ "$VERSION" =~ ^v[0-9]+\.[0-9]+\.[0-9]+([.-][a-zA-Z0-9.-]+)?$ ]] || { echo 'Invalid version' >&2; exit 2; }
command -v go >/dev/null || { echo 'Go toolchain is required' >&2; exit 1; }
command -v sha256sum >/dev/null || { echo 'sha256sum required' >&2; exit 1; }
mkdir -p "$OUT"
OUT=$(cd "$OUT" && pwd)
: > "$OUT/SHA256SUMS"
for spec in 'amd64:amd64:' 'arm64:arm64:' 'armv7:arm:7' 'armv6:arm:6'; do
  IFS=: read -r NAME ARCH ARM <<< "$spec"
  archive="apc-ups-bridge_linux_${NAME}.tar.gz"
  tmp=$(mktemp -d)
  CGO_ENABLED=0 GOOS=linux GOARCH="$ARCH" GOARM="$ARM" \
    go build -trimpath -ldflags="-s -w -X main.version=$VERSION" -o "$tmp/apc-ups-bridge" ./cmd/apc-ups-bridge
  tar -C "$tmp" -czf "$OUT/$archive" apc-ups-bridge
  (cd "$OUT" && sha256sum "$archive") >> "$OUT/SHA256SUMS"
  rm -rf "$tmp"
  echo "Built $archive"
done
