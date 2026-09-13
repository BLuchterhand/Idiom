package redaction

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

const credentialName = `(?:[a-z0-9]+[_-])*(?:api[_-]?key|token|secret|password|passwd)`

var (
	quotedKeyPattern       = regexp.MustCompile(`(?i)(["'](?:` + credentialName + `|authorization)["']\s*:\s*)("(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*')`)
	apiKeyPattern          = regexp.MustCompile(`(?i)\b(` + credentialName + `)\b\s*[:=]\s*("(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|[^\s,;]+)`)
	bearerPattern          = regexp.MustCompile(`(?i)\b(Bearer|Basic)\s+[A-Za-z0-9._~+/=-]+`)
	openAIKeyPattern       = regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{12,}\b`)
	urlUserPattern         = regexp.MustCompile(`(?i)\b(https?://)[^/\s@]+@`)
	credentialFieldPattern = regexp.MustCompile(`(?i)^(?:` + credentialName + `|authorization)$`)
)

func RedactSecrets(text string) string {
	text = quotedKeyPattern.ReplaceAllString(text, `${1}"<redacted>"`)
	text = apiKeyPattern.ReplaceAllString(text, `$1=<redacted>`)
	text = bearerPattern.ReplaceAllStringFunc(text, func(match string) string {
		if strings.EqualFold(strings.Fields(match)[0], "Basic") {
			return "Basic <redacted>"
		}
		return "Bearer <redacted>"
	})
	text = openAIKeyPattern.ReplaceAllString(text, `<redacted-secret>`)
	text = urlUserPattern.ReplaceAllString(text, `${1}<redacted>@`)

	return text
}

// MarshalJSON redacts credentials at the persistence boundary, including nested
// structs and typed collections. Numbers retain their original precision and the
// caller's values are never mutated. This is best-effort secret filtering, not
// a substitute for excluding raw prompts and source bodies from evidence.
func MarshalJSON(value any) ([]byte, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var document any
	if err := decoder.Decode(&document); err != nil {
		return nil, err
	}
	return json.Marshal(redactJSONValue("", document))
}

func redactJSONValue(key string, value any) any {
	switch typed := value.(type) {
	case map[string]any:
		for name, item := range typed {
			typed[name] = redactJSONValue(name, item)
		}
	case []any:
		for i, item := range typed {
			typed[i] = redactJSONValue(key, item)
		}
	case string:
		return RedactField(key, typed)
	}
	return value
}

// RedactField also protects bare credential values identified by their field
// name, which cannot be recognized from the value alone.
func RedactField(key, value string) string {
	if value != "" && credentialFieldPattern.MatchString(strings.TrimSpace(key)) {
		return "<redacted>"
	}
	return RedactSecrets(value)
}

func RedactedValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "<empty>"
	}

	sum := sha256.Sum256([]byte(value))

	return fmt.Sprintf("<redacted len=%d sha256:%s>", len(value), hex.EncodeToString(sum[:4]))
}
