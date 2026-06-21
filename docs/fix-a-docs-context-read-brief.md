# Fix A: Allow Bounded docs/*.md Context Reads After Plan-Execute Readiness

Status: planned

## Problem

`planThenExecuteCandidatePostReadinessDiscoveryRejection` in
`internal/runtime/agent/plan_execute.go` rejects every post-readiness read
whose target is not in `brief.AllowedEditPaths`. This conflates write scope
with read scope: once a planner has source-body evidence for its edit target,
it cannot read any other file — including directly relevant design docs.

Observed failure: a plan-execute planner editing
`internal/contracts/plan/scope_contract_test.go` was blocked from reading
`docs/design-intent-loop-mutation-cycle.md` (revised v4 the same day,
references live session `20260618-171432.600`, explicitly discusses the
`PlanScopeContract` under test). The block terminated the planner turn with
`planner_blocked` / "Plan-execute read outside allowed candidate scope"; the
synthesized blocker template then reached the operator. The model had
correctly identified a relevant context read; the harness wrongly denied it.

North-star violation: `AGENTS.md` requires that read requests be honored
unless a real invariant rejects them, and that `allowed_edit_paths` (mutation
scope) not be used as shorthand for read scope. The current gate uses
`allowed_edit_paths` as the read-scope predicate.

## Scope

- `internal/runtime/agent/plan_execute.go`
- `internal/runtime/agent/contract_surface_tests_test.go`

## Reference Context

- `internal/runtime/agent/plan_execute.go:8923-8958` — current rejection logic.
- `internal/runtime/agent/plan_execute.go:9084-9106` —
  `planThenExecuteCandidateDifferentPathReadAllowed` shows the existing
  candidate-recovery-scope exception pattern.
- `internal/runtime/agent/plan_execute.go:9482-9490` —
  `readPathsFromResults` returns the set of distinct paths already read this
  turn; reuse it to count prior context reads.
- `docs/decisioning-north-star.md` — read-request honoring rule.
- `docs/idiom-maintenance-best-practices.md` — `allowed_edit_paths`
  scope-separation guidance.

## Files To Modify

| File | Change |
| --- | --- |
| `internal/runtime/agent/plan_execute.go` | Add a pure helper that classifies a path as a context doc (e.g. `docs/**/*.md` and repo-root `*.md` files such as `README.md`, `AGENTS.md`). Add a pure helper that counts distinct context-doc paths already present in `readPathsFromResults(results)`. In `planThenExecuteCandidatePostReadinessDiscoveryRejection`, before the `allowedCandidateSet` rejection fires, return no-rejection when (a) the target is a context doc AND (b) the count of distinct context docs already read is below the cap. The cap is a package-level constant set to 5. When the cap is exceeded, fall through to the existing rejection path so the anti-sprawl invariant stays intact. Do not change behavior for non-doc paths. |
| `internal/runtime/agent/contract_surface_tests_test.go` | Add table-driven cases alongside `TestPlanThenExecuteCandidatePostReadinessRejectsDirectorySearchContainingReadyPath` (line ~28619) covering: (1) a `read_file` of `docs/design-intent-loop-mutation-cycle.md` after readiness with no prior doc reads is NOT rejected; (2) the same read after 5 prior distinct `docs/*.md` reads IS rejected with the existing "outside allowed candidate scope" summary so the cap is enforced; (3) a `read_file` of a non-doc path outside `AllowedEditPaths` is still rejected exactly as today. Do not weaken the existing cases. |

## Verified Current State

- `internal/runtime/agent/plan_execute.go:8945-8958` rejects any post-
  readiness read whose target is not in `AllowedEditPaths`, with no exception
  for design docs or markdown context files.
- `readPathsFromResults` is the canonical way to count prior distinct reads.
- No existing helper classifies paths as context docs.

## Required Behavior

1. The post-readiness read gate keeps rejecting non-doc paths outside
   `AllowedEditPaths` exactly as today. Behavior for source files, search
   tools, and unrelated paths is unchanged.
2. A post-readiness `read_file` whose target is a context doc is allowed
   while the count of distinct context docs already read this turn is less
   than 5.
3. Once 5 distinct context docs have been read this turn, further context-doc
   reads are rejected with the existing summary and error text so the
   anti-sprawl loop bound still holds.
4. The classifier helpers are pure functions of `(path string)` and
   `(results []workspace.ToolResult)`. They do not perform I/O. The rejection
   function remains pure and unit-testable as today.
5. "Context doc" means a path whose normalized form starts with `docs/` and
   ends in `.md`, or is a repo-root markdown file (`README.md`, `AGENTS.md`,
   `CONTRIBUTING.md`, `CHANGELOG.md`). Anything else stays in the existing
   rejection path. This is intentionally conservative; broader generalization
   belongs in the follow-up shared read-scope contract.
6. No prompt, recovery text, telemetry event, or audit field is added in
   Fix A. The shared `ReadApprovalDecider` affordance is a separate fix (C).

## Out of Scope

- Operator prompt for non-auto-approved reads (Fix C).
- Wiring the synthesizer-leak detector into the model-rewrite path (Fix B).
- Any change to write scope, source-body obligation scope, or candidate
  discovery scope. `allowed_edit_paths` continues to govern writes exactly as
  today; only the post-readiness read predicate loosens for a bounded class
  of context docs.
- Any change outside the two listed files.

## Acceptance

- New helper classifies `docs/design-intent-loop-mutation-cycle.md`,
  `docs/idiom-maintenance-best-practices.md`, `README.md`, `AGENTS.md` as
  context docs.
- New helper does not classify `internal/runtime/agent/plan_execute.go`,
  `internal/runtime/agent/plan_execute_test.go`, `go.mod`, or any non-md path
  as a context doc.
- Existing cases in
  `TestPlanThenExecuteCandidatePostReadinessRejectsDirectorySearchContainingReadyPath`
  and
  `TestPlanThenExecuteCandidatePostReadinessRejectsSiblingTopicSearchOutsideAllowedScope`
  continue to pass unchanged.
- New cases prove: under-cap docs read is allowed; over-cap docs read is
  rejected with the existing summary; non-doc out-of-scope read is rejected
  unchanged.
- `go test ./internal/runtime/agent -run 'PlanThenExecute' -count=1` passes.
- `go test ./internal/runtime/agent -count=1` passes.
- `go test ./...` passes.
- `git diff --check` is clean.

## Required Tests

- `go test ./internal/runtime/agent -run 'PlanThenExecute' -count=1`
- `go test ./internal/runtime/agent -count=1`
- `go test ./...`
