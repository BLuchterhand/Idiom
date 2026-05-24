# Feature Planner Orchestration Evidence

Date: 2026-05-24

Source design:

- [Feature Planner Orchestration](../design-feature-planner-orchestration.md)

Related designs:

- [Feature Planner TUI Orchestration](../design-feature-planner-tui-orchestration.md)
- [Behavioral TDD Coverage](../design-tdd-coverage.md)
- [PRD To ADR To Brief Research Workflow](../design-prd-adr-brief-research-workflow.md)

## Research Scope

The design defines a feature-level workflow for planning, repairing,
dispatching, reviewing, recovering, replanning, continuing, and releasing
multi-atom deliverables. The durability/TDD addendum specifically investigated:

- durable progress and resume patterns in `internal/planexecute/progress.go`
- loopable planner modes and transport projections in
  `internal/agent/plan_execute_planning_state.go`
- TDD obligations, implementation ledger records, and report paths in
  `internal/productworkflow/types.go`
- coverage-gate behavior in `internal/productworkflow/coverage_gate.go`
- dispatch brief blocking behavior in `internal/productworkflow/brief_generation.go`
- SDD/TDD target coverage in `docs/design-tdd-coverage.md`
- spec-first workflow contracts in
  `docs/design-prd-adr-brief-research-workflow.md`

## Findings

Verified facts:

- `plan-execute` already persists phase/status, prompt hash, plan path/id, pack
  id, edit progress, executor evidence, review-repair state, failure
  kind/reason, and resume readiness.
- Plan-execute planning state already derives loopable modes such as required
  source, candidate source, submit-ready, submit-correction, review-repair, and
  done.
- Product workflow coverage fails closed when TDD obligations lack verification
  evidence.
- Product workflow brief generation blocks when coverage is blocked, planned
  scope is missing, or TDD/verification commands are absent.
- The target TDD design treats behavioral coverage as a systematic pipeline:
  ADR, EARS, examples, BDD, Design by Contract, FMEA/HAZOP/FTA, state model,
  tests, mutation, and dispatch gate.

Inferences:

- Feature orchestration should not duplicate lane execution; it should add a
  durable coordinator above existing lane progress.
- Loopable planning needs explicit done and stuck triggers; otherwise it becomes
  prompt retry behavior instead of a state machine.
- The top-level plan should own the atom graph and SDD/TDD contracts, not
  implementation strategy inside atoms.
- Pre-execution TDD evidence needs its own dispatch gate because post-write
  verification alone cannot prove tests shaped implementation.

## Design Updates

The design now includes:

- `planning`, `planning-repair`, `planning-question-open`, and
  `planning-stuck` feature states.
- `question-for-planner` and `question-for-operator` atom states.
- Typed `PlannerQuestion` artifacts for implementor-to-planner handoff, including
  question payload, requested decision, and answer authority.
- `planning_loop`, `active_runs`, continuation, event replay, lease delta, and
  active-run delta fields on feature state.
- Top-level plan scope boundaries.
- SDD contract fields for spec sources, ADRs, requirements, examples, contracts,
  state models, decision tables, and open spec questions.
- TDD contract fields for coverage tier, pre-execution status, obligation ids,
  test artifacts, test commands, baseline events, immutable snapshots, and
  coverage-gate event refs.
- Test-first atom fields requiring pre-execution red/characterization evidence
  before implementation dispatch.
- Acceptance rows for planning loop, SDD, pre-execution TDD, planner questions,
  continuation, and product workflow projection.

## Review Status

Earlier full-bench review of this design direction found blockers around runtime
envelopes, transition tables, TUI parity, prompt/protocol transport projection,
TDD/productworkflow evidence, and risk acceptance. Those findings shaped the
current document.

Targeted final review was then run for the durability/TDD addendum.

### Harness / Go Reviewer

Decision: SIGNOFF.

Resolved blockers:

- Feature and atom transition semantics are separated, with atom deltas folded
  inside feature events.
- Event replay has before/after revisions, idempotency keys, atom deltas, lease
  deltas, active-run deltas, artifact deltas, and hard replay failures for
  malformed state.
- Active-run continuation includes progress path/revision/hash, heartbeat
  source, result source, lane contract ids, and deterministic running,
  terminal-unfolded, crashed, orphaned, and unknown classifications.
- Fenced dispatch commits CAS, leases, active-run insert, event append, and state
  write as one transition.

### Prompt / Protocol Reviewer

Decision: SIGNOFF.

Resolved blockers:

- `PlannerQuestion` preserves structured question payload, blocking claim, and
  requested decision.
- Native required-tool mode cannot fake `<<QUESTION>>:`; questions are host
  conversions from structured rejection data or use a non-required-tool turn.
- `source-satisfied`, malformed output, and stale-anchor outcomes are reachable
  through explicit atom deltas.
- Model prose remains non-authoritative unless compiled into typed artifacts.

### TDD / Acceptance Reviewer

Decision: SIGNOFF.

Resolved blockers:

- Pre-execution TDD baseline records are productworkflow-visible and include
  `task_id`, `effort_id`, obligation id, command, result class, workspace
  fingerprint, git head, artifact paths, ordering event id, and timestamp.
- Productworkflow needs typed feature fields for baseline and snapshot evidence,
  rather than relying on an untyped `Extra` map.
- Pre-dispatch `source-satisfied` emits productworkflow-visible
  `verification_completed` satisfaction evidence or must become an explicit
  coverage-gate satisfaction source.
- Post-write verification is checked against the immutable pre-dispatch TDD
  snapshot.

### North-Star / Architecture Reviewer

Decision: SIGNOFF.

Resolved blockers:

- Planner-question resolution now has an authority matrix, preventing the planner
  from self-resolving product/spec/scope decisions without accepted source,
  operator approval, harness policy, or fresh context.
- Risk acceptance has concrete fields and cannot waive dispatch, SDD/TDD,
  verification, review, safety, privacy, audit, identity, or write-scope
  invariants.
- Recovery and release-readiness policy ownership is expressed as harness-owned
  policy refs with planner-provided hints.
- Next actions are derived, revision-bound, and non-authoritative.

No raw prompts, transcripts, file bodies, secrets, or customer-identifying data
are stored in this evidence file.
