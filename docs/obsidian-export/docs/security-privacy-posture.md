# Security And Privacy Posture

This posture artifact is written for enterprise and regulated reviewers. Every
claim below should be tied to executable checks or fixture evidence. It is not
a SOC 2, HIPAA, FINRA, FedRAMP, DoD, or NIST attestation.

For a new ecosystem, keep the threat model, data-flow map, and control mapping
as living local artifacts owned by the implementation team.

## Evidence Boundary

Agent Harness's posture is contract-first:

- The harness owns protected paths, redaction, local artifact scope, telemetry
  minimization, side-effect policy, and readiness contracts.
- The model owns strategy and synthesis within those contracts.
- Durable evidence should use paths, IDs, hashes, counts, status fields,
  command labels, and policy codes. It should not use raw prompts, raw file
  bodies, secrets, protected workspace URLs, deployment identifiers, personal names,
  account numbers, or unredacted log excerpts.

## Reviewable Controls

| Control | Required evidence | Initial release limitation |
| --- | --- | --- |
| Protected-path denial | Protected-path and denial tests validate protected/forbidden path deny decisions and discovery-profile mutation denial. | CLI and shared resolver evidence are required; MCP resource parity requires separate protocol tests. |
| Redaction and no raw prompt/file body | Denial-output, product-evidence, classification, and telemetry tests reject raw prompt/file-body leakage. | Full debug or historical conversation logs can still be sensitive if explicitly enabled; posture evidence should stay summarized. |
| Retention and local artifact scope | Local artifact checks record artifact directory classes, cleanup status, artifact IDs/hashes, and no hosted dependency. | Exact global deletion windows for all logs and graph caches require explicit retention policy tests. |
| Telemetry minimization | Local-ops privacy evidence has `metadata_only:true`, raw prompt/file-body capture flags false, and hosted telemetry defaults false; telemetry argument redaction is tested. | Local diagnostic JSONL can be written when telemetry is enabled; it is not a hosted analytics pipeline or formal DLP system. |
| Side-effect policy | Discovery profile tests deny writes, shell, destructive/open-world effects, unknown effects, and local context-artifact writes before execution. | CLI sidecar behavior is covered; MCP/app parity requires separate protocol and connector evidence. |
| MCP/resource containment | MCP integration and classification-parity tests cover stdio replay, roots/resources metadata, protected-resource denial, caller metadata, and CLI/MCP parity evidence without raw protocol body export. | Fixture evidence supports fixture-level stdio/resource containment only; broader MCP resource body export, non-stdio authorization, and formal conformance remain no-go until reviewed. |

## Classification Coverage

The validation matrix covers every active deployment profile and ties each row
back to a CLI evidence command ID:

- `finance`: regulated proof-only; no finance compliance claim.
- `healthcare`: regulated proof-only; no HIPAA compliance or PHI-processing
  attestation.
- `public-sector`: regulated proof-only; no FedRAMP, DoD, or NIST
  certification claim.
- `enterprise-saas`: primary buyer posture; retained-use and paid-signal
  claims require separate privacy-safe validation records.
- `tooling-vendor`: integration proof; MCP resource parity requires separate
  protocol evidence.
- `local-model`: private-code/local readiness proof; live model quality and
  capacity require local-ops evidence.
- `open-source`: activation and adoption wedge; not paid enterprise demand
  evidence.

## Reviewer Command

Focused posture evidence:

```bash
go test ./cmd/harness -run 'TestSecurity|TestPrivacy|TestProductEvidence|TestLocalOpsBenchmark'
go test ./internal/autonomy ./internal/workspace ./internal/telemetry
```

Release-time posture review should cite the matrix check IDs and command
results, not raw prompts, file bodies, protected paths, secrets, or deployment data.
