package lane

import (
	"testing"

	queuecontract "idiom/internal/contracts/queue"
)

func TestParseSessionSummaryExtractsTokens(t *testing.T) {
	out := []byte(`{"type":"start","prompt":"x"}
{"type":"tool_request","tool":"read_file"}
{"type":"session_summary","session_id":"abc123","model_calls":5,"tool_calls":4,"prompt_tokens":12345,"completion_tokens":678,"total_tokens":13023,"peak_prompt_tokens":9000,"ctx_size":262144,"duration_ms":4500,"stop_reason":"model_final","turns":4}
{"type":"final","content":"done"}
`)
	summary, ok := parseSessionSummary(out)

	if !ok {
		t.Fatal("expected session_summary to be parsed")
	}

	if summary.PromptTokens != 12345 || summary.CompletionTokens != 678 || summary.TotalTokens != 13023 {
		t.Fatalf("got %#v", summary)
	}

	if summary.SessionID != "abc123" || summary.ModelCalls != 5 || summary.ToolCalls != 4 || summary.PeakPromptTokens != 9000 || summary.ContextSize != 262144 || summary.DurationMS != 4500 || summary.StopReason != "model_final" {
		t.Fatalf("missing run metrics: %#v", summary)
	}
}

func TestParseSessionSummaryExtractsProviderCacheUsage(t *testing.T) {
	out := []byte(`{"type":"session_summary","prompt_tokens":13,"completion_tokens":3,"total_tokens":16,"prompt_cache_hit_tokens":8,"prompt_cache_miss_tokens":5,"prompt_cache_accounting_known":true,"reasoning_tokens":2}
`)
	summary, ok := parseSessionSummary(out)

	if !ok {
		t.Fatal("expected session_summary to be parsed")
	}

	if !summary.PromptCacheAccountingKnown ||
		summary.PromptCacheHitTokens != 8 ||
		summary.PromptCacheMissTokens != 5 ||
		summary.ReasoningTokens != 2 {

		t.Fatalf("missing cache usage: %#v", summary)
	}
}

func TestParseSessionSummaryReturnsFalseWhenAbsent(t *testing.T) {
	out := []byte(`{"type":"start"}
some human-readable output
{"type":"final","content":"done"}
`)
	if _, ok := parseSessionSummary(out); ok {
		t.Fatal("expected ok=false when no session_summary event present")
	}
}

func TestParseSessionSummaryPicksLastWhenMultiple(t *testing.T) {
	// If the subcommand emits multiple summaries (e.g. error recovery
	// re-runs a Close() path), the final one is authoritative.
	out := []byte(`{"type":"session_summary","prompt_tokens":100,"completion_tokens":10,"total_tokens":110}
{"type":"session_summary","prompt_tokens":999,"completion_tokens":99,"total_tokens":1098}
`)
	summary, ok := parseSessionSummary(out)

	if !ok {
		t.Fatal("expected session_summary to be parsed")
	}

	if summary.TotalTokens != 1098 {
		t.Fatalf("expected last summary's totals, got TotalTokens=%d", summary.TotalTokens)
	}
}

func TestParseSessionSummaryIgnoresMalformedLines(t *testing.T) {
	out := []byte(`not json
{"type":"session_summary"
{"type":"session_summary","prompt_tokens":42,"completion_tokens":8,"total_tokens":50}
also not json
`)
	summary, ok := parseSessionSummary(out)

	if !ok {
		t.Fatal("expected session_summary to be parsed")
	}

	if summary.TotalTokens != 50 {
		t.Fatalf("got %#v", summary)
	}
}

func TestParseLaneQueuePool(t *testing.T) {
	out := []byte(`{"type":"` + queuecontract.CommandAdmissionEventName(queuecontract.CommandEventPrefixPlanExecute, queuecontract.AdmissionEventQueueAcquired) + `","pool":"qwen-35b"}
{"type":"session_summary","total_tokens":50}
`)
	if got := parseLaneQueuePool(out); got != "qwen-35b" {
		t.Fatalf("pool = %q, want qwen-35b", got)
	}
}
