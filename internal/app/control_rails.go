package app

import (
	"encoding/json"

	"qwen-bubbles/internal/agent"
	"qwen-bubbles/internal/frontier"
	"qwen-bubbles/internal/frontier/workerruntime"
	"qwen-bubbles/internal/workspace"
)

func toFrontierControlRailPlan(plan promptExecutionPlan) frontier.ControlRailPlan {
	return frontier.ControlRailPlan{
		Lane:              plan.lane,
		FollowupLane:      plan.followupLane,
		Contract:          promptPlanAnswerContract(plan),
		TaskFamily:        plan.taskFamily,
		PermissionProfile: plan.permissionProfile,
		AgentMode:         string(plan.agentMode),
		WorkflowKind:      string(plan.workflowKind),
		WorkflowPhase:     string(plan.workflowPhase),
		Intent:            plan.intent,
		UsesThoroughness:  analysisLaneUsesThoroughness(plan.lane),
	}
}

func renderDynamicToolPolicySnapshot(rails deterministicControlRailsSnapshot) string {
	return frontier.RenderDynamicToolPolicySnapshot(rails)
}

func buildWorkerRuntimeBrief(prompt string, rails deterministicControlRailsSnapshot, packet implementationTargetPacket, targetPacketText string, verificationPlan []string, policy agent.RunPolicy, extraConstraints []string, requiredNext []string, invalidActions []string) workerruntime.WorkerRuntimeBrief {
	return frontier.BuildWorkerRuntimeBrief(frontier.WorkerRuntimeBriefInput{
		Objective:        prompt,
		Rails:            rails,
		TargetPacket:     packet,
		TargetPacketText: targetPacketText,
		VerificationPlan: verificationPlan,
		RunBudget:        frontier.RunPolicyBudgetBrief(policy),
		ExtraConstraints: extraConstraints,
		RequiredNext:     requiredNext,
		InvalidActions:   invalidActions,
	})
}

func buildDeterministicControlRails(prompt string, plan promptExecutionPlan) deterministicControlRailsSnapshot {
	return frontier.BuildControlRailsForPrompt(prompt, toFrontierControlRailPlan(plan))
}

func controlRailPermissionProfile(family string, plan promptExecutionPlan) string {
	return frontier.PermissionProfile(family, toFrontierControlRailPlan(plan))
}

func controlRailRequiredEvidence(family string, plan promptExecutionPlan) []string {
	return frontier.RequiredEvidence(family, toFrontierControlRailPlan(plan))
}

func controlRailRequiredTools(family string, plan promptExecutionPlan) []string {
	return frontier.RequiredTools(family, toFrontierControlRailPlan(plan))
}

func controlRailAllowedTools(rails deterministicControlRailsSnapshot) []string {
	return frontier.AllowedTools(rails)
}

func controlRailForbiddenTools(family string, plan promptExecutionPlan) []string {
	return frontier.ForbiddenTools(family, toFrontierControlRailPlan(plan))
}

func controlRailReadinessGate(family string, plan promptExecutionPlan) []string {
	return frontier.ReadinessGate(family, toFrontierControlRailPlan(plan))
}

func controlRailSearchPolicy(family string, plan promptExecutionPlan) []string {
	return frontier.SearchPolicy(family, toFrontierControlRailPlan(plan))
}

func controlRailVerificationContract(plan promptExecutionPlan) string {
	return frontier.VerificationContract(toFrontierControlRailPlan(plan))
}

func controlRailStopConditions(family string, plan promptExecutionPlan) []string {
	return frontier.StopConditions(family, toFrontierControlRailPlan(plan))
}

func controlRailFinalCritic(plan promptExecutionPlan) []string {
	return frontier.FinalCritic(toFrontierControlRailPlan(plan))
}

func controlRailPreExecute(rails deterministicControlRailsSnapshot, req workspace.ToolRequest) *workspace.ToolResult {
	return frontier.PreExecute(rails, req)
}

func controlRailToolForbidden(rails deterministicControlRailsSnapshot, req workspace.ToolRequest) bool {
	return frontier.ToolForbidden(rails, req)
}

func controlRailBoundaryForbidsTool(rails deterministicControlRailsSnapshot, req workspace.ToolRequest) bool {
	return frontier.BoundaryForbidsTool(rails, req)
}

func controlRailRuntimeInstruction(rails deterministicControlRailsSnapshot) string {
	return frontier.RuntimeInstruction(rails)
}

func controlRailCompletionGate(rails deterministicControlRailsSnapshot) agent.CompletionGate {
	return frontier.CompletionGate(rails)
}

func controlRailHasShellVerification(results []workspace.ToolResult) bool {
	return frontier.HasShellVerification(results)
}

func controlRailAnswerReportsVerificationStatus(answer string) bool {
	return frontier.AnswerReportsVerificationStatus(answer)
}

func controlRailPermissionPolicy(rails deterministicControlRailsSnapshot) agent.ToolPermissionPolicy {
	return frontier.PermissionPolicy(rails)
}

func controlRailsEnforcementForAnswer(rails deterministicControlRailsSnapshot, answer string, evidencePaths []string, changedPaths []string, verificationResolved bool) controlRailsEnforcementSnapshot {
	return frontier.EnforcementForAnswer(rails, answer, evidencePaths, changedPaths, verificationResolved)
}

func controlRailMissingTargetRefs(targetRefs []string, answer string, evidencePaths []string) []string {
	return frontier.MissingTargetRefs(targetRefs, answer, evidencePaths)
}

func requiresEvidencePaths(rails deterministicControlRailsSnapshot) bool {
	return frontier.RequiresEvidencePaths(rails)
}

func repairControlRailsAnswer(answer string, enforcement controlRailsEnforcementSnapshot) string {
	return frontier.RepairAnswer(answer, enforcement)
}

func controlRailFailureToEval(prompt string, family string) bool {
	return frontier.FailureToEval(prompt, family)
}

func (m *Model) applyControlRailsFinalAnswer(rails deterministicControlRailsSnapshot, answer string, evidencePaths []string, changedPaths []string, verificationResolved bool) string {
	enforcement := controlRailsEnforcementForAnswer(rails, answer, evidencePaths, changedPaths, verificationResolved)
	m.logControlRailsEnforcement(enforcement)
	if enforcement.Passed {
		return answer
	}
	return repairControlRailsAnswer(answer, enforcement)
}

func (m *Model) logDeterministicControlRails(rails deterministicControlRailsSnapshot) {
	if m == nil || m.debugLogger == nil {
		return
	}
	data, err := json.Marshal(rails)
	if err != nil {
		return
	}
	m.logMessage("control_rails", string(data))
}

func (m *Model) logControlRailsEnforcement(enforcement controlRailsEnforcementSnapshot) {
	if m == nil || m.debugLogger == nil {
		return
	}
	data, err := json.Marshal(enforcement)
	if err != nil {
		return
	}
	m.logMessage("control_rails_enforcement", string(data))
}

func looksLikeLogOrFailureInspectionPrompt(normalized string) bool {
	return frontier.LooksLikeLogOrFailureInspectionPrompt(normalized)
}

func looksLikeVerificationOnlyPrompt(normalized string) bool {
	return frontier.LooksLikeVerificationOnlyPrompt(normalized)
}
