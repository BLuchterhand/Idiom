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
    python3 - "$log_path" "$request_log" <<'PY'
import json
import pathlib
import sys
from datetime import datetime, timezone

log_path, request_log = map(pathlib.Path, sys.argv[1:])
count = 0
tools = []
for line in request_log.read_text(encoding="utf-8").splitlines():
    if not line.strip():
        continue
    count += 1
    try:
        record = json.loads(line)
    except json.JSONDecodeError:
        continue
    tools.append(int(record.get("tool_count") or 0))
event = {
    "type": "idiom.scripted_model.requests",
    "ts": datetime.now(timezone.utc).isoformat(),
    "request_count": count,
    "tool_counts": tools,
}
with log_path.open("a", encoding="utf-8") as f:
    json.dump(event, f, sort_keys=True)
    f.write("\n")
PY
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

"$repo_root/scripts/live-contract-runner.sh"
