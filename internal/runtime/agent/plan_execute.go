package agent

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/autonomy"
	"idiom/internal/contracts/pathparse"
	plancontract "idiom/internal/contracts/plan"
	planartifact "idiom/internal/contracts/planartifact"
	"idiom/internal/contracts/toolfacts"
	"idiom/internal/platform/textutil"
	semanticlanguage "idiom/internal/repository/semantic/language"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/workerruntime"
)

func (r *runExecution) scratchpadDeliverableFailedEvidenceNudge(req workspace.ToolRequest, result workspace.ToolResult, results []workspace.ToolResult) (string, bool) {
	if r == nil || !briefIsScratchpadDeliverableLane(r.state.runtimeBrief) || strings.TrimSpace(result.Error) == "" {
		return "", false
	}

	if workspace.IsWorkspaceWriteTool(req.Tool) || !r.scopedReadClassTool(req) || len(r.remainingRequiredEditPaths(results)) == 0 {
		return "", false
	}

	missing := r.remainingRequiredEditPaths(results)
	target := "the required deliverable"

	if len(missing) > 0 {
		target = strings.Join(missing, ", ")
	} else if paths := nonEmptyRunnerStrings(r.state.runtimeBrief.RequiredEditPaths, 3); len(paths) > 0 {
		target = strings.Join(paths, ", ")
	}

	reason := strings.TrimSpace(result.Error)
	if reason == "" {
		reason = strings.TrimSpace(result.Summary)
	}

	if reason == "" {
		reason = "the optional evidence request failed"
	}

	return fmt.Sprintf("The optional evidence request for %s failed: %s. This failure does not satisfy the source-evidence contract. Permission-authorized inspection remains callable for a fresh model-selected request; the deliverable may also record the typed evidence gap when that is accurate. Final acceptance still requires the deliverable write or <<QUESTION>> with the exact blocker.", target, reason), true
}

func scratchpadDeliverableFailedEvidenceResult(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, missingRequiredPaths []string) (workspace.ToolResult, bool) {
	if !briefIsScratchpadDeliverableLane(brief) {
		return workspace.ToolResult{}, false
	}

	if len(missingRequiredPaths) == 0 {
		return workspace.ToolResult{}, false
	}

	for i := len(results) - 1; i >= 0; i-- {
		result := results[i]
		if strings.TrimSpace(result.Error) == "" || workspace.IsWorkspaceWriteTool(result.Tool) {
			continue
		}

		if strings.TrimSpace(result.Summary) == "Scratchpad evidence follow-up rejected" {
			continue
		}

		if workspace.IsReadTool(strings.TrimSpace(result.Tool)) {
			return result, true
		}
	}

	return workspace.ToolResult{}, false
}

func scratchpadDeliverableFailedEvidenceStatus(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, missingRequiredPaths []string) string {
	failed, ok := scratchpadDeliverableFailedEvidenceResult(brief, results, missingRequiredPaths)
	if !ok {
		return ""
	}

	target := "the required deliverable"
	if missing := uniqueSortedRunnerPaths(missingRequiredPaths); len(missing) > 0 {
		target = strings.Join(missing, ", ")
	} else if paths := nonEmptyRunnerStrings(brief.RequiredEditPaths, 3); len(paths) > 0 {
		target = strings.Join(paths, ", ")
	}

	reason := strings.TrimSpace(failed.Error)
	if reason == "" {
		reason = strings.TrimSpace(failed.Summary)
	}

	if reason == "" {
		reason = "the optional evidence request failed"
	}

	tool := strings.TrimSpace(failed.Tool)
	if tool == "" {
		tool = "read/discovery tool"
	}

	return fmt.Sprintf("closed_negative_evidence: optional %s evidence for %s failed (%s); record this as an evidence gap in the non-source deliverable if the exact source fact remains unavailable", tool, target, textutil.Truncate(strings.TrimSpace(reason), 220))
}

func (r *runExecution) scopedReadClassTool(req workspace.ToolRequest) bool {
	if workspace.IsReadTool(strings.TrimSpace(req.Tool)) {
		return true
	}

	return r != nil && r.externalReadOnlyDiscoveryTool(req.Tool)
}

func (r *runExecution) externalReadOnlyDiscoveryTool(tool string) bool {
	if r == nil || r.runner.Workspace == nil {
		return false
	}

	return r.runner.Workspace.ToolSurfaceSnapshot().ExternalToolIsReadOnlyDiscovery(tool)
}

func (r *runExecution) remainingRequiredEditPaths(results []workspace.ToolResult) []string {
	if r == nil {
		return nil
	}
	if r.state.mutation.bound {
		return r.state.missingRequiredMutationCoverageIDs()
	}

	return append([]string(nil), r.requiredEditCoveragePaths()...)
}

func (r *runExecution) requiredEditCoveragePaths() []string {
	if r == nil {
		return nil
	}
	if r.state.mutation.bound {
		return append([]string(nil), r.state.mutation.canonicalScopeProjection.RequiredCoverageIDs...)
	}

	required := r.state.runtimeBrief.RequiredEditPaths
	if !r.planThenExecutePlanSubmissionPhase() {
		return required
	}

	root := ""
	if r.runner.Workspace != nil {
		root = strings.TrimSpace(r.runner.Workspace.Root())
	}

	return planThenExecuteRequiredCoveragePaths(required, root)
}

func contextPackUnavailableForRecovery(results []workspace.ToolResult) bool {
	for _, result := range results {
		if strings.TrimSpace(result.Tool) != workspace.ToolNameContextPack {
			continue
		}

		if strings.TrimSpace(result.Error) != "" {
			return true
		}

		if result.Facts == nil || !result.Facts.HasDiscoveryTargets {
			return true
		}
	}

	return false
}

func contextPackSeen(results []workspace.ToolResult) bool {
	for _, result := range results {
		if strings.TrimSpace(result.Tool) == workspace.ToolNameContextPack {
			return true
		}
	}

	return false
}

func concreteRecoveryEvidencePaths(results []workspace.ToolResult, max int) []string {
	filtered := make([]workspace.ToolResult, 0, len(results))
	for _, result := range results {
		switch strings.TrimSpace(result.Tool) {
		case workspace.ToolNameContextPack, completionGateToolName:
			continue
		default:
			filtered = append(filtered, result)
		}
	}

	return newToolResultEvidenceProjection(filtered).promptEvidencePaths(max)
}

func recoveryEvidencePathsForObjective(objective string, results []workspace.ToolResult, max int) []string {
	if max <= 0 {
		max = 4
	}

	searchRanked := rankedSearchContentEvidencePaths(objective, results, max)
	if len(searchRanked) >= max {
		return searchRanked[:max]
	}

	seen := make(map[string]struct{}, len(searchRanked))
	paths := make([]string, 0, max)

	for _, path := range searchRanked {
		seen[path] = struct{}{}
		paths = append(paths, path)
	}

	for _, path := range concreteRecoveryEvidencePaths(results, max*2) {
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

func candidateRecoveryScopePaths(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, max int) []string {
	if max <= 0 {
		max = 4
	}

	seen := make(map[string]struct{}, max)
	paths := make([]string, 0, max)
	add := func(path string) {
		path = NormalizeRunnerPath(path)
		if path == "" {
			return
		}

		if !planThenExecuteRecoveryScopePathAllowed(path) {
			return
		}

		if _, ok := seen[path]; ok {
			return
		}

		seen[path] = struct{}{}
		paths = append(paths, path)
	}

	for _, path := range brief.AllowedEditPaths {
		add(path)

		if len(paths) >= max {
			return paths
		}
	}

	for _, path := range recoveryEvidencePathsForObjective(brief.Objective, results, max) {
		add(path)

		if len(paths) >= max {
			return paths
		}
	}

	for _, path := range typedDiscoveryCandidatePathsFromResults(results, max) {
		add(path)

		if len(paths) >= max {
			return paths
		}
	}

	return paths
}

func unreadCandidateRecoveryScopePaths(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, max int) []string {
	candidates := candidateRecoveryScopePaths(brief, results, max)
	if len(candidates) == 0 {
		return nil
	}

	read := newToolResultEvidenceProjection(results).ReadPaths()
	for _, path := range candidates {
		if _, ok := read[NormalizeRunnerPath(path)]; ok {
			return nil
		}
	}

	unread := make([]string, 0, len(candidates))

	unread = append(unread, candidates...)

	return unread
}

func unreadCandidateRecoveryScopePathsForRecovery(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, max int, authority planThenExecutePathAuthority) []string {
	candidates := candidateRecoveryScopePaths(brief, results, max)
	if len(candidates) == 0 {
		return nil
	}

	read := newToolResultEvidenceProjection(results).ReadPaths()
	unread := make([]string, 0, len(candidates))

	for _, path := range candidates {
		normalized := NormalizeRunnerPath(path)
		if normalized == "" {
			continue
		}

		if authority.authorizedNewFile(normalized) {
			continue
		}

		if _, ok := read[normalized]; ok {
			continue
		}

		unread = append(unread, normalized)
	}

	return unread
}

func rankedSearchContentEvidencePaths(objective string, results []workspace.ToolResult, max int) []string {
	if max <= 0 {
		max = 4
	}

	terms := recoveryObjectiveSignalTerms(objective)
	lowerObjective := strings.ToLower(objective)
	uiObjective := objectiveMentionsUI(lowerObjective)

	type scoredPath struct {
		path  string
		score int
		order int
	}

	scores := map[string]*scoredPath{}
	order := 0

	for _, result := range results {
		if strings.TrimSpace(result.Tool) != workspace.ToolNameSearchContent || strings.TrimSpace(result.Error) != "" {
			continue
		}

		for _, line := range strings.Split(result.Output, "\n") {
			path, text, ok := searchContentPathAndText(line)
			if !ok {
				continue
			}

			entry, exists := scores[path]
			if !exists {
				entry = &scoredPath{path: path, order: order}
				scores[path] = entry
				order++
			}

			entry.score += recoverySearchLineScore(path, text, terms, uiObjective)
		}
	}

	if len(scores) == 0 {
		return nil
	}

	paths := make([]scoredPath, 0, len(scores))
	for _, entry := range scores {
		paths = append(paths, *entry)
	}

	sort.SliceStable(paths, func(i, j int) bool {
		if paths[i].score != paths[j].score {
			return paths[i].score > paths[j].score
		}

		return paths[i].order < paths[j].order
	})

	out := make([]string, 0, min(max, len(paths)))
	for _, entry := range paths {
		if entry.score <= -50 {
			continue
		}

		out = append(out, entry.path)
		if len(out) >= max {
			break
		}
	}

	return out
}

func searchContentPathAndText(line string) (path string, text string, ok bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return "", "", false
	}

	firstColon := strings.Index(line, ":")
	if firstColon < 0 {
		return "", "", false
	}

	before := strings.TrimSpace(line[:firstColon])
	fields := strings.Fields(before)

	if len(fields) == 0 {
		return "", "", false
	}

	path = fields[len(fields)-1]
	if !recoverySearchPathLooksEditableSource(path) || !strings.Contains(path, "/") {
		return "", "", false
	}

	rest := line[firstColon+1:]
	secondColon := strings.Index(rest, ":")

	if secondColon >= 0 {
		text = strings.TrimSpace(rest[secondColon+1:])
	}

	return filepath.ToSlash(path), text, true
}

func recoverySearchLineScore(path string, text string, terms []string, uiObjective bool) int {
	lowerPath := strings.ToLower(filepath.ToSlash(path))
	lowerText := strings.ToLower(text)
	score := 1

	for _, term := range terms {
		if strings.Contains(lowerPath, term) {
			score += 10
		}

		if strings.Contains(lowerText, term) {
			score += 12
		}
	}

	if strings.HasSuffix(lowerPath, "_test.go") {
		score -= 100
	}

	if strings.HasPrefix(strings.TrimSpace(lowerText), "//") {
		score -= 8
	}

	if strings.Contains(lowerPath, "tool_schemas.go") || strings.Contains(lowerPath, "/agent/") || strings.Contains(lowerPath, "/eval/") {
		score -= 30
	}

	if strings.HasPrefix(lowerPath, "cmd/") && !ContainsAny(lowerText, "implementation", "core", "internal") {
		score -= 20
	}

	if slices.Contains(terms, "apply") && ContainsAny(lowerPath, "migration_apply", "_apply.", "/apply") {
		score += 60
	}

	if uiObjective {
		if strings.HasPrefix(lowerPath, "internal/surfaces/tui/app/") {
			score += 45
		}

		if ContainsAny(lowerPath, "render", "view", "model_state", "update_handlers") {
			score += 25
		}

		if ContainsAny(lowerPath, "frontier/", "comprehension/", "workspace/") {
			score -= 25
		}
	}

	return score
}

func recoveryObjectiveSignalTerms(objective string) []string {
	pattern := literalRecoverySearchPattern(objective)
	if pattern == "" {
		return nil
	}

	parts := strings.Split(pattern, "|")
	terms := make([]string, 0, len(parts))

	for _, part := range parts {
		part = strings.ToLower(strings.TrimSpace(part))
		if part == "" {
			continue
		}

		terms = append(terms, strings.ReplaceAll(part, `\`, ""))
	}

	return terms
}

func objectiveMentionsUI(lowerObjective string) bool {
	return ContainsAny(lowerObjective, "tui", "ui", "banner", "header", "footer", "top-right", "top right", "window", "screen", "view", "render")
}

func (r *runExecution) mutationRejectedRetryWriteRecovery(decision runnerRequestBudgetDecision, req workspace.ToolRequest, results []workspace.ToolResult) (workspace.ToolResult, string, bool) {
	if r == nil || decision.ForceAnswerReason != "rejection_retry_blocked" {
		return workspace.ToolResult{}, "", false
	}

	if !runnerBriefRequiresMutation(r.state.runtimeBrief) || r.planThenExecutePlannerPhase() {
		return workspace.ToolResult{}, "", false
	}

	if workspace.IsWorkspaceWriteTool(req.Tool) || len(r.completionChangedPaths(results)) > 0 {
		return workspace.ToolResult{}, "", false
	}

	tool := strings.TrimSpace(req.Tool)
	if tool == "" {
		tool = "the requested tool"
	}

	result := workspace.ToolResult{
		Tool:    req.Tool,
		Summary: "Mutation write required after repeated rejected request",
		Error:   fmt.Sprintf("%s was already rejected and then repeated before any successful workspace write. The implementation contract is not satisfied by repeating rejected non-write requests.", tool),
	}
	prompt := result.Error + " Use the gathered source evidence to make the smallest parser-readable write now. If no write is valid, answer exactly `<<QUESTION>>:` with the concrete path-specific blocker or no-op evidence. Do not repeat the rejected non-write request."

	return result, prompt, true
}

type planThenExecuteLoopBudgetRecovery struct {
	Prompt string
}

func (r *runExecution) planThenExecuteLoopBudgetRecoveryPrompt(req workspace.ToolRequest, results []workspace.ToolResult, reason string, nativeSubmitPlan bool) (planThenExecuteLoopBudgetRecovery, bool) {
	if r == nil || !r.planThenExecutePlannerPhase() {
		return planThenExecuteLoopBudgetRecovery{}, false
	}

	var unreadCandidatePaths []string

	if len(r.planThenExecutePathAuthority().requiredCoveragePaths(r.state.runtimeBrief)) == 0 {
		unreadCandidatePaths = unreadCandidateRecoveryScopePathsForRecovery(r.state.runtimeBrief, results, 8, r.planThenExecutePathAuthority())
	}

	noBodyMissRecovery := planThenExecuteNoBodyMissRecoveryApplies(results)
	if !planThenExecuteTerminalSubmitPlanAllowed(r.state.runtimeBrief, results, r.planThenExecutePathAuthority()) &&
		(len(unreadCandidatePaths) == 0 || !noBodyMissRecovery) {

		return planThenExecuteLoopBudgetRecovery{}, false
	}

	tool := strings.TrimSpace(req.Tool)
	if tool == "" {
		tool = "the requested tool"
	}

	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = fmt.Sprintf("%s repeated without advancing the planner contract", tool)
	}

	if planThenExecuteSubmitCorrectionActive(r.state.runtimeBrief, results, r.planThenExecutePathAuthority()) {
		lead := fmt.Sprintf("The repeated %s request hit the loop budget: %s. Do not repeat that exact request. Submit-correction mode remains open, and permission-authorized inspection requests remain callable when they can establish a concrete missing fact.", tool, reason)
		outcome := "When the evidence is sufficient, submit one parser-readable `submit_plan`. " + planThenExecuteSubmitPlanCoverageSentence() + " Do not answer `Decision: source-satisfied`, do not answer `<<QUESTION>>:`, and do not report the loop-budget recovery itself as the blocker."
		if planThenExecuteSubmitCorrectionBlockerAllowed(r.state.runtimeBrief, results) {
			outcome = "When the evidence is sufficient, submit a parser-readable `submit_plan`; if no safe executable edit body can be produced after repeated rejected submit_plan attempts, answer `<<QUESTION>>:` with the exact path-specific missing body/anchor blocker. " + planThenExecuteSubmitPlanCoverageSentence() + " Do not answer `Decision: source-satisfied`, and do not report the loop-budget recovery itself as the blocker."
		}
		parts := []string{
			lead,
			outcome,
			planThenExecuteTerminalSubmitPlanSkeletonNoMoreTools(r.state.runtimeBrief, results, nativeSubmitPlan, r.planThenExecutePathAuthority()),
			"Fill the skeleton with real source text from the reads already in this turn. Do not copy placeholders, do not request another tool, and do not describe the plan in prose outside the parser-readable action.",
		}

		return planThenExecuteLoopBudgetRecovery{
			Prompt: strings.Join(nonEmptyRunnerPromptParts(parts), "\n\n"),
		}, true
	}

	if len(r.planThenExecutePathAuthority().requiredCoveragePaths(r.state.runtimeBrief)) == 0 && noBodyMissRecovery {
		if unread := unreadCandidatePaths; len(unread) > 0 {
			read := sortedRunnerPathKeys(newToolResultEvidenceProjection(results).ReadPaths())
			lead := fmt.Sprintf("The repeated %s request hit the loop budget: %s. Do not repeat that request.", tool, reason)
			parts := []string{
				lead,
				"Unscoped plan-execute candidate paths are not coverage obligations, but this planner still has unread candidate surfaces that may close a concrete edit-safety fact.",
				"Read candidate path(s) so far: " + textutil.FirstNonEmpty(strings.Join(read, ", "), "(none)"),
				"Unread candidate path(s): " + strings.Join(unread, ", "),
				"The next visible action must be exactly one parser-readable action: one bounded source-body read/search for a model-selected unread candidate path with a named edit-safety fact, a parser-readable `submit_plan` using the source evidence already gathered, `Decision: source-satisfied` with current-state evidence, or `<<QUESTION>>:` naming the exact blocker. If the prior symbol lookup missed before source-body evidence existed, a bounded read on that same candidate path is valid.",
				"Do not call the same tool with the same arguments again, do not run verification, and do not report the loop-budget recovery itself as the blocker.",
			}

			return planThenExecuteLoopBudgetRecovery{
				Prompt: strings.Join(nonEmptyRunnerPromptParts(parts), "\n\n"),
			}, true
		}
	}

	lead := fmt.Sprintf("The repeated %s request hit the loop budget: %s. Do not repeat that exact request. The planner already has source evidence, and permission-authorized inspection requests remain callable when they can establish a concrete missing fact.", tool, reason)
	parts := []string{
		lead,
		"When the evidence is sufficient, choose one terminal outcome: a parser-readable `submit_plan`, `Decision: source-satisfied` citing the current source path(s), or `<<QUESTION>>:` naming the exact blocker. Do not report the loop-budget recovery itself as the blocker.",
		planThenExecuteTerminalSubmitPlanSkeletonNoMoreTools(r.state.runtimeBrief, results, nativeSubmitPlan, r.planThenExecutePathAuthority()),
		"If submitting a plan, fill the skeleton with real source text from the reads already in this turn. Do not copy placeholders, do not request another tool, and do not describe the plan in prose outside the parser-readable action.",
	}

	return planThenExecuteLoopBudgetRecovery{
		Prompt: strings.Join(nonEmptyRunnerPromptParts(parts), "\n\n"),
	}, true
}

func planThenExecuteNoBodyMissRecoveryApplies(results []workspace.ToolResult) bool {
	return len(newToolResultEvidenceProjection(results).ReadPaths()) == 0 && planThenExecuteHasPriorReadClassFailure(results)
}

func (r *runExecution) planThenExecuteIgnoredLoopBudgetRecoveryPrompt(req workspace.ToolRequest, results []workspace.ToolResult, reason string, nativeSubmitPlan bool) (planThenExecuteLoopBudgetRecovery, bool) {
	if r == nil || !r.planThenExecutePlannerPhase() {
		return planThenExecuteLoopBudgetRecovery{}, false
	}

	if !planThenExecuteTerminalSubmitPlanAllowed(r.state.runtimeBrief, results, r.planThenExecutePathAuthority()) {

		return planThenExecuteLoopBudgetRecovery{}, false
	}

	tool := strings.TrimSpace(req.Tool)
	if tool == "" {
		tool = "the requested tool"
	}

	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = fmt.Sprintf("%s repeated after a loop-budget recovery", tool)
	}

	if planThenExecuteSubmitCorrectionActive(r.state.runtimeBrief, results, r.planThenExecutePathAuthority()) {
		lead := fmt.Sprintf("The planner repeated %s after a loop-budget recovery: %s. Submit-correction mode is already ready; repeating the same request is not progress.", tool, reason)
		outcome := "Do not repeat the exact request. Permission-authorized inspection remains callable for a fresh request; when the evidence is sufficient, submit one parser-readable `submit_plan`. " + planThenExecuteSubmitPlanCoverageSentence() + " Do not answer `Decision: source-satisfied` and do not answer `<<QUESTION>>:`."
		if planThenExecuteSubmitCorrectionBlockerAllowed(r.state.runtimeBrief, results) {
			outcome = "Do not repeat the exact request. Permission-authorized inspection remains callable for a fresh request; when the evidence is sufficient, submit a parser-readable `submit_plan`, or answer `<<QUESTION>>:` with the exact path-specific missing body/anchor blocker if no safe executable edit body can be produced after repeated rejected submit_plan attempts. " + planThenExecuteSubmitPlanCoverageSentence() + " Do not answer `Decision: source-satisfied`."
		}
		parts := []string{
			lead,
			outcome,
			planThenExecuteTerminalSubmitPlanSkeletonNoMoreTools(r.state.runtimeBrief, results, nativeSubmitPlan, r.planThenExecutePathAuthority()),
			"Fill the skeleton with real source text from the evidence already gathered. Do not request another tool, do not copy placeholders, and do not describe the plan in prose outside the parser-readable action.",
		}

		return planThenExecuteLoopBudgetRecovery{
			Prompt: strings.Join(nonEmptyRunnerPromptParts(parts), "\n\n"),
		}, true
	}

	lead := fmt.Sprintf("The planner repeated %s after a loop-budget recovery: %s. The previous recovery already allowed either a different bounded read/search with a named edit-safety fact or a terminal planner outcome; repeating the same request is not progress.", tool, reason)
	parts := []string{
		lead,
		"Do not repeat the exact request. Permission-authorized inspection remains callable for a fresh request; when the evidence is sufficient, choose one terminal planner outcome: a parser-readable `submit_plan`, `Decision: source-satisfied` citing current-state evidence, or `<<QUESTION>>:` naming the exact blocker.",
		planThenExecuteTerminalSubmitPlanSkeletonNoMoreTools(r.state.runtimeBrief, results, nativeSubmitPlan, r.planThenExecutePathAuthority()),
		"If submitting a plan, fill the skeleton with real source text from the evidence already gathered. Do not request another tool, do not copy placeholders, and do not describe the plan in prose outside the parser-readable action.",
	}

	return planThenExecuteLoopBudgetRecovery{
		Prompt: strings.Join(nonEmptyRunnerPromptParts(parts), "\n\n"),
	}, true
}

func planThenExecuteHasPriorReadClassFailure(results []workspace.ToolResult) bool {
	for _, result := range results {
		if strings.TrimSpace(result.Tool) == "" {
			continue
		}

		if workerruntime.IsZeroHitSearchResult(result) {
			return true
		}

		if strings.TrimSpace(result.Error) == "" {
			continue
		}

		lower := strings.ToLower(strings.Join([]string{result.Summary, result.Error, result.Output}, "\n"))
		if strings.Contains(lower, "zero-hit retry blocked") ||
			strings.Contains(lower, "no symbols matched") ||
			strings.Contains(lower, "no go symbols matched") ||
			strings.Contains(lower, "repeating it will not find new matches") {

			return true
		}
	}

	return false
}

func fullSourceReadFileSeen(path string, results []workspace.ToolResult) bool {
	path = NormalizeRunnerPath(path)
	if path == "" {
		return false
	}

	for _, result := range results {
		if strings.TrimSpace(result.Tool) != workspace.ToolNameReadFile || strings.TrimSpace(result.Error) != "" {
			continue
		}

		facts := newToolResultEvidenceRecord(result).SourceWindowFacts(path)
		if len(facts) == 0 {
			continue
		}

		for _, fact := range facts {
			if sourceWindowCoversWholeFile(fact) {
				return true
			}
		}
	}

	return false
}

func implementationRequiredPathHiddenOnlyRecoveryStall(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, reply llama.ChatMessage, priorGateRejections int, supportsNativeTools bool, completion implementationCompletionState) bool {
	if supportsNativeTools || priorGateRejections == 0 {
		return false
	}

	if !runnerBriefRequiresMutation(brief) || len(brief.RequiredEditPaths) == 0 {
		return false
	}

	if len(completion.MissingRequiredPaths) == 0 {
		return false
	}

	if !implementationRequiredPathAnchorsReady(brief, results, completion.ChangedPaths) {
		return false
	}

	return strings.TrimSpace(reply.Content) == "" && strings.TrimSpace(reply.ReasoningContent) != ""
}

func mutationHiddenOnlyNoWriteTerminalRecoveryStall(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, reply llama.ChatMessage, priorGateRejections int, supportsNativeTools bool, completion implementationCompletionState) bool {
	if supportsNativeTools || priorGateRejections == 0 {
		return false
	}

	if strings.TrimSpace(brief.TaskFamily) == PlanThenExecutePlannerTaskFamily {
		return false
	}

	if !runnerBriefRequiresMutation(brief) {
		return false
	}

	if len(completion.ChangedPaths) > 0 {
		return false
	}

	if implementationTargetWriteReadySurfaceActive(brief, results, len(completion.ChangedPaths) > 0) {
		return false
	}

	if strings.TrimSpace(reply.Content) != "" || strings.TrimSpace(reply.ReasoningContent) == "" {
		return false
	}

	return len(newToolResultEvidenceProjection(results).SourceWindowPaths()) > 0
}

func (r *runExecution) unscopedNoWriteTerminalRecoveryReady(results []workspace.ToolResult) bool {
	if r == nil || !r.unscopedNoWriteCompletionRecoveryApplies(results) {
		return false
	}

	if len(r.completionChangedPaths(results)) > 0 || len(r.remainingRequiredEditPaths(results)) > 0 {
		return false
	}

	return len(newToolResultEvidenceProjection(results).SourceWindowPaths()) > 0 && mutationNoWriteCompletionGateRejectionCount(results) >= 2
}

func mutationNoWriteCompletionGateRejectionCount(results []workspace.ToolResult) int {
	count := 0

	for _, result := range results {
		envelope, ok := completionGateRecoveryEnvelopeFromResult(result)
		if ok && envelope.InvariantID == completionGateInvariantMutationWrite && envelope.ViolationID == "workspace_write_missing" {
			count++
		}
	}

	return count
}

func (r *runExecution) unscopedNoWriteTerminalRecoveryPrompt(results []workspace.ToolResult) string {
	lines := []string{
		"The completion gate has now rejected multiple no-write final answers after source evidence was gathered.",
		"Keep using the active implementation strategy surface: parser-readable write when enough evidence exists, bounded read/search with `named_gap` for one exact missing edit-safety fact, or `<<QUESTION>>:` naming the exact blocker/no-op evidence.",
		"Do not repeat a prose final answer until a workspace write, no-op, or exact blocker has been recorded.",
		textualMutationWriteJSONInstruction(),
	}
	if sourced := implementationTargetWriteReadySourcedPathSummary(results, 4); sourced != "" {
		lines = append(lines, "Already-read source-body evidence is available for: "+sourced+".")
	}

	lines = append(lines, "The model owns whether more evidence is needed before writing; the harness only requires the next visible action to satisfy the active parser-readable contract. Actions written only in hidden reasoning are ignored by the parser. Do not copy placeholders, schema examples, or prose as edit fields.")

	return strings.Join(lines, "\n\n")
}

func (r *runExecution) implementationTargetWriteReadyTerminalRecoveryPrompt(results []workspace.ToolResult) string {
	lines := []string{
		"The final answer was rejected because the implementation lane is already target-write-ready and no workspace write has been recorded.",
		"Keep using the active target-write-ready strategy surface: parser-readable write when enough evidence exists, bounded read/search with `named_gap` for one exact missing edit-safety fact, or `<<QUESTION>>:` naming the exact blocker/no-op evidence.",
		"Do not repeat a prose final answer until a workspace write, no-op, or exact blocker has been recorded.",
		textualMutationWriteJSONInstruction(),
	}
	if sourced := implementationTargetWriteReadySourcedPathSummary(results, 4); sourced != "" {
		lines = append(lines, "Already-read source-body evidence is available for: "+sourced+".")
	}

	lines = append(lines, "The model owns whether more evidence is needed before writing; the harness only requires the next visible action to satisfy the active parser-readable contract. Actions written only in hidden reasoning are ignored by the parser. Do not copy placeholders, schema examples, or prose as edit fields.")

	return strings.Join(lines, "\n\n")
}

func hiddenOnlyNoWriteTerminalRecoveryPrompt() string {
	return strings.Join([]string{
		"The previous retry produced hidden-only output after concrete source evidence was already read. Hidden thinking is disabled for this retry.",
		"Permission-authorized read/search/discovery tools remain callable for a fresh model-selected request.",
		"Produce one visible parser-readable action selected from the active contract: lawful inspection when another fact is needed, a write using sufficient current evidence, or `<<QUESTION>>:` naming the exact blocker that prevents the required write.",
		textualMutationWriteJSONInstruction(),
		"Choose the path and edit shape from the gathered evidence and active runtime contract. Do not copy placeholders, schema examples, or prose as edit fields.",
	}, "\n\n")
}

func reviewRepairCompletionGateRecoveryPrompt(brief workerruntime.WorkerRuntimeBrief) string {
	lines := []string{
		"The completion gate rejected the review-repair response because it did not visibly close the review contract.",
	}
	if failed := planThenExecuteReviewRepairFailedPhaseObligations(brief); len(failed) > 0 {
		lines = append(lines,
			"Phase Evidence still has failed obligations: "+strings.Join(failed, ", ")+". `Decision: pass` is invalid in this state.",
			"The eventual terminal review-repair outcome must be exactly one of: a corrective `submit_plan` whose rationale starts with `Verification Checklist:`, or `<<QUESTION>>:` naming the exact failed obligation blocker.",
			"Corrective plans must be one parser-readable `submit_plan` object with a real `Verification Checklist:` rationale and executable edits for allowed repair paths only. Blockers must start with `<<QUESTION>>:` and name the exact failed obligation.",
		)
	} else {
		lines = append(lines,
			"The eventual terminal review-repair outcome must be exactly one of: `Decision: pass` with a `Verification Checklist`, a corrective `submit_plan` whose rationale starts with `Verification Checklist:`, or `<<QUESTION>>:` naming the exact blocker.",
			"Pass responses start with `Decision: pass` followed by `Verification Checklist:` bullets. Corrective plans must be one parser-readable `submit_plan` object with a real checklist rationale and executable edits for allowed repair paths only. Blockers must start with `<<QUESTION>>:` and name the exact blocker.",
		)
	}

	lines = append(lines,
		"Do not add prose before or after the chosen terminal shape.",
		"Use the existing Phase Evidence Bundle, Diff Evidence, submitted_plan_state, source_sanity, verification entries, and inherited context packet as the evidence ledger. A terminal outcome is ready when that evidence is sufficient; permission-authorized inspection requests remain callable.",
		"Actions written only in hidden reasoning are ignored by the tool parser and do not satisfy this contract.",
	)

	return strings.Join(lines, "\n\n")
}

func reviewRepairSourceSanityRepairNeedsEvidence(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if strings.TrimSpace(brief.TaskFamily) != PlanThenExecuteReviewRepairTaskFamily {
		return false
	}

	failed := planThenExecuteReviewRepairFailedPhaseObligations(brief)
	hasSourceSanityFailure := false

	for _, obligation := range failed {
		obligation = strings.TrimSpace(obligation)
		if strings.HasPrefix(obligation, "source_sanity_failed:") || strings.HasPrefix(obligation, "source_sanity_error:") {
			hasSourceSanityFailure = true
			break
		}
	}

	if !hasSourceSanityFailure {
		return false
	}

	return reviewRepairCompletionGateRejectionCount(results) < 2
}

func reviewRepairCompletionGateRejectionCount(results []workspace.ToolResult) int {
	count := 0

	for _, result := range results {
		if reviewRepairCompletionGateViolation(result) {
			count++
		}
	}

	return count
}

func reviewRepairCompletionGateViolation(result workspace.ToolResult) bool {
	if strings.TrimSpace(result.Tool) != completionGateToolName {
		return false
	}
	envelope, ok := completionGateRecoveryEnvelopeFromResult(result)
	if !ok || envelope.InvariantID != completionGateInvariantPlanSubmission {
		return false
	}
	switch envelope.ViolationID {
	case "review_repair_contradictory_pass", "review_repair_closeout":
		return true
	default:
		return false
	}
}

func reviewRepairSourceSanityRepairEvidencePrompt(brief workerruntime.WorkerRuntimeBrief) string {
	lines := []string{
		"The completion gate rejected the review-repair response, and Phase Evidence still has failed source_sanity obligations: " + strings.Join(planThenExecuteReviewRepairFailedPhaseObligations(brief), ", ") + ".",
		"Permission-authorized inspection remains callable before choosing the terminal outcome; the model owns which read, search, discovery, or read-only command can establish the missing review fact.",
		"When the evidence is sufficient, close with exactly one terminal outcome: a corrective `submit_plan` whose rationale starts with `Verification Checklist:`, or `<<QUESTION>>:` naming the exact failed obligation blocker.",
		"`Decision: pass` is invalid while source_sanity remains failed.",
	}

	return strings.Join(lines, "\n\n")
}

func implementationTargetWriteReadySourcedPathSummary(results []workspace.ToolResult, max int) string {
	paths := newToolResultEvidenceProjection(results).SourceWindowPaths()
	if len(paths) == 0 {
		return ""
	}

	paths = uniqueSortedRunnerPaths(paths)
	if max <= 0 || max > len(paths) {
		max = len(paths)
	}

	parts := append([]string{}, paths[:max]...)
	if len(paths) > max {
		parts = append(parts, fmt.Sprintf("+%d more", len(paths)-max))
	}

	return strings.Join(parts, ", ")
}

func (r *runExecution) markConcreteSourceEvidenceObserved(result workspace.ToolResult) {
	if r == nil || r.runner.Workspace == nil {
		return
	}

	for _, path := range newToolResultEvidenceRecord(result).SourceWindowPaths() {
		r.runner.Workspace.MarkPathObserved(path)
	}
}

func (r *runExecution) mutationCompletionRejection(reply string, results []workspace.ToolResult) (workspace.ToolResult, bool) {
	if strings.HasPrefix(r.agentName(), "child:") || strings.HasPrefix(r.agentName(), "child_write:") {
		return workspace.ToolResult{}, false
	}

	if !runnerBriefRequiresMutation(r.state.runtimeBrief) {
		return workspace.ToolResult{}, false
	}

	completionState := r.implementationCompletionStateForTerminalAnswer(reply, results)
	typedBlockerReady := completionState.TerminalDisposition == implementationTerminalDispositionExactBlocker && completionState.CloseoutReady()
	if r.planThenExecutePlannerPhase() && len(r.completionChangedPaths(results)) == 0 {
		if completionState.CloseoutReady() &&
			(completionState.TerminalDisposition == implementationTerminalDispositionPlanSubmitted ||
				completionState.TerminalDisposition == implementationTerminalDispositionSourceSatisfied || typedBlockerReady) {
			return workspace.ToolResult{}, false
		}
		if completionState.TerminalDisposition == implementationTerminalDispositionPlanSubmitted {
			pendingPaths := textutil.Merge(
				completionState.UnresolvedRejectedMutationPaths,
				completionState.MissingRequiredSnippetPaths,
			)
			envelope := newCompletionGateRecoveryEnvelope(
				completionGateInvariantMutationWrite,
				"plan_submitted_with_open_obligations",
				completionGateViolationMutation,
				pendingPaths,
			)
			envelope.PendingObligationIDs = textutil.Merge(
				completionState.OpenTaskLedgerObligationIDs,
				completionState.MissingRequiredSnippetIDs,
			)
			envelope.AcceptedVerificationCommands = append([]string(nil), r.state.runtimeBrief.VerificationPlan...)
			return completionGateRejectionWithRecovery(
				"Plan-execute completion gate rejected open obligations",
				"submit_plan succeeded, but universal completion obligations remain open. Resolve the typed rejected mutation, task-ledger, snippet, or verification obligations before final closeout.",
				envelope,
			), true
		}

		return completionGateRejectionWithRecovery(
			"Plan-execute completion gate rejected final answer",
			planThenExecuteMissingSubmitPlanError(r.state.runtimeBrief.RequiredEditPaths),
			newCompletionGateRecoveryEnvelope(completionGateInvariantPlanSubmission, "submit_plan_missing", completionGateViolationPlanSubmission, nil),
		), true
	}

	if len(r.completionChangedPaths(results)) == 0 {
		if unsupported := r.unsupportedClaimedChangedPaths(reply, results); len(unsupported) > 0 {
			return completionGateRejectionWithRecovery(
				"Mutation completion gate rejected unsupported change claim",
				"final answer reports changed paths not backed by successful write tool results: "+quotedRunnerStrings(unsupported)+". Satisfy the missing write contract; prose blocker/no-op claims are not completion evidence.",
				newCompletionGateRecoveryEnvelope(completionGateInvariantMutationWrite, "unsupported_change_claim", completionGateViolationMutation, unsupported),
			), true
		}

		if mutationReplyClaimsCompletedWorkspaceChange(reply) {
			return completionGateRejectionWithRecovery(
				"Mutation completion gate rejected unsupported change claim",
				"final answer claims the workspace change was completed, but no successful write tool result is present. Satisfy the missing write contract or record a validated typed blocker in the task ledger; prose blocker/no-op claims are not completion evidence.",
				newCompletionGateRecoveryEnvelope(completionGateInvariantMutationWrite, "unsupported_change_claim", completionGateViolationMutation, nil),
			), true
		}

		if typedBlockerReady {
			return workspace.ToolResult{}, false
		}

		return completionGateRejectionWithRecovery(
			"Mutation completion gate rejected final answer",
			"this task asks for a workspace change, but no successful write tool result is present. The contract outcomes are the smallest necessary edit or a validated typed blocker recorded by the task ledger.",
			newCompletionGateRecoveryEnvelope(completionGateInvariantMutationWrite, "workspace_write_missing", completionGateViolationMutation, r.remainingRequiredEditPaths(results)),
		), true
	}

	if unsupported := r.unsupportedClaimedChangedPaths(reply, results); len(unsupported) > 0 {
		return completionGateRejectionWithRecovery(
			"Mutation completion gate rejected final answer",
			"final answer reports changed paths not backed by successful write tool results: "+quotedRunnerStrings(unsupported)+". Satisfy the missing write contract, or correct the changed-paths summary to only include recorded edits.",
			newCompletionGateRecoveryEnvelope(completionGateInvariantMutationWrite, "unsupported_change_claim", completionGateViolationMutation, unsupported),
		), true
	}

	if missing := r.remainingRequiredEditPaths(results); len(missing) > 0 {
		if !typedBlockerReady {
			return completionGateRejectionWithRecovery(
				"Mutation completion gate rejected final answer",
				"required edit path(s) were not changed: "+quotedRunnerStrings(missing)+". Satisfy the required-path write contract or record a validated typed blocker for every skipped path.",
				newCompletionGateRecoveryEnvelope(completionGateInvariantMutationRequiredPaths, "required_edit_paths_remaining", completionGateViolationMutation, missing),
			), true
		}
	}

	if completionState.HasPendingRejectedMutation() {
		if typedBlockerReady {
			return workspace.ToolResult{}, false
		}
		return completionGateRejectionWithRecovery(
			"Mutation completion gate rejected unresolved write intent",
			"workspace write attempt(s) were rejected and not later satisfied by successful writes: "+quotedRunnerStrings(completionState.UnresolvedRejectedMutationPaths)+". Retry those intended edits or record a validated typed blocker for every unresolved path.",
			newCompletionGateRecoveryEnvelope(completionGateInvariantMutationWrite, "rejected_mutation_paths_remaining", completionGateViolationMutation, completionState.UnresolvedRejectedMutationPaths),
		), true
	}

	if removed := r.unreportedRemovedTestDeclarations(reply, results); len(removed) > 0 {
		envelope := newCompletionGateRecoveryEnvelope(completionGateInvariantSilentTestRemoval, "silent_test_removal_unreported", completionGateViolationMutation, changedTestSourcePaths(results))
		envelope.PendingObligationIDs = append([]string(nil), removed...)
		return completionGateRejectionWithRecovery(
			"Mutation completion gate rejected silent test removal",
			"git diff shows existing test declaration(s) removed without the final answer reporting them: "+quotedRunnerStrings(removed)+". If the removal is intentional, report each removed test with verification status; otherwise restore the deleted test or fix the edit boundary.",
			envelope,
		), true
	}

	if missing := r.missingRequiredEditSnippets(results); len(missing) > 0 {
		if typedBlockerReady {
			return workspace.ToolResult{}, false
		}
		envelope := newCompletionGateRecoveryEnvelope(completionGateInvariantMutationSnippets, "required_edit_snippets_missing", completionGateViolationMutation, r.state.runtimeBrief.RequiredEditPaths)
		envelope.PendingObligationIDs = append([]string(nil), missing...)
		return completionGateRejectionWithRecovery(
			"Mutation completion gate rejected final answer",
			"required edit snippet(s) are not present after the recorded edits: "+quotedRunnerStrings(missing)+". Make the missing edit(s) or record a validated typed blocker for each skipped required edit.",
			envelope,
		), true
	}

	if typedBlockerReady {
		return workspace.ToolResult{}, false
	}

	if workerruntime.VerificationContractSkipsGate(r.state.runtimeBrief.VerificationContract) {
		return workspace.ToolResult{}, false
	}

	verificationLedger := r.postWriteVerificationLedger(results)
	if verificationLedger.Pending() {
		errorText := "workspace changes are present, but the latest successful write is not followed by successful verification. Required evidence is a relevant language/project test/build/check result, compiler-backed diagnostics, or the concrete verification blocker."
		if plannerOwnsVerification(r.state.runtimeBrief) {
			errorText = "workspace changes are present, but the latest successful write is not followed by lane sanity. This route delegates build/test verification to the planner; source edits require compiler-backed source-sanity evidence, and non-source edits may use git diff --check only as patch-hygiene evidence or state the concrete planner-owned verification blocker."
		}

		envelope := newCompletionGateRecoveryEnvelope(completionGateInvariantPostWriteVerification, "post_write_verification_pending", completionGateViolationVerification, verificationLedger.PendingPaths)
		envelope.AcceptedVerificationCommands = append([]string(nil), r.state.runtimeBrief.VerificationPlan...)
		return completionGateRejectionWithRecovery(
			"Mutation completion gate rejected final answer",
			errorText,
			envelope,
		), true
	}

	semanticVerification := r.semanticVerificationLedger(results)
	if semanticVerification.Pending() {
		errorText := "source edits are present, but the available verification is only patch hygiene (`git diff --check`), generic source-sanity fallback, or no compiler-backed source-aware check. Required evidence is a focused language-appropriate build/test/check command, compiler-backed diagnostics/source-sanity, or the concrete verification blocker; patch hygiene alone cannot certify source edits."
		if plannerOwnsVerification(r.state.runtimeBrief) {
			errorText = "source edits are present, but the available lane sanity is only patch hygiene (`git diff --check`), generic source-sanity fallback, or no compiler-backed source-aware check. planner-owned verification lanes require compiler-backed source-sanity or language diagnostics for the changed source path. If source sanity is unavailable, answer with changed paths plus the concrete lane-sanity blocker. Do not claim patch hygiene certifies source edits."
		}

		envelope := newCompletionGateRecoveryEnvelope(completionGateInvariantSemanticVerification, "semantic_verification_pending", completionGateViolationVerification, semanticVerification.PendingPaths)
		envelope.AcceptedVerificationCommands = append([]string(nil), r.state.runtimeBrief.VerificationPlan...)
		return completionGateRejectionWithRecovery(
			"Mutation completion gate rejected final answer",
			errorText,
			envelope,
		), true
	}

	return workspace.ToolResult{}, false
}

func mutationReplyClaimsCompletedWorkspaceChange(reply string) bool {
	trimmed := strings.TrimSpace(reply)
	if trimmed == "" || questionProtocolAnswer(trimmed) {
		return false
	}

	lower := strings.ToLower(strings.Join(strings.Fields(trimmed), " "))
	if ContainsAny(lower, "already present", "already implemented", "already satisfied", "already up to date") &&
		ContainsAny(lower, "no change", "no-op", "no op", "nothing to change") {

		return false
	}

	if len(pathparse.MentionedWorkspacePaths(normalizeWorkspacePathScanText(trimmed))) == 0 {
		for _, fragment := range pathparse.QuotedFragments(trimmed) {
			if _, ok := pathparse.NormalizeWorkspacePathToken(fragment); ok {
				return ContainsAny(lower,
					"has been edited",
					"have edited",
					"has been updated",
					"have updated",
					"updated so",
					"changed so",
					"implemented",
					"applied the edit",
					"made the edit",
					"source read confirms the edit",
				)
			}
		}

		return false
	}

	return ContainsAny(lower,
		"has been edited",
		"have edited",
		"has been updated",
		"have updated",
		"updated so",
		"changed so",
		"implemented",
		"applied the edit",
		"made the edit",
		"source read confirms the edit",
	)
}

func (r *runExecution) escalateCompletionGateResultMessage(result workspace.ToolResult, priorRejections int, allowedTools []string) string {
	_, sourceGrounding := sourceGroundingAssessmentFromResult(result)
	sourceBodyReadAvailable := sourceGrounding && r.sourceGroundingBodyReadAvailable(allowedTools)
	return escalateCompletionGateMessageWithSourceRecovery(
		result.Error,
		priorRejections,
		sourceGrounding,
		sourceBodyReadAvailable,
	)
}

func escalateCompletionGateMessageWithSourceRecovery(base string, priorRejections int, sourceGrounding bool, sourceBodyReadAvailable bool) string {
	trimmed := strings.TrimSpace(base)
	if trimmed == "" {
		return base
	}
	if priorRejections <= 0 {
		return base
	}

	attempt := priorRejections + 1

	label := "gate retry"
	guidance := "select one valid recovery from the active completion contract instead of repeating the rejected answer"
	if sourceGrounding {
		label = "source evidence recovery"
		guidance = "select one valid strategy from the typed source-grounding recovery contract instead of repeating the rejected answer"
		if sourceBodyReadAvailable {
			guidance += "; source-body reads are available but are not the only valid strategy"
		}
	}

	return fmt.Sprintf("[%s attempt %d] %s — %s.", label, attempt, trimmed, guidance)
}

const sourceGroundingAdditionalReadRecoveryRetries = 2

func (r *runExecution) completionGateRetryLimit(results []workspace.ToolResult) int {
	if r != nil && r.unscopedNoWriteCompletionRecoveryApplies(results) {
		return 2
	}

	if r != nil && planThenExecuteSubmitCorrectionActive(r.state.runtimeBrief, results, r.planThenExecutePathAuthority()) {
		return 2
	}

	if r != nil && r.planThenExecutePlannerPhase() && len(r.state.runtimeBrief.RequiredEditPaths) > 0 && successfulSubmitPlanResultCount(results) == 0 {
		return 1
	}

	if r != nil && r.textualRequiredPathCompletionRecoveryApplies(results) {
		return 2
	}

	if r != nil && r.completionGateMutationRecoveryApplies(results) {
		return 1
	}

	if r != nil && workerruntime.PermissionProfileIsReadOnlyWorkspace(r.state.runtimeBrief.PermissionProfile) &&
		!runnerBriefRequiresMutation(r.state.runtimeBrief) {

		return 3
	}

	return 2
}

func (r *runExecution) completionGateActiveRetryLimit(result workspace.ToolResult, results []workspace.ToolResult, allowedTools []string) int {
	limit := r.completionGateRetryLimit(results)
	if _, ok := sourceGroundingAssessmentFromResult(result); ok && r.sourceGroundingBodyReadAvailable(allowedTools) {
		return limit + sourceGroundingAdditionalReadRecoveryRetries
	}
	return limit
}

func planThenExecuteMissingSubmitPlanError(requiredEditPaths []string) string {
	if len(requiredEditPaths) > 0 {
		return "plan-execute planner phase requires a successful submit_plan tool result before final answer, or a source-satisfied answer that names every required path and cites current-state evidence. Call `submit_plan`: " + planThenExecuteSubmitPlanCoverageSentence() + " Or answer `Decision: source-satisfied`, or answer with <<QUESTION>> naming the exact path-specific blocker."
	}

	return "plan-execute planner phase requires a successful submit_plan tool result before final answer, or a source-satisfied answer that names the relevant path(s) and cites current-state evidence. Call `submit_plan` for the needed candidate/allowed edit path(s), answer `Decision: source-satisfied`, or answer with `<<QUESTION>>:` naming the exact blocker."
}

func (r *runExecution) sourceGroundingBodyReadAvailable(allowedTools []string) bool {
	if r == nil {
		return false
	}

	brief := workerruntime.NormalizeRuntimeBrief(r.state.runtimeBrief)
	if !workerruntime.PermissionProfileIsReadOnlyWorkspace(brief.PermissionProfile) ||
		len(brief.RequiredEditPaths) > 0 ||
		runnerBriefRequiresMutation(brief) {

		return false
	}

	tools := textutil.Unique(append(append([]string(nil), brief.AllowedTools...), allowedTools...))

	return sourceGroundingBodyReadToolsAvailable(tools)
}

func sourceGroundingBodyReadToolsAvailable(tools []string) bool {
	return toolAllowed(tools, workspace.ToolNameReadFile) ||
		toolAllowed(tools, workspace.ToolNameCodeNavigate)
}

func (r *runExecution) retryableFinalAnswerAcceptanceRejection(acceptance finalAnswerAcceptance, results []workspace.ToolResult, allowedTools []string) bool {
	if r == nil {
		return false
	}

	brief := workerruntime.NormalizeRuntimeBrief(r.state.runtimeBrief)
	violationKind, hasViolation := finalAnswerAcceptanceViolationKind(acceptance)
	if hasViolation &&
		violationKind == turnViolationRejectedMutationPathsRemaining &&
		r.implementationCompletionState(results).HasPendingRejectedMutation() {

		return implementationWriteCapabilityAvailable(runnerCurrentAllowedOrBriefTools(allowedTools, brief.AllowedTools))
	}

	if hasViolation &&
		violationKind == turnViolationRequiredEvidenceUnmet &&
		runnerBriefRequiresMutation(brief) &&
		implementationTargetWriteReadySurfaceActive(brief, results, len(r.completionChangedPaths(results)) > 0) &&
		len(r.completionChangedPaths(results)) == 0 {

		return true
	}

	// Source-grounding rejection is evidence repair, including after an
	// implementation write. When the current permission-authorized surface
	// still exposes inspection tools, return control to the model so it can
	// refresh stale cited windows and author a supported final answer. The
	// active completion-recovery streak remains the sole retry limit.
	if hasViolation && violationKind == turnViolationSourceGroundingUnmet {
		if len(brief.RequiredEditPaths) > 0 && workerruntime.PermissionProfileIsReadOnlyWorkspace(brief.PermissionProfile) {
			return false
		}
		if runnerBriefRequiresMutation(brief) &&
			!workerruntime.RuntimeContractMatches(brief.Contract, workerruntime.ContractImplementationResult) {
			return false
		}
		tools := textutil.Unique(append(append([]string(nil), brief.AllowedTools...), allowedTools...))
		return len(genericReadOnlyEvidenceTools(tools)) > 0
	}

	if r.planThenExecutePlannerPhase() && planThenExecuteAnchorRefreshFinalAnswerRejection(acceptance) {
		return r.planThenExecuteAnchorRefreshFinalAnswerPending(results)
	}

	retryPolicy := turnFinalAnswerRejectionRetryPolicyForBrief(brief, acceptance)
	if !retryPolicy.Retryable {
		return false
	}

	if !retryPolicy.RequiresReadOnlyEvidence {
		return true
	}

	if !workerruntime.PermissionProfileIsReadOnlyWorkspace(brief.PermissionProfile) ||
		len(brief.RequiredEditPaths) > 0 ||
		runnerBriefRequiresMutation(brief) {

		return false
	}

	tools := textutil.Unique(append(append([]string(nil), brief.AllowedTools...), allowedTools...))

	return len(genericReadOnlyEvidenceTools(tools)) > 0
}

func (r *runExecution) textualRequiredPathCompletionRecoveryApplies(results []workspace.ToolResult) bool {
	return r != nil &&
		!r.supportsNativeTools() &&
		runnerBriefRequiresMutation(r.state.runtimeBrief) &&
		len(r.remainingRequiredEditPaths(results)) > 0
}

func (r *runExecution) unscopedNoWriteCompletionRecoveryApplies(results []workspace.ToolResult) bool {
	if r == nil || !runnerBriefRequiresMutation(r.state.runtimeBrief) {
		return false
	}

	return len(r.completionChangedPaths(results)) == 0 && len(r.state.runtimeBrief.RequiredEditPaths) == 0
}

func (r *runExecution) completionGateMutationRecoveryApplies(results []workspace.ToolResult) bool {
	if r == nil || !runnerBriefRequiresMutation(r.state.runtimeBrief) {
		return false
	}

	completionState := r.implementationCompletionState(results)
	if len(completionState.MissingRequiredPaths) > 0 {
		return true
	}

	if len(r.missingRequiredEditSnippets(results)) > 0 {
		return true
	}

	if len(r.completionChangedPaths(results)) == 0 && len(r.state.runtimeBrief.RequiredEditPaths) == 0 {
		return true
	}

	return completionState.HasPendingRejectedMutation() || completionState.VerificationOpen()
}

func (r *runExecution) completionGateRepeatedViolation(results []workspace.ToolResult) turnViolation {
	if r == nil || !runnerBriefRequiresMutation(r.state.runtimeBrief) {
		return turnCompletionGateRepeatedViolation(turnViolationCompletionGateRepeatedRejection)
	}
	completion := r.implementationCompletionState(results)
	if completion.VerificationFailed {
		if plannerOwnsVerification(r.state.runtimeBrief) {
			return turnCompletionGateRepeatedViolation(turnViolationCompletionGateLaneSanityRepeated)
		}

		return turnCompletionGateRepeatedViolation(turnViolationCompletionGateVerificationRepeated)
	}

	if r.planThenExecutePlannerPhase() && len(r.state.runtimeBrief.RequiredEditPaths) > 0 && successfulSubmitPlanResultCount(results) == 0 {
		if planThenExecuteUnreadRequiredSourceNudge(r.state.runtimeBrief, results, r.planThenExecutePathAuthority()) != "" {
			return turnCompletionGateRepeatedViolation(turnViolationCompletionGateRequiredSourceReadsRemainingRepeated)
		}

		if _, _, ok := lastRejectedSubmitPlanCoverageResult(results); ok {
			return turnCompletionGateRepeatedViolation(turnViolationCompletionGateSubmitPlanInvalidRepeated)
		}

		return turnCompletionGateRepeatedViolation(turnViolationCompletionGateSubmitPlanMissingRepeated)
	}

	if len(completion.MissingRequiredSnippetIDs) > 0 {
		return turnCompletionGateRepeatedViolation(turnViolationCompletionGateRequiredSnippetsMissingRepeated)
	}
	if completion.HasPendingRejectedMutation() || len(completion.OpenTaskLedgerObligationIDs) > 0 {
		return turnCompletionGateRepeatedViolation(turnViolationCompletionGateRepeatedRejection)
	}
	if len(completion.MissingRequiredPaths) > 0 {
		return turnCompletionGateRepeatedViolation(turnViolationCompletionGateRequiredPathsRemainingRepeated)
	}

	if completion.VerificationPending || completion.SemanticVerificationPending {

		if plannerOwnsVerification(r.state.runtimeBrief) {
			return turnCompletionGateRepeatedViolation(turnViolationCompletionGateLaneSanityRepeated)
		}

		return turnCompletionGateRepeatedViolation(turnViolationCompletionGateVerificationRepeated)
	}

	return turnCompletionGateRepeatedViolation(turnViolationCompletionGateRepeatedRejection)
}

func (r *runExecution) completionGateRecoveryPrompt(results []workspace.ToolResult) (prompt string, ok bool) {
	if r == nil {
		return "", false
	}

	if prompt := r.readOnlyEvidenceCompletionRecoveryPrompt(results); prompt != "" {
		return prompt, true
	}

	if strings.TrimSpace(r.state.runtimeBrief.TaskFamily) == PlanThenExecuteReviewRepairTaskFamily {
		if reviewRepairSourceSanityRepairNeedsEvidence(r.state.runtimeBrief, results) {
			return reviewRepairSourceSanityRepairEvidencePrompt(r.state.runtimeBrief), true
		}

		return reviewRepairCompletionGateRecoveryPrompt(r.state.runtimeBrief), true
	}

	if !runnerBriefRequiresMutation(r.state.runtimeBrief) {
		return "", false
	}
	completion := r.implementationCompletionState(results)
	verificationLedger := r.postWriteVerificationLedger(results)

	if verificationLedger.HasFailure {
		return postWriteVerificationFailedRecoveryPrompt(verificationLedger.LatestFailed, plannerOwnsVerification(r.state.runtimeBrief)), true
	}

	if missing := completion.MissingRequiredSnippetIDs; len(missing) > 0 {
		return fmt.Sprintf("The completion gate rejected a final answer while required edit snippet(s) are still missing: %s. Permission-authorized read/search tools remain callable for a fresh model-selected request. The terminal contract outcomes are snippet-backed write capability or <<QUESTION>> naming the exact blocker for each missing snippet.", strings.Join(missing, ", ")), true
	}

	if pending := completion.UnresolvedRejectedMutationPaths; len(pending) > 0 {
		return strings.Join([]string{
			"The completion gate rejected final prose because a workspace write attempt remains unresolved: " + strings.Join(pending, ", ") + ".",
			"The implementation remains write-ready even if earlier successful writes are also pending verification. Retry the rejected path using fresh source-backed arguments, take one bounded read/history probe only for an exact edit-safety gap, or answer `<<QUESTION>>:` with the exact path-specific blocker.",
			"Do not claim completion until a successful write or exact blocker resolves every listed path; verify the final changed set afterward.",
		}, "\n\n"), true
	}

	if open := completion.OpenTaskLedgerObligationIDs; len(open) > 0 {
		return strings.Join([]string{
			"The completion gate rejected final prose while task-ledger work remains open: " + strings.Join(open, ", ") + ".",
			"Continue only the action-backed unfinished work and update `task_ledger` with evidence as it contracts. If the work cannot continue, answer `<<QUESTION>>:` with the exact task-specific blocker.",
			"Do not claim successful completion while these task-ledger obligations remain open.",
		}, "\n\n"), true
	}

	if missing := completion.MissingRequiredPaths; len(missing) > 0 {
		if r.planThenExecutePlannerPhase() {
			state := r.derivePlanExecutePlanningState(results, PlanExecutePlanningTransport{TextualToolParser: true})
			if state.Mode == PlanExecutePlanningModeAnchorRefresh && len(state.RefreshPaths) > 0 {
				refreshPaths := state.RefreshPaths
				target := "the rejected required path"

				if len(refreshPaths) > 0 {
					target = strings.Join(refreshPaths, ", ")
				}

				return strings.Join([]string{
					"Recovery decision: workflow_id=recovery state=inspecting terminal_state=recovery-open dispatchable=false reason=anchor_refresh callable_tools=read_file, code_navigate safe_alternatives=bounded_source_read.",
					"The completion gate rejected a final answer after `submit_plan` was rejected for stale or missing edit anchors.",
					"A current source-body window for the rejected anchor is required before submit_plan can be accepted: " + target + ". Permission-authorized inspection requests remain callable.",
					"When the evidence is sufficient, issue a fresh parser-readable `submit_plan`; otherwise choose any permission-authorized inspection request that can resolve the missing anchor fact.",
				}, "\n\n"), true
			}

			if nudge := planThenExecuteUnreadRequiredSourceNudge(r.state.runtimeBrief, results, r.planThenExecutePathAuthority()); nudge != "" {
				return strings.Join([]string{
					"The completion gate rejected a final answer before every required_edit_path produced source-body evidence.",
					"Do not draft `submit_plan` from outline/search hits alone. A producer source-body window is still required for each edited path; permission-authorized inspection requests remain callable.",
					nudge,
				}, "\n\n"), true
			}

			if pending := planThenExecuteHistoryPendingPaths(r.state.runtimeBrief, results, nil, r.planThenExecutePathAuthority()); len(pending) > 0 {
				return strings.Join([]string{
					"The completion gate rejected a final answer before required git-history evidence was produced for every required_edit_path.",
					"Source-body evidence is complete, but required history is still pending before `submit_plan` can be accepted. Permission-authorized inspection requests remain callable.",
					"Use `history_range` or `history_symbol` when ready to satisfy one pending history path, or continue with another permission-authorized inspection request.",
					"Pending git-history path(s): " + strings.Join(pending, ", "),
				}, "\n\n"), true
			}

			correction := planThenExecuteSubmitPlanVisibleJSONInstruction()

			if rejected, _, ok := lastRejectedSubmitPlanCoverageResult(results); ok {
				correctionBrief := lastSubmitPlanRejectionBrief(rejected)
				if planThenExecuteSubmitCorrectionActive(r.state.runtimeBrief, results, r.planThenExecutePathAuthority()) {
					correctionBrief = lastSubmitPlanCorrectionRejectionBrief(rejected)
				}

				correction = correctionBrief + planThenExecuteSubmitPlanCorrectionInstructionForBrief(r.state.runtimeBrief, rejected)
			}

			if planThenExecuteSubmitCorrectionActive(r.state.runtimeBrief, results, r.planThenExecutePathAuthority()) {
				outcome := "Your next visible message must be exactly one parser-readable `submit_plan` request. " + planThenExecuteSubmitPlanCoverageSentence() + " Do not answer `Decision: source-satisfied`, do not answer `<<QUESTION>>:`, and do not reply in prose."
				if planThenExecuteSubmitCorrectionBlockerAllowed(r.state.runtimeBrief, results) {
					outcome = "Your next visible message must be a parser-readable `submit_plan` request, unless no safe executable edit body can be produced from gathered evidence after repeated rejected submit_plan attempts; in that case answer `<<QUESTION>>:` with the exact path-specific missing body/anchor blocker. " + planThenExecuteSubmitPlanCoverageSentence() + " Do not answer `Decision: source-satisfied`, and do not reply in prose."
				}
				return fmt.Sprintf("Recovery decision: workflow_id=recovery state=inspecting terminal_state=recovery-open dispatchable=false reason=submit_ready terminal_outcomes=submit_plan, Decision: source-satisfied, <<QUESTION>>:.\n\nThe completion gate rejected a final answer before the corrected plan-submission contract (`submit_plan`) produced a valid artifact for required edit path(s): %s. Previous rejected submit_plan drafts do not count as coverage. Permission-authorized read/search tools remain callable for a fresh model-selected request. %s %s For missing/new files use explicit replacement edit shapes: `replace_all:true` with empty `old_text`, `new_text` as the full file body, and a one-sentence `purpose`. Existing source/test files must use bounded anchored edits.", strings.Join(missing, ", "), outcome, correction), true
			}

			return fmt.Sprintf("Recovery decision: workflow_id=recovery state=inspecting terminal_state=recovery-open dispatchable=false reason=submit_ready terminal_outcomes=submit_plan, Decision: source-satisfied, <<QUESTION>>:.\n\nThe completion gate rejected a final answer before the plan-submission contract (`submit_plan`) produced a valid artifact for required edit path(s): %s. Previous rejected submit_plan drafts do not count as coverage. Permission-authorized read/search tools remain callable for a fresh model-selected request. Your next visible message must be exactly one parser-readable action, not the same prose. Actions written only in hidden reasoning are ignored by the tool parser and do not satisfy this contract. If the gathered evidence supports the requested changes, your next response may be a parser-readable `submit_plan` request. %s %s For missing/new files use explicit replacement edit shapes: `replace_all:true` with empty `old_text`, `new_text` as the full file body, and a one-sentence `purpose`. Existing source/test files must use bounded anchored edits. If a required missing source/test path has only behavior/helper requirements and no substantial inline body or explicit replacement shape, answer with <<QUESTION>> naming the missing body evidence. The other valid outcome is `Decision: source-satisfied` naming every path plus current-state evidence if the requested final state is already present.", strings.Join(missing, ", "), planThenExecuteSubmitPlanCoverageSentence(), correction), true
		}

		if briefIsScratchpadDeliverableLane(r.state.runtimeBrief) {
			return fmt.Sprintf("The completion gate rejected a final answer while non-source deliverable path(s) remain unwritten: %s. The deliverable-write contract requires full corrected content with gathered evidence plus any missing, stale, ambiguous, or out-of-scope source fact recorded as `Source verification pending:` or `## Scope Correction Required`. Answer with <<QUESTION>> only if the deliverable cannot be written from the gathered evidence plus recorded uncertainty.", strings.Join(missing, ", ")), true
		}

		if exactAnchorPreWriteSurfaceActive(r.state.runtimeBrief, results, r.implementationCompletionState(results).HasChanges()) {
			return r.implementationRequiredPathCompletionRecoveryPrompt(missing, results, true), true
		}

		return r.implementationRequiredPathCompletionRecoveryPrompt(missing, results, false), true
	}

	if r.planThenExecutePlannerPhase() && len(r.state.runtimeBrief.RequiredEditPaths) == 0 && successfulSubmitPlanResultCount(results) == 0 {
		gateRejected := completionGateResultSeen(results)
		gateRejections := completionGateResultCount(results)
		readPathSet := newToolResultEvidenceProjection(results).ReadPaths()
		readPaths := make([]string, 0, len(readPathSet))

		for path := range readPathSet {
			readPaths = append(readPaths, path)
		}

		sort.Strings(readPaths)

		candidatePaths := candidateRecoveryScopePaths(r.state.runtimeBrief, results, 4)
		for _, path := range readPaths {
			if !slices.Contains(candidatePaths, path) {
				candidatePaths = append([]string{path}, candidatePaths...)
			}
		}

		targetSummary := "No concrete candidate path has been read yet."
		nextAction := "If one edit-safety fact is missing, call one bounded read/search on the highest-confidence candidate path with a named missing edit-safety fact."
		if len(candidatePaths) > 0 {
			targetSummary = "Current candidate path evidence: " + strings.Join(candidatePaths, ", ")
			nextAction = fmt.Sprintf("If one edit-safety fact is missing, read the highest-confidence candidate path now: `read_file` on `%s` with a named missing edit-safety fact. Otherwise call `submit_plan`.", candidatePaths[0])
		}

		if len(readPaths) > 0 {
			targetSummary = "Read candidate path(s): " + strings.Join(readPaths, ", ")

			if gateRejected && gateRejections >= 2 {
				nextAction = "Allowed/candidate paths are not coverage obligations. Permission-authorized read/search tools remain callable for a fresh model-selected request. When the evidence is sufficient, choose one terminal planner outcome: call `submit_plan` using exact source anchors from the read output, answer `Decision: source-satisfied` with current-state evidence, or answer `<<QUESTION>>:` with the exact blocker."
			} else if gateRejected {
				if unread := unreadCandidateRecoveryScopePathsForRecovery(r.state.runtimeBrief, results, 8, r.planThenExecutePathAuthority()); len(unread) > 0 {
					nextAction = "Allowed/candidate paths are not coverage obligations. If the read source is enough, call `submit_plan` using exact source anchors from the read output, answer `Decision: source-satisfied` with current-state evidence, or answer `<<QUESTION>>:` with the exact blocker. If the rejected answer was premature because one concrete edit-safety fact is still missing on another source/test surface, make exactly one bounded source read for a different unread candidate path with a named edit-safety fact: " + strings.Join(unread, ", ") + "."
				} else {
					nextAction = "Allowed/candidate paths are not coverage obligations. If the read source is enough, call `submit_plan` using exact source anchors from the read output, answer `Decision: source-satisfied` with current-state evidence, or answer `<<QUESTION>>:` with the exact blocker. If one concrete edit-safety fact is still missing, make exactly one bounded read/search request naming that missing fact instead of repeating the final-answer prose."
				}
			} else if unread := unreadCandidateRecoveryScopePathsForRecovery(r.state.runtimeBrief, results, 4, r.planThenExecutePathAuthority()); len(unread) > 0 {
				nextAction = "Allowed/candidate paths are not coverage obligations. Call `submit_plan` using exact source anchors from the read output when enough evidence exists. If source-satisfied closeout depends on another candidate path, read one different unread candidate path with a bounded `read_file` range now: " + strings.Join(unread, ", ") + ". Do not reread a candidate path that already has source evidence."
			} else {
				nextAction = "Allowed/candidate paths are not coverage obligations. Permission-authorized read/search tools remain callable for a fresh model-selected request. Call `submit_plan` using exact source anchors from the read output, answer `Decision: source-satisfied` with current-state evidence, or answer `<<QUESTION>>:` with the exact blocker when the evidence is sufficient."
			}
		}

		return strings.Join([]string{
			"The completion gate rejected a final answer before the plan-submission contract (`submit_plan`) produced an artifact for candidate/allowed scope. No required_edit_paths list is active, so choose the needed candidate path(s) from the objective and target refs instead of treating the allowlist as coverage.",
			targetSummary,
			"Your next visible message must be exactly one parser-readable action, not the same prose. Do not include analysis before or after the action.",
			"Actions written only in hidden reasoning are ignored by the tool parser and do not satisfy this contract. The `submit_plan`, `Decision: source-satisfied`, `<<QUESTION>>:` outcome must appear in visible content or a native tool call.",
			"For textual tool transport, any bounded read/search/navigation action is one visible JSON object with a top-level `tool` field plus that tool's arguments, for example `" + workspace.CodeNavigateRequestExample(semanticlanguage.CapReadSymbol, "path/from/evidence.go", "SymbolFromEvidence") + "`. XML-like tags and key-value notes are not accepted tool calls unless they contain a parser-readable JSON object.",
			"For textual tool transport, a `submit_plan` action is one visible JSON object with top-level `tool`, `rationale`, and `edits` keys; `edits` must be a JSON array, not a quoted string.",
			"When submitting, each `submit_plan.edits[]` entry must use a real candidate path plus exactly one executable shape: exact `old_text`/`new_text`, replacement `start_line`/`end_line`/`content`, small inline insertion-only `insert_after_line`/`content`, or missing/new-file-only `replace_all:true`/`new_text`; include a one-sentence `purpose`. Existing source/test files must use bounded anchored edits. For large additions, use line-range replacement of unchanged anchor line(s) plus inserted content. Edit bodies must be complete executable source text; do not copy schema examples, placeholders, ellipses, or prose as field values.",
			nextAction,
			"The only non-tool closeouts are a visible answer starting exactly with `Decision: source-satisfied` plus current-state evidence for the relevant path(s), or a visible `<<QUESTION>>:` naming the exact blocker. Do not call per-edit write tools.",
		}, "\n\n"), true
	}

	if r.unscopedNoWriteCompletionRecoveryApplies(results) {
		if implementationTargetWriteReadySurfaceActive(r.state.runtimeBrief, results, len(r.completionChangedPaths(results)) > 0) && len(r.completionChangedPaths(results)) == 0 {
			return r.implementationTargetWriteReadyTerminalRecoveryPrompt(results), true
		}

		if r.unscopedNoWriteTerminalRecoveryReady(results) {
			return r.unscopedNoWriteTerminalRecoveryPrompt(results), true
		}

		candidatePaths := concreteRecoveryEvidencePaths(results, 4)
		writeStep := "Do not call a write tool until prior tool results contain source-body evidence for the concrete edit path and the exact replacement shape. When that evidence is present, emit one parser-readable write: `edit_file`, `write_file`, `append_file`, or `apply_patch`."

		if len(candidatePaths) > 0 {
			writeStep = fmt.Sprintf("Do not call a write tool until prior tool results contain source-body evidence for the concrete edit path and the exact replacement shape. Candidate path(s) with evidence so far: %s. When that evidence is present, emit one parser-readable write: `edit_file`, `write_file`, `append_file`, or `apply_patch`, using only real path and text values from the source evidence. Do not copy placeholder edit bodies or schema examples.", strings.Join(candidatePaths, ", "))
		}

		targetDiscoveryStep := "If the edit target is not identified, call `context_pack` for the user's objective rather than repeating a literal search that already missed. Use `mode:\"plan\"`, a small limit, and a task value copied from the actual user objective rather than a placeholder."
		if len(candidatePaths) > 0 {
			targetDiscoveryStep = fmt.Sprintf("Current evidence paths already identify candidate target(s): %s.\n\nIf one edit-safety fact is still missing, read the highest-confidence candidate instead of restarting discovery:\n\n```tool\n{\"tool\":\"read_file\",\"path\":\"%s\",\"start_line\":1,\"end_line\":180,\"named_gap\":\"exact current source for the requested behavior\",\"why_not_answering\":\"need current source before the write\"}\n```\n\nDo not call `context_pack` again unless every listed candidate is proven irrelevant.", strings.Join(candidatePaths, ", "), candidatePaths[0])
		} else if contextPackUnavailableForRecovery(results) {
			targetDiscoveryStep = "The previous `context_pack` result failed or returned no usable target candidates, so do not call `context_pack` again in this recovery. Run a bounded fallback discovery phase instead: first `search_files` for likely filenames/components/domain terms, then `search_content` for stable labels, symbols, config keys, command names, or error text. If a literal phrase missed, broaden to nearby concepts. Read the highest-confidence candidate with `read_file` or a language-aware symbol tool before writing."
		} else if contextPackSeen(results) {
			targetDiscoveryStep = "The previous `context_pack` did not establish an exact edit target. Do not write or read a context-pack candidate unless it contains exact current-state evidence for the requested behavior. Run bounded fallback discovery instead: use `search_content` for stable domain terms, symbols, config keys, command names, or error text from the user's objective, then read the highest-confidence hit before writing."
		}

		return strings.Join([]string{
			"The completion gate rejected a final answer before any successful workspace write. No required_edit_paths list is active, so this is a target-selection failure, not a required-path coverage failure.",
			"Your next visible message must be exactly one parser-readable action, not prose and not an implementation summary. Do not include analysis before or after the action.",
			writeStep,
			textualMutationEditFileJSONInstruction(),
			targetDiscoveryStep,
			"If one edit-safety fact is missing after a concrete candidate exists, call exactly one bounded discovery/read tool with `named_gap` and `why_not_answering` that names the fact needed before the write.",
			"The only non-tool closeout is `<<QUESTION>>:` naming the exact blocker, such as no resolvable edit target, missing permission, or missing body evidence. Do not repeat a final answer without a tool-backed write.",
		}, "\n\n"), true
	}

	if verificationLedger.Pending() || completion.SemanticVerificationPending {

		if plannerOwnsVerification(r.state.runtimeBrief) {
			return postWriteVerificationPrompt(verificationLedger, true, "The completion gate rejected a final answer because"), true
		}

		return postWriteVerificationPrompt(verificationLedger, false, "The completion gate rejected a final answer because"), true
	}

	return "", false
}

func textualMutationWriteJSONInstruction() string {
	return "For textual tool transport, a write action is either one visible JSON object with a top-level `tool` field plus real source-backed arguments, or the Qwen key-value `tool:` format with the same real arguments. Function-call write syntax is not executable."
}

func textualMutationEditFileJSONInstruction() string {
	return textualMutationWriteJSONInstruction() + " For `edit_file`, include a real required path when one is active, exact `old_text` copied from source evidence, complete replacement `new_text`, and a one-sentence `description`. Do not copy placeholders, ellipses, schema examples, or prose as edit fields."
}

func (r *runExecution) readOnlyEvidenceCompletionRecoveryPrompt(results []workspace.ToolResult) string {
	if r == nil {
		return ""
	}

	brief := workerruntime.NormalizeRuntimeBrief(r.state.runtimeBrief)
	if !workerruntime.PermissionProfileIsReadOnlyWorkspace(brief.PermissionProfile) {
		return ""
	}

	// Source-grounding rejection: evidence was gathered but the answer cited
	// paths without body evidence. Guide the model to read those paths or
	// qualify the answer rather than searching for new targets. Check this
	// before the Readiness gate because even a nil/ready Readiness can
	// produce a source-grounding-unmet rejection on the answer text.
	if r.state.lastCompletionGateSourceGrounding.hasTypedSignal() {
		if prompt := r.state.lastCompletionGateSourceGrounding.RecoveryPrompt(readOnlyEvidenceLedgerGuidanceForBrief(brief, results)); prompt != "" {
			return prompt
		}
	}

	if brief.Readiness == nil || brief.Readiness.ReadyToAnswer {
		return ""
	}

	if len(genericReadOnlyEvidenceTools(brief.AllowedTools)) == 0 || genericReadOnlyAnswerEvidenceReady(brief, results) {
		return ""
	}

	targets := pathparse.MentionedWorkspacePaths(brief.Objective)
	if len(targets) == 0 {
		targets = append(targets, brief.TargetRefs...)
	}

	readPaths := newToolResultEvidenceProjection(results).ReadPaths()
	target := ""

	for _, rawTarget := range targets {
		candidate := NormalizeRunnerPath(rawTarget)
		if candidate == "" {
			continue
		}

		if _, alreadyRead := readPaths[candidate]; alreadyRead {
			continue
		}

		target = candidate

		break
	}

	if target != "" {
		return fmt.Sprintf("The completion gate rejected a read-only final answer because no bounded workspace evidence has been gathered yet. Pending source evidence includes `%s`. Permission-authorized read/search/discovery tools remain callable for a fresh model-selected request; the final answer must use body-backed evidence or state the exact unavailable source.", target)
	}

	if len(readPaths) > 0 {
		return "The completion gate rejected a read-only final answer because the gathered evidence does not yet include the codebase source needed to answer the explicit question. Permission-authorized read/search/discovery tools remain callable for a fresh model-selected request; the final answer must stay within the body-backed evidence or state the exact unavailable source."
	}

	return "The completion gate rejected a read-only final answer because no bounded workspace evidence has been gathered yet. Permission-authorized read/search/discovery tools remain callable for a fresh model-selected request; the final answer must use body-backed evidence or state the exact unavailable source."
}

func (r *runExecution) postWriteVerificationRequiredPrompt(results []workspace.ToolResult, plannerOwned bool) string {
	return postWriteVerificationPrompt(r.postWriteVerificationLedger(results), plannerOwned, "A workspace write completed and")
}

func postWriteVerificationPrompt(ledger postWriteVerificationLedger, plannerOwned bool, intro string) string {
	paths := ledger.PendingPaths
	target := "changed source files"

	if len(paths) > 0 {
		target = strings.Join(paths, ", ")
	}

	scope := "post-write verification"
	requirement := "Required evidence is one focused language-appropriate verification command/tool for changed source files, compiler-backed diagnostics, or `<<QUESTION>>:` with the concrete verification blocker. Generic source-sanity fallback and `git diff --check` alone do not certify source edits."

	if plannerOwned {
		scope = "post-write lane sanity"
		requirement = "Required evidence is compiler-backed source-sanity or language diagnostics for changed source files, or `<<QUESTION>>:` with the concrete source-sanity blocker. Generic source-sanity fallback and `git diff --check` alone do not certify source edits."
	}

	action := "Call one parser-readable verification tool now."
	if len(paths) == 1 {
		action = "Choose the relevant language/project verification command or diagnostics route for this changed path and issue one parser-readable verification action now."
	}

	return strings.Join([]string{
		strings.TrimSpace(intro) + " " + scope + " is still unresolved for: " + target + ".",
		"Final acceptance remains blocked until one parser-readable verification action succeeds or a typed `<<QUESTION>>:` blocker resolves the verification obligation. Permission-authorized reads and in-scope writes remain callable if they are still useful; any later commit refreshes the final changed set that must be verified.",
		requirement,
		action,
	}, "\n\n")
}

func (r *runExecution) implementationRequiredPathCompletionRecoveryPrompt(missing []string, results []workspace.ToolResult, writeOnly bool) string {
	missing = nonEmptyRunnerStrings(missing, 12)

	target := "the remaining required edit path(s)"

	if len(missing) > 0 {
		target = strings.Join(missing, ", ")
	}

	var b strings.Builder

	if writeOnly {
		b.WriteString("The completion gate rejected a final answer while exact-anchor required edit path(s) remain unchanged: ")
	} else {
		b.WriteString("The completion gate rejected a final answer while required edit path(s) remain unchanged: ")
	}

	b.WriteString(target)
	b.WriteString(".\n\n")
	b.WriteString("Your next visible message must be exactly one parser-readable action, not prose and not an implementation summary. Do not include analysis before or after the action.\n\n")
	b.WriteString("Hidden-only reasoning is not completion evidence and may not be available to the tool parser. The contract outcome is a visible write, bounded read/discovery action, native tool call, or `<<QUESTION>>` blocker.\n\n")

	if r.scopedImplementationPathsRequireExistingSourceEvidence(missing) {
		b.WriteString("If the current evidence supports the requested change, call one bounded edit capability now. Use `apply_patch` for one coherent multi-file patch across required source/test paths, or `edit_file` with `old_text`+`new_text` / `start_line`+`end_line`+`content` for one remaining path. Do not use `write_file` or `append_file` for existing source/test edits in this recovery; `write_file` is a full-body write for missing/new files or non-source deliverables, and `append_file` is only for true end-of-file appends.\n\n")
	} else {
		b.WriteString("If the current evidence supports the requested change, call one write capability now for a remaining required path: `edit_file`, `write_file`, `append_file`, or `apply_patch`. Use real current text, complete replacement text, or a valid patch; do not copy placeholders or ellipses.\n\n")
	}

	projection := newToolResultEvidenceProjection(results)
	unread := make([]string, 0, len(missing))
	for _, path := range missing {
		if !r.scopedImplementationPathsRequireExistingSourceEvidence([]string{path}) {
			continue
		}
		evidence := projection.SourceEvidenceForPath(path)
		if evidence.Level == planThenExecuteEvidenceAnchorReady {
			continue
		}

		unread = append(unread, path)
	}

	if len(unread) > 0 {
		b.WriteString("Source-body evidence remains pending for: ")
		b.WriteString(strings.Join(uniqueSortedRunnerPaths(unread), ", "))
		b.WriteString(". Permission-authorized read and search tools remain callable; choose a fresh request that supplies the missing edit-safety evidence without relying on a harness-selected target.\n")
	} else {
		b.WriteString("Every remaining existing source path already has source-body anchor evidence; authorized new-file paths do not require an impossible preimage read. A write capability or exact `<<QUESTION>>:` blocker can now satisfy the mutation contract; permission-authorized inspection remains callable for a fresh model-selected request.\n")

		if r.scopedImplementationPathsRequireExistingSourceEvidence(missing) {
			b.WriteString("\n" + textualMutationEditFileJSONInstruction() + "\n")
		}
	}

	b.WriteString("\nThe only non-tool closeout is `<<QUESTION>>:` with one concrete blocker reason for every remaining required path.")

	return b.String()
}

func (r *runExecution) scopedImplementationPathsRequireExistingSourceEvidence(paths []string) bool {
	if len(paths) == 0 || r == nil || r.runner.Workspace == nil {
		return false
	}

	authority := r.planThenExecutePathAuthority()
	for _, path := range paths {
		relPath := cleanRelativeRunnerPath(path)
		if relPath == "" || writePathIsNonSource(relPath) {
			return false
		}
		if authority.authorizedNewFile(relPath) {
			return false
		}

		assessment := r.runner.Workspace.AssessMutationAdmission(r.state.ctx, workspace.ToolRequest{
			Tool: workspace.ToolNameWriteFile, Path: relPath, Description: "assess required source preimage",
		})
		if assessment.Disposition != workspace.MutationAdmissionAdmittedWithoutApproval ||
			len(assessment.Analysis.Targets) != 1 ||
			assessment.Analysis.Targets[0].Preimage != workspace.MutationPreimageRequired {

			return false
		}
	}

	return true
}

func postWriteVerificationFailedRecoveryPrompt(result workspace.ToolResult, plannerOwned bool) string {
	summary := strings.TrimSpace(result.Summary)
	detail := strings.TrimSpace(result.Error)

	if detail == "" {
		detail = strings.TrimSpace(result.Output)
	}

	if detail != "" {
		detail = textutil.Truncate(strings.TrimSpace(detail), 320)
	}

	var b strings.Builder

	if plannerOwned {
		b.WriteString("Post-write lane sanity failed after the required edits. Treat the failed sanity output as repair evidence, not as successful completion.\n\n")
	} else {
		b.WriteString("Post-write verification failed after the required edits. Treat the failed verification output as repair evidence, not as successful completion.\n\n")
	}

	if summary != "" {
		b.WriteString("Failed check: ")
		b.WriteString(summary)
		b.WriteString("\n")
	}

	if detail != "" {
		b.WriteString("Failure detail: ")
		b.WriteString(detail)
		b.WriteString("\n")
	}

	b.WriteString("\nNext action must be one parser-readable action. If the failure names a changed or required path, line, symbol, or type error, repair the affected in-scope file with a write tool. If exactly one edit-safety fact is missing, use one narrow read/language/context lookup for that fact, then repair. Rerun verification only after a repair write or when the failure is clearly environmental. Answer `<<QUESTION>>:` only for an external, permission, or environment blocker that prevents repair.")

	return b.String()
}

func (r *runExecution) prunePlanThenExecuteTerminalSurfaceHistory(messages *[]llama.ChatMessage, reason string) {
	if r == nil || !r.planThenExecutePlannerPhase() || messages == nil {
		return
	}

	pruned, droppedActions, droppedScaffolding := prunePlanThenExecuteTerminalTextualMessages(*messages)
	if droppedActions == 0 && droppedScaffolding == 0 {
		return
	}

	*messages = pruned

	r.emitEvent(marshalRunEvent("plan_then_execute_terminal_surface_history_pruned", map[string]any{
		"reason":                      strings.TrimSpace(reason),
		"dropped_tool_requests":       droppedActions,
		"dropped_surface_scaffolding": droppedScaffolding,
	}))
}

func (r *runExecution) refreshPlanThenExecuteTerminalToolSurface(messages *[]llama.ChatMessage, results []workspace.ToolResult, reason string) {
	if r == nil || !r.planThenExecutePlannerPhase() || messages == nil {
		return
	}

	surface := derivePlanExecuteRecoverySurface(r.state.runtimeBrief, results, PlanExecutePlanningTransport{TextualToolParser: true}, r.planThenExecutePathAuthority())
	if surface.Kind == PlanExecuteRecoverySurfaceAnchorRefresh {
		r.refreshPlanThenExecuteAnchorRefreshSurface(messages, results, reason)

		return
	}

	if surface.Kind == PlanExecuteRecoverySurfaceSourceSatisfiedTerminal {
		r.refreshPlanThenExecuteTerminalOutcomeSurface(messages, results, reason)

		return
	}

	if surface.Kind != PlanExecuteRecoverySurfaceSubmitReady && surface.Kind != PlanExecuteRecoverySurfaceSubmitCorrection {
		r.prunePlanThenExecuteTerminalSurfaceHistory(messages, reason)

		return
	}

	readTools := r.permissionAuthorizedInspection(r.workspaceAdvertisedAllowedTools()).ToolNames()
	submitPlanTools := textutil.Unique(append(readTools, workspace.ToolSurfacePhaseToolNames(workspace.ToolSurfacePhasePlanExecuteSubmitPlanOnly)...))
	terminalBrief := r.planThenExecuteTerminalToolBrief(results)
	terminalState := workerruntime.RunState{
		Lane:              textutil.FirstNonEmpty(strings.TrimSpace(terminalBrief.Lane), r.agentName()),
		TaskFamily:        terminalBrief.TaskFamily,
		Contract:          terminalBrief.Contract,
		Phase:             "plan_submit_ready",
		Objective:         terminalBrief.Objective,
		Permission:        terminalBrief.PermissionProfile,
		TargetRefs:        append([]string(nil), terminalBrief.TargetRefs...),
		RequiredEditPaths: append([]string(nil), terminalBrief.RequiredEditPaths...),
		EvidencePaths:     newToolResultEvidenceProjection(results).evidencePathsForBrief(terminalBrief, 6),
		Constraints:       append([]string(nil), terminalBrief.Constraints...),
		AllowedTools:      submitPlanTools,
		ToolManifest:      workspace.BuildToolManifest(submitPlanTools),
		RequiredTools:     []string{workspace.ToolNameSubmitPlan},
		ReadinessGate:     append([]string(nil), terminalBrief.ReadinessGate...),
		SearchPolicy:      append([]string(nil), terminalBrief.SearchPolicy...),
		StopConditions:    append([]string(nil), terminalBrief.StopConditions...),
		FinalCritic:       append([]string(nil), terminalBrief.FinalCritic...),
		NextActions:       append([]string(nil), terminalBrief.RequiredNext...),
		InvalidActions:    append([]string(nil), terminalBrief.InvalidActions...),
		Readiness:         terminalBrief.Readiness,
	}
	transport := "textual"
	surfaceTitle := "# Terminal Plan-Execute Textual Tool Surface"
	nativeTerminal := r.supportsTerminalSubmitPlanNativeTools(results)
	pruned := *messages
	droppedActions := 0
	droppedScaffolding := 0
	compactDropped := 0
	compactEvidenceMessages := 0

	if nativeTerminal {
		pruned, droppedScaffolding = prunePlanThenExecuteTerminalNativeMessages(pruned)
	} else {
		pruned, droppedActions, droppedScaffolding = prunePlanThenExecuteTerminalTextualMessages(pruned)
		pruned, compactDropped, compactEvidenceMessages = compactPlanThenExecuteTerminalTextualMessages(pruned, terminalBrief, results)
	}

	actionInstruction := r.textualToolProtocolPrompt(terminalBrief)
	submitPlanSkeleton := planThenExecuteTerminalSubmitPlanSkeleton(terminalBrief, results, nativeTerminal, r.planThenExecutePathAuthority())
	renderedState := workerruntime.RenderRunState(terminalState)

	if nativeTerminal {
		transport = "native"
		surfaceTitle = "# Terminal Plan-Execute Native Tool Surface"
		terminalState.ReadinessGate = []string{"native_submit_plan_tool_call_required"}
		terminalState.StopConditions = []string{"submit_plan_returned_ok", "submit_plan_rejected_with_blocker"}
		terminalState.FinalCritic = []string{"submit_plan_called", "edits_or_source_satisfied_paths_cover_required_paths"}
		terminalState.NextActions = []string{"call native submit_plan using edits[] for changed paths and source_satisfied_paths for already-final paths; permission-authorized reads remain callable for a fresh model-selected request"}
		renderedState = workerruntime.RenderStepInstruction(workerruntime.StepInstruction{
			CurrentState: terminalState,
			Directive:    "Use this current-state packet for the next action. Call native submit_plan when the gathered evidence is sufficient. " + planThenExecuteSubmitPlanCoverageSentence() + " Permission-authorized read/search tools remain callable for a fresh model-selected request; verification and per-edit writes remain unavailable in the planner phase.",
		})
		actionInstruction = "Use native `submit_plan` when the gathered evidence is sufficient. Permission-authorized read/search tools remain callable for a fresh model-selected request."
	}

	terminalPrompt := strings.Join([]string{
		surfaceTitle,
		"",
		"This refreshes the planner contract. Permission-authorized read/search tools remain callable; verification and per-edit writes remain unavailable in the planner phase.",
		"",
		renderedState,
		"",
		submitPlanSkeleton,
		"",
		actionInstruction,
	}, "\n")

	*messages = append(pruned, llama.ChatMessage{Role: "system", Content: strings.TrimSpace(terminalPrompt)})
	r.emitEvent(marshalRunEvent("plan_then_execute_textual_tool_surface_refreshed", map[string]any{
		"reason":                        strings.TrimSpace(reason),
		"transport":                     transport,
		"dropped_tool_request_messages": droppedActions,
		"dropped_scaffolding_messages":  droppedScaffolding,
		"dropped_compact_messages":      compactDropped,
		"compact_evidence_messages":     compactEvidenceMessages,
		"allowed_tools":                 submitPlanTools,
	}))
}

func (r *runExecution) refreshPlanThenExecuteTerminalOutcomeSurface(messages *[]llama.ChatMessage, results []workspace.ToolResult, reason string) {
	if r == nil || !r.planThenExecutePlannerPhase() || messages == nil {
		return
	}

	terminalBrief := r.planThenExecuteTerminalToolBrief(results)
	pruned, droppedActions, droppedScaffolding := prunePlanThenExecuteTerminalTextualMessages(*messages)
	pruned, compactDropped, compactEvidenceMessages := compactPlanThenExecuteTerminalTextualMessages(pruned, terminalBrief, results)
	allowedTools := textutil.Unique(append(
		r.permissionAuthorizedInspection(r.workspaceAdvertisedAllowedTools()).ToolNames(),
		workspace.ToolNameSubmitPlan,
	))
	terminalState := workerruntime.RunState{
		Lane:              textutil.FirstNonEmpty(strings.TrimSpace(terminalBrief.Lane), r.agentName()),
		TaskFamily:        terminalBrief.TaskFamily,
		Contract:          terminalBrief.Contract,
		Phase:             "plan_terminal_outcome",
		Objective:         terminalBrief.Objective,
		Permission:        terminalBrief.PermissionProfile,
		TargetRefs:        append([]string(nil), terminalBrief.TargetRefs...),
		RequiredEditPaths: append([]string(nil), terminalBrief.RequiredEditPaths...),
		EvidencePaths:     newToolResultEvidenceProjection(results).evidencePathsForBrief(terminalBrief, 6),
		Constraints:       append([]string(nil), terminalBrief.Constraints...),
		AllowedTools:      allowedTools,
		ToolManifest:      workspace.BuildToolManifest(allowedTools),
		RequiredTools:     nil,
		ReadinessGate:     []string{"terminal_outcome_required"},
		SearchPolicy:      []string{"permission-authorized read/search tools remain callable for a fresh model-selected request"},
		StopConditions:    []string{"decision_source_satisfied", "textual_submit_plan_returned_ok", "question_blocker_reported"},
		FinalCritic:       []string{"one_terminal_outcome_only", "per_path_current_state_evidence_for_source_satisfied"},
		NextActions: []string{
			"answer `Decision: source-satisfied` with per-path current-state evidence if every required path already contains the requested final state",
			"or emit one visible textual submit_plan JSON object with real executable edits if changes are still needed",
			"or answer `<<QUESTION>>:` with the exact path-specific blocker",
		},
		InvalidActions: []string{
			"empty or source-satisfied submit_plan transport",
			"verification/build/test in planner phase",
			"per-edit write in planner phase",
			"prose-only answer without Decision/source evidence or blocker",
		},
		Readiness: terminalBrief.Readiness,
	}
	terminalPrompt := strings.Join([]string{
		"# Terminal Plan-Execute Outcome Surface",
		"",
		"This replaces earlier runtime brief and tool protocol examples for this planner turn. Earlier `submit_plan`-only recovery text is stale because the previous `submit_plan` attempted a source-satisfied closeout through the wrong transport.",
		"",
		workerruntime.RenderRunState(terminalState),
		"",
		"Next visible action must be exactly one terminal outcome:",
		"1. `Decision: source-satisfied` with per-path current-state evidence if every required path already contains the requested final state.",
		"2. One visible textual `submit_plan` JSON object with real executable edits if changes are still needed. " + planThenExecuteSubmitPlanVisibleJSONInstruction(),
		"3. `<<QUESTION>>:` with the exact path-specific blocker.",
		"",
		"Do not repeat an empty or source-satisfied `submit_plan` object. Permission-authorized read/search tools remain callable; shell verification and per-edit write tools do not.",
	}, "\n")

	*messages = append(pruned, llama.ChatMessage{Role: "system", Content: strings.TrimSpace(terminalPrompt)})
	r.emitEvent(marshalRunEvent("plan_then_execute_textual_tool_surface_refreshed", map[string]any{
		"reason":                        strings.TrimSpace(reason),
		"transport":                     "terminal_outcome",
		"dropped_tool_request_messages": droppedActions,
		"dropped_scaffolding_messages":  droppedScaffolding,
		"dropped_compact_messages":      compactDropped,
		"compact_evidence_messages":     compactEvidenceMessages,
		"allowed_tools":                 allowedTools,
	}))
}

func (r *runExecution) refreshPlanThenExecuteTerminalTextualFallbackSurface(messages *[]llama.ChatMessage, results []workspace.ToolResult, reason string) {
	if r == nil || !r.planThenExecutePlannerPhase() || messages == nil || !planThenExecuteTerminalSubmitPlanAllowed(r.state.runtimeBrief, results, r.planThenExecutePathAuthority()) {
		return
	}

	readTools := r.permissionAuthorizedInspection(r.workspaceAdvertisedAllowedTools()).ToolNames()
	submitPlanTools := textutil.Unique(append(readTools, workspace.ToolSurfacePhaseToolNames(workspace.ToolSurfacePhasePlanExecuteSubmitPlanOnly)...))
	terminalBrief := r.planThenExecuteTerminalToolBrief(results)
	pruned, droppedActions, droppedScaffolding := prunePlanThenExecuteTerminalTextualMessages(*messages)
	pruned, compactDropped, compactEvidenceMessages := compactPlanThenExecuteTerminalTextualMessages(pruned, terminalBrief, results)
	terminalState := workerruntime.RunState{
		Lane:              textutil.FirstNonEmpty(strings.TrimSpace(terminalBrief.Lane), r.agentName()),
		TaskFamily:        terminalBrief.TaskFamily,
		Contract:          terminalBrief.Contract,
		Phase:             "plan_submit_ready_textual_fallback",
		Objective:         terminalBrief.Objective,
		Permission:        terminalBrief.PermissionProfile,
		TargetRefs:        append([]string(nil), terminalBrief.TargetRefs...),
		RequiredEditPaths: append([]string(nil), terminalBrief.RequiredEditPaths...),
		EvidencePaths:     newToolResultEvidenceProjection(results).evidencePathsForBrief(terminalBrief, 6),
		Constraints:       append([]string(nil), terminalBrief.Constraints...),
		AllowedTools:      submitPlanTools,
		ToolManifest:      workspace.BuildToolManifest(submitPlanTools),
		RequiredTools:     []string{workspace.ToolNameSubmitPlan},
		ReadinessGate:     []string{"visible_textual_submit_plan_json_required"},
		SearchPolicy:      []string{"permission-authorized read/search tools remain callable for a fresh model-selected request"},
		StopConditions:    []string{"submit_plan_returned_ok", "submit_plan_rejected_with_blocker"},
		FinalCritic:       []string{"submit_plan_called", "edits_or_source_satisfied_paths_cover_required_paths"},
		NextActions:       []string{"emit exactly one visible submit_plan JSON object using edits[] for changed paths and source_satisfied_paths for already-final paths"},
		InvalidActions:    append([]string(nil), terminalBrief.InvalidActions...),
		Readiness:         terminalBrief.Readiness,
	}
	terminalPrompt := strings.Join([]string{
		"# Terminal Plan-Execute Textual Tool Surface",
		"",
		"Native tool-call transport failed to produce a required `submit_plan` call. Native tools are disabled for this retry. A visible textual `submit_plan` JSON object remains available, and permission-authorized read/search tools remain callable for a fresh model-selected request.",
		"",
		workerruntime.RenderRunState(terminalState),
		"",
		planThenExecuteTerminalSubmitPlanSkeleton(terminalBrief, results, false, r.planThenExecutePathAuthority()),
		"",
		r.textualToolProtocolPrompt(terminalBrief),
	}, "\n")

	*messages = append(pruned, llama.ChatMessage{Role: "system", Content: strings.TrimSpace(terminalPrompt)})
	r.emitEvent(marshalRunEvent("plan_then_execute_textual_tool_surface_refreshed", map[string]any{
		"reason":                        strings.TrimSpace(reason),
		"transport":                     "textual_fallback",
		"dropped_tool_request_messages": droppedActions,
		"dropped_scaffolding_messages":  droppedScaffolding,
		"dropped_compact_messages":      compactDropped,
		"compact_evidence_messages":     compactEvidenceMessages,
		"allowed_tools":                 submitPlanTools,
	}))
}

func (r *runExecution) refreshPlanThenExecuteAnchorRefreshSurface(messages *[]llama.ChatMessage, results []workspace.ToolResult, reason string) {
	if r == nil || !r.planThenExecutePlannerPhase() || messages == nil {
		return
	}

	stateProjection := r.derivePlanExecutePlanningState(results, PlanExecutePlanningTransport{TextualToolParser: true})
	if stateProjection.Mode != PlanExecutePlanningModeAnchorRefresh || len(stateProjection.RefreshPaths) == 0 {
		return
	}

	refreshPaths := stateProjection.RefreshPaths
	pruned, droppedActions, droppedScaffolding := prunePlanThenExecuteTerminalTextualMessages(*messages)
	brief := runnerBriefWithPhaseHints(r.state.runtimeBrief, results, r.planThenExecutePathAuthority(), r.implementationCompletionState(results))
	anchorRefreshRequiredTools := workspace.ToolSurfacePhaseToolNames(workspace.ToolSurfacePhasePlanExecuteAnchorRefresh)
	anchorRefreshTools := textutil.Merge(
		anchorRefreshRequiredTools,
		r.permissionAuthorizedInspection(r.workspaceAdvertisedAllowedTools()).ToolNames(),
	)
	state := workerruntime.RunState{
		Lane:              textutil.FirstNonEmpty(strings.TrimSpace(brief.Lane), r.agentName()),
		TaskFamily:        brief.TaskFamily,
		Contract:          brief.Contract,
		Phase:             "plan_submit_anchor_refresh_required",
		Objective:         brief.Objective,
		Permission:        brief.PermissionProfile,
		TargetRefs:        append([]string(nil), brief.TargetRefs...),
		RequiredEditPaths: append([]string(nil), brief.RequiredEditPaths...),
		EvidencePaths:     newToolResultEvidenceProjection(results).evidencePathsForBrief(brief, 6),
		Constraints:       append([]string(nil), brief.Constraints...),
		AllowedTools:      anchorRefreshTools,
		ToolManifest:      workspace.BuildToolManifest(anchorRefreshTools),
		RequiredTools:     anchorRefreshRequiredTools,
		ReadinessGate:     []string{"bounded_anchor_refresh_before_corrected_submit_plan"},
		SearchPolicy:      append([]string(nil), brief.SearchPolicy...),
		StopConditions:    []string{"bounded_source_refresh_returned"},
		FinalCritic:       []string{"refresh_targets_rejected_anchor_path"},
		NextActions:       append([]string(nil), brief.RequiredNext...),
		InvalidActions:    append([]string(nil), brief.InvalidActions...),
		Readiness:         brief.Readiness,
	}
	targetText := "the stale or missing required edit path named by the submit_plan rejection"

	if len(refreshPaths) > 0 {
		targetText = strings.Join(refreshPaths, ", ")
	}

	prompt := strings.Join([]string{
		"# Plan-Execute Anchor Refresh Surface",
		"",
		"A previous `submit_plan` was rejected because one or more edit anchors were stale or missing. A current source window for those anchors is required before another plan can be accepted.",
		"",
		workerruntime.RenderRunState(state),
		"",
		"Known refresh target(s): " + targetText + ". Permission-authorized inspection requests remain callable.",
		planThenExecuteAnchorRefreshReadFallbackInstruction(refreshPaths),
		"Use any permission-authorized inspection route that returns a current producer source window covering the anchor you intend to edit.",
		"A fresh `submit_plan` will remain blocked until that current anchor evidence exists.",
		"After the refresh, use anchors copied from the refreshed source. Prefer `insert_after_line` plus `content` for insertions/appends; use `old_text` only when copied exactly from fresh source.",
	}, "\n")

	*messages = append(pruned, llama.ChatMessage{Role: "system", Content: strings.TrimSpace(prompt)})
	r.emitEvent(marshalRunEvent("plan_then_execute_anchor_refresh_surface_refreshed", map[string]any{
		"reason":                        strings.TrimSpace(reason),
		"transport":                     "textual",
		"dropped_tool_request_messages": droppedActions,
		"dropped_scaffolding_messages":  droppedScaffolding,
		"allowed_tools":                 anchorRefreshTools,
		"refresh_paths":                 refreshPaths,
	}))
}

func (r *runExecution) planThenExecuteTerminalToolBrief(results []workspace.ToolResult) workerruntime.WorkerRuntimeBrief {
	brief := runnerBriefWithPhaseHints(r.state.runtimeBrief, results, r.planThenExecutePathAuthority(), r.implementationCompletionState(results))
	if strings.TrimSpace(brief.Lane) == "" {
		brief.Lane = r.agentName()
	}

	inspectionTools := r.permissionAuthorizedInspection(r.workspaceAdvertisedAllowedTools()).ToolNames()
	submitPlanTools := textutil.Unique(append(inspectionTools, workspace.ToolNameSubmitPlan))
	brief.AllowedTools = submitPlanTools
	brief.RequiredTools = []string{workspace.ToolNameSubmitPlan}
	brief.ToolManifest = workspace.BuildToolManifest(submitPlanTools)
	brief.Constraints = textutil.Merge(brief.Constraints, []string{"plan_then_execute_submit_plan_ready", "required_edit_paths_define_write_scope", "planner_owned_verification"})
	brief.SearchPolicy = []string{"permission-authorized read/search tools remain callable for a fresh model-selected request after submit_plan readiness"}

	if planThenExecuteSubmitCorrectionActive(r.state.runtimeBrief, results, r.planThenExecutePathAuthority()) {
		brief.Constraints = textutil.Merge(brief.Constraints, []string{"plan_then_execute_submit_correction_only"})
		brief.RequiredNext = []string{"corrected submit_plan using edits[] for changed paths and source_satisfied_paths for already-final paths"}
		brief.InvalidActions = []string{"Decision: source-satisfied after an incomplete submit_plan", "<<QUESTION>> after source evidence is sufficient for correction", "verify in planner phase", "per-edit write in planner phase", "prose-only final answer"}
		brief.ReadinessGate = []string{"submit_plan_correction_required"}
		brief.StopConditions = []string{"submit_plan_returned_ok"}
		brief.FinalCritic = []string{"answers_actual_user_request", "submit_plan_called", "edits_or_source_satisfied_paths_cover_required_paths"}
		if planThenExecuteSubmitCorrectionBlockerAllowed(r.state.runtimeBrief, results) {
			brief.RequiredNext = []string{"corrected submit_plan using edits[] for changed paths, or <<QUESTION>> with the exact path-specific missing body/anchor blocker after repeated rejected submit_plan attempts"}
			brief.InvalidActions = []string{"Decision: source-satisfied after an incomplete submit_plan", "generic <<QUESTION>> without exact path-specific missing body/anchor blocker", "verify in planner phase", "per-edit write in planner phase", "prose-only final answer"}
			brief.ReadinessGate = []string{"submit_plan_correction_or_exact_body_anchor_blocker_required"}
			brief.StopConditions = []string{"submit_plan_returned_ok", "exact_missing_body_anchor_blocker_returned"}
			brief.FinalCritic = []string{"answers_actual_user_request", "submit_plan_called_or_exact_body_anchor_blocker", "edits_or_source_satisfied_paths_cover_required_paths_unless_exact_blocker"}
		}
	} else {
		brief.ReadinessGate = textutil.Merge([]string{string(workerruntime.ReadinessSubmitPlanOrSourceSatisfiedOrBlocker)}, brief.ReadinessGate)
		brief.StopConditions = textutil.Merge([]string{string(workerruntime.StopSubmitPlanReturnedOrSourceSatisfiedOrBlocker), string(workerruntime.StopBlockedByMissingPermissionOrInformation)}, brief.StopConditions)
		brief.FinalCritic = []string{"answers_actual_user_request", "submit_plan_called", "source_satisfied_or_blocker_only_with_per_path_evidence"}
	}

	completionState := r.implementationCompletionState(results)
	brief.Readiness = runnerReadinessState(r.agentName(), brief, results, &completionState, r.planThenExecutePathAuthority())

	return workerruntime.NormalizeRuntimeBrief(brief)
}

const planThenExecuteCurrentSubmitPlanSkeletonPrefix = "# Current Submit Plan Skeleton"

func planThenExecuteTerminalSubmitPlanSkeleton(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, native bool, authority planThenExecutePathAuthority) string {
	entries := planThenExecuteTerminalSubmitPlanSkeletonEntries(brief, results, authority)
	if len(entries) == 0 {
		return "No concrete selected edit path is available for a safe `submit_plan` skeleton. Use `Decision: source-satisfied` with current-state evidence, answer `<<QUESTION>>:` with the exact blocker, or perform one bounded read of a concrete candidate path before submitting a plan."
	}

	if native {
		return planThenExecuteNativeSubmitPlanSkeleton(entries)
	}

	return planThenExecuteTextualSubmitPlanSkeleton(entries)
}

func planThenExecuteTerminalSubmitPlanSkeletonNoMoreTools(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, native bool, authority planThenExecutePathAuthority) string {
	entries := planThenExecuteTerminalSubmitPlanSkeletonEntries(brief, results, authority)
	if len(entries) == 0 {
		return "No concrete selected edit path is available for a safe `submit_plan` skeleton. Because the loop budget is exhausted, do not call more tools. Use `Decision: source-satisfied` with current-state evidence, or answer `<<QUESTION>>:` with the exact blocker."
	}

	if native {
		return planThenExecuteNativeSubmitPlanSkeleton(entries)
	}

	return planThenExecuteTextualSubmitPlanSkeleton(entries)
}

type planThenExecuteSubmitPlanSkeletonEntry struct {
	Path            string
	Evidence        []string
	EvidenceOmitted bool
	StartLine       int
	EndLine         int
	NewFile         bool
}

const planThenExecuteSubmitPlanSkeletonMaxEvidenceLines = 16

func planThenExecuteTerminalSubmitPlanSkeletonEntries(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, authority planThenExecutePathAuthority) []planThenExecuteSubmitPlanSkeletonEntry {
	state := derivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{}, authority)
	paths := planThenExecuteSubmitPlanSkeletonPaths(state)
	evidenceByPath := planThenExecuteTerminalSubmitPlanEvidenceByPath(results, 2)
	entries := make([]planThenExecuteSubmitPlanSkeletonEntry, 0, len(paths))
	evidenceLinesRemaining := planThenExecuteSubmitPlanSkeletonMaxEvidenceLines

	for _, path := range paths {
		entry := planThenExecuteSubmitPlanSkeletonEntry{
			Path:      path,
			StartLine: 1,
			EndLine:   1,
			NewFile:   authority.authorizedNewFile(path),
		}
		for _, evidence := range evidenceByPath[NormalizeRunnerPath(path)] {
			if evidenceLinesRemaining > 0 {
				entry.Evidence = append(entry.Evidence, evidence.Summary)
				evidenceLinesRemaining--
			} else {
				entry.EvidenceOmitted = true
			}

			if entry.StartLine == 1 && entry.EndLine == 1 && evidence.StartLine > 0 && evidence.EndLine >= evidence.StartLine {
				entry.StartLine = evidence.StartLine
				entry.EndLine = evidence.EndLine
			}
		}

		entries = append(entries, entry)
	}

	return entries
}

type planThenExecuteTerminalSubmitPlanEvidence struct {
	Summary   string
	StartLine int
	EndLine   int
}

func planThenExecuteTerminalSubmitPlanEvidenceByPath(results []workspace.ToolResult, maxPerPath int) map[string][]planThenExecuteTerminalSubmitPlanEvidence {
	if maxPerPath <= 0 {
		maxPerPath = 2
	}

	out := map[string][]planThenExecuteTerminalSubmitPlanEvidence{}
	seen := map[string]map[string]struct{}{}
	add := func(path string, summary string, start int, end int) {
		path = NormalizeRunnerPath(path)
		summary = strings.TrimSpace(summary)

		if path == "" || summary == "" || len(out[path]) >= maxPerPath {
			return
		}

		if seen[path] == nil {
			seen[path] = map[string]struct{}{}
		}

		if _, ok := seen[path][summary]; ok {
			return
		}

		seen[path][summary] = struct{}{}

		out[path] = append(out[path], planThenExecuteTerminalSubmitPlanEvidence{
			Summary:   summary,
			StartLine: start,
			EndLine:   end,
		})
	}

	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" {
			continue
		}

		sourceWindows := result.SourceWindows()
		if len(sourceWindows) == 0 {
			for _, path := range newToolResultEvidenceRecord(result).ReadPaths() {
				add(path, result.Summary, 0, 0)
			}
			continue
		}

		for _, window := range sourceWindows {
			fact := window.Evidence
			add(fact.CanonicalID, result.Summary, fact.StartLine, fact.EndLine)
		}
	}

	return out
}

func planThenExecuteNativeSubmitPlanSkeleton(entries []planThenExecuteSubmitPlanSkeletonEntry) string {
	var b strings.Builder

	b.WriteString(planThenExecuteCurrentSubmitPlanSkeletonPrefix)
	b.WriteString("\n\nNative `submit_plan` tool-argument shape. Fill these arguments in the tool call; do not answer with this template as prose.\n\n")
	b.WriteString("rationale: write one concrete sentence explaining why the edit set satisfies the request.\n")
	b.WriteString(planThenExecuteSubmitPlanCoverageSentence())
	b.WriteString("\n")
	b.WriteString("edits:\n")

	for _, entry := range entries {
		b.WriteString("- path: ")
		b.WriteString(strconv.Quote(entry.Path))
		b.WriteString("\n")

		for _, evidence := range entry.Evidence {
			b.WriteString("  evidence: ")
			b.WriteString(evidence)
			b.WriteString("\n")
		}

		b.WriteString("  purpose: write one concrete sentence for this path.\n")
		b.WriteString("  choose exactly one edit shape:\n")

		if entry.NewFile {
			b.WriteString("    new_file: replace_all=true + old_text=\"\" + new_text\n")
		} else {
			b.WriteString(fmt.Sprintf("    line_range_default: start_line=%d + end_line=%d + content (replacement only; safest for source/test edits and large additions)\n", entry.StartLine, entry.EndLine))
			b.WriteString("    substring_small_exact: old_text + new_text\n")
			b.WriteString("    insertion_small_only: insert_after_line + small inline content\n")
		}
	}

	b.WriteString("\nEdit shape semantics:\n")
	b.WriteString("- The shown source ranges are evidence handles, not proposed edits; adjust to the exact replacement range when needed.\n")
	b.WriteString("- Line-range `content` replaces the inclusive current-source range from `start_line` through `end_line`; include the complete final replacement for that range.\n")
	b.WriteString("- `insert_after_line` inserts small inline non-empty `content` after an existing 1-based line in an existing file; it does not replace current lines, must not be combined with other edit shapes, and must not be used for large generated blocks.\n")
	b.WriteString("- For large additions inside existing files, use a line-range replacement covering a small current anchor range and include the unchanged anchor line(s) plus inserted content.\n")
	b.WriteString("- Substring `old_text` must be copied exactly from current source and match once; `new_text` is the complete final replacement.\n")
	b.WriteString("- Missing/new-file `new_text` is the complete final file body.\n")
	b.WriteString("- For missing/new files, use only `replace_all:true` with empty `old_text` and a complete `new_text` file body.\n")

	if planThenExecuteSubmitPlanSkeletonEvidenceOmitted(entries) {
		b.WriteString("- Additional evidence handles were omitted after the template evidence cap; use the current-source evidence already present in this turn.\n")
	}

	b.WriteString("- Do not copy placeholder values into edit bodies; fill `old_text`, `new_text`, or `content` only from source evidence and the final intended code.")

	return b.String()
}

func planThenExecuteTextualSubmitPlanSkeleton(entries []planThenExecuteSubmitPlanSkeletonEntry) string {
	var b strings.Builder

	b.WriteString(planThenExecuteCurrentSubmitPlanSkeletonPrefix)
	b.WriteString("\n\nParser-readable submit_plan contract. Emit exactly one visible JSON object with top-level `tool:\"submit_plan\"`, `rationale`, and an `edits` array. The JSON must contain real values only; do not copy placeholders, ellipses, or schema examples. ")
	b.WriteString(planThenExecuteSubmitPlanCoverageSentence())
	b.WriteString("\n\n")

	for _, entry := range entries {
		b.WriteString("- required edit path: ")
		b.WriteString(entry.Path)
		b.WriteString("\n")

		for _, evidence := range entry.Evidence {
			b.WriteString("  evidence: ")
			b.WriteString(evidence)
			b.WriteString("\n")
		}

		if entry.NewFile {
			b.WriteString("  expected shape: replace_all true, empty old_text, complete final new_text file body, and a concrete purpose.\n")
		} else {
			startLine := entry.StartLine
			endLine := entry.EndLine

			if startLine <= 0 {
				startLine = 1
			}

			if endLine < startLine {
				endLine = startLine
			}

			b.WriteString("  default source range evidence: start_line ")
			b.WriteString(strconv.Itoa(startLine))
			b.WriteString(", end_line ")
			b.WriteString(strconv.Itoa(endLine))
			b.WriteString(". Use that range only if it is the exact replacement range; otherwise adjust from source evidence.\n")
			b.WriteString("  allowed shapes: start_line/end_line/content, old_text/new_text, insert_after_line/content for small insertions, or replace_all/new_text only for missing/new-file writes.\n")
		}
	}

	if planThenExecuteSubmitPlanSkeletonEvidenceOmitted(entries) {
		b.WriteString("\nAdditional evidence handles were omitted after the evidence cap; use the current-source evidence already present in this turn.\n\n")
	}

	b.WriteString("Edit shape choices: prefer `start_line` + `end_line` + `content` for source/test edits, multi-line changes, and large additions anchored to existing lines; use `old_text` + `new_text` for small exact replacements/deletions; use `insert_after_line` + small inline `content` only for small insertions/appends after an existing line; use `replace_all:true` + empty `old_text` + full `new_text` only for missing/new files. Existing source/test files must use bounded anchored edits, not whole-file `replace_all`.\n")
	b.WriteString("The shown source ranges are evidence handles, not proposed edits; adjust to the exact replacement range when needed.\n")
	b.WriteString("`insert_after_line` targets an existing 1-based line in an existing file, inserts small inline non-empty `content`, and must not be combined with other edit shapes or used with large content.\n")
	b.WriteString("For missing/new files, use only `replace_all:true` with empty `old_text` and a complete `new_text` file body.\n")
	b.WriteString("Line-range `content` replaces every current source line from `start_line` through `end_line`, inclusive. It is not a patch hunk; include the complete final replacement for that range.\n")
	b.WriteString("Do not copy placeholder values into edit bodies; fill `old_text`, `new_text`, or `content` only from source evidence and the final intended code.")

	return b.String()
}

func planThenExecuteSubmitPlanSkeletonEvidenceOmitted(entries []planThenExecuteSubmitPlanSkeletonEntry) bool {
	for _, entry := range entries {
		if entry.EvidenceOmitted {
			return true
		}
	}

	return false
}

const planThenExecuteTerminalSourceEvidencePrefix = "# Terminal Source Evidence"

func compactPlanThenExecuteTerminalTextualMessages(messages []llama.ChatMessage, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) ([]llama.ChatMessage, int, int) {
	out := make([]llama.ChatMessage, 0, len(messages)+4)
	dropped := 0

	for _, message := range messages {
		role := strings.TrimSpace(message.Role)
		switch role {
		case "user":
			out = append(out, message)
		case "system":
			if planThenExecuteKeepTerminalCompactSystemMessage(message) {
				out = append(out, message)
			} else {
				dropped++
			}
		default:
			dropped++
		}
	}

	evidence := planThenExecuteTerminalSourceEvidenceMessages(brief, results)

	out = append(out, evidence...)

	return out, dropped, len(evidence)
}

func planThenExecuteKeepTerminalCompactSystemMessage(message llama.ChatMessage) bool {
	content := strings.TrimSpace(message.Content)
	if content == "" {
		return false
	}

	return strings.HasPrefix(content, "AUTHORITATIVE_CONSTRAINT:") ||
		strings.HasPrefix(content, planThenExecuteRejectedSubmitPlanDraftEvidencePrefix)
}

func planThenExecuteTerminalSourceEvidenceMessages(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) []llama.ChatMessage {
	required := map[string]struct{}{}

	for _, path := range normalizedRequiredEditPaths(brief.RequiredEditPaths) {
		if normalized := NormalizeRunnerPath(path); normalized != "" {
			required[normalized] = struct{}{}
		}
	}

	messages := make([]llama.ChatMessage, 0, len(results))
	seen := map[string]struct{}{}

	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" {
			continue
		}

		paths := newToolResultEvidenceRecord(result).ReadPaths()

		paths = nonArtifactEvidencePaths(paths)

		if len(paths) == 0 {
			continue
		}

		if len(required) > 0 && !planThenExecuteEvidenceIntersectsRequiredPaths(paths, required) {
			continue
		}

		summary := strings.TrimSpace(result.Summary)
		output := strings.TrimSpace(result.Output)

		if summary == "" && output == "" {
			continue
		}

		key := strings.TrimSpace(result.Tool) + "\x00" + summary + "\x00" + output
		if _, ok := seen[key]; ok {
			continue
		}

		seen[key] = struct{}{}

		var b strings.Builder

		b.WriteString(planThenExecuteTerminalSourceEvidencePrefix)
		b.WriteString("\n\n")

		if summary != "" {
			b.WriteString("summary: ")
			b.WriteString(summary)
			b.WriteString("\n")
		}

		if len(paths) > 0 {
			b.WriteString("paths: ")
			b.WriteString(strings.Join(normalizedRunnerPaths(paths), ", "))
			b.WriteString("\n")
		}

		if output != "" {
			b.WriteString("\n")
			b.WriteString(output)
		}

		messages = append(messages, llama.ChatMessage{Role: "system", Content: strings.TrimSpace(b.String())})
	}

	return messages
}

func planThenExecuteEvidenceIntersectsRequiredPaths(paths []string, required map[string]struct{}) bool {
	for _, path := range paths {
		if IsNonSourceEvidenceArtifactPath(path) {
			continue
		}

		if _, ok := required[NormalizeRunnerPath(path)]; ok {
			return true
		}
	}

	return false
}

func normalizedRunnerPaths(paths []string) []string {
	out := make([]string, 0, len(paths))
	seen := map[string]struct{}{}

	for _, path := range paths {
		normalized := NormalizeRunnerPath(path)
		if normalized == "" {
			continue
		}

		if IsNonSourceEvidenceArtifactPath(normalized) {
			continue
		}

		if _, ok := seen[normalized]; ok {
			continue
		}

		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}

	return out
}

func prunePlanThenExecuteTerminalTextualMessages(messages []llama.ChatMessage) ([]llama.ChatMessage, int, int) {
	if len(messages) == 0 {
		return messages, 0, 0
	}

	out := make([]llama.ChatMessage, 0, len(messages))
	droppedActions := 0
	droppedScaffolding := 0

	for _, message := range messages {
		if strings.TrimSpace(message.Role) == "tool" {
			// Keep tool results — they carry source-body evidence the
			// model needs for reasoning about edits. Without them, the
			// model sees "Read file X" in the summary but not the
			// actual content.
			out = append(out, message)
			continue
		}

		if planThenExecuteDropTextualToolRequestHistoryMessage(message) || strings.TrimSpace(message.Role) == "assistant" {
			droppedActions++
			continue
		}

		if planThenExecuteDropTerminalTextualScaffoldingMessage(message) {
			droppedScaffolding++
			continue
		}

		out = append(out, message)
	}

	if droppedActions == 0 && droppedScaffolding == 0 {
		return messages, 0, 0
	}

	return out, droppedActions, droppedScaffolding
}

// prunePlanThenExecuteTerminalNativeMessages removes stale runtime scaffolding
// without breaking native assistant/tool message pairs. Native transports require
// the assistant tool call and its tool result to remain adjacent and share the
// original call ID; deleting only one side causes adapters to discard the other.
func prunePlanThenExecuteTerminalNativeMessages(messages []llama.ChatMessage) ([]llama.ChatMessage, int) {
	if len(messages) == 0 {
		return messages, 0
	}

	out := make([]llama.ChatMessage, 0, len(messages))
	droppedScaffolding := 0

	for _, message := range messages {
		if planThenExecuteDropTerminalTextualScaffoldingMessage(message) {
			droppedScaffolding++
			continue
		}

		out = append(out, message)
	}

	if droppedScaffolding == 0 {
		return messages, 0
	}

	return out, droppedScaffolding
}

func planThenExecuteDropTerminalTextualScaffoldingMessage(message llama.ChatMessage) bool {
	if strings.TrimSpace(message.Role) != "system" {
		return false
	}

	content := strings.TrimSpace(message.Content)
	if content == "" {
		return false
	}

	return strings.HasPrefix(content, "# Textual Tool Call Protocol") ||
		strings.HasPrefix(content, "# Terminal Plan-Execute Textual Tool Surface") ||
		strings.HasPrefix(content, "# Terminal Plan-Execute Native Tool Surface") ||
		strings.HasPrefix(content, "# Terminal Plan-Execute Outcome Surface") ||
		strings.HasPrefix(content, planThenExecuteCurrentSubmitPlanSkeletonPrefix) ||
		strings.HasPrefix(content, "[frontier step instruction]") ||
		strings.HasPrefix(content, "[frontier runtime brief]") ||
		strings.HasPrefix(content, "REQUIRED SOURCE EVIDENCE PENDING:") ||
		strings.HasPrefix(content, "PLANNER READINESS REACHED:") ||
		strings.HasPrefix(content, "SUBMIT_PLAN PARSE RECOVERY:") ||
		strings.HasPrefix(content, "Plan-execute submit_plan is now the only allowed planner action.") ||
		strings.HasPrefix(content, "The completion gate rejected") ||
		strings.HasPrefix(content, "The previous `submit_plan` was rejected before it became a valid plan artifact.") ||
		strings.HasPrefix(content, "The previous native `submit_plan` tool call was rejected before it became a valid plan artifact.") ||
		strings.HasPrefix(content, "Native submit_plan tool-call transport failed before the model produced a tool call.") ||
		strings.HasPrefix(content, "The previous retry produced hidden-only output with no parser-readable action.") ||
		strings.HasPrefix(content, "Note: your `read_file` call was redirected") ||
		strings.HasPrefix(content, "Note: your `code_navigate` call was redirected")
}

func planThenExecuteDropTextualToolRequestHistoryMessage(message llama.ChatMessage) bool {
	if strings.TrimSpace(message.Role) != "assistant" || len(message.ToolCalls) > 0 {
		return false
	}

	content := strings.TrimSpace(message.Content)
	if content == "" || !strings.Contains(content, `"tool"`) {
		return false
	}

	req, _, ok, parseErr := workspace.ParseToolRequest(content)
	if ok {
		tool := strings.TrimSpace(req.Tool)
		return tool != workspace.ToolNameSubmitPlan
	}

	return parseErr != nil || strings.Contains(content, `"tool"`)
}

const planThenExecuteRejectedSubmitPlanDraftEvidencePrefix = "# Rejected Submit Plan Draft Evidence"

func replacePlanThenExecuteRejectedSubmitPlanDraftEvidence(messages *[]llama.ChatMessage, req workspace.ToolRequest, result workspace.ToolResult) {
	if messages == nil {
		return
	}

	prompt := planThenExecuteRejectedSubmitPlanDraftEvidence(req, result)
	if prompt == "" {
		return
	}

	out := make([]llama.ChatMessage, 0, len(*messages)+1)
	for _, message := range *messages {
		if strings.TrimSpace(message.Role) == "system" &&
			strings.HasPrefix(strings.TrimSpace(message.Content), planThenExecuteRejectedSubmitPlanDraftEvidencePrefix) {

			continue
		}

		out = append(out, message)
	}

	out = append(out, llama.ChatMessage{Role: "system", Content: prompt})
	*messages = out
}

func planThenExecuteRejectedSubmitPlanDraftEvidence(req workspace.ToolRequest, result workspace.ToolResult) string {
	edits := planThenExecuteEffectiveSubmitPlanEdits(req)
	if len(edits) == 0 {
		return ""
	}

	invalidPaths := submitPlanInvalidAnchorPaths(result)
	missingPaths := submitPlanMissingRequiredPaths(result)

	var b strings.Builder

	b.WriteString(planThenExecuteRejectedSubmitPlanDraftEvidencePrefix)
	b.WriteString("\n\nFactual evidence from the previous `submit_plan` request. The harness rejected that request; no plan artifact was created.\n")

	if brief := strings.TrimSpace(lastSubmitPlanCorrectionRejectionBrief(result)); brief != "" {
		b.WriteString("\n")
		b.WriteString(brief)
		b.WriteString("\n")
	}

	b.WriteString("\nDraft edit entries from the rejected request. Rejected body excerpts are not source evidence; correct them against the latest read_file/code_navigate source evidence before resubmitting.\n")

	for i, edit := range edits {
		path := NormalizeRunnerPath(edit.Path)
		if path == "" {
			path = strings.TrimSpace(edit.Path)
		}

		if path == "" {
			path = fmt.Sprintf("edits[%d]", i)
		}

		b.WriteString(fmt.Sprintf("- edits[%d] path=%s", i, path))

		if edit.StartLine > 0 || edit.EndLine > 0 {
			b.WriteString(fmt.Sprintf(" lines=%d-%d", edit.StartLine, edit.EndLine))
		}

		if _, ok := invalidPaths[NormalizeRunnerPath(edit.Path)]; ok {
			b.WriteString(" status=invalid_anchor_or_syntax")
		}

		if _, ok := missingPaths[NormalizeRunnerPath(edit.Path)]; ok {
			b.WriteString(" status=missing_required_path")
		}

		if purpose := strings.TrimSpace(edit.Purpose); purpose != "" {
			b.WriteString(" purpose=")
			b.WriteString(textutil.Truncate(strings.TrimSpace(purpose), 180))
		}

		b.WriteString("\n")

		if planThenExecuteShouldIncludeRejectedDraftBody(edit, invalidPaths) {
			body := rejectedDraftEditBody(edit)
			if body != "" {
				b.WriteString("  rejected_body_excerpt_not_source_evidence:\n")

				for _, line := range strings.Split(textutil.Truncate(strings.TrimSpace(body), 2200), "\n") {
					b.WriteString("    ")
					b.WriteString(line)
					b.WriteString("\n")
				}
			}
		}

		if b.Len() > 7000 {
			b.WriteString("- remaining rejected draft entries omitted after bounded evidence cap.\n")

			break
		}
	}

	return strings.TrimSpace(b.String())
}

func planThenExecuteShouldIncludeRejectedDraftBody(edit planartifact.PlanEdit, invalidPaths map[string]struct{}) bool {
	if len(invalidPaths) == 0 {
		return false
	}

	_, ok := invalidPaths[NormalizeRunnerPath(edit.Path)]

	return ok
}

func rejectedDraftEditBody(edit planartifact.PlanEdit) string {
	if strings.TrimSpace(edit.Content) != "" {
		return edit.Content
	}

	if strings.TrimSpace(edit.NewText) != "" {
		return edit.NewText
	}

	return ""
}

// PlanThenExecutePlannerTaskFamily is the runtime-brief task_family
// that marks the planner phase of `idiom plan-execute`. The runner
// uses it to forbid per-edit write tools (edit_file, write_file,
// append_file, apply_patch) — the planner's only
// writes go through submit_plan. The executor phase, which runs in a
// separate process via the orchestrator in internal/surfaces/cli/planexecute/plan_execute.go,
// uses the workspace dispatch directly.
const PlanThenExecutePlannerTaskFamily = string(autonomy.TaskFamilyPlanThenExecutePlanner)

const planThenExecuteAnchorRefreshMaxTokens = 2048

// PlanThenExecuteReviewRepairTaskFamily marks the post-executor
// action-capable review phase. It shares the submit_plan surface for
// corrective edits, but unlike the planner phase it may also close with a
// direct `Decision: pass` answer when the evidence checklist is satisfied.
const PlanThenExecuteReviewRepairTaskFamily = string(autonomy.TaskFamilyPlanThenExecuteReview)

const (
	planThenExecuteResultSubmitPlanMissingSourceBody      = "plan_then_execute.submit_plan.missing_source_body"
	planThenExecuteResultSubmitPlanInvalidAnchor          = "plan_then_execute.submit_plan.invalid_anchor"
	planThenExecuteResultSubmitPlanMissingRequiredSnippet = "plan_then_execute.submit_plan.missing_required_snippet"
)

// planThenExecutePlannerPhase reports whether the runner is currently
// servicing the planner half of plan-then-execute mode.
func (r *runExecution) planThenExecutePlannerPhase() bool {
	if r == nil {
		return false
	}

	return planThenExecutePlannerBrief(r.state.runtimeBrief)
}

func (r *runExecution) planThenExecutePlanSubmissionPhase() bool {
	if r == nil {
		return false
	}

	switch strings.TrimSpace(r.state.runtimeBrief.TaskFamily) {
	case PlanThenExecutePlannerTaskFamily, PlanThenExecuteReviewRepairTaskFamily:
		return true
	default:
		return false
	}
}

func planThenExecutePlannerBrief(brief workerruntime.WorkerRuntimeBrief) bool {
	return strings.TrimSpace(brief.TaskFamily) == PlanThenExecutePlannerTaskFamily
}

// preflightPlanThenExecuteWriteGuard rejects per-edit write tools
// during the planner phase of plan-then-execute mode and steers the
// model at submit_plan instead. The system addendum the orchestrator
// injects is a soft instruction; this guard is the hard one — even
// if the model ignores the prompt, the tool call is intercepted and
// converted into a directional rejection.
//
// Returns Intercepted=false (and an empty result) for any other lane
// shape, so the cost when the guard is inactive is a single string
// comparison.
func (r *runExecution) preflightPlanThenExecuteWriteGuard(req workspace.ToolRequest) verificationInterceptResult {
	if !r.planThenExecutePlanSubmissionPhase() {
		return verificationInterceptResult{}
	}

	tool := strings.TrimSpace(req.Tool)
	if !workspace.IsWorkspaceWriteTool(tool) {
		return verificationInterceptResult{}
	}

	directive := "plan-then-execute planner phase: per-edit write tools are unavailable. " +
		"Enumerate every change in a single submit_plan call and stop. " +
		"The orchestrator's executor phase will dispatch each edit deterministically after you submit the plan."

	return verificationInterceptResult{
		Intercepted: true,
		Result: workspace.ToolResult{
			Tool:    tool,
			Summary: "plan-then-execute planner phase rejected per-edit write",
			Error:   directive,
		},
		Reason: "plan-then-execute planner phase: write tool " + tool + " redirected to submit_plan",
	}
}

// preflightPlanThenExecuteSubmitPlanCoverageGuard validates the
// planner's structured plan against the same required_edit_paths
// contract that normal write tools obey. submit_plan is not a
// per-edit write, so the normal write-scope guard cannot see the
// paths it will later cause the executor to mutate. This guard keeps
// the invariant deterministic while still giving the planner a normal
// recoverable tool error it can fix with a new submit_plan call.
func (r *runExecution) preflightPlanThenExecuteSubmitPlanCoverageGuard(req workspace.ToolRequest) verificationInterceptResult {
	if !r.planThenExecutePlanSubmissionPhase() || strings.TrimSpace(req.Tool) != workspace.ToolNameSubmitPlan {
		return verificationInterceptResult{}
	}

	edits := planThenExecuteEffectiveSubmitPlanEdits(req)

	if len(edits) == 0 && workspace.SubmitPlanRequestClaimsSourceSatisfied(req) {
		code := workspace.SubmitPlanSourceSatisfiedTerminalRequiredCode
		return verificationInterceptResult{
			Intercepted: true,
			Result: workspace.ToolResult{
				Tool:    workspace.ToolNameSubmitPlan,
				Summary: "submit_plan rejected: source-satisfied terminal shape required",
				Error:   "submit_plan cannot carry a source-satisfied/no-op closeout. Answer `Decision: source-satisfied` with per-path current-state evidence, submit real edits, or answer <<QUESTION>> with the exact blocker.",
				Code:    code,
				Facts: toolfacts.NewSubmitPlanDiagnosticFacts(toolfacts.SubmitPlanDiagnostic{
					Code: code,
					Kind: toolfacts.SubmitPlanDiagnosticSourceSatisfiedShape,
				}),
			},
			Reason: "plan-then-execute planner phase: source-satisfied closeout used submit_plan transport",
		}
	}

	sourceSatisfiedPaths := planThenExecuteEffectiveSubmitPlanSourceSatisfiedPaths(req)
	planned := planThenExecutePlannedSubmitPaths(edits, sourceSatisfiedPaths)
	coverageEdits := planThenExecuteCoverageEdits(edits)
	coverageSourceSatisfiedPaths := append([]string(nil), sourceSatisfiedPaths...)
	if r.state.mutation.bound {
		projection := r.state.mutation.canonicalScopeProjection
		for index := range coverageEdits {
			coverageEdits[index].Path = projectedPlanThenExecuteCoveragePath(projection, coverageEdits[index].Path)
		}
		for index := range coverageSourceSatisfiedPaths {
			coverageSourceSatisfiedPaths[index] = projectedPlanThenExecuteCoveragePath(projection, coverageSourceSatisfiedPaths[index])
		}
	}
	decision := plancontract.ValidateCoverage(plancontract.CoverageInput{
		Scope:                   r.planThenExecuteRequiredEditScope(),
		Edits:                   coverageEdits,
		SourceSatisfiedPaths:    coverageSourceSatisfiedPaths,
		MissingRequiredSnippets: r.missingSubmitPlanRequiredEditSnippets(req, planned),
		InvalidAnchors:          r.invalidSubmitPlanAnchors(req),
		PathRoleCoverage:        planThenExecuteContractPathRoleCoverage(r.planThenExecuteSubmitPlanPathRoleCoverage(mapKeys(planned))),
	})

	if !decision.Rejected {
		return verificationInterceptResult{}
	}
	code := decision.Details.Code
	if len(decision.Details.InvalidAnchors) > 0 {
		code = planThenExecuteResultSubmitPlanInvalidAnchor
	} else if len(decision.Details.MissingRequiredSnippets) > 0 {
		code = planThenExecuteResultSubmitPlanMissingRequiredSnippet
	}

	return verificationInterceptResult{
		Intercepted: true,
		Result: workspace.ToolResult{
			Tool:    workspace.ToolNameSubmitPlan,
			Code:    code,
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Output:  plancontract.CoverageDetailsJSON(decision.Details),
			Error:   decision.ErrorText(),
			Facts:   r.submitPlanCoverageDiagnosticFacts(req, decision.Details, code),
		},
		Reason: decision.Reason,
	}
}

// preflightPlanThenExecuteSubmitPlanGroundingGuard prevents planner atoms from
// turning guessed file paths into executor plans. Required and allowed paths
// scope the edit, but existing-file edits still need source-body evidence or an
// explicit new-file/body contract that makes the edit executable.
func (r *runExecution) preflightPlanThenExecuteSubmitPlanGroundingGuard(req workspace.ToolRequest, results []workspace.ToolResult) verificationInterceptResult {
	if r == nil || !r.planThenExecutePlannerPhase() || strings.TrimSpace(req.Tool) != workspace.ToolNameSubmitPlan {
		return verificationInterceptResult{}
	}

	if len(planThenExecuteEffectiveSubmitPlanEdits(req)) == 0 {
		return verificationInterceptResult{}
	}

	missing := r.ungroundedSubmitPlanEditPaths(req, results)
	if len(missing) == 0 {
		return verificationInterceptResult{}
	}

	sort.Strings(missing)

	return verificationInterceptResult{
		Intercepted: true,
		Result: workspace.ToolResult{
			Tool:    workspace.ToolNameSubmitPlan,
			Code:    planThenExecuteResultSubmitPlanMissingSourceBody,
			Summary: "submit_plan rejected: edit paths lack source grounding",
			Error: "submit_plan edits for plan-execute planner atoms must be grounded before executor handoff; missing source-body evidence for " + quotedRunnerStrings(missing) + ". " +
				"Read each existing edit path with `read_file` or " + workspace.ToolNameCodeNavigate + " " + workspace.CodeNavigateSourceBodyCapabilityPhrase() + ", then submit a corrected plan. If the feature is already present, answer `Decision: source-satisfied` with the observed current-state evidence; if no concrete path can be identified, answer `<<QUESTION>>:` with the exact blocker.",
			Facts: r.submitPlanPathDiagnosticFacts(
				planThenExecuteResultSubmitPlanMissingSourceBody,
				toolfacts.SubmitPlanDiagnosticMissingSourceGrounding,
				missing,
			),
		},
		Reason: "plan-then-execute planner phase: submit_plan lacked grounded edit paths",
	}
}

func (r *runExecution) submitPlanCoverageDiagnosticFacts(req workspace.ToolRequest, details plancontract.CoverageDetails, code string) *toolfacts.ToolResultFacts {
	diagnostics := make([]toolfacts.SubmitPlanDiagnostic, 0,
		len(details.MissingRequiredPaths)+len(details.OutsideRequiredPaths)+len(details.MissingRequiredSnippets)+len(details.PlaceholderEditBodies)+1)
	appendPaths := func(kind toolfacts.SubmitPlanDiagnosticKind, paths []string) {
		for _, path := range paths {
			if canonicalID := r.submitPlanDiagnosticCanonicalID(path); canonicalID != "" {
				diagnostics = append(diagnostics, toolfacts.SubmitPlanDiagnostic{
					Code:        code,
					Kind:        kind,
					CanonicalID: canonicalID,
				})
			}
		}
	}

	appendPaths(toolfacts.SubmitPlanDiagnosticMissingRequiredPath, details.MissingRequiredPaths)
	appendPaths(toolfacts.SubmitPlanDiagnosticOutsideScopePath, details.OutsideRequiredPaths)
	if len(details.MissingPathEdits) > 0 {
		diagnostics = append(diagnostics, toolfacts.SubmitPlanDiagnostic{Code: code, Kind: toolfacts.SubmitPlanDiagnosticMissingEditPath})
	}
	for _, obligationID := range details.MissingRequiredSnippets {
		diagnostics = append(diagnostics, toolfacts.SubmitPlanDiagnostic{
			Code:         code,
			Kind:         toolfacts.SubmitPlanDiagnosticMissingRequiredSnippet,
			ObligationID: strings.TrimSpace(obligationID),
		})
	}
	for _, edit := range planThenExecuteEffectiveSubmitPlanEdits(req) {
		if field, _ := plancontract.PlanEditPlaceholderField(planThenExecuteCoverageEdit(edit)); field != "" {
			if canonicalID := r.submitPlanDiagnosticCanonicalID(edit.Path); canonicalID != "" {
				diagnostics = append(diagnostics, toolfacts.SubmitPlanDiagnostic{
					Code:        code,
					Kind:        toolfacts.SubmitPlanDiagnosticPlaceholderBody,
					CanonicalID: canonicalID,
				})
			}
		}
	}

	return toolfacts.NewSubmitPlanDiagnosticFacts(diagnostics...)
}

func (r *runExecution) submitPlanPathDiagnosticFacts(code string, kind toolfacts.SubmitPlanDiagnosticKind, paths []string) *toolfacts.ToolResultFacts {
	diagnostics := make([]toolfacts.SubmitPlanDiagnostic, 0, len(paths))
	for _, path := range paths {
		if canonicalID := r.submitPlanDiagnosticCanonicalID(path); canonicalID != "" {
			diagnostics = append(diagnostics, toolfacts.SubmitPlanDiagnostic{Code: code, Kind: kind, CanonicalID: canonicalID})
		}
	}
	return toolfacts.NewSubmitPlanDiagnosticFacts(diagnostics...)
}

func (r *runExecution) submitPlanDiagnosticCanonicalID(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if r != nil && r.state.mutation.bound {
		claims := r.state.mutation.canonicalScopeProjection.ProjectClaims([]string{path})
		if len(claims.UnsupportedPaths) == 0 && len(claims.CanonicalIDs) == 1 {
			return pathparse.NormalizeWorkspaceRelativePath(claims.CanonicalIDs[0])
		}
		return ""
	}
	if r != nil && r.runner != nil && r.runner.Workspace != nil {
		identity, err := r.runner.Workspace.CanonicalizeWorkspacePath(path, "")
		if err == nil {
			return pathparse.NormalizeWorkspaceRelativePath(identity.CanonicalID)
		}
	}
	return pathparse.NormalizeWorkspaceRelativePath(path)
}

func (r *runExecution) ungroundedSubmitPlanEditPaths(req workspace.ToolRequest, results []workspace.ToolResult) []string {
	edits := planThenExecuteEffectiveSubmitPlanEdits(req)
	if len(edits) == 0 {
		return []string{"edits[]"}
	}
	missing := make([]string, 0, len(edits))
	seen := make(map[string]struct{}, len(edits))

	for i, edit := range edits {
		path := NormalizeRunnerPath(edit.Path)
		if path == "" {
			path = plancontract.EditName(i, planThenExecuteCoverageEdit(edit))
		}

		if _, ok := seen[path]; ok {
			continue
		}

		seen[path] = struct{}{}
		if r.submitPlanEditPathGrounding(path, edit, results).Level != planThenExecuteEvidenceNone {
			continue
		}

		missing = append(missing, path)
	}

	return missing
}

func (r *runExecution) submitPlanEditPathGrounding(path string, edit planartifact.PlanEdit, results []workspace.ToolResult) planThenExecuteSourceEvidence {
	path = NormalizeRunnerPath(path)
	if path == "" {
		return planThenExecuteSourceEvidence{}
	}

	if r != nil && r.planThenExecutePathAuthority().authorizedNewFile(path) {
		shape, err := planartifact.ClassifyPlanEditShape(edit)
		if err == nil && shape == planartifact.PlanEditShapeReplaceAll {
			return planThenExecuteSourceEvidence{
				Path:   path,
				Level:  planThenExecuteEvidenceNewFileContract,
				Source: workspace.ToolNameSubmitPlan,
				Detail: "complete body for canonical authorized-new file",
			}
		}
	}

	if r != nil && r.currentProducerSourceWindowGroundsPlanEdit(path, edit, results) {
		return planThenExecuteSourceEvidence{
			Path:   path,
			Level:  planThenExecuteEvidenceExactAnchorContract,
			Source: workspace.ToolNameSubmitPlan,
			Detail: "edit anchor covered by a current producer source window",
		}
	}

	return planThenExecuteSourceEvidence{Path: path}
}

func (r *runExecution) currentProducerSourceWindowGroundsPlanEdit(path string, edit planartifact.PlanEdit, results []workspace.ToolResult) bool {
	if r == nil || r.runner.Workspace == nil {
		return false
	}
	path = pathparse.NormalizeWorkspaceRelativePath(NormalizeRunnerPath(path))
	if path == "" {
		return false
	}
	shape, err := planartifact.ClassifyPlanEditShape(edit)
	if err != nil || shape == planartifact.PlanEditShapeReplaceAll {
		return false
	}
	ctx := r.state.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	for resultIndex := len(results) - 1; resultIndex >= 0; resultIndex-- {
		if strings.TrimSpace(results[resultIndex].Error) != "" {
			continue
		}
		for _, window := range results[resultIndex].SourceWindows() {
			fact := window.Evidence
			if NormalizeRunnerPath(fact.CanonicalID) != path {
				continue
			}
			current, currentErr := r.runner.Workspace.SourceWindowCurrent(ctx, fact)
			if currentErr != nil || !current {
				continue
			}
			switch shape {
			case planartifact.PlanEditShapeSubstring:
				if strings.Contains(window.Content, edit.OldText) && (!edit.ReplaceAll || sourceWindowCoversWholeFile(fact)) {
					return true
				}
			case planartifact.PlanEditShapeLineRange:
				if fact.StartLine <= edit.StartLine && fact.EndLine >= edit.EndLine {
					return true
				}
			case planartifact.PlanEditShapeInsertAfterLine:
				if fact.StartLine <= edit.InsertAfterLine && fact.EndLine >= edit.InsertAfterLine {
					return true
				}
			}
		}
	}
	return false
}

func (r *runExecution) planThenExecuteRequiredEditScope() plancontract.SubmitPlanCoverageScope {
	root := ""
	if r != nil && r.runner.Workspace != nil {
		root = strings.TrimSpace(r.runner.Workspace.Root())
	}

	if r == nil {
		return plancontract.RequiredScope(nil, nil, planThenExecuteDirectoryScopePredicate(root))
	}
	if r.state.mutation.bound {
		projection := r.state.mutation.canonicalScopeProjection
		scope := plancontract.SubmitPlanCoverageScope{
			Coverage: append([]string(nil), projection.RequiredCoverageIDs...),
		}
		for _, rule := range projection.Rules {
			switch rule.Kind {
			case workspace.MutationScopeExactFile:
				scope.AllowedExact = textutil.AppendUnique(scope.AllowedExact, rule.CanonicalID)
			case workspace.MutationScopeDirectorySubtree:
				scope.AllowedDirectoryScopes = textutil.AppendUnique(scope.AllowedDirectoryScopes, rule.CanonicalID)
			}
		}
		return scope
	}

	return plancontract.RequiredScope(r.state.runtimeBrief.RequiredEditPaths, r.state.runtimeBrief.AllowedEditPaths, planThenExecuteDirectoryScopePredicate(root))
}

func projectedPlanThenExecuteCoveragePath(scope workspace.CanonicalMutationScopeProjection, path string) string {
	claim := scope.ProjectClaims([]string{path})
	if len(claim.UnsupportedPaths) == 0 && len(claim.CanonicalIDs) == 1 {
		return claim.CanonicalIDs[0]
	}
	return NormalizeRunnerPath(path)
}

func planThenExecuteRequiredCoveragePaths(required []string, root string) []string {
	return plancontract.CoveragePaths(required, planThenExecuteDirectoryScopePredicate(root))
}

func normalizedRequiredEditPaths(required []string) []string {
	out := make([]string, 0, len(required))
	seen := make(map[string]struct{}, len(required))

	for _, raw := range required {
		path := NormalizeRunnerPath(raw)
		if path == "" {
			continue
		}

		if _, ok := seen[path]; ok {
			continue
		}

		out = append(out, path)
		seen[path] = struct{}{}
	}

	return out
}

func planThenExecuteRequiredPathIsDirectoryScope(path string, all []string, root string) bool {
	path = pathparse.NormalizeWorkspaceRelativePath(NormalizeRunnerPath(path))
	if path == "" {
		return false
	}

	if pathparse.HasWorkspaceFileExtension(path) {
		return false
	}

	prefix := strings.TrimSuffix(path, "/") + "/"
	for _, other := range all {
		if other != path && strings.HasPrefix(other, prefix) {
			return true
		}
	}

	return false
}

func planThenExecuteDirectoryScopePredicate(root string) func(string) bool {
	return func(path string) bool {
		return planThenExecuteRequiredPathIsDirectoryScope(path, nil, root)
	}
}

func submitPlanDiagnostics(result workspace.ToolResult) []toolfacts.SubmitPlanDiagnostic {
	if strings.TrimSpace(result.Tool) != workspace.ToolNameSubmitPlan || result.Facts == nil {
		return nil
	}
	return toolfacts.NormalizeSubmitPlanDiagnostics(result.Facts.SubmitPlanDiagnostics)
}

func planThenExecutePlannedSubmitPaths(edits []planartifact.PlanEdit, sourceSatisfiedPaths []string) map[string]struct{} {
	planned := make(map[string]struct{}, len(edits)+len(sourceSatisfiedPaths))
	for _, path := range sourceSatisfiedPaths {
		if normalized := NormalizeRunnerPath(path); normalized != "" {
			planned[normalized] = struct{}{}
		}
	}
	for _, edit := range edits {
		if path := NormalizeRunnerPath(edit.Path); path != "" {
			planned[path] = struct{}{}
		}
	}
	return planned
}

func planThenExecuteCoverageEdits(edits []planartifact.PlanEdit) []plancontract.SubmitPlanCoverageEdit {
	out := make([]plancontract.SubmitPlanCoverageEdit, 0, len(edits))
	for _, edit := range edits {
		out = append(out, planThenExecuteCoverageEdit(edit))
	}
	return out
}

func planThenExecuteCoverageEdit(edit planartifact.PlanEdit) plancontract.SubmitPlanCoverageEdit {
	return plancontract.SubmitPlanCoverageEdit{
		Path:            edit.Path,
		Purpose:         edit.Purpose,
		OldText:         edit.OldText,
		NewText:         edit.NewText,
		Content:         edit.Content,
		StartLine:       edit.StartLine,
		EndLine:         edit.EndLine,
		InsertAfterLine: edit.InsertAfterLine,
		ReplaceAll:      edit.ReplaceAll,
	}
}

func planThenExecuteContractPathRoleCoverage(coverage []autonomy.PlanScopePathRoleCoverage) []plancontract.PathRoleCoverage {
	out := make([]plancontract.PathRoleCoverage, 0, len(coverage))
	for _, item := range coverage {
		out = append(out, plancontract.PathRoleCoverage{
			Path:                       item.Path,
			OriginalPath:               item.OriginalPath,
			Roles:                      planThenExecutePathRoleNames(item.Roles),
			EffectiveRole:              string(item.EffectiveRole),
			EffectiveInvariantID:       string(item.EffectiveInvariantID),
			Decision:                   string(item.Decision),
			SatisfiesRequiredCoverage:  item.SatisfiesRequiredCoverage,
			WithinAllowedCandidatePath: item.WithinAllowedCandidatePath,
		})
	}
	return out
}

func planThenExecutePathRoleNames(roles []autonomy.PathRoleKind) []string {
	out := make([]string, 0, len(roles))
	for _, role := range roles {
		if strings.TrimSpace(string(role)) != "" {
			out = append(out, string(role))
		}
	}
	return out
}

func (r *runExecution) planThenExecuteSubmitPlanPathRoleCoverage(candidatePaths []string) []autonomy.PlanScopePathRoleCoverage {
	if r == nil || len(candidatePaths) == 0 {
		return nil
	}
	coverage, err := autonomy.ResolvePlanScopePathRoleObligations(autonomy.PlanScopePathRoleObligationInput{
		RequiredEditPaths:      r.state.runtimeBrief.RequiredEditPaths,
		AllowedCandidatePaths:  r.state.runtimeBrief.AllowedEditPaths,
		CandidateCoveragePaths: candidatePaths,
		Source:                 "plan_execute_submit_plan_guard",
	})
	if err != nil {
		return nil
	}
	return append([]autonomy.PlanScopePathRoleCoverage(nil), coverage.CandidateCoverage...)
}

func (r *runExecution) invalidSubmitPlanAnchors(req workspace.ToolRequest) []string {
	if r == nil || r.runner.Workspace == nil {
		return nil
	}
	return nil
}

func (r *runExecution) missingSubmitPlanRequiredEditSnippets(req workspace.ToolRequest, planned map[string]struct{}) []string {
	if r == nil {
		return nil
	}

	brief := workerruntime.NormalizeRuntimeBrief(r.state.runtimeBrief)
	if len(brief.RequiredEditSnippets) == 0 {
		return nil
	}

	projectedContent := projectedPlanContentByPath(planThenExecuteEffectiveSubmitPlanEdits(req))

	var missing []string

	for i, snippet := range brief.RequiredEditSnippets {
		candidates := requiredEditSnippetCandidatePaths(snippet, brief.RequiredEditPaths, planned)
		if len(candidates) == 0 {
			candidates = mapKeys(planned)
		}

		if planThenExecuteRequiredSnippetCovered(candidates, snippet.Content, projectedContent) {
			continue
		}

		missing = append(missing, requiredEditSnippetName(snippet, i))
	}

	return missing
}

func projectedPlanContentByPath(edits []planartifact.PlanEdit) map[string]string {
	out := make(map[string]string, len(edits))
	for _, edit := range edits {
		path := NormalizeRunnerPath(edit.Path)
		if path == "" {
			continue
		}

		content, loaded := out[path]
		if !loaded {
			content = ""
		}

		out[path] = applyPlanProjectionEdit(content, edit)
	}

	return out
}

func planThenExecuteEffectiveSubmitPlanEdits(req workspace.ToolRequest) []planartifact.PlanEdit {
	if req.PlanArtifact != nil && len(req.PlanArtifact.Edits) > 0 {
		return append([]planartifact.PlanEdit(nil), req.PlanArtifact.Edits...)
	}

	return append([]planartifact.PlanEdit(nil), req.Edits...)
}

func planThenExecuteEffectiveSubmitPlanSourceSatisfiedPaths(req workspace.ToolRequest) []string {
	var paths []string

	if req.PlanArtifact != nil && len(req.PlanArtifact.Paths.SourceSatisfied) > 0 {
		paths = append(paths, req.PlanArtifact.Paths.SourceSatisfied...)
	}

	paths = append(paths, req.SourceSatisfiedPaths...)

	return paths
}

func (r *runExecution) planThenExecuteAttachSubmitPlanSourceSatisfiedEvidence(req workspace.ToolRequest, results []workspace.ToolResult) workspace.ToolRequest {
	if r == nil || !r.planThenExecutePlanSubmissionPhase() || strings.TrimSpace(req.Tool) != workspace.ToolNameSubmitPlan {
		return req
	}

	sourceSatisfiedPaths := plancontract.UniqueScopePaths(planThenExecuteEffectiveSubmitPlanSourceSatisfiedPaths(req))
	if len(sourceSatisfiedPaths) == 0 {
		return req
	}

	artifact := planartifact.PlanArtifact{}
	if req.PlanArtifact != nil {
		artifact = *req.PlanArtifact
	}

	if len(artifact.Edits) == 0 && len(req.Edits) > 0 {
		artifact.Edits = append([]planartifact.PlanEdit(nil), req.Edits...)
	}

	if strings.TrimSpace(artifact.Task.Summary) == "" {
		artifact.Task.Summary = textutil.FirstNonEmpty(strings.TrimSpace(req.Rationale), "legacy submit_plan request")
	}

	artifact.Contract.Kind = planartifact.ContractKind(textutil.FirstNonEmpty(strings.TrimSpace(string(artifact.Contract.Kind)), string(planartifact.ContractKindPlanThenExecute)))
	artifact.Contract.StrategyOwner = planartifact.Owner(textutil.FirstNonEmpty(strings.TrimSpace(string(artifact.Contract.StrategyOwner)), string(planartifact.OwnerModel)))
	artifact.Contract.ContractOwner = planartifact.Owner(textutil.FirstNonEmpty(strings.TrimSpace(string(artifact.Contract.ContractOwner)), string(planartifact.OwnerHarness)))
	artifact.Contract.PermissionProfile = textutil.FirstNonEmpty(strings.TrimSpace(artifact.Contract.PermissionProfile), planartifact.PermissionProfilePlanThenExecute)
	artifact.Contract.VerificationOwner = planartifact.Owner(textutil.FirstNonEmpty(strings.TrimSpace(string(artifact.Contract.VerificationOwner)), string(planartifact.OwnerPlanner)))
	artifact.Paths.SourceSatisfied = plancontract.UniqueScopePaths(append(artifact.Paths.SourceSatisfied, sourceSatisfiedPaths...))

	if len(artifact.Paths.Required) == 0 {
		artifact.Paths.Required = plancontract.UniqueScopePaths(r.state.runtimeBrief.RequiredEditPaths)
	}

	if len(artifact.Contract.RequiredEditPaths) == 0 {
		artifact.Contract.RequiredEditPaths = append([]string(nil), artifact.Paths.Required...)
	}

	if len(artifact.VerificationObligations) == 0 {
		artifact.VerificationObligations = []planartifact.PlanArtifactVerificationObligation{{
			ID:       "verification-1",
			Kind:     planartifact.VerificationKindExecutorLaneSanity,
			Owner:    artifact.Contract.VerificationOwner,
			Status:   planartifact.VerificationStatusOpen,
			Required: true,
		}}
	}

	sourceSatisfiedSet := map[string]struct{}{}

	for _, path := range sourceSatisfiedPaths {
		if normalized := NormalizeRunnerPath(path); normalized != "" {
			sourceSatisfiedSet[normalized] = struct{}{}
		}
	}

	filteredEvidence := artifact.Evidence[:0]
	for _, evidence := range artifact.Evidence {
		path := NormalizeRunnerPath(evidence.Path)
		if _, sourceSatisfied := sourceSatisfiedSet[path]; sourceSatisfied {
			continue
		}

		filteredEvidence = append(filteredEvidence, evidence)
	}

	artifact.Evidence = filteredEvidence

	knownEvidence := map[string]struct{}{}

	for _, evidence := range artifact.Evidence {
		path := NormalizeRunnerPath(evidence.Path)
		kind := strings.TrimSpace(string(evidence.Kind))

		if path != "" && kind != "" {
			knownEvidence[path+"|"+kind] = struct{}{}
		}
	}

	for _, path := range sourceSatisfiedPaths {
		canonicalPath := NormalizeRunnerPath(path)
		if r.state.mutation.bound {
			claims := r.state.mutation.canonicalScopeProjection.ProjectClaims([]string{path})
			if len(claims.UnsupportedPaths) > 0 || len(claims.CanonicalIDs) != 1 {
				continue
			}
			canonicalPath = claims.CanonicalIDs[0]
		}

		evidence, current := r.currentPlanThenExecuteSourceSatisfiedEvidence(canonicalPath, results)
		if !current {
			continue
		}

		kind := string(planartifact.EvidenceKindHarnessObservedSource)
		key := NormalizeRunnerPath(path) + "|" + kind

		if _, ok := knownEvidence[key]; ok {
			continue
		}

		artifact.Evidence = append(artifact.Evidence, planartifact.PlanArtifactEvidence{
			Kind:       planartifact.EvidenceKind(kind),
			Path:       path,
			Summary:    textutil.FirstNonEmpty(strings.TrimSpace(evidence.Detail), "current-state source evidence") + " via " + textutil.FirstNonEmpty(strings.TrimSpace(evidence.Source), "source tool"),
			Confidence: "observed",
		})
		knownEvidence[key] = struct{}{}
	}

	req.PlanArtifact = &artifact

	return req
}

func (r *runExecution) currentPlanThenExecuteSourceSatisfiedEvidence(path string, results []workspace.ToolResult) (planThenExecuteSourceEvidence, bool) {
	if r == nil || r.runner.Workspace == nil {
		return planThenExecuteSourceEvidence{}, false
	}
	path = pathparse.NormalizeWorkspaceRelativePath(path)
	if path == "" {
		return planThenExecuteSourceEvidence{}, false
	}
	ctx := r.state.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	for resultIndex := len(results) - 1; resultIndex >= 0; resultIndex-- {
		result := results[resultIndex]
		if strings.TrimSpace(result.Error) != "" {
			continue
		}
		for _, window := range result.SourceWindows() {
			fact := window.Evidence
			if pathparse.NormalizeWorkspaceRelativePath(fact.CanonicalID) != path {
				continue
			}
			level, detail := planThenExecuteReadFileEvidenceLevel(fact)
			if level != planThenExecuteEvidenceAnchorReady {
				continue
			}
			current, err := r.runner.Workspace.SourceWindowCurrent(ctx, fact)
			if err != nil || !current {
				continue
			}
			return planThenExecuteSourceEvidence{
				Path:   path,
				Level:  level,
				Source: strings.TrimSpace(result.Tool),
				Detail: detail,
			}, true
		}
	}
	return planThenExecuteSourceEvidence{}, false
}

func applyPlanProjectionEdit(current string, edit planartifact.PlanEdit) string {
	if edit.ReplaceAll && strings.TrimSpace(edit.OldText) == "" && strings.TrimSpace(edit.NewText) != "" {
		return edit.NewText
	}

	if strings.TrimSpace(edit.OldText) != "" {
		if strings.Contains(current, edit.OldText) {
			return strings.Replace(current, edit.OldText, edit.NewText, 1)
		}

		return current + "\n" + edit.NewText
	}

	if edit.StartLine > 0 || edit.EndLine > 0 {
		if projected, ok := applyPlanProjectionRange(current, edit.StartLine, edit.EndLine, edit.Content); ok {
			return projected
		}

		return current + "\n" + edit.Content
	}

	if strings.TrimSpace(edit.NewText) != "" {
		return current + "\n" + edit.NewText
	}

	if strings.TrimSpace(edit.Content) != "" {
		return current + "\n" + edit.Content
	}

	return current
}

func applyPlanProjectionRange(current string, startLine int, endLine int, replacement string) (string, bool) {
	if startLine < 1 || endLine < startLine {
		return "", false
	}

	lines := strings.SplitAfter(current, "\n")
	if current == "" {
		lines = nil
	}

	if endLine > len(lines) {
		return "", false
	}

	start := startLine - 1
	before := strings.Join(lines[:start], "")
	after := strings.Join(lines[endLine:], "")

	if replacement != "" && !strings.HasSuffix(replacement, "\n") && after != "" {
		replacement += "\n"
	}

	return before + replacement + after, true
}

func planThenExecuteRequiredSnippetCovered(candidates []string, snippet string, projectedContent map[string]string) bool {
	snippet = strings.TrimSpace(snippet)
	if snippet == "" {
		return true
	}

	for _, candidate := range candidates {
		path := NormalizeRunnerPath(candidate)
		if path == "" {
			continue
		}

		if editSnippetMatchesContent(projectedContent[path], snippet) {
			return true
		}
	}

	return false
}

func planThenExecuteSourceBodyReadRequest(req workspace.ToolRequest) bool {
	tool := strings.TrimSpace(req.Tool)
	switch tool {
	case workspace.ToolNameReadFile:
		return req.StartLine > 0 || req.EndLine > 0
	case workspace.ToolNameCodeNavigate, workspace.ToolNameReadOnlyCapability:
		capability := strings.TrimSpace(req.Capability)

		return workspace.CodeNavigateCapabilityPredicate(semanticlanguage.CapReadSymbol)(capability) ||
			workspace.CodeNavigateCapabilityPredicate(semanticlanguage.CapExtractBody)(capability) ||
			strings.HasSuffix(capability, "_read_symbol") ||
			strings.HasSuffix(capability, "_extract_body")
	default:
		return strings.HasSuffix(tool, "_read_symbol") || strings.HasSuffix(tool, "_extract_body")
	}
}

func planThenExecuteAnchorRefreshRequestAllowed(req workspace.ToolRequest) bool {
	switch strings.TrimSpace(req.Tool) {
	case workspace.ToolNameReadFile:
		return req.StartLine > 0 && req.EndLine >= req.StartLine && req.EndLine-req.StartLine+1 <= 400
	case workspace.ToolNameCodeNavigate:
		capability := strings.TrimSpace(req.Capability)

		return workspace.CodeNavigateCapabilityPredicate(semanticlanguage.CapReadSymbol)(capability) ||
			workspace.CodeNavigateCapabilityPredicate(semanticlanguage.CapExtractBody)(capability)
	default:
		return false
	}
}

func planThenExecuteInvalidAnchorRefreshRequestAllowed(req workspace.ToolRequest, path string, results []workspace.ToolResult) bool {
	if !planThenExecuteAnchorRefreshRequestAllowed(req) {
		return false
	}

	if strings.TrimSpace(req.Tool) != workspace.ToolNameReadFile {
		return true
	}

	path = NormalizeRunnerPath(path)
	if path == "" || !strings.EqualFold(filepath.Ext(path), ".go") {
		return true
	}

	if planThenExecuteReadFileCoversRejectedSyntaxLine(req, path, results) {
		return true
	}
	if planThenExecuteReadFileCoversRejectedLineRange(req, path, results) {
		return true
	}

	return true
}

func planThenExecuteReadFileCoversRejectedSyntaxLine(req workspace.ToolRequest, path string, results []workspace.ToolResult) bool {
	if strings.TrimSpace(req.Tool) != workspace.ToolNameReadFile || req.StartLine <= 0 || req.EndLine < req.StartLine {
		return false
	}

	rejected, _, ok := lastRejectedSubmitPlanCoverageResult(results)
	if !ok {
		return false
	}

	line, ok := submitPlanSyntaxValidationLineForPath(rejected, path)
	if !ok {
		return false
	}

	return req.StartLine <= line && line <= req.EndLine
}

func planThenExecuteReadFileCoversRejectedLineRange(req workspace.ToolRequest, path string, results []workspace.ToolResult) bool {
	if strings.TrimSpace(req.Tool) != workspace.ToolNameReadFile || req.StartLine <= 0 || req.EndLine < req.StartLine {
		return false
	}

	rejected, _, ok := lastRejectedSubmitPlanCoverageResult(results)
	if !ok {
		return false
	}

	ranges := submitPlanLineRangeInvalidAnchorRanges(rejected, path)
	for _, lineRange := range ranges {
		if req.StartLine <= lineRange.start && req.EndLine >= lineRange.end {
			return true
		}
	}

	return false
}

func submitPlanSyntaxValidationLineForPath(result workspace.ToolResult, path string) (int, bool) {
	path = NormalizeRunnerPath(path)
	if path == "" {
		return 0, false
	}
	for _, diagnostic := range submitPlanDiagnostics(result) {
		if diagnostic.Kind == toolfacts.SubmitPlanDiagnosticSyntaxValidation &&
			diagnostic.CanonicalID == path && diagnostic.StartLine > 0 {
			return diagnostic.StartLine, true
		}
	}
	return 0, false
}

func planThenExecuteAnchorRefreshReadFallbackInstruction(refreshPaths []string) string {
	paths := nonEmptyRunnerStrings(refreshPaths, 1)
	if len(paths) == 0 {
		return "A bounded `read_file` or `" + workspace.ToolNameCodeNavigate + "` source-body request can refresh a concrete missing anchor fact."
	}

	path := NormalizeRunnerPath(paths[0])
	if path == "" {
		path = strings.TrimSpace(paths[0])
	}

	return "`" + path + "` is one known refresh target; choose any permission-authorized bounded read or `" + workspace.ToolNameCodeNavigate + "` source-body request that resolves the missing anchor fact."
}

func planThenExecutePostSubmitRejectionRefreshNote(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, rejected workspace.ToolResult, rejectedAt int) string {
	invalidAnchorPaths := submitPlanInvalidAnchorPaths(rejected)
	refreshable := make([]string, 0, 2)

	for _, rawPath := range planThenExecuteRefreshScopePaths(brief, results) {
		path := NormalizeRunnerPath(rawPath)
		if path == "" {
			continue
		}

		if _, invalid := invalidAnchorPaths[path]; invalid && !successfulInvalidAnchorCorrectionEvidenceForRejection(results, rejectedAt, path) {
			refreshable = append(refreshable, path)
			continue
		}
	}

	if len(refreshable) == 0 {
		return "The gathered source-body evidence supports a corrected plan. Permission-authorized inspection remains available when one concrete edit-safety fact is still unresolved."
	}

	return "Known stale anchor paths that may need source-body refresh are: " + strings.Join(nonEmptyRunnerStrings(refreshable, 4), ", ") + ". Permission-authorized bounded reads or `" + workspace.ToolNameCodeNavigate + "` with " + workspace.CodeNavigateSourceBodyCapabilityPhrase() + " plus `pattern` remain available for a concrete unresolved anchor fact before the corrected `submit_plan`."
}

func planThenExecuteAnchorRefreshReadRangeAlreadyTried(req workspace.ToolRequest, target string, results []workspace.ToolResult) bool {
	if strings.TrimSpace(req.Tool) != workspace.ToolNameReadFile {
		return false
	}

	target = NormalizeRunnerPath(target)
	if target == "" || req.StartLine <= 0 || req.EndLine < req.StartLine {
		return false
	}

	for _, readRange := range readRangesForPathFromResults(results, target) {
		if planThenExecuteRequestedRangeCoveredByRead(req.StartLine, req.EndLine, readRange) {
			return true
		}
	}

	return false
}

func submitPlanRejectionHasInvalidAnchor(result workspace.ToolResult) bool {
	for _, diagnostic := range submitPlanDiagnostics(result) {
		switch diagnostic.Kind {
		case toolfacts.SubmitPlanDiagnosticInvalidAnchor,
			toolfacts.SubmitPlanDiagnosticSyntaxValidation,
			toolfacts.SubmitPlanDiagnosticWholeFileRewrite:
			return true
		}
	}
	return false
}

func submitPlanInvalidAnchorPaths(result workspace.ToolResult) map[string]struct{} {
	out := make(map[string]struct{})
	for _, diagnostic := range submitPlanDiagnostics(result) {
		switch diagnostic.Kind {
		case toolfacts.SubmitPlanDiagnosticInvalidAnchor,
			toolfacts.SubmitPlanDiagnosticSyntaxValidation,
			toolfacts.SubmitPlanDiagnosticWholeFileRewrite:
			if diagnostic.CanonicalID != "" {
				out[diagnostic.CanonicalID] = struct{}{}
			}
		}
	}
	return out
}

func submitPlanWholeFileExistingSourceRewritePaths(result workspace.ToolResult) map[string]struct{} {
	out := make(map[string]struct{})
	for _, diagnostic := range submitPlanDiagnostics(result) {
		if diagnostic.Kind == toolfacts.SubmitPlanDiagnosticWholeFileRewrite && diagnostic.CanonicalID != "" {
			out[diagnostic.CanonicalID] = struct{}{}
		}
	}
	return out
}

func submitPlanLineRangeInvalidAnchorRanges(result workspace.ToolResult, path string) []planThenExecuteLineRange {
	path = NormalizeRunnerPath(path)
	if path == "" {
		return nil
	}
	var ranges []planThenExecuteLineRange
	for _, diagnostic := range submitPlanDiagnostics(result) {
		if diagnostic.CanonicalID != path || diagnostic.StartLine <= 0 || diagnostic.EndLine < diagnostic.StartLine {
			continue
		}
		switch diagnostic.Kind {
		case toolfacts.SubmitPlanDiagnosticInvalidAnchor,
			toolfacts.SubmitPlanDiagnosticSyntaxValidation,
			toolfacts.SubmitPlanDiagnosticWholeFileRewrite:
			ranges = append(ranges, planThenExecuteLineRange{start: diagnostic.StartLine, end: diagnostic.EndLine})
		}
	}
	return ranges
}

type planThenExecuteLineRange struct {
	start int
	end   int
}

func submitPlanMissingRequiredPaths(result workspace.ToolResult) map[string]struct{} {
	out := make(map[string]struct{})
	for _, diagnostic := range submitPlanDiagnostics(result) {
		if diagnostic.Kind == toolfacts.SubmitPlanDiagnosticMissingRequiredPath && diagnostic.CanonicalID != "" {
			out[diagnostic.CanonicalID] = struct{}{}
		}
	}
	return out
}

func submitPlanMissingSourceGroundingPaths(result workspace.ToolResult) map[string]struct{} {
	out := make(map[string]struct{})
	for _, diagnostic := range submitPlanDiagnostics(result) {
		if diagnostic.Kind == toolfacts.SubmitPlanDiagnosticMissingSourceGrounding && diagnostic.CanonicalID != "" {
			out[diagnostic.CanonicalID] = struct{}{}
		}
	}
	return out
}

func planThenExecuteRecoveryScopePathAllowed(path string) bool {
	path = NormalizeRunnerPath(path)
	if path == "" || submitPlanDiagnosticProtocolToken(path) {
		return false
	}

	if pathparse.LooksLikeWorkspacePathToken(path) || pathparse.LooksLikeWorkspaceFilePath(path) {
		return true
	}

	base := filepath.Base(path)
	if strings.Contains(base, ".") && !strings.HasSuffix(base, ".") && !strings.ContainsAny(path, " \t\r\n") {
		return true
	}

	if strings.HasPrefix(base, ".") && len(base) > 1 && !strings.ContainsAny(path, " \t\r\n") {
		return true
	}

	switch base {
	case "Brewfile", "Justfile", "Pipfile", "Rakefile", "Taskfile":
		return true
	default:
		return false
	}
}

func submitPlanDiagnosticProtocolToken(path string) bool {
	lower := strings.ToLower(strings.TrimSpace(path))
	if lower == "" {
		return true
	}

	if !strings.ContainsAny(lower, "abcdefghijklmnopqrstuvwxyz0123456789.") {
		return true
	}

	if strings.Contains(lower, "<<question>>") || lower == "decision" || strings.HasPrefix(lower, "decision ") {
		return true
	}

	switch lower {
	case workspace.ToolNameReadFile, workspace.ToolNameCodeNavigate, "read_symbol", "extract_body", workspace.ToolNameSubmitPlan:
		return true
	default:
		return false
	}
}

func submitPlanInvalidAnchorPathsAllowsRead(paths map[string]struct{}, path string) bool {
	if len(paths) == 0 {
		return false
	}

	_, ok := paths[NormalizeRunnerPath(path)]

	return ok
}

func successfulSourceEvidencePathAfter(results []workspace.ToolResult, index int, path string) bool {
	path = NormalizeRunnerPath(path)
	if path == "" {
		return false
	}

	for i := index + 1; i < len(results); i++ {
		if planThenExecuteEvidenceRank(newToolResultEvidenceRecord(results[i]).SourceEvidenceForPath(path).Level) >= planThenExecuteEvidenceRank(planThenExecuteEvidenceBodyWindow) {
			return true
		}
	}

	return false
}

func successfulInvalidAnchorCorrectionEvidencePathAfter(results []workspace.ToolResult, index int, path string) bool {
	path = NormalizeRunnerPath(path)
	if path == "" {
		return false
	}

	for i := index + 1; i < len(results); i++ {
		if invalidAnchorCorrectionEvidenceResult(results[i], path, results[:i+1]) {
			return true
		}
	}

	return false
}

func successfulInvalidAnchorCorrectionEvidenceForRejection(results []workspace.ToolResult, rejectedAt int, path string) bool {
	if successfulInvalidAnchorCorrectionEvidencePathAfter(results, rejectedAt, path) {
		return true
	}

	return successfulInvalidAnchorCorrectionEvidenceAfterPriorRejection(results, rejectedAt, path)
}

func successfulInvalidAnchorCorrectionEvidenceAfterPriorRejection(results []workspace.ToolResult, rejectedAt int, path string) bool {
	path = NormalizeRunnerPath(path)
	if path == "" || rejectedAt <= 0 || rejectedAt > len(results) {
		return false
	}

	for i := rejectedAt - 1; i >= 0; i-- {
		result := results[i]
		if strings.TrimSpace(result.Tool) != workspace.ToolNameSubmitPlan {
			continue
		}
		if strings.TrimSpace(result.Error) == "" {
			return false
		}
		if planThenExecuteSourceSatisfiedSubmitPlanRejectedResult(result) || !planThenExecuteSubmitPlanRejectedResult(result) {
			continue
		}
		if _, invalid := submitPlanInvalidAnchorPaths(result)[path]; !invalid {
			continue
		}
		for j := i + 1; j < rejectedAt; j++ {
			if invalidAnchorCompleteCorrectionEvidenceResult(results[j], path, results[:j+1]) {
				return true
			}
		}
	}

	return false
}

func invalidAnchorCompleteCorrectionEvidenceResult(result workspace.ToolResult, path string, results []workspace.ToolResult) bool {
	if readFileResultCoversRejectedLineRange(result, path, results) {
		return true
	}

	evidence := newToolResultEvidenceRecord(result).SourceEvidenceForPath(path)
	if evidence.Level != planThenExecuteEvidenceAnchorReady {
		return false
	}

	if strings.TrimSpace(result.Tool) != workspace.ToolNameReadFile {
		return true
	}

	facts := newToolResultEvidenceRecord(result).SourceWindowFacts(path)
	if len(facts) == 0 {
		return false
	}

	for _, fact := range facts {
		if sourceWindowCoversWholeFile(fact) {
			return true
		}

		req := workspace.ToolRequest{
			Tool:      workspace.ToolNameReadFile,
			Path:      path,
			StartLine: fact.StartLine,
			EndLine:   fact.EndLine,
		}
		if planThenExecuteReadFileCoversRejectedSyntaxLine(req, path, results) ||
			planThenExecuteReadFileCoversRejectedLineRange(req, path, results) {
			return true
		}
	}

	return false
}

func invalidAnchorCorrectionEvidenceResult(result workspace.ToolResult, path string, results []workspace.ToolResult) bool {
	if readFileResultCoversRejectedLineRange(result, path, results) {
		return true
	}
	if readFileResultIsBoundedCppSourceWindow(result, path) {
		return true
	}

	evidence := newToolResultEvidenceRecord(result).SourceEvidenceForPath(path)
	if evidence.Level != planThenExecuteEvidenceAnchorReady {
		return false
	}

	if strings.TrimSpace(result.Tool) != workspace.ToolNameReadFile {
		return true
	}

	facts := newToolResultEvidenceRecord(result).SourceWindowFacts(path)
	if len(facts) == 0 {
		return false
	}

	for _, fact := range facts {
		if sourceWindowCoversWholeFile(fact) {
			return true
		}

		req := workspace.ToolRequest{
			Tool:      workspace.ToolNameReadFile,
			Path:      path,
			StartLine: fact.StartLine,
			EndLine:   fact.EndLine,
		}
		if planThenExecuteReadFileCoversRejectedSyntaxLine(req, path, results) ||
			planThenExecuteReadFileCoversRejectedLineRange(req, path, results) {
			return true
		}
	}

	return false
}

func readFileResultIsBoundedCppSourceWindow(result workspace.ToolResult, path string) bool {
	if strings.TrimSpace(result.Tool) != workspace.ToolNameReadFile || strings.TrimSpace(result.Error) != "" {
		return false
	}
	windows := newToolResultEvidenceRecord(result).SourceWindowsForPath(path)
	if len(windows) == 0 {
		return false
	}

	normalizedPath := strings.ToLower(NormalizeRunnerPath(path))
	if !(strings.HasSuffix(normalizedPath, ".cpp") ||
		strings.HasSuffix(normalizedPath, ".cc") ||
		strings.HasSuffix(normalizedPath, ".cxx")) {
		return false
	}
	for _, window := range windows {
		if window.Evidence.StartLine <= 400 {
			return true
		}
	}

	return false
}

func readFileResultCoversRejectedLineRange(result workspace.ToolResult, path string, results []workspace.ToolResult) bool {
	if strings.TrimSpace(result.Tool) != workspace.ToolNameReadFile || strings.TrimSpace(result.Error) != "" {
		return false
	}

	facts := newToolResultEvidenceRecord(result).SourceWindowFacts(path)
	if len(facts) == 0 {
		return false
	}

	for _, fact := range facts {
		if planThenExecuteReadFileCoversRejectedLineRange(workspace.ToolRequest{
			Tool:      workspace.ToolNameReadFile,
			Path:      path,
			StartLine: fact.StartLine,
			EndLine:   fact.EndLine,
		}, path, results) {
			return true
		}
	}

	return false
}

func lastRejectedSubmitPlanCoverageResult(results []workspace.ToolResult) (workspace.ToolResult, int, bool) {
	for i := len(results) - 1; i >= 0; i-- {
		result := results[i]
		if strings.TrimSpace(result.Tool) == workspace.ToolNameSubmitPlan &&
			strings.TrimSpace(result.Code) == planThenExecuteSubmitPlanHistoryDeferredCode &&
			result.Facts != nil &&
			result.Facts.RejectionDisposition == toolfacts.ToolRejectionDeferredByPrerequisite {

			// A newer, typed prerequisite deferral owns the current submit-plan
			// lifecycle. Do not revive an older malformed/empty-plan recovery
			// while the model gathers the exact history evidence for this intent.
			return workspace.ToolResult{}, -1, false
		}
		if planThenExecuteSourceSatisfiedSubmitPlanRejectedResult(result) {
			continue
		}

		if planThenExecuteSubmitPlanRejectedResult(result) {
			return result, i, true
		}
	}

	return workspace.ToolResult{}, -1, false
}

func planThenExecuteSubmitPlanRejectedForCorrection(req workspace.ToolRequest, result workspace.ToolResult) bool {
	return strings.TrimSpace(req.Tool) == workspace.ToolNameSubmitPlan &&
		planThenExecuteSubmitPlanRejectedResult(result) &&
		!planThenExecuteSourceSatisfiedSubmitPlanRejectedResult(result)
}

func planThenExecuteSubmitPlanRejectedResult(result workspace.ToolResult) bool {
	if strings.TrimSpace(result.Tool) != workspace.ToolNameSubmitPlan {
		return false
	}
	if strings.TrimSpace(result.Code) == planThenExecuteSubmitPlanHistoryDeferredCode {
		return false
	}

	if len(submitPlanDiagnostics(result)) > 0 {
		return true
	}

	return result.Failure != nil || strings.TrimSpace(result.Error) != ""
}

func planThenExecuteSourceSatisfiedSubmitPlanRejectedResult(result workspace.ToolResult) bool {
	if strings.TrimSpace(result.Tool) != workspace.ToolNameSubmitPlan {
		return false
	}

	if strings.TrimSpace(result.Code) == workspace.SubmitPlanSourceSatisfiedTerminalRequiredCode {
		return true
	}
	for _, diagnostic := range submitPlanDiagnostics(result) {
		if diagnostic.Kind == toolfacts.SubmitPlanDiagnosticSourceSatisfiedShape {
			return true
		}
	}
	return false
}

func planThenExecuteSourceSatisfiedTerminalRequiredPrompt(brief workerruntime.WorkerRuntimeBrief, result workspace.ToolResult) string {
	required := planThenExecuteRequiredCoveragePaths(brief.RequiredEditPaths, "")
	scopeLine := "Use the relevant selected edit paths from gathered current-source evidence."

	if len(required) > 0 {
		scopeLine = "Required path(s): " + strings.Join(required, ", ") + "."
	}

	return strings.Join([]string{
		"The previous `submit_plan` attempted a source-satisfied/no-op closeout using the wrong transport. That rejected tool call is not a valid plan artifact and is not an accepted source-satisfied terminal answer.",
		"",
		lastSubmitPlanRejectionBrief(result),
		scopeLine,
		"",
		"The next visible action must be exactly one terminal outcome:",
		"1. `Decision: source-satisfied` with per-path current-state evidence if every required or selected path already contains the requested final state.",
		"2. `submit_plan` with real executable edits for every required or selected path if changes are needed.",
		"3. `<<QUESTION>>:` with the exact path-specific blocker.",
		"",
		"Do not repeat an empty `submit_plan`, and do not treat the rejected tool call as plan coverage.",
	}, "\n")
}

func planThenExecuteSubmitPlanGroundingRejectedResult(result workspace.ToolResult) bool {
	for _, diagnostic := range submitPlanDiagnostics(result) {
		if diagnostic.Kind == toolfacts.SubmitPlanDiagnosticMissingSourceGrounding {
			return true
		}
	}
	return false
}

func submitPlanRejectionHasLargeInsertAfterLine(result workspace.ToolResult) bool {
	for _, diagnostic := range submitPlanDiagnostics(result) {
		if diagnostic.Kind == toolfacts.SubmitPlanDiagnosticOversizedInsertion {
			return true
		}
	}
	return false
}

func submitPlanRejectionHasInvalidEditShape(result workspace.ToolResult) bool {
	for _, diagnostic := range submitPlanDiagnostics(result) {
		if diagnostic.Kind == toolfacts.SubmitPlanDiagnosticInvalidEditShape {
			return true
		}
	}
	return false
}

func lastSubmitPlanRejectionBrief(result workspace.ToolResult) string {
	text := strings.TrimSpace(result.Error)
	if text == "" {
		text = strings.TrimSpace(result.Summary)
	}

	if text == "" {
		return ""
	}

	if planThenExecuteSubmitPlanGroundingRejectedResult(result) {
		lower := strings.ToLower(text)
		for _, marker := range []string{". read each existing edit path", ". if the feature is already present", ". if no concrete path can be identified"} {
			if cut := strings.Index(lower, marker); cut > 0 {
				text = strings.TrimSpace(text[:cut]) + "."
				break
			}
		}
	}

	text = strings.Join(strings.Fields(text), " ")

	const limit = 720

	if len(text) > limit {
		const (
			head = 360
			tail = 300
		)

		text = strings.TrimSpace(text[:head]) + " ... " + strings.TrimSpace(text[len(text)-tail:])
	}

	return "Last submit_plan rejection: " + text + " "
}

func lastSubmitPlanCorrectionRejectionBrief(result workspace.ToolResult) string {
	return sanitizeSubmitCorrectionRejectionBrief(lastSubmitPlanRejectionBrief(result))
}

func sanitizeSubmitCorrectionRejectionBrief(text string) string {
	replacements := map[string]string{
		"answer `Decision: source-satisfied`, use `source_satisfied_paths` for mixed coverage, or submit real edits":                                     "use `source_satisfied_paths` for already-final paths or submit real edits",
		"Answer `Decision: source-satisfied`, use `source_satisfied_paths` for mixed coverage, or submit real edits":                                     "Use `source_satisfied_paths` for already-final paths or submit real edits",
		"answer `Decision: source-satisfied` with current-state evidence when no changes are needed":                                                     "use `source_satisfied_paths` for already-final paths or submit real edits",
		"Answer `Decision: source-satisfied` with current-state evidence when no changes are needed":                                                     "Use `source_satisfied_paths` for already-final paths or submit real edits",
		"If every required path is already final, answer `Decision: source-satisfied` with per-path current-state evidence instead of using submit_plan": "Do not use terminal source-satisfied in submit-correction mode",
		"or answer `Decision: source-satisfied` if every required path is final":                                                                         "or use `source_satisfied_paths` for already-final paths",
		"answer `Decision: source-satisfied` if all paths are final":                                                                                     "use `source_satisfied_paths` for already-final paths",
		"if all paths are final answer `Decision: source-satisfied`; else":                                                                               "",
		"use `Decision: source-satisfied` only if all paths are final; else":                                                                             "",
	}
	for old, replacement := range replacements {
		text = strings.ReplaceAll(text, old, replacement)
	}

	return text
}

func planThenExecuteUnreadRequiredPaths(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, authority planThenExecutePathAuthority) map[string]struct{} {
	required := authority.requiredCoveragePaths(brief)
	unread := make(map[string]struct{}, len(required))

	for _, path := range required {
		state := planThenExecuteRequiredPathState(authority, results, path)
		if !state.requiresSourceBodyRead() {
			continue
		}

		unread[path] = struct{}{}
	}

	return unread
}

func planThenExecutePendingRequiredSourceEvidence(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, authority planThenExecutePathAuthority) []planThenExecuteSourceEvidence {
	required := authority.requiredCoveragePaths(brief)
	pending := make([]planThenExecuteSourceEvidence, 0, len(required))

	projection := newToolResultEvidenceProjection(results)
	for _, path := range required {
		state := planThenExecuteRequiredPathState(authority, results, path)
		if !state.requiresSourceBodyRead() {
			continue
		}

		evidence := projection.SourceEvidenceForPath(path)
		if evidence.Level == planThenExecuteEvidenceAnchorReady {
			continue
		}

		if evidence.Path == "" {
			evidence.Path = path
		}

		if evidence.Level == planThenExecuteEvidenceNone {
			evidence.Level = planThenExecuteEvidenceNone
			evidence.Detail = "no source evidence"
		}

		pending = append(pending, evidence)
	}

	return pending
}

func planThenExecutePendingRequiredSourceEvidenceSummary(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, max int, authority planThenExecutePathAuthority) string {
	pending := planThenExecutePendingRequiredSourceEvidence(brief, results, authority)
	if len(pending) == 0 {
		return ""
	}

	if max <= 0 || max > len(pending) {
		max = len(pending)
	}

	parts := make([]string, 0, max)
	for _, evidence := range pending[:max] {
		level := string(evidence.Level)
		if level == "" {
			level = "none"
		}

		detail := strings.TrimSpace(evidence.Detail)
		if detail != "" {
			parts = append(parts, evidence.Path+"("+level+": "+detail+")")
		} else {
			parts = append(parts, evidence.Path+"("+level+")")
		}
	}

	if len(pending) > max {
		parts = append(parts, fmt.Sprintf("+%d more", len(pending)-max))
	}

	return strings.Join(parts, ", ")
}

type planThenExecuteRequiredPathTypedState string

const (
	planThenExecuteRequiredPathExistingSourceBodyRequired planThenExecuteRequiredPathTypedState = "existing_source_body_required"
	planThenExecuteRequiredPathNewFileBodyRequired        planThenExecuteRequiredPathTypedState = "new_file_body_required"
	planThenExecuteRequiredPathSourceSatisfied            planThenExecuteRequiredPathTypedState = "source_satisfied"
	planThenExecuteRequiredPathStaleAnchor                planThenExecuteRequiredPathTypedState = "stale_anchor"
	planThenExecuteRequiredPathBlocked                    planThenExecuteRequiredPathTypedState = "blocked"
)

func (state planThenExecuteRequiredPathTypedState) requiresSourceBodyRead() bool {
	switch state {
	case planThenExecuteRequiredPathExistingSourceBodyRequired:
		return true
	default:
		return false
	}
}

type planThenExecutePathAuthority struct {
	scope                   *workspace.CanonicalMutationScopeProjection
	blockedWorkspacePathIDs []string
}

func (r *runExecution) planThenExecutePathAuthority() planThenExecutePathAuthority {
	if r == nil {
		return planThenExecutePathAuthority{}
	}
	scope := r.state.canonicalMutationScopeProjection()
	authority := planThenExecutePathAuthority{scope: scope}
	if scope == nil {
		return authority
	}
	for _, item := range taskLedgerBlockedItemProjection(r.taskLedger) {
		if item.SubjectNamespace != workspace.TaskLedgerBlockerSubjectWorkspacePath {
			continue
		}
		claims := scope.ProjectClaims([]string{item.SubjectID})
		if len(claims.UnsupportedPaths) != 0 || len(claims.CanonicalIDs) != 1 {
			continue
		}
		authority.blockedWorkspacePathIDs = textutil.AppendUnique(authority.blockedWorkspacePathIDs, claims.CanonicalIDs[0])
	}
	return authority
}

func (authority planThenExecutePathAuthority) canonicalID(path string) (string, bool) {
	if authority.scope == nil {
		return "", false
	}
	claims := authority.scope.ProjectClaims([]string{path})
	if len(claims.UnsupportedPaths) != 0 || len(claims.CanonicalIDs) != 1 {
		return "", false
	}
	return claims.CanonicalIDs[0], true
}

func (authority planThenExecutePathAuthority) requiredCoveragePaths(brief workerruntime.WorkerRuntimeBrief) []string {
	if authority.scope != nil {
		return append([]string(nil), authority.scope.RequiredCoverageIDs...)
	}
	return planThenExecuteRequiredCoveragePaths(brief.RequiredEditPaths, "")
}

func (authority planThenExecutePathAuthority) authorizedNewFile(path string) bool {
	canonicalID, ok := authority.canonicalID(path)
	if !ok {
		return false
	}
	for _, rule := range authority.scope.Rules {
		if rule.Kind == workspace.MutationScopeExactFile &&
			rule.CanonicalID == canonicalID &&
			rule.Source == workspace.MutationScopeAuthorizedNewFile {

			return true
		}
	}
	return false
}

func (authority planThenExecutePathAuthority) blocked(path string) bool {
	canonicalID, ok := authority.canonicalID(path)
	return ok && slices.Contains(authority.blockedWorkspacePathIDs, canonicalID)
}

func planThenExecuteRequiredPathState(authority planThenExecutePathAuthority, results []workspace.ToolResult, path string) planThenExecuteRequiredPathTypedState {
	path = NormalizeRunnerPath(path)
	if path == "" {
		return planThenExecuteRequiredPathBlocked
	}

	if authority.authorizedNewFile(path) {
		return planThenExecuteRequiredPathNewFileBodyRequired
	}

	if authority.blocked(path) {
		return planThenExecuteRequiredPathBlocked
	}

	if rejected, rejectedAt, ok := lastRejectedSubmitPlanCoverageResult(results); ok {
		_, wholeFileRewrite := submitPlanWholeFileExistingSourceRewritePaths(rejected)[path]
		if _, stale := submitPlanInvalidAnchorPaths(rejected)[path]; stale && !wholeFileRewrite && !successfulInvalidAnchorCorrectionEvidencePathAfter(results, rejectedAt, path) {
			return planThenExecuteRequiredPathStaleAnchor
		}
	}

	if newToolResultEvidenceProjection(results).SourceEvidenceForPath(path).Level == planThenExecuteEvidenceAnchorReady {
		return planThenExecuteRequiredPathSourceSatisfied
	}

	return planThenExecuteRequiredPathExistingSourceBodyRequired
}

func planThenExecuteResultIsHarnessDiagnostic(result workspace.ToolResult) bool {
	tool := strings.ToLower(strings.TrimSpace(result.Tool))
	if strings.Contains(tool, "autonomy") ||
		strings.Contains(tool, "gate") ||
		strings.Contains(tool, "guard") {

		return true
	}

	text := strings.ToLower(strings.TrimSpace(result.Summary + "\n" + result.Error + "\n" + result.Output))
	if strings.Contains(text, `"safe_alternatives"`) ||
		strings.Contains(text, `"gate"`) ||
		strings.Contains(text, `"guard_friction"`) ||
		strings.Contains(text, "runner_completion_gate") ||
		strings.Contains(text, "runner_budget_gate") {

		return true
	}

	return false
}

func planThenExecuteRequiredPathStateSummaries(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, authority planThenExecutePathAuthority) []string {
	required := authority.requiredCoveragePaths(brief)
	out := make([]string, 0, len(required))

	for _, path := range required {
		normalized := NormalizeRunnerPath(path)
		if normalized == "" {
			continue
		}

		out = append(out, normalized+":"+string(planThenExecuteRequiredPathState(authority, results, normalized)))
	}

	return out
}

func planThenExecuteReadFileEvidenceLevel(fact toolfacts.SourceWindowEvidence) (PlanThenExecuteSourceEvidenceLevel, string) {
	start, end := fact.StartLine, fact.EndLine
	total := fact.TotalLines
	hasRange := sourceWindowHasRange(fact)
	hasTotal := total > 0

	if fact.Truncated {
		return planThenExecuteEvidenceBodyWindow, "bounded body window"
	}

	if hasRange && hasTotal && end < total {
		if end-start+1 <= planThenExecuteBoundedReadMaxLines {
			return planThenExecuteEvidenceAnchorReady, "bounded source range"
		}
		if hasTotal && total > 0 && float64(end-start+1)/float64(total) >= 0.85 {
			return planThenExecuteEvidenceAnchorReady, "near-full-file bounded range"
		}

		return planThenExecuteEvidenceBodyWindow, "broad source range"
	}

	if hasRange && hasTotal && end >= total {
		return planThenExecuteEvidenceAnchorReady, "whole-file/current-state read"
	}

	if hasRange {
		if end-start+1 <= planThenExecuteBoundedReadMaxLines {
			return planThenExecuteEvidenceAnchorReady, "bounded source range"
		}

		return planThenExecuteEvidenceBodyWindow, "broad source range"
	}

	return planThenExecuteEvidenceBodyWindow, "source body window"
}

const planThenExecuteBoundedReadMaxLines = 120

func planThenExecuteRequestTargetPath(req workspace.ToolRequest) string {
	for _, candidate := range []string{req.Path, req.Destination, req.Workdir} {
		candidate = strings.TrimSpace(candidate)
		if candidate != "" && candidate != "." {
			return candidate
		}
	}

	if path := pathFromPlanThenExecuteHandle(req.Handle); path != "" {
		return path
	}

	if path := pathFromPlanThenExecuteHandle(req.ID); path != "" {
		return path
	}

	return ""
}

func pathFromPlanThenExecuteHandle(handle string) string {
	handle = strings.TrimSpace(handle)
	switch {
	case strings.HasPrefix(handle, "file://"):
		return strings.TrimPrefix(handle, "file://")
	case strings.HasPrefix(handle, "symbol://"):
		rest := strings.TrimPrefix(handle, "symbol://")
		if path, _, ok := strings.Cut(rest, "#"); ok {
			return path
		}

		return rest
	default:
		return ""
	}
}

// preflightPlanThenExecutePlannerToolGuard rejects planner requests that are
// outside the permission-authorized inspection and plan-submission contract.
// Request-specific read-only classification belongs to the workspace surface;
// the runtime must not reject those requests from the tool name alone.
func (r *runExecution) preflightPlanThenExecutePlannerToolGuard(req workspace.ToolRequest) verificationInterceptResult {
	if !r.planThenExecutePlannerPhase() {
		return verificationInterceptResult{}
	}

	tool := strings.TrimSpace(req.Tool)
	inspection := r.permissionAuthorizedInspection(r.workspaceAdvertisedAllowedTools())
	if inspection.AllowsRequest(req) {
		return verificationInterceptResult{}
	}
	if tool == workspace.ToolNameContextTracePack &&
		slices.Contains(textutil.Merge(r.workspaceAdvertisedAllowedTools(), r.state.runtimeBrief.AllowedTools), tool) &&
		workspace.AnalyzeShellCommand(req.Command).SideEffect == autonomy.SideEffectReadOnly {

		return verificationInterceptResult{}
	}
	if !planThenExecutePlannerForbiddenTool(tool) {
		return verificationInterceptResult{}
	}

	return verificationInterceptResult{
		Intercepted: true,
		Result: workspace.ToolResult{
			Tool:    tool,
			Summary: "plan-then-execute planner phase rejected non-inspection tool",
			Error: "plan-then-execute planner phase does not run mutation, build, test, source-sanity, delegation, or task-ledger commands. " +
				"Use the current planner terminal contract instead; in submit-correction mode, emit corrected `submit_plan` only.",
		},
		Reason: "plan-then-execute planner phase: non-inspection tool " + tool + " is outside the planner contract",
	}
}

func planThenExecutePlannerForbiddenTool(tool string) bool {
	switch strings.TrimSpace(tool) {
	case workspace.ToolNameShell, workspace.ToolNameGitDiff, workspace.ToolNameGitStatus, workspace.ToolNameSourceSanity, "affected_suites", workspace.ToolNameContextTracePack, workspace.ToolNameTask, workspace.ToolNameTaskLedger, workspace.ToolNamePwd:
		return true
	default:
		return false
	}
}

func planThenExecuteEmptySubmitPlanRejection(result workspace.ToolResult) bool {
	if strings.TrimSpace(result.Tool) != workspace.ToolNameSubmitPlan {
		return false
	}

	return strings.TrimSpace(result.Code) == workspace.SubmitPlanEmptyEditsCode
}

func planThenExecuteUnscopedEmptySubmitPlanRejected(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if strings.TrimSpace(brief.TaskFamily) != PlanThenExecutePlannerTaskFamily {
		return false
	}

	if len(planThenExecuteRequiredCoveragePaths(brief.RequiredEditPaths, "")) > 0 ||
		len(normalizedRequiredEditPaths(brief.AllowedEditPaths)) > 0 {

		return false
	}

	rejected, _, ok := lastRejectedSubmitPlanCoverageResult(results)

	return ok && planThenExecuteEmptySubmitPlanRejection(rejected)
}

func planThenExecuteUnscopedEmptySubmitPlanAttempt(brief workerruntime.WorkerRuntimeBrief, req workspace.ToolRequest) bool {
	if strings.TrimSpace(brief.TaskFamily) != PlanThenExecutePlannerTaskFamily ||
		strings.TrimSpace(req.Tool) != workspace.ToolNameSubmitPlan {

		return false
	}

	if len(planThenExecuteRequiredCoveragePaths(brief.RequiredEditPaths, "")) > 0 ||
		len(normalizedRequiredEditPaths(brief.AllowedEditPaths)) > 0 {

		return false
	}

	return len(planThenExecuteEffectiveSubmitPlanEdits(req)) == 0
}

// successfulSubmitPlanResultCount returns the number of successful
// submit_plan tool results in the lane's recorded history. The
// completion gate uses this as the "the planner did its job" signal
// for plan-then-execute mode, replacing the usual changed-paths check
// (the planner's submit_plan call doesn't change files; the executor
// phase does, in a separate process).
func successfulSubmitPlanResultCount(results []workspace.ToolResult) int {
	count := 0

	for _, r := range results {
		if strings.TrimSpace(r.Tool) != workspace.ToolNameSubmitPlan {
			continue
		}

		if strings.TrimSpace(r.Error) != "" {
			continue
		}

		count++
	}

	return count
}

func (r *runExecution) planThenExecuteAcceptedSourceSatisfiedPaths(reply string, results []workspace.ToolResult) ([]string, bool) {
	if r == nil || !r.planThenExecutePlannerPhase() {
		return nil, false
	}

	if planThenExecuteSubmitCorrectionActive(r.state.runtimeBrief, results, r.planThenExecutePathAuthority()) {
		return nil, false
	}

	missing := append([]string(nil), r.requiredEditCoveragePaths()...)
	declared := plancontract.SourceSatisfiedEvidencePaths(reply)
	if r.state.mutation.bound {
		claims := r.state.mutation.canonicalScopeProjection.ProjectClaims(declared)
		if len(claims.UnsupportedPaths) > 0 || len(claims.CanonicalIDs) == 0 || !plancontract.SourceSatisfiedAnswerIntent(reply) {
			return nil, false
		}
		canonicalDeclared := uniqueSortedRunnerPaths(claims.CanonicalIDs)
		if len(missing) == 0 {
			missing = canonicalDeclared
		} else if !slices.Equal(uniqueSortedRunnerPaths(missing), canonicalDeclared) {
			return nil, false
		}
	} else {
		if len(missing) == 0 {
			missing = declared
		}
		if !plancontract.SourceSatisfiedAnswerCoversPaths(reply, missing) {
			return nil, false
		}
	}

	// Evidence-ledger check: every required path the model claims as
	// source-satisfied must have a source-body read receipt in the
	// collected results. This prevents stale-evidence claims where
	// source-satisfied is asserted for a path whose content may have
	// changed since the last read.
	//
	// During the anchor-refresh phase, refresh-state paths must have
	// evidence from the current turn (results after SeedResults) to
	// prevent stale seed evidence from satisfying the check.
	if len(missing) > 0 {
		state := r.derivePlanExecutePlanningState(results, PlanExecutePlanningTransport{TextualToolParser: true})
		refreshCutoff := len(r.runner.SeedResults)

		if state.Mode != PlanExecutePlanningModeAnchorRefresh || len(state.RefreshPaths) == 0 {
			refreshCutoff = 0
		}

		if !r.requiredPathsHaveCurrentSourceWindowEvidence(missing, results, state.RefreshPaths, refreshCutoff) {
			return nil, false
		}
	}

	return uniqueSortedRunnerPaths(missing), true
}

func planThenExecuteSubmitCorrectionActive(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, authority planThenExecutePathAuthority) bool {
	if strings.TrimSpace(brief.TaskFamily) != PlanThenExecutePlannerTaskFamily {
		return false
	}

	return derivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true}, authority).Mode == PlanExecutePlanningModeSubmitCorrection
}

func planThenExecuteSubmitCorrectionBlockerAllowed(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if strings.TrimSpace(brief.TaskFamily) != PlanThenExecutePlannerTaskFamily || successfulSubmitPlanResultCount(results) > 0 {
		return false
	}

	rejected, rejectedAt, ok := lastRejectedSubmitPlanCoverageResult(results)
	if !ok {
		return false
	}
	if len(planThenExecuteRejectedPlanRefreshPathsNeedingRefreshBeforeBlocker(brief, results, rejected, rejectedAt)) > 0 {
		return false
	}

	streak := planThenExecuteRejectedSubmitPlanStreak(results)
	if len(streak) < planThenExecuteRejectedSubmitPlanStreakThreshold {
		return false
	}

	return planThenExecuteSubmitPlanMissingBodyOrUnsafeAnchor(rejected)
}

func planThenExecuteRejectedPlanRefreshPathsNeedingRefreshBeforeBlocker(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, rejected workspace.ToolResult, rejectedAt int) []string {
	paths := planThenExecuteRejectedPlanRefreshPaths(brief, results, rejected, rejectedAt)
	if len(paths) == 0 {
		return nil
	}

	wholeFileRewritePaths := submitPlanWholeFileExistingSourceRewritePaths(rejected)
	if len(wholeFileRewritePaths) == 0 {
		return paths
	}

	var out []string
	for _, path := range paths {
		normalized := NormalizeRunnerPath(path)
		if normalized == "" {
			continue
		}
		if _, wholeFileRewrite := wholeFileRewritePaths[normalized]; wholeFileRewrite {
			continue
		}
		out = append(out, normalized)
	}

	return uniqueSortedRunnerPaths(out)
}

func planThenExecuteSubmitPlanMissingBodyOrUnsafeAnchor(result workspace.ToolResult) bool {
	switch strings.TrimSpace(result.Code) {
	case planThenExecuteResultSubmitPlanMissingSourceBody,
		planThenExecuteResultSubmitPlanInvalidAnchor,
		planThenExecuteResultSubmitPlanMissingRequiredSnippet:
		return true
	default:
		return false
	}
}

func planThenExecuteSubmitCorrectionOutcomeInstruction(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) string {
	if planThenExecuteSubmitCorrectionBlockerAllowed(brief, results) {
		return "Submit a corrected parser-readable `submit_plan` request whose coverage includes every required_edit_path with valid executable edits from current source-body evidence. If the available evidence still cannot support a safe executable edit body, answer `<<QUESTION>>:` with the exact path-specific missing body/anchor blocker. Permission-authorized inspection requests remain callable."
	}

	return "Submit a corrected parser-readable `submit_plan` request whose coverage includes every required_edit_path with either valid executable edits from current source-body evidence or `source_satisfied_paths` for paths already at final state. Permission-authorized inspection requests remain callable."
}

func planThenExecuteSubmitCorrectionReadyNudgeOutcomeInstruction(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) string {
	if planThenExecuteSubmitCorrectionBlockerAllowed(brief, results) {
		return "When ready, emit the corrected parser-readable action rather than describing it in prose. Permission-authorized inspection requests remain callable. If no safe executable edit body can be produced after these rejected submit_plan attempts, answer `<<QUESTION>>:` with the exact path-specific missing body/anchor blocker."
	}

	return "When ready, emit the corrected parser-readable action rather than describing it in prose. Permission-authorized inspection requests remain callable."
}

func planThenExecuteSourceSatisfiedResultsCoverPaths(requiredPaths []string, results []workspace.ToolResult) bool {
	if len(results) == 0 {
		return false
	}

	projection := newToolResultEvidenceProjection(results)
	for _, path := range requiredPaths {
		if projection.SourceEvidenceForPath(path).Level != planThenExecuteEvidenceAnchorReady {
			return false
		}
	}

	return true
}

func (r *runExecution) requiredPathsHaveCurrentSourceWindowEvidence(requiredPaths []string, results []workspace.ToolResult, refreshPaths []string, resultCutoff int) bool {
	if r == nil || r.runner.Workspace == nil || len(requiredPaths) == 0 {
		return false
	}

	refreshSet := make(map[string]struct{}, len(refreshPaths))
	for _, p := range refreshPaths {
		refreshSet[NormalizeRunnerPath(p)] = struct{}{}
	}

	ctx := r.state.ctx
	if ctx == nil {
		ctx = context.Background()
	}

	for _, path := range requiredPaths {
		normPath := NormalizeRunnerPath(path)
		windowResults := results

		if _, needsFresh := refreshSet[normPath]; needsFresh && resultCutoff > 0 && resultCutoff < len(results) {
			windowResults = results[resultCutoff:]
		}

		current := false
		for _, result := range windowResults {
			record := newToolResultEvidenceRecord(result)
			if record.failed {
				continue
			}
			for _, window := range record.SourceWindowsForPath(normPath) {
				level, _ := planThenExecuteReadFileEvidenceLevel(window.Evidence)
				if level != planThenExecuteEvidenceAnchorReady {
					continue
				}
				isCurrent, err := r.runner.Workspace.SourceWindowCurrent(ctx, window.Evidence)
				if err == nil && isCurrent {
					current = true
					break
				}
			}
			if current {
				break
			}
		}
		if !current {
			return false
		}
	}

	return true
}

// planThenExecuteReadyToSubmitNudge returns a system-message string
// the runner injects into the next turn when the planner phase has
// anchor-ready source evidence for every required_edit_path and has not
// yet called submit_plan. The nudge converts the planner's verification urge
// into closeout pressure: instead of letting the model spend more
// reads narrowing anchors that already exist in the brief, it gets a
// directive saying the state is ready and the next action MUST be
// submit_plan or <<QUESTION>>.
//
// Returns "" when the nudge does not apply (wrong lane, plan already
// submitted, paths still unread, etc.). The runner emits the nudge
// at most once per turn-since-coverage; it is the caller's job to
// gate against re-injection on every subsequent tool result.
func planThenExecuteReadyToSubmitNudge(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, authority planThenExecutePathAuthority) string {
	if strings.TrimSpace(brief.TaskFamily) != PlanThenExecutePlannerTaskFamily {
		return ""
	}

	required := authority.requiredCoveragePaths(brief)

	if successfulSubmitPlanResultCount(results) > 0 {
		// Plan already submitted — the gate-exemption path will close
		// the runner cleanly; nothing to nudge.
		return ""
	}

	if rejected, _, ok := lastRejectedSubmitPlanCoverageResult(results); ok && planThenExecuteSubmitCorrectionActive(brief, results, authority) {
		return strings.Join([]string{
			"PLANNER SUBMIT-CORRECTION READY: every required_edit_path has source-body evidence, but the previous `submit_plan` was rejected before it became a valid plan artifact.",
			"",
			lastSubmitPlanCorrectionRejectionBrief(rejected),
			"Current source evidence supports a parser-readable `submit_plan` request. " + planThenExecuteSubmitPlanCoverageSentence(),
			planThenExecuteSubmitPlanCorrectionInstructionForBrief(brief, rejected),
			"",
			planThenExecuteSubmitCorrectionReadyNudgeOutcomeInstruction(brief, results),
		}, "\n")
	}

	if len(required) == 0 {
		if len(newToolResultEvidenceProjection(results).ReadPaths()) == 0 {
			return ""
		}

		state := PlanExecutePlanningState{
			EvidencePaths: sortedRunnerPathKeys(newToolResultEvidenceProjection(results).ReadPaths()),
		}
		paths := planThenExecuteSelectedEditPaths(brief, results, state)

		if len(paths) == 0 {
			return ""
		}

		observed := newToolResultEvidenceProjection(results).ReadPaths()

		var observedSelected []string

		for _, path := range paths {
			normalized := NormalizeRunnerPath(path)
			if _, ok := observed[normalized]; ok {
				observedSelected = append(observedSelected, normalized)
			}
		}

		if len(observedSelected) > 0 {
			paths = uniqueSortedRunnerPaths(observedSelected)
		}

		if len(planThenExecuteHistoryPendingPaths(brief, results, paths, authority)) > 0 {
			return ""
		}

		unreadCandidateLine := "Allowed/candidate paths are not coverage obligations: do not read every allowed path by default."
		readActionLine := "  2. One bounded source-read/search for a different candidate path, but only when it names one concrete edit-safety fact still needed before the plan."
		finalLine := "Permission-authorized inspection remains available when it targets a concrete unresolved edit-safety fact; otherwise the gathered evidence supports a terminal planner outcome."

		return strings.Join([]string{
			"PLANNER READINESS REACHED: at least one selected candidate edit path has source evidence in this turn. This makes `submit_plan` available, but it does not prove every candidate path is complete.",
			"",
			"Selected candidate path(s): " + strings.Join(paths, ", "),
			"",
			unreadCandidateLine,
			"",
			"Available evidence-backed actions:",
			"  1. `submit_plan` with only the edit(s) needed by the objective, using insertion, substring, replacement-only line range, or missing/new-file shape, plus a one-sentence purpose per edit.",
			readActionLine,
			"  3. `Decision: source-satisfied` naming the relevant candidate path(s) and observed current-state evidence when the requested final state is already present.",
			"  4. `<<QUESTION>>:` with the exact blocker that prevents drafting the plan.",
			"",
			"Hidden reasoning is not executable evidence. The next action must be visible content or a native tool call.",
			"",
			finalLine,
		}, "\n")
	}

	var readyPaths []string

	for _, path := range required {
		normalized := NormalizeRunnerPath(path)
		state := planThenExecuteRequiredPathState(authority, results, normalized)

		switch state {
		case planThenExecuteRequiredPathSourceSatisfied, planThenExecuteRequiredPathNewFileBodyRequired:
			readyPaths = append(readyPaths, normalized)
		default:
			return ""
		}
	}

	if len(readyPaths) == 0 {
		return ""
	}

	if len(planThenExecuteHistoryPendingPaths(brief, results, readyPaths, authority)) > 0 {
		return ""
	}

	return strings.Join([]string{
		"PLANNER READINESS REACHED: every required_edit_path is ready for a terminal planner outcome; existing files have anchor-ready source evidence and missing/new files have explicit new-file evidence.",
		"",
		"The gathered evidence supports a parser-readable `submit_plan` request. " + planThenExecuteSubmitPlanCoverageSentence(),
		"Use the smallest executable edit body that satisfies the objective. Prefer exact `old_text`/`new_text` for small unique replacements; use line-range `content` only for multi-line replacements or large insertions anchored to unchanged source lines. Do not replace whole functions or files when a smaller anchored edit is enough.",
		planThenExecuteSubmitPlanVisibleJSONInstruction() + " For a missing/new source or test file, the brief must provide a substantial inline body or explicit replacement edit shape.",
		"",
		"Narrow exceptions only:",
		"- Use `Decision: source-satisfied` only when every required path already contains the requested final state, and cite current-state evidence for every required path.",
		"- Use `<<QUESTION>>:` only when one exact path-specific blocker prevents drafting any valid `submit_plan` edit.",
		"",
		"Hidden reasoning is not executable evidence. The next action must be visible content or a native tool call.",
		"",
		"Permission-authorized inspection remains callable when it addresses a concrete unresolved edit-safety fact; otherwise proceed with the supported terminal planner outcome.",
	}, "\n")
}

func planThenExecuteSubmitPlanOnlyReady(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, authority planThenExecutePathAuthority) bool {
	if strings.TrimSpace(brief.TaskFamily) != PlanThenExecutePlannerTaskFamily {
		return false
	}

	if planThenExecuteUnscopedEmptySubmitPlanRejected(brief, results) {
		return false
	}

	required := authority.requiredCoveragePaths(brief)

	if successfulSubmitPlanResultCount(results) > 0 {
		return false
	}

	if len(required) == 0 {
		state := PlanExecutePlanningState{
			EvidencePaths: sortedRunnerPathKeys(newToolResultEvidenceProjection(results).ReadPaths()),
		}
		selected := planThenExecuteSelectedEditPaths(brief, results, state)

		return len(selected) > 0 && len(planThenExecuteHistoryPendingPaths(brief, results, selected, authority)) == 0
	}

	for _, path := range required {
		state := planThenExecuteRequiredPathState(authority, results, path)
		switch state {
		case planThenExecuteRequiredPathSourceSatisfied, planThenExecuteRequiredPathNewFileBodyRequired:
			continue
		default:
			return false
		}
	}

	return len(planThenExecuteHistoryPendingPaths(brief, results, required, authority)) == 0
}

func planThenExecuteCandidateSourcePending(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, authority planThenExecutePathAuthority) bool {
	if strings.TrimSpace(brief.TaskFamily) != PlanThenExecutePlannerTaskFamily {
		return false
	}

	if successfulSubmitPlanResultCount(results) > 0 {
		return false
	}

	if len(authority.requiredCoveragePaths(brief)) > 0 {
		return false
	}

	normalized := workerruntime.NormalizeRuntimeBrief(brief)
	if len(normalized.RequiredEditSnippets) > 0 {
		return false
	}

	if len(normalizedRequiredEditPaths(brief.AllowedEditPaths)) == 0 {
		return true
	}

	if len(planThenExecuteAnchorReadyPaths(brief, results, authority)) > 0 {
		return false
	}

	return true
}

func planThenExecuteRequestedRangeCoveredByRead(start int, end int, prior planThenExecuteReadRange) bool {
	if start <= 0 {
		start = 1
	}

	if prior.total > 0 {
		if end <= 0 || end > prior.total {
			end = prior.total
		}

		if start > prior.total {
			start = prior.total
		}
	}

	if end <= 0 || end < start {
		return false
	}

	return start >= prior.start && end <= prior.end
}

func planThenExecuteSubmitPlanVisibleJSONInstruction() string {
	return "Textual-tool backends must emit exactly one visible JSON object with top-level `\"tool\":\"submit_plan\"` and top-level `edits` as a JSON array, not a quoted string. " + planThenExecuteSubmitPlanCoverageSentence() + " If a previous rejection says an edit would not change a path, move that path to top-level `source_satisfied_paths` only when current evidence shows it is already at final state; otherwise replace the no-op with a real edit. Every edit path must be a real required or selected path from gathered source evidence, and every edit body must be executable final source. Keep edit bodies as small as executable: prefer exact `old_text`/`new_text` for small unique replacements; use numeric `start_line`/`end_line` plus complete `content` for multi-line source/test edits and large additions anchored to unchanged source lines. For small insertions/appends, use `insert_after_line` plus inline non-empty `content` only after an existing line. Do not replace whole functions or files when a smaller anchored edit is enough."
}

func planThenExecuteSubmitPlanCoverageSentence() string {
	return "Cover each required edit path exactly once: put paths needing changes in `edits[]` with one real executable edit object, and put paths already at the requested final state in top-level `source_satisfied_paths`; never create an `edits[]` object whose only purpose is source-satisfied coverage."
}

func planThenExecuteSubmitPlanCorrectionInstructionForBrief(brief workerruntime.WorkerRuntimeBrief, result workspace.ToolResult) string {
	var parts []string

	if checklist := planThenExecuteSubmitPlanCorrectionChecklist(brief, result); checklist != "" {
		parts = append(parts, checklist)
	}

	if submitPlanRejectionHasLargeInsertAfterLine(result) {
		parts = append(parts, "The rejected edit used an oversized or ref-backed `insert_after_line` body. Do not repeat `insert_after_line` for that edit, and do not use `content_ref` with `insert_after_line`. Emit exactly one visible JSON object with top-level `edits` as a JSON array, using `start_line`/`end_line`/`content` for the large addition by replacing a small current-source anchor range on the real edit path with the unchanged anchor line(s) plus inserted content from existing evidence.")
	} else if submitPlanRejectionHasInvalidEditShape(result) {
		parts = append(parts, "The rejected plan contained an invalid edit shape. "+planThenExecuteSubmitPlanCoverageSentence()+" Each `edits[]` object must choose exactly one executable shape: `start_line`/`end_line`/non-empty `content` for a line-range replacement, exact `old_text`/`new_text`, small `insert_after_line`/inline non-empty `content`, or `replace_all:true` with full `new_text` for missing/new-file writes only. Existing source/test files must use bounded anchored edits. Do not combine shapes, do not use placeholders, and do not use content refs with `insert_after_line`.")
	} else {
		parts = append(parts, planThenExecuteSubmitPlanVisibleJSONInstruction())
	}

	return strings.Join(parts, " ")
}

func planThenExecuteNativeSubmitPlanCorrectionDetails(brief workerruntime.WorkerRuntimeBrief, result workspace.ToolResult) string {
	var parts []string

	if checklist := planThenExecuteSubmitPlanCorrectionChecklist(brief, result); checklist != "" {
		parts = append(parts, checklist)
	}

	if submitPlanRejectionHasLargeInsertAfterLine(result) {
		parts = append(parts, "do not repeat `insert_after_line` for the oversized/ref-backed edit. Use `start_line`/`end_line`/`content` for the large addition by replacing a small current-source anchor range with the unchanged anchor line(s) plus inserted content. `insert_after_line` must have small inline non-empty `content` and cannot use `content_ref`.")
	} else if submitPlanRejectionHasInvalidEditShape(result) {
		parts = append(parts, "Correct the invalid edit shape. "+planThenExecuteSubmitPlanCoverageSentence()+" Each edit must choose exactly one executable shape: line-range replacement, exact substring replacement, small inline insertion, or missing/new-file write. Existing source/test files must use bounded anchored edits. Do not combine shapes or use placeholders.")
	}

	return strings.Join(parts, " ")
}

func planThenExecuteSubmitPlanCorrectionChecklist(brief workerruntime.WorkerRuntimeBrief, result workspace.ToolResult) string {
	required := nonEmptyRunnerStrings(brief.RequiredEditPaths, 12)
	if len(required) == 0 {
		return ""
	}

	missing := nilIfEmptyRunnerPathSet(submitPlanMissingRequiredPaths(result))
	invalid := nilIfEmptyRunnerPathSet(submitPlanInvalidAnchorPaths(result))
	noop := nilIfEmptyRunnerPathSet(submitPlanNoOpEditPaths(result))
	unpathableNoop := submitPlanHasUnpathableNoOpEdit(result) && len(noop) == 0

	var b strings.Builder

	b.WriteString("Next submit_plan coverage checklist:")

	for _, rawPath := range required {
		path := NormalizeRunnerPath(rawPath)
		if path == "" {
			continue
		}

		b.WriteString(" ")
		b.WriteString(path)
		b.WriteString("=")

		switch {
		case noop != nil:
			if _, ok := noop[path]; ok {
				b.WriteString("use-source_satisfied_paths-if-current-state-is-final-or-submit-real-edit")
			} else if _, ok := invalid[path]; ok {
				b.WriteString("invalid-anchor-or-syntax")
			} else if missing != nil {
				if _, ok := missing[path]; ok {
					b.WriteString("missing-entry")
				} else {
					b.WriteString("must-keep-valid-coverage")
				}
			} else {
				b.WriteString("must-keep-valid-coverage")
			}
		case unpathableNoop:
			b.WriteString("must-have-real-edit-or-source_satisfied_paths-entry-no-noop")
		case missing != nil:
			if _, ok := missing[path]; ok {
				b.WriteString("missing-entry")
			} else if _, ok := invalid[path]; ok {
				b.WriteString("invalid-anchor-or-syntax")
			} else {
				b.WriteString("must-keep-valid-coverage")
			}
		case invalid != nil:
			if _, ok := invalid[path]; ok {
				b.WriteString("invalid-anchor-or-syntax")
			} else {
				b.WriteString("must-keep-valid-coverage")
			}
		default:
			b.WriteString("must-have-valid-edit-or-source_satisfied_paths-entry")
		}

		b.WriteString(";")
	}

	return strings.TrimSpace(b.String())
}

func nilIfEmptyRunnerPathSet(paths map[string]struct{}) map[string]struct{} {
	if len(paths) == 0 {
		return nil
	}

	return paths
}

func submitPlanNoOpEditPaths(result workspace.ToolResult) map[string]struct{} {
	paths := make(map[string]struct{})
	for _, diagnostic := range submitPlanDiagnostics(result) {
		if diagnostic.Kind == toolfacts.SubmitPlanDiagnosticNoOpEdit && diagnostic.CanonicalID != "" {
			paths[diagnostic.CanonicalID] = struct{}{}
		}
	}
	if len(paths) == 0 {
		return nil
	}

	return paths
}

func submitPlanHasUnpathableNoOpEdit(result workspace.ToolResult) bool {
	for _, diagnostic := range submitPlanDiagnostics(result) {
		if diagnostic.Kind == toolfacts.SubmitPlanDiagnosticNoOpEdit && diagnostic.CanonicalID == "" {
			return true
		}
	}
	return false
}

func planThenExecuteUnreadRequiredSourceNudge(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, authority planThenExecutePathAuthority) string {
	if strings.TrimSpace(brief.TaskFamily) != PlanThenExecutePlannerTaskFamily {
		return ""
	}

	if len(brief.RequiredEditPaths) == 0 || successfulSubmitPlanResultCount(results) > 0 {
		return ""
	}

	pending := planThenExecuteUnreadRequiredPathList(brief, results, authority)
	if len(pending) == 0 {
		return ""
	}

	projection := newToolResultEvidenceProjection(results)
	lines := make([]string, 0, len(pending))
	for _, path := range pending {
		evidence := projection.SourceEvidenceForPath(path)
		state := string(evidence.Level)
		if state == "" {
			state = "none"
		}
		if detail := strings.TrimSpace(evidence.Detail); detail != "" {
			state += " (" + detail + ")"
		}
		lines = append(lines, "- "+path+": "+state)
	}

	return strings.Join([]string{
		"REQUIRED SOURCE EVIDENCE PENDING: required_edit_paths still lack current source-body evidence.",
		"",
		"Pending paths and current evidence state:",
		strings.Join(lines, "\n"),
		"",
		"Source-window tools are available to establish body evidence. Choose any permission-authorized inspection that resolves a concrete missing edit-safety fact; no particular path or inspection strategy is required next.",
		"",
		"Outline and search hits do not satisfy the source-body evidence contract by themselves. A submit plan becomes evidence-complete when every existing required path has a current source window or typed blocker, while authorized-new paths use canonical scope authority.",
	}, "\n")
}

func planThenExecuteUnreadRequiredPathList(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, authority planThenExecutePathAuthority) []string {
	unread := planThenExecuteUnreadRequiredPaths(brief, results, authority)
	if len(unread) == 0 {
		return nil
	}

	paths := make([]string, 0, len(unread))
	seen := make(map[string]struct{}, len(unread))
	for _, rawPath := range authority.requiredCoveragePaths(brief) {
		path := NormalizeRunnerPath(rawPath)
		if _, ok := unread[path]; !ok || path == "" {
			continue
		}
		paths = append(paths, path)
		seen[path] = struct{}{}
	}

	var remaining []string
	for path := range unread {
		if _, ok := seen[path]; !ok {
			remaining = append(remaining, path)
		}
	}
	sort.Strings(remaining)
	return append(paths, remaining...)
}

func planThenExecuteAnchorReadyPaths(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, authority planThenExecutePathAuthority) map[string]struct{} {
	paths := make(map[string]struct{})
	projection := newToolResultEvidenceProjection(results)

	for _, path := range authority.requiredCoveragePaths(brief) {
		if projection.SourceEvidenceForPath(path).Level == planThenExecuteEvidenceAnchorReady {
			paths[path] = struct{}{}
		}
	}

	if len(paths) > 0 {
		return paths
	}

	for path := range projection.ReadPaths() {
		if projection.SourceEvidenceForPath(path).Level == planThenExecuteEvidenceAnchorReady {
			paths[path] = struct{}{}
		}
	}

	return paths
}

type planThenExecuteReadRange struct {
	start int
	end   int
	total int
}

func readRangesForPathFromResults(results []workspace.ToolResult, target string) []planThenExecuteReadRange {
	target = NormalizeRunnerPath(target)
	if target == "" {
		return nil
	}

	ranges := []planThenExecuteReadRange{}

	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" {
			continue
		}

		for _, window := range result.SourceWindows() {
			fact := window.Evidence
			if NormalizeRunnerPath(fact.CanonicalID) != target {
				continue
			}

			if readRange, ok := sourceWindowReadRange(fact); ok {
				ranges = append(ranges, readRange)
			}
		}
	}

	return ranges
}

const planThenExecuteRejectedSubmitPlanStreakThreshold = 3

func completionGateRecoveryChatOptionsForBackend(opts llama.ChatOptions, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, supportsNativeTools bool, authority planThenExecutePathAuthority, completion implementationCompletionState) llama.ChatOptions {
	if briefIsScratchpadDeliverableLane(brief) {
		if opts.MaxTokens <= 0 {
			opts.MaxTokens = 16384
		}

		return opts
	}

	if strings.TrimSpace(brief.TaskFamily) == PlanThenExecuteReviewRepairTaskFamily {
		opts = forceVisibleActionChatOptions(opts)
		if opts.MaxTokens <= 0 {
			opts.MaxTokens = 8192
		}

		return opts
	}

	if planThenExecutePlannerBrief(brief) {
		surface := derivePlanExecuteRecoverySurface(brief, results, PlanExecutePlanningTransport{SupportsNativeTools: supportsNativeTools, TextualToolParser: !supportsNativeTools}, authority)
		if surface.Kind == PlanExecuteRecoverySurfaceAnchorRefresh || surface.Kind == PlanExecuteRecoverySurfaceHistoryPending {
			return planThenExecutePostReadinessRecoveryChatOptionsForBackend(opts, brief, results, supportsNativeTools, authority)
		}

		return planThenExecuteCandidatePostReadChatOptionsForBackend(opts, brief, results, supportsNativeTools, authority)
	}

	if strings.TrimSpace(brief.TaskFamily) != PlanThenExecutePlannerTaskFamily &&
		runnerBriefRequiresMutation(brief) &&
		len(brief.RequiredEditPaths) == 0 &&
		len(completion.ChangedPaths) == 0 &&
		len(newToolResultEvidenceProjection(results).SourceWindowPaths()) > 0 &&
		mutationNoWriteCompletionGateRejectionCount(results) >= 2 {

		opts = forceVisibleActionChatOptions(opts)
		opts = planExecuteSetExactMaxTokens(opts, 768)
		opts.ToolChoice = planThenExecuteTerminalAwareToolChoice(opts.ToolChoice)

		return opts
	}

	if runnerBriefRequiresMutation(brief) && len(brief.RequiredEditPaths) > 0 && len(completion.MissingRequiredPaths) > 0 {

		opts = implementationRequiredPathForceVisibleActionChatOptions(opts)
		if !supportsNativeTools && implementationRequiredPathAnchorsReady(brief, results, completion.ChangedPaths) {
			opts = forceVisibleActionChatOptions(opts)
		}

		limit := implementationRequiredPathRecoveryTokenLimit(brief, results)

		opts = planExecuteSetExactMaxTokens(opts, limit)

		return opts
	}

	limit := 1536
	if runnerBriefRequiresMutation(brief) {
		limit = 1024
		if len(brief.RequiredEditPaths) == 0 {
			limit = 2048
		}

		if len(brief.RequiredEditPaths) == 0 && lastNonGateTool(results) == workspace.ToolNameReadFile && successfulToolSeen(results, workspace.ToolNameReadFile) {
			// Keep the model's current thinking capability available for
			// the recovery response, but strip prior hidden reasoning from
			// the next request. For local Qwen backends, forcing thinking
			// off here can produce an empty visible response; preserving
			// current-response reasoning still lets parser-readable hidden
			// tool calls be salvaged.
			opts.PreserveThinking = false
			limit = 1024
		} else if len(brief.RequiredEditPaths) == 0 && textutil.EqualTrimFold(opts.ToolChoice, "required") {
			opts.ToolChoice = "auto"
		}
	}

	if opts.MaxTokens <= 0 || opts.MaxTokens > limit {
		opts.MaxTokens = limit
	}

	return opts
}

func implementationRequiredPathForceVisibleActionChatOptions(opts llama.ChatOptions) llama.ChatOptions {
	opts.PreserveThinking = false
	opts.ToolChoice = planThenExecuteTerminalAwareToolChoice(opts.ToolChoice)

	return opts
}

func implementationRequiredPathAnchorsReady(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, committedPaths []string) bool {
	if len(brief.RequiredEditPaths) == 0 {
		return false
	}

	projection := newToolResultEvidenceProjection(results)
	changed := normalizedRequiredEditPathSet(committedPaths)

	for _, path := range brief.RequiredEditPaths {
		normalized := NormalizeRunnerPath(path)
		if normalized == "" {
			continue
		}

		if _, ok := changed[normalized]; ok {
			continue
		}

		evidence := projection.SourceEvidenceForPath(normalized)
		if evidence.Level != planThenExecuteEvidenceAnchorReady {
			return false
		}
	}

	return true
}

func implementationRequiredPathRecoveryTokenLimit(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) int {
	if implementationRequiredPathRecoveryNeedsLargeBodyBudget(brief, results) {
		return 16384
	}

	return 4096
}

func implementationRequiredPathRecoveryNeedsLargeBodyBudget(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	var resultText strings.Builder

	for _, result := range results {
		if strings.TrimSpace(result.Tool) == completionGateToolName {
			continue
		}

		resultText.WriteString(result.Summary)
		resultText.WriteByte('\n')
		resultText.WriteString(result.Error)
		resultText.WriteByte('\n')
		resultText.WriteString(result.Output)
		resultText.WriteByte('\n')
	}

	return recoveryNeedsLargeBodyBudgetText(brief, resultText.String())
}

func planThenExecuteCandidatePostReadChatOptionsForBackend(opts llama.ChatOptions, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, supportsNativeTools bool, authority planThenExecutePathAuthority) llama.ChatOptions {
	if !planThenExecutePlannerBrief(brief) {
		return opts
	}

	if planThenExecuteUnreadRequiredSourceNudge(brief, results, authority) != "" {
		return planThenExecuteUnreadRequiredSourceChatOptions(opts, brief, results, authority)
	}

	readEvidenceSeen := successfulToolSeen(results, workspace.ToolNameReadFile) || lastNonGateTool(results) == workspace.ToolNameReadFile || len(newToolResultEvidenceProjection(results).ReadPaths()) > 0
	if len(brief.RequiredEditPaths) > 0 {
		if !readEvidenceSeen {
			if opts.MaxTokens <= 0 {
				opts.MaxTokens = 16384
			}

			return opts
		}

		if !planThenExecuteTerminalSubmitPlanAllowed(brief, results, authority) {
			opts = forceVisibleActionChatOptions(opts)
			opts.ToolChoice = planThenExecuteTerminalAwareToolChoice(opts.ToolChoice)

			return planExecuteCapMaxTokens(opts, 4096)
		}

		limit := planThenExecuteRecoveryTokenLimit(brief, results)

		if planThenExecuteRecoveryNeedsLargeBodyBudget(brief, results) {
			limit = 16384
		}

		if opts.MaxTokens <= 0 || opts.MaxTokens < limit || opts.MaxTokens > limit {
			opts.MaxTokens = limit
		}

		return opts
	}

	if readEvidenceSeen {
		opts = forceVisibleActionChatOptions(opts)
		if !planThenExecuteTerminalSubmitPlanAllowed(brief, results, authority) {
			opts.ToolChoice = planThenExecuteTerminalAwareToolChoice(opts.ToolChoice)
		}
	}

	opts = planExecuteCapMaxTokens(opts, 4096)

	return opts
}

func planThenExecuteRecoveryNeedsLargeBodyBudget(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	return recoveryNeedsLargeBodyBudgetText(brief, planThenExecuteRecoveryResultText(results))
}

func recoveryNeedsLargeBodyBudgetText(brief workerruntime.WorkerRuntimeBrief, resultText string) bool {
	text := strings.ToLower(strings.Join([]string{
		brief.Objective,
		strings.Join(brief.RequiredEditPaths, "\n"),
		resultText,
	}, "\n"))
	for _, phrase := range []string{"create new file", "add new file", "write new file", "whole-file", "replace_all", "full file body", "new file", "missing parent"} {
		if strings.Contains(text, phrase) {
			return true
		}
	}

	for _, path := range brief.RequiredEditPaths {
		lowerPath := strings.ToLower(filepath.ToSlash(strings.TrimSpace(path)))
		if lowerPath == "" {
			continue
		}

		base := filepath.Base(lowerPath)
		if strings.Contains(lowerPath, "/new/") || strings.HasPrefix(base, "new_") {
			return true
		}

		if strings.Contains(text, lowerPath) &&
			(strings.Contains(text, " create ") || strings.HasPrefix(text, "create ") ||
				strings.Contains(text, " add ") || strings.HasPrefix(text, "add ") ||
				strings.Contains(text, " write ") || strings.HasPrefix(text, "write ")) {

			return true
		}
	}

	return false
}

func planThenExecuteRecoveryResultText(results []workspace.ToolResult) string {
	var b strings.Builder

	for _, result := range results {
		b.WriteString(result.Summary)
		b.WriteByte('\n')
		b.WriteString(result.Error)
		b.WriteByte('\n')
		b.WriteString(result.Output)
		b.WriteByte('\n')
	}

	return b.String()
}

func (r *runExecution) planThenExecuteSubmitPlanParseRecoveryPlan(raw string, parseErr error, results []workspace.ToolResult) (PlanExecuteRecoveryPlan, bool) {
	if r == nil || !r.planThenExecutePlannerPhase() || parseErr == nil {
		return PlanExecuteRecoveryPlan{}, false
	}

	stringifiedEdits := submitPlanEditsStringParseError(raw, parseErr)
	if !stringifiedEdits && !planThenExecuteSubmitPlanOnlyReady(r.state.runtimeBrief, results, r.planThenExecutePathAuthority()) {
		return PlanExecuteRecoveryPlan{}, false
	}

	if planThenExecuteReadyToSubmitNudge(r.state.runtimeBrief, results, r.planThenExecutePathAuthority()) == "" {
		return PlanExecuteRecoveryPlan{}, false
	}

	required := nonEmptyRunnerStrings(r.state.runtimeBrief.RequiredEditPaths, 12)

	return planThenExecuteSubmitPlanParseRecoveryPlan(stringifiedEdits, required), true
}

func submitPlanEditsStringParseError(raw string, parseErr error) bool {
	if parseErr == nil {
		return false
	}

	lower := strings.ToLower(parseErr.Error() + "\n" + raw)

	return strings.Contains(lower, "toolrequest.edits") && strings.Contains(lower, "string")
}

func (r *runExecution) emitPlanExecuteSubmitPlanParseRecoveryEvent(plan PlanExecuteRecoveryPlan, content string) {
	if r == nil || plan.Kind != PlanExecuteRecoveryKindSubmitPlanParse {
		return
	}

	r.emitEvent(RunEvent{Role: "plan_then_execute_submit_plan_parse_recovery", Content: strings.TrimSpace(content)})
}

func (r *runExecution) emitPlanExecuteSubmitPlanParseRecoveryEventForParseFailure(raw string, parseErr error) {
	if r == nil || !r.planThenExecutePlannerPhase() || !submitPlanEditsStringParseError(raw, parseErr) {
		return
	}

	r.emitEvent(RunEvent{
		Role:    "plan_then_execute_submit_plan_parse_recovery",
		Content: "submit_plan parse recovery observed: edits was emitted as a quoted non-array string and did not count as a plan artifact",
	})
}

func planThenExecuteNativeSubmitPlanNudge(brief workerruntime.WorkerRuntimeBrief) string {
	required := nonEmptyRunnerStrings(brief.RequiredEditPaths, 12)

	var b strings.Builder

	b.WriteString("PLANNER READINESS REACHED: every required_edit_path has anchor-ready source evidence in this turn.\n\n")
	b.WriteString("Call native `submit_plan` when the gathered evidence is sufficient. Permission-authorized read/search tools remain callable for a fresh model-selected request. ")
	b.WriteString(planThenExecuteSubmitPlanCoverageSentence())
	b.WriteString(" Do not answer in prose.")

	if len(required) > 0 {
		b.WriteString("\n\nRequired edit paths: ")
		b.WriteString(strings.Join(required, ", "))
		b.WriteString(".")
	}

	return b.String()
}

func planThenExecuteNativeSubmitPlanCorrectionPrompt(brief workerruntime.WorkerRuntimeBrief, result workspace.ToolResult) string {
	return RenderPlanExecuteRecoveryPlan(planThenExecuteNativeSubmitPlanCorrectionRecoveryPlan(brief, result))
}

func (r *runExecution) planThenExecuteNativeSubmitPlanNoCallFallbackPrompt(modelErr error, opts llama.ChatOptions, results []workspace.ToolResult, used int) (string, bool) {
	if r == nil || r.runner.Client == nil || modelErr == nil || used > 0 || !r.planThenExecutePlannerPhase() {
		return "", false
	}

	if !planThenExecuteTerminalSubmitPlanAllowed(r.state.runtimeBrief, results, r.planThenExecutePathAuthority()) {
		return "", false
	}

	detail := strings.TrimSpace(modelErr.Error())
	lower := strings.ToLower(detail)

	if !strings.Contains(lower, "tool_choice") || !strings.Contains(lower, "required") || !strings.Contains(lower, "did not produce any tool calls") {
		return "", false
	}

	required := nonEmptyRunnerStrings(r.state.runtimeBrief.RequiredEditPaths, 12)

	return RenderPlanExecuteRecoveryPlan(planThenExecuteNativeNoCallFallbackRecoveryPlan(required)), true
}

func planThenExecuteReadyToSubmitChatOptionsForBackend(opts llama.ChatOptions, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, supportsNativeTools bool, authority planThenExecutePathAuthority) llama.ChatOptions {
	surface := derivePlanExecuteRecoverySurface(brief, results, PlanExecutePlanningTransport{SupportsNativeTools: supportsNativeTools, TextualToolParser: !supportsNativeTools}, authority)
	switch surface.Kind {
	case PlanExecuteRecoverySurfaceAnchorRefresh, PlanExecuteRecoverySurfaceHistoryPending:
		return planThenExecutePostReadinessRecoveryChatOptionsForBackend(opts, brief, results, supportsNativeTools, authority)
	case PlanExecuteRecoverySurfaceSourceSatisfiedTerminal:
		return planThenExecuteSourceSatisfiedTerminalChatOptions(opts, brief, results)
	}

	if planThenExecuteReadyToSubmitNudge(brief, results, authority) == "" {
		return planThenExecuteCandidatePostReadChatOptionsForBackend(opts, brief, results, supportsNativeTools, authority)
	}

	if len(brief.RequiredEditPaths) == 0 && !planThenExecuteTerminalSubmitPlanAllowed(brief, results, authority) {
		return planThenExecuteCandidatePostReadChatOptionsForBackend(opts, brief, results, supportsNativeTools, authority)
	}

	limit := planThenExecuteRecoveryTokenLimit(brief, results)

	opts = planExecuteSetExactMaxTokens(opts, limit)

	return opts
}

func planThenExecuteSourceSatisfiedTerminalChatOptions(opts llama.ChatOptions, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) llama.ChatOptions {
	opts = forceVisibleActionChatOptions(opts)
	opts.ResponseFormatJSON = false
	if len(opts.Tools) > 0 {
		opts.ToolChoice = planThenExecuteTerminalAwareToolChoice(opts.ToolChoice)
	}

	limit := planThenExecuteRecoveryTokenLimit(brief, results)

	opts = planExecuteSetExactMaxTokens(opts, limit)

	return opts
}

func planThenExecuteSourceSatisfiedTerminalRequiredActive(results []workspace.ToolResult) bool {
	for i := len(results) - 1; i >= 0; i-- {
		result := results[i]
		if strings.TrimSpace(result.Tool) != workspace.ToolNameSubmitPlan {
			continue
		}

		if strings.TrimSpace(result.Error) == "" {
			return false
		}

		return planThenExecuteSourceSatisfiedSubmitPlanRejectedResult(result)
	}

	return false
}

func planThenExecuteTextualSubmitPlanFallbackChatOptions(opts llama.ChatOptions, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) llama.ChatOptions {
	opts.ForceNativeTools = false
	opts = planThenExecuteSubmitPlanTerminalChatOptions(opts, false)

	limit := planThenExecuteRecoveryTokenLimit(brief, results)

	opts = planExecuteSetExactMaxTokens(opts, limit)

	return opts
}

func planThenExecuteUnreadRequiredSourceChatOptions(opts llama.ChatOptions, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, authority planThenExecutePathAuthority) llama.ChatOptions {
	if planThenExecuteUnreadRequiredSourceNudge(brief, results, authority) == "" {
		return opts
	}

	opts = forceVisibleActionChatOptions(opts)
	opts.ToolChoice = planThenExecuteTerminalAwareToolChoice(opts.ToolChoice)

	opts = planExecuteCapMaxTokens(opts, 768)

	return opts
}

func planThenExecuteCandidateWriteOnlyRecoveryChatOptionsForBackend(opts llama.ChatOptions, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, supportsNativeTools bool) llama.ChatOptions {
	if !planThenExecutePlannerBrief(brief) || len(brief.RequiredEditPaths) > 0 {
		return opts
	}

	if successfulSubmitPlanResultCount(results) > 0 {
		return opts
	}

	if len(newToolResultEvidenceProjection(results).ReadPaths()) == 0 && !successfulToolSeen(results, workspace.ToolNameReadFile) && lastNonGateTool(results) != workspace.ToolNameReadFile {
		return opts
	}

	if supportsNativeTools && !chatOptionsExposeTool(opts, workspace.ToolNameSubmitPlan) {
		return opts
	}

	opts = planThenExecuteSubmitPlanTerminalChatOptions(opts, supportsNativeTools)
	if supportsNativeTools {
		opts.ToolChoice = planThenExecuteTerminalAwareToolChoice(opts.ToolChoice)
	}

	limit := planThenExecuteRecoveryTokenLimit(brief, results)

	opts = planExecuteSetExactMaxTokens(opts, limit)

	return opts
}

func planThenExecuteRecoveryTokenLimit(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) int {
	requiredPathCount := len(normalizedRequiredEditPathSet(brief.RequiredEditPaths))
	if planThenExecuteRejectedPlanCorrectionOnly(brief, results) {
		if planThenExecuteRecoveryNeedsLargeBodyBudget(brief, results) {
			return 16384
		}

		if requiredPathCount == 0 {
			return 16384
		}

		// Multi-file textual submit_plan output serializes full edit
		// bodies into one JSON object. Four-path source+test plans have
		// hit the 8192-token ceiling mid-JSON, so keep the larger budget
		// scoped to terminal plan synthesis/correction.
		if requiredPathCount >= 4 {
			return 16384
		}

		if requiredPathCount >= 3 {
			return 8192
		}

		return 1024
	}

	if planThenExecuteRecoveryNeedsLargeBodyBudget(brief, results) {
		return 16384
	}

	if lastNonGateTool(results) == workspace.ToolNameSubmitPlan {
		return 4096
	}

	if requiredPathCount >= 4 {
		return 16384
	}

	if requiredPathCount >= 3 {
		return 8192
	}

	return 4096
}

func planThenExecuteRejectedPlanCorrectionOnly(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if strings.TrimSpace(brief.TaskFamily) != PlanThenExecutePlannerTaskFamily {
		return false
	}

	if _, _, ok := lastRejectedSubmitPlanCoverageResult(results); !ok {
		return false
	}

	if planThenExecuteSourceSatisfiedRejectionAfterCoverageRejection(results) {
		return false
	}

	if planThenExecuteUnscopedEmptySubmitPlanRejected(brief, results) {
		return false
	}

	return !planThenExecuteRejectedPlanNeedsSourceRefresh(brief, results)
}

func planThenExecuteSourceSatisfiedRejectionAfterCoverageRejection(results []workspace.ToolResult) bool {
	lastCoverage := -1
	lastSourceSatisfied := -1

	for i, result := range results {
		if planThenExecuteSourceSatisfiedSubmitPlanRejectedResult(result) {
			lastSourceSatisfied = i
			continue
		}

		if planThenExecuteSubmitPlanRejectedResult(result) {
			lastCoverage = i
		}
	}

	return lastCoverage >= 0 && lastSourceSatisfied > lastCoverage
}

func planThenExecutePostReadinessRecoveryChatOptionsForBackend(opts llama.ChatOptions, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, supportsNativeTools bool, authority planThenExecutePathAuthority) llama.ChatOptions {
	if planThenExecuteUnscopedEmptySubmitPlanRejected(brief, results) {
		return planThenExecuteUnscopedEmptySubmitPlanRecoveryChatOptions(opts)
	}

	surface := derivePlanExecuteRecoverySurface(brief, results, PlanExecutePlanningTransport{SupportsNativeTools: supportsNativeTools, TextualToolParser: !supportsNativeTools}, authority)
	if surface.Kind == PlanExecuteRecoverySurfaceAnchorRefresh {
		opts = forceVisibleActionChatOptions(opts)
		opts.ToolChoice = planThenExecuteTerminalAwareToolChoice(opts.ToolChoice)

		opts = planExecuteSetExactMaxTokens(opts, planThenExecuteAnchorRefreshMaxTokens)

		return opts
	}

	if surface.Kind == PlanExecuteRecoverySurfaceHistoryPending {
		opts = forceVisibleActionChatOptions(opts)
		opts.ToolChoice = planThenExecuteTerminalAwareToolChoice(opts.ToolChoice)

		opts = planExecuteSetExactMaxTokens(opts, 1024)

		return opts
	}

	if surface.Kind == PlanExecuteRecoverySurfaceSourceSatisfiedTerminal {
		return planThenExecuteSourceSatisfiedTerminalChatOptions(opts, brief, results)
	}

	if len(authority.requiredCoveragePaths(brief)) == 0 {
		return planThenExecuteCandidateWriteOnlyRecoveryChatOptionsForBackend(opts, brief, results, supportsNativeTools)
	}

	return planThenExecuteRequiredWriteOnlyRecoveryChatOptionsForBackend(opts, brief, results, supportsNativeTools)
}

func planThenExecuteUnscopedEmptySubmitPlanRecoveryChatOptions(opts llama.ChatOptions) llama.ChatOptions {
	opts.ForceNativeTools = false
	opts.ResponseFormatJSON = false
	opts.EnableThinking = nil
	opts.ChatTemplateKwargs = false
	opts.PreserveThinking = false
	opts.ToolChoice = planThenExecuteTerminalAwareToolChoice(opts.ToolChoice)
	opts = planExecuteCapMaxTokens(opts, 4096)

	return opts
}

func planThenExecuteRejectedPlanNeedsSourceRefresh(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	rejected, rejectedAt, ok := lastRejectedSubmitPlanCoverageResult(results)
	if !ok {
		return false
	}

	return len(planThenExecuteRejectedPlanRefreshPaths(brief, results, rejected, rejectedAt)) > 0
}

func planThenExecuteRejectedPlanRefreshPaths(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, rejected workspace.ToolResult, rejectedAt int) []string {
	if paths := planThenExecuteRejectedPlanRefreshPathsForResult(brief, results, rejected, rejectedAt); len(paths) > 0 {
		return paths
	}

	if planThenExecuteSuppressRejectedSubmitPlanStreakRefresh(brief, rejected, results) {
		return nil
	}

	return planThenExecuteRejectedSubmitPlanStreakRefreshPaths(brief, results)
}

func planThenExecuteSuppressRejectedSubmitPlanStreakRefresh(brief workerruntime.WorkerRuntimeBrief, rejected workspace.ToolResult, results []workspace.ToolResult) bool {
	if !planThenExecutePathlessUnsafeSubmitPlanRejectionExhausted(rejected, results) {
		return false
	}

	return len(planThenExecuteRejectedSubmitPlanStreakRefreshPaths(brief, results)) == 0
}

func planThenExecutePathlessUnsafeSubmitPlanRejectionExhausted(rejected workspace.ToolResult, results []workspace.ToolResult) bool {
	if len(submitPlanRefreshPathCandidates(rejected)) > 0 {
		return false
	}
	if !planThenExecuteSubmitPlanMissingBodyOrUnsafeAnchor(rejected) {
		return false
	}

	return len(planThenExecuteRejectedSubmitPlanStreak(results)) >= planThenExecuteRejectedSubmitPlanStreakThreshold
}

func planThenExecuteLastRejectionSuppressesStreakRefresh(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	rejected, _, ok := lastRejectedSubmitPlanCoverageResult(results)
	if !ok {
		return false
	}

	return planThenExecuteSuppressRejectedSubmitPlanStreakRefresh(brief, rejected, results)
}

func planThenExecuteRejectedPlanRefreshPathsForResult(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, rejected workspace.ToolResult, rejectedAt int) []string {
	refreshPaths := submitPlanRefreshPathCandidates(rejected)

	var out []string

	for _, rawPath := range planThenExecuteRefreshScopePathsForRejectedPlan(brief, results, refreshPaths) {
		path := NormalizeRunnerPath(rawPath)
		if path == "" {
			continue
		}

		if _, ok := refreshPaths[path]; !ok {
			continue
		}

		if _, invalid := submitPlanInvalidAnchorPaths(rejected)[path]; invalid {
			if successfulInvalidAnchorCorrectionEvidenceForRejection(results, rejectedAt, path) {
				continue
			}
		} else if successfulSourceEvidencePathAfter(results, rejectedAt, path) {
			continue
		}

		out = append(out, path)
	}

	return out
}

func submitPlanRefreshPathCandidates(rejected workspace.ToolResult) map[string]struct{} {
	refreshPaths := submitPlanInvalidAnchorPaths(rejected)
	for path := range submitPlanMissingSourceGroundingPaths(rejected) {
		refreshPaths[path] = struct{}{}
	}

	return refreshPaths
}

func planThenExecuteRejectedSubmitPlanStreak(results []workspace.ToolResult) []int {
	var indices []int

	for i := len(results) - 1; i >= 0; i-- {
		result := results[i]
		if strings.TrimSpace(result.Tool) != workspace.ToolNameSubmitPlan {
			continue
		}

		if strings.TrimSpace(result.Error) == "" {
			break
		}

		if planThenExecuteSourceSatisfiedSubmitPlanRejectedResult(result) {
			continue
		}

		if !planThenExecuteSubmitPlanRejectedResult(result) {
			continue
		}

		indices = append(indices, i)
	}

	slices.Reverse(indices)

	return indices
}

func planThenExecuteRejectedSubmitPlanStreakInvalidPaths(results []workspace.ToolResult) map[string]struct{} {
	out := map[string]struct{}{}
	streak := planThenExecuteRejectedSubmitPlanStreak(results)
	if len(streak) < planThenExecuteRejectedSubmitPlanStreakThreshold {
		return out
	}

	for _, index := range streak {
		for path := range submitPlanRefreshPathCandidates(results[index]) {
			if normalized := NormalizeRunnerPath(path); normalized != "" {
				out[normalized] = struct{}{}
			}
		}
	}

	return out
}

func planThenExecuteRejectedSubmitPlanStreakRefreshPaths(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) []string {
	streak := planThenExecuteRejectedSubmitPlanStreak(results)
	if len(streak) < planThenExecuteRejectedSubmitPlanStreakThreshold {
		return nil
	}

	pathSet := map[string]struct{}{}
	for _, index := range streak {
		rejected := results[index]
		for _, path := range planThenExecuteRejectedPlanRefreshPathsForResult(brief, results, rejected, index) {
			if normalized := NormalizeRunnerPath(path); normalized != "" {
				pathSet[normalized] = struct{}{}
			}
		}
	}

	return sortedRunnerPathKeys(pathSet)
}

func planThenExecuteRefreshScopePaths(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) []string {
	required := planThenExecuteRequiredCoveragePaths(brief.RequiredEditPaths, "")
	if len(required) > 0 {
		return required
	}

	return candidateRecoveryScopePaths(brief, results, 16)
}

func planThenExecuteRefreshScopePathsForRejectedPlan(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, refreshPaths map[string]struct{}) []string {
	paths := planThenExecuteRefreshScopePaths(brief, results)
	if len(planThenExecuteRequiredCoveragePaths(brief.RequiredEditPaths, "")) > 0 || len(refreshPaths) == 0 {
		return paths
	}

	seen := normalizedRequiredEditPathSet(paths)

	for path := range refreshPaths {
		normalized := NormalizeRunnerPath(path)
		if !planThenExecuteRecoveryScopePathAllowed(normalized) {
			continue
		}

		if _, ok := seen[normalized]; ok {
			continue
		}

		seen[normalized] = struct{}{}
		paths = append(paths, normalized)
	}

	return paths
}

func planThenExecuteAnchorRefreshAfterRejectedSubmitPlanPrompt(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, rejected workspace.ToolResult, rejectedAt int) string {
	refreshPaths := planThenExecuteRejectedPlanRefreshPaths(brief, results, rejected, rejectedAt)
	target := "the stale or missing required edit path named by the submit_plan rejection"

	if len(refreshPaths) > 0 {
		target = strings.Join(refreshPaths, ", ")
	}

	return strings.Join([]string{
		"The previous `submit_plan` was rejected before it became a valid plan artifact. " + lastSubmitPlanRejectionBrief(rejected),
		"The corrected plan still needs fresh source evidence for: " + target + ".",
		"Permission-authorized inspection remains callable for any concrete missing anchor fact on those paths. " + planThenExecuteAnchorRefreshReadFallbackInstruction(refreshPaths),
		"For large Go/source files, symbol/body evidence is usually stronger than a generic raw window when the invalid anchor names a declaration.",
		"A corrected `submit_plan`, source-satisfied decision, or exact blocker is appropriate once the remaining anchor facts are resolved.",
	}, "\n\n")
}

func planThenExecuteRequiredWriteOnlyRecoveryChatOptionsForBackend(opts llama.ChatOptions, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, supportsNativeTools bool) llama.ChatOptions {
	if !planThenExecutePlannerBrief(brief) || len(brief.RequiredEditPaths) == 0 {
		return opts
	}

	if successfulSubmitPlanResultCount(results) > 0 {
		return opts
	}

	if supportsNativeTools && !chatOptionsExposeTool(opts, workspace.ToolNameSubmitPlan) {
		return opts
	}

	opts = planThenExecuteSubmitPlanTerminalChatOptions(opts, supportsNativeTools)
	if supportsNativeTools {
		opts.ToolChoice = planThenExecuteTerminalAwareToolChoice(opts.ToolChoice)
	}

	limit := planThenExecuteRecoveryTokenLimit(brief, results)

	opts = planExecuteSetExactMaxTokens(opts, limit)

	return opts
}

func planThenExecuteSubmitPlanTerminalChatOptions(opts llama.ChatOptions, supportsNativeTools bool) llama.ChatOptions {
	opts = planExecuteSubmitPlanTerminalChatOptions(opts, supportsNativeTools)
	if supportsNativeTools && !chatOptionsExposeTool(opts, workspace.ToolNameSubmitPlan) {
		opts.Tools = nil
		opts.ToolChoice = ""
		opts.ForceNativeTools = false
		opts.ResponseFormatJSON = true
	}

	return opts
}

func planThenExecuteTerminalSubmitPlanAllowed(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, authority planThenExecutePathAuthority) bool {
	if planThenExecuteUnscopedEmptySubmitPlanRejected(brief, results) {
		return false
	}

	if planThenExecuteSubmitPlanHistoryMissing(brief, results, authority) {
		return false
	}

	state := derivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TerminalOnly: true}, authority)
	if !slices.Contains(state.TerminalOptions, workspace.ToolNameSubmitPlan) {
		return false
	}

	if state.Mode == PlanExecutePlanningModeSubmitReady || state.Mode == PlanExecutePlanningModeSubmitCorrection {
		return true
	}

	return len(state.SelectedEditPaths) > 0
}

func forceVisibleActionChatOptions(opts llama.ChatOptions) llama.ChatOptions {
	thinkingOff := false

	opts.EnableThinking = &thinkingOff
	opts.ChatTemplateKwargs = true
	opts.PreserveThinking = false

	return opts
}

func planThenExecuteTerminalAwareToolChoice(current string) string {
	if strings.TrimSpace(current) == "" || textutil.EqualTrimFold(current, "required") {
		return "auto"
	}

	return current
}

func chatOptionsExposeTool(opts llama.ChatOptions, name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}

	for _, tool := range opts.Tools {
		if strings.TrimSpace(tool.Function.Name) == name {
			return true
		}
	}

	return false
}

func sourcePrerequisiteRejection(summary string, errorText string, pendingPaths []string) runnerToolRejection {
	pending, _ := canonicalRunnerWorkspaceRelativePaths(pendingPaths)

	return runnerToolRejection{
		Summary:     strings.TrimSpace(summary),
		Error:       strings.TrimSpace(errorText),
		Disposition: toolfacts.ToolRejectionDeferredByPrerequisite,
		Facts: &toolfacts.ToolResultFacts{
			PendingSourceBodyPaths: pending,
			RejectionDisposition:   toolfacts.ToolRejectionDeferredByPrerequisite,
		},
	}
}

func planThenExecuteSubmitReadyParseFailureSeen(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, authority planThenExecutePathAuthority) bool {
	return planThenExecuteSubmitReadyParseFailureIndex(brief, results, authority) >= 0
}

func planThenExecuteSubmitReadyParseFailureIndex(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, authority planThenExecutePathAuthority) int {
	last := -1

	for i, result := range results {
		if strings.TrimSpace(result.Tool) != parserRecoveryToolName || strings.TrimSpace(result.Error) == "" {
			continue
		}

		if planThenExecuteSubmitPlanOnlyReady(brief, results[:i], authority) {
			last = i
		}
	}

	return last
}

func planThenExecuteSubmitCorrectionRecoveryPlanForResults(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, refreshNote string) PlanExecuteRecoveryPlan {
	rejected, _, _ := lastRejectedSubmitPlanCoverageResult(results)
	return planThenExecuteSubmitCorrectionRecoveryPlan(brief, rejected, refreshNote, planThenExecuteSubmitCorrectionBlockerAllowed(brief, results))
}
