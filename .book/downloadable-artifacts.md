# Downloadable Artifacts

Purpose: define the appendix-ready and public-downloadable artifacts that make
the book useful beyond prose.

Core artifacts appear in the appendix. The full pilot kit, including standalone
spreadsheets and implementation templates that are too detailed for the
appendix, can become a landing-page download bundle.

## Primary Download Bundle: AI Workflow Pilot Kit

Directory: `downloads/ai-workflow-pilot-kit/`

Purpose: give an engineering manager or workflow owner enough structure to run
one AI workflow pilot without rebuilding the book's framework from scratch.

The bundle contains:

| File | Use |
| --- | --- |
| `README.md` | Kit overview and minimum usable output. |
| `00-start-here.md` | First-workflow selection, authority mode, and first-session checklist. |
| `01-90-minute-workshop-guide.md` | Facilitated workshop agenda and exit criteria. |
| `02-pilot-operating-guide.md` | Three-week pilot cadence, measures, and scale gates. |
| `03-pilot-runbook.md` | Artifact ownership, packet lifecycle, reviewer SLA, acceptance handoff, and weekly meeting agenda. |
| `04-threshold-setting-guide.md` | Default thresholds, capacity formulas, sampling guidance, and authority-increase rules. |
| `templates/decision-acceptance-contract.md` | Defines the accepted decision, sources, reviewer actions, failure states, and scale gate. |
| `templates/claim-to-evidence-matrix.md` | Maps decision claims to authority-bearing sources, freshness rules, citations, and states. |
| `templates/reviewer-packet.md` | Gives reviewers a packet they can accept, reject, return incomplete, escalate, or block. |
| `templates/effect-boundary-spec.md` | Names allowed and prohibited effects across tools, records, communication, money, code, and routing. |
| `templates/control-validation-loop.md` | Weekly review loop for evidence, review debt, stale sources, bypass, and escaped errors. |
| `templates/vendor-tool-evaluation-checklist.md` | Vendor/internal platform evaluation against workflow-level evidence and controls. |
| `templates/authority-pace-checklist.md` | Stop/go/narrow/throttle/authority-change gate. |
| `templates/review-capacity-scorecard.md` | Plain-English companion to the CSV scorecard. |
| `templates/workflow-readiness-preflight.md` | Refusal-oriented readiness check for choosing authority mode. |
| `templates/source-freshness-map.md` | Source ownership, freshness, conflict, and context-vs-authority map. |
| `templates/citation-honesty-ledger.md` | Claim-level citation caveat ledger separating source existence, source authority, source type, and decision use or context-only use. |
| `templates/responsibility-incentive-map.md` | Accountability, veto, budget, escalation, and incentive-risk map. |
| `templates/first-workflow-design-canvas.md` | One-page first-pilot design canvas. |
| `examples/support-credit-filled.md` | Filled composite operations example using the book's running support-credit scenario. |
| `examples/ai-code-review-filled.md` | Filled software-engineering example for AI-assisted PR review. |
| `examples/discovery-only-source-ownership-filled.md` | Filled no-go example for unresolved source ownership. |
| `examples/draft-assistance-filled.md` | Filled low-authority drafting example. |
| `examples/bounded-action-filled.md` | Filled bounded action example with permission, rollback, and incident controls. |
| `spreadsheets/review-capacity-scorecard.csv` | Spreadsheet-ready review-capacity scorecard with formulas. |
| `spreadsheets/review-capacity-scorecard-sample.csv` | Sample scorecard showing a do-not-scale outcome. |
| `spreadsheets/pilot-log.csv` | Packet-level pilot log for sources, reviewer actions, accepted decisions, effects, incidents, and rollback notes. |
| `spreadsheets/ai-workflow-pilot-workbook.xlsx` | Multi-tab workbook for capacity, claim evidence, citation honesty, pilot log, control review, authority pace, readiness, source freshness, responsibility map, and first-workflow canvas. |
| `spreadsheets/claim-to-evidence-matrix.csv` | Spreadsheet-form claim and citation matrix. |
| `spreadsheets/citation-honesty-ledger.csv` | Spreadsheet-form citation honesty ledger. |
| `spreadsheets/control-validation-loop.csv` | Spreadsheet-form weekly control review. |
| `spreadsheets/authority-pace-checklist.csv` | Spreadsheet-form authority pace gate. |
| `spreadsheets/workflow-readiness-preflight.csv` | Spreadsheet-form readiness preflight. |
| `spreadsheets/source-freshness-map.csv` | Spreadsheet-form source and freshness map. |
| `spreadsheets/responsibility-incentive-map.csv` | Spreadsheet-form responsibility and incentive map. |
| `spreadsheets/first-workflow-design-canvas.csv` | Spreadsheet-form first workflow design canvas. |

Standard: the kit should be usable without rereading the whole manuscript. A
manager should be able to select one workflow, run the workshop, fill the core
templates, and decide whether to continue, narrow, throttle, escalate, or stop.

## 1. Decision-Acceptance Preflight

Use this before a workflow lets AI output affect a customer, record, account,
system, financial decision, legal filing, operational recommendation, or other
authority-bearing state.

```text
Workflow:
Authority mode: discovery only / draft assistance / reviewed proposal / bounded action
What the output may do before acceptance:
What the output must not do before acceptance:
Authority-bearing source map:
Required reviewer or checker:
Reviewer capacity this week:
Accepted state:
Blocked state:
Escalation path:
Stop condition:
Audit record:
Owner after acceptance:
```

Done when: every field can block, route, require, log, throttle, escalate, or
stop work. If a field cannot change workflow behavior, remove it.

## 2. Minimum Viable Decision Acceptance Contract

The smallest useful contract is:

| Field | Answer |
| --- | --- |
| One decision | What exact decision may be accepted? |
| Controlling source map | Which source or sources control the highest-risk claims? |
| Action limit before acceptance | What may the AI output do before acceptance? |
| One checker | Who or what verifies the controlling claim? |
| One stop condition | What forces the workflow to block, throttle, or refuse automation? |
| One owner | Who accepts the decision after the workflow has done its checks? |

Everything else is an escalation tier.

## 3. Claim-To-Evidence Matrix

Use this when a smooth AI-generated proposal bundles several claims into one
recommendation.

| Claim | Authority-Bearing Source | Freshness Rule | Verification Method | State |
| --- | --- | --- | --- | --- |
|  |  |  |  |  |
|  |  |  |  |  |
|  |  |  |  |  |

Allowed state values: missing / stale / supported / conflicted / verified.

Rules:

- Context helps the model understand the situation.
- Evidence supports or rejects a specific claim.
- Freshness decides whether the source is current enough for the decision.
- Acceptance decides whether the output may be used.

## 4. Responsibility And Incentive Map

Use this when someone is named accountable but may not control the evidence,
budget, authority, or escalation path.

| Role | Owns | Does Not Own | Budget / Capacity | Veto Right | Escalation Cover | Incentive Risk | Required Repair |
| --- | --- | --- | --- | --- | --- | --- | --- |
|  |  |  |  |  |  |  |  |
|  |  |  |  |  |  |  |  |
|  |  |  |  |  |  |  |  |
|  |  |  |  |  |  |  |  |
|  |  |  |  |  |  |  |  |

Done when every named owner has the control needed for the responsibility they
carry. If not, the workflow names accountability without providing control.

## 5. Review-Capacity Scorecard

Use this before increasing AI-generated proposal volume.

| Measure | Current Value | Unit / Notes | Threshold | Action When Threshold Is Crossed |
| --- | ---: | --- | ---: | --- |
| Expected proposals per week |  | count |  |  |
| Complete-packet rate |  | decimal fraction, e.g. `0.75` |  |  |
| Funded reviewer-hours per week |  | hours |  |  |
| Average review time per complete packet |  | hours |  |  |
| Maximum well-reviewed packets per week |  | count |  |  |
| Review debt |  | count |  |  |
| Incomplete packets |  | count |  |  |
| Escaped unsupported decisions |  | count | 0 |  |

Allowed action values include throttle / narrow scope / add capacity / auto-reject / return to source collection / stop / rollback.

Formula:

```text
Maximum well-reviewed packets =
  funded reviewer-hours / average review time per complete packet
```

Accepted verified output counts only work that passed the required checks and
was accepted for use. Generated volume is activity, not accepted work.

## 6. Reviewer Packet

Use this when human review is the control.

| Packet Field | Purpose |
| --- | --- |
| Proposed action | Shows what effect is at stake. |
| Claim list | Splits the recommendation into checkable statements. |
| Source link per claim | Lets the reviewer inspect evidence without reconstruction work. |
| Source existence | Shows whether the source is missing, attached, or an illustrative placeholder. |
| Source authority | Shows whether the source is authority-bearing, supporting, background context, or not authority-bearing. |
| Source type | Shows whether the source is primary, official, regulator/court, academic, journalism, internal, vendor, or background. |
| Source date or version | Shows which source version the reviewer is checking. |
| Source caveat | Shows what the source does not establish. |
| Freshness status | Shows whether that source version is current enough for the claim. |
| Missing/conflicted evidence | Makes blocking conditions explicit. |
| Allowed reviewer actions | Accept packet, accept decision, authorize effect, reject, return incomplete, escalate, or block. |
| Rejection rule | States when rejection cannot be overridden informally. |
| Audit note | Records what was accepted, rejected, escalated, or missing. |

Review is useful only when the reviewer has evidence, time, expertise,
independence, and authority to reject or escalate.

## 7. Effect-Boundary Specification

Use this before an AI workflow can access tools or communicate outside a draft
surface.

| Surface | Discovery Only | Draft Assistance | Reviewed Proposal | Bounded Action |
| --- | --- | --- | --- | --- |
| Source access | Approved sources for inspection | Approved low-risk context only | Required sources for claims | Required sources plus action logs |
| Communication | None outside the user/team | Internal draft only | Internal reviewer packet | Approved customer/system message |
| Record mutation | None | None | None or draft note | Only permitted fields |
| Money / credit / entitlement | None | None | Recommendation only | Within threshold after acceptance |
| Tool use | Read-only or sandboxed | Tools limited to drafting | Route packet / request missing source | Act within explicit permission |
| Failure state | Stop / mark missing | Ask user / mark missing | Return incomplete / escalate | Stop, rollback, or incident path |

The boundary is real only if the workflow can enforce it.

## 8. Control-Validation Loop

Use this after launch.

| Check | Question | Action |
| --- | --- | --- |
| Accepted-decision sampling | Did accepted work have current evidence and proper authority? | Fix source, packet, review, or acceptance rule. |
| Rejected-decision review | Did the workflow reject useful work for the wrong reason? | Retune source collection or escalation path. |
| Stale-source drill | Does the workflow block old but plausible evidence? | Repair freshness rule. |
| Reviewer calibration | Do reviewers apply the same acceptance standard? | Update packet, examples, or training. |
| Bypass detection | Are teams using private tools or side channels? | Create a sanctioned lower-authority path or fix throughput. |
| Escaped-error review | What unsupported work reached authority? | Change the workflow, not only the prompt. |
| Control retirement | Did a control ever change acceptance? | Retire or redesign controls that never block, route, log, throttle, or stop. |

## 9. Workflow Readiness Preflight

Use this before deciding whether a workflow is ready for AI at any authority
level.

| Question | Answer |
| --- | --- |
| What exact decision or work state is involved? |  |
| Which source controls the highest-risk claim? |  |
| What may the output do before acceptance? |  |
| What must the output not change, send, update, route, apply, or trigger before acceptance? |  |
| Who checks the controlling claim? |  |
| How much review capacity exists? |  |
| What condition requires rejection or escalation? |  |
| Who owns the accepted decision? |  |
| What is logged? |  |
| What happens when it fails? |  |

Recommended mode:

```text
discovery only (no production workflow yet) / draft assistance / reviewed proposal / bounded action
```

## 10. Vendor And Tool Evaluation Checklist

Use this when a vendor or internal platform claims a system is safe, governed,
grounded, or production-ready.

| Area | Required Evidence | Workflow Question |
| --- | --- | --- |
| Model evals | Task-specific failures and regression history | Does this match our workflow? |
| Retrieval | Source display and freshness behavior | Can stale or non-authoritative sources be blocked? |
| Permissions | Role, data, and tool scopes | Do permissions match business authority? |
| Human review | Packet, queue, reject, and escalation states | Can reviewers say no with evidence and time? |
| Audit | Prompt, source, output, action, reviewer, acceptance state | Can we reconstruct what happened? |
| Incident response | Rollback, correction, alerting, disable path | Who owns recovery? |
| Change management | Model, prompt, and corpus-change notice | When does old evidence expire? |

## 11. AI Workflow Antipatterns

Use this when a pilot looks successful but the workflow has not earned the
authority being granted.

| Antipattern | Repair |
| --- | --- |
| Prompt as policy | Add source ownership, freshness rules, and acceptance criteria. |
| Human-in-the-loop decoration | Give reviewers packets, capacity, reject actions, and rejection authority. |
| Evals without acceptance rules | Connect eval failures to workflow controls and incident changes. |
| RAG as freshness | Add source-of-truth ownership, version rules, and stale-source blocks. |
| Generated-volume dashboard | Track accepted verified output and review debt. |
| Named owner without control | Add budget, evidence access, rejection authority, and escalation cover. |
| Governance document without runtime hook | Convert policy into packet fields, permissions, logs, throttles, and stop conditions. |
| Sanctioned path too slow | Create a lower-authority sanctioned path or fund review. |

## 12. First-Workflow Design Canvas

Use this to design the first pilot.

```text
Workflow name:
Decision to support:
Why this workflow:
Authority mode:
What the output may do before acceptance:
What the output must not do before acceptance:

Authority-bearing sources:
- Claim:
  Source:
  Freshness rule:
  Owner:

Required packet:
- Claims:
- Source links:
- Missing-evidence states:
- Reviewer action set:

Review capacity:
- Funded reviewer-hours:
- Expected proposals:
- Average review time:
- Throttle threshold:

Stop conditions:
- Missing source:
- Stale source:
- Contract/policy conflict:
- Review debt:
- Escaped unsupported decision:

Bypass check:
- Likely private-tool path:
- Sanctioned lower-authority alternative:

Pilot schedule:
- Week 0 setup:
- Week 1 reviewed proposals:
- Week 2 capacity and bypass checks:
- Week 3 stop/go decision:

Final decision:
- Continue:
- Narrow:
- Throttle:
- Escalate:
- Stop:
```

## 13. Authority-Pace Checklist

Use this at the end of a pilot or before expanding scope.

| Question | Answer |
| --- | --- |
| What useful work can AI do now? |  |
| What authority is still withheld? |  |
| Which source controls the highest-risk claim? |  |
| What is the current review-capacity limit? |  |
| Which stop condition fired during the pilot? |  |
| Which bypass path appeared? |  |
| What control changed acceptance at least once? |  |
| What automation step are we deliberately rejecting for now? |  |
| What evidence would justify the next authority increase? |  |

Grant authority at the pace the workflow can support.
