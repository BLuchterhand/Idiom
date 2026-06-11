[2m  ▎ thinking[0m
  · 17188 tok · in 16910 (13312 cached / 3598 new) · out 278 (32 reasoning) · ¥0.0044
  -> read_file {"path": "docs/evidence/read-only-prefetch-proposal/implementation/scope-notes.md"}
  -> read_file {"path": "docs/evidence/read-only-prefetch-proposal/implementation/harness-go-review.md"}
  -> read_file {"path": "docs/evidence/read-only-prefetch-proposal/implementation/north-star-review.md"}
  -> read_file {"path": "docs/evidence/read-only-prefetch-proposal/implementation/runtime-review-focused.md"}
  -> read_file {"path": "docs/proposal-read-only-named-path-evidence-prefetch.md"}
[2m  ▎ thinking[0m
  · 34744 tok · in 32791 (17152 cached / 15639 new) · out 1953 (1849 reasoning) · ¥0.0199 · cache prefix changed: tools
  -> todo_write {"todos": [{"content": "Read and cross-analyze all five review documents", "status": "completed"}, {"content": "Identify...
[2m  ▎ thinking[0m
## Final Drift Reconciliation: Read-Only Named-Path Evidence Prefetch

### Verified Facts

1. **Three independent reviews, one direction**: harness-go, north-star, and runtime reviews all reach **approve-with-changes** — unanimous that the implementation is structurally correct, safe, and faithful to the proposal's design.

2. **Core prefetch mechanism is sound**: The path extractor (`pathparse.MentionedWorkspacePaths`), the workspace policy adapter (`resolveReadTarget` parity), the runner injection point (before the first model turn, exactly once), evidence attribution (`source:"prefetch"` vs `"read_file"`), dedup logic, lane isolation (read-only direct-answer only), kill switch (`IDIOM_DISABLE_READONLY_PREFETCH=1`), caps (6 files / 160KB / 64KB per-file), and telemetry event shape all match the proposal. All three reviews confirm this.

3. **Tests cover critical invariants**: Inclusion (`TestRunnerDirectAnswerSurfacePrefetchesNamedPath`), write-lane exclusion (`TestRunnerNamedPathPrefetchDoesNotRunForWriteBrief`), dedup with source preservation (`TestReadOnlyPrefetchEvidenceDedupsWithModelReadAndKeepsSource`). Lower-level policy-parity tests exist in the workspace layer.

4. **Proposal Rollout step 4 (benchmark summary fields) is deferred**: Scope-notes.md explicitly acknowledges this is not part of the implementation pass. The raw telemetry event carries all the data — benchmark analysis will read from conversation logs until the follow-up.

5. **Dirty-tree scope notes are accurate**: The four non-prefetch hunks identified in scope-notes.md (diagnostic-log trigger narrowing, `modelErrorAfterPartialEditBlockerAnswer` reorder, child-write task-ledger evidence adjustment, plan-execute source-satisfied freshness guard) match what the north-star review independently flagged.

### Remaining Findings

#### Finding A — Scope boundary violation (all reviews, north-star most explicitly)

The diff bundles **four changes that the proposal explicitly excluded or assigned to separate rollout steps**:

| Bundled change | Proposal intent | Review agreement |
|---|---|---|
| `objectiveRequestsDiagnosticLogInspection` rewrite | Rollout step 1: land separately | All three note it; north-star says **should fix**; harness-go calls it "exactly the fix the proposal calls for" but doesn't flag the bundling as a problem |
| `modelErrorAfterPartialEditBlockerAnswer` reorder | Not in prefetch scope | Only north-star flags it; scope-notes lists as non-prefetch hunk |
| Child-write `RequireEvidence` prefix check | Not in prefetch scope | Only north-star flags it; scope-notes lists as non-prefetch hunk |
| Plan-execute source-satisfied freshness guard | Proposal: "No changes to ... plan-execute" | harness-go calls it "reasonable and necessary" but acknowledges scope violation; north-star doesn't mention it |

**Cross-review disagreement**: The harness-go review (focused on Go correctness) calls the plan-execute guard "reasonable" and says *no code changes are required* — just commit-message acknowledgment. The north-star review (focused on architecture and scope discipline) calls three of the four bundled changes **should-fix** splits. These aren't contradictory: the prefetch itself is correct regardless; the disagreement is about commit-boundary hygiene.

#### Finding B — `EstimatedRoundTripsSaved` formula is undocumented

Only the runtime review catches this. The formula `len(results) - 1` is a conservative lower bound with no inline comment explaining its derivation or assumptions. Minor, but relevant for future drift analysis.

#### Finding C — No cross-review contradiction that changes the outcome

No reviewer says the prefetch is unsafe, wrongly routed, incorrectly accounted, or missing a required gate. The disagreements are exclusively about *bundling discipline* and *documentation completeness*.

### Required Fixes Before Benchmark

**None.**

The implementation is correct and safe as-is. Benchmarking can proceed on this code without risk of misattribution or incorrect results. The bundled changes are orthogonal to prefetch behavior — they don't affect whether prefetch fires, what it reads, how it attributes evidence, or how the model sees the injected content.

### Required Fixes *Before Commit* (post-benchmark)

These must be resolved before the changeset lands, but they don't block benchmarking:

| # | Fix | Source | Severity |
|---|---|---|---|
| 1 | Split the diagnostic-log trigger narrowing (`objectiveRequestsDiagnosticLogInspection` rewrite) into its own diff landed first | north-star review + proposal Rollout step 1 | **Required** — direct violation of the rollout plan |
| 2 | Split or remove the `modelErrorAfterPartialEditBlockerAnswer` reorder, or add rationale | north-star review | **Should fix** — separate behavioral change with no prefetch dependency |
| 3 | Split or remove the child-write `RequireEvidence` prefix check, or add rationale | north-star review | **Should fix** — ad-hoc agent-name prefix matching is brittle |
| 4 | If the plan-execute freshness guard is kept bundled (harness-go says it's reasonable), the commit message must explicitly call it out as a deliberate scope expansion needed to prevent stale seed evidence, **not** as a hidden feature addition | harness-go review + Proposal out-of-scope section | **Required** — the proposal explicitly excludes plan-execute changes |
| 5 | Add inline comment explaining `EstimatedRoundTripsSaved = len(results) - 1` as a conservative lower bound | runtime review | **Recommended** — prevents misinterpretation by drift analysts |
| 6 | Document in commit body that benchmark summary field integration (proposal Rollout step 4) is deferred | runtime review + scope-notes.md | **Recommended** — scope clarity for future consumers |

### Final Verdict

**approve-with-changes**

**Rationale**: The core prefetch implementation is correct, safe, tested, and ready for benchmarking. All three independent reviews agree on this. The required changes are about commit-boundary discipline — the diff bundles four items the proposal said should be separate — not about feature correctness. Benchmarking can proceed immediately; the split/discipline work is required before the commit ships.

**What the "changes" require** (post-benchmark):
1. Split the diagnostic-log trigger narrowing into its own diff (Rollout step 1 compliance).
2. Split or justify the two other non-prefetch behavioral changes (`modelErrorAfterPartialEditBlockerAnswer` reorder, child-write prefix check).
3. Acknowledge the plan-execute scope expansion in the commit message.
4. Document the `EstimatedRoundTripsSaved` formula.
5. Note the deferred benchmark summary integration.

The implementation is cleared for benchmark. The proposal's drift checks (model call count, tool call count, prompt tokens, wall time, answer correctness, prefetch event telemetry) can all be evaluated against this code.
  · 36351 tok · in 34766 (32768 cached / 1998 new) · out 1585 (11 reasoning) · ¥0.0058
  -> todo_write {"todos": [{"content":"Read and cross-analyze all five review documents","status":"completed"},{"content":"Identify conf...
  ⊘ todo_write todo 3 "Write final drift reconciliation verdict" is newly completed but has no matching successful complete_step receipt in this turn
[2m  ▎ thinking[0m
  · 36612 tok · in 36390 (36224 cached / 166 new) · out 222 (48 reasoning) · ¥0.0013
  -> complete_step {"step": "Write final drift reconciliation verdict", "result": "Final drift reconciliation completed and delivered above...
[2m  ▎ thinking[0m
Done. The full reconciliation is above. The bottom line:

**Benchmark: go ahead.** The implementation is correct, safe, and all three reviewers approve-with-changes. Fix the bundling discipline before the commit ships, but nothing blocks running benchmarks on this code.
  · 36747 tok · in 36675 (36352 cached / 323 new) · out 72 (17 reasoning) · ¥0.0012
  ! final-answer readiness blocked: latest successful todo_write still has incomplete items: Write final drift reconciliation verdict: in_progress
[2m  ▎ thinking[0m
  · 35006 tok · in 34849 (17152 cached / 17697 new) · out 157 (55 reasoning) · ¥0.0184
  -> todo_write {"todos": [{"content": "Read and cross-analyze all five review documents", "status": "completed"}, {"content": "Identify...
[2m  ▎ thinking[0m
All items are now properly recorded as completed. The final answer was already delivered in full above. Here is the structured summary for clarity:

---

## Final Drift Reconciliation — Summary

**Verdict: approve-with-changes** (unanimous across three independent reviews)

**Benchmark: cleared to proceed.** The implementation is structurally correct, safe, and tested. No code changes are required before benchmarking.

**Post-benchmark, pre-commit requirements:**

| Requirement | Severity |
|---|---|
| Split diagnostic-log trigger narrowing into its own diff | Required — violates Rollout step 1 |
| Split or justify `modelErrorAfterPartialEditBlockerAnswer` reorder | Should fix |
| Split or justify child-write `RequireEvidence` prefix check | Should fix |
| Acknowledge plan-execute scope expansion in commit message | Required — proposal explicitly excludes plan-execute |
| Add inline comment for `EstimatedRoundTripsSaved` formula | Recommended |
| Note deferred benchmark summary integration in commit body | Recommended |

**No cross-review contradictions** that change the outcome. All three reviews reach `approve-with-changes`. The differences are about bundling discipline, not correctness.
  · 35298 tok · in 35028 (34816 cached / 212 new) · out 270 (27 reasoning) · ¥0.0014
