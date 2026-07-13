package agent

import (
	"slices"
	"strings"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/autonomy"
	"idiom/internal/contracts/toolfacts"
	"idiom/internal/contracts/toolpolicy"
	"idiom/internal/runtime/workerruntime"
)

type LifecycleCloseoutPolicyDecision struct {
	Handled bool
	Gate    runnerAutonomyGateDecision
}

func lifecycleLoopCloseoutDecision(stopReason string, hasOpenLoopObligations bool) LifecycleCloseoutPolicyDecision {
	if !hasOpenLoopObligations {
		return LifecycleCloseoutPolicyDecision{}
	}
	reason := strings.TrimSpace(stopReason)
	if reason == "" || reason == "completed" {
		reason = "completed_without_loop_fuse"
	}
	return LifecycleCloseoutPolicyDecision{Handled: true, Gate: runnerAutonomyGateDecision{
		Gate:        autonomy.GateLoop,
		Decision:    autonomy.PolicyObserve,
		InvariantID: "loop.repeated_call",
		ReasonCode:  reason,
		Source:      "runner_loop_closeout",
	}}
}

func lifecycleBudgetCloseoutDecision(policy RunPolicy, metrics runnerMetrics, stopReason string, exhausted bool) LifecycleCloseoutPolicyDecision {
	reason := strings.TrimSpace(stopReason)
	if reason == "" {
		reason = "completed"
	}
	decision := autonomy.PolicyObserve
	if exhausted {
		decision = autonomy.PolicyStop
	}
	budget := &autonomy.BudgetState{
		MaxToolSteps:             policy.MaxToolSteps,
		UsedToolSteps:            metrics.ToolCalls,
		MaxModelCalls:            policy.MaxModelCalls,
		MaxOperationalModelCalls: policy.MaxOperationalModelCalls,
		UsedModelCalls:           metrics.ModelCalls,
		MaxContextMessages:       policy.MaxContextMessages,
		MaxContextChars:          policy.MaxContextChars,
		Exhausted:                exhausted,
	}
	if exhausted {
		budget.ExhaustionReason = reason
	}

	return LifecycleCloseoutPolicyDecision{Handled: true, Gate: runnerAutonomyGateDecision{
		Gate:        autonomy.GateBudget,
		Decision:    decision,
		InvariantID: "budget.run_policy",
		ReasonCode:  reason,
		Budget:      budget,
		Source:      "runner_budget_gate",
	}}
}

type ModelCallReadinessPolicyInput struct {
	Policy  RunPolicy
	Metrics runnerMetrics
}

type ModelCallReadinessPolicyDecision struct {
	Stop            bool
	StopReason      string
	BudgetExhausted bool
	CloseoutMode    noToolsCloseoutMode
	CloseoutPrompt  string
}

func modelCallReadinessBeforeModelCall(input ModelCallReadinessPolicyInput) ModelCallReadinessPolicyDecision {
	if input.Policy.shouldStopForOperationalModelCalls(input.Metrics.ModelCalls) {
		return ModelCallReadinessPolicyDecision{
			Stop:            true,
			StopReason:      "operational_model_call_limit_exceeded",
			BudgetExhausted: true,
			CloseoutMode:    noToolsCloseoutDeterministicRequired,
			CloseoutPrompt:  "Operational model-call limit is exhausted. Tools are now disabled. " + forceAnswerTailGatheredEvidence,
		}
	}
	return ModelCallReadinessPolicyDecision{}
}

type ToolAuthorizationSurfaceDecision struct {
	Rejected     bool
	Summary      string
	Error        string
	Policy       ToolAuthorizationPolicyEvent
	ParityReport ParityReport
}

type ToolAuthorizationPolicyEvent struct {
	autonomy.ContractPolicyEvent
	EventType    autonomy.EventType
	ParityReport ParityReport
}

func toolSurfaceRejectionDisposition(req workspace.ToolRequest, baseAllowedTools []string, currentAllowedTools []string) toolfacts.ToolRejectionDisposition {
	tool := strings.TrimSpace(req.Tool)
	if tool == "" {
		return toolfacts.ToolRejectionNotRetained
	}

	baseAllows := slices.Contains(baseAllowedTools, tool)
	currentAllows := slices.Contains(currentAllowedTools, tool)
	if baseAllows && !currentAllows {
		return toolfacts.ToolRejectionDeferredByPhase
	}
	return toolfacts.ToolRejectionPolicyDenied
}

const toolAuthorizationPolicyInvariant = "tool.authorization"

func (r *runExecution) readOnlyWriteChildTaskPermissionDecision(req workspace.ToolRequest) (PermissionDecisionSnapshot, bool) {
	if r == nil || strings.TrimSpace(req.Tool) != workspace.ToolNameTask || !isWriteChildMode(req.Mode) {
		return PermissionDecisionSnapshot{}, false
	}

	readOnlyProfile := strings.TrimSpace(r.state.runtimeBrief.PermissionProfile) == string(workerruntime.PermissionReadOnlyWorkspace)
	readOnlyWorkspace := r.runner.Workspace != nil && r.runner.Workspace.Mode() == workspace.ModeExplore
	if !readOnlyProfile && !readOnlyWorkspace {
		return PermissionDecisionSnapshot{}, false
	}

	return PermissionDecisionSnapshot{
		Tool:     strings.TrimSpace(req.Tool),
		Target:   PermissionTarget(req),
		Decision: toolpolicy.Ask,
		Rule:     "read_only_workspace task.mode=write decision=ask",
	}, true
}

func toolAuthorizationSurfaceDecision(req workspace.ToolRequest, visibleTools []string, recoveryAllowance bool) ToolAuthorizationSurfaceDecision {
	parity := CompareParity(ParityInput{
		SurfaceID:   "current_tool_surface",
		InvariantID: toolAuthorizationPolicyInvariant,
		OldDecision: "reject",
		NewDecision: map[bool]string{true: "allow", false: "reject"}[recoveryAllowance],
	})
	if recoveryAllowance {
		policy := toolAuthorizationSurfacePolicyEvent(req, autonomy.PolicyAllow)
		policy.ParityReport = parity
		return ToolAuthorizationSurfaceDecision{Policy: policy, ParityReport: parity}
	}
	rejection := NewActiveSurface(visibleTools, nil).RejectionForTool(req.Tool)
	if !rejection.Reject {
		parity.NewDecision = "allow"
		return ToolAuthorizationSurfaceDecision{ParityReport: parity}
	}
	policy := toolAuthorizationSurfacePolicyEvent(req, autonomy.PolicyReject)
	policy.ParityReport = parity
	return ToolAuthorizationSurfaceDecision{
		Rejected:     true,
		Summary:      rejection.Summary,
		Error:        rejection.Error,
		Policy:       policy,
		ParityReport: parity,
	}
}

func toolAuthorizationSurfacePolicyEvent(req workspace.ToolRequest, decision autonomy.ToolPolicyDecision) ToolAuthorizationPolicyEvent {
	result := "rejected"
	errorClass := "tool_surface"
	if decision == autonomy.PolicyAllow {
		result = "allowed"
		errorClass = ""
	}
	return toolAuthorizationPolicyEvent(req, decision, "current_tool_surface", result, errorClass, "tool_authorization_policy", false)
}

func emitToolAuthorizationSurfacePolicyResult(recorder *runnerAutonomyRecorder, req workspace.ToolRequest, decision autonomy.ToolPolicyDecision) {
	if recorder == nil {
		return
	}
	parityDecision := "reject"
	if decision == autonomy.PolicyAllow {
		parityDecision = "allow"
	}
	recorder.emitPolicyResult(req, toolAuthorizationPolicyResultInput(
		toolAuthorizationSurfacePolicyEvent(req, decision),
		autonomy.EventToolPolicyResult,
		toolAuthorizationSurfaceParityReport(recorder, parityDecision),
	))
}

func toolAuthorizationPolicyEvent(req workspace.ToolRequest, decision autonomy.ToolPolicyDecision, reason string, result string, errorClass string, source string, approval bool) ToolAuthorizationPolicyEvent {
	return ToolAuthorizationPolicyEvent{
		EventType: autonomy.EventToolPolicyDecision,
		ContractPolicyEvent: autonomy.ContractPolicyEvent{
			ToolName:         strings.TrimSpace(req.Tool),
			PackID:           autonomy.ContractPackToolAuthorization,
			Decision:         decision,
			InvariantID:      toolAuthorizationPolicyInvariant,
			ReasonCode:       strings.TrimSpace(reason),
			ApprovalRequired: approval,
			Result:           strings.TrimSpace(result),
			ErrorClass:       strings.TrimSpace(errorClass),
			Source:           strings.TrimSpace(source),
		},
	}
}

func toolAuthorizationPolicyResultInput(policy ToolAuthorizationPolicyEvent, eventType autonomy.EventType, parity ParityReport) runnerAutonomyPolicyEventInput {
	if eventType == "" {
		eventType = autonomy.EventToolPolicyResult
	}
	return runnerAutonomyPolicyEventInput{
		EventType:        eventType,
		Decision:         policy.Decision,
		PackID:           policy.PackID,
		InvariantID:      policy.InvariantID,
		ReasonCode:       policy.ReasonCode,
		Transport:        policy.Transport,
		ParserFailure:    policy.ParserFailure,
		RepairSource:     policy.RepairSource,
		ApprovalRequired: policy.ApprovalRequired,
		Result:           policy.Result,
		ErrorClass:       policy.ErrorClass,
		ChangedPaths:     policy.ChangedPaths,
		EvidencePaths:    runnerAutonomyParityEvidenceRefs(parity),
		Source:           policy.Source,
	}
}

func toolAuthorizationSurfaceParityReport(recorder *runnerAutonomyRecorder, decision string) ParityReport {
	return CompareParity(ParityInput{
		ContractID:   recorder.activeAuthorityContractID(),
		ProtocolHash: recorder.activeProtocolHash(),
		SurfaceID:    "current_tool_surface",
		InvariantID:  toolAuthorizationPolicyInvariant,
		OldDecision:  decision,
		NewDecision:  decision,
	})
}
