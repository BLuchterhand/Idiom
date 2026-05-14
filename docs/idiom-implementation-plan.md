# Idiom Implementation Plan

**North Star: AI coding agents are becoming common, but teams do not yet trust
their context, decisions, or outputs. Idiom sells the missing substrate: repo
memory, freshness, contracts, audit, and evals.**

Status: project-defining implementation plan, drafted 2026-05-13

This plan turns [idiom-strategic-thesis.md](idiom-strategic-thesis.md) and
[idiom-design-roadmap.md](idiom-design-roadmap.md) into ordered implementation
work. It is intentionally narrower than the full thesis: the first proof should
show that Idiom can be a local evidence, contract, audit, and sidecar substrate,
not another terminal chat clone.

The controlling rule remains:

- The model owns strategy.
- The harness owns contracts.

## MVP Claim

The first project-defining release should prove this claim:

> Idiom can localize repository work, enforce task contracts, and produce
> auditable improvement signals through a stable local sidecar surface that
> first-party lanes and external agents can both use.

This release is successful only if it produces measurable behavior change:

- fewer broad reads and zero-hit searches in focused implementation runs
- higher `context_pack` top-k coverage of files that actually change
- stable contract and freshness events that replay from logs
- one working external-agent path through the sidecar discovery profile
- at least one verified learning signal feeding ranker state
- evidence that one beachhead buyer segment can activate, understand the value,
  and justify continued use without Idiom becoming another chat UI

## Planning Assumptions

- Go remains the first precision language for implementation and validation.
- Other languages can stay on generic, ctags, structural, or syntax fallback
  until the capability resolver and freshness contracts are stable.
- The first sidecar profile is read-only `discovery`; write and shell profiles
  are explicitly later work.
- Metrics are collected from local logs, evals, telemetry, and workspace tools.
  Hard-coded prose snapshots are not the source of truth.
- Mutable market, audit, benchmark, and tool-surface claims must either be
  dated examples or point to a command/report that regenerates the evidence.
- Public integration surfaces must use the same manifest, schema, permission,
  telemetry, freshness, and audit contracts as first-party lanes.
- Product-success evidence must be collected without raw prompt bodies, raw file
  bodies, secrets, or hosted telemetry as a default.

## Product Success Gates

Product success cannot be guaranteed by planning prose. It can only be
de-risked by refusing to continue unless Idiom proves a buyer-valued wedge. The
technical north star remains the same: repo memory, freshness, contracts, audit,
and evals. The product gate asks whether those substrate primitives create a
reason for a specific audience to install, keep, and pay for Idiom.

MVP beachhead decision:

- Primary MVP ICP: AI-forward enterprise SaaS and DevSecOps platform teams with
  50-500 engineers, active use or pilot use of multiple coding agents, and an
  internal owner for developer platforms, CI, code review, or AI enablement.
- Paid buyer: director or VP-level engineering platform, DevEx, DevSecOps, or
  AI enablement owner who can fund local developer-productivity and governance
  tooling when it produces merge-facing evidence.
- Daily user and champion: staff/principal engineer, senior repo maintainer, or
  platform engineer who already uses Codex, Claude Code, Copilot, Aider,
  OpenCode, local Qwen, or scripts, and who feels the cost of repeated repo
  rediscovery, missing verification, and unreviewable agent output.
- Urgent pain: agents are already entering development workflows, but the team
  cannot consistently prove what context was used, whether the context was
  fresh, which paths were protected, which verification was required, and
  whether the run is safe enough to merge.
- Adoption wedge: a read-only local sidecar workflow for real repositories:
  `context_pack`, `context_status`, `harness_audit`, suggested verification,
  and a privacy-preserving product-evidence export that can be used from a
  shell/CI script or another coding agent. The wedge is not another chat UI.
- Trigger event: a coding-agent pilot expands beyond a few power users, an
  AI-generated pull request creates review or security concern, platform/InfoSec
  asks for evidence before allowing agentic workflows, or a team starts
  comparing several agents and needs one local trust substrate beneath them.
- Buying criteria: first useful packet plus freshness and audit evidence inside
  the P0.10 activation threshold; no raw prompt bodies, raw file bodies, or
  secrets in product evidence; parseable sidecar output for CI/scripts; evidence
  that `context_pack` improves localization over raw search/read; replayable
  contract/freshness/audit events; and at least one external-agent integration
  path by P9.8.
- Explicit MVP non-targets: solo developers seeking a better chat assistant;
  teams with no current or planned coding-agent use; buyers requiring hosted
  dashboards, SSO, fleet policy, procurement, or compliance certification before
  sidecar proof; teams demanding write/shell automation before read-only
  discovery is trusted; regulated teams whose first requirement is formal
  control attestation; and agent-platform vendors seeking SDK/MCP embedding
  before the CLI sidecar contract is stable.
- Final-state expansion: after the enterprise-SaaS sidecar wedge is validated,
  expand to regulated engineering teams through the P0.9/P7.9/P9.9 security and
  privacy path, to tooling vendors through the P7/P8 CLI/MCP parity path, and to
  local-model operators through P6 readiness and capacity gates. Write/verify
  profiles, admin surfaces, and fleet management remain post-substrate proof.

Expected design-partner profile:

- Fits the primary ICP above and can run Idiom locally on a real or
  representative repository without uploading raw source to a hosted service.
- Has at least one active coding-agent workflow or pilot and can name the
  current workaround for context, policy, verification, or audit gaps.
- Can provide P0.8 evidence: activation result, blocked workflow, retained-use
  signal, value signal, and credible paid or internal-platform adoption signal.
- Is disqualified if the desired outcome is mainly a new pair-programming UI,
  a vertical compliance product, a hosted analytics dashboard, or a replacement
  for existing CI/security tools rather than a substrate beneath agents.

Falsifiable continuation criteria:

- Continue full MVP investment when at least three P0.8 design-partner records
  match the primary ICP, at least two reach activation without hand-holding,
  at least one shows repeated weekly or repeated repo use, and at least one
  credible paid or internal-platform adoption signal is recorded by P9.7.
- Pivot to sidecar-only or open-source/tooling-vendor-led packaging if users
  value `context_pack`, freshness, audit, and integration evidence but no paid
  enterprise SaaS buyer owns the problem or budget.
- No-go if activation depends on a chat UI, if buyers do not value local audit
  and repo-memory evidence, if `context_pack` cannot beat ordinary search/read
  on the focused benchmark, or if privacy-safe product evidence cannot be
  produced without raw prompts or file bodies.

Research basis:

These bullets are dated source examples for the draft plan, not evergreen
claims about the live market. P0.3 owns link/source freshness checks; P9.1 must
publish the release-time evidence bundle before any release or sales claim uses
these anchors.

- Stack Overflow's 2025 Developer Survey page is a dated survey anchor reporting
  broad AI-tool adoption, weaker trust, security/privacy concerns, workflow
  integration difficulty, and developer use of agents for software engineering.
  This supports a trust-substrate wedge, not another standalone coding assistant.
- DORA's 2025 report page is a dated report anchor framing AI as an amplifier of
  the underlying engineering system. This supports Idiom's claim that measurable
  process, context, governance, and platform quality matter more than agent
  access alone.
- Stack Overflow's June 2025 agent-integration article is a dated product
  guidance anchor emphasizing data access, performance, zero-trust controls,
  human oversight, monitoring, prompt/memory protection, and
  observable/correctable behavior.
- GitHub Copilot Enterprise docs checked on 2026-05-13 UTC show that enterprise
  buyers already expect usage metrics, policy controls, content exclusions, and
  audit logs around coding assistants. They also state that Copilot audit logs
  do not include local client session prompts and point to custom logging for
  that class of data. This is a sourced market signal for governance demand and
  an inference that a local sidecar can fill a cross-agent evidence gap.
- FINRA's July 2025 GenAI PDF note is a dated regulatory-observation anchor that
  includes coding and states that existing obligations continue to apply,
  supporting audit and evidence needs in finance.
- HHS HIPAA risk-analysis guidance, NIST AI RMF, NIST SP 800-218A, OWASP GenAI
  security guidance, and the MCP 2025-11-25 specification are governance or
  protocol anchors, not proof of product demand. They reinforce the same product
  requirement: local agentic systems need explicit data, tool, consent,
  authorization, and risk controls.
- The METR July 2025 open-source productivity RCT is an arXiv preprint anchor and
  a warning against self-reported productivity claims. Idiom's value must be
  measured against actual task, localization, verification, and exploration
  outcomes.

Product inference from the sources: agent use is common enough to create
trust, workflow, and governance pressure, but the sources do not prove that
buyers will pay for Idiom. P0.8 and P9.7 must prove that the chosen ICP values
local repo memory, freshness, contracts, audit, evals, and sidecar integration
more than another assistant surface.

Product gates:

| Gate | Required Proof | North-Star Fit | Ledger Tasks |
| --- | --- | --- | --- |
| ICP clarity | `enterprise-saas` is the primary paid beachhead; open-source is the adoption wedge; tooling-vendor and local-model are secondary; regulated rows are deferred or proof-only until control evidence exists. Buyer, user, urgent pain, trigger, buying criteria, non-targets, and expansion path are explicit. | Prevents broad assistant drift and keeps work tied to substrate value. | P0.7, P9.12 |
| Design-partner validation | At least three target users or teams matching the primary ICP run the sidecar workflow on real or representative repos; each record is scored with the P0.8 rubric; at least one credible paid or internal-platform adoption signal is recorded. | Proves that audit/context evidence is buyer-valued, not just technically interesting. | P0.8, P9.7 |
| Activation | Clean setup or a clean fixture reaches first useful `context_pack`, freshness check, audit report, suggested verification, product-evidence-safe summary, docs-gap or blocker recording, and cleanup within a measured threshold. Default target: under 10 minutes for CLI-only local use; segment-specific thresholds are P0.10 hypotheses until P7.7/P9.6 prove them. | Makes repo memory and audit visible before any chat-loop polish. | P0.10, P7.7, P9.6 |
| Retained use | A repeat workflow shows weekly or repeated sidecar use on at least one repo, with saved packets/audits providing cumulative value. | Proves persistent memory and audit have durable utility. | P0.8, P7.11, P9.7 |
| Integration proof | Three paths work: shell/CI script, one external coding-agent workflow, and MCP stdio. | Validates "substrate for other agents" rather than "Idiom-only harness." | P7.8, P8.6, P9.8 |
| Security and privacy posture | Threat model, data-flow map, redaction/retention policy, protected-path behavior, and MCP/tool-risk mapping are reviewable. | Converts trust into contracts, not marketing. | P0.9, P7.9, P9.9 |
| Competitive proof | Benchmark compares raw search/read, the generated baseline/current discovery reports, `context_pack`, and at least one Aider-style or agent-native exploration baseline where feasible. | Prevents self-referential success claims. | P4.6, P9.10 |
| Client demo proof | Every client classification matrix row has a runnable transcript with commands, expected output, required evidence fields, value claim, limitations, and go/no-go result. | Turns sales stories into executable substrate examples. | P7.10, P9.11 |
| Packaging and support thesis | MVP/final packaging, pricing hypothesis, support boundary, admin needs, enterprise-surface limits, classification priorities, and go/no-go criteria are explicit and tied to product evidence. | Avoids accidentally building enterprise surface before substrate proof. | P0.11, P9.12 |

Follow-on task implications:

| Task or gate | P0.7 implication |
| --- | --- |
| P0.8 | The validation runbook must screen for the primary ICP, record the buyer/user split, current agent stack, trigger event, activation outcome, retained-use signal, and paid or internal-platform adoption signal. Non-ICP participants can inform pivots but do not satisfy the primary design-partner count. |
| P0.9 | The threat model must support enterprise SaaS and DevSecOps rollout first, then map finance, healthcare, and public-sector examples as deferred regulated expansion proof. |
| P0.10 | Activation thresholds must measure the read-only sidecar wedge: install or clean setup, first useful `context_pack`, freshness check, audit report, suggested verification, product-evidence-safe output, cleanup, docs gap, and blocker reason. |
| P0.11 | Packaging must start with a local CLI sidecar for platform teams and explicit support/admin non-goals; pricing remains a hypothesis tied to P0.8/P9.7 value evidence, not a claim in this plan. |
| P7.10 and P9.11 | Client demos still cover all seven rows, but the enterprise-SaaS demo must be the primary buyer-facing story and other demos must state MVP limitation or final-state expansion. |
| P9.7 and P9.12 | Release cannot claim commercial success unless the primary ICP evidence, adoption signal, non-targets, packaging/support thesis, and continue/pivot/no-go decision are recorded together. |

### P0.8 Design-Partner Validation Protocol

Purpose: validate Idiom as a local evidence and policy substrate for other
coding agents. The protocol must not test whether teams want another coding chat
UI. It tests whether repo memory, freshness, contracts, audit, evals, sidecar
integration, and privacy-preserving product evidence create enough value for the
P0.7 beachhead to continue.

Source basis checked on 2026-05-13 UTC:

- Product Talk product-discovery guidance:
  https://www.producttalk.org/product-discovery/. Method input only: interview
  around concrete customer stories and run assumption tests rather than treating
  solution preference as proof.
- Strategyzer Test Card and Learning Card:
  https://www.strategyzer.com/library/validate-your-ideas-with-the-test-card
  and
  https://www.strategyzer.com/library/capture-customer-insights-and-actions-with-the-learning-card.
  Method input only: write the hypothesis, test, measure, success threshold,
  observation, inference, and action before scaling claims.
- Stack Overflow 2025 AI survey:
  https://survey.stackoverflow.co/2025/ai and DORA 2025:
  https://dora.dev/research/2025/dora-report/. Market-signal inputs only:
  agent use and AI-assisted development are common enough to create workflow
  pressure, but they do not prove Idiom demand.
- GitHub Copilot enterprise audit and metrics docs:
  https://docs.github.com/en/copilot/how-tos/administer-copilot/manage-for-enterprise/review-audit-logs
  and
  https://docs.github.com/en/enterprise-cloud@latest/copilot/reference/metrics-data.
  Governance inputs only: enterprise buyers expect activity, policy, and audit
  evidence; GitHub's audit docs also show gaps around local client-session data.
- OpenTelemetry sensitive-data guidance:
  https://opentelemetry.io/docs/security/handling-sensitive-data/ and NIST
  Privacy Framework: https://www.nist.gov/privacy-framework. Privacy inputs
  only: product evidence should minimize collection, prefer summaries, and make
  retention and redaction explicit.
- OWASP GenAI LLM Top 10 2025:
  https://genai.owasp.org/llm-top-10/
  and MCP 2025-11-25 authorization:
  https://modelcontextprotocol.io/specification/2025-11-25/basic/authorization.
  Control inputs only: agentic systems need explicit data, tool, authorization,
  and protocol boundaries. They are not demand proof.

Design-partner lanes:

| Lane | Counts Toward P9.7 Primary Demand? | What It Tests | Required Handling |
| --- | --- | --- | --- |
| `enterprise-saas` primary paid buyer | Yes, only when the team has active or planned coding-agent use, a platform/DevEx/DevSecOps/AI-enablement owner, a real or representative repo, and a blocked merge-facing workflow. | Paid demand for local sidecar evidence across agents. | Minimum three complete primary records before P9.7 can pass. At least one must include buyer or budget-owner evidence, not only a daily user. |
| `open-source` adoption wedge | No, unless the maintainer is also validating an enterprise-SaaS platform problem in their organization. | Low-friction activation, docs, retained use, and trust value for real repositories. | May support activation, quickstart, demo, and retained-use confidence, but cannot replace paid enterprise-SaaS demand. |
| `tooling-vendor` secondary path | No by default. | CLI/MCP/schema parity, integration packaging, no-strategy-leakage, and partner ecosystem value. | Useful pivot input only if enterprise-SaaS buyer evidence is weak and vendor sponsorship is explicit. |
| `local-model` secondary path | No by default. | Local readiness, capacity, privacy, degraded-mode evidence, and offline workflows. | Useful for privacy/locality claims and P6 gates, not paid beachhead proof by itself. |
| `finance`, `healthcare`, `public-sector` deferred/proof-only rows | No for MVP demand. | Control evidence, protected paths, redaction, retention, and policy posture. | Do not claim regulated demand before P0.9/P7.9/P9.9 control evidence exists. Records may prove non-leakage only. |

Protocol stages:

1. Qualification and consent: classify the participant by matrix row, split buyer
   from daily user, confirm repo/workflow fit, confirm local-only evidence
   boundaries, and record disqualifiers before any activation attempt.
2. Problem interview: capture the concrete trigger event, current coding-agent
   stack, blocked workflow, privacy constraints, current workaround, expected
   evidence, and what would make the participant reject Idiom.
3. Activation attempt: run the read-only sidecar wedge on a real or
   representative repo: `context_pack`, `context_status`, `harness_audit`,
   suggested verification, and any shell/CI or external-agent handoff already
   available. Record command IDs, artifact hashes, packet IDs, freshness state,
   audit event IDs, blockers, and elapsed time.
4. Immediate value scoring: score the record against the rubric below while the
   participant can still challenge the interpretation. Mark every score as
   observed evidence, participant claim, or Idiom hypothesis.
5. Follow-up cadence: schedule retained-use checks at 7 days and 30 days, or at
   the next two real agent-assisted tasks if that happens sooner. A missed
   follow-up is evidence and must be recorded as a blocker or loss of signal.
6. Retained-use check: count only repeated sidecar use on the same or related
   repo where saved packets, freshness, audit, suggested verification,
   integration transcript, or evidence export made a later workflow easier,
   safer, faster, or more reviewable.
7. Decision write-up: record continue, pivot, or no-go rationale and link it to
   P9.7 and P7.11 evidence IDs without copying raw prompts, file bodies, or
   secrets.

Interview and runbook template:

```md
P0.8 validation record

Record metadata:
- Record ID:
- Created/updated date:
- Interviewer:
- Participant pseudonym or approved name:
- Client-classification row: enterprise-saas | open-source | tooling-vendor |
  local-model | finance | healthcare | public-sector
- Evidence status: draft | complete | disqualified | withdrawn

Participant qualification:
- Organization/team size band:
- Primary ICP fit: yes/no, with reason:
- Active or planned coding-agent use: yes/no:
- Repo/workflow available for local sidecar attempt: real | representative |
  none, with reason:
- Consent and data boundary accepted: yes/no:

Buyer/user split:
- Daily user/champion role:
- Buyer/economic owner role:
- Security/platform approver role, if any:
- Who feels the pain:
- Who can sponsor adoption or budget:

Repo and workflow context:
- Repo class and language family:
- Private/public/representative fixture:
- Workflow stage: planning | PR review | CI | incident | migration | other:
- Current verification or review gates:

Current agent stack:
- Agents/tools used today:
- How agents get repo context today:
- How agent output is reviewed today:
- Current telemetry/audit/governance tooling:

Trigger event:
- Specific recent event that made evidence/policy urgent:
- Cost of current workaround:

Blocked workflow:
- Agent workflow the participant cannot trust or scale:
- What evidence is missing today:
- What decision the evidence should support:

Privacy constraints:
- Raw prompts allowed in record: no
- Raw file bodies allowed in record: no
- Secrets or credentials allowed in record: no
- Protected paths or data classes:
- Redaction reviewer:
- Retention exception requested: yes/no, with reason:

Activation attempt:
- Commands or sidecar calls attempted:
- Caller path: local CLI | shell/CI | external coding agent | MCP placeholder:
- Packet IDs, index IDs, audit event IDs, trace IDs:
- Freshness state:
- Suggested verification and exit status, if run:
- Elapsed time:
- Blockers and docs gaps:

Expected evidence:
- Evidence the participant expected before the attempt:
- Evidence Idiom produced:
- Evidence still missing:
- Whether the evidence would change a merge, rollout, or policy decision:

Follow-up cadence:
- 7-day check date or next task trigger:
- 30-day check date or second task trigger:
- Owner for follow-up:

Retained-use check:
- Repeated-use dates or task IDs:
- Same repo or related repo:
- Saved packet/audit/freshness/verification/evidence-export reused:
- User-visible value from reuse:
- Reason for no retained use, if absent:

Adoption and budget signal:
- Daily-user pull:
- Internal sponsor:
- Paid pilot, budget owner, procurement path, or platform-roadmap signal:
- Open-source star/download/community signal, if applicable:
- What would block adoption:

Disqualifiers:
- Wants a chat UI instead of a sidecar substrate:
- No active or planned coding-agent workflow:
- Cannot run local evidence workflow:
- Requires hosted dashboard, SSO, fleet admin, or formal compliance before
  sidecar proof:
- Requires raw prompt/file-body capture:
- Other:

Rubric scores:
- Activation:
- Integration proof:
- Trust/security evidence:
- Repeated use:
- Workflow value:
- Adopt/pay/sponsor:
- Reason-to-pivot pressure:

Decision:
- Continue | pivot input | no-go | insufficient evidence:
- Sourced facts:
- Idiom hypotheses:
- P7.11 export summary fields:
- P9.7 release-gate implication:
```

Value-signal rubric:

Score each criterion from 0 to 3. A primary-ICP record is usable only when
activation, trust/security evidence, and workflow value are each at least 2 and
no disqualifier applies. A strong primary-ICP record also has repeated use or an
adoption/pay/sponsor score of at least 2.

| Criterion | 0 | 1 | 2 | 3 |
| --- | --- | --- | --- | --- |
| Activation | No attempt or cannot run locally. | Demo-only or requires Idiom-team hand-holding. | Participant reaches first useful packet plus freshness/audit on a representative repo. | Participant reaches first useful packet plus freshness/audit on a real repo with minimal hand-holding inside the P0.10 threshold. |
| Integration proof | Used only inside Idiom. | Manual copy/paste into another workflow. | Shell/CI or external-agent handoff consumes parseable evidence. | Repeated external-agent, shell/CI, or MCP-ready path uses the same profile/schema/freshness/audit contract. |
| Trust/security evidence | No trust decision changes. | Participant likes the idea but cannot name decision evidence. | Evidence answers a concrete review, policy, protected-path, freshness, or verification question. | Evidence is sufficient for a platform/security/buyer stakeholder to allow, expand, or govern agent use. |
| Repeated use | No follow-up or no second task. | Follow-up interest only, no actual reuse. | Same user repeats sidecar use on one later task or repo. | Team or maintainer reuses saved packet/audit/freshness/verification evidence across multiple tasks or users. |
| Workflow value | No concrete workflow value. | Convenience value only. | Reduces rediscovery, review ambiguity, verification uncertainty, or audit effort for one real workflow. | Changes a merge, rollout, policy, or agent-selection decision with less risk or waste. |
| Adopt/pay/sponsor internally | No sponsor and no path. | Daily user would try it but no sponsor or budget owner. | Internal platform/security sponsor wants pilot, roadmap slot, or budget discussion. | Paid pilot, procurement step, committed internal platform rollout, or named budget owner. |
| Reason-to-pivot pressure | No conflict with P0.7. | Minor packaging or segment pressure. | Stronger signal from open-source, tooling-vendor, or local-model lane than primary ICP. | Repeated evidence that primary buyers want a different product, mainly a chat UI, hosted admin surface, or formal compliance product. |

Continue, pivot, and no-go thresholds:

- Minimum P9.7 evidence set: at least five complete P0.8 records, including at
  least three `enterprise-saas` primary-ICP records from at least two distinct
  team or company contexts. At least one primary record must include a buyer,
  budget owner, internal-platform owner, or security/platform approver; at least
  two primary records must include daily-user activation attempts; at least one
  record must include retained use.
- Continue: the minimum set exists; at least two primary records score 2 or
  higher on activation, trust/security evidence, and workflow value; at least
  one primary record scores 2 or higher on adopt/pay/sponsor; no high privacy or
  security blocker remains; and open-source evidence supports activation or
  docs without substituting for enterprise demand.
- Pivot: primary records validate the technical substrate but fail buyer,
  budget, or sponsor evidence while `open-source`, `tooling-vendor`, or
  `local-model` records repeatedly score 2 or higher on activation, integration,
  retained use, and workflow value. The pivot must name the replacement beachhead
  and which P0.7/P0.11/P9.12 assumptions changed.
- No-go: fewer than two primary records activate; privacy-safe evidence cannot
  be produced without raw prompts or file bodies; participants mainly ask for a
  chat UI, hosted dashboard, SSO/admin/fleet policy, or compliance product; or
  `context_pack` and audit evidence do not change any concrete workflow decision.

What counts for primary ICP:

- An enterprise SaaS, DevSecOps, platform, DevEx, or AI-enablement team with
  active or planned coding-agent workflows.
- A buyer/user split where either the economic owner or an internal platform
  sponsor can explain how local evidence would affect adoption, policy, review,
  or budget.
- A real or representative repository workflow that can run the read-only
  sidecar wedge locally without uploading raw source to a hosted service.

What does not count:

- One-person hobby use, generic founder calls, survey responses, or demo
  applause without activation.
- Open-source maintainer evidence unless it also includes a named
  enterprise-SaaS platform or buyer context.
- Tooling-vendor integration interest without a buyer/sponsor signal from the
  primary ICP.
- Regulated-sector interest that depends on control attestations not yet
  supplied by P0.9/P7.9/P9.9.
- Any record requiring raw prompts, raw file bodies, secrets, unredacted logs, or
  hosted telemetry as the default evidence source.

Privacy-preserving retained-use evidence format:

| Category | Required Format |
| --- | --- |
| Fields to collect | `record_id`, checked date, client-classification row, participant pseudonym or approved name, organization size band, role split, repo class, language family, workflow label, agent stack categories, trigger label, blocked workflow summary, privacy constraints, activation command IDs, artifact paths or hashes, `packet_id`, `index_id`, freshness state, audit event IDs, verification command labels and exit status, integration path, retained-use dates or task IDs, rubric scores, adoption signal type, disqualifiers, redaction reviewer, retention expiration, P7.11 export ID, and P9.7 decision linkage. |
| Fields forbidden by default | Raw prompts, raw chat transcripts, raw file bodies, secrets, credentials, API keys, tokens, private repo URLs, customer names without approval, exact usernames without approval, regulated production data, PHI, financial account data, unredacted logs, full issue/PR bodies, and screenshots containing source or secrets. |
| Redaction requirements | Pseudonymize partner, participant, repo, and task identifiers unless explicit approval exists; hash or bucket sensitive paths; replace concrete private paths with path roles or file-type categories; summarize workflows in one or two sentences; record the redaction reviewer and date; fail the record if forbidden fields are present. |
| Retention window | Keep complete local validation records only through the P9.7/P9.12 release decision plus 180 days unless a shorter partner agreement applies. Delete withdrawn or disqualified records within 30 days after aggregation. Keep only aggregate counts, scores, and non-identifying lessons after the window. |
| Local-only expectation | Evidence lives in local files, local logs, local product-evidence exports, or partner-approved artifacts. No hosted telemetry, raw source upload, prompt capture, or external analytics dependency is required for product decisions. |
| Feed into P7.11 and P9.7 | P7.11 exports aggregate activation, integration, retained-use, rubric, blocker, and adoption-signal summaries with artifact hashes and record IDs only. P9.7 consumes those summaries plus reviewer-approved examples; it must not require raw prompts, file bodies, secrets, or partner-identifying details. |

Review and traceability ties:

- Client Classification Example Tests: every P0.8 record must name exactly one
  primary row and any supporting rows. Only `enterprise-saas` can satisfy the
  primary paid-demand count; `open-source` supports activation and retained use;
  `tooling-vendor` and `local-model` are secondary pivot inputs; regulated rows
  are proof-only until P0.9/P7.9/P9.9 pass.
- Product Success Gates: P0.8 supplies scored evidence for ICP, activation,
  retained use, integration proof, security/privacy posture, packaging/support
  hypotheses, and local product-evidence export. It does not satisfy competitive
  proof by itself.
- Requirement Traceability: P0.8 operationalizes PROD-02 and PROD-04 and defines
  the P7.11/P9.7 privacy-preserving evidence shape for PROD-10.
- Risk Controls: records that collect forbidden fields, depend on hosted
  telemetry, or treat open-source activation as enterprise-SaaS paid demand must
  be rejected or converted into pivot/no-go evidence.
- Review Protocol: the P0.8 review subagent must verify the runbook template,
  rubric, thresholds, forbidden-field list, retention rule, and ledger/work
  package references. The reviewer must not update live ledger assignment or
  status fields.

### P0.10 Activation And Onboarding Metrics

Purpose: define how Idiom measures whether a new local user reaches the
read-only sidecar value promised by P0.7 and collected by P0.8. This is not a
paid-demand proxy by itself. It measures whether a clean local path can produce
repo-memory, freshness, audit, verification, and cleanup evidence without
becoming another chat UI.

Source basis checked on 2026-05-13 UTC:

- Amplitude time-to-value guidance:
  https://amplitude.com/blog/time-to-value-drives-user-retention. Method input
  only: distinguish completion of setup from a user experiencing a meaningful
  outcome, and measure first value by cohort instead of treating activation as
  one generic event.
- Amplitude Product Analytics onboarding docs:
  https://amplitude.com/docs/analytics/product-analytics. Method input only:
  model onboarding as a funnel and name the value moment as the event that shows
  the user realized feature value.
- Google HEART metrics paper:
  https://research.google.com/pubs/archive/36299.pdf. Method input only:
  activation should be paired with task success, adoption, retention, and
  segment-aware interpretation rather than raw volume or vanity counts.
- Command Line Interface Guidelines:
  https://clig.dev/. CLI-method input only: help must be accessible in the
  tool, uninstall should be easy to find near install instructions, and CLI
  analytics should not phone home without clear consent.
- OpenTelemetry sensitive-data guidance:
  https://opentelemetry.io/docs/security/handling-sensitive-data/. Privacy
  input only: telemetry/product evidence should minimize collection, avoid
  sensitive content by default, and use redaction or hashing only with explicit
  limits.

Idiom-specific hypotheses:

- The default CLI-only activation target is under 10 minutes from clean setup
  start to a privacy-safe activation record with first useful packet, freshness,
  audit, suggested verification, and cleanup result.
- Segment-specific targets below are default targets for P7.7/P9.6 to validate,
  not market claims. If smoke evidence rejects them, P9.6 must lower release
  claims or record a no-go/pivot blocker.

Activation funnel for the P0.7/P0.8 wedge:

| Step | Event | Required evidence | Failure or blocker record |
| --- | --- | --- | --- |
| 0 | Clean repo or clean fixture selected | `activation_run_id`, `client_classification`, environment class, repo/fixture ID, repo class, checked commit or fixture hash, consent/privacy boundary, and whether this is clean install, source build, or clean fixture. | Missing repo/fixture, unsupported platform, or privacy boundary rejected. |
| 1 | Install or setup path starts | Command ID, install/setup path, binary/source version, OS/arch, shell, documented prerequisite state, and start timestamp. | Install failure, missing prerequisite, undocumented prerequisite, or operator hand-holding required. |
| 2 | Readiness or freshness preflight | `idiom doctor` or `context_status` command ID, exit status, environment readiness, graph/index freshness state, stale paths or blocker reason. | Readiness blocked, stale graph cannot refresh, or preflight output is prose-only. |
| 3 | First useful `context_pack` | `context_pack` command ID, `packet_id`, `index_id`, task string, profile/caller path, ranked candidate count, relevant file/symbol/test evidence or explicit no-result/blocker, elapsed time, and freshness state. | Packet missing IDs, no freshness, no relevant evidence, broad raw-file output, or the user cannot identify any useful repo insight. |
| 4 | Freshness check | `context_status` or packet freshness report ID, fresh/stale/blocked state, stale path count, refresh action if any, and stale-use reason if allowed. | Stale evidence silently accepted, refresh path unclear, or freshness only appears as unstructured prose. |
| 5 | Audit report | `harness_audit` command ID, audit report ID, audit event IDs, high/medium finding counts, owner/status requirement if applicable, and any reproducer command. | Audit cannot run, IDs are absent, high findings are unowned, or the user must inspect raw logs to know status. |
| 6 | Suggested verification | Suggested command labels, reason the command is relevant, whether it was run, command ID if run, exit status, or explicit not-run reason. | No suggested verification, unsafe/unsupported command, missing exit status after run, or verification claim without evidence. |
| 7 | Product-evidence-safe output | Local summary/export path or artifact hash, redaction status, forbidden-field check, field list, P0.8/P7.11/P9.6 linkage, and no raw prompt/file-body/secret inclusion. | Raw prompt, raw file body, secret, private repo URL, unredacted log, or hosted telemetry dependency appears. |
| 8 | Docs gap or blocker recorded | Missing instruction path, expected command, actual output, severity, failure class, owner/follow-up if known, and whether retry is allowed. | Failure has no classified reason or requires undocumented tribal knowledge. |
| 9 | Cleanup or uninstall/state cleanup | Cleanup command ID, removed or retained paths, cache/state policy, exit status, remaining local state, and whether fixture/repo returned to the expected state. | Cleanup missing, state left behind without explanation, uninstall docs absent, or cleanup needs manual source edits. |

First useful value:

An activation attempt reaches first useful value when the participant or smoke
runner has a local, privacy-safe record showing a useful `context_pack` plus
freshness and audit evidence, with suggested verification and cleanup status
recorded. A useful `context_pack` must include `packet_id`, `index_id`, ranked
evidence, and an explicit freshness state. It must either identify candidate
files/symbols/tests that are plausibly useful for the requested task, or return
a precise no-result/blocker that saves the user from blind search. It is not
useful if it only proves that a command ran.

Segment-specific value moments:

| Context | First useful value | Default target | Fail threshold | No-go threshold |
| --- | --- | --- | --- | --- |
| `enterprise-saas` | A platform, DevEx, DevSecOps, or AI-enablement participant can use the packet/freshness/audit/suggested-verification evidence to inform a merge, agent-pilot, review, or policy decision on a real or representative repo. Counts toward paid demand only through a complete P0.8 primary-ICP record. | Under 15 minutes, allowing documented enterprise repo setup friction. | Over 30 minutes, hidden prerequisite, or completion only with material Idiom-team hand-holding. | Cannot run locally, requires raw prompt/file-body/secret capture, depends on hosted telemetry, or evidence cannot inform a workflow decision. |
| `open-source` | A maintainer or senior power user can run the quickstart on a real repo or fixture, see useful ranked context, freshness, audit, suggested verification, and cleanup evidence, and name a docs gap if blocked. This supports activation, docs, demo, and retained-use proof only; it does not count as paid enterprise-SaaS demand. | Under 10 minutes. | Over 20 minutes, more than one retry, or unclear docs gap. | Requires hand-holding, cannot clean up state, cannot produce audit evidence, or uploads raw content by default. |
| `local-model` | A local/privacy-first operator gets readiness or degraded-mode evidence plus useful context/freshness/audit output, with backend or environment blockers classified before any unsafe agent run. | Under 20 minutes when model artifacts are already installed or the run uses a clean fixture that does not require model download. | Over 45 minutes, readiness blockers are ambiguous, or queue/backend/context state is not machine-readable. | Starts unsafe execution despite failed readiness, spins unbounded retries, requires network/model download without a blocker, or emits raw prompt/file content. |
| `tooling-vendor` | An integrator can consume parseable sidecar output with schema/profile/caller metadata, `packet_id`, freshness, audit IDs, structured errors, no strategy leakage, and privacy-safe export linkage. | Under 15 minutes. | Over 30 minutes, schema mismatch, or manual copy/paste required. | Output is not machine-readable, transport grants broader authority than discovery, strategy is prescribed instead of evidenced, or privacy checks fail. |

Measurement fields:

Each activation attempt must collect these fields when applicable. Missing
fields are blockers unless the record marks them `not_applicable` with a
reason.

- Run identity: `activation_run_id`, P0.8 `record_id` if participant-backed,
  P7.7 `smoke_id` if automated, P9.6 `activation_report_id`, operator
  pseudonym or smoke runner, checked date, and command transcript path or hash.
- Environment: `environment_class` (`clean-install`, `clean-fixture`,
  `source-build`, `existing-repo-clean-state`), OS/arch, shell, Idiom version,
  git commit, install/setup path, repo class, language family, fixture ID, and
  local-only/hosted-telemetry policy.
- Commands: command IDs, command labels, start/end timestamps, elapsed time,
  exit status, retry number, operator-assistance class, and stderr/stdout
  artifact hashes when retained.
- Repository memory: `context_pack` task string, `packet_id`, `index_id`,
  profile, caller path, candidate count, relevant file/symbol/test evidence,
  unresolved gaps, and first-value event classification.
- Freshness: freshness state, graph/index ID, stale paths count, refresh action,
  stale-use reason, blocker reason, and whether freshness came from
  `context_status`, packet output, or both.
- Audit: `harness_audit` purpose, audit report ID, audit event IDs, high/medium
  finding counts, finding owner/status requirement, reproducer command, and
  report/export path or hash.
- Verification: suggested verification labels, command text or allowlisted
  command ID, whether it was run, exit status, failure reason, and not-run
  reason if applicable.
- Outcome: activation status (`pass`, `fail`, `no-go`, `blocked`,
  `insufficient-evidence`), elapsed time to first useful packet,
  elapsed time to trust evidence, blocker class, docs gap, cleanup result, and
  next follow-up owner if one exists.
- Privacy and export: redaction reviewer, privacy/redaction status, forbidden
  field check result, retention window, product-evidence-safe export ID or
  artifact hash, P7.11 export ID, P9.6 report linkage, and explicit confirmation
  that raw prompts, raw file bodies, secrets, credentials, private repo URLs,
  unredacted logs, and hosted telemetry are absent by default.

Metrics:

| Metric | Definition | Default target or threshold |
| --- | --- | --- |
| `time_to_first_packet_seconds` | Elapsed time from first documented setup command to first `context_pack` that meets the first-usefulness definition. | Segment target above; fail if the packet lacks `packet_id`, `index_id`, ranked evidence, or freshness state. |
| `time_to_trust_evidence_seconds` | Elapsed time from first documented setup command to completion of first useful packet, freshness check, audit report, suggested verification, product-evidence-safe output, and cleanup result. | Under 10 minutes for generic CLI-only clean fixture; segment targets above override only when explicitly selected. |
| `activation_completion_rate` | Completed activation attempts divided by started clean attempts for the same segment/environment class. | P7.7 establishes baseline; P9.6 must report pass/fail/no-go counts and cannot hide failures as setup noise. |
| `docs_gap_rate` | Attempts with at least one docs gap divided by started attempts. | No default pass claim before P7.7; every gap needs path/command/expected/actual fields. |
| `privacy_safe_export_rate` | Attempts with a product-evidence-safe summary/export that passes forbidden-field checks divided by completed attempts. | Must be 100% for release claims; any forbidden-field failure is no-go until corrected. |
| `cleanup_success_rate` | Attempts where uninstall/state cleanup exits successfully and remaining state is either absent or explicitly retained by policy divided by completed attempts. | Must be 100% for release claims, or P9.6 must lower claims to experimental/internal. |
| `assistance_class` | None, docs-only, operator typo correction, environment prerequisite, Idiom-team hand-holding, or hidden fix. | Pass requires none, docs-only, or operator typo correction. Hidden fix is no-go for release activation. |

Pass, fail, and no-go classification:

- Pass: all funnel steps complete inside the selected target; evidence contains
  required IDs and statuses; product evidence passes privacy checks; cleanup
  succeeds or records an intentional retained-state policy; assistance is no
  more than docs-only or operator typo correction.
- Fail: the funnel completes after the target, needs a documented retry, exposes
  a docs gap, or produces a non-fatal blocker. The record remains useful for
  P7.7/P9.6 but cannot satisfy the activation gate by itself.
- No-go: the path requires a chat UI, hosted telemetry, raw prompt/file-body or
  secret capture, undocumented internal commands, source edits, hidden operator
  fixes, unsafe stale evidence, unbounded retries, missing audit/freshness IDs,
  or failed cleanup. P9.6 must block release claims until resolved or explicitly
  reduce scope.

Measurement protocol:

- Clean repo/fixture definition: use a fresh clone or fixture at a recorded
  commit/hash, no pre-existing `.idiom/comprehension` graph for the target, no
  pre-generated packet/audit artifacts, and a clean or isolated `IDIOM_HOME`.
  Existing OS package managers and documented prerequisites may be present, but
  they must be recorded.
- Clean install definition: start from no Idiom binary on `PATH`, or from a
  documented source-build path when no release binary exists. If the release
  binary does not exist yet, the record must be labeled `clean-fixture` or
  `source-build` and cannot support binary-install claims.
- Allowed assistance: the operator may follow public docs, choose the target
  repo/fixture, answer shell prompts, and correct a typo once. They may not run
  hidden commands, pre-seed graph state, edit source, hand-pick expected files,
  suppress failures, or receive undocumented Idiom-team instructions without
  changing the assistance class.
- Retries: allow at most one retry for operator typo or transient environment
  setup. Retry records keep the same `activation_run_id`, increment
  `retry_number`, and preserve the failed command evidence. A second retry is a
  fail; any hidden fix is no-go.
- Failure classification: use one primary class and optional supporting classes:
  `install`, `prerequisite`, `readiness`, `freshness`, `context_pack`, `audit`,
  `suggested_verification`, `privacy_export`, `docs_gap`, `cleanup`,
  `operator_error`, `unsupported_environment`, or `external_dependency`.
- Docs-gap recording: name the doc path/section or missing doc, the command the
  user tried, expected output, actual output, severity, whether the gap blocked
  activation, and the proposed follow-up task if known.
- Privacy constraints: product evidence stays local by default, minimizes data,
  and records summaries, IDs, hashes, status fields, counts, and classifications
  instead of raw prompts, raw file bodies, full private paths, private repo URLs,
  secrets, credentials, tokens, unredacted logs, or screenshots containing code.
- Repeatability: P7.7 implements the smoke recipe and records every command ID.
  P9.6 reruns or reviews the same recipe for release and reports pass/fail/no-go
  by client classification and environment class. Claims must cite generated
  activation records, not one-off prose.

Traceability ties:

- Product Success Gates: the Activation gate uses `time_to_first_packet_seconds`,
  `time_to_trust_evidence_seconds`, privacy-safe export status, docs-gap rate,
  and cleanup success; Retained use remains owned by P0.8/P7.11/P9.7.
- Requirement Traceability: P0.10 operationalizes PROD-03 and supplies the
  activation subset of PROD-10. It also strengthens UX-01, UX-02, UX-03,
  UX-05, UX-07, UX-08, CTRL-04, CTRL-11, CTRL-15, and CTRL-19 by making
  first-run evidence, freshness, audit, export, cleanup, and reviewability
  measurable.
- Client Classification Example Tests: `open-source-quickstart-transcript`,
  `enterprise-ci-verification-smoke`, `local-model-readiness-degraded`, and
  `tooling-vendor-cli-mcp-parity` must consume the same field names and
  pass/fail/no-go meanings. Regulated rows may reuse the metric only as proof of
  local/privacy posture until P0.9/P7.9/P9.9 controls pass.
- P0.8 design-partner records: the activation attempt section embeds the P0.10
  fields and value moment, but `open-source`, `tooling-vendor`, and
  `local-model` activation remains supporting or pivot evidence unless the P0.8
  record independently satisfies the `enterprise-saas` primary-ICP criteria.
- P7.7: implements clean-install/clean-fixture smoke coverage and records the
  activation fields without updating live ledger status.
- P7.11: exports only aggregate or redacted activation summaries, artifact
  hashes, IDs, blockers, and linkage fields; it must not export raw prompts,
  raw file bodies, secrets, or unredacted logs by default.
- P9.6: gates release claims on the generated activation report meeting the
  target or recording blockers/no-go decisions with claim-scope impact.
- Risk Controls: any activation evidence that depends on chat UI polish, hosted
  telemetry, raw repository content, unbounded retries, or treating open-source
  activation as paid enterprise-SaaS demand must be rejected or converted into
  explicit pivot/no-go evidence.
- Validation Matrix: the Activation/onboarding row is the focused validation
  rule for this section; product-success docs also require trace to release
  gates and researched source dates.
- Review Protocol: the P0.10 review subagent must verify this funnel, field
  list, thresholds, privacy constraints, P0.7/P0.8/P7.7/P7.11/P9.6 ties, and
  the P0.10 work-package row. The reviewer must not update live ledger
  assignment or status fields.

### P0.11 Packaging, Pricing, Support, Admin, And Enterprise-Surface Hypotheses

Purpose: define how Idiom should be packaged and sold only if the P0.7
beachhead and P0.8/P9.7 demand evidence hold. This section is a hypothesis
boundary, not a release promise, status tracker, or pricing sheet. It keeps the
MVP conservative: a local CLI evidence sidecar for teams already using coding
agents, with privacy-safe export and limited support. It also names the
final-state enterprise surface so the project can defer it deliberately instead
of accidentally building a generic SaaS product.

Dependency and scope:

- P0.11 depends on P0.7. The default buyer remains `enterprise-saas`; the
  adoption wedge remains `open-source`; `tooling-vendor` and `local-model` are
  secondary; `finance`, `healthcare`, and `public-sector` are proof-only or
  deferred until P0.9/P7.9/P8.4/P9.9 control evidence passes.
- P0.11 consumes P0.8 design-partner evidence, P0.10 activation evidence, P0.9
  security/privacy posture, P7/P8 integration evidence, P7.11 local
  product-evidence export, and P9 release-gate reports. It does not create a
  second validation ledger.
- Packaging language must preserve the model/harness boundary: Idiom packages
  contracts, evidence, freshness, audit, evals, and integration surfaces. It
  does not package hardcoded agent strategy.

MVP packaging hypothesis:

- Product shape: local CLI sidecar plus first-party harness commands, not a
  hosted application. The MVP package is `idiom` running locally from source or
  a signed/checksummed release binary when available.
- Core workflow: `idiom doctor`, `idiom tool context_pack`,
  `idiom tool context_status`, `idiom tool harness_audit`, sidecar schema or
  profile inspection, suggested verification evidence, and a local
  product-evidence-safe export.
- Permission scope: read-only `discovery` sidecar by default. Write, shell,
  fleet policy, SSO, hosted dashboards, and managed telemetry are out of MVP
  unless a release gate explicitly narrows or changes scope.
- Package contents: CLI binary or source-build path, fixture/demo pack,
  quickstart, install and cleanup/uninstall docs, sidecar command transcripts,
  privacy-safe evidence-export examples, schema/profile docs, and runnable
  client-demo transcripts.
- Evidence export: local artifact or report containing IDs, hashes, counts,
  classifications, blocker reasons, activation summaries, retained-use signals,
  integration proof, benchmark summaries, audit summaries, and learning signals
  without raw prompts, raw file bodies, secrets, credentials, private repo URLs,
  or hosted telemetry by default.
- Installation posture: macOS/Linux first, source build allowed before a release
  binary exists, documented cleanup path, no hidden setup scripts, and clear
  blocker output when local dependencies or model backends are unavailable.
- Demo posture: a clean fixture and at least one representative real-repo
  transcript should prove the package without requiring customer source upload
  or a live SaaS account.
- Support posture: limited async design-partner or evaluator support covering
  installation blockers, reproducible activation failures, docs gaps,
  evidence-export privacy issues, and sidecar integration questions. No 24/7
  SLA, dedicated support engineer, custom compliance attestation, managed
  deployment, or bespoke feature development in MVP.
- Admin posture: repo-local configuration, environment variables, profile
  files, redaction/retention settings, and machine-readable command output are
  acceptable. Org-wide policy consoles, user directories, RBAC, SSO, billing
  admin, fleet rollout, centralized evidence management, and hosted audit
  dashboards are final-state hypotheses, not MVP commitments.
- Pricing posture: pricing remains unquoted until P0.8/P9.7 records show paid
  or internal-platform adoption value. The MVP may use free evaluation,
  open-source/community use, or a design-partner pilot, but any paid hypothesis
  must be tied to platform-team value from local evidence, not token resale,
  model access, chat UI seats, or generic developer-assistant usage.

MVP pricing hypotheses to test:

| Hypothesis | What It Tests | Evidence Required Before Claim |
| --- | --- | --- |
| Free local/community package for activation and open-source proof | Whether low-friction local setup creates adoption, docs, retained-use, and demo evidence. | P0.10/P7.7 activation records, P7.10 demos, P7.11 export, and no privacy failure. |
| Paid design-partner or internal-platform pilot for `enterprise-saas` | Whether a platform owner values sidecar evidence enough to fund a limited team/repo pilot. | P0.8 primary-ICP record with buyer or budget-owner signal, retained-use evidence, integration proof, and P9.7 adoption signal. |
| Team/platform package priced by repo/team/caller scale, not model tokens | Whether value tracks controlled repository evidence across agents rather than LLM usage volume. | P9.12 pricing interview notes, P7.8/P9.8 caller/integration evidence, and local export fields showing repo/team/caller value. |
| Tooling-vendor partner package | Whether a vendor wants CLI/MCP/schema parity for embedding or customer workflows. | Vendor-specific P0.8 record or P9.8 integration proof; cannot displace the primary enterprise-SaaS evidence unless P9.12 records a pivot. |

Final-state packaging hypotheses:

- Paid enterprise/platform surface: self-hosted or local-first enterprise
  package for platform teams that need cross-agent repo memory, freshness,
  contracts, audit, evals, release evidence, and local product-evidence export.
  Pricing may become annual platform subscription, team/repo/caller tier, or
  enterprise agreement; it should not become a token marketplace or model resale
  business.
- Team policy and admin: policy-as-code, org config distribution, profile
  management, protected-path policy, approval policy, retention/redaction
  policy, SSO/RBAC, user/caller attribution, fleet rollout, version pinning, and
  usage limits only after the local substrate proves value.
- Audit and evidence management: local or self-hosted evidence store, retention
  controls, audit ownership/status, export to SIEM/GRC or internal data
  warehouses, report signing/checksums, bundle regeneration, release evidence
  comparisons, and reviewer workflows.
- Support tiers: community/docs for open-source use, team support for platform
  pilots, enterprise support with response targets and upgrade guidance, and
  regulated/customer-success support only after security/privacy evidence and
  support load are measured.
- Procurement and compliance affordances: security package, threat model,
  data-flow map, SBOM/checksums, release provenance, vulnerability disclosure,
  DPA/vendor questionnaires, retention/redaction policy, control mapping, and
  customer-owned compliance review. Formal certification is not implied by the
  MVP plan.
- Integration packaging: CLI, CI/script recipes, external-agent recipes, MCP
  stdio, future SDKs only if they reuse the same schema/profile/freshness/audit
  contracts, and compatibility reports for versioned sidecar envelopes.
- Partner and tooling-vendor packaging: conformance tests, embedded sidecar
  licensing, schema compatibility, partner examples, and support boundaries for
  vendors that want Idiom as repo-memory/evidence infrastructure under their
  own agent or developer-tool surface.

Client-classification packaging stance:

| Classification | MVP Packaging Stance | Final-State Hypothesis | Support/Admin Boundary | Go/No-Go Evidence |
| --- | --- | --- | --- | --- |
| `enterprise-saas` | Primary paid beachhead. Package the local CLI sidecar, fixture/demo pack, CI/script-friendly output, product-evidence export, and limited platform-team support. | Paid platform/team package with policy, evidence management, integration recipes, support tiers, and procurement artifacts. | MVP excludes SSO, fleet policy, billing admin, hosted dashboard, and managed deployment. | GO only with P0.8/P9.7 buyer signal, P0.10 activation, P7.8/P9.8 integration proof, P7.11 export, and P9.12 pricing/support decision. |
| `open-source` | Adoption wedge and proof pool. Package quickstart, fixture, local install/cleanup docs, readable audit, and privacy-safe export examples. | Community package can remain free or low-friction while feeding docs, demo, and retained-use evidence. | No paid-enterprise demand credit unless the record independently satisfies primary ICP criteria. | GO for activation/docs/demo proof; NO-GO as paid proof without enterprise-SaaS buyer evidence. |
| `tooling-vendor` | Secondary integration path. Package schema/profile docs, CLI sidecar transcript, no-strategy-leakage examples, and MCP parity when available. | Partner package with conformance tests, embedded sidecar terms, SDKs, compatibility guarantees, and partner support tiers. | MVP support is integration-question triage only; no custom embedding or vendor roadmap commitment. | GO as pivot or expansion only with vendor sponsor signal and parity evidence; NO-GO if it pulls scope ahead of substrate stability. |
| `local-model` | Secondary enabling segment. Package readiness/degraded-mode docs, local smoke fixture, capacity evidence, and no-hosted-telemetry proof. | Local-operations package with backend readiness, queue controls, capacity reports, and offline-friendly evidence management. | MVP does not sell "local Aider" or guarantee model quality; support covers harness readiness, not model tuning as a service. | GO when P6/P9 readiness and activation evidence are strong; NO-GO if value depends on being a generic local chat agent. |
| `finance` | Deferred regulated proof row. Package local evidence, protected-path posture, audit controls, and finance demo limitations only after P0.9 evidence. | Enterprise regulated package with control mapping, retention/redaction policy, audit export, support tiers, and procurement artifacts. | MVP makes no compliance or FINRA-specific product claim and offers no formal attestation. | GO for proof-only demos with controls; NO-GO for sales claims before P7.9/P9.9 evidence and support posture exist. |
| `healthcare` | Deferred regulated proof row. Package local/privacy posture and healthcare demo limitations only when no e-PHI or raw content is required. | Regulated package with retention, redaction, data-flow review, support process, and customer-owned compliance review. | MVP makes no HIPAA compliance claim and must not require e-PHI, raw prompts, or raw file bodies. | GO for non-sensitive proof; NO-GO if value requires hosted telemetry, e-PHI handling, or formal compliance before controls exist. |
| `public-sector` | Deferred/proof-only row. Package offline-friendly commands, reproducible release evidence, checksums, and policy-limit demos. | Self-hosted/offline-friendly enterprise package with supply-chain artifacts, evidence retention, admin policy, and support tiers. | MVP does not promise FedRAMP, air-gapped support, or procurement readiness. | GO for executable local proof; NO-GO if adoption requires certification, fleet admin, or managed support before substrate proof. |

Product-success coverage:

| Product Need | P0.11 Packaging Rule |
| --- | --- |
| ICP | Keep `enterprise-saas` as the paid beachhead and `open-source` as the adoption wedge unless P9.12 records a named pivot. |
| Activation | Package install/setup, fixture, quickstart, first useful packet, freshness, audit, suggested verification, export, docs-gap, and cleanup paths from P0.10. |
| Retained use | Treat saved packets, audit history, evidence exports, and repeated repo/caller use as the durable value proof, not session count or chat volume. |
| Integration proof | Package shell/CI, external-agent, and MCP paths only through shared schema/profile/freshness/audit contracts. |
| Security/privacy | Keep product evidence local and summarized by default; regulated packaging waits for P0.9/P7.9/P8.4/P9.9 controls. |
| Competitive proof | Do not price or package around broad productivity claims until P4.6/P9.10 benchmark evidence exists. |
| Client demo proof | Every classification demo must state MVP limitation, final-state expansion, required evidence fields, and go/no-go result. |
| Packaging/support | MVP support is limited and reproducibility-focused; enterprise support/admin tiers are final-state hypotheses. |
| Local product-evidence export | Every commercial or pivot decision must cite P7.11/P9.12 export fields instead of raw prompts, file bodies, or hosted telemetry. |

Explicit non-goals:

- No generic SaaS dashboard as an MVP dependency.
- No hosted telemetry requirement, raw prompt capture, raw file-body capture, or
  secret collection for product decisions.
- No IDE replacement or pair-programming UI parity project.
- No model marketplace, token resale, hosted model-routing business, or
  benchmark claim based on model access alone.
- No hardcoded agent strategy, vertical-specific strategy branch, or prompt
  recipe sold as the product. The product surface is evidence and contracts.
- No enterprise admin, SSO, RBAC, fleet management, billing admin, formal
  compliance attestation, procurement package, or dedicated SLA before sidecar
  substrate proof.
- No write/shell sidecar packaging until read-only discovery has schema,
  freshness, permission, audit, and evidence-export parity.
- No regulated-industry sales claim before P0.9/P7.9/P8.4/P9.9 evidence and a
  support posture make the claim reviewable.
- No pricing claim that treats open-source activation, demo interest, or
  self-reported productivity as enterprise paid demand.

Go, pivot, and no-go criteria:

- Go for MVP packaging when P0.10 activation passes for a local CLI/fixture or
  source-build path; P0.8/P9.7 produce at least one credible paid or
  internal-platform signal in the primary ICP; P7.11 product-evidence export
  passes privacy checks; P7.8/P9.8 prove at least shell/CI and one external
  caller path or record an explicit scoped release decision; P7.10/P9.11 demos
  cover all seven classifications with MVP limitations; and P9.12 records the
  pricing/support/admin decision.
- Pivot to sidecar-only, open-source-led, tooling-vendor-led, or local-model-led
  packaging only when evidence shows users value repo memory, freshness, audit,
  and integration but the primary enterprise-SaaS buyer does not own budget or
  urgency. The pivot must name which P0.7 assumption failed.
- No-go for commercial claims if activation requires hand-holding, a hosted
  dashboard, SSO, fleet policy, formal compliance, raw source/prompt telemetry,
  chat UI parity, or custom agent strategy before the local sidecar proof works.
- No-go for final-state enterprise surface if support load cannot be bounded,
  product-evidence export fails privacy checks, regulated rows need unsupported
  attestations, or packaging would require a separate admin product before the
  substrate has buyer-valued evidence.

P9.12 evidence fields:

- Package hypothesis ID, client classification, buyer, daily user, support
  level, admin need, pricing hypothesis, activation record IDs, retained-use
  record IDs, integration proof IDs, security/privacy posture link, competitive
  proof link, demo transcript IDs, product-evidence export ID, accepted
  non-goals, rejected requests, pivot/no-go trigger if any, and final
  continue/pivot/no-go decision.

Traceability ties:

- Product Success Gates: P0.11 operationalizes the Packaging and support thesis
  gate and constrains ICP, activation, retained use, integration proof,
  security/privacy, competitive proof, client demo proof, and local
  product-evidence export claims.
- Requirement Traceability: P0.11 operationalizes PROD-09 and feeds PROD-01,
  PROD-03, PROD-05, PROD-06, PROD-08, and PROD-10 release decisions without
  creating new live status tracking.
- Client Classification Example Tests: P7.10/P9.11 demos must reuse the
  classification stance above and state whether each row is MVP, deferred,
  secondary, proof-only, pivot, or no-go.
- Risk Controls: any packaging that depends on generic SaaS, hosted telemetry,
  raw content, IDE replacement, model marketplace, hardcoded strategy, or
  enterprise admin before substrate proof must be rejected or converted into an
  explicit pivot/no-go decision.
- Validation Matrix: product-success docs and the packaging/support row require
  `git diff --check`, ledger/work-package consistency, focused coherence scans,
  traceability to release gates, and researched-source date checks only if new
  external sources are introduced.
- Review Protocol: the P0.11 review subagent must verify the MVP/final-state
  split, pricing/support/admin hypotheses, non-goals, all seven client
  classifications, product-success coverage, P9.12 evidence fields, and
  work-package row. The reviewer must not update live ledger assignment or
  status fields.

## End-State Users And Stories

Idiom's end state should be evaluated through these users, not through generic
"AI assistant" expectations.

Personas:

- Privacy-sensitive senior developer: uses Codex, Claude Code, Aider, OpenCode,
  or local Qwen, but wants repo-aware help without surrendering control over
  context, writes, or logs.
- Engineering platform lead: wants a common local evidence and policy substrate
  for multiple agents across team repositories.
- Startup CTO or productivity owner: wants proof that agents reduce waste and
  risk, not only that they produce plausible patches.
- AI tooling engineer: builds wrappers, CI flows, custom agents, or MCP clients
  and needs stable programmatic access to repo memory and verification evidence.
- Regulated or security-sensitive team: needs local/offline operation, protected
  paths, redaction, permission gates, and replayable evidence.
- Local-model operator: runs Qwen, MLX, llama.cpp, Ollama, or OpenAI-compatible
  local endpoints and needs role routing, queueing, readiness, and budget
  control.

End-state experience:

- An external-agent developer asks Codex or Claude Code to implement a fix. The
  agent first calls Idiom for a fresh context packet, edits only scoped files,
  runs source sanity or configured verification, and leaves behind contract,
  freshness, and audit evidence. Idiom supplies the substrate; the caller still
  owns strategy.
- A platform lead installs Idiom as a sidecar across team repos. Agents and
  scripts use the same `discovery` profile. Weekly audit output shows freshness,
  caller mix, high-volume tools, zero-hit searches, permission denials,
  verification gaps, and ranker-learning changes.
- A local-model operator runs a plan-execute lane. Idiom resolves planner and
  executor roles, checks queue admission, warns about classifier degradation,
  budgets context, records stop reason, and reports verification status without
  requiring hand-reading raw logs.

## Client Story Matrix

These client stories are sales-facing, but every promise must remain feasible
inside the implementation plan. They should be used to test whether a roadmap
item is advancing the missing substrate or drifting toward generic AI-chat
parity. The evidence column is intentionally not a live "current state"
snapshot. It names either a dated external example or the generated evidence
required before release.

| Client Type | Dated Example Or Generated Evidence Requirement | Story And Selling Point | Required Idiom Capabilities | Plan Coverage | Feasibility |
| --- | --- | --- | --- | --- | --- |
| Financial services and fintech | Dated example: FINRA's July 2025 GenAI note lists coding among observed member-firm use cases and says existing obligations continue to apply. Release-time proof must cite the checked source version in the P0.9 control mapping and P9.1 evidence bundle. | A bank wants developers to use Codex or Claude Code, but compliance needs proof of what repo context was used, which paths were protected, what tools were allowed, and whether verification happened. Idiom sells the audit and permission substrate beneath whatever agent the team already uses. | Local sidecar, protected paths, contract replay, caller attribution, redacted telemetry, verification evidence, audit ownership. | P1.1-P1.6, P2.1-P2.4, P7.1-P7.5, P9.1-P9.3 | MVP-feasible for read-only discovery and audit; write/verify profiles are final-state or post-MVP. |
| Healthcare and life sciences | Generated evidence requirement: P0.9 must map healthcare examples to the checked HHS HIPAA Security Rule/risk-analysis guidance version and record which PHI-bearing fixtures or exports are protected in the demo. P0.3 verified the page by browser/search access on 2026-05-13, but `curl` returned 403, so release evidence must record its verification method. | A healthcare engineering team wants AI help on internal systems but cannot allow accidental protected-data exposure or untraceable code changes. Idiom sells local operation, redaction, freshness, protected path controls, and evidence trails. | Discovery profile, redaction policy, protected path denial, local-model readiness, no raw prompt/file body telemetry, audit reports. | P2.2-P2.4, P6.1-P6.5, P7.2, P7.5, P8.4, P9.3 | MVP-feasible for read-only/private-code discovery; regulated write automation requires later verify/write profile hardening. |
| Defense, public sector, and critical infrastructure | Dated examples: NIST AI RMF and NIST SP 800-218A are the cited control anchors in this draft. A candidate DoD source label is "DoD Enterprise DevSecOps Fundamentals v2.5, DoD CIO, approved October 2024"; P0.3 could not verify the candidate PDF with `curl` because the host returned 403, so public-sector release proof must attach a P0.9/P9.1 command or manual verification report before citing it. | A software factory wants agentic coding in sensitive repos, but every tool in the chain must be understood, secured, monitored, and reproducible. Idiom sells local evidence, contract controls, supply-chain-friendly logs, and release gates. | Offline-friendly sidecar, contract events, software-supply-chain auditability, local backend readiness, reproducible benchmark and release evidence. | P0.1-P0.4, P1.1-P1.6, P6.1-P6.5, P9.1-P9.3 | Feasible as an internal tool substrate; production adoption needs final-state policy, retention, and admin controls. |
| Enterprise SaaS and DevSecOps teams | Dated examples: Stack Overflow 2025, DORA 2025, and GitHub Copilot Enterprise docs are the draft adoption/trust/governance anchors. Release-time proof must regenerate activation, benchmark, audit, and design-partner evidence rather than relying on survey prose. | A SaaS platform team's P0.8 validation record shows developers using several agents already. The problem is not access to another agent; it is knowing which agent runs are safe enough to merge. Idiom sells localization benchmarks, verification obligations, audit findings, and sidecar consistency across tools. | Context localization, source sanity, verification tracking, behavior audit, ranker learning, sidecar compatibility, release evidence gates. | P3.1-P3.5, P4.1-P4.5, P5.1-P5.4, P7.1-P7.4, P9.1-P9.3 | Primary MVP paid beachhead. |
| AI tooling, agent-platform, and IDE vendors | Dated example: the official MCP 2025-11-25 specification is the draft protocol anchor and was the latest selector option when checked on 2026-05-13. Generated proof must come from P7/P8 CLI-vs-MCP schema, profile, error, resource, and caller-metadata parity reports. | An agent-platform team wants better repository intelligence without building a language graph, freshness policy, audit layer, and eval loop from scratch. Idiom sells the repo-memory sidecar: one local CLI/MCP substrate that agents can call. | Stable CLI envelope, schema export, MCP stdio, resources, caller metadata, compatibility tests, no strategy leakage. | P2.1, P3.4, P7.1-P7.4, P8.1-P8.4 | Feasible after sidecar MVP; strongest final-state integration story. |
| Local-model and privacy-first teams | Generated evidence requirement: P6/P9 must publish local smoke, capacity, replay, readiness, queue, and degraded-mode outputs for the supported local backends before claiming operational reliability. | A team wants to use Qwen or other local models for bounded coding work, but local inference fails in ways cloud tools hide. Idiom sells predictable role routing, queue admission, context budgets, readiness, and replayable failures. | Backend profiles, model roles, queue controls, context budget, doctor remedies, telemetry, local smoke/capacity/replay benchmarks. | P6.1-P6.5, P1.1-P1.3, P9.1-P9.2 | Feasible inside the plan; most valuable when paired with sidecar/audit rather than sold as "local Aider." |
| Open-source maintainers and senior power users | Generated evidence requirement: P0.8/P7.7/P7.10 must record active agent/tool stacks, setup time, first useful packet, freshness check, audit report, and runnable demo transcript before calling this an adoption wedge. | A maintainer wants agent help without letting every tool rediscover the repo, miss tests, or leave unexplained diffs. Idiom sells a local trust layer: fresh packets, suggested tests, audit, and evidence that can be used with any agent. | `context_pack`, `context_query`, `context_status`, audit, sidecar smoke recipes, low-friction install and docs. | P0.1-P0.4, P2.2, P4.1-P4.4, P7.1-P7.3 | Adoption wedge and proof pool; not the primary paid MVP buyer unless enterprise SaaS demand fails and retained open-source/tooling evidence justifies a named pivot. |

MVP classification priority:

| Matrix Row | MVP Priority | Why | P0.7 Decision Rule |
| --- | --- | --- | --- |
| `enterprise-saas` | Primary paid beachhead | It has the clearest buyer/user split, budget owner, merge-facing pain, and direct need for sidecar evidence across several agents. | P0.8 design partners should primarily match this row; P9.7 and P9.12 cannot pass without evidence from this row. |
| `open-source` | Adoption wedge and proof pool | Maintainers and senior power users can validate low-friction setup, first useful packet, freshness, and audit without enterprise procurement. | Use for activation, quickstart, and demo proof, but do not treat willingness to try as proof of paid MVP demand. |
| `tooling-vendor` | Secondary integration path | Vendors and consultants validate schema, CLI/MCP parity, and no-strategy-leakage, but buying cycles can pull Idiom toward platform embedding before the substrate is stable. | Keep P7/P8 integration proof strong; defer vendor packaging until P0.11/P9.12 unless P0.8 shows stronger paid demand than enterprise SaaS. |
| `local-model` | Secondary enabling segment | Local readiness strengthens the product's substrate claim and helps privacy-first users, but "local Aider" is not the sales thesis. | Treat P6 as necessary enabling proof, not the primary ICP, unless enterprise SaaS buyers reject the sidecar and local operators show retained paid demand. |
| `finance` | Deferred regulated expansion | Finance has high audit value but requires stronger P0.9 control mapping, protected-path behavior, review evidence, and support posture before MVP sales claims. | Include executable proof and security controls, but do not make regulated finance the MVP buyer without P9.9 evidence. |
| `healthcare` | Deferred regulated expansion | PHI, retention, redaction, and data-flow proof must be validated before healthcare becomes a sales target. | Use healthcare examples to harden generic privacy primitives; defer buyer claims until P0.9/P7.9/P9.9. |
| `public-sector` | Deferred/proof-only for MVP | Offline, reproducibility, and supply-chain evidence are valuable but demand heavier procurement and control proof than the first sidecar wedge should carry. | Keep public-sector examples as proof that generic primitives can satisfy stricter overlays; do not let them drive MVP packaging. |

External anchors:

- Stack Overflow 2025 Developer Survey AI page, checked 2026-05-13 UTC: dated
  survey anchor reporting broad AI tool and agent use alongside trust,
  complex-task performance, security/privacy, integration difficulty, and
  agent-observability gaps.
  <https://survey.stackoverflow.co/2025/ai>
- Stack Overflow June 2025 agent-integration article, checked 2026-05-13 UTC:
  dated guidance anchor framing enterprise agent adoption around data-access
  controls,
  performance/scalability design, governance, monitoring, prompt/memory
  protection, and observable/correctable behavior.
  <https://stackoverflow.blog/2025/06/02/integrating-ai-agents-navigating-challenges-ensuring-security-and-driving-adoption/>
- DORA 2025 report page, checked 2026-05-13 UTC: dated report anchor for the
  claim that AI's returns depend on the underlying organizational system, so
  Idiom must prove process and platform improvements rather than agent novelty.
  <https://dora.dev/research/2025/dora-report/>
- GitHub Copilot usage metrics docs, checked 2026-05-13 UTC: primary vendor
  evidence that enterprise stakeholders expect adoption, engagement, code
  generation, pull-request, and exportable usage reporting for AI coding tools.
  <https://docs.github.com/en/enterprise-cloud@latest/copilot/concepts/copilot-usage-metrics/copilot-metrics>
- GitHub Copilot audit log docs, checked 2026-05-13 UTC: primary vendor evidence
  for enterprise audit expectations and a boundary note that local client
  session prompts are not included in the Copilot audit log.
  <https://docs.github.com/en/enterprise-cloud@latest/copilot/how-tos/administer-copilot/manage-for-enterprise/review-audit-logs>
- GitHub Copilot policy and content-exclusion docs, checked 2026-05-13 UTC:
  primary vendor evidence that organizations and enterprises use feature,
  model, privacy, and content-access controls for assistant rollout.
  <https://docs.github.com/en/copilot/concepts/policies>
  <https://docs.github.com/en/enterprise-cloud@latest/copilot/concepts/context/content-exclusion>
- FINRA "How Member Firms Use GenAI": July 2025 PDF note; observed finance use
  cases include coding, and existing obligations continue to apply.
  <https://www.finra.org/sites/default/files/2025-07/How-Member-Firms-Use-GenAI.pdf>
- HHS HIPAA Security Rule risk-analysis page: official guidance anchor. P0.3
  verified browser/search access on 2026-05-13, but `curl` returned 403 from
  this environment, so P0.9/P9.1 must record the verification method before
  release claims.
  <https://www.hhs.gov/hipaa/for-professionals/security/guidance/guidance-risk-analysis/index.html>
- NIST AI RMF and NIST SP 800-218A: GenAI risk management and secure software
  development practices are explicit governance surfaces.
  <https://www.nist.gov/itl/ai-risk-management-framework>
  <https://csrc.nist.gov/pubs/sp/800/218/a/final>
- OWASP GenAI LLM Top 10 2025: project documentation anchor for prompt
  injection, improper output handling, excessive agency, sensitive information
  disclosure, and supply-chain risk controls in agentic development systems.
  <https://genai.owasp.org/llm-top-10/>
- DoD Enterprise DevSecOps Fundamentals: unresolved source label for this plan.
  P0.3 found a candidate DoD CIO "DoD Enterprise DevSecOps Fundamentals v2.5"
  PDF label dated October 2024, but `curl` returned 403 for the candidate URL;
  P0.9/P9.1 must manually verify the exact source before public-sector claims.
- MCP 2025-11-25 official protocol docs: dated protocol anchor, selected as the
  latest version on the MCP site when checked on 2026-05-13. This supports the
  hypothesis that agent tool integration is becoming a shared substrate problem
  rather than one assistant's private UI.
  <https://modelcontextprotocol.io/specification/2025-11-25>
- METR July 2025 open-source productivity RCT: arXiv preprint anchor warning
  that self-reported AI productivity can diverge from measured task outcomes, so
  Idiom must use measured activation, localization, verification, and
  retained-use evidence.
  <https://arxiv.org/abs/2507.09089>

## Generic Yet Strict Architecture

The client stories should not create finance-specific, healthcare-specific, or
defense-specific code paths. They should create generic primitives with strict
contracts. Vertical policy belongs in configuration, profiles, protected-path
sets, retention rules, and acceptance gates.

Generic means:

- Use task capabilities instead of agent-specific or language-specific strategy
  instructions.
- Use profile and policy overlays instead of vertical forks.
- Use one tool manifest, one schema source, one sidecar envelope, and one event
  vocabulary across CLI, MCP, lanes, and scripts.
- Use evidence packets, graph views, plan artifacts, and audit findings that
  carry provenance, freshness, confidence, and uncertainty.
- Use the same review rubric for product stories, implementation tasks, and
  release gates.

Strict means:

- Contracts are typed, logged, and replayable.
- Profiles are default-deny and can only narrow authority unless workspace
  policy grants more.
- Freshness is explicit; stale evidence is either refreshed, reported, blocked,
  or deliberately allowed with a reason.
- Protected paths, side effects, writes, shell commands, and verification
  obligations are enforced by host policy, not prompt reminders.
- Tool schemas validate input before execution and structured results before
  downstream use.
- Every risky claim must have a file, symbol, command, log, test, audit event,
  or cited doc behind it.

### Architecture Primitives

| Primitive | Generic Role | Strict Boundary | Needs Met |
| --- | --- | --- | --- |
| `AutonomyContract` | Describes the active task authority and obligations for lanes, phases, sidecar calls, and future MCP calls. | Typed path roles, effects, tools, budgets, verification, completion, stop reasons, and policy decisions. | UX-04, UX-06, SEC-01, CTRL-01, CTRL-06, CTRL-07, CTRL-08 |
| Profile policy | Selects visible capabilities and effect authority for `discovery`, `verify`, `write`, `full`, and lane-owned modes. | Default-deny; profile filters never grant authority beyond workspace config; destructive/open-world effects require explicit allowance. | UX-02, SEC-01, CTRL-02, CTRL-11 |
| Capability resolver | Converts model/user intent such as localize, inspect, verify, or audit into concrete adapters. | Reports selected adapter, fallback, confidence, precision, absence confidence, and unresolved gaps without prescribing strategy. | UX-01, CTRL-05, CTRL-17 |
| Repository memory | Maintains graph index, views, context packs, traces, ranker signal, and evidence handles. | Every result carries freshness, provenance, confidence, stale paths, and invalidation semantics. | UX-01, UX-03, EVAL-01, CTRL-04 |
| Sidecar envelope | Normalizes CLI and MCP request/response shape for external agents and scripts. | Versioned schema, caller metadata, profile, contract ID, freshness, warnings, structured errors, and audit IDs. | UX-02, UX-07, CTRL-03, CTRL-11, CTRL-12, CTRL-16 |
| Plan artifact | Captures planner output as a durable, parseable handoff. | Packet ID, contract ID, target/evidence paths, unresolved questions, approval scope, handoff ID, verification obligations, and drift checks. | UX-06, CTRL-07, CTRL-08 |
| Verification obligation | Records what evidence is required after mutation. | Verification must occur after relevant writes or close with a concrete blocker/waiver. | UX-06, CTRL-08, P5.4 |
| Local readiness profile | Describes whether a lane or sidecar workflow can safely run on the current machine. | Role, backend, queue, classifier, context budget, freshness, and remedy checks are machine-readable. | UX-05, OPS-01, CTRL-10 |
| Audit event stream | Joins contracts, tool calls, freshness, sidecar calls, telemetry, eval results, and stop reasons. | High/medium findings need owner, status, evidence, and reproduce command before release. | AUD-01, UX-08, CTRL-15 |
| Ranker learning log | Records localization feedback from verified outcomes. | No held-out eval leakage; every boost/penalty has source, reason, graph index, and verification outcome. | UX-08, EVAL-01, CTRL-14 |

### Requirement Fulfillment Pattern

| Need | Generic Architecture Move | Strict Control | Ledger Tasks |
| --- | --- | --- | --- |
| Trust repo context | Route discovery through repository memory and capability resolver. | Freshness policy plus provenance/confidence on every graph-backed result. | P2.2, P2.4, P3.1-P3.5 |
| Trust what the agent was allowed to do | Compile an `AutonomyContract` for every lane, phase, and sidecar call. | Policy events for tools, paths, effects, obligations, and stop reasons. | P1.1-P1.6 |
| Trust external agent access | Expose sidecar profiles rather than raw tool lists. | `discovery` blocks write, shell, destructive, protected-path, and open-world effects. | P7.1, P7.2, P7.5 |
| Trust local/private operation | Keep inference and evidence local by default and make readiness explicit. | Doctor/readiness gates, queue admission, backend role probes, context budget, and redacted telemetry. | P6.1-P6.5 |
| Trust plans before mutation | Convert planner output into a plan artifact rather than free-form prose. | Plan audit, approval scope, handoff IDs, stale-packet checks, and drift evals. | P5.1-P5.4 |
| Trust verification | Treat post-write checks as obligations. | Completion cannot silently close unresolved verification without blocker or waiver evidence. | P1.5, P5.1, P5.4 |
| Trust auditability | Normalize events and replay from logs. | Audit reports high/medium findings with owner, status, evidence, and reproducer. | P1.3, P4.5, P9.3 |
| Trust improvement over time | Feed verified outcomes into evals and ranker learning. | Learning logs are inspectable and do not train on held-out eval labels. | P4.1-P4.5 |
| Trust performance | Prefer packets, graph queries, cache, and capability adapters over broad read/search. | Benchmarks track top-k coverage, zero-hit searches, repeated calls, cache/freshness, and sidecar latency. | P4.2, P4.4, P7.4, P8.3 |
| Trust vertical fit | Let verticals configure policies, protected paths, retention, and approvals. | No vertical-specific behavior lands without mapping to generic primitives and strict validation. | P0.4, P0.5, P7.5, P9.3, P9.4 |

### Vertical Policy Overlays

Vertical overlays are configuration and acceptance bundles, not separate
architectures.

- Finance overlay: protected paths for secrets and regulated data, stricter
  audit retention, caller attribution required, no write profile until
  verification and approval gates are enabled.
- Healthcare overlay: protected paths for PHI-bearing fixtures or exports,
  redaction required, telemetry retention minimized, local-only discovery
  default.
- Defense/public-sector overlay: offline-first sidecar, reproducible release
  evidence, strict supply-chain audit output, no network/open-world tools by
  default.
- Enterprise SaaS overlay: DevSecOps integration, sidecar smoke in CI,
  verification allowlists, audit finding ownership, benchmark trend reporting.
- Tooling-vendor overlay: schema compatibility, MCP parity, no strategy leakage,
  resource redaction, caller metadata, golden tests.
- Local-model overlay: backend role profile, queue caps, context budgets,
  degraded-mode policy, local smoke/capacity/replay benchmarks.

Any overlay can only set:

- profiles and allowed capabilities
- protected/forbidden path sets
- freshness policy
- verification allowlists
- telemetry retention/redaction
- backend/queue/context budgets
- audit gates and release thresholds

Any overlay must not:

- change the model/harness authority split
- add prompt-only controls for security or correctness
- bypass the shared sidecar envelope or schema source
- bypass freshness, path-role, verification, or audit obligations

### Client Classification Example Tests

This matrix is the reusable acceptance checklist for client coverage. It is not
a live assignment or status tracker; the MVP Task Ledger remains the only live
authority for implementer, reviewer, and checkbox status. Work packages and
reviews use the short row IDs below to state which examples were considered,
preserved, updated, newly added, satisfied, skipped as not applicable with a
specific reason, or converted into a no-go/follow-up.

The rows intentionally prove vertical fit through shared primitives: contracts,
profiles, protected paths, freshness, sidecar envelopes, schema parity,
readiness, verification, audit, and local evidence. They must not create
finance-specific, healthcare-specific, public-sector-specific, or vendor-specific
code paths. They tie to CTRL-18, PROD-06, PROD-08, the Client demo proof product
gate, P7.6 CLI example tests, P8.5 MCP parity, P9.4 classification coverage,
and P9.11 runnable demo transcripts.

| Row ID and classification | Buyer/user pain and sales story | Generic architectural need Idiom must meet | Example acceptance test or transcript fixture | Required evidence/artifact fields | Minimum privacy, security, and performance expectations | Go/no-go signal |
| --- | --- | --- | --- | --- | --- | --- |
| `finance`: financial services and fintech | Compliance wants developers to use existing coding agents, but only if every run proves allowed tools, protected paths, verification status, and audit trail. | Policy overlay over `AutonomyContract`, discovery profile, protected-path decisions, caller attribution, verification obligation, and replayable audit. | `finance-discovery-protected-path`: a finance overlay sidecar call attempts to inspect a regulated fixture path and returns denial/redaction, caller attribution, contract ID, and audit event IDs. | `client_classification`, `overlay_id`, `contract_id`, `profile`, `caller_id`, `protected_path_decisions`, `tool_policy_decisions`, `freshness`, `verification_status`, `audit_event_ids`, `retention_policy`. | Discovery exposes no write, shell, destructive, open-world, or protected-content access; telemetry excludes raw prompts, raw file bodies, secrets, and regulated data; fixture completes inside the configured sidecar smoke budget or records a blocker. | GO when protected access is denied or redacted and audit replay works without raw logs; NO-GO if protected content leaks, caller attribution is missing, or verification/audit status is absent. |
| `healthcare`: healthcare and life sciences | Engineering wants AI help on clinical or life-sciences systems without accidental PHI exposure or untraceable code changes. | Protected PHI fixture policy, redaction, telemetry minimization, freshness, local/private evidence, and CLI/MCP resource containment. | `healthcare-phi-redaction-fixture`: a healthcare overlay marks PHI-bearing fixtures as protected, then CLI and any MCP resource path return redacted or denied evidence without raw content. | `client_classification`, `overlay_id`, `phi_fixture_id`, `redaction_policy_id`, `resource_id`, `freshness`, `telemetry_fields`, `retention_policy`, `audit_event_ids`, `blocked_or_redacted_reason`. | No PHI, raw prompt, raw file body, or secret appears in tool output, telemetry, resources, or product evidence; stale PHI-adjacent evidence cannot be treated as fresh; response size and latency stay within the configured fixture threshold or record a blocker. | GO when protected PHI fixtures stay inaccessible and telemetry is metadata-only; NO-GO if any raw protected content appears or stale/redacted state is not explicit. |
| `public-sector`: public-sector, government, defense, and critical infrastructure | A software factory needs agentic coding to be reproducible, offline-capable, and reviewable before sensitive repos can trust it. | Offline-friendly sidecar profile, default-deny network/open-world effects, local readiness, supply-chain evidence, release commands, and audit ownership. | `public-sector-offline-smoke`: an offline/public-sector overlay runs the sidecar smoke with network/open-world tools unavailable and emits reproducible release-evidence commands plus blockers. | `client_classification`, `overlay_id`, `offline_mode`, `profile`, `network_policy`, `command_digest`, `input_fixture_hash`, `output_artifact_paths`, `tool_versions`, `audit_event_ids`, `blockers`. | Network, open-world, destructive, and unapproved shell effects are unavailable by policy; roots are clamped; evidence is reproducible from local commands without hosted telemetry or raw sensitive content; runtime stays inside the configured smoke budget or names the blocker. | GO when the smoke is replayable offline with denied external effects; NO-GO if the fixture needs network/open-world access, writes outside policy, or cannot reproduce evidence from recorded commands. |
| `enterprise-saas`: enterprise SaaS and platform teams | Platform owners need merge-facing evidence across agents, not another chat surface developers must manually police. | Repository memory, context localization, verification obligations, CI/script-friendly sidecar envelope, audit findings with owner/status, and benchmark trend evidence. | `enterprise-ci-verification-smoke`: a CI-style workflow localizes a change, reports suggested verification, runs the allowed verification command, and records audit ownership for findings. | `client_classification`, `packet_id`, `index_id`, `changed_paths`, `suggested_tests`, `verification_commands`, `exit_status`, `finding_owner`, `finding_status`, `caller_id`, `trace_id`, `latency_ms`. | Outputs are parseable by CI/scripts; no raw prompt or raw file body is required for product evidence; high/medium findings have owner and status; localization and verification finish within configured CI smoke thresholds or record a blocker. | GO when merge evidence includes freshness, changed-path relation, verification result, and owned findings; NO-GO if verification is missing, stale evidence is silent, or high findings are unowned. |
| `tooling-vendor`: AI tooling, agent-platform, IDE vendor, and consultancy teams | Tool builders and consultants need repo intelligence they can embed through stable contracts instead of rebuilding graphs, policies, and eval loops for every client. | One schema source, sidecar envelope parity, MCP/CLI compatibility, caller metadata, resource redaction, structured errors, and no strategy leakage. | `tooling-vendor-cli-mcp-parity`: the same discovery request is run through CLI and MCP; outputs match on profile-filtered capabilities, resource visibility, errors, caller metadata, and redaction. | `client_classification`, `schema_version`, `profile`, `transport`, `caller_id`, `request_id`, `contract_id`, `tool_names`, `resource_ids`, `error_codes`, `strategy_leakage_check`, `audit_event_ids`. | Discovery parity grants no broader authority over MCP than CLI; outputs never prescribe next agent strategy except typed precondition blocks; resources are redacted/root-clamped; latency and payload size stay inside compatibility thresholds or record a blocker. | GO when CLI and MCP expose equivalent contracts and no strategy leakage; NO-GO if schemas diverge, MCP grants broader access, or outputs prescribe implementation strategy. |
| `local-model`: local-model, offline, and privacy-first teams | Operators want Qwen, llama.cpp, MLX, Ollama, or OpenAI-compatible local endpoints to fail predictably instead of hiding overload, routing, or context-budget failures. | Local readiness profile, role routing, queue admission, context budget, degraded-mode policy, redacted telemetry, and replayable local smoke/capacity evidence. | `local-model-readiness-degraded`: doctor or lane preflight reports planner/executor/backend/queue/context readiness before a lane and blocks or degrades an unsafe local run with a machine-readable reason. | `client_classification`, `backend_id`, `role`, `queue_depth`, `context_budget`, `readiness_status`, `degraded_reason`, `smoke_command`, `telemetry_event_ids`, `latency_ms`, `replay_artifact`. | All evidence remains local by default; telemetry excludes raw prompts and file bodies; unsafe or overloaded execution blocks or degrades before mutation; queue and retry behavior are bounded and visible. | GO when unsafe local execution blocks/degrades with a replayable reason; NO-GO if a lane starts despite failed readiness, spins unbounded retries, or emits raw prompt/file content. |
| `open-source`: open-source and individual maintainers | Maintainers want a low-friction trust layer for whichever agent they already use, with fresh packets, suggested tests, and audit evidence they can understand quickly. | Quickstart path through `context_pack`, `context_status`, `harness_audit`, verification evidence, ranker feedback, cleanup, and docs-gap reporting. | `open-source-quickstart-transcript`: a clean local recipe runs `context_pack`, checks freshness, runs `harness_audit` after a small verified change, records elapsed time, and cleans up state. | `client_classification`, `install_steps`, `elapsed_time`, `packet_id`, `freshness`, `verification_commands`, `audit_findings`, `learning_log_ref`, `cleanup_result`, `docs_gap`, `blockers`. | Works without internal Idiom knowledge; no hosted telemetry, raw prompt capture, raw file-body upload, or secrets are required; first useful packet plus audit meets the activation threshold from P0.10/P7.7/P9.6 or records a precise blocker. | GO when a maintainer can reach first useful packet, freshness, audit, and cleanup from the transcript; NO-GO if the path requires hand-holding, cannot produce audit evidence, or uploads raw content. |

### P0.9 Product Security And Privacy Posture

This section is the P0.9 artifact. It is a product threat model, data-flow map,
privacy/retention posture, and control mapping for Idiom's MVP and final-state
architecture. It is mapping evidence for implementation and review, not a
regulatory compliance claim, legal opinion, certification, or attestation.
Regulated examples remain proof-only until P7.9 automated checks, P8.4 MCP
parity checks, P9.1 release evidence, and P9.9 reviewer signoff exist.

Source anchors checked on 2026-05-13 UTC:

| Anchor | Checked source | P0.9 use |
| --- | --- | --- |
| NIST AI RMF Core | <https://airc.nist.gov/airmf-resources/airmf/5-sec-core/> and overview <https://www.nist.gov/itl/ai-risk-management-framework> | Frames Idiom controls as Govern, Map, Measure, and Manage evidence: policy ownership, context/risk mapping, tested controls, and go/no-go response. The source is voluntary guidance, not compliance proof. |
| NIST SSDF and GenAI community profile | SP 800-218 final: <https://csrc.nist.gov/pubs/sp/800/218/final>; SP 800-218A final: <https://csrc.nist.gov/pubs/sp/800/218/a/final> | Anchors secure-development, acquisition, vulnerability, and AI-system development vocabulary for schema, verification, release evidence, and supply-chain-sensitive agent tooling. |
| OWASP GenAI / LLM Top 10 2025 | <https://genai.owasp.org/llm-top-10/> | Anchors current LLM01:2025 Prompt Injection, LLM02:2025 Sensitive Information Disclosure, LLM03:2025 Supply Chain, LLM05:2025 Improper Output Handling, LLM06:2025 Excessive Agency, LLM07:2025 System Prompt Leakage, LLM08:2025 Vector and Embedding Weaknesses, LLM09:2025 Misinformation, and LLM10:2025 Unbounded Consumption where those risks apply to coding-agent workflows. |
| MCP security, authorization, tools, resources, and roots | Authorization: <https://modelcontextprotocol.io/specification/2025-11-25/basic/authorization>; tools: <https://modelcontextprotocol.io/specification/2025-11-25/server/tools>; resources: <https://modelcontextprotocol.io/specification/2025-11-25/server/resources>; roots: <https://modelcontextprotocol.io/specification/2025-11-25/client/roots>; security best practices: <https://modelcontextprotocol.io/docs/tutorials/security/security_best_practices> | Anchors caller authorization, token audience boundaries for future non-stdio transports, tool input/output validation, untrusted tool annotations, resource URI validation, access control, root containment, local-server compromise risk, consent, and audit needs. |
| HHS HIPAA Security Rule risk analysis | <https://www.hhs.gov/hipaa/for-professionals/security/guidance/guidance-risk-analysis/index.html> | Anchors healthcare examples to risk analysis for confidentiality, integrity, and availability of e-PHI. Idiom uses this only to shape generic protected-path, redaction, and audit controls; it does not claim HIPAA compliance. |
| FINRA GenAI member-firm note | <https://www.finra.org/sites/default/files/2025-07/How-Member-Firms-Use-GenAI.pdf> | Anchors finance examples to FINRA's observed GenAI coding use case and its reminder that existing obligations continue to apply. Idiom maps audit/protected-path evidence to generic controls, not broker-dealer compliance. |
| Telemetry and privacy minimization | OpenTelemetry sensitive-data guidance: <https://opentelemetry.io/docs/security/handling-sensitive-data/>; NIST Privacy Framework: <https://www.nist.gov/privacy-framework> | Anchors sensitive-data minimization, allowlisted telemetry attributes, redaction, hashing, aggregation, retention review, and privacy-risk management for product evidence. |

Assumptions and non-claims:

- MVP architecture is a local CLI sidecar plus local repository memory, graph
  views, logs, telemetry, audit, evidence export, and external caller recipes.
  Future-state MCP exposes the same discovery profile and evidence model over
  stdio first.
- P0.7 sets `enterprise-saas` as the primary paid beachhead,
  `open-source` as the adoption wedge, `tooling-vendor` and `local-model` as
  secondary rows, and finance/healthcare/public-sector as regulated proof rows.
- Idiom may process repository source, prompts, and model output locally during
  a run, but product evidence must not require hosted telemetry, raw prompt
  collection, raw file-body collection, secrets, PHI, financial account data, or
  customer-regulated data.
- Finance, healthcare, and public-sector rows are proof overlays over generic
  primitives. They must not create vertical-specific code paths.
- Exact retention enforcement is future validation work. Until P7.9 implements
  retention/redaction checks and P9.9 reviews them, retention windows below are
  target gates or placeholders, not current-state guarantees.

Product threat model:

| Threat surface | Threat or failure mode | Generic Idiom controls | Validation and go/no-go link |
| --- | --- | --- | --- |
| Untrusted repo content and prompts | Prompt injection, malicious comments, or task text persuade a model or caller to read protected data, broaden scope, skip verification, or leak strategy. | `AutonomyContract`, default-deny profiles, no-strategy-leakage rule, protected-path roles, verification obligations, source provenance, and audit events. | P1.4/P1.6 define path/effect events; P7.5/P7.9 test denial and redaction; P9.9 blocks release if protected data leaks or strategy leakage appears. |
| External coding-agent callers | A script, Codex, Claude Code, OpenCode, Aider, or CI job calls Idiom without stable caller identity, profile, freshness policy, or contract narrowing. | Sidecar envelope with schema version, `caller_id`, request/trace IDs, profile, contract ID, freshness policy, warnings, structured errors, and audit IDs. | P7.1/P7.4/P7.8 must prove parseable caller-attributed output; P9.8 and P9.9 block if attribution is missing for security-relevant calls. |
| Context graph, views, and packets | Graph caches or context packets become stale, store raw sensitive content unnecessarily, or are treated as authoritative without freshness evidence. | Repository memory emits `index_id`, packet ID, provenance, confidence, stale paths, refresh state, ranker reasons, and protected-path decisions. | P2.2/P2.4 prove freshness; P7.9 proves protected content is denied/redacted; P9.9 blocks regulated claims if stale or protected evidence can be used silently. |
| Logs, telemetry, and audit | Raw prompts, file bodies, secrets, PHI, financial data, local paths, or user identity leak into telemetry, logs, audit summaries, or product evidence. | Allowlisted telemetry fields, redaction at collection and export boundaries, pseudonymous caller/user IDs, hashes instead of content, local-only defaults, and retention metadata. | P7.9 checks no raw prompt/file-body/secret telemetry; P7.11 checks product export minimization; P9.9 requires reviewer signoff and source-mapping completeness. |
| Evidence export and partner records | P0.8/P7.11 evidence captures customer-sensitive details, becomes a shadow analytics store, or overclaims compliance. | Pseudonymous design-partner records, artifact hashes, command IDs, scores, retention expiration, redaction reviewer, and explicit non-compliance wording. | P0.8 defines forbidden fields; P7.11 exports summaries only; P9.7/P9.9 reject records that need hosted telemetry or raw content. |
| Protected-path behavior | CLI, lane, shell, patch, graph, telemetry, evidence export, or MCP resource paths disagree on protected, forbidden, evidence, or side-effect roles. | Shared path-role resolver and policy events; deny or redact before data enters graph views, tool output, telemetry, audit, or resource responses. | P1.4/P1.6 create one resolver vocabulary; P7.5/P7.9 and P8.4 must prove parity; any transport-specific bypass is a no-go. |
| MCP tools and resources | MCP exposes broader tool authority than CLI, trusts annotations as security truth, lists protected resources, reads outside roots, or passes tokens across trust boundaries. | MCP uses the same schema/profile source; resources are root-clamped and redaction-ready; tool annotations are untrusted; tool inputs/outputs are validated; future authorization validates token audience and forbids token passthrough. | P8.1-P8.4 prove profile, root, resource, error, caller, redaction, and freshness parity. Disable MCP resources if containment or redaction fails. |
| Local model readiness | Local Qwen/llama.cpp/other backends overload, start with unsafe context budgets, retry unboundedly, or leak prompt/file content through readiness telemetry. | Local readiness profile, role routing, bounded queues, context budget, degraded-mode blockers, retry limits, local smoke/capacity evidence, and metadata-only telemetry. | P6.1-P6.5 and P7.9 must show blocked/degraded states and redacted telemetry. Unsafe readiness before mutation is a no-go. |
| Side effects and verification | Discovery profile accidentally writes, shells out, performs open-world network access, or allows a mutable task to close without verification/blocker evidence. | Default-deny discovery, side-effect classes, approval outcomes, verification obligations, allowed verification commands, and audit/release gates. | P7.2/P7.5/P7.9 prove no write/shell/destructive/open-world discovery effects; P5.4/P9.3/P9.9 block unresolved high security regressions. |
| Release and audit evidence | Reviewers cannot reproduce security claims, high findings are unowned, control mappings are incomplete, or cited anchors become stale. | P9.1 release bundle, P9.3 finding ownership, P9.5 review protocol, source checked dates, P9.9 posture review, and explicit unknown/follow-up list. | P9.9 cannot pass without checked sources, P7.9/P8.4 evidence, complete client-row mapping, and reviewer GO. |

Data-flow map:

| Flow | Actors and transport | Data classes | Storage or artifact | Trust boundary and minimization rule |
| --- | --- | --- | --- | --- |
| F1 request entry | Developer, script, CI, or external coding agent to `idiom tool` over local process/stdin/stdout. Future MCP uses stdio first. | Task string, root, cwd, profile, freshness policy, caller metadata, contract narrowing, trace ID. | Sidecar request/response envelope; audit event stream. | Caller input is untrusted. Validate schema, bind caller/profile/contract, and do not require secrets or raw customer data in request metadata. |
| F2 repository read | Idiom workspace tools to local repo files and VCS metadata. | Paths, symbols, snippets or file bodies when permitted, test names, diffs, blame/history, dependency data. | User repository remains the source of truth; derived metadata may enter graph/index artifacts. | Raw file bodies stay local and must not flow to product evidence. Protected and forbidden paths deny or redact before graph, output, telemetry, audit, or MCP resource exposure. |
| F3 context memory | Workspace tools to context graph, context packets, views, traces, and ranker logs. | Derived paths, symbols, handles, hashes, provenance, confidence, freshness, stale paths, ranker reasons, possible snippets if later enabled. | `.idiom/comprehension/graph.sqlite`, `.idiom/comprehension/views/*.json`, `.idiom/comprehension/traces/`, and learning logs. | Derived evidence must carry freshness and data-class metadata. P7.9 must classify any snippet/file-body fields and prove protected data does not persist. |
| F4 model/local runner | Runner or lane to local backend and back to logs/audit. | Prompt/context assembled for local inference, model output, stop reason, readiness state, queue/context budget. | Local process memory, local logs, telemetry, audit. | Local inference does not make prompt bodies safe for product evidence. Telemetry exports metadata only and excludes raw prompt/file bodies and secrets. |
| F5 sidecar output | CLI stdout/stderr or future MCP tool result to external caller. | Structured findings, candidate files, symbols, counts, freshness, warnings, denied/redacted reasons, audit IDs, suggested verification. | Caller transcript, integration proof, optional product evidence summary. | Output is a boundary. It may expose repo facts only under the active profile and path policy. It must not prescribe strategy except typed precondition blocks. |
| F6 telemetry/audit | Runtime, sidecar, workspace tools, and MCP server to local telemetry/audit logs. | Operation names, caller/profile/contract IDs, paths or hashed paths, data class, policy decisions, freshness, latency, errors, verification status. | `.idiom/logs/conversations/*.jsonl`, `logs/telemetry/*.jsonl`, audit reports, release bundle summaries. | Use allowlisted fields and redaction at write time. Raw prompts, raw file bodies, secrets, PHI, financial data, and customer-regulated data must not flow into telemetry or product evidence. |
| F7 product evidence | Baseline/current commands, audit, benchmark, sidecar smoke, P0.8 records, and P7.11 export to release docs. | Artifact paths or hashes, command labels, exit status, packet/index IDs, rubric scores, blockers, redaction reviewer, retention expiration. | `docs/evidence/...`, P7.11 export, P9.1/P9.9 release bundles. | Evidence is local, summarized, and reproducible. It must not include raw prompts, raw file bodies, secrets, or real regulated data. |
| F8 MCP resources | Future MCP server to MCP client through `resources/list`, `resources/read`, tools, roots, and protocol errors. | Resource IDs/URIs, metadata, selected context packets/audit reports/log summaries, resource contents only when allowed. | Same underlying CLI evidence artifacts; no separate MCP retention store by default. | Roots are clamped to declared project roots; resource URIs are validated; protected paths deny/redact; protocol errors must not leak raw content; MCP authority cannot exceed CLI discovery profile. |
| F9 partner validation | P0.8 interviewer/operator to validation records and P7.11/P9.7 summaries. | Pseudonym or approved name, org size band, role split, repo class, workflow labels, privacy constraints, scores, adoption signal type, retention expiration. | P0.8 records and summarized product-evidence export. | Collect only what proves buyer value. Raw prompts, raw file bodies, secrets, real PHI, account data, and customer-sensitive content are forbidden fields. |

Raw data must not flow to these destinations: product-evidence exports,
partner-validation records, release bundles, telemetry summaries, audit
dashboards, public docs, MCP `resources/list`, or MCP protocol errors. Raw
repository file bodies and prompts may be processed locally only where the
active profile, path policy, and task require them; they must not be retained or
exported as product proof by default. Secrets and credentials should not be
collected at all and must be redacted if encountered.

Privacy and retention posture:

| Data or artifact class | Default posture | Retention target or placeholder | Release blocker |
| --- | --- | --- | --- |
| Repository source and diffs | User-owned local data. Idiom reads only under active profile and path policy. | Not copied into product evidence. Any local derived cache is cleared by index rebuild or future cleanup command. | Raw source appears in release evidence, P0.8 records, telemetry, or MCP resource output for protected paths. |
| Context graph, packets, views, traces, and ranker logs | Local derived repository memory with provenance and freshness. Prefer paths, symbols, hashes, counts, and summaries over body text. | Retain locally until rebuild/cleanup. P7.9 must document cleanup behavior and classify any snippet/body fields before release. | Derived artifacts lack freshness/data-class metadata or retain protected content after denial/redaction should apply. |
| Conversation logs and operational logs | Local diagnostic evidence, not product analytics. Treat as high sensitivity if prompt text or file snippets are present. | Placeholder: P7.9 must either exclude raw prompt/file bodies or set a short configurable retention target before P9.9. Product evidence may retain only summarized/hardened fields. | Release claims depend on raw logs or fixed retention is claimed without enforcement. |
| Telemetry and audit events | Metadata-only by default: operation, caller/profile/contract IDs, data class, policy decision, freshness, timing, status, and error class. | Target: local configurable retention, with release summaries retained through P9.1/P9.9. Recheck fields each release. | Raw prompts, raw file bodies, secrets, or regulated data appear in telemetry; caller attribution is missing. |
| Product-evidence export | Local, summarized evidence for activation, retained use, integration, benchmark, audit, learning, blocker, and adoption signals. | Retain with release evidence or delete/regenerate on redaction failure. Include source artifact hashes and redaction policy IDs. | Export requires hosted telemetry, raw prompts, raw file bodies, or secrets. |
| Design-partner records | Privacy-preserving records from P0.8 with pseudonyms, approved names only when consented, rubric scores, artifact hashes, and expiration. | Target: 90 days or shorter by participant agreement unless renewed for release evidence. Every record needs `retention_expiration`. | Records include forbidden fields or lack a retention expiration/redaction reviewer. |
| Regulated examples and fixtures | Use synthetic or representative fixtures. Real PHI, financial account data, controlled customer data, or public-sector sensitive data is not needed for MVP proof. | Keep only fixture metadata and denial/redaction evidence unless a private customer validation separately approves handling. | Any public or release evidence contains real regulated content or implies compliance certification. |
| MCP resources and tool outputs | Same evidence model as CLI. MCP must not create a broader data store, broader retention posture, or wider authority. | No separate MCP retention by default. Resource caches, if introduced, inherit CLI retention and redaction rules. | MCP lists/reads outside roots, leaks protected content, or diverges from CLI retention/redaction semantics. |

Control mapping evidence:

| Idiom primitive | Control evidence Idiom must produce | External anchor mapping | Client rows |
| --- | --- | --- | --- |
| `AutonomyContract`, permission profiles, side-effect classes | Profile, allowed tools/effects, path roles, verification obligations, approval/denial decisions, and stop reasons are typed and auditable. | NIST AI RMF Govern/Map/Manage for policy and risk response; NIST SSDF/SP 800-218 for secure development vocabulary; OWASP LLM06:2025 Excessive Agency and LLM09:2025 Misinformation for bounded authority and output-trust checks; MCP tools guidance on access controls and user confirmation. | `enterprise-saas`, `finance`, `public-sector`, `tooling-vendor`, `open-source` |
| Protected-path resolver and redaction | Required/evidence/protected/forbidden paths, deny/redact decisions, redaction policy ID, caller ID, and audit event IDs. | NIST AI RMF Map/Measure privacy and security risk documentation; OWASP LLM02:2025 Sensitive Information Disclosure; HHS risk analysis for e-PHI confidentiality/integrity/availability; FINRA existing-obligation reminder for finance examples; OpenTelemetry minimization/redaction. | `finance`, `healthcare`, `public-sector`, `enterprise-saas` |
| Sidecar envelope and caller attribution | Stable schema, `caller_id`, request ID, trace ID, profile, contract ID, freshness, warnings, structured errors, and audit IDs across shell/CI/external-agent/MCP paths. | NIST AI RMF transparency/accountability and Measure/Manage evidence; MCP authorization and security best practices for accountability, token audience, consent, and token-passthrough risks; OWASP LLM06:2025 Excessive Agency for caller/tool authority and LLM02:2025 Sensitive Information Disclosure for token/context exposure. | `enterprise-saas`, `tooling-vendor`, `finance`, `public-sector`, `open-source` |
| Context graph, views, packets, and freshness | `index_id`, packet ID, stale paths, refresh state, provenance, confidence, ranker reasons, and unresolved gaps. | NIST AI RMF Map/Measure for context, limits, testing, and traceability; SSDF/SP 800-218 for acquisition/common vocabulary and vulnerability reduction; OWASP LLM03:2025 Supply Chain, LLM08:2025 Vector and Embedding Weaknesses, and LLM09:2025 Misinformation for dependency/context provenance, retrieval quality, and stale or incorrect evidence. | `enterprise-saas`, `open-source`, `tooling-vendor`, `public-sector` |
| Telemetry, audit, and product-evidence export | Allowlisted fields, redacted/hardened summaries, hashes instead of content, retention expiration, redaction reviewer, finding owner/status, reproduce commands. | OpenTelemetry sensitive-data minimization and redaction; NIST Privacy Framework privacy-risk management; NIST AI RMF Govern/Measure/Manage documentation and incident response; HHS/FINRA as sector proof overlays, not compliance claims. | All rows, with regulated rows proof-only until P9.9 |
| MCP tools, resources, roots, and protocol errors | CLI/MCP schema parity, tool input/output validation, root clamping, resource URI validation, resource redaction, freshness parity, caller attribution, and no trust in untrusted annotations. | MCP tools/resources/roots/security/authorization guidance; OWASP LLM05:2025 Improper Output Handling, LLM02:2025 Sensitive Information Disclosure, LLM06:2025 Excessive Agency, and LLM07:2025 System Prompt Leakage; NIST AI RMF Manage for risk treatment. | `tooling-vendor`, `finance`, `healthcare`, `public-sector`, `enterprise-saas` |
| Local-model readiness and bounded execution | Role/backend/queue/context readiness, degraded or blocked reasons, retry/queue bounds, local smoke/capacity/replay evidence, metadata-only telemetry. | NIST AI RMF Measure/Manage for monitoring and safe failure; OWASP LLM10:2025 Unbounded Consumption and LLM06:2025 Excessive Agency; OpenTelemetry minimization for readiness telemetry. | `local-model`, `enterprise-saas`, `public-sector`, `open-source` |
| Release gates and review protocol | P9.1 bundle, P9.3 high/medium findings with owner/status/reproducer, P9.5 schema-complete review, P9.9 control mapping and source dates. | NIST AI RMF Govern/Measure/Manage; NIST SSDF/SP 800-218 and SP 800-218A secure-development evidence; OWASP risk classes; HHS/FINRA sector anchors; MCP protocol security anchors. | All rows |

Client-classification control ties:

| Row | P0.9 control story | MVP stance |
| --- | --- | --- |
| `enterprise-saas` | Prove merge-facing evidence: freshness, verification, caller attribution, owned audit findings, and no raw prompt/file-body evidence. | Primary paid beachhead. P0.9 must make the security/privacy posture reviewable without turning Idiom into a hosted governance dashboard. |
| `finance` | Prove protected-path denial/redaction, audit replay, verification status, caller attribution, and retention policy under a finance overlay. | Deferred regulated expansion proof. Do not claim FINRA compliance or regulated sales readiness before P9.9. |
| `healthcare` | Prove PHI-marked fixture denial/redaction, telemetry minimization, resource containment, stale-state visibility, and no real e-PHI in public evidence. | Deferred regulated expansion proof. Use HHS guidance to harden generic privacy primitives only. |
| `public-sector` | Prove offline/local operation, no default network/open-world effects, reproducible release evidence, root containment, and owned findings. | Proof-only for MVP. Do not cite unverified public-sector sources as release proof. |
| `tooling-vendor` | Prove CLI/MCP schema parity, resource redaction, caller metadata, structured errors, and no strategy leakage for embeddable sidecar use. | Secondary integration path. MCP cannot create broader authority or retention than CLI. |
| `local-model` | Prove readiness gates, bounded queues, context budgets, degraded-mode blockers, and redacted local telemetry. | Secondary enabling segment. Security value is predictable local operation, not a separate local-agent product. |
| `open-source` | Prove quickstart evidence, local-only defaults, audit readability, cleanup, and no hosted/raw-content product evidence. | Adoption wedge and proof pool. It cannot replace primary enterprise-SaaS demand evidence. |

Downstream go/no-go gates:

| Gate | GO requires | NO-GO if |
| --- | --- | --- |
| P7.9 security/privacy checks | Automated or scripted checks cover protected paths, redaction, retention metadata, telemetry minimization, side-effect policy, caller attribution, audit/event evidence, and no raw prompt/file-body/secret product evidence. Checks must exercise generic profiles/overlays, not vertical branches. | Protected content leaks; discovery profile writes/shells/performs destructive or open-world effects; telemetry/export contains raw prompts, raw file bodies, secrets, PHI, or account data; retention policy is absent; denial events lack caller/contract/audit IDs. |
| P8.4 MCP resource parity | MCP resources/list/read are root-clamped, protected-path aware, redaction-ready, freshness-aware, caller-attributed, and no broader than CLI discovery. Protocol errors do not leak raw content. Tool annotations are not treated as security authority. Future non-stdio auth must validate token audience and avoid token passthrough. | MCP lists or reads outside roots; resource output leaks protected content; MCP freshness differs silently from CLI; caller attribution is missing; MCP introduces a separate retention store or broader authority. |
| P9.9 release posture review | Reviewed bundle includes this threat model, data-flow map, privacy/retention posture, checked source anchors, control mapping, client-row tieback, P7.9/P8.4 evidence, P9.1 release evidence, P9.3 finding ownership, and explicit unknown/follow-up list. | Control mapping is incomplete; source anchors are stale/unreachable without a recorded verification method; regulated claims imply compliance; high contract/security regressions are unresolved; review output is not schema-complete GO or project-approved no-go/pivot. |

Known gaps and future validation requirements:

- P7.9 must make retention, redaction, telemetry allowlists, and cleanup
  behavior executable. This section alone is not enough.
- P8.4 must prove MCP resource and root semantics match CLI behavior before any
  MCP-specific security or vendor claim is used.
- P9.9 must re-check cited sources, attach verification method for any source
  that blocks command-line access, and preserve the distinction between control
  mapping evidence and compliance claims.
- Real customer-regulated data requires a separate customer-approved handling
  plan outside this MVP public evidence path.

## Requirement Traceability

Every requirement below must map to one or more ledger tasks. If a requirement
is only represented by prose, it is not implementation-ready.

| ID | Requirement | MVP Experience | Final-State Experience | Plan Coverage |
| --- | --- | --- | --- | --- |
| UX-01 | Pre-edit localization | Caller runs `context_pack` before editing and receives ranked, fresh, source-linked evidence. | Agents use Idiom context automatically through CLI or MCP before risky edits. | P2.2, P3.1, P4.1, P4.2, P7.3 |
| UX-02 | Read-only sidecar | External agents get repository facts under `discovery` without write or shell authority. | Teams expose profile-scoped sidecar access across multiple agents. | P7.1, P7.2, P7.5, P8.1 |
| UX-03 | Freshness confidence | Graph-backed output shows fresh, stale, refreshed, or blocked state. | Callers choose freshness policy and stale evidence cannot support writes silently. | P2.2, P2.3, P2.4, P7.4 |
| UX-04 | Contract replay | Logs show active contract, path roles, allowed effects, and obligations. | A run can be replayed from task through plan, handoff, execution, verification, and closeout. | P1.1, P1.2, P1.3, P5.4 |
| UX-05 | Local-model readiness | `idiom doctor --json` explains pass, degraded, or blocked readiness. | Role, backend, queue, classifier, and context capacity are visible before and after each lane. | P6.1, P6.2, P6.3, P6.4, P6.5 |
| UX-06 | Auditable plan handoff | Plan artifact names packet, target paths, evidence, questions, and verification obligations. | Partial approvals, handoff IDs, stale-packet overrides, and execution drift are audit-checked. | P5.1, P5.2, P5.3, P5.4 |
| UX-07 | Stable sidecar envelope | Scripts parse schema version, profile, freshness, structured output, warnings, and errors. | CLI, MCP, lanes, and future SDKs share one versioned contract envelope. | P2.1, P7.1, P7.4, P8.1 |
| UX-08 | Improvement signal | At least one verified edit or grounded feedback signal updates ranker state. | Failed runs become evals, audit rules, ranker changes, contract changes, prompt cleanups, or explicit non-goals. | P4.3, P4.5, P9.3 |
| AUD-01 | Behavior audit | `harness_audit` surfaces broad reads, zero-hit searches, stale packets, malformed calls, and missing lifecycle/verification. | Audit reports ownership, status, reproducer, and trend for high/medium findings. | P0.2, P1.3, P4.5, P9.3 |
| EVAL-01 | Localization benchmark | `context_pack` top-k edit-path coverage beats ordinary search on a focused corpus. | Benchmark reports outcome, trajectory, cache/freshness, ranker, verification, and audit metrics. | P4.1, P4.2, P4.4 |
| SEC-01 | Least-privilege defaults | Discovery profile exposes no mutation, shell, destructive, or open-world tools. | Destructive/open-world actions require explicit contract allowance and auditable approval. | P7.2, P7.5, P1.6 |
| OPS-01 | Role-aware local model stack | Backends declare planner, executor, classifier, and sidecar readiness. | Operators can tune role, context, timeout, queue pool, and degraded-mode behavior per lane. | P6.1, P6.2, P6.4 |
| CTRL-01 | Typed autonomy contract | Passive contract events exist for lanes, phases, and sidecar calls. | Contract drives tool visibility, path roles, verification, completion, audit, replay, and eval. | P1.1, P1.2, P1.4, P1.5 |
| CTRL-02 | Permission profiles | `discovery` profile filters sidecar tools. | `discovery`, `discovery+sec`, `verify`, `write`, `full`, lane-owned, planner-only, and verification-only profiles share one policy model. | P7.2, P7.5, P8.1 |
| CTRL-03 | Tool schema source | Schemas export from the existing manifest. | CLI, MCP `tools/list`, audit, docs, and compatibility tests use one schema source. | P2.1, P2.3, P7.4, P8.1 |
| CTRL-04 | Freshness controls | Graph-backed tools return index, stale paths, confidence, and refresh state. | Caller-selectable `report_only`, `read_through`, `require_fresh`, and `no_refresh` policies are enforced. | P2.2, P2.4, P7.4 |
| CTRL-05 | Capability routing | Read-only capabilities route through existing adapters with fallback evidence. | Full registry reports precision, fallback, confidence, absence confidence, and unmapped adapters. | P3.1, P3.2, P3.4, P3.5 |
| CTRL-06 | Path roles and protected paths | Contracts record required edit, evidence, protected, and forbidden paths. | All writes, patches, shell, and MCP filesystem calls pass through shared path-role policy. | P1.4, P1.5, P7.5 |
| CTRL-07 | Planner and completion gates | Planner and completion decisions emit stable events and blockers. | Planner cannot mutate; mutable-task closeout flows through contract obligations and waivers. | P1.5, P5.1, P5.4 |
| CTRL-08 | Verification controls | Write-capable lanes record verification state or blockers. | Verify profile uses configured allowlists and records command, status, blocker, and changed-path relation. | P1.5, P5.1, P6.3, P7.4 |
| CTRL-09 | Loop and budget fuses | Audit tracks zero-hit searches, same-tool repeats, redirect storms, and exhausted budgets. | Contracts enforce repeat limits, post-readiness narrow probes, and normalized stop reasons. | P0.2, P1.5, P4.4 |
| CTRL-10 | Backend and queue controls | Doctor reports role/backend/queue readiness with remedies. | Queue caps, memory class, context limits, stale in-flight jobs, and per-role health are enforced or degraded visibly. | P6.1, P6.2, P6.3, P6.4, P6.5 |
| CTRL-11 | Sidecar request controls | CLI calls support profile, stable output, freshness, and structured errors. | Request envelope carries root, cwd, contract narrowing, path scopes, freshness policy, trace IDs, and caller metadata. | P7.1, P7.2, P7.4 |
| CTRL-12 | MCP controls | Stdio discovery server exposes profile-filtered tools. | MCP resources, roots, telemetry attribution, redaction, and protocol errors match CLI semantics. | P8.1, P8.2, P8.3, P8.4 |
| CTRL-13 | Telemetry and redaction controls | Process telemetry is redacted and caller attribution begins with sidecar smoke. | Retention, redaction, caller, no raw prompt/file body, and no secret policies are configurable. | P7.4, P7.5, P8.3, P8.4 |
| CTRL-14 | Ranker-learning controls | One verified learning source writes inspectable logs. | Learning sources are operator-reviewable, avoid held-out eval leakage, and explain boosts/penalties. | P4.3, P4.5 |
| CTRL-15 | Release evidence gates | Baseline/current bundles, tests, audit, benchmark, and sidecar smoke are required. | Go/no-go includes no unresolved high contract/security regressions and owned high/medium audit findings. | P9.1, P9.2, P9.3 |
| CTRL-16 | Compatibility controls | Existing sidecar output remains usable during migration. | Versioned `idiom.tool.v1` has golden tests, deprecation policy, and compatibility report. | P7.1, P7.4 |
| CTRL-17 | No strategy leakage | Evidence tools report findings, paths, symbols, counts, freshness, and uncertainty. | Tool outputs never prescribe next strategy except for hard typed preconditions or invariant blocks. | P3.3, P7.4, P8.3 |
| CTRL-18 | Client-classification example tests | Every work package records a matrix-row coverage decision for finance, healthcare, public-sector, enterprise SaaS, tooling-vendor, local-model, and open-source examples when the task can affect them. | Release includes at least one executable example test per classification, each mapped to generic primitives, required evidence fields, minimum privacy/security/performance expectations, and go/no-go results rather than vertical-specific code paths. | P0.5, P7.6, P8.5, P9.4 |
| CTRL-19 | Mandatory review protocol | Every review subagent receives the canonical reviewer prompt and returns the required review output schema. | No task can be marked reviewed without a separate review subagent's schema-complete GO, criterion-by-criterion evidence, client-classification coverage, product-success coverage, verification evidence, and signoff; exceptions, no-go decisions, and pivots must be project-approved and recorded in this plan. | P0.6, P9.5 |
| PROD-01 | Beachhead ICP | MVP names `enterprise-saas` as the primary paid buyer row, `open-source` as the adoption wedge, secondary and deferred rows, non-targets, buyer/user split, trigger event, buying criteria, falsifiable continue/pivot/no-go signals, and final-state expansion path. | Release decision includes proof that roadmap work served the chosen beachhead rather than all seven client stories equally. | P0.7, P9.12 |
| PROD-02 | Design-partner validation | Interview/runbook captures participant qualification, buyer/user split, repo/workflow context, current agent stack, trigger event, blocked workflow, privacy constraints, activation attempt, expected evidence, follow-up cadence, retained-use check, adoption/budget signal, disqualifiers, and rubric scores. | At least three primary-ICP target users or teams run a representative workflow, the minimum P0.8 evidence set is complete, and at least one credible paid/internal adoption signal is recorded. | P0.8, P9.7 |
| PROD-03 | Activation and onboarding | A clean local setup or clean fixture can run doctor/status, first useful `context_pack`, freshness check, audit report, suggested verification, product-evidence-safe output, and cleanup without internal Idiom knowledge. | Time-to-first-packet, time-to-trust-evidence, failure reason, docs gap, install/setup path, privacy/redaction status, and cleanup result are measured and release-gated. Thresholds are hypotheses until P7.7/P9.6 produce evidence. | P0.10, P7.7, P9.6 |
| PROD-04 | Retained-use evidence | Repeated sidecar use on a repo shows saved packets, audits, freshness, ranker feedback, integration transcript, evidence export, or verification evidence becoming more useful over time. | Retained use is measured with the P0.8 privacy-preserving record format and without hosted telemetry or raw prompt/file-body collection by default. | P0.8, P7.11, P9.7 |
| PROD-05 | Integration proof | Shell/CI, one external coding-agent workflow, and MCP stdio each exercise the same profile/schema/freshness/audit contract. | External callers do not need Idiom internals or prompt interpolation to use the sidecar. | P7.8, P8.6, P9.8 |
| PROD-06 | Security and privacy posture | Threat model, data-flow map, privacy/retention posture, protected-path behavior, telemetry minimization, side-effect policy, and protocol/security mappings are reviewable without claiming compliance. | Finance, healthcare, public-sector, enterprise, tooling-vendor, local-model, and open-source examples point to the same generic controls, and regulated rows remain proof-only until P7.9/P8.4/P9.9 evidence passes. | P0.9, P7.9, P8.4, P9.9 |
| PROD-07 | Competitive proof | Benchmarks compare raw search/read, generated baseline/current discovery reports, `context_pack`, and an Aider-style or agent-native exploration baseline where feasible. | A project go decision cannot depend only on Idiom-vs-Idiom measurements. | P4.6, P9.10 |
| PROD-08 | Client demo proof | Every client classification matrix row has a runnable transcript with command, expected output, required evidence fields, value claim, limitations, and go/no-go result. | Sales stories remain executable and honest about MVP versus final-state capability. | P7.10, P9.11 |
| PROD-09 | Packaging and support thesis | MVP/final packaging, pricing hypothesis, support boundary, admin needs, enterprise-surface limits, all seven client-classification stances, and explicit non-goals are documented as hypotheses. | Commercial scope is tied to P0.8/P0.10/P7.11/P9 evidence and does not pull enterprise/admin features ahead of substrate proof. | P0.11, P9.12 |
| PROD-10 | Product evidence export | Local product-success reports summarize activation, integration, benchmark, audit, retained-use, value-signal, blocker, and adoption-signal evidence without raw secrets or prompt/file bodies. | Product decisions stay evidence-backed while preserving local/privacy-first positioning; P7.11 exports aggregate P0.8 record IDs, scores, artifact hashes, and release-gate summaries only. | P7.11, P9.6, P9.7, P9.12 |

## Phase Ordering

| Phase | Theme | Depends On | Main Output |
| --- | --- | --- | --- |
| 0 | Baseline and doc hygiene | Current docs | Stable starting evidence and corrected current-state claims |
| 1 | Passive contract events | Phase 0 | Contract types and log events with no behavior enforcement change |
| 2 | Freshness and schema foundation | Phase 1 | Structured freshness plus exported tool schemas |
| 3 | Read-only capability resolver | Phase 2 | Small capability vocabulary routed through existing adapters |
| 4 | Localization benchmark and ranker trigger | Phase 2 | Measured context quality and one learning source |
| 5 | Auditable plan handoff | Phases 1, 2, 4 | Durable plan artifact and executor handoff checks |
| 6 | Local-model readiness | Phase 1 | Doctor, telemetry, queue, and context-budget readiness gates |
| 7 | Discovery sidecar MVP | Phases 2, 3, 4 | Stable CLI envelope and read-only external caller recipes |
| 8 | MCP stdio read-only server | Phase 7 | Profile-filtered MCP tools/resources for discovery |
| 9 | Release gates and pivot review | Phases 0-8 | Project go/no-go decision against measured criteria |

Phases 3, 4, and 6 can overlap after Phase 2. Phases 7 and 8 must not start
until the schema, freshness, and read-only permission model are stable. The
client-classification example tests and product-success gates are cross-cutting:
define the checklist, ICP, validation protocol, threat model, activation
metrics, and packaging hypothesis in Phase 0; add competitive proof in Phase 4;
add CLI onboarding, integration, demo, and product-evidence exports in Phase 7;
add MCP parity in Phase 8; gate completeness in Phase 9; and require the
canonical review prompt for every review handoff.

## Task And Review Tracking

Every implementation atom must have a paired review step. The implementation
checkbox and review checkbox are separate because "the code changed" and "the
change is complete, correct, aligned, and efficient" are different claims.
This file is the tracking document for that flow.

Delegation rules:

- The main orchestrator owns this implementation plan, task decomposition,
  dependency ordering, handoff prompts, status updates, and final go/no-go
  accounting.
- The main orchestrator should not perform implementation work outside updating
  this tracking plan. Substantive code, test, doc, schema, prompt, or telemetry
  changes must be delegated to an implementation subagent.
- Each task must be assigned to an implementation subagent before work starts.
- Each task must be reviewed by a separate review subagent after implementation.
- The review subagent must not be the same agent that implemented the task.
- If subagent delegation is unavailable, the task is blocked unless the project
  owner explicitly approves a one-off exception in this tracking doc.
- The main orchestrator may integrate subagent results only by updating this
  tracking doc, recording evidence, and delegating any required follow-up work.

Tracking rules:

- Mark `Implemented` only after the work is complete enough for review and the
  required local validation has either passed or has a recorded blocker.
- Mark `Reviewed` only after a dedicated review subagent, separate from the
  implementer, returns the required schema below with `Decision: GO`, or after
  this plan records a project-approved exception, no-go, or pivot.
- Record the implementation subagent and review subagent in the MVP Task Ledger
  task row before checking off either status. The ledger is the sole
  authoritative source for live implementer names, reviewer names, and checkbox
  status; Work Packages must refer to the ledger row instead of duplicating
  assignment names.
- If review returns `NO-GO`, omits a required schema section, lacks evidence for
  a required criterion, or finds required follow-up work, leave `Reviewed`
  unchecked and add or update a follow-up task unless this plan records a
  project-approved exception, no-go, or pivot.
- Do not mark a phase complete until every task in that phase has both
  checkboxes checked or an explicit project no-go/pivot decision recorded.
- Reviews should cite files, tests, logs, metrics, or docs. Unsupported opinion
  is not review evidence.
- Reviews must check client-classification coverage. For each affected matrix
  row, the reviewer should confirm that the work records a concrete coverage
  decision: preserved, updated, newly added, satisfied, skipped as
  not-applicable with a task-specific reason, or no-go/follow-up.
- Reviews must check product-success coverage. For each affected product gate,
  the reviewer should confirm that the work advances, preserves, or explicitly
  marks not-applicable the ICP, activation, retained-use, integration,
  security/privacy, competitive, demo, packaging/support, and local evidence
  requirements.
- Every review handoff must send the canonical reviewer prompt below. A review
  response that does not follow every required output-schema section is
  incomplete and cannot check the task's `Reviewed` box.

Review rubric:

- Completeness: Does the task satisfy the phase scope, acceptance criteria,
  docs, tests, and required telemetry/audit outputs? Are edge cases, degraded
  modes, and rollback paths covered?
- Correctness: Does the implementation enforce the intended contract without
  changing unrelated behavior? Are schemas, events, permissions, freshness, and
  path roles internally consistent?
- North-star alignment: Does the change strengthen repo memory, freshness,
  contracts, audit, or evals? Does it avoid Aider/Codex/Claude/OpenCode surface
  copying unless that surface directly supports the missing substrate?
- Bugs and regressions: Are there plausible failures in existing lanes, runner
  behavior, sidecar calls, stale-context handling, prompt authority, permission
  enforcement, or user-visible output?
- Performance: Does the change avoid avoidable broad reads/searches, repeated
  work, cache churn, excessive telemetry volume, slow tests, token bloat, local
  model overload, and sidecar latency regressions?
- Verification: Were the required validation commands run, and do they match the
  blast radius? If not, is the blocker concrete and acceptable?
- Client-classification coverage: Does the task preserve or improve the required
  matrix rows for finance, healthcare, public-sector, enterprise SaaS,
  tooling-vendor, local-model, and open-source users, including example fixture,
  required evidence fields, minimum privacy/security/performance expectations,
  and go/no-go result where the row is affected?
- Product-success coverage: Does the task preserve or improve the researched
  gates for beachhead ICP, activation, retained use, integration proof,
  security/privacy posture, competitive proof, executable demos,
  packaging/support, and local product-evidence export?
- Go/no-go decision: Does the review decision follow the evidence and reserve
  GO for schema-complete, evidence-backed acceptance?

Review output must use the schema below. It should lead with findings ordered by
severity, then list open questions, verification evidence, and the go/no-go
decision for the task.

### Canonical Reviewer Prompt

This prompt and required output schema are mandatory for every work-package
review. Send this prompt to every review subagent, fill the placeholders before
handoff, and reject review responses that omit any required schema section. The
reviewer must not be the implementation subagent.

```md
You are the dedicated review subagent for `<TASK_ID>: <TASK_NAME>`.

Context:

- North Star: AI coding agents are becoming common, but teams do not yet trust
  their context, decisions, or outputs. Idiom sells the missing substrate: repo
  memory, freshness, contracts, audit, and evals.
- Implementation evidence: <link or pasted summary>
- Changed files/modules: <paths>
- Verification/validation evidence supplied by implementer: <commands, logs,
  metrics, or blocker>
- Client-classification impact statement supplied by implementer: <matrix row
  IDs and coverage decisions for finance, healthcare, public-sector,
  enterprise SaaS, tooling-vendor, local-model, and open-source>
- Product-success impact statement supplied by implementer: <ICP, activation,
  retained use, integration proof, security/privacy, competitive proof, client
  demo proof, packaging/support, local product-evidence export statuses>

Review rules:

- Lead with findings ordered by severity: Blocker, High, Medium, Low.
- Cite concrete evidence: files, tests, logs, metrics, schema output, audit
  events, benchmark output, or docs.
- Do not accept unsupported opinion as evidence.
- Do not make code changes. If fixes are needed, propose follow-up tasks.
- Do not mark a criterion pass unless the implementation includes evidence.
- Treat "not applicable" as valid only when the reason is specific and tied to
  the task scope.
- Return every section in the required output schema. Write `None` only when the
  section is genuinely empty and evidence supports that.
- Use `Decision: GO` only when every required criterion passes or has an
  evidence-backed task-specific not-applicable reason and no required follow-up
  remains. Use `Decision: NO-GO` for missing evidence, failed criteria, required
  follow-up work, or a recommended pivot/exception.
- Check that vertical/client behavior is implemented through generic primitives
  such as contracts, profiles, sidecar envelopes, freshness, audit, readiness,
  verification, schema, and policy overlays. Flag hardcoded vertical branches.
- Inspect serialized/live output shape, not only docs or hand-redacted
  artifacts. For audit, telemetry, sidecar, and product-evidence outputs,
  search for forbidden field names and representative forbidden values such as
  `user_prompt`, raw transcript or file body text, secrets, and private paths.
- Reconcile summary booleans and status labels with detail fields. A pass such
  as `all_clear`, `ready`, or `GO` is invalid when underlying arrays, warnings,
  findings, stale references, open obligations, invalid events, correlation
  mismatches, or required follow-up still report work or risk.
- Require at least one negative or contradictory fixture for contract, audit,
  privacy, gate, and schema changes. Happy-path evidence is insufficient when a
  malformed event, stale gate, forbidden field, or protected path could silently
  pass.
- Verify trust-boundary decoders and aggregators validate inputs and surface
  invalid records as warnings or findings. Malformed records must not be counted
  as good coverage.

Required criteria:

1. Completeness: task scope, acceptance criteria, docs, tests, telemetry/audit
   outputs, edge cases, degraded modes, rollback or disable path.
2. Correctness: intended contracts are enforced without unrelated behavior
   changes; schemas, events, permissions, freshness, path roles, and state
   transitions are internally consistent.
3. North-star alignment: change strengthens repo memory, freshness, contracts,
   audit, evals, sidecar substrate, or local readiness; it does not drift toward
   generic agent-chat parity or copy Aider/Codex/Claude/OpenCode surfaces unless
   that directly supports the substrate.
4. Client-classification coverage: affected finance, healthcare, public-sector,
   enterprise SaaS, tooling-vendor, local-model, and open-source matrix rows
   record whether the example was preserved, updated, newly added, satisfied,
   skipped as not-applicable with a task-specific reason, or no-go/follow-up.
5. Product-success coverage: affected ICP, activation, retained-use,
   integration, security/privacy, competitive, demo, packaging/support, and
   local evidence gates are advanced, preserved, or explicitly not-applicable.
6. Bugs and regressions: check existing lanes, runner behavior, sidecar calls,
   MCP behavior, stale-context handling, prompt authority, permission
   enforcement, user-visible output, and failure/degraded paths.
7. Performance: check avoidable broad reads/searches, repeated work, cache
   churn, telemetry volume, slow tests, token bloat, local-model overload,
   sidecar latency, and benchmark impact.
8. Security and privacy: check protected paths, redaction, telemetry retention,
   no raw prompt/file-body leakage, default-deny effects, shell/network/open-world
   exposure, and approval semantics. Serialized outputs must enforce redaction
   by code; manual artifact redaction is not sufficient evidence.
9. Observability and auditability: check contract IDs, caller attribution,
   trace IDs, audit event IDs, freshness/provenance/confidence, reproduce
   commands, owners, and finding status. Pass statuses must agree with detail
   arrays and warnings; invalid events, stale references, open obligations, and
   correlation mismatches must surface as findings or explicit follow-up.
10. Verification and validation: verify required commands were run for the blast
   radius, or that blockers are concrete and acceptable. Include focused
   negative fixtures and JSON field-level assertions for forbidden fields,
   malformed input, contradictory gate state, and fail-closed audit behavior
   when those risks are in scope.
11. Operability and maintainability: check rollback/disable path, docs, naming,
    ownership boundaries, test locality, and absence of needless abstraction.
12. Go/no-go decision: make the final decision from the evidence above; do not
    return GO when required evidence or required follow-up is missing.

Required output schema:

Decision: GO | NO-GO

Findings:

- <severity>: <finding with file/path/test/log evidence>

Criteria checklist:

| Criterion | Status | Evidence | Required Follow-up |
| --- | --- | --- | --- |
| Completeness | pass/fail/na | ... | ... |
| Correctness | pass/fail/na | ... | ... |
| North-star alignment | pass/fail/na | ... | ... |
| Client-classification coverage | pass/fail/na | ... | ... |
| Product-success coverage | pass/fail/na | ... | ... |
| Bugs and regressions | pass/fail/na | ... | ... |
| Performance | pass/fail/na | ... | ... |
| Security and privacy | pass/fail/na | ... | ... |
| Observability and auditability | pass/fail/na | ... | ... |
| Verification and validation | pass/fail/na | ... | ... |
| Operability and maintainability | pass/fail/na | ... | ... |
| Go/no-go decision | pass/fail/na | ... | ... |

Client-classification example tests:

| Matrix Row | Coverage Decision | Example Test Evidence | Required Follow-up |
| --- | --- | --- | --- |
| `finance` | preserved/updated/added/satisfied/skipped-na/no-go | ... | ... |
| `healthcare` | preserved/updated/added/satisfied/skipped-na/no-go | ... | ... |
| `public-sector` | preserved/updated/added/satisfied/skipped-na/no-go | ... | ... |
| `enterprise-saas` | preserved/updated/added/satisfied/skipped-na/no-go | ... | ... |
| `tooling-vendor` | preserved/updated/added/satisfied/skipped-na/no-go | ... | ... |
| `local-model` | preserved/updated/added/satisfied/skipped-na/no-go | ... | ... |
| `open-source` | preserved/updated/added/satisfied/skipped-na/no-go | ... | ... |

Product-success requirements:

| Product Gate | Status | Evidence | Required Follow-up |
| --- | --- | --- | --- |
| ICP | pass/fail/na | ... | ... |
| Activation | pass/fail/na | ... | ... |
| Retained use | pass/fail/na | ... | ... |
| Integration proof | pass/fail/na | ... | ... |
| Security/privacy | pass/fail/na | ... | ... |
| Competitive proof | pass/fail/na | ... | ... |
| Client demo proof | pass/fail/na | ... | ... |
| Packaging/support | pass/fail/na | ... | ... |
| Local product-evidence export | pass/fail/na | ... | ... |

Verification and validation evidence:

- Commands run:
- Commands not run and why:
- Metrics or audit output:

Open questions:

- ...

Required follow-up tasks:

- <new task id suggestion or existing task id>: <scope and owner recommendation>

Review signoff:

- I am not the implementation subagent.
- I reviewed the implementation against every required criterion above.
- I recommend marking `Reviewed` only if the decision is GO.
- I understand that NO-GO, missing schema sections, or required follow-up work
  cannot mark the task `Reviewed` unless this plan records a project-approved
  exception, no-go, or pivot.
```

## MVP Task Ledger

| ID | Task | Depends On | Implementer | Implemented | Reviewer | Reviewed |
| --- | --- | --- | --- | --- | --- | --- |
| P0.1 | Replace stale current-state prose with dated examples or generated commands | Current docs | Confucius | - [x] | Kierkegaard | - [x] |
| P0.2 | Capture and store the baseline bundle for context, tool surface, behavior, and suite validation | P0.1 | Hume | - [x] | Poincare | - [x] |
| P0.12 | Make the P0.2 baseline bundle durable in version control without staging unrelated dirty files | P0.2 | Nash | - [x] | Dewey | - [x] |
| P0.13 | Refresh the staged implementation-plan tracking rows for P0.12 follow-up without staging unrelated paths | P0.12 | Nash | - [x] | Dewey | - [x] |
| P0.3 | Fix stale external links and keep source labels clear for preprints/project claims | P0.1 | McClintock | - [x] | Fermat | - [x] |
| P0.14 | Replace residual live mutable source-label wording from the P0.3 review with checked-date or release-evidence phrasing | P0.3 | Goodall | - [x] | Fermat | - [x] |
| P0.4 | Expand every ledger atom into work-package entries with user story, control requirements, acceptance, metric, rollback, implementer, and reviewer | P0.1-P0.3 | Linnaeus | - [x] | Kepler | - [x] |
| P0.15 | Sync the P0.4 work-package subagent fields with the ledger reviewer assignment and rerun consistency checks | P0.4 | Boole | - [x] | Kepler | - [x] |
| P0.16 | Make the ledger the authoritative source for live implementer/reviewer assignment to prevent duplicate tracking drift | P0.4, P0.15 | James | - [x] | Volta | - [x] |
| P0.17 | Align the Work Package Template status placeholders with the ledger-only assignment and status authority | P0.16 | Harvey | - [x] | Volta | - [x] |
| P0.5 | Define the client-classification example-test matrix as a reusable acceptance checklist for all work packages | P0.4 | Russell | - [x] | Tesla | - [x] |
| P0.6 | Install the canonical reviewer prompt and review-output schema as mandatory fields for every work package review | P0.4, P0.5 | Godel | - [x] | Gauss | - [x] |
| P0.7 | Choose and document the MVP beachhead ICP, adoption wedge, paid buyer, non-targets, and final-state expansion path | P0.1-P0.6 | Cicero | - [x] | Leibniz | - [x] |
| P0.8 | Create the design-partner validation protocol, interview/runbook template, value-signal rubric, and retained-use evidence format | P0.7 | Anscombe | - [x] | Epicurus | - [x] |
| P0.9 | Produce a product threat model, data-flow map, privacy/retention posture, and NIST/OWASP/MCP/HHS/FINRA control mapping | P0.7 | Copernicus | - [x] | Huygens | - [x] |
| P0.18 | Align the P0.9 OWASP control mapping with the cited current OWASP GenAI Top 10 source | P0.9 | Meitner | - [x] | Mencius | - [x] |
| P0.10 | Define activation and onboarding metrics for clean install, first `context_pack`, freshness check, audit report, and cleanup path | P0.7 | Mendel | - [x] | Herschel | - [x] |
| P0.11 | Define MVP and final-state packaging, pricing, support, admin, and enterprise-surface hypotheses with explicit non-goals | P0.7 | Peirce | - [x] | Plato | - [x] |
| P1.1 | Introduce the typed autonomy contract shape for lanes, phases, and sidecar calls | P0 | Wegener | - [x] | Popper + P1.7 follow-up | - [x] |
| P1.7 | Fix autonomy contract scope aliasing and add stable-ID regression coverage | P1.1 | Carver | - [x] | Beauvoir | - [x] |
| P1.2 | Emit passive contract lifecycle, policy, and obligation events | P1.1, P1.7 | Feynman | - [x] | Pascal | - [x] |
| P1.3 | Join contract events to audit output without changing lane behavior | P1.2 | Kant | - [x] | Ampere + P1.8 follow-up | - [x] |
| P1.8 | Align `harness_audit` schema limits and assert no-event contract coverage omission | P1.3 | Aquinas | - [x] | Newton | - [x] |
| P1.4 | Add a named invariant map and shared path-role resolver for required, evidence, protected, forbidden, and side-effect paths | P1.1, P1.7 | Halley | - [x] | Raman | - [x] |
| P1.5 | Migrate existing write, verification, completion, loop, and budget gates to contract obligations and events | P1.2, P1.4 | Lovelace | - [x] | Hypatia + P1.9/P1.10 follow-ups | - [x] |
| P1.9 | Close P1.5 gate-obligation lifecycle and verify post-readiness behavior preservation | P1.5 | Hegel | - [x] | Averroes + P1.10 follow-up | - [x] |
| P1.10 | Scope write-gate obligation closure to changed required paths and preserve partial-write unresolved evidence | P1.9 | Noether | - [x] | Planck | - [x] |
| P1.6 | Normalize side-effect classes, protected-path decisions, and approval outcomes into contract policy events | P1.4, P1.9, P1.10 | Chandrasekhar | - [x] | Arendt + P1.11 follow-up | - [x] |
| P1.11 | Fix P1.6 forbidden-path normalization and conservative shell side-effect classification | P1.6 | Dalton | - [x] | Locke | - [x] |
| P2.1 | Export tool schemas from the existing manifest source of truth | P1 | Ohm | - [x] | Laplace | - [x] |
| P2.2 | Add structured freshness and confidence fields to graph-backed results | P2.1 | Franklin | - [x] | Darwin + P2.5/P2.6/P2.7/P2.8 follow-ups | - [x] |
| P2.5 | Replace graph-backed prescriptive remediation strings with neutral structured reason codes | P2.2 | Helmholtz | - [x] | Descartes + P2.6/P2.7/P2.8 follow-ups | - [x] |
| P2.6 | Support path-only `context_impact` requests with neutral graph-backed validation output | P2.5 | Mill | - [x] | Einstein + P2.8 follow-up | - [x] |
| P2.8 | Restore broad-suite validation by aligning `idiom-perf-eval` worst-offender output with the current perf schema | P2.6 | Turing | - [x] | Ramanujan | - [x] |
| P2.7 | Clear and regenerate legacy graph views so cached outputs honor the P2.5 evidence-only contract | P2.5 | Kuhn | - [x] | Sagan | - [x] |
| P2.3 | Add manifest/schema/suggestion drift checks and stale-evidence blockers | P2.1, P2.2, P2.5, P2.6, P2.7, P2.8 | Lagrange | - [x] | Faraday + P2.9 follow-up | - [x] |
| P2.9 | Fix P2.3 stale-evidence correctness gaps for new files, zoom policy propagation, and report-only telemetry | P2.3 | Curie | - [x] | Euclid | - [x] |
| P2.4 | Define graph view result contracts, ranker reasons, unresolved gaps, and cache invalidation for ranker weights and runtime traces | P2.2, P2.5, P2.6, P2.7, P2.8, P2.9 | Hilbert | - [x] | Parfit + P2.10/P2.11/P2.12 follow-ups | - [x] |
| P2.10 | Bound runtime-trace cache fingerprinting and separate invalidation source attribution | P2.4 | Dirac | - [x] | Jason + P2.11/P2.12 follow-ups | - [x] |
| P2.11 | Update graph-view cache documentation for workspace, ranker, and runtime trace fingerprints | P2.10 | Bohr | - [x] | Ptolemy + P2.12 follow-up | - [x] |
| P2.12 | Complete graph-view fingerprint privacy docs for secrets and protected contents | P2.11 | Archimedes | - [x] | Boyle | - [x] |
| P2.13 | Purge current pre-production legacy graph-view rows/artifacts and regenerate replacement view cache | P2.7, P2.12 | Socrates the 2nd | - [x] | Avicenna the 2nd + P2.14/P2.15 follow-ups | - [x] |
| P2.14 | Re-purge graph-view cache after active source edits settle and clear legacy graph-backed discovery artifacts | P2.13, P4.12 | Peirce the 2nd | - [x] | Mencius the 2nd + P2.15 follow-up | - [x] |
| P2.15 | Finish graph-view purge with terminal fresh-status and current-index SQL checks | P2.14 | Pascal the 2nd | - [x] | Carson the 2nd | - [x] |
| P3.1 | Define the initial read-only capability vocabulary and resolver registry | P2 | Carson | - [x] | Bacon + P3.6 follow-up | - [x] |
| P3.6 | Fix P3.1 source-sanity capability side-effect classification against schema truth | P3.1 | Sartre | - [x] | Pauli | - [x] |
| P3.2 | Route at least three read-only capabilities through existing adapters | P3.1, P3.6 | Banach | - [x] | Aristotle + P3.7 follow-up | - [x] |
| P3.7 | Remove broad workspace language-detection scans from read-only capability routing | P3.2 | Nietzsche | - [x] | Socrates | - [x] |
| P3.3 | Migrate runtime briefs and recovery text toward capability language | P3.2 | Hooke | - [x] | Euler + P3.8/P3.9 follow-ups | - [x] |
| P3.8 | Remove remaining prompt-surface tool prescriptions from P3.3 runtime and lane text | P3.3 | Pasteur | - [x] | Poincare the 2nd + P3.9 follow-up | - [x] |
| P3.9 | Remove raw discovery-tool strategy menus from live profile prompt surfaces | P3.8 | Hooke the 2nd | - [x] | Banach the 2nd | - [x] |
| P3.4 | Add capability status, precision, fallback, absence-confidence, and unmapped-adapter reporting | P3.1, P3.2, P3.3, P3.8, P3.9 | Noether the 2nd | - [x] | Locke the 2nd + P3.10 follow-up | - [x] |
| P3.10 | Add capability status public-suppression rollback path and fix audit field count | P3.4 | Pauli the 2nd | - [x] | Epicurus the 2nd | - [x] |
| P3.5 | Expand the first routing slice to definition, references, file outline, symbol lookup, related files, and source sanity | P3.2, P3.4, P3.10 | Hume the 2nd | - [x] | Huygens the 2nd | - [x] |
| P4.1 | Build a localization eval corpus from recent implementation logs | P2 | Hilbert the 2nd | - [x] | Harvey the 2nd | - [x] |
| P4.2 | Add benchmark reporting for top-k edit-path coverage and exploration waste | P4.1 | Ptolemy the 2nd | - [x] | Arendt the 2nd | - [x] |
| P4.3 | Wire one verified ranker-learning trigger with inspectable learning logs | P4.2 | Ampere the 2nd | - [x] | Turing the 2nd + P4.7-P4.12 follow-ups | - [x] |
| P4.7 | Sanitize manual ranker-learning notes before shared learning-log persistence | P4.3 | Euler the 2nd | - [x] | Lorentz the 2nd + P4.8-P4.12 follow-ups | - [x] |
| P4.8 | Sanitize learning-log timestamps and all shared persistence freeform fields | P4.7 | Beauvoir the 2nd | - [x] | Averroes the 2nd + P4.9-P4.12 follow-ups | - [x] |
| P4.9 | Replace unknown learning-log tokens with strict enums, generated IDs, or bounded hashes | P4.8 | Hegel the 2nd | - [x] | Heisenberg the 2nd + P4.10-P4.12 follow-ups | - [x] |
| P4.10 | Prevent secret-shaped path and weight keys from persisting in learning logs | P4.9 | Zeno the 2nd | - [x] | Aquinas the 2nd + P4.11/P4.12 follow-ups | - [x] |
| P4.11 | Reject or scrub secret-shaped learning-log path segments and filename stems | P4.10 | Bernoulli the 2nd | - [x] | Singer the 2nd + P4.12 follow-up | - [x] |
| P4.12 | Block hyphenated sensitive key compounds in learning-log path and weight channels | P4.11 | Mendel the 2nd | - [x] | Anscombe the 2nd | - [x] |
| P4.4 | Add unified localization report covering outcome, trajectory, cache/freshness, ranker, verification, and audit metrics | P4.2, P2.15 | Boole the 2nd | - [x] | Dewey the 2nd + P4.13/P4.14 follow-ups | - [x] |
| P4.13 | Add product-success gate statuses and coherent client classification summaries to the unified localization report | P4.4 | Galileo the 2nd | - [x] | Franklin the 2nd + P4.14 follow-up | - [x] |
| P4.14 | Make product-success gates account for degraded cache evidence in localization reports | P4.13 | Descartes the 2nd | - [x] | Euclid the 2nd | - [x] |
| P4.5 | Add audit-watch ownership, regression-promotion workflow, and before/after replay closure discipline | P4.12, P4.14 | Planck the 2nd | - [x] | Sartre the 2nd + P4.15-P4.17 follow-ups | - [x] |
| P4.15 | Sanitize audit-watch trigger, session, and reproducer evidence before local export | P4.5 | Ramanujan the 2nd | - [x] | Ohm the 2nd + P4.16/P4.17 follow-ups | - [x] |
| P4.16 | Block customer-like and raw-transcript audit-watch evidence across trigger, session, reason, and reproducer fields | P4.15 | Archimedes the 2nd | - [x] | Kant the 2nd + P4.17 follow-up | - [x] |
| P4.17 | Refresh terminal context index and regenerated graph views after audit-watch privacy changes | P4.16 | Gibbs the 2nd | - [x] | Lagrange the 2nd | - [x] |
| P4.6 | Add competitive benchmark conditions for Aider-style repo-map exploration, external-agent native exploration, or documented infeasibility | P4.1-P4.4, P4.12, P4.14, P4.17 | Mill the 2nd | - [x] | Aristotle the 2nd + P4.18 follow-up | - [x] |
| P4.18 | Constrain competitive-condition client classifications to approved bounded classes | P4.6 | Einstein the 2nd | - [x] | Rawls the 2nd | - [x] |
| P5.1 | Define and parse the durable plan artifact schema | P1, P2, P4 | Schrodinger the 2nd | - [x] | Maxwell the 2nd + P5.5/P5.6 follow-ups | - [x] |
| P5.5 | Sanitize plan edit metadata and refresh graph views after durable schema changes | P5.1 | Goodall the 2nd | - [x] | Wegener the 2nd + P5.6 follow-up | - [x] |
| P5.6 | Broaden plan edit-purpose privacy boundary for standalone token, customer, and private-path metadata | P5.5 | Feynman the 2nd | - [x] | Laplace the 2nd | - [x] |
| P5.2 | Harden `submit_plan`, plan audit, and replay compare around packet and scope evidence | P5.6 | Hubble the 2nd | - [x] | Sagan the 2nd + P5.7/P5.8 follow-ups | - [x] |
| P5.7 | Block handoff-ready plan artifacts when required approval is unsatisfied | P5.2 | Godel the 2nd | - [x] | Jason the 2nd + P5.8 follow-up | - [x] |
| P5.8 | Prevent rejected approval-gated plans from persisting spilled edit assets | P5.7 | Bohr the 2nd | - [x] | Kepler the 2nd | - [x] |
| P5.3 | Emit executor handoff events for inherited, missing, stale, and overridden packets | P5.8 | Popper the 2nd | - [x] | Halley the 2nd + P5.9 follow-up | - [x] |
| P5.9 | Classify stale graph-view executor packets without legacy view-file fallback | P5.3 | Cicero the 2nd | - [x] | Erdos the 2nd | - [x] |
| P5.10 | Bound `context_status` stale samples and unscoped refresh attempts | P5.9 | Leibniz the 2nd | - [x] | Kierkegaard the 2nd | - [x] |
| P5.4 | Add approval scope IDs, handoff IDs, partial approval semantics, and eval assertions for plan-to-execution drift | P5.2, P5.10 | Gauss the 2nd | - [x] | Tesla the 2nd + P5.11/P5.12 follow-ups | - [x] |
| P5.11 | Refresh terminal graph-view cache after P5.4 drift assertions | P5.4 | Pasteur the 2nd | - [x] | Faraday the 2nd + P5.12 follow-up | - [x] |
| P5.12 | Final post-validation context refresh for P5.4 plan-execute edits | P5.11 | Newton the 2nd | - [x] | Meitner the 2nd | - [x] |
| P5.13 | Constrain plan-execute source-satisfied clean-noop detection to explicit decisions | P5.4, P5.12 | Fermat the 2nd | - [x] | McClintock the 2nd | - [x] |
| P5.14 | Align plan-execute new-file body guidance and preflight enforcement | P5.13 | Helmholtz the 2nd | - [x] | Raman the 2nd + P5.15 follow-up | - [x] |
| P5.15 | Remove plan-execute host auto-submit and tighten plan-execute brief detection | P5.14 | Russell the 2nd | - [x] | Dalton the 2nd | - [x] |
| P5.16 | Reject atom/refine shorthand paths that could authorize unintended root writes | P5.15 | Boyle the 2nd | - [x] | Kuhn the 2nd + P5.17 follow-up | - [x] |
| P5.17 | Report ambiguous atom shorthand paths with coherent invalid-scope reasons | P5.16 | Confucius the 2nd | - [x] | Lovelace the 2nd | - [x] |
| P5.18 | Reject no-op `submit_plan` edits before artifact persistence | P5.13, P5.15 | Linnaeus the 3rd | - [x] | Parfit the 3rd | - [x] |
| P5.19 | Resolve `context_status` default refresh-budget drift | P5.10, P5.12 | Poincare the 3rd | - [x] | Mendel the 3rd + P5.20 follow-up | - [x] |
| P5.20 | Restore default `context_status` stale-file budget after P5.19 review | P5.19 | Leibniz the 3rd | - [x] | Erdos the 3rd | - [x] |
| P5.21 | Re-apply `context_status` default stale-file budget after concurrent dirty regression | P5.20 | Boyle the 3rd | - [x] | Archimedes the 3rd | - [x] |
| P6.1 | Extend `idiom doctor` with readiness profiles | P1, P5.12 | Herschel the 2nd | - [x] | Volta the 2nd | - [x] |
| P6.2 | Add backend, queue, context budget, route classifier, implement-session closeout, and degraded-mode telemetry | P6.1, P5.13 | Carver the 2nd | - [x] | James the 2nd + P6.6 follow-up | - [x] |
| P6.6 | Make implement queue telemetry unavailable states and doctor command attribution explicit | P6.2 | Parfit the 2nd | - [x] | Bacon the 2nd | - [x] |
| P6.3 | Define soft and hard readiness gates by profile | P6.6, P5.15 | Curie the 2nd | - [x] | Hypatia the 2nd | - [x] |
| P6.4 | Add lane-start role, backend, queue, classifier, and context telemetry parity tests | P6.2, P6.3 | Copernicus the 2nd | - [x] | Dirac the 2nd | - [x] |
| P6.5 | Add local-operations smoke, capacity, and replay benchmark harness | P6.3, P6.4, P5.17 | Darwin the 2nd | - [x] | Nash the 2nd + P6.7 follow-up | - [x] |
| P6.7 | Sanitize local-ops replay export and normalize invalid-profile exits | P6.5 | Nietzsche the 2nd | - [x] | Linnaeus the 2nd | - [x] |
| P7.1 | Ship the stable structured CLI envelope for `idiom tool` results | P2, P3, P4, P6.7 | Bernoulli the 3rd + Singer the 3rd + Darwin the 3rd | - [x] | Lagrange the 3rd | - [x] |
| P7.2 | Implement read-only `--profile discovery` filtering | P7.1 | Kierkegaard the 3rd | - [x] | Russell the 3rd + P7.12/Ptolemy the 3rd follow-up | - [x] |
| P7.12 | Block refresh-capable discovery-profile requests before execution | P7.2 | Cicero the 3rd | - [x] | Ptolemy the 3rd | - [x] |
| P7.3 | Add external caller recipes and at least one sidecar smoke transcript | P7.2 | Tesla the 3rd | - [x] | Aristotle the 3rd + P7.13/Epicurus the 3rd follow-up | - [x] |
| P7.13 | Add executable validation for sidecar recipe commands | P7.3 | Kuhn the 3rd | - [x] | Epicurus the 3rd | - [x] |
| P7.4 | Add sidecar v1 API compatibility, caller metadata, error-class, stale-freshness, backend-fallback, and no-strategy-leakage golden tests | P7.1, P7.2 | Schrodinger the 3rd | - [x] | Ampere the 3rd + P7.14/Newton the 3rd follow-up | - [x] |
| P7.14 | Close P7.4 strategy-leakage and caller-metadata privacy gaps | P7.4 | Carson the 3rd | - [x] | Newton the 3rd | - [x] |
| P7.5 | Add discovery-profile containment, protected-path, redaction, permission-denial, and external side-effect tests | P7.2, P1.6 | unassigned subagent | - [ ] | unassigned review subagent | - [ ] |
| P7.6 | Add CLI-side example tests for finance, healthcare, public-sector, enterprise SaaS, tooling-vendor, local-model, and open-source classification scenarios | P7.1-P7.5, P6.5 | unassigned subagent | - [ ] | unassigned review subagent | - [ ] |
| P7.7 | Add install, quickstart, time-to-first-value, and clean-uninstall smoke coverage for CLI-only local use | P7.1-P7.3, P0.10 | unassigned subagent | - [ ] | unassigned review subagent | - [ ] |
| P7.8 | Add three integration proof paths: shell/CI script, one external coding-agent workflow, and a placeholder handoff to MCP stdio | P7.1-P7.5 | unassigned subagent | - [ ] | unassigned review subagent | - [ ] |
| P7.9 | Add security/privacy posture artifacts and automated checks for protected paths, redaction, retention, telemetry minimization, and side-effect policy | P7.2, P7.5, P0.9 | unassigned subagent | - [ ] | unassigned review subagent | - [ ] |
| P7.10 | Add runnable client demo transcripts for every client classification with commands, expected output, value claim, evidence, and limitations | P7.3, P7.6, P0.8 | unassigned subagent | - [ ] | unassigned review subagent | - [ ] |
| P7.11 | Add local product-evidence export for activation, retained use, integration, benchmark, audit, and learning signals without raw prompt or file bodies | P7.1-P7.6, P0.8, P0.10 | unassigned subagent | - [ ] | unassigned review subagent | - [ ] |
| P8.1 | Implement `idiom mcp serve --stdio --profile discovery` from the same schemas | P7 | unassigned subagent | - [ ] | unassigned review subagent | - [ ] |
| P8.2 | Add MCP resources, root clamping, and protocol/error tests | P8.1 | unassigned subagent | - [ ] | unassigned review subagent | - [ ] |
| P8.3 | Add MCP client smoke tests and telemetry/caller attribution parity with CLI | P8.1, P8.2 | unassigned subagent | - [ ] | unassigned review subagent | - [ ] |
| P8.4 | Add MCP resource redaction, protected-path containment, and stale-freshness parity tests | P8.2, P7.5 | unassigned subagent | - [ ] | unassigned review subagent | - [ ] |
| P8.5 | Add MCP parity coverage for any classification example that depends on resources, caller metadata, or redaction semantics | P8.1-P8.4, P7.6 | unassigned subagent | - [ ] | unassigned review subagent | - [ ] |
| P8.6 | Complete MCP stdio integration proof with protocol, caller metadata, roots, resources, redaction, and product-evidence export parity | P8.1-P8.5, P7.8, P7.11 | unassigned subagent | - [ ] | unassigned review subagent | - [ ] |
| P9.1 | Produce release evidence bundle from baseline/current metrics, tests, audit, benchmark, and sidecar smoke | P0-P8 | unassigned subagent | - [ ] | unassigned review subagent | - [ ] |
| P9.2 | Record project go/no-go/pivot decision against measured criteria | P9.1 | unassigned subagent | - [ ] | unassigned review subagent | - [ ] |
| P9.3 | Gate release on no unresolved high contract/security regressions and owned high/medium audit findings with evidence and reproduce commands | P9.1 | unassigned subagent | - [ ] | unassigned review subagent | - [ ] |
| P9.4 | Gate release on complete client-classification example-test coverage and reviewer signoff that every example maps to generic architecture primitives | P7.6, P8.5, P9.1 | unassigned subagent | - [ ] | unassigned review subagent | - [ ] |
| P9.5 | Gate release on review-protocol compliance for every implemented ledger task | P0.6, P9.1-P9.4 | unassigned subagent | - [ ] | unassigned review subagent | - [ ] |
| P9.6 | Gate release on activation/onboarding evidence, including measured install-to-first-value and cleanup path | P7.7, P7.11, P9.1 | unassigned subagent | - [ ] | unassigned review subagent | - [ ] |
| P9.7 | Gate release on design-partner validation, retained-use evidence, and at least one credible paid or internal-platform adoption signal | P0.8, P7.10, P7.11, P9.1 | unassigned subagent | - [ ] | unassigned review subagent | - [ ] |
| P9.8 | Gate release on three integration proof paths: shell/CI, external coding-agent workflow, and MCP stdio | P7.8, P8.6, P9.1 | unassigned subagent | - [ ] | unassigned review subagent | - [ ] |
| P9.9 | Gate release on reviewed security/privacy posture, threat model, data-flow map, and control mapping | P0.9, P7.9, P8.4, P9.1 | unassigned subagent | - [ ] | unassigned review subagent | - [ ] |
| P9.10 | Gate release on competitive benchmark coverage or documented infeasibility for each external baseline | P4.6, P9.1 | unassigned subagent | - [ ] | unassigned review subagent | - [ ] |
| P9.11 | Gate release on runnable client demo transcripts and honest MVP/final-state limitations for every client classification | P7.10, P8.5, P9.1 | unassigned subagent | - [ ] | unassigned review subagent | - [ ] |
| P9.12 | Gate release on packaging, pricing, support, admin, and ICP thesis with explicit continue/pivot decision | P0.7, P0.11, P9.1-P9.11 | unassigned subagent | - [ ] | unassigned review subagent | - [ ] |

## Work Packages

This generated-style inventory expands each ledger row into one compact
work-package entry. The MVP Task Ledger remains the source of dependency order,
implementation status, review status, and assigned agent names; do not update
checkboxes or live assignment names here. The Subagents field is required in
each Work Package row, but it must point to the MVP Task Ledger row rather than
repeat implementer or reviewer names. Each row must be copied into the
implementation handoff and review handoff with the current ledger status,
canonical reviewer prompt, and required output schema. Review output, validation
evidence, client-classification coverage, product-success coverage, and signoff
are review evidence, not a second live status tracker.

Client-classification impact uses the matrix row IDs: `finance`, `healthcare`,
`public-sector`, `enterprise-saas`, `tooling-vendor`, `local-model`, and
`open-source`. Every implementation and review must record whether each affected
row was preserved, updated, newly added, satisfied, skipped as not-applicable
with a task-specific reason, or converted into a no-go/follow-up. These coverage
decisions are review evidence only; they must not duplicate live implementer,
reviewer, or checkbox status from the MVP Task Ledger. Product-success impact
uses these gates: ICP, activation, retained use, integration proof,
security/privacy, competitive proof, client demo proof, packaging/support, and
local product-evidence export.

### Phase 0 Work Packages

| ID and name | Story | Control requirements | Acceptance and validation | Metric or audit signal | Rollback or disable path | Subagents | Client-classification and product-success impact |
| --- | --- | --- | --- | --- | --- | --- | --- |
| P0.1: Replace stale current-state prose with dated examples or generated commands | As a plan reader, I need mutable claims tied to dated examples or regenerable commands. | Current-state claims must be dated or command-backed, with no evergreen mutable counts. | Affected prose names dates or generation commands; validation: docs diff and link/path check when links change. | Stale-claim count and source-freshness findings decline. | Revert wording to the last reviewed evidence-backed text. | Implementer/reviewer: see MVP Task Ledger row. | Class: all categories get clearer evidence anchors; Product: improves ICP, security/privacy, competitive proof, and local evidence. |
| P0.2: Capture and store the baseline bundle for context, tool surface, behavior, and suite validation | As a reviewer, I need a baseline bundle before judging later movement. | Baseline commands must be local, parseable, and stored without raw secrets or prompt/file bodies. | Bundle exists at the recorded path and commands are reproducible; validation: phase baseline commands. | Baseline artifact count, audit output, context status, and representative suite result become comparable. | Regenerate the bundle from recorded commands and replace only the baseline artifacts. | Implementer/reviewer: see MVP Task Ledger row. | Class: all categories get a common baseline; Product: improves competitive proof, security/privacy, and local evidence. |
| P0.12: Make the P0.2 baseline bundle durable in version control without staging unrelated dirty files | As a maintainer, I need baseline evidence durable without disturbing other worktree changes. | Only baseline evidence paths may be touched; staged and unrelated dirty files remain intact. | Baseline files are durable in version control state and unrelated paths are unchanged; validation: git status and docs diff. | Baseline durability and dirty-file isolation are visible in status output. | Remove or regenerate only the baseline evidence paths. | Implementer/reviewer: see MVP Task Ledger row. | Class: all categories benefit from durable evidence; Product: strengthens local evidence and competitive proof. |
| P0.13: Refresh the staged implementation-plan tracking rows for P0.12 follow-up without staging unrelated paths | As the orchestrator, I need tracking rows to reflect reviewed follow-up work without staging unrelated files. | Tracking updates must match reviewed evidence and preserve unrelated staging. | P0.12 follow-up status is represented in the ledger and no unrelated paths are staged; validation: git status and docs diff. | Ledger/status consistency improves with no unrelated staging churn. | Revert only the tracking-row update if review evidence is invalid. | Implementer/reviewer: see MVP Task Ledger row. | Class: all categories preserve review traceability; Product: improves local evidence and review-protocol readiness. |
| P0.3: Fix stale external links and keep source labels clear for preprints/project claims | As a release reader, I need external anchors to be reachable and honestly labeled. | Links must be checked, mutable sources dated, and preprint/project claims labeled without overstatement. | Broken or ambiguous source labels are corrected; validation: docs diff and link/source check. | Stale-link count and ambiguous-source findings decline. | Revert to the last checked source label or replace with a release-evidence placeholder. | Implementer/reviewer: see MVP Task Ledger row. | Class: finance, healthcare, public-sector, and tooling-vendor claims become safer; Product: improves security/privacy, competitive proof, and client demo proof. |
| P0.14: Replace residual live mutable source-label wording from the P0.3 review with checked-date or release-evidence phrasing | As a reviewer, I need residual mutable labels removed after link review. | Source labels must use checked-date or release-evidence language, not live mutable wording. | Residual wording is replaced and no new mutable claim is introduced; validation: docs diff and source-label scan. | Mutable-source-label audit findings decline. | Restore the previous wording only with a checked-date note or release-evidence task. | Implementer/reviewer: see MVP Task Ledger row. | Class: finance, healthcare, public-sector, and tooling-vendor references become clearer; Product: improves security/privacy and client demo proof. |
| P0.4: Expand every ledger atom into work-package entries with user story, control requirements, acceptance, metric, rollback, implementer, and reviewer | As a subagent, I need every ledger atom to be handoff-ready before implementation. | One work-package entry must exist per ledger row, with required fields and no checkbox/status changes. | Ledger IDs and work-package entries match exactly; validation: docs diff, ledger/package consistency check, and reference-resolution check. | Missing/extra work-package count reaches zero. | Remove or regenerate the Work Packages section from the ledger. | Implementer/reviewer: see MVP Task Ledger row. | Class: all categories get mandatory coverage fields; Product: improves every product gate by making impact explicit. |
| P0.15: Sync the P0.4 work-package subagent fields with the ledger reviewer assignment and rerun consistency checks | As an orchestrator, I need the P0.4 work-package reviewer to match the ledger before review handoff. | Only the P0.4 work-package subagent reviewer may change; ledger rows, status checkboxes, and unrelated package content must stay intact. | P0.4 work-package assignment is sourced from the MVP Task Ledger and consistency checks are rerun; validation: docs diff, `git diff --check`, ledger/package consistency check, and reference-resolution check. | agent_assignment_mismatches reaches zero and missing/extra work-package count stays zero. | Revert only the P0.4 subagent-field edit if the ledger reviewer assignment changes. | Implementer/reviewer: see MVP Task Ledger row. | Class: all categories preserve reviewer traceability; Product: improves local evidence, review-protocol consistency, and packaging/support. |
| P0.16: Make the ledger the authoritative source for live implementer/reviewer assignment to prevent duplicate tracking drift | As an orchestrator, I need one authoritative assignment source so package handoffs cannot diverge from ledger tracking. | The MVP Task Ledger must be the sole source for live implementer names, reviewer names, and checkbox status; Work Package Subagents fields must refer to the ledger row. | Work Package rows no longer duplicate live assignment names, P0.16 has a package entry, and ledger/package IDs match; validation: `git diff --check`, ledger/package consistency and reference-resolution check, and assignment-drift check. | agent_assignment_drift risk declines and missing/extra work-package count stays zero. | Revert only the P0.16 documentation edits if the ledger-source contract is invalid. | Implementer/reviewer: see MVP Task Ledger row. | Class: all categories preserve assignment traceability; Product: improves local evidence, review-protocol consistency, packaging/support, and security/privacy. |
| P0.17: Align the Work Package Template status placeholders with the ledger-only assignment and status authority | As an orchestrator, I need work-package templates to point at the ledger for assignment and status so new package handoffs cannot reintroduce duplicate live tracking fields. | The Work Package Template must not include live Implemented/Reviewed checkboxes or live implementer/reviewer names; template status and subagent placeholders must source from the MVP Task Ledger row. | Template status and implementer/reviewer placeholders point to the MVP Task Ledger row, P0.17 has a package entry, and ledger/package IDs match; validation: `git diff --check`, ledger/package consistency and reference-resolution check, and assignment/status drift check. | template_status_drift risk declines and missing/extra work-package count stays zero. | Revert only the P0.17 template/package documentation edits if the ledger-source contract is invalid. | Implementer/reviewer: see MVP Task Ledger row. | Class: all categories preserve assignment and status traceability; Product: improves local evidence, review-protocol consistency, packaging/support, and security/privacy. |
| P0.5: Define the client-classification example-test matrix as a reusable acceptance checklist for all work packages | As a reviewer, I need classification coverage checked the same way for every task. | Matrix rows must map to generic primitives, not vertical-specific branches. | Reusable checklist names row IDs, fixture/evidence expectations, minimum privacy/security/performance expectations, and go/no-go signals; handoffs require a coverage decision per affected row; validation: docs diff and checklist trace to review prompt. | Classification-coverage omissions in reviews decline. | Revert to the previous matrix and block release until coverage is restored. | Implementer/reviewer: see MVP Task Ledger row. | Class: directly covers all categories; Product: improves client demo proof, security/privacy, and ICP focus. |
| P0.6: Install the canonical reviewer prompt and review-output schema as mandatory fields for every work package review | As an orchestrator, I need reviews to be comparable and evidence-backed. | Every review handoff must use the canonical prompt and required output schema. | Work-package reviews cannot be accepted without schema-complete criteria, client-classification table, product-success table, verification evidence, signoff, and `Decision: GO`, unless this plan records a project-approved exception/no-go/pivot; validation: docs diff and review-schema trace. | Informal-review count declines and review-protocol coverage rises. | Revert prompt changes only if a replacement schema preserves required criteria and the schema-complete GO rule. | Implementer/reviewer: see MVP Task Ledger row. | Class: all categories get review coverage; Product: improves security/privacy, client demo proof, packaging/support, and local evidence. |
| P0.7: Choose and document the MVP beachhead ICP, adoption wedge, paid buyer, non-targets, and final-state expansion path | As a product owner, I need a beachhead that prevents broad assistant drift. | ICP choice must distinguish primary paid buyer, daily user/champion, urgent pain, adoption wedge, trigger event, buying criteria, non-targets, classification priority, falsifiable continue/pivot/no-go evidence, and expansion path. | MVP ICP names `enterprise-saas` as primary paid beachhead, `open-source` as adoption wedge, secondary/deferred rows, and explicit non-targets enough to reject out-of-scope work; validation: docs diff, product-gate trace, ledger/work-package ID consistency, and focused coherence check against Product Success Gates, classification tests, traceability, tracking, and this row. | ICP ambiguity and all-segments-equal language decline; P0.8-P0.11 and P9 release gates have concrete buyer/value inputs. | Revert to hypothesis state and record no-go/pivot if no beachhead can be chosen or if P0.8/P9.7 evidence rejects the chosen buyer. | Implementer/reviewer: see MVP Task Ledger row. | Class: `enterprise-saas` primary, `open-source` adoption wedge, `tooling-vendor` and `local-model` secondary, `finance`/`healthcare`/`public-sector` deferred or proof-only; Product: directly covers ICP, retained use, activation, packaging/support, and local evidence. |
| P0.8: Create the design-partner validation protocol, interview/runbook template, value-signal rubric, and retained-use evidence format | As a product validator, I need repeatable design-partner evidence that tests Idiom as a local sidecar substrate without raw repo content. | Protocol must enforce the P0.7 beachhead priority, collect participant qualification, buyer/user split, repo/workflow context, current agent stack, trigger event, blocked workflow, privacy constraints, activation attempt, expected evidence, follow-up cadence, retained-use check, adoption/budget signal, disqualifiers, rubric scores, and forbidden-field handling while minimizing sensitive data. | Dedicated P0.8 protocol, runbook template, rubric, retained-use evidence format, primary-ICP thresholds, and P7.11/P9.7 feed rules are documented; validation: `git diff --check -- docs/idiom-implementation-plan.md`, ledger/work-package ID consistency check, and coherence check against P0.7, P9.7, P7.11, Product Success Gates, Client Classification Example Tests, Requirement Traceability, Risk Controls, and review protocol. | Completed validation-record count, primary-ICP count, activation score, trust/security score, retained-use score, adoption-signal score, forbidden-field failures, and open-source supporting-signal quality become measurable. | Suspend partner claims and revert to protocol draft if data collection is unsafe, incomplete, depends on hosted telemetry/raw prompts/raw file bodies/secrets, or treats open-source adoption as paid enterprise-SaaS demand. | Implementer/reviewer: see MVP Task Ledger row. | Class: `enterprise-saas` primary, `open-source` adoption wedge, `tooling-vendor` and `local-model` secondary pivot inputs, `finance`/`healthcare`/`public-sector` proof-only until controls pass; Product: directly improves ICP, activation, retained use, integration proof, security/privacy, packaging/support, and local product-evidence export. |
| P0.9: Produce a product threat model, data-flow map, privacy/retention posture, and NIST/OWASP/MCP/HHS/FINRA control mapping | As a security-sensitive buyer, I need trust claims mapped to concrete controls. | Data flows, retention, redaction, protected paths, MCP resources/roots, telemetry minimization, side-effect policy, and control anchors must be explicit, generic, and non-compliance-claiming. | Dedicated P0.9 posture section covers source anchors, threat model, data-flow map, privacy/retention posture, control mapping, client-row tieback, P7.9/P8.4/P9.9 gates, and unknowns; validation: `git diff --check -- docs/idiom-implementation-plan.md`, ledger/work-package ID consistency check, coherence check against P0.7, P0.8, P7.9, P8.4, P9.9, Product Success Gates, Client Classification Example Tests, Requirement Traceability, Risk Controls, Validation Matrix, and review protocol, plus control-source reachability/support check. | Unmapped security/privacy claims decline, protected-data paths become visible, and release reviewers can reject regulated claims without executable checks. | Block regulated claims, keep MVP positioned as local/internal sidecar proof, and require P7.9/P8.4/P9.9 follow-up evidence until mapping and checks are restored. | Implementer/reviewer: see MVP Task Ledger row. | Class: `enterprise-saas` primary posture, `finance`/`healthcare`/`public-sector` regulated proof, `tooling-vendor` MCP/resource proof, `local-model` offline/readiness proof, and `open-source` local-minimization proof become explicit; Product: directly improves security/privacy, client demo proof, integration proof, and local product-evidence export. |
| P0.18: Align the P0.9 OWASP control mapping with the cited current OWASP GenAI Top 10 source | As a security reviewer, I need OWASP control references to match the source version actually cited. | P0.9 must not cite the current OWASP GenAI Top 10 page while using older OWASP LLM IDs or names. | The OWASP source anchor and every OWASP mapping in P0.9 agree on the checked source version; validation: docs diff, OWASP source reachability/support check, P0.9 coherence check, and ledger/work-package ID consistency. | Unsupported OWASP mapping findings decline to zero and P0.9 can be re-reviewed. | Revert only the OWASP mapping/source edits if source support remains invalid, then block P0.9 review until a supported mapping exists. | Implementer/reviewer: see MVP Task Ledger row. | Class: all categories keep generic control mapping; Product: restores security/privacy, client demo proof, integration proof, and local product-evidence export confidence. |
| P0.10: Define activation and onboarding metrics for clean install, first `context_pack`, freshness check, audit report, and cleanup path | As a new user, I need first value measured instead of assumed. | Activation metrics must cover clean setup or clean fixture, first useful packet, freshness, audit, suggested verification, product-evidence-safe output, failure reason, docs gap, privacy/redaction status, P7.7/P7.11/P9.6 linkage, and cleanup. | Dedicated P0.10 metrics section defines funnel, evidence fields, segment-specific first-value moments, default targets, pass/fail/no-go thresholds, and measurement protocol; validation: `git diff --check -- docs/idiom-implementation-plan.md`, ledger/work-package ID consistency check, and coherence check against P0.7/P0.8, P7.7, P7.11, P9.6, Product Success Gates, Client Classification Example Tests, Requirement Traceability, Risk Controls, Validation Matrix, and review protocol. | Time-to-first-packet, time-to-trust-evidence, activation completion rate, docs-gap rate, privacy-safe export rate, cleanup success rate, failure reason, and assistance class become collectable. | Revert threshold claims to measurement-only or mark release activation experimental until P7.7/P9.6 smoke evidence validates the target. | Implementer/reviewer: see MVP Task Ledger row. | Class: `open-source`, `enterprise-saas`, `local-model`, and `tooling-vendor` activation become measurable; `open-source` remains adoption/docs evidence, not paid enterprise-SaaS demand. Product: directly improves activation and local product-evidence export. |
| P0.11: Define MVP and final-state packaging, pricing, support, admin, and enterprise-surface hypotheses with explicit non-goals | As a project owner, I need packaging scope to follow substrate proof. | Packaging and support hypotheses must preserve the P0.7 beachhead, keep MVP local/CLI/evidence-first, include pricing/support/admin non-goals, and avoid premature enterprise surface. | Dedicated P0.11 section documents MVP package, final-state package, pricing hypotheses, support boundary, admin boundary, enterprise-surface hypotheses, explicit non-goals, all seven classification stances, product-success coverage, P9.12 evidence fields, and continue/pivot/no-go criteria; validation: `git diff --check -- docs/idiom-implementation-plan.md`, ledger/work-package ID consistency check, and coherence check against Product Success Gates, Requirement Traceability, Risk Controls, Validation Matrix, P7.10/P7.11/P9.7/P9.11/P9.12. | Packaging ambiguity, premature-admin scope, unsupported pricing claims, and all-segments-equal packaging language decline. | Revert to hypothesis-only or non-goal-only state if buyer/support assumptions are unsupported, evidence export fails privacy checks, or packaging requires enterprise/admin work before sidecar proof. | Implementer/reviewer: see MVP Task Ledger row. | Class: all seven classifications are prioritized, deferred, secondary, proof-only, or no-go explicitly; Product: directly improves ICP, activation, retained use, integration proof, security/privacy, competitive proof, client demo proof, packaging/support, and local product-evidence export. |

### Phase 1 Work Packages

| ID and name | Story | Control requirements | Acceptance and validation | Metric or audit signal | Rollback or disable path | Subagents | Client-classification and product-success impact |
| --- | --- | --- | --- | --- | --- | --- | --- |
| P1.1: Introduce the typed autonomy contract shape for lanes, phases, and sidecar calls | As a harness maintainer, I need authority and obligations represented as data. | Contract shape must express path roles, effects, tools, budgets, verification, completion, and stop reasons without prescribing strategy. | Contract types exist and are passive; validation: phase contract tests and behavior audit. | Contract-coverage fields appear in logs/audit. | Keep contract generation report-only or revert type usage without changing lane behavior. | Implementer/reviewer: see MVP Task Ledger row. | Class: all categories gain a generic authority model; Product: improves security/privacy, integration proof, and local evidence. |
| P1.7: Fix autonomy contract scope aliasing and add stable-ID regression coverage | As an audit consumer, I need a constructed autonomy contract to be immutable with respect to caller-owned sidecar/tool-call input. | `NewContract` must deep-copy pointer scope fields before normalization and ID generation so caller mutations cannot make `contract_id` diverge from the contract body. | `ContractScope.Sidecar` and `ContractScope.ToolCall` are copied on construction; regression tests prove post-construction mutation of the original input cannot alter the returned contract or invalidate stable ID assumptions; validation: `go test ./internal/autonomy`, broad Go tests when feasible, `git diff --check`, and ledger/work-package consistency. | Stale-contract-ID aliasing findings decline to zero and P1.1 can be re-reviewed. | Revert only the deep-copy helper/test if it breaks construction, then keep P1.2/P1.4 blocked on P1.7. | Implementer/reviewer: see MVP Task Ledger row. | Class: all categories preserve trustworthy audit joins; Product: restores security/privacy, integration proof, and local product-evidence export confidence. |
| P1.2: Emit passive contract lifecycle, policy, and obligation events | As an auditor, I need contract decisions replayable before enforcement. | Events must be stable, passive, typed, and joinable to lane/sidecar calls. | Lifecycle, policy, and obligation events emit without behavior change; validation: phase contract tests and behavior audit. | Contract event count and replay coverage rise. | Disable event emission or ignore events in audit while preserving lane behavior. | Implementer/reviewer: see MVP Task Ledger row. | Class: finance, public-sector, enterprise SaaS, and tooling-vendor auditability improves; Product: improves security/privacy and local evidence. |
| P1.3: Join contract events to audit output without changing lane behavior | As a reviewer, I need audit reports to show contract coverage without raw-log reconstruction. | Audit output must reference contract IDs and event status while remaining passive. | Audit reports include contract lifecycle coverage; validation: phase tests and behavior audit. | Missing-contract-audit findings decline. | Hide new audit fields or revert join logic while keeping raw events. | Implementer/reviewer: see MVP Task Ledger row. | Class: finance, healthcare, public-sector, and enterprise SaaS gain replayable evidence; Product: improves security/privacy, retained use, and local evidence. |
| P1.8: Align `harness_audit` schema limits and assert no-event contract coverage omission | As an audit-tool caller, I need the tool schema and no-event behavior to match the passive audit implementation. | `harness_audit` schema text must match the current behavior limit defaults/caps, and no-event audit output must omit `contract_coverage` explicitly. | `internal/workspace/tool_schemas.go` describes the current `80` default and `5000` cap or removes stale exact numbers; workspace tests assert `ContractCoverage == nil` and/or serialized output omits `contract_coverage` when no autonomy events are present; validation: targeted workspace tests, broad relevant tests, `git diff --check`, and ledger/work-package consistency. | Tool-schema drift findings and no-event audit ambiguity decline to zero so P1.3 can be re-reviewed. | Revert only the schema/test edits and keep P1.3 review blocked if schema accuracy or no-event omission cannot be proven. | Implementer/reviewer: see MVP Task Ledger row. | Class: all categories preserve trustworthy audit-tool contracts; Product: restores packaging/support accuracy, security/privacy evidence, and local product-evidence export confidence. |
| P1.4: Add a named invariant map and shared path-role resolver for required, evidence, protected, forbidden, and side-effect paths | As a tool caller, I need path authority resolved consistently. | Path roles must be named, shared, and policy-owned rather than prompt-owned. | Resolver covers required, evidence, protected, forbidden, and side-effect paths; validation: path-role tests and behavior audit. | Path-role decision events and protected-path findings become consistent. | Route calls through the previous path checks or keep resolver report-only. | Implementer/reviewer: see MVP Task Ledger row. | Class: finance, healthcare, public-sector, and enterprise SaaS protected-path stories improve; Product: improves security/privacy and integration proof. |
| P1.5: Migrate existing write, verification, completion, loop, and budget gates to contract obligations and events | As a harness maintainer, I need existing gates owned by the contract model. | Migration must preserve existing behavior while making obligations typed and auditable. | Existing gate tests pass with contract obligation events; validation: phase tests and behavior audit. | Gate event coverage rises and unresolved-obligation findings become explicit. | Fall back to legacy gates and leave contract events passive. | Implementer/reviewer: see MVP Task Ledger row. | Class: all categories gain consistent task closure controls; Product: improves activation, security/privacy, retained use, and local evidence. |
| P1.9: Close P1.5 gate-obligation lifecycle and verify post-readiness behavior preservation | As an audit reviewer, I need successful observed gates to close their opened obligations without false unresolved-obligation findings. | Gate outcomes must either emit `obligation_resolved`/`obligation_waived` events or be reconciled into obligation lifecycle state by one authoritative audit path. Post-readiness probe-budget behavior must be proven unchanged or restored if the P1.5 migration altered it. | Write accepted, verification satisfied, final answer accepted, budget closeout/exhaustion, and normal loop-fuse closeout close the matching obligations; successful runner event streams produce no false unresolved obligations; post-readiness behavior-preservation tests pass; validation: autonomy, agent, workspace, broad Go tests, behavior audit, and ledger/work-package consistency. | False unresolved-obligation findings decline to zero and P1.5 can be re-reviewed. | Disable only the new obligation-close emission/reconciliation and keep P1.5 review blocked if closure or behavior preservation cannot be proven. | Implementer/reviewer: see MVP Task Ledger row. | Class: all categories gain reliable audit closure; Product: restores security/privacy, retained use, integration proof, and local product-evidence export confidence. |
| P1.10: Scope write-gate obligation closure to changed required paths and preserve partial-write unresolved evidence | As an audit reviewer, I need one successful write to close only the required-path obligations it actually satisfied. | Write-gate lifecycle closure must be path-aware. For contracts with required edit paths, only obligations whose `TargetRef` is in the successful changed-path set may resolve; other required-path write obligations must remain open. Contracts without required paths keep the single workspace-change obligation behavior. | A regression test with at least two `RequiredEditPaths` emits one successful write for one path and proves audit keeps the other path unresolved; validation: focused runner/autonomy tests, agent tests, broad Go tests, `git diff --check`, and ledger/work-package consistency. | False partial-write completion findings decline to zero and P1.9/P1.5 can be re-reviewed. | Revert only the path-aware resolution filter and keep P1.9/P1.5 review blocked if partial writes cannot be represented accurately. | Implementer/reviewer: see MVP Task Ledger row. | Class: all categories preserve trustworthy required-path audit evidence; Product: restores security/privacy, retained use, integration proof, and local product-evidence export confidence. |
| P1.6: Normalize side-effect classes, protected-path decisions, and approval outcomes into contract policy events | As a security reviewer, I need side effects and approvals normalized across callers. | Side-effect, protected-path, and approval outcomes must use shared event vocabulary. | Policy events represent allowed, denied, and approved outcomes; validation: policy tests and behavior audit. | Side-effect and permission-denial audit coverage rises. | Disable normalized event reporting and retain existing permission behavior. | Implementer/reviewer: see MVP Task Ledger row. | Class: finance, healthcare, public-sector, enterprise SaaS, and tooling-vendor controls improve; Product: improves security/privacy and integration proof. |
| P1.11: Fix P1.6 forbidden-path normalization and conservative shell side-effect classification | As a security reviewer, I need policy events to report outside-scope denials and shell side effects conservatively. | Required-edit-scope denials outside required paths and not protected must emit live `path.forbidden` normalization with denied result, and shell commands with compound syntax or mutating chained commands must not be classified as `read_only`. | Runner-autonomy tests prove live outside-scope write denial emits `InvariantForbiddenPath` without manually injecting the invariant; shell tests prove allowed `git diff --check` and `go test` remain `read_only`, while `git status && rm -rf tmp`, `go test ./...; touch x`, and similar compound/mutating commands classify `unknown`; validation: autonomy/agent/broad Go tests, `git diff --check`, and ledger/work-package consistency. | Policy-event semantic findings decline to zero and P1.6 can be re-reviewed. | Disable only the new normalization helpers and keep P1.6 review blocked if forbidden-path or shell classification semantics remain unreliable. | Implementer/reviewer: see MVP Task Ledger row. | Class: finance, healthcare, public-sector, enterprise SaaS, and tooling-vendor policy evidence becomes trustworthy; Product: restores security/privacy, integration proof, client demo proof, and local product-evidence export confidence. |

### Phase 2 Work Packages

| ID and name | Story | Control requirements | Acceptance and validation | Metric or audit signal | Rollback or disable path | Subagents | Client-classification and product-success impact |
| --- | --- | --- | --- | --- | --- | --- | --- |
| P2.1: Export tool schemas from the existing manifest source of truth | As an external integrator, I need schemas exported from the real tool manifest. | Schema export must not create a second source of truth. | Every manifest tool has exported schema, profile, side-effect, and error metadata where available; validation: workspace/cmd tests and tool-surface audit. | Manifest/schema drift findings decline. | Hide the export command or revert to internal manifest only. | Implementer/reviewer: see MVP Task Ledger row. | Class: tooling-vendor and enterprise SaaS integration improves; Product: improves integration proof and local evidence. |
| P2.2: Add structured freshness and confidence fields to graph-backed results | As an agent, I need to know whether repository evidence is fresh enough to trust. | Graph-backed results must include index, freshness state, stale paths, confidence, and provenance. | `context_status`, `context_pack`, and graph-backed outputs expose freshness/confidence; validation: comprehension/workspace tests and context status. | Stale-evidence ambiguity declines and freshness fields appear in audit. | Keep freshness report-only or block stale-use with an explicit waiver path. | Implementer/reviewer: see MVP Task Ledger row. | Class: all categories get trustworthy context; Product: improves activation, security/privacy, competitive proof, and local evidence. |
| P2.5: Replace graph-backed prescriptive remediation strings with neutral structured reason codes | As a graph-backed tool caller, I need stale/missing/gap outputs to report facts and uncertainty without choosing the next tool or action. | Graph-backed blocker/gap/remediation text must not name next tools or imperative actions. Replace prescriptive strings with neutral structured reason/state codes such as `missing_index`, `collector_version_mismatch`, `stale_unbounded_graph`, and `budget_exceeded`, plus factual fields for affected paths/counts. | `context_status`, `context_pack`, `context_query`, `context_zoom`, `context_impact`, and graph-backed `discovery_query` outputs contain no tool-prescriptive remediation strings such as `run context_index`, `use context_pack`, `provide a bounded path`, or similar next-action prose; validation: comprehension/workspace tests, output-smoke checks, absence-regression tests, broad Go tests, `git diff --check`, and ledger/work-package consistency. | Strategy-leakage findings in graph-backed outputs decline to zero so P2.2 can be re-reviewed. | Revert only wording/reason-code normalization and keep P2.2/P2.3/P2.4 blocked if neutral evidence output cannot be proven. | Implementer/reviewer: see MVP Task Ledger row. | Class: all categories get evidence-only graph contracts; Product: restores security/privacy, integration proof, competitive proof, and local product-evidence export confidence. |
| P2.6: Support path-only `context_impact` requests with neutral graph-backed validation output | As a graph-backed tool caller and reviewer, I need a path-only impact query to produce source-specific impact evidence instead of failing with a missing task. | `context_impact` must derive a bounded evidence task from `path` when no task/query/prompt/content/pattern is supplied, preserve P2.5 neutral reason/state wording, and avoid raw source bodies, secrets, next-action prescriptions, or unbounded scans. | `go run ./cmd/idiom tool context_impact --input '{"path":"internal/comprehension/pack.go"}'` succeeds and emits structured freshness/confidence/provenance/gap facts with neutral wording; validation: workspace/comprehension tests, exact path-only smoke, P2.5 forbidden-phrase absence tests, broad Go tests, `git diff --check`, and ledger/work-package consistency. | Path-only impact smoke becomes a reliable release/review proof and P2.5/P2.2 can be re-reviewed. | Keep path-only `context_impact` unsupported only with an explicit reviewed plan amendment and block downstream stale-evidence tasks until the validation contract is coherent. | Implementer/reviewer: see MVP Task Ledger row. | Class: all categories get dependable path-scoped impact evidence; Product: restores integration proof, security/privacy, competitive proof, client demo proof, and local product-evidence export confidence. |
| P2.8: Restore broad-suite validation by aligning `idiom-perf-eval` worst-offender output with the current perf schema | As a reviewer, I need `go test ./...` to compile so follow-up reviews can trust broad-suite validation. | `cmd/idiom-perf-eval` must use the current `PerfWorstOffender` schema without reintroducing removed raw prompt fields or weakening privacy posture. | The compile error from `cmd/idiom-perf-eval/main.go` reading `off.UserPrompt` is fixed in the narrowest compatible way; validation: focused cmd/eval tests or builds, `go test ./...`, `git diff --check`, and ledger/work-package consistency. | Broad-suite validation returns to green and P2.6 can be closed by follow-up review evidence. | Revert only the perf-eval output adjustment if it misrepresents worst-offender data; keep P2.6/P2.7 blocked if the broad suite cannot compile. | Implementer/reviewer: see MVP Task Ledger row. | Class: all categories regain trustworthy validation evidence; Product: restores integration proof, competitive proof, packaging/support, and local product-evidence export confidence. |
| P2.7: Clear and regenerate legacy graph views so cached outputs honor the P2.5 evidence-only contract | As a pre-production caller, I need this workspace's retained graph views to be rebuilt under the new neutral evidence contract instead of preserving obsolete prescriptive cached rows. | Because Idiom is not production yet, legacy `graph_views` rows and view artifacts that contain old prescriptive gap/remediation strings should be cleared and regenerated from current code. Do not design a long-lived migration/versioning scheme unless needed to make the cleanup safe and repeatable. | Delete or invalidate the local legacy graph-view cache, regenerate representative `context_pack`, `context_impact`, and graph-backed `discovery_query` outputs, and verify reused-task cache hits no longer emit forbidden phrases such as `rerun context_trace_pack`, `run context_index`, `use context_pack`, or `provide a bounded path`; validation: current-source smokes before/after cleanup, comprehension/workspace tests, broad Go tests if feasible, `git diff --check`, and ledger/work-package consistency. | Existing-workspace strategy-leakage findings decline to zero after regeneration and P2.5/P2.2 can be re-reviewed against this workspace. | If cleanup damages the local graph store, rebuild the comprehension index and regenerated view artifacts from current source; keep downstream stale-evidence tasks blocked until smokes pass. | Implementer/reviewer: see MVP Task Ledger row. | Class: all categories get dependable retained-use evidence in the pre-production workspace; Product: restores retained use, integration proof, security/privacy, competitive proof, packaging/support, client demo proof, and local product-evidence export confidence. |
| P2.3: Add manifest/schema/suggestion drift checks and stale-evidence blockers | As a maintainer, I need schema drift and unsafe stale evidence caught automatically. | Drift checks must compare manifest, schema, suggestions, and stale-use blockers. | Checks fail on drift or unsafe stale evidence; validation: workspace/comprehension/cmd tests and tool-surface audit. | Drift finding count and stale-use blocker coverage become measurable. | Disable the blocker to report-only while preserving drift reports. | Implementer/reviewer: see MVP Task Ledger row. | Class: tooling-vendor, enterprise SaaS, finance, healthcare, and public-sector gain safer contracts; Product: improves security/privacy and integration proof. |
| P2.9: Fix P2.3 stale-evidence correctness gaps for new files, zoom policy propagation, and report-only telemetry | As a stale-evidence reviewer, I need freshness blockers to catch newly added source files, preserve caller-selected policy across graph tools, and report telemetry states accurately. | Unbounded freshness must detect source files added after indexing; `context_zoom` must forward `freshness_policy` and `freshness_max_files` into freshness checks; telemetry/audit/perf counters must distinguish blocked, reported, allowed-stale, and fresh evidence instead of labeling report-only as blocked. Outputs must remain neutral evidence, not next-action prescriptions. | Temp-repo or fixture tests prove a newly added source file makes `require_fresh` stale/blocked, `context_zoom` preserves `report_only`, and telemetry/audit counts do not mark report-only as blocked; validation: comprehension/workspace/eval tests, tool-surface audit, exact smokes from the P2.3 review, broad Go tests, `git diff --check`, and ledger/work-package consistency. | False-fresh count for added files reaches zero, `context_zoom` stale-use coverage becomes truthful, and blocker telemetry distinguishes report-only from hard blockers. | Revert only the P2.9 freshness-policy/telemetry changes and keep P2.3 blocked if any high stale-evidence gap remains. | Implementer/reviewer: see MVP Task Ledger row. | Class: finance, healthcare, public-sector, enterprise SaaS, tooling-vendor, local-model, and open-source users regain trustworthy stale-evidence controls; Product: restores security/privacy, integration proof, retained use, competitive proof, client demo proof, packaging/support, and local product-evidence export confidence. |
| P2.4: Define graph view result contracts, ranker reasons, unresolved gaps, and cache invalidation for ranker weights and runtime traces | As a repository-memory user, I need graph views to explain confidence, gaps, and cache validity. | Result contracts must include reasons, unresolved gaps, invalidation semantics, and provenance. | Graph view contracts document and expose ranker/cache/freshness semantics; validation: comprehension/workspace tests and context status. | Cache/freshness and unresolved-gap audit signals become explicit. | Fall back to recomputing packets and mark cache semantics experimental. | Implementer/reviewer: see MVP Task Ledger row. | Class: open-source, enterprise SaaS, tooling-vendor, and local-model users get clearer repo memory; Product: improves retained use, competitive proof, and local evidence. |
| P2.10: Bound runtime-trace cache fingerprinting and separate invalidation source attribution | As a graph-view cache reviewer, I need invalidation checks to be bounded and to identify whether ranker weights, runtime traces, index state, or source fingerprints affect cache validity. | Normal `context_pack` cache lookup must not scan every retained runtime trace artifact. Runtime trace invalidation must use the same bounded recent-trace horizon or an aggregate manifest, and invalidation source metadata must expose distinct ranker-weight and runtime-trace fingerprints/sources rather than one composite value for both. | Tests or fixtures prove cache lookup trace fingerprinting is bounded with many retained traces, ranker-weight-only and runtime-trace-only changes produce distinct invalidation source attribution, and graph-view outputs still expose neutral cache/gap contracts; validation: comprehension/workspace tests, first/second `context_pack` smoke, audit/status smokes, broad Go tests, `git diff --check`, and ledger/work-package consistency. | Cache lookup work is bounded and invalidation-source attribution becomes truthful enough for audit/debug tooling. | Fall back to recomputing graph views if bounded invalidation cannot be made truthful; keep P2.4 blocked until performance and attribution are reviewed. | Implementer/reviewer: see MVP Task Ledger row. | Class: open-source, enterprise SaaS, tooling-vendor, local-model, and regulated users get supportable retained-cache evidence; Product: restores retained use, integration proof, competitive proof, packaging/support, and local evidence confidence. |
| P2.11: Update graph-view cache documentation for workspace, ranker, and runtime trace fingerprints | As an operator or future agent, I need repo documentation to match the reviewed graph-view cache key and invalidation behavior. | `AGENTS.md` and `docs/codebase-comprehension-memory-design.md` must no longer claim ranker weights/runtime traces are outside the cache key or describe obsolete all-trace stat/directory-name fingerprinting. Docs must describe workspace-state fingerprinting, separate ranker/runtime trace fingerprints, the bounded trace manifest/fallback behavior, and remaining limitations accurately. | Documentation matches the current code paths in `pack.go`, `views.go`, `trace.go`, and `result_metadata.go`; validation: docs diff, focused source-doc coherence scan, `git diff --check`, and ledger/work-package consistency. | Cache-operability contradiction count reaches zero and P2.10/P2.4 can be closed by follow-up review evidence. | Revert only the documentation edits if code behavior changes again; keep P2.10/P2.4 blocked while docs contradict runtime cache contracts. | Implementer/reviewer: see MVP Task Ledger row. | Class: all categories get supportable cache documentation; Product: restores retained-use, integration-proof, packaging/support, competitive-proof, and local-evidence confidence. |
| P2.12: Complete graph-view fingerprint privacy docs for secrets and protected contents | As a privacy/security reviewer, I need graph-view fingerprint documentation to explicitly rule out secrets and protected contents as well as raw bodies. | `docs/codebase-comprehension-memory-design.md` must state that fingerprint contracts exclude secrets and protected contents, in addition to raw runtime trace bodies, prompts, transcript excerpts, and source file bodies. | The P2.11 privacy wording gap is corrected without changing source behavior or implementation-plan tracking; validation: docs diff, focused privacy wording scan, `git diff --check`, and ledger/work-package consistency. | Fingerprint privacy wording omissions decline to zero and P2.11/P2.10/P2.4 can be closed by follow-up review evidence. | Revert only the privacy wording edit if the underlying source contract changes; keep graph-view cache docs blocked while privacy exclusions are incomplete. | Implementer/reviewer: see MVP Task Ledger row. | Class: finance, healthcare, public-sector, enterprise SaaS, tooling-vendor, local-model, and open-source users get clearer privacy posture; Product: restores security/privacy, packaging/support, integration proof, and local evidence confidence. |
| P2.13: Purge current pre-production legacy graph-view rows/artifacts and regenerate replacement view cache | As a pre-production caller, I need the current workspace to stop carrying stale `graph_views` rows and exported view artifacts from older index IDs once the replacement cache contract has landed. | Treat `graph_views` rows and `.idiom/comprehension/views/*.json` as derived local data. Because Idiom is not production yet, delete the legacy rows/artifacts and regenerate from current source rather than building a migration layer. Do not delete source files, tracked evidence, docs, ranker logs, runtime traces, or non-view comprehension artifacts. | Capture before/after counts, delete current `graph_views` rows and exported view JSON files, rebuild or refresh the comprehension index from current source, regenerate representative `context_pack`, `context_impact`, and graph-backed `discovery_query` outputs, prove repeated same-task `context_pack` returns a `graph_views` cache hit, and verify no regenerated output contains forbidden strategy phrases such as `rerun context_trace_pack`, `run context_index`, `use context_pack`, or `provide a bounded path`; validation: current-source smokes, cache-row SQL checks, `context_status`, `harness_audit`, `git diff --check`, and ledger/work-package consistency. | Current `graph_views` rows should be fresh/current-index rows after regeneration, stale legacy-row count should fall to zero for the regenerated cache, and representative view artifacts should match the replacement contract. | If cleanup damages the local graph store, rebuild the comprehension index and regenerated view artifacts from current source. If regeneration cannot pass neutral-output smokes, keep P4.4 blocked on this row rather than preserving legacy cache data. | Implementer/reviewer: see MVP Task Ledger row. | Class: all client classifications get trustworthy retained context in this pre-production workspace; Product: restores retained use, integration proof, competitive proof, client demo proof, packaging/support, security/privacy, and local product-evidence export confidence. |
| P2.14: Re-purge graph-view cache after active source edits settle and clear legacy graph-backed discovery artifacts | As a pre-production caller, I need the final retained graph-view cache to remain current after active code-writing follow-ups finish, and I need generated graph-backed discovery artifacts to stop carrying old strategy-prescriptive output. | Run this after active source-editing follow-ups that affect the comprehension index have passed review. Delete only derived `graph_views` rows, `.idiom/comprehension/views/*.json`, and legacy generated `.idiom/discovery/queries/*.json` artifacts that contain forbidden graph-backed strategy phrases. Do not delete source, docs, ranker logs, runtime traces, tracked evidence, or unrelated generated data. | Rebuild/refresh the index from current source, regenerate representative `context_pack`, `context_impact`, and graph-backed `discovery_query` outputs, prove same-task `context_pack` cache hit, and finish with zero stale `graph_views`, zero non-current-index `graph_views`, view-file count matching cache rows, and zero forbidden strategy phrases in graph-view rows/files and retained generated discovery-query artifacts; validation: SQL checks, `context_status`, `harness_audit`, forbidden-phrase scans, `git diff --check`, and ledger/work-package consistency. | P2.13 review NO-GO closes; current `graph_views` cache is fresh/current-index at review time, and legacy generated graph-backed query outputs no longer undermine neutral-output claims. | If any later source edit invalidates the cache during this implementation loop, rerun this purge/regenerate task after that edit's review rather than preserving stale rows. If generated discovery artifacts cannot be distinguished safely, delete only artifacts matching the forbidden phrases and regenerate representative graph-backed queries. | Implementer/reviewer: see MVP Task Ledger row. | Class: all client classifications get trustworthy retained context and evidence-only generated artifacts; Product: restores retained use, integration proof, competitive proof, client-demo proof, packaging/support, security/privacy, and local product-evidence export confidence. |
| P2.15: Finish graph-view purge with terminal fresh-status and current-index SQL checks | As a pre-production caller, I need the final purge/regenerate validation to end on a fresh graph and current-index-only cache rows, not on an intermediate cache state that review-time probes later invalidate. | Treat terminal `context_status` freshness and current-index SQL checks as the final go/no-go. After the last representative graph-backed smoke and cache-hit proof, run a final `context_index` refresh if needed, delete any old-index/stale `graph_views` rows and their exported view files, regenerate only the minimal representative rows against the final current index, then make the last checks `context_status` plus SQL stale/non-current counts. Do not delete source, docs, ranker logs, runtime traces, tracked evidence, or unrelated generated data. | End state must be `context_status.fresh=true`, zero stale `graph_views`, zero non-current-index `graph_views`, view-file count matching current rows, and zero forbidden strategy phrases in graph-view rows/files and retained generated discovery-query artifacts. Validation must show repeated same-task `context_pack` hit on the final index and then terminal SQL/status checks with no further source-changing actions afterward. | P2.14 review NO-GO closes; P4.4 can rely on a current replacement graph-view cache. | If a later source edit invalidates the cache, rerun P2.15 after that edit's review; otherwise do not preserve old-index rows. | Implementer/reviewer: see MVP Task Ledger row. | Class: all client classifications get trustworthy retained context and evidence-only generated artifacts; Product: restores retained use, integration proof, competitive proof, client-demo proof, packaging/support, security/privacy, and local product-evidence export confidence. |

### Phase 3 Work Packages

| ID and name | Story | Control requirements | Acceptance and validation | Metric or audit signal | Rollback or disable path | Subagents | Client-classification and product-success impact |
| --- | --- | --- | --- | --- | --- | --- | --- |
| P3.1: Define the initial read-only capability vocabulary and resolver registry | As an agent caller, I need task capabilities instead of a raw tool menu. | Capability vocabulary must be read-only, generic, and explicit about resolver ownership. | Initial capability registry exists with documented semantics; validation: workspace/agent/app tests and behavior audit. | Capability coverage and unmapped-adapter signals appear. | Keep legacy tool exposure and mark resolver disabled. | Implementer/reviewer: see MVP Task Ledger row. | Class: all categories get generic, profile-safe discovery language; Product: improves activation and integration proof. |
| P3.6: Fix P3.1 source-sanity capability side-effect classification against schema truth | As a security reviewer, I need the read-only capability vocabulary to exclude or honestly mark adapters with mixed/unknown side effects. | The `source-sanity` capability must not be advertised as read-only-only while the schema side-effect source of truth marks `source_sanity`/diagnostics as `read_only` plus `unknown`. The registry, docs, tests, and audit metrics must either defer `source-sanity` from the initial read-only vocabulary or model its mixed/unknown side effects explicitly. | Tests compare capability adapter side effects against the schema/manifest source of truth, audit identifies any non-read-only mapped adapters directly, and `harness_audit` no longer hides the mismatch behind aggregate counts; validation: workspace/cmd tests, audit smoke, broad Go tests, `git diff --check`, and ledger/work-package consistency. | Unsafe read-only capability mappings decline to zero or are explicitly counted as deferred/non-read-only. | Disable or defer mixed/unknown capabilities while preserving direct tool fallback; keep P3.1 blocked until read-only vocabulary truthfulness is reviewed. | Implementer/reviewer: see MVP Task Ledger row. | Class: finance, healthcare, public-sector, enterprise SaaS, tooling-vendor, local-model, and open-source users get safer integration contracts; Product: restores security/privacy, integration proof, retained use, packaging/support, and local evidence confidence. |
| P3.2: Route at least three read-only capabilities through existing adapters | As an external agent, I need capabilities to call proven adapters rather than new strategy code. | Routing must preserve adapter uncertainty, fallback, and read-only boundaries. | Three capabilities route through existing adapters in focused tests; validation: workspace/agent/app tests and behavior audit. | Routed-capability count rises and fallback reasons are logged. | Disable capability routes and keep direct adapters visible. | Implementer/reviewer: see MVP Task Ledger row. | Class: tooling-vendor, open-source, and enterprise SaaS integration improves; Product: improves activation, integration proof, and competitive proof. |
| P3.7: Remove broad workspace language-detection scans from read-only capability routing | As a capability caller, I need a path-specific read-only capability request to add only resolver overhead, not broad workspace discovery before the selected adapter runs. | `read_only_capability` must not call `AllowedTools()` or otherwise trigger full workspace language-detection scans on every routed request. Resolver execution should use bounded/static registry metadata or a cached manifest view while preserving direct-tool fallback and schema-truth side-effect checks. | Tests prove a path-specific capability route does not call broad language detection/`AllowedTools()` before routing, disable/unmapped fallback remains structured, and audit metrics remain accurate; validation: workspace/agent/app/cmd tests as affected, route smokes, audit smoke, broad Go tests, `git diff --check`, and ledger/work-package consistency. | Capability routing overhead becomes bounded and P3.2 can be closed by follow-up review evidence. | Disable capability execution and keep direct adapters visible if bounded routing cannot be proven. | Implementer/reviewer: see MVP Task Ledger row. | Class: tooling-vendor, open-source, enterprise SaaS, local-model, and regulated users get supportable routing performance; Product: restores activation, integration proof, retained use, competitive proof, and packaging/support confidence. |
| P3.3: Migrate runtime briefs and recovery text toward capability language | As a model operator, I need recovery nudges to name capabilities without prescribing strategy. | Runtime text must avoid next-tool prescriptions except typed invariant blocks. | Briefs/recovery text use capability language and remove duplicate strategy menus; validation: agent/app tests and behavior audit. | Strategy-leakage audit findings and prompt-token duplication decline. | Revert prompt text while leaving resolver code disabled or hidden. | Implementer/reviewer: see MVP Task Ledger row. | Class: all categories preserve model/harness authority split; Product: improves security/privacy and integration proof. |
| P3.8: Remove remaining prompt-surface tool prescriptions from P3.3 runtime and lane text | As a runtime-text reviewer, I need live prompt surfaces and lane preambles to describe capability/contract outcomes instead of telling the model which tool strategy to use. | The live strings flagged in `internal/agent/runner.go`, `internal/agent/runner_child_tasks.go`, and `cmd/idiom/lanes/implement/preamble.md` must no longer prescribe raw next tools or strategy menus unless they are inside typed invariant blocks or adapter/schema examples. Behavior audit must not be the only proof; source-level absence checks must back the claim. | Focused tests/greps prove no live runtime or prompt-surface occurrences of `read the newest selected log tail now`, `prefer one discovery_query first`, raw `search_content`/`read_file` strategy prescriptions, or implementation-lane raw tool menus remain outside allowed contexts; validation: agent/app/cmd tests as affected, behavior audit, source grep/sanity checks, broad Go tests, `git diff --check`, and ledger/work-package consistency. | Current-source strategy-prescription findings decline to zero and P3.3 can be closed by follow-up review evidence. | Revert only P3.8 text changes if they break typed invariant clarity; keep P3.3 blocked while live prompt surfaces prescribe strategies. | Implementer/reviewer: see MVP Task Ledger row. | Class: all categories preserve model/harness authority split; Product: restores security/privacy, integration proof, retained use, packaging/support, and local evidence confidence. |
| P3.9: Remove raw discovery-tool strategy menus from live profile prompt surfaces | As a profile prompt reviewer, I need all live profile prompts to use capability/contract language instead of raw discovery-tool preference menus. | `cmd/idiom/profiles/claude-qwen-worker.md` and `prompts/profiles/claude-qwen-worker.md` must no longer tell the model to prefer raw tools such as `code_navigate`, `*_find_symbol`, `*_read_symbol`, `*_references`, project wrappers, or `read_file`. The source-level regression net must cover live profile prompt files, not only runner code and lane preambles. | Focused source checks prove live profile prompt surfaces no longer contain raw discovery-tool strategy menus outside allowed adapter/schema/direct-fallback contexts; validation: cmd/agent/app tests as affected, behavior audit, source grep/sanity checks, broad Go tests, `git diff --check`, and ledger/work-package consistency. | Live-profile strategy-menu findings decline to zero and P3.3 can be closed by follow-up review evidence. | Revert only P3.9 prompt text if profile assembly breaks; keep P3.3 blocked while live profile prompts prescribe strategies. | Implementer/reviewer: see MVP Task Ledger row. | Class: all categories preserve model/harness authority split; Product: restores security/privacy, integration proof, retained use, packaging/support, and local evidence confidence. |
| P3.4: Add capability status, precision, fallback, absence-confidence, and unmapped-adapter reporting | As a reviewer, I need capability outputs to show uncertainty. | Reports must expose selected adapter, precision, fallback, absence confidence, and gaps. | Capability status is machine-readable and audited; validation: workspace/agent/app tests and behavior audit. | Unmapped-adapter and fallback metrics become visible. | Suppress status fields from public output while keeping internal logs. | Implementer/reviewer: see MVP Task Ledger row. | Class: tooling-vendor, enterprise SaaS, and open-source users get honest discovery signals; Product: improves competitive proof and local evidence. |
| P3.10: Add capability status public-suppression rollback path and fix audit field count | As an operator, I need a rollback switch that suppresses public capability status fields without losing internal telemetry/audit signals. | `read_only_capability` must support suppressing public `status` output while retaining internal telemetry/audit fields, and the capability audit `status_field_count` must match the actual JSON status field count. Documentation/tests must distinguish routing disable from status suppression. | Tests/smokes prove routed and fallback capability calls omit public `status` when suppression is enabled while telemetry/audit still reports status/fallback metrics, and audit reports the correct status field count; validation: workspace/agent/app/cmd tests as affected, route smokes, audit smoke, broad Go tests, `git diff --check`, and ledger/work-package consistency. | Public-status rollback path exists and status audit count is truthful, allowing P3.4 closure by follow-up evidence. | Disable only public status emission while preserving direct tools and internal logs; keep P3.4 blocked until rollback and audit-count truth are reviewed. | Implementer/reviewer: see MVP Task Ledger row. | Class: tooling-vendor, enterprise SaaS, open-source, regulated, and local-model users get safer rollout controls; Product: restores packaging/support, security/privacy, integration proof, competitive proof, and local evidence confidence. |
| P3.5: Expand the first routing slice to definition, references, file outline, symbol lookup, related files, and source sanity | As a coding agent, I need common read-only discovery actions routed consistently. | Expanded routes must stay read-only and preserve adapter evidence and uncertainty. | Routing slice covers named discovery actions with tests; validation: workspace/agent/app tests and behavior audit. | Broad read/search and zero-hit search metrics should decline in later evals. | Disable individual routes and fall back to legacy adapters. | Implementer/reviewer: see MVP Task Ledger row. | Class: open-source, enterprise SaaS, tooling-vendor, and local-model workflows improve; Product: improves activation, integration proof, and competitive proof. |

### Phase 4 Work Packages

| ID and name | Story | Control requirements | Acceptance and validation | Metric or audit signal | Rollback or disable path | Subagents | Client-classification and product-success impact |
| --- | --- | --- | --- | --- | --- | --- | --- |
| P4.1: Build a localization eval corpus from recent implementation logs | As an evaluator, I need representative localization tasks with known changed files. | Corpus must use logs safely, avoid raw secrets, and separate labels from measured runs. | Corpus records tasks, changed files, relevant tests, and failure classes; validation: comprehension/eval/workspace tests. | Eval sample count and label completeness become measurable. | Freeze corpus at prior version or exclude unsafe samples. | Implementer/reviewer: see MVP Task Ledger row. | Class: open-source and enterprise SaaS tasks get measured context proof; Product: improves competitive proof and local evidence. |
| P4.2: Add benchmark reporting for top-k edit-path coverage and exploration waste | As a product reviewer, I need measured context quality instead of anecdotes. | Reports must track top-k coverage, broad reads, zero-hit search, redirects, and budgets. | Benchmark report compares configured conditions and emits parseable metrics; validation: eval/workspace tests and behavior audit. | Top-k coverage and exploration-waste metrics move from absent to reported. | Disable benchmark gate and keep report generation manual. | Implementer/reviewer: see MVP Task Ledger row. | Class: enterprise SaaS, open-source, and tooling-vendor proof improves; Product: directly improves competitive proof and local evidence. |
| P4.3: Wire one verified ranker-learning trigger with inspectable learning logs | As a maintainer, I need one verified feedback loop into repository memory. | Learning trigger must use verified outcomes and avoid held-out eval leakage. | One trigger writes inspectable learning logs with source, reason, graph index, and outcome; validation: comprehension/eval/workspace tests. | Learning-log entries and ranker update audit signals appear. | Disable learning trigger and leave benchmark/read-only behavior intact. | Implementer/reviewer: see MVP Task Ledger row. | Class: open-source, enterprise SaaS, and local-model retained value improves; Product: improves retained use and local evidence. |
| P4.7: Sanitize manual ranker-learning notes before shared learning-log persistence | As a privacy/security reviewer, I need every writer of the shared ranker learning log to preserve the same sanitized evidence contract, including manual CLI boost/penalize paths. | Manual ranker notes and any other freeform learning-log fields must not persist raw prompts, raw file bodies, secrets, protected contents, or arbitrary operator text. Preserve useful auditability through bounded status/reason codes, note-present booleans, or sanitized identifiers rather than raw note text. | The manual `idiom ranker boost|penalize --note ...` path cannot write raw note text into `.idiom/ranker/learning_log.jsonl`; regression tests include a sentinel note value and prove it is absent from the log while accepted/rejected/manual event counts still work; validation: cmd/comprehension/workspace tests, privacy scan of generated temp logs, broad Go tests where feasible, `git diff --check`, and ledger/work-package consistency. | P4.3 privacy NO-GO is closed; learning-log privacy evidence becomes uniform across verified and manual writers. | Revert only the manual-note logging change if it breaks CLI operation, and keep P4.3 unreviewed plus P4.5/P4.6 blocked until shared learning logs are privacy-safe. | Implementer/reviewer: see MVP Task Ledger row. | Class: finance, healthcare, public-sector, enterprise SaaS, tooling-vendor, local-model, and open-source users get trustworthy local learning logs; Product: restores security/privacy, retained use, packaging/support, client-demo proof, and local product-evidence export confidence. |
| P4.8: Sanitize learning-log timestamps and all shared persistence freeform fields | As a privacy/security reviewer, I need the shared `AppendLearningLog` boundary to make every persisted field safe even for direct or future callers. | Caller-supplied timestamps and any other string fields at the shared learning-log persistence boundary must be normalized, generated, validated, or replaced with bounded identifiers before JSONL persistence. A caller must not be able to persist raw prompts, raw file bodies, secrets, protected contents, arbitrary operator text, or malformed timestamp payloads through `Timestamp` or any other direct `LearningLogEntry` field. | A direct `AppendLearningLog` regression with sentinel text in `Timestamp`, `Note`, and another plausible freeform string proves the sentinel is absent from `learning_log.jsonl`; valid/generated timestamps remain parseable or bounded; P4.7 manual note tests still pass; validation: cmd/comprehension/workspace tests, queue-isolated broad Go tests where feasible, `git diff --check`, and ledger/work-package consistency. | P4.7 review NO-GO closes; the P4.3 learning-log privacy NO-GO can be re-reviewed against one shared sanitized persistence boundary. | Revert only the timestamp/freeform sanitization helper if it breaks log parsing, and keep P4.3/P4.7 unreviewed plus P2.14/P4.5/P4.6 blocked until shared learning-log persistence is privacy-safe. | Implementer/reviewer: see MVP Task Ledger row. | Class: finance, healthcare, public-sector, enterprise SaaS, tooling-vendor, local-model, and open-source users get trustworthy local learning logs; Product: restores security/privacy, retained use, packaging/support, client-demo proof, and local product-evidence export confidence. |
| P4.9: Replace unknown learning-log tokens with strict enums, generated IDs, or bounded hashes | As a privacy/security reviewer, I need “safe-looking” arbitrary tokens and secret-shaped strings to be impossible to persist through learning-log ID, reason, evidence, or metric fields. | The shared learning-log persistence boundary must not treat regex-safe strings as automatically safe. Known enum fields must be allowlisted; generated IDs must be generated or structurally validated and regenerated on mismatch; unknown-but-needed evidence identifiers may be replaced by irreversible bounded hashes with stable prefixes; metric keys/evidence kinds must be allowlisted or collapsed to `unknown`. Direct callers must not be able to persist values like `ghp_1234567890abcdef`, `sk_live_demo_token`, or arbitrary operator prose through `EventID`, `Source`, `Reason`, `TaskID`, `CorpusID`, `ResultID`, evidence `Kind`, metric keys, or similar token fields. | Regression tests inject secret-shaped and arbitrary safe-token strings into every token-like `LearningLogEntry` field and prove the raw values are absent from JSONL while legitimate verified-learning/manual-ranker events still retain useful bounded source/action/state/reason/count semantics; validation: cmd/comprehension/workspace tests, queue-isolated broad Go tests where feasible, `git diff --check`, and ledger/work-package consistency. | P4.8 review NO-GO closes; the P4.7/P4.3 learning-log privacy chain can be re-reviewed against strict shared persistence semantics. | Revert only the strict-token sanitization helper if it breaks summary/audit parsing, and keep P4.3/P4.7/P4.8 unreviewed plus P2.14/P4.5/P4.6 blocked until shared learning-log persistence cannot store secret-shaped token strings. | Implementer/reviewer: see MVP Task Ledger row. | Class: finance, healthcare, public-sector, enterprise SaaS, tooling-vendor, local-model, and open-source users get trustworthy local learning logs; Product: restores security/privacy, retained use, packaging/support, client-demo proof, and local product-evidence export confidence. |
| P4.10: Prevent secret-shaped path and weight keys from persisting in learning logs | As a privacy/security reviewer, I need learning-log path and weight fields to avoid storing secret-shaped strings while preserving useful relative-path evidence. | The shared learning-log persistence boundary must treat `Files`, `Evidence.Files`, `BeforeWeights`, `AfterWeights`, and rollback weight keys as privacy-sensitive fields, not generic relative paths. Real repository-relative file paths and known ranker feature keys may remain if they pass bounded path/feature validation; token-only, secret-shaped, or arbitrary operator strings such as `ghp_1234567890abcdef`, `sk_live_demo_token`, and `normal_token` must be collapsed, hashed, or dropped before JSONL persistence. | Regression tests inject the blocked values through `Files`, `Evidence.Files`, `BeforeWeights`, `AfterWeights`, and `Rollback.Weights` and prove raw values are absent from JSONL while legitimate verified-learning/manual-ranker events still preserve bounded files, feature weights, rollback data, and audit counts; validation: cmd/comprehension/workspace tests, queue-isolated broad Go tests where feasible, `git diff --check`, and ledger/work-package consistency. | P4.9 review NO-GO closes; the P4.3/P4.7/P4.8/P4.9 learning-log privacy chain can be re-reviewed against all `LearningLogEntry` channels. | Revert only the path/weight sanitization helper if it breaks rollback, and keep P4.3/P4.7/P4.8/P4.9 unreviewed plus P2.14/P4.5/P4.6 blocked until path and weight fields cannot store secret-shaped strings. | Implementer/reviewer: see MVP Task Ledger row. | Class: finance, healthcare, public-sector, enterprise SaaS, tooling-vendor, local-model, and open-source users get trustworthy local learning logs; Product: restores security/privacy, retained use, packaging/support, client-demo proof, and local product-evidence export confidence. |
| P4.11: Reject or scrub secret-shaped learning-log path segments and filename stems | As a privacy/security reviewer, I need the learning-log path sanitizer to inspect every path segment and filename stem, not only the full path key. | `Files`, `Evidence.Files`, `BeforeWeights`, `AfterWeights`, and rollback weight keys must not persist blocked values when they appear as directory segments, filename stems, or path-like prefixes. Examples such as `ghp_1234567890abcdef/safe.go`, `sk_live_demo_token/pkg.go`, `normal_token/file.go`, `pkg/normal_token.go`, and equivalent weight keys must be rejected, scrubbed, or replaced before JSONL persistence, while legitimate bounded repo-relative paths and known ranker feature keys remain useful. | Regression tests inject blocked values as full paths, directory segments, filename stems, evidence files, before/after weight keys, and rollback weight keys, and prove raw values are absent from JSONL while valid rollback and representative file evidence still work; validation: cmd/comprehension/workspace tests, queue-isolated broad Go tests where feasible, `git diff --check`, and ledger/work-package consistency. | P4.10 review NO-GO closes; the P4.3/P4.7/P4.8/P4.9/P4.10 learning-log privacy chain can be re-reviewed against all known string channels. | Revert only the segment/stem path sanitizer if it breaks rollback or valid path evidence, and keep P4.3/P4.7/P4.8/P4.9/P4.10 unreviewed plus P2.14/P4.5/P4.6 blocked until path segments and stems cannot store secret-shaped strings. | Implementer/reviewer: see MVP Task Ledger row. | Class: finance, healthcare, public-sector, enterprise SaaS, tooling-vendor, local-model, and open-source users get trustworthy local learning logs; Product: restores security/privacy, retained use, packaging/support, client-demo proof, and local product-evidence export confidence. |
| P4.12: Block hyphenated sensitive key compounds in learning-log path and weight channels | As a privacy/security reviewer, I need sensitive key compounds to be rejected whether they appear as exact tokens, hyphenated compounds, path segments, filename stems, or weight keys. | The segment/stem sanitizer must recognize sensitive compounds such as `api-key`, `private-key`, `access-key`, and related separator variants without blocking all ordinary path words. `Files`, `Evidence.Files`, `BeforeWeights`, `AfterWeights`, and rollback weight keys must not persist those compounds as full values, directory segments, filename stems, or path-like prefixes. | Regression tests inject `api-key/safe.go`, `pkg/private-key.go`, `access-key/safe.go`, and equivalent underscore/dot/camel or separator variants through every path/weight channel and prove raw values are absent from JSONL while valid bounded paths and rollback still work; validation: cmd/comprehension/workspace tests, queue-isolated broad Go tests where feasible, `git diff --check`, and ledger/work-package consistency. | P4.11 review NO-GO closes; the P4.3/P4.7/P4.8/P4.9/P4.10/P4.11 learning-log privacy chain can be re-reviewed against all known string channels. | Revert only the compound-sensitive path sanitizer if it breaks rollback or valid path evidence, and keep P4.3/P4.7/P4.8/P4.9/P4.10/P4.11 unreviewed plus P2.14/P4.5/P4.6 blocked until compound key strings cannot persist. | Implementer/reviewer: see MVP Task Ledger row. | Class: finance, healthcare, public-sector, enterprise SaaS, tooling-vendor, local-model, and open-source users get trustworthy local learning logs; Product: restores security/privacy, retained use, packaging/support, client-demo proof, and local product-evidence export confidence. |
| P4.4: Add unified localization report covering outcome, trajectory, cache/freshness, ranker, verification, and audit metrics | As a release reviewer, I need one report connecting localization behavior to audit and verification. | Report must combine outcome, trajectory, cache/freshness, ranker, verification, and audit metrics without raw file bodies. | Unified report is generated and reviewable; validation: eval/workspace tests and behavior audit. | Localization report completeness and audit join coverage rise. | Revert to separate reports and record missing unified report as a blocker. | Implementer/reviewer: see MVP Task Ledger row. | Class: all categories gain evidence summaries; Product: improves competitive proof, retained use, and local evidence. |
| P4.13: Add product-success gate statuses and coherent client classification summaries to the unified localization report | As a release reviewer and buyer-facing product owner, I need the unified localization report to answer the sales-thesis gates explicitly, not only technical metrics. | The report must include statuses and evidence/reason fields for ICP, activation, retained use, integration proof, security/privacy, competitive proof, client-demo proof, packaging/support, and local product-evidence export. Any gate that cannot be evaluated from current inputs must be marked `not_available` or `not_applicable` with a bounded reason. The client-classification summary must list finance, healthcare/life sciences, public-sector, enterprise SaaS, tooling-vendor, local-model/private-code, and open-source with useful evidence status from the report/corpus, not just a confusing all-missing output for the real smoke. | Text and JSON unified reports include product-success gates and coherent client-classification rows; tests cover all gates and all client classifications, including the current corpus case where some classes have no direct samples but still get explicit `not_available` reasons tied to missing classification coverage rather than silent absence; validation: cmd/eval tests, CLI text/JSON smokes, privacy scan, broad Go tests where feasible, `git diff --check`, and ledger/work-package consistency. | P4.4 review NO-GO closes; localization report becomes a north-star release-review artifact rather than only a technical benchmark summary. | Revert only the product-gate/client-summary report additions if they break output compatibility, and keep P4.4 unreviewed plus P4.5/P4.6 blocked until product-success gates and client summaries are represented. | Implementer/reviewer: see MVP Task Ledger row. | Class: all client classifications get explicit report treatment; Product: restores ICP, activation, retained use, integration proof, security/privacy, competitive proof, client-demo proof, packaging/support, and local product-evidence export confidence. |
| P4.14: Make product-success gates account for degraded cache evidence in localization reports | As a release reviewer, I need product-success gates to downgrade when the same report contains stale or non-current cache evidence, rather than reporting `pass` from broad availability alone. | Gate logic must treat stale or non-current graph-view cache rows as degraded evidence. `retained_use`, `integration_proof`, and `local_product_evidence_export` must not return `pass` when cache rows are stale or non-current; they should return `partial`, `not_available`, or `fail` with bounded reasons such as `cache_stale` or `cache_non_current` depending on existing status semantics. Tests must cover stale/non-current cache metrics already present in the report. | Unified report text/JSON shows product gates degraded when cache evidence has stale/non-current rows; tests assert the P4.13 review scenario (`cache_stale>0`, `cache_non_current>0`) cannot produce pass for the affected gates; validation: cmd/eval/workspace/comprehension tests, CLI text/JSON smokes, privacy scan, cache-status checks, broad Go tests where feasible, `git diff --check`, and ledger/work-package consistency. | P4.13 review NO-GO closes; product-success gates become evidence-honest release criteria aligned with the north-star sales thesis. | Revert only the degraded-cache gate logic if it breaks report output compatibility, and keep P4.4/P4.13 unreviewed plus P4.5/P4.6 blocked until gates account for degraded cache evidence. | Implementer/reviewer: see MVP Task Ledger row. | Class: all client classifications get honest evidence status; Product: restores retained use, integration proof, local product-evidence export, security/privacy, and competitive proof confidence. |
| P4.5: Add audit-watch ownership, regression-promotion workflow, and before/after replay closure discipline | As an audit owner, I need failures promoted and closed with evidence. | High/medium findings need owner, status, reproducer, and before/after replay evidence. | Audit-watch workflow exists and regression promotion is documented/tested; validation: eval/workspace/cmd tests and behavior audit. | Owned finding rate rises and unresolved high/medium findings decline. | Disable promotion gate and keep findings report-only. | Implementer/reviewer: see MVP Task Ledger row. | Class: finance, public-sector, enterprise SaaS, and tooling-vendor governance improves; Product: improves security/privacy and local evidence. |
| P4.15: Sanitize audit-watch trigger, session, and reproducer evidence before local export | As a security/privacy reviewer, I need audit-watch packets to preserve useful closure evidence without leaking sensitive path segments, session identifiers, or token-shaped strings. | `trigger_files`, `sessions`, and reproducer evidence must pass through the same bounded privacy policy as the rest of audit-watch output. Sensitive-looking path segments such as `token=SECRET`, raw session IDs such as `token=SESSIONSECRET`, and equivalent arbitrary token/path values must be scrubbed, hashed, or replaced with bounded identifiers before JSON/text export. Terminal validation must refresh or otherwise resolve context index staleness so `context_status` is fresh after review smokes. | Regression tests inject sensitive trigger paths and session IDs and prove raw values are absent from audit-watch JSON while owner/status/reproducer/promotion/closure fields remain useful. Validation: cmd audit-watch tests, broad Go tests where feasible, adversarial CLI smoke, privacy scan, `harness_audit`, `context_status`, SQL graph-view current/stale/non-current checks, `git diff --check`, and ledger/work-package consistency. | P4.5 review NO-GO closes; audit-watch local evidence export becomes usable for regulated and private-code buyers. | Revert only the trigger/session/reproducer sanitization if it breaks audit-watch output shape, and keep P4.5 unreviewed plus P4.6 blocked until local evidence export is privacy-safe and terminal freshness is clean. | Implementer/reviewer: see MVP Task Ledger row. | Class: finance, healthcare/life sciences, public-sector, enterprise SaaS, tooling-vendor, local-model/private-code, and open-source users get privacy-safe audit evidence; Product: restores security/privacy, client-demo proof, packaging/support, retained use, and local product-evidence export confidence. |
| P4.16: Block customer-like and raw-transcript audit-watch evidence across trigger, session, reason, and reproducer fields | As a regulated/private-code buyer, I need audit-watch evidence to avoid leaking customer-like labels, raw transcript markers, or prompt/body phrases even when they do not look like API tokens. | The audit-watch privacy boundary must treat path segments under sensitive directories such as `secrets/`, customer-like session identifiers such as `customer-acme-session`, and raw prompt/transcript/body phrases such as `raw_prompt patient transcript body` as non-exportable. `trigger_files`, `sessions`, completion reason fragments, and reproducer evidence must emit bounded redacted identifiers or allowlisted reason codes instead of raw values. Terminal validation must end with fresh `context_status` and zero non-current/stale `graph_views` rows. | Regression tests and adversarial CLI smokes inject `logs/telemetry/secrets/customer-acme/feedback.jsonl`, `customer-acme-session`, `raw_prompt customer transcript`, and `raw_prompt patient transcript body`, then prove the raw strings are absent while schema, seven client classifications, product gate impacts, owner/status/reproducer/promotion/closure evidence remain useful. Validation: cmd audit-watch tests, broad Go tests where feasible, privacy scan, `harness_audit`, `context_status`, SQL graph-view current/stale/non-current checks, `git diff --check`, and ledger/work-package consistency. | P4.15 review NO-GO closes; P4.5 can be re-reviewed as a privacy-safe local evidence export workflow. | Revert only the broader audit-watch privacy-boundary hardening if it breaks packet shape, and keep P4.5/P4.15 unreviewed plus P4.6 blocked until customer-like and raw-transcript evidence cannot leak. | Implementer/reviewer: see MVP Task Ledger row. | Class: finance, healthcare/life sciences, public-sector, enterprise SaaS, tooling-vendor, local-model/private-code, and open-source users get buyer-safe local audit evidence; Product: restores security/privacy, local product-evidence export, client-demo proof, retained use, and packaging/support confidence. |
| P4.17: Refresh terminal context index and regenerated graph views after audit-watch privacy changes | As a reviewer, I need the audit-watch privacy chain to end with a fresh comprehension index and current regenerated graph-view rows, not stale operational state. | Treat `graph_views` rows and `.idiom/comprehension/views/*.json` as derived pre-production data. Delete stale/non-current view rows/artifacts, refresh the context index after the final audit-watch source edits, regenerate only representative current view rows, and make the last validation checks `context_status` plus SQL stale/non-current counts. Do not delete source, docs, ranker logs, runtime traces, or unrelated evidence. | Terminal state is `context_status.fresh=true`, `stale_path_count=0`, zero stale `graph_views`, zero non-current-index `graph_views`, and repeated same-task `context_pack` proves a current cache hit. Validation: targeted context refresh/index commands, SQL graph-view checks, `harness_audit`, focused audit-watch tests or smoke, `git diff --check`, and ledger/work-package consistency. | P4.16 review NO-GO closes; P4.5/P4.15/P4.16 can be marked reviewed if privacy smokes still pass. | If refresh fails, rebuild the comprehension index from current source and keep P4.5/P4.15/P4.16 unreviewed plus P4.6 blocked until terminal freshness is clean. | Implementer/reviewer: see MVP Task Ledger row. | Class: all client classifications get trustworthy local evidence export state; Product: restores retained use, integration proof, security/privacy, client-demo proof, packaging/support, and local product-evidence export confidence. |
| P4.6: Add competitive benchmark conditions for Aider-style repo-map exploration, external-agent native exploration, or documented infeasibility | As a buyer, I need fair comparison against alternatives or documented blockers. | Conditions must be reproducible, budget-aware, and honest about infeasibility. | Competitive conditions run or record precise blockers and impact on claims; validation: eval/workspace tests and benchmark report. | External-baseline coverage or documented-infeasibility count becomes explicit. | Remove unsupported comparison claims and keep only reproducible internal metrics. | Implementer/reviewer: see MVP Task Ledger row. | Class: enterprise SaaS, open-source, and tooling-vendor evaluation improves; Product: directly improves competitive proof. |
| P4.18: Constrain competitive-condition client classifications to approved bounded classes | As a privacy reviewer, I need competitive benchmark reports to aggregate only approved client classifications and never export customer-like classification labels. | Competitive-condition `client_classifications` must accept only the seven approved classes: finance, healthcare/life sciences, public-sector, enterprise SaaS, tooling-vendor, local-model/private-code, and open-source. Unknown or customer-like values such as `customer-acme-session` must be rejected, collapsed to a bounded non-exported invalid signal, or reported with an allowlisted reason without appearing in text/JSON output or allowing the competitive proof gate to pass from that poisoned classification. | Regression tests and adversarial CLI smokes inject `client_classifications=[\"customer-acme-session\"]` and prove `customer_acme_session` is absent from text/JSON reports, client coverage contains only approved classes/bounded counts, and `competitive_proof` does not pass from poisoned evidence. Validation: internal/eval and cmd/idiom-eval tests, CLI text/JSON smokes, privacy scan, broad Go tests where feasible, `harness_audit`, `context_status`, SQL graph-view current/stale/non-current checks, `git diff --check`, and ledger/work-package consistency. | P4.6 review NO-GO closes; competitive proof becomes safe for regulated and private-code buyers. | Revert only the classification constraint if it breaks existing approved-class fixtures, and keep P4.6 unreviewed plus P5 blocked until competitive reports cannot export arbitrary customer labels. | Implementer/reviewer: see MVP Task Ledger row. | Class: all approved classifications remain represented while unknown/customer-like labels are kept out of local evidence exports; Product: restores competitive proof, security/privacy, local product-evidence export, client-demo proof, and packaging/support confidence. |

### Phase 5 Work Packages

| ID and name | Story | Control requirements | Acceptance and validation | Metric or audit signal | Rollback or disable path | Subagents | Client-classification and product-success impact |
| --- | --- | --- | --- | --- | --- | --- | --- |
| P5.1: Define and parse the durable plan artifact schema | As an executor, I need planner output to be parseable and auditable. | Schema must include task, contract, packet, paths, evidence, questions, verification, approval, and handoff fields. | Plan artifacts parse and validate; validation: cmd/agent/workspace tests and representative suite. | Parse success, schema error, and verification-obligation fields appear. | Accept legacy prose plans while requiring blocker if schema parsing fails. | Implementer/reviewer: see MVP Task Ledger row. | Class: all categories get auditable handoffs; Product: improves integration proof, security/privacy, and local evidence. |
| P5.5: Sanitize plan edit metadata and refresh graph views after durable schema changes | As a security/privacy reviewer, I need durable plan artifacts to preserve executor-required edit content while preventing edit metadata from leaking raw prompts, transcripts, secrets, or customer-like labels. | `PlanEdit.Purpose` and any similar edit metadata must pass through the plan artifact privacy boundary before persistence for both structured and legacy `submit_plan` paths. Sensitive markers such as `SECRET`, `raw_prompt`, `transcript`, and `customer-acme` must be rejected or replaced with bounded sanitized values in `.idiom/plans/last.plan.json`, schema errors, and CLI output. Terminal validation must also remove stale/non-current derived `graph_views` rows and regenerate current view rows after the final source edits. | Regression tests submit valid structured and legacy artifacts with sensitive `edits[0].purpose` metadata and prove persisted artifacts and outputs do not contain the raw markers while executable edit bodies/refs needed by the executor remain intact. Validation: workspace/cmd/agent tests, adversarial structured `submit_plan` smoke, privacy scan, broad Go tests where feasible, `harness_audit`, `context_status`, SQL graph-view current/stale/non-current checks, `git diff --check`, and ledger/work-package consistency. | P5.1 review NO-GO closes; durable plan artifacts become safe enough for P5.2 audit hardening. | Revert only the edit-metadata sanitization if it breaks executor-required edit contracts, and keep P5.1 unreviewed plus P5.2 blocked until edit metadata is privacy-safe and terminal graph-view freshness is clean. | Implementer/reviewer: see MVP Task Ledger row. | Class: finance, healthcare/life sciences, public-sector, enterprise SaaS, tooling-vendor, local-model/private-code, and open-source users get privacy-safe auditable handoffs; Product: restores security/privacy, integration proof, packaging/support, retained use, and local product-evidence export confidence. |
| P5.6: Broaden plan edit-purpose privacy boundary for standalone token, customer, and private-path metadata | As a privacy reviewer, I need plan edit metadata to reject broader sensitive prose, not only exact marker strings. | `PlanEdit.Purpose` sanitization must catch standalone token/customer phrasing and absolute/private local paths such as `rotate session token ... customer acme ... /Users/ben/private/client/repo`, for both structured and legacy submissions. The raw phrase, normalized variants, and private path must not persist in `.idiom/plans/last.plan.json`, schema errors, or CLI output. Terminal validation must end with fresh `context_status` and zero stale/non-current `graph_views` rows. | Regression tests and legacy/structured CLI smokes inject `rotate session token ... customer acme ... /Users/ben/private/client/repo` and prove persisted artifacts/output contain only bounded sanitized metadata while executor-required edit content/refs remain intact. Validation: workspace/cmd/agent tests, broad Go tests where feasible, privacy scan, `harness_audit`, `context_status`, SQL graph-view current/stale/non-current checks, `git diff --check`, and ledger/work-package consistency. | P5.5 review NO-GO closes; P5.1 can be re-reviewed as a privacy-safe durable schema. | Revert only the broader purpose sanitizer if it breaks executor-required edit contracts, and keep P5.1/P5.5 unreviewed plus P5.2 blocked until broader sensitive metadata cannot leak. | Implementer/reviewer: see MVP Task Ledger row. | Class: finance, healthcare/life sciences, public-sector, enterprise SaaS, tooling-vendor, local-model/private-code, and open-source users get privacy-safe plan metadata; Product: restores security/privacy, local product-evidence export, integration proof, retained use, and packaging/support confidence. |
| P5.2: Harden `submit_plan`, plan audit, and replay compare around packet and scope evidence | As a reviewer, I need submitted plans checked against packet and scope evidence. | Plan audit must compare packet, scope, evidence, and unresolved questions without host-authored strategy. | `submit_plan` and replay compare expose scope or packet mismatches; validation: cmd/agent/workspace tests and representative suite. | Plan-audit mismatch and replay-compare metrics become visible. | Keep hardening report-only and preserve prior plan submission path. | Implementer/reviewer: see MVP Task Ledger row. | Class: enterprise SaaS, public-sector, and tooling-vendor auditability improves; Product: improves security/privacy and integration proof. |
| P5.7: Block handoff-ready plan artifacts when required approval is unsatisfied | As a governance reviewer, I need a plan with required approval still pending to be non-dispatchable even if the handoff says it is ready. | Workspace `submit_plan` audit and `architect plan-audit --plan-artifact` must treat `approval.required=true`, unsatisfied approval status, and `handoff.ready=true` as a blocking error, not a warning. The output should expose a bounded `approval_handoff_mismatch` signal without leaking raw plan metadata. Legacy report-only compatibility should remain only where it cannot dispatch execution. | Regression tests and CLI smokes submit a structured artifact with `approval.required=true`, `approval.status=pending`, and `handoff.ready=true`; `submit_plan` must reject/block dispatch with `audit_ok=false`, and architect plan-audit must return non-OK or blocking status while preserving sanitized mismatch metrics. Validation: workspace/architect/cmd/agent tests, broad Go tests where feasible, privacy scan, `harness_audit`, `context_status`, SQL graph-view current/stale/non-current checks, `git diff --check`, and ledger/work-package consistency. | P5.2 review NO-GO closes; P5.3 can rely on handoff state as a contract signal. | Revert only the approval/handoff blocking severity change if it breaks legacy non-dispatch plan reporting, and keep P5.2 unreviewed plus P5.3 blocked until pending required approval cannot dispatch. | Implementer/reviewer: see MVP Task Ledger row. | Class: finance, healthcare/life sciences, public-sector, enterprise SaaS, tooling-vendor, local-model/private-code, and open-source users get enforceable approval semantics; Product: restores security/privacy, integration proof, retained use, and local evidence confidence. |
| P5.8: Prevent rejected approval-gated plans from persisting spilled edit assets | As a privacy reviewer, I need rejected approval-gated plans to leave no private edit-body artifacts behind. | `submit_plan` must audit approval/handoff and other blocking conditions before spilling large edit bodies, or clean up any spill assets on rejection. A plan with `approval.required=true`, `approval.status=pending`, `handoff.ready=true`, and large sensitive `new_text` must reject with `audit_ok=false` and leave no `.idiom/plans/assets/*new_text.txt` or equivalent private spill file. Successful dispatchable artifacts may still spill executor-required edit bodies as part of the contract. | Regression tests and CLI smokes submit the pending-approval large-edit case and prove no `last.plan.json` or spilled asset containing the sensitive body remains after rejection; normal accepted large-edit behavior still works. Validation: workspace/cmd/architect/agent tests, broad Go tests where feasible, privacy scan of `.idiom/plans`, `harness_audit`, `context_status`, SQL graph-view current/stale/non-current checks, `git diff --check`, and ledger/work-package consistency. | P5.7 review NO-GO closes; P5.2/P5.7 can be marked reviewed and committed. | Revert only the pre-spill audit/cleanup change if it breaks accepted large-edit execution, and keep P5.2/P5.7 unreviewed plus P5.3 blocked until rejected plans cannot leak spill assets. | Implementer/reviewer: see MVP Task Ledger row. | Class: finance, healthcare/life sciences, public-sector, enterprise SaaS, tooling-vendor, local-model/private-code, and open-source users get privacy-safe rejected plan handling; Product: restores security/privacy, local product-evidence export, integration proof, and retained use confidence. |
| P5.3: Emit executor handoff events for inherited, missing, stale, and overridden packets | As an orchestrator, I need to know whether execution inherited valid planning context. | Handoff events must distinguish inherited, missing, stale, and overridden packet states. | Executor emits handoff events and audit can report packet state; validation: cmd/agent/workspace tests and representative suite. | Executor packet-miss and stale-packet audit signals appear. | Disable handoff event emission while keeping plan artifact parsing. | Implementer/reviewer: see MVP Task Ledger row. | Class: all categories gain context provenance; Product: improves integration proof and local evidence. |
| P5.9: Classify stale graph-view executor packets without legacy view-file fallback | As an executor reviewer, I need stale graph-backed packet evidence to be reported as stale even when no legacy `.idiom/comprehension/views/<pack>.json` file exists. | `classifyExecutorHandoffPacket` or its context loader must distinguish stale/non-current graph-view rows from true cache misses. A stale graph-view row must emit `packet_state=stale` with bounded reason/evidence, not `missing/cache_miss`, without relying on legacy view JSON files. Terminal validation must end with fresh `context_status` and zero stale/non-current `graph_views` rows after final source edits. | Regression tests create stale or non-current graph-view rows with no legacy view file and prove executor handoff emits `stale`; true misses still emit `missing`; inherited/overridden cases remain intact. Validation: cmd/internal comprehension/workspace/architect tests, broad Go tests where feasible, privacy scan, `harness_audit`, `context_status`, SQL graph-view current/stale/non-current checks, `git diff --check`, and ledger/work-package consistency. | P5.3 review NO-GO closes; P5.4 can build on accurate packet-state provenance. | Revert only stale graph-row classification if it breaks valid inherited/missing/overridden behavior, and keep P5.3 unreviewed plus P5.4 blocked until stale graph-backed packets are not collapsed to missing. | Implementer/reviewer: see MVP Task Ledger row. | Class: all client classifications get accurate context provenance; Product: restores integration proof, retained use, local evidence export, and security/privacy confidence. |
| P5.10: Bound `context_status` stale samples and unscoped refresh attempts | As an operator, I need stale-context reports to stay readable and bounded while preserving exact stale counts and avoiding unbounded refresh work. | `context_status` output must cap stale/missing path samples while preserving full count fields. With `refresh=true`, unscoped stale-file refresh attempts must honor an explicit/default budget and report `budget_exceeded` instead of doing unbounded repair. Documentation/tool schema text must describe the `limit`/budget semantics. | Tests create stale sets larger than the cap and prove counts are preserved, displayed samples are capped, and refresh reports a bounded budget blocker. Validation: workspace tests, cmd context flag/schema checks, broad Go tests where feasible, `harness_audit`, `context_status`, SQL graph-view current/stale/non-current checks, `git diff --check`, and ledger/work-package consistency. | Context freshness evidence stays usable for P5.4 and later readiness work without runaway refresh behavior. | Revert only the display/refresh budget cap if it breaks context_status compatibility, and keep P5.4 blocked until stale reports are bounded and exact counts remain available. | Implementer/reviewer: see MVP Task Ledger row. | Class: all client classifications get bounded local evidence exports; Product: improves retained use, integration proof, packaging/support, and local product-evidence export confidence. |
| P5.4: Add approval scope IDs, handoff IDs, partial approval semantics, and eval assertions for plan-to-execution drift | As a platform lead, I need approvals and execution drift tracked explicitly. | Approval and handoff IDs must be stable and drift assertions must protect mutable work. | Partial approval and drift checks are represented in tests/evals; validation: cmd/agent/workspace tests and representative suite. | Plan-to-execution drift and approval-scope audit metrics appear. | Treat approval IDs as report-only and block write-profile expansion until restored. | Implementer/reviewer: see MVP Task Ledger row. | Class: finance, healthcare, public-sector, and enterprise SaaS governance improves; Product: improves security/privacy and integration proof. |
| P5.11: Refresh terminal graph-view cache after P5.4 drift assertions | As a release reviewer, I need Phase 5 to end with current regenerated graph-view rows, not stale or non-current derived cache rows created during review. | Treat `graph_views` rows and `.idiom/comprehension/views/*.json` as derived pre-production data. Delete stale/non-current view rows/artifacts, refresh the context index after final P5.4 source edits, regenerate representative current view rows, and make the last validation checks `context_status` plus SQL stale/non-current counts. Do not delete source, docs, ranker logs, runtime traces, telemetry logs, test fixtures, or unrelated evidence. | Terminal state is `context_status.fresh=true`, `stale_path_count=0`, zero stale `graph_views`, zero non-current-index `graph_views`, and repeated same-task `context_pack` proves a current cache hit. Validation: targeted context refresh/index commands, SQL graph-view checks, `harness_audit`, focused P5.4 tests or smokes, `git diff --check`, and ledger/work-package consistency. | P5.4 review's residual stale/non-current cache state closes; Phase 6 can start on a clean derived evidence cache. | If refresh fails, rebuild the comprehension index from current source and keep P5.4 unreviewed plus P6.1 blocked until terminal freshness is clean. | Implementer/reviewer: see MVP Task Ledger row. | Class: all client classifications get trustworthy local evidence export state; Product: restores retained use, integration proof, security/privacy, packaging/support, and local product-evidence export confidence. |
| P5.12: Final post-validation context refresh for P5.4 plan-execute edits | As a release reviewer, I need the final Phase 5 validation sequence to end after test-induced file touches with a fresh context index and current graph-view rows. | After the P5.11 validation suite, refresh the context index for stale `cmd/idiom/plan_execute.go` and `cmd/idiom/plan_execute_test.go` or rebuild the index if needed, then regenerate representative graph-view rows. The final commands in the sequence must be `context_status`, repeated same-task `context_pack`, and SQL checks showing no stale/non-current rows. Do not delete source, docs, ranker logs, runtime traces, telemetry logs, test fixtures, or unrelated evidence. | Terminal state after all validation is `context_status.fresh=true`, `stale_path_count=0`, zero stale `graph_views`, zero non-current-index `graph_views`, and repeated same-task `context_pack` hits current `graph_views`. Validation: targeted refresh/index commands, focused smoke if needed before refresh, final status/SQL/cache-hit checks, `git diff --check`, and ledger/work-package consistency. | P5.11 review NO-GO closes; Phase 5 can be committed and Phase 6 can proceed. | If final freshness cannot be achieved, keep P5.4/P5.11 unreviewed and block P6.1 until the index/cache can end clean after validation. | Implementer/reviewer: see MVP Task Ledger row. | Class: all client classifications get clean terminal evidence state; Product: restores retained use, integration proof, security/privacy, packaging/support, and local product-evidence export confidence. |
| P5.13: Constrain plan-execute source-satisfied clean-noop detection to explicit decisions | As an executor reviewer, I need plan-execute to accept source-satisfied no-op closeout only when the model makes an explicit source-satisfied decision, not when a blocker merely mentions that phrase in guidance text. | `PlanThenExecuteSourceSatisfiedAnswerCoversPaths` and its intent helper must classify only explicit `Decision: source-satisfied` / `Decision: source satisfied` answers as source-satisfied candidates. Question/blocker text that mentions source-satisfied as an option must continue to fail missing-plan-artifact checks. | Regression tests prove an explicit source-satisfied no-op remains accepted and a `<<QUESTION>>:` blocker mentioning source-satisfied fails as missing artifact. Validation: focused agent/cmd tests, `go test ./cmd/idiom -count=1`, `go test ./internal/agent -run PlanThenExecute -count=1`, `git diff --check`, and ledger/work-package consistency. | Prevents false clean-noop closeout in plan-execute, preserving plan artifact and blocker contracts. | If explicit source-satisfied parsing breaks valid already-complete cases, revert to report-only detection and keep plan-execute no-op closeout blocked until explicit decisions are safe. | Implementer/reviewer: see MVP Task Ledger row. | Class: all client classifications get stricter auditability for mutation/no-op decisions; Product: improves security/privacy, integration proof, retained use, and local product-evidence export confidence. |
| P5.14: Align plan-execute new-file body guidance and preflight enforcement | As a plan-execute operator, I need missing/new source-file tasks to either carry a full planned file body or be rejected before dispatch, so the planner does not loop on reads or ask for bodies it can synthesize only in an implementation lane. | Plan-execute prompt/recovery text must describe the accepted whole-file/new-file `replace_all:true` shape. Brief preflight must reject plan-execute new source/test files without a substantial inline body or explicit replacement edit shape, while still allowing implementation-shape requirements in non-plan-execute lanes. New-file read-rewrite guidance must point at the same contract. | Tests cover prompt embedding, recovery/nudge text, new-file read rewrite, brief preflight rejection without inline body, and acceptance with inline body. Validation: focused cmd/agent lint tests, `go test ./cmd/idiom -count=1`, `go test ./internal/agent -run 'NewFile|PlanThenExecute|CompletionGate' -count=1`, `git diff --check`, and ledger/work-package consistency. | Prevents plan-execute atoms from launching with insufficient exact plan evidence and keeps new-file creation contracts auditable. | If preflight is too strict for legitimate plan-execute tasks, degrade to warning and keep P6 tasks unblocked only after review documents the accepted risk. | Implementer/reviewer: see MVP Task Ledger row. | Class: all client classifications get stricter exact-plan evidence; Product: improves activation reliability, integration proof, retained use, security/privacy, and local product-evidence export confidence. |
| P5.15: Remove plan-execute host auto-submit and tighten plan-execute brief detection | As a plan-execute reviewer, I need P5.14 to enforce evidence boundaries without the harness synthesizing planner strategy or misclassifying implementation briefs. | Remove any host-side fallback that parses prompt fences and calls `submit_plan` after the planner failed to produce an artifact. Plan-execute missing-artifact behavior must remain a failure/blocker unless the model explicitly submitted a valid plan or made an accepted source-satisfied decision. Preflight plan-execute detection must require explicit plan-execute routing or canonical lane markers; a non-plan-execute implementation brief must not become plan-execute merely because prose mentions `submit_plan`. | Regression tests prove missing-artifact plan-execute with inline body still fails rather than auto-submits, implementation-lane briefs mentioning `submit_plan` are not subject to the plan-execute new-file body gate, and explicit plan-execute new-file briefs still fail/pass according to body evidence. Validation: focused cmd/agent lint and plan-execute tests, `go test ./cmd/idiom -count=1`, `go test ./internal/agent -run 'NewFile|PlanThenExecute|CompletionGate|ReadyToSubmit' -count=1`, `git diff --check`, and ledger/work-package consistency. | P5.14 review NO-GO closes; exact-plan evidence enforcement aligns with the north star and P6 can continue from a clean branch. | If plan-execute detection cannot be made precise enough, keep P5.14 unreviewed and disable the stricter preflight gate until explicit routing metadata is available. | Implementer/reviewer: see MVP Task Ledger row. | Class: all client classifications retain strict exact-plan evidence without false implementation-lane blockers; Product: restores activation reliability, integration proof, retained use, security/privacy, and local product-evidence export confidence. |
| P5.16: Reject atom/refine shorthand paths that could authorize unintended root writes | As a scope reviewer, I need atom/refine intake to distinguish concrete repo-relative paths from shorthand labels, prose pairs, and command snippets so missing-file scope cannot authorize unintended root writes. | Atom intake must reject missing bare basename shorthand unless it is a known allowed basename. Refine target extraction must reject slash-separated prose labels and command examples while preserving concrete repo-relative source/docs paths. Rules must be generic path-shape checks, not project-specific labels. | Regression tests cover bare basename rejection with a concrete nested new file still allowed, slash-separated prose/command rejection, and valid source/docs path extraction. Validation: focused atom/refine tests, `go test ./cmd/idiom -run 'Atom|Refine|Deliverable|Scope' -count=1`, `go test ./cmd/idiom -count=1`, `git diff --check`, and ledger/work-package consistency. | Scope false positives decline; atom/refine intake no longer broadens write authority from ambiguous labels. | If false positives block legitimate single-file targets, keep a small reviewed allowlist of known basenames and require explicit nested paths for new files until richer path evidence is available. | Implementer/reviewer: see MVP Task Ledger row. | Class: all client classifications get stricter protected-scope evidence; Product: improves security/privacy, integration proof, retained use, and local product-evidence export confidence. |
| P5.17: Report ambiguous atom shorthand paths with coherent invalid-scope reasons | As an atom reviewer, I need invalid-scope reports to distinguish outside-root paths from ambiguous bare shorthand paths, so scope denials are auditable and actionable. | Atom intake reports must include a bounded reason for invalid paths that are rejected because they are missing bare basename shorthand. Existing outside-project-root invalid path behavior must keep its own reason. The report must not imply a repo-relative shorthand path is outside the project root. | Regression tests assert ambiguous shorthand invalid paths emit a clear shorthand/ambiguous-root-write reason, outside-root paths still report outside-root, and P5.16 accept/reject cases remain intact. Validation: focused atom/refine tests, `go test ./cmd/idiom -run 'Atom|Refine|Deliverable|Scope' -count=1`, `go test ./cmd/idiom -count=1`, `git diff --check`, and ledger/work-package consistency. | P5.16 review NO-GO closes; invalid scope reports become accurate enough for reviewers and release evidence. | If per-path reasons require a schema change that is too large, keep the older state but change the summary/remedy to describe both possible invalid-scope causes without mislabeling shorthand as outside-root. | Implementer/reviewer: see MVP Task Ledger row. | Class: all client classifications get clearer scope-denial evidence; Product: restores security/privacy, integration proof, retained use, and local product-evidence export confidence. |
| P5.18: Reject no-op `submit_plan` edits before artifact persistence | As a plan-execute reviewer, I need planner-submitted no-op edits to fail before durable artifacts are written, so clean-noop/source-satisfied decisions remain explicit and auditable. | `submit_plan` validation must reject substring, range, and whole-file edit shapes that would not change the target file before writing `last.plan.json` or spilled edit assets. Error text must direct callers to an explicit `Decision: source-satisfied` or review `Decision: pass` path rather than accepting placeholder edits. The check must preserve valid deletions, valid new files, valid replacements, and content-ref behavior. | Regression tests prove no-op substring and range edits are rejected before artifact persistence; accepted edit-shape tests continue to pass. Validation: focused `submit_plan` tests, `go test ./internal/workspace -count=1`, `go test ./cmd/idiom -count=1`, `git diff --check`, and ledger/work-package consistency. | No-op plan-artifact writes become measurable failures instead of dispatchable fake edits. | If no-op detection blocks a valid generated-edit flow, keep the preflight error but narrow only the affected edit shape; do not allow dispatchable no-op artifacts. | Implementer/reviewer: see MVP Task Ledger row. | Class: all client classifications get stricter auditability for mutation/no-op decisions; Product: improves security/privacy, retained use, integration proof, and local product-evidence export confidence. |
| P5.19: Resolve `context_status` default refresh-budget drift | As an operator, I need P5.10's bounded refresh contract to remain true after follow-up work: unscoped `context_status refresh=true` must not silently become an unbounded workspace refresh. | Restore or preserve a default max-files budget for indexed stale graphs when no bounded path or required-edit scope is provided. Explicit `freshness_max_files` or `limit` may override that budget. Small stale sets within budget should still refresh; stale sets over budget must report `budget_exceeded` with counts and no unbounded repair. Tool schema/docs must state explicit/default budget semantics consistently. | Regression tests prove `contextStatusRefreshMaxFiles` returns the default budget for unscoped indexed stale status, explicit `limit`/`freshness_max_files` override it, bounded path/required-edit scopes remain uncapped exact refreshes, and budget-exceeded behavior remains covered. Validation: focused context-status tests, `go test ./internal/workspace -count=1`, `go test ./cmd/idiom -count=1`, `git diff --check`, and ledger/work-package consistency. | P5.10's bounded-refresh invariant remains enforceable before Phase 7 sidecar exposure. | If default budgeting breaks a legitimate local repair path, keep the default cap and require callers to pass an explicit larger `freshness_max_files`; do not switch defaults to unbounded without a project-approved plan change. | Implementer/reviewer: see MVP Task Ledger row. | Class: all client classifications keep bounded local evidence refresh behavior; finance, healthcare, public-sector, enterprise SaaS, and tooling-vendor benefit directly from predictable side-effect/performance controls. Product: restores security/privacy, retained use, integration proof, packaging/support, and local product-evidence confidence. |
| P5.20: Restore default `context_status` stale-file budget after P5.19 review | As a reviewer, I need the committed P5.19 change to stop encoding the uncapped default path and to prove the default stale-file budget with tests that fail on regression. | `contextStatusRefreshMaxFiles` must return the default max-files budget for unscoped indexed stale graphs when no bounded path, required-edit path, `freshness_max_files`, or `limit` is present. Existing explicit overrides still win, and bounded scopes/collector-mismatch/missing-index paths remain distinct. Rewrite any tests asserting uncapped default behavior so large unscoped stale sets produce `budget_exceeded`; do not approve unrelated refresh-scope batching as a substitute for the budget. | Regression tests prove default unscoped stale sets above the default cap are not refreshed and report `budget_exceeded`, `stale_path_count`, and `max_files`; small sets within the default still refresh; explicit `limit`/`freshness_max_files` behavior remains covered. Validation: focused context-status tests, `go test ./internal/workspace -count=1`, `go test ./internal/comprehension -count=1` if freshness batching remains in the diff, `go test ./cmd/idiom -count=1`, `git diff --check`, and ledger/work-package consistency. | P5.19 review NO-GO closes; P5.10 bounded-refresh contract is restored before Phase 7 sidecar work resumes. | If refresh-scope batching is valuable, leave it only behind the same default budget; if it prevents clear budget evidence, narrow or defer it to a separate reviewed task. | Implementer/reviewer: see MVP Task Ledger row. | Class: all client classifications regain bounded local side-effect and performance guarantees; regulated and enterprise rows benefit directly. Product: restores security/privacy, retained use, integration proof, packaging/support, and local product-evidence confidence. |
| P5.21: Re-apply `context_status` default stale-file budget after concurrent dirty regression | As an orchestrator, I need the worktree to preserve the reviewed P5.20 bounded-refresh contract while P7.1 sidecar work continues. | Restore the P5.20 constant, default return path, and regression-test expectations without touching P7.1 sidecar files or unrelated comprehension SQLite-recovery files. `contextStatusRefreshMaxFiles` must again return the default stale-file budget for unscoped indexed stale graphs, while explicit caps, bounded scopes, missing-index, and collector-mismatch behavior remain unchanged. Tests must not assert uncapped default behavior. | Focused tests compile and pass, especially the P5.20 default-budget tests that currently reference `defaultContextStatusUnboundedRefreshMaxFiles` and large default stale sets; validation: focused context-status tests, `go test ./internal/workspace -count=1` if unrelated sidecar changes allow it, `git diff --check`, and ledger consistency. | P7.1 validation is no longer blocked by a missing default-budget symbol or stale uncapped tests. | If another concurrent task intends to replace the budget contract, record a new no-go/pivot; otherwise preserve P5.20 exactly. | Implementer/reviewer: see MVP Task Ledger row. | Class: all client classifications retain bounded local side-effect and performance guarantees; Product: preserves security/privacy, retained use, integration proof, and local evidence confidence while Phase 7 proceeds. |

### Phase 6 Work Packages

| ID and name | Story | Control requirements | Acceptance and validation | Metric or audit signal | Rollback or disable path | Subagents | Client-classification and product-success impact |
| --- | --- | --- | --- | --- | --- | --- | --- |
| P6.1: Extend `idiom doctor` with readiness profiles | As a local operator, I need readiness described by workflow profile before launch. | Doctor output must be structured and profile-aware. | Readiness profiles exist for discovery, plan, implement, eval, and sidecar; validation: cmd/backend/app/workerruntime tests and `idiom doctor --json`. | Readiness profile fields and blocked/degraded states appear. | Keep old doctor output and mark profile readiness unavailable. | Implementer/reviewer: see MVP Task Ledger row. | Class: local-model and open-source users benefit directly, with healthcare and public-sector local-use support; Product: improves activation, retained use, and local evidence. |
| P6.2: Add backend, queue, context budget, route classifier, implement-session closeout, and degraded-mode telemetry | As a local-model operator, I need capacity and degraded state visible. | Telemetry must avoid raw prompt/file bodies and report backend, queue, budget, classifier, implement-session closeout, and degradation. | Lifecycle and capacity telemetry emit in structured form; every implement conversation reaches exactly one `session_summary` on normal close, empty startup close, or handled signal closeout; validation: backend/app/workerruntime/telemetry tests and doctor JSON. | Queue wait, context budget, backend readiness, implement lifecycle-closeout coverage, and degraded-mode metrics appear. | Disable telemetry fields or keep them local/report-only; if signal closeout proves unsafe, keep normal-close summaries and mark signal summaries as a P6.2 blocker. | Implementer/reviewer: see MVP Task Ledger row. | Class: local-model, enterprise SaaS, and public-sector local operations improve; Product: improves activation, retained use, and local evidence. |
| P6.6: Make implement queue telemetry unavailable states and doctor command attribution explicit | As an operator, I need implement queue telemetry to distinguish real queue acquisition from missing or unreachable queue infrastructure, and I need doctor telemetry to be attributed to the doctor command. | Implement queue telemetry must not report an unavailable/no-op queue as acquired or non-degraded. Queue states must use explicit bounded values such as acquired, unavailable, disabled, degraded, or error. `doctor` telemetry command attribution must not fall through to `tui`. | Tests prove an unreachable queue path emits explicit unavailable/degraded metadata, acquired remains reserved for real acquisition, no duplicate closeout summaries are introduced, and doctor telemetry uses `command=doctor`. Validation: focused cmd implement/doctor telemetry tests, `go test ./cmd/idiom -count=1`, relevant workspace/eval telemetry tests, `git diff --check`, and ledger/work-package consistency. | P6.2 review NO-GO closes; queue/degraded telemetry becomes trustworthy enough for P6.3 soft/hard gates. | If true queue acquisition cannot be distinguished through the current runner API, add a typed result or report the queue check as unavailable/degraded until the API is fixed; keep P6.2 unreviewed and P6.3 blocked. | Implementer/reviewer: see MVP Task Ledger row. | Class: all client classifications get stricter local-ops evidence; local-model/private-code, enterprise SaaS, and public-sector users benefit directly. Product: restores retained use, integration proof, security/privacy, packaging/support, and local product-evidence export confidence. |
| P6.3: Define soft and hard readiness gates by profile | As a runner, I need unsafe starts blocked and degraded starts explained. | Gates must distinguish soft degradation from hard block by profile. | Readiness gates produce machine-readable pass/degraded/blocked reasons; validation: backend/app/workerruntime tests and doctor JSON. | Blocked/degraded readiness counts and remedy fields become visible. | Set gates to report-only for profiles until reviewed. | Implementer/reviewer: see MVP Task Ledger row. | Class: local-model, public-sector, and healthcare safety improves; Product: improves activation, security/privacy, and retained use. |
| P6.4: Add lane-start role, backend, queue, classifier, and context telemetry parity tests | As a maintainer, I need lane-start telemetry to match doctor readiness. | Tests must assert role/backend/queue/classifier/context parity without widening telemetry retention. | Parity tests pass for lane-start readiness fields; validation: backend/app/workerruntime tests. | Telemetry parity failures become catchable. | Disable parity assertion while retaining visible doctor output. | Implementer/reviewer: see MVP Task Ledger row. | Class: local-model and enterprise SaaS operations improve; Product: improves retained use and local evidence. |
| P6.5: Add local-operations smoke, capacity, and replay benchmark harness | As a release reviewer, I need repeatable local readiness and replay evidence. | Harness must run locally, avoid raw prompt/file capture, and show capacity/replay limits. | Smoke, capacity, and replay outputs are generated for supported local profiles; validation: backend/app/workerruntime tests and benchmark harness. | Local smoke pass rate, capacity, and replay metrics appear. | Mark local reliability claims unsupported and skip harness gate with blocker. | Implementer/reviewer: see MVP Task Ledger row. | Class: local-model, public-sector, healthcare, and open-source stories improve; Product: improves activation, retained use, and local evidence. |
| P6.7: Sanitize local-ops replay export and normalize invalid-profile exits | As a release reviewer, I need local-ops benchmark replay output to remain metadata-only even when user-supplied log paths contain customer strings, and I need invalid profile selection to follow CLI usage-error conventions. | Replay blockers and summaries must not echo raw replay paths, basenames, `open ...` errors, or `eval.PerfAggregate` path-bearing worst-offender fields. Replace raw replay identifiers with bounded counts, hashes, or redacted IDs. Invalid `--profile` must return usage/argument exit code 2 while preserving normal runtime/report errors as code 1. | Regression tests prove a replay path such as `/tmp/AcmeCustomer-secret-session.jsonl` does not appear in JSON/stdout/stderr/telemetry, path-bearing aggregate fields are redacted or omitted, valid replay summaries still expose bounded metrics, and invalid profile exits 2. Validation: focused local-ops tests, temp binary local-ops JSON smokes, `go test ./cmd/idiom -count=1`, relevant eval tests, `git diff --check`, and ledger/work-package consistency. | P6.5 review NO-GO closes; Phase 6 can close with privacy-safe local evidence export. | If replay sanitization cannot preserve useful aggregate detail, omit replay aggregate details and expose only counts/status/blockers until P7/P9 product-evidence export adds a reviewed redaction layer. | Implementer/reviewer: see MVP Task Ledger row. | Class: all client classifications get privacy-safe local evidence export; healthcare, public-sector, finance, and local-model/private-code benefit directly. Product: restores security/privacy, activation, retained use, packaging/support, and local product-evidence export confidence. |

### Phase 7 Work Packages

| ID and name | Story | Control requirements | Acceptance and validation | Metric or audit signal | Rollback or disable path | Subagents | Client-classification and product-success impact |
| --- | --- | --- | --- | --- | --- | --- | --- |
| P7.1: Ship the stable structured CLI envelope for `idiom tool` results | As a script or agent, I need stable machine-readable sidecar output. | Envelope must include schema version, tool, profile, contract, freshness, warnings, audit IDs, and structured errors. | CLI outputs use the stable envelope for sidecar-ready tools; validation: cmd/workspace tests and tool-surface audit. | Parse-success, error-class, freshness, and audit-ID coverage rise. | Keep legacy envelope compatibility and gate v1 exposure. | Implementer/reviewer: see MVP Task Ledger row. | Class: tooling-vendor, enterprise SaaS, and open-source integration improves; Product: directly improves integration proof and activation. |
| P7.2: Implement read-only `--profile discovery` filtering | As an external agent caller, I need discovery without write or shell authority. | Discovery profile must default-deny mutation, shell, destructive, protected-path, and open-world effects. | Profile filtering hides disallowed tools and reports denials; validation: cmd/workspace tests and sidecar smoke. | Denial events and visible tool count by profile become auditable. | Disable profile flag or make discovery unavailable until containment passes. | Implementer/reviewer: see MVP Task Ledger row. | Class: finance, healthcare, public-sector, enterprise SaaS, and tooling-vendor safety improves; Product: improves security/privacy and integration proof. |
| P7.12: Block refresh-capable discovery-profile requests before execution | As a security reviewer, I need the discovery profile to be request-aware, so tools that are generally read-only cannot trigger graph rebuilds, cache writes, shell-like execution, or other additive effects through specific input flags. | `idiom tool <name> --profile discovery` must deny request shapes that can write local state before `workspace.Manager.Execute` runs. At minimum, graph/context tools with `refresh:true`, request-selected read-through or require-fresh refresh behavior that can build/patch graph state, context indexing/building tools, trace pack, and other known additive request flags must be blocked or forced into report-only/no-refresh semantics through an explicit reviewed path. Denials must return structured sidecar errors with `profile_denied`/`read_only_profile` and must not create `.idiom/comprehension` artifacts. | Regression tests prove `context_status --profile discovery --input '{"refresh":true}'` exits non-zero, emits structured denial, and writes no graph/cache files; allowed no-refresh/report-only discovery calls still work with `sidecar.profile=discovery`; existing mutation/shell denials still pass; validation: cmd/workspace profile tests, temp binary allowed/denied smokes, and `git diff --check`. | P7.2 review NO-GO closes; discovery profile becomes request-aware instead of only tool-name-aware. | If request-aware containment cannot be made safe, disable discovery profile for all graph-backed tools until P7.5 containment is complete. | Implementer/reviewer: see MVP Task Ledger row. | Class: finance, healthcare, public-sector, enterprise SaaS, tooling-vendor, local-model/private-code, and open-source users get read-only sidecar containment; Product: restores security/privacy, integration proof, activation, packaging/support, and local product-evidence confidence. |
| P7.3: Add external caller recipes and at least one sidecar smoke transcript | As an external-agent user, I need recipes that work without Idiom internals. | Recipes must use structured CLI contracts, not prompt interpolation. | At least one smoke transcript and caller recipe are runnable; validation: cmd/workspace tests and context-pack smoke. | Sidecar-smoke pass and recipe reproducibility metrics appear. | Mark recipes experimental and remove unsupported claims. | Implementer/reviewer: see MVP Task Ledger row. | Class: open-source, enterprise SaaS, tooling-vendor, and local-model users get activation paths; Product: improves activation and integration proof. |
| P7.13: Add executable validation for sidecar recipe commands | As an integration reviewer, I need every unique sidecar recipe command to be executable evidence, not only documentation text. | Tests must run the external-harness `context_query` recipe and the local/private-code default-profile `context_pack` recipe in a temp workspace/config, while preserving the discovery-profile denial semantics and clearly treating `context_pack` as a local artifact-writing default-profile path. | Regression tests replay the documented transcript plus the unique `context_query` and `context_pack` recipes; validation: focused sidecar tests, cmd/workspace tests, temp binary or live smokes, and `git diff --check`. | P7.3 review NO-GO closes; recipe reproducibility covers all documented caller paths. | Remove or mark any recipe experimental if it cannot be made executable without relying on unstable local state. | Implementer/reviewer: see MVP Task Ledger row. | Class: enterprise SaaS, tooling-vendor, local-model/private-code, open-source, and regulated buyers get more reproducible integration evidence; Product: restores activation, integration proof, client demo proof, packaging/support, and local product-evidence confidence for P7.3. |
| P7.4: Add sidecar v1 API compatibility, caller metadata, error-class, stale-freshness, backend-fallback, and no-strategy-leakage golden tests | As an integration maintainer, I need compatibility and no strategy leakage locked down. | Golden tests must cover caller metadata, errors, freshness, fallback, and evidence-only outputs. | Compatibility golden tests pass and no-strategy-leakage assertions hold; validation: cmd/workspace tests and tool-surface audit. | API compatibility, caller attribution, and strategy-leakage findings become measurable. | Freeze v1 exposure and keep previous envelope while tests fail. | Implementer/reviewer: see MVP Task Ledger row. | Class: tooling-vendor, enterprise SaaS, public-sector, and finance integrations improve; Product: improves integration proof and security/privacy. |
| P7.14: Close P7.4 strategy-leakage and caller-metadata privacy gaps | As a security and integration reviewer, I need the public `idiom tool` JSON payload and caller metadata contract to match the privacy and no-strategy-leakage claims. | Golden tests must scan the complete public JSON payload, including legacy fields, for strategy-prescriptive leakage while allowing hard invariant denials. Caller metadata validation and docs must align: flags accept short opaque IDs and reject common secret-, token-, path-, and private-identifier-shaped values without claiming comprehensive secret detection. | Regression tests prove whole-payload no-strategy leakage checks, accepted opaque caller IDs, rejected token/path-shaped caller IDs, and doc wording aligned with enforcement; validation: focused sidecar tests, cmd/workspace tests, temp binary accepted/rejected caller smokes, and `git diff --check`. | P7.4 review NO-GO closes; caller attribution and public-payload leakage guarantees become reviewable. | Remove caller metadata flags or mark caller metadata experimental until the privacy contract passes. | Implementer/reviewer: see MVP Task Ledger row. | Class: finance, healthcare, public-sector, enterprise SaaS, tooling-vendor, local-model/private-code, and open-source users get safer integration metadata; Product: restores security/privacy, integration proof, packaging/support, and client demo confidence for P7.4. |
| P7.5: Add discovery-profile containment, protected-path, redaction, permission-denial, and external side-effect tests | As a security reviewer, I need discovery containment proven by tests. | Tests must assert protected-path denial, redaction, no side effects, and permission events through generic primitives. | Containment tests pass for discovery profile; validation: cmd/workspace tests and audit output. | Protected-path denial, redaction, and side-effect audit signals appear. | Disable discovery profile for external callers until containment passes. | Implementer/reviewer: see MVP Task Ledger row. | Class: finance, healthcare, public-sector, enterprise SaaS, and tooling-vendor examples improve; Product: directly improves security/privacy and client demo proof. |
| P7.6: Add CLI-side example tests for finance, healthcare, public-sector, enterprise SaaS, tooling-vendor, local-model, and open-source classification scenarios | As a product reviewer, I need every classification represented by executable CLI tests. | Tests must use overlays and shared primitives, not vertical-specific code branches. | One CLI example test per classification passes or has explicit no-go record; validation: classification tests and audit output. | Classification test coverage reaches seven categories. | Mark unsupported classifications as no-go/pivot inputs and block release. | Implementer/reviewer: see MVP Task Ledger row. | Class: directly covers all categories; Product: improves client demo proof, security/privacy, and ICP validation. |
| P7.7: Add install, quickstart, time-to-first-value, and clean-uninstall smoke coverage for CLI-only local use | As a new local user, I need first value and cleanup to be measurable. | Smoke must record setup, first useful packet, freshness check, audit report, suggested verification, product-evidence-safe output, failure reason, docs gap, privacy/redaction status, and cleanup using the P0.10 field names. | Clean local smoke produces time-to-first-packet, time-to-trust-evidence, and cleanup evidence; validation: cmd/workspace tests and quickstart smoke. | Activation time, failure reason, docs gap, privacy-safe export, assistance class, and cleanup metrics appear. | Remove quickstart claims and keep install instructions as experimental. | Implementer/reviewer: see MVP Task Ledger row. | Class: open-source, local-model, enterprise SaaS, and tooling-vendor activation improves; Product: directly improves activation and local evidence. |
| P7.8: Add three integration proof paths: shell/CI script, one external coding-agent workflow, and a placeholder handoff to MCP stdio | As a platform lead, I need proof Idiom is a substrate for other agents. | Proof paths must consume shared profile/schema/freshness/audit contracts. | Shell/CI, external-agent, and MCP handoff proof records exist; validation: sidecar smoke and integration transcript checks. | Integration proof count reaches three paths. | Drop unsupported path claims and record blocker or defer to MCP phase. | Implementer/reviewer: see MVP Task Ledger row. | Class: enterprise SaaS, tooling-vendor, open-source, and public-sector integration improves; Product: directly improves integration proof. |
| P7.9: Add security/privacy posture artifacts and automated checks for protected paths, redaction, retention, telemetry minimization, and side-effect policy | As a regulated buyer, I need posture artifacts backed by checks. | Checks must enforce protected paths, redaction, retention, telemetry minimization, and side-effect policy. | Posture artifacts and automated checks are reviewable; validation: cmd/workspace tests and security/privacy checks. | Security/privacy check pass rate and policy-denial audit signals appear. | Block regulated claims and disable affected profile until checks pass. | Implementer/reviewer: see MVP Task Ledger row. | Class: finance, healthcare, public-sector, and enterprise SaaS posture improves; Product: directly improves security/privacy and client demo proof. |
| P7.10: Add runnable client demo transcripts for every client classification with commands, expected output, value claim, evidence, and limitations | As a sales or reviewer user, I need demos that are executable and honest. | Transcripts must include commands, expected output, evidence, value claim, MVP limitation, and final-state expansion. | Seven demo transcripts are runnable and tied to generic primitives; validation: transcript smoke and docs diff. | Runnable demo count reaches all classifications. | Remove non-runnable demo claims and block release on missing category. | Implementer/reviewer: see MVP Task Ledger row. | Class: directly covers all categories; Product: directly improves client demo proof and ICP validation. |
| P7.11: Add local product-evidence export for activation, retained use, integration, benchmark, audit, and learning signals without raw prompt or file bodies | As a product owner, I need local summarized evidence for go/no-go decisions. | Export must exclude raw prompts, raw file bodies, and secrets by default. | Product-evidence export summarizes required signals and redaction policy; validation: product-evidence fixture and privacy check. | Export completeness and no-raw-body checks become measurable. | Disable export or mark it internal-only until privacy checks pass. | Implementer/reviewer: see MVP Task Ledger row. | Class: all categories get privacy-preserving product evidence; Product: directly improves local product-evidence export, activation, retained use, integration proof, competitive proof, and security/privacy. |

### Phase 8 Work Packages

| ID and name | Story | Control requirements | Acceptance and validation | Metric or audit signal | Rollback or disable path | Subagents | Client-classification and product-success impact |
| --- | --- | --- | --- | --- | --- | --- | --- |
| P8.1: Implement `idiom mcp serve --stdio --profile discovery` from the same schemas | As an MCP client, I need the same discovery profile over stdio. | MCP must use the shared schema/profile source and stay read-only. | MCP stdio server exposes discovery tools from the same schemas; validation: cmd/workspace tests and tool-surface audit. | CLI/MCP schema parity and stdio smoke signals appear. | Disable MCP serve command and keep CLI as canonical transport. | Implementer/reviewer: see MVP Task Ledger row. | Class: tooling-vendor, enterprise SaaS, and open-source integration improves; Product: improves integration proof and client demo proof. |
| P8.2: Add MCP resources, root clamping, and protocol/error tests | As a security reviewer, I need MCP roots and resources bounded to the project. | Resources must be redaction-ready, root-clamped, and covered by protocol/error tests. | initialize, tools/list, tools/call, resources/list, roots, and errors are tested; validation: cmd/workspace tests. | Root-clamp and protocol-error metrics become visible. | Disable resources or MCP server until clamping tests pass. | Implementer/reviewer: see MVP Task Ledger row. | Class: healthcare, public-sector, tooling-vendor, and finance resource safety improves; Product: improves security/privacy and integration proof. |
| P8.3: Add MCP client smoke tests and telemetry/caller attribution parity with CLI | As an integration owner, I need MCP and CLI calls attributed the same way. | MCP telemetry must match CLI caller attribution without raw prompt/file bodies. | Client smoke tests and telemetry parity assertions pass; validation: cmd/workspace tests and tool-surface audit. | Caller-attribution parity and MCP smoke metrics appear. | Keep MCP internal-only until parity is restored. | Implementer/reviewer: see MVP Task Ledger row. | Class: tooling-vendor, enterprise SaaS, and public-sector attribution improves; Product: improves integration proof and security/privacy. |
| P8.4: Add MCP resource redaction, protected-path containment, and stale-freshness parity tests | As a regulated MCP user, I need resource privacy and freshness matching CLI. | MCP resources must enforce redaction, protected paths, and stale-freshness semantics. | Parity tests pass for redaction, containment, and freshness; validation: cmd/workspace tests and audit output. | MCP redaction and stale-freshness parity findings decline. | Disable MCP resources or mark affected resources unavailable. | Implementer/reviewer: see MVP Task Ledger row. | Class: finance, healthcare, public-sector, and tooling-vendor safety improves; Product: improves security/privacy and integration proof. |
| P8.5: Add MCP parity coverage for any classification example that depends on resources, caller metadata, or redaction semantics | As a product reviewer, I need MCP-visible classification claims tested when MCP is part of the claim. | Classification parity must use generic primitives and document transport limitations. | Required classification examples have MCP parity tests or explicit not-applicable rationale; validation: MCP parity tests and classification report. | MCP classification parity coverage becomes measurable. | Remove MCP-specific classification claims until parity exists. | Implementer/reviewer: see MVP Task Ledger row. | Class: directly covers affected categories across MCP; Product: improves client demo proof and integration proof. |
| P8.6: Complete MCP stdio integration proof with protocol, caller metadata, roots, resources, redaction, and product-evidence export parity | As a platform buyer, I need one replayable MCP proof equivalent to CLI proof. | Integration proof must cover protocol, caller metadata, roots, resources, redaction, freshness, and evidence export parity. | MCP proof transcript is replayable and tied to release evidence; validation: MCP smoke, parity tests, and product-evidence export check. | MCP integration proof completeness reaches release threshold. | Defer MCP from release scope and keep CLI-only sidecar proof. | Implementer/reviewer: see MVP Task Ledger row. | Class: tooling-vendor, enterprise SaaS, finance, healthcare, public-sector, and open-source MCP stories improve; Product: directly improves integration proof, client demo proof, and local evidence. |

### Phase 9 Work Packages

| ID and name | Story | Control requirements | Acceptance and validation | Metric or audit signal | Rollback or disable path | Subagents | Client-classification and product-success impact |
| --- | --- | --- | --- | --- | --- | --- | --- |
| P9.1: Produce release evidence bundle from baseline/current metrics, tests, audit, benchmark, and sidecar smoke | As a release reviewer, I need one bundle comparing baseline to current evidence. | Bundle must include commands, outputs, blockers, and privacy-safe summaries. | Release evidence bundle is generated from required metrics, tests, audits, benchmark, and smoke; validation: full release validation commands. | Baseline/current deltas, test status, audit findings, benchmark results, and smoke outcomes are captured. | Hold release and regenerate bundle from recorded commands. | Implementer/reviewer: see MVP Task Ledger row. | Class: all categories get release evidence; Product: improves every product gate. |
| P9.2: Record project go/no-go/pivot decision against measured criteria | As a project owner, I need continuation decided from evidence. | Decision must cite measured criteria, unresolved blockers, and named pivot if needed. | Go/no-go/pivot decision is recorded with evidence links; validation: release evidence review and docs diff. | Decision completeness and unresolved-blocker count become explicit. | Reopen release review and mark decision provisional. | Implementer/reviewer: see MVP Task Ledger row. | Class: all categories are honestly included or deferred; Product: directly covers ICP, packaging/support, and local evidence. |
| P9.3: Gate release on no unresolved high contract/security regressions and owned high/medium audit findings with evidence and reproduce commands | As a security reviewer, I need critical findings closed or owned before release. | High regressions must be zero; medium/high findings need owner, status, evidence, and reproducer. | Gate passes only with no unresolved high contract/security regression; validation: release audit and reproduce-command review. | High regression count, owned finding rate, and reproducer coverage become release metrics. | Block release or pivot scope until findings are resolved or explicitly no-go. | Implementer/reviewer: see MVP Task Ledger row. | Class: finance, healthcare, public-sector, and enterprise SaaS safety improves; Product: directly improves security/privacy and local evidence. |
| P9.4: Gate release on complete client-classification example-test coverage and reviewer signoff that every example maps to generic architecture primitives | As a product reviewer, I need every classification proved through shared primitives. | Examples must pass or have no-go record, and reviewers must reject vertical-specific branches. | Classification report and reviewer signoff cover all categories; validation: classification tests and review-protocol evidence. | Classification coverage reaches seven categories with generic-primitive mapping. | Block release on missing/vertical-only category and create follow-up. | Implementer/reviewer: see MVP Task Ledger row. | Class: directly covers all categories; Product: directly improves client demo proof, ICP, and security/privacy. |
| P9.5: Gate release on review-protocol compliance for every implemented ledger task | As the orchestrator, I need every implemented task reviewed through the mandatory schema. | Each implemented ledger task must have canonical reviewer output and go/no-go decision. | Compliance report shows every implemented task has required review evidence; validation: review-protocol audit. | Review-compliance percentage reaches release threshold. | Block release until missing reviews are completed or tasks are marked no-go. | Implementer/reviewer: see MVP Task Ledger row. | Class: all categories get required review checks; Product: improves security/privacy, packaging/support, and local evidence. |
| P9.6: Gate release on activation/onboarding evidence, including measured install-to-first-value and cleanup path | As a target user, I need proof the product reaches value locally. | Activation evidence must include P0.10 setup path, first useful value, freshness, audit, suggested verification, privacy-safe output, failure reason, docs gap, cleanup, and segment-specific pass/fail/no-go classification. | Activation report meets threshold or records blocker with claim-scope impact; validation: quickstart smoke and product-evidence export. | Time-to-first-packet, time-to-trust-evidence, failure reason, docs gap, privacy-safe export status, assistance class, and cleanup status become release metrics. | Hold release or lower claim scope to internal/experimental. | Implementer/reviewer: see MVP Task Ledger row. | Class: open-source, local-model, enterprise SaaS, and tooling-vendor activation proof improves; Product: directly improves activation and local evidence. |
| P9.7: Gate release on design-partner validation, retained-use evidence, and at least one credible paid or internal-platform adoption signal | As a product owner, I need buyer-valued evidence before continuing. | Validation must be privacy-preserving and tie retained use to substrate value. | Design-partner report includes target users, workflow, activation, retained use, value signal, and adoption signal; validation: runbook review and product-evidence export. | Partner count, retained-use signal, and adoption-signal quality become release metrics. | Pivot or delay release if no credible signal is recorded. | Implementer/reviewer: see MVP Task Ledger row. | Class: primary ICP categories get adoption evidence and others are deferred honestly; Product: directly improves ICP, retained use, activation, and packaging/support. |
| P9.8: Gate release on three integration proof paths: shell/CI, external coding-agent workflow, and MCP stdio | As a platform buyer, I need proof Idiom works as a substrate across callers. | All integration paths must use shared profile, schema, freshness, and audit contracts. | Three proof transcripts pass or have documented no-go; validation: integration report and smoke tests. | Integration proof count reaches three with parity notes. | Release CLI-only or defer release if required paths fail. | Implementer/reviewer: see MVP Task Ledger row. | Class: tooling-vendor, enterprise SaaS, open-source, and public-sector integration proof improves; Product: directly improves integration proof. |
| P9.9: Gate release on reviewed security/privacy posture, threat model, data-flow map, and control mapping | As a regulated buyer, I need reviewed control evidence before release. | Threat model, data-flow, redaction/retention, protected paths, side effects, and control anchors must be reviewed. | Security/privacy bundle has reviewer signoff and reproducible checks; validation: posture review and automated checks. | Control-mapping completeness and security/privacy check status become release metrics. | Block regulated release claims and pivot to non-regulated scope. | Implementer/reviewer: see MVP Task Ledger row. | Class: finance, healthcare, public-sector, enterprise SaaS, and tooling-vendor readiness improves; Product: directly improves security/privacy. |
| P9.10: Gate release on competitive benchmark coverage or documented infeasibility for each external baseline | As a buyer, I need competitive proof that is not self-referential. | Each external baseline must be measured fairly or have documented infeasibility and claim impact. | Competitive report covers required baselines or explicit blockers; validation: benchmark report review. | External-baseline coverage and documented-infeasibility metrics become release evidence. | Remove unsupported competitive claims and record pivot risk. | Implementer/reviewer: see MVP Task Ledger row. | Class: enterprise SaaS, open-source, and tooling-vendor evaluation improves; Product: directly improves competitive proof. |
| P9.11: Gate release on runnable client demo transcripts and honest MVP/final-state limitations for every client classification | As a buyer-facing reviewer, I need demos that can be rerun and state limits. | Every demo must include commands, expected output, evidence, value claim, MVP limitation, and final-state expansion. | Demo report covers all classifications with runnable transcripts; validation: demo smoke and classification report. | Runnable demo coverage reaches seven categories. | Remove demo from release claims or block release for missing category. | Implementer/reviewer: see MVP Task Ledger row. | Class: directly covers all categories; Product: directly improves client demo proof and ICP validation. |
| P9.12: Gate release on packaging, pricing, support, admin, and ICP thesis with explicit continue/pivot decision | As a project owner, I need commercial scope tied to evidence before continuing. | Packaging/support thesis must cite ICP, pricing, support boundary, admin non-goals, enterprise-surface limits, classification stance, local evidence export, and continue/pivot evidence. | Release decision records packaging/support thesis and explicit continue, pivot, or no-go with P0.11 evidence fields; validation: product-gate review, docs diff, and product-evidence export review. | Packaging/support completeness, pricing-signal quality, unsupported-admin-request count, and pivot-decision evidence become release metrics. | Pivot to sidecar subset, keep packaging experimental, or defer commercial claims. | Implementer/reviewer: see MVP Task Ledger row. | Class: all categories are prioritized, deferred, proof-only, secondary, pivot, or excluded coherently; Product: directly improves ICP, packaging/support, and local evidence. |

## Phase 0: Baseline And Doc Hygiene

Goal: make the strategy executable and remove stale claims before building
against them.

Work:

- Add this plan to the roadmap reading path.
- Replace current-state prose snapshots with dated examples or commands that
  regenerate the evidence.
- Fix stale external links, especially Claude Code subagent URLs.
- Capture a baseline bundle:
  - `idiom tool context_status`
  - `idiom tool harness_audit --input '{"purpose":"tool_surface"}'`
  - `idiom tool harness_audit --input '{"purpose":"behavior"}'`
  - `go run ./cmd/idiom-swe-eval -suite testdata/swe/representative-suite.json -suite-only -json`
- P0.2 baseline artifact path: `docs/evidence/phase0-baseline-2026-05-13/`.
- Save the baseline path in the relevant design doc or release note, not in
  evergreen thesis prose.
- Convert the client-classification example-test matrix into a reusable
  work-package checklist so implementers and reviewers cannot omit vertical fit
  accidentally.
- Install the canonical reviewer prompt and required output schema into every
  work-package review handoff.
- Choose the MVP beachhead ICP, adoption wedge, paid buyer, non-targets,
  buyer/user distinction, and final-state expansion path.
- Create the design-partner validation protocol:
  - participant qualification and client-classification row
  - buyer/user split
  - repo/workflow context
  - current coding-agent stack
  - trigger event, blocked workflow, and current workaround
  - privacy constraints and disqualifiers
  - activation attempt and expected evidence
  - follow-up cadence and retained-use check
  - value-signal rubric scores
  - willingness-to-pay, budget-owner, sponsor, or internal-platform adoption
    signal
  - privacy-preserving retained-use evidence format for P7.11/P9.7
- Produce a product threat model and data-flow map that covers local logs,
  graph views, context packets, telemetry, MCP resources, external-agent calls,
  and sidecar traces.
- Map the threat model to the researched control anchors: NIST AI RMF, NIST SP
  800-218A, OWASP GenAI risks, MCP security principles, HHS risk-analysis
  guidance, and FINRA GenAI obligations.
- Define activation and onboarding metrics:
  - clean install, source-build, or clean-fixture path
  - first `idiom doctor` or `context_status`
  - first useful `context_pack`
  - freshness check
  - audit report
  - suggested verification
  - product-evidence-safe output
  - cleanup or uninstall path
  - docs gap
  - blocker reason and privacy/redaction status
- Define packaging, pricing, support, admin, and enterprise-surface hypotheses
  for MVP and final state, including explicit non-goals.

Validation:

```sh
git diff --check
idiom tool context_status
idiom tool harness_audit --input '{"purpose":"tool_surface"}'
idiom tool harness_audit --input '{"purpose":"behavior"}'
go run ./cmd/idiom-swe-eval -suite testdata/swe/representative-suite.json -suite-only -json
```

Go:

- Baseline commands run and produce parseable JSON.
- Tool surface drift is zero or has an explicit issue.
- Context graph freshness state is visible and not hidden in prose.
- Work packages have an explicit client-classification example-test field.
- Work packages have an explicit reviewer-prompt/schema reference and require
  the canonical review output schema, signoff, and GO before acceptance.
- The `enterprise-saas` beachhead ICP, open-source adoption wedge, secondary
  rows, deferred rows, and non-targets are explicit enough to reject unrelated
  feature requests.
- The design-partner protocol can collect activation, retained-use, value,
  integration, trust/security, adoption/budget, and pivot signals without raw
  prompt or file-body collection.
- The threat model and data-flow map make local/private operation, telemetry,
  retention, redaction, and MCP exposure concrete.
- Activation, packaging, support, and admin hypotheses have measurable release
  gates.

No-go:

- The docs still present mutable audit counts as stable product facts.
- The generated behavior-audit bundle cannot identify the top failure classes.
- The representative suite cannot validate its own structure.
- A task can be marked reviewed without addressing the client-classification
  example-test matrix.
- A task can be marked reviewed without a completed canonical review prompt
  response, required schema, and GO or a recorded project-approved
  exception/no-go/pivot.
- The MVP still tries to serve every client classification equally or treats
  open-source activation as proof of paid enterprise SaaS demand.
- Design-partner validation lacks primary-ICP record thresholds, retained-use
  checks, forbidden-field rules, or a way to feed P7.11/P9.7 without raw
  prompts, file bodies, secrets, or hosted telemetry.
- Product-success evidence depends on hosted telemetry, raw prompts, raw file
  bodies, or hand-waved customer claims.
- Security/privacy posture remains spread across prose without a single threat
  model and data-flow artifact.

## Phase 1: Passive Contract Events

Goal: define the contract object and emit events without changing model
freedom. This de-risks later enforcement.

Work:

- Introduce a typed autonomy contract for lane, phase, and sidecar calls.
- Represent target roles, allowed effects, verification obligations, evidence
  obligations, completion criteria, budget state, and path role policy.
- Emit stable events:
  - `contract_compiled`
  - `contract_policy_decision`
  - `contract_obligation_opened`
  - `contract_obligation_closed`
  - `contract_recompiled`
- Add tests that existing lanes still make the same high-level decisions.
- Add audit fields that join contract events to tool calls and final answers.

Validation:

```sh
go test ./internal/agent ./internal/app ./internal/workspace ./cmd/idiom
idiom tool harness_audit --input '{"purpose":"behavior"}'
```

Go:

- Events are passive and replayable from logs.
- Existing write, verification, and completion tests pass.
- Audit can report contract coverage without hand-reading JSONL.

No-go:

- Contract state becomes another prompt-only instruction layer.
- The contract compiler prescribes exact next tool calls instead of authority
  and obligation state.
- Existing lanes change behavior before events have baseline coverage.

## Phase 2: Freshness And Schema Foundation

Goal: make evidence and sidecar contracts machine-readable before exposing more
surface area.

Work:

- Export tool schemas from the existing manifest through a stable command.
- Include side-effect hints, profile availability, input schema, output schema,
  freshness fields, and error taxonomy.
- Make freshness structured in all graph-backed results:
  - index ID
  - graph freshness
  - stale paths
  - refresh attempt
  - confidence tier
  - stale-use blocker or allowed-stale reason
- Add a compatibility check that manifest, schema, and suggestion surfaces do
  not drift.
- Replace stale-cache prose baselines with generated audit output.

Validation:

```sh
go test ./internal/workspace ./internal/comprehension ./cmd/idiom
idiom tool context_status
idiom tool harness_audit --input '{"purpose":"tool_surface"}'
```

Go:

- Schema export covers every manifest tool.
- Freshness is present in `context_status`, `context_pack`, and graph-backed
  sidecar outputs.
- Stale evidence cannot silently support write-scoped decisions.

No-go:

- Tool schemas are generated from a second source of truth.
- A graph-backed result omits freshness or provenance.
- A stale context packet can be treated as fresh by later phases.

## Phase 3: Read-Only Capability Resolver

Goal: collapse model-visible language/tool complexity into task capabilities
without hiding useful adapter evidence.

Initial capability set:

- `localize_change`
- `inspect_symbol`
- `find_references`
- `source_sanity`
- `list_tests`
- `explain_dependency`
- `audit_behavior`

Work:

- Build a resolver registry over existing language, graph, structural, and
  generic tools.
- Route only read-only capabilities first.
- Return selected adapter, fallback reason, confidence, precision tier, and
  unresolved gaps.
- Update runtime briefs and recovery packets to mention capabilities instead of
  language-specific strategy menus.
- Keep existing language-prefixed tools available during migration.

Validation:

```sh
go test ./internal/workspace ./internal/agent ./internal/app
idiom tool harness_audit --input '{"purpose":"behavior"}'
```

Go:

- At least three capabilities route through the resolver in focused tests.
- Fallbacks are explicit and auditable.
- Prompt/recovery text shrinks or centralizes rather than gaining duplicate
  instructions.

No-go:

- The resolver hides adapter uncertainty.
- Recovery text tells the model exactly which next tool to call outside a hard
  invariant.
- Existing language-aware precision gets worse without audit visibility.

## Phase 4: Localization Benchmark And Ranker Trigger

Goal: prove repository memory is better than ordinary exploration for Idiom's
own tasks.

Work:

- Build a localization eval from 20 to 30 recent implementation logs with known
  changed files, relevant tests, and failure classes.
- Compare conditions:
  - raw read/search baseline
  - baseline-time `context_pack` command output
  - `context_pack` with freshness read-through
  - `context_pack` with ranker signal
  - Aider-style repo-map or equivalent indexed-context baseline, where it can
    be run locally under the same task budget
  - one external-agent native exploration baseline, where it can be captured
    without leaking private repository contents
- Track top-3, top-5, and top-10 edit-path coverage.
- Track broad read/search count, zero-hit search count, redirect storms, and
  time/tool budget.
- Wire one ranker learning trigger from verified edits or grounded
  `harness_feedback`.
- For any competitive condition that cannot be run, record the exact blocker,
  attempted command or integration path, and whether the omission weakens the
  MVP sales claim.

Validation:

```sh
go test ./internal/comprehension ./internal/eval ./internal/workspace
idiom tool harness_audit --input '{"purpose":"behavior"}'
```

Promotion expectation:

- Top-10 edit-path coverage improves over the raw baseline.
- Broad read/search calls drop in the focused eval.
- Ranker learning writes inspectable `learning_log.jsonl` entries.
- Competitive reports distinguish measured results from documented
  infeasibility and do not compare Idiom-only variants as though they prove
  market differentiation.

No-go:

- The graph does not beat ordinary search on localization after a fair sample.
- Ranker updates train on held-out gold during measured runs.
- Ranker explanations cannot tell why a path was boosted or penalized.
- Competitive proof is omitted, anecdotal, or impossible to reproduce.

## Phase 5: Auditable Plan Handoff

Goal: make planning durable enough that execution can inherit packet IDs,
editable paths, evidence, and verification obligations.

Work:

- Define the durable plan artifact with:
  - task ID
  - contract ID
  - packet ID
  - target paths and roles
  - evidence paths
  - unresolved questions
  - verification obligations
  - approval scope
  - executor handoff fields
- Strengthen `submit_plan` and plan-audit checks.
- Add replay compare for plan variants.
- Ensure executor events distinguish inherited packet, packet miss, stale
  packet, and changed-scope override.

Validation:

```sh
go test ./cmd/idiom ./internal/agent ./internal/workspace
go run ./cmd/idiom-swe-eval -suite testdata/swe/representative-suite.json -suite-only -json
```

Go:

- Plan artifacts are parseable and audit-checked.
- Executor handoff records packet ID and verification obligations.
- Missing or stale packet handoff is visible in telemetry and audit.

No-go:

- Plans become hidden host-authored strategies.
- Executor mutates files outside the plan's allowed scope without a typed
  replan or blocker.
- Plan audit failures require hand-inspecting prose to diagnose.

## Phase 6: Local-Model Readiness

Goal: make local inference constraints visible before a lane starts and
measurable after it runs.

Work:

- Extend `idiom doctor` with readiness profiles:
  - `discovery`
  - `plan`
  - `implement`
  - `eval`
  - `sidecar`
- Add checks for backend availability, queue admission, context budget, model
  role config, route classifier health, graph freshness, and required local
  tools.
- Emit lifecycle and capacity telemetry for backend start, queue wait, context
  budget, implement-session closeout, fallback mode, and degraded readiness.
- Define soft and hard gates by profile.

Validation:

```sh
go test ./cmd/idiom ./internal/backends ./internal/app ./internal/frontier/workerruntime
idiom doctor --json
```

Go:

- A developer can see why a lane is safe, degraded, or blocked before launch.
- Queue and backend failures produce structured remediation.
- Implement sessions emit exactly one `session_summary` on normal, empty-start,
  and handled-signal closeout paths.
- Local-model readiness appears in audit summaries.

No-go:

- Readiness is only printed as free-form text.
- Backend failures remain diagnosable only from process logs.
- Implement conversations can end without a `session_summary` after normal close
  or handled signal.
- The implement profile can start when the configured model and queue state are
  known to be unsafe.

## Phase 7: Discovery Sidecar MVP

Goal: expose Idiom's evidence substrate to other agents without exposing write
or shell authority.

Work:

- Ship a stable CLI envelope for `idiom tool` results:
  - schema version
  - tool name
  - profile
  - contract ID
  - freshness
  - structured output
  - warnings
  - audit event IDs
  - machine-readable errors
- Implement `--profile discovery` filtering.
- Add external caller recipes for Codex, Claude Code, OpenCode, Aider, and
  manual scripts.
- Include examples for:
  - preloading a context packet
  - asking graph-shaped questions
  - running behavior audit after edits
  - checking freshness before trusting a packet
- Add CLI-side example tests for every client classification. These should use
  overlay fixtures and shared primitives, not client-specific control paths.
- Add a clean-install quickstart smoke path that measures install to first
  useful `context_pack`, freshness check, audit report, failure reason, docs
  gap, and clean uninstall or state cleanup.
- Add three integration proofs:
  - shell or CI script consuming structured CLI output
  - one external coding-agent workflow consuming the same profile and schema
  - a placeholder handoff record for MCP stdio to close in Phase 8
- Add security/privacy posture artifacts and checks covering protected paths,
  redaction, retention, telemetry minimization, and side-effect policy.
- Add runnable demo transcripts for every client classification. Each transcript
  must name the command, expected output shape, evidence produced, buyer value
  claim, MVP limitation, and final-state expansion.
- Add a local product-evidence export that summarizes activation, retained use,
  integration proof, benchmark proof, audit status, and learning signals without
  raw prompts, raw file bodies, or secrets.

Validation:

```sh
go test ./cmd/idiom ./internal/workspace
idiom tool context_pack --input '{"task":"sidecar discovery smoke","mode":"plan","limit":5}'
idiom tool harness_audit --input '{"purpose":"tool_surface"}'
```

Go:

- An external agent can use discovery tools without knowing Idiom internals.
- Discovery profile exposes no write, shell, destructive, or open-world tools.
- Outputs are stable enough for scripts to parse.
- The CLI suite includes one finance, healthcare, public-sector, enterprise SaaS,
  tooling-vendor, local-model, and open-source example test where the required
  primitive is available through the CLI.
- Clean setup can reach first value without insider project knowledge and
  records its elapsed time, blockers, and cleanup path.
- Shell/CI and external-agent integration proofs consume stable contracts
  rather than prompt prose.
- Security/privacy checks and local product-evidence export are usable by a
  reviewer without collecting hosted telemetry or raw repository content.
- Client demos are executable sales artifacts: each one can be rerun and tied
  back to a generic contract, freshness, audit, readiness, or sidecar primitive.

No-go:

- Sidecar contracts fork from first-party lane contracts.
- Discovery profile accidentally exposes mutation tools.
- External recipes depend on brittle prompt interpolation rather than CLI
  contracts.
- Classification examples pass by hardcoded vertical branches instead of profile,
  contract, schema, freshness, audit, readiness, or sidecar primitives.
- Quickstart only works through undocumented local setup knowledge.
- Product-evidence export includes raw prompts, raw file bodies, secrets, or
  hosted telemetry as a default requirement.
- Security posture is described as policy prose without executable containment,
  redaction, or retention checks.

## Phase 8: MCP Stdio Read-Only Server

Goal: add MCP as a second transport after the CLI contract is proven.

Work:

- Implement `idiom mcp serve --stdio --profile discovery`.
- Map profile-filtered tools from the same manifest and schema export.
- Expose resources for context packs, audit reports, selected logs, and ranker
  state.
- Clamp workspace roots to the active project root.
- Add protocol tests for initialize, tools/list, tools/call, resources/list,
  and error cases.
- Mirror classification examples through MCP where resources, caller metadata,
  redaction, profile filtering, or freshness semantics are part of the claim.
- Complete the MCP integration proof by verifying protocol handshake, caller
  metadata, root clamping, resource listing, resource redaction, freshness
  behavior, and product-evidence export parity with CLI-side evidence.

Validation:

```sh
go test ./cmd/idiom ./internal/workspace
idiom tool harness_audit --input '{"purpose":"tool_surface"}'
```

Go:

- MCP tools/list matches the discovery-profile schema export.
- MCP tool calls return the same structured contract envelope as CLI calls.
- Roots, profiles, and permission decisions are auditable.
- MCP parity tests cover the classification examples that depend on MCP-visible
  behavior.
- MCP product-evidence export does not introduce a second evidence model, a
  broader data-retention posture, or transport-specific sales claims.

No-go:

- MCP becomes a separate tool implementation.
- HTTP transport starts before stdio is stable.
- Resources can leak paths outside the declared project root.
- MCP classification behavior diverges from the CLI contract without a documented
  transport limitation and follow-up task.
- MCP integration proof cannot demonstrate redaction, root containment, caller
  attribution, and evidence parity from one replayable transcript.

## Phase 9: Release Gates And Pivot Review

Goal: decide whether Idiom should continue as a full harness or narrow to a
sidecar substrate.

Required release evidence:

- Baseline bundle from Phase 0.
- Current bundle using the same commands.
- Localization benchmark report.
- Sidecar smoke transcript from at least one external caller.
- Client-classification example-test report covering finance, healthcare,
  public-sector, enterprise SaaS, tooling-vendor, local-model, and open-source
  scenarios.
- Activation/onboarding report with measured time-to-first-packet,
  time-to-trust-evidence, failure reasons, docs gaps, privacy/redaction status,
  product-evidence-safe output, cleanup result, and whether the default target
  was met or narrowed.
- Design-partner validation report with interview/runbook records, value-signal
  rubric, retained-use evidence, and at least one credible paid or internal
  platform adoption signal.
- Integration proof report for shell/CI, external coding-agent workflow, and MCP
  stdio, including transcript paths and schema/profile parity notes.
- Security/privacy posture bundle with threat model, data-flow map,
  redaction/retention policy, protected-path checks, side-effect policy, and
  NIST AI RMF, NIST SP 800-218A, OWASP GenAI, MCP, HHS, and FINRA mapping where
  relevant to the client examples.
- Competitive benchmark report covering measured baselines and documented
  infeasibility for any Aider-style or external-agent baseline that cannot be
  run fairly.
- Packaging and support thesis covering MVP buyer, user, non-targets, pricing
  hypothesis, support boundary, admin needs, enterprise-surface non-goals,
  all seven classification stances, P9.12 evidence fields, and final-state
  expansion.
- Local product-evidence export showing activation, integration, benchmark,
  audit, retained-use, and learning evidence without raw prompts, raw file
  bodies, or secrets.
- Review-protocol compliance report showing every reviewed ledger task has a
  canonical reviewer-prompt response and required output schema.
- Audit report showing contract, freshness, and behavior metrics.
- Test report for changed packages and full `go test ./...`.

Validation:

```sh
git diff --check
go test ./...
idiom tool context_status
idiom tool harness_audit --input '{"purpose":"tool_surface"}'
idiom tool harness_audit --input '{"purpose":"behavior"}'
go run ./cmd/idiom-swe-eval -suite testdata/swe/representative-suite.json -suite-only -json
```

Project go:

- `context_pack` localization beats ordinary search in the focused benchmark.
- Broad read/search and zero-hit search behavior improves in the benchmark or
  has a precise remaining failure class.
- Contract and freshness events are replayable from logs.
- One read-only sidecar workflow works for an external agent or script.
- Local-model readiness can explain pass, degraded, and blocked states.
- Every client-classification example test passes or has an explicit project
  no-go/pivot decision.
- Every reviewed task has criterion-by-criterion review evidence and an explicit
  reviewer go/no-go decision.
- The `enterprise-saas` beachhead, paid buyer, open-source adoption wedge,
  classification priorities, and explicit non-target list remain coherent with
  the implemented sidecar substrate.
- Activation, integration, security/privacy, competitive, and retained-use
  evidence are good enough to justify either MVP release or a narrow, named
  pivot.
- Packaging and support scope do not require building enterprise/admin features
  before substrate proof.

Project no-go:

- Most work is UI parity, prompt duplication, or agent-chat feature copying.
- Repository memory cannot beat ordinary search on local tasks.
- Audit findings stay flat after targeted fixes.
- External agents still need Idiom internals to use the sidecar.
- Local-model operations remain too fragile for repeatable bounded tasks.
- Any client classification remains represented only by sales prose without an
  executable example test.
- Review evidence is missing, informal, or not tied to the canonical reviewer
  prompt.
- No enterprise SaaS or DevSecOps platform buyer can be named who values local
  audit, repo memory, controlled discovery, and integration substrate more than
  another chat interface.
- Activation cannot be completed by a target user without hand-holding.
- Product success depends on hosted telemetry, raw prompt capture, raw file-body
  capture, or claims that cannot be reproduced from local evidence.

Pivot:

- If the full harness does not clear the go criteria, narrow the project to the
  sidecar subset: context, freshness, audit, verification, and eval tools for
  other agents.

## Dependency Map

Critical path:

```text
baseline
  -> beachhead ICP, validation protocol, threat model, activation metric
  -> passive contracts
     -> schema and freshness
        -> capability resolver
        -> localization benchmark
           -> competitive benchmark
           -> sidecar CLI
              -> activation and product-evidence export
              -> integration proof paths
              -> client-classification CLI examples
              -> MCP stdio
                 -> MCP parity examples
                 -> release decision
```

Secondary path:

```text
passive contracts
  -> plan handoff
  -> local-model readiness
  -> release decision
```

Product-success path:

```text
beachhead ICP
  -> design-partner validation protocol
  -> activation/onboarding metric
  -> security/privacy posture
  -> packaging, pricing, support, admin, and enterprise-surface thesis
     -> sidecar quickstart and product-evidence export
        -> runnable client demos
        -> integration proof paths
        -> release go/no-go/pivot decision
```

Ranker learning depends on benchmark labels and verified edit signals. Write
profiles depend on contract enforcement, stale evidence blockers, and sidecar
permission hardening; they are outside the MVP.

## Work Package Template

Every implementation atom should include:

- Pillar advanced.
- User-visible or agent-visible outcome.
- Contract, evidence source, or feedback loop strengthened.
- Files or modules expected to change.
- Implementation subagent assignment sourced from the MVP Task Ledger.
- Review subagent assignment sourced from the MVP Task Ledger.
- Required tests and audit commands.
- Product-success requirement impact:
  ICP, activation, retained use, integration proof, security/privacy,
  competitive proof, client demo proof, packaging/support, and local
  product-evidence export.
- Client-classification example-test impact:
  `finance`, `healthcare`, `public-sector`, `enterprise-saas`,
  `tooling-vendor`, `local-model`, and `open-source`, with coverage decision,
  example fixture or evidence, and any no-go/follow-up.
- Baseline metric expected to move.
- Dedicated review handoff that references the canonical reviewer prompt/schema
  and names where review evidence will be recorded.
- Go/no-go condition.
- Rollback or disable path.
- Documentation update.

Reject atoms that cannot name the metric or audit signal they should move.

Template:

```md
### Pn.n: Task Name

Status:

- Implemented: see MVP Task Ledger row.
- Reviewed: see MVP Task Ledger row.

Implementation subagent:

- Agent assignment: see MVP Task Ledger row.
- Handoff prompt:
- Owned files/modules:
- Required outputs:

Scope:

- ...

Validation:

- ...

Client-classification example-test impact:

Use one coverage decision per affected matrix row: preserved, updated, added,
satisfied, skipped-na, or no-go. Include the fixture/evidence or the exact
not-applicable reason.

- `finance`:
- `healthcare`:
- `public-sector`:
- `enterprise-saas`:
- `tooling-vendor`:
- `local-model`:
- `open-source`:

Product-success impact:

- ICP:
- Activation:
- Retained use:
- Integration proof:
- Security/privacy:
- Competitive proof:
- Client demo proof:
- Packaging/support:
- Local product-evidence export:

Review subagent:

- Agent assignment: see MVP Task Ledger row.
- Handoff prompt: use the canonical reviewer prompt and required output schema
  verbatim with placeholders filled.
- Expected review-output location: <handoff or evidence location for the
  schema-complete response, verification evidence, client-classification table,
  product-success table, decision, and signoff>.
- Must be different from implementation subagent: yes/no

Review acceptance:

- Reviewed: see MVP Task Ledger row.
- Acceptance requires the separate review subagent's schema-complete
  `Decision: GO`, or a project-approved exception/no-go/pivot recorded in this
  plan. Review output is evidence, not live status tracking.

Go/no-go:

- ...
```

Implementation subagent handoff prompts must include the task ID, dependency
state, allowed write scope, expected validation, and the instruction to list all
changed files. They must also require the implementer to cite affected
client-classification matrix row IDs and state whether each row was preserved,
updated, newly added, satisfied, skipped as not-applicable with a specific
reason, or converted into a no-go/follow-up. Review subagent handoff prompts
must use the canonical reviewer prompt and required output schema, include the
task ID, implementation evidence, changed files, validation output,
client-classification impact statement, product-success impact statement, and
expected review-output location. Reviewers should not make code changes unless a
follow-up implementation task is created and assigned separately.

## Validation Matrix

| Change Type | Required Validation |
| --- | --- |
| Docs only | `git diff --check`; local link/path check when links change |
| Tool schema or manifest | `go test ./internal/workspace ./cmd/idiom`; `harness_audit tool_surface` |
| Contract or runner policy | `go test ./internal/agent ./internal/app ./cmd/idiom`; behavior audit |
| Context graph or freshness | `go test ./internal/comprehension ./internal/workspace`; `context_status`; context-pack smoke |
| Eval or audit | `go test ./internal/eval ./internal/workspace ./cmd/idiom`; representative suite validation |
| Backend or queue | `go test ./internal/backends ./internal/frontier/workerruntime ./cmd/idiom`; `idiom doctor --json` |
| Sidecar or MCP | `go test ./cmd/idiom ./internal/workspace`; schema/profile parity check; smoke caller transcript |
| Client-classification examples | Focused classification example tests mapped to matrix row IDs, required evidence fields, minimum privacy/security/performance expectations, and go/no-go signals; CLI/MCP parity where transport is part of the claim; audit output showing contract/profile/freshness evidence |
| Review protocol | Canonical reviewer prompt present; required output schema complete; criteria, client-classification table, product-success table, verification evidence, findings, signoff, and explicit GO/NO-GO recorded |
| Product-success docs or gates | `git diff --check`; trace every product-success requirement to at least one ledger task and one release gate; verify researched source links and dates |
| Activation/onboarding | Clean-install or clean-fixture smoke; measured time-to-first-packet and time-to-trust-evidence; first useful `context_pack`; freshness check; audit report; suggested verification; product-evidence-safe output; docs gap or blocker reason; privacy/redaction status; cleanup result; no duplicate live status tracking |
| Design-partner validation | Completed runbook entries; value-signal rubric; retained-use evidence; buyer/user/non-target consistency check; primary-ICP threshold check; forbidden-field/privacy check; privacy-preserving evidence export into P7.11/P9.7 |
| Security/privacy posture | Threat model review; data-flow map; protected-path, redaction, retention, telemetry-minimization, side-effect, caller-attribution, MCP resource/root containment, and control-mapping checks; source reachability/support check for NIST/OWASP/MCP/HHS/FINRA/telemetry anchors |
| Packaging/support thesis | MVP/final-state packaging split; pricing/support/admin hypotheses; all seven client-classification stances; explicit non-goals; P9.12 evidence-field coverage; trace to ICP, activation, retained use, integration proof, security/privacy, competitive proof, client demo proof, packaging/support, and local product-evidence export; no duplicate live status tracking |
| Competitive benchmark | Raw baseline, generated baseline/current discovery reports, context-pack variants, Aider-style or equivalent indexed-context baseline, external-agent native baseline, or documented infeasibility per condition |
| Product-evidence export | Local report fixture; no raw prompt bodies; no raw file bodies; no secrets; includes activation, integration, benchmark, audit, retained-use, and learning summaries |

Full `go test ./...` is required before release-gate review and before merging
shared runtime, prompt/control-rail, routing, workspace-tooling, or sidecar
changes with broad blast radius.

## Risk Controls

- Do not add prompt reminders when a typed contract, tool schema, audit field,
  or runtime brief field is the real missing owner.
- Do not expose write or shell sidecar profiles until read-only discovery has
  schema, freshness, audit, and permission parity.
- Do not treat MCP tool annotations or side-effect hints as trusted authority.
- Do not train ranker state on held-out eval labels during measured runs.
- Do not accept stale graph evidence for write decisions without an explicit
  refresh, stale-use reason, or blocker.
- Do not add support for another language backend until the capability resolver
  can report precision and fallback semantics consistently.
- Do not count a client classification as covered by prose, screenshots, or a
  sales example alone; it needs an executable example test or an explicit
  no-go/pivot record.
- Do not mark a task reviewed from an informal summary. The canonical reviewer
  prompt response must include the criteria checklist, client-classification
  table, product-success table, verification evidence, findings, follow-up
  tasks, signoff, and `Decision: GO`, unless this plan records a
  project-approved exception/no-go/pivot.
- Do not treat all client classifications as equal MVP targets. The release
  decision must test `enterprise-saas` as the paid beachhead, `open-source` as
  the adoption wedge, and named secondary/deferred rows before it names the
  non-targets and expansion path.
- Do not count `open-source`, `tooling-vendor`, `local-model`, finance,
  healthcare, public-sector, survey-only, or demo-only records as primary
  enterprise-SaaS paid-demand evidence unless the P0.8 protocol says exactly why
  they meet the primary-ICP criteria.
- Do not claim product success from self-reported productivity or demo polish
  alone. Require activation, retained-use, integration, benchmark, security, and
  buyer-value evidence.
- Do not require hosted telemetry, raw prompts, raw file bodies, or secrets to
  make product decisions. Default product evidence must be local, summarized,
  and reviewable.
- Do not count activation as passed when the run lacks a useful packet,
  freshness state, audit IDs, suggested verification, privacy-safe evidence
  summary, docs-gap or blocker record, and cleanup result. Late or assisted
  runs are failure evidence, not hidden setup noise.
- Do not present P0.9 control mapping as NIST, OWASP, MCP, HHS, FINRA, HIPAA,
  or finance-industry compliance. It is review evidence until a separate
  customer-approved compliance program exists.
- Do not let packaging, admin, SSO, fleet management, or enterprise policy work
  pull ahead of substrate proof unless a gate explicitly changes the MVP scope.
- Do not price or package Idiom as a generic coding assistant, IDE replacement,
  model marketplace, token reseller, hosted telemetry product, or hardcoded
  agent-strategy engine. The packaged value is local repo memory, freshness,
  contracts, audit, evals, integration proof, and privacy-safe evidence export.
- Do not convert regulated, tooling-vendor, local-model, open-source, demo-only,
  or survey-only interest into primary enterprise-SaaS packaging proof without
  the P0.8/P9.7 buyer and adoption evidence P0.11 requires.
- Do not use competitor names as proof. Competitive claims require measured
  runs under comparable budgets or a documented infeasibility record.

## Expected Project Shape After MVP

After Phase 9, Idiom should have:

- one canonical contract event stream
- one schema source for CLI and future MCP tools
- one freshness model for repository memory
- a small read-only capability vocabulary
- a measured context-localization benchmark
- one ranker learning source
- a durable planning handoff path
- local-model readiness gates
- a stable discovery-profile sidecar path
- executable client-classification example tests proving vertical fit through
  generic primitives
- researched product-success gates tying the north star to a beachhead ICP,
  activation, retained use, integration proof, security/privacy posture,
  competitive proof, and packaging/support scope
- local product-evidence export suitable for buyer conversations without hosted
  telemetry or raw repository content collection
- replayable client demo transcripts that sell the same architecture rather
  than separate vertical products
- clear evidence for whether to continue full harness investment or pivot to
  sidecar-first
