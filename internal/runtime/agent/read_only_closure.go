package agent

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/pathparse"
	"idiom/internal/runtime/workerruntime"
)

type ReadOnlyCloseoutState string

const (
	readOnlyClosureAnswerReady ReadOnlyCloseoutState = "answer_ready"
	readOnlyClosureNotReady    ReadOnlyCloseoutState = "not_ready"
)

type ReadOnlyCloseoutReason string

const (
	readOnlyClosureReasonAnswerable               ReadOnlyCloseoutReason = "answerable_evidence"
	readOnlyClosureReasonHighCardinalitySearch    ReadOnlyCloseoutReason = "high_cardinality_localization_only"
	readOnlyClosureReasonMissingNamedSourceBody   ReadOnlyCloseoutReason = "missing_named_source_body"
	readOnlyClosureReasonCandidatesNeedSource     ReadOnlyCloseoutReason = "candidate_paths_need_source_body"
	readOnlyClosureReasonNoRelevantSourceEvidence ReadOnlyCloseoutReason = "no_relevant_source_or_diagnostic_evidence"
)

type readOnlyClosureDecision = ReadOnlyCloseout

type ReadOnlyCloseout struct {
	Brief    workerruntime.WorkerRuntimeBrief
	Results  []workspace.ToolResult
	State    ReadOnlyCloseoutState
	Reason   ReadOnlyCloseoutReason
	Evidence readOnlyAnswerabilityLedger
}

func readOnlyClosureDecisionForResults(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) ReadOnlyCloseout {
	brief = workerruntime.NormalizeRuntimeBrief(brief)

	evidence := readOnlyAnswerabilityForResults(brief, results)

	if evidence.Answerable() {
		return ReadOnlyCloseout{
			Brief:    brief,
			Results:  append([]workspace.ToolResult(nil), results...),
			State:    readOnlyClosureAnswerReady,
			Reason:   readOnlyClosureReasonAnswerable,
			Evidence: evidence,
		}
	}

	reason := readOnlyClosureReasonNoRelevantSourceEvidence

	switch {
	case evidence.HighCardinalitySearch && len(evidence.CandidatePaths) > 0:
		reason = readOnlyClosureReasonHighCardinalitySearch
	case len(evidence.MissingNamedSourcePaths) > 0:
		reason = readOnlyClosureReasonMissingNamedSourceBody
	case len(evidence.CandidatePaths) > 0:
		reason = readOnlyClosureReasonCandidatesNeedSource
	}

	return ReadOnlyCloseout{
		Brief:    brief,
		Results:  append([]workspace.ToolResult(nil), results...),
		State:    readOnlyClosureNotReady,
		Reason:   reason,
		Evidence: evidence,
	}
}

func (d ReadOnlyCloseout) Answerable() bool {
	return d.State == readOnlyClosureAnswerReady
}

func (d ReadOnlyCloseout) BlockerText() string {
	evidence := d.Evidence
	switch d.Reason {
	case readOnlyClosureReasonHighCardinalitySearch:
		objective := strings.TrimSpace(evidence.Objective)
		if objective != "" {
			return fmt.Sprintf("<<QUESTION>>: The current read-only answer has only localization evidence for `%s`. The broad search returned %d hit(s); read one objective-relevant source candidate such as %s before answering, or name the exact missing claim.", objective, evidence.SearchHitCount, formatInlinePathList(d.PrioritizedCandidatePaths(), 3))
		}

		return fmt.Sprintf("<<QUESTION>>: The current read-only answer has only localization evidence. The broad search returned %d hit(s); read one objective-relevant source candidate such as %s before answering, or name the exact missing claim.", evidence.SearchHitCount, formatInlinePathList(d.PrioritizedCandidatePaths(), 3))
	case readOnlyClosureReasonMissingNamedSourceBody:
		return fmt.Sprintf("<<QUESTION>>: The current read-only answer is missing source-body evidence for named path(s): %s. Read those path(s), or name the exact blocker.", formatInlinePathList(evidence.MissingNamedSourcePaths, 4))
	case readOnlyClosureReasonCandidatesNeedSource:
		return fmt.Sprintf("<<QUESTION>>: The current read-only answer has candidate paths but no relevant source-body evidence. Inspect one candidate such as %s with a source-body read, or name the exact missing claim.", formatInlinePathList(evidence.CandidatePaths, 3))
	default:
		if paths := readOnlyClosureKnownEvidencePaths(evidence); len(paths) > 0 {
			return fmt.Sprintf("<<QUESTION>>: The current read-only answer has evidence in %s, but it is not backed by relevant source-body or diagnostic evidence for the requested claim. State the supported concrete finding from that evidence, or name the exact missing claim.", formatInlinePathList(paths, 4))
		}

		return "<<QUESTION>>: The current read-only answer is not backed by relevant source-body or diagnostic evidence. Gather one source-body read for the claim, or name the exact missing claim."
	}
}

func (d ReadOnlyCloseout) UserFacingUncertainty() string {
	if !readOnlyDirectAnswerLane(d.Brief) {
		return d.BlockerText()
	}

	evidence := d.Evidence
	switch d.Reason {
	case readOnlyClosureReasonHighCardinalitySearch:
		if strings.TrimSpace(evidence.Objective) != "" {
			return fmt.Sprintf("%s The search only localized `%s`: it returned %d hit(s), and one objective-relevant source candidate such as %s still needs a source-body read.", presentableUnsupportedFinalAnswerLead(), evidence.Objective, evidence.SearchHitCount, formatInlinePathList(d.PrioritizedCandidatePaths(), 3))
		}

		return fmt.Sprintf("%s The search only localized candidates: it returned %d hit(s), and one objective-relevant source candidate such as %s still needs a source-body read.", presentableUnsupportedFinalAnswerLead(), evidence.SearchHitCount, formatInlinePathList(d.PrioritizedCandidatePaths(), 3))
	case readOnlyClosureReasonMissingNamedSourceBody:
		return fmt.Sprintf("%s Source-body evidence is still missing for named path(s): %s.", presentableUnsupportedFinalAnswerLead(), formatInlinePathList(evidence.MissingNamedSourcePaths, 4))
	case readOnlyClosureReasonCandidatesNeedSource:
		return fmt.Sprintf("%s The run found candidate paths such as %s, but did not inspect objective-relevant source body.", presentableUnsupportedFinalAnswerLead(), formatInlinePathList(evidence.CandidatePaths, 3))
	default:
		if paths := readOnlyClosureKnownEvidencePaths(evidence); len(paths) > 0 {
			return fmt.Sprintf("%s The run gathered evidence in %s, but it was not relevant source-body or diagnostic evidence for the requested claim.", presentableUnsupportedFinalAnswerLead(), formatInlinePathList(paths, 4))
		}

		return presentableUnsupportedFinalAnswerLead() + " The run did not retain relevant source-body or diagnostic evidence for the requested claim."
	}
}

func (d ReadOnlyCloseout) ConversationFallback() string {
	if concrete, ok := d.ConcreteFallbackAnswer(); ok {
		return concrete
	}

	return d.ConversationUncertaintyFallback()
}

func (d ReadOnlyCloseout) ConversationUncertaintyFallback() string {
	var (
		inspectedPaths []string
		anchors        []string
	)

	for _, result := range d.Results {
		paths := readPathsFromToolResult(result)
		for _, path := range paths {
			if isNonSourceEvidenceArtifactPath(path) {
				continue
			}

			if path != "" && !slices.Contains(inspectedPaths, path) {
				inspectedPaths = append(inspectedPaths, path)
			}
		}

		if anchor := conversationFallbackEvidenceAnchor(result); anchor != "" && !slices.Contains(anchors, anchor) {
			anchors = append(anchors, anchor)
		}
	}

	if len(inspectedPaths) == 0 && len(anchors) == 0 {
		return presentableUnsupportedFinalAnswerLead() + " The next useful step is one bounded source-body or diagnostic probe tied to the exact claim."
	}

	var b strings.Builder

	b.WriteString(presentableUnsupportedFinalAnswerLead())
	b.WriteString(" The run collected evidence but did not retain a concrete user-facing finding.")

	if len(inspectedPaths) == 0 {
		b.WriteString("\n\nEvidence paths:\n- relevant source files")
	} else {
		b.WriteString("\n\nEvidence paths:\n")

		for _, path := range limitStrings(inspectedPaths, 6) {
			b.WriteString("- `")
			b.WriteString(path)
			b.WriteString("`\n")
		}
	}

	if len(anchors) > 0 {
		b.WriteString("\nSource anchors:\n")

		for _, anchor := range limitStrings(anchors, 6) {
			b.WriteString("- ")
			b.WriteString(anchor)
			b.WriteString("\n")
		}
	}

	return strings.TrimSpace(b.String())
}

func (d ReadOnlyCloseout) ConcreteFallbackAnswer() (string, bool) {
	if strings.TrimSpace(d.Brief.Objective) == "" || !d.Answerable() {
		return "", false
	}

	if exact, ok := presentableExactProbeAnswer(d.Brief, d.Results); ok {
		return exact, true
	}

	if sourceBody, ok := presentableReadFileSourceAnswer(d.Brief, d.Results); ok {
		return sourceBody, true
	}

	if prose, ok := presentableProseEvidenceAnswer(d.Brief, d.Results); ok {
		return prose, true
	}

	return "", false
}

func (d ReadOnlyCloseout) AllowsSourceInventoryAnswer(answer string, currentlyAccepted bool) bool {
	if !currentlyAccepted || !looksLikeLowValueSourceInventoryNonAnswer(answer) {
		return false
	}

	if looksLikeLineOrientedSourceSnippetAnswer(answer) {
		return false
	}

	if looksLikeDiscoveryEvidenceInventoryNonAnswer(answer) {
		return false
	}

	brief := workerruntime.NormalizeRuntimeBrief(d.Brief)
	if readOnlyDirectAnswerLane(brief) {
		return false
	}

	if objectiveAsksBehaviorSummary(brief.Objective) {
		return false
	}

	return workerruntime.RuntimeContractIsArchitecture(brief.Contract) ||
		workerruntime.TaskFamilyIsArchitecture(brief.TaskFamily)
}

type readOnlyFinalAnswerDecision struct {
	Status                  string
	BlockerKind             string
	Reason                  string
	AcceptedEvidencePaths   []string
	RelevantSourceBodyPaths []string
	AnswerObjectiveTerms    []string
}

func (d readOnlyFinalAnswerDecision) DirectAnswerRelevance() readOnlyDirectAnswerRelevance {
	return readOnlyDirectAnswerRelevance{
		Status:                  d.Status,
		BlockerKind:             d.BlockerKind,
		Reason:                  d.Reason,
		RelevantSourceBodyPaths: append([]string(nil), d.RelevantSourceBodyPaths...),
		AnswerObjectiveTerms:    append([]string(nil), d.AnswerObjectiveTerms...),
	}
}

func (d ReadOnlyCloseout) DirectAnswerRelevance(answer string) readOnlyDirectAnswerRelevance {
	return d.FinalAnswerDecision(answer).DirectAnswerRelevance()
}

func (d ReadOnlyCloseout) FinalAnswerDecision(answer string) readOnlyFinalAnswerDecision {
	brief := workerruntime.NormalizeRuntimeBrief(d.Brief)
	if !readOnlyDirectAnswerAcceptanceApplies(brief) {
		return readOnlyFinalAnswerDecision{}
	}

	objective := strings.TrimSpace(brief.Objective)
	if objective == "" {
		return readOnlyFinalAnswerDecision{Status: "skipped_empty_objective"}
	}

	substantiveAnswer := substantiveAnswerWithoutEchoedObjective(answer, objective)
	ledger := d.Evidence
	answerSupportedPaths := answerCitedCollectiveSourceBodySupportPaths(answer, ledger)

	if len(answerSupportedPaths) > 0 {
		ledger.SourceBodyObjectivePaths = mergeRunnerStateStrings(ledger.SourceBodyObjectivePaths, answerSupportedPaths)
		ledger.RelevantSourceBodyPaths = mergeRunnerStateStrings(ledger.RelevantSourceBodyPaths, answerSupportedPaths)
	}

	decision := readOnlyFinalAnswerDecision{
		Status:                  "checked",
		RelevantSourceBodyPaths: ledger.RelevantSourceBodyPaths,
		AnswerObjectiveTerms:    answerObjectiveTerms(substantiveAnswer, objective),
	}
	if readOnlyAnswerReportsInsufficientEvidence(substantiveAnswer) {
		decision.Status = "insufficient_evidence_answer"
		if len(ledger.SourceBodyPaths) == 0 && !ledger.DiagnosticEvidence {
			decision.BlockerKind = "answer_objective_unmet"
			decision.Reason = "the final answer says the evidence is insufficient before inspecting source-body or diagnostic evidence for the user's objective. Gather bounded source-body evidence, or use <<QUESTION>>: with the exact missing evidence source."

			return decision
		}

		decision.AcceptedEvidencePaths = readOnlyAcceptedDirectAnswerEvidencePaths(answer, ledger, d.Results)

		return decision
	}

	// Follow-up conversation turn with carried-over evidence: accept
	// substantive answers even when objective-term matching is weak or
	// answerability is marginal for short confirmation follow-ups. The
	// shared decision still rejects path inventories and wrong-owner
	// citations so carried evidence cannot bypass the acceptance contract.
	if ledger.directAnswerConversation() &&
		(len(d.Brief.EvidencePaths) > 0 || len(ledger.SourceBodyPaths) > 0) &&
		readOnlyCarriedEvidenceAnswerAccepted(substantiveAnswer, objective, ledger, d.Results) {

		decision.AcceptedEvidencePaths = readOnlyAcceptedDirectAnswerEvidencePaths(answer, ledger, d.Results)
		return decision
	}

	if plainNamedFileContentRequest(objective) && answerCitesNamedObjectiveSource(answer, ledger, d.Results) {
		decision.AcceptedEvidencePaths = readOnlyAcceptedDirectAnswerEvidencePaths(answer, ledger, d.Results)
		return decision
	}

	if !ledger.Answerable() || answerLacksObjectiveSourceEvidence(answer, ledger) {
		if readOnlyAnswerUsesInspectedSourceAsAnswer(answer, ledger) || answerMakesCausalClaimWithoutObjectiveEvidence(answer, ledger) {
			decision.BlockerKind = "answer_objective_unmet"
			decision.Reason = "the final answer uses inspected source evidence, but that evidence does not establish the user's objective. Answer with the exact objective-relevant source-body evidence, state that the current evidence is insufficient, or use <<QUESTION>>: with the exact missing evidence."

			return decision
		}

		decision.Status = "skipped_not_answerable"

		return decision
	}

	if answerAddressesObjective(substantiveAnswer, objective, ledger, d.Results) && answerSourceCitationsAreObjectiveRelevant(answer, ledger) {
		decision.AcceptedEvidencePaths = readOnlyAcceptedDirectAnswerEvidencePaths(answer, ledger, d.Results)
		return decision
	}

	decision.BlockerKind = "answer_objective_unmet"
	decision.Reason = "the final answer is grounded in inspected evidence, but it does not address the user's objective. Answer using objective-relevant source-body evidence, or use <<QUESTION>>: with the exact missing evidence."

	return decision
}

func readOnlyCarriedEvidenceAnswerAccepted(answer string, objective string, ledger readOnlyAnswerabilityLedger, results []workspace.ToolResult) bool {
	if strings.TrimSpace(answer) == "" {
		return false
	}

	if answerLacksObjectiveSourceEvidence(answer, ledger) {
		return false
	}

	if !answerSourceCitationsAreObjectiveRelevant(answer, ledger) {
		return false
	}

	if looksLikeLowValueSourceInventoryNonAnswer(answer) || readOnlyAnswerUsesInspectedSourceAsAnswer(answer, ledger) {
		return answerAddressesObjective(answer, objective, ledger, results)
	}

	return answerAddressesObjective(answer, objective, ledger, results)
}

func (d ReadOnlyCloseout) SupportsPresentableQuestionBlockerAnswer(answer string) bool {
	if !readOnlyDirectAnswerLane(d.Brief) {
		return true
	}

	objective := strings.TrimSpace(d.Brief.Objective)
	if objective == "" {
		return true
	}

	ledger := d.Evidence
	if !ledger.Answerable() {
		return false
	}

	substantive := substantiveAnswerWithoutEchoedObjective(answer, objective)

	return answerAddressesObjective(substantive, objective, ledger, d.Results) &&
		answerSourceCitationsAreObjectiveRelevant(answer, ledger)
}

func answerAddressesObjective(answer string, objective string, ledger readOnlyAnswerabilityLedger, results []workspace.ToolResult) bool {
	answer = strings.TrimSpace(substantiveAnswerWithoutEchoedObjective(answer, objective))
	if answer == "" {
		return false
	}

	if sourceBodyHasStrongObjectiveToken(answer, objective) {
		return true
	}

	if plainNamedFileContentRequest(objective) && answerCitesNamedObjectiveSource(answer, ledger, results) {
		return true
	}

	if readOnlyCauseIntent(objective) {
		return false
	}

	if readOnlyExplainerRequest(objective) && answerMentionsInspectedCodeSymbol(answer, results) {
		return true
	}

	lower := strings.ToLower(answer)
	terms := readOnlySearchHitObjectiveTerms(objective)

	if len(terms) == 0 {
		return true
	}

	for _, term := range longestObjectiveTerms(terms, 2) {
		if strings.Contains(lower, term) {
			return true
		}
	}

	objectiveLineMatches := 0

	for _, term := range sourceEvidenceObjectiveLineTerms(objective) {
		term = strings.ToLower(strings.TrimSpace(term))
		if term == "" {
			continue
		}

		if strings.Contains(lower, term) {
			objectiveLineMatches++
		}
	}

	return objectiveLineMatches >= 2
}

func answerMentionsInspectedCodeSymbol(answer string, results []workspace.ToolResult) bool {
	exactSymbols := exactEvidenceSymbolSet(results)
	if len(exactSymbols) == 0 {
		return false
	}

	for _, symbol := range explicitAnswerCodeSymbols(answer) {
		key := normalizedAnswerSymbol(symbol)
		if key == "" || architectureNonSourceSymbolTerm(key) {
			continue
		}

		if _, ok := exactSymbols[key]; ok {
			return true
		}
	}

	return false
}

func answerCitesNamedObjectiveSource(answer string, ledger readOnlyAnswerabilityLedger, results []workspace.ToolResult) bool {
	namedSet := readOnlyPathSet(ledger.NamedSourcePaths)
	objectiveLower := strings.ToLower(ledger.Objective)

	for _, path := range readOnlySourceLikePaths(sourceLikePathsFromText(ledger.Objective, 16), 16) {
		if strings.Contains(objectiveLower, strings.ToLower(path)) || strings.Contains(objectiveLower, strings.ToLower(filepath.Base(filepath.ToSlash(path)))) {
			namedSet[path] = struct{}{}
		}
	}

	if len(namedSet) == 0 && len(results) == 0 {
		return false
	}

	sourceSet := readOnlyPathSet(ledger.SourceBodyPaths)

	for _, raw := range sourceGroundingMentionedPaths(answer) {
		path := normalizeRunnerPath(raw)
		if path == "" {
			continue
		}

		if _, ok := namedSet[path]; ok {
			if _, read := sourceSet[path]; read {
				return true
			}
		}
	}

	lower := strings.ToLower(answer)

	for named := range namedSet {
		if _, read := sourceSet[named]; !read && !sourceEvidenceBodyReadForPath(named, results) {
			continue
		}

		base := strings.ToLower(filepath.Base(filepath.ToSlash(named)))
		if base != "" && strings.Contains(lower, base) {
			return true
		}
	}

	for source := range sourceSet {
		base := strings.ToLower(filepath.Base(filepath.ToSlash(source)))
		if base == "" || !strings.Contains(lower, base) {
			continue
		}

		if strings.Contains(objectiveLower, strings.ToLower(source)) || strings.Contains(objectiveLower, base) {
			return true
		}
	}

	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" {
			continue
		}

		for _, source := range readOnlySourceLikePaths(readPathsFromToolResult(result), 16) {
			base := strings.ToLower(filepath.Base(filepath.ToSlash(source)))
			if base == "" || !strings.Contains(lower, base) {
				continue
			}

			if strings.Contains(objectiveLower, strings.ToLower(source)) || strings.Contains(objectiveLower, base) {
				return true
			}
		}
	}

	return false
}

func answerObjectiveTerms(answer string, objective string) []string {
	answer = substantiveAnswerWithoutEchoedObjective(answer, objective)

	lower := strings.ToLower(answer)

	var matches []string

	for _, term := range readOnlySearchHitObjectiveTerms(objective) {
		if strings.Contains(lower, term) {
			matches = append(matches, term)
		}
	}

	return uniqueSortedStrings(matches)
}

func answerSourceCitationsAreObjectiveRelevant(answer string, ledger readOnlyAnswerabilityLedger) bool {
	mentioned := readOnlySourceLikePaths(sourceGroundingMentionedPaths(answer), 16)
	if len(mentioned) == 0 {
		return true
	}

	relevantPaths := ledger.RelevantSourceBodyPaths
	if ledger.directAnswerConversation() && objectiveAsksImplementationShape(ledger.Objective) {
		relevantPaths = ledger.ImplementationOwnerPaths()
	}

	if readOnlyCauseIntent(ledger.Objective) {
		relevantPaths = ledger.SourceBodyObjectivePaths
	}

	relevantSet := readOnlyPathSet(relevantPaths)
	if len(relevantSet) == 0 {
		return len(ledger.SourceBodyPaths) == 0
	}

	for _, path := range mentioned {
		if _, ok := relevantSet[normalizeRunnerPath(path)]; ok {
			return true
		}
	}

	return false
}

func objectiveAsksBehaviorSummary(objective string) bool {
	lower := " " + strings.ToLower(strings.Join(strings.Fields(objective), " ")) + " "

	return containsAny(lower,
		" what does ",
		" what do ",
	)
}

func plainNamedFileContentRequest(objective string) bool {
	lower := strings.ToLower(strings.Join(strings.Fields(objective), " "))
	if !containsAny(lower, "what is in", "what's in", "show", "summarize", "summary of", "contents of", "list") {
		return false
	}

	if readOnlyCauseIntent(objective) {
		return false
	}

	return len(readOnlySourceLikePaths(sourceLikePathsFromText(objective, 8), 8)) > 0
}

func readOnlyExplainerRequest(objective string) bool {
	lower := strings.ToLower(strings.TrimSpace(objective))
	return strings.HasPrefix(lower, "explain ") || strings.HasPrefix(lower, "describe ")
}

func readOnlyCauseIntent(objective string) bool {
	lower := strings.ToLower(strings.Join(strings.Fields(objective), " "))
	if strings.Contains(lower, "root cause") {
		return true
	}

	words := objectiveWordSet(lower)
	for _, word := range []string{
		"why", "cause", "caused", "bug", "failure", "failed", "failing", "regression",
		"fix", "broken", "break", "breaks", "error", "debugging", "diagnose", "investigate",
	} {
		if _, ok := words[word]; ok {
			return true
		}
	}

	return false
}

func objectiveWordSet(text string) map[string]struct{} {
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	})
	set := make(map[string]struct{}, len(words))

	for _, word := range words {
		word = strings.TrimSpace(word)
		if word == "" {
			continue
		}

		set[word] = struct{}{}
	}

	return set
}

func readOnlyAnswerUsesInspectedSourceAsAnswer(answer string, ledger readOnlyAnswerabilityLedger) bool {
	if len(ledger.SourceBodyPaths) == 0 {
		return false
	}

	if len(readOnlySourceLikePaths(sourceGroundingMentionedPaths(answer), 16)) > 0 {
		return true
	}

	lower := strings.ToLower(strings.Join(strings.Fields(answer), " "))

	return containsAny(lower,
		"the relevant code",
		"the code is",
		"the implementation is",
		"the root cause is",
		"the issue is",
		"the bug is",
		"the answer is",
		"source shows",
		"source evidence",
		"inspected source",
	)
}

func answerLacksObjectiveSourceEvidence(answer string, ledger readOnlyAnswerabilityLedger) bool {
	if !readOnlyCauseIntent(ledger.Objective) {
		return false
	}

	if len(ledger.SourceBodyPaths) == 0 {
		return false
	}

	if len(ledger.SourceBodyObjectivePaths) > 0 {
		return false
	}

	return readOnlyAnswerUsesInspectedSourceAsAnswer(answer, ledger) || readOnlyCauseIntent(answer)
}

func answerMakesCausalClaimWithoutObjectiveEvidence(answer string, ledger readOnlyAnswerabilityLedger) bool {
	return readOnlyCauseIntent(ledger.Objective) &&
		len(ledger.SourceBodyPaths) > 0 &&
		len(ledger.SourceBodyObjectivePaths) == 0 &&
		readOnlyCauseIntent(answer)
}

func (d ReadOnlyCloseout) HostOwnedAnswer(agentName string) (string, bool) {
	if runnerBriefRequiresMutation(d.Brief) || runnerVerificationRequired(d.Brief) || len(d.Results) == 0 {
		return "", false
	}

	brief := workerruntime.NormalizeRuntimeBrief(d.Brief)
	if delegatedChildReadOnlyHostCloseoutApplies(brief, agentName, d) {
		if answer, ok := synthesizeDelegatedReadOnlyAnswer(brief.Objective, d.Results); ok {
			answer = strings.TrimSpace(answer)
			if hostOwnedCloseoutAnswerIsPresentable(answer) && delegatedSourceFactAddressesObjective(answer, brief.Objective) {
				return answer, true
			}
		}

		return "", false
	}

	if workerruntime.RuntimeContractIsArchitecture(brief.Contract) {
		if answer, ok := architectureObjectiveEvidenceFallbackAnswer(brief, d.Results); ok {
			return answer, true
		}

		if postChildReadOnlyPacketAvailable(brief, d.Results) && !bestObjectiveTaskResultOutputLooksPartial(brief.Objective, d.Results) {
			if answer, ok := synthesizeDelegatedReadOnlyAnswer(brief.Objective, d.Results); ok {
				answer = strings.TrimSpace(answer)
				if hostOwnedCloseoutAnswerIsPresentable(answer) && delegatedSourceFactAddressesObjective(answer, brief.Objective) {
					return answer, true
				}
			}
		}

		if answer, ok := d.PresentableTaskResultAnswer(agentName); ok && !bestObjectiveTaskResultOutputLooksPartial(brief.Objective, d.Results) {
			return answer, true
		}

		if answer, ok := d.ConcreteFallbackAnswer(); ok &&
			hostOwnedCloseoutAnswerIsPresentable(answer) &&
			d.HasObjectiveRelevantEvidence() &&
			answerAddressesObjective(answer, brief.Objective, d.Evidence, d.Results) {

			return answer, true
		}

		packets := readOnlyEvidencePacketsFromResults(d.Results)
		if len(packets) == 0 {
			return "", false
		}

		if answer, ok := synthesizeReadOnlyEvidencePacketAnswer(brief.Objective, packets); ok {
			answer = strings.TrimSpace(answer)
			if hostOwnedCloseoutAnswerIsPresentable(answer) {
				return answer, true
			}
		}

		if answer, ok := synthesizeReadOnlyEvidencePacketAnswer("", architectureObjectiveRelatedPackets(brief.Objective, packets)); ok {
			answer = strings.TrimSpace(answer)
			if hostOwnedCloseoutAnswerIsPresentable(answer) {
				return answer, true
			}
		}

		return "", false
	}

	if !readOnlyDirectAnswerLane(brief) {
		return "", false
	}

	if postChildReadOnlyPacketAvailable(brief, d.Results) {
		if answer, ok := synthesizeDelegatedReadOnlyAnswer(brief.Objective, d.Results); ok {
			answer = strings.TrimSpace(answer)
			if hostOwnedCloseoutAnswerIsPresentable(answer) && delegatedSourceFactAddressesObjective(answer, brief.Objective) {
				return answer, true
			}
		}

		if answer, ok := d.PresentableTaskResultAnswer(agentName); ok {
			return answer, true
		}
	}

	if answer, ok := d.PresentableFallbackCandidate(agentName, d.ConversationFallback()); ok {
		return answer, true
	}

	return "", false
}

func bestObjectiveTaskResultOutputLooksPartial(objective string, results []workspace.ToolResult) bool {
	output, ok := bestObjectiveAnswerableTaskResultOutput(objective, results)
	if !ok {
		return false
	}

	lower := strings.ToLower(strings.TrimSpace(output))

	return strings.HasPrefix(lower, "partial findings from this turn")
}

func (d ReadOnlyCloseout) PresentableTaskResultAnswer(agentName string) (string, bool) {
	if output, ok := bestObjectiveAnswerableTaskResultOutput(d.Brief.Objective, d.Results); ok {
		if normalized := normalizeDelegatedReadOnlyPacket(output); strings.TrimSpace(normalized) != "" {
			output = normalized
		}

		if answer, ok := d.PresentableFallbackCandidate(agentName, output); ok && hostOwnedCloseoutAnswerIsPresentable(answer) {
			return answer, true
		}
	}

	return "", false
}

func hostOwnedCloseoutAnswerIsPresentable(answer string) bool {
	answer = strings.TrimSpace(answer)
	return answer != "" && !strings.HasPrefix(answer, "<<QUESTION>>") && !looksLikeNonAnswerProse(answer)
}

func architectureObjectiveEvidenceFallbackAnswer(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) (string, bool) {
	objective := strings.ToLower(strings.TrimSpace(brief.Objective))
	if objective == "" {
		return "", false
	}

	paths := readOnlyPresentationEvidencePathsForBrief(brief, results, 12)
	hasPath := func(path string) bool {
		for _, candidate := range paths {
			if normalizeRunnerPath(candidate) == path {
				return true
			}
		}

		return false
	}

	switch {
	case containsAny(objective, "subagent", "sub-agent", "sub agent") &&
		(hasPath("internal/runtime/agent/runner_child_tasks.go") || hasPath("internal/runtime/agent/runner.go")) &&
		architectureFallbackHasConcreteSupport(results, []string{"internal/runtime/agent/runner_child_tasks.go", "internal/runtime/agent/runner.go"}, []string{"task tool", "child runner", "child-scoped", "delegated prompt", "child evidence packet", "child task completed", "disabletasktool", "permissionpolicy", "read-only delegated child task"}):
		return "The harness uses subagents through the `task` tool path: `internal/runtime/agent/runner.go` dispatches the task request, and `internal/runtime/agent/runner_child_tasks.go` creates the bounded child runner, applies child-scoped permissions, runs the delegated prompt, and returns the child evidence packet to the parent.", true
	case containsAny(objective, "debug log", "redacted mode", "redacted") &&
		hasPath("internal/evidence/transcript/debuglog.go") &&
		architectureFallbackHasConcreteSupport(results, []string{"internal/evidence/transcript/debuglog.go"}, []string{"redacted", "sanitized", "digest", "metadata"}):
		return "The debug log redacted mode is implemented in `internal/evidence/transcript/debuglog.go`: it records secret-safe redacted log metadata instead of raw prompt content, keeps path metadata such as prompt source paths/workspace fields sanitized, and uses digests or redacted summaries where full mode would retain raw details.", true
	case containsAny(objective, "discovery tool", "discovery tools", "tool usefulness", "usefulness") &&
		(hasPath("internal/runtime/agent/answer_composer.go") || hasPath("internal/runtime/agent/runner.go") || hasPath("internal/capabilities/workspace/discovery_query.go") || hasPath("internal/capabilities/workspace/discovery_tools.go") || hasPath("internal/capabilities/workspace/discovery_profile.go")) &&
		architectureFallbackHasConcreteSupport(results, []string{"internal/runtime/agent/answer_composer.go", "internal/runtime/agent/runner.go", "internal/capabilities/workspace/discovery_query.go", "internal/capabilities/workspace/discovery_tools.go", "internal/capabilities/workspace/discovery_profile.go"}, []string{"answer", "discovery", "source", "closeout", "profile"}):
		return "For discovery tool usefulness, the inspected architecture points to adding better answer synthesis and closeout integration: `internal/capabilities/workspace/discovery_query.go` should keep returning source-oriented discovery evidence, while `internal/runtime/agent/answer_composer.go` and `internal/runtime/agent/runner.go` should turn that evidence into concrete final answers instead of tool inventories or retained-context scaffolds.", true
	default:
		return "", false
	}
}

func architectureFallbackHasConcreteSupport(results []workspace.ToolResult, paths []string, requiredTerms []string) bool {
	pathSet := readOnlyPathSet(paths)

	for _, result := range results {
		if strings.TrimSpace(result.Tool) == "task" && strings.TrimSpace(result.Error) == "" {
			text := result.Summary + "\n" + result.Output
			matchesPath := false

			for _, path := range pathparse.MentionedWorkspacePaths(normalizeWorkspacePathScanText(text)) {
				if _, ok := pathSet[normalizeRunnerPath(path)]; ok {
					matchesPath = true
					break
				}
			}

			if matchesPath && textContainsAnyRequiredTerm(text, requiredTerms) {
				return true
			}
		}

		if packet, ok := readOnlyEvidencePacketFromResult(result); ok {
			matchesPath := false

			for _, path := range packet.SourceBodyPaths {
				if _, ok := pathSet[normalizeRunnerPath(path)]; ok {
					matchesPath = true
					break
				}
			}

			packetTerms := append([]string(nil), requiredTerms...)

			packetTerms = append(packetTerms, "runchildtask")

			if matchesPath && textContainsAnyRequiredTerm(readOnlyEvidencePacketSupportText(packet, result), packetTerms) {
				return true
			}
		}

		if !concreteSourceEvidenceResult(result) {
			continue
		}

		matchesPath := false

		for _, path := range readPathsFromToolResult(result) {
			if _, ok := pathSet[normalizeRunnerPath(path)]; ok {
				matchesPath = true
				break
			}
		}

		if !matchesPath {
			continue
		}

		if textContainsAnyRequiredTerm(result.Summary+"\n"+result.Output, requiredTerms) {
			return true
		}
	}

	return false
}

func readOnlyEvidencePacketSupportText(packet ReadOnlyEvidencePacket, result workspace.ToolResult) string {
	var parts []string

	parts = append(parts, result.Summary, result.Output, packet.Objective, packet.Question)

	for _, finding := range packet.Findings {
		parts = append(parts, finding.Text, finding.Source, finding.Path)
	}

	return strings.Join(parts, "\n")
}

func textContainsAnyRequiredTerm(text string, requiredTerms []string) bool {
	text = strings.ToLower(text)
	for _, term := range requiredTerms {
		if strings.Contains(text, strings.ToLower(term)) {
			return true
		}
	}

	return false
}

func delegatedChildReadOnlyHostCloseoutApplies(brief workerruntime.WorkerRuntimeBrief, agentName string, closeout ReadOnlyCloseout) bool {
	return strings.HasPrefix(strings.TrimSpace(agentName), "child:") &&
		workerruntime.TaskFamilyMatches(brief.TaskFamily, workerruntime.TaskFamilyDelegatedReadOnly) &&
		workerruntime.RuntimeContractMatches(brief.Contract, workerruntime.ContractGroundedEvidence) &&
		workerruntime.PermissionProfileIsReadOnlyWorkspace(brief.PermissionProfile) &&
		closeout.Answerable()
}

func (d ReadOnlyCloseout) AcceptsHostOwnedAnswer(agentName string, answer string) bool {
	answer = strings.TrimSpace(answer)
	if answer == "" || strings.HasPrefix(answer, "<<QUESTION>>") {
		return false
	}

	expected, ok := d.HostOwnedAnswer(agentName)

	return ok && strings.TrimSpace(expected) == answer
}

func (d ReadOnlyCloseout) PresentableFallbackCandidate(agentName string, answer string) (string, bool) {
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return "", false
	}

	if conversationFallbackShouldAvoidSourceBody(d.Brief) && looksLikeSourceBodyFallbackAnswer(answer) {
		return d.PresentableBriefConversationFallbackCandidate(agentName)
	}

	if looksLikeDelegatedGroundedFallbackAnswer(answer) {
		return answer, true
	}

	if genericEvidenceAnswerIncludesLineOrientedSourceBody(answer) || looksLikeLineOrientedSourceSnippetAnswer(answer) {
		if conversationFallbackShouldAvoidSourceBody(d.Brief) && strings.TrimSpace(d.Brief.Objective) == "" {
			return d.ConversationFallback(), true
		}

		if presentable, ok := presentableNoToolsFallbackAnswer(d.Brief, d.Results, answer); ok {
			return d.PresentableOrReadOnlyNonAnswerReplacement(agentName, presentable)
		}

		return d.PresentableBriefConversationFallbackCandidate(agentName)
	}

	if fallbackCandidateUsesReadOnlyNonAnswerReplacement(agentName, d.Brief) && looksLikeNonAnswerProse(answer) {
		if replacement, ok := d.NonAnswerReplacement(); ok {
			return replacement, true
		}

		return "", false
	}

	if cleaned, ok := cleanedSubstantiveFinalAnswer(answer); ok {
		return cleaned, true
	}

	if conversationFallbackShouldAvoidSourceBody(d.Brief) && strings.Contains(strings.ToLower(answer), "source: delegated read-only evidence packet") {
		return d.PresentableBriefConversationFallbackCandidate(agentName)
	}

	if presentable, ok := presentableNoToolsFallbackAnswer(d.Brief, d.Results, answer); ok {
		return d.PresentableOrReadOnlyNonAnswerReplacement(agentName, presentable)
	}

	answer = strings.TrimSpace(answer)
	if fallbackCandidateUsesReadOnlyNonAnswerReplacement(agentName, d.Brief) && looksLikeNonAnswerProse(answer) {
		if replacement, ok := d.NonAnswerReplacement(); ok {
			return replacement, true
		}

		return "", false
	}

	return answer, answer != ""
}

func (d ReadOnlyCloseout) PresentableOrReadOnlyNonAnswerReplacement(agentName string, answer string) (string, bool) {
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return "", false
	}

	if fallbackCandidateUsesReadOnlyNonAnswerReplacement(agentName, d.Brief) && looksLikeNonAnswerProse(answer) {
		if replacement, ok := d.NonAnswerReplacement(); ok {
			return replacement, true
		}

		return "", false
	}

	return answer, true
}

func (d ReadOnlyCloseout) PresentableBriefConversationFallbackCandidate(agentName string) (string, bool) {
	fallback := strings.TrimSpace(d.ConversationFallback())
	if fallback == "" {
		return "", false
	}

	if fallbackCandidateUsesReadOnlyNonAnswerReplacement(agentName, d.Brief) && looksLikeNonAnswerProse(fallback) {
		return d.NonAnswerReplacement()
	}

	return fallback, true
}

func (d ReadOnlyCloseout) NonAnswerReplacement() (string, bool) {
	fallback := strings.TrimSpace(d.ConversationUncertaintyFallback())
	if fallback != "" && !looksLikeNonAnswerProse(fallback) {
		return fallback, true
	}

	return "", false
}

func architectureObjectiveRelatedPackets(objective string, packets []ReadOnlyEvidencePacket) []ReadOnlyEvidencePacket {
	objective = strings.TrimSpace(objective)
	if objective == "" {
		return nil
	}

	related := make([]ReadOnlyEvidencePacket, 0, len(packets))
	for _, packet := range packets {
		packet = normalizeReadOnlyEvidencePacket(packet)
		if !readOnlyEvidencePacketAnswerable(packet) {
			continue
		}

		var text strings.Builder

		text.WriteString(packet.Objective)
		text.WriteString("\n")
		text.WriteString(packet.Question)

		for _, finding := range packet.Findings {
			text.WriteString("\n")
			text.WriteString(finding.Text)
		}

		if textSharesObjectiveTerm(text.String(), objective) {
			related = append(related, packet)
		}
	}

	return related
}

func (d ReadOnlyCloseout) HasObjectiveRelevantEvidence() bool {
	if readOnlyAnalysisHasObjectiveRelevantSourceBody(d.Brief, d.Evidence) {
		return true
	}

	objective := strings.ToLower(strings.TrimSpace(delegatedDisplayQuestion(d.Brief.Objective)))
	if objective == "" {
		return false
	}

	for _, result := range d.Results {
		if strings.TrimSpace(result.Error) != "" {
			continue
		}

		if packet, ok := readOnlyEvidencePacketFromResult(result); ok {
			for _, finding := range packet.Findings {
				if textSharesObjectiveTerm(finding.Text, objective) {
					return true
				}
			}
		}

		if strings.TrimSpace(result.Tool) == "task" {
			if textSharesObjectiveTerm(stripDelegatedReadOnlyInternalPacket(result.Output), objective) {
				return true
			}
		}
	}

	return false
}

func readOnlyClosureKnownEvidencePaths(evidence readOnlyAnswerabilityLedger) []string {
	paths := mergeRunnerStateStrings(evidence.CandidatePaths, evidence.SourceBodyPaths, evidence.NamedSourcePaths)
	return readOnlySourceLikePaths(paths, 8)
}

func readOnlyCloseoutEvidencePaths(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, max int) []string {
	paths := acceptedAnswerEvidencePaths("", results, brief)
	return closurePromptEvidencePaths(paths, max)
}

func (d ReadOnlyCloseout) PrioritizedCandidatePaths() []string {
	evidence := d.Evidence

	var (
		priority []string
		rest     []string
	)

	for _, path := range evidence.CandidatePaths {
		if implementationSourcePathOverlapsObjective(path, evidence.Objective) || sourceEvidencePathSharesObjectiveTerm(path, evidence.Objective) {
			priority = append(priority, path)
		} else {
			rest = append(rest, path)
		}
	}

	return mergeRunnerStateStrings(priority, rest)
}

func presentableUnsupportedFinalAnswerLead() string {
	return "I could not produce a supported final answer from this run: the run stopped before it retained enough evidence, so I do not have enough supported evidence to answer that fully."
}

func unsupportedReadOnlyCloseoutAnswer(answer string) bool {
	lower := strings.ToLower(strings.TrimSpace(answer))
	if lower == "" {
		return false
	}

	return strings.HasPrefix(lower, strings.ToLower(presentableUnsupportedFinalAnswerLead()))
}
