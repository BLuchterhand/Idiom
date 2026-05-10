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

echo "Installing idiom core requirements..."
brew_install go ripgrep ast-grep universal-ctags semgrep joern gopls

if ! ctags --version 2>/dev/null | grep -qi "universal ctags"; then
  echo "ctags is present but is not Universal Ctags. Ensure /opt/homebrew/bin precedes /usr/bin on PATH." >&2
  exit 1
fi

if $install_all; then
  echo "Installing optional language precision backends..."
  brew_install jdtls rust-analyzer

  if ! command -v clangd >/dev/null 2>&1; then
    echo "clangd is missing. Install Xcode Command Line Tools with: xcode-select --install" >&2
  fi

  if command -v npm >/dev/null 2>&1; then
    npm install -g typescript-language-server typescript pyright madge
  else
    echo "npm is missing; skipping TypeScript/Python npm-backed language tools." >&2
  fi

  if command -v pipx >/dev/null 2>&1; then
    pipx install pydeps || pipx upgrade pydeps || true
  elif command -v python3 >/dev/null 2>&1; then
    python3 -m pip install --user pydeps || {
      echo "pydeps install failed. Install pipx with 'brew install pipx' and rerun with --all." >&2
    }
  else
    echo "python3 is missing; skipping pydeps." >&2
  fi
fi

require_command git "git is required for history and blame tools."
require_command rg "ripgrep is required for fast content search."
require_command ast-grep "ast-grep is required for structural_search."
require_command semgrep "semgrep is required for semgrep_scan."
require_command joern-scan "joern-scan is required for joern_scan."
require_command gopls "gopls is required for precise Go workspace tools."

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
