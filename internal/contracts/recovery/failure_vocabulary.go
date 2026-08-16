package recovery

import (
	"strings"

	lanecontract "idiom/internal/contracts/lane"
)

type SourceLane lanecontract.LaneID
type FailureClass string
type DirtyStatus string
type ScopeStatus string

const (
	SourceLanePlanExecute SourceLane = SourceLane(lanecontract.LanePlanExecute)
	SourceLaneImplement   SourceLane = SourceLane(lanecontract.LaneImplement)
)

const (
	FailureClassPlannerFailed     FailureClass = "planner_failed"
	FailureClassPlannerBlocked    FailureClass = "planner_blocked"
	FailureClassPlanMissing       FailureClass = "plan_missing"
	FailureClassPlanInvalid       FailureClass = "plan_invalid"
	FailureClassScopeTooLarge     FailureClass = "scope_too_large"
	FailureClassExecutorEdit      FailureClass = "executor_edit_failed"
	FailureClassVerification      FailureClass = "verification_failed"
	FailureClassReviewRepair      FailureClass = "review_repair_failed"
	FailureClassStaleContext      FailureClass = "stale_context"
	FailureClassBudgetExhausted   FailureClass = "budget_exhausted"
	FailureClassDirtyWorkspace    FailureClass = "dirty_workspace"
	FailureClassNeedsHumanAnswer  FailureClass = "needs_human_answer"
	FailureClassUnsafePolicyBlock FailureClass = "unsafe_policy_block"
	FailureClassUnknown           FailureClass = "unknown_failure"
)

const (
	FailureClassToolParseError                FailureClass = "tool_parse_error"
	FailureClassPermissionBoundary            FailureClass = "permission_boundary"
	FailureClassGuardRejection                FailureClass = "guard_rejection"
	FailureClassCompletionGate                FailureClass = "completion_gate"
	FailureClassVerificationUnresolved        FailureClass = "verification_unresolved"
	FailureClassRepeatedAction                FailureClass = "repeated_action"
	FailureClassBroadInspectionLoop           FailureClass = "broad_inspection_loop"
	FailureClassTargetScopeViolation          FailureClass = "target_scope_violation"
	FailureClassPostEditFocus                 FailureClass = "post_edit_focus"
	FailureClassChildTaskFailure              FailureClass = "child_task_failure"
	FailureClassCheckpointFailure             FailureClass = "checkpoint_failure"
	FailureClassToolFailure                   FailureClass = "tool_failure"
	FailureClassMCPToolFailure                FailureClass = "mcp_tool_failure"
	FailureClassMCPInputError                 FailureClass = "mcp_input_error"
	FailureClassExactAnchorWriteFailure       FailureClass = "exact_anchor_write_failure"
	FailureClassExactAnchorPrewriteHiddenTool FailureClass = "exact_anchor_prewrite_hidden_tool"
)

const (
	DirtyStatusNotChecked    DirtyStatus = "not_checked"
	DirtyStatusClean         DirtyStatus = "clean"
	DirtyStatusDirtyInScope  DirtyStatus = "dirty_in_scope"
	DirtyStatusDirtyOutScope DirtyStatus = "dirty_out_of_scope"
)

const (
	ScopeStatusNotChecked ScopeStatus = "not_checked"
	ScopeStatusNone       ScopeStatus = "none"
	ScopeStatusInScope    ScopeStatus = "in_scope"
	ScopeStatusOutOfScope ScopeStatus = "out_of_scope"
)

func KnownSourceLane(value SourceLane) bool {
	return lanecontract.KnownLaneID(lanecontract.LaneID(value))
}

func FailureClasses() []FailureClass {
	return []FailureClass{
		FailureClassPlannerFailed,
		FailureClassPlannerBlocked,
		FailureClassPlanMissing,
		FailureClassPlanInvalid,
		FailureClassScopeTooLarge,
		FailureClassExecutorEdit,
		FailureClassVerification,
		FailureClassReviewRepair,
		FailureClassStaleContext,
		FailureClassBudgetExhausted,
		FailureClassDirtyWorkspace,
		FailureClassNeedsHumanAnswer,
		FailureClassUnsafePolicyBlock,
		FailureClassUnknown,
		FailureClassToolParseError,
		FailureClassPermissionBoundary,
		FailureClassGuardRejection,
		FailureClassCompletionGate,
		FailureClassVerificationUnresolved,
		FailureClassRepeatedAction,
		FailureClassBroadInspectionLoop,
		FailureClassTargetScopeViolation,
		FailureClassPostEditFocus,
		FailureClassChildTaskFailure,
		FailureClassCheckpointFailure,
		FailureClassToolFailure,
		FailureClassMCPToolFailure,
		FailureClassMCPInputError,
		FailureClassExactAnchorWriteFailure,
		FailureClassExactAnchorPrewriteHiddenTool,
	}
}

func ParseFailureClass(value string) (FailureClass, bool) {
	return parseKnown(value, FailureClasses(), strings.TrimSpace, false)
}

func KnownFailureClass(value FailureClass) bool {
	_, ok := ParseFailureClass(string(value))
	return ok
}

func DirtyStatuses() []DirtyStatus {
	return []DirtyStatus{
		DirtyStatusNotChecked,
		DirtyStatusClean,
		DirtyStatusDirtyInScope,
		DirtyStatusDirtyOutScope,
	}
}

func ParseDirtyStatus(value string) (DirtyStatus, bool) {
	return parseKnown(value, DirtyStatuses(), strings.TrimSpace, false)
}

func ScopeStatuses() []ScopeStatus {
	return []ScopeStatus{
		ScopeStatusNotChecked,
		ScopeStatusNone,
		ScopeStatusInScope,
		ScopeStatusOutOfScope,
	}
}

func ParseScopeStatus(value string) (ScopeStatus, bool) {
	return parseKnown(value, ScopeStatuses(), strings.TrimSpace, false)
}

func parseKnown[T ~string](value string, known []T, normalize func(string) string, normalizeKnown bool) (T, bool) {
	candidate := normalize(value)
	for _, item := range known {
		itemValue := string(item)
		if normalizeKnown {
			itemValue = normalize(itemValue)
		}
		if candidate == itemValue {
			return item, true
		}
	}
	var zero T
	return zero, false
}
