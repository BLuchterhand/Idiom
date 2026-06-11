# Harness Correctness Review: Prefetch Excerpt Implementation

Status: approve.

## Scope Reviewed

- `internal/capabilities/workspace/read_prefetch.go`
- `internal/capabilities/workspace/read_prefetch_test.go`
- `internal/runtime/agent/read_only_prefetch.go`
- `internal/runtime/agent/read_only_prefetch_test.go`
- `internal/runtime/agent/direct_answer_surface_test.go`
- `internal/runtime/agent/runner.go`
- `internal/capabilities/workspace/manager.go`
- `docs/evidence/read-only-prefetch-proposal/prefetch-model-facing-excerpt-intent.md`

## Findings

- Prefetch uses the same workspace path-resolution boundary as normal reads via
  `resolveReadTarget`, so outside-root paths are rejected by the existing
  workspace policy.
- Prefetch excludes generated and log artifacts before reading source bodies.
- Prefetch records source bodies through normal tool-result recording, while
  the telemetry event stays metadata-only.
- The runner guards prefetch to top-level read-only workspace lanes with no
  prior evidence, and skips delegated child agents to preserve actor
  boundaries.
- Line splitting and display-path handling reuse existing workspace helpers, so
  model-facing source snippets preserve the read-file evidence shape.

## Verification

- `go test ./internal/capabilities/workspace -run TestPrefetchReadFiles`
- Focused `go test ./internal/runtime/agent` prefetch/source-evidence cases
- `go test ./internal/runtime/agent`

## Verdict

The implementation is aligned with the harness contract: prefetch is a
host-owned evidence optimization, not a new model-facing authority surface. It
preserves workspace path safety, keeps telemetry bounded, and participates in
the same source-body evidence ledger as explicit reads.
