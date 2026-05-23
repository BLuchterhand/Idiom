#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
base_config="${IDIOM_CONFIG:-"$repo_root/config.json"}"
fixture_source="${IDIOM_SWE_FIXTURE:?IDIOM_SWE_FIXTURE is required}"
log_path="${IDIOM_SWE_LOG:?IDIOM_SWE_LOG is required}"
idiom_bin="${IDIOM_BIN:-"$HOME/.idiom/bin/idiom"}"
turns_json="${IDIOM_SWE_TURNS_JSON:-}"

if [[ ! -x "$idiom_bin" ]]; then
  echo "live-contract runner requires built idiom binary at $idiom_bin; run make build or set IDIOM_BIN" >&2
  exit 2
fi
if [[ -z "$turns_json" ]]; then
  prompt="${IDIOM_SWE_PROMPT:-}"
  if [[ -z "$prompt" ]]; then
    echo "IDIOM_SWE_TURNS_JSON or IDIOM_SWE_PROMPT is required" >&2
    exit 2
  fi
  turns_json="$(python3 -c 'import json,os; print(json.dumps([os.environ["IDIOM_SWE_PROMPT"]]))')"
fi

run_dir="$(mktemp -d "${TMPDIR:-/tmp}/idiom-live-contract.XXXXXX")"
workspace_root="$run_dir/workspace"
turns_file="$run_dir/turns.json"
prompt_file="$run_dir/prompt.txt"
config_path="$run_dir/config.json"
debug_log_dir="$run_dir/logs/conversations"
telemetry_dir="$run_dir/logs/telemetry"
save_dir="$run_dir/transcripts"
stdout_path="$run_dir/stdout.txt"
stderr_path="$run_dir/stderr.txt"
extra_records="$run_dir/extra-records.jsonl"
stdout_copied_marker="$run_dir/stdout-copied"
baseline_tree=""
worker_exit=0

mkdir -p "$workspace_root" "$debug_log_dir" "$telemetry_dir" "$save_dir" "$(dirname "$log_path")"
rm -f "$log_path"
printf '%s\n' "$turns_json" > "$turns_file"
python3 - "$turns_json" "$prompt_file" <<'PY'
import json
import sys

turns = json.loads(sys.argv[1])
prompt = turns[0] if turns else ""
with open(sys.argv[2], "w", encoding="utf-8") as f:
    f.write(prompt)
    f.write("\n")
PY
: >"$extra_records"

append_record() {
  local type="$1"
  local payload_json="$2"
  python3 - "$extra_records" "$type" "$payload_json" <<'PY'
import json
import sys
from datetime import datetime, timezone

path, typ, payload = sys.argv[1:]
data = json.loads(payload)
record = {"type": typ, "ts": datetime.now(timezone.utc).isoformat(), **data}
with open(path, "a", encoding="utf-8") as f:
    json.dump(record, f, sort_keys=True)
    f.write("\n")
PY
}

copy_latest_log() {
  if [[ ! -s "$log_path" ]]; then
    find "$debug_log_dir" -maxdepth 1 -type f -name '*.jsonl' -print 2>/dev/null | sort | while IFS= read -r debug_log; do
      cat "$debug_log"
    done >"$log_path"
  fi
  if [[ -s "$stdout_path" && ! -e "$stdout_copied_marker" ]]; then
    python3 - "$log_path" "$stdout_path" <<'PY'
import json
import pathlib
import sys

log_path, stdout_path = sys.argv[1:]
out = pathlib.Path(log_path)
stdout = pathlib.Path(stdout_path)
with stdout.open("r", encoding="utf-8", errors="replace") as src, out.open("a", encoding="utf-8") as dst:
    for line in src:
        text = line.strip()
        if not text.startswith("{"):
            continue
        try:
            json.loads(text)
        except json.JSONDecodeError:
            continue
        dst.write(text)
        dst.write("\n")
PY
    : >"$stdout_copied_marker"
  fi
  if [[ -s "$log_path" ]]; then
    return 0
  fi
  python3 - "$log_path" "$stdout_path" "$stderr_path" "$worker_exit" <<'PY'
import json
import pathlib
import sys
from datetime import datetime, timezone

log_path, stdout_path, stderr_path, exit_code = sys.argv[1:]
stdout = pathlib.Path(stdout_path).read_text(encoding="utf-8", errors="replace") if pathlib.Path(stdout_path).exists() else ""
stderr = pathlib.Path(stderr_path).read_text(encoding="utf-8", errors="replace") if pathlib.Path(stderr_path).exists() else ""
message = "live contract runner produced no conversation log"
if stderr.strip():
    message += ": " + stderr.strip()[:500]
record = {
    "type": "error",
    "ts": datetime.now(timezone.utc).isoformat(),
    "message": message,
    "exit_code": int(exit_code),
    "stdout_chars": len(stdout),
    "stderr_chars": len(stderr),
}
with open(log_path, "w", encoding="utf-8") as f:
    json.dump(record, f, sort_keys=True)
    f.write("\n")
PY
}

workspace_status_json() {
  python3 - "$workspace_root" <<'PY'
import json
import pathlib
import subprocess
import sys

root = pathlib.Path(sys.argv[1])
if not (root / ".git").exists():
    print("[]")
    raise SystemExit
out = subprocess.check_output(
    ["git", "-C", str(root), "status", "--porcelain=v1", "--untracked-files=all"],
    text=True,
)
paths = []
for line in out.splitlines():
    if not line.strip():
        continue
    path = line[3:] if len(line) > 3 else line
    if path == ".idiom" or path.startswith(".idiom/") or path == ".planning" or path.startswith(".planning/") or path == "logs" or path.startswith("logs/") or path == "transcripts" or path.startswith("transcripts/"):
        continue
    paths.append(path)
print(json.dumps(sorted(set(paths))))
PY
}

finish() {
  local exit_code=$?
  trap - EXIT
  worker_exit="${worker_exit:-$exit_code}"

  local changed_before="[]"
  local changed_after="[]"
  local post_tree=""
  local rollback_status="not_initialized"
  if [[ -d "$workspace_root/.git" ]]; then
    changed_before="$(workspace_status_json)"
    git -C "$workspace_root" reset --hard -q HEAD >/dev/null 2>&1 || rollback_status="reset_failed"
    git -C "$workspace_root" clean -fdx -q >/dev/null 2>&1 || rollback_status="clean_failed"
    changed_after="$(workspace_status_json)"
    post_tree="$(git -C "$workspace_root" rev-parse HEAD^{tree} 2>/dev/null || true)"
    if [[ "$rollback_status" == "not_initialized" || -z "$rollback_status" ]]; then
      rollback_status="clean"
    fi
    if [[ "$rollback_status" != "clean" ]]; then
      exit_code=1
    fi
    if [[ "$changed_after" != "[]" || -n "$baseline_tree" && "$post_tree" != "$baseline_tree" ]]; then
      rollback_status="dirty_after_reset"
      exit_code=1
    fi
  fi

  copy_latest_log || true
  append_record "idiom.live_contract.rollback" "$(python3 - "$rollback_status" "$baseline_tree" "$post_tree" "$changed_before" "$changed_after" <<'PY'
import json
import sys

status, baseline, post, before, after = sys.argv[1:]
print(json.dumps({
    "status": status,
    "baseline_tree": baseline,
    "post_reset_tree": post,
    "changed_before_reset": json.loads(before),
    "changed_after_reset": json.loads(after),
}))
PY
)"
  cat "$extra_records" >>"$log_path" 2>/dev/null || true
  exit "$exit_code"
}
trap finish EXIT

(cd "$fixture_source" && tar \
  --exclude .git \
  --exclude .idiom \
  --exclude .cache \
  --exclude transcripts \
  --exclude logs \
  -cf - .) | (cd "$workspace_root" && tar -xf -)

git -C "$workspace_root" init -q
git -C "$workspace_root" config user.email "live-contracts@example.invalid"
git -C "$workspace_root" config user.name "Idiom Live Contracts"
git -C "$workspace_root" add -A
git -C "$workspace_root" commit -q -m "baseline live contract fixture"
baseline_tree="$(git -C "$workspace_root" rev-parse HEAD^{tree})"
append_record "idiom.live_contract.workspace_prepared" "$(python3 - "$workspace_root" "$baseline_tree" "$fixture_source" <<'PY'
import json
import os
import sys

workspace, tree, fixture = sys.argv[1:]
print(json.dumps({
    "workspace_root": workspace,
    "baseline_tree": tree,
    "fixture": os.path.abspath(fixture),
    "rollback_strategy": "git reset --hard HEAD && git clean -fdx",
}))
PY
)"
if [[ "${IDIOM_SWE_FAMILY:-}" == plan_then_execute* || "${IDIOM_SWE_FAMILY:-}" == plan_execute* ]]; then
  append_record "message" "$(python3 - "$prompt_file" <<'PY'
import json
import pathlib
import sys

prompt = pathlib.Path(sys.argv[1]).read_text(encoding="utf-8")
print(json.dumps({"role": "user", "content": prompt.rstrip("\n")}))
PY
)"
fi

python3 - "$base_config" "$config_path" "$debug_log_dir" "$save_dir" "$workspace_root" "$run_dir" "$repo_root" <<'PY'
import json
import os
import sys

base_config, config_path, debug_log_dir, save_dir, workspace_root, run_dir, repo_root = sys.argv[1:]
with open(base_config, "r", encoding="utf-8") as f:
    cfg = json.load(f)

def repo_path(path):
    if not path or os.path.isabs(path):
        return path
    return os.path.join(repo_root, path)

for section_name in ("llama_server", "implementer"):
    section = cfg.setdefault(section_name, {})
    if section.get("model"):
        section["model"] = repo_path(section["model"])
    if section.get("log_file"):
        section["log_file"] = os.path.join(run_dir, section_name + ".log")

classifier = cfg.setdefault("route_classifier", {})
if os.environ.get("IDIOM_LIVE_ENABLE_ROUTE_CLASSIFIER", "").lower() not in ("1", "true", "yes"):
    planner = cfg.setdefault("llama_server", {})
    host = planner.get("host") or "127.0.0.1"
    port = planner.get("port")
    if port:
        classifier["base_url"] = f"http://{host}:{port}"
    classifier["auto_start_server"] = False
if classifier.get("server_model"):
    classifier["server_model"] = repo_path(classifier["server_model"])
if classifier.get("log_file"):
    classifier["log_file"] = os.path.join(run_dir, "route-classifier.log")

chat = cfg.setdefault("chat", {})
chat["debug_log_dir"] = debug_log_dir
chat["save_dir"] = save_dir
chat["debug_log_mode"] = os.environ.get("IDIOM_DEBUG_LOG_MODE", "redacted")
chat["project_root"] = workspace_root
chat["system_prompt_files"] = [repo_path(path) for path in chat.get("system_prompt_files", [])]
if chat.get("profiles_dir"):
    chat["profiles_dir"] = repo_path(chat["profiles_dir"])

workspace = cfg.setdefault("workspace", {})
workspace["root"] = workspace_root
workspace["cwd"] = workspace_root
workspace["enable_writes"] = os.environ.get("IDIOM_ENABLE_WRITES", "true").lower() != "false"
workspace["enable_shell"] = os.environ.get("IDIOM_ENABLE_SHELL", "true").lower() != "false"

if os.environ.get("IDIOM_LIVE_ENABLE_MCP", "").lower() not in ("1", "true", "yes"):
    cfg["mcp"] = []

os.makedirs(os.path.dirname(config_path), exist_ok=True)
with open(config_path, "w", encoding="utf-8") as f:
    json.dump(cfg, f, indent=2)
    f.write("\n")
PY

append_record "idiom.live_contract.resolved_config" "$(python3 - "$config_path" "$telemetry_dir" <<'PY'
import json
import os
import sys

config_path, telemetry_dir = sys.argv[1:]
cfg = json.load(open(config_path, encoding="utf-8"))
planner = cfg.get("llama_server", {})
implementer = cfg.get("implementer", {})
classifier = cfg.get("route_classifier", {})
chat = cfg.get("chat", {})
print(json.dumps({
    "config_path": config_path,
    "planner_kind": planner.get("kind", ""),
    "planner_model_exists": bool(planner.get("model")) and os.path.exists(planner.get("model")),
    "implementer_kind": implementer.get("kind", ""),
    "implementer_model_exists": bool(implementer.get("model")) and os.path.exists(implementer.get("model")),
    "route_classifier_enabled": bool(classifier.get("enabled")),
    "route_classifier_model_exists": bool(classifier.get("server_model")) and os.path.exists(classifier.get("server_model")),
    "implementation_dispatch": chat.get("implementation_dispatch", ""),
    "debug_log_dir": chat.get("debug_log_dir", ""),
    "telemetry_dir": telemetry_dir,
    "mcp_enabled": bool(cfg.get("mcp")),
}))
PY
)"

task_timeout="${IDIOM_SWE_TASK_TIMEOUT:-240}"
(
  cd "$run_dir"
  if [[ "${IDIOM_SWE_FAMILY:-}" == plan_then_execute* || "${IDIOM_SWE_FAMILY:-}" == plan_execute* ]]; then
    IDIOM_TELEMETRY_DIR="$telemetry_dir" "$idiom_bin" plan-execute -config "$config_path" -json --prompt-file "$prompt_file" ${IDIOM_EXTRA_ARGS:-}
  else
    IDIOM_TELEMETRY_DIR="$telemetry_dir" "$idiom_bin" -config "$config_path" -turns-file "$turns_file" ${IDIOM_EXTRA_ARGS:-}
  fi
) >"$stdout_path" 2>"$stderr_path" &
worker_pid=$!
(
  sleep "$task_timeout" &
  sleep_pid=$!
  trap 'kill "$sleep_pid" 2>/dev/null; exit 0' TERM INT
  wait "$sleep_pid" 2>/dev/null
  if kill -0 "$worker_pid" 2>/dev/null; then
    echo "live-contract watchdog: task exceeded ${task_timeout}s, sending SIGTERM" >&2
    kill -TERM "$worker_pid" 2>/dev/null || true
    sleep 5
    kill -KILL "$worker_pid" 2>/dev/null || true
  fi
) &
watchdog_pid=$!
wait "$worker_pid" 2>/dev/null || worker_exit=$?
kill -TERM "$watchdog_pid" 2>/dev/null || true
pkill -P "$watchdog_pid" 2>/dev/null || true
wait "$watchdog_pid" 2>/dev/null || true

if [[ $worker_exit -eq 143 ]] || [[ $worker_exit -eq 137 ]]; then
  echo "live-contract: task was watchdog-killed (exit=$worker_exit); preserving partial log" >&2
elif [[ $worker_exit -ne 0 ]]; then
  echo "idiom exit=$worker_exit" >&2
fi

exit "$worker_exit"
