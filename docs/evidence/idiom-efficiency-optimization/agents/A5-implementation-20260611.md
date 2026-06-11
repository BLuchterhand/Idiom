# A5 Hosted Cache/Cost Metrics Implementation

Status: implemented after redesign

Design:

- `docs/design-hosted-cache-cost-metrics.md`

Review alignment:

- Full reviewer bench: `docs/evidence/idiom-efficiency-optimization/reviews/a5-cache-cost/`
- Final consolidated verdict: `PASS`

Implemented atoms:

- A5.1 provider usage parsing:
  `internal/runtime/llama/client.go`, `internal/runtime/llama/client_test.go`
- A5.2 runner metrics and JSONL:
  `internal/runtime/agent/runner_state.go`, `internal/runtime/agent/runner.go`,
  `internal/runtime/agent/plan_execute_runtime_policy_test.go`,
  `internal/runtime/agent/runner_usage_metrics_test.go`
- A5.3 TUI, transcript, and reader propagation:
  `internal/surfaces/tui/app/model_state.go`,
  `internal/surfaces/tui/app/session_summary_test.go`,
  `internal/evidence/transcript/debuglog.go`,
  `internal/evidence/transcript/debuglog_test.go`,
  `internal/surfaces/cli/lane/post_analyze.go`,
  `internal/surfaces/cli/lane/post_analyze_test.go`,
  `internal/surfaces/cli/lane/explain.go`,
  `internal/surfaces/cli/lane/explain_test.go`
- A5.4 benchmark adapter:
  `docs/evidence/idiom-efficiency-optimization/scripts/benchmark-adapter.py`
  and fixtures.

Verification:

- `go test ./internal/runtime/llama ./internal/runtime/agent ./internal/surfaces/tui/app ./internal/evidence/transcript ./internal/surfaces/cli/lane -run 'Usage|Cache|Reasoning|SessionSummary|ModelCall|BudgetLedger|Explain|PostAnalyze' -count=1`
- `go test ./internal/evidence/transcript -run 'DebugLogger|SessionTelemetry' -count=1`
- `go build ./cmd/idiom`
- Benchmark adapter fixture checks listed in `agents/A5.4-benchmark-adapter.md`
