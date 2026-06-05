package autonomy

import "strings"

const TurnPhaseSurfaceSummarySchemaVersion = "idiom.turn_phase_surface_summary.v1"

func SurfaceSummaryFromTurnPhaseContract(phase TurnPhaseContractEvent) TurnPhaseSurfaceSummaryEvent {
	phase.normalize()
	return TurnPhaseSurfaceSummaryEvent{
		SchemaVersion:           TurnPhaseSurfaceSummarySchemaVersion,
		SurfaceID:               "turn_phase_contract",
		ContractID:              phase.ContractID,
		PhaseID:                 phase.PhaseID,
		ModelCallID:             phase.ModelCallID,
		ProtocolHash:            phase.ProtocolHash,
		InvariantID:             firstString(phase.InvariantIDs),
		AdvertisedActionClasses: append([]string(nil), phase.ActionPolicy.AllowedActionClasses...),
		CallableTools:           append([]string(nil), phase.ActionPolicy.CallableToolNames...),
		TerminalOutcomes:        append([]string(nil), phase.TerminalOutcomes...),
		TerminalResponseShapes:  append([]string(nil), phase.ActionPolicy.TerminalResponseShapes...),
		InvalidActionClasses:    append([]string(nil), phase.ActionPolicy.InvalidActionClasses...),
		ObligationRefs:          turnPhaseSurfaceObligationRefs(phase),
		RuntimeCall:             phase.RuntimeCall,
	}
}

func SurfaceSummaryFromModelProtocol(protocol ModelProtocolEvent, phase TurnPhaseContractEvent) TurnPhaseSurfaceSummaryEvent {
	protocol.normalize()
	phase.normalize()
	return TurnPhaseSurfaceSummaryEvent{
		SchemaVersion:           TurnPhaseSurfaceSummarySchemaVersion,
		SurfaceID:               "model_protocol",
		ContractID:              protocol.ContractID,
		PhaseID:                 firstNonEmptyAuditValue(protocol.PhaseID, phase.PhaseID),
		ModelCallID:             phase.ModelCallID,
		ProtocolHash:            protocol.ProtocolHash,
		InvariantID:             protocol.ActiveInvariantID,
		AdvertisedActionClasses: append([]string(nil), protocol.CallableActionClasses...),
		CallableTools:           append([]string(nil), protocol.CallableTools...),
		TerminalOutcomes:        append([]string(nil), protocol.TerminalResponseShapes...),
		TerminalResponseShapes:  append([]string(nil), protocol.TerminalResponseShapes...),
		InvalidActionClasses:    append([]string(nil), protocol.InvalidActionClasses...),
		ObligationRefs: append(append(append([]string(nil),
			protocol.SourceBodyObligations...),
			protocol.RequiredEditCoverage...),
			protocol.VerificationObligations...),
		RuntimeCall: RuntimeCallContractEvent{
			ToolTransportMode:        protocol.ToolTransportMode,
			ParserContract:           protocol.ParserContract,
			ResponseFormat:           protocol.ResponseFormat,
			ToolChoice:               protocol.ToolChoice,
			ExpectedVisibleOutput:    protocol.ExpectedVisibleOutput,
			ExpressibleActionClasses: append([]string(nil), protocol.CallableActionClasses...),
		},
	}
}

func ReconcileTurnPhaseSurface(phase TurnPhaseContractEvent, surface TurnPhaseSurfaceSummaryEvent) []TurnPhaseContradiction {
	phase.normalize()
	surface.normalize()
	if phase.PhaseID == "" || surface.SurfaceID == "" {
		return nil
	}
	reconciler := turnPhaseSurfaceReconciler{
		phase:      phase,
		surface:    surface,
		surfaceID:  surface.SurfaceID,
		pair:       turnPhaseSurfacePair("turn_phase_contract", surface.SurfaceID),
		contractID: surface.ContractID,
	}
	if reconciler.contractID == "" {
		reconciler.contractID = phase.ContractID
	}
	reconciler.checkIdentity()
	reconciler.checkToolVisibility()
	reconciler.checkActionClasses()
	reconciler.checkTerminalOutcomes()
	reconciler.checkRuntimeCompatibility()
	return reconciler.issues
}

type turnPhaseSurfaceReconciler struct {
	phase      TurnPhaseContractEvent
	surface    TurnPhaseSurfaceSummaryEvent
	surfaceID  string
	pair       string
	contractID string
	issues     []TurnPhaseContradiction
}

func (r *turnPhaseSurfaceReconciler) checkIdentity() {
	if r.surface.ContractID != "" && r.phase.ContractID != "" && r.surface.ContractID != r.phase.ContractID {
		r.add("", "", "turn_phase.identity.contract_id", "surface contract_id differs from turn_phase_contract")
	}
	if r.surface.PhaseID != "" && r.phase.PhaseID != "" && r.surface.PhaseID != r.phase.PhaseID {
		r.add("", "", "turn_phase.identity.phase_id", "surface phase_id differs from turn_phase_contract")
	}
	if r.surface.ModelCallID != "" && r.phase.ModelCallID != "" && r.surface.ModelCallID != r.phase.ModelCallID {
		r.add("", "", "turn_phase.identity.model_call_id", "surface model_call_id differs from turn_phase_contract")
	}
	switch {
	case r.phase.ProtocolHash != "" && r.surface.ProtocolHash == "":
		r.add("", "", "turn_phase.identity.protocol_hash", "surface missing protocol_hash")
	case r.phase.ProtocolHash == "" && r.surface.ProtocolHash != "":
		r.add("", "", "turn_phase.identity.protocol_hash", "turn_phase_contract missing protocol_hash")
	case r.surface.ProtocolHash != "" && r.phase.ProtocolHash != "" && r.surface.ProtocolHash != r.phase.ProtocolHash:
		r.add("", "", "turn_phase.identity.protocol_hash", "surface protocol_hash differs from turn_phase_contract")
	}
}

func (r *turnPhaseSurfaceReconciler) checkToolVisibility() {
	callable := TurnPhaseCallableTools(r.phase)
	for _, tool := range r.surface.CallableTools {
		actionClass := TurnPhaseActionClassForTool(tool)
		if actionClass != "" && stringSetContains(r.phase.ActionPolicy.InvalidActionClasses, actionClass) {
			r.add(tool, actionClass, "turn_phase.tool_visibility.invalid_action_class", "surface advertised callable tool whose action class is invalid in turn_phase_contract")
			continue
		}
		if len(callable) > 0 && !stringSetContains(callable, tool) {
			r.add(tool, actionClass, "turn_phase.tool_visibility.disallowed_tool", "surface advertised callable tool absent from turn_phase_contract")
		}
	}
}

func TurnPhaseCallableTools(phase TurnPhaseContractEvent) []string {
	var tools []string
	if len(phase.ActionPolicy.CallableToolNames) > 0 {
		tools = append([]string(nil), phase.ActionPolicy.CallableToolNames...)
	} else {
		tools = append([]string(nil), phase.AllowedTools...)
	}
	return removeTurnPhaseHiddenTools(tools, phase.HiddenTools)
}

func removeTurnPhaseHiddenTools(tools []string, hidden []string) []string {
	if len(tools) == 0 || len(hidden) == 0 {
		return compactStrings(tools)
	}
	hiddenSet := make(map[string]struct{}, len(hidden))
	for _, tool := range hidden {
		tool = strings.TrimSpace(tool)
		if tool != "" {
			hiddenSet[tool] = struct{}{}
		}
	}
	var out []string
	for _, tool := range tools {
		tool = strings.TrimSpace(tool)
		if tool == "" {
			continue
		}
		if _, ok := hiddenSet[tool]; ok {
			continue
		}
		out = append(out, tool)
	}
	return compactStrings(out)
}

func (r *turnPhaseSurfaceReconciler) checkActionClasses() {
	for _, actionClass := range r.surface.AdvertisedActionClasses {
		if stringSetContains(r.phase.ActionPolicy.InvalidActionClasses, actionClass) {
			r.add("", actionClass, "turn_phase.action.invalid_advertised", "surface advertised action class that turn_phase_contract marks invalid")
			continue
		}
		if len(r.phase.ActionPolicy.AllowedActionClasses) > 0 && !stringSetContains(r.phase.ActionPolicy.AllowedActionClasses, actionClass) {
			r.add("", actionClass, "turn_phase.action.not_allowed", "surface advertised action class absent from turn_phase_contract")
		}
	}
	for _, actionClass := range r.surface.InvalidActionClasses {
		if stringSetContains(r.phase.ActionPolicy.AllowedActionClasses, actionClass) {
			r.add("", actionClass, "turn_phase.action.allowed_marked_invalid", "surface marks action class invalid while turn_phase_contract allows it")
		}
	}
}

func (r *turnPhaseSurfaceReconciler) checkTerminalOutcomes() {
	if r.surface.SurfaceID == "next_action_state" {
		return
	}
	surfaceOutcomes := append([]string(nil), r.surface.TerminalOutcomes...)
	surfaceOutcomes = append(surfaceOutcomes, r.surface.TerminalResponseShapes...)
	surfaceOutcomes = compactStrings(surfaceOutcomes)
	if len(surfaceOutcomes) == 0 {
		return
	}
	contractOutcomes := append([]string(nil), r.phase.TerminalOutcomes...)
	contractOutcomes = append(contractOutcomes, r.phase.ActionPolicy.TerminalResponseShapes...)
	contractOutcomes = compactStrings(contractOutcomes)
	surfaceTerminalClasses := turnPhaseActionClassesForTerminalOutcomes(surfaceOutcomes)
	contractTerminalClasses := turnPhaseActionClassesForTerminalOutcomes(contractOutcomes)
	if len(surfaceTerminalClasses) > 0 || len(contractTerminalClasses) > 0 {
		for _, actionClass := range surfaceTerminalClasses {
			if !stringSetContains(contractTerminalClasses, actionClass) {
				r.add("", actionClass, "turn_phase.terminal_outcome.not_allowed", "surface advertised terminal action class absent from turn_phase_contract")
			}
		}
		for _, actionClass := range contractTerminalClasses {
			if !stringSetContains(surfaceTerminalClasses, actionClass) {
				r.add("", actionClass, "turn_phase.terminal_outcome.omitted", "surface omitted terminal action class from turn_phase_contract")
			}
		}
		return
	}
	for _, outcome := range surfaceOutcomes {
		if !stringSetContains(contractOutcomes, outcome) {
			r.add("", "", "turn_phase.terminal_outcome.not_allowed", "surface advertised terminal outcome absent from turn_phase_contract")
		}
	}
	for _, outcome := range contractOutcomes {
		if !stringSetContains(surfaceOutcomes, outcome) {
			r.add("", "", "turn_phase.terminal_outcome.omitted", "surface omitted terminal outcome from turn_phase_contract")
		}
	}
}

func turnPhaseActionClassesForTerminalOutcomes(outcomes []string) []string {
	var classes []string
	for _, outcome := range outcomes {
		lower := strings.ToLower(strings.TrimSpace(outcome))
		if lower == "" {
			continue
		}
		if strings.Contains(lower, "submit_plan") {
			classes = append(classes, ActionClassToken(ActionClassSubmitPlan))
		}
		if strings.Contains(lower, "source-satisfied") {
			classes = append(classes, ActionClassToken(ActionClassSourceSatisfied))
		}
		if strings.Contains(lower, "<<question>>") || strings.Contains(lower, "blocker") {
			classes = append(classes, ActionClassToken(ActionClassBlocker))
		}
		if strings.Contains(lower, "decision: pass") {
			classes = append(classes, ActionClassToken(ActionClassReviewPass))
		}
		if strings.Contains(lower, "final answer") || strings.Contains(lower, "answer directly") || strings.Contains(lower, "answer from") {
			classes = append(classes, ActionClassToken(ActionClassFinalAnswer))
		}
	}
	return compactStrings(classes)
}

func (r *turnPhaseSurfaceReconciler) checkRuntimeCompatibility() {
	runtime := r.surface.RuntimeCall
	r.checkRuntimeField("parser_contract", r.phase.RuntimeCall.ParserContract, runtime.ParserContract)
	r.checkRuntimeField("response_format", r.phase.RuntimeCall.ResponseFormat, runtime.ResponseFormat)
	r.checkRuntimeField("tool_choice", r.phase.RuntimeCall.ToolChoice, runtime.ToolChoice)
	r.checkRuntimeField("expected_visible_output", r.phase.RuntimeCall.ExpectedVisibleOutput, runtime.ExpectedVisibleOutput)
	if runtime.ToolTransportMode != "" && r.phase.RuntimeCall.ToolTransportMode != "" && runtime.ToolTransportMode != r.phase.RuntimeCall.ToolTransportMode {
		r.add("", "", "turn_phase.runtime.tool_transport_mode", "surface runtime_call tool_transport_mode differs from turn_phase_contract")
	}
	if len(runtime.ExpressibleActionClasses) == 0 {
		runtime = r.phase.RuntimeCall
	}
	for _, actionClass := range r.surface.AdvertisedActionClasses {
		if len(runtime.ExpressibleActionClasses) > 0 && !stringSetContains(runtime.ExpressibleActionClasses, actionClass) {
			r.add("", actionClass, "turn_phase.runtime.inexpressible_action", "surface advertised action class that runtime_call cannot express")
		}
	}
}

func (r *turnPhaseSurfaceReconciler) checkRuntimeField(field string, phaseValue string, surfaceValue string) {
	phaseValue = strings.TrimSpace(phaseValue)
	surfaceValue = strings.TrimSpace(surfaceValue)
	if phaseValue == "" || surfaceValue == "" || phaseValue == surfaceValue {
		return
	}
	r.add("", "", "turn_phase.runtime."+field, "surface runtime_call "+field+" differs from turn_phase_contract")
}

func (r *turnPhaseSurfaceReconciler) add(toolName string, actionClass string, invariantID string, reason string) {
	r.issues = append(r.issues, TurnPhaseContradiction{
		ContractID:   r.contractID,
		PhaseID:      r.phase.PhaseID,
		ModelCallID:  firstNonEmptyAuditValue(r.surface.ModelCallID, r.phase.ModelCallID),
		ProtocolHash: firstNonEmptyAuditValue(r.surface.ProtocolHash, r.phase.ProtocolHash),
		SurfaceID:    r.surfaceID,
		SurfacePair:  r.pair,
		InvariantID:  strings.TrimSpace(invariantID),
		ToolName:     strings.TrimSpace(toolName),
		ActionClass:  strings.TrimSpace(actionClass),
		Reason:       strings.TrimSpace(reason),
	})
}

func turnPhaseSurfacePair(left string, right string) string {
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	switch {
	case left == "":
		return right
	case right == "":
		return left
	default:
		return left + "," + right
	}
}

func turnPhaseInvariantIDForReason(reason string) string {
	lower := strings.ToLower(strings.TrimSpace(reason))
	switch {
	case strings.Contains(lower, "runtime_call"):
		return "turn_phase.runtime.inexpressible_action"
	case strings.Contains(lower, "textual phase advertised"):
		return "turn_phase.parser_transport.textual_manifest_tools"
	case strings.Contains(lower, "protocol_hash"):
		return "turn_phase.identity.protocol_hash"
	case strings.Contains(lower, "invalid in latest"):
		return "turn_phase.action.invalid_observed"
	case strings.Contains(lower, "absent"):
		return "turn_phase.tool_visibility.disallowed_tool"
	default:
		return "turn_phase.contract_contradiction"
	}
}

func turnPhaseSurfaceObligationRefs(phase TurnPhaseContractEvent) []string {
	var refs []string
	refs = append(refs, phase.EvidenceState.AcceptanceRefs...)
	refs = append(refs, phase.EvidenceState.TDDObligationRefs...)
	refs = append(refs, phase.EvidenceState.VerificationEvidenceRefs...)
	refs = append(refs, phase.EvidenceState.RecoveryPacketRefs...)
	refs = append(refs, phase.EvidenceState.ResidualBlockers...)
	return compactStrings(refs)
}

func firstString(values []string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
