#!/bin/sh
# install.sh — Phase 9 (distribution).
#
# Downloads the latest idiom release asset matching the host's OS/arch
# from GitHub Releases, verifies the SHA256 checksum, extracts the
# binary, and installs it into a directory on $PATH.
#
# Usage:
#   curl -fsSL https://idiom.sh/install.sh | sh
#
# Override knobs (env vars):
#   IDIOM_VERSION           pin a specific version (default: latest)
#   IDIOM_INSTALL_DIR       destination dir (default: ~/.local/bin, or
#                           /usr/local/bin if writable without sudo)
#   IDIOM_REPO              override the GitHub repo (default: BLuchterhand/idiom)
#   IDIOM_RELEASE_URL_BASE  override the URL prefix the asset and
#                           SHA256SUMS download from. Used by the
#                           Docker install test (scripts/test-install-docker.sh)
#                           to point at a local http.server hosting a
#                           snapshot tarball; production callers leave
#                           it unset and the script builds the URL from
#                           https://github.com/$REPO/releases/download/$VERSION.
#
# The script is POSIX sh — no bashisms. Tested with sh, dash, bash, zsh.

set -eu

REPO="${IDIOM_REPO:-BLuchterhand/idiom}"
VERSION="${IDIOM_VERSION:-}"
INSTALL_DIR="${IDIOM_INSTALL_DIR:-}"
RELEASE_URL_BASE="${IDIOM_RELEASE_URL_BASE:-}"
# Strip a trailing slash so URL joins like "${BASE}/${asset}" don't
# produce "//asset" — most servers tolerate it, but the doubled slash
# can break checksum-file matching on stricter HTTP servers and looks
# wrong in the install logs.
RELEASE_URL_BASE="${RELEASE_URL_BASE%/}"

log() { printf '%s\n' "$*" >&2; }
die() { log "error: $*"; exit 1; }

require() {
  command -v "$1" >/dev/null 2>&1 || die "required command not found: $1"
}

require curl
require tar
require uname

# Detect host OS in goreleaser's naming.
detect_os() {
  uname_out=$(uname -s)
  case "$uname_out" in
    Darwin) printf 'darwin' ;;
    Linux)  printf 'linux'  ;;
    MINGW*|MSYS*|CYGWIN*)
      die "windows install via this script is not supported; download the .zip from https://github.com/${REPO}/releases manually"
      ;;
    *) die "unsupported OS: $uname_out" ;;
  esac
}

# Detect host arch in goreleaser's naming.
detect_arch() {
  uname_out=$(uname -m)
  case "$uname_out" in
    x86_64|amd64) printf 'amd64' ;;
    arm64|aarch64) printf 'arm64' ;;
    *) die "unsupported arch: $uname_out (idiom ships amd64 and arm64 only)" ;;
  esac
}

# Resolve VERSION to the latest release tag if not pinned. Uses the
# GitHub API; falls back to parsing the redirect from /releases/latest
# when the API rate limit is hit.
resolve_version() {
  if [ -n "$VERSION" ]; then
    printf '%s' "$VERSION"
    return
  fi
  api="https://api.github.com/repos/${REPO}/releases/latest"
  tag=$(curl -fsSL "$api" 2>/dev/null | grep -o '"tag_name":\s*"[^"]*"' | head -1 | sed -e 's/.*:\s*"\(.*\)"/\1/')
  if [ -z "$tag" ]; then
    # API failed (rate limit?); fall back to following the redirect.
    redirect="https://github.com/${REPO}/releases/latest"
    tag=$(curl -fsSI "$redirect" | awk -F': ' 'tolower($1)=="location"{print $2}' | sed -e 's|.*/tag/||' -e 's|[\r\n]||g' | head -1)
  fi
  [ -n "$tag" ] || die "could not resolve latest release tag from GitHub"
  printf '%s' "$tag"
}

# Pick the install dir. Order:
#   1. $IDIOM_INSTALL_DIR if set
#   2. /usr/local/bin if writable without sudo
#   3. ~/.local/bin (creating it if needed)
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

OS=$(detect_os)
ARCH=$(detect_arch)
VERSION=$(resolve_version)
INSTALL_DIR=$(pick_install_dir)

# Strip leading 'v' for the asset filename (goreleaser archive name
# uses {Version} which is the tag minus the v prefix).
asset_version=${VERSION#v}
asset="idiom_${asset_version}_${OS}_${ARCH}.tar.gz"
if [ -n "$RELEASE_URL_BASE" ]; then
  url="${RELEASE_URL_BASE}/${asset}"
  sums_url="${RELEASE_URL_BASE}/SHA256SUMS"
else
  url="https://github.com/${REPO}/releases/download/${VERSION}/${asset}"
  sums_url="https://github.com/${REPO}/releases/download/${VERSION}/SHA256SUMS"
fi

log "downloading idiom ${VERSION} for ${OS}/${ARCH}"
log "  asset:  ${url}"
log "  install: ${INSTALL_DIR}/idiom"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

curl -fsSL -o "${tmp}/${asset}" "$url" || die "download failed: $url"
curl -fsSL -o "${tmp}/SHA256SUMS" "$sums_url" || die "checksum file download failed: $sums_url"

# Verify the asset's SHA256 against the SHA256SUMS file. Use shasum on
# darwin (where sha256sum isn't always present) and sha256sum on linux.
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

# Extract.
tar -xzf "${tmp}/${asset}" -C "$tmp"
[ -f "${tmp}/idiom" ] || die "archive did not contain an 'idiom' binary"

# Install.
mkdir -p "$INSTALL_DIR" || die "cannot create install dir: ${INSTALL_DIR}"
mv "${tmp}/idiom" "${INSTALL_DIR}/idiom" || die "cannot move binary into ${INSTALL_DIR} (try IDIOM_INSTALL_DIR=\$HOME/.local/bin)"
chmod 755 "${INSTALL_DIR}/idiom"

log "installed: ${INSTALL_DIR}/idiom"

# PATH check — warn if the install dir isn't on PATH so the user
# doesn't get a confusing "command not found" on the next line.
case ":${PATH}:" in
  *":${INSTALL_DIR}:"*) ;;
  *)
    log ""
    log "warning: ${INSTALL_DIR} is not on your PATH"
    log "add it to your shell profile (e.g. ~/.zshrc or ~/.bashrc):"
    log "  export PATH=\"${INSTALL_DIR}:\$PATH\""
    ;;
esac

# Print version so the user sees the install worked.
"${INSTALL_DIR}/idiom" version || die "install verification failed: ${INSTALL_DIR}/idiom did not run"
