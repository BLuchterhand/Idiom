package main

import "testing"

func TestParseSessionSummaryExtractsTokens(t *testing.T) {
	out := []byte(`{"type":"start","prompt":"x"}
{"type":"tool_request","tool":"read_file"}
{"type":"session_summary","prompt_tokens":12345,"completion_tokens":678,"total_tokens":13023,"turns":4}
{"type":"final","content":"done"}
`)
	summary, ok := parseSessionSummary(out)
	if !ok {
		t.Fatal("expected session_summary to be parsed")
	}
	if summary.PromptTokens != 12345 || summary.CompletionTokens != 678 || summary.TotalTokens != 13023 {
		t.Fatalf("got %#v", summary)
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
