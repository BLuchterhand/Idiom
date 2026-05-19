package planexecute

import (
	"strings"

	"idiom/internal/frontier/pathparse"
	"idiom/internal/util"
)

func RequiredEditPathsFromPrompt(prompt string) []string {
	return requiredEditPathsFromPrompt(prompt)
}

func AllowedEditPathsFromPrompt(prompt string) []string {
	return allowedEditPathsFromPrompt(prompt)
}

func requiredEditPathsFromPrompt(prompt string) []string {
	prompt = pathparse.ScopeToBriefBody(prompt)
	return appendUniqueBriefStrings(
		requiredEditPathsUnderPromptHeadings(prompt, requiredEditPathHeadings()),
		structuredRequiredEditPathsFromPrompt(prompt)...,
	)
}

func allowedEditPathsFromPrompt(prompt string) []string {
	prompt = pathparse.ScopeToBriefBody(prompt)
	return appendUniqueBriefStrings(
		requiredEditPathsUnderPromptHeadings(prompt, allowedEditPathHeadings()),
		structuredAllowedEditPathsFromPrompt(prompt)...,
	)
}

func requiredEditPathHeadings() []string {
	return []string{
		"files to modify",
		"files-to-modify",
		"files to edit",
		"edit files",
		"editable files",
		"required edit paths",
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

func requiredEditPathsUnderPromptHeadings(prompt string, headings []string) []string {
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
			if len(linePaths) > 0 {
				sawPath = true
				paths = appendUniqueBriefStrings(paths, linePaths...)
			}
			continue
		}
		linePaths := promptPacketWorkspacePaths(line)
		if len(linePaths) > 0 {
			sawPath = true
			paths = appendUniqueBriefStrings(paths, linePaths...)
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
		"editable_files",
	})
}

func structuredAllowedEditPathsFromPrompt(prompt string) []string {
	return structuredEditPathsFromPrompt(prompt, []string{
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
		linePaths := promptPacketWorkspacePaths(scanLine)
		if len(linePaths) > 0 {
			paths = appendUniqueBriefStrings(paths, linePaths...)
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

func promptPacketWorkspacePaths(text string) []string {
	paths := appendUniqueBriefStrings(nil, pathparse.MentionedWorkspacePaths(text)...)
	for _, fragment := range pathparse.QuotedFragments(text) {
		path, ok := pathparse.NormalizeWorkspacePathToken(fragment)
		if !ok {
			continue
		}
		paths = appendUniqueBriefStrings(paths, path)
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

func promptHeadingInSet(candidate string, headingSet map[string]struct{}) bool {
	if _, ok := headingSet[candidate]; ok {
		return true
	}
	if trimmed := trimPromptHeadingQualifier(candidate); trimmed != candidate {
		_, ok := headingSet[trimmed]
		return ok
	}
	return false
}

func trimPromptHeadingQualifier(heading string) string {
	heading = strings.TrimSpace(heading)
	for _, marker := range []string{" (", " [", " - ", " -- "} {
		if idx := strings.Index(heading, marker); idx > 0 {
			return strings.TrimSpace(heading[:idx])
		}
	}
	return heading
}

func appendUniqueBriefStrings(dst []string, values ...string) []string {
	for _, v := range values {
		dst = util.AppendUnique(dst, v)
	}
	return dst
}
