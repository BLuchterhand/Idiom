[2m  ▎ thinking[0m
  · 17324 tok · in 17056 (16640 cached / 416 new) · out 268 (50 reasoning) · ¥0.0013
  -> read_file {"path": "internal/capabilities/workspace/read_prefetch.go"}
  -> read_file {"path": "internal/capabilities/workspace/read_prefetch_test.go"}
  -> read_file {"path": "internal/runtime/agent/read_only_prefetch.go"}
  -> read_file {"path": "internal/runtime/agent/direct_answer_surface.go"}
  -> read_file {"path": "internal/runtime/agent/direct_answer_surface_test.go"}
[2m  ▎ thinking[0m
  · 31712 tok · in 31527 (17152 cached / 14375 new) · out 185 (8 reasoning) · ¥0.0151 · cache prefix changed: tools
  -> read_file {"path": "internal/runtime/agent/read_only_answerability.go"}
  -> read_file {"path": "internal/runtime/agent/read_only_answerability_test.go"}
  -> read_file {"path": "internal/runtime/agent/runner.go"}
  -> read_file {"path": "internal/runtime/agent/runner_plan_then_execute_guard.go"}
  · tool output truncated: 15834 of 48602 bytes elided
  · tool output truncated: 52243 of 85011 bytes elided
  · tool output truncated: 89431 of 122199 bytes elided
  · tool output truncated: 59903 of 92671 bytes elided
[2m  ▎ thinking[0m
Let me find the key integration points — where prefetch is called in runner.go and how the plan-then-execute guard interacts with it.
  · 71139 tok · in 70820 (31616 cached / 39204 new) · out 319 (91 reasoning) · ¥0.0405
  -> grep {"pattern": "maybeApplyReadOnlyNamedPathPrefetch", "path": "internal/runtime/agent/runner.go"}
  -> grep {"pattern": "PrefetchReadFiles|ToolNamePrefetch|prefetch", "path": "internal/runtime/agent/runner.go"}
  -> grep {"pattern": "prefetch", "path": "internal/runtime/agent/runner_plan_then_execute_guard.go"}
[2m  ▎ thinking[0m
  · 71445 tok · in 71251 (71040 cached / 211 new) · out 194 (41 reasoning) · ¥0.0020
  -> read_file {"offset": 530, "path": "internal/runtime/agent/runner.go", "limit": 80}
  -> read_file {"offset": 13220, "path": "internal/runtime/agent/runner.go", "limit": 30}
[2m  ▎ thinking[0m
Let me check how the runnerTurnRecorder's collectedResults pointer works, and examine the prefetch result flow more carefully.
  · 73823 tok · in 73124 (71424 cached / 1700 new) · out 699 (546 reasoning) · ¥0.0045
  -> grep {"path": "internal/runtime/agent", "pattern": "type runnerTurnRecorder struct"}
  -> grep {"path": "internal/runtime/agent", "pattern": "func.*runnerTurnRecorder.*recordWithOptions\""}
[2m  ▎ thinking[0m
  · 73978 tok · in 73876 (73728 cached / 148 new) · out 102 (17 reasoning) · ¥0.0018
  -> read_file {"limit": 120, "offset": 0, "path": "internal/runtime/agent/runner_recording.go"}
[2m  ▎ thinking[0m
  · 77704 tok · in 75541 (73856 cached / 1685 new) · out 2163 (2037 reasoning) · ¥0.0075
  -> grep {"path": "internal/runtime/agent/runner.go", "pattern": "func.*agentName"}
  -> grep {"path": "internal/runtime/agent/runner.go", "pattern": "r\\.AgentName"}
[2m  ▎ thinking[0m
  · 78417 tok · in 78240 (77696 cached / 544 new) · out 177 (35 reasoning) · ¥0.0025
  -> read_file {"offset": 19730, "path": "internal/runtime/agent/runner.go", "limit": 15}
  -> grep {"path": "internal/runtime/agent/runner.go", "pattern": "AgentName.*=.*child"}
[2m  ▎ thinking[0m
  · 78849 tok · in 78637 (78336 cached / 301 new) · out 212 (88 reasoning) · ¥0.0023
  -> grep {"path": "internal/runtime/agent", "pattern": "AgentName.*=.*\\\"child"}
  -> grep {"path": "internal/runtime", "pattern": "AgentName.*=.*\\\"child"}
[2m  ▎ thinking[0m
  · 78955 tok · in 78873 (78848 cached / 25 new) · out 82 (16 reasoning) · ¥0.0018
  -> grep {"path": "internal", "pattern": "AgentName:\\s*\\\"child"}
[2m  ▎ thinking[0m
Let me verify key integration points — whether "prefetch" is listed in tool manifests and how the recorder's message injection flows into the model conversation.
  · 80965 tok · in 80177 (78848 cached / 1329 new) · out 788 (633 reasoning) · ¥0.0045
  -> grep {"path": "internal", "pattern": "\\\"prefetch\\\""}
  -> grep {"path": "internal/runtime/agent/runner_recording.go", "pattern": "modelFacingToolResult|toolResultMessage"}
[2m  ▎ thinking[0m
  · 81485 tok · in 81283 (80896 cached / 387 new) · out 202 (117 reasoning) · ¥0.0024
  -> read_file {"limit": 30, "offset": 111, "path": "internal/runtime/agent/runner_recording.go"}
[2m  ▎ thinking[0m
  · 82000 tok · in 81851 (81408 cached / 443 new) · out 149 (77 reasoning) · ¥0.0024
  -> grep {"path": "internal/runtime/agent/runner_recording.go", "pattern": "func toolResultMessage"}
  · 82081 tok · in 82014 (81920 cached / 94 new) · out 67 · ¥0.0019
  -> grep {"path": "internal/runtime/agent", "pattern": "func toolResultMessage"}
  · 82221 tok · in 82138 (81920 cached / 218 new) · out 83 · ¥0.0020
  -> read_file {"limit": 30, "offset": 339, "path": "internal/runtime/agent/tools.go"}
[2m  ▎ thinking[0m
  · 82787 tok · in 82610 (82048 cached / 562 new) · out 177 (106 reasoning) · ¥0.0026
  -> grep {"path": "internal/capabilities/workspace", "pattern": "func FormatToolResultForModel"}
[2m  ▎ thinking[0m
  · 82944 tok · in 82836 (82688 cached / 148 new) · out 108 (22 reasoning) · ¥0.0020
  -> read_file {"limit": 30, "offset": 2206, "path": "internal/capabilities/workspace/manager.go"}
[2m  ▎ thinking[0m
Let me check how `sourceBodyEvidencePaths` handles prefetch results and verify the complete evidence pipeline.
  · 83939 tok · in 83311 (82944 cached / 367 new) · out 628 (537 reasoning) · ¥0.0033
  -> grep {"path": "internal/runtime/agent/runner.go", "pattern": "func sourceBodyEvidencePaths"}
[2m  ▎ thinking[0m
  · 84088 tok · in 83990 (83840 cached / 150 new) · out 98 (30 reasoning) · ¥0.0020
  -> grep {"path": "internal/runtime/agent", "pattern": "func sourceEvidenceBodyPaths"}
  · 84227 tok · in 84141 (83968 cached / 173 new) · out 86 · ¥0.0020
  -> read_file {"limit": 40, "offset": 70, "path": "internal/runtime/agent/source_evidence_state.go"}
[2m  ▎ thinking[0m
  · 84768 tok · in 84653 (84096 cached / 557 new) · out 115 (45 reasoning) · ¥0.0025
  -> grep {"path": "internal/runtime/agent", "pattern": "func sourceEvidenceBodyReadForPath"}
  · 84911 tok · in 84825 (84736 cached / 89 new) · out 86 · ¥0.0020
  -> read_file {"limit": 30, "offset": 66, "path": "internal/runtime/agent/source_evidence_state.go"}
[2m  ▎ thinking[0m
  · 85284 tok · in 85192 (84736 cached / 456 new) · out 92 (18 reasoning) · ¥0.0023
  -> grep {"path": "internal/runtime/agent/source_evidence_state.go", "pattern": "func sourceEvidenceForPath"}
  · 85428 tok · in 85343 (85248 cached / 95 new) · out 85 · ¥0.0020
  -> read_file {"limit": 30, "offset": 37, "path": "internal/runtime/agent/source_evidence_state.go"}
[2m  ▎ thinking[0m
Let me quickly verify the path extraction function prefetch relies on.
  · 86026 tok · in 85794 (85248 cached / 546 new) · out 232 (149 reasoning) · ¥0.0027
  -> grep {"path": "internal/contracts/pathparse", "pattern": "func MentionedWorkspacePaths"}
[2m  ▎ thinking[0m
  · 86323 tok · in 86205 (86016 cached / 189 new) · out 118 (48 reasoning) · ¥0.0021
  -> grep {"path": "internal/runtime/agent", "pattern": "func readOnlyDirectAnswerLane"}
  · 86468 tok · in 86381 (86272 cached / 109 new) · out 87 · ¥0.0020
  -> read_file {"limit": 25, "offset": 138, "path": "internal/runtime/agent/final_answer_acceptance.go"}
[2m  ▎ thinking[0m
Let me verify that path extraction from objectives works correctly for the test cases.
  · 87112 tok · in 86816 (86272 cached / 544 new) · out 296 (196 reasoning) · ¥0.0029
  -> read_file {"limit": 30, "offset": 312, "path": "internal/contracts/pathparse/pathparse.go"}
[2m  ▎ thinking[0m
我现在对实现有了透彻的理解。让我也检查一下 `conversationEvidenceSatisfied` 是否正确地包含了预取结果。
  · 89023 tok · in 87425 (87040 cached / 385 new) · out 1598 (1502 reasoning) · ¥0.0053
  -> grep {"path": "internal/runtime/agent", "pattern": "func conversationEvidenceSatisfied"}
[2m  ▎ thinking[0m
  · 89352 tok · in 89244 (88960 cached / 284 new) · out 108 (20 reasoning) · ¥0.0023
  -> read_file {"limit": 25, "offset": 1042, "path": "internal/runtime/agent/runner_lane_policy.go"}
[2m  ▎ thinking[0m
## APPROVE

No harness correctness issues found. The implementation is sound across all reviewed files. Here's the detailed assessment:

### Architecture: Correct

- **Prefetch injection point** (`runner.go:561`): Called exactly once, before the model turn loop begins. The recorder's `collectedResults` pointer is correctly wired — `recordWithOptions` appends to `*recorder.collectedResults`, and the return value is reassigned to the runner's local variable, keeping the pointer valid for future appends.

- **Model-facing format** (`tools.go:358-361`): Prefetch results have no active `ToolCall`, so `toolResultMessage` correctly emits a `system`-role message with `FormatToolResultForModel`. The model sees `tool: prefetch` with the same line-numbered source format as `read_file`.

- **Prefetch is not a model-facing tool**: `"prefetch"` appears only in the `ToolNamePrefetch` constant and test assertions — never in a tool manifest. The model can't call it; it only consumes injected prefetch results.

### Eligibility Guards (`read_only_prefetch.go:69-93`): Correct

| Guard | Line | Justification |
|-------|------|---------------|
| `r == nil / r.Workspace == nil` | 71 | Defensive, matches other runner paths |
| `IDIOM_DISABLE_READONLY_PREFETCH` env var | 75 | Escape hatch |
| `child:` / `child_write:` agent name | 79 | Explicit per AGENTS.md requirement. Children are named in `runner_child_tasks.go:75,407` |
| Existing evidence present | 83 | Conservative — skips prefetch when `SeedResults` are non-empty |
| Not read-only workspace | 87 | Expands from direct-answer-only to all read-only profiles (architecture, conversation, etc.) |

The **lane expansion** is the intended change: `readOnlyNamedPathPrefetchLane` checks `PermissionProfileIsReadOnlyWorkspace` (broader), while the old direct-answer surface code used `readOnlyDirectAnswerLane` (narrower). The test `TestRunnerNamedPathPrefetchRunsForReadOnlyArchitectureBrief` (`direct_answer_surface_test.go:334`) confirms this works for architecture lanes.

### Evidence Integration: Correct

- **`concreteSourceEvidenceResult`** (`runner.go:13231`): `ToolNamePrefetch` is handled alongside `read_file`, returning true when the result has concrete source body content.

- **`readOnlyEvidenceItemSource`** (`read_only_answerability.go:287-291`): Returns `"prefetch"` as the source label, keeping prefetch items distinct from model-driven `read_file` items in the evidence ledger.

- **Deduplication** (`readOnlyPrefetchEvidenceDedupsWithModelReadAndKeepsSource` test at `direct_answer_surface_test.go:1554`): Correctly verifies that prefetch + read_file on the same path produce deduped `SourceBodyPaths` but separate evidence items with distinct `Source` attribution.

- **`planThenExecuteToolResultSourceEvidence`** (`runner_plan_then_execute_guard.go:1763`): Treats `ToolNamePrefetch` identically to `read_file`, allowing plan-then-execute planner phases to recognize prefetched paths as valid source grounding.

### Path Extraction (`read_only_prefetch.go:105-111`): Correct

`readOnlyPrefetchObjectivePaths` delegates to `pathparse.MentionedWorkspacePaths(brief.Objective)`, which is the same path-parsing function used elsewhere in answerability. The test objectives (`"Read README.md and answer what Idiom is."`, `"Read README.md and explain how cache metrics flow."`) correctly extract `README.md`.

### Test Coverage: Adequate

The four prefetch-specific tests exercise:
- Direct-answer prefetch with grounded answer (line 177)
- Write-brief exclusion with metadata event assertion (line 265)
- Architecture-brief prefetch (line 334) — the expansion target
- Deduplication with model-driven reads (line 1554)

### No Issues Found

| Concern | Verdict |
|---------|---------|
| Prefetch running when `SeedResults` exist | Conservative skip — correctness-preserving, even if it misses some optimization |
| Prefetch wasting context on irrelevant named paths | Bounded by `MaxFiles=6`, `MaxBytes=160KB` — the model can ignore irrelevant entries |
| Recorder pointer invalidation after `collectedResults` reassignment | The pointer `&collectedResults` was taken before reassignment; the address is stable, the value changes — `*recorder.collectedResults` continues to work |
| `"prefetch"` leaking into tool manifests | Confirmed absent from all manifests |
| Prefetch results in answerability closure | Handled via the same `concreteSourceEvidenceResult` → `observeSourceBodyObjectiveSupport` path as read_file |

All specified tests pass, and `go build ./cmd/idiom` compiles cleanly.
  · 90858 tok · in 89692 (89344 cached / 348 new) · out 1166 (72 reasoning) · ¥0.0045
