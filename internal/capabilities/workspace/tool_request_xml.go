package workspace

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var (
	qwenXMLFunctionPattern       = regexp.MustCompile(`(?s)^<function=([^>\s]+)>(.*)</function>$`)
	qwenXMLParameterPattern      = regexp.MustCompile(`(?s)<parameter=([^>\s]+)>(.*?)</parameter>`)
	deepSeekXMLInvokePattern     = regexp.MustCompile(`(?is)<[^>\s]*(?:invoke|function)\s+name\s*=\s*["']([^"']+)["'][^>]*>(.*?)</[^>]*(?:invoke|function)\s*>`)
	deepSeekXMLInvokeOpenPattern = regexp.MustCompile(`(?is)<[^>\s]*(?:invoke|function)\s+name\s*=\s*["']([^"']+)["'][^>]*>`)
	deepSeekXMLParameterPattern  = regexp.MustCompile(`(?is)<[^>\s]*(?:parameter)\s+name\s*=\s*["']([^"']+)["'][^>]*>(.*?)</[^>]*(?:parameter)\s*>`)
)

func LooksLikeXMLToolCallMarkup(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}

	return xmlToolMarkupLineContains(lower, "<tool_call") ||
		xmlToolMarkupLineContains(lower, "</tool_call>") ||
		xmlToolMarkupLineContains(lower, "<function=") ||
		xmlToolMarkupLineContains(lower, "</function>") ||
		xmlToolMarkupLineContains(lower, "<invoke") ||
		xmlToolMarkupLineContains(lower, "</invoke>") ||
		xmlToolMarkupLineContains(lower, "<janus_tool") ||
		xmlToolMarkupLineContains(lower, "</janus_tool") ||
		xmlToolMarkupLineContains(lower, "<janus_invoke") ||
		xmlToolMarkupLineContains(lower, "</janus_invoke") ||
		xmlToolMarkupLineContains(lower, "<n_tool") ||
		xmlToolMarkupLineContains(lower, "</n_tool") ||
		xmlToolMarkupLineContains(lower, "<n_invoke") ||
		xmlToolMarkupLineContains(lower, "</n_invoke") ||
		xmlToolMarkupLineContains(lower, "<n_parameter") ||
		xmlToolMarkupLineContains(lower, "</n_parameter") ||
		xmlToolMarkupTagLineContains(lower, "tool_calls") ||
		xmlToolMarkupTagLineContains(lower, "_invoke") ||
		xmlToolMarkupTagLineContains(lower, "dsml")
}

func xmlToolMarkupLineContains(lower string, marker string) bool {
	if strings.HasPrefix(lower, marker) || strings.Contains(lower, "\n"+marker) {
		return true
	}

	for _, line := range strings.Split(lower, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "<") && strings.Contains(line, marker) {
			return true
		}
	}

	return false
}

func xmlToolMarkupTagLineContains(lower string, marker string) bool {
	for _, line := range strings.Split(lower, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "<") && strings.Contains(line, marker) {
			return true
		}
	}

	return false
}

func extractQwenXMLToolPayload(text string) (string, bool, error) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || (!strings.Contains(trimmed, "<function=") && !strings.Contains(trimmed, "<tool_call>") && !strings.Contains(trimmed, "<tool_")) {
		return "", false, nil
	}

	if payload, ok, err := extractQwenXMLPrefixedToolPayload(trimmed); ok || err != nil {
		return payload, ok, err
	}

	envelope, ok := trimQwenXMLToolEnvelope(trimmed)
	if !ok {
		return "", false, nil
	}

	if strings.HasPrefix(strings.TrimSpace(envelope), "<function=") {
		return extractQwenXMLFunctionPayload(envelope)
	}

	if object, ok := extractFirstJSONObject(envelope); ok {
		payload, err := normalizeQwenJSONToolPayload(object)
		if err != nil {
			return object, true, err
		}

		return payload, true, nil
	}

	if payload, ok, err := extractQwenXMLNamedToolPayload(envelope); ok || err != nil {
		return payload, ok, err
	}

	return extractQwenXMLFunctionPayload(envelope)
}

func extractDeepSeekXMLToolPayload(text string) (string, bool, error) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return "", false, nil
	}

	if !LooksLikeXMLToolCallMarkup(trimmed) {
		return "", false, nil
	}

	match := deepSeekXMLInvokePattern.FindStringSubmatch(trimmed)
	if len(match) != 3 {
		if payload, ok, err := extractDeepSeekXMLOpenInvokePayload(trimmed); ok || err != nil {
			return payload, ok, err
		}

		return trimmed, true, fmt.Errorf("malformed DeepSeek XML tool request")
	}

	return deepSeekXMLPayloadFromParts(trimmed, match[1], match[2])
}

func extractDeepSeekXMLOpenInvokePayload(trimmed string) (string, bool, error) {
	indexes := deepSeekXMLInvokeOpenPattern.FindStringSubmatchIndex(trimmed)
	if len(indexes) < 4 {
		return "", false, nil
	}

	tool := trimmed[indexes[2]:indexes[3]]
	body := trimmed[indexes[1]:]
	if closeIndex := strings.Index(strings.ToLower(body), "</n_tool"); closeIndex >= 0 {
		body = body[:closeIndex]
	}
	if closeIndex := strings.Index(strings.ToLower(body), "</tool"); closeIndex >= 0 {
		body = body[:closeIndex]
	}

	return deepSeekXMLPayloadFromParts(trimmed, tool, body)
}

func deepSeekXMLPayloadFromParts(original string, toolName string, body string) (string, bool, error) {
	tool := strings.TrimSpace(toolName)
	if tool == "" || !xmlToolNameIsSafe(tool) {
		return original, true, fmt.Errorf("DeepSeek XML tool request is missing a safe tool name")
	}

	if !IsReadTool(tool) {
		return original, true, fmt.Errorf("DeepSeek XML tool request %q is not a read/search/discovery tool", tool)
	}

	fields := map[string]json.RawMessage{}
	toolRaw, _ := json.Marshal(tool)

	fields["tool"] = toolRaw

	for _, param := range deepSeekXMLParameterPattern.FindAllStringSubmatch(body, -1) {
		if len(param) != 3 {
			continue
		}

		name := strings.TrimSpace(param[1])
		if name == "" {
			continue
		}

		if !xmlToolNameIsSafe(name) {
			return original, true, fmt.Errorf("DeepSeek XML tool request has unsafe parameter name %q", name)
		}

		raw, err := qwenXMLParameterRaw(name, param[2])
		if err != nil {
			return original, true, err
		}

		fields[name] = raw
	}

	data, err := json.Marshal(fields)
	if err != nil {
		return original, true, err
	}

	return string(data), true, nil
}

func extractQwenXMLPrefixedToolPayload(text string) (string, bool, error) {
	text = strings.TrimSpace(text)

	start := strings.Index(text, "<tool_")

	for start >= 0 {
		tagStart := start + len("<tool_")
		tagEnd := strings.Index(text[tagStart:], ">")

		if tagEnd < 0 {
			return "", true, fmt.Errorf("malformed prefixed XML tool request")
		}

		tagEnd += tagStart

		tool := strings.TrimSpace(text[tagStart:tagEnd])

		if tool == "call" || tool == "code" || tool == "name" || !xmlToolNameIsSafe(tool) {
			next := strings.Index(text[tagEnd+1:], "<tool_")
			if next < 0 {
				return "", false, nil
			}

			start = tagEnd + 1 + next

			continue
		}

		closeTag := "</tool_" + tool + ">"
		closeStart := strings.Index(text[tagEnd+1:], closeTag)

		if closeStart < 0 {
			body := qwenXMLUnclosedPrefixedToolBody(text[tagEnd+1:])
			if payload, ok, err := extractQwenXMLPrefixedToolBodyPayload(tool, body); ok || err != nil {
				return payload, ok, err
			}

			return "", false, nil
		}

		closeStart += tagEnd + 1

		body := text[tagEnd+1 : closeStart]

		if payload, ok, err := extractQwenXMLPrefixedToolBodyPayload(tool, body); ok || err != nil {
			return payload, ok, err
		}

		if !strings.Contains(body, "<parameter=") {
			next := strings.Index(text[closeStart+len(closeTag):], "<tool_")
			if next < 0 {
				return "", false, nil
			}

			start = closeStart + len(closeTag) + next

			continue
		}

		return "", true, fmt.Errorf("prefixed XML tool request %q is missing close tag", tool)
	}

	return "", false, nil
}

func extractQwenXMLPrefixedToolBodyPayload(tool string, body string) (string, bool, error) {
	if object, ok := extractFirstJSONObject(body); ok {
		// Only treat the extracted JSON as a tool call if it contains
		// fields that match Qwen's actual tool-call schema ("name" or
		// "arguments"). Without this guard, any JSON inside <tool_NAME>
		// tags — including descriptive prose — is misclassified as a
		// tool invocation, causing false-positive parse rejections.
		if !qwenXMLObjectLooksLikeToolCall(object) {
			return "", false, nil
		}

		payload, err := normalizeExplicitToolJSONObjectPayload("prefixed XML", tool, object)
		if err != nil {
			return object, true, err
		}

		return payload, true, nil
	}

	if strings.Contains(body, "<parameter=") {
		payload, ok, err := extractQwenXMLFunctionPayload("<function=" + tool + ">" + body + "</function>")
		if ok && err == nil {
			return payload, true, nil
		}

		return "", true, fmt.Errorf("prefixed XML tool request %q is missing close tag", tool)
	}

	return "", false, nil
}

func qwenXMLUnclosedPrefixedToolBody(body string) string {
	body = strings.TrimSpace(body)
	for _, marker := range []string{"</tool_call>", "<tool_"} {
		if idx := strings.Index(body, marker); idx >= 0 {
			body = strings.TrimSpace(body[:idx])
		}
	}

	return body
}

// qwenXMLObjectLooksLikeToolCall checks whether a JSON object extracted
// from inside <tool_NAME> tags looks like a genuine Qwen tool call rather
// than descriptive prose. It accepts objects that contain recognized
// tool-parameter field names (drawn from the ToolRequest struct) or the
// standard Qwen call-envelope fields ("tool", "name", "arguments").
//
// This prevents false positives where Qwen writes prose like
//
//	<tool_note>{"key":"evidence","content":"found X"}</tool_note>
//
// where the JSON describes what a tool does rather than invoking one,
// while still accepting legitimate prefixed Qwen tool calls like
//
//	<tool_code_navigate>{"path":"foo.go","capability":"read_symbol","symbol":"Bar"}</tool_code_navigate>
//
// where the JSON carries tool parameters even though the tool name
// comes from the XML tag.
func qwenXMLObjectLooksLikeToolCall(object string) bool {
	if strings.TrimSpace(object) == "" {
		return false
	}

	var fields map[string]json.RawMessage

	if err := json.Unmarshal([]byte(object), &fields); err != nil {
		return false
	}

	// Standard Qwen call-envelope fields.
	if _, ok := fields["name"]; ok {
		return true
	}

	if _, ok := fields["arguments"]; ok {
		return true
	}

	if _, ok := fields["tool"]; ok {
		return true
	}

	// Recognized tool-parameter fields from the ToolRequest struct.
	// These strongly indicate the JSON is tool arguments, not prose.
	for _, param := range qwenXMLToolParameterFields {
		if _, ok := fields[param]; ok {
			return true
		}
	}

	return false
}

// qwenXMLToolParameterFields lists ToolRequest JSON field names that
// strongly indicate a tool invocation when found inside <tool_NAME>
// tags. Fields that are common in prose (e.g. "content", "description",
// "summary", "name" without arguments) are intentionally excluded.
var qwenXMLToolParameterFields = []string{
	"path", "pattern", "symbol", "capability", "command", "prompt",
	"task", "query", "rationale", "edits", "old_text", "new_text",
	"agent", "language", "start_line", "end_line", "replace_all",
	"recursive", "resolution", "glob",
	"include_evidence", "include_source", "include_files", "refresh",
	"category", "component", "expected", "observed", "workaround",
	"impact", "evidence", "suggested_fix", "required_tests",
	"required_edit_paths", "forbidden_paths", "destination",
	"declarations", "moves", "todos", "items", "named_gap",
	"why_not_answering", "budget_tokens", "limit", "max_depth",
	"mode", "freshness_policy", "freshness_max_files",
}

func xmlToolNameIsSafe(tool string) bool {
	if strings.TrimSpace(tool) == "" {
		return false
	}

	for _, r := range tool {
		if r == '_' || r == '-' || r == '.' || ('a' <= r && r <= 'z') || ('A' <= r && r <= 'Z') || ('0' <= r && r <= '9') {
			continue
		}

		return false
	}

	return true
}

func extractQwenXMLNamedToolPayload(envelope string) (string, bool, error) {
	tool, ok := extractQwenXMLTagValue(envelope, "tool_name")
	if !ok {
		tool, ok = extractQwenXMLTagValue(envelope, "name")
	}

	tool = strings.TrimSpace(tool)
	if !ok || tool == "" {
		return "", false, nil
	}

	arguments, ok := extractQwenXMLTagValue(envelope, "arguments")
	if !ok {
		arguments, ok = extractQwenXMLTagValue(envelope, "args")
	}

	if !ok {
		return envelope, true, fmt.Errorf("qwen XML named tool request is missing arguments")
	}

	var args map[string]json.RawMessage

	arguments = strings.TrimSpace(arguments)

	if arguments == "" {
		args = map[string]json.RawMessage{}
	} else if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		if escaped, repaired := repairStringifiedJSONControlChars(arguments); repaired {
			if repairErr := json.Unmarshal(escaped, &args); repairErr == nil {
				err = nil
			}
		}

		if err != nil {
			return envelope, true, fmt.Errorf("qwen XML named tool arguments must decode to an object: %w", err)
		}
	}

	fields := map[string]json.RawMessage{}
	toolRaw, _ := json.Marshal(tool)

	fields["tool"] = toolRaw

	for key, value := range args {
		if strings.TrimSpace(key) == "" || key == "tool" {
			continue
		}

		fields[key] = value
	}

	data, err := json.Marshal(fields)
	if err != nil {
		return envelope, true, err
	}

	return string(data), true, nil
}

func extractQwenXMLTagValue(envelope string, tag string) (string, bool) {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return "", false
	}

	open := "<" + tag + ">"
	close := "</" + tag + ">"
	start := strings.Index(envelope, open)

	if start < 0 {
		return extractQwenXMLSplitTagValue(envelope, tag)
	}

	start += len(open)

	end := strings.Index(envelope[start:], close)

	if end < 0 {
		return extractQwenXMLSplitTagValue(envelope, tag)
	}

	return envelope[start : start+end], true
}

func extractQwenXMLSplitTagValue(envelope string, tag string) (string, bool) {
	pattern := regexp.MustCompile(`(?s)<\s*` + regexp.QuoteMeta(tag) + `\s*>(.*?)<\s*/\s*` + regexp.QuoteMeta(tag) + `\s*>`)
	match := pattern.FindStringSubmatch(envelope)

	if len(match) != 2 {
		return "", false
	}

	return match[1], true
}

func extractQwenXMLFunctionPayload(envelope string) (string, bool, error) {
	envelope = strings.TrimSpace(envelope)

	match := qwenXMLFunctionPattern.FindStringSubmatch(envelope)

	if len(match) != 3 {
		return envelope, true, fmt.Errorf("malformed qwen XML tool request")
	}

	tool := strings.TrimSpace(match[1])
	if tool == "" {
		return envelope, true, fmt.Errorf("qwen XML tool request is missing function name")
	}

	fields := map[string]json.RawMessage{}
	toolRaw, _ := json.Marshal(tool)

	fields["tool"] = toolRaw

	for _, param := range qwenXMLParameterPattern.FindAllStringSubmatch(match[2], -1) {
		if len(param) != 3 {
			continue
		}

		name := strings.TrimSpace(param[1])
		if name == "" {
			continue
		}

		raw, err := qwenXMLParameterRaw(name, param[2])
		if err != nil {
			return envelope, true, err
		}

		fields[name] = raw
	}

	data, err := json.Marshal(fields)
	if err != nil {
		return envelope, true, err
	}

	return string(data), true, nil
}

func normalizeQwenJSONToolPayload(payload string) (string, error) {
	var fields map[string]json.RawMessage

	if err := json.Unmarshal([]byte(payload), &fields); err != nil {
		return payload, err
	}

	if _, ok := fields["tool"]; ok {
		return payload, nil
	}

	name := qwenJSONToolName(fields)
	if name == "" {
		return payload, nil
	}

	normalized := map[string]json.RawMessage{}
	toolRaw, _ := json.Marshal(name)

	normalized["tool"] = toolRaw

	if args, ok, err := qwenJSONToolArguments(fields); err != nil {
		return payload, err
	} else if ok {
		for key, value := range args {
			if strings.TrimSpace(key) == "" || key == "tool" {
				continue
			}

			normalized[key] = value
		}
	} else {
		for key, value := range fields {
			switch key {
			case "name", "function", "tool", "arguments", "args":
				continue
			default:
				normalized[key] = value
			}
		}
	}

	data, err := json.Marshal(normalized)
	if err != nil {
		return payload, err
	}

	return string(data), nil
}

func qwenJSONToolName(fields map[string]json.RawMessage) string {
	for _, key := range []string{"name", "function"} {
		raw, ok := fields[key]
		if !ok {
			continue
		}

		var name string

		if err := json.Unmarshal(raw, &name); err == nil {
			if name = strings.TrimSpace(name); name != "" {
				return name
			}
		}
	}

	return ""
}

func qwenJSONToolArguments(fields map[string]json.RawMessage) (map[string]json.RawMessage, bool, error) {
	for _, key := range []string{"arguments", "args"} {
		raw, ok := fields[key]
		if !ok {
			continue
		}

		var args map[string]json.RawMessage

		if err := json.Unmarshal(raw, &args); err == nil {
			return args, true, nil
		}

		var encoded string

		if err := json.Unmarshal(raw, &encoded); err != nil {
			return nil, true, fmt.Errorf("qwen JSON tool arguments must be an object or encoded JSON object")
		}

		encoded = strings.TrimSpace(encoded)
		if encoded == "" {
			return map[string]json.RawMessage{}, true, nil
		}

		if err := json.Unmarshal([]byte(encoded), &args); err != nil {
			if escaped, ok := repairStringifiedJSONControlChars(encoded); ok {
				if err := json.Unmarshal(escaped, &args); err == nil {
					return args, true, nil
				}
			}

			return nil, true, fmt.Errorf("qwen JSON tool arguments must decode to an object: %w", err)
		}

		return args, true, nil
	}

	return nil, false, nil
}

func trimQwenXMLToolEnvelope(text string) (string, bool) {
	text = strings.TrimSpace(text)
	if idx := strings.Index(text, "<tool_call>"); idx >= 0 {
		text = strings.TrimSpace(text[idx+len("<tool_call>"):])
		if end := strings.Index(text, "</tool_call>"); end >= 0 {
			text = strings.TrimSpace(text[:end])
		}

		return text, true
	}

	if envelope, ok := extractQwenXMLTagValue(text, "tool_call"); ok {
		return strings.TrimSpace(envelope), true
	}

	if strings.HasPrefix(text, "<function=") {
		return text, true
	}

	return "", false
}

func qwenXMLParameterRaw(name string, value string) (json.RawMessage, error) {
	value = trimQwenXMLParameterValue(value)

	trimmed := strings.TrimSpace(value)

	switch strings.TrimSpace(name) {
	case "edits", "evidence", "required_tests", "required_edit_paths", "forbidden_paths", "declarations", "moves", "todos", "items":
		if trimmed == "" {
			return json.RawMessage("[]"), nil
		}

		if !json.Valid([]byte(trimmed)) {
			if escaped, ok := repairStringifiedJSONControlChars(trimmed); ok {
				return escaped, nil
			}

			return nil, fmt.Errorf("qwen XML parameter %q must be valid JSON array/object content", name)
		}

		return json.RawMessage(trimmed), nil
	case "plan_artifact":
		if trimmed == "" {
			return json.RawMessage("null"), nil
		}

		if !json.Valid([]byte(trimmed)) {
			if escaped, ok := repairStringifiedJSONControlChars(trimmed); ok {
				return escaped, nil
			}

			return nil, fmt.Errorf("qwen XML parameter %q must be valid JSON object content", name)
		}

		return json.RawMessage(trimmed), nil
	case "replace_all", "recursive", "fixed_strings", "include_evidence", "include_source", "include_files", "refresh":
		parsed, err := strconv.ParseBool(strings.ToLower(trimmed))
		if err != nil {
			return nil, fmt.Errorf("qwen XML parameter %q must be boolean: %w", name, err)
		}

		if parsed {
			return json.RawMessage("true"), nil
		}

		return json.RawMessage("false"), nil
	case "start_line", "end_line", "max_depth", "limit", "budget_tokens", "freshness_max_files":
		parsed, err := strconv.Atoi(trimmed)
		if err != nil {
			return nil, fmt.Errorf("qwen XML parameter %q must be integer: %w", name, err)
		}

		return json.RawMessage(strconv.Itoa(parsed)), nil
	default:
		raw, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}

		return raw, nil
	}
}

func trimQwenXMLParameterValue(value string) string {
	value = strings.TrimPrefix(value, "\n")
	value = strings.TrimSuffix(value, "\n")

	return value
}
