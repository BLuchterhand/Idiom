[2m  ▎ thinking[0m
  · 17134 tok · in 16875 (16640 cached / 235 new) · out 259 (118 reasoning) · ¥0.0011
  -> mcp__codegraph__context {"task": "read_only_prefetch.go gate named-path prefetch workspace policy"}
  -> read_file {"path": "internal/runtime/agent/read_only_prefetch.go"}
  -> read_file {"path": "internal/capabilities/workspace/read_prefetch.go"}
[2m  ▎ thinking[0m
  · 22833 tok · in 22522 (17152 cached / 5370 new) · out 311 (35 reasoning) · ¥0.0063 · cache prefix changed: tools
  -> read_file {"path": "internal/runtime/agent/read_only_prefetch_test.go"}
  -> read_file {"path": "internal/capabilities/workspace/read_prefetch_test.go"}
  -> task {"prompt": "Find all files in the codebase that reference \"architecture_assessment\" as a lane/permission/wire name. Al...
  -> grep {"pattern": "architecture_assessment", "path": "/workspace/idiom"}
  -> grep {"pattern": "read_only_workspace", "path": "/workspace/idiom"}
  -> grep {"pattern": "PermissionProfileIsReadOnlyWorkspace", "path": "/workspace/idiom"}
  -> grep {"pattern": "MentionedWorkspacePaths", "path": "/workspace/idiom"}
  -> grep {"pattern": "direct-answer-only|direct_answer_only", "path": "/workspace/idiom"}
  -> read_file {"path": "/workspace/idiom/internal/runtime/workerruntime/vocabulary.go", "offset": 65, "limit": 30}
  -> read_file {"path": "/workspace/idiom/internal/runtime/workerruntime/vocabulary.go", "offset": 340, "limit": 50}
  -> read_file {"path": "/workspace/idiom/internal/contracts/pathparse/pathparse.go", "offset": 300, "limit": 80}
  -> grep {"path": "/workspace/idiom", "pattern": "direct-answer-only|direct_answer_only", "path": "/home/example/Do...
  -> read_file {"limit": 30, "offset": 23285, "path": "/workspace/idiom/internal/runtime/agent/runner_test.go"}
  -> read_file {"limit": 15, "offset": 1425, "path": "/workspace/idiom/internal/surfaces/tui/app/prompt_routing.go"}
  -> read_file {"limit": 15, "offset": 455, "path": "/workspace/idiom/internal/surfaces/tui/app/turn_planner.go"}
  -> read_file {"limit": 20, "offset": 40, "path": "/workspace/idiom/internal/runtime/agent/lane_policy_test.go"}
  -> read_file {"limit": 15, "offset": 222, "path": "/workspace/idiom/internal/runtime/agent/lane_policy.go"}
  -> read_file {"limit": 15, "offset": 40, "path": "/workspace/idiom/internal/surfaces/tui/app/model_swap.go"}
  -> grep {"path": "/workspace/idiom", "pattern": "direct.answer.only"}
  -> grep {"path": "/workspace/idiom", "pattern": "directAnswerOnly"}
  -> read_file {"limit": 30, "offset": 78, "path": "/workspace/idiom/internal/runtime/agent/read_only_prefetch.go"}
  -> read_file {"limit": 25, "offset": 130, "path": "/workspace/idiom/internal/runtime/agent/final_answer_acceptance.g...
  -> read_file {"limit": 30, "offset": 770, "path": "/workspace/idiom/internal/runtime/agent/read_only_closure.go"}
  ⊘ read_file read /workspace/idiom/internal/runtime/agent/read_only_prefetch_test.go: open /workspace/idiom/internal/runtime/agent/read_only_prefetch_test.go: no such file or directory
[2m  ▎ thinking[0m
  · 27495 tok · in 26886 (22784 cached / 4102 new) · out 609 (395 reasoning) · ¥0.0058
  -> grep {"pattern": "maybeApplyReadOnlyNamedPathPrefetch", "path": "/workspace/idiom"}
  -> grep {"pattern": "readOnlyNamedPathPrefetchEvent", "path": "/workspace/idiom"}
  -> read_file {"path": "internal/runtime/agent/read_only_answerability.go", "limit": 50, "offset": 1220}
[2m  ▎ thinking[0m
  · 29043 tok · in 28869 (27392 cached / 1477 new) · out 174 (27 reasoning) · ¥0.0024
  -> read_file {"path": "/workspace/idiom/internal/runtime/agent/runner.go", "offset": 545, "limit": 40}
  -> read_file {"path": "/workspace/idiom/internal/runtime/agent/read_only_prefetch.go"}
[2m  ▎ thinking[0m
  · 31284 tok · in 31072 (28928 cached / 2144 new) · out 212 (57 reasoning) · ¥0.0031
  -> glob {"pattern": "internal/runtime/agent/*prefetch*"}
  -> glob {"pattern": "internal/runtime/agent/*prefetch*test*"}
  -> grep {"path": "/workspace/idiom/internal/runtime/agent", "pattern": "NamedPathPrefetch"}
[2m  ▎ thinking[0m
  · 31972 tok · in 31806 (31232 cached / 574 new) · out 166 (14 reasoning) · ¥0.0015
  -> read_file {"path": "/workspace/idiom/internal/runtime/agent/direct_answer_surface_test.go", "offset": 250, "limit...
  -> glob {"pattern": "docs/evidence/read-only-prefetch-proposal/reviews/final-architecture-expansion/benchmark-readiness.md"}
[2m  ▎ thinking[0m
  · 33826 tok · in 33645 (31872 cached / 1773 new) · out 181 (16 reasoning) · ¥0.0028
  -> read_file {"offset": 380, "path": "/workspace/idiom/internal/runtime/agent/direct_answer_surface_test.go", "limit...
  -> read_file {"path": "/workspace/idiom/docs/evidence/read-only-prefetch-proposal/reviews/final-architecture-expansi...
[2m  ▎ thinking[0m
  · 36713 tok · in 36421 (33792 cached / 2629 new) · out 292 (67 reasoning) · ¥0.0039
  -> grep {"path": "/workspace/idiom/internal", "pattern": "DirectAnswerSurfaceCapSourceBodyPrefetch"}
  -> grep {"path": "/workspace/idiom/internal", "pattern": "SourceBodyPrefetch"}
  -> read_file {"offset": 130, "path": "/workspace/idiom/internal/runtime/agent/final_answer_acceptance.go", "limit": ...
[2m  ▎ thinking[0m
  · 38781 tok · in 37675 (36608 cached / 1067 new) · out 1106 (940 reasoning) · ¥0.0040
  -> read_file {"limit": 50, "path": "/workspace/idiom/internal/runtime/agent/direct_answer_surface.go"}
  -> read_file {"limit": 20, "offset": 75, "path": "/workspace/idiom/internal/runtime/agent/direct_answer_surface.go"}
[2m  ▎ thinking[0m
  · 39765 tok · in 39651 (38656 cached / 995 new) · out 114 (16 reasoning) · ¥0.0020
  -> read_file {"limit": 50, "offset": 95, "path": "/workspace/idiom/internal/runtime/agent/direct_answer_surface.go"}
[2m  ▎ thinking[0m
  · 41977 tok · in 40371 (39680 cached / 691 new) · out 1606 (1425 reasoning) · ¥0.0047
  -> read_file {"limit": 40, "path": "/workspace/idiom/internal/runtime/agent/direct_answer_surface.go", "offset": 145...
  -> read_file {"limit": 30, "path": "/workspace/idiom/internal/runtime/workerruntime/vocabulary.go", "offset": 65}
[2m  ▎ thinking[0m
  · 43310 tok · in 43038 (41856 cached / 1182 new) · out 272 (26 reasoning) · ¥0.0026
  -> grep {"path": "/workspace/idiom/internal/runtime/workerruntime/vocabulary.go", "pattern": "func RuntimeContr...
  -> grep {"path": "/workspace/idiom/internal/runtime/workerruntime/vocabulary.go", "pattern": "func TaskFamilyIs...
  -> read_file {"limit": 30, "offset": 345, "path": "/workspace/idiom/internal/runtime/workerruntime/vocabulary.go"}
[2m  ▎ thinking[0m
  · 43833 tok · in 43707 (43264 cached / 443 new) · out 126 (28 reasoning) · ¥0.0016
  -> read_file {"limit": 80, "path": "/workspace/idiom/internal/contracts/pathparse/pathparse.go", "offset": 290}
[2m  ▎ thinking[0m
  · 44826 tok · in 44633 (43776 cached / 857 new) · out 193 (25 reasoning) · ¥0.0021
  -> grep {"path": "/workspace/idiom/internal/contracts/pathparse", "pattern": "func NormalizeWorkspacePathToken"...
  -> read_file {"limit": 5, "offset": 370, "path": "/workspace/idiom/internal/contracts/pathparse/pathparse.go"}
  · 45053 tok · in 44955 (44800 cached / 155 new) · out 98 · ¥0.0012
  -> read_file {"limit": 50, "offset": 384, "path": "/workspace/idiom/internal/contracts/pathparse/pathparse.go"}
[2m  ▎ thinking[0m
  · 45766 tok · in 45660 (44928 cached / 732 new) · out 106 (18 reasoning) · ¥0.0018
  -> grep {"path": "/workspace/idiom/internal/contracts/pathparse/pathparse.go", "pattern": "func LooksLikeWorksp...
  · 45914 tok · in 45816 (45696 cached / 120 new) · out 98 · ¥0.0012
  -> read_file {"limit": 35, "offset": 11, "path": "/workspace/idiom/internal/contracts/pathparse/pathparse.go"}
[2m  ▎ thinking[0m
Now I have a thorough picture. Here's the analysis.

---

## Benchmark Readiness Review: Read-Only Named-Path Prefetch

**Scope verified:** `internal/runtime/agent/read_only_prefetch.go` and `internal/capabilities/workspace/read_prefetch.go` — both read in full. Gate, path extraction (`pathparse.MentionedWorkspacePaths`), surface definitions, and integration point in `runner.go:561` all inspected.

---

## APPROVE with 5 findings

### Finding 1 (⚠️ Medium): Gate widened, surface capability/version check removed

The current `readOnlyNamedPathPrefetchLane` (line 95–98) checks only `PermissionProfileIsReadOnlyWorkspace` — **it does not check task family, contract, or surface capability**.

The old code (from implementation documentation and the diff in `docs/evidence/.../scoped-after-architecture.diff:403`) required both `readOnlyDirectAnswerLane(brief)` and `DirectAnswerSurfacePrefetchEligibility(surface)`. The `DirectAnswerSurfacePrefetchEligibility` function still exists at `internal/runtime/agent/direct_answer_surface.go:96` and validates surface version + `source_body_prefetch` capability — but **it is never called** from the prefetch code path.

**Risk:** The prefetch now fires for *all* read-only workspace lanes (architecture_assessment, review_findings, dependency_scan, grounded_explanation, etc.), not just direct-answer ones. If the benchmark objective for a previous `ContractArchitecture` run lacked explicit file paths, behavior was identical (no-op miss). But any scenario that *does* name paths in the objective now gets prefetched evidence the model didn't ask for — a genuine behavioral change from prior runs.

**If the benchmark is a fresh rerun** (not a comparison), this is acceptable by design — but you should verify that the benchmark surface would have passed `DirectAnswerSurfacePrefetchEligibility` anyway, or confirm the removal is deliberate. The surface now does not guard prefetch at all.

### Finding 2 (⚠️ Medium): No dedicated agent-level test file

There is no `internal/runtime/agent/read_only_prefetch_test.go`. The two integration tests in `direct_answer_surface_test.go` cover:
- `TestRunnerNamedPathPrefetchDoesNotRunForWriteBrief` — write lane exclusion
- `TestRunnerNamedPathPrefetchRunsForReadOnlyArchitectureBrief` — basic architecture lane prefetch

**Not tested:**
- `child:` / `child_write:` agent exclusion (line 79–82)
- `IDIOM_DISABLE_READONLY_PREFETCH` env guard (line 75–78)
- `existing_evidence_present` guard (line 83–86)
- Empty/malformed Objective (no paths → graceful miss)
- Multiple prefetched files, truncation, dedup behavior at the agent level

These gaps mean a subtle regression in gate logic wouldn't be caught by existing tests. For a rerun against a specific model, this is a process risk, not a correctness risk for the current code — but if the benchmark surface or brief setup changes, the guard that catches the regression doesn't exist.

### Finding 3 (⚠️ Low-Medium): `existing_evidence_present` guard is silent from benchmark view

Line 83–86:
```go
if len(collectedResults) > 0 {
    event.MissReason = "existing_evidence_present"
    return event
}
```

The prefetch runs exactly once, before the main turn loop (runner.go:561), on an empty `collectedResults` — so the guard triggers only if the benchmark infrastructure or a previous phase injects results before the loop starts. This is fine for standard bench runs, but **if any benchmark scenario pre-loads evidence as part of its setup, prefetch silently becomes a no-op** with only an event to signal it.

**Recommendation:** Ensure the benchmark does not populate `collectedResults` before the prefetch call, or validate that the `read_only_named_path_prefetch` event shows `"eligible":true` for scenarios that should prefetch.

### Finding 4 (⚠️ Low): Path extraction from Objective is phrasing-dependent

`readOnlyPrefetchObjectivePaths` → `MentionedWorkspacePaths` → tokenizer + `LooksLikeWorkspacePathToken`. The parser:
- Rejects absolute paths (no leading `/`)
- Rejects paths without known file extensions (unless in the shortlist: `README.md`, `go.mod`, `go.sum`, `config.json`, `Makefile`)
- Rejects paths looking like `/separated/symbol` tokens (no dot-extension)
- Accepts relative paths like `docs/README.md`, `internal/runner.go`

If the benchmark objective says "explain the architecture" without naming files, **prefetch is a no-op** — graceful miss, no behavioral change. If it says "read docs/README.md and internal/runner.go", prefetch fires in both files (up to `MaxFiles=6`, `MaxBytes=160KB`, `MaxBytesPerFile=64KB`).

**Recommendation:** For a rerun, verify which benchmark objectives contain explicit file paths. If the majority do, you're testing the new prefetch behavior. If most don't, the benchmark largely exercises the same code path as before and the gate widening is irrelevant.

### Finding 5 (✅ Low): Synthetic "prefetch" tool name is invisible to the model

The prefetch results are recorded with `Tool: "prefetch"` (a synthetic tool name not in any allowed tools list). However, the results flow into `turnMessages` as tool-result content with summary + line-numbered output — the model sees line-numbered file text in its context, not a tool call it initiated. The tool name is a recorder label only. No risk.

---

## Summary

| # | Finding | Severity | Action |
|---|---------|----------|--------|
| 1 | Gate widened to all read-only lanes; surface version/capability check removed | ⚠️ Medium | Verify surface compatibility or add check back; confirm objective phrasing |
| 2 | No agent-level unit test file | ⚠️ Medium | Acceptable for fresh rerun; add coverage before future changes |
| 3 | `existing_evidence_present` guard may silently skip prefetch | ⚠️ Low-Medium | Validate `"eligible":true` in benchmark event |
| 4 | Path extraction depends on objective phrasing | ⚠️ Low | Check which benchmark items have explicit named paths |
| 5 | Synthetic "prefetch" tool name is harmless | ✅ Low | No action |

**Verdict:** APPROVE — the current code is correct for its stated design (all read-only workspace lanes, explicit named paths, no child agents, bounded workspace policy). A fresh benchmark rerun will produce valid results for the new behavior. The main caveat is finding 1: if the benchmark objectives are architecture-description prompts without explicit file paths, the prefetch is a no-op and the benchmark tests the same code path as before. If they contain explicit file paths, the model now receives prefetched source evidence — a genuine behavioral change that the benchmark should measure.
  · 49391 tok · in 46322 (45696 cached / 626 new) · out 3069 (1518 reasoning) · ¥0.0077
