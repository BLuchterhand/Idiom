package workspace

import (
	"context"
	"fmt"
	"strings"

	"idiom/internal/platform/failures"
)

func (m *Manager) makeDir(ctx context.Context, req ToolRequest) ToolResult {
	if !m.cfg.EnableWrites {
		return toolWriteDisabledResult(ToolNameMkdir, "Write request rejected", "workspace writes are disabled")
	}

	mutation, err := m.pathAccess.beginWorkspaceMutation(ctx, req)
	if err != nil {
		return toolPathResolutionFailureResult(ToolNameMkdir, "Failed to resolve path", err)
	}
	defer mutation.Close()

	target, err := mutation.Resolve(req.Path)
	if err != nil {
		return toolPathResolutionFailureResult(ToolNameMkdir, "Failed to resolve path", err)
	}
	mutation.reachCheckpoint(workspaceMutationAfterPreflight, target, 1)
	if err := mutation.MakeDir(target, 1); err != nil {
		mutation.setFailureIntent(target, nil)
		return m.workspaceMutationFailureResult(
			ToolNameMkdir,
			"Failed to create directory",
			"Directory creation partially applied",
			err,
			mutation,
			failures.Internal,
			reasonToolWriteFailed,
			nil,
		)
	}

	if len(mutation.CommittedPaths()) == 0 {
		result := ToolResult{
			Tool:    ToolNameMkdir,
			Summary: fmt.Sprintf("Directory already exists: %s", target.display),
			Output:  target.display,
		}
		mutation.attachCommitReceipt(ToolNameMkdir, &result)
		return result
	}
	m.resetLanguageTools()

	result := ToolResult{
		Tool:    ToolNameMkdir,
		Summary: fmt.Sprintf("Created directory %s", target.display),
		Output:  target.display,
	}
	mutation.attachCommitReceipt(ToolNameMkdir, &result)
	return result
}

func (m *Manager) writeFile(ctx context.Context, req ToolRequest) ToolResult {
	toolName := ToolNameWriteFile
	appendMode := strings.TrimSpace(req.Tool) == ToolNameAppendFile
	if appendMode {
		toolName = ToolNameAppendFile
	}

	if !m.cfg.EnableWrites {
		return toolWriteDisabledResult(toolName, "Write request rejected", "workspace writes are disabled")
	}

	mutation, err := m.pathAccess.beginWorkspaceMutation(ctx, req)
	if err != nil {
		return toolPathResolutionFailureResult(toolName, "Failed to resolve path", err)
	}
	defer mutation.Close()

	target, err := mutation.Resolve(req.Path)
	if err != nil {
		return toolPathResolutionFailureResult(toolName, "Failed to resolve path", err)
	}
	preimage, err := mutation.Observe(target)
	if err != nil {
		mutation.setFailureIntent(target, nil)
		return m.workspaceMutationFailureResult(
			toolName,
			"Failed to read existing file",
			"Write partially applied",
			err,
			mutation,
			failures.Internal,
			reasonToolInternalFailure,
			nil,
		)
	}

	updated := []byte(req.Content)
	if appendMode {
		updated = append(append([]byte(nil), preimage.body...), []byte(req.Content)...)
	} else if preimage.exists {
		if IsCompletedPlanningArtifact(target.absolute, preimage.body) {
			return withToolFailure(ToolResult{
				Tool:    toolName,
				Summary: "Write request rejected",
				Error:   fmt.Sprintf("write_file refuses to overwrite completed planning artifact %s. The available contract outcomes are a bounded anchored update or explicit user approval to reopen/replace the completed artifact.", target.display),
			}, failures.FailedPrecondition, reasonPlanSubmissionRejected)
		}
	}

	if rejected := m.writeGuard.RejectPlannedWrite(toolName, "Write request rejected", target.absolute, preimage.body, updated, m.pathAccess.DisplayPath); rejected != nil {
		return *rejected
	}

	mutation.reachCheckpoint(workspaceMutationAfterPreflight, target, 1)
	var mutationErr error
	if appendMode {
		mutationErr = mutation.Append(target, preimage, []byte(req.Content), 0o644, 1)
	} else {
		mutationErr = mutation.Replace(target, preimage, updated, 0o644, 1)
	}
	if mutationErr != nil {
		mutation.setFailureIntent(target, nil)
		failureSummary := "Failed to write file"
		partialSummary := "Write partially applied"
		if appendMode {
			failureSummary = "Failed to append file"
			partialSummary = "Append partially applied"
		}

		return m.workspaceMutationFailureResult(
			toolName,
			failureSummary,
			partialSummary,
			mutationErr,
			mutation,
			failures.Internal,
			reasonToolWriteFailed,
			nil,
		)
	}

	action := "Wrote"
	if appendMode {
		action = "Appended"
	}

	m.resetLanguageTools()

	result := ToolResult{
		Tool:    toolName,
		Summary: fmt.Sprintf("%s %s", action, target.display),
		Output:  fmt.Sprintf("%s bytes: %d", action, len(req.Content)),
	}
	mutation.attachCommitReceipt(toolName, &result)
	return result
}

func (m *Manager) editFile(ctx context.Context, req ToolRequest) ToolResult {
	if !m.cfg.EnableWrites {
		return toolWriteDisabledResult(ToolNameEditFile, "Edit request rejected", "workspace writes are disabled")
	}

	mutation, err := m.pathAccess.beginWorkspaceMutation(ctx, req)
	if err != nil {
		result := toolPathResolutionFailureResult(ToolNameEditFile, "Failed to resolve path", err)
		if strings.TrimSpace(req.Path) != "" {
			result.Output = formatPathSuggestions(m.findPathSuggestions(req.Path, 5))
		}

		return result
	}
	defer mutation.Close()

	target, err := mutation.Resolve(req.Path)
	if err != nil {
		result := toolPathResolutionFailureResult(ToolNameEditFile, "Failed to resolve path", err)
		if strings.TrimSpace(req.Path) != "" {
			result.Output = formatPathSuggestions(m.findPathSuggestions(req.Path, 5))
		}

		return result
	}

	if rejected := m.writeGuard.RejectUnobservedEdit(ToolNameEditFile, target.absolute, req.Path, m.bypassReadBeforeEdit, m.HasObservedPath, m.pathAccess.DisplayPath); rejected != nil {
		return *rejected
	}

	if rejected := m.writeGuard.RejectPendingEditRecovery(ToolNameEditFile, target.absolute, m.pathAccess.DisplayPath); rejected != nil {
		return *rejected
	}

	preimage, err := mutation.Observe(target)
	if err != nil {
		result := toolFailureFromErrorResult(ToolNameEditFile, "Failed to read file", err, failures.Internal, reasonToolInternalFailure)
		if strings.TrimSpace(req.Path) != "" {
			result.Output = formatPathSuggestions(m.findPathSuggestions(req.Path, 5))
		}

		return result
	}
	if !preimage.exists {
		result := toolNotFoundResult(ToolNameEditFile, "Failed to read file", fmt.Sprintf("edit file target %s not found", target.display))
		if strings.TrimSpace(req.Path) != "" {
			result.Output = formatPathSuggestions(m.findPathSuggestions(req.Path, 5))
		}

		return result
	}

	current := string(preimage.body)

	projection := projectEditFile(target.absolute, target.display, current, req)
	if !projection.shouldWrite {
		return projection.result
	}

	if rejected := m.writeGuard.RejectPlannedWrite(ToolNameEditFile, "Edit request rejected", target.absolute, preimage.body, []byte(projection.updated), m.pathAccess.DisplayPath); rejected != nil {
		return *rejected
	}

	mutation.reachCheckpoint(workspaceMutationAfterPreflight, target, 1)
	if err := mutation.Replace(target, preimage, []byte(projection.updated), 0o644, 1); err != nil {
		mutation.setFailureIntent(target, nil)
		return m.workspaceMutationFailureResult(
			ToolNameEditFile,
			"Failed to write edited file",
			"Edit partially applied",
			err,
			mutation,
			failures.Internal,
			reasonToolWriteFailed,
			nil,
		)
	}

	m.resetLanguageTools()

	mutation.attachCommitReceipt(ToolNameEditFile, &projection.result)
	return projection.result
}
