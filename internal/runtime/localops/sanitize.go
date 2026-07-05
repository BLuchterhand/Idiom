package localops

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"

	"idiom/internal/contracts/sensitivity"
)

var (
	customerPattern    = regexp.MustCompile(`(?i)\bcustomer[a-z0-9_-]*\b|\b[a-z0-9_-]*customer[a-z0-9_-]*\b`)
	privateRepoPattern = regexp.MustCompile(`(?i)\bprivate\s+(repo|repository|workspace|project|customer)\b|\b[a-z0-9_-]*private[a-z0-9_-]*(repo|repository|workspace|project|customer)[a-z0-9_-]*\b|\b[a-z0-9_-]*(repo|repository|workspace|project|customer)[a-z0-9_-]*private[a-z0-9_-]*\b`)
)

func StableID(value string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(value)))
	return hex.EncodeToString(sum[:8])
}

func ContentHash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:8])
}

func SafeToken(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "-", "_")
	value = strings.ReplaceAll(value, " ", "_")

	var b strings.Builder

	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '.' {
			b.WriteRune(r)
		}
	}

	out := b.String()
	if len(out) > 80 {
		return out[:80]
	}

	return out
}

func CompactStrings(values ...string) []string {
	seen := map[string]bool{}

	var out []string

	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}

		seen[value] = true
		out = append(out, value)
	}

	return out
}

func StringField(values map[string]any, key string) string {
	value, ok := values[key]
	if !ok {
		return ""
	}

	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	default:
		return ""
	}
}

func NestedString(values map[string]any, parent string, key string) string {
	child, ok := values[parent].(map[string]any)
	if !ok {
		return ""
	}

	return StringField(child, key)
}

func BoolField(values map[string]any, key string) bool {
	value, ok := values[key]
	if !ok {
		return false
	}

	typed, ok := value.(bool)

	return ok && typed
}

func IntField(values map[string]any, key string) int {
	value, ok := values[key]
	if !ok {
		return 0
	}

	switch typed := value.(type) {
	case float64:
		return int(typed)
	case int:
		return typed
	default:
		return 0
	}
}

func ArrayField(values map[string]any, key string) []any {
	value, ok := values[key]
	if !ok {
		return nil
	}

	typed, ok := value.([]any)
	if !ok {
		return nil
	}

	return typed
}

func SafeCommand(command string) string {
	command = strings.TrimSpace(command)
	if command == "" {
		return ""
	}

	if strings.Contains(command, "\n") || strings.Contains(command, "\r") {
		return ""
	}

	parts := strings.Fields(command)
	for i, part := range parts {
		if CommandTokenNeedsRedaction(part) {
			parts[i] = "[redacted]"
		}
	}

	return strings.Join(parts, " ")
}

func PathEvidence(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}

	return "path_id:" + StableID(path)
}

func SafeText(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}

	parts := strings.Fields(value)
	for i, part := range parts {
		trimmed := strings.Trim(part, ".,:;()[]{}\"'")
		if TokenNeedsRedaction(trimmed) {
			parts[i] = "[redacted:" + StableID(trimmed) + "]"
		}
	}

	return strings.Join(parts, " ")
}

func TaskEvidence(task string) ActivationTaskEvidence {
	task = strings.TrimSpace(task)
	if task == "" {
		task = "localize first useful Idiom quickstart packet"
	}

	classes := SensitiveStringClasses(task)
	if len(classes) == 0 {
		classes = []string{"general_task_text"}
	}

	status := "hashed"
	if len(classes) > 1 || classes[0] != "general_task_text" {
		status = "redacted"
	}

	return ActivationTaskEvidence{
		TaskID:          "task_id:" + StableID(task),
		EvidenceType:    "hash_and_classification",
		RedactionStatus: status,
		RawTaskCaptured: false,
		ContentClasses:  classes,
	}
}

func ProductRedactionStatus(smoke QuickstartSmoke) string {
	if smoke.FirstUsefulPacket.Task.RedactionStatus == "redacted" {
		return "redacted"
	}

	if len(smoke.PrivacyRedactionStatus.RedactedFields) > 0 {
		return "redacted"
	}

	return "pass"
}

func SensitiveStringClasses(value string) []string {
	seen := map[string]bool{}

	var classes []string

	add := func(class string) {
		if !seen[class] {
			seen[class] = true
			classes = append(classes, class)
		}
	}

	if ContainsAbsolutePath(value) {
		add("absolute_path")
	}

	if sensitivity.ContainsSecretMarker(value) {
		add("secret_marker")
	}

	if customerPattern.MatchString(value) || privateRepoPattern.MatchString(value) {
		add("sensitive_identifier")
	}

	return classes
}

func TokenNeedsRedaction(token string) bool {
	token = strings.TrimSpace(strings.Trim(token, ".,:;()[]{}\"'"))
	if token == "" {
		return false
	}

	lower := strings.ToLower(token)

	return filepath.IsAbs(token) ||
		strings.Contains(token, "/") ||
		strings.Contains(token, "\\") ||
		strings.Contains(lower, "secret") ||
		strings.Contains(lower, "token") ||
		strings.Contains(lower, "credential") ||
		strings.Contains(lower, "password") ||
		customerPattern.MatchString(token) ||
		privateRepoPattern.MatchString(token)
}

func CommandTokenNeedsRedaction(token string) bool {
	raw := strings.TrimSpace(token)

	token = strings.TrimSpace(strings.Trim(raw, ".,:;()[]{}\"'"))

	if raw == "" || token == "" {
		return false
	}

	lowerRaw := strings.ToLower(raw)
	lowerToken := strings.ToLower(token)
	hasSensitiveMarker := strings.Contains(lowerRaw, "secret") ||
		strings.Contains(lowerRaw, "token") ||
		strings.Contains(lowerRaw, "credential") ||
		strings.Contains(lowerRaw, "password") ||
		strings.Contains(lowerToken, "secret") ||
		strings.Contains(lowerToken, "token") ||
		strings.Contains(lowerToken, "credential") ||
		strings.Contains(lowerToken, "password") ||
		customerPattern.MatchString(raw) ||
		customerPattern.MatchString(token) ||
		privateRepoPattern.MatchString(raw) ||
		privateRepoPattern.MatchString(token)

	if hasSensitiveMarker {
		return true
	}

	if strings.HasPrefix(raw, "./") || strings.HasPrefix(raw, "../") {
		return false
	}

	return filepath.IsAbs(token) || strings.Contains(token, "\\")
}

func ForbiddenEvidenceLeak(body string) string {
	var value any

	if err := json.Unmarshal([]byte(body), &value); err == nil {
		return ForbiddenEvidenceValue(value)
	}

	return ForbiddenStringEvidence(body)
}

func ForbiddenEvidenceValue(value any) string {
	switch typed := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}

		slices.Sort(keys)

		for _, key := range keys {
			if boolValue, ok := typed[key].(bool); ok && boolValue && ForbiddenBooleanEvidenceKey(key) {
				return key
			}

			if forbidden := ForbiddenEvidenceValue(typed[key]); forbidden != "" {
				return forbidden
			}
		}
	case []any:
		for _, item := range typed {
			if forbidden := ForbiddenEvidenceValue(item); forbidden != "" {
				return forbidden
			}
		}
	case string:
		return ForbiddenStringEvidence(typed)
	}

	return ""
}

func ForbiddenBooleanEvidenceKey(key string) bool {
	switch key {
	case "raw_prompt_captured",
		"raw_file_body_captured",
		"secret_captured",
		"secret_capture_allowed",
		"private_path_captured",
		"private_repo_url_captured",
		"unredacted_log_captured",
		"hosted_telemetry_required",
		"raw_task_captured":
		return true
	default:
		return false
	}
}

func ForbiddenStringEvidence(value string) string {
	if ContainsAbsolutePath(value) {
		return "absolute_path"
	}

	if sensitivity.ContainsSecretMarker(value) {
		return "secret_value"
	}

	lower := strings.ToLower(value)
	if strings.Contains(lower, "logs/telemetry/") ||
		strings.Contains(lower, "logs/conversations/") ||
		strings.Contains(lower, ".idiom/logs/") ||
		strings.Contains(lower, ".idiom/telemetry/") {

		return "log_path"
	}

	if customerPattern.MatchString(value) {
		return "customer_identifier"
	}

	if privateRepoPattern.MatchString(value) {
		return "private_identifier"
	}

	return ""
}

func RedactForbiddenStrings(target any) {
	redactForbiddenValue(reflect.ValueOf(target))
}

func redactForbiddenValue(value reflect.Value) {
	if !value.IsValid() {
		return
	}

	switch value.Kind() {
	case reflect.Pointer, reflect.Interface:
		if !value.IsNil() {
			redactForbiddenValue(value.Elem())
		}
	case reflect.Struct:
		for i := 0; i < value.NumField(); i++ {
			redactForbiddenValue(value.Field(i))
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < value.Len(); i++ {
			redactForbiddenValue(value.Index(i))
		}
	case reflect.Map:
		redactForbiddenMap(value)
	case reflect.String:
		if !value.CanSet() {
			return
		}

		if forbidden := ForbiddenStringEvidence(value.String()); forbidden != "" {
			value.SetString(redactedStringValue(forbidden, value.String()))
		}
	}
}

func redactForbiddenMap(value reflect.Value) {
	if value.IsNil() || !value.CanSet() {
		return
	}

	redacted := reflect.MakeMapWithSize(value.Type(), value.Len())
	for _, key := range value.MapKeys() {
		outKey := key
		if key.Kind() == reflect.String {
			if forbidden := ForbiddenStringEvidence(key.String()); forbidden != "" {
				outKey = reflect.ValueOf(redactedStringValue(forbidden, key.String())).Convert(key.Type())
			}
		}

		outValue := redactedMapValue(value.MapIndex(key))
		redacted.SetMapIndex(outKey, outValue)
	}

	value.Set(redacted)
}

func redactedMapValue(value reflect.Value) reflect.Value {
	if !value.IsValid() || !value.CanInterface() {
		return value
	}

	if value.Kind() == reflect.Interface {
		if value.IsNil() {
			return value
		}

		elem := reflect.New(value.Elem().Type()).Elem()
		elem.Set(value.Elem())
		redactForbiddenValue(elem)

		out := reflect.New(value.Type()).Elem()
		out.Set(elem)

		return out
	}

	copyValue := reflect.New(value.Type()).Elem()
	copyValue.Set(value)
	redactForbiddenValue(copyValue)

	return copyValue
}

func redactedStringValue(forbidden string, value string) string {
	return "[redacted:" + forbidden + ":" + StableID(value) + "]"
}

func ContainsAbsolutePath(value string) bool {
	for i := 0; i < len(value); i++ {
		if value[i] != '/' || !absolutePathStart(value, i) {
			continue
		}

		token := absolutePathToken(value[i:])
		if len(token) > 1 && filepath.IsAbs(token) {
			return true
		}
	}

	return false
}

func absolutePathStart(value string, index int) bool {
	if index == 0 {
		return true
	}

	prev := value[index-1]
	if prev == ':' && index+1 < len(value) && value[index+1] == '/' && looksLikeURLScheme(value[:index-1]) {
		return false
	}

	return strings.ContainsRune(" \t\r\n=:'\"`([{<,;|", rune(prev))
}

func looksLikeURLScheme(prefix string) bool {
	start := strings.LastIndexAny(prefix, " \t\r\n=\"'`([{<,;|")
	scheme := prefix[start+1:]

	if scheme == "" {
		return false
	}

	for i, r := range scheme {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			continue
		}

		if i > 0 && ((r >= '0' && r <= '9') || r == '+' || r == '-' || r == '.') {
			continue
		}

		return false
	}

	return true
}

func absolutePathToken(value string) string {
	end := len(value)
	for i, r := range value {
		if i == 0 {
			continue
		}

		if strings.ContainsRune(" \t\r\n\"'`<>", r) {
			end = i
			break
		}
	}

	return strings.Trim(value[:end], ".,:;()[]{}")
}
