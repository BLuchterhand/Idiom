package agent

import (
	"fmt"
	"strings"

	"idiom/internal/autonomy"
	"idiom/internal/frontier/workerruntime"
	"idiom/internal/workspace"
)

const recoveryCardSchemaVersion = "idiom.recovery_card.v1"

// RecoveryCard is a generic model-facing recovery packet for a rejected or
// low-value action. It explains the active invariant and valid recovery surface
// without choosing strategy unless the harness has a deterministic safe action.
type RecoveryCard struct {
	SchemaVersion string

	ContractID   string
	PhaseID      string
	InvariantID  string
	EvidenceRefs []string

	RejectedAction RecoveryRejectedAction
	Invariant      string
	InvalidRepeats []string

	CallableTools         []string
	CallableActionClasses []string
	TerminalOutcomes      []string

	DeterministicSafeNextAction string
	ExactBlockerShape           string
}

type RecoveryRejectedAction struct {
	Tool        string
	ActionClass string
	Target      string
	Reason      string
}

type RecoveryCardInput struct {
	PhaseContract       TurnPhaseContract
	CurrentAllowedTools []string
	RejectedRequest     workspace.ToolRequest
	RejectedResult      workspace.ToolResult
	EvidenceRefs        []string

	InvalidRepeats              []string
	DeterministicSafeNextAction string
	ExactBlockerShape           string
}

func BuildRecoveryCard(input RecoveryCardInput) RecoveryCard {
	contract := input.PhaseContract
	invariantID := recoveryCardInvariantID(input)
	deterministicSafeNextAction := firstRecoveryCardString(
		input.DeterministicSafeNextAction,
		recoveryCardSafeNextActionFromPhaseContract(contract),
		recoveryCardStrategyNextActionFromPhaseContract(contract),
	)
	return NormalizeRecoveryCard(RecoveryCard{
		ContractID:  contract.ContractID,
		PhaseID:     contract.PhaseID,
		InvariantID: invariantID,
		EvidenceRefs: mergeRecoveryCardStrings(
			input.EvidenceRefs,
			recoveryCardEvidenceRefsFromContract(contract),
		),
		RejectedAction: RecoveryRejectedAction{
			Tool:        recoveryCardRejectedTool(input.RejectedRequest, input.RejectedResult),
			ActionClass: recoveryCardRejectedActionClass(input.RejectedRequest, input.RejectedResult),
			Target:      workerruntimeToolTarget(input.RejectedRequest),
			Reason:      recoveryCardRejectedReason(input.RejectedResult),
		},
		Invariant:                   recoveryCardInvariantText(invariantID, contract),
		InvalidRepeats:              mergeRecoveryCardStrings(input.InvalidRepeats, recoveryCardInvalidRepeats(invariantID, contract)),
		CallableTools:               recoveryCardCallableTools(contract, input.CurrentAllowedTools),
		CallableActionClasses:       recoveryCardCallableActionClasses(contract, input.CurrentAllowedTools),
		TerminalOutcomes:            recoveryCardTerminalOutcomes(contract),
		DeterministicSafeNextAction: deterministicSafeNextAction,
		ExactBlockerShape:           recoveryCardExactBlockerShape(input.ExactBlockerShape, contract),
	})
}

func RenderRecoveryCard(card RecoveryCard) string {
	return NormalizeRecoveryCard(card).Render()
}

func appendRecoveryCardToMessage(message string, card string) string {
	message = strings.TrimSpace(message)
	card = strings.TrimSpace(card)
	switch {
	case message == "":
		return card
	case card == "":
		return message
	default:
		return message + "\n\n" + card
	}
}

func (r *Runner) renderRecoveryCardForRejectedAction(req workspace.ToolRequest, result workspace.ToolResult, results []workspace.ToolResult, currentAllowedTools []string) string {
	if r == nil {
		return ""
	}
	brief := r.visibleBriefForToolSurface(r.RuntimeBrief, results)
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief:            runnerBriefWithPhaseHints(brief, results),
		Results:          results,
		LastRequest:      req,
		LastResult:       result,
		BaseAllowedTools: currentAllowedTools,
	})
	return RenderRecoveryCard(BuildRecoveryCard(RecoveryCardInput{
		PhaseContract:               contract,
		CurrentAllowedTools:         effectiveRecoveryCardAllowedTools(currentAllowedTools, brief, results, contract),
		RejectedRequest:             req,
		RejectedResult:              result,
		EvidenceRefs:                runnerEvidencePathsForBrief(brief, results, 6),
		DeterministicSafeNextAction: recoveryCardSafeNextActionForRejectedAction(brief, contract, results),
	}))
}

func NormalizeRecoveryCard(card RecoveryCard) RecoveryCard {
	if strings.TrimSpace(card.SchemaVersion) == "" {
		card.SchemaVersion = recoveryCardSchemaVersion
	}
	card.ContractID = strings.TrimSpace(card.ContractID)
	card.PhaseID = strings.TrimSpace(card.PhaseID)
	card.InvariantID = strings.TrimSpace(card.InvariantID)
	card.EvidenceRefs = uniqueRecoveryCardStrings(card.EvidenceRefs)
	card.RejectedAction = normalizeRecoveryRejectedAction(card.RejectedAction)
	card.Invariant = strings.TrimSpace(card.Invariant)
	card.InvalidRepeats = uniqueRecoveryCardStrings(card.InvalidRepeats)
	card.CallableTools = uniqueRecoveryCardStrings(card.CallableTools)
	card.CallableActionClasses = uniqueRecoveryCardStrings(card.CallableActionClasses)
	card.TerminalOutcomes = uniqueRecoveryCardStrings(card.TerminalOutcomes)
	card.DeterministicSafeNextAction = strings.TrimSpace(card.DeterministicSafeNextAction)
	card.ExactBlockerShape = sanitizeRecoveryCardBlockerShape(card.ExactBlockerShape)
	return card
}

func (card RecoveryCard) Render() string {
	card = NormalizeRecoveryCard(card)

	var b strings.Builder
	b.WriteString("Recovery card")
	writeRecoveryCardLine(&b, "schema", card.SchemaVersion)
	writeRecoveryCardLine(&b, "contract", recoveryCardContractLabel(card))
	writeRecoveryCardLine(&b, "evidence", strings.Join(card.EvidenceRefs, ", "))
	writeRecoveryCardLine(&b, "rejected action", card.RejectedAction.label())
	writeRecoveryCardLine(&b, "invariant", recoveryCardInvariantLabel(card))
	writeRecoveryCardLine(&b, "invalid repeats", recoveryCardListLabel(card.InvalidRepeats, "none recorded"))
	writeRecoveryCardLine(&b, "currently callable tools", recoveryCardListLabel(card.CallableTools, "none"))
	writeRecoveryCardLine(&b, "currently callable action classes", recoveryCardListLabel(card.CallableActionClasses, "none"))
	writeRecoveryCardLine(&b, "deterministic safe next action", recoveryCardOptionalLabel(card.DeterministicSafeNextAction, "none available"))
	writeRecoveryCardLine(&b, "exact blocker shape", recoveryCardOptionalLabel(card.ExactBlockerShape, "none available"))
	return strings.TrimRight(b.String(), "\n")
}

func (card RecoveryCard) TurnPhaseSurfaceSummary(contract TurnPhaseContract) autonomy.TurnPhaseSurfaceSummaryEvent {
	card = NormalizeRecoveryCard(card)
	return autonomy.TurnPhaseSurfaceSummaryEvent{
		SchemaVersion:           autonomy.TurnPhaseSurfaceSummarySchemaVersion,
		SurfaceID:               "recovery_card",
		ContractID:              card.ContractID,
		PhaseID:                 firstRecoveryCardString(card.PhaseID, contract.PhaseID),
		ModelCallID:             contract.ModelCallID,
		ProtocolHash:            contract.autonomyEventPayload().ProtocolHash,
		InvariantID:             card.InvariantID,
		AdvertisedActionClasses: append([]string(nil), card.CallableActionClasses...),
		CallableTools:           append([]string(nil), card.CallableTools...),
		TerminalOutcomes:        append([]string(nil), card.TerminalOutcomes...),
		TerminalResponseShapes:  append([]string(nil), card.TerminalOutcomes...),
		ObligationRefs:          append([]string(nil), card.EvidenceRefs...),
	}
}

func sanitizeRecoveryCardBlockerShape(shape string) string {
	shape = strings.TrimSpace(shape)
	if shape == "" {
		return ""
	}
	if strings.Contains(shape, "<") && strings.Contains(shape, ">") {
		return "<<QUESTION>>: State the exact blocker and cite the concrete evidence source, path, command, or capability that is unavailable or failed."
	}
	return shape
}

func normalizeRecoveryRejectedAction(action RecoveryRejectedAction) RecoveryRejectedAction {
	action.Tool = strings.TrimSpace(action.Tool)
	action.ActionClass = strings.TrimSpace(action.ActionClass)
	action.Target = strings.TrimSpace(action.Target)
	action.Reason = strings.TrimSpace(action.Reason)
	if action.Tool == "completion_gate" {
		action.Tool = ""
		if action.ActionClass == "" {
			action.ActionClass = actionFinalAnswer
		}
	}
	return action
}

func (action RecoveryRejectedAction) label() string {
	var parts []string
	if action.Tool != "" {
		parts = append(parts, "tool="+quoteRecoveryCardValue(action.Tool))
	}
	if action.ActionClass != "" {
		parts = append(parts, "action_class="+quoteRecoveryCardValue(action.ActionClass))
	}
	if action.Target != "" {
		parts = append(parts, "target="+quoteRecoveryCardValue(action.Target))
	}
	if action.Reason != "" {
		parts = append(parts, "reason="+quoteRecoveryCardValue(action.Reason))
	}
	if len(parts) == 0 {
		return "unspecified"
	}
	return strings.Join(parts, " ")
}

func recoveryCardContractLabel(card RecoveryCard) string {
	var parts []string
	if card.ContractID != "" {
		parts = append(parts, "contract_id="+quoteRecoveryCardValue(card.ContractID))
	}
	if card.PhaseID != "" {
		parts = append(parts, "phase_id="+quoteRecoveryCardValue(card.PhaseID))
	}
	if card.InvariantID != "" {
		parts = append(parts, "invariant_id="+quoteRecoveryCardValue(card.InvariantID))
	}
	if len(parts) == 0 {
		return "unspecified"
	}
	return strings.Join(parts, " ")
}

func recoveryCardInvariantLabel(card RecoveryCard) string {
	if card.Invariant == "" {
		return recoveryCardOptionalLabel(card.InvariantID, "unspecified")
	}
	if card.InvariantID == "" {
		return card.Invariant
	}
	return fmt.Sprintf("%s: %s", card.InvariantID, card.Invariant)
}

func recoveryCardOptionalLabel(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}

func recoveryCardListLabel(values []string, fallback string) string {
	if len(values) == 0 {
		return fallback
	}
	return strings.Join(values, ", ")
}

func writeRecoveryCardLine(b *strings.Builder, label, value string) {
	if strings.TrimSpace(value) == "" {
		return
	}
	b.WriteString("\n- ")
	b.WriteString(label)
	b.WriteString(": ")
	b.WriteString(value)
}

func quoteRecoveryCardValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if strings.ContainsAny(value, " \t\n\r,:") {
		return fmt.Sprintf("%q", value)
	}
	return value
}

func uniqueRecoveryCardStrings(values []string) []string {
	seen := make(map[string]bool, len(values))
	var out []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func recoveryCardInvariantID(input RecoveryCardInput) string {
	if len(input.PhaseContract.InvariantIDs) > 0 {
		return input.PhaseContract.InvariantIDs[0]
	}
	lower := strings.ToLower(strings.TrimSpace(input.RejectedResult.Summary + "\n" + input.RejectedResult.Error))
	switch {
	case strings.Contains(lower, "zero-hit") || strings.Contains(lower, "zero hit") || strings.Contains(lower, "no results"):
		return "evidence.zero_hit_not_absence"
	case strings.Contains(lower, "repeated dead-end") || strings.Contains(lower, "repeated range-less read") || strings.Contains(lower, "already fully read"):
		return "loop.repeated_dead_end"
	case strings.Contains(lower, "completion gate") && input.PhaseContract.PhaseID == turnPhaseImplementationVerifyReady:
		return "completion.post_write_verification"
	case strings.Contains(lower, "completion gate"):
		return "completion.phase_contract"
	case strings.Contains(lower, "submit_plan"):
		return "parser.submit_plan_required"
	case strings.Contains(lower, "not available") || strings.Contains(lower, "hidden"):
		return "surface.current_phase_tools"
	default:
		if strings.TrimSpace(input.PhaseContract.PhaseID) != "" && input.PhaseContract.PhaseID != turnPhaseGeneric {
			return "phase.current_contract"
		}
		return "recovery.current_evidence"
	}
}

func recoveryCardInvariantText(invariantID string, contract TurnPhaseContract) string {
	switch invariantID {
	case "evidence.zero_hit_not_absence":
		return "A zero-hit search is not source-body evidence for absence."
	case "loop.repeated_dead_end":
		return "Repeating the same rejected or low-value action does not add evidence."
	case "completion.post_write_verification":
		return "A completion answer requires verification evidence or an exact verification blocker after writes."
	case "completion.phase_contract":
		return "Completion must satisfy the active phase contract before a final answer is accepted."
	case "parser.submit_plan_required":
		if !recoveryCardBlockerAllowed(contract) {
			return "The active phase requires one parser-readable submit_plan action that satisfies the current phase contract."
		}
		return "The active phase requires one parser-readable submit_plan action or a supported terminal blocker."
	case "surface.current_phase_tools":
		return "Only the tools and action classes exposed by the current phase contract are callable."
	case "phase.current_contract":
		return "The next action must satisfy the active phase contract."
	default:
		if next := firstRecoveryCardString(contract.RequiredNext...); next != "" {
			return "The next action must satisfy the active phase contract: " + next
		}
		return "Use the gathered evidence, current tool surface, and exact blocker shape to recover."
	}
}

func recoveryCardSafeNextActionForRejectedAction(brief workerruntime.WorkerRuntimeBrief, contract TurnPhaseContract, results []workspace.ToolResult) string {
	if contract.PhaseID != turnPhaseImplementationWriteReady && contract.PhaseID != turnPhaseImplementationTargetSource {
		return ""
	}
	if pending := sourceEvidencePendingRedirectsForBrief(brief, results); len(pending) > 0 {
		if recoveryCardActionClassAllowed(contract, actionRead) {
			callablePending := recoveryCardCallablePendingRedirects(contract, pending)
			if summary := sourceEvidencePendingRedirectActionSummary(callablePending, 3); summary != "" {
				return "Resolve the exact pending redirected source-body obligation (" + summary + "), then emit one parser-readable write to a sourced target path. For textual write transport, use one visible JSON object or apply_patch envelope, not key/value edit-body notes; or answer <<QUESTION>> with the exact edit blocker/no-op evidence."
			}
		}
	}
	if sourcedSummary := implementationTargetWriteReadySourcedPathSummary(results, 3); sourcedSummary != "" {
		return "Emit one parser-readable write to the already-sourced path(s): " + sourcedSummary + ". For textual write transport, use one visible JSON object or apply_patch envelope, not key/value edit-body notes; or answer <<QUESTION>> with the exact edit blocker/no-op evidence."
	}
	if contract.PhaseID == turnPhaseImplementationTargetSource {
		return "Make one bounded source-body read for the concrete edit target with named_gap, then emit one parser-readable write. For textual write transport, use one visible JSON object or apply_patch envelope, not key/value edit-body notes; or answer <<QUESTION>> with the exact edit blocker/no-op evidence."
	}
	return ""
}

func recoveryCardSafeNextActionFromPhaseContract(contract TurnPhaseContract) string {
	next := firstRecoveryCardString(contract.RequiredNext...)
	if next == "" {
		return ""
	}
	switch contract.PhaseID {
	case turnPhaseImplementationWriteReady:
		return "Follow the active write-ready requirement: " + next
	case turnPhaseImplementationVerifyReady:
		if contract.Phase != "verification" || !recoveryCardActionClassesExactly(contract, actionVerify, actionBlocker) {
			return ""
		}
		return "Run one focused verification action from the active verification requirement: " + next
	case turnPhaseSubmitReadyNativeRequired, turnPhaseSubmitReadyTextual, turnPhaseMalformedSubmitPlanRecovery:
		return "Emit one parser-readable planner outcome: " + next
	case turnPhaseReviewRepairTerminal:
		return "Emit one terminal review-repair outcome: " + next
	case turnPhaseArtifactFinalAnswer:
		return "Emit the active terminal outcome: " + next
	default:
		if contract.Phase == "read_only_evidence_ready" && recoveryCardActionClassesExactly(contract, actionFinalAnswer, actionBlocker) {
			return "Answer from the gathered bounded evidence now, or answer <<QUESTION>> only with the exact unsupported claim."
		}
		return ""
	}
}

func recoveryCardStrategyNextActionFromPhaseContract(contract TurnPhaseContract) string {
	classes := firstNonEmptyRecoveryCardStrings(
		contract.RuntimeCall.ExpressibleActionClasses,
		contract.ActionPolicy.AllowedActionClasses,
	)
	if !slicesContainRecoveryCardString(classes, actionFinalAnswer) {
		return ""
	}
	phase := strings.TrimSpace(contract.Phase)
	if strings.HasPrefix(phase, "read_only_") ||
		slicesContainRecoveryCardString(recoveryCardTerminalOutcomes(contract), actionFinalAnswer) {
		return "Let the model choose strategy: answer from gathered evidence now, or state uncertainty with the exact unsupported claim and evidence source."
	}
	return ""
}

func recoveryCardActionClassAllowed(contract TurnPhaseContract, class string) bool {
	classes := firstNonEmptyRecoveryCardStrings(
		contract.RuntimeCall.ExpressibleActionClasses,
		contract.ActionPolicy.AllowedActionClasses,
	)
	return slicesContainRecoveryCardString(classes, class)
}

func recoveryCardActionClassesExactly(contract TurnPhaseContract, expected ...string) bool {
	classes := firstNonEmptyRecoveryCardStrings(
		contract.RuntimeCall.ExpressibleActionClasses,
		contract.ActionPolicy.AllowedActionClasses,
	)
	classes = uniqueRecoveryCardStrings(classes)
	expected = uniqueRecoveryCardStrings(expected)
	if len(classes) != len(expected) {
		return false
	}
	for _, class := range expected {
		if !slicesContainRecoveryCardString(classes, class) {
			return false
		}
	}
	return true
}

func recoveryCardCallablePendingRedirects(contract TurnPhaseContract, pending []sourceEvidenceObligation) []sourceEvidenceObligation {
	callableTools := firstNonEmptyRecoveryCardStrings(
		contract.ActionPolicy.CallableToolNames,
		contract.AllowedTools,
	)
	if len(callableTools) == 0 || len(pending) == 0 {
		return nil
	}
	callable := make(map[string]struct{}, len(callableTools))
	for _, tool := range callableTools {
		if tool = strings.TrimSpace(tool); tool != "" {
			callable[tool] = struct{}{}
		}
	}
	filtered := make([]sourceEvidenceObligation, 0, len(pending))
	for _, obligation := range pending {
		if _, ok := callable[strings.TrimSpace(obligation.PendingTool)]; ok {
			filtered = append(filtered, obligation)
		}
	}
	return filtered
}

func recoveryCardInvalidRepeats(invariantID string, contract TurnPhaseContract) []string {
	var repeats []string
	switch invariantID {
	case "evidence.zero_hit_not_absence":
		repeats = append(repeats, "repeat the same zero-hit search", "broaden the query without naming one missing claim")
	case "loop.repeated_dead_end":
		repeats = append(repeats, "repeat the same rejected action class", "ask for another generic probe without a named gap")
	case "completion.post_write_verification":
		repeats = append(repeats, "final answer without verification status", "more source reads before naming the verification blocker")
	case "parser.submit_plan_required":
		repeats = append(repeats, "prose-only final answer", "non-parser-readable action")
	}
	return mergeRecoveryCardStrings(repeats, contract.InvalidNext)
}

func recoveryCardCallableTools(contract TurnPhaseContract, currentAllowedTools []string) []string {
	phaseTools := firstNonEmptyRecoveryCardStrings(contract.ActionPolicy.CallableToolNames, contract.AllowedTools)
	current := uniqueRecoveryCardStrings(currentAllowedTools)
	phaseTools = removeRecoveryCardStrings(phaseTools, contract.HiddenTools)
	current = removeRecoveryCardStrings(current, contract.HiddenTools)
	if len(phaseTools) == 0 && recoveryCardActionPolicyHasNoToolClasses(contract.ActionPolicy.AllowedActionClasses) {
		return nil
	}
	if len(current) == 0 {
		return uniqueRecoveryCardStrings(phaseTools)
	}
	if len(phaseTools) == 0 {
		return current
	}
	phaseSet := make(map[string]struct{}, len(phaseTools))
	for _, tool := range phaseTools {
		phaseSet[strings.TrimSpace(tool)] = struct{}{}
	}
	var out []string
	for _, tool := range current {
		if _, ok := phaseSet[tool]; ok {
			out = append(out, tool)
		}
	}
	return uniqueRecoveryCardStrings(out)
}

func removeRecoveryCardStrings(values []string, remove []string) []string {
	if len(values) == 0 || len(remove) == 0 {
		return values
	}
	removed := make(map[string]struct{}, len(remove))
	for _, value := range remove {
		value = strings.TrimSpace(value)
		if value != "" {
			removed[value] = struct{}{}
		}
	}
	var out []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := removed[value]; ok {
			continue
		}
		out = append(out, value)
	}
	return uniqueRecoveryCardStrings(out)
}

func recoveryCardActionPolicyHasNoToolClasses(classes []string) bool {
	classes = uniqueRecoveryCardStrings(classes)
	if len(classes) == 0 {
		return false
	}
	for _, class := range classes {
		switch strings.TrimSpace(class) {
		case actionRead, actionSearch, actionPerEditWrite, actionVerify, actionSubmitPlan, actionGenericProbe:
			return false
		}
	}
	return true
}

func recoveryCardCallableActionClasses(contract TurnPhaseContract, currentAllowedTools []string) []string {
	contractClasses := firstNonEmptyRecoveryCardStrings(
		contract.RuntimeCall.ExpressibleActionClasses,
		contract.ActionPolicy.AllowedActionClasses,
	)
	contractClasses = actionClassesAllowedByPolicy(contractClasses, contract.ActionPolicy.InvalidActionClasses)
	toolClasses := recoveryCardCallableActionClassesForTools(recoveryCardCallableTools(contract, currentAllowedTools), contractClasses)
	if len(toolClasses) == 0 {
		return contractClasses
	}
	return actionClassesAllowedByPolicy(mergeRecoveryCardStrings(toolClasses, contractClasses), contract.ActionPolicy.InvalidActionClasses)
}

func recoveryCardTerminalOutcomes(contract TurnPhaseContract) []string {
	return firstNonEmptyRecoveryCardStrings(
		contract.ActionPolicy.TerminalResponseShapes,
		contract.TerminalOutcomes,
	)
}

func recoveryCardCallableActionClassesForTools(tools []string, contractClasses []string) []string {
	var classes []string
	for _, tool := range tools {
		switch tool = strings.TrimSpace(tool); {
		case tool == "":
			continue
		case tool == "submit_plan":
			classes = append(classes, actionSubmitPlan)
		case tool == "source_sanity" || tool == "harness_feedback" || tool == "idiom_feedback" || tool == "context_trace_pack" || tool == "git_diff" || tool == "git_status" || strings.HasSuffix(tool, "_diagnostics"):
			classes = append(classes, actionVerify)
		case tool == "shell":
			if slicesContainRecoveryCardString(contractClasses, actionVerify) {
				classes = append(classes, actionVerify)
			} else {
				classes = append(classes, actionGenericProbe)
			}
		default:
			switch workspace.ClassifyTool(tool) {
			case workspace.CategorySearch:
				classes = append(classes, actionSearch)
			case workspace.CategoryRead, workspace.CategoryDiscovery:
				classes = append(classes, actionRead)
			case workspace.CategoryWrite:
				classes = append(classes, actionPerEditWrite)
			case workspace.CategoryVerification:
				classes = append(classes, actionVerify)
			case workspace.CategoryShell, workspace.CategoryMCP:
				classes = append(classes, actionGenericProbe)
			}
		}
	}
	return uniqueRecoveryCardStrings(classes)
}

func recoveryCardBlockerShape(contract TurnPhaseContract) string {
	if !recoveryCardBlockerAllowed(contract) {
		return ""
	}
	classes := firstNonEmptyRecoveryCardStrings(
		contract.RuntimeCall.ExpressibleActionClasses,
		contract.ActionPolicy.AllowedActionClasses,
	)
	for _, class := range classes {
		switch class {
		case actionVerify:
			return "<<QUESTION>>: Verification blocked because the named command or capability cannot run; include the concrete reason."
		case actionSubmitPlan:
			return "<<QUESTION>>: Cannot submit_plan because the named required path or edit body evidence is unavailable; include the concrete reason."
		case actionRead, actionSearch:
			return "<<QUESTION>>: Cannot resolve the named claim because the needed source range, candidate path, or capability is unavailable."
		}
	}
	return "<<QUESTION>>: State the exact unsupported claim and the concrete evidence source that is unavailable or failed."
}

func recoveryCardExactBlockerShape(shape string, contract TurnPhaseContract) string {
	if !recoveryCardBlockerAllowed(contract) {
		return ""
	}
	return firstRecoveryCardString(shape, recoveryCardBlockerShape(contract))
}

func recoveryCardBlockerAllowed(contract TurnPhaseContract) bool {
	if slicesContainRecoveryCardString(contract.ActionPolicy.InvalidActionClasses, actionBlocker) {
		return false
	}
	classes := firstNonEmptyRecoveryCardStrings(
		contract.RuntimeCall.ExpressibleActionClasses,
		contract.ActionPolicy.AllowedActionClasses,
	)
	classes = actionClassesAllowedByPolicy(classes, contract.ActionPolicy.InvalidActionClasses)
	if slicesContainRecoveryCardString(classes, actionBlocker) {
		return true
	}
	for _, outcome := range recoveryCardTerminalOutcomes(contract) {
		if strings.Contains(outcome, "<<QUESTION>>") {
			return true
		}
	}
	return false
}

func recoveryCardEvidenceRefsFromContract(contract TurnPhaseContract) []string {
	return mergeRecoveryCardStrings(
		contract.EvidenceState.AcceptanceRefs,
		contract.EvidenceState.VerificationEvidenceRefs,
		contract.EvidenceState.RecoveryPacketRefs,
		contract.EvidenceState.ResidualBlockers,
	)
}

func recoveryCardRejectedTool(req workspace.ToolRequest, result workspace.ToolResult) string {
	if tool := strings.TrimSpace(req.Tool); tool != "" {
		return tool
	}
	return strings.TrimSpace(result.Tool)
}

func recoveryCardRejectedActionClass(req workspace.ToolRequest, result workspace.ToolResult) string {
	tool := recoveryCardRejectedTool(req, result)
	switch {
	case tool == "completion_gate":
		return actionFinalAnswer
	case tool == "submit_plan":
		return actionSubmitPlan
	case workspace.IsWorkspaceWriteTool(tool):
		return actionPerEditWrite
	case isVerificationRequest(workspace.ToolRequest{Tool: tool, Command: req.Command, Path: req.Path}):
		return actionVerify
	case isReadClassTool(workspace.ToolRequest{Tool: tool, Capability: req.Capability}):
		return actionRead
	case tool == "search_files" || tool == "search_content" || tool == "rg" || tool == "grep":
		return actionSearch
	default:
		return strings.TrimSpace(toolActionClass(req))
	}
}

func recoveryCardRejectedReason(result workspace.ToolResult) string {
	return firstRecoveryCardString(result.Summary, result.Error)
}

func workerruntimeToolTarget(req workspace.ToolRequest) string {
	return workerruntime.ToolRequestTarget(req)
}

func firstRecoveryCardString(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func firstNonEmptyRecoveryCardStrings(groups ...[]string) []string {
	for _, group := range groups {
		if len(uniqueRecoveryCardStrings(group)) > 0 {
			return uniqueRecoveryCardStrings(group)
		}
	}
	return nil
}

func mergeRecoveryCardStrings(groups ...[]string) []string {
	var merged []string
	for _, group := range groups {
		merged = append(merged, group...)
	}
	return uniqueRecoveryCardStrings(merged)
}

func slicesContainRecoveryCardString(values []string, needle string) bool {
	needle = strings.TrimSpace(needle)
	for _, value := range values {
		if strings.TrimSpace(value) == needle {
			return true
		}
	}
	return false
}
