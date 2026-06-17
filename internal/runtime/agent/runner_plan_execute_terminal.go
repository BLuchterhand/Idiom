package agent

import (
	"fmt"
	"idiom/internal/capabilities/workspace"
	"idiom/internal/platform/util"
	"idiom/internal/runtime/agent/agentutil"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/workerruntime"
	"strconv"
	"strings"
)

func (r *Runner) prunePlanThenExecuteTerminalSurfaceHistory(messages *[]llama.ChatMessage, reason string) {
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

func (r *Runner) refreshPlanThenExecuteTerminalToolSurface(messages *[]llama.ChatMessage, results []workspace.ToolResult, reason string) {
	if r == nil || !r.planThenExecutePlannerPhase() || messages == nil {
		return
	}

	surface := DerivePlanExecuteRecoverySurface(r.RuntimeBrief, results, PlanExecutePlanningTransport{TextualToolParser: true})
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

	terminalBrief := r.planThenExecuteTerminalToolBrief(results)
	terminalState := workerruntime.RunState{
		Lane:              util.FirstNonEmpty(strings.TrimSpace(terminalBrief.Lane), r.agentName()),
		TaskFamily:        terminalBrief.TaskFamily,
		Contract:          terminalBrief.Contract,
		Phase:             "plan_submit_ready",
		Objective:         terminalBrief.Objective,
		Permission:        terminalBrief.PermissionProfile,
		TargetRefs:        append([]string(nil), terminalBrief.TargetRefs...),
		RequiredEditPaths: append([]string(nil), terminalBrief.RequiredEditPaths...),
		EvidencePaths:     runnerEvidencePathsForBrief(terminalBrief, results, 6),
		Constraints:       append([]string(nil), terminalBrief.Constraints...),
		AllowedTools:      []string{workspace.ToolNameSubmitPlan},
		ToolManifest:      runnerSanitizedToolManifest([]string{workspace.ToolNameSubmitPlan}),
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
	pruned, droppedActions, droppedScaffolding := prunePlanThenExecuteTerminalTextualMessages(*messages)
	compactDropped := 0
	compactEvidenceMessages := 0

	if !nativeTerminal {
		pruned, compactDropped, compactEvidenceMessages = compactPlanThenExecuteTerminalTextualMessages(pruned, terminalBrief, results)
	}

	actionInstruction := r.textualToolProtocolPrompt(terminalBrief)
	submitPlanSkeleton := planThenExecuteTerminalSubmitPlanSkeleton(terminalBrief, results, nativeTerminal)
	renderedState := workerruntime.RenderRunState(terminalState)

	if nativeTerminal {
		transport = "native"
		surfaceTitle = "# Terminal Plan-Execute Native Tool Surface"
		terminalState.ReadinessGate = []string{"native_submit_plan_tool_call_required"}
		terminalState.StopConditions = []string{"submit_plan_returned_ok", "submit_plan_rejected_with_blocker"}
		terminalState.FinalCritic = []string{"submit_plan_called", "edits_or_source_satisfied_paths_cover_required_paths"}
		terminalState.NextActions = []string{"call native submit_plan using edits[] for changed paths and source_satisfied_paths for already-final paths"}
		renderedState = workerruntime.RenderStepInstruction(workerruntime.StepInstruction{
			CurrentState: terminalState,
			Directive:    "Use this current-state packet for the next action. Plan-execute source obligations are closed and this request exposes exactly one native tool: submit_plan. Call native submit_plan now. " + planThenExecuteSubmitPlanCoverageSentence() + " Do not read, search, shell, verify, use per-edit write tools, or answer in prose.",
		})
		actionInstruction = "Native `submit_plan` is the only exposed tool in this request. Use the `submit_plan` tool call now; do not answer in prose."
	}

	terminalPrompt := strings.Join([]string{
		surfaceTitle,
		"",
		"This replaces earlier runtime brief and tool protocol examples for this planner turn. Earlier `read_file`, `code_navigate`, search, shell, source-sanity, and per-edit write examples are stale because source obligations are closed.",
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
		"allowed_tools":                 []string{workspace.ToolNameSubmitPlan},
	}))
}

func (r *Runner) refreshPlanThenExecuteTerminalOutcomeSurface(messages *[]llama.ChatMessage, results []workspace.ToolResult, reason string) {
	if r == nil || !r.planThenExecutePlannerPhase() || messages == nil {
		return
	}

	terminalBrief := r.planThenExecuteTerminalToolBrief(results)
	pruned, droppedActions, droppedScaffolding := prunePlanThenExecuteTerminalTextualMessages(*messages)
	pruned, compactDropped, compactEvidenceMessages := compactPlanThenExecuteTerminalTextualMessages(pruned, terminalBrief, results)
	terminalState := workerruntime.RunState{
		Lane:              util.FirstNonEmpty(strings.TrimSpace(terminalBrief.Lane), r.agentName()),
		TaskFamily:        terminalBrief.TaskFamily,
		Contract:          terminalBrief.Contract,
		Phase:             "plan_terminal_outcome",
		Objective:         terminalBrief.Objective,
		Permission:        terminalBrief.PermissionProfile,
		TargetRefs:        append([]string(nil), terminalBrief.TargetRefs...),
		RequiredEditPaths: append([]string(nil), terminalBrief.RequiredEditPaths...),
		EvidencePaths:     runnerEvidencePathsForBrief(terminalBrief, results, 6),
		Constraints:       append([]string(nil), terminalBrief.Constraints...),
		AllowedTools:      nil,
		ToolManifest:      nil,
		RequiredTools:     nil,
		ReadinessGate:     []string{"terminal_outcome_required"},
		SearchPolicy:      []string{"no read/search/discovery action is valid after submit_plan readiness"},
		StopConditions:    []string{"decision_source_satisfied", "textual_submit_plan_returned_ok", "question_blocker_reported"},
		FinalCritic:       []string{"one_terminal_outcome_only", "per_path_current_state_evidence_for_source_satisfied"},
		NextActions: []string{
			"answer `Decision: source-satisfied` with per-path current-state evidence if every required path already contains the requested final state",
			"or emit one visible textual submit_plan JSON object with real executable edits if changes are still needed",
			"or answer `<<QUESTION>>:` with the exact path-specific blocker",
		},
		InvalidActions: []string{
			"empty or source-satisfied submit_plan transport",
			"read/search/discovery after terminal readiness",
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
		"Do not repeat an empty or source-satisfied `submit_plan` object. Do not call read/search/discovery, shell, verification, or per-edit write tools.",
	}, "\n")

	*messages = append(pruned, llama.ChatMessage{Role: "system", Content: strings.TrimSpace(terminalPrompt)})
	r.emitEvent(marshalRunEvent("plan_then_execute_textual_tool_surface_refreshed", map[string]any{
		"reason":                        strings.TrimSpace(reason),
		"transport":                     "terminal_outcome",
		"dropped_tool_request_messages": droppedActions,
		"dropped_scaffolding_messages":  droppedScaffolding,
		"dropped_compact_messages":      compactDropped,
		"compact_evidence_messages":     compactEvidenceMessages,
		"allowed_tools":                 []string{},
	}))
}

func (r *Runner) refreshPlanThenExecuteTerminalTextualFallbackSurface(messages *[]llama.ChatMessage, results []workspace.ToolResult, reason string) {
	if r == nil || !r.planThenExecutePlannerPhase() || messages == nil || !planThenExecuteTerminalSubmitPlanAllowed(r.RuntimeBrief, results) {
		return
	}

	terminalBrief := r.planThenExecuteTerminalToolBrief(results)
	pruned, droppedActions, droppedScaffolding := prunePlanThenExecuteTerminalTextualMessages(*messages)
	pruned, compactDropped, compactEvidenceMessages := compactPlanThenExecuteTerminalTextualMessages(pruned, terminalBrief, results)
	terminalState := workerruntime.RunState{
		Lane:              util.FirstNonEmpty(strings.TrimSpace(terminalBrief.Lane), r.agentName()),
		TaskFamily:        terminalBrief.TaskFamily,
		Contract:          terminalBrief.Contract,
		Phase:             "plan_submit_ready_textual_fallback",
		Objective:         terminalBrief.Objective,
		Permission:        terminalBrief.PermissionProfile,
		TargetRefs:        append([]string(nil), terminalBrief.TargetRefs...),
		RequiredEditPaths: append([]string(nil), terminalBrief.RequiredEditPaths...),
		EvidencePaths:     runnerEvidencePathsForBrief(terminalBrief, results, 6),
		Constraints:       append([]string(nil), terminalBrief.Constraints...),
		AllowedTools:      []string{workspace.ToolNameSubmitPlan},
		ToolManifest:      runnerSanitizedToolManifest([]string{workspace.ToolNameSubmitPlan}),
		RequiredTools:     []string{workspace.ToolNameSubmitPlan},
		ReadinessGate:     []string{"visible_textual_submit_plan_json_required"},
		SearchPolicy:      []string{"no read/search/discovery action is valid after submit_plan readiness before the first terminal outcome"},
		StopConditions:    []string{"submit_plan_returned_ok", "submit_plan_rejected_with_blocker"},
		FinalCritic:       []string{"submit_plan_called", "edits_or_source_satisfied_paths_cover_required_paths"},
		NextActions:       []string{"emit exactly one visible submit_plan JSON object using edits[] for changed paths and source_satisfied_paths for already-final paths"},
		InvalidActions:    append([]string(nil), terminalBrief.InvalidActions...),
		Readiness:         terminalBrief.Readiness,
	}
	terminalPrompt := strings.Join([]string{
		"# Terminal Plan-Execute Textual Tool Surface",
		"",
		"Native tool-call transport failed to produce a required `submit_plan` call. Native tools are disabled for this retry; the only accepted action is one visible textual `submit_plan` JSON object.",
		"",
		workerruntime.RenderRunState(terminalState),
		"",
		planThenExecuteTerminalSubmitPlanSkeleton(terminalBrief, results, false),
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
		"allowed_tools":                 []string{workspace.ToolNameSubmitPlan},
	}))
}

func (r *Runner) refreshPlanThenExecuteAnchorRefreshSurface(messages *[]llama.ChatMessage, results []workspace.ToolResult, reason string) {
	if r == nil || !r.planThenExecutePlannerPhase() || messages == nil {
		return
	}

	stateProjection := DerivePlanExecutePlanningState(r.RuntimeBrief, results, PlanExecutePlanningTransport{TextualToolParser: true})
	if stateProjection.Mode != PlanExecutePlanningModeAnchorRefresh || len(stateProjection.RefreshPaths) == 0 {
		return
	}

	refreshPaths := stateProjection.RefreshPaths
	pruned, droppedActions, droppedScaffolding := prunePlanThenExecuteTerminalTextualMessages(*messages)
	brief := runnerBriefWithPhaseHints(r.RuntimeBrief, results)
	state := workerruntime.RunState{
		Lane:              util.FirstNonEmpty(strings.TrimSpace(brief.Lane), r.agentName()),
		TaskFamily:        brief.TaskFamily,
		Contract:          brief.Contract,
		Phase:             "plan_submit_anchor_refresh_required",
		Objective:         brief.Objective,
		Permission:        brief.PermissionProfile,
		TargetRefs:        append([]string(nil), brief.TargetRefs...),
		RequiredEditPaths: append([]string(nil), brief.RequiredEditPaths...),
		EvidencePaths:     runnerEvidencePathsForBrief(brief, results, 6),
		Constraints:       append([]string(nil), brief.Constraints...),
		AllowedTools:      planThenExecuteAnchorRefreshToolNames(),
		ToolManifest:      runnerSanitizedToolManifest(planThenExecuteAnchorRefreshToolNames()),
		RequiredTools:     planThenExecuteAnchorRefreshToolNames(),
		ReadinessGate:     []string{"bounded_anchor_refresh_before_corrected_submit_plan"},
		SearchPolicy:      append([]string(nil), brief.SearchPolicy...),
		StopConditions:    []string{"bounded_source_refresh_returned"},
		FinalCritic:       []string{"refresh_targets_rejected_anchor_path", "no unrelated discovery"},
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
		"A previous `submit_plan` was rejected because one or more edit anchors were stale or missing. This is a source-refresh phase, not a submit-plan phase.",
		"",
		workerruntime.RenderRunState(state),
		"",
		"Refresh only: " + targetText + ".",
		planThenExecuteAnchorRefreshReadFallbackInstruction(refreshPaths),
		"For large Go/source files, raw `read_file` windows are refresh evidence but are not enough to close an invalid-anchor correction; prefer the symbol/body route for the declaration you intend to edit.",
		"Do not emit `submit_plan` until this bounded refresh returns.",
		"After the refresh, use anchors copied from the refreshed source. Prefer `insert_after_line` plus `content` for insertions/appends; use `old_text` only when copied exactly from fresh source.",
	}, "\n")

	*messages = append(pruned, llama.ChatMessage{Role: "system", Content: strings.TrimSpace(prompt)})
	r.emitEvent(marshalRunEvent("plan_then_execute_anchor_refresh_surface_refreshed", map[string]any{
		"reason":                        strings.TrimSpace(reason),
		"transport":                     "textual",
		"dropped_tool_request_messages": droppedActions,
		"dropped_scaffolding_messages":  droppedScaffolding,
		"allowed_tools":                 planThenExecuteAnchorRefreshToolNames(),
		"refresh_paths":                 refreshPaths,
	}))
}

func (r *Runner) planThenExecuteTerminalToolBrief(results []workspace.ToolResult) workerruntime.WorkerRuntimeBrief {
	brief := runnerBriefWithPhaseHints(r.RuntimeBrief, results)
	if strings.TrimSpace(brief.Lane) == "" {
		brief.Lane = r.agentName()
	}

	brief.AllowedTools = []string{workspace.ToolNameSubmitPlan}
	brief.RequiredTools = []string{workspace.ToolNameSubmitPlan}
	brief.ForbiddenTools = util.Merge(brief.ForbiddenTools,
		[]string{workspace.ToolNameReadFile, workspace.ToolNameCodeNavigate, workspace.ToolNameSearchFiles, workspace.ToolNameSearchContent, workspace.ToolNameListFiles, workspace.ToolNameShell, workspace.ToolNameSourceSanity, workspace.ToolNameEditFile, workspace.ToolNameWriteFile, workspace.ToolNameAppendFile, workspace.ToolNameApplyPatch},
	)
	brief.ToolManifest = runnerSanitizedToolManifest([]string{workspace.ToolNameSubmitPlan})
	brief.Constraints = util.Merge(brief.Constraints, []string{"plan_then_execute_submit_plan_ready", "required_edit_paths_define_write_scope", "planner_owned_verification"})
	brief.SearchPolicy = []string{"no read/search/discovery action is valid after submit_plan readiness before the first terminal outcome"}

	if planThenExecuteSubmitCorrectionActive(r.RuntimeBrief, results) {
		brief.Constraints = util.Merge(brief.Constraints, []string{"plan_then_execute_submit_correction_only"})
		brief.RequiredNext = []string{"corrected submit_plan using edits[] for changed paths and source_satisfied_paths for already-final paths"}
		brief.InvalidActions = []string{"read/search/source-refresh after submit_plan correction is ready", "Decision: source-satisfied after an incomplete submit_plan", "<<QUESTION>> after source evidence is sufficient for correction", "verify in planner phase", "per-edit write in planner phase", "prose-only final answer"}
		brief.ReadinessGate = []string{"submit_plan_correction_required"}
		brief.StopConditions = []string{"submit_plan_returned_ok"}
		brief.FinalCritic = []string{"answers_actual_user_request", "submit_plan_called", "edits_or_source_satisfied_paths_cover_required_paths"}
		if planThenExecuteSubmitCorrectionBlockerAllowed(r.RuntimeBrief, results) {
			brief.RequiredNext = []string{"corrected submit_plan using edits[] for changed paths, or <<QUESTION>> with the exact path-specific missing body/anchor blocker after repeated rejected submit_plan attempts"}
			brief.InvalidActions = []string{"read/search/source-refresh after submit_plan correction is ready", "Decision: source-satisfied after an incomplete submit_plan", "generic <<QUESTION>> without exact path-specific missing body/anchor blocker", "verify in planner phase", "per-edit write in planner phase", "prose-only final answer"}
			brief.ReadinessGate = []string{"submit_plan_correction_or_exact_body_anchor_blocker_required"}
			brief.StopConditions = []string{"submit_plan_returned_ok", "exact_missing_body_anchor_blocker_returned"}
			brief.FinalCritic = []string{"answers_actual_user_request", "submit_plan_called_or_exact_body_anchor_blocker", "edits_or_source_satisfied_paths_cover_required_paths_unless_exact_blocker"}
		}
	} else {
		brief.ReadinessGate = util.Merge([]string{string(workerruntime.ReadinessSubmitPlanOrSourceSatisfiedOrBlocker)}, brief.ReadinessGate)
		brief.StopConditions = util.Merge([]string{string(workerruntime.StopSubmitPlanReturnedOrSourceSatisfiedOrBlocker), string(workerruntime.StopBlockedByMissingPermissionOrInformation)}, brief.StopConditions)
		brief.FinalCritic = []string{"answers_actual_user_request", "submit_plan_called", "source_satisfied_or_blocker_only_with_per_path_evidence"}
	}

	brief.Readiness = runnerReadinessState(r.agentName(), brief, results)

	return workerruntime.NormalizeRuntimeBrief(brief)
}

const planThenExecuteCurrentSubmitPlanSkeletonPrefix = "# Current Submit Plan Skeleton"

func planThenExecuteTerminalSubmitPlanSkeleton(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, native bool) string {
	entries := planThenExecuteTerminalSubmitPlanSkeletonEntries(brief, results)
	if len(entries) == 0 {
		return "No concrete selected edit path is available for a safe `submit_plan` skeleton. Use `Decision: source-satisfied` with current-state evidence, answer `<<QUESTION>>:` with the exact blocker, or perform one bounded read of a concrete candidate path before submitting a plan."
	}

	if native {
		return planThenExecuteNativeSubmitPlanSkeleton(entries)
	}

	return planThenExecuteTextualSubmitPlanSkeleton(entries)
}

func planThenExecuteTerminalSubmitPlanSkeletonNoMoreTools(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, native bool) string {
	entries := planThenExecuteTerminalSubmitPlanSkeletonEntries(brief, results)
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

func planThenExecuteTerminalSubmitPlanSkeletonEntries(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) []planThenExecuteSubmitPlanSkeletonEntry {
	state := DerivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{})
	paths := planThenExecuteSubmitPlanSkeletonPaths(state)
	evidenceByPath := planThenExecuteTerminalSubmitPlanEvidenceByPath(results, 2)
	entries := make([]planThenExecuteSubmitPlanSkeletonEntry, 0, len(paths))
	evidenceLinesRemaining := planThenExecuteSubmitPlanSkeletonMaxEvidenceLines

	for _, path := range paths {
		entry := planThenExecuteSubmitPlanSkeletonEntry{
			Path:      path,
			StartLine: 1,
			EndLine:   1,
			NewFile:   planThenExecuteRequiredPathHasTerminalMissingFileEvidence(path, results),
		}
		for _, evidence := range evidenceByPath[agentutil.NormalizeRunnerPath(path)] {
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
	add := func(path string, summary string) {
		path = agentutil.NormalizeRunnerPath(path)
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

		start, end, _ := planThenExecuteReadSummaryLineRange(summary)

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

		for _, path := range agentutil.ReadPathsFromToolResult(result) {
			add(path, result.Summary)
		}

		for _, line := range strings.Split(result.Output, "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "route_summary:") {
				continue
			}

			summary := strings.TrimSpace(strings.TrimPrefix(line, "route_summary:"))
			path := agentutil.ExtractReadPathFromSummary(summary)

			if path == "" {
				path = agentutil.ExtractRoutedReadPathFromOutput(line)
			}

			add(path, summary)
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
		if normalized := agentutil.NormalizeRunnerPath(path); normalized != "" {
			required[normalized] = struct{}{}
		}
	}

	messages := make([]llama.ChatMessage, 0, len(results))
	seen := map[string]struct{}{}

	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" {
			continue
		}

		paths := agentutil.ReadPathsFromToolResult(result)

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
		if agentutil.IsNonSourceEvidenceArtifactPath(path) {
			continue
		}

		if _, ok := required[agentutil.NormalizeRunnerPath(path)]; ok {
			return true
		}
	}

	return false
}

func normalizedRunnerPaths(paths []string) []string {
	out := make([]string, 0, len(paths))
	seen := map[string]struct{}{}

	for _, path := range paths {
		normalized := agentutil.NormalizeRunnerPath(path)
		if normalized == "" {
			continue
		}

		if agentutil.IsNonSourceEvidenceArtifactPath(normalized) {
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
			droppedActions++
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

func prunePlanThenExecuteTextualToolRequestHistory(messages []llama.ChatMessage) ([]llama.ChatMessage, int) {
	if len(messages) == 0 {
		return messages, 0
	}

	out := make([]llama.ChatMessage, 0, len(messages))
	dropped := 0

	for _, message := range messages {
		if planThenExecuteDropTextualToolRequestHistoryMessage(message) {
			dropped++
			continue
		}

		out = append(out, message)
	}

	if dropped == 0 {
		return messages, 0
	}

	return out, dropped
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
		strings.HasPrefix(content, "REQUIRED SOURCE READ PENDING:") ||
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
		path := agentutil.NormalizeRunnerPath(edit.Path)
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

		if _, ok := invalidPaths[agentutil.NormalizeRunnerPath(edit.Path)]; ok {
			b.WriteString(" status=invalid_anchor_or_syntax")
		}

		if _, ok := missingPaths[agentutil.NormalizeRunnerPath(edit.Path)]; ok {
			b.WriteString(" status=missing_required_path")
		}

		if purpose := strings.TrimSpace(edit.Purpose); purpose != "" {
			b.WriteString(" purpose=")
			b.WriteString(util.Truncate(strings.TrimSpace(purpose), 180))
		}

		b.WriteString("\n")

		if planThenExecuteShouldIncludeRejectedDraftBody(edit, invalidPaths) {
			body := rejectedDraftEditBody(edit)
			if body != "" {
				b.WriteString("  rejected_body_excerpt_not_source_evidence:\n")

				for _, line := range strings.Split(util.Truncate(strings.TrimSpace(body), 2200), "\n") {
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

func planThenExecuteShouldIncludeRejectedDraftBody(edit workspace.PlanEdit, invalidPaths map[string]struct{}) bool {
	if len(invalidPaths) == 0 {
		return false
	}

	_, ok := invalidPaths[agentutil.NormalizeRunnerPath(edit.Path)]

	return ok
}

func rejectedDraftEditBody(edit workspace.PlanEdit) string {
	if strings.TrimSpace(edit.Content) != "" {
		return edit.Content
	}

	if strings.TrimSpace(edit.NewText) != "" {
		return edit.NewText
	}

	return ""
}
