# Subagent Context Management Review Evidence

**Status:** design review complete; implementation review in progress,
2026-06-12

This file records the requested two review rounds over
`docs/design-subagent-context-management.md`.

## Inputs

- Draft: `docs/design-subagent-context-management.md`
- Local discovery packet: `.idiom/comprehension/views/4ba39bf38877010b.json`
- Additional reviewer packet: `.idiom/comprehension/views/6be6d1b4477e9b6b.json`
- Actual Idiom investigation session: `20260611-212729.436`
- Reviewer bench: `docs/debugging-reviewer-bench.md`
- Prior intent anchor: `docs/subagent-lane-contracts-reviewed-intent.md`

## Round 1 Summary

All seven codified reviewer lenses returned `SIGN-OFF: no` on the first draft.
The objections were consistent and were addressed in the revised design.

| Reviewer | Blocking issue | Resolution in revised design |
| --- | --- | --- |
| Incident Root-Cause | The draft cited mislocalization but did not name the first broken invariant: delegated scope rewrite and acceptance must preserve objective scope or block readiness. | Added `Root Cause` section, objective-aligned evidence requirement, failure mode, and assistance acceptance criteria. |
| Model Runtime / Config | Parent-child record lacked full runtime envelope and per-role backend/capacity policy. | Added `runtime_envelope` with backend, transport, parser, native tool, budget, schema hash, and timeout fields. |
| Harness / Go | `parent_child_contract` duplicated `AutonomyContract`; `delegate_edit` and workbranch had different semantics; v1 parallel writes were premature. | Made `AutonomyContract` canonical, changed parent-child record to lifecycle wrapper, split executor semantics, serialized v1 write children. |
| Prompt / Protocol | Terminal outcomes and source-body obligations were too conceptual; read-only readiness risked becoming hard-gated. | Added `Child Terminal Record`, `Model-Facing Protocol`, source-satisfied/blocker result kinds, and direct-answer advisory readiness language. |
| TDD / Acceptance | The draft lacked deterministic acceptance criteria and event-level oracles. | Added `Acceptance Criteria`, mutation/assistance happy paths, negative oracles, audit oracles, and expanded verification list. |
| North-Star / Architecture | First slice was too broad and risked a generic scheduler; topic-specific branches must not be preserved. | Narrowed rollout to lifecycle records around existing paths; deferred role expansion/parallel writes; added migration note against topic-specific branches. |
| Model-Agnostic / Cross-Backend | Backend enum and runtime identity were wrong/too thin; sidecar extension path was underspecified. | Switched to current backend kinds, added runtime/protocol envelope, parser status, and sidecar-compatible child metadata. |

## Round 2

The first round-2 batch reviewed the revised design after the round-1 fixes.
Two reviewers signed off and two found contract-level blockers. The blockers
were folded into the design before dispatching the remaining/follow-up reviews.

| Reviewer | Sign-off | Finding | Resolution |
| --- | --- | --- | --- |
| Incident Root-Cause | yes | The revised root cause matched the live log: delegated scope rewrite plus weak parent acceptance let off-objective evidence become readiness. | No change required. |
| Prompt / Protocol | yes | Terminal outcomes and source-body obligations were now concrete enough for model-facing protocol work. | No change required. |
| Model Runtime / Config | no | Runtime envelope still used prose config paths for alias source, missed capacity admission, and did not fully specify actor-change details. | Replaced prose alias source with typed `RoleAliasSource` values, added `capacity_admission`, and expanded `runtime_actor_changed` fields. |
| Harness / Go | no | The child contract shape allowed two incompatible implementations; candidate patch artifacts were not structured enough; `delegate_edit` acceptance semantics were too loose. | Chose `InvocationKind:"child"` only, added `idiom.candidate_patch_artifact.v1`, clarified `delegate_edit` as applied parent edit with `recorded_only`/verification-gate acceptance only. |

The remaining round-2 reviewers then reviewed the blocker-fixed draft:

| Reviewer | Sign-off | Finding | Resolution |
| --- | --- | --- | --- |
| TDD / Acceptance | no, then yes on follow-up | Objective alignment was still a label rather than a deterministic predicate; verification closure lacked a concrete event shape; lifecycle audit missed invocation-without-terminal. | Added `alignment_basis`, `idiom.verification_acceptance.v1`, invocation-without-terminal audit, acceptance referential-integrity audit, and negative tests for incomplete objective alignment. |
| North-Star / Architecture | yes | No blockers. Suggested wording to avoid release-arbiter role drift, clarify answer influence, shadow objective-alignment enforcement, and clarify synthesized acceptance wording. | Applied the wording clarifications. |
| Model-Agnostic / Cross-Backend | yes | No blockers. Suggested sidecar child-context schema version, backend-kind matrix, normalized transport values, hosted/client capacity semantics, and normalizer status. | Applied the compatibility clarifications. |
| Harness / Go follow-up | yes | The first follow-up found one remaining blocker: `delegate_edit` route/path-gate/plan-authority metadata was prose-only. | Added structured `execution_metadata`, `parent_plan_authority_ref`, `path_gate_ref`, `path_gate_decision`, and `route` fields to `child_terminal` and `child_acceptance`; final follow-up signed off. |

All seven reviewer lenses completed two rounds. Follow-up reviews were run for
each lens whose round-2 feedback caused a material contract change.

## Implementation Review

After implementation began, an Idiom reviewer ran a patch-level contract review
over the lifecycle, audit, sidecar, runtime, and plan-execute changes. The first
implementation review signed off on focused package verification but found
blockers. The blockers were fixed before round-2 implementation review:

| Finding | Resolution |
| --- | --- |
| Read-only child objective alignment treated any successful task result as accepted. | Read-only lifecycle acceptance now requires a typed source-body evidence packet, no gaps, medium/high confidence, and stricter objective-source ownership for implementation-shape questions. Weak packets produce repair-required acceptance without accepted evidence refs. |
| `harness_audit` checked only that lifecycle IDs existed somewhere. | Audit now validates terminal-to-invocation and acceptance-to-terminal/invocation parent/child contract links and reports `child_lifecycle_mismatched_link`. |
| Plan-execute invalid lifecycle events were emitted as `<event>_invalid` but ignored by audit. | Audit now recognizes invalid `idiom.plan_execute.*_invalid` lifecycle/verification events and reports them as invalid lifecycle records. |
| Child tool request/result events lacked durable lifecycle correlation. | Child tool request/result run events now carry `child_invocation_id` and `child_contract_id`. |
| Sidecar `child_context` did not validate schema or redaction safety. | The sidecar builder forces `idiom.sidecar_child_context.v1`, validates every child-context field with the existing opaque-ID safety guard, and omits unsafe optional child context metadata. |

The second implementation review found additional patch-level gaps, which were
also fixed:

| Finding | Resolution |
| --- | --- |
| Plan-execute readiness non-submit rejection could run before missing-target read recovery. | Missing-target read redirection now runs before readiness non-submit rejection, preserving `candidate_read_missing_target` recovery. |
| Workbranch child tool request/result events still lacked lifecycle IDs. | Workbranch child callbacks now populate `child_invocation_id` and `child_contract_id`, with a workbranch regression test. |
| Candidate patch artifact audit did not verify artifact ownership. | Audit now verifies candidate patch artifact `invocation_id` and `child_contract_id` against the referencing terminal. |
| Verification acceptance audit proved schema shape but not that a command/result event existed. | Audit now correlates `verification_acceptance` command/result refs with sibling `idiom.plan_execute.verification` events and reports missing result pairs. |
| Allowed-candidate post-readiness rejection could run before the post-completion-gate out-of-scope recovery. | Post-gate out-of-scope candidate rejection now runs before the generic allowed-candidate rejection inside the same guard. |
