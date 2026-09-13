#!/usr/bin/env bash
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

# Dependencies selected by build tags differ across supported targets. Preserve
# the notices for every shipped binary, including the linked Go runtime.
for target in darwin/amd64 darwin/arm64 linux/amd64 linux/arm64 windows/amd64; do
  IFS=/ read -r target_os target_arch <<< "$target"
  GOOS="$target_os" GOARCH="$target_arch" CGO_ENABLED=0 \
    go-licenses save ./cmd/idiom --ignore idiom --force \
      --save_path "third-party-licenses/${target_os}_${target_arch}"
done
mkdir -p third-party-licenses/go
cp -f "$(go env GOROOT)/LICENSE" third-party-licenses/go/LICENSE
python3 - <<'PY'
import hashlib
import json
from pathlib import Path

root = Path("third-party-licenses")
notices = {
    path.as_posix(): hashlib.sha256(path.read_bytes()).hexdigest()
    for path in sorted(root.rglob("*"))
    if path.is_file() and path.name != "manifest.json"
}
(root / "manifest.json").write_text(json.dumps(notices, indent=2) + "\n")
PY
