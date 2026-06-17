package agent

import (
	"idiom/internal/capabilities/workspace"
	"idiom/internal/runtime/workerruntime"
	"idiom/internal/platform/util"
)

func exactAnchorPreWriteSurfaceActive(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if planThenExecutePlannerBrief(brief) {
		return false
	}

	if !exactAnchorImplementationActive(brief) {
		return false
	}

	return len(brief.ChangedPaths) == 0 && len(changedPathsFromToolResults(results)) == 0
}

func implementationPreWriteRuntimeBrief(brief workerruntime.WorkerRuntimeBrief) workerruntime.WorkerRuntimeBrief {
	allowed := implementationPreWriteAllowedTools(brief.AllowedTools)

	brief.AllowedTools = allowed
	brief.ToolManifest = nil

	if len(allowed) > 0 {
		brief.ToolManifest = runnerSanitizedToolManifest(allowed)
	}

	brief.RequiredTools = []string{"edit_file_or_apply_patch"}
	brief.Constraints = util.Merge([]string{"implementation_prewrite_write_only"}, brief.Constraints)
	brief.RequiredNext = util.Merge([]string{
		"exact-anchor required-path write capability",
		"answer <<QUESTION>> only if the packet lacks the exact anchor needed for that required path",
	}, brief.RequiredNext)
	brief.InvalidActions = util.Merge([]string{
		"read/search/task_ledger/shell before first exact-anchor required-path write",
	}, brief.InvalidActions)
	brief.SearchPolicy = util.Merge([]string{
		"the exact target packet and required_edit_paths are the pre-write evidence surface",
		"do not use discovery or task planning before the first exact-anchor required-path write",
	}, brief.SearchPolicy)

	return workerruntime.NormalizeRuntimeBrief(brief)
}

func implementationScopedEditRuntimeBrief(brief workerruntime.WorkerRuntimeBrief) workerruntime.WorkerRuntimeBrief {
	allowed := implementationScopedEditAllowedToolsWithRegistry(brief.AllowedTools, nil)

	brief.AllowedTools = allowed
	brief.ToolManifest = nil

	if len(allowed) > 0 {
		brief.ToolManifest = runnerSanitizedToolManifest(allowed)
	}

	brief.RequiredTools = []string{"edit_file_or_apply_patch", "semantic_discovery_or_narrow_read_for_named_gap"}
	brief.Constraints = util.Merge([]string{"implementation_required_path_scoped_surface"}, brief.Constraints)
	brief.RequiredNext = util.Merge([]string{
		"required-path edit, semantic/context/project discovery or narrow required-path read with named_gap for an exact edit-safety fact, or <<QUESTION>> with the exact blocker",
	}, brief.RequiredNext)
	brief.InvalidActions = util.Merge([]string{
		"task_ledger or broad discovery before resolving the required-path contract",
		"raw read/search outside required edit paths before resolving the required-path contract",
	}, brief.InvalidActions)
	brief.SearchPolicy = util.Merge([]string{
		"required edit paths define the implementation scope",
		"prefer context, language, and declared read-only project discovery tools for named edit-safety gaps before raw source reads",
		"raw source reads are only for exact anchor confirmation on required paths or explicit evidence paths",
		"large range-less raw source reads on required/evidence paths are bounded by the harness; request start_line/end_line or a symbol for follow-up",
	}, brief.SearchPolicy)

	return workerruntime.NormalizeRuntimeBrief(brief)
}

func implementationTargetWriteReadyRuntimeBrief(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) workerruntime.WorkerRuntimeBrief {
	pending := sourceEvidencePendingRedirectsForBrief(brief, results)
	pendingTools := sourceEvidencePendingRedirectTools(brief.AllowedTools, pending)
	sourceRefresh := len(pending) == 0 && implementationTargetWriteReadySourceRefreshProjectionActive(brief, results, brief.AllowedTools)
	sourcedSummary := implementationTargetWriteReadySourcedPathSummary(results, 3)
	allowed := implementationTargetWriteReadyAllowedToolsForBrief(brief.AllowedTools, brief, results)

	brief.AllowedTools = allowed
	brief.ToolManifest = nil

	if len(allowed) > 0 {
		brief.ToolManifest = runnerSanitizedToolManifest(allowed)
	}

	brief.RequiredTools = []string{"edit_file_or_apply_patch"}
	brief.Constraints = util.Merge([]string{"implementation_target_write_ready"}, brief.Constraints)
	brief.RequiredNext = util.Merge([]string{
		"parser-readable write to the sourced target path, or <<QUESTION>> with the exact edit blocker/no-op evidence",
	}, brief.RequiredNext)

	if sourceRefresh {
		brief.RequiredNext = util.Merge([]string{
			"parser-readable write to the sourced target path, bounded read_file/search_content source refresh with named_gap for a precise edit-safety fact, or <<QUESTION>> with the exact edit blocker/no-op evidence",
		}, brief.RequiredNext)
		brief.InvalidActions = util.Merge([]string{
			"broad read/search after source-body evidence",
			"read/search for already-sourced paths unless the request adds a bounded missing range or exact named_gap",
			"verification before a successful write",
		}, brief.InvalidActions)
		brief.SearchPolicy = util.Merge([]string{
			"the gathered source-body evidence is the pre-write target surface",
			"use bounded source refresh only when an exact missing edit-safety fact remains; otherwise write or block",
			"read_file/search_content refreshes must name the gap and stay scoped to source code, not docs or broad discovery",
		}, brief.SearchPolicy)
		brief.Constraints = util.Merge([]string{"bounded_source_refresh_allowance"}, brief.Constraints)
	} else {
		brief.InvalidActions = util.Merge([]string{
			"additional read/search after source-body evidence",
			"verification before a successful write",
		}, brief.InvalidActions)
		brief.SearchPolicy = util.Merge([]string{
			"the gathered source-body evidence is the pre-write target surface",
			"do not inspect further before the first write",
		}, brief.SearchPolicy)
	}

	if sourcedSummary != "" {
		brief.RequiredNext = util.Merge([]string{
			"write using the already-read source-body evidence for: " + sourcedSummary,
		}, brief.RequiredNext)
		brief.InvalidActions = util.Merge([]string{
			"read/search/navigation for already-sourced path(s): " + sourcedSummary,
		}, brief.InvalidActions)
		brief.SearchPolicy = util.Merge([]string{
			"already-sourced path(s) must not be read, searched, or navigated again before the first write: " + sourcedSummary,
		}, brief.SearchPolicy)
		brief.Constraints = util.Merge([]string{"source_body_evidence_paths: " + sourcedSummary}, brief.Constraints)
	}

	if len(pendingTools) > 0 {
		pendingSummary := sourceEvidencePendingRedirectSummary(pending, 3)
		pendingActionSummary := sourceEvidencePendingRedirectActionSummary(pending, 3)

		brief.RequiredTools = []string{"exact_pending_source_body_followup_or_edit_file_or_apply_patch"}
		brief.RequiredNext = util.Merge([]string{
			"resolve the pending redirected source-body obligation (" + pendingActionSummary + "), then make one parser-readable write to a sourced target path, or <<QUESTION>> with the exact edit blocker/no-op evidence",
		}, brief.RequiredNext)
		brief.InvalidActions = util.Merge([]string{
			"broad read/search while a redirected source-body obligation is pending",
			"read/navigation for any path except the exact pending redirected source-body follow-up: " + pendingActionSummary,
		}, brief.InvalidActions)
		brief.SearchPolicy = util.Merge([]string{
			"one redirected source-body obligation remains open: " + pendingActionSummary,
			"use only the pending redirect's symbol/body follow-up, with at most one same-path outline first when the symbol is unknown; do not use references, broad discovery, or unrelated navigation",
		}, brief.SearchPolicy)
		brief.Constraints = util.Merge([]string{"pending_redirected_source_body_obligation: " + pendingSummary}, brief.Constraints)
	}

	return workerruntime.NormalizeRuntimeBrief(brief)
}

func implementationTargetSourceRuntimeBrief(brief workerruntime.WorkerRuntimeBrief) workerruntime.WorkerRuntimeBrief {
	allowed := implementationTargetSourceAllowedTools(brief.AllowedTools)

	brief.AllowedTools = allowed
	brief.ToolManifest = nil

	if len(allowed) > 0 {
		brief.ToolManifest = runnerSanitizedToolManifest(allowed)
	}

	brief.RequiredTools = []string{"bounded_workspace_evidence_tool", "edit_file_or_apply_patch"}
	brief.Constraints = util.Merge([]string{"implementation_target_source_pending"}, brief.Constraints)
	brief.RequiredNext = util.Merge([]string{
		"localize the concrete edit target, read bounded source-body evidence with named_gap for the missing edit-safety fact, write only after exact source-body evidence and replacement shape are known, or <<QUESTION>> with the exact blocker/no-op evidence",
	}, brief.RequiredNext)
	brief.InvalidActions = util.Merge([]string{
		"final answer before a successful write, no-op, or blocker",
		"write before exact source-body evidence and replacement shape",
		"verification before a successful write",
	}, brief.InvalidActions)
	brief.SearchPolicy = util.Merge([]string{
		"do not run verification before the first write",
		"use bounded read/search/context only for a named missing target or edit-safety fact",
	}, brief.SearchPolicy)
	brief.ForbiddenTools = util.Merge(brief.ForbiddenTools, []string{workspace.ToolNameSourceSanity, workspace.ToolNameShell, workspace.ToolNameGitDiff, workspace.ToolNameGitStatus})

	return workerruntime.NormalizeRuntimeBrief(brief)
}

func exactAnchorPreWriteRuntimeBrief(brief workerruntime.WorkerRuntimeBrief) workerruntime.WorkerRuntimeBrief {
	allowed := implementationPreWriteAllowedTools(brief.AllowedTools)

	brief.AllowedTools = allowed
	brief.ToolManifest = nil

	if len(allowed) > 0 {
		brief.ToolManifest = runnerSanitizedToolManifest(allowed)
	}

	brief.RequiredTools = []string{"edit_file_or_apply_patch"}
	brief.Constraints = util.Merge([]string{"exact_anchor_prewrite_write_first"}, brief.Constraints)
	brief.RequiredNext = util.Merge([]string{
		"exact-anchor write capability with the supplied target-packet anchor",
		"answer <<QUESTION>> only if the supplied anchor is missing or stale",
	}, brief.RequiredNext)
	brief.InvalidActions = util.Merge([]string{
		"read/search/shell before first exact-anchor write",
		"verification before first exact-anchor write",
	}, brief.InvalidActions)
	brief.SearchPolicy = util.Merge([]string{
		"target-packet exact anchors count as current-state path and source evidence for the first write",
		"do not inspect before the first exact-anchor write",
	}, brief.SearchPolicy)

	return workerruntime.NormalizeRuntimeBrief(brief)
}
