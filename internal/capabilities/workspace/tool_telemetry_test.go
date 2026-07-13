package workspace

import (
	"fmt"
	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("tool telemetry", func() {
	ginkgo.It("add tool telemetry attribution uses tool sidecar contract id", func() {
		fields := map[string]any{}
		AddToolTelemetryAttribution(fields, ToolSidecarEnvelopeVersion, "mcp_stdio", ToolProfileDiscovery, ToolSidecarCaller{}, " context_status ")

		want := BuildToolSidecarContract("context_status", ToolProfileDiscovery, "mcp_stdio").ID
		gomega.Expect(fields["contract_id"]).To(gomega.Equal(want), fmt.Sprintf("contract_id = %#v, want %q", fields["contract_id"], want))

	})
	ginkgo.It("tool request telemetry fields supports value and presence modes", func() {
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
		gomega.Expect(values["path"]).To(gomega.Equal("internal/capabilities"), fmt.Sprintf("value request fields not projected: %#v", values))
		gomega.Expect(values["workdir"]).To(gomega.Equal("."), fmt.Sprintf("value request fields not projected: %#v", values))
		gomega.Expect(values["purpose"]).To(gomega.Equal("inspect context freshness"), fmt.Sprintf("value request fields not projected: %#v", values))

		if _, ok := values["has_path"]; ok {
			ginkgo.Fail(fmt.Sprintf("value mode should not emit presence path field: %#v", values))
		}
		gomega.Expect(values["transport"]).To(gomega.Equal("cli"), fmt.Sprintf("request attribution not projected: %#v", values))
		gomega.Expect(values["caller_id"]).To(gomega.Equal("caller-1"), fmt.Sprintf("request attribution not projected: %#v", values))
		gomega.Expect(values["request_id"]).To(gomega.Equal("request-1"), fmt.Sprintf("request attribution not projected: %#v", values))
		gomega.Expect(values["trace_id"]).To(gomega.Equal("trace-1"), fmt.Sprintf("request attribution not projected: %#v", values))
		gomega.Expect(values["raw_input_bytes"]).To(gomega.BeNumerically("==", 321), fmt.Sprintf("request execution facts not projected: %#v", values))
		gomega.Expect(values["timeout_owner"]).To(gomega.Equal("workspace_tool_timeout_with_cli_margin"), fmt.Sprintf("request execution facts not projected: %#v", values))
		gomega.Expect(values["has_pattern"]).To(gomega.BeTrue(), fmt.Sprintf("request content booleans not projected: %#v", values))
		gomega.Expect(values["has_query"]).To(gomega.BeTrue(), fmt.Sprintf("request content booleans not projected: %#v", values))
		gomega.Expect(values["has_command"]).To(gomega.BeTrue(), fmt.Sprintf("request content booleans not projected: %#v", values))
		gomega.Expect(values["has_content"]).To(gomega.BeTrue(), fmt.Sprintf("request content booleans not projected: %#v", values))
		gomega.Expect(values["discovery_class"]).To(gomega.Equal(ToolNameContextStatus), fmt.Sprintf("discovery facts not projected: %#v", values))
		gomega.Expect(values["discovery_scope"]).To(gomega.Equal("targeted"), fmt.Sprintf("discovery facts not projected: %#v", values))
		gomega.Expect(values["discovery_cost"]).To(gomega.Equal("low"), fmt.Sprintf("discovery facts not projected: %#v", values))

		presence := ToolRequestTelemetryFields(req, ToolTelemetryRequestOptions{
			Agent:            "mcp",
			Transport:        "mcp_stdio",
			Profile:          ToolProfileDiscovery,
			Caller:           caller,
			RawInputBytes:    321,
			TelemetryEventID: "request-1:request",
			RequestFields:    ToolTelemetryRequestPresence,
		})
		gomega.Expect(presence["has_path"]).To(gomega.BeTrue(), fmt.Sprintf("presence request fields not projected: %#v", presence))
		gomega.Expect(presence["has_workdir"]).To(gomega.BeTrue(), fmt.Sprintf("presence request fields not projected: %#v", presence))
		gomega.Expect(presence["has_purpose"]).To(gomega.BeTrue(), fmt.Sprintf("presence request fields not projected: %#v", presence))

		if _, ok := presence["path"]; ok {
			ginkgo.Fail(fmt.Sprintf("presence mode should not emit raw path field: %#v", presence))
		}
		gomega.Expect(presence["telemetry_event_id"]).To(gomega.Equal("request-1:request"), fmt.Sprintf("presence transport event facts not projected: %#v", presence))
		gomega.Expect(presence["transport"]).To(gomega.Equal("mcp_stdio"), fmt.Sprintf("presence transport event facts not projected: %#v", presence))

	})
	ginkgo.It("tool result telemetry fields includes output and audit facts", func() {
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
		gomega.Expect(fields["tool"]).To(gomega.Equal(ToolNameContextStatus), fmt.Sprintf("result basics not projected: %#v", fields))
		gomega.Expect(fields["summary"]).To(gomega.Equal("Context index is fresh"), fmt.Sprintf("result basics not projected: %#v", fields))
		gomega.Expect(fields["duration_ms"]).To(gomega.Equal(int64(42)), fmt.Sprintf("result basics not projected: %#v", fields))
		gomega.Expect(fields["output_chars"]).To(gomega.Equal(len(output)), fmt.Sprintf("result output facts not projected: %#v", fields))
		gomega.Expect(fields["output_lines"]).To(gomega.BeNumerically("==", 1), fmt.Sprintf("result output facts not projected: %#v", fields))
		gomega.Expect(fields["precision_tier"]).To(gomega.Equal("exact"), fmt.Sprintf("result output facts not projected: %#v", fields))

		ids, ok := fields["audit_event_ids"].([]string)
		gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("audit ids not projected: %#v", fields["audit_event_ids"]))
		gomega.Expect(ids).To(gomega.HaveLen(2), fmt.Sprintf("audit ids not projected: %#v", fields["audit_event_ids"]))
		gomega.Expect(ids[0]).To(gomega.Equal("event-0"), fmt.Sprintf("audit ids not projected: %#v", fields["audit_event_ids"]))
		gomega.Expect(ids[1]).To(gomega.Equal("event-1"), fmt.Sprintf("audit ids not projected: %#v", fields["audit_event_ids"]))
		gomega.Expect(fields["context_indexed"]).To(gomega.BeTrue(), fmt.Sprintf("tool-specific telemetry not merged: %#v", fields))
		gomega.Expect(fields["context_freshness_policy"]).To(gomega.Equal("report_only"), fmt.Sprintf("tool-specific telemetry not merged: %#v", fields))
		gomega.Expect(fields["telemetry_event_id"]).To(gomega.Equal("request-1:result"), fmt.Sprintf("result attribution not projected: %#v", fields))
		gomega.Expect(fields["transport"]).To(gomega.Equal("mcp_stdio"), fmt.Sprintf("result attribution not projected: %#v", fields))

	})
})
