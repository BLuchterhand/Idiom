package workspace

import (
	"strings"

	"idiom/internal/platform/textutil"
)

type ToolTelemetryRequestFieldMode string

const (
	ToolTelemetryRequestValues   ToolTelemetryRequestFieldMode = "values"
	ToolTelemetryRequestPresence ToolTelemetryRequestFieldMode = "presence"
)

type ToolTelemetryRequestOptions struct {
	Agent            string
	Transport        string
	Profile          string
	Caller           ToolSidecarCaller
	RawInputBytes    int
	TimeoutOwner     string
	TelemetryEventID string
	RequestFields    ToolTelemetryRequestFieldMode
}

type ToolTelemetryResultOptions struct {
	Agent                string
	Transport            string
	Profile              string
	Caller               ToolSidecarCaller
	DurationMillis       int64
	TimeoutOwner         string
	TelemetryEventID     string
	IncludeAuditEventIDs bool
}

func ToolRequestTelemetryFields(req ToolRequest, opts ToolTelemetryRequestOptions) map[string]any {
	tool := strings.TrimSpace(req.Tool)
	fields := map[string]any{
		"step":            1,
		"agent":           strings.TrimSpace(opts.Agent),
		"tool":            tool,
		"raw_input_bytes": opts.RawInputBytes,
		"language":        strings.TrimSpace(req.Language),
		"has_pattern":     strings.TrimSpace(req.Pattern) != "",
		"has_query":       strings.TrimSpace(req.Query) != "",
		"has_command":     strings.TrimSpace(req.Command) != "",
		"has_content":     strings.TrimSpace(req.Content) != "",
		"start_line":      req.StartLine,
		"end_line":        req.EndLine,
		"limit":           req.Limit,
		"recursive":       req.Recursive,
	}

	switch opts.RequestFields {
	case ToolTelemetryRequestPresence:
		fields["has_path"] = strings.TrimSpace(req.Path) != ""
		fields["has_workdir"] = strings.TrimSpace(req.Workdir) != ""
		fields["has_purpose"] = strings.TrimSpace(req.Purpose) != ""
	default:
		fields["path"] = strings.TrimSpace(req.Path)
		fields["workdir"] = strings.TrimSpace(req.Workdir)
		fields["purpose"] = strings.TrimSpace(req.Purpose)
	}

	if timeoutOwner := strings.TrimSpace(opts.TimeoutOwner); timeoutOwner != "" {
		fields["timeout_owner"] = timeoutOwner
	}

	AddToolTelemetryAttribution(fields, ToolSidecarEnvelopeVersion, strings.TrimSpace(opts.Transport), strings.TrimSpace(opts.Profile), opts.Caller, tool)

	if eventID := strings.TrimSpace(opts.TelemetryEventID); eventID != "" {
		fields["telemetry_event_id"] = eventID
	}

	addToolDiscoveryTelemetryFields(fields, req)

	return fields
}

func ToolResultTelemetryFields(req ToolRequest, result ToolResult, opts ToolTelemetryResultOptions) map[string]any {
	tool := textutil.FirstNonEmpty(result.Tool, req.Tool)
	fields := map[string]any{
		"step":        1,
		"agent":       strings.TrimSpace(opts.Agent),
		"tool":        tool,
		"summary":     result.Summary,
		"error":       result.Error,
		"duration_ms": opts.DurationMillis,
	}

	if timeoutOwner := strings.TrimSpace(opts.TimeoutOwner); timeoutOwner != "" {
		fields["timeout_owner"] = timeoutOwner
	}

	AddToolTelemetryAttribution(fields, ToolSidecarEnvelopeVersion, strings.TrimSpace(opts.Transport), strings.TrimSpace(opts.Profile), opts.Caller, tool)

	if eventID := strings.TrimSpace(opts.TelemetryEventID); eventID != "" {
		fields["telemetry_event_id"] = eventID
	}

	if result.Output != "" {
		fields["output_chars"] = len(result.Output)
		fields["output_lines"] = ToolOutputLineCount(result.Output)
	}

	if result.PrecisionTier != "" {
		fields["precision_tier"] = result.PrecisionTier
	}

	if opts.IncludeAuditEventIDs {
		envelope := BuildToolSidecarEnvelopeWithOptions(req, result, opts.Profile, ToolSidecarOptions{
			Caller:    opts.Caller,
			Transport: opts.Transport,
		})
		if len(envelope.AuditEventIDs) > 0 {
			fields["audit_event_ids"] = append([]string(nil), envelope.AuditEventIDs...)
		}
	}

	addToolDiscoveryTelemetryFields(fields, req)

	for key, value := range ToolTelemetryFields(tool, result.Output) {
		fields[key] = value
	}

	return fields
}

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

func addToolDiscoveryTelemetryFields(fields map[string]any, req ToolRequest) {
	profile := DiscoveryProfileForRequest(req)
	if !profile.Discovery {
		return
	}

	fields["discovery_class"] = profile.Class
	fields["discovery_scope"] = profile.Scope
	fields["discovery_cost"] = profile.Cost
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
