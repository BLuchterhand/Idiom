package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"idiom/internal/capabilities/workspace/patching"
	"idiom/internal/platform/failures"
)

const (
	PatchOpAdd    = patching.OpAdd
	PatchOpUpdate = patching.OpUpdate
	PatchOpDelete = patching.OpDelete
)

type PatchOp = patching.Op

func ParsePatch(text string) ([]PatchOp, error) {
	return patching.Parse(text)
}

// SerializePatch reverses ParsePatch: it turns a slice of PatchOp into the
// canonical *** Begin Patch / *** End Patch envelope text. Used by the
// runner-side pre-flight when it needs to drop redundant sections from a
// model-issued patch and re-submit the trimmed envelope to applyPatch
// without changing applyPatch's semantics.
func SerializePatch(ops []PatchOp) string {
	return patching.Serialize(ops)
}

func PatchOpPaths(ops []PatchOp) []string {
	return patching.OpPaths(ops)
}

func (m *Manager) applyPatch(req ToolRequest) ToolResult {
	if !m.cfg.EnableWrites {
		return toolWriteDisabledResult("apply_patch", "Patch request rejected", "workspace writes are disabled")
	}

	body := patching.NormalizeEnvelope(req.Content)
	if strings.TrimSpace(body) == "" {
		return toolInvalidArgumentResult("apply_patch", "Patch request rejected", "apply_patch requires the patch body in content")
	}

	ops, err := ParsePatch(body)
	if err != nil {
		return toolFailureFromErrorResult("apply_patch", "Patch parse failed", err, failures.InvalidArgument, reasonToolInvalidArgument)
	}

	type plannedWrite struct {
		path     string
		contents []byte
		remove   bool
	}

	planned := make([]plannedWrite, 0, len(ops))
	summaries := make([]string, 0, len(ops))

	for opIdx, op := range ops {
		path, err := m.resolvePath(op.Path, req.Workdir)
		if err != nil {
			return toolFailureFromErrorResult("apply_patch", "Failed to resolve path", fmt.Errorf("op %d (%s %s): %w", opIdx+1, op.Kind, op.Path, err), failures.InvalidArgument, reasonToolPathResolutionFailed)
		}

		switch op.Kind {
		case PatchOpAdd:
			if _, statErr := os.Stat(path); statErr == nil {
				return toolFailureResult("apply_patch", "Patch request rejected", fmt.Sprintf("Add File target %s already exists", m.displayPath(path)), failures.AlreadyExists, reasonPlanSubmissionRejected)
			}

			body := op.NewBody
			if body != "" && !strings.HasSuffix(body, "\n") {
				body += "\n"
			}

			planned = append(planned, plannedWrite{path: path, contents: []byte(body)})
			summaries = append(summaries, fmt.Sprintf("add %s (%d bytes)", m.displayPath(path), len(body)))
		case PatchOpDelete:
			info, statErr := os.Stat(path)
			if statErr != nil {
				return toolFailureResult("apply_patch", "Patch request rejected", fmt.Sprintf("Delete File target %s not found", m.displayPath(path)), failures.NotFound, reasonPlanSubmissionRejected)
			}

			if info.IsDir() {
				return toolInvalidArgumentResult("apply_patch", "Patch request rejected", fmt.Sprintf("Delete File target %s is a directory", m.displayPath(path)))
			}

			planned = append(planned, plannedWrite{path: path, remove: true})
			summaries = append(summaries, fmt.Sprintf("delete %s", m.displayPath(path)))
		case PatchOpUpdate:
			current, readErr := os.ReadFile(path)
			if readErr != nil {
				return toolFailureFromErrorResult("apply_patch", "Patch request rejected", fmt.Errorf("Update File target %s: %w", m.displayPath(path), readErr), failures.NotFound, reasonPlanSubmissionRejected)
			}

			updated, applyErr := patching.ApplyUpdateHunks(string(current), op.Hunks)
			if applyErr != nil {
				return toolFailureFromErrorResult("apply_patch", "Patch hunk did not apply", fmt.Errorf("Update File %s: %w", m.displayPath(path), applyErr), failures.FailedPrecondition, reasonPlanSubmissionRejected)
			}

			destPath := path
			summary := fmt.Sprintf("update %s", m.displayPath(path))

			if op.MoveTo != "" {
				destPath, err = m.resolvePath(op.MoveTo, req.Workdir)
				if err != nil {
					return toolFailureFromErrorResult("apply_patch", "Failed to resolve move destination", fmt.Errorf("Move to %s: %w", op.MoveTo, err), failures.InvalidArgument, reasonToolPathResolutionFailed)
				}

				if _, statErr := os.Stat(destPath); statErr == nil && filepath.Clean(destPath) != filepath.Clean(path) {
					return toolFailureResult("apply_patch", "Patch request rejected", fmt.Sprintf("Move to destination %s already exists", m.displayPath(destPath)), failures.AlreadyExists, reasonPlanSubmissionRejected)
				}

				planned = append(planned, plannedWrite{path: path, remove: true})
				summary = fmt.Sprintf("update+move %s -> %s", m.displayPath(path), m.displayPath(destPath))
			}

			planned = append(planned, plannedWrite{path: destPath, contents: []byte(updated)})
			summaries = append(summaries, summary)
		}
	}

	for _, write := range planned {
		if write.remove {
			continue
		}

		if rejected := m.rejectInvalidPlannedWrite("apply_patch", "Patch request rejected", write.path, write.contents); rejected != nil {
			return *rejected
		}
	}

	for _, write := range planned {
		if write.remove {
			if err := os.Remove(write.path); err != nil && !os.IsNotExist(err) {
				return toolFailureFromErrorResult("apply_patch", "Patch apply failed mid-stream", fmt.Errorf("delete %s: %w", m.displayPath(write.path), err), failures.Internal, reasonToolWriteFailed)
			}

			continue
		}

		if err := os.MkdirAll(filepath.Dir(write.path), 0o755); err != nil {
			return toolFailureFromErrorResult("apply_patch", "Patch apply failed", fmt.Errorf("mkdir %s: %w", m.displayPath(filepath.Dir(write.path)), err), failures.Internal, reasonToolWriteFailed)
		}

		if err := os.WriteFile(write.path, write.contents, 0o644); err != nil {
			return toolFailureFromErrorResult("apply_patch", "Patch apply failed", fmt.Errorf("write %s: %w", m.displayPath(write.path), err), failures.Internal, reasonToolWriteFailed)
		}
	}

	m.invalidateGoIndex()
	m.invalidateCppIndex()

	return ToolResult{
		Tool:    "apply_patch",
		Summary: fmt.Sprintf("Applied patch with %d op(s)", len(ops)),
		Output:  strings.Join(summaries, "\n"),
	}
}
