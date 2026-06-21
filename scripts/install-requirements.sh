#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'USAGE'
Usage: scripts/install-requirements.sh [--all] [--no-build] [--no-audit] [--no-queue]

Installs the macOS/Homebrew or Linux/apt toolchain idiom expects for local
development and workspace discovery, then builds bin/idiom and verifies the
active harness audit backends. On macOS, also installs the per-user queue daemon
launchd agent so concurrent `idiom plan-execute` / `idiom implement` dispatches
serialize through a single configurable pool cap.

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

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"
export PATH="$HOME/.local/bin:$HOME/Library/Python/3.12/bin:$HOME/Library/Python/3.11/bin:$HOME/Library/Python/3.10/bin:$(go env GOPATH 2>/dev/null || printf '%s/go' "$HOME")/bin:$PATH"

os_name="$(uname -s)"

run_as_root() {
  if [[ "$(id -u)" -eq 0 ]]; then
    "$@"
  else
    sudo "$@"
  fi
}

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

apt_install() {
  export DEBIAN_FRONTEND=noninteractive
  run_as_root apt-get update
  run_as_root apt-get install -y --no-install-recommends "$@"
}

npm_global_install() {
  if [[ "$os_name" == "Linux" ]]; then
    run_as_root npm install -g "$@"
  else
    npm install -g "$@"
  fi
}

require_command() {
  local binary="$1"
  local message="$2"
  if ! command -v "$binary" >/dev/null 2>&1; then
    echo "$message" >&2
    return 1
  fi
}

install_go_language_tools() {
  require_command go "go is required before installing Go language tools."
  go install golang.org/x/tools/gopls@latest
}

install_rust_language_tools() {
  if ! command -v cargo >/dev/null 2>&1; then
    curl --proto '=https' --tlsv1.2 -fsSL https://sh.rustup.rs | sh -s -- -y --profile minimal
    # shellcheck disable=SC1091
    source "$HOME/.cargo/env"
  fi
  if command -v rustup >/dev/null 2>&1; then
    rustup component add rust-analyzer
  fi
}

install_jdtls_linux() {
  if command -v jdtls >/dev/null 2>&1; then
    return
  fi

  local install_dir="/opt/idiom-jdtls"
  local archive
  archive="$(mktemp)"
  curl -fsSL "https://download.eclipse.org/jdtls/snapshots/jdt-language-server-latest.tar.gz" -o "$archive"
  run_as_root rm -rf "$install_dir"
  run_as_root mkdir -p "$install_dir"
  run_as_root tar -xzf "$archive" -C "$install_dir"
  rm -f "$archive"

  if [[ -x "$install_dir/bin/jdtls" ]]; then
    run_as_root ln -sf "$install_dir/bin/jdtls" /usr/local/bin/jdtls
    return
  fi

  local launcher
  launcher="$(find "$install_dir/plugins" -name 'org.eclipse.equinox.launcher_*.jar' | head -n 1)"
  if [[ -z "$launcher" ]]; then
    echo "jdtls archive did not contain an Eclipse launcher jar." >&2
    return 1
  fi

  local wrapper
  wrapper="$(mktemp)"
  cat >"$wrapper" <<EOF
#!/usr/bin/env bash
exec java -Declipse.application=org.eclipse.jdt.ls.core.id1 \\
  -Dosgi.bundles.defaultStartLevel=4 \\
  -Declipse.product=org.eclipse.jdt.ls.core.product \\
  -jar "$launcher" \\
  -configuration "$install_dir/config_linux" \\
  -data "\${JDTLS_WORKSPACE:-\${TMPDIR:-/tmp}/jdtls-workspace}" "\$@"
EOF
  chmod +x "$wrapper"
  run_as_root mv "$wrapper" /usr/local/bin/jdtls
}

install_joern_linux() {
  if command -v joern-scan >/dev/null 2>&1; then
    return
  fi

  local tmp_dir
  tmp_dir="$(mktemp -d)"
  curl -fsSL "https://github.com/joernio/joern/releases/latest/download/joern-install.sh" -o "$tmp_dir/joern-install.sh"
  chmod +x "$tmp_dir/joern-install.sh"
  run_as_root "$tmp_dir/joern-install.sh" --install-dir=/opt/joern --reinstall --without-plugins
  rm -rf "$tmp_dir"

  local binary
  for binary in joern joern-scan joern-parse; do
    local target
    target="$(find /opt/joern -type f -name "$binary" -perm -111 2>/dev/null | head -n 1)"
    if [[ -n "$target" ]]; then
      run_as_root ln -sf "$target" "/usr/local/bin/$binary"
    fi
  done
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

resolve_idiom_bin() {
  if [[ -n "${IDIOM_BIN:-}" && -x "${IDIOM_BIN}" ]]; then
    printf '%s\n' "${IDIOM_BIN}"
    return 0
  fi
  if [[ -x "./bin/idiom" ]]; then
    printf '%s\n' "./bin/idiom"
    return 0
  fi
  if [[ -x "./idiom" ]]; then
    printf '%s\n' "./idiom"
    return 0
  fi
  if command -v idiom >/dev/null 2>&1; then
    command -v idiom
    return 0
  fi

  return 1
}

initialize_global_config() {
  local idiom_bin
  if ! idiom_bin="$(resolve_idiom_bin)"; then
    echo "No idiom binary found for global config initialization. Run without --no-build or set IDIOM_BIN." >&2
    return 1
  fi

  "$idiom_bin" init --global --merge >/dev/null
  echo "Verified global config initialized under ~/.idiom"
}

case "$os_name" in
  Darwin)
    if ! command -v brew >/dev/null 2>&1; then
      echo "Homebrew is required. Install it from https://brew.sh/ and rerun this script." >&2
      exit 1
    fi

    echo "Installing idiom core requirements..."
    brew_install go ripgrep ast-grep universal-ctags semgrep joern gopls
    ;;
  Linux)
    if ! command -v apt-get >/dev/null 2>&1; then
      echo "scripts/install-requirements.sh supports Linux through apt-get only." >&2
      exit 1
    fi

    echo "Installing idiom core requirements..."
    apt_install ca-certificates curl git ripgrep universal-ctags unzip tar gzip xz-utils build-essential python3 python3-pip python3-venv pipx nodejs npm golang-go
    npm_global_install @ast-grep/cli
    pipx install semgrep || pipx upgrade semgrep || true
    install_go_language_tools
    install_joern_linux
    ;;
  *)
    echo "scripts/install-requirements.sh supports macOS/Homebrew and Linux/apt only." >&2
    exit 1
    ;;
esac

if ! ctags --version 2>/dev/null | grep -qi "universal ctags"; then
  echo "ctags is present but is not Universal Ctags. Ensure the package-manager ctags path precedes system ctags on PATH." >&2
  exit 1
fi

if $install_all; then
  echo "Installing optional language precision backends..."
  case "$os_name" in
    Darwin)
      brew_install jdtls node pipx rust rust-analyzer

      if ! command -v clangd >/dev/null 2>&1; then
        echo "clangd is missing. Install Xcode Command Line Tools with: xcode-select --install" >&2
      fi

      npm_global_install typescript-language-server typescript pyright madge

      pipx install pydeps || pipx upgrade pydeps || true
      ;;
    Linux)
      apt_install clangd openjdk-21-jdk-headless || apt_install clangd openjdk-17-jdk-headless
      npm_global_install typescript-language-server typescript pyright madge
      pipx install pydeps || pipx upgrade pydeps || true
      install_rust_language_tools
      install_jdtls_linux
      ;;
  esac
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

initialize_global_config

if $install_queue && [[ "$os_name" == "Darwin" ]]; then
  echo "Installing per-user queue daemon (launchd agent)..."
  scripts/install-queue-daemon.sh
elif $install_queue; then
  echo "Skipping queue daemon installation; launchd queue daemon is macOS-only."
fi

if $run_audit; then
  if ! idiom_bin="$(resolve_idiom_bin)"; then
    echo "No idiom binary found for audit verification. Run make build first." >&2
    exit 1
  fi
  echo "Verifying harness audit..."
  "$idiom_bin" tool harness_audit --input '{"purpose":"tool_surface"}'
fi
