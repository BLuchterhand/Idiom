package planexecute

import (
	"strings"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/pathparse"
	"idiom/internal/workflows/workflowstate"
)

const planExecuteExpectedArtifactExactPlan = "exact_plan_edits"

type PlanExecuteDispatchState struct {
	WorkflowID            workflowstate.WorkflowID `json:"workflow_id"`
	State                 workflowstate.StateID    `json:"state"`
	Dispatchable          bool                     `json:"dispatchable"`
	ExpectedArtifact      string                   `json:"expected_artifact,omitempty"`
	LegacyHeadingFallback bool                     `json:"legacy_heading_fallback"`
	Reason                string                   `json:"reason,omitempty"`
}

func planExecuteExactPlanEdits(prompt string, requiredEditPaths []string) ([]workspace.PlanEdit, string) {
	return planExecuteExactPlanEditsForWorkspace(prompt, requiredEditPaths, "")
}

func planExecuteExactPlanEditsForWorkspace(prompt string, requiredEditPaths []string, workspaceRoot string) ([]workspace.PlanEdit, string) {
	required := planExecuteUniqueRequiredPaths(requiredEditPaths)
	state := planExecuteExactPlanDispatchState(required)

	return planExecuteExactPlanEditsForWorkspaceWithDispatchState(prompt, required, workspaceRoot, state)
}

func planExecuteExactPlanEditsForWorkspaceWithDispatchState(prompt string, required []string, workspaceRoot string, state PlanExecuteDispatchState) ([]workspace.PlanEdit, string) {
	required = planExecuteUniqueRequiredPaths(required)
	state = normalizePlanExecuteDispatchState(state)
	if !state.Dispatchable {
		return nil, firstPlanExecuteTerminalString(state.Reason, "plan-execute dispatch state is not dispatchable")
	}
	if len(required) == 0 {
		return nil, "no required_edit_paths"
	}
	section := ""

	if planExecuteDispatchStateAllowsLegacyHeadingScan(state) {
		for _, candidatePrompt := range planExecuteAutoPlanScanTexts(prompt) {
			for _, heading := range []string{"required edits", "edits to apply", "edits"} {
				if candidate := atomMarkdownSectionByHeading(candidatePrompt, heading); strings.TrimSpace(candidate) != "" {
					section = candidate
					break
				}
			}

			if strings.TrimSpace(section) != "" {
				break
			}
		}
	}

	if strings.TrimSpace(section) == "" {
		return planExecuteImplicitNewFileBodyEdits(prompt, required, workspaceRoot)
	}

	edits, reason := planExecuteExactPlanEditsFromSection(section, required, workspaceRoot)
	if len(edits) > 0 {
		return edits, ""
	}

	if implicit, implicitReason := planExecuteImplicitNewFileBodyEdits(prompt, required, workspaceRoot); len(implicit) > 0 {
		return implicit, ""
	} else if strings.TrimSpace(implicitReason) != "" && reason == "no parseable exact old_text/new_text pairs" {
		reason = reason + "; " + implicitReason
	}

	return nil, reason
}

func planExecuteExactPlanDispatchState(required []string) PlanExecuteDispatchState {
	if len(planExecuteUniqueRequiredPaths(required)) == 0 {
		return PlanExecuteDispatchState{
			WorkflowID: workflowstate.WorkflowPlanExecutePlanner,
			State:      workflowstate.StateTerminal,
			Reason:     "no required_edit_paths",
		}
	}

	return PlanExecuteDispatchState{
		WorkflowID:            workflowstate.WorkflowPlanExecutePlanner,
		State:                 workflowstate.StateDispatchable,
		Dispatchable:          true,
		ExpectedArtifact:      planExecuteExpectedArtifactExactPlan,
		LegacyHeadingFallback: true,
	}
}

func normalizePlanExecuteDispatchState(state PlanExecuteDispatchState) PlanExecuteDispatchState {
	if state.WorkflowID == "" {
		state.WorkflowID = workflowstate.WorkflowPlanExecutePlanner
	}
	state.ExpectedArtifact = strings.TrimSpace(state.ExpectedArtifact)
	state.Reason = strings.TrimSpace(state.Reason)

	return state
}

func planExecuteDispatchStateAllowsLegacyHeadingScan(state PlanExecuteDispatchState) bool {
	state = normalizePlanExecuteDispatchState(state)
	return state.Dispatchable &&
		state.LegacyHeadingFallback &&
		state.WorkflowID == workflowstate.WorkflowPlanExecutePlanner &&
		state.State == workflowstate.StateDispatchable &&
		state.ExpectedArtifact == planExecuteExpectedArtifactExactPlan
}

func planExecuteExactPlanEditsFromSection(section string, required []string, workspaceRoot string) ([]workspace.PlanEdit, string) {
	type pendingOldBlock struct {
		paths []string
		text  string
	}

	var (
		edits        []workspace.PlanEdit
		pending      pendingOldBlock
		current      []string
		currentPaths []string
		blockPaths   []string
	)

	blockKind := ""
	previousNonEmpty := ""
	fence := ""

	for _, rawLine := range strings.Split(section, "\n") {
		trimmed := strings.TrimSpace(rawLine)
		if fence == "" {
			if marker, _, ok := atomMarkdownFenceOpen(trimmed); ok {
				fence = marker
				current = nil

				blockPaths = append([]string(nil), currentPaths...)
				blockKind = planExecuteExactEditBlockKind(previousNonEmpty)

				continue
			}

			if trimmed != "" {
				if paths := atomRequiredEditPathsFromLabel(trimmed, required); len(paths) > 0 {
					currentPaths = paths
				}

				previousNonEmpty = trimmed
			}

			continue
		}

		if strings.HasPrefix(trimmed, fence) {
			block := strings.TrimSpace(strings.Join(current, "\n"))

			switch blockKind {
			case "old":
				pending = pendingOldBlock{paths: append([]string(nil), blockPaths...), text: block}
			case "new":
				paths := pending.paths
				if len(paths) == 0 {
					paths = blockPaths
				}

				if len(paths) == 0 && len(required) == 1 {
					paths = required
				}

				if len(paths) == 0 {
					paths = planExecuteInferPathsForOldText(workspaceRoot, required, pending.text)
				}

				for _, path := range paths {
					path = strings.TrimSpace(path)
					if path == "" || strings.TrimSpace(pending.text) == "" {
						continue
					}

					edits = append(edits, workspace.PlanEdit{
						Path:    path,
						OldText: pending.text,
						NewText: block,
						Purpose: "Apply the exact old_text/new_text edit from the Required Edits packet.",
					})
				}

				pending = pendingOldBlock{}
			case "replace_all":
				paths := blockPaths
				if len(paths) == 0 && len(required) == 1 {
					paths = required
				}

				for _, path := range paths {
					path = strings.TrimSpace(path)
					if path == "" || strings.TrimSpace(block) == "" {
						continue
					}

					edits = append(edits, workspace.PlanEdit{
						Path:       path,
						OldText:    "",
						NewText:    block,
						ReplaceAll: true,
						Purpose:    "Create or replace the full file body from the Required Edits packet.",
					})
				}
			}

			fence = ""
			current = nil
			blockPaths = nil
			blockKind = ""
			previousNonEmpty = ""

			continue
		}

		current = append(current, rawLine)
	}

	if len(edits) == 0 {
		return nil, "no parseable exact old_text/new_text pairs"
	}

	missing := planExecuteMissingEditPaths(required, edits)
	if len(missing) > 0 {
		return nil, "exact old_text/new_text pairs do not cover required path(s): " + strings.Join(missing, ", ")
	}

	return edits, ""
}

func planExecuteImplicitNewFileBodyEdits(prompt string, required []string, workspaceRoot string) ([]workspace.PlanEdit, string) {
	for _, candidatePrompt := range planExecuteAutoPlanScanTexts(prompt) {
		section := atomMarkdownSectionByHeading(candidatePrompt, "required behavior")
		if strings.TrimSpace(section) == "" {
			continue
		}

		edits := planExecuteImplicitNewFileBodyEditsFromSection(section, required, workspaceRoot)
		if len(edits) == 0 {
			continue
		}

		if missing := planExecuteMissingEditPaths(required, edits); len(missing) > 0 {
			return nil, "implicit new-file bodies do not cover required path(s): " + strings.Join(missing, ", ")
		}

		return edits, ""
	}

	return nil, "no exact-edit section"
}

func planExecuteImplicitNewFileBodyEditsFromSection(section string, required []string, workspaceRoot string) []workspace.PlanEdit {
	var (
		edits      []workspace.PlanEdit
		recent     []string
		current    []string
		blockPaths []string
	)

	capture := false
	fence := ""

	for _, rawLine := range strings.Split(section, "\n") {
		trimmed := strings.TrimSpace(rawLine)
		if fence == "" {
			if marker, _, ok := atomMarkdownFenceOpen(trimmed); ok {
				fence = marker
				current = nil

				contextText := strings.Join(recent, "\n")

				blockPaths = planExecuteImplicitBodyBoundPaths(recent, required)
				capture = len(blockPaths) > 0 &&
					planExecuteImplicitNewFileBodyContext(contextText) &&
					planExecuteAllPathsMissing(workspaceRoot, blockPaths)

				continue
			}

			if trimmed != "" {
				recent = append(recent, trimmed)
				if len(recent) > 8 {
					recent = recent[len(recent)-8:]
				}
			}

			continue
		}

		if strings.HasPrefix(trimmed, fence) {
			block := strings.TrimSpace(strings.Join(current, "\n"))

			if capture {
				for _, path := range blockPaths {
					if planExecuteImplicitBodyLooksSafeForPath(path, block) {
						edits = append(edits, workspace.PlanEdit{
							Path:       path,
							OldText:    "",
							NewText:    block,
							ReplaceAll: true,
							Purpose:    "Create the full file body from the refined design-doc atom.",
						})
					}
				}
			}

			fence = ""
			current = nil
			blockPaths = nil
			capture = false

			continue
		}

		current = append(current, rawLine)
	}

	return edits
}

func planExecuteImplicitBodyBoundPaths(recent []string, required []string) []string {
	for i := len(recent) - 1; i >= 0; i-- {
		line := strings.TrimSpace(recent[i])
		if len(pathparse.MentionedWorkspacePaths(line)) == 0 {
			continue
		}

		return atomRequiredEditPathsFromLabel(line, required)
	}

	return atomRequiredEditPathsFromLabel(strings.Join(recent, "\n"), required)
}
