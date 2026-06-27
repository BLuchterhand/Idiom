package workspace

import "strings"

func AddToolTelemetryAttribution(fields map[string]any, sidecarSchemaVersion string, transport string, profile string, caller ToolSidecarCaller, tool string) {
	fields["transport"] = transport
	fields["profile"] = profile
	fields["sidecar_schema_version"] = sidecarSchemaVersion
	fields["contract_id"] = ToolSidecarContractID(tool)
	fields["contract_kind"] = "workspace_tool_result"
	fields["contract_schema_version"] = ToolSchemaExportVersion

	if caller.CallerID != "" {
		fields["caller_id"] = caller.CallerID
	}

	if caller.RequestID != "" {
		fields["request_id"] = caller.RequestID
	}

	if caller.TraceID != "" {
		fields["trace_id"] = caller.TraceID
	}
}

func ToolOutputLineCount(output string) int {
	if output == "" {
		return 0
	}

	lines := strings.Count(output, "\n")
	if !strings.HasSuffix(output, "\n") {
		lines++
	}

	return lines
}
