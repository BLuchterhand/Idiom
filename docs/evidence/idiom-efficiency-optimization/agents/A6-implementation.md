A6 implementation report

Status: implemented with dependency caution

Files changed:
- `internal/runtime/agent/direct_answer_surface.go`
- `internal/runtime/agent/direct_answer_surface_test.go`
- `internal/runtime/agent/runner.go`

What changed:
- Added a typed `DirectAnswerSurface` inventory for read-only direct-answer
  lanes with schema/version, capabilities, tool names, and terminal states.
- Added `DirectAnswerSurfacePrefetchEligibility` so A1 remains disabled with a
  typed miss unless the surface advertises `source_body_prefetch`.
- Wired the runner to emit a `direct_answer_surface` event once and use the
  inventory as the allowed-tool authority for direct-answer lanes.
- Kept `task` on the surface because existing direct-answer tests depend on
  read-only child packet returns; mutation tools, shell, plan submission, note,
  and task ledger remain excluded.
- Added focused tests for mutation/protocol exclusion, retained read-only
  investigation tools, non-read-only brief rejection, typed prefetch miss
  states, future prefetch eligibility, and event payload shape.

Verification:
- `go test ./internal/runtime/agent -run 'DirectAnswerSurface|DirectAnswer|Lane'`
  passed.

Notes:
- A1 must not enable source-body prefetch until the A6 surface explicitly
  advertises `source_body_prefetch` and the integration tests are added.
- Existing dirty changes in `internal/runtime/agent/runner.go` were preserved;
  this atom only added the direct-answer surface hook near allowed-tool
  selection.
