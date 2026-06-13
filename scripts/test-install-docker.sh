#!/usr/bin/env bash
# Docker integration test for install.sh — Phase 9 (distribution).
#
# Verifies the install path end-to-end against a clean Ubuntu container:
# cross-builds idiom for the docker arch, packages it in goreleaser
# archive format, serves the archive + SHA256SUMS via a local
# http.server inside the container, runs install.sh with
# IDIOM_RELEASE_URL_BASE pointing at the local server, and asserts the
# installed binary runs (`idiom version` matches the injected version,
# `idiom doctor --json` produces parseable output).
#
# Why Docker — the test needs a clean filesystem with no idiom binary,
# no goreleaser-installed deps, no project .idiom/ config: the same
# conditions a fresh user hits. Linux/Ubuntu was picked because Docker
# Desktop on macOS runs Linux containers natively (matching the
# install.sh detect_os Linux branch); macOS host install path is
# covered separately by the smoke test in `idiom doctor`.
#
# Usage:
#   ./scripts/test-install-docker.sh
#   IDIOM_TEST_VERSION=0.5.0-rc1 ./scripts/test-install-docker.sh
#   IDIOM_TEST_DIST=dist IDIOM_TEST_GOARCH=arm64 ./scripts/test-install-docker.sh
#
# Exit codes:
#   0 — install + version + doctor verified
#   non-0 — any step failed (build, package, docker run, in-container
#           assertion); error message names the step

set -euo pipefail

repo_root=$(cd "$(dirname "$0")/.." && pwd)
script_dir="${repo_root}/scripts"

if ! command -v docker >/dev/null 2>&1; then
  echo "error: docker not on PATH" >&2
  echo "       install Docker Desktop or the docker CLI, then re-run" >&2
  exit 1
fi

if ! docker info >/dev/null 2>&1; then
  echo "error: docker daemon not reachable" >&2
  echo "       start Docker Desktop (or your local docker daemon) and re-run" >&2
  exit 1
fi

# Detect the docker daemon's arch so we install a matching binary.
# Docker Desktop on Apple Silicon runs linux/arm64 natively; linux/amd64
# hosts get linux/amd64. Goreleaser uses these names too, so the asset
# filename install.sh expects matches. CI may pass IDIOM_TEST_GOARCH to
# assert that the native runner is testing the intended archive.
docker_arch=$(docker info --format '{{.Architecture}}' 2>/dev/null || true)
case "${docker_arch}" in
  aarch64|arm64) GOARCH=arm64 ;;
  x86_64|amd64)  GOARCH=amd64 ;;
  *) echo "error: unsupported docker arch: '${docker_arch}'" >&2; exit 1 ;;
esac

if [ -n "${IDIOM_TEST_GOARCH:-}" ] && [ "${IDIOM_TEST_GOARCH}" != "${GOARCH}" ]; then
  echo "error: docker arch ${GOARCH} does not match requested IDIOM_TEST_GOARCH=${IDIOM_TEST_GOARCH}" >&2
  exit 1
fi

dist=$(mktemp -d)
trap 'rm -rf "${dist}"' EXIT

if [ -n "${IDIOM_TEST_DIST:-}" ]; then
  source_dist="${IDIOM_TEST_DIST}"
  if [ ! -d "${source_dist}" ]; then
    echo "error: IDIOM_TEST_DIST is not a directory: ${source_dist}" >&2
    exit 1
  fi

  if [ -n "${IDIOM_TEST_VERSION:-}" ]; then
    VERSION="${IDIOM_TEST_VERSION}"
    ASSET="idiom_${VERSION#v}_linux_${GOARCH}.tar.gz"
  else
    asset_count=$(find "${source_dist}" -maxdepth 1 -type f -name "idiom_*_linux_${GOARCH}.tar.gz" | wc -l | tr -d ' ')
    if [ "${asset_count}" != "1" ]; then
      echo "error: expected exactly one linux/${GOARCH} archive in ${source_dist}, found ${asset_count}" >&2
      find "${source_dist}" -maxdepth 1 -type f -name "idiom_*_linux_${GOARCH}.tar.gz" -print >&2
      exit 1
    fi
    asset_path=$(find "${source_dist}" -maxdepth 1 -type f -name "idiom_*_linux_${GOARCH}.tar.gz" -print)
    ASSET=$(basename "${asset_path}")
    VERSION="${ASSET#idiom_}"
    VERSION="${VERSION%_linux_${GOARCH}.tar.gz}"
  fi

  if [ ! -f "${source_dist}/${ASSET}" ]; then
    echo "error: snapshot archive not found: ${source_dist}/${ASSET}" >&2
    exit 1
  fi
  if [ ! -f "${source_dist}/SHA256SUMS" ]; then
    echo "error: SHA256SUMS not found in ${source_dist}" >&2
    exit 1
  fi
  if ! awk -v want="${ASSET}" '$2 == want {found = 1} END {exit found ? 0 : 1}' "${source_dist}/SHA256SUMS"; then
    echo "error: ${ASSET} not listed in ${source_dist}/SHA256SUMS" >&2
    exit 1
  fi

  echo "==> staging prebuilt snapshot ${ASSET} from ${source_dist}"
  cp "${source_dist}/${ASSET}" "${dist}/${ASSET}"
  cp "${source_dist}/SHA256SUMS" "${dist}/SHA256SUMS"
else
  VERSION="${IDIOM_TEST_VERSION:-0.0.0-docker-test}"
  ASSET="idiom_${VERSION#v}_linux_${GOARCH}.tar.gz"

  echo "==> cross-building idiom for linux/${GOARCH} (version=${VERSION})"
  (
    cd "${repo_root}"
    GOOS=linux GOARCH="${GOARCH}" CGO_ENABLED=0 go build \
      -ldflags "-s -w -X main.version=${VERSION} -X main.commit=docker-test -X main.date=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
      -o "${dist}/idiom" ./cmd/idiom
  )

  echo "==> packaging ${ASSET}"
  (cd "${dist}" && tar -czf "${ASSET}" idiom && rm idiom)

  echo "==> generating SHA256SUMS"
  if command -v sha256sum >/dev/null 2>&1; then
    (cd "${dist}" && sha256sum "${ASSET}" > SHA256SUMS)
  else
    (cd "${dist}" && shasum -a 256 "${ASSET}" > SHA256SUMS)
  fi
fi

echo "==> running docker container (ubuntu:24.04, ${GOARCH})"
docker run --rm \
  -e VERSION="${VERSION}" \
  -v "${script_dir}/test-install-entrypoint.sh:/entrypoint.sh:ro" \
  -v "${repo_root}/install.sh:/install.sh:ro" \
  -v "${repo_root}/install-tools.sh:/install-tools.sh:ro" \
  -v "${dist}:/dist:ro" \
  ubuntu:24.04 \
  bash /entrypoint.sh

echo "==> docker install test passed"
