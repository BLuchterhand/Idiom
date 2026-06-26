# AI Workflow Pilot Operating Guide

Use this guide after the workshop. The default pilot runs for three weeks after
Week 0 setup.

Before launch, fill `03-pilot-runbook.md` and set thresholds using
`04-threshold-setting-guide.md`.

## Week 0: Set The Boundary

Required outputs:

- decision acceptance contract;
- source map for highest-risk claims;
- reviewer packet;
- review-capacity scorecard;
- effect-boundary spec;
- bypass check;
- stop/go owner.

Stop before launch if:

- no source owner exists for the highest-risk claim;
- no reviewer has protected rejection authority;
- review capacity is unknown;
- the tool can send, update, apply, route, or trigger before acceptance;
- the team cannot log sources, claims, reviewer action, acceptance state, and effect.

## Week 1: Run Reviewed Proposals Only

Allowed:

- draft summaries;
- claim extraction;
- source collection;
- reviewer packet preparation;
- internal recommendation for review.

Prohibited:

- customer commitment;
- production change;
- record mutation implying approval;
- payment, credit, entitlement, filing, or external communication;
- bypassing reviewer action.

Measure:

- generated proposals;
- complete packets;
- incomplete packets;
- reviewer-hours used;
- accepted verified output;
- rejected, returned, escalated, and blocked packets.

## Week 2: Capacity And Bypass Review

Review:

- complete-packet rate;
- average review time;
- maximum well-reviewed packets;
- review debt;
- private-tool or side-channel use;
- stale-source behavior;
- source-owner delays.

Decisions:

- continue if capacity is under threshold and no unsupported decision escaped;
- narrow if the workflow is useful for a smaller class of cases;
- throttle if proposal volume exceeds review capacity;
- escalate if source ownership or authority conflict blocks review;
- stop if unsupported work reached authority.

Recommend an authority change only after completing
`templates/authority-pace-checklist.md`.

## Week 3: Stop/Go Decision

Use `templates/authority-pace-checklist.md`.

Possible outcomes:

| Decision | Meaning |
| --- | --- |
| Continue | Reviewed proposals meet evidence and capacity thresholds. |
| Narrow | Only a smaller scope has enough source and review support. |
| Throttle | AI output volume exceeds funded review capacity. |
| Escalate | Source ownership, authority, or budget needs executive resolution. |
| Stop | Unsupported decisions escaped, stale evidence passed, or review failed. |

## Scale Only If

- accepted verified output is measurable;
- incomplete packets are rejected before review;
- review debt remains below threshold;
- stale-source drills block old but plausible evidence;
- bypass pressure has a sanctioned lower-authority path;
- incident review changes workflow controls, not only prompts;
- the owner after acceptance has evidence, authority, and correction path.

Do not scale because generated output volume increased.
