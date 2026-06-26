# Chapter 5: Evidence, Freshness, And Context

Evidence work separates context, evidence, freshness, and authority.

In the support-credit workflow, a reviewer found that the policy page in the AI
packet was last month's version. The draft summarized the ticket, pulled in the
outage note, found a policy page in the knowledge base, and drafted a clean
recommendation:

```text
Approve a discretionary service credit. The outage appears to qualify under
the severe-incident exception policy, and the customer is strategically
important.
```

The draft was not sloppy. It used the right nouns, reflected the customer's
frustration, and gave the support manager language that could be sent, with a
little editing, to the account team.

The stale source did not make the AI draft worthless. It made the decision
unready. The ticket showed that the customer had asked for a credit. The outage
note described what happened to the service. The old policy explained how
exceptions used to be handled. The current policy had changed the threshold. The
contract contained a narrower credit cap and a notice requirement. The billing
ledger showed prior credits. The compliance note raised a consistency concern.

All of that was context. Only some of it was evidence. Only some of the evidence
was current. And only some current evidence had authority for the claim being
made.

The workflow had plenty of context. It had not yet established authority.

> **Real case: Ofqual's 2020 grading algorithm.** When GCSE, AS, and A-level
> exams in England were cancelled during the pandemic, Ofqual used a
> standardisation model to calculate grades from inputs such as teacher
> assessments, school rank orders, and schools' historical performance.
>
> The evidence boundary mattered. A school's prior results could help calibrate
> overall grade distributions, but they were weak evidence for an individual
> student's actual performance that year. For this book's workflow lesson, the
> weak control was treating aggregate context as authority for individual
> outcomes.
>
> After many A-level grades were downgraded and public confidence did not hold,
> Ofqual and the government withdrew the calculated grades and reissued results
> using the higher of the centre-assessed grade and the calculated grade. The
> consequence appeared across several records: students' university and career
> plans were disrupted, the national grading process had to be reversed, and
> Ofqual's chief regulator resigned. The control lesson is
> evidence authority: useful context can support a decision, but it should not
> become the controlling evidence for a claim it cannot establish (Ofqual 2020;
> House of Commons Education Committee 2020; University of Bristol Centre for
> Multilevel Modelling n.d.; Richardson 2020).

## How Context Gets Mistaken For Evidence

People often talk about context as if it were one substance. More context sounds
better than less context. A larger prompt sounds safer than a smaller prompt.
A retrieval system sounds more grounded than a model answering from memory. A
human summary sounds more reliable than an unassisted generated answer.

Usually, more relevant context does help. A model that can see the support
ticket, the policy page, and the incident note will usually produce a better
draft than a model that sees only the user's one-line question. Retrieval can
connect outputs to external material instead of relying only on learned
parameters (Lewis et al. 2020). Documentation practices such as model cards,
datasheets, and FactSheets exist because provenance, intended use, and
limitations matter in real systems (Mitchell et al. 2019; Gebru et al. 2021;
Arnold et al. 2019). NIST's generative AI profile also treats information
integrity, provenance, and lifecycle controls as adoption concerns, not cosmetic
details (NIST 2024).

But context and evidence do different jobs. Context gives the model material to
reason from, summarize, classify, and draft with. Evidence gives the workflow a
basis for deciding whether a claim is true enough, current enough, relevant
enough, and authorized enough to use.

This distinction is familiar outside AI. An analyst preparing a decision memo
may have a spreadsheet, a Slack thread, a policy document, a prior approval,
and a manager's memory of how a similar case was handled. Those materials are
not equal. The spreadsheet may be useful but stale. The Slack thread may explain
pressure but not authority. The prior approval may show precedent but not
policy. The manager's memory may be honest but incomplete.

High-consequence work survives by separating those things. Finance does not
usually treat an old approval matrix as current authority for this quarter's
spend limit. Incident teams distinguish a preliminary timeline from confirmed
telemetry. Software teams distinguish a design note from a passing test.
Decision makers distinguish assumptions from facts and facts from approvals.

AI does not remove that older discipline. It makes the discipline easier to
skip because weak context and strong evidence can appear in the same fluent
paragraph.

## Checking Claims Instead Of Answers

The support recommendation sounded like one answer. It was really a bundle of
claims.

- The outage affected this customer.
- The incident qualifies as severe under the current policy.
- The customer contract allows this kind of credit.
- The proposed amount is within finance authority.
- Prior credits do not change the recommendation.
- Compliance risk is acceptable.
- The support manager may tell the customer approval is likely.

Each claim needs different evidence.

The support ticket shows that the customer asked for help, not that the customer
is contractually eligible for a credit. The outage timeline documents the
service incident, not this customer's credit cap. The current policy describes
the general exception rule but does not override the contract. The billing ledger
shows prior credits and account standing without establishing outage severity. A
compliance note identifies consistency risk without approving the financial
effect.

A source citation can mislead here. A source can support one claim
while leaving another claim unsupported. A retrieved document can be accurate
and still not be authoritative for the decision. A summary can be faithful to
its source and still omit the fact that another source controls the case.
Research on summarization factuality and hallucination shows why fluent
generated text needs source-grounded checking, but the operational problem is
more specific than hallucination alone (Ji et al. 2023; Maynez et al. 2020). A
claim can be fluent, sourced, and still not accepted.

The practical move is to verify at the claim level.

Instead of asking, "Is this AI-generated proposal ready to use?" ask:

1. What claims does it make?
2. Which source is allowed to support each claim?
3. How fresh must that source be?
4. Which claims are unsupported, stale, contested, or only partially supported?
5. What decision state follows from that evidence?

That last question matters. Evidence is not collected for decoration. It changes
what the workflow can accept.

## When Real Sources Go Stale

Stale evidence is dangerous because it feels responsible.

The AI did not invent the policy page. It found a real document. The policy
page had been correct last month. The language matched the kind of exception
the customer wanted. A human under pressure might have made the same mistake.

Freshness has to be handled as part of the decision rule.

In low-risk work, freshness may be informal. If AI summarizes a personal note
from yesterday's meeting, the user can often judge whether it is recent enough.
In consequential work, freshness needs a rule. The approved policy must be the
current approved version. The billing threshold must reflect current account
standing. The incident record must be approved or clearly marked preliminary.
The contract clause must be the executed customer contract, not a template or
sales summary.

Authenticity is not enough. A source can expire, remain retrievable, and be
summarized accurately while still supporting a bad decision.

The workflow should not ask only whether the answer is grounded. It has to ask
grounded in what, for which claim, as of when, and with whose authority.

This is also where AI breaks the familiar human analogy. A human analyst often
leaves some visible trace of the source path: the file name, the spreadsheet
tab, the policy URL, the forwarded email, the uncertainty in the memo. AI can
compress many source types into one polished answer. Model memory, user
context, retrieved snippets, current records, stale policies, and assumptions
can land in the same tone of confidence.

The workflow needs to restore the distinctions the prose erased.

## Mapping Which Source Can Answer Which Claim

The first artifact is intentionally boring: a small table that says which source
is allowed to answer which kind of claim.

For the support-credit workflow, a starting version looks like this:

| Claim Type | Authority-Bearing Source | Freshness Rule | Failure Signal |
| --- | --- | --- | --- |
| Customer requested credit | Support ticket | Current ticket state | Ticket summary only |
| Incident occurred | Outage timeline | Incident record closed or approved | Informal Slack recap |
| Customer was affected | Customer-specific impact evidence | Current telemetry or approved impact note | General outage summary only |
| Credit is allowed | Current policy and contract clause | Current approved version | Stale KB article |
| Credit amount is permitted | Billing ledger and finance threshold | Current account standing | Prior spend estimate |
| Exception is consistent | Compliance note or policy owner review | Current guidance | Prior ad hoc exception |

This table is a small guard against one common failure: treating a useful source
as if it answered every claim.

The support ticket is necessary, but it does not settle eligibility. The outage
timeline is necessary, but it does not settle customer-specific impact. The
policy is necessary, but it may not be sufficient if the contract narrows the
customer's rights. The billing ledger is necessary for financial authority, but
it is not a substitute for policy interpretation.

The table also makes missing evidence visible. If the current policy cannot be
found, the output enters a different decision state. If customer-specific impact
evidence is missing, the workflow should not use the general outage timeline to
establish customer harm. If the contract clause conflicts with the policy, the
correct output may be escalation, not a recommendation.

The artifact is small because it maps one consequential decision well enough to
show where authority is still missing.

## Putting The Draft Into An Evidence Table

The source map tells the workflow where evidence may come from. The
claim-to-evidence matrix applies that map to a specific AI output.

For the draft recommendation, the matrix might look like this:

| AI Claim | Evidence Required | Current Status | Decision State |
| --- | --- | --- | --- |
| Customer qualifies for service credit | Current policy, contract clause, customer impact evidence | Incomplete | Proposal only |
| Incident severity qualifies as severe | Outage timeline and customer-specific impact | Partially supported | Needs evidence |
| Recommended amount is within authority | Billing ledger and finance threshold | Missing | Blocked |
| Customer can be told approval is likely | Accepted recommendation and communication owner | Not accepted | Not permitted |

The matrix changes what the team can argue about.

Without it, the team argues about the answer. Sales says the recommendation
looks reasonable. Support says the customer needs a response. Finance asks
whether the amount is allowed. Compliance worries about precedent. The AI draft
floats between them as if its coherence were the shared object.

With the matrix, the team argues about claims and evidence. The disagreement
becomes easier to locate.

The recommendation can remain useful. The AI has already summarized the ticket,
identified likely policy language, and exposed the missing evidence. But the
workflow can now say, with precision, that the output is a proposal only. One
claim is partially supported. One claim is missing finance evidence. One
customer-communication claim is not permitted because the decision has not been
accepted.

This is how a probabilistic output starts to become operationally useful: the
fluent answer is broken into claims the workflow can inspect.

## Authority Depends On The Decision

One trap in evidence work is looking for "the source of truth" as if every
workflow has a single controlling record.

Some decisions do have a clear controlling source. A deployed configuration may
be the current configuration. A signed contract may control a customer term. A
policy owner may publish the approved policy. But most operational decisions
combine several sources, each authoritative for a different part of the
decision.

In the support-credit workflow, the current policy is not more authoritative than the
contract in the abstract. It is authoritative for policy interpretation. The
contract is authoritative for the customer's obligations and limits. The billing
ledger is authoritative for current account standing. The outage timeline is
authoritative for incident facts. Customer-specific impact evidence is
authoritative for whether this customer was affected.

The workflow fails when it lets one source borrow authority from another.

That can happen with humans. A manager remembers a policy and applies it too
broadly. An analyst copies a clause from a prior customer memo. A team treats
an incident recap as if it established customer-specific impact. Mature workflows
try to catch those mistakes through versioning, approvals, review, and audit.

AI makes the borrowing harder to see. It can put the ticket, policy, outage
note, and contract language into one coherent paragraph. The paragraph may
sound as if all sources support one conclusion. The matrix forces the paragraph
back into parts.

## Decision States After The Evidence Check

Chapter 5 adds two pieces to the decision acceptance contract:

- an authority-bearing source and freshness map;
- a claim-to-evidence matrix.

These pieces make acceptance inspectable.

With those controls in place, the support-credit workflow can distinguish at
least five states:

- **Draft:** AI has produced useful language, but the claims have not been
  checked.
- **Needs evidence:** one or more claims require source support.
- **Blocked:** a required source is missing, stale, or outside the allowed
  authority path.
- **Ready for review:** claims have adequate evidence for a human reviewer to
  judge.
- **Accepted:** an accountable owner has approved the decision under the
  workflow's rules.

Those states matter because they prevent a draft from becoming a decision by
accident.

In the naive workflow, the AI-generated proposal moved from draft to Slack to
expected customer answer before anyone knew whether the policy, contract,
billing threshold, and customer-impact evidence supported it. In the improved
workflow, the same AI-generated proposal produces a different result:

```text
Decision state: blocked.
Reason: current policy found, contract clause conflicts with discretionary
credit language, billing authority missing, customer-specific impact not yet
confirmed.
Valid next action: prepare escalation packet for finance and compliance.
Customer communication: not permitted to imply approval.
```

That output is the workflow doing its job. The AI helped summarize the case and
identify the relevant claims. The workflow prevented unsupported authority from
escaping.

## When The Artifact Is Too Heavy

Sometimes it is.

If a person asks AI to summarize a meeting note for private use, a source map
and evidence matrix may be unnecessary. The user can often inspect the original
note, decide whether the summary is good enough, and move on. Controls should
scale by consequence.

The service-credit recommendation in the support-credit workflow is different because it affects money,
customer treatment, audit exposure, and future precedent. Circulation also
creates pressure: if sales sees a polished recommendation before finance and
compliance have approved or cleared the claims they own, the recommendation
starts to become politically real.

The practical question is whether this workflow can safely accept this output
without a small evidence artifact.

For consequential decisions, the cost of a small evidence artifact may be lower
than the cost of reconstructing the case after the wrong answer has already
spread.

## When Retrieval Is Already In Place

Chapter 3 treated retrieval as an artifact fix. Here the same fix is tested
against claim-level authority.

Retrieval helps. It still leaves acceptance work for the workflow.

A retrieval system can bring relevant documents into the model's context (Lewis
et al. 2020). That can reduce reliance on model memory and make outputs more
grounded. But retrieval does not automatically decide whether a document is
current, whether it is authoritative for the claim, whether another source
overrides it, or whether the retrieved passage has been applied to the right
decision.

In the support-credit workflow, retrieval may be exactly how the stale policy entered the
answer. The model did not need to hallucinate. It needed only to retrieve a
real but superseded document and apply it fluently.

That is why provenance and documentation matter. Model cards, datasheets, and
FactSheets all point toward a broader lesson: AI workflows need records about
sources, intended use, limitations, and evaluation context (Mitchell et al.
2019; Gebru et al. 2021; Arnold et al. 2019). In operational adoption, those
records have to connect to the workflow's acceptance rules.

Better retrieval gives the workflow better candidates. The workflow still has
to decide which candidate has authority.

## When The Sources Are Messy

The evidence check exposes an operating problem: the team does not know which
source can support which claim.

Many organizations do not have a clean source for every claim. Policies live in
knowledge bases, PDFs, shared drives, ticket macros, and someone's memory.
Contracts have amendments, billing systems lag, incident records can be
preliminary, and customer data is messy. This is one of the reasons AI adoption
becomes hard in production.

Hiding that mess inside a fluent recommendation leaves the real problem in
place.

If the authority-bearing source is contested, the output should say so. If the
policy and contract conflict, the decision should escalate. If the billing
threshold is unknown, the workflow should block financial approval. If no one
owns the current policy, the AI workflow has exposed an operating problem.

That exposure can be valuable. AI can reveal where the organization was already
relying on informal knowledge. The practical response is to start with a narrow
workflow and make source uncertainty explicit enough that people can decide what
to fix, what to escalate, and what not to automate yet.

## A Rule For Drafting The Evidence Packet

Context is material the model can use.

Evidence is material the workflow can use to support or reject a claim.

Relevance, authority, authenticity, completeness, and acceptance are separate
states. A source may be relevant without being authoritative, authoritative for
one claim and irrelevant to another, or authentic and stale. A summary may be
faithful and incomplete. A recommendation may be useful and still blocked.

This distinction is the first practical step from AI as a drafting tool to AI
as part of a trustworthy workflow. Once the packet names missing evidence, the
organization can assign who owns the delay, who resolves source conflict, who
funds review cost, and who has authority to slow or stop the decision. The next
question is who has the budget and organizational cover to use that authority.
