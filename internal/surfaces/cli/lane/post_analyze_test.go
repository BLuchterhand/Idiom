package lane

import (
	"testing"

	queuecontract "idiom/internal/contracts/queue"
)

func TestAnalyzeLaneOutputFactsExtractsSessionSummaryTokens(t *testing.T) {
	out := []byte(`{"type":"start","prompt":"x"}
{"type":"tool_request","tool":"read_file"}
{"type":"session_summary","session_id":"abc123","model_calls":5,"tool_calls":4,"prompt_tokens":12345,"completion_tokens":678,"total_tokens":13023,"peak_prompt_tokens":9000,"ctx_size":262144,"duration_ms":4500,"stop_reason":"model_final","turns":4}
{"type":"final","content":"done"}
`)
	facts := analyzeLaneOutputFacts(out)

	if !facts.HasSessionSummary {
		t.Fatal("expected session_summary to be parsed")
	}

	summary := facts.SessionSummary
	if summary.PromptTokens != 12345 || summary.CompletionTokens != 678 || summary.TotalTokens != 13023 {
		t.Fatalf("got %#v", summary)
	}

	if summary.SessionID != "abc123" || summary.ModelCalls != 5 || summary.ToolCalls != 4 || summary.PeakPromptTokens != 9000 || summary.ContextSize != 262144 || summary.DurationMS != 4500 || summary.StopReason != "model_final" {
		t.Fatalf("missing run metrics: %#v", summary)
	}
}

func TestAnalyzeLaneOutputFactsExtractsProviderCacheUsage(t *testing.T) {
	out := []byte(`{"type":"session_summary","prompt_tokens":13,"completion_tokens":3,"total_tokens":16,"prompt_cache_hit_tokens":8,"prompt_cache_miss_tokens":5,"prompt_cache_accounting_known":true,"reasoning_tokens":2}
`)
	facts := analyzeLaneOutputFacts(out)

	if !facts.HasSessionSummary {
		t.Fatal("expected session_summary to be parsed")
	}

	summary := facts.SessionSummary
	if !summary.PromptCacheAccountingKnown ||
		summary.PromptCacheHitTokens != 8 ||
		summary.PromptCacheMissTokens != 5 ||
		summary.ReasoningTokens != 2 {

		t.Fatalf("missing cache usage: %#v", summary)
	}
}

func TestAnalyzeLaneOutputFactsHasNoSessionSummaryWhenAbsent(t *testing.T) {
	out := []byte(`{"type":"start"}
some human-readable output
{"type":"final","content":"done"}
`)
	if facts := analyzeLaneOutputFacts(out); facts.HasSessionSummary {
		t.Fatal("expected ok=false when no session_summary event present")
	}
}

func TestAnalyzeLaneOutputFactsPicksLastSessionSummary(t *testing.T) {
	// If the subcommand emits multiple summaries (e.g. error recovery
	// re-runs a Close() path), the final one is authoritative.
	out := []byte(`{"type":"session_summary","prompt_tokens":100,"completion_tokens":10,"total_tokens":110}
{"type":"session_summary","prompt_tokens":999,"completion_tokens":99,"total_tokens":1098}
`)
	facts := analyzeLaneOutputFacts(out)

	if !facts.HasSessionSummary {
		t.Fatal("expected session_summary to be parsed")
	}

	summary := facts.SessionSummary
	if summary.TotalTokens != 1098 {
		t.Fatalf("expected last summary's totals, got TotalTokens=%d", summary.TotalTokens)
	}
}

func TestAnalyzeLaneOutputFactsIgnoresMalformedLines(t *testing.T) {
	out := []byte(`not json
{"type":"session_summary"
{"type":"session_summary","prompt_tokens":42,"completion_tokens":8,"total_tokens":50}
also not json
`)
	facts := analyzeLaneOutputFacts(out)

	if !facts.HasSessionSummary {
		t.Fatal("expected session_summary to be parsed")
	}

	summary := facts.SessionSummary
	if summary.TotalTokens != 50 {
		t.Fatalf("got %#v", summary)
	}
}

func TestAnalyzeLaneOutputFactsUsesLastImplementationStateSnapshot(t *testing.T) {
	out := []byte(`{"type":"implementation_lifecycle_state","phase":"implementation.target_write_ready","stop_reason":"model_call_budget_exhausted","remaining_required_edit_paths":["internal/runtime/agent/runner_core.go"],"sourced_evidence_paths":["internal/runtime/agent/implementation_lifecycle.go"],"pending_redirects":[{"path":"internal/runtime/agent/runner_core.go","pending_tool":"read_file","source":"required_edit_path","detail":"refresh source"}],"write_ready_nudge_count":1}
{"type":"completion_status","status":"blocked","phase":"implementation.target_write_ready","stop_reason":"model_call_budget_exhausted","remaining_required_edit_paths":["internal/runtime/agent/runner_core.go"],"changed_paths":["internal/runtime/agent/implementation_lifecycle.go"],"sourced_evidence_paths":["internal/runtime/agent/implementation_lifecycle.go"],"pending_redirects":[{"path":"internal/runtime/agent/runner_core.go","pending_tool":"read_file","source":"required_edit_path","detail":"refresh source"}],"write_ready_nudge_count":2}
`)
	facts := analyzeLaneOutputFacts(out)
	if facts.ImplementationState == nil {
		t.Fatal("expected implementation state")
	}

	state := facts.ImplementationState
	if state.Phase != "implementation.target_write_ready" || state.StopReason != "model_call_budget_exhausted" {
		t.Fatalf("unexpected lifecycle state: %#v", state)
	}

	if state.WriteReadyNudgeCount != 2 {
		t.Fatalf("expected completion_status to update nudge count, got %#v", state)
	}

	if len(state.RemainingRequiredEditPaths) != 1 || state.RemainingRequiredEditPaths[0] != "internal/runtime/agent/runner_core.go" {
		t.Fatalf("missing remaining paths: %#v", state)
	}

	if len(state.PendingRedirects) != 1 || state.PendingRedirects[0].PendingTool != "read_file" {
		t.Fatalf("missing pending redirect: %#v", state.PendingRedirects)
	}
}

func TestAnalyzeLaneOutputFactsLastImplementationStateClearsStaleSlices(t *testing.T) {
	out := []byte(`{"type":"implementation_lifecycle_state","phase":"implementation.target_write_ready","remaining_required_edit_paths":["internal/runtime/agent/runner_core.go"],"pending_redirects":[{"path":"internal/runtime/agent/runner_core.go","pending_tool":"read_file"}]}
{"type":"completion_status","status":"blocked","changed_paths":["internal/runtime/agent/implementation_lifecycle.go"]}
`)
	facts := analyzeLaneOutputFacts(out)
	if facts.ImplementationState == nil {
		t.Fatal("expected implementation state")
	}

	state := facts.ImplementationState
	if len(state.RemainingRequiredEditPaths) != 0 || len(state.PendingRedirects) != 0 {
		t.Fatalf("later snapshot should clear stale paths and redirects, got %#v", state)
	}

	if len(state.ChangedPaths) != 1 || state.ChangedPaths[0] != "internal/runtime/agent/implementation_lifecycle.go" {
		t.Fatalf("expected changed path from last snapshot, got %#v", state)
	}
}

func TestAnalyzeLaneOutputFactsDecodesImplementationStateProjection(t *testing.T) {
	facts := analyzeLaneOutputFacts([]byte(`{"type":"completion_status","phase":"implementation.target_write_ready","stop_reason":"model_call_budget_exhausted","remaining_required_edit_paths":["a.go"],"pending_redirects":[{"path":"a.go","pending_tool":"read_file"}],"write_ready_nudge_count":2}`))
	if facts.ImplementationState == nil {
		t.Fatal("expected implementation state projection")
	}

	state := facts.ImplementationState
	if state.Phase != "implementation.target_write_ready" ||
		state.StopReason != "model_call_budget_exhausted" ||
		state.WriteReadyNudgeCount != 2 ||
		len(state.PendingRedirects) != 1 {

		t.Fatalf("unexpected projection: %#v", state)
	}
}

func TestAnalyzeLaneOutputFactsExtractsQueuePool(t *testing.T) {
	out := []byte(`{"type":"` + queuecontract.CommandAdmissionEventName(queuecontract.CommandEventPrefixPlanExecute, queuecontract.AdmissionEventQueueAcquired) + `","pool":"qwen-35b"}
{"type":"session_summary","total_tokens":50}
`)
	if got := analyzeLaneOutputFacts(out).QueuePool; got != "qwen-35b" {
		t.Fatalf("pool = %q, want qwen-35b", got)
	}
}
