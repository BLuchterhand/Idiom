package agent

import (
	"strings"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/platform/util"
	"idiom/internal/runtime/agent/agentutil"
	"idiom/internal/runtime/workerruntime"
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

	var directAnswerRelevance readOnlyDirectAnswerRelevance

	if r != nil {
		directAnswerRelevance = r.readOnlyDirectAnswerRelevance(answer, results)
		if directAnswerRelevance.Status != "" {
			acceptance.ObjectiveRelevanceStatus = directAnswerRelevance.Status
			acceptance.RelevantSourceBodyPaths = directAnswerRelevance.RelevantSourceBodyPaths
			acceptance.AnswerObjectiveTerms = directAnswerRelevance.AnswerObjectiveTerms
		}
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

	if r != nil && r.planThenExecuteAnchorRefreshFinalAnswerPending(results) {
		return reject("plan_execute_anchor_refresh_pending", "plan_execute_anchor_refresh_pending")
	}

	if looksLikeUnexecutedToolRequestAnswer(answer) {
		return reject("unexecuted_tool_request_final_answer", "unexecuted_tool_request_final_answer")
	}

	if r != nil && runnerBriefRequiresMutation(r.RuntimeBrief) {
		if missing := missingRequiredEditPaths(r.RuntimeBrief.RequiredEditPaths, changedPathsFromToolResults(results)); len(missing) > 0 {
			return reject("required_edit_paths_remaining", "required_edit_paths_remaining")
		}

		if missing := unresolvedRejectedMutationPaths(results, r.RuntimeBrief.RequiredEditPaths); len(missing) > 0 {
			acceptance.EvidencePaths = util.Merge(acceptance.EvidencePaths, missing)
			return reject("rejected_mutation_paths_remaining", "rejected_mutation_paths_remaining")
		}
	}

	if strings.HasPrefix(strings.TrimSpace(answer), "<<QUESTION>>") {
		if r != nil && readOnlyDirectAnswerLane(r.RuntimeBrief) && !runnerBriefRequiresMutation(r.RuntimeBrief) {
			return reject("read_only_protocol_blocker", "read_only_protocol_blocker")
		}

		return reject("user_visible_blocker", "user_visible_blocker")
	}

	if r != nil && !readOnlyDirectAnswerLane(r.RuntimeBrief) &&
		readOnlyAnswerReportsInsufficientEvidence(answer) &&
		readOnlyClosureDecisionForResults(r.RuntimeBrief, results).Answerable() {

		return reject("non_answer_prose", "answerable_evidence_reported_as_insufficient")
	}

	if r != nil &&
		workerruntime.PermissionProfileIsReadOnlyWorkspace(r.RuntimeBrief.PermissionProfile) &&
		workerruntime.RuntimeContractIsArchitectureOrReview(r.RuntimeBrief.Contract) &&
		unsupportedReadOnlyCloseoutAnswer(answer) {

		return reject("non_answer_prose", "insufficient_evidence_answer")
	}

	if looksLikeNonAnswerProse(answer) && !finalAnswerAcceptanceAllowsSourceInventory(acceptance, answer, r, results) {
		return reject("non_answer_prose", "non_answer_prose")
	}

	if r != nil {
		// Compute objective relevance to detect compound rejection
		// reasons. The source-grounding check runs first, but if the
		// evidence also fails objective relevance, surface both problems
		// so the model doesn't fix one issue only to discover another.
		hasObjectiveBlocker := directAnswerRelevance.BlockerKind != "" && directAnswerRelevance.Reason != ""

		if reason := r.sourceGroundingRejectionReason(answer, results); reason != "" {
			if len(acceptance.EvidencePaths) == 0 {
				acceptance.EvidencePaths = closurePromptEvidencePaths(directAnswerRelevance.RelevantSourceBodyPaths, 8)
			}

			if hasObjectiveBlocker {
				reason = reason + " Additionally, " + directAnswerRelevance.Reason
			}

			return reject("source_grounding_unmet", reason)
		}

		if hasObjectiveBlocker {
			return reject(directAnswerRelevance.BlockerKind, directAnswerRelevance.Reason)
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

func finalAnswerAcceptanceAllowsSourceInventory(acceptance finalAnswerAcceptance, answer string, r *Runner, results []workspace.ToolResult) bool {
	if r == nil {
		return false
	}

	return readOnlyClosureDecisionForResults(r.RuntimeBrief, results).
		AllowsSourceInventoryAnswer(answer, acceptance.Decision == finalAnswerAcceptanceAccepted)
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

	return readOnlyClosureDecisionForResults(readOnlyDirectAnswerAcceptanceLedgerBrief(r.RuntimeBrief), results).DirectAnswerRelevance(answer)
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


func acceptedReadOnlyDirectAnswerEvidencePaths(answer string, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) []string {
	if !readOnlyDirectAnswerAcceptanceApplies(brief) {
		return nil
	}

	decision := readOnlyClosureDecisionForResults(readOnlyDirectAnswerAcceptanceLedgerBrief(brief), results).
		FinalAnswerDecision(answer)

	return append([]string(nil), decision.AcceptedEvidencePaths...)
}

func readOnlyAcceptedDirectAnswerEvidencePaths(answer string, ledger readOnlyAnswerabilityLedger, results []workspace.ToolResult) []string {
	mentioned := readOnlySourceLikePaths(sourceGroundingMentionedPaths(answer), 16)
	relevant := ledger.RelevantSourceBodyPaths
	objectivePaths := ledger.SourceBodyObjectivePaths

	if ledger.directAnswerConversation() && agentutil.ObjectiveAsksImplementationShape(ledger.Objective) {
		relevant = ledger.ImplementationOwnerPaths()
		objectivePaths = relevant
	}

	paths := intersectRunnerPaths(mentioned, relevant)

	paths = util.Merge(paths, objectivePaths, relevant)
	paths = util.Merge(paths, answerCitedCollectiveSourceBodySupportPaths(answer, ledger))

	if len(paths) == 0 && ledger.DiagnosticRelevant {
		paths = util.Merge(paths, mentionedEvidencePathsFromToolResults(results, 8))
	}

	return closurePromptEvidencePaths(paths, 8)
}

func answerCitedCollectiveSourceBodySupportPaths(answer string, ledger readOnlyAnswerabilityLedger) []string {
	mentionedSet := readOnlyPathSet(sourceGroundingMentionedPaths(answer))
	if len(mentionedSet) == 0 {
		return nil
	}

	if ledger.directAnswerConversation() && agentutil.ObjectiveAsksImplementationShape(ledger.Objective) {
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
		path := agentutil.NormalizeRunnerPath(support.Path)
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
			term = agentutil.ReadOnlyObjectiveSupportTerm(term)
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
		path := agentutil.NormalizeRunnerPath(raw)
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
		fields["relevant_source_body_paths"] = util.LimitSlice(acceptance.RelevantSourceBodyPaths, 8)
	}

	return fields
}

func (r *Runner) planThenExecuteAnchorRefreshFinalAnswerPending(results []workspace.ToolResult) bool {
	if r == nil || !r.planThenExecutePlannerPhase() {
		return false
	}

	state := DerivePlanExecutePlanningState(r.RuntimeBrief, results, PlanExecutePlanningTransport{TextualToolParser: true})
	return state.Mode == PlanExecutePlanningModeAnchorRefresh && len(state.RefreshPaths) > 0
}

func planThenExecuteAnchorRefreshFinalAnswerRejection(acceptance finalAnswerAcceptance) bool {
	return strings.TrimSpace(acceptance.BlockerKind) == "plan_execute_anchor_refresh_pending"
}


