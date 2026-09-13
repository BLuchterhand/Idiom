#!/usr/bin/env bash
set -euo pipefail

# Scan the prospective source tree, including new files but excluding ignored
# local state, then every reachable Git revision. Never print secret values.
cd "$(git rev-parse --show-toplevel)"
scanner="${GITLEAKS_BIN:-gitleaks}"
command -v "$scanner" >/dev/null || {
  echo "gitleaks is required; see docs/release.md" >&2
  exit 1
}
scan_dir="$(mktemp -d)"
trap 'rm -rf "$scan_dir"' EXIT
git ls-files --cached --others --exclude-standard -z |
  python3 -c 'import os, shutil, sys
from pathlib import Path
root = Path.cwd()
target = Path(sys.argv[1])
for name in set(sys.stdin.buffer.read().split(b"\0")):
    if not name:
        continue
    relative = Path(os.fsdecode(name))
    source = root / relative
    if source.is_symlink():
        continue
    if not source.is_file():
        continue  # A tracked deletion is not part of the prospective release.
    destination = target / relative
    destination.parent.mkdir(parents=True, exist_ok=True)
    shutil.copyfile(source, destination)
' "$scan_dir"
"$scanner" dir "$scan_dir" --config .gitleaks.toml --redact --no-banner
"$scanner" git . --config .gitleaks.toml --log-opts="--all" --redact --no-banner
