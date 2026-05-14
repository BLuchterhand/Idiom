# Security And Privacy Posture

This is the P7.9 posture artifact for Idiom's local sidecar and coding-agent
harness. It is written for enterprise and regulated reviewers, but every claim
below is tied to executable checks or fixture evidence. It is not a SOC 2,
HIPAA, FINRA, FedRAMP, DoD, or NIST attestation.

The machine-checkable validation matrix is
[`docs/security-privacy-posture.matrix.json`](security-privacy-posture.matrix.json).
The P0.9 threat model, data-flow map, and control mapping remain in
`docs/idiom-implementation-plan.md`; this document does not update tracker
status.

## Evidence Boundary

Idiom's posture is contract-first:

- The harness owns protected paths, redaction, local artifact scope, telemetry
  minimization, side-effect policy, and readiness contracts.
- The model owns strategy and synthesis within those contracts.
- Durable evidence should use paths, IDs, hashes, counts, status fields,
  command labels, and policy codes. It should not use raw prompts, raw file
  bodies, secrets, private repo URLs, customer identifiers, patient names,
  account numbers, or unredacted log excerpts.

## Reviewable Controls

| Control | Current evidence | MVP limitation |
| --- | --- | --- |
| Protected-path denial | `cmd/idiom/p7_9_security_privacy_posture_test.go`, `internal/autonomy/path_roles_test.go`, and P7.5 denial tests validate protected/forbidden path deny decisions and discovery-profile mutation denial. | CLI and shared resolver evidence are covered; MCP resource parity remains P8.4/P9.9 scope. |
| Redaction and no raw prompt/file body | P7.9 checks denial output and local product-evidence-shaped output for forbidden markers; P7.6 classification examples and telemetry tests reject raw prompt/file-body leakage. | Full debug or historical conversation logs can still be sensitive if explicitly enabled; P7.9 only supports summarized sidecar/local-ops/posture evidence. |
| Retention and local artifact scope | P7.9 and local-ops quickstart checks record local artifact directory classes, cleanup status, artifact IDs/hashes, and no hosted dependency. | Exact global deletion windows for all logs and graph caches are not fully enforced until P7.11/P9.9. |
| Telemetry minimization | Local-ops privacy evidence has `metadata_only:true`, raw prompt/file-body capture flags false, and hosted telemetry defaults false; telemetry argument redaction is tested. | Local diagnostic JSONL can be written when telemetry is enabled; it is not a hosted analytics pipeline or formal DLP system. |
| Side-effect policy | Discovery profile tests deny writes, shell, destructive/open-world effects, unknown effects, and local context-artifact writes before execution. | CLI sidecar behavior is covered; MCP/app parity remains later review scope. |
| MCP/resource containment | MCP integration and classification-parity tests cover stdio replay, roots/resources metadata, protected-resource denial, caller metadata, and CLI/MCP parity evidence without raw protocol body export. | Current evidence supports fixture-level stdio/resource containment only; broader MCP resource body export, non-stdio authorization, and formal conformance remain no-go until reviewed. |

## Classification Coverage

The validation matrix covers every current client classification from
`autonomy.DefaultClientClassifications()` and ties each row back to the P7.6
CLI example command ID:

- `finance`: regulated proof-only; no finance compliance claim.
- `healthcare`: regulated proof-only; no HIPAA compliance or PHI-processing
  attestation.
- `public-sector`: regulated proof-only; no FedRAMP, DoD, or NIST
  certification claim.
- `enterprise-saas`: primary MVP buyer posture; retained-use and paid-signal
  claims remain P7.11/P9.7 scope.
- `tooling-vendor`: integration proof; MCP resource parity remains P8.4/P8.5.
- `local-model`: private-code/local readiness proof; live model quality and
  capacity remain P6/P9.9 scope.
- `open-source`: activation and adoption wedge; not paid enterprise demand
  evidence.

## Reviewer Command

Focused posture evidence:

```bash
go test ./cmd/idiom -run 'TestP79|TestP75|TestP76|TestLocalOpsBenchmark'
go test ./internal/autonomy ./internal/workspace ./internal/telemetry
```

Release-time posture review should cite the matrix check IDs and command
results, not raw prompts, file bodies, private paths, secrets, or customer data.
