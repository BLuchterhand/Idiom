package agent

import (
	"fmt"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/toolfacts"
)

func readFileSourceBodyResult(path string, startLine int, endLine int, totalLines int, output string) workspace.ToolResult {
	summary := fmt.Sprintf("Read %s lines %d-%d", path, startLine, endLine)
	if totalLines > 0 {
		summary += fmt.Sprintf(" (total lines: %d)", totalLines)
	}

	return workspace.ToolResult{
		Tool:    workspace.ToolNameReadFile,
		Summary: summary,
		Output:  output,
		Paths:   []string{path},
		Facts:   sourceBodyFactsForTest(path, startLine, endLine, totalLines),
	}
}

func codeNavigateSourceBodyResult(path string, startLine int, endLine int, totalLines int, output string) workspace.ToolResult {
	summary := fmt.Sprintf("Code navigation read %s lines %d-%d", path, startLine, endLine)
	if totalLines > 0 {
		summary += fmt.Sprintf(" (total lines: %d)", totalLines)
	}

	return workspace.ToolResult{
		Tool:    "code_navigate",
		Summary: summary,
		Output:  output,
		Paths:   []string{path},
		Facts:   sourceBodyFactsForTest(path, startLine, endLine, totalLines),
	}
}

func sourceBodyFactsForTest(path string, startLine int, endLine int, totalLines int) *toolfacts.ToolResultFacts {
	return toolfacts.NewSourceBodyFacts(toolfacts.SourceBodyEvidence{
		Path:       path,
		StartLine:  startLine,
		EndLine:    endLine,
		TotalLines: totalLines,
	})
}
