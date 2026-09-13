#!/usr/bin/env bash
set -euo pipefail
shopt -s nullglob

dist="${1:-dist}"
script_dir="$(cd "$(dirname "$0")" && pwd)"
archives=("$dist"/idiom_*_source.tar.gz)
if [[ ${#archives[@]} -ne 1 ]]; then
  echo "expected exactly one corresponding-source archive" >&2
  exit 1
fi

python3 "$script_dir/validate-release-assets.py" "$dist"
source_dir="$(mktemp -d)"
trap 'rm -rf "$source_dir"' EXIT
tar -xzf "${archives[0]}" -C "$source_dir"
"${GITLEAKS_BIN:-gitleaks}" dir "$source_dir" --config "$script_dir/../.gitleaks.toml" --redact --no-banner
cd "$source_dir"
# Resolve the already-selected compiler before disabling module downloads.
# This also works when a local older `go` launcher selected a cached toolchain.
go_binary="$(go env GOROOT)/bin/go"
GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off CGO_ENABLED=0 "$go_binary" build -mod=vendor -trimpath -o idiom-source-build ./cmd/idiom
./idiom-source-build version
