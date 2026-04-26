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
	if presentable, ok := presentableNoToolsFallbackAnswer(input.Brief, input.Results, answer); ok {
		return presentable
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

func presentableNoToolsFallbackAnswer(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, internalAnswer string) (string, bool) {
	internalAnswer = strings.TrimSpace(internalAnswer)
	if internalAnswer == "" {
		return "", false
	}
	if !finalAnswerNeedsDecoration(internalAnswer) && !looksLikeGenericEvidenceComposerAnswer(internalAnswer) {
		return "", false
	}
	if cleaned, ok := cleanedSubstantiveFinalAnswer(internalAnswer); ok {
		return cleaned, true
	}

	paths := delegatedClosureEvidencePaths(brief.Objective, results, 4)
	if len(paths) == 0 {
		paths = postChildClosureEvidencePaths(brief.Objective, results, 4)
	}
	if len(paths) == 0 {
		paths = promptEvidencePathsFromToolResults(results, 4)
	}
	paths = closurePromptEvidencePaths(paths, 4)

	findings := presentableFindingsFromInternalAnswer(internalAnswer, 2)
	if len(findings) > 0 {
		var b strings.Builder
		b.WriteString(strings.Join(findings, " "))
		if len(paths) > 0 {
			b.WriteString(" The relevant source files are ")
			b.WriteString(formatInlinePathList(paths, 4))
			b.WriteString(".")
		}
		return strings.TrimSpace(b.String()), true
	}

	if len(paths) == 0 {
		return "", false
	}
	var b strings.Builder
	b.WriteString("The retained source context identifies the relevant implementation surface as ")
	b.WriteString(formatInlinePathList(paths, 4))
	b.WriteString(".")
	if closureHasExactProbeEvidence(results) {
		b.WriteString(" They are enough for a bounded answer, but not enough to safely expand beyond the inspected anchors.")
	} else {
		b.WriteString(" It does not retain enough exact body-level detail to give a fully grounded implementation explanation.")
	}
	return strings.TrimSpace(b.String()), true
}

func presentableFindingsFromInternalAnswer(answer string, max int) []string {
	if max <= 0 {
		max = 2
	}
	inKeyFindings := false
	findings := make([]string, 0, max)
	for _, raw := range strings.Split(answer, "\n") {
		line := strings.TrimSpace(raw)
		lower := strings.ToLower(line)
		switch {
		case lower == "key findings:":
			inKeyFindings = true
			continue
		case inKeyFindings && strings.HasSuffix(line, ":") && !strings.HasPrefix(line, "- "):
			return findings
		case !inKeyFindings || !strings.HasPrefix(line, "- "):
			continue
		}
		finding := strings.TrimSpace(strings.TrimPrefix(line, "- "))
		if !presentableInternalFinding(finding) {
			continue
		}
		findings = append(findings, finding)
		if len(findings) >= max {
			return findings
		}
	}
	return findings
}

func presentableInternalFinding(finding string) bool {
	finding = strings.TrimSpace(finding)
	if len(finding) < 40 {
		return false
	}
	lower := strings.ToLower(finding)
	return !containsAny(lower,
		"child task completed",
		"outlined ",
		"ran ",
		"read ",
		"found ",
		"text anchors:",
		"symbol read:",
		"related files:",
		"strongest implementation evidence",
		"what this establishes",
	)
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

func looksLikeGenericEvidenceComposerAnswer(answer string) bool {
	lower := strings.ToLower(strings.TrimSpace(answer))
	if lower == "" {
		return false
	}
	return strings.Contains(lower, "the strongest implementation evidence is in") ||
		strings.Contains(lower, "use those findings as the source of truth") ||
		strings.Contains(lower, "what this establishes:")
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
		"\n\nInspected paths:",
		"\nInspected paths:",
		"\n\nKey findings:",
		"\nKey findings:",
		"\n\nConfidence:",
		"\nConfidence:",
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
