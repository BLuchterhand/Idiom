package agent

import (
	"encoding/json"
	"strconv"
	"strings"

	"idiom/internal/llama"
	"idiom/internal/util"
	"idiom/internal/workspace"
)

const (
	toolRequestNormalizerOutcomeNormalized          = "normalized"
	toolRequestNormalizerOutcomeMissingToolInferred = "missing_tool_inferred"
	toolRequestNormalizerOutcomeUnsupportedShape    = "unsupported_shape"
	toolRequestNormalizerOutcomeUnsafeToInfer       = "unsafe_to_infer"
	toolRequestNormalizerOutcomeRequiresTerminal    = "requires_terminal_action"

	toolRequestNormalizerTransportNative                  = "native"
	toolRequestNormalizerTransportTextual                 = "textual"
	toolRequestNormalizerTransportFencedJSON              = "fenced_json"
	toolRequestNormalizerTransportKeyValue                = "key_value"
	toolRequestNormalizerTransportKeyValueArgs            = "key_value_args"
	toolRequestNormalizerTransportFunctionCall            = "function_call"
	toolRequestNormalizerTransportNestedArguments         = "nested_arguments"
	toolRequestNormalizerTransportIdiomCLI                = "idiom_cli"
	toolRequestNormalizerTransportStringifiedSubmitRepair = "stringified_submit_plan_repair"
)

type toolRequestSyntaxNormalization struct {
	Request            workspace.ToolRequest
	Raw                string
	OK                 bool
	ParseErr           error
	EventOutcome       string
	EventTransport     string
	ParserFailureClass string
	ActiveToolCall     *llama.ToolCall
	Repaired           bool
}

func normalizeToolRequestSyntax(message llama.ChatMessage) toolRequestSyntaxNormalization {
	if len(message.ToolCalls) > 0 {
		call := message.ToolCalls[0]
		req, ok, parseErr := parseToolCall(call)
		result := toolRequestSyntaxNormalization{
			Request:        req,
			Raw:            call.Function.Arguments,
			OK:             ok,
			ParseErr:       parseErr,
			ActiveToolCall: &call,
		}
		if parseErr != nil {
			repaired := repairToolRequestSyntax(result)
			if repaired.Repaired {
				return repaired
			}
			result.EventOutcome = toolRequestNormalizerOutcomeUnsupportedShape
			result.EventTransport = toolRequestNormalizerTransportNative
			result.ParserFailureClass = toolRequestParserFailureClass(result.Raw, parseErr)
			return result
		}
		if repaired := repairToolRequestSyntax(result); repaired.Repaired {
			return repaired
		}
		if ok {
			normalizeParsedToolRequestSyntax(&result)
			result.EventOutcome = toolRequestNormalizerOutcomeNormalized
			result.EventTransport = toolRequestNormalizerTransportNative
			return result
		}
		result.EventOutcome = toolRequestNormalizerOutcomeUnsupportedShape
		result.EventTransport = toolRequestNormalizerTransportNative
		return result
	}

	req, raw, ok, parseErr := workspace.ParseToolRequest(message.Content)
	result := toolRequestSyntaxNormalization{
		Request:  req,
		Raw:      raw,
		OK:       ok,
		ParseErr: parseErr,
	}
	if parseErr != nil {
		repaired := repairToolRequestSyntax(result)
		if repaired.Repaired {
			return repaired
		}
		result.EventOutcome = toolRequestNormalizerOutcomeUnsupportedShape
		result.EventTransport = toolRequestNormalizerTransportTextual
		result.ParserFailureClass = toolRequestParserFailureClass(result.Raw, parseErr)
		return result
	}
	if repaired := repairToolRequestSyntax(result); repaired.Repaired {
		return repaired
	}
	if ok {
		normalizeParsedToolRequestSyntax(&result)
		result.EventOutcome = toolRequestNormalizerOutcomeNormalized
		if toolRequestPayloadMissingExplicitTool(raw) && strings.TrimSpace(req.Tool) != "" {
			if missingToolInferenceNeedsTypedAuthority(req.Tool) {
				result.Request = workspace.ToolRequest{}
				result.OK = false
				result.EventOutcome = toolRequestNormalizerOutcomeUnsafeToInfer
				result.EventTransport = toolRequestNormalizerTransportTextual
				return result
			}
			result.EventOutcome = toolRequestNormalizerOutcomeMissingToolInferred
		}
		result.EventTransport = toolRequestNormalizerTransportTextual
		return result
	}
	if nested := normalizeNestedArgumentsToolRequestSyntax(message.Content); nested.EventOutcome != "" {
		return nested
	}
	if fenced := normalizeFencedJSONToolRequestSyntax(message.Content); fenced.EventOutcome != "" {
		return fenced
	}
	if idiomCLI := normalizeIdiomCLIToolRequestSyntax(message.Content); idiomCLI.EventOutcome != "" {
		return idiomCLI
	}
	if functionCall := normalizeFunctionCallToolRequestSyntax(message.Content); functionCall.EventOutcome != "" {
		return functionCall
	}
	if argsBlock := normalizeKeyValueArgsToolRequestSyntax(message.Content); argsBlock.EventOutcome != "" {
		return argsBlock
	}
	if keyValue := normalizeKeyValueToolRequestSyntax(message.Content); keyValue.EventOutcome != "" {
		return keyValue
	}
	if outcome := classifyUnsupportedTextualToolShape(message.Content); outcome != "" {
		result.EventOutcome = outcome
		result.EventTransport = toolRequestNormalizerTransportTextual
	}
	return result
}

func normalizeNestedArgumentsToolRequestSyntax(text string) toolRequestSyntaxNormalization {
	req, raw, ok := repairNestedArgumentsToolRequest(text)
	if !ok {
		return toolRequestSyntaxNormalization{}
	}
	result := toolRequestSyntaxNormalization{
		Request:        req,
		Raw:            raw,
		OK:             true,
		EventOutcome:   toolRequestNormalizerOutcomeNormalized,
		EventTransport: toolRequestNormalizerTransportNestedArguments,
		Repaired:       true,
	}
	normalizeParsedToolRequestSyntax(&result)
	return result
}

func repairToolRequestSyntax(result toolRequestSyntaxNormalization) toolRequestSyntaxNormalization {
	if repairedReq, repairedRaw, repaired := repairNestedArgumentsToolRequest(result.Raw); repaired {
		result.Request = repairedReq
		result.Raw = repairedRaw
		result.OK = true
		result.ParseErr = nil
		result.EventOutcome = toolRequestNormalizerOutcomeNormalized
		result.EventTransport = toolRequestNormalizerTransportNestedArguments
		result.ParserFailureClass = ""
		result.Repaired = true
		normalizeParsedToolRequestSyntax(&result)
		return result
	}
	repairedReq, repairedRaw, repaired := repairStringifiedSubmitPlanEditsToolRequest(result.Raw, result.ActiveToolCall)
	if !repaired {
		return result
	}
	result.Request = repairedReq
	result.Raw = repairedRaw
	result.OK = true
	result.ParseErr = nil
	result.EventOutcome = toolRequestNormalizerOutcomeNormalized
	result.EventTransport = toolRequestNormalizerTransportStringifiedSubmitRepair
	result.ParserFailureClass = ""
	result.Repaired = true
	normalizeParsedToolRequestSyntax(&result)
	return result
}

func repairNestedArgumentsToolRequest(raw string) (workspace.ToolRequest, string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || !strings.HasPrefix(raw, "{") {
		return workspace.ToolRequest{}, "", false
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &obj); err != nil {
		return workspace.ToolRequest{}, "", false
	}
	if len(obj) != 1 {
		return workspace.ToolRequest{}, "", false
	}
	args, ok := obj["arguments"]
	if !ok || !json.Valid(args) {
		return workspace.ToolRequest{}, "", false
	}
	req, err := workspace.DecodeToolRequestJSON(args)
	if err != nil || strings.TrimSpace(req.Tool) == "" {
		return workspace.ToolRequest{}, "", false
	}
	return req, string(args), true
}

func toolRequestParserFailureClass(raw string, parseErr error) string {
	if parseErr == nil {
		return ""
	}
	lower := strings.ToLower(parseErr.Error())
	combined := lower + "\n" + strings.ToLower(raw)
	switch {
	case strings.Contains(combined, "toolrequest.edits") && strings.Contains(combined, "string"):
		return "submit_plan_edits_string"
	case strings.Contains(lower, "unexpected end of json") ||
		strings.Contains(lower, "invalid character") ||
		strings.Contains(lower, "syntax error"):
		return "malformed_json"
	case strings.Contains(lower, "cannot unmarshal"):
		return "schema_type_mismatch"
	case strings.Contains(lower, "missing tool"):
		return "missing_tool"
	case strings.Contains(lower, "nested request tool") && strings.Contains(lower, "does not match"):
		return "nested_tool_mismatch"
	default:
		return "unknown"
	}
}

func repairStringifiedSubmitPlanEditsToolRequest(raw string, activeToolCall *llama.ToolCall) (workspace.ToolRequest, string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return workspace.ToolRequest{}, "", false
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &obj); err != nil {
		return workspace.ToolRequest{}, "", false
	}
	toolName := ""
	if activeToolCall != nil {
		toolName = strings.TrimSpace(activeToolCall.Function.Name)
	}
	if toolName == "" {
		_ = json.Unmarshal(obj["tool"], &toolName)
		toolName = strings.TrimSpace(toolName)
	}
	if toolName != "submit_plan" {
		return workspace.ToolRequest{}, "", false
	}
	var encodedEdits string
	if err := json.Unmarshal(obj["edits"], &encodedEdits); err != nil {
		return workspace.ToolRequest{}, "", false
	}
	decodedEdits := strings.TrimSpace(encodedEdits)
	if decodedEdits == "" || !json.Valid([]byte(decodedEdits)) {
		return workspace.ToolRequest{}, "", false
	}
	var edits []workspace.PlanEdit
	if err := json.Unmarshal([]byte(decodedEdits), &edits); err != nil || len(edits) == 0 {
		return workspace.ToolRequest{}, "", false
	}
	obj["edits"] = json.RawMessage(decodedEdits)
	if _, ok := obj["tool"]; !ok {
		obj["tool"] = json.RawMessage(`"submit_plan"`)
	}
	repaired, err := json.Marshal(obj)
	if err != nil {
		return workspace.ToolRequest{}, "", false
	}
	req, err := workspace.DecodeToolRequestJSON(repaired)
	if err != nil {
		return workspace.ToolRequest{}, "", false
	}
	req.Tool = "submit_plan"
	return req, string(repaired), true
}

func normalizeIdiomCLIToolRequestSyntax(text string) toolRequestSyntaxNormalization {
	req, raw, ok, unsafe := parseIdiomCLIToolRequestSyntax(text)
	if !ok {
		if unsafe {
			return toolRequestSyntaxNormalization{
				EventOutcome:   toolRequestNormalizerOutcomeUnsafeToInfer,
				EventTransport: toolRequestNormalizerTransportIdiomCLI,
			}
		}
		return toolRequestSyntaxNormalization{}
	}
	result := toolRequestSyntaxNormalization{
		Request:        req,
		Raw:            raw,
		OK:             true,
		EventOutcome:   toolRequestNormalizerOutcomeNormalized,
		EventTransport: toolRequestNormalizerTransportIdiomCLI,
		Repaired:       true,
	}
	normalizeParsedToolRequestSyntax(&result)
	return result
}

func parseIdiomCLIToolRequestSyntax(text string) (workspace.ToolRequest, string, bool, bool) {
	trimmed := strings.TrimSpace(text)
	lower := strings.ToLower(trimmed)
	if !strings.HasPrefix(lower, "idiom tool ") {
		return workspace.ToolRequest{}, "", false, false
	}
	objectStart := strings.Index(trimmed, "{")
	if objectStart < 0 {
		return workspace.ToolRequest{}, "", false, true
	}
	prefix := strings.TrimSpace(trimmed[:objectStart])
	parts := strings.Fields(prefix)
	if len(parts) < 3 || !strings.EqualFold(parts[0], "idiom") || !strings.EqualFold(parts[1], "tool") || !toolRequestSyntaxIdentifier(parts[2]) {
		return workspace.ToolRequest{}, "", false, true
	}
	object, _, ok := extractJSONObjectAt(trimmed, objectStart)
	if !ok {
		return workspace.ToolRequest{}, "", false, true
	}
	req, err := workspace.DecodeToolRequestJSON([]byte(object))
	if err != nil {
		return workspace.ToolRequest{}, "", false, true
	}
	if strings.TrimSpace(req.Tool) == "" {
		req.Tool = parts[2]
	}
	if !strings.EqualFold(strings.TrimSpace(req.Tool), parts[2]) {
		return workspace.ToolRequest{}, "", false, true
	}
	return req, object, true, false
}

func normalizeFencedJSONToolRequestSyntax(text string) toolRequestSyntaxNormalization {
	payload, ok, unsafe := exactFencedJSONPayload(text)
	if !ok {
		if unsafe {
			return toolRequestSyntaxNormalization{
				EventOutcome:   toolRequestNormalizerOutcomeUnsafeToInfer,
				EventTransport: toolRequestNormalizerTransportFencedJSON,
			}
		}
		return toolRequestSyntaxNormalization{}
	}
	req, err := workspace.DecodeToolRequestJSON([]byte(payload))
	result := toolRequestSyntaxNormalization{
		Raw:            payload,
		ParseErr:       err,
		EventTransport: toolRequestNormalizerTransportFencedJSON,
	}
	if err != nil {
		result.EventOutcome = toolRequestNormalizerOutcomeUnsupportedShape
		result.ParserFailureClass = toolRequestParserFailureClass(result.Raw, err)
		return result
	}
	if strings.TrimSpace(req.Tool) == "" {
		result.EventOutcome = toolRequestNormalizerOutcomeUnsafeToInfer
		return result
	}
	result.Request = req
	result.OK = true
	result.EventOutcome = toolRequestNormalizerOutcomeNormalized
	normalizeParsedToolRequestSyntax(&result)
	return result
}

func exactFencedJSONPayload(text string) (payload string, ok bool, unsafe bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return "", false, false
	}
	if !strings.HasPrefix(trimmed, "```") {
		if strings.Contains(strings.ToLower(trimmed), "```json") {
			return "", false, true
		}
		return "", false, false
	}
	firstLineEnd := strings.Index(trimmed, "\n")
	if firstLineEnd < 0 {
		return "", false, true
	}
	language := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(trimmed[:firstLineEnd], "```")))
	if language != "json" {
		return "", false, false
	}
	rest := trimmed[firstLineEnd+1:]
	if !strings.HasSuffix(rest, "\n```") {
		return "", false, true
	}
	payload = strings.TrimSpace(strings.TrimSuffix(rest, "\n```"))
	if !strings.HasPrefix(payload, "{") {
		return "", false, true
	}
	return payload, true, false
}

func normalizeKeyValueToolRequestSyntax(text string) toolRequestSyntaxNormalization {
	req, ok, unsafe := parseKeyValueToolRequestSyntax(text)
	if !ok {
		if unsafe {
			return toolRequestSyntaxNormalization{
				EventOutcome:   toolRequestNormalizerOutcomeUnsafeToInfer,
				EventTransport: toolRequestNormalizerTransportKeyValue,
			}
		}
		return toolRequestSyntaxNormalization{}
	}
	raw := marshalToolRequestRaw(req)
	result := toolRequestSyntaxNormalization{
		Request:        req,
		Raw:            raw,
		OK:             true,
		EventOutcome:   toolRequestNormalizerOutcomeNormalized,
		EventTransport: toolRequestNormalizerTransportKeyValue,
	}
	normalizeParsedToolRequestSyntax(&result)
	return result
}

func normalizeKeyValueArgsToolRequestSyntax(text string) toolRequestSyntaxNormalization {
	req, raw, ok, unsafe := parseKeyValueArgsToolRequestSyntax(text)
	if !ok {
		if unsafe {
			return toolRequestSyntaxNormalization{
				EventOutcome:   toolRequestNormalizerOutcomeUnsafeToInfer,
				EventTransport: toolRequestNormalizerTransportKeyValueArgs,
			}
		}
		return toolRequestSyntaxNormalization{}
	}
	result := toolRequestSyntaxNormalization{
		Request:        req,
		Raw:            raw,
		OK:             true,
		EventOutcome:   toolRequestNormalizerOutcomeNormalized,
		EventTransport: toolRequestNormalizerTransportKeyValueArgs,
		Repaired:       true,
	}
	normalizeParsedToolRequestSyntax(&result)
	return result
}

func parseKeyValueArgsToolRequestSyntax(text string) (workspace.ToolRequest, string, bool, bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || !strings.Contains(trimmed, "{") || !strings.Contains(strings.ToLower(trimmed), "args:") {
		return workspace.ToolRequest{}, "", false, false
	}
	tool, argsStart, ok := keyValueArgsToolAndBodyStart(trimmed)
	if !ok {
		return workspace.ToolRequest{}, "", false, true
	}
	objectOffset := strings.Index(trimmed[argsStart:], "{")
	if objectOffset < 0 {
		return workspace.ToolRequest{}, "", false, true
	}
	objectStart := argsStart + objectOffset
	object, _, ok := extractJSONObjectAt(trimmed, objectStart)
	if !ok {
		return workspace.ToolRequest{}, "", false, true
	}
	payload, ok := normalizeKeyValueArgsObject(tool, object)
	if !ok {
		return workspace.ToolRequest{}, "", false, true
	}
	req, err := workspace.DecodeToolRequestJSON([]byte(payload))
	if err != nil || strings.TrimSpace(req.Tool) == "" {
		return workspace.ToolRequest{}, "", false, true
	}
	return req, payload, true, false
}

func keyValueArgsToolAndBodyStart(text string) (string, int, bool) {
	tool := ""
	argsStart := -1
	offset := 0
	for _, line := range strings.SplitAfter(text, "\n") {
		rawLine := strings.TrimRight(line, "\r\n")
		trimmed := strings.TrimSpace(rawLine)
		lower := strings.ToLower(trimmed)
		if value, ok := strings.CutPrefix(lower, "tool:"); ok {
			original := strings.TrimSpace(rawLine[strings.Index(strings.ToLower(rawLine), "tool:")+len("tool:"):])
			if original == "" || !toolRequestSyntaxIdentifier(original) {
				return "", 0, false
			}
			tool = original
			_ = value
		}
		if strings.HasPrefix(lower, "args:") {
			argsIndex := strings.Index(strings.ToLower(rawLine), "args:")
			argsStart = offset + argsIndex + len("args:")
			break
		}
		offset += len(line)
	}
	if tool == "" || argsStart < 0 {
		return "", 0, false
	}
	return tool, argsStart, true
}

func normalizeKeyValueArgsObject(tool string, object string) (string, bool) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(object), &fields); err != nil {
		return "", false
	}
	if rawTool, exists := fields["tool"]; exists {
		var explicit string
		if err := json.Unmarshal(rawTool, &explicit); err != nil {
			return "", false
		}
		if explicit = strings.TrimSpace(explicit); explicit != "" && explicit != tool {
			return "", false
		}
	}
	if _, ok := fields["capability"]; !ok && syntaxToolRequestUsesCapabilityAlias(tool) {
		if rawKind, hasKind := fields["kind"]; hasKind {
			var kind string
			if err := json.Unmarshal(rawKind, &kind); err == nil && strings.TrimSpace(kind) != "" {
				capabilityRaw, _ := json.Marshal(strings.TrimSpace(kind))
				fields["capability"] = capabilityRaw
			}
		}
		if _, hasCapability := fields["capability"]; !hasCapability {
			if rawResolution, hasResolution := fields["resolution"]; hasResolution {
				var resolution string
				if err := json.Unmarshal(rawResolution, &resolution); err == nil && strings.TrimSpace(resolution) != "" {
					capabilityRaw, _ := json.Marshal(strings.TrimSpace(resolution))
					fields["capability"] = capabilityRaw
				}
			}
		}
	}
	toolRaw, _ := json.Marshal(tool)
	fields["tool"] = toolRaw
	delete(fields, "kind")
	payload, err := json.Marshal(fields)
	if err != nil {
		return "", false
	}
	return string(payload), true
}

func normalizeFunctionCallToolRequestSyntax(text string) toolRequestSyntaxNormalization {
	req, ok, unsafe := parseFunctionCallToolRequestSyntax(text)
	if !ok {
		if unsafe {
			return toolRequestSyntaxNormalization{
				EventOutcome:   toolRequestNormalizerOutcomeUnsafeToInfer,
				EventTransport: toolRequestNormalizerTransportFunctionCall,
			}
		}
		return toolRequestSyntaxNormalization{}
	}
	raw := marshalToolRequestRaw(req)
	result := toolRequestSyntaxNormalization{
		Request:        req,
		Raw:            raw,
		OK:             true,
		EventOutcome:   toolRequestNormalizerOutcomeNormalized,
		EventTransport: toolRequestNormalizerTransportFunctionCall,
	}
	normalizeParsedToolRequestSyntax(&result)
	return result
}

func normalizeParsedToolRequestSyntax(result *toolRequestSyntaxNormalization) {
	if result == nil || !result.OK {
		return
	}
	if normalizeReadToolAlias(&result.Request) {
		result.Raw = marshalToolRequestRaw(result.Request)
		result.Repaired = true
	}
	if normalizeCodeNavigateMissingCapability(&result.Request) {
		result.Raw = marshalToolRequestRaw(result.Request)
		result.Repaired = true
	}
}

func normalizeReadToolAlias(req *workspace.ToolRequest) bool {
	if req == nil || strings.TrimSpace(req.Tool) != "code_read" || strings.TrimSpace(req.Path) == "" {
		return false
	}
	req.Tool = "read_file"
	return true
}

func normalizeCodeNavigateMissingCapability(req *workspace.ToolRequest) bool {
	if req == nil || strings.TrimSpace(req.Tool) != "code_navigate" || strings.TrimSpace(req.Capability) != "" {
		return false
	}
	mode := strings.TrimSpace(req.Mode)
	if mode != "" && visibleCodeNavigateCapability(mode) {
		req.Capability = mode
		return true
	}
	pattern := strings.TrimSpace(req.Pattern)
	query := strings.TrimSpace(req.Query)
	symbol := strings.TrimSpace(req.Symbol)
	target := util.FirstNonEmpty(pattern, query, symbol)
	if target != "" && !visibleCodeNavigateCapability(target) {
		req.Capability = "read_symbol"
		if strings.TrimSpace(req.Pattern) == "" {
			req.Pattern = target
		}
		return true
	}
	if strings.TrimSpace(req.Path) == "" {
		return false
	}
	req.Capability = "file_outline"
	if target != "" && visibleCodeNavigateCapability(target) {
		req.Pattern = ""
		req.Query = ""
		req.Symbol = ""
	}
	return true
}

func parseFunctionCallToolRequestSyntax(text string) (workspace.ToolRequest, bool, bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || strings.Contains(trimmed, "{") || strings.Contains(trimmed, "```") {
		return workspace.ToolRequest{}, false, false
	}
	if strings.Contains(trimmed, "\n") {
		if strings.Contains(trimmed, "(") && keyValueLikeText(trimmed) {
			return workspace.ToolRequest{}, false, true
		}
		return workspace.ToolRequest{}, false, false
	}
	key, body, ok := strings.Cut(trimmed, ":")
	if !ok || strings.ToLower(strings.TrimSpace(key)) != "tool" {
		return workspace.ToolRequest{}, false, false
	}
	body = strings.TrimSpace(body)
	open := strings.Index(body, "(")
	if open < 0 {
		return workspace.ToolRequest{}, false, false
	}
	if !strings.HasSuffix(body, ")") {
		return workspace.ToolRequest{}, false, true
	}
	tool := strings.TrimSpace(body[:open])
	if !toolRequestSyntaxIdentifier(tool) {
		return workspace.ToolRequest{}, false, true
	}
	args := strings.TrimSpace(body[open+1 : len(body)-1])
	values, ok := parseFunctionCallToolArguments(args)
	if !ok {
		return workspace.ToolRequest{}, false, true
	}
	req, ok := toolRequestFromSyntaxValues(tool, values)
	if !ok {
		return workspace.ToolRequest{}, false, true
	}
	return req, true, false
}

func parseFunctionCallToolArguments(args string) (map[string]string, bool) {
	values := map[string]string{}
	if strings.TrimSpace(args) == "" {
		return values, true
	}
	for _, part := range splitFunctionCallArguments(args) {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, false
		}
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			return nil, false
		}
		key = strings.ToLower(strings.TrimSpace(key))
		if !keyValueToolRequestFieldAllowed(key) {
			return nil, false
		}
		decoded, ok := decodeFunctionCallArgumentValue(value)
		if !ok || decoded == "" {
			return nil, false
		}
		values[key] = decoded
	}
	return values, true
}

func splitFunctionCallArguments(args string) []string {
	var parts []string
	var b strings.Builder
	quote := rune(0)
	escaped := false
	for _, r := range args {
		if escaped {
			b.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' && quote != 0 {
			b.WriteRune(r)
			escaped = true
			continue
		}
		if quote != 0 {
			b.WriteRune(r)
			if r == quote {
				quote = 0
			}
			continue
		}
		if r == '"' || r == '\'' {
			quote = r
			b.WriteRune(r)
			continue
		}
		if r == ',' {
			parts = append(parts, b.String())
			b.Reset()
			continue
		}
		b.WriteRune(r)
	}
	parts = append(parts, b.String())
	return parts
}

func decodeFunctionCallArgumentValue(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", false
	}
	if strings.HasPrefix(value, `"`) {
		decoded, err := strconv.Unquote(value)
		if err != nil {
			return "", false
		}
		return strings.TrimSpace(decoded), true
	}
	if strings.HasPrefix(value, `'`) {
		if !strings.HasSuffix(value, `'`) || len(value) < 2 {
			return "", false
		}
		return strings.TrimSpace(strings.Trim(value, `'`)), true
	}
	if strings.ContainsAny(value, " \t") {
		return "", false
	}
	return strings.Trim(value, `"'`), true
}

func toolRequestSyntaxIdentifier(value string) bool {
	if value == "" {
		return false
	}
	for i, r := range value {
		if r == '_' || ('a' <= r && r <= 'z') || ('A' <= r && r <= 'Z') || (i > 0 && '0' <= r && r <= '9') {
			continue
		}
		return false
	}
	return true
}

func parseKeyValueToolRequestSyntax(text string) (workspace.ToolRequest, bool, bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || strings.Contains(trimmed, "{") || strings.Contains(trimmed, "```") {
		return workspace.ToolRequest{}, false, false
	}
	lines := strings.Split(trimmed, "\n")
	values := map[string]string{}
	sawKeyValue := false
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			// Blank line after tool fields ends this tool call
			// so a second tool: block later is ignored.
			if sawKeyValue && strings.TrimSpace(values["tool"]) != "" {
				break
			}
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			if sawKeyValue {
				return workspace.ToolRequest{}, false, true
			}
			return workspace.ToolRequest{}, false, false
		}
		sawKeyValue = true
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if key == "" || value == "" {
			return workspace.ToolRequest{}, false, true
		}
		// Stop at a second tool: line — parse only the first.
		if key == "tool" && strings.TrimSpace(values["tool"]) != "" {
			break
		}
		if !keyValueToolRequestFieldAllowed(key) {
			return workspace.ToolRequest{}, false, true
		}
		values[key] = value
	}
	if !sawKeyValue {
		return workspace.ToolRequest{}, false, false
	}
	tool := strings.TrimSpace(values["tool"])
	if tool == "" {
		return workspace.ToolRequest{}, false, true
	}
	req, ok := toolRequestFromSyntaxValues(tool, values)
	if !ok {
		return workspace.ToolRequest{}, false, true
	}
	return req, true, false
}

func toolRequestFromSyntaxValues(tool string, values map[string]string) (workspace.ToolRequest, bool) {
	req := workspace.ToolRequest{
		Tool:            tool,
		ID:              values["id"],
		Path:            values["path"],
		Pattern:         values["pattern"],
		Query:           values["query"],
		Symbol:          values["symbol"],
		Capability:      values["capability"],
		Operation:       values["operation"],
		Action:          values["action"],
		Mode:            values["mode"],
		Task:            values["task"],
		Glob:            values["glob"],
		Language:        values["language"],
		Resolution:      values["resolution"],
		NamedGap:        values["named_gap"],
		WhyNotAnswering: values["why_not_answering"],
		Purpose:         values["purpose"],
		Command:         values["command"],
		Description:     values["description"],
		Content:         values["content"],
		Prompt:          values["prompt"],
		Agent:           values["agent"],
		Workdir:         values["workdir"],
		Handle:          values["handle"],
		Root:            values["root"],
		Destination:     values["destination"],
		Status:          values["status"],
		PacketPath:      values["packet_path"],
		OutputPath:      values["output_path"],
		Component:       values["component"],
		Category:        values["category"],
		SessionID:       values["session_id"],
		Confidence:      values["confidence"],
		Severity:        values["severity"],
		SuggestedFix:    values["suggested_fix"],
		Expected:        values["expected"],
		Observed:        values["observed"],
		Workaround:      values["workaround"],
		Impact:          values["impact"],
		TaskID:          values["task_id"],
	}
	if strings.TrimSpace(req.Capability) == "" && strings.TrimSpace(req.Operation) != "" {
		req.Capability = req.Operation
	}
	if strings.TrimSpace(req.Capability) == "" && strings.TrimSpace(req.Action) != "" && syntaxToolRequestUsesCapabilityAlias(req.Tool) {
		req.Capability = req.Action
	}
	if strings.TrimSpace(req.Capability) == "" && strings.TrimSpace(values["kind"]) != "" && syntaxToolRequestUsesCapabilityAlias(req.Tool) {
		req.Capability = values["kind"]
	}
	if strings.TrimSpace(req.Capability) == "" && strings.TrimSpace(req.Resolution) != "" && syntaxToolRequestUsesCapabilityAlias(req.Tool) {
		req.Capability = req.Resolution
	}
	if strings.TrimSpace(req.Pattern) == "" && strings.TrimSpace(req.Symbol) != "" {
		req.Pattern = req.Symbol
	}
	if strings.TrimSpace(req.Pattern) == "" && strings.TrimSpace(req.Query) != "" && syntaxToolRequestUsesPatternAlias(req.Tool) {
		req.Pattern = req.Query
	}

	// Parse lines alias: "lines: N-M" becomes start_line=N, end_line=M;
	// "lines: N" becomes start_line=N, end_line=N.
	if raw := strings.TrimSpace(values["lines"]); raw != "" {
		if dash := strings.Index(raw, "-"); dash >= 0 {
			if s, err := strconv.Atoi(strings.TrimSpace(raw[:dash])); err == nil {
				req.StartLine = s
			}
			if e, err := strconv.Atoi(strings.TrimSpace(raw[dash+1:])); err == nil {
				req.EndLine = e
			}
		} else if v, err := strconv.Atoi(raw); err == nil {
			req.StartLine = v
			req.EndLine = v
		}
	}

	for key, assign := range map[string]func(int){
		"start_line":    func(v int) { req.StartLine = v },
		"end_line":      func(v int) { req.EndLine = v },
		"limit":         func(v int) { req.Limit = v },
		"budget_tokens": func(v int) { req.BudgetTokens = v },
		"max_depth":     func(v int) { req.MaxDepth = v },
	} {
		if raw := strings.TrimSpace(values[key]); raw != "" {
			v, err := strconv.Atoi(raw)
			if err != nil {
				return workspace.ToolRequest{}, false
			}
			assign(v)
		}
	}
	for key, assign := range map[string]func(bool){
		"include_evidence": func(v bool) { req.IncludeEvidence = v },
		"include_source":   func(v bool) { req.IncludeSource = v },
		"include_files":    func(v bool) { req.IncludeFiles = v },
		"refresh":          func(v bool) { req.Refresh = v },
	} {
		if raw := strings.TrimSpace(values[key]); raw != "" {
			v, err := strconv.ParseBool(raw)
			if err != nil {
				return workspace.ToolRequest{}, false
			}
			assign(v)
		}
	}
	return req, true
}

func syntaxToolRequestUsesPatternAlias(tool string) bool {
	switch strings.TrimSpace(tool) {
	case "search_content", "rg", "grep", "search_files":
		return true
	default:
		return false
	}
}

func syntaxToolRequestUsesCapabilityAlias(tool string) bool {
	switch strings.TrimSpace(tool) {
	case "code_navigate", "read_only_capability":
		return true
	default:
		return false
	}
}

func keyValueToolRequestFieldAllowed(key string) bool {
	switch key {
	case "tool", "id", "path", "file", "pattern", "query", "capability", "operation", "action", "kind", "mode", "task", "glob", "language", "resolution",
		"symbol", "named_gap", "why_not_answering", "start_line", "end_line", "limit", "budget_tokens", "max_depth",
		"include_evidence", "include_source", "include_files", "refresh", "lines",
		"purpose", "command", "description", "content", "prompt", "agent", "workdir", "handle", "root", "destination", "status",
		"packet_path", "output_path", "component", "category", "session_id", "confidence", "severity", "suggested_fix",
		"expected", "observed", "workaround", "impact", "task_id", "recursive":
		return true
	default:
		return false
	}
}

func classifyUnsupportedTextualToolShape(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}
	lower := strings.ToLower(trimmed)
	if strings.Contains(lower, "```json") || strings.Contains(trimmed, "{") {
		return toolRequestNormalizerOutcomeUnsafeToInfer
	}
	if strings.Contains(trimmed, ":") && keyValueLikeText(trimmed) {
		return toolRequestNormalizerOutcomeUnsafeToInfer
	}
	return ""
}

func keyValueLikeText(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		key, _, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		if keyValueToolRequestFieldAllowed(strings.ToLower(strings.TrimSpace(key))) {
			return true
		}
	}
	return false
}

func toolRequestPayloadMissingExplicitTool(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" || !strings.HasPrefix(raw, "{") {
		return false
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		return false
	}
	_, hasTool := fields["tool"]
	return !hasTool
}

func missingToolInferenceNeedsTypedAuthority(tool string) bool {
	switch strings.TrimSpace(tool) {
	case "append_file",
		"apply_patch",
		"delegate_edit",
		"edit_file",
		"go_move_declarations",
		"mkdir",
		"shell",
		"submit_plan",
		"task",
		"task_ledger",
		"write_file":
		return true
	default:
		return false
	}
}
