[2m  ▎ thinking[0m
  · 17072 tok · in 16830 (16640 cached / 190 new) · out 242 (112 reasoning) · ¥0.0010
  -> bash {"command": "cd /workspace/idiom && git branch --show-current && git log --oneline -20"}
  -> bash {"command": "cd /workspace/idiom && git diff --stat HEAD~5..HEAD 2>/dev/null || git diff --stat"}
[2m  ▎ thinking[0m
  · 18777 tok · in 18514 (17152 cached / 1362 new) · out 263 (121 reasoning) · ¥0.0022 · cache prefix changed: tools
  -> bash {"command": "cd /workspace/idiom && git diff HEAD~1..HEAD --stat"}
  -> bash {"command": "cd /workspace/idiom && git diff HEAD~5..HEAD -- 'prompts/' 'internal/runtime/agent/' 'inte...
[2m  ▎ thinking[0m
  · 25630 tok · in 25298 (18688 cached / 6610 new) · out 332 (110 reasoning) · ¥0.0076
  -> read_file {"path": "/workspace/idiom/docs/design-reasonix-alignment.md"}
  -> read_file {"path": "/workspace/idiom/internal/runtime/agent/runner_prefix_diagnostics.go"}
  -> read_file {"path": "/workspace/idiom/internal/runtime/agent/runner_preimage_checkpoint.go"}
  -> read_file {"path": "/workspace/idiom/internal/runtime/agent/runner_task_ledger.go"}
[2m  ▎ thinking[0m
  · 34491 tok · in 34217 (25600 cached / 8617 new) · out 274 (51 reasoning) · ¥0.0097
  -> read_file {"path": "/workspace/idiom/internal/runtime/agent/safety/preimage_checkpoint.go"}
  -> read_file {"path": "/workspace/idiom/internal/runtime/agent/safety/preimage_checkpoint_test.go"}
  -> bash {"command": "cd /workspace/idiom && git diff HEAD~1..HEAD -- internal/runtime/agent/runner.go internal/...
[2m  ▎ thinking[0m
  · 39399 tok · in 39190 (34432 cached / 4758 new) · out 209 (35 reasoning) · ¥0.0059
  -> read_file {"path": "/workspace/idiom/internal/runtime/agent/runner_prefix_diagnostics_test.go"}
  -> read_file {"path": "/workspace/idiom/internal/capabilities/workspace/tool_names.go"}
  -> read_file {"path": "/workspace/idiom/internal/capabilities/workspace/tool_surface_metadata.go"}
[2m  ▎ thinking[0m
  · 47445 tok · in 47101 (39296 cached / 7805 new) · out 344 (53 reasoning) · ¥0.0093
  -> read_file {"path": "/workspace/idiom/internal/runtime/agent/runner_preimage_checkpoint_test.go"}
  -> read_file {"path": "/workspace/idiom/internal/runtime/agent/runner_task_ledger_evidence_test.go"}
  -> bash {"command": "cd /workspace/idiom && git diff HEAD~5..HEAD -- 'prompts/' 'internal/surfaces/'"}
  -> bash {"command": "cd /workspace/idiom && git diff HEAD~5..HEAD -- internal/runtime/agent/phase_contract.go i...
[2m  ▎ thinking[0m
  · 53624 tok · in 53090 (47360 cached / 5730 new) · out 534 (57 reasoning) · ¥0.0077
  -> bash {"command": "cd /workspace/idiom && git diff HEAD~1..HEAD -- internal/contracts/autonomy/planner_tool_m...
  -> bash {"command": "cd /workspace/idiom && git status --porcelain | grep -v '^??' | head -20"}
  -> bash {"command": "cd /workspace/idiom && git diff HEAD~1..HEAD -- :!docs/design-reasonix-alignment.md :!inte...
  -> bash {"command": "cd /workspace/idiom && git diff HEAD~1..HEAD -- 'prompts/'"}
[2m  ▎ thinking[0m
  · 54352 tok · in 53913 (53504 cached / 409 new) · out 439 (373 reasoning) · ¥0.0024
  -> bash {"command": "cd /workspace/idiom && git diff --stat HEAD 2>/dev/null"}
[2m  ▎ thinking[0m
  · 55102 tok · in 54772 (54272 cached / 500 new) · out 330 (80 reasoning) · ¥0.0022
  -> bash {"command": "cd /workspace/idiom && git diff HEAD -- internal/runtime/agent/runner.go | head -80"}
  -> bash {"command": "cd /workspace/idiom && git diff HEAD -- internal/runtime/agent/runner_state.go | head -80"...
  -> bash {"command": "cd /workspace/idiom && git diff HEAD -- internal/runtime/llama/client.go | head -80"}
  -> bash {"command": "cd /workspace/idiom && git diff HEAD -- internal/runtime/agent/read_only_closure.go | head...
[2m  ▎ thinking[0m
  · 60700 tok · in 59539 (55040 cached / 4499 new) · out 1161 (938 reasoning) · ¥0.0079
  -> bash {"command": "cd /workspace/idiom && git diff HEAD -- internal/runtime/llama/client.go | head -200"}
  -> bash {"command": "cd /workspace/idiom && git diff HEAD -- internal/runtime/agent/runner_plan_then_execute_gu...
  -> bash {"command": "cd /workspace/idiom && git diff HEAD -- internal/runtime/agent/final_answer_acceptance.go ...
[2m  ▎ thinking[0m
  · 65718 tok · in 65434 (60672 cached / 4762 new) · out 284 (29 reasoning) · ¥0.0065
  -> bash {"command": "cd /workspace/idiom && git diff HEAD -- internal/surfaces/tui/app/model_state.go | head -8...
  -> bash {"command": "cd /workspace/idiom && git diff HEAD -- internal/evidence/transcript/debuglog.go | head -8...
  -> bash {"command": "cd /workspace/idiom && git diff HEAD -- internal/surfaces/cli/lane/explain.go | head -80"}
  -> bash {"command": "cd /workspace/idiom && git diff HEAD -- AGENTS.md .codex/README.md | head -60"}
[2m  ▎ thinking[0m
  · 69295 tok · in 69023 (65664 cached / 3359 new) · out 272 (21 reasoning) · ¥0.0052
  -> bash {"command": "cd /workspace/idiom && git diff HEAD -- internal/surfaces/cli/route/writer_route.go intern...
  -> bash {"command": "cd /workspace/idiom && git diff HEAD -- internal/runtime/agent/runner.go | grep -E '^[+-]'...
  -> bash {"command": "cd /workspace/idiom && git diff HEAD -- internal/surfaces/tui/app/model_state.go | grep -E...
[2m  ▎ thinking[0m
  · 72070 tok · in 71734 (69248 cached / 2486 new) · out 336 (54 reasoning) · ¥0.0045
  -> bash {"command": "cd /workspace/idiom && git diff HEAD -- internal/runtime/agent/runner.go | grep -B2 -A5 'p...
  -> bash {"command": "cd /workspace/idiom && git diff HEAD -- internal/runtime/agent/runner.go | grep -B2 -A5 'D...
  -> bash {"command": "cd /workspace/idiom && git diff HEAD -- 'prompts/' 2>/dev/null || echo \"No prompt changes...
  -> bash {"command": "cd /workspace/idiom && git diff HEAD~1..HEAD -- 'prompts/' 2>/dev/null || echo \"No prompt...
[2m  ▎ thinking[0m
  · 72826 tok · in 72391 (72064 cached / 327 new) · out 435 (163 reasoning) · ¥0.0026
  -> bash {"command": "cd /workspace/idiom && git diff HEAD -- internal/contracts/writerroute/decision.go interna...
  -> bash {"command": "cd /workspace/idiom && git diff HEAD -- internal/runtime/llama/client_test.go | head -60"}
  -> bash {"command": "cd /workspace/idiom && git diff HEAD -- internal/surfaces/tui/app/session_summary_test.go ...
  -> bash {"command": "cd /workspace/idiom && git diff HEAD -- internal/runtime/agent/plan_execute_runtime_policy...
[2m  ▎ thinking[0m
Good — I now have a complete picture of both the committed effort and the dirty work-in-progress. Let me verify one more thing about the `emitModelCallTraceWithOptions` addition for cache data in the runner.
  · 77262 tok · in 75577 (72704 cached / 2873 new) · out 1685 (1485 reasoning) · ¥0.0077
  -> bash {"command": "cd /workspace/idiom && git diff HEAD -- internal/runtime/agent/runner.go | grep -A10 'emit...
  -> bash {"command": "cd /workspace/idiom && git diff HEAD -- internal/runtime/agent/runner.go | grep -B2 -A15 '...
[2m  ▎ thinking[0m
  · 77773 tok · in 77636 (77184 cached / 452 new) · out 137 (54 reasoning) · ¥0.0023
  -> bash {"command": "cd /workspace/idiom && git diff HEAD -- internal/runtime/agent/runner.go | grep -B3 -A3 'm...
  ⊘ bash command exited: exit status 1
[2m  ▎ thinking[0m
  · 77904 tok · in 77795 (77696 cached / 99 new) · out 109 (22 reasoning) · ¥0.0019
  -> bash {"command": "cd /workspace/idiom && git diff HEAD~1..HEAD -- internal/runtime/agent/runner.go | grep -B...
[2m  ▎ thinking[0m
  · 78552 tok · in 78039 (77824 cached / 215 new) · out 513 (377 reasoning) · ¥0.0028
  -> bash {"command": "cd /workspace/idiom && git grep -n 'prefix_cache_signal_present'"}
  -> bash {"command": "cd /workspace/idiom && git diff HEAD -- internal/runtime/agent/runner.go | grep -B2 -A2 'm...
[2m  ▎ thinking[0m
  · 79034 tok · in 78742 (78464 cached / 278 new) · out 292 (148 reasoning) · ¥0.0024
  -> bash {"command": "cd /workspace/idiom && grep -rn 'prefix_cache_signal' --include='*.go' | head -10"}
  -> bash {"command": "cd /workspace/idiom && grep -rn 'prompt_cache_accounting_known\\|PromptCacheAccountingKnow...
[2m  ▎ thinking[0m
  · 81266 tok · in 79707 (78976 cached / 731 new) · out 1559 (1475 reasoning) · ¥0.0054
  -> bash {"command": "cd /workspace/idiom && git diff HEAD -- internal/runtime/agent/runner.go | grep -B5 -A20 '...
[2m  ▎ thinking[0m
Now I have complete evidence for the review. Let me compose the findings.

---

# Prompt / Protocol Reviewer — Full-Bench Sign-Off

**Review scope:** Committed efficiency/cache-cost effort (`34d14107` — "Harden runtime contracts from Reasonix review") PLUS dirty WIP changes that implement the follow-on phases (DeepSeek usage schema, direct answer surface, cache token accounting, transport retry). Uncommitted WIP is evaluated because the user named those areas.

**Reviewed materials:** `docs/design-reasonix-alignment.md`, 15 committed files + 23 dirty files across `runtime/llama/client.go`, `runtime/agent/`, `capabilities/workspace/`, `surfaces/`, `evidence/transcript/`, `contracts/writerroute/`.

## Effort vs Noise Classification

**Effort files (same wave, different commit stages):**

| Layer | Files | Status |
|-------|-------|--------|
| Prefix cache diagnostics | `runner_prefix_diagnostics.go`, `runner_prefix_diagnostics_test.go` | Committed |
| Evidence task ledger | `runner_task_ledger.go`, `runner_task_ledger_evidence_test.go`, `runner_state.go` | Committed |
| Preimage checkpoint | `safety/preimage_checkpoint.go`, `safety/preimage_checkpoint_test.go`, `runner_preimage_checkpoint.go`, `runner_preimage_checkpoint_test.go`, `write_preimage_paths.go`, `write_preimage_paths_test.go` | Committed |
| Stale anchor refresh fix | `phase_contract.go`, `phase_contract_test.go`, `plan_execute_planning_state.go` | Committed |
| Greenfield guard fix | `prompt_routing.go`, `implementation_dispatch.go`, `model_state.go` | Committed |
| Tool name consolidation | `capabilities.go`, `manager.go`, `language_tool_catalog.go`, `tool_names.go`, `tool_surface_metadata.go`, `tool_profile_policy.go`, `tool_schemas.go`, `post_analyze.go`, `safety.go` | Committed |
| DeepSeek reasoning preservation | `client.go` | Dirty WIP |
| Provider cache token accounting | `client.go` (`Usage`, `UnmarshalJSON`), `runner_state.go` (metrics, ledger), `runner.go` (model_call emission), `debuglog.go`, `explain.go`, `model_state.go` + tests | Dirty WIP |
| Transport retry | `runner.go` | Dirty WIP |
| Direct answer surface | `runner.go` (surface override + event) | Dirty WIP |
| Evidence-ledger source-satisfied | `runner_plan_then_execute_guard.go` | Dirty WIP |
| DirectAnswerRelevance refactor | `read_only_closure.go`, `final_answer_acceptance.go` + test | Dirty WIP |
| EffectiveModel route | `writer_route.go`, `decision.go`, `decision_test.go` | Dirty WIP |
| Agent guidance | `AGENTS.md`, `.codex/README.md` | Dirty WIP |

**No unrelated noise detected.** All dirty files participate in the cache/efficiency/DeepSeek/answer-surface themes of this effort.

---

## Findings

### FINDING 1 — Protocol Contradiction: `prefix_cache_signal_present: false` vs `prompt_cache_hit_tokens: n` [REQUIRED FIX]

**Location:** `runner_prefix_diagnostics.go:24` (committed) + `runner.go:6116-6120` (dirty WIP)

Both populate the **same** `model_call` event payload. The committed prefix diagnostics unconditionally set:

```go
"prefix_cache_signal_present":  false,
"prefix_cache_signal_provider": "",
```

The dirty cache accounting conditionally sets:

```go
if reply.Usage.PromptCacheAccountingKnown {
    payload["prompt_cache_hit_tokens"] = reply.Usage.PromptCacheHitTokens    // e.g. 8
    payload["prompt_cache_miss_tokens"] = reply.Usage.PromptCacheMissTokens  // e.g. 5
    payload["prompt_cache_accounting_known"] = true
}
```

A single `model_call` event can therefore carry **both** `prefix_cache_signal_present: false` AND `prompt_cache_hit_tokens: 8`. A telemetry consumer cannot tell which signal to trust.

**Fix options (choose one):**
1. **Remove `prefix_cache_signal_present` and `prefix_cache_signal_provider`** from the committed prefix diagnostics entirely. The dirty cache accounting is the only live source of cache signal. Dead fields in committed code that are always false/null poison downstream consumers.
2. **Make `prefix_cache_signal_present` dynamic** — derive it from `reply.Usage.PromptCacheAccountingKnown` when available, fall back to `false`. Requires plumbing `Usage` into `modelCallPrefixDiagnostics()`.

**Recommendation:** Option 1 — delete the two dead fields from `runner_prefix_diagnostics.go`. They add zero signal and will always be wrong once the dirty cache accounting merges. The dirty WIP already emits a richer, accurate cache signal. Update the test at `runner_prefix_diagnostics_test.go:31` that asserts `prefix_cache_signal_present == false`.

---

### FINDING 2 — Event Contract Drift: `model_call` payload now carries 12+ new fields with no contract specification [REQUIRED FIX]

The `model_call` event (`emitModelCallTraceWithOptions`) now receives:

**Committed:**
- `prefix_hash`, `prefix_hash_version`, `prefix_hash_components`, `prefix_change_reason_fields`
- `prompt_prefix_message_count`
- `tool_schema_hash`
- `prefix_cache_metadata_scope`, `prefix_cache_signal_present`, `prefix_cache_signal_provider`

**Dirty WIP:**
- `prompt_cache_hit_tokens`, `prompt_cache_miss_tokens`, `prompt_cache_accounting_known`
- `reasoning_tokens`

That's 12+ new payload fields on `model_call`. Some are part-of-record (always present), others are conditional. Some overlap semantically (`prefix_cache_signal_present` vs `prompt_cache_accounting_known` — see Finding 1).

**Fix:** Add a structured comment in `emitModelCallTraceWithOptions` (or a `modelEventContract` doc) specifying:
- Which fields are **stable contract** (always present, typed, meaningful)
- Which are **volatile/telemetry-only** (diagnostic, may change, prefix `telemetry_` or documented as bounded)
- Which are **provider-dependent** (conditional on backend signal)

The committed prefix diagnostics already tag `prefix_cache_metadata_scope: telemetry_only` but the structural dependencies aren't documented. A consumer shouldn't have to reverse-engineer whether `prefix_cache_signal_present` and `prompt_cache_accounting_known` are the same thing (they're not, but the naming implies they could be).

---

### FINDING 3 — Task Ledger Read Tool List Is Incomplete [REQUIRED FIX BEFORE FINAL MERGE]

**Location:** `runner_task_ledger.go:216-222`

```go
func taskLedgerReadTool(result workspace.ToolResult) bool {
    switch strings.TrimSpace(result.Tool) {
    case "read_file", "code_navigate", "context_zoom", "context_pack",
         "context_query", "discovery_query", "search_content",
         "go_read_symbol", "go_extract_body", "go_file_outline",
         "python_read_symbol", "python_file_outline":
        return true
    default:
        return false
    }
}
```

Missing read tools that produce evidence:
- `context_impact` — produces blast-radius evidence
- `context_status` — produces index freshness evidence
- `history_range`, `history_symbol`, `blame_range` — produce git-history evidence
- `structural_search` — produces AST evidence
- `dependency_graph` — produces dependency evidence
- `harness_audit` — produces diagnostic evidence
- `source_sanity` — produces diagnostic evidence
- `go_importers`, `go_find_*` — produce symbol evidence
- `cpp_*` tools — produce symbol/includes evidence

A model that uses any of these as evidence sources for `read:path` will get **rejected** by the ledger, even though the tool was successfully called and returned valid evidence.

**Fix:** Replace the hardcoded switch with the workspace's own read-tool classification. Options:
1. Use `builtInToolSurfaceSpec` metadata: tools with `Category == CategoryRead || Category == CategoryDiscovery || Category == CategorySearch` and `SideEffects == sideEffectReadOnly`.
2. Or use a workspace function like `IsReadOnlyTool(tool) bool` that already exists (needs verification).

The verification tool list at `runner_task_ledger.go:225-231` has the same vulnerability — `taskLedgerVerificationTool` is a hardcoded switch that will drift from the actual verification tool set.

---

### FINDING 4 — `stableHash` Uses `json.Marshal` — Map Ordering Not Guaranteed [MODERATE RISK]

**Location:** `runner_prefix_diagnostics.go:41-47`

```go
func stableHash(value any) string {
    data, err := json.Marshal(value)
    if err != nil {
        data = []byte("{}")
    }
    sum := sha256.Sum256(data)
    return hex.EncodeToString(sum[:])
}
```

`json.Marshal` sorts object keys alphabetically but preserves array ordering. The `prefix` field is `[]ChatMessage` (array), so it's stable. However:

- `toolSchemaHash` hashes `opts.Tools` which is `[]Tool` — array ordering depends on the caller. If the tool manifest adds/deletes/reorders tools between runs, the hash changes even if the semantic content is the same.
- `modelCallPrefixDiagnostics` receives `prompt []llama.ChatMessage` and `opts llama.ChatOptions` — if either struct adds a new field that `json.Marshal` serializes, the hash silently changes.

**This is acceptable for a diagnostic hash** (the design doc says it's `telemetry_only`). But consumers must not use `prefix_hash` as a cache key because Go `json.Marshal` field ordering is not cross-version guaranteed (only alphabetical key ordering is). Document this restriction.

**Fix:** Add a comment on `stableHash` or at the call site: "This hash is diagnostic-only. Do not use as a cache key — Go json.Marshal field ordering is not a cross-version stable serialization."

---

### FINDING 5 — Direct Answer Surface Override Bypasses Phase Contract Tool Visibility [MODERATE RISK]

**Location:** `runner.go` (dirty WIP)

```go
// Direct-answer surface inventory overrides the allowed tool list
if surfaceTools := DirectAnswerSurfaceForBriefTools(r.RuntimeBrief); surfaceTools != nil {
    allowed = append([]string(nil), surfaceTools...)
    // ...emits event
}
// THEN turnHiddenTools filtering
```

This discards the phase contract's `AllowedTools` for direct-answer lanes. The surface becomes the single authority. Two concerns:

1. The surface must be a **superset** of what the phase contract allows, otherwise the model can be denied tools the phase contract intends to give it.
2. The surface emission event occurs before `turnHiddenTools` filtering. `turnHiddenTools` is processed after, so hidden tools are still filtered from the surface. This is correct but fragile — any future reordering of these three blocks (contract tools → surface override → hidden filter) could silently break tool visibility.

**Fix:** Document the dependency in a comment above the surface override block: "Surface replaces contract AllowedTools. The turnHiddenTools filter below still applies. Surface must be a superset of contract AllowedTools for this lane."

---

### FINDING 6 — DeepSeek `ReasoningContent` Preservation Is Correct [PASS]

**Location:** `client.go:624-648` (dirty WIP)

```go
preserveReasoning := util.EqualTrimFold(backend, config.BackendDeepSeek)
for i, msg := range messages {
    if !preserveReasoning || msg.Role != "assistant" {
        msg.ReasoningContent = ""
    }
}
```

Correct: DeepSeek hosted-chat API requires `reasoning_content` continuity on assistant messages in follow-up requests. Only assistant role is preserved; system/user/tool messages still strip reasoning content. Backend-scoped so other providers (Ollama, OpenAI) are unaffected.

**No changes required.** This is a model/harness protocol compliance fix that matches documented DeepSeek API behavior.

---

### FINDING 7 — Transport Retry Uses String Matching on Error Messages [LOW RISK]

**Location:** `runner.go` (dirty WIP)

```go
errStr := strings.ToLower(err.Error())
if transportRetries < 1 && (strings.Contains(errStr, "transport failed") ||
    strings.Contains(errStr, "eof") || strings.Contains(errStr, "connection reset")) {
```

The retry trigger depends on error message wording from the transport layer. Three different backends (llama.cpp, DeepSeek hosted, OpenAI) may produce different error messages. This risks either retrying when it shouldn't (false positive from a different error containing "eof") or not retrying when it should (unrecognized transport error wording).

**No changes required** for this review — this is a Harness/Go concern, not protocol. Flag as a future consolidation item with typed transport errors.

---

### FINDING 8 — `leadingSystemPromptPrefix` Only Captures Contiguous Leading System Messages [LOW RISK, DOCUMENT]

**Location:** `runner_prefix_diagnostics.go:30-38`

```go
func leadingSystemPromptPrefix(prompt []llama.ChatMessage) []llama.ChatMessage {
    var prefix []llama.ChatMessage
    for _, msg := range prompt {
        if msg.Role != "system" {
            break
        }
        prefix = append(prefix, llama.ChatMessage{Role: msg.Role, Content: msg.Content})
    }
    return prefix
}
```

If a user message is followed by an injected system message (e.g., from recovery nudge or transport retry injection), the second system message is **excluded** from the prefix hash. The prefix hash only covers the initial contiguous block of system messages.

**This is correct** for typical prompt construction where all system prompts are at the start, but the design doc's "stable prefix hash" claim should note: "Covers leading system messages only. Injected runtime messages after the first user turn do not affect the prefix hash."

---

## Required Pre-Merge Fixes

| # | Finding | Required Before Merge |
|---|---------|----------------------|
| **F1** | `prefix_cache_signal_present: false` contradicts `prompt_cache_hit_tokens: n` | Delete dead `prefix_cache_signal_present` + `prefix_cache_signal_provider` from committed diagnostics, update test |
| **F2** | 12+ undocumented `model_call` fields | Add event contract comment specifying stable vs volatile vs conditional fields |
| **F3** | `taskLedgerReadTool` hardcoded list incomplete | Replace with workspace classification of read/discovery tools, or add all missing tools |

## Recommended Pre-Merge Fixes

| # | Finding | Recommendation |
|---|---------|---------------|
| **F4** | `stableHash` field ordering not guaranteed | Add doc comment: "diagnostic only, do not use as cache key" |
| **F5** | Surface override bypasses contract tools | Add comment documenting superset dependency |
| **F8** | Prefix hash only covers contiguous leading system messages | Document the boundary in the function comment |

---

## Verdict

**PASS_WITH_RISKS**

Three required fixes before merge:

1. **F1**: Resolve the `prefix_cache_signal_present` vs `prompt_cache_hit_tokens` contradiction — the dead field in committed code will poison downstream consumers once the dirty cache accounting lands.
2. **F2**: Specify the `model_call` event contract for the 12+ new fields — future contributors need to know which are stable vs volatile.
3. **F3**: Fix the `taskLedgerReadTool` hardcoded list — it's missing ~10 read/discovery tools and will cause false evidence rejections for any model using those tools.

**SIGN-OFF: CONDITIONAL** — conditions F1, F2, F3 above must be resolved before this effort is considered merged. No changes to prompt files detected (zero prompt drift). DeepSeek usage schema is correct. Direct answer surface protocol is sound with the documented dependency caveat.
  · 85723 tok · in 81612 (81152 cached / 460 new) · out 4111 (461 reasoning) · ¥0.0103
