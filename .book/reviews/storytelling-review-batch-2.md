# Storytelling Review Batch 2

Supersession note: this is a historical review record. Several watch items
below have since been addressed; use `publication-readiness-tracker.md` and
`package-manifest.md` for current beta/proof readiness status.

## Review Scope

Five reviewers checked the storytelling pass from editorial, antagonistic,
executive/operator, evidence-rigor, and whole-book-flow perspectives.

Reviewed materials included:

- `storytelling-guide.md`
- `outline-v2-compact.md`
- `book-brief.md`
- `writing-guide.md`
- `sidecar-human-process-parallel.md`
- `case-dossier-support-exception.md`
- `argument-map.md`
- `north-star.md`
- `compact-source-crosswalk.md`
- `source-gap-backlog.md`

## Bottom Line

The storytelling direction is right. Professional readers are most likely to
trust the book when each major assertion begins with a visible workplace moment,
then shows the human-control pattern, then translates it to AI.

The correction is important: the book should not imply that organizations
already solved these problems with people. The stronger and more honest claim
is that mature work learned the shape of the problem with people, imperfectly.
AI changes the mechanics through scale, fluency, opacity, tool authority,
retrieval behavior, vendor drift, data leakage, prompt injection, and
non-human accountability.

## Prioritized Findings

### 1. Do Not Overclaim The Human-Control Analogy

Risk: older wording implied that organizations had already solved this problem
with people. That sounds like institutional nostalgia and ignores audit theater,
rubber-stamp review, misaligned incentives, and weak rejection authority.

Decision: change the frame to:

> We learned the shape of this problem with people, imperfectly.

Required changes:

- Use "mature, high-consequence workflows often..." rather than universal
  claims about all serious organizations.
- Treat human controls as fallible systems that need validation.
- Make the analogy break part of the main argument, not a safety note.

### 2. Make First Visible Moment Mandatory

Risk: the guide defines a scene-first pattern, but the outline still lets
chapters begin with abstractions such as "evidence hierarchy," "rails," and
"governance."

Decision: every chapter packet needs a first visible moment: person, artifact,
deadline, and consequence.

Examples:

- Chapter 5: an analyst uses the available spreadsheet instead of the
  authoritative source.
- Chapter 7: an employee can draft a payment request but cannot approve,
  release, and reconcile payment alone.
- Chapter 8: a queue report shows more AI proposals than reviewer-hours.

### 3. Separate Chapters 6, 8, And 9

Risk: responsibility, review capacity, and human review can blur into one
repeated "reviewer under pressure" argument.

Decision:

- Chapter 6 owns the ownership meeting: who has budget, veto, escalation, and
  rejection authority.
- Chapter 8 owns queue economics: proposal volume, reviewer-hours, WIP,
  throttles, incomplete-packet rejection, and accepted verified output as a
  proposed metric.
- Chapter 9 owns reviewer ergonomics: claim list, source links, stale-source
  warnings, reject/escalate controls, and rejection authority.

### 4. Treat The Support Case As Composite Until Sourced

Risk: the running support exception case is detailed enough to feel like proof
before support-workflow sources have been gathered.

Decision:

- Label the case as a composite design scenario until sourced.
- Require source work for support QA/escalation, service-credit or policy
  exception handling, audit/source-of-truth practices, and review queue
  capacity.
- Use the support case as the spine, not the only proof.

### 5. Make Controls Look Like Throughput Tools, Not Only Safety Tools

Risk: operators may hear the book as governance telling them to add approval
debt while executives still demand ROI.

Decision:

- Show where controls cost time.
- Show where controls save time by reducing rework, preventing escalations, and
  speeding acceptance of complete packets.
- Put numbers into Chapter 8 and Chapter 12.
- Include the "do not automate" countercase and the "controls save time"
  countercase in the main story arc.

### 6. Couple Model Quality And Workflow Control

Risk: "not a model-selection problem" can sound like process dismissing
engineering reality.

Decision:

- Use "model selection matters, but it is not enough."
- Treat model capability, calibration, retrieval quality, latency, tool
  reliability, and decision acceptance as coupled economics.

### 7. Give Chapter 12 A Concrete Final Payoff

Risk: the final chapter could become a checklist dump.

Decision:

- Draft Chapter 12 as a 30-day operating narrative before presenting the full
  canvas.
- End with a completed decision packet and one concrete decision under
  pressure: approve, reject, escalate, throttle, or stop.
- The payoff should clarify authority, protect the reviewer, close or absorb a
  shadow path, and deliberately reject at least one tempting automation step.

## Evidence Gates Added

- **Analogy gate:** non-software analogies need a source for the control, a
  source or incident for failure/degradation, and a sentence naming where the
  AI analogy breaks.
- **Case gate:** recurring support-case details must be sourced, composite, or
  hypothetical.
- **Verb-strength gate:** avoid "always," "never," "must," "the metric," and
  "often" unless the source map supports the claim.
- **Human-control gate:** distinguish normative control standards from
  empirical claims about what organizations actually do.
- **Survey/vendor gate:** consulting surveys are adoption color; vendor system
  cards are vendor claims.

## Implementation Notes

Changes integrated after this batch:

- `storytelling-guide.md`: softened the human-control claim, promoted analogy
  breaks, added evidence discipline, and bounded "accepted verified output" as
  a proposed metric for consequential workflows.
- `writing-guide.md`: added first visible moment, analogy break, operator
  artifact, and evidence status to the chapter packet and draft template.
- `outline-v2-compact.md`: softened model-selection language, labeled the
  support case as composite until sourced, added chapter visual jobs, separated
  Chapters 6/8/9, added a Chapter 10 bypass scene, and made Chapter 12 a
  30-day operating narrative with a final decision payoff.

## Remaining Watch Items

- Chapter 1 must open with a workplace scene before model mechanics.
- Chapter 3 must show artifact fixes helping the output without controlling the
  decision.
- Chapter 4 should use an intake/work-order scene, not a junior-employee
  analogy.
- Chapter 8 needs a numerical review-capacity example.
- Chapter 12 needs the completed decision packet and stop/go beat.
