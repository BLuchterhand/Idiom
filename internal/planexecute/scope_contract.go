package planexecute

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

type PlanScopeContract struct {
	ContractID            string             `json:"contract_id,omitempty"`
	RequiredCoveragePaths []string           `json:"required_coverage_paths,omitempty"`
	AllowedCandidatePaths []string           `json:"allowed_candidate_paths,omitempty"`
	EditSetObligations    EditSetObligations `json:"edit_set_obligations,omitempty"`
}

func NewPlanScopeContract(requiredCoveragePaths []string, allowedCandidatePaths []string, obligations EditSetObligations) PlanScopeContract {
	return PlanScopeContract{
		RequiredCoveragePaths: append([]string(nil), requiredCoveragePaths...),
		AllowedCandidatePaths: append([]string(nil), allowedCandidatePaths...),
		EditSetObligations:    append(EditSetObligations(nil), obligations...),
	}.Normalized()
}

func (contract PlanScopeContract) Normalized() PlanScopeContract {
	contract.RequiredCoveragePaths = planExecuteUniqueRequiredPaths(contract.RequiredCoveragePaths)
	contract.AllowedCandidatePaths = planExecuteUniqueRequiredPaths(contract.AllowedCandidatePaths)
	contract.EditSetObligations = contract.EditSetObligations.Normalized()
	contract.ContractID = ""
	if contract.HasContent() {
		contract.ContractID = planScopeContractID(contract)
	}
	return contract
}

func (contract PlanScopeContract) HasContent() bool {
	return len(contract.RequiredCoveragePaths) > 0 ||
		len(contract.AllowedCandidatePaths) > 0 ||
		len(contract.EditSetObligations.Normalized()) > 0
}

func (contract PlanScopeContract) RequiredEditPaths() []string {
	contract = contract.Normalized()
	return append([]string(nil), contract.RequiredCoveragePaths...)
}

func (contract PlanScopeContract) AllowedEditPaths() []string {
	contract = contract.Normalized()
	return append([]string(nil), contract.AllowedCandidatePaths...)
}

func (contract PlanScopeContract) WithEditSetObligation(kind EditSetObligationKind, source string) PlanScopeContract {
	contract.EditSetObligations = contract.EditSetObligations.With(kind, source)
	return contract.Normalized()
}

func planScopeContractID(contract PlanScopeContract) string {
	contract.ContractID = ""
	body, err := json.Marshal(contract)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])[:16]
}

func planExecuteScopeContract(opts Options, prompt string) PlanScopeContract {
	scope := opts.ScopeContract
	required := append([]string(nil), scope.RequiredCoveragePaths...)
	required = append(required, []string(opts.RequiredEditPaths)...)
	required = append(required, RequiredEditPathsFromPrompt(prompt)...)
	allowed := append([]string(nil), scope.AllowedCandidatePaths...)
	allowed = append(allowed, []string(opts.AllowedEditPaths)...)
	allowed = append(allowed, AllowedEditPathsFromPrompt(prompt)...)
	obligations := MergeEditSetObligations(
		scope.EditSetObligations.normalizedWithDefaultSource("scope_contract"),
		opts.EditSetObligations.normalizedWithDefaultSource("options"),
	)
	return NewPlanScopeContract(required, allowed, obligations)
}
