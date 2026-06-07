# P9.7 Design-Partner Validation Release Gate

Gate date: 2026-05-14

Decision: no-go, needs design-partner validation, retained-use evidence, and a
credible paid or internal-platform adoption signal. P9.7 does not support a
release demand, paid-readiness, or commercial-success claim yet.

Artifact acceptance scope: this report can be accepted only as a privacy-safe,
fail-closed release-gate artifact. It records the P0.8/P7.11 evidence contract,
the current generated P9.7 machine-readable status, including structured
`counts` and `blockers`, the P9.21 evidence-ID contract for counted records,
and the claim impact of the missing evidence. It does not update the
implementation tracker, does not commit generated `.idiom` artifacts, and does
not approve release.

P9.7 passes only when privacy-safe P0.8 validation records prove all of the
following:

- at least five complete validation records;
- at least three complete `enterprise-saas` primary-ICP records from at least
  two contexts;
- at least two primary records with activation, trust/security, and workflow
  value scores at or above the plan threshold;
- at least two primary records with daily-user activation attempts;
- at least one record-backed retained-use signal tied to substrate value;
- at least one credible paid, budget-owner, buyer, security/platform approver,
  or internal-platform adoption signal from a primary-ICP record.

Current evidence fails closed because no validation-record file was supplied to
the local product-evidence or release-evidence smoke commands.

## Evidence Inputs

| Input | Command | Privacy-safe result | Gate effect |
| --- | --- | --- | --- |
| P7.11 product-evidence export | `go run ./cmd/idiom product-evidence export --project-root .` or with a local `--output` path | Generated product evidence reports status `partial`, privacy forbidden-field check `pass`, and a P9.7 release-gate linkage of `needs_evidence`. Regenerated export IDs and artifact hashes are intentionally not pinned in this report. | P9.7 linkage is `needs_evidence`. |
| P9.1 release evidence bundle | `go run ./cmd/idiom release evidence --project-root . --output .idiom/release/p9.7-smoke.json` | Generated release evidence reports status `partial`, privacy forbidden-field check `pass`, and P9.7 release readiness of `needs_evidence`. Regenerated bundle IDs, product export IDs, and artifact hashes are intentionally not pinned in this report. | Release readiness P9.7 is `needs_evidence`. |
| P9.7 generated status parity | Product and release evidence both report structured `counts` with `records:0 complete:0 primary:0 contexts:0 retained:0 adoption:0` and structured blocker arrays. No P9.7 `evidence_ids` are emitted in the current smoke because no validation records were supplied; when records are counted, P9.21 requires evidence IDs to be exposed by the product and release outputs. | Both surfaces agree that current P9.7 is no-go. |
| P9.2 release decision | `docs/release-decision-p9.2.md` records release no-go until P9.3-P9.12 reviewed gates pass. | Preserves no-go scope. |
| P0.8/P0.11 plan contract | P0.8 defines primary ICP, retained-use format, forbidden fields, and P9.7 thresholds; P0.11 prevents pricing/packaging claims without P9.7 evidence. | Source of gate criteria. |

Generated `.idiom` outputs are ignored local reproduction artifacts. This
document stores command labels, counts, statuses, privacy results, and bounded
blocker summaries, but does not pin volatile generated artifact IDs or hashes.

## Gate Result

| Check | Required pass condition | Current status | Gate effect |
| --- | --- | --- | --- |
| Privacy-safe validation records | Complete records must be redacted/pass and must not contain forbidden fields. | Missing: no validation-record file supplied. | Blocks release. |
| Overall P0.8 record count | At least five complete privacy-safe records. | Fail: `complete:0`. | Blocks demand and release-readiness claims. |
| Primary ICP count | At least three complete `enterprise-saas` records. | Fail: `primary:0`. | Blocks paid enterprise-SaaS demand claim. |
| Primary context diversity | Primary records must span at least two contexts. | Fail: `contexts:0`. | Blocks design-partner validation claim. |
| Primary score threshold | At least two primary records must meet activation, trust/security, and workflow-value thresholds. | Fail: no primary scored records. | Blocks buyer-valued workflow claim. |
| Daily-user activation | At least two primary records must include daily-user activation attempts. | Fail: no primary activation records. | Blocks primary ICP usage claim. |
| Retained use | At least one qualifying record must show repeated use tied to saved packet, freshness, audit, verification, integration, or evidence-export substrate value. | Fail: `retained:0`. | Blocks retained-use claim. |
| Paid/internal-platform adoption | At least one primary record must include credible buyer, budget-owner, paid pilot, procurement, platform-roadmap, security/platform approver, or internal-platform signal. | Fail: `adoption:0`. | Blocks paid-readiness and internal-platform adoption claims. |
| Supporting/open-source records | Open-source records may support activation, docs, demos, or retained-use confidence, but cannot replace primary paid demand. | No supporting records supplied in current smoke. | No paid-demand credit. |
| Product/release agreement | Product evidence P9.7 linkage and release readiness must agree. | Pass for gate mechanics: both report `needs_evidence` with the same counts. | Honest no-go posture preserved. |

Current P9.7 classification: **NO-GO for release demand and paid-readiness
claims**.

## Claim Impact

No current release material may claim:

- validated paid `enterprise-saas` demand;
- design-partner continuation criteria met;
- retained use proved from product export generation alone;
- open-source activation as a substitute for paid enterprise-SaaS demand;
- pricing, procurement, paid pilot, or internal-platform rollout readiness.

The allowed claim is narrower: Idiom has a machine-readable, privacy-preserving
P9.7 gate that fails closed until qualifying validation records are supplied.

## Client-Classification Impact

| Client classification | P9.7 impact |
| --- | --- |
| `finance` | Proof-only/no-go. Regulated interest cannot count as primary paid demand and no validation record is supplied. |
| `healthcare` | Proof-only/no-go. No PHI, HIPAA, or regulated buyer claim; no validation record is supplied. |
| `public-sector` | Proof-only/no-go. No procurement, certification, or public-sector adoption claim; no validation record is supplied. |
| `enterprise-saas` | NO-GO. This is the primary paid beachhead, but the required primary records, retained use, and credible adoption signal are missing. |
| `tooling-vendor` | Secondary/no-go for P9.7. Integration interest can inform P9.8/P9.12 pivot evidence, but it cannot replace primary ICP demand. |
| `local-model` | Secondary/no-go for P9.7. Local readiness evidence can support privacy/locality, not paid beachhead validation. |
| `open-source` | Adoption wedge/no-go for paid proof. Open-source evidence can support activation/docs/demo/retained-use confidence only when recorded safely; it cannot replace enterprise-SaaS paid demand. |

All seven generated release client-readiness rows currently remain
`needs_evidence` with missing `validation record`.

## Required Closure

P9.7 can be reconsidered only after a fresh release review supplies a
privacy-safe validation-record input and verifies:

- forbidden-field scan passes before records are counted;
- disqualified, withdrawn, draft, unsafe, malformed, or non-qualifying records
  do not count;
- adoption values such as `none`, `not_available`, empty, or open-source-only
  wedge signals do not satisfy P9.7;
- retained use is backed by record IDs and tied to substrate value, not inferred
  from product export generation;
- product-evidence P9.7 linkage and release-evidence P9.7 readiness agree;
- P9.12 packaging/support language remains no-go or experimental until this gate
  and the P9.12 decision pass.

## Reproduction Commands

```sh
go test ./internal/evidence/productevidence -run 'TestP97|TestP711|TestRunReleaseEvidence|TestRunProductEvidence' -count=1
go test ./internal/evidence/sidecarvalidation -run 'TestP76|TestP710' -count=1
go test ./cmd/idiom -count=1

go run ./cmd/idiom product-evidence export --project-root . --output .idiom/product-evidence/p9.7-product-evidence.json
jq -r '.release_gate_linkage[] | select(.gate=="P9.7") | [.gate,.status,.evidence,.claim_impact] | @tsv' .idiom/product-evidence/p9.7-product-evidence.json

go run ./cmd/idiom release evidence --project-root . --output .idiom/release/p9.7-smoke.json
jq -r '.release_readiness[] | select(.gate=="P9.7") | [.gate,.status,.evidence,.claim_impact] | @tsv' .idiom/release/p9.7-smoke.json
jq -r '.client_readiness[] | [.classification,.status,((.missing_evidence // [])|join(","))] | @tsv' .idiom/release/p9.7-smoke.json

git diff --check
```

Expected current result: product and release evidence both report P9.7
`needs_evidence`; the structured `counts` show zero validation, primary,
retained-use, and adoption counts; structured `blockers` list the missing P0.8
validation, primary-ICP, activation, retained-use, and adoption evidence; all
seven client-readiness rows still need a validation record; and the privacy
forbidden-field check remains `pass`.
