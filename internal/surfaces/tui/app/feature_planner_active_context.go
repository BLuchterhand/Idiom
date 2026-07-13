package app

import (
	"encoding/json"
	"fmt"
	"strings"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/workflows/featureplanner"
)

const featurePlannerPlanArtifactPayloadType = "feature_planner_plan_artifact"

type featurePlannerPlanArtifactPayload struct {
	StatePath     string   `json:"state_path,omitempty"`
	PlanPath      string   `json:"plan_path,omitempty"`
	Summary       string   `json:"summary,omitempty"`
	Objective     string   `json:"objective,omitempty"`
	ProposedPaths []string `json:"proposed_paths,omitempty"`
}

func (m *promptExecutionSession) activeFeaturePlannerPlanContext() string {
	state, statePath, plan, ok := m.activeFeaturePlannerPlanState()
	if !ok {
		return ""
	}

	paths := featureplanner.ActiveFeaturePlanProposedPaths(state, plan)
	var b strings.Builder

	b.WriteString("Active feature-planner plan artifact. This is proposed plan evidence, not current source evidence.\n")
	if statePath = strings.TrimSpace(statePath); statePath != "" {
		fmt.Fprintf(&b, "State: %s\n", statePath)
	}
	if planPath := strings.TrimSpace(state.PlanPath); planPath != "" {
		fmt.Fprintf(&b, "Plan tab: %s\n", planPath)
	}
	if objective := strings.TrimSpace(firstNonEmptyFeaturePlannerPlanText(plan.TopLevelScope.Objective, plan.Summary)); objective != "" {
		fmt.Fprintf(&b, "Objective: %s\n", featurePlannerInlineMarkdown(objective))
	}
	if summary := strings.TrimSpace(plan.Summary); summary != "" {
		fmt.Fprintf(&b, "Summary: %s\n", featurePlannerInlineMarkdown(summary))
	}
	if len(paths) > 0 {
		b.WriteString("Proposed paths to create:\n")
		for _, path := range paths {
			fmt.Fprintf(&b, "- %s\n", path)
		}
	}
	if atoms := featurePlannerApprovalAtomSummaries(plan.Atoms, 4); len(atoms) > 0 {
		fmt.Fprintf(&b, "Work breakdown: %s\n", strings.Join(atoms, "; "))
	}
	if commands := featurePlannerApprovalVerificationCommands(plan, 3); len(commands) > 0 {
		fmt.Fprintf(&b, "Proposed verification: %s\n", strings.Join(commands, "; "))
	}
	b.WriteString("When answering questions about this plan, describe these paths as proposed/planned files, not existing source files.")

	return strings.TrimSpace(b.String())
}

func (m *promptExecutionSession) activeFeaturePlannerPlanSeedResult() (workspace.ToolResult, bool) {
	state, statePath, plan, ok := m.activeFeaturePlannerPlanState()
	if !ok {
		return workspace.ToolResult{}, false
	}

	paths := featureplanner.ActiveFeaturePlanProposedPaths(state, plan)
	if len(paths) == 0 {
		return workspace.ToolResult{}, false
	}

	payload := featurePlannerPlanArtifactPayload{
		StatePath:     statePath,
		PlanPath:      strings.TrimSpace(state.PlanPath),
		Summary:       strings.TrimSpace(plan.Summary),
		Objective:     strings.TrimSpace(firstNonEmptyFeaturePlannerPlanText(plan.TopLevelScope.Objective, plan.Summary)),
		ProposedPaths: paths,
	}
	raw, _ := json.Marshal(payload)

	return workspace.ToolResult{
		Tool:        featurePlannerPlanArtifactPayloadType,
		Summary:     "Active feature-planner plan artifact with proposed paths: " + strings.Join(paths, ", "),
		Output:      m.activeFeaturePlannerPlanContext(),
		PayloadType: featurePlannerPlanArtifactPayloadType,
		Payload:     raw,
	}, true
}

func (m *promptExecutionSession) activeFeaturePlannerPlanState() (featureplanner.FeatureState, string, featureplanner.FeaturePlan, bool) {
	if m == nil {
		return featureplanner.FeatureState{}, "", featureplanner.FeaturePlan{}, false
	}

	sessionID := strings.TrimSpace(m.directFeaturePlannerSessionID)
	if sessionID == "" {
		return featureplanner.FeatureState{}, "", featureplanner.FeaturePlan{}, false
	}

	state, statePath, err := featureplanner.LoadState(resolveProjectRoot(m.cfg.Chat.ProjectRoot, m.workspace), sessionID)
	if err != nil || featureplanner.CountOpenPlannerQuestions(state) == 0 {
		return featureplanner.FeatureState{}, "", featureplanner.FeaturePlan{}, false
	}

	plan, _, ok := featurePlannerPlanTabCandidate(state)
	if !ok {
		return featureplanner.FeatureState{}, "", featureplanner.FeaturePlan{}, false
	}

	return state, statePath, plan, true
}
