[2m  ▎ thinking[0m
  · 17046 tok · in 16868 (12032 cached / 4836 new) · out 178 (38 reasoning) · ¥0.0054
  -> read_file {"path": "docs/evidence/read-only-prefetch-proposal/implementation/scoped.diff"}
  -> read_file {"path": "docs/decisioning-north-star.md"}
  -> read_file {"path": "docs/proposal-read-only-named-path-evidence-prefetch.md"}
[2m  ▎ thinking[0m
Let me look at key implementation details that the diff references but doesn't show in full.
  · 34323 tok · in 34006 (12032 cached / 21974 new) · out 317 (45 reasoning) · ¥0.0228 · cache prefix changed: tools
  -> task {"prompt": "In the repository at /workspace/idiom, I need to understand the read-only named path prefet...
  -> grep {"pattern": "maybeApplyReadOnlyNamedPathPrefetch", "path": "/workspace/idiom"}
  -> grep {"pattern": "ToolNamePrefetch", "path": "/workspace/idiom"}
  -> grep {"pattern": "readOnlyPrefetchEventRole", "path": "/workspace/idiom"}
  -> grep {"pattern": "DirectAnswerSurfaceCapSourceBodyPrefetch", "path": "/workspace/idiom"}
  -> glob {"pattern": "**/internal/runtime/agent/*prefetch*", "path": "/workspace/idiom"}
  -> read_file {"path": "/workspace/idiom/internal/runtime/agent/read_only_prefetch.go"}
  -> read_file {"path": "/workspace/idiom/internal/capabilities/workspace/read_prefetch.go"}
  -> grep {"path": "/workspace/idiom/internal/runtime/agent", "pattern": "type RunEvent struct"}
  -> read_file {"path": "/workspace/idiom/internal/runtime/agent/direct_answer_surface.go"}
  -> read_file {"path": "/workspace/idiom/internal/runtime/agent/runner_state.go", "offset": 45, "limit": 15}
[2m  ▎ thinking[0m
  · 36659 tok · in 36504 (34304 cached / 2200 new) · out 155 (43 reasoning) · ¥0.0032
  -> read_file {"path": "internal/capabilities/workspace/read_prefetch.go"}
  -> grep {"pattern": "func MentionedWorkspacePaths", "path": "internal/contracts/pathparse"}
[2m  ▎ thinking[0m
  · 39122 tok · in 38930 (36608 cached / 2322 new) · out 192 (34 reasoning) · ¥0.0034
  -> read_file {"path": "internal/contracts/pathparse/pathparse.go", "offset": 312, "limit": 80}
  -> read_file {"path": "internal/runtime/agent/direct_answer_surface.go", "offset": 90, "limit": 30}
[2m  ▎ thinking[0m
Let me check a key detail about the call site — is `maybeApplyReadOnlyNamedPathPrefetch` called once or per-turn?
  · 40611 tok · in 40331 (39040 cached / 1291 new) · out 280 (110 reasoning) · ¥0.0026
  -> grep {"pattern": "maybeApplyReadOnlyNamedPathPrefetch", "path": "internal/runtime/agent/runner.go"}
  -> read_file {"offset": 545, "limit": 40, "path": "internal/runtime/agent/runner.go"}
[2m  ▎ thinking[0m
  · 41345 tok · in 41223 (40576 cached / 647 new) · out 122 (51 reasoning) · ¥0.0017
  -> grep {"pattern": "Prefetch|prefetch", "path": "internal/runtime/agent/runner.go"}
[2m  ▎ thinking[0m
Now let me look at a few more verification points — the `Source` field usage in answerability and the plan-execute guard changes.
  · 41788 tok · in 41441 (41344 cached / 97 new) · out 347 (156 reasoning) · ¥0.0016
  -> read_file {"offset": 95, "limit": 50, "path": "internal/runtime/agent/read_only_answerability.go"}
  -> read_file {"offset": 2685, "limit": 80, "path": "internal/runtime/agent/runner_plan_then_execute_guard.go"}
[2m  ▎ thinking[0m
  · 43787 tok · in 43622 (41728 cached / 1894 new) · out 165 (73 reasoning) · ¥0.0031
  -> read_file {"limit": 40, "offset": 1750, "path": "internal/runtime/agent/runner_plan_then_execute_guard.go"}
[2m  ▎ thinking[0m
  · 44898 tok · in 44360 (43776 cached / 584 new) · out 538 (468 reasoning) · ¥0.0025
  -> grep {"pattern": "func readOnlyDirectAnswerLane", "path": "internal/runtime/agent"}
[2m  ▎ thinking[0m
  · 45150 tok · in 44956 (44800 cached / 156 new) · out 194 (37 reasoning) · ¥0.0014
  -> read_file {"limit": 20, "offset": 138, "path": "internal/runtime/agent/final_answer_acceptance.go"}
  -> read_file {"limit": 30, "offset": 13227, "path": "internal/runtime/agent/runner.go"}
[2m  ▎ thinking[0m
Now I have all the evidence I need. Let me compile the review.

---

## North-Star / Architecture Implementation Review

### Verified Facts

1. **Diff scope**: `scoped.diff` touches 7 files — `direct_answer_surface.go`, `direct_answer_surface_test.go`, `read_only_answerability.go`, `read_only_answerability_test.go`, `runner.go`, `runner_plan_then_execute_guard.go`, plus one new file not shown in skeleton form (`read_only_prefetch.go`) and one new workspace file (`read_prefetch.go`).

2. **Prefetch eligibility gate**: The runner calls `maybeApplyReadOnlyNamedPathPrefetch` **exactly once, before the main loop** (runner.go line 561). It checks in order: workspace availability → env kill switch → no existing evidence → `readOnlyDirectAnswerLane()` → surface version match → surface advertises `source_body_prefetch`. All must pass before any file is read.

3. **Path extraction**: Uses `pathparse.MentionedWorkspacePaths` — a deterministic token parser that strips backticks, quotes, line suffixes (`:N`, `#L<N>-L<M>`), and `./` prefixes. Only explicit workspace-relative or absolute-under-workspace paths are eligible. Globs, package names, symbols, and log/session IDs are **not** extractable.

4. **Safety policy**: `prefetchReadFile` in `read_prefetch.go` routes through `Manager.resolveReadTarget(requested)` — the **same** path resolution, protected-path, symlink, and outside-root checks as normal `read_file`. Additionally excludes: binary files (NUL byte check), log files, generated artifacts, directories, stat-failed, too-large files, and files exceeding per-file or aggregate byte caps (defaults: 6 files, 160KB total, 64KB/file).

5. **Evidence integration**: Prefetched results enter the normal `readOnlyAnswerabilityLedger` pipeline with `Source: "prefetch"` vs. `Source: "read_file"` (from `readOnlyEvidenceItemSource`). Dedup: same path via prefetch + later `read_file` produces one `SourceBodyPaths` entry but two distinct ledger `Items` with different sources. The `concreteSourceEvidenceResult` function (runner.go:13231) and `planThenExecuteToolResultSourceEvidence` (guard.go:1763) both recognize `ToolNamePrefetch` alongside `read_file`.

6. **Lane isolation**: `TestRunnerNamedPathPrefetchDoesNotRunForWriteBrief` verifies that a mutation brief (`TaskFamilyImplementation`, `PermissionGuardedWorkspaceWrite`) receives a `"not_read_only_direct_answer"` miss event and **zero** prefetch tool results. The `readOnlyDirectAnswerLane` function gates on `PermissionProfileIsReadOnlyWorkspace && readOnlyDirectAnswerTaskFamily && RuntimeContractIsDirectAnswer`.

7. **Telemetry/audit**: Every attempt (eligible or not) emits a `RunEvent{Role: "read_only_named_path_prefetch"}` with a typed `readOnlyPrefetchEvent` struct (schema version, eligibility, paths, skips, bytes, lines, truncations, estimated round trips saved).

8. **Surface activation**: `DirectAnswerSurfaceVersion` bumped from `"2026-06-11"` to `"2026-06-12"`. The long-dormant `DirectAnswerSurfaceCapSourceBodyPrefetch` is now advertised in `directAnswerSurfaceCapabilities()`. The old "must NOT advertise" test is replaced with "must advertise" + "prefetch event emitted" + "model-initiated read_file = 0" assertions.

---

### Findings

#### ✅ North-Star Aligned (core prefetch)

| Principle | Status | Evidence |
|-----------|--------|----------|
| Model owns strategy, harness owns bounded evidence transport | ✅ Fully aligned | Prefetch injects verbatim file excerpts, not summaries or rankings. Model decides whether evidence is sufficient and how to answer. No forced closeout. |
| No user-visible hard blockers in strategy lanes | ✅ Fully aligned | Prefetch is invisible to the user; normal tool calls remain available. No completion gate, no "answer now" trigger. |
| No prompt-magic contract triggers | ✅ Fully aligned | Activation uses typed route classification + surface version + capability bits — no magic phrases. |
| Typed authority contracts | ✅ Fully aligned | Prefetched evidence gets `source:"prefetch"`, distinct from model reads. Uses same `resolveReadTarget` authority. Typed event struct with schema version. |
| Capability menus, not language menus | ✅ Fully aligned | Prefetch is path-level, not language-specific. Routes through generic workspace manager. |
| Frontier harness simplicity | ✅ Fully aligned | One new runner file, one new workspace file, targeted edits. Bounded caps. No new prompt layers, no recovery nudges, no special-cased tool affordances. |
| Safety policy parity | ✅ Fully aligned | Same `resolveReadTarget` as normal reads. Stricter: also excludes logs, generated files, binaries, too-large files. |

#### ⚠️ Scope Creep — Bundled Changes Not Part of Prefetch Design

The diff bundles three logically separate changes beyond the prefetch feature:

**Finding 1: `modelErrorAfterPartialEditBlockerAnswer` reordering** (runner.go lines 738-741 moved before transport retry, lines 770-774 removed)

The partial-edit-blocker check is moved **above** the transport retry logic. This changes error-recovery priority for **all** lanes, not just read-only. In a read-only lane (no edits) the check is a no-op, so it's benign for prefetch — but it's a separate behavioral change with no rationale in the diff. If a partial-edit state somehow exists when a transport error occurs, the runner now blocks instead of retrying.

**Finding 2: `objectiveRequestsDiagnosticLogInspection` rewrite** (runner.go lines 20405-20441)

The entire function is replaced with a two-phase check: `objectiveRequestsSourceFeatureInspection` + `objectiveNamesConcreteDiagnosticLogArtifact`. This is the "diagnostic-log trigger narrowing" fix from the proposal's Rollout step 1, which the proposal explicitly says should be "landed separately" before the prefetch. While the fix is correct (it prevents source-inspection objectives like "read these files" from being misclassified as diagnostic-log investigation), it's mixed into this diff.

**Finding 3: Task-ledger `RequireEvidence` condition change** (runner.go line 273)

`strings.HasPrefix(r.agentName(), "child_write:")` is added to exclude child write agents from the evidence requirement. This is a separate invariant change about subagent evidence policy, not about prefetch. It's also a mild code smell — using `agentName()` prefix matching is an ad-hoc check that could break with agent naming changes.

#### ✅ Plan-Execute Guard: Prefetch as Source Evidence

The changes to `runner_plan_then_execute_guard.go` — recognizing `ToolNamePrefetch` in `planThenExecuteToolResultSourceEvidence` (line 1763) and the new `requiredPathsHaveSourceBodyEvidence` function — are **correct and well-scoped**. They prevent stale-evidence claims in source-satisfied closeout by requiring current-turn evidence.

However, `requiredPathsHaveSourceBodyEvidence` is gated by `len(missing) > 0`. If `missing` is empty (the plan-execute check found no missing evidence through its own earlier processing), the function is skipped entirely and falls through to `planThenExecuteSourceSatisfiedResultsCoverPaths`. This means the freshness/staleness check only applies when the standard path already failed — it's a secondary backstop, not the primary path. This is conservative and correct, but worth noting as a minor design choice: the stale-evidence check is a reinforcement, not a replacement.

---

### Required Fixes

**Fix 1 — Land diagnostic-log trigger narrowing separately** (Finding 2)

The `objectiveRequestsDiagnosticLogInspection` rewrite and its two new helper functions (`objectiveRequestsSourceFeatureInspection`, `objectiveNamesConcreteDiagnosticLogArtifact`) should be split into a separate diff, landed before the prefetch diff. This is what the proposal's Rollout step 1 prescribes, and keeping them bundled makes the prefetch diff larger than necessary with no functional dependency.

Severity: **Should fix** (the code is correct, but the bundling violates the rollout plan and makes review harder).

**Fix 2 — Split `modelErrorAfterPartialEditBlockerAnswer` reorder** (Finding 1)

The reordering of the partial-edit-blocker check before transport retry should either (a) be removed from this diff and evaluated separately, or (b) be explained with a comment about why it's safe/necessary. If the intent is to prevent retrying after partial edits in all lanes, that's a separate behavioral change that needs its own rationale and tests.

Severity: **Should fix** (moderate — the reorder is likely harmless but bundled without rationale).

**Fix 3 — Split or explain task-ledger `RequireEvidence` prefix check** (Finding 3)

The `!strings.HasPrefix(r.agentName(), "child_write:")` change should either be removed (if the evidence requirement is correct for child writes) or moved to its own focused diff with an explanation of why child_write agents need relaxed evidence requirements.

Severity: **Should fix** (low — the change is small but ad-hoc; prefix-matching on agent names is brittle).

---

### North-Star Risk Assessment

**Low.** The core prefetch implementation is a textbook example of north-star alignment:

- The model retains full strategic control (answer, probe, redirect — all still available).
- The harness adds a bounded, permission-checked transport optimization.
- The evidence pipeline uses typed contracts (`schema_version`, capability bits, source attribution).
- No new prompt surfaces, no magic phrases, no degraded tool affordances.
- Safety is upheld or slightly tightened (log/generated-file exclusion).
- Telemetry/audit is structured from day one.

The bundled extra changes increase review surface and violate the proposal's own rollout plan, but they don't undermine the prefetch design or the north-star contract. Even if all three extra changes were removed, the prefetch feature would be complete and self-consistent.

The one architectural concern about mixing concerns is that the `requiredPathsHaveSourceBodyEvidence` function in the plan-execute guard — while correct — is a secondary backstop rather than the primary staleness check. This is a minor design choice, not a defect. The prefetch evidence path is fully orthogonal to the plan-execute lane (prefetch never fires in plan-execute), so the plan-execute guard changes are defensive hardening, not coupling.

---

### Final Verdict

**Approve-with-changes**

**Reasoning**: The core prefetch implementation is clean, well-scoped, and faithfully implements the proposal's design while adhering to the decisioning north star. The model/harness boundary is respected; safety is maintained; evidence accounting is correct; lane isolation is verified by tests.

The approval is conditional on splitting out the three bundled changes (diagnostic-log trigger narrowing, `modelErrorAfterPartialEditBlockerAnswer` reorder, task-ledger prefix check) into separate diffs or providing documented rationale for why they belong together. These are scope-creep issues, not design flaws — the prefetch itself is ready.
  · 49873 tok · in 45820 (45056 cached / 764 new) · out 4053 (1601 reasoning) · ¥0.0098
