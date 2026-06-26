# Filled Example: Draft Assistance Without Review Burden

This example shows a low-authority workflow where AI helps produce internal
drafts but the team deliberately avoids creating a reviewed proposal queue.
It is illustrative and does not document a specific release workflow.
All source names in this example are illustrative placeholders, not public
citations or real internal records.

## Decision Acceptance Contract

| Field | Answer |
| --- | --- |
| Workflow name | Internal release-note draft assistance |
| Contract owner | Engineering manager |
| Business owner after acceptance | Release owner |
| Exact decision | None. The AI output is draft text only. |
| Initial authority mode | draft assistance |
| What AI may do before acceptance | Summarize merged pull requests and draft internal release notes. |
| What AI must not do | Publish release notes, notify customers, change release status, or mark risks resolved. |

## Source And Verification

| Claim Type | Controlling Source | Source Existence | Source Authority | Source Type | Freshness Rule | Checker | Required State |
| --- | --- | --- | --- | --- | --- | --- | --- |
| Merged changes | Pull request list for release branch | illustrative placeholder | supporting | internal primary | Current release branch SHA | Release owner | supported |
| Customer-facing impact | Product manager review | illustrative placeholder | supporting | internal official | Current release plan | Product manager | not required for draft |
| Known risks | Release checklist | illustrative placeholder | supporting | internal primary | Current release checklist | Release owner | supported |

## Why Draft Assistance Is Enough

- The output does not leave the internal draft assistance surface.
- The release owner already has an existing publication workflow.
- Review capacity is not funded for a separate AI proposal queue.
- The main risk is misleading draft prose, so the control is source display and
  prohibited-effect enforcement.

## Effect Boundary

| Surface | Allowed Before Acceptance | Prohibited | Enforcement |
| --- | --- | --- | --- |
| Repository | Read merged PR titles and release labels. | Pushing commits. | Read-only access. |
| Release notes | Draft internal notes. | Publishing notes. | No publish permission. |
| Customer communication | None. | Sending customer updates. | No messaging permission. |
| Release status | None. | Changing status. | No release-management write role. |

## Decision

```text
Continue as draft assistance. Do not create a reviewed proposal pilot unless
the workflow begins recommending customer-facing release statements or release
go/no-go decisions.
```
