package languagetools

import (
	"fmt"
	"strings"
)

func (m *Manager) relatedFilesResult(tool string, language string, pattern string, paths []string, display func(string) (string, error)) ToolResult {
	if len(paths) == 0 {
		return ToolResult{Tool: tool, Summary: fmt.Sprintf("No related %s files matched %q", language, pattern)}
	}
	lines := make([]string, 0, len(paths))
	for _, rel := range paths {
		line, err := display(rel)
		if err != nil {
			continue
		}
		lines = append(lines, line)
	}
	return ToolResult{Tool: tool, Summary: fmt.Sprintf("Found %d related %s file(s).", len(lines), language), Output: strings.Join(lines, "\n")}
}
