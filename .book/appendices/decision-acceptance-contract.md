# Appendix A: Decision Acceptance Contract

The decision acceptance contract defines when AI output can move from proposal
to accepted work.

## Minimum Viable Contract

| Field | Answer |
| --- | --- |
| One decision | What exact decision may be accepted? |
| Controlling source map | Which source or sources control the highest-risk claims? |
| Action limit before acceptance | What may the AI output do before acceptance? |
| One checker | Who or what verifies the controlling claim? |
| One stop condition | What forces the workflow to block, throttle, or refuse automation? |
| One owner | Who accepts the decision after the workflow has done its checks? |

Everything else is an escalation tier.

## Full Contract

```text
Workflow:
Authority mode: discovery only / draft assistance / reviewed proposal / bounded action
What the output may do before acceptance:
What the output must not do before acceptance:

Authority-bearing source map:
Required reviewer:
Reviewer capacity:
Accepted state:
Blocked state:
Escalation path:
Stop condition:
Audit record:
Owner after acceptance:

Failure states:
- Missing source:
- Stale source:
- Source conflict:
- Review capacity exceeded:
- Required owner unavailable:
- Unsupported action requested:
```

The contract is useful only if its fields can change workflow behavior. A field
that cannot block, route, require, log, throttle, escalate, retire, or stop work
is documentation, not a control.
