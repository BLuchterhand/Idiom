#!/usr/bin/env bash
# G884: score the past-work channel on usage-eval v1 (one machine's cases) and
# judge it against the frozen e324acb1 scores.
#
#   bash scripts/g884-eval.sh -r <bentech clone> -c <cases.json> -b <baseline.csv>[,<more.csv>] [options]
#
# It builds one idiom binary from this checkout (with the eval-only timeout
# patch applied through `go build -overlay`, so the tree stays clean), runs the
# cases twice with it, once with the channel off (`-history {"disabled":true}`)
# and once with it on, writes a per-case score CSV for each arm, and prints
# compare-scores for dev and hold-out:
#   candidate vs the frozen e324acb1 CSVs, and candidate vs the channel-off arm.
# Case files, scores and queries stay in -o (default .cache/g884); never commit them.
#
# Options:
#   -r DIR   git clone of the repository the cases came from (required)
#   -c FILE  usage-eval case file (required)
#   -b CSVS  frozen baseline score CSV(s), comma-separated (required)
#   -a CSVS  other machines' candidate CSVs for the hold-out join (optional)
#   -o DIR   output and cache directory (default .cache/g884)
#   -H JSON  history section for the candidate arm (default {} = shipped defaults)
#   -s SET   dev, holdout or all (default all)
#   -t N     timed calls per case after the warm-up (default 1; 0 for a fast sweep)
#   -n NAME  arm name used in file names (default cand)
#   -S       skip the channel-off arm
set -u
set -o pipefail

repo=""; cases=""; baseline=""; others=""; out=".cache/g884"; hist="{}"; subset="all"; timed=1; name="cand"; skipoff=0
while getopts "r:c:b:a:o:H:s:t:n:S" opt; do
  case "$opt" in
    r) repo="$OPTARG" ;; c) cases="$OPTARG" ;; b) baseline="$OPTARG" ;; a) others="$OPTARG" ;;
    o) out="$OPTARG" ;; H) hist="$OPTARG" ;; s) subset="$OPTARG" ;; t) timed="$OPTARG" ;;
    n) name="$OPTARG" ;; S) skipoff=1 ;;
    *) exit 2 ;;
  esac
done
[ -n "$repo" ] && [ -n "$cases" ] && [ -n "$baseline" ] || { sed -n '2,28p' "$0"; exit 2; }

root="$(cd "$(dirname "$0")/.." && pwd)"
mkdir -p "$out" "$out/bin" "$out/runs"
out="$(cd "$out" && pwd)"
exe=""; case "$(uname -s)" in MINGW*|MSYS*|CYGWIN*) exe=".exe" ;; esac

# Eval-only build: context_impact's 30 s and context_index's 5 min dispatch
# timeouts rise to 5 and 40 min (BenTech queries take 6-29 s here). Ranking code is untouched.
meta="internal/capabilities/workspace/tool_surface_metadata.go"
if [ ! -x "$out/bin/idiom$exe" ] || [ -n "$(find "$root/internal" "$root/cmd" -name '*.go' -newer "$out/bin/idiom$exe" | head -1)" ]; then
  node -e '
    const fs=require("fs");const [src,dst]=process.argv.slice(1);let s=fs.readFileSync(src,"utf8");
    const a="{Tool: ToolNameContextImpact, Handler: (*Manager).contextImpact,";
    if(!s.includes(a))process.exit(3);
    s=s.replace(a,a+" Timeout: 5 * time.Minute,");
    const i="{Tool: ToolNameContextIndex, Handler: (*Manager).contextIndex,";const k=s.indexOf(i);const e=s.indexOf("\n",k);
    const line=s.slice(k,e);if(k<0||!line.includes("Timeout: 5 * time.Minute}"))process.exit(4);
    s=s.slice(0,k)+line.replace("Timeout: 5 * time.Minute}","Timeout: 40 * time.Minute}")+s.slice(e);
    fs.writeFileSync(dst,s);' "$root/$meta" "$out/patched_metadata.go" || { echo "timeout patch failed"; exit 1; }
  node -e 'const p=require("path");const r=process.argv[1],o=process.argv[2];
    require("fs").writeFileSync(o+"/overlay.json",JSON.stringify({Replace:{[p.resolve(r,process.argv[3])]:o+"/patched_metadata.go"}}))' "$root" "$out" "$meta"
  (cd "$root" && go build -overlay "$out/overlay.json" -o "$out/bin/idiom$exe" ./cmd/idiom \
    && go build -o "$out/bin/loc-eval$exe" ./cmd/idiom-localization-eval) || { echo "build failed"; exit 1; }
fi
evalbin="$out/bin/loc-eval$exe"; idiom="$out/bin/idiom$exe"
echo "built from $(git -C "$root" rev-parse --short HEAD)"

arm() { # arm <tag> <history json>
  local tag="$1" h="$2"
  [ -s "$out/runs/$tag.json" ] || timeout 28800 "$evalbin" run -repo "$repo" -cases "$cases" -idiom-bin "$idiom" \
    -cache "$out/cache" -subset "$subset" -timed-calls "$timed" -history "$h" -out "$out/runs/$tag.json" > "$out/runs/$tag.log" 2>&1 || return 1
  "$evalbin" case-scores -run "$out/runs/$tag.json" -cases "$cases" -out "$out/runs/$tag.csv"
}

[ "$skipoff" = 1 ] || arm "off" '{"disabled":true}' || { echo "channel-off arm failed, see $out/runs/off.log"; exit 1; }
arm "$name" "$hist" || { echo "candidate arm failed, see $out/runs/$name.log"; exit 1; }

after="$out/runs/$name.csv"; [ -n "$others" ] && after="$after,$others"
splits="dev holdout"; [ "$subset" = all ] || splits="$subset"
for split in $splits; do
  echo "== $split: $name vs frozen e324acb1"
  "$evalbin" compare-scores -split "$split" -before "$baseline" -after "$after" 2>&1 | sed 's/^/   /'
  if [ "$skipoff" = 0 ]; then
    echo "== $split: $name vs channel-off arm (same binary)"
    "$evalbin" compare-scores -split "$split" -before "$out/runs/off.csv" -after "$out/runs/$name.csv" 2>&1 | sed 's/^/   /'
  fi
done
