# Chapter 8 Packet: Measuring Accepted Verified Output

## Planning Status

This packet is a planning record. Current manuscript readiness is tracked in
`publication-readiness-tracker.md`.

Draft with caveat for strongest economic claims.

Frame accepted verified output as the proposed metric for consequential
workflows with verification bottlenecks. Keep ROI and broad economic claims out
of scope unless separately sourced.

## Chapter Claim

For consequential workflows, this chapter proposes accepted verified output as
a better operating metric than generated volume. The metric is useful because it
forces the workflow to count review capacity, rejected packets, review debt,
rework assumptions, and escaped-risk signals separately.

## Reader Starting Point

The reader may measure AI by generated volume, usage, time saved by drafters, or
pilot activity.

## First Visible Moment

A queue report shows 47 AI-generated recommendations and six available
reviewer-hours before Friday's customer deadline.

Scene beats:

1. The adoption dashboard looks successful because generated recommendations
   increased.
2. The review queue tells a different story: 47 proposals, 6 reviewer-hours,
   and 15 minutes per complete packet.
3. Fourteen packets are missing required evidence and should not consume expert
   reconstruction time.
4. Nine complete packets still exceed funded capacity, forcing a throttle or
   scope decision.
5. The team stops talking about volume alone and asks how many recommendations
   became accepted verified work.

## Workplace Behavior Parallel

A team that produces more drafts than reviewers can accept has not increased
throughput. It has moved the bottleneck.

## Human Fallibility Pattern

People celebrate visible production while hiding review debt, rework, and
escaped-error cost downstream.

## Existing Workplace Contract

Work-in-progress limits, review queues, quality gates, sampling, and throttles
keep production matched to verification capacity.

## AI Analogue

AI makes proposal generation cheaper. In consequential workflows, the scarce
capacity often shifts to verification, review, and acceptance.

## Analogy Break

AI can increase proposal volume suddenly and invisibly across teams, creating
review overload before formal workflow metrics show risk.

## Operator Artifact

Reviewer-capacity calculator:

| Input | Example |
| --- | --- |
| Proposals expected | 47/week |
| Complete-packet rate | 70% |
| Complete packets | 33/week, rounded from 47 x 70% |
| Reviewer-hours available | 6/week |
| Average review time | 15 minutes |
| Max reviewed proposals | 24/week |
| Auto-reject count | 14 incomplete packets |
| Reviewed complete packets | 24/week |
| Accepted packet rate | 75% scenario assumption |
| Accepted verified output | 18 recommendations |
| Rejected reviewed packets | 6 reviewed packets |
| Review debt | 9 complete packets over capacity |
| Reviewer cost | 6 hours at loaded hourly rate |
| Avoided rework | Estimate from rejected incomplete packets |
| Escaped-error estimate | Tracked from incidents and sampled audits |
| Value per accepted output | Scenario-specific, stated as assumption |
| Auto-reject threshold | Missing required evidence |
| Throttle threshold | Queue > 24 open proposals |
| Stop/go decision | Continue reviewed proposal, throttle, narrow, or stop |

Does not own: reviewer-interface design or accountability rights. This chapter
owns queue math, capacity, and economic tradeoffs.

Artifact validation:

| Field | Rule |
| --- | --- |
| Owner | The workflow sponsor funds reviewer capacity and owns throttle decisions. |
| Source authority | Queue logs, reviewer time, accepted/rejected decisions, incident feedback, and sampled audits. |
| Update cadence | Weekly during pilot; after any escaped high-consequence error. |
| Failure signal | Generated proposals grow faster than complete packets and review capacity. |
| Operational hook | Throttle, auto-reject, narrow scope, or stop the workflow when review debt exceeds the threshold. |
| Escalation/retirement | Throttle generation, auto-reject incomplete packets, or narrow the workflow when review debt grows. |

## Claim Gate

| Claim Type | Drafting Rule |
| --- | --- |
| Sourced empirical claim | Use operations, WIP, quality, and review-capacity sources for bottleneck logic where available. |
| Normative control standard | Accepted verified output may be proposed as an operating metric, but the draft must not claim it is empirically superior until sourced. |
| Hypothesis/composite scenario | ROI, avoided rework, and escaped-error examples must be labeled as assumptions or scenario math until stronger sources are added. |

## Evidence Status

Sources: **A5**, **D1**, **D2**, **D3**, **S1**, **S3**, **S4**, **S5**,
**S6**, **G1**, **G2**.

Gaps: rework cost, review queue studies, support QA cost, compliance/legal/
medical review workload evidence.

## Main Sections

1. **The queue report**: proposal volume exceeds reviewer-hours.
2. **Generated work and accepted work**: separate activity from value.
3. **Review capacity math**: proposals, time, WIP, throttle.
4. **Incomplete packets**: auto-reject rather than reviewer reconstruction.
5. **What the calculator can and cannot establish**: distinguish accepted output,
   assumed value, avoided rework, and escaped-risk estimates.

## Practical Takeaway

Track accepted verified output, review debt, rejected incomplete packets,
escaped errors, and throttle events.

## Possible Objections

- "This slows adoption": unchecked generation slows adoption through rework.
- "The model will improve": better models increase delegation and review stakes.
- "Reviewers can sample": sampling needs risk tiers and escaped-error feedback.

## Bridge

Chapter 8 shows review capacity as a bottleneck. Chapter 9 designs review so
the human can actually judge the work.
