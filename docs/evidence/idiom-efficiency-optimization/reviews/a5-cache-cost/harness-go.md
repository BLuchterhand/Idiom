[2m  ▎ thinking[0m
  · 16936 tok · in 16823 (16768 cached / 55 new) · out 113 (59 reasoning) · ¥0.0006
  -> read_file {"path": "docs/design-hosted-cache-cost-metrics.md"}
[2m  ▎ thinking[0m
Let me examine the actual code structures that would be affected by this design.
  · 21253 tok · in 20883 (17152 cached / 3731 new) · out 370 (142 reasoning) · ¥0.0048 · cache prefix changed: tools
  -> mcp__codegraph__context {"task": "llama.Usage struct, runnerMetrics, runnerBudgetLedger, sessionAgg, model_call event emission, TUI model_state ...
  -> grep {"pattern": "type Usage struct", "path": "internal/runtime/llama"}
  -> grep {"pattern": "type runnerMetrics struct", "path": "internal/runtime/agent"}
[2m  ▎ thinking[0m
  · 22628 tok · in 22377 (21248 cached / 1129 new) · out 251 (37 reasoning) · ¥0.0021
  -> read_file {"path": "internal/runtime/llama/client.go", "offset": 100, "limit": 30}
  -> grep {"path": "internal/runtime/agent", "pattern": "type runnerBudgetLedger struct"}
  -> grep {"path": "internal/runtime/llama", "pattern": "prompt_cache_hit_tokens|prompt_cache_miss_tokens|reasoning_tokens|complet...
[2m  ▎ thinking[0m
  · 23247 tok · in 23003 (22528 cached / 475 new) · out 244 (33 reasoning) · ¥0.0014
  -> read_file {"path": "internal/runtime/agent/runner_state.go", "offset": 205, "limit": 70}
  -> grep {"path": "internal/runtime/agent", "pattern": "emitModelCallTraceWithOptions|model_call|ModelCallEvent"}
  -> grep {"path": "internal/surfaces/tui/app", "pattern": "model_call|ModelCall|sessionAgg|sessionSummary|SessionSummary"}
[2m  ▎ thinking[0m
  · 31106 tok · in 30868 (23168 cached / 7700 new) · out 238 (28 reasoning) · ¥0.0086
  -> read_file {"limit": 80, "offset": 34, "path": "internal/surfaces/tui/app/model_state.go"}
  -> read_file {"limit": 30, "offset": 6087, "path": "internal/runtime/agent/runner.go"}
  -> grep {"path": "internal/evidence/transcript", "pattern": "ModelCall|SessionSummary"}
[2m  ▎ thinking[0m
  · 33544 tok · in 33283 (31104 cached / 2179 new) · out 261 (34 reasoning) · ¥0.0033
  -> read_file {"limit": 40, "offset": 6100, "path": "internal/runtime/agent/runner.go"}
  -> read_file {"limit": 60, "offset": 109, "path": "internal/evidence/transcript/debuglog.go"}
  -> read_file {"limit": 50, "offset": 1172, "path": "internal/surfaces/tui/app/model_state.go"}
[2m  ▎ thinking[0m
  · 35685 tok · in 35485 (33536 cached / 1949 new) · out 200 (38 reasoning) · ¥0.0030
  -> explore {"task": "In the idiom project, look at how DeepSeek API responses are parsed in internal/runtime/llama/client.go. Find:...
  -> read_file {"path": "internal/runtime/llama/client.go"}
  -> glob {"pattern": "internal/runtime/llama/*.go"}
  -> read_file {"path": "internal/runtime/llama/deepseek_test.go"}
  -> grep {"pattern": "hostedChatPayload|hostedChatRequestPayload|hostedChatResponse", "path": "internal/runtime/llama"}
  -> grep {"pattern": "DeepSeek|deepseek|DeepSeekResponse|deepseekResponse", "path": "internal/runtime/llama"}
  -> read_file {"path": "internal/runtime/llama/client.go", "offset": 455, "limit": 200}
[2m  ▎ thinking[0m
  · 37077 tok · in 36852 (35584 cached / 1268 new) · out 225 (35 reasoning) · ¥0.0024
  -> glob {"pattern": "internal/runtime/llama/deepseek_test.go"}
  -> grep {"path": "internal/surfaces/cli/lane", "pattern": "Usage|usage|prompt_tokens|completion_tokens|total_tokens"}
  -> grep {"path": "internal/evidence/eval/perf.go", "pattern": "Usage|usage|prompt_tokens|\\.PromptTokens|\\.CompletionTokens"}
[2m  ▎ thinking[0m
  · 38983 tok · in 38711 (36992 cached / 1719 new) · out 272 (37 reasoning) · ¥0.0030
  -> read_file {"limit": 60, "offset": 240, "path": "internal/surfaces/cli/lane/explain.go"}
  -> read_file {"limit": 60, "offset": 270, "path": "internal/surfaces/cli/lane/post_analyze.go"}
  -> read_file {"limit": 30, "offset": 550, "path": "internal/surfaces/cli/lane/post_analyze.go"}
[2m  ▎ thinking[0m
  · 40743 tok · in 40466 (38912 cached / 1554 new) · out 277 (59 reasoning) · ¥0.0029
  -> read_file {"limit": 40, "offset": 1170, "path": "internal/surfaces/tui/app/model_state.go"}
  -> grep {"path": "internal/evidence/lanehistory", "pattern": "prompt_tokens|Usage|session_summary"}
  -> read_file {"limit": 70, "offset": 0, "path": "internal/runtime/llama/deepseek_test.go"}
[2m  ▎ thinking[0m
  · 42768 tok · in 42570 (40704 cached / 1866 new) · out 198 (45 reasoning) · ¥0.0031
  -> read_file {"limit": 50, "offset": 173, "path": "internal/runtime/llama/client.go"}
  -> grep {"path": "internal/runtime/llama/client.go", "pattern": "type chatMessage struct|type choice struct|type chatResponse st...
[2m  ▎ thinking[0m
  · 43628 tok · in 43461 (42752 cached / 709 new) · out 167 (35 reasoning) · ¥0.0019
  -> grep {"path": "internal/surfaces/cli/lane", "pattern": "func jsonInt|func jsonString|func jsonBool"}
  -> grep {"path": "internal/evidence/eval/perf.go", "pattern": "func intField"}
[2m  ▎ thinking[0m
  · 43922 tok · in 43755 (43520 cached / 235 new) · out 167 (24 reasoning) · ¥0.0014
  -> read_file {"limit": 15, "offset": 361, "path": "internal/surfaces/cli/lane/post_analyze.go"}
  -> grep {"path": "internal/evidence/eval/perf.go", "pattern": "func intField"}
[2m  ▎ thinking[0m
I've now read the design doc and all the affected code structures. Here is my review.

---

## Review: `docs/design-hosted-cache-cost-metrics.md`

**Verdict: PASS_WITH_RISKS**

---

### 1. Implementation Seams — Well-identified, one structural gap

The design correctly traces the data flow through three clean seams:

| Seam | Where | Assessment |
|---|---|---|
| **Provider → `llama.Usage`** | `client.go:1067→1101` — `chatResponse.Usage` maps via struct tags | Clean: new `Usage` fields with `json:"...,omitempty"` auto-populate from DeepSeek responses |
| **`llama.Usage` → `model_call` event** | `runner.go:6109-6111` — `reply.Usage.X` written into `map[string]any` payload | Clean: just add `payload["prompt_cache_hit_tokens"] = reply.Usage.PromptCacheHitTokens` etc. |
| **`model_call` → TUI aggregation** | `model_state.go:1186` — `json.Unmarshal` into a typed struct, then `recordModelCall(...)` | Clean: unknown JSON fields are silently ignored by `json.Unmarshal`; extend the struct and the method signature |

**RISK: `completion_tokens_details.reasoning_tokens` is a nested JSON path that cannot be extracted by flat struct tags.** The document says to add `ReasoningTokens` as a top-level field on `Usage` (§1, "Additive fields"), but DeepSeek returns:

```json
{"usage":{"completion_tokens_details":{"reasoning_tokens":10}}}
```

Go's `json.Unmarshal` will not unwind this into a flat `Usage` field. You need either:
- A `CompletionTokensDetails` sub-struct with `json:"completion_tokens_details"` on `Usage`
- A custom `UnmarshalJSON` on `Usage`
- Parse the field from the raw response before it reaches `Usage`

This is the **only** structural gap in the design. The doc needs to specify which approach is chosen.

**Recommendation:** Add the sub-struct path into the design so the atom tests can cover it. The custom `UnmarshalJSON` path adds per-deserialization overhead; the sub-struct is more idiomatic Go.

---

### 2. Type Ownership — Clean separation

| Type lives in | Owns | Does not own |
|---|---|---|
| `llama.Usage` | Provider facts: `PromptCacheHitTokens`, `PromptCacheMissTokens`, `ReasoningTokens` | Pricing, hit rate, adjusted tokens |
| `runnerMetrics` | Summed raw facts across calls in a runner session | Derived/computed fields |
| `sessionAggregate` (TUI) | Summed facts reconstructed from `model_call` events | Derived fields |
| Benchmark/reporting | `estimated_total_cost`, pricing snapshots | Provider facts |

The boundary is well-drawn and matches the stated goal: "Keep pricing policy outside the provider client" (Goals, §5). The design explicitly says not to add hit rate or adjusted tokens to `llama.Usage` (Non-Goals, §1).

One small naming concern: `cache_adjusted_prompt_tokens` in `session_summary` (JSON Field Contract table) is defined as `prompt_cache_miss_tokens + prompt_cache_hit_tokens` — which is just `prompt_tokens` when the provider reports both fields. This adds zero information beyond the raw components and could confuse readers into thinking it's a rate-adjusted figure. The design acknowledges this in Risks: "Confusing 'adjusted tokens' with billable currency." Consider naming it `cache_accounted_prompt_tokens` or dropping it from per-call JSONL and computing it only in benchmark reports.

---

### 3. Additive JSON Compatibility — Confirmed safe at every reader

Every downstream reader uses one of two patterns, both safe:

**Pattern A — `map[string]any` + `jsonInt(v)` (returns 0 for missing keys):**
- `explain.go:258-261` — reads `session_summary` fields
- `post_analyze.go:324-327` — reads `session_summary` fields
- `perf.go:257-258, 728-732, 754-758` — reads `model_call` and `session_summary` via `intField`

These readers silently pick up new fields through their map access pattern: `jsonInt(ev["prompt_cache_hit_tokens"])` returns `0` when the key is absent. Old JSONL without the new fields is handled transparently. New JSONL with new fields won't affect old behavior.

**Pattern B — Typed struct with `json.Unmarshal`:**
- `model_state.go:1173-1185` — TUI `model_call` parser

Go's `json.Unmarshal` into a struct **ignores unknown fields by default**. Adding `prompt_cache_hit_tokens`, etc. to the model_call JSON will not cause this struct to fail — the new fields are simply not mapped into any struct field and are dropped. This is correct for backward compatibility: old TUI binary + new JSONL still works.

**`debuglog.go` (transcript):** Both `ModelCall` and `SessionSummary` are serialized as manually constructed `map[string]any` in `LogModelCall`/`LogSessionSummary` (lines 444, 471). New fields are just new map entries. Old stored records that lack the fields will naturally have them absent — zero in Go on re-read since the map access returns nil which defaults to 0. Confirmed safe.

---

### 4. Runner/TUI Aggregation — Clear propagation path

The data flow is:

```
CompleteChatMessage → reply.Usage (llama.Usage with new fields)
  ↓
emitModelCallTraceWithOptions (runner.go:6109)
  — reads reply.Usage.PromptCacheHitTokens etc.
  — writes to map[string]any payload → json.Marshal → model_call JSONL
  ↓
addUsage (runner_state.go:253)
  — sums PromptCacheHitTokens, PromptCacheMissTokens, ReasoningTokens
  — stores in runnerMetrics
  ↓
emitBudgetLedger (runner.go:6016)
  — copies runnerMetrics cache fields into runnerBudgetLedger → budget_ledger JSONL
  ↓
TUI model_call handler (model_state.go:1172)
  — parses model_call JSON into extended struct
  — calls extended sessionAggregate.recordModelCall(...)
  — writes extended transcript.ModelCall via debugLogger
  ↓
writeSessionSummary (model_state.go:1377)
  — emits extended session_summary JSONL with cache fields + hit_rate + adjusted_prompt_tokens
```

This is the correct propagation tree. The design doc's flowchart matches the actual code flow.

**Minor risk of gap:** `recordModelCall` currently takes 3 int params. Extending to 6 int params is clumsy — consider a usage struct or variadic option, but that's an implementation detail, not a design flaw.

---

### 5. Tests — Adequate scope, one missing case

| Atom | Tests specified | Coverage |
|---|---|---|
| A5.1 | `go test ./internal/runtime/llama -run 'Usage\|DeepSeek\|Cache\|Reasoning'` | Covers parse, zero-omit, existing-regression |
| A5.2 | `go test ./internal/runtime/agent -run 'Usage\|BudgetLedger\|SessionSummary\|ModelCall\|Cache'` | Covers aggregation + JSONL emission |
| A5.3 | `go test ./internal/surfaces/tui/app ./internal/evidence/transcript ./internal/surfaces/cli/lane -run 'SessionSummary\|ModelCall\|Cache\|Explain\|PostAnalyze'` | Covers propagation and reader compatibility |
| A5.4 | Captured fixture + manual rerun | Thin but acceptable for benchmark |

**Missing from test plan:** A test that the `completion_tokens_details.reasoning_tokens` nested JSON path actually parses into `Usage.ReasoningTokens`. This is the structural gap from finding #1. Without it, integration tests will silently pass with zero `ReasoningTokens` because the flat struct silently ignores the nested field.

**Recommendation:** Add an explicit test in `deepseek_test.go` that sends a response with `completion_tokens_details` and asserts `Usage.ReasoningTokens > 0`.

---

### 6. Go Package Blast Radius — Small and contained

| Package | Change type | Risk | Why |
|---|---|---|---|
| `internal/runtime/llama` | Extend `Usage` struct (3 fields) | Low | No new imports; no API caller changes (all callers use `reply.Usage.PromptTokens` which stays) |
| `internal/runtime/agent` | Extend `runnerMetrics`, `runnerBudgetLedger`, `addUsage` (3 fields each), `emitModelCallTraceWithOptions` (set 3 map keys) | Low | No new dependencies; `runner.go:6105` already builds a `map[string]any` |
| `internal/surfaces/tui/app` | Extend `sessionAggregate` (3 fields), `recordModelCall` (3+ params), `writeSessionSummary` (3+ map keys), `model_call` struct (3+ fields) | Low | No new imports |
| `internal/evidence/transcript` | Extend `ModelCall` (3 fields), `SessionSummary` (3 fields + 2 derived), `LogModelCall`/`LogSessionSummary` | Low | No new imports; map-based serialization |
| `internal/surfaces/cli/lane` | Extend `laneSessionSummary` if needed | Very low | Map-based readers, `jsonInt` defaults to 0 |
| `internal/evidence/eval/perf.go` | Possibly extend `intField` lookups | Very low | Map-based, defaults to 0 |
| `internal/evidence/lanehistory` | Not touched (first implementation) | None | Deferred as per design |

No circular imports, no new external dependencies, no interface changes, no exported API surface changes besides the three new `Usage` fields.

---

### Brief Risk Summary

| Risk | Severity | Mitigation |
|---|---|---|
| `reasoning_tokens` nested JSON won't parse with flat fields | **High** | Add `CompletionTokensDetails` sub-struct or custom `UnmarshalJSON` to `Usage`; update design doc and test plan |
| `cache_adjusted_prompt_tokens` name could confuse readers into thinking it's a rate-adjusted cost | Low | Rename to `cache_accounted_prompt_tokens` or defer to benchmark layer only |
| `sessionAggregate.recordModelCall` parameter count grows from 3→6 | Low | Acceptable for a private method; avoid adding more params in future |
| TUI/debug transcript divergence risk noted in doc | Low | Already mitigated in design: both consume same event payloads |

The design is otherwise well-grounded in the actual code structure. The additive JSON strategy is correct at every reader. The seam boundaries match the existing architecture. The one structural gap around `completion_tokens_details.reasoning_tokens` is concrete and easily fixed before implementation starts.
  · 48594 tok · in 43991 (43904 cached / 87 new) · out 4603 (2154 reasoning) · ¥0.0102
