package workerruntime

import (
	"slices"
	"strings"
)

type ConstraintID string

const (
	ConstraintPlanThenExecuteSubmitPlanReady    ConstraintID = "plan_then_execute_submit_plan_ready"
	ConstraintRequiredEditPathsDefineWriteScope ConstraintID = "required_edit_paths_define_write_scope"
	ConstraintPlannerOwnedVerification          ConstraintID = "planner_owned_verification"
	ConstraintExactAnchorImplementation         ConstraintID = "exact_anchor_implementation"
	ConstraintExactAnchorPrewriteWriteFirst     ConstraintID = "exact_anchor_prewrite_write_first"
	ConstraintImplementationPacketWriteReady    ConstraintID = "implementation_packet_write_ready"
	ConstraintImplementationRequiredPathsRemain ConstraintID = "implementation_required_paths_remaining"
	ConstraintAnswerReadinessReached            ConstraintID = "answer_readiness_reached"
	ConstraintChildPacketAvailable              ConstraintID = "child_packet_available"
)

func ConstraintIDs() []ConstraintID {
	return []ConstraintID{
		ConstraintPlanThenExecuteSubmitPlanReady,
		ConstraintRequiredEditPathsDefineWriteScope,
		ConstraintPlannerOwnedVerification,
		ConstraintExactAnchorImplementation,
		ConstraintExactAnchorPrewriteWriteFirst,
		ConstraintImplementationPacketWriteReady,
		ConstraintImplementationRequiredPathsRemain,
		ConstraintAnswerReadinessReached,
		ConstraintChildPacketAvailable,
	}
}

func KnownConstraintID(value ConstraintID) bool {
	_, ok := constraintIDSet[value]
	return ok
}

func ConstraintPresent(constraints []string, want ConstraintID) bool {
	return slices.Contains(NormalizeConstraints(constraints), string(want))
}

func NormalizeConstraints(constraints []string) []string {
	out := make([]string, 0, len(constraints))
	for _, constraint := range constraints {
		constraint = strings.TrimSpace(constraint)
		if constraint != "" {
			out = append(out, constraint)
		}
	}
	return out
}

func ConstraintStrings(ids ...ConstraintID) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, string(id))
	}
	return out
}

var constraintIDSet = func() map[ConstraintID]struct{} {
	out := make(map[ConstraintID]struct{}, len(ConstraintIDs()))
	for _, id := range ConstraintIDs() {
		out[id] = struct{}{}
	}
	return out
}()
