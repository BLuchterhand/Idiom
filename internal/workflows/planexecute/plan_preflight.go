package planexecute

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"idiom/internal/contracts/autonomy"
	plancontract "idiom/internal/contracts/plan"
	planartifact "idiom/internal/contracts/planartifact"
)

func preflightPlanApprovalAndScope(plan planartifact.PlanArtifact, requiredEditPaths []string) *PlanEditPreflightMismatch {
	if strings.TrimSpace(plan.Handoff.ID) == "" {
		return &PlanEditPreflightMismatch{Index: -1, Reason: "handoff id is missing"}
	}

	if !plan.Handoff.Ready {
		return &PlanEditPreflightMismatch{Index: -1, Reason: "handoff is not ready"}
	}

	if len(plan.Handoff.Blockers) > 0 {
		return &PlanEditPreflightMismatch{Index: -1, Reason: "handoff has unresolved blockers"}
	}

	if plan.Approval.Required {
		if planartifact.IsApprovalStatusPartial(string(plan.Approval.Status)) {
			if mismatch := preflightPlanPartialApprovalScope(plan); mismatch != nil {
				return mismatch
			}
		} else if !planartifact.ApprovalStatusSatisfied(string(plan.Approval.Status)) {
			return &PlanEditPreflightMismatch{Index: -1, Reason: "required approval is not satisfied"}
		}
	}

	if plan.Approval.ExecutionPermission == planartifact.ExecutionPermissionBlocked {
		return &PlanEditPreflightMismatch{Index: -1, Reason: "approval execution permission is blocked"}
	}

	return preflightPlanDeclaredScope(plan, requiredEditPaths)
}

func preflightPlanPartialApprovalScope(plan planartifact.PlanArtifact) *PlanEditPreflightMismatch {
	if strings.TrimSpace(plan.Approval.ScopeID) == "" {
		return &PlanEditPreflightMismatch{Index: -1, Reason: "partial approval scope id is missing"}
	}

	if len(plan.Approval.ApprovedPaths) == 0 {
		return &PlanEditPreflightMismatch{Index: -1, Reason: "partial approval has no approved scope paths"}
	}

	if len(plan.Approval.PendingPaths) == 0 {
		return &PlanEditPreflightMismatch{Index: -1, Reason: "partial approval has no pending scope paths"}
	}

	if plan.Approval.ExecutionPermission != planartifact.ExecutionPermissionExecuteApprovedScope {
		return &PlanEditPreflightMismatch{Index: -1, Reason: "partial approval must execute only approved scope"}
	}

	approved := planExecutePathSet(plan.Approval.ApprovedPaths)
	pending := planExecutePathSet(plan.Approval.PendingPaths)

	for i, edit := range plan.Edits {
		path := plancontract.NormalizeScopePath(edit.Path)
		if path == "" {
			return &PlanEditPreflightMismatch{Index: i, Reason: "edit has no path"}
		}

		if _, ok := pending[path]; ok {
			return &PlanEditPreflightMismatch{Index: i, Path: path, Reason: "plan edit path is still pending approval"}
		}

		if _, ok := approved[path]; !ok {
			return &PlanEditPreflightMismatch{Index: i, Path: path, Reason: "plan edit path is outside approved scope"}
		}
	}

	return nil
}

func preflightPlanDeclaredScope(plan planartifact.PlanArtifact, originalRequiredPaths ...[]string) *PlanEditPreflightMismatch {
	forbidden := planExecutePathSet(append(append([]string(nil), plan.Paths.Forbidden...), plan.Contract.ForbiddenPaths...))
	required := planExecutePathSet(append(append([]string(nil), plan.Paths.Required...), plan.Contract.RequiredEditPaths...))

	for _, paths := range originalRequiredPaths {
		for _, path := range paths {
			if normalized := plancontract.NormalizeScopePath(path); normalized != "" {
				required[normalized] = struct{}{}
			}
		}
	}

	planned := planExecutePathSet(append(append([]string(nil), plan.Paths.Editable...), plan.Paths.Changed...))
	approved := planExecutePathSet(plan.Approval.ApprovedPaths)
	pending := planExecutePathSet(plan.Approval.PendingPaths)
	sourceSatisfiedViolations := plancontract.ValidateSourceSatisfiedPathCoverage(plancontract.SourceSatisfiedPathCoverageInput{
		RequiredPaths:        planExecuteSetStrings(required),
		SourceSatisfiedPaths: plan.Paths.SourceSatisfied,
		Evidence:             planartifact.SourceSatisfiedEvidenceRefs(plan),
		ApprovedPaths:        plan.Approval.ApprovedPaths,
		PendingPaths:         plan.Approval.PendingPaths,
		RequireEvidence:      true,
	})

	if mismatch := preflightPlanSourceSatisfiedViolationMismatch(sourceSatisfiedViolations); mismatch != nil {
		if mismatch.Reason == "source-satisfied path is outside declared required scope" {
			mismatch.Reason = "source-satisfied path is outside required plan scope"
		}

		return mismatch
	}

	seen := map[string]struct{}{}

	for path := range planExecutePathSet(plan.Paths.SourceSatisfied) {
		if _, ok := forbidden[path]; ok {
			return &PlanEditPreflightMismatch{Index: -1, Path: path, Reason: "source-satisfied path is forbidden"}
		}

		seen[path] = struct{}{}
	}

	for i, edit := range plan.Edits {
		path := plancontract.NormalizeScopePath(edit.Path)
		if path == "" {
			return &PlanEditPreflightMismatch{Index: i, Reason: "edit has no path"}
		}

		seen[path] = struct{}{}
		if _, ok := forbidden[path]; ok {
			return &PlanEditPreflightMismatch{Index: i, Path: path, Reason: "plan edit path is forbidden"}
		}

		if _, ok := pending[path]; ok {
			return &PlanEditPreflightMismatch{Index: i, Path: path, Reason: "plan edit path is still pending approval"}
		}

		if len(approved) > 0 {
			if _, ok := approved[path]; !ok {
				return &PlanEditPreflightMismatch{Index: i, Path: path, Reason: "plan edit path is outside approved scope"}
			}
		}

		if len(required) > 0 {
			if _, ok := required[path]; !ok {
				return &PlanEditPreflightMismatch{Index: i, Path: path, Reason: "plan edit path is outside required plan scope"}
			}
		}

		if len(planned) > 0 {
			if _, ok := planned[path]; !ok {
				return &PlanEditPreflightMismatch{Index: i, Path: path, Reason: "plan edit path is outside declared editable scope"}
			}
		}
	}

	for path := range required {
		if _, ok := seen[path]; !ok {
			return &PlanEditPreflightMismatch{Index: -1, Path: path, Reason: "required plan scope path has no submitted edit"}
		}
	}

	return nil
}

func planExecutePathSet(paths []string) map[string]struct{} {
	out := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		if normalized := plancontract.NormalizeScopePath(path); normalized != "" {
			out[normalized] = struct{}{}
		}
	}

	return out
}

func planExecuteSetStrings(values map[string]struct{}) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}

	sort.Strings(out)

	return out
}

func preflightPlanSourceSatisfiedPaths(input plancontract.SourceSatisfiedPathCoverageInput) *PlanEditPreflightMismatch {
	return preflightPlanSourceSatisfiedViolationMismatch(plancontract.ValidateSourceSatisfiedPathCoverage(input))
}

func preflightPlanSourceSatisfiedViolationMismatch(violations []plancontract.SourceSatisfiedPathViolation) *PlanEditPreflightMismatch {
	if len(violations) == 0 {
		return nil
	}

	violation := violations[0]

	return &PlanEditPreflightMismatch{
		Index:  -1,
		Path:   plancontract.NormalizeScopePath(violation.Path),
		Reason: violation.Reason,
	}
}

func planScopeDriftEventFields(handoff ExecutorHandoffPacket, mismatch *PlanEditPreflightMismatch) map[string]any {
	fields := map[string]any{
		"edit_index":                mismatch.Index,
		"reason":                    mismatch.Reason,
		"packet_state":              handoff.State,
		"handoff_id":                handoff.HandoffID,
		"approval_scope_id":         handoff.ApprovalScopeID,
		"handoff_created":           handoff.HandoffCreated,
		"packet_present":            handoff.PacketPresent,
		"packet_not_used_by_design": handoff.PacketNotUsedByDesign,
		"packet_freshness_state":    firstNonEmptyString(string(handoff.PacketFreshnessState), string(plancontract.ExecutorPacketFreshnessUnknown)),
	}
	if path := sanitizePlanExecuteEventPath(mismatch.Path); path != "" {
		fields["path"] = path
	}

	return fields
}

func preflightPlanRequiredEditPaths(required []string, plan planartifact.PlanArtifact) *PlanEditPreflightMismatch {
	coverage, err := autonomy.ResolvePlanScopePathRoleObligations(autonomy.PlanScopePathRoleObligationInput{
		RequiredEditPaths: required,
		Source:            "planexecute.required_preflight",
	})
	if err != nil {
		return &PlanEditPreflightMismatch{Index: -1, Reason: err.Error()}
	}

	if len(coverage.RequiredCoveragePaths) == 0 {
		return nil
	}

	candidatePaths, invalid := preflightPlanCandidatePaths(plan)
	if invalid != nil {
		return invalid
	}

	if mismatch := preflightPlanSourceSatisfiedPaths(plancontract.SourceSatisfiedPathCoverageInput{
		RequiredPaths:        coverage.RequiredCoveragePaths,
		SourceSatisfiedPaths: plan.Paths.SourceSatisfied,
		Evidence:             planartifact.SourceSatisfiedEvidenceRefs(plan),
		ApprovedPaths:        plan.Approval.ApprovedPaths,
		PendingPaths:         plan.Approval.PendingPaths,
		RequireEvidence:      true,
	}); mismatch != nil {
		if mismatch.Reason == "source-satisfied path is outside declared required scope" {
			mismatch.Reason = "source-satisfied path is outside required_edit_paths allowlist"
		}

		return mismatch
	}

	candidatePaths = append(candidatePaths, plan.Paths.SourceSatisfied...)

	coverage, err = autonomy.ResolvePlanScopePathRoleObligations(autonomy.PlanScopePathRoleObligationInput{
		RequiredEditPaths:      coverage.RequiredCoveragePaths,
		CandidateCoveragePaths: candidatePaths,
		Source:                 "planexecute.required_preflight",
	})
	if err != nil {
		return &PlanEditPreflightMismatch{Index: -1, Reason: err.Error()}
	}

	coverageByPath := planScopeCandidateCoverageByPath(coverage.CandidateCoverage)

	for i, edit := range plan.Edits {
		path := plancontract.NormalizeScopePath(edit.Path)
		if !coverageByPath[path].SatisfiesRequiredCoverage {
			return &PlanEditPreflightMismatch{
				Index:  i,
				Path:   path,
				Reason: "plan edit path is outside required_edit_paths allowlist",
			}
		}
	}

	for _, path := range coverage.MissingRequiredCoveragePaths {
		return &PlanEditPreflightMismatch{
			Index:  -1,
			Path:   path,
			Reason: "plan does not include an edit for this required_edit_paths entry",
		}
	}

	return nil
}

func preflightPlanMissingPathAuthorization(root string, authorizedNewFiles []string, plan planartifact.PlanArtifact) *PlanEditPreflightMismatch {
	authorized := map[string]struct{}{}
	for _, path := range mergePlanExecutePaths(authorizedNewFiles) {
		authorized[path] = struct{}{}
	}

	for i, edit := range plan.Edits {
		path := plancontract.NormalizeScopePath(edit.Path)
		if path == "" {
			continue
		}

		if _, ok := authorized[path]; ok {
			continue
		}

		abs := filepath.Join(root, filepath.FromSlash(path))
		if _, err := os.Stat(abs); err != nil {
			if os.IsNotExist(err) {
				return &PlanEditPreflightMismatch{
					Index:  i,
					Path:   path,
					Reason: "plan would create missing edit path without new-file authorization",
				}
			}

			return &PlanEditPreflightMismatch{
				Index:  i,
				Path:   path,
				Reason: "could not stat edit path before write: " + err.Error(),
			}
		}
	}

	return nil
}

func preflightPlanWriteAllowedPaths(writeAllowed []string, explicit bool, plan planartifact.PlanArtifact) *PlanEditPreflightMismatch {
	coverage, err := autonomy.ResolvePlanScopePathRoleObligations(autonomy.PlanScopePathRoleObligationInput{
		AllowedCandidatePaths: writeAllowed,
		Source:                "planexecute.write_scope_preflight",
	})
	if err != nil {
		return &PlanEditPreflightMismatch{Index: -1, Reason: err.Error()}
	}

	if len(coverage.AllowedCandidatePaths) == 0 {
		if explicit && len(plan.Edits) > 0 {
			return &PlanEditPreflightMismatch{
				Index:  0,
				Path:   plancontract.NormalizeScopePath(plan.Edits[0].Path),
				Reason: "edit path is outside explicit empty write_allowed_paths envelope",
			}
		}

		return nil
	}

	candidatePaths, invalid := preflightPlanCandidatePaths(plan)
	if invalid != nil {
		return invalid
	}

	coverage, err = autonomy.ResolvePlanScopePathRoleObligations(autonomy.PlanScopePathRoleObligationInput{
		AllowedCandidatePaths:  coverage.AllowedCandidatePaths,
		CandidateCoveragePaths: candidatePaths,
		Source:                 "planexecute.write_scope_preflight",
	})
	if err != nil {
		return &PlanEditPreflightMismatch{Index: -1, Reason: err.Error()}
	}

	coverageByPath := planScopeCandidateCoverageByPath(coverage.CandidateCoverage)

	for i, edit := range plan.Edits {
		path := plancontract.NormalizeScopePath(edit.Path)
		if !coverageByPath[path].WithinAllowedCandidatePath {
			return &PlanEditPreflightMismatch{
				Index:  i,
				Path:   path,
				Reason: "plan edit path is outside write_allowed_paths envelope",
			}
		}
	}

	return nil
}

func preflightPlanAllowedEditPaths(allowed []string, plan planartifact.PlanArtifact) *PlanEditPreflightMismatch {
	coverage, err := autonomy.ResolvePlanScopePathRoleObligations(autonomy.PlanScopePathRoleObligationInput{
		AllowedCandidatePaths: allowed,
		Source:                "planexecute.allowed_preflight",
	})
	if err != nil {
		return &PlanEditPreflightMismatch{Index: -1, Reason: err.Error()}
	}

	if len(coverage.AllowedCandidatePaths) == 0 {
		return nil
	}

	candidatePaths, invalid := preflightPlanCandidatePaths(plan)
	if invalid != nil {
		return invalid
	}

	coverage, err = autonomy.ResolvePlanScopePathRoleObligations(autonomy.PlanScopePathRoleObligationInput{
		AllowedCandidatePaths:  coverage.AllowedCandidatePaths,
		CandidateCoveragePaths: candidatePaths,
		Source:                 "planexecute.allowed_preflight",
	})
	if err != nil {
		return &PlanEditPreflightMismatch{Index: -1, Reason: err.Error()}
	}

	coverageByPath := planScopeCandidateCoverageByPath(coverage.CandidateCoverage)

	for i, edit := range plan.Edits {
		path := plancontract.NormalizeScopePath(edit.Path)
		if !coverageByPath[path].WithinAllowedCandidatePath {
			return &PlanEditPreflightMismatch{
				Index:  i,
				Path:   path,
				Reason: "plan edit path is outside allowed_edit_paths allowlist",
			}
		}
	}

	return nil
}

func preflightPlanCandidatePaths(plan planartifact.PlanArtifact) ([]string, *PlanEditPreflightMismatch) {
	var paths []string

	for i, edit := range plan.Edits {
		path := plancontract.NormalizeScopePath(edit.Path)
		if path == "" {
			return nil, &PlanEditPreflightMismatch{Index: i, Reason: "edit has no path"}
		}

		paths = append(paths, path)
	}

	return paths, nil
}

func planScopeCandidateCoverageByPath(coverage []autonomy.PlanScopePathRoleCoverage) map[string]autonomy.PlanScopePathRoleCoverage {
	out := make(map[string]autonomy.PlanScopePathRoleCoverage, len(coverage))
	for _, item := range coverage {
		out[item.Path] = item
	}

	return out
}
