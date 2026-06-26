package featureplanner

import "strings"

type FeaturePlanProjectionCandidate struct {
	Plan              FeaturePlan
	RejectedCandidate RejectedPlanCandidate
}

type FeaturePlanProposedFileGroups struct {
	RequiredEditPaths []string
	AllowedEditPaths  []string
	TestArtifactPaths []string
}

func SelectFeaturePlanProjectionCandidate(state FeatureState) (FeaturePlanProjectionCandidate, bool) {
	if featurePlanHasDisplayableContent(state.Plan) && !featureStatePrefersRejectedCandidate(state.Status) {
		return FeaturePlanProjectionCandidate{Plan: state.Plan}, true
	}

	if len(state.RejectedPlanCandidates) > 0 {
		candidate := state.RejectedPlanCandidates[len(state.RejectedPlanCandidates)-1]
		if featurePlanHasDisplayableContent(candidate.Plan) {
			return FeaturePlanProjectionCandidate{Plan: candidate.Plan, RejectedCandidate: candidate}, true
		}
	}

	if featurePlanHasDisplayableContent(state.Plan) {
		return FeaturePlanProjectionCandidate{Plan: state.Plan}, true
	}

	return FeaturePlanProjectionCandidate{}, false
}

func FeaturePlanHasDisplayableContent(plan FeaturePlan) bool {
	return featurePlanHasDisplayableContent(plan)
}

func featurePlanHasDisplayableContent(plan FeaturePlan) bool {
	return strings.TrimSpace(plan.TopLevelScope.Objective) != "" ||
		strings.TrimSpace(plan.SourceTask.Summary) != "" ||
		len(plan.TopLevelScope.SuccessCriteria) > 0 ||
		len(plan.Atoms) > 0
}

func featureStatePrefersRejectedCandidate(status string) bool {
	switch strings.TrimSpace(status) {
	case FeatureStatePlanningQuestion,
		FeatureStatePlanningRepair,
		FeatureStatePlanningStuck,
		FeatureStateStaleEditAnchor,
		FeatureStateMalformedOutput:
		return true
	default:
		return false
	}
}

func FeaturePlanProposedFiles(plan FeaturePlan) FeaturePlanProposedFileGroups {
	var groups FeaturePlanProposedFileGroups
	for _, atom := range plan.Atoms {
		groups.RequiredEditPaths = append(groups.RequiredEditPaths, atom.RequiredEditPaths...)
		groups.AllowedEditPaths = append(groups.AllowedEditPaths, atom.AllowedEditPaths...)
		groups.TestArtifactPaths = append(groups.TestArtifactPaths, atom.TestFirst.TestArtifactPaths...)
		groups.TestArtifactPaths = append(groups.TestArtifactPaths, atom.PlannedTDDSnapshot.TestArtifactPaths...)
	}

	groups.RequiredEditPaths = normalizedStringList(groups.RequiredEditPaths)
	groups.AllowedEditPaths = normalizedStringList(groups.AllowedEditPaths)
	groups.TestArtifactPaths = normalizedStringList(groups.TestArtifactPaths)

	return groups
}

func FeaturePlanProposedPaths(plan FeaturePlan) []string {
	groups := FeaturePlanProposedFiles(plan)
	return normalizedStringList(append(append(append([]string(nil), groups.RequiredEditPaths...), groups.AllowedEditPaths...), groups.TestArtifactPaths...))
}

func ActiveFeaturePlanProposedPaths(state FeatureState, plan FeaturePlan) []string {
	var paths []string
	for _, question := range state.OpenQuestions {
		if plannerQuestionIsOpen(question) {
			paths = append(paths, question.AffectedPaths...)
		}
	}
	paths = append(paths, FeaturePlanProposedPaths(plan)...)

	return normalizedStringList(paths)
}

func plannerQuestionIsOpen(question PlannerQuestion) bool {
	status := strings.TrimSpace(question.Status)
	return status == "" || strings.EqualFold(status, "open")
}
