package agent

import (
	"idiom/internal/capabilities/workspace"
	"idiom/internal/runtime/workerruntime"
	"slices"
	"strings"
	"idiom/internal/platform/util"
)

func runnerGuidanceConstraints(agent string, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) []string {
	constraints := append([]string(nil), brief.Constraints...)
	if planThenExecuteSubmitPlanOnlyReady(brief, results) {
		constraints = util.Merge(constraints, []string{"plan_then_execute_submit_plan_ready"})
	}

	if delegatedReadOnlyClosureReady(agent, brief, results) {
		constraints = util.Merge(constraints, []string{"answer_readiness_reached"})
	}

	if !strings.HasPrefix(strings.TrimSpace(agent), "child:") && postChildPacketClosureReadyWithBrief(brief, results) {
		constraints = util.Merge(constraints, []string{"child_packet_available"})
	}

	return constraints
}

func applyDelegatedReadOnlyClosureBias(state runnerNextActionState, agent string, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) runnerNextActionState {
	if !delegatedReadOnlyClosureReady(agent, brief, results) {
		return state
	}

	state.Constraints = util.Merge(state.Constraints, []string{"answer_readiness_reached"})
	state.InvalidNextActions = filterRunnerStateStrings(state.InvalidNextActions, func(value string) bool {
		return value == "final_answer_without_satisfying_completion_gate"
	})
	state.NextValidActions = filterRunnerStateStrings(state.NextValidActions, func(value string) bool {
		lower := strings.ToLower(value)
		return strings.Contains(lower, "gate") || strings.Contains(lower, "gather or edit the missing evidence")
	})
	state.NextValidActions = prependRunnerStateStrings([]string{
		"final answer from the evidence already gathered is available",
		"if one exact missing claim remains, use one bounded probe against a named path, symbol, discovered handle, or narrow range",
	}, state.NextValidActions)
	state.InvalidNextActions = util.Merge(state.InvalidNextActions, []string{
		"open-ended discovery after answer readiness without naming a concrete gap",
	})

	return state
}

func delegatedReadOnlyClosureReady(agent string, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if slices.Contains(brief.Constraints, "answer_readiness_reached") {
		return true
	}

	if !strings.HasPrefix(strings.TrimSpace(agent), "child:") || strings.TrimSpace(brief.TaskFamily) != "delegated_read_only" {
		return false
	}

	if runnerBriefRequiresMutation(brief) || runnerVerificationRequired(brief) {
		return false
	}

	return readOnlyClosureDecisionForResults(brief, results).Answerable()
}

func applyPostChildClosureBias(state runnerNextActionState, agent string, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) runnerNextActionState {
	if strings.HasPrefix(strings.TrimSpace(agent), "child:") || !postChildPacketClosureReadyWithBrief(brief, results) {
		return state
	}

	state.Constraints = util.Merge(state.Constraints, []string{"child_packet_available"})
	state.NextValidActions = prependRunnerStateStrings([]string{
		"final answer from the child packet evidence is available",
		"if one exact missing claim remains, use one bounded probe against a named path, symbol, discovered handle, or narrow range",
	}, state.NextValidActions)
	state.InvalidNextActions = util.Merge(state.InvalidNextActions, []string{
		"open-ended discovery after a child packet without naming a concrete gap",
	})

	return state
}

func postChildPacketClosureReadyWithBrief(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if runnerBriefRequiresMutation(brief) || runnerVerificationRequired(brief) {
		return false
	}

	if !postChildReadOnlyPacketAvailable(brief, results) {
		return false
	}

	return readOnlyClosureDecisionForResults(brief, results).Answerable()
}

func postChildReadOnlyPacketAvailable(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	objective := strings.TrimSpace(workerruntime.NormalizeRuntimeBrief(brief).Objective)
	for _, result := range results {
		if ReadOnlyTaskResultAnswerableForObjective(result, objective) {
			return true
		}
	}

	return false
}


func runnerClosureGuidanceActive(agent string, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, delegatedReadOnlyAnswerReady bool, postChildPacketActive bool) bool {
	if delegatedReadOnlyAnswerReady || postChildPacketActive {
		return true
	}

	if delegatedReadOnlyClosureReady(agent, brief, results) {
		return true
	}

	if strings.HasPrefix(strings.TrimSpace(agent), "child:") {
		return false
	}

	return postChildPacketClosureReadyWithBrief(brief, results)
}

func closureSynthesisAfterIgnoredNudgePrompt(agent string, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, req workspace.ToolRequest, delegatedReadOnlyAnswerReady bool, postChildPacketActive bool, delegatedDiminishingNudges int, postChildDiminishingNudges int) (string, bool) {
	// Read-only closure nudges are advisory. The harness may reject repeated
	// identical or broad probes, but it must not convert its readiness estimate
	// into a no-tools final-answer requirement.
	return "", false
}
