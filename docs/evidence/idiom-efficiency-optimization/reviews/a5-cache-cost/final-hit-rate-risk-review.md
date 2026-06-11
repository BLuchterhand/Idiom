[2m  ▎ thinking[0m
  · 17073 tok · in 16867 (13312 cached / 3555 new) · out 206 (72 reasoning) · ¥0.0042
  -> read_file {"path": "internal/surfaces/tui/app/model_state.go"}
  -> read_file {"path": "internal/evidence/transcript/debuglog.go"}
  -> read_file {"path": "internal/surfaces/tui/app/session_summary_test.go"}
  · tool output truncated: 28520 of 61288 bytes elided
[2m  ▎ thinking[0m
## Review: A5 Implementation-Review Risk (suppressed zero cache hit rate)

### Files Reviewed

1. **`internal/surfaces/tui/app/model_state.go`** — `writeSessionSummary()` (lines 1415–1471)
2. **`internal/evidence/transcript/debuglog.go`** — `LogSessionSummary()` (line 514)
3. **`internal/surfaces/tui/app/session_summary_test.go`** — `TestModelCloseSessionSummaryEmitsZeroCacheHitRate` (lines 81–116)

### Risk Analysis

**Original risk**: when `prompt_cache_hit_tokens = 0` and `prompt_cache_miss_tokens > 0`, the `prompt_cache_hit_rate` field was suppressed entirely because the guard used `hitRate > 0` instead of `hit + miss > 0`.

**Current condition in `model_state.go` (lines 1467–1471)**:
```go
if m.sessionAgg.promptCacheAccountingKnown {
    if m.sessionAgg.promptCacheHitTokens+m.sessionAgg.promptCacheMissTokens > 0 {
        summary["prompt_cache_hit_rate"] = promptCacheHitRate
    }
}
```
- `promptCacheHitRate` is computed as `0.0` for hit=0, miss=13 (line 1416–1420).
- The guard checks `accountingKnown && (hit+miss > 0)` — correct; emits `0.0` when cache accounting is known and total > 0.

**Current condition in `debuglog.go` (line 514)**:
```go
if s.PromptCacheAccountingKnown && s.PromptCacheHitTokens+s.PromptCacheMissTokens > 0 {
    record["prompt_cache_hit_rate"] = roundPct(s.PromptCacheHitRate)
}
```
- Identical correct guard. `roundPct(0.0) = 0.0`.

**Test coverage (`session_summary_test.go`, lines 81–116)**:
- Sets up hit=0, miss=13, accounting_known=true via one `model_call` event.
- Asserts `prompt_cache_accounting_known == true`, `prompt_cache_hit_tokens == 0`, `prompt_cache_miss_tokens == 13`.
- Asserts `prompt_cache_hit_rate` **exists** as a `float64` and **equals 0** — explicitly testing the emission, not just the absence.

### Verdict

All three boundaries (computation, JSON event stream, debug log persistence) use the correct condition `accountingKnown && (hit+miss > 0)` — not the old `hitRate > 0` pattern. The test directly covers the edge case and asserts the field is present with value 0.

No residual risk: the fix is complete, correctly duplicated to both output paths, and regression-tested.

**Result: PASS**
  · 38421 tok · in 36632 (13312 cached / 23320 new) · out 1789 (1163 reasoning) · ¥0.0272 · cache prefix changed: tools
