package sourcekind

import (
	"path/filepath"
	"strings"
)

// CoversSourceSanity reports whether source_sanity is expected to cover path
// through either concrete diagnostics or the generic source fallback.
func CoversSourceSanity(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}

	switch strings.ToLower(filepath.Ext(path)) {
	case ".go",
		".c", ".cc", ".cpp", ".cxx", ".h", ".hh", ".hpp", ".hxx", ".ipp", ".inl", ".tpp",
		".py", ".pyi",
		".ts", ".tsx", ".mts", ".cts", ".js", ".jsx", ".mjs", ".cjs",
		".rs",
		".java",
		".gd":
		return true
	default:
		return strings.HasSuffix(strings.ToLower(path), ".d.ts") ||
			strings.HasSuffix(strings.ToLower(path), ".d.mts") ||
			strings.HasSuffix(strings.ToLower(path), ".d.cts")
	}
}

func RequiresConcreteSourceSanityDiagnostics(path string) bool {
	return IsCppSource(path)
}

func IsCppSource(path string) bool {
	switch strings.ToLower(filepath.Ext(strings.TrimSpace(path))) {
	case ".c", ".cc", ".cpp", ".cxx", ".h", ".hh", ".hpp", ".hxx", ".ipp", ".inl", ".tpp":
		return true
	default:
		return false
	}
}

func IsGenericSourceSanityFallback(summary, errText string) bool {
	text := strings.ToLower(strings.TrimSpace(summary + "\n" + errText))
	return strings.Contains(text, "generic_source_sanity") ||
		strings.Contains(text, "generic fallback") ||
		strings.Contains(text, "generic delimiter sanity")
}
