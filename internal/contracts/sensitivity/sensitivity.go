package sensitivity

import (
	"regexp"
	"strings"
)

var secretMarkerPattern = regexp.MustCompile(`(?i)(?:\b(?:secret|token|credential|password|api[_-]?key)\b\s*[:=]\s*\S+|\b(?:secret|credential|password)\b\s+\S+|authorization\s*:\s*(?:bearer|basic|token)\s+\S+|bearer\s+\S+|sk-[a-z0-9_-]{16,}|akia[0-9a-z]{12,}|ghp_[0-9a-z_]+)`)

var sensitiveTextMarkers = []string{
	"raw_prompt",
	"raw prompt",
	"raw_body",
	"raw body",
	"raw_content",
	"raw content",
	"prompt_body",
	"prompt body",
	"transcript",
	"file_body",
	"file body",
	"file-body",
	"private key",
	"secret",
	"token=",
	"token:",
	"access_token",
	"api token",
	"secret=",
	"api_key",
	"api-key",
	"password=",
	"bearer ",
	"customer-",
	"customer_",
	"secrets/",
	"/secrets/",
	"\\secrets\\",
}

func ContainsSecretMarker(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}

	return secretMarkerPattern.MatchString(value)
}

func ContainsSensitiveText(value string) bool {
	lower := strings.ToLower(value)
	for _, marker := range sensitiveTextMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}

	if ContainsSecretMarker(value) {
		return true
	}

	if ContainsSensitiveWord(lower, "token") ||
		ContainsSensitiveWord(lower, "tokens") ||
		ContainsSensitiveWord(lower, "customer") ||
		ContainsSensitiveWord(lower, "customers") {
		return true
	}

	return ContainsLocalPrivatePath(lower)
}

func ContainsSensitiveWord(value string, word string) bool {
	for _, token := range strings.FieldsFunc(value, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	}) {
		if token == word {
			return true
		}
	}

	return false
}

func ContainsLocalPrivatePath(value string) bool {
	normalized := strings.ReplaceAll(strings.ToLower(value), "\\", "/")
	for _, marker := range []string{
		"/users/",
		"/home/",
		"/volumes/",
		"/private/",
		"/var/folders/",
		"c:/users/",
		"~/",
		"/customer",
		"/customers/",
	} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}

	return false
}
