package workspace

import (
	pathpkg "path"
	"path/filepath"
	"strings"
)

type toolRelationshipEndpoint struct {
	Path   string
	Line   int
	Symbol string
}

func annotateLanguageRelationships(result ToolResult) ToolResult {
	if len(result.Relationships) > 0 || strings.TrimSpace(result.Error) != "" {
		return result
	}

	kind, direction := relationshipKindForTool(result.Tool)
	if kind == "" || strings.TrimSpace(result.Output) == "" {
		return result
	}

	for _, rawLine := range strings.Split(result.Output, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}

		relationship, ok := relationshipFromToolLine(kind, direction, line)
		if ok {
			result.Relationships = append(result.Relationships, relationship)
		}
	}

	return result
}

func relationshipKindForTool(tool string) (string, string) {
	tool = strings.ToLower(strings.TrimSpace(tool))
	switch {
	case strings.HasSuffix(tool, "_calls_from"):
		return "calls", "outgoing"
	case strings.HasSuffix(tool, "_calls_into"):
		return "calls", "incoming"
	case strings.HasSuffix(tool, "_references"):
		return "references", "incoming"
	case strings.HasSuffix(tool, "_implementations"):
		return "implements", "incoming"
	default:
		return "", ""
	}
}

func relationshipFromToolLine(kind string, direction string, line string) (ToolRelationship, bool) {
	switch direction {
	case "outgoing":
		left, right, ok := splitRelationshipArrow(line)
		if !ok {
			return ToolRelationship{}, false
		}

		source := parseToolRelationshipEndpoint(left)
		target := parseToolRelationshipEndpoint(right)

		if source.Path == "" && source.Symbol == "" {
			return ToolRelationship{}, false
		}

		if target.Path == "" && target.Symbol == "" {
			return ToolRelationship{}, false
		}

		return ToolRelationship{
			Kind:         kind,
			SourcePath:   source.Path,
			SourceLine:   source.Line,
			SourceSymbol: source.Symbol,
			TargetPath:   target.Path,
			TargetLine:   target.Line,
			TargetSymbol: target.Symbol,
			Evidence:     line,
		}, true
	case "incoming":
		left, right, ok := splitRelationshipArrow(line)
		source := parseToolRelationshipEndpoint(line)
		target := toolRelationshipEndpoint{}

		if ok {
			source = parseToolRelationshipEndpoint(left)
			target = parseToolRelationshipEndpoint(right)
		}

		if source.Path == "" && source.Symbol == "" {
			return ToolRelationship{}, false
		}

		return ToolRelationship{
			Kind:         kind,
			SourcePath:   source.Path,
			SourceLine:   source.Line,
			SourceSymbol: source.Symbol,
			TargetPath:   target.Path,
			TargetLine:   target.Line,
			TargetSymbol: target.Symbol,
			Evidence:     line,
		}, true
	default:
		return ToolRelationship{}, false
	}
}

func splitRelationshipArrow(line string) (string, string, bool) {
	left, right, ok := strings.Cut(line, "->")
	if !ok {
		return "", "", false
	}

	return strings.TrimSpace(left), strings.TrimSpace(right), true
}

func parseToolRelationshipEndpoint(text string) toolRelationshipEndpoint {
	text = strings.TrimSpace(text)
	if text == "" {
		return toolRelationshipEndpoint{}
	}

	if name, location, ok := strings.Cut(text, " in "); ok {
		endpoint := parseToolRelationshipEndpoint(location)
		if endpoint.Symbol == "" {
			endpoint.Symbol = firstRelationshipName(name)
		}

		return endpoint
	}

	if path, line, rest, ok := parseRelationshipLocation(text); ok {
		return toolRelationshipEndpoint{
			Path:   path,
			Line:   line,
			Symbol: firstRelationshipName(rest),
		}
	}

	fields := strings.Fields(text)
	if len(fields) >= 2 && looksLikeRelationshipPath(fields[0]) {
		return toolRelationshipEndpoint{
			Path:   cleanRelationshipPath(fields[0]),
			Symbol: firstRelationshipName(strings.Join(fields[1:], " ")),
		}
	}

	return toolRelationshipEndpoint{Symbol: firstRelationshipName(text)}
}

func parseRelationshipLocation(text string) (string, int, string, bool) {
	for i := 0; i < len(text); i++ {
		if text[i] != ':' {
			continue
		}

		j := i + 1
		for j < len(text) && text[j] >= '0' && text[j] <= '9' {
			j++
		}

		if j == i+1 {
			continue
		}

		line := 0
		for _, ch := range text[i+1 : j] {
			line = line*10 + int(ch-'0')
		}

		k := j
		if k < len(text) && text[k] == ':' {
			k++
			for k < len(text) && text[k] >= '0' && text[k] <= '9' {
				k++
			}
		}

		path := cleanRelationshipPath(text[:i])
		if path == "" || !looksLikeRelationshipPath(path) {
			continue
		}

		return path, line, strings.TrimSpace(text[k:]), true
	}

	return "", 0, "", false
}

func cleanRelationshipPath(path string) string {
	path = strings.Trim(strings.TrimSpace(path), `"'(),`)
	path = strings.TrimPrefix(path, "file://")
	path = pathpkg.Clean(strings.ReplaceAll(filepath.ToSlash(path), "\\", "/"))

	if path == "." || path == ".." || strings.HasPrefix(path, "../") {
		return ""
	}

	return strings.TrimPrefix(path, "./")
}

func looksLikeRelationshipPath(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}

	if strings.Contains(path, "/") || strings.Contains(path, `\`) {
		return true
	}

	switch strings.ToLower(filepath.Ext(path)) {
	case ".go", ".c", ".cc", ".cpp", ".cxx", ".h", ".hh", ".hpp", ".hxx", ".py", ".ts", ".tsx", ".js", ".jsx", ".rs", ".java", ".gd":
		return true
	default:
		return false
	}
}

func firstRelationshipName(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}

	if before, _, ok := strings.Cut(text, " in "); ok {
		text = before
	}

	fields := strings.Fields(text)
	if len(fields) == 0 {
		return ""
	}

	switch strings.ToLower(fields[0]) {
	case "func", "function", "method", "class", "type", "interface", "struct", "enum", "constructor", "impl":
		if len(fields) > 1 {
			return strings.Trim(fields[1], `"'(),`)
		}
	}

	return strings.Trim(fields[0], `"'(),`)
}
