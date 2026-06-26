# Chapter 5 Packet: Evidence, Freshness, And Context

## Planning Status

This packet is a planning record. Current manuscript readiness is tracked in
`publication-readiness-tracker.md`.

Draft with caveat.

The AI/provenance source base is usable for a careful draft. The running support
case remains a composite design scenario until support-specific evidence is
added for policy exceptions, service credits, support QA, billing authority,
and source-of-truth practices.

## Chapter Claim

AI-generated claims begin as proposals. They become usable only when the
workflow ties each consequential claim to adequate, current, authority-bearing
evidence for the decision at hand.

## Reader Starting Point

The reader likely believes that more context makes AI more trustworthy: bigger
prompts, better retrieval, more documents, or a human-provided summary.

This chapter should move the reader to a sharper belief:

> Context gives the model material. Evidence gives the workflow a basis for
> acceptance.

## First Visible Moment

A support manager opens an AI-drafted credit recommendation at 4:30 on Friday.
The answer is polished and specific. It cites the support ticket, an outage
summary, and a policy page in the knowledge base. Sales is waiting in Slack.
Finance wants to know whether the credit exceeds the customer's threshold.

Then the reviewer notices the policy page is last month's version.

The recommendation is not useless. It is also not acceptable yet. The ticket,
outage summary, stale policy, current policy, contract clause, billing ledger,
and compliance note each answer a different question. Until those differences
are visible, the workflow cannot know what the AI is allowed to recommend.

## Opening Story Or Example

Open with the support-policy exception case.

Narrative beats:

1. The AI draft looks useful: it summarizes the customer impact, names the
   apparent policy clause, and proposes approving a credit.
2. The support manager nearly forwards the recommendation because the language
   is coherent and the customer pressure is real.
3. The reviewer asks a basic operational question: "Which source answers which
   claim?"
4. The team discovers that the knowledge-base policy is stale, the contract has
   a narrower credit cap, the billing ledger shows prior credits, and the
   outage timeline does not yet establish customer-specific impact.
5. The chapter turns on this sentence:

```text
The workflow had plenty of context. It had not yet established authority.
```

## Workplace Behavior Parallel

An analyst preparing a decision memo uses the spreadsheet or policy document
that is easiest to find. The document may be relevant, familiar, and partly
right. It may also be stale, unofficial, incomplete, or inappropriate for the
decision being made.

The human-control pattern is familiar: high-consequence work distinguishes
background material, working notes, official records, current policy, contract
terms, logs, and accountable approval.

## Human Fallibility Pattern

People confuse available information with authoritative evidence, especially
under time pressure.

Common human failure modes:

- Using the source that is easiest to find.
- Treating a summary as if it were the underlying record.
- Forgetting that a policy or spreadsheet may have been superseded.
- Assuming a source answers more claims than it actually does.
- Carrying forward a stale fact because it appeared in a previous decision.
- Letting urgency make a partial evidence packet feel complete.

## Existing Workplace Contract

The older control pattern is source discipline:

- Current policy overrides stale guidance.
- Contract terms govern customer-specific obligations.
- Billing records answer financial authority and prior-credit questions.
- Incident timelines answer what happened operationally.
- Customer-specific telemetry or records answer whether this customer was
  affected.
- Compliance notes answer consistency and fairness concerns.
- Decision memos distinguish facts, interpretations, assumptions, and missing
  evidence.

This chapter should show that serious work does not treat "the information I
have" as equivalent to "the evidence this decision requires."

## AI Analogue

AI can combine model memory, user-provided context, retrieved snippets, ticket
history, policy text, and official records into one fluent answer.

That is useful because AI can synthesize messy context faster than a person
can. It is risky because the output can erase differences between:

- background knowledge;
- user-supplied context;
- retrieved snippets;
- inspected source documents;
- current official records;
- tests, logs, or telemetry;
- accountable human acceptance.

The AI analogue should not be "AI hallucinates, so distrust it." It should be:

> AI can make weak, stale, partial, and official context sound equally coherent.

## Analogy Break

Human analysts usually leave traces of the source documents they opened, and
their uncertainty may be visible in the working process. AI can compress many
context types into a single confident answer with no obvious surface marker for
source authority, freshness, or claim coverage.

AI-specific differences:

- It can retrieve a stale document and present it with the same fluency as a
  current record.
- It can mix user-provided assertions with official sources.
- It can summarize a source accurately while applying it to the wrong decision.
- It can generate a plausible missing bridge between sources.
- It can propagate unsupported claims quickly into Slack, tickets, CRM notes,
  or customer-facing drafts.

## Operator Artifact

This chapter introduces two artifacts.

### Source Authority And Freshness Map

| Claim Type | Authority-Bearing Source | Freshness Rule | Failure Signal |
| --- | --- | --- | --- |
| Customer requested credit | Support ticket | Current ticket state | Ticket summary only |
| Incident occurred | Outage timeline | Incident record closed or approved | Informal Slack recap |
| Customer was affected | Customer-specific impact evidence | Current telemetry or incident customer-impact note | General outage summary only |
| Credit is allowed | Current policy and contract clause | Current approved version | Stale KB article |
| Credit amount is permitted | Billing ledger and finance threshold | Current account standing | Prior spend estimate |
| Exception is consistent | Compliance note or policy owner review | Current guidance | Prior ad hoc exception |

### Claim-To-Evidence Matrix

| AI Claim | Evidence Required | Current Status | Decision State |
| --- | --- | --- | --- |
| Customer qualifies for service credit | Current policy, contract clause, customer impact evidence | Incomplete | Proposal only |
| Incident severity qualifies as severe | Outage timeline and customer-specific impact | Partially supported | Needs evidence |
| Recommended amount is within authority | Billing ledger and finance threshold | Missing | Blocked |
| Customer can be told approval is likely | Accepted recommendation and communication owner | Not accepted | Not permitted |

The artifact should be concrete enough that Chapter 9 can later turn it into a
reviewer packet.

## Evidence Status

Current status: draft with caveat.

Sources available:

- **A2**: hallucination definitions, causes, measurement, and mitigation.
- **A3**: summarization factuality and faithfulness failures.
- **A6**: retrieval-augmented generation and updateable knowledge.
- **A7**: model reporting, intended use, limitations, and evaluation context.
- **A8**: dataset provenance, collection, and recommended use.
- **A9**: AI service factsheets, supplier declarations, provenance, and
  governance records.
- **G2**: generative-AI risks, information integrity, and lifecycle controls.
- **S1**: operational feedback and monitoring.
- **S3**: postmortem culture and converting failures into system improvements.

Source gaps:

- Support QA and escalation workflow sources.
- Service-credit or policy-exception handling sources.
- Sources for customer contract, billing, policy, and support records as
  separate sources of truth.
- Examples of stale internal knowledge bases causing operational decisions to
  fail.

Drafting constraint: do not present the support-credit workflow as a documented case
study. Present it as a composite design scenario until the source ledger
supports the details.

## Main Sections

### 1. The Stale Policy Problem

Open with the support-credit workflow. Show the AI draft as useful but unacceptable.

Key point: the issue is not "AI got it wrong." The issue is that the workflow
had no explicit rule for which source could answer which claim.

### 2. How Context Gets Mistaken For Evidence

Define the evidence hierarchy in plain operational terms:

- model memory;
- user prompt context;
- retrieved snippets;
- inspected source documents;
- current official records;
- logs, tests, telemetry, and transaction records;
- accountable review and acceptance.

Key point: each layer can help, but each carries different authority.

### 3. Checking Claims Instead Of Answers

Break the AI recommendation into claims.

Example claims:

- The outage affected this customer.
- The current policy allows a discretionary credit.
- The customer's contract does not override the policy.
- The proposed amount is within finance authority.
- The support manager may communicate the decision.

Key point: the whole answer is not verified just because one claim is sourced.

### 4. When Real Sources Go Stale

Explain why source age matters.

Possible micro-analogy: a finance team does not use last quarter's approval
matrix for this quarter's spend limit. The old document may be authentic and
still no longer authoritative.

Key point: a source-grounded answer from stale evidence can be more dangerous
than an answer that admits uncertainty.

### 5. Authority Depends On The Decision

Show that different sources answer different questions.

The support ticket establishes that the customer asked for help. It does not
establish contractual eligibility. The outage timeline establishes that an
incident occurred. It does not establish the customer's credit amount. The policy describes general
rules. It does not override the customer contract.

Key point: no single source is "the context." Authority depends on the claim.

### 6. Mapping Which Source Can Answer Which Claim

Introduce the source authority and freshness map.

Keep the artifact small. It should not look like governance overhead; it should
look like the minimum table required to prevent a bad decision.

### 7. Putting The Draft Into An Evidence Table

Turn the AI recommendation into an evidence packet.

Show what happens when the matrix is incomplete: the valid output is not an
answer, but a proposal plus missing evidence and a blocked decision state.

### 8. Decision States After The Evidence Check

Connect the chapter to the decision acceptance contract.

Chapter 5 adds:

- source-of-truth map;
- freshness rule;
- claim-to-evidence matrix;
- blocked state for missing or stale authority-bearing evidence.

Key point: the workflow can now say "draft," "needs evidence," "blocked," or
"ready for review" without granting the model decision authority.

## Concepts Introduced

- Context versus evidence.
- Source authority.
- Freshness rule.
- Claim-level verification.
- Source authority and freshness map.
- Claim-to-evidence matrix.
- Proposal, supported claim, verified claim, accepted decision.

## Concepts Reused

- Fluency is not authority.
- Capability is not accountability.
- Decision acceptance contract.
- Human review is not a control without evidence.
- Review throughput and accepted verified output are reserved for Chapter 8.

## Practical Takeaway

Before using AI output in a consequential workflow, ask:

1. What claims does the output make?
2. Which source is allowed to support each claim?
3. How fresh must that source be?
4. Which claims are unsupported, stale, or only partially supported?
5. What decision state follows: draft, needs evidence, blocked, ready for
   review, or accepted?

Artifact validation:

| Field | Rule |
| --- | --- |
| Owner | Source owners maintain freshness rules; workflow owner enforces blocked states. |
| Source authority | Current policy, contract, billing ledger, outage evidence, and compliance guidance. |
| Update cadence | Recheck on every consequential decision and after source-owner changes. |
| Failure signal | A stale, partial, or unofficial source supports an accepted claim. |
| Operational hook | Block acceptance, mark the decision needs-evidence, or escalate a source conflict. |
| Escalation/retirement | Block acceptance until authority-bearing evidence is current or escalate the source conflict. |

## Claim Gate

| Claim Type | Drafting Rule |
| --- | --- |
| Sourced empirical claim | Use AI/provenance/factuality sources for claims about hallucination, faithfulness, retrieval, and source handling. |
| Normative control standard | Consequential claims should be tied to adequate, current, authority-bearing evidence. |
| Composite scenario | The support-credit source set remains a composite design scenario until support-specific evidence is added. |

## Possible Objections

### "This Is Too Much Process For A Simple AI Summary"

Answer: scale the control to consequence. A personal summary may only need a
quick source check. A customer credit recommendation needs source authority
because it can affect money, customer treatment, audit exposure, and precedent.

### "RAG Solves This"

Answer: retrieval helps but does not decide whether the retrieved source is
current, authoritative, complete, or appropriate for the claim. RAG can retrieve
the stale policy just as fluently as the current one.

### "Humans Also Use Stale Information"

Answer: exactly. That is why mature workflows use versioning, approvals,
records, and review. AI makes the same failure easier to miss because weak and
strong context can appear in one polished answer.

### "We Do Not Have Clean Sources Of Truth"

Answer: then the workflow should expose that as a blocker or escalation, not
hide it in a confident recommendation. A contested source is a decision fact.

## What This Chapter Sets Up Next

Chapter 5 shows what evidence the workflow needs. Chapter 6 asks who owns the
incentives, costs, and authority around that evidence.

Bridge sentence:

```text
Once the packet names missing evidence, the organization can assign the delay,
conflict, review cost, and protected no. The next question is who has the
authority, budget, and cover to slow or stop the decision.
```

## Ending Takeaway

Context improves the proposal. Evidence determines what the workflow can do
with it.

## Drafting Notes

- Keep the first draft practical, not encyclopedic.
- Do not overexplain transformer mechanics here; Chapter 1 owns model
  mechanics.
- Do not solve permissions or reviewer capacity here; Chapters 7, 8, and 9 own
  those.
- Use the support-credit workflow as the spine and one finance/accounting freshness
  micro-analogy at most.
- Mark all support-domain operational details as composite until sourced.
