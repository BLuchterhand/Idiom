# AI Workflow Pilot Kit

Use this kit to design and run one AI workflow pilot without granting the
system more authority than the workflow can support.

The kit is built for engineering managers, product leads, platform teams, risk
partners, and reviewers who need to decide whether AI output may remain a draft,
become a reviewed proposal, or take bounded action.

Let the model explore, summarize, draft, and propose. Make the workflow own
permissions, source access, freshness, verification, acceptance, audit, and
failure handling.

The support-credit example is a composite design scenario, not a documented
deployment or a claim about typical support operations.

## Start Here

1. Read `00-start-here.md`.
2. Run the workshop in `01-90-minute-workshop-guide.md`.
3. Fill in the required templates:
   - `templates/decision-acceptance-contract.md`
   - `templates/claim-to-evidence-matrix.md`
   - `templates/reviewer-packet.md`
   - `templates/effect-boundary-spec.md`
   - `spreadsheets/review-capacity-scorecard.csv`
4. Set the operating path:
   - `03-pilot-runbook.md`
   - `04-threshold-setting-guide.md`
   - `spreadsheets/pilot-log.csv`
5. Use the supporting templates when they apply:
   - `templates/control-validation-loop.md`
   - `templates/vendor-tool-evaluation-checklist.md`
   - `templates/authority-pace-checklist.md`
   - `templates/review-capacity-scorecard.md`
   - `templates/workflow-readiness-preflight.md`
   - `templates/source-freshness-map.md`
   - `templates/citation-honesty-ledger.md`
   - `templates/responsibility-incentive-map.md`
   - `templates/first-workflow-design-canvas.md`
6. Compare against the filled examples in `examples/`.
7. Run the pilot using `02-pilot-operating-guide.md`.
8. Decide whether to continue, narrow, throttle, escalate, or stop.

## Authority Mode Ladder

Use these terms consistently:

| Mode | Meaning | Typical First Control |
| --- | --- | --- |
| Discovery only | The team is still finding the decision, owner, source, or risk boundary. | Source-owner backlog and workshop notes |
| Draft assistance | AI drafts or summarizes, but no reviewed workflow or operational effect exists. | Data boundary and user responsibility |
| Reviewed proposal | AI prepares a recommendation that a reviewer may accept, reject, return, escalate, or block. | Claim matrix and reviewer packet |
| Bounded action | AI may change a permitted workflow state after acceptance. | Effect boundary, logs, rollback, and incident path |

Individual low-authority drafting use belongs under draft assistance.

## How This Maps To The Book

| Book Contract Piece | Kit Artifact |
| --- | --- |
| Task-fit diagnostic | `00-start-here.md`; `templates/workflow-readiness-preflight.md` |
| Source-of-truth and freshness map | `templates/source-freshness-map.md`; `templates/claim-to-evidence-matrix.md` |
| Claim-to-evidence matrix | `templates/claim-to-evidence-matrix.md` |
| Citation honesty and caveats | `templates/citation-honesty-ledger.md`; `templates/claim-to-evidence-matrix.md` |
| Responsibility and incentive map | `templates/responsibility-incentive-map.md`; `03-pilot-runbook.md` owner tables |
| Tool inventory and effect boundary | `templates/effect-boundary-spec.md` |
| Verification and reviewer capacity | `spreadsheets/review-capacity-scorecard.csv`; `04-threshold-setting-guide.md` |
| Reviewer packet | `templates/reviewer-packet.md` |
| Shadow-AI and bypass loop | `templates/control-validation-loop.md`; `02-pilot-operating-guide.md` |
| Eval, incident, and governance loop | `templates/control-validation-loop.md` |
| First-workflow design canvas | `templates/first-workflow-design-canvas.md`; `01-90-minute-workshop-guide.md` |

## Minimum Usable Output

The pilot is not ready until the team can name:

- the exact decision or work state;
- what AI may do before acceptance;
- what AI must not change, send, update, route, apply, or trigger;
- the source map for the highest-risk claims;
- reviewer capacity and queue threshold;
- reject, return-incomplete, escalate, and stop conditions;
- the owner after acceptance;
- the evidence that would justify increasing authority.

## Included Examples

- `examples/support-credit-filled.md`: filled composite operations example using
  the book's running support-credit scenario.
- `examples/ai-code-review-filled.md`: a software-engineering pilot example.
- `examples/discovery-only-source-ownership-filled.md`: a workflow that should
  not enter a pilot yet.
- `examples/draft-assistance-filled.md`: a low-authority drafting workflow that
  avoids review burden.
- `examples/bounded-action-filled.md`: a bounded action workflow with
  permission, rollback, and incident controls.

## Spreadsheet Files

Use `spreadsheets/ai-workflow-pilot-workbook.xlsx` when you want a multi-tab
workbook. The CSV files in `spreadsheets/` open in Google Sheets, Excel, or
Numbers when a team wants one table per file. Keep the formulas visible. Do not
replace formula cells with static numbers during a pilot review.

Spreadsheet files included:

- `spreadsheets/ai-workflow-pilot-workbook.xlsx`
- `spreadsheets/review-capacity-scorecard.csv`
- `spreadsheets/review-capacity-scorecard-sample.csv`
- `spreadsheets/pilot-log.csv`
- `spreadsheets/claim-to-evidence-matrix.csv`
- `spreadsheets/citation-honesty-ledger.csv`
- `spreadsheets/control-validation-loop.csv`
- `spreadsheets/authority-pace-checklist.csv`
- `spreadsheets/workflow-readiness-preflight.csv`
- `spreadsheets/source-freshness-map.csv`
- `spreadsheets/responsibility-incentive-map.csv`
- `spreadsheets/first-workflow-design-canvas.csv`

## Operating Rule

AI output starts as a proposal. A workflow accepts it for use only after
required evidence is checked and a named owner accepts the decision or
authorizes the permitted effect.
