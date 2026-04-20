#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
base_config="${QWEN_BUBBLES_CONFIG:-"$repo_root/config.json"}"
workspace_source="${QWEN_SWE_FIXTURE:-"$repo_root"}"
log_path="${QWEN_SWE_LOG:?QWEN_SWE_LOG is required}"
turns_json="${QWEN_SWE_TURNS_JSON:-}"

if [[ -z "$turns_json" ]]; then
  prompt="${QWEN_SWE_PROMPT:-}"
  if [[ -z "$prompt" ]]; then
    echo "QWEN_SWE_TURNS_JSON or QWEN_SWE_PROMPT is required" >&2
    exit 2
  fi
  turns_json="$(python3 -c 'import json,os; print(json.dumps([os.environ["QWEN_SWE_PROMPT"]]))')"
fi

run_dir="$(mktemp -d "${TMPDIR:-/tmp}/qwen-swe-live.XXXXXX")"
workspace_root="$run_dir/workspace"
turns_file="$run_dir/turns.json"
config_path="$run_dir/config.json"
debug_log_dir="$run_dir/logs/conversations"
save_dir="$run_dir/transcripts"
stdout_path="$run_dir/stdout.txt"
stderr_path="$run_dir/stderr.txt"

mkdir -p "$debug_log_dir" "$save_dir" "$(dirname "$log_path")" "$workspace_root"
printf '%s\n' "$turns_json" > "$turns_file"

(cd "$workspace_source" && tar --exclude .git --exclude logs --exclude transcripts --exclude bin --exclude models --exclude testdata/swe/generated-logs -cf - .) | (cd "$workspace_root" && tar -xf -)

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
cfg["chat"]["debug_log_mode"] = os.environ.get("QWEN_BUBBLES_DEBUG_LOG_MODE", "full")
cfg["chat"]["project_root"] = workspace_root
cfg["chat"]["system_prompt_files"] = [repo_path(path) for path in cfg["chat"].get("system_prompt_files", [])]
if cfg["chat"].get("profiles_dir"):
    cfg["chat"]["profiles_dir"] = repo_path(cfg["chat"]["profiles_dir"])

cfg.setdefault("workspace", {})
cfg["workspace"]["root"] = workspace_root
cfg["workspace"]["cwd"] = workspace_root
cfg["workspace"]["enable_writes"] = os.environ.get("QWEN_BUBBLES_ENABLE_WRITES", "true").lower() != "false"
cfg["workspace"]["enable_shell"] = os.environ.get("QWEN_BUBBLES_ENABLE_SHELL", "true").lower() != "false"

os.makedirs(os.path.dirname(config_path), exist_ok=True)
with open(config_path, "w", encoding="utf-8") as f:
    json.dump(cfg, f, indent=2)
    f.write("\n")
PY

if [[ -n "${QWEN_BUBBLES_BIN:-}" ]]; then
  "$QWEN_BUBBLES_BIN" -config "$config_path" -turns-file "$turns_file" ${QWEN_BUBBLES_EXTRA_ARGS:-} >"$stdout_path" 2>"$stderr_path"
else
  (cd "$repo_root" && go run ./cmd/qwen-bubbles -config "$config_path" -turns-file "$turns_file" ${QWEN_BUBBLES_EXTRA_ARGS:-}) >"$stdout_path" 2>"$stderr_path"
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
