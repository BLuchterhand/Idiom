# Design Doc Implementation Workflow

Status: operating rule, created 2026-05-24

Use this workflow when a design document is broad enough that implementation
must be split across multiple agents, phases, packages, or documentation
surfaces. It applies to architecture overhauls, harness behavior changes, lane
contracts, prompt/protocol work, audit/eval surfaces, product workflow changes,
and any design whose implementation can outlive one session.

The goal is not to add ceremony. The goal is to make design-doc implementation
dispatchable, reviewable, parallel where safe, and resumable without relying on
chat memory.

## Controlling Rule

Implementation may begin only after the design has passed full preflight:

1. The design has been reviewed by the full
   [Debugging Reviewer Bench](debugging-reviewer-bench.md) when the design is a
   broad architecture, harness, lane, prompt/protocol, audit, or local-model
   change.
2. Reviewer outputs are recorded durably under `docs/evidence/` or the active
   product-workflow effort folder.
3. Review findings have been folded back into the design or recorded as
   explicit accepted risks.
4. The design phases have been converted into dispatchable implementation
   atoms.
5. Each atom has a current context packet or equivalent evidence packet.
6. Every atom has no-go gates checked before any implementor starts.

Small documentation edits may use a smaller reviewer set, but any effort that
changes shared harness authority, lane semantics, contract boundaries, or
operator workflow must use the full bench before implementation and again after
the complete deliverable is assembled.

## Required Artifacts

Each design-doc implementation effort needs durable artifacts:

- source design doc
- pre-implementation reviewer report
- implementation atom ledger
- per-atom implementor evidence
- per-atom reviewer evidence
- post-effort investigation reports
- final end-to-end reviewer report
- docs-freshness note covering roadmap and Obsidian parity
- verification record
- commit and push evidence after approval

Store these in the active product-workflow effort folder when one exists. For a
standalone design, use `docs/evidence/<design-slug>-implementation.md` or a
nearby evidence folder with the same durability and redaction expectations.

Reviewer and implementor evidence must avoid raw prompts, raw transcripts, file
bodies, secrets, and customer-identifying data. Keep paths, command ids, session
ids, aggregate counts, gate names, contract ids, invariant ids, findings, and
verification results.

## Atom Schema

Before coding, convert each design phase into one or more implementation atoms.
An atom is dispatchable only when it records all fields below:

```json
{
  "schema_version": "idiom.design_doc_implementation_atom.v1",
  "effort_id": "effort-slug",
  "task_id": "unit-001",
  "phase": "phase-name",
  "state": "planned",
  "complexity_tier": "C0|C1|C2|C3|C4",
  "context_evidence": ["context_pack:<pack-id>"],
  "expected_files": [],
  "forbidden_files": [],
  "blast_radius": "bounded description",
  "tdd_obligations": [],
  "required_tests": [],
  "audit_signal_expected_to_move": [],
  "rollback_note": "how to back out this atom safely",
  "implementor_evidence": "reports/unit-001-implementation.md",
  "reviewer_evidence": "reports/unit-001-review.md",
  "post_effort_investigation": "reports/unit-001-investigation.md"
}
```

Use the complexity tiers from the product workflow design:

| Tier | Typical scope |
| --- | --- |
| `C0` | prose docs, fixtures, examples, tiny mechanical updates |
| `C1` | focused schemas, validators, unit tests, narrow adapters |
| `C2` | CLI commands, storage, deterministic audit logic, status migration |
| `C3` | harness contracts, dispatch gates, telemetry, shared CLI/TUI workflow state |
| `C4` | privacy/security boundaries, protocol/state-machine correctness, data migrations, ambiguous behavioral failures |

Upgrade the tier when evidence is stale, tests are weak, the scope crosses
authority boundaries, or failure would affect write permissions, readiness,
verification, privacy, or sidecar contracts.

## No-Go Gates

An atom must not be sent to an implementor when any of these are true:

- Context evidence is stale or the atom lacks a current `context_pack` or
  replacement evidence packet.
- Expected files, forbidden files, complexity tier, TDD obligations, required
  tests, audit signal, rollback note, implementor evidence, reviewer evidence,
  or post-effort investigation fields are missing.
- Any expected file is dirty with unrelated work and the atom does not have an
  explicit dirty-scope allowance.
- The atom relies on prompt wording as authority instead of a typed contract,
  schema, gate, state machine, telemetry event, or test.
- The atom weakens an existing gate without both allowed-case and blocked-case
  regression tests.
- The atom has no regression test plan for a behavior, protocol, contract,
  workflow, or audit change.
- The atom's write scope overlaps another in-flight atom unless the
  orchestrator serializes them.
- Obsidian, roadmap, or operator docs are known stale and the atom changes the
  concepts those docs explain.
- The implementor and reviewer would be the same actor.

When a no-go gate trips, record a blocker or maintenance atom. Do not ask an
implementation lane or worker to discover the blocker by spending model tokens.

## Parallelization Rule

Parallelize aggressively only where authority and write scopes are disjoint.

Safe parallel candidates:

- independent documentation updates in different files
- fixtures that do not share generated state
- package-local code atoms with non-overlapping expected files
- reviewer-only work over immutable diffs or durable evidence
- read-only evidence gathering, context refresh, audit baselines, and doc gap
  analysis

Serialize when atoms touch:

- the same source file, fixture, prompt, contract schema, or generated artifact
- shared testdata manifests
- runner gates, lane state machines, readiness, audit event schema, or
  workspace write policy
- docs that define the same operator workflow or architectural boundary

The orchestrator owns merge order. Implementors must preserve unrelated
worktree changes and must not revert dirty files outside their assigned
expected files.

## Agent Staffing

Each atom gets:

- one implementor agent assigned to the atom's exact scope
- one independent reviewer matched to the atom's risk
- extra specialist reviewers only when the atom spans multiple concrete failure
  domains

Match reviewers to risk:

- Incident Root-Cause for behavior, lane, recovery, or completion failures.
- Model Runtime / Config for backend, parser, thinking, context, token, queue,
  or role changes.
- Harness / Go for runner, gates, state machines, queueing, lifecycle,
  manifests, permissions, recovery, or Go control flow.
- Prompt / Protocol for model-visible protocol, malformed output, stale
  anchors, terminal states, or recovery wording.
- TDD / Acceptance for acceptance criteria, product workflow evidence, failing
  tests, and coverage obligations.
- North-Star / Architecture for broad harness behavior, gate relocation, lane
  semantics, strategy-vs-contract boundaries, or repeated related failures.

The final assembled deliverable must go through the full bench when the source
design required full pre-implementation review.

## Status Discipline

Keep a visible checklist or ledger for the effort and update it as work moves:

- `planned`
- `context-ready`
- `assigned`
- `implementing`
- `implemented`
- `under-review`
- `review-blocked`
- `repairing`
- `verification-running`
- `docs-refreshing`
- `final-review`
- `ready-to-commit`
- `committed`
- `pushed`

Do not mark all items done only at the end. Record state transitions
incrementally so a later session can resume from durable state and choose the
next valid action without reading raw transcripts.

## Per-Atom Implementation Loop

For each dispatchable atom:

1. Reconfirm current git status and scoped dirty files.
2. Refresh or validate the atom's context evidence.
3. Send the atom to its implementor with expected files, forbidden files, tests,
   and no-go gates.
4. Run narrow tests first.
5. Send the diff and evidence to the matched reviewer.
6. Repair only reviewer findings that are inside the atom scope.
7. Run the atom's required tests and any audit check named in the atom.
8. Write implementor evidence, reviewer evidence, and post-effort
   investigation.
9. Update the ledger with the next valid action.

If review finds a scope violation, stale context, missing tests, duplicated
authority, privacy leakage, or prompt-only authority, stop that atom and return
it as a blocked or repair atom.

## Whole-Effort Closeout

After all atoms are implemented and reviewed:

1. Reconcile the atom ledger against the source design.
2. Refresh docs touched by the implementation, including roadmap and Obsidian
   notes. Obsidian docs should explain the current architecture, lane behavior,
   operator workflow, and known limitations, not the pre-implementation plan.
3. Run narrow tests for changed packages.
4. Run broad tests appropriate to blast radius. Shared runner, workspace,
   prompt/protocol, lane, audit, or contract changes normally require
   `go test ./...`; entrypoint changes also require `make build` or
   `go build ./cmd/idiom`.
5. Run relevant `idiom tool harness_audit` or behavior replay checks when the
   design expected an audit signal to move.
6. Send the complete diff, evidence, tests, and docs-freshness note to the full
   reviewer bench.
7. Address final reviewer findings or record accepted risks.
8. Commit only after final reviewers pass and docs are fresh.
9. Push only after the commit succeeds and the branch state is clean except for
   intentionally uncommitted local artifacts.

The final commit message should explain the requirement or failure that drove
the effort, the contract or workflow decision, meaningful alternatives avoided,
and verification evidence. Do not squash away evidence that future maintainers
need to distinguish intended design from churn.

## Minimal Checklist

Use this checklist at the top of an effort ledger or orchestration note:

```text
[ ] Full pre-implementation reviewer bench complete or scoped exception recorded
[ ] Reviewer outputs stored durably
[ ] Design revised or accepted risks recorded
[ ] Phases atomized into dispatchable units
[ ] Every atom has expected files, forbidden files, tier, evidence, TDD, tests,
    audit signal, rollback, implementor evidence, reviewer evidence, and
    investigation paths
[ ] Parallel atoms have disjoint write scopes
[ ] Per-atom implementors and reviewers assigned
[ ] Narrow tests pass for each atom
[ ] Required broad tests pass or blockers recorded
[ ] Roadmap and Obsidian docs refreshed
[ ] Full end-to-end reviewer bench complete
[ ] Final docs-freshness and verification record complete
[ ] Commit created
[ ] Branch pushed
```
