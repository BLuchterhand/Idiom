#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
task_id="${IDIOM_SWE_TASK_ID:?IDIOM_SWE_TASK_ID is required}"
log_path="${IDIOM_SWE_LOG:?IDIOM_SWE_LOG is required}"
script_dir="${IDIOM_SCRIPTED_MODEL_DIR:-"$repo_root/testdata/scripted-contracts/scripts"}"
script_path="${IDIOM_SCRIPTED_MODEL_SCRIPT:-"$script_dir/$task_id.json"}"

if [[ ! -f "$script_path" ]]; then
  echo "scripted contract runner missing model script: $script_path" >&2
  exit 2
fi

run_dir="$(mktemp -d "${TMPDIR:-/tmp}/idiom-scripted-contract.XXXXXX")"
port_file="$run_dir/model-url.txt"
request_log="$run_dir/model-requests.jsonl"
server_pid=""

finish() {
  local exit_code=$?
  trap - EXIT
  if [[ -n "$server_pid" ]]; then
    kill -TERM "$server_pid" 2>/dev/null || true
    wait "$server_pid" 2>/dev/null || true
  fi
  if [[ -s "$request_log" && -f "$log_path" ]]; then
    if ! python3 "$repo_root/scripts/scripted-model-server.py" \
      --request-log "$request_log" \
      --emit-events-to "$log_path" \
      --require-fully-consumed; then
      echo "scripted contract runner did not fully consume the model script" >&2
      exit_code=1
    fi
  fi
  rm -rf "$run_dir"
  exit "$exit_code"
}
trap finish EXIT

python3 "$repo_root/scripts/scripted-model-server.py" \
  --script "$script_path" \
  --port-file "$port_file" \
  --request-log "$request_log" &
server_pid=$!

for _ in $(seq 1 100); do
  if [[ -s "$port_file" ]]; then
    break
  fi
  if ! kill -0 "$server_pid" 2>/dev/null; then
    echo "scripted model server exited before reporting a port" >&2
    exit 2
  fi
  sleep 0.05
done

if [[ ! -s "$port_file" ]]; then
  echo "scripted model server did not become ready" >&2
  exit 2
fi

export IDIOM_SCRIPTED_MODEL_BASE_URL
IDIOM_SCRIPTED_MODEL_BASE_URL="$(tr -d '[:space:]' < "$port_file")"
export IDIOM_SCRIPTED_MODEL_ID="${IDIOM_SCRIPTED_MODEL_ID:-scripted-model}"
export IDIOM_SCRIPTED_REQUEST_SURFACE_FACTS=1

"$repo_root/scripts/live-contract-runner.sh"
