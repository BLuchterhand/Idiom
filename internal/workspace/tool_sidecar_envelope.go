package workspace

import (
	"encoding/json"
	"sort"
	"strings"

	"idiom/internal/autonomy"
)

const ToolSidecarEnvelopeVersion = "idiom.tool.v1"
const maxToolSidecarErrorMessageBytes = 500

type ToolSidecarEnvelope struct {
	SchemaVersion    string                    `json:"schema_version"`
	Tool             string                    `json:"tool"`
	Profile          string                    `json:"profile"`
	Contract         ToolSidecarContract       `json:"contract"`
	Freshness        any                       `json:"freshness"`
	Warnings         []ToolSidecarWarning      `json:"warnings"`
	AuditEventIDs    []string                  `json:"audit_event_ids"`
	StructuredOutput any                       `json:"structured_output"`
	Error            *ToolSidecarError         `json:"error"`
	Metadata         autonomy.ContractMetadata `json:"metadata,omitempty"`
}

type ToolSidecarContract struct {
	ID                string                    `json:"id"`
	Kind              string                    `json:"kind"`
	Tool              string                    `json:"tool"`
	Profile           string                    `json:"profile"`
	SchemaVersion     string                    `json:"schema_version"`
	Transport         string                    `json:"transport"`
	DecisionAuthority string                    `json:"decision_authority,omitempty"`
	Policy            ToolSchemaPolicyExport    `json:"policy,omitempty"`
	Metadata          autonomy.ContractMetadata `json:"metadata,omitempty"`
}

type ToolSidecarWarning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type ToolSidecarError struct {
	Class   string `json:"class"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func BuildToolSidecarEnvelope(req ToolRequest, result ToolResult, profile string) ToolSidecarEnvelope {
	tool := firstNonEmptyString(strings.TrimSpace(result.Tool), strings.TrimSpace(req.Tool))
	profile = firstNonEmptyString(strings.TrimSpace(profile), "default")
	structuredOutput := toolSidecarStructuredOutput(result.Output)
	outputObject := toolSidecarOutputObject(structuredOutput)
	metadata := autonomy.DefaultContractMetadata("p7.1", "tool_sidecar_envelope")
	return ToolSidecarEnvelope{
		SchemaVersion:    ToolSidecarEnvelopeVersion,
		Tool:             tool,
		Profile:          profile,
		Contract:         toolSidecarContract(tool, profile, metadata),
		Freshness:        toolSidecarFreshness(outputObject),
		Warnings:         toolSidecarWarnings(outputObject),
		AuditEventIDs:    toolSidecarAuditEventIDs(outputObject),
		StructuredOutput: structuredOutput,
		Error:            toolSidecarError(result),
		Metadata:         metadata,
	}
}

func toolSidecarContract(tool string, profile string, metadata autonomy.ContractMetadata) ToolSidecarContract {
	policy := toolSchemaPolicyForTool(tool)
	return ToolSidecarContract{
		ID:                "idiom.tool.v1:" + tool,
		Kind:              "workspace_tool_result",
		Tool:              tool,
		Profile:           profile,
		SchemaVersion:     ToolSchemaExportVersion,
		Transport:         "cli",
		DecisionAuthority: policy.DecisionAuthority,
		Policy:            policy,
		Metadata:          metadata,
	}
}

func toolSidecarStructuredOutput(output string) any {
	trimmed := strings.TrimSpace(output)
	var decoded any
	if trimmed != "" {
		if err := json.Unmarshal([]byte(trimmed), &decoded); err == nil {
			return decoded
		}
	}
	return map[string]any{
		"format": "text",
		"text":   output,
	}
}

func toolSidecarOutputObject(structuredOutput any) map[string]any {
	object, ok := structuredOutput.(map[string]any)
	if !ok {
		return nil
	}
	if format, _ := object["format"].(string); format == "text" {
		return nil
	}
	return object
}

func toolSidecarFreshness(output map[string]any) any {
	if output == nil {
		return nil
	}
	for _, key := range []string{"freshness", "context_freshness"} {
		if value, ok := output[key]; ok {
			return value
		}
	}
	if contract, ok := output["contract"].(map[string]any); ok {
		if value, ok := contract["freshness"]; ok {
			return value
		}
	}
	return nil
}

func toolSidecarWarnings(output map[string]any) []ToolSidecarWarning {
	warnings := make([]ToolSidecarWarning, 0)
	if output == nil {
		return warnings
	}
	if rawWarnings, ok := output["warnings"].([]any); ok {
		for _, raw := range rawWarnings {
			switch value := raw.(type) {
			case string:
				if message := strings.TrimSpace(value); message != "" {
					warnings = append(warnings, ToolSidecarWarning{Code: "tool_warning", Message: message})
				}
			case map[string]any:
				message := firstNonEmptyString(stringFromAny(value["message"]), stringFromAny(value["summary"]), stringFromAny(value["reason"]))
				if message != "" {
					warnings = append(warnings, ToolSidecarWarning{
						Code:    firstNonEmptyString(stringFromAny(value["code"]), "tool_warning"),
						Message: message,
					})
				}
			}
		}
	}
	return warnings
}

func toolSidecarAuditEventIDs(output map[string]any) []string {
	seen := map[string]struct{}{}
	ids := make([]string, 0)
	var walk func(any)
	walk = func(value any) {
		if len(ids) >= 100 {
			return
		}
		switch typed := value.(type) {
		case map[string]any:
			for key, child := range typed {
				if key == "event_id" || key == "audit_event_id" {
					if id := stringFromAny(child); id != "" {
						if _, ok := seen[id]; !ok {
							seen[id] = struct{}{}
							ids = append(ids, id)
						}
					}
				}
				if key == "audit_event_ids" {
					appendAuditEventIDStrings(child, seen, &ids)
				}
				walk(child)
			}
		case []any:
			for _, child := range typed {
				walk(child)
			}
		}
	}
	walk(output)
	sort.Strings(ids)
	return ids
}

func toolSidecarError(result ToolResult) *ToolSidecarError {
	message := strings.TrimSpace(result.Error)
	if message == "" {
		return nil
	}
	code := classifyToolSidecarError(result)
	return &ToolSidecarError{
		Class:   code,
		Code:    code,
		Message: boundedToolSidecarErrorMessage(message),
	}
}

func appendAuditEventIDStrings(value any, seen map[string]struct{}, ids *[]string) {
	values, ok := value.([]any)
	if !ok {
		return
	}
	for _, raw := range values {
		if len(*ids) >= 100 {
			return
		}
		id := stringFromAny(raw)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		*ids = append(*ids, id)
	}
}

func boundedToolSidecarErrorMessage(message string) string {
	if len(message) <= maxToolSidecarErrorMessageBytes {
		return message
	}
	var builder strings.Builder
	for _, r := range message {
		if builder.Len()+len(string(r)) > maxToolSidecarErrorMessageBytes {
			break
		}
		builder.WriteRune(r)
	}
	return strings.TrimSpace(builder.String()) + "...(truncated)"
}

func classifyToolSidecarError(result ToolResult) string {
	text := strings.ToLower(strings.Join([]string{result.Summary, result.Error}, " "))
	switch {
	case strings.Contains(text, "unsupported tool") || strings.Contains(text, "unknown external tool") || strings.Contains(text, "unknown mcp tool"):
		return "unsupported_tool"
	case strings.Contains(text, "invalid input") || strings.Contains(text, " is required") || strings.Contains(text, "missing required"):
		return "invalid_input"
	case strings.Contains(text, "freshness_blocker") || strings.Contains(text, "stale context"):
		return "stale_context"
	case strings.Contains(text, "timed out") || strings.Contains(text, "timeout"):
		return "timeout"
	case strings.Contains(text, "permission") || strings.Contains(text, "read-only") || strings.Contains(text, "protected path"):
		return "permission_policy"
	case strings.Contains(text, "unavailable") || strings.Contains(text, "not configured") || strings.Contains(text, "not found"):
		return "backend_unavailable"
	case strings.Contains(text, "failed"):
		return "tool_failed"
	default:
		return "tool_error"
	}
}

func stringFromAny(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	default:
		return ""
	}
}
