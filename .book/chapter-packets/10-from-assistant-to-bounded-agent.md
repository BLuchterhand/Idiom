# Chapter 10 Packet: From Assistant To Bounded Agent

## Planning Status

This packet is a planning record. Current manuscript readiness is tracked in
`publication-readiness-tracker.md`.

Draft with caveat.

Security and lifecycle sources support bounded action. Shadow AI and bypass
patterns should be framed as plausible pressure patterns to test, not as
measured universal adoption behavior.

## Chapter Claim

Move from assistance to agency only when the workflow has scoped tools,
explicit permissions, valid failure states, sanctioned alternatives, and
post-action verification.

## Reader Starting Point

The reader may see "agent" as a capability upgrade rather than an authority
change.

## First Visible Moment

The official support workflow is too slow, so teams use spreadsheets, side Slack
approvals, copied docs, unofficial templates, or private AI tools to keep work
moving.

Scene beats:

1. Nobody starts by trying to break policy; they are trying to answer a
   customer before the official path catches up.
2. A private AI draft uses copied ticket notes and a stale template because it
   is faster than the sanctioned workflow.
3. Security sees data leaving the approved path; finance sees decisions forming
   outside the credit workflow.
4. The team treats bypass as a pressure signal and creates a sanctioned
   draft assistance path.
5. Agency is deferred until logging, boundaries, rollback, and post-action
   verification exist.

## Workplace Behavior Parallel

Shadow processes existed before AI. Teams bypass slow systems when official
paths cannot meet demand.

## Human Fallibility Pattern

People create local workarounds when the sanctioned process is slower than the
work requires.

## Existing Workplace Contract

Organizations manage bypass through sanctioned fast paths, exception logging,
tool rationalization, retirement of duplicates, and escalation when official
paths cannot meet demand.

## AI Analogue

Private AI tools and unofficial agents can move work outside evidence, logging,
security, and acceptance controls.

## Analogy Break

AI tools can ingest sensitive context, generate customer-facing language, and
take tool-mediated actions at a scale old spreadsheets could not.

## Operator Artifact

Tool inventory and authority tiers:

| Tool/Path | Data Class | Allowed Use | Effect Authority | Logging |
| --- | --- | --- | --- | --- |
| Chat assistant | Low/internal | Draft/summarize | None | Optional |
| Official support AI | Ticket/contract | Reviewed proposal | No direct credit | Required |
| Credit agent | Restricted | Bounded action | Threshold-limited | Required |
| Private tool | Unknown | Not sanctioned | None | Missing |

Artifact validation:

| Field | Rule |
| --- | --- |
| Owner | Security/governance owns sanctioned tool inventory; workflow owners own allowed use and effect authority. |
| Source authority | Data-class rules, procurement/security records, tool logs, and incident/bypass findings. |
| Update cadence | Review after new tools, procurement changes, bypass discoveries, or expanded agency. |
| Failure signal | Teams use unofficial tools because the sanctioned path cannot meet demand. |
| Operational hook | Sanction a low-friction path, retire duplicate tools, block unsanctioned effects, or reduce agency. |
| Escalation/retirement | Provide a low-friction sanctioned path, retire duplicate paths, or reduce agency until logging and rollback exist. |

## Claim Gate

| Claim Type | Drafting Rule |
| --- | --- |
| Sourced empirical claim | Use security, shadow IT, and lifecycle sources where available. |
| Normative control standard | Agency requires scoped tools, explicit permissions, failure states, sanctioned alternatives, and post-action verification. |
| Hypothesis/composite scenario | Shadow-AI and bypass examples are pressure patterns to test unless sourced. |

## Evidence Status

Sources: **X1**, **X2**, **X3**, **A10**, **H1**, **H4**, **G1**, **G2**,
**G3**, **D3**, **D4**, **D7**.

Gaps: shadow AI/shadow IT evidence, bypass loops, procurement/DLP signals, tool
sprawl evidence.

## Main Sections

1. **The bypass path**: show unofficial workarounds.
2. **Assistance versus agency**: read, draft, propose, mutate.
3. **Authority tiers**: sanctioned low-risk assistant path and stricter agent
   path.
4. **Tool inventory**: data class, allowed use, effect boundary, logging.
5. **Recovery and rollback**: valid failure states and post-action checks.

## Practical Takeaway

Do not ban all unofficial use without providing sanctioned low-friction paths.
Do not grant agency without effect boundaries and verification.

## Possible Objections

- "Shadow AI is misconduct": sometimes; often it is also a pressure signal.
- "Agents need autonomy": autonomy still needs scoped tools and rollback.
- "Logging feels punitive": logging should support learning and audit, not
  surveillance theater.

## Bridge

Chapter 10 defines the failure and recovery path for agentic work. Chapter 11
shows how failures, near-misses, evals, and governance improve the workflow over
time.
