# Threshold-Setting Guide

Use this guide when the templates ask for thresholds. Thresholds should be
small enough to change behavior during the pilot.

## Default Thresholds

| Measure | Default Threshold | Required Action |
| --- | ---: | --- |
| Escaped unsupported decisions | 0 | Stop, roll back, or lower authority. |
| Stale-source passes | 0 | Block workflow and repair freshness rule. |
| Unsupported action attempts | 0 | Deny action and review permission boundary. |
| Source conflicts hidden in accepted packets | 0 | Stop acceptance until escalation path works. |
| Review debt before increasing authority | 0 | Do not increase authority or volume. |
| Bypass incidents before increasing authority | 0 | Provide a sanctioned lower-authority path or fix throughput. |
| Reviewer override without audit note | 0 | Stop or require escalation-owner approval. |

## Capacity Thresholds

Start with these formulas:

```text
complete packets per week =
  expected proposals per week * complete-packet rate

maximum well-reviewed packets per week =
  funded reviewer-hours per week / average review time per complete packet

review debt =
  max(0, complete packets per week - maximum well-reviewed packets per week)
```

Set the volume threshold at or below maximum well-reviewed packets. If review
debt is greater than zero, choose one of three actions: throttle proposal
volume, narrow the workflow, or add funded reviewer time.

## Completeness Thresholds

For a reviewed proposal pilot, use these starting thresholds unless the team has
a documented reason to choose stricter values:

| Measure | Starting Threshold | Reason |
| --- | ---: | --- |
| Complete-packet rate | 80% | Below this, reviewers spend too much time reconstructing evidence. |
| Accepted-decision sample rate | 10 packets or 20%, whichever is larger | Sampling must catch stale evidence and unsupported claims. |
| Clean review windows before authority increase | 2 consecutive weekly reviews | One good week can be luck or suppressed volume. |
| Maximum review queue age | One review SLA | Old packets invite bypass and stale evidence. |

## Authority-Increase Rule

A workflow may recommend higher authority only when all are true:

- escaped unsupported decisions are zero;
- stale-source passes are zero;
- review debt is zero;
- bypass incidents are zero or have a sanctioned lower-authority path;
- accepted-decision sampling found current evidence and proper owner handoff;
- two consecutive review windows passed the scale gate;
- the authority-pace checklist is complete.

The weekly control review may recommend an authority change. It does not grant
one by itself.

## When To Lower Authority

Move the workflow down one authority mode or stop it when:

- reviewers cannot meet the SLA with funded time;
- packet completeness stays below threshold for two review windows;
- a prohibited effect cannot be technically denied or monitored;
- source ownership remains unresolved;
- unsupported work reaches customer, money, record, production, legal, or other
  authority-bearing state.
