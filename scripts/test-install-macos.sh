#!/usr/bin/env bash
# macOS integration test for install.sh.
#
# This mirrors the release install path on a clean per-test HOME:
# package or select a darwin release archive, serve it over loopback HTTP,
# install with install.sh into a temp-controlled destination, verify global
# config bootstrap, run the post-install mocked harness smoke, then verify the
# tools profile does not replace the installed idiom binary.

set -euo pipefail

repo_root=$(cd "$(dirname "$0")/.." && pwd)

if [[ "$(uname -s)" != "Darwin" ]]; then
  echo "error: scripts/test-install-macos.sh must run on macOS" >&2
  exit 1
fi

detect_goarch() {
  if [[ -n "${IDIOM_TEST_GOARCH:-}" ]]; then
    printf '%s' "$IDIOM_TEST_GOARCH"
    return
  fi
  case "$(uname -m)" in
    x86_64|amd64) printf 'amd64' ;;
    arm64|aarch64) printf 'arm64' ;;
    *) echo "error: unsupported macOS arch: $(uname -m)" >&2; exit 1 ;;
  esac
}

sha256_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

free_port() {
  python3 - <<'PY'
import socket
with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
    sock.bind(("127.0.0.1", 0))
    print(sock.getsockname()[1])
PY
}

GOARCH=$(detect_goarch)
VERSION="${IDIOM_TEST_VERSION:-0.0.0-macos-test}"
ASSET="idiom_${VERSION#v}_darwin_${GOARCH}.tar.gz"

work_dir=$(mktemp -d)
install_dir="${IDIOM_INSTALL_DIR:-${work_dir}/bin}"
home_dir="${IDIOM_TEST_HOME:-${work_dir}/home}"
dist="${work_dir}/dist"
mkdir -p "$dist" "$install_dir" "$home_dir"
trap 'rm -rf "$work_dir"; kill "${server_pid:-}" 2>/dev/null || true' EXIT

if [[ -n "${IDIOM_TEST_DIST:-}" ]]; then
  source_dist="${IDIOM_TEST_DIST}"
  if [[ ! -d "$source_dist" ]]; then
    echo "error: IDIOM_TEST_DIST is not a directory: ${source_dist}" >&2
    exit 1
  fi

  if [[ -z "${IDIOM_TEST_VERSION:-}" ]]; then
    asset_count=$(find "$source_dist" -maxdepth 1 -type f -name "idiom_*_darwin_${GOARCH}.tar.gz" | wc -l | tr -d ' ')
    if [[ "$asset_count" != "1" ]]; then
      echo "error: expected exactly one darwin/${GOARCH} archive in ${source_dist}, found ${asset_count}" >&2
      find "$source_dist" -maxdepth 1 -type f -name "idiom_*_darwin_${GOARCH}.tar.gz" -print >&2
      exit 1
    fi
    source_asset=$(find "$source_dist" -maxdepth 1 -type f -name "idiom_*_darwin_${GOARCH}.tar.gz" -print)
    ASSET=$(basename "$source_asset")
    VERSION="${ASSET#idiom_}"
    VERSION="${VERSION%_darwin_${GOARCH}.tar.gz}"
  else
    source_asset="${source_dist}/${ASSET}"
  fi

  if [[ ! -f "$source_asset" ]]; then
    echo "error: snapshot archive not found: ${source_asset}" >&2
    exit 1
  fi
  if [[ ! -f "${source_dist}/SHA256SUMS" ]]; then
    echo "error: SHA256SUMS not found in ${source_dist}" >&2
    exit 1
  fi
  if ! awk -v want="$(basename "$source_asset")" '$2 == want {found = 1} END {exit found ? 0 : 1}' "${source_dist}/SHA256SUMS"; then
    echo "error: $(basename "$source_asset") not listed in ${source_dist}/SHA256SUMS" >&2
    exit 1
  fi

  echo "==> staging prebuilt macOS snapshot ${ASSET}"
  cp "$source_asset" "${dist}/${ASSET}"
  cp "${source_dist}/SHA256SUMS" "${dist}/SHA256SUMS"
else
  echo "==> building idiom for darwin/${GOARCH} (version=${VERSION})"
  (
    cd "$repo_root"
    GOOS=darwin GOARCH="$GOARCH" CGO_ENABLED=0 go build \
      -ldflags "-s -w -X main.version=${VERSION} -X main.commit=macos-test -X main.date=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
      -o "${dist}/idiom" ./cmd/idiom
  )

  echo "==> packaging ${ASSET}"
  (cd "$dist" && tar -czf "$ASSET" idiom && rm idiom)
  (cd "$dist" && printf '%s  %s\n' "$(sha256_file "$ASSET")" "$ASSET" > SHA256SUMS)
fi

port=$(free_port)
echo "==> starting release http.server on 127.0.0.1:${port}"
python3 -m http.server "$port" --bind 127.0.0.1 --directory "$dist" >"${work_dir}/release-server.log" 2>&1 &
server_pid=$!
for _ in 1 2 3 4 5 6 7 8 9 10; do
  if curl -fsSL "http://127.0.0.1:${port}/SHA256SUMS" >/dev/null 2>&1; then
    break
  fi
  sleep 0.5
done
curl -fsSL "http://127.0.0.1:${port}/SHA256SUMS" >/dev/null

echo "==> installing idiom ${VERSION} into ${install_dir}"
HOME="$home_dir" USERPROFILE="$home_dir" \
IDIOM_VERSION="$VERSION" \
IDIOM_RELEASE_URL_BASE="http://127.0.0.1:${port}" \
IDIOM_INSTALL_DIR="$install_dir" \
sh "$repo_root/install.sh"

"${install_dir}/idiom" version | grep -qF "version: ${VERSION}"
HOME="$home_dir" USERPROFILE="$home_dir" IDIOM_BIN="${install_dir}/idiom" \
  python3 "$repo_root/scripts/test-post-install-harness.py"

before_hash=$(shasum -a 256 "${install_dir}/idiom" | awk '{print $1}')
HOME="$home_dir" USERPROFILE="$home_dir" \
IDIOM_VERSION="$VERSION" \
IDIOM_RELEASE_URL_BASE="http://127.0.0.1:${port}" \
IDIOM_INSTALL_DIR="$install_dir" \
sh "$repo_root/install-tools.sh"
after_hash=$(shasum -a 256 "${install_dir}/idiom" | awk '{print $1}')
if [[ "$before_hash" != "$after_hash" ]]; then
  echo "error: install-tools.sh changed existing idiom binary" >&2
  exit 1
fi
"${install_dir}/idiom-tool" list --profile discovery >/dev/null

echo "==> macOS install test passed for darwin/${GOARCH}"
