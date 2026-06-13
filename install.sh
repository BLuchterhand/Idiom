#!/bin/sh
# install.sh - Unix installer for idiom release archives.
#
# Usage:
#   curl -fsSL https://idiom.sh/install.sh | sh
#   curl -fsSL https://idiom.sh/install.sh | sh -s -- --profile tools
#
# Profiles:
#   cli             install the idiom CLI only (default; current public footprint)
#   tools           install idiom-tool and idiom-mcp-discovery only
#   full            install idiom plus tool launchers; dependency setup is delegated
#   discovery-deps  install idiom and print dependency setup guidance
#   doctor-only     install idiom, verify version, then run doctor diagnostics
#
# Override knobs:
#   IDIOM_VERSION           pin a specific version (default: latest)
#   IDIOM_INSTALL_DIR       destination dir (default: ~/.local/bin, or
#                           /usr/local/bin if writable without sudo)
#   IDIOM_REPO              GitHub repo (default: BLuchterhand/idiom)
#   IDIOM_RELEASE_URL_BASE  URL prefix for assets and SHA256SUMS
#
# The script is POSIX sh. Keep it self-contained so curl-piped installs
# do not depend on a second helper fetch.

set -eu

REPO="${IDIOM_REPO:-BLuchterhand/idiom}"
VERSION="${IDIOM_VERSION:-}"
INSTALL_DIR="${IDIOM_INSTALL_DIR:-}"
RELEASE_URL_BASE="${IDIOM_RELEASE_URL_BASE:-}"
RELEASE_URL_BASE="${RELEASE_URL_BASE%/}"
PROFILE="cli"

log() { printf '%s\n' "$*" >&2; }
die() { log "error: $*"; exit 1; }

usage() {
  cat >&2 <<'USAGE'
Usage: install.sh [--profile PROFILE]

Profiles:
  cli             install the idiom CLI only (default)
  tools           install idiom-tool and idiom-mcp-discovery only
  full            install idiom plus tool launchers
  discovery-deps  install idiom and print dependency setup guidance
  doctor-only     install idiom, verify version, run doctor diagnostics
USAGE
}

require() {
  command -v "$1" >/dev/null 2>&1 || die "required command not found: $1"
}

normalize_profile() {
  case "$1" in
    cli|tools|full|discovery-deps|doctor-only) printf '%s' "$1" ;;
    doctor) printf 'doctor-only' ;;
    discovery|deps) printf 'discovery-deps' ;;
    *) die "unknown install profile: $1 (expected cli, tools, full, discovery-deps, or doctor-only)" ;;
  esac
}

parse_args() {
  while [ "$#" -gt 0 ]; do
    case "$1" in
      --profile|-p)
        [ "$#" -ge 2 ] || die "$1 requires a profile value"
        PROFILE=$(normalize_profile "$2")
        shift 2
        ;;
      --profile=*)
        value=${1#--profile=}
        [ -n "$value" ] || die "--profile requires a profile value"
        PROFILE=$(normalize_profile "$value")
        shift
        ;;
      --help|-h)
        usage
        exit 0
        ;;
      *)
        die "unknown argument: $1"
        ;;
    esac
  done
}

detect_os() {
  uname_out=$(uname -s)
  case "$uname_out" in
    Darwin) printf 'darwin' ;;
    Linux)  printf 'linux' ;;
    MINGW*|MSYS*|CYGWIN*)
      die "windows install via this script is not supported; download the Windows asset from https://github.com/${REPO}/releases"
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
  tag=$(curl -fsSL "$api" 2>/dev/null | grep -o '"tag_name":[[:space:]]*"[^"]*"' | head -1 | sed -e 's/.*:[[:space:]]*"\(.*\)"/\1/')
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

sha256_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  else
    die "no sha256 tool found (need sha256sum or shasum)"
  fi
}

verify_checksum() {
  file="$1"
  asset="$2"
  sums="$3"

  hash=$(sha256_file "$file")
  expected=$(awk -v want="$asset" '$2 == want {print $1}' "$sums")
  [ -n "$expected" ] || die "${asset} not listed in SHA256SUMS"
  [ "$hash" = "$expected" ] || die "checksum mismatch (got ${hash}, expected ${expected})"
}

write_tool_launcher() {
  path="$1"
  target="$2"
  mode="$3"

  case "$mode" in
    tool)
      cat > "$path" <<SH
#!/bin/sh
DIR=\$(CDPATH= cd -- "\$(dirname -- "\$0")" && pwd)
exec "\$DIR/${target}" tool "\$@"
SH
      ;;
    mcp)
      cat > "$path" <<SH
#!/bin/sh
DIR=\$(CDPATH= cd -- "\$(dirname -- "\$0")" && pwd)
exec "\$DIR/${target}" mcp serve --stdio --profile discovery "\$@"
SH
      ;;
    *) die "unknown launcher mode: $mode" ;;
  esac
  chmod 755 "$path"
}

warn_path() {
  case ":${PATH}:" in
    *":${INSTALL_DIR}:"*) ;;
    *)
      log ""
      log "warning: ${INSTALL_DIR} is not on your PATH"
      log "add it to your shell profile:"
      log "  export PATH=\"${INSTALL_DIR}:\$PATH\""
      ;;
  esac
}

print_dependency_delegation_note() {
  log ""
  log "dependency setup is delegated to idiom doctor and the release dependency manifest track"
  log "run: ${INSTALL_DIR}/idiom doctor"
}

install_cli_binary() {
  src="$1"
  mkdir -p "$INSTALL_DIR" || die "cannot create install dir: ${INSTALL_DIR}"
  mv "$src" "${INSTALL_DIR}/idiom" || die "cannot move binary into ${INSTALL_DIR} (try IDIOM_INSTALL_DIR=\$HOME/.local/bin)"
  chmod 755 "${INSTALL_DIR}/idiom"
  log "installed: ${INSTALL_DIR}/idiom"
}

install_tools_binary() {
  src="$1"
  target="$2"

  mkdir -p "$INSTALL_DIR" || die "cannot create install dir: ${INSTALL_DIR}"
  if [ "$target" = "idiom-tool-bin" ]; then
    mv "$src" "${INSTALL_DIR}/idiom-tool-bin" || die "cannot install binary into ${INSTALL_DIR}"
    chmod 755 "${INSTALL_DIR}/idiom-tool-bin"
    write_tool_launcher "${INSTALL_DIR}/idiom-tool" "idiom-tool-bin" tool
    write_tool_launcher "${INSTALL_DIR}/idiom-mcp-discovery" "idiom-tool-bin" mcp
  else
    write_tool_launcher "${INSTALL_DIR}/idiom-tool" "$target" tool
    write_tool_launcher "${INSTALL_DIR}/idiom-mcp-discovery" "$target" mcp
  fi
  log "installed: ${INSTALL_DIR}/idiom-tool"
  log "installed: ${INSTALL_DIR}/idiom-mcp-discovery"
}

verify_version() {
  bin="$1"
  "$bin" version || die "install verification failed: ${bin} did not run"
}

verify_tools() {
  "${INSTALL_DIR}/idiom-tool" list --profile discovery >/dev/null || die "install verification failed: idiom-tool did not run"
  log "verified: idiom-tool list --profile discovery"
}

verify_doctor_json() {
  doctor_out="${tmp}/doctor.json"
  doctor_err="${tmp}/doctor.err"

  set +e
  "${INSTALL_DIR}/idiom" doctor --json >"$doctor_out" 2>"$doctor_err"
  doctor_status=$?
  set -e

  [ -s "$doctor_out" ] || {
    [ -s "$doctor_err" ] && cat "$doctor_err" >&2
    die "doctor verification failed: no JSON output"
  }
  grep '"checks"' "$doctor_out" >/dev/null 2>&1 || die "doctor verification failed: JSON output missing checks"
  grep '"summary"' "$doctor_out" >/dev/null 2>&1 || die "doctor verification failed: JSON output missing summary"
  log "doctor diagnostics completed with status ${doctor_status}"
}

parse_args "$@"
require curl
require tar
require uname

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

log "downloading idiom ${VERSION} for ${OS}/${ARCH} (${PROFILE} profile)"
log "  asset:  ${url}"
log "  install: ${INSTALL_DIR}"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

curl -fsSL -o "${tmp}/${asset}" "$url" || die "download failed: $url"
curl -fsSL -o "${tmp}/SHA256SUMS" "$sums_url" || die "checksum file download failed: $sums_url"

log "verifying SHA256 checksum"
verify_checksum "${tmp}/${asset}" "$asset" "${tmp}/SHA256SUMS"

tar -xzf "${tmp}/${asset}" -C "$tmp"
[ -f "${tmp}/idiom" ] || die "archive did not contain an 'idiom' binary"

case "$PROFILE" in
  cli)
    install_cli_binary "${tmp}/idiom"
    verify_version "${INSTALL_DIR}/idiom"
    ;;
  tools)
    install_tools_binary "${tmp}/idiom" "idiom-tool-bin"
    verify_tools
    ;;
  full)
    install_cli_binary "${tmp}/idiom"
    install_tools_binary "${INSTALL_DIR}/idiom" "idiom"
    verify_version "${INSTALL_DIR}/idiom"
    verify_tools
    print_dependency_delegation_note
    ;;
  discovery-deps)
    install_cli_binary "${tmp}/idiom"
    verify_version "${INSTALL_DIR}/idiom"
    print_dependency_delegation_note
    ;;
  doctor-only)
    install_cli_binary "${tmp}/idiom"
    verify_version "${INSTALL_DIR}/idiom"
    verify_doctor_json
    ;;
esac

warn_path
