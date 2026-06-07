# Capability-First Model Interface Implementation Evidence

Status: implemented and verified, started 2026-05-24

Source design: `docs/design-capability-first-model-interface.md`

## Pre-Implementation Review

Full debugging reviewer bench completed before implementation.

Shared verified findings:

- The design direction is north-star aligned: model declares evidence capability intent; harness resolves adapters and reports precision/fallback without choosing strategy.
- Current code already has a partial `read_only_capability` facade in `internal/capabilities/workspace/capabilities.go` with five routed read-only IDs: `file-outline`, `symbol-lookup`, `definition`, `references`, and `related-files`.
- `source_sanity` must stay deferred from the read-only facade because its schema side effects are `read_only` plus `unknown`.
- `harness_audit` reported tool-surface drift at 0, with 5 routed read-only capabilities, 75 unmapped read-only adapters, 0 unsafe mappings, and 1 deferred adapter.
- The main gap is parity and observability, not facade absence: capability vocabulary, runtime profile exposure, manifest/export metadata, resolver precedence, runtime brief rendering, and audit grouping are not yet one contract.

Accepted implementation constraints:

- Do not add another generic `capability_request` facade in this slice.
- Do not remove language-prefixed adapter tools.
- Do not put `source_sanity` or `verify_edit` into the read-only-only registry.
- Keep capability registry and result normalization in `internal/capabilities/workspace`.
- Keep runtime brief/profile rendering in `internal/runtime/workerruntime`.
- Keep post-readiness fallback refusal and write/verification gates in `internal/runtime/agent`, not in the resolver.
- Fix path-derived language precedence before broadening runtime guidance.

Reviewer-requested tests:

- Manifest/schema export includes capability IDs and classifications.
- `read_only_capability` and `code_navigate` honor path-derived language over conflicting language override.
- Runtime profiles expose `read_only_capability` only where the phase contract permits it.
- Runtime brief renders a compact capability summary without enumerating every language-prefixed adapter.
- Audit/tool-surface coverage preserves routed/deferred/unsafe classification, including deferred `source_sanity`.

## Implementation Atoms

| Atom | State | Scope | Tests |
| --- | --- | --- | --- |
| `cap-iface-001-workspace-contract` | implemented | `internal/capabilities/workspace/capabilities.go`, `internal/repository/semantic/languagetools/code_navigation.go`, `internal/capabilities/workspace/tool_manifest.go`, `internal/capabilities/workspace/tool_schema_export.go`, focused workspace and language-tools tests | `go test ./internal/capabilities/workspace ./internal/repository/semantic/languagetools -count=1` |
| `cap-iface-002-runtime-brief` | implemented | `internal/runtime/workerruntime/catalog.go`, `internal/runtime/workerruntime/runtime.go`, focused runtime tests | `go test ./internal/runtime/workerruntime -count=1` |
| `cap-iface-003-review-audit` | implemented | final review evidence and `harness_audit` validation | `go run ./cmd/idiom tool harness_audit --input '{"purpose":"tool_surface","limit":20}'` |

## Verification Log

- 2026-05-24: Baseline `idiom tool harness_audit --input '{"purpose":"tool_surface","limit":20}'` passed with 0 drift; reported Python backend availability findings and capability coverage 37/112.
- 2026-05-24: Baseline focused workspace tests passed: `go test ./internal/capabilities/workspace -run 'TestReadOnlyCapability|TestToolSchema|TestBuildToolManifest|TestDiscoveryProfile' -count=1`.
- 2026-05-24: Baseline focused runtime tests passed: `go test ./internal/runtime/workerruntime -run 'Test.*RuntimeBrief|TestManifestToolsForCapabilityProfile|TestBuildToolManifest' -count=1`.
- 2026-05-24: Focused autonomy compile guard passed after removing duplicate guard-friction audit helper definitions from existing dirty work: `go test ./internal/contracts/autonomy -count=1`.
- 2026-05-24: Focused workspace capability/navigation/manifest/audit tests passed after delayed retry: `go test ./internal/capabilities/workspace -run 'TestReadOnlyCapability|TestCodeNavigate|TestToolManifestAndExportExposeEvidenceCapabilityMetadata|TestHarnessAuditReportsCapabilityRegistryMetrics' -count=1`.
- 2026-05-24: Focused runtime profile/brief tests passed after delayed retry: `go test ./internal/runtime/workerruntime -run 'Test.*RuntimeBrief|TestManifestToolsForCapabilityProfile|TestBuildToolManifest|TestRoleSpecForChildReadOnly' -count=1`.
- 2026-05-24: Broader package tests passed after delayed retry: `go test ./internal/contracts/autonomy -count=1`, `go test ./internal/capabilities/workspace -count=1`, and `go test ./internal/runtime/workerruntime -count=1`.
- 2026-05-24: Current-source tool-surface audit passed: `go run ./cmd/idiom tool harness_audit --input '{"purpose":"tool_surface","limit":20}'`. Summary: 135 tools, 0 drift, capability coverage 55/112, 8 routed read-only capabilities, 0 unsafe mappings, 1 deferred adapter, 2 Python backend availability findings.
- 2026-05-24: Post-implementation reviewer bench found blocking gaps in compact runtime capability wording, result-envelope completeness, behavior audit grouping, discovery-profile parity for new capability IDs, and one dead helper. Fixes applied: `available_evidence_capabilities` is capability/class-only, the model protocol card renders explicit visible and callable tool lists for the active contract, read-only capability results now include resolved target/freshness/count/truncation/follow-up fields, behavior audit reports observed capability mix/top capabilities, discovery profiles map `inspect-symbol`/`calls-from`/`calls-into` through `code_navigate`, and the unused routed-ID helper was removed.
- 2026-05-24: Reviewer-fix focused tests passed after delayed retry: `go test ./internal/capabilities/workspace -run 'TestReadOnlyCapability|TestCodeNavigate|TestToolManifestAndExportExposeEvidenceCapabilityMetadata|TestHarnessAuditReportsCapabilityRegistryMetrics|TestDiscoveryProfile|TestHarnessAuditBehaviorScansLogs' -count=1`, `go test ./internal/runtime/workerruntime -run 'Test.*RuntimeBrief|TestManifestToolsForCapabilityProfile|TestBuildToolManifest|TestRoleSpecForChildReadOnly' -count=1`, and focused `internal/runtime/agent` protocol/parser tests.
- 2026-05-24: Broader touched package verification passed after delayed retry: `go test ./internal/capabilities/workspace -count=1`, `go test ./internal/runtime/workerruntime -count=1`, and `go test ./internal/runtime/agent -count=1`.
- 2026-05-24: Behavior audit now reports observed capability grouping: `go run ./cmd/idiom tool harness_audit --input '{"purpose":"behavior","limit":5}'` passed and included `log_behavior.capability_mix` / `top_capabilities` with current dominant capabilities `inspect_file`, `verify_edit`, and `find_references`.
- 2026-05-24: Final reviewer re-checks passed for Incident Root-Cause, Model Runtime/Config, Harness/Go, Prompt/Protocol, TDD/Acceptance, and North-Star/Architecture reviewers. Prior blockers cleared: observed audit grouping now scans `tool_request` records, protocol cards render explicit visible/callable tools from the active projection, `source_sanity` maps to `source_diagnostics`/`verify_edit` evidence capabilities, protocol presentation is wired and cited by completion friction, and the touched package set is green.
- 2026-05-24: Full live test suite passed after reviewer approval and delayed retry spacing: `go test ./...`.
