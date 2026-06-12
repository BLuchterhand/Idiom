package agent

import (
	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/pathparse"
	"idiom/internal/platform/util"
	"idiom/internal/runtime/workerruntime"
	"slices"
	"strings"
	"unicode"
)

func (r *Runner) requiredPathCompletionRecoveryRejection(reply string, results []workspace.ToolResult) (workspace.ToolResult, bool) {
	missing := r.remainingRequiredEditPaths(results)
	submitCorrection := r.planThenExecutePlannerPhase() && planThenExecuteSubmitCorrectionActive(r.RuntimeBrief, results)
	anchorRefreshState := PlanExecutePlanningState{}
	anchorRefresh := false

	if r.planThenExecutePlannerPhase() {
		anchorRefreshState = DerivePlanExecutePlanningState(r.RuntimeBrief, results, PlanExecutePlanningTransport{TextualToolParser: true})
		anchorRefresh = anchorRefreshState.Mode == PlanExecutePlanningModeAnchorRefresh && len(anchorRefreshState.RefreshPaths) > 0
	}

	if len(missing) == 0 || (!submitCorrection && !anchorRefresh && requiredPathBlockerAnswerCovers(reply, missing)) {
		return workspace.ToolResult{}, false
	}

	if !anchorRefresh && r.planThenExecutePlannerPhase() && r.planThenExecuteSourceSatisfiedAnswerCovers(reply, results) {
		return workspace.ToolResult{}, false
	}

	if r.planThenExecutePlannerPhase() {
		if anchorRefresh {
			return workspace.ToolResult{
				Tool:    "completion_gate",
				Summary: "Completion gate rejected anchor-refresh final answer",
				Error:   "bounded source-body refresh remains required for rejected submit_plan anchor path(s): " + quotedRunnerStrings(anchorRefreshState.RefreshPaths) + ". Final answers, `Decision: source-satisfied`, and `<<QUESTION>>:` do not satisfy this phase while a concrete refresh path is available. Issue exactly one bounded `read_file` or `code_navigate` source-body request for one refresh path before corrected `submit_plan`.",
			}, true
		}

		correction := ""

		if rejected, _, ok := lastRejectedSubmitPlanCoverageResult(results); ok {
			correctionBrief := lastSubmitPlanRejectionBrief(rejected)
			if submitCorrection {
				correctionBrief = lastSubmitPlanCorrectionRejectionBrief(rejected)
			}

			correction = " Previous rejected submit_plan drafts do not count as coverage. " + correctionBrief + planThenExecuteSubmitPlanCorrectionInstructionForBrief(r.RuntimeBrief, rejected)
		}

		if submitCorrection {
			return workspace.ToolResult{
				Tool:    "completion_gate",
				Summary: "Completion gate rejected non-submit_plan correction",
				Error:   "required edit path(s) remain uncovered by corrected submit_plan: " + quotedRunnerStrings(missing) + "." + correction + " Submit-correction mode has exactly one valid outcome: parser-readable submit_plan. " + planThenExecuteSubmitPlanCoverageSentence() + " Do not answer `Decision: source-satisfied`, do not answer <<QUESTION>>, and do not reply in prose.",
			}, true
		}

		return workspace.ToolResult{
			Tool:    "completion_gate",
			Summary: "Completion gate rejected non-specific submit_plan blocker",
			Error:   "required edit path(s) remain uncovered by submit_plan: " + quotedRunnerStrings(missing) + "." + correction + " After completion-gate recovery, the required contract outcome is submit_plan (" + planThenExecuteSubmitPlanCoverageSentence() + "), `Decision: source-satisfied` with current-state evidence for every remaining required path, or <<QUESTION>> with one path-specific blocker reason for every remaining required path.",
		}, true
	}

	return workspace.ToolResult{
		Tool:    "completion_gate",
		Summary: "Completion gate rejected non-specific required-path blocker",
		Error:   "required edit path(s) remain unchanged: " + quotedRunnerStrings(missing) + ". After completion-gate recovery, the required contract outcome is a concrete write or <<QUESTION>> with one path-specific blocker reason for every remaining required path.",
	}, true
}

func requiredPathBlockerAnswerCovers(reply string, missing []string) bool {
	trimmed := strings.TrimSpace(reply)
	if trimmed == "" || len(missing) == 0 {
		return false
	}

	lower := strings.ToLower(trimmed)
	if !strings.HasPrefix(trimmed, "<<QUESTION>>") {
		return false
	}

	if !containsAny(lower,
		"blocked",
		"blocker",
		"cannot",
		"can't",
		"could not",
		"unable",
		"permission",
		"approval",
	) {

		return false
	}

	for _, path := range missing {
		if !strings.Contains(trimmed, path) {
			return false
		}
	}

	return true
}

func (r *Runner) completionGateRepeatedBlockerAnswer(result workspace.ToolResult, results []workspace.ToolResult) string {
	reason := strings.TrimSpace(result.Error)
	if reason == "" {
		reason = "the final answer did not satisfy the active completion gate"
	}

	changedPaths := mapKeys(changedPathsFromToolResults(results))

	var b strings.Builder

	b.WriteString("<<QUESTION>>: Completion gate rejected the final answer repeatedly before the required completion evidence was produced.")

	if len(changedPaths) > 0 {
		b.WriteString("\n\nChanged paths:\n")

		for _, path := range changedPaths {
			b.WriteString("- ")
			b.WriteString(path)
			b.WriteString("\n")
		}
	}

	if runnerBriefRequiresMutation(r.RuntimeBrief) {
		missing := r.remainingRequiredEditPaths(results)
		if len(missing) > 0 {
			b.WriteString("\nRequired edit paths not changed:\n")

			for _, path := range missing {
				b.WriteString("- ")
				b.WriteString(path)
				b.WriteString("\n")
			}

			b.WriteString("\nPath-level blockers:\n")

			for _, path := range missing {
				b.WriteString("- ")
				b.WriteString(path)

				if r.planThenExecutePlannerPhase() {
					b.WriteString(": no successful submit_plan edit entry was recorded for this path, and the recovery reply did not provide source-satisfied evidence or a path-specific blocker reason.\n")
				} else {
					b.WriteString(": no successful write was recorded for this path, and the recovery reply did not provide a path-specific blocker reason.\n")
				}
			}
		}
	}

	b.WriteString("\nMissing completion evidence: ")
	b.WriteString(reason)

	if !runnerBriefRequiresMutation(r.RuntimeBrief) {
		if paths := promptEvidencePathsFromToolResults(results, 8); len(paths) > 0 {
			b.WriteString("\n\nEvidence paths observed before rejection:\n")
			for _, path := range paths {
				b.WriteString("- ")
				b.WriteString(path)
				b.WriteString("\n")
			}
		}
	}

	if plannerOwnsVerification(r.RuntimeBrief) && hasPendingVerificationAfterWrites(results, r.RuntimeBrief) {
		b.WriteString("\n\nRequired next evidence: compiler-backed source-sanity or language diagnostics for source edits, one cheap non-build sanity check for non-source edits, or a concrete planner-owned verification blocker. `git diff --check` and generic source-sanity fallback are only patch hygiene for source edits.")
	}

	return strings.TrimSpace(b.String())
}

func (r *Runner) planThenExecuteRepeatedSubmitPlanCorrectionPrompt(req workspace.ToolRequest, lastResult *workspace.ToolResult, results []workspace.ToolResult, nativeSubmitPlan bool) (string, bool) {
	if r == nil || lastResult == nil || !r.planThenExecutePlannerPhase() {
		return "", false
	}

	if strings.TrimSpace(req.Tool) != workspace.ToolNameSubmitPlan || !planThenExecuteSubmitCorrectionActive(r.RuntimeBrief, results) {
		return "", false
	}

	if !planThenExecuteSubmitPlanRejectedForCorrection(req, *lastResult) {
		return "", false
	}

	state := DerivePlanExecutePlanningState(r.RuntimeBrief, results, PlanExecutePlanningTransport{TextualToolParser: true})
	if state.Mode == PlanExecutePlanningModeAnchorRefresh {
		return "", false
	}

	if nativeSubmitPlan {
		return planThenExecuteNativeSubmitPlanCorrectionPrompt(r.RuntimeBrief, *lastResult), true
	}

	prompt := "The last request repeated the same invalid `submit_plan`; it was not executed again. " +
		lastSubmitPlanCorrectionRejectionBrief(*lastResult) +
		"Submit a corrected parser-readable `submit_plan` now. " + planThenExecuteSubmitPlanCoverageSentence() + " Do not answer `Decision: source-satisfied`, do not answer `<<QUESTION>>:`, do not read/search/discovery again, and do not reply in prose. " +
		planThenExecuteSubmitPlanCorrectionInstructionForBrief(r.RuntimeBrief, *lastResult)

	return prompt, true
}

func (r *Runner) planThenExecuteRepeatedSubmitPlanCorrectionExhaustedRejection(req workspace.ToolRequest, lastResult *workspace.ToolResult, results []workspace.ToolResult) (string, string, bool) {
	if _, ok := r.planThenExecuteRepeatedSubmitPlanCorrectionPrompt(req, lastResult, results, false); !ok {
		return "", "", false
	}

	reason := ""
	if lastResult != nil {
		reason = lastSubmitPlanCorrectionRejectionBrief(*lastResult)
	}

	return "Repeated invalid submit_plan rejected",
		"this exact invalid `submit_plan` was repeated after submit-correction prompts. " + reason + "The only valid next action is a corrected parser-readable `submit_plan`. " + planThenExecuteSubmitPlanCoverageSentence() + " Do not answer `Decision: source-satisfied`, do not answer `<<QUESTION>>:`, do not call read/search/discovery, and do not choose a different tool.",
		true
}

func planThenExecuteSubmitCorrectionHiddenOnlyVisiblePrompt(base string) string {
	return "The previous retry produced hidden-only output with no parser-readable action. Hidden thinking is disabled for this retry; produce exactly one visible parser-readable `submit_plan` request now. Do not answer `Decision: source-satisfied`, do not answer `<<QUESTION>>:`, and do not call read/search/discovery.\n\n" + base
}

func (r *Runner) hostOwnedReadOnlyCloseoutAnswer(results []workspace.ToolResult) (string, bool) {
	return "", false
}

func textSharesObjectiveTerm(text string, objective string) bool {
	text = strings.ToLower(strings.TrimSpace(text))
	objective = strings.ToLower(strings.TrimSpace(objective))

	if text == "" || objective == "" {
		return false
	}

	for _, term := range recoveryObjectiveSearchWords(objective, 32) {
		term = singularObjectiveWord(strings.ToLower(strings.TrimSpace(term)))
		if len(term) < 4 {
			continue
		}

		if strings.Contains(text, term) {
			return true
		}
	}

	return false
}

func lowercaseFirstSentence(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}

	first := value[0]
	if first >= 'A' && first <= 'Z' {
		return string(first+'a'-'A') + value[1:]
	}

	return value
}

func mentionedEvidencePathsFromToolResults(results []workspace.ToolResult, max int) []string {
	return mentionedEvidencePathsFromToolResultsForObjective(results, "", max)
}

func mentionedEvidencePathsFromToolResultsForObjective(results []workspace.ToolResult, objective string, max int) []string {
	var paths []string

	for _, result := range results {
		if !acceptedMentionedEvidenceResultForObjective(result, objective) {
			continue
		}

		paths = mergeRunnerStateStrings(paths, pathparse.MentionedWorkspacePaths(result.Summary))
		paths = mergeRunnerStateStrings(paths, pathparse.MentionedWorkspacePaths(result.Output))
		paths = mergeRunnerStateStrings(paths, pathparse.MentionedWorkspacePaths(result.Error))

		if len(paths) >= max {
			break
		}
	}

	return closurePromptEvidencePaths(paths, max)
}

func acceptedMentionedEvidenceResultForObjective(result workspace.ToolResult, objective string) bool {
	if strings.TrimSpace(result.Error) != "" {
		return false
	}

	if concreteSourceEvidenceResult(result) || readOnlyDiagnosticEvidenceResult(result) {
		return true
	}

	if strings.TrimSpace(result.Tool) != workspace.ToolNameTask || strings.TrimSpace(result.Output) == "" {
		return false
	}

	if packet, ok := readOnlyEvidencePacketFromResult(result); ok {
		return readOnlyEvidencePacketAnswerableForObjective(packet, objective)
	}

	if strings.TrimSpace(objective) != "" {
		return childPacketContainsGroundedAnswerEvidence(objective, result.Output) ||
			delegatedPacketAddressesObjective(objective, result.Output)
	}

	return delegatedTaskOutputHasFindingEvidence(result.Output)
}

// verificationContractSkipsGate reports whether the brief's
// VerificationContract value declares the post-mutation verification
// gate inapplicable. The gate is the right shape for atoms that
// produce code requiring tests/builds to confirm correctness; it's
// the wrong shape for atoms that only edit prose, prompts, or other
// text where the file write IS the deliverable.
//
// Generic mechanism — task-family declarations choose the value:
//   - "tests_passed_or_failed_or_skipped_with_blocker": gate fires
//     (current default for code-producing families).
//   - "not_required_for_text_mutations": gate skips (text-only
//     families like docs_edit, prompt_edit_security).
//   - any "not_required_for_*_lane" suffix: gate skips (read-only
//     lanes, by definition).
//
// The host project picks the right verification_contract per family
// in its workerruntime catalog; the runner enforces consistently.
// Adding a new "no verification needed" task family is now a one-line
// catalog change instead of a runner patch.
func verificationContractSkipsGate(contract string) bool {
	return workerruntime.VerificationContractSkipsGate(contract)
}

func readOnlyExplanationGateApplies(brief workerruntime.WorkerRuntimeBrief) bool {
	contract := strings.TrimSpace(brief.Contract)
	readOnlyPermission := workerruntime.PermissionProfileIsReadOnlyWorkspace(brief.PermissionProfile)
	family := strings.TrimSpace(brief.TaskFamily)

	if workerruntime.TaskFamilyIsPlanThenExecuteReview(family) {
		return false
	}

	switch contract {
	case string(workerruntime.ContractArchitecture):
		return readOnlyPermission || workerruntime.TaskFamilyIsArchitecture(family)
	case string(workerruntime.ContractReviewFindings):
		return readOnlyPermission ||
			family == "review" ||
			family == string(workerruntime.TaskFamilySecurityReview) ||
			family == string(workerruntime.TaskFamilyPerformanceReview)
	}

	if !readOnlyPermission {
		return false
	}

	switch family {
	case string(workerruntime.TaskFamilyArchitecture), "review":
		return true
	default:
		return false
	}
}

// retryAnswerHedgeRejectionReason checks whether the retry answer from
// ensureNonEmptyAnswer is a hedge or meta-response about evidence sufficiency
// rather than a substantive answer. This catches cases where the model, after
// being forced to retry an empty response, produces text that describes why it
// cannot answer instead of actually answering.
func retryAnswerHedgeRejectionReason(reply string) string {
	trimmed := strings.TrimSpace(reply)
	if trimmed == "" || len(trimmed) > 500 {
		return ""
	}

	lower := strings.ToLower(trimmed)
	for _, phrase := range []string{
		"does not retain enough",
		"not retain enough",
		"cannot provide",
		"unable to provide",
		"retained source context",
		"not sufficient",
		"insufficient evidence",
		"does not contain enough",
	} {
		if strings.Contains(lower, phrase) {
			return "the answer is a hedge about evidence sufficiency rather than a substantive response to the user's question"
		}
	}

	return ""
}

func readOnlyExplanationMetaRejectionReason(reply string) string {
	trimmed := strings.TrimSpace(reply)
	if trimmed == "" {
		return ""
	}

	lower := strings.ToLower(trimmed)
	for _, phrase := range []string{
		"i have sufficient evidence",
		"the strongest implementation evidence is in",
		"the inspected implementation evidence is enough",
		"the retained source context identifies",
		"the gathered evidence points to these implementation surfaces",
		"enough for a bounded answer",
		"implementation surface",
	} {
		if strings.Contains(lower, phrase) {
			return "the answer describes evidence sufficiency or implementation surface instead of explaining the concrete behavior from the inspected code. State the mechanism or findings directly."
		}
	}

	if len(pathparse.MentionedWorkspacePaths(trimmed)) > 0 && !readOnlyExplanationHasMechanismVerb(lower) {
		return "the answer mostly inventories paths or scope and does not explain what the inspected code does. State the concrete behavior those files establish."
	}

	return ""
}

func readOnlyExplanationHasMechanismVerb(lower string) bool {
	for _, phrase := range []string{
		" uses ", " use ", " used ", " delegates ", " delegate ", " delegated ",
		" creates ", " create ", " created ", " calls ", " call ", " called ",
		" contains ", " contain ", " contained ",
		" invokes ", " invoke ", " invoked ", " routes ", " route ", " routed ",
		" runs ", " run ", " ran ", " returns ", " return ", " returned ",
		" passes ", " pass ", " passed ", " enforces ", " enforce ", " enforced ",
		" computes ", " compute ", " computed ", " evaluates ", " evaluate ", " evaluated ",
		" determines ", " determine ", " determined ", " derives ", " derive ", " derived ",
		" spawns ", " spawn ", " spawned ", " builds ", " build ", " built ",
		" wires ", " wire ", " wired ", " folds ", " fold ", " folded ",
		" hands ", " hand ", " handed ", " launches ", " launch ", " launched ",
		" finds ", " find ", " found ", " locates ", " locate ", " located ",
		" identifies ", " identify ", " identified ",
		" sanitizes ", " sanitize ", " sanitized ", " redacts ", " redact ", " redacted ",
		" masks ", " mask ", " masked ", " replaces ", " replace ", " replaced ", " replacing ",
		" filters ", " filter ", " filtered ", " scrubs ", " scrub ", " scrubbed ",
		" summarizes ", " summarize ", " summarized ",
	} {
		if strings.Contains(" "+lower+" ", phrase) {
			return true
		}
	}

	return false
}

func readOnlyExplanationObjectiveRejectionReason(reply string, brief workerruntime.WorkerRuntimeBrief) string {
	brief = workerruntime.NormalizeRuntimeBrief(brief)

	objective := strings.TrimSpace(brief.Objective)

	if objective == "" || !readOnlyExplanationGateApplies(brief) {
		return ""
	}

	body := substantiveAnswerWithoutEchoedObjective(reply, objective)
	if strings.TrimSpace(body) == "" {
		return "the answer repeats the question but does not provide a substantive explanation from the inspected evidence."
	}

	terms := readOnlySearchHitObjectiveTerms(objective)
	if len(terms) == 0 {
		return ""
	}

	objectiveText := answerTextWithoutPathCitations(body)
	lower := strings.ToLower(answerProseForObjectiveMatching(body))
	mechanismText := strings.ToLower(answerProseForObjectiveMatching(objectiveText))

	if readOnlyObjectiveAnswerEvidenceSupported(objectiveText, objective) {
		return ""
	}

	matches := 0

	for _, term := range terms {
		if strings.Contains(lower, term) {
			matches++
		}
	}

	if matches >= 2 {
		return ""
	}

	if sourceBodyHasStrongObjectiveToken(objectiveText, objective) && readOnlyExplanationHasMechanismVerb(mechanismText) {
		return ""
	}

	for _, term := range longestObjectiveTerms(terms, 2) {
		if strings.Contains(lower, term) && readOnlyExplanationHasMechanismVerb(mechanismText) {
			return ""
		}
	}

	return "the answer is grounded in inspected evidence, but the substantive answer body does not address the user's objective terms. Do not rely on an echoed question or path citation as the answer; state the concrete behavior or finding tied to the objective."
}

func answerTextWithoutPathCitations(answer string) string {
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return ""
	}

	var b strings.Builder

	for i := 0; i < len(answer); {
		if answer[i] != '`' {
			b.WriteByte(answer[i])

			i++

			continue
		}

		end := strings.IndexByte(answer[i+1:], '`')
		if end < 0 {
			b.WriteByte(' ')

			i++

			continue
		}

		span := answer[i+1 : i+1+end]
		if !looksLikePathCitationToken(span) {
			b.WriteByte(' ')
			b.WriteString(span)
			b.WriteByte(' ')
		} else {
			b.WriteByte(' ')
		}

		i += end + 2
	}

	words := strings.Fields(b.String())
	if len(words) == 0 {
		return ""
	}

	kept := make([]string, 0, len(words))
	for _, word := range words {
		trimmed := strings.Trim(word, ".,:;!?()[]{}\"'")
		if looksLikePathCitationToken(trimmed) {
			continue
		}

		kept = append(kept, trimmed)
	}

	return strings.Join(kept, " ")
}

func looksLikePathCitationToken(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}

	value = strings.Trim(value, ".,:;!?()[]{}\"'")

	return strings.Contains(value, "/") ||
		strings.HasSuffix(value, ".go") ||
		strings.Contains(value, ".go:") ||
		strings.Contains(value, "\\")
}

func answerProseForObjectiveMatching(answer string) string {
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return ""
	}

	var b strings.Builder

	inCodeSpan := false

	for i := 0; i < len(answer); i++ {
		ch := answer[i]
		if ch == '`' {
			inCodeSpan = !inCodeSpan

			b.WriteByte(' ')

			continue
		}

		if inCodeSpan {
			continue
		}

		b.WriteByte(ch)
	}

	words := strings.Fields(b.String())
	if len(words) == 0 {
		return ""
	}

	kept := make([]string, 0, len(words))
	for _, word := range words {
		trimmed := strings.Trim(word, ".,:;!?()[]{}\"'")
		if looksLikePathCitationToken(trimmed) {
			continue
		}

		kept = append(kept, trimmed)
	}

	return strings.Join(kept, " ")
}

func substantiveAnswerWithoutEchoedObjective(answer string, objective string) string {
	body := strings.TrimSpace(answer)
	if body == "" {
		return ""
	}

	lowerBody := strings.ToLower(body)
	if strings.HasPrefix(lowerBody, "for `") {
		if end := strings.Index(body[5:], "`,"); end >= 0 {
			body = strings.TrimSpace(body[5+end+2:])
			body = strings.TrimLeft(body, " ,:")
		}
	}

	lines := strings.Split(body, "\n")
	if len(lines) > 0 && strings.HasPrefix(strings.ToLower(strings.TrimSpace(lines[0])), "question:") {
		body = strings.TrimSpace(strings.Join(lines[1:], "\n"))
	}

	for _, variant := range []string{
		strings.TrimSpace(objective),
		strings.TrimSpace(delegatedDisplayQuestion(objective)),
	} {
		if variant == "" {
			continue
		}

		body = strings.ReplaceAll(body, variant, "")
		body = strings.ReplaceAll(body, strings.ToLower(variant), "")
	}

	return strings.TrimSpace(body)
}

func architectureUnsupportedExactDetail(reply string, results []workspace.ToolResult) []string {
	exactSymbols := exactEvidenceSymbolSet(results)
	if len(exactSymbols) == 0 {
		return nil
	}

	mentioned := explicitAnswerCodeSymbols(reply)
	if len(mentioned) == 0 {
		return nil
	}

	var unsupported []string

	for _, symbol := range mentioned {
		key := normalizedAnswerSymbol(symbol)
		if key == "" {
			continue
		}

		if architectureNonSourceSymbolTerm(key) {
			continue
		}

		if _, ok := exactSymbols[key]; ok {
			continue
		}

		if answerSymbolHasExplicitUncertainty(reply, symbol) {
			continue
		}

		unsupported = util.AppendUnique(unsupported, symbol)
	}

	return unsupported
}

func exactEvidenceSymbolSet(results []workspace.ToolResult) map[string]struct{} {
	symbols := make(map[string]struct{})

	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" || strings.TrimSpace(result.Output) == "" {
			continue
		}

		if candidateAnchorOnlyResult(result) {
			continue
		}

		if concreteSourceEvidenceResult(result) {
			addSourceBodyEvidenceSymbols(symbols, result.Output)
		}

		addOutlineEvidenceSymbols(symbols, result)

		if !presentableExactProbeResult(result) {
			continue
		}

		_, symbol := extractExactProbeAnchor(result)
		key := normalizedAnswerSymbol(symbol)

		if key != "" {
			symbols[key] = struct{}{}
		}

		for _, call := range exactProbeCallNames(symbol, result.Output, 8) {
			key := normalizedAnswerSymbol(call)
			if key != "" {
				symbols[key] = struct{}{}
			}
		}
	}

	return symbols
}

func architectureNonSourceSymbolTerm(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case workspace.ToolNameCodeNavigate,
		workspace.ToolNameContextImpact,
		workspace.ToolNameContextIndex,
		"context_merge",
		workspace.ToolNameContextPack,
		workspace.ToolNameContextQuery,
		workspace.ToolNameContextStatus,
		"context_trace",
		workspace.ToolNameContextTracePack,
		workspace.ToolNameContextZoom,
		workspace.ToolNameCtagsSymbols,
		workspace.ToolNameDependencyGraph,
		workspace.ToolNameDiscoveryQuery,
		"discovery_state",
		workspace.ToolNameFileStats,
		workspace.ToolNameHarnessAudit,
		workspace.ToolNameHistoryRange,
		workspace.ToolNameHistorySymbol,
		workspace.ToolNameJoernScan,
		"progressive_refinement",
		workspace.ToolNameReadFile,
		workspace.ToolNameReadOnlyCapability,
		workspace.ToolNameSearchContent,
		workspace.ToolNameSearchFiles,
		workspace.ToolNameSemgrepScan,
		workspace.ToolNameSourceSanity,
		workspace.ToolNameStructuralSearch:
		return true
	default:
		return false
	}
}

func addOutlineEvidenceSymbols(symbols map[string]struct{}, result workspace.ToolResult) {
	if strings.TrimSpace(result.Error) != "" {
		return
	}

	if strings.TrimSpace(result.Tool) != workspace.ToolNameCodeNavigate && !strings.HasSuffix(strings.TrimSpace(result.Tool), "_file_outline") {
		return
	}

	text := result.Summary + "\n" + result.Output
	if !strings.Contains(strings.ToLower(text), "file_outline") && !strings.Contains(strings.ToLower(text), "outlined ") {
		return
	}

	for _, line := range strings.Split(result.Output, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "- ") || !strings.Contains(line, " lines ") {
			continue
		}

		head := strings.TrimSpace(strings.TrimPrefix(line, "- "))
		if before, _, ok := strings.Cut(head, " lines "); ok {
			head = before
		}

		for _, part := range strings.Split(head, ",") {
			key := normalizedAnswerSymbol(part)
			if key != "" {
				symbols[key] = struct{}{}
			}
		}
	}
}

func addSourceBodyEvidenceSymbols(symbols map[string]struct{}, output string) {
	for _, line := range exactProbeSourceLines(output) {
		for _, raw := range sourceBodyEvidenceSymbolCandidates(line) {
			key := normalizedAnswerSymbol(raw)
			if key != "" {
				symbols[key] = struct{}{}
			}
		}
	}
}

func sourceBodyEvidenceSymbolCandidates(line string) []string {
	seen := map[string]struct{}{}

	var out []string

	add := func(raw string) {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			return
		}

		key := normalizedAnswerSymbol(raw)
		if key == "" {
			return
		}

		if _, ok := seen[key]; ok {
			return
		}

		seen[key] = struct{}{}

		out = append(out, raw)
	}

	if name := sourceBodyFunctionName(stripSourceEvidenceLinePrefix(strings.TrimSpace(line))); name != "" {
		add(name)
	}

	for _, match := range backtickedTokenPattern.FindAllStringSubmatch(line, -1) {
		if len(match) > 1 {
			add(match[1])
		}
	}

	for _, match := range qualifiedIdentifierPattern.FindAllStringSubmatch(line, -1) {
		if len(match) > 1 {
			add(match[1])
		}
	}

	for _, match := range callLikeIdentifierPattern.FindAllStringSubmatch(line, -1) {
		if len(match) > 1 {
			add(match[1])
		}
	}

	for _, token := range codeShapedTokenPattern.FindAllString(line, -1) {
		add(token)
	}

	for _, token := range codeShapedSnakeCasePattern.FindAllString(line, -1) {
		add(token)
	}

	for _, token := range strings.FieldsFunc(line, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_'
	}) {
		add(token)
	}

	return out
}

func explicitAnswerCodeSymbols(reply string) []string {
	symbols := make([]string, 0, 6)
	add := func(raw string) {
		key := normalizedAnswerSymbol(raw)
		if key == "" {
			return
		}

		symbols = util.AppendUnique(symbols, raw)
	}

	for _, match := range backtickedTokenPattern.FindAllStringSubmatch(reply, -1) {
		if len(match) > 1 {
			add(match[1])
		}
	}

	for _, match := range qualifiedIdentifierPattern.FindAllStringSubmatch(reply, -1) {
		if len(match) > 1 {
			add(match[1])
		}
	}

	for _, match := range callLikeIdentifierPattern.FindAllStringSubmatch(reply, -1) {
		if len(match) > 1 {
			add(match[1])
		}
	}

	return symbols
}

func normalizedAnswerSymbol(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.Trim(raw, "`'\"()[]{}<>,.:;!?")

	qualified := raw

	if dot := strings.LastIndex(raw, "."); dot >= 0 {
		raw = strings.TrimSpace(raw[dot+1:])
	}

	candidate, ok := delegatedReadOnlyLiteralSymbolCandidate(raw)
	if !ok {
		if qualifiedCandidate, qualifiedOK := delegatedReadOnlyQualifiedSymbolCandidate(qualified); qualifiedOK {
			return strings.ToLower(qualifiedCandidate)
		}

		return ""
	}

	return strings.ToLower(candidate)
}

func delegatedReadOnlyQualifiedSymbolCandidate(raw string) (string, bool) {
	value := strings.TrimSpace(raw)

	value = strings.Trim(value, "`'\"()[]{}<>,.:;!?")

	if value == "" || !strings.Contains(value, ".") || pathparse.LooksLikeWorkspacePathToken(strings.ToLower(value)) {
		return "", false
	}

	parts := strings.Split(value, ".")
	if len(parts) != 2 {
		return "", false
	}

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if len(part) < 2 {
			return "", false
		}

		hasLetter := false

		for _, r := range part {
			switch {
			case unicode.IsLetter(r):
				hasLetter = true
			case unicode.IsDigit(r), r == '_':
			default:
				return "", false
			}
		}

		if !hasLetter || (!delegatedReadOnlyLooksCodeShapedSymbol(part) && !delegatedReadOnlyLooksExportedTypeName(part)) {
			return "", false
		}
	}

	return value, true
}

func delegatedReadOnlyLooksExportedTypeName(value string) bool {
	if len(value) < 2 {
		return false
	}

	first := rune(value[0])
	if !unicode.IsUpper(first) {
		return false
	}

	hasLower := false

	for _, r := range value[1:] {
		if unicode.IsLower(r) {
			hasLower = true
			continue
		}

		if unicode.IsDigit(r) {
			continue
		}

		return false
	}

	return hasLower
}

func answerSymbolHasExplicitUncertainty(reply string, symbol string) bool {
	window := answerSymbolEvidenceWindow(reply, symbol, 180)
	if window == "" {
		return false
	}

	return answerHasExplicitUncertainty(window)
}

func answerSymbolEvidenceWindow(reply string, symbol string, radius int) string {
	reply = strings.TrimSpace(reply)
	symbol = strings.TrimSpace(symbol)

	if reply == "" || symbol == "" {
		return ""
	}

	candidates := []string{symbol}
	if dot := strings.LastIndex(symbol, "."); dot >= 0 && dot+1 < len(symbol) {
		candidates = append(candidates, strings.TrimSpace(symbol[dot+1:]))
	}

	lowerReply := strings.ToLower(reply)
	idx := -1
	length := 0

	for _, candidate := range candidates {
		candidate = strings.Trim(candidate, "`'\"()[]{}<>,.:;!?")
		if candidate == "" {
			continue
		}

		found := strings.Index(lowerReply, strings.ToLower(candidate))
		if found >= 0 {
			idx = found
			length = len(candidate)

			break
		}
	}

	if idx < 0 {
		return ""
	}

	if sentence := answerSentenceContainingIndex(reply, idx, length); sentence != "" {
		return sentence
	}

	if radius <= 0 {
		radius = 120
	}

	start := idx - radius
	if start < 0 {
		start = 0
	}

	end := idx + length + radius
	if end > len(reply) {
		end = len(reply)
	}

	return reply[start:end]
}

func answerSentenceContainingIndex(text string, idx int, length int) string {
	if idx < 0 || idx >= len(text) {
		return ""
	}

	start := 0

	for i := idx - 1; i >= 0; i-- {
		switch text[i] {
		case '.', '!', '?', '\n':
			start = i + 1
			i = -1
		}
	}

	end := len(text)
	for i := idx + length; i < len(text); i++ {
		switch text[i] {
		case '.', '!', '?', '\n':
			end = i + 1
			i = len(text)
		}
	}

	return strings.TrimSpace(text[start:end])
}

func answerHasExplicitUncertainty(reply string) bool {
	lower := strings.ToLower(strings.TrimSpace(reply))

	return containsAny(lower,
		"not directly inspected",
		"not directly read",
		"not confirmed",
		"not verified",
		"did not inspect",
		"didn't inspect",
		"uncertain",
		"may ",
		"might ",
		"appears ",
		"suggests ",
		"likely ",
		"seems ",
	)
}

func runnerBriefRequiresMutation(brief workerruntime.WorkerRuntimeBrief) bool {
	family := strings.ToLower(strings.TrimSpace(brief.TaskFamily))
	contract := strings.ToLower(strings.TrimSpace(brief.Contract))
	permission := strings.ToLower(strings.TrimSpace(brief.PermissionProfile))

	return strings.Contains(family, "implement") ||
		strings.Contains(family, "edit") ||
		strings.Contains(contract, "implementation") ||
		strings.Contains(permission, "write")
}

func writeReadyPacketReadContractActive(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if !runnerBriefRequiresMutation(brief) || len(brief.RequiredEditPaths) == 0 {
		return false
	}

	if len(changedPathsFromToolResults(results)) > 0 {
		return false
	}

	return true
}

// briefHasUsableEditAnchors reports whether the brief carries inline
// edit anchors the model can actually act on without first reading
// source. Required edit paths alone don't qualify — they're a write
// allowlist, not evidence of where to put the change. Without anchors,
// the harness shouldn't claim packet-driven write-readiness, and the
// pre-write read guidance should match an abstract brief.
func briefHasUsableEditAnchors(brief workerruntime.WorkerRuntimeBrief) bool {
	if len(brief.RequiredEditSnippets) > 0 {
		return true
	}

	return exactAnchorImplementationActive(brief)
}

func runtimeBriefDefinesWriteReadyReadContract(brief workerruntime.WorkerRuntimeBrief) bool {
	if !briefHasUsableEditAnchors(brief) {
		return false
	}

	if slices.Contains(brief.Constraints, "implementation_packet_write_ready") ||
		slices.Contains(brief.Constraints, "required_edit_paths_define_write_scope") {

		return true
	}

	if brief.Readiness != nil && brief.Readiness.ReadyToWrite {
		if slices.Contains(brief.Readiness.ClosedClaims, "required_edit_paths_define_write_scope") ||
			strings.Contains(strings.ToLower(brief.Readiness.FurtherReadsRequire), "named_gap") {

			return true
		}
	}

	for _, policy := range brief.SearchPolicy {
		policy = strings.ToLower(strings.TrimSpace(policy))
		if strings.Contains(policy, "required_edit_paths") &&
			(strings.Contains(policy, "readiness-to-write") ||
				strings.Contains(policy, "readiness to write") ||
				strings.Contains(policy, "named edit-safety") ||
				strings.Contains(policy, "named_gap")) {

			return true
		}
	}

	return false
}

func implementationPreWriteToolSurfaceActive(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	return exactAnchorPreWriteSurfaceActive(brief, results)
}

func implementationScopedEditSurfaceActive(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if planThenExecutePlannerBrief(brief) {
		return false
	}

	if !runnerBriefRequiresMutation(brief) || len(brief.RequiredEditPaths) == 0 {
		return false
	}

	if implementationPreWriteToolSurfaceActive(brief, results) {
		return false
	}

	changed := changedPathsFromToolResults(results)

	return len(missingRequiredEditPaths(brief.RequiredEditPaths, changed)) != 0
}
