# Appendix N: Pilot Operating Guide

Use this guide to run the first trustworthy AI workflow pilot.

## Week 0: Set The Boundary

Do not start by writing an enterprise AI policy. Start by defining one decision
and what the output may do before acceptance.

Required outputs:

- workflow name;
- authority mode: discovery only, draft assistance, reviewed proposal, or bounded action;
- decision to support;
- what the output may do before acceptance;
- what the output must not do before acceptance;
- authority-bearing source for the highest-risk claim;
- reviewer or checker;
- stop condition;
- audit record;
- owner after acceptance.

Week 0 should expose source ownership. If nobody can say which source controls
the claim, the model cannot fix that ambiguity. It can only make the ambiguity
faster.

## Week 1: Run Reviewed Proposals

Start with reviewed proposal mode unless the workflow is clearly draft assistance.

The AI may:

- summarize source material;
- identify claims;
- draft a recommendation;
- prepare a reviewer packet;
- request missing evidence;
- draft customer or internal language marked as not accepted.

The AI may not:

- communicate final approval;
- update authority-bearing records;
- apply credits, payments, entitlements, or penalties;
- bypass required owner review;
- convert missing evidence into a confident recommendation.

Incomplete packets should return to source collection before reaching human
review. This feels inefficient at first. It is usually less wasteful than making
reviewers reconstruct evidence by hand.

## Week 2: Measure Capacity And Bypass

By Week 2, measure the work instead of debating impressions.

Track:

- proposals generated;
- complete packets;
- incomplete packets;
- accepted packets;
- rejected packets;
- escalations;
- reviewer-hours used;
- review debt;
- stale-source blocks;
- escaped unsupported decisions;
- bypass attempts or private-tool use.

If generated proposals rise but accepted verified output does not, the workflow
has not gained trusted capacity. It has moved the bottleneck.

Look for bypass without treating it only as misconduct. Bypass often means the
official path is too slow, too broad, too hard to use, or mismatched to the
actual work. The repair may be a sanctioned lower-authority path, such as a
draft assistance path that cannot make commitments or touch restricted data.

## Week 3: Decide Under Pressure

The pilot should end with a recorded stop/go decision.

Use these questions:

1. Which work can the AI do reliably enough to keep?
2. Which authority remains withheld?
3. Which source blocked the most packets?
4. Which reviewer capacity threshold fired?
5. Did any unsupported decision escape?
6. Did reviewers have rejection authority?
7. Which bypass path appeared?
8. Which control actually changed acceptance?
9. Which control never changed behavior and should be removed or redesigned?
10. What evidence would justify increasing authority?

The decision should be one of:

- continue;
- narrow;
- throttle;
- escalate;
- stop.

The pilot is successful when it produces a truthful operating claim. "We can
run reviewed proposals for low-risk cases when packets are complete, sources
are current, and review debt stays below the threshold" is a better result than
"AI transformed support" with no acceptance evidence.

## Stop Conditions

Stop or roll back when:

- stale evidence is accepted;
- a missing authority-bearing source is bypassed;
- customer-facing language implies approval before acceptance;
- review debt exceeds the stated threshold;
- reviewers cannot reject without informal escalation pressure;
- private tools become the real path for restricted data;
- incident review produces only prompt changes when workflow controls failed.

Stopping is not anti-AI. It is how the workflow protects the next accepted use
inside a trustworthy workflow.
