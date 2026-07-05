package workerruntime

import (
	"strings"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/platform/textutil"
)

func BuildActionGuidance(input ActionGuidanceInput) ActionGuidance {
	statusCombined := combinedLower(input.Reason, input.Result.Summary, input.Result.Error)
	tool := strings.TrimSpace(input.Request.Tool)
	closureScope := closureGuidanceScope(input.Constraints, statusCombined)
	closureActive := closureScope != ""
	writeReadyPacket := implementationPacketWriteReady(input.Constraints)
	planExecuteSubmitReady := ConstraintPresent(input.Constraints, ConstraintPlanThenExecuteSubmitPlanReady)
	exactAnchorPacket := ConstraintPresent(input.Constraints, ConstraintExactAnchorImplementation) ||
		ConstraintPresent(input.Constraints, ConstraintExactAnchorPrewriteWriteFirst)
	requiredPathsRemaining := implementationRequiredPathsRemaining(input.Constraints)
	guidance := ActionGuidance{}

	if input.DeclarationMovesForbidden {
		guidance.Constraints = appendUnique(guidance.Constraints, "declaration_moves_forbidden")
		guidance.InvalidNext = appendUnique(guidance.InvalidNext, "go_move_declarations")
	}

	if input.WriteCapable && (input.ApprovedTargetScopePresent || textutil.ContainsAny(statusCombined, "implementation target packet", "approved editable target")) {
		guidance.Constraints = appendUnique(guidance.Constraints, "approved_target_scope_enforced")
	}

	if strings.Contains(statusCombined, "write approval required") {
		guidance.Constraints = appendUnique(guidance.Constraints, "write_approval_required")
	}

	if strings.Contains(statusCombined, "completion gate") {
		guidance.Constraints = appendUnique(guidance.Constraints, "completion_gate_active")
	}

	if strings.Contains(statusCombined, "bootstrap already inspected") {
		guidance.Constraints = appendUnique(guidance.Constraints, "bootstrap_evidence_available")
	}

	if closureActive {
		guidance.Constraints = appendUnique(guidance.Constraints, closureScope)
	}

	if planExecuteSubmitReady {
		guidance.InvalidNext = appendUnique(guidance.InvalidNext, "read/search/source-refresh after plan-execute submit_plan readiness")
		guidance.ValidNext = appendUnique(guidance.ValidNext, "submit_plan covering every required_edit_paths entry with a real edit or source_satisfied_paths entry")
		guidance.ValidNext = appendUnique(guidance.ValidNext, "narrow exception: Decision: source-satisfied only with current-state evidence for every required path")
		guidance.ValidNext = appendUnique(guidance.ValidNext, "narrow exception: <<QUESTION>> only with exact path-specific blockers")

		if textutil.ContainsAny(statusCombined, "malformed tool request", "tool_parse") {
			guidance.InvalidNext = appendUnique(guidance.InvalidNext, "malformed_json_tool_request")
			guidance.ValidNext = appendUnique(guidance.ValidNext, toolParseSubmitPlanRequiredNext(input.ToolTransportMode))
		}

		return guidance
	}

	if textutil.ContainsAny(statusCombined, "malformed tool request", "tool_parse") {
		guidance.InvalidNext = appendUnique(guidance.InvalidNext, "malformed_json_tool_request")
		guidance.ValidNext = appendUnique(guidance.ValidNext, toolParseRequiredNext(input.ToolTransportMode))
		if !toolParseNativeTransport(input.ToolTransportMode) {
			guidance.ValidNext = appendUnique(guidance.ValidNext, "answer normally only if the task is complete")
		}

		return guidance
	}

	if exactAnchorWriteFailure(input.Constraints, tool, statusCombined) {
		guidance.InvalidNext = appendUnique(guidance.InvalidNext, "read/search before resolving exact-anchor write failure")
		guidance.ValidNext = appendUnique(guidance.ValidNext, "retry one corrected write shape from the target packet if a safe anchor is known")
		guidance.ValidNext = appendUnique(guidance.ValidNext, "answer with <<QUESTION>> naming the missing anchor if the packet anchor no longer matches")
	}

	if exactAnchorHiddenPrewriteTool(input.Constraints, tool, statusCombined) {
		guidance.InvalidNext = appendUnique(guidance.InvalidNext, "read/search/shell before first exact-anchor write")
		guidance.ValidNext = appendUnique(guidance.ValidNext, "exact-anchor write capability with the supplied target-packet anchor")
		guidance.ValidNext = appendUnique(guidance.ValidNext, "answer with <<QUESTION>> if no exact write anchor or patch is present")
	}

	if textutil.ContainsAny(statusCombined, "repeated", "replayed", "already failed earlier") {
		if tool != "" {
			guidance.InvalidNext = appendUnique(guidance.InvalidNext, "repeat_exact_"+tool+"_request")
		} else {
			guidance.InvalidNext = appendUnique(guidance.InvalidNext, "repeat_exact_tool_request")
		}
	}

	if strings.Contains(statusCombined, "broad inspection") {
		guidance.InvalidNext = appendUnique(guidance.InvalidNext, "additional_broad_workspace_inspection")
	}

	if strings.Contains(statusCombined, "bootstrap already inspected") && tool == workspace.ToolNameReadFile {
		guidance.InvalidNext = appendUnique(guidance.InvalidNext, "duplicate_read_file_on_bootstrapped_target")
	}

	if textutil.ContainsAny(statusCombined, "completion gate", "final answer rejected") {
		guidance.InvalidNext = appendUnique(guidance.InvalidNext, "final_answer_without_satisfying_completion_gate")
	}

	if closureScope != "" {
		guidance.InvalidNext = appendUnique(guidance.InvalidNext, "open-ended discovery after readiness without one exact missing claim and one exact probe")
	}

	if writeReadyPacket {
		guidance.InvalidNext = appendUnique(guidance.InvalidNext, "orientation read/search after required edit paths and packet evidence without a named missing fact")
		guidance.InvalidNext = appendUnique(guidance.InvalidNext, "task_ledger planning before the first required-path write")
	}

	if requiredPathsRemaining {
		guidance.InvalidNext = appendUnique(guidance.InvalidNext, "final answer while required edit paths remain unchanged")
		guidance.InvalidNext = appendUnique(guidance.InvalidNext, "new orientation read/search before resolving remaining required edit paths")
	}

	if strings.Contains(statusCombined, "bootstrap already inspected") {
		guidance.ValidNext = appendUnique(guidance.ValidNext, "approved-target write capability using bootstrapped evidence")
		guidance.ValidNext = appendUnique(guidance.ValidNext, "narrow range read only if exact lines are missing")
		guidance.ValidNext = appendUnique(guidance.ValidNext, "exact-text search scoped to the approved target")
		guidance.ValidNext = appendUnique(guidance.ValidNext, "answer with a concrete blocker or no-op reason")
	}

	if !closureActive && textutil.ContainsAny(statusCombined, "repeated dead-end", "already failed earlier", "replayed same blocker", "replayed prior evidence") {
		guidance.ValidNext = appendUnique(guidance.ValidNext, "choose a materially different tool or target")
		guidance.ValidNext = appendUnique(guidance.ValidNext, "answer with the explicit blocker and evidence")
	}

	if !closureActive && strings.Contains(statusCombined, "broad inspection") {
		guidance.ValidNext = appendUnique(guidance.ValidNext, "one structured symbol, call/reference, or file-outline capability on a named target")
		guidance.ValidNext = appendUnique(guidance.ValidNext, "one bounded discovery-packet capability for a broad architecture, tracing, or impact question")
		guidance.ValidNext = appendUnique(guidance.ValidNext, "one exact-text search only when a semantic capability or discovered handle does not land")
		guidance.ValidNext = appendUnique(guidance.ValidNext, "answer if the current evidence already resolves the question")
	}

	if textutil.ContainsAny(statusCombined, "post-edit", "workspace edit already landed") {
		guidance.ValidNext = appendUnique(guidance.ValidNext, "provide targeted verification evidence")
		guidance.ValidNext = appendUnique(guidance.ValidNext, "read the changed file if exact failure lines are needed")
		guidance.ValidNext = appendUnique(guidance.ValidNext, "record required ledger state or answer with the concrete blocker")
	}

	if textutil.ContainsAny(statusCombined, "approved editable target", "implementation target packet") {
		guidance.ValidNext = appendUnique(guidance.ValidNext, "approved-target write capability inside approved paths")
		guidance.ValidNext = appendUnique(guidance.ValidNext, "request a target-scope update if the approved target is wrong")
		guidance.ValidNext = appendUnique(guidance.ValidNext, "report the guard as a concrete blocker")
	}

	if writeReadyPacket {
		guidance.ValidNext = appendUnique(guidance.ValidNext, "required-path write capability using the packet and gathered evidence")
		if !exactAnchorPacket && workspace.IsWorkspaceWriteTool(tool) && strings.TrimSpace(input.Result.Error) != "" && textutil.ContainsAny(statusCombined, "old_text did not appear", "hunk", "context matched", "ambiguous", "stale") {
			guidance.ValidNext = appendUnique(guidance.ValidNext, "semantic/context/declared project discovery capability or narrow required-path read with named_gap to refresh the stale or ambiguous anchor")
		}

		guidance.ValidNext = appendUnique(guidance.ValidNext, "answer with <<QUESTION>> naming the exact edit-safety fact if no safe edit can be attempted")
	}

	if verificationToolFailure(tool, statusCombined, input.Result) {
		guidance.InvalidNext = appendUnique(guidance.InvalidNext, "final answer treating failed verification as completion")
		guidance.InvalidNext = appendUnique(guidance.InvalidNext, "same verification retry before a repair write unless the failure is environmental")
		guidance.ValidNext = appendUnique(guidance.ValidNext, "repair the failing changed or required path when the diagnostic identifies a code or test issue")
		guidance.ValidNext = appendUnique(guidance.ValidNext, "one narrow source read or language/context lookup for the exact line, symbol, or type named by the failure if needed before repair")
		guidance.ValidNext = appendUnique(guidance.ValidNext, "rerun verification after the repair write")
		guidance.ValidNext = appendUnique(guidance.ValidNext, "answer with <<QUESTION>> only for an external, permission, or environment blocker")
	}

	if requiredPathsRemaining {
		guidance.ValidNext = appendUnique(guidance.ValidNext, "edit each remaining required path")
		if !exactAnchorPacket {
			guidance.ValidNext = appendUnique(guidance.ValidNext, "semantic/context/declared project discovery capability or narrow remaining-path read with named_gap if an exact edit-safety fact is missing")
		}

		guidance.ValidNext = appendUnique(guidance.ValidNext, "answer with <<QUESTION>> naming the exact blocker for each remaining required path")
	}

	if strings.Contains(statusCombined, "write approval required") {
		guidance.ValidNext = appendUnique(guidance.ValidNext, "stop and report that write approval is required")
	}

	if textutil.ContainsAny(statusCombined, "completion gate", "final answer rejected") {
		if writeReadyPacket || requiredPathsRemaining {
			guidance.ValidNext = appendUnique(guidance.ValidNext, "satisfy the missing write contract with an allowed edit, or answer with <<QUESTION>> naming the exact blocker")
		} else {
			guidance.ValidNext = appendUnique(guidance.ValidNext, "produce the missing evidence or edit required by the gate")
			guidance.ValidNext = appendUnique(guidance.ValidNext, "record ledger state if the gate requires it")
		}

		guidance.ValidNext = appendUnique(guidance.ValidNext, "answer only after the gate condition is satisfied")
	}

	if closureActive {
		for _, next := range closureGuidanceNext(scopeSpecificClosureLabel(closureScope)) {
			guidance.ValidNext = appendUnique(guidance.ValidNext, next)
		}
	}

	if input.DeclarationMovesForbidden {
		guidance.ValidNext = appendUnique(guidance.ValidNext, "do not call go_file_outline or go_move_declarations")
		guidance.ValidNext = appendUnique(guidance.ValidNext, "narrow range read inside the approved target")
		guidance.ValidNext = appendUnique(guidance.ValidNext, "same-file edit capability instead of declaration moves")
		guidance.ValidNext = appendUnique(guidance.ValidNext, "verification evidence or concrete blocker")
	}

	if !closureActive {
		if languageOutlineGuidanceTool(tool) && strings.TrimSpace(input.Result.Error) == "" {
			if input.EvidenceIntent == "refactor" {
				// Refactoring objective — outline's built-in
				// go_move_declarations hint is the right next step.
				guidance.ValidNext = appendUnique(guidance.ValidNext, "declaration-move write capability for a tagged group of declarations")
				guidance.ValidNext = appendUnique(guidance.ValidNext, "read-symbol capability if one declaration still needs inspection before moving")
			} else {
				// Default (explanatory/relational/unknown): reading a
				// specific declaration is the high-value next step.
				// The outline summary itself may suggest
				// go_move_declarations — that advice only lands for
				// refactor objectives, so we override here.
				guidance.ValidNext = appendUnique(guidance.ValidNext, "read-symbol capability with one outlined declaration name to inspect its body")
				guidance.ValidNext = appendUnique(guidance.ValidNext, "references or calls-into capability on a named declaration to trace how it is used")
				guidance.ValidNext = appendUnique(guidance.ValidNext, "ignore the outline's go_move_declarations suggestion unless the objective is actually a refactor")
			}
		}

		switch tool {
		case workspace.ToolNameReadFile, workspace.ToolNameCodeNavigate:
			if strings.TrimSpace(input.Result.Error) == "" {
				if writeReadyPacket {
					guidance.ValidNext = appendUnique(guidance.ValidNext, "required-path edit from the packet and inspected evidence")
					guidance.ValidNext = appendUnique(guidance.ValidNext, "answer with <<QUESTION>> only if the edit still has a path-specific blocker")
				} else {
					guidance.ValidNext = appendUnique(guidance.ValidNext, "answer if the inspected body already resolves the question")
					guidance.ValidNext = appendUnique(guidance.ValidNext, "if one exact missing claim remains, request one exact symbol, reference, or narrow range capability that would resolve it")
				}

				if input.WriteCapable && (input.ApprovedTargetScopePresent || input.EvidenceIntent == "refactor" || writeReadyPacket) {
					guidance.ValidNext = appendUnique(guidance.ValidNext, "write capability using the inspected evidence")
				}

				if writeReadyPacket {
					guidance.ValidNext = appendUnique(guidance.ValidNext, "do not take another orientation read just to improve confidence")
				} else {
					guidance.ValidNext = appendUnique(guidance.ValidNext, "one narrow range read or exact-text search only if a specific anchor is still missing")
				}
			}
		case workspace.ToolNameSearchFiles, workspace.ToolNameSearchContent, workspace.ToolNameRg, "go_find_symbol", "go_find_type", "go_find_const", "go_references", "go_definition":
			if strings.TrimSpace(input.Result.Error) == "" {
				if IsZeroHitSearchResult(input.Result) {
					// The default "read the highest-confidence hit" nudge is
					// meaningless when there are no hits. Point the model at
					// a different search shape instead — its common failure
					// after a zero-hit search_files is to invent more
					// filenames to search for.
					guidance.ValidNext = appendUnique(guidance.ValidNext, "structured symbol/navigation capability for the affected language before guessing filenames or repeating the same search")
					guidance.ValidNext = appendUnique(guidance.ValidNext, "narrow to one exact term or scope from the user's question")
					guidance.ValidNext = appendUnique(guidance.ValidNext, "answer with the evidence you already have if the target may not exist")
				} else {
					guidance.ValidNext = appendUnique(guidance.ValidNext, "read one exact symbol body, discovered handle, or narrow range from the highest-confidence hit")
					guidance.ValidNext = appendUnique(guidance.ValidNext, "answer if the best hit already resolves the question")

					if input.WriteCapable {
						guidance.ValidNext = appendUnique(guidance.ValidNext, "write capability if the target is already clear")
					}
				}
			}
		case workspace.ToolNameEditFile, workspace.ToolNameWriteFile, workspace.ToolNameAppendFile, workspace.ToolNameGoMoveDeclarations:
			if strings.TrimSpace(input.Result.Error) == "" {
				guidance.ValidNext = appendUnique(guidance.ValidNext, "targeted verification evidence")
				guidance.ValidNext = appendUnique(guidance.ValidNext, "required ledger state or answer with the change summary")
			}
		case workspace.ToolNameTaskLedger:
			if writeReadyPacket || requiredPathsRemaining {
				guidance.ValidNext = appendUnique(guidance.ValidNext, "task_ledger does not satisfy the required-path write contract")
				guidance.ValidNext = appendUnique(guidance.ValidNext, "required-path write capability")
				guidance.ValidNext = appendUnique(guidance.ValidNext, "answer with <<QUESTION>> naming the exact blocker for each remaining required path")
			} else {
				guidance.ValidNext = appendUnique(guidance.ValidNext, "continue with the next incomplete ledger item")
				guidance.ValidNext = appendUnique(guidance.ValidNext, "answer if all required ledger items are complete")
			}
		}

		if languageBodyInspectionGuidanceTool(tool) && strings.TrimSpace(input.Result.Error) == "" {
			if writeReadyPacket {
				guidance.ValidNext = appendUnique(guidance.ValidNext, "required-path edit from the packet and inspected evidence")
				guidance.ValidNext = appendUnique(guidance.ValidNext, "answer with <<QUESTION>> only if the edit still has a path-specific blocker")
			} else {
				guidance.ValidNext = appendUnique(guidance.ValidNext, "answer if the inspected body already resolves the question")
				guidance.ValidNext = appendUnique(guidance.ValidNext, "if one exact missing claim remains, request one exact symbol, reference, or narrow range capability that would resolve it")
			}
		}
	}

	if len(guidance.ValidNext) == 0 {
		guidance.ValidNext = appendUnique(guidance.ValidNext, "answer from the current evidence if it already resolves the task")
		guidance.ValidNext = appendUnique(guidance.ValidNext, "otherwise take one exact tool-shaped follow-up tied to a concrete missing claim")
	}

	if !closureActive && input.EvidenceIntent == "relation_trace" && isDiscoveryTool(tool) {
		// Promote semantic navigation ahead of the generic
		// "read a hit" advice. The generic nudges are tuned for "find a
		// file and read it"; a relational question ("how does X call Y")
		// is better answered by routing to symbol definitions and call sites.
		relational := []string{
			"symbol-lookup capability for the named symbol, followed by references capability if call sites are the missing evidence",
			"calls-into or calls-from capability to trace handoff direction from a known symbol",
			"include path or language when multiple language routes are active",
		}
		rebuilt := make([]string, 0, len(relational)+len(guidance.ValidNext))

		for _, hint := range relational {
			rebuilt = appendUnique(rebuilt, hint)
		}

		for _, existing := range guidance.ValidNext {
			rebuilt = appendUnique(rebuilt, existing)
		}

		guidance.ValidNext = rebuilt
	}

	if closureScope != "" {
		guidance.ValidNext = filterClosureGuidanceNext(guidance.ValidNext, closureScope)
	}

	return guidance
}

func closureGuidanceScope(constraints []string, statusCombined string) string {
	switch {
	case ConstraintPresent(constraints, ConstraintChildPacketAvailable):
		return string(ConstraintChildPacketAvailable)
	case ConstraintPresent(constraints, ConstraintAnswerReadinessReached):
		return string(ConstraintAnswerReadinessReached)
	}

	switch {
	case textutil.ContainsAny(statusCombined,
		"child packet already contains sufficient evidence",
		"post-child follow-up missing named gap",
		string(ConstraintChildPacketAvailable),
	):
		return string(ConstraintChildPacketAvailable)
	case textutil.ContainsAny(statusCombined,
		string(ConstraintAnswerReadinessReached),
		"post-readiness follow-up missing named gap",
		string(ConstraintAnswerReadinessReached),
	):
		return string(ConstraintAnswerReadinessReached)
	default:
		return ""
	}
}

func exactAnchorWriteFailure(constraints []string, tool string, statusCombined string) bool {
	if !ConstraintPresent(constraints, ConstraintExactAnchorImplementation) {
		return false
	}

	if !workspace.IsWorkspaceWriteTool(tool) {
		return false
	}

	return textutil.ContainsAny(statusCombined,
		"edit request rejected",
		"write request rejected",
		"patch request rejected",
		"patch hunk did not apply",
		"old_text did not appear",
		"requested lines",
		"outside",
		"line-range",
	)
}

func exactAnchorHiddenPrewriteTool(constraints []string, tool string, statusCombined string) bool {
	if !ConstraintPresent(constraints, ConstraintExactAnchorImplementation) {
		return false
	}

	if workspace.IsWorkspaceWriteTool(tool) || strings.TrimSpace(tool) == workspace.ToolNameTaskLedger || strings.TrimSpace(tool) == workspace.ToolNameHarnessFeedback {
		return false
	}

	return textutil.ContainsAny(statusCombined,
		"tool hidden by active contract",
		"hidden for the rest of this turn by the active runtime contract",
	)
}

func implementationPacketWriteReady(constraints []string) bool {
	return ConstraintPresent(constraints, ConstraintImplementationPacketWriteReady)
}

func implementationRequiredPathsRemaining(constraints []string) bool {
	return ConstraintPresent(constraints, ConstraintImplementationRequiredPathsRemain)
}

func scopeSpecificClosureLabel(scope string) string {
	if strings.TrimSpace(scope) == string(ConstraintChildPacketAvailable) {
		return "child packet"
	}

	return "current evidence"
}

func closureGuidanceNext(subject string) []string {
	if strings.TrimSpace(subject) == "child packet" {
		return []string{
			"final answer from the child packet evidence is available",
			"if one exact missing claim remains, choose one exact named path, symbol, discovered handle, or narrow range that would resolve it",
			"<<QUESTION>> with the exact blocker if neither answer nor bounded probe is possible",
		}
	}

	return []string{
		"final answer from the evidence already gathered is available",
		"if one exact missing claim remains, choose one exact symbol, discovered handle, or narrow range that would resolve it",
		"<<QUESTION>> with the exact blocker if neither answer nor bounded probe is possible",
	}
}

func filterClosureGuidanceNext(values []string, scope string) []string {
	filtered := make([]string, 0, len(values))
	for _, value := range values {
		lower := strings.ToLower(strings.TrimSpace(value))
		switch {
		case strings.Contains(lower, "search_files"),
			strings.Contains(lower, "todo|fixme"),
			strings.Contains(lower, "panic\\("),
			strings.Contains(lower, "exact text search"),
			strings.Contains(lower, "narrow to one exact term or scope"),
			strings.Contains(lower, "continue from the latest evidence with a targeted tool"),
			strings.Contains(lower, "choose a materially different tool or target"),
			strings.Contains(lower, "use a targeted read-only shell search"),
			strings.Contains(lower, "produce the missing evidence or edit required by the gate"),
			strings.Contains(lower, "update task_ledger"),
			strings.Contains(lower, "edit_file"),
			strings.Contains(lower, "verification"),
			strings.Contains(lower, "changed file"),
			strings.Contains(lower, "target-scope update"),
			strings.Contains(lower, "go_move_declarations"),
			strings.Contains(lower, "choose a materially different tool or target"),
			strings.Contains(lower, "use one structured symbol, call/reference, or file-outline probe"),
			strings.Contains(lower, "one structured symbol, call/reference, or file-outline capability"),
			strings.Contains(lower, "one bounded discovery-packet capability"),
			strings.Contains(lower, "read-symbol capability with one outlined declaration name"),
			strings.Contains(lower, "references or calls-into capability on a named declaration"),
			strings.Contains(lower, "ignore the outline"),
			strings.Contains(lower, "read one exact symbol body, discovered handle, or narrow range from the highest-confidence hit"):
			continue
		}

		filtered = appendUnique(filtered, value)
	}

	prefixed := make([]string, 0, len(filtered)+3)
	for _, value := range closureGuidanceNext(scopeSpecificClosureLabel(scope)) {
		prefixed = appendUnique(prefixed, value)
	}

	for _, value := range filtered {
		prefixed = appendUnique(prefixed, value)
	}

	return prefixed
}

// isDiscoveryTool reports whether the just-finished tool was an
// orientation/search step — the only moment it's worth re-ranking the
// suggested next actions. We deliberately exclude edit/write tools
// (post-edit guidance has its own shape) and structured symbol tools
// (already returned what relational queries want).
func isDiscoveryTool(tool string) bool {
	switch strings.TrimSpace(tool) {
	case workspace.ToolNameListFiles, workspace.ToolNameSearchContent, workspace.ToolNameSearchFiles, workspace.ToolNameReadFile, workspace.ToolNameRg:
		return true
	}

	return false
}

func languageOutlineGuidanceTool(tool string) bool {
	return workspace.LanguageToolHasAnyCapability(tool, workspace.CapFileOutline, workspace.CapOverview)
}

func languageBodyInspectionGuidanceTool(tool string) bool {
	return workspace.LanguageToolHasAnyCapability(tool, workspace.CapReadSymbol, workspace.CapExtractBody)
}
