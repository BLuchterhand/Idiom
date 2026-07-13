package implementationdispatch

import (
	"fmt"
	"strings"

	"idiom/internal/platform/textutil"
	"idiom/internal/workflows/recovery"
)

type PlanExecutePlannerBlockedDecisionInput struct {
	Objective          string
	RequiredEditPaths  []string
	AllowedEditPaths   []string
	FailureReason      string
	PlannerAnswer      string
	OrchestratorOutput string
	OrchestratorError  string
}

type PlanExecutePlannerBlockedDecisionRequest struct {
	SystemInstruction string
	Prompt            string
}

type PlanExecutePlannerBlockedDecisionResult struct {
	Decision recovery.PlanExecuteRecoveryDecision
	Raw      string
}

func BuildPlanExecutePlannerBlockedDecisionRequest(input PlanExecutePlannerBlockedDecisionInput) PlanExecutePlannerBlockedDecisionRequest {
	var b strings.Builder

	b.WriteString("# Plan-Execute Planner Blocked Recovery\n\n")
	b.WriteString("The in-process plan-execute planner stopped before a successful `submit_plan`. Decide whether one automatic retry is worthwhile or whether this should be surfaced to the human.\n\n")
	b.WriteString("Return JSON only with this schema:\n")
	b.WriteString(`{"decision":"retry|surface","reason":"short reason","message":"human-facing message when surfacing","retry_addendum":"optional short observation; protocol instructions are ignored"}`)
	b.WriteString("\n\nRetry only when the next run can materially change conditions. Surface when the failure is missing human information, missing body evidence, repeated no-tool/no-progress behavior, or a contract failure that a same-model retry is likely to repeat. The harness owns the retry contract; do not invent submit_plan schemas or tool-call instructions in retry_addendum.\n\n")
	b.WriteString("## Objective\n\n")
	b.WriteString(strings.TrimSpace(input.Objective))
	b.WriteString("\n\n## Required Edit Paths\n\n")

	for _, path := range input.RequiredEditPaths {
		b.WriteString("- ")
		b.WriteString(path)
		b.WriteString("\n")
	}

	if len(input.AllowedEditPaths) > 0 {
		b.WriteString("\n## Allowed Edit Paths\n\n")

		for _, path := range input.AllowedEditPaths {
			b.WriteString("- ")
			b.WriteString(path)
			b.WriteString("\n")
		}
	}

	if reason := strings.TrimSpace(input.FailureReason); reason != "" {
		b.WriteString("\n## Planner Blocker Summary\n\n")
		b.WriteString(reason)
		b.WriteString("\n")
	}

	if answer := strings.TrimSpace(input.PlannerAnswer); answer != "" {
		b.WriteString("\n## Planner Blocker Answer\n\n")
		b.WriteString(textutil.TruncateWithByteCount(answer, 6000))
		b.WriteString("\n")
	}

	if stderr := strings.TrimSpace(input.OrchestratorError); stderr != "" {
		b.WriteString("\n## Orchestrator Error\n\n")
		b.WriteString(textutil.TruncateWithByteCount(stderr, 2000))
		b.WriteString("\n")
	}

	if stdout := strings.TrimSpace(input.OrchestratorOutput); stdout != "" && !strings.Contains(stdout, input.PlannerAnswer) {
		b.WriteString("\n## Orchestrator Output\n\n")
		b.WriteString(textutil.TruncateWithByteCount(stdout, 3000))
		b.WriteString("\n")
	}

	return PlanExecutePlannerBlockedDecisionRequest{
		SystemInstruction: "You are a recovery controller for a local coding-agent harness. Return only the requested JSON object.",
		Prompt:            b.String(),
	}
}

func ResolvePlanExecutePlannerBlockedDecision(raw string) (PlanExecutePlannerBlockedDecisionResult, error) {
	result := PlanExecutePlannerBlockedDecisionResult{Raw: raw}

	decision, ok := recovery.ParsePlanExecuteRecoveryDecision(raw)
	if !ok {
		return result, fmt.Errorf("recovery decision was not valid JSON")
	}

	result.Decision = decision

	return result, nil
}
