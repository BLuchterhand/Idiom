# Vendor And Tool Evaluation Checklist

Use this when a vendor or internal platform claims a system is safe, governed,
grounded, compliant, autonomous, or production-ready. Require evidence at the
workflow level, not only at the model or platform level.

## Product And Workflow Fit

| Field | Answer |
| --- | --- |
| Vendor / tool |  |
| Evaluated workflow |  |
| Authority mode requested |  |
| Evaluator |  |
| Date |  |

Allowed authority mode values: discovery only / draft assistance / reviewed proposal / bounded action.

## Checklist

| Area | Required Evidence | Workflow Question | Pass / Gap / NA | Notes |
| --- | --- | --- | --- | --- |
| Task-specific evals | Failure cases, regression history, and test data limits | Does this match our workflow? |  |  |
| Retrieval and grounding | Source display, source ownership, and freshness behavior | Can stale or non-authoritative sources be blocked? |  |  |
| Citation behavior | Claim-level citations and source version visibility | Can each decision claim be checked without reconstruction? |  |  |
| Citation honesty | Source type, owner, reviewed date, caveat, and unsupported stronger claim | Can vendor-provided evidence be separated from workflow evidence? |  |  |
| Permissions | Role, data, and tool scopes | Do permissions match business authority? |  |  |
| Human review | Packet, queue, reject, return, escalate, and block states | Can reviewers say no with evidence and time? |  |  |
| Audit | Prompt, source, output, action, reviewer, acceptance state | Can we reconstruct what happened? |  |  |
| Incident response | Rollback, correction, alerting, disable path | Who owns recovery? |  |  |
| Change management | Model, prompt, corpus, and connector-change notice | When does old evidence expire? |  |  |
| Data handling | Retention, training use, residency, and deletion terms | Are source and customer constraints preserved? |  |  |
| Integration limits | Rate limits, failure states, and fallback behavior | Does the workflow fail closed? |  |  |

Allowed checklist status values: pass / gap / NA.

## Decision

```text
[ ] Approve for discovery only
[ ] Approve for draft assistance
[ ] Approve for reviewed proposal pilot
[ ] Approve for bounded action pilot
[ ] Reject for this workflow
```

Blocking gaps:

```text

```

Evidence required before next review:

```text

```

Vendor documentation, demos, benchmarks, and eval summaries can support a tool
evaluation. They do not establish that a specific workflow claim is true, current,
authorized, or safe to accept.
