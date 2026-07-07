package workspace

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
)

var (
	lineAnchoredToolJSONPattern = regexp.MustCompile(`(?m)(^|\n)\s*tool\s*:\s*([A-Za-z_][A-Za-z0-9_.-]*)\s*(\{)`)
	knownBareJSONToolNamesOnce  sync.Once
	knownBareJSONToolNames      map[string]struct{}
)

func extractTaggedJSONObjectToolPayload(text string) (string, bool) {
	for _, tag := range []string{"tool", "tool_code"} {
		open := "<" + tag + ">"
		close := "</" + tag + ">"
		start := strings.Index(text, open)

		if start < 0 {
			continue
		}

		start += len(open)

		end := strings.Index(text[start:], close)

		if end >= 0 {
			end += start
		} else {
			end = len(text)
		}

		if object, ok := extractFirstJSONObject(text[start:end]); ok {
			return object, true
		}
	}

	return "", false
}

// DecodeToolRequestJSON decodes a built-in tool request payload. It accepts a
// small, safe repair surface for common model JSON-schema slips: typed fields
// emitted as decimal/boolean strings, stringified non-plan structured fields,
// and singleton values for array-only fields. Native tool-calls and fenced JSON
// requests both route through this helper so repair behavior stays consistent.
func extractToolPayload(text string) (string, bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return "", false
	}

	if strings.HasPrefix(trimmed, "```tool") {
		start := strings.Index(trimmed, "\n")
		if start == -1 {
			return "", false
		}

		rest := trimmed[start+1:]
		end := strings.Index(rest, "\n```")

		if end == -1 {
			return "", false
		}

		payload := strings.TrimSpace(rest[:end])
		if object, ok := extractFirstJSONObject(payload); ok {
			return object, true
		}

		return payload, true
	}

	start := strings.Index(trimmed, "```tool")
	if start >= 0 {
		return extractToolPayload(trimmed[start:])
	}

	if payload, ok := extractPlainToolPrefixPayload(trimmed); ok {
		return payload, true
	}

	if payload, ok := extractCallingToolPayload(trimmed); ok {
		return payload, true
	}

	if strings.HasPrefix(trimmed, "{") {
		if object, ok := extractFirstJSONObject(trimmed); ok {
			if bareJSONObjectIsToolPayload(object) {
				return object, true
			}

			return "", false
		}

		if strings.Contains(trimmed, `"tool"`) {
			return trimmed, true
		}
	}

	return "", false
}

func bareJSONObjectIsToolPayload(payload string) bool {
	data := []byte(payload)
	if !json.Valid(data) {
		if repaired, ok := repairRawJSONControlChars(data); ok {
			data = repaired
		}
	}

	var fields map[string]json.RawMessage

	if err := json.Unmarshal(data, &fields); err != nil {
		return false
	}

	if _, ok := fields["tool"]; ok {
		return true
	}

	if _, ok := fields["capability"]; ok {
		return false
	}

	var req ToolRequest

	if err := json.Unmarshal(data, &req); err != nil {
		return false
	}

	return bareJSONMissingToolPayloadIsReadOnly(req)
}

func bareJSONMissingToolPayloadIsReadOnly(req ToolRequest) bool {
	if strings.TrimSpace(req.Tool) != "" {
		return true
	}

	if len(req.Todos) > 0 || len(req.Items) > 0 {
		return false
	}

	if strings.TrimSpace(req.Command) != "" ||
		strings.TrimSpace(req.Prompt) != "" ||
		strings.TrimSpace(req.Agent) != "" ||
		strings.TrimSpace(req.Destination) != "" ||
		len(req.Declarations) > 0 ||
		len(req.Moves) > 0 ||
		strings.HasPrefix(strings.TrimSpace(req.Pattern), "tag:") ||
		strings.TrimSpace(req.OldText) != "" ||
		strings.TrimSpace(req.NewText) != "" ||
		strings.TrimSpace(req.Content) != "" ||
		len(req.Edits) > 0 ||
		req.PlanArtifact != nil ||
		strings.TrimSpace(req.Rationale) != "" {

		return false
	}

	if strings.TrimSpace(req.Glob) != "" && strings.TrimSpace(req.Pattern) != "" {
		return true
	}

	return req.StartLine != 0 || req.EndLine != 0 || looksLikeFileHandleID(req.ID)
}

func looksLikeFileHandleID(id string) bool {
	id = strings.TrimSpace(id)
	if len(id) < 2 || id[0] != 'f' {
		return false
	}

	for _, r := range id[1:] {
		if r < '0' || r > '9' {
			return false
		}
	}

	return true
}

// extractCallingToolPayload detects the [Calling tool: name({json})]
// format that some models emit in textual-transport mode. The JSON object
// inside the envelope already contains a "tool" field — the extractor
// just needs to unwrap the outer brackets and return the JSON payload
// for standard DecodeToolRequestJSON decoding.
// Uses extractFirstJSONObject for bracket-depth-aware, string-escape-aware
// extraction, consistent with the other extractors in the chain.
func extractCallingToolPayload(text string) (string, bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return "", false
	}

	// Match case-insensitive [Calling tool: prefix.
	lower := strings.ToLower(trimmed)

	const prefix = "[calling tool:"

	idx := strings.Index(lower, prefix)

	if idx < 0 {
		return "", false
	}

	// Skip past the prefix. The format is: TOOL_NAME(JSON_OBJECT)].
	rest := strings.TrimSpace(trimmed[idx+len(prefix):])
	// Find the first '{' to locate the JSON payload, then delegate to
	// extractFirstJSONObject for bracket-depth-aware, string-escape-aware
	// extraction.
	braceIdx := strings.Index(rest, "{")
	if braceIdx < 0 {
		return "", false
	}

	payload, ok := extractFirstJSONObject(rest[braceIdx:])
	if !ok {
		return "", false
	}

	// Must contain "tool" key to distinguish from prose JSON.
	if !strings.Contains(payload, `"tool"`) {
		return "", false
	}

	return payload, true
}

func extractPlainToolPrefixPayload(text string) (string, bool) {
	const prefix = "tool"

	if len(text) <= len(prefix) || !strings.HasPrefix(text, prefix) {
		return "", false
	}

	rest := text[len(prefix):]
	if rest == "" {
		return "", false
	}

	first := rest[0]
	if first != '{' && first != '\n' && first != '\r' && first != '\t' && first != ' ' && first != ':' {
		return "", false
	}

	rest = strings.TrimLeft(rest, " \t\r\n:")
	if !strings.HasPrefix(rest, "{") || !strings.Contains(rest, `"tool"`) {
		return "", false
	}

	object, ok := extractFirstJSONObject(rest)

	return object, ok
}

func extractLineAnchoredToolJSONObjectPayload(text string) (string, bool, error) {
	match := lineAnchoredToolJSONPattern.FindStringSubmatchIndex(text)
	if len(match) < 8 {
		return "", false, nil
	}

	tool := strings.TrimSpace(text[match[4]:match[5]])
	if tool == "" {
		return "", true, fmt.Errorf("line-anchored tool request is missing tool name")
	}

	objectStart := match[6]
	object, ok := extractFirstJSONObject(text[objectStart:])

	if !ok {
		return "", true, fmt.Errorf("line-anchored tool request is missing JSON arguments object")
	}

	payload, err := normalizeExplicitToolJSONObjectPayload("line-anchored", tool, object)
	if err != nil {
		return object, true, err
	}

	return payload, true, nil
}

func extractBareToolJSONObjectPayload(text string) (string, bool, error) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || strings.HasPrefix(trimmed, "{") {
		return "", false, nil
	}

	tool, rest, ok := splitBareToolJSONObjectPrefix(trimmed)
	if !ok || !knownBareToolJSONObjectName(tool) {
		return "", false, nil
	}

	object, ok := extractFirstJSONObject(rest)
	if !ok {
		return "", true, fmt.Errorf("bare tool request %q is missing JSON arguments object", tool)
	}

	payload, err := normalizeExplicitToolJSONObjectPayload("bare", tool, object)
	if err != nil {
		return object, true, err
	}

	return payload, true, nil
}

func splitBareToolJSONObjectPrefix(text string) (string, string, bool) {
	toolEnd := -1

	for i, r := range text {
		if r == '_' || r == '.' || r == '-' || ('a' <= r && r <= 'z') || ('A' <= r && r <= 'Z') || (i > 0 && '0' <= r && r <= '9') {
			continue
		}

		toolEnd = i

		break
	}

	if toolEnd <= 0 {
		return "", "", false
	}

	tool := strings.TrimSpace(text[:toolEnd])
	rest := strings.TrimSpace(text[toolEnd:])

	if tool == "" || rest == "" || !strings.HasPrefix(rest, "{") {
		return "", "", false
	}

	return tool, rest, true
}

func knownBareToolJSONObjectName(tool string) bool {
	tool = strings.TrimSpace(tool)
	if tool == "" {
		return false
	}

	knownBareJSONToolNamesOnce.Do(func() {
		knownBareJSONToolNames = make(map[string]struct{})

		for _, name := range toolManifestToolsAcrossBuiltInSurfaces() {
			if name = strings.TrimSpace(name); name != "" {
				knownBareJSONToolNames[name] = struct{}{}
			}
		}
	})

	_, ok := knownBareJSONToolNames[tool]

	return ok
}

func normalizeExplicitToolJSONObjectPayload(source string, tool string, object string) (string, error) {
	var fields map[string]json.RawMessage

	if err := json.Unmarshal([]byte(object), &fields); err != nil {
		return "", err
	}

	if rawTool, exists := fields["tool"]; exists {
		var explicit string

		if err := json.Unmarshal(rawTool, &explicit); err != nil {
			return "", fmt.Errorf("%s tool request has non-string tool field", source)
		}

		if explicit = strings.TrimSpace(explicit); explicit != "" && explicit != tool {
			return "", fmt.Errorf("%s tool name %q does not match JSON tool %q", source, tool, explicit)
		}
	}

	toolRaw, _ := json.Marshal(tool)

	fields["tool"] = toolRaw

	payload, err := json.Marshal(fields)
	if err != nil {
		return "", err
	}

	return string(payload), nil
}

func extractFirstJSONObject(text string) (string, bool) {
	text = strings.TrimSpace(text)
	if text == "" || text[0] != '{' {
		return "", false
	}

	depth := 0
	inString := false
	escaped := false

	for i, r := range text {
		switch {
		case escaped:
			escaped = false
		case r == '\\' && inString:
			escaped = true
		case r == '"':
			inString = !inString
		case !inString && r == '{':
			depth++
		case !inString && r == '}':
			depth--
			if depth == 0 {
				return strings.TrimSpace(text[:i+1]), true
			}
		}
	}

	return "", false
}
