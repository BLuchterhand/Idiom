#!/bin/sh
# Install only Idiom's externally callable tool sidecar.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/BLuchterhand/idiom/main/install-tools.sh | sh
#
# Installs:
#   idiom-tool           -> runs `idiom tool ...`
#   idiom-mcp-discovery -> runs `idiom mcp serve --stdio --profile discovery ...`
#
# Override knobs:
#   IDIOM_VERSION           pin a specific version (default: latest)
#   IDIOM_INSTALL_DIR       destination dir (default: ~/.local/bin, or
#                           /usr/local/bin if writable without sudo)
#   IDIOM_REPO              GitHub repo (default: BLuchterhand/idiom)
#   IDIOM_RELEASE_URL_BASE  URL prefix for assets and SHA256SUMS

set -eu

REPO="${IDIOM_REPO:-BLuchterhand/idiom}"
VERSION="${IDIOM_VERSION:-}"
INSTALL_DIR="${IDIOM_INSTALL_DIR:-}"
RELEASE_URL_BASE="${IDIOM_RELEASE_URL_BASE:-}"
RELEASE_URL_BASE="${RELEASE_URL_BASE%/}"

log() { printf '%s\n' "$*" >&2; }
die() { log "error: $*"; exit 1; }

require() {
  command -v "$1" >/dev/null 2>&1 || die "required command not found: $1"
}

require curl
require tar
require uname

detect_os() {
  uname_out=$(uname -s)
  case "$uname_out" in
    Darwin) printf 'darwin' ;;
    Linux)  printf 'linux' ;;
    MINGW*|MSYS*|CYGWIN*)
      die "use the Windows installer instead: irm https://raw.githubusercontent.com/${REPO}/main/install-tools.ps1 | iex"
      ;;
    *) die "unsupported OS: $uname_out" ;;
  esac
}

detect_arch() {
  uname_out=$(uname -m)
  case "$uname_out" in
    x86_64|amd64) printf 'amd64' ;;
    arm64|aarch64) printf 'arm64' ;;
    *) die "unsupported arch: $uname_out (idiom ships amd64 and arm64 only)" ;;
  esac
}

resolve_version() {
  if [ -n "$VERSION" ]; then
    printf '%s' "$VERSION"
    return
  fi
  api="https://api.github.com/repos/${REPO}/releases/latest"
  tag=$(curl -fsSL "$api" 2>/dev/null | grep -o '"tag_name":\s*"[^"]*"' | head -1 | sed -e 's/.*:\s*"\(.*\)"/\1/')
  if [ -z "$tag" ]; then
    redirect="https://github.com/${REPO}/releases/latest"
    tag=$(curl -fsSI "$redirect" | awk -F': ' 'tolower($1)=="location"{print $2}' | sed -e 's|.*/tag/||' -e 's|[\r\n]||g' | head -1)
  fi
  [ -n "$tag" ] || die "could not resolve latest release tag from GitHub"
  printf '%s' "$tag"
}

pick_install_dir() {
  if [ -n "$INSTALL_DIR" ]; then
    printf '%s' "$INSTALL_DIR"
    return
  fi
  if [ -w /usr/local/bin ] 2>/dev/null; then
    printf '/usr/local/bin'
    return
  fi
  printf '%s/.local/bin' "$HOME"
}

write_tool_launcher() {
  path="$1"
  mode="$2"
  if [ "$mode" = "tool" ]; then
    cat > "$path" <<'SH'
#!/bin/sh
DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
exec "$DIR/idiom-tool-bin" tool "$@"
SH
  else
    cat > "$path" <<'SH'
#!/bin/sh
DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
exec "$DIR/idiom-tool-bin" mcp serve --stdio --profile discovery "$@"
SH
  fi
  chmod 755 "$path"
}

OS=$(detect_os)
ARCH=$(detect_arch)
VERSION=$(resolve_version)
INSTALL_DIR=$(pick_install_dir)

asset_version=${VERSION#v}
asset="idiom_${asset_version}_${OS}_${ARCH}.tar.gz"
if [ -n "$RELEASE_URL_BASE" ]; then
  url="${RELEASE_URL_BASE}/${asset}"
  sums_url="${RELEASE_URL_BASE}/SHA256SUMS"
else
  url="https://github.com/${REPO}/releases/download/${VERSION}/${asset}"
  sums_url="https://github.com/${REPO}/releases/download/${VERSION}/SHA256SUMS"
fi

log "downloading idiom tool sidecar ${VERSION} for ${OS}/${ARCH}"
log "  asset:  ${url}"
log "  install: ${INSTALL_DIR}/idiom-tool"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

curl -fsSL -o "${tmp}/${asset}" "$url" || die "download failed: $url"
curl -fsSL -o "${tmp}/SHA256SUMS" "$sums_url" || die "checksum file download failed: $sums_url"

log "verifying SHA256 checksum"
if command -v sha256sum >/dev/null 2>&1; then
  hash=$(sha256sum "${tmp}/${asset}" | awk '{print $1}')
elif command -v shasum >/dev/null 2>&1; then
  hash=$(shasum -a 256 "${tmp}/${asset}" | awk '{print $1}')
else
  die "no sha256 tool found (need sha256sum or shasum)"
fi
expected=$(awk -v want="${asset}" '$2 == want {print $1}' "${tmp}/SHA256SUMS")
[ -n "$expected" ] || die "${asset} not listed in SHA256SUMS"
[ "$hash" = "$expected" ] || die "checksum mismatch (got ${hash}, expected ${expected})"

tar -xzf "${tmp}/${asset}" -C "$tmp"
[ -f "${tmp}/idiom" ] || die "archive did not contain an 'idiom' binary"

mkdir -p "$INSTALL_DIR" || die "cannot create install dir: ${INSTALL_DIR}"
mv "${tmp}/idiom" "${INSTALL_DIR}/idiom-tool-bin" || die "cannot install binary into ${INSTALL_DIR}"
chmod 755 "${INSTALL_DIR}/idiom-tool-bin"
write_tool_launcher "${INSTALL_DIR}/idiom-tool" tool
write_tool_launcher "${INSTALL_DIR}/idiom-mcp-discovery" mcp

log "installed: ${INSTALL_DIR}/idiom-tool"
log "installed: ${INSTALL_DIR}/idiom-mcp-discovery"

case ":${PATH}:" in
  *":${INSTALL_DIR}:"*) ;;
  *)
    log ""
    log "warning: ${INSTALL_DIR} is not on your PATH"
    log "add it to your shell profile:"
    log "  export PATH=\"${INSTALL_DIR}:\$PATH\""
    ;;
esac

"${INSTALL_DIR}/idiom-tool" list --profile discovery >/dev/null || die "install verification failed: idiom-tool did not run"
log "verified: idiom-tool list --profile discovery"
