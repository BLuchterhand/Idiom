[2m  ▎ thinking[0m
  · 17305 tok · in 16945 (16640 cached / 305 new) · out 360 (132 reasoning) · ¥0.0014
  -> read_file {"path": "/Users/ben/Documents/GitHub/idiom/docs/design-direct-answer-efficiency.md"}
  -> read_file {"path": "/Users/ben/Documents/GitHub/idiom/docs/evidence/idiom-efficiency-optimization/ledger.md"}
  -> read_file {"path": "/Users/ben/Documents/GitHub/idiom/docs/evidence/idiom-efficiency-optimization/agents/final-drift-review-rerun....
  -> bash {"command": "cd /Users/ben/Documents/GitHub/idiom && git diff HEAD"}
[2m  ▎ thinking[0m
Now let me verify the actual code state — the new A6 files and the A3 tests that the diff doesn't show (they're new files):
  · 39824 tok · in 38940 (17152 cached / 21788 new) · out 884 (716 reasoning) · ¥0.0239 · cache prefix changed: tools
  -> glob {"pattern": "internal/runtime/agent/direct_answer_surface*"}
  -> glob {"pattern": "internal/runtime/llama/deepseek_test*"}
  -> bash {"command": "cd /Users/ben/Documents/GitHub/idiom && git status --short"}
[2m  ▎ thinking[0m
  · 40466 tok · in 40170 (39808 cached / 362 new) · out 296 (125 reasoning) · ¥0.0018
  -> read_file {"path": "/Users/ben/Documents/GitHub/idiom/internal/runtime/agent/direct_answer_surface.go"}
  -> read_file {"path": "/Users/ben/Documents/GitHub/idiom/internal/runtime/agent/direct_answer_surface_test.go"}
  -> read_file {"path": "/Users/ben/Documents/GitHub/idiom/internal/runtime/llama/deepseek_test.go"}
[2m  ▎ thinking[0m
Good — I have all the source. Now let me run the focused test suites to verify PASS/WITH_RISKS claims independently:
  · 55128 tok · in 54160 (40448 cached / 13712 new) · out 968 (749 reasoning) · ¥0.0165
  -> bash {"command": "cd /Users/ben/Documents/GitHub/idiom && go test ./internal/runtime/llama ./internal/contracts/writerroute ....
  -> bash {"command": "cd /Users/ben/Documents/GitHub/idiom && go test ./internal/runtime/agent -run 'DirectAnswerSurface|DirectAn...
[2m  ▎ thinking[0m
All 52 focused tests pass across both test groups. Let me now do the final verification sweeps:
  · 57601 tok · in 57258 (55040 cached / 2218 new) · out 343 (65 reasoning) · ¥0.0040
  -> bash {"command": "cd /Users/ben/Documents/GitHub/idiom && grep -rn 'EvidenceCitationsOnly\\|citationClassifier\\|HostedUsage\...
  -> bash {"command": "cd /Users/ben/Documents/GitHub/idiom && go build ./... 2>&1"}
  -> bash {"command": "cd /Users/ben/Documents/GitHub/idiom && grep -n 'DirectAnswerSurfaceForBrief\\|directAnswerSurfaceEmitted\\...
[2m  ▎ thinking[0m
All evidence collected. Here is my independent final alignment verdict.

---

# Final Alignment Review — Verdict: **PASS_WITH_RISKS**

**Effort:** `idiom-efficiency-optimization`  
**Date:** 2026-06-11  
**Design doc:** `docs/design-direct-answer-efficiency.md`  
**Ledger:** `docs/evidence/idiom-efficiency-optimization/ledger.md`  
**Retained atoms:** A3 (DeepSeek reasoning protocol), A4 (effective model telemetry), A6 (lightweight direct-answer surface)

---

## Three Drift-Risk Items — Verified

### 1. Runner-level prefetch-disabled coverage ✅

`TestRunnerDirectAnswerSurfaceKeepsPrefetchDisabled` in `direct_answer_surface_test.go` (lines 177–258) is a **genuine runner-level integration test** — it constructs a `Runner` with a direct-answer brief, runs `Runner.Run()` through a mock server, and asserts:

- The `direct_answer_surface` event does **not** advertise `source_body_prefetch`
- **Zero** prefetch events are emitted
- The answer is still produced and grounded (`Source: README.md`)
- The expected model request count is satisfied

This is not a unit test of `DirectAnswerSurfacePrefetchEligibility` in isolation; it exercises the full runner tool-allowed path and surface emission.

### 2. End-to-end `search_content` + `read_file` direct-answer investigation ✅

`TestRunnerDirectAnswerSurfaceAllowsSearchThenBoundedRead` in the same file (lines 260–329) is a **runner-level integration test** proving non-trivial read-only investigation works:

- Model replies: `search_content` → `read_file` (bounded) → grounded final answer
- Asserts `searchResults=1`, `readResults=1`
- Asserts final answer is grounded (`Source: README.md`)
- Asserts correct model request count

This proves the lightweight surface does not collapse read-only investigation into a trivial path-only answer — the design's **negative oracle** is satisfied at the runner level.

### 3. Dirty-diff separation ✅

The efficiency changes (A3/A4/A6) are confined to these files:

| File | Atom | Nature |
|---|---|---|
| `internal/runtime/agent/direct_answer_surface.go` | A6 | New file |
| `internal/runtime/agent/direct_answer_surface_test.go` | A6 | New file (all runner-level tests) |
| `internal/runtime/llama/client.go` (line 502, 598–605) | A3 | Targeted `hostedChatMessages` change |
| `internal/runtime/llama/deepseek_test.go` | A3 | New file (parameterized fixtures) |
| `internal/contracts/writerroute/decision.go` | A4 | +`EffectiveModel` field |
| `internal/contracts/writerroute/decision_test.go` | A4 | +`effective_model` assertion |
| `internal/surfaces/cli/route/writer_route.go` | A4 | +`EffectiveModelForDecision` |
| `internal/surfaces/tui/app/writer_route.go` | A4 | +`appEffectiveModelForWriterRoute` |
| `internal/runtime/agent/runner.go` (lines 223–228, 316–330) | A6 | Surface event integration |

Pre-existing dirty (documented in the ledger, separate blocks):
- `internal/runtime/agent/runner.go` lines 737–749 — transport retry resilience
- `internal/runtime/agent/runner_plan_then_execute_guard.go` — evidence-ledger check
- `.codex/README.md`, `AGENTS.md` — subagent guidance

The A6 integration in `runner.go` sits adjacent to but **not interleaved with** the pre-existing transport retry block. The ledger records this honestly. No cherry-pick ambiguity was created.

---

## Focused Tests — All Pass

| Test Group | Command | Result |
|---|---|---|
| A3 + A4 (llama, writerroute, route, app) | `-run 'DeepSeek\|WriterRoute\|Decision\|EffectiveModel'` | **ALL PASS** (4 packages, ok) |
| A6 (agent) | `-run 'DirectAnswerSurface\|DirectAnswer'` | **ALL PASS** (28 subtests, ok) |
| Build | `go build ./...` | **CLEAN** |

No new failures introduced. Two pre-existing final-answer acceptance test failures (`TestFinalAnswerAcceptanceRejectsEchoedObjectivePathInventoryFromLiveTUIQuestion`, `TestFinalAnswerAcceptanceRejectsSessionsPanelAsChatRecallOwner`) are confirmed pre-existing — they fail identically without efficiency changes.

---

## A1/A2/A5 Cleanliness — Verified

```
NO LEFTOVER A1/A2/A5 SYMBOLS
```

No `EvidenceCitationsOnly`, `citationClassifier`, `HostedUsage`, `DirectAnswerPrefetch`, `CacheHitTokens`, `CacheMissTokens`, `cost_known`, `PromptCacheHitKnown` symbols exist in any `.go` file. The `source_body_prefetch` constant lives only in `direct_answer_surface.go` as a **capability string intentionally not advertised** — confirmed by the negative test.

---

## Ledger Honesty — Verified

Every claim in `docs/evidence/idiom-efficiency-optimization/ledger.md` matches the current workspace state:

| Ledger Claim | Verdict |
|---|---|
| A6 implemented; A6 reviewer PASS_WITH_RISKS | ✓ |
| A3 implemented; focused tests pass | ✓ |
| A4 implemented; focused tests pass | ✓ |
| A1 blocked; partial agent edits reverted | ✓ (no A1 symbols) |
| A2 blocked; reverted after regressions | ✓ (no A2 symbols) |
| A5 blocked; reverted | ✓ (no A5 symbols) |
| Dirty overlap documented | ✓ (matches observed state) |
| Retained verification commands match | ✓ |

---

## Benchmarks

**Benchmarks MAY be rerun.** The retained implementation (A3+A4+A6) is complete, tested, and builds cleanly. Rerunning `.idiom/benchmark/20260611-122151` against the direct-answer surface, DeepSeek reasoning protocol, and effective-model telemetry would confirm the expected improvements. A1/A2/A5 remain blocked — benchmark results against those atoms would not change.

**Rerunning benchmarks is optional** for this review; no blocker prevents it.

---

## Bottom Line

**PASS_WITH_RISKS.** All three drift-risk items from the ledger review are handled with runner-level integration tests (not isolated unit checks). The retained A3/A4/A6 implementation is coherent with the design, fully tested, and cleanly separated from pre-existing dirty work. The ledger is accurate. No blockers for the retained atoms. A1/A2/A5 remain correctly blocked and must revisit their designs before re-implementation.
  · 60071 tok · in 57717 (57600 cached / 117 new) · out 2354 (835 reasoning) · ¥0.0060
