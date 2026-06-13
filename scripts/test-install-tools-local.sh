#!/usr/bin/env bash
# Local smoke test for install-tools.sh.

set -euo pipefail

repo_root=$(cd "$(dirname "$0")/.." && pwd)

case "$(uname -s)" in
  Darwin) goos=darwin ;;
  Linux) goos=linux ;;
  *) echo "skip: install-tools local smoke supports macOS/Linux hosts only"; exit 0 ;;
esac

case "$(uname -m)" in
  x86_64|amd64) goarch=amd64 ;;
  arm64|aarch64) goarch=arm64 ;;
  *) echo "skip: unsupported host arch $(uname -m)"; exit 0 ;;
esac

version="${IDIOM_TEST_VERSION:-0.0.0-tools-test}"
asset="idiom_${version}_${goos}_${goarch}.tar.gz"
port="${IDIOM_TEST_PORT:-18081}"

file_sha256() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

dist=$(mktemp -d)
install_dir=$(mktemp -d)
existing_idiom="$install_dir/idiom"
server_log=$(mktemp)
server_pid=""
cleanup() {
  if [[ -n "$server_pid" ]]; then
    kill "$server_pid" 2>/dev/null || true
  fi
  rm -rf "$dist" "$install_dir" "$server_log"
}
trap cleanup EXIT

echo "==> building idiom for ${goos}/${goarch}"
(
  cd "$repo_root"
  GOOS="$goos" GOARCH="$goarch" CGO_ENABLED=0 go build \
    -ldflags "-s -w -X main.version=${version} -X main.commit=tools-test" \
    -o "$dist/idiom" ./cmd/idiom
)

echo "==> packaging ${asset}"
(cd "$dist" && tar -czf "$asset" idiom && rm idiom)

echo "==> generating SHA256SUMS"
if command -v sha256sum >/dev/null 2>&1; then
  (cd "$dist" && sha256sum "$asset" > SHA256SUMS)
else
  (cd "$dist" && shasum -a 256 "$asset" > SHA256SUMS)
fi

echo "==> serving local release assets on 127.0.0.1:${port}"
(cd "$dist" && python3 -m http.server "$port" --bind 127.0.0.1 >"$server_log" 2>&1) &
server_pid=$!
for _ in 1 2 3 4 5 6 7 8 9 10; do
  if curl -fsSL "http://127.0.0.1:${port}/SHA256SUMS" >/dev/null 2>&1; then
    break
  fi
  sleep 0.3
done
curl -fsSL "http://127.0.0.1:${port}/SHA256SUMS" >/dev/null || {
  cat "$server_log" >&2 || true
  echo "FAIL: local release server did not start" >&2
  exit 1
}

echo "==> running install-tools.sh"
cat > "$existing_idiom" <<'SH'
#!/bin/sh
echo pre-existing idiom
SH
chmod 755 "$existing_idiom"
existing_idiom_hash=$(file_sha256 "$existing_idiom")

IDIOM_VERSION="$version" \
IDIOM_RELEASE_URL_BASE="http://127.0.0.1:${port}" \
IDIOM_INSTALL_DIR="$install_dir" \
  sh "$repo_root/install-tools.sh"

[[ -x "$install_dir/idiom-tool" ]] || { echo "FAIL: idiom-tool not installed"; exit 1; }
[[ -x "$install_dir/idiom-mcp-discovery" ]] || { echo "FAIL: idiom-mcp-discovery not installed"; exit 1; }
[[ -x "$install_dir/idiom-tool-bin" ]] || { echo "FAIL: sidecar binary not installed"; exit 1; }
[[ -x "$install_dir/idiom" ]] || { echo "FAIL: pre-existing idiom was removed"; exit 1; }
after_idiom_hash=$(file_sha256 "$install_dir/idiom")
[[ "$after_idiom_hash" == "$existing_idiom_hash" ]] || { echo "FAIL: tools installer modified pre-existing idiom"; exit 1; }

tool_list="$("$install_dir/idiom-tool" list --profile discovery)"
echo "$tool_list" | grep -q '^context_status$' \
  || { echo "FAIL: idiom-tool list did not include context_status"; exit 1; }

status_json="$("$install_dir/idiom-tool" context_status --profile discovery --input '{"limit":1}')"
STATUS_JSON="$status_json" python3 - <<'PY'
import json
import os

payload = json.loads(os.environ["STATUS_JSON"])
sidecar = payload["sidecar"]
assert sidecar["schema_version"] == "idiom.tool.v1", sidecar
assert sidecar["profile"] == "discovery", sidecar
assert sidecar["contract"]["transport"] == "cli", sidecar
assert sidecar["error"] is None, sidecar
print("sidecar OK")
PY

echo "==> install-tools local smoke passed"
