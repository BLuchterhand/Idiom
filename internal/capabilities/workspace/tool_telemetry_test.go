package workspace

import "testing"

func TestAddToolTelemetryAttributionUsesToolSidecarContractID(t *testing.T) {
	fields := map[string]any{}
	AddToolTelemetryAttribution(fields, ToolSidecarEnvelopeVersion, "mcp_stdio", ToolProfileDiscovery, ToolSidecarCaller{}, " context_status ")

	want := BuildToolSidecarContract("context_status", ToolProfileDiscovery, "mcp_stdio").ID
	if fields["contract_id"] != want {
		t.Fatalf("contract_id = %#v, want %q", fields["contract_id"], want)
	}
}

func TestToolRequestTelemetryFieldsSupportsValueAndPresenceModes(t *testing.T) {
	req := ToolRequest{
		Tool:      ToolNameContextStatus,
		Path:      " internal/capabilities ",
		Workdir:   " . ",
		Purpose:   " inspect context freshness ",
		Language:  " go ",
		Pattern:   "Run",
		Query:     "symbols",
		Command:   "go test ./...",
		Content:   "body",
		StartLine: 3,
		EndLine:   9,
		Limit:     4,
		Recursive: true,
	}
	caller := ToolSidecarCaller{CallerID: "caller-1", RequestID: "request-1", TraceID: "trace-1"}

	values := ToolRequestTelemetryFields(req, ToolTelemetryRequestOptions{
		Agent:         "direct",
		Transport:     "cli",
		Profile:       ToolProfileDefault,
		Caller:        caller,
		RawInputBytes: 321,
		TimeoutOwner:  "workspace_tool_timeout_with_cli_margin",
		RequestFields: ToolTelemetryRequestValues,
	})
	if values["path"] != "internal/capabilities" || values["workdir"] != "." || values["purpose"] != "inspect context freshness" {
		t.Fatalf("value request fields not projected: %#v", values)
	}
	if _, ok := values["has_path"]; ok {
		t.Fatalf("value mode should not emit presence path field: %#v", values)
	}
	if values["transport"] != "cli" || values["caller_id"] != "caller-1" || values["request_id"] != "request-1" || values["trace_id"] != "trace-1" {
		t.Fatalf("request attribution not projected: %#v", values)
	}
	if values["raw_input_bytes"] != 321 || values["timeout_owner"] != "workspace_tool_timeout_with_cli_margin" {
		t.Fatalf("request execution facts not projected: %#v", values)
	}
	if values["has_pattern"] != true || values["has_query"] != true || values["has_command"] != true || values["has_content"] != true {
		t.Fatalf("request content booleans not projected: %#v", values)
	}
	if values["discovery_class"] != ToolNameContextStatus || values["discovery_scope"] != "targeted" || values["discovery_cost"] != "low" {
		t.Fatalf("discovery facts not projected: %#v", values)
	}

	presence := ToolRequestTelemetryFields(req, ToolTelemetryRequestOptions{
		Agent:            "mcp",
		Transport:        "mcp_stdio",
		Profile:          ToolProfileDiscovery,
		Caller:           caller,
		RawInputBytes:    321,
		TelemetryEventID: "request-1:request",
		RequestFields:    ToolTelemetryRequestPresence,
	})
	if presence["has_path"] != true || presence["has_workdir"] != true || presence["has_purpose"] != true {
		t.Fatalf("presence request fields not projected: %#v", presence)
	}
	if _, ok := presence["path"]; ok {
		t.Fatalf("presence mode should not emit raw path field: %#v", presence)
	}
	if presence["telemetry_event_id"] != "request-1:request" || presence["transport"] != "mcp_stdio" {
		t.Fatalf("presence transport event facts not projected: %#v", presence)
	}
}

func TestToolResultTelemetryFieldsIncludesOutputAndAuditFacts(t *testing.T) {
	output := `{"indexed":true,"freshness":{"policy":"report_only","operation":"context_status","fresh_for_scope":true},"audit_event_ids":["event-0"],"events":[{"event_id":"event-1"}]}`
	req := ToolRequest{Tool: ToolNameContextStatus}
	result := ToolResult{
		Summary:       "Context index is fresh",
		Output:        output,
		PrecisionTier: "exact",
	}

	fields := ToolResultTelemetryFields(req, result, ToolTelemetryResultOptions{
		Agent:                "mcp",
		Transport:            "mcp_stdio",
		Profile:              ToolProfileDiscovery,
		Caller:               ToolSidecarCaller{RequestID: "request-1"},
		DurationMillis:       42,
		TelemetryEventID:     "request-1:result",
		IncludeAuditEventIDs: true,
	})
	if fields["tool"] != ToolNameContextStatus || fields["summary"] != "Context index is fresh" || fields["duration_ms"] != int64(42) {
		t.Fatalf("result basics not projected: %#v", fields)
	}
	if fields["output_chars"] != len(output) || fields["output_lines"] != 1 || fields["precision_tier"] != "exact" {
		t.Fatalf("result output facts not projected: %#v", fields)
	}
	ids, ok := fields["audit_event_ids"].([]string)
	if !ok || len(ids) != 2 || ids[0] != "event-0" || ids[1] != "event-1" {
		t.Fatalf("audit ids not projected: %#v", fields["audit_event_ids"])
	}
	if fields["context_indexed"] != true || fields["context_freshness_policy"] != "report_only" {
		t.Fatalf("tool-specific telemetry not merged: %#v", fields)
	}
	if fields["telemetry_event_id"] != "request-1:result" || fields["transport"] != "mcp_stdio" {
		t.Fatalf("result attribution not projected: %#v", fields)
	}
}
