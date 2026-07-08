package workspace

import (
	"context"
	"fmt"
	"strings"

	"idiom/internal/platform/gitstate"
)

func (m *Manager) gitStatus(ctx context.Context, req ToolRequest) ToolResult {
	workdir, err := m.pathAccess.ResolveWorkdir(req.Workdir)
	if err != nil {
		return ToolResult{Tool: ToolNameGitStatus, Summary: "Failed to resolve workdir", Error: err.Error()}
	}

	output, execErr := m.gitRunner(workdir).Status(ctx)
	result := ToolResult{
		Tool:    ToolNameGitStatus,
		Summary: fmt.Sprintf("Read git status in %s", m.pathAccess.DisplayPath(workdir)),
		Output:  strings.TrimSpace(output),
	}

	if execErr != nil {
		result.Error = execErr.Error()
	}

	return result
}

func (m *Manager) gitDiff(ctx context.Context, req ToolRequest) ToolResult {
	workdir, err := m.pathAccess.ResolveWorkdir(req.Workdir)
	if err != nil {
		return ToolResult{Tool: ToolNameGitDiff, Summary: "Failed to resolve workdir", Error: err.Error()}
	}

	diffPath := ""

	if req.Path != "" {
		resolved, err := m.pathAccess.ResolvePath(req.Path, req.Workdir)
		if err != nil {
			return ToolResult{Tool: ToolNameGitDiff, Summary: "Failed to resolve path", Error: err.Error()}
		}

		diffPath = resolved
	}

	output, execErr := m.gitRunner(workdir).WorktreeDiff(ctx, req.Path)
	scope := "working tree"

	if req.Path != "" {
		scope = req.Path
	}

	result := ToolResult{
		Tool:    ToolNameGitDiff,
		Summary: fmt.Sprintf("Read git diff for %s in %s", scope, m.pathAccess.DisplayPath(workdir)),
		Output:  strings.TrimSpace(output),
	}
	if execErr != nil {
		result.Error = execErr.Error()
	} else if diffPath != "" {
		m.writeGuard.ClearEditRecovery(diffPath)
	} else {
		m.writeGuard.ClearAllEditRecovery()
	}

	return result
}

func (m *Manager) gitRunner(root string) gitstate.Runner {
	return gitstate.Runner{
		Root: root,
		RunCommand: func(ctx context.Context, root string, args ...string) (string, error) {
			return m.runCommand(ctx, root, "git", args...)
		},
	}
}
