# Book Writing Skill Suite Review

Date: 2026-06-21

Supersession note: this is a historical review record. Several recommended next
work items below have since been addressed; use
`publication-readiness-tracker.md` and `package-manifest.md` for current
beta/proof readiness status.

Scope: `.book/chapters/*.md`, `.book/source-ledger.md`,
`.book/real-case-map.md`, and current book-planning files.

Skills applied:

- `technical-book-architect`
- `technical-chapter-architect`
- `technical-book-flow-editor`
- `technical-book-open-close`
- `technical-case-evidence-editor`
- `technical-book-style-editor`
- `plain-technical-writing`

## Overall Finding

The manuscript now reads as a coherent technical field guide. It has a stable
reader promise: useful AI output should become accepted work only through
evidence, authority, review capacity, effect boundaries, measurement, and
learning loops. The book consistently uses the support-credit workflow as the
running example and pairs it with human-process parallels rather than treating
AI as an exotic exception.

The main publication-risk gap identified in this review was citation readiness,
not book shape. That gap has since been remediated for the current chapter
drafts: real-case citations now use author-date form, and reviewed
research/governance sources live in `source-ledger.md`. New claims from
`source-gap-backlog.md` or `research-sources.md` still need promotion into the
ledger before they enter chapter prose.

## Whole-Book Architecture

Fit: strong.

The chapter ladder builds in a usable order:

1. The opening shows a fluent output escaping into reliance.
2. Chapter 1 names the category error between usefulness and authority.
3. Chapter 2 establishes the human-process trust parallel.
4. Chapter 3 separates artifact improvement from decision control.
5. Chapters 4-7 define task shape, evidence, ownership, and effect boundaries.
6. Chapters 8-11 make the workflow measurable, reviewable, agent-ready, and
   governable.
7. Chapter 12 assembles the controls into one pilot.
8. Chapter 13 closes on the operating stance: work with the machine by granting
   authority at the pace the workflow can support.

Reader handle coverage is strong. Every chapter ends with a rule, test, table,
or operating question rather than a slogan.

Architecture risk: Chapter 13 is intentionally shorter than the others. That
works as a closing chapter, but it should stay focused on synthesis. Do not add
new machinery there unless a later revision exposes a concrete unanswered
reader question.

## Chapter Structure

Fit: strong with small watchpoints.

Most chapters follow the intended pattern:

- literal claim;
- concrete workplace or support-credit scene;
- real case;
- mechanism;
- insufficient fix or failure mode;
- control;
- reusable artifact;
- bridge.

The strongest chapters structurally are Chapters 3, 5, 7, 8, 10, 11, and 12.
They expose a failure mode, show why common fixes are insufficient, and leave a
specific workflow test.

Watchpoints:

- Chapter 0 is an opening teardown, so it does not need the same full chapter
  machinery as later chapters. It now opens with the title-aligned failure
  instead of a detached definition.
- Chapter 13 should not drift into a second introduction. It should remain a
  closing synthesis.

Edits made in this pass:

- Clarified the first Chapter 0 paragraph so it says the support-credit case
  begins with a useful AI recommendation, without the awkward "for example, the
  following..." setup.
- Split dense real-case paragraphs in Chapters 2, 5, and 10 so event summary,
  weak control, and consequence are easier to scan.

## Flow And Continuity

Fit: strong.

The running support-credit example has clear re-entry in the places where it
returns after another workplace parallel. Chapter 7 is a good example: it opens
with a familiar payment-approval boundary, then explicitly returns to the
support-credit workflow before introducing the Cruise case.

Term continuity is stable across the manuscript:

- `draft`, `proposal`, `accepted decision`, and `blocked` remain the authority
  ladder.
- `evidence`, `source`, `freshness`, and `authority` remain distinct.
- `human review` is consistently treated as useful only when the reviewer has
  evidence, time, and authority to reject or escalate.
- `effect boundary` remains about what a workflow may access, produce,
  communicate, mutate, or trigger.

Flow status: Chapters 5 and 11 carry the broadest research scaffolding, but the
draft now cites the academic, governance, and incident sources in author-date
form. Treat any remaining source work as verification and integration, not as
placeholder cleanup.

## Openings And Closings

Fit: strong.

Openings generally state the chapter's practical claim before the story:

- Chapter 3: artifact fixes improve the object but do not control acceptance.
- Chapter 4: good AI tasks define objective, evidence, permission, output,
  verification, and recovery.
- Chapter 8: accepted verified output measures work that has passed checks.
- Chapter 10: assistance and agency differ by authority.

The opening style is now literal rather than suspense-driven. A scan found no
chapter-draft hits for the earlier teaser patterns such as "the hard part,"
"the uncomfortable part," or "then things got worse."

Closings are doing useful work. Most chapters bridge to the next operating
question, which keeps the book from feeling like a collection of essays.

## Real Cases And Evidence

Fit: strong with one explicit source-readiness caveat.

The real cases now generally meet the desired sequence:

```text
What happened.
Weak or missing control.
Why the control mattered.
Consequence.
Repair or response.
Transfer lesson.
Source caveat.
```

The case map is coherent:

- Air Canada supports customer-facing AI guidance and reliance.
- Mata v. Avianca supports generated work crossing a professional filing
  boundary without source verification.
- Boeing 737 MAX supports trust through reviewable process rather than
  institutional confidence.
- CNET supports the limits of disclosures, labels, and after-the-fact
  correction.
- NEDA Tessa supports task-shape boundaries.
- Ofqual supports context versus authority-bearing evidence.
- Post Office Horizon supports accountability without evidence rights becoming
  blame.
- Cruise supports effect boundaries after uncertain system state.
- Michigan MiDAS supports accepted verified output over throughput.
- Uber supports the conditions required for human review.
- Knight Capital supports bounded agency and stop conditions.
- Waymo supports governance as a workflow-changing loop.
- Morgan Stanley supports a positive bounded-pilot pattern, with the caveat
  that it is company-reported.
- Deloitte supports final-source verification for professional deliverables.

Citation-readiness update: the broader research IDs previously flagged in
Chapters 5 and 11 have now been reviewed into `source-ledger.md` and converted
from internal source IDs to author-date citations in the chapter drafts.

## Style And Readability

Fit: strong.

Chapter drafts no longer show the obvious "AI output" tells the user flagged:

- no sales-language hits in chapter drafts for `unlock`, `supercharge`,
  `game-changing`, `seamless`, `future-proof`, or similar terms;
- no repeated teaser-cadence hits for the earlier suspense phrases;
- no obvious executive slogan pattern in chapter openings or closings.

The prose is practical and concrete. Most paragraphs name a work object, owner,
source, decision state, review action, or workflow consequence. The book's
payoff comes from recognition of ordinary workplace controls, not from
manufactured drama.

Line-level watchpoints for later polishing:

- Some case blocks are still intentionally compact. That is acceptable for this
  draft, but final layout should keep them scannable.
- Chapters 5 and 12 are the longest and densest. They are also the chapters
  with the most tables and implementation detail, so length is justified. They
  should receive the first line-edit pass after source integration.

## Recommended Next Work

1. Keep author-date citations aligned with the ledger as new sources are added.
2. Promote any new source-pool items into `source-ledger.md` before citing them
   in chapter prose.
3. Do a targeted line edit of Chapters 5 and 12 after any future citation
   expansion.
4. Decide whether Chapter 13 needs one small final artifact, such as a closing
   "authority pace" checklist, or whether the current quiet ending is preferred.
