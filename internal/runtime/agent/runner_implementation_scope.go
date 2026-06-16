package agent

import (
	"encoding/json"
	"fmt"
	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/pathparse"
	"idiom/internal/runtime/workerruntime"
	"path/filepath"
	"regexp"
	"strings"
)

func (r *Runner) implementationTargetWriteReadyNonWriteRecovery(req workspace.ToolRequest, results []workspace.ToolResult) (summary string, errorText string, prompt string, reject bool) {
	if r == nil || !implementationTargetWriteReadySurfaceActive(r.RuntimeBrief, results) || workspace.IsWorkspaceWriteTool(req.Tool) {
		return "", "", "", false
	}

	if pending := sourceEvidencePendingRedirectsForBrief(r.RuntimeBrief, results); len(pending) > 0 {
		pendingActionSummary := sourceEvidencePendingRedirectActionSummary(pending, 3)
		tool := strings.TrimSpace(req.Tool)

		if tool == "" {
			tool = "requested tool"
		}

		summary = "Implementation target write-ready pending source-body obligation rejected"
		errorText = fmt.Sprintf("%s is outside the pending redirected source-body obligation. The only valid read/navigation follow-up is the exact pending source-body request: %s. Otherwise write to a sourced target path or answer <<QUESTION>> with the exact blocker/no-op evidence.", tool, pendingActionSummary)
		prompt = "The implementation lane is target-write-ready, but a redirected source-body obligation is still pending: " + pendingActionSummary + ". Use only that exact pending source-body follow-up, make one parser-readable write from already gathered source evidence, or answer <<QUESTION>> with the exact blocker/no-op evidence. Do not use references, broad read, search, shell, or unrelated navigation."

		return summary, errorText, prompt, true
	}

	tool := strings.TrimSpace(req.Tool)
	if tool == "" {
		tool = "requested tool"
	}

	sourceSentence := "Source-body evidence is already available"
	if sourcedSummary := implementationTargetWriteReadySourcedPathSummary(results, 3); sourcedSummary != "" {
		sourceSentence += " for: " + sourcedSummary
	}

	summary = "Implementation target write-ready tool rejected"
	errorText = fmt.Sprintf("%s is outside the unscoped implementation target-write-ready surface. %s; the next valid action is a parser-readable write to a sourced target path, or <<QUESTION>> with the exact edit blocker/no-op evidence.", tool, sourceSentence)
	prompt = "The implementation lane is target-write-ready. " + sourceSentence + ". Do not inspect, search, or navigate that source again before the first write. Use a parser-readable write tool for the sourced target path, or answer <<QUESTION>> with the exact blocker/no-op evidence."

	return summary, errorText, prompt, true
}

func (r *Runner) writeDescriptionRepairSynthesizedRequest(req workspace.ToolRequest, repairActive bool) (workspace.ToolRequest, string, bool) {
	if r == nil || checkWriteDescription(req) == nil {
		return workspace.ToolRequest{}, "", false
	}

	if !runnerBriefRequiresMutation(r.RuntimeBrief) {
		return workspace.ToolRequest{}, "", false
	}

	if reasoningToolRequestHasPlaceholder(req) || !safeWriteDescriptionSynthesisShape(req) {
		return workspace.ToolRequest{}, "", false
	}

	if _, _, reject := requiredEditPathScopeRejection(req, r.RuntimeBrief); reject {
		return workspace.ToolRequest{}, "", false
	}

	paths := writeRequestPaths(req)
	if len(paths) == 0 {
		return workspace.ToolRequest{}, "", false
	}

	for _, path := range paths {
		if strings.TrimSpace(path) == "" {
			return workspace.ToolRequest{}, "", false
		}
	}

	repaired := req

	repaired.Description = synthesizedWriteDescription(req, paths)

	return repaired, marshalToolRequestRaw(repaired), true
}

func safeWriteDescriptionSynthesisShape(req workspace.ToolRequest) bool {
	if !safeWorkspaceWriteRequestShape(req) {
		return false
	}

	if strings.TrimSpace(req.Tool) != workspace.ToolNameEditFile {
		return true
	}

	if req.StartLine > 0 || req.EndLine > 0 {
		return req.StartLine > 0 && req.EndLine >= req.StartLine && strings.TrimSpace(req.Content) != ""
	}

	return strings.TrimSpace(req.OldText) != "" && strings.TrimSpace(req.NewText) != ""
}

func synthesizedWriteDescription(req workspace.ToolRequest, paths []string) string {
	tool := strings.TrimSpace(req.Tool)
	if tool == "" {
		tool = "write"
	}

	target := "selected path"
	if len(paths) > 0 {
		target = normalizeRunnerPath(paths[0])
		if len(paths) > 1 {
			target = fmt.Sprintf("%s and %d related path(s)", target, len(paths)-1)
		}
	}

	switch tool {
	case workspace.ToolNameEditFile:
		return fmt.Sprintf("Apply the requested edit to %s after adding the missing write description.", target)
	case workspace.ToolNameWriteFile:
		return fmt.Sprintf("Write the requested file content to %s after adding the missing write description.", target)
	case workspace.ToolNameAppendFile:
		return fmt.Sprintf("Append the requested content to %s after adding the missing write description.", target)
	case workspace.ToolNameApplyPatch:
		return fmt.Sprintf("Apply the requested patch to %s after adding the missing write description.", target)
	default:
		return fmt.Sprintf("Apply the requested workspace change to %s after adding the missing write description.", target)
	}
}

func (r *Runner) implementationTargetWriteReadySourceReadAllowed(req workspace.ToolRequest, results []workspace.ToolResult, refreshes int) bool {
	if r == nil || workspace.IsWorkspaceWriteTool(req.Tool) {
		return false
	}

	if !implementationTargetWriteReadySurfaceActive(r.RuntimeBrief, results) {
		return false
	}

	if implementationTargetWriteReadyPendingRedirectProgressAllowed(r.RuntimeBrief, req, results) {
		return true
	}

	if implementationTargetWriteReadyHasPendingSourceRedirect(r.RuntimeBrief, results) {
		return false
	}

	if r.implementationTargetWriteReadyHistoryReadAllowed(req, results) {
		return true
	}

	if implementationTargetWriteReadyTargetedDiscoveryAllowed(req, results, refreshes) {
		return true
	}

	path := normalizeRunnerPath(req.Path)
	if !implementationTargetWriteReadySourcePath(path) {
		return false
	}

	if strings.TrimSpace(req.Tool) != workspace.ToolNameReadFile {
		return false
	}

	if concreteSourceEvidenceReadForPath(path, results) {
		if implementationTargetWriteReadySamePathContinuationReadAllowed(req, results) {
			return true
		}

		if refreshes > 0 {
			return false
		}

		return !fullSourceReadFileSeen(path, results)
	}

	if refreshes >= implementationTargetWriteReadySourceReadAllowanceLimit {
		return false
	}

	return implementationTargetWriteReadyNewTargetRead(req)
}

func implementationTargetWriteReadyTargetedDiscoveryAllowed(req workspace.ToolRequest, results []workspace.ToolResult, refreshes int) bool {
	if refreshes >= implementationTargetWriteReadySourceReadAllowanceLimit {
		return false
	}

	switch strings.TrimSpace(req.Tool) {
	case workspace.ToolNameCodeNavigate:
		return implementationTargetWriteReadyTargetedCodeNavigate(req, results)
	case workspace.ToolNameSearchContent:
		return implementationTargetWriteReadyTargetedSearchContent(req, results)
	default:
		if implementationTargetWriteReadySourceSymbolTool(req.Tool) {
			return implementationTargetWriteReadyTargetedCodeNavigate(req, results)
		}

		return false
	}
}

func implementationTargetWriteReadySourceSymbolTool(tool string) bool {
	tool = strings.TrimSpace(tool)
	return strings.HasSuffix(tool, "_read_symbol") || strings.HasSuffix(tool, "_extract_body")
}

// targetWriteReadyStagedNudge builds a progressively stronger nudge message
// as the model accumulates non-write calls without making an edit. Early
// nudges are soft reminders; late stages require action before more discovery.
func targetWriteReadyStagedNudge(basePrompt string, req workspace.ToolRequest, nudgeCount int) string {
	tool := strings.TrimSpace(req.Tool)
	if tool == "" {
		tool = "requested tool"
	}

	suffix := fmt.Sprintf("\n\nYour %s request will execute. Nudge %d.", tool, nudgeCount)

	evidencePrefix := strings.TrimSpace(basePrompt)
	if evidencePrefix == "" {
		evidencePrefix = "Source evidence is available for sourced target path(s)."
	}

	if nudgeCount <= 6 {
		return fmt.Sprintf("%s Either write to a sourced target path, or name the one exact missing claim and one exact probe that would resolve it, then take that probe.%s", evidencePrefix, suffix)
	}

	return fmt.Sprintf("You have taken %d discovery calls without a write. %s Before more discovery: name the one exact missing claim and one exact probe that would resolve it. If you cannot name both, you have enough evidence — write to a sourced target path or answer <<QUESTION>> with the exact blocker.%s", nudgeCount, evidencePrefix, suffix)
}
func implementationTargetWriteReadyTargetedCodeNavigate(req workspace.ToolRequest, results []workspace.ToolResult) bool {
	path := normalizeRunnerPath(req.Path)
	if !implementationTargetWriteReadySourcePath(path) {
		return false
	}

	if !concreteSourceEvidenceReadForPath(path, results) {
		return false
	}

	capability := strings.TrimSpace(req.Capability)
	if capability == "" {
		capability = strings.TrimSpace(req.Operation)
	}

	if capability == "" {
		return false
	}

	switch capability {
	case "find_definition", "read_symbol", "extract_body", "outline":
		return true
	default:
		return strings.TrimSpace(req.Symbol) != "" && strings.Contains(strings.ToLower(capability), "symbol")
	}
}

func implementationTargetWriteReadyTargetedSearchContent(req workspace.ToolRequest, results []workspace.ToolResult) bool {
	pattern := strings.TrimSpace(req.Pattern)
	if len(pattern) < 4 {
		return false
	}

	if strings.TrimSpace(req.NamedGap) == "" && strings.TrimSpace(req.WhyNotAnswering) == "" {
		return false
	}

	if !implementationTargetWriteReadySearchPatternLooksNarrow(pattern) {
		return false
	}

	if implementationTargetWriteReadySearchPatternAlreadySourced(pattern, results) {
		return false
	}

	path := normalizeRunnerPath(req.Path)
	if path != "" {
		return implementationTargetWriteReadySearchPathLooksScoped(path)
	}

	return true
}

func implementationTargetWriteReadySearchPathLooksScoped(path string) bool {
	path = strings.Trim(normalizeRunnerPath(path), "/")
	if path == "" || path == "." {
		return false
	}

	switch path {
	case "internal", "cmd", "pkg", "src", "lib", "app", "apps", "packages", "test", "tests":
		return false
	default:
		return strings.Contains(path, "/") || recoverySearchPathLooksEditableSource(path)
	}
}

func implementationTargetWriteReadySearchPatternAlreadySourced(pattern string, results []workspace.ToolResult) bool {
	pattern = strings.ToLower(strings.TrimSpace(pattern))
	if pattern == "" {
		return false
	}

	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" || strings.TrimSpace(result.Output) == "" {
			continue
		}

		tool := strings.TrimSpace(result.Tool)
		if tool != workspace.ToolNameReadFile && tool != workspace.ToolNameCodeNavigate &&
			!strings.HasSuffix(tool, "_read_symbol") &&
			!strings.HasSuffix(tool, "_extract_body") {

			continue
		}

		if strings.Contains(strings.ToLower(result.Output), pattern) {
			return true
		}
	}

	return false
}

func implementationTargetWriteReadySearchPatternLooksNarrow(pattern string) bool {
	pattern = strings.TrimSpace(pattern)
	if len(pattern) < 8 {
		return false
	}

	if strings.Contains(pattern, " ") || strings.ContainsAny(pattern, ".:_-(){}[]") {
		return true
	}

	hasUpper := strings.IndexFunc(pattern, func(r rune) bool { return r >= 'A' && r <= 'Z' }) >= 0
	hasLower := strings.IndexFunc(pattern, func(r rune) bool { return r >= 'a' && r <= 'z' }) >= 0

	return hasUpper && hasLower
}

func implementationTargetWriteReadyPendingRedirectProgressAllowed(brief workerruntime.WorkerRuntimeBrief, req workspace.ToolRequest, results []workspace.ToolResult) bool {
	return sourceEvidencePendingRedirectProgressAllowedForBrief(brief, req, results)
}

func implementationTargetWriteReadyHasPendingSourceRedirect(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	return len(sourceEvidencePendingRedirectsForBrief(brief, results)) > 0
}

func implementationTargetWriteReadyRedirectedReadPath(result workspace.ToolResult) string {
	if strings.TrimSpace(result.Tool) != workspace.ToolNameReadFile || strings.TrimSpace(result.Error) != "" {
		return ""
	}

	summary := strings.TrimSpace(result.Summary)
	lower := strings.ToLower(summary)

	if !strings.HasPrefix(lower, "redirected ") {
		return ""
	}

	tail := summary[len("Redirected "):]
	idx := strings.Index(strings.ToLower(tail), " to ")

	if idx < 0 {
		return ""
	}

	return normalizeRunnerPath(strings.TrimSpace(tail[:idx]))
}




func implementationTargetWriteReadySamePathContinuationReadAllowed(req workspace.ToolRequest, results []workspace.ToolResult) bool {
	path := normalizeRunnerPath(req.Path)
	if path == "" || strings.TrimSpace(req.Tool) != workspace.ToolNameReadFile {
		return false
	}

	if req.StartLine <= 0 || req.EndLine < req.StartLine || req.EndLine-req.StartLine+1 > 400 {
		return false
	}

	ranges := readRangesForPathFromResults(results, path)
	if len(ranges) == 0 || len(ranges) >= implementationTargetWriteReadySamePathContinuationLimit {
		return false
	}

	return implementationTargetWriteReadyRequestedRangeAddsSource(req.StartLine, req.EndLine, ranges)
}

// Caps total same-path read_file ranges, including the range already read
// before continuations. New-target source expansions use their own turn-level
// allowance; this cap prevents one admitted path from becoming open discovery.
const implementationTargetWriteReadySamePathContinuationLimit = 3

func implementationTargetWriteReadyRequestedRangeAddsSource(start int, end int, ranges []planThenExecuteReadRange) bool {
	if start <= 0 || end < start {
		return false
	}

	if total, ok := implementationTargetWriteReadyKnownTotalLines(ranges); ok {
		if start > total {
			return false
		}

		if end > total {
			end = total
		}
	}

	for line := start; line <= end; line++ {
		covered := false

		for _, readRange := range ranges {
			rangeStart := readRange.start
			rangeEnd := readRange.end

			if rangeStart <= 0 || rangeEnd < rangeStart {
				continue
			}

			if readRange.total > 0 && rangeEnd > readRange.total {
				rangeEnd = readRange.total
			}

			if line >= rangeStart && line <= rangeEnd {
				covered = true
				break
			}
		}

		if !covered {
			return true
		}
	}

	return false
}

func implementationTargetWriteReadyKnownTotalLines(ranges []planThenExecuteReadRange) (int, bool) {
	total := 0
	for _, readRange := range ranges {
		if readRange.total <= 0 {
			continue
		}

		if total == 0 || readRange.total < total {
			total = readRange.total
		}
	}

	return total, total > 0
}

func (r *Runner) implementationTargetWriteReadySourceRefreshAllowed(req workspace.ToolRequest, results []workspace.ToolResult, refreshes int) bool {
	if !r.implementationTargetWriteReadySourceReadAllowed(req, results, refreshes) {
		return false
	}

	return concreteSourceEvidenceReadForPath(req.Path, results)
}

func implementationTargetWriteReadyNewTargetRead(req workspace.ToolRequest) bool {
	return strings.TrimSpace(req.Path) != ""
}

const implementationTargetWriteReadySourceReadAllowanceLimit = 3

func implementationTargetSourceReadEstablishedWriteReady(brief workerruntime.WorkerRuntimeBrief, req workspace.ToolRequest, result workspace.ToolResult, results []workspace.ToolResult) bool {
	if strings.TrimSpace(result.Error) != "" {
		return false
	}

	path := normalizeRunnerPath(req.Path)
	if path == "" || !implementationTargetWriteReadySourcePath(path) {
		return false
	}

	if !runnerStringSliceContainsPath(brief.RequiredEditPaths, path) &&
		!runnerStringSliceContainsPath(brief.EvidencePaths, path) &&
		!runnerStringSliceContainsPath(brief.AllowedEditPaths, path) {

		return false
	}

	switch strings.TrimSpace(req.Tool) {
	case workspace.ToolNameReadFile, workspace.ToolNameCodeNavigate:
	default:
		if !strings.HasSuffix(strings.TrimSpace(req.Tool), "_read_symbol") &&
			!strings.HasSuffix(strings.TrimSpace(req.Tool), "_extract_body") {

			return false
		}
	}

	return concreteSourceEvidenceReadForPath(path, results)
}

func runnerStringSliceContainsPath(values []string, path string) bool {
	path = normalizeRunnerPath(path)
	if path == "" {
		return false
	}

	for _, value := range values {
		if normalizeRunnerPath(value) == path {
			return true
		}
	}

	return false
}

func implementationTargetWriteReadySourcePath(path string) bool {
	path = normalizeRunnerPath(path)
	if path == "" {
		return false
	}

	lower := strings.ToLower(path)

	return !strings.HasSuffix(lower, ".md") && !strings.HasSuffix(lower, ".mdx")
}

func (r *Runner) implementationPreWriteNonWriteRecovery(req workspace.ToolRequest, results []workspace.ToolResult) (summary string, errorText string, prompt string, reject bool) {
	if r == nil || !implementationPreWriteToolSurfaceActive(r.RuntimeBrief, results) || r.writeOnlyRecoveryToolIsWrite(req.Tool) {
		return "", "", "", false
	}

	missing := r.remainingRequiredEditPaths(results)
	target := "the required edit path(s)"

	if len(missing) > 0 {
		target = strings.Join(missing, ", ")
	} else if paths := nonEmptyRunnerStrings(r.RuntimeBrief.RequiredEditPaths, 3); len(paths) > 0 {
		target = strings.Join(paths, ", ")
	}

	tool := strings.TrimSpace(req.Tool)
	if tool == "" {
		tool = "non-write tool"
	}

	summary = "Write-ready implementation non-write tool rejected"
	errorText = fmt.Sprintf("the target packet and required_edit_paths already establish write readiness for %s. %s does not satisfy the pre-write implementation contract; available outcomes are an allowed write capability or <<QUESTION>> with the exact path-specific blocker.", target, tool)
	prompt = fmt.Sprintf("The exact-anchor implementation packet is write-first for %s. Non-write tools are now hidden for this turn. The available contract outcome is the smallest safe write capability for each remaining required path. If the supplied anchor is stale or missing, answer with <<QUESTION>> and name that exact blocker; do not perform additional discovery or task planning.", target)

	return summary, errorText, prompt, true
}

func (r *Runner) unscopedContextPackCandidateReadRecovery(req workspace.ToolRequest, results []workspace.ToolResult) (summary string, errorText string, prompt string, reject bool) {
	if r == nil || !r.unscopedNoWriteCompletionRecoveryApplies(results) || !contextPackSeen(results) || len(concreteRecoveryEvidencePaths(results, 1)) > 0 {
		return "", "", "", false
	}

	if strings.TrimSpace(req.Tool) != workspace.ToolNameReadFile {
		return "", "", "", false
	}

	if contextPackCandidateReadAllowed(req, results) {
		return "", "", "", false
	}

	summary = "Context packet candidate read rejected"
	errorText = fmt.Sprintf("context_pack has not established an exact edit target yet. %s is only a packet candidate, so recovery must first gather literal or semantic evidence tied to the user's requested behavior.", strings.TrimSpace(req.Path))
	prompt = "The context packet did not establish an exact edit target. Do not read or write packet candidates yet. Run one bounded fallback discovery action tied to literal words, symbols, or behavior from the user's actual request, and include a named gap explaining that the missing fact is a grounded source hit. After a concrete hit, read the highest-confidence path before writing."

	return summary, errorText, prompt, true
}

func (r *Runner) deterministicPlanExecuteGroundingRecoveryRequest(reply string, results []workspace.ToolResult) (workspace.ToolRequest, string, bool) {
	if r == nil || !r.planThenExecutePlannerPhase() || successfulSubmitPlanResultCount(results) > 0 {
		return workspace.ToolRequest{}, "", false
	}

	if planThenExecuteTerminalReplyShape(reply) {
		state := DerivePlanExecutePlanningState(r.RuntimeBrief, results, PlanExecutePlanningTransport{TextualToolParser: true})
		if state.Mode != PlanExecutePlanningModeAnchorRefresh || len(state.RefreshPaths) == 0 {
			return workspace.ToolRequest{}, "", false
		}

		rejected, _, ok := lastRejectedSubmitPlanCoverageResult(results)
		if !ok || !submitPlanRejectionHasInvalidAnchor(rejected) {
			return workspace.ToolRequest{}, "", false
		}
	}

	rejectedAt := -1

	for i := len(results) - 1; i >= 0; i-- {
		if planThenExecuteSubmitPlanGroundingRejectedResult(results[i]) {
			rejectedAt = i
			break
		}
	}

	if rejectedAt < 0 {
		state := DerivePlanExecutePlanningState(r.RuntimeBrief, results, PlanExecutePlanningTransport{TextualToolParser: true})
		if rejected, fallbackRejectedAt, ok := lastRejectedSubmitPlanCoverageResult(results); ok &&
			submitPlanRejectionHasInvalidAnchor(rejected) &&
			state.Mode == PlanExecutePlanningModeAnchorRefresh {

			rejectedAt = fallbackRejectedAt
		}
	}

	if rejectedAt < 0 {
		return workspace.ToolRequest{}, "", false
	}

	state := DerivePlanExecutePlanningState(r.RuntimeBrief, results, PlanExecutePlanningTransport{TextualToolParser: true})
	if rejected, _, ok := lastRejectedSubmitPlanCoverageResult(results); ok && state.Mode == PlanExecutePlanningModeAnchorRefresh {
		for _, path := range state.RefreshPaths {
			normalized := normalizeRunnerPath(path)
			if normalized == "" {
				continue
			}

			start, end := planThenExecuteAnchorRefreshFallbackRange(normalized, results, rejected)
			req := workspace.ToolRequest{
				Tool:            workspace.ToolNameReadFile,
				Path:            normalized,
				StartLine:       start,
				EndLine:         end,
				NamedGap:        "refresh current source evidence for the rejected submit_plan edit path",
				WhyNotAnswering: "need bounded source evidence before resubmitting the corrected plan",
			}

			return req, marshalToolRequestRaw(req), true
		}

		return workspace.ToolRequest{}, "", false
	}

	if !successfulToolAfter(results, rejectedAt, workspace.ToolNameContextPack) {
		task := strings.TrimSpace(r.RuntimeBrief.Objective)
		if task == "" {
			task = "localize candidate edit paths for the unscoped plan-execute atom"
		}

		req := workspace.ToolRequest{Tool: workspace.ToolNameContextPack, Task: task, Mode: "plan", Limit: 8}

		return req, marshalToolRequestRaw(req), true
	}

	if !successfulToolAfter(results, rejectedAt, workspace.ToolNameSearchContent) {
		if pattern := r.planExecuteGroundingSearchPattern(); pattern != "" {
			req := workspace.ToolRequest{
				Tool:            workspace.ToolNameSearchContent,
				Path:            ".",
				Pattern:         pattern,
				NamedGap:        "locate exact current source for the requested plan-execute atom before submit_plan",
				WhyNotAnswering: "need grounded source hits before plan-execute can submit edits or source-satisfied",
			}

			return req, marshalToolRequestRaw(req), true
		}
	}

	candidates := append([]string{}, candidateRecoveryScopePaths(r.RuntimeBrief, results[rejectedAt+1:], 8)...)

	candidates = appendUniqueRunnerPaths(candidates, contextPackCandidatePaths(results[rejectedAt+1:], 8)...)

	read := readPathsFromResults(results[rejectedAt+1:])

	if len(read) >= 4 {
		return workspace.ToolRequest{}, "", false
	}

	for _, path := range candidates {
		if _, ok := read[normalizeRunnerPath(path)]; ok {
			continue
		}

		req := workspace.ToolRequest{
			Tool:            workspace.ToolNameReadFile,
			Path:            path,
			StartLine:       1,
			EndLine:         180,
			NamedGap:        "current source evidence for the candidate edit path before submit_plan",
			WhyNotAnswering: "need source-body evidence before plan-execute can submit edits or source-satisfied",
		}

		return req, marshalToolRequestRaw(req), true
	}

	return workspace.ToolRequest{}, "", false
}

func planThenExecuteAnchorRefreshFallbackRange(path string, results []workspace.ToolResult, rejected workspace.ToolResult) (int, int) {
	path = normalizeRunnerPath(path)
	if path == "" {
		return 1, 180
	}

	if line, ok := submitPlanSyntaxValidationLineForPath(rejected, path); ok {
		start := line - 40
		if start < 1 {
			start = 1
		}

		return planThenExecuteClampAnchorRefreshRange(start, line+40)
	}

	ranges := readRangesForPathFromResults(results, path)
	for i := len(ranges) - 1; i >= 0; i-- {
		if ranges[i].start <= 0 || ranges[i].end < ranges[i].start {
			continue
		}

		end := ranges[i].end
		if ranges[i].total > 0 && end > ranges[i].total {
			end = ranges[i].total
		}

		if ranges[i].total <= 0 || end < ranges[i].total {
			return planThenExecuteClampAnchorRefreshRange(end+1, end+180)
		}

		return planThenExecuteClampAnchorRefreshRange(ranges[i].start, end)
	}

	return 1, 180
}

func planThenExecuteClampAnchorRefreshRange(start, end int) (int, int) {
	if start <= 0 {
		start = 1
	}
	if end < start {
		end = start
	}
	if end-start+1 > 400 {
		end = start + 399
	}

	return start, end
}

func (r *Runner) deterministicPlanExecuteSourceSatisfiedEvidenceRead(reply string, results []workspace.ToolResult) (workspace.ToolRequest, string, bool) {
	if r == nil || !r.planThenExecutePlannerPhase() || successfulSubmitPlanResultCount(results) > 0 {
		return workspace.ToolRequest{}, "", false
	}

	if !planThenExecuteSourceSatisfiedAnswerIntent(reply) || r.planThenExecuteSourceSatisfiedAnswerCovers(reply, results) {
		return workspace.ToolRequest{}, "", false
	}

	evidencePaths := PlanThenExecuteSourceSatisfiedEvidencePaths(reply)
	if len(evidencePaths) == 0 {
		return workspace.ToolRequest{}, "", false
	}

	candidateSet := normalizedRequiredEditPathSet(candidateRecoveryScopePaths(r.RuntimeBrief, results, 16))
	requiredSet := normalizedRequiredEditPathSet(r.requiredEditCoveragePaths())

	for _, path := range evidencePaths {
		normalized := normalizeRunnerPath(path)
		if normalized == "" || !pathparse.HasWorkspaceFileExtension(normalized) {
			continue
		}

		if len(requiredSet) > 0 {
			if _, ok := requiredSet[normalized]; !ok {
				continue
			}
		} else if len(candidateSet) > 0 {
			if _, ok := candidateSet[normalized]; !ok {
				continue
			}
		}

		if planThenExecuteRequiredSourceEvidenceForPath(r.RuntimeBrief, results, normalized).Level == planThenExecuteEvidenceAnchorReady {
			continue
		}

		req := workspace.ToolRequest{
			Tool:            workspace.ToolNameReadFile,
			Path:            normalized,
			StartLine:       1,
			EndLine:         180,
			NamedGap:        "source-satisfied current-state evidence for the named path",
			WhyNotAnswering: "need source-body evidence before accepting the plan-execute source-satisfied closeout",
		}

		return req, marshalToolRequestRaw(req), true
	}

	return workspace.ToolRequest{}, "", false
}

func planThenExecuteGroundingRejectedSeen(results []workspace.ToolResult) bool {
	for _, result := range results {
		if planThenExecuteSubmitPlanGroundingRejectedResult(result) {
			return true
		}
	}

	return false
}

func (r *Runner) planExecuteGroundingSearchPattern() string {
	if r == nil {
		return ""
	}

	objectiveTerms := recoveryObjectiveSearchWords(r.RuntimeBrief.Objective, 24)
	objectiveSet := map[string]struct{}{}

	for _, term := range objectiveTerms {
		objectiveSet[singularObjectiveWord(term)] = struct{}{}
	}

	patterns := make([]string, 0, 12)
	seen := map[string]struct{}{}
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}

		if _, ok := seen[value]; ok {
			return
		}

		seen[value] = struct{}{}
		patterns = append(patterns, regexp.QuoteMeta(value))
	}

	for _, tool := range r.RuntimeBrief.AllowedTools {
		tool = strings.TrimSpace(tool)
		if !toolNameOverlapsObjective(tool, objectiveSet) {
			continue
		}

		add(tool)

		if len(patterns) >= 4 {
			break
		}
	}

	if len(patterns) > 0 {
		return strings.Join(patterns, "|")
	}

	for _, term := range objectiveTerms {
		add(term)

		if len(patterns) >= 8 {
			break
		}
	}

	return strings.Join(patterns, "|")
}

func toolNameOverlapsObjective(tool string, objectiveSet map[string]struct{}) bool {
	if strings.TrimSpace(tool) == "" || len(objectiveSet) == 0 {
		return false
	}

	parts := strings.FieldsFunc(strings.ToLower(tool), func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	})
	matches := 0

	for _, part := range parts {
		if len(part) < 4 {
			continue
		}

		if _, ok := objectiveSet[singularObjectiveWord(part)]; ok {
			matches++
		}
	}

	return matches >= 2 || (len(parts) == 1 && matches == 1)
}



func planThenExecuteTerminalReplyShape(reply string) bool {
	trimmed := strings.TrimSpace(reply)
	if trimmed == "" {
		return false
	}

	return strings.HasPrefix(trimmed, "Decision: source-satisfied") ||
		strings.HasPrefix(trimmed, "Decision: block") ||
		strings.HasPrefix(trimmed, "<<QUESTION>>:")
}

func successfulToolAfter(results []workspace.ToolResult, index int, tool string) bool {
	tool = strings.TrimSpace(tool)
	for i := index + 1; i < len(results); i++ {
		if strings.TrimSpace(results[i].Tool) == tool && strings.TrimSpace(results[i].Error) == "" {
			return true
		}
	}

	return false
}

func appendUniqueRunnerPaths(paths []string, more ...string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(paths)+len(more))

	for _, path := range append(paths, more...) {
		normalized := normalizeRunnerPath(path)
		if normalized == "" || !pathparse.HasWorkspaceFileExtension(normalized) {
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

func contextPackCandidateReadAllowed(req workspace.ToolRequest, results []workspace.ToolResult) bool {
	target := normalizeRunnerPath(req.Path)
	if target == "" || !pathparse.HasWorkspaceFileExtension(target) {
		return false
	}

	candidates := contextPackCandidatePaths(results, 32)
	for _, candidate := range candidates {
		if candidate == target {
			return true
		}
	}

	return false
}

func contextPackCandidatePaths(results []workspace.ToolResult, max int) []string {
	if max <= 0 {
		max = 8
	}

	seen := map[string]struct{}{}
	paths := make([]string, 0, max)
	add := func(path string) {
		path = normalizeRunnerPath(path)
		if path == "" || !pathparse.HasWorkspaceFileExtension(path) {
			return
		}

		if _, ok := seen[path]; ok {
			return
		}

		seen[path] = struct{}{}
		paths = append(paths, path)
	}

	for _, result := range results {
		if strings.TrimSpace(result.Tool) != workspace.ToolNameContextPack || strings.TrimSpace(result.Output) == "" {
			continue
		}

		var view struct {
			CandidateFiles []struct {
				Path string `json:"path"`
				Role string `json:"role"`
			} `json:"candidate_files"`
		}

		if err := json.Unmarshal([]byte(result.Output), &view); err != nil {
			continue
		}

		for _, file := range view.CandidateFiles {
			role := strings.TrimSpace(file.Role)
			if role != "" && role != "editable" && role != "read_only_evidence" && role != "test" {
				continue
			}

			add(file.Path)

			if len(paths) >= max {
				return paths
			}
		}
	}

	return paths
}

func recoverySearchPathLooksEditableSource(path string) bool {
	lower := strings.ToLower(filepath.ToSlash(strings.TrimSpace(path)))
	if lower == "" {
		return false
	}

	for _, suffix := range []string{
		".go", ".rs", ".py", ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs",
		".java", ".kt", ".swift", ".c", ".cc", ".cpp", ".h", ".hpp", ".cs",
		".rb", ".php", ".sh", ".bash", ".zsh", ".fish", ".lua", ".sql",
		".html", ".css", ".scss", ".sass", ".vue", ".svelte", ".templ",
		".json", ".yaml", ".yml", ".toml", ".xml", ".md", ".mdx",
	} {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}

	return false
}

func literalRecoverySearchPattern(objective string) string {
	terms := recoveryObjectiveSearchWords(objective, 8)
	if len(terms) == 0 {
		return ""
	}

	quoted := make([]string, 0, len(terms))
	for _, term := range terms {
		quoted = append(quoted, regexp.QuoteMeta(term))
	}

	return strings.Join(quoted, "|")
}

