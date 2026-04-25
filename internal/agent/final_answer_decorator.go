package agent

import (
	"encoding/json"
	"strings"

	"idiom/internal/frontier/workerruntime"
	"idiom/internal/workspace"
)

type FinalAnswerDecorateInput struct {
	Answer     string
	StopReason string
	Brief      workerruntime.WorkerRuntimeBrief
	Results    []workspace.ToolResult
	UserFacing bool
}

func (r *Runner) decorateFinalAnswer(input FinalAnswerDecorateInput) string {
	answer := strings.TrimSpace(input.Answer)
	if answer == "" || !input.UserFacing {
		return answer
	}
	if finalAnswerStopReasonPreservesUncertainty(input.StopReason) {
		return answer
	}
	if !finalAnswerNeedsDecoration(answer) {
		return answer
	}
	if composed, ok := r.composedFinalAnswerFromEvidence(input); ok {
		return composed
	}
	return answer
}

func (r *Runner) composedFinalAnswerFromEvidence(input FinalAnswerDecorateInput) (string, bool) {
	if answer, ok := synthesizeDelegatedReadOnlyAnswer(input.Brief.Objective, input.Results); ok && !finalAnswerNeedsDecoration(answer) {
		return answer, true
	}
	if output, ok := bestTaskResultOutput(input.Results); ok && !finalAnswerNeedsDecoration(output) {
		return output, true
	}
	return "", false
}

func bestTaskResultOutput(results []workspace.ToolResult) (string, bool) {
	for i := len(results) - 1; i >= 0; i-- {
		result := results[i]
		if strings.TrimSpace(result.Tool) != "task" || strings.TrimSpace(result.Error) != "" {
			continue
		}
		output := strings.TrimSpace(result.Output)
		if output == "" {
			continue
		}
		return output, true
	}
	return "", false
}

func finalAnswerNeedsDecoration(answer string) bool {
	lower := strings.ToLower(strings.TrimSpace(answer))
	if lower == "" {
		return false
	}
	if strings.HasPrefix(lower, "what i found:") ||
		strings.HasPrefix(lower, "partial findings from this turn") ||
		strings.HasPrefix(lower, "question:") {
		return true
	}
	return containsAny(lower,
		"discovery packet",
		"relevant source hits",
		"evidence used:",
		"text_anchor_search:",
		"go_read_symbol:",
		"go_workspace_symbol:",
		"dependency_graph:",
		"related_files:",
		".idiom/discovery/queries/",
	)
}

func finalAnswerStopReasonPrefersDecoration(stopReason string) bool {
	switch strings.TrimSpace(stopReason) {
	case "child_packet_answered", "child_packet_synthesized_answer", "delegated_read_only_answer_ready":
		return true
	default:
		return false
	}
}

func finalAnswerStopReasonPreservesUncertainty(stopReason string) bool {
	return strings.HasSuffix(strings.TrimSpace(stopReason), "_budget_exhausted")
}

func (r *Runner) userFacingFinalAnswer() bool {
	name := r.agentName()
	return !strings.HasPrefix(name, "child:") && !strings.HasPrefix(name, "child_write:")
}

func (r *Runner) emitFinalAnswerDecorationEvent(stopReason string, before string, after string) {
	if r == nil || r.OnEvent == nil || strings.TrimSpace(before) == strings.TrimSpace(after) {
		return
	}
	payload := map[string]any{
		"agent":        r.agentName(),
		"stop_reason":  strings.TrimSpace(stopReason),
		"before_chars": len(strings.TrimSpace(before)),
		"after_chars":  len(strings.TrimSpace(after)),
	}
	if data, err := json.Marshal(payload); err == nil {
		r.OnEvent(RunEvent{Role: "answer_decorated", Content: string(data)})
	}
}
