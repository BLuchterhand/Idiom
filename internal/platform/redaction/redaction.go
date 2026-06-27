package redaction

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
)

var (
	apiKeyPattern    = regexp.MustCompile(`(?i)\b(api[_-]?key|token|secret|password)\b\s*[:=]\s*([^\s,;]+)`)
	bearerPattern    = regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._-]+\b`)
	openAIKeyPattern = regexp.MustCompile(`\bsk-[A-Za-z0-9]{12,}\b`)
)

func RedactSecrets(text string) string {
	text = apiKeyPattern.ReplaceAllString(text, `$1=<redacted>`)
	text = bearerPattern.ReplaceAllString(text, `Bearer <redacted>`)
	text = openAIKeyPattern.ReplaceAllString(text, `<redacted-secret>`)

	return text
}

func RedactedValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "<empty>"
	}

	sum := sha256.Sum256([]byte(value))

	return fmt.Sprintf("<redacted len=%d sha256:%s>", len(value), hex.EncodeToString(sum[:4]))
}
