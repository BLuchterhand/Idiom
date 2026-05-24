package agent

import (
	"slices"
	"sort"
	"strings"

	"idiom/internal/frontier/workerruntime"
	"idiom/internal/workspace"
)

type PlanExecutePlanningMode string

const (
	PlanExecutePlanningModeNone             PlanExecutePlanningMode = ""
	PlanExecutePlanningModeRequiredSource   PlanExecutePlanningMode = "required_source"
	PlanExecutePlanningModeOpenCandidate    PlanExecutePlanningMode = "open_candidate"
	PlanExecutePlanningModeAllowedCandidate PlanExecutePlanningMode = "allowed_candidate"
	PlanExecutePlanningModeSubmitReady      PlanExecutePlanningMode = "submit_ready"
	PlanExecutePlanningModeSubmitCorrection PlanExecutePlanningMode = "submit_correction"
	PlanExecutePlanningModeReviewRepair     PlanExecutePlanningMode = "review_repair"
	PlanExecutePlanningModeDone             PlanExecutePlanningMode = "done"
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
	AllowedCandidatePaths  []string
	EvidencePaths          []string
	SelectedEditPaths      []string
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

func DerivePlanExecutePlanningActionState(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, transport PlanExecutePlanningTransport, req workspace.ToolRequest) PlanExecutePlanningState {
	state := DerivePlanExecutePlanningState(brief, results, transport)
	if reason, ok := planThenExecuteLowValueActionReason(req, brief, results, state); ok {
		state.LowValueActions = []string{reason}
	}
	return state
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
	state.AllowedCandidatePaths = planThenExecuteCandidateScopePaths(brief, results, 16)
	state.EvidencePaths = sortedRunnerPathKeys(readPathsFromResults(results))
	state.LastInvalidPaths = planThenExecuteLastInvalidSubmitPlanPaths(results)
	state.TerminalOptions = []string{"source_satisfied", "blocker"}

	switch {
	case len(state.LastInvalidPaths) > 0 && planThenExecuteRejectedPlanCorrectionOnly(brief, results):
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
		if len(state.EvidencePaths) > 0 {
			state.Mode = PlanExecutePlanningModeOpenCandidate
		} else {
			state.Mode = PlanExecutePlanningModeAllowedCandidate
		}
	}

	state.SelectedEditPaths = planThenExecuteSelectedEditPaths(brief, results, state)
	if state.Mode == PlanExecutePlanningModeSubmitReady ||
		state.Mode == PlanExecutePlanningModeSubmitCorrection ||
		len(state.SelectedEditPaths) > 0 {
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
		state.ParserVisibleTools = []string{"read_file", "code_navigate", "submit_plan"}
		state.CallableTools = []string{"read_file", "code_navigate"}
		state.AllowedActionClasses = []string{actionRead, actionSourceSatisfied, actionBlocker}
		state.InvalidActionClasses = []string{actionSubmitPlan, actionPerEditWrite, actionSearch}
		state.TerminalResponseShapes = []string{"Decision: source-satisfied", "<<QUESTION>>:"}
	case PlanExecutePlanningModeAllowedCandidate, PlanExecutePlanningModeOpenCandidate:
		state.PhaseID = turnPhaseCandidateSource
		state.Phase = "candidate_source_pending"
		state.DerivationReason = "plan_execute_unscoped_candidate_source_pending"
		state.ParserVisibleTools = []string{
			"context_pack", "context_query", "context_zoom", "discovery_query",
			"search_files", "search_content", "read_file", "code_navigate",
			"product_efforts", "product_status", "recovery_inspect", "recovery_status",
			"submit_plan",
		}
		state.CallableTools = []string{
			"context_pack", "context_query", "context_zoom", "discovery_query",
			"search_files", "search_content", "read_file", "code_navigate",
			"product_efforts", "product_status", "recovery_inspect", "recovery_status",
		}
		state.AllowedActionClasses = []string{actionRead, actionSearch, actionBlocker, actionSourceSatisfied}
		state.InvalidActionClasses = []string{actionSubmitPlan, actionPerEditWrite, actionVerify, actionGenericProbe}
		state.TerminalResponseShapes = []string{"Decision: source-satisfied", "<<QUESTION>>:"}
		if slices.Contains(state.TerminalOptions, "submit_plan") {
			state.CallableTools = append(state.CallableTools, "submit_plan")
			state.AllowedActionClasses = append(state.AllowedActionClasses, actionSubmitPlan)
			state.InvalidActionClasses = []string{actionPerEditWrite, actionVerify, actionGenericProbe}
			state.TerminalResponseShapes = append(state.TerminalResponseShapes, "submit_plan")
		}
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
		state.TerminalResponseShapes = []string{"submit_plan", "Decision: source-satisfied", "<<QUESTION>>:"}
		if planExecuteSubmitReadyNativeRequired(transport) {
			state.PhaseID = turnPhaseSubmitReadyNativeRequired
			state.Phase = "submit_ready_native"
			state.AllowedActionClasses = []string{actionSubmitPlan}
			state.TerminalResponseShapes = []string{"submit_plan"}
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
