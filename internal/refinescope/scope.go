package refinescope

import (
	"path/filepath"
	"strings"
)

const (
	ModeAnchored = "anchored"
	ModeMarkdown = "markdown"
)

var sourceExtensions = []string{
	".go", ".cpp", ".cc", ".cxx", ".c", ".h", ".hpp", ".hh",
	".py", ".rs", ".ts", ".tsx", ".js", ".jsx", ".java", ".kt",
	".rb", ".php", ".swift", ".scala", ".cs", ".m", ".mm",
	".sh", ".bash", ".zsh", ".fish",
	".sql", ".psql", ".plsql",
	".proto", ".tf", ".hcl", ".bicep",
}

var markdownExtensions = []string{".md", ".txt", ".json", ".yaml", ".yml", ".toml"}

var knownFileBasenames = map[string]bool{
	"Dockerfile":         true,
	"Makefile":           true,
	"README":             true,
	"LICENSE":            true,
	".dockerignore":      true,
	".editorconfig":      true,
	".env":               true,
	".env.example":       true,
	".gitignore":         true,
	".gitattributes":     true,
	".prettierrc":        true,
	".stylelintrc":       true,
	"AGENTS.md":          true,
	"CLAUDE.md":          true,
	"CODEOWNERS":         true,
	"CONTRIBUTING.md":    true,
	"CHANGELOG.md":       true,
	"SECURITY.md":        true,
	"docker-compose":     true,
	"docker-compose.yml": true,
}

func DetectMode(specBody string) string {
	lower := strings.ToLower(specBody)
	hasSource := false
	hasMarkdown := false
	for _, ext := range sourceExtensions {
		if ExtensionMentioned(lower, ext) {
			hasSource = true
			break
		}
	}
	for _, ext := range markdownExtensions {
		if ExtensionMentioned(lower, ext) {
			hasMarkdown = true
			break
		}
	}
	if hasMarkdown && !hasSource {
		return ModeMarkdown
	}
	return ModeAnchored
}

func PathLooksLikeSource(path string) bool {
	lower := strings.ToLower(path)
	for _, ext := range sourceExtensions {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}

func PathLooksLikeFile(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}
	if strings.ContainsAny(path, "\n\r\t <>|*?\"{}:") {
		return false
	}
	slashed := filepath.ToSlash(path)
	base := filepath.Base(slashed)
	if strings.ContainsAny(path, "/\\") {
		return filepath.Ext(base) != "" || knownFileBasenames[base]
	}
	if strings.HasPrefix(path, ".") {
		return true
	}
	if ext := filepath.Ext(path); ext != "" {
		return KnownFileExtension(ext)
	}
	return knownFileBasenames[path]
}

func KnownFileExtension(ext string) bool {
	ext = strings.ToLower(strings.TrimSpace(ext))
	if ext == "" {
		return false
	}
	for _, known := range sourceExtensions {
		if ext == known {
			return true
		}
	}
	for _, known := range markdownExtensions {
		if ext == known {
			return true
		}
	}
	switch ext {
	case ".jsonl", ".mdx", ".markdown", ".html", ".htm", ".css", ".scss", ".sass",
		".svg", ".png", ".jpg", ".jpeg", ".gif", ".webp", ".ico",
		".mod", ".sum", ".lock", ".ini", ".cfg", ".conf", ".env", ".ps1",
		".gradle", ".properties", ".toml", ".xml", ".csv", ".tsv":
		return true
	default:
		return false
	}
}

func KnownFileBasename(path string) bool {
	return knownFileBasenames[strings.TrimSpace(path)]
}

func ExtensionMentioned(text, ext string) bool {
	start := 0
	for {
		idx := strings.Index(text[start:], ext)
		if idx < 0 {
			return false
		}
		pos := start + idx + len(ext)
		if pos >= len(text) {
			return true
		}
		next := rune(text[pos])
		if next < 'a' || next > 'z' {
			return true
		}
		start += idx + len(ext)
	}
}
