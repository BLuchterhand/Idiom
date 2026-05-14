# P9.6 Activation/Onboarding Release Gate

Gate date: 2026-05-14

Decision: no-go, needs measured clean-install activation evidence. P9.6 does not
support a release activation/onboarding claim yet.

Artifact acceptance scope: this report can be accepted only as a privacy-safe,
fail-closed activation gate artifact. It records the current P0.10/P7.7/P7.16
quickstart evidence, P7.11 product-evidence linkage, P9.1 release-bundle
consumption, segment impact, and release criteria. It does not complete P9.7
design-partner/retained-use proof or P9.11 runnable demo proof.

P9.6 passes only when a current release bundle consumes activation evidence that
proves measured clean install or clean setup to first useful value, cleanup
success, privacy-safe export, no hidden setup, bounded assistance, recorded docs
gaps or blocker reasons, and no raw sensitive content. Current evidence fails
closed because the quickstart smoke is source-build/local-binary evidence with
deterministic step timings, not measured wall-clock clean-install evidence.

## Evidence Inputs

| Input | Command | Privacy-safe result | Gate effect |
| --- | --- | --- | --- |
| P9.6 context packet | `idiom tool context_pack --input '{"task":"P9.6 activation onboarding evidence install to first value cleanup gate","mode":"plan","limit":10}'` | Fresh packet `8955578b8a244a38`, index `9366f65e1655a483`; candidate files included `cmd/idiom/local_ops.go`, `cmd/idiom/product_evidence.go`, and `cmd/idiom/release_evidence.go`; two unrelated stale runtime-trace gaps were reported. | Localization evidence only. |
| P7.7/P7.16 quickstart smoke | `go run ./cmd/idiom local-ops benchmark --json --quickstart-smoke --quickstart-cleanup remove-smoke-artifacts --output .idiom/local-ops/report.json` | Wrote ignored local report. Quickstart `71c2e10108799132` passed with source-build/local-binary setup, assistance `none`, first packet in deterministic 2 seconds, trust evidence in deterministic 6 seconds, product-safe activation export `p77-activation-f4b61f69ef2b73d8`, forbidden-field check `pass`, and cleanup `pass` for one smoke artifact. | Supports source-build/fixture activation evidence, but not release clean-install activation claims. |
| First useful value | Same quickstart smoke | Packet `0ac1fb428fbf7965`, index `9366f65e1655a483`, freshness state `fresh`, ranked counts `candidate_files:4`, `candidate_symbols:8`, `candidate_tests:4`. | First useful packet evidence exists for the smoke. |
| Context/freshness | Same quickstart smoke | `context_status` status `pass`, state `fresh`, stale path count `0`; scope/global freshness booleans were false in the smoke payload, so this is not broad freshness signoff. | Sufficient for smoke evidence, not enough for broad release freshness claims. |
| Audit/trust evidence | Same quickstart smoke | Harness audit status `pass`, audit report ID `930ce444069441ff`, high findings `0`, medium findings `2`, total findings `11`; event IDs are hashed. | Trust evidence exists, but medium findings remain P9.3/P9.9 closure context. |
| Suggested verification | Same quickstart smoke | Suggested verification labels were `go-test`; commands were redacted and not run by the smoke. | P9.6 requires release validation to run focused tests separately. |
| P7.11 product-evidence export | `go run ./cmd/idiom product-evidence export --project-root . >/tmp/idiom-p9.6-product-evidence.json` | Export `product-evidence-58ebeb545012d76d`, artifact hash `58ebeb545012d76d`, status `partial`, activation signal `pass/complete`, privacy forbidden-field check `pass`; standalone P9.6 release-gate linkage downgrades to `needs_evidence` for source-build deterministic smoke. | Product export consumes useful activation evidence without overclaiming release activation readiness. |
| P9.1 release evidence bundle | `go run ./cmd/idiom release evidence --project-root . --output .idiom/release/p9.6-smoke.json` | Bundle `release-evidence-dec159e0fbecc1b6`, artifact hash `dec159e0fbecc1b6`, top-level status `partial`, privacy check `pass`. Activation onboarding status is `needs_evidence`, classification `no-go`; P9.6 readiness is `needs_evidence`. | Current release bundle now consumes and downgrades activation evidence instead of overclaiming readiness. |

Ignored `.idiom` and `/tmp` outputs are local reproduction artifacts only. This
document stores summaries, IDs, hashes, counts, statuses, and command labels.

## Gate Result

| Check | Required pass condition | Current status | Gate effect |
| --- | --- | --- | --- |
| Clean install/setup status | A current bundle records clean install or clean setup from documented prerequisites, with release binary proof when release activation is claimed. | Fail closed: setup is `source-build-or-local-binary`; `release_install_proof` is `not_claimed`. | Blocks release activation claims. |
| Measured install-to-first-value | Wall-clock time from setup start to first useful packet and trust evidence is measured and within the selected segment threshold. | Fail closed: quickstart reports deterministic command-step timing, not measured wall-clock timing. | Blocks release activation claims. |
| First useful value | Packet has `packet_id`, `index_id`, ranked evidence, and freshness state. | Pass for smoke: packet and ranked counts are present. | Supports source-build/fixture evidence only. |
| Freshness/context | Freshness status is explicit and stale evidence is not silently accepted. | Partial: state is `fresh` and stale path count is `0`, but scope/global booleans were false in the smoke payload. | Needs broader freshness confirmation before release claims. |
| Audit/trust evidence | Harness audit report is present, IDs are metadata-only, and findings are summarized. | Partial/pass for smoke: audit ID and counts are present; medium findings remain. | Does not close P9.3/P9.9. |
| Suggested verification | Relevant verification is suggested and release validation records whether it ran. | Partial: suggestions exist; smoke did not run them. Focused tests were run separately for this implementation. | Must be included in final release validation evidence. |
| Product-evidence-safe output | Export/check uses IDs, hashes, statuses, and redaction fields only; forbidden-field check passes. | Pass for smoke/export: quickstart and product export forbidden-field checks passed. | Privacy invariant preserved. |
| Failure reason and docs gap | Failures, blockers, or docs gaps are classified without hidden tribal knowledge. | Partial: smoke records no README quickstart gap, but this gate records release-binary measurement and wall-clock timing gaps. | Blocks release claim until recorded evidence exists. |
| Cleanup path | Cleanup succeeds and remaining local state is absent or intentionally retained by reviewed policy. | Partial for smoke artifact cleanup: mode `remove-smoke-artifacts`, exit `pass`, removed count `1`, context graph remains, but the release classifier requires explicit retained-state policy evidence before P9.6 can pass. Full uninstall/local-state cleanup is not measured here. | Blocks release activation claims. |
| Privacy/redaction status | No raw prompts, protocol bodies, file bodies, absolute private paths, sentinels, secrets, tokens, credentials, unredacted logs, hosted telemetry dependency, or ignored bundle bodies. | Pass: local-ops, product export, release bundle, and this report use metadata-only summaries. | Required invariant preserved. |
| Assistance class | Pass requires `none`, docs-only, or one operator typo correction. | Pass for smoke: `none`. | Supports source-build/fixture evidence only. |
| Release bundle consumption | P9.1 bundle must consume the activation evidence and fail closed if P9.6 details are incomplete. | Pass for gate mechanics: release bundle records `activation_onboarding.status=needs_evidence` and P9.6 readiness `needs_evidence`. | Correct no-go release posture. |

Current P9.6 classification: **NO-GO for release activation claims**.

## Segment Impact

| Client classification | Activation status and impact |
| --- | --- |
| `finance` | NO-GO. Regulated proof-only; source-build activation smoke does not prove governed install, cleanup, audit closure, retention, or control evidence. No finance compliance claim. |
| `healthcare` | NO-GO. Privacy-safe shape is preserved, but no measured clean-install activation or healthcare control posture is proved. No HIPAA or PHI-processing claim. |
| `public-sector` | NO-GO. Local/offline-friendly evidence exists as a shape, but release activation still lacks measured clean setup, reproducible bundle validation, and procurement/security posture. |
| `enterprise-saas` | NO-GO. Primary paid beachhead cannot cite activation readiness until measured clean-install evidence, validation records, and P9.7 buyer/retained-use proof exist. |
| `tooling-vendor` | NO-GO for release activation. Parseable evidence and product export linkage help integration review, but clean install, wall-clock activation, and conformance packaging remain unproved. |
| `local-model` | NO-GO for broad release. Source-build/local evidence helps a local-ops preview, but model/backend readiness and measured local setup are not enough for release activation claims. |
| `open-source` | PARTIAL, no-go for release. The quickstart smoke proves a source-build/fixture path with first packet, audit, privacy-safe output, and smoke cleanup; it does not prove release-binary install-to-first-value or full cleanup/uninstall. |

All seven P9.1 client-readiness rows in the fresh bundle remain
`needs_evidence` because validation records are missing.

## Required Closure

P9.6 can be reconsidered only after a fresh release review records all of the
following:

- measured wall-clock clean install or clean setup to first useful value, with
  release-binary proof for release activation claims or an explicit narrowed
  source-build/internal-preview scope;
- first useful packet with packet/index IDs, ranked evidence, useful/no-result
  classification, and explicit freshness state;
- freshness check, harness audit report, suggested verification, product-safe
  export, failure reason, docs gap, and cleanup result in the activation record;
- cleanup success for generated smoke artifacts and either full local-state
  cleanup/uninstall proof or a reviewed retained-state policy;
- no hidden setup, source edits, pre-seeded graph state, undisclosed commands, or
  assistance above allowed class;
- privacy-safe product evidence and release bundle output with forbidden-field
  checks passing;
- docs gaps recorded with doc path or missing doc, command label, expected and
  actual outcome, severity, and owner/follow-up when known;
- current release bundle consumes the activation evidence and keeps P9.6
  `needs_evidence` or `blocked` until every required field passes.

P9.7 still owns design-partner validation, retained use, and paid/internal
platform signal. P9.11 still owns runnable client demo transcripts and MVP/final
state limitations.

## Reproduction Commands

```sh
go test ./cmd/idiom -run 'TestP96|TestP91|TestP711|TestRunReleaseEvidence|TestRunProductEvidence|TestLocalOpsBenchmarkQuickstart|TestLocalOpsForbidden' -count=1

go run ./cmd/idiom local-ops benchmark --json --quickstart-smoke --quickstart-cleanup remove-smoke-artifacts --output .idiom/local-ops/report.json

go run ./cmd/idiom product-evidence export --project-root . >/tmp/idiom-p9.6-product-evidence.json
jq -r '[.status,.export_id,.artifact_hash,.product_success_gates.activation,.signal_completeness.status,.privacy.forbidden_field_check] | @tsv' /tmp/idiom-p9.6-product-evidence.json
jq -r '.signals[] | select(.id=="activation") | [.status,.completeness,((.evidence_ids // [])|join(",")),(.counts.candidate_files|tostring),(.counts.candidate_symbols|tostring),(.counts.candidate_tests|tostring)] | @tsv' /tmp/idiom-p9.6-product-evidence.json
jq -r '.release_gate_linkage[] | select(.gate=="P9.6") | [.gate,.status,.evidence,.claim_impact] | @tsv' /tmp/idiom-p9.6-product-evidence.json

go run ./cmd/idiom release evidence --project-root . --output .idiom/release/p9.6-smoke.json
jq -r '[.status,.bundle_id,.artifact_hash,.activation_onboarding.status,.activation_onboarding.classification,.activation_onboarding.quickstart_status,.activation_onboarding.cleanup_status,.activation_onboarding.release_install_proof,.activation_onboarding.duration_source,.product_evidence.status,.privacy.forbidden_field_check] | @tsv' .idiom/release/p9.6-smoke.json
jq -r '.release_readiness[] | select(.gate=="P9.6") | [.gate,.status,.evidence,.claim_impact] | @tsv' .idiom/release/p9.6-smoke.json
jq -r '.activation_onboarding.blockers[]? | [.section,.reason,.evidence] | @tsv' .idiom/release/p9.6-smoke.json
jq -r '.client_readiness[] | [.classification,.status,((.missing_evidence // [])|join(","))] | @tsv' .idiom/release/p9.6-smoke.json

git diff --check
```

Expected current result: quickstart smoke passes as source-build/fixture
evidence, product export remains partial with activation complete, standalone
product P9.6 linkage is `needs_evidence`, release bundle remains partial, P9.6
readiness is `needs_evidence`, activation classification is `no-go`, and the
explicit blockers include missing release-binary clean-install proof,
deterministic rather than measured wall-clock timing, and incomplete reviewed
retained-state cleanup semantics.
