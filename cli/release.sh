#!/bin/sh
set -eu

CLI_DIR="$(cd "$(dirname "$0")" && pwd)"
VERSION="${1:-}"

if [ -z "$VERSION" ]; then
  echo "usage: $0 <version>   (example: $0 0.1.0)" >&2
  exit 1
fi

VERSION="${VERSION#v}"
DIST_DIR="$CLI_DIR/dist"
LDFLAGS="-s -w -X github.com/dada-tuda/console/cli/internal/version.Version=$VERSION"

rm -rf "$DIST_DIR"
mkdir -p "$DIST_DIR"

targets="darwin/arm64 darwin/amd64 linux/amd64 linux/arm64"

for target in $targets; do
  os="${target%/*}"
  arch="${target#*/}"
  out="$DIST_DIR/ddc_${os}_${arch}"
  echo "Building $out ..."
  ( cd "$CLI_DIR" && GOOS="$os" GOARCH="$arch" CGO_ENABLED=0 go build -trimpath -ldflags "$LDFLAGS" -o "$out" . )
done

( cd "$DIST_DIR" && shasum -a 256 ddc_* > checksums.txt )

echo ""
echo "Built artifacts in $DIST_DIR:"
ls -la "$DIST_DIR"
