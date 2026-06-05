package agent

import (
	"path/filepath"
	"sort"
	"strings"

	"idiom/internal/frontier/workerruntime"
	"idiom/internal/workspace"
)

type finalAnswerAcceptanceDecision string

const (
	finalAnswerAcceptanceAccepted finalAnswerAcceptanceDecision = "accept"
	finalAnswerAcceptanceRejected finalAnswerAcceptanceDecision = "reject"
)

const finalAnswerAcceptanceSchema = "final_answer_acceptance.v1"

type finalAnswerAcceptance struct {
	SchemaVersion            string
	Decision                 finalAnswerAcceptanceDecision
	BlockerKind              string
	Reason                   string
	EvidencePaths            []string
	ObjectiveRelevanceStatus string
	RelevantSourceBodyPaths  []string
	AnswerObjectiveTerms     []string
}

func (r *Runner) evaluateFinalAnswerAcceptance(answer string, results []workspace.ToolResult) finalAnswerAcceptance {
	acceptance := finalAnswerAcceptance{
		SchemaVersion: finalAnswerAcceptanceSchema,
		Decision:      finalAnswerAcceptanceAccepted,
	}
	if r != nil {
		acceptance.EvidencePaths = acceptedAnswerEvidencePaths(answer, results, r.RuntimeBrief)
	}
	reject := func(kind string, reason string) finalAnswerAcceptance {
		acceptance.Decision = finalAnswerAcceptanceRejected
		acceptance.BlockerKind = kind
		acceptance.Reason = reason
		return acceptance
	}

	// Plan-then-execute planner phase: a successful submit_plan IS the
	// planner's deliverable; the executor phase (run by the orchestrator
	// after this runner exits) is what produces the actual file changes.
	if r != nil && r.planThenExecutePlannerPhase() && successfulSubmitPlanResultCount(results) > 0 {
		return acceptance
	}
	if r != nil && r.planThenExecutePlannerPhase() && r.planThenExecuteSourceSatisfiedAnswerCovers(answer, results) {
		return acceptance
	}
	if looksLikeUnexecutedToolRequestAnswer(answer) {
		return reject("unexecuted_tool_request_final_answer", "unexecuted_tool_request_final_answer")
	}
	if r != nil && runnerBriefRequiresMutation(r.RuntimeBrief) {
		if missing := missingRequiredEditPaths(r.RuntimeBrief.RequiredEditPaths, changedPathsFromToolResults(results)); len(missing) > 0 {
			return reject("required_edit_paths_remaining", "required_edit_paths_remaining")
		}
	}
	if strings.HasPrefix(strings.TrimSpace(answer), "<<QUESTION>>") {
		if r != nil && readOnlyDirectAnswerLane(r.RuntimeBrief) && !runnerBriefRequiresMutation(r.RuntimeBrief) {
			return reject("read_only_protocol_blocker", "read_only_protocol_blocker")
		}
		return reject("user_visible_blocker", "user_visible_blocker")
	}
	if looksLikeNonAnswerProse(answer) {
		return reject("non_answer_prose", "non_answer_prose")
	}
	if r != nil {
		if reason := r.sourceGroundingRejectionReason(answer, results); reason != "" {
			return reject("source_grounding_unmet", reason)
		}
		if relevance := r.readOnlyDirectAnswerRelevance(answer, results); relevance.BlockerKind != "" {
			acceptance.ObjectiveRelevanceStatus = relevance.Status
			acceptance.RelevantSourceBodyPaths = relevance.RelevantSourceBodyPaths
			acceptance.AnswerObjectiveTerms = relevance.AnswerObjectiveTerms
			return reject(relevance.BlockerKind, relevance.Reason)
		} else if relevance.Status != "" {
			acceptance.ObjectiveRelevanceStatus = relevance.Status
			acceptance.RelevantSourceBodyPaths = relevance.RelevantSourceBodyPaths
			acceptance.AnswerObjectiveTerms = relevance.AnswerObjectiveTerms
		}
	}
	if r != nil {
		missing := missingRequiredEvidence(r.RuntimeBrief.RequiredEvidence, answer, results)
		if len(missing) > 0 {
			return reject("required_evidence_unmet", "required_evidence_unmet")
		}
	}
	return acceptance
}

type readOnlyDirectAnswerRelevance struct {
	Status                  string
	BlockerKind             string
	Reason                  string
	RelevantSourceBodyPaths []string
	AnswerObjectiveTerms    []string
}

func (r *Runner) readOnlyDirectAnswerRelevance(answer string, results []workspace.ToolResult) readOnlyDirectAnswerRelevance {
	if r == nil {
		return readOnlyDirectAnswerRelevance{}
	}
	brief := workerruntime.NormalizeRuntimeBrief(r.RuntimeBrief)
	if !readOnlyDirectAnswerAcceptanceApplies(brief) {
		return readOnlyDirectAnswerRelevance{}
	}
	objective := strings.TrimSpace(brief.Objective)
	if objective == "" {
		return readOnlyDirectAnswerRelevance{Status: "skipped_empty_objective"}
	}
	substantiveAnswer := substantiveAnswerWithoutEchoedObjective(answer, objective)
	ledger := readOnlyAnswerabilityForResults(readOnlyDirectAnswerAcceptanceLedgerBrief(brief), results)
	answerSupportedPaths := answerCitedCollectiveSourceBodySupportPaths(answer, ledger)
	if len(answerSupportedPaths) > 0 {
		ledger.SourceBodyObjectivePaths = mergeRunnerStateStrings(ledger.SourceBodyObjectivePaths, answerSupportedPaths)
		ledger.RelevantSourceBodyPaths = mergeRunnerStateStrings(ledger.RelevantSourceBodyPaths, answerSupportedPaths)
	}
	relevance := readOnlyDirectAnswerRelevance{
		Status:                  "checked",
		RelevantSourceBodyPaths: ledger.RelevantSourceBodyPaths,
		AnswerObjectiveTerms:    answerObjectiveTerms(substantiveAnswer, objective),
	}
	if readOnlyAnswerReportsInsufficientEvidence(substantiveAnswer) {
		relevance.Status = "insufficient_evidence_answer"
		if len(ledger.SourceBodyPaths) == 0 && !ledger.DiagnosticEvidence {
			relevance.BlockerKind = "answer_objective_unmet"
			relevance.Reason = "the final answer says the evidence is insufficient before inspecting source-body or diagnostic evidence for the user's objective. Gather bounded source-body evidence, or use <<QUESTION>>: with the exact missing evidence source."
			return relevance
		}
		return relevance
	}
	if plainNamedFileContentObjective(objective) && answerCitesNamedObjectiveSource(answer, ledger, results) {
		return relevance
	}
	if !ledger.Answerable() || answerLacksObjectiveSourceEvidence(answer, ledger) {
		if readOnlyAnswerUsesInspectedSourceAsAnswer(answer, ledger) || answerMakesCausalClaimWithoutObjectiveEvidence(answer, ledger) {
			relevance.BlockerKind = "answer_objective_unmet"
			relevance.Reason = "the final answer uses inspected source evidence, but that evidence does not establish the user's objective. Answer with the exact objective-relevant source-body evidence, state that the current evidence is insufficient, or use <<QUESTION>>: with the exact missing evidence."
			return relevance
		}
		relevance.Status = "skipped_not_answerable"
		return relevance
	}
	if answerAddressesObjective(substantiveAnswer, objective, ledger, results) && answerSourceCitationsAreObjectiveRelevant(answer, ledger) {
		return relevance
	}
	relevance.BlockerKind = "answer_objective_unmet"
	relevance.Reason = "the final answer is grounded in inspected evidence, but it does not address the user's objective. Answer using objective-relevant source-body evidence, or use <<QUESTION>>: with the exact missing evidence."
	return relevance
}

func readOnlyDirectAnswerAcceptanceApplies(brief workerruntime.WorkerRuntimeBrief) bool {
	brief = workerruntime.NormalizeRuntimeBrief(brief)
	return readOnlyDirectAnswerLane(brief)
}

func readOnlyDirectAnswerLane(brief workerruntime.WorkerRuntimeBrief) bool {
	brief = workerruntime.NormalizeRuntimeBrief(brief)
	return workerruntime.PermissionProfileIsReadOnlyWorkspace(brief.PermissionProfile) &&
		readOnlyDirectAnswerTaskFamily(brief.TaskFamily) &&
		workerruntime.RuntimeContractIsDirectAnswer(brief.Contract)
}

func readOnlyDirectAnswerTaskFamily(family string) bool {
	return workerruntime.TaskFamilyIsConversation(family) ||
		workerruntime.TaskFamilyMatches(family, workerruntime.TaskFamilyDirectAnswer)
}

func readOnlyDirectAnswerAcceptanceLedgerBrief(brief workerruntime.WorkerRuntimeBrief) workerruntime.WorkerRuntimeBrief {
	brief = workerruntime.NormalizeRuntimeBrief(brief)
	if workerruntime.TaskFamilyMatches(brief.TaskFamily, workerruntime.TaskFamilyDirectAnswer) {
		brief.TaskFamily = string(workerruntime.TaskFamilyConversation)
	}
	return brief
}

func answerAddressesObjective(answer string, objective string, ledger readOnlyAnswerabilityLedger, results []workspace.ToolResult) bool {
	answer = strings.TrimSpace(substantiveAnswerWithoutEchoedObjective(answer, objective))
	if answer == "" {
		return false
	}
	if sourceBodyHasStrongObjectiveToken(answer, objective) {
		return true
	}
	if plainNamedFileContentObjective(objective) && answerCitesNamedObjectiveSource(answer, ledger, results) {
		return true
	}
	if readOnlyCauseObjective(objective) {
		return false
	}
	if readOnlyExplainerObjective(objective) && answerMentionsInspectedCodeSymbol(answer, results) {
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
	if objectiveLineMatches >= 2 {
		return true
	}
	return false
}

func readOnlyExplainerObjective(objective string) bool {
	lower := strings.ToLower(strings.TrimSpace(objective))
	return strings.HasPrefix(lower, "explain ") || strings.HasPrefix(lower, "describe ")
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
	if readOnlyCauseObjective(ledger.Objective) {
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

func plainNamedFileContentObjective(objective string) bool {
	lower := strings.ToLower(strings.Join(strings.Fields(objective), " "))
	if !containsAny(lower, "what is in", "what's in", "show", "summarize", "summary of", "contents of", "list") {
		return false
	}
	if readOnlyCauseObjective(objective) {
		return false
	}
	return len(readOnlySourceLikePaths(sourceLikePathsFromText(objective, 8), 8)) > 0
}

func readOnlyCauseObjective(objective string) bool {
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
	if !readOnlyCauseObjective(ledger.Objective) {
		return false
	}
	if len(ledger.SourceBodyPaths) == 0 {
		return false
	}
	if len(ledger.SourceBodyObjectivePaths) > 0 {
		return false
	}
	return readOnlyAnswerUsesInspectedSourceAsAnswer(answer, ledger) || readOnlyCauseObjective(answer)
}

func answerMakesCausalClaimWithoutObjectiveEvidence(answer string, ledger readOnlyAnswerabilityLedger) bool {
	return readOnlyCauseObjective(ledger.Objective) &&
		len(ledger.SourceBodyPaths) > 0 &&
		len(ledger.SourceBodyObjectivePaths) == 0 &&
		readOnlyCauseObjective(answer)
}

func readOnlyAnswerReportsInsufficientEvidence(answer string) bool {
	lower := strings.ToLower(strings.Join(strings.Fields(answer), " "))
	return containsAny(lower,
		"available evidence is not enough",
		"evidence is not enough",
		"insufficient evidence",
		"not enough evidence",
		"not enough to name",
		"cannot determine",
		"can't determine",
		"unable to determine",
		"cannot identify",
		"can't identify",
		"unable to identify",
		"cannot name",
		"can't name",
		"unable to name",
	)
}

func acceptedReadOnlyDirectAnswerEvidencePaths(answer string, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) []string {
	if !readOnlyDirectAnswerAcceptanceApplies(brief) {
		return nil
	}
	ledger := readOnlyAnswerabilityForResults(readOnlyDirectAnswerAcceptanceLedgerBrief(brief), results)
	mentioned := readOnlySourceLikePaths(sourceGroundingMentionedPaths(answer), 16)
	relevant := ledger.RelevantSourceBodyPaths
	objectivePaths := ledger.SourceBodyObjectivePaths
	if ledger.directAnswerConversation() && objectiveAsksImplementationShape(ledger.Objective) {
		relevant = ledger.ImplementationOwnerPaths()
		objectivePaths = relevant
	}
	paths := intersectRunnerPaths(mentioned, relevant)
	paths = mergeRunnerStateStrings(paths, objectivePaths, relevant)
	paths = mergeRunnerStateStrings(paths, answerCitedCollectiveSourceBodySupportPaths(answer, ledger))
	if len(paths) == 0 && ledger.DiagnosticRelevant {
		paths = mergeRunnerStateStrings(paths, mentionedEvidencePathsFromToolResults(results, 8))
	}
	return closurePromptEvidencePaths(paths, 8)
}

func answerCitedCollectiveSourceBodySupportPaths(answer string, ledger readOnlyAnswerabilityLedger) []string {
	mentionedSet := readOnlyPathSet(sourceGroundingMentionedPaths(answer))
	if len(mentionedSet) == 0 {
		return nil
	}
	if ledger.directAnswerConversation() && objectiveAsksImplementationShape(ledger.Objective) {
		return intersectRunnerPaths(ledger.ImplementationOwnerPaths(), sourceGroundingMentionedPaths(answer))
	}
	sourceSet := readOnlyPathSet(ledger.SourceBodyPaths)
	requiredTerms := ledger.requiredSourceBodyObjectiveTerms()
	if len(requiredTerms) < 2 {
		return nil
	}
	termCovered := map[string]struct{}{}
	pathTermCount := map[string]int{}
	for _, support := range ledger.SourceBodyObjectiveTerms {
		path := normalizeRunnerPath(support.Path)
		if path == "" {
			continue
		}
		if _, cited := mentionedSet[path]; !cited {
			continue
		}
		if _, read := sourceSet[path]; !read {
			continue
		}
		seenTermsForPath := map[string]struct{}{}
		for _, term := range support.Terms {
			term = readOnlyObjectiveSupportTerm(term)
			if term == "" {
				continue
			}
			termCovered[term] = struct{}{}
			if _, seen := seenTermsForPath[term]; seen {
				continue
			}
			seenTermsForPath[term] = struct{}{}
			pathTermCount[path]++
		}
	}
	if len(termCovered) < collectiveSourceBodyObjectiveTermThreshold(len(requiredTerms)) {
		return nil
	}
	var paths []string
	for path, count := range pathTermCount {
		if count < 2 {
			continue
		}
		paths = append(paths, path)
	}
	return closurePromptEvidencePaths(paths, 8)
}

func intersectRunnerPaths(left []string, right []string) []string {
	rightSet := readOnlyPathSet(right)
	if len(rightSet) == 0 {
		return nil
	}
	var out []string
	for _, raw := range left {
		path := normalizeRunnerPath(raw)
		if path == "" {
			continue
		}
		if _, ok := rightSet[path]; ok {
			out = append(out, path)
		}
	}
	return uniqueSortedRunnerPaths(out)
}

func finalAnswerAcceptanceEventFields(acceptance finalAnswerAcceptance) map[string]any {
	fields := map[string]any{
		"acceptance_contract": acceptance.SchemaVersion,
		"acceptance_decision": string(acceptance.Decision),
	}
	if acceptance.ObjectiveRelevanceStatus != "" {
		fields["objective_relevance"] = acceptance.ObjectiveRelevanceStatus
	}
	if len(acceptance.RelevantSourceBodyPaths) > 0 {
		fields["relevant_source_body_paths"] = cappedStrings(acceptance.RelevantSourceBodyPaths, 8)
	}
	return fields
}

func uniqueSortedStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func cappedStrings(values []string, limit int) []string {
	if limit <= 0 || len(values) <= limit {
		return append([]string(nil), values...)
	}
	return append([]string(nil), values[:limit]...)
}
