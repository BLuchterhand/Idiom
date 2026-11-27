#!/usr/bin/env bash
# Runs one rerank arm of the localization eval and judges it against a baseline.
#
#   ARM=rerank BACKENDS=cross  CASES=... REPO=... BASELINES=... bash scripts/g885-rerank-arm.sh
#
# Required env:
#   ARM        arm name (output files are <WORK>/<ARM>.*)
#   CASES      case file (usage-v1 pc-cases.json, or the Mac's)
#   REPO       git clone the cases' bases come from
#   BASELINES  comma-separated baseline score CSVs (frozen e324acb1)
# Optional env:
#   BACKENDS   cross | cross,embed | chat | anthropic (empty: installed ranking, no rerank)
#   RERANK_DRY 1 to estimate tokens only (no backend is called)
#   WORK       output directory (default .cache/g885/<ARM>)
#   IDIOM_BIN, EVAL_BIN  built binaries (default .cache/g885/bin/idiom.exe, loc-eval.exe)
#   INDEX_CACHE index cache directory (default .cache/g885/index)
# Backend endpoints and models come from IDIOM_RERANK_{CROSS,EMBED,CHAT}_URL / _MODEL;
# the anthropic arm reads the key file named by BENTECH_API_KEY_FILE.
set -u
: "${ARM:?}" "${CASES:?}" "${REPO:?}" "${BASELINES:?}"
HERE="$(cd "$(dirname "$0")/.." && pwd)"
WORK="${WORK:-$HERE/.cache/g885/$ARM}"
IDIOM_BIN="${IDIOM_BIN:-$HERE/.cache/g885/bin/idiom.exe}"
EVAL_BIN="${EVAL_BIN:-$HERE/.cache/g885/bin/loc-eval.exe}"
INDEX_CACHE="${INDEX_CACHE:-$HERE/.cache/g885/index}"
mkdir -p "$WORK" "$INDEX_CACHE"

rerank_flags=()
if [ -n "${BACKENDS:-}" ]; then
  rerank_flags=(-rerank "$BACKENDS" -rerank-dir "$WORK")
  [ "${RERANK_DRY:-0}" = 1 ] && rerank_flags+=(-rerank-dry)
fi

# timed-calls 1: the warm-up call makes the real backend call, the timed call is a cache hit.
timeout 14400 "$EVAL_BIN" run -repo "$REPO" -cases "$CASES" -idiom-bin "$IDIOM_BIN" \
  -cache "$INDEX_CACHE" -subset all -timed-calls 1 "${rerank_flags[@]}" \
  -out "$WORK/run.json" > "$WORK/run.log" 2>&1
echo "run exit=$?"
"$EVAL_BIN" case-scores -run "$WORK/run.json" -cases "$CASES" -out "$WORK/scores.csv" || exit 1

for split in dev holdout; do
  echo "== $ARM vs baseline, $split"
  "$EVAL_BIN" compare-scores -split "$split" -before "$BASELINES" -after "$WORK/scores.csv"
done

[ -f "$WORK/usage.jsonl" ] && "$EVAL_BIN" rerank-report -log "$WORK/usage.jsonl" \
  ${IN_USD:+-in-usd-per-mtok "$IN_USD"} ${OUT_USD:+-out-usd-per-mtok "$OUT_USD"}
exit 0
