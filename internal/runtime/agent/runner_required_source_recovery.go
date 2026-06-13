package agent

import (
	"fmt"
	"idiom/internal/capabilities/workspace"
	"idiom/internal/runtime/workerruntime"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode"
)

func (r *Runner) deterministicPlanExecuteRequiredSourceRead(results []workspace.ToolResult) (workspace.ToolRequest, string, bool) {
	if r == nil || !r.planThenExecutePlannerPhase() || len(r.RuntimeBrief.RequiredEditPaths) == 0 {
		return workspace.ToolRequest{}, "", false
	}

	if successfulSubmitPlanResultCount(results) > 0 || lastNonGateTool(results) == workspace.ToolNameSubmitPlan {
		return workspace.ToolRequest{}, "", false
	}

	unread := planThenExecuteUnreadRequiredPaths(r.RuntimeBrief, results)
	next := ""

	for _, path := range r.requiredEditCoveragePaths() {
		normalized := normalizeRunnerPath(path)
		if normalized == "" {
			continue
		}

		if _, ok := unread[normalized]; !ok {
			continue
		}

		if planThenExecuteRequiredPathHasTerminalMissingFileEvidence(normalized, results) || planThenExecutePathHasAnyToolEvidence(normalized, results) {
			if req, ok := r.deterministicPlanExecuteRequiredSourceFollowup(normalized, results); ok {
				return req, marshalToolRequestRaw(req), true
			}

			continue
		}

		next = normalized

		break
	}

	if next == "" {
		return workspace.ToolRequest{}, "", false
	}

	req := workspace.ToolRequest{
		Tool:            workspace.ToolNameReadFile,
		Path:            next,
		StartLine:       1,
		EndLine:         180,
		NamedGap:        "source-body evidence for required plan-execute edit path",
		WhyNotAnswering: "need current source before submit_plan can anchor the required edit",
	}

	return req, marshalToolRequestRaw(req), true
}

type planThenExecuteRequiredSourceSurfaceAction struct {
	Request  workspace.ToolRequest
	Raw      string
	Summary  string
	Error    string
	Redirect bool
}

func (r *Runner) planThenExecuteRequiredSourceSurfaceAction(req workspace.ToolRequest, results []workspace.ToolResult, allowedTools []string) (planThenExecuteRequiredSourceSurfaceAction, bool) {
	if r == nil || !r.planThenExecutePlannerPhase() || len(r.RuntimeBrief.RequiredEditPaths) == 0 {
		return planThenExecuteRequiredSourceSurfaceAction{}, false
	}

	if successfulSubmitPlanResultCount(results) > 0 || strings.TrimSpace(req.Tool) == workspace.ToolNameSubmitPlan || !isReadClassTool(req) {
		return planThenExecuteRequiredSourceSurfaceAction{}, false
	}

	unread := planThenExecuteUnreadRequiredPaths(r.RuntimeBrief, results)
	if len(unread) == 0 {
		return planThenExecuteRequiredSourceSurfaceAction{}, false
	}

	if redirected, raw, ok := r.deterministicPlanExecuteRequiredSourceRead(results); ok &&
		runnerAllowsTool(allowedTools, redirected.Tool) &&
		normalizedToolRequestRaw(redirected) != normalizedToolRequestRaw(req) {

		target := normalizeRunnerPath(planThenExecuteRequestTargetPath(redirected))
		if _, missing := unread[target]; missing {
			evidence := planThenExecuteRequiredSourceEvidenceForPath(r.RuntimeBrief, results, target)
			if planThenExecuteRequiredSourceRequestAdvances(redirected, target, evidence, results) {
				return planThenExecuteRequiredSourceSurfaceAction{
					Request:  redirected,
					Raw:      raw,
					Redirect: true,
				}, true
			}
		}
	}

	summary, errorText, reject := planThenExecuteRequiredPathDiscoveryRejection(req, r.RuntimeBrief, results)
	if !reject {
		return planThenExecuteRequiredSourceSurfaceAction{}, false
	}

	return planThenExecuteRequiredSourceSurfaceAction{
		Summary: summary,
		Error:   errorText,
	}, true
}

func (r *Runner) deterministicPlanExecuteRejectedRequiredSourceRead(reply string, results []workspace.ToolResult) (workspace.ToolRequest, string, bool) {
	if r == nil || !r.planThenExecutePlannerPhase() || len(r.RuntimeBrief.RequiredEditPaths) == 0 {
		return workspace.ToolRequest{}, "", false
	}

	trimmed := strings.TrimSpace(reply)
	if trimmed == "" || strings.HasPrefix(trimmed, "<<QUESTION>>") {
		return workspace.ToolRequest{}, "", false
	}

	if planThenExecuteUnreadRequiredSourceNudge(r.RuntimeBrief, results) == "" {
		return workspace.ToolRequest{}, "", false
	}

	return r.deterministicPlanExecuteRequiredSourceRead(results)
}

func (r *Runner) deterministicImplementationRejectedRequiredPathRead(reply string, results []workspace.ToolResult) (workspace.ToolRequest, string, bool) {
	if r == nil || r.planThenExecutePlannerPhase() || !runnerBriefRequiresMutation(r.RuntimeBrief) || len(r.RuntimeBrief.RequiredEditPaths) == 0 {
		return workspace.ToolRequest{}, "", false
	}

	trimmed := strings.TrimSpace(reply)
	if trimmed == "" || strings.HasPrefix(trimmed, "<<QUESTION>>") {
		return workspace.ToolRequest{}, "", false
	}

	remaining := r.remainingRequiredEditPaths(results)
	if !scopedImplementationUsesBoundedSourceEdits(r.RuntimeBrief, remaining) {
		return workspace.ToolRequest{}, "", false
	}

	if implementationRequiredPathAnchorsReady(r.RuntimeBrief, results) {
		return workspace.ToolRequest{}, "", false
	}

	if !implementationRejectedTextCanAdvanceRequiredRead(trimmed) {
		return workspace.ToolRequest{}, "", false
	}

	return r.deterministicImplementationRequiredPathRead(results)
}

func implementationRejectedTextCanAdvanceRequiredRead(reply string) bool {
	trimmed := strings.TrimSpace(reply)
	if trimmed == "" || strings.HasPrefix(trimmed, "<<QUESTION>>") {
		return false
	}

	lower := " " + strings.ToLower(strings.Join(strings.Fields(trimmed), " ")) + " "

	return !containsAny(lower,
		" blocked ",
		" blocker ",
		" cannot ",
		" can't ",
		" could not ",
		" unable ",
		" no access ",
		" permission ",
		" not available ",
	)
}

func (r *Runner) deterministicImplementationRequiredPathRead(results []workspace.ToolResult) (workspace.ToolRequest, string, bool) {
	if r == nil || r.planThenExecutePlannerPhase() || !runnerBriefRequiresMutation(r.RuntimeBrief) || len(r.RuntimeBrief.RequiredEditPaths) == 0 {
		return workspace.ToolRequest{}, "", false
	}

	if len(r.remainingRequiredEditPaths(results)) == 0 {
		return workspace.ToolRequest{}, "", false
	}

	changed := changedPathsFromToolResults(results)

	for _, path := range r.requiredEditCoveragePaths() {
		normalized := normalizeRunnerPath(path)
		if normalized == "" {
			continue
		}

		if _, ok := changed[normalized]; ok {
			continue
		}

		evidence := planThenExecuteRequiredSourceEvidenceForPath(r.RuntimeBrief, results, normalized)
		if evidence.Level == planThenExecuteEvidenceAnchorReady {
			continue
		}

		if evidence.Level != planThenExecuteEvidenceNone || planThenExecutePathHasAnyToolEvidence(normalized, results) {
			if req, ok := r.deterministicImplementationRequiredSourceFollowup(normalized, results); ok {
				return req, marshalToolRequestRaw(req), true
			}

			if alreadyReadPath(results, normalized) {
				continue
			}
		}

		req := workspace.ToolRequest{
			Tool:            workspace.ToolNameReadFile,
			Path:            normalized,
			StartLine:       1,
			EndLine:         220,
			NamedGap:        "exact current source needed before editing this required path",
			WhyNotAnswering: "need current source evidence before the write",
		}

		return req, marshalToolRequestRaw(req), true
	}

	return workspace.ToolRequest{}, "", false
}

func (r *Runner) deterministicImplementationEvidencePathRead(results []workspace.ToolResult) (workspace.ToolRequest, string, bool) {
	if r == nil || r.planThenExecutePlannerPhase() || !r.unscopedNoWriteCompletionRecoveryApplies(results) || lastNonGateTool(results) != "" {
		return workspace.ToolRequest{}, "", false
	}

	seen := map[string]struct{}{}

	var paths []string

	for _, path := range r.RuntimeBrief.EvidencePaths {
		normalized := normalizeRunnerPath(path)
		if normalized == "" || !recoverySearchPathLooksEditableSource(normalized) || alreadyReadPath(results, normalized) {
			continue
		}

		if _, ok := seen[normalized]; ok {
			continue
		}

		seen[normalized] = struct{}{}
		paths = append(paths, normalized)
	}

	if len(paths) != 1 {
		return workspace.ToolRequest{}, "", false
	}

	req := workspace.ToolRequest{
		Tool:            workspace.ToolNameReadFile,
		Path:            paths[0],
		StartLine:       1,
		EndLine:         180,
		NamedGap:        "exact current source needed before editing the implementation evidence path",
		WhyNotAnswering: "need current source before the write",
	}

	return req, marshalToolRequestRaw(req), true
}

func (r *Runner) deterministicPlanExecuteRequiredSourceFollowup(path string, results []workspace.ToolResult) (workspace.ToolRequest, bool) {
	return r.deterministicRequiredSourceFollowup(path, results, requiredSourceFollowupWording{
		OutlineGap:       "source-body line anchors for required plan-execute edit path",
		SymbolGap:        "source-body evidence for required plan-execute edit path",
		OutlineWhy:       "whole-file read redirected; need declarations before selecting a source-body read",
		SymbolWhy:        "need current declaration body before submit_plan can anchor the required edit",
		RangeWhy:         "outline did not produce a readable symbol; need bounded source before submit_plan can anchor the required edit",
		FallbackRangeWhy: "outline did not expose usable declarations; need bounded source before submit_plan can anchor the required edit",
	})
}

func (r *Runner) planThenExecuteRequiredSourceFollowupRedirect(results []workspace.ToolResult, req workspace.ToolRequest) (workspace.ToolRequest, bool) {
	if r == nil || !r.planThenExecutePlannerPhase() || len(r.RuntimeBrief.RequiredEditPaths) == 0 {
		return workspace.ToolRequest{}, false
	}

	if successfulSubmitPlanResultCount(results) > 0 || strings.TrimSpace(req.Tool) == workspace.ToolNameSubmitPlan || !isReadClassTool(req) {
		return workspace.ToolRequest{}, false
	}

	target := normalizeRunnerPath(planThenExecuteRequestTargetPath(req))
	if target == "" {
		return workspace.ToolRequest{}, false
	}

	if _, ok := normalizedRequiredEditPathSet(r.RuntimeBrief.RequiredEditPaths)[target]; !ok {
		return workspace.ToolRequest{}, false
	}

	if planThenExecuteInvalidAnchorRefreshAllowed(req, target, results) {
		return workspace.ToolRequest{}, false
	}

	unread := planThenExecuteUnreadRequiredPaths(r.RuntimeBrief, results)
	if _, missing := unread[target]; !missing {
		if len(unread) == 0 {
			return workspace.ToolRequest{}, false
		}

		redirected, _, ok := r.deterministicPlanExecuteRequiredSourceRead(results)
		if !ok || normalizedToolRequestRaw(redirected) == normalizedToolRequestRaw(req) {
			return workspace.ToolRequest{}, false
		}

		return redirected, true
	}

	evidence := planThenExecuteRequiredSourceEvidenceForPath(r.RuntimeBrief, results, target)
	if evidence.Level == planThenExecuteEvidenceNone && !planThenExecutePathHasAnyToolEvidence(target, results) {
		return workspace.ToolRequest{}, false
	}

	if planThenExecuteRequiredSourceRequestAdvances(req, target, evidence, results) {
		return workspace.ToolRequest{}, false
	}

	redirected, ok := r.deterministicPlanExecuteRequiredSourceFollowup(target, results)
	if !ok || normalizedToolRequestRaw(redirected) == normalizedToolRequestRaw(req) {
		return workspace.ToolRequest{}, false
	}

	return redirected, true
}

func (r *Runner) planThenExecuteAnchorRefreshWriteOnlyRejection(req workspace.ToolRequest, results []workspace.ToolResult) (workspace.ToolResult, bool) {
	if r == nil || !r.planThenExecutePlannerPhase() {
		return workspace.ToolResult{}, false
	}

	state := DerivePlanExecutePlanningState(r.RuntimeBrief, results, PlanExecutePlanningTransport{TextualToolParser: true})
	if state.Mode != PlanExecutePlanningModeAnchorRefresh || len(state.RefreshPaths) == 0 {
		return workspace.ToolResult{}, false
	}

	targets := strings.Join(nonEmptyRunnerStrings(state.RefreshPaths, 4), ", ")

	return workspace.ToolResult{
		Tool:    req.Tool,
		Summary: "Bounded submit_plan anchor refresh required",
		Error:   fmt.Sprintf("%s is not available in its requested form while plan-execute anchor-refresh mode is still open for: %s. Issue exactly one bounded source-body request for a remaining refresh path: `read_file` with explicit `start_line`/`end_line`, or `%s` with %s plus `pattern`. Do not call `submit_plan` until that refresh returns.", strings.TrimSpace(req.Tool), targets, workspace.CodeNavigateToolName, workspace.CodeNavigateSourceBodyCapabilityPhrase()),
	}, true
}

func planThenExecuteInvalidAnchorRefreshAllowed(req workspace.ToolRequest, target string, results []workspace.ToolResult) bool {
	rejected, rejectedAt, ok := lastRejectedSubmitPlanCoverageResult(results)
	if !ok {
		return false
	}

	invalidAnchorPaths := submitPlanInvalidAnchorPaths(rejected)
	if _, invalidAnchor := invalidAnchorPaths[normalizeRunnerPath(target)]; !invalidAnchor {
		return false
	}

	return submitPlanInvalidAnchorPathsAllowsRead(invalidAnchorPaths, target) &&
		planThenExecuteInvalidAnchorRefreshRequestAllowed(req, target, results) &&
		!successfulInvalidAnchorCorrectionEvidenceForRejection(results, rejectedAt, target)
}

const planThenExecuteReadySourceRefreshLimit = 4

func (r *Runner) planThenExecuteReadySourceRefreshAllowed(req workspace.ToolRequest, results []workspace.ToolResult, used int) bool {
	if r == nil || !r.planThenExecutePlannerPhase() || used >= planThenExecuteReadySourceRefreshLimit {
		return false
	}

	if len(r.RuntimeBrief.RequiredEditPaths) == 0 || successfulSubmitPlanResultCount(results) > 0 || len(planThenExecuteUnreadRequiredPaths(r.RuntimeBrief, results)) > 0 {
		return false
	}

	if planThenExecuteSubmitReadyParseFailureBlocksSourceRefresh(r.RuntimeBrief, results) {
		return false
	}

	if !planThenExecuteRejectedPlanSourceRefreshAllowed(req, r.requiredEditCoveragePaths(), results) {
		return false
	}

	target := normalizeRunnerPath(planThenExecuteRequestTargetPath(req))
	if target == "" {
		return false
	}

	if _, ok := normalizedRequiredEditPathSet(r.RuntimeBrief.RequiredEditPaths)[target]; !ok {
		return false
	}

	if !planThenExecuteInvalidAnchorRefreshRequestAllowed(req, target, results) {
		return false
	}

	return true
}

func (r *Runner) planThenExecuteCandidateSourceRefreshAllowed(req workspace.ToolRequest, results []workspace.ToolResult, used int) bool {
	if r == nil || !r.planThenExecutePlannerPhase() || used >= planThenExecuteReadySourceRefreshLimit {
		return false
	}

	if len(r.RuntimeBrief.RequiredEditPaths) > 0 || successfulSubmitPlanResultCount(results) > 0 {
		return false
	}

	return planThenExecuteCandidateRejectedPlanSourceRefreshAllowed(req, r.RuntimeBrief, results)
}

func (r *Runner) planThenExecuteRejectedPlanRefreshReadRedirect(req workspace.ToolRequest, results []workspace.ToolResult) (workspace.ToolRequest, string, bool) {
	if r == nil || !r.planThenExecutePlannerPhase() || strings.TrimSpace(req.Tool) != workspace.ToolNameReadFile {
		return workspace.ToolRequest{}, "", false
	}

	if successfulSubmitPlanResultCount(results) > 0 {
		return workspace.ToolRequest{}, "", false
	}

	target := normalizeRunnerPath(planThenExecuteRequestTargetPath(req))
	if target == "" {
		return workspace.ToolRequest{}, "", false
	}

	state := DerivePlanExecutePlanningState(r.RuntimeBrief, results, PlanExecutePlanningTransport{TextualToolParser: true})
	if state.Mode != PlanExecutePlanningModeAnchorRefresh || !slices.Contains(state.RefreshPaths, target) {
		return workspace.ToolRequest{}, "", false
	}

	rejected, rejectedAt, ok := lastRejectedSubmitPlanCoverageResult(results)
	if !ok {
		return workspace.ToolRequest{}, "", false
	}

	line, ok := submitPlanSyntaxValidationLineForPath(rejected, target)
	if ok && req.StartLine <= line && line <= req.EndLine && planThenExecuteAnchorRefreshRequestAllowed(req) {
		return workspace.ToolRequest{}, "", false
	}

	if !ok &&
		planThenExecuteAnchorRefreshRequestAllowed(req) &&
		!planThenExecuteAnchorRefreshReadRangeAlreadyTried(req, target, results[rejectedAt+1:]) {
		return workspace.ToolRequest{}, "", false
	}

	start, end := planThenExecuteAnchorRefreshFallbackRange(target, results, rejected)
	if ok {
		start = line - 20
		if start < 1 {
			start = 1
		}
		end = line + 60
	}
	redirected := workspace.ToolRequest{
		Tool:            workspace.ToolNameReadFile,
		Path:            target,
		StartLine:       start,
		EndLine:         end,
		NamedGap:        "refresh current source around rejected submit_plan syntax failure",
		WhyNotAnswering: "need current source around rejected plan line before corrected submit_plan",
	}

	return redirected, marshalToolRequestRaw(redirected), true
}

func planThenExecuteSubmitReadyParseFailureSeen(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	return planThenExecuteSubmitReadyParseFailureIndex(brief, results) >= 0
}

func planThenExecuteSubmitReadyParseFailureBlocksSourceRefresh(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	parseFailureAt := planThenExecuteSubmitReadyParseFailureIndex(brief, results)
	if parseFailureAt < 0 {
		return false
	}

	_, rejectedAt, ok := lastRejectedSubmitPlanCoverageResult(results)

	return !ok || rejectedAt < parseFailureAt
}

func planThenExecuteSubmitReadyParseFailureIndex(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) int {
	last := -1

	for i, result := range results {
		if strings.TrimSpace(result.Tool) != "tool_parse" || strings.TrimSpace(result.Error) == "" {
			continue
		}

		if planThenExecuteSubmitPlanOnlyReady(brief, results[:i]) {
			last = i
		}
	}

	return last
}

func (r *Runner) planThenExecuteSubmitPlanOnlySurfaceRejection(req workspace.ToolRequest, results []workspace.ToolResult, allowedTools []string) (string, string, string, bool) {
	if r == nil || !planThenExecuteTerminalSubmitPlanSurfaceReady(r.RuntimeBrief, results) {
		return "", "", "", false
	}

	tool := strings.TrimSpace(req.Tool)
	if tool == "" || tool == workspace.ToolNameSubmitPlan {
		return "", "", "", false
	}

	if len(allowedTools) > 0 && !runnerAllowsTool(allowedTools, workspace.ToolNameSubmitPlan) {
		return "", "", "", false
	}

	if planThenExecuteSubmitCorrectionActive(r.RuntimeBrief, results) {
		summary, errorText, prompt := planThenExecuteSubmitCorrectionWrongToolRejection(tool, planThenExecuteSubmitCorrectionBlockerAllowed(r.RuntimeBrief, results))
		return summary, errorText, prompt, true
	}

	prompt := strings.Join([]string{
		"Plan-execute submit_plan is now the only allowed planner action. Every required_edit_path already has anchor-ready source evidence, so read/search/source-refresh tools are closed for this planner turn.",
		"The next visible action must be a parser-readable `submit_plan` JSON object. " + planThenExecuteSubmitPlanCoverageSentence() + " " + planThenExecuteSubmitPlanVisibleJSONInstruction(),
		"Do not call `read_file`, `code_navigate`, search, shell, git, source-sanity, or per-edit write tools in this state. The rejected request will not become available again in this planner phase.",
		"Only use `Decision: source-satisfied` if every required path already contains the requested final state and you cite per-path current-state evidence. Only use `<<QUESTION>>:` if one exact path-specific blocker prevents drafting any `submit_plan` edit.",
	}, "\n")

	return "Plan-execute submit_plan required", fmt.Sprintf("%s is not available because the plan-execute planner is source-ready and exposes only `submit_plan`. Emit a parser-readable `submit_plan` JSON object now. %s Use `Decision: source-satisfied` or `<<QUESTION>>:` only for a true terminal exception; do not request another source read.", tool, planThenExecuteSubmitPlanCoverageSentence()), prompt, true
}

func planThenExecuteSubmitCorrectionWrongToolRejection(tool string, blockerAllowed bool) (string, string, string) {
	tool = strings.TrimSpace(tool)
	if tool == "" {
		tool = "the requested tool"
	}

	outcome := "Do not call `read_file`, `code_navigate`, search, shell, git, source-sanity, or per-edit write tools in this state. Do not answer `Decision: source-satisfied`, do not answer `<<QUESTION>>:`, and do not describe the correction in prose."
	errorSuffix := "No terminal source-satisfied/question/prose fallback is valid in this state."
	if blockerAllowed {
		outcome = "Do not call `read_file`, `code_navigate`, search, shell, git, source-sanity, or per-edit write tools in this state. Do not answer `Decision: source-satisfied`. If no safe executable edit body can be produced from the gathered evidence after these rejected submit_plan attempts, answer `<<QUESTION>>:` with the exact path-specific missing body/anchor blocker."
		errorSuffix = "`<<QUESTION>>:` is valid only for the exact path-specific missing body/anchor blocker; no source-satisfied/prose fallback is valid in this state."
	}

	prompt := strings.Join([]string{
		"Plan-execute submit-correction mode is now the only allowed planner action. Every required_edit_path already has source-body evidence, and the previous `submit_plan` was rejected before it became a valid plan artifact.",
		"The next visible action must be exactly one parser-readable `submit_plan` JSON object. " + planThenExecuteSubmitPlanCoverageSentence() + " " + planThenExecuteSubmitPlanVisibleJSONInstruction(),
		outcome,
	}, "\n")

	return "Corrected submit_plan required",
		fmt.Sprintf("%s is not available because plan-execute submit-correction mode exposes only `submit_plan`. Emit one parser-readable `submit_plan` JSON object now. %s %s", tool, planThenExecuteSubmitPlanCoverageSentence(), errorSuffix),
		prompt
}

func planThenExecuteSubmitCorrectionCurrentSurfaceRejection(req workspace.ToolRequest, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) (string, string, string, bool) {
	if strings.TrimSpace(req.Tool) == "" || strings.TrimSpace(req.Tool) == workspace.ToolNameSubmitPlan {
		return "", "", "", false
	}

	if !planThenExecuteSubmitCorrectionActive(brief, results) {
		return "", "", "", false
	}

	summary, errorText, prompt := planThenExecuteSubmitCorrectionWrongToolRejection(req.Tool, planThenExecuteSubmitCorrectionBlockerAllowed(brief, results))

	return summary, errorText, prompt, true
}

func planThenExecuteReadinessNudgedNonSubmitRejection(req workspace.ToolRequest, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, readinessNudged bool) (string, string, string, bool) {
	if !readinessNudged ||
		!planThenExecutePlannerBrief(brief) ||
		successfulSubmitPlanResultCount(results) > 0 ||
		strings.TrimSpace(req.Tool) == "" ||
		strings.TrimSpace(req.Tool) == workspace.ToolNameSubmitPlan {

		return "", "", "", false
	}

	if _, _, rejected := lastRejectedSubmitPlanCoverageResult(results); rejected {
		return "", "", "", false
	}

	tool := strings.TrimSpace(req.Tool)
	prompt := strings.Join([]string{
		"Plan-execute planner readiness was already reached before this request. The first terminal planner action is now required, so discovery, search, source refresh, verification, and per-edit write tools are closed until a `submit_plan` is attempted.",
		"The next visible action must be a parser-readable `submit_plan` JSON object. " + planThenExecuteSubmitPlanCoverageSentence() + " " + planThenExecuteSubmitPlanVisibleJSONInstruction(),
		"Only use `Decision: source-satisfied` if the current source evidence proves every selected path already satisfies the requested final state. Only use `<<QUESTION>>:` if one exact path-specific blocker prevents drafting any plan edit.",
	}, "\n")

	return "Plan-execute submit_plan required",
		fmt.Sprintf("%s is not available after plan-execute readiness. The planner must attempt `submit_plan` before any more tool calls; use a terminal source-satisfied decision or exact blocker only if no executable edit can be drafted.", tool),
		prompt,
		true
}

func planThenExecuteTerminalSubmitPlanSurfaceReady(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if planThenExecuteSubmitPlanOnlyReady(brief, results) {
		return true
	}

	state := DerivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true})
	return state.Mode == PlanExecutePlanningModeSubmitReady || state.Mode == PlanExecutePlanningModeSubmitCorrection
}

func planThenExecuteRequiredSourceRequestAdvances(req workspace.ToolRequest, path string, evidence planThenExecuteSourceEvidence, results []workspace.ToolResult) bool {
	path = normalizeRunnerPath(path)
	if path == "" {
		return false
	}

	switch strings.TrimSpace(req.Tool) {
	case workspace.ToolNameReadFile:
		start, end := req.StartLine, req.EndLine
		if start <= 0 && end <= 0 {
			return evidence.Level == planThenExecuteEvidenceNone
		}

		if start <= 0 {
			start = 1
		}

		if end <= 0 || end < start {
			return false
		}

		if planThenExecuteReadRangeSeen(path, start, end, results) {
			return false
		}

		if planThenExecuteLargeGoRawRangeNeedsSymbolBody(path, planThenExecuteRequiredSourceReadTotal(path, results)) &&
			!planThenExecuteFileOutlineSeen(path, results) {

			return false
		}

		if start == 1 && end <= planThenExecuteGenericHeaderReadMaxLines && planThenExecutePathHasOutlineEvidence(path, results) {
			return false
		}

		if start == 1 && end <= planThenExecuteGenericHeaderReadMaxLines &&
			planThenExecuteEvidenceRank(evidence.Level) >= planThenExecuteEvidenceRank(planThenExecuteEvidenceBodyWindow) {

			return false
		}

		return true
	case workspace.ToolNameCodeNavigate, workspace.ToolNameReadOnlyCapability:
		capability := strings.TrimSpace(req.Capability)
		switch {
		case capability == "file_outline":
			return !planThenExecuteFileOutlineSeen(path, results)
		case capability == "read_symbol" || capability == "extract_body" ||
			strings.HasSuffix(capability, "_read_symbol") ||
			strings.HasSuffix(capability, "_extract_body"):
			symbol := strings.TrimSpace(req.Pattern)
			if symbol == "" {
				symbol = strings.TrimSpace(req.Symbol)
			}

			return symbol == "" || !planThenExecuteReadSymbolSeen(path, symbol, results)
		default:
			return false
		}
	default:
		tool := strings.TrimSpace(req.Tool)
		if strings.HasSuffix(tool, "_read_symbol") || strings.HasSuffix(tool, "_extract_body") {
			return true
		}

		return false
	}
}

func planThenExecuteRequiredSourceReadTotal(path string, results []workspace.ToolResult) int {
	path = normalizeRunnerPath(path)
	if path == "" {
		return 0
	}

	total := 0
	lowerPath := strings.ToLower(path)

	for _, result := range results {
		if strings.TrimSpace(result.Tool) != workspace.ToolNameReadFile || strings.TrimSpace(result.Error) != "" {
			continue
		}

		text := strings.ToLower(strings.Join([]string{result.Summary, result.Output}, "\n"))
		if !strings.Contains(text, lowerPath) {
			continue
		}

		if lineCount, ok := planThenExecuteReadSummaryTotalLines(result.Summary); ok && lineCount > total {
			total = lineCount
		}
	}

	return total
}

func planThenExecuteReadRangeSeen(path string, start, end int, results []workspace.ToolResult) bool {
	path = normalizeRunnerPath(path)
	if path == "" || start <= 0 || end < start {
		return false
	}

	for _, result := range results {
		if strings.TrimSpace(result.Tool) != workspace.ToolNameReadFile || strings.TrimSpace(result.Error) != "" {
			continue
		}

		text := strings.ToLower(strings.Join([]string{result.Summary, result.Output}, "\n"))
		if !strings.Contains(text, strings.ToLower(path)) {
			continue
		}

		priorStart, priorEnd, ok := planThenExecuteReadSummaryLineRange(result.Summary)
		if ok && priorStart == start && priorEnd == end {
			return true
		}
	}

	return false
}

func (r *Runner) deterministicImplementationRequiredSourceFollowup(path string, results []workspace.ToolResult) (workspace.ToolRequest, bool) {
	return r.deterministicRequiredSourceFollowup(path, results, requiredSourceFollowupWording{
		OutlineGap:       "source-body line anchors for required implementation edit path",
		SymbolGap:        "source-body evidence for required implementation edit path",
		OutlineWhy:       "whole-file read redirected; need declarations before selecting a source-body read",
		SymbolWhy:        "need current declaration body before editing this required path",
		RangeWhy:         "outline did not produce a readable symbol; need bounded source before editing this required path",
		FallbackRangeWhy: "outline did not expose usable declarations; need bounded source before editing this required path",
	})
}

type requiredSourceFollowupWording struct {
	OutlineGap       string
	SymbolGap        string
	OutlineWhy       string
	SymbolWhy        string
	RangeWhy         string
	FallbackRangeWhy string
}

func (r *Runner) deterministicRequiredSourceFollowup(path string, results []workspace.ToolResult, wording requiredSourceFollowupWording) (workspace.ToolRequest, bool) {
	path = normalizeRunnerPath(path)
	if path == "" {
		return workspace.ToolRequest{}, false
	}

	if strings.TrimSpace(wording.OutlineGap) == "" {
		wording.OutlineGap = "source-body line anchors for required edit path"
	}

	if strings.TrimSpace(wording.SymbolGap) == "" {
		wording.SymbolGap = "source-body evidence for required edit path"
	}

	if !planThenExecutePathSupportsCodeNavigate(path) {
		return deterministicRequiredSourceRangeFollowup(path, results, wording)
	}

	if !planThenExecuteFileOutlineSeen(path, results) {
		return workspace.ToolRequest{
			Tool:            workspace.ToolNameCodeNavigate,
			Path:            path,
			Capability:      "file_outline",
			NamedGap:        wording.OutlineGap,
			WhyNotAnswering: wording.OutlineWhy,
		}, true
	}

	if symbol := planThenExecuteBestOutlineSymbol(path, r.RuntimeBrief.Objective, results); symbol != "" && !planThenExecuteReadSymbolSeen(path, symbol, results) {
		return workspace.ToolRequest{
			Tool:            workspace.ToolNameCodeNavigate,
			Path:            path,
			Capability:      "read_symbol",
			Pattern:         symbol,
			NamedGap:        wording.SymbolGap,
			WhyNotAnswering: wording.SymbolWhy,
		}, true
	}

	if start, end, ok := planThenExecuteBestOutlineRange(path, r.RuntimeBrief.Objective, results); ok {
		return workspace.ToolRequest{
			Tool:            workspace.ToolNameReadFile,
			Path:            path,
			StartLine:       start,
			EndLine:         end,
			NamedGap:        wording.SymbolGap,
			WhyNotAnswering: wording.RangeWhy,
		}, true
	}

	return deterministicRequiredSourceRangeFollowup(path, results, wording)
}

func deterministicRequiredSourceRangeFollowup(path string, results []workspace.ToolResult, wording requiredSourceFollowupWording) (workspace.ToolRequest, bool) {
	start, end, ok := planThenExecuteNextRequiredSourceReadRange(path, results)
	if !ok {
		return workspace.ToolRequest{}, false
	}

	return workspace.ToolRequest{
		Tool:            workspace.ToolNameReadFile,
		Path:            path,
		StartLine:       start,
		EndLine:         end,
		NamedGap:        wording.SymbolGap,
		WhyNotAnswering: wording.FallbackRangeWhy,
	}, true
}

func planThenExecuteNextRequiredSourceReadRange(path string, results []workspace.ToolResult) (int, int, bool) {
	path = normalizeRunnerPath(path)
	if path == "" {
		return 0, 0, false
	}

	maxEnd, total := 0, 0
	sawReadFileEvidence := false

	for _, result := range results {
		if strings.TrimSpace(result.Tool) != workspace.ToolNameReadFile || strings.TrimSpace(result.Error) != "" {
			continue
		}

		text := strings.ToLower(strings.Join([]string{result.Summary, result.Output}, "\n"))
		if !strings.Contains(text, strings.ToLower(path)) {
			continue
		}

		if !strings.Contains(text, "redirected") || !planThenExecutePathSupportsCodeNavigate(path) {
			sawReadFileEvidence = true
		}

		if priorStart, priorEnd, ok := planThenExecuteReadSummaryLineRange(result.Summary); ok && priorStart > 0 && priorEnd >= priorStart && priorEnd > maxEnd {
			maxEnd = priorEnd
		}

		if lineCount, ok := planThenExecuteReadSummaryTotalLines(result.Summary); ok && lineCount > total {
			total = lineCount
		}
	}

	if sawReadFileEvidence && maxEnd == 0 {
		return 0, 0, false
	}

	start := 1
	if maxEnd > 0 {
		start = maxEnd + 1
	}

	if total > 0 && start > total {
		return 0, 0, false
	}

	end := start + 179
	if total > 0 && end > total {
		end = total
	}

	if start <= 0 || end < start {
		return 0, 0, false
	}

	return start, end, true
}

func planThenExecutePathSupportsCodeNavigate(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go", ".py", ".ts", ".tsx", ".js", ".jsx", ".rs", ".java", ".kt", ".kts", ".c", ".cc", ".cpp", ".cxx", ".h", ".hpp", ".hxx":
		return true
	default:
		return false
	}
}

func planThenExecuteFileOutlineSeen(path string, results []workspace.ToolResult) bool {
	path = normalizeRunnerPath(path)
	if path == "" {
		return false
	}

	lowerPath := strings.ToLower(path)
	for _, result := range results {
		text := strings.ToLower(result.Summary + "\n" + result.Output)
		if strings.TrimSpace(result.Error) != "" || !strings.Contains(text, lowerPath) {
			continue
		}

		if strings.Contains(text, "capability: file_outline") ||
			strings.Contains(text, "routed file_outline") ||
			strings.Contains(text, "top-level declaration") {

			return true
		}
	}

	return false
}

func planThenExecuteReadSymbolSeen(path, symbol string, results []workspace.ToolResult) bool {
	path = normalizeRunnerPath(path)
	symbol = strings.ToLower(strings.TrimSpace(symbol))

	if path == "" || symbol == "" {
		return false
	}

	for _, result := range results {
		text := strings.ToLower(result.Summary + "\n" + result.Error + "\n" + result.Output)
		if strings.TrimSpace(result.Tool) == workspace.ToolNameCodeNavigate &&
			strings.Contains(text, "read_symbol") &&
			strings.Contains(text, strings.ToLower(path)) &&
			strings.Contains(text, symbol) {

			return true
		}
	}

	return false
}

func planThenExecuteBestOutlineSymbol(path, objective string, results []workspace.ToolResult) string {
	path = normalizeRunnerPath(path)
	if path == "" {
		return ""
	}

	objectiveText := objective + " " + filepath.Base(path)
	objectiveTokens := planThenExecuteSymbolScoreTokens(objectiveText)
	best, bestScore := "", 0

	for _, result := range results {
		text := result.Summary + "\n" + result.Output
		if strings.TrimSpace(result.Error) != "" || !strings.Contains(strings.ToLower(text), strings.ToLower(path)) {
			continue
		}

		for _, match := range fileOutlineDeclarationPattern.FindAllStringSubmatch(text, -1) {
			if len(match) < 2 {
				continue
			}

			name := strings.TrimSpace(match[1])
			score := planThenExecuteSymbolScore(path, name, objectiveText, objectiveTokens)

			if score > bestScore {
				best, bestScore = name, score
			}
		}

		for _, name := range planThenExecutePreviewDeclarationNames(text) {
			score := planThenExecuteSymbolScore(path, name, objectiveText, objectiveTokens)
			if score > bestScore {
				best, bestScore = name, score
			}
		}
	}

	return best
}

func planThenExecutePreviewDeclarationNames(text string) []string {
	var names []string

	seen := map[string]struct{}{}

	for _, match := range fileOutlinePreviewPattern.FindAllStringSubmatch(text, -1) {
		if len(match) < 2 {
			continue
		}

		for _, raw := range strings.Split(match[1], ",") {
			name := strings.TrimSpace(raw)
			if name == "" || strings.HasPrefix(name, "...") || strings.Contains(name, " more") {
				continue
			}

			if _, ok := seen[name]; ok {
				continue
			}

			seen[name] = struct{}{}
			names = append(names, name)
		}
	}

	return names
}

func planThenExecuteBestOutlineRange(path, objective string, results []workspace.ToolResult) (int, int, bool) {
	path = normalizeRunnerPath(path)
	if path == "" {
		return 0, 0, false
	}

	objectiveText := objective + " " + filepath.Base(path)
	objectiveTokens := planThenExecuteSymbolScoreTokens(objectiveText)
	bestStart, bestEnd, bestScore := 0, 0, 0

	for _, result := range results {
		text := result.Summary + "\n" + result.Output
		if strings.TrimSpace(result.Error) != "" || !strings.Contains(strings.ToLower(text), strings.ToLower(path)) {
			continue
		}

		for _, match := range fileOutlineDeclarationPattern.FindAllStringSubmatch(text, -1) {
			if len(match) < 4 {
				continue
			}

			start, startErr := strconv.Atoi(match[2])
			end, endErr := strconv.Atoi(match[3])

			if startErr != nil || endErr != nil || start <= 0 || end < start {
				continue
			}

			score := planThenExecuteSymbolScore(path, strings.TrimSpace(match[1]), objectiveText, objectiveTokens)
			if score > bestScore {
				bestStart, bestEnd, bestScore = start, end, score
			}
		}
	}

	if bestScore <= 0 {
		return 0, 0, false
	}

	return bestStart, bestEnd, true
}

func planThenExecuteSymbolScore(path, name, objective string, objectiveTokens map[string]int) int {
	score := 0
	for token := range planThenExecuteSymbolScoreTokens(name) {
		score += objectiveTokens[token]
	}

	lowerName := strings.ToLower(name)
	lowerPath := strings.ToLower(filepath.ToSlash(path))

	if strings.HasSuffix(lowerPath, "_test.go") && strings.HasPrefix(lowerName, "test") {
		score += 4
	}

	if strings.HasPrefix(lowerPath, "cmd/") && strings.HasPrefix(lowerName, "run") {
		score += 4
	}

	if strings.Contains(lowerName, "validator") {
		score -= 3
	}

	return score
}

func planThenExecuteSymbolScoreTokens(text string) map[string]int {
	out := make(map[string]int)
	runes := []rune(text)

	var current strings.Builder

	flush := func() {
		token := strings.ToLower(strings.TrimSpace(current.String()))
		if len(token) >= 3 {
			out[token]++
		}

		current.Reset()
	}

	for i, r := range runes {
		if r == '_' || r == '-' || r == '/' || r == '.' || !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			flush()

			continue
		}

		if unicode.IsUpper(r) && current.Len() > 0 {
			prev := runes[i-1]

			var next rune

			if i+1 < len(runes) {
				next = runes[i+1]
			}

			if unicode.IsLower(prev) || unicode.IsDigit(prev) || (next != 0 && unicode.IsLower(next)) {
				flush()
			}
		}

		current.WriteRune(unicode.ToLower(r))
	}

	flush()

	return out
}

func planThenExecutePathHasAnyToolEvidence(path string, results []workspace.ToolResult) bool {
	normalized := normalizeRunnerPath(path)
	if normalized == "" {
		return false
	}

	for _, result := range results {
		text := strings.ToLower(strings.Join([]string{result.Summary, result.Error, result.Output}, "\n"))
		if strings.Contains(text, strings.ToLower(normalized)) {
			return true
		}
	}

	return false
}

func (r *Runner) deterministicNoWriteRecoveryUnreadCandidateRead(results []workspace.ToolResult) (workspace.ToolRequest, string, bool) {
	if !r.noWriteRecoveryAllowed(results) {
		return workspace.ToolRequest{}, "", false
	}

	if r.planThenExecutePlannerPhase() {
		return workspace.ToolRequest{}, "", false
	}

	if successfulSubmitPlanResultCount(results) > 0 || len(readPathsFromResults(results)) == 0 {
		return workspace.ToolRequest{}, "", false
	}

	if last := lastNonGateTool(results); last != workspace.ToolNameReadFile && last != workspace.ToolNameSearchContent && last != workspace.ToolNameCodeNavigate {
		return workspace.ToolRequest{}, "", false
	}

	paths := unreadCandidateRecoveryScopePaths(r.RuntimeBrief, results, 4)
	if completionGateResultSeen(results) {
		paths = unreadCandidateRecoveryScopePathsForRecovery(r.RuntimeBrief, results, 4)
	}

	if len(paths) == 0 {
		return workspace.ToolRequest{}, "", false
	}

	return noWriteRecoveryReadRequest(
		paths[0],
		"exact current code needed before writing the requested implementation change",
		"need current source before the write",
	)
}

func (r *Runner) deterministicPlanExecuteNonToolCandidateRecovery(reply string, results []workspace.ToolResult) (workspace.ToolRequest, string, bool) {
	// Open-candidate localization is strategic work, not a hard invariant.
	// Required edit paths and rejected stale anchors may still use deterministic
	// recovery, but open-ended plan-execute prose must be repaired by a
	// model-facing contract nudge/rejection instead of a host-chosen probe.
	return workspace.ToolRequest{}, "", false
}

func (r *Runner) deterministicPlanExecuteChainedSearchRecovery(req workspace.ToolRequest, results []workspace.ToolResult, executionState *runnerExecutionState) (workspace.ToolRequest, string, bool) {
	// Chained search is strategy, not a hard invariant. Exact-repeat and
	// no-progress guards still prevent loops; otherwise the model owns whether
	// a follow-up search or a source read is the better next probe.
	return workspace.ToolRequest{}, "", false
}

func (r *Runner) deterministicNoWriteRecoveryTargetRead(results []workspace.ToolResult) (workspace.ToolRequest, string, bool) {
	if !r.noWriteRecoveryAllowed(results) || lastNonGateTool(results) != "" {
		return workspace.ToolRequest{}, "", false
	}

	for _, ref := range r.RuntimeBrief.TargetRefs {
		path := strings.TrimSpace(filepath.ToSlash(ref))
		if path == "" || !recoverySearchPathLooksEditableSource(path) || alreadyReadPath(results, path) {
			continue
		}

		return noWriteRecoveryReadRequest(
			path,
			"exact current code needed before submitting the requested plan",
			"need current source before submit_plan can anchor the edit",
		)
	}

	return workspace.ToolRequest{}, "", false
}

func (r *Runner) deterministicNoWriteRecoverySearch(results []workspace.ToolResult) (workspace.ToolRequest, string, bool) {
	if !r.noWriteRecoveryAllowed(results) || (lastNonGateTool(results) != workspace.ToolNameContextPack && !lastNonGateAttemptWasUnavailableContextPack(results)) {
		return workspace.ToolRequest{}, "", false
	}

	if len(concreteRecoveryEvidencePaths(results, 1)) > 0 || successfulToolSeen(results, workspace.ToolNameSearchContent) {
		return workspace.ToolRequest{}, "", false
	}

	pattern := literalRecoverySearchPattern(r.RuntimeBrief.Objective)
	if pattern == "" {
		return workspace.ToolRequest{}, "", false
	}

	req := workspace.ToolRequest{
		Tool:            workspace.ToolNameSearchContent,
		Path:            ".",
		Pattern:         pattern,
		NamedGap:        "locate exact current source for the requested behavior",
		WhyNotAnswering: "need a grounded source hit before reading or writing",
	}

	return req, marshalToolRequestRaw(req), true
}
