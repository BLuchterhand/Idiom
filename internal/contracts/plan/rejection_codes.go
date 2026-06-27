package plan

import "strings"

// PlanExecuteRejectionCode is the stable machine vocabulary for plan-execute
// tool-surface rejections. User-facing summaries may change; these codes must
// remain stable for gates and audit consumers.
type PlanExecuteRejectionCode string

const (
	PlanExecuteRejectionNone                              PlanExecuteRejectionCode = ""
	PlanExecuteRejectionCandidateScopeRequired            PlanExecuteRejectionCode = "candidate_scope_required"
	PlanExecuteRejectionReadOutsideAllowedCandidateScope  PlanExecuteRejectionCode = "read_outside_allowed_candidate_scope"
	PlanExecuteRejectionPostGateReadOutsideCandidateScope PlanExecuteRejectionCode = "post_gate_read_outside_candidate_scope"
	PlanExecuteRejectionAnchorRefreshCapabilityRequired   PlanExecuteRejectionCode = "anchor_refresh_capability_required"
	PlanExecuteRejectionCandidateSourceAlreadyReady       PlanExecuteRejectionCode = "candidate_source_already_ready"
)

func KnownPlanExecuteRejectionCode(code PlanExecuteRejectionCode) bool {
	for _, known := range PlanExecuteRejectionCodes() {
		if code == known {
			return true
		}
	}

	return false
}

func ParsePlanExecuteRejectionCode(code string) (PlanExecuteRejectionCode, bool) {
	typed := PlanExecuteRejectionCode(strings.TrimSpace(code))
	if typed == PlanExecuteRejectionNone {
		return typed, true
	}
	if !KnownPlanExecuteRejectionCode(typed) {
		return "", false
	}

	return typed, true
}

func PlanExecuteRejectionCodes() []PlanExecuteRejectionCode {
	return []PlanExecuteRejectionCode{
		PlanExecuteRejectionNone,
		PlanExecuteRejectionCandidateScopeRequired,
		PlanExecuteRejectionReadOutsideAllowedCandidateScope,
		PlanExecuteRejectionPostGateReadOutsideCandidateScope,
		PlanExecuteRejectionAnchorRefreshCapabilityRequired,
		PlanExecuteRejectionCandidateSourceAlreadyReady,
	}
}
