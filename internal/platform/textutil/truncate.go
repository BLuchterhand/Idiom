package textutil

import "unicode/utf8"

func TruncateBytes(s string, maxBytes int, marker string) string {
	if maxBytes <= 0 || len(s) <= maxBytes {
		return s
	}

	if maxBytes <= len(marker) {
		return utf8SafePrefix(s, maxBytes)
	}

	return utf8SafePrefix(s, maxBytes-len(marker)) + marker
}

func TruncateRunes(s string, maxRunes int, marker string) string {
	if maxRunes <= 0 || utf8.RuneCountInString(s) <= maxRunes {
		return s
	}

	if maxRunes <= utf8.RuneCountInString(marker) {
		runes := []rune(s)
		return string(runes[:maxRunes])
	}

	targetRunes := maxRunes - utf8.RuneCountInString(marker)
	runes := []rune(s)

	return string(runes[:targetRunes]) + marker
}

func Truncate(s string, maxBytes int) string {
	return TruncateBytes(s, maxBytes, "...")
}

func utf8SafePrefix(s string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}

	if len(s) <= maxBytes {
		return s
	}

	end := 0
	for i := range s {
		if i > maxBytes {
			break
		}

		end = i
	}

	return s[:end]
}
