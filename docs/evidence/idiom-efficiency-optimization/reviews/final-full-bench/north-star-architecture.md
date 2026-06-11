[2m  ▎ thinking[0m
  · 17015 tok · in 16836 (16640 cached / 196 new) · out 179 (70 reasoning) · ¥0.0009
  -> bash {"command": "cd /Users/ben/Documents/GitHub/idiom && git status --short"}
  -> bash {"command": "cd /Users/ben/Documents/GitHub/idiom && git log --oneline -30"}
[2m  ▎ thinking[0m
  · 18656 tok · in 18423 (17152 cached / 1271 new) · out 233 (66 reasoning) · ¥0.0021 · cache prefix changed: tools
  -> read_file {"path": "/Users/ben/Documents/GitHub/idiom/docs/design-direct-answer-efficiency.md"}
  -> read_file {"path": "/Users/ben/Documents/GitHub/idiom/docs/design-hosted-cache-cost-metrics.md"}
  -> ls {"path": "/Users/ben/Documents/GitHub/idiom/docs/evidence/idiom-efficiency-optimization"}
[2m  ▎ thinking[0m
  · 28686 tok · in 28472 (18560 cached / 9912 new) · out 214 (44 reasoning) · ¥0.0107
  -> bash {"command": "cd /Users/ben/Documents/GitHub/idiom && git diff --stat HEAD"}
  -> bash {"command": "cd /Users/ben/Documents/GitHub/idiom && git diff --name-only HEAD~1..HEAD"}
  -> bash {"command": "cd /Users/ben/Documents/GitHub/idiom && git log HEAD~5..HEAD --format=\"%h %s\""}
[2m  ▎ thinking[0m
Let me examine the actual diffs more carefully to distinguish effort changes from unrelated noise.
  · 31460 tok · in 29405 (28672 cached / 733 new) · out 2055 (1780 reasoning) · ¥0.0054
  -> bash {"command": "cd /Users/ben/Documents/GitHub/idiom && git diff HEAD -- .codex/README.md AGENTS.md internal/contracts/writ...
  -> bash {"command": "cd /Users/ben/Documents/GitHub/idiom && git diff HEAD -- internal/runtime/llama/client.go internal/runtime/...
  -> bash {"command": "cd /Users/ben/Documents/GitHub/idiom && git diff HEAD -- internal/runtime/agent/runner.go internal/runtime/...
[2m  ▎ thinking[0m
  · 39997 tok · in 39708 (31360 cached / 8348 new) · out 289 (44 reasoning) · ¥0.0096
  -> bash {"command": "cd /Users/ben/Documents/GitHub/idiom && git diff HEAD -- internal/runtime/agent/read_only_closure.go intern...
  -> bash {"command": "cd /Users/ben/Documents/GitHub/idiom && git diff HEAD -- internal/surfaces/cli/route/writer_route.go intern...
  -> bash {"command": "cd /Users/ben/Documents/GitHub/idiom && git diff HEAD -- internal/evidence/transcript/debuglog.go internal/...
[2m  ▎ thinking[0m
  · 48910 tok · in 48733 (39936 cached / 8797 new) · out 177 (60 reasoning) · ¥0.0099
  -> read_file {"path": "/Users/ben/Documents/GitHub/idiom/internal/runtime/agent/direct_answer_surface.go"}
  -> read_file {"path": "/Users/ben/Documents/GitHub/idiom/internal/runtime/agent/runner_runstate.go"}
[2m  ▎ thinking[0m
  · 56984 tok · in 56739 (48896 cached / 7843 new) · out 245 (44 reasoning) · ¥0.0093
  -> bash {"command": "cd /Users/ben/Documents/GitHub/idiom && git diff HEAD -- internal/surfaces/tui/app/model_state.go | head -3...
  -> bash {"command": "cd /Users/ben/Documents/GitHub/idiom && git diff HEAD -- internal/surfaces/cli/lane/explain.go internal/sur...
  -> read_file {"path": "/Users/ben/Documents/GitHub/idiom/internal/runtime/llama/deepseek_test.go"}
[2m  ▎ thinking[0m
  · 69871 tok · in 69732 (56960 cached / 12772 new) · out 139 (18 reasoning) · ¥0.0142
  -> read_file {"path": "/Users/ben/Documents/GitHub/idiom/internal/runtime/agent/direct_answer_surface_test.go"}
  -> read_file {"path": "/Users/ben/Documents/GitHub/idiom/internal/runtime/agent/runner_usage_metrics_test.go"}
[2m  ▎ thinking[0m
  · 74920 tok · in 74594 (69760 cached / 4834 new) · out 326 (33 reasoning) · ¥0.0069
  -> read_file {"path": "/Users/ben/Documents/GitHub/idiom/internal/runtime/agent/lane_policy_test.go", "limit": 50}
  -> read_file {"path": "/Users/ben/Documents/GitHub/idiom/internal/surfaces/cli/route/writer_route_test.go", "limit": 50}
  -> read_file {"path": "/Users/ben/Documents/GitHub/idiom/internal/surfaces/tui/app/writer_route_test.go", "limit": 50}
  -> read_file {"path": "/Users/ben/Documents/GitHub/idiom/internal/surfaces/tui/app/session_summary_test.go", "limit": 80}
[2m  ▎ thinking[0m
  · 77482 tok · in 77278 (74880 cached / 2398 new) · out 204 (14 reasoning) · ¥0.0043
  -> bash {"command": "wc -l /Users/ben/Documents/GitHub/idiom/internal/surfaces/tui/app/session_summary_test.go"}
  -> bash {"command": "wc -l /Users/ben/Documents/GitHub/idiom/internal/runtime/agent/lane_policy_test.go"}
  -> bash {"command": "cd /Users/ben/Documents/GitHub/idiom && git diff HEAD -- internal/runtime/agent/runner_plan_then_execute_gu...
[2m  ▎ thinking[0m
  · 78667 tok · in 78442 (77440 cached / 1002 new) · out 225 (88 reasoning) · ¥0.0030
  -> bash {"command": "cd /Users/ben/Documents/GitHub/idiom && git diff HEAD -- internal/runtime/agent/runner.go | grep -A3 -B3 \"...
  -> bash {"command": "cd /Users/ben/Documents/GitHub/idiom && git diff HEAD -- internal/runtime/agent/runner.go | head -40"}
[2m  ▎ thinking[0m
  · 81184 tok · in 79548 (78592 cached / 956 new) · out 1636 (1458 reasoning) · ¥0.0058
  -> ls {"path": "/Users/ben/Documents/GitHub/idiom/docs/evidence/idiom-efficiency-optimization", "recursive": true}
  -> bash {"command": "head -50 /Users/ben/Documents/GitHub/idiom/docs/refactor-phase2-plan.md"}
  -> ls {"path": "/Users/ben/Documents/GitHub/idiom/.codex/agents"}
[2m  ▎ thinking[0m
  · 83816 tok · in 83695 (81152 cached / 2543 new) · out 121 (42 reasoning) · ¥0.0044
  -> read_file {"path": "/Users/ben/Documents/GitHub/idiom/docs/evidence/idiom-efficiency-optimization/reviews/final-full-bench/north-s...
[2m  ▎ thinking[0m
Now I have a thorough picture. Let me also check the tests compile and get a quick test result.
  · 86018 tok · in 85812 (83712 cached / 2100 new) · out 206 (115 reasoning) · ¥0.0042
  -> bash {"command": "cd /Users/ben/Documents/GitHub/idiom && go build ./cmd/idiom 2>&1 | head -30"}
[2m  ▎ thinking[0m
  · 86242 tok · in 86029 (86016 cached / 13 new) · out 213 (21 reasoning) · ¥0.0022
  -> bash {"command": "cd /Users/ben/Documents/GitHub/idiom && go test ./internal/runtime/llama -run 'Usage|DeepSeek|Cache|Reasoni...
  -> bash {"command": "cd /Users/ben/Documents/GitHub/idiom && go test ./internal/runtime/agent -run 'DirectAnswer|Usage|BudgetLed...
[2m  ▎ thinking[0m
  · 88676 tok · in 88351 (86144 cached / 2207 new) · out 325 (151 reasoning) · ¥0.0046
  -> bash {"command": "cd /Users/ben/Documents/GitHub/idiom && go test ./internal/runtime/agent -run 'TestEmitAcceptedAnswerSignal...
  -> bash {"command": "cd /Users/ben/Documents/GitHub/idiom && grep -n \"TestEmitAcceptedAnswerSignalsDirectAnswerAliasBlocksUnrel...
  · 88931 tok · in 88834 (88576 cached / 258 new) · out 97 · ¥0.0022
  -> read_file {"path": "/Users/ben/Documents/GitHub/idiom/internal/runtime/agent/runner_test.go", "offset": 17313, "limit": 60}
[2m  ▎ thinking[0m
  · 90308 tok · in 89916 (88832 cached / 1084 new) · out 392 (312 reasoning) · ¥0.0036
  -> grep {"pattern": "func closurePromptEvidencePaths", "path": "/Users/ben/Documents/GitHub/idiom/internal/runtime/agent"}
  · 90498 tok · in 90402 (90240 cached / 162 new) · out 96 · ¥0.0022
  -> read_file {"offset": 18772, "path": "/Users/ben/Documents/GitHub/idiom/internal/runtime/agent/runner.go", "limit": 15}
[2m  ▎ thinking[0m
Let me read the specific test assertion line in context to fully understand the evidence_paths leak.
  · 92440 tok · in 90736 (90368 cached / 368 new) · out 1704 (1589 reasoning) · ¥0.0056
  -> read_file {"limit": 20, "offset": 17346, "path": "/Users/ben/Documents/GitHub/idiom/internal/runtime/agent/runner_test.go"}
[2m  ▎ thinking[0m
---

# North-Star / Architecture Review: Efficiency / Cache-Cost Effort

**Status: PASS_WITH_RISKS** — one design ambiguity that broke an existing invariant, plus boundary noise to separate before landing.

---

## 1. Effort Files vs Unrelated Dirty Noise

### Effort (efficiency/cache-cost) — files reviewed

| Atom | Files | Status |
|------|-------|--------|
| **A6** Lightweight Direct-Answer Surface | `direct_answer_surface.go` (new), `direct_answer_surface_test.go` (new), `runner.go` (surface injection), `read_only_closure.go` (shared decision) | ✅ |
| **A1** Exact-Path Prefetch (disabled by default) | `direct_answer_surface.go` (`PrefetchEligibility` — capability NOT advertised) | ✅ |
| **A2** Citation Classifier | `final_answer_acceptance.go`, `read_only_closure.go`, `read_only_answerability_test.go` | ⚠️ (see #1 below) |
| **A3** DeepSeek Reasoning-Content Protocol Repair | `client.go` (`hostedChatMessages`), `deepseek_test.go` (new) | ✅ |
| **A4** Effective Model Telemetry | `writerroute/decision.go`, `cli/route/writer_route.go`, `tui/app/writer_route.go`, `cli/route/writer_route_test.go` (new), `tui/app/writer_route_test.go` (new) | ✅ |
| **A5** Hosted Cache/Cost Metrics | `client.go` (Usage), `runner.go`, `runner_state.go`, `debuglog.go`, `model_state.go`, `explain.go`, `post_analyze.go`, `runner_usage_metrics_test.go` (new), `session_summary_test.go` (new), `debuglog_test.go` | ✅ |
| **Design docs** | `docs/design-direct-answer-efficiency.md`, `docs/design-hosted-cache-cost-metrics.md` | ✅ |
| **Benchmark artifacts** | `docs/evidence/idiom-efficiency-optimization/` | ✅ |

### Unrelated dirty noise — must separate before shipping

| File | Why not effort | Action |
|------|---------------|--------|
| `.codex/README.md` `AGENTS.md` | Codex subagent guidance | Separate commit or removal |
| `.codegraph/` | Auto-generated index | `.gitignore` or separate |
| `.codex/agents/` | Codex agent `.toml` configs | Separate commit |
| `docs/refactor-phase2-plan.md` | Plan to wire `runner_runstate.go` into runner.go — separate refactor | Separate |
| `global-reasonix-config.toml` `reasonix.toml` | Dev config | Remove or `.gitignore` |
| `internal/surfaces/cliassets/base.md` `prompts/assets.go` | Generated assets | Separate commit |
| `internal/runtime/agent/runner_runstate.go` | Closure→struct extraction, not wired in yet (dead code per phase2-plan) | Separate commit or remove from working tree |
| `internal/runtime/agent/runner_plan_then_execute_guard.go` | Evidence-ledger source-satisfied hardening, separate concern | Separate commit |
| `internal/runtime/agent/lane_policy_test.go` | Conversation lane policy for read-only lanes — behavior fix, not efficiency | Separate commit |

---

## 2. North-Star Authority Boundaries

### Model vs Harness: ✅ CLEAN

The effort strictly honors the north-star separation:

- **Exact-path prefetch (A1) is disabled by default.** The `directAnswerSurfaceCapabilities()` function intentionally does NOT advertise `source_body_prefetch`. The prefetch eligibility function returns typed miss reasons (`direct_answer_surface_missing`, `direct_answer_surface_stale:`, `direct_answer_surface_no_source_body_prefetch_capability`). The A1 atom cannot run until the version is bumped and the capability is explicitly advertised. This is the ideal north-star pattern: harness provides the contract; the model does not decide whether prefetch is active.

- **Direct-answer surface inventory is typed and compile-time enforced**, not prompt text. Tools are listed, excluded by set membership, and consumed by `DirectAnswerSurfaceForBrief` which gates on `readOnlyDirectAnswerLane`. No string-pattern matching, no prompt nudges.

- **Provider facts and pricing estimates are in separate layers.** `llama.Usage` stores only provider-reported fields (`PromptCacheHitTokens`, `PromptCacheMissTokens`, `ReasoningTokens`). Pricing is in benchmark scripts (`scripts/benchmark-adapter.py`). The `prompt_cache_accounting_known` boolean distinguishes "known zero" from "unknown" — exactly the right contract.

- **DeepSeek protocol repair is at the client boundary**, with backend-specific branching (`util.EqualTrimFold(backend, config.BackendDeepSeek)`). Non-DeepSeek hosted backends and local backends are each tested with separate fixtures. No leak into the runner, prompts, or model-facing protocol.

- **Effective model telemetry (A4) separates role alias from effective model** at the decision layer, with a typed `EffectiveModelForDecision` function. The `writer_route.go` change correctly flips the priority: direct config over alias, not the other way around.

### What "No scenario-specific hacks" means here: ✅ PASS

Every behavioral change is a typed runtime contract:

- Surface inventory: struct + function, not prompt language
- Citation classification: shared `FinalAnswerDecision` method, not one-off string checks
- DeepSeek protocol: backend string comparison, not model-id pattern matching
- Cache metrics: additive optional fields with `*_known` boolean, not hardcoded field names
- Model attribution: `EffectiveModelForDecision` switch on `WriterMode`, not prompt inspection

No instance of "if the user says README.md, do X" — everything is gated on typed task family, contract, permission profile, or backend identity.

---

## 3. Specific Findings

### FINDING #1 (RISK — must resolve before landing): Evidence paths leak on blocked answers

**File:** `internal/runtime/agent/final_answer_acceptance.go`

**What changed:** When `evaluateFinalAnswerAcceptance` reaches the source-grounding rejection branch, the new code attaches evidence paths from the relevance check:

```go
if len(acceptance.EvidencePaths) == 0 {
    acceptance.EvidencePaths = closurePromptEvidencePaths(directAnswerRelevance.RelevantSourceBodyPaths, 8)
}
return reject("source_grounding_unmet", reason)
```

Same pattern in the `directAnswerRelevance.BlockerKind` branch.

**Test that fails:** `TestEmitAcceptedAnswerSignalsDirectAnswerAliasBlocksUnrelatedEvidence` (line 17358-17359) asserts that a blocked answer — where the model cites a file but does not address the user's objective — must NOT carry evidence paths in the readiness_check event:

```go
if paths, _ := readiness["evidence_paths"].([]any); len(paths) != 0 {
    t.Fatalf("direct_answer alias blocked telemetry must not fall back to unrelated evidence paths, got %#v", paths)
}
```

The test was intentional: when telemetry shows `evidence_paths=["internal/surfaces/tui/app/rendering.go"]` but `acceptance_decision="rejected"`, downstream consumers may misinterpret the path list as evidence that grounds the answer.

**Root cause:** The `FinalAnswerDecision` refactor in `read_only_closure.go` introduced a shared code path that populates `acceptedEvidencePaths` for all terminal outcomes, including blocked ones. The caller in `evaluateFinalAnswerAcceptance` then copies those paths into `acceptance.EvidencePaths` before the rejection is finalized.

**Required fix:** Apply the design decision explicitly:
- **Option A:** Blocked answers should carry empty `evidence_paths` — fix the code to not attach paths on rejection branches. This preserves telemetry clarity.
- **Option B:** Blocked answers legitimately carry evidence paths when the model inspected relevant source — update the test assertion to accept the new behavior, with a documented rationale in the commit message and a comment in the test explaining why blocked-telemetry path presence is now expected.

**Recommendation:** Option A (restore old invariant) unless there is an explicit consumer that needs evidence paths on blocked answers. The test name says "must not fall back to unrelated evidence paths" — this was a deliberate invariant.

### FINDING #2 (BOUNDARY): Transport retry in runner.go is undocumented in design docs

**File:** `internal/runtime/agent/runner.go` lines ~740-750

A single generic transport retry was added:
```go
if transportRetries < 1 && (strings.Contains(errStr, "transport failed") || strings.Contains(errStr, "eof") || strings.Contains(errStr, "connection reset")) {
```

This is a reasonable generic resilience improvement surfaced by the benchmark's DeepSeek connectivity issues. However, it is not described in either design doc, has no configuration, no test fixture, and uses string matching on error messages. It's a one-off escape hatch.

**Recommendation:** Either document it in the design doc as a lightweight resilience measure with the rationale that hosted-provider transports are unreliable, or add a typed `enableTransportRetry` policy field so it is not an undocumented string-match escape hatch. For now I recommend documenting rather than over-engineering — the current implementation is minimal and bounded (max 1 retry).

### FINDING #3 (NOISE / NOT BLOCKING): runner_runstate.go is dead code

`internal/runtime/agent/runner_runstate.go` (336 lines) extracts mutable Runner.Run state into a `planExecuteRunState` struct. It builds clean but is entirely dead code — none of its methods are called from `runner.go`. This is documented in `docs/refactor-phase2-plan.md` as phase 2 of a refactoring, but it sits in the working tree alongside the efficiency changes.

**Recommendation:** Remove from the efficiency-effort working tree. It is a separate refactoring with its own risk and review surface. Ship it when the wiring into `runner.go` is also done.

### FINDING #4 (NOT BLOCKING): runner_plan_then_execute_guard.go change is separate concern

The `requiredPathsHaveSourceBodyEvidence` function and its evidence-ledger freshness check in `planThenExecuteSourceSatisfiedAnswerCovers` is a plan-execute contract hardening. It was done as part of the "Harden runtime contracts from Reasonix review" commit. It is completely unrelated to the efficiency/cache-cost effort.

**Recommendation:** Separate into its own commit with commit message explaining the stale-evidence invariant it prevents, which failure mode motivated it, and the verification run.

### FINDING #5 (NOT BLOCKING): Lane policy test is separate concern

`lane_policy_test.go` (106 lines, new) tests that read-only investigation lanes (architecture, locate, review) get `ConversationLanePolicy`. This is a behavior fix for non-deterministic force-answer behavior — a pre-existing issue surfaced during this session but not in the design docs.

**Recommendation:** Separate commit.

---

## 4. Architecture Boundary Review

| Boundary | Assessment |
|----------|-----------|
| `llama.Usage` ↔ provider response | ✅ Clean: custom `UnmarshalJSON` parses flat fields and nested `completion_tokens_details` in one pass; uses raw field presence for `PromptCacheAccountingKnown` |
| `llama.Usage` ↔ `runnerMetrics` | ✅ Clean: `addUsage` sums non-negative ints, ORs the boolean; no synthesis of hit/miss from prompt tokens |
| Runner ↔ TUI/transcript/CLI | ✅ Clean: cache fields flow through `model_call` JSONL event payload; TUI and transcript parse the same payload; backwards compatible (zero value is missing) |
| Routing ↔ model attribution | ✅ Clean: `EffectiveModelForDecision` is a pure function on `WriterMode`; CLI and TUI each have their own copy (unfortunate duplication but follows existing pattern) |
| DeepSeek ↔ non-DeepSeek clients | ✅ Clean: `hostedChatMessages(backend string)` preserves `reasoning_content` only for `config.BackendDeepSeek` on assistant messages; non-DeepSeek backends and local backends are tested with separate fixtures |
| Provider facts ↔ pricing estimates | ✅ Clean: `llama.Usage` has no pricing fields; `scripts/benchmark-adapter.py` owns pricing; `prompt_cache_accounting_known` keeps the distinction explicit |

### Duplication concern: `EffectiveModelForDecision` and `appEffectiveModelForWriterRoute` are identical

`internal/surfaces/cli/route/writer_route.go` and `internal/surfaces/tui/app/writer_route.go` each have a copy of the same switch statement. The TUI version was written because the CLI version lives in a package the TUI doesn't import. This follows the existing pattern in the codebase (both files have their own `ModelForRole`/`writerRouteModelForRole`), but the duplication is unfortunate.

**Recommendation:** Not blocking for this atom. If a third consumer appears, extract to `writerroute.EffectiveModelForDecision`.

---

## 5. Test Coverage Quality

| Area | Coverage | Assessment |
|------|----------|------------|
| A6 surface | 5 tests: tool filtering, write lane rejection, prefetch eligibility (3 negative cases, 1 positive), event emission, runner integration (2 end-to-end) | ✅ Strong |
| A5 cache parsing | `TestCompleteChatParsesHostedCacheUsageAndReasoningTokens`, `known zero`, `omitted` | ✅ Covers all three states |
| A5 runner aggregation | `TestRunnerMetricsAggregatesProviderCacheUsage`, `TestRunnerBudgetLedgerSerializesProviderCacheUsage` | ✅ |
| A5 TUI/transcript propagation | `TestModelCloseSessionSummaryIncludesProviderCacheUsage` + debuglog test updates | ✅ |
| A3 DeepSeek protocol | 5 test functions covering omitted/empty/normal/multiline/special/truncated/long reasoning, streamed, non-assistant stripping, non-DeepSeek backends, tool-call assistant, local backends | ✅ Comprehensive |
| A4 model telemetry | `TestBuildDecisionEffectiveModelPrefersDirectConfigOverRoleAlias`, alias fallback | ✅ |
| A2 citation classifier | Existing `TestFinalAnswerAcceptanceRejectsSessionsPanelAsChatRecallOwner` extended; **but see Finding #1** | ⚠️ |

---

## 6. Verdict

**PASS_WITH_RISKS**

### What passes clean

1. **Model-vs-harness authority.** A1 is disabled by default behind an unadvertised capability. The surface inventory is typed, compiled, and gate-checked. No prompt text nudges.
2. **Provider facts vs pricing estimates.** `llama.Usage` has no pricing fields. The `*_known` booleans are the right abstraction.
3. **No scenario-specific hacks.** Every behavioral change is a typed contract, struct, or function. Zero string-pattern or prompt-wording special cases.
4. **Clean boundaries.** DeepSeek repair at client boundary. Model attribution at routing layer. Cache metrics as additive fields through the event pipeline.
5. **North-star alignment throughout.** Evidence gates are invariant-based, telemetry is explicit, atom sequencing is enforced by typed capability gates.

### What must be resolved before shipping

1. **FINDING #1**: The evidence-paths leak on blocked answers breaks an existing test and a deliberate invariant. Decide Option A (restore empty paths on blocked answers — recommended) or Option B (accept new behavior with rationale). This affects `final_answer_acceptance.go` and the test at `runner_test.go:17358`.

### What should be separated before the PR

- `internal/runtime/agent/runner_runstate.go` (dead code refactor)
- `internal/runtime/agent/runner_plan_then_execute_guard.go` (evidence-ledger change)
- `internal/runtime/agent/lane_policy_test.go` (lane policy fix)
- `.codex/README.md`, `AGENTS.md` (Codex subagent guidance)
- `.codegraph/`, `.codex/agents/`, `docs/refactor-phase2-plan.md`, config files, generated assets

### What should be documented

- Transport retry in `runner.go` — add a rationale comment or brief design-doc mention explaining it was motivated by benchmark-observed hosted-provider transport failures before the generic retry medallion exists.
  · 96434 tok · in 92796 (92416 cached / 380 new) · out 3638 (16 reasoning) · ¥0.0095
