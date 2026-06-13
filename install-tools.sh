#!/bin/sh
# Compatibility installer for Idiom's externally callable tool sidecar.
#
# Prefer:
#   curl -fsSL https://raw.githubusercontent.com/BLuchterhand/idiom/main/install.sh | sh -s -- --profile tools
#
# This wrapper preserves the older install-tools.sh entrypoint while routing
# through the consolidated Unix installer. The tools profile never exposes
# ${INSTALL_DIR}/idiom; it installs idiom-tool-bin plus:
#   exec "$DIR/idiom-tool-bin" tool "$@"
#   exec "$DIR/idiom-tool-bin" mcp serve --stdio --profile discovery "$@"
# Verification still runs: idiom-tool list --profile discovery.
# Windows users should use the Windows installer instead.

set -eu

REPO="${IDIOM_REPO:-BLuchterhand/idiom}"
VERSION="${IDIOM_VERSION:-}"

die() {
  printf '%s\n' "error: $*" >&2
  exit 1
}

case "$0" in
  */install-tools.sh|install-tools.sh) running_from_file=true ;;
  *) running_from_file=false ;;
esac

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
if $running_from_file && [ -f "${script_dir}/install.sh" ]; then
  exec sh "${script_dir}/install.sh" --profile tools "$@"
fi

if [ -n "$VERSION" ]; then
  installer_url="https://raw.githubusercontent.com/${REPO}/${VERSION}/install.sh"
else
  installer_url="https://raw.githubusercontent.com/${REPO}/main/install.sh"
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

command -v curl >/dev/null 2>&1 || die "required command not found: curl"
curl -fsSL -o "${tmp}/install.sh" "$installer_url" || die "could not fetch consolidated installer: ${installer_url}"

exec sh "${tmp}/install.sh" --profile tools "$@"
