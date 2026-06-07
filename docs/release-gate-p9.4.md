# P9.4 Client Classification Example-Test Gate

Gate date: 2026-05-14

Decision: no-go, needs validation records and independent reviewer signoff. P9.4
does not support a release readiness claim yet.

Artifact acceptance scope: this report can be accepted only as a privacy-safe
classification-coverage gate artifact. It records the executable example-test
coverage that exists, the generic architecture primitives each row maps to, and
the reviewer checklist needed for signoff. It does not mark reviewer GO, does
not complete P9.11 demo-transcript proof, and does not override the P9.1 bundle
status.

## Evidence Inputs

| Input | Evidence summary | Gate effect |
| --- | --- | --- |
| P0.5 client-classification matrix | All seven rows define shared primitives, evidence fields, privacy/security/performance expectations, and go/no-go signals. The matrix explicitly says vertical fit is proved through contracts, profiles, protected paths, freshness, sidecar envelopes, schema parity, readiness, verification, audit, and local evidence. | Source of row expectations. |
| P7.6/P7.15 CLI classification tests | `TestP76CLIClassificationExamplesUseGenericPrimitives` covers finance, healthcare, public-sector, enterprise-saas, tooling-vendor, local-model, and open-source with exact exit-code assertions and generic sidecar/schema/readiness primitives. | Executable CLI coverage exists for all seven classifications. |
| P8.5/P8.9 MCP parity tests | `TestP85MCPClassificationParityReport` and `TestP85MCPClassificationParityExamplesUseGenericPrimitives` cover MCP-visible resource, caller metadata, redaction, freshness, and profile-denial semantics. P8.9 public-sector denied-call caller metadata is asserted through the P8.5 public-sector frame. | MCP coverage exists where transport/resource/caller metadata claims apply; local-model is explicitly not applicable for MCP discovery. |
| P9.1 release evidence smoke | `go run ./cmd/idiom release evidence --project-root . --output .idiom/release/p9.4-smoke.json` wrote ignored local bundle `release-evidence-72e0530313501ba1`, artifact hash `72e0530313501ba1`, top-level status `partial`. | Release readiness fails closed. |
| P9.1 client readiness rows | The smoke bundle contains seven `client_readiness` rows, all with status `needs_evidence` and missing evidence `validation record`. | Every classification remains blocked for release readiness even though executable CLI/MCP coverage exists. |
| P9.2 release decision tieback | `docs/release-decision-p9.2.md` kept release readiness no-go until P9.3-P9.12 produce reviewed evidence; it also records all seven client rows as missing validation records. | Grounds this report as fail-closed P9.4 coverage evidence, not release GO. |
| Privacy posture | The smoke bundle reports metadata-only evidence and forbidden-field check `pass`. This report stores only relative paths, command labels, test identifiers, IDs, hashes, statuses, counts, and summaries. | Privacy invariant preserved. |

Do not commit ignored `.idiom/release` outputs. They are local reproduction
artifacts; this document keeps only privacy-safe summaries, hashes, statuses,
and command labels.

## Gate Result

| Check | Required pass condition | Current status | Gate effect |
| --- | --- | --- | --- |
| Seven classification rows | Finance, healthcare, public-sector, enterprise-saas, tooling-vendor, local-model, and open-source all have example-test coverage or explicit no-go records. | Partial for release readiness: all seven have executable CLI coverage, MCP coverage where applicable, and release-bundle rows; all seven still need validation records. | Blocks release. |
| Generic primitive mapping | Every example maps to shared primitives such as sidecar contract, profile policy, redaction, protected paths, caller attribution, MCP/CLI parity, freshness, audit ownership, local readiness, verification obligation, product evidence export, or release-gate linkage. | Pass for artifact evidence: current CLI/MCP examples use shared primitives and tests reject classification-specific primitive names in MCP rows. | Ready for reviewer verification, not reviewer GO. |
| Vertical-specific branches | Release must reject finance-specific, healthcare-specific, public-sector-specific, vendor-specific, or other client-specific control paths. | Pass for artifact evidence: examples use classification labels as coverage rows, not implementation branches. No release claim is based on vertical-only behavior. | Reviewer must independently confirm. |
| P9.1 validation records | Generated bundle must include current validation records for every classification before release readiness can pass. | Fail: every row lists missing `validation record`. | Blocks release. |
| Reviewer signoff | Separate reviewer subagent must sign off completeness, correctness, north-star alignment, bug/security/performance review, and generic primitive mapping. | Missing: this implementer report intentionally does not mark reviewer GO. | Blocks P9.4 release gate closure. |
| P9.11 separation | P9.4 must not claim runnable demo transcript proof or final/MVP limitations completion. | Pass: this report references docs/demo coverage only as non-release-readiness context and leaves runnable transcript proof to P9.11. | Preserves task boundary. |

## Classification Coverage

| Classification | Example/test identifiers | Coverage recorded | Generic architecture primitives used | Required evidence fields | Privacy/security/performance expectations | Current go/no-go status and blockers |
| --- | --- | --- | --- | --- | --- | --- |
| `finance` | P0.5 `finance-discovery-protected-path`; P7.6 `finance-discovery-policy`; P8.5 row `denied_write_without_input_leakage`; P9.1 evidence IDs `finance-discovery-policy`, `p7.10-finance-discovery-denial`. | CLI covered; MCP covered; docs/demo referenced but P9.11-owned; release-bundle row present; missing validation record. | Sidecar contract, discovery profile policy, protected-path denial, redaction/no raw body, caller attribution, audit/reviewable artifact, release gate linkage. | `client_classification`, `overlay_id`, `contract_id`, `profile`, `caller_id`, `protected_path_decisions`, `tool_policy_decisions`, `freshness`, `verification_status`, `audit_event_ids`, `retention_policy`. | Discovery exposes no write/shell/destructive/open-world/protected-content access; telemetry and reports remain metadata-only; smoke must finish inside configured budget or record blocker. | NO-GO for release: executable coverage exists, but P9.1 row is `needs_evidence` because validation record is missing; reviewer GO is absent. No finance compliance, retention, or formal regulated claim. |
| `healthcare` | P0.5 `healthcare-phi-redaction-fixture`; P7.6 `healthcare-redaction-denial`; P8.5 row `protected_resource_denial_before_metadata`; P9.1 evidence IDs `healthcare-redaction-denial`, `p7.10-healthcare-refresh-denial`. | CLI covered; MCP covered; docs/demo referenced but P9.11-owned; release-bundle row present; missing validation record. | Protected-path denial, metadata-only resources, root clamping, redaction/no raw body, freshness, sidecar contract, audit/reviewable artifact. | `client_classification`, `overlay_id`, `phi_fixture_id`, `redaction_policy_id`, `resource_id`, `freshness`, `telemetry_fields`, `retention_policy`, `audit_event_ids`, `blocked_or_redacted_reason`. | No PHI-like fixture content, raw prompt, raw file body, secret, or private path may appear in output, telemetry, resources, or product evidence; stale/redacted state must be explicit; latency/size budget issues must be blockers. | NO-GO for release: executable coverage exists, but P9.1 row is `needs_evidence` because validation record is missing; reviewer GO is absent. No HIPAA, PHI-processing, or compliance attestation claim. |
| `public-sector` | P0.5 `public-sector-offline-smoke`; P7.6 `public-sector-offline-side-effect-denial`; P8.5/P8.9 row `denied_shell_without_side_effect`; P9.1 evidence IDs `public-sector-offline-side-effect-denial`, `p7.10-public-sector-side-effect-denial`. | CLI covered; MCP covered; docs/demo referenced but P9.11-owned; release-bundle row present; missing validation record. | Discovery profile policy, side-effect denial, caller attribution, MCP transport attribution, local/offline evidence, root clamping, audit ownership, release command reproducibility. | `client_classification`, `overlay_id`, `offline_mode`, `profile`, `network_policy`, `command_digest`, `input_fixture_hash`, `output_artifact_paths`, `tool_versions`, `audit_event_ids`, `blockers`. | Network/open-world/destructive/unapproved shell effects unavailable by policy; roots clamped; evidence reproducible from local commands without hosted telemetry or raw sensitive content; runtime budget blockers are explicit. | NO-GO for release: executable coverage exists, but P9.1 row is `needs_evidence` because validation record is missing; reviewer GO is absent. No certification, procurement readiness, or managed-support claim. |
| `enterprise-saas` | P0.5 `enterprise-ci-verification-smoke`; P7.6 `enterprise-source-sanity-contract`; P8.5 row `caller_metadata_context_status`; P9.1 evidence IDs `enterprise-source-sanity-contract`, `p7.10-enterprise-saas-source-sanity`. | CLI covered; MCP covered for caller metadata/schema surface; docs/demo referenced but P9.11-owned; release-bundle row present; missing validation record. | Repository memory, context freshness, verification obligation, source sanity, CI/script-friendly sidecar envelope, caller attribution, audit finding owner/status, release gate linkage. | `client_classification`, `packet_id`, `index_id`, `changed_paths`, `suggested_tests`, `verification_commands`, `exit_status`, `finding_owner`, `finding_status`, `caller_id`, `trace_id`, `latency_ms`. | CI/script output must be parseable; product evidence must not require raw prompts or raw file bodies; high/medium findings need owner/status; localization and verification must finish within smoke threshold or record blocker. | NO-GO for release: executable coverage exists, but P9.1 row is `needs_evidence` because validation record is missing; reviewer GO is absent. Retained buyer value and paid/internal-platform signal remain P9.7/P9.12 scope. |
| `tooling-vendor` | P0.5 `tooling-vendor-cli-mcp-parity`; P7.6 `tooling-vendor-schema-metadata`; P8.5 row `tool_catalog_and_nested_caller_metadata`; P9.1 evidence IDs `tooling-vendor-schema-metadata`, `p7.10-tooling-vendor-schema-contract`. | CLI covered; MCP covered; docs/demo referenced but P9.11-owned; release-bundle row present; missing validation record. | Schema-derived tool surface, sidecar envelope parity, MCP/CLI compatibility, caller metadata, metadata-only resources, root clamping, structured errors, no strategy leakage. | `client_classification`, `schema_version`, `profile`, `transport`, `caller_id`, `request_id`, `contract_id`, `tool_names`, `resource_ids`, `error_codes`, `strategy_leakage_check`, `audit_event_ids`. | MCP must not grant broader authority than CLI; outputs must avoid prescribing next-agent strategy except typed precondition blocks; resources redacted/root-clamped; latency/payload budget blockers explicit. | NO-GO for release: executable coverage exists, but P9.1 row is `needs_evidence` because validation record is missing; reviewer GO is absent. Partner conformance, SDK behavior, and custom embedding remain out of scope. |
| `local-model` | P0.5 `local-model-readiness-degraded`; P7.6 `local-model-readiness-evidence`; P8.5 row `local_model_not_exposed_by_mcp_discovery` marked not applicable; P9.1 evidence IDs `local-model-readiness-evidence`, `p7.10-local-model-private-code-readiness`. | CLI covered; MCP explicitly not applicable for discovery local-ops readiness; docs/demo referenced but P9.11-owned; release-bundle row present; missing validation record. | Local readiness profile, backend/role routing evidence, queue admission, context budget, degraded-mode reporting, metadata-only telemetry, replayable local smoke/capacity evidence. | `client_classification`, `backend_id`, `role`, `queue_depth`, `context_budget`, `readiness_status`, `degraded_reason`, `smoke_command`, `telemetry_event_ids`, `latency_ms`, `replay_artifact`. | Evidence remains local by default; telemetry excludes raw prompts and file bodies; unsafe or overloaded execution blocks/degrades before mutation; queue/retry behavior bounded and visible. | NO-GO for release: executable CLI coverage exists, but P9.1 row is `needs_evidence` because validation record is missing; reviewer GO is absent. Model quality, live capacity, and backend reliability claims remain out of scope. |
| `open-source` | P0.5 `open-source-quickstart-transcript`; P7.6 `open-source-context-pack`; P8.5 row `metadata_only_resource_with_freshness`; P9.1 evidence IDs `open-source-context-pack`, `p7.10-open-source-quickstart`. | CLI covered; MCP covered for metadata-only freshness/resource semantics; docs/demo referenced but P9.11-owned; release-bundle row present; missing validation record. | `context_pack`, freshness/read-through evidence, metadata-only resources, verification evidence, harness audit, ranker feedback, cleanup/docs-gap reporting, release gate linkage. | `client_classification`, `install_steps`, `elapsed_time`, `packet_id`, `freshness`, `verification_commands`, `audit_findings`, `learning_log_ref`, `cleanup_result`, `docs_gap`, `blockers`. | Path must work without internal Idiom knowledge; no hosted telemetry, raw prompt capture, raw file-body upload, or secrets required; first useful packet plus audit must meet activation threshold or record precise blocker. | NO-GO for release: executable coverage exists, but P9.1 row is `needs_evidence` because validation record is missing; reviewer GO is absent. Full runnable transcript/cleanup proof remains P9.11/P9.6 scope. |

## Generic-Primitive Decision Boundary

P9.4 explicitly rejects vertical-specific branches. A classification label may
select an example row, evidence checklist, limitation, or reviewer stance, but
it must not select a separate control path. The reusable architecture primitives
accepted for this gate are:

- sidecar contract and `idiom.tool.v1` envelope
- protected-path and profile policy decisions
- redaction and metadata-only evidence
- caller attribution and transport attribution
- MCP/CLI schema parity and structured error parity
- freshness and stale-evidence reporting
- audit ownership, audit event IDs, and reviewable artifacts
- local readiness, queue/context budget, and degraded-mode reporting
- verification obligations and source-sanity evidence
- product-evidence export and release-gate linkage

Any example that can pass only through a finance-specific, healthcare-specific,
public-sector-specific, vendor-specific, local-model-specific, or open-source
implementation branch is a P9.4 NO-GO even if its output resembles the expected
sales story.

## Reviewer Signoff Checklist

The reviewer subagent should mark P9.4 GO only if every item below is satisfied
from code, tests, generated evidence, and this report:

- Completeness: all seven classifications appear exactly once in the P0.5
  matrix, P7.6 CLI example set, P9.1 client-readiness rows, and this P9.4
  report; MCP rows are covered or explicitly not applicable with rationale.
- Correctness: focused CLI/MCP classification tests pass, exact exit-code
  assertions remain active, and the P9.1 bundle still reports honest
  per-class status instead of silently passing missing validation records.
- North-star alignment: examples prove generic harness contracts and evidence
  gates; model strategy is not hardcoded, and tool outputs do not prescribe a
  caller's next action beyond typed denials or precondition blocks.
- Generic-primitives mapping: every example maps to shared sidecar/profile/
  redaction/caller/freshness/audit/readiness/verification/product-evidence
  primitives; no row relies on a vertical-specific implementation branch.
- Bugs/security/performance: protected content and side effects are denied or
  redacted; caller attribution is preserved where claimed; stale evidence is
  explicit; smoke budgets or blockers are recorded; no raw prompts, raw
  protocol bodies, raw file bodies, private paths, sentinels, secrets, tokens,
  credentials, or ignored bundle bodies are included in committed artifacts.
- Release-readiness honesty: because validation records are currently missing,
  reviewer signoff can accept this artifact as a fail-closed report only; it
  cannot mark release readiness GO until P9.1 client rows stop reporting
  missing evidence and P9.3/P9.5-P9.12 blockers are addressed.

Required reviewer output: `Decision: GO` only after the checklist passes and
fresh validation evidence is attached. Otherwise the reviewer should return
`Decision: NO-GO` with row-specific blockers.

## Reproduction Commands

```sh
go test ./internal/evidence/productevidence -run 'TestP91|TestRunReleaseEvidence' -count=1
go test ./internal/evidence/sidecarvalidation -run TestP76 -count=1
go test ./internal/surfaces/cli/mcp -run TestP85 -count=1
go run ./cmd/idiom release evidence --project-root . --output .idiom/release/p9.4-smoke.json
jq '.client_readiness | length' .idiom/release/p9.4-smoke.json
jq -r '.client_readiness[] | "\(.classification)\t\(.status)\t\((.missing_evidence // []) | join(","))"' .idiom/release/p9.4-smoke.json
git diff --check
```

Expected current result: seven client rows are present, and every row remains
`needs_evidence` with missing validation record until qualifying validation
records are supplied. That is the correct fail-closed release posture for P9.4.
