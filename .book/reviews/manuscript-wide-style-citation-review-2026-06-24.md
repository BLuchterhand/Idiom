# Manuscript-Wide Style And Citation Review

Date: 2026-06-24

Supersession note: this is a historical review record. Several findings below
have since been addressed; use `publication-readiness-tracker.md` and
`package-manifest.md` for current beta/proof readiness status.

Scope:

- `.book/front-back/*.md`
- `.book/chapters/*.md`
- `.book/appendices/*.md`
- `.book/style-guide.md`
- `.book/source-ledger.md`
- `.book/compact-source-crosswalk.md`
- `.book/source-gap-backlog.md`
- `.book/front-back/bibliography.md`

Method:

- Six delegated reviewers read assigned manuscript slices or cross-cutting
  evidence surfaces.
- Reviewers were instructed to read actual files, not rely on memory.
- Mechanical checks covered publication-facing source handles, author-date
  citation mismatches, risky style terms, long paragraphs, and unfinished
  markers.
- Several reviewers spot-checked high-risk real cases against external source
  pages. This was not a full legal/source verification of every URL.

## Executive Verdict

The manuscript is directionally strong and professional, but it does not yet
pass a final style and citation honesty gate.

The largest issues are not argument failure. They are term drift, source-scope
overreach, and a few real-case phrasings that make stronger claims than the
current source ledger supports.

Fix before publication:

1. Replace the recurring "evidence, verification, and owner grant the effect"
   sentence with term-correct acceptance language.
2. Label the support-credit running case as a composite scenario at every
   chapter-level first use.
3. Scope broad prevalence claims where the source backlog still marks evidence
   as open.
4. Fix bibliography suffix residue such as `2025b` / `2025c` without `2025a`.
5. Narrow or source several real-case phrasings, especially MiDAS and Knight
   Capital.

## High-Severity Findings

### 1. Central Rule Collapses Evidence, Verification, Ownership, And Authority

Files:

- `.book/front-back/introduction.md:20`
- `.book/chapters/13-working-with-the-machine.md:23`

Issue:

The sentence says output is accepted only after "evidence, verification, and a
named owner grant the permitted effect." Evidence supports claims. Verification
checks claims. A named role accepts a decision or authorizes an effect. Evidence
and verification do not grant anything.

Recommended edit:

```text
AI output starts as a proposal. A workflow accepts it for use only after the
required evidence is checked and a named owner accepts the decision or
authorizes the permitted effect.
```

### 2. Running Support-Credit Scenario Can Read Like A Real Case

File:

- `.book/chapters/00-opening-workflow-teardown.md:4`

Issue:

The chapter calls it a "support-credit case" before clearly labeling it as the
book's composite design scenario. Front matter discloses this, but chapters can
be read independently.

Recommended edit:

At the first chapter use, say:

```text
The book's running support-credit scenario is a composite design scenario, not
a documented case study.
```

## Medium-Severity Findings

### 3. Air Canada Case Overstates The Source By Naming A Specific Missing Control

File:

- `.book/chapters/00-opening-workflow-teardown.md:61`

Issue:

"The missing control was..." turns an operational lesson into a source-backed
finding. The tribunal supports responsibility for the chatbot information and
the weakness of "correct policy elsewhere" as a defense. The specific workflow
repair is the book's inference.

Safer wording:

```text
For this book's workflow lesson, one relevant missing control was...
```

### 4. Mata Case Fault Tone Is Too Strong

File:

- `.book/chapters/01-category-error.md:33`

Issue:

"professional responsibility in name" implies the professional role was only
nominal. The court source supports a real gatekeeping duty that failed in use.

Safer wording:

```text
The professional gate existed, but the filing still crossed into court before
the cited authorities were verified.
```

### 5. Proposal Is "Promoted Into Authority"

File:

- `.book/chapters/00-opening-workflow-teardown.md:134`

Issue:

Authority is a permission/right, not the state into which a proposal is
promoted.

Safer wording:

```text
promoted into accepted work
```

or

```text
allowed to produce an authorized effect
```

### 6. Task Shape Terms Drift Between Verification And Acceptance

Files:

- `.book/chapters/04-shape-of-good-ai-task.md:3`
- `.book/chapters/04-shape-of-good-ai-task.md:81`

Issue:

The chapter first defines task shape as objective, evidence, permission,
output, verification, and recovery. Later it changes the list to objective,
evidence, permission, output, acceptance criteria, and recovery path.

Recommended edit:

Use one canonical list and distinguish verification from acceptance:

```text
objective, evidence, permission, output, verification, acceptance criteria, and
recovery
```

### 7. Workflow Class / Task Mode Terminology Drifts

Files:

- `.book/chapters/03-when-artifact-fixes-do-not-control-the-decision.md:216`
- `.book/chapters/04-shape-of-good-ai-task.md:189`

Issue:

The same three-level structure is called both "Workflow Class" and "task modes."

Recommended edit:

Pick one term, probably `authority mode`, and use it consistently:

- draft assistance
- reviewed proposal
- bounded action

### 8. Freshness Table Mixes Freshness, Authority, Completeness, And Approval

File:

- `.book/chapters/05-evidence-freshness-context.md:179`

Issue:

The table column `Freshness Rule` includes entries such as "Incident record
closed or approved" and "Current approved version." These combine freshness
with completeness and approval.

Recommended edit:

Split into:

- `Authority-Bearing Source`
- `Freshness Rule`
- `Completeness / Approval Rule`
- `Failure Signal`

### 9. MiDAS "Without Human Participation" Needs Narrowing Or Stronger Source

File:

- `.book/chapters/08-measuring-accepted-verified-output.md:42`

Issue:

The ledger currently cites the district-court Cahoo opinion, which is more
nuanced about human participation. A stronger formulation exists elsewhere, but
that source is not in the ledger.

Fix:

Either add the stronger source to `source-ledger.md` and bibliography, or
narrow the prose.

Safer wording:

```text
Court records and later settlement materials describe automated fraud
determination practices that claimants challenged as lacking adequate human
review.
```

### 10. Chapter 6 Incentive Claims Outrun Current Source Support

Files:

- `.book/chapters/06-responsibility-incentive-split.md:199`
- `.book/chapters/06-responsibility-incentive-split.md:216`
- `.book/chapters/06-responsibility-incentive-split.md:238`
- `.book/chapters/06-responsibility-incentive-split.md:298`

Issue:

The crosswalk and source-gap backlog mark incentive/accountability evidence as
an open research gap. Current prose sometimes reads as measured prevalence.

Recommended edit:

Frame these as operating risks to test:

```text
One risk to test is whether the team that benefits from speed is different
from the team that pays the review cost.
```

### 11. Chapter 6 Fault Tone Clusters Too Heavily

File:

- `.book/chapters/06-responsibility-incentive-split.md`

Examples:

- `launder` at lines 25 and 133
- `pretends` around line 110
- `rubber stamp` / `liability shield` around lines 162-164
- `ceremonial` around line 255
- `accountability ceremony` around line 345

Issue:

Any one phrase may be defensible, but the cluster conflicts with the style
guide's instruction to critique workflow state before motive.

Recommended edit:

Use missing-control language:

```text
accountability without source access, review capacity, rejection authority, or
escalation cover
```

### 12. Chapter 7 Blurs Machine Enforcement And Human Decision

File:

- `.book/chapters/07-effect-boundaries-for-ai-workflows.md:156`

Issue:

The prose says software is suited to mechanical boundaries and includes
"reviewer decisions." Software can enforce the presence, identity, logging, or
required state of a reviewer decision; it cannot mechanically enforce the
human judgment itself.

Recommended edit:

```text
Software can enforce that a reviewer decision exists, names a reviewer, records
the state, and blocks missing decisions. It cannot decide whether the business
case is persuasive.
```

### 13. Accepted Verified Output Is Treated As Both Metric And Estimate

Files:

- `.book/chapters/08-measuring-accepted-verified-output.md:343`
- `.book/chapters/12-designing-one-trustworthy-workflow.md:228`

Issue:

Accepted verified output should be counted from accepted decisions or logged
workflow states. Proposal count, packet completeness, review capacity, and
acceptance-rate assumptions estimate future throughput. Chapter 12's `18/week`
number is not derived or explicitly labeled as a scenario assumption.

Recommended edit:

Say:

```text
Count accepted verified output from logged accepted decisions. Use proposal
volume, packet completeness, review capacity, and acceptance-rate assumptions
to estimate future throughput.
```

Label `18/week` as an illustrative scenario assumption or derive it in the
table.

### 14. Knight Capital Timing Compresses Loss And Event Window

File:

- `.book/chapters/10-from-assistant-to-bounded-agent.md:49`

Issue:

The SEC source supports the 45-minute order/execution window and the unwanted
positions. The more than $460 million loss is the consequence of the event and
unwinding the positions, not necessarily a realized loss inside that same 45
minutes.

Safer wording:

```text
In about forty-five minutes, the system sent millions of orders and accumulated
large unwanted positions. Unwinding those positions left Knight with a loss of
more than $460 million.
```

### 15. Bibliography Has Suffix Residue

Files:

- `.book/front-back/bibliography.md:13`
- `.book/front-back/bibliography.md:18`
- `.book/front-back/bibliography.md:152`
- `.book/chapters/13-working-with-the-machine.md:65`

Issue:

Publication-facing entries use `2025b`, `2025c`, and `2023a` without visible
matching `2025a` or `2023b` entries. This appears to be ledger/source-pool
residue.

Fix:

Normalize bibliography suffixes and matching in-text citations.

### 16. Deloitte Case Should Name Citation/Reference Verification Directly

File:

- `.book/chapters/13-working-with-the-machine.md:53`

Issue:

"Professional delivery without enough source verification" is directionally
fair but vague. The supported facts are citation/reference errors, a court-case
summary error, a partial refund, and correspondence saying generative AI tools
likely contributed to publishing-related errors.

Safer wording:

```text
The missing control was not professional delivery in the abstract. It was
claim-level citation, reference, and case-summary verification before
publication.
```

### 17. Broad Adoption And Bypass Claims Need Scope

Files:

- `.book/front-back/introduction.md:55`
- `.book/chapters/00-opening-workflow-teardown.md:75`
- `.book/chapters/04-shape-of-good-ai-task.md:75`
- `.book/chapters/06-responsibility-incentive-split.md:298`
- `.book/chapters/09-conditions-for-useful-human-review.md:162`
- `.book/chapters/10-from-assistant-to-bounded-agent.md:70`
- `.book/chapters/10-from-assistant-to-bounded-agent.md:280`
- `.book/chapters/12-designing-one-trustworthy-workflow.md:46`
- `.book/chapters/13-working-with-the-machine.md:106`
- `.book/chapters/13-working-with-the-machine.md:137`
- `.book/appendices/ai-workflow-antipatterns.md:17`
- `.book/appendices/pilot-operating-guide.md:73`

Issue:

The source backlog still marks reviewer capacity, shadow AI, bypass pressure,
and organizational incentive evidence as open or uneven. Current prose
sometimes uses `most`, `many`, `often`, `will`, and universal design-law
phrasing.

Recommended edit pattern:

- `most` -> `a recurring`
- `many` -> `some` or `in many consequential workflows` only when supported
- `often` -> `can`
- `will` -> `may`
- broad claim -> "this pilot should test whether..."

## Low-Severity Style And Terminology Findings

- `.book/chapters/00-opening-workflow-teardown.md:177`: `decision state label`
  is premature; use `work item state` or `packet state`.
- `.book/chapters/03-when-artifact-fixes-do-not-control-the-decision.md:270`:
  replace `Rubber-stamp review` with missing-control language.
- `.book/chapters/09-conditions-for-useful-human-review.md:70` and
  `.book/chapters/10-from-assistant-to-bounded-agent.md:69`: `The human
  probability issue` reads like scaffold text.
- `.book/chapters/10-from-assistant-to-bounded-agent.md:311`: `AI adoption
  becomes trustworthy` and a workflow `knows` are imprecise.
- `.book/chapters/12-designing-one-trustworthy-workflow.md:35`: `positive
  control pattern` overuses `control`; use `deployment pattern`.
- `.book/chapters/12-designing-one-trustworthy-workflow.md:182`: `legal path`
  overclaims; use `approved path` or `sanctioned path`.
- `.book/chapters/12-designing-one-trustworthy-workflow.md:189`: `agency` is
  not the book's term; use `authority` or `bounded action`.
- `.book/chapters/12-designing-one-trustworthy-workflow.md:414`: `This is the
  payoff the opening lacked` sounds like manuscript scaffolding.
- `.book/front-back/back-matter.md:15`: `pretending` is avoidable fault-tone
  language.
- `.book/appendices/pilot-operating-guide.md:80`: `not a vibe` is too casual
  for the book's senior-practitioner voice.

## Mechanical Scan Findings

- No publication-facing bracket source handles such as `RC1`, `A2`, or `G2`
  were found in front/back matter, chapters, or appendices.
- No obvious unsupported direct quotations were found. Most quoted text is
  fictional scenario, UI, command, or packet text.
- Risky words found and reviewed included `prove`, `fake`, `never`, `always`,
  `responsible AI`, and `pretending`. Most were either caveats or acceptable,
  but the findings above should be repaired.
- Long paragraphs are concentrated in real-case callouts and Markdown tables.
  These may render acceptably as tables/callouts, but several case blocks would
  benefit from splitting for scannability.

## Residual Risk

This was a broad, multi-reviewer pass, not a full publisher-grade source audit
of every URL and every legal record. Reviewers spot-checked high-risk sources,
including Air Canada, Mata, Boeing, CNET, Tessa/NEDA, Ofqual, Cruise, MiDAS,
GAO, Federal Reserve model-risk guidance, NTSB, SEC/Knight Capital, Waymo,
Morgan Stanley, Deloitte/AP, and DEWR materials.

Remaining source risk:

- Chapter 6 organizational incentive claims need either more evidence or
  narrower operating-risk language.
- Shadow AI / bypass claims need stronger sources or more careful "may/can/test
  for" wording.
- Legal and regulator cases should receive one final primary-source pass before
  publication, especially MiDAS and bibliography suffix normalization.
