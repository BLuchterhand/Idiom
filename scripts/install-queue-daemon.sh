#!/usr/bin/env bash
set -euo pipefail

# install-queue-daemon.sh — Install the idiom queue daemon as a
# per-user launchd agent on macOS. The daemon serializes concurrent
# `idiom plan-execute` and `idiom implement` dispatches against a
# small set of named pools, preventing Metal-pressure crashes when
# multiple lanes hit the same MLX backend.
#
# Idempotent: rerunning the script with existing config / plist /
# loaded agent is a no-op except for the verification step.
#
# Default pool layout (qwen-35b, qwen-27b, qwen-coder, each
# max_in_flight=1) matches idiom's local Qwen backend profiles;
# projects that run different MLX backends can edit
# ~/.idiom/runner.json after install.

usage() {
  cat <<'USAGE'
Usage: scripts/install-queue-daemon.sh [--reload] [--uninstall] [--print-plist]

Installs ~/.idiom/runner.json (if missing) and a per-user
launchd agent at ~/Library/LaunchAgents/com.idiom.queue.plist
that starts `idiom queue serve` on login. Verifies the daemon
is reachable via `idiom queue ping`.

Options:
  --reload       Stop and reload an already-installed agent (use after
                 changing the binary or config).
  --uninstall    Remove the agent and config. Leaves runner.json untouched.
  --print-plist  Print the generated plist body and exit (dry-run).
  -h, --help     Show this help.

This script is macOS-only. On other platforms, run the daemon
directly: `idiom queue serve --config ~/.idiom/runner.json`.
USAGE
}

mode="install"
while [[ $# -gt 0 ]]; do
  case "$1" in
    --reload)      mode="reload" ;;
    --uninstall)   mode="uninstall" ;;
    --print-plist) mode="print-plist" ;;
    -h|--help)     usage; exit 0 ;;
    *) echo "unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
  shift
done

if [[ "$(uname -s)" != "Darwin" ]]; then
  echo "install-queue-daemon.sh currently supports macOS only." >&2
  echo "On other platforms run: idiom queue serve --config ~/.idiom/runner.json" >&2
  exit 1
fi

agent_label="com.idiom.queue"
plist_path="$HOME/Library/LaunchAgents/${agent_label}.plist"
config_path="$HOME/.idiom/runner.json"
log_dir="$HOME/.idiom/logs"
log_path="$log_dir/queue-daemon.log"
err_path="$log_dir/queue-daemon.err"
uid="$(id -u)"

idiom_bin="$(command -v idiom || true)"
if [[ -z "$idiom_bin" ]]; then
  echo "idiom binary not on PATH. Run scripts/install-requirements.sh or make build first." >&2
  exit 1
fi
idiom_bin_src="$(readlink -f "$idiom_bin" 2>/dev/null || echo "$idiom_bin")"

# Ensure the daemon binary lives in ~/.idiom/bin/ so launchd can spawn it
# without crossing macOS TCC-protected paths. ~/Documents/ (where source
# checkouts typically live) is TCC-controlled; launchd-spawned processes hang
# indefinitely in dyld when their executable path resolves through there
# because the consent prompt has no UI to show. Source builds make this path the
# canonical real binary; release installs or older source builds are copied here
# only when the PATH binary is newer.
idiom_bin_real="$HOME/.idiom/bin/idiom"
mkdir -p "$(dirname "$idiom_bin_real")"
if [[ ! -f "$idiom_bin_real" || "$idiom_bin_src" -nt "$idiom_bin_real" ]]; then
  cp -f "$idiom_bin_src" "$idiom_bin_real"
  # Ad-hoc re-sign the copy. Go's linker-signed binaries lose their
  # signature on cp; without a fresh signature, the new file path
  # isn't trusted by the kernel and launchd's spawn can still hang.
  if command -v codesign >/dev/null 2>&1; then
    codesign -s - "$idiom_bin_real" 2>/dev/null || true
  fi
  echo "installed binary copy at $idiom_bin_real"
else
  echo "ok: $idiom_bin_real already up to date"
fi

render_plist() {
  cat <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>${agent_label}</string>
  <key>ProgramArguments</key>
  <array>
    <string>${idiom_bin_real}</string>
    <string>queue</string>
    <string>serve</string>
    <string>--config</string>
    <string>${config_path}</string>
    <string>--socket</string>
    <string>${HOME}/.idiom/runner.sock</string>
  </array>
  <key>WorkingDirectory</key><string>${HOME}/.idiom</string>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key>
  <dict>
    <key>SuccessfulExit</key><false/>
    <key>Crashed</key><true/>
  </dict>
  <key>StandardOutPath</key><string>${log_path}</string>
  <key>StandardErrorPath</key><string>${err_path}</string>
  <key>EnvironmentVariables</key>
  <dict>
    <key>PATH</key><string>/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin</string>
    <key>IDIOM_TELEMETRY_DIR</key><string>${HOME}/.idiom/logs/telemetry</string>
  </dict>
</dict>
</plist>
PLIST
}

if [[ "$mode" == "print-plist" ]]; then
  render_plist
  exit 0
fi

ensure_config() {
  mkdir -p "$(dirname "$config_path")" "$log_dir"
  if [[ ! -f "$config_path" ]]; then
    cat > "$config_path" <<'EOF'
{
  "_comment": "Per-machine idiom queue daemon config. Pools map to the qwen-pair and qwen-27b-pair backend profiles (35B planner, 27B planner, Coder executor) — Metal command-buffer pressure is the binding constraint, so each pool caps at 1 in-flight job. Edit max_in_flight or add pools as needed for your local MLX setup.",
  "pools": {
    "qwen-35b":   {"max_in_flight": 1},
    "qwen-27b":   {"max_in_flight": 1},
    "qwen-coder": {"max_in_flight": 1}
  }
}
EOF
    echo "wrote $config_path"
  else
    echo "ok: $config_path already present"
  fi
}

unload_agent() {
  if launchctl print "gui/${uid}/${agent_label}" >/dev/null 2>&1; then
    launchctl bootout "gui/${uid}/${agent_label}" 2>/dev/null || true
    echo "unloaded $agent_label"
  fi
}

load_agent() {
  mkdir -p "$(dirname "$plist_path")"
  render_plist > "$plist_path"
  echo "wrote $plist_path"
  launchctl bootstrap "gui/${uid}" "$plist_path"
  echo "bootstrapped $agent_label"
}

verify_daemon() {
  for _ in 1 2 3 4 5 6 7 8 9 10; do
    if idiom queue ping </dev/null 2>/dev/null | grep -q "ok\|uptime"; then
      idiom queue ping </dev/null
      idiom queue status </dev/null
      return 0
    fi
    sleep 0.3
  done
  echo "queue daemon never responded to ping; check $log_path and $err_path" >&2
  if [[ -f "$err_path" ]]; then tail -20 "$err_path" >&2; fi
  return 1
}

case "$mode" in
  install)
    ensure_config
    if launchctl print "gui/${uid}/${agent_label}" >/dev/null 2>&1; then
      echo "ok: $agent_label already loaded"
    else
      load_agent
    fi
    verify_daemon
    ;;
  reload)
    ensure_config
    unload_agent
    load_agent
    verify_daemon
    ;;
  uninstall)
    unload_agent
    if [[ -f "$plist_path" ]]; then
      rm "$plist_path"
      echo "removed $plist_path"
    fi
    echo "($config_path left in place; remove manually if you want a clean slate)"
    ;;
esac
