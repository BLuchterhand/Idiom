#!/usr/bin/env bash
set -euo pipefail

LAUNCH_DIR="$PWD"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

export IDIOM_SPAWN_DIR="$LAUNCH_DIR"
exec go run ./cmd/idiom -config "$ROOT/config.json"
