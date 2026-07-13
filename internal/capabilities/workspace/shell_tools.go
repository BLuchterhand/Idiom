package workspace

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"idiom/internal/contracts/autonomy"
	"idiom/internal/contracts/toolfacts"
	"idiom/internal/platform/commandexec"
	"idiom/internal/repository/semantic/languagetools"
)

func IsBroadInspectionRequest(req ToolRequest) bool {
	switch strings.TrimSpace(req.Tool) {
	case ToolNameListFiles:
		return !IsScopedInspectionRequest(req)
	case ToolNameShell:
		return AnalyzeShellCommand(req.Command).BroadInspection
	default:
		return false
	}
}

func IsScopedInspectionRequest(req ToolRequest) bool {
	if strings.TrimSpace(req.Tool) != ToolNameListFiles {
		return false
	}

	path := filepath.Clean(defaultString(req.Path, "."))

	return path != "." && path != string(filepath.Separator)
}

func isStructuredNarrowingTool(tool string) bool {
	tool = strings.TrimSpace(tool)
	if languagetools.IsToolName(tool) {
		return true
	}

	if metadata, ok := builtInToolSurfaceCatalog().lookup(tool); ok {
		return metadata.Narrowing
	}

	return false
}

func IsNarrowingRequest(req ToolRequest) bool {
	switch strings.TrimSpace(req.Tool) {
	case ToolNameListFiles:
		return IsScopedInspectionRequest(req)
	case ToolNameReadFile:
		return true
	case ToolNameShell:
		return AnalyzeShellCommand(req.Command).Narrowing
	default:
		return isStructuredNarrowingTool(req.Tool)
	}
}

func (m *Manager) ShouldClearBroadInspectionLock(req ToolRequest, result ToolResult) bool {
	if result.Error != "" {
		return false
	}

	switch strings.TrimSpace(req.Tool) {
	case ToolNameListFiles:
		return IsScopedInspectionRequest(req)
	case ToolNameReadFile:
		path, _, err := m.resolveReadTarget(req)
		if err != nil {
			return false
		}

		return m.isRelevantExplorationPath(path)
	case ToolNameShell:
		return AnalyzeShellCommand(req.Command).Narrowing
	default:
		return isStructuredNarrowingTool(req.Tool)
	}
}

func splitShellSegments(command string) []string {
	replacer := strings.NewReplacer("&&", "|", "||", "|", ";", "|")
	normalized := replacer.Replace(command)
	parts := strings.Split(normalized, "|")
	segments := make([]string, 0, len(parts))

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			segments = append(segments, part)
		}
	}

	return segments
}

type workspaceShellExecution struct {
	command       string
	workdir       string
	result        commandexec.Result
	rawOutput     string
	displayOutput string
	failure       string
}

func (m *Manager) captureWorkspaceShell(ctx context.Context, req ToolRequest, tool string) (workspaceShellExecution, ToolResult, bool) {
	if tool == ToolNameShell && !m.cfg.EnableShell {
		return workspaceShellExecution{}, toolPermissionPolicyResult(tool, workspaceShellRejectedSummary(tool), "shell execution is disabled"), false
	}

	command := strings.TrimSpace(req.Command)
	if command == "" {
		return workspaceShellExecution{}, toolInvalidArgumentResult(tool, workspaceShellRejectedSummary(tool), "command is required"), false
	}

	if !m.cfg.EnableShell {
		return workspaceShellExecution{}, toolPermissionPolicyResult(tool, workspaceShellRejectedSummary(tool), "shell execution is disabled"), false
	}

	if isDirectoryOnlyShellCommand(command) {
		return workspaceShellExecution{}, toolInvalidArgumentResult(tool, workspaceShellRejectedSummary(tool), "directory changes inside shell commands do not persist; set workdir or include the command to run in that directory"), false
	}

	analysis := AnalyzeShellCommand(command)
	if m.Mode() == ModeExplore {
		if !analysis.ExploreAllowed {
			message := "explore mode only allows read-only inspection shell commands: " + analysis.ExploreRejection
			if tool == ToolNameContextTracePack {
				message = "explore mode only allows read-only inspection commands: " + analysis.ExploreRejection
			}

			return workspaceShellExecution{}, toolPermissionPolicyResult(tool, workspaceShellRejectedSummary(tool), message), false
		}
	}

	workdir, err := m.pathAccess.ResolveWorkdir(req.Workdir)
	if err != nil {
		summary := "Failed to resolve workdir"
		if tool == ToolNameContextTracePack {
			summary = "Context trace pack failed to resolve workdir"
		}

		return workspaceShellExecution{}, toolPathResolutionFailureResult(tool, summary, err), false
	}

	execResult := m.runCommandResult(ctx, commandexec.Spec{Mode: commandexec.ModeLoginShell, Shell: m.cfg.Shell, Command: command, Dir: workdir})
	rawOutput := execResult.CombinedOutput()
	displayOutput := m.commandDisplayOutput(execResult)
	execution := workspaceShellExecution{
		command:       command,
		workdir:       workdir,
		result:        execResult,
		rawOutput:     rawOutput,
		displayOutput: displayOutput.Text,
	}

	if execErr := execResult.FailureError(fmt.Sprintf("%ds", workspaceCommandTimeoutSeconds(m.cfg.CommandTimeoutSeconds))); execErr != nil {
		execution.failure = execErr.Error()
	}

	return execution, ToolResult{}, true
}

func workspaceShellRejectedSummary(tool string) string {
	if tool == ToolNameContextTracePack {
		return "Context trace pack rejected"
	}

	return "Shell request rejected"
}

func (m *Manager) runShell(ctx context.Context, req ToolRequest) ToolResult {
	execution, rejection, ok := m.captureWorkspaceShell(ctx, req, ToolNameShell)
	if !ok {
		return rejection
	}

	shellWorkdir := normalizeVerificationScope(m.pathAccess.DisplayPath(execution.workdir))
	if shellWorkdir == "" {
		shellWorkdir = "."
	}
	result := ToolResult{
		Tool:    ToolNameShell,
		Summary: fmt.Sprintf("Ran shell command in %s", m.pathAccess.DisplayPath(execution.workdir)),
		Output:  "$ " + execution.command + "\n" + strings.TrimSpace(execution.displayOutput),
		Facts: &toolfacts.ToolResultFacts{
			ShellCommand: execution.command,
			ShellWorkdir: shellWorkdir,
		},
	}
	analysis := AnalyzeShellCommand(execution.command)
	if analysis.SideEffect != autonomy.SideEffectReadOnly {
		m.mutationEpoch.Add(1)
	}

	if execution.failure != "" {
		result.Error = execution.failure
	} else if reason := zeroSelectedTestFailureReason(execution.command, execution.rawOutput); reason != "" {
		result.Error = reason
	} else {
		result.Facts.VerificationEvidence = m.successfulShellVerificationEvidence(ctx, execution.command, execution.workdir, shellWorkdir)
		if m.Mode() != ModeExplore {
			m.resetLanguageTools()
		}
	}

	return result
}

func zeroSelectedTestFailureReason(command string, output string) string {
	if !AnalyzeShellCommand(command).BuildOrTest {
		return ""
	}
	fields := shellCommandFields(command)
	if len(fields) > 0 && normalizeShellHead(fields[0]) == "go" {
		if reason := goVerificationNonExecutionReason(fields[1:]); reason != "" {
			return "verification command did not execute despite exiting 0 (" + reason + "). Run a command that performs the intended verification."
		}
	}

	if signal := zeroSelectedTestSignal(output); signal != "" {
		return "verification command selected zero tests despite exiting 0 (" + signal + "). Check the test filter/name and rerun a command that executes at least one test."
	}

	return ""
}

func zeroSelectedTestSignal(output string) string {
	lower := strings.ToLower(output)
	switch {
	case strings.Contains(lower, "test cases: 0 |"):
		return "doctest reported test cases: 0"
	case strings.Contains(lower, "0 tests selected"):
		return "reported 0 tests selected"
	case strings.Contains(lower, "collected 0 items"):
		return "pytest collected 0 items"
	case strings.Contains(lower, "no tests ran"):
		return "reported no tests ran"
	case strings.Contains(lower, "no tests to run"):
		return "reported no tests to run"
	case strings.Contains(lower, "ran 0 tests"):
		return "reported ran 0 tests"
	default:
		return ""
	}
}

func isDirectoryOnlyShellCommand(command string) bool {
	segments := splitShellSegments(command)
	if len(segments) == 0 {
		return false
	}

	for _, segment := range segments {
		fields := strings.Fields(segment)
		if len(fields) == 0 {
			continue
		}

		switch fields[0] {
		case "cd", "pushd", "popd":
			continue
		default:
			return false
		}
	}

	return true
}
