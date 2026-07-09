package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/capabilities/workspace/patching"
	"idiom/internal/contracts/pathparse"
	"idiom/internal/contracts/toolfacts"
	"idiom/internal/platform/config"
	"idiom/internal/platform/textutil"
	semanticlanguage "idiom/internal/repository/semantic/language"
	"idiom/internal/repository/semantic/languagetools"
	"idiom/internal/runtime/workerruntime"
)

type readOnlyEvidenceKind string

const (
	readOnlyEvidenceContextPacket     readOnlyEvidenceKind = "context_packet"
	readOnlyEvidenceSearchHit         readOnlyEvidenceKind = "search_hit"
	readOnlyEvidenceOutline           readOnlyEvidenceKind = "outline"
	readOnlyEvidenceSourceBody        readOnlyEvidenceKind = "source_body"
	readOnlyEvidenceDiagnostic        readOnlyEvidenceKind = "diagnostic"
	readOnlyEvidencePlanArtifact      readOnlyEvidenceKind = "plan_artifact"
	readOnlyEvidenceGeneratedArtifact readOnlyEvidenceKind = "generated_artifact"
	readOnlyEvidenceExternalDocument  readOnlyEvidenceKind = "external_document_body"
)

const featurePlannerPlanArtifactPayloadType = "feature_planner_plan_artifact"

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
	Objective                      string
	TaskFamily                     string
	Contract                       string
	Items                          []readOnlyEvidenceItem
	CandidatePaths                 []string
	CandidateObjectivePaths        []string
	NamedSourcePaths               []string
	MissingNamedSourcePaths        []string
	SourceBodyPaths                []string
	SourceBodyTaskPaths            []string
	RelevantSourceBodyPaths        []string
	ExternalDocumentBodyPaths      []string
	RelevantExternalDocumentPaths  []string
	GeneratedArtifactPaths         []string
	SourceBodyObjectivePaths       []string
	SourceBodyObjectiveTerms       []readOnlySourceBodyObjectiveSupport
	SourceOwnerEvidence            []readOnlySourceOwnerEvidence
	SearchHitCount                 int
	HighCardinalitySearch          bool
	DiagnosticEvidence             bool
	DiagnosticPaths                []string
	DiagnosticRelevant             bool
	PlanArtifactEvidence           bool
	PlanArtifactPaths              []string
	PlanArtifactRelevant           bool
	HasUnreadCandidatePaths        bool
	GoalRequiresStructureDiscovery bool
}

type readOnlyDelegatedEvidenceDecision struct {
	Summary                               string
	HasPacket                             bool
	PotentialSourcePaths                  []string
	InspectedPaths                        []string
	CandidatePaths                        []string
	LedgerSourcePaths                     []string
	ReadinessSourcePaths                  []string
	ClosureEvidencePaths                  []string
	FollowupAnchors                       []string
	SourceOwnerEvidence                   []readOnlySourceOwnerEvidence
	SourceBodySupportText                 string
	PromoteLedgerSourcePathsToObjective   bool
	SourceBodyPacketAvailable             bool
	TaskResultAnswerable                  bool
	AcceptedMentionedEvidenceForObjective bool
}

func readOnlyDelegatedEvidenceDecisionForObjective(objective string, fact readOnlyDelegatedEvidenceFact) readOnlyDelegatedEvidenceDecision {
	decision := readOnlyDelegatedEvidenceDecision{
		Summary:        fact.Summary,
		HasPacket:      fact.HasPacket,
		InspectedPaths: append([]string(nil), fact.InspectedPaths...),
		CandidatePaths: append([]string(nil), fact.CandidatePaths...),
	}

	if fact.HasPacket {
		answerable := readOnlyEvidencePacketAnswerableForObjective(fact.Packet, objective)

		decision.PotentialSourcePaths = append([]string(nil), fact.SourceBodyPaths...)
		decision.SourceBodyPacketAvailable = len(fact.SourceBodyPaths) > 0
		if answerable {
			decision.LedgerSourcePaths = append([]string(nil), fact.SourceBodyPaths...)
			decision.ReadinessSourcePaths = append([]string(nil), fact.SourceBodyPaths...)
			decision.ClosureEvidencePaths = append(decision.ClosureEvidencePaths, fact.SourceBodyPaths...)
			decision.ClosureEvidencePaths = append(decision.ClosureEvidencePaths, fact.InspectedPaths...)
			decision.ClosureEvidencePaths = append(decision.ClosureEvidencePaths, fact.CandidatePaths...)
			decision.SourceOwnerEvidence = readOnlyEvidencePacketObjectiveSourceOwnerEvidence(fact.Packet, objective)
		} else {
			decision.FollowupAnchors = readOnlyChildPacketFollowupAnchors(objective, fact.Packet)
		}

		decision.TaskResultAnswerable = answerable
		decision.AcceptedMentionedEvidenceForObjective = strings.TrimSpace(fact.Output) != "" && answerable
		return decision
	}

	return decision
}

func (ledger *readOnlyAnswerabilityLedger) observeDelegatedEvidence(decision readOnlyDelegatedEvidenceDecision) {
	sourcePaths := readOnlySourceLikePaths(decision.PotentialSourcePaths, 8)
	ledger.CandidatePaths = textutil.Merge(
		ledger.CandidatePaths,
		decision.CandidatePaths,
		decision.InspectedPaths,
		sourcePaths,
	)

	for _, evidence := range decision.SourceOwnerEvidence {
		ledger.observeSourceOwnerEvidence(evidence)

		if evidence.OwnerEligible() {
			ledger.SourceBodyObjectivePaths = textutil.Merge(ledger.SourceBodyObjectivePaths, []string{evidence.Path})
		}
	}

	ledgerSourcePaths := readOnlySourceLikePaths(decision.LedgerSourcePaths, 8)
	if len(ledgerSourcePaths) == 0 {
		return
	}

	for _, path := range ledgerSourcePaths {
		ledger.addItem(readOnlyEvidenceItem{
			Kind:    readOnlyEvidenceSourceBody,
			Tool:    workspace.ToolNameTask,
			Source:  workspace.ToolNameTask,
			Path:    path,
			Summary: decision.Summary,
		})

		if decision.SourceBodySupportText != "" {
			ledger.observeSourceBodyObjectiveSupport(path, decision.SourceBodySupportText)
		}
	}

	if decision.PromoteLedgerSourcePathsToObjective ||
		(decision.HasPacket && (!ledger.directAnswerConversation() || !ObjectiveAsksImplementationShape(ledger.Objective))) {

		ledger.SourceBodyObjectivePaths = textutil.Merge(ledger.SourceBodyObjectivePaths, ledgerSourcePaths)
	}

	ledger.SourceBodyPaths = textutil.Merge(ledger.SourceBodyPaths, ledgerSourcePaths)
	ledger.SourceBodyTaskPaths = textutil.Merge(ledger.SourceBodyTaskPaths, ledgerSourcePaths)
}

func readOnlyAnswerabilityForResults(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) readOnlyAnswerabilityLedger {
	return readOnlyAnswerabilityForProjection(brief, newToolResultEvidenceProjection(results))
}

func readOnlyAnswerabilityForProjection(brief workerruntime.WorkerRuntimeBrief, projection toolResultEvidenceProjection) readOnlyAnswerabilityLedger {
	brief = workerruntime.NormalizeRuntimeBrief(brief)

	ledger := readOnlyAnswerabilityLedger{
		Objective:  strings.TrimSpace(brief.Objective),
		TaskFamily: strings.TrimSpace(brief.TaskFamily),
		Contract:   strings.TrimSpace(brief.Contract),
	}
	namedTargets := readOnlyObjectiveEvidencePaths(brief)

	ledger.CandidatePaths = textutil.Merge(ledger.CandidatePaths, readOnlyBriefCandidateEvidencePaths(brief))

	for _, observation := range projection.ReadOnlyEvidenceObservations() {
		switch observation.Kind {
		case readOnlyEvidenceGeneratedArtifact:
			ledger.addItem(readOnlyEvidenceItem{
				Kind:           readOnlyEvidenceGeneratedArtifact,
				Tool:           observation.Tool,
				CandidatePaths: observation.CandidatePaths,
				Summary:        observation.Summary,
			})

			ledger.GeneratedArtifactPaths = textutil.Merge(ledger.GeneratedArtifactPaths, observation.CandidatePaths)

		case readOnlyEvidenceDiagnostic:
			relevant := readOnlyDiagnosticEvidenceMatchesObjective(ledger.Objective, namedTargets, observation.Summary+"\n"+observation.Output, observation.CandidatePaths)

			ledger.DiagnosticEvidence = true
			ledger.DiagnosticPaths = textutil.Merge(ledger.DiagnosticPaths, observation.CandidatePaths)

			if relevant {
				ledger.DiagnosticRelevant = true
			}

			ledger.addItem(readOnlyEvidenceItem{
				Kind:           observation.Kind,
				Tool:           observation.Tool,
				CandidatePaths: observation.CandidatePaths,
				Relevant:       relevant,
				Summary:        observation.Summary,
			})

		case readOnlyEvidencePlanArtifact:
			relevant := featurePlannerPlanArtifactPathsMatchObjective(ledger.Objective, observation.CandidatePaths)
			ledger.PlanArtifactEvidence = true
			ledger.PlanArtifactPaths = textutil.Merge(ledger.PlanArtifactPaths, observation.CandidatePaths)

			if relevant {
				ledger.PlanArtifactRelevant = true
			}

			ledger.addItem(readOnlyEvidenceItem{
				Kind:           readOnlyEvidencePlanArtifact,
				Tool:           observation.Tool,
				CandidatePaths: observation.CandidatePaths,
				Relevant:       relevant,
				Summary:        observation.Summary,
			})

		case readOnlyEvidenceSourceBody:
			ledger.addItem(readOnlyEvidenceItem{
				Kind:    observation.Kind,
				Tool:    observation.Tool,
				Source:  observation.Source,
				Path:    observation.Path,
				Summary: observation.Summary,
			})

			ledger.SourceBodyPaths = textutil.Merge(ledger.SourceBodyPaths, []string{observation.Path})
			ledger.observeSourceBodyObjectiveSupport(observation.Path, observation.Output)

		case readOnlyEvidenceExternalDocument:
			ledger.addItem(readOnlyEvidenceItem{
				Kind:    observation.Kind,
				Tool:    observation.Tool,
				Source:  observation.Source,
				Path:    observation.Path,
				Summary: observation.Summary,
			})

			ledger.ExternalDocumentBodyPaths = textutil.Merge(ledger.ExternalDocumentBodyPaths, []string{observation.Path})
			ledger.observeExternalDocumentObjectiveSupport(observation.Path, observation.Output)

		case readOnlyEvidenceOutline:
			ledger.addItem(readOnlyEvidenceItem{
				Kind:           readOnlyEvidenceOutline,
				Tool:           observation.Tool,
				CandidatePaths: observation.CandidatePaths,
				Summary:        observation.Summary,
			})

			ledger.CandidatePaths = textutil.Merge(ledger.CandidatePaths, observation.CandidatePaths)

		case readOnlyEvidenceContextPacket:
			ledger.addItem(readOnlyEvidenceItem{
				Kind:           readOnlyEvidenceContextPacket,
				Tool:           observation.Tool,
				CandidatePaths: observation.CandidatePaths,
				Summary:        observation.Summary,
			})

			ledger.CandidatePaths = textutil.Merge(ledger.CandidatePaths, observation.CandidatePaths)

		case readOnlyEvidenceSearchHit:
			objectiveCandidates := readOnlyObjectiveCandidatePathsFromText(observation.Summary+"\n"+observation.Output, ledger.Objective, 128)

			if observation.HitCount > ledger.SearchHitCount {
				ledger.SearchHitCount = observation.HitCount
			}

			if observation.HighCardinality {
				ledger.HighCardinalitySearch = true
			}

			ledger.addItem(readOnlyEvidenceItem{
				Kind:           readOnlyEvidenceSearchHit,
				Tool:           observation.Tool,
				CandidatePaths: observation.CandidatePaths,
				HitCount:       observation.HitCount,
				Summary:        observation.Summary,
			})

			ledger.CandidatePaths = textutil.Merge(ledger.CandidatePaths, observation.CandidatePaths)
			ledger.CandidateObjectivePaths = textutil.Merge(ledger.CandidateObjectivePaths, objectiveCandidates)
		}
	}

	for _, fact := range projection.ReadOnlyDelegatedEvidenceFacts() {
		ledger.observeDelegatedEvidence(readOnlyDelegatedEvidenceDecisionForObjective(ledger.Objective, fact))
	}

	ledger.CandidatePaths = textutil.Merge(namedTargets, ledger.CandidatePaths)
	ledger.promoteCollectiveSourceBodyObjectiveSupport(namedTargets)
	ledger.markRelevantSourceBodies(namedTargets)
	ledger.markRelevantExternalDocuments()

	// Determine whether unread candidate paths exist (candidates not yet read as source bodies).
	candidateSet := make(map[string]struct{}, len(ledger.CandidatePaths))
	for _, p := range ledger.CandidatePaths {
		candidateSet[NormalizeRunnerPath(p)] = struct{}{}
	}
	for _, p := range ledger.SourceBodyPaths {
		delete(candidateSet, NormalizeRunnerPath(p))
	}
	ledger.HasUnreadCandidatePaths = len(candidateSet) > 0

	// Determine whether the objective requires architecture/structure discovery.
	// This heuristic identifies questions that benefit from reading candidate
	// paths before answering (e.g. "how is the pipeline organized?").
	ledger.GoalRequiresStructureDiscovery = readOnlyObjectiveImpliesArchitecture(ledger.Objective)

	return ledger
}

func (l *readOnlyAnswerabilityLedger) addItem(item readOnlyEvidenceItem) {
	item.Tool = strings.TrimSpace(item.Tool)
	item.Source = strings.TrimSpace(item.Source)
	item.Path = NormalizeRunnerPath(item.Path)
	item.CandidatePaths = readOnlySourceLikePaths(item.CandidatePaths, 128)
	item.Summary = strings.TrimSpace(item.Summary)
	l.Items = append(l.Items, item)
}

func (l *readOnlyAnswerabilityLedger) observeSourceBodyObjectiveSupport(path string, body string) {
	path = NormalizeRunnerPath(path)
	if path == "" || IsNonSourceEvidenceArtifactPath(path) || !pathparse.HasWorkspaceFileExtension(path) {
		return
	}

	if l.directAnswerConversation() && ObjectiveAsksImplementationShape(l.Objective) {
		evidence := readOnlySourceOwnerEvidenceForBody(path, "", l.Objective, body)
		l.observeSourceOwnerEvidence(evidence)

		if evidence.OwnerEligible() && sourceBodyHasStrongObjectiveToken(body, l.Objective) {
			l.SourceBodyObjectivePaths = textutil.Merge(l.SourceBodyObjectivePaths, []string{path})
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
			!ObjectiveAsksImplementationShape(l.Objective) ||
			implementationShapeSourceBodyCanClose(l.Objective, body)) {

		l.SourceBodyObjectivePaths = textutil.Merge(l.SourceBodyObjectivePaths, []string{path})
	}

	terms := l.sourceBodyObjectiveTerms(body)
	if l.sourceBodyObjectiveTermSupportSatisfies(path, terms, body) {
		l.SourceBodyObjectivePaths = textutil.Merge(l.SourceBodyObjectivePaths, []string{path})
	}

	if len(terms) == 0 {
		return
	}

	l.observeSourceBodyObjectiveTerms(path, terms)
}

func (l *readOnlyAnswerabilityLedger) observeExternalDocumentObjectiveSupport(path string, body string) {
	path = normalizeReadOnlyExternalDocumentPath(path)
	if path == "" || !readOnlyExternalDocumentBodyPath(path) {
		return
	}

	if sourceBodyHasStrongObjectiveToken(body, l.Objective) {
		l.SourceBodyObjectivePaths = textutil.Merge(l.SourceBodyObjectivePaths, []string{path})
	}

	terms := l.sourceBodyObjectiveTerms(body)
	if l.sourceBodyObjectiveTermSupportSatisfies(path, terms, body) {
		l.SourceBodyObjectivePaths = textutil.Merge(l.SourceBodyObjectivePaths, []string{path})
	}

	if len(terms) == 0 {
		return
	}

	l.observeSourceBodyObjectiveTerms(path, terms)
}

func (l *readOnlyAnswerabilityLedger) observeSourceBodyObjectiveTerms(path string, terms []string) {
	for i := range l.SourceBodyObjectiveTerms {
		if l.SourceBodyObjectiveTerms[i].Path == path {
			l.SourceBodyObjectiveTerms[i].Terms = textutil.Sorted(textutil.Merge(l.SourceBodyObjectiveTerms[i].Terms, terms))
			return
		}
	}

	l.SourceBodyObjectiveTerms = append(l.SourceBodyObjectiveTerms, readOnlySourceBodyObjectiveSupport{
		Path:  path,
		Terms: terms,
	})
}

func (l *readOnlyAnswerabilityLedger) observeSourceOwnerEvidence(evidence readOnlySourceOwnerEvidence) {
	evidence.Path = NormalizeRunnerPath(evidence.Path)
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

	return textutil.Sorted(terms)
}

func (l readOnlyAnswerabilityLedger) sourceBodyObjectiveTermSupportSatisfies(path string, terms []string, body string) bool {
	required := l.requiredSourceBodyObjectiveTerms()
	if len(required) == 0 || len(terms) == 0 {
		return false
	}

	termSet := map[string]struct{}{}
	for _, term := range terms {
		term = ReadOnlyObjectiveSupportTerm(term)
		if term == "" {
			continue
		}

		termSet[term] = struct{}{}
	}

	matches := 0
	for _, requiredTerm := range required {
		requiredTerm = ReadOnlyObjectiveSupportTerm(requiredTerm)
		if requiredTerm == "" {
			continue
		}

		if _, ok := termSet[requiredTerm]; ok {
			matches++
		}
	}

	if len(required) == 1 {
		requiredTerm := ReadOnlyObjectiveSupportTerm(required[0])
		if readOnlyTextNegatesObjectiveTerm(l.Objective, requiredTerm) &&
			!readOnlyTextNegatesObjectiveTerm(body, requiredTerm) {

			return false
		}

		_, carried := readOnlyPathSet(l.CandidatePaths)[NormalizeRunnerPath(path)]
		return matches == 1 &&
			readOnlyCarriedReferentObjective(l.Objective) &&
			carried
	}

	return false
}

func readOnlyTextNegatesObjectiveTerm(text string, term string) bool {
	term = ReadOnlyObjectiveSupportTerm(term)
	if term == "" {
		return false
	}

	normalized := " " + strings.Join(strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	}), " ") + " "

	return strings.Contains(normalized, " not "+term+" ") ||
		strings.Contains(normalized, " no "+term+" ") ||
		strings.Contains(normalized, " non "+term+" ") ||
		strings.Contains(normalized, " not yet "+term+" ") ||
		strings.Contains(normalized, " "+term+" no ") ||
		strings.Contains(normalized, " "+term+" false ")
}

func readOnlyCarriedReferentObjective(objective string) bool {
	words := objectiveWordSet(objective)
	for _, word := range []string{"these", "those", "they", "them", "their"} {
		if _, ok := words[word]; ok {
			return true
		}
	}

	return false
}

func (l readOnlyAnswerabilityLedger) requiredSourceBodyObjectiveTerms() []string {
	terms := readOnlySearchHitObjectiveTerms(l.Objective)
	if len(terms) == 0 {
		return nil
	}

	var required []string

	seen := map[string]struct{}{}

	for _, term := range terms {
		term = ReadOnlyObjectiveSupportTerm(term)
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
	term = ReadOnlyObjectiveSupportTerm(term)
	if term == "" {
		return true
	}

	if _, skip := RecoverySearchStopWords()[term]; skip {
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

	if l.directAnswerConversation() && ObjectiveAsksImplementationShape(l.Objective) {
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
		path := NormalizeRunnerPath(support.Path)
		if path == "" {
			continue
		}

		for _, term := range support.Terms {
			term = ReadOnlyObjectiveSupportTerm(term)
			if term == "" {
				continue
			}

			termPaths[term] = textutil.Merge(termPaths[term], []string{path})
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
				if pathTermCount[NormalizeRunnerPath(path)] < 2 {
					continue
				}

				if !l.collectiveSourceBodyPromotionPathAllowed(path, candidateSet, objectiveCandidateSet, namedSet) {
					continue
				}

				collectivePaths = textutil.Merge(collectivePaths, []string{path})
			}
		}
	}

	if coveredTerms < collectiveSourceBodyObjectiveTermThreshold(len(requiredTerms)) {
		return
	}

	if len(collectivePaths) == 0 {
		return
	}

	l.SourceBodyObjectivePaths = textutil.Merge(l.SourceBodyObjectivePaths, collectivePaths)
}

func (l *readOnlyAnswerabilityLedger) promoteCollectiveImplementationOwnerSupport(namedTargets []string, requiredTerms []string) {
	candidateSet := readOnlyPathSet(l.CandidatePaths)
	objectiveCandidateSet := readOnlyPathSet(l.CandidateObjectivePaths)
	namedSet := readOnlyPathSet(namedTargets)
	termPaths := make(map[string][]string, len(requiredTerms))

	for _, support := range l.SourceBodyObjectiveTerms {
		path := NormalizeRunnerPath(support.Path)
		if path == "" {
			continue
		}

		for _, term := range support.Terms {
			term = ReadOnlyObjectiveSupportTerm(term)
			if term == "" {
				continue
			}

			termPaths[term] = textutil.Merge(termPaths[term], []string{path})
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

		path := NormalizeRunnerPath(evidence.Path)
		if !l.collectiveSourceBodyPromotionPathAllowed(path, candidateSet, objectiveCandidateSet, namedSet) {
			continue
		}

		ownerPaths = textutil.Merge(ownerPaths, []string{path})
	}

	if len(ownerPaths) == 0 {
		return
	}

	l.SourceBodyObjectivePaths = textutil.Merge(l.SourceBodyObjectivePaths, ownerPaths)
}

func (l readOnlyAnswerabilityLedger) collectiveSourceBodyPromotionPathAllowed(path string, candidateSet map[string]struct{}, objectiveCandidateSet map[string]struct{}, namedSet map[string]struct{}) bool {
	path = NormalizeRunnerPath(path)
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
			_, l.Items[i].Relevant = readOnlyPathSet(l.RelevantSourceBodyPaths)[NormalizeRunnerPath(l.Items[i].Path)]
		}
	}
}

func (l *readOnlyAnswerabilityLedger) markRelevantExternalDocuments() {
	bodySet := readOnlyExternalDocumentPathSet(l.ExternalDocumentBodyPaths)
	objectiveSet := readOnlyExternalDocumentPathSet(l.SourceBodyObjectivePaths)

	var relevant []string
	for path := range bodySet {
		if _, ok := objectiveSet[path]; ok {
			relevant = append(relevant, path)
		}
	}

	l.RelevantExternalDocumentPaths = textutil.Sorted(relevant)
	relevantSet := readOnlyExternalDocumentPathSet(l.RelevantExternalDocumentPaths)
	for i := range l.Items {
		if l.Items[i].Kind == readOnlyEvidenceExternalDocument {
			_, l.Items[i].Relevant = relevantSet[normalizeReadOnlyExternalDocumentPath(l.Items[i].Path)]
		}
	}
}

func (l readOnlyAnswerabilityLedger) sourceBodyPathRelevant(path string, candidateSet map[string]struct{}, objectiveCandidateSet map[string]struct{}, namedSet map[string]struct{}, objectiveBodySet map[string]struct{}, taskSourceBodySet map[string]struct{}) bool {
	path = NormalizeRunnerPath(path)
	if path == "" || IsNonSourceEvidenceArtifactPath(path) {
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

	for _, term := range RecoveryObjectiveSearchWords(objective, 32) {
		term = SingularObjectiveWord(strings.TrimSpace(term))
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

func readOnlyObjectiveCandidatePathsFromText(text string, objective string, max int) []string {
	if max <= 0 {
		max = 128
	}

	var paths []string

	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !readOnlySearchHitHasObjectiveTerms(line, objective) {
			continue
		}

		linePaths := readOnlySourceLikePaths(sourceLikePathsFromText(line, max), max)

		paths = textutil.Merge(paths, linePaths)

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
		"these": {}, "those": {}, "they": {}, "them": {}, "their": {},
	}
	seen := map[string]struct{}{}

	var terms []string

	for _, word := range words {
		word = SingularObjectiveWord(strings.TrimSpace(word))
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

	terms := RecoveryObjectiveSearchWords(objective, 32)
	if len(terms) == 0 {
		return false
	}

	seenTerms := map[string]struct{}{}

	var objectiveTerms []string

	for _, term := range terms {
		term = SingularObjectiveWord(term)
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
		term = ReadOnlyObjectiveSupportTerm(term)
		if term == "" || weakSourceOwnerObjectiveTerm(term) {
			continue
		}

		filtered = append(filtered, term)
	}

	return textutil.Sorted(filtered)
}

func implementationShapeSourceBodyCanClose(objective string, body string) bool {
	if !ObjectiveAsksImplementationShape(objective) {
		return false
	}

	return readOnlySourceOwnerEvidenceForBody("", "", objective, body).OwnerEligible()
}

func weakSourceOwnerObjectiveTerm(term string) bool {
	switch ReadOnlyObjectiveSupportTerm(term) {
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

	citations := analyzeSourceCitations(output)
	objectiveText := citations.textWithoutPathCitations()
	mechanismText := strings.ToLower(sourceCitationTextWithoutPathTokens(objectiveText))

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
	rawTerms := RecoveryObjectiveSearchWords(objective, 32)
	if len(rawTerms) == 0 {
		return nil
	}

	seen := map[string]struct{}{}

	var terms []string

	for _, term := range rawTerms {
		term = SingularObjectiveWord(strings.TrimSpace(term))
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
	term = SingularObjectiveWord(strings.TrimSpace(term))
	if term == "" {
		return true
	}

	if _, skip := RecoverySearchStopWords()[term]; skip {
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
	term = SingularObjectiveWord(strings.TrimSpace(term))

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
	if l.planArtifactAnswerable() {
		return true
	}

	if l.directAnswerConversation() && ObjectiveAsksImplementationShape(l.Objective) {
		return l.diagnosticEvidenceAnswerable() || (len(l.ImplementationOwnerPaths()) > 0 && len(l.MissingNamedSourcePaths) == 0)
	}

	if l.diagnosticEvidenceAnswerable() {
		return true
	}

	if len(l.RelevantExternalDocumentPaths) > 0 && len(l.MissingNamedSourcePaths) == 0 {
		return true
	}

	// When the question is about architecture/structure and there are unread
	// candidate paths, the model should read those candidates before answering.
	if l.GoalRequiresStructureDiscovery && l.HasUnreadCandidatePaths {
		return false
	}

	return len(l.RelevantSourceBodyPaths) > 0 && len(l.MissingNamedSourcePaths) == 0
}

func (l readOnlyAnswerabilityLedger) planArtifactAnswerable() bool {
	return l.PlanArtifactRelevant && len(l.PlanArtifactPaths) > 0 && len(l.MissingNamedSourcePaths) == 0
}

func (l readOnlyAnswerabilityLedger) ImplementationOwnerPaths() []string {
	var paths []string

	objectiveSet := readOnlyPathSet(l.SourceBodyObjectivePaths)

	for _, evidence := range l.SourceOwnerEvidence {
		path := NormalizeRunnerPath(evidence.Path)
		if path == "" || !evidence.OwnerEligible() {
			continue
		}

		if len(objectiveSet) > 0 {
			if _, ok := objectiveSet[path]; !ok {
				continue
			}
		}

		paths = textutil.Merge(paths, []string{path})
	}

	return readOnlySourceLikePaths(paths, 16)
}

func (l readOnlyAnswerabilityLedger) SourceOwnerEvidenceForPath(path string) (readOnlySourceOwnerEvidence, bool) {
	path = NormalizeRunnerPath(path)
	if path == "" {
		return readOnlySourceOwnerEvidence{}, false
	}

	for _, evidence := range l.SourceOwnerEvidence {
		if NormalizeRunnerPath(evidence.Path) == path {
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
		paths = textutil.Merge(paths, readOnlySourceLikePaths(group, 64))
	}

	return paths
}

func readOnlyObjectiveImpliesArchitecture(objective string) bool {
	lower := strings.ToLower(strings.Join(strings.Fields(objective), " "))
	return ContainsAny(lower,
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
		if readOnlyPathEscapesWorkspace(raw) {
			continue
		}
		path := NormalizeRunnerPath(raw)
		if !readOnlyRepoLocalSourcePath(path) {
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

func readOnlyRepoLocalSourcePath(path string) bool {
	path = NormalizeRunnerPath(path)
	if path == "" || path == "." || path == ".." {
		return false
	}
	if filepath.IsAbs(path) || strings.HasPrefix(path, "../") || strings.HasPrefix(path, `..\`) {
		return false
	}
	if IsNonSourceEvidenceArtifactPath(path) || !pathparse.HasWorkspaceFileExtension(path) {
		return false
	}

	return true
}

func readOnlyPathEscapesWorkspace(raw string) bool {
	path := strings.TrimSpace(raw)
	path = strings.Trim(path, "`'\"()[]{}<>,.;")
	if path == "" {
		return false
	}
	if filepath.IsAbs(path) || filepath.VolumeName(path) != "" || pathparse.LooksLikeWindowsAbsolutePath(path) {
		return true
	}

	path = filepath.ToSlash(strings.ReplaceAll(path, `\`, `/`))
	return path == ".." || strings.HasPrefix(path, "../")
}

func readOnlyPathSet(paths []string) map[string]struct{} {
	set := make(map[string]struct{}, len(paths))
	for _, path := range readOnlySourceLikePaths(paths, 0) {
		set[path] = struct{}{}
	}

	return set
}

func normalizeReadOnlyExternalDocumentPath(path string) string {
	path = strings.TrimSpace(path)
	path = strings.Trim(path, "`'\"()[]{}<>,;")
	if path == "" {
		return ""
	}

	if filepath.IsAbs(path) || filepath.VolumeName(path) != "" || pathparse.LooksLikeWindowsAbsolutePath(path) {
		return filepath.ToSlash(filepath.Clean(path))
	}

	return ""
}

func readOnlyExternalDocumentBodyPath(path string) bool {
	path = normalizeReadOnlyExternalDocumentPath(path)
	if path == "" {
		return false
	}

	if readOnlyExternalDocumentArtifactPath(path) || !pathparse.HasWorkspaceFileExtension(path) {
		return false
	}

	return true
}

func readOnlyExternalDocumentArtifactPath(path string) bool {
	lower := strings.ToLower(filepath.ToSlash(strings.TrimSpace(path)))
	if lower == "" {
		return false
	}

	return strings.Contains(lower, "/.idiom/") ||
		strings.Contains(lower, "/idiom/logs/") ||
		strings.Contains(lower, "/logs/") ||
		strings.Contains(lower, "/.codex/") ||
		strings.Contains(lower, "/codex/discovery/") ||
		strings.Contains(lower, "/transcripts/") ||
		strings.Contains(lower, "/testdata/swe/generated-logs/")
}

func readOnlyExternalDocumentPathSet(paths []string) map[string]struct{} {
	set := make(map[string]struct{}, len(paths))
	for _, raw := range paths {
		path := normalizeReadOnlyExternalDocumentPath(raw)
		if path == "" {
			continue
		}
		set[path] = struct{}{}
	}
	return set
}

func readOnlyDiagnosticEvidenceMatchesObjective(objective string, namedTargets []string, text string, paths []string) bool {
	objective = strings.TrimSpace(objective)
	paths = readOnlySourceLikePaths(paths, 8)

	if objective == "" || len(paths) == 0 {
		return false
	}

	namedSet := readOnlyPathSet(namedTargets)
	for _, path := range paths {
		if _, ok := namedSet[NormalizeRunnerPath(path)]; ok {
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

	if sourceBodyHasStrongObjectiveToken(text, objective) {
		return true
	}

	return readOnlyObjectiveExplicitlyAsksForDiagnostics(objective)
}

func readOnlyObjectiveExplicitlyAsksForDiagnostics(objective string) bool {
	lower := strings.ToLower(strings.TrimSpace(objective))
	if lower == "" {
		return false
	}

	return ContainsAny(lower,
		"diagnostic", "diagnostics",
		"source_sanity",
		"compiler error", "compile error",
	)
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

func looksLikeReadOnlyReadinessNudgeEcho(answer string) bool {
	normalized := strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(answer)), " "))
	if normalized == "" {
		return false
	}

	if strings.HasPrefix(normalized, "the read-only evidence is sufficient for a grounded answer to this question") {
		return true
	}

	return ContainsAny(normalized,
		"why a final answer is now valid:",
		"a final answer is available unless one exact unresolved claim remains",
		"if one more source read would materially change the answer",
		"core evidence paths:",
	) && ContainsAny(normalized,
		"answer_readiness_reached",
		"read-only evidence is sufficient",
		"post-readiness",
		"bounded follow-up through the normal tool policy",
	)
}

func contextDependentSourceFooter(lower string) bool {
	normalized := strings.Join(strings.Fields(lower), " ")
	if normalized == "" {
		return false
	}

	if !ContainsAny(normalized, "the answer above", "the previous answer", "the prior answer") {
		return false
	}

	return ContainsAny(normalized,
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

	return ContainsAny(lower,
		"available evidence is not enough",
		"could not produce a supported final answer",
		"do not have a supported answer",
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

		calls = textutil.AppendUnique(calls, candidate)
		if len(calls) >= max {
			return calls
		}
	}

	return calls
}

func extractExactProbeAnchor(result workspace.ToolResult) (string, string) {
	summary := strings.TrimSpace(result.Summary)
	tool := strings.TrimSpace(result.Tool)
	switch {
	case languagetools.ToolHasAnyCapability(tool, semanticlanguage.CapReadSymbol):
		return extractReadExactProbeAnchor(summary)
	case languagetools.ToolHasAnyCapability(tool, semanticlanguage.CapExtractBody):
		if rest, ok := strings.CutPrefix(summary, "Extracted "); ok {
			if symbol, remainder, found := strings.Cut(rest, " in "); found {
				return extractExactProbePath(remainder), strings.TrimSpace(symbol)
			}
		}
	case tool == workspace.ToolNameCodeNavigate || tool == workspace.ToolNameReadOnlyCapability:
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

		symbols = textutil.AppendUnique(symbols, raw)
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

		record := newToolResultEvidenceRecord(result)
		if record.candidateAnchorOnly {
			continue
		}

		if len(record.SourceBodyPaths()) > 0 {
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

	for _, term := range RecoveryObjectiveSearchWords(objective, 32) {
		term = SingularObjectiveWord(strings.ToLower(strings.TrimSpace(term)))
		if len(term) < 4 {
			continue
		}

		if strings.Contains(text, term) {
			return true
		}
	}

	return false
}

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

type ReadOnlyCloseout struct {
	Brief    workerruntime.WorkerRuntimeBrief
	Results  []workspace.ToolResult
	State    ReadOnlyCloseoutState
	Reason   ReadOnlyCloseoutReason
	Evidence readOnlyAnswerabilityLedger
}

func readOnlyClosureDecisionForResults(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) ReadOnlyCloseout {
	return readOnlyClosureDecisionForProjection(brief, newToolResultEvidenceProjection(results))
}

func readOnlyClosureDecisionForProjection(brief workerruntime.WorkerRuntimeBrief, projection toolResultEvidenceProjection) ReadOnlyCloseout {
	brief = workerruntime.NormalizeRuntimeBrief(brief)

	evidence := readOnlyAnswerabilityForProjection(brief, projection)

	if evidence.Answerable() {
		return ReadOnlyCloseout{
			Brief:    brief,
			Results:  append([]workspace.ToolResult(nil), projection.results...),
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
		Results:  append([]workspace.ToolResult(nil), projection.results...),
		State:    readOnlyClosureNotReady,
		Reason:   reason,
		Evidence: evidence,
	}
}

func (projection toolResultEvidenceProjection) postChildPacketClosureReady(brief workerruntime.WorkerRuntimeBrief) bool {
	if runnerBriefRequiresMutation(brief) || runnerVerificationRequired(brief) {
		return false
	}
	return projection.hasAnswerableDelegatedPacket(brief) &&
		readOnlyClosureDecisionForProjection(brief, projection).Answerable()
}

func (d ReadOnlyCloseout) Answerable() bool {
	return d.State == readOnlyClosureAnswerReady
}

func (d ReadOnlyCloseout) CandidateOnlyEvidence() bool {
	evidence := d.Evidence
	return len(evidence.CandidatePaths) > 0 &&
		len(evidence.SourceBodyPaths) == 0 &&
		len(evidence.ExternalDocumentBodyPaths) == 0 &&
		!evidence.DiagnosticEvidence
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
	inspectedPaths := readOnlyEvidenceDisplayPaths(
		textutil.Merge(d.Evidence.SourceBodyPaths, d.Evidence.ExternalDocumentBodyPaths, d.Evidence.DiagnosticPaths),
		6,
	)
	var anchors []string

	for _, result := range d.Results {
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

		for _, path := range textutil.Prefix(inspectedPaths, 6) {
			b.WriteString("- `")
			b.WriteString(path)
			b.WriteString("`\n")
		}
	}

	if len(anchors) > 0 {
		b.WriteString("\nSource anchors:\n")

		for _, anchor := range textutil.Prefix(anchors, 6) {
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

	if externalDocument, ok := presentableExternalDocumentEvidenceAnswer(d.Brief, d.Results); ok {
		return externalDocument, true
	}

	if sourceBody, ok := presentableReadFileSourceAnswer(d.Brief, d.Results); ok {
		return sourceBody, true
	}

	if prose, ok := presentableProseEvidenceAnswer(d.Brief, d.Results); ok {
		return prose, true
	}

	return "", false
}

func (d ReadOnlyCloseout) RecoverableTerminalCandidate(agentName string, assessment turnContractAssessment) (string, bool) {
	if !turnContractAssessmentCapabilities(assessment).Recoverable {
		return "", false
	}

	brief := workerruntime.NormalizeRuntimeBrief(d.Brief)
	if !readOnlyDirectAnswerLane(brief) || runnerBriefRequiresMutation(brief) || runnerVerificationRequired(brief) {
		return "", false
	}

	recovered, ok := d.ConcreteFallbackAnswer()
	if !ok {
		return "", false
	}

	return d.PresentableObjectiveAnswerCandidate(agentName, recovered)
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

func acceptedFinalAnswerEvidencePaths(answer string, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) []string {
	brief = workerruntime.NormalizeRuntimeBrief(brief)
	if runnerBriefRequiresMutation(brief) {
		if paths := newToolResultEvidenceProjection(results).acceptedImplementationEvidencePaths(); len(paths) > 0 {
			return paths
		}
	}

	return readOnlyClosureDecisionForResults(brief, results).AcceptedAnswerEvidencePaths(answer)
}

func (d ReadOnlyCloseout) AcceptedAnswerEvidencePaths(answer string) []string {
	brief := workerruntime.NormalizeRuntimeBrief(d.Brief)
	if readOnlyDirectAnswerLane(brief) {
		decision := readOnlyClosureDecisionForResults(readOnlyDirectAnswerAcceptanceLedgerBrief(brief), d.Results).
			FinalAnswerDecision(answer)

		return append([]string(nil), decision.AcceptedEvidencePaths...)
	}

	projection := newToolResultEvidenceProjection(d.Results)
	if readOnlyExplanationGateApplies(brief) {
		paths := textutil.Merge(projection.exactProbeEvidencePaths(), pathparse.MentionedWorkspacePaths(answer))
		paths = textutil.Merge(paths, projection.acceptedDelegatedReadOnlyEvidencePaths(brief.Objective))

		paths = textutil.Merge(paths, projection.delegatedReadOnlyInspectedImplementationPaths())
		if len(paths) == 0 {
			paths = textutil.Merge(paths, projection.MentionedEvidencePathsForObjective(brief.Objective, 24))
		}

		paths = projection.prioritizedClosureEvidencePaths(paths, 8)
		paths = closurePromptEvidencePaths(paths, 8)

		if len(paths) > 0 {
			return paths
		}
	}

	paths := textutil.Merge(projection.pinnedPaths(8), pathparse.MentionedWorkspacePaths(answer))
	paths = textutil.Merge(paths, projection.acceptedDelegatedReadOnlyEvidencePaths(brief.Objective))
	paths = delegatedDiscoveryAnswerEvidencePaths(brief.Objective, paths)

	return closurePromptEvidencePaths(paths, 8)
}

func (d ReadOnlyCloseout) FinalAnswerDecision(answer string) readOnlyFinalAnswerDecision {
	brief := workerruntime.NormalizeRuntimeBrief(d.Brief)
	if !readOnlyDirectAnswerLane(brief) {
		return readOnlyFinalAnswerDecision{}
	}

	objective := strings.TrimSpace(brief.Objective)
	if objective == "" {
		return readOnlyFinalAnswerDecision{Status: "skipped_empty_objective"}
	}

	substantiveAnswer := SubstantiveAnswerWithoutEchoedObjective(answer, objective)
	ledger := d.Evidence
	answerSupportedPaths := answerCitedCollectiveSourceBodySupportPaths(answer, ledger)

	if len(answerSupportedPaths) > 0 {
		ledger.SourceBodyObjectivePaths = textutil.Merge(ledger.SourceBodyObjectivePaths, answerSupportedPaths)
		ledger.RelevantSourceBodyPaths = textutil.Merge(ledger.RelevantSourceBodyPaths, answerSupportedPaths)
	}

	decision := readOnlyFinalAnswerDecision{
		Status:                  "checked",
		RelevantSourceBodyPaths: ledger.RelevantSourceBodyPaths,
		AnswerObjectiveTerms:    answerObjectiveTerms(substantiveAnswer, objective),
	}
	if readOnlyAnswerReportsInsufficientEvidence(substantiveAnswer) {
		decision.Status = string(turnReasonInsufficientEvidenceAnswer)
		if ledger.Answerable() {
			decision.BlockerKind = answerObjectiveUnmetInsufficientEvidenceClaimed
			decision.Reason = "the final answer says the evidence is insufficient even though the gathered source evidence appears answerable for the user's objective. This objective-relevance diagnostic is advisory."

			return decision
		}

		if len(ledger.SourceBodyPaths) == 0 && len(ledger.ExternalDocumentBodyPaths) == 0 && !ledger.DiagnosticEvidence {
			decision.BlockerKind = "answer_objective_unmet:no_source_body_evidence"
			decision.Reason = "the final answer says the evidence is insufficient before source-body or diagnostic evidence for the user's objective is present. This objective-relevance diagnostic is advisory."

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
		(len(d.Brief.EvidencePaths) > 0 || len(ledger.SourceBodyPaths) > 0 || len(ledger.ExternalDocumentBodyPaths) > 0) &&
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
			decision.BlockerKind = "answer_objective_unmet:citation_mismatch"
			decision.Reason = "the final answer uses inspected source evidence, but that evidence does not establish the user's objective. This objective-relevance diagnostic is advisory."

			return decision
		}

		decision.Status = "skipped_not_answerable"

		return decision
	}

	if answerAddressesObjective(substantiveAnswer, objective, ledger, d.Results) && answerSourceCitationsAreObjectiveRelevant(answer, ledger) {
		decision.AcceptedEvidencePaths = readOnlyAcceptedDirectAnswerEvidencePaths(answer, ledger, d.Results)
		return decision
	}

	if readOnlyLowConfidenceTermMatchAccepted(substantiveAnswer, objective, ledger, d.Results) {
		decision.Status = "checked_low_confidence_term_match"
		decision.AcceptedEvidencePaths = readOnlyAcceptedDirectAnswerEvidencePaths(answer, ledger, d.Results)
		decision.AnswerObjectiveTerms = readOnlySharedObjectiveSupportTerms(substantiveAnswer, objective)
		return decision
	}

	decision.BlockerKind = "answer_objective_unmet:term_mismatch"
	decision.Reason = "the final answer is grounded in inspected source evidence, but it does not address the user's objective. This objective-relevance diagnostic is advisory."

	return decision
}

func readOnlyLowConfidenceTermMatchAccepted(answer string, objective string, ledger readOnlyAnswerabilityLedger, results []workspace.ToolResult) bool {
	if !ledger.Answerable() || answerLacksObjectiveSourceEvidence(answer, ledger) {
		return false
	}

	if !answerSourceCitationsAreObjectiveRelevant(answer, ledger) {
		return false
	}

	if len(readOnlyAcceptedDirectAnswerEvidencePaths(answer, ledger, results)) == 0 {
		return false
	}

	if readOnlyAnswerHasHardNonFinalProse(answer) {
		return false
	}

	supportTerms := readOnlySharedObjectiveSupportTerms(answer, objective)
	if readOnlyScaffoldFinalAnswer(answer) && len(supportTerms) < 2 {
		return false
	}

	return len(supportTerms) > 0
}

func readOnlyAnswerHasHardNonFinalProse(answer string) bool {
	return looksLikeLowValueSourceInventoryNonAnswer(answer) ||
		looksLikeNonAnswerProseCore(answer)
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

	substantive := SubstantiveAnswerWithoutEchoedObjective(answer, objective)

	return answerAddressesObjective(substantive, objective, ledger, d.Results) &&
		answerSourceCitationsAreObjectiveRelevant(answer, ledger)
}

func answerAddressesObjective(answer string, objective string, ledger readOnlyAnswerabilityLedger, results []workspace.ToolResult) bool {
	answer = strings.TrimSpace(SubstantiveAnswerWithoutEchoedObjective(answer, objective))
	if answer == "" {
		return false
	}

	if sourceBodyHasStrongObjectiveToken(answer, objective) {
		return true
	}

	if plainNamedFileContentRequest(objective) && answerCitesNamedObjectiveSource(answer, ledger, results) {
		return true
	}

	if ledger.planArtifactAnswerable() && answerAddressesPlanArtifactObjective(answer, objective, ledger) {
		return true
	}

	if readOnlyCauseIntent(objective) && len(ledger.SourceBodyObjectivePaths) == 0 {
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

func answerAddressesPlanArtifactObjective(answer string, objective string, ledger readOnlyAnswerabilityLedger) bool {
	if !ledger.planArtifactAnswerable() {
		return false
	}

	objectiveText := strings.ToLower(strings.Join(strings.Fields(objective), " "))
	if !objectiveAsksAboutActivePlan(objectiveText) {
		return false
	}

	mentioned := readOnlySourceLikePaths(analyzeSourceCitations(answer).MentionedPaths, 16)
	if len(mentioned) > 0 {
		return featurePlannerPlanPathCitationsAreRelevant(answer, mentioned, ledger)
	}

	return answerMentionsFeaturePlannerPlan(answer)
}

func answerMentionsInspectedCodeSymbol(answer string, results []workspace.ToolResult) bool {
	exactSymbols := ExactEvidenceSymbolSet(results)
	if len(exactSymbols) == 0 {
		return false
	}

	for _, symbol := range ExplicitAnswerCodeSymbols(answer) {
		key := NormalizedAnswerSymbol(symbol)
		if key == "" || ArchitectureNonSourceSymbolTerm(key) {
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

	for _, raw := range analyzeSourceCitations(answer).MentionedPaths {
		path := NormalizeRunnerPath(raw)
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
		if _, read := sourceSet[named]; !read && !newToolResultEvidenceProjection(results).HasSourceBody(named) {
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

		for _, source := range readOnlySourceLikePaths(newToolResultEvidenceRecord(result).ReadPaths(), 16) {
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
	answer = SubstantiveAnswerWithoutEchoedObjective(answer, objective)

	lower := strings.ToLower(answer)

	var matches []string

	for _, term := range readOnlySearchHitObjectiveTerms(objective) {
		if strings.Contains(lower, term) {
			matches = append(matches, term)
		}
	}

	return textutil.Sorted(matches)
}

func readOnlySharedObjectiveSupportTerms(answer string, objective string) []string {
	answer = sourceCitationTextWithoutPathTokens(
		analyzeSourceCitations(SubstantiveAnswerWithoutEchoedObjective(answer, objective)).textWithoutPathCitations(),
	)
	if strings.TrimSpace(answer) == "" || strings.TrimSpace(objective) == "" {
		return nil
	}

	var matches []string
	for _, term := range readOnlyObjectiveSupportTerms(objective) {
		if readOnlyObjectivePromotionTermInText(answer, term) {
			matches = append(matches, term)
		}
	}

	return textutil.Sorted(matches)
}

func readOnlyObjectiveSupportTerms(objective string) []string {
	rawTerms := RecoveryObjectiveSearchWords(objective, 32)
	if len(rawTerms) == 0 {
		return nil
	}

	seen := map[string]struct{}{}
	var terms []string

	for _, raw := range rawTerms {
		term := ReadOnlyObjectiveSupportTerm(raw)
		term = SingularObjectiveWord(term)
		if len(term) < 4 {
			continue
		}

		if readOnlyLowConfidenceObjectiveSupportStopWord(term) ||
			readOnlyCollectiveObjectiveSupportStopWord(term) ||
			readOnlyObjectivePromotionStopWord(term) ||
			sourceOwnerGenericIdentityTerm(term) {

			continue
		}

		if _, ok := seen[term]; ok {
			continue
		}

		seen[term] = struct{}{}
		terms = append(terms, term)
	}

	return textutil.Sorted(terms)
}

func readOnlyLowConfidenceObjectiveSupportStopWord(term string) bool {
	term = ReadOnlyObjectiveSupportTerm(term)
	if len(term) < 5 {
		return true
	}

	switch term {
	case "answer", "source", "file", "code", "user", "risk", "change",
		"request", "result", "finding", "objective", "workflow", "evidence":
		return true
	case "agent", "better", "generic", "good", "need", "option", "project":
		return true
	default:
		return false
	}
}

func answerSourceCitationsAreObjectiveRelevant(answer string, ledger readOnlyAnswerabilityLedger) bool {
	mentionedPaths := analyzeSourceCitations(answer).MentionedPaths
	if answerCitesRelevantExternalDocument(mentionedPaths, ledger) {
		return true
	}
	mentionsExternalDocument := answerMentionsExternalDocument(mentionedPaths, ledger.ExternalDocumentBodyPaths)

	mentioned := readOnlySourceLikePaths(mentionedPaths, 16)
	if len(mentioned) == 0 {
		return !mentionsExternalDocument
	}

	if ledger.planArtifactAnswerable() && featurePlannerPlanPathCitationsAreRelevant(answer, mentioned, ledger) {
		return true
	}

	relevantPaths := ledger.RelevantSourceBodyPaths
	if ledger.directAnswerConversation() && ObjectiveAsksImplementationShape(ledger.Objective) {
		relevantPaths = ledger.ImplementationOwnerPaths()
	}

	if readOnlyCauseIntent(ledger.Objective) {
		relevantPaths = ledger.SourceBodyObjectivePaths
	}

	relevantSet := readOnlyPathSet(relevantPaths)
	if len(relevantSet) == 0 {
		return len(ledger.SourceBodyPaths) == 0 && !mentionsExternalDocument
	}

	for _, path := range mentioned {
		if _, ok := relevantSet[NormalizeRunnerPath(path)]; ok {
			return true
		}
	}

	return false
}

func answerMentionsExternalDocument(mentionedPaths []string, externalDocumentPaths []string) bool {
	if len(mentionedPaths) == 0 || len(externalDocumentPaths) == 0 {
		return false
	}

	for _, path := range mentionedPaths {
		for _, externalPath := range externalDocumentPaths {
			if externalDocumentPathMatchesCitation(path, externalPath) {
				return true
			}
		}
	}

	return false
}

func answerCitesRelevantExternalDocument(mentionedPaths []string, ledger readOnlyAnswerabilityLedger) bool {
	if len(mentionedPaths) == 0 || len(ledger.RelevantExternalDocumentPaths) == 0 {
		return false
	}

	for _, path := range mentionedPaths {
		for _, relevantPath := range ledger.RelevantExternalDocumentPaths {
			if externalDocumentPathMatchesCitation(path, relevantPath) {
				return true
			}
		}
	}

	return false
}

func objectiveAsksBehaviorSummary(objective string) bool {
	lower := " " + strings.ToLower(strings.Join(strings.Fields(objective), " ")) + " "

	return ContainsAny(lower,
		" what does ",
		" what do ",
	)
}

func plainNamedFileContentRequest(objective string) bool {
	lower := strings.ToLower(strings.Join(strings.Fields(objective), " "))
	if !ContainsAny(lower, "what is in", "what's in", "show", "summarize", "summary of", "contents of", "list") {
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

	if len(readOnlySourceLikePaths(analyzeSourceCitations(answer).MentionedPaths, 16)) > 0 {
		return true
	}

	lower := strings.ToLower(strings.Join(strings.Fields(answer), " "))

	return ContainsAny(lower,
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

func (d ReadOnlyCloseout) PresentableObjectiveAnswerCandidate(agentName string, answer string) (string, bool) {
	answer, ok := d.PresentableFallbackCandidate(agentName, answer)
	if !ok || !d.presentableObjectiveAnswer(answer) {
		return "", false
	}

	return answer, true
}

func (d ReadOnlyCloseout) presentableObjectiveAnswer(answer string) bool {
	answer = strings.TrimSpace(answer)
	if looksLikeSourceInventoryNonAnswer(answer) {
		return false
	}

	return answer != "" &&
		!strings.HasPrefix(answer, "<<QUESTION>>") &&
		!looksLikeNonAnswerProse(answer) &&
		d.answerAddressesReadOnlyObjective(answer)
}

func (d ReadOnlyCloseout) answerAddressesReadOnlyObjective(answer string) bool {
	brief := workerruntime.NormalizeRuntimeBrief(d.Brief)
	objective := strings.TrimSpace(brief.Objective)
	if objective == "" {
		return true
	}

	if !workerruntime.PermissionProfileIsReadOnlyWorkspace(brief.PermissionProfile) {
		return true
	}

	if !workerruntime.RuntimeContractIsArchitectureOrReview(brief.Contract) &&
		!workerruntime.RuntimeContractMatches(brief.Contract, workerruntime.ContractGroundedEvidence) {

		return true
	}

	return answerAddressesObjective(answer, objective, d.Evidence, d.Results) &&
		answerSourceCitationsAreObjectiveRelevant(answer, d.Evidence)
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

	if !readOnlyFallbackCandidateAllowsRawPassthrough(d.Brief) {
		return "", false
	}

	return answer, true
}

func readOnlyFallbackCandidateAllowsRawPassthrough(brief workerruntime.WorkerRuntimeBrief) bool {
	brief = workerruntime.NormalizeRuntimeBrief(brief)
	if runnerBriefRequiresMutation(brief) || runnerVerificationRequired(brief) {
		return true
	}

	if !workerruntime.PermissionProfileIsReadOnlyWorkspace(brief.PermissionProfile) {
		return true
	}

	if readOnlyDirectAnswerLane(brief) ||
		workerruntime.RuntimeContractIsArchitectureOrReview(brief.Contract) ||
		workerruntime.RuntimeContractMatches(brief.Contract, workerruntime.ContractGroundedEvidence) {

		return false
	}

	return true
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

func readOnlyClosureKnownEvidencePaths(evidence readOnlyAnswerabilityLedger) []string {
	paths := textutil.Merge(evidence.CandidatePaths, evidence.SourceBodyPaths, evidence.ExternalDocumentBodyPaths, evidence.NamedSourcePaths)
	return readOnlyEvidenceDisplayPaths(paths, 8)
}

func readOnlyEvidenceDisplayPaths(paths []string, max int) []string {
	if max <= 0 {
		max = 8
	}

	out := closurePromptEvidencePaths(paths, max)
	seen := map[string]struct{}{}
	for _, path := range out {
		seen[path] = struct{}{}
	}

	for _, raw := range paths {
		path := normalizeReadOnlyExternalDocumentPath(raw)
		if !readOnlyExternalDocumentBodyPath(path) {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}

		seen[path] = struct{}{}
		out = append(out, path)
		if len(out) >= max {
			break
		}
	}

	return out
}

func readOnlyEvidenceLedgerGuidanceForBrief(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) string {
	brief = workerruntime.NormalizeRuntimeBrief(brief)
	if !workerruntime.PermissionProfileIsReadOnlyWorkspace(brief.PermissionProfile) {
		return ""
	}

	ledger := readOnlyAnswerabilityForResults(readOnlyDirectAnswerAcceptanceLedgerBrief(brief), results)
	var parts []string

	if paths := closurePromptEvidencePaths(ledger.RelevantSourceBodyPaths, 3); len(paths) > 0 {
		parts = append(parts, "body-backed source evidence: "+formatInlinePathList(paths, 3))
	} else if paths := closurePromptEvidencePaths(ledger.SourceBodyPaths, 3); len(paths) > 0 {
		parts = append(parts, "source bodies inspected: "+formatInlinePathList(paths, 3))
	}

	if paths := readOnlyEvidenceDisplayPaths(ledger.RelevantExternalDocumentPaths, 3); len(paths) > 0 {
		parts = append(parts, "body-backed external document evidence: "+formatInlinePathList(paths, 3))
	} else if paths := readOnlyEvidenceDisplayPaths(ledger.ExternalDocumentBodyPaths, 3); len(paths) > 0 {
		parts = append(parts, "external documents inspected: "+formatInlinePathList(paths, 3))
	}

	if ledger.DiagnosticRelevant {
		if paths := closurePromptEvidencePaths(ledger.DiagnosticPaths, 2); len(paths) > 0 {
			parts = append(parts, "diagnostic evidence: "+formatInlinePathList(paths, 2))
		} else {
			parts = append(parts, "diagnostic evidence is present")
		}
	}

	if paths := readOnlyCandidateOnlyPaths(ledger, 3); len(paths) > 0 {
		parts = append(parts, "candidate-only leads: "+formatInlinePathList(paths, 3))
	}

	if paths := closurePromptEvidencePaths(ledger.MissingNamedSourcePaths, 3); len(paths) > 0 {
		parts = append(parts, "missing named source-body evidence: "+formatInlinePathList(paths, 3))
	}

	if len(parts) == 0 {
		return ""
	}

	return "Evidence ledger: " + strings.Join(parts, "; ") + ". Source-body and diagnostic entries can support a final answer; candidate/search/context entries are leads until read as body evidence or explicitly qualified as unverified."
}

func readOnlyCandidateOnlyPaths(ledger readOnlyAnswerabilityLedger, max int) []string {
	if max <= 0 {
		max = 3
	}

	bodySet := readOnlyPathSet(ledger.SourceBodyPaths)
	diagnosticSet := readOnlyPathSet(ledger.DiagnosticPaths)
	var out []string

	for _, candidate := range readOnlySourceLikePaths(ledger.CandidatePaths, 64) {
		normalized := NormalizeRunnerPath(candidate)
		if normalized == "" {
			continue
		}
		if _, ok := bodySet[normalized]; ok {
			continue
		}
		if _, ok := diagnosticSet[normalized]; ok {
			continue
		}

		out = textutil.AppendUnique(out, normalized)
		if len(out) >= max {
			return out
		}
	}

	return out
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

	return textutil.Merge(priority, rest)
}

func presentableUnsupportedFinalAnswerLead() string {
	return "I do not have a supported answer from this run."
}

func unsupportedReadOnlyCloseoutAnswer(answer string) bool {
	lower := strings.ToLower(strings.TrimSpace(answer))
	if lower == "" {
		return false
	}

	return strings.HasPrefix(lower, strings.ToLower(presentableUnsupportedFinalAnswerLead()))
}

func indexFold(value string, needle string) int {
	if needle == "" || len(value) < len(needle) {
		return -1
	}

	for i := 0; i+len(needle) <= len(value); i++ {
		if strings.EqualFold(value[i:i+len(needle)], needle) {
			return i
		}
	}

	return -1
}

func cleanDelegatedReadOnlyFindingLine(line string) string {
	line = strings.TrimSpace(line)
	for {
		lower := strings.ToLower(line)
		stripped := false

		for _, label := range delegatedReadOnlyPacketLineLabels() {
			if !strings.HasPrefix(lower, label) {
				continue
			}

			line = strings.TrimSpace(line[len(label):])
			stripped = true

			break
		}

		if !stripped {
			return line
		}
	}
}

func isGroundedChildPacketEvidencePath(objective string, path string) bool {
	path = strings.ToLower(filepath.ToSlash(strings.TrimSpace(normalizeDelegatedDiscoveryEvidencePath(path))))
	if path == "" {
		return false
	}

	switch {
	case strings.HasPrefix(path, ".idiom/"),
		strings.HasPrefix(path, "idiom/"),
		strings.HasPrefix(path, "logs/"),
		strings.HasPrefix(path, "transcripts/"),
		strings.HasPrefix(path, "testdata/swe/fixtures/"),
		strings.Contains(path, "/testdata/swe/fixtures/"):
		return false
	}

	base := filepath.Base(path)
	if allowsDocumentationOnlyPacket(objective) && (strings.HasSuffix(path, ".md") || base == "readme.md") {
		return true
	}

	switch base {
	case "go.mod", "go.sum", "go.work", "makefile", "package.json", "cargo.toml", "pyproject.toml", "pom.xml", "build.gradle", "build.gradle.kts":
		return true
	}

	switch {
	case strings.HasSuffix(path, ".go"),
		strings.HasSuffix(path, ".rs"),
		strings.HasSuffix(path, ".py"),
		strings.HasSuffix(path, ".ts"),
		strings.HasSuffix(path, ".tsx"),
		strings.HasSuffix(path, ".js"),
		strings.HasSuffix(path, ".jsx"),
		strings.HasSuffix(path, ".java"),
		strings.HasSuffix(path, ".c"),
		strings.HasSuffix(path, ".cc"),
		strings.HasSuffix(path, ".cpp"),
		strings.HasSuffix(path, ".cxx"),
		strings.HasSuffix(path, ".h"),
		strings.HasSuffix(path, ".hpp"),
		strings.HasSuffix(path, ".m"),
		strings.HasSuffix(path, ".mm"),
		strings.HasSuffix(path, ".json"),
		strings.HasSuffix(path, ".yaml"),
		strings.HasSuffix(path, ".yml"),
		strings.HasSuffix(path, ".toml"),
		strings.HasSuffix(path, ".sh"),
		strings.HasSuffix(path, ".sql"),
		strings.HasSuffix(path, ".proto"):
		return true
	}

	return strings.HasPrefix(path, "internal/") ||
		strings.HasPrefix(path, "cmd/") ||
		strings.HasPrefix(path, "pkg/") ||
		strings.HasPrefix(path, "src/") ||
		strings.HasPrefix(path, "scripts/")
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

func isHarnessRejectionErrorText(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}

	return ContainsAny(lower,
		"payload was tool-shaped",
		"unsafe to infer as a tool call",
		"not an executable tool call",
		"emit exactly one valid tool call",
		"include `named_gap`",
		"include named_gap",
		"already contains sufficient evidence to bias toward answering",
		"file-level raw read is too broad",
		"file-level outline is broad orientation",
		"do not restart broad discovery",
		"successful write(s) since the last verification",
		"this action class already failed earlier in the turn",
		"not in the current allowed tool surface",
		"not available because the current runtime contract exposes no tools",
	)
}

func isHarnessInternalRejectionResult(result workspace.ToolResult) bool {
	if strings.TrimSpace(result.Tool) == parserRecoveryToolName {
		return true
	}

	return isHarnessRejectionSummary(result.Summary) || isHarnessRejectionErrorText(result.Error)
}

func isParserRecoveryRejectionSummary(summary string) bool {
	switch strings.TrimSpace(summary) {
	case parserRecoverySummaryMalformed,
		parserRecoverySummaryUnsafe:
		return true
	default:
		return false
	}
}

func isHarnessRejectionSummary(summary string) bool {
	summary = normalizedHarnessRejectionSummary(summary)
	if isParserRecoveryRejectionSummary(summary) {
		return true
	}
	if postReadinessContinuationRejectionSummary(summary) {
		return true
	}

	switch summary {
	case "Post-child reread blocked",
		"Broad exploration after delegation nudge blocked",
		"Additional read_only child task blocked",
		"Broad inspection request rejected",
		"Verification required after workspace edits",
		"Tool not available in current runtime contract",
		"Repeated dead-end action rejected",
		"Chained list_files rejected",
		"Chained search rejected":
		return true
	}

	return false
}

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

func finalAnswerSectionMarkerCount(text string) int {
	count := 0

	for _, line := range strings.Split(text, "\n") {
		if _, ok := finalAnswerSectionMarkerRest(line); ok {
			count++
		}
	}

	return count
}

func compactOutput(output string, maxLines int) string {
	output = stripFallbackJSONArtifacts(output)

	lines := strings.Split(strings.TrimSpace(output), "\n")

	if len(lines) == 0 || lines[0] == "" {
		return ""
	}

	if maxLines <= 0 || len(lines) <= maxLines {
		return strings.Join(lines, "\n")
	}

	return strings.Join(lines[:maxLines], "\n") + "\n..."
}

const readOnlyEvidencePacketPayloadType = "agent.read_only_evidence_packet.v1"

const ReadOnlyEvidencePacketSchema = readOnlyEvidencePacketPayloadType

type ReadOnlyEvidenceFinding struct {
	Path   string `json:"path,omitempty"`
	Text   string `json:"text,omitempty"`
	Source string `json:"source,omitempty"`
}

type ReadOnlyEvidencePacket struct {
	SchemaVersion   string                    `json:"schema_version"`
	Objective       string                    `json:"objective,omitempty"`
	Question        string                    `json:"question,omitempty"`
	SourceBodyPaths []string                  `json:"source_body_paths,omitempty"`
	InspectedPaths  []string                  `json:"inspected_paths,omitempty"`
	CandidatePaths  []string                  `json:"candidate_paths,omitempty"`
	Findings        []ReadOnlyEvidenceFinding `json:"findings,omitempty"`
	Gaps            []string                  `json:"gaps,omitempty"`
	Confidence      string                    `json:"confidence,omitempty"`
}

func buildReadOnlyEvidencePacket(objective string, answer string, results []workspace.ToolResult) (ReadOnlyEvidencePacket, bool) {
	objective = strings.TrimSpace(objective)

	if readOnlyToolControlGuidanceText(answer) {
		answer = ""
	}

	answer = sanitizeReadOnlyEvidencePacketAnswer(answer)

	projection := newToolResultEvidenceProjection(results)
	sourceBodyPaths := readOnlyEvidencePacketSourceLikePaths(projection.SourceBodyPaths(), 16)
	candidatePaths := projection.ReadOnlyEvidencePacketCandidatePaths()
	citations := analyzeSourceCitations(answer)
	inspectedPaths := readOnlyEvidencePacketSourceLikePaths(textutil.Merge(sourceBodyPaths, candidatePaths, citations.MentionedPaths), 16)
	findings := readOnlyEvidencePacketFindings(answer, results, sourceBodyPaths, 6)
	packet := ReadOnlyEvidencePacket{
		SchemaVersion:   readOnlyEvidencePacketPayloadType,
		Objective:       objective,
		Question:        delegatedDisplayQuestion(objective),
		SourceBodyPaths: sourceBodyPaths,
		InspectedPaths:  inspectedPaths,
		CandidatePaths:  candidatePaths,
		Findings:        findings,
		Gaps:            readOnlyEvidencePacketGaps(answer, results),
		Confidence:      readOnlyEvidencePacketConfidence(answer, sourceBodyPaths, findings),
	}

	packet = normalizeReadOnlyEvidencePacket(packet)

	return packet, readOnlyEvidencePacketHasContent(packet)
}

func sanitizeReadOnlyEvidencePacketAnswer(answer string) string {
	if !readOnlyEvidencePacketPartialNonAnswer(answer) {
		return answer
	}

	var kept []string

	for _, raw := range strings.Split(answer, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || readOnlyEvidencePacketTransportLine(line) {
			continue
		}

		kept = append(kept, raw)
	}

	return strings.TrimSpace(strings.Join(kept, "\n"))
}

func readOnlyEvidencePacketPartialNonAnswer(answer string) bool {
	lower := strings.ToLower(strings.TrimSpace(answer))
	return strings.HasPrefix(lower, "partial findings from this turn")
}

func readOnlyEvidencePacketToolResult(result workspace.ToolResult, packet ReadOnlyEvidencePacket) workspace.ToolResult {
	packet = normalizeReadOnlyEvidencePacket(packet)

	payload, err := json.Marshal(packet)

	if err != nil || !readOnlyEvidencePacketHasContent(packet) {
		return result
	}

	result.PayloadType = readOnlyEvidencePacketPayloadType
	result.Payload = payload

	return result
}

func readOnlyEvidencePacketFromResult(result workspace.ToolResult) (ReadOnlyEvidencePacket, bool) {
	if strings.TrimSpace(result.PayloadType) != readOnlyEvidencePacketPayloadType || len(result.Payload) == 0 {
		return ReadOnlyEvidencePacket{}, false
	}

	var packet ReadOnlyEvidencePacket

	if err := json.Unmarshal(result.Payload, &packet); err != nil {
		return ReadOnlyEvidencePacket{}, false
	}

	packet = normalizeReadOnlyEvidencePacket(packet)
	if !readOnlyEvidencePacketHasContent(packet) {
		return ReadOnlyEvidencePacket{}, false
	}

	return packet, true
}

func renderReadOnlyEvidencePacketForToolOutput(packet ReadOnlyEvidencePacket, answer string) string {
	packet = normalizeReadOnlyEvidencePacket(packet)

	var b strings.Builder

	if question := strings.TrimSpace(packet.Question); question != "" {
		b.WriteString("Question: ")
		b.WriteString(question)
		b.WriteString("\n")
	}

	if answer = strings.TrimSpace(stripDelegatedReadOnlyInternalPacket(answer)); answer != "" && !readOnlyToolControlGuidanceText(answer) {
		b.WriteString("Grounded answer:\n")
		b.WriteString(answer)
		b.WriteString("\n")
	}

	if len(packet.Findings) > 0 {
		b.WriteString("Grounded findings:\n")

		for _, finding := range packet.Findings {
			text := strings.TrimSpace(finding.Text)
			if text == "" {
				continue
			}

			b.WriteString("- ")
			b.WriteString(text)

			if path := strings.TrimSpace(finding.Path); path != "" && !strings.Contains(text, path) {
				b.WriteString(" (")
				b.WriteString(path)
				b.WriteString(")")
			}

			b.WriteString("\n")
		}
	}

	if len(packet.SourceBodyPaths) > 0 {
		b.WriteString("Source body paths:\n")

		for _, path := range packet.SourceBodyPaths {
			b.WriteString("- `")
			b.WriteString(path)
			b.WriteString("`\n")
		}
	}

	if len(packet.CandidatePaths) > 0 {
		b.WriteString("Candidate paths:\n")

		for _, path := range textutil.Prefix(packet.CandidatePaths, 4) {
			b.WriteString("- `")
			b.WriteString(path)
			b.WriteString("`\n")
		}
	}

	if confidence := strings.TrimSpace(packet.Confidence); confidence != "" {
		b.WriteString("Confidence: ")
		b.WriteString(confidence)
		b.WriteString("\n")
	}

	return strings.TrimSpace(b.String())
}

func synthesizeReadOnlyDelegatedEvidenceAnswer(objective string, facts []readOnlyDelegatedEvidenceFact) (string, bool) {
	var (
		sourcePaths []string
		findings    []ReadOnlyEvidenceFinding
	)

	for _, fact := range facts {
		decision := readOnlyDelegatedEvidenceDecisionForObjective(objective, fact)
		if !decision.TaskResultAnswerable {
			continue
		}

		packet := normalizeReadOnlyEvidencePacket(fact.Packet)
		sourcePaths = textutil.Merge(sourcePaths, decision.LedgerSourcePaths)

		for _, finding := range packet.Findings {
			if strings.TrimSpace(finding.Text) == "" {
				continue
			}

			if finding.Path != "" && !slices.Contains(decision.LedgerSourcePaths, finding.Path) {
				continue
			}

			findings = append(findings, finding)
		}
	}

	sourcePaths = readOnlyEvidencePacketSourceLikePaths(sourcePaths, 8)
	if len(sourcePaths) == 0 || len(findings) == 0 {
		return "", false
	}

	paths := closurePromptEvidencePaths(sourcePaths, 8)
	if len(paths) == 0 {
		return "", false
	}

	var b strings.Builder

	if question := delegatedDisplayQuestion(objective); question != "" {
		b.WriteString("For `")
		b.WriteString(question)
		b.WriteString("`, ")
	}

	seen := map[string]struct{}{}

	for _, finding := range findings {
		text := strings.TrimSpace(finding.Text)
		if text == "" {
			continue
		}

		if _, ok := seen[text]; ok {
			continue
		}

		seen[text] = struct{}{}
		if len(seen) > 1 {
			b.WriteString(" ")
		}

		b.WriteString(readOnlyEvidencePacketSentence(text))

		if len(seen) >= 4 {
			break
		}
	}

	if len(seen) == 0 {
		return "", false
	}

	b.WriteString(" This is grounded in ")
	b.WriteString(formatInlinePathList(paths, 4))
	b.WriteString(".")

	return strings.TrimSpace(b.String()), true
}

func readOnlyEvidencePacketSentence(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}

	switch text[len(text)-1:] {
	case ".", "!", "?":
		return text
	default:
		return text + "."
	}
}

func normalizeReadOnlyEvidencePacket(packet ReadOnlyEvidencePacket) ReadOnlyEvidencePacket {
	packet.SchemaVersion = readOnlyEvidencePacketPayloadType
	packet.Objective = strings.TrimSpace(packet.Objective)
	packet.Question = strings.TrimSpace(packet.Question)
	packet.Confidence = strings.TrimSpace(packet.Confidence)
	packet.SourceBodyPaths = readOnlyEvidencePacketSourceLikePaths(packet.SourceBodyPaths, 16)
	packet.InspectedPaths = readOnlyEvidencePacketSourceLikePaths(packet.InspectedPaths, 16)
	packet.CandidatePaths = readOnlyEvidencePacketSourceLikePaths(packet.CandidatePaths, 16)

	for i := range packet.Findings {
		findingPaths := readOnlyEvidencePacketSourceLikePaths([]string{packet.Findings[i].Path}, 1)
		if len(findingPaths) > 0 {
			packet.Findings[i].Path = findingPaths[0]
		} else {
			packet.Findings[i].Path = ""
		}

		packet.Findings[i].Text = strings.TrimSpace(packet.Findings[i].Text)
		packet.Findings[i].Source = strings.TrimSpace(packet.Findings[i].Source)
	}

	packet.Findings = readOnlyEvidencePacketUsefulFindings(packet.Findings)
	packet.Gaps = utilUniqueTrimmed(packet.Gaps, 8)

	return packet
}

func readOnlyEvidencePacketHasContent(packet ReadOnlyEvidencePacket) bool {
	return len(packet.SourceBodyPaths) > 0 || len(packet.InspectedPaths) > 0 || len(packet.CandidatePaths) > 0 || len(packet.Findings) > 0
}

func readOnlyEvidencePacketAnswerable(packet ReadOnlyEvidencePacket) bool {
	packet = normalizeReadOnlyEvidencePacket(packet)
	return len(packet.SourceBodyPaths) > 0 && len(packet.Findings) > 0
}

func readOnlyEvidencePacketAnswerableForObjective(packet ReadOnlyEvidencePacket, objective string) bool {
	packet = normalizeReadOnlyEvidencePacket(packet)
	if !readOnlyEvidencePacketAnswerable(packet) {
		return false
	}

	objective = strings.TrimSpace(objective)
	if objective == "" {
		return true
	}

	if ObjectiveAsksImplementationShape(objective) {
		return len(readOnlyEvidencePacketObjectiveSourceOwnerEvidence(packet, objective)) > 0
	}

	var findingText []string

	for _, finding := range packet.Findings {
		text := strings.TrimSpace(finding.Text)
		if text != "" {
			findingText = append(findingText, text)
		}
	}

	body := SubstantiveAnswerWithoutEchoedObjective(strings.Join(findingText, "\n"), objective)
	objectiveText := analyzeSourceCitations(body).textWithoutPathCitations()

	if readOnlyObjectiveAnswerEvidenceSupported(objectiveText, objective) {
		return true
	}

	if readOnlyEvidencePacketNamesObjectiveSource(packet, objective) &&
		readOnlyObjectiveAnswerEvidenceSupportedByNamedSource(body, objective) {

		return true
	}

	if readOnlyEvidencePacketHasObjectiveScopedSourceSupport(packet, objective, findingText) {
		return true
	}

	return false
}

func readOnlyEvidencePacketHasObjectiveScopedSourceSupport(packet ReadOnlyEvidencePacket, objective string, findingText []string) bool {
	if len(findingText) == 0 {
		return false
	}

	if ObjectiveAsksImplementationShape(objective) {
		return len(readOnlyEvidencePacketObjectiveSourceOwnerEvidence(packet, objective)) > 0
	}

	for _, path := range packet.SourceBodyPaths {
		body := readOnlyEvidencePacketFindingBodyForPath(packet, path, findingText)
		if readOnlyEvidencePacketSourcePathScopedToObjective(path, objective, body) &&
			sourceBodyHasStrongObjectiveToken(body, objective) {

			return true
		}
	}

	return false
}

func readOnlyEvidencePacketObjectiveSourceOwnerEvidence(packet ReadOnlyEvidencePacket, objective string) []readOnlySourceOwnerEvidence {
	packet = normalizeReadOnlyEvidencePacket(packet)

	if !ObjectiveAsksImplementationShape(objective) {
		return nil
	}

	var findingText []string

	for _, finding := range packet.Findings {
		text := strings.TrimSpace(finding.Text)
		if text != "" {
			findingText = append(findingText, text)
		}
	}

	if len(findingText) == 0 {
		return nil
	}

	var evidence []readOnlySourceOwnerEvidence

	for _, path := range packet.SourceBodyPaths {
		body := readOnlyEvidencePacketFindingBodyForPath(packet, path, findingText)
		if !readOnlyEvidencePacketSourcePathScopedToObjective(path, objective, body) {
			continue
		}

		owner := readOnlySourceOwnerEvidenceForBody(path, "", objective, body)
		if !owner.OwnerEligible() {
			continue
		}

		evidence = append(evidence, owner)
	}

	return evidence
}

func readOnlyEvidencePacketSourcePathScopedToObjective(path string, objective string, body string) bool {
	path = normalizeReadOnlyEvidencePacketPath(path)
	if path == "" {
		return false
	}

	return implementationSourcePathOverlapsObjective(path, objective) ||
		sourceEvidencePathSharesObjectiveTerm(path, objective) ||
		candidateSourcePathStronglyOverlapsObjective(path, objective) ||
		sourceBodyHasStrongObjectiveToken(body, objective)
}

func readOnlyEvidencePacketFindingBodyForPath(packet ReadOnlyEvidencePacket, path string, fallback []string) string {
	path = normalizeReadOnlyEvidencePacketPath(path)

	var body []string

	for _, finding := range packet.Findings {
		findingPath := normalizeReadOnlyEvidencePacketPath(finding.Path)
		if findingPath != "" && findingPath != path {
			continue
		}

		text := strings.TrimSpace(finding.Text)
		if text == "" {
			continue
		}

		body = append(body, text)
	}

	if len(body) == 0 {
		body = fallback
	}

	return strings.Join(body, "\n")
}

func readOnlyEvidencePacketNamesObjectiveSource(packet ReadOnlyEvidencePacket, objective string) bool {
	objective = strings.ToLower(strings.TrimSpace(objective))
	if objective == "" {
		return false
	}

	for _, path := range packet.SourceBodyPaths {
		normalized := strings.ToLower(normalizeReadOnlyEvidencePacketPath(path))
		if normalized == "" {
			continue
		}

		base := strings.ToLower(filepath.Base(normalized))
		stem := strings.TrimSuffix(base, filepath.Ext(base))

		if strings.Contains(objective, normalized) ||
			(base != "" && strings.Contains(objective, base)) ||
			(stem != "" && strings.Contains(objective, stem)) {

			return true
		}
	}

	return false
}

func readOnlyEvidencePacketFindings(answer string, results []workspace.ToolResult, sourceBodyPaths []string, limit int) []ReadOnlyEvidenceFinding {
	if limit <= 0 {
		return nil
	}

	answer = stripNestedFallbackTemplate(stripDelegatedReadOnlyInternalPacket(answer))

	var findings []ReadOnlyEvidenceFinding

	seen := map[string]struct{}{}
	add := func(path string, text string, source string) {
		if len(findings) >= limit {
			return
		}

		text = strings.TrimSpace(strings.TrimPrefix(text, "- "))
		if readOnlyEvidencePacketTransportLine(text) || looksLikeLineOrientedSourceSnippetLine(text) {
			return
		}

		if source == workspace.ToolNameReadFile && lineHasLeadingSourceLineNumber(text) {
			return
		}

		if !delegatedReadOnlyFindingLineIsUseful(text) {
			return
		}

		if path == "" {
			for _, mentioned := range readOnlySourceLikePaths(pathparse.MentionedWorkspacePaths(normalizeWorkspacePathScanText(text)), 4) {
				if slices.Contains(sourceBodyPaths, mentioned) {
					path = mentioned
					break
				}
			}
		}

		if path == "" && len(sourceBodyPaths) == 1 {
			path = sourceBodyPaths[0]
		}

		path = normalizeDelegatedDiscoveryEvidencePath(path)
		if path != "" && !slices.Contains(sourceBodyPaths, path) {
			return
		}

		key := path + "\x00" + text
		if _, ok := seen[key]; ok {
			return
		}

		seen[key] = struct{}{}

		findings = append(findings, ReadOnlyEvidenceFinding{Path: path, Text: text, Source: source})
	}

	for _, line := range delegatedReadOnlyFindingLines([]workspace.ToolResult{{Tool: "task", Output: answer}}, sourceBodyPaths, limit) {
		add("", line, "answer")
	}

	for _, result := range results {
		if len(findings) >= limit {
			break
		}

		if strings.TrimSpace(result.Error) != "" {
			continue
		}

		for _, line := range delegatedReadOnlyFindingLines([]workspace.ToolResult{result}, sourceBodyPaths, limit-len(findings)) {
			add("", line, strings.TrimSpace(result.Tool))
		}
	}

	return findings
}

func readOnlyEvidencePacketGaps(answer string, results []workspace.ToolResult) []string {
	var gaps []string

	answer = stripNestedFallbackTemplate(answer)

	for _, raw := range strings.Split(answer, "\n") {
		line := strings.TrimSpace(strings.TrimPrefix(raw, "- "))
		lower := strings.ToLower(line)

		if strings.Contains(lower, "gap") || strings.Contains(lower, "missing claim") || strings.Contains(lower, "unsupported") {
			gaps = append(gaps, line)
		}
	}

	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" {
			gaps = append(gaps, strings.TrimSpace(result.Error))
		}
	}

	return utilUniqueTrimmed(gaps, 4)
}

func readOnlyEvidencePacketConfidence(answer string, sourceBodyPaths []string, findings []ReadOnlyEvidenceFinding) string {
	lower := strings.ToLower(answer)
	switch {
	case strings.Contains(lower, "confidence: high"):
		return "high"
	case strings.Contains(lower, "confidence: low"):
		return "low"
	case strings.Contains(lower, "confidence: medium"):
		return "medium"
	case len(sourceBodyPaths) > 0 && len(findings) > 0:
		return "medium"
	case len(sourceBodyPaths) > 0:
		return "low"
	default:
		return ""
	}
}

func readOnlyEvidencePacketSourceLikePaths(paths []string, limit int) []string {
	filtered := make([]string, 0, len(paths))
	for _, raw := range paths {
		path := normalizeReadOnlyEvidencePacketPath(raw)
		if path == "" || IsNonSourceEvidenceArtifactPath(path) {
			continue
		}

		filtered = append(filtered, path)
	}

	return readOnlySourceLikePaths(filtered, limit)
}

func normalizeReadOnlyEvidencePacketPath(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if readOnlyPathEscapesWorkspace(trimmed) {
		return ""
	}

	path := normalizeDelegatedDiscoveryEvidencePath(trimmed)

	if strings.HasPrefix(trimmed, "./") && strings.HasPrefix(path, "/") {
		path = strings.TrimPrefix(path, "/")
	}

	if strings.HasPrefix(trimmed, ".") && !strings.HasPrefix(trimmed, "./") && !strings.HasPrefix(path, ".") {
		path = "." + path
	}

	return NormalizeRunnerPath(path)
}

func readOnlyEvidencePacketUsefulFindings(findings []ReadOnlyEvidenceFinding) []ReadOnlyEvidenceFinding {
	out := make([]ReadOnlyEvidenceFinding, 0, len(findings))
	seen := map[string]struct{}{}

	for _, finding := range findings {
		if strings.TrimSpace(finding.Text) == "" || readOnlyEvidencePacketTransportLine(finding.Text) {
			continue
		}

		key := finding.Path + "\x00" + finding.Text
		if _, ok := seen[key]; ok {
			continue
		}

		seen[key] = struct{}{}

		out = append(out, finding)
	}

	return out
}

func readOnlyEvidencePacketTransportLine(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))

	return lower == "" ||
		strings.HasPrefix(lower, "partial findings from this turn") ||
		strings.HasPrefix(lower, "verified context:") ||
		strings.HasPrefix(lower, "grounded answer:") ||
		strings.HasPrefix(lower, "grounded findings:") ||
		strings.HasPrefix(lower, "read ")
}

func utilUniqueTrimmed(values []string, limit int) []string {
	seen := map[string]struct{}{}
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

		if limit > 0 && len(out) >= limit {
			break
		}
	}

	return out
}

func delegatedDiscoveryPathPriority(path string) int {
	path = strings.ToLower(filepath.ToSlash(strings.TrimSpace(path)))
	switch {
	case strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") && strings.HasPrefix(path, "internal/"):
		return 0
	case strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go"):
		return 1
	case strings.HasPrefix(path, "internal/"):
		return 2
	case strings.HasSuffix(path, "_test.go"):
		return 3
	default:
		return 4
	}
}

func delegatedDiscoveryLooksLikeEvidencePath(path string) bool {
	path = filepath.ToSlash(strings.TrimSpace(path))
	if path == "" {
		return false
	}

	switch {
	case strings.HasPrefix(path, "search_content/"),
		strings.HasPrefix(path, "search_files/"),
		strings.HasPrefix(path, "read/"),
		strings.HasPrefix(path, "list_files/"):
		return false
	case pathparse.HasWorkspaceFileExtension(path):
		return true
	default:
		return false
	}
}

func preferNonTestEvidencePaths(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}

	hasNonTestImplementation := false

	for _, raw := range paths {
		path := filepath.ToSlash(strings.TrimSpace(raw))
		if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			hasNonTestImplementation = true
			break
		}
	}

	if !hasNonTestImplementation {
		return paths
	}

	filtered := make([]string, 0, len(paths))
	for _, raw := range paths {
		path := filepath.ToSlash(strings.TrimSpace(raw))
		if strings.HasSuffix(path, "_test.go") {
			continue
		}

		filtered = append(filtered, raw)
	}

	if len(filtered) == 0 {
		return paths
	}

	return filtered
}

func normalizeDelegatedDiscoveryEvidencePath(path string) string {
	path = normalizeWorkspacePathScanText(path)
	path = filepath.ToSlash(strings.TrimSpace(path))
	path = strings.Trim(path, "`'\"()[]{}<>,.;")

	if path == "" {
		return ""
	}

	for _, ext := range []string{".go", ".md", ".json", ".yaml", ".yml", ".toml", ".txt"} {
		if idx := indexFold(path, ext); idx >= 0 {
			end := idx + len(ext)
			suffix := path[end:]

			switch {
			case suffix == "":
				return path[:end]
			case strings.HasPrefix(suffix, ":"):
				return path[:end]
			case strings.Trim(suffix, "`'\"") == "":
				return path[:end]
			}
		}
	}

	return path
}

func discoveryQueryCandidateEvidenceFacts(results []workspace.ToolResult) []toolfacts.DiscoveryCandidateEvidence {
	var candidates []toolfacts.DiscoveryCandidateEvidence
	for _, result := range results {
		if strings.TrimSpace(result.Tool) != workspace.ToolNameDiscoveryQuery ||
			strings.TrimSpace(result.Error) != "" ||
			result.Facts == nil {

			continue
		}

		if len(result.Facts.RankedDiscoveryCandidates) > 0 {
			for _, candidate := range result.Facts.RankedDiscoveryCandidates {
				candidate.Path = normalizeDelegatedDiscoveryEvidencePath(candidate.Path)
				if !discoveryQueryCandidateEvidencePath(candidate.Path) {
					continue
				}

				candidates = append(candidates, candidate)
			}
			continue
		}

		for _, path := range result.Facts.CandidatePaths {
			path = normalizeDelegatedDiscoveryEvidencePath(path)
			if !discoveryQueryCandidateEvidencePath(path) {
				continue
			}

			candidates = append(candidates, toolfacts.DiscoveryCandidateEvidence{Path: path})
		}
	}

	return candidates
}

func discoveryQueryCandidatePaths(results []workspace.ToolResult, limit int) []string {
	if limit <= 0 {
		limit = 4
	}

	facts := discoveryQueryCandidateEvidenceFacts(results)
	if len(facts) == 0 {
		return nil
	}

	paths := make([]string, 0, len(facts))
	seen := make(map[string]struct{}, len(facts))
	for _, fact := range facts {
		path := normalizeDelegatedDiscoveryEvidencePath(fact.Path)
		if !discoveryQueryCandidateEvidencePath(path) {
			continue
		}

		if _, ok := seen[path]; ok {
			continue
		}

		seen[path] = struct{}{}
		paths = append(paths, path)
	}

	return textutil.Prefix(preferNonTestEvidencePaths(paths), limit)
}

func discoveryQueryCandidateEvidencePath(path string) bool {
	path = normalizeDelegatedDiscoveryEvidencePath(path)
	if path == "" || strings.HasPrefix(path, ".idiom/") || strings.HasPrefix(path, ".") {
		return false
	}

	if !delegatedDiscoveryLooksLikeEvidencePath(path) {
		return false
	}

	return strings.Contains(path, "/") || strings.HasSuffix(path, ".go")
}

func delegatedDiscoveryLineIsBarePath(line string) bool {
	line = strings.TrimSpace(line)
	if line == "" || strings.ContainsAny(line, " \t") {
		return false
	}

	path := normalizeDelegatedDiscoveryEvidencePath(line)

	return path != "" && delegatedDiscoveryLooksLikeEvidencePath(path)
}

func delegatedDiscoveryFindingSummaryIsUseful(name string, text string) bool {
	lowerName := strings.ToLower(strings.TrimSpace(name))
	lower := strings.ToLower(strings.TrimSpace(text))

	if lower == "" {
		return false
	}

	if strings.Contains(lower, "context pack wrote") ||
		strings.Contains(lower, "no related go files matched") ||
		strings.Contains(lower, "no go symbols matched") ||
		strings.Contains(lower, "no symbols matched") ||
		strings.Contains(lower, "no matching symbol") ||
		strings.Contains(lower, "semantic fallback after gopls failure") ||
		strings.Contains(lower, "collected go dependency graph") {

		return false
	}

	if lowerName == workspace.ToolNameDependencyGraph && strings.HasPrefix(lower, "collected ") {
		return false
	}

	if lowerName == "text_anchor_search" && delegatedDiscoverySummaryIsBroadCountOnly(lower) {
		return false
	}

	return true
}

func delegatedDiscoveryFindingLines(candidates []toolfacts.DiscoveryCandidateEvidence, limit int) []string {
	var lines []string
	seen := map[string]struct{}{}

	for _, evidence := range candidates {
		name := strings.TrimSpace(evidence.LayerName)
		if name == "" || isDelegatedDiscoveryContextDigestLayer(name) || name == "target_listing" {
			continue
		}

		text := strings.TrimSpace(evidence.Summary)
		if text == "" {
			continue
		}

		if !delegatedDiscoveryFindingSummaryIsUseful(name, text) {
			continue
		}

		line := name + ": " + text
		if _, ok := seen[line]; ok {
			continue
		}

		seen[line] = struct{}{}
		lines = append(lines, line)
		if limit > 0 && len(lines) >= limit {
			return lines
		}
	}

	return lines
}

func uniqueDelegatedDiscoveryPaths(paths []string) []string {
	normalized := make([]string, 0, len(paths))
	fullByBase := make(map[string]string)
	ambiguousBase := make(map[string]bool)
	dirCounts := make(map[string]int)

	for _, raw := range paths {
		path := normalizeDelegatedDiscoveryEvidencePath(raw)
		if path == "" {
			continue
		}

		normalized = append(normalized, path)
		if strings.Contains(path, "/") {
			base := filepath.Base(path)
			dirCounts[filepath.Dir(path)]++

			if prior, ok := fullByBase[base]; ok && prior != path {
				ambiguousBase[base] = true
				continue
			}

			fullByBase[base] = path
		}
	}

	seen := make(map[string]struct{})
	unique := make([]string, 0, len(normalized))
	dominantDir := ""
	dominantCount := 0

	for dir, count := range dirCounts {
		if count > dominantCount || (count == dominantCount && (dominantDir == "" || dir < dominantDir)) {
			dominantDir = dir
			dominantCount = count
		}
	}

	for _, path := range normalized {
		if !strings.Contains(path, "/") {
			base := filepath.Base(path)
			if full, ok := fullByBase[base]; ok && !ambiguousBase[base] {
				path = full
			} else if dominantCount >= 2 {
				path = filepath.ToSlash(filepath.Join(dominantDir, base))
			}
		}

		if _, ok := seen[path]; ok {
			continue
		}

		seen[path] = struct{}{}
		unique = append(unique, path)
	}

	return unique
}

func delegatedDisplayQuestion(objective string) string {
	for _, line := range strings.Split(strings.TrimSpace(objective), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "named workspace scope:") ||
			strings.HasPrefix(lower, "key terms:") ||
			strings.HasPrefix(lower, "scope guidance:") ||
			strings.HasPrefix(lower, "start from ") ||
			strings.HasPrefix(lower, "return inspected paths") ||
			strings.HasPrefix(lower, "- ") {

			continue
		}

		return line
	}

	return strings.TrimSpace(objective)
}

func delegatedReadOnlyFindingLineIsUseful(line string) bool {
	if _, ok := stripSourceEvidenceHandleLinePrefix(line); ok {
		return false
	}

	if line == "" ||
		delegatedDiscoveryLineIsBarePath(line) ||
		looksLikeLineOrientedSourceSnippetLine(line) ||
		sourceEvidenceLineIsBoilerplate(stripSourceEvidenceLinePrefix(line)) {

		return false
	}

	lower := strings.ToLower(line)
	if readOnlyToolControlGuidanceText(lower) ||
		strings.Contains(lower, "<<question>>") ||
		strings.Contains(lower, "completion gate") ||
		strings.Contains(lower, "missing completion evidence") ||
		strings.Contains(lower, "child task completed") ||
		strings.Contains(lower, "the retained source context identifies") ||
		strings.Contains(lower, "enough for a bounded answer") ||
		strings.Contains(lower, "safely expand beyond the inspected anchors") ||
		strings.Contains(lower, "the strongest implementation evidence is in") ||
		strings.Contains(lower, "use those findings as the source of truth") ||
		lower == "context:" ||
		lower == "context" ||
		strings.Contains(lower, "top-level declaration") ||
		strings.HasPrefix(lower, "source-body evidence paths:") ||
		strings.HasPrefix(lower, "source-body evidence:") ||
		strings.HasPrefix(lower, "source body evidence paths:") ||
		strings.HasPrefix(lower, "source body evidence:") ||
		strings.HasPrefix(lower, "what this establishes:") ||
		strings.HasPrefix(lower, "outlined ") ||
		strings.HasPrefix(lower, "source:") ||
		strings.HasPrefix(lower, "question:") ||
		strings.HasPrefix(lower, "confidence:") {

		return false
	}

	return strings.Contains(line, ":") || strings.Contains(line, "`") || strings.Contains(line, ".")
}

func delegatedReadOnlyFindingLineSupportedByPaths(line string, supported map[string]struct{}) bool {
	mentionedAny := false
	supportedAny := false

	for _, path := range readOnlySourceLikePaths(pathparse.MentionedWorkspacePaths(normalizeWorkspacePathScanText(line)), 16) {
		mentionedAny = true

		if _, ok := supported[path]; ok {
			supportedAny = true
		}
	}

	return !mentionedAny || supportedAny
}

func delegatedReadOnlyFindingLines(results []workspace.ToolResult, supportedPaths []string, limit int) []string {
	if limit <= 0 {
		return nil
	}

	supported := readOnlyPathSet(supportedPaths)

	var lines []string

	seen := map[string]struct{}{}

	for _, result := range results {
		output := compactOutput(result.Output, fallbackLineBudgetForTool(result.Tool))
		for _, raw := range strings.Split(output, "\n") {
			line := cleanDelegatedReadOnlyFindingLine(strings.TrimSpace(strings.TrimPrefix(raw, "- ")))
			if !delegatedReadOnlyFindingLineIsUseful(line) {
				continue
			}

			if !delegatedReadOnlyFindingLineSupportedByPaths(line, supported) {
				continue
			}

			if _, ok := seen[line]; ok {
				continue
			}

			seen[line] = struct{}{}
			lines = append(lines, line)

			if len(lines) >= limit {
				return lines
			}
		}
	}

	return lines
}

func sanitizeDelegatedReadOnlyResultOutput(result workspace.ToolResult) string {
	output := strings.TrimSpace(result.Output)
	if output == "" {
		return ""
	}

	if strings.TrimSpace(result.Tool) != workspace.ToolNameTask {
		return output
	}

	return stripDelegatedReadOnlyInternalPacket(output)
}

func normalizeDelegatedReadOnlyPacket(answer string) string {
	answer = strings.TrimSpace(normalizeWorkspacePathScanText(answer))
	if answer == "" {
		return ""
	}

	answer = stableHandlePathLinePattern.ReplaceAllString(answer, "$1")
	answer = stableHandleInlinePattern.ReplaceAllString(answer, "$1")
	answer = delegatedPacketPathLinePattern.ReplaceAllString(answer, "$1")
	answer = delegatedPacketPathQuotePattern.ReplaceAllString(answer, "$1")

	return answer
}

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

func delegatedReadOnlyLiteralSymbolCandidate(raw string) (string, bool) {
	value := strings.TrimSpace(raw)

	value = strings.Trim(value, "`'\"()[]{}<>,.:;!?")
	value = strings.TrimSpace(strings.TrimPrefix(value, "./"))

	if value == "" || pathparse.LooksLikeWorkspacePathToken(strings.ToLower(value)) {
		return "", false
	}

	if len(value) < 4 {
		return "", false
	}

	firstRune, _ := utf8.DecodeRuneInString(value)
	if !unicode.IsLetter(firstRune) && firstRune != '_' {
		return "", false
	}

	hasLetter := false

	for _, r := range value {
		switch {
		case unicode.IsLetter(r):
			hasLetter = true
		case unicode.IsDigit(r), r == '_':
		default:
			return "", false
		}
	}

	if !hasLetter {
		return "", false
	}

	if !delegatedReadOnlyLooksCodeShapedSymbol(value) {
		return "", false
	}

	lower := strings.ToLower(value)
	if delegatedDiscoveryStopWord(lower, delegatedDiscoveryStopWordSymbol) {
		return "", false
	}

	return value, true
}

func nonArtifactEvidencePaths(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		if strings.TrimSpace(path) == "" || IsNonSourceEvidenceArtifactPath(path) {
			continue
		}

		out = append(out, path)
	}

	return out
}

func planThenExecuteEvidenceRank(level PlanThenExecuteSourceEvidenceLevel) int {
	switch level {
	case planThenExecuteEvidenceAnchorReady, planThenExecuteEvidenceExactAnchorContract, planThenExecuteEvidenceNewFileContract:
		return 3
	case planThenExecuteEvidenceBodyWindow:
		return 2
	case planThenExecuteEvidenceSourceBodyPending, planThenExecuteEvidencePathSeen:
		return 1
	default:
		return 0
	}
}

type planThenExecuteSourceEvidence struct {
	Path   string
	Level  PlanThenExecuteSourceEvidenceLevel
	Source string
	Detail string
}

type PlanThenExecuteSourceEvidenceLevel string

const (
	planThenExecuteEvidenceNone                PlanThenExecuteSourceEvidenceLevel = ""
	planThenExecuteEvidencePathSeen            PlanThenExecuteSourceEvidenceLevel = "path_seen"
	planThenExecuteEvidenceSourceBodyPending   PlanThenExecuteSourceEvidenceLevel = "source_body_pending"
	planThenExecuteEvidenceBodyWindow          PlanThenExecuteSourceEvidenceLevel = "body_window"
	planThenExecuteEvidenceAnchorReady         PlanThenExecuteSourceEvidenceLevel = "anchor_ready"
	planThenExecuteEvidenceExactAnchorContract PlanThenExecuteSourceEvidenceLevel = "exact_anchor_contract"
	planThenExecuteEvidenceNewFileContract     PlanThenExecuteSourceEvidenceLevel = "new_file_contract"
)

func sourceEvidenceObjectiveLineTerms(objective string) []string {
	lower := strings.ToLower(strings.TrimSpace(objective))
	terms := readOnlySearchHitObjectiveTerms(objective)
	add := func(values ...string) {
		for _, value := range values {
			value = strings.ToLower(strings.TrimSpace(value))
			if value == "" || slices.Contains(terms, value) {
				continue
			}

			terms = append(terms, value)
		}
	}
	words := objectiveWordSet(lower)

	if _, ok := words["up"]; ok {
		add("keyup", "up")
	}

	if _, ok := words["down"]; ok {
		add("keydown", "down")
	}

	if _, ok := words["enter"]; ok {
		add("keyenter", "enter")
	}

	if _, ok := words["message"]; ok {
		add("message")
	}

	if _, ok := words["messages"]; ok {
		add("message")
	}

	if _, ok := words["history"]; ok {
		add("history")
	}

	if _, ok := words["subagent"]; ok {
		add("child", "task", "delegate", "delegated", "delegation")
	}

	if _, ok := words["subagents"]; ok {
		add("child", "task", "delegate", "delegated", "delegation")
	}

	if _, ok := words["user"]; ok {
		add("user")
	}

	return textutil.Sorted(terms)
}

func sourceEvidenceLineObjectiveScore(line string, terms []string) int {
	lower := strings.ToLower(strings.TrimSpace(line))
	if lower == "" {
		return 0
	}

	score := 0

	for _, term := range terms {
		term = strings.ToLower(strings.TrimSpace(term))
		if term == "" {
			continue
		}

		if strings.Contains(lower, term) {
			score++
		}
	}

	return score
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

func implementationTargetWriteReadyRedirectedReadTool(result workspace.ToolResult) (string, bool) {
	if strings.TrimSpace(result.Tool) != workspace.ToolNameReadFile || strings.TrimSpace(result.Error) != "" {
		return "", false
	}

	summary := strings.TrimSpace(result.Summary)
	lower := strings.ToLower(summary)

	if !strings.HasPrefix(lower, "redirected ") {
		return "", false
	}

	tail := summary[len("Redirected "):]
	idx := strings.Index(strings.ToLower(tail), " to ")

	if idx < 0 {
		return "", false
	}

	fields := strings.Fields(tail[idx+len(" to "):])
	if len(fields) == 0 {
		return "", false
	}

	tool := strings.Trim(fields[0], "`'\".,;:")
	if !implementationTargetWriteReadyRedirectedSourceTool(tool) {
		if !strings.HasSuffix(tool, "_file_outline") {
			return "", false
		}

		related, ok := implementationTargetWriteReadyRedirectedRelatedSourceTool(result)
		if !ok {
			return "", false
		}

		return related, true
	}

	return tool, true
}

const readOnlyPrefetchEventRole = "read_only_named_path_prefetch"

type readOnlyPrefetchEvent struct {
	SchemaVersion            string                          `json:"schema_version"`
	Eligible                 bool                            `json:"eligible"`
	MissReason               string                          `json:"miss_reason,omitempty"`
	RequestedPaths           int                             `json:"requested_paths"`
	SelectedPaths            []string                        `json:"selected_paths,omitempty"`
	SkippedPaths             []workspace.PrefetchSkippedPath `json:"skipped_paths,omitempty"`
	DuplicatePaths           int                             `json:"duplicate_paths,omitempty"`
	Bytes                    int                             `json:"bytes,omitempty"`
	Lines                    int                             `json:"lines,omitempty"`
	TruncatedFiles           int                             `json:"truncated_files,omitempty"`
	EstimatedRoundTripsSaved int                             `json:"estimated_round_trips_saved,omitempty"`
}

func (r *Runner) maybeApplyReadOnlyNamedPathPrefetch(recorder runnerTurnRecorder, collectedResults []workspace.ToolResult) []workspace.ToolResult {
	event := r.readOnlyNamedPathPrefetchEvent(collectedResults)
	defer func() {
		if data, err := json.Marshal(event); err == nil {
			r.emitEvent(RunEvent{Role: readOnlyPrefetchEventRole, Content: string(data)})
		}
	}()

	if !event.Eligible || r == nil || r.Workspace == nil {
		return collectedResults
	}

	paths := readOnlyPrefetchObjectivePaths(r.RuntimeBrief)

	event.RequestedPaths = len(paths)

	if len(paths) == 0 {
		event.Eligible = false
		event.MissReason = "no_explicit_named_paths"

		return collectedResults
	}

	report := r.Workspace.PrefetchReadFiles(paths, workspace.PrefetchReadOptions{})

	event.SelectedPaths = append([]string(nil), report.SelectedPaths...)
	event.SkippedPaths = append([]workspace.PrefetchSkippedPath(nil), report.SkippedPaths...)
	event.DuplicatePaths = report.DuplicatePaths
	event.Bytes = report.Bytes
	event.Lines = report.Lines
	event.TruncatedFiles = report.TruncatedFiles
	event.EstimatedRoundTripsSaved = report.EstimatedRoundTripsSaved

	if len(report.Results) == 0 {
		event.Eligible = false
		event.MissReason = "no_paths_prefetched"

		return collectedResults
	}

	for i, result := range report.Results {
		req := workspace.ToolRequest{Tool: workspace.ToolNamePrefetch, Path: report.SelectedPaths[i]}
		recorder.recordWithOptions(0, req, result, "read_only_named_path_prefetch", runnerRecordOptions{
			TrackProgress: true,
			ResetRejected: true,
		})
	}

	return *recorder.collectedResults
}

func (r *Runner) readOnlyNamedPathPrefetchEvent(collectedResults []workspace.ToolResult) readOnlyPrefetchEvent {
	event := readOnlyPrefetchEvent{SchemaVersion: "idiom.read_only_named_path_prefetch.v1"}
	if r == nil || r.Workspace == nil {
		event.MissReason = "workspace_unavailable"
		return event
	}

	if os.Getenv("IDIOM_DISABLE_READONLY_PREFETCH") == "1" {
		event.MissReason = "disabled_by_env"
		return event
	}

	if readOnlyNamedPathPrefetchChildAgent(r.agentName()) {
		event.MissReason = "delegated_child_agent"
		return event
	}

	if len(collectedResults) > 0 {
		event.MissReason = "existing_evidence_present"
		return event
	}

	if !readOnlyNamedPathPrefetchLane(r.RuntimeBrief) {
		event.MissReason = "not_read_only_workspace"
		return event
	}

	event.Eligible = true

	return event
}

func readOnlyNamedPathPrefetchLane(brief workerruntime.WorkerRuntimeBrief) bool {
	brief = workerruntime.NormalizeRuntimeBrief(brief)
	return workerruntime.PermissionProfileIsReadOnlyWorkspace(brief.PermissionProfile)
}

// readOnlyNamedPathPrefetchChildAgent keeps delegated child packet production
// model-directed. Child runners have their own evidence-acquisition contract
// and already receive named paths through child-prompt narrowing, so top-level
// prefetch should not alter that actor boundary.
func readOnlyNamedPathPrefetchChildAgent(agentName string) bool {
	agentName = strings.TrimSpace(agentName)
	return strings.HasPrefix(agentName, "child:") || strings.HasPrefix(agentName, "child_write:")
}

func readOnlyPrefetchObjectivePaths(brief workerruntime.WorkerRuntimeBrief) []string {
	text := strings.TrimSpace(brief.Objective)
	if text == "" {
		return nil
	}

	return pathparse.MentionedWorkspacePaths(text)
}

type readOnlySourceOwnerRole string

const (
	readOnlySourceOwnerUnknown               readOnlySourceOwnerRole = "unknown"
	readOnlySourceOwnerMechanism             readOnlySourceOwnerRole = "mechanism_owner"
	readOnlySourceOwnerStateHolder           readOnlySourceOwnerRole = "state_holder"
	readOnlySourceOwnerDisplaySupport        readOnlySourceOwnerRole = "display_support"
	readOnlySourceOwnerTestSupport           readOnlySourceOwnerRole = "test_support"
	readOnlySourceOwnerConflictingNavigation readOnlySourceOwnerRole = "conflicting_navigation_owner"
)

type readOnlySourceOwnerEvidence struct {
	Path          string
	Symbol        string
	Role          readOnlySourceOwnerRole
	Mechanism     bool
	SpecificTerms []string
	WeakTerms     []string
	SupportTerms  []string
	ConflictTerms []string
	Score         int
}

func (e readOnlySourceOwnerEvidence) OwnerEligible() bool {
	return e.Role == readOnlySourceOwnerMechanism
}

func readOnlySourceOwnerEvidenceForBody(path string, symbol string, objective string, body string) readOnlySourceOwnerEvidence {
	evidence := readOnlySourceOwnerEvidence{
		Path:   NormalizeRunnerPath(path),
		Symbol: strings.TrimSpace(symbol),
		Role:   readOnlySourceOwnerUnknown,
	}
	if !ObjectiveAsksImplementationShape(objective) {
		return evidence
	}

	if implementationShapeTestOwnerDisallowed(evidence.Path, objective) {
		evidence.Role = readOnlySourceOwnerTestSupport
		return evidence
	}

	rawText := body
	text := strings.ToLower(body)

	if strings.TrimSpace(text) == "" {
		return evidence
	}

	evidence.Mechanism = implementationShapeMechanismEvidence(text)
	evidence.SpecificTerms = sourceOwnerMatchedTerms(text, sourceOwnerSpecificObjectiveTerms(RecoveryObjectiveSearchWords(objective, 32)))
	evidence.WeakTerms = sourceOwnerMatchedWeakTerms(text, RecoveryObjectiveSearchWords(objective, 32))
	evidence.SupportTerms = sourceOwnerSupportTerms(text)
	evidence.ConflictTerms = sourceOwnerConflictTerms(rawText, evidence.Path, evidence.Symbol, objective)
	evidence.Score = sourceOwnerEvidenceScore(evidence)

	if sourceOwnerLooksLikeDisplaySupport(text) && !sourceOwnerHasDirectMechanismSignal(text) {
		evidence.Role = readOnlySourceOwnerDisplaySupport
		return evidence
	}

	if sourceOwnerLooksLikeStateHolder(text) && !sourceOwnerHasDirectMechanismSignal(text) {
		evidence.Role = readOnlySourceOwnerStateHolder
		return evidence
	}

	if evidence.Mechanism {
		if len(evidence.ConflictTerms) >= 2 && len(evidence.SpecificTerms) == 0 {
			evidence.Role = readOnlySourceOwnerConflictingNavigation
			return evidence
		}

		if len(evidence.SpecificTerms) > 0 || len(evidence.WeakTerms) >= 2 {
			evidence.Role = readOnlySourceOwnerMechanism
			return evidence
		}
	}

	if sourceOwnerLooksLikeDisplaySupport(text) {
		evidence.Role = readOnlySourceOwnerDisplaySupport
		return evidence
	}

	if sourceOwnerLooksLikeStateHolder(text) {
		evidence.Role = readOnlySourceOwnerStateHolder
		return evidence
	}

	return evidence
}

func sourceOwnerHasDirectMechanismSignal(text string) bool {
	return ContainsAny(text,
		"case tea.",
		"case \"",
		" handles ",
		" handle",
		" calls ",
		" append",
		" switch ",
		" route",
		" dispatch",
		" invoke",
	)
}

func implementationShapeMechanismEvidence(text string) bool {
	return sourceOwnerHasDirectMechanismSignal(text) || strings.Contains(text, " func ")
}

func sourceOwnerMatchedTerms(text string, terms []string) []string {
	var matches []string

	for _, term := range terms {
		term = ReadOnlyObjectiveSupportTerm(term)
		if len(term) < 3 {
			continue
		}

		if strings.Contains(text, term) {
			matches = append(matches, term)
		}
	}

	return textutil.Sorted(matches)
}

func sourceOwnerMatchedWeakTerms(text string, terms []string) []string {
	var matches []string

	for _, term := range terms {
		term = ReadOnlyObjectiveSupportTerm(term)
		if len(term) < 3 || !weakSourceOwnerObjectiveTerm(term) {
			continue
		}

		if strings.Contains(text, term) {
			matches = append(matches, term)
		}
	}

	return textutil.Sorted(matches)
}

func sourceOwnerSupportTerms(text string) []string {
	var matches []string

	for _, term := range []string{
		"render", "view", "viewport", "content", "state", "struct", "field", "type",
	} {
		if strings.Contains(text, term) {
			matches = append(matches, term)
		}
	}

	return textutil.Sorted(matches)
}

func sourceOwnerConflictTerms(text string, path string, symbol string, objective string) []string {
	objectiveTerms := sourceOwnerObjectiveTermSet(objective)

	var matches []string

	for _, term := range sourceOwnerIdentityTerms(path, symbol, text) {
		if _, ok := objectiveTerms[term]; ok {
			continue
		}

		matches = append(matches, term)
	}

	return textutil.Sorted(matches)
}

func sourceOwnerLooksLikeDisplaySupport(text string) bool {
	return ContainsAny(text, "render", "viewport", "view()", "setcontent", "content")
}

func sourceOwnerLooksLikeStateHolder(text string) bool {
	return ContainsAny(text, "type ", " struct ", "state", " field")
}

func implementationShapeTestOwnerDisallowed(path string, objective string) bool {
	path = strings.ToLower(strings.TrimSpace(NormalizeRunnerPath(path)))
	if path == "" || !strings.HasSuffix(path, "_test.go") {
		return false
	}

	return !objectiveAllowsTestImplementationOwner(objective)
}

func objectiveAllowsTestImplementationOwner(objective string) bool {
	lower := " " + strings.ToLower(strings.Join(strings.Fields(objective), " ")) + " "

	return ContainsAny(lower,
		" test ",
		" tests ",
		" testing ",
		" coverage ",
		" regression ",
		" _test.go ",
		" test file ",
		" test files ",
	)
}

func sourceOwnerEvidenceScore(evidence readOnlySourceOwnerEvidence) int {
	score := 0
	if evidence.Mechanism {
		score += 10
	}

	score += len(evidence.SpecificTerms) * 5
	score += len(evidence.WeakTerms) * 2
	score += len(evidence.SupportTerms)

	score -= len(evidence.ConflictTerms) * 8

	return score
}

func sourceOwnerObjectiveTermSet(objective string) map[string]struct{} {
	set := map[string]struct{}{}

	for _, term := range RecoveryObjectiveSearchWords(objective, 64) {
		term = ReadOnlyObjectiveSupportTerm(term)
		if len(term) < 3 {
			continue
		}

		set[term] = struct{}{}
	}

	return set
}

func sourceOwnerIdentityTerms(path string, symbol string, text string) []string {
	var terms []string

	for _, term := range implementationPathTerms(path) {
		if sourceOwnerGenericIdentityTerm(term) {
			continue
		}

		terms = append(terms, term)
	}

	for _, raw := range sourceOwnerIdentityFragments(symbol + "\n" + sourceOwnerBodyIdentityText(text)) {
		for _, term := range splitImplementationPathToken(raw) {
			term = ReadOnlyObjectiveSupportTerm(term)
			if len(term) < 4 || sourceOwnerGenericIdentityTerm(term) {
				continue
			}

			terms = append(terms, term)
		}
	}

	return textutil.Sorted(terms)
}

func sourceOwnerBodyIdentityText(text string) string {
	var lines []string

	for _, line := range strings.Split(text, "\n") {
		lower := strings.ToLower(strings.TrimSpace(line))
		if strings.Contains(lower, "route_summary:") ||
			strings.Contains(lower, "func ") ||
			strings.Contains(lower, " handles ") ||
			strings.Contains(lower, " handle") ||
			strings.Contains(lower, " has focus") ||
			strings.Contains(lower, "returns ") {

			lines = append(lines, line)
		}

		if len(lines) >= 8 {
			break
		}
	}

	return strings.Join(lines, "\n")
}

func sourceOwnerIdentityFragments(text string) []string {
	return strings.FieldsFunc(text, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9')
	})
}

func sourceOwnerGenericIdentityTerm(term string) bool {
	switch ReadOnlyObjectiveSupportTerm(term) {
	case "internal", "cmd", "pkg", "app", "apps", "test", "tests", "model", "func", "return", "returns",
		"handle", "handler", "handlers", "update", "press", "key", "keys", "message",
		"state", "true", "false", "bool", "line", "lines", "read", "body", "source", "summary",
		"route", "routed", "tool", "code", "navigate", "msg":
		return true
	default:
		return false
	}
}

func stripSourceEvidenceHandleLinePrefix(line string) (string, bool) {
	space := strings.IndexByte(line, ' ')
	if space <= 1 || line[0] != 'f' {
		return "", false
	}

	for _, ch := range line[1:space] {
		if ch < '0' || ch > '9' {
			return "", false
		}
	}

	rest := strings.TrimSpace(line[space+1:])
	pathEnd := strings.IndexByte(rest, ':')

	if pathEnd <= 0 {
		return "", false
	}

	path := strings.TrimSpace(rest[:pathEnd])
	if path == "" || (!strings.Contains(path, "/") && !strings.Contains(path, ".")) {
		return "", false
	}

	afterPath := rest[pathEnd+1:]
	lineEnd := 0

	for lineEnd < len(afterPath) && afterPath[lineEnd] >= '0' && afterPath[lineEnd] <= '9' {
		lineEnd++
	}

	if lineEnd == 0 || lineEnd >= len(afterPath) || afterPath[lineEnd] != ':' {
		return "", false
	}

	return strings.TrimSpace(afterPath[lineEnd+1:]), true
}

func stripSourceEvidenceLinePrefix(line string) string {
	line = strings.TrimSpace(line)
	if line == "" {
		return ""
	}

	if stripped, ok := stripSourceEvidenceHandleLinePrefix(line); ok {
		return stripped
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

func looksLikeDelegatedGroundedFallbackAnswer(answer string) bool {
	trimmed := strings.TrimSpace(answer)
	if trimmed == "" {
		return false
	}

	if genericEvidenceAnswerIncludesLineOrientedSourceBody(trimmed) || looksLikeLineOrientedSourceSnippetAnswer(trimmed) {
		return false
	}

	lower := strings.ToLower(trimmed)

	return strings.HasPrefix(lower, "the answer is grounded in ") &&
		strings.Contains(lower, "\n\nwhat this establishes:\n- ")
}

func (r *Runner) initialBootstrapInstruction() string {
	if r == nil {
		return ""
	}

	family := strings.TrimSpace(r.RuntimeBrief.TaskFamily)
	profile := strings.TrimSpace(r.RuntimeBrief.PermissionProfile)

	switch {
	case strings.HasPrefix(r.agentName(), "child:") && family == "delegated_read_only":
		return strings.Join([]string{
			"[delegated read-only bootstrap]",
			"Consume the delegated scope first. Do not start with directory walking when the delegated prompt already names paths or components.",
			"Use one exact-text or language-symbol capability to locate the mechanism, then inspect the best hit directly with a bounded read or symbol-read capability.",
			"For broad architecture or boundary questions, prefer a composite discovery_query first, then inspect only the named paths or symbols that answer the delegated question.",
			"After any successful search hit, inspect that hit before doing another filename or content search.",
			"Stop as soon as the delegated question is answered. Return concise grounded findings with inspected paths and confidence.",
		}, "\n")
	case workerruntime.TaskFamilyIsArchitecture(family) && workerruntime.RuntimeContractIsArchitecture(r.RuntimeBrief.Contract):
		return strings.Join([]string{
			"[architecture bootstrap]",
			"Start broad architecture questions with delegated read-only discovery when the task tool is available.",
			"The delegated packet should establish inspected paths, boundary evidence, confidence, and named gaps before local follow-up.",
			"After a child packet, answer unless one exact missing claim needs one bounded probe.",
		}, "\n")
	case workerruntime.TaskFamilyIsVerificationOnly(family) || workerruntime.PermissionProfileIsReadOnlyVerificationShell(profile):
		cmd := "go test ./..."
		if len(r.RuntimeBrief.VerificationPlan) > 0 && strings.TrimSpace(r.RuntimeBrief.VerificationPlan[0]) != "" {
			cmd = strings.TrimSpace(r.RuntimeBrief.VerificationPlan[0])
		}

		return strings.Join([]string{
			"[verification bootstrap]",
			"Verification task contract: run the configured verification command before inspection.",
			fmt.Sprintf("Configured verification command: %s", cmd),
			"After it runs, answer with the observed pass/fail result.",
		}, "\n")
	case debugLogContractApplies(r.RuntimeBrief, nil):
		return strings.Join([]string{
			"[debug-log bootstrap]",
			"Start from the newest relevant log artifact; source-code orientation is lower value until log evidence is exhausted.",
			"First choose the newest real log file (for example under logs/, *.log, or *.jsonl), then read its recent tail.",
			"Stay on that selected log until you can cite the weird decision or declare the missing datum.",
			"After one bounded log read, answer from the logs if the cause is already clear; otherwise do one targeted follow-up inside the same log.",
		}, "\n")
	case workerruntime.TaskFamilyEvidenceFirstMutation(family) && len(r.RuntimeBrief.TargetRefs) > 0:
		return strings.Join([]string{
			"[evidence-first mutation bootstrap]",
			fmt.Sprintf("This turn will update named target file(s): %s.", strings.Join(r.RuntimeBrief.TargetRefs, ", ")),
			"First gather source evidence from the implementation files or configs that drive the change.",
			"Then read only the target section you plan to edit.",
		}, "\n")
	default:
		return ""
	}
}

func (r *Runner) verificationBootstrapRequest() (workspace.ToolRequest, bool) {
	if r == nil {
		return workspace.ToolRequest{}, false
	}

	if !workerruntime.PermissionProfileIsReadOnlyVerificationShell(r.RuntimeBrief.PermissionProfile) {
		return workspace.ToolRequest{}, false
	}

	cmd := "go test ./..."

	for _, candidate := range r.RuntimeBrief.VerificationPlan {
		if trimmed := strings.TrimSpace(candidate); trimmed != "" {
			cmd = trimmed
			break
		}
	}

	if strings.TrimSpace(cmd) == "" {
		return workspace.ToolRequest{}, false
	}

	return workspace.ToolRequest{
		Tool:    workspace.ToolNameShell,
		Command: cmd,
		Workdir: ".",
	}, true
}

func synthesizeVerificationOnlyAnswer(req workspace.ToolRequest, result workspace.ToolResult) string {
	command := strings.TrimSpace(req.Command)
	if command == "" {
		command = strings.TrimSpace(req.Tool)
	}

	status := "passed"
	if strings.TrimSpace(result.Error) != "" {
		status = "failed"
	}

	var b strings.Builder

	fmt.Fprintf(&b, "Verification %s.\n\n", status)

	if command != "" {
		fmt.Fprintf(&b, "Command: `%s`\n", command)
	}

	if errText := strings.TrimSpace(result.Error); errText != "" {
		fmt.Fprintf(&b, "Failure: %s\n", errText)
	}

	if output := compactOutput(result.Output, 24); strings.TrimSpace(output) != "" {
		b.WriteString("\nOutput:\n")
		b.WriteString(output)
		b.WriteString("\n")
	}

	b.WriteString("\nNo files were edited.")

	return strings.TrimSpace(b.String())
}

func (r *Runner) debugLogPreflightRequest(results []workspace.ToolResult, req workspace.ToolRequest) (workspace.ToolRequest, bool) {
	if r == nil || !debugLogContractApplies(r.RuntimeBrief, results) || debugLogEvidenceSatisfied(r.RuntimeBrief, results) {
		return workspace.ToolRequest{}, false
	}

	discovery, _ := debugLogDiscoveryRequest(r.Workspace)
	selection, ok := latestStableDebugLogSelection(results, currentDebugLogSessionID(r.Workspace))

	if !ok {
		if debugLogRequestMatchesDiscovery(req, discovery) || isConcreteDebugLogArtifactRequest(req) {
			return workspace.ToolRequest{}, false
		}

		return discovery, true
	}

	if !hasDebugLogReadForSelection(results, selection) {
		if strings.TrimSpace(req.Tool) == workspace.ToolNameReadFile && debugLogRequestTargetsSelection(req, selection) {
			return workspace.ToolRequest{}, false
		}

		return debugLogTailRequest(selection), true
	}

	if _, ok := latestDebugLogSearchHit(results); !ok {
		if shouldDeepenDebugLogSearch(results, selection) {
			if next, ok := nextStableDebugLogSelection(results, selection, currentDebugLogSessionID(r.Workspace)); ok {
				if strings.TrimSpace(req.Tool) == workspace.ToolNameReadFile && debugLogRequestTargetsSelection(req, next) {
					return workspace.ToolRequest{}, false
				}

				return debugLogTailRequest(next), true
			}
		}

		if redirected, ok := debugLogScopedSearchRedirect(req, selection); ok {
			return redirected, true
		}
	}

	return workspace.ToolRequest{}, false
}

func prematureTargetReadRejection(req workspace.ToolRequest, results []workspace.ToolResult, brief workerruntime.WorkerRuntimeBrief) (string, string, bool) {
	if !workerruntime.TaskFamilyEvidenceFirstMutation(brief.TaskFamily) || len(brief.TargetRefs) == 0 {
		return "", "", false
	}

	if strings.TrimSpace(req.Tool) != workspace.ToolNameReadFile {
		return "", "", false
	}

	path := strings.TrimSpace(req.Path)
	if path == "" || !slices.Contains(brief.TargetRefs, path) {
		return "", "", false
	}

	if !isLargeTargetRead(req) {
		return "", "", false
	}

	if hasFocusedNonTargetEvidence(results, brief.TargetRefs) {
		return "", "", false
	}

	return "Premature target-file read rejected",
		"this turn needs source evidence before editing the named target file. Inspect the implementation files or configs that drive the change, then read only the target section you plan to edit.",
		true
}

func evidenceFirstTargetReadRedirect(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, req workspace.ToolRequest) (workspace.ToolRequest, bool) {
	if !workerruntime.TaskFamilyEvidenceFirstMutation(brief.TaskFamily) || len(brief.TargetRefs) == 0 {
		return workspace.ToolRequest{}, false
	}

	if len(newToolResultEvidenceProjection(results).ChangedPathSet()) > 0 {
		return workspace.ToolRequest{}, false
	}

	targetReadCount := successfulTargetReadCount(results, brief.TargetRefs)
	if !hasFocusedNonTargetEvidence(results, brief.TargetRefs) {
		return workspace.ToolRequest{}, false
	}

	if targetReadCount == 0 {
		if !workspace.IsReadTool(strings.TrimSpace(req.Tool)) {
			return workspace.ToolRequest{}, false
		}

		path := strings.TrimSpace(req.Path)
		if strings.TrimSpace(req.Tool) == workspace.ToolNameReadFile && slices.Contains(brief.TargetRefs, path) && !isLargeTargetRead(req) {
			return workspace.ToolRequest{}, false
		}

		return firstTargetReadWindowRequest(brief.TargetRefs), true
	}

	if strings.TrimSpace(req.Tool) != workspace.ToolNameReadFile || !slices.Contains(brief.TargetRefs, strings.TrimSpace(req.Path)) {
		return workspace.ToolRequest{}, false
	}

	if !targetReadNeedsBounding(req) {
		return workspace.ToolRequest{}, false
	}

	return boundedFollowupTargetReadRequest(req, results, brief.TargetRefs), true
}

func isLargeTargetRead(req workspace.ToolRequest) bool {
	start := req.StartLine
	end := req.EndLine

	if start <= 0 && end <= 0 {
		return true
	}

	if start <= 0 {
		start = 1
	}

	if end <= 0 {
		return true
	}

	return end-start > 160
}

func hasFocusedNonTargetEvidence(results []workspace.ToolResult, targetRefs []string) bool {
	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" {
			continue
		}

		switch tool := strings.TrimSpace(result.Tool); {
		case tool == workspace.ToolNameReadFile:
			path := extractRunnerFilePath(result.Summary)
			if path != "" && !slices.Contains(targetRefs, path) {
				return true
			}
		case strings.HasSuffix(tool, "_file_outline"), strings.HasSuffix(tool, "_read_symbol"), strings.HasSuffix(tool, "_extract_body"):
			path := extractSemanticInspectionPath(result.Summary)
			if path != "" && !slices.Contains(targetRefs, path) {
				return true
			}
		}
	}

	return false
}

func hasSuccessfulTargetRead(results []workspace.ToolResult, targetRefs []string) bool {
	return successfulTargetReadCount(results, targetRefs) > 0
}

func successfulTargetReadCount(results []workspace.ToolResult, targetRefs []string) int {
	count := 0

	for _, result := range results {
		if strings.TrimSpace(result.Tool) != workspace.ToolNameReadFile || strings.TrimSpace(result.Error) != "" {
			continue
		}

		if path := extractRunnerFilePath(result.Summary); slices.Contains(targetRefs, path) {
			count++
		}
	}

	return count
}

func evidenceFirstTargetReadUnlocked(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	return workerruntime.TaskFamilyEvidenceFirstMutation(brief.TaskFamily) &&
		len(brief.TargetRefs) > 0 &&
		hasFocusedNonTargetEvidence(results, brief.TargetRefs) &&
		!hasSuccessfulTargetRead(results, brief.TargetRefs)
}

const (
	initialEvidenceFirstTargetReadWindowEnd = 220
	followupEvidenceFirstTargetReadLines    = 140
)

func firstTargetReadWindowRequest(targetRefs []string) workspace.ToolRequest {
	for _, path := range targetRefs {
		if trimmed := strings.TrimSpace(path); trimmed != "" {
			return workspace.ToolRequest{
				Tool:      workspace.ToolNameReadFile,
				Path:      trimmed,
				StartLine: 1,
				EndLine:   initialEvidenceFirstTargetReadWindowEnd,
			}
		}
	}

	return workspace.ToolRequest{}
}

func boundedFollowupTargetReadRequest(req workspace.ToolRequest, results []workspace.ToolResult, targetRefs []string) workspace.ToolRequest {
	start := req.StartLine
	if start <= 0 {
		if window, ok := latestSuccessfulTargetReadWindow(results, targetRefs); ok {
			start = window.End + 1
		} else {
			start = 1
		}
	}

	if start <= 0 {
		start = 1
	}

	return workspace.ToolRequest{
		Tool:      workspace.ToolNameReadFile,
		Path:      strings.TrimSpace(req.Path),
		StartLine: start,
		EndLine:   start + followupEvidenceFirstTargetReadLines - 1,
	}
}

func targetReadNeedsBounding(req workspace.ToolRequest) bool {
	if strings.TrimSpace(req.Tool) != workspace.ToolNameReadFile {
		return false
	}

	start := req.StartLine
	end := req.EndLine

	if start <= 0 || end <= 0 {
		return true
	}

	return end-start+1 > followupEvidenceFirstTargetReadLines
}

func latestSuccessfulTargetReadWindow(results []workspace.ToolResult, targetRefs []string) (debugLogReadWindow, bool) {
	for i := len(results) - 1; i >= 0; i-- {
		result := results[i]
		if strings.TrimSpace(result.Tool) != workspace.ToolNameReadFile || strings.TrimSpace(result.Error) != "" {
			continue
		}

		window, ok := parseDebugLogReadWindow(result)
		if !ok || !slices.Contains(targetRefs, strings.TrimSpace(window.Path)) {
			continue
		}

		return window, true
	}

	return debugLogReadWindow{}, false
}

func clearEvidenceFirstTargetReadActionClasses(rejectedActions map[string]int, brief workerruntime.WorkerRuntimeBrief) {
	if len(rejectedActions) == 0 {
		return
	}

	for _, path := range brief.TargetRefs {
		actionClass := toolActionClass(workspace.ToolRequest{Tool: workspace.ToolNameReadFile, Path: strings.TrimSpace(path)})
		if actionClass == "" {
			continue
		}

		delete(rejectedActions, actionClass)
	}
}

func clearRejectedFingerprintSummary(rejected map[string]string, fingerprint string, summary string) {
	fingerprint = strings.TrimSpace(fingerprint)
	summary = strings.TrimSpace(summary)

	if fingerprint == "" || summary == "" || len(rejected) == 0 {
		return
	}

	if strings.TrimSpace(rejected[fingerprint]) == summary {
		delete(rejected, fingerprint)
	}
}

func (r *Runner) readOnlyExplanationCompletionRejection(reply string, results []workspace.ToolResult) (workspace.ToolResult, bool) {
	if r == nil || !readOnlyExplanationGateApplies(r.RuntimeBrief) {
		return workspace.ToolResult{}, false
	}

	if reason := readOnlyExplanationMetaRejectionReason(reply); reason != "" {
		return workspace.ToolResult{
			Tool:    "completion_gate",
			Summary: "Read-only explanation gate rejected meta final answer",
			Error:   reason,
		}, true
	}

	if reason := readOnlyBinaryVerdictContradictionReason(reply, r.RuntimeBrief.Objective); reason != "" {
		return workspace.ToolResult{
			Tool:    "completion_gate",
			Summary: "Read-only explanation gate rejected contradictory verdict",
			Error:   reason,
		}, true
	}

	if unsupported := architectureUnsupportedExactDetail(reply, results); len(unsupported) > 0 {
		return workspace.ToolResult{
			Tool:    "completion_gate",
			Summary: "Architecture completion gate rejected unsupported detail",
			Error:   "the answer names additional implementation detail " + quotedRunnerStrings(unsupported) + " without an exact inspected probe or explicit uncertainty. Either anchor that detail with one exact read, or qualify it as unverified.",
		}, true
	}

	return workspace.ToolResult{}, false
}

func readOnlyBinaryVerdictContradictionReason(answer string, objective string) string {
	if !readOnlyObjectiveLooksBinaryQuestion(objective) {
		return ""
	}

	polarities := map[string]struct{}{}
	for _, line := range strings.Split(answer, "\n") {
		if polarity := readOnlyLabeledVerdictPolarity(line); polarity != "" {
			polarities[polarity] = struct{}{}
		}
	}

	if _, hasYes := polarities["yes"]; !hasYes {
		return ""
	}

	if _, hasNo := polarities["no"]; !hasNo {
		return ""
	}

	return "the final answer gives contradictory yes/no conclusions for the requested question. Reconcile the conclusion against the inspected evidence, qualify the uncertainty, or state the exact blocker."
}

func readOnlyObjectiveLooksBinaryQuestion(objective string) bool {
	lower := strings.ToLower(strings.Join(strings.Fields(objective), " "))
	if lower == "" {
		return false
	}

	for _, marker := range []string{
		" whether ",
		" can ",
		" could ",
		" does ",
		" do ",
		" is ",
		" are ",
		" was ",
		" were ",
		" did ",
	} {
		if strings.Contains(" "+lower+" ", marker) {
			return true
		}
	}

	return strings.HasPrefix(lower, "can ") ||
		strings.HasPrefix(lower, "could ") ||
		strings.HasPrefix(lower, "does ") ||
		strings.HasPrefix(lower, "do ") ||
		strings.HasPrefix(lower, "is ") ||
		strings.HasPrefix(lower, "are ")
}

func readOnlyLabeledVerdictPolarity(line string) string {
	line = strings.ToLower(strings.TrimSpace(line))
	line = strings.Trim(line, "*_#` \t")
	if line == "" {
		return ""
	}

	label := ""
	for _, candidate := range []string{"finding:", "verdict:", "conclusion:", "answer:"} {
		if idx := strings.Index(line, candidate); idx >= 0 {
			label = strings.TrimSpace(line[idx+len(candidate):])
			break
		}
	}

	if label == "" {
		return ""
	}

	label = strings.Trim(label, "*_` \t")
	switch {
	case strings.HasPrefix(label, "yes") || strings.HasPrefix(label, "it can") || strings.HasPrefix(label, "can "):
		return "yes"
	case strings.HasPrefix(label, "no") || strings.HasPrefix(label, "it cannot") || strings.HasPrefix(label, "it can't") || strings.HasPrefix(label, "cannot"):
		return "no"
	default:
		return ""
	}
}

func featurePlannerPlanArtifactResult(result workspace.ToolResult) bool {
	if strings.EqualFold(strings.TrimSpace(result.PayloadType), featurePlannerPlanArtifactPayloadType) ||
		strings.EqualFold(strings.TrimSpace(result.Tool), featurePlannerPlanArtifactPayloadType) {
		return true
	}

	return featurePlannerPlanMarkdownReadResult(result)
}

func featurePlannerPlanArtifactMatchesObjective(objective string, result workspace.ToolResult) bool {
	if !featurePlannerPlanArtifactResult(result) {
		return false
	}

	return featurePlannerPlanArtifactPathsMatchObjective(objective, featurePlannerPlanArtifactProposedPaths(result))
}

func featurePlannerPlanArtifactPathsMatchObjective(objective string, paths []string) bool {
	if len(paths) == 0 {
		return false
	}

	objectiveText := strings.ToLower(strings.Join(strings.Fields(objective), " "))
	if objectiveText == "" {
		return false
	}

	if objectiveAsksAboutActivePlan(objectiveText) {
		return true
	}

	return false
}

func objectiveAsksAboutActivePlan(objectiveText string) bool {
	objectiveText = normalizedPlanObjectiveText(objectiveText)

	return ContainsAny(" "+objectiveText+" ",
		" plan ", " planning ", " planned ",
		" proposal ", " proposed ",
		" approve ", " approval ",
		" feature-planner ", " feature planner ",
		" plan tab ", " plan view ",
		" what does this entail ", " what it entails ",
		" what will this do ", " what does it do ",
		" what are we doing ", " what are we building ",
	)
}

func objectiveAsksAboutPlanExpansion(objectiveText string) bool {
	objectiveText = normalizedPlanObjectiveText(objectiveText)

	return ContainsAny(" "+objectiveText+" ",
		" expand ", " expansion ", " add ", " additional ", " more ",
		" recommend ", " recommendation ", " should we ",
		" coverage ", " tests ", " test ", " test suite ", " tdd ",
	)
}

func normalizedPlanObjectiveText(objectiveText string) string {
	objectiveText = strings.NewReplacer(
		"?", " ",
		"!", " ",
		",", " ",
		".", " ",
		":", " ",
		";", " ",
		"(", " ",
		")", " ",
	).Replace(strings.ToLower(objectiveText))
	return strings.Join(strings.Fields(objectiveText), " ")
}

func answerMentionsFeaturePlannerPlan(answer string) bool {
	lower := " " + strings.ToLower(strings.Join(strings.Fields(answer), " ")) + " "
	return ContainsAny(lower,
		" active plan ", " proposed plan ", " planned files ", " proposed files ",
		" plan artifact ", " plan tab ", " feature-planner ", " feature planner ",
	)
}

func featurePlannerPlanMarkdownReadResult(result workspace.ToolResult) bool {
	if strings.TrimSpace(result.Error) != "" {
		return false
	}

	record := newToolResultEvidenceRecord(result)
	if record.tool != workspace.ToolNameReadFile && record.tool != workspace.ToolNamePrefetch {
		return false
	}

	text := featurePlannerPlanMarkdownRecognitionText(result)
	if !strings.Contains(text, "# Feature Planner Candidate") {
		return false
	}

	if !ContainsAny(text, "\nStatus:", "\nSession:", "\nState:") {
		return false
	}

	if !ContainsAny(text, "\n## Proposed Paths", "\n## Test Coverage", "\n## Verification", "\n## Atoms") {
		return false
	}

	readPaths := record.ReadPaths()
	if len(readPaths) == 0 {
		return false
	}

	for _, path := range readPaths {
		path = NormalizeRunnerPath(path)
		if path == "" {
			continue
		}
		if strings.HasPrefix(path, ".planning/") ||
			strings.HasPrefix(path, "planning/") ||
			strings.HasSuffix(path, ".planning.md") {

			return true
		}
	}

	return false
}

func featurePlannerPlanMarkdownRecognitionText(result workspace.ToolResult) string {
	text := strings.TrimSpace(result.Summary + "\n" + result.Output)
	if text == "" {
		return ""
	}

	lineNumberPrefix := regexp.MustCompile(`(?m)^\s*\d+:`)
	return lineNumberPrefix.ReplaceAllString(text, "")
}

func featurePlannerPlanArtifactProposedPaths(result workspace.ToolResult) []string {
	var raw struct {
		ProposedPaths []string `json:"proposed_paths"`
		Paths         []string `json:"paths"`
	}

	if len(result.Payload) > 0 {
		_ = json.Unmarshal(result.Payload, &raw)
	}

	paths := append([]string(nil), raw.ProposedPaths...)
	paths = append(paths, raw.Paths...)
	if featurePlannerPlanMarkdownReadResult(result) {
		paths = append(paths, pathparse.MentionedWorkspaceFiles(featurePlannerPlanMarkdownRecognitionText(result))...)
	}
	paths = append(paths, pathparse.MentionedWorkspaceFiles(result.Output)...)
	paths = append(paths, pathparse.MentionedWorkspaceFiles(result.Summary)...)

	var out []string
	seen := map[string]struct{}{}
	for _, path := range paths {
		path = NormalizeRunnerPath(path)
		if path == "" {
			continue
		}
		if featurePlannerPlanMetadataPath(path) {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}

	return out
}

func featurePlannerPlanMetadataPath(path string) bool {
	path = NormalizeRunnerPath(path)
	return strings.HasPrefix(path, ".idiom/") ||
		strings.HasPrefix(path, "idiom/") ||
		strings.HasPrefix(path, ".planning/") ||
		strings.HasPrefix(path, "planning/")
}

func planThenExecuteAnswerDeclaresTypedBlocker(answer string) bool {
	trimmed := strings.TrimSpace(answer)
	if strings.HasPrefix(trimmed, "<<QUESTION>>") ||
		strings.Contains(trimmed, "\n<<QUESTION>>") {

		return true
	}

	for _, line := range strings.Split(trimmed, "\n") {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), "decision: block") {
			return true
		}
	}

	return false
}

func (r *Runner) implementationTargetSourceWriteBeforeEvidenceRedirect(req workspace.ToolRequest, results []workspace.ToolResult, currentAllowedTools []string) (workspace.ToolRequest, string, bool) {
	if r == nil || !slices.Contains(currentAllowedTools, workspace.ToolNameReadFile) {
		return workspace.ToolRequest{}, "", false
	}

	if summary, _, reject := r.implementationTargetSourceWriteBeforeEvidenceRejection(req, results, currentAllowedTools); !reject || summary != "Write rejected before source-body evidence" {
		return workspace.ToolRequest{}, "", false
	}

	paths := writeRequestPaths(req)
	if len(paths) != 1 {
		return workspace.ToolRequest{}, "", false
	}

	target := NormalizeRunnerPath(paths[0])
	if target == "" || newToolResultEvidenceProjection(results).HasSourceBody(target) {
		return workspace.ToolRequest{}, "", false
	}

	redirected := workspace.ToolRequest{
		Tool:            workspace.ToolNameReadFile,
		Path:            target,
		StartLine:       1,
		EndLine:         180,
		NamedGap:        "source-body evidence before applying the requested write",
		WhyNotAnswering: "need current source evidence for the model-selected write target before editing",
	}

	return redirected, marshalToolRequestRaw(redirected), true
}

type implementationDeferredSourceEvidenceWrite struct {
	Request         workspace.ToolRequest
	Raw             string
	EvidencePath    string
	RequiresHistory bool
}

func newImplementationDeferredSourceEvidenceWrite(req workspace.ToolRequest, raw string, evidencePath string) *implementationDeferredSourceEvidenceWrite {
	target := NormalizeRunnerPath(evidencePath)
	if target == "" {
		paths := writeRequestPaths(req)
		if len(paths) == 1 {
			target = NormalizeRunnerPath(paths[0])
		}
	}

	if target == "" || !workspace.IsWorkspaceWriteTool(req.Tool) {
		return nil
	}

	if strings.TrimSpace(raw) == "" {
		raw = marshalToolRequestRaw(req)
	}

	return &implementationDeferredSourceEvidenceWrite{
		Request:      req,
		Raw:          raw,
		EvidencePath: target,
	}
}

func (r *Runner) newImplementationDeferredSourceEvidenceWrite(req workspace.ToolRequest, raw string, evidencePath string, currentAllowedTools []string) *implementationDeferredSourceEvidenceWrite {
	if normalizedReq, normalizedRaw, ok := r.normalizeDeleteIntentDeferredWrite(req, raw, evidencePath, currentAllowedTools); ok {
		req = normalizedReq
		raw = normalizedRaw
	}

	return newImplementationDeferredSourceEvidenceWrite(req, raw, evidencePath)
}

func (r *Runner) normalizeDeleteIntentDeferredWrite(req workspace.ToolRequest, raw string, evidencePath string, currentAllowedTools []string) (workspace.ToolRequest, string, bool) {
	target := NormalizeRunnerPath(evidencePath)
	if target == "" {
		paths := writeRequestPaths(req)
		if len(paths) == 1 {
			target = NormalizeRunnerPath(paths[0])
		}
	}

	if target == "" ||
		strings.TrimSpace(req.Tool) != workspace.ToolNameWriteFile ||
		strings.TrimSpace(req.Content) != "" ||
		!runnerToolAllowed(workspace.ToolNameApplyPatch, currentAllowedTools, r.RuntimeBrief.AllowedTools) {

		return workspace.ToolRequest{}, "", false
	}

	if !implementationDeleteIntentForPath(r.RuntimeBrief.Objective, strings.Join([]string{req.Description, raw}, "\n"), target) {
		return workspace.ToolRequest{}, "", false
	}

	patch := fmt.Sprintf("*** Begin Patch\n*** Delete File: %s\n*** End Patch\n", target)
	normalized := workspace.ToolRequest{
		Tool:        workspace.ToolNameApplyPatch,
		Content:     patch,
		Description: "delete " + target,
	}

	return normalized, marshalToolRequestRaw(normalized), true
}

func runnerToolAllowed(tool string, currentAllowedTools []string, briefAllowedTools []string) bool {
	tool = strings.TrimSpace(tool)
	if tool == "" {
		return false
	}

	if slices.Contains(currentAllowedTools, tool) {
		return true
	}

	return len(currentAllowedTools) == 0 && slices.Contains(briefAllowedTools, tool)
}

func implementationDeleteIntentForPath(objective string, requestText string, path string) bool {
	path = NormalizeRunnerPath(path)
	if path == "" {
		return false
	}

	objective = strings.ToLower(objective)
	requestText = strings.ToLower(requestText)
	if !implementationTextHasDeleteVerb(objective) || !implementationTextHasDeleteVerb(requestText) {
		return false
	}

	if implementationTextLooksLikeContentRemoval(objective, path) {
		return false
	}

	return implementationTextNamesFileTarget(objective, path)
}

func implementationTextHasDeleteVerb(text string) bool {
	text = " " + strings.ToLower(strings.TrimSpace(text)) + " "
	for _, token := range []string{" delete ", " deleting ", " remove ", " removing ", " unlink "} {
		if strings.Contains(text, token) {
			return true
		}
	}

	return false
}

func implementationTextLooksLikeContentRemoval(text string, path string) bool {
	text = " " + strings.ToLower(strings.TrimSpace(text)) + " "
	base := strings.ToLower(filepath.Base(path))
	for _, phrase := range []string{
		" text from ",
		" content from ",
		" line from ",
		" lines from ",
		" string from ",
		" snippet from ",
		" from " + strings.ToLower(path),
		" from " + base,
	} {
		if strings.Contains(text, phrase) {
			return true
		}
	}

	return false
}

func implementationTextNamesFileTarget(text string, path string) bool {
	text = " " + strings.ToLower(strings.TrimSpace(text)) + " "
	path = strings.ToLower(path)
	base := strings.ToLower(filepath.Base(path))
	stem := strings.TrimSuffix(base, filepath.Ext(base))

	if strings.Contains(text, path) || strings.Contains(text, base) || (stem != "" && strings.Contains(text, " "+stem+" ")) {
		return true
	}

	for _, phrase := range []string{
		" that file ",
		" this file ",
		" the file ",
		" a file ",
		" new file ",
		" created file ",
		" file you just created ",
		" file just created ",
	} {
		if strings.Contains(text, phrase) {
			return true
		}
	}

	return false
}

func (d *implementationDeferredSourceEvidenceWrite) replayAfterEvidence(req workspace.ToolRequest, results []workspace.ToolResult, currentAllowedTools []string) (workspace.ToolRequest, string, bool) {
	if d == nil || strings.TrimSpace(d.EvidencePath) == "" {
		return workspace.ToolRequest{}, "", false
	}

	if !implementationTargetSourceWriteTargetsHaveEvidence(d.Request, results) {
		return workspace.ToolRequest{}, "", false
	}

	if !implementationDeferredWriteCoveredBySourceEvidence(d.Request, d.EvidencePath, results, d.RequiresHistory) {
		return workspace.ToolRequest{}, "", false
	}

	if d.RequiresHistory && !implementationDeferredWriteHasHistoryEvidence(d.Request, results) {
		return workspace.ToolRequest{}, "", false
	}

	if !d.replayAllowedForRequest(req) {
		return workspace.ToolRequest{}, "", false
	}

	raw := d.Raw
	if strings.TrimSpace(raw) == "" {
		raw = marshalToolRequestRaw(d.Request)
	}

	return d.Request, raw, true
}

func (d *implementationDeferredSourceEvidenceWrite) replayAfterCompletionAttempt(results []workspace.ToolResult, currentAllowedTools []string) (workspace.ToolRequest, string, bool) {
	if d == nil || strings.TrimSpace(d.EvidencePath) == "" {
		return workspace.ToolRequest{}, "", false
	}

	if !implementationApplyPatchDeletesOnlyPath(d.Request, d.EvidencePath) {
		return workspace.ToolRequest{}, "", false
	}

	if !runnerToolAllowed(d.Request.Tool, currentAllowedTools, nil) {
		return workspace.ToolRequest{}, "", false
	}

	if !implementationTargetSourceWriteTargetsHaveEvidence(d.Request, results) {
		return workspace.ToolRequest{}, "", false
	}

	if !implementationDeferredWriteCoveredBySourceEvidence(d.Request, d.EvidencePath, results, d.RequiresHistory) {
		return workspace.ToolRequest{}, "", false
	}

	if d.RequiresHistory && !implementationDeferredWriteHasHistoryEvidence(d.Request, results) {
		return workspace.ToolRequest{}, "", false
	}

	raw := d.Raw
	if strings.TrimSpace(raw) == "" {
		raw = marshalToolRequestRaw(d.Request)
	}

	return d.Request, raw, true
}

func (d *implementationDeferredSourceEvidenceWrite) replayAfterHistoryEvidence(results []workspace.ToolResult) (workspace.ToolRequest, string, bool) {
	if d == nil || !d.RequiresHistory || strings.TrimSpace(d.EvidencePath) == "" {
		return workspace.ToolRequest{}, "", false
	}

	if !implementationTargetSourceWriteTargetsHaveEvidence(d.Request, results) && !implementationDeferredWriteHasExecutableAnchor(d.Request) {
		return workspace.ToolRequest{}, "", false
	}

	if !implementationDeferredWriteCoveredBySourceEvidence(d.Request, d.EvidencePath, results, true) {
		return workspace.ToolRequest{}, "", false
	}

	if !implementationDeferredWriteHasHistoryEvidence(d.Request, results) {
		return workspace.ToolRequest{}, "", false
	}

	raw := d.Raw
	if strings.TrimSpace(raw) == "" {
		raw = marshalToolRequestRaw(d.Request)
	}

	return d.Request, raw, true
}

func (d *implementationDeferredSourceEvidenceWrite) fallbackVerificationRequest() (workspace.ToolRequest, bool) {
	if d == nil {
		return workspace.ToolRequest{}, false
	}

	paths := writeRequestPaths(d.Request)
	if len(paths) != 1 {
		return workspace.ToolRequest{}, false
	}

	path := NormalizeRunnerPath(paths[0])
	if path == "" {
		return workspace.ToolRequest{}, false
	}

	return workspace.ToolRequest{
		Tool: workspace.ToolNameSourceSanity,
		Path: path,
	}, true
}

func (d *implementationDeferredSourceEvidenceWrite) replayAllowedForRequest(req workspace.ToolRequest) bool {
	tool := strings.TrimSpace(req.Tool)
	if tool == "" || workspace.IsWorkspaceWriteTool(tool) {
		return false
	}

	if d.RequiresHistory && tool == workspace.ToolNameShell {
		return true
	}

	switch tool {
	case workspace.ToolNameReadFile, workspace.ToolNameCodeNavigate, workspace.ToolNameReadOnlyCapability, workspace.ToolNameSourceSanity, workspace.ToolNameHistoryRange, workspace.ToolNameHistorySymbol:
	default:
		return false
	}

	target := NormalizeRunnerPath(planThenExecuteRequestTargetPath(req))
	if target == "" {
		target = NormalizeRunnerPath(req.Path)
	}

	return target == d.EvidencePath
}

func implementationDeferredWriteHasHistoryEvidence(req workspace.ToolRequest, results []workspace.ToolResult) bool {
	paths := writeRequestPaths(req)
	if len(paths) == 0 {
		return false
	}

	for _, path := range paths {
		if !successfulGitHistoryResultForPath(path, results) {
			return false
		}
	}

	return true
}

func implementationDeferredWriteCoveredBySourceEvidence(req workspace.ToolRequest, path string, results []workspace.ToolResult, allowAnchorMismatch bool) bool {
	path = NormalizeRunnerPath(path)
	if path == "" {
		return false
	}

	switch strings.TrimSpace(req.Tool) {
	case workspace.ToolNameEditFile:
		oldText := strings.TrimSpace(req.OldText)
		if oldText == "" {
			return false
		}

		if allowAnchorMismatch {
			return newToolResultEvidenceProjection(results).HasSourceBody(path) || implementationDeferredWriteHasExecutableAnchor(req)
		}

		return implementationSourceEvidenceTextContains(path, results, oldText)
	case workspace.ToolNameWriteFile:
		return fullSourceReadFileSeen(path, results)
	case workspace.ToolNameAppendFile:
		return fullSourceReadFileSeen(path, results)
	case workspace.ToolNameApplyPatch:
		if implementationApplyPatchDeletesOnlyPath(req, path) {
			return newToolResultEvidenceProjection(results).HasSourceBody(path)
		}

		return false
	default:
		return false
	}
}

func implementationApplyPatchDeletesOnlyPath(req workspace.ToolRequest, path string) bool {
	if strings.TrimSpace(req.Tool) != workspace.ToolNameApplyPatch {
		return false
	}

	path = NormalizeRunnerPath(path)
	if path == "" {
		return false
	}

	ops, err := patching.Parse(req.Content)
	if err != nil || len(ops) == 0 {
		return false
	}

	for _, op := range ops {
		if op.Kind != patching.OpDelete || NormalizeRunnerPath(op.Path) != path {
			return false
		}
	}

	return true
}

func implementationDeferredWriteHasExecutableAnchor(req workspace.ToolRequest) bool {
	if strings.TrimSpace(req.Tool) != workspace.ToolNameEditFile {
		return false
	}

	if NormalizeRunnerPath(req.Path) == "" {
		return false
	}

	return strings.TrimSpace(req.OldText) != ""
}

func implementationSourceEvidenceTextContains(path string, results []workspace.ToolResult, want string) bool {
	path = NormalizeRunnerPath(path)
	want = strings.TrimSpace(want)

	if path == "" || want == "" {
		return false
	}

	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" || !slices.Contains(newToolResultEvidenceRecord(result).SourceBodyPaths(), path) {
			continue
		}

		if strings.Contains(strings.Join([]string{result.Summary, result.Output}, "\n"), want) {
			return true
		}
	}

	return false
}

func planThenExecuteCandidateSourceReadyRejectionSeen(results []workspace.ToolResult) bool {
	for _, result := range results {
		if planThenExecuteCandidateSourceReadyResult(result) && strings.TrimSpace(result.Error) != "" {
			return true
		}
	}

	return false
}

func planThenExecuteCandidateSourceReadyResult(result workspace.ToolResult) bool {
	if strings.TrimSpace(result.Code) == planThenExecuteResultCandidateSourceReady {
		return true
	}

	// Compatibility for already-recorded logs and older seeded tests while
	// new behavior writes and reads the typed result code above.
	return strings.TrimSpace(result.Code) == "" &&
		strings.TrimSpace(result.Summary) == "Candidate source already ready"
}

func (r *Runner) implementationTargetSourceWriteBeforeEvidenceRejection(req workspace.ToolRequest, results []workspace.ToolResult, currentAllowedTools []string) (string, string, bool) {
	if r == nil || !workspace.IsWorkspaceWriteTool(req.Tool) {
		return "", "", false
	}

	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief:            r.RuntimeBrief,
		Results:          results,
		BaseAllowedTools: runnerCurrentAllowedOrBriefTools(currentAllowedTools, r.RuntimeBrief.AllowedTools),
	})
	if contract.PhaseID != turnPhaseImplementationTargetSource && contract.Phase != "target_write_ready" {
		return "", "", false
	}

	if r.writeRequestCreatesOnlyMissingTargets(req) {
		return "", "", false
	}

	if implementationTargetSourceWriteTargetsHaveEvidence(req, results) {
		return "", "", false
	}

	return "Write rejected before source-body evidence",
		"implementation.target_source requires target localization and source-body evidence before changing existing source. Use a bounded read/discovery tool with named_gap for the missing target or edit-safety fact, use write_file without prior source-body evidence only for a genuinely new file, or answer <<QUESTION>> with the exact blocker/no-op evidence.",
		true
}

func (r *Runner) writeRequestTargetsAllMissing(req workspace.ToolRequest) bool {
	if r == nil || r.Workspace == nil {
		return false
	}

	root := strings.TrimSpace(r.Workspace.Root())
	if root == "" {
		return false
	}

	paths := writeRequestPaths(req)
	if len(paths) == 0 {
		return false
	}

	for _, path := range paths {
		clean := r.cleanWorkspaceRelativeWritePath(path)
		if clean == "" {
			return false
		}

		_, err := os.Stat(filepath.Join(root, filepath.FromSlash(clean)))
		if err == nil || !os.IsNotExist(err) {
			return false
		}
	}

	return true
}

func (r *Runner) writeRequestCreatesOnlyMissingTargets(req workspace.ToolRequest) bool {
	switch strings.TrimSpace(req.Tool) {
	case workspace.ToolNameWriteFile:
		return strings.TrimSpace(req.Content) != "" && r.writeRequestTargetsAllMissing(req)
	case workspace.ToolNameMkdir:
		return r.writeRequestTargetsAllMissing(req)
	case workspace.ToolNameApplyPatch:
		ops, err := patching.Parse(req.Content)
		if err != nil || len(ops) == 0 {
			return false
		}

		for _, op := range ops {
			if op.Kind != patching.OpAdd {
				return false
			}
		}

		return r.writeRequestTargetsAllMissing(req)
	default:
		return false
	}
}

func (r *Runner) cleanWorkspaceRelativeWritePath(path string) string {
	if clean := cleanRelativeRunnerPath(path); clean != "" {
		return clean
	}

	if r == nil || r.Workspace == nil {
		return ""
	}

	root := strings.TrimSpace(r.Workspace.Root())
	if root == "" {
		return ""
	}

	path = strings.TrimSpace(path)
	if path == "" || !filepath.IsAbs(path) {
		return ""
	}

	for _, candidateRoot := range []string{root, config.CanonicalizeExistingPath(root)} {
		candidateRoot = strings.TrimSpace(candidateRoot)
		if candidateRoot == "" {
			continue
		}

		rel, err := filepath.Rel(candidateRoot, config.CanonicalizeExistingPath(path))
		if err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return filepath.ToSlash(filepath.Clean(rel))
		}
	}

	return ""
}

func implementationTargetSourceWriteTargetsHaveEvidence(req workspace.ToolRequest, results []workspace.ToolResult) bool {
	paths := writeRequestPaths(req)
	if len(paths) == 0 {
		return false
	}

	for _, path := range paths {
		path = NormalizeRunnerPath(path)
		if path == "" || !newToolResultEvidenceProjection(results).HasSourceBody(path) {
			return false
		}
	}

	return true
}

func runnerHasWorkspaceWriteCapability(brief workerruntime.WorkerRuntimeBrief, tools []string) bool {
	if strings.Contains(strings.ToLower(strings.TrimSpace(brief.PermissionProfile)), "write") {
		return true
	}

	for _, tool := range tools {
		if workspace.IsWorkspaceWriteTool(tool) {
			return true
		}
	}

	return false
}

func runnerVerificationRequired(brief workerruntime.WorkerRuntimeBrief) bool {
	contract := strings.ToLower(strings.TrimSpace(brief.VerificationContract))
	return contract != "" && !strings.Contains(contract, "not_required")
}

func defaultRunnerStateSlice(values []string, defaults []string) []string {
	if len(values) > 0 {
		return values
	}

	return append([]string(nil), defaults...)
}

func appendRunnerTools(values []string, tools ...string) []string {
	for _, tool := range tools {
		values = textutil.AppendUnique(values, tool)
	}

	return values
}

func filterOutRunnerTools(values []string, disallowed ...string) []string {
	if len(values) == 0 || len(disallowed) == 0 {
		return values
	}

	blocked := make(map[string]struct{}, len(disallowed))
	for _, tool := range disallowed {
		tool = strings.TrimSpace(tool)
		if tool != "" {
			blocked[tool] = struct{}{}
		}
	}

	filtered := values[:0]
	for _, tool := range values {
		if _, skip := blocked[strings.TrimSpace(tool)]; skip {
			continue
		}

		filtered = append(filtered, tool)
	}

	return filtered
}

var lineOrientedSourceSnippetPattern = regexp.MustCompile(`(?:^|[\s,;])(\d{1,5}):\s*([^\s]+)?`)

type sourceInventoryStats struct {
	MarkerCount int
	Assigns     int
	Contains    int
	Declares    int
	Definitions int
	References  int
	Returns     int
	Sets        int
	Calls       int
	Handles     int
	ListLike    bool
	Synthesized bool
}

func (stats sourceInventoryStats) LooksLikeInventory() bool {
	if stats.Synthesized {
		return false
	}

	return stats.MarkerCount >= 2 || (stats.MarkerCount >= 1 && stats.ListLike)
}

func sourceInventoryStatsForClaim(claim string) sourceInventoryStats {
	normalized := normalizeSourceInventoryClaim(claim)
	stats := sourceInventoryStats{
		Assigns:     sourceInventoryMarkerCount(normalized, "assigns"),
		Contains:    sourceInventoryMarkerCount(normalized, "contains"),
		Declares:    sourceInventoryMarkerCount(normalized, "declares"),
		Definitions: sourceInventoryMarkerCount(normalized, "defines"),
		References:  sourceInventoryMarkerCount(normalized, "references"),
		Returns:     sourceInventoryMarkerCount(normalized, "returns"),
		Sets:        sourceInventoryMarkerCount(normalized, "sets"),
		Calls:       sourceInventoryMarkerCount(normalized, "calls"),
		Handles:     sourceInventoryMarkerCount(normalized, "handles"),
		ListLike:    strings.Contains(normalized, ",") || strings.Contains(normalized, " and ") || strings.Contains(normalized, ";"),
		Synthesized: sourceInventoryClaimLooksSynthesized(normalized),
	}

	stats.MarkerCount = stats.Assigns + stats.Definitions + stats.References + stats.Returns + stats.Sets + stats.Calls + stats.Handles
	stats.MarkerCount += stats.Contains + stats.Declares

	return stats
}

func normalizeSourceInventoryClaim(claim string) string {
	return " " + strings.ToLower(strings.Join(strings.Fields(claim), " ")) + " "
}

func sourceInventoryMarkerCount(normalized string, marker string) int {
	withCode := strings.Count(normalized, " "+marker+" `")
	if withCode > 0 {
		return withCode
	}

	if strings.Contains(normalized, " "+marker+" ") {
		return 1
	}

	return 0
}

func sourceInventoryClaimLooksSynthesized(normalized string) bool {
	return ContainsAny(normalized,
		" because ",
		" this means ",
		" the likely cause is ",
		" the root cause is ",
		" the smallest fix ",
		" the fix is ",
		" should ",
		" would ",
		" needs ",
		" to enable ",
		" preserves ",
	)
}

func looksLikeInspectedBodySourceInventory(lower string) bool {
	normalized := normalizeSourceInventoryClaim(lower)
	idx := strings.Index(normalized, " inspected body shows that it ")

	if idx < 0 {
		idx = strings.Index(normalized, " inspected source shows that it ")
	}

	if idx < 0 {
		return false
	}

	return sourceInventoryStatsForClaim(normalized[idx:]).LooksLikeInventory()
}

func looksLikeSingleAnchorInspectedSourceInventory(lower string) bool {
	if !strings.Contains(lower, "clearest inspected implementation anchor") {
		return false
	}

	normalized := normalizeSourceInventoryClaim(lower)
	idx := strings.Index(normalized, " inspected body shows that it ")

	if idx < 0 {
		idx = strings.Index(normalized, " inspected source shows that it ")
	}

	if idx < 0 {
		return false
	}

	stats := sourceInventoryStatsForClaim(normalized[idx:])

	return stats.MarkerCount > 0 && !stats.Synthesized
}

func looksLikePathShowsSourceInventory(lower string) bool {
	normalized := normalizeSourceInventoryClaim(lower)
	idx := strings.Index(normalized, "` shows ")

	if idx < 0 {
		return false
	}

	return sourceInventoryStatsForClaim(normalized[idx+len("` shows "):]).LooksLikeInventory()
}

func looksLikeLowValueSourceInventoryFallback(answer string) bool {
	stats := sourceInventoryStatsForClaim(stripPresentableSourceObjectiveEcho(answer))
	if stats.Handles > 0 {
		return false
	}

	return stats.Returns >= 2 ||
		stats.Assigns >= 2 ||
		stats.Sets >= 2 ||
		stats.References >= 2 ||
		stats.Assigns+stats.Calls+stats.Returns >= 3 ||
		stats.Definitions+stats.References >= 3 ||
		(stats.Definitions >= 2 && stats.Returns >= 1) ||
		(stats.Definitions >= 1 && stats.Calls+stats.Sets >= 2)
}

func looksLikeDiscoveryEvidenceInventoryNonAnswer(answer string) bool {
	normalized := normalizeSourceInventoryClaim(answer)
	markers := 0

	for _, marker := range sourceInventoryNonAnswerMarkers() {
		if strings.Contains(normalized, marker) {
			markers++
		}
	}

	if strings.Contains(normalized, " this is grounded in `") && markers > 0 {
		markers++
	}

	if sourceInventoryHasPathBulletList(answer) {
		if markers >= 1 && sourceInventoryAnswerIsPrimarilyInventory(answer) {
			return true
		}
		return false
	}

	if markers >= 2 {
		return true
	}

	if sourceInventoryClaimLooksSynthesized(normalized) {
		return false
	}

	return false
}

func sourceInventoryNonAnswerMarkers() []string {
	return []string{
		"evidence paths inspected",
		"evidence paths inspected body backed",
		"evidence paths inspected (body-backed)",
		"body backed source evidence",
		"body-backed source evidence",
		"source body evidence available",
		" related files:",
		" symbol index:",
		" capability:",
		" language:",
		" routed_tool:",
		" route_summary:",
		" discovery packet:",
		" relevant source hits:",
		" evidence used:",
		" no go workspace symbols matched ",
	}
}

func sourceInventoryHasPathBulletList(answer string) bool {
	pathBullets := 0
	for _, line := range strings.Split(answer, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "- ") {
			continue
		}
		if len(analyzeSourceCitations(line).MentionedPaths) > 0 {
			pathBullets++
		}
	}

	return pathBullets >= 1
}

func sourceInventoryAnswerIsPrimarilyInventory(answer string) bool {
	for _, line := range strings.Split(answer, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		normalized := normalizeSourceInventoryClaim(strings.Trim(line, "*_# "))
		if sourceInventoryLineHasNonAnswerMarker(normalized) {
			continue
		}
		if strings.HasPrefix(line, "- ") && len(analyzeSourceCitations(line).MentionedPaths) > 0 {
			continue
		}
		return false
	}

	return true
}

func sourceInventoryLineHasNonAnswerMarker(normalized string) bool {
	for _, marker := range sourceInventoryNonAnswerMarkers() {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

func looksLikeLowValueSourceInventoryNonAnswer(answer string) bool {
	trimmed := strings.TrimSpace(answer)
	lower := strings.ToLower(trimmed)

	if looksLikeLineOrientedSourceSnippetAnswer(trimmed) {
		return true
	}

	if looksLikeSingleAnchorInspectedSourceInventory(lower) {
		return true
	}

	if looksLikeDiscoveryEvidenceInventoryNonAnswer(trimmed) {
		return true
	}

	if strings.Contains(lower, "clearest inspected implementation anchor") &&
		looksLikeInspectedBodySourceInventory(lower) {

		return true
	}

	return (looksLikePathShowsSourceInventory(lower) || looksLikeInspectedBodySourceInventory(lower)) &&
		looksLikeLowValueSourceInventoryFallback(trimmed)
}

func looksLikeSourceInventoryNonAnswer(answer string) bool {
	if looksLikeLowValueSourceInventoryNonAnswer(answer) {
		return true
	}

	stats := sourceInventoryStatsForClaim(stripPresentableSourceObjectiveEcho(answer))
	if stats.Synthesized {
		return false
	}

	normalized := normalizeSourceInventoryClaim(answer)
	if stats.MarkerCount >= 1 &&
		(strings.Contains(normalized, " the answer is grounded in `") ||
			strings.Contains(normalized, " this is grounded in `")) {

		return stats.Definitions+stats.References+stats.Assigns+stats.Returns+
			stats.Sets+stats.Calls+stats.Handles+stats.Contains+stats.Declares > 0
	}

	return (stats.Definitions >= 2 && stats.MarkerCount == stats.Definitions) ||
		(stats.References >= 2 && stats.MarkerCount == stats.References) ||
		(stats.Definitions+stats.References >= 3 && stats.MarkerCount == stats.Definitions+stats.References)
}

func looksLikeLineOrientedSourceSnippetAnswer(answer string) bool {
	trimmed := strings.TrimSpace(answer)
	if trimmed == "" {
		return false
	}

	if looksLikeCompactedLineOrientedSourceSnippet(trimmed) {
		return true
	}

	fragments, sourceMarkers := lineOrientedSourceSnippetCounts(trimmed)
	if fragments < 2 || sourceMarkers == 0 {
		return false
	}

	if sourceInventoryClaimLooksSynthesized(normalizeSourceInventoryClaim(trimmed)) {
		return false
	}

	return true
}

func looksLikeLineOrientedSourceSnippetLine(line string) bool {
	line = strings.TrimSpace(strings.TrimPrefix(line, "- "))
	line = strings.TrimSpace(strings.TrimPrefix(line, "..."))
	line = strings.TrimSpace(strings.TrimPrefix(line, "…"))
	if line == "" {
		return false
	}

	if !lineHasLeadingSourceLineNumber(line) {
		return false
	}

	colon := strings.IndexByte(line, ':')
	rest := strings.TrimSpace(line[colon+1:])
	if rest == "" || rest == "." || rest == ")" || rest == "}" || rest == "{" {
		return true
	}

	return lineOrientedSourceTokenLooksLikeCode(rest)
}

func lineHasLeadingSourceLineNumber(line string) bool {
	colon := strings.IndexByte(line, ':')
	if colon <= 0 || colon > 5 {
		return false
	}

	for _, ch := range line[:colon] {
		if ch < '0' || ch > '9' {
			return false
		}
	}

	return true
}

func looksLikeCompactedLineOrientedSourceSnippet(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || !(strings.HasPrefix(trimmed, "...") || strings.HasPrefix(trimmed, "…")) {
		return false
	}

	if !lineOrientedSourceSnippetPattern.MatchString(trimmed) {
		return false
	}

	lower := strings.ToLower(trimmed)
	return ContainsAny(lower,
		"\t",
		"[]",
		"{}",
		"func ",
		"type ",
		"const ",
		"var ",
		"package ",
		"return ",
	)
}

func lineOrientedSourceSnippetCounts(answer string) (int, int) {
	matches := lineOrientedSourceSnippetPattern.FindAllStringSubmatch(answer, -1)
	fragments := 0
	sourceMarkers := 0

	for _, match := range matches {
		if len(match) < 2 || strings.TrimSpace(match[1]) == "" {
			continue
		}

		fragments++

		token := ""

		if len(match) > 2 {
			token = match[2]
		}

		if lineOrientedSourceTokenLooksLikeCode(token) {
			sourceMarkers++
		}
	}

	return fragments, sourceMarkers
}

func lineOrientedSourceTokenLooksLikeCode(token string) bool {
	token = strings.TrimSpace(token)
	token = strings.Trim(token, "`*_.,;:()[]{}")

	if token == "" {
		return false
	}

	lower := strings.ToLower(token)
	if strings.Contains(token, "(") ||
		strings.Contains(token, "[]") ||
		strings.Contains(token, "&&") ||
		strings.Contains(token, "||") {

		return true
	}

	if strings.HasPrefix(lower, "\"") || strings.HasPrefix(lower, "'") {
		return true
	}

	if strings.HasPrefix(lower, "//") || strings.HasPrefix(lower, "/*") {
		return true
	}

	for _, prefix := range []string{
		"package",
		"import",
		"func",
		"type",
		"const",
		"var",
		"return",
		"case",
		"if",
		"for",
		"switch",
		"map[",
		"make(",
	} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}

	return false
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

func normalizeWorkspacePathScanText(text string) string {
	if strings.TrimSpace(text) == "" {
		return text
	}

	return strings.NewReplacer(`\n`, "\n", `\r`, "\r", `\t`, "\t").Replace(text)
}

func looksLikeSourceEchoOnlyAnswer(answer string) bool {
	trimmed := strings.TrimSpace(answer)
	if trimmed == "" || len(trimmed) > 700 || strings.HasPrefix(trimmed, "<<QUESTION>>") {
		return false
	}

	lower := " " + strings.ToLower(strings.Join(strings.Fields(trimmed), " ")) + " "
	if !looksLikeToolResultTranscript(lower) {
		return false
	}

	claim := normalizeSourceInventoryClaim(stripPresentableSourceObjectiveEcho(trimmed))
	if sourceInventoryClaimLooksSynthesized(claim) ||
		ContainsAny(lower,
			" should reject ",
			" should change ",
		) {

		return false
	}

	return true
}

func looksLikeScratchpadLeakedAnswer(answer string) bool {
	trimmed := strings.TrimSpace(answer)
	if trimmed == "" || strings.HasPrefix(trimmed, "<<QUESTION>>") {
		return false
	}

	normalized := " " + strings.ToLower(strings.Join(strings.Fields(trimmed), " ")) + " "
	for i, line := range strings.Split(trimmed, "\n") {
		lowerLine := strings.ToLower(strings.TrimSpace(line))
		if lowerLine == "" {
			continue
		}

		if lineStartsWithAny(lowerLine,
			"actually, i should",
			"actually, i need",
			"actually, let me",
			"actually, wait",
			"let me analyze",
			"let me inspect",
			"let me check",
			"let me read",
			"let me search",
			"let me verify",
			"let me gather",
			"let me prepare",
			"let me write",
			"let me provide",
			"let me produce",
			"let me think",
			"i should analyze",
			"i should inspect",
			"i should check",
			"i should read",
			"i should search",
			"i should verify",
			"i should gather",
			"i should prepare",
			"i should write",
			"i should provide",
			"i should produce",
			"i need to analyze",
			"i need to inspect",
			"i need to check",
			"i need to read",
			"i need to search",
			"i need to verify",
			"i need to gather",
			"i need to prepare",
			"i need to write",
			"i need to provide",
			"i need to produce",
			"ok, i'm providing",
			"ok, i’m providing",
			"ok, final answer",
			"ok, i'm ready",
			"ok, i’m ready",
			"wait, i",
		) {

			return true
		}

		if i == 0 && lineStartsWithAny(lowerLine, "the user wants", "the user is asking") {
			if ContainsAny(normalized,
				" actually, ",
				" actually, i ",
				" actually, let me ",
				" let me ",
				" i should ",
				" i need to ",
				" ok, i'm providing ",
				" ok, i’m providing ",
				" ok, final answer ",
				" wait, ",
			) {

				return true
			}

			return !ContainsAny(normalized,
				" the answer is ",
				" the likely cause is ",
				" the root cause is ",
				" because ",
				" this means ",
			)
		}
	}

	return false
}

func looksLikeMetaIntentRestatement(answer string) bool {
	trimmed := strings.TrimSpace(answer)
	if trimmed == "" || len(trimmed) > 700 || strings.HasPrefix(trimmed, "<<QUESTION>>") {
		return false
	}

	lower := " " + strings.ToLower(strings.Join(strings.Fields(trimmed), " ")) + " "
	if !ContainsAny(lower,
		" the user is asking me to ",
		" the user asked me to ",
		" the user wants me to ",
		" the user requested me to ",
	) {

		return false
	}

	if ContainsAny(lower,
		" the answer is ",
		" the likely cause is ",
		" the root cause is ",
		" because ",
		" this means ",
	) {

		return false
	}

	return ContainsAny(lower,
		" i need to find ",
		" i need to inspect ",
		" i need to search ",
		" i need to read ",
		" i need to check ",
		" i should find ",
		" i should inspect ",
		" i should search ",
		" i should read ",
		" i should check ",
	)
}

func looksLikeLeakedImplementationBlockerTemplate(lowerAnswer string) bool {
	if !strings.HasPrefix(lowerAnswer, "changed paths: none") {
		return false
	}

	return strings.Contains(lowerAnswer, "\nverification: not run") &&
		strings.Contains(lowerAnswer, "\nblocker:")
}

func looksLikeRawReadOnlyEvidencePacketAnswer(answer string) bool {
	lower := strings.ToLower(strings.TrimSpace(answer))
	if lower == "" || strings.Contains(lower, "grounded answer:") {
		return false
	}

	packetMarkers := 0

	for _, marker := range append(delegatedReadOnlyPacketLineLabels(), "source: delegated read-only evidence packet") {
		if strings.Contains(lower, marker) {
			packetMarkers++
		}
	}

	return packetMarkers >= 1
}

func looksLikeNonAnswerProse(answer string) bool {
	if looksLikeNonAnswerProseCore(answer) {
		return true
	}

	if finalAnswerSectionMarkerCount(answer) > 1 {
		return true
	}

	return false
}

func looksLikeNonAnswerProseCore(answer string) bool {
	trimmed := strings.TrimSpace(answer)
	lower := strings.ToLower(trimmed)

	if trimmed == "" {
		return false
	}

	// Textual tool-call markup like [Calling tool: ...] in answer prose.
	if IsTextualToolCall(answer) {
		return true
	}

	// Echoed user question: the answer starts with "Question:" followed
	// by the user's own words rather than an actual investigation result.
	if strings.HasPrefix(lower, "question:") {
		return true
	}

	if looksLikeMetaIntentRestatement(trimmed) {
		return true
	}

	if looksLikeScratchpadLeakedAnswer(trimmed) {
		return true
	}

	if looksLikeGroundedAnswerRepairState(trimmed) {
		return true
	}

	if looksLikeGateSatisfactionMetaFinalAnswer(trimmed) {
		return true
	}

	if looksLikeReadOnlyReadinessNudgeEcho(trimmed) {
		return true
	}

	if looksLikeEmbeddedQuestionProtocolFinalAnswer(trimmed) {
		return true
	}

	if looksLikePriorAnswerReferenceFinalAnswer(trimmed) {
		return true
	}

	if looksLikeRawReadOnlyEvidencePacketAnswer(trimmed) {
		return true
	}

	if looksLikeLowValueSourceInventoryNonAnswer(trimmed) {
		return true
	}

	// Raw tool-result transcript with no user-facing prose.
	if looksLikeRawToolResultTranscript(answer) {
		return true
	}

	if looksLikeSourceEchoOnlyAnswer(trimmed) {
		return true
	}

	// Leaked implementation-result templates are protocol artifacts, not
	// direct answers. Do not reject ordinary diagnostic prose just because it
	// discusses a blocker, a failed command, or an exit status.
	if looksLikeLeakedImplementationBlockerTemplate(lower) {
		return true
	}

	return false
}

func looksLikePriorAnswerReferenceFinalAnswer(answer string) bool {
	normalized := strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(answer)), " "))
	if normalized == "" || priorAnswerReferenceDiscussesFailure(normalized) {
		return false
	}

	for _, prefix := range []string{
		"the answer above ",
		"the previous answer ",
		"the prior answer ",
		"my previous answer ",
		"my prior answer ",
		"the rca above ",
		"the fix proposal above ",
	} {
		if strings.HasPrefix(normalized, prefix) {
			return true
		}
	}

	for _, phrase := range []string{
		"as described above",
		"as covered above",
		"as listed above",
		"covered above",
		"answer above",
	} {
		if strings.HasPrefix(normalized, phrase) ||
			strings.Contains(normalized, ". "+phrase) ||
			strings.Contains(normalized, "\n"+phrase) {

			return true
		}
	}

	return false
}

func priorAnswerReferenceDiscussesFailure(normalized string) bool {
	if !gateMetaPhraseDiscussesFailure(normalized) {
		return false
	}

	return ContainsAny(normalized,
		"model said",
		"answer said",
		"literal",
		"quoted",
		"phrase",
		"wrongly",
		"incorrectly",
		"accepted incorrectly",
		"leaked because",
		"did not classify",
		"did not treat",
		"root cause was",
		"failure was",
		"bug was",
	)
}

func looksLikeGateSatisfactionMetaFinalAnswer(answer string) bool {
	normalized := strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(answer)), " "))
	if normalized == "" {
		return false
	}

	selfReferential := strings.HasPrefix(normalized, "the answer above ") ||
		strings.HasPrefix(normalized, "the previous answer ") ||
		strings.HasPrefix(normalized, "the prior answer ")
	if selfReferential && ContainsAny(normalized,
		"complete and grounded",
		"grounded in the inspected evidence",
		"grounded in inspected evidence",
		"satisfies the gate",
		"satisfies the completion gate",
		"satisfies the gate requirements",
		"gate requirements",
	) {
		return true
	}

	if ContainsAny(normalized,
		"the answer above is complete",
		"the answer above already answers",
		"the answer above is grounded",
		"the previous answer is complete",
		"the previous answer already answers",
		"the previous answer is grounded",
		"the prior answer is complete",
		"the prior answer already answers",
		"the prior answer is grounded",
	) && ContainsAny(normalized,
		"i have all the evidence",
		"i have enough evidence",
		"i have sufficient evidence",
		"all the evidence i need",
		"all the evidence needed",
	) {
		return true
	}

	if gateMetaPhraseAppearsSelfCertifying(normalized) {
		return true
	}

	return false
}

func gateMetaPhraseAppearsSelfCertifying(normalized string) bool {
	if gateMetaPhraseDiscussesFailure(normalized) {
		return false
	}

	for _, phrase := range []string{
		"this satisfies the gate",
		"this satisfies the completion gate",
		"this satisfies the gate requirements",
		"this answer satisfies the gate",
		"this answer satisfies the completion gate",
		"this answer satisfies the gate requirements",
		"the gate requirements are satisfied",
		"gate requirements are satisfied",
		"the completion gate accepted this answer",
		"completion gate accepted this answer",
	} {
		if strings.HasPrefix(normalized, phrase) ||
			strings.Contains(normalized, ". "+phrase) ||
			strings.Contains(normalized, "\n"+phrase) {

			return true
		}
	}

	return false
}

func gateMetaPhraseDiscussesFailure(normalized string) bool {
	return ContainsAny(normalized,
		"model said",
		"answer said",
		"literal",
		"quoted",
		"phrase",
		"wrongly",
		"incorrectly",
		"bug",
		"root cause",
		"failure",
		"leak",
		"leaked",
		"non-self-contained",
		"accepted incorrectly",
	)
}

func looksLikeEmbeddedQuestionProtocolFinalAnswer(answer string) bool {
	trimmedAnswer := strings.TrimSpace(answer)
	if trimmedAnswer == "" || strings.HasPrefix(trimmedAnswer, "<<QUESTION>>") {
		return false
	}

	for _, line := range strings.Split(trimmedAnswer, "\n") {
		if embeddedQuestionProtocolLine(line) {
			return true
		}
	}

	return false
}

func embeddedQuestionProtocolLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "<<QUESTION>>") {
		return true
	}

	stripped := strings.TrimSpace(strings.TrimLeft(trimmed, "#>*-+ \t"))
	if stripped != trimmed && strings.HasPrefix(stripped, "<<QUESTION>>") {
		return true
	}

	stripped = regexp.MustCompile(`^\d+[\.)]\s*`).ReplaceAllString(trimmed, "")
	return stripped != trimmed && strings.HasPrefix(strings.TrimSpace(stripped), "<<QUESTION>>")
}

func looksLikeGroundedAnswerRepairState(answer string) bool {
	normalized := strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(answer)), " "))
	if normalized == "" {
		return false
	}

	if !strings.HasPrefix(normalized, "for `") ||
		!strings.Contains(normalized, "` provides the grounded answer:") {

		return false
	}

	prefix := normalized[:strings.Index(normalized, "` provides the grounded answer:")]
	if strings.Count(prefix, "`") < 3 {
		return false
	}

	for _, path := range pathparse.MentionedWorkspaceFiles(prefix) {
		path = NormalizeRunnerPath(path)
		if path == "" {
			continue
		}
		if IsNonSourceEvidenceArtifactPath(path) ||
			strings.HasPrefix(path, ".planning/") ||
			strings.HasPrefix(path, "planning/") {

			return true
		}
	}

	return false
}

type sourceEvidenceObligation struct {
	Path        string
	Level       PlanThenExecuteSourceEvidenceLevel
	Source      string
	Detail      string
	PendingTool string
}

// maxObligationResolutionAttempts is the number of failed code_navigate
// attempts on a single path before we treat an obligation as unresolvable
// and drop it from the write-gating set.
const maxObligationResolutionAttempts = 3

func sourceEvidenceHasBody(evidence planThenExecuteSourceEvidence) bool {
	return planThenExecuteEvidenceRank(evidence.Level) >= planThenExecuteEvidenceRank(planThenExecuteEvidenceBodyWindow)
}

func sourceEvidencePendingRedirects(results []workspace.ToolResult) []sourceEvidenceObligation {
	seen := make(map[string]struct{})

	var pending []sourceEvidenceObligation

	for i := len(results) - 1; i >= 0; i-- {
		path := implementationTargetWriteReadyRedirectedReadPath(results[i])
		if path == "" {
			continue
		}

		if _, ok := seen[path]; ok {
			continue
		}

		seen[path] = struct{}{}
		if sourceEvidencePendingRedirectSatisfiedForPath(path, results) {
			continue
		}

		tool, ok := implementationTargetWriteReadyRedirectedReadTool(results[i])
		if !ok {
			continue
		}

		evidence := newToolResultEvidenceProjection(results).SourceEvidenceForPath(path)

		pending = append(pending, sourceEvidenceObligation{
			Path:        path,
			Level:       planThenExecuteEvidenceSourceBodyPending,
			Source:      evidence.Source,
			Detail:      evidence.Detail,
			PendingTool: tool,
		})
	}

	reverseSourceEvidenceObligations(pending)

	return pending
}

func sourceEvidencePendingRedirectSatisfiedForPath(path string, results []workspace.ToolResult) bool {
	path = NormalizeRunnerPath(path)
	if path == "" {
		return false
	}

	for _, result := range results {
		evidence := newToolResultEvidenceRecord(result).SourceEvidenceForPath(path)
		if planThenExecuteEvidenceRank(evidence.Level) < planThenExecuteEvidenceRank(planThenExecuteEvidenceBodyWindow) {
			continue
		}
		return true
	}

	return false
}

func sourceEvidencePendingRedirectsForBrief(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) []sourceEvidenceObligation {
	pending := sourceEvidencePendingRedirects(results)
	if len(pending) == 0 {
		return nil
	}

	if !implementationTargetSourceEvidenceReady(brief, results) {
		return pending
	}

	var filtered []sourceEvidenceObligation

	for _, obligation := range pending {
		if !sourceEvidencePendingRedirectRelevantAfterBodyEvidence(brief, obligation.Path) {
			continue
		}

		if sourceEvidenceObligationResolutionAttempts(obligation.Path, results) >= maxObligationResolutionAttempts {
			continue
		}

		filtered = append(filtered, obligation)
	}

	return filtered
}

func sourceEvidencePendingRedirectRelevantAfterBodyEvidence(brief workerruntime.WorkerRuntimeBrief, path string) bool {
	path = NormalizeRunnerPath(path)
	if path == "" {
		return false
	}

	if implementationSourcePathOverlapsObjective(path, brief.Objective) ||
		sourceEvidencePathSharesObjectiveTerm(path, brief.Objective) {

		return true
	}

	candidates := append([]string{}, brief.RequiredEditPaths...)

	candidates = append(candidates, brief.TargetRefs...)
	candidates = append(candidates, brief.EvidencePaths...)
	candidates = append(candidates, brief.ChangedPaths...)
	candidates = append(candidates, pathparse.MentionedWorkspacePaths(strings.Join(brief.UserBoundaries, "\n"))...)
	candidates = append(candidates, pathparse.MentionedWorkspacePaths(brief.Objective)...)

	for _, candidate := range candidates {
		if NormalizeRunnerPath(candidate) == path {
			return true
		}
	}

	return false
}

func sourceEvidencePathSharesObjectiveTerm(path string, objective string) bool {
	terms := RecoveryObjectiveSearchWords(objective, 32)
	if len(terms) == 0 {
		return false
	}

	objectiveTerms := make(map[string]struct{}, len(terms))
	for _, term := range terms {
		objectiveTerms[SingularObjectiveWord(term)] = struct{}{}
	}

	for _, term := range implementationPathTerms(path) {
		if _, ok := objectiveTerms[term]; ok {
			return true
		}
	}

	compactPath := strings.ToLower(strings.Join(implementationPathTerms(path), ""))
	for term := range objectiveTerms {
		if len(term) >= 4 && strings.Contains(compactPath, term) {
			return true
		}
	}

	return false
}

// sourceEvidenceObligationResolutionAttempts counts failed code_navigate
// attempts on a path. When attempts reach maxObligationResolutionAttempts
// the obligation is treated as unresolvable.
func sourceEvidenceObligationResolutionAttempts(path string, results []workspace.ToolResult) int {
	path = NormalizeRunnerPath(path)
	if path == "" {
		return 0
	}

	attempts := 0

	for _, result := range results {
		if !sourceEvidenceToolResultMentionsPath(result, path) {
			continue
		}

		tool := strings.TrimSpace(result.Tool)
		if tool != "code_navigate" && tool != "read_only_capability" {
			continue
		}

		if strings.TrimSpace(result.Error) != "" ||
			strings.Contains(strings.ToLower(result.Summary), "no generic") ||
			strings.Contains(strings.ToLower(result.Summary), "zero-hit") ||
			strings.Contains(strings.ToLower(result.Summary), "repeated dead-end") ||
			strings.Contains(strings.ToLower(result.Summary), "needs route") {

			attempts++
		}
	}

	return attempts
}

func reverseSourceEvidenceObligations(values []sourceEvidenceObligation) {
	for i, j := 0, len(values)-1; i < j; i, j = i+1, j-1 {
		values[i], values[j] = values[j], values[i]
	}
}

func sourceEvidencePendingRedirectRequestAllowed(obligation sourceEvidenceObligation, req workspace.ToolRequest) bool {
	tool := strings.TrimSpace(req.Tool)
	if tool == "" || tool != strings.TrimSpace(obligation.PendingTool) {
		return false
	}

	switch {
	case tool == "code_navigate":
		return sourceEvidenceToolSourceBodyRequest(req)
	case tool == "read_only_capability":
		return sourceEvidenceToolSourceBodyRequest(req)
	case strings.HasSuffix(tool, "_read_symbol"), strings.HasSuffix(tool, "_extract_body"):
		return sourceEvidenceRequestHasSymbolPattern(req)
	default:
		return false
	}
}

func sourceEvidenceToolSourceBodyRequest(req workspace.ToolRequest) bool {
	capability := sourceEvidenceCapabilityToken(firstSourceEvidenceString(req.Capability, req.Mode))
	switch capability {
	case "read_symbol", "inspect_symbol", "symbol_body":
		return sourceEvidenceRequestHasSymbolPattern(req)
	default:
		return false
	}
}

func sourceEvidencePendingRedirectOutlineProgressAllowed(obligation sourceEvidenceObligation, req workspace.ToolRequest, results []workspace.ToolResult) bool {
	if NormalizeRunnerPath(req.Path) != strings.TrimSpace(obligation.Path) {
		return false
	}

	if sourceEvidenceOutlineReadForPath(obligation.Path, results) {
		return false
	}

	if strings.TrimSpace(req.NamedGap) == "" && strings.TrimSpace(req.WhyNotAnswering) == "" {
		return false
	}

	tool := strings.TrimSpace(req.Tool)
	pendingTool := strings.TrimSpace(obligation.PendingTool)

	if tool == "" {
		return false
	}

	if outlineTool := sourceEvidencePendingRedirectOutlineTool(pendingTool); outlineTool != "" && tool == outlineTool {
		return true
	}

	if tool != pendingTool {
		return false
	}

	switch tool {
	case "code_navigate", "read_only_capability":
		return sourceEvidenceCapabilityIsOutline(firstSourceEvidenceString(req.Capability, req.Mode))
	default:
		return false
	}
}

func sourceEvidenceOutlineReadForPath(path string, results []workspace.ToolResult) bool {
	path = NormalizeRunnerPath(path)
	if path == "" {
		return false
	}

	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" {
			continue
		}

		tool := strings.TrimSpace(result.Tool)
		if tool != "code_navigate" && tool != "read_only_capability" && !strings.HasSuffix(tool, "_file_outline") {
			continue
		}

		if !sourceEvidenceToolResultMentionsPath(result, path) {
			continue
		}

		text := strings.ToLower(strings.Join([]string{result.Summary, result.Output}, "\n"))
		if strings.Contains(text, "capability: file_outline") ||
			strings.Contains(text, "routed file_outline") ||
			strings.Contains(text, "top-level declaration") ||
			strings.HasSuffix(tool, "_file_outline") {

			return true
		}
	}

	return false
}

func sourceEvidenceToolResultMentionsPath(result workspace.ToolResult, path string) bool {
	for _, readPath := range newToolResultEvidenceRecord(result).ReadPaths() {
		if NormalizeRunnerPath(readPath) == path {
			return true
		}
	}

	text := strings.ToLower(strings.Join([]string{result.Summary, result.Output}, "\n"))

	return strings.Contains(text, strings.ToLower(path))
}

func sourceEvidenceCapabilityIsOutline(value string) bool {
	switch sourceEvidenceCapabilityToken(value) {
	case "file_outline", "outline":
		return true
	default:
		return false
	}
}

func sourceEvidencePendingRedirectOutlineTool(tool string) string {
	tool = strings.TrimSpace(tool)
	switch {
	case strings.HasSuffix(tool, "_read_symbol"):
		return strings.TrimSuffix(tool, "_read_symbol") + "_file_outline"
	case strings.HasSuffix(tool, "_extract_body"):
		return strings.TrimSuffix(tool, "_extract_body") + "_file_outline"
	default:
		return ""
	}
}

func sourceEvidenceRequestHasSymbolPattern(req workspace.ToolRequest) bool {
	return strings.TrimSpace(req.Pattern) != ""
}

func sourceEvidenceCapabilityToken(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "-", "_")
	value = strings.ReplaceAll(value, " ", "_")

	return value
}

func firstSourceEvidenceString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}

	return ""
}

func sourceEvidencePendingRedirectTools(baseTools []string, pending []sourceEvidenceObligation) []string {
	if len(pending) == 0 {
		return nil
	}

	available := make(map[string]struct{}, len(baseTools))
	for _, tool := range baseTools {
		available[strings.TrimSpace(tool)] = struct{}{}
	}

	seen := make(map[string]struct{})

	var tools []string

	for _, obligation := range pending {
		for _, tool := range []string{
			strings.TrimSpace(obligation.PendingTool),
			sourceEvidencePendingRedirectOutlineTool(obligation.PendingTool),
		} {
			if tool == "" {
				continue
			}

			if _, ok := available[tool]; !ok {
				continue
			}

			if _, ok := seen[tool]; ok {
				continue
			}

			seen[tool] = struct{}{}
			tools = append(tools, tool)
		}
	}

	return tools
}

func sourceEvidencePendingRedirectSummary(pending []sourceEvidenceObligation, max int) string {
	if max <= 0 {
		max = len(pending)
	}

	var parts []string

	for i, obligation := range pending {
		if i >= max {
			parts = append(parts, fmt.Sprintf("+%d more", len(pending)-max))
			break
		}

		path := strings.TrimSpace(obligation.Path)
		tool := strings.TrimSpace(obligation.PendingTool)

		if path == "" || tool == "" {
			continue
		}

		parts = append(parts, fmt.Sprintf("%s via %s", path, tool))
	}

	return strings.Join(parts, ", ")
}

func sourceEvidencePendingRedirectActionSummary(pending []sourceEvidenceObligation, max int) string {
	if max <= 0 {
		max = len(pending)
	}

	var parts []string

	for i, obligation := range pending {
		if i >= max {
			parts = append(parts, fmt.Sprintf("+%d more", len(pending)-max))
			break
		}

		if label := sourceEvidencePendingRedirectActionLabel(obligation); label != "" {
			parts = append(parts, label)
		}
	}

	return strings.Join(parts, ", ")
}

func sourceEvidencePendingRedirectActionLabel(obligation sourceEvidenceObligation) string {
	path := strings.TrimSpace(obligation.Path)
	tool := strings.TrimSpace(obligation.PendingTool)

	if path == "" || tool == "" {
		return ""
	}

	switch {
	case tool == workspace.ToolNameCodeNavigate:
		return fmt.Sprintf(
			"%s via %s capability=%s with pattern/symbol, or one %s first when the symbol is unknown",
			path,
			workspace.ToolNameCodeNavigate,
			string(semanticlanguage.CapReadSymbol),
			string(semanticlanguage.CapFileOutline),
		)
	case tool == "read_only_capability":
		return fmt.Sprintf("%s via read_only_capability inspect-symbol with pattern/symbol, or one outline first when the symbol is unknown", path)
	case strings.HasSuffix(tool, "_read_symbol"), strings.HasSuffix(tool, "_extract_body"):
		if outlineTool := sourceEvidencePendingRedirectOutlineTool(tool); outlineTool != "" {
			return fmt.Sprintf("%s via %s with pattern/symbol, or one %s same-path outline first when the symbol is unknown", path, tool, outlineTool)
		}

		return fmt.Sprintf("%s via %s with pattern/symbol", path, tool)
	default:
		return fmt.Sprintf("%s via %s source-body capability", path, tool)
	}
}

func splitImplementationPathToken(token string) []string {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil
	}

	var out []string

	start := 0

	for i, r := range token {
		if i > start && r >= 'A' && r <= 'Z' {
			out = append(out, token[start:i])
			start = i
		}
	}

	out = append(out, token[start:])
	for i, value := range out {
		out[i] = strings.ToLower(value)
	}

	return out
}

func implementationSourcePathOverlapsObjective(path string, objective string) bool {
	terms := RecoveryObjectiveSearchWords(objective, 32)
	if len(terms) == 0 {
		return false
	}

	objectiveTerms := make(map[string]struct{}, len(terms))
	for _, term := range terms {
		objectiveTerms[SingularObjectiveWord(term)] = struct{}{}
	}

	matches := 0

	for _, term := range implementationPathTerms(path) {
		if _, ok := objectiveTerms[term]; ok {
			matches++
		}
	}

	return matches >= 2
}

func implementationTargetWriteReadyRedirectedSourceTool(tool string) bool {
	tool = strings.TrimSpace(tool)

	return tool == workspace.ToolNameCodeNavigate ||
		tool == workspace.ToolNameReadOnlyCapability ||
		strings.HasSuffix(tool, "_read_symbol") ||
		strings.HasSuffix(tool, "_extract_body")
}

func implementationTargetWriteReadyRedirectedRelatedSourceTool(result workspace.ToolResult) (string, bool) {
	for _, line := range strings.Split(result.Output, "\n") {
		lower := strings.ToLower(line)
		if !strings.Contains(lower, "related_capability:") ||
			!strings.Contains(lower, "read_symbol") ||
			!strings.Contains(lower, " via ") {

			continue
		}

		idx := strings.Index(lower, " via ")
		fields := strings.Fields(line[idx+len(" via "):])

		if len(fields) == 0 {
			continue
		}

		tool := strings.Trim(fields[0], "`'\".,;:")
		if implementationTargetWriteReadyRedirectedSourceTool(tool) {
			return tool, true
		}
	}

	return "", false
}
