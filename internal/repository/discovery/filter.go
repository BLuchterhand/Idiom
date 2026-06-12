package discovery

import (
	"path/filepath"
	"strings"
)

var noisyDirNames = map[string]struct{}{
	".cache":        {},
	".claude":       {},
	".idiom":        {},
	".git":          {},
	".hg":           {},
	".mypy_cache":   {},
	".next":         {},
	".planning":     {},
	".pytest_cache": {},
	".ruff_cache":   {},
	".svn":          {},
	".tox":          {},
	".turbo":        {},
	".venv":         {},
	"__pycache__":   {},
	"bin":           {},
	"build":         {},
	"coverage":      {},
	"dist":          {},
	"logs":          {},
	"models":        {},
	"node_modules":  {},
	"target":        {},
	"tmp":           {},
	"transcripts":   {},
	"vendor":        {},
	"venv":          {},
}

var noisyRelFiles = map[string]struct{}{
	"testdata/swe/representative-suite.json": {},
}

var noisyRelDirPrefixes = []string{
	"testdata/swe/generated-logs/",
}

// ShouldSkipDirName reports whether directory name is normally too noisy for
// model-facing discovery. Explicitly targeted paths may still inspect these.
func ShouldSkipDirName(name string) bool {
	_, ok := noisyDirNames[name]
	return ok
}

// RelTargetsNoisyDir reports whether a user-supplied relative path explicitly
// starts inside a normally skipped directory. Callers use this to allow
// targeted inspection while keeping broad discovery bounded.
func RelTargetsNoisyDir(rel string) bool {
	rel = NormalizeRel(rel)
	if rel == "" || rel == "." {
		return false
	}

	first := strings.Split(rel, "/")[0]

	return ShouldSkipDirName(first)
}

// ShouldSkipRelFile reports whether a workspace-relative file should be
// omitted from default discovery because it is an oracle, generated output, or
// other benchmark artifact that can falsely satisfy app-code searches.
func ShouldSkipRelFile(rel string) bool {
	rel = NormalizeRel(rel)
	if _, ok := noisyRelFiles[rel]; ok {
		return true
	}

	for _, prefix := range noisyRelDirPrefixes {
		if strings.HasPrefix(rel, prefix) {
			return true
		}
	}

	return false
}

// ShouldSkipPath applies the default discovery filter to a path under root.
// The root itself is never skipped, so an explicit path to a noisy directory
// can still be listed or searched by callers that set root to that directory.
func ShouldSkipPath(root string, path string, isDir bool) bool {
	if filepath.Clean(path) == filepath.Clean(root) {
		return false
	}

	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}

	rel = NormalizeRel(rel)
	if rel == "." || rel == "" {
		return false
	}

	if isDir {
		return ShouldSkipDirName(filepath.Base(rel))
	}

	return ShouldSkipRelFile(rel)
}

func NormalizeRel(rel string) string {
	return filepath.ToSlash(filepath.Clean(strings.TrimSpace(rel)))
}

func RipgrepExcludeGlobs() []string {
	return []string{
		"!**/.cache/**",
		"!**/.claude/**",
		"!**/.idiom/**",
		"!**/.git/**",
		"!**/.hg/**",
		"!**/.mypy_cache/**",
		"!**/.next/**",
		"!**/.planning/**",
		"!**/.pytest_cache/**",
		"!**/.ruff_cache/**",
		"!**/.svn/**",
		"!**/.tox/**",
		"!**/.turbo/**",
		"!**/.venv/**",
		"!**/__pycache__/**",
		"!**/bin/**",
		"!**/build/**",
		"!**/coverage/**",
		"!**/dist/**",
		"!**/logs/**",
		"!**/models/**",
		"!**/node_modules/**",
		"!**/target/**",
		"!**/tmp/**",
		"!**/transcripts/**",
		"!**/vendor/**",
		"!**/venv/**",
		"!testdata/swe/representative-suite.json",
		"!**/testdata/swe/generated-logs/**",
	}
}
