# Research Sources For Effective AI Adoption

This bibliography is organized for drafting. It defines reusable source IDs with
short annotations.

For the current chapter drafting structure, use
`compact-source-crosswalk.md`.

Selection bias: prefer primary academic papers, official standards and policy
documents, government guidance, established research programs, and reputable
field studies. Consulting and vendor sources are included only where they add
useful adoption data or practitioner framing.

## Source Pool

### Governance, Standards, And Policy

- **G1. NIST AI Risk Management Framework 1.0**
  Source: NIST, 2023.
  Link: https://www.nist.gov/itl/ai-risk-management-framework
  Use for: risk framing, trustworthiness characteristics, governance,
  measurement, and lifecycle management.

- **G2. NIST AI RMF Generative AI Profile**
  Source: NIST AI 600-1, 2024.
  Link: https://www.nist.gov/publications/artificial-intelligence-risk-management-framework-generative-artificial-intelligence
  Use for: genAI-specific risks, lifecycle controls, hallucination,
  information integrity, cybersecurity, and human oversight.

- **G3. ISO/IEC 42001:2023 AI Management Systems**
  Source: ISO overview.
  Link: https://www.iso.org/artificial-intelligence/ai-management-systems
  Use for: organizational AI management systems, accountability, continuous
  improvement, policies, and operational governance.

- **G4. OECD AI Principles**
  Source: OECD.AI.
  Link: https://oecd.ai/en/ai-principles
  Use for: trustworthy AI principles, human rights, transparency,
  accountability, robustness, and responsible stewardship.

- **G5. OECD AI Incidents Monitor**
  Source: OECD.
  Link: https://www.oecd.org/en/topics/sub-issues/ai-risks-and-incidents.html
  Use for: incident evidence, real-world failure patterns, and why adoption
  needs monitoring rather than static policy.

- **G6. EU AI Act**
  Source: European Commission, Regulation (EU) 2024/1689 overview.
  Link: https://digital-strategy.ec.europa.eu/en/policies/regulatory-framework-ai
  Use for: risk-based regulation, high-risk systems, prohibited practices,
  transparency obligations, and governance maturity.

- **G7. U.S. Executive Order 14110 Historical Context**
  Source: Federal Register, 2023, revoked in 2025.
  Link: https://www.federalregister.gov/documents/2023/11/01/2023-24283/safe-secure-and-trustworthy-development-and-use-of-artificial-intelligence
  Use for: historical Biden-era U.S. policy framing around safe, secure, and
  trustworthy AI. Do not present as current U.S. executive policy.

- **G7a. Executive Order 14179: Removing Barriers To American Leadership In AI**
  Source: White House, 2025.
  Link: https://www.whitehouse.gov/presidential-actions/2025/01/removing-barriers-to-american-leadership-in-artificial-intelligence/
  Use for: current post-EO-14110 U.S. executive policy direction and the
  revocation context for prior AI directives.

- **G8. OMB M-24-10 Historical Agency Use Of AI Guidance**
  Source: U.S. Office of Management and Budget, 2024, superseded in 2025.
  Link: https://www.whitehouse.gov/wp-content/uploads/2024/03/M-24-10-Advancing-Governance-Innovation-and-Risk-Management-for-Agency-Use-of-Artificial-Intelligence.pdf
  Use for: historical agency minimum risk-management practices,
  rights-and-safety-impacting AI, governance bodies, inventories, and
  validation. Do not present as current agency guidance.

- **G8a. OMB M-25-21: Accelerating Federal Use Of AI**
  Source: U.S. Office of Management and Budget, 2025.
  Link: https://www.whitehouse.gov/wp-content/uploads/2025/02/M-25-21-Accelerating-Federal-Use-of-AI-through-Innovation-Governance-and-Public-Trust.pdf
  Use for: current federal agency AI-use guidance, including governance,
  public trust, proportionate safeguards, and discontinuance when mitigation is
  not possible.

- **G8b. OMB M-25-22: Efficient Acquisition Of AI In Government**
  Source: U.S. Office of Management and Budget, 2025.
  Link: https://www.whitehouse.gov/wp-content/uploads/2025/02/M-25-22-Driving-Efficient-Acquisition-of-Artificial-Intelligence-in-Government.pdf
  Use for: current federal AI acquisition guidance, procurement documentation,
  transparency, performance tracking, and vendor evaluation.

- **G9. UK Pro-Innovation Approach To AI Regulation**
  Source: GOV.UK, 2023 white paper and government response.
  Link: https://www.gov.uk/government/publications/ai-regulation-a-pro-innovation-approach/white-paper
  Use for: principles-based governance and regulator coordination.

- **G10. World Economic Forum AI Governance Alliance Briefing Series**
  Source: WEF, 2024.
  Link: https://www.weforum.org/publications/ai-governance-alliance-briefing-paper-series/
  Use for: multi-stakeholder governance, responsible adoption, and global
  coordination themes.

### AI Capability, Risk, And Evaluation

- **A0. Attention Is All You Need**
  Source: Vaswani et al., NeurIPS 2017.
  Link: https://arxiv.org/abs/1706.03762
  Use for: transformer architecture as historical/mechanistic grounding. Cite
  sparingly; the book should not become a model-architecture explainer.

- **A0b. Language Models Are Unsupervised Multitask Learners**
  Source: Radford et al., OpenAI, 2019.
  Link: https://cdn.openai.com/better-language-models/language_models_are_unsupervised_multitask_learners.pdf
  Use for: language modeling objective and scaling-era framing of predicting
  text from broad data. Treat as a primary historical/vendor source, not a
  neutral evaluation.

- **A0c. Language Models Are Few-Shot Learners**
  Source: Brown et al., NeurIPS 2020.
  Link: https://arxiv.org/abs/2005.14165
  Use for: next-token prediction at scale, in-context learning, broad task
  capability, and the gap between capability and reliability.

- **A0d. Emergent Abilities Of Large Language Models**
  Source: Wei et al., 2022.
  Link: https://arxiv.org/abs/2206.07682
  Use for: capability emergence with scale. Use carefully; emergence claims are
  debated and should not be overstated.

- **A0e. Language Models Represent Space And Time**
  Source: Gurnee and Tegmark, ICLR 2024.
  Link: https://arxiv.org/abs/2310.02207
  Use for: evidence that LLMs can learn internal spatial and temporal
  representations, helping avoid the misleading "just autocomplete" framing.

- **A0f. Othello-GPT / Emergent World Representations**
  Source: Li et al. / follow-up mechanistic interpretability discussion.
  Link: https://thegradient.pub/othello/
  Use for: accessible example of a sequence model trained on move prediction
  learning an internal game-state representation. Treat as illustrative and
  verify primary-paper details before formal citation.

- **A1. On The Dangers Of Stochastic Parrots**
  Source: Bender, Gebru, McMillan-Major, and Shmitchell, FAccT 2021.
  Link: https://dl.acm.org/doi/10.1145/3442188.3445922
  Use for: limits of language-model meaning, risks of scale, dataset
  documentation, and why fluency is not truth.

- **A2. Survey Of Hallucination In Natural Language Generation**
  Source: Ji et al., ACM Computing Surveys, 2023.
  Link: https://dl.acm.org/doi/10.1145/3571730
  Use for: hallucination definitions, causes, measurement, and mitigation
  approaches across generation tasks.

- **A3. On Faithfulness And Factuality In Abstractive Summarization**
  Source: Maynez et al., ACL 2020.
  Link: https://aclanthology.org/2020.acl-main.173/
  Use for: summarization-specific factuality failures and why fluent summaries
  need source-grounded checks.

- **A4. GPT-4 Technical Report**
  Source: OpenAI, 2023.
  Link: https://arxiv.org/abs/2303.08774
  Use for: high capability alongside explicit limitations such as
  hallucination, limited context, and lack of learning from experience.

- **A5. Holistic Evaluation Of Language Models (HELM)**
  Source: Stanford CRFM, 2022.
  Link: https://arxiv.org/abs/2211.09110
  Use for: evaluation beyond accuracy, standardized scenarios, robustness,
  calibration, fairness, toxicity, and efficiency.

- **A6. Retrieval-Augmented Generation For Knowledge-Intensive NLP Tasks**
  Source: Lewis et al., NeurIPS 2020.
  Link: https://arxiv.org/abs/2005.11401
  Use for: grounding model outputs in non-parametric memory, provenance, and
  updateable knowledge.

- **A7. Model Cards For Model Reporting**
  Source: Mitchell et al., FAT* 2019.
  Link: https://arxiv.org/abs/1810.03993
  Use for: intended use, limitations, evaluation conditions, and transparent
  model reporting.

- **A8. Datasheets For Datasets**
  Source: Gebru et al., Communications of the ACM, 2021.
  Link: https://cacm.acm.org/research/datasheets-for-datasets/
  Use for: data provenance, collection process, recommended uses, and
  transparency as adoption infrastructure.

- **A9. FactSheets For AI Services**
  Source: IBM Research, 2019.
  Link: https://research.ibm.com/blog/factsheets-ai
  Use for: supplier declarations, purpose, performance, safety, security,
  provenance, and governance records.

- **A10. GPT-4 System Card**
  Source: OpenAI, 2023.
  Link: https://cdn.openai.com/papers/gpt-4-system-card.pdf
  Use for: system-level risk analysis, red teaming, deployment mitigations,
  monitoring, and non-model controls.

### Human Factors, Trust, And Human-AI Interaction

- **H1. Ironies Of Automation**
  Source: Bainbridge, Automatica, 1983.
  Link: https://ckrybus.com/static/papers/Bainbridge_1983_Automatica.pdf
  Use for: automation can increase human responsibility while degrading human
  readiness; crucial for AI oversight chapters.

- **H2. Humans And Automation: Use, Misuse, Disuse, Abuse**
  Source: Parasuraman and Riley, Human Factors, 1997.
  Link: https://journals.sagepub.com/doi/10.1518/001872097778543886
  Use for: calibrated reliance, misuse, disuse, abuse, workload, and feedback.

- **H3. Trust In Automation: Designing For Appropriate Reliance**
  Source: Lee and See, Human Factors, 2004.
  Link: https://journals.sagepub.com/doi/10.1518/hfes.46.1.50_30392
  Use for: appropriate reliance, trust calibration, specificity, and
  resolution.

- **H4. From Here To Autonomy**
  Source: Endsley, Human Factors, 2017.
  Link: https://journals.sagepub.com/doi/abs/10.1177/0018720816681350
  Use for: situation awareness, out-of-the-loop problems, and human oversight
  of autonomous systems.

- **H5. Guidelines For Human-AI Interaction**
  Source: Amershi et al., CHI 2019 / Microsoft Research.
  Link: https://www.microsoft.com/en-us/research/publication/guidelines-for-human-ai-interaction/
  Use for: practical product design guidelines, expectation setting, correction,
  graceful failure, and adapting over time.

- **H6. People + AI Guidebook**
  Source: Google PAIR.
  Link: https://pair.withgoogle.com/guidebook/
  Use for: human-centered AI product design, user needs, explainability, and
  feedback loops.

- **H7. Human-Centered AI: Reliable, Safe And Trustworthy**
  Source: Ben Shneiderman, Oxford University Press / HCAI overview.
  Link: https://global.oup.com/academic/product/human-centered-ai-9780192845290
  Use for: framing AI as human performance augmentation under reliable, safe,
  and trustworthy design.

### Software Engineering, Operations, And Verification

- **S1. Site Reliability Engineering: Monitoring Distributed Systems**
  Source: Google SRE Book.
  Link: https://sre.google/sre-book/monitoring-distributed-systems/
  Use for: observability, alerting, and why reliable systems need operational
  feedback loops.

- **S2. Site Reliability Engineering: Embracing Risk**
  Source: Google SRE Book.
  Link: https://sre.google/sre-book/embracing-risk/
  Use for: risk budgets, explicit risk appetite, and the idea that reliability
  is engineered through tradeoffs rather than assumed.

- **S3. Site Reliability Workbook: Postmortem Culture**
  Source: Google SRE Workbook.
  Link: https://sre.google/workbook/postmortem-culture/
  Use for: learning from incidents and converting failures into durable system
  improvements.

- **S4. Software Engineering At Google: Code Review**
  Source: Winters, Manshreck, and Wright, 2020.
  Link: https://abseil.io/resources/swe-book/html/ch09.html
  Use for: review as a quality, maintainability, and knowledge-sharing control.

- **S5. DORA Continuous Delivery Capability**
  Source: DORA / Google Cloud.
  Link: https://dora.dev/capabilities/continuous-delivery/
  Use for: fast feedback, process and architecture changes, and automation that
  must be paired with quality systems.

- **S6. Accelerate: State Of DevOps 2018 Report**
  Source: DORA, Forsgren/Humble/Kim research program.
  Link: https://dora.dev/research/2018/dora-report/2018-dora-accelerate-state-of-devops-report.pdf
  Use for: empirical grounding on technical practices, culture, delivery
  performance, and organizational outcomes.

### Adoption, Productivity, And Organizational Change

- **D1. The Impact Of AI On Developer Productivity**
  Source: Peng, Kalliamvakou, Cihon, Demirer, 2023.
  Link: https://arxiv.org/abs/2302.06590
  Use for: controlled evidence that AI can speed up bounded software tasks.

- **D2. Measuring Early-2025 AI On Experienced Open-Source Developers**
  Source: METR, 2025.
  Link: https://metr.org/blog/2025-07-10-early-2025-ai-experienced-os-dev-study/
  Use for: counterpoint showing AI can slow experienced developers on complex,
  mature, high-context work; also useful for perceived versus measured impact.

- **D3. DORA State Of AI-Assisted Software Development 2025**
  Source: DORA / Google Cloud, 2025.
  Link: https://dora.dev/dora-report-2025/
  Use for: AI as an amplifier of existing organizational strengths and
  weaknesses; practices required for value.

- **D4. McKinsey State Of AI 2025**
  Source: McKinsey Global Survey, 2025.
  Link: https://www.mckinsey.com/capabilities/quantumblack/our-insights/the-state-of-ai
  Use for: enterprise adoption patterns, agentic AI, scaling pain, management
  practices, and human validation of model outputs.

- **D5. Stanford AI Index 2026**
  Source: Stanford HAI, 2026.
  Link: https://hai.stanford.edu/ai-index/2026-ai-index-report
  Use for: macro trends in adoption, capabilities, investment, incidents,
  benchmarks, and governance gaps.

- **D6. BCG AI At Work 2025**
  Source: Boston Consulting Group, 2025.
  Link: https://www.bcg.com/publications/2025/ai-at-work-momentum-builds-but-gaps-remain
  Use for: workforce adoption, frontline gaps, agent integration, training,
  workflow redesign, and job-loss concerns.

- **D7. MIT NANDA: The GenAI Divide, State Of AI In Business 2025**
  Source: MIT Project NANDA, 2025, mirrored PDF.
  Link: https://mlq.ai/media/quarterly_decks/v0.1_State_of_AI_in_Business_2025_Report.pdf
  Use for: pilot-to-production gap, integration failures, organizational
  learning, and why adoption is not the same as transformation. Note: the
  previously circulated MIT-hosted URL now redirects to the NANDA overview, so
  re-verify the authoritative host before using this in a formal bibliography.

- **D8. Harvard Business School: Human Judgment Still Drives Innovation**
  Source: Harvard Business School, 2025.
  Link: https://www.hbs.edu/bigs/artificial-intelligence-human-jugment-drives-innovation
  Use for: limits of AI in evaluating ideas and the continuing value of human
  judgment.

- **D9. MIT Sloan: When Humans And AI Work Best Together**
  Source: MIT Sloan, 2025.
  Link: https://mitsloan.mit.edu/ideas-made-to-matter/when-humans-and-ai-work-best-together-and-when-each-better-alone
  Use for: task allocation between humans and AI, augmentation versus
  substitution, and collaboration patterns.

### AI Security And Adversarial Risk

- **X1. OWASP Top 10 For LLM Applications 2025**
  Source: OWASP GenAI Security Project.
  Link: https://owasp.org/www-project-top-10-for-large-language-model-applications/
  Use for: prompt injection, insecure output handling, supply chain,
  excessive agency, sensitive information disclosure, and unbounded
  consumption.

- **X2. Guidelines For Secure AI System Development**
  Source: UK NCSC, CISA, NSA, and international partners, 2023.
  Link: https://www.ncsc.gov.uk/files/Guidelines-for-secure-AI-system-development.pdf
  Use for: secure-by-design AI development, deployment, operation, monitoring,
  incident management, and supply-chain security.

- **X3. MITRE ATLAS**
  Source: MITRE.
  Link: https://atlas.mitre.org/
  Use for: adversarial tactics and techniques against AI-enabled systems.

## Current Chapter Mapping

Use `compact-source-crosswalk.md` for the canonical chapter source mapping.

## Cross-Cutting Reference Themes

### Probabilistic Output Versus Workflow Trust

Primary sources: **A1**, **A2**, **A4**, **H3**, **G1**.

Use this theme whenever the manuscript contrasts fluent generation with evidence, acceptance, and authority.

### Human Oversight Is Not A Magic Control

Primary sources: **H1**, **H2**, **H3**, **H4**, **S4**.

Use this theme to avoid weak "human in the loop" claims. Oversight must preserve situation awareness, evidence access, time, independence, and authority to intervene.

### Context And Provenance Are Adoption Infrastructure

Primary sources: **A6**, **A7**, **A8**, **A9**, **G2**.

Use this theme when arguing that the answer is not just larger prompts or better models, but better source-linked context, provenance, and documentation.
