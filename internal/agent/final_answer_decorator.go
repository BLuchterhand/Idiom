package agent

import (
	"encoding/json"
	"slices"
	"strings"

	"idiom/internal/frontier/pathparse"
	"idiom/internal/frontier/workerruntime"
	"idiom/internal/util"
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
	agentName := "main"
	if r != nil {
		agentName = r.agentName()
	}
	present := func(candidate string) string {
		return presentableFinalAnswerForUser(input.Brief, input.Results, candidate)
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
		return present(cleaned)
	}
	if !strings.HasPrefix(answer, "<<QUESTION>>") && !mutationReplyReportsConcreteNoopOrBlocker(strings.ToLower(answer)) {
		if blocker, ok := canonicalMutationBlockerFinalAnswer(input.Brief, input.Results, answer); ok {
			return present(blocker)
		}
	}
	if genericEvidenceAnswerIncludesLineOrientedSourceBody(answer) || looksLikeLineOrientedSourceSnippetAnswer(answer) {
		if composed, ok := r.composedFinalAnswerFromEvidence(input); ok {
			return present(composed)
		}
		if presentable, ok := presentableNoToolsFallbackAnswer(input.Brief, input.Results, answer); ok {
			if checked, ok := presentableOrReadOnlyNonAnswerReplacement(agentName, input.Brief, input.Results, presentable); ok {
				return present(checked)
			}
		}
		if conversationFallbackShouldAvoidSourceBody(input.Brief) {
			if checked, ok := presentableBriefConversationFallbackCandidate(agentName, input.Brief, input.Results); ok {
				return present(checked)
			}
		}
	}
	if finalAnswerStopReasonPreservesUncertainty(input.StopReason) &&
		!finalAnswerNeedsDecoration(answer) &&
		!looksLikeGenericEvidenceComposerAnswer(answer) {
		return present(answer)
	}
	if !finalAnswerNeedsDecoration(answer) && !looksLikeGenericEvidenceComposerAnswer(answer) {
		return present(answer)
	}
	if cleaned, ok := cleanedSubstantiveFinalAnswer(answer); ok {
		return present(cleaned)
	}
	if composed, ok := r.composedFinalAnswerFromEvidence(input); ok {
		return present(composed)
	}
	if presentable, ok := presentableNoToolsFallbackAnswer(input.Brief, input.Results, answer); ok {
		if checked, ok := presentableOrReadOnlyNonAnswerReplacement(agentName, input.Brief, input.Results, presentable); ok {
			return present(checked)
		}
	}
	return present(answer)
}

func presentableFinalAnswerForUser(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, answer string) string {
	answer = strings.TrimSpace(answer)
	if presentable, ok := presentableNonMutationQuestionBlocker(brief, results, answer); ok {
		return presentable
	}
	return answer
}

func presentableNonMutationQuestionBlocker(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, blocker string) (string, bool) {
	brief = workerruntime.NormalizeRuntimeBrief(brief)
	if runnerBriefRequiresMutation(brief) ||
		workerruntime.TaskFamilyIsPlanThenExecutePlanner(brief.TaskFamily) ||
		workerruntime.TaskFamilyIsPlanThenExecuteReview(brief.TaskFamily) {
		return "", false
	}
	reason := strings.TrimSpace(blocker)
	if !strings.HasPrefix(reason, "<<QUESTION>>") {
		return "", false
	}
	if answer, ok := deterministicNoToolsAnswer("main", brief, results); ok {
		answer = strings.TrimSpace(answer)
		if answer != "" &&
			!strings.HasPrefix(answer, "<<QUESTION>>") &&
			!readOnlyAnswerReportsInsufficientEvidence(answer) &&
			presentableQuestionBlockerDeterministicAnswerSupported(brief, results, answer) {
			return answer, true
		}
	}
	reason = strings.TrimPrefix(reason, "<<QUESTION>>")
	reason = strings.TrimLeft(strings.TrimSpace(reason), ": ")
	internalReason := questionBlockerReasonLooksInternal(reason)
	reason = presentableQuestionBlockerReason(reason)
	if reason == "" {
		reason = "the run stopped before producing a supported answer."
	}
	if !internalReason && !questionBlockerReasonLooksInternal(reason) && !strings.Contains(reason, "\n") {
		return ensureTrailingPeriod(reason), true
	}
	evidenceLabel, paths := questionBlockerContextPaths(brief, results, 4)

	var b strings.Builder
	b.WriteString(presentableUnsupportedFinalAnswerLead())
	if reason != "" {
		b.WriteString(" The unresolved evidence gap was: ")
		b.WriteString(lowercaseFirstSentence(reason))
		if !strings.HasSuffix(strings.TrimSpace(b.String()), ".") {
			b.WriteString(".")
		}
	}
	if len(paths) > 0 {
		b.WriteString(" ")
		b.WriteString(evidenceLabel)
		b.WriteString(": ")
		b.WriteString(formatInlinePathList(paths, 4))
		b.WriteString(".")
	}
	return strings.TrimSpace(b.String()), true
}

func presentableQuestionBlockerDeterministicAnswerSupported(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, answer string) bool {
	return readOnlyClosureDecisionForResults(brief, results).SupportsPresentableQuestionBlockerAnswer(answer)
}

func questionBlockerContextPaths(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, max int) (string, []string) {
	if max <= 0 {
		max = 4
	}
	if ledger, candidateOnly := readOnlyDirectAnswerCandidateOnlyLedger(brief, results); candidateOnly {
		paths := closurePromptEvidencePaths(ledger.CandidatePaths, max)
		if len(paths) > 0 {
			return "Candidate paths surfaced", paths
		}
		return "", nil
	} else if readOnlyDirectAnswerLane(brief) {
		ledger = readOnlyAnswerabilityForResults(readOnlyDirectAnswerAcceptanceLedgerBrief(brief), results)
		paths := mergeRunnerStateStrings(ledger.RelevantSourceBodyPaths, ledger.SourceBodyObjectivePaths)
		if ledger.DiagnosticRelevant {
			paths = mergeRunnerStateStrings(paths, ledger.DiagnosticPaths)
		}
		paths = closurePromptEvidencePaths(paths, max)
		if len(paths) > 0 {
			return "Inspected evidence", paths
		}
		paths = readOnlyCloseoutEvidencePaths(brief, results, max)
		if len(paths) > 0 {
			return "Inspected evidence", paths
		}
		paths = promptEvidencePathsFromToolResults(results, max)
		if len(paths) > 0 {
			return "Inspected evidence", paths
		}
		return "", nil
	}
	paths := readOnlyCloseoutEvidencePaths(brief, results, max)
	if len(paths) == 0 {
		paths = delegatedClosureEvidencePaths(brief.Objective, results, max)
	}
	if len(paths) == 0 {
		return "", nil
	}
	return "Inspected evidence", paths
}

func presentableQuestionBlockerReason(reason string) string {
	reason = strings.TrimSpace(reason)
	if questionBlockerReasonLooksInternal(reason) {
		if malformed := extractMalformedWriteBlockerReason(reason); malformed != "" {
			return malformed
		}
		return "the run stopped before it had enough supported evidence for the requested answer"
	}
	replacements := []struct {
		old string
		new string
	}{
		{"`<<QUESTION>>:`", "a blocker"},
		{"`<<QUESTION>>`", "a blocker"},
		{"<<QUESTION>>:", "a blocker"},
		{"<<QUESTION>>", "a blocker"},
	}
	for _, replacement := range replacements {
		reason = strings.ReplaceAll(reason, replacement.old, replacement.new)
	}
	return strings.TrimSpace(reason)
}

func questionBlockerReasonLooksInternal(reason string) bool {
	lower := strings.ToLower(strings.TrimSpace(reason))
	if lower == "" {
		return false
	}
	return containsAny(lower,
		"completion gate",
		"missing completion evidence",
		"required next evidence",
		"answer with the exact",
		"current read-only answer has",
		"current read-only answer is",
		"current read-only answer uses",
		"no concrete user-facing finding was retained",
		"state the supported claim from the inspected source",
		"state the supported concrete finding from",
		"name the exact missing claim",
		"only localization evidence",
		"candidate paths but no relevant source-body evidence",
		"not backed by relevant source-body or diagnostic evidence",
		"missing source-body evidence for named path",
		"retained enough source-body evidence for a grounded answer",
		"read one objective-relevant source candidate",
		"gather one source-body read",
	)
}

func extractMalformedWriteBlockerReason(reason string) string {
	lower := strings.ToLower(reason)
	idx := strings.Index(lower, "blocked by malformed write request")
	if idx < 0 {
		return ""
	}
	end := idx + len("blocked by malformed write request")
	if end < len(reason) {
		if dot := strings.Index(reason[end:], "."); dot >= 0 {
			end += dot + 1
		}
	}
	return ensureTrailingPeriod(strings.TrimSpace(reason[idx:end]))
}

func ensureTrailingPeriod(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if strings.HasSuffix(s, ".") || strings.HasSuffix(s, "!") || strings.HasSuffix(s, "?") {
		return s
	}
	return s + "."
}

func runnerWorkspaceRoot(r *Runner) string {
	if r == nil || r.Workspace == nil {
		return ""
	}
	return strings.TrimSpace(r.Workspace.Root())
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
	if workerruntime.TaskFamilyIsPlanThenExecutePlanner(brief.TaskFamily) && successfulSubmitPlanResultCount(results) > 0 {
		return "", false
	}
	changed := changedPathsFromToolResults(results)
	missing := missingRequiredEditPaths(brief.RequiredEditPaths, changed)
	if workerruntime.TaskFamilyIsPlanThenExecutePlanner(brief.TaskFamily) &&
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
	if exact, ok := presentableExactProbeAnswer(input.Brief, input.Results); ok {
		return exact, true
	}
	delegatedOwnerReady := delegatedImplementationShapeSynthesisAllowed(input.Brief, input.Results)
	if delegatedOwnerReady {
		if answer, ok := synthesizeReadOnlyEvidencePacketAnswer(input.Brief.Objective, readOnlyEvidencePacketsFromResults(input.Results)); ok {
			return answer, true
		}
	}
	if answer, ok := presentableProseEvidenceAnswer(input.Brief, input.Results); ok {
		return answer, true
	}
	if workerruntime.RuntimeContractIsArchitecture(input.Brief.Contract) {
		if answer, ok := synthesizeDelegatedReadOnlyAnswer(input.Brief.Objective, input.Results); ok {
			if genericEvidenceAnswerIncludesLineOrientedSourceBody(answer) {
				return "", false
			}
			if cleaned, ok := cleanedSubstantiveFinalAnswer(answer); ok {
				return cleaned, true
			}
			if (!finalAnswerNeedsDecoration(answer) && !looksLikeGenericEvidenceComposerAnswer(answer)) || questionPresentationBlock(input.Brief.Objective) != "" {
				return answer, true
			}
		}
	}
	if delegatedOwnerReady {
		if output, ok := bestObjectiveAnswerableTaskResultOutput(input.Brief.Objective, input.Results); ok {
			if cleaned, ok := cleanedSubstantiveFinalAnswer(output); ok {
				return cleaned, true
			}
			if !finalAnswerNeedsDecoration(output) && !looksLikeGenericEvidenceComposerAnswer(output) {
				return output, true
			}
		}
	}
	if delegatedOwnerReady {
		if answer, ok := synthesizeDelegatedReadOnlyAnswer(input.Brief.Objective, input.Results); ok {
			if genericEvidenceAnswerIncludesLineOrientedSourceBody(answer) {
				return "", false
			}
			if cleaned, ok := cleanedSubstantiveFinalAnswer(answer); ok {
				return cleaned, true
			}
			if !finalAnswerNeedsDecoration(answer) && !looksLikeGenericEvidenceComposerAnswer(answer) {
				return answer, true
			}
		}
	}
	return "", false
}

func delegatedImplementationShapeSynthesisAllowed(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if !implementationShapeOwnerContractApplies(brief) {
		return true
	}
	ledger := readOnlyAnswerabilityForResults(readOnlyDirectAnswerAcceptanceLedgerBrief(brief), results)
	return len(ledger.ImplementationOwnerPaths()) > 0
}

func implementationShapeOwnerContractApplies(brief workerruntime.WorkerRuntimeBrief) bool {
	brief = workerruntime.NormalizeRuntimeBrief(brief)
	return readOnlyDirectAnswerLane(brief) && objectiveAsksImplementationShape(brief.Objective)
}

func presentableNoToolsFallbackAnswer(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, internalAnswer string) (string, bool) {
	internalAnswer = strings.TrimSpace(internalAnswer)
	if internalAnswer == "" {
		return "", false
	}
	if !finalAnswerNeedsDecoration(internalAnswer) && !looksLikeGenericEvidenceComposerAnswer(internalAnswer) {
		return "", false
	}
	if answer, ok := presentableGroundedAnswerFromRenderedPacket(internalAnswer); ok {
		return answer, true
	}
	closure := readOnlyClosureDecisionForResults(brief, results)
	if readOnlyFallbackNeedsAnswerableEvidence(brief, internalAnswer) && !closure.Answerable() {
		return closure.UserFacingUncertainty(), true
	}
	if conversationFallbackShouldAvoidSourceBody(brief) && isDeterministicFallbackAnswer(internalAnswer) {
		if concrete, ok := closure.ConcreteFallbackAnswer(); ok {
			return concrete, true
		}
		return synthesizeBriefConversationFallback(brief, results), true
	}
	if conversationFallbackShouldAvoidSourceBody(brief) && looksLikeSourceBodyFallbackAnswer(internalAnswer) {
		return synthesizeBriefConversationFallback(brief, results), true
	}
	lineOrientedGenericAnswer := genericEvidenceAnswerIncludesLineOrientedSourceBody(internalAnswer) ||
		looksLikeLineOrientedSourceSnippetAnswer(internalAnswer)
	if !lineOrientedGenericAnswer {
		if cleaned, ok := cleanedSubstantiveFinalAnswer(internalAnswer); ok {
			return cleaned, true
		}
	}
	// For non-mutation turns where the model produced a genuine substantive
	// answer, prefer it over source-body fact extraction. Source-body extraction
	// from read_file results produces nonsense like "The inspected source
	// contains `"strings"`, `)` and `)`. No files were changed." for
	// conversation questions. But only apply this when the answer doesn't look
	// like a budget-closure or post-readiness stub — those need composition.
	if !lineOrientedGenericAnswer &&
		!finalAnswerNeedsDecoration(internalAnswer) &&
		!looksLikeGenericEvidenceComposerAnswer(internalAnswer) &&
		!runnerBriefRequiresMutation(brief) &&
		looksLikeSubstantiveFinalAnswer(internalAnswer) {
		lower := strings.ToLower(internalAnswer)
		if !strings.Contains(lower, "partial findings from this turn") &&
			!strings.Contains(lower, "budget was reached") &&
			!strings.Contains(lower, "post-readiness") {
			substantive := stripInternalEvidenceAppendix(internalAnswer)
			if !finalAnswerNeedsDecoration(substantive) && len(strings.TrimSpace(substantive)) >= 120 {
				return substantive, true
			}
		}
	}
	if exact, ok := presentableExactProbeAnswer(brief, results); ok {
		return exact, true
	}
	// When the internal answer is a deterministic fallback ("Partial findings
	// from this turn...") and we are in a read-only conversation lane, skip
	// source-body extraction. It produces nonsense like "The inspected source
	// contains `type Evidence struct {`..." that does not answer the question.
	// Only block for direct user-facing conversation lanes, not for delegated
	// read-only investigation lanes that benefit from source-body composition.
	if isDeterministicFallbackAnswer(internalAnswer) && conversationFallbackShouldAvoidSourceBody(brief) {
		return synthesizeBriefConversationFallback(brief, results), true
	}

	if sourceBody, ok := presentableReadFileSourceAnswer(brief, results); ok {
		return sourceBody, true
	}
	if prose, ok := presentableProseEvidenceAnswer(brief, results); ok {
		return prose, true
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
	if !closure.Answerable() {
		return closure.UserFacingUncertainty(), true
	}
	return presentableUnsupportedFinalAnswerLead() + " The current read-only answer has relevant evidence in " +
		formatInlinePathList(paths, 4) +
		", but no concrete user-facing finding was retained.", true
}

func presentableGroundedAnswerFromRenderedPacket(answer string) (string, bool) {
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return "", false
	}
	lower := strings.ToLower(answer)
	marker := "grounded answer:\n"
	idx := strings.Index(lower, marker)
	if idx < 0 {
		return "", false
	}
	rest := strings.TrimSpace(answer[idx+len(marker):])
	if rest == "" {
		return "", false
	}
	cut := len(rest)
	for _, section := range []string{
		"\nGrounded findings:",
		"\nSource body paths:",
		"\nCandidate paths:",
		"\nConfidence:",
	} {
		if next := strings.Index(rest, section); next >= 0 && next < cut {
			cut = next
		}
	}
	grounded := strings.TrimSpace(rest[:cut])
	if grounded == "" ||
		finalAnswerNeedsDecoration(grounded) ||
		looksLikeGenericEvidenceComposerAnswer(grounded) ||
		looksLikeLowValueSourceInventoryNonAnswer(grounded) {
		return "", false
	}
	return grounded, true
}

func presentableReadOnlyClosureUncertainty(brief workerruntime.WorkerRuntimeBrief, closure readOnlyClosureDecision) string {
	_ = brief
	return closure.UserFacingUncertainty()
}

func presentableReadFileSourceAnswer(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) (string, bool) {
	if runnerBriefRequiresMutation(brief) || briefPrefersDelegatedPacketFallback(brief, results) {
		return "", false
	}
	var ownerPathSet map[string]struct{}
	if implementationShapeOwnerContractApplies(brief) {
		ledger := readOnlyAnswerabilityForResults(readOnlyDirectAnswerAcceptanceLedgerBrief(brief), results)
		ownerPathSet = readOnlyPathSet(ledger.ImplementationOwnerPaths())
		if len(ownerPathSet) == 0 {
			return "", false
		}
	}
	bestAnswer := ""
	bestScore := -1
	for i := len(results) - 1; i >= 0; i-- {
		result := results[i]
		if strings.TrimSpace(result.Error) != "" {
			continue
		}
		if strings.TrimSpace(result.Tool) != "read_file" {
			continue
		}
		if !readFileResultHasConcreteSourceBody(result) {
			continue
		}
		paths := readPathsFromToolResult(result)
		if len(paths) == 0 {
			continue
		}
		if isNonSourceEvidenceArtifactPath(paths[0]) {
			continue
		}
		output := strings.TrimSpace(result.Output)
		if answer, ok := presentableSourceBodyFactsAnswer(brief.Objective, paths[0], output); ok {
			if ownerPathSet != nil {
				if _, ok := ownerPathSet[normalizeRunnerPath(paths[0])]; !ok {
					continue
				}
			}
			if implementationShape, ok := presentableImplementationShapeAnswerFromClaim(brief.Objective, paths[0], "", stripPresentableSourceObjectiveEcho(answer)); ok {
				return implementationShape, true
			}
			score := presentableSourceAnswerObjectiveScore(answer, brief.Objective)
			if looksLikeSourceEchoOnlyAnswer(answer) && looksLikeLowValueSourceInventoryFallback(answer) {
				continue
			}
			if bestAnswer == "" || score > bestScore {
				bestScore = score
				bestAnswer = answer
			}
		}
	}
	return bestAnswer, strings.TrimSpace(bestAnswer) != ""
}

func presentableSourceAnswerObjectiveScore(answer string, objective string) int {
	claim := stripPresentableSourceObjectiveEcho(answer)
	score := sourceEvidenceLineObjectiveScore(claim, sourceEvidenceObjectiveLineTerms(objective))
	score += len(answerObjectiveTerms(claim, objective)) * 2
	if strings.Contains(strings.ToLower(claim), "defines `") {
		score--
	}
	return score
}

func stripPresentableSourceObjectiveEcho(answer string) string {
	trimmed := strings.TrimSpace(answer)
	if !strings.HasPrefix(trimmed, "For `") {
		return trimmed
	}
	rest := strings.TrimPrefix(trimmed, "For `")
	end := strings.Index(rest, "`, ")
	if end < 0 {
		return trimmed
	}
	return strings.TrimSpace(rest[end+len("`, "):])
}

func presentableProseEvidenceAnswer(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) (string, bool) {
	if runnerBriefRequiresMutation(brief) || runnerVerificationRequired(brief) || briefPrefersDelegatedPacketFallback(brief, results) {
		return "", false
	}
	objective := strings.TrimSpace(delegatedDisplayQuestion(brief.Objective))
	if objective == "" {
		return "", false
	}
	for i := len(results) - 1; i >= 0; i-- {
		result := results[i]
		if strings.TrimSpace(result.Error) != "" || strings.TrimSpace(result.Output) == "" {
			continue
		}
		if strings.TrimSpace(result.Tool) != "read_file" {
			continue
		}
		paths := readPathsFromToolResult(result)
		if len(paths) == 0 {
			continue
		}
		path := strings.TrimSpace(paths[0])
		if !presentableProseEvidencePath(path) {
			continue
		}
		lines := objectiveRelevantProseEvidenceLines(objective, result.Output, 4)
		if len(lines) == 0 {
			continue
		}
		return synthesizeProseEvidenceAnswer(objective, path, lines), true
	}
	return "", false
}

func presentableProseEvidencePath(path string) bool {
	lower := strings.ToLower(strings.TrimSpace(path))
	lower = strings.TrimPrefix(lower, "./")
	if lower == "" || isNonSourceEvidenceArtifactPath(lower) {
		return false
	}
	if strings.HasPrefix(lower, "docs/") || strings.HasPrefix(lower, ".codex/") {
		return true
	}
	if lower == "readme" || strings.HasPrefix(lower, "readme.") {
		return true
	}
	for _, suffix := range []string{".md", ".markdown", ".txt", ".rst", ".adoc"} {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	return false
}

type proseEvidenceScoredLine struct {
	line  string
	score int
	index int
}

func objectiveRelevantProseEvidenceLines(objective string, output string, max int) []string {
	if max <= 0 {
		max = 4
	}
	terms := recoveryObjectiveSearchWords(objective, 32)
	if len(terms) == 0 {
		return nil
	}
	termSet := make(map[string]struct{}, len(terms))
	for _, term := range terms {
		termSet[singularObjectiveWord(term)] = struct{}{}
	}
	var candidates []proseEvidenceScoredLine
	for index, raw := range strings.Split(output, "\n") {
		line := normalizeProseEvidenceLine(raw)
		if line == "" {
			continue
		}
		score := proseEvidenceLineObjectiveScore(line, termSet)
		if score <= 0 {
			continue
		}
		candidates = append(candidates, proseEvidenceScoredLine{line: line, score: score, index: index})
	}
	if len(candidates) == 0 {
		return nil
	}
	slices.SortFunc(candidates, func(a, b proseEvidenceScoredLine) int {
		if a.score != b.score {
			return b.score - a.score
		}
		return a.index - b.index
	})
	var selected []proseEvidenceScoredLine
	for _, candidate := range candidates {
		if proseEvidenceLineDuplicates(selected, candidate.line) {
			continue
		}
		selected = append(selected, candidate)
		if len(selected) >= max {
			break
		}
	}
	slices.SortFunc(selected, func(a, b proseEvidenceScoredLine) int {
		return a.index - b.index
	})
	lines := make([]string, 0, len(selected))
	for _, candidate := range selected {
		lines = append(lines, candidate.line)
	}
	return lines
}

func normalizeProseEvidenceLine(line string) string {
	line = stripSourceEvidenceLinePrefix(strings.TrimSpace(line))
	if line == "" {
		return ""
	}
	if strings.HasPrefix(line, "```") || strings.HasPrefix(line, "|") {
		return ""
	}
	line = strings.TrimLeft(line, "#")
	line = strings.TrimSpace(line)
	for {
		trimmed := strings.TrimSpace(line)
		next := strings.TrimPrefix(trimmed, "- ")
		next = strings.TrimPrefix(next, "* ")
		next = strings.TrimPrefix(next, "+ ")
		next = strings.TrimPrefix(next, "> ")
		if next == trimmed {
			break
		}
		line = next
	}
	line = strings.TrimSpace(line)
	if line == "" || len(line) < 24 {
		return ""
	}
	lower := strings.ToLower(line)
	if strings.HasPrefix(lower, "package ") ||
		strings.HasPrefix(lower, "import ") ||
		strings.HasPrefix(lower, "func ") ||
		strings.HasPrefix(lower, "type ") ||
		strings.HasPrefix(lower, "const ") ||
		strings.HasPrefix(lower, "var ") ||
		strings.HasPrefix(lower, "return ") {
		return ""
	}
	if len(line) > 220 {
		line = strings.TrimSpace(line[:220])
	}
	return strings.Trim(line, "`*_ ")
}

func proseEvidenceLineObjectiveScore(line string, termSet map[string]struct{}) int {
	lower := strings.ToLower(line)
	words := strings.FieldsFunc(lower, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	})
	score := 0
	seen := map[string]struct{}{}
	for _, word := range words {
		word = singularObjectiveWord(word)
		if len(word) < 4 {
			continue
		}
		if _, ok := termSet[word]; ok {
			if _, dup := seen[word]; !dup {
				score += 2
				seen[word] = struct{}{}
			}
		}
	}
	if score > 0 && containsAny(lower, "evidence", "contract", "source", "repo", "repository", "audit", "eval", "verification", "runtime brief", "harness") {
		score++
	}
	return score
}

func proseEvidenceLineDuplicates(selected []proseEvidenceScoredLine, line string) bool {
	normalized := strings.ToLower(strings.TrimSpace(line))
	for _, candidate := range selected {
		existing := strings.ToLower(strings.TrimSpace(candidate.line))
		if existing == normalized || strings.Contains(existing, normalized) || strings.Contains(normalized, existing) {
			return true
		}
	}
	return false
}

func synthesizeProseEvidenceAnswer(objective string, path string, lines []string) string {
	var b strings.Builder
	b.WriteString("For `")
	b.WriteString(strings.TrimSpace(objective))
	b.WriteString("`, `")
	b.WriteString(strings.TrimSpace(path))
	b.WriteString("` provides the grounded answer: ")
	for i, line := range lines {
		if i > 0 {
			b.WriteString(" ")
		}
		b.WriteString(strings.TrimRight(strings.TrimSpace(line), "."))
		b.WriteString(".")
	}
	return strings.TrimSpace(b.String())
}

func briefPrefersDelegatedPacketFallback(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if slices.Contains(brief.Constraints, "child_packet_available") {
		return true
	}
	for _, result := range results {
		if strings.TrimSpace(result.Tool) == "task" && strings.TrimSpace(result.Error) == "" {
			return true
		}
	}
	return false
}

func presentableSourceBodyFactsAnswer(objective string, path string, output string) (string, bool) {
	path = strings.TrimSpace(path)
	lines := sourceEvidenceAnswerLinesForObjective(output, objective, 24)
	if path == "" || len(lines) == 0 {
		return "", false
	}
	facts := sourceBodySemanticFacts(lines, objective, 5)
	if len(facts) == 0 {
		return "", false
	}
	var b strings.Builder
	if prefix := presentableSourceBodyObjectivePrefix(objective, lines); prefix != "" {
		b.WriteString(prefix)
		b.WriteString(" ")
	}
	if objective = strings.TrimSpace(objective); objective != "" {
		b.WriteString("For `")
		b.WriteString(objective)
		b.WriteString("`, the answer is grounded in ")
	}
	b.WriteString("`")
	b.WriteString(path)
	if objective != "" {
		b.WriteString("`: it shows ")
	} else {
		b.WriteString("` shows ")
	}
	factText := joinFallbackFacts(facts)
	b.WriteString(factText)
	b.WriteString(".")
	answer := strings.TrimSpace(b.String())
	if objectiveAsksBehaviorSummary(objective) && looksLikeLowValueSourceInventoryFallback(factText) {
		return "", false
	}
	return answer, true
}

func sourceBodySemanticFacts(lines []string, objective string, max int) []string {
	if max <= 0 {
		max = 3
	}
	facts := make([]string, 0, max)
	add := func(fact string) {
		fact = strings.TrimSpace(fact)
		if fact == "" || slices.Contains(facts, fact) {
			return
		}
		facts = append(facts, fact)
	}
	currentFunction := ""
	for _, line := range lines {
		functionLine := false
		if name := sourceBodyFunctionName(line); name != "" {
			currentFunction = name
			add("defines `" + name + "`")
			functionLine = true
		}
		if fact := sourceBodyObjectiveLiteralFact(currentFunction, line, objective); fact != "" {
			add(fact)
		}
		if !functionLine {
			if fact := sourceBodyReturnFact(currentFunction, line); fact != "" {
				add(fact)
			} else if fact := sourceBodyDefinitionFact(line); fact != "" {
				add(fact)
			} else if fact := sourceBodyCaseFact(currentFunction, line); fact != "" {
				add(fact)
			} else if fact := exactProbeAssignmentObservation(line); fact != "" {
				add(fact)
			} else if fact := sourceBodyCallFact(currentFunction, line); fact != "" {
				add(fact)
			}
		}
		if len(facts) >= max {
			return facts
		}
	}
	if len(facts) > 1 && currentFunction != "" && facts[0] == "defines `"+currentFunction+"`" {
		return facts[1:]
	}
	return facts
}

func sourceBodyObjectiveLiteralFact(functionName string, line string, objective string) string {
	literals := sourceBodyObjectiveStringLiterals(line, objective, 2)
	if len(literals) == 0 {
		return ""
	}
	if functionName != "" {
		return "`" + functionName + "` references " + joinFallbackFacts(literals)
	}
	return "references " + joinFallbackFacts(literals)
}

func sourceBodyObjectiveStringLiterals(line string, objective string, limit int) []string {
	if limit <= 0 {
		limit = 2
	}
	objective = strings.TrimSpace(objective)
	var literals []string
	inQuote := false
	escaped := false
	var current strings.Builder
	for _, r := range line {
		if !inQuote {
			if r == '"' {
				inQuote = true
				escaped = false
				current.Reset()
			}
			continue
		}
		if escaped {
			current.WriteRune(r)
			escaped = false
			continue
		}
		switch r {
		case '\\':
			escaped = true
		case '"':
			value := strings.TrimSpace(current.String())
			inQuote = false
			if sourceBodyLiteralSupportsObjective(value, objective) {
				literals = util.AppendUnique(literals, "`\""+value+"\"`")
				if len(literals) >= limit {
					return literals
				}
			}
		default:
			current.WriteRune(r)
		}
	}
	return literals
}

func sourceBodyLiteralSupportsObjective(value string, objective string) bool {
	value = strings.TrimSpace(value)
	if len(value) < 4 || len(value) > 120 {
		return false
	}
	if strings.ContainsAny(value, "\n\r\t") {
		return false
	}
	if objective == "" {
		return codeShapedTokenPattern.MatchString(value)
	}
	return textSharesObjectiveTerm(value, objective)
}

func sourceBodyCaseFact(functionName string, line string) string {
	line = strings.TrimSpace(line)
	rest, ok := strings.CutPrefix(line, "case ")
	if !ok {
		return ""
	}
	head, _, ok := strings.Cut(rest, ":")
	if !ok {
		return ""
	}
	var cases []string
	for _, raw := range strings.Split(head, ",") {
		value := strings.TrimSpace(raw)
		if value == "" || strings.Contains(value, `"`) {
			continue
		}
		if !strings.Contains(value, ".") && !codeShapedTokenPattern.MatchString(value) {
			continue
		}
		cases = append(cases, "`"+value+"`")
	}
	if len(cases) == 0 {
		return ""
	}
	if functionName != "" {
		return "`" + functionName + "` handles " + joinFallbackFacts(cases)
	}
	return "handles " + joinFallbackFacts(cases)
}

func sourceBodyCallFact(functionName string, line string) string {
	line = strings.TrimSpace(line)
	if line == "" {
		return ""
	}
	var calls []string
	for _, match := range callLikeIdentifierPattern.FindAllStringSubmatch(line, -1) {
		if len(match) <= 1 {
			continue
		}
		candidate, ok := delegatedReadOnlyLiteralSymbolCandidate(match[1])
		if !ok {
			continue
		}
		if strings.EqualFold(candidate, "String") || strings.EqualFold(candidate, "Error") {
			continue
		}
		calls = util.AppendUnique(calls, "`"+candidate+"`")
		if len(calls) >= 2 {
			break
		}
	}
	if len(calls) == 0 {
		return ""
	}
	if functionName != "" {
		return "`" + functionName + "` calls " + joinFallbackFacts(calls)
	}
	return "calls " + joinFallbackFacts(calls)
}

func sourceBodyFunctionName(line string) string {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "func ") {
		return ""
	}
	rest := strings.TrimSpace(strings.TrimPrefix(line, "func "))
	receiver := ""
	if strings.HasPrefix(rest, "(") {
		if idx := strings.Index(rest, ")"); idx >= 0 {
			receiver = sourceBodyReceiverType(rest[1:idx])
			rest = strings.TrimSpace(rest[idx+1:])
		}
	}
	if rest == "" {
		return ""
	}
	end := len(rest)
	for _, marker := range []string{"(", "[", " ", "\t"} {
		if idx := strings.Index(rest, marker); idx >= 0 && idx < end {
			end = idx
		}
	}
	name := strings.TrimSpace(rest[:end])
	if name == "" {
		return ""
	}
	if receiver != "" {
		return receiver + "." + name
	}
	return name
}

func sourceBodyReceiverType(receiver string) string {
	fields := strings.Fields(strings.TrimSpace(receiver))
	if len(fields) == 0 {
		return ""
	}
	typ := fields[len(fields)-1]
	typ = strings.TrimLeft(strings.TrimSpace(typ), "*")
	typ = strings.TrimPrefix(typ, "[]")
	typ = strings.TrimSpace(typ)
	if typ == "" || strings.ContainsAny(typ, " \t,(){}") {
		return ""
	}
	return typ
}

func sourceBodyReturnFact(functionName string, line string) string {
	expr, ok := strings.CutPrefix(strings.TrimSpace(line), "return ")
	if !ok {
		return ""
	}
	expr = exactProbeTrimExpression(expr)
	if expr == "" {
		return ""
	}
	if functionName != "" {
		return "`" + functionName + "` returns `" + expr + "`"
	}
	return "returns `" + expr + "`"
}

func sourceBodyDefinitionFact(line string) string {
	line = strings.TrimSpace(line)
	for _, keyword := range []string{"const", "var"} {
		rest, ok := strings.CutPrefix(line, keyword+" ")
		if !ok {
			continue
		}
		rest = strings.TrimSpace(rest)
		if rest == "" || strings.HasPrefix(rest, "(") {
			return ""
		}
		nameEnd := len(rest)
		for _, marker := range []string{"=", " ", "\t"} {
			if idx := strings.Index(rest, marker); idx >= 0 && idx < nameEnd {
				nameEnd = idx
			}
		}
		name := strings.TrimSpace(rest[:nameEnd])
		if name == "" {
			return ""
		}
		if _, rhs, ok := strings.Cut(rest, "="); ok {
			value := exactProbeTrimExpression(rhs)
			if value != "" {
				return "defines `" + name + "` as `" + value + "`"
			}
		}
		return "defines `" + name + "`"
	}
	if rest, ok := strings.CutPrefix(line, "type "); ok {
		rest = strings.TrimSpace(rest)
		if rest == "" || strings.HasPrefix(rest, "(") {
			return ""
		}
		nameEnd := len(rest)
		for _, marker := range []string{" ", "\t", "[", "{"} {
			if idx := strings.Index(rest, marker); idx >= 0 && idx < nameEnd {
				nameEnd = idx
			}
		}
		name := strings.TrimSpace(rest[:nameEnd])
		if name != "" {
			return "defines `" + name + "`"
		}
	}
	return ""
}

func presentableSourceBodyObjectivePrefix(objective string, lines []string) string {
	lower := strings.ToLower(strings.TrimSpace(objective))
	if !strings.Contains(lower, "can ") && !strings.Contains(lower, "could ") {
		return ""
	}
	if !containsAny(lower, "shorten", "rename", "change", "update") {
		return ""
	}
	if !containsAny(lower, "label", "text", "string", "copy") {
		return ""
	}
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.Contains(line, `"`) || strings.Contains(line, "`") {
			return "At the source level, yes."
		}
	}
	return ""
}

func normalizeSourceEvidenceLines(output string) string {
	var normalized []string
	for _, line := range strings.Split(output, "\n") {
		trimmed := stripSourceEvidenceLinePrefix(strings.TrimSpace(line))
		if trimmed == "" {
			continue
		}
		if idx := strings.Index(trimmed, "func "); idx >= 0 {
			trimmed = trimmed[idx:]
		} else if idx := strings.Index(trimmed, "return "); idx >= 0 {
			trimmed = trimmed[idx:]
		}
		normalized = append(normalized, trimmed)
	}
	return strings.Join(normalized, "\n")
}

func sourceEvidenceAnswerLinesForObjective(output string, objective string, maxLines int) []string {
	if maxLines <= 0 {
		maxLines = 8
	}
	all := sourceEvidenceAnswerLines(output, 256)
	if len(all) == 0 {
		return nil
	}
	if strings.TrimSpace(objective) == "" || len(all) <= maxLines {
		return limitStrings(all, maxLines)
	}
	terms := sourceEvidenceObjectiveLineTerms(objective)
	if len(terms) == 0 {
		return limitStrings(all, maxLines)
	}
	selected := make([]string, 0, maxLines)
	add := func(line string) {
		line = strings.TrimSpace(line)
		if line == "" || slices.Contains(selected, line) {
			return
		}
		selected = append(selected, line)
	}
	for _, line := range all {
		if sourceBodyFunctionName(line) != "" {
			add(line)
			break
		}
	}
	for _, line := range all {
		if sourceEvidenceLineObjectiveScore(line, terms) <= 0 {
			continue
		}
		add(line)
		if len(selected) >= maxLines {
			return selected
		}
	}
	if len(selected) == 0 {
		return limitStrings(all, maxLines)
	}
	return selected
}

func sourceEvidenceAnswerLines(output string, maxLines int) []string {
	if maxLines <= 0 {
		maxLines = 3
	}
	var lines []string
	for _, line := range strings.Split(output, "\n") {
		trimmed := stripSourceEvidenceLinePrefix(strings.TrimSpace(line))
		if trimmed == "" || sourceEvidenceLineIsBoilerplate(trimmed) {
			continue
		}
		if len(trimmed) > 160 {
			trimmed = strings.TrimSpace(trimmed[:160])
		}
		if slices.Contains(lines, trimmed) {
			continue
		}
		lines = append(lines, trimmed)
		if len(lines) >= maxLines {
			break
		}
	}
	return lines
}

func stripSourceEvidenceLinePrefix(line string) string {
	line = strings.TrimSpace(line)
	if line == "" {
		return ""
	}
	idx := 0
	for idx < len(line) && line[idx] >= '0' && line[idx] <= '9' {
		idx++
	}
	if idx == 0 || idx >= len(line) || line[idx] != ':' {
		return line
	}
	return strings.TrimSpace(line[idx+1:])
}

func sourceEvidenceLineIsBoilerplate(line string) bool {
	lower := strings.ToLower(strings.TrimSpace(line))
	if lower == "" {
		return true
	}
	if strings.HasPrefix(lower, "package ") || strings.HasPrefix(lower, "import ") {
		return true
	}
	return lower == "{" || lower == "}" || lower == "};" || strings.HasPrefix(lower, "//")
}

func presentableExactProbeAnswer(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) (string, bool) {
	if runnerBriefRequiresMutation(brief) || runnerVerificationRequired(brief) {
		return "", false
	}
	anchor, ok := bestPresentableExactProbeAnchor(brief, results)
	if !ok {
		return "", false
	}
	facts := exactProbeObservationsForObjective(anchor.symbol, anchor.output, brief.Objective)
	if len(facts) == 0 {
		return "", false
	}
	if implementationShape, ok := presentableImplementationShapeAnswerFromClaim(brief.Objective, anchor.path, anchor.symbol, strings.Join(facts, ", ")); ok {
		return implementationShape, true
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
	answer := strings.TrimSpace(b.String())
	if objectiveAsksBehaviorSummary(brief.Objective) && looksLikeLowValueSourceInventoryNonAnswer(answer) {
		return "", false
	}
	return answer, true
}

func presentableImplementationShapeAnswerFromClaim(objective string, path string, symbol string, claim string) (string, bool) {
	if !objectiveAsksImplementationShape(objective) {
		return "", false
	}
	path = strings.TrimSpace(path)
	claim = strings.TrimSpace(claim)
	if path == "" || claim == "" {
		return "", false
	}
	lowerClaim := " " + strings.ToLower(strings.Join(strings.Fields(claim), " ")) + " "
	if !containsAny(lowerClaim, " handles ", " calls ", " sets ") {
		return "", false
	}

	anchor := "`" + path + "`"
	if symbol = strings.TrimSpace(symbol); symbol != "" {
		anchor = "`" + symbol + "` in " + anchor
	} else if inferred := firstSourceInventoryCodeToken(claim); inferred != "" {
		anchor = "`" + inferred + "` in " + anchor
	}

	claim = presentableImplementationShapeClaim(path, claim)
	var parts []string
	parts = append(parts, "The code evidence points to "+anchor+" as the inspected implementation owner.")
	parts = append(parts, "In that source, "+ensureTrailingPeriod(strings.TrimRight(claim, ".")))
	return strings.Join(parts, " "), true
}

func presentableImplementationShapeClaim(path string, claim string) string {
	claim = strings.TrimSpace(claim)
	path = strings.TrimSpace(path)
	if path != "" {
		prefix := "`" + path + "` shows "
		if strings.HasPrefix(claim, prefix) {
			claim = strings.TrimSpace(strings.TrimPrefix(claim, prefix))
		}
	}
	claim = strings.TrimPrefix(claim, "defines ")
	return strings.TrimSpace(claim)
}

func firstSourceInventoryCodeToken(claim string) string {
	for _, part := range strings.Split(claim, "`") {
		token := strings.TrimSpace(part)
		if token == "" || strings.ContainsAny(token, " \t\n\r") {
			continue
		}
		lower := strings.ToLower(token)
		if lower == "true" || lower == "false" || strings.HasPrefix(lower, "tea.key") {
			continue
		}
		if strings.Contains(token, "/") || strings.Contains(lower, ".go") || strings.Contains(lower, ".md") {
			continue
		}
		return token
	}
	return ""
}

type presentableExactProbeAnchor struct {
	path   string
	symbol string
	output string
}

func bestPresentableExactProbeAnchor(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) (presentableExactProbeAnchor, bool) {
	objective := strings.TrimSpace(brief.Objective)
	candidates := make([]presentableExactProbeAnchor, 0, len(results))
	var best presentableExactProbeAnchor
	requiresOwner := implementationShapeOwnerContractApplies(brief)
	ledger := readOnlyAnswerabilityLedger{}
	ownerPathSet := map[string]struct{}{}
	if requiresOwner {
		ledger = readOnlyAnswerabilityForResults(readOnlyDirectAnswerAcceptanceLedgerBrief(brief), results)
		ownerPathSet = readOnlyPathSet(ledger.ImplementationOwnerPaths())
		if len(ownerPathSet) == 0 {
			return presentableExactProbeAnchor{}, false
		}
	}
	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" || strings.TrimSpace(result.Output) == "" {
			continue
		}
		if candidateAnchorOnlyResult(result) {
			continue
		}
		if presentableExactProbeResult(result) {
			path, symbol := extractExactProbeAnchor(result)
			candidates = append(candidates, presentableExactProbeAnchor{
				path:   path,
				symbol: symbol,
				output: strings.TrimSpace(result.Output),
			})
		}
	}
	for _, candidate := range candidates {
		var owner readOnlySourceOwnerEvidence
		if requiresOwner {
			if _, ok := ownerPathSet[normalizeRunnerPath(candidate.path)]; !ok {
				continue
			}
			owner, _ = ledger.SourceOwnerEvidenceForPath(candidate.path)
		}
		if len(exactProbeObservationsForObjective(candidate.symbol, candidate.output, objective)) == 0 {
			continue
		}
		if best.path == "" || presentableExactProbeAnchorObjectiveScore(candidate, objective, owner) > presentableExactProbeAnchorObjectiveScore(best, objective, sourceOwnerEvidenceForBestAnchor(ledger, best)) {
			best = candidate
		}
	}
	if best.path != "" {
		return best, true
	}
	if requiresOwner {
		return presentableExactProbeAnchor{}, false
	}
	if len(candidates) == 0 {
		return presentableExactProbeAnchor{}, false
	}
	return candidates[0], true
}

func sourceOwnerEvidenceForBestAnchor(ledger readOnlyAnswerabilityLedger, anchor presentableExactProbeAnchor) readOnlySourceOwnerEvidence {
	evidence, _ := ledger.SourceOwnerEvidenceForPath(anchor.path)
	return evidence
}

func presentableExactProbeAnchorObjectiveScore(candidate presentableExactProbeAnchor, objective string, owner readOnlySourceOwnerEvidence) int {
	text := strings.Join([]string{candidate.symbol, candidate.output}, "\n")
	score := sourceEvidenceLineObjectiveScore(text, sourceEvidenceObjectiveLineTerms(objective))
	score += len(answerObjectiveTerms(text, objective)) * 2
	if owner.OwnerEligible() {
		score += 20 + owner.Score
	} else if owner.Role == readOnlySourceOwnerConflictingNavigation {
		score -= 20
	}
	if sourceBodyHasStrongObjectiveToken(text, objective) {
		score += 3
	}
	return score
}

func exactProbeObservationsForObjective(symbol string, output string, objective string) []string {
	lines := sourceEvidenceAnswerLinesForObjective(output, objective, 24)
	if len(lines) > 0 {
		if facts := sourceBodySemanticFacts(lines, objective, 5); len(facts) > 0 {
			return facts
		}
	}
	return exactProbeObservations(symbol, output)
}

func presentableExactProbeResult(result workspace.ToolResult) bool {
	switch strings.TrimSpace(result.Tool) {
	case "go_read_symbol", "go_extract_body", "cpp_read_symbol", "cpp_extract_body", "python_read_symbol", "ts_read_symbol", "rust_read_symbol", "java_read_symbol":
		return true
	case "code_navigate", "read_only_capability":
		return routedSourceReadOutputHasBody(result.Output) && toolResultHasNonArtifactReadPath(result)
	default:
		return false
	}
}

func extractExactProbeAnchor(result workspace.ToolResult) (string, string) {
	summary := strings.TrimSpace(result.Summary)
	switch strings.TrimSpace(result.Tool) {
	case "go_read_symbol", "cpp_read_symbol", "python_read_symbol", "ts_read_symbol", "rust_read_symbol", "java_read_symbol":
		return extractReadExactProbeAnchor(summary)
	case "go_extract_body", "cpp_extract_body":
		if rest, ok := strings.CutPrefix(summary, "Extracted "); ok {
			if symbol, remainder, found := strings.Cut(rest, " in "); found {
				return extractExactProbePath(remainder), strings.TrimSpace(symbol)
			}
		}
	case "code_navigate", "read_only_capability":
		if routeSummary := routeSummaryFromToolOutput(result.Output); routeSummary != "" {
			return extractReadExactProbeAnchor(routeSummary)
		}
		return extractReadExactProbeAnchor(summary)
	}
	return "", ""
}

func extractReadExactProbeAnchor(summary string) (string, string) {
	if rest, ok := strings.CutPrefix(strings.TrimSpace(summary), "Read "); ok {
		if symbol, remainder, found := strings.Cut(rest, " in "); found {
			return extractExactProbePath(remainder), strings.TrimSpace(symbol)
		}
		return extractExactProbePath(rest), ""
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
		if value == "" || slices.Contains(observations, value) {
			return
		}
		observations = append(observations, value)
	}

	for _, line := range exactProbeSourceLines(output) {
		if observation := exactProbeReturnObservation(line); observation != "" {
			add(observation)
		}
		if len(observations) >= 3 {
			return observations
		}
		if observation := exactProbeAssignmentObservation(line); observation != "" {
			add(observation)
		}
		if len(observations) >= 3 {
			return observations
		}
	}

	calls := exactProbeCallNames(symbol, output, 4)
	if len(calls) > 0 {
		add("calls " + quotedFallbackCallNames(calls))
	}
	return observations
}

func exactProbeSourceLines(output string) []string {
	var lines []string
	for _, line := range strings.Split(output, "\n") {
		line = stripSourceEvidenceLinePrefix(strings.TrimSpace(line))
		if line == "" || sourceEvidenceLineIsBoilerplate(line) {
			continue
		}
		if strings.HasPrefix(line, "capability:") ||
			strings.HasPrefix(line, "language:") ||
			strings.HasPrefix(line, "routed_tool:") ||
			strings.HasPrefix(line, "route_summary:") {
			continue
		}
		lines = append(lines, line)
	}
	return lines
}

func exactProbeReturnObservation(line string) string {
	expr, ok := strings.CutPrefix(strings.TrimSpace(line), "return ")
	if !ok {
		return ""
	}
	expr = exactProbeTrimExpression(expr)
	if expr == "" {
		return ""
	}
	return "returns `" + expr + "`"
}

func exactProbeAssignmentObservation(line string) string {
	line = strings.TrimSpace(line)
	for _, op := range []string{":=", "="} {
		lhs, rhs, ok := strings.Cut(line, op)
		if !ok {
			continue
		}
		name := exactProbeAssignmentName(lhs)
		value := exactProbeTrimExpression(rhs)
		if name == "" || value == "" {
			return ""
		}
		if op == "=" {
			return "sets `" + name + "` to `" + value + "`"
		}
		return "assigns `" + name + "` from `" + value + "`"
	}
	return ""
}

func exactProbeAssignmentName(lhs string) string {
	lhs = strings.TrimSpace(lhs)
	if strings.ContainsAny(lhs, " \t,(){}[]") {
		return ""
	}
	if lhs == "" || strings.HasPrefix(lhs, "if") || strings.HasPrefix(lhs, "for") || strings.HasPrefix(lhs, "switch") {
		return ""
	}
	return lhs
}

func exactProbeTrimExpression(expr string) string {
	expr = strings.TrimSpace(expr)
	if idx := strings.Index(expr, "//"); idx >= 0 {
		expr = strings.TrimSpace(expr[:idx])
	}
	expr = strings.TrimRight(strings.TrimSpace(expr), ",")
	if len(expr) > 120 {
		expr = strings.TrimSpace(expr[:120])
	}
	return expr
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
		calls = util.AppendUnique(calls, candidate)
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

func bestObjectiveAnswerableTaskResultOutput(objective string, results []workspace.ToolResult) (string, bool) {
	for i := len(results) - 1; i >= 0; i-- {
		result := results[i]
		if !readOnlyTaskResultAnswerableForObjective(result, objective) {
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
	if looksLikeRawToolResultTranscript(answer) {
		return true
	}
	if looksLikeLineOrientedSourceSnippetAnswer(answer) {
		return true
	}
	if looksLikeTextualToolCall(answer) {
		return true
	}
	if looksLikeStructuredAnswerEnvelope(lower) {
		return true
	}
	if strings.Contains(lower, "<invoke") ||
		strings.HasPrefix(lower, "what i found:") ||
		strings.HasPrefix(lower, "partial findings from this turn") ||
		strings.HasPrefix(lower, "## grounded answer") ||
		strings.HasPrefix(lower, "question:") ||
		strings.Contains(lower, "source: delegated read-only evidence packet") ||
		strings.Contains(lower, "the gathered evidence points to these implementation surfaces") ||
		strings.Contains(lower, "the retained source context identifies") ||
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

func looksLikeStructuredAnswerEnvelope(lower string) bool {
	if !strings.HasPrefix(strings.TrimSpace(lower), "{") {
		return false
	}
	return strings.Contains(lower, `"answer"`) &&
		(strings.Contains(lower, `"verification"`) ||
			strings.Contains(lower, `"evidence"`) ||
			strings.Contains(lower, `"sources"`))
}

func looksLikeRawToolResultTranscript(answer string) bool {
	lines := strings.Split(strings.TrimSpace(answer), "\n")
	first := ""
	seenSummary := false
	seenOutput := false
	seenStatus := false
	for _, line := range lines {
		lower := strings.ToLower(strings.TrimSpace(line))
		if lower == "" {
			continue
		}
		if first == "" {
			first = lower
		}
		if strings.HasPrefix(lower, "summary:") {
			seenSummary = true
		}
		if strings.HasPrefix(lower, "output:") {
			seenOutput = true
		}
		if strings.HasPrefix(lower, "status:") {
			seenStatus = true
		}
	}
	return strings.HasPrefix(first, "tool: ") && seenSummary && (seenOutput || seenStatus)
}

func looksLikeGenericEvidenceComposerAnswer(answer string) bool {
	lower := strings.ToLower(strings.TrimSpace(answer))
	if lower == "" {
		return false
	}
	return strings.Contains(lower, "the strongest implementation evidence is in") ||
		strings.Contains(lower, "use those findings as the source of truth") ||
		strings.Contains(lower, "what this establishes:") ||
		strings.Contains(lower, "the retained source context identifies") ||
		looksLikeSourceBodyFallbackAnswer(answer)
}

func looksLikeSourceBodyFallbackAnswer(answer string) bool {
	lower := strings.ToLower(strings.TrimSpace(answer))
	return strings.Contains(lower, "the inspected source in") &&
		strings.Contains(lower, "no files were changed")
}

func genericEvidenceAnswerIncludesLineOrientedSourceBody(answer string) bool {
	lower := strings.ToLower(strings.TrimSpace(answer))
	if !strings.Contains(lower, "what this establishes:") {
		return false
	}
	for _, raw := range strings.Split(answer, "\n") {
		line := strings.TrimSpace(raw)
		line = strings.TrimSpace(strings.TrimPrefix(line, "- "))
		if line == "" {
			continue
		}
		colon := strings.IndexByte(line, ':')
		if colon <= 0 || colon > 6 {
			continue
		}
		lineNumber := strings.TrimSpace(line[:colon])
		if lineNumber == "" {
			continue
		}
		allDigits := true
		for _, ch := range lineNumber {
			if ch < '0' || ch > '9' {
				allDigits = false
				break
			}
		}
		if allDigits {
			return true
		}
	}
	return false
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
	if looksLikeLowValueSourceInventoryNonAnswer(cleaned) || !looksLikeSubstantiveFinalAnswer(cleaned) || finalAnswerNeedsDecoration(cleaned) || looksLikeGenericEvidenceComposerAnswer(cleaned) {
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
		"\n\nSource: ",
		"\nSource: ",
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

// isConversationLikeLane returns true when the task family is a direct
// user-facing conversation lane where source-body extraction would produce
// incomprehensible code-fragment answers rather than useful findings.
func isConversationLikeLane(brief workerruntime.WorkerRuntimeBrief) bool {
	family := strings.ToLower(strings.TrimSpace(brief.TaskFamily))
	switch family {
	case "conversation", "direct_question", "information_request", "coding_help", "general_chat":
		return true
	default:
		return false
	}
}

func conversationFallbackShouldAvoidSourceBody(brief workerruntime.WorkerRuntimeBrief) bool {
	if !isConversationLikeLane(brief) {
		return false
	}
	lower := strings.ToLower(strings.TrimSpace(brief.Objective))
	if containsAny(lower, "shorten", "rename", "change", "update") &&
		containsAny(lower, ".go", ".rs", ".py", ".ts", ".tsx", ".js", ".jsx", "label", "function", "source") {
		return false
	}
	return true
}

// isDeterministicFallbackAnswer returns true when the internal answer is
// the deterministic fallback produced by synthesizeFallbackAnswer — the
// "Partial findings from this turn..." text that the harness generates when
// the model fails to produce a substantive answer.
func isDeterministicFallbackAnswer(answer string) bool {
	lower := strings.ToLower(strings.TrimSpace(answer))
	return strings.HasPrefix(lower, "partial findings from this turn")
}

// synthesizeBriefConversationFallback produces a short fallback for
// conversation lanes when the model failed but the deterministic fallback
// answer is available. It avoids the source-body extraction path that
// produces misleading code-fragment answers.
func synthesizeBriefConversationFallback(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) string {
	return readOnlyClosureDecisionForResults(brief, results).ConversationFallback()
}

func synthesizeBriefConversationUncertaintyFallback(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) string {
	return readOnlyClosureDecisionForResults(brief, results).ConversationUncertaintyFallback()
}

func conversationFallbackEvidenceAnchor(result workspace.ToolResult) string {
	if strings.TrimSpace(result.Error) != "" {
		return ""
	}
	if routeSummary := routeSummaryFromToolOutput(result.Output); routeSummary != "" {
		return routeSummary
	}
	summary := strings.TrimSpace(result.Summary)
	if summary == "" || isHarnessRejectionSummary(summary) {
		return ""
	}
	switch strings.TrimSpace(result.Tool) {
	case "search_content":
		if paths := sourceLikePathsFromText(result.Output, 6); len(paths) > 0 {
			return "Search hits included " + formatInlinePathList(paths, 6)
		}
		return summary
	case "context_pack", "context_query", "context_zoom", "code_navigate", "read_file", "search_files":
		if strings.TrimSpace(result.Tool) == "read_file" {
			paths := readPathsFromToolResult(result)
			if len(paths) > 0 && len(nonArtifactEvidencePaths(paths)) == 0 {
				return ""
			}
		}
		return summary
	default:
		return ""
	}
}

func sourceLikePathsFromText(text string, max int) []string {
	if max <= 0 {
		max = 6
	}
	paths := make([]string, 0, max)
	seen := map[string]struct{}{}
	for _, path := range pathparse.MentionedWorkspacePaths(normalizeWorkspacePathScanText(text)) {
		path = normalizeDelegatedDiscoveryEvidencePath(path)
		if path == "" || isNonSourceEvidenceArtifactPath(path) || !delegatedDiscoveryLooksLikeEvidencePath(path) {
			continue
		}
		if strings.Contains(path, "testdata/swe/generated-logs") {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
		if len(paths) >= max {
			break
		}
	}
	return paths
}

func routeSummaryFromToolOutput(output string) string {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if value, ok := strings.CutPrefix(line, "route_summary:"); ok {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func limitStrings(values []string, limit int) []string {
	if limit <= 0 || len(values) <= limit {
		return values
	}
	return values[:limit]
}
