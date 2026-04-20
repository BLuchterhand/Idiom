package frontier

import (
	"strings"

	"qwen-bubbles/internal/agent"
	"qwen-bubbles/internal/frontier/workerruntime"
	"qwen-bubbles/internal/workspace"
)

func ImplementRunPolicyForRequest(maxToolSteps int, prompt string, packet TargetPacket) agent.RunPolicy {
	if RequestNeedsExpandedImplementBudget(prompt, packet) {
		return agent.RunPolicy{
			MaxToolSteps:       0,
			MaxNoProgressTurns: 8,
			MaxContextMessages: 28,
			MaxContextChars:    60000,
			MaxModelCalls:      64,
		}
	}
	budget := ImplementToolBudget(maxToolSteps)
	return agent.RunPolicy{
		MaxToolSteps:       budget,
		MaxNoProgressTurns: 6,
		MaxContextMessages: 24,
		MaxContextChars:    40000,
		MaxModelCalls:      maxInt(budget+6, 12),
	}
}

func ImplementRepairRunPolicyForRequest(maxToolSteps int, prompt string, packet TargetPacket) agent.RunPolicy {
	budget := 8
	if RequestNeedsExpandedImplementBudget(prompt, packet) {
		budget = 14
	}
	if maxToolSteps > 0 {
		budget = minInt(budget, maxInt(maxToolSteps, 6))
	}
	return agent.RunPolicy{
		MaxToolSteps:       budget,
		MaxNoProgressTurns: 4,
		MaxContextMessages: 22,
		MaxContextChars:    42000,
		MaxModelCalls:      maxInt(budget+4, 10),
	}
}

func RunPolicyBudgetBrief(policy agent.RunPolicy) workerruntime.BudgetBrief {
	return workerruntime.BudgetBrief{
		MaxToolSteps:       policy.MaxToolSteps,
		MaxModelCalls:      policy.MaxModelCalls,
		MaxContextMessages: policy.MaxContextMessages,
		MaxContextChars:    policy.MaxContextChars,
	}
}

func ImplementationCompletionGate(prompt string, changedPaths func() []string) agent.CompletionGate {
	return implementationCompletionGateWithMode(prompt, changedPaths, false)
}

func ImplementationRepairCompletionGate(prompt string, changedPaths func() []string) agent.CompletionGate {
	return implementationCompletionGateWithMode(prompt, changedPaths, true)
}

func implementationCompletionGateWithMode(prompt string, changedPaths func() []string, repair bool) agent.CompletionGate {
	return func(input agent.CompletionGateInput) agent.CompletionGateResult {
		if changedPaths != nil && len(UniqueNormalizedPaths(changedPaths())) > 0 {
			return agent.CompletionGateResult{Accept: true}
		}
		reply := strings.ToLower(strings.TrimSpace(input.Reply))
		if ImplementationFinalReplyHasTerminalReason(reply) {
			return agent.CompletionGateResult{Accept: true}
		}
		if repair {
			return agent.CompletionGateResult{
				Summary: "Implementation repair completion gate rejected final answer",
				Error:   "verification failed and no repair edit has succeeded yet. Continue with the smallest repair edit, or answer with an explicit blocker tied to the failing verification output.",
			}
		}
		if PromptRequestsWholeFileRefactor(prompt) && SuccessfulToolResult(input.Results, "go_file_outline") && !SuccessfulToolResult(input.Results, "go_move_declarations") {
			return agent.CompletionGateResult{
				Summary: "Implementation completion gate rejected final answer",
				Error:   "the request is a whole-file refactor; go_file_outline succeeded, but no go_move_declarations call has succeeded yet. Continue with a move tool call or report the concrete failed move blocker.",
			}
		}
		if PromptRequestsImplementation(prompt) {
			return agent.CompletionGateResult{
				Summary: "Implementation completion gate rejected final answer",
				Error:   "no workspace change has succeeded yet. Continue with the next edit/tool call, or answer with an explicit no-op reason, already-satisfied finding, permission blocker, or verification blocker.",
			}
		}
		return agent.CompletionGateResult{Accept: true}
	}
}

func ImplementationFinalReplyHasTerminalReason(reply string) bool {
	return ContainsAny(strings.ToLower(strings.TrimSpace(reply)),
		"no changes needed",
		"no change needed",
		"no file edits were applied",
		"already satisfied",
		"already implemented",
		"already complete",
		"blocked",
		"permission",
		"approval",
		"unable to",
		"cannot proceed",
		"could not proceed",
	)
}

func SuccessfulToolResult(results []workspace.ToolResult, tool string) bool {
	for _, result := range results {
		if result.Tool == tool && strings.TrimSpace(result.Error) == "" {
			return true
		}
	}
	return false
}
