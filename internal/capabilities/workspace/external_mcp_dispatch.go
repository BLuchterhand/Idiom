package workspace

import (
	"context"
	"encoding/json"
	"strings"

	"idiom/internal/platform/failures"
)

// executeExternal dispatches a project-defined external tool registered via
// SetExternalToolsRegistry.
func (m *Manager) executeExternal(ctx context.Context, req ToolRequest) ToolResult {
	registry := m.toolSurface.ExternalToolsRegistry()
	if registry == nil {
		return toolUnavailableResult(req.Tool, "External tools unavailable", "external tools registry not configured")
	}

	if !registry.Has(req.Tool) {
		return toolUnsupportedResult(req.Tool, "Unknown external tool", "external tool not registered: "+req.Tool)
	}

	if ok, reason := registry.Availability(req.Tool, m.externalToolBaseDir()); !ok {
		return toolUnavailableResult(req.Tool, "External tool unavailable", "external tool "+req.Tool+" unavailable: "+reason)
	}

	args := req.Args
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}

	output, truncated, err := registry.Invoke(ctx, req.Tool, args, m.cfg.CWD, m.cfg.MaxOutputBytes)
	out := ToolResult{Tool: req.Tool, Summary: "External tool " + req.Tool, Output: output}
	if truncated {
		out.Summary += " (output truncated)"
	}

	if err != nil {
		out.Error = err.Error()
		out.Failure = failures.Wrap(failures.Internal, reasonToolInternalFailure, "external tool failed", err)

		if output == "" {
			out.Output = ""
		}
	}

	return out
}

// executeMCP dispatches a `mcp:<server>/<tool>` call through the attached
// registry. Args are passed verbatim so upstream JSON-Schema parameters survive
// round-trip without forced typing through ToolRequest fields.
func (m *Manager) executeMCP(ctx context.Context, req ToolRequest) ToolResult {
	registry := m.toolSurface.MCPRegistry()
	if registry == nil {
		return toolUnavailableResult(req.Tool, "MCP unavailable", "mcp registry not configured")
	}

	if !registry.Has(req.Tool) {
		return toolUnsupportedResult(req.Tool, "Unknown MCP tool", "mcp tool not registered: "+req.Tool)
	}

	args := req.Args
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}

	result, err := registry.Call(ctx, req.Tool, args)
	if err != nil {
		return toolFailureFromMaybeTypedErrorResult(req.Tool, "MCP call failed", err, failures.Internal, reasonToolInternalFailure)
	}

	output := joinMCPContent(result.Content)
	summary := "MCP " + req.Tool
	out := ToolResult{Tool: req.Tool, Summary: summary, Output: output}

	if result.IsError {
		out.Error = output
		out.Output = ""
		out.Summary = "MCP tool reported an error"
	}

	return out
}

func joinMCPContent(parts []json.RawMessage) string {
	if len(parts) == 0 {
		return ""
	}

	var b strings.Builder

	for i, raw := range parts {
		if i > 0 {
			b.WriteByte('\n')
		}

		var probe struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}

		if err := json.Unmarshal(raw, &probe); err == nil && probe.Type == "text" {
			b.WriteString(probe.Text)

			continue
		}

		b.Write(raw)
	}

	return b.String()
}
