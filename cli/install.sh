#!/bin/sh
set -eu

REPO="DadaDevelopment/dada-cloud-console"
INSTALL_DIR="${DDC_INSTALL_DIR:-$HOME/.local/bin}"
RELEASE_TAG="${DDC_RELEASE_TAG:-}"

log() { echo "$@" >&2; }

resolve_latest_cli_tag() {
  curl -fsSL "https://api.github.com/repos/${REPO}/releases" 2>/dev/null \
    | grep '"tag_name"' \
    | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/' \
    | grep '^cli-v' \
    | head -n 1
}

detect_os() {
  case "$(uname -s)" in
    Darwin) echo darwin ;;
    Linux) echo linux ;;
    *) echo "unsupported" ;;
  esac
}

detect_arch() {
  case "$(uname -m)" in
    arm64|aarch64) echo arm64 ;;
    x86_64|amd64) echo amd64 ;;
    *) echo "unsupported" ;;
  esac
}

verify_checksum() {
  bin_path="$1"
  bin_name="$2"
  checksums_path="$3"

  expected="$(grep " ${bin_name}\$" "$checksums_path" 2>/dev/null | awk '{print $1}' || true)"
  if [ -z "$expected" ]; then
    log "ddc install: no checksum entry for $bin_name, skipping verification"
    return 0
  fi

  if command -v shasum >/dev/null 2>&1; then
    actual="$(shasum -a 256 "$bin_path" | awk '{print $1}')"
  elif command -v sha256sum >/dev/null 2>&1; then
    actual="$(sha256sum "$bin_path" | awk '{print $1}')"
  else
    log "ddc install: no sha256 tool found, skipping checksum verification"
    return 0
  fi

  if [ "$expected" != "$actual" ]; then
    log "ddc install: checksum mismatch for $bin_name (expected $expected, got $actual)"
    return 1
  fi
}

install_from_release() {
  os="$(detect_os)"
  arch="$(detect_arch)"

  if [ "$os" = "unsupported" ] || [ "$arch" = "unsupported" ]; then
    log "ddc install: unsupported platform $(uname -s)/$(uname -m)"
    return 1
  fi

  if ! command -v curl >/dev/null 2>&1; then
    log "ddc install: curl is required to download a prebuilt release"
    return 1
  fi

  bin_name="ddc_${os}_${arch}"
  tag="$RELEASE_TAG"
  if [ -z "$tag" ]; then
    tag="$(resolve_latest_cli_tag)"
    if [ -z "$tag" ]; then
      log "ddc install: could not find a cli-v* release for $REPO"
      return 1
    fi
  fi
  base_url="https://github.com/${REPO}/releases/download/${tag}"

  tmp_dir="$(mktemp -d)"
  trap 'rm -rf "$tmp_dir"' EXIT

  log "Downloading $bin_name from $base_url ..."
  if ! curl -fsSL "$base_url/$bin_name" -o "$tmp_dir/ddc"; then
    log "ddc install: could not download $base_url/$bin_name"
    return 1
  fi

  if curl -fsSL "$base_url/checksums.txt" -o "$tmp_dir/checksums.txt" 2>/dev/null; then
    if ! verify_checksum "$tmp_dir/ddc" "$bin_name" "$tmp_dir/checksums.txt"; then
      return 1
    fi
  else
    log "ddc install: checksums.txt not found, skipping checksum verification"
  fi

  mkdir -p "$INSTALL_DIR"
  chmod +x "$tmp_dir/ddc"
  mv "$tmp_dir/ddc" "$INSTALL_DIR/ddc"
  return 0
}

install_from_source() {
  script_dir="$(cd "$(dirname "$0")" && pwd)"
  if [ ! -f "$script_dir/go.mod" ]; then
    return 1
  fi
  if ! command -v go >/dev/null 2>&1; then
    return 1
  fi

  log "Falling back to a local build from $script_dir ..."
  mkdir -p "$INSTALL_DIR"
  ( cd "$script_dir" && go build -o "$INSTALL_DIR/ddc" . )
  return 0
}

main() {
  if install_from_release; then
    log "Installed: $INSTALL_DIR/ddc (prebuilt release)"
  elif install_from_source; then
    log "Installed: $INSTALL_DIR/ddc (built from source)"
  else
    log ""
    log "ddc install: could not download a prebuilt binary and no local Go checkout"
    log "was found to build from. Install Go (https://go.dev/dl/) and re-run this"
    log "script from a clone of $REPO, or download a release manually from:"
    log "  https://github.com/${REPO}/releases"
    exit 1
  fi

  case ":$PATH:" in
    *":$INSTALL_DIR:"*) ;;
    *)
      log ""
      log "$INSTALL_DIR is not on your PATH. Add this to your shell profile:"
      log "  export PATH=\"$INSTALL_DIR:\$PATH\""
      ;;
  esac

  log ""
  log "Next: ddc login"
}

main
