#!/usr/bin/env sh
set -eu

REPO="${ABRA_REPO:-hermawan22/abra}"
VERSION="${ABRA_VERSION:-latest}"
INSTALL_DIR="${ABRA_INSTALL_DIR:-}"
ALLOW_SOURCE_BUILD="${ABRA_ALLOW_SOURCE_BUILD:-0}"
VERIFY_ATTESTATION="${ABRA_VERIFY_ATTESTATION:-auto}"
RELEASE_BASE_URL="${ABRA_RELEASE_BASE_URL:-}"

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
  archive="$tmp/$asset"
  sums="$tmp/SHA256SUMS"
  if [ -n "$RELEASE_BASE_URL" ]; then
    base_url="${RELEASE_BASE_URL%/}"
  elif [ "$VERSION" = "latest" ]; then
    base_url="https://github.com/$REPO/releases/latest/download"
  else
    base_url="https://github.com/$REPO/releases/download/$VERSION"
  fi
  url="$base_url/$asset"
  log "Trying release asset: $url"
  if ! curl -fsSL "$url" -o "$archive"; then
    log "release asset unavailable for ${os}/${arch}: $asset"
    return 1
  fi
  if ! curl -fsSL "$base_url/SHA256SUMS" -o "$sums"; then
    log "missing SHA256SUMS for release asset"
    exit 1
  fi
  verify_checksum "$archive" "$sums" "$asset"
  verify_attestation "$archive" "$asset"
  verify_attestation "$sums" "SHA256SUMS"
  mkdir -p "$tmp/release"
  if ! tar -xzf "$archive" -C "$tmp/release"; then
    log "failed to extract verified release archive: $asset"
    exit 1
  fi
  if [ -x "$tmp/release/abra" ]; then
    printf '%s\n' "$tmp/release/abra"
    return 0
  fi
  found="$(find "$tmp/release" -type f -name abra -perm -111 2>/dev/null | head -n 1 || true)"
  if [ -n "$found" ]; then
    printf '%s\n' "$found"
    return 0
  fi
  log "verified release archive did not contain an executable abra binary"
  exit 1
}

verify_attestation() {
  file="$1"
  asset="$2"
  case "$VERIFY_ATTESTATION" in
    0|false|False|FALSE|no|No|NO|off|Off|OFF)
      log "Skipped GitHub artifact attestation verification: ABRA_VERIFY_ATTESTATION=$VERIFY_ATTESTATION"
      return 0
      ;;
    1|true|True|TRUE|yes|Yes|YES|on|On|ON|auto)
      ;;
    *)
      log "invalid ABRA_VERIFY_ATTESTATION=$VERIFY_ATTESTATION; use auto, 1, or 0"
      exit 1
      ;;
  esac
  if ! command -v gh >/dev/null 2>&1; then
    if [ "$VERIFY_ATTESTATION" = "auto" ]; then
      log "GitHub CLI not found; checksum verified, attestation verification skipped."
      log "For hardened installs, install gh and set ABRA_VERIFY_ATTESTATION=1."
      return 0
    fi
    log "missing GitHub CLI: install gh or set ABRA_VERIFY_ATTESTATION=0 to skip provenance verification"
    exit 1
  fi
  if gh attestation verify --repo "$REPO" "$file" >/dev/null 2>&1; then
    log "Verified GitHub artifact attestation: $asset"
    return 0
  fi
  log "GitHub artifact attestation verification failed for $asset"
  if [ "$VERIFY_ATTESTATION" = "auto" ]; then
    log "GitHub CLI is installed, so automatic provenance verification is enforced."
    log "Set ABRA_VERIFY_ATTESTATION=0 only when you intentionally accept checksum-only installation."
  fi
  exit 1
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
  case "$ALLOW_SOURCE_BUILD" in
    1|true|True|TRUE|yes|Yes|YES|on|On|ON)
      ;;
    *)
      log "No matching release asset was found for this platform/version."
      log "Source builds are disabled by default for install-script safety."
      log "Set ABRA_ALLOW_SOURCE_BUILD=1 only for an explicit developer source build with Go."
      exit 1
      ;;
  esac
  if ! need go; then
    log "ABRA_ALLOW_SOURCE_BUILD=1 was set but Go is not installed."
    log "Install Go once, then rerun this installer, or download a verified Abra release binary."
    exit 1
  fi
  resolved="$VERSION"
  if [ "$resolved" = "latest" ]; then
    resolved="$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" | sed -n 's/.*"tag_name":[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1)"
    if [ -z "$resolved" ]; then
      log "could not resolve latest release tag"
      exit 1
    fi
  fi
  mkdir -p "$tmp/source" "$tmp/gobin"
  source_url="https://github.com/$REPO/archive/refs/tags/$resolved.tar.gz"
  log "Building Abra CLI from source tag: $source_url"
  log "Source builds are developer fallback installs and are not release artifacts."
  curl -fsSL "$source_url" -o "$tmp/source.tar.gz"
  tar -xzf "$tmp/source.tar.gz" -C "$tmp/source"
  src_dir="$(find "$tmp/source" -mindepth 1 -maxdepth 1 -type d | head -n 1)"
  if [ -z "$src_dir" ]; then
    log "source archive did not contain a directory"
    exit 1
  fi
  build_date="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  (cd "$src_dir" && go build -ldflags "-X main.version=$resolved -X main.commit=source -X main.date=$build_date" -o "$tmp/gobin/abra" ./cmd/abra)
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
  binary_path_file="$tmp/binary-path"
  if try_release "$tmp" "$os" "$arch" > "$binary_path_file"; then
    :
  else
    build_with_go "$tmp" > "$binary_path_file"
  fi
  if ! IFS= read -r binary < "$binary_path_file" || [ -z "$binary" ]; then
    log "installer did not resolve an abra binary path"
    exit 1
  fi

  install_binary "$binary" "$dst_dir"
  log "Installed: $dst_dir/abra"
  if ! command -v abra >/dev/null 2>&1; then
    log "Add this to PATH if needed: export PATH=\"$dst_dir:\$PATH\""
  fi
  log "Next: abra setup"
}

main "$@"
