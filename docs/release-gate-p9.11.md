# P9.11 Client Demo Transcript Release Gate

Gate date: 2026-05-14

Decision: fail-closed client-demo gate implemented with safe executable replay.
Current P9.11 machine evidence is `evidence_present` for runnable demo transcript
coverage, but this does not approve release or client readiness.

Report scope: this report records the P9.11 transcript contract, current
product/release evidence status, counts, evidence IDs, claim impact, and
residual blockers from adjacent gates. It does not update the implementation
tracker, does not commit generated `.idiom` artifacts, and does not approve
regulated, commercial, packaging, support, or release go claims.

P9.11 passes only when every client classification has exactly one runnable,
privacy-safe transcript with commands, expected output, evidence fields, value
claim, MVP limitation, final-state expansion, privacy/no-forbidden-field
posture, go/no-go criteria, and release claim impact. Replay first validates
that the `command` string and `argv` describe the same invocation, then permits
only known P7.10-safe command shapes. Default-profile shell/write-capable
commands, unsafe top-level subcommands, unsafe flags, backend/lane/release/
product-evidence execution, and unbounded local-ops paths return
`needs_evidence` with structured blockers. Side-effect examples are valid only
as discovery-profile denials or equivalent bounded-denial evidence.

## Evidence Inputs

| Input | Evidence summary | Gate effect |
| --- | --- | --- |
| Transcript fixture | `testdata/sidecar/client-demo-transcripts.json` contains seven transcript rows for `finance`, `healthcare`, `public-sector`, `enterprise-saas`, `tooling-vendor`, `local-model`, and `open-source`. | Supplies P9.11 runnable demo rows. |
| Transcript docs | `docs/client-demo-transcripts.md` points to the fixture and executable `TestP710ClientDemoTranscripts` validation. | Keeps human-readable demo boundaries aligned with the machine fixture. |
| P7.10/P7.17 tests | `go test ./internal/evidence/sidecarvalidation -run 'TestP710|TestP717' -count=1` passes with strict schema and executable command checks. | Confirms existing demo transcript validation still passes. |
| Product evidence export | `go run ./cmd/idiom product-evidence export --project-root .` reports P9.11 `evidence_present`. | Product evidence exposes machine-readable P9.11 status and counts. |
| Release evidence bundle | `go run ./cmd/idiom release evidence --project-root . --output .idiom/release/p9.11-smoke.json` reports P9.11 `evidence_present` while the overall bundle remains `partial`. | Release evidence agrees with product evidence and preserves broader no-go status. |
| P9.29 safe replay tests | Focused P9.11 tests cover command/argv mismatch, unsafe top-level subcommands, default-profile shell/write-capable commands, permitted current fixture commands, and discovery-profile denied side-effect replay without writing a marker file. | Closes the executable-replay safety gap without broadening model strategy control. |

Generated `/tmp` and `.idiom/release` outputs are local reproduction artifacts.
This document stores only command labels, statuses, counts, bounded blockers,
and evidence IDs.

## Gate Result

| Check | Required pass condition | Current status | Gate effect |
| --- | --- | --- | --- |
| Classification coverage | Exactly one transcript for each of the seven classifications. | Pass: `covered_classifications=7`, `missing_classifications=0`, `duplicate_classifications=0`. | P9.11 coverage complete. |
| Runnable commands | Every transcript has at least one `idiom ...` command with argv. | Pass: `runnable_command_rows=7`. | Demo commands are rerunnable. |
| Safe replay boundary | Command text and argv match, replay is allowlisted, and side-effect examples are discovery-profile denials. | Pass: `executable_command_evidence_rows=7`, `command_execution_blockers=0`, `command_id_mismatch_rows=0`. | Transcript-controlled unsafe harness actions are structurally blocked. |
| Expected output | Every transcript declares stable expected output shape. | Pass: `expected_output_rows=7`. | Demo output is reviewable. |
| Required fields | Value claim, evidence fields, MVP limitation, final-state expansion, privacy posture, go/no-go criteria, and release claim impact are present. | Pass: each required row count is `7`. | Fixture is complete for P9.11. |
| Privacy posture | No raw prompts, raw file bodies, private paths/URLs, secrets, PHI, account data, partner identifiers, or hosted telemetry are included. | Pass: `privacy_or_forbidden_blockers=0`. | Keeps demo evidence metadata-only. |
| Regulated rows | Finance, healthcare, and public-sector stay proof-only. | Pass: `regulated_proof_only_rows=3`. | Regulated rows cannot be used as compliance or procurement proof. |
| Primary buyer story | Enterprise SaaS remains the primary buyer-facing story. | Pass: `primary_enterprise_saas_rows=1`. | Prevents all-segments-equal sales language. |
| Overclaim guard | Demo text must not imply regulated or commercial readiness beyond evidence. | Pass: `regulated_or_commercial_overclaims=0`. | Unsupported claims remain blocked. |

Current P9.11 classification: **GO for demo-transcript artifact coverage only**.

## Machine Status

Product and release evidence agree:

```text
P9.11 status: evidence_present
evidence: classes:7/7 runnable:7 expected_output:7 blockers:0
required_classifications: 7
covered_classifications: 7
transcript_rows: 7
runnable_command_rows: 7
expected_output_rows: 7
evidence_field_rows: 7
mvp_limitation_rows: 7
final_state_expansion_rows: 7
privacy_posture_rows: 7
go_no_go_criteria_rows: 7
release_claim_impact_rows: 7
regulated_proof_only_rows: 3
primary_enterprise_saas_rows: 1
privacy_or_forbidden_blockers: 0
regulated_or_commercial_overclaims: 0
executable_command_evidence_rows: 7
command_execution_blockers: 0
command_id_mismatch_rows: 0
blockers: 0
```

Evidence IDs:

```text
p7.10_client_demo_transcripts
p7.10-finance-discovery-denial
finance-discovery-policy
p7.10-healthcare-refresh-denial
healthcare-redaction-denial
p7.10-public-sector-side-effect-denial
public-sector-offline-side-effect-denial
p7.10-enterprise-saas-source-sanity
enterprise-source-sanity-contract
p7.10-tooling-vendor-schema-contract
tooling-vendor-schema-metadata
p7.10-local-model-private-code-readiness
local-model-readiness-evidence
p7.10-open-source-context-pack
open-source-context-pack
```

## Claim Impact

Allowed claim: Idiom has a machine-readable P9.11 gate that validates runnable,
privacy-safe client demo transcripts for all seven classifications with explicit
MVP limitations and final-state expansion notes.

Disallowed claim: P9.11 does not prove regulated compliance, procurement
approval, commercial buyer signal, paid rollout, packaging/support scope,
local-model operational reliability, competitive superiority, or a release go
decision.

## Residual Release Blockers

P9.11 is not the remaining release blocker. The generated P9.11 smoke bundle is
still `partial` because adjacent gates remain incomplete:

- P9.7 needs privacy-safe validation records, retained-use evidence, and a
  credible primary `enterprise-saas` adoption signal.
- P9.9 still reports security/privacy posture blockers in the current smoke.
- P9.10 still needs external competitive baseline coverage or documented
  infeasibility.
- All seven release client-readiness rows still need validation records.
- P9.12 packaging/support and ICP decision remains no-go until downstream
  evidence is reviewed.

## Reproduction Commands

```sh
go test ./internal/evidence/productevidence -run 'TestP911|TestP91|TestP94|TestP711|TestRunReleaseEvidence|TestRunProductEvidence' -count=1
go test ./internal/evidence/sidecarvalidation -run 'TestP710|TestP717' -count=1
go test ./internal/surfaces/cli/mcp -run TestP85 -count=1
go test ./cmd/idiom -count=1

jq empty testdata/sidecar/client-demo-transcripts.json

go run ./cmd/idiom product-evidence export --project-root . --output .idiom/product-evidence/p9.11-product-evidence.json
jq '.release_gate_linkage[] | select(.gate=="P9.11")' .idiom/product-evidence/p9.11-product-evidence.json

go run ./cmd/idiom release evidence --project-root . --output .idiom/release/p9.11-smoke.json
jq '.release_readiness[] | select(.gate=="P9.11")' .idiom/release/p9.11-smoke.json

git diff --check
```

Expected current result: product and release evidence both report P9.11
`evidence_present` with seven covered classifications, seven runnable command
rows, seven expected-output rows, zero blockers, and matching evidence IDs.
Overall release output remains partial/no-go because other P9 gates still need
evidence.
