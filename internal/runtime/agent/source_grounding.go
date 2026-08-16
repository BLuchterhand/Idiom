package agent

import (
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/autonomy"
	"idiom/internal/contracts/pathparse"
	"idiom/internal/runtime/workerruntime"
)

type sourceGroundingRejectionKind string

const (
	sourceGroundingRejectionUnknown           sourceGroundingRejectionKind = ""
	sourceGroundingRejectionMissingEvidence   sourceGroundingRejectionKind = "missing_evidence"
	sourceGroundingRejectionCandidateOnly     sourceGroundingRejectionKind = "candidate_only"
	sourceGroundingRejectionMissingCandidate  sourceGroundingRejectionKind = "missing_candidate"
	sourceGroundingRejectionUnsupportedDetail sourceGroundingRejectionKind = "unsupported_detail"
	sourceGroundingRejectionScaffold          sourceGroundingRejectionKind = "scaffold"
)

const (
	sourceGroundingCandidateOnlyMarker     = "Candidate/search/context paths are not source-body evidence"
	sourceGroundingCandidateOnlyPathMarker = "Candidate path(s):"
	sourceGroundingMissingCandidateMarker  = "not read as source-body evidence"
	sourceGroundingUnsupportedDetailMarker = "not backed by a source-body read"
)

type sourceGroundingRecoveryResolutionKind string

const (
	sourceGroundingRecoveryReadBody          sourceGroundingRecoveryResolutionKind = "read_body"
	sourceGroundingRecoveryCiteBodyBacked    sourceGroundingRecoveryResolutionKind = "cite_body_backed"
	sourceGroundingRecoveryQualifyUnverified sourceGroundingRecoveryResolutionKind = "qualify_unverified"
	sourceGroundingRecoveryProbeAlternative  sourceGroundingRecoveryResolutionKind = "probe_alternative_source"
	sourceGroundingRecoveryStateBlocker      sourceGroundingRecoveryResolutionKind = "state_exact_blocker"
	sourceGroundingRecoveryAnswerSupported   sourceGroundingRecoveryResolutionKind = "answer_from_supported_evidence"
)

type sourceGroundingRecoveryResolution struct {
	Kind       sourceGroundingRecoveryResolutionKind `json:"kind"`
	References []sourceReference                     `json:"references,omitempty"`
}

type sourceGroundingAssessment struct {
	Kind        sourceGroundingRejectionKind
	References  []sourceReference
	Resolutions []sourceGroundingRecoveryResolution
	Text        string
}

type sourceGroundingAssessmentPayload struct {
	Kind        sourceGroundingRejectionKind        `json:"kind,omitempty"`
	References  []sourceReference                   `json:"references,omitempty"`
	Resolutions []sourceGroundingRecoveryResolution `json:"resolutions,omitempty"`
	Text        string                              `json:"text,omitempty"`
}

func (a sourceGroundingAssessment) hasTypedSignal() bool {
	return a.Kind != sourceGroundingRejectionUnknown ||
		len(a.References) > 0 ||
		len(a.Resolutions) > 0 ||
		strings.TrimSpace(a.Text) != ""
}

func (a sourceGroundingAssessment) payload() sourceGroundingAssessmentPayload {
	a = normalizeSourceGroundingAssessment(a)
	return sourceGroundingAssessmentPayload{
		Kind:        a.Kind,
		References:  a.References,
		Resolutions: a.Resolutions,
		Text:        strings.TrimSpace(a.Text),
	}
}

func annotateSourceGroundingAssessment(result workspace.ToolResult, assessment sourceGroundingAssessment) workspace.ToolResult {
	if !assessment.hasTypedSignal() {
		return result
	}

	payload := assessment.payload()
	envelope := newCompletionGateRecoveryEnvelope(
		completionGateInvariantFinalAnswerAcceptance,
		string(turnViolationSourceGroundingUnmet),
		completionGateViolationSourceGrounding,
		assessment.ReadableRecoveryPaths(),
	)
	if assessment.Kind == sourceGroundingRejectionMissingEvidence {
		envelope.PendingObligationIDs = []string{completionGateObligationReadOnlyAnswerability}
	}
	envelope.SourceGrounding = &payload
	return annotateCompletionGateRecoveryEnvelope(result, envelope)
}

func sourceGroundingAssessmentFromResult(result workspace.ToolResult) (sourceGroundingAssessment, bool) {
	envelope, ok := completionGateRecoveryEnvelopeFromResult(result)
	if !ok || envelope.SourceGrounding == nil {
		return sourceGroundingAssessment{}, false
	}
	payload := *envelope.SourceGrounding

	assessment := sourceGroundingAssessment{
		Kind:        payload.Kind,
		References:  payload.References,
		Resolutions: payload.Resolutions,
		Text:        strings.TrimSpace(payload.Text),
	}
	assessment = normalizeSourceGroundingAssessment(assessment)
	if !assessment.hasTypedSignal() {
		return sourceGroundingAssessment{}, false
	}

	return assessment, true
}

func sourceGroundingCandidateOnlyAssessment(paths []string) sourceGroundingAssessment {
	return newSourceGroundingAssessment(sourceGroundingRejectionCandidateOnly, sourceReferencesForPaths(nonEmptyRunnerStrings(paths, 8)), "")
}

func sourceGroundingMissingCandidateReferenceAssessment(references []sourceReference) sourceGroundingAssessment {
	return newSourceGroundingAssessment(sourceGroundingRejectionMissingCandidate, references, "")
}

func sourceGroundingUnsupportedDetailReferenceAssessment(references []sourceReference) sourceGroundingAssessment {
	return newSourceGroundingAssessment(sourceGroundingRejectionUnsupportedDetail, references, "")
}

func sourceGroundingMissingEvidenceAssessment(text string) sourceGroundingAssessment {
	return newSourceGroundingAssessment(sourceGroundingRejectionMissingEvidence, nil, text)
}

func sourceGroundingScaffoldAssessment(text string) sourceGroundingAssessment {
	return newSourceGroundingAssessment(sourceGroundingRejectionScaffold, nil, text)
}

func newSourceGroundingAssessment(kind sourceGroundingRejectionKind, references []sourceReference, text string) sourceGroundingAssessment {
	assessment := sourceGroundingAssessment{
		Kind:       kind,
		References: references,
		Text:       strings.TrimSpace(text),
	}
	assessment.Resolutions = sourceGroundingRecoveryResolutions(kind, references)
	return normalizeSourceGroundingAssessment(assessment)
}

func sourceGroundingRecoveryResolutions(kind sourceGroundingRejectionKind, references []sourceReference) []sourceGroundingRecoveryResolution {
	readable := filterSourceReferences(references, func(reference sourceReference) bool {
		return reference.BodyReadable()
	})
	var resolutions []sourceGroundingRecoveryResolution
	if len(readable) > 0 {
		resolutions = append(resolutions, sourceGroundingRecoveryResolution{
			Kind:       sourceGroundingRecoveryReadBody,
			References: readable,
		})
	}

	switch kind {
	case sourceGroundingRejectionCandidateOnly:
		resolutions = append(resolutions,
			sourceGroundingRecoveryResolution{Kind: sourceGroundingRecoveryProbeAlternative},
			sourceGroundingRecoveryResolution{Kind: sourceGroundingRecoveryStateBlocker},
		)
	case sourceGroundingRejectionMissingCandidate, sourceGroundingRejectionUnsupportedDetail:
		resolutions = append(resolutions,
			sourceGroundingRecoveryResolution{Kind: sourceGroundingRecoveryCiteBodyBacked},
			sourceGroundingRecoveryResolution{Kind: sourceGroundingRecoveryQualifyUnverified},
			sourceGroundingRecoveryResolution{Kind: sourceGroundingRecoveryProbeAlternative},
			sourceGroundingRecoveryResolution{Kind: sourceGroundingRecoveryStateBlocker},
		)
	case sourceGroundingRejectionMissingEvidence:
		resolutions = append(resolutions,
			sourceGroundingRecoveryResolution{Kind: sourceGroundingRecoveryProbeAlternative},
			sourceGroundingRecoveryResolution{Kind: sourceGroundingRecoveryStateBlocker},
		)
	case sourceGroundingRejectionScaffold:
		resolutions = append(resolutions,
			sourceGroundingRecoveryResolution{Kind: sourceGroundingRecoveryAnswerSupported},
			sourceGroundingRecoveryResolution{Kind: sourceGroundingRecoveryProbeAlternative},
			sourceGroundingRecoveryResolution{Kind: sourceGroundingRecoveryStateBlocker},
		)
	}

	return resolutions
}

func normalizeSourceGroundingAssessment(assessment sourceGroundingAssessment) sourceGroundingAssessment {
	assessment.References = normalizeSourceReferences(assessment.References)
	assessment.Text = strings.TrimSpace(assessment.Text)

	seen := make(map[sourceGroundingRecoveryResolutionKind]struct{}, len(assessment.Resolutions))
	resolutions := make([]sourceGroundingRecoveryResolution, 0, len(assessment.Resolutions))
	for _, resolution := range assessment.Resolutions {
		if resolution.Kind == "" {
			continue
		}
		if _, ok := seen[resolution.Kind]; ok {
			continue
		}
		resolution.References = normalizeSourceReferences(resolution.References)
		if resolution.Kind == sourceGroundingRecoveryReadBody {
			resolution.References = filterSourceReferences(resolution.References, func(reference sourceReference) bool {
				return reference.BodyReadable()
			})
			if len(resolution.References) == 0 {
				continue
			}
		}
		seen[resolution.Kind] = struct{}{}
		resolutions = append(resolutions, resolution)
	}
	assessment.Resolutions = resolutions
	return assessment
}

func sourceReferencesForPaths(paths []string) []sourceReference {
	references := make([]sourceReference, 0, len(paths))
	for _, path := range paths {
		references = append(references, sourceReferenceForPath(path))
	}
	return normalizeSourceReferences(references)
}

func (a sourceGroundingAssessment) ReferencePaths() []string {
	return sourceReferencePaths(a.References)
}

func (a sourceGroundingAssessment) ReadableRecoveryPaths() []string {
	for _, resolution := range a.Resolutions {
		if resolution.Kind == sourceGroundingRecoveryReadBody {
			return sourceReferencePaths(resolution.References)
		}
	}
	return nil
}

func (a sourceGroundingAssessment) BlocksFinalAnswer() bool {
	return strings.TrimSpace(a.Message()) != ""
}

func (a sourceGroundingAssessment) Message() string {
	message := a.rejectionSummary()
	if message == "" {
		return ""
	}
	if options := a.recoveryOptionText(); len(options) > 0 {
		message += " Valid recovery: " + strings.Join(options, "; ") + "."
	}
	return message
}

func (a sourceGroundingAssessment) rejectionSummary() string {
	paths := a.ReferencePaths()
	switch a.Kind {
	case sourceGroundingRejectionMissingEvidence:
		return strings.TrimSpace(a.Text)
	case sourceGroundingRejectionCandidateOnly:
		pathSummary := strings.Join(paths, ", ")
		if pathSummary == "" {
			pathSummary = "(candidate path unavailable)"
		}
		return "read-only workspace answers require bounded source-body or diagnostic evidence tied to the user's objective before a final answer. " + sourceGroundingCandidateOnlyMarker + ". " + sourceGroundingCandidateOnlyPathMarker + " " + pathSummary + "."
	case sourceGroundingRejectionMissingCandidate:
		return "the answer cites body-readable file/document reference(s) that were discovered as index candidates but " + sourceGroundingMissingCandidateMarker + ": " + quotedRunnerStrings(paths) + "."
	case sourceGroundingRejectionUnsupportedDetail:
		return "the answer cites source path/line/symbol detail " + sourceGroundingUnsupportedDetailMarker + ": " + quotedRunnerStrings(paths) + "."
	case sourceGroundingRejectionScaffold:
		return strings.TrimSpace(a.Text)
	default:
		return ""
	}
}

func (a sourceGroundingAssessment) RecoveryPrompt(ledgerGuidance string) string {
	if !a.BlocksFinalAnswer() {
		return ""
	}

	lines := []string{"The completion gate rejected the answer: " + a.rejectionSummary()}
	if options := a.recoveryOptionText(); len(options) > 0 {
		lines = append(lines, "Valid next strategies:\n- "+strings.Join(options, "\n- "))
	}
	lines = append(lines, "Do not answer with only an evidence-path inventory, readiness summary, or gate-satisfaction statement; answer the user's actual question using only supported evidence.")
	if strings.TrimSpace(ledgerGuidance) != "" {
		lines = append(lines, strings.TrimSpace(ledgerGuidance))
	}
	lines = append(lines, "Do not broaden into new search/discovery unless you can name the exact missing claim that the current evidence ledger cannot answer.")

	return strings.Join(lines, "\n\n")
}

func (a sourceGroundingAssessment) recoveryOptionText() []string {
	var options []string
	for _, resolution := range normalizeSourceGroundingAssessment(a).Resolutions {
		switch resolution.Kind {
		case sourceGroundingRecoveryReadBody:
			options = append(options, "read one body-readable file/document with a bounded `read_file` or `code_navigate` request: "+formatInlinePathList(sourceReferencePaths(resolution.References), 4))
		case sourceGroundingRecoveryCiteBodyBacked:
			options = append(options, "rewrite the answer so factual source citations name only body-backed files/documents")
		case sourceGroundingRecoveryQualifyUnverified:
			options = append(options, "qualify an unsupported file/document reference as `unverified` next to the claim")
		case sourceGroundingRecoveryProbeAlternative:
			options = append(options, "make one materially different source-owner probe for the exact missing claim")
		case sourceGroundingRecoveryStateBlocker:
			options = append(options, "answer `<<QUESTION>>:` with the exact unavailable evidence source")
		case sourceGroundingRecoveryAnswerSupported:
			options = append(options, "answer the user's actual question from the supported evidence instead of describing the evidence scaffold")
		}
	}
	return options
}

func (r *runExecution) assessSourceGrounding(answer string, results []workspace.ToolResult) sourceGroundingAssessment {
	if r == nil {
		return sourceGroundingAssessment{}
	}

	brief := workerruntime.NormalizeRuntimeBrief(r.state.runtimeBrief)
	readOnlyWorkspace := workerruntime.PermissionProfileIsReadOnlyWorkspace(brief.PermissionProfile)
	citationGateApplies := sourceGroundingGateApplies(brief) || runnerBriefRequiresMutation(brief)
	evidenceFloorApplies := readOnlyWorkspace && (sourceGroundingGateApplies(brief) ||
		readOnlyExplanationGateApplies(brief) ||
		(brief.Readiness != nil &&
			!brief.Readiness.ReadyToAnswer &&
			len(genericReadOnlyEvidenceTools(brief.AllowedTools)) > 0))
	if !citationGateApplies && !evidenceFloorApplies {
		return sourceGroundingAssessment{}
	}

	if strings.HasPrefix(strings.TrimSpace(answer), "<<QUESTION>>") {
		return sourceGroundingAssessment{}
	}

	projection := newToolResultEvidenceProjection(results)
	if conflicts := projection.ConflictingDiagnosticLogReferences(answer); len(conflicts) > 0 {
		return sourceGroundingUnsupportedDetailReferenceAssessment(conflicts)
	}

	if evidenceFloorApplies {
		if readOnlyScaffoldFinalAnswer(answer) {
			return sourceGroundingScaffoldAssessment("the answer is only a retained-context or evidence-scaffold summary. Final answers must state the concrete behavior supported by relevant source-body or diagnostic evidence, not only list evidence surfaces.")
		}

		if assessment := r.readOnlyEvidenceSourceGroundingAssessment(brief, answer, projection); assessment.BlocksFinalAnswer() {
			return assessment
		}
	}

	if !citationGateApplies {
		return sourceGroundingAssessment{}
	}

	validationLedgerEvidence := r.validationStatusAnswerCanUseLedgerEvidence(answer, results)

	citations := analyzeSourceCitations(answer)
	if escaping := sourceGroundingUnsupportedEscapingReferences(answer, citations.EscapingReferences(), projection); len(escaping) > 0 {
		return sourceGroundingUnsupportedDetailReferenceAssessment(escaping)
	}

	references := normalizeSourceReferences(append(citations.BodyReadableReferences(), projection.DiagnosticLogReferences(answer)...))
	if len(references) == 0 {
		return sourceGroundingAssessment{}
	}
	mutationClaimPaths := r.receiptBackedChangedPathClaims(answer)

	var (
		missing     []sourceReference
		unsupported []sourceReference
	)

	for _, reference := range references {
		path := NormalizeRunnerPath(reference.Path)
		if path == "" {
			continue
		}
		if projection.DiagnosticLogReadCoversPath(path, 0, 0) {
			if reference.LineRange == nil ||
				(reference.LineRange.Valid() && projection.DiagnosticLogReadCoversPath(path, reference.LineRange.Start, reference.LineRange.End)) {

				continue
			}
			missing = append(missing, reference)
			continue
		}

		if sourceCitationAmbiguousBodyEvidence(r, path, projection) {
			missing = append(missing, reference)
			continue
		}

		citationPath := path
		if resolved, ok := sourceCitationCanonicalPath(r, path, projection); ok {
			citationPath = resolved
		}
		if reference.LineRange == nil &&
			!sourceCitationClaimsSourceDetail(answer, path) &&
			(mutationClaimPaths[path] || mutationClaimPaths[citationPath]) {

			continue
		}
		hasWorkspaceWindow := projection.HasSourceWindow(citationPath)
		if (hasWorkspaceWindow || reference.LineRange != nil) &&
			!sourceCitationBackedByCurrentWindow(r, citationPath, reference.LineRange, projection) {
			missing = append(missing, reference)
			continue
		}
		if len(projection.ExternalDocumentBodyMatches(path)) > 0 || filepath.IsAbs(citationPath) {
			reference.Kind = sourceReferenceExternalDocument
		}

		if validationLedgerEvidence {
			if r.validationStatusPathBackedByLedger(path, citationPath, results) &&
				!sourceCitationClaimsSourceDetail(answer, path) {

				continue
			}
		}

		if projection.HasSourceWindow(citationPath) ||
			len(projection.ExternalDocumentBodyMatches(citationPath)) > 0 ||
			slices.Contains(projection.ReadOnlySourceWindowEvidencePaths(), citationPath) {
			continue
		}

		if !sourceCitationExists(r, citationPath) {
			if !strings.Contains(path, "/") &&
				!sourceCitationLooksLikeRootPath(path) &&
				!sourceCitationHasLineSuffix(answer, path) &&
				!sourceCitationClaimsSourceDetail(answer, path) {

				continue
			}

			authority := featurePlannerPlanPathCitationAuthority(r, answer, path, results)
			if !sourceCitationExplicitlyNonCurrent(answer, path) &&
				!sourceCitationExplicitlyUnverified(answer, path) &&
				authority.Kind != featurePlannerPlanCitationArtifactProposed &&
				authority.Kind != featurePlannerPlanCitationFutureRecommendation {
				missing = append(missing, reference)
			}

			continue
		}

		if !sourceGroundingPathRequiresBody(path) {
			continue
		}

		if !sourceCitationClaimsSourceDetail(answer, path) {
			continue
		}

		authority := featurePlannerPlanPathCitationAuthority(r, answer, path, results)
		if sourceCitationExplicitlyNonCurrent(answer, path) ||
			authority.Kind == featurePlannerPlanCitationArtifactProposed ||
			sourceCitationExplicitlyUnverified(answer, path) {
			continue
		}

		reference.Path = citationPath
		unsupported = append(unsupported, reference)
	}

	if len(missing) > 0 {
		return sourceGroundingMissingCandidateReferenceAssessment(missing)
	}

	if len(unsupported) > 0 {
		return sourceGroundingUnsupportedDetailReferenceAssessment(unsupported)
	}

	return sourceGroundingAssessment{}
}

// receiptBackedChangedPathClaims returns only paths whose appearance in the
// final answer's Changed paths section is backed by the run's authoritative
// mutation receipts. This authority covers the fact that a path changed; it
// does not authorize line, symbol, or implementation-detail claims.
func (r *runExecution) receiptBackedChangedPathClaims(answer string) map[string]bool {
	backed := make(map[string]bool)
	if r == nil {
		return backed
	}

	committed := make(map[string]struct{})
	for _, id := range r.state.committedMutationEffectIDs() {
		committed[id] = struct{}{}
	}
	for _, claim := range claimedChangedPathsFromAnswer(answer) {
		claim = cleanRelativeRunnerPath(claim)
		if claim == "" {
			continue
		}

		canonicalID := claim
		if r.state.mutation.bound {
			projection := r.state.projectMutationClaims([]string{claim})
			if len(projection.UnsupportedPaths) > 0 || len(projection.CanonicalIDs) != 1 {
				continue
			}
			canonicalID = projection.CanonicalIDs[0]
		}
		if _, ok := committed[canonicalID]; !ok {
			continue
		}
		backed[claim] = true
		backed[canonicalID] = true
	}

	return backed
}

func (r *runExecution) readOnlyEvidenceSourceGroundingAssessment(brief workerruntime.WorkerRuntimeBrief, answer string, projection toolResultEvidenceProjection) sourceGroundingAssessment {
	brief = workerruntime.NormalizeRuntimeBrief(brief)
	if brief.Readiness != nil && brief.Readiness.ReadyToAnswer {
		return sourceGroundingAssessment{}
	}

	closeout := readOnlyClosureDecisionForProjection(brief, projection)
	directAnswerCloseout := ReadOnlyCloseout{}
	if readOnlyDirectAnswerLane(brief) {
		directAnswerCloseout = readOnlyClosureDecisionForProjection(
			readOnlyDirectAnswerAcceptanceLedgerBrief(brief),
			projection,
		)
	}
	directCandidateOnly := readOnlyDirectAnswerLane(brief) && directAnswerCloseout.CandidateOnlyEvidence()
	if readOnlyDirectAnswerLane(brief) && directAnswerCloseout.Answerable() &&
		len(directAnswerCloseout.Evidence.RelevantExternalWebSources) > 0 &&
		!answerCitesRelevantExternalWebSource(answer, directAnswerCloseout.Evidence) {
		return sourceGroundingMissingEvidenceAssessment("internet-research answers must cite a canonical URL whose fetched body supports the user's objective.")
	}
	if brief.Readiness == nil &&
		!readOnlyExplanationGateApplies(brief) &&
		!workerruntime.TaskFamilyMatches(brief.TaskFamily, autonomy.TaskFamilyDirectAnswer) &&
		!directCandidateOnly {

		return sourceGroundingAssessment{}
	}

	if len(genericReadOnlyEvidenceTools(brief.AllowedTools)) == 0 {
		return sourceGroundingAssessment{}
	}

	if closeout.Answerable() {
		return sourceGroundingAssessment{}
	}

	if readOnlyDirectAnswerLane(brief) && readOnlyAnswerReportsInsufficientEvidence(answer) {
		ledger := directAnswerCloseout.Evidence
		if len(ledger.SourceBodyPaths) > 0 || len(ledger.ExternalDocumentBodyPaths) > 0 || ledger.DiagnosticEvidence {
			return sourceGroundingAssessment{}
		}
	}

	diagnosticArtifactSupported := sourceGroundingDiagnosticArtifactCitationSupportsObjective(brief, answer, projection)
	if readOnlyDirectAnswerLane(brief) && directAnswerCloseout.Answerable() {
		return sourceGroundingAssessment{}
	}

	if diagnosticArtifactSupported {
		return sourceGroundingAssessment{}
	}

	if workerruntime.ConstraintPresent(brief.Constraints, workerruntime.ConstraintAnswerReadinessReached) {
		return sourceGroundingAssessment{}
	}

	if workerruntime.ConstraintPresent(brief.Constraints, workerruntime.ConstraintChildPacketAvailable) {
		return sourceGroundingAssessment{}
	}

	if len(intersectRunnerPaths(
		readOnlySourceLikePaths(pathparse.MentionedWorkspacePaths(answer), 16),
		projection.delegatedClosureEvidencePaths(brief.Objective, 8),
	)) > 0 {
		return sourceGroundingAssessment{}
	}

	if projection.hasAnswerableDelegatedPacket(brief) {
		return sourceGroundingAssessment{}
	}

	if directCandidateOnly {
		if candidates := closurePromptEvidencePaths(directAnswerCloseout.Evidence.CandidatePaths, 4); len(candidates) > 0 {
			return sourceGroundingCandidateOnlyAssessment(candidates)
		}
	}

	if readPaths := sortedRunnerPathKeys(projection.ReadPaths()); len(readPaths) > 0 {
		return sourceGroundingMissingEvidenceAssessment("read-only workspace answers require bounded source-body or diagnostic evidence tied to the user's objective before a final answer. Already inspected path(s) should not be reread: " + strings.Join(readPaths, ", ") + ".")
	}

	return sourceGroundingMissingEvidenceAssessment("read-only workspace answers require bounded source-body or diagnostic evidence tied to the user's objective before a final answer.")
}

func sourceGroundingStripGateRecoveryPrefix(text string) string {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "[gate retry") &&
		!strings.HasPrefix(text, "[source evidence recovery") {

		return text
	}

	if idx := strings.Index(text, "]"); idx >= 0 {
		return strings.TrimSpace(text[idx+1:])
	}

	return text
}

func sourceCitationCanonicalPath(r *runExecution, path string, projection toolResultEvidenceProjection) (string, bool) {
	path = NormalizeRunnerPath(path)
	if path == "" {
		return "", false
	}
	if r != nil && r.state.mutation.bound {
		claims := r.state.mutation.canonicalScopeProjection.ProjectClaims([]string{path})
		if len(claims.UnsupportedPaths) > 0 || len(claims.CanonicalIDs) != 1 {
			return path, false
		}
		canonical := NormalizeRunnerPath(claims.CanonicalIDs[0])
		if canonical == "" || !sourceCitationHasExactWorkspaceBodyEvidence(r, canonical, projection) {
			return canonical, false
		}
		return canonical, true
	}

	if sourceCitationHasExactWorkspaceBodyEvidence(r, path, projection) {
		return path, true
	}

	externalMatches := projection.ExternalDocumentBodyMatches(path)
	if strings.Contains(path, "/") || sourceCitationLooksLikeRootPath(path) {
		if len(externalMatches) == 1 {
			return externalMatches[0], true
		}
		return path, false
	}

	matches := sourceCitationWorkspaceBodyMatches(r, path, projection)
	if len(matches) == 1 && len(externalMatches) == 0 {
		return matches[0], true
	}
	if len(matches) == 0 && len(externalMatches) == 1 {
		return externalMatches[0], true
	}

	return path, false
}

func sourceCitationBackedByCurrentWindow(r *runExecution, path string, lineRange *sourceCitationRange, projection toolResultEvidenceProjection) bool {
	path = NormalizeRunnerPath(path)
	if path == "" || (lineRange != nil && !lineRange.Valid()) || r == nil || r.runner == nil || r.runner.Workspace == nil {
		return false
	}

	for _, record := range projection.records {
		if record.failed {
			continue
		}
		for _, evidence := range record.SourceWindowFacts(path) {
			if lineRange != nil && (evidence.StartLine > lineRange.Start || evidence.EndLine < lineRange.End) {
				continue
			}
			current, err := r.runner.Workspace.SourceWindowCurrent(r.state.ctx, evidence)
			if err == nil && current {
				return true
			}
		}
	}

	return false
}

func sourceCitationAmbiguousBodyEvidence(r *runExecution, path string, projection toolResultEvidenceProjection) bool {
	externalMatches := projection.ExternalDocumentBodyMatches(path)
	if len(externalMatches) == 0 {
		return false
	}

	if sourceCitationExists(r, path) {
		return !sourceCitationHasExactWorkspaceBodyEvidence(r, path, projection)
	}

	if strings.Contains(path, "/") || sourceCitationLooksLikeRootPath(path) {
		return len(externalMatches) > 1
	}

	return len(sourceCitationWorkspaceBodyMatches(r, path, projection))+len(externalMatches) > 1
}

func sourceCitationHasExactWorkspaceBodyEvidence(r *runExecution, path string, projection toolResultEvidenceProjection) bool {
	return projection.HasSourceWindow(path) ||
		slices.Contains(projection.ReadOnlySourceWindowEvidencePaths(), path)
}

func sourceCitationWorkspaceBodyMatches(r *runExecution, path string, projection toolResultEvidenceProjection) []string {
	base := strings.ToLower(filepath.Base(path))
	var matches []string
	for _, candidate := range projection.ReadOnlySourceWindowEvidencePaths() {
		candidate = NormalizeRunnerPath(candidate)
		if candidate == "" || strings.EqualFold(candidate, path) {
			continue
		}

		if strings.ToLower(filepath.Base(candidate)) != base {
			continue
		}

		matches = append(matches, candidate)
	}

	return uniqueSortedRunnerPaths(matches)
}

func sourceGroundingUnsupportedEscapingReferences(answer string, references []sourceReference, projection toolResultEvidenceProjection) []sourceReference {
	if len(references) == 0 {
		return nil
	}

	var unsupported []sourceReference
	for _, reference := range references {
		path := reference.Path
		if reference.LineRange == nil && projection.DiagnosticLogReadCoversPath(path, 0, 0) ||
			reference.LineRange != nil && reference.LineRange.Valid() &&
				projection.DiagnosticLogReadCoversPath(path, reference.LineRange.Start, reference.LineRange.End) {
			continue
		}
		if len(projection.ExternalDocumentBodyMatches(path)) > 0 {
			continue
		}
		if sourceCitationExplicitlyUnverified(answer, path) || sourceCitationExplicitlyNonCurrent(answer, path) {
			continue
		}
		unsupported = append(unsupported, reference)
	}

	return normalizeSourceReferences(unsupported)
}

func sourceGroundingGateApplies(brief workerruntime.WorkerRuntimeBrief) bool {
	if !workerruntime.PermissionProfileIsReadOnlyWorkspace(brief.PermissionProfile) {
		return false
	}

	if readOnlyDirectAnswerLane(brief) {
		return true
	}

	lowerObjective := strings.ToLower(strings.Join(strings.Fields(brief.Objective), " "))
	if strings.Contains(lowerObjective, "root cause") ||
		strings.Contains(lowerObjective, "debug") ||
		strings.Contains(lowerObjective, "failure") {

		return true
	}

	return false
}

func sourceCitationExists(r *runExecution, path string) bool {
	path = NormalizeRunnerPath(path)
	if path == "" || strings.HasPrefix(path, "../") || path == ".." || filepath.IsAbs(path) {
		return false
	}
	return false
}

func sourceGroundingPathRequiresBody(path string) bool {
	switch strings.ToLower(filepath.Ext(filepath.ToSlash(path))) {
	case ".go", ".c", ".cc", ".cpp", ".cxx", ".h", ".hh", ".hpp", ".hxx",
		".py", ".pyi", ".ts", ".tsx", ".js", ".jsx", ".rs", ".java",
		".kt", ".kts", ".sh", ".bash", ".zsh", ".fish", ".ps1", ".gd", ".astro":
		return true
	default:
		return false
	}
}

func sourceCitationLooksLikeRootPath(path string) bool {
	base := strings.ToLower(strings.TrimSpace(filepath.ToSlash(path)))
	switch base {
	case "go.mod", "go.sum", "makefile", "dockerfile", "readme.md", "agents.md",
		"package.json", "package-lock.json", "pnpm-lock.yaml", "yarn.lock",
		"pyproject.toml", "poetry.lock", "cargo.toml", "cargo.lock":
		return true
	default:
		return false
	}
}

func sourceCitationHasLineSuffix(answer string, path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}

	return regexp.MustCompile(regexp.QuoteMeta(path) + `:\d+`).MatchString(answer)
}

func sourceCitationClaimsSourceDetail(answer string, path string) bool {
	window := sourceCitationWindow(answer, path)
	lower := strings.ToLower(window)

	if strings.Contains(lower, "line ") || strings.Contains(lower, "lines ") ||
		strings.Contains(lower, "symbol") || strings.Contains(lower, "function") ||
		strings.Contains(lower, "method") || strings.Contains(lower, "handler") ||
		strings.Contains(lower, "root cause") {

		return true
	}

	if regexp.MustCompile(regexp.QuoteMeta(path) + `:\d+`).MatchString(answer) {
		return true
	}

	for _, token := range backtickedCodeLikeTokens(window) {
		if sourceGroundingTokenLooksLikeSymbol(token) {
			return true
		}
	}

	return false
}

func sourceCitationExplicitlyNonCurrent(answer string, path string) bool {
	lower := strings.ToLower(sourceCitationWindow(answer, path))
	for _, phrase := range []string{
		"does not exist", "do not exist", "doesn't exist",
		"not in the current checkout", "not current",
		"not in the workspace", "outside the workspace", "outside workspace",
		"historical", "log-only", "from the log", "in the log",
	} {
		if strings.Contains(lower, phrase) {
			return true
		}
	}

	return false
}

type featurePlannerPlanCitationKind string

const (
	featurePlannerPlanCitationNone                 featurePlannerPlanCitationKind = ""
	featurePlannerPlanCitationArtifactProposed     featurePlannerPlanCitationKind = "artifact_proposed"
	featurePlannerPlanCitationFutureRecommendation featurePlannerPlanCitationKind = "future_recommendation"
)

type featurePlannerPlanCitationAuthority struct {
	Kind                 featurePlannerPlanCitationKind
	ResolvedPlanPath     string
	BackedByPlanArtifact bool
}

type featurePlannerPlanArtifactCitationContext struct {
	Objective            string
	ProposedPaths        []string
	BackedByPlanArtifact bool
	AnswerHasPlanContext bool
}

func featurePlannerPlanArtifactCitationContextForAnswer(answer string, objective string, proposedPaths []string, backedByPlanArtifact bool) featurePlannerPlanArtifactCitationContext {
	return featurePlannerPlanArtifactCitationContext{
		Objective:            strings.TrimSpace(objective),
		ProposedPaths:        proposedPaths,
		BackedByPlanArtifact: backedByPlanArtifact,
		AnswerHasPlanContext: answerHasFeaturePlannerPlanArtifactContext(answer, objective),
	}
}

func featurePlannerPlanPathCitationAuthority(r *runExecution, answer string, path string, results []workspace.ToolResult) featurePlannerPlanCitationAuthority {
	if r == nil {
		return featurePlannerPlanCitationAuthority{}
	}

	path = NormalizeRunnerPath(path)
	if path == "" {
		return featurePlannerPlanCitationAuthority{}
	}

	for _, result := range results {
		if !featurePlannerPlanArtifactResult(result) {
			continue
		}

		brief := workerruntime.NormalizeRuntimeBrief(r.state.runtimeBrief)
		ctx := featurePlannerPlanArtifactCitationContextForAnswer(answer, brief.Objective, featurePlannerPlanArtifactProposedPaths(result), true)
		authority := featurePlannerPlanArtifactPathCitationAuthority(answer, path, ctx)
		if authority.Kind != featurePlannerPlanCitationArtifactProposed {
			continue
		}

		resolved := authority.ResolvedPlanPath
		if sourceCitationClaimsPlanResult(answer, path) || sourceCitationClaimsPlanResult(answer, resolved) {
			return featurePlannerPlanCitationAuthority{}
		}
		if (sourceCitationExists(r, path) || sourceCitationExists(r, resolved)) &&
			(sourceCitationClaimsSourceDetail(answer, path) || sourceCitationClaimsSourceDetail(answer, resolved)) {

			return featurePlannerPlanCitationAuthority{}
		}

		return authority
	}

	if sourceCitationClaimsPlanResult(answer, path) ||
		sourceCitationClaimsSourceDetail(answer, path) ||
		sourceCitationClaimsCurrentOrPlannedPath(answer, path) ||
		!sourceCitationExplicitlyFutureRecommendation(answer, path) {

		return featurePlannerPlanCitationAuthority{}
	}

	brief := workerruntime.NormalizeRuntimeBrief(r.state.runtimeBrief)
	objectiveText := strings.ToLower(strings.Join(strings.Fields(brief.Objective), " "))
	if !objectiveAsksAboutActivePlan(objectiveText) || !objectiveAsksAboutPlanExpansion(objectiveText) {
		return featurePlannerPlanCitationAuthority{}
	}

	if path == "" || !strings.Contains(path, "/") || featurePlannerPlanMetadataPath(path) {
		return featurePlannerPlanCitationAuthority{}
	}

	for _, result := range results {
		if featurePlannerPlanArtifactMatchesObjective(brief.Objective, result) {
			return featurePlannerPlanCitationAuthority{
				Kind:                 featurePlannerPlanCitationFutureRecommendation,
				BackedByPlanArtifact: true,
			}
		}
	}

	return featurePlannerPlanCitationAuthority{}
}

func featurePlannerPlanArtifactPathCitationAuthority(answer string, path string, ctx featurePlannerPlanArtifactCitationContext) featurePlannerPlanCitationAuthority {
	resolved, ok := featurePlannerPlanProposedPathForCitation(path, ctx.ProposedPaths)
	if !ok {
		return featurePlannerPlanCitationAuthority{}
	}

	hasLocalPlanContext := sourceCitationExplicitlyProposedPlanPath(answer, path) ||
		sourceCitationExplicitlyProposedPlanPath(answer, resolved)
	if !hasLocalPlanContext && !ctx.AnswerHasPlanContext {
		return featurePlannerPlanCitationAuthority{}
	}

	if sourceCitationClaimsPlanResult(answer, path) ||
		sourceCitationClaimsPlanResult(answer, resolved) ||
		sourceCitationClaimsCurrentSourcePath(answer, path) ||
		sourceCitationClaimsCurrentSourcePath(answer, resolved) {

		return featurePlannerPlanCitationAuthority{}
	}

	return featurePlannerPlanCitationAuthority{
		Kind:                 featurePlannerPlanCitationArtifactProposed,
		ResolvedPlanPath:     resolved,
		BackedByPlanArtifact: ctx.BackedByPlanArtifact,
	}
}

func featurePlannerPlanProposedPathForCitation(citation string, proposedPaths []string) (string, bool) {
	citation = NormalizeRunnerPath(citation)
	if citation == "" || len(proposedPaths) == 0 {
		return "", false
	}

	for _, proposed := range proposedPaths {
		proposed = NormalizeRunnerPath(proposed)
		if proposed == "" {
			continue
		}
		if proposed == citation {
			return proposed, true
		}
	}

	if strings.Contains(citation, "/") || sourceCitationLooksLikeRootPath(citation) {
		return "", false
	}

	base := strings.ToLower(filepath.Base(filepath.ToSlash(citation)))
	var matches []string
	for _, proposed := range proposedPaths {
		proposed = NormalizeRunnerPath(proposed)
		if proposed == "" {
			continue
		}
		if strings.ToLower(filepath.Base(filepath.ToSlash(proposed))) == base {
			matches = append(matches, proposed)
		}
	}

	matches = uniqueSortedRunnerPaths(matches)
	if len(matches) != 1 {
		return "", false
	}

	return matches[0], true
}

func answerHasFeaturePlannerPlanArtifactContext(answer string, objective string) bool {
	lowerAnswer := " " + strings.ToLower(strings.Join(strings.Fields(answer), " ")) + " "
	if ContainsAny(lowerAnswer,
		" feature-planner artifact ", " feature planner artifact ",
		" active plan ", " proposed plan ", " current plan ",
		" planned tests ", " planned test files ", " planned files ",
		" proposed tests ", " proposed test files ", " proposed files ",
		" plan proposes ", " plan defines ", " plan includes ",
		" from the plan ", " in the plan ",
		" what tests are planned ", " what tests are currently planned ",
	) {
		return true
	}

	objectiveText := normalizedPlanObjectiveText(objective)
	return objectiveAsksAboutActivePlan(objectiveText)
}

func proposedPlanPathCitationsAreRelevant(answer string, mentioned []string, ledger readOnlyAnswerabilityLedger) bool {
	if len(ledger.PlanArtifactPaths) == 0 {
		return false
	}

	ctx := featurePlannerPlanArtifactCitationContextForAnswer(answer, ledger.Objective, ledger.PlanArtifactPaths, true)
	for _, path := range mentioned {
		if featurePlannerPlanArtifactPathCitationAuthority(answer, path, ctx).Kind == featurePlannerPlanCitationArtifactProposed {
			return true
		}
	}

	return false
}

func featurePlannerPlanPathCitationsAreRelevant(answer string, mentioned []string, ledger readOnlyAnswerabilityLedger) bool {
	if proposedPlanPathCitationsAreRelevant(answer, mentioned, ledger) {
		return true
	}

	return futurePlanRecommendationCitationsAreRelevant(answer, mentioned, ledger)
}

func futurePlanRecommendationCitationsAreRelevant(answer string, mentioned []string, ledger readOnlyAnswerabilityLedger) bool {
	if len(ledger.PlanArtifactPaths) == 0 {
		return false
	}

	objectiveText := strings.ToLower(strings.Join(strings.Fields(ledger.Objective), " "))
	if !objectiveAsksAboutActivePlan(objectiveText) || !objectiveAsksAboutPlanExpansion(objectiveText) {
		return false
	}

	for _, path := range mentioned {
		path = NormalizeRunnerPath(path)
		if path == "" || !strings.Contains(path, "/") || featurePlannerPlanMetadataPath(path) {
			continue
		}
		if sourceCitationClaimsPlanResult(answer, path) ||
			sourceCitationClaimsSourceDetail(answer, path) ||
			sourceCitationClaimsCurrentOrPlannedPath(answer, path) ||
			!sourceCitationExplicitlyFutureRecommendation(answer, path) {

			continue
		}

		return true
	}

	return false
}

func sourceCitationExplicitlyFutureRecommendation(answer string, path string) bool {
	lower := strings.ToLower(sourceCitationWindow(answer, path))
	return ContainsAny(lower,
		"i would add", "i'd add", "would add", "should add", "also add",
		"recommend adding", "recommend add", "recommended addition", "recommended additions",
		"future test", "future tests", "future file", "future files",
		"new test file", "new test files", "new file to add", "new files to add",
		"additional test", "additional tests", "additional file", "additional files",
		"expand with", "expand the", "expansion test", "expansion tests",
		"files to add", "tests to add",
	)
}

func sourceCitationClaimsCurrentOrPlannedPath(answer string, path string) bool {
	if sourceCitationClaimsCurrentSourcePath(answer, path) {
		return true
	}

	lower := strings.ToLower(sourceCitationLocalWindow(answer, path, 80, 140))
	return ContainsAny(lower,
		"planned tests include", "planned test includes", "planned files include", "planned file includes",
		"the planned tests are", "the planned files are", "the plan includes", "plan proposes",
		"from the plan", "in the plan", "already planned",
	)
}

func sourceCitationClaimsPlanResult(answer string, path string) bool {
	lower := strings.ToLower(sourceCitationWindow(answer, path))
	return ContainsAny(lower,
		" passed", " passes", " passing",
		" succeeded", " succeeds", " successful",
		" failed", " failing",
		" proves", " prove ", " proof",
		" verified", " verifies", " confirms", " validates",
	)
}

func sourceCitationExplicitlyProposedPlanPath(answer string, path string) bool {
	lower := strings.ToLower(sourceCitationWindow(answer, path))
	for _, phrase := range []string{
		"proposed path", "proposed paths", "proposed file", "proposed files",
		"planned path", "planned paths", "planned file", "planned files",
		"proposed test", "proposed tests", "planned test", "planned tests", "intended test", "intended tests",
		"planned test plan", "proposed test plan", "intended test plan",
		"planned test suite", "proposed test suite", "intended test suite",
		"planned verification", "proposed verification", "verification command", "verification commands",
		"planned coverage", "proposed coverage", "intended coverage", "planned test coverage",
		"proposed test coverage", "intended test coverage",
		"would create", "will create", "to create", "to be created",
		"files to create", "paths to create",
		"not existing source", "not current source",
	} {
		if strings.Contains(lower, phrase) {
			return true
		}
	}

	return false
}

func sourceCitationClaimsCurrentSourcePath(answer string, path string) bool {
	lower := strings.ToLower(sourceCitationLocalWindow(answer, path, 80, 140))
	if ContainsAny(lower,
		"not existing source", "not existing file",
		"not current source", "not current file",
		"not yet existing source", "not yet existing file",
		"not an existing source", "not an existing file",
		"not a current source", "not a current file",
	) {
		return false
	}

	return ContainsAny(lower,
		"already exists", "existing source", "existing file",
		"current source", "current file", "currently exists",
		"exists on disk", "exists in the workspace",
		"defines ", "contains ", "implements ",
	)
}

func sourceCitationExplicitlyUnverified(answer string, path string) bool {
	lower := strings.ToLower(sourceCitationWindow(answer, path))
	for _, phrase := range []string{
		"unverified", "not yet read", "not source-read", "not source read", "not backed",
		"not enough evidence", "not enough to name", "not enough to identify",
	} {
		if strings.Contains(lower, phrase) {
			return true
		}
	}

	return false
}

func sourceCitationWindow(answer string, path string) string {
	return sourceCitationLocalWindow(answer, path, 180, 180)
}

func sourceCitationLocalWindow(answer string, path string, before int, after int) string {
	if strings.TrimSpace(answer) == "" || strings.TrimSpace(path) == "" {
		return ""
	}

	idx := strings.Index(answer, path)
	if idx < 0 {
		return ""
	}

	start := idx - before
	if start < 0 {
		start = 0
	}

	end := idx + len(path) + after
	if end > len(answer) {
		end = len(answer)
	}

	return answer[start:end]
}

func backtickedCodeLikeTokens(text string) []string {
	matches := backtickedTokenPattern.FindAllStringSubmatch(text, -1)
	out := make([]string, 0, len(matches))

	for _, match := range matches {
		if len(match) > 1 {
			out = append(out, strings.TrimSpace(match[1]))
		}
	}

	return out
}

func sourceGroundingTokenLooksLikeSymbol(token string) bool {
	if token == "" || strings.Contains(token, "/") || strings.Contains(token, " ") {
		return false
	}

	return codeShapedTokenPattern.MatchString(token) ||
		codeShapedSnakeCasePattern.MatchString(token) ||
		qualifiedIdentifierPattern.MatchString(token)
}

func sourceGroundingAnswerCitesReadDiagnosticArtifact(answer string, projection toolResultEvidenceProjection) bool {
	for _, reference := range projection.DiagnosticLogReferences(answer) {
		if reference.LineRange == nil && projection.DiagnosticLogReadCoversPath(reference.Path, 0, 0) {
			return true
		}
		if reference.LineRange != nil && reference.LineRange.Valid() &&
			projection.DiagnosticLogReadCoversPath(reference.Path, reference.LineRange.Start, reference.LineRange.End) {

			return true
		}
	}

	return false
}

func sourceGroundingDiagnosticArtifactCitationSupportsObjective(brief workerruntime.WorkerRuntimeBrief, answer string, projection toolResultEvidenceProjection) bool {
	if !sourceGroundingAnswerCitesReadDiagnosticArtifact(answer, projection) {
		return false
	}

	objective := strings.TrimSpace(brief.Objective)
	if objective == "" {
		return false
	}

	if objectiveRequestsDiagnosticLogInspection(objective) && !objectiveTreatsDiagnosticLogsAsOptionalEvidence(objective) {
		return true
	}

	words := " " + strings.ToLower(strings.Join(strings.Fields(objective), " ")) + " "
	if !ContainsAny(words,
		" harness ", " session ", " turn ", " conversation ", " completion gate ",
		" final answer ", " runner gate ", " gate rejection ", " gate rejected ",
	) {
		return false
	}

	return ContainsAny(words,
		" rca ", " failure ", " failed ", " issue ", " regression ", " rejected ",
		" blocker ", " latest ", " recent ", " most recent ", " previous ",
	)
}
