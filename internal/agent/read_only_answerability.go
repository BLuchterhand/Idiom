package agent

import (
	"slices"
	"strings"

	"idiom/internal/frontier/pathparse"
	"idiom/internal/frontier/workerruntime"
	"idiom/internal/workspace"
)

type readOnlyEvidenceKind string

const (
	readOnlyEvidenceContextPacket     readOnlyEvidenceKind = "context_packet"
	readOnlyEvidenceSearchHit         readOnlyEvidenceKind = "search_hit"
	readOnlyEvidenceOutline           readOnlyEvidenceKind = "outline"
	readOnlyEvidenceSourceBody        readOnlyEvidenceKind = "source_body"
	readOnlyEvidenceDiagnostic        readOnlyEvidenceKind = "diagnostic"
	readOnlyEvidenceGeneratedArtifact readOnlyEvidenceKind = "generated_artifact"
)

const readOnlyHighCardinalitySearchThreshold = 50

type readOnlyEvidenceItem struct {
	Kind           readOnlyEvidenceKind
	Tool           string
	Path           string
	CandidatePaths []string
	HitCount       int
	Relevant       bool
	Summary        string
}

type readOnlySourceBodyObjectiveSupport struct {
	Path  string
	Terms []string
}

type readOnlyAnswerabilityLedger struct {
	Objective                string
	TaskFamily               string
	Contract                 string
	Items                    []readOnlyEvidenceItem
	CandidatePaths           []string
	CandidateObjectivePaths  []string
	NamedSourcePaths         []string
	MissingNamedSourcePaths  []string
	SourceBodyPaths          []string
	SourceBodyTaskPaths      []string
	RelevantSourceBodyPaths  []string
	GeneratedArtifactPaths   []string
	SourceBodyObjectivePaths []string
	SourceBodyObjectiveTerms []readOnlySourceBodyObjectiveSupport
	SourceOwnerEvidence      []readOnlySourceOwnerEvidence
	SearchHitCount           int
	HighCardinalitySearch    bool
	DiagnosticEvidence       bool
	DiagnosticPaths          []string
	DiagnosticRelevant       bool
}

func readOnlyAnswerabilityForResults(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) readOnlyAnswerabilityLedger {
	brief = workerruntime.NormalizeRuntimeBrief(brief)
	ledger := readOnlyAnswerabilityLedger{
		Objective:  strings.TrimSpace(brief.Objective),
		TaskFamily: strings.TrimSpace(brief.TaskFamily),
		Contract:   strings.TrimSpace(brief.Contract),
	}
	namedTargets := readOnlyObjectiveEvidencePaths(brief)
	ledger.CandidatePaths = mergeRunnerStateStrings(ledger.CandidatePaths, readOnlyBriefCandidateEvidencePaths(brief))
	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" {
			continue
		}
		tool := strings.TrimSpace(result.Tool)
		if tool == "" {
			continue
		}
		if readFileResultOnlyMentionsDiagnosticLogArtifacts(result) {
			paths := readOnlySourceLikePaths(readPathsFromToolResult(result), 8)
			ledger.addItem(readOnlyEvidenceItem{
				Kind:           readOnlyEvidenceGeneratedArtifact,
				Tool:           tool,
				CandidatePaths: paths,
				Summary:        strings.TrimSpace(result.Summary),
			})
			ledger.GeneratedArtifactPaths = mergeRunnerStateStrings(ledger.GeneratedArtifactPaths, paths)
			continue
		}
		if readOnlyDiagnosticEvidenceResult(result) {
			paths := readOnlyDiagnosticEvidencePaths(result, 8)
			relevant := readOnlyDiagnosticEvidenceMatchesObjective(ledger.Objective, namedTargets, result, paths)
			ledger.DiagnosticEvidence = true
			ledger.DiagnosticPaths = mergeRunnerStateStrings(ledger.DiagnosticPaths, paths)
			if relevant {
				ledger.DiagnosticRelevant = true
			}
			ledger.addItem(readOnlyEvidenceItem{
				Kind:           readOnlyEvidenceDiagnostic,
				Tool:           tool,
				CandidatePaths: paths,
				Relevant:       relevant,
				Summary:        strings.TrimSpace(result.Summary),
			})
		}
		if concreteSourceEvidenceResult(result) {
			for _, path := range readOnlySourceLikePaths(readPathsFromToolResult(result), 8) {
				ledger.addItem(readOnlyEvidenceItem{
					Kind:    readOnlyEvidenceSourceBody,
					Tool:    tool,
					Path:    path,
					Summary: strings.TrimSpace(result.Summary),
				})
				ledger.SourceBodyPaths = mergeRunnerStateStrings(ledger.SourceBodyPaths, []string{path})
				ledger.observeSourceBodyObjectiveSupport(path, result.Output)
			}
			continue
		}
		if tool == "task" {
			if packet, ok := readOnlyEvidencePacketFromResult(result); ok {
				sourcePaths := readOnlySourceLikePaths(packet.SourceBodyPaths, 8)
				if readOnlyEvidencePacketAnswerableForObjective(packet, ledger.Objective) {
					for _, path := range sourcePaths {
						ledger.addItem(readOnlyEvidenceItem{
							Kind:    readOnlyEvidenceSourceBody,
							Tool:    tool,
							Path:    path,
							Summary: strings.TrimSpace(result.Summary),
						})
					}
					ledger.SourceBodyPaths = mergeRunnerStateStrings(ledger.SourceBodyPaths, sourcePaths)
					ledger.SourceBodyTaskPaths = mergeRunnerStateStrings(ledger.SourceBodyTaskPaths, sourcePaths)
					if !ledger.directAnswerConversation() || !objectiveAsksImplementationShape(ledger.Objective) {
						ledger.SourceBodyObjectivePaths = mergeRunnerStateStrings(ledger.SourceBodyObjectivePaths, sourcePaths)
					}
				}
				ledger.CandidatePaths = mergeRunnerStateStrings(ledger.CandidatePaths, packet.CandidatePaths, packet.InspectedPaths, sourcePaths)
				continue
			}
			sourcePaths := readOnlySourceLikePaths(sourceBodyEvidencePathsFromDelegatedPacket(result.Output), 8)
			inspectedPathFallback := false
			if len(sourcePaths) == 0 && delegatedTaskOutputHasFindingEvidence(result.Output) {
				sourcePaths = readOnlySourceLikePaths(groundedChildPacketEvidencePaths(ledger.Objective, result.Output), 8)
				inspectedPathFallback = len(sourcePaths) > 0
			}
			ledger.CandidatePaths = mergeRunnerStateStrings(ledger.CandidatePaths, sourcePaths)
			sourcePathsAnswerable := childPacketContainsGroundedAnswerEvidence(ledger.Objective, result.Output) ||
				(inspectedPathFallback && delegatedPacketAddressesObjective(ledger.Objective, result.Output))
			if sourcePathsAnswerable {
				sourceBodyText := sanitizeDelegatedReadOnlyResultOutput(result)
				for _, path := range sourcePaths {
					ledger.addItem(readOnlyEvidenceItem{
						Kind:    readOnlyEvidenceSourceBody,
						Tool:    tool,
						Path:    path,
						Summary: strings.TrimSpace(result.Summary),
					})
					ledger.observeSourceBodyObjectiveSupport(path, sourceBodyText)
				}
				if inspectedPathFallback {
					ledger.SourceBodyObjectivePaths = mergeRunnerStateStrings(ledger.SourceBodyObjectivePaths, sourcePaths)
				}
				ledger.SourceBodyPaths = mergeRunnerStateStrings(ledger.SourceBodyPaths, sourcePaths)
				ledger.SourceBodyTaskPaths = mergeRunnerStateStrings(ledger.SourceBodyTaskPaths, sourcePaths)
			}
			continue
		}
		switch tool {
		case "context_pack":
			candidates := readOnlySourceLikePaths(contextPackCandidatePathsFromResults([]workspace.ToolResult{result}, 16), 16)
			ledger.addItem(readOnlyEvidenceItem{
				Kind:           readOnlyEvidenceContextPacket,
				Tool:           tool,
				CandidatePaths: candidates,
				Summary:        strings.TrimSpace(result.Summary),
			})
			ledger.CandidatePaths = mergeRunnerStateStrings(ledger.CandidatePaths, candidates)
		case "search_content", "search_files", "rg", "grep":
			candidates := readOnlySourceLikePaths(sourceLikePathsFromText(result.Summary+"\n"+result.Output, 128), 128)
			objectiveCandidates := readOnlyObjectiveCandidatePathsFromSearch(result, ledger.Objective, 128)
			count, _ := searchContentSummaryMatchCount(result.Summary)
			if count > ledger.SearchHitCount {
				ledger.SearchHitCount = count
			}
			if count > readOnlyHighCardinalitySearchThreshold {
				ledger.HighCardinalitySearch = true
			}
			ledger.addItem(readOnlyEvidenceItem{
				Kind:           readOnlyEvidenceSearchHit,
				Tool:           tool,
				CandidatePaths: candidates,
				HitCount:       count,
				Summary:        strings.TrimSpace(result.Summary),
			})
			ledger.CandidatePaths = mergeRunnerStateStrings(ledger.CandidatePaths, candidates)
			ledger.CandidateObjectivePaths = mergeRunnerStateStrings(ledger.CandidateObjectivePaths, objectiveCandidates)
		case "discovery_query", "context_query", "context_zoom", "context_impact", "structural_search",
			"go_workspace_symbol", "cpp_workspace_symbol", "python_workspace_symbol", "ts_workspace_symbol", "rust_workspace_symbol", "java_workspace_symbol":
			candidates := readOnlySourceLikePaths(discoveryConsumptionCandidatesFromResult(result, 16), 16)
			if len(candidates) > 0 {
				ledger.addItem(readOnlyEvidenceItem{
					Kind:           readOnlyEvidenceSearchHit,
					Tool:           tool,
					CandidatePaths: candidates,
					Summary:        strings.TrimSpace(result.Summary),
				})
				ledger.CandidatePaths = mergeRunnerStateStrings(ledger.CandidatePaths, candidates)
			}
		default:
			if readOnlyOutlineEvidenceResult(result) {
				candidates := readOnlySourceLikePaths(readPathsFromToolResult(result), 8)
				ledger.addItem(readOnlyEvidenceItem{
					Kind:           readOnlyEvidenceOutline,
					Tool:           tool,
					CandidatePaths: candidates,
					Summary:        strings.TrimSpace(result.Summary),
				})
				ledger.CandidatePaths = mergeRunnerStateStrings(ledger.CandidatePaths, candidates)
			}
		}
	}
	ledger.CandidatePaths = mergeRunnerStateStrings(namedTargets, ledger.CandidatePaths)
	ledger.SourceBodyPaths = mergeRunnerStateStrings(ledger.SourceBodyPaths, sourceEvidenceBodyPaths(results), delegatedReadOnlySourceBodyReadinessPaths(ledger.Objective, results))
	ledger.promoteCollectiveSourceBodyObjectiveSupport(namedTargets)
	ledger.markRelevantSourceBodies(namedTargets)
	return ledger
}

func readOnlyDirectAnswerCandidateOnlyLedger(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) (readOnlyAnswerabilityLedger, bool) {
	if !readOnlyDirectAnswerLane(brief) {
		return readOnlyAnswerabilityLedger{}, false
	}
	ledger := readOnlyAnswerabilityForResults(readOnlyDirectAnswerAcceptanceLedgerBrief(brief), results)
	return ledger, len(ledger.CandidatePaths) > 0 && len(ledger.SourceBodyPaths) == 0 && !ledger.DiagnosticEvidence
}

func delegatedTaskOutputHasFindingEvidence(output string) bool {
	output = strings.TrimSpace(output)
	if output == "" {
		return false
	}
	lower := strings.ToLower(output)
	if readOnlyToolControlGuidanceText(lower) {
		return false
	}
	return containsAny(lower,
		"grounded answer",
		"core files:",
		"primary files:",
		"verified context:",
		"inspected paths:",
		"evidence used:",
		"important flow:",
		"key findings:",
		"\n- read ",
		"\n- found ",
		"\n- located ",
		"\n- calls ",
		"\n- uses ",
		"\n- references ",
	)
}

func (l *readOnlyAnswerabilityLedger) addItem(item readOnlyEvidenceItem) {
	item.Tool = strings.TrimSpace(item.Tool)
	item.Path = normalizeRunnerPath(item.Path)
	item.CandidatePaths = readOnlySourceLikePaths(item.CandidatePaths, 128)
	item.Summary = strings.TrimSpace(item.Summary)
	l.Items = append(l.Items, item)
}

func (l *readOnlyAnswerabilityLedger) observeSourceBodyObjectiveSupport(path string, body string) {
	path = normalizeRunnerPath(path)
	if path == "" || isNonSourceEvidenceArtifactPath(path) || !pathparse.HasWorkspaceFileExtension(path) {
		return
	}
	if l.directAnswerConversation() && objectiveAsksImplementationShape(l.Objective) {
		evidence := readOnlySourceOwnerEvidenceForBody(path, "", l.Objective, body)
		l.observeSourceOwnerEvidence(evidence)
		if evidence.OwnerEligible() && sourceBodyHasStrongObjectiveToken(body, l.Objective) {
			l.SourceBodyObjectivePaths = mergeRunnerStateStrings(l.SourceBodyObjectivePaths, []string{path})
		}
		terms := l.sourceBodyObjectiveTerms(body)
		if len(terms) == 0 {
			return
		}
		l.observeSourceBodyObjectiveTerms(path, terms)
		return
	}
	if sourceBodyHasStrongObjectiveToken(body, l.Objective) &&
		(!l.directAnswerConversation() ||
			!objectiveAsksImplementationShape(l.Objective) ||
			implementationShapeSourceBodyCanClose(l.Objective, body)) {
		l.SourceBodyObjectivePaths = mergeRunnerStateStrings(l.SourceBodyObjectivePaths, []string{path})
	}
	terms := l.sourceBodyObjectiveTerms(body)
	if len(terms) == 0 {
		return
	}
	l.observeSourceBodyObjectiveTerms(path, terms)
}

func (l *readOnlyAnswerabilityLedger) observeSourceBodyObjectiveTerms(path string, terms []string) {
	for i := range l.SourceBodyObjectiveTerms {
		if l.SourceBodyObjectiveTerms[i].Path == path {
			l.SourceBodyObjectiveTerms[i].Terms = uniqueSortedStrings(mergeRunnerStateStrings(l.SourceBodyObjectiveTerms[i].Terms, terms))
			return
		}
	}
	l.SourceBodyObjectiveTerms = append(l.SourceBodyObjectiveTerms, readOnlySourceBodyObjectiveSupport{
		Path:  path,
		Terms: terms,
	})
}

func (l *readOnlyAnswerabilityLedger) observeSourceOwnerEvidence(evidence readOnlySourceOwnerEvidence) {
	evidence.Path = normalizeRunnerPath(evidence.Path)
	if evidence.Path == "" {
		return
	}
	for i := range l.SourceOwnerEvidence {
		if l.SourceOwnerEvidence[i].Path != evidence.Path {
			continue
		}
		if evidence.Score > l.SourceOwnerEvidence[i].Score ||
			evidence.OwnerEligible() ||
			sourceOwnerObjectiveMatchCount(evidence) > sourceOwnerObjectiveMatchCount(l.SourceOwnerEvidence[i]) {
			l.SourceOwnerEvidence[i] = evidence
		}
		return
	}
	l.SourceOwnerEvidence = append(l.SourceOwnerEvidence, evidence)
}

func sourceOwnerObjectiveMatchCount(evidence readOnlySourceOwnerEvidence) int {
	return len(evidence.SpecificTerms) + len(evidence.WeakTerms)
}

func (l readOnlyAnswerabilityLedger) sourceBodyObjectiveTerms(body string) []string {
	body = strings.ToLower(body)
	if strings.TrimSpace(body) == "" {
		return nil
	}
	var terms []string
	for _, term := range l.requiredSourceBodyObjectiveTerms() {
		if strings.Contains(body, term) {
			terms = append(terms, term)
		}
	}
	return uniqueSortedStrings(terms)
}

func (l readOnlyAnswerabilityLedger) requiredSourceBodyObjectiveTerms() []string {
	terms := readOnlySearchHitObjectiveTerms(l.Objective)
	if len(terms) == 0 {
		return nil
	}
	var required []string
	seen := map[string]struct{}{}
	for _, term := range terms {
		term = readOnlyObjectiveSupportTerm(term)
		if len(term) < 3 {
			continue
		}
		if readOnlyCollectiveObjectiveSupportStopWord(term) {
			continue
		}
		if _, ok := seen[term]; ok {
			continue
		}
		seen[term] = struct{}{}
		required = append(required, term)
	}
	return required
}

func readOnlyCollectiveObjectiveSupportStopWord(term string) bool {
	term = readOnlyObjectiveSupportTerm(term)
	if term == "" {
		return true
	}
	if _, skip := recoverySearchStopWords()[term]; skip {
		return true
	}
	switch term {
	case "allow", "allows", "allowed", "enable", "enabled", "enabl",
		"support", "supported", "what", "would", "take", "need", "needs",
		"needed", "require", "requires", "required", "between":
		return true
	default:
		return false
	}
}

func readOnlyObjectiveSupportTerm(term string) string {
	term = strings.TrimSpace(strings.ToLower(term))
	if len(term) > 3 && strings.HasSuffix(term, "s") {
		term = strings.TrimSuffix(term, "s")
	}
	return term
}

func (l *readOnlyAnswerabilityLedger) promoteCollectiveSourceBodyObjectiveSupport(namedTargets []string) {
	requiredTerms := l.requiredSourceBodyObjectiveTerms()
	if len(requiredTerms) < 2 {
		return
	}
	if l.directAnswerConversation() && objectiveAsksImplementationShape(l.Objective) {
		l.promoteCollectiveImplementationOwnerSupport(namedTargets, requiredTerms)
		return
	}
	candidateSet := readOnlyPathSet(l.CandidatePaths)
	objectiveCandidateSet := readOnlyPathSet(l.CandidateObjectivePaths)
	namedSet := readOnlyPathSet(namedTargets)
	termPaths := make(map[string][]string, len(requiredTerms))
	pathTermCount := make(map[string]int)
	supportingPaths := map[string]struct{}{}
	for _, support := range l.SourceBodyObjectiveTerms {
		path := normalizeRunnerPath(support.Path)
		if path == "" {
			continue
		}
		for _, term := range support.Terms {
			term = readOnlyObjectiveSupportTerm(term)
			if term == "" {
				continue
			}
			termPaths[term] = mergeRunnerStateStrings(termPaths[term], []string{path})
			supportingPaths[path] = struct{}{}
			pathTermCount[path]++
		}
	}
	if len(supportingPaths) < 2 {
		return
	}
	var collectivePaths []string
	coveredTerms := 0
	for _, term := range requiredTerms {
		paths := termPaths[term]
		if len(paths) > 0 {
			coveredTerms++
			for _, path := range paths {
				if pathTermCount[normalizeRunnerPath(path)] < 2 {
					continue
				}
				if !l.collectiveSourceBodyPromotionPathAllowed(path, candidateSet, objectiveCandidateSet, namedSet) {
					continue
				}
				collectivePaths = mergeRunnerStateStrings(collectivePaths, []string{path})
			}
		}
	}
	if coveredTerms < collectiveSourceBodyObjectiveTermThreshold(len(requiredTerms)) {
		return
	}
	if len(collectivePaths) == 0 {
		return
	}
	l.SourceBodyObjectivePaths = mergeRunnerStateStrings(l.SourceBodyObjectivePaths, collectivePaths)
}

func (l *readOnlyAnswerabilityLedger) promoteCollectiveImplementationOwnerSupport(namedTargets []string, requiredTerms []string) {
	candidateSet := readOnlyPathSet(l.CandidatePaths)
	objectiveCandidateSet := readOnlyPathSet(l.CandidateObjectivePaths)
	namedSet := readOnlyPathSet(namedTargets)
	termPaths := make(map[string][]string, len(requiredTerms))
	for _, support := range l.SourceBodyObjectiveTerms {
		path := normalizeRunnerPath(support.Path)
		if path == "" {
			continue
		}
		for _, term := range support.Terms {
			term = readOnlyObjectiveSupportTerm(term)
			if term == "" {
				continue
			}
			termPaths[term] = mergeRunnerStateStrings(termPaths[term], []string{path})
		}
	}
	coveredTerms := 0
	for _, term := range requiredTerms {
		if len(termPaths[term]) > 0 {
			coveredTerms++
		}
	}
	if coveredTerms < collectiveSourceBodyObjectiveTermThreshold(len(requiredTerms)) {
		return
	}
	var ownerPaths []string
	for _, evidence := range l.SourceOwnerEvidence {
		if !evidence.OwnerEligible() {
			continue
		}
		path := normalizeRunnerPath(evidence.Path)
		if !l.collectiveSourceBodyPromotionPathAllowed(path, candidateSet, objectiveCandidateSet, namedSet) {
			continue
		}
		ownerPaths = mergeRunnerStateStrings(ownerPaths, []string{path})
	}
	if len(ownerPaths) == 0 {
		return
	}
	l.SourceBodyObjectivePaths = mergeRunnerStateStrings(l.SourceBodyObjectivePaths, ownerPaths)
}

func (l readOnlyAnswerabilityLedger) collectiveSourceBodyPromotionPathAllowed(path string, candidateSet map[string]struct{}, objectiveCandidateSet map[string]struct{}, namedSet map[string]struct{}) bool {
	path = normalizeRunnerPath(path)
	if path == "" {
		return false
	}
	if _, ok := namedSet[path]; ok {
		return true
	}
	if _, ok := objectiveCandidateSet[path]; ok {
		return true
	}
	if _, ok := candidateSet[path]; ok {
		return true
	}
	return implementationSourcePathOverlapsObjective(path, l.Objective) ||
		sourceEvidencePathSharesObjectiveTerm(path, l.Objective) ||
		candidateSourcePathStronglyOverlapsObjective(path, l.Objective)
}

func collectiveSourceBodyObjectiveTermThreshold(requiredTermCount int) int {
	switch {
	case requiredTermCount <= 0:
		return 1
	case requiredTermCount <= 2:
		return requiredTermCount
	case requiredTermCount <= 4:
		return 3
	default:
		return 4
	}
}

func (l *readOnlyAnswerabilityLedger) markRelevantSourceBodies(namedTargets []string) {
	l.NamedSourcePaths = readOnlySourceLikePaths(namedTargets, 128)
	sourcePaths := readOnlySourceLikePaths(l.SourceBodyPaths, 128)
	l.SourceBodyPaths = sourcePaths
	sourceSet := make(map[string]struct{}, len(sourcePaths))
	for _, path := range sourcePaths {
		sourceSet[path] = struct{}{}
	}
	candidateSet := readOnlyPathSet(l.CandidatePaths)
	objectiveCandidateSet := readOnlyPathSet(l.CandidateObjectivePaths)
	namedSet := readOnlyPathSet(namedTargets)
	objectiveBodySet := readOnlyPathSet(l.SourceBodyObjectivePaths)
	taskSourceBodySet := readOnlyPathSet(l.SourceBodyTaskPaths)
	var relevant []string
	for path := range sourceSet {
		if l.sourceBodyPathRelevant(path, candidateSet, objectiveCandidateSet, namedSet, objectiveBodySet, taskSourceBodySet) {
			relevant = append(relevant, path)
		}
	}
	l.RelevantSourceBodyPaths = uniqueSortedRunnerPaths(relevant)
	for _, named := range l.NamedSourcePaths {
		if _, ok := sourceSet[named]; !ok {
			l.MissingNamedSourcePaths = append(l.MissingNamedSourcePaths, named)
		}
	}
	for i := range l.Items {
		if l.Items[i].Kind == readOnlyEvidenceSourceBody {
			_, l.Items[i].Relevant = readOnlyPathSet(l.RelevantSourceBodyPaths)[normalizeRunnerPath(l.Items[i].Path)]
		}
	}
}

func (l readOnlyAnswerabilityLedger) sourceBodyPathRelevant(path string, candidateSet map[string]struct{}, objectiveCandidateSet map[string]struct{}, namedSet map[string]struct{}, objectiveBodySet map[string]struct{}, taskSourceBodySet map[string]struct{}) bool {
	path = normalizeRunnerPath(path)
	if path == "" || isNonSourceEvidenceArtifactPath(path) {
		return false
	}
	_, fromTaskPacket := taskSourceBodySet[path]
	if _, ok := namedSet[path]; ok {
		return true
	}
	if l.HighCardinalitySearch {
		if _, ok := candidateSet[path]; !ok {
			return false
		}
		if _, ok := objectiveBodySet[path]; !ok {
			return false
		}
		if implementationSourcePathOverlapsObjective(path, l.Objective) ||
			sourceEvidencePathSharesObjectiveTerm(path, l.Objective) {
			return true
		}
		_, objectiveHit := objectiveCandidateSet[path]
		return objectiveHit
	}
	if _, ok := candidateSet[path]; ok {
		if !l.directAnswerConversation() {
			return true
		}
		return l.candidateSourceBodyPathRelevant(path, objectiveCandidateSet, objectiveBodySet)
	}
	if _, ok := objectiveBodySet[path]; ok {
		return true
	}
	if implementationSourcePathOverlapsObjective(path, l.Objective) || sourceEvidencePathSharesObjectiveTerm(path, l.Objective) {
		return true
	}
	return !fromTaskPacket && len(candidateSet) == 0 && len(namedSet) == 0 && !l.directConversationWithoutObjective()
}

func (l readOnlyAnswerabilityLedger) directAnswerConversation() bool {
	return workerruntime.TaskFamilyIsConversation(l.TaskFamily) &&
		workerruntime.RuntimeContractIsDirectAnswer(l.Contract)
}

func (l readOnlyAnswerabilityLedger) candidateSourceBodyPathRelevant(path string, objectiveCandidateSet map[string]struct{}, objectiveBodySet map[string]struct{}) bool {
	if strings.TrimSpace(l.Objective) == "" {
		return true
	}
	if _, ok := objectiveBodySet[path]; ok {
		return true
	}
	if candidateSourcePathStronglyOverlapsObjective(path, l.Objective) {
		return true
	}
	_, objectiveHit := objectiveCandidateSet[path]
	return objectiveHit
}

func candidateSourcePathStronglyOverlapsObjective(path string, objective string) bool {
	path = strings.ToLower(strings.TrimSpace(path))
	objective = strings.ToLower(strings.TrimSpace(objective))
	if path == "" || objective == "" {
		return false
	}
	path = strings.NewReplacer("-", " ", "_", " ", "/", " ", ".", " ").Replace(path)
	matches := 0
	seen := map[string]struct{}{}
	for _, term := range recoveryObjectiveSearchWords(objective, 32) {
		term = singularObjectiveWord(strings.TrimSpace(term))
		if len(term) < 4 {
			continue
		}
		if _, ok := seen[term]; ok {
			continue
		}
		seen[term] = struct{}{}
		if strings.Contains(path, term) {
			matches++
		}
	}
	return matches >= 2
}

func readOnlyObjectiveCandidatePathsFromSearch(result workspace.ToolResult, objective string, max int) []string {
	if max <= 0 {
		max = 128
	}
	var paths []string
	for _, line := range strings.Split(result.Summary+"\n"+result.Output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !readOnlySearchHitHasObjectiveTerms(line, objective) {
			continue
		}
		linePaths := readOnlySourceLikePaths(sourceLikePathsFromText(line, max), max)
		paths = mergeRunnerStateStrings(paths, linePaths)
		if len(paths) >= max {
			return paths[:max]
		}
	}
	return paths
}

func readOnlySearchHitHasObjectiveTerms(line string, objective string) bool {
	text := strings.ToLower(strings.TrimSpace(line))
	if text == "" {
		return false
	}
	terms := readOnlySearchHitObjectiveTerms(objective)
	matches := 0
	for _, term := range terms {
		if strings.Contains(text, term) {
			matches++
		}
	}
	return matches >= 2
}

func readOnlySearchHitObjectiveTerms(objective string) []string {
	lower := strings.ToLower(strings.TrimSpace(objective))
	if lower == "" {
		return nil
	}
	replacer := strings.NewReplacer("'", " ", "\"", " ", "`", " ", "’", " ", "-", " ", "_", " ", "/", " ")
	lower = replacer.Replace(lower)
	words := strings.FieldsFunc(lower, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	})
	stop := map[string]struct{}{
		"what": {}, "does": {}, "this": {}, "that": {}, "with": {}, "from": {}, "about": {},
		"explain": {}, "describe": {}, "should": {}, "could": {}, "would": {}, "where": {}, "when": {}, "which": {},
	}
	seen := map[string]struct{}{}
	var terms []string
	for _, word := range words {
		word = singularObjectiveWord(strings.TrimSpace(word))
		if len(word) < 4 {
			continue
		}
		if _, skip := stop[word]; skip {
			continue
		}
		if _, ok := seen[word]; ok {
			continue
		}
		seen[word] = struct{}{}
		terms = append(terms, word)
	}
	return terms
}

func sourceBodyHasStrongObjectiveToken(output string, objective string) bool {
	text := strings.ToLower(output)
	if strings.TrimSpace(text) == "" {
		return false
	}
	terms := recoveryObjectiveSearchWords(objective, 32)
	if len(terms) == 0 {
		return false
	}
	seenTerms := map[string]struct{}{}
	var objectiveTerms []string
	for _, term := range terms {
		term = singularObjectiveWord(term)
		if len(term) < 3 {
			continue
		}
		if _, ok := seenTerms[term]; ok {
			continue
		}
		seenTerms[term] = struct{}{}
		objectiveTerms = append(objectiveTerms, term)
	}
	if len(objectiveTerms) == 0 {
		return false
	}
	specificCandidates := objectiveTerms
	if filtered := sourceOwnerSpecificObjectiveTerms(objectiveTerms); len(filtered) > 0 {
		specificCandidates = filtered
	}
	specificTerms := sourceOwnerSpecificObjectiveTerms(specificCandidates)
	if len(specificTerms) == 0 {
		specificTerms = longestObjectiveTerms(objectiveTerms, 1)
	}
	proseMatches := 0
	proseHasSpecific := false
	for _, term := range objectiveTerms {
		if strings.Contains(text, term) {
			proseMatches++
		}
	}
	for _, term := range specificTerms {
		if strings.Contains(text, term) {
			proseHasSpecific = true
			break
		}
	}
	if proseHasSpecific && proseMatches >= 2 {
		return true
	}
	for _, token := range codeLikeObjectiveTokens(text) {
		matches := 0
		hasSpecific := false
		for _, term := range objectiveTerms {
			if strings.Contains(token, term) {
				matches++
			}
		}
		for _, term := range specificTerms {
			if strings.Contains(token, term) {
				hasSpecific = true
				break
			}
		}
		if hasSpecific && matches >= 2 {
			return true
		}
	}
	return false
}

func sourceOwnerSpecificObjectiveTerms(terms []string) []string {
	var filtered []string
	for _, term := range terms {
		term = readOnlyObjectiveSupportTerm(term)
		if term == "" || weakSourceOwnerObjectiveTerm(term) {
			continue
		}
		filtered = append(filtered, term)
	}
	return uniqueSortedStrings(filtered)
}

func implementationShapeSourceBodyCanClose(objective string, body string) bool {
	if !objectiveAsksImplementationShape(objective) {
		return false
	}
	return readOnlySourceOwnerEvidenceForBody("", "", objective, body).OwnerEligible()
}

func weakSourceOwnerObjectiveTerm(term string) bool {
	switch readOnlyObjectiveSupportTerm(term) {
	case "user", "chat", "history", "key", "arrow", "up", "down", "navigate", "old", "easy", "find", "intent", "make", "typing", "type":
		return true
	default:
		return false
	}
}

func readOnlyObjectiveAnswerEvidenceSupported(output string, objective string) bool {
	return readOnlyObjectiveAnswerEvidenceSupportedWithMinTerms(output, objective, 2)
}

func readOnlyObjectiveAnswerEvidenceSupportedByNamedSource(output string, objective string) bool {
	return readOnlyObjectiveAnswerEvidenceSupportedWithMinTerms(output, objective, 1)
}

func readOnlyObjectiveAnswerEvidenceSupportedWithMinTerms(output string, objective string, minTerms int) bool {
	objective = strings.TrimSpace(objective)
	output = strings.TrimSpace(output)
	if objective == "" || output == "" {
		return false
	}
	objectiveText := answerTextWithoutPathCitations(output)
	mechanismText := strings.ToLower(answerProseForObjectiveMatching(objectiveText))
	if sourceBodyHasStrongObjectiveToken(objectiveText, objective) &&
		readOnlyExplanationHasMechanismVerb(mechanismText) {
		return true
	}
	terms := readOnlyObjectivePromotionTerms(objective)
	if len(terms) == 0 || !readOnlyExplanationHasMechanismVerb(mechanismText) {
		return false
	}
	matches := 0
	for _, term := range terms {
		if readOnlyObjectivePromotionTermInText(objectiveText, term) {
			matches++
		}
	}
	required := minTerms
	if required < 1 {
		required = 1
	}
	if len(terms) < required {
		required = len(terms)
	}
	if len(terms) == 1 {
		required = 1
	}
	return matches >= required
}

func readOnlyObjectivePromotionTerms(objective string) []string {
	rawTerms := recoveryObjectiveSearchWords(objective, 32)
	if len(rawTerms) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	var terms []string
	for _, term := range rawTerms {
		term = singularObjectiveWord(strings.TrimSpace(term))
		if len(term) < 4 || readOnlyObjectivePromotionStopWord(term) {
			continue
		}
		if _, ok := seen[term]; ok {
			continue
		}
		seen[term] = struct{}{}
		terms = append(terms, term)
	}
	return terms
}

func readOnlyObjectivePromotionStopWord(term string) bool {
	term = singularObjectiveWord(strings.TrimSpace(term))
	if term == "" {
		return true
	}
	if _, skip := recoverySearchStopWords()[term]; skip {
		return true
	}
	switch term {
	case "explain", "describe", "what", "does", "where", "when", "which", "question",
		"their", "your", "ours", "mine", "they", "them",
		"tell", "uses", "using", "user", "overall", "effort", "increase", "increasing",
		"harness", "tool", "tools", "schema", "schemas", "surface", "surfaces":
		return true
	default:
		return false
	}
}

func readOnlyObjectivePromotionTermInText(text string, term string) bool {
	text = strings.ToLower(strings.TrimSpace(text))
	term = singularObjectiveWord(strings.TrimSpace(term))
	if text == "" || term == "" {
		return false
	}
	if strings.Contains(text, term) {
		return true
	}
	if term == "subagent" &&
		(strings.Contains(text, "child runner") ||
			strings.Contains(text, "child task") ||
			strings.Contains(text, "delegated child")) {
		return true
	}
	if strings.HasSuffix(term, "ness") {
		stem := strings.TrimSuffix(term, "ness")
		if len(stem) >= 4 && strings.Contains(text, stem) {
			return true
		}
	}
	if strings.HasSuffix(term, "ed") {
		stem := strings.TrimSuffix(term, "ed")
		if len(stem) >= 4 && (strings.Contains(text, stem) || strings.Contains(text, stem+"ion")) {
			return true
		}
	}
	return false
}

func longestObjectiveTerms(terms []string, limit int) []string {
	terms = append([]string(nil), terms...)
	slices.SortFunc(terms, func(a, b string) int {
		if len(a) == len(b) {
			return strings.Compare(a, b)
		}
		return len(b) - len(a)
	})
	if limit > 0 && len(terms) > limit {
		terms = terms[:limit]
	}
	return terms
}

func codeLikeObjectiveTokens(text string) []string {
	parts := strings.FieldsFunc(text, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	})
	var tokens []string
	seen := map[string]struct{}{}
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if len(part) < 4 {
			continue
		}
		if _, ok := seen[part]; ok {
			continue
		}
		seen[part] = struct{}{}
		tokens = append(tokens, part)
	}
	return tokens
}

func (l readOnlyAnswerabilityLedger) diagnosticEvidenceAnswerable() bool {
	if !l.DiagnosticRelevant {
		return false
	}
	return len(readOnlySourceLikePaths(l.DiagnosticPaths, 8)) > 0
}

func (l readOnlyAnswerabilityLedger) Answerable() bool {
	if l.directAnswerConversation() && objectiveAsksImplementationShape(l.Objective) {
		return l.diagnosticEvidenceAnswerable() || (len(l.ImplementationOwnerPaths()) > 0 && len(l.MissingNamedSourcePaths) == 0)
	}
	return l.diagnosticEvidenceAnswerable() || (len(l.RelevantSourceBodyPaths) > 0 && len(l.MissingNamedSourcePaths) == 0)
}

func (l readOnlyAnswerabilityLedger) ImplementationOwnerPaths() []string {
	var paths []string
	objectiveSet := readOnlyPathSet(l.SourceBodyObjectivePaths)
	for _, evidence := range l.SourceOwnerEvidence {
		path := normalizeRunnerPath(evidence.Path)
		if path == "" || !evidence.OwnerEligible() {
			continue
		}
		if len(objectiveSet) > 0 {
			if _, ok := objectiveSet[path]; !ok {
				continue
			}
		}
		paths = mergeRunnerStateStrings(paths, []string{path})
	}
	return readOnlySourceLikePaths(paths, 16)
}

func (l readOnlyAnswerabilityLedger) SourceOwnerEvidenceForPath(path string) (readOnlySourceOwnerEvidence, bool) {
	path = normalizeRunnerPath(path)
	if path == "" {
		return readOnlySourceOwnerEvidence{}, false
	}
	for _, evidence := range l.SourceOwnerEvidence {
		if normalizeRunnerPath(evidence.Path) == path {
			return evidence, true
		}
	}
	return readOnlySourceOwnerEvidence{}, false
}

func (l readOnlyAnswerabilityLedger) directConversationWithoutObjective() bool {
	return workerruntime.TaskFamilyIsConversation(l.TaskFamily) &&
		workerruntime.RuntimeContractIsDirectAnswer(l.Contract) &&
		strings.TrimSpace(l.Objective) == ""
}

func (l readOnlyAnswerabilityLedger) HasKind(kind readOnlyEvidenceKind) bool {
	for _, item := range l.Items {
		if item.Kind == kind {
			return true
		}
	}
	return false
}

func readOnlyObjectiveEvidencePaths(brief workerruntime.WorkerRuntimeBrief) []string {
	var paths []string
	for _, group := range [][]string{
		brief.TargetRefs,
		pathparse.MentionedWorkspacePaths(brief.Objective),
	} {
		paths = mergeRunnerStateStrings(paths, readOnlySourceLikePaths(group, 64))
	}
	return paths
}

func readOnlyBriefCandidateEvidencePaths(brief workerruntime.WorkerRuntimeBrief) []string {
	return readOnlySourceLikePaths(brief.EvidencePaths, 64)
}

func readOnlySourceLikePaths(paths []string, max int) []string {
	if max <= 0 {
		max = len(paths)
	}
	var out []string
	seen := map[string]struct{}{}
	for _, raw := range paths {
		path := normalizeRunnerPath(raw)
		if path == "" || isNonSourceEvidenceArtifactPath(path) || !pathparse.HasWorkspaceFileExtension(path) {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		out = append(out, path)
		if max > 0 && len(out) >= max {
			break
		}
	}
	return out
}

func readOnlyPathSet(paths []string) map[string]struct{} {
	set := make(map[string]struct{}, len(paths))
	for _, path := range readOnlySourceLikePaths(paths, 0) {
		set[path] = struct{}{}
	}
	return set
}

func readOnlyDiagnosticEvidenceResult(result workspace.ToolResult) bool {
	if strings.TrimSpace(result.Error) != "" {
		return false
	}
	tool := strings.ToLower(strings.TrimSpace(result.Tool))
	if tool == "" {
		return false
	}
	if strings.Contains(tool, "diagnostic") || strings.Contains(tool, "source_sanity") || strings.HasSuffix(tool, "_diagnostics") {
		return strings.TrimSpace(result.Summary) != "" || strings.TrimSpace(result.Output) != ""
	}
	return false
}

func readOnlyDiagnosticEvidencePaths(result workspace.ToolResult, max int) []string {
	if max <= 0 {
		max = 8
	}
	return readOnlySourceLikePaths(mergeRunnerStateStrings(
		readPathsFromToolResult(result),
		readOnlyDiagnosticLocationPaths(result.Summary+"\n"+result.Output, max),
	), max)
}

func readOnlyDiagnosticLocationPaths(text string, max int) []string {
	if max <= 0 {
		max = 8
	}
	var paths []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		for _, path := range sourceLikePathsFromText(line, max) {
			if !readOnlyDiagnosticLineIdentifiesPath(line, path) {
				continue
			}
			paths = mergeRunnerStateStrings(paths, []string{path})
			if len(paths) >= max {
				return paths
			}
		}
	}
	return paths
}

func readOnlyDiagnosticLineIdentifiesPath(line string, path string) bool {
	line = strings.ToLower(strings.TrimSpace(line))
	path = strings.ToLower(strings.TrimSpace(path))
	if line == "" || path == "" {
		return false
	}
	idx := strings.Index(line, path)
	if idx < 0 {
		return false
	}
	after := strings.TrimSpace(line[idx+len(path):])
	if strings.HasPrefix(after, ":") && len(after) > 1 && after[1] >= '0' && after[1] <= '9' {
		return true
	}
	before := strings.TrimSpace(line[:idx])
	return strings.HasSuffix(before, "failed for") ||
		strings.HasSuffix(before, "failed in") ||
		strings.HasSuffix(before, "error in") ||
		strings.HasSuffix(before, "errors in")
}

func readOnlyDiagnosticEvidenceMatchesObjective(objective string, namedTargets []string, result workspace.ToolResult, paths []string) bool {
	objective = strings.TrimSpace(objective)
	paths = readOnlySourceLikePaths(paths, 8)
	if objective == "" || len(paths) == 0 {
		return false
	}
	namedSet := readOnlyPathSet(namedTargets)
	for _, path := range paths {
		if _, ok := namedSet[normalizeRunnerPath(path)]; ok {
			return true
		}
		if implementationSourcePathOverlapsObjective(path, objective) ||
			sourceEvidencePathSharesObjectiveTerm(path, objective) ||
			candidateSourcePathStronglyOverlapsObjective(path, objective) {
			return true
		}
	}
	if len(readOnlySourceLikePaths(namedTargets, 8)) > 0 {
		return false
	}
	if sourceBodyHasStrongObjectiveToken(result.Summary+"\n"+result.Output, objective) {
		return true
	}
	return readOnlyObjectiveExplicitlyAsksForDiagnostics(objective)
}

func readOnlyObjectiveExplicitlyAsksForDiagnostics(objective string) bool {
	lower := strings.ToLower(strings.TrimSpace(objective))
	if lower == "" {
		return false
	}
	return containsAny(lower,
		"diagnostic", "diagnostics",
		"source_sanity",
		"compiler error", "compile error",
	)
}

func readOnlyOutlineEvidenceResult(result workspace.ToolResult) bool {
	if strings.TrimSpace(result.Error) != "" || concreteSourceEvidenceResult(result) {
		return false
	}
	text := strings.ToLower(strings.TrimSpace(result.Summary + "\n" + result.Output))
	if text == "" {
		return false
	}
	if !containsAny(text, "file_outline", "capability: outline", "outlined ", "top-level declaration") {
		return false
	}
	return len(readOnlySourceLikePaths(readPathsFromToolResult(result), 1)) > 0 ||
		len(sourceLikePathsFromText(result.Summary+"\n"+result.Output, 1)) > 0
}

func readOnlyScaffoldFinalAnswer(answer string) bool {
	lower := strings.ToLower(strings.TrimSpace(answer))
	if lower == "" {
		return false
	}
	return strings.Contains(lower, "the retained source context identifies") ||
		strings.Contains(lower, "the strongest implementation evidence is in") ||
		strings.Contains(lower, "use those findings as the source of truth") ||
		strings.Contains(lower, "what this establishes:") ||
		strings.Contains(lower, "the gathered evidence points to these implementation surfaces") ||
		strings.Contains(lower, "inspected paths:")
}

func readOnlyFallbackNeedsAnswerableEvidence(brief workerruntime.WorkerRuntimeBrief, answer string) bool {
	brief = workerruntime.NormalizeRuntimeBrief(brief)
	if runnerBriefRequiresMutation(brief) || runnerVerificationRequired(brief) {
		return false
	}
	if !workerruntime.PermissionProfileIsReadOnlyWorkspace(brief.PermissionProfile) {
		return false
	}
	return isDeterministicFallbackAnswer(answer) ||
		looksLikeGenericEvidenceComposerAnswer(answer) ||
		readOnlyScaffoldFinalAnswer(answer)
}
