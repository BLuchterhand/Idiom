#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'USAGE'
Usage: scripts/install-snapshot-from-dist.sh [install.sh args...]

Installs idiom from a local GoReleaser dist directory by serving the snapshot
assets over a loopback HTTP server and exercising install.sh. Arguments are
forwarded to install.sh. With no arguments, the helper preserves the historical
compatibility smoke and also runs install-tools.sh.

Environment:
  IDIOM_TEST_DIST     dist directory containing archives and SHA256SUMS
                      (default: dist)
  IDIOM_INSTALL_DIR   destination directory (required)
  IDIOM_TEST_GOOS     target OS override: darwin or linux (optional)
  IDIOM_TEST_GOARCH   target arch override: amd64 or arm64 (optional)
USAGE
}

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi

dist="${IDIOM_TEST_DIST:-dist}"
install_dir="${IDIOM_INSTALL_DIR:-}"

if [[ -z "$install_dir" ]]; then
  echo "IDIOM_INSTALL_DIR is required" >&2
  exit 2
fi
if [[ ! -d "$dist" ]]; then
  echo "dist directory does not exist: $dist" >&2
  exit 1
fi

detect_goos() {
  if [[ -n "${IDIOM_TEST_GOOS:-}" ]]; then
    printf '%s' "$IDIOM_TEST_GOOS"
    return
  fi
  case "$(uname -s)" in
    Darwin) printf 'darwin' ;;
    Linux) printf 'linux' ;;
    *) echo "unsupported OS for snapshot install smoke: $(uname -s)" >&2; exit 1 ;;
  esac
}

detect_goarch() {
  if [[ -n "${IDIOM_TEST_GOARCH:-}" ]]; then
    printf '%s' "$IDIOM_TEST_GOARCH"
    return
  fi
  case "$(uname -m)" in
    x86_64|amd64) printf 'amd64' ;;
    arm64|aarch64) printf 'arm64' ;;
    *) echo "unsupported arch for snapshot install smoke: $(uname -m)" >&2; exit 1 ;;
  esac
}

goos="$(detect_goos)"
goarch="$(detect_goarch)"

archive_count="$(find "$dist" -maxdepth 1 -type f -name "idiom_*_${goos}_${goarch}.tar.gz" | wc -l | tr -d ' ')"
if [[ "$archive_count" != "1" ]]; then
  echo "expected exactly one ${goos}/${goarch} archive in $dist, found ${archive_count}" >&2
  find "$dist" -maxdepth 1 -type f -name "idiom_*_${goos}_${goarch}.tar.gz" -print >&2
  exit 1
fi

archive="$(find "$dist" -maxdepth 1 -type f -name "idiom_*_${goos}_${goarch}.tar.gz" -print)"
asset="$(basename "$archive")"
version="${asset#idiom_}"
version="${version%_${goos}_${goarch}.tar.gz}"

python3 -m http.server 8080 --bind 127.0.0.1 --directory "$dist" >"${RUNNER_TEMP:-/tmp}/idiom-release-server.log" 2>&1 &
server_pid=$!
trap 'kill "${server_pid}" 2>/dev/null || true' EXIT
for _ in 1 2 3 4 5; do
  curl -fsSL http://127.0.0.1:8080/SHA256SUMS >/dev/null && break
  sleep 0.5
done

IDIOM_VERSION="$version" \
IDIOM_RELEASE_URL_BASE="http://127.0.0.1:8080" \
IDIOM_INSTALL_DIR="$install_dir" \
sh ./install.sh "$@"

if [[ "$#" -eq 0 ]]; then
  IDIOM_VERSION="$version" \
  IDIOM_RELEASE_URL_BASE="http://127.0.0.1:8080" \
  IDIOM_INSTALL_DIR="$install_dir" \
  sh ./install-tools.sh
fi

"$install_dir/idiom" version | grep -qF "version: ${version}"
if [[ -x "$install_dir/idiom-tool" ]]; then
  "$install_dir/idiom-tool" list --profile discovery >/dev/null
fi

echo "installed idiom snapshot ${version} for ${goos}/${goarch} into ${install_dir}"
