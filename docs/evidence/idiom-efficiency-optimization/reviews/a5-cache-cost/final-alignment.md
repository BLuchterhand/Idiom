[2m  ▎ thinking[0m
  · 17057 tok · in 16874 (16640 cached / 234 new) · out 183 (68 reasoning) · ¥0.0009
  -> read_file {"path": "docs/design-hosted-cache-cost-metrics.md"}
  -> bash {"command": "cd /Users/ben/Documents/GitHub/idiom && git log --oneline -20 -- docs/design-hosted-cache-cost-metrics.md"}
[2m  ▎ thinking[0m
Let me check for any prior review artifacts or tracked risks.
  · 23679 tok · in 21528 (17152 cached / 4376 new) · out 2151 (1900 reasoning) · ¥0.0090 · cache prefix changed: tools
  -> bash {"command": "cd /Users/ben/Documents/GitHub/idiom && git log --all --oneline -30 -- docs/design-hosted-cache-cost-metric...
  -> grep {"pattern": "cache_adjusted|CacheAdjusted|adjusted.tokens", "path": "/Users/ben/Documents/GitHub/idiom/docs/design-hoste...
  -> grep {"pattern": "known.zero|known-zero|both.*fields.*present|both.*cache.*field", "path": "/Users/ben/Documents/GitHub/idiom...
[2m  ▎ thinking[0m
  · 24234 tok · in 23905 (23552 cached / 353 new) · out 329 (87 reasoning) · ¥0.0015
  -> grep {"path": "/Users/ben/Documents/GitHub/idiom/docs/design-hosted-cache-cost-metrics.md", "pattern": "stream|non.runner|bac...
  -> grep {"path": "/Users/ben/Documents/GitHub/idiom/docs/design-hosted-cache-cost-metrics.md", "pattern": "TurnSummary"}
  -> grep {"path": "/Users/ben/Documents/GitHub/idiom/docs/design-hosted-cache-cost-metrics.md", "pattern": "backward|compatible|e...
[2m  ▎ thinking[0m
  · 24835 tok · in 24649 (24192 cached / 457 new) · out 186 (38 reasoning) · ¥0.0013
  -> bash {"command": "cd /Users/ben/Documents/GitHub/idiom && grep -n '^- `[A-Z][A-Za-z]*`' docs/design-hosted-cache-cost-metrics...
  -> bash {"command": "cd /Users/ben/Documents/GitHub/idiom && grep -n '^- `prompt_cache_` docs/design-hosted-cache-cost-metrics.m...
  ⊘ bash command exited: exit status 2
[2m  ▎ thinking[0m
  · 25043 tok · in 24950 (24832 cached / 118 new) · out 93 (7 reasoning) · ¥0.0008
  -> grep {"path": "/Users/ben/Documents/GitHub/idiom/docs/design-hosted-cache-cost-metrics.md", "pattern": "`prompt_cache_"}
[2m  ▎ thinking[0m
## Final Consolidated Alignment Review

### Verdict: **PASS**

All seven prior-review items are fully and correctly addressed in `docs/design-hosted-cache-cost-metrics.md`:

| Item | Status | Evidence |
|------|--------|----------|
| **Remove `cache_adjusted` runtime field** | ✅ Closed | No `cache_adjusted` / `CacheAdjusted` field defined in any contract. Line 149 explicitly states derived values belong in benchmark/reporting scripts, not runtime JSONL. Line 232 mitigates the risk of confusing "adjusted tokens" with currency — naming is confined to benchmark artifacts. |
| **Add `prompt_cache_accounting_known`** | ✅ Present | Go struct field `PromptCacheAccountingKnown` (lines 88, 128); JSONL field `prompt_cache_accounting_known` (lines 144, 218, 222). Referenced in validation tests (line 117), runner aggregation OR-semantics (line 131), and risks section (line 236). |
| **Known-zero semantics as both fields present** | ✅ Defined | Lines 92–93: `true` only when both cache hit and miss accounting fields are explicitly present. Lines 103–105: parser uses field-presence, not numeric values. Line 117: tests assert `PromptCacheAccountingKnown=true` for known-zero. |
| **Streaming / non-runner limitations** | ✅ Documented | Lines 109–111: non-streaming `CompleteChatMessage` only; no streaming coverage claimed. Lines 163–166: non-runner calls, background compaction, streaming-only paths are out of scope. |
| **Debug transcript TurnSummary** | ✅ Covered | Line 159: `ModelCall, TurnSummary`/budget ledger payloads named explicitly. A5.3 targets `internal/evidence/transcript/debuglog.go` and acceptance requires transcript summaries include cache facts. |
| **Backward-compatible reader tests** | ✅ Addressed | Line 42: goal to preserve existing readers. Section 4 lists four reader packages with explicit "accept new fields, not require them" contract. A5.3 acceptance: no breakage on old summaries. A5.4: fixture coverage includes one legacy and one new summary. |
| **Consistent Go identifier naming** | ✅ Convention followed | PascalCase exported fields: `PromptCacheHitTokens`, `PromptCacheMissTokens`, `PromptCacheAccountingKnown`, `ReasoningTokens` (lines 86–89, repeated 126–129). Snake_case JSONL fields: all prefixed `prompt_cache_` (lines 141–145, 216–224). Consistent throughout. |

### Parallel Dispatch Feasibility

The four implementation atoms form a pipeline but contain a natural fork:

```
A5.1 Provider Parsing  ── prerequisite ──>  A5.2 Runner Metrics
                                                │
                          A5.4 Benchmark Adapter ┘  (needs only settled JSONL contract, not A5.2 code)
                          A5.3 TUI/Transcript ────  (needs A5.2 event payload shape)
```

**Yes, partial parallelism is safe:**
- **A5.1** first (sole prerequisite — delivers the `llama.Usage` struct and parser).
- **A5.2** and **A5.4** can be dispatched in parallel after A5.1: A5.2 wires the runner aggregation, while A5.4 works from the settled JSONL contract and can use fixture data or the existing `model_call` event shape.
- **A5.3** depends on A5.2's event payload, so it must follow A5.2.
- All four atoms can be verified independently with focused `go test -run` commands as specified in the verification plan.
  · 27419 tok · in 25857 (24960 cached / 897 new) · out 1562 (755 reasoning) · ¥0.0045
