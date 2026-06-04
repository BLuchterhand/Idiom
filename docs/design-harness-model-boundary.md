# Harness Model Boundary Design And Runbook

**North Star: AI coding agents are becoming common, but teams do not yet trust
their context, decisions, or outputs. Idiom sells the missing substrate: repo
memory, freshness, contracts, audit, and evals.**

Status: reviewer-approved design intent for observe-only rollout; enforcement
is a later phase gate.

## Purpose

This document defines the implementation and operations intent for keeping the
model/harness boundary auditable without turning the harness into a strategy
engine.

The boundary is:

- The model owns task strategy, synthesis, investigation order, and whether the
  evidence is sufficient to answer or continue.
- The harness owns contracts: permissions, protected paths, typed lane
  protocols, source and verification obligations, completion gates, trace
  minimization, replay, audit, and rollback controls.

The first rollout is observe-only. New boundary checks must record what would
have happened, classify the trajectory, and expose safe recovery context. They
must not block a run until the phase gates in this document are met and a
reviewer approves enforcement.

## Design Constraints

- Do not use prompt wording as a contract trigger. Runtime mode, typed route
  metadata, lane contracts, and gate decisions are the contract sources.
- Do not prescribe the model's next tool or strategy step except when enforcing
  an existing hard invariant such as permission, protected path, protocol, or
  verification safety.
- Do not create tool-specific policy language when a capability or contract
  term is enough.
- Do not store raw prompts, raw transcripts, file bodies, command output bodies,
  secrets, private repository URLs, or customer identifiers in audit or product
  evidence.
- Keep new trace and replay data bounded, redacted, and attributable by IDs,
  counts, hashes, relative paths, statuses, reason codes, and command labels.

## Boundary Events

Every candidate boundary decision should be represented as a typed event that
can be replayed without invoking a model. The event is evidence for audit and
phase gating, not a strategy instruction.

Required fields:

- `schema_version`
- `session_id`
- `lane_id` or `run_id`
- `contract_id`
- `pack_id`
- `invariant_id`
- `enforcement_mode`
- `decision`
- `reason_code`
- `target_refs`
- `observed_facts`
- `safe_alternatives` when a future enforced decision would block, reject, deny,
  or stop
- `redaction_status`
- `trace_ref`

`enforcement_mode` starts as `observe_only` for all new boundary checks.
Existing hard gates keep their existing behavior and may still emit boundary
events with `enforcement_mode:"enforce"`.

`observed_facts` are structured labels only. Allowed facts include counts,
statuses, path refs, obligation refs, command labels, policy codes, gate names,
pack IDs, invariant IDs, and protocol hashes. Forbidden facts include raw
prompt text, raw transcript text, raw command output, raw file bodies, patches,
secrets, and identity-bearing customer data.

## Replay Classifications

Replay should classify whether trajectories can be reconstructed from typed
contract evidence. These classifications are harness facts, not judgments about
model capability.

| Classification | Meaning | Initial action |
| --- | --- | --- |
| `full` | Required event payloads, hashes/refs, obligations, verification status, and explicit trace policy are present without redaction loss. | Count as fully replayable evidence. |
| `redacted` | Required signals are present and privacy-safe, but one or more fields are intentionally redacted. | Count as replayable; preserve redaction markers. |
| `partial` | Typed events exist but required replay signals are missing. | Improve event coverage; do not infer from prose. |
| `non_replayable` | Events are missing, malformed, or contain forbidden raw content. | Exclude from thresholds until regenerated or repaired. |

Replay results should keep deterministic boundary findings separate from broader
behavior-audit findings. Audit can report broader friction, but enforcement
promotion depends on replayable typed events.

Boundary findings such as `would_block`, `false_positive`,
`missed_violation`, and safe-alternative coverage are metrics derived from
replayable trajectories. They are not replay classifications.

## Trace Store Controls

Trace storage is local by default and must remain safe to export only after
redaction checks pass.

Required controls:

- Trace records use stable IDs, hashes, relative paths, counts, statuses, and
  reason codes instead of raw bodies.
- Redaction runs before trace persistence when the event contains model-facing
  text, tool arguments, command labels, or path metadata.
- Exported evidence includes `redaction_status:"pass"` and omits absolute
  local paths unless the export target explicitly permits them.
- Trace refs point to bounded event spans, not whole conversation logs.
- Full debug logs remain local diagnostic input and are not product or release
  evidence by default.
- Retention settings are explicit for telemetry, conversation/debug logs,
  replay fixtures, and product evidence exports.

Retention expectations:

| Artifact class | Default retention intent | Exportable by default |
| --- | --- | --- |
| Process telemetry | Metadata-only local records; keep long enough for audit trend baselines. | Yes, after redaction pass. |
| Boundary trace events | Bounded replay records tied to session/run IDs. | Yes, after redaction pass. |
| Conversation/debug logs | Redacted local debugging evidence; full mode only by explicit operator choice. | No. |
| Replay fixtures | Synthetic or redacted event trajectories. | Yes. |
| Product/release evidence | Metadata-only summaries with check IDs and limitations. | Yes. |

Retention implementation must include a cleanup path and an audit summary that
reports retained counts by class, oldest retained timestamp, redaction status,
and any skipped cleanup reason.

## Privacy Rules

Privacy failures block promotion regardless of replay quality.

Forbidden in trace, audit, replay fixture, product evidence, and release
evidence outputs:

- raw prompts
- raw transcripts
- raw file bodies
- raw command output bodies
- patch bodies
- secrets or secret-like tokens
- private repository URLs
- account numbers, patient names, personal identifiers, or customer names
- unredacted absolute local paths when the artifact leaves local diagnostics

Allowed evidence shapes:

- relative paths when needed for reproduction
- file, command, or event hashes
- path role labels
- command labels and exit statuses
- line counts, changed-file counts, retry counts, and gate counts
- pack IDs, invariant IDs, reason codes, and obligation refs
- redacted excerpts only when the receiving artifact explicitly permits
  bounded excerpts

If a classifier cannot decide whether evidence is safe, it must return
`partial` or `non_replayable`, not pass by assumption.

## Phase Gates

Promotion is staged. A phase may advance only when the current phase has enough
typed evidence and no blocking privacy issue.

### Phase 0: Design And Wiring

Scope: documentation, schemas, and integration points.

Exit thresholds:

- reviewer approves the model/harness authority split;
- every candidate check has a named `pack_id`, `invariant_id`,
  `reason_code`, and owner;
- trace fields are bounded and redaction rules are defined;
- rollback control is documented before any enforcement path exists.

### Phase 1: Observe-Only Event Capture

Scope: emit boundary events for candidate checks without changing runtime
behavior.

Exit thresholds:

- at least 30 replay trajectories include the new event shape;
- at least 20 live or recorded lane sessions include boundary events;
- `partial` replay is at most 5% of candidate boundary events;
- `non_replayable` is 0 for exportable replay and audit evidence;
- every `would_block` event has a `safe_alternatives` entry or an explicit
  reason why no alternative is valid;
- existing hard gates show no behavior regression in focused tests.

### Phase 2: Audit And Replay Baseline

Scope: summarize candidate rule quality and establish false-positive trends.

Exit thresholds:

- false positives are at most 1% of candidate `would_block` decisions and no
  more than 1 finding in the latest 100 candidate decisions;
- missed violations are 0 for promoted replay fixtures;
- candidate rules fire on at least one meaningful fixture or are marked
  dormant and kept out of enforcement;
- replay classifications are stable across two consecutive baseline runs;
- audit reports coverage by `pack_id`, `enforcement_mode`, `decision`, and
  redaction status.

### Phase 3: Limited Enforcement Proposal

Scope: propose enforcement for a narrow rule that has passed observe-only
baselines.

Exit thresholds:

- reviewer approves the specific invariant, not only the pack;
- rollback switch has been exercised in a rehearsal;
- enforcement candidate has a valid trajectory fixture, a would-block fixture,
  and a privacy rejection fixture;
- false-positive review notes are attached to the invariant;
- churn metrics stay below the thresholds in this document for the same
  observation window.

### Phase 4: Enforcement

Scope: enable a narrow invariant in enforcement mode.

Entry rule:

- enforcement is opt-in or explicitly configured for the affected lane/profile;
- existing safety gates are not weakened by the override mechanism;
- audit can distinguish enforced blocks from observe-only would-blocks.

Exit expectation:

- production or release evidence records enforced decisions by invariant,
  rollback state, and redaction status without raw content.

## Churn Metrics

Churn metrics protect the boundary from being repaired by duplicated prompt
text or moving authority between layers without evidence.

Track during Phase 1 and later:

- `prompt_authority_churn`: prompt/runtime brief edits that add or restate a
  contract already owned by a typed gate.
- `event_schema_churn`: boundary event field additions, removals, or semantic
  changes.
- `rule_churn`: candidate invariant reason-code or matching logic changes.
- `trace_churn`: changes in retained trace volume per session.
- `replay_churn`: replay fixture rewrites not caused by intentional contract
  changes.
- `false_positive_churn`: repeated fix/revert cycles for the same invariant.

Promotion thresholds:

- no more than one schema semantic change in the latest promotion window;
- no repeated prompt-authority edits for the same invariant;
- trace volume increase stays under 25% per comparable lane session unless a
  reviewer accepts the added fields as required for classification;
- replay fixture changes are tied to explicit contract changes or redaction
  repairs;
- no unresolved false-positive churn for the invariant being promoted.

## Rollback Runbook

Rollback must be available before enforcement is enabled.

Primary rollback control:

- Provide one operator-visible control that forces all candidate boundary rules
  back to `observe_only` while leaving existing hard safety gates intact.

Required behavior:

- The rollback control must not disable permission checks, protected-path
  checks, protocol correctness, or existing verification safety gates.
- The rollback event must be recorded as a typed `rollback_control`
  `model_exchange` event with operator/source, timestamp, previous mode, new
  mode, affected pack IDs or guard refs, and reason.
- Audit must report rollback-active state and distinguish it from normal
  observe-only rollout.
- Replay must continue to classify typed trajectories while rollback is active.

Rollback procedure:

1. Enable the rollback control for the affected lane/profile or globally if the
   failure scope is unclear.
2. Re-run the failing trajectory or nearest replay fixture and confirm the
   candidate invariant records `decision:"observe_only"` instead of blocking.
3. Confirm existing hard safety gates still block invalid permission,
   protected-path, protocol, or verification trajectories.
4. Record the rollback reason, affected pack IDs, observed false-positive or
   missed-violation class, and follow-up owner.
5. Keep enforcement disabled until replay and privacy gates pass again and a
   reviewer approves re-enablement.

Rollback evidence must use the same privacy rules as all other trace evidence.

## Test And Audit Expectations

Every implementation slice that touches this boundary should include focused
tests or fixtures for:

- valid trajectory;
- observe-only would-block trajectory;
- false-positive classification or reviewer override path;
- insufficient trace classification;
- privacy rejection for forbidden raw content;
- rollback switch behavior;
- audit summary by pack, invariant, mode, decision, and redaction status.

Audit output must answer:

- Which packs and invariants fired?
- Which decisions were enforced and which were observe-only?
- Which traces were export-safe?
- Which events lacked enough data for replay?
- Which rules are dormant, noisy, or repeatedly changed?
- Was rollback active?

Do not use raw transcripts or file bodies as audit proof. If raw logs are
needed for local debugging, cite the redacted event span or trace ref in the
shareable artifact.

## Reviewer Approval Note

Reviewer approval for this design is conditional on the constraints above:

- observe-only rollout happens first;
- enforcement waits for explicit phase-gate approval;
- rollback and trace controls are documented before enforcement;
- privacy failures block promotion;
- churn metrics are considered before a rule is promoted;
- replay classifications remain deterministic and model-free;
- the model/harness authority split is preserved.

Any implementation that turns prompt language, context packet prose, tool names,
or audit summaries into hidden execution authority needs a new review before it
ships.
