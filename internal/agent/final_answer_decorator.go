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
	if cleaned, ok := cleanedSubstantiveFinalAnswer(answer); ok {
		return cleaned
	}
	if composed, ok := r.composedFinalAnswerFromEvidence(input); ok {
		return composed
	}
	return answer
}

func (r *Runner) composedFinalAnswerFromEvidence(input FinalAnswerDecorateInput) (string, bool) {
	if output, ok := bestTaskResultOutput(input.Results); ok {
		if cleaned, ok := cleanedSubstantiveFinalAnswer(output); ok {
			return cleaned, true
		}
		if !finalAnswerNeedsDecoration(output) {
			return output, true
		}
	}
	if answer, ok := synthesizeDelegatedReadOnlyAnswer(input.Brief.Objective, input.Results); ok {
		if cleaned, ok := cleanedSubstantiveFinalAnswer(answer); ok {
			return cleaned, true
		}
		if !finalAnswerNeedsDecoration(answer) {
			return answer, true
		}
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
		strings.HasPrefix(lower, "## grounded answer") ||
		strings.HasPrefix(lower, "question:") ||
		strings.Contains(lower, "source: delegated read-only evidence packet") ||
		strings.Contains(lower, "the gathered evidence points to these implementation surfaces") ||
		strings.Contains(lower, "additional inspected paths:") ||
		strings.Contains(lower, "exact probes already inspected:") {
		return true
	}
	return containsAny(lower,
		"\nevidence:\n- text anchors:",
		"### evidence highlights",
		"### decision guidance",
		"discovery packet",
		"relevant source hits",
		"evidence used:",
		"inspected paths:",
		"key findings:",
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
	switch strings.TrimSpace(stopReason) {
	case "post_child_read_budget_exhausted",
		"post_readiness_probe_budget_exhausted",
		"post_readiness_no_gap_repeated",
		"closure_synthesis_after_ignored_nudge":
		return false
	}
	return strings.HasSuffix(strings.TrimSpace(stopReason), "_budget_exhausted")
}

func cleanedSubstantiveFinalAnswer(answer string) (string, bool) {
	cleaned := stripInternalEvidenceAppendix(answer)
	if !looksLikeSubstantiveFinalAnswer(cleaned) || finalAnswerNeedsDecoration(cleaned) {
		return "", false
	}
	return cleaned, true
}

func stripInternalEvidenceAppendix(answer string) string {
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return ""
	}
	cut := len(answer)
	for _, marker := range []string{
		"\n\nAdditional inspected paths:",
		"\nAdditional inspected paths:",
		"\n\nExact probes already inspected:",
		"\nExact probes already inspected:",
	} {
		if idx := strings.Index(answer, marker); idx >= 0 && idx < cut {
			cut = idx
		}
	}
	return strings.TrimSpace(answer[:cut])
}

func looksLikeSubstantiveFinalAnswer(answer string) bool {
	answer = strings.TrimSpace(answer)
	if len(answer) < 80 {
		return false
	}
	lower := strings.ToLower(answer)
	if strings.HasPrefix(lower, "for `") && strings.Contains(lower, "the gathered evidence points to these implementation surfaces") {
		return false
	}
	if strings.Contains(lower, "\nevidence:\n- text anchors:") {
		return false
	}
	return strings.Contains(answer, ".") || strings.Contains(answer, "\n- ")
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
