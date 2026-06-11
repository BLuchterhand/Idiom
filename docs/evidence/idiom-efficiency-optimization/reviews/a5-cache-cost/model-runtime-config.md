[2m  ▎ thinking[0m
  · 16912 tok · in 16825 (16768 cached / 57 new) · out 87 (33 reasoning) · ¥0.0006
  -> read_file {"path": "docs/design-hosted-cache-cost-metrics.md"}
[2m  ▎ thinking[0m
  · 21157 tok · in 20859 (17152 cached / 3707 new) · out 298 (173 reasoning) · ¥0.0046 · cache prefix changed: tools
  -> mcp__codegraph__context {"task": "llama.Usage struct, llama client.go DeepSeek/OpenAI response parsing, runnerMetrics addUsage, emitModelCallTra...
[2m  ▎ thinking[0m
  · 22723 tok · in 22584 (21120 cached / 1464 new) · out 139 (34 reasoning) · ¥0.0022
  -> read_file {"path": "internal/runtime/llama/client.go", "limit": 120}
  -> read_file {"path": "internal/runtime/agent/runner_state.go"}
[2m  ▎ thinking[0m
  · 28267 tok · in 27950 (22656 cached / 5294 new) · out 317 (58 reasoning) · ¥0.0064
  -> read_file {"limit": 100, "offset": 120, "path": "internal/runtime/llama/client.go"}
  -> grep {"pattern": "emitModelCallTraceWithOptions", "path": "internal/runtime/agent/runner.go"}
  -> grep {"pattern": "sessionAgg|SessionAgg", "path": "internal/surfaces/tui/app/model_state.go"}
  -> grep {"pattern": "ModelCall|SessionSummary", "path": "internal/evidence/transcript/debuglog.go"}
[2m  ▎ thinking[0m
  · 32313 tok · in 32035 (28160 cached / 3875 new) · out 278 (52 reasoning) · ¥0.0050
  -> read_file {"offset": 109, "path": "internal/evidence/transcript/debuglog.go", "limit": 80}
  -> read_file {"offset": 34, "path": "internal/surfaces/tui/app/model_state.go", "limit": 70}
  -> read_file {"offset": 6087, "path": "internal/runtime/agent/runner.go", "limit": 100}
[2m  ▎ thinking[0m
  · 35529 tok · in 35316 (32256 cached / 3060 new) · out 213 (71 reasoning) · ¥0.0041
  -> grep {"path": "internal/runtime/llama", "pattern": "prompt_cache|deepseek|hosted|openai.*chat|chat.*response.*Usage"}
  -> grep {"path": "internal/runtime/llama", "pattern": "chatCompletion|ChatCompletion|chatResponse"}
[2m  ▎ thinking[0m
  · 37331 tok · in 37106 (35456 cached / 1650 new) · out 225 (16 reasoning) · ¥0.0028
  -> read_file {"offset": 1060, "path": "internal/runtime/llama/client.go", "limit": 50}
  -> read_file {"offset": 630, "path": "internal/runtime/llama/client.go", "limit": 100}
  -> read_file {"path": "internal/runtime/llama/deepseek_test.go", "limit": 100}
[2m  ▎ thinking[0m
  · 40916 tok · in 40619 (37248 cached / 3371 new) · out 297 (47 reasoning) · ¥0.0047
  -> grep {"path": "internal/runtime/llama/client.go", "pattern": "func.*hosted|isHosted|isOpenAI|BackendDeepSeek|BackendOpenAI|Ba...
  -> read_file {"limit": 60, "offset": 459, "path": "internal/runtime/llama/client.go"}
  -> grep {"path": "internal/platform/config", "pattern": "BackendDeepSeek|BackendOpenAI|BackendZAI|BackendMiniMax|BackendLlama|Ba...
[2m  ▎ thinking[0m
  · 43587 tok · in 43382 (40832 cached / 2550 new) · out 205 (54 reasoning) · ¥0.0038
  -> grep {"path": "internal/runtime/llama/client.go", "pattern": "func.*CompleteChat|func.*openaiResponses|func.*completeChat"}
  -> read_file {"limit": 100, "offset": 985, "path": "internal/runtime/llama/client.go"}
[2m  ▎ thinking[0m
  · 45065 tok · in 44925 (43520 cached / 1405 new) · out 140 (31 reasoning) · ¥0.0026
  -> grep {"path": "internal/runtime/llama/client.go", "pattern": "func.*completeResponses"}
  -> glob {"pattern": "internal/runtime/llama/responses*.go"}
  · 45223 tok · in 45140 (45056 cached / 84 new) · out 83 · ¥0.0012
  -> read_file {"limit": 125, "offset": 868, "path": "internal/runtime/llama/client.go"}
[2m  ▎ thinking[0m
  · 46738 tok · in 46576 (45056 cached / 1520 new) · out 162 (34 reasoning) · ¥0.0027
  -> grep {"path": "internal/runtime/llama/client.go", "pattern": "responsesResponse|responsesUsage"}
  -> grep {"path": "internal/runtime/llama", "pattern": "type responses|Usage struct"}
[2m  ▎ thinking[0m
  · 47107 tok · in 47006 (46720 cached / 286 new) · out 101 (18 reasoning) · ¥0.0014
  -> read_file {"limit": 60, "offset": 775, "path": "internal/runtime/llama/client.go"}
[2m  ▎ thinking[0m
  · 48910 tok · in 47839 (47104 cached / 735 new) · out 1071 (944 reasoning) · ¥0.0038
  -> grep {"path": "internal/runtime/llama/client.go", "pattern": "CompleteChatStream"}
  -> grep {"path": "internal/runtime/agent/runner.go", "pattern": "CompleteChatStream|ChatStream"}
[2m  ▎ thinking[0m
  · 49125 tok · in 48934 (48896 cached / 38 new) · out 191 (62 reasoning) · ¥0.0014
  -> grep {"path": "internal/runtime/agent/runner.go", "pattern": "\\.CompleteChat|\\.completeChat"}
  -> grep {"path": "internal/runtime/llama/client.go", "pattern": "func.*CompleteChat"}
[2m  ▎ thinking[0m
  · 49588 tok · in 49471 (49024 cached / 447 new) · out 117 (54 reasoning) · ¥0.0017
  -> grep {"path": "internal", "pattern": "\\.CompleteChat\\("}
[2m  ▎ thinking[0m
  · 51599 tok · in 50417 (49536 cached / 881 new) · out 1182 (1052 reasoning) · ¥0.0042
  -> grep {"path": "internal", "pattern": "completion_tokens_details|CompletionTokensDetails"}
  -> grep {"path": "internal/runtime/llama", "pattern": "prompt_cache|reasoning_tokens|ReasoningTokens"}
[2m  ▎ thinking[0m
---

## Review: `docs/design-hosted-cache-cost-metrics.md`

**Verdict: PASS_WITH_RISKS**

---

### ✅ Strengths

1. **Correct parsing path identified.** DeepSeek's chat-completions API returns `prompt_cache_hit_tokens`, `prompt_cache_miss_tokens` at `usage.*` and `completion_tokens_details.reasoning_tokens` inside `usage.*`. The non-streaming `CompleteChatMessage` path → `chatResponse` → `Usage` (line 111-115 of `client.go`) is the right place to extend. Those fields are currently silently dropped by JSON unmarshaling because the Go struct doesn't name them.

2. **Provider facts vs pricing separation is clean.** The doc correctly keeps pricing policy outside `llama.Usage` and `internal/runtime/llama`. The `chatResponse` struct is shared across all non-OpenAI-Responses backends (llama.cpp, MLX, DeepSeek, ZAI, MiniMax at `client.go:173-188`), so adding fields to `Usage` is the correct shared abstraction. Backends that don't emit these fields get zero values — no harm.

3. **OpenAI Responses API is correctly handled.** `completeResponsesChatMessage` (`client.go:868`) uses a separate `responsesResponse` struct with its own inline `Usage` (`client.go:798-802`). The doc's statement that this path "remains compatible and zeroes cache fields" is accurate.

4. **Aggregation and event boundaries are correct.** The data flow (Provider → `llama.Usage` → `runnerMetrics.addUsage` → `emitModelCallTraceWithOptions` → JSONL + TUI events + debug transcript) matches the current code structure precisely at `runner_state.go:205-217`, `runner.go:6087-6143`, `model_state.go:38-61`, and `debuglog.go:110-160`.

5. **Backwards compatibility is preserved.** Extending `Usage`, `runnerMetrics`, `sessionAggregate`, `ModelCall`, and `SessionSummary` with additive fields (zero when absent) won't break existing readers. `json:"omitempty"` on per-call fields keeps JSONL lean.

---

### ⚠️ Risks

**R1 — Streaming path loses all usage data (undocumented).**  
`CompleteChat` (`client.go:739`) is the streaming variant; it returns only `(string, error)` with no usage at all. The TUI uses this path in `feature_planner_product.go`, `turn_planner.go`, and `compaction.go` for summarization, classification, and planning calls. These calls will never surface cache metrics.  
*Impact:* Non-runner model calls (planner, compactor) are invisible to cache tracking. The doc should note this limitation. The runner itself only uses `CompleteChatMessage` (non-streaming), so benchmark artifacts won't be affected.  
*Fix:* Add a "Non-goals" or "Known limitations" subsection stating that streaming paths don't surface usage and are out of scope for this change.

**R2 — `prompt_cache_accounting_known` detection heuristic is underspecified.**  
The doc says `cache_adjusted_prompt_tokens` should equal `hit + miss` "when cache fields are known" and `prompt_tokens` otherwise. But the trigger "known" is ambiguous: DeepSeek may return `0` for both hit and miss on a cold cache. Does zero indicate "unknown" or "known zero"? The `prompt_cache_accounting_known` diagnostic field is mentioned in the risks section but not defined in the JSON Field Contract table.  
*Fix:* Define the predicate explicitly — e.g. "`accounting_known = true` when the API response included any of `prompt_cache_hit_tokens` or `prompt_cache_miss_tokens` as non-omitted JSON fields." This requires either a separate boolean tracking field in `Usage` or using `*int` with nil vs zero.

**R3 — ZAI and MiniMax cache behavior is unmentioned.**  
These backends share the same `chatResponse` struct and `Usage` type. The doc says "OpenAI Responses parsing remains compatible" but doesn't address whether ZAI/MiniMax also emit cache fields. If they start to, these fields will be captured automatically (good), but the pricing estimate logic in the benchmark layer would need per-provider rates. Currently the doc only names DeepSeek.  
*Fix:* Add a one-liner in the "Provider Usage Contract" section: "ZAI and MiniMax use the same parsing path; cache fields default to zero unless those providers add equivalent fields."

**R4 — `completion_tokens_details` is a nested object, not a flat field.**  
The doc's JSON Field Contract table lists `reasoning_tokens` at the top level, but DeepSeek actually returns it as `usage.completion_tokens_details.reasoning_tokens`. The Go struct needs a nested `CompletionTokensDetails` type:

```go
type Usage struct {
    PromptTokens     int                     `json:"prompt_tokens,omitempty"`
    CompletionTokens int                     `json:"completion_tokens,omitempty"`
    TotalTokens      int                     `json:"total_tokens,omitempty"`
    PromptCacheHitTokens  int                `json:"prompt_cache_hit_tokens,omitempty"`
    PromptCacheMissTokens int                `json:"prompt_cache_miss_tokens,omitempty"`
    CompletionTokensDetails *CompletionTokensDetails `json:"completion_tokens_details,omitempty"`
}
```

The design text in §1 says to add `ReasoningTokens` as a top-level field on `Usage`, but the JSON deserialization will only work if there's a matching struct tag. The doc should clarify whether `Usage.ReasoningTokens` is flattened into a direct `json:"reasoning_tokens"` (wrong — DeepSeek nests it) or accessed via a new nested struct, or whether the non-streaming response handler should copy it up from `completion_tokens_details`.

*Fix:* Show the `Usage` struct extension with nested `CompletionTokensDetails` to match the actual DeepSeek wire format.

---

### 📋 Exact References

| Area | File | Line(s) |
|------|------|---------|
| `Usage` struct definition | `internal/runtime/llama/client.go` | 111-115 |
| `chatResponse` (shared by all non-Responses backends) | `internal/runtime/llama/client.go` | 173-188 |
| `chatStreamChunk` (streaming — no usage captured) | `internal/runtime/llama/client.go` | 190-208 |
| `responsesResponse` / its inline `Usage` | `internal/runtime/llama/client.go` | 775-802 |
| `completeResponsesChatMessage` (Responses API path) | `internal/runtime/llama/client.go` | 868-972 |
| `CompleteChatMessage` (non-streaming — usage IS captured) | `internal/runtime/llama/client.go` | 990-1105 |
| `runnerMetrics.addUsage` | `internal/runtime/agent/runner_state.go` | 253-264 |
| `emitModelCallTraceWithOptions` | `internal/runtime/agent/runner.go` | 6087-6143 |
| `sessionAggregate.recordModelCall` | `internal/surfaces/tui/app/model_state.go` | 53-61 |
| `sessionAggregate` send to JSON | `internal/surfaces/tui/app/model_state.go` | 1393-1428 |
| `ModelCall` struct | `internal/evidence/transcript/debuglog.go` | 110-121 |
| `SessionSummary` struct | `internal/evidence/transcript/debuglog.go` | 145-160 |
| Backend constants | `internal/platform/config/config.go` | 130-136, 1079 |
| `isHostedChatBackend` (DeepSeek, ZAI, MiniMax) | `internal/runtime/llama/client.go` | 303-306 |
| `isOpenAIResponsesBackend` (OpenAI Responses, Codex) | `internal/runtime/llama/client.go` | 294-296 |

---

### Summary

The design is structurally sound and correctly traces the data flow through the existing runtime. The main concerns are:

- The **streaming gap** (R1) should be documented so future readers don't waste time debugging missing cache fields on non-runner calls.
- The **`accounting_known` heuristic** (R2) needs explicit JSON-presence detection rather than relying on zero vs non-zero.
- The **`completion_tokens_details` nesting** (R4) must be reflected in the `Usage` struct definition — a flat `ReasoningTokens int` with `json:"reasoning_tokens"` won't work without a custom `UnmarshalJSON`.
  · 53593 tok · in 51623 (51584 cached / 39 new) · out 1970 (30 reasoning) · ¥0.0050
