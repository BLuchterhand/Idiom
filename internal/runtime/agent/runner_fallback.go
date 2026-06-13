package agent

import (
	"fmt"
	"strings"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/runtime/workerruntime"
)

func immediateBudgetFallbackAnswer(agentName string, brief workerruntime.WorkerRuntimeBrief, reason string, results []workspace.ToolResult) (string, bool) {
	if len(results) == 0 || runnerBriefRequiresMutation(brief) || runnerVerificationRequired(brief) {
		return "", false
	}

	if len(changedPathsFromToolResults(results)) > 0 {
		return "", false
	}

	lowerReason := strings.ToLower(strings.TrimSpace(reason))
	if !containsAny(lowerReason,
		"budget",
		"maximum tool steps",
		"tools are now disabled",
		"run duration",
		"model-call budget",
	) {

		return "", false
	}

	if !hasSuccessfulTaskResult(results) && !strings.HasPrefix(agentName, "child:") {
		return "", false
	}

	delegatedOwnerReady := delegatedImplementationShapeSynthesisAllowed(brief, results)
	if delegatedOwnerReady {
		if output, ok := bestObjectiveAnswerableTaskResultOutput(brief.Objective, results); ok {
			return presentableFallbackCandidate(agentName, brief, results, annotateBudgetLimitedAnswer(output))
		}
	}

	if delegatedOwnerReady {
		if answer, ok := synthesizeDelegatedReadOnlyAnswer(brief.Objective, results); ok {
			return presentableFallbackCandidate(agentName, brief, results, annotateBudgetLimitedAnswer(answer))
		}
	}

	if implementationShapeOwnerContractApplies(brief) && !delegatedOwnerReady {
		return presentableFallbackCandidate(agentName, brief, results, synthesizeBriefConversationFallback(brief, results))
	}

	if usefulFallbackResultCount(results) < 2 {
		return "", false
	}

	if answer, ok := synthesizeFallbackAnswer(agentName, results); ok {
		return presentableFallbackCandidate(agentName, brief, results, answer)
	}

	return "", false
}

func synthesizeNoToolsFallbackAnswer(agentName string, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) (string, bool) {
	if len(results) == 0 {
		return "", false
	}
	if planThenExecuteAnchorRefreshFallbackPending(brief, results) {
		return "", false
	}

	if answer, ok := synthesizePlanThenExecutePlannerFallbackAnswer(brief, results); ok {
		return answer, true
	}

	if runnerBriefRequiresMutation(brief) {
		return synthesizeMutationFallbackAnswer(brief, results), true
	}

	if !runnerBriefRequiresMutation(brief) && !runnerVerificationRequired(brief) {
		closeout := readOnlyClosureDecisionForResults(brief, results)
		if readOnlyDirectAnswerLane(brief) {
			if answer, ok := closeout.ConcreteFallbackAnswer(); ok {
				return answer, true
			}
		}

		delegatedOwnerReady := delegatedImplementationShapeSynthesisAllowed(brief, results)
		if delegatedOwnerReady {
			if output, ok := bestObjectiveAnswerableTaskResultOutput(brief.Objective, results); ok {
				if normalized := normalizeDelegatedReadOnlyPacket(output); strings.TrimSpace(normalized) != "" {
					return normalized, true
				}
			}
		}

		if delegatedOwnerReady {
			if answer, ok := synthesizeDelegatedReadOnlyAnswer(brief.Objective, results); ok {
				return answer, true
			}
		}

		if implementationShapeOwnerContractApplies(brief) && !delegatedOwnerReady {
			return synthesizeBriefConversationFallback(brief, results), true
		}
	}

	return synthesizeFallbackAnswer(agentName, results)
}

func planThenExecuteAnchorRefreshFallbackPending(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	brief, ok := planThenExecuteFallbackBrief(brief, results)
	if !ok {
		return false
	}
	if planThenExecuteSubmitCorrectionBlockerAllowed(brief, results) {
		return false
	}

	return planThenExecuteFallbackHasUnresolvedInvalidAnchorRefresh(brief, results)
}

func synthesizePlanThenExecutePlannerFallbackAnswer(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) (string, bool) {
	brief, ok := planThenExecuteFallbackBrief(brief, results)
	if !ok {
		return "", false
	}

	state := DerivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true})
	if state.Mode == PlanExecutePlanningModeAnchorRefresh && len(state.RefreshPaths) > 0 {
		if state.SubmitCorrectionBlockerAllowed {
			if blocker := synthesizePlanThenExecuteSubmitCorrectionBlocker(brief, results); blocker != "" {
				return blocker, true
			}
		}
		return "", false
	}
	if planThenExecuteFallbackHasUnresolvedInvalidAnchorRefresh(brief, results) {
		return "", false
	}

	if state.Mode == PlanExecutePlanningModeSubmitCorrection {
		if state.SubmitCorrectionBlockerAllowed {
			if blocker := synthesizePlanThenExecuteSubmitCorrectionBlocker(brief, results); blocker != "" {
				return blocker, true
			}

			return strings.TrimSpace("<<QUESTION>>: Plan-execute submit correction is still required; no valid plan artifact has been accepted.\n\n" +
				"Next valid action: emit one parser-readable submit_plan covering every required edit path with real executable edits, or answer <<QUESTION>> with the exact path-specific missing body/anchor blocker if gathered evidence cannot support a safe executable edit body."), true
		}

		return strings.TrimSpace("<<QUESTION>>: Plan-execute submit correction is still required; no valid plan artifact has been accepted.\n\n" +
			"Next valid action: emit one parser-readable submit_plan covering every required edit path with real executable edits or top-level source_satisfied_paths for already-final paths."), true
	}

	if pending := planThenExecuteHistoryPendingPaths(brief, results, nil); len(pending) > 0 {
		return strings.TrimSpace("<<QUESTION>>: Plan-execute git history evidence is still required before submit_plan.\n\n" +
			"Remaining history paths:\n" + bulletList(pending)), true
	}

	if unread := planThenExecuteUnreadRequiredPaths(brief, results); len(unread) > 0 {
		return strings.TrimSpace("<<QUESTION>>: Plan-execute source-body evidence is still required before submit_plan.\n\n" +
			"Remaining source paths:\n" + bulletList(mapKeys(unread))), true
	}

	return "", false
}

func planThenExecuteFallbackHasUnresolvedInvalidAnchorRefresh(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	rejected, _, ok := lastRejectedSubmitPlanCoverageResult(results)
	if !ok || len(submitPlanInvalidAnchorPaths(rejected)) == 0 {
		return false
	}

	return len(planThenExecuteFallbackUnresolvedLastRejectedRefreshPaths(brief, results)) > 0
}

func planThenExecuteFallbackUnresolvedLastRejectedRefreshPaths(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) []string {
	rejected, rejectedAt, ok := lastRejectedSubmitPlanCoverageResult(results)
	if !ok {
		return nil
	}

	refreshPaths := submitPlanRefreshPathCandidates(rejected)
	if len(refreshPaths) == 0 {
		return nil
	}
	wholeFileRewritePaths := submitPlanWholeFileExistingSourceRewritePaths(rejected)
	allowUnsafeRewriteBlocker := planThenExecuteSubmitPlanMissingBodyOrUnsafeAnchor(rejected) &&
		len(planThenExecuteRejectedSubmitPlanStreak(results)) >= planThenExecuteRejectedSubmitPlanStreakThreshold

	var unresolved []string
	for _, rawPath := range planThenExecuteRefreshScopePathsForRejectedPlan(brief, results, refreshPaths) {
		path := normalizeRunnerPath(rawPath)
		if path == "" {
			continue
		}
		if _, required := refreshPaths[path]; !required {
			continue
		}
		if _, wholeFileRewrite := wholeFileRewritePaths[path]; wholeFileRewrite && allowUnsafeRewriteBlocker {
			continue
		}
		if _, invalid := submitPlanInvalidAnchorPaths(rejected)[path]; invalid {
			if successfulInvalidAnchorCorrectionEvidencePathAfter(results, rejectedAt, path) {
				continue
			}
		} else if successfulSourceEvidencePathAfter(results, rejectedAt, path) {
			continue
		}

		unresolved = append(unresolved, path)
	}

	return uniqueSortedRunnerPaths(unresolved)
}

func synthesizePlanThenExecuteSubmitCorrectionBlocker(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) string {
	rejected, _, ok := lastRejectedSubmitPlanCoverageResult(results)
	if !ok || !planThenExecuteSubmitPlanMissingBodyOrUnsafeAnchor(rejected) {
		return ""
	}

	paths := submitPlanRefreshPathCandidates(rejected)
	if len(paths) == 0 {
		paths = planThenExecuteRejectedSubmitPlanStreakInvalidPaths(results)
	}

	rejectedPathList := sortedRunnerPathKeys(paths)
	if len(rejectedPathList) == 0 {
		return ""
	}
	rejectedPathSet := make(map[string]struct{}, len(rejectedPathList))
	for _, path := range rejectedPathList {
		rejectedPathSet[path] = struct{}{}
	}

	requiredCoverage := planThenExecuteRequiredCoveragePaths(brief.RequiredEditPaths, "")
	missing := missingRequiredEditPaths(requiredCoverage, changedPathsFromToolResults(results))
	if len(missing) == 0 {
		missing = rejectedPathList
	}
	missingSet := make(map[string]struct{}, len(missing)+len(rejectedPathList))
	for _, path := range missing {
		missingSet[path] = struct{}{}
	}
	for _, path := range rejectedPathList {
		if _, ok := missingSet[path]; ok {
			continue
		}
		missing = append(missing, path)
		missingSet[path] = struct{}{}
	}

	reason := strings.TrimSpace(rejected.Error)
	if reason == "" {
		reason = strings.TrimSpace(rejected.Summary)
	}

	var b strings.Builder
	b.WriteString("<<QUESTION>>: Plan-execute submit correction is blocked because repeated submit_plan attempts did not produce a safe executable edit body or bounded anchor for every remaining required path.\n\n")
	b.WriteString("Path-level blockers:\n")
	for _, path := range missing {
		b.WriteString("- ")
		b.WriteString(path)
		if _, ok := rejectedPathSet[path]; ok {
			b.WriteString(": repeated submit_plan attempts did not produce a safe executable edit body or bounded anchor for this path.\n")
		} else {
			b.WriteString(": no accepted submit_plan artifact has covered this required path; a safe executable edit body or exact path-specific body/anchor blocker is still required.\n")
		}
	}
	b.WriteString("\nLast rejected submit_plan: ")
	b.WriteString(reason)

	return strings.TrimSpace(b.String())
}

func planThenExecuteFallbackBrief(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) (workerruntime.WorkerRuntimeBrief, bool) {
	if strings.TrimSpace(brief.TaskFamily) == PlanThenExecutePlannerTaskFamily {
		return brief, true
	}

	if !planThenExecuteFallbackEvidenceSeen(brief, results) {
		return brief, false
	}

	copy := brief

	copy.TaskFamily = PlanThenExecutePlannerTaskFamily

	return copy, true
}

func planThenExecuteFallbackEvidenceSeen(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	switch strings.ToLower(strings.TrimSpace(brief.Contract)) {
	case "plan_then_execute", "plan-execute":
		return true
	}

	for _, result := range results {
		tool := strings.TrimSpace(result.Tool)
		if tool != "submit_plan" {
			continue
		}

		text := strings.ToLower(strings.Join([]string{result.Summary, result.Error}, "\n"))
		if strings.Contains(text, "source_satisfied_paths") ||
			strings.Contains(text, "required edit path") ||
			strings.Contains(text, "bounded source-body refresh") ||
			strings.Contains(text, "plan-execute") {

			return true
		}
	}

	return false
}

func bulletList(values []string) string {
	var b strings.Builder

	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}

		b.WriteString("- ")
		b.WriteString(value)
		b.WriteString("\n")
	}

	return strings.TrimRight(b.String(), "\n")
}

func synthesizeMutationFallbackAnswer(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) string {
	changedPaths := mapKeys(changedPathsFromToolResults(results))
	missingRequiredPaths := missingRequiredEditPaths(brief.RequiredEditPaths, changedPathsFromToolResults(results))

	var lastError *workspace.ToolResult

	latestProgressIndex := -1

	for i := range results {
		result := results[i]
		if strings.TrimSpace(result.Error) != "" {
			continue
		}

		if workspace.IsWorkspaceWriteTool(result.Tool) || resultCountsAsSuccessfulVerification(result) {
			latestProgressIndex = i
		}
	}

	for i := range results {
		if i <= latestProgressIndex {
			continue
		}

		result := normalizeFallbackResult(results[i])
		if strings.TrimSpace(result.Error) == "" {
			continue
		}

		if isHarnessRejectionSummary(result.Summary) || isHarnessRejectionErrorText(result.Error) {
			continue
		}

		copy := result

		lastError = &copy
	}

	var b strings.Builder

	if len(missingRequiredPaths) > 0 {
		b.WriteString("<<QUESTION>>: Implementation incomplete; required edit path(s) remain unchanged.\n\n")
	} else if len(changedPaths) > 0 {
		b.WriteString(mutationFallbackCompletionLead(brief, changedPaths))
		b.WriteString("\n\n")
	}

	if len(changedPaths) > 0 {
		b.WriteString("Changed paths:\n")

		for _, path := range changedPaths {
			b.WriteString("- ")
			b.WriteString(path)
			b.WriteString("\n")
		}

		b.WriteString("\n")
	} else {
		b.WriteString("Changed paths: none\n\n")
	}

	if len(missingRequiredPaths) > 0 {
		b.WriteString("Required edit paths not changed:\n")

		for _, path := range missingRequiredPaths {
			b.WriteString("- ")
			b.WriteString(path)
			b.WriteString("\n")
		}

		b.WriteString("\n")
	}

	if latestSourceVerification, ok := latestVerificationAfterLatestSourceWrite(results); sourceEditsNeedSemanticVerification(brief, results) && ok && verificationResultIsPatchHygieneOnly(latestSourceVerification) {
		b.WriteString("Verification: ")

		if summary, ok := latestSuccessfulVerificationSummary(results); ok {
			b.WriteString(summary)
			b.WriteString(" (patch hygiene only); ")
		}

		if plannerOwnsVerification(brief) {
			b.WriteString("source-aware lane sanity still pending; planner-owned build/test verification not run by lane.\n")
		} else {
			b.WriteString("source-aware verification pending for source edits.\n")
		}
	} else if summary, ok := latestSuccessfulVerificationSummary(results); ok {
		b.WriteString("Verification: ")
		b.WriteString(summary)
		b.WriteString("\n")
	} else if hasPendingVerificationAfterWrites(results, brief) {
		if plannerOwnsVerification(brief) {
			b.WriteString("Verification: pending lane sanity; build/test verification is planner-owned.\n")
		} else {
			b.WriteString("Verification: blocked; workspace writes are still pending verification.\n")
		}
	} else {
		b.WriteString("Verification: not run or not detected.\n")
	}

	if lastError != nil {
		b.WriteString("\nBlocker:\n- ")
		b.WriteString(lastError.Summary)

		if errText := strings.TrimSpace(lastError.Error); errText != "" {
			b.WriteString(": ")
			b.WriteString(errText)
		}

		b.WriteString("\n")
	} else if len(changedPaths) == 0 {
		b.WriteString("\nBlocker:\n- The implementation lane ended before a successful workspace edit was recorded.\n")
	}

	return strings.TrimSpace(b.String())
}

func mutationFallbackCompletionLead(brief workerruntime.WorkerRuntimeBrief, changedPaths []string) string {
	objective := strings.ToLower(strings.TrimSpace(brief.Objective))
	for _, path := range changedPaths {
		if strings.Contains(strings.ToLower(path), "label") {
			return "Implemented the label change."
		}
	}

	if strings.Contains(objective, "label") {
		return "Implemented the label change."
	}

	return "Implemented the requested change."
}

func (r *Runner) modelErrorAfterPartialEditBlockerAnswer(modelErr error, results []workspace.ToolResult) (string, bool) {
	if r == nil || modelErr == nil || !runnerBriefRequiresMutation(r.RuntimeBrief) {
		return "", false
	}

	if len(changedPathsFromToolResults(results)) == 0 {
		return "", false
	}

	detail := strings.TrimSpace(modelErr.Error())
	if detail == "" {
		detail = "model transport failed"
	}

	fallback := strings.TrimSpace(synthesizeMutationFallbackAnswer(r.RuntimeBrief, results))
	if strings.HasPrefix(fallback, "<<QUESTION>>:") {
		if _, remainder, ok := strings.Cut(fallback, "\n\n"); ok {
			fallback = strings.TrimSpace(remainder)
		} else {
			fallback = ""
		}
	}

	var b strings.Builder

	b.WriteString("<<QUESTION>>: Local model endpoint failed after workspace edits before implementation closeout: ")
	b.WriteString(detail)
	b.WriteString("\n\n")

	if fallback != "" {
		b.WriteString(fallback)
	}

	return strings.TrimSpace(b.String()), true
}

func latestSuccessfulVerificationSummary(results []workspace.ToolResult) (string, bool) {
	for i := len(results) - 1; i >= 0; i-- {
		result := results[i]
		if !resultCountsAsSuccessfulVerification(result) {
			continue
		}

		if command := shellCommandFromToolResult(result); command != "" {
			return command + " passed", true
		}

		if summary := strings.TrimSpace(result.Summary); summary != "" {
			if strings.Contains(strings.ToLower(summary), "passed") {
				return summary, true
			}

			return summary + " passed", true
		}

		return "verification passed", true
	}

	return "", false
}

func synthesizeFallbackAnswer(agentName string, results []workspace.ToolResult) (string, bool) {
	if len(results) == 0 {
		return "", false
	}

	var (
		firstSuccessful     *workspace.ToolResult
		lastError           *workspace.ToolResult
		successfulNarrowing []workspace.ToolResult
	)

	for i := range results {
		result := normalizeFallbackResult(results[i])
		if result.Error == "" && strings.TrimSpace(result.Output) != "" && firstSuccessful == nil {
			copy := result

			firstSuccessful = &copy
		}

		if result.Error == "" && strings.TrimSpace(result.Output) != "" && isResultUsefulForFallback(result) {
			successfulNarrowing = append(successfulNarrowing, result)
		}

		if result.Error != "" && !isHarnessRejectionSummary(result.Summary) && !isHarnessRejectionErrorText(result.Error) {
			copy := result

			lastError = &copy
		}
	}

	// OpenCode-style fallback framing (see docs/opencode-fixes-design.md,
	// `default.txt`): the model failed to synthesize a direct reply, but the
	// evidence collected during the turn is still a legitimate partial
	// answer. Lead with the findings as findings -- not with an apology.
	// Prior wording ("did not complete cleanly, but here is the evidence
	// gathered") read as a system error to users; this reframes the same
	// content as an honest "partial findings" report.
	var b strings.Builder

	if agentName == "explore" {
		b.WriteString("Partial findings from this turn (explore lane reached its budget before synthesizing):\n\n")
	} else {
		b.WriteString("Partial findings from this turn (the budget was reached before a full synthesis):\n\n")
	}

	if len(successfulNarrowing) > 0 || firstSuccessful != nil {
		b.WriteString("Verified context:\n")

		if len(successfulNarrowing) == 0 && firstSuccessful != nil {
			successfulNarrowing = append(successfulNarrowing, *firstSuccessful)
		}

		for _, result := range limitFallbackResults(successfulNarrowing, 3) {
			b.WriteString("- ")
			b.WriteString(result.Summary)

			if output := compactOutput(sanitizeFallbackResultOutput(result), fallbackLineBudgetForTool(result.Tool)); output != "" {
				b.WriteString("\n")
				b.WriteString(output)
			}

			b.WriteString("\n")
		}

		b.WriteString("\n")
	}

	if lastError != nil {
		b.WriteString("Final blocker:\n")
		b.WriteString("- ")
		b.WriteString(lastError.Summary)

		if errText := strings.TrimSpace(lastError.Error); errText != "" {
			b.WriteString(": ")
			b.WriteString(errText)
		}

		b.WriteString("\n\n")
	}

	// Before giving up, sift the verified context for any concrete
	// observation the agent can offer instead of a generic apology. Files
	// above a size threshold are surfaced as plausible refactor candidates
	// when the user's original prompt looked like a review / cleanup ask.
	if observations := inferFallbackObservations(successfulNarrowing); len(observations) > 0 {
		b.WriteString("Best-effort observations from what was read:\n")

		for _, obs := range observations {
			b.WriteString("- ")
			b.WriteString(obs)
			b.WriteString("\n")
		}

		b.WriteString("\n")
	}

	b.WriteString("Recommended next step:\n")

	if agentName == "explore" {
		b.WriteString("- answer from the verified evidence above; if resuming later, start from one exact missing claim and one exact probe rather than reopening broad discovery")
	} else {
		b.WriteString("- answer from the verified evidence above; if resuming later, name one exact missing claim and one exact probe before any additional reads")
	}

	return strings.TrimSpace(b.String()), true
}

func annotateBudgetLimitedAnswer(answer string) string {
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return ""
	}

	return answer
}

// inferFallbackObservations turns a handful of read_file / outline results
// into concrete talking points so the forced-completion fallback can offer
// something useful instead of a bare "I failed" message. Observations are
// bounded in count and content so they do not balloon the reply.
func inferFallbackObservations(results []workspace.ToolResult) []string {
	if len(results) == 0 {
		return nil
	}

	observations := make([]string, 0, 3)
	seen := make(map[string]struct{})

	for _, result := range results {
		if result.Tool != "read_file" {
			continue
		}

		path := extractReadFilePath(result.Summary)
		if path == "" {
			continue
		}

		if _, ok := seen[path]; ok {
			continue
		}

		seen[path] = struct{}{}

		lines := strings.Count(result.Output, "\n")

		if lines >= 600 {
			observations = append(observations, fmt.Sprintf("%s is unusually large (%d lines) -- a plausible refactor candidate on its own, even without deeper evidence.", path, lines))
		} else if lines >= 250 {
			observations = append(observations, fmt.Sprintf("%s (%d lines) was read; scan for functions exceeding one screen or mixed responsibilities as a small follow-up target.", path, lines))
		}

		if len(observations) >= 3 {
			break
		}
	}

	return observations
}

// extractReadFilePath pulls the path out of a read_file result's summary
// (the canonical format is "Read <display> lines N-M"). Returns empty on
// any parse failure so the caller just skips the observation.
func extractReadFilePath(summary string) string {
	trimmed := strings.TrimSpace(summary)
	if !strings.HasPrefix(trimmed, "Read ") {
		return ""
	}

	rest := strings.TrimPrefix(trimmed, "Read ")
	idx := strings.Index(rest, " lines ")

	if idx <= 0 {
		return ""
	}

	return strings.TrimSpace(rest[:idx])
}

// normalizeFallbackResult prepares a tool result for fallback synthesis by
// removing nested fallback wrappers from a delegated child's Output. When a
// child lane force-completes, its answer is already a "Partial findings from
// this turn..." template; that wrapper bleeds into the parent's view of the
// task result and produces nested "Partial findings" headers in the final
// answer. We strip the outer template so the substance (Verified context
// bullets, observations) folds cleanly into the parent's own template.
func normalizeFallbackResult(result workspace.ToolResult) workspace.ToolResult {
	if strings.TrimSpace(result.Tool) != "task" {
		return result
	}

	stripped := stripNestedFallbackTemplate(result.Output)
	if stripped != result.Output {
		result.Output = stripped
	}

	return result
}

// stripNestedFallbackTemplate removes the outer scaffolding of a
// synthesizeFallbackAnswer output so it can be embedded inside another
// fallback answer without producing duplicated headers. Idempotent: if the
// input is not a fallback template, it is returned unchanged.
func stripNestedFallbackTemplate(s string) string {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" || !strings.HasPrefix(trimmed, "Partial findings from this turn") {
		return s
	}

	// Drop the leading "Partial findings from this turn (...):" line plus
	// the immediately-following blank line, leaving just the substance.
	if idx := strings.Index(trimmed, "\n"); idx >= 0 {
		trimmed = strings.TrimLeft(trimmed[idx+1:], "\n")
	}

	// Drop the trailing "Recommended next step:" footer; that's
	// scaffolding for the user, not evidence.
	if idx := strings.Index(trimmed, "\nRecommended next step:"); idx >= 0 {
		trimmed = strings.TrimSpace(trimmed[:idx])
	}

	// Drop the inner "Verified context:" header. The outer fallback adds
	// its own header; nested verified-context labels make the answer read
	// like two stacked reports.
	trimmed = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(trimmed), "Verified context:"))
	// Drop a trailing "Final blocker:" if it's just a harness rejection --
	// that signal has already been recomputed by the outer template.
	if idx := strings.Index(trimmed, "\nFinal blocker:"); idx >= 0 {
		head := strings.TrimSpace(trimmed[:idx])
		tail := strings.TrimSpace(trimmed[idx+len("\nFinal blocker:"):])
		// Look one bullet ahead and drop the section if its content is a
		// known harness-only blocker; otherwise keep it.
		if firstBullet := firstBulletText(tail); isHarnessRejectionSummary(firstBullet) || isHarnessRejectionErrorText(tail) {
			trimmed = head
		}
	}

	return trimmed
}

// firstBulletText returns the text of the first bullet ("- ") in s, with the
// leading dash trimmed. Returns the empty string if no bullet is found.
func firstBulletText(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "- ") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "- "))
			if idx := strings.Index(line, ":"); idx >= 0 {
				return strings.TrimSpace(line[:idx])
			}

			return line
		}
	}

	return ""
}

// isHarnessRejectionSummary returns true for tool-result Summary strings that
// originate from harness contracts (post-readiness gap requirement, packet
// anchoring, etc.) rather than from real tool failures. These are not useful
// "Final blocker" lines -- the user did nothing wrong and the message is
// internal to the harness's own enforcement loop.
func isHarnessRejectionSummary(summary string) bool {
	summary = normalizedHarnessRejectionSummary(summary)
	if postReadinessContinuationRejectionSummary(summary) {
		return true
	}

	switch summary {
	case "Post-child reread blocked",
		"Broad exploration after delegation nudge blocked",
		"Additional read_only child task blocked",
		"Broad inspection request rejected",
		"Verification required after workspace edits",
		"Repeated dead-end action rejected",
		"Chained list_files rejected",
		"Chained search rejected":
		return true
	}

	return false
}

// isHarnessRejectionErrorText returns true when the error text reads like a
// harness contract message rather than a real tool error. Used as a fallback
// when the Summary itself isn't enough to classify the rejection.
func isHarnessRejectionErrorText(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}

	return containsAny(lower,
		"include `named_gap`",
		"include named_gap",
		"already contains sufficient evidence to bias toward answering",
		"file-level raw read is too broad",
		"file-level outline is broad orientation",
		"do not restart broad discovery",
		"successful write(s) since the last verification",
		"this action class already failed earlier in the turn",
	)
}

func isResultUsefulForFallback(result workspace.ToolResult) bool {
	lowerSummary := strings.ToLower(strings.TrimSpace(result.Summary))
	lowerOutput := strings.ToLower(strings.TrimSpace(result.Output))

	if strings.HasPrefix(lowerSummary, "redirected to ") || containsAny(lowerOutput,
		"try: {\"tool\":",
		"retry your original request and it will execute",
	) {

		return false
	}

	switch result.Tool {
	case "task", "discovery_query", "read_file", "search_content", "search_files",
		"go_read_symbol", "go_file_outline",
		"cpp_read_symbol", "cpp_file_outline",
		"python_read_symbol", "python_file_outline",
		"ts_read_symbol", "ts_file_outline",
		"rust_read_symbol", "rust_file_outline",
		"java_read_symbol", "java_file_outline":
		return true
	default:
		return false
	}
}

func usefulFallbackResultCount(results []workspace.ToolResult) int {
	count := 0

	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" || strings.TrimSpace(result.Output) == "" || !isResultUsefulForFallback(result) {
			continue
		}

		count++
	}

	return count
}

func limitFallbackResults(results []workspace.ToolResult, max int) []workspace.ToolResult {
	if max <= 0 || len(results) <= max {
		return results
	}

	return results[:max]
}

func fallbackLineBudgetForTool(tool string) int {
	switch tool {
	case "discovery_query":
		return 20
	case "search_content":
		return 6
	case "read_file":
		return 10
	default:
		return 8
	}
}
