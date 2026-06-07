# External Caller Recipes For `idiom tool`

These recipes are for callers that want Idiom's repository evidence without
running Idiom's chat UI or local model loop. The caller owns strategy: it
decides whether the evidence is enough, whether to edit, and which verification
to run. Idiom owns the contract: profile, policy, freshness, structured errors,
and the sidecar envelope.

Use the structured `sidecar` object for new integrations. The legacy top-level
`Tool`, `Summary`, `Output`, and `Error` fields remain present for P7.1
compatibility, but they are not the stable machine contract.

## Contract Fields To Parse

Every `idiom tool <name>` result should be treated as JSON:

- `sidecar.schema_version`: currently `idiom.tool.v1`.
- `sidecar.profile`: the active permission profile, for example `discovery`.
- `sidecar.caller`: optional allowlisted caller metadata from `--caller-id`,
  `--request-id`, and `--trace-id`. Values must be short opaque identifiers.
  The CLI rejects common secret-, token-, email-, and path-shaped values, but
  this is a conservative boundary check rather than comprehensive secret
  scanning.
- `sidecar.contract`: stable contract metadata, transport, profile, policy, and
  decision authority.
- `sidecar.freshness`: graph freshness for graph-backed evidence when present.
- `sidecar.structured_output`: parsed tool output when the tool emits JSON.
- `sidecar.error`: typed error object with `class`, `code`, and `message`; null
  on success.

Backend fallback is not a first-class `sidecar.backend_fallback` field in v1.
Current callers should read fallback evidence from the tool's structured output
or summary, such as `source_sanity` reporting a generic fallback. A future
compatibility task should add a typed fallback field only after CLI and MCP can
emit the same contract.

Do not ask an external agent to interpolate a prompt phrase to choose a
contract. Choose the CLI mode, profile, tool name, and JSON input explicitly.

## Shell Or CI Script

Use this when a script needs a preflight freshness check before it consumes
repository evidence.

The checked script form is
[`scripts/sidecar-ci-proof.sh`](../scripts/sidecar-ci-proof.sh). It accepts
`IDIOM_BIN`, `IDIOM_CALLER_ID`, and `IDIOM_REQUEST_ID` overrides and validates
the same fields shown below.

```bash
set -euo pipefail

status_json="$(
  idiom tool context_status \
    --profile discovery \
    --caller-id ci-script \
    --request-id build-123 \
    --input '{"limit":1}'
)"

jq -e '
  .sidecar.schema_version == "idiom.tool.v1" and
  .sidecar.profile == "discovery" and
  .sidecar.caller.caller_id == "ci-script" and
  .sidecar.error == null and
  .sidecar.contract.transport == "cli" and
  .sidecar.freshness.policy == "report_only"
' >/dev/null <<<"$status_json"

jq '{fresh: .sidecar.structured_output.fresh,
     index_id: .sidecar.freshness.index_id,
     stale_path_count: .sidecar.freshness.stale_path_count}' <<<"$status_json"
```

For CI, fail on a typed sidecar error rather than matching prose:

```bash
if ! idiom tool context_status --profile discovery --input '{"refresh":true}' >status.json; then
  jq -e '
    .sidecar.profile == "discovery" and
    .sidecar.error.class == "profile_denied" and
    (.sidecar.error.message | contains("read_only_profile"))
  ' status.json >/dev/null
fi
```

This demonstrates that the `discovery` profile is request-aware: a report-only
freshness check can run, but a refresh request is denied before graph/cache
mutation.

## External Coding-Agent Harness

Use this when another harness, such as Codex, Claude Code, OpenCode, Aider, or a
custom runner, wants evidence without adopting Idiom's control loop.

1. Start by listing tools available to the profile:

   ```bash
   idiom tool list --profile discovery
   ```

   The list is capability inventory, not the tool-result envelope. For actual
   evidence calls, parse `sidecar`.

2. Call evidence tools with explicit JSON input:

   ```bash
   idiom tool context_query \
     --profile discovery \
     --input '{"query":"callers of runToolMainWithTelemetry","resolution":"symbol","freshness_policy":"report_only","include_evidence":true}'
   ```

3. Gate consumption on the envelope:

   ```bash
   jq -e '
     .sidecar.schema_version == "idiom.tool.v1" and
     .sidecar.profile == "discovery" and
     .sidecar.error == null and
     (.sidecar.freshness.policy == "report_only" or .sidecar.freshness.policy == "allow_stale")
   ' result.json >/dev/null
   ```

The external harness may pass the returned findings, paths, symbols, counts,
and freshness to its model. It should not treat Idiom output as a prescribed
next step.

## MCP Stdio

Use this when an MCP client needs the same discovery-profile evidence contract
over stdio. The P8.6 replay fixture records the contract shape:

- transport: `mcp_stdio`
- sidecar schema: `idiom.tool.v1`
- profile: `discovery`
- readiness fields: protocol implemented, shared contract shape, profile,
  schema, caller metadata, roots/resources, redaction denial, freshness, audit,
  privacy review, and product-evidence export parity
- limitation: `resources/read` is metadata-only; file bodies are not returned
  until resource body redaction is implemented safely

The executable proof lives in
[`testdata/sidecar/mcp-stdio-integration-proof.json`](../testdata/sidecar/mcp-stdio-integration-proof.json)
and is linked from
[`testdata/sidecar/integration-proofs.json`](../testdata/sidecar/integration-proofs.json).
It is replayed by
`go test ./internal/surfaces/cli/mcp -run TestP86MCPStdioIntegrationProofReplay -count=1`.

## Local Or Private-Code User

Use this when the repository cannot leave the machine and the user wants a
local evidence packet for another agent.

```bash
idiom tool context_status --profile discovery --input '{"limit":1}'
```

If the status is fresh enough for your local policy, a trusted local caller can
use the default profile to create a context packet artifact:

```bash
idiom tool context_pack \
  --input '{"task":"localize auth retry handling","mode":"plan","limit":8}'
```

`context_pack` writes local context artifacts, so it is intentionally outside
the read-only `discovery` profile until a reviewed read-only packet mode exists.
Parse the returned `sidecar.contract`, `sidecar.freshness`, and packet fields
instead of relying on the human summary.

## Runnable Smoke Transcript

The checked transcript fixture is
[`testdata/sidecar/discovery-smoke.transcript.json`](../testdata/sidecar/discovery-smoke.transcript.json).
It covers:

- `context_status --profile discovery --caller-id ci-script --request-id build-123 --input '{"limit":1}'`
  success with caller attribution,
  `sidecar.schema_version`, `sidecar.profile`, `sidecar.contract`, and
  freshness fields.
- `tool list --profile discovery` profile inventory that includes read-only
  discovery tools and excludes write/shell tools.
- A temp-workspace graph build used only to make the report-only recipe
  deterministic.
- The external-harness `context_query --profile discovery --input '{"query":"callers of runToolMainWithTelemetry","resolution":"symbol","freshness_policy":"report_only","include_evidence":true}'`
  recipe, proving the discovery-profile sidecar envelope and graph packet
  fields are executable without mutation.
- The local/private-code `context_pack --input '{"task":"localize auth retry handling","mode":"plan","limit":8}'`
  recipe, proving default-profile packet artifact behavior and expected packet
  fields without exposing raw file bodies.
- `context_status --profile discovery --input '{"refresh":true}'` denial with a
  typed `sidecar.error.class == "profile_denied"` before execution.

The fixture is replayed by `go test ./internal/evidence/sidecarvalidation -run TestSidecarSmokeTranscript`.
The v1 compatibility golden tests are replayed by
`go test ./internal/evidence/sidecarvalidation -run TestSidecarV1CompatibilityGoldenCases`.
The P7.8 integration proof records are validated by
`go test ./internal/evidence/sidecarvalidation -run TestP78IntegrationProofRecords`.
The P8.6 MCP stdio proof is replayed by
`go test ./internal/surfaces/cli/mcp -run TestP86MCPStdioIntegrationProofReplay`.
