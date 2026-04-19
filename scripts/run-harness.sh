#!/usr/bin/env bash
set -euo pipefail

LAUNCH_DIR="$PWD"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

export QWEN_BUBBLES_SPAWN_DIR="$LAUNCH_DIR"
exec go run ./cmd/qwen-bubbles -config "$ROOT/config.json"
