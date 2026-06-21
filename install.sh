#!/bin/sh
# install.sh - Unix installer for idiom release archives.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/BLuchterhand/idiom/main/install.sh | sh
#   curl -fsSL https://raw.githubusercontent.com/BLuchterhand/idiom/main/install.sh | sh -s -- --profile tools
#
# Profiles:
#   cli             install the idiom CLI only (default; current public footprint)
#   tools           install idiom-tool and idiom-mcp-discovery only
#   full            install idiom, tool launchers, and all discovery dependencies
#   discovery-deps  install idiom and discovery dependencies
#   doctor-only     install idiom, verify version, then run doctor diagnostics
#
# Override knobs:
#   IDIOM_VERSION           pin a specific version (default: latest)
#   IDIOM_INSTALL_DIR       destination dir (default: ~/.local/bin, or
#                           /usr/local/bin if writable without sudo)
#   IDIOM_REPO              GitHub repo (default: BLuchterhand/idiom)
#   IDIOM_RELEASE_URL_BASE  URL prefix for assets and SHA256SUMS
#   IDIOM_INSTALL_SCRIPT_REF Git ref used when this script must fetch helper
#                            installers (default: release tag, or main)
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
INSTALL_DEPS=false
DEPS_ALL=false
DEPS_DISABLED=false
DEPS_NO_QUEUE=false
DEPS_NO_AUDIT=false
HELPER_REF="${IDIOM_INSTALL_SCRIPT_REF:-}"

log() { printf '%s\n' "$*" >&2; }
die() { log "error: $*"; exit 1; }

usage() {
  cat >&2 <<'USAGE'
Usage: install.sh [--profile PROFILE]

Profiles:
  cli             install the idiom CLI only (default)
  tools           install idiom-tool and idiom-mcp-discovery only
  full            install idiom, tool launchers, and all discovery dependencies
  discovery-deps  install idiom and discovery dependencies
  doctor-only     install idiom, verify version, run doctor diagnostics

Dependency flags:
  --with-deps     install the default discovery dependency set
  --all-deps      install the full discovery dependency set for all languages
  --no-deps       do not install discovery dependencies, even for full profile
  --no-queue      skip queue daemon setup when dependency setup runs
  --no-audit      skip dependency-script harness audit after dependency setup
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
      --with-deps)
        INSTALL_DEPS=true
        DEPS_DISABLED=false
        shift
        ;;
      --all-deps|--all)
        INSTALL_DEPS=true
        DEPS_ALL=true
        DEPS_DISABLED=false
        shift
        ;;
      --no-deps)
        INSTALL_DEPS=false
        DEPS_ALL=false
        DEPS_DISABLED=true
        shift
        ;;
      --no-queue)
        DEPS_NO_QUEUE=true
        shift
        ;;
      --no-audit)
        DEPS_NO_AUDIT=true
        shift
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

dependency_helper_ref() {
  if [ -n "$HELPER_REF" ]; then
    printf '%s' "$HELPER_REF"
    return
  fi
  case "$VERSION" in
    v*) printf '%s' "$VERSION" ;;
    *) printf 'main' ;;
  esac
}

resolve_dependency_installer() {
  if [ -f "./scripts/install-requirements.sh" ]; then
    printf '%s' "./scripts/install-requirements.sh"
    return
  fi

  ref=$(dependency_helper_ref)
  helper_url="https://raw.githubusercontent.com/${REPO}/${ref}/scripts/install-requirements.sh"
  helper_path="${tmp}/install-requirements.sh"
  curl -fsSL -o "$helper_path" "$helper_url" || die "could not fetch dependency installer: ${helper_url}"
  chmod 755 "$helper_path"
  printf '%s' "$helper_path"
}

install_discovery_dependencies() {
  require bash
  helper=$(resolve_dependency_installer)
  log "installing discovery dependencies with ${helper}"

  set -- --no-build --no-queue
  if $DEPS_NO_AUDIT; then
    set -- "$@" --no-audit
  fi
  if $DEPS_ALL; then
    set -- --all "$@"
  fi
  if ! $DEPS_NO_QUEUE; then
    # Release installs are binary-first and may run without a source checkout;
    # queue setup is source-specific, so leave it to source installs.
    :
  fi
  IDIOM_BIN="${INSTALL_DIR}/idiom" bash "$helper" "$@"
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

initialize_global_config() {
  bin="$1"
  "$bin" init --global --merge >/dev/null || die "global config initialization failed"
  log "verified: global config initialized under ~/.idiom"
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

case "$PROFILE" in
  full)
    if ! $DEPS_DISABLED; then
      INSTALL_DEPS=true
      DEPS_ALL=true
    fi
    ;;
  discovery-deps)
    if ! $DEPS_DISABLED; then
      INSTALL_DEPS=true
    fi
    ;;
esac

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
    initialize_global_config "${INSTALL_DIR}/idiom"
    ;;
  tools)
    install_tools_binary "${tmp}/idiom" "idiom-tool-bin"
    verify_tools
    initialize_global_config "${INSTALL_DIR}/idiom-tool-bin"
    ;;
  full)
    install_cli_binary "${tmp}/idiom"
    install_tools_binary "${INSTALL_DIR}/idiom" "idiom"
    verify_version "${INSTALL_DIR}/idiom"
    verify_tools
    initialize_global_config "${INSTALL_DIR}/idiom"
    ;;
  discovery-deps)
    install_cli_binary "${tmp}/idiom"
    verify_version "${INSTALL_DIR}/idiom"
    initialize_global_config "${INSTALL_DIR}/idiom"
    ;;
  doctor-only)
    install_cli_binary "${tmp}/idiom"
    verify_version "${INSTALL_DIR}/idiom"
    initialize_global_config "${INSTALL_DIR}/idiom"
    verify_doctor_json
    ;;
esac

if $INSTALL_DEPS; then
  install_discovery_dependencies
fi

warn_path
