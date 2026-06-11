[2m  ▎ thinking[0m
  · 16911 tok · in 16819 (16640 cached / 179 new) · out 92 (29 reasoning) · ¥0.0007
  -> read_file {"path": "docs/evidence/read-only-prefetch-proposal/prefetch-model-facing-excerpt-intent.md"}
[2m  ▎ thinking[0m
Let me gather context around the existing prefetch implementation to ground this review in actual code.
  · 18750 tok · in 17790 (17152 cached / 638 new) · out 960 (814 reasoning) · ¥0.0029 · cache prefix changed: tools
  -> explore {"task": "In the idiom codebase at /Users/ben/Documents/GitHub/idiom, find all code related to \"prefetch\" — especially...
  -> mcp__codegraph__context {"projectPath": "/Users/ben/Documents/GitHub/idiom", "task": "prefetch read_only_named_path_prefetch prefetch adapter tr...
  -> grep {"pattern": "prefetch", "path": "/Users/ben/Documents/GitHub/idiom"}
  -> grep {"pattern": "TruncatedFiles", "path": "/Users/ben/Documents/GitHub/idiom"}
  -> grep {"pattern": "truncat", "path": "/Users/ben/Documents/GitHub/idiom"}
  -> read_file {"path": "/Users/ben/Documents/GitHub/idiom/internal/runtime/agent/read_only_prefetch.go"}
  -> read_file {"path": "/Users/ben/Documents/GitHub/idiom/internal/capabilities/workspace/read_prefetch.go"}
  -> grep {"path": "/Users/ben/Documents/GitHub/idiom", "pattern": "maybeApplyReadOnlyNamedPathPrefetch"}
  -> grep {"path": "/Users/ben/Documents/GitHub/idiom", "pattern": "ToolNamePrefetch"}
  -> grep {"path": "/Users/ben/Documents/GitHub/idiom", "pattern": "DirectAnswerSurfacePrefetchEligibility"}
  -> grep {"path": "/Users/ben/Documents/GitHub/idiom", "pattern": "FormatToolResultForModel"}
  -> read_file {"offset": 410, "path": "/Users/ben/Documents/GitHub/idiom/internal/runtime/agent/runner.go", "limit": 30}
  -> read_file {"offset": 2200, "path": "/Users/ben/Documents/GitHub/idiom/internal/capabilities/workspace/manager.go", "limit": 120}
  -> read_file {"path": "/Users/ben/Documents/GitHub/idiom/internal/runtime/agent/direct_answer_surface.go", "limit": 160}
  -> read_file {"offset": 270, "path": "/Users/ben/Documents/GitHub/idiom/internal/runtime/agent/read_only_answerability.go", "limit": ...
  -> read_file {"path": "/Users/ben/Documents/GitHub/idiom/internal/runtime/agent/read_only_prefetch_test.go"}
  -> read_file {"offset": 330, "path": "/Users/ben/Documents/GitHub/idiom/internal/runtime/agent/tools.go", "limit": 50}
  -> read_file {"path": "/Users/ben/Documents/GitHub/idiom/internal/capabilities/workspace/read_prefetch_test.go"}
  -> read_file {"offset": 13070, "path": "/Users/ben/Documents/GitHub/idiom/internal/runtime/agent/runner.go", "limit": 30}
  -> read_file {"limit": 30, "offset": 1745, "path": "/Users/ben/Documents/GitHub/idiom/internal/runtime/agent/runner_plan_then_execute...
  -> grep {"path": "/Users/ben/Documents/GitHub/idiom/internal/runtime/agent", "pattern": "requiredPathsHaveSourceBodyEvidence"}
  -> grep {"path": "/Users/ben/Documents/GitHub/idiom/internal/runtime/agent", "pattern": "func readOnlyDirectAnswerLane"}
  -> read_file {"offset": 1540, "path": "/Users/ben/Documents/GitHub/idiom/internal/runtime/agent/read_only_answerability_test.go", "li...
  -> read_file {"limit": 20, "offset": 130, "path": "/Users/ben/Documents/GitHub/idiom/internal/runtime/agent/final_answer_acceptance.g...
[2m  ▎ thinking[0m
Good, I have the landscape. Let me read the core files to understand exact current behavior before evaluating drift.
  · 21296 tok · in 21111 (18688 cached / 2423 new) · out 185 (27 reasoning) · ¥0.0032
  -> read_file {"path": "internal/capabilities/workspace/read_prefetch.go"}
  -> read_file {"path": "internal/capabilities/workspace/read_prefetch_test.go"}
  -> read_file {"path": "internal/runtime/agent/read_only_prefetch.go"}
[2m  ▎ thinking[0m
  · 26315 tok · in 26157 (21248 cached / 4909 new) · out 158 (31 reasoning) · ¥0.0056
  -> read_file {"path": "internal/runtime/agent/read_only_prefetch_test.go"}
  -> read_file {"path": "internal/capabilities/workspace/manager.go", "offset": 2200, "limit": 30}
[2m  ▎ thinking[0m
  · 28446 tok · in 28196 (26240 cached / 1956 new) · out 250 (96 reasoning) · ¥0.0030
  -> grep {"pattern": "defaultLogLineCharCap"}
  -> read_file {"limit": 30, "offset": 2228, "path": "internal/capabilities/workspace/manager.go"}
  -> grep {"pattern": "splitWorkspaceFileLines"}
[2m  ▎ thinking[0m
  · 31592 tok · in 29653 (28416 cached / 1237 new) · out 1939 (1823 reasoning) · ¥0.0057
  -> read_file {"path": "internal/runtime/agent/tools.go", "offset": 330, "limit": 40}
  -> grep {"pattern": "TruncatedFiles"}
[2m  ▎ thinking[0m
I see there are existing reviews in `reviews/`. Let me check those to avoid duplicating analysis.
  · 32981 tok · in 32847 (31488 cached / 1359 new) · out 134 (40 reasoning) · ¥0.0023
  -> ls {"recursive": true, "path": "docs/evidence/read-only-prefetch-proposal/reviews"}
[2m  ▎ thinking[0m
  · 33645 tok · in 33389 (32896 cached / 493 new) · out 256 (29 reasoning) · ¥0.0017
  -> read_file {"path": "docs/evidence/read-only-prefetch-proposal/reviews/prefetch-excerpt-intent/drift-risk.md"}
  -> read_file {"path": "docs/evidence/read-only-prefetch-proposal/reviews/prefetch-excerpt-intent/harness-correctness.md"}
  -> read_file {"path": "docs/evidence/read-only-prefetch-proposal/reviews/prefetch-excerpt-intent/benchmark-readiness.md"}
  -> read_file {"path": "docs/evidence/read-only-prefetch-proposal/reviews/prefetch-excerpt-intent/north-star.md"}
[2m  ▎ thinking[0m
Let me check one more thing — whether plan-execute's planner runs under a read-only workspace profile, which could be an unaddressed interaction:
  · 42975 tok · in 41679 (33536 cached / 8143 new) · out 1296 (1155 reasoning) · ¥0.0114
  -> grep {"pattern": "PermissionProfileIsReadOnlyWorkspace"}
  -> grep {"pattern": "PermissionReadOnlyWorkspace|PermissionGuardedWorkspaceWrite", "path": "internal/runtime/workerruntime"}
[2m  ▎ thinking[0m
  · 46121 tok · in 46020 (42880 cached / 3140 new) · out 101 (25 reasoning) · ¥0.0042
  -> grep {"pattern": "plan-execute|plan_then_execute|PlanExecute", "path": "internal/runtime/workerruntime"}
[2m  ▎ thinking[0m
  · 48712 tok · in 48613 (46080 cached / 2533 new) · out 99 (15 reasoning) · ¥0.0037
  -> read_file {"limit": 30, "offset": 190, "path": "internal/runtime/workerruntime/catalog.go"}
[2m  ▎ thinking[0m
Now I have a full picture. Here is my independent drift-risk analysis.

---

# Prefetch Model-Facing Excerpt Intent — Drift Risk Review

**Document reviewed:** `docs/evidence/read-only-prefetch-proposal/prefetch-model-facing-excerpt-intent.md`
**Ground truth checked against:** `read_prefetch.go`, `read_only_prefetch.go`, `manager.go` (FormatToolResultForModel), `read_prefetch_test.go`, `read_only_prefetch_test.go`

## Existing review coverage (not re-audited)

The `/reviews/` directory already contains thorough analyses. I confirm that these existing findings are accurate and I do not repeat them here:

| Finding | Source | Severity |
|---|---|---|
| `too_large` skip makes truncation code dead | drift-risk.md #1, north-star #1 | Severe |
| Missing model-facing `read_file` follow-up note | drift-risk.md #2, north-star #2 | Medium |
| Summary missing original line/byte counts | drift-risk.md #3, north-star #4 | Low |
| Relationship between two limits underspecified | north-star #3 | Medium |
| Benchmark measurement requirements | benchmark-readiness.md | Binding |

---

## New findings beyond existing reviews

### Finding A — Telemetry `TruncatedFiles` is already wired but always 0; the proposal treats it as new

The intent doc says "report truncation in the existing ... telemetry via `TruncatedFiles`" (line 24) as if adding a field. But `TruncatedFiles` already exists in both `PrefetchReadReport` (`read_prefetch.go:34`) and `readOnlyPrefetchEvent` (`read_only_prefetch.go:25`), and it's already wired end-to-end from the workspace layer to the runner event (`read_prefetch.go:81`, `read_only_prefetch.go:52`).

**The drift:** Because of the `too_large` skip (finding #1 in existing reviews), `TruncatedFiles` is **always 0** today — the byte-truncation code at line 152-158 is dead. After the fix, it will finally report non-zero values. The proposal does not acknowledge this existing dead field, which creates ambiguity:
- Is `TruncatedFiles` the *existing* field that will *start working* after the change?
- Or is the proposal describing a *new* telemetry field?

**Risk:** If a reviewer or implementor doesn't connect the dead-field + dead-code observation, they might add a *second* truncation counter or rename the field, breaking log-analysis tools that already parse `truncated_files` (even though it's currently always 0).

**Recommendation:** State explicitly in the proposal that `TruncatedFiles` is an existing telemetry field that will become live once the `too_large` skip is removed (or replaced with excerpt truncation). Do not add a second counter.

---

### Finding B — The model-facing note is a prompt artifact, not just metadata

The proposal says "append an explicit model-facing note" (line 22). The existing reviews call this out as a missing feature but don't flag its lifecycle implications.

**The drift:** This note is *prompt text* — a new instruction injected into the model's context on every truncated prefetch. It has all the properties of a prompt artifact:
- It biases model behavior (trains the model to reach for `read_file` after every truncated prefetch)
- It may need tuning per lane (direct-answer vs. architecture: different follow-up tolerance)
- It should be versioned and diff-able
- It becomes part of the model's behavioral baseline — if evals don't account for it, they measure model + note, not model alone

**Hidden behavior change risk:** Over many turns, repeated exposure to "bounded `read_file` follow-up is available" could shift the model toward tool-use over reasoning — increasing round trips on subsequent turns even when the evidence is sufficient. This is the opposite of what prefetch is trying to achieve.

**Recommendation:** Treat the note as a prompt artifact — version it, file in `prompts/` or alongside prefetch code, and add an eval check that a truncated prefetch followed by sufficient evidence does NOT trigger a gratuitous `read_file` call. Without that regression guard, the note is unmeasured behavioral drift.

---

### Finding C — Summary format change breaks downstream consumers

The proposal adds "original line/byte counts" to the summary and appends the follow-up note. The current summary format:

```
Read f1 README.md lines 1-50 (total lines: 50; source: prefetch) (truncated)
```

After the change, something like:
```
Read f1 README.md lines 1-50 (excerpt: 50 lines, original: 412 lines, original: 28672 bytes; source: prefetch) (truncated).
Note: prefetch returned a partial excerpt. Use read_file with a specific line range to request the full content.
```

**This is a breaking change** for anything that pattern-matches summary text:
- Telemetry log queries that filter by summary content
- Eval fixtures that assert exact summary strings
- Any post-processing or dashboard that extracts fields from the summary

**Risk:** No existing test asserts the summary format. The current test (`read_prefetch_test.go:45`) only checks that the summary *contains* `"Read f1 README.md lines 1-1"` — a substring match. The change will pass this test trivially. A new format will have no regression guard.

**Recommendation:** Add an explicit test for the *full* summary format (both truncated and non-truncated cases) as part of implementation, using exact string match. This locks the format and signals to downstream consumers that it's part of the contract.

---

### Finding D — No test validates the follow-up `read_file` path (core safety guarantee)

The proposal's safety argument (line 39-44) is:
1. Truncation applies only to opportunistic prefetch
2. The model retains normal read tools
3. The model can ask for exact missing ranges

Point 3 is the entire safety net — but **there is zero test coverage** that a truncated prefetch + follow-up `read_file` actually works end-to-end. No unit test, no integration test, no runner-level test exercises:
- Resolve a truncated path → verify preview is excerpt → call `read_file` with a range → get the full content back
- The two results share the same `displayPath` / content that merges without gaps
- The second read doesn't re-trigger prefetch (existing evidence guard at `read_only_prefetch.go:83`)

**Risk:** If the safety net has a bug (e.g., `MarkPathObserved` interferes with the follow-up read, or the line-numbering scheme diverges between prefetch and `read_file`), the safety claim is invalid and the proposal knows it only when a user hits it.

**Recommendation:** Add a workspace-layer integration test: prefetch a file that exceeds the excerpt limit → verify truncation evidence → call `read_file` on the same path → verify full content is returned and line numbers are contiguous. This is the cheapest test that validates the safety argument.

---

### Finding E — Single `TruncatedFiles` counter loses signal when two truncation regimes overlap

If the implementation keeps the existing byte-level truncation AND adds a line-level excerpt cap, both can set `TruncatedFiles++`. But `TruncatedFiles` is a single integer — you can't tell which limit was binding.

**Risk:** In telemetry analysis, a file truncated by the byte cap looks identical to one truncated by the line cap. If you're tuning the excerpt limit, you can't distinguish "excerpt too aggressive" from "file too large for byte budget." This loses diagnostic signal.

**Recommendation:** Either:
- (a) Replace byte truncation with line cap (one regime), or
- (b) Add separate counters: `LineCappedFiles` and `ByteCappedFiles` (or `ExcerptCappedFiles` if byte truncation is removed)

The proposal should specify which.

---

### Finding F — The "new model-facing prefetch limit" layer is unspecified

The proposal says "capped by a new model-facing prefetch limit" (line 17) but does not say where this limit lives:

| Layer | Pros | Cons |
|---|---|---|
| `prefetchReadFile` (workspace) | Co-located with existing truncation; testable at workspace level | Applies uniformly, can't vary by lane |
| `readOnlyPrefetchPaths` (runner event) | Lane-aware, can differ per contract | Harder to test; mixes gate logic with formatting |
| `FormatToolResultForModel` (manager.go) | Purely model-facing, no effect on internal contracts | Too late — telemetry and answerability already saw the full output |

**Risk:** If an implementor puts it at the wrong layer, either:
- Lane-specific excerpt tuning is impossible without refactoring
- Or telemetry/answerability pipelines see different data than the model

**Recommendation:** State the intended layer in the proposal. My suggestion: `prefetchReadFile` (workspace layer), since all other safeguards live there and it's the most testable. Lane variation can be added via `PrefetchReadOptions` later.

---

## Verdict

**Conditional APPROVE** — the core idea (smaller model-facing excerpt for prefetch only, `read_file` unchanged) is sound and well-scoped. But I would treat these as binding pre-implementation clarifications:

| # | Finding | Action required | Owner |
|---|---|---|---|
| A | `TruncatedFiles` is existing dead telemetry | Acknowledge it becomes live; don't rename or duplicate | Proposal author |
| B | Model-facing note is a prompt artifact | Add versioning + eval baseline against gratuitous follow-ups | Implementation |
| C | Summary format change is breaking | Add exact-format regression test | Implementation |
| D | Safety argument untested | Add workspace-layer follow-up `read_file` integration test | Implementation |
| E | Ambiguous truncation regime interaction | Choose: single regime (replace byte truncation with line cap) or add separate counters | Proposal author |
| F | Excerpt limit layer unspecified | State `prefetchReadFile` (or other) explicitly | Proposal author |

The existing reviews' findings (#1 `too_large` skip, #2 missing note, #3 missing original counts) remain preconditions. The six items above are additional preconditions that should be resolved before or alongside implementation, not deferred.
  · 51758 tok · in 49394 (48640 cached / 754 new) · out 2364 (102 reasoning) · ¥0.0065
