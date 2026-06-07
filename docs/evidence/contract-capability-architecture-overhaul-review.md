# Contract And Capability Architecture Overhaul Review

Status: reviewer evidence, created 2026-05-24

Reviewed document:
`docs/design-contract-capability-architecture-overhaul.md`

Reviewer bench:
`docs/debugging-reviewer-bench.md`

All six codified reviewers were run read-only against the draft:

- Incident Root-Cause Reviewer
- Model Runtime / Config Reviewer
- Harness / Go Reviewer
- Prompt / Protocol Reviewer
- TDD / Acceptance Reviewer
- North-Star / Architecture Reviewer

## Review Outcome

Decision: revise before implementation.

The reviewers agreed that the direction is north-star aligned: make
`AutonomyContract` the source of truth, keep model strategy with the model, and
move harness authority into typed, replayable contracts. The draft needed
tightening before it could guide implementation.

The revised design incorporates the reviewer findings by adding:

- a named first broken invariant, `contract.active_authority`
- an explicit evidence-gaps section
- model-facing protocol projection and protocol hash requirements
- runtime envelope as Phase 1 contract input
- fixed package ownership for contract domain and compilation
- acceptance and atomization plan
- no-go gates for dispatchable implementation atoms
- traceability matrix from invariant to event, package, fixture, audit, and
  product evidence
- concrete regression-test families
- stricter Phase 1, Phase 3, and Phase 5 acceptance criteria

## Incident Root-Cause Reviewer

Verified facts:

- The design separates verified facts from inferences.
- The design identifies multiple current authority sources:
  `AutonomyContract`, runtime brief, runner rails, workspace manifests, lane
  wrappers, prompts, plan-execute scope, gates, telemetry, and audit
  reconstruction.
- Current source has an `AutonomyContract` with contract id, parent id, scope,
  authority, path roles, capabilities, tool policy, obligations, verification,
  completion, readiness, policy decisions, and evidence refs.
- Some phase-contract machinery is still shadow/projection state rather than
  authoritative enforcement.

Inferences:

- The design identified the right class of failure but did not initially name
  the first broken invariant with incident-level precision.
- Aggregate audit evidence proves the failure class but not one ordered invalid
  transition.

Root cause:

- Before model dispatch, tool-policy decision, verification decision, lifecycle
  closeout, or audit interpretation, there must be exactly one active contract
  identity whose invariant IDs own the decision.

Contributing factors:

- Existing contract machinery is partly passive.
- Audit findings are aggregate.
- Contract compilation package ownership was initially ambiguous.
- The first slice needs passive coverage before enforcement migration.

Non-causes:

- Tool schema drift.
- Missing graph freshness.
- Absence of an `AutonomyContract` type.
- Model quality as the first proven cause.

Fix recommendation:

- Add `contract.active_authority` as the first broken invariant.
- Record command output and reviewer evidence durably.
- Add an evidence-gaps note.

Regression tests:

- Every covered lane emits `contract_compiled` before model dispatch.
- No final-answer, tool-policy, write, verification, or lifecycle event is
  accepted without `contract_id` and `invariant_id`.
- A known turn-phase contradiction can be grouped without transcript parsing.
- Prompt/runtime brief text cannot expose a tool hidden by contract policy.
- Implementation-backed runs always produce closeout tied to the contract.

North-star risk:

- Medium. The design is aligned, but the compiler must govern authority rather
  than strategy.

Confidence:

- Medium-high on diagnosis; medium on incident completeness because evidence is
  aggregate.

## Model Runtime / Config Reviewer

Verified facts:

- The initial draft put backend, role, parser mode, thinking policy, token
  budget, queue state, readiness profile, degraded decisions, and fallbacks in
  Phase 5.
- Current `AutonomyContract` and `WorkerRuntimeBrief` do not yet explicitly
  carry backend, parser/transport, thinking mode, context window, visible max
  tokens, queue lease, or degraded fallback fields.
- The model client has runtime-sensitive knobs such as thinking, max tokens,
  forced native tools, and response-format JSON.
- Queue admission currently proceeds ungated when the queue daemon is missing
  or unavailable.

Inferences:

- Phase 5 was too late for runtime state. Runtime drift can invalidate earlier
  contract coverage.
- Parser/transport should be modeled separately from backend.

Root cause:

- Local-model operations were modeled as a later readiness enhancement rather
  than a first-class contract input before model dispatch.

Contributing factors:

- Runtime facts are split across config, client options, doctor readiness, lane
  telemetry, queue admission, and runner telemetry.
- Queue behavior is intentionally best-effort in some flows.
- Budget fields are not the same as visible output/token controls.

Non-causes:

- Lack of runtime telemetry.
- Generic model quality.
- Ignoring local-model operations entirely.

Fix recommendation:

- Promote a minimal runtime envelope into Phase 1.
- Split readiness into `report_only`, `allowed_degraded`, and
  `launch_blocked`.
- Require every model call and lane/phase transition to cite the active runtime
  contract id.

Regression tests:

- Contract compilation includes runtime envelope before first model call.
- MLX defaults to textual JSON/tool parsing unless native tools are explicitly
  authorized.
- Missing backend role is launch-blocked once enforcement is enabled.
- Queue unavailable is degraded or blocked according to lane policy.
- Token truncation updates the obligation/degraded ledger.

North-star risk:

- Medium. Runtime drift outside the first contract weakens the harness-owned
  contract boundary.

Confidence:

- High on the gap and recommendation.

## Harness / Go Reviewer

Verified facts:

- Current runner derives an autonomy contract from `WorkerRuntimeBrief`,
  `RunPolicy`, and tool results inside `internal/runtime/agent`.
- `WorkerRuntimeBrief` and `RunState` duplicate many contract-shaped fields.
- Runner control rails rebuild allowed tools, permission profile, required
  evidence, readiness, verification, and stop conditions from brief/workspace
  state.
- Workspace independently owns tool-surface listing and read-only/write
  blocking.
- Existing tests already cover several contract-event and path-scope cases.

Inferences:

- Phase 1 risked becoming a better event mirror of current runner state instead
  of moving authority.
- The first migration target should be sharper; plan-execute planner is the
  best pilot.
- The capability coverage target was under-specified.

Root cause:

- Harness authority is reconstructed in runtime brief, runner control rails,
  gates, workspace manager policy, completion gates, recovery prompts, and
  audit.

Contributing factors:

- Large responsibility centers in runner, workspace manager, app dispatch, and
  plan-execute CLI.
- Broad cross-cutting `ToolRequest` schema.
- Audit detects turn-phase contradictions but does not remove duplicated
  authorities.

Non-causes:

- Lack of contract vocabulary.
- Tool schema drift.
- Language adapters themselves.
- File size alone.

Fix recommendation:

- Require a pure contract compiler outside `internal/runtime/agent` and concrete
  workspace adapters.
- Make covered runner gates consume `PolicyDecision` and obligation-ledger
  outputs.
- Define one `PathRoleResolver`.
- Keep workspace manager safety checks as adapter backstops.
- Add a lifecycle closeout owner.
- Replace open-ended capability coverage with a numeric/classification slice.
- Add package-boundary tests.

Regression tests:

- Contract compiler fixtures.
- Runner gate tests over contract decisions.
- Path-role table tests.
- Workspace policy backstop tests.
- Lifecycle closeout matrix.
- Capability result envelope tests.
- Audit replay tests.
- Import-boundary tests.

North-star risk:

- Contract compiler or capability resolver must not become a strategy engine.
- Prompt-only migration would preserve duplicated authority.
- Removing backstops too early would weaken safety.

Confidence:

- High on diagnosis; medium-high on recommendations.

## Prompt / Protocol Reviewer

Verified facts:

- The design states prompts explain the contract but are not the contract.
- It initially did not explicitly name malformed tool calls, textual/native
  tool-call protocol parity, expected visible output, or terminal response
  shapes.
- Existing phase-contract tests cover stale-anchor refresh, malformed
  `submit_plan` recovery, and review-repair terminal phases.

Inferences:

- The model-facing protocol boundary was implicit.
- Malformed tool calls need explicit contract vocabulary.
- Stale-anchor refresh should appear in the same protocol view the model sees
  and the gate enforces.

Root cause:

- The draft defined `AutonomyContract` but not a concrete model-facing protocol
  projection binding prompt text, visible tools, parser mode, recovery,
  terminal states, and gate validation to the same contract version.

Contributing factors:

- Phase 1 focused on event coverage before protocol parity.
- Policy subjects did not include malformed tool parse attempts.
- Terminal vocabulary was not enumerated.

Non-causes:

- North-star mismatch.
- Missing audit ambition.
- Model quality.

Fix recommendation:

- Add `Model-Facing Protocol Contract`.
- Include protocol hash, parser mode, expected visible output, visible tools,
  action classes, terminal response shapes, malformed-output policy,
  stale-anchor policy, source-body obligations, and recovery budget.
- Add malformed parse attempts as policy subjects/events.

Regression tests:

- Malformed output produces the same visible output shape, terminal shapes, and
  recovery budget in protocol view and gate.
- Stale anchors require source refresh evidence.
- Terminal-state phases expose only terminal-compatible actions.
- Prompt/control-rail text, parser-visible tools, callable tools, and
  completion gates cite the same contract/protocol ids.

North-star risk:

- Medium. Without protocol projection, implementation could recreate contract
  in one place, prompt text in another, and gates in a third.

Confidence:

- Medium-high.

## TDD / Acceptance Reviewer

Verified facts:

- The design has six phases and acceptance bullets.
- Test intent was present but initially mostly category-level.
- Product-workflow evidence was under-specified.
- Existing guidance requires artifact classification before dispatch.

Inferences:

- The design was not yet acceptance-ready for implementation dispatch.
- Phases were too large for safe implementation atoms.
- Several acceptance bullets were not directly testable.

Root cause:

- The draft defined the architectural target but not the acceptance operating
  model: dispatchable atom schema, no-go gates, verification commands, fixture
  inventory, product-workflow evidence, and failure packets.

Contributing factors:

- The first covered lane was not defined.
- Capability coverage target was deferred.
- Package-boundary/responsibility metrics were qualitative.

Non-causes:

- Lack of phased migration.
- Lack of north-star alignment.
- Over-focusing on line count.
- Proposal to weaken gates.

Fix recommendation:

- Add `Acceptance And Atomization Plan`.
- Define the first covered lane.
- Convert phases into small implementation atoms with task id, complexity tier,
  expected/forbidden files, TDD obligations, required tests, audit signal,
  rollback note, implementor/reviewer evidence, and investigation requirement.
- Add traceability matrix.
- Replace vague acceptance bullets with checkable gates.
- Add failure-packet/no-go rule.

Regression tests:

- Contract compiler golden tests.
- Event coverage tests.
- Path-role resolver tests.
- Product-workflow atom-intake tests.
- CLI/TUI/sidecar parity fixtures.
- Capability-routing replay fixture.
- Readiness gate tests.
- Audit replay tests.

North-star risk:

- Medium. Without atomization, work can degrade into scattered event tags,
  prompt reminders, and broad refactors.

Confidence:

- Medium-high.

## North-Star / Architecture Reviewer

Verified facts:

- The design preserves model strategy vs harness contract.
- The target dependency rule is clean-architecture aligned.
- Existing `WorkerRuntimeBrief` duplicates many contract-shaped fields and
  imports workspace types.
- Initial package plan left contract compilation/package placement ambiguous.
- Non-goals protect against strategy capture and generic write capabilities.

Inferences:

- The direction is correct; ownership placement is the largest risk.
- Leaving compilation in `internal/runtime/agent` or routing in `internal/capabilities/workspace`
  could preserve mixed responsibilities under cleaner names.
- Import-boundary and event-schema tests should precede behavior changes.

Root cause:

- Duplicated authority across runtime brief, runner gates, workspace policy,
  prompts, lane wrappers, verification, and audit.

Contributing factors:

- `WorkerRuntimeBrief` and `RunState` carry many of the same fields as the
  proposed contract.
- Large responsibility centers remain.
- Evidence-to-readiness-to-compiler feedback must be mediated by typed
  policy/use-case boundaries.

Non-causes:

- Model quality.
- Absence of a contract model.
- File size alone.
- Tool-schema drift alone.

Fix recommendation:

- Approve direction, tighten before implementation.
- Put contract domain in `internal/contracts/autonomy`.
- Put contract compilation in a use-case/application package using plain input
  models.
- Keep runner as a consumer of decisions.
- Make capability contracts/result envelopes inner concepts; workspace,
  language, and shell tools are adapters.
- Require every enforcing/nudging/rejecting/verifying/closing event to include
  `contract_id`, `invariant_id`, and relevant `obligation_id`.

Regression tests:

- Import-boundary tests.
- Contract parity tests.
- Path-role tests.
- Strategy-boundary tests.
- No prompt-magic tests.
- Audit replay tests.
- Migration compatibility tests.

North-star risk:

- Medium. Package placement ambiguity could recreate authority leaks in new
  modules.

Confidence:

- High on direction and root cause; medium on migration risk.
