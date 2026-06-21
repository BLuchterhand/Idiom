#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

export PATH="$HOME/.local/bin:$HOME/.cargo/bin:$HOME/Library/Python/3.12/bin:$HOME/Library/Python/3.11/bin:$HOME/Library/Python/3.10/bin:$(go env GOPATH 2>/dev/null || printf '%s/go' "$HOME")/bin:$PATH"

python3 scripts/test-discovery-tools-e2e.py "$@"
