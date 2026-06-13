#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'USAGE'
Usage: scripts/install-requirements.sh [--all] [--no-build] [--no-audit] [--no-queue]

Installs the macOS/Homebrew toolchain idiom expects for local development and
workspace discovery, then builds bin/idiom and verifies the active harness audit
backends. Also installs the per-user queue daemon launchd agent so
concurrent `idiom plan-execute` / `idiom implement` dispatches serialize
through a single configurable pool cap.

Options:
  --all       Also install optional language precision backends.
  --no-build  Skip make build.
  --no-audit  Skip the final harness_audit verification.
  --no-queue  Skip the per-user queue daemon installation. The CLI still
              works without the daemon (lanes proceed un-gated); skip
              this step only when running on shared infra where launchd
              agents aren't appropriate.
  -h, --help  Show this help.
USAGE
}

install_all=false
build_idiom=true
run_audit=true
install_queue=true

while [[ $# -gt 0 ]]; do
  case "$1" in
    --all)
      install_all=true
      ;;
    --no-build)
      build_idiom=false
      ;;
    --no-audit)
      run_audit=false
      ;;
    --no-queue)
      install_queue=false
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

if [[ "$(uname -s)" != "Darwin" ]]; then
  echo "scripts/install-requirements.sh currently supports macOS/Homebrew only." >&2
  exit 1
fi

if ! command -v brew >/dev/null 2>&1; then
  echo "Homebrew is required. Install it from https://brew.sh/ and rerun this script." >&2
  exit 1
fi

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"
export PATH="$HOME/.local/bin:$HOME/Library/Python/3.12/bin:$HOME/Library/Python/3.11/bin:$HOME/Library/Python/3.10/bin:$PATH"

brew_install() {
  local formula
  for formula in "$@"; do
    if brew list --formula "$formula" >/dev/null 2>&1; then
      echo "ok: $formula already installed"
    else
      echo "install: $formula"
      brew install "$formula"
    fi
  done
}

require_command() {
  local binary="$1"
  local message="$2"
  if ! command -v "$binary" >/dev/null 2>&1; then
    echo "$message" >&2
    return 1
  fi
}

require_java_17() {
  require_command java "Java 17+ is required for jdtls-backed Java discovery."
  local version
  version="$(java -version 2>&1 | awk -F '"' '/version/ {print $2; exit}')"
  local major="${version%%.*}"
  if [[ "$major" == "1" ]]; then
    major="$(printf '%s' "$version" | awk -F. '{print $2}')"
  fi
  if [[ -z "$major" || "$major" -lt 17 ]]; then
    echo "Java 17+ is required for jdtls-backed Java discovery; found ${version:-unknown}." >&2
    return 1
  fi
}

echo "Installing idiom core requirements..."
brew_install go ripgrep ast-grep universal-ctags semgrep joern gopls

if ! ctags --version 2>/dev/null | grep -qi "universal ctags"; then
  echo "ctags is present but is not Universal Ctags. Ensure /opt/homebrew/bin precedes /usr/bin on PATH." >&2
  exit 1
fi

if $install_all; then
  echo "Installing optional language precision backends..."
  brew_install jdtls node pipx rust rust-analyzer

  if ! command -v clangd >/dev/null 2>&1; then
    echo "clangd is missing. Install Xcode Command Line Tools with: xcode-select --install" >&2
  fi

  npm install -g typescript-language-server typescript pyright madge

  pipx install pydeps || pipx upgrade pydeps || true
fi

require_command git "git is required for history and blame tools."
require_command rg "ripgrep is required for fast content search."
require_command ast-grep "ast-grep is required for structural_search."
require_command semgrep "semgrep is required for semgrep_scan."
require_command joern-scan "joern-scan is required for joern_scan."
require_command go "go is required for Go builds and Go workspace discovery."
require_command gopls "gopls is required for precise Go workspace tools."

if $install_all; then
  require_command node "node is required for TypeScript, Pyright, and madge discovery helpers."
  require_command npm "npm is required to install TypeScript, Pyright, and madge discovery helpers."
  require_command typescript-language-server "typescript-language-server is required for TypeScript discovery."
  require_command tsc "typescript is required for TypeScript discovery."
  require_command pyright-langserver "pyright-langserver is required for Python discovery."
  require_command madge "madge is required for TypeScript dependency graph precision."
  require_command pydeps "pydeps is required for Python dependency graph precision."
  require_command cargo "cargo is required for Rust diagnostics and rust-analyzer projects."
  require_command rust-analyzer "rust-analyzer is required for Rust discovery."
  require_java_17
  require_command jdtls "jdtls is required for Java discovery."
  require_command clangd "clangd is required for C/C++ discovery. Install Xcode Command Line Tools with: xcode-select --install"
fi

if $build_idiom; then
  echo "Building idiom..."
  make build
elif [[ -x "./bin/idiom" ]]; then
  make install-path
fi

if $install_queue; then
  echo "Installing per-user queue daemon (launchd agent)..."
  scripts/install-queue-daemon.sh
fi

if $run_audit; then
  idiom_bin="./bin/idiom"
  if [[ ! -x "$idiom_bin" ]]; then
    idiom_bin="./idiom"
  fi
  if [[ ! -x "$idiom_bin" ]]; then
    echo "No idiom binary found for audit verification. Run make build first." >&2
    exit 1
  fi
  echo "Verifying harness audit..."
  "$idiom_bin" tool harness_audit --input '{"purpose":"tool_surface"}'
fi
