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
	// Strip self-inflicted "Blocker:" prose when the harness has
	// objective evidence the contract is satisfied: all required edit
	// paths changed, planner owns verification, and the last failed
	// tool was a post-completion write probe (e.g., apply_patch with
	// stale context after the same file was already updated through
	// edit_file). The completion gate already accepted this answer —
	// the rewrite keeps downstream consumers from reading the model's
	// no-op blocker note as a run-level failure. Runs heading toward a
	// real <<QUESTION>> blocker still pass through untouched.
	if cleaned, ok := stripCompletedMutationSelfBlocker(input.Brief, input.Results, answer); ok {
		return cleaned
	}
	if !strings.HasPrefix(answer, "<<QUESTION>>") && !mutationReplyReportsConcreteNoopOrBlocker(strings.ToLower(answer)) {
		if blocker, ok := canonicalMutationBlockerFinalAnswer(input.Brief, input.Results, answer); ok {
			return blocker
		}
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

func canonicalMutationBlockerFinalAnswer(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, answer string) (string, bool) {
	brief = workerruntime.NormalizeRuntimeBrief(brief)
	if !runnerBriefRequiresMutation(brief) {
		return "", false
	}
	// Plan-then-execute planner phase: a successful submit_plan IS the
	// planner's deliverable; the executor phase (run by the orchestrator
	// after this runner exits) produces the file changes. Without this
	// exemption the decorator rewrites the model's clean "plan
	// submitted" answer into a "Required edit paths remain unchanged"
	// blocker because the planner phase has no changed_paths yet, even
	// though the plan was submitted correctly.
	if strings.TrimSpace(brief.TaskFamily) == "plan_then_execute_planner" && successfulSubmitPlanResultCount(results) > 0 {
		return "", false
	}
	changed := changedPathsFromToolResults(results)
	missing := missingRequiredEditPaths(brief.RequiredEditPaths, changed)
	if strings.TrimSpace(brief.TaskFamily) == "plan_then_execute_planner" &&
		PlanThenExecuteSourceSatisfiedAnswerCoversPaths(answer, missing) &&
		planThenExecuteSourceSatisfiedResultsCoverPaths(missing, results) {
		return "", false
	}
	if len(missing) == 0 {
		return "", false
	}
	var b strings.Builder
	b.WriteString("<<QUESTION>>: Required edit path")
	if len(missing) > 1 {
		b.WriteString("s")
	}
	if len(missing) == 1 {
		b.WriteString(" remains unchanged: ")
	} else {
		b.WriteString(" remain unchanged: ")
	}
	b.WriteString(strings.Join(missing, ", "))
	b.WriteString(".")
	if len(changed) > 0 {
		b.WriteString(" Changed path")
		if len(changed) > 1 {
			b.WriteString("s")
		}
		b.WriteString(" recorded: ")
		b.WriteString(strings.Join(mapKeys(changed), ", "))
		b.WriteString(".")
	} else {
		b.WriteString(" No required workspace edit was completed.")
	}
	b.WriteString(" Provide current source anchors for the remaining path")
	if len(missing) > 1 {
		b.WriteString("s")
	}
	b.WriteString(", or use a bounded required-path read to refresh stale anchors before retrying.")
	return strings.TrimSpace(b.String()), true
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
	if exact, ok := presentableExactProbeAnswer(brief, results); ok {
		return exact, true
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

func presentableExactProbeAnswer(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) (string, bool) {
	if runnerBriefRequiresMutation(brief) || runnerVerificationRequired(brief) {
		return "", false
	}
	anchor, ok := bestPresentableExactProbeAnchor(results)
	if !ok {
		return "", false
	}
	facts := exactProbeObservations(anchor.symbol, anchor.output)
	if len(facts) == 0 {
		return "", false
	}

	var b strings.Builder
	switch {
	case anchor.symbol != "" && anchor.path != "":
		b.WriteString("`")
		b.WriteString(anchor.symbol)
		b.WriteString("` in `")
		b.WriteString(anchor.path)
		b.WriteString("` is the clearest inspected implementation anchor.")
	case anchor.path != "":
		b.WriteString("`")
		b.WriteString(anchor.path)
		b.WriteString("` is the clearest inspected implementation anchor.")
	default:
		b.WriteString("The clearest inspected implementation anchor is the exact probe that was already read.")
	}
	b.WriteString(" The inspected body shows that it ")
	b.WriteString(joinFallbackFacts(facts))
	b.WriteString(".")
	return strings.TrimSpace(b.String()), true
}

type presentableExactProbeAnchor struct {
	path   string
	symbol string
	output string
}

func bestPresentableExactProbeAnchor(results []workspace.ToolResult) (presentableExactProbeAnchor, bool) {
	candidates := make([]presentableExactProbeAnchor, 0, len(results))
	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" || strings.TrimSpace(result.Output) == "" {
			continue
		}
		switch strings.TrimSpace(result.Tool) {
		case "go_read_symbol", "go_extract_body", "cpp_read_symbol", "cpp_extract_body", "python_read_symbol", "ts_read_symbol", "rust_read_symbol", "java_read_symbol":
			path, symbol := extractExactProbeAnchor(result)
			candidates = append(candidates, presentableExactProbeAnchor{
				path:   path,
				symbol: symbol,
				output: strings.TrimSpace(result.Output),
			})
		}
	}
	for _, candidate := range candidates {
		if len(exactProbeObservations(candidate.symbol, candidate.output)) > 0 {
			return candidate, true
		}
	}
	if len(candidates) == 0 {
		return presentableExactProbeAnchor{}, false
	}
	return candidates[0], true
}

func extractExactProbeAnchor(result workspace.ToolResult) (string, string) {
	summary := strings.TrimSpace(result.Summary)
	switch strings.TrimSpace(result.Tool) {
	case "go_read_symbol", "cpp_read_symbol", "python_read_symbol", "ts_read_symbol", "rust_read_symbol", "java_read_symbol":
		if rest, ok := strings.CutPrefix(summary, "Read "); ok {
			if symbol, remainder, found := strings.Cut(rest, " in "); found {
				return extractExactProbePath(remainder), strings.TrimSpace(symbol)
			}
		}
	case "go_extract_body", "cpp_extract_body":
		if rest, ok := strings.CutPrefix(summary, "Extracted "); ok {
			if symbol, remainder, found := strings.Cut(rest, " in "); found {
				return extractExactProbePath(remainder), strings.TrimSpace(symbol)
			}
		}
	}
	return "", ""
}

func extractExactProbePath(remainder string) string {
	remainder = strings.TrimSpace(remainder)
	for _, marker := range []string{" lines ", " bytes ", " (", ":"} {
		if head, _, ok := strings.Cut(remainder, marker); ok {
			return strings.TrimSpace(head)
		}
	}
	return strings.TrimSpace(remainder)
}

func exactProbeObservations(symbol string, output string) []string {
	output = strings.TrimSpace(output)
	if output == "" {
		return nil
	}
	observations := make([]string, 0, 5)
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" || containsRunnerString(observations, value) {
			return
		}
		observations = append(observations, value)
	}

	lower := strings.ToLower(output)
	if strings.Contains(output, "normalizeReadOnlyChildPrompt(") || strings.Contains(output, "augmentReadOnlyChildPrompt(") {
		add("rewrites the delegated prompt before handing work to the child runner")
	}
	if strings.Contains(output, "RunPolicy{") || strings.Contains(output, "MaxToolSteps:") || strings.Contains(output, "MaxModelCalls:") || strings.Contains(output, "MaxDuration:") {
		add("gives that child its own bounded execution policy")
	}
	if strings.Contains(output, "RuntimeBriefForRole(") || strings.Contains(output, "RuntimeBrief:") {
		add("creates child-scoped runtime context for the delegated task")
	}
	if strings.Contains(output, "PermissionPolicy:") || strings.Contains(output, "readOnlyChildTaskPermissionPolicy(") || strings.Contains(output, "workbranchChildTaskPermissionPolicy(") {
		add("applies a child-scoped permission policy instead of inheriting unrestricted access")
	}
	if strings.Contains(output, "DisableTaskTool: true") || strings.Contains(output, "DisableTaskTool:  true") {
		add("disables nested task delegation inside the child run")
	}
	if strings.Contains(output, "child.Run(") {
		add("executes the delegated question through a nested child runner")
	}
	if strings.Contains(output, "augmentDelegatedReadOnlyPacket(") || strings.Contains(output, "normalizeDelegatedReadOnlyPacket(") || strings.Contains(lower, `summary: "child task completed"`) || strings.Contains(lower, `tool: "task"`) {
		add("folds the child answer and tool results back into the parent task-result/evidence flow")
	}
	if len(observations) > 0 {
		return observations
	}

	calls := exactProbeCallNames(symbol, output, 4)
	if len(calls) == 0 {
		return nil
	}
	add("calls " + quotedFallbackCallNames(calls))
	return observations
}

func exactProbeCallNames(symbol string, output string, max int) []string {
	if max <= 0 {
		max = 4
	}
	skip := strings.TrimSpace(symbol)
	if dot := strings.LastIndex(skip, "."); dot >= 0 {
		skip = strings.TrimSpace(skip[dot+1:])
	}
	calls := make([]string, 0, max)
	for _, match := range callLikeIdentifierPattern.FindAllStringSubmatch(output, -1) {
		if len(match) <= 1 {
			continue
		}
		candidate, ok := delegatedReadOnlyLiteralSymbolCandidate(match[1])
		if !ok {
			continue
		}
		if strings.EqualFold(candidate, skip) || strings.EqualFold(candidate, "String") || strings.EqualFold(candidate, "Error") {
			continue
		}
		calls = appendUniqueSearchTerm(calls, candidate)
		if len(calls) >= max {
			return calls
		}
	}
	return calls
}

func quotedFallbackCallNames(values []string) string {
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		quoted = append(quoted, "`"+value+"`")
	}
	switch len(quoted) {
	case 0:
		return ""
	case 1:
		return quoted[0]
	case 2:
		return quoted[0] + " and " + quoted[1]
	default:
		return strings.Join(quoted[:len(quoted)-1], ", ") + ", and " + quoted[len(quoted)-1]
	}
}

func joinFallbackFacts(facts []string) string {
	filtered := make([]string, 0, len(facts))
	for _, fact := range facts {
		fact = strings.TrimSpace(fact)
		if fact == "" {
			continue
		}
		filtered = append(filtered, fact)
	}
	switch len(filtered) {
	case 0:
		return ""
	case 1:
		return filtered[0]
	case 2:
		return filtered[0] + " and " + filtered[1]
	default:
		return strings.Join(filtered[:len(filtered)-1], ", ") + ", and " + filtered[len(filtered)-1]
	}
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
		strings.Contains(lower, "the strongest implementation evidence is in") ||
		strings.Contains(lower, "use those findings as the source of truth") ||
		strings.Contains(lower, "what this establishes:") ||
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

// stripCompletedMutationSelfBlocker rewrites a mutation-lane final
// answer when the model surfaced a self-inflicted "Blocker:" line even
// though the harness can see the work is done. Returns the cleaned
// answer and true on a rewrite; otherwise empty and false.
//
// Preconditions:
//   - Brief requires mutation
//   - All required_edit_paths show up in changed paths
//   - Answer is not already a <<QUESTION>>
//   - Planner owns verification (so no harness-side verification gate
//     is going to flip this run to blocked)
//   - The last tool result with a non-empty error is a write-class call
//     against a path that was already successfully changed earlier in
//     the run — the shape of an apply_patch/edit_file probe with stale
//     context after the file was edited.
func stripCompletedMutationSelfBlocker(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, answer string) (string, bool) {
	if !runnerBriefRequiresMutation(brief) {
		return "", false
	}
	if strings.HasPrefix(strings.TrimSpace(answer), "<<QUESTION>>") {
		return "", false
	}
	changed := changedPathsFromToolResults(results)
	if len(missingRequiredEditPaths(brief.RequiredEditPaths, changed)) > 0 {
		return "", false
	}
	if !plannerOwnsVerification(brief) {
		return "", false
	}
	if !lastFailureIsPostCompletionWriteProbe(results, changed) {
		return "", false
	}
	cleaned, stripped := stripBlockerSection(answer)
	if !stripped || cleaned == "" {
		return "", false
	}
	return cleaned, true
}

// lastFailureIsPostCompletionWriteProbe reports whether the most recent
// failed tool result is a write-class call targeting a path that was
// already changed earlier in the run. Non-write failures and writes
// against fresh paths return false — those may still represent real
// blockers.
func lastFailureIsPostCompletionWriteProbe(results []workspace.ToolResult, changed map[string]struct{}) bool {
	if len(changed) == 0 {
		return false
	}
	for i := len(results) - 1; i >= 0; i-- {
		result := results[i]
		if strings.TrimSpace(result.Error) == "" {
			continue
		}
		switch strings.TrimSpace(result.Tool) {
		case "edit_file", "apply_patch", "write_file", "append_file":
		default:
			return false
		}
		path := normalizeRunnerPath(writeFailureTargetPath(result))
		if path == "" {
			return false
		}
		_, ok := changed[path]
		return ok
	}
	return false
}

// writeFailureTargetPath extracts the workspace path the failed write
// was aimed at, when the tool's error or summary surfaces it. Returns
// the empty string when no path is recoverable.
func writeFailureTargetPath(result workspace.ToolResult) string {
	switch strings.TrimSpace(result.Tool) {
	case "apply_patch":
		// "Update File <path>: hunk 1 context did not match the file"
		// "Add File <path>: …", etc.
		errText := strings.TrimSpace(result.Error)
		for _, prefix := range []string{"Update File ", "Add File ", "Delete File ", "Move File ", "Update+Move File "} {
			if rest, ok := strings.CutPrefix(errText, prefix); ok {
				if path, _, found := strings.Cut(rest, ":"); found {
					return strings.TrimSpace(path)
				}
				return strings.TrimSpace(rest)
			}
		}
	case "edit_file", "write_file", "append_file":
		// edit_file rejection summaries don't always carry the path,
		// but successful summaries are formatted "Edited <path>" /
		// "Wrote <path>" / "Appended <path>". Fail closed when neither
		// the summary nor a parsed error yields a path.
		summary := strings.TrimSpace(result.Summary)
		fields := strings.Fields(summary)
		if len(fields) >= 2 {
			switch fields[0] {
			case "Edited", "Wrote", "Appended":
				return fields[1]
			}
		}
	}
	return ""
}

// stripBlockerSection removes a trailing "Blocker:" / "Blockers:"
// section (and its bullet body) from the answer, returning the
// cleaned text and true when something was stripped. Other prose
// (Changed paths, Verification) is preserved.
func stripBlockerSection(answer string) (string, bool) {
	lines := strings.Split(answer, "\n")
	cut := -1
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		lower := strings.ToLower(trimmed)
		if lower == "blocker:" ||
			lower == "blockers:" ||
			strings.HasPrefix(lower, "blocker:") ||
			strings.HasPrefix(lower, "blockers:") ||
			strings.HasPrefix(lower, "blocker -") ||
			strings.HasPrefix(lower, "blocker—") {
			cut = i
			break
		}
	}
	if cut < 0 {
		return "", false
	}
	head := strings.TrimRightFunc(strings.Join(lines[:cut], "\n"), func(r rune) bool {
		return r == '\n' || r == ' ' || r == '\t'
	})
	return head, true
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
