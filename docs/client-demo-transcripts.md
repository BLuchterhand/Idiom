# Client Demo Transcripts

P7.10 records one runnable demo transcript for every generic client
classification. The machine-checkable transcript set lives in
[`testdata/sidecar/client-demo-transcripts.json`](../testdata/sidecar/client-demo-transcripts.json)
and is validated by:

```bash
go test ./internal/evidence/sidecarvalidation -run TestP710ClientDemoTranscripts
```

## Demo Boundary

The demos sell Idiom as a local sidecar trust layer: repo memory, freshness,
permission contracts, audit fields, readiness, and schema evidence for other
coding agents. They keep the north-star split intact: the model owns strategy;
the harness owns contracts.

The transcript set intentionally reuses P7.1-P7.9 primitives:

- `idiom.tool.v1` sidecar envelopes, caller metadata, contract metadata,
  freshness, audit IDs, and typed errors.
- Discovery-profile denial for mutation, shell, and additive context writes.
- Shared client-classification metadata from `autonomy.DefaultClientClassifications()`.
- P7.6 command IDs and P7.9 security/privacy controls.
- P7.8 integration posture without claiming MCP protocol support before Phase 8.
- A privacy-safe local-ops quickstart export for the open-source activation row
  so packet, freshness, candidate-count, verification, and redaction evidence
  are visible without exposing temp workspace roots.

## Classification Coverage

| Classification | Transcript ID | Demo command ID | MVP stance |
| --- | --- | --- | --- |
| `finance` | `p7.10-finance-discovery-denial` | `finance-discovery-policy` | Regulated proof-only; no finance compliance or FINRA claim. |
| `healthcare` | `p7.10-healthcare-refresh-denial` | `healthcare-redaction-denial` | Regulated proof-only; no HIPAA or PHI-processing claim. |
| `public-sector` | `p7.10-public-sector-side-effect-denial` | `public-sector-offline-side-effect-denial` | Proof-only; no FedRAMP, DoD, certification, or procurement-readiness claim. |
| `enterprise-saas` | `p7.10-enterprise-saas-source-sanity` | `enterprise-source-sanity-contract` | Primary buyer-facing local sidecar trust demo; paid buyer signal remains P0.8/P9.7 evidence. |
| `tooling-vendor` | `p7.10-tooling-vendor-schema-contract` | `tooling-vendor-schema-metadata` | Secondary integration demo; MCP parity and SDK/conformance support remain later scope. |
| `local-model` | `p7.10-local-model-private-code-readiness` | `local-model-readiness-evidence` | Local/private-code readiness demo; no model-quality or tuning-support claim. |
| `open-source` | `p7.10-open-source-context-pack` | `open-source-context-pack` | Privacy-safe quickstart evidence export for adoption; not paid enterprise-SaaS demand by itself. |

Each transcript includes commands, expected output shape, value claim, evidence
fields, MVP limitation, final-state expansion, privacy/no-forbidden-field
posture, pass/no-go criteria, and release claim impact. The
validation test runs the commands in fixture workspaces and scans executed
stdout and stderr for missing classifications, unstable expected fields,
vertical-specific branch claims, and forbidden evidence such as raw prompts, raw
file bodies, private paths, credentials, private repo identifiers, customer
identifiers, account numbers, patient names, or unredacted telemetry paths.
P9.11 release replay adds a stricter safety boundary before execution: the
human-readable `command` string must parse to the same invocation as `argv`,
the argv shape must match the known P7.10-safe allowlist, side-effect examples
must run only as discovery-profile denials, and the replay config disables
shell/write defaults.

## P9.11 Release-Gate Contract

P9.11 consumes the same fixture as a fail-closed release gate. Product and
release evidence report `P9.11` as `evidence_present` only when all seven
classifications have exactly one runnable transcript with a command, expected
output, evidence fields, MVP limitation, final-state expansion, explicit privacy
posture, go/no-go criteria, and release claim impact.

Malformed rows, missing or duplicate classifications, missing runnable commands,
command/argv mismatches, unsafe command shapes, missing expected output, missing
evidence fields, missing limitations, missing privacy posture, forbidden/private
content, or regulated/commercial overclaim language return `needs_evidence` with
structured blockers. The gate supports only demo-proof claims. It does not prove
regulated approval, paid buyer signal, support scope, packaging scope, or a
release go decision.
