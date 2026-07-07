package workerruntime

import (
	"strings"

	"idiom/internal/capabilities/workspace"
	plancontract "idiom/internal/contracts/plan"
	"idiom/internal/platform/textutil"
)

func BuildStepInstruction(state RunState) StepInstruction {
	state = MergeRuntimeBriefIntoState(WorkerRuntimeBrief{}, state)
	state.TargetRefs = textutil.UniqueNonEmpty(state.TargetRefs)
	state.RequiredEditPaths = textutil.UniqueNonEmpty(state.RequiredEditPaths)
	state.AllowedEditPaths = textutil.UniqueNonEmpty(state.AllowedEditPaths)
	state.EvidencePaths = textutil.UniqueNonEmpty(state.EvidencePaths)
	state.ChangedPaths = textutil.UniqueNonEmpty(state.ChangedPaths)
	state.Constraints = textutil.UniqueNonEmpty(state.Constraints)
	state.AllowedTools = textutil.UniqueNonEmpty(state.AllowedTools)
	state.ForbiddenTools = textutil.UniqueNonEmpty(state.ForbiddenTools)
	state.EvidenceCapabilities = textutil.UniqueNonEmpty(state.EvidenceCapabilities)
	state.RequiredEvidence = textutil.UniqueNonEmpty(state.RequiredEvidence)
	state.RequiredTools = textutil.UniqueNonEmpty(state.RequiredTools)
	state.ReadinessGate = textutil.UniqueNonEmpty(state.ReadinessGate)
	state.EvidenceContract = textutil.UniqueNonEmpty(state.EvidenceContract)
	state.SearchPolicy = textutil.UniqueNonEmpty(state.SearchPolicy)
	state.VerificationPlan = textutil.UniqueNonEmpty(state.VerificationPlan)
	state.StopConditions = textutil.UniqueNonEmpty(state.StopConditions)
	state.FinalCritic = textutil.UniqueNonEmpty(state.FinalCritic)
	state.TargetPacket = textutil.UniqueNonEmpty(state.TargetPacket)
	state.UserBoundaries = textutil.UniqueNonEmpty(state.UserBoundaries)
	state.NextActions = textutil.UniqueNonEmpty(state.NextActions)
	state.InvalidActions = textutil.UniqueNonEmpty(state.InvalidActions)
	state.Readiness = NormalizeReadinessState(state.Readiness)

	directive := "Use this current-state packet for the next action. Treat next_actions as ranked options, not mandatory commands. Do not repeat invalid actions."

	if planThenExecutePlannerState(state) && ConstraintPresent(state.Constraints, ConstraintPlanThenExecuteSubmitPlanReady) {
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
	} else if ConstraintPresent(state.Constraints, ConstraintAnswerReadinessReached) || ConstraintPresent(state.Constraints, ConstraintChildPacketAvailable) {
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
		directive += " Conversation lane: every response must either (a) contain tool calls that investigate the codebase, or (b) deliver a final prose answer to the user. Answer the user's actual specific question — do not begin new investigation on neighboring topics once you have the evidence to answer. For factual or yes/no questions, answer directly from the evidence gathered; for explanatory questions, investigate until you can support the answer with file:line evidence from at least one inspected source location. For mechanism or architecture questions — where thresholds, constants, and type relationships form part of the answer — gather enough targeted evidence to support those claims, give each probe one exact purpose, and stop when the evidence is sufficient. Never re-read the same range or repeat a search with the same scope. Choose from the available evidence capabilities according to the specific gap: localization, graph questions, symbol bodies, bounded source reads, or literal text. When ready, name the evidence paths and file:line citations you inspected. Do not return raw tool summaries, function signatures, or JSON envelopes."
		directive += " Acceptance criteria for your final answer: (1) echo at least two key nouns or terms from the user's question in your answer — this is required for the intent check; (2) every file path, function name, or symbol you cite must be backed by a `read_file` or `code_navigate` call from this session — uncited assertions cause a source-grounding rejection; (3) use `<<QUESTION>>:` only when you have already gathered evidence AND can name the exact missing path, file, or claim — generic blockers like \"I can't answer\" are rejected. These checks apply when you deliver your final answer, not during investigation."
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
		contract = append(contract, "required evidence before contract fulfillment: "+strings.Join(textutil.UniqueNonEmpty(state.RequiredEvidence), ", "))
	}

	if len(state.ReadinessGate) > 0 {
		contract = append(contract, "readiness gates to close or explicitly block: "+strings.Join(textutil.UniqueNonEmpty(state.ReadinessGate), ", "))
	}

	if conversationDirectAnswerReadOnlyState(state) {
		contract = append(contract,
			"for repo-specific answers, localization/context packets scope candidates but do not complete evidence by themselves",
			"minimum inspected evidence before answering: source, symbol, narrow line-range, read_symbol/extract_body, or routed source-body evidence that directly resolves the user's question",
			"terminal condition: answer once inspected evidence resolves the question; continue only for one exact unresolved claim and one bounded probe",
		)
	}

	if len(state.StopConditions) > 0 {
		contract = append(contract, "terminal stop conditions: "+strings.Join(textutil.UniqueNonEmpty(state.StopConditions), ", "))
	}

	return textutil.UniqueNonEmpty(contract)
}

func conversationDirectAnswerReadOnlyState(state RunState) bool {
	return TaskFamilyIsConversation(state.TaskFamily) &&
		RuntimeContractIsDirectAnswer(state.Contract) &&
		PermissionProfileIsReadOnlyWorkspace(state.Permission)
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

	if textutil.EqualTrimFold(state.TaskFamily, "delegated_isolated_implementation") {
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
		state.Readiness = NormalizeReadinessState(brief.Readiness)
	}

	if len(state.AllowedTools) == 0 {
		state.AllowedTools = append([]string(nil), brief.AllowedTools...)
	}

	if len(state.ForbiddenTools) == 0 {
		state.ForbiddenTools = append([]string(nil), brief.ForbiddenTools...)
	}

	if len(state.ToolManifest) == 0 {
		state.ToolManifest = append([]workspace.ToolManifestEntry(nil), brief.ToolManifest...)
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
	brief.Objective = textutil.Truncate(strings.TrimSpace(brief.Objective), 500)
	brief.RequestedOutcome = strings.TrimSpace(brief.RequestedOutcome)
	brief.WriteIntent = strings.TrimSpace(brief.WriteIntent)
	brief.EvidenceIntent = strings.TrimSpace(brief.EvidenceIntent)
	brief.PermissionProfile = strings.TrimSpace(brief.PermissionProfile)
	brief.VerificationContract = strings.TrimSpace(brief.VerificationContract)
	brief.TargetRefs = textutil.UniqueNonEmpty(brief.TargetRefs)
	brief.RequiredEditPaths = textutil.UniqueNonEmpty(brief.RequiredEditPaths)
	brief.AllowedEditPaths = textutil.UniqueNonEmpty(brief.AllowedEditPaths)
	brief.RequiredEditSnippets = normalizeRequiredEditSnippets(brief.RequiredEditSnippets)
	brief.EvidencePaths = textutil.UniqueNonEmpty(brief.EvidencePaths)
	brief.ChangedPaths = textutil.UniqueNonEmpty(brief.ChangedPaths)
	brief.UserBoundaries = textutil.UniqueNonEmpty(brief.UserBoundaries)
	brief.AllowedTools = textutil.UniqueNonEmpty(brief.AllowedTools)
	brief.ForbiddenTools = textutil.UniqueNonEmpty(brief.ForbiddenTools)
	brief.EvidenceCapabilities = textutil.UniqueNonEmpty(brief.EvidenceCapabilities)
	brief.RequiredEvidence = textutil.UniqueNonEmpty(brief.RequiredEvidence)
	brief.RequiredTools = textutil.UniqueNonEmpty(brief.RequiredTools)
	brief.ReadinessGate = textutil.UniqueNonEmpty(brief.ReadinessGate)
	brief.SearchPolicy = textutil.UniqueNonEmpty(brief.SearchPolicy)
	brief.DiscoveryExamples = textutil.UniqueNonEmpty(brief.DiscoveryExamples)
	brief.VerificationPlan = textutil.UniqueNonEmpty(brief.VerificationPlan)
	brief.StopConditions = textutil.UniqueNonEmpty(brief.StopConditions)
	brief.FinalCritic = textutil.UniqueNonEmpty(brief.FinalCritic)
	brief.TargetPacket = textutil.UniqueNonEmpty(brief.TargetPacket)
	brief.Constraints = textutil.UniqueNonEmpty(brief.Constraints)
	brief.RequiredNext = textutil.UniqueNonEmpty(brief.RequiredNext)
	brief.InvalidActions = textutil.UniqueNonEmpty(brief.InvalidActions)
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
		brief.ToolManifest = workspace.BuildToolManifest(brief.AllowedTools)
	}

	if len(brief.EvidenceCapabilities) == 0 && len(brief.ToolManifest) > 0 {
		brief.EvidenceCapabilities = compactEvidenceCapabilitySummary(brief.ToolManifest)
	}

	return brief
}

func normalizeRequiredEditSnippets(snippets []plancontract.RequiredEditSnippet) []plancontract.RequiredEditSnippet {
	if len(snippets) == 0 {
		return nil
	}

	out := make([]plancontract.RequiredEditSnippet, 0, len(snippets))
	seen := make(map[string]struct{}, len(snippets))

	for _, snippet := range snippets {
		normalized := plancontract.RequiredEditSnippet{
			ID:      strings.TrimSpace(snippet.ID),
			Path:    strings.TrimSpace(snippet.Path),
			Content: strings.TrimSpace(snippet.Content),
			Anchor:  strings.TrimSpace(snippet.Anchor),
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
		ClosedClaims:        textutil.UniqueNonEmpty(readiness.ClosedClaims),
		OpenObligations:     textutil.UniqueNonEmpty(readiness.OpenObligations),
		EvidenceSources:     textutil.UniqueNonEmpty(readiness.EvidenceSources),
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
