package recovery

import (
	"strings"

	"idiom/internal/contracts/sensitivity"
)

func RedactText(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}

	value = redactFencedRecoveryBlocks(value)

	lines := strings.Split(value, "\n")
	for i, line := range lines {
		if looksSensitiveRecoveryLine(line) {
			lines[i] = "[redacted sensitive recovery detail]"
		}
	}

	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func redactFencedRecoveryBlocks(value string) string {
	lines := strings.Split(value, "\n")
	inFence := false
	redactedFence := false

	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			if !inFence {
				inFence = true
				redactedFence = false

				continue
			}

			inFence = false

			continue
		}

		if inFence {
			if !redactedFence {
				lines[i] = "[redacted fenced recovery detail]"
				redactedFence = true
			} else {
				lines[i] = ""
			}
		}
	}

	return strings.Join(lines, "\n")
}

func looksSensitiveRecoveryLine(line string) bool {
	lower := strings.ToLower(line)
	if strings.Contains(lower, "-----begin ") && strings.Contains(lower, "private key") {
		return true
	}

	if strings.Contains(lower, "raw_prompt") ||
		strings.Contains(lower, "secret_prompt") ||
		strings.Contains(lower, "transcript") ||
		strings.Contains(lower, "file_body") ||
		strings.Contains(lower, "file body") ||
		strings.Contains(lower, "protected content") {

		return true
	}

	if sensitivity.ContainsSecretMarker(line) {
		return true
	}

	hasAssignment := strings.Contains(line, "=") || strings.Contains(line, ":")
	if !hasAssignment {
		return false
	}

	for _, indicator := range []string{
		"api_key",
		"api key",
		"apikey",
		"access_token",
		"refresh_token",
		"auth_token",
		"password",
		"passwd",
		"credential",
		"secret",
		"old_text",
		"new_text",
		"prompt",
	} {
		if strings.Contains(lower, indicator) {
			return true
		}
	}

	return false
}
