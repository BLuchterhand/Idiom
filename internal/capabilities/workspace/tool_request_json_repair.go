package workspace

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	planartifact "idiom/internal/contracts/planartifact"

	"idiom/internal/platform/textutil"
	semanticlanguage "idiom/internal/repository/semantic/language"
	"idiom/internal/repository/semantic/languagetools"
)

var malformedToolNameObjectPattern = regexp.MustCompile(`(?s)^\{\s*"tool"\s*:\s*"([A-Za-z_][A-Za-z0-9_.-]*)"\s*:\s*\{(.*)\}\s*\}$`)

func decodeFlatToolRequestJSON(payload []byte) (ToolRequest, error) {
	var req ToolRequest

	stringifiedFields := stringifiedStructuredToolFields(payload)

	if _, ok := stringifiedFields["edits"]; ok {
		if repaired, ok := repairToolRequestJSONPayload(payload); ok {
			var repairedReq ToolRequest

			if repairErr := json.Unmarshal(repaired, &repairedReq); repairErr == nil && !toolRequestLostStringifiedStructuredField(repairedReq, stringifiedFields) {
				normalizeToolRequestAliases(&repairedReq)
				repairEmbeddedQwenXMLToolParameters(&repairedReq)

				return repairedReq, nil
			}
		}

		return ToolRequest{}, errors.New("json: cannot unmarshal string into Go struct field ToolRequest.edits of type []planartifact.PlanEdit")
	}

	if err := json.Unmarshal(payload, &req); err != nil {
		if repaired, ok := repairToolRequestJSONPayload(payload); ok {
			if repairErr := json.Unmarshal(repaired, &req); repairErr == nil {
				if toolRequestLostStringifiedStructuredField(req, stringifiedFields) {
					return ToolRequest{}, err
				}

				normalizeToolRequestAliases(&req)
				repairEmbeddedQwenXMLToolParameters(&req)

				return req, nil
			}
		}

		return ToolRequest{}, err
	}

	if repaired, ok := repairToolRequestJSONPayload(payload); ok {
		var repairedReq ToolRequest

		if repairErr := json.Unmarshal(repaired, &repairedReq); repairErr == nil {
			if !toolRequestLostStringifiedStructuredField(repairedReq, stringifiedFields) {
				req = repairedReq
			}
		}
	}

	normalizeToolRequestAliases(&req)
	repairEmbeddedQwenXMLToolParameters(&req)

	return req, nil
}

func decodeNestedToolRequestJSON(payload []byte) (ToolRequest, bool, error) {
	var envelope struct {
		Tool    string          `json:"tool"`
		Request json.RawMessage `json:"request"`
		Input   json.RawMessage `json:"input"`
	}

	if err := json.Unmarshal(payload, &envelope); err != nil {
		return ToolRequest{}, false, nil
	}

	outerTool := strings.TrimSpace(envelope.Tool)
	request := bytes.TrimSpace(envelope.Request)

	if len(request) == 0 {
		request = bytes.TrimSpace(envelope.Input)
	}

	if outerTool == "" || len(request) == 0 {
		return ToolRequest{}, false, nil
	}

	if len(request) > 0 && request[0] == '"' {
		var encoded string

		if err := json.Unmarshal(request, &encoded); err != nil {
			return ToolRequest{}, true, fmt.Errorf("decode nested request: %w", err)
		}

		request = []byte(strings.TrimSpace(encoded))
	}

	if len(request) == 0 || request[0] != '{' {
		return ToolRequest{}, false, nil
	}

	nested, err := decodeFlatToolRequestJSON(request)
	if err != nil {
		return ToolRequest{}, true, fmt.Errorf("decode nested request: %w", err)
	}

	if strings.TrimSpace(nested.Tool) == "" {
		nested.Tool = outerTool
	} else if outerTool == ToolNameCodeNavigate && CodeNavigateCapabilityAlias(nested.Tool) != "" {
		if strings.TrimSpace(nested.Capability) == "" {
			nested.Capability = CodeNavigateCapabilityAlias(nested.Tool)
		}

		nested.Tool = outerTool
	} else if strings.TrimSpace(nested.Tool) != outerTool {
		return ToolRequest{}, true, fmt.Errorf("nested request tool %q does not match envelope tool %q", nested.Tool, outerTool)
	}

	normalizeToolRequestAliases(&nested)
	repairEmbeddedQwenXMLToolParameters(&nested)

	return nested, true, nil
}

func CodeNavigateCapabilityAlias(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}

	spec, err := languagetools.CodeNavigationSpecFor(value)
	if err != nil {
		return ""
	}

	return strings.TrimSpace(spec.Name)
}

func CodeNavigateResolutionCapabilityAlias(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || ContextGraphResolutionOnly(value) {
		return ""
	}

	return CodeNavigateCapabilityAlias(value)
}

func ContextGraphResolutionOnly(value string) bool {
	key := strings.ToLower(strings.TrimSpace(value))

	key = strings.ReplaceAll(key, "-", "_")
	key = strings.ReplaceAll(key, " ", "_")

	switch key {
	case "repository", "package", "file", "symbol", "summary", "any":
		return true
	default:
		return false
	}
}

var stringifiedJSONToolFieldNames = []string{
	"edits",
	"evidence",
	"required_tests",
	"required_edit_paths",
	"forbidden_paths",
	"source_satisfied_paths",
	"declarations",
	"moves",
	"todos",
	"items",
	"plan_artifact",
}

var stringArrayToolFieldNames = []string{
	"evidence",
	"required_tests",
	"required_edit_paths",
	"source_satisfied_paths",
	"forbidden_paths",
	"declarations",
}

var singletonArrayToolFieldNames = []string{
	"edits",
	"moves",
	"todos",
	"items",
}

func stringifiedJSONToolFieldAllowedStarts(field string) string {
	switch field {
	case "plan_artifact":
		return "{"
	default:
		if stringifiedJSONToolFieldIsArray(field) {
			return "[{"
		}

		return ""
	}
}

func stringifiedJSONToolFieldIsArray(field string) bool {
	return slices.Contains(stringifiedJSONToolFieldNames, field) && field != "plan_artifact"
}

func stringifiedStructuredToolFields(payload []byte) map[string]string {
	var obj map[string]json.RawMessage

	if err := json.Unmarshal(payload, &obj); err != nil {
		return nil
	}

	out := make(map[string]string)

	for _, field := range stringifiedJSONToolFieldNames {
		raw, ok := obj[field]
		if !ok {
			continue
		}

		var encoded string

		if err := json.Unmarshal(raw, &encoded); err == nil && strings.TrimSpace(encoded) != "" {
			out[field] = strings.TrimSpace(encoded)
		}
	}

	return out
}

func toolRequestLostStringifiedStructuredField(req ToolRequest, fields map[string]string) bool {
	if len(fields) == 0 {
		return false
	}

	for field, value := range fields {
		if decodedStructuredToolFieldEmpty(req, field) && !stringifiedStructuredFieldRepresentsEmptyValue(field, value) {
			return true
		}
	}

	return false
}

func decodedStructuredToolFieldEmpty(req ToolRequest, field string) bool {
	switch field {
	case "edits":
		return len(req.Edits) == 0
	case "required_tests":
		return len(req.RequiredTests) == 0
	case "required_edit_paths":
		return len(req.RequiredEditPaths) == 0
	case "source_satisfied_paths":
		return len(req.SourceSatisfiedPaths) == 0
	case "forbidden_paths":
		return len(req.ForbiddenPaths) == 0
	case "declarations":
		return len(req.Declarations) == 0
	case "moves":
		return len(req.Moves) == 0
	case "todos":
		return len(req.Todos) == 0
	case "items":
		return len(req.Items) == 0
	case "plan_artifact":
		return req.PlanArtifact == nil
	default:
		return false
	}
}

func stringifiedStructuredFieldRepresentsEmptyValue(field string, value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}

	switch field {
	case "plan_artifact":
		var obj map[string]json.RawMessage

		if err := json.Unmarshal([]byte(value), &obj); err != nil {
			return false
		}

		return len(obj) == 0
	default:
		if !stringifiedJSONToolFieldIsArray(field) {
			return false
		}

		var arr []json.RawMessage

		if err := json.Unmarshal([]byte(value), &arr); err != nil {
			return false
		}

		return len(arr) == 0
	}
}

func repairEmbeddedQwenXMLToolParameters(req *ToolRequest) {
	if req == nil || strings.TrimSpace(req.Rationale) == "" || !strings.Contains(req.Rationale, "<parameter=") {
		return
	}

	for _, param := range qwenXMLParameterPattern.FindAllStringSubmatch(req.Rationale, -1) {
		if len(param) != 3 {
			continue
		}

		name := strings.TrimSpace(param[1])

		raw, err := qwenXMLParameterRaw(name, param[2])
		if err != nil {
			continue
		}

		switch name {
		case "edits":
			if len(req.Edits) == 0 {
				var edits []planartifact.PlanEdit

				if err := json.Unmarshal(raw, &edits); err == nil {
					req.Edits = edits
				}
			}
		case "required_tests":
			if len(req.RequiredTests) == 0 {
				var values []string

				if err := json.Unmarshal(raw, &values); err == nil {
					req.RequiredTests = values
				}
			}
		case "required_edit_paths":
			if len(req.RequiredEditPaths) == 0 {
				var values []string

				if err := json.Unmarshal(raw, &values); err == nil {
					req.RequiredEditPaths = values
				}
			}
		case "rationale":
			var value string

			if err := json.Unmarshal(raw, &value); err == nil && strings.TrimSpace(value) != "" {
				req.Rationale = strings.TrimSpace(value)
			}
		}
	}

	if idx := strings.Index(req.Rationale, "<parameter="); idx >= 0 {
		req.Rationale = strings.TrimSpace(req.Rationale[:idx])
	}
}

func repairToolRequestJSONPayload(payload []byte) ([]byte, bool) {
	current := payload
	repaired := false

	for range 4 {
		stepRepaired := false

		for _, repair := range []func([]byte) ([]byte, bool){
			repairMalformedToolNameObjectPayload,
			repairRawJSONControlChars,
			repairStringifiedJSONToolFields,
			repairObjectEvidenceToolField,
			repairSourceSatisfiedPathsToolField,
			repairSingletonArrayToolFields,
			repairStringArrayToolFields,
			repairNumericStringToolFields,
			repairBoolStringToolFields,
		} {
			next, ok := repair(current)
			if !ok {
				continue
			}

			current = next
			repaired = true
			stepRepaired = true
		}

		if !stepRepaired {
			break
		}
	}

	if !repaired {
		return nil, false
	}

	return current, true
}

func repairMalformedToolNameObjectPayload(payload []byte) ([]byte, bool) {
	text := strings.TrimSpace(string(payload))
	if text == "" {
		return nil, false
	}

	matches := malformedToolNameObjectPattern.FindStringSubmatch(text)
	if len(matches) != 3 {
		return nil, false
	}

	tool := strings.TrimSpace(matches[1])
	body := strings.TrimSpace(matches[2])

	if tool == "" || body == "" {
		return nil, false
	}

	repaired := fmt.Sprintf(`{"tool":%q,%s}`, tool, body)
	if !json.Valid([]byte(repaired)) {
		return nil, false
	}

	return []byte(repaired), true
}

func repairRawJSONControlChars(payload []byte) ([]byte, bool) {
	repaired := escapeRawControlCharsInJSONStringLiterals(string(payload))
	if repaired == string(payload) {
		return nil, false
	}

	if !json.Valid([]byte(repaired)) {
		return nil, false
	}

	return []byte(repaired), true
}

func repairStringifiedJSONToolFields(payload []byte) ([]byte, bool) {
	var obj map[string]json.RawMessage

	if err := json.Unmarshal(payload, &obj); err != nil {
		return nil, false
	}

	repaired := false

	for _, field := range stringifiedJSONToolFieldNames {
		raw, ok := obj[field]
		if !ok {
			continue
		}

		var encoded string

		if err := json.Unmarshal(raw, &encoded); err != nil {
			continue
		}

		decoded := strings.TrimSpace(encoded)
		if decoded == "" {
			continue
		}

		if json.Valid([]byte(decoded)) {
			obj[field] = json.RawMessage(decoded)
			repaired = true

			continue
		}

		if escaped, ok := repairStringifiedJSONControlChars(decoded); ok {
			obj[field] = escaped
			repaired = true

			continue
		}

		if stringifiedStructuredFieldHasTopLevelJSONStart(field, decoded) {
			if extracted, ok := extractLeadingStringifiedJSONToolFieldValue(field, decoded); ok {
				obj[field] = extracted
				repaired = true
			}

			continue
		}

		if extracted, ok := extractStringifiedJSONToolFieldValue(field, decoded); ok {
			obj[field] = extracted
			repaired = true
		}
	}

	if !repaired {
		return nil, false
	}

	out, err := json.Marshal(obj)
	if err != nil {
		return nil, false
	}

	return out, true
}

func repairObjectEvidenceToolField(payload []byte) ([]byte, bool) {
	var obj map[string]json.RawMessage

	if err := json.Unmarshal(payload, &obj); err != nil {
		return nil, false
	}

	raw, ok := obj["evidence"]
	if !ok || len(bytes.TrimSpace(raw)) == 0 || bytes.TrimSpace(raw)[0] != '{' {
		return nil, false
	}

	encoded, err := json.Marshal([]string{string(bytes.TrimSpace(raw))})
	if err != nil {
		return nil, false
	}

	obj["evidence"] = encoded

	out, err := json.Marshal(obj)
	if err != nil {
		return nil, false
	}

	return out, true
}

func repairSourceSatisfiedPathsToolField(payload []byte) ([]byte, bool) {
	var obj map[string]json.RawMessage

	if err := json.Unmarshal(payload, &obj); err != nil {
		return nil, false
	}

	raw, ok := obj["source_satisfied_paths"]
	if !ok {
		return nil, false
	}

	paths, ok := decodeSourceSatisfiedPathsField(raw, true)
	if !ok {
		return nil, false
	}

	encoded, err := json.Marshal(paths)
	if err != nil {
		return nil, false
	}

	obj["source_satisfied_paths"] = encoded

	out, err := json.Marshal(obj)
	if err != nil {
		return nil, false
	}

	return out, true
}

func decodeSourceSatisfiedPathsField(raw json.RawMessage, allowPathKeyedObject bool) ([]string, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, false
	}

	var paths []string

	switch trimmed[0] {
	case '"':
		var value string

		if err := json.Unmarshal(trimmed, &value); err != nil {
			return nil, false
		}

		value = strings.TrimSpace(value)
		if value == "" {
			return nil, false
		}

		return []string{value}, true
	case '{':
		return decodeSourceSatisfiedPathsObject(trimmed, allowPathKeyedObject)
	case '[':
		var items []json.RawMessage

		if err := json.Unmarshal(trimmed, &items); err != nil {
			return nil, false
		}

		for _, item := range items {
			itemPaths, ok := decodeSourceSatisfiedPathsField(item, false)
			if !ok {
				return nil, false
			}

			paths = append(paths, itemPaths...)
		}
	default:
		return nil, false
	}

	paths = textutil.Unique(paths)
	if len(paths) == 0 {
		return nil, false
	}

	return paths, true
}

func decodeSourceSatisfiedPathsObject(raw json.RawMessage, allowPathKeyedObject bool) ([]string, bool) {
	var obj map[string]json.RawMessage

	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, false
	}

	for _, key := range []string{"path", "file"} {
		if value, ok := decodeStringObjectField(obj, key); ok {
			return []string{value}, true
		}
	}

	for _, key := range []string{"paths", "files", "source_satisfied_paths"} {
		if rawPaths, ok := obj[key]; ok {
			return decodeSourceSatisfiedPathsField(rawPaths, true)
		}
	}

	if !allowPathKeyedObject {
		return nil, false
	}

	paths := make([]string, 0, len(obj))
	for key := range obj {
		key = strings.TrimSpace(key)
		if key != "" {
			paths = append(paths, key)
		}
	}

	paths = textutil.Unique(paths)
	if len(paths) == 0 {
		return nil, false
	}

	sort.Strings(paths)

	return paths, true
}

func decodeStringObjectField(obj map[string]json.RawMessage, key string) (string, bool) {
	raw, ok := obj[key]
	if !ok {
		return "", false
	}

	var value string

	if err := json.Unmarshal(raw, &value); err != nil {
		return "", false
	}

	value = strings.TrimSpace(value)

	return value, value != ""
}

func stringifiedStructuredFieldHasTopLevelJSONStart(field string, value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}

	allowedStarts := stringifiedJSONToolFieldAllowedStarts(field)
	if allowedStarts == "" {
		return false
	}

	return strings.ContainsRune(allowedStarts, rune(value[0]))
}

func extractLeadingStringifiedJSONToolFieldValue(field string, value string) (json.RawMessage, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, false
	}

	allowedStarts := stringifiedJSONToolFieldAllowedStarts(field)
	if allowedStarts == "" {
		return nil, false
	}

	if !strings.ContainsRune(allowedStarts, rune(value[0])) {
		return nil, false
	}

	end, ok := balancedJSONEnd(value, 0)
	if !ok || !safeTrailingStringifiedJSONText(value[end:]) {
		return nil, false
	}

	candidate := strings.TrimSpace(value[:end])
	if json.Valid([]byte(candidate)) {
		return json.RawMessage(candidate), true
	}

	if escaped, ok := repairStringifiedJSONControlChars(candidate); ok {
		return escaped, true
	}

	return nil, false
}

func safeTrailingStringifiedJSONText(tail string) bool {
	if strings.TrimSpace(tail) == "" {
		return true
	}

	return strings.HasPrefix(tail, "\n\n") || strings.HasPrefix(strings.TrimSpace(tail), "```")
}

func extractStringifiedJSONToolFieldValue(field string, value string) (json.RawMessage, bool) {
	allowedStarts := stringifiedJSONToolFieldAllowedStarts(field)
	if allowedStarts == "" {
		return nil, false
	}

	return extractBalancedJSONToolValue(value, allowedStarts)
}

func extractBalancedJSONToolValue(value string, allowedStarts string) (json.RawMessage, bool) {
	for i := 0; i < len(value); i++ {
		if !strings.ContainsRune(allowedStarts, rune(value[i])) {
			continue
		}

		end, ok := balancedJSONEnd(value, i)
		if !ok {
			continue
		}

		candidate := strings.TrimSpace(value[i:end])
		if json.Valid([]byte(candidate)) {
			return json.RawMessage(candidate), true
		}

		if escaped, ok := repairStringifiedJSONControlChars(candidate); ok {
			return escaped, true
		}
	}

	return nil, false
}

func repairStringifiedJSONControlChars(value string) (json.RawMessage, bool) {
	repaired := escapeRawControlCharsInJSONStringLiterals(value)
	if repaired == value {
		return nil, false
	}

	repaired = strings.TrimSpace(repaired)
	if !json.Valid([]byte(repaired)) {
		return nil, false
	}

	return json.RawMessage(repaired), true
}

func escapeRawControlCharsInJSONStringLiterals(value string) string {
	var b strings.Builder

	b.Grow(len(value))

	inString := false
	escaped := false
	changed := false

	for _, r := range value {
		if !inString {
			if r == '"' {
				inString = true
			}

			b.WriteRune(r)

			continue
		}

		if escaped {
			escaped = false

			b.WriteRune(r)

			continue
		}

		switch r {
		case '\\':
			escaped = true

			b.WriteRune(r)
		case '"':
			inString = false

			b.WriteRune(r)
		case '\n':
			b.WriteString(`\n`)

			changed = true
		case '\r':
			b.WriteString(`\r`)

			changed = true
		case '\t':
			b.WriteString(`\t`)

			changed = true
		default:
			if r >= 0 && r < 0x20 {
				b.WriteString(fmt.Sprintf(`\u%04x`, r))

				changed = true

				continue
			}

			b.WriteRune(r)
		}
	}

	if !changed {
		return value
	}

	return b.String()
}

func balancedJSONEnd(value string, start int) (int, bool) {
	if start < 0 || start >= len(value) {
		return 0, false
	}

	var stack []byte

	inString := false
	escaped := false

	for i := start; i < len(value); i++ {
		ch := value[i]

		if inString {
			if escaped {
				escaped = false
				continue
			}

			switch ch {
			case '\\':
				escaped = true
			case '"':
				inString = false
			}

			continue
		}

		switch ch {
		case '"':
			inString = true
		case '[', '{':
			stack = append(stack, ch)
		case ']':
			if len(stack) == 0 || stack[len(stack)-1] != '[' {
				return 0, false
			}

			stack = stack[:len(stack)-1]
		case '}':
			if len(stack) == 0 || stack[len(stack)-1] != '{' {
				return 0, false
			}

			stack = stack[:len(stack)-1]
		}

		if len(stack) == 0 {
			return i + 1, true
		}
	}

	return 0, false
}

func repairSingletonArrayToolFields(payload []byte) ([]byte, bool) {
	var obj map[string]json.RawMessage

	if err := json.Unmarshal(payload, &obj); err != nil {
		return nil, false
	}

	repaired := false

	for _, field := range singletonArrayToolFieldNames {
		raw, ok := obj[field]
		if !ok {
			continue
		}

		trimmed := bytes.TrimSpace(raw)
		if len(trimmed) == 0 || trimmed[0] != '{' {
			continue
		}

		if !json.Valid(trimmed) {
			continue
		}

		wrapped, err := json.Marshal([]json.RawMessage{trimmed})
		if err != nil {
			continue
		}

		obj[field] = wrapped
		repaired = true
	}

	if !repaired {
		return nil, false
	}

	out, err := json.Marshal(obj)
	if err != nil {
		return nil, false
	}

	return out, true
}

func repairStringArrayToolFields(payload []byte) ([]byte, bool) {
	var obj map[string]json.RawMessage

	if err := json.Unmarshal(payload, &obj); err != nil {
		return nil, false
	}

	repaired := false

	for _, field := range stringArrayToolFieldNames {
		raw, ok := obj[field]
		if !ok {
			continue
		}

		var value string

		if err := json.Unmarshal(raw, &value); err != nil {
			continue
		}

		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}

		wrapped, err := json.Marshal([]string{value})
		if err != nil {
			continue
		}

		obj[field] = wrapped
		repaired = true
	}

	if !repaired {
		return nil, false
	}

	out, err := json.Marshal(obj)
	if err != nil {
		return nil, false
	}

	return out, true
}

func normalizeToolRequestAliases(req *ToolRequest) {
	if req == nil {
		return
	}

	if strings.TrimSpace(req.Pattern) == "" && strings.TrimSpace(req.Symbol) != "" {
		req.Pattern = req.Symbol
	}

	if strings.TrimSpace(req.Capability) == "" && strings.TrimSpace(req.Operation) != "" {
		req.Capability = req.Operation
	}

	if strings.TrimSpace(req.Capability) == "" && strings.TrimSpace(req.Action) != "" && toolRequestUsesCapabilityAlias(req.Tool) {
		req.Capability = req.Action
	}

	if strings.TrimSpace(req.Capability) == "" && strings.TrimSpace(req.Resolution) != "" && toolRequestUsesCapabilityAlias(req.Tool) {
		req.Capability = CodeNavigateResolutionCapabilityAlias(req.Resolution)
	}

	if strings.TrimSpace(req.Pattern) == "" && strings.TrimSpace(req.Query) != "" && toolRequestUsesPatternAlias(req.Tool) {
		req.Pattern = req.Query
	}
}

func toolRequestUsesCapabilityAlias(tool string) bool {
	switch strings.TrimSpace(tool) {
	case ToolNameCodeNavigate, ToolNameReadOnlyCapability:
		return true
	default:
		return false
	}
}

func toolRequestUsesPatternAlias(tool string) bool {
	switch strings.TrimSpace(tool) {
	case ToolNameSearchContent, ToolNameRg, ToolNameSearchFiles:
		return true
	default:
		return false
	}
}

func repairNumericStringToolFields(payload []byte) ([]byte, bool) {
	var value any

	if err := json.Unmarshal(payload, &value); err != nil {
		return nil, false
	}

	repaired, invalid := repairNumericStringToolValue(value)
	if invalid || !repaired {
		return nil, false
	}

	out, err := json.Marshal(value)
	if err != nil {
		return nil, false
	}

	return out, true
}

func repairBoolStringToolFields(payload []byte) ([]byte, bool) {
	var value any

	if err := json.Unmarshal(payload, &value); err != nil {
		return nil, false
	}

	repaired, invalid := repairBoolStringToolValue(value)
	if invalid || !repaired {
		return nil, false
	}

	out, err := json.Marshal(value)
	if err != nil {
		return nil, false
	}

	return out, true
}

func repairBoolStringToolValue(value any) (bool, bool) {
	switch typed := value.(type) {
	case map[string]any:
		repaired := false

		for key, raw := range typed {
			if boolToolField(key) {
				text, ok := raw.(string)
				if !ok {
					continue
				}

				parsed, ok := parseStrictBoolString(text)
				if !ok {
					return repaired, true
				}

				typed[key] = parsed
				repaired = true

				continue
			}

			childRepaired, invalid := repairBoolStringToolValue(raw)
			if invalid {
				return repaired, true
			}

			repaired = repaired || childRepaired
		}

		return repaired, false
	case []any:
		repaired := false

		for _, item := range typed {
			childRepaired, invalid := repairBoolStringToolValue(item)
			if invalid {
				return repaired, true
			}

			repaired = repaired || childRepaired
		}

		return repaired, false
	default:
		return false, false
	}
}

func parseStrictBoolString(text string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(text)) {
	case "true":
		return true, true
	case "false":
		return false, true
	default:
		return false, false
	}
}

func repairNumericStringToolValue(value any) (bool, bool) {
	switch typed := value.(type) {
	case map[string]any:
		repaired := false

		for key, raw := range typed {
			if numericToolField(key) {
				text, ok := raw.(string)
				if !ok {
					continue
				}

				text = strings.TrimSpace(text)
				if text == "" || strings.HasPrefix(text, "-") || strings.HasPrefix(text, "+") {
					return repaired, true
				}

				parsed, err := strconv.Atoi(text)
				if err != nil {
					return repaired, true
				}

				typed[key] = parsed
				repaired = true

				continue
			}

			childRepaired, invalid := repairNumericStringToolValue(raw)
			if invalid {
				return repaired, true
			}

			repaired = repaired || childRepaired
		}

		return repaired, false
	case []any:
		repaired := false

		for _, item := range typed {
			childRepaired, invalid := repairNumericStringToolValue(item)
			if invalid {
				return repaired, true
			}

			repaired = repaired || childRepaired
		}

		return repaired, false
	default:
		return false, false
	}
}

func boolToolField(field string) bool {
	switch field {
	case "replace_all", "recursive", "fixed_strings", "include_evidence", "include_source", "include_files", "refresh":
		return true
	default:
		return false
	}
}

func numericToolField(field string) bool {
	switch field {
	case "start_line", "end_line", "max_depth", "limit", "budget_tokens", "freshness_max_files":
		return true
	default:
		return false
	}
}

func inferMissingToolRequest(req ToolRequest) (ToolRequest, bool) {
	if strings.TrimSpace(req.Tool) != "" {
		return req, true
	}

	switch {
	case len(req.Todos) > 0 || len(req.Items) > 0:
		req.Tool = ToolNameTaskLedger
	case strings.TrimSpace(req.Prompt) != "" || strings.TrimSpace(req.Agent) != "":
		req.Tool = ToolNameTask
	case strings.TrimSpace(req.Command) != "":
		req.Tool = ToolNameShell
	case strings.TrimSpace(req.Destination) != "" || len(req.Declarations) > 0 || len(req.Moves) > 0 || strings.HasPrefix(strings.TrimSpace(req.Pattern), "tag:"):
		req.Tool = ToolNameGoMoveDeclarations
	case strings.TrimSpace(req.OldText) != "" || strings.TrimSpace(req.NewText) != "" || (strings.TrimSpace(req.Content) != "" && (req.StartLine != 0 || req.EndLine != 0)):
		req.Tool = ToolNameEditFile
	case strings.TrimSpace(req.Glob) != "" && strings.TrimSpace(req.Pattern) != "":
		req.Tool = ToolNameSearchContent
	case strings.TrimSpace(req.ID) != "" || req.StartLine != 0 || req.EndLine != 0:
		req.Tool = ToolNameReadFile
	case strings.HasSuffix(strings.TrimSpace(req.Path), ".go") && strings.TrimSpace(req.Pattern) == "" && strings.TrimSpace(req.Content) == "":
		req.Tool = LanguageToolForCapability("go", semanticlanguage.CapFileOutline)
	default:
		return ToolRequest{}, false
	}

	return req, true
}
