package languagetools

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"idiom/internal/platform/failures"
	semanticcpp "idiom/internal/repository/semantic/cpp"
)

const cppDeadSymbolTool = "cpp_dead_symbol"

// cppReferenceLocationPattern finds the first "path:line" in a rendered
// reference line ("<handle> <path>:<line>:<text>"). The lazy path keeps a
// Windows drive colon ("C:/...") inside the path.
var cppReferenceLocationPattern = regexp.MustCompile(`(\S+?):(\d+)(?::|$)`)

func (m *Manager) cppDeadSymbol(ctx context.Context, req ToolRequest) ToolResult {
	match, resolved, failure := m.resolveCppMatch(ctx, cppDeadSymbolTool, "C++ dead-symbol query rejected", req)
	if failure != nil {
		return *failure
	}

	references, backend := m.cppReferencesMatch(ctx, req, match, true)
	if references.Error != "" {
		references.Tool = cppDeadSymbolTool
		references.Summary = "C++ dead-symbol query failed"

		return references
	}

	return resolved.annotate(cppDeadSymbolVerdict(references, backend, resolved.resolution))
}

// cppDeadSymbolVerdict decides deadness only from real evidence. Every resolved
// symbol has at least its own declaration, so a backend that returned nothing
// failed; that is reported as an error, never as "dead". Reference sites inside
// the symbol's own declarations and definitions (including recursion) do not
// keep it alive.
func cppDeadSymbolVerdict(references ToolResult, backend cppReferenceBackend, resolution semanticcpp.CppResolution) ToolResult {
	result := ToolResult{Tool: cppDeadSymbolTool, Warnings: references.Warnings}

	if backend == cppReferencesNoEvidence {
		result.Summary = fmt.Sprintf("Cannot decide whether %s is dead: no reference backend produced evidence.", resolution.Owner)
		result.Error = fmt.Sprintf("no C++ reference evidence for %s: clangd and text search found not even its own declaration", resolution.Owner)
		result.Failure = failures.New(failures.Unavailable, reasonLanguageToolUnavailable, result.Error)

		return result
	}

	source := "name-based text search"
	if backend == cppReferencesByClangd {
		source = "clangd"
	}

	lines := nonEmptySearchLines(references.Output)
	uses := cppReferencesOutsideDefinitions(lines, resolution.Definitions)

	if len(uses) == 0 {
		result.Summary = fmt.Sprintf("No references to %s outside its own declarations and definitions (via %s); the symbol is likely dead.", resolution.Owner, source)
		result.Output = strings.Join(lines, "\n")

		return result
	}

	result.Summary = fmt.Sprintf("Found %d reference(s) to %s outside its own declarations and definitions (via %s); the symbol is not dead.", len(uses), resolution.Owner, source)
	result.Output = strings.Join(uses, "\n")

	return result
}

func cppReferencesOutsideDefinitions(lines []string, definitions []semanticcpp.CppSymbolMatch) []string {
	uses := make([]string, 0, len(lines))

	for _, line := range lines {
		if !cppReferenceWithinDefinitions(line, definitions) {
			uses = append(uses, line)
		}
	}

	return uses
}

// cppReferenceWithinDefinitions reports whether a rendered reference line lies
// inside one of the symbol's own declaration or definition spans. A line whose
// location cannot be parsed counts as a use: the error leans to "not dead".
func cppReferenceWithinDefinitions(line string, definitions []semanticcpp.CppSymbolMatch) bool {
	location := cppReferenceLocationPattern.FindStringSubmatch(line)
	if location == nil {
		return false
	}

	referencePath := filepath.ToSlash(location[1])

	lineNumber, err := strconv.Atoi(location[2])
	if err != nil {
		return false
	}

	for _, definition := range definitions {
		first, last := cppDefinitionLineSpan(definition)
		if filepath.ToSlash(definition.Path) == referencePath && lineNumber >= first && lineNumber <= last {
			return true
		}
	}

	return false
}

func cppDefinitionLineSpan(definition semanticcpp.CppSymbolMatch) (int, int) {
	first, last := definition.Line, definition.Line

	if definition.StartLine > 0 && definition.StartLine < first {
		first = definition.StartLine
	}

	if definition.EndLine > last {
		last = definition.EndLine
	}

	return first, last
}
