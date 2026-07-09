package textutil

import (
	"fmt"
)

func TruncateBytes(s string, maxBytes int, marker string) string {
	if maxBytes <= 0 || len(s) <= maxBytes {
		return s
	}

	if maxBytes <= len(marker) {
		return utf8SafePrefix(s, maxBytes)
	}

	return utf8SafePrefix(s, maxBytes-len(marker)) + marker
}

func Truncate(s string, maxBytes int) string {
	return TruncateBytes(s, maxBytes, "...")
}

func TruncateWithByteCount(s string, maxBytes int) string {
	if maxBytes <= 0 || len(s) <= maxBytes {
		return s
	}

	return utf8SafePrefix(s, maxBytes) + fmt.Sprintf("\n\n[truncated %d bytes]\n", len(s)-maxBytes)
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
