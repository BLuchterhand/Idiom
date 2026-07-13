package workerruntime

import (
	"strings"

	"idiom/internal/capabilities/workspace"
	recoverycontract "idiom/internal/contracts/recovery"
	"idiom/internal/platform/textutil"
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
		ToolTransportMode:         input.ToolTransportMode,
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

	if textutil.ContainsAny(combined, "already failed earlier", "repeated") {
		plan.DoNotRepeat = appendUnique(plan.DoNotRepeat, "same failed action class")
	}

	if toolParseFailure := textutil.ContainsAny(combined, "malformed tool", "tool_parse"); toolParseFailure {
		plan.DoNotRepeat = appendUnique(plan.DoNotRepeat, toolParseDoNotRepeat(input.ToolTransportMode))
		if ConstraintPresent(input.Constraints, ConstraintPlanThenExecuteSubmitPlanReady) {
			plan.RequiredNext = appendUnique(plan.RequiredNext, toolParseSubmitPlanRequiredNext(input.ToolTransportMode))
		} else {
			plan.RequiredNext = appendUnique(plan.RequiredNext, toolParseRequiredNext(input.ToolTransportMode))
		}
		plan.ValidNext = toolParseValidNext(plan.ValidNext, input.ToolTransportMode, ConstraintPresent(input.Constraints, ConstraintPlanThenExecuteSubmitPlanReady))
	}

	if verificationToolFailure(tool, combined, input.Result) {
		plan.FailureClass = recoverycontract.FailureClassVerification
		plan.RequiredNext = appendUnique(plan.RequiredNext, "repair the failed verification or report an external blocker before final answer")
	}

	if exactAnchorWriteFailure(input.Constraints, tool, combined) {
		plan.FailureClass = recoverycontract.FailureClassExactAnchorWriteFailure
		plan.RequiredNext = appendUnique(plan.RequiredNext, "retry one corrected write shape from the target packet, or answer with <<QUESTION>> naming the missing anchor")
	}

	if exactAnchorHiddenPrewriteTool(input.Constraints, tool, combined) {
		plan.FailureClass = recoverycontract.FailureClassExactAnchorPrewriteHiddenTool
		plan.RequiredNext = appendUnique(plan.RequiredNext, "exact-anchor write capability with the supplied anchor, or answer with <<QUESTION>> naming the missing anchor")
	}

	if textutil.ContainsAny(combined, "approval required", "write approval required") {
		plan.RequiredNext = appendUnique(plan.RequiredNext, "stop and report that write approval is required")
	}

	if isActualVerificationRecoverySignal(combined) {
		plan.RequiredNext = appendUnique(plan.RequiredNext, "resolve verification status before claiming completion")
	}

	if textutil.ContainsAny(combined, "completion gate", "final answer rejected") {
		plan.RequiredNext = appendUnique(plan.RequiredNext, "resolve the completion-gate blocker with a corrected final answer, one allowed evidence/tool action, or <<QUESTION>> with the exact blocker")
	}

	return plan
}

func toolParseDoNotRepeat(transport string) string {
	if toolParseNativeTransport(transport) {
		return "markdown-wrapped, XML/DSML, or textual tool payloads while native tool transport is active"
	}
	return "markdown-wrapped or malformed tool JSON"
}

func toolParseRequiredNext(transport string) string {
	if toolParseNativeTransport(transport) {
		return "use the active native tool-call transport for one callable tool, or answer normally if the task is complete"
	}
	return "emit exactly one valid JSON tool object"
}

func toolParseSubmitPlanRequiredNext(transport string) string {
	if toolParseNativeTransport(transport) {
		return "use the active native submit_plan tool call, or answer with <<QUESTION>> if submit_plan cannot be formed"
	}
	return "emit exactly one parser-readable submit_plan JSON object"
}

func toolParseNativeTransport(transport string) bool {
	return strings.EqualFold(strings.TrimSpace(transport), RecoveryToolTransportNative)
}

func toolParseValidNext(validNext []string, transport string, submitPlanReady bool) []string {
	if !toolParseNativeTransport(transport) {
		return validNext
	}

	out := validNext[:0]
	for _, item := range validNext {
		trimmed := strings.TrimSpace(item)
		if strings.EqualFold(trimmed, "emit exactly one valid JSON tool object") ||
			strings.Contains(strings.ToLower(trimmed), "active native tool-call transport") {
			continue
		}
		out = append(out, item)
	}
	if submitPlanReady {
		return appendUnique(out, toolParseSubmitPlanRequiredNext(transport))
	}
	return appendUnique(out, toolParseRequiredNext(transport))
}

func ClassifyFailure(reason string, req workspace.ToolRequest, result workspace.ToolResult) recoverycontract.FailureClass {
	combined := combinedLower(reason, result.Summary, result.Error, result.Output)
	tool := strings.TrimSpace(req.Tool)

	switch {
	case textutil.ContainsAny(combined, "tool_parse", "malformed tool request", "valid json tool object"):
		return recoverycontract.FailureClassToolParseError
	case textutil.ContainsAny(combined, "approval required", "write approval required", "permission denied", "permission"):
		return recoverycontract.FailureClassPermissionBoundary
	case textutil.ContainsAny(combined, "guard", "rejected by guard", "approved editable target", "implementation target packet", "bootstrap already inspected"):
		return recoverycontract.FailureClassGuardRejection
	case textutil.ContainsAny(combined, "completion gate", "final answer rejected"):
		return recoverycontract.FailureClassCompletionGate
	case verificationToolFailure(tool, combined, result):
		return recoverycontract.FailureClassVerification
	case isActualVerificationRecoverySignal(combined):
		return recoverycontract.FailureClassVerificationUnresolved
	case textutil.ContainsAny(combined, "same tool request", "repeated tool request", "replayed", "already failed earlier", "repeated dead-end"):
		return recoverycontract.FailureClassRepeatedAction
	case strings.Contains(combined, "broad inspection"):
		return recoverycontract.FailureClassBroadInspectionLoop
	case textutil.ContainsAny(combined, "target scope", "outside approved", "approved target"):
		return recoverycontract.FailureClassTargetScopeViolation
	case textutil.ContainsAny(combined, "post-edit", "workspace edit already landed"):
		return recoverycontract.FailureClassPostEditFocus
	case tool == "task" || strings.Contains(combined, "child task"):
		return recoverycontract.FailureClassChildTaskFailure
	case strings.Contains(combined, "checkpoint"):
		return recoverycontract.FailureClassCheckpointFailure
	case strings.TrimSpace(result.Error) != "":
		return recoverycontract.FailureClassToolFailure
	default:
		return recoverycontract.FailureClassUnknown
	}
}

func isActualVerificationRecoverySignal(combined string) bool {
	combined = strings.ToLower(strings.TrimSpace(combined))
	if combined == "" {
		return false
	}

	return textutil.ContainsAny(combined,
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
			textutil.ContainsAny(strings.ToLower(combined), "test failed", "tests failed", "build failed")
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
	return textutil.ContainsAny(combined, "approval required", "permission", "guard", "completion gate", "blocked")
}
