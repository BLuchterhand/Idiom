# Decision Acceptance Contract

Use this before AI output can become accepted work. Keep one contract per
workflow. If a workflow has two materially different decisions, split it.

## Metadata

| Field | Answer |
| --- | --- |
| Workflow name |  |
| Contract owner |  |
| Business owner after acceptance |  |
| Pilot dates |  |
| Version |  |
| Review date |  |

## Decision

| Field | Answer |
| --- | --- |
| Exact decision or accepted work state |  |
| Decision consequence |  |
| Initial authority mode |  |
| What AI may do before acceptance |  |
| What AI must not change, send, update, route, apply, trigger, or approve |  |

Allowed decision consequence values: customer / money / record / production / legal / internal-only.

Allowed authority mode values: discovery only / draft assistance / reviewed proposal / bounded action.

## Source And Verification

| Claim Type | Controlling Source | Source Type | Freshness Rule | Checker | Required State |
| --- | --- | --- | --- | --- | --- |
|  |  |  |  |  |  |
|  |  |  |  |  |  |
|  |  |  |  |  |  |

Allowed source type values: primary / official / internal / vendor / background.

Allowed required state values: missing / stale / supported / conflicted / verified.

Source rule: a citation is not enough by itself. The packet must show the
source, the claim it supports, the freshness status, and the checker that
accepted or rejected it.

Do not use background context as an authority-bearing source unless the source
owner also controls the decision claim.

## Acceptance

| Field | Answer |
| --- | --- |
| Accepted state |  |
| Blocked state |  |
| Allowed reviewer actions |  |
| Rejection rule |  |
| Escalation path |  |
| Audit record |  |

Allowed reviewer action values: accept packet as review input / accept proposed decision / authorize effect / reject / return incomplete / escalate / block.

## Failure States

| Failure State | Required Behavior | Owner |
| --- | --- | --- |
| Missing source |  |  |
| Stale source |  |  |
| Source conflict |  |  |
| Review capacity exceeded |  |  |
| Required owner unavailable |  |  |
| Unsupported action requested |  |  |
| Bypass detected |  |  |

Allowed required behavior values include return incomplete / block / escalate / throttle / narrow / stop / delegate formally / reject / incident / investigate / provide lower-authority path.

## Scale Gate

The workflow may increase authority or volume only when all are true:

- accepted work has current evidence for the highest-risk claims;
- reviewers have funded time and can reject without informal override;
- incomplete packets are rejected or returned without manual reconstruction;
- stale-source and source-conflict tests block as designed;
- no unsupported action reached the accepted state during the review window.
