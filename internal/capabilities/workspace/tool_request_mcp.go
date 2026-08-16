package workspace

import (
	"bytes"
	"encoding/json"
	"strings"
)

// NormalizeMCPToolRequestArgs fills Args for a textual or parser-normalized
// MCP request. MCP parameters are not limited to ToolRequest's built-in typed
// fields, so the argument object must be carried separately from those fields.
// Native tool calls already populate Args from the transport and are left
// unchanged.
func NormalizeMCPToolRequestArgs(req ToolRequest, payload []byte) ToolRequest {
	if !strings.HasPrefix(strings.TrimSpace(req.Tool), "mcp:") || len(req.Args) > 0 {
		return req
	}

	if args, ok := mcpArgumentObject(payload, req.Tool); ok {
		req.Args = args
		return req
	}

	// Textual key/value and function-call tokenizers have already reduced their
	// input to typed fields. Preserve those fields as the MCP argument object
	// when there is no raw JSON envelope to inspect.
	if args, ok := mcpArgumentObjectFromRequest(req); ok {
		req.Args = args
	} else {
		req.Args = json.RawMessage(`{}`)
	}

	return req
}

func mcpArgumentObject(payload []byte, tool string) (json.RawMessage, bool) {
	data := bytes.TrimSpace(payload)
	if len(data) == 0 {
		return nil, false
	}

	fields, ok := decodeMCPObject(data)
	if !ok {
		return nil, false
	}

	return mcpArgumentObjectFields(fields, tool)
}

func decodeMCPObject(data []byte) (map[string]json.RawMessage, bool) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err == nil && fields != nil {
		return fields, true
	}

	if repaired, ok := repairToolRequestJSONPayload(data); ok {
		if err := json.Unmarshal(repaired, &fields); err == nil && fields != nil {
			return fields, true
		}
	}

	return nil, false
}

func mcpArgumentObjectFields(fields map[string]json.RawMessage, tool string) (json.RawMessage, bool) {
	if rawTool, exists := fields["tool"]; exists {
		var explicit string
		if err := json.Unmarshal(rawTool, &explicit); err == nil && strings.TrimSpace(explicit) != "" && explicit != tool {
			return nil, false
		}
	}

	// Textual model protocols commonly wrap the actual parameters in request,
	// input, or arguments. Only unwrap an envelope-shaped object so a legitimate
	// MCP parameter named "input" or "request" is not discarded.
	for _, key := range []string{"request", "input", "arguments"} {
		if len(fields) != 2 {
			continue
		}

		raw, exists := fields[key]
		if !exists {
			continue
		}

		if nested, ok := decodeMCPNestedObject(raw); ok {
			return mcpArgumentObjectFields(nested, tool)
		}
	}

	delete(fields, "tool")
	args, err := json.Marshal(fields)
	if err != nil {
		return nil, false
	}

	return args, true
}

func decodeMCPNestedObject(raw json.RawMessage) (map[string]json.RawMessage, bool) {
	data := bytes.TrimSpace(raw)
	if len(data) == 0 {
		return nil, false
	}

	if data[0] == '"' {
		var encoded string
		if err := json.Unmarshal(data, &encoded); err != nil {
			return nil, false
		}

		data = bytes.TrimSpace([]byte(encoded))
	}

	return decodeMCPObject(data)
}

func mcpArgumentObjectFromRequest(req ToolRequest) (json.RawMessage, bool) {
	data, err := json.Marshal(req)
	if err != nil {
		return nil, false
	}

	return mcpArgumentObject(data, req.Tool)
}
