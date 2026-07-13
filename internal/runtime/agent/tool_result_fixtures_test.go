package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/onsi/ginkgo/v2"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/toolfacts"
	"idiom/internal/contracts/toolpolicy"
	"idiom/internal/platform/config"
)

func readFileSourceWindowResult(path string, startLine int, endLine int, totalLines int, output string) workspace.ToolResult {
	summary := fmt.Sprintf("Read %s lines %d-%d", path, startLine, endLine)
	if totalLines > 0 {
		summary += fmt.Sprintf(" (total lines: %d)", totalLines)
	}

	return sourceWindowResultForTest(workspace.ToolNameReadFile, path, startLine, endLine, totalLines, summary, output)
}

func codeNavigateSourceWindowResult(path string, startLine int, endLine int, totalLines int, output string) workspace.ToolResult {
	summary := fmt.Sprintf("Code navigation read %s lines %d-%d", path, startLine, endLine)
	if totalLines > 0 {
		summary += fmt.Sprintf(" (total lines: %d)", totalLines)
	}

	return sourceWindowResultForTest(workspace.ToolNameCodeNavigate, path, startLine, endLine, totalLines, summary, output)
}

func diagnosticLogReadResultForTest(canonicalID string, policySubject string, startLine int, endLine int, totalLines int, truncated bool, output string) workspace.ToolResult {
	return workspace.ToolResult{
		Tool:   workspace.ToolNameReadFile,
		Output: output,
		Paths:  []string{policySubject},
		Facts: toolfacts.NewDiagnosticLogReadFacts(toolfacts.DiagnosticLogReadEvidence{
			CanonicalID:    canonicalID,
			PolicySubject:  policySubject,
			PolicyDecision: toolpolicy.Allow,
			StartLine:      startLine,
			EndLine:        endLine,
			TotalLines:     totalLines,
			Truncated:      truncated,
		}),
	}
}

func sourceWindowFactsForTest(path string, startLine int, endLine int, totalLines int) *toolfacts.ToolResultFacts {
	if totalLines <= 0 {
		totalLines = endLine
	}
	body := []byte(fmt.Sprintf("fixture:%s:%d:%d:%d", path, startLine, endLine, totalLines))
	return toolfacts.NewSourceWindowFacts(toolfacts.SourceWindowEvidence{
		CanonicalID: path,
		Authority: toolfacts.SourceWindowAuthorityEvidence{
			PolicySubject:  path,
			PolicyDecision: toolpolicy.Allow,
		},
		StartLine:      startLine,
		EndLine:        endLine,
		TotalLines:     totalLines,
		RevisionSHA256: toolfacts.SourceWindowSHA256(body),
		WindowSHA256:   toolfacts.SourceWindowSHA256(body),
		MutationEpoch:  1,
	})
}

func sourceWindowResultForTest(tool string, path string, startLine int, endLine int, totalLines int, summary string, output string) workspace.ToolResult {
	if filepath.IsAbs(path) || IsNonSourceEvidenceArtifactPath(path) {
		return workspace.ToolResult{Tool: tool, Summary: summary, Output: output, Paths: []string{path}}
	}

	t := ginkgo.GinkgoT()
	t.Helper()
	root := t.TempDir()
	cleanPath := filepath.Clean(filepath.FromSlash(path))
	if cleanPath == "." || cleanPath == ".." || strings.HasPrefix(cleanPath, ".."+string(filepath.Separator)) {
		return workspace.ToolResult{Tool: tool, Summary: summary, Output: output, Paths: []string{path}}
	}

	contentLines := strings.Split(strings.TrimSuffix(output, "\n"), "\n")
	if len(contentLines) == 0 || (len(contentLines) == 1 && contentLines[0] == "") {
		contentLines = []string{"fixture source window"}
	}
	if startLine <= 0 {
		startLine = 1
	}
	if endLine < startLine+len(contentLines)-1 {
		endLine = startLine + len(contentLines) - 1
	}
	if totalLines < endLine {
		totalLines = endLine
	}
	lines := make([]string, totalLines)
	for i := range lines {
		lines[i] = "fixture padding"
	}
	copy(lines[startLine-1:endLine], contentLines)

	absolutePath := filepath.Join(root, cleanPath)
	if err := os.MkdirAll(filepath.Dir(absolutePath), 0o755); err != nil {
		t.Fatalf("create source-window fixture directory: %v", err)
	}
	if err := os.WriteFile(absolutePath, []byte(strings.Join(lines, "\n")), 0o600); err != nil {
		t.Fatalf("write source-window fixture: %v", err)
	}

	manager := workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true, MaxOutputBytes: 4 << 20})
	manager.SetPermissionRules(map[string][]toolpolicy.Rule{
		workspace.ToolNameReadFile: {{Pattern: "**", Decision: toolpolicy.Allow}},
	})
	result := manager.Execute(context.Background(), workspace.ToolRequest{
		Tool:      workspace.ToolNameReadFile,
		Path:      filepath.ToSlash(cleanPath),
		StartLine: startLine,
		EndLine:   endLine,
	})
	if result.Error != "" {
		t.Fatalf("produce source-window fixture for %s: %#v", path, result)
	}
	result.Tool = tool
	result.Summary = summary
	result.Output = output
	return result
}

func sourceWindowManagerResultsForTest(paths ...string) (*workspace.Manager, []workspace.ToolResult) {
	t := ginkgo.GinkgoT()
	t.Helper()
	root := t.TempDir()
	for _, path := range paths {
		absolutePath := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(absolutePath), 0o755); err != nil {
			t.Fatalf("create source-window manager fixture directory: %v", err)
		}
		if err := os.WriteFile(absolutePath, []byte("package fixture\n"), 0o600); err != nil {
			t.Fatalf("write source-window manager fixture: %v", err)
		}
	}

	manager := workspace.NewManager(config.Workspace{
		Root:           root,
		CWD:            root,
		EnableWrites:   true,
		MaxOutputBytes: 4 << 20,
	})
	manager.SetPermissionRules(map[string][]toolpolicy.Rule{
		workspace.ToolNameReadFile: {{Pattern: "**", Decision: toolpolicy.Allow}},
	})
	results := make([]workspace.ToolResult, 0, len(paths))
	for _, path := range paths {
		result := manager.Execute(context.Background(), workspace.ToolRequest{
			Tool:      workspace.ToolNameReadFile,
			Path:      path,
			StartLine: 1,
			EndLine:   1,
		})
		if result.Error != "" || len(result.SourceWindows()) == 0 {
			t.Fatalf("read current source-window fixture for %s: %#v", path, result)
		}
		results = append(results, result)
	}
	return manager, results
}

func committedMutationResultForTest(path string) (workspace.ToolRequest, workspace.ToolResult) {
	t := ginkgo.GinkgoT()
	t.Helper()
	root := t.TempDir()
	manager := workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
	content := "receipt-backed mutation fixture\n"
	if strings.HasSuffix(strings.ToLower(path), ".go") {
		content = "package fixture\n"
	}
	request := workspace.ToolRequest{
		Tool:        workspace.ToolNameWriteFile,
		Path:        path,
		Content:     content,
		Description: "create receipt-backed mutation fixture",
	}
	result := manager.Execute(context.Background(), request)
	if result.Error != "" || result.CommitReceipt() == nil {
		t.Fatalf("produce mutation receipt fixture for %s: %#v", path, result)
	}
	return request, result
}

func installMutationReceiptForTest(run *runExecution, result workspace.ToolResult) {
	ginkgo.GinkgoHelper()
	if run == nil || result.CommitReceipt() == nil {
		ginkgo.Fail("mutation receipt fixture requires a run and commit receipt", 1)
	}
	run.state.recordMutationCommitReceipt(result)
	if len(run.state.runtimeBrief.RequiredEditPaths) > 0 {
		run.state.mutation.bound = true
		run.state.mutation.canonicalScopeProjection.RequiredCoverageIDs = append(
			[]string(nil),
			run.state.runtimeBrief.RequiredEditPaths...,
		)
	}
}

func shellCommandResultForTest(command string, output string, errText string) workspace.ToolResult {
	command = strings.TrimSpace(command)
	rendered := "$ " + command
	if body := strings.TrimSpace(output); body != "" {
		rendered += "\n" + body
	}

	result := workspace.ToolResult{
		Tool:    workspace.ToolNameShell,
		Summary: "Ran shell command",
		Output:  rendered,
		Facts:   &toolfacts.ToolResultFacts{ShellCommand: command, ShellWorkdir: "."},
	}
	if errText != "" {
		result.Error = errText
	}
	return result
}
