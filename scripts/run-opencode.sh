#!/usr/bin/env bash
# Launches OpenCode pointed at the same llama-server that qwen-bubbles uses.
#
# If llama-server is already reachable (e.g. you ran `make server` separately),
# this script leaves it alone and just execs opencode. Otherwise it spawns
# llama-server in the background via qwen-bubbles' --server-only mode, waits
# for it to become healthy, and tears it down when opencode exits.
set -euo pipefail

LAUNCH_DIR="$PWD"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

LLAMA_HOST="${LLAMA_HOST:-127.0.0.1}"
LLAMA_PORT="${LLAMA_PORT:-8080}"
LLAMA_URL="http://${LLAMA_HOST}:${LLAMA_PORT}"
SERVER_READY_TIMEOUT="${SERVER_READY_TIMEOUT:-240}"
SERVER_LOG="${SERVER_LOG:-logs/opencode-server.log}"

if ! command -v opencode > /dev/null 2>&1; then
  echo "error: opencode not found on PATH" >&2
  echo "       install from https://opencode.ai" >&2
  exit 1
fi

server_reachable() {
  curl -sf --max-time 2 "${LLAMA_URL}/v1/models" > /dev/null 2>&1 \
    || curl -sf --max-time 2 "${LLAMA_URL}/health" > /dev/null 2>&1
}

SERVER_PID=""
SPAWNED_SERVER=0

cleanup() {
  if [[ "$SPAWNED_SERVER" == "1" && -n "$SERVER_PID" ]]; then
    echo "stopping llama-server (pid ${SERVER_PID})" >&2
    kill "$SERVER_PID" 2>/dev/null || true
    # kill descendants too; `go run` spawns a separate compiled child
    pkill -P "$SERVER_PID" 2>/dev/null || true
    wait "$SERVER_PID" 2>/dev/null || true
  fi
}
trap cleanup EXIT INT TERM

if server_reachable; then
  echo "llama-server already up at ${LLAMA_URL} — reusing" >&2
else
  mkdir -p "$(dirname "$SERVER_LOG")"
  echo "starting llama-server (logs: ${SERVER_LOG})" >&2
  ./scripts/run-server.sh > "$SERVER_LOG" 2>&1 &
  SERVER_PID=$!
  SPAWNED_SERVER=1

  for ((i = 0; i < SERVER_READY_TIMEOUT; i++)); do
    if ! kill -0 "$SERVER_PID" 2>/dev/null; then
      echo "error: llama-server process exited before becoming ready" >&2
      echo "       see ${SERVER_LOG}" >&2
      exit 1
    fi
    if server_reachable; then
      echo "llama-server ready at ${LLAMA_URL}" >&2
      break
    fi
    sleep 1
  done

  if ! server_reachable; then
    echo "error: llama-server did not become ready within ${SERVER_READY_TIMEOUT}s" >&2
    echo "       see ${SERVER_LOG}" >&2
    exit 1
  fi
fi

opencode "$@"
