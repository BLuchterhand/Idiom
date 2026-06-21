#!/usr/bin/env bash
# In-container half of the Docker install test (Phase 9).
#
# Runs inside ubuntu:24.04 with the host repo's install.sh + the
# pre-built idiom snapshot tarball mounted at /install.sh and /dist
# respectively. Brings up a local http.server, points install.sh at
# it via IDIOM_RELEASE_URL_BASE, then asserts the install path and
# the resulting binary's reachable surface in three tiers:
#
#   tier 1 — subcommand surface coverage
#     every documented subcommand prints help and exits cleanly.
#     Catches dispatch-table regressions and missing-flag panics.
#
#   tier 2 — static functionality (no LLM)
#     idiom brief lint, idiom lane postflight, idiom backend list
#     run against fixture inputs. Validates the Python brief-preflight
#     integration, the Phase 5 postflight pipeline, and the Phase 4
#     backend resolver — none of which need a live model.
#
#   tier 3 — post-install global-config harness smoke
#     a python3 mock chat-completions server is reached by `idiom run`
#     from a clean temp workspace without --config. The test temporarily
#     points installer-created ~/.idiom/config.json at the mock, restores
#     it, and verifies a completion artifact was written.
#
#   tier 4 — stub-LLM lane
#     a python3 mock chat-completions server returns canonical
#     OpenAI-shaped responses; idiom lane implement runs against a
#     workspace pointing at the mock; the assertion is that the mock
#     received at least one POST during the run. Proves the lane
#     runner — config load, prompt composition, model dispatch — is
#     intact end-to-end after install.
#
# Environment expected from the host orchestrator:
#   VERSION  the version string injected via -ldflags during the
#            cross-build; install.sh fetches the matching tarball and
#            `idiom version` should print this exact string.
#
# Mounts expected:
#   /install.sh  the install.sh under test
#   /dist        directory containing idiom_${VERSION}_linux_${ARCH}.tar.gz
#                + SHA256SUMS

set -euo pipefail

: "${VERSION:?VERSION env not set; orchestrator must export it}"

#######################################
# phase 0 — setup
#######################################

setup_deps() {
  echo "[setup] installing curl, tar, python3, ca-certificates, git"
  export DEBIAN_FRONTEND=noninteractive
  apt-get update -qq >/dev/null
  apt-get install -y -qq --no-install-recommends \
    curl tar python3 ca-certificates git >/dev/null
}

stage_llama_server_stub() {
  # idiom's lane runner auto-starts the configured backend binary before
  # making LLM calls. The container doesn't ship a real llama-server, so
  # stage a no-op shim at /usr/local/bin/llama-server: exec'd, it sleeps
  # while idiom polls the configured port (already bound by our mock at
  # 127.0.0.1:9091), so the readiness check passes and the lane proceeds
  # to the LLM call. The shim is killed on container exit.
  echo "[setup] staging /usr/local/bin/llama-server stub"
  cat > /usr/local/bin/llama-server <<'STUB'
#!/bin/sh
# install-test stub: idiom auto-starts this; the real port is bound by
# the python mock at 127.0.0.1:9091 already, so we just need to keep a
# process alive for the lifetime of the lane invocation.
exec sleep 3600
STUB
  chmod 755 /usr/local/bin/llama-server
}

start_release_server() {
  echo "[setup] starting release http.server on 127.0.0.1:8080 serving /dist"
  cd /dist
  python3 -m http.server 8080 --bind 127.0.0.1 >/var/log/release-server.log 2>&1 &
  release_pid=$!
  cd /
  for _ in 1 2 3 4 5; do
    if curl -fsSL "http://127.0.0.1:8080/SHA256SUMS" >/dev/null 2>&1; then
      return 0
    fi
    sleep 0.5
  done
  echo "FAIL: release http.server did not come up on :8080" >&2
  cat /var/log/release-server.log >&2 || true
  exit 1
}

#######################################
# phase 1 — install
#######################################

run_install() {
  echo "[install] running install.sh against http://127.0.0.1:8080"
  export IDIOM_VERSION="${VERSION}"
  export IDIOM_RELEASE_URL_BASE="http://127.0.0.1:8080"
  export IDIOM_INSTALL_DIR="/usr/local/bin"
  sh /install.sh
}

verify_binary() {
  echo "[install] verifying /usr/local/bin/idiom is installed and executable"
  if [ ! -x /usr/local/bin/idiom ]; then
    echo "FAIL: idiom not installed at /usr/local/bin/idiom" >&2
    exit 1
  fi
  echo "[install] checking idiom version output matches injected ${VERSION}"
  version_out=$(/usr/local/bin/idiom version)
  echo "${version_out}"
  if ! grep -qF "version: ${VERSION}" <<<"${version_out}"; then
    echo "FAIL: idiom version did not print 'version: ${VERSION}'" >&2
    exit 1
  fi
}

verify_global_config() {
  echo "[install] verifying global ~/.idiom bootstrap artifacts"
  required=(
    "$HOME/.idiom/config.json"
    "$HOME/.idiom/backends.json"
    "$HOME/.idiom/external_tools/README.md"
    "$HOME/.idiom/postflight/checks/README.md"
    "$HOME/.idiom/prompts/base.md"
    "$HOME/.idiom/prompts/lanes/README.md"
    "$HOME/.idiom/prompts/rules/README.md"
    "$HOME/.idiom/prompts/profiles/coding.md"
    "$HOME/.idiom/prompts/profiles/claude-qwen-worker.md"
  )
  for path in "${required[@]}"; do
    if [ ! -f "$path" ]; then
      echo "FAIL: installer did not create required global artifact: $path" >&2
      exit 1
    fi
  done
  echo "[install] global config bootstrap OK"
}

verify_post_install_harness() {
  echo "[install] verifying installed harness can run from global config"
  if [ ! -f /test-post-install-harness.py ]; then
    echo "FAIL: /test-post-install-harness.py not mounted into install test container" >&2
    exit 1
  fi
  IDIOM_BIN=/usr/local/bin/idiom python3 /test-post-install-harness.py
}

verify_tools_profile() {
  echo "[install] verifying install-tools.sh co-installs sidecar launchers"
  if [ ! -f /install-tools.sh ]; then
    echo "FAIL: /install-tools.sh not mounted into install test container" >&2
    exit 1
  fi

  before_hash=$(sha256sum /usr/local/bin/idiom | awk '{print $1}')
  export IDIOM_VERSION="${VERSION}"
  export IDIOM_RELEASE_URL_BASE="http://127.0.0.1:8080"
  export IDIOM_INSTALL_DIR="/usr/local/bin"
  sh /install-tools.sh
  after_hash=$(sha256sum /usr/local/bin/idiom | awk '{print $1}')

  if [ "${before_hash}" != "${after_hash}" ]; then
    echo "FAIL: install-tools.sh changed existing /usr/local/bin/idiom" >&2
    exit 1
  fi
  if [ ! -x /usr/local/bin/idiom-tool ]; then
    echo "FAIL: idiom-tool not installed by tools profile" >&2
    exit 1
  fi
  if [ ! -x /usr/local/bin/idiom-mcp-discovery ]; then
    echo "FAIL: idiom-mcp-discovery not installed by tools profile" >&2
    exit 1
  fi
  /usr/local/bin/idiom-tool list --profile discovery >/dev/null \
    || { echo "FAIL: idiom-tool did not run after tools profile install" >&2; exit 1; }
}

verify_doctor() {
  echo "[doctor] running idiom doctor --json (expect non-zero exit; verify shape)"
  set +e
  /usr/local/bin/idiom doctor --json > /tmp/doctor.json
  doctor_exit=$?
  set -e
  echo "[doctor] exit=${doctor_exit}"
  python3 - <<'PY'
import json
with open("/tmp/doctor.json") as f:
    d = json.load(f)
assert "checks" in d, "doctor JSON missing 'checks'"
assert "summary" in d, "doctor JSON missing 'summary'"
assert isinstance(d["checks"], list) and d["checks"], "'checks' must be a non-empty list"
total = d["summary"]["pass"] + d["summary"]["warn"] + d["summary"]["fail"]
assert total == len(d["checks"]), f"summary count {total} != check count {len(d['checks'])}"
print(f"[doctor] OK: {len(d['checks'])} checks; summary={d['summary']}")
PY
}

#######################################
# phase 2 — tier 1: subcommand surface coverage
#######################################

# Subcommands listed in cmd/idiom/main.go's dispatch table. Order
# matches the dispatch order so changes to the table show up here too.
SURFACE_SUBCOMMANDS=(
  tool
  mcp
  context
  atom
  artifact
  implement
  plan-execute
  recover
  lane
  backend
  architect
  audit
  ranker
  brief
  probe
  queue
  local-ops
  product
  product-evidence
  release
  init
  skills
  version
  doctor
)

verify_subcommand_help() {
  local sub="$1"
  set +e
  out=$(idiom "$sub" --help 2>&1)
  exit_code=$?
  set -e
  # Conventional exit codes for help / usage:
  #   0 — explicit --help support
  #   2 — flag-parser usage on unknown / missing args
  # Anything else (panics, segfaults, runtime panics) is a real fail.
  if [ "${exit_code}" -ne 0 ] && [ "${exit_code}" -ne 2 ]; then
    echo "FAIL: idiom ${sub} --help exit=${exit_code} (expected 0 or 2)" >&2
    echo "--- output ---" >&2
    echo "${out}" >&2
    exit 1
  fi
  if [ -z "${out}" ]; then
    echo "FAIL: idiom ${sub} --help produced empty output" >&2
    exit 1
  fi
  echo "[surface] idiom ${sub} --help — exit ${exit_code}, ${#out} bytes"
}

verify_surface_coverage() {
  echo "[surface] tier 1: subcommand surface coverage (${#SURFACE_SUBCOMMANDS[@]} subcommands)"
  for sub in "${SURFACE_SUBCOMMANDS[@]}"; do
    verify_subcommand_help "${sub}"
  done
  echo "[surface] tier 1 OK: all subcommands reachable"
}

#######################################
# phase 3 — tier 2: static functionality (no LLM)
#######################################

verify_brief_lint() {
  echo "[static] idiom brief lint (validates python brief-preflight integration)"
  cat > /tmp/sample-brief.md <<'BRIEF'
# Sample atom

## Files To Modify

- src/main.go

## Verified Current State

`src/main.go:1`:

```go
package main
```

## Required Edits

### main.go

- src/main.go:1 — change `package main` to `package alpha`

## Acceptance

- Compiles with `go build`.
BRIEF
  set +e
  out=$(idiom brief lint /tmp/sample-brief.md 2>&1)
  exit_code=$?
  set -e
  echo "[static] brief lint exit=${exit_code}"
  # 0 = clean, 1 = lint warnings (still success — the lint ran), 2 = usage error
  if [ "${exit_code}" -eq 2 ]; then
    echo "FAIL: brief lint reported a usage error" >&2
    echo "${out}" >&2
    exit 1
  fi
  echo "[static] brief lint OK"
}

verify_lane_postflight() {
  echo "[static] idiom lane postflight (validates Phase 5 pipeline)"
  cat > /tmp/sample.diff <<'DIFF'
diff --git a/src/main.go b/src/main.go
index 0000000..1111111 100644
--- a/src/main.go
+++ b/src/main.go
@@ -1,3 +1,3 @@
 package main

-// nothing here
+// added a line
DIFF
  set +e
  out=$(idiom lane postflight --diff /tmp/sample.diff 2>&1)
  exit_code=$?
  set -e
  echo "[static] postflight exit=${exit_code}"
  # 0 = pass, 1 = IO/setup, 2 = flag error, 3 = checks fired (also success — the pipeline ran)
  if [ "${exit_code}" -eq 1 ] || [ "${exit_code}" -eq 2 ]; then
    echo "FAIL: postflight encountered IO/setup or flag error" >&2
    echo "${out}" >&2
    exit 1
  fi
  echo "[static] postflight OK"
}

verify_backend_list() {
  echo "[static] idiom backend list (validates Phase 4 resolver)"
  mkdir -p /tmp/backend-test/.idiom
  cat > /tmp/backend-test/.idiom/backends.json <<'JSON'
{
  "profiles": {
    "local": {
      "models": [
        {"alias": "test-coder-7b", "roles": ["executor"]},
        {"alias": "test-planner-30b", "roles": ["planner"]}
      ]
    }
  }
}
JSON
  cd /tmp/backend-test
  set +e
  out=$(idiom backend list 2>&1)
  exit_code=$?
  set -e
  cd /
  echo "[static] backend list exit=${exit_code}"
  if [ "${exit_code}" -ne 0 ]; then
    echo "FAIL: backend list exit=${exit_code}" >&2
    echo "${out}" >&2
    exit 1
  fi
  if ! grep -qF "test-coder-7b" <<<"${out}"; then
    echo "FAIL: backend list did not surface staged alias 'test-coder-7b'" >&2
    echo "${out}" >&2
    exit 1
  fi
  echo "[static] backend list OK"
}

verify_static_functionality() {
  echo "[static] tier 2: static functionality (no LLM)"
  verify_brief_lint
  verify_lane_postflight
  verify_backend_list
  echo "[static] tier 2 OK"
}

#######################################
# phase 4 — tier 3: stub-LLM lane
#######################################

start_mock_llm() {
  echo "[mock] writing mock chat-completions server"
  cat > /tmp/mock-llm.py <<'PY'
"""Minimal OpenAI chat-completions mock for the install test.

Records every POST it receives by touching /tmp/mock-llm-received and
appending a one-line summary to /tmp/mock-llm.log. Returns a canonical
chat-completion shape with a plain assistant message — no tool calls,
no streaming. The lane runner under test is expected to make at least
one request; the assertion downstream checks for the sentinel file.
"""
from http.server import BaseHTTPRequestHandler, HTTPServer
import json
import datetime


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *args, **kwargs):
        return  # silence default access log; we keep our own

    def do_GET(self):
        # idiom's lane runner pings a health/models endpoint to confirm
        # the backend is reachable before issuing chat completions. A
        # generic 200 OK with a stub /v1/models payload satisfies both
        # llama-server's readiness check (returns 200 on /health) and
        # the OpenAI client's model listing (returns the configured
        # alias on /v1/models). Path is ignored — any GET passes.
        with open("/tmp/mock-llm.log", "a") as audit:
            audit.write(
                f"{datetime.datetime.utcnow().isoformat()}Z GET {self.path}\n"
            )
        body = json.dumps({
            "object": "list",
            "data": [
                {"id": "test-model", "object": "model", "created": 1, "owned_by": "install-test"}
            ],
        }).encode("utf-8")
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_POST(self):
        length = int(self.headers.get("Content-Length", "0") or "0")
        try:
            body = self.rfile.read(length).decode("utf-8", errors="replace")
        except Exception:
            body = ""
        with open("/tmp/mock-llm-received", "w") as sentinel:
            sentinel.write(body[:512])
        with open("/tmp/mock-llm.log", "a") as audit:
            audit.write(
                f"{datetime.datetime.utcnow().isoformat()}Z POST {self.path} {length}B\n"
            )
        response = {
            "id": "chatcmpl-install-test",
            "object": "chat.completion",
            "created": 1,
            "model": "test-model",
            "choices": [
                {
                    "index": 0,
                    "message": {
                        "role": "assistant",
                        "content": "(install-test mock LLM: no-op response)",
                    },
                    "finish_reason": "stop",
                }
            ],
            "usage": {
                "prompt_tokens": 10,
                "completion_tokens": 5,
                "total_tokens": 15,
            },
        }
        payload = json.dumps(response).encode("utf-8")
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(payload)))
        self.end_headers()
        self.wfile.write(payload)


if __name__ == "__main__":
    HTTPServer(("127.0.0.1", 9091), Handler).serve_forever()
PY
  echo "[mock] starting mock LLM on 127.0.0.1:9091"
  python3 /tmp/mock-llm.py >/var/log/mock-llm.log 2>&1 &
  mock_pid=$!
  for _ in 1 2 3 4 5; do
    # POST to / with empty body — the mock accepts any path.
    if curl -fsS -X POST -d '{}' "http://127.0.0.1:9091/" >/dev/null 2>&1; then
      return 0
    fi
    sleep 0.5
  done
  echo "FAIL: mock LLM did not come up on :9091" >&2
  cat /var/log/mock-llm.log >&2 || true
  exit 1
}

verify_lane_against_mock() {
  echo "[mock] tier 4: stub-LLM lane test"
  local ws=/tmp/install-test-project
  rm -rf "${ws}"
  mkdir -p "${ws}/.idiom"
  cat > "${ws}/.idiom/config.json" <<JSON
{
  "app_name": "install-test",
  "llama_server": {
    "kind": "llama",
    "host": "127.0.0.1",
    "port": 9091,
    "model": "test-model"
  },
  "workspace": {
    "root": "${ws}",
    "cwd": "${ws}",
    "shell": "/bin/bash",
    "command_timeout_seconds": 30,
    "max_output_bytes": 65536,
    "enable_writes": true
  }
}
JSON
  cat > "${ws}/brief.md" <<'BRIEF'
# Install-test brief

This brief is consumed by the Docker install test against a mock LLM.
The mock returns a trivial assistant response, so the lane will not
land edits — the test asserts only that idiom reached the LLM call.

## Files To Modify

- README.txt

## Verified Current State

`README.txt:1`:

```text
init
```

## Required Edits

### README.txt

- README.txt:1 — append a sentinel line.

## Acceptance

- Pass condition: the mock LLM received at least one POST request.
BRIEF
  echo "init" > "${ws}/README.txt"

  rm -f /tmp/mock-llm-received
  rm -f /tmp/mock-llm.log

  echo "[mock] running idiom lane implement against mock (30s timeout)"
  cd "${ws}"
  set +e
  timeout 30 idiom lane implement --allow-stale-brief --title install-test brief.md \
    >/tmp/lane.stdout.log 2>/tmp/lane.stderr.log
  lane_exit=$?
  set -e
  cd /
  echo "[mock] lane exit=${lane_exit}"

  if [ ! -f /tmp/mock-llm-received ]; then
    echo "FAIL: mock LLM did not receive any requests during the lane run" >&2
    echo "       (this means the lane runner did not reach the model dispatch step;" >&2
    echo "       check config loading, prompt composition, or pre-dispatch gates)" >&2
    echo "--- lane stdout (last 80) ---" >&2
    tail -80 /tmp/lane.stdout.log >&2 || true
    echo "--- lane stderr (last 80) ---" >&2
    tail -80 /tmp/lane.stderr.log >&2 || true
    exit 1
  fi

  request_count=$(wc -l < /tmp/mock-llm.log 2>/dev/null || echo 0)
  echo "[mock] tier 4 OK: lane runner reached LLM call (${request_count} request(s) recorded)"
}

#######################################
# main
#######################################

main() {
  setup_deps
  start_release_server

  echo
  echo "==> phase 1: install"
  run_install
  verify_binary
  verify_global_config
  verify_post_install_harness
  verify_tools_profile
  verify_doctor

  echo
  echo "==> phase 2: tier 1 (subcommand surface coverage)"
  verify_surface_coverage

  echo
  echo "==> phase 3: tier 2 (static functionality)"
  verify_static_functionality

  echo
  echo "==> phase 4: tier 4 (stub-LLM lane)"
  stage_llama_server_stub
  start_mock_llm
  verify_lane_against_mock

  echo
  echo "==> all tiers passed"
}

main "$@"
