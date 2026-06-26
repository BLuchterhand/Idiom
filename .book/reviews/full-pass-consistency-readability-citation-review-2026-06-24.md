# Full-Pass Consistency, Readability, And Citation Review

Date: 2026-06-24

Supersession note: this is a historical review record. Several findings below
have since been addressed; use `publication-readiness-tracker.md` and
`package-manifest.md` for current beta/proof readiness status.

Scope reviewed:

- `.book/chapters/*.md`
- `.book/front-back/*.md`
- `.book/appendices/*.md`
- `.book/source-ledger.md`
- `.book/research-sources.md`
- `.book/compact-source-crosswalk.md`
- `.book/real-case-audit.md`
- `.book/front-back/bibliography.md`

Review method:

- Main pass against the book brief, style guide, term glossary, source ledger,
  bibliography, and chapter drafts.
- Three read-only subagent passes: flow/continuity, line style/terminology, and
  citation/evidence honesty.
- Spot external checks for time-sensitive and high-risk citation claims, mainly
  Waymo/NHTSA, Deloitte Australia, Air Canada, and Michigan MiDAS.

Overall judgment: the manuscript is in much better shape than a typical draft at
this stage. The core language is clear and consistent, the running
support-credit example works, and the citation apparatus is unusually honest
about caveats. The remaining work is not a rewrite. It is a precision pass:
label composite material earlier, normalize citation keys, reduce a few
too-strong proof/zero/prevalence claims, and smooth several places where
terminology drifts from the book's own glossary.

## Highest-Priority Findings

### 1. Label The Running Support-Credit Scenario As Composite At First Use

Finding: `.book/chapters/00-opening-workflow-teardown.md:3` introduces the
support-credit "case" before saying plainly that it is a composite design
scenario. `.book/front-back/front-matter.md:29` does disclose that status, but
readers can enter the opening chapter and initially treat the scenario as a
reported case.

Why it matters: the book is careful about citation honesty, and this is the one
place where the setup could feel more like an undocumented case study than a
worked example.

Suggested revision:

```text
A fluent output escapes when it starts doing the work of an accepted decision
while the workflow still labels it a draft. The composite support-credit
scenario used throughout this book begins with a genuinely useful
AI-generated proposal.
```

Also remove the trailing spaces at `.book/chapters/00-opening-workflow-teardown.md:4`
and `.book/chapters/00-opening-workflow-teardown.md:5`.

### 2. Fix The Closing Thesis So Evidence Does Not "Grant" Authority

Finding: `.book/chapters/13-working-with-the-machine.md:23` says:

```text
A workflow accepts it for use only after evidence, verification, and a named
owner grant the permitted effect.
```

Why it matters: this collapses the manuscript's own term distinctions. Evidence
supports claims; verification checks evidence and claims; a named role accepts a
decision or authorizes an effect.

Suggested revision:

```text
A workflow accepts it for use only after required evidence is checked and a
named owner accepts the decision or authorizes the permitted effect.
```

Check for the same formula elsewhere before final production because it is part
of the brand doctrine.

### 3. Normalize Citation Keys Across Chapter Prose, Ledger, And Bibliography

Finding: some chapter citations use a different author-date key than the
ledger's declared key:

- `.book/chapters/06-responsibility-incentive-split.md:70` cites `U.S. Government Accountability Office 2021/2025`, while `.book/source-ledger.md:60` uses `GAO 2021/2025`.
- `.book/chapters/08-measuring-accepted-verified-output.md:69` cites `National Institute of Standards and Technology 2002`, while `.book/source-ledger.md:55` uses `NIST 2002`.

Why it matters: this is not a source-support problem, but it will create
copyedit and bibliography friction.

Suggested action: pick one Chicago author-date convention for institutional
authors and apply it consistently in chapter citations, `source-ledger.md`, and
`front-back/bibliography.md`.

Related issue: `.book/front-back/bibliography.md` is not consistently
alphabetized. `NIST 2024` appears near the top before `Bates`, while other NIST
entries appear later. Sort or group institutional authors before production.

### 4. Keep Case Claims Inside The Ledger Caveats

#### Mata v. Avianca

Finding: `.book/chapters/01-category-error.md:33` describes the weak control as
"professional responsibility in name." That language makes the professional
gate sound nominal or fake. The source supports a real professional duty that
failed before filing.

Suggested revision:

```text
The professional gate existed, but verification failed before filing.
```

#### Knight Capital

Finding: `.book/chapters/10-from-assistant-to-bounded-agent.md:49` compresses
the event and realized loss into one time window. Safer phrasing separates the
erroneous trading window from the later realized loss.

Suggested revision:

```text
During the event, Knight accumulated several billion dollars in unwanted
positions. Unwinding those positions left the firm with a loss of more than
$460 million.
```

#### Waymo Flooded-Road Recall

Finding: `.book/chapters/11-evals-incidents-workflow-governance.md:40` is
appropriately date-bound. Keep that caution. External spot-checking confirmed
the May 2026 flooded-road recall, and also found a newer June 2026 Waymo/NHTSA
recall for freeway construction-zone behavior. If the case is framed as current
or latest near publication, it will need another freshness check.

Suggested action: retain the sentence at lines 53-54 that says the case should
be rechecked before publication. Consider adding the new June recall only if it
strengthens the governance-loop point without distracting from the chapter.

External sources checked:

- NHTSA Part 573 Safety Recall Report 26E026, Waymo flooded roadway recall:
  https://static.nhtsa.gov/odi/rcl/2026/RCLRPT-26E026-6527.pdf
- NHTSA recall acknowledgment 26E026:
  https://static.nhtsa.gov/odi/rcl/2026/RCAK-26E026-9973.pdf
- NHTSA Part 573 Safety Recall Report 26E035, Waymo freeway construction-zone
  recall, found in search results:
  https://static.nhtsa.gov/odi/rcl/2026/RCLRPT-26E035-7637.pdf

#### Deloitte Australia

Finding: `.book/chapters/13-working-with-the-machine.md:45` is basically
well-caveated. Keep "apparent" or "suspected" for AI-generated errors unless
using department correspondence that specifically supports stronger wording.

External spot check: AP reports a partial refund for an A$440,000/about
US$290,000 report, apparent AI-generated errors, Deloitte confirming incorrect
footnotes/references, a revised report, a disclosure that Azure OpenAI was used,
and no change to the report's substance or recommendations.

Source checked:

- Associated Press, Deloitte Australia report:
  https://apnews.com/article/australia-ai-errors-deloitte-ab54858680ffc4ae6555b31c8fb987f3

#### Michigan MiDAS

Finding: `.book/chapters/08-measuring-accepted-verified-output.md:38` is
generally safe because it uses settlement/allegation language. Keep the
property-seizure point tied to allegations, settlement, court records, or later
reporting.

External source checked:

- Michigan Attorney General settlement announcement:
  https://www.michigan.gov/ag/news/press-releases/2022/10/20/som-settlement-of-civil-rights-class-action-alleging-false-accusations-of-unemployment-fraud

### 5. Mark Scenario Math As Estimated, Not Measured

Finding: `.book/chapters/08-measuring-accepted-verified-output.md:344` says the
team can "derive accepted verified output" from proposal count, packet
completeness, review capacity, acceptance rate, and escaped-risk feedback.
`.book/chapters/12-designing-one-trustworthy-workflow.md:228` says `18/week
under stated assumptions`.

Why it matters: logged accepted decisions count output. Proposal count,
complete-packet rate, and reviewer capacity estimate possible throughput. The
chapter already mostly respects this distinction, but a few phrases make the
estimate sound more measured than it is.

Suggested revisions:

- In Chapter 8, prefer "estimate expected accepted verified output" when the
  number comes from planning inputs.
- Reserve "measure accepted verified output" for counts from logged accepted
  decisions.
- In Chapter 12, make the table cell read `18/week as a worked-example estimate
  under the 47 x 70% x capacity x 75% assumptions`.

### 6. Avoid "Zero" Claims Where Sampling Is The Control

Finding: `.book/chapters/12-designing-one-trustworthy-workflow.md:231` uses
`Zero tolerated for go decision; sampled audit required`, and
`.book/chapters/12-designing-one-trustworthy-workflow.md:361` says `zero
escaped unsupported decisions in sample`.

Why it matters: the wording is directionally right, but sampled audits cannot
establish a true zero escape rate. They can establish zero found in the sample,
or no known unresolved escaped unsupported decisions.

Suggested revisions:

- `No unresolved known escaped unsupported decisions; sampled audit required`
- `Continue only with complete packets improving, review debt controlled, and
  zero escaped unsupported decisions found in the sampled audit`

## Flow And Structure Findings

### 7. Rebalance The Introduction's Chapter Arc

Finding: `.book/front-back/introduction.md:90` groups Chapter 7 with "put AI
where it is strong." Chapter 7 is really where the book turns from task shape
and ownership into enforceable effect boundaries.

Suggested replacement:

```text
Chapters 4 through 6 put AI where it is strong. They define task fit,
claim-level evidence, source freshness, responsibility, and incentives.

Chapters 7 through 9 turn that shape into enforceable workflow. They define
effect boundaries, measure accepted verified output, and make human review a
real control.

Chapters 10 and 11 operate the workflow under pressure. They distinguish
assistants from bounded agents and make evals, incidents, and governance change
the workflow instead of only documenting it.
```

### 8. Smooth Chapter 1's Re-Entry Into The Running Scenario

Finding: `.book/chapters/01-category-error.md:6` says "the memo" without
clearly re-entering the opening scenario.

Suggested revision:

```text
In the same support-credit scenario, the AI-generated proposal behaved like a
short decision memo.
```

### 9. Move Chapter 12's Narrative Payoff Earlier

Finding: `.book/chapters/12-designing-one-trustworthy-workflow.md:384` delays
`The Final Case Decision` until after the assumption ledger, payoff model,
negative path, cadence, and one-page contract.

Why it matters: Chapter 12 is the assembly chapter. The reader wants to see the
customer answer land once the Week 3 stop/go decision is made. The later
operating-model sections will read better after that payoff.

Suggested action: move `## The Final Case Decision` immediately after the Week
3 stop/go section, then let the ledger/payoff/contract sections explain how the
decision will be governed and scaled.

### 10. Rename The Two "Mid-Pilot" Headings

Finding: `.book/chapters/12-designing-one-trustworthy-workflow.md:119` and
`.book/chapters/12-designing-one-trustworthy-workflow.md:163` both start with
`Mid-Pilot`.

Suggested revisions:

- `## Week 2: Capacity Becomes A Product Constraint`
- `## Week 2: The Bypass Check`

### 11. Fix A Backward-Looking Chapter Reference

Finding: `.book/chapters/12-designing-one-trustworthy-workflow.md:161` says a
stale-source drill is "a rehearsal for the near miss in Chapter 11," but Chapter
11 is already behind the reader.

Suggested revision:

```text
This is Chapter 11's stale-source near miss turned into a launch drill.
```

### 12. Improve Two Transition Openings

Finding: `.book/chapters/07-effect-boundaries-for-ai-workflows.md:6` opens with
"The employee" before grounding the scenario.

Suggested revision:

```text
A familiar finance boundary makes the next step visible: an employee may
prepare a payment request without being allowed to approve, release, and
reconcile it.
```

Finding: `.book/chapters/10-from-assistant-to-bounded-agent.md:26` makes a good
bypass point, but the chapter title promises bounded agency. Add a bridge after
line 27.

Suggested sentence:

```text
That pressure pattern is the bridge to bounded agency: before granting more
tool authority, the workflow needs a sanctioned lower-authority path for the
work people are already trying to do.
```

## Terminology And Style Findings

### 13. Standardize "Bounded Action", "Bounded Agent", And "Bounded Effect"

Finding: the manuscript moves among these terms:

- `.book/front-back/introduction.md:95`
- `.book/chapters/04-shape-of-good-ai-task.md:207`
- `.book/chapters/10-from-assistant-to-bounded-agent.md:30`

Suggested convention:

- `bounded action`: workflow mode or authority tier.
- `bounded agent`: a system granted tool-mediated action authority.
- `bounded effect`: the actual action or state change the workflow permits.

This distinction is already implicit in the chapters. Naming it explicitly in
the term glossary would reduce drift.

### 14. Replace "The Unit Of Trust Is The Model Inside A Workflow"

Finding: `.book/chapters/11-evals-incidents-workflow-governance.md:119` still
centers the model as the trust unit.

Suggested revision:

```text
The workflow is the unit of trust assessment: model, sources, tools,
permissions, reviewers, incentives, logs, and stop conditions together.
```

### 15. Replace "Source-Control Fix"

Finding: `.book/chapters/11-evals-incidents-workflow-governance.md:239` says
`source-control fix`, which can read as Git/version-control terminology.

Suggested revisions:

- `source-ownership fix`
- `source-freshness fix`
- `authority-bearing source repair`

### 16. Reduce Blame-Loaded Or Abstract Review Language

Findings:

- `.book/chapters/06-responsibility-incentive-split.md:108` says a polished
  answer "pretends finance already accepted the risk."
- `.book/chapters/06-responsibility-incentive-split.md:120` says `finance
  acceptance`.
- `.book/chapters/06-responsibility-incentive-split.md:162` uses `rubber stamp`,
  `liability shield`, and `exhausted approver` in one sentence.
- `.book/chapters/09-conditions-for-useful-human-review.md:144` says reviewer
  action must be `symmetrical`, and line 145 says reject/escalate must be
  `legitimate`.

Suggested replacements:

- `pretends finance already accepted the risk` -> `implies finance approval
  already exists`
- `We need finance acceptance` -> `We need finance approval`
- Replace the list at Chapter 6 lines 162-165 with reviewer states: reviewer
  with decision authority, reviewer without evidence, reviewer without rejection
  authority, reviewer under queue pressure.
- `symmetrical` / `legitimate` -> `Accept, reject, and escalate should be
  equally reachable, logged, and protected by policy.`

### 17. Trim Repeated "Ceremony" Shorthand

Finding: `ceremony` appears in several places:

- `.book/chapters/01-category-error.md:202`
- `.book/chapters/04-shape-of-good-ai-task.md:237`
- `.book/chapters/06-responsibility-incentive-split.md:345`
- `.book/chapters/11-evals-incidents-workflow-governance.md:220`
- `.book/chapters/13-working-with-the-machine.md:40`

Why it matters: some uses work, but manuscript-wide it becomes a shorthand for
"visible process without control." The style guide asks the manuscript to name
the missing hook.

Suggested action: keep one or two strong uses. Replace the rest with the exact
missing state: no rejection authority, no source-freshness gate, no permission
boundary, no audit hook, no throttle, or no owner.

### 18. Replace "Prove" Where The Passage Means "Support"

Finding: `.book/chapters/01-category-error.md:78` says citations are not being
used to `prove` a point. The manuscript's own style guide discourages proof
language unless the evidence really proves the claim.

Suggested revision:

```text
...but those capabilities are not what these particular citations are being
used to support.
```

### 19. Clarify Appendix Source Status Categories

Finding: `.book/appendices/reviewer-packet-template.md:10` mixes categories:
`real, internal, illustrative, or background`. `Real` and `internal` are not
parallel statuses.

Suggested revision: split the field into clearer dimensions:

- `Source existence`: verified source / illustrative placeholder / missing.
- `Source authority`: controlling / supporting / background.
- `Source freshness`: current / stale / unknown.
- `Caveat`: what the source does not prove.

## Citation Checks With No Major Issue Found

- No bracket ledger IDs such as `RC1`, `A2`, or `G2` were found leaking into
  publication-facing chapter prose.
- The real-case citations in chapter prose generally have corresponding
  `source-ledger.md` entries.
- The Air Canada chatbot case is framed carefully. The ABA summary confirms the
  key facts used by the manuscript: tribunal liability for chatbot
  misinformation, incorrect bereavement-fare guidance, customer reliance, and
  Air Canada's responsibility for information on its website.
- The Deloitte case is appropriately caveated as apparent/suspected
  AI-generated error unless stronger official correspondence is being cited.
- The Michigan MiDAS passage keeps the settlement/allegation boundary visible.
- The Waymo flooded-road case is usable, but should be freshness-checked again
  because NHTSA/Waymo automated-vehicle recalls are moving quickly.

## Mechanical Cleanup Notes

- Remove trailing whitespace in `.book/chapters/00-opening-workflow-teardown.md:4`
  and `.book/chapters/00-opening-workflow-teardown.md:5`.
- Very long Markdown table rows are common in Chapter 12 and appendices. They
  may be acceptable in source Markdown, but the HTML/EPUB export should be
  visually checked because several rows exceed 150 characters and may wrap
  poorly on mobile.
- `.book/chapters/10-from-assistant-to-bounded-agent.md:177` has an awkward line
  break inside a sentence:

```text
The workflow owner should ask why it is being used. If the answer is that
```

  It is not semantically wrong, but it is a good line-edit target.

## Suggested Edit Order

1. Fix the composite-scenario label and the closing thesis formula.
2. Normalize citation keys and bibliography ordering.
3. Apply the case-claim caveat edits for Mata, Knight, Waymo freshness, Deloitte,
   and MiDAS.
4. Clarify measured versus estimated accepted verified output.
5. Repair Chapter 12 flow: move the final case decision, rename Week 2 headings,
   and fix the Chapter 11 back-reference.
6. Run a terminology pass for bounded action/agent/effect, approval/acceptance,
   source ownership/freshness, and ceremony shorthand.
7. Rebuild HTML/EPUB and spot-check wide tables and line wrapping.
