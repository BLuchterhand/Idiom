package agent

import (
	"slices"
	"sort"
	"strings"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/runtime/workerruntime"
)

type PlanExecutePlanningMode string

const (
	PlanExecutePlanningModeNone             PlanExecutePlanningMode = ""
	PlanExecutePlanningModeRequiredSource   PlanExecutePlanningMode = "required_source"
	PlanExecutePlanningModeOpenCandidate    PlanExecutePlanningMode = "open_candidate"
	PlanExecutePlanningModeAllowedCandidate PlanExecutePlanningMode = "allowed_candidate"
	PlanExecutePlanningModeHistoryPending   PlanExecutePlanningMode = "history_pending"
	PlanExecutePlanningModeAnchorRefresh    PlanExecutePlanningMode = "anchor_refresh"
	PlanExecutePlanningModeSubmitReady      PlanExecutePlanningMode = "submit_ready"
	PlanExecutePlanningModeSubmitCorrection PlanExecutePlanningMode = "submit_correction"
	PlanExecutePlanningModeReviewRepair     PlanExecutePlanningMode = "review_repair"
	PlanExecutePlanningModeDone             PlanExecutePlanningMode = "done"
)

type PlanExecuteRecoverySurfaceKind string

const (
	PlanExecuteRecoverySurfaceNone                    PlanExecuteRecoverySurfaceKind = ""
	PlanExecuteRecoverySurfaceAnchorRefresh           PlanExecuteRecoverySurfaceKind = "anchor_refresh"
	PlanExecuteRecoverySurfaceHistoryPending          PlanExecuteRecoverySurfaceKind = "history_pending"
	PlanExecuteRecoverySurfaceSubmitCorrection        PlanExecuteRecoverySurfaceKind = "submit_correction"
	PlanExecuteRecoverySurfaceSubmitReady             PlanExecuteRecoverySurfaceKind = "submit_ready"
	PlanExecuteRecoverySurfaceSourceSatisfiedTerminal PlanExecuteRecoverySurfaceKind = "source_satisfied_terminal"
)

type PlanExecuteTransportMode string

const (
	PlanExecuteTransportModeTextual     PlanExecuteTransportMode = "textual_tools"
	PlanExecuteTransportModeTextualJSON PlanExecuteTransportMode = "textual_json_terminal"
	PlanExecuteTransportModeNative      PlanExecuteTransportMode = "native_tools"
)

type PlanExecutePlanningTransport struct {
	SupportsNativeTools bool
	ForceNativeTools    bool
	TextualToolParser   bool
	TerminalOnly        bool
	ResponseFormatJSON  bool
	EffectiveMaxTokens  int
	TimeoutMillis       int
}

type PlanExecuteRuntimePolicy struct {
	SupportsNativeTools bool
	ForceNativeTools    bool
	TextualToolParser   bool
	ResponseFormatJSON  bool
	EffectiveMaxTokens  int
	TimeoutMillis       int
}

type PlanExecutePlanningState struct {
	Mode                   PlanExecutePlanningMode
	PhaseID                string
	Phase                  string
	DerivationReason       string
	RequiredCoveragePaths  []string
	RequiredPathStates     []string
	AllowedCandidatePaths  []string
	EvidencePaths          []string
	SelectedEditPaths      []string
	RefreshPaths           []string
	TerminalOptions        []string
	LowValueActions        []string
	ParserVisibleTools     []string
	CallableTools          []string
	AllowedActionClasses   []string
	InvalidActionClasses   []string
	TerminalResponseShapes []string
	TransportMode          PlanExecuteTransportMode
	RuntimePolicy          PlanExecuteRuntimePolicy
	LastInvalidPaths       []string
}

type PlanExecuteRecoverySurface struct {
	Kind  PlanExecuteRecoverySurfaceKind
	State PlanExecutePlanningState
}

func DerivePlanExecutePlanningActionState(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, transport PlanExecutePlanningTransport, req workspace.ToolRequest) PlanExecutePlanningState {
	state := DerivePlanExecutePlanningState(brief, results, transport)
	if reason, ok := planThenExecuteLowValueActionReason(req, brief, results, state); ok {
		state.LowValueActions = []string{reason}
	}
	return state
}

func DerivePlanExecuteRecoverySurface(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, transport PlanExecutePlanningTransport) PlanExecuteRecoverySurface {
	state := DerivePlanExecutePlanningState(brief, results, transport)
	return PlanExecuteRecoverySurface{
		Kind:  planExecuteRecoverySurfaceKind(brief, results, state),
		State: state,
	}
}

func planExecuteRecoverySurfaceKind(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, state PlanExecutePlanningState) PlanExecuteRecoverySurfaceKind {
	if !planThenExecutePlannerBrief(brief) {
		return PlanExecuteRecoverySurfaceNone
	}
	if state.Mode == PlanExecutePlanningModeAnchorRefresh && len(state.RefreshPaths) > 0 {
		return PlanExecuteRecoverySurfaceAnchorRefresh
	}
	if state.Mode == PlanExecutePlanningModeHistoryPending {
		return PlanExecuteRecoverySurfaceHistoryPending
	}
	if planExecuteSourceSatisfiedTerminalEvidenceComplete(brief, results, state) {
		return PlanExecuteRecoverySurfaceSourceSatisfiedTerminal
	}
	if state.Mode == PlanExecutePlanningModeSubmitCorrection {
		return PlanExecuteRecoverySurfaceSubmitCorrection
	}
	if state.Mode == PlanExecutePlanningModeSubmitReady || planThenExecuteTerminalSubmitPlanAllowed(brief, results) {
		return PlanExecuteRecoverySurfaceSubmitReady
	}
	return PlanExecuteRecoverySurfaceNone
}

func planExecuteSourceSatisfiedTerminalEvidenceComplete(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, state PlanExecutePlanningState) bool {
	if !planThenExecuteSourceSatisfiedTerminalRequiredActive(results) || len(state.RefreshPaths) > 0 {
		return false
	}
	if len(state.RequiredCoveragePaths) > 0 {
		return len(planThenExecuteUnreadRequiredPaths(brief, results)) == 0
	}
	return len(state.EvidencePaths) > 0 || len(state.SelectedEditPaths) > 0
}

func DerivePlanExecutePlanningState(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, transport PlanExecutePlanningTransport) PlanExecutePlanningState {
	state := PlanExecutePlanningState{
		RuntimePolicy: PlanExecuteRuntimePolicy{
			SupportsNativeTools: transport.SupportsNativeTools,
			ForceNativeTools:    transport.ForceNativeTools,
			TextualToolParser:   transport.TextualToolParser,
			ResponseFormatJSON:  transport.ResponseFormatJSON,
			EffectiveMaxTokens:  transport.EffectiveMaxTokens,
			TimeoutMillis:       transport.TimeoutMillis,
		},
		TransportMode: planExecuteTransportMode(transport),
	}
	if strings.TrimSpace(brief.TaskFamily) == PlanThenExecuteReviewRepairTaskFamily {
		state.Mode = PlanExecutePlanningModeReviewRepair
		return state
	}
	if !planThenExecutePlannerBrief(brief) {
		return state
	}
	if successfulSubmitPlanResultCount(results) > 0 {
		state.Mode = PlanExecutePlanningModeDone
		state.TerminalOptions = []string{"plan_artifact"}
		return state
	}

	state.RequiredCoveragePaths = planThenExecuteRequiredCoveragePaths(brief.RequiredEditPaths, "")
	state.RequiredPathStates = planThenExecuteRequiredPathStateSummaries(brief, results)
	state.AllowedCandidatePaths = candidateRecoveryScopePaths(brief, results, 16)
	state.EvidencePaths = sortedRunnerPathKeys(readPathsFromResults(results))
	state.LastInvalidPaths = planThenExecuteLastInvalidSubmitPlanPaths(results)
	state.TerminalOptions = []string{"source_satisfied", "blocker"}
	unscopedEmptySubmitRecovery := planThenExecuteUnscopedEmptySubmitPlanRejected(brief, results)
	candidateReadyRejection := planThenExecuteCandidateSourceReadyRejectionSeen(results) && !unscopedEmptySubmitRecovery
	if rejected, rejectedAt, ok := lastRejectedSubmitPlanCoverageResult(results); ok {
		state.RefreshPaths = planThenExecuteRejectedPlanRefreshPaths(brief, results, rejected, rejectedAt)
	}

	switch {
	case len(state.RefreshPaths) > 0:
		state.Mode = PlanExecutePlanningModeAnchorRefresh
	case candidateReadyRejection:
		state.Mode = PlanExecutePlanningModeSubmitReady
	case planThenExecuteRejectedPlanCorrectionOnly(brief, results):
		state.Mode = PlanExecutePlanningModeSubmitCorrection
	case len(state.RequiredCoveragePaths) > 0:
		if len(workerruntime.NormalizeRuntimeBrief(brief).RequiredEditSnippets) > 0 || planThenExecuteSubmitPlanOnlyReady(brief, results) {
			state.Mode = PlanExecutePlanningModeSubmitReady
		} else {
			state.Mode = PlanExecutePlanningModeRequiredSource
		}
	case len(normalizedRequiredEditPaths(brief.AllowedEditPaths)) > 0:
		if planThenExecuteTerminalSubmitPlanAllowedLegacy(brief, results) {
			state.Mode = PlanExecutePlanningModeSubmitReady
		} else {
			state.Mode = PlanExecutePlanningModeAllowedCandidate
		}
	default:
		if planThenExecuteSubmitPlanOnlyReady(brief, results) {
			state.Mode = PlanExecutePlanningModeSubmitReady
		} else if len(state.EvidencePaths) > 0 {
			state.Mode = PlanExecutePlanningModeOpenCandidate
		} else {
			state.Mode = PlanExecutePlanningModeAllowedCandidate
		}
	}

	state.SelectedEditPaths = planThenExecuteSelectedEditPaths(brief, results, state)
	if len(state.RefreshPaths) == 0 && !unscopedEmptySubmitRecovery && planThenExecuteHistoryPendingEligible(brief, results, state) {
		if pending := planThenExecuteHistoryPendingPaths(brief, results, state.SelectedEditPaths); len(pending) > 0 {
			state.Mode = PlanExecutePlanningModeHistoryPending
			state.SelectedEditPaths = pending
		}
	}
	if state.Mode == PlanExecutePlanningModeSubmitReady ||
		state.Mode == PlanExecutePlanningModeSubmitCorrection ||
		unscopedEmptySubmitRecovery ||
		(state.Mode != PlanExecutePlanningModeHistoryPending &&
			state.Mode != PlanExecutePlanningModeAnchorRefresh &&
			len(state.SelectedEditPaths) > 0) {
		state.TerminalOptions = append(state.TerminalOptions, "submit_plan")
	}
	state.TerminalOptions = uniquePhaseStrings(state.TerminalOptions)
	state.applyProjection(transport)
	return state
}

func (state *PlanExecutePlanningState) applyProjection(transport PlanExecutePlanningTransport) {
	switch state.Mode {
	case PlanExecutePlanningModeRequiredSource:
		state.PhaseID = turnPhaseRequiredSource
		state.Phase = "needs_source_body"
		state.DerivationReason = "plan_execute_required_source_pending"
		state.ParserVisibleTools = []string{"read_file", "code_navigate"}
		state.CallableTools = []string{"read_file", "code_navigate"}
		state.AllowedActionClasses = []string{actionRead, actionSourceSatisfied, actionBlocker}
		state.InvalidActionClasses = []string{actionSubmitPlan, actionPerEditWrite, actionSearch}
		state.TerminalResponseShapes = planExecuteTerminalSourceSatisfiedOrQuestion()
	case PlanExecutePlanningModeAllowedCandidate, PlanExecutePlanningModeOpenCandidate:
		state.PhaseID = turnPhaseCandidateSource
		state.Phase = "candidate_source_pending"
		state.DerivationReason = "plan_execute_unscoped_candidate_source_pending"
		state.ParserVisibleTools = []string{
			"context_pack", "context_query", "context_zoom", "discovery_query",
			"search_files", "search_content", "read_file", "code_navigate",
			"product_efforts", "product_status", "recovery_inspect", "recovery_status",
		}
		state.CallableTools = []string{
			"context_pack", "context_query", "context_zoom", "discovery_query",
			"search_files", "search_content", "read_file", "code_navigate",
			"product_efforts", "product_status", "recovery_inspect", "recovery_status",
		}
		state.AllowedActionClasses = []string{actionRead, actionSearch, actionBlocker, actionSourceSatisfied}
		state.InvalidActionClasses = []string{actionSubmitPlan, actionPerEditWrite, actionVerify, actionGenericProbe}
		state.TerminalResponseShapes = planExecuteTerminalSourceSatisfiedOrQuestion()
		if slices.Contains(state.TerminalOptions, "submit_plan") {
			state.ParserVisibleTools = append(state.ParserVisibleTools, "submit_plan")
			state.CallableTools = append(state.CallableTools, "submit_plan")
			state.AllowedActionClasses = append(state.AllowedActionClasses, actionSubmitPlan)
			state.InvalidActionClasses = []string{actionPerEditWrite, actionVerify, actionGenericProbe}
			state.TerminalResponseShapes = append(state.TerminalResponseShapes, "submit_plan")
		}
	case PlanExecutePlanningModeHistoryPending:
		state.PhaseID = turnPhaseHistoryPending
		state.Phase = "history_pending"
		state.DerivationReason = "plan_execute_history_evidence_pending"
		state.ParserVisibleTools = planExecuteHistoryPendingToolNames()
		state.CallableTools = planExecuteHistoryPendingToolNames()
		state.AllowedActionClasses = []string{actionRead, actionSourceSatisfied, actionBlocker}
		state.InvalidActionClasses = []string{actionSearch, actionVerify, actionPerEditWrite, actionGenericProbe}
		state.TerminalResponseShapes = planExecuteTerminalSourceSatisfiedOrQuestion()
	case PlanExecutePlanningModeAnchorRefresh:
		state.PhaseID = turnPhaseStaleAnchorRefresh
		state.Phase = "anchor_refresh"
		state.DerivationReason = "rejected_submit_plan_needs_source_refresh"
		state.ParserVisibleTools = planThenExecuteAnchorRefreshToolNames()
		state.CallableTools = planThenExecuteAnchorRefreshToolNames()
		state.AllowedActionClasses = []string{actionRead}
		state.InvalidActionClasses = []string{actionSubmitPlan, actionSourceSatisfied, actionBlocker, actionSearch, actionPerEditWrite, actionProseFinal}
		state.TerminalOptions = nil
		state.TerminalResponseShapes = nil
	case PlanExecutePlanningModeSubmitReady, PlanExecutePlanningModeSubmitCorrection:
		state.PhaseID = turnPhaseSubmitReadyTextual
		state.Phase = "submit_ready_textual"
		state.DerivationReason = "plan_execute_source_ready"
		if state.Mode == PlanExecutePlanningModeSubmitCorrection {
			state.DerivationReason = "plan_execute_submit_plan_correction"
		}
		state.ParserVisibleTools = []string{"submit_plan"}
		state.CallableTools = []string{"submit_plan"}
		state.AllowedActionClasses = []string{actionSubmitPlan, actionSourceSatisfied, actionBlocker}
		state.InvalidActionClasses = []string{actionRead, actionSearch, actionVerify, actionPerEditWrite, actionGenericProbe, actionProseFinal}
		state.TerminalResponseShapes = planExecuteTerminalSubmitPlanSourceSatisfiedQuestion()
		if state.Mode == PlanExecutePlanningModeSubmitCorrection {
			state.AllowedActionClasses = []string{actionSubmitPlan}
			state.InvalidActionClasses = []string{actionRead, actionSearch, actionSourceSatisfied, actionBlocker, actionVerify, actionPerEditWrite, actionGenericProbe, actionProseFinal}
			state.TerminalResponseShapes = planExecuteTerminalSubmitPlanOnly()
		}
		if planExecuteSubmitReadyNativeRequired(transport) {
			state.PhaseID = turnPhaseSubmitReadyNativeRequired
			state.Phase = "submit_ready_native"
			state.AllowedActionClasses = []string{actionSubmitPlan}
			state.TerminalResponseShapes = planExecuteTerminalSubmitPlanOnly()
		}
	}
	state.ParserVisibleTools = uniquePhaseStrings(state.ParserVisibleTools)
	state.CallableTools = uniquePhaseStrings(state.CallableTools)
	state.AllowedActionClasses = uniquePhaseStrings(state.AllowedActionClasses)
	state.InvalidActionClasses = uniquePhaseStrings(state.InvalidActionClasses)
	state.TerminalResponseShapes = uniquePhaseStrings(state.TerminalResponseShapes)
}

func planExecuteSubmitReadyNativeRequired(transport PlanExecutePlanningTransport) bool {
	return transport.ForceNativeTools || (transport.SupportsNativeTools && !transport.TextualToolParser)
}

func planExecuteTransportMode(transport PlanExecutePlanningTransport) PlanExecuteTransportMode {
	if transport.ForceNativeTools || (transport.SupportsNativeTools && !transport.TextualToolParser && !transport.ResponseFormatJSON) {
		return PlanExecuteTransportModeNative
	}
	if transport.TerminalOnly || transport.ResponseFormatJSON {
		return PlanExecuteTransportModeTextualJSON
	}
	return PlanExecuteTransportModeTextual
}

func planThenExecuteHistoryPendingPaths(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, selected []string) []string {
	if !mutationHistoryEvidenceRequired(brief) {
		return nil
	}
	paths := uniqueSortedRunnerPaths(selected)
	if len(paths) == 0 {
		paths = planThenExecuteRequiredCoveragePaths(brief.RequiredEditPaths, "")
	}
	var pending []string
	for _, path := range paths {
		if !concreteSourceEvidenceReadForPath(path, results) {
			continue
		}
		if successfulGitHistoryResultForPath(path, results) {
			continue
		}
		pending = append(pending, path)
	}
	return uniqueSortedRunnerPaths(pending)
}

func planThenExecuteHistoryPendingEligible(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, state PlanExecutePlanningState) bool {
	if len(state.RequiredCoveragePaths) == 0 {
		return true
	}
	return len(planThenExecuteUnreadRequiredPaths(brief, results)) == 0
}

func planThenExecuteSelectedEditPaths(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, state PlanExecutePlanningState) []string {
	if len(state.RequiredCoveragePaths) > 0 {
		return append([]string(nil), state.RequiredCoveragePaths...)
	}
	if len(state.LastInvalidPaths) > 0 {
		return append([]string(nil), state.LastInvalidPaths...)
	}
	candidates := planThenExecuteSelectableEditPaths(brief, results, 16)
	if len(candidates) == 0 {
		return nil
	}
	evidence := normalizedRequiredEditPathSet(state.EvidencePaths)
	var selected []string
	for _, path := range candidates {
		normalized := normalizeRunnerPath(path)
		if normalized == "" {
			continue
		}
		if _, ok := evidence[normalized]; ok {
			selected = append(selected, normalized)
		}
	}
	return uniqueSortedRunnerPaths(selected)
}

func planThenExecuteSubmitPlanSkeletonPaths(state PlanExecutePlanningState) []string {
	switch state.Mode {
	case PlanExecutePlanningModeSubmitCorrection:
		if len(state.RequiredCoveragePaths) > 0 {
			return uniqueSortedRunnerPaths(state.RequiredCoveragePaths)
		}
		return uniqueSortedRunnerPaths(state.LastInvalidPaths)
	case PlanExecutePlanningModeSubmitReady:
		if len(state.RequiredCoveragePaths) > 0 {
			return uniqueSortedRunnerPaths(state.RequiredCoveragePaths)
		}
		return uniqueSortedRunnerPaths(state.SelectedEditPaths)
	case PlanExecutePlanningModeRequiredSource:
		return uniqueSortedRunnerPaths(state.RequiredCoveragePaths)
	case PlanExecutePlanningModeAllowedCandidate, PlanExecutePlanningModeOpenCandidate:
		return uniqueSortedRunnerPaths(state.SelectedEditPaths)
	default:
		return nil
	}
}

func planThenExecuteSelectableEditPaths(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, max int) []string {
	if max <= 0 {
		max = 4
	}
	paths := make([]string, 0, max)
	seen := map[string]struct{}{}
	add := func(path string) {
		normalized := normalizeRunnerPath(path)
		if normalized == "" {
			return
		}
		if _, ok := seen[normalized]; ok {
			return
		}
		seen[normalized] = struct{}{}
		paths = append(paths, normalized)
	}
	for _, path := range normalizedRequiredEditPaths(brief.AllowedEditPaths) {
		add(path)
		if len(paths) >= max {
			return paths
		}
	}
	for _, path := range contextPackCandidatePathsFromResults(results, max) {
		add(path)
		if len(paths) >= max {
			return paths
		}
	}
	return paths
}

func planThenExecuteLastInvalidSubmitPlanPaths(results []workspace.ToolResult) []string {
	rejected, _, ok := lastRejectedSubmitPlanCoverageResult(results)
	if !ok {
		return nil
	}
	pathSet := map[string]struct{}{}
	for path := range submitPlanInvalidAnchorPaths(rejected) {
		if normalized := normalizeRunnerPath(path); normalized != "" {
			pathSet[normalized] = struct{}{}
		}
	}
	for path := range submitPlanMissingRequiredPaths(rejected) {
		if normalized := normalizeRunnerPath(path); normalized != "" {
			pathSet[normalized] = struct{}{}
		}
	}
	for path := range submitPlanMissingSourceGroundingPaths(rejected) {
		if normalized := normalizeRunnerPath(path); normalized != "" {
			pathSet[normalized] = struct{}{}
		}
	}
	return sortedRunnerPathKeys(pathSet)
}

func sortedRunnerPathKeys(paths map[string]struct{}) []string {
	out := make([]string, 0, len(paths))
	for path := range paths {
		if normalized := normalizeRunnerPath(path); normalized != "" {
			out = append(out, normalized)
		}
	}
	return uniqueSortedRunnerPaths(out)
}

func uniqueSortedRunnerPaths(paths []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		normalized := normalizeRunnerPath(path)
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	sort.Strings(out)
	return out
}

func planThenExecuteLowValueActionReason(req workspace.ToolRequest, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, state PlanExecutePlanningState) (string, bool) {
	if strings.TrimSpace(brief.TaskFamily) != PlanThenExecutePlannerTaskFamily ||
		successfulSubmitPlanResultCount(results) > 0 ||
		!isReadClassTool(req) ||
		strings.TrimSpace(req.Tool) == "submit_plan" {
		return "", false
	}
	if len(planThenExecuteRequiredCoveragePaths(brief.RequiredEditPaths, "")) > 0 &&
		len(planThenExecuteUnreadRequiredPaths(brief, results)) > 0 {
		return "", false
	}
	target := normalizeRunnerPath(planThenExecuteRequestTargetPath(req))
	if target == "" && planThenExecuteReadRequestMissingConcreteTarget(req) {
		return "candidate_read_missing_target", true
	}
	return "", false
}

func planThenExecuteReadRequestMissingConcreteTarget(req workspace.ToolRequest) bool {
	switch strings.TrimSpace(req.Tool) {
	case "read_file":
		return strings.TrimSpace(req.Path) == "" &&
			strings.TrimSpace(req.ID) == "" &&
			strings.TrimSpace(req.Handle) == ""
	case "code_navigate":
		capability := strings.ToLower(strings.TrimSpace(req.Capability))
		capability = strings.ReplaceAll(capability, "-", "_")
		switch capability {
		case "find_symbol", "symbol", "symbol_lookup", "lookup",
			"workspace_symbol", "workspace_symbols", "find_symbol_workspace":
			return strings.TrimSpace(req.Pattern) == "" &&
				strings.TrimSpace(req.Query) == ""
		}
		return strings.TrimSpace(req.Path) == "" &&
			strings.TrimSpace(req.ID) == "" &&
			strings.TrimSpace(req.Handle) == "" &&
			strings.TrimSpace(req.Workdir) == ""
	default:
		return false
	}
}

func planThenExecuteLowValueActionShouldReject(reason string) bool {
	switch strings.TrimSpace(reason) {
	case "candidate_read_missing_target":
		return true
	default:
		return false
	}
}

func planThenExecuteLowValueActionRejectSummary(reason string) string {
	switch strings.TrimSpace(reason) {
	case "candidate_read_missing_target":
		return "Plan-execute source read missing target"
	default:
		return "Plan-execute low-value action rejected"
	}
}

func planThenExecuteLowValueActionStopReason(reason string) string {
	switch strings.TrimSpace(reason) {
	case "candidate_read_missing_target":
		return "plan_execute_source_read_missing_target"
	default:
		return "plan_execute_low_value_action_rejected"
	}
}
