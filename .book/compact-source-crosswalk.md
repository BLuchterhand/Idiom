# Compact Source Crosswalk

Canonical outline: `outline-v2-compact.md`

Purpose: map the existing source pool in `research-sources.md` onto the compact
chapter structure. This does not replace the full bibliography.

Status note: this is a planning record for source routing and claim gates. The
current manuscript readiness state is tracked in `publication-readiness-tracker.md`
and the chapter drafts themselves.

Source status labels:

- **Ready**: enough existing sources for a careful draft.
- **Draft with caveat**: usable sources exist, but claims should be qualified.
- **Research gap**: gather more sources before making strong claims.

Claim gate: when a chapter is marked **Research gap**, draft its core claim
only as a design thesis, operating hypothesis, or normative control standard
until the gap is closed. Verbs such as "is," "becomes," "must," "default," and
"equilibrium" should be weakened unless the mapped evidence directly supports
them.

## Opening Teardown: The Fluent Output That Escaped

Use existing sources:

- **A2**, **A3**, **A4** for plausible but unsupported generated text.
- **H1**, **H2**, **H3**, **H4** for human oversight and automation reliance.
- **G1**, **G2**, **G3** for lifecycle governance and operational controls.
- **S1**, **S2**, **S3** for operational feedback, risk, and incident learning.

Status: **Draft with caveat**.

Evidence gaps:

- Customer-support exception handling and QA evidence.
- Shadow AI or shadow IT evidence.
- Reviewer-capacity and escalation queue evidence.

## Chapter 1: The Category Error

Use existing sources:

- **A0**, **A0b**, **A0c** for transformer and language-model grounding.
- **A0d**, **A0e**, **A0f** for the careful "not just autocomplete" balance.
- **A1**, **A2**, **A3**, **A4** for fluency, hallucination, factuality, and
  limitations.
- **A5** for calibration and evaluation framing.

Status: **Draft with caveat**.

Evidence gaps:

- Stronger LLM calibration and uncertainty sources.
- Primary source verification for Othello-GPT or any world-model example.
- Careful treatment of debated emergence claims.

Drafting guardrail: use "can exhibit capabilities consistent with..." rather
than "has human-like reasoning."

## Chapter 2: How Important Work Earns Trust

Use existing sources:

- **S1**, **S2**, **S3**, **S4**, **S5**, **S6** for software and operations
  reliability practices.
- **H1**, **H2**, **H3**, **H4** for human factors, oversight, workload, and
  reliance.
- **G1**, **G3**, **G4** for governance, accountability, and management systems.

Status: **Draft with caveat**.

Evidence gaps:

- Aviation, medicine, finance, law, and journalism sidebars need domain-specific
  sources before use.
- Sources on control degradation under pressure, normalization of deviance,
  audit theater, and reviewer overload.

Drafting guardrail: keep the primary proof in enterprise software and
operations. Use other professions only as sourced sidebars that include both the
control and its failure mode.

## Chapter 3: When Artifact Fixes Do Not Control The Decision

Use existing sources:

- **A6** for retrieval-augmented generation as a useful but incomplete control.
- **A7**, **A8**, **A9**, **A10** for documentation, system cards, and
  deployment mitigations.
- **G1**, **G2**, **G3** for governance and lifecycle controls.
- **H3**, **H5**, **H6** for appropriate reliance and human-AI interaction.
- **X1**, **X2** for security and excessive-agency risks.

Status: **Draft with caveat**.

Evidence gaps:

- Examples where eval dashboards, policies, or RAG failed because authority
  boundaries remained weak.
- Examples where human-in-the-loop labels failed because reviewers lacked
  evidence, time, independence, or authority.

Drafting guardrail: treat artifact fixes as useful improvements, not strawmen.

## Chapter 4: The Shape Of A Good AI Task

Use existing sources:

- **D1**, **D2**, **D3** for productivity variation across task shape and context.
- **H5**, **H6**, **H7** for human-centered allocation and interaction patterns.
- **S2**, **S5**, **S6** for risk, feedback, and delivery systems.
- **A5**, **A10** for evaluation and deployment mitigations.

Status: **Split status**.

- Bounded-agent and security material: **Draft with caveat**.
- Shadow AI, bypass loops, procurement/DLP signals, and tool rationalization:
  **Draft with caveat** as plausible pressure patterns to test.

Evidence gaps:

- Broader task-fit studies outside software development.
- Operational decision workflow and support-QA evidence should remain scoped to
  the cited support-center standards and the labeled composite case.

## Chapter 5: Evidence, Freshness, And Context

Use existing sources:

- **A2**, **A3** for hallucination and factuality.
- **A6** for retrieval and updateable knowledge.
- **A7**, **A8**, **A9** for provenance, documentation, and intended use.
- **G2** for generative-AI information integrity.
- **S1**, **S3** for operational evidence and incident learning.

Status: **Draft with caveat**.

Evidence gaps:

- Domain-specific sources for customer contract, billing, policy, and support
  records as separate sources of truth.

Drafting guardrail: keep evidence, verification, acceptance, and authority
distinct.

## Chapter 6: The Responsibility And Incentive Split

Use existing sources:

- **G1**, **G3**, **G4** for accountability and governance.
- **H1**, **H2**, **H3**, **H4** for human oversight and responsibility under
  automation.
- **S2**, **S3**, **S4**, **S6** for risk ownership, incident learning, review,
  and culture.
- **D3**, **D7**, **D9** for organizational adoption and human-AI task
  allocation.

Status: **Draft with caveat** for the incentive half.

Evidence gaps:

- Incentive misalignment, metric gaming, named accountability without control,
  and budget ownership for controls are supported as design/control risks, not
  measured universal failure rates.
- Evidence on who pays review cost versus who receives AI productivity benefit
  remains assumption-labeled.

Drafting guardrail: present the five-right accountability rule as the book's
normative control standard until stronger organizational-control sources are
added.

## Chapter 7: Effect Boundaries For AI Workflows

Use existing sources:

- **G1**, **G2**, **G3** for risk management and operational governance.
- **X1**, **X2**, **X3** for prompt injection, excessive agency, secure AI
  lifecycle, and adversarial risk.
- **A10** for system-level mitigations and monitoring.
- **S1**, **S2**, **S5** for operational controls and feedback loops.

Status: **Draft with caveat**.

Evidence gaps:

- Case examples for support or operational tools where action authority needed
  explicit permission boundaries.

## Chapter 8: Measuring Accepted Verified Output

Use existing sources:

- **A5** for evaluation discipline.
- **D1**, **D2**, **D3** for AI productivity and context-dependent value.
- **S1**, **S3**, **S4**, **S5**, **S6** for monitoring, postmortems, review,
  delivery, and organizational performance.
- **G1**, **G2** for lifecycle measurement and risk controls.

Status: **Draft with caveat** for the strongest economic claims.

Evidence gaps:

- ROI and broad economic claims remain out of scope unless separately sourced.
- Review queue, inspection bottleneck, and customer-support economics should be
  framed as operating-model risks or assumptions, not as quantified proof.

Drafting guardrail: phrase the central claim as "accepted verified output is
the proposed metric for consequential workflows with verification bottlenecks"
or "verification is the scarce adoption capability in many consequential
workflows" until the verification-economics source base is stronger.

## Chapter 9: Conditions For Useful Human Review

Use existing sources:

- **H1**, **H2**, **H3**, **H4** for automation bias, reliance, workload, and
  out-of-the-loop risks.
- **H5**, **H6**, **H7** for human-centered review and interaction.
- **S4** for code review as a structured human control.
- **G2** for human oversight in generative-AI risk management.

Status: **Draft with caveat** for modern GenAI review claims.

Evidence gaps:

- GenAI-specific legal and medical review examples can support narrow claims.
- Broader human-review failure claims should remain review-condition design
  standards unless the source ledger directly supports the domain.
- Studies where AI assistance changed reviewer confidence or error detection.

Drafting guardrail: use "human review can fail..." until modern GenAI evidence
supports stronger wording.

## Chapter 10: From Assistant To Bounded Agent

Use existing sources:

- **X1**, **X2**, **X3** for excessive agency, prompt injection, secure
  development, and adversarial tactics.
- **A10** for deployment mitigations and monitoring.
- **H1**, **H4** for handoff and out-of-the-loop risks.
- **G1**, **G2**, **G3** for governance and lifecycle management.
- **D3**, **D4**, **D7** for enterprise adoption and agentic AI context.

Status: **Draft with caveat**.

Evidence gaps:

- Shadow AI / shadow IT evidence.
- Case evidence for bypass loops when official AI workflows are too slow.
- Procurement, DLP, or governance signals for unofficial AI use.
- Enterprise tool-sprawl and AI-tool rationalization evidence.

Drafting guardrail: describe the shadow-AI loop as a plausible pressure pattern
to test until sourced as a demonstrated default.

## Chapter 11: Evals, Incidents, And Workflow Governance

Use existing sources:

- **G1**, **G2**, **G3**, **G5**, **G6**, **G8a**, **G8b**, **G9** for AI risk
  management, incidents, regulation, agency guidance, acquisition, and policy.
- **S1**, **S3**, **S6** for monitoring, postmortem culture, and organizational
  performance.
- **A5**, **A10** for evals and system-level mitigations.
- **D3**, **D7** for organizational adoption and production gaps.

Status: **Draft with caveat** for governance-theater claims.

Evidence gaps:

- Governance-theater, metric-gaming, and incentive-pressure claims should be
  framed as operating risks supported by internal-control, model-risk, and case
  evidence, not as measured universal AI-adoption outcomes.
- Governance arriving after tool adoption.
- Budget ownership for controls versus benefits.
- Evidence for control validation practices: sampling accepted decisions,
  reviewer calibration, stale-source drills, bypass detection, and control
  retirement.

Drafting guardrail: source standards for what governance should include; source
organizational failure literature or incidents for why artifacts fail.

## Chapter 12: Designing One Trustworthy Workflow

Use existing sources:

- All sources used in Chapters 4-11.
- **D3**, **D7**, **D9** for adoption maturity, production gap, and human-AI task
  allocation.
- **G1**, **G2**, **G3** for governance and lifecycle scaffolding.
- **S2**, **S3**, **S5**, **S6** for risk, learning, delivery, and culture.

Status: **Draft with caveat**.

Evidence gaps:

- Practical examples of staged AI workflow rollout in enterprise operations.
- Evidence for maturity models, adoption sequencing, and workflow redesign.

Drafting guardrail: Chapter 12 should synthesize the book into a Monday method,
not introduce a new framework.

## Closing: Working With The Machine

Use existing sources:

- No heavy new evidence should be introduced.
- Reuse the book's strongest sources from Chapters 1, 5, 8, 9, and 11.

Status: **Ready after full draft**.

Drafting guardrail: the closing should resolve the reader's stance, not summarize
every mechanism.

## Highest Priority Research Before Drafting

1. Verification cost and review bottlenecks.
2. Modern GenAI human-review failure.
3. Organizational incentives, artifact ceremony, and shadow AI.
4. LLM calibration and uncertainty.
5. Customer support / operational decision workflow evidence.
