package workspace

import (
	"context"
	"fmt"
	"strings"

	"idiom/internal/capabilities/workspace/patching"
	"idiom/internal/platform/failures"
)

type plannedPatchMutationKind uint8

const (
	plannedPatchReplace plannedPatchMutationKind = iota
	plannedPatchRemove
)

type plannedPatchMutation struct {
	kind           plannedPatchMutationKind
	operationIndex int
	description    string
	target         workspaceMutationTarget
	preimage       workspaceMutationPreimage
	contents       []byte
}

func (m *Manager) applyPatch(ctx context.Context, req ToolRequest) ToolResult {
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

	mutation, err := m.pathAccess.beginWorkspaceMutation(ctx, req)
	if err != nil {
		return toolFailureFromErrorResult(ToolNameApplyPatch, "Failed to resolve path", err, failures.InvalidArgument, reasonToolPathResolutionFailed)
	}
	defer mutation.Close()

	planned := make([]plannedPatchMutation, 0, len(ops)*2)
	summaries := make([]string, 0, len(ops))
	var changedPaths []string

	for opIdx, op := range ops {
		operationIndex := opIdx + 1
		target, err := mutation.Resolve(op.Path)
		if err != nil {
			return toolFailureFromErrorResult(ToolNameApplyPatch, "Failed to resolve path", fmt.Errorf("op %d (%s %s): %w", operationIndex, op.Kind, op.Path, err), failures.InvalidArgument, reasonToolPathResolutionFailed)
		}
		preimage, err := mutation.Observe(target)
		if err != nil {
			return toolFailureFromErrorResult(ToolNameApplyPatch, "Patch request rejected", fmt.Errorf("inspect %s: %w", target.display, err), failures.FailedPrecondition, reasonPlanSubmissionRejected)
		}

		switch op.Kind {
		case patching.OpAdd:
			if preimage.exists {
				return toolFailureResult(ToolNameApplyPatch, "Patch request rejected", fmt.Sprintf("Add File target %s already exists", target.display), failures.AlreadyExists, reasonPlanSubmissionRejected)
			}

			newBody := op.NewBody
			if newBody != "" && !strings.HasSuffix(newBody, "\n") {
				newBody += "\n"
			}
			planned = append(planned, plannedPatchMutation{
				kind:           plannedPatchReplace,
				operationIndex: operationIndex,
				description:    fmt.Sprintf("add %s", target.display),
				target:         target,
				preimage:       preimage,
				contents:       []byte(newBody),
			})
			summaries = append(summaries, fmt.Sprintf("add %s (%d bytes)", target.display, len(newBody)))
			changedPaths = appendUniqueWorkspaceStrings(changedPaths, target.display)

		case patching.OpDelete:
			if !preimage.exists {
				return toolFailureResult(ToolNameApplyPatch, "Patch request rejected", fmt.Sprintf("Delete File target %s not found", target.display), failures.NotFound, reasonPlanSubmissionRejected)
			}

			planned = append(planned, plannedPatchMutation{
				kind:           plannedPatchRemove,
				operationIndex: operationIndex,
				description:    fmt.Sprintf("delete %s", target.display),
				target:         target,
				preimage:       preimage,
			})
			summaries = append(summaries, fmt.Sprintf("delete %s", target.display))
			changedPaths = appendUniqueWorkspaceStrings(changedPaths, target.display)

		case patching.OpUpdate:
			if !preimage.exists {
				return toolFailureResult(ToolNameApplyPatch, "Patch request rejected", fmt.Sprintf("update file target %s not found", target.display), failures.NotFound, reasonPlanSubmissionRejected)
			}

			updated, applyErr := patching.ApplyUpdateHunks(string(preimage.body), op.Hunks)
			if applyErr != nil {
				return toolFailureFromErrorResult(ToolNameApplyPatch, "Patch hunk did not apply", fmt.Errorf("update file %s: %w", target.display, applyErr), failures.FailedPrecondition, reasonPlanSubmissionRejected)
			}

			destination := target
			destinationPreimage := preimage
			summary := fmt.Sprintf("update %s", target.display)
			moved := false
			if strings.TrimSpace(op.MoveTo) != "" {
				destination, err = mutation.Resolve(op.MoveTo)
				if err != nil {
					return toolFailureFromErrorResult(ToolNameApplyPatch, "Failed to resolve move destination", fmt.Errorf("move to %s: %w", op.MoveTo, err), failures.InvalidArgument, reasonToolPathResolutionFailed)
				}
				moved = destination.relative != target.relative
				if moved {
					destinationPreimage, err = mutation.Observe(destination)
					if err != nil {
						return toolFailureFromErrorResult(ToolNameApplyPatch, "Patch request rejected", fmt.Errorf("inspect move destination %s: %w", destination.display, err), failures.FailedPrecondition, reasonPlanSubmissionRejected)
					}
					if destinationPreimage.exists {
						return toolFailureResult(ToolNameApplyPatch, "Patch request rejected", fmt.Sprintf("Move to destination %s already exists", destination.display), failures.AlreadyExists, reasonPlanSubmissionRejected)
					}
					summary = fmt.Sprintf("update+move %s -> %s", target.display, destination.display)
					changedPaths = appendUniqueWorkspaceStrings(changedPaths, target.display, destination.display)
				}
			}
			if !moved {
				changedPaths = appendUniqueWorkspaceStrings(changedPaths, target.display)
			}

			planned = append(planned, plannedPatchMutation{
				kind:           plannedPatchReplace,
				operationIndex: operationIndex,
				description:    summary,
				target:         destination,
				preimage:       destinationPreimage,
				contents:       []byte(updated),
			})
			if moved {
				planned = append(planned, plannedPatchMutation{
					kind:           plannedPatchRemove,
					operationIndex: operationIndex,
					description:    fmt.Sprintf("remove moved source %s", target.display),
					target:         target,
					preimage:       preimage,
				})
			}
			summaries = append(summaries, summary)
		}
	}

	for _, action := range planned {
		if action.kind != plannedPatchReplace {
			continue
		}
		if rejected := m.writeGuard.RejectPlannedWrite(ToolNameApplyPatch, "Patch request rejected", action.target.absolute, action.preimage.body, action.contents, m.pathAccess.DisplayPath); rejected != nil {
			return *rejected
		}
	}

	ordered := orderPlannedPatchMutations(planned)
	for actionIndex, action := range ordered {
		mutation.reachCheckpoint(workspaceMutationAfterPreflight, action.target, action.operationIndex)
		var mutationErr error
		switch action.kind {
		case plannedPatchReplace:
			mutationErr = mutation.Replace(action.target, action.preimage, action.contents, 0o644, action.operationIndex)
		case plannedPatchRemove:
			mutationErr = mutation.Remove(action.target, action.preimage, action.operationIndex)
		}
		if mutationErr == nil {
			continue
		}
		remainingTargets := make([]workspaceMutationTarget, 0, len(ordered)-actionIndex-1)
		for _, unattempted := range ordered[actionIndex+1:] {
			remainingTargets = append(remainingTargets, unattempted.target)
		}
		mutation.setFailureIntent(action.target, remainingTargets)

		remaining := make([]string, 0, len(ordered)-actionIndex-1)
		for _, unattempted := range ordered[actionIndex+1:] {
			remaining = append(remaining, unattempted.description)
		}
		return m.workspaceMutationFailureResult(
			ToolNameApplyPatch,
			"Patch apply failed",
			"Patch partially applied",
			fmt.Errorf("%s: %w", action.description, mutationErr),
			mutation,
			failures.Internal,
			reasonToolWriteFailed,
			remaining,
		)
	}

	m.resetLanguageTools()

	result := ToolResult{
		Tool:    ToolNameApplyPatch,
		Summary: fmt.Sprintf("Applied patch with %d op(s)", len(ops)),
		Output:  strings.Join(summaries, "\n"),
	}
	mutation.attachCommitReceipt(ToolNameApplyPatch, &result)
	return result
}

func orderPlannedPatchMutations(planned []plannedPatchMutation) []plannedPatchMutation {
	ordered := make([]plannedPatchMutation, 0, len(planned))
	for _, action := range planned {
		if action.kind == plannedPatchReplace {
			ordered = append(ordered, action)
		}
	}
	for _, action := range planned {
		if action.kind == plannedPatchRemove {
			ordered = append(ordered, action)
		}
	}

	return ordered
}

func appendUniqueWorkspaceStrings(values []string, additions ...string) []string {
	seen := make(map[string]struct{}, len(values)+len(additions))
	out := make([]string, 0, len(values)+len(additions))
	for _, value := range append(append([]string(nil), values...), additions...) {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, duplicate := seen[value]; duplicate {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
