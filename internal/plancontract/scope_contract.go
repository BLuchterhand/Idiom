package plancontract

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"strings"
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
	contract.RequiredCoveragePaths = UniqueScopePaths(contract.RequiredCoveragePaths)
	contract.AllowedCandidatePaths = UniqueScopePaths(contract.AllowedCandidatePaths)
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

func UniqueScopePaths(paths []string) []string {
	var out []string
	seen := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		path = NormalizeScopePath(path)
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}
	return out
}

func NormalizeScopePath(path string) string {
	path = strings.TrimSpace(path)
	path = strings.ReplaceAll(path, "\\", "/")
	for strings.HasPrefix(path, "./") {
		path = strings.TrimPrefix(path, "./")
	}
	if path == "" || path == "." || path == ".." || filepath.IsAbs(path) || strings.HasPrefix(path, "../") {
		return ""
	}
	for _, part := range strings.Split(path, "/") {
		if part == "" || part == "." || part == ".." {
			return ""
		}
	}
	return path
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
