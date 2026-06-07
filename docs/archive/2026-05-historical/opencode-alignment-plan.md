# OpenCode-alignment rewrite — final status

In-place refactor that brought the agent loop in line with OpenCode's minimal
shape: user prompt → single system prompt + tools → model loop → answer.
No hardcoded phase machines, no templated fallback responses, no rails that
rewrite model output, no deterministic edit shortcuts racing the model.

**Net impact: 3,612 deletions / 321 insertions across 16 files.**

## Behavioral changes

### Phase 1 — listening fix

- `internal/surfaces/tui/app/analysis_agents.go`: removed the pre-model
  `deterministicArchitectureAnswer` short-circuit. Every architecture prompt
  reaches the model before any fallback can fire.
- `internal/frontier/explore.go`: added `PromptRequestsScopeLimitedAnswer`.
  The templated Phase 1/2/3 scaffold is suppressed for prompts containing
  "one small / atomic / single / what is one" and similar scope qualifiers.
- `internal/frontier/control.go`: `EnforcementForAnswer` gates the
  `changed_files_or_noop_missing` and `verification_contract_unresolved`
  violations on `railsIntentWantsEdits`. Plan-only / plan-architecture /
  verification-only / debug-logs intents no longer trigger the implementation
  contract.
- New test: `TestArchitectureFallbackSummaryHonorsScopeLimitedPrompts` in
  `internal/surfaces/tui/app/write_guard_test.go`.

### Phase 2 — unified dispatch + subagent cleanup

- `internal/surfaces/tui/app/prompt_routing.go`: `runPromptPlan` no longer dispatches to
  lane-specific subagents. Every non-implement prompt goes through
  `runMainAgent`, which has the full workspace tool set via `agent.Runner`.
  Collapsed from ~120 lines to ~10.
- Deleted `runExploreSubagent`, `runArchitectureSubagent`, `runReviewSubagent`,
  `runLocateAgent` and all their transitive helpers
  (`bootstrapExploreEvidence`, `bootstrapArchitectureEvidence`,
  `deepenArchitectureEvidence`, `bootstrapReviewEvidence`,
  `deepenReviewEvidence`, `completeAnalysisSummary`,
  `deterministicArchitectureAnswer`).
- Deleted templated fallback renderers: `buildExploreFallbackSummary`,
  `buildArchitectureFallbackSummary`, `buildReviewFallbackSummary`,
  `renderTargetedArchitecturePlan`, `renderBoundedOneFileArchitecturePlan`.
- Deleted 23 orphaned tests that exercised the above.

### Phase 3 — rails no longer rewrite

- `internal/surfaces/tui/app/control_rails.go`: `applyControlRailsFinalAnswer` still
  computes the enforcement snapshot for telemetry but no longer calls
  `repairControlRailsAnswer`. The model's answer passes through verbatim.

### Phase 4 — model owns every edit

- `internal/surfaces/tui/app/implementation.go`: deleted ~225 lines of dispatch plus the
  function bodies of eight deterministic pre-model edit shortcuts:
  `tryDirectPromptEdit`, `tryExactSentenceAppend`,
  `tryDirectConfigFieldUpdate`, `tryDirectLocateRemoval`,
  `tryDirectUISpacingEdit`, `tryFocusedStructuredExecutionPlan`,
  `tryFocusedWindowRewrite`, `tryStructuredExecutionPlan`.
- Also deleted `tryDeterministicVerificationRepair` (only called by the
  shortcut dispatch).
- The model now drives every edit through the agent runner and workspace
  tools. Guardrails (`writeGuard`, `targetGuard`, `structuralAnchorGuard`,
  rollback journal, verification loop) still apply during model-driven
  execution.

### Phase 5 — pipeline posture

With rails neutralized (Phase 3), subagent lanes collapsed (Phase 2), and
shortcuts deleted (Phase 4), the pre-model pipeline (route_plan →
intent_verifier → rails → dispatch) is effectively advisory:

- Route classification still decides implement vs non-implement dispatch,
  which is necessary for the guarded write policy and the implement-lane
  scaffolding that helps a smaller local model.
- `intent_verifier` is itself a model call (`runPromptIntentVerifier`
  against the route-classifier model) — it's already OpenCode-shaped.
- Rails no longer mutate answers.
- No deterministic output overrides the model anywhere.

### OpenCode blank #1 — unified write-permission UX

- `internal/surfaces/tui/app/lifecycle.go`: `runMainAgent` now uses `implementWriteGuard`
  and the same approval-proposal flow as the implement lane. In guarded-write
  mode, a model that decides to edit produces an interactive approval
  proposal instead of a terse "switch lanes" refusal. In auto-write mode,
  writes pass through. Eliminates the lane-switching gotcha.

### OpenCode blank #2 — pre-classifier retained (not a gap)

After the above, the remaining "classifier before model" step is not actually
an OpenCode gap for this codebase:

- OpenCode drives Claude, which can plan an implementation, read the right
  files, make the edit, and verify it end-to-end in one loop. The classifier
  that idiom runs is redundant for that model.
- idiom drives a local Qwen model, which benefits from the implement
  lane's scaffolding: target packet construction, structural anchor guards,
  verification + repair loops, rollback journal. Routing write-intent prompts
  to that lane is legitimate model-capability help, not a harness-knows-best
  anti-pattern.
- Removing the classifier would require replacing that scaffolding with
  something the small model can drive itself. That's a separate initiative,
  not an alignment fix.

## Invariants preserved through every step

- `go build ./...` green at every checkpoint.
- `go test ./...` green at every checkpoint (all packages).
- No tool gained new filesystem access.
- Write guard fires on every edit, create, delete.
- Rollback journal writes on every successful edit chain.
- Target packet, structural anchor guard, and verification loop still apply
  to the implement path.

## What remains (by choice, not deferral)

1. **Pre-classifier.** Serves the small-model scaffolding, not an
   anti-pattern. Keep until the scaffolding itself is replaced.
2. **Workflow state machine** (`workflow_state.go`, `long_horizon_state`).
   Drives resume and approval-gated workflows. Orthogonal to the model-loop
   shape; OpenCode simply doesn't have this kind of resumable task
   persistence.
3. **Phase cards.** UI feature for user feedback during long runs.

If you later decide to tear out the scaffolding (swap to a stronger driver
model or accept local-model regressions), the deletion path is mechanical:
delete the implement-lane flow in `implementation.go` above the agent-runner
call, replace the dispatch in `prompt_routing.go` with an unconditional
`runMainAgent`, and remove the classifier telemetry pipeline.
