package workspace

import "testing"

func TestBuildToolSidecarEnvelopeIncludesStructuredFreshness(t *testing.T) {
	result := ToolResult{
		Tool:    "context_status",
		Summary: "Context index is fresh",
		Output:  `{"indexed":true,"freshness":{"policy":"report_only","operation":"context_status","fresh_for_scope":true},"warnings":["sample warning"],"audit_event_ids":["event-0"],"events":[{"event_id":"event-1"}]}`,
	}
	envelope := BuildToolSidecarEnvelope(ToolRequest{Tool: "context_status"}, result, "")
	if envelope.SchemaVersion != ToolSidecarEnvelopeVersion {
		t.Fatalf("schema version = %q, want %q", envelope.SchemaVersion, ToolSidecarEnvelopeVersion)
	}
	if envelope.Tool != "context_status" || envelope.Profile != "default" {
		t.Fatalf("unexpected tool/profile: %#v", envelope)
	}
	if envelope.Contract.ID != "idiom.tool.v1:context_status" || envelope.Contract.Metadata.Tags == nil {
		t.Fatalf("missing contract metadata/id: %#v", envelope.Contract)
	}
	freshness, ok := envelope.Freshness.(map[string]any)
	if !ok || freshness["operation"] != "context_status" || freshness["policy"] != "report_only" {
		t.Fatalf("freshness should be structured, got %#v", envelope.Freshness)
	}
	if len(envelope.Warnings) != 1 || envelope.Warnings[0].Message != "sample warning" {
		t.Fatalf("warnings not extracted: %#v", envelope.Warnings)
	}
	if len(envelope.AuditEventIDs) != 2 || envelope.AuditEventIDs[0] != "event-0" || envelope.AuditEventIDs[1] != "event-1" {
		t.Fatalf("audit event ids not extracted: %#v", envelope.AuditEventIDs)
	}
	if _, ok := envelope.StructuredOutput.(map[string]any); !ok {
		t.Fatalf("structured output should retain parsed JSON, got %#v", envelope.StructuredOutput)
	}
	if envelope.Error != nil {
		t.Fatalf("success envelope should not include error: %#v", envelope.Error)
	}
}

func TestBuildToolSidecarEnvelopeIncludesAllowlistedCallerMetadata(t *testing.T) {
	caller, err := NewToolSidecarCaller("codex-cli", "req-123", "trace:abc")
	if err != nil {
		t.Fatalf("caller metadata should validate: %v", err)
	}
	envelope := BuildToolSidecarEnvelopeWithOptions(
		ToolRequest{Tool: "pwd"},
		ToolResult{Tool: "pwd", Summary: "Workspace", Output: "/tmp/workspace"},
		"default",
		ToolSidecarOptions{Caller: caller},
	)
	if envelope.Caller.CallerID != "codex-cli" || envelope.Caller.RequestID != "req-123" || envelope.Caller.TraceID != "trace:abc" {
		t.Fatalf("caller metadata not carried through: %#v", envelope.Caller)
	}
}

func TestNewToolSidecarCallerRejectsLeakyValues(t *testing.T) {
	for _, tc := range []struct {
		name      string
		callerID  string
		requestID string
		traceID   string
	}{
		{name: "private path", callerID: "/Users/alice/private/repo"},
		{name: "home path", callerID: "~/private/repo"},
		{name: "windows path", callerID: `C:\Users\alice\repo`},
		{name: "ssh key path", callerID: ".ssh/id_rsa"},
		{name: "email address", callerID: "alice@example.com"},
		{name: "openai key prefix", callerID: "sk-proj-abc123"},
		{name: "openai underscore prefix", callerID: "sk_abc123"},
		{name: "github token prefix", callerID: "ghp_abc123"},
		{name: "github pat prefix", callerID: "github_pat_abc123"},
		{name: "slack token prefix", callerID: "xoxb-abc123"},
		{name: "jwt shaped", callerID: "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0In0.sflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c"},
		{name: "long hex blob", callerID: "0123456789abcdef0123456789abcdef"},
		{name: "long base64 blob", callerID: "dGhpc2lzYXZlcnlsb25nYmFzZTY0bGlrZXZhbHVldGhhdGxvb2tzbGlrZWFzZWNyZXQ"},
		{name: "secret word", requestID: "prod-token"},
		{name: "non ascii", traceID: "trace-π"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewToolSidecarCaller(tc.callerID, tc.requestID, tc.traceID); err == nil {
				t.Fatalf("expected leaky caller metadata to be rejected")
			}
		})
	}
}

func TestBuildToolSidecarEnvelopeExtractsStaleFreshnessWithoutGraphState(t *testing.T) {
	result := ToolResult{
		Tool:    "context_query",
		Summary: "Context query returned stale report-only evidence",
		Output:  `{"freshness":{"policy":"allow_stale","operation":"context_query","fresh_for_scope":false,"global_fresh":false,"stale_path_count":2,"missing_path_count":1},"nodes":[],"edges":[]}`,
	}
	envelope := BuildToolSidecarEnvelope(ToolRequest{Tool: "context_query"}, result, "discovery")
	freshness, ok := envelope.Freshness.(map[string]any)
	if !ok {
		t.Fatalf("freshness should be structured, got %#v", envelope.Freshness)
	}
	if freshness["policy"] != "allow_stale" || freshness["operation"] != "context_query" {
		t.Fatalf("unexpected freshness identity: %#v", freshness)
	}
	if freshness["fresh_for_scope"] != false || freshness["global_fresh"] != false {
		t.Fatalf("stale freshness booleans not preserved: %#v", freshness)
	}
	if freshness["stale_path_count"] != float64(2) || freshness["missing_path_count"] != float64(1) {
		t.Fatalf("stale counts not preserved: %#v", freshness)
	}
}

func TestBuildToolSidecarEnvelopePreservesTextOutput(t *testing.T) {
	output := "  first line\nsecond line\n"
	envelope := BuildToolSidecarEnvelope(ToolRequest{Tool: "read_file"}, ToolResult{
		Tool:    "read_file",
		Summary: "Read file",
		Output:  output,
	}, "default")
	structured, ok := envelope.StructuredOutput.(map[string]any)
	if !ok {
		t.Fatalf("expected wrapped text output, got %#v", envelope.StructuredOutput)
	}
	if structured["format"] != "text" || structured["text"] != output {
		t.Fatalf("text output should be preserved exactly, got %#v", structured)
	}
	if envelope.Freshness != nil {
		t.Fatalf("text output should not be scraped for freshness: %#v", envelope.Freshness)
	}
}

func TestBuildToolSidecarEnvelopeClassifiesStructuredError(t *testing.T) {
	result := ToolResult{
		Tool:    "not_a_tool",
		Summary: "Tool request rejected",
		Error:   `unsupported tool "not_a_tool"`,
	}
	envelope := BuildToolSidecarEnvelope(ToolRequest{Tool: "not_a_tool"}, result, "default")
	if envelope.SchemaVersion != ToolSidecarEnvelopeVersion {
		t.Fatalf("schema version = %q, want %q", envelope.SchemaVersion, ToolSidecarEnvelopeVersion)
	}
	if envelope.Error == nil {
		t.Fatalf("expected structured error")
	}
	if envelope.Error.Class != "unsupported_tool" || envelope.Error.Code != "unsupported_tool" {
		t.Fatalf("unexpected error classification: %#v", envelope.Error)
	}
	if envelope.Error.Message != result.Error {
		t.Fatalf("error message changed: %#v", envelope.Error)
	}
}

func TestBuildToolSidecarEnvelopeBoundsGenericStructuredError(t *testing.T) {
	longMessage := ""
	for len(longMessage) <= maxToolSidecarErrorMessageBytes+50 {
		longMessage += "tool failed with repeated diagnostic detail "
	}
	envelope := BuildToolSidecarEnvelope(ToolRequest{Tool: "context_query"}, ToolResult{
		Tool:    "context_query",
		Summary: "Context query failed",
		Error:   longMessage,
	}, "default")
	if envelope.Error == nil {
		t.Fatalf("expected structured error")
	}
	if envelope.Error.Class != "tool_failed" || envelope.Error.Code != "tool_failed" {
		t.Fatalf("unexpected generic error classification: %#v", envelope.Error)
	}
	if len(envelope.Error.Message) > maxToolSidecarErrorMessageBytes+len("...(truncated)") {
		t.Fatalf("structured error message was not bounded: len=%d", len(envelope.Error.Message))
	}
}
