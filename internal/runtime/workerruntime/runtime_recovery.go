package workerruntime

import (
	"slices"
	"strings"

	"idiom/internal/capabilities/workspace"
)

func BuildRecoveryPlan(input RecoveryInput) RecoveryPlan {
	guidance := BuildActionGuidance(ActionGuidanceInput{
		Reason:                    input.Reason,
		Request:                   input.Request,
		Result:                    input.Result,
		Constraints:               input.Constraints,
		DeclarationMovesForbidden: input.DeclarationMovesForbidden,
		EvidenceIntent:            input.EvidenceIntent,
		WriteCapable:              input.WriteCapable,
	})
	tool := strings.TrimSpace(input.Request.Tool)

	if tool == "" {
		tool = strings.TrimSpace(input.Result.Tool)
	}

	target := ToolRequestTarget(input.Request)
	combined := combinedLower(input.Reason, input.Result.Summary, input.Result.Error, input.Result.Output)
	plan := RecoveryPlan{
		FailureClass:       ClassifyFailure(input.Reason, input.Request, input.Result),
		UserVisibleBlocker: UserVisibleBlocker(input.Reason, input.Result),
		InvalidNext:        append([]string(nil), guidance.InvalidNext...),
		ValidNext:          append([]string(nil), guidance.ValidNext...),
		StateSummary:       recoveryStateSummary(input.Reason, tool, target, input.Result, input.EvidencePaths),
	}

	for _, invalid := range plan.InvalidNext {
		plan.DoNotRepeat = appendUnique(plan.DoNotRepeat, invalid)
	}

	if tool != "" {
		if target != "" {
			plan.DoNotRepeat = appendUnique(plan.DoNotRepeat, "same "+tool+" request on "+target)
		} else {
			plan.DoNotRepeat = appendUnique(plan.DoNotRepeat, "same "+tool+" request")
		}
	}

	if strings.Contains(combined, "permission denied") {
		plan.DoNotRepeat = appendUnique(plan.DoNotRepeat, "tool requests outside the current permission policy")
	}

	if containsAny(combined, "already failed earlier", "repeated") {
		plan.DoNotRepeat = appendUnique(plan.DoNotRepeat, "same failed action class")
	}

	if containsAny(combined, "malformed tool", "tool_parse") {
		plan.DoNotRepeat = appendUnique(plan.DoNotRepeat, "markdown-wrapped or malformed tool JSON")
	}

	if containsAny(combined, "malformed tool", "tool_parse") {
		if slices.Contains(input.Constraints, "plan_then_execute_submit_plan_ready") {
			plan.RequiredNext = appendUnique(plan.RequiredNext, "emit exactly one parser-readable submit_plan JSON object")
		} else {
			plan.RequiredNext = appendUnique(plan.RequiredNext, "emit exactly one valid JSON tool object")
		}
	}

	if verificationToolFailure(tool, combined, input.Result) {
		plan.FailureClass = "verification_failed"
		plan.RequiredNext = appendUnique(plan.RequiredNext, "repair the failed verification or report an external blocker before final answer")
	}

	if exactAnchorWriteFailure(input.Constraints, tool, combined) {
		plan.FailureClass = "exact_anchor_write_failure"
		plan.DoNotRepeat = appendUnique(plan.DoNotRepeat, "read/search after an exact-anchor write failed")
		plan.RequiredNext = appendUnique(plan.RequiredNext, "retry one corrected write shape from the target packet, or answer with <<QUESTION>> naming the missing anchor")
	}

	if exactAnchorHiddenPrewriteTool(input.Constraints, tool, combined) {
		plan.FailureClass = "exact_anchor_prewrite_hidden_tool"
		plan.DoNotRepeat = appendUnique(plan.DoNotRepeat, "read/search/shell before first exact-anchor write")
		plan.RequiredNext = appendUnique(plan.RequiredNext, "exact-anchor write capability with the supplied anchor, or answer with <<QUESTION>> naming the missing anchor")
	}

	if containsAny(combined, "approval required", "write approval required") {
		plan.RequiredNext = appendUnique(plan.RequiredNext, "stop and report that write approval is required")
	}

	if isActualVerificationRecoverySignal(combined) {
		plan.RequiredNext = appendUnique(plan.RequiredNext, "resolve verification status before claiming completion")
	}

	if containsAny(combined, "completion gate", "final answer rejected") {
		plan.RequiredNext = appendUnique(plan.RequiredNext, "satisfy the completion gate before final answer")
	}

	return plan
}

func ClassifyFailure(reason string, req workspace.ToolRequest, result workspace.ToolResult) string {
	combined := combinedLower(reason, result.Summary, result.Error, result.Output)
	tool := strings.TrimSpace(req.Tool)

	switch {
	case containsAny(combined, "tool_parse", "malformed tool request", "valid json tool object"):
		return "tool_parse_error"
	case containsAny(combined, "approval required", "write approval required", "permission denied", "permission"):
		return "permission_boundary"
	case containsAny(combined, "guard", "rejected by guard", "approved editable target", "implementation target packet", "bootstrap already inspected"):
		return "guard_rejection"
	case containsAny(combined, "completion gate", "final answer rejected"):
		return "completion_gate"
	case verificationToolFailure(tool, combined, result):
		return "verification_failed"
	case isActualVerificationRecoverySignal(combined):
		return "verification_unresolved"
	case containsAny(combined, "same tool request", "repeated tool request", "replayed", "already failed earlier", "repeated dead-end"):
		return "repeated_action"
	case strings.Contains(combined, "broad inspection"):
		return "broad_inspection_loop"
	case containsAny(combined, "target scope", "outside approved", "approved target"):
		return "target_scope_violation"
	case containsAny(combined, "post-edit", "workspace edit already landed"):
		return "post_edit_focus"
	case tool == "task" || strings.Contains(combined, "child task"):
		return "child_task_failure"
	case strings.Contains(combined, "checkpoint"):
		return "checkpoint_failure"
	case strings.TrimSpace(result.Error) != "":
		return "tool_failure"
	default:
		return "unknown"
	}
}

func isActualVerificationRecoverySignal(combined string) bool {
	combined = strings.ToLower(strings.TrimSpace(combined))
	if combined == "" {
		return false
	}

	return containsAny(combined,
		"source sanity failed",
		"source_sanity failed",
		"diagnostics failed",
		"verification failed",
		"verification result",
		"verification status",
		"verification-only",
		"verification only",
		"verification command",
		"verification contract",
		"missing verification",
		"missing-verification",
		"post-edit verification",
		"resolve verification",
	)
}

func verificationToolFailure(tool string, combined string, result workspace.ToolResult) bool {
	if strings.TrimSpace(result.Error) == "" {
		return false
	}

	tool = strings.TrimSpace(tool)
	if tool == "" {
		tool = strings.TrimSpace(result.Tool)
	}

	switch tool {
	case workspace.ToolNameSourceSanity, workspace.ToolNameGoDiagnostics, workspace.ToolNameCppDiagnostics:
		return true
	case workspace.ToolNameShell:
		return isActualVerificationRecoverySignal(combined) ||
			containsAny(strings.ToLower(combined), "test failed", "tests failed", "build failed")
	default:
		return isActualVerificationRecoverySignal(combined)
	}
}

func ClassifyPhase(req workspace.ToolRequest, result workspace.ToolResult) string {
	if strings.TrimSpace(result.Error) != "" {
		return "repair"
	}

	tool := strings.TrimSpace(req.Tool)
	if workspace.IsWorkspaceWriteTool(tool) {
		return "verification"
	}

	switch tool {
	case workspace.ToolNameTaskLedger:
		return "planning"
	default:
		return "evidence"
	}
}

func UserVisibleBlocker(reason string, result workspace.ToolResult) bool {
	combined := combinedLower(reason, result.Summary, result.Error)
	return containsAny(combined, "approval required", "permission", "guard", "completion gate", "blocked")
}
