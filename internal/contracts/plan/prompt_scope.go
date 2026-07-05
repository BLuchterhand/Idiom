package plan

import (
	"strings"
	"unicode"

	"idiom/internal/contracts/pathparse"
)

func RequiredEditPathsFromPrompt(prompt string) []string {
	prompt = pathparse.ScopeToBriefBody(prompt)

	paths := requiredEditPathsUnderPromptHeadings(prompt, requiredEditPathHeadings(), true)

	paths = appendUniquePromptScopeStrings(paths, pathparse.FilterWorkspaceFilePaths(structuredRequiredEditPathsFromPrompt(prompt))...)
	paths = appendUniquePromptScopeStrings(paths, inlineRequiredEditPathsFromPrompt(prompt)...)

	return paths
}

func AllowedEditPathsFromPrompt(prompt string) []string {
	prompt = pathparse.ScopeToBriefBody(prompt)

	paths := appendUniquePromptScopeStrings(
		requiredEditPathsUnderPromptHeadings(prompt, allowedEditPathHeadings(), false),
		structuredAllowedEditPathsFromPrompt(prompt)...,
	)

	paths = appendUniquePromptScopeStrings(paths, inlineAllowedEditPathsFromPrompt(prompt)...)

	return paths
}

func inlineAllowedEditPathsFromPrompt(prompt string) []string {
	text := pathparse.ScopeToBriefBody(prompt)

	var paths []string

	for _, line := range strings.Split(text, "\n") {
		for _, segment := range inlineAllowedScopeSegments(line) {
			words := promptWordText(segment)
			for _, phrase := range inlineAllowedScopePhrases() {
				if strings.Contains(words, phrase) {
					paths = appendUniquePromptScopeStrings(paths, pathparse.MentionedWorkspacePaths(segment)...)
					break
				}
			}
		}
	}

	return paths
}

func inlineAllowedScopeSegments(line string) []string {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}

	replacer := strings.NewReplacer(". ", "\n", "? ", "\n", "! ", "\n")

	return strings.Split(replacer.Replace(line), "\n")
}

func inlineAllowedScopePhrases() []string {
	return []string{
		" only edit ",
		" only modify ",
		" only change ",
		" edit only ",
		" modify only ",
		" change only ",
		" limit edits to ",
		" restrict edits to ",
		" limit changes to ",
		" restrict changes to ",
	}
}

func inlineRequiredEditPathsFromPrompt(prompt string) []string {
	text := pathparse.ScopeToBriefBody(prompt)

	var paths []string

	for _, rawLine := range strings.Split(text, "\n") {
		line := strings.TrimSpace(rawLine)
		lower := strings.ToLower(line)

		for _, prefix := range []string{
			"required edit paths:",
			"required edit paths only:",
			"required files:",
			"files to modify:",
			"files to edit:",
		} {
			idx := strings.Index(lower, prefix)
			if idx < 0 {
				continue
			}

			value := strings.TrimSpace(line[idx+len(prefix):])
			if value == "" {
				continue
			}

			paths = appendUniquePromptScopeStrings(paths, pathparse.MentionedWorkspaceFilesInInlineList(value)...)
		}
	}

	return paths
}

func promptWordText(prompt string) string {
	prompt = strings.TrimSpace(strings.ToLower(prompt))
	if prompt == "" {
		return " "
	}

	var b strings.Builder

	b.Grow(len(prompt) + 2)
	b.WriteByte(' ')

	lastSpace := true

	for _, r := range prompt {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)

			lastSpace = false

			continue
		}

		if !lastSpace {
			b.WriteByte(' ')

			lastSpace = true
		}
	}

	if !lastSpace {
		b.WriteByte(' ')
	}

	return b.String()
}

func requiredEditPathHeadings() []string {
	return []string{
		"files to modify",
		"files-to-modify",
		"files to edit",
		"edit files",
		"required edit paths",
		"required edit paths only",
		"required files",
		"required paths",
	}
}

func allowedEditPathHeadings() []string {
	return []string{
		"candidate edit scope",
		"candidate edit scope from target references",
		"candidate paths",
		"candidate files",
		"editable files",
		"files you may edit",
		"files you may modify",
		"files you can edit",
		"write scope",
		"allowed files",
		"allowed edit files",
		"allowed edit paths",
		"allowed source files",
		"edit allowlist",
		"file allowlist",
		"files allowlist",
		"source allowlist",
	}
}

func requiredEditPathsUnderPromptHeadings(prompt string, headings []string, fileOnly bool) []string {
	headingSet := promptHeadingSet(headings)

	var paths []string

	active := false
	sawPath := false

	for _, rawLine := range strings.Split(prompt, "\n") {
		line := strings.TrimSpace(rawLine)
		if _, ok := headingSet[normalizePromptSectionHeading(line)]; ok {
			active = true
			sawPath = false

			continue
		}

		if !active {
			continue
		}

		if line == "" {
			if sawPath {
				active = false
			}

			continue
		}

		if strings.HasPrefix(line, "|") && strings.Count(line, "|") >= 2 {
			linePaths := pathparse.MentionedWorkspacePathsInMarkdownTableFirstCell(line)
			if fileOnly {
				linePaths = pathparse.FilterWorkspaceFilePaths(linePaths)
			}

			if len(linePaths) > 0 {
				sawPath = true
				paths = appendUniquePromptScopeStrings(paths, linePaths...)
			}

			continue
		}

			linePaths := PromptWorkspacePaths(line)
		if fileOnly {
			linePaths = pathparse.FilterWorkspaceFilePaths(linePaths)
		}

		if len(linePaths) > 0 {
			sawPath = true
			paths = appendUniquePromptScopeStrings(paths, linePaths...)

			continue
		}

		if looksLikePromptSectionBoundary(line) {
			if !sawPath && looksLikeRequiredPathPrelude(line) {
				continue
			}

			active = false

			continue
		}
	}

	return paths
}

func structuredRequiredEditPathsFromPrompt(prompt string) []string {
	return structuredEditPathsFromPrompt(prompt, []string{
		"files_to_modify",
		"files_to_edit",
		"required_edit_paths",
		"required_edit_paths_only",
	})
}

func structuredAllowedEditPathsFromPrompt(prompt string) []string {
	return structuredEditPathsFromPrompt(prompt, []string{
		"editable_files",
		"allowed_edit_paths",
		"allowed_files",
		"write_scope",
	})
}

func structuredEditPathsFromPrompt(prompt string, keys []string) []string {
	prompt = pathparse.ScopeToBriefBody(prompt)

	var paths []string

	active := false
	activeArray := false

	for _, rawLine := range strings.Split(prompt, "\n") {
		line := strings.TrimSpace(rawLine)
		started := false

		if !active && structuredEditPathKeyLine(line, keys) {
			active = true
			started = true
			activeArray = structuredEditPathLineStartsArray(line)
		}

		if !active {
			continue
		}

		scanLine := line
		if started {
			scanLine = structuredEditPathInlineValue(line)
		}

		linePaths := PromptWorkspacePaths(scanLine)
		if len(linePaths) > 0 {
			paths = appendUniquePromptScopeStrings(paths, linePaths...)
		}

		if started && !activeArray {
			if len(linePaths) > 0 || strings.Contains(line, "]") || strings.Contains(line, "}") {
				active = false
				activeArray = false
			}

			continue
		}

		if structuredEditPathBlockEnded(line, activeArray) {
			active = false
			activeArray = false
		}
	}

	return paths
}

func structuredEditPathKeyLine(line string, keys []string) bool {
	normalized := normalizeStructuredEditPathKeyPrefix(line)

	for _, key := range keys {
		switch {
		case normalized == key:
			return true
		case strings.HasPrefix(normalized, key+":") || strings.HasPrefix(normalized, key+"="):
			return true
		case strings.HasPrefix(normalized, `"`+key+`":`) || strings.HasPrefix(normalized, `"`+key+`"=`):
			return true
		case strings.HasPrefix(normalized, `'`+key+`':`) || strings.HasPrefix(normalized, `'`+key+`'=`):
			return true
		}
	}

	return false
}

func normalizeStructuredEditPathKeyPrefix(line string) string {
	normalized := strings.ToLower(strings.NewReplacer("-", "_", " ", "_").Replace(strings.TrimSpace(line)))

	normalized = strings.TrimLeft(normalized, "{[, ")

	if strings.HasPrefix(normalized, "-_") {
		normalized = strings.TrimSpace(strings.TrimPrefix(normalized, "-_"))
	}

	return normalized
}

func structuredEditPathLineStartsArray(line string) bool {
	value := structuredEditPathInlineRawValue(line)

	value = strings.TrimSpace(value)

	return strings.HasPrefix(value, "[") && structuredInlineClosingIndex(value, '[', ']') < 0
}

func structuredEditPathInlineValue(line string) string {
	return structuredEditPathValueSegment(structuredEditPathInlineRawValue(line))
}

func structuredEditPathInlineRawValue(line string) string {
	trimmed := strings.TrimSpace(line)

	trimmed = strings.TrimLeft(trimmed, "{[, ")

	if strings.HasPrefix(trimmed, "- ") {
		trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))
	}

	colon := strings.Index(trimmed, ":")
	equals := strings.Index(trimmed, "=")

	switch {
	case colon < 0 && equals < 0:
		return ""
	case colon < 0:
		return trimmed[equals+1:]
	case equals < 0:
		return trimmed[colon+1:]
	case colon < equals:
		return trimmed[colon+1:]
	default:
		return trimmed[equals+1:]
	}
}

func structuredEditPathValueSegment(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}

	switch value[0] {
	case '[':
		if end := structuredInlineClosingIndex(value, '[', ']'); end >= 0 {
			return strings.TrimSpace(value[:end+1])
		}

		return value
	case '{':
		if end := structuredInlineClosingIndex(value, '{', '}'); end >= 0 {
			return strings.TrimSpace(value[:end+1])
		}

		return value
	case '"', '\'':
		if end := structuredInlineQuoteClosingIndex(value, value[0]); end >= 0 {
			return strings.TrimSpace(value[:end+1])
		}

		return value
	default:
		end := len(value)
		if comma := strings.Index(value, ","); comma >= 0 && comma < end {
			end = comma
		}

		if comment := strings.Index(value, " #"); comment >= 0 && comment < end {
			end = comment
		}

		return strings.TrimSpace(value[:end])
	}
}

func structuredInlineClosingIndex(value string, open byte, close byte) int {
	depth := 0

	var quote byte

	escaped := false

	for i := 0; i < len(value); i++ {
		ch := value[i]

		if quote != 0 {
			if escaped {
				escaped = false
				continue
			}

			if ch == '\\' {
				escaped = true
				continue
			}

			if ch == quote {
				quote = 0
			}

			continue
		}

		if ch == '"' || ch == '\'' {
			quote = ch
			continue
		}

		if ch == open {
			depth++
			continue
		}

		if ch != close {
			continue
		}

		depth--
		if depth == 0 {
			return i
		}
	}

	return -1
}

func structuredInlineQuoteClosingIndex(value string, quote byte) int {
	escaped := false

	for i := 1; i < len(value); i++ {
		ch := value[i]

		if escaped {
			escaped = false
			continue
		}

		if ch == '\\' {
			escaped = true
			continue
		}

		if ch == quote {
			return i
		}
	}

	return -1
}

func structuredEditPathBlockEnded(line string, activeArray bool) bool {
	trimmed := strings.TrimSpace(strings.TrimRight(line, ","))
	if activeArray {
		return trimmed == "" || strings.Contains(trimmed, "]") || looksLikePromptSectionBoundary(trimmed)
	}

	return trimmed == "" ||
		strings.Contains(trimmed, "]") ||
		strings.Contains(trimmed, "}") ||
		looksLikePromptSectionBoundary(trimmed)
}

func looksLikePromptSectionBoundary(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}

	if strings.HasPrefix(trimmed, "#") {
		return true
	}

	if strings.HasPrefix(trimmed, "**") && strings.Contains(strings.TrimPrefix(trimmed, "**"), "**") {
		return true
	}

	return strings.HasSuffix(trimmed, ":")
}

func looksLikeMarkdownPromptSectionBoundary(line string) bool {
	trimmed := strings.TrimSpace(line)

	return strings.HasPrefix(trimmed, "#") ||
		(strings.HasPrefix(trimmed, "**") && strings.Contains(strings.TrimPrefix(trimmed, "**"), "**"))
}

func looksLikeRequiredPathPrelude(line string) bool {
	if !looksLikePromptSectionBoundary(line) || looksLikeMarkdownPromptSectionBoundary(line) {
		return false
	}

	normalized := normalizePromptSectionHeading(line)

	return strings.Contains(normalized, "allowlist") ||
		strings.Contains(normalized, "scope") ||
		strings.Contains(normalized, "files") ||
		strings.Contains(normalized, "paths") ||
		strings.Contains(normalized, "table")
}

func promptHeadingSet(headings []string) map[string]struct{} {
	headingSet := make(map[string]struct{}, len(headings))
	for _, heading := range headings {
		if normalized := normalizePromptSectionHeading(heading); normalized != "" {
			headingSet[normalized] = struct{}{}
		}
	}

	return headingSet
}

// PromptWorkspacePaths returns the workspace path references recognized by
// prompt packet parsing, including quoted fragments that are valid workspace
// path tokens.
func PromptWorkspacePaths(text string) []string {
	paths := appendUniquePromptScopeStrings(nil, pathparse.MentionedWorkspacePaths(text)...)
	for _, fragment := range pathparse.QuotedFragments(text) {
		path, ok := pathparse.NormalizeWorkspacePathToken(fragment)
		if !ok {
			continue
		}

		paths = appendUniquePromptScopeStrings(paths, path)
	}

	return paths
}

func normalizePromptSectionHeading(text string) string {
	text = strings.TrimSpace(text)
	text = strings.Trim(text, "#*: `\"'")
	text = strings.TrimSuffix(text, ":")
	text = strings.ToLower(text)
	text = strings.NewReplacer("-", " ", "_", " ", "/", " ").Replace(text)

	return strings.Join(strings.Fields(text), " ")
}

func appendUniquePromptScopeStrings(dst []string, values ...string) []string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}

		seen := false

		for _, existing := range dst {
			if existing == value {
				seen = true
				break
			}
		}

		if !seen {
			dst = append(dst, value)
		}
	}

	return dst
}
