package workspace

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"idiom/internal/platform/commandexec"
	"idiom/internal/platform/failures"
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
	if IsLanguageToolName(tool) {
		return true
	}

	if metadata, ok := builtInToolSurfaceCatalog.lookup(tool); ok {
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

func (m *Manager) runShell(ctx context.Context, req ToolRequest) ToolResult {
	if !m.cfg.EnableShell {
		return toolPermissionPolicyResult(ToolNameShell, "Shell request rejected", "shell execution is disabled")
	}

	command := strings.TrimSpace(req.Command)
	if command == "" {
		return toolInvalidArgumentResult(ToolNameShell, "Shell request rejected", "command is required")
	}

	workdir, err := m.pathAccess.ResolveWorkdir(req.Workdir)
	if err != nil {
		return toolPathResolutionFailureResult(ToolNameShell, "Failed to resolve workdir", err)
	}

	if isDirectoryOnlyShellCommand(command) {
		return withToolFailure(ToolResult{
			Tool:    ToolNameShell,
			Summary: "Shell request rejected",
			Error:   "directory changes inside shell commands do not persist; set workdir or include the command to run in that directory",
		}, failures.InvalidArgument, reasonToolInvalidArgument)
	}

	if m.Mode() == ModeExplore {
		if err := validateExploreShellCommand(command); err != nil {
			return withToolFailure(ToolResult{
				Tool:    ToolNameShell,
				Summary: "Shell request rejected",
				Error:   "explore mode only allows read-only inspection shell commands: " + err.Error(),
			}, failures.PermissionDenied, reasonToolPermissionPolicy)
		}
	}

	stdout, stderr, execErr := m.runCommandDetailedWithEnv(ctx, workdir, nil, m.cfg.Shell, "-lc", command)
	rawOutput := commandexec.CombineOutput(stdout, stderr)
	output := m.truncateCommandOutput(rawOutput)
	result := ToolResult{
		Tool:    ToolNameShell,
		Summary: fmt.Sprintf("Ran shell command in %s", m.pathAccess.DisplayPath(workdir)),
		Output:  "$ " + command + "\n" + strings.TrimSpace(output),
	}

	if execErr != nil {
		result.Error = execErr.Error()
	} else if reason := zeroSelectedTestFailureReason(command, rawOutput); reason != "" {
		result.Error = reason
	} else if m.Mode() != ModeExplore {
		m.resetLanguageTools()
	}

	return result
}

func zeroSelectedTestFailureReason(command string, output string) string {
	if !shellCommandLooksLikeTest(command) {
		return ""
	}

	if signal := zeroSelectedTestSignal(output); signal != "" {
		return "verification command selected zero tests despite exiting 0 (" + signal + "). Check the test filter/name and rerun a command that executes at least one test."
	}

	return ""
}

func shellCommandLooksLikeTest(command string) bool {
	lower := strings.ToLower(strings.TrimSpace(command))
	if lower == "" {
		return false
	}

	for _, needle := range []string{
		"go test",
		"cargo test",
		"npm test",
		"yarn test",
		"pnpm test",
		"pytest",
		"doctest",
		"ctest",
		"jest",
		"vitest",
		"colony_tests",
		"run_tests.py",
		"run_tests.sh",
		"run_tests",
		"-tc=",
	} {
		if strings.Contains(lower, needle) {
			return true
		}
	}

	return false
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
