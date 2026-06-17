package agent

import (
	"fmt"
	"idiom/internal/capabilities/workspace"
	"idiom/internal/runtime/workerruntime"
	"strings"
	"idiom/internal/platform/util"
)

func runnerBriefWithPhaseHints(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) workerruntime.WorkerRuntimeBrief {
	brief = workerruntime.NormalizeRuntimeBrief(brief)
	brief = runnerBriefWithRequiredEditPathHints(brief, results)

	switch phase := debugLogPhase(brief, results); phase {
	case "latest_log_selection":
		brief.Constraints = util.Merge([]string{"latest_log_selection_phase"}, brief.Constraints)
		brief.RequiredNext = util.Merge([]string{
			"select the newest real log artifact before inspecting source files",
			"prefer log directories and log extensions over source files whose names merely mention logging",
		}, brief.RequiredNext)
		brief.InvalidActions = util.Merge([]string{
			"source-code inspection before selecting a concrete log artifact",
		}, brief.InvalidActions)
		brief.SearchPolicy = util.Merge([]string{
			"prefer newest actual log artifacts over source files with log-like names",
		}, brief.SearchPolicy)
	case "latest_log_tail":
		brief.Constraints = util.Merge([]string{"latest_log_tail_phase"}, brief.Constraints)
		brief.RequiredNext = util.Merge([]string{
			"selected-log tail evidence is required before source inspection or broader log expansion",
			"the selected log remains the evidence boundary until it supports a weird decision or an explicit missing datum",
		}, brief.RequiredNext)
		brief.InvalidActions = util.Merge([]string{
			"additional workspace discovery before selected-log tail evidence",
		}, brief.InvalidActions)
		brief.SearchPolicy = util.Merge([]string{
			"selected-log tail evidence must precede deeper search or source inspection",
		}, brief.SearchPolicy)
	case "selected_log_analysis":
		brief.Constraints = util.Merge([]string{"selected_log_analysis_phase"}, brief.Constraints)
		brief.RequiredNext = util.Merge([]string{
			"answer from the selected log if the weird decision is already clear",
			"otherwise close one named missing selected-log fact before answering",
		}, brief.RequiredNext)
		brief.InvalidActions = util.Merge([]string{
			"source-code inspection outside the selected log before answering",
			"another blind selected-log reread before a concrete marker",
		}, brief.InvalidActions)
		brief.SearchPolicy = util.Merge([]string{
			"stay inside the selected log until the answer is supported or the gap is explicit",
		}, brief.SearchPolicy)
	case "failure_window":
		brief.Constraints = util.Merge([]string{"failure_window_phase"}, brief.Constraints)
		brief.RequiredNext = util.Merge([]string{
			"bounded failure-window evidence is required around the concrete failure hit",
		}, brief.RequiredNext)
		brief.InvalidActions = util.Merge([]string{
			"more log searching before failure-window evidence",
		}, brief.InvalidActions)
		brief.SearchPolicy = util.Merge([]string{
			"failure-window evidence must follow a concrete hit before broader log expansion",
		}, brief.SearchPolicy)
	case "log_analysis":
		brief.Constraints = util.Merge([]string{"log_analysis_phase"}, brief.Constraints)
		brief.RequiredNext = util.Merge([]string{
			"answer from the gathered log evidence now unless one specific missing datum remains",
		}, brief.RequiredNext)
	}

	switch phase := evidenceFirstMutationPhase(brief, results); phase {
	case "source_evidence":
		brief.Constraints = util.Merge([]string{"source_evidence_phase"}, brief.Constraints)
		brief.RequiredNext = util.Merge([]string{
			"non-target implementation evidence is required before named-target evidence",
			"after concrete non-target evidence, one bounded named-target evidence window satisfies the target-entry contract",
		}, brief.RequiredNext)
		brief.InvalidActions = util.Merge([]string{
			"open-ended named-target read before source evidence",
		}, brief.InvalidActions)
		brief.SearchPolicy = util.Merge([]string{
			"prefer semantic inspection of discovered implementation files before target reads",
		}, brief.SearchPolicy)
	case "target_entry":
		brief.Constraints = util.Merge([]string{"first_target_read_pending"}, brief.Constraints)
		brief.RequiredNext = util.Merge([]string{
			"one bounded named-target evidence window is required before renewed source discovery",
			"the named target remains the evidence boundary for the next step",
		}, brief.RequiredNext)
		brief.InvalidActions = util.Merge([]string{
			"additional source discovery before the first target read",
		}, brief.InvalidActions)
		brief.SearchPolicy = util.Merge([]string{
			"enter the named target as soon as concrete source evidence exists",
		}, brief.SearchPolicy)
	case "target_inspection":
		brief.Constraints = util.Merge([]string{"target_inspection_phase"}, brief.Constraints)
		brief.RequiredNext = util.Merge([]string{
			"make the edit in the named target once the change is supported by the target evidence",
			"additional inspection is valid only for a named missing target fact and must stay scoped to named-target evidence",
		}, brief.RequiredNext)
		brief.InvalidActions = util.Merge([]string{
			"source discovery outside the named target during target inspection",
			"open-ended target-body read during target inspection",
		}, brief.InvalidActions)
		brief.SearchPolicy = util.Merge([]string{
			"prefer target-scoped follow-up over renewed source discovery; do not keep reading once the edit is clear",
		}, brief.SearchPolicy)
	}

	return workerruntime.NormalizeRuntimeBrief(brief)
}

func runnerBriefWithRequiredEditPathHints(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) workerruntime.WorkerRuntimeBrief {
	if !runnerBriefRequiresMutation(brief) || len(brief.RequiredEditPaths) == 0 {
		return brief
	}

	if planThenExecutePlannerBrief(brief) {
		if successfulSubmitPlanResultCount(results) > 0 {
			return brief
		}

		brief = clearPlanThenExecuteRequiredSourceHints(brief)
		brief.Constraints = util.Merge([]string{"plan_then_execute_submit_plan_pending"}, brief.Constraints)

		unread := planThenExecuteUnreadRequiredPaths(brief, results)

		for path := range unread {
			if planThenExecuteRequiredPathHasTerminalMissingFileEvidence(path, results) {
				delete(unread, path)
			}
		}

		if len(unread) > 0 {
			next := firstPlanThenExecuteUnreadRequiredPath(brief.RequiredEditPaths, unread)
			readInstruction := "read the next unread required_edit_path with source-body evidence before submit_plan; after every existing required_edit_path has satisfied its source-body obligation, submit_plan using edits[] for changed paths and source_satisfied_paths for already-final paths, Decision: source-satisfied with per-path evidence, or <<QUESTION>> with the exact path-specific blocker"

			if next != "" {
				readInstruction = fmt.Sprintf("read required_edit_path %s with source-body evidence before submit_plan; after every existing required_edit_path has satisfied its source-body obligation, submit_plan using edits[] for changed paths and source_satisfied_paths for already-final paths, Decision: source-satisfied with per-path evidence, or <<QUESTION>> with the exact path-specific blocker", next)
			}

			brief.RequiredNext = util.Merge([]string{readInstruction}, brief.RequiredNext)
			brief.InvalidActions = util.Merge([]string{
				"submit_plan before every existing required_edit_path has satisfied its source-body obligation",
				"final answer before submit_plan succeeds unless it is a source-satisfied closeout with current-state evidence for every required path",
				"prose final answer while required source evidence is pending",
				"per-edit write tool in plan-execute planner phase",
				"shell/git/source-sanity verification in plan-execute planner phase",
				"new orientation read/search before the next unread required_edit_path source-body read",
			}, brief.InvalidActions)
			brief.SearchPolicy = util.Merge([]string{
				"required_edit_paths plus packet evidence define the submit_plan scope",
				"progress through unread required_edit_paths one source-body read at a time before submit_plan",
				"outline/search hits orient but do not satisfy required source-body evidence",
				"if current source already matches requested final state, close as source-satisfied instead of submitting stale edits",
			}, brief.SearchPolicy)

			return brief
		}

		state := DerivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true})
		if state.Mode == PlanExecutePlanningModeAnchorRefresh && len(state.RefreshPaths) > 0 {
			refreshPaths := state.RefreshPaths
			next := "the invalid-anchor required path"

			if len(refreshPaths) > 0 {
				next = refreshPaths[0]
			}

			brief.RequiredNext = util.Merge([]string{
				fmt.Sprintf("refresh bounded source-body evidence for %s before corrected submit_plan; then submit a corrected plan using anchors copied from the refreshed source", next),
			}, rejectRunnerStateStrings(brief.RequiredNext, planThenExecuteSubmitReadySourceHint))
			brief.Constraints = util.Merge([]string{"plan_then_execute_submit_anchor_refresh_required"}, brief.Constraints)
			brief.InvalidActions = util.Merge([]string{
				"corrected submit_plan before refreshing the stale or missing edit anchor",
				"final answer, Decision: source-satisfied, or <<QUESTION>> before bounded anchor refresh returns",
				"per-edit write tool in plan-execute planner phase",
				"shell/git/source-sanity verification in plan-execute planner phase",
				"open-ended read/search/discovery outside the invalid-anchor source refresh",
			}, rejectRunnerStateStrings(brief.InvalidActions, planThenExecuteSubmitReadySourceHint))
			brief.SearchPolicy = util.Merge([]string{
				"stale submit_plan anchors require a bounded source-body refresh before corrected submit_plan",
				"refresh only the rejected required path named by the submit_plan guard",
				"after the refresh, use insert_after_line/content or old_text/new_text copied from fresh source; do not reuse stale anchors",
			}, rejectRunnerStateStrings(brief.SearchPolicy, planThenExecuteSubmitReadySourceHint))

			return brief
		}

		brief.RequiredNext = util.Merge([]string{
			"submit_plan using edits[] for changed paths and top-level source_satisfied_paths for already-final paths, Decision: source-satisfied with per-path evidence, or <<QUESTION>> with the exact path-specific blocker",
		}, rejectRunnerStateStrings(brief.RequiredNext, planThenExecuteSubmitReadySourceHint))
		brief.Constraints = util.Merge([]string{"plan_then_execute_submit_plan_ready"}, brief.Constraints)
		brief.InvalidActions = util.Merge([]string{
			"final answer before submit_plan succeeds unless it is a source-satisfied closeout with current-state evidence for every required path",
			"per-edit write tool in plan-execute planner phase",
			"shell/git/source-sanity verification in plan-execute planner phase",
			"new orientation read/search after enough anchor evidence to draft submit_plan",
		}, brief.InvalidActions)
		brief.SearchPolicy = util.Merge([]string{
			"required_edit_paths plus packet evidence define the submit_plan scope",
			"if current source already matches requested final state, close as source-satisfied instead of submitting stale edits",
			"no read/search/discovery action is valid after submit_plan readiness before the first terminal outcome",
		}, rejectRunnerStateStrings(brief.SearchPolicy, planThenExecuteSubmitReadySourceHint))

		return brief
	}

	changed := changedPathsFromToolResults(results)
	missing := missingRequiredEditPaths(brief.RequiredEditPaths, changed)

	if len(changed) == 0 {
		if briefHasUsableEditAnchors(brief) {
			brief.Constraints = util.Merge([]string{"implementation_packet_write_ready"}, brief.Constraints)
		}

		if exactAnchorPreWriteSurfaceActive(brief, results) {
			brief.RequiredNext = util.Merge([]string{
				"required-path write capability from the exact target-packet anchor",
				"answer <<QUESTION>> only if the supplied exact anchor is stale or missing",
			}, brief.RequiredNext)
			brief.InvalidActions = util.Merge([]string{
				"read/search/task_ledger/shell before first exact-anchor write",
			}, brief.InvalidActions)
			brief.SearchPolicy = util.Merge([]string{
				"required_edit_paths plus exact target-packet anchors establish the first write surface",
			}, brief.SearchPolicy)
		} else {
			brief.RequiredNext = util.Merge([]string{
				"resolve a required path with packet evidence, semantic/context/declared project discovery or a narrow required-path source read with named_gap for an exact edit-safety fact, or answer <<QUESTION>> with the exact path-specific blocker",
			}, brief.RequiredNext)
			brief.InvalidActions = util.Merge([]string{
				"orientation read/search after required edit paths and packet evidence without a named missing fact",
				"raw read/search outside required edit paths before resolving the required-path contract",
				"task_ledger planning before the first required-path write",
			}, brief.InvalidActions)
			brief.SearchPolicy = util.Merge([]string{
				"required_edit_paths plus packet evidence establish the edit scope; prefer semantic/context/declared project discovery for named edit-safety gaps before raw source reads",
			}, brief.SearchPolicy)
		}

		return brief
	}

	if len(missing) > 0 {
		brief.Constraints = util.Merge([]string{"implementation_required_paths_remaining"}, brief.Constraints)
		brief.RequiredNext = util.Merge([]string{
			"resolve each remaining required path with an edit, semantic/context/declared project discovery or a narrow required-path source read with named_gap for an exact edit-safety fact, or <<QUESTION>> with the exact blocker for that path",
		}, brief.RequiredNext)
		brief.InvalidActions = util.Merge([]string{
			"final answer while required edit paths remain unchanged",
			"new orientation read/search outside remaining required edit paths before resolving the required-path contract",
		}, brief.InvalidActions)
	}

	return brief
}

func clearPlanThenExecuteRequiredSourceHints(brief workerruntime.WorkerRuntimeBrief) workerruntime.WorkerRuntimeBrief {
	brief.RequiredNext = rejectRunnerStateStrings(brief.RequiredNext, planThenExecuteRequiredSourceHint)
	brief.InvalidActions = rejectRunnerStateStrings(brief.InvalidActions, planThenExecuteRequiredSourceHint)
	brief.SearchPolicy = rejectRunnerStateStrings(brief.SearchPolicy, planThenExecuteRequiredSourceHint)

	return brief
}

func planThenExecuteRequiredSourceHint(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}

	for _, marker := range []string{
		"read required_edit_path ",
		"read the next unread required_edit_path",
		"submit_plan before every existing required_edit_path has satisfied its source-body obligation",
		"prose final answer while required source evidence is pending",
		"new orientation read/search before the next unread required_edit_path source-body read",
		"progress through unread required_edit_paths one source-body read at a time before submit_plan",
		"outline/search hits orient but do not satisfy required source-body evidence",
	} {
		if strings.Contains(value, marker) {
			return true
		}
	}

	return false
}

func planThenExecuteSubmitReadySourceHint(value string) bool {
	if planThenExecuteRequiredSourceHint(value) {
		return true
	}

	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}

	for _, marker := range []string{
		"for existing required paths, satisfy source-body obligations before submit_plan",
		"packet anchors establish scope but existing paths still need source-body obligations satisfied before submit_plan",
		"source-body obligations before submit_plan",
		"use reads only for exact anchor confirmation before submit_plan",
		"raw source reads are only for exact anchor confirmation",
	} {
		if strings.Contains(value, marker) {
			return true
		}
	}

	return false
}

func rejectRunnerStateStrings(values []string, reject func(string) bool) []string {
	if len(values) == 0 {
		return nil
	}

	out := make([]string, 0, len(values))
	for _, value := range values {
		if reject != nil && reject(value) {
			continue
		}

		out = append(out, value)
	}

	return out
}

func runnerVisibleBriefForToolSurface(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) workerruntime.WorkerRuntimeBrief {
	brief = workerruntime.NormalizeRuntimeBrief(brief)
	if exactAnchorPreWriteSurfaceActive(brief, results) {
		return exactAnchorPreWriteRuntimeBrief(brief)
	}

	if implementationPreWriteToolSurfaceActive(brief, results) {
		return implementationPreWriteRuntimeBrief(brief)
	}

	if implementationTargetSourcePending(brief, results) {
		return implementationTargetSourceRuntimeBrief(brief)
	}

	if implementationTargetWriteReadySurfaceActive(brief, results) {
		return implementationTargetWriteReadyRuntimeBrief(brief, results)
	}

	if implementationScopedEditSurfaceActive(brief, results) {
		return implementationScopedEditRuntimeBrief(brief)
	}

	if genericReadOnlyEvidencePending(brief, brief.AllowedTools, results) {
		return readOnlyEvidencePendingRuntimeBrief(brief)
	}

	return brief
}

func readOnlyEvidencePendingRuntimeBrief(brief workerruntime.WorkerRuntimeBrief) workerruntime.WorkerRuntimeBrief {
	allowed := genericReadOnlyEvidenceTools(brief.AllowedTools)

	brief.AllowedTools = allowed
	brief.ToolManifest = nil

	if len(allowed) > 0 {
		brief.ToolManifest = runnerSanitizedToolManifest(allowed)
	}

	brief.RequiredTools = util.Merge([]string{"bounded_workspace_evidence_tool"}, brief.RequiredTools)
	brief.ReadinessGate = util.Merge([]string{"bounded_workspace_evidence_before_final_answer"}, brief.ReadinessGate)
	brief.RequiredNext = util.Merge([]string{
		"exactly one parser-readable bounded read/search/context tool call for the user's question, or <<QUESTION>> with the exact evidence blocker",
	}, brief.RequiredNext)
	brief.InvalidActions = util.Merge([]string{
		"prose/final answer before a successful bounded evidence tool result",
		"workspace write",
		"shell/mutating command",
	}, brief.InvalidActions)
	brief.SearchPolicy = util.Merge([]string{
		"use the narrowest bounded evidence tool that can answer the user's question; answer only after evidence returns",
	}, brief.SearchPolicy)
	brief.Constraints = util.Merge([]string{"read_only_evidence_pending"}, brief.Constraints)

	return workerruntime.NormalizeRuntimeBrief(brief)
}
