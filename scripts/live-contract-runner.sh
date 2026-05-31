#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
base_config="${IDIOM_CONFIG:-"$repo_root/config.json"}"
fixture_source="${IDIOM_SWE_FIXTURE:-}"
fixture_git_ref="${IDIOM_SWE_FIXTURE_GIT_REF:-}"
fixture_git_repo="${IDIOM_SWE_FIXTURE_GIT_REPO:-$repo_root}"
log_path="${IDIOM_SWE_LOG:?IDIOM_SWE_LOG is required}"
idiom_bin="${IDIOM_BIN:-"$HOME/.idiom/bin/idiom"}"
turns_json="${IDIOM_SWE_TURNS_JSON:-}"
runner_mode="${IDIOM_SWE_RUNNER_MODE:-auto}"

if [[ ! -x "$idiom_bin" ]]; then
  echo "live-contract runner requires built idiom binary at $idiom_bin; run make build or set IDIOM_BIN" >&2
  exit 2
fi
if [[ -z "$fixture_source" && -z "$fixture_git_ref" ]]; then
  echo "live-contract runner requires IDIOM_SWE_FIXTURE or IDIOM_SWE_FIXTURE_GIT_REF" >&2
  exit 2
fi
if [[ -n "$fixture_source" && -n "$fixture_git_ref" ]]; then
  echo "live-contract runner accepts only one of IDIOM_SWE_FIXTURE or IDIOM_SWE_FIXTURE_GIT_REF" >&2
  exit 2
fi
if [[ "$fixture_git_ref" == -* ]]; then
  echo "live-contract runner refuses fixture git refs that begin with '-'" >&2
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
run_id="live-contract.$(date +%s).$$.${RANDOM:-0}"
log_owner_path="$log_path.owner"
queue_socket="$run_dir/missing-queue.sock"
if [[ "${IDIOM_SWE_USE_QUEUE:-false}" == "true" ]]; then
  queue_socket="${IDIOM_QUEUE_SOCKET:-}"
fi
baseline_tree=""
worker_exit=0

mkdir -p "$workspace_root" "$debug_log_dir" "$telemetry_dir" "$save_dir" "$(dirname "$log_path")"
rm -f "$log_path"
printf '%s\n' "$run_id" >"$log_owner_path"
printf '%s\n' "$turns_json" > "$turns_file"
turn_count="$(python3 - "$turns_json" <<'PY'
import json
import sys

print(len(json.loads(sys.argv[1])))
PY
)"
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

require_single_turn_lane_entrypoint() {
  local mode="$1"
  if [[ "$turn_count" -gt 1 ]]; then
    echo "live-contract runner cannot replay ${turn_count} turns through ${mode}; use the turns-file entrypoint or add explicit lane replay support" >&2
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
  if ! owns_log_path; then
    return 0
  fi
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

owns_log_path() {
  local owner
  owner="$(cat "$log_owner_path" 2>/dev/null || true)"
  [[ "$owner" == "$run_id" ]]
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

snapshot_requested_files() {
  local raw="${IDIOM_LIVE_SNAPSHOT_PATHS_JSON:-}"
  if [[ -z "$raw" ]]; then
    return 0
  fi
  python3 - "$extra_records" "$workspace_root" "$raw" <<'PY'
import json
import pathlib
import sys
from datetime import datetime, timezone

out_path = pathlib.Path(sys.argv[1])
root = pathlib.Path(sys.argv[2]).resolve()
try:
    requested = json.loads(sys.argv[3])
except json.JSONDecodeError as exc:
    raise SystemExit(f"IDIOM_LIVE_SNAPSHOT_PATHS_JSON is invalid JSON: {exc}") from exc
if not isinstance(requested, list):
    raise SystemExit("IDIOM_LIVE_SNAPSHOT_PATHS_JSON must be a JSON array")
records = []
for raw_path in requested:
    rel = str(raw_path or "").strip()
    if not rel or rel.startswith("/") or "\x00" in rel:
        raise SystemExit(f"snapshot path must be workspace-relative: {rel!r}")
    path = (root / rel).resolve()
    if path != root and root not in path.parents:
        raise SystemExit(f"snapshot path escapes workspace root: {rel}")
    exists = path.is_file()
    content = ""
    size = 0
    truncated = False
    if exists:
        data = path.read_bytes()
        size = len(data)
        limit = 65536
        truncated = size > limit
        content = data[:limit].decode("utf-8", errors="replace")
    records.append({
        "type": "idiom.live_contract.file_snapshot",
        "ts": datetime.now(timezone.utc).isoformat(),
        "path": rel,
        "exists": exists,
        "size_bytes": size,
        "truncated": truncated,
        "content": content,
    })
with out_path.open("a", encoding="utf-8") as f:
    for record in records:
        json.dump(record, f, sort_keys=True)
        f.write("\n")
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
    snapshot_requested_files || true
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

  if ! owns_log_path; then
    echo "live-contract runner lost ownership of $log_path; skipping stale log write" >&2
    exit "$exit_code"
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
  rm -f "$log_owner_path"
  exit "$exit_code"
}
trap finish EXIT

source_commit=""
source_tree=""
source_kind="directory"
if [[ -n "$fixture_git_ref" ]]; then
  source_kind="git_ref"
  source_commit="$(git -C "$fixture_git_repo" rev-parse "$fixture_git_ref^{commit}")"
  source_tree="$(git -C "$fixture_git_repo" rev-parse "$fixture_git_ref^{tree}")"
  git -C "$fixture_git_repo" archive --format=tar "$fixture_git_ref" | (cd "$workspace_root" && tar -xf -)
else
  (cd "$fixture_source" && tar \
    --exclude .git \
    --exclude .idiom \
    --exclude .cache \
    --exclude transcripts \
    --exclude logs \
    -cf - .) | (cd "$workspace_root" && tar -xf -)
fi

python3 - "$workspace_root" <<'PY'
import json
import os
import pathlib
import sys

root = pathlib.Path(sys.argv[1]).resolve()
raw = os.environ.get("IDIOM_WORKSPACE_EXTRA_FILES_JSON", "").strip()
if not raw:
    raise SystemExit(0)
entries = json.loads(raw)
if not isinstance(entries, list):
    raise SystemExit("IDIOM_WORKSPACE_EXTRA_FILES_JSON must be a JSON array")
for entry in entries:
    if not isinstance(entry, dict):
        raise SystemExit("IDIOM_WORKSPACE_EXTRA_FILES_JSON entries must be objects")
    rel = str(entry.get("path") or "").strip()
    if not rel or rel.startswith("/") or "\x00" in rel:
        raise SystemExit("extra file path must be a non-empty relative path")
    path = (root / rel).resolve()
    if path != root and root not in path.parents:
        raise SystemExit(f"extra file path escapes workspace root: {rel}")
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(str(entry.get("content") or ""), encoding="utf-8")
PY

git -C "$workspace_root" init -q
git -C "$workspace_root" config user.email "live-contracts@example.invalid"
git -C "$workspace_root" config user.name "Idiom Live Contracts"
git -C "$workspace_root" add -A
git -C "$workspace_root" commit -q -m "baseline live contract fixture"
baseline_tree="$(git -C "$workspace_root" rev-parse HEAD^{tree})"
append_record "idiom.live_contract.workspace_prepared" "$(python3 - "$workspace_root" "$baseline_tree" "$fixture_source" "$fixture_git_repo" "$fixture_git_ref" "$source_kind" "$source_commit" "$source_tree" <<'PY'
import json
import os
import sys

workspace, tree, fixture, git_repo, git_ref, source_kind, source_commit, source_tree = sys.argv[1:]
print(json.dumps({
    "workspace_root": workspace,
    "baseline_tree": tree,
    "fixture": os.path.abspath(fixture) if fixture else "",
    "fixture_git_repo": os.path.abspath(git_repo) if git_repo else "",
    "fixture_git_ref": git_ref,
    "fixture_source_kind": source_kind,
    "source_commit": source_commit,
    "source_tree": source_tree,
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
classifier_timeout = int(os.environ.get("IDIOM_LIVE_ROUTE_CLASSIFIER_TIMEOUT_SECONDS", "60"))
classifier["timeout_seconds"] = max(int(classifier.get("timeout_seconds") or 0), classifier_timeout)

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
tool_rules_json = os.environ.get("IDIOM_WORKSPACE_TOOL_RULES_JSON", "").strip()
if tool_rules_json:
    tool_rules = json.loads(tool_rules_json)
    if not isinstance(tool_rules, dict):
        raise SystemExit("IDIOM_WORKSPACE_TOOL_RULES_JSON must be a JSON object")
    workspace["tool_rules"] = tool_rules

scripted_base_url = os.environ.get("IDIOM_SCRIPTED_MODEL_BASE_URL", "").strip().rstrip("/")
if scripted_base_url:
    scripted_model = os.environ.get("IDIOM_SCRIPTED_MODEL_ID", "scripted-model").strip() or "scripted-model"
    scripted_classifier = os.environ.get("IDIOM_SCRIPTED_ENABLE_ROUTE_CLASSIFIER", "").lower() in ("1", "true", "yes")
    chat["auto_start_server"] = False
    for section_name in ("llama_server", "implementer"):
        section = cfg.setdefault(section_name, {})
        section["kind"] = "mlx"
        section["base_url"] = scripted_base_url
        section["model"] = scripted_model
        section["host"] = "127.0.0.1"
        section["port"] = 48881 if section_name == "llama_server" else 48882
        section["binary"] = "scripted-model-server"
    classifier["enabled"] = scripted_classifier
    classifier["base_url"] = scripted_base_url
    classifier["model"] = scripted_model
    classifier["auto_start_server"] = False

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
    "scripted_model_enabled": bool(os.environ.get("IDIOM_SCRIPTED_MODEL_BASE_URL", "").strip()),
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
  if [[ "$runner_mode" == "tui" ]]; then
    IDIOM_QUEUE_SOCKET="$queue_socket" IDIOM_TELEMETRY_DIR="$telemetry_dir" python3 - "$idiom_bin" "$config_path" "$prompt_file" "$task_timeout" "$turns_json" "$turn_count" <<'PY'
import os
import pty
import select
import subprocess
import sys
import time
import json
import pathlib

idiom_bin, config_path, prompt_file, timeout_s, turns_json_raw, turn_count_raw = sys.argv[1:]
timeout = max(10, int(timeout_s))
turn_count = max(1, int(turn_count_raw))
# Build turn list: if turns_json has multiple entries, use them; otherwise use prompt_file.
turns = []
if turns_json_raw and turns_json_raw != "null":
    try:
        parsed = json.loads(turns_json_raw)
        if isinstance(parsed, list) and len(parsed) > 0:
            turns = [str(t).strip() for t in parsed if str(t).strip()]
    except json.JSONDecodeError:
        pass
if not turns:
    prompt = open(prompt_file, encoding="utf-8").read().rstrip("\n")
    if not prompt.lstrip().startswith("/feature-planner"):
        prompt = "/feature-planner " + prompt
    turns = [prompt]

cfg = json.load(open(config_path, encoding="utf-8"))
debug_log_dir = pathlib.Path(cfg.get("chat", {}).get("debug_log_dir") or "")
env = os.environ.copy()
env.setdefault("TERM", "xterm-256color")
master, slave = pty.openpty()
proc = subprocess.Popen(
    [idiom_bin, "-config", config_path],
    stdin=slave,
    stdout=slave,
    stderr=slave,
    env=env,
    close_fds=True,
)
os.close(slave)
out = bytearray()
marker = b"feature-planner:"
turn_index = 0
deadline = time.time() + timeout
completed = False
turn_sent_at = 0.0
min_turn_delay = 2.0  # seconds between turns to let async handlers complete

def debug_log_completed():
    if not debug_log_dir:
        return False
    logs = sorted(debug_log_dir.glob("*.jsonl"), key=lambda p: p.stat().st_mtime if p.exists() else 0)
    if not logs:
        return False
    try:
        text = logs[-1].read_text(encoding="utf-8", errors="replace")
    except OSError:
        return False
    terminal_roles = (
        "idiom.feature_planner.complete",
        "idiom.feature_planner.recovery_open",
        "idiom.feature_planner.question_open",
        "idiom.feature_planner.planning_stuck",
        "idiom.feature_planner.failed",
        "idiom.feature_planner.terminal",
    )
    if any(role in text for role in terminal_roles):
        return True
    raw_done_markers = os.environ.get("IDIOM_LIVE_TUI_DONE_CONTAINS", "").strip()
    if raw_done_markers:
        try:
            parsed = json.loads(raw_done_markers)
            markers = [str(item) for item in parsed] if isinstance(parsed, list) else [raw_done_markers]
        except json.JSONDecodeError:
            markers = [raw_done_markers]
        if all(marker and marker in text for marker in markers):
            return True
    return False

def should_send_next_turn():
    global turn_index, turn_sent_at
    if turn_index >= len(turns):
        return False
    if turn_index == 0:
        return True  # first turn after initial delay
    # Subsequent turns: wait for streaming to complete (no active turn)
    if time.time() - turn_sent_at < min_turn_delay:
        return False
    return True

try:
    while time.time() < deadline:
        readable, _, _ = select.select([master], [], [], 0.1)
        if readable:
            try:
                chunk = os.read(master, 65536)
            except OSError:
                break
            if not chunk:
                break
            out.extend(chunk)
            if marker in out and turn_index >= len(turns):
                completed = True
                break
        if debug_log_completed():
            completed = True
            break
        if turn_index < len(turns) and should_send_next_turn():
            time.sleep(0.5)
            turn = turns[turn_index]
            os.write(master, turn.encode("utf-8") + b"\r")
            turn_sent_at = time.time()
            turn_index += 1
    sys.stdout.buffer.write(bytes(out))
    sys.stdout.flush()
finally:
    if proc.poll() is None:
        try:
            os.write(master, b"\x03")
        except OSError:
            pass
        try:
            proc.wait(timeout=5)
        except subprocess.TimeoutExpired:
            proc.kill()
            proc.wait(timeout=5)
    os.close(master)
if not completed:
    print("tui runner did not observe feature-planner completion marker", file=sys.stderr)
    sys.exit(1)
PY
  elif [[ "$runner_mode" == "feature-planner" ]] || { [[ "$runner_mode" == "auto" || -z "$runner_mode" ]] && [[ "${IDIOM_SWE_FAMILY:-}" == feature_planner* ]]; }; then
    if [[ "$turn_count" -gt 1 ]]; then
      turn_prompt_dir="$run_dir/feature-planner-turns"
      mkdir -p "$turn_prompt_dir"
      python3 - "$turns_json" "$turn_prompt_dir" <<'PY'
import json
import pathlib
import sys

turns = json.loads(sys.argv[1])
out = pathlib.Path(sys.argv[2])
for i, turn in enumerate(turns):
    (out / f"{i:03d}.txt").write_text(str(turn).rstrip("\n") + "\n", encoding="utf-8")
PY
      feature_resume=""
      for turn_prompt in "$turn_prompt_dir"/*.txt; do
        feature_stdout="$run_dir/feature-planner-$(basename "$turn_prompt" .txt).jsonl"
        if [[ -n "$feature_resume" ]]; then
          IDIOM_QUEUE_SOCKET="$queue_socket" IDIOM_TELEMETRY_DIR="$telemetry_dir" "$idiom_bin" feature-planner -config "$config_path" -json --prompt-file "$turn_prompt" --resume "$feature_resume" ${IDIOM_EXTRA_ARGS:-} >"$feature_stdout"
        else
          IDIOM_QUEUE_SOCKET="$queue_socket" IDIOM_TELEMETRY_DIR="$telemetry_dir" "$idiom_bin" feature-planner -config "$config_path" -json --prompt-file "$turn_prompt" ${IDIOM_EXTRA_ARGS:-} >"$feature_stdout"
        fi
        cat "$feature_stdout"
next_resume="$(python3 - "$feature_stdout" <<'PY'
import json
import pathlib
import sys

fallback = ""
for line in pathlib.Path(sys.argv[1]).read_text(encoding="utf-8", errors="replace").splitlines():
    if not line.strip().startswith("{"):
        continue
    try:
        record = json.loads(line)
    except json.JSONDecodeError:
        continue
    session_id = str(record.get("session_id") or "").strip()
    if not session_id:
        continue
    if record.get("type") == "idiom.feature_planner.complete":
        print(session_id)
        sys.exit(0)
    if not fallback:
        fallback = session_id
if fallback:
    print(fallback)
PY
)"
        if [[ -n "$next_resume" ]]; then
          feature_resume="$next_resume"
        fi
      done
    else
      IDIOM_QUEUE_SOCKET="$queue_socket" IDIOM_TELEMETRY_DIR="$telemetry_dir" "$idiom_bin" feature-planner -config "$config_path" -json --prompt-file "$prompt_file" ${IDIOM_EXTRA_ARGS:-}
    fi
  elif [[ "$runner_mode" == "plan-execute" ]] || { [[ "$runner_mode" == "auto" || -z "$runner_mode" ]] && { [[ "${IDIOM_SWE_FAMILY:-}" == plan_then_execute* || "${IDIOM_SWE_FAMILY:-}" == plan_execute* ]]; }; }; then
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
) >"$stdout_path" 2>"$stderr_path" &
worker_pid=$!
(
  sleep "$task_timeout" &
  sleep_pid=$!
  trap 'kill "$sleep_pid" 2>/dev/null; exit 0' TERM INT
  wait "$sleep_pid" 2>/dev/null
  if kill -0 "$worker_pid" 2>/dev/null; then
    echo "live-contract watchdog: task exceeded ${task_timeout}s, sending SIGTERM" >&2
    terminate_tree TERM "$worker_pid"
    sleep 5
    terminate_tree KILL "$worker_pid"
  fi
) &
watchdog_pid=$!
wait "$worker_pid" 2>/dev/null || worker_exit=$?
kill -TERM "$watchdog_pid" 2>/dev/null || true
terminate_tree TERM "$watchdog_pid"
wait "$watchdog_pid" 2>/dev/null || true

if [[ $worker_exit -eq 143 ]] || [[ $worker_exit -eq 137 ]]; then
  append_record "idiom.live_contract.timeout" "$(python3 - "$task_timeout" "$worker_exit" <<'PY'
import json
import sys

timeout_seconds, exit_code = sys.argv[1:]
print(json.dumps({
    "timeout_seconds": int(timeout_seconds),
    "exit_code": int(exit_code),
    "signal": "SIGTERM" if int(exit_code) == 143 else "SIGKILL",
}))
PY
)"
  echo "live-contract: task was watchdog-killed (exit=$worker_exit); preserving partial log" >&2
elif [[ $worker_exit -ne 0 ]]; then
  echo "idiom exit=$worker_exit" >&2
fi

exit "$worker_exit"
