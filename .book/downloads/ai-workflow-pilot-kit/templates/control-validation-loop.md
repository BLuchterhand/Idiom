# Control-Validation Loop

Use this weekly during the pilot. Controls are useful only if they change
workflow behavior when evidence, permission, or review is not sufficient.

## Weekly Review

| Field | Answer |
| --- | --- |
| Workflow |  |
| Week |  |
| Review date |  |
| Participants |  |
| Decision |  |

Allowed decisions: continue / narrow / throttle / escalate / stop / recommend
authority change.

## Operating Measures

| Measure | Current | Threshold | Decision |
| --- | ---: | ---: | --- |
| Generated packets |  |  |  |
| Complete packets |  |  |  |
| Accepted verified packets |  |  |  |
| Returned incomplete |  |  |  |
| Rejected |  |  |  |
| Escalated |  |  |  |
| Review debt |  |  |  |
| Escaped unsupported decisions |  | 0 |  |
| Bypass incidents |  | 0 |  |

## Control Checks

| Check | Question | Finding | Required Action |
| --- | --- | --- | --- |
| Accepted-decision sampling | Did accepted work have current evidence and proper authority? |  |  |
| Rejected-decision review | Did the workflow reject useful work for the wrong reason? |  |  |
| Stale-source drill | Does old but plausible evidence block? |  |  |
| Reviewer calibration | Do reviewers apply the same acceptance standard? |  |  |
| Bypass detection | Are teams using private tools or side channels? |  |  |
| Escaped-error review | What unsupported work reached authority? |  |  |
| Control retirement | Did a control ever change acceptance? |  |  |

## Decision Record

```text
Decision:
Evidence:
Change before next review:
Owner:
Due date:
```

An authority recommendation does not grant authority. Complete
`authority-pace-checklist.md` before the workflow increases authority or
volume.
