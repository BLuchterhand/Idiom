[2m  ▎ thinking[0m
  · 17197 tok · in 16898 (16640 cached / 258 new) · out 299 (123 reasoning) · ¥0.0012
  -> read_file {"path": "internal/capabilities/workspace/read_prefetch.go"}
  -> read_file {"path": "internal/runtime/agent/read_only_prefetch.go"}
  -> read_file {"path": "internal/runtime/agent/direct_answer_surface.go"}
  -> read_file {"path": "internal/runtime/agent/direct_answer_surface_test.go"}
[2m  ▎ thinking[0m
Let me gather broader context to understand actor boundaries, the child-agent naming convention, and how this integrates with the runner lifecycle.
  · 30649 tok · in 30260 (17152 cached / 13108 new) · out 389 (118 reasoning) · ¥0.0142 · cache prefix changed: tools
  -> mcp__codegraph__context {"task": "read_only_prefetch.go actor boundaries: readOnlyNamedPathPrefetchChildAgent, agentName, child agent creation, ...
  -> grep {"pattern": "func.*agentName\\(\\)", "path": "internal/runtime/agent"}
  -> grep {"pattern": "child_write:|child:", "path": "internal/runtime"}
  -> grep {"pattern": "PermissionReadOnlyWorkspace", "path": "internal/runtime/workerruntime"}
[2m  ▎ thinking[0m
  · 36425 tok · in 36109 (30592 cached / 5517 new) · out 316 (49 reasoning) · ¥0.0068
  -> read_file {"path": "internal/runtime/agent/runner_child_tasks.go", "offset": 30, "limit": 100}
  -> read_file {"path": "internal/runtime/workerruntime/roles.go"}
  -> read_file {"path": "internal/runtime/workerruntime/vocabulary.go", "offset": 65, "limit": 30}
  -> read_file {"path": "internal/contracts/pathparse/pathparse.go", "offset": 310, "limit": 60}
[2m  ▎ thinking[0m
  · 42290 tok · in 41046 (36352 cached / 4694 new) · out 1244 (1044 reasoning) · ¥0.0079
  -> grep {"path": "internal/runtime/agent", "pattern": "func readOnlyDirectAnswerLane"}
  -> grep {"path": "internal/runtime/workerruntime", "pattern": "func PermissionProfileIsReadOnlyWorkspace"}
  -> read_file {"path": "internal/runtime/agent/runner_child_tasks.go", "limit": 50, "offset": 0}
[2m  ▎ thinking[0m
  · 43218 tok · in 42945 (42240 cached / 705 new) · out 273 (43 reasoning) · ¥0.0021
  -> read_file {"limit": 30, "offset": 135, "path": "internal/runtime/agent/final_answer_acceptance.go"}
  -> read_file {"limit": 10, "offset": 350, "path": "internal/runtime/workerruntime/vocabulary.go"}
  -> read_file {"limit": 40, "offset": 100, "path": "internal/runtime/agent/runner_child_tasks.go"}
[2m  ▎ thinking[0m
  · 44761 tok · in 44231 (43136 cached / 1095 new) · out 530 (397 reasoning) · ¥0.0030
  -> grep {"path": "internal/runtime/agent", "pattern": "maybeApplyReadOnlyNamedPathPrefetch"}
  -> grep {"path": "internal/runtime/agent", "pattern": "normalizeReadOnlyChildPrompt|augmentReadOnlyChildPrompt"}
[2m  ▎ thinking[0m
  · 46021 tok · in 45842 (44672 cached / 1170 new) · out 179 (24 reasoning) · ¥0.0024
  -> read_file {"limit": 40, "offset": 545, "path": "internal/runtime/agent/runner.go"}
  -> read_file {"limit": 30, "offset": 205, "path": "internal/runtime/agent/runner_child_tasks.go"}
[2m  ▎ thinking[0m
  · 47118 tok · in 46949 (45952 cached / 997 new) · out 169 (85 reasoning) · ¥0.0023
  -> read_file {"limit": 30, "offset": 21295, "path": "internal/runtime/agent/runner.go"}
[2m  ▎ thinking[0m
Now let me check how `recordWithOptions` interacts with the shared pointer to ensure the replacement at line 66 is correct:
  · 50352 tok · in 47777 (47104 cached / 673 new) · out 2575 (2482 reasoning) · ¥0.0068
  -> grep {"path": "internal/runtime/agent", "pattern": "func.*recordWithOptions"}
[2m  ▎ thinking[0m
  · 50612 tok · in 50424 (50304 cached / 120 new) · out 188 (34 reasoning) · ¥0.0015
  -> read_file {"limit": 60, "offset": 42, "path": "internal/runtime/agent/runner_recording.go"}
  -> read_file {"limit": 20, "offset": 895, "path": "internal/runtime/agent/runner.go"}
[2m  ▎ thinking[0m
Let me verify the child agent Run path to confirm prefetch would be reachable:
  · 52324 tok · in 51815 (50560 cached / 1255 new) · out 509 (349 reasoning) · ¥0.0033
  -> read_file {"limit": 30, "offset": 360, "path": "internal/runtime/agent/runner.go"}
  -> grep {"path": "internal/runtime/agent/runner.go", "pattern": "func \\(r \\*Runner\\) Run\\("}
  · 52868 tok · in 52785 (52224 cached / 561 new) · out 83 · ¥0.0018
  -> read_file {"limit": 100, "offset": 148, "path": "internal/runtime/agent/runner.go"}
[2m  ▎ thinking[0m
  · 54454 tok · in 54268 (52736 cached / 1532 new) · out 186 (100 reasoning) · ¥0.0030
  -> read_file {"limit": 30, "offset": 775, "path": "internal/runtime/agent/read_only_closure.go"}
[2m  ▎ thinking[0m
I now have a thorough picture. Let me also verify one final boundary — whether the `readOnlyPrefetchObjectivePaths` extraction could introduce a strategy leak by extracting namespace/symbol paths that aren't file paths:
  · 55703 tok · in 54870 (54400 cached / 470 new) · out 833 (705 reasoning) · ¥0.0032
  -> read_file {"limit": 40, "offset": 347, "path": "internal/contracts/pathparse/pathparse.go"}
[2m  ▎ thinking[0m
Good — `NormalizeWorkspacePathToken` is a conservative path-like check, not a semantic inference. No strategy leak. I have everything I need.

---

## Review: Read-Only Named-Path Prefetch — North-Star & Actor-Boundary Assessment

### Scope Reviewed

| File | Lines | Role |
|---|---|---|
| `internal/capabilities/workspace/read_prefetch.go` | 1–178 | Workspace-level prefetch engine: resolution, safety gates, size budgets, line-numbered output |
| `internal/runtime/agent/read_only_prefetch.go` | 1–111 | Runner-level eligibility gates, path extraction from brief, result injection into turn recorder |
| `internal/runtime/agent/direct_answer_surface.go` | 1–255 | Surface inventory declaring `source_body_prefetch` as a capability, version-gated eligibility check |
| `internal/runtime/agent/direct_answer_surface_test.go` | 1–501 | Tests covering surface filtering, prefetch eligibility, integration with runner lifecycle |

### Central Finding

**APPROVE** — no north-star drift or actor-boundary violation. The implementation is principled, well-bounded, and supported by evidence. One minor documentation gap is noted below.

---

### 1. Top-Level Read-Only Workspace Prefetch: APPROVE ✓

**North-star test:** *The model owns strategy; the harness may provide bounded evidence and enforce permissions/contracts.*

| Aspect | Assessment | Evidence |
|---|---|---|
| **Evidence vs. strategy** | Prefetch injects *tool results* (line-numbered file content), not instructions or strategy shaping. The model sees them as if it had called `read_file` and remains free to ignore, search elsewhere, or express uncertainty. | `read_prefetch.go:173-177` — result is a `ToolResult{Tool: "prefetch", Summary, Output}` appended to turn messages via `recordWithOptions` (`read_only_prefetch.go:60-65`) |
| **Bounded** | Hard limits: 6 files, 160KB total, 64KB per file. Logs/generated files excluded. Binary detection. Path resolution respects workspace root. | `read_prefetch.go:14-18`, `123-127`, `149-151` |
| **Permission-gated** | Only `PermissionReadOnlyWorkspace` lanes qualify. Write lanes are unconditionally excluded. | `read_only_prefetch.go:87-89` → `readOnlyNamedPathPrefetchLane` → `workerruntime.PermissionProfileIsReadOnlyWorkspace` |
| **No strategy inference** | Paths extracted syntactically from the brief objective text via `MentionedWorkspacePaths`, not via semantic inference about what the model *should* read. | `read_only_prefetch.go:105-110` → `pathparse.MentionedWorkspacePaths(pathparse.go:312)` |
| **Telemetry** | Structured `read_only_named_path_prefetch` event always emitted (even for misses), enabling monitoring of eligibility and effectiveness. | `read_only_prefetch.go:31-35` (deferred), `31-67` (populated) |
| **Surface capability declared** | `source_body_prefetch` is an advertised capability on the direct-answer surface, version-gated and typed. | `direct_answer_surface.go:35`, `129-133`; eligibility check at `96-107` |

**Verdict:** The harness provides bounded evidence at the right layer. Strategy (what to do with the evidence, whether to search more, how to answer) remains fully with the model.

---

### 2. Child-Agent Exclusion: APPROVE (with documentation gap) ✓

**Actor-boundary test:** *Delegated agents operate under a different contract and interaction pattern than top-level principals.*

The exclusion at `read_only_prefetch.go:100-103` checks:

```go
func readOnlyNamedPathPrefetchChildAgent(agentName string) bool {
    return strings.HasPrefix(agentName, "child:") ||
           strings.HasPrefix(agentName, "child_write:")
}
```

**Why this is correct:**

1. **Different contract.** Child read-only agents use `ContractGroundedEvidence`, not `ContractDirectAnswer`. The prefetch is architected for the direct-answer pattern where the model typically reads named files and answers in one turn. Child agents, by contrast, are expected to do *deliberate investigation* — the system prompt instructs them to "prefer structured symbol/body tools, discovered handles, or narrow line ranges over raw path-wide rereads" (`runner_child_tasks.go:97-103`).

2. **Prompt-level path surfacing already exists.** Both `normalizeReadOnlyChildPrompt` (`runner_child_tasks.go:234-235`) and `augmentReadOnlyChildPrompt` (`runner.go:21304-21306`) already extract named paths from the delegation prompt and add them as explicit guidance (e.g., "Start from these named workspace paths: ..."). Adding prefetch on top would duplicate evidence pathways without clear benefit.

3. **Tight budgets.** Child agents get 3 tool steps, 5 model calls (`runner_child_tasks.go:48`) — designed for focused investigation, not instant-answer from prefetched content. The cost of prefetch parsing and I/O (stat, read, line-number) isn't zero, and the benefit is marginal given the child's expected interaction pattern.

4. **Consistent with ~60+ child-agent boundaries.** The `child:` prefix is the stable actor-boundary convention used throughout the codebase — lane policy (`runner_lane_policy.go:1150`), closure readiness (`read_only_closure.go:780`), request budget (`runner_request_budget.go:596`), tool authorisation, and budget reset logic all distinguish child agents by the same prefix. Excluding prefetch at this same boundary is consistent, not ad-hoc.

5. **Telemetry is preserved.** The prefetch event fires for child agents too — with `eligible=false, miss_reason=delegated_child_agent` — so misses are observable and the exclusion is auditable.

**Documentation gap (minor):** The function `readOnlyNamedPathPrefetchChildAgent` at `read_only_prefetch.go:100` has no comment explaining *why* child agents are excluded. A future maintainer might reasonably ask "but child read-only agents have `PermissionReadOnlyWorkspace` — shouldn't the lane check handle this?" and could remove the guard. The ordering (child check comes *before* the lane check at line 87) makes this especially subtle.

**Suggested fix:** Add a doc comment:

```go
// readOnlyNamedPathPrefetchChildAgent reports whether the agent name identifies
// a delegated (child) agent. Child agents are excluded from named-path prefetch
// because they use ContractGroundedEvidence rather than ContractDirectAnswer,
// their prompt already surfaces named paths via normalizeReadOnlyChildPrompt,
// and their interaction pattern is deliberate investigation (not instant-answer
// from named files). The direct-answer prefetch is a top-level principal
// optimization; delegated actors follow a different evidence-acquisition path.
```

---

### 3. Additional Observations (no drift)

| Observation | File:Line | Status |
|---|---|---|
| Prefetch runs from the shared `Run()` method at line 561, before any model turn — correct lifecycle hook. | `runner.go:561` | ✓ |
| `maybeApplyReadOnlyNamedPathPrefetch` correctly returns `*recorder.collectedResults` (shared pointer) so the prefetch results are visible to the turn loop. | `read_only_prefetch.go:66` | ✓ |
| `recordWithOptions` with `ResetRejected: true` ensures prefetch content resets the no-progress counter — correct: prefetch is progress. | `read_only_prefetch.go:63-64` | ✓ |
| Surface version `"2026-06-12"` is future-dated — this is a version tag, not a typo, but could confuse during debugging if stale versions are compared. | `direct_answer_surface.go:25` | Informational |
| `readOnlyPrefetchObjectivePaths` scopes `ScopeToBriefBody` before extracting paths, so delimiter-structured briefs don't leak preamble/postamble tokens. | `read_only_prefetch.go:105-110`, `pathparse.go:295-318` | ✓ |

### Summary

**APPROVE** both the top-level read-only workspace prefetch and the child-agent exclusion boundary. The implementation is north-star aligned: the harness provides bounded, permission-gated evidence without encroaching on strategy. The child-agent exclusion is principled and consistent with the actor-boundary convention used across the entire codebase. One minor documentation gap: the rationale for excluding child agents is not captured in a comment at the exclusion site.
  · 58086 tok · in 56109 (55680 cached / 429 new) · out 1977 (96 reasoning) · ¥0.0055
