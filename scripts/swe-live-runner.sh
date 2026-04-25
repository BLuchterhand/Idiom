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
config_path="$run_dir/config.json"
debug_log_dir="$run_dir/logs/conversations"
save_dir="$run_dir/transcripts"
stdout_path="$run_dir/stdout.txt"
stderr_path="$run_dir/stderr.txt"

mkdir -p "$debug_log_dir" "$save_dir" "$(dirname "$log_path")" "$workspace_root"
printf '%s\n' "$turns_json" > "$turns_file"

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
  "$IDIOM_BIN" -config "$config_path" -turns-file "$turns_file" ${IDIOM_EXTRA_ARGS:-} >"$stdout_path" 2>"$stderr_path" &
else
  (cd "$repo_root" && go run ./cmd/idiom -config "$config_path" -turns-file "$turns_file" ${IDIOM_EXTRA_ARGS:-}) >"$stdout_path" 2>"$stderr_path" &
fi
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
    kill -TERM "$worker_pid" 2>/dev/null || true
    sleep 5
    kill -KILL "$worker_pid" 2>/dev/null || true
  fi
) &
watchdog_pid=$!
# With `set -e` at the top of the script, a non-zero `wait` exit would
# abort before we can capture the code. Explicit || assignment keeps
# the exit status available without aborting on a watchdog SIGTERM.
worker_exit=0
wait "$worker_pid" 2>/dev/null || worker_exit=$?
# Kill the watchdog AND any sleep child it spawned — without this the
# shell blocks on the sleep finishing naturally, making every fast task
# take the full watchdog window.
kill -TERM "$watchdog_pid" 2>/dev/null || true
pkill -P "$watchdog_pid" 2>/dev/null || true
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

latest_log="$(find "$debug_log_dir" -maxdepth 1 -type f -name '*.jsonl' -print | sort | tail -n 1)"
if [[ -z "$latest_log" ]]; then
  echo "no debug log produced; stdout:" >&2
  cat "$stdout_path" >&2 || true
  echo "stderr:" >&2
  cat "$stderr_path" >&2 || true
  exit 1
fi

cp "$latest_log" "$log_path"
