package workerruntime

import (
	"slices"
	"strings"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/platform/util"
)

func BuildStepInstruction(state RunState) StepInstruction {
	state = MergeRuntimeBriefIntoState(WorkerRuntimeBrief{}, state)
	state.TargetRefs = uniqueNonEmpty(state.TargetRefs)
	state.RequiredEditPaths = uniqueNonEmpty(state.RequiredEditPaths)
	state.AllowedEditPaths = uniqueNonEmpty(state.AllowedEditPaths)
	state.EvidencePaths = uniqueNonEmpty(state.EvidencePaths)
	state.ChangedPaths = uniqueNonEmpty(state.ChangedPaths)
	state.Constraints = uniqueNonEmpty(state.Constraints)
	state.AllowedTools = uniqueNonEmpty(state.AllowedTools)
	state.ForbiddenTools = uniqueNonEmpty(state.ForbiddenTools)
	state.EvidenceCapabilities = uniqueNonEmpty(state.EvidenceCapabilities)
	state.RequiredEvidence = uniqueNonEmpty(state.RequiredEvidence)
	state.RequiredTools = uniqueNonEmpty(state.RequiredTools)
	state.ReadinessGate = uniqueNonEmpty(state.ReadinessGate)
	state.EvidenceContract = uniqueNonEmpty(state.EvidenceContract)
	state.SearchPolicy = uniqueNonEmpty(state.SearchPolicy)
	state.VerificationPlan = uniqueNonEmpty(state.VerificationPlan)
	state.StopConditions = uniqueNonEmpty(state.StopConditions)
	state.FinalCritic = uniqueNonEmpty(state.FinalCritic)
	state.TargetPacket = uniqueNonEmpty(state.TargetPacket)
	state.UserBoundaries = uniqueNonEmpty(state.UserBoundaries)
	state.NextActions = uniqueNonEmpty(state.NextActions)
	state.InvalidActions = uniqueNonEmpty(state.InvalidActions)
	state.Readiness = NormalizeReadinessState(state.Readiness)

	directive := "Use this current-state packet for the next action. Treat next_actions as ranked options, not mandatory commands. Do not repeat invalid actions."

	if planThenExecutePlannerState(state) && slices.Contains(state.Constraints, "plan_then_execute_submit_plan_ready") {
		directive = "Use this current-state packet for the next action. Plan-execute source obligations are closed: do not read, search, shell, verify, or use per-edit write tools. Emit one parser-readable submit_plan with complete executable final edit bodies. Use Decision: source-satisfied only when every required path already contains the requested final state with per-path evidence, or <<QUESTION>> only with exact path-specific blockers. Do not repeat invalid actions."
	} else if implementationStateHasRequiredPathsForMutation(state) && len(state.ChangedPaths) == 0 {
		directive = "Use this current-state packet for the next action. Required edit paths define the implementation scope. Contract outcomes are an in-scope required-path edit, semantic/context/declared project discovery or a narrow required-path source read with named_gap for an exact edit-safety fact, or <<QUESTION>> naming the exact missing anchor or stale fact that blocks the edit. Treat next_actions as ranked options, not mandatory commands. Do not repeat invalid actions."
	} else if implementationStateMissingRequiredPaths(state) {
		directive = "Use this current-state packet for the next action. Required edit paths remain contractual until each one is changed or explicitly blocked. The available contract menu is remaining-path edits, semantic/context/declared project discovery or narrow remaining-path source reads with named_gap for exact edit-safety facts, or concrete blockers. Treat next_actions as ranked options, not mandatory commands. Do not repeat invalid actions."
	} else if state.Readiness != nil && state.Readiness.ReadyToVerify {
		directive = "Use this current-state packet for the next action. The readiness state says verification is the open obligation; resolve verification status or report the concrete blocker before claiming completion. Treat next_actions as ranked options, not mandatory commands. Do not repeat invalid actions."
	} else if genericGuardedMutationNeedsTargetContract(state) {
		directive = "Use this current-state packet for the next action. " + genericGuardedMutationContract() + " Treat next_actions as ranked options, not mandatory commands. Do not repeat invalid actions."
	} else if state.Readiness != nil && state.Readiness.ReadyToWrite {
		directive = "Use this current-state packet for the next action. The readiness state says writing is supported by the current packet/evidence; the contract outcome is an edit or <<QUESTION>> with the exact edit blocker. Treat next_actions as ranked options, not mandatory commands. Do not repeat invalid actions."
	} else if state.Readiness != nil && state.Readiness.ReadyToAnswer {
		directive = "Use this current-state packet for the next action. The readiness state says a final answer is available if the evidence supports it; continue only for one exact missing claim and one exact probe. Treat next_actions as options, not mandatory commands. Do not repeat invalid actions."
	} else if slices.Contains(state.Constraints, "answer_readiness_reached") || slices.Contains(state.Constraints, "child_packet_available") {
		directive = "Use this current-state packet for the next action. A final answer is available if the evidence supports it; continue only for one exact missing claim and one exact probe. Treat next_actions as options, not mandatory commands. Do not repeat invalid actions."
	}

	return StepInstruction{
		CurrentState: state,
		Directive:    directive,
	}
}

func BuildInitialStepInstruction(brief WorkerRuntimeBrief, isFollowUp ...bool) StepInstruction {
	skipConversationDirective := len(isFollowUp) > 0 && isFollowUp[0]
	state := MergeRuntimeBriefIntoState(brief, RunState{Phase: "start", Step: 1})

	state.EvidenceContract = initialEvidenceContract(state)

	directive := "Use this session-state packet when choosing the next action. The system prompt already lists the available tools and how to call them. Treat the search policy and examples as guidance, not a fixed script."

	if len(state.EvidenceCapabilities) > 0 {
		directive += " Available evidence capabilities are summarized in current_state.available_evidence_capabilities; exact parser-readable tool names remain in current_state.tool_manifest."
	}

	if len(state.EvidenceContract) > 0 {
		directive += " Evidence contract / exit criteria are summarized in current_state.evidence_contract; use them before contract fulfillment while you judge whether inspected evidence directly resolves the user's request."
	}

	if TaskFamilyIsConversation(state.TaskFamily) && RuntimeContractIsDirectAnswer(state.Contract) && !skipConversationDirective {
		directive += " Conversation lane: every response must either (a) contain tool calls that investigate the codebase, or (b) deliver a final prose answer to the user. Answer the user's actual specific question — do not begin new investigation on neighboring topics once you have the evidence to answer. For factual or yes/no questions, answer directly from the evidence gathered; for explanatory questions, investigate until you can support the answer with file:line evidence from at least one inspected source location. Prefer answering after 5-15 targeted tool calls over exhaustive exploration. Use context_pack to localize, context_query for graph questions, code_navigate for symbol bodies, read_file for bounded source reads, search_content for literal text. Use the note tool to capture key findings (file paths, function names, mechanism descriptions) as you discover them — notes persist across context compactions and help you build your answer scaffold. When ready, name the evidence paths and file:line citations you inspected. Do not return raw tool summaries, function signatures, or JSON envelopes."
	}

	if readOnlyWorkspaceEvidencePendingState(state) {
		directive += " The read-only workspace evidence gate is open: the first assistant response must be exactly one parser-readable bounded evidence tool request, or <<QUESTION>> with the exact evidence blocker. A prose/final answer is valid only after the evidence tool returns."
	}

	if initialActionContractApplies(state) {
		directive += " For guarded workspace write tasks, the first assistant response must be either exactly one tool call or an explicit blocker/no-op answer; narrative-only implementation summaries are invalid before a successful write, no-op, or blocker."
	}

	if planThenExecutePlannerState(state) && len(state.RequiredEditPaths) > 0 {
		directive += " When required_edit_paths are present in plan-execute planner phase, they are submit_plan coverage scope, not permission for direct edit tools. Follow the active contract protocol card when present; before source-body obligations close, the legal menu is a bounded required-path source-body read, Decision: source-satisfied with per-path current-state evidence, or <<QUESTION>> with the exact blocker. When source-body obligations are closed, submit exactly one parser-readable submit_plan with complete executable final edit bodies."
	} else if implementationStateHasRequiredPathsForMutation(state) {
		directive += " When required_edit_paths are present, treat the packet as the write scope. The available contract menu is an in-scope required-path edit, semantic/context/declared project discovery or a narrow required-path source read with named_gap for an exact edit-safety fact, or <<QUESTION>> with the exact blocker."
	} else if genericGuardedMutationNeedsTargetContract(state) {
		directive += " " + genericGuardedMutationContract()
	} else if state.Readiness != nil && state.Readiness.ReadyToWrite {
		directive += " The readiness state says writing is supported by the current packet/evidence; the contract outcome is an edit or <<QUESTION>> with the exact edit blocker."
	}

	// Readiness state directive tells the model it can answer, but
	// this contradicts the conversation lane's tool-or-answer contract
	// ("keep investigating until you have enough evidence"). The
	// conversation lane must not receive a premature "you're ready"
	// signal — it causes indefinite tool calling when the model can't
	// reconcile "keep investigating" with "one more probe then stop."
	if state.Readiness != nil &&
		state.Readiness.ReadyToAnswer &&
		!state.Readiness.ReadyToWrite &&
		!state.Readiness.ReadyToVerify &&
		!implementationStateHasRequiredPathsForMutation(state) &&
		!genericGuardedMutationNeedsTargetContract(state) &&
		!TaskFamilyIsConversation(state.TaskFamily) &&
		!RuntimeContractIsDirectAnswer(state.Contract) {

		directive += " The readiness state says a final answer is available if the evidence supports it; continue only for one exact missing claim and one exact probe."
	}

	return StepInstruction{
		CurrentState: state,
		Directive:    directive,
	}
}

func initialEvidenceContract(state RunState) []string {
	var contract []string

	if len(state.RequiredEvidence) > 0 {
		contract = append(contract, "required evidence before contract fulfillment: "+strings.Join(uniqueNonEmpty(state.RequiredEvidence), ", "))
	}

	if len(state.ReadinessGate) > 0 {
		contract = append(contract, "readiness gates to close or explicitly block: "+strings.Join(uniqueNonEmpty(state.ReadinessGate), ", "))
	}

	if conversationDirectAnswerReadOnlyState(state) {
		contract = append(contract,
			"for repo-specific answers, localization/context packets scope candidates but do not complete evidence by themselves",
			"minimum inspected evidence before answering: source, symbol, narrow line-range, read_symbol/extract_body, or routed source-body evidence that directly resolves the user's question",
			"terminal condition: answer once inspected evidence resolves the question; continue only for one exact unresolved claim and one bounded probe",
		)
	}

	if len(state.StopConditions) > 0 {
		contract = append(contract, "terminal stop conditions: "+strings.Join(uniqueNonEmpty(state.StopConditions), ", "))
	}

	return uniqueNonEmpty(contract)
}

func conversationDirectAnswerReadOnlyState(state RunState) bool {
	return ConversationDirectAnswerReadOnly(state.TaskFamily, state.Contract, state.Permission)
}

func planThenExecutePlannerState(state RunState) bool {
	return TaskFamilyIsPlanThenExecutePlanner(state.TaskFamily)
}

func initialActionContractApplies(state RunState) bool {
	return PermissionProfileAllowsInitialActionContract(state.Permission) ||
		RuntimeContractRequiresInitialAction(state.Contract) ||
		TaskFamilyRequiresInitialActionContract(state.TaskFamily)
}

func readOnlyWorkspaceEvidencePendingState(state RunState) bool {
	if !PermissionProfileIsReadOnlyWorkspace(state.Permission) {
		return false
	}

	if state.Readiness == nil || state.Readiness.ReadyToAnswer {
		return false
	}

	return len(state.AllowedTools) > 0 || len(state.ToolManifest) > 0 || len(state.RequiredTools) > 0
}

func implementationStateHasRequiredPathsForMutation(state RunState) bool {
	if len(state.RequiredEditPaths) == 0 {
		return false
	}

	return initialActionContractApplies(state)
}

func implementationStateMissingRequiredPaths(state RunState) bool {
	if !implementationStateHasRequiredPathsForMutation(state) || len(state.ChangedPaths) == 0 {
		return false
	}

	changed := make(map[string]struct{}, len(state.ChangedPaths))
	for _, path := range state.ChangedPaths {
		path = strings.TrimSpace(path)
		if path != "" {
			changed[path] = struct{}{}
		}
	}

	for _, path := range state.RequiredEditPaths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}

		if _, ok := changed[path]; !ok {
			return true
		}
	}

	return false
}

func genericGuardedMutationNeedsTargetContract(state RunState) bool {
	if !initialActionContractApplies(state) {
		return false
	}

	if planThenExecutePlannerState(state) {
		return false
	}

	if util.EqualTrimFold(state.TaskFamily, "delegated_isolated_implementation") {
		return false
	}

	if len(state.RequiredEditPaths) > 0 || len(state.ChangedPaths) > 0 {
		return false
	}

	return state.Readiness == nil || !state.Readiness.ReadyToWrite
}

func genericGuardedMutationContract() string {
	return "For guarded workspace mutation without required_edit_paths, select the target before writing and get source-body evidence for that target first. The next visible action must be parser-readable: write when the exact target and edit shape are known, otherwise use bounded discovery/read with a named_gap for the missing target or edit-safety fact, or answer with <<QUESTION>>/no-op and concrete evidence. After a write, verify or report the concrete verification blocker before claiming completion."
}

func MergeRuntimeBriefIntoState(brief WorkerRuntimeBrief, state RunState) RunState {
	brief = NormalizeRuntimeBrief(brief)
	if state.Lane == "" {
		state.Lane = brief.Lane
	}

	if state.TaskFamily == "" {
		state.TaskFamily = brief.TaskFamily
	}

	if state.Contract == "" {
		state.Contract = brief.Contract
	}

	if state.Objective == "" {
		state.Objective = brief.Objective
	}

	if state.RequestedOutcome == "" {
		state.RequestedOutcome = brief.RequestedOutcome
	}

	if state.WriteIntent == "" {
		state.WriteIntent = brief.WriteIntent
	}

	if !state.ReadOnlyConstraint {
		state.ReadOnlyConstraint = brief.ReadOnlyConstraint
	}

	if state.EvidenceIntent == "" {
		state.EvidenceIntent = brief.EvidenceIntent
	}

	if state.Permission == "" {
		state.Permission = brief.PermissionProfile
	}

	if len(state.TargetRefs) == 0 {
		state.TargetRefs = append([]string(nil), brief.TargetRefs...)
	}

	if len(state.RequiredEditPaths) == 0 {
		state.RequiredEditPaths = append([]string(nil), brief.RequiredEditPaths...)
	}

	if len(state.AllowedEditPaths) == 0 {
		state.AllowedEditPaths = append([]string(nil), brief.AllowedEditPaths...)
	}

	state.EvidencePaths = mergeStrings(state.EvidencePaths, brief.EvidencePaths)
	state.ChangedPaths = mergeStrings(state.ChangedPaths, brief.ChangedPaths)
	state.Constraints = mergeStrings(state.Constraints, brief.Constraints)

	if state.Readiness == nil {
		state.Readiness = cloneReadinessState(brief.Readiness)
	}

	if len(state.AllowedTools) == 0 {
		state.AllowedTools = append([]string(nil), brief.AllowedTools...)
	}

	if len(state.ForbiddenTools) == 0 {
		state.ForbiddenTools = append([]string(nil), brief.ForbiddenTools...)
	}

	if len(state.ToolManifest) == 0 {
		state.ToolManifest = append([]ToolManifestEntry(nil), brief.ToolManifest...)
	}

	if len(state.EvidenceCapabilities) == 0 {
		state.EvidenceCapabilities = append([]string(nil), brief.EvidenceCapabilities...)
	}

	if len(state.RequiredEvidence) == 0 {
		state.RequiredEvidence = append([]string(nil), brief.RequiredEvidence...)
	}

	if len(state.RequiredTools) == 0 {
		state.RequiredTools = append([]string(nil), brief.RequiredTools...)
	}

	if len(state.ReadinessGate) == 0 {
		state.ReadinessGate = append([]string(nil), brief.ReadinessGate...)
	}

	if len(state.SearchPolicy) == 0 {
		state.SearchPolicy = append([]string(nil), brief.SearchPolicy...)
	}

	if len(state.DiscoveryExamples) == 0 {
		state.DiscoveryExamples = append([]string(nil), brief.DiscoveryExamples...)
	}

	if state.VerificationContract == "" {
		state.VerificationContract = brief.VerificationContract
	}

	if len(state.VerificationPlan) == 0 {
		state.VerificationPlan = append([]string(nil), brief.VerificationPlan...)
	}

	if len(state.StopConditions) == 0 {
		state.StopConditions = append([]string(nil), brief.StopConditions...)
	}

	if len(state.FinalCritic) == 0 {
		state.FinalCritic = append([]string(nil), brief.FinalCritic...)
	}

	if len(state.TargetPacket) == 0 {
		state.TargetPacket = append([]string(nil), brief.TargetPacket...)
	}

	if len(state.UserBoundaries) == 0 {
		state.UserBoundaries = append([]string(nil), brief.UserBoundaries...)
	}

	if state.Budget == (BudgetBrief{}) {
		state.Budget = brief.Budget
	}

	state.NextActions = mergeStrings(state.NextActions, brief.RequiredNext)
	state.InvalidActions = mergeStrings(state.InvalidActions, brief.InvalidActions)

	return state
}

func NormalizeRuntimeBrief(brief WorkerRuntimeBrief) WorkerRuntimeBrief {
	brief.Lane = strings.TrimSpace(brief.Lane)
	brief.TaskFamily = strings.TrimSpace(brief.TaskFamily)
	brief.Contract = strings.TrimSpace(brief.Contract)
	brief.Objective = util.Truncate(strings.TrimSpace(brief.Objective), 500)
	brief.RequestedOutcome = strings.TrimSpace(brief.RequestedOutcome)
	brief.WriteIntent = strings.TrimSpace(brief.WriteIntent)
	brief.EvidenceIntent = strings.TrimSpace(brief.EvidenceIntent)
	brief.PermissionProfile = strings.TrimSpace(brief.PermissionProfile)
	brief.VerificationContract = strings.TrimSpace(brief.VerificationContract)
	brief.TargetRefs = uniqueNonEmpty(brief.TargetRefs)
	brief.RequiredEditPaths = uniqueNonEmpty(brief.RequiredEditPaths)
	brief.AllowedEditPaths = uniqueNonEmpty(brief.AllowedEditPaths)
	brief.RequiredEditSnippets = normalizeRequiredEditSnippets(brief.RequiredEditSnippets)
	brief.EvidencePaths = uniqueNonEmpty(brief.EvidencePaths)
	brief.ChangedPaths = uniqueNonEmpty(brief.ChangedPaths)
	brief.UserBoundaries = uniqueNonEmpty(brief.UserBoundaries)
	brief.AllowedTools = uniqueNonEmpty(brief.AllowedTools)
	brief.ForbiddenTools = uniqueNonEmpty(brief.ForbiddenTools)
	brief.EvidenceCapabilities = uniqueNonEmpty(brief.EvidenceCapabilities)
	brief.RequiredEvidence = uniqueNonEmpty(brief.RequiredEvidence)
	brief.RequiredTools = uniqueNonEmpty(brief.RequiredTools)
	brief.ReadinessGate = uniqueNonEmpty(brief.ReadinessGate)
	brief.SearchPolicy = uniqueNonEmpty(brief.SearchPolicy)
	brief.DiscoveryExamples = uniqueNonEmpty(brief.DiscoveryExamples)
	brief.VerificationPlan = uniqueNonEmpty(brief.VerificationPlan)
	brief.StopConditions = uniqueNonEmpty(brief.StopConditions)
	brief.FinalCritic = uniqueNonEmpty(brief.FinalCritic)
	brief.TargetPacket = uniqueNonEmpty(brief.TargetPacket)
	brief.Constraints = uniqueNonEmpty(brief.Constraints)
	brief.RequiredNext = uniqueNonEmpty(brief.RequiredNext)
	brief.InvalidActions = uniqueNonEmpty(brief.InvalidActions)
	brief.Readiness = NormalizeReadinessState(brief.Readiness)
	brief.ToolManifest = workspace.SanitizeToolManifestRequests(brief.ToolManifest)

	if PermissionProfileIsConversationOnly(brief.PermissionProfile) {
		brief.AllowedTools = nil
		brief.RequiredTools = nil
		brief.ToolManifest = nil
		brief.EvidenceCapabilities = nil
		brief.DiscoveryExamples = nil
	}

	if len(brief.ToolManifest) == 0 && len(brief.AllowedTools) > 0 {
		brief.ToolManifest = BuildToolManifest(brief.AllowedTools)
	}

	if len(brief.EvidenceCapabilities) == 0 && len(brief.ToolManifest) > 0 {
		brief.EvidenceCapabilities = compactEvidenceCapabilitySummary(brief.ToolManifest)
	}

	return brief
}

func normalizeRequiredEditSnippets(snippets []RequiredEditSnippet) []RequiredEditSnippet {
	if len(snippets) == 0 {
		return nil
	}

	out := make([]RequiredEditSnippet, 0, len(snippets))
	seen := make(map[string]struct{}, len(snippets))

	for _, snippet := range snippets {
		normalized := RequiredEditSnippet{
			ID:      strings.TrimSpace(snippet.ID),
			Path:    strings.TrimSpace(snippet.Path),
			Content: strings.TrimSpace(snippet.Content),
		}
		if normalized.Content == "" {
			continue
		}

		key := normalized.Path + "\x00" + normalized.Content
		if _, ok := seen[key]; ok {
			continue
		}

		seen[key] = struct{}{}

		out = append(out, normalized)
	}

	if len(out) == 0 {
		return nil
	}

	return out
}

func NormalizeReadinessState(readiness *ReadinessState) *ReadinessState {
	if readiness == nil {
		return nil
	}

	normalized := &ReadinessState{
		ReadyToAnswer:       readiness.ReadyToAnswer,
		ReadyToWrite:        readiness.ReadyToWrite,
		ReadyToVerify:       readiness.ReadyToVerify,
		ClosedClaims:        uniqueNonEmpty(readiness.ClosedClaims),
		OpenObligations:     uniqueNonEmpty(readiness.OpenObligations),
		EvidenceSources:     uniqueNonEmpty(readiness.EvidenceSources),
		FurtherReadsRequire: strings.TrimSpace(readiness.FurtherReadsRequire),
	}
	if !normalized.ReadyToAnswer &&
		!normalized.ReadyToWrite &&
		!normalized.ReadyToVerify &&
		len(normalized.ClosedClaims) == 0 &&
		len(normalized.OpenObligations) == 0 &&
		len(normalized.EvidenceSources) == 0 &&
		normalized.FurtherReadsRequire == "" {

		return nil
	}

	return normalized
}

func cloneReadinessState(readiness *ReadinessState) *ReadinessState {
	readiness = NormalizeReadinessState(readiness)
	if readiness == nil {
		return nil
	}

	return &ReadinessState{
		ReadyToAnswer:       readiness.ReadyToAnswer,
		ReadyToWrite:        readiness.ReadyToWrite,
		ReadyToVerify:       readiness.ReadyToVerify,
		ClosedClaims:        append([]string(nil), readiness.ClosedClaims...),
		OpenObligations:     append([]string(nil), readiness.OpenObligations...),
		EvidenceSources:     append([]string(nil), readiness.EvidenceSources...),
		FurtherReadsRequire: readiness.FurtherReadsRequire,
	}
}
