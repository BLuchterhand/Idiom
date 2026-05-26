#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
base_config="${IDIOM_CONFIG:-"$repo_root/config.json"}"
workspace_source="${IDIOM_SWE_FIXTURE:-"$repo_root"}"
log_path="${IDIOM_SWE_LOG:?IDIOM_SWE_LOG is required}"
turns_json="${IDIOM_SWE_TURNS_JSON:-}"

if [[ -z "$turns_json" ]]; then
  prompt="${IDIOM_SWE_PROMPT:-}"
  if [[ -z "$prompt" ]]; then
    echo "IDIOM_SWE_TURNS_JSON or IDIOM_SWE_PROMPT is required" >&2
    exit 2
  fi
  turns_json="$(python3 -c 'import json,os; print(json.dumps([os.environ["IDIOM_SWE_PROMPT"]]))')"
fi

run_dir="$(mktemp -d "${TMPDIR:-/tmp}/idiom-swe-live.XXXXXX")"
workspace_root="$run_dir/workspace"
turns_file="$run_dir/turns.json"
prompt_file="$run_dir/prompt.txt"
config_path="$run_dir/config.json"
debug_log_dir="$run_dir/logs/conversations"
telemetry_dir="$run_dir/logs/telemetry"
save_dir="$run_dir/transcripts"
stdout_path="$run_dir/stdout.txt"
stderr_path="$run_dir/stderr.txt"
runner_mode="${IDIOM_SWE_RUNNER_MODE:-auto}"
queue_socket="$run_dir/missing-queue.sock"
if [[ "${IDIOM_SWE_USE_QUEUE:-false}" == "true" ]]; then
  queue_socket="${IDIOM_QUEUE_SOCKET:-}"
fi

copy_latest_log() {
  local latest_log
  latest_log="$(find "$debug_log_dir" -maxdepth 1 -type f -name '*.jsonl' -print | sort | tail -n 1)"
  if [[ -n "$latest_log" ]]; then
    cp "$latest_log" "$log_path"
    return 0
  fi
  return 1
}

append_plan_execute_stdout_events() {
  if [[ ! -s "$stdout_path" ]]; then
    return 0
  fi
  python3 - "$stdout_path" "$log_path" <<'PY'
import json
import sys

stdout_path, log_path = sys.argv[1:]
with open(stdout_path, "r", encoding="utf-8", errors="replace") as src, open(log_path, "a", encoding="utf-8") as dst:
    for line in src:
        line = line.strip()
        if not line:
            continue
        try:
            event = json.loads(line)
        except json.JSONDecodeError:
            continue
        event_type = str(event.get("type", ""))
        if event_type.startswith("idiom.plan_execute."):
            dst.write(json.dumps(event, separators=(",", ":")) + "\n")
PY
}

mkdir -p "$debug_log_dir" "$telemetry_dir" "$save_dir" "$(dirname "$log_path")" "$workspace_root"
printf '%s\n' "$turns_json" > "$turns_file"
turn_count="$(python3 - "$turns_json" <<'PY'
import json
import sys

print(len(json.loads(sys.argv[1])))
PY
)"
python3 - "$turns_json" "$prompt_file" <<'PY'
import json
import pathlib
import sys

turns = json.loads(sys.argv[1])
prompt = str(turns[-1] if turns else "").rstrip("\n") + "\n"
pathlib.Path(sys.argv[2]).write_text(prompt, encoding="utf-8")
PY

require_single_turn_lane_entrypoint() {
  local mode="$1"
  if [[ "$turn_count" -gt 1 ]]; then
    echo "swe-live runner cannot replay ${turn_count} turns through ${mode}; use the turns-file entrypoint or add explicit lane replay support" >&2
    exit 2
  fi
}

terminate_tree() {
  local signal="$1"
  local pid="$2"
  local child
  if [[ -z "$pid" ]] || ! kill -0 "$pid" 2>/dev/null; then
    return 0
  fi
  while IFS= read -r child; do
    [[ -n "$child" ]] && terminate_tree "$signal" "$child"
  done < <(pgrep -P "$pid" 2>/dev/null || true)
  kill "-$signal" "$pid" 2>/dev/null || true
}

(cd "$workspace_source" && tar \
  --exclude .git \
  --exclude .cache \
  --exclude .claude \
  --exclude .planning \
  --exclude transcripts \
  --exclude bin \
  --exclude models \
  --exclude testdata/swe/generated-logs \
  -cf - .) | (cd "$workspace_root" && tar -xf -)

python3 - "$base_config" "$config_path" "$debug_log_dir" "$save_dir" "$workspace_root" "$repo_root" <<'PY'
import json
import os
import sys

base_config, config_path, debug_log_dir, save_dir, workspace_root, repo_root = sys.argv[1:]
with open(base_config, "r", encoding="utf-8") as f:
    cfg = json.load(f)

def repo_path(path):
    if os.path.isabs(path):
        return path
    return os.path.join(repo_root, path)

cfg.setdefault("llama_server", {})
if cfg["llama_server"].get("model"):
    cfg["llama_server"]["model"] = repo_path(cfg["llama_server"]["model"])

cfg.setdefault("chat", {})
cfg["chat"]["debug_log_dir"] = debug_log_dir
cfg["chat"]["save_dir"] = save_dir
cfg["chat"]["debug_log_mode"] = os.environ.get("IDIOM_DEBUG_LOG_MODE", "full")
cfg["chat"]["project_root"] = workspace_root
cfg["chat"]["system_prompt_files"] = [repo_path(path) for path in cfg["chat"].get("system_prompt_files", [])]
if cfg["chat"].get("profiles_dir"):
    cfg["chat"]["profiles_dir"] = repo_path(cfg["chat"]["profiles_dir"])

cfg.setdefault("workspace", {})
cfg["workspace"]["root"] = workspace_root
cfg["workspace"]["cwd"] = workspace_root
cfg["workspace"]["enable_writes"] = os.environ.get("IDIOM_ENABLE_WRITES", "true").lower() != "false"
cfg["workspace"]["enable_shell"] = os.environ.get("IDIOM_ENABLE_SHELL", "true").lower() != "false"

os.makedirs(os.path.dirname(config_path), exist_ok=True)
with open(config_path, "w", encoding="utf-8") as f:
    json.dump(cfg, f, indent=2)
    f.write("\n")
PY

# Wall-clock safety cap per task. If idiom hasn't returned in
# ${IDIOM_SWE_TASK_TIMEOUT} seconds, SIGTERM it so the suite run can
# continue past a genuinely-hung task instead of burning the full
# harness budget on one outlier. Default 240s (4 min) — generous enough
# for multi-edit implementation tasks, tight enough to detect a real
# hang fast. macOS lacks `timeout`; run the worker in the background and
# watchdog it.
task_timeout="${IDIOM_SWE_TASK_TIMEOUT:-240}"
if [[ -n "${IDIOM_BIN:-}" ]]; then
  idiom_bin="$IDIOM_BIN"
else
  idiom_bin=""
fi
(
  if [[ -n "$idiom_bin" ]]; then
    if [[ "$runner_mode" == "plan-execute" ]] || { [[ "$runner_mode" == "auto" || -z "$runner_mode" ]] && { [[ "${IDIOM_SWE_FAMILY:-}" == plan_then_execute* || "${IDIOM_SWE_FAMILY:-}" == plan_execute* ]]; }; }; then
      require_single_turn_lane_entrypoint "plan-execute"
      IDIOM_QUEUE_SOCKET="$queue_socket" IDIOM_TELEMETRY_DIR="$telemetry_dir" "$idiom_bin" plan-execute -config "$config_path" -json --prompt-file "$prompt_file" ${IDIOM_EXTRA_ARGS:-}
    elif [[ "$runner_mode" == "implement" ]]; then
      require_single_turn_lane_entrypoint "implement"
      IDIOM_QUEUE_SOCKET="$queue_socket" IDIOM_TELEMETRY_DIR="$telemetry_dir" "$idiom_bin" implement -config "$config_path" -json --prompt-file "$prompt_file" ${IDIOM_EXTRA_ARGS:-}
    elif [[ "$runner_mode" == "auto" || -z "$runner_mode" ]]; then
      IDIOM_QUEUE_SOCKET="$queue_socket" IDIOM_TELEMETRY_DIR="$telemetry_dir" "$idiom_bin" -config "$config_path" -turns-file "$turns_file" ${IDIOM_EXTRA_ARGS:-}
    else
      echo "unsupported IDIOM_SWE_RUNNER_MODE: $runner_mode" >&2
      exit 2
    fi
  else
    cd "$repo_root"
    if [[ "$runner_mode" == "plan-execute" ]] || { [[ "$runner_mode" == "auto" || -z "$runner_mode" ]] && { [[ "${IDIOM_SWE_FAMILY:-}" == plan_then_execute* || "${IDIOM_SWE_FAMILY:-}" == plan_execute* ]]; }; }; then
      require_single_turn_lane_entrypoint "plan-execute"
      IDIOM_QUEUE_SOCKET="$queue_socket" IDIOM_TELEMETRY_DIR="$telemetry_dir" go run ./cmd/idiom plan-execute -config "$config_path" -json --prompt-file "$prompt_file" ${IDIOM_EXTRA_ARGS:-}
    elif [[ "$runner_mode" == "implement" ]]; then
      require_single_turn_lane_entrypoint "implement"
      IDIOM_QUEUE_SOCKET="$queue_socket" IDIOM_TELEMETRY_DIR="$telemetry_dir" go run ./cmd/idiom implement -config "$config_path" -json --prompt-file "$prompt_file" ${IDIOM_EXTRA_ARGS:-}
    elif [[ "$runner_mode" == "auto" || -z "$runner_mode" ]]; then
      IDIOM_QUEUE_SOCKET="$queue_socket" IDIOM_TELEMETRY_DIR="$telemetry_dir" go run ./cmd/idiom -config "$config_path" -turns-file "$turns_file" ${IDIOM_EXTRA_ARGS:-}
    else
      echo "unsupported IDIOM_SWE_RUNNER_MODE: $runner_mode" >&2
      exit 2
    fi
  fi
) >"$stdout_path" 2>"$stderr_path" &
worker_pid=$!
(
  sleep "$task_timeout" &
  sleep_pid=$!
  # The watchdog itself is a subshell; if the parent kills us, don't
  # leave the `sleep` orphan behind. Trap wraps the sleep wait so a
  # SIGTERM from the parent reliably tears down the whole subtree.
  trap 'kill "$sleep_pid" 2>/dev/null; exit 0' TERM INT
  wait "$sleep_pid" 2>/dev/null
  if kill -0 "$worker_pid" 2>/dev/null; then
    echo "swe-live watchdog: task exceeded ${task_timeout}s, sending SIGTERM" >&2
    terminate_tree TERM "$worker_pid"
    sleep 5
    terminate_tree KILL "$worker_pid"
  fi
) &
watchdog_pid=$!
trap 'copy_latest_log || true; terminate_tree TERM "$worker_pid"; terminate_tree TERM "$watchdog_pid"; exit 143' TERM INT
# With `set -e` at the top of the script, a non-zero `wait` exit would
# abort before we can capture the code. Explicit || assignment keeps
# the exit status available without aborting on a watchdog SIGTERM.
worker_exit=0
wait "$worker_pid" 2>/dev/null || worker_exit=$?
# Kill the watchdog AND any sleep child it spawned — without this the
# shell blocks on the sleep finishing naturally, making every fast task
# take the full watchdog window.
kill -TERM "$watchdog_pid" 2>/dev/null || true
terminate_tree TERM "$watchdog_pid"
wait "$watchdog_pid" 2>/dev/null || true
# Treat a watchdog-killed task the same as a normal finish; we still
# copy whatever partial log the worker produced. idiom-swe-eval halts
# the whole suite on any non-zero exit, so we translate watchdog signals
# (143 SIGTERM, 137 SIGKILL) to exit 0 with a note on stderr.
if [[ $worker_exit -eq 143 ]] || [[ $worker_exit -eq 137 ]]; then
  echo "swe-live: task was watchdog-killed (exit=$worker_exit); continuing suite with partial log" >&2
  worker_exit=0
elif [[ $worker_exit -ne 0 ]]; then
  echo "idiom exit=$worker_exit" >&2
fi

if ! copy_latest_log; then
  echo "no debug log produced; stdout:" >&2
  cat "$stdout_path" >&2 || true
  echo "stderr:" >&2
  cat "$stderr_path" >&2 || true
  exit 1
fi
append_plan_execute_stdout_events
