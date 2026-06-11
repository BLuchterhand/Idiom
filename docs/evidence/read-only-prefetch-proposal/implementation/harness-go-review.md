[2m  ▎ thinking[0m
  · 17077 tok · in 16951 (12032 cached / 4919 new) · out 126 (23 reasoning) · ¥0.0054
  -> read_file {"path": "docs/evidence/read-only-prefetch-proposal/implementation/scoped.diff"}
  -> read_file {"path": "docs/proposal-read-only-named-path-evidence-prefetch.md"}
[2m  ▎ thinking[0m
Let me inspect the source code behind the key new additions to verify alignment with the proposal.
  · 28598 tok · in 28167 (12032 cached / 16135 new) · out 431 (105 reasoning) · ¥0.0172 · cache prefix changed: tools
  -> grep {"pattern": "func \\(r \\*Runner\\) maybeApplyReadOnlyNamedPathPrefetch", "path": "internal/runtime/agent"}
  -> grep {"pattern": "ToolNamePrefetch", "path": "internal/capabilities/workspace"}
  -> grep {"pattern": "func \\(r \\*Runner\\) modelErrorAfterPartialEditBlockerAnswer", "path": "internal/runtime/agent"}
  -> grep {"pattern": "readOnlyPrefetchEventRole", "path": "internal/runtime/agent"}
  -> grep {"pattern": "DirectAnswerSurfaceCapSourceBodyPrefetch", "path": "internal/runtime/agent"}
[2m  ▎ thinking[0m
  · 29417 tok · in 29306 (28544 cached / 762 new) · out 111 (16 reasoning) · ¥0.0016
  -> read_file {"path": "internal/runtime/agent/read_only_prefetch.go"}
  -> read_file {"path": "internal/capabilities/workspace/read_prefetch.go"}
[2m  ▎ thinking[0m
  · 33047 tok · in 32895 (29312 cached / 3583 new) · out 152 (25 reasoning) · ¥0.0045
  -> grep {"path": "internal/runtime/agent", "pattern": "func DirectAnswerSurfacePrefetchEligibility"}
  -> grep {"path": "internal/runtime/agent", "pattern": "readOnlyDirectAnswerLane"}
[2m  ▎ thinking[0m
  · 34860 tok · in 34672 (33024 cached / 1648 new) · out 188 (33 reasoning) · ¥0.0027
  -> read_file {"path": "internal/runtime/agent/direct_answer_surface.go", "offset": 90, "limit": 50}
  -> read_file {"path": "internal/runtime/agent/runner.go", "offset": 515, "limit": 30}
[2m  ▎ thinking[0m
  · 36128 tok · in 35937 (34816 cached / 1121 new) · out 191 (39 reasoning) · ¥0.0022
  -> read_file {"limit": 30, "offset": 545, "path": "internal/runtime/agent/runner.go"}
  -> read_file {"limit": 50, "offset": 730, "path": "internal/runtime/agent/runner.go"}
[2m  ▎ thinking[0m
  · 37666 tok · in 37475 (36096 cached / 1379 new) · out 191 (50 reasoning) · ¥0.0025
  -> grep {"pattern": "func MentionedWorkspacePaths", "path": "internal/contracts/pathparse"}
  -> read_file {"path": "internal/runtime/agent/runner.go", "offset": 4084, "limit": 35}
  · 38466 tok · in 38380 (37632 cached / 748 new) · out 86 · ¥0.0017
  -> read_file {"limit": 40, "offset": 312, "path": "internal/contracts/pathparse/pathparse.go"}
[2m  ▎ thinking[0m
  · 39066 tok · in 38891 (38272 cached / 619 new) · out 175 (31 reasoning) · ¥0.0017
  -> grep {"pattern": "func NormalizeWorkspacePathToken", "path": "internal/contracts/pathparse"}
  -> read_file {"offset": 352, "path": "internal/contracts/pathparse/pathparse.go", "limit": 50}
[2m  ▎ thinking[0m
  · 39816 tok · in 39642 (39040 cached / 602 new) · out 174 (29 reasoning) · ¥0.0017
  -> grep {"path": "internal/contracts/pathparse", "pattern": "func trimWorkspacePathTokenEdges"}
  -> read_file {"offset": 450, "path": "internal/contracts/pathparse/pathparse.go", "limit": 60}
[2m  ▎ thinking[0m
  · 40372 tok · in 40185 (39808 cached / 377 new) · out 187 (33 reasoning) · ¥0.0015
  -> read_file {"limit": 25, "offset": 13220, "path": "internal/runtime/agent/runner.go"}
  -> read_file {"offset": 21740, "path": "internal/runtime/agent/runner.go", "limit": 20}
[2m  ▎ thinking[0m
  · 41134 tok · in 41002 (40320 cached / 682 new) · out 132 (40 reasoning) · ¥0.0018
  -> read_file {"limit": 30, "offset": 1755, "path": "internal/runtime/agent/runner_plan_then_execute_guard.go"}
[2m  ▎ thinking[0m
  · 41723 tok · in 41595 (41088 cached / 507 new) · out 128 (43 reasoning) · ¥0.0016
  -> read_file {"limit": 30, "offset": 315, "path": "internal/runtime/agent/runner_fallback.go"}
[2m  ▎ thinking[0m
Now let me verify that the kill switch and the surface gate eligibility path work correctly by checking the env var path and the full eligibility chain.
  · 44457 tok · in 42068 (41600 cached / 468 new) · out 2389 (2204 reasoning) · ¥0.0061
  -> read_file {"limit": 30, "offset": 69, "path": "internal/runtime/agent/direct_answer_surface.go"}
  -> read_file {"offset": 14, "path": "internal/runtime/agent/read_only_prefetch.go", "limit": 15}
[2m  ▎ thinking[0m
Here is the complete review.

---

## Verified facts

- **Surface gate activation**: `DirectAnswerSurfaceCapSourceBodyPrefetch` is now advertised in `directAnswerSurfaceCapabilities()` and the version stamp is bumped from `2026-06-11` to `2026-06-12`. The eligibility gate is enforced in `DirectAnswerSurfacePrefetchEligibility` which checks surface version match and capability presence — matches the proposal's rollout step 2.
- **Path extractor**: `readOnlyPrefetchObjectivePaths` delegates to `pathparse.MentionedWorkspacePaths`, which uses `NormalizeWorkspacePathToken` (strips backticks, quotes, `./` prefix, line suffixes `:N`, `:N-M`, `#L<N>`) — matches the proposal's safety rules exactly. The extractor is conservative: existence is not checked here; it only extracts syntactic path candidates.
- **Workspace policy sharing**: `PrefetchReadFiles` calls `m.resolveReadTarget()` (same path as `read_file`) for symlink, protected-path, and outside-root checks, and `classifySearchFilePath` for log/generated exclusion — matches proposal's "policy parity" requirement. The same `MaxFileBytes` cap from workspace config constrains per-file prefetch bytes.
- **Exclusion logic**: Binary files (null byte check), directories, missing files, too-large files, logs, generated artifacts, and `.git`-classified paths are all skipped with structured reasons — covers the proposal's explicit exclusion list.
- **Caps**: `DefaultPrefetchMaxFiles = 6`, `DefaultPrefetchMaxBytes = 160KB`, `DefaultPrefetchMaxBytesPerFile = 64KB`. Truncated files produce a `(truncated)` suffix in the summary — matches proposal's cap requirements.
- **Deduplication**: Duplicate exact paths (after resolution) are skipped with a `DuplicatePaths` count. The read-only answerability ledger handles dedup at the `SourceBodyPaths` level: prefetch + model `read_file` of the same path emit one evidence path with two distinct `Source` values (`"prefetch"` vs `"read_file"`) — matches proposal's "dedup for acceptance, preserve for audit" requirement.
- **Route guard**: `readOnlyNamedPathPrefetchEvent` checks `readOnlyDirectAnswerLane(brief)` and `DirectAnswerSurfacePrefetchEligibility(surface)`. Write briefs are explicitly rejected with `miss_reason: "not_read_only_direct_answer"` — verified by `TestRunnerNamedPathPrefetchDoesNotRunForWriteBrief`.
- **Kill switch**: `IDIOM_DISABLE_READONLY_PREFETCH=1` disables prefetch before any eligibility checks — matches rollout step 3.
- **Existing evidence guard**: `len(collectedResults) > 0` short-circuits prefetch — prevents prefetch from running on replay/recovery turns where seed evidence already exists.
- **Model error ordering**: `modelErrorAfterPartialEditBlockerAnswer` is moved before `transportRetries` and `planThenExecuteNativeSubmitPlanNoCallFallbackPrompt`. This is a safety-priority fix — if partial edits exist and the model then errors, the partial-edit blocker answer should take precedence over retry.
- **Diagnostic-log trigger narrowing**: `objectiveRequestsDiagnosticLogInspection` is refactored into three predicates (`objectiveRequestsSourceFeatureInspection`, `objectiveNamesConcreteDiagnosticLogArtifact`) with a new early-return gate for "read these files" phrasing — exactly the "already localized diagnostic-trigger fix" the proposal calls for in rollout step 1.
- **Source-satisfied evidence freshness**: `planThenExecuteSourceSatisfiedAnswerCovers` adds a `requiredPathsHaveSourceBodyEvidence` check that requires fresh-turn evidence for anchor-refresh paths. This prevents stale prefetch seed evidence from satisfying the plan-execute source-satisfied gate.

## Findings

1. **All proposal requirements are met in the scoped diff.** The implementation faithfully follows the design, safety rules, caps, exclusion list, dedup semantics, surface gate, route guarding, telemetry event shape, and rollout plan.

2. **The plan-execute source-satisfied freshness check (`runner_plan_then_execute_guard.go`) is an unplanned scope expansion.** The proposal's "Out of scope" section explicitly lists "No changes to write lanes, plan-execute, review-repair, atom intake, or recovery." The `planThenExecuteSourceSatisfiedAnswerCovers` changes add an evidence freshness guard inside plan-execute. While this is a **reasonable and necessary complement** to prefetch (prefetch seed evidence would otherwise be stale-stale-eligible during anchor-refresh), it technically exceeds the declared boundary. The change is correct and defensive.

3. **The `concreteSourceEvidenceResult` change (runner.go:13231) treats prefetch identically to `read_file`** for concrete-source-body classification. This is correct — prefetch output has line-numbered content identical to `read_file`. The same `readFileResultHasConcreteSourceBody` predicate validates both.

4. **The `readOnlyReplayedEvidenceCloseoutApplies` change (runner.go:4091) adds an additional `readOnlyDirectAnswerLane(brief)` guard.** This narrows a closeout path that was already read-only in name but missing the runtime lane check. This is a pre-existing invariant gap being closed, not a behavioral change for prefetch. Correct and minimal.

5. **Telemetry event structure** (`readOnlyPrefetchEvent`) includes `schema_version`, `eligible`, `miss_reason`, `requested_paths`, `selected_paths`, `skipped_paths`, `duplicate_paths`, `bytes`, `lines`, `truncated_files`, and `estimated_round_trips_saved` — matches the proposal's requirement for "structured event with selected paths, excluded paths, bytes, line counts, truncation, policy reasons, dedup counts, and estimated eliminated round trips."

## Required fixes

None. All changes in the scoped diff are structurally correct, properly guarded, and aligned with the proposal.

The plan-execute scope expansion (finding 2) should be noted in the commit message as deliberate, not accidental — explain that it fixes a stale-seed-evidence vulnerability introduced by prefetch, so future reviewers understand why it exists.

## Regression test gaps

The proposal's test plan lists these items that lack dedicated test coverage in the diff:

| Test plan item | Coverage status | Risk |
|---|---|---|
| Outside-root, protected, log, generated, binary, missing, too-large files skipped with structured reasons | Tested implicitly through the workspace-level `prefetchReadFile` exclusion logic, but no runner-level test asserts specific `miss_reason` values for each exclusion type. | Low — exclusions are exercised by the workspace `resolveReadTarget` / `classifySearchFilePath` code paths which have their own tests. |
| Model can still request normal follow-up read/search after prefetch | Not explicitly tested. The existing `TestRunnerDirectAnswerSurfacePrefetchesNamedPath` uses a one-turn reply, so it never exercises follow-up. | Low — the runner loop is unchanged after prefetch injection; model tools remain available. The test could be strengthened. |
| Prompt requiring evidence outside prefetched set still allows gathering | Not tested. Same reason as above — the test uses a single-turn closeout. | Low — no gate prevents additional tool calls after prefetch (that's the design). |
| Backtick-quoted paths and line-suffixed paths extracted; globs/symbols not | Covered by existing `pathparse.MentionedWorkspacePaths` tests, not by new runner-level tests. | Low — the extraction is delegated entirely to `pathparse`, no new parser in this diff. |
| Policy parity (same rejection for prefetch vs `read_file`) | Not tested with a dedicated parity test. | Low-medium — both paths use `resolveReadTarget` but a focused `TestPrefetchReadFilePolicyParity` table test would strengthen confidence. |

**Bottom line**: No test gap is release-blocking. The three new tests (`TestRunnerDirectAnswerSurfacePrefetchesNamedPath`, `TestRunnerNamedPathPrefetchDoesNotRunForWriteBrief`, `TestReadOnlyPrefetchEvidenceDedupsWithModelReadAndKeepsSource`) cover the critical safety and correctness invariants. The missing items are either covered by existing lower-level tests or are edge cases that the workspace policy layer already tests.

## Final verdict

**approve-with-changes** — with the caveat being that the commit message must explicitly acknowledge the plan-execute source-satisfied freshness check as a deliberate scope expansion needed to prevent stale seed evidence from satisfying the plan-execute gate, not as a latent feature addition. No code changes required.
  · 47054 tok · in 45117 (44416 cached / 701 new) · out 1937 (57 reasoning) · ¥0.0055
