#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'USAGE'
Usage: scripts/test-install-dependencies.sh [--manifest-only]

Validates install dependency coverage for scripts/install-requirements.sh.
The default mode checks every macOS default and --all dependency command that
the manifest says the installer owns. Use --manifest-only for local structural
checks without requiring host tool installation.
USAGE
}

manifest_only=false
while [[ $# -gt 0 ]]; do
  case "$1" in
    --manifest-only)
      manifest_only=true
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
  shift
done

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"
export PATH="$HOME/.local/bin:$HOME/Library/Python/3.12/bin:$HOME/Library/Python/3.11/bin:$HOME/Library/Python/3.10/bin:$PATH"

expected_ids=(
  ast_grep
  clangd
  git
  go
  java
  joern
  madge
  pydeps
  python_lsp
  ripgrep
  rust
  semgrep
  typescript_language_server
  universal_ctags
)

manifest_ids="$(
  python3 - <<'PY'
import json
from pathlib import Path

manifest = json.loads(Path("scripts/install/manifest.json").read_text())
for dep in sorted(item["id"] for item in manifest["dependencies"]):
    print(dep)
PY
)"
expected_ids_text="$(printf '%s\n' "${expected_ids[@]}" | sort)"

if [[ "$manifest_ids" != "$expected_ids_text" ]]; then
  echo "dependency coverage list does not match scripts/install/manifest.json" >&2
  echo "manifest ids:" >&2
  printf '%s\n' "$manifest_ids" >&2
  echo "coverage ids:" >&2
  printf '%s\n' "$expected_ids_text" >&2
  exit 1
fi

if $manifest_only; then
  echo "ok: install dependency coverage list matches manifest"
  exit 0
fi

require_command() {
  local binary="$1"
  local dependency="$2"
  if ! command -v "$binary" >/dev/null 2>&1; then
    echo "${dependency}: expected ${binary} on PATH after scripts/install-requirements.sh --all" >&2
    exit 1
  fi
  echo "ok: ${dependency} (${binary})"
}

require_java_17() {
  require_command java java
  local version
  version="$(java -version 2>&1 | awk -F '"' '/version/ {print $2; exit}')"
  local major="${version%%.*}"
  if [[ "$major" == "1" ]]; then
    major="$(printf '%s' "$version" | awk -F. '{print $2}')"
  fi
  if [[ -z "$major" || "$major" -lt 17 ]]; then
    echo "java: expected Java 17+ after scripts/install-requirements.sh --all, found ${version:-unknown}" >&2
    exit 1
  fi
  echo "ok: java version ${version}"
}

require_command git git
require_command rg ripgrep
require_command ast-grep ast_grep
require_command semgrep semgrep
require_command joern-scan joern
require_command go go
require_command gopls go
require_command pyright-langserver python_lsp
require_command node typescript_language_server
require_command npm typescript_language_server
require_command typescript-language-server typescript_language_server
require_command tsc typescript_language_server
require_command cargo rust
require_command rust-analyzer rust
require_java_17
require_command jdtls java
require_command clangd clangd
require_command madge madge
require_command pydeps pydeps

if ! ctags --version 2>/dev/null | grep -qi "universal ctags"; then
  echo "universal_ctags: expected ctags to be Universal Ctags" >&2
  exit 1
fi
echo "ok: universal_ctags (ctags)"

echo "ok: install dependency smoke covered all manifest dependencies"
