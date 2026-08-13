#!/bin/sh
set -eu

REPO_DIR="$(cd "$(dirname "$0")" && pwd)"
INSTALL_DIR="${DDC_INSTALL_DIR:-$HOME/.local/bin}"

if ! command -v go >/dev/null 2>&1; then
  echo "ddc install: Go is required (https://go.dev/dl/) but was not found on PATH" >&2
  exit 1
fi

mkdir -p "$INSTALL_DIR"

echo "Building ddc from $REPO_DIR ..."
(cd "$REPO_DIR" && go build -o "$INSTALL_DIR/ddc" .)

echo "Installed: $INSTALL_DIR/ddc"

case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *)
    echo ""
    echo "$INSTALL_DIR is not on your PATH. Add this to your shell profile:"
    echo "  export PATH=\"$INSTALL_DIR:\$PATH\""
    ;;
esac

echo ""
echo "Next: ddc login"
