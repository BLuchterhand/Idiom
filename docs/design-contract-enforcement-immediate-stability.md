# Immediate Contract Enforcement Stability

**North Star: AI coding agents are becoming common, but teams do not yet trust
their context, decisions, or outputs. Idiom sells the missing substrate: repo
memory, freshness, contracts, audit, and evals.**

Status: reviewed and implemented; scripted e2e replay coverage added for
blocked and valid post-write verification trajectories, drafted 2026-05-24

## Purpose

This effort takes the fastest stabilizing lesson from Sponsio without turning
Idiom into a separate policy engine: make existing gates report one uniform,
replayable decision shape, and run any new enforcement as observe-only before it
blocks model work.

The goal is not to add natural-language-to-LTL, stochastic judges, framework
adapters, or a broad contract library in this slice. Those belong in
[design-contract-enforcement-followup.md](design-contract-enforcement-followup.md).

The immediate goal is operational stability:

- debugging a gate failure should not require reconstructing scattered runner
  prose;
- hard blocks should expose the contract, invariant, reason, observed facts, and
  valid alternatives;
- candidate enforcement should produce would-block evidence before it becomes a
  hard gate;
- the first reusable packs should wrap current invariants rather than invent new
  semantics.

## Background

Sponsio's strongest stability pattern is not its full formal-methods stack. It is
the product shape around enforcement: every action is observed at the tool-call
boundary, evaluated against named contracts, and recorded as pass, warn,
observe, block, or escalate. Teams can run in observe mode, inspect violations,
tune false positives, and only then enforce.

Idiom already has the richer authority object. `AutonomyContract` carries scope,
authority sources, path roles, capabilities, tool policy, obligations, budget,
verification, completion, readiness, policy decisions, and evidence refs. The
runner and compiler already emit or derive contract-shaped events.

The current stability problem is ownership and consistency. Completion gates,
plan-execute guards, verification checks, turn-phase contracts, recovery, and
audit-watch events each carry useful details, but not always in the same
machine-readable envelope. That makes regressions harder to compare, and it
encourages fixes that add another prompt or rejection string instead of
tightening one typed contract surface.

## Decision

Treat the existing `ContractGateEvent` payload on `EventGateDecision` as the
canonical gate decision envelope, and extend it with the missing fields needed
for Sponsio-style rollout and replay. Do not add a second parallel envelope.

The envelope is evidence, not a new strategy engine. It already records:

- `schema_version`
- `contract_id`
- `invariant_id`
- `gate`
- `decision`
- `reason_code`
- `tool_call_id`
- `tool_name`
- `target_refs`
- `changed_paths`
- `verification_status`
- `budget`
- `source`

This slice adds:

- `pack_id`
- `enforcement_mode`
- `observed_facts`
- `safe_alternatives`

`decision` keeps Idiom's existing `ToolPolicyDecision` vocabulary: `allow`,
`ask`, `deny`, `nudge`, `observe_only`, `reject`, and `stop`. Sponsio-style
words such as pass, warn, block, and escalate are comparison language only, not
new runtime values.

`enforcement_mode` is separate from `decision` and has exactly two values:
`enforce` and `observe_only`. A gate can make an `allow`, `reject`, or `stop`
decision under `enforce`. A candidate gate that would have blocked but is still
being tuned records `decision: observe_only` and `enforcement_mode:
observe_only`.

`observed_facts` are bounded structured labels, not raw evidence. They may cite
counts, statuses, reason codes, path refs, obligation refs, tool names, and
protocol hashes. They must not contain raw prompt text, raw transcript text,
file bodies, command output bodies, patch bodies, secrets, or customer data.

`safe_alternatives` are short model-visible recovery options, reusing the same
privacy constraints as guard-friction alternatives.

The runner should emit this envelope for the highest-value existing gates first:

- completion gate decisions;
- required-path write gate decisions;
- post-write verification gate decisions;
- loop and budget stop decisions when they block progress.

The first implementation leaves legacy events in place. Stability comes from
making `EventGateDecision` complete enough to be the shared evidence shape, then
teaching audit and tests to read it consistently.

E2e coverage now pins that shape through
`testdata/scripted-contracts/suite.json`:
`scripted-contract-replay-blocked-post-write-completion` saves a trajectory
where a premature completion is blocked until post-write verification runs, and
`scripted-contract-replay-valid-post-write-verification` saves the valid
write-then-verify-then-complete path. Both require `autonomy_event` gate
decision evidence in addition to the legacy post-write verification events.
The replay assertion keeps deterministic replay violations separate from broader
audit diagnostics: audit findings remain visible in the report, while
`max_violations` gates the replay engine's own missing-pack, missing-mode,
unsafe-fact, repeated-decision, and post-write-verification findings.

## Explicit Non-Goals

- No LTL/DFA runtime.
- No natural-language contract compiler.
- No external SDK adapters.
- No new hard-blocking policy for broad model behavior.
- No rewrite of `AutonomyContract`.
- No attempt to solve all prompt/protocol drift in this patch.

## Immediate Contract Packs

This slice defines "packs" as named, reusable invariant groupings over existing
Idiom gates. A pack is initially a reporting and audit grouping, not a separate
YAML plugin system.

### Built-In Pack IDs

Every new gate envelope needs a stable `pack_id`. Initial IDs are grouping
labels over existing invariants:

- `completion/readiness`: completion and final-answer readiness.
- `write/required_paths`: required edit path and write-scope checks.
- `verification/post_write`: post-write verification and mutation closeout.
- `loop/repeated_call`: repeated tool-call loop breaks.
- `budget/run_policy`: exhausted or exceeded budget stops.
- `plan_execute/protocol`: planner/executor protocol checks when the event is
  emitted from the plan-execute lane.
- `self_modify/guard_files`: observe-only reports for guarded harness surfaces.

### `plan_execute/protocol`

Purpose: stabilize planner/executor authority.

Invariants:

- planner phases cannot perform per-edit writes;
- planner terminal outcomes are valid `submit_plan`, accepted
  `Decision: source-satisfied`, or typed blocker;
- every required edit path is covered by a valid plan edit, source-satisfied
  evidence, or blocker.

Initial mode: enforce where runner gates already enforce, observe-only for any
newly detected contradictions.

Full parity for CLI plan-execute JSON events such as
`source_satisfied_rejected`, `planner_plan_rejected`, and
`review_repair_pass_rejected` is a follow-up integration unless this slice
explicitly touches `cmd/idiom/plan_execute.go`.

### `verification/post_write`

Purpose: stabilize mutation closeout.

Invariants:

- source edits open a verification obligation unless the active verification
  contract explicitly skips it;
- accepted verification must occur after relevant writes;
- skipped verification on mutation requires a blocker or explicit unsupported
  status.

Initial mode: enforce where completion gates already enforce, observe-only for
new post-write timing facts.

### `self_modify/guard_files`

Purpose: prevent accidental agent edits to harness-owned guard and audit
surfaces.

Invariants:

- edits to prompt layers, runtime policy files, explicit audit disabling knobs,
  and generated contract-pack definitions are reported as self-modification
  attempts;
- `.idiom` generated views, refine drafts, traces, and ordinary cache outputs
  are not blanket protected by this pack;
- enforcement starts observe-only unless a path is already protected elsewhere;
- reports must include the path role and safe alternative, usually "ask the
  operator" or "use the explicit maintenance command".

Initial mode: observe-only.

## Stability Phases

1. Extend `ContractGateEvent` with pack, mode, observed-fact, and
   safe-alternative fields. Implemented.
2. Emit complete `EventGateDecision` payloads from runner gate decisions without
   removing legacy events. Implemented for the immediate runner gate surface.
3. Teach `AuditEvents` and `harness_audit` to summarize pack/mode coverage and
   contradictions. Implemented for current typed gate events.
4. Add focused regression tests for allow/reject/stop/observe outcomes.
   Implemented, with scripted e2e coverage for blocked and valid post-write
   verification trajectories.
5. Add the built-in pack names as stable grouping labels in emitted decisions.
   Implemented for the immediate built-in packs.

This order deliberately improves diagnosis before enforcement. If a new rule is
wrong, observe-only data should show that without breaking a lane.

## Implementation Slices

The work should be split into disjoint write scopes:

- Slice A, domain model: `internal/autonomy/*`
  - extend `ContractGateEvent` and normalization/validation tests;
  - add stable pack and enforcement-mode vocabulary;
  - reject unsafe observed facts and safe alternatives;
  - keep the model free of runner imports.
- Slice B, runner emission: `internal/agent/*`
  - map existing gate decisions into complete `EventGateDecision` payloads;
  - include contract ID, invariant ID, reason code, facts, and alternatives;
  - preserve existing event names for compatibility.
- Slice C, audit/reporting: `internal/autonomy/audit*` and
  `internal/workspace/harness_audit*`
  - count gate decision coverage by gate, pack, enforcement mode, and safe
    alternative presence;
  - flag missing pack or mode on typed gate events;
  - keep raw prompt/transcript/file-body content out of reports.
- Slice D, docs/tests: focused tests plus roadmap links.

Do not include broad plan-execute CLI event parity in the first implementation
unless the focused runner changes are already stable. That parity belongs in the
follow-up doc with a clearer event-correlation design.

## Verification

Required focused checks:

```sh
go test ./internal/autonomy
go test ./internal/agent
go test ./internal/rootcmd ./internal/lanecmd ./internal/planexecutecmd
```

If runner/audit behavior changes beyond envelope emission, run:

```sh
go test ./...
```

## Alignment Criteria

Reviewer alignment means:

- the root cause is accepted as incomplete, inconsistently covered typed gate
  evidence rather than a missing prompt reminder;
- the patch improves observability before adding new hard enforcement;
- every new field is bounded and privacy-safe;
- the implementation preserves valid existing lanes;
- new packs are grouping labels over existing invariants, not scenario-specific
  product policy;
- tests cover at least one blocked case and one valid case.

## Review Log

Initial reviewer bench completed on 2026-05-24:

- Incident Root-Cause, Harness/Go, Prompt/Protocol, and North-Star/Architecture
  reviewers agreed the stability direction is aligned.
- Blocking correction: extend existing `ContractGateEvent` instead of adding a
  parallel `GateDecisionEnvelope`.
- Blocking correction: define pack IDs, enforcement mode, observed-fact bounds,
  and safe-alternative privacy rules before implementation.
- Blocking correction: keep self-modification enforcement observe-only and avoid
  blanket protection for all `.idiom` generated state.
- Scope correction: `harness_audit` implementation is under
  `internal/workspace`; CLI plan-execute JSON event parity is follow-up unless
  explicitly included.

Second reviewer pass completed on 2026-05-24:

- Incident Root-Cause, Harness/Go, Prompt/Protocol, and North-Star/Architecture
  reviewers all marked the immediate design aligned for implementation.
- Remaining blocking issues: none.
- Non-blocking implementation notes: keep `observed_facts` tightly typed and
  validated; require or audit `safe_alternatives` mainly for reject/deny/stop
  outcomes; keep historical logs readable; add `go test ./internal/workspace`
  when harness audit changes land.

Implementation update on 2026-05-25:

- `/feature-planner` atom dispatch now delegates execution to the plan-execute
  lane instead of accepting a direct feature-planner implementor model closeout.
- Feature-planner active-run and event data records the atom lane as
  `plan-execute`; completion is accepted through a typed
  `plan_execute_completed` envelope.
- Scripted TUI regressions require the nested plan-execute completion event and
  `idiom.feature_planner.atom_completed`, so a direct model-only implementor
  path no longer satisfies the saved e2e contract.
- A retained delivery run exposed and fixed the CLI adapter's nil
  plan-execute prompt runner. Nested feature-planner plan-execute dispatch now
  uses deterministic exact-plan execution with review repair skipped in this
  adapter path, records the nested progress state path when JSON events expose
  one, and phrases atom required tests as requested verification commands so
  plan-execute runs them.
- Added `scripted-feature-planner-tui-plan-execute-delivery`, a live TUI
  regression that plans an atom, writes a source file plus test through nested
  plan-execute, observes `go test ./internal/fpdelivery ... ok` in the atom
  completion summary, and verifies rollback captured both changed files.
