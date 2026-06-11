# Implementation Scope Notes

The workspace was already dirty before the read-only named-path prefetch
implementation. The review packet's raw `git diff` includes some pre-existing
or previously addressed hunks in shared files, especially `runner.go` and
`runner_plan_then_execute_guard.go`.

## Prefetch Implementation Scope

The read-only named-path prefetch implementation consists of:

- `internal/capabilities/workspace/read_prefetch.go`
- `internal/capabilities/workspace/read_prefetch_test.go`
- `internal/runtime/agent/read_only_prefetch.go`
- Direct-answer surface activation in `internal/runtime/agent/direct_answer_surface.go`
- Direct-answer surface tests in `internal/runtime/agent/direct_answer_surface_test.go`
- Source attribution in `internal/runtime/agent/read_only_answerability.go`
- Prefetch dedup/source attribution test in
  `internal/runtime/agent/read_only_answerability_test.go`
- Treating `workspace.ToolNamePrefetch` as concrete source-body evidence in
  the existing evidence classifiers.

## Dirty-Tree Hunks Not Introduced By This Prefetch Pass

The following hunks appeared in the raw scoped diff because they are in files
also touched by the prefetch implementation, but they are not part of the
prefetch design:

- diagnostic-log trigger narrowing in `objectiveRequestsDiagnosticLogInspection`
- `modelErrorAfterPartialEditBlockerAnswer` ordering around transport retries
- child-write task-ledger evidence requirement adjustment
- plan-execute source-satisfied freshness guard

If this work is committed as one changeset, the commit body should explicitly
call out these non-prefetch hunks or they should be staged separately. The
benchmark and prefetch correctness review should evaluate the prefetch scope
above.

## Deferred Follow-Up

The implementation emits structured `read_only_named_path_prefetch` telemetry
with hit/skip/byte/dedup/estimated-round-trip fields. Competitive benchmark
`summary.json` aggregation for those fields is not part of this implementation
pass; benchmark analysis should read the structured prefetch event from the
conversation log until the benchmark harness grows first-class summary fields.
