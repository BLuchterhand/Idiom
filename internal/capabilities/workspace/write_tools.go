package workspace

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"idiom/internal/platform/atomicfile"
	"idiom/internal/platform/failures"
)

func (m *Manager) makeDir(req ToolRequest) ToolResult {
	if !m.cfg.EnableWrites {
		return toolWriteDisabledResult(ToolNameMkdir, "Write request rejected", "workspace writes are disabled")
	}

	path, err := m.pathAccess.ResolveWritePath(req.Path, req.Workdir)
	if err != nil {
		return toolPathResolutionFailureResult(ToolNameMkdir, "Failed to resolve path", err)
	}

	if err := os.MkdirAll(path, 0o755); err != nil {
		return toolWriteFailureResult(ToolNameMkdir, "Failed to create directory", err)
	}

	m.invalidateLanguageToolsAfterWorkspaceMutation()

	return ToolResult{
		Tool:    ToolNameMkdir,
		Summary: fmt.Sprintf("Created directory %s", m.pathAccess.DisplayPath(path)),
		Output:  m.pathAccess.DisplayPath(path),
	}
}

func (m *Manager) writeFile(req ToolRequest, appendMode bool) ToolResult {
	toolName := ToolNameWriteFile
	if appendMode {
		toolName = ToolNameAppendFile
	}

	if !m.cfg.EnableWrites {
		return toolWriteDisabledResult(toolName, "Write request rejected", "workspace writes are disabled")
	}

	path, err := m.pathAccess.ResolveWritePath(req.Path, req.Workdir)
	if err != nil {
		return toolPathResolutionFailureResult(toolName, "Failed to resolve path", err)
	}

	updated := []byte(req.Content)

	if appendMode {
		current, err := os.ReadFile(path)
		if err != nil && !os.IsNotExist(err) {
			return toolFailureFromErrorResult(toolName, "Failed to read existing file", err, failures.Internal, reasonToolInternalFailure)
		}

		updated = append(append([]byte(nil), current...), []byte(req.Content)...)
	} else if current, err := os.ReadFile(path); err == nil {
		if IsCompletedPlanningArtifact(path, current) {
			return withToolFailure(ToolResult{
				Tool:    toolName,
				Summary: "Write request rejected",
				Error:   fmt.Sprintf("write_file refuses to overwrite completed planning artifact %s. The available contract outcomes are a bounded anchored update or explicit user approval to reopen/replace the completed artifact.", m.pathAccess.DisplayPath(path)),
			}, failures.FailedPrecondition, reasonPlanSubmissionRejected)
		}
	} else if !os.IsNotExist(err) {
		return toolFailureFromErrorResult(toolName, "Failed to read existing file", err, failures.Internal, reasonToolInternalFailure)
	}

	if rejected := m.writeGuard.RejectInvalidPlannedWrite(toolName, "Write request rejected", path, updated, m.pathAccess.DisplayPath); rejected != nil {
		return *rejected
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return toolWriteFailureResult(toolName, "Failed to create parent directory", err)
	}

	if appendMode {
		if err := appendFileContent(path, []byte(req.Content)); err != nil {
			return toolWriteFailureResult(toolName, "Failed to append file", err)
		}
	} else if err := atomicfile.WriteFile(path, updated, 0o644); err != nil {
		return toolWriteFailureResult(toolName, "Failed to write file", err)
	}

	action := "Wrote"
	if appendMode {
		action = "Appended"
	}

	m.invalidateLanguageToolsAfterWorkspaceMutation()

	return ToolResult{
		Tool:    toolName,
		Summary: fmt.Sprintf("%s %s", action, m.pathAccess.DisplayPath(path)),
		Output:  fmt.Sprintf("%s bytes: %d", action, len(req.Content)),
	}
}

func appendFileContent(path string, content []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}

	writeErr := writeFull(f, content)
	syncErr := f.Sync()
	closeErr := f.Close()

	if writeErr != nil {
		return writeErr
	}
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}

func writeFull(w io.Writer, content []byte) error {
	for len(content) > 0 {
		n, err := w.Write(content)
		if err != nil {
			return err
		}
		if n <= 0 {
			return io.ErrShortWrite
		}
		content = content[n:]
	}

	return nil
}

func (m *Manager) editFile(req ToolRequest) ToolResult {
	if !m.cfg.EnableWrites {
		return toolWriteDisabledResult(ToolNameEditFile, "Edit request rejected", "workspace writes are disabled")
	}

	path, err := m.pathAccess.ResolveWritePath(req.Path, req.Workdir)
	if err != nil {
		result := toolPathResolutionFailureResult(ToolNameEditFile, "Failed to resolve path", err)
		if strings.TrimSpace(req.Path) != "" {
			result.Output = formatPathSuggestions(m.findPathSuggestions(req.Path, 5))
		}

		return result
	}

	if rejected := m.writeGuard.RejectUnobservedEdit(ToolNameEditFile, path, req.Path, m.bypassReadBeforeEdit, m.HasObservedPath, m.pathAccess.DisplayPath); rejected != nil {
		return *rejected
	}

	if rejected := m.writeGuard.RejectPendingEditRecovery(ToolNameEditFile, path, m.pathAccess.DisplayPath); rejected != nil {
		return *rejected
	}

	content, err := os.ReadFile(path)
	if err != nil {
		result := toolFailureFromErrorResult(ToolNameEditFile, "Failed to read file", err, failures.Internal, reasonToolInternalFailure)
		if strings.TrimSpace(req.Path) != "" {
			result.Output = formatPathSuggestions(m.findPathSuggestions(req.Path, 5))
		}

		return result
	}

	current := string(content)

	projection := projectEditFile(path, m.pathAccess.DisplayPath(path), current, req)
	if !projection.shouldWrite {
		return projection.result
	}

	if rejected := m.writeGuard.RejectInvalidPlannedWrite(ToolNameEditFile, "Edit request rejected", path, []byte(projection.updated), m.pathAccess.DisplayPath); rejected != nil {
		return *rejected
	}

	if err := atomicfile.WriteFile(path, []byte(projection.updated), 0o644); err != nil {
		return toolWriteFailureResult(ToolNameEditFile, "Failed to write edited file", err)
	}

	m.invalidateLanguageToolsAfterWorkspaceMutation()

	return projection.result
}
