#!/usr/bin/env sh
set -eu

REPO="${ABRA_REPO:-hermawan22/abra}"
VERSION="${ABRA_VERSION:-latest}"
INSTALL_DIR="${ABRA_INSTALL_DIR:-}"

log() {
  printf '%s\n' "$*" >&2
}

need() {
  if ! command -v "$1" >/dev/null 2>&1; then
    return 1
  fi
}

pick_install_dir() {
  if [ -n "$INSTALL_DIR" ]; then
    printf '%s\n' "$INSTALL_DIR"
    return
  fi
  if [ -d /usr/local/bin ] && [ -w /usr/local/bin ]; then
    printf '%s\n' /usr/local/bin
    return
  fi
  printf '%s\n' "$HOME/.local/bin"
}

detect_os() {
  case "$(uname -s)" in
    Darwin) printf '%s\n' darwin ;;
    Linux) printf '%s\n' linux ;;
    *) log "unsupported OS: $(uname -s)"; exit 1 ;;
  esac
}

detect_arch() {
  case "$(uname -m)" in
    x86_64|amd64) printf '%s\n' amd64 ;;
    arm64|aarch64) printf '%s\n' arm64 ;;
    *) log "unsupported architecture: $(uname -m)"; exit 1 ;;
  esac
}

install_binary() {
  src="$1"
  dst_dir="$2"
  mkdir -p "$dst_dir"
  cp "$src" "$dst_dir/abra"
  chmod 0755 "$dst_dir/abra"
}

try_release() {
  tmp="$1"
  os="$2"
  arch="$3"
  asset="abra_${os}_${arch}.tar.gz"
  if [ "$VERSION" = "latest" ]; then
    base_url="https://github.com/$REPO/releases/latest/download"
  else
    base_url="https://github.com/$REPO/releases/download/$VERSION"
  fi
  url="$base_url/$asset"
  log "Trying release asset: $url"
  if curl -fsSL "$url" -o "$tmp/abra.tar.gz"; then
    if curl -fsSL "$base_url/SHA256SUMS" -o "$tmp/SHA256SUMS"; then
      verify_checksum "$tmp/abra.tar.gz" "$tmp/SHA256SUMS" "$asset"
    else
      log "missing SHA256SUMS for release asset"
      return 1
    fi
    mkdir -p "$tmp/release"
    if tar -xzf "$tmp/abra.tar.gz" -C "$tmp/release"; then
      if [ -x "$tmp/release/abra" ]; then
        printf '%s\n' "$tmp/release/abra"
        return 0
      fi
      found="$(find "$tmp/release" -type f -name abra -perm -111 2>/dev/null | head -n 1 || true)"
      if [ -n "$found" ]; then
        printf '%s\n' "$found"
        return 0
      fi
    fi
  fi
  return 1
}

verify_checksum() {
  file="$1"
  sums="$2"
  asset="$3"
  expected="$(awk -v asset="$asset" '$2 == asset {print $1}' "$sums" | head -n 1)"
  if [ -z "$expected" ]; then
    log "SHA256SUMS does not include $asset"
    exit 1
  fi
  if command -v sha256sum >/dev/null 2>&1; then
    actual="$(sha256sum "$file" | awk '{print $1}')"
  elif command -v shasum >/dev/null 2>&1; then
    actual="$(shasum -a 256 "$file" | awk '{print $1}')"
  elif command -v openssl >/dev/null 2>&1; then
    actual="$(openssl dgst -sha256 "$file" | awk '{print $2}')"
  else
    log "missing checksum tool: install sha256sum, shasum, or openssl"
    exit 1
  fi
  if [ "$actual" != "$expected" ]; then
    log "checksum mismatch for $asset"
    log "expected: $expected"
    log "actual:   $actual"
    exit 1
  fi
  log "Verified checksum: $asset"
}

build_with_go() {
  tmp="$1"
  if ! need go; then
    log "No release asset found and Go is not installed."
    log "Install Go once, then rerun this installer, or download an Abra release binary."
    exit 1
  fi
  mkdir -p "$tmp/gobin"
  log "Building Abra CLI with go install..."
  GOBIN="$tmp/gobin" go install "github.com/$REPO/cmd/abra@$VERSION"
  printf '%s\n' "$tmp/gobin/abra"
}

main() {
  if ! need curl; then
    log "missing required command: curl"
    exit 1
  fi
  if ! need tar; then
    log "missing required command: tar"
    exit 1
  fi

  dst_dir="$(pick_install_dir)"
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' EXIT INT TERM

  os="$(detect_os)"
  arch="$(detect_arch)"
  if binary="$(try_release "$tmp" "$os" "$arch")"; then
    :
  else
    binary="$(build_with_go "$tmp")"
  fi

  install_binary "$binary" "$dst_dir"
  log "Installed: $dst_dir/abra"
  if ! command -v abra >/dev/null 2>&1; then
    log "Add this to PATH if needed: export PATH=\"$dst_dir:\$PATH\""
  fi
  log "Next: abra install"
}

main "$@"
