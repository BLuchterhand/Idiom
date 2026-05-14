# P9.8 Integration Proof Release Gate

Gate date: 2026-05-14

Decision: P9.8 integration evidence is present for the current repository
fixtures. This is not a full release GO. The broader P9.1 bundle remains
partial until the other P9 gates and validation evidence close.

Artifact acceptance scope: this report can be accepted only as a privacy-safe
integration-proof gate artifact. It records that product evidence and release
evidence now expose the same machine-readable P9.8 status, path counts,
blockers, evidence IDs, and claim impact. It does not update the implementation
tracker, does not commit generated `.idiom` artifacts, and does not approve
release.

P9.8 passes only when all three distinct executable proof paths are present:
`shell_ci_script`, `external_coding_agent_workflow`, and `mcp_stdio`. The MCP
path must carry MCP-specific parity on the `mcp_stdio` row itself. Parity on
another row, duplicate rows, placeholder/deferred rows, malformed privacy
semantics, or unsafe privacy fields fail closed as `needs_evidence` or
`blocked`.

## Evidence Inputs

| Input | Evidence summary | Gate effect |
| --- | --- | --- |
| P7.8 integration proof fixture | `testdata/sidecar/integration-proofs.json` contains three test-validated rows: `p7.8-shell-ci`, `p7.8-external-coding-agent`, and `p7.8-mcp-stdio`. Each consumes profile, schema, freshness, audit, and contract fields, and each privacy object explicitly reports no raw prompts, file bodies, private paths, credentials, or private repository/customer IDs. | Supplies the three required executable paths. |
| P8.6 MCP stdio replay fixture | `testdata/sidecar/mcp-stdio-integration-proof.json` links to `p7.8-mcp-stdio` and P9.8. The replay covers initialize, `tools/list`, allowed `tools/call`, denied `tools/call`, roots/resources metadata, caller metadata, `transport:"mcp_stdio"`, protected-resource denial, redaction, and report-only freshness. | Supplies MCP protocol proof without raw protocol body export. |
| Product evidence export | `idiom product-evidence export --project-root .` reports P9.8 `evidence_present`, counts `required:3`, `shell_ci:1`, `external_agent:1`, `mcp_stdio:1`, `mcp_parity:1`, no P9.8 blockers, and proof IDs `p7.8-shell-ci`, `p7.8-external-coding-agent`, `p7.8-mcp-stdio`. | Product evidence can cite the substrate integration proof. |
| Release evidence bundle | `idiom release evidence --project-root . --output .idiom/release/p9.8-smoke.json` reports matching P9.8 `evidence_present`, the same counts, the same proof IDs, and sidecar integration path rows all `pass`. | Release evidence agrees with product evidence for P9.8 only. |
| Privacy posture | Product/release smokes report forbidden-field check `pass`. Tests and fixtures scan for raw prompts, raw protocol bodies, file bodies, private paths, secrets, credentials, and customer/private identifiers. | Privacy invariant preserved. |

Generated `/tmp` and `.idiom/release` outputs are local reproduction artifacts.
This document stores only command labels, statuses, counts, proof IDs, and
bounded blocker semantics.

## Gate Result

| Check | Required pass condition | Current status | Gate effect |
| --- | --- | --- | --- |
| Shell/CI proof path | One valid executable row with shared sidecar contract, caller attribution, report-only freshness, audit fields, and denial behavior. | Pass: `p7.8-shell-ci`. | Counts toward P9.8. |
| External coding-agent workflow | One valid executable row proving another harness can consume evidence without prompt interpolation or strategy prescription. | Pass: `p7.8-external-coding-agent`. | Counts toward P9.8. |
| MCP stdio proof path | One valid executable `mcp_stdio` row with MCP-specific parity fields on that row. | Pass: `p7.8-mcp-stdio`. | Counts toward P9.8. |
| MCP parity detail | Initialize, tools/list, allowed and denied tools/call, roots/resources metadata, caller metadata, transport attribution, protected-resource denial, redaction, report-only freshness, and product-evidence export parity are represented. | Pass: readiness fields are required on the MCP row and replayed by `TestP86MCPStdioIntegrationProofReplay`. | Prevents vague MCP placeholders from passing. |
| Distinct-path enforcement | Duplicate executable rows cannot substitute for a missing required path. | Pass: tests cover missing shell/CI, missing external, missing MCP, duplicate rows, and unrelated paths. | Fails closed when a path is absent. |
| Wrong-path MCP parity | MCP parity fields on a non-`mcp_stdio` row cannot satisfy P9.8. | Pass: product and release tests keep this `needs_evidence`. | Prevents overclaiming MCP readiness. |
| Privacy/security | No raw prompt, raw protocol body export, file body, private path, secret, credential, PHI, account data, or partner-identifying detail is emitted in fixtures/reports. | Pass for current fixtures and generated summaries. | Required invariant preserved. |
| Release readiness honesty | P9.8 may be evidence-present without overriding other release gates. | Pass: P9.8 is `evidence_present`; current release bundle remains `partial`. | No full-release claim. |

Current P9.8 classification: **evidence present for integration proof**.

## Claim Impact

Allowed claim: Idiom has a machine-readable, replay-backed integration proof
for shell/CI, one external coding-agent workflow, and MCP stdio using shared
profile, schema, freshness, audit, caller metadata, transport attribution, and
privacy-safe denial semantics.

Disallowed claim: P9.8 does not prove commercial readiness, regulated
compliance, MCP resource body export, hosted telemetry safety, competitive
superiority, activation readiness, retained use, or release GO.

## Reproduction Commands

```sh
go test ./cmd/idiom -run 'TestP98|TestP86|TestP78|TestP711|TestRunReleaseEvidence|TestRunProductEvidence|TestMCP' -count=1

jq empty testdata/sidecar/integration-proofs.json testdata/sidecar/mcp-stdio-integration-proof.json

go run ./cmd/idiom product-evidence export --project-root . >/tmp/idiom-p9.8-product-evidence.json
jq -r '.release_gate_linkage[] | select(.gate=="P9.8") | [.gate,.status,.evidence,((.evidence_ids // [])|join(","))] | @tsv' /tmp/idiom-p9.8-product-evidence.json

go run ./cmd/idiom release evidence --project-root . --output .idiom/release/p9.8-smoke.json
jq -r '.release_readiness[] | select(.gate=="P9.8") | [.gate,.status,.evidence,((.evidence_ids // [])|join(","))] | @tsv' .idiom/release/p9.8-smoke.json
jq -r '.sidecar_integration.paths[] | [.path,.status,((.evidence_ids // [])|join(",")),(.blocker // "")] | @tsv' .idiom/release/p9.8-smoke.json

git diff --check
```

Expected current result: product and release evidence both report P9.8
`evidence_present`, counts show all three required paths and one MCP stdio
parity path, blockers are empty, evidence IDs are `p7.8-shell-ci`,
`p7.8-external-coding-agent`, and `p7.8-mcp-stdio`, sidecar paths are `pass`,
and the overall release bundle remains `partial` until non-P9.8 release
blockers are resolved.
