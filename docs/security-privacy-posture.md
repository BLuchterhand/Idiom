# Security And Privacy Posture

This is the P7.9 posture artifact for Idiom's local sidecar and coding-agent
harness. It is written for enterprise and regulated reviewers, but every claim
below is tied to executable checks or fixture evidence. It is not a SOC 2,
HIPAA, FINRA, FedRAMP, DoD, or NIST attestation.

The machine-checkable validation matrix is
[`docs/security-privacy-posture.matrix.json`](security-privacy-posture.matrix.json).
This document is the public posture source for release review. Historical
implementation trackers and private evidence bundles are not part of the public
documentation set.

## Evidence Boundary

Idiom's posture is contract-first:

- The harness owns protected paths, redaction, local artifact scope, telemetry
  minimization, side-effect policy, and readiness contracts.
- The model owns strategy and synthesis within those contracts.
- Durable evidence should use paths, IDs, hashes, counts, status fields,
  command labels, and policy codes. It should not use raw prompts, raw file
  bodies, secrets, private repo URLs, customer identifiers, patient names,
  account numbers, or unredacted log excerpts.
- Coding-rule reports follow the same boundary: findings include paths,
  symbols, line numbers, hashes, severities, and evidence predicates, not raw
  source bodies.

## Reviewable Controls

| Control | Current evidence | MVP limitation |
| --- | --- | --- |
| Protected-path denial | `internal/evidence/sidecarvalidation/security_privacy_posture_test.go`, `internal/contracts/autonomy/path_roles_test.go`, and P7.5 denial tests validate protected/forbidden path deny decisions and discovery-profile mutation denial. | CLI and shared resolver evidence are covered; MCP resource parity remains P8.4/P9.9 scope. |
| Redaction and no raw prompt/file body | P7.9 checks denial output and local product-evidence-shaped output for forbidden markers; P7.6 classification examples and telemetry tests reject raw prompt/file-body leakage. | Full debug or historical conversation logs can still be sensitive if explicitly enabled; P7.9 only supports summarized sidecar/local-ops/posture evidence. |
| Retention and local artifact scope | P7.9 and local-ops quickstart checks record local artifact directory classes, cleanup status, artifact IDs/hashes, and no hosted dependency. | Exact global deletion windows for all logs and graph caches are not fully enforced until P7.11/P9.9. |
| Telemetry minimization | Local-ops privacy evidence has `metadata_only:true`, raw prompt/file-body capture flags false, and hosted telemetry defaults false; telemetry argument redaction is tested. | Local diagnostic JSONL can be written when telemetry is enabled; it is not a hosted analytics pipeline or formal DLP system. |
| Side-effect policy | Discovery profile tests deny writes, shell, destructive/open-world effects, unknown effects, and local context-artifact writes before execution. | CLI sidecar behavior is covered; MCP/app parity remains later review scope. |
| MCP/resource containment | MCP integration and classification-parity tests cover stdio replay, roots/resources metadata, protected-resource denial, caller metadata, and CLI/MCP parity evidence without raw protocol body export. | Current evidence supports fixture-level stdio/resource containment only; broader MCP resource body export, non-stdio authorization, and formal conformance remain no-go until reviewed. |
| Hosted MCP HTTP network containment | HTTP endpoint validation is repeated for redirects and at connection time; the transport dials the validated IP directly, denies private/link-local/loopback destinations unless explicitly enabled, disables unpoliceable environment proxies in deny-private mode, and strips endpoint credentials from cross-origin redirects. MCP pending calls are cleared on cancellation, stream failure, reader termination, and close. | Private endpoints require `allow_private_network`; hosted authorization and formal MCP transport conformance remain deployment-specific. |

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
go test ./internal/evidence/sidecarvalidation -ginkgo.focus='p79 security privacy|P7\.6 CLI classification examples' -ginkgo.fail-on-empty -count=1
go test ./internal/surfaces/cli/tool -ginkgo.focus='p7 5 containment' -ginkgo.fail-on-empty -count=1
go test ./internal/surfaces/cli/localops -ginkgo.focus='local ops local ops benchmark' -ginkgo.fail-on-empty -count=1
go test ./internal/surfaces/cli/root -ginkgo.focus='Direct tool routing writes telemetry for rejected direct-tool input' -ginkgo.fail-on-empty -count=1
go test ./internal/surfaces/cli/mcp -ginkgo.focus='P8\.6 MCP stdio proof|p85 mcp classification parity' -ginkgo.fail-on-empty -count=1
go test ./internal/capabilities/mcp -ginkgo.focus='HTTP network policy|MCP HTTP transport|MCP client (removes canceled calls from pending state|fails and removes a pending call when the response reader terminates|fails a pending call when the client closes)' -ginkgo.fail-on-empty -count=1
go test ./internal/contracts/autonomy ./internal/capabilities/workspace ./internal/evidence/telemetry
```

Release-time posture review should cite the matrix check IDs and command
results, not raw prompts, file bodies, private paths, secrets, or customer data.

## P9.9 Release Gate Inputs

Source anchors checked on 2026-06-27 UTC.

Product threat model:

- Local-first coding-agent harness with shell, filesystem, model API, sidecar,
  and MCP integration risk.
- Primary release risks are protected-path writes, raw prompt or file-body
  retention, secret leakage, overbroad sidecar effects, and unsupported
  regulated-use claims.
- Dependent gate tiebacks: P7.9, P8.4, P9.1, P9.3, and P9.9.

Data-flow map:

- Operator prompts, workspace metadata, tool requests, tool results, telemetry
  summaries, and evidence records remain local unless the configured model
  backend or external tool receives the request.
- Release evidence should preserve IDs, paths, hashes, counts, status fields,
  and command labels rather than raw prompts or source bodies.

Privacy and retention posture:

- Default debug logs are redacted.
- Generated logs, transcripts, local models, `.book/`, `.patent/`, and private
  release evidence are excluded from the public source tree.
- Hosted telemetry is not a default release requirement.

Control mapping evidence:

- `docs/security-privacy-posture.matrix.json` owns the structured control map.
- The reviewer commands above provide the reproducible local checks.

Source verification evidence:

- source_id: docs-security-privacy-posture
  checked_date: 2026-06-27
  source_status: verified-local-public-doc
  verification_method: repository file review and `go test ./internal/evidence/sidecarvalidation`
- source_id: docs-release-checklist
  checked_date: 2026-06-27
  source_status: verified-local-public-doc
  verification_method: repository file review and release-prep verification commands

Downstream go/no-go gates:

- P7.9, P8.4, P9.1, P9.3, and P9.9 remain evidence tiebacks, not compliance
  claims.
