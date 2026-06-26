# Case Dossier: Support Policy Exception After An Incident

Purpose: keep the running case specific enough that it can pressure-test the
book's workflow controls.

Status: source dossier for the labeled composite support-credit scenario. The
scenario is grounded in sourced operational patterns and remains a composite
unless a chapter explicitly names a real case.

## Situation

A strategic customer requests a service credit after an outage. The support team
uses an AI assistant to draft an exception recommendation.

## Source Artifacts

- Support ticket: customer request, timeline, impact description, prior support
  notes.
- Outage timeline: incident start, mitigation, resolution, affected services,
  customer-specific impact.
- Stale policy: knowledge-base article cached by the AI assistant.
- Current policy: approved exception policy updated last month.
- Contract clause: service-credit eligibility, exclusions, notification
  obligations, and cap.
- Billing ledger: customer spend, prior credits, account standing, approval
  thresholds.
- Account history: strategic status, renewal risk, prior escalations.
- Compliance note: constraints on inconsistent customer treatment.
- Slack thread: sales pressure, support urgency, finance hesitation, executive
  visibility.

## Roles

- Requester: account or support lead asking for a recommendation.
- AI assistant: drafts a summary and proposed exception decision.
- Support manager: named accountable for customer communication.
- Finance owner: owns credit threshold and budget impact.
- Legal/compliance owner: owns policy interpretation and fairness constraints.
- Reviewer: checks evidence packet and can reject or escalate.
- Governance/security owner: owns data classes, tool authorization, logging, and
  auditability.
- Executive sponsor: wants faster resolution and visible AI productivity.

## Naive Workflow

1. Support asks the AI assistant whether the customer qualifies.
2. AI summarizes the ticket, stale policy, and outage note.
3. AI recommends approving the credit.
4. Support posts the recommendation in Slack.
5. Sales treats it as the expected answer.
6. Finance discovers missing contract and billing evidence after the customer
   has already been told approval is likely.

## Failure Modes

- Stale policy accepted as current policy.
- Ticket summary treated as verified incident evidence.
- Support manager named accountable without finance authority.
- Reviewers receive polished prose without claim-level evidence.
- Review queue exceeds capacity, causing review without enough time or evidence.
- Sales bypasses the official workflow with a private AI tool.
- Audit log records the final decision but not the evidence path.
- Executive pressure pushes for a fast exception despite unresolved source
  conflict.

## Moment Where The Framework Struggles

The contract clause and current policy conflict. The policy says severe
incidents can qualify for discretionary credit; the contract has a narrower
credit cap and notice requirement. Sales argues for an executive override
because the renewal is at risk.

The workflow must show that a trustworthy process does not always produce a
clean automated answer. Sometimes it produces a valid escalation with evidence,
tradeoffs, named authority, and a documented exception.

## Countercases

Use two small countercases so the main case does not feel engineered to flatter
the framework.

### Countercase A: Do Not Automate

A customer asks for an exception that depends on ambiguous legal interpretation
and a disputed contract clause. The AI can summarize the sources and identify
the conflict, but the workflow should refuse automated recommendation. The valid
output is an escalation packet, not a decision.

Politically unsatisfying outcome: sales dislikes the delay, but the workflow
protects the organization from inventing policy under pressure.

### Countercase B: Controls Save Time

A low-risk credit request falls below a finance threshold, uses current policy,
has complete outage evidence, and has no prior exception history. The AI drafts
the packet, the system verifies the required fields, and the reviewer accepts it
quickly.

Measurable outcome: review time drops because the reviewer checks a complete
evidence packet instead of reconstructing the case from Slack and tickets.

## Pilot Timeline

### Week 0: Inventory

- Identify current support exception paths.
- Inventory AI tools already used by support, sales, and account teams.
- Identify data classes and systems touched by the workflow.
- Estimate reviewer-hours available from finance and compliance.

### Week 1: Reviewed Proposal Pilot

- AI can summarize and draft only.
- AI cannot approve, communicate, or apply a credit.
- Every recommendation must include source links, missing evidence, and a
  proposed decision state.
- Incomplete evidence packets are auto-rejected.

### Week 2: Capacity And Failure Review

- Count proposals generated.
- Count accepted, rejected, escalated, and stale-source cases.
- Measure average reviewer time per packet.
- Identify bypass attempts and tool-sprawl issues.
- Decide whether to continue, narrow, throttle, or stop.

## Minimum Viable Decision Acceptance Contract For This Case

- One decision: whether to recommend a service credit.
- Controlling source map: current approved exception policy plus contract terms.
- Action limit before acceptance: draft recommendation only.
- One checker: finance/compliance reviewer for credit authority and policy fit.
- One stop condition: missing current policy, missing contract clause, or review
  queue above capacity.
- One owner: support manager for customer communication after acceptance.

## Escalation Tier

Move from reviewed proposal to bounded action only after:

- Source freshness status is automated.
- Contract and billing evidence are required fields.
- Finance threshold rules are enforced by the system.
- Reviewer capacity has a hard queue limit.
- Audit logs show claims, sources, checks, reviewer decision, and exception
  owner.
- A kill switch can disable credit communication or application.

## Case Questions By Chapter

- Opening: how did fluent output escape?
- Chapter 1: what did the model do usefully, and what did it overclaim?
- Chapter 2: why was "human reviewed" not enough?
- Chapter 3: why did prompt/RAG/policy/checklist fixes fail alone?
- Chapter 4: which subtasks fit AI, and which should not be automated?
- Chapter 5: which source answers which claim?
- Chapter 6: who benefits, who pays review cost, who owns risk?
- Chapter 7: what effects must the system prevent?
- Chapter 8: can review capacity keep up with proposal volume?
- Chapter 9: what must the reviewer see to say yes or no?
- Chapter 10: where is shadow AI already happening?
- Chapter 11: how does the organization test the controls?
- Chapter 12: what is the redesigned Monday workflow?
