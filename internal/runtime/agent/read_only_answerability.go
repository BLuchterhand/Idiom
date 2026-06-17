package agent

import (
	"idiom/internal/platform/util"
	"slices"
	"strings"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/pathparse"
	"idiom/internal/runtime/agent/agentutil"
	"idiom/internal/runtime/workerruntime"
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
	Source         string
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
	HasUnreadCandidatePaths  bool
	GoalRequiresStructureDiscovery bool
}

func readOnlyAnswerabilityForResults(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) readOnlyAnswerabilityLedger {
	brief = workerruntime.NormalizeRuntimeBrief(brief)

	ledger := readOnlyAnswerabilityLedger{
		Objective:  strings.TrimSpace(brief.Objective),
		TaskFamily: strings.TrimSpace(brief.TaskFamily),
		Contract:   strings.TrimSpace(brief.Contract),
	}
	namedTargets := readOnlyObjectiveEvidencePaths(brief)

	ledger.CandidatePaths = util.Merge(ledger.CandidatePaths, readOnlyBriefCandidateEvidencePaths(brief))

	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" {
			continue
		}

		tool := strings.TrimSpace(result.Tool)
		if tool == "" {
			continue
		}

		if readFileResultOnlyMentionsDiagnosticLogArtifacts(result) {
			paths := readOnlySourceLikePaths(agentutil.ReadPathsFromToolResult(result), 8)
			ledger.addItem(readOnlyEvidenceItem{
				Kind:           readOnlyEvidenceGeneratedArtifact,
				Tool:           tool,
				CandidatePaths: paths,
				Summary:        strings.TrimSpace(result.Summary),
			})

			ledger.GeneratedArtifactPaths = util.Merge(ledger.GeneratedArtifactPaths, paths)

			continue
		}

		if readOnlyDiagnosticEvidenceResult(result) {
			paths := readOnlyDiagnosticEvidencePaths(result, 8)
			relevant := readOnlyDiagnosticEvidenceMatchesObjective(ledger.Objective, namedTargets, result, paths)

			ledger.DiagnosticEvidence = true
			ledger.DiagnosticPaths = util.Merge(ledger.DiagnosticPaths, paths)

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
			for _, path := range readOnlySourceLikePaths(agentutil.ReadPathsFromToolResult(result), 8) {
				ledger.addItem(readOnlyEvidenceItem{
					Kind:    readOnlyEvidenceSourceBody,
					Tool:    tool,
					Source:  readOnlyEvidenceItemSource(tool),
					Path:    path,
					Summary: strings.TrimSpace(result.Summary),
				})

				ledger.SourceBodyPaths = util.Merge(ledger.SourceBodyPaths, []string{path})
				ledger.observeSourceBodyObjectiveSupport(path, result.Output)
			}

			continue
		}

		if tool == "task" {
			if packet, ok := readOnlyEvidencePacketFromResult(result); ok {
				sourcePaths := readOnlySourceLikePaths(packet.SourceBodyPaths, 8)
				packetAnswerable := readOnlyEvidencePacketAnswerableForObjective(packet, ledger.Objective)

				if packetAnswerable {
					for _, evidence := range readOnlyEvidencePacketObjectiveSourceOwnerEvidence(packet, ledger.Objective) {
						ledger.observeSourceOwnerEvidence(evidence)

						if evidence.OwnerEligible() {
							ledger.SourceBodyObjectivePaths = util.Merge(ledger.SourceBodyObjectivePaths, []string{evidence.Path})
						}
					}

					for _, path := range sourcePaths {
						ledger.addItem(readOnlyEvidenceItem{
							Kind:    readOnlyEvidenceSourceBody,
							Tool:    tool,
							Source:  readOnlyEvidenceItemSource(tool),
							Path:    path,
							Summary: strings.TrimSpace(result.Summary),
						})
					}

					ledger.SourceBodyPaths = util.Merge(ledger.SourceBodyPaths, sourcePaths)
					ledger.SourceBodyTaskPaths = util.Merge(ledger.SourceBodyTaskPaths, sourcePaths)

					if !ledger.directAnswerConversation() || !agentutil.ObjectiveAsksImplementationShape(ledger.Objective) {
						ledger.SourceBodyObjectivePaths = util.Merge(ledger.SourceBodyObjectivePaths, sourcePaths)
					}
				}

				ledger.CandidatePaths = util.Merge(ledger.CandidatePaths, packet.CandidatePaths, packet.InspectedPaths, sourcePaths)

				continue
			}

			sourcePaths := readOnlySourceLikePaths(sourceBodyEvidencePathsFromDelegatedPacket(result.Output), 8)
			inspectedPathFallback := false

			if len(sourcePaths) == 0 && delegatedTaskOutputHasFindingEvidence(result.Output) {
				sourcePaths = readOnlySourceLikePaths(groundedChildPacketEvidencePaths(ledger.Objective, result.Output), 8)
				inspectedPathFallback = len(sourcePaths) > 0
			}

			ledger.CandidatePaths = util.Merge(ledger.CandidatePaths, sourcePaths)

			sourcePathsAnswerable := childPacketContainsGroundedAnswerEvidence(ledger.Objective, result.Output) ||
				(inspectedPathFallback && delegatedPacketAddressesObjective(ledger.Objective, result.Output))

			if sourcePathsAnswerable {
				sourceBodyText := sanitizeDelegatedReadOnlyResultOutput(result)
				for _, path := range sourcePaths {
					ledger.addItem(readOnlyEvidenceItem{
						Kind:    readOnlyEvidenceSourceBody,
						Tool:    tool,
						Source:  readOnlyEvidenceItemSource(tool),
						Path:    path,
						Summary: strings.TrimSpace(result.Summary),
					})
					ledger.observeSourceBodyObjectiveSupport(path, sourceBodyText)
				}

				if inspectedPathFallback {
					ledger.SourceBodyObjectivePaths = util.Merge(ledger.SourceBodyObjectivePaths, sourcePaths)
				}

				ledger.SourceBodyPaths = util.Merge(ledger.SourceBodyPaths, sourcePaths)
				ledger.SourceBodyTaskPaths = util.Merge(ledger.SourceBodyTaskPaths, sourcePaths)
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

			ledger.CandidatePaths = util.Merge(ledger.CandidatePaths, candidates)
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

			ledger.CandidatePaths = util.Merge(ledger.CandidatePaths, candidates)
			ledger.CandidateObjectivePaths = util.Merge(ledger.CandidateObjectivePaths, objectiveCandidates)
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

				ledger.CandidatePaths = util.Merge(ledger.CandidatePaths, candidates)
			}
		default:
			if readOnlyOutlineEvidenceResult(result) {
				candidates := readOnlySourceLikePaths(agentutil.ReadPathsFromToolResult(result), 8)
				ledger.addItem(readOnlyEvidenceItem{
					Kind:           readOnlyEvidenceOutline,
					Tool:           tool,
					CandidatePaths: candidates,
					Summary:        strings.TrimSpace(result.Summary),
				})

				ledger.CandidatePaths = util.Merge(ledger.CandidatePaths, candidates)
			}
		}
	}

	ledger.CandidatePaths = util.Merge(namedTargets, ledger.CandidatePaths)
	ledger.SourceBodyPaths = util.Merge(ledger.SourceBodyPaths, sourceEvidenceBodyPaths(results), delegatedReadOnlySourceBodyReadinessPaths(ledger.Objective, results))
	ledger.promoteCollectiveSourceBodyObjectiveSupport(namedTargets)
	ledger.markRelevantSourceBodies(namedTargets)

	// Determine whether unread candidate paths exist (candidates not yet read as source bodies).
	candidateSet := make(map[string]struct{}, len(ledger.CandidatePaths))
	for _, p := range ledger.CandidatePaths {
		candidateSet[agentutil.NormalizeRunnerPath(p)] = struct{}{}
	}
	for _, p := range ledger.SourceBodyPaths {
		delete(candidateSet, agentutil.NormalizeRunnerPath(p))
	}
	ledger.HasUnreadCandidatePaths = len(candidateSet) > 0

	// Determine whether the objective requires architecture/structure discovery.
	// This heuristic identifies questions that benefit from reading candidate
	// paths before answering (e.g. "how is the pipeline organized?").
	ledger.GoalRequiresStructureDiscovery = readOnlyObjectiveImpliesArchitecture(ledger.Objective)

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

	return agentutil.ContainsAny(lower,
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
	item.Source = strings.TrimSpace(item.Source)
	item.Path = agentutil.NormalizeRunnerPath(item.Path)
	item.CandidatePaths = readOnlySourceLikePaths(item.CandidatePaths, 128)
	item.Summary = strings.TrimSpace(item.Summary)
	l.Items = append(l.Items, item)
}

func readOnlyEvidenceItemSource(tool string) string {
	tool = strings.TrimSpace(tool)
	if tool == workspace.ToolNamePrefetch {
		return "prefetch"
	}

	return tool
}

func (l *readOnlyAnswerabilityLedger) observeSourceBodyObjectiveSupport(path string, body string) {
	path = agentutil.NormalizeRunnerPath(path)
	if path == "" || agentutil.IsNonSourceEvidenceArtifactPath(path) || !pathparse.HasWorkspaceFileExtension(path) {
		return
	}

	if l.directAnswerConversation() && agentutil.ObjectiveAsksImplementationShape(l.Objective) {
		evidence := readOnlySourceOwnerEvidenceForBody(path, "", l.Objective, body)
		l.observeSourceOwnerEvidence(evidence)

		if evidence.OwnerEligible() && sourceBodyHasStrongObjectiveToken(body, l.Objective) {
			l.SourceBodyObjectivePaths = util.Merge(l.SourceBodyObjectivePaths, []string{path})
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
			!agentutil.ObjectiveAsksImplementationShape(l.Objective) ||
			implementationShapeSourceBodyCanClose(l.Objective, body)) {

		l.SourceBodyObjectivePaths = util.Merge(l.SourceBodyObjectivePaths, []string{path})
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
			l.SourceBodyObjectiveTerms[i].Terms = util.Sorted(util.Merge(l.SourceBodyObjectiveTerms[i].Terms, terms))
			return
		}
	}

	l.SourceBodyObjectiveTerms = append(l.SourceBodyObjectiveTerms, readOnlySourceBodyObjectiveSupport{
		Path:  path,
		Terms: terms,
	})
}

func (l *readOnlyAnswerabilityLedger) observeSourceOwnerEvidence(evidence readOnlySourceOwnerEvidence) {
	evidence.Path = agentutil.NormalizeRunnerPath(evidence.Path)
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

	return util.Sorted(terms)
}

func (l readOnlyAnswerabilityLedger) requiredSourceBodyObjectiveTerms() []string {
	terms := readOnlySearchHitObjectiveTerms(l.Objective)
	if len(terms) == 0 {
		return nil
	}

	var required []string

	seen := map[string]struct{}{}

	for _, term := range terms {
		term = agentutil.ReadOnlyObjectiveSupportTerm(term)
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
	term = agentutil.ReadOnlyObjectiveSupportTerm(term)
	if term == "" {
		return true
	}

	if _, skip := agentutil.RecoverySearchStopWords()[term]; skip {
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



func (l *readOnlyAnswerabilityLedger) promoteCollectiveSourceBodyObjectiveSupport(namedTargets []string) {
	requiredTerms := l.requiredSourceBodyObjectiveTerms()
	if len(requiredTerms) < 2 {
		return
	}

	if l.directAnswerConversation() && agentutil.ObjectiveAsksImplementationShape(l.Objective) {
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
		path := agentutil.NormalizeRunnerPath(support.Path)
		if path == "" {
			continue
		}

		for _, term := range support.Terms {
			term = agentutil.ReadOnlyObjectiveSupportTerm(term)
			if term == "" {
				continue
			}

			termPaths[term] = util.Merge(termPaths[term], []string{path})
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
				if pathTermCount[agentutil.NormalizeRunnerPath(path)] < 2 {
					continue
				}

				if !l.collectiveSourceBodyPromotionPathAllowed(path, candidateSet, objectiveCandidateSet, namedSet) {
					continue
				}

				collectivePaths = util.Merge(collectivePaths, []string{path})
			}
		}
	}

	if coveredTerms < collectiveSourceBodyObjectiveTermThreshold(len(requiredTerms)) {
		return
	}

	if len(collectivePaths) == 0 {
		return
	}

	l.SourceBodyObjectivePaths = util.Merge(l.SourceBodyObjectivePaths, collectivePaths)
}

func (l *readOnlyAnswerabilityLedger) promoteCollectiveImplementationOwnerSupport(namedTargets []string, requiredTerms []string) {
	candidateSet := readOnlyPathSet(l.CandidatePaths)
	objectiveCandidateSet := readOnlyPathSet(l.CandidateObjectivePaths)
	namedSet := readOnlyPathSet(namedTargets)
	termPaths := make(map[string][]string, len(requiredTerms))

	for _, support := range l.SourceBodyObjectiveTerms {
		path := agentutil.NormalizeRunnerPath(support.Path)
		if path == "" {
			continue
		}

		for _, term := range support.Terms {
			term = agentutil.ReadOnlyObjectiveSupportTerm(term)
			if term == "" {
				continue
			}

			termPaths[term] = util.Merge(termPaths[term], []string{path})
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

		path := agentutil.NormalizeRunnerPath(evidence.Path)
		if !l.collectiveSourceBodyPromotionPathAllowed(path, candidateSet, objectiveCandidateSet, namedSet) {
			continue
		}

		ownerPaths = util.Merge(ownerPaths, []string{path})
	}

	if len(ownerPaths) == 0 {
		return
	}

	l.SourceBodyObjectivePaths = util.Merge(l.SourceBodyObjectivePaths, ownerPaths)
}

func (l readOnlyAnswerabilityLedger) collectiveSourceBodyPromotionPathAllowed(path string, candidateSet map[string]struct{}, objectiveCandidateSet map[string]struct{}, namedSet map[string]struct{}) bool {
	path = agentutil.NormalizeRunnerPath(path)
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
			_, l.Items[i].Relevant = readOnlyPathSet(l.RelevantSourceBodyPaths)[agentutil.NormalizeRunnerPath(l.Items[i].Path)]
		}
	}
}

func (l readOnlyAnswerabilityLedger) sourceBodyPathRelevant(path string, candidateSet map[string]struct{}, objectiveCandidateSet map[string]struct{}, namedSet map[string]struct{}, objectiveBodySet map[string]struct{}, taskSourceBodySet map[string]struct{}) bool {
	path = agentutil.NormalizeRunnerPath(path)
	if path == "" || agentutil.IsNonSourceEvidenceArtifactPath(path) {
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

	for _, term := range agentutil.RecoveryObjectiveSearchWords(objective, 32) {
		term = agentutil.SingularObjectiveWord(strings.TrimSpace(term))
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

		paths = util.Merge(paths, linePaths)

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
		word = agentutil.SingularObjectiveWord(strings.TrimSpace(word))
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

	terms := agentutil.RecoveryObjectiveSearchWords(objective, 32)
	if len(terms) == 0 {
		return false
	}

	seenTerms := map[string]struct{}{}

	var objectiveTerms []string

	for _, term := range terms {
		term = agentutil.SingularObjectiveWord(term)
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
		term = agentutil.ReadOnlyObjectiveSupportTerm(term)
		if term == "" || weakSourceOwnerObjectiveTerm(term) {
			continue
		}

		filtered = append(filtered, term)
	}

	return util.Sorted(filtered)
}

func implementationShapeSourceBodyCanClose(objective string, body string) bool {
	if !agentutil.ObjectiveAsksImplementationShape(objective) {
		return false
	}

	return readOnlySourceOwnerEvidenceForBody("", "", objective, body).OwnerEligible()
}

func weakSourceOwnerObjectiveTerm(term string) bool {
	switch agentutil.ReadOnlyObjectiveSupportTerm(term) {
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

	objectiveText := AnswerTextWithoutPathCitations(output)
	mechanismText := strings.ToLower(AnswerProseForObjectiveMatching(objectiveText))

	if sourceBodyHasStrongObjectiveToken(objectiveText, objective) &&
		ReadOnlyExplanationHasMechanismVerb(mechanismText) {

		return true
	}

	terms := readOnlyObjectivePromotionTerms(objective)
	if len(terms) == 0 || !ReadOnlyExplanationHasMechanismVerb(mechanismText) {
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
	rawTerms := agentutil.RecoveryObjectiveSearchWords(objective, 32)
	if len(rawTerms) == 0 {
		return nil
	}

	seen := map[string]struct{}{}

	var terms []string

	for _, term := range rawTerms {
		term = agentutil.SingularObjectiveWord(strings.TrimSpace(term))
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
	term = agentutil.SingularObjectiveWord(strings.TrimSpace(term))
	if term == "" {
		return true
	}

	if _, skip := agentutil.RecoverySearchStopWords()[term]; skip {
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
	term = agentutil.SingularObjectiveWord(strings.TrimSpace(term))

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
	if l.directAnswerConversation() && agentutil.ObjectiveAsksImplementationShape(l.Objective) {
		return l.diagnosticEvidenceAnswerable() || (len(l.ImplementationOwnerPaths()) > 0 && len(l.MissingNamedSourcePaths) == 0)
	}

	if l.diagnosticEvidenceAnswerable() {
		return true
	}

	// When the question is about architecture/structure and there are unread
	// candidate paths, the model should read those candidates before answering.
	if l.GoalRequiresStructureDiscovery && l.HasUnreadCandidatePaths {
		return false
	}

	return len(l.RelevantSourceBodyPaths) > 0 && len(l.MissingNamedSourcePaths) == 0
}

func (l readOnlyAnswerabilityLedger) ImplementationOwnerPaths() []string {
	var paths []string

	objectiveSet := readOnlyPathSet(l.SourceBodyObjectivePaths)

	for _, evidence := range l.SourceOwnerEvidence {
		path := agentutil.NormalizeRunnerPath(evidence.Path)
		if path == "" || !evidence.OwnerEligible() {
			continue
		}

		if len(objectiveSet) > 0 {
			if _, ok := objectiveSet[path]; !ok {
				continue
			}
		}

		paths = util.Merge(paths, []string{path})
	}

	return readOnlySourceLikePaths(paths, 16)
}

func (l readOnlyAnswerabilityLedger) SourceOwnerEvidenceForPath(path string) (readOnlySourceOwnerEvidence, bool) {
	path = agentutil.NormalizeRunnerPath(path)
	if path == "" {
		return readOnlySourceOwnerEvidence{}, false
	}

	for _, evidence := range l.SourceOwnerEvidence {
		if agentutil.NormalizeRunnerPath(evidence.Path) == path {
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
		paths = util.Merge(paths, readOnlySourceLikePaths(group, 64))
	}

	return paths
}

func readOnlyObjectiveImpliesArchitecture(objective string) bool {
	lower := strings.ToLower(strings.Join(strings.Fields(objective), " "))
	return agentutil.ContainsAny(lower,
		"how is",
		"layout",
		"structure",
		"architecture",
		"pipeline",
		"module",
		"organization",
		"how does",
		"component",
		"dependency",
		"organization",
	)
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
		path := agentutil.NormalizeRunnerPath(raw)
		if path == "" || agentutil.IsNonSourceEvidenceArtifactPath(path) || !pathparse.HasWorkspaceFileExtension(path) {
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

	return readOnlySourceLikePaths(util.Merge(
		agentutil.ReadPathsFromToolResult(result),
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

			paths = util.Merge(paths, []string{path})
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
		if _, ok := namedSet[agentutil.NormalizeRunnerPath(path)]; ok {
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

	return agentutil.ContainsAny(lower,
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

	if !agentutil.ContainsAny(text, "file_outline", "capability: outline", "outlined ", "top-level declaration") {
		return false
	}

	return len(readOnlySourceLikePaths(agentutil.ReadPathsFromToolResult(result), 1)) > 0 ||
		len(sourceLikePathsFromText(result.Summary+"\n"+result.Output, 1)) > 0
}

func readOnlyScaffoldFinalAnswer(answer string) bool {
	lower := strings.ToLower(strings.TrimSpace(answer))
	if lower == "" {
		return false
	}

	return strings.Contains(lower, "the retained source context identifies") ||
		strings.Contains(lower, "the strongest implementation evidence is in") ||
		contextDependentSourceFooter(lower) ||
		strings.Contains(lower, "use those findings as the source of truth") ||
		strings.Contains(lower, "what this establishes:") ||
		strings.Contains(lower, "the gathered evidence points to these implementation surfaces") ||
		strings.Contains(lower, "inspected paths:")
}

func contextDependentSourceFooter(lower string) bool {
	normalized := strings.Join(strings.Fields(lower), " ")
	if normalized == "" {
		return false
	}

	if !agentutil.ContainsAny(normalized, "the answer above", "the previous answer", "the prior answer") {
		return false
	}

	return agentutil.ContainsAny(normalized,
		"grounded in the following inspected source files",
		"grounded in the following source files",
		"grounded in these inspected source files",
		"inspected source files:",
		"inspected source file:",
	)
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

func readOnlyAnswerReportsInsufficientEvidence(answer string) bool {
	lower := strings.ToLower(strings.Join(strings.Fields(answer), " "))

	return agentutil.ContainsAny(lower,
		"available evidence is not enough",
		"could not produce a supported final answer",
		"evidence is not enough",
		"insufficient evidence",
		"not have enough supported evidence",
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


func sourceEvidenceLineIsBoilerplate(line string) bool {
	lower := strings.ToLower(strings.TrimSpace(line))
	if lower == "" {
		return true
	}

	if lower == "context:" || lower == "context" {
		return true
	}

	if strings.HasPrefix(lower, "package ") || strings.HasPrefix(lower, "import ") {
		return true
	}

	return lower == "{" || lower == "}" || lower == "};" || strings.HasPrefix(lower, "//")
}





func NormalizedAnswerSymbol(raw string) string {
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


func ExplicitAnswerCodeSymbols(reply string) []string {
	symbols := make([]string, 0, 6)
	add := func(raw string) {
		key := NormalizedAnswerSymbol(raw)
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


func addSourceBodyEvidenceSymbols(symbols map[string]struct{}, output string) {
	for _, line := range exactProbeSourceLines(output) {
		for _, raw := range sourceBodyEvidenceSymbolCandidates(line) {
			key := NormalizedAnswerSymbol(raw)
			if key != "" {
				symbols[key] = struct{}{}
			}
		}
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
			key := NormalizedAnswerSymbol(part)
			if key != "" {
				symbols[key] = struct{}{}
			}
		}
	}
}


func ArchitectureNonSourceSymbolTerm(key string) bool {
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


func ExactEvidenceSymbolSet(results []workspace.ToolResult) map[string]struct{} {
	symbols := make(map[string]struct{})

	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" || strings.TrimSpace(result.Output) == "" {
			continue
		}

		if agentutil.CandidateAnchorOnlyResult(result) {
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
		key := NormalizedAnswerSymbol(symbol)

		if key != "" {
			symbols[key] = struct{}{}
		}

		for _, call := range exactProbeCallNames(symbol, result.Output, 8) {
			key := NormalizedAnswerSymbol(call)
			if key != "" {
				symbols[key] = struct{}{}
			}
		}
	}

	return symbols
}


func SubstantiveAnswerWithoutEchoedObjective(answer string, objective string) string {
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


func AnswerProseForObjectiveMatching(answer string) string {
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


func AnswerTextWithoutPathCitations(answer string) string {
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


func ReadOnlyExplanationHasMechanismVerb(lower string) bool {
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


func TextSharesObjectiveTerm(text string, objective string) bool {
	text = strings.ToLower(strings.TrimSpace(text))
	objective = strings.ToLower(strings.TrimSpace(objective))

	if text == "" || objective == "" {
		return false
	}

	for _, term := range agentutil.RecoveryObjectiveSearchWords(objective, 32) {
		term = agentutil.SingularObjectiveWord(strings.ToLower(strings.TrimSpace(term)))
		if len(term) < 4 {
			continue
		}

		if strings.Contains(text, term) {
			return true
		}
	}

	return false
}


func ReadOnlyTaskResultAnswerableForObjective(result workspace.ToolResult, objective string) bool {
	if strings.TrimSpace(result.Tool) != workspace.ToolNameTask || strings.TrimSpace(result.Error) != "" {
		return false
	}

	if packet, ok := readOnlyEvidencePacketFromResult(result); ok {
		if strings.TrimSpace(objective) == "" {
			return readOnlyEvidencePacketAnswerable(packet)
		}

		return readOnlyEvidencePacketAnswerableForObjective(packet, objective)
	}

	return childPacketContainsGroundedAnswerEvidence(objective, result.Output)
}




