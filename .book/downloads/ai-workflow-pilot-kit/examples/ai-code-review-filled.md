# Filled Example: AI Code Review Reviewed Proposal

This example shows an engineering workflow where AI prepares a review packet
but does not merge, deploy, or approve code.

The source references are illustrative internal-source placeholders, not real
public citations, internal records, pull requests, or repository evidence.

## Decision Acceptance Contract

| Field | Answer |
| --- | --- |
| Workflow name | AI-assisted pull request risk review |
| Contract owner | Engineering manager for platform team |
| Business owner after acceptance | Code owner and merge approver |
| Exact decision | Whether a pull request is ready for human code-owner approval. |
| Initial authority mode | reviewed proposal |
| What AI may do before acceptance | Summarize the diff, identify risk areas, cite files and tests, propose review questions, and recommend block/continue. |
| What AI must not do | Approve the pull request, dismiss review comments, merge, deploy, change branch protection, or mark checks required-passing. |

## Claim-To-Evidence Matrix

| Claim ID | Claim | Decision Relevance | Authority-Bearing Source | Source Existence | Source Authority | Source Type | Source Owner | Source Date / Version | Reviewed Date | Freshness Rule | Verification Method | Source Reference / Citation | Exact Claim Supported | Caveat / Limit | Unsupported Stronger Claim | State |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| C1 | The change touches only the intended module. | required | Pull request diff and repository ownership map | illustrative placeholder | authority-bearing | internal primary | Code owner | Current PR head SHA | Review date | Must match reviewed SHA | Compare changed files to ownership map and declared scope. | PR diff and CODEOWNERS | Changed files match declared module scope. | If SHA changes, packet expires. | Change is safe to merge. | verified |
| C2 | Relevant tests passed. | required | CI run for PR head SHA | illustrative placeholder | authority-bearing | internal primary | CI owner | Current PR head SHA | Review date | Must match reviewed SHA and required checks | Verify status, command, and failing/skipped tests. | CI run for reviewed SHA | Required checks passed for reviewed SHA. | Passing checks are evidence, not approval. | Code owner approval is unnecessary. | verified |
| C3 | Migration/backfill risk is addressed. | required when schema changes | Migration files, rollout plan, and owning service docs | illustrative placeholder | authority-bearing | internal primary | Service owner | Current PR head SHA | Review date | Must match reviewed SHA | Inspect migration direction, rollback, and deploy order. | migration files and rollout plan | Migration path can be inspected. | Rollback plan is missing. | Migration is ready for production. | conflicted |
| C4 | Security-sensitive paths were not weakened. | required when auth, secrets, or permissions change | Diff, threat model, security test, ownership rules | illustrative placeholder | authority-bearing | internal official | Security / service owner | Current PR head SHA | Review date | Must match reviewed SHA | Check permission changes and tests. | diff and security ownership rules | Auth-sensitive changes require specialist review. | Needs security reviewer if auth scope changed. | Security impact is resolved. | supported |
| C5 | The review packet cites specific files and lines. | required | PR diff and generated packet | illustrative placeholder | supporting | workflow output | Reviewer | Current packet | Review date | Must match reviewed SHA | Confirm each claim cites a file, test, log, or doc. | packet PR-8421-risk-review | Packet citations are specific enough for review. | Specific citation is not the same as correctness. | The recommendation is accepted. | verified |

## Reviewer Packet

| Field | Answer |
| --- | --- |
| Packet ID | PR-8421-risk-review |
| Proposed action | Continue review but block merge until migration rollback is supplied. |
| Consequence if accepted | Human reviewer focuses on named risks; merge remains gated by code owner and CI. |
| Reviewer | Code owner |
| Allowed decisions | accept packet as review input, accept proposed decision, reject packet, return incomplete, request specialist review, block merge |

### Reviewer Decision

```text
Accept the packet as review input and return incomplete for decision
acceptance. The AI packet correctly found the migration risk, but the pull
request cannot be considered ready until the author adds a rollback or
forward-fix plan and updates tests for the migration path. No approval, merge,
or deployment effect is authorized.
```

## Effect Boundary

| Surface | Allowed Before Acceptance | Allowed After Acceptance | Prohibited | Enforcement |
| --- | --- | --- | --- | --- |
| Repository | Read PR diff, tests, ownership files, and docs. | Same. | Pushing commits unless explicitly requested by author. | Read-only token for review mode. |
| PR review | Draft review notes and risk summary. | Post comment only after reviewer approval. | Approving, dismissing, merging. | Branch protection and token scope. |
| CI | Read check status and logs. | Same. | Re-running or marking checks. | CI read-only permission. |
| Deployment | None. | None. | Triggering deploy or changing flags. | No deploy credentials. |

## Weekly Control Review

| Measure | Current | Threshold | Decision |
| --- | ---: | ---: | --- |
| Generated packets | 45 | 50 | Continue. |
| Complete packets | 41 | 45 | Continue. |
| Accepted verified packets | 33 | 35 | Inspect rejected sample. |
| Returned incomplete | 6 | 8 | Continue. |
| Escaped unsupported decisions | 0 | 0 | Required to continue. |
| Bypass incidents | 2 | 0 | Provide faster draft assistance path for small PRs before raising authority. |

Scale decision: stay in reviewed proposal mode. The workflow is useful, but
bypass pressure shows the sanctioned path is too slow for low-risk pull
requests. Add a draft assistance lane for small, low-risk PRs before considering
comment-posting authority.
