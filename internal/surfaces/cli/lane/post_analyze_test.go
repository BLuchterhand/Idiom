package lane

import (
	"fmt"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	queuecontract "idiom/internal/contracts/queue"
)

var _ = ginkgo.Describe("post analyze", func() {
	ginkgo.It("analyze lane output facts extracts session summary tokens", func() {
		out := []byte(`{"type":"start","prompt":"x"}
{"type":"tool_request","tool":"read_file"}
{"type":"session_summary","session_id":"abc123","model_calls":5,"tool_calls":4,"prompt_tokens":12345,"completion_tokens":678,"total_tokens":13023,"peak_prompt_tokens":9000,"ctx_size":262144,"duration_ms":4500,"stop_reason":"model_final","turns":4}
{"type":"final","content":"done"}
`)
		facts := analyzeLaneOutputFacts(out)
		gomega.Expect(facts.HasSessionSummary).To(gomega.BeTrue(), "expected session_summary to be parsed")

		summary := facts.SessionSummary
		gomega.Expect(summary.PromptTokens).To(gomega.Equal(12345), fmt.Sprintf("got %#v", summary))
		gomega.Expect(summary.CompletionTokens).To(gomega.Equal(678), fmt.Sprintf("got %#v", summary))
		gomega.Expect(summary.TotalTokens).To(gomega.Equal(13023), fmt.Sprintf("got %#v", summary))
		gomega.Expect(summary.SessionID).To(gomega.Equal("abc123"), fmt.Sprintf("missing run metrics: %#v", summary))
		gomega.Expect(summary.ModelCalls).To(gomega.Equal(5), fmt.Sprintf("missing run metrics: %#v", summary))
		gomega.Expect(summary.ToolCalls).To(gomega.Equal(4), fmt.Sprintf("missing run metrics: %#v", summary))
		gomega.Expect(summary.PeakPromptTokens).To(gomega.Equal(9000), fmt.Sprintf("missing run metrics: %#v", summary))
		gomega.Expect(summary.ContextSize).To(gomega.Equal(262144), fmt.Sprintf("missing run metrics: %#v", summary))
		gomega.Expect(summary.DurationMS).To(gomega.Equal(4500), fmt.Sprintf("missing run metrics: %#v", summary))
		gomega.Expect(summary.StopReason).To(gomega.Equal("model_final"), fmt.Sprintf("missing run metrics: %#v", summary))

	})
	ginkgo.It("analyze lane output facts extracts provider cache usage", func() {
		out := []byte(`{"type":"session_summary","prompt_tokens":13,"completion_tokens":3,"total_tokens":16,"prompt_cache_hit_tokens":8,"prompt_cache_miss_tokens":5,"prompt_cache_accounting_known":true,"reasoning_tokens":2}
`)
		facts := analyzeLaneOutputFacts(out)
		gomega.Expect(facts.HasSessionSummary).To(gomega.BeTrue(), "expected session_summary to be parsed")

		summary := facts.SessionSummary
		gomega.Expect(summary.PromptCacheAccountingKnown).To(gomega.BeTrue(), fmt.Sprintf("missing cache usage: %#v", summary))
		gomega.Expect(summary.PromptCacheHitTokens).To(gomega.Equal(8), fmt.Sprintf("missing cache usage: %#v", summary))
		gomega.Expect(summary.PromptCacheMissTokens).To(gomega.Equal(5), fmt.Sprintf("missing cache usage: %#v", summary))
		gomega.Expect(summary.ReasoningTokens).To(gomega.Equal(2), fmt.Sprintf("missing cache usage: %#v", summary))

	})
	ginkgo.It("analyze lane output facts has no session summary when absent", func() {
		out := []byte(`{"type":"start"}
some human-readable output
{"type":"final","content":"done"}
`)
		if facts := analyzeLaneOutputFacts(out); facts.HasSessionSummary {
			ginkgo.Fail("expected ok=false when no session_summary event present")
		}
	})
	ginkgo.It("analyze lane output facts picks last session summary", func() {

		out := []byte(`{"type":"session_summary","prompt_tokens":100,"completion_tokens":10,"total_tokens":110}
{"type":"session_summary","prompt_tokens":999,"completion_tokens":99,"total_tokens":1098}
`)
		facts := analyzeLaneOutputFacts(out)
		gomega.Expect(facts.HasSessionSummary).To(gomega.BeTrue(), "expected session_summary to be parsed")

		summary := facts.SessionSummary
		gomega.Expect(summary.TotalTokens).To(gomega.Equal(1098), fmt.Sprintf("expected last summary's totals, got TotalTokens=%d", summary.TotalTokens))

	})
	ginkgo.It("analyze lane output facts ignores malformed lines", func() {
		out := []byte(`not json
{"type":"session_summary"
{"type":"session_summary","prompt_tokens":42,"completion_tokens":8,"total_tokens":50}
also not json
`)
		facts := analyzeLaneOutputFacts(out)
		gomega.Expect(facts.HasSessionSummary).To(gomega.BeTrue(), "expected session_summary to be parsed")

		summary := facts.SessionSummary
		gomega.Expect(summary.TotalTokens).To(gomega.Equal(50), fmt.Sprintf("got %#v", summary))

	})
	ginkgo.It("analyze lane output facts uses last implementation state snapshot", func() {
		out := []byte(`{"type":"implementation_lifecycle_state","phase":"implementation.target_write_ready","stop_reason":"model_call_budget_exhausted","remaining_required_edit_paths":["internal/runtime/agent/runner_core.go"],"sourced_evidence_paths":["internal/runtime/agent/implementation_lifecycle.go"],"write_ready_nudge_count":1}
{"type":"completion_status","status":"blocked","phase":"implementation.target_write_ready","stop_reason":"model_call_budget_exhausted","remaining_required_edit_paths":["internal/runtime/agent/runner_core.go"],"changed_paths":["internal/runtime/agent/implementation_lifecycle.go"],"sourced_evidence_paths":["internal/runtime/agent/implementation_lifecycle.go"],"write_ready_nudge_count":2}
`)
		facts := analyzeLaneOutputFacts(out)
		gomega.Expect(facts.ImplementationState).NotTo(gomega.Equal(nil), "expected implementation state")

		state := facts.ImplementationState
		gomega.Expect(state.Phase).To(gomega.Equal("implementation.target_write_ready"), fmt.Sprintf("unexpected lifecycle state: %#v", state))
		gomega.Expect(state.StopReason).To(gomega.Equal("model_call_budget_exhausted"), fmt.Sprintf("unexpected lifecycle state: %#v", state))
		gomega.Expect(state.WriteReadyNudgeCount).To(gomega.Equal(2), fmt.Sprintf("expected completion_status to update nudge count, got %#v", state))
		gomega.Expect(state.RemainingRequiredEditPaths).To(gomega.HaveLen(1), fmt.Sprintf("missing remaining paths: %#v", state))
		gomega.Expect(state.RemainingRequiredEditPaths[0]).To(gomega.Equal("internal/runtime/agent/runner_core.go"), fmt.Sprintf("missing remaining paths: %#v", state))

	})
	ginkgo.It("analyze lane output facts last implementation state clears stale slices", func() {
		out := []byte(`{"type":"implementation_lifecycle_state","phase":"implementation.target_write_ready","remaining_required_edit_paths":["internal/runtime/agent/runner_core.go"]}
{"type":"completion_status","status":"blocked","changed_paths":["internal/runtime/agent/implementation_lifecycle.go"]}
`)
		facts := analyzeLaneOutputFacts(out)
		gomega.Expect(facts.ImplementationState).NotTo(gomega.Equal(nil), "expected implementation state")

		state := facts.ImplementationState
		gomega.Expect(state.RemainingRequiredEditPaths).To(gomega.BeEmpty(), fmt.Sprintf("later snapshot should clear stale paths and redirects, got %#v", state))
		gomega.Expect(state.ChangedPaths).To(gomega.HaveLen(1), fmt.Sprintf("expected changed path from last snapshot, got %#v", state))
		gomega.Expect(state.ChangedPaths[0]).To(gomega.Equal("internal/runtime/agent/implementation_lifecycle.go"), fmt.Sprintf("expected changed path from last snapshot, got %#v", state))

	})
	ginkgo.It("analyze lane output facts decodes implementation state projection", func() {
		facts := analyzeLaneOutputFacts([]byte(`{"type":"completion_status","phase":"implementation.target_write_ready","stop_reason":"model_call_budget_exhausted","remaining_required_edit_paths":["a.go"],"write_ready_nudge_count":2}`))
		gomega.Expect(facts.ImplementationState).NotTo(gomega.Equal(nil), "expected implementation state projection")

		state := facts.ImplementationState
		gomega.Expect(state.Phase).To(gomega.Equal("implementation.target_write_ready"), fmt.Sprintf("unexpected projection: %#v", state))
		gomega.Expect(state.StopReason).To(gomega.Equal("model_call_budget_exhausted"), fmt.Sprintf("unexpected projection: %#v", state))
		gomega.Expect(state.WriteReadyNudgeCount).To(gomega.Equal(2), fmt.Sprintf("unexpected projection: %#v", state))

	})
	ginkgo.It("analyze lane output facts extracts queue pool", func() {
		out := []byte(`{"type":"` + queuecontract.CommandAdmissionEventName(queuecontract.CommandEventPrefixPlanExecute, queuecontract.AdmissionEventQueueAcquired) + `","pool":"qwen-35b"}
{"type":"session_summary","total_tokens":50}
`)
		if got := analyzeLaneOutputFacts(out).QueuePool; got != "qwen-35b" {
			ginkgo.Fail(fmt.Sprintf("pool = %q, want qwen-35b", got))
		}
	})
})
