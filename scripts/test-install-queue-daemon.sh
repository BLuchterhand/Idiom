#!/usr/bin/env bash
# Smoke test for scripts/install-queue-daemon.sh.
#
# Asserts:
#   1. --print-plist produces well-formed XML referencing the
#      idiom binary copy at ~/.idiom/bin/idiom.
#   2. install + reload + uninstall round-trip leaves no orphaned
#      launchd agent.
#   3. After install, `idiom queue ping` returns within a reasonable
#      window, confirming the launchd-spawned daemon actually binds
#      the socket and serves traffic.
#   4. Re-running --install on an already-loaded agent is a no-op.
#
# macOS-only (relies on launchctl). On other platforms the script is
# a no-op exit 0.
#
# Designed to be safe to run on a developer's machine: snapshots
# pre-existing state (config, plist, loaded agent), runs the test,
# restores. Failures abort and leave state restored.

set -euo pipefail

if [[ "$(uname -s)" != "Darwin" ]]; then
  echo "skip: install-queue-daemon is macOS-only"
  exit 0
fi

if ! command -v idiom >/dev/null 2>&1; then
  echo "skip: idiom binary not on PATH (run make build first)"
  exit 0
fi

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

agent_label="com.idiom.queue"
plist_path="$HOME/Library/LaunchAgents/${agent_label}.plist"
config_path="$HOME/.idiom/runner.json"
uid="$(id -u)"

# Snapshot pre-existing state so we can restore on exit.
snapshot_dir="$(mktemp -d -t idiom-queue-test.XXXXXX)"
pre_loaded=false
if launchctl print "gui/${uid}/${agent_label}" >/dev/null 2>&1; then
  pre_loaded=true
fi
if [[ -f "$plist_path" ]]; then
  cp "$plist_path" "$snapshot_dir/plist"
fi
if [[ -f "$config_path" ]]; then
  cp "$config_path" "$snapshot_dir/config"
fi

restore() {
  echo "restoring snapshot..."
  launchctl bootout "gui/${uid}/${agent_label}" 2>/dev/null || true
  if [[ -f "$snapshot_dir/plist" ]]; then
    cp "$snapshot_dir/plist" "$plist_path"
  else
    rm -f "$plist_path"
  fi
  if [[ -f "$snapshot_dir/config" ]]; then
    cp "$snapshot_dir/config" "$config_path"
  else
    rm -f "$config_path"
  fi
  if $pre_loaded; then
    launchctl bootstrap "gui/${uid}" "$plist_path" 2>/dev/null || true
  fi
  rm -rf "$snapshot_dir"
}
trap restore EXIT

fail() {
  echo "FAIL: $1" >&2
  exit 1
}

# --- test 1: --print-plist dry-run produces valid XML ---
echo "test 1: --print-plist dry-run"
plist_body="$(scripts/install-queue-daemon.sh --print-plist)"
echo "$plist_body" | grep -q '<key>Label</key><string>com.idiom.queue</string>' \
  || fail "print-plist missing Label key"
echo "$plist_body" | grep -q '/.idiom/bin/idiom' \
  || fail "print-plist should reference ~/.idiom/bin/idiom (TCC-safe copy)"
echo "$plist_body" | grep -q '<key>RunAtLoad</key><true/>' \
  || fail "print-plist missing RunAtLoad=true"
echo "$plist_body" | grep -q '<key>KeepAlive</key>' \
  || fail "print-plist missing KeepAlive"
echo "  ok"

# --- test 2: install fresh agent + reachable daemon ---
echo "test 2: install + daemon reachable"
launchctl bootout "gui/${uid}/${agent_label}" 2>/dev/null || true
rm -f "$plist_path"
rm -f "$config_path"
scripts/install-queue-daemon.sh >/tmp/idiom-queue-install-1.log 2>&1 \
  || { cat /tmp/idiom-queue-install-1.log; fail "first install failed"; }
launchctl print "gui/${uid}/${agent_label}" >/dev/null 2>&1 \
  || fail "agent not loaded after install"
ping_ok=false
for _ in 1 2 3 4 5 6 7 8 9 10; do
  if idiom queue ping </dev/null 2>/dev/null | grep -q "ok"; then
    ping_ok=true
    break
  fi
  sleep 0.3
done
$ping_ok || fail "queue daemon never responded to ping after install"
status_out="$(idiom queue status </dev/null 2>&1)"
echo "$status_out" | grep -q "qwen-35b" \
  || fail "queue status should list qwen-35b pool"
echo "$status_out" | grep -q "qwen-27b" \
  || fail "queue status should list qwen-27b pool"
echo "$status_out" | grep -q "qwen-coder" \
  || fail "queue status should list qwen-coder pool"
echo "  ok"

# --- test 3: idempotent re-install ---
echo "test 3: re-install is a no-op"
out="$(scripts/install-queue-daemon.sh 2>&1)"
echo "$out" | grep -q "already loaded" \
  || fail "re-install should report 'already loaded' (got: $out)"
echo "  ok"

# --- test 4: --reload bounces cleanly ---
echo "test 4: --reload bounces the agent"
scripts/install-queue-daemon.sh --reload >/tmp/idiom-queue-reload.log 2>&1 \
  || { cat /tmp/idiom-queue-reload.log; fail "reload failed"; }
ping_ok=false
for _ in 1 2 3 4 5 6 7 8 9 10; do
  if idiom queue ping </dev/null 2>/dev/null | grep -q "ok"; then
    ping_ok=true
    break
  fi
  sleep 0.3
done
$ping_ok || fail "queue daemon never came back after reload"
echo "  ok"

# --- test 5: --uninstall removes agent + plist ---
echo "test 5: --uninstall cleans up"
scripts/install-queue-daemon.sh --uninstall >/tmp/idiom-queue-uninstall.log 2>&1 \
  || { cat /tmp/idiom-queue-uninstall.log; fail "uninstall failed"; }
if launchctl print "gui/${uid}/${agent_label}" >/dev/null 2>&1; then
  fail "agent still loaded after --uninstall"
fi
if [[ -f "$plist_path" ]]; then
  fail "plist still on disk after --uninstall"
fi
echo "  ok"

echo "ALL PASS"
