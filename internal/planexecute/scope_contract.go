package planexecute

import (
	"idiom/internal/autonomy"
	"idiom/internal/plancontract"
)

type PlanScopeContract = plancontract.PlanScopeContract

func NewPlanScopeContract(requiredCoveragePaths []string, allowedCandidatePaths []string, obligations EditSetObligations) PlanScopeContract {
	return plancontract.NewPlanScopeContract(requiredCoveragePaths, allowedCandidatePaths, obligations)
}

func planExecuteScopeContract(opts Options, prompt string) PlanScopeContract {
	scope := opts.ScopeContract
	writeAllowed := planExecuteHostWriteAllowedPaths(opts)
	required := append([]string(nil), scope.RequiredCoveragePaths...)
	required = append(required, []string(opts.RequiredEditPaths)...)
	required = append(required, RequiredEditPathsFromPrompt(prompt)...)
	allowed := append([]string(nil), scope.AllowedCandidatePaths...)
	allowed = append(allowed, []string(opts.AllowedEditPaths)...)
	allowed = append(allowed, AllowedEditPathsFromPrompt(prompt)...)
	obligations := plancontract.MergeEditSetObligations(
		scope.EditSetObligations.WithDefaultSource("scope_contract"),
		opts.EditSetObligations.WithDefaultSource("options"),
	)
	coverage, err := autonomy.ResolvePlanScopePathRoleObligations(autonomy.PlanScopePathRoleObligationInput{
		RequiredEditPaths:     required,
		AllowedCandidatePaths: allowed,
		Source:                "planexecute.scope_contract",
	})
	if err != nil {
		return plancontract.NewPlanScopeContract(required, allowed, obligations).WithWriteAllowedPaths(writeAllowed)
	}
	return plancontract.NewPlanScopeContract(coverage.RequiredCoveragePaths, coverage.AllowedCandidatePaths, obligations).WithWriteAllowedPaths(writeAllowed)
}

func planExecuteHostWriteAllowedPaths(opts Options) []string {
	scope := opts.ScopeContract.Normalized()
	if len(scope.WriteAllowedPaths) > 0 {
		return plancontract.UniqueScopePaths(scope.WriteAllowedPaths)
	}
	var paths []string
	if len(scope.RequiredCoveragePaths) > 0 || len(scope.AllowedCandidatePaths) > 0 {
		paths = append(paths, scope.RequiredCoveragePaths...)
		paths = append(paths, scope.AllowedCandidatePaths...)
	}
	paths = append(paths, []string(opts.RequiredEditPaths)...)
	paths = append(paths, []string(opts.AllowedEditPaths)...)
	return plancontract.UniqueScopePaths(paths)
}
