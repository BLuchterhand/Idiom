# P9.9 Security/Privacy Posture Release Gate

Gate date: 2026-05-14

Decision: no-go, needs reviewed closure evidence. P9.9 does not support a
regulated-release, security/privacy readiness, or formal regulated posture claim.

Artifact acceptance scope: this report can be accepted only as a privacy-safe,
fail-closed release-gate artifact. It records the current posture artifact,
threat model, data-flow map, privacy/retention posture, protected-path behavior,
telemetry minimization, side-effect policy, MCP/resource containment,
control-mapping coverage, and release-gate tiebacks. It does not update the
implementation tracker, does not commit generated `.idiom` artifacts, and does
not approve release.

Reviewer signoff: no-go.

P9.9 passes only when the posture bundle has reviewer GO and current reproducible
evidence for all of the following:

- P0.9 threat model, data-flow map, privacy/retention posture, source dates, and
  control mapping;
- P7.9 posture matrix with every required control mapped to all seven client
  classifications where applicable;
- protected-path behavior, telemetry minimization/redaction, side-effect policy,
  MCP/resource containment, and forbidden-evidence-field coverage;
- P8.4/P8.6 MCP/resource parity evidence and P9.8 integration proof tieback;
- P9.1 release evidence and P9.3 audit/security gate evidence with no unresolved
  high security or contract blockers;
- explicit reviewer GO or an explicit no-go/pivot record.

Current evidence fails closed because this report records no-go, the P9.3 gate
still records open high contract findings, the P9.1 bundle remains partial in
the P9.2 decision, P0.9 source/date anchors still need structured checked-date
and verification-method evidence, and current generated product/release evidence
must still be reviewed after this P9.9 implementation.

## Evidence Inputs

| Input | Evidence summary | Gate effect |
| --- | --- | --- |
| P0.9 posture section | `docs/idiom-implementation-plan.md` contains the threat model, data-flow map, privacy/retention posture, checked source anchors, downstream gate rules, and control mapping. | Required artifact source; not edited by this task. |
| P7.9 posture artifact | `docs/security-privacy-posture.md` describes reviewable controls and the reviewer command. | Required posture source. |
| P7.9 matrix | `docs/security-privacy-posture.matrix.json` is machine-readable and now includes the MCP/resource containment control plus P8.4/P9.1/P9.3/P9.9 tiebacks. | Required machine source. |
| P9.2 decision | `docs/release-decision-p9.2.md` records that release remains no-go until P9.3-P9.12 gates close. | Blocks release claims. |
| P9.3 gate | `docs/release-gate-p9.3.md` records no-go with open high contract findings. | Blocks P9.9 readiness. |
| P9.8 gate | `docs/release-gate-p9.8.md` records current integration proof presence only. | Supports MCP/integration tieback but does not override P9.9 blockers. |

Generated `.idiom` and `/tmp` outputs are local reproduction artifacts only. This
report stores command labels, statuses, counts, and bounded blocker summaries.

## Gate Result

| Check | Required pass condition | Current status | Gate effect |
| --- | --- | --- | --- |
| Threat model | P0.9 threat surface list exists and ties release/audit failure modes to controls. | Present as source artifact. | Does not close gate without reviewer GO and current dependent evidence. |
| Data-flow map | P0.9 flow map names actors, data classes, storage/artifacts, trust boundaries, and minimization rules. | Present as source artifact. | Does not close gate without current evidence. |
| Privacy/retention posture | P0.9/P7.9 states local-only evidence posture, forbidden fields, retention limitations, and release blockers. | Present, with retention limitations still explicit. | Blocks any claim that retention is fully enforced. |
| Protected paths and redaction | P7.9 matrix and tests cover protected/forbidden path denial and redacted summaries. | Present for CLI/shared resolver evidence. | Needs release review plus P9.3 closure. |
| Telemetry minimization | P7.9 matrix covers metadata-only/local evidence, no hosted default, and redacted telemetry arguments. | Present for current fixtures/tests. | Needs release review. |
| Side effects | P7.9 matrix covers discovery-profile write/shell/destructive/open-world denial. | Present for CLI sidecar evidence. | Needs release review. |
| MCP/resource containment | P7.9 matrix points to MCP stdio replay and classification-parity tests. | Present as fixture-level evidence; broader resource body export and non-stdio auth remain out of scope. | Supports proof-only MCP/resource story only. |
| Client-row mapping | All seven classifications are present in the matrix. | Present. Regulated rows remain proof-only/deferred. | No regulated sales readiness claim. |
| Dependent release gates | P9.1, P9.3, P8.4/P9.8, and P7.9 evidence must be current and passing or explicitly no-go. | Fail closed: P9.2/P9.3 are no-go/partial. | Blocks release. |
| Source verification | P0.9 source/date anchors must include structured source IDs, checked dates, verification methods, and an unreachable-source method when a source could not be reached by one command path. | Needs evidence in the current tracker text. | Blocks source/date and regulated-anchor claims. |
| Product privacy/export status | P9.9 consumes the final product-evidence privacy/export status and blocks on failed forbidden-field checks, blocked exports, true raw-prompt/file-body/secret/private-path flags, or privacy-failed validation records. | Current expected export remains privacy-safe, but this gate now fails closed if that changes. | Blocks P9.9 if product evidence is privacy-blocked. |
| Reviewer result | Reviewer GO is required for readiness; explicit no-go is allowed as fail-closed artifact completion. | No-go. | Blocks P9.9 readiness. |

Current P9.9 classification: **NO-GO for release security/privacy readiness and
regulated claims**.

## Claim Impact

Allowed claim: Idiom now has a machine-readable P9.9 gate that fails closed and
reports posture coverage, client-row mapping, blockers, and evidence IDs.

Disallowed claim: P9.9 does not prove regulated sales readiness, formal
third-party approval, hosted telemetry safety, complete retention enforcement,
MCP conformance, or release GO.

## Client-Classification Impact

| Client classification | P9.9 impact |
| --- | --- |
| `finance` | Proof-only/no-go. Protected-path, audit, and retention posture remain evidence inputs, not a regulated buyer claim. |
| `healthcare` | Proof-only/no-go. Synthetic/redacted fixture posture only; no real sensitive-data handling claim. |
| `public-sector` | Proof-only/no-go. Offline/root-containment evidence remains bounded by unresolved release gates and support posture. |
| `enterprise-saas` | No-go for release security/privacy readiness until P9.3 closes and release evidence is current. |
| `tooling-vendor` | Integration proof helps substrate review, but MCP/resource containment remains bounded and no conformance claim is made. |
| `local-model` | Local/private posture evidence exists, but model/backend operational reliability and retention enforcement remain bounded. |
| `open-source` | Transparent local evidence is available, but it does not replace review closure for release readiness. |

## Required Closure

P9.9 can be reconsidered only after a fresh review verifies:

- product and release evidence both report P9.9 `evidence_present` with zero
  P9.9 blockers;
- P9.3 has no unresolved high security/contract findings or has an accepted
  no-go/pivot scope that removes the release claim;
- P9.1 release evidence consumes current validation, audit, posture, product,
  integration, and privacy-safe summary inputs;
- P0.9 source/date evidence includes structured `source_id`, `checked_date`,
  `verification_method`, and any required unreachable-source verification method;
- P8.4/P8.6/P9.8 MCP/resource evidence is current and no broader than CLI;
- matrix client rows remain exactly the seven classifications and regulated rows
  remain proof-only/deferred;
- no posture/report/product summary contains raw prompts, raw file bodies,
  private paths or URLs, secrets, sensitive regulated data, account data, or
  partner-identifying details;
- no release material asserts regulated readiness or formal approval beyond the
  proof-only evidence actually present.

## Reproduction Commands

```sh
go test ./cmd/idiom -run 'TestP99|TestP79|TestP84|TestP91|TestP93|TestP711|TestRunReleaseEvidence|TestRunProductEvidence' -count=1
go test ./cmd/idiom -count=1

jq empty docs/security-privacy-posture.matrix.json

go run ./cmd/idiom product-evidence export --project-root . >/tmp/idiom-p9.25-product-evidence.json
jq -r '.release_gate_linkage[] | select(.gate=="P9.9") | [.gate,.status,.evidence,((.evidence_ids // [])|join(",")),((.blockers // [])|length|tostring)] | @tsv' /tmp/idiom-p9.25-product-evidence.json

go run ./cmd/idiom release evidence --project-root . --output .idiom/release/p9.25-smoke.json
jq -r '.release_readiness[] | select(.gate=="P9.9") | [.gate,.status,.evidence,((.evidence_ids // [])|join(",")),((.blockers // [])|length|tostring)] | @tsv' .idiom/release/p9.25-smoke.json

git diff --check
```

Expected current result: product and release evidence report P9.9
`needs_evidence`; counts expose control/client-row/tieback coverage; blockers
include the explicit P9.9 no-go and dependent P9.1/P9.3 no-go or partial
evidence plus a structured source-verification blocker until P0.9 source/date
records carry checked dates and verification methods; privacy forbidden-field
checks remain `pass`; and the overall release status remains no-go/partial until
later gates close.
