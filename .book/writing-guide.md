# Writing Guide For The AI Adoption Book

## Recommended Approach

Use a whole-book plan, then write chapter by chapter in passes.

Do not slowly fill every chapter at once. That creates a manuscript full of
half-finished fragments and makes it hard to feel progress. Also do not write
the compact outline from Chapter 1 through Chapter 12 as polished final prose on
the first pass. That usually locks the early structure too soon.

The best workflow for this book is:

1. Define the book promise, reader, argument, and chapter arc.
2. Build a chapter packet for each chapter.
3. Draft chapters one at a time, but allow the order to be strategic.
4. Complete a rough full manuscript before deep polishing.
5. Revise the whole argument and structure.
6. Then revise chapters for clarity, evidence, voice, and citations.

Use `style-guide.md` as the voice standard during drafting and line edits. Use
`storytelling-guide.md` as the example and narrative standard.
Use `book-writing-skill-suite.md` to choose the right reusable Codex skill for
architecture, chapter structure, flow, style, openings/endings, and evidence
passes.

This matches common nonfiction practice: nonfiction proposals usually require a
main argument, annotated table of contents, chapter summaries, market/audience
positioning, and sample chapters before the full book is written. University of
Chicago Press submission guidance asks for an overview, an annotated table of
contents, and a sense of audience and competing books. Jane Friedman describes a
nonfiction book proposal as a business case for why the book should exist.

Sources:

- University of Chicago Press, submissions:
  https://press.uchicago.edu/resource/books_submissions.html
- Jane Friedman, nonfiction book proposal:
  https://janefriedman.com/start-here-how-to-write-a-book-proposal/
- University of Chicago Press, manuscript preparation:
  https://press.uchicago.edu/resource/emsguide.html

## The Working Model

Think of the book as an argument, not a container of topics.

The current thesis is strong:

> AI adoption fails when fluent probabilistic output is allowed to become
> operational authority without evidence, permissions, verification, and
> accountable acceptance. It succeeds when AI is treated as a reasoning,
> synthesis, and navigation layer operating inside explicit, inspectable,
> enforceable, and correctable workflows for trust-producing work.

Every chapter should move the reader from one belief state to another. A chapter
is not "about hallucinations" or "about governance." It should make a claim the
reader can use:

- Bad chapter purpose: Explain hallucinations.
- Better chapter purpose: Show why fluent output cannot be treated as authority
  until it is connected to evidence.
- Bad chapter purpose: Discuss governance.
- Better chapter purpose: Separate real operational controls from policy
  theater.

Purdue OWL's organization guidance emphasizes designing writing so readers can
understand it; the writer is responsible for the reader's path through the
argument. For this book, that means every chapter needs a clear promise,
progression, and payoff.

Source:

- Purdue OWL, organization and structure:
  https://owl.purdue.edu/owl/graduate_writing/graduate_writing_topics/graduate_writing_organization_structure_new.html

## Sidecar Drafting Rule

Use the human-process parallel as a recurring sidecar:

> High-consequence work learned not to trust consequential human output raw
> either.

For each chapter, ask what mature human workflow already learned the same
lesson: accounting, medicine, aviation, law, software, support operations, or
public administration. Use the parallel only when it clarifies the chapter's
claim, and always tie it back to the AI adoption implication.

The strongest chapter sections should follow this sequence:

1. Show the workplace behavior.
2. Name the human fallibility pattern.
3. Show the existing control or contract.
4. Translate the pattern to AI.
5. Add the decision acceptance contract field that makes the AI workflow
   trustworthy.

Do not overuse the analogy. One tight paragraph, sidebar, or example is usually
enough. The sidecar should make the reader think, "Of course; serious work has
always needed process," not distract from the chapter argument.

## Book-Level Planning

Before drafting more prose, create four planning artifacts.

### 1. Book Brief

File: `.book/book-brief.md`

Include:

- Working title.
- One-sentence thesis.
- Reader profile.
- Reader pain.
- Promise to the reader.
- What the book is not.
- Competing or adjacent books.
- Why this book is needed now.
- Tone and style notes.
- Target length.
- Citation style.

This is the internal equivalent of a nonfiction proposal overview. It prevents
the book from drifting into a collection of essays.

### 2. Argument Map

File: `.book/argument-map.md`

Include:

- Starting belief: what the reader likely believes before the book.
- Ending belief: what the reader should believe after the book.
- The major turns in the argument.
- The objections the book must answer.
- The recurring concepts that must be introduced once and reused consistently.

For the current outline, the likely argument arc is:

1. AI creates a trust mismatch.
2. The mismatch is predictable, not mysterious.
3. AI has real strengths and real weaknesses.
4. Trustworthy adoption requires a responsibility split.
5. Evidence, contracts, verification, and governance turn AI from risky output
   into trusted work.
6. Mature adoption is a learning system.

### 3. Chapter Packets

Directory: `.book/chapter-packets/`

Note: create new packets using the compact chapter structure.

Create one file per chapter before drafting the chapter itself.

Each packet should include:

- Chapter title.
- One-sentence claim.
- Reader problem.
- First visible moment: person, artifact, deadline, consequence.
- Opening story or example.
- Workplace behavior parallel.
- Human fallibility pattern.
- Existing workplace contract.
- AI analogue.
- Analogy break.
- Operator artifact.
- Evidence status: sourced, composite, hypothetical, or source gap.
- Main sections.
- Required sources.
- Concepts introduced.
- Concepts reused.
- Practical takeaway.
- Possible objections.
- What this chapter sets up next.

Jane Friedman recommends building a "fat outline" before drafting nonfiction
chapters: map the pieces, then write manageable chunks rather than staring at a
blank chapter. That is the right method here because the book is evidence-heavy
and argument-driven.

Source:

- Jane Friedman, writing a nonfiction chapter:
  https://janefriedman.com/how-to-write-a-nonfiction-book-chapter-without-tears/

### 4. Source Ledger

File: `.book/source-ledger.md`

The existing `research-sources.md` is a good starting bibliography. The source
ledger should become more operational:

- Source ID.
- Full citation.
- URL/DOI.
- Reliability tier.
- Chapters where used.
- Claims it supports.
- Key page/section notes.
- Quotation candidates.
- Permission/copyright concern, if any.

The University of Chicago Press manuscript guidance stresses complete citations,
consistent citation method, careful URL checks, and a citation style sheet when
needed. Do that early rather than repairing citations after the manuscript is
large.

Source:

- University of Chicago Press, manuscript preparation:
  https://press.uchicago.edu/resource/emsguide.html

## Drafting Strategy

### Do Not Start By Polishing Chapter 1

Chapter 1 will probably change after the rest of the book teaches you what the
book is really about. Start with the chapter that best demonstrates the book's
core claim.

Best first draft candidates:

- Chapter 5: Evidence, Freshness, And Context.
- Chapter 6: The Responsibility And Incentive Split.
- Chapter 7: Effect Boundaries For AI Workflows.
- Chapter 8: Measuring Accepted Verified Output.
- Chapter 9: Conditions For Useful Human Review.

Those chapters contain the book's central mechanism. Once they work, the opening
chapters can be written to lead readers toward them.

### Use A Three-Pass Chapter Method

Each chapter should follow the technical-book pattern defined in
`style-guide.md`: problem, concrete situation, mechanism, failure mode, control,
tradeoffs, and usable handle. If a chapter reads as commentary, add an
operator-facing artifact or decision rule before polishing the prose.

#### Pass 1: Structural Draft

Goal: get the argument on the page.

Target: 1,500 to 2,500 rough words per chapter.

In early rough-draft work only, include placeholders freely:

- `[source: NIST AI RMF]`
- `[example: support workflow]`
- `[bridge to Chapter 7]`
- `[needs stronger objection]`

Remove or resolve these before any publication-readiness pass.

Do not sentence-polish yet.

#### Pass 2: Evidence Draft

Goal: attach claims to sources and examples.

Add:

- Primary sources.
- Case examples.
- Short citations.
- Counterarguments.
- Definitions.
- Practical implications.

Cut claims that cannot be supported.

#### Pass 3: Reader Draft

Goal: make the chapter readable.

Revise:

- Opening hook.
- Section flow.
- Transitions.
- Repetition.
- Jargon.
- Ending payoff.
- Link to next chapter.

Purdue OWL and university writing centers describe writing as recursive:
prewriting, drafting, revising, and editing often loop rather than proceed in a
strict line. Use that intentionally: draft for structure first, revise for
meaning second, polish last.

Sources:

- Purdue OWL, stages of the writing process:
  https://owl.purdue.edu/owl/multilingual/multilingual_students/key_concepts_for_writing_in_north_american_colleges/stages_of_the_writing_process.html
- Purdue OWL, writing process:
  https://owl.purdue.edu/owl/general_writing/the_writing_process/index.html
- Montclair State University, writing process:
  https://www.montclair.edu/center-for-writing-excellence/digitaldashboard/resources-for-writers/the-writing-process/

## Chapter Template

Use this template for every chapter draft.

```markdown
# Chapter N: Title

## Chapter Claim

One sentence. A reader could disagree with it.

## Reader Starting Point

What the reader likely believes or misunderstands at the start.

## Opening

A concrete scenario, tension, failure pattern, or decision moment.

## First Visible Moment

The first person, artifact, deadline, and consequence the reader sees before
the chapter introduces abstract language.

## Workplace Parallel

The human version of the same trust problem.

## Human Fallibility Pattern

The fallible human behavior serious workflows already account for.

## Existing Contract

The review, approval, evidence, audit, separation-of-duties, verification, or
escalation practice that makes the human workflow trustworthy.

## AI Analogue

What AI changes: speed, scale, fluency, opacity, context mixing, tool authority,
or reviewer load.

## Analogy Break

Where the human-control analogy stops being enough and the AI-specific control
need begins.

## Operator Artifact

The packet, scorecard, map, approval rule, queue report, escalation script,
audit trail, calculator, or meeting artifact the chapter makes concrete.

## Evidence Status

Mark the example as sourced, composite, hypothetical, or source gap.

## The Problem

What breaks when people think about this topic poorly.

## The Core Idea

The chapter's main model, distinction, or argument.

## Evidence

Research, standards, field reports, examples, and counterexamples.

## Practical Pattern

How the reader should apply the idea.

## Common Objection

The most likely pushback, answered fairly.

## Takeaway

The one thing the reader should remember.

## Bridge

Why the next chapter follows.

## Notes

Source IDs, unresolved questions, examples to add.
```

This structure should not be visible as rigid headings in the final book. It is
a planning aid. Later, chapter headings can become more literary or
reader-friendly.

## Goals And Cadence

Use progress goals, not only word-count goals.

Word count is useful, but for an evidence-based nonfiction book, a chapter with
3,000 unsupported words may be less complete than a 1,200-word chapter packet
with a clean argument and source plan.

Recommended milestones:

### Milestone 1: Book Architecture

Deliverables:

- `book-brief.md`
- `argument-map.md`
- `outline-v2-compact.md`
- compact source crosswalk
- chapter packets for the compact chapter structure

Definition of done:

- Every chapter has a claim, purpose, sources, and takeaway.
- The chapter order has a clear argument arc.
- No chapter exists only because the topic is interesting.

### Milestone 2: Core Mechanism Draft

Draft chapters:

- Chapter 5.
- Chapter 6.
- Chapter 7.
- Chapter 8.
- Chapter 9.

Definition of done:

- The book's central model is understandable.
- Key terms are stable.
- Key terms are correct: `proposal`, `draft`, `evidence`, `verification`,
  `review`, `approval`, `acceptance`, `authority`, `control`, and
  `trustworthy` are not used as substitutes for one another.
- The reader can see what "trustworthy AI adoption" means in practice.

### Term Coherence Pass

Run this pass after the rough manuscript exists and before deep line editing.
Use `style-guide.md` as the term ledger.

For each chapter:

1. List the chapter's recurring terms.
2. Mark where each term is introduced, reused, or changed.
3. Check whether the term names an object, state, action, permission, source,
   or control.
4. Replace vague or stronger-than-earned terms.
5. Check chapter-to-chapter handoff: a term should not acquire a new meaning
   simply because the example changed.
6. Check source fit: legal, safety, scientific, and engineering terms should
   not imply claims the cited source does not support.
7. Add a note to the source ledger when a term depends on a source-specific
   definition or caveat.

Definition of done:

- One term means one concept.
- Neighboring terms remain distinct.
- Strong terms are earned by evidence, role, state, or control.
- The running example and real cases use the same vocabulary unless a domain
  caveat explains the difference.

### Fault-Tone Pass

Run this pass with the term coherence pass or the final line edit. Use
`style-guide.md` as the standard.

Search for words that imply bad faith or personal fault: `pretend`,
`dishonest`, `fake`, `theater`, `blame`, and `negligent`.

For each hit, ask:

1. Is the chapter actually discussing misconduct, legal fault, or incentive
   pressure?
2. If not, can the sentence name the missing state, source, owner, queue,
   permission, or control instead?
3. Does the replacement keep the criticism firm while making the failure easier
   to inspect and fix?

Definition of done:

- The prose critiques workflows, controls, incentives, and evidence before it
  critiques motives.
- Judgment words remain only where the evidence supports them.
- Readers can see the repair path, not only the failure.

### Milestone 3: Full Rough Draft

Draft all chapters structurally.

Definition of done:

- Every chapter exists as prose.
- Placeholders are acceptable.
- Major assertions have workplace-scene placeholders where examples are still
  missing.
- Each chapter has a first visible moment before abstract terminology.
- Missing sources are explicitly marked.
- No chapter needs to be polished.

### Milestone 4: Whole-Manuscript Revision

Use a reverse outline.

The University of Wisconsin Writing Center describes reverse outlining as
stripping a draft down to its main points so you can inspect organization. For
this book, create a reverse outline at both chapter and whole-book levels.

Definition of done:

- Every chapter's actual argument is visible.
- Repeated material is merged or cut.
- Missing transitions are identified.
- The book's argument still builds in the right order.

Source:

- University of Wisconsin Writing Center, reverse outlines:
  https://writing.wisc.edu/handbook/reverseoutlines/

### Milestone 5: Evidence And Citation Pass

Definition of done:

- Every factual claim has support.
- Every source used in prose appears in the source ledger.
- Weak sources are replaced or marked as illustrative.
- URLs and dates are verified.
- Citation style is consistent.

### Milestone 6: Reader Draft

Definition of done:

- Chapters have strong openings and endings.
- Technical sections are readable to the target audience.
- Examples carry the argument without overexplaining.
- The manuscript has a consistent voice.

### Milestone 7: External Review

Review in layers:

- Technical reviewer: correctness and missing nuance.
- Target reader: clarity and usefulness.
- Skeptical executive/operator: objections and adoption realism.
- Editor: structure, pacing, voice, and line-level clarity.

Do not send the whole rough draft to everyone. Send targeted chapters with
specific questions.

### Milestone 8: Final Production Draft

Definition of done:

- Full manuscript exported to `exports/trust-the-process-not-the-prompt.md`,
  HTML, EPUB, and KPF for the beta/proof package. DOCX/PDF can be produced
  later if a distribution channel requires them.
- Front matter and back matter exist.
- Citations and bibliography are complete.
- Permissions issues are resolved.
- Figures/tables are final or removed.

## How To Work In Markdown

Recommended structure:

```text
.book/
  README.md
  outline-v2-compact.md
  research-sources.md
  compact-source-crosswalk.md
  writing-guide.md
  book-brief.md
  argument-map.md
  case-dossier-support-exception.md
  positioning-memo.md
  style-guide.md
  storytelling-guide.md
  source-ledger.md
  chapter-packets/
    05-evidence-freshness-context.md
    06-responsibility-incentive-split.md
    ...
  chapters/
    01-category-error.md
    02-how-important-work-earns-trust.md
    ...
  exports/
```

Chapter files should be the source of truth. The generated manuscript lives at
`exports/trust-the-process-not-the-prompt.md`.

Use stable Markdown conventions:

- One `#` heading for the chapter title.
- `##` for major sections.
- Author-date citations for sources that have been promoted into
  `source-ledger.md`.
- Ledger source IDs only in planning and evidence files, not in chapter prose.
- Footnotes only if the final citation style changes to notes-bibliography.
- Plain Markdown tables only when genuinely useful.
- No complex formatting in draft files.

## Citation Strategy

Use Chicago author-date or Chicago notes-bibliography. For this type of book,
author-date is easier during drafting; notes-bibliography may feel more natural
for a trade nonfiction final manuscript.

Recommendation:

- Use Chicago author-date in chapter prose for citation-ready sources.
- Maintain `source-ledger.md` with the citation key, source type, reviewed date,
  verification method, supported claim, and caveat.
- Keep source IDs as internal ledger handles only.
- If a publisher requires notes-bibliography, convert the author-date citations
  after the manuscript and bibliography are stable.

This keeps chapter prose publication-facing while preserving traceability.

## Chapter Order Strategy

Do not write strictly in final order.

Recommended order:

1. Write the central mechanism chapters first: 5-9.
2. Write the operating-pressure chapters next: 10-11.
3. Write the practical playbook chapter: 12.
4. Write the opening teardown and trust-mismatch chapters: opening plus 1-3.
5. Write the task-fit chapter: 4, revising it to bridge from the opening into
   the central mechanism.
6. Write the closing last.

Reason: the opening chapters should promise the exact book you actually wrote.
You will know that only after the core has been drafted.

## Length Targets

For a business/engineering nonfiction book:

- Short book: 45,000 to 60,000 words.
- Standard trade nonfiction: 60,000 to 80,000 words.
- Deep technical/business hybrid: 80,000 to 95,000 words.

The current publication path is a concise field guide rather than a standard
trade nonfiction book. Keep chapter expansion tied to artifacts, evidence,
objections, or operating examples rather than length.

Recommended target:

- 38,000 to 44,000 words manuscript.
- 2,000 to 3,500 words per chapter.
- 3,500 to 4,800 words only for the central applied chapter if needed.
- 1,500 to 3,000 words for the opening teardown and closing if they do not need
  more.

## Quality Bar For Each Chapter

A chapter is draft-complete when it has:

- One clear claim.
- One memorable opening.
- A workplace parallel that makes the AI control feel obvious.
- A named analogy break so the human parallel does not become false
  equivalence.
- One operator artifact the reader can picture.
- A visible progression of ideas.
- At least three credible sources where research claims matter.
- One practical pattern or takeaway.
- One answered objection.
- A clean bridge to the next chapter.
- No unsupported major claims.
- No duplicated argument from another chapter unless intentionally reinforced.
- No sales-deck AI language, as defined in `style-guide.md`.

## How To Use AI While Writing This Book

Use AI as a drafting and critique partner, not as the authority.

Good uses:

- Generate alternative chapter openings.
- Identify weak transitions.
- Ask for skeptical objections.
- Summarize a source after you provide the text or citation.
- Compare two chapter structures.
- Convert notes into a rough section.
- Generate a reverse outline of a completed draft.
- Find repetition across chapters.

Bad uses:

- Asking AI to invent citations.
- Letting AI decide whether claims are true.
- Using AI-generated examples as if they were real case studies.
- Polishing prose before the argument works.
- Expanding chapters to hit word count without adding substance.

Before accepting AI-assisted prose, run the anti-sales checklist in
`style-guide.md`.

The book's own thesis should govern the writing process: probabilistic systems
can help with exploration and synthesis, but evidence, citations, structure,
and final judgment remain under accountable human control and source-grounded
verification.

## Immediate Next Steps

1. Treat `outline-v2-compact.md` as the canonical drafting outline.
2. Use and maintain `compact-source-crosswalk.md`.
3. Use `source-ledger.md` for citation-ready source notes.
4. Use `evidence-memo-template.md` before drafting research-gap claim clusters.
5. Maintain `case-dossier-support-exception.md` as the running example scaffold.
6. Use `positioning-memo.md` when checking whether a chapter's point of view is
   differentiated.
7. Use `storytelling-guide.md` and
   `reviews/storytelling-review-batch-2.md` when choosing examples for each
   chapter.
8. Use `style-guide.md` as the chapter voice and line-editing standard.
9. Create chapter packets for Chapters 5-9 first.
10. Draft Chapter 5 first, because "Evidence, Freshness, And Context" is the
   clearest expression of the book's central standard in the compact outline.
11. Use the Chapter 5 draft to validate the chapter template, story pattern,
   and voice.
12. Adjust the outline only after that test chapter reveals what the book wants
   to be.

## Sources Used For This Guide

- Jane Friedman, "Start Here: How to Write a Book Proposal":
  https://janefriedman.com/start-here-how-to-write-a-book-proposal/
- Jane Friedman, "How to Write a Nonfiction Book Chapter Without Tears":
  https://janefriedman.com/how-to-write-a-nonfiction-book-chapter-without-tears/
- University of Chicago Press, "Submit a book proposal or manuscript":
  https://press.uchicago.edu/resource/books_submissions.html
- University of Chicago Press, "Manuscript Preparation Guidelines":
  https://press.uchicago.edu/resource/emsguide.html
- Chicago Manual of Style, "Manuscript Preparation":
  https://www.chicagomanualofstyle.org/help-tools/manuscript-prep.html
- Purdue OWL, "The Writing Process":
  https://owl.purdue.edu/owl/general_writing/the_writing_process/index.html
- Purdue OWL, "Stages of the Writing Process":
  https://owl.purdue.edu/owl/multilingual/multilingual_students/key_concepts_for_writing_in_north_american_colleges/stages_of_the_writing_process.html
- Purdue OWL, "Organization and Structure":
  https://owl.purdue.edu/owl/graduate_writing/graduate_writing_topics/graduate_writing_organization_structure_new.html
- University of Wisconsin Writing Center, "Creating a Reverse Outline":
  https://writing.wisc.edu/handbook/reverseoutlines/
- PLOS SciComm, "Want to Write a Popular Science Book?":
  https://scicomm.plos.org/2019/08/27/want-to-write-a-popular-science-book-heres-what-you-need-to-know/
- PLOS Computational Biology, "Ten simple rules for writing a popular science
  book":
  https://pmc.ncbi.nlm.nih.gov/articles/PMC5794074/
