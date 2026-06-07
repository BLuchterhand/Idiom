package workspace

import (
	"encoding/json"
	"strings"
	"testing"

	"idiom/internal/platform/failures"
)

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
	if envelope.Contract.Transport != "cli" {
		t.Fatalf("default sidecar transport should remain cli, got %#v", envelope.Contract)
	}
}

func TestBuildToolSidecarEnvelopeCanAttributeMCPStdioTransport(t *testing.T) {
	caller, err := NewToolSidecarCaller("mcp-client", "req-456", "trace:mcp")
	if err != nil {
		t.Fatalf("caller metadata should validate: %v", err)
	}
	envelope := BuildToolSidecarEnvelopeWithOptions(
		ToolRequest{Tool: "context_status"},
		ToolResult{Tool: "context_status", Summary: "Context status", Output: `{"freshness":{"policy":"report_only"}}`},
		"discovery",
		ToolSidecarOptions{Caller: caller, Transport: "mcp_stdio"},
	)
	if envelope.Caller.CallerID != "mcp-client" || envelope.Caller.RequestID != "req-456" || envelope.Caller.TraceID != "trace:mcp" {
		t.Fatalf("MCP caller metadata not carried through: %#v", envelope.Caller)
	}
	if envelope.Contract.Transport != "mcp_stdio" {
		t.Fatalf("MCP sidecar transport should be mcp_stdio, got %#v", envelope.Contract)
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

func TestBuildToolSidecarEnvelopePrefersTypedFailureMetadata(t *testing.T) {
	result := ToolResult{
		Tool:    "context_query",
		Summary: "Context query failed",
		Error:   "invalid input: pattern is required",
		Failure: failures.New(failures.PermissionDenied, failures.NoReason, "permission denied by policy"),
	}
	envelope := BuildToolSidecarEnvelope(ToolRequest{Tool: "context_query"}, result, "default")
	if envelope.Error == nil {
		t.Fatalf("expected structured error")
	}
	if envelope.Error.Class != "permission_policy" || envelope.Error.Code != "permission_policy" {
		t.Fatalf("typed failure should override message classification, got %#v", envelope.Error)
	}
	if envelope.Error.Message != result.Error {
		t.Fatalf("error message should preserve legacy text, got %#v", envelope.Error)
	}
}

func TestBuildToolSidecarEnvelopePreservesStructuredResultCode(t *testing.T) {
	result := ToolResult{
		Tool:    "context_status",
		Code:    "profile_denied",
		Summary: "Tool request denied by discovery profile",
		Error:   "profile_denied: read_only_profile: refresh requests are not allowed",
		Failure: failures.New(failures.PermissionDenied, failures.NoReason, "refresh requests are not allowed"),
	}
	envelope := BuildToolSidecarEnvelope(ToolRequest{Tool: "context_status"}, result, "discovery")
	if envelope.Error == nil {
		t.Fatalf("expected structured error")
	}
	if envelope.Error.Class != "profile_denied" || envelope.Error.Code != "profile_denied" {
		t.Fatalf("structured result code should override generic typed failure, got %#v", envelope.Error)
	}
}

func TestBuildToolSidecarEnvelopeFallsBackToStringClassification(t *testing.T) {
	result := ToolResult{
		Tool:    "context_query",
		Summary: "Context query rejected",
		Error:   "invalid input: pattern is required",
	}
	envelope := BuildToolSidecarEnvelope(ToolRequest{Tool: "context_query"}, result, "default")
	if envelope.Error == nil {
		t.Fatalf("expected structured error")
	}
	if envelope.Error.Class != "invalid_input" || envelope.Error.Code != "invalid_input" {
		t.Fatalf("legacy message classification should still work, got %#v", envelope.Error)
	}
}

func TestBuildToolSidecarEnvelopeRendersTypedFailureWithoutLegacyError(t *testing.T) {
	result := ToolResult{
		Tool:    "context_query",
		Summary: "Context query failed",
		Failure: failures.New(failures.DeadlineExceeded, failures.NoReason, "context_query exceeded its deadline"),
	}
	envelope := BuildToolSidecarEnvelope(ToolRequest{Tool: "context_query"}, result, "default")
	if envelope.Error == nil {
		t.Fatalf("expected structured error")
	}
	if envelope.Error.Class != "timeout" || envelope.Error.Code != "timeout" {
		t.Fatalf("typed failure should classify timeout, got %#v", envelope.Error)
	}
	if !strings.Contains(envelope.Error.Message, "context_query exceeded its deadline") {
		t.Fatalf("typed failure message should be rendered when legacy Error is empty, got %#v", envelope.Error)
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

func TestBuildToolSidecarInspectionReportRendersReadOnlyArtifactBoundary(t *testing.T) {
	report := BuildToolSidecarInspectionReport(ToolSidecarInspectionReportInput{
		Artifact: ToolSidecarInspectionArtifact{
			ID:     "artifact-123",
			Kind:   "json",
			Path:   "reports/sample.json",
			Status: "available",
		},
		ReadStatus: ToolSidecarInspectionStatus{
			Status:   "read",
			Evidence: []string{"reports/sample.json:1"},
		},
		ParseStatus: ToolSidecarInspectionStatus{
			Status: "parsed",
		},
		Freshness: map[string]any{
			"policy":          "report_only",
			"fresh_for_scope": true,
		},
		Provenance: map[string]any{
			"source": "tool_output",
		},
		Findings: []ToolSidecarInspectionFinding{{
			Code:     "field_present",
			Severity: "low",
			Message:  "field was present in parsed output",
			Evidence: []string{"reports/sample.json:2"},
		}},
		Blockers: []ToolSidecarInspectionFinding{{
			Code:    "parse_gap",
			Message: "nested value could not be parsed",
		}},
		Warnings: []ToolSidecarWarning{{
			Code:    "partial_read",
			Message: "read was bounded",
		}},
		AuditEventIDs: []string{"event-2", "event-1", "event-2"},
	})

	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal inspection report: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatalf("unmarshal inspection report JSON: %v", err)
	}
	if payload["schema_version"] != ToolSidecarInspectionReportVersion {
		t.Fatalf("schema_version = %#v, want %q", payload["schema_version"], ToolSidecarInspectionReportVersion)
	}
	if payload["side_effects"] != ToolSidecarInspectionNoSideEffects {
		t.Fatalf("side_effects = %#v, want %q", payload["side_effects"], ToolSidecarInspectionNoSideEffects)
	}
	for _, forbiddenKey := range []string{"mutation", "mutations", "edit", "edits", "write", "writes", "write_paths", "required_edit_paths", "patch", "diff"} {
		if jsonObjectContainsKey(payload, forbiddenKey) {
			t.Fatalf("inspection report JSON should not expose mutation field %q: %s", forbiddenKey, encoded)
		}
	}
	artifact, ok := payload["artifact"].(map[string]any)
	if !ok {
		t.Fatalf("artifact should be an object, got %#v", payload["artifact"])
	}
	if artifact["id"] != "artifact-123" || artifact["kind"] != "json" || artifact["path"] != "reports/sample.json" || artifact["status"] != "available" {
		t.Fatalf("artifact identity not rendered: %#v", artifact)
	}
	readStatus, ok := payload["read_status"].(map[string]any)
	if !ok || readStatus["status"] != "read" {
		t.Fatalf("read_status not rendered: %#v", payload["read_status"])
	}
	parseStatus, ok := payload["parse_status"].(map[string]any)
	if !ok || parseStatus["status"] != "parsed" {
		t.Fatalf("parse_status not rendered: %#v", payload["parse_status"])
	}
	if _, ok := payload["freshness"].(map[string]any); !ok {
		t.Fatalf("freshness should be preserved when available: %#v", payload["freshness"])
	}
	if _, ok := payload["provenance"].(map[string]any); !ok {
		t.Fatalf("provenance should be preserved when available: %#v", payload["provenance"])
	}
	if findings, ok := payload["findings"].([]any); !ok || len(findings) != 1 {
		t.Fatalf("findings not rendered: %#v", payload["findings"])
	}
	if blockers, ok := payload["blockers"].([]any); !ok || len(blockers) != 1 {
		t.Fatalf("blockers not rendered: %#v", payload["blockers"])
	}
	if warnings, ok := payload["warnings"].([]any); !ok || len(warnings) != 1 {
		t.Fatalf("warnings not rendered: %#v", payload["warnings"])
	}
	auditIDs, ok := payload["audit_event_ids"].([]any)
	if !ok || len(auditIDs) != 2 || auditIDs[0] != "event-1" || auditIDs[1] != "event-2" {
		t.Fatalf("audit IDs should be deduplicated and sorted: %#v", payload["audit_event_ids"])
	}
}

func TestBuildToolSidecarInspectionReportDoesNotInjectProductLifecycleLanguage(t *testing.T) {
	report := BuildToolSidecarInspectionReport(ToolSidecarInspectionReportInput{
		Artifact: ToolSidecarInspectionArtifact{
			Kind:   "markdown",
			Path:   "docs/example.md",
			Status: "available",
		},
		ReadStatus:  ToolSidecarInspectionStatus{Status: "read"},
		ParseStatus: ToolSidecarInspectionStatus{Status: "parsed"},
		Findings: []ToolSidecarInspectionFinding{{
			Code:    "heading_seen",
			Message: "heading was observed",
		}},
	})

	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal inspection report: %v", err)
	}
	rendered := strings.ToLower(string(encoded))
	for _, forbidden := range []string{"prd", "adr", "brief", "dispatch", "status migration", "lifecycle"} {
		if strings.Contains(rendered, forbidden) {
			t.Fatalf("inspection report should not inject product-specific lifecycle language %q: %s", forbidden, encoded)
		}
	}
}

func TestBuildToolSidecarInspectionReportSanitizesMetadataMutationKeys(t *testing.T) {
	report := BuildToolSidecarInspectionReport(ToolSidecarInspectionReportInput{
		Artifact:    ToolSidecarInspectionArtifact{Kind: "json", Path: "reports/sample.json", Status: "available"},
		ReadStatus:  ToolSidecarInspectionStatus{Status: "read"},
		ParseStatus: ToolSidecarInspectionStatus{Status: "parsed"},
		Freshness: map[string]any{
			"fresh_for_scope": true,
			"patch":           "*** Begin Patch",
			"nested": map[string]any{
				"required_edit_paths": []any{"a.go"},
				"source":              "audit",
			},
		},
		Provenance: map[string]any{
			"source": "tool_output",
			"writes": []any{"a.go"},
		},
	})

	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal inspection report: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatalf("unmarshal inspection report JSON: %v", err)
	}
	for _, forbiddenKey := range []string{"mutation", "mutations", "edit", "edits", "write", "writes", "write_paths", "required_edit_paths", "patch", "diff"} {
		if jsonObjectContainsKey(payload, forbiddenKey) {
			t.Fatalf("inspection report JSON should sanitize metadata mutation field %q: %s", forbiddenKey, encoded)
		}
	}
	freshness := payload["freshness"].(map[string]any)
	if freshness["fresh_for_scope"] != true {
		t.Fatalf("safe freshness metadata should be preserved: %#v", freshness)
	}
	nested := freshness["nested"].(map[string]any)
	if nested["source"] != "audit" {
		t.Fatalf("safe nested metadata should be preserved: %#v", nested)
	}
}

func jsonObjectContainsKey(value any, key string) bool {
	switch typed := value.(type) {
	case map[string]any:
		for objectKey, child := range typed {
			if objectKey == key || jsonObjectContainsKey(child, key) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if jsonObjectContainsKey(child, key) {
				return true
			}
		}
	}
	return false
}
