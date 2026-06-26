# Production Readiness Plan

Purpose: define the production, Amazon, beta-review, and low-friction launch
work required for self-publishing.

## Publication Decisions

| Decision | Choice | Rationale |
| --- | --- | --- |
| Primary success definition | Credibility and durable public thesis first; royalties second. | This is B2B technical nonfiction. Strategic value is more likely to come from trust, artifacts, and qualified reader conversations than Amazon royalties alone. |
| Final manuscript length | 38,000-44,000 words. | A concise field guide fits the promise better than padding to the old 65,000-word target. |
| Citation style | Chicago author-date. | It is already used in chapter prose and works for research-backed self-published nonfiction. |
| Production path | Markdown source to PDF/EPUB/KPF through a repeatable local or external formatter. | The repo keeps Markdown as source of truth; final layout can use a purpose-built formatter. |
| KDP Select | Use `amazon-production-readiness-plan.md` as the current Amazon-specific decision record; current recommendation is no KDP Select for the first 90 days unless the launch is Amazon-only and review acquisition is the only near-term goal. | The likely value is credibility and direct artifacts, not page reads. |
| Pricing assumption | Kindle $9.99; paperback based on print cost plus professional nonfiction margin. | Price should signal serious professional value without requiring a consulting-style funnel. |
| Launch goal | Early reviews and durable public positioning. | Reviews and trust are more important than a one-week sales spike. |

## Amazon Description Draft

**Trust the Process, Not the Prompt** is a practical field guide for leaders and
senior practitioners moving generative AI from experiments into real work.

AI can summarize, draft, synthesize, reason through ambiguity, and prepare work
faster than many organizations can verify it. The risk is not only that AI can
be wrong. The larger operational failure is letting fluent output become
authority before evidence, permissions, review capacity, and accountable
acceptance have caught up.

This book gives teams a way to use AI while treating model output as
probabilistic and without smothering useful work in vague governance. It shows
how to design one workflow where AI output begins as a proposal and becomes
accepted work only through source evidence, freshness checks, effect
boundaries, reviewer capacity, stop conditions, audit, and learning.

Inside, you will learn how to:

- distinguish useful AI work from operational authority;
- decide whether a workflow should use discovery only, draft assistance,
  reviewed proposal, or bounded action;
- map claims to authority-bearing sources;
- build review packets that protect human reviewers instead of overloading
  them;
- measure accepted verified output instead of generated volume;
- detect shadow AI as a pressure signal;
- use evals, incidents, and governance to change workflow behavior;
- design a narrow pilot that can approve, reject, escalate, throttle, or stop.

The book is not a prompt cookbook, a vendor comparison, or a generic AI
governance manifesto. It is about the process that turns probabilistic output
into trusted work.

AI output starts as a proposal. A workflow accepts it for use only after
required evidence is checked and a named owner accepts the decision or
authorizes the permitted effect.

## Metadata Candidates

Category candidates to research in KDP:

- Business & Money / Management & Leadership / Decision-Making & Problem Solving
- Business & Money / Processes & Infrastructure
- Computers & Technology / Computer Science / AI & Machine Learning
- Computers & Technology / Business Technology
- Engineering & Transportation / Engineering / Industrial, Manufacturing &
  Operational Systems

Keyword candidates:

- AI governance
- generative AI at work
- enterprise AI adoption
- AI workflow
- AI risk management
- human in the loop
- AI agents
- AI verification
- AI operations
- responsible AI
- AI productivity
- trustworthy AI
- decision automation
- workflow governance
- AI review process

## Author Central Draft

Benjamin Luchterhand writes about AI workflows, software engineering, and the
operational controls that let probabilistic systems produce trustworthy work.
His work focuses on the boundary between useful AI output and accepted
authority: evidence, source freshness, permissions, review capacity, audit, and
learning loops.

## Sample Chapter Recommendation

Use one of these:

1. **Opening teardown** if the goal is broad reader conversion. It shows the
   problem quickly and gives an immediate preflight.
2. **Chapter 5: Evidence, Freshness, And Context** if the goal is technical
   credibility. It contains the clearest expression of the book's operating
   standard.
3. **Chapter 12: Designing One Trustworthy Workflow** if the goal is artifact
   value and lead generation.

Recommended public sample: opening teardown plus the decision-acceptance
preflight.

## Controlled Beta Reader Plan

Target 8-12 early readers before public launch:

- two engineering or platform leaders;
- one product or operations leader;
- one security, governance, risk, or compliance reader;
- one AI practitioner building with tools or agents;
- one skeptical executive/operator;
- one technical writer or editor;
- one non-specialist business reader.

Request private beta feedback after readers have read enough to judge the book.
Amazon review requests belong to the separate launch review-copy plan. Do not
ask for positive reviews.

## Beta-Reader Brief

Ask each beta reader:

1. Where did you lose trust in the argument?
2. Where did you get bored or feel the book repeated itself?
3. Which artifact would you actually use on Monday?
4. Which claim felt unsupported or too strong?
5. Which term was unclear or used inconsistently?
6. Which chapter felt most useful?
7. Which chapter could be shorter?
8. What would make you recommend this to a colleague?

Triage feedback as factual, structural, evidence, terminology, reader-handle,
style, production, or preference.

## Low-Friction Launch Plan

This launch avoids high-interpersonal-load tactics.

1. Publish a simple landing page with book promise, sample chapter, artifacts,
   and Amazon link.
2. Release the downloadable artifact bundle.
3. Publish 8-12 essays adapted from the chapters:
   - AI outputs are proposals, not authority.
   - Human in the loop is not a control.
   - Context is not evidence.
   - Better prompts are not decision controls.
   - Review capacity is the AI adoption bottleneck.
   - Accepted verified output beats generated volume.
   - Shadow AI is a pressure signal.
   - Why agents need effect boundaries.
   - Governance counts only when it changes workflow behavior.
   - Start with one workflow.
4. Run the separate launch review-copy funnel: contact about 40 candidates,
   expect 25-30 accepted review-copy readers, and target 10-15 honest Amazon
   reviews.
5. Wait for honest reviews and page conversion before running paid ads.
6. Test Amazon Sponsored Products with a small budget only after reviews,
   metadata, sample, and description are stable.

## Production Checklist

Ebook:

- Convert Markdown source into EPUB/KPF.
- Inspect title page, table of contents, headings, tables, citations, links,
  and appendix formatting.
- Check the Kindle preview on phone, tablet, and e-reader sizes.

Paperback:

- Choose trim size, likely 6x9 inches for professional nonfiction.
- Set margins, running headers, page numbers, table handling, and bibliography
  formatting.
- Create full paperback wrap with spine and back-cover copy.
- Upload to KDP and inspect digital proof.
- Order a physical proof before public launch.

Cover:

- Keep the current sober technical direction only if the thumbnail remains
  legible.
- Produce final ebook cover at KDP dimensions.
- Produce paperback wrap after final page count.
- Check title, subtitle, author name, contrast, and spine readability.

External actions that cannot be completed inside this workspace:

- KDP account setup.
- Final source/archive verification for command-line-blocked, paywalled, and
  fast-moving citation links.
- Final category selection inside KDP's current UI.
- Digital KDP preview after upload.
- Physical proof order and inspection.
- External beta-feedback triage before public launch.
- Public review collection.
