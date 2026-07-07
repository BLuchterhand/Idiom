package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"idiom/internal/capabilities/workspace/patching"
	"idiom/internal/platform/atomicfile"
	"idiom/internal/platform/failures"
)

func (m *Manager) applyPatch(req ToolRequest) ToolResult {
	if !m.cfg.EnableWrites {
		return toolWriteDisabledResult(ToolNameApplyPatch, "Patch request rejected", "workspace writes are disabled")
	}

	body := patching.NormalizeEnvelope(req.Content)
	if strings.TrimSpace(body) == "" {
		return toolInvalidArgumentResult(ToolNameApplyPatch, "Patch request rejected", "apply_patch requires the patch body in content")
	}

	ops, err := patching.Parse(body)
	if err != nil {
		return toolFailureFromErrorResult(ToolNameApplyPatch, "Patch parse failed", err, failures.InvalidArgument, reasonToolInvalidArgument)
	}

	type plannedWrite struct {
		path     string
		contents []byte
		remove   bool
	}

	planned := make([]plannedWrite, 0, len(ops))
	summaries := make([]string, 0, len(ops))

	for opIdx, op := range ops {
		path, err := m.pathAccess.ResolveWritePath(op.Path, req.Workdir)
		if err != nil {
			return toolFailureFromErrorResult(ToolNameApplyPatch, "Failed to resolve path", fmt.Errorf("op %d (%s %s): %w", opIdx+1, op.Kind, op.Path, err), failures.InvalidArgument, reasonToolPathResolutionFailed)
		}

		switch op.Kind {
		case patching.OpAdd:
			if _, statErr := os.Stat(path); statErr == nil {
				return toolFailureResult(ToolNameApplyPatch, "Patch request rejected", fmt.Sprintf("Add File target %s already exists", m.pathAccess.DisplayPath(path)), failures.AlreadyExists, reasonPlanSubmissionRejected)
			}

			body := op.NewBody
			if body != "" && !strings.HasSuffix(body, "\n") {
				body += "\n"
			}

			planned = append(planned, plannedWrite{path: path, contents: []byte(body)})
			summaries = append(summaries, fmt.Sprintf("add %s (%d bytes)", m.pathAccess.DisplayPath(path), len(body)))
		case patching.OpDelete:
			info, statErr := os.Stat(path)
			if statErr != nil {
				return toolFailureResult(ToolNameApplyPatch, "Patch request rejected", fmt.Sprintf("Delete File target %s not found", m.pathAccess.DisplayPath(path)), failures.NotFound, reasonPlanSubmissionRejected)
			}

			if info.IsDir() {
				return toolInvalidArgumentResult(ToolNameApplyPatch, "Patch request rejected", fmt.Sprintf("Delete File target %s is a directory", m.pathAccess.DisplayPath(path)))
			}

			planned = append(planned, plannedWrite{path: path, remove: true})
			summaries = append(summaries, fmt.Sprintf("delete %s", m.pathAccess.DisplayPath(path)))
		case patching.OpUpdate:
			current, readErr := os.ReadFile(path)
			if readErr != nil {
				return toolFailureFromErrorResult(ToolNameApplyPatch, "Patch request rejected", fmt.Errorf("Update File target %s: %w", m.pathAccess.DisplayPath(path), readErr), failures.NotFound, reasonPlanSubmissionRejected)
			}

			updated, applyErr := patching.ApplyUpdateHunks(string(current), op.Hunks)
			if applyErr != nil {
				return toolFailureFromErrorResult(ToolNameApplyPatch, "Patch hunk did not apply", fmt.Errorf("Update File %s: %w", m.pathAccess.DisplayPath(path), applyErr), failures.FailedPrecondition, reasonPlanSubmissionRejected)
			}

			destPath := path
			summary := fmt.Sprintf("update %s", m.pathAccess.DisplayPath(path))

			if op.MoveTo != "" {
				destPath, err = m.pathAccess.ResolveWritePath(op.MoveTo, req.Workdir)
				if err != nil {
					return toolFailureFromErrorResult(ToolNameApplyPatch, "Failed to resolve move destination", fmt.Errorf("Move to %s: %w", op.MoveTo, err), failures.InvalidArgument, reasonToolPathResolutionFailed)
				}

				if _, statErr := os.Stat(destPath); statErr == nil && filepath.Clean(destPath) != filepath.Clean(path) {
					return toolFailureResult(ToolNameApplyPatch, "Patch request rejected", fmt.Sprintf("Move to destination %s already exists", m.pathAccess.DisplayPath(destPath)), failures.AlreadyExists, reasonPlanSubmissionRejected)
				}

				if filepath.Clean(destPath) != filepath.Clean(path) {
					planned = append(planned, plannedWrite{path: path, remove: true})
				}
				summary = fmt.Sprintf("update+move %s -> %s", m.pathAccess.DisplayPath(path), m.pathAccess.DisplayPath(destPath))
			}

			planned = append(planned, plannedWrite{path: destPath, contents: []byte(updated)})
			summaries = append(summaries, summary)
		}
	}

	for _, write := range planned {
		if write.remove {
			continue
		}

		if rejected := m.writeGuard.RejectInvalidPlannedWrite(ToolNameApplyPatch, "Patch request rejected", write.path, write.contents, m.pathAccess.DisplayPath); rejected != nil {
			return *rejected
		}
	}

	for _, write := range planned {
		if write.remove {
			continue
		}

		if err := os.MkdirAll(filepath.Dir(write.path), 0o755); err != nil {
			return toolFailureFromErrorResult(ToolNameApplyPatch, "Patch apply failed", fmt.Errorf("mkdir %s: %w", m.pathAccess.DisplayPath(filepath.Dir(write.path)), err), failures.Internal, reasonToolWriteFailed)
		}

		if err := atomicfile.WriteFile(write.path, write.contents, 0o644); err != nil {
			return toolFailureFromErrorResult(ToolNameApplyPatch, "Patch apply failed", fmt.Errorf("write %s: %w", m.pathAccess.DisplayPath(write.path), err), failures.Internal, reasonToolWriteFailed)
		}
	}

	for _, write := range planned {
		if !write.remove {
			continue
		}
		if err := os.Remove(write.path); err != nil && !os.IsNotExist(err) {
			return toolFailureFromErrorResult(ToolNameApplyPatch, "Patch apply failed", fmt.Errorf("delete %s: %w", m.pathAccess.DisplayPath(write.path), err), failures.Internal, reasonToolWriteFailed)
		}
	}

	m.resetLanguageTools()

	return ToolResult{
		Tool:    ToolNameApplyPatch,
		Summary: fmt.Sprintf("Applied patch with %d op(s)", len(ops)),
		Output:  strings.Join(summaries, "\n"),
	}
}
