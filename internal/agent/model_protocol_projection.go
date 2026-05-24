package agent

import (
	"slices"
	"strings"

	"idiom/internal/autonomy"
	"idiom/internal/contractcompile"
	"idiom/internal/frontier/workerruntime"
	"idiom/internal/llama"
	"idiom/internal/util"
	"idiom/internal/workspace"
)

const modelProtocolProjectionSchemaVersion = "idiom.model_protocol_projection.v1"

func (r *Runner) compileModelProtocolProjection(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, phase TurnPhaseContract, policy RunPolicy, supportsNativeTools bool) (contractcompile.ModelProtocolProjection, bool) {
	brief = workerruntime.NormalizeRuntimeBrief(brief)
	if strings.TrimSpace(brief.TaskFamily) != PlanThenExecutePlannerTaskFamily {
		return contractcompile.ModelProtocolProjection{}, false
	}
	input := contractcompile.PlanExecutePlannerInput{
		ParentID:             strings.TrimSpace(phase.ContractID),
		Lane:                 util.FirstNonEmpty(strings.TrimSpace(brief.Lane), contractcompile.DefaultPlanExecuteLane),
		Phase:                contractcompile.DefaultPlannerPhase,
		TaskFamily:           contractcompile.InvocationPlanExecutePlanner,
		RuntimeContract:      strings.TrimSpace(brief.Contract),
		PermissionProfile:    strings.TrimSpace(brief.PermissionProfile),
		TargetRefs:           append([]string(nil), brief.TargetRefs...),
		RequiredEditPaths:    append([]string(nil), brief.RequiredEditPaths...),
		AllowedEditPaths:     append([]string(nil), brief.AllowedEditPaths...),
		EvidencePaths:        append(append([]string(nil), brief.EvidencePaths...), sortedRunnerPathKeys(readPathsFromResults(results))...),
		ChangedPaths:         mapKeys(changedPathsFromToolResults(results)),
		UserBoundaries:       append([]string(nil), brief.UserBoundaries...),
		AllowedTools:         modelProtocolProjectionTools(phase.AllowedTools, phase.ActionPolicy.CallableToolNames),
		RequiredEvidence:     append([]string(nil), brief.RequiredEvidence...),
		RequiredTools:        modelProtocolRequiredTools(brief.RequiredTools, phase),
		ReadinessGate:        append([]string(nil), brief.ReadinessGate...),
		VerificationContract: strings.TrimSpace(brief.VerificationContract),
		VerificationPlan:     append([]string(nil), brief.VerificationPlan...),
		StopConditions:       append([]string(nil), brief.StopConditions...),
		FinalCritic:          append([]string(nil), brief.FinalCritic...),
		TargetPacket:         append([]string(nil), brief.TargetPacket...),
		Constraints:          append([]string(nil), brief.Constraints...),
		InvalidActions:       append([]string(nil), phase.InvalidNext...),
		Readiness:            modelProtocolReadinessInput(brief),
		Budget: contractcompile.BudgetInput{
			MaxToolSteps:       policy.MaxToolSteps,
			MaxModelCalls:      policy.MaxModelCalls,
			MaxContextMessages: policy.MaxContextMessages,
			MaxContextChars:    policy.MaxContextChars,
		},
		Runtime: contractcompile.RuntimeEnvelopeInput{
			BackendKind:         phase.RuntimeCall.BackendKind,
			ModelAlias:          phase.RuntimeCall.ModelAlias,
			ModelRole:           "planner",
			ParserTransportMode: modelProtocolParserTransportMode(phase),
			ToolTransportMode:   modelProtocolToolTransportMode(phase),
			ParserContract:      phase.RuntimeCall.ParserContract,
			NativeToolPolicy:    modelProtocolNativeToolPolicy(phase, supportsNativeTools),
			SupportsNativeTools: supportsNativeTools,
			ForceNativeTools:    strings.EqualFold(strings.TrimSpace(phase.RuntimeCall.ToolTransportMode), "native") && strings.EqualFold(strings.TrimSpace(phase.RuntimeCall.ToolChoice), "required"),
			ToolChoice:          modelProtocolToolChoice(phase.RuntimeCall.ToolChoice),
			ResponseFormat:      phase.RuntimeCall.ResponseFormat,
			ThinkingPolicy:      phase.RuntimeCall.ThinkingPolicy,
			EffectiveMaxTokens:  phase.RuntimeCall.MaxTokens,
			ContextBudget: contractcompile.ContextBudget{
				MaxContextMessages: policy.MaxContextMessages,
				MaxContextChars:    policy.MaxContextChars,
			},
			ReadinessProfile: "report_only",
			EnforcementMode:  "report_only",
		},
		Protocol: contractcompile.ProtocolInput{
			PhaseID:                phase.PhaseID,
			ParserMode:             phase.RuntimeCall.ParserContract,
			ExpectedVisibleOutput:  phase.RuntimeCall.ExpectedVisibleOutput,
			VisibleTools:           modelProtocolVisibleTools(phase),
			CallableTools:          modelProtocolProjectionTools(phase.ActionPolicy.CallableToolNames),
			CallableToolsSpecified: true,
			CallableActionClasses:  append([]string(nil), phase.ActionPolicy.AllowedActionClasses...),
			InvalidActionClasses:   append([]string(nil), phase.ActionPolicy.InvalidActionClasses...),
			TerminalResponseShapes: append([]string(nil), phase.ActionPolicy.TerminalResponseShapes...),
			StructuredSchemaNames:  modelProtocolStructuredSchemas(phase),
			MalformedOutputPolicy:  "retry_same_protocol_hash",
			StaleAnchorPolicy:      "bounded_source_refresh_before_submit_plan",
			SourceBodyObligations:  modelProtocolSourceBodyObligations(brief, results, phase),
			RecoveryBudget:         modelProtocolRecoveryBudget(phase),
		},
		MetadataTags: []string{"phase1_report_only", "model_protocol_projection"},
	}
	compiled, err := contractcompile.CompilePlanExecutePlanner(input)
	if err != nil {
		if r != nil {
			r.emitEvent(marshalRunEvent("model_protocol_projection_gap", map[string]any{
				"phase_id": phase.PhaseID,
				"reason":   "compile_failed",
				"error":    err.Error(),
			}))
		}
		return contractcompile.ModelProtocolProjection{}, false
	}
	return compiled.Protocol, true
}

func modelProtocolReadinessInput(brief workerruntime.WorkerRuntimeBrief) contractcompile.ReadinessInput {
	if brief.Readiness == nil {
		return contractcompile.ReadinessInput{}
	}
	return contractcompile.ReadinessInput{
		ReadyToAnswer:       brief.Readiness.ReadyToAnswer,
		ReadyToWrite:        brief.Readiness.ReadyToWrite,
		ReadyToVerify:       brief.Readiness.ReadyToVerify,
		ClosedClaims:        append([]string(nil), brief.Readiness.ClosedClaims...),
		OpenObligations:     append([]string(nil), brief.Readiness.OpenObligations...),
		EvidenceSources:     append([]string(nil), brief.Readiness.EvidenceSources...),
		FurtherReadsRequire: brief.Readiness.FurtherReadsRequire,
	}
}

func modelProtocolProjectionTools(groups ...[]string) []string {
	var tools []string
	for _, group := range groups {
		for _, value := range group {
			value = strings.TrimSpace(value)
			if value == "" || !modelProtocolKnownReadOnlyPlannerTool(value) {
				continue
			}
			tools = append(tools, value)
		}
	}
	return uniquePhaseStrings(tools)
}

func modelProtocolKnownReadOnlyPlannerTool(tool string) bool {
	switch strings.TrimSpace(tool) {
	case "context_pack", "context_query", "context_zoom", "context_status", "discovery_query",
		"search_files", "search_content", "read_file", "code_navigate",
		"product_efforts", "product_status", "recovery_inspect", "recovery_status", "submit_plan":
		return true
	default:
		return false
	}
}

func modelProtocolRequiredTools(required []string, phase TurnPhaseContract) []string {
	tools := modelProtocolProjectionTools(required)
	if containsPhaseString(phase.ActionPolicy.CallableToolNames, "submit_plan") {
		tools = append(tools, "submit_plan")
	}
	return uniquePhaseStrings(tools)
}

func modelProtocolVisibleTools(phase TurnPhaseContract) []string {
	tools := modelProtocolProjectionTools(phase.AllowedTools, phase.ActionPolicy.CallableToolNames)
	if containsPhaseString(phase.ActionPolicy.TerminalResponseShapes, "submit_plan") ||
		containsPhaseString(phase.ActionPolicy.InvalidActionClasses, actionSubmitPlan) {
		tools = append(tools, "submit_plan")
	}
	return uniquePhaseStrings(tools)
}

func modelProtocolStructuredSchemas(phase TurnPhaseContract) []string {
	if containsPhaseString(phase.ActionPolicy.TerminalResponseShapes, "submit_plan") ||
		containsPhaseString(phase.ActionPolicy.CallableToolNames, "submit_plan") {
		return []string{"submit_plan"}
	}
	return nil
}

func containsPhaseString(values []string, want string) bool {
	want = strings.TrimSpace(want)
	if want == "" {
		return false
	}
	for _, value := range values {
		if strings.TrimSpace(value) == want {
			return true
		}
	}
	return false
}

func modelProtocolSourceBodyObligations(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, phase TurnPhaseContract) []string {
	if phase.PhaseID != turnPhaseRequiredSource && phase.PhaseID != turnPhaseStaleAnchorRefresh {
		return nil
	}
	var paths []string
	for _, path := range brief.RequiredEditPaths {
		normalized := normalizeRunnerPath(path)
		if normalized == "" || concreteSourceEvidenceReadForPath(normalized, results) {
			continue
		}
		paths = append(paths, normalized)
	}
	return uniqueSortedRunnerPaths(paths)
}

func modelProtocolRecoveryBudget(phase TurnPhaseContract) int {
	switch phase.PhaseID {
	case turnPhaseMalformedSubmitPlanRecovery, turnPhaseStaleAnchorRefresh:
		return 1
	default:
		return 2
	}
}

func modelProtocolParserTransportMode(phase TurnPhaseContract) string {
	if strings.EqualFold(strings.TrimSpace(phase.RuntimeCall.ToolTransportMode), "native") {
		return "native_tools"
	}
	if strings.EqualFold(strings.TrimSpace(phase.RuntimeCall.ResponseFormat), "json") {
		return "textual_json_terminal"
	}
	return "textual_tools"
}

func modelProtocolToolTransportMode(phase TurnPhaseContract) string {
	if mode := strings.TrimSpace(phase.RuntimeCall.ToolTransportMode); mode != "" {
		return mode
	}
	return "textual"
}

func modelProtocolNativeToolPolicy(phase TurnPhaseContract, supportsNativeTools bool) string {
	if strings.EqualFold(strings.TrimSpace(phase.RuntimeCall.ToolTransportMode), "native") {
		return "native_required"
	}
	if supportsNativeTools {
		return "textual_unless_forced"
	}
	return "textual_only"
}

func modelProtocolToolChoice(toolChoice string) string {
	if strings.TrimSpace(toolChoice) == "" {
		return "none"
	}
	return strings.TrimSpace(toolChoice)
}

func modelProtocolEventPayload(protocol contractcompile.ModelProtocolProjection) autonomy.ModelProtocolEvent {
	return autonomy.ModelProtocolEvent{
		SchemaVersion:           modelProtocolProjectionSchemaVersion,
		ContractID:              protocol.ContractID,
		ContractVersion:         protocol.ContractVersion,
		ProtocolHash:            protocol.ProtocolHash,
		PhaseID:                 protocol.PhaseID,
		ParserMode:              protocol.ParserMode,
		ToolTransportMode:       protocol.ToolTransportMode,
		ParserTransportMode:     protocol.ParserTransportMode,
		ParserContract:          protocol.ParserContract,
		NativeToolPolicy:        protocol.NativeToolPolicy,
		SupportsNativeTools:     protocol.SupportsNativeTools,
		ForceNativeTools:        protocol.ForceNativeTools,
		ToolChoice:              protocol.ToolChoice,
		ResponseFormat:          protocol.ResponseFormat,
		ExpectedVisibleOutput:   protocol.ExpectedVisibleOutput,
		VisibleTools:            append([]string(nil), protocol.VisibleTools...),
		CallableTools:           append([]string(nil), protocol.CallableTools...),
		CallableActionClasses:   append([]string(nil), protocol.CallableActionClasses...),
		InvalidActionClasses:    append([]string(nil), protocol.InvalidActionClasses...),
		TerminalResponseShapes:  append([]string(nil), protocol.TerminalResponseShapes...),
		SourceBodyObligations:   append([]string(nil), protocol.SourceBodyObligations...),
		RequiredEditCoverage:    append([]string(nil), protocol.RequiredEditCoverage...),
		PathRoleSummary:         append([]string(nil), protocol.PathRoleSummary...),
		VerificationObligations: append([]string(nil), protocol.VerificationObligations...),
		SafeAlternatives:        append([]string(nil), protocol.SafeAlternatives...),
		MalformedOutputPolicy:   protocol.MalformedOutputPolicy,
		RecoveryBudget:          protocol.RecoveryBudget,
		ActiveInvariantID:       protocol.ActiveInvariantID,
	}
}

func modelMessagesWithProtocolProjection(messages []llama.ChatMessage, protocol contractcompile.ModelProtocolProjection) []llama.ChatMessage {
	card := strings.TrimSpace(modelProtocolProjectionCard(protocol))
	if card == "" {
		return append([]llama.ChatMessage(nil), messages...)
	}
	out := make([]llama.ChatMessage, 0, len(messages)+1)
	inserted := false
	for _, message := range messages {
		if !inserted && message.Role == "system" && strings.Contains(message.Content, "[frontier step instruction]") {
			out = append(out, llama.ChatMessage{Role: "system", Content: card})
			inserted = true
		}
		out = append(out, message)
	}
	if !inserted {
		out = append(out, llama.ChatMessage{Role: "system", Content: card})
	}
	return out
}

func modelProtocolProjectionCard(protocol contractcompile.ModelProtocolProjection) string {
	if strings.TrimSpace(protocol.ProtocolHash) == "" {
		return ""
	}
	terminalOutcomes := modelProtocolDisplayOrder(protocol.TerminalResponseShapes)
	if containsPhaseString(protocol.InvalidActionClasses, string(autonomy.ActionClassSubmitPlan)) {
		terminalOutcomes = filterPhaseStrings(terminalOutcomes, func(value string) bool {
			return strings.TrimSpace(value) != string(autonomy.ActionClassSubmitPlan)
		})
	}
	invalid := modelProtocolDisplayOrder(protocol.InvalidActionClasses)
	var b strings.Builder
	b.WriteString("[active contract protocol]\n")
	b.WriteString("Active contract: ")
	b.WriteString(strings.TrimSpace(protocol.ContractID))
	if strings.TrimSpace(protocol.ProtocolHash) != "" {
		b.WriteString(" / protocol_hash=")
		b.WriteString(strings.TrimSpace(protocol.ProtocolHash))
	}
	if strings.TrimSpace(protocol.PhaseID) != "" {
		b.WriteString("\nPhase: ")
		b.WriteString(strings.TrimSpace(protocol.PhaseID))
	}
	b.WriteString("\nVisible tools: ")
	b.WriteString(modelProtocolList(modelProtocolDisplayOrder(protocol.VisibleTools)))
	b.WriteString("\nCallable tools now: ")
	b.WriteString(modelProtocolList(modelProtocolDisplayOrder(protocol.CallableTools)))
	if len(terminalOutcomes) > 0 {
		b.WriteString("\nValid terminal outcomes: ")
		b.WriteString(strings.Join(terminalOutcomes, "; "))
	}
	if len(invalid) > 0 {
		b.WriteString("\nInvalid now: ")
		b.WriteString(strings.Join(invalid, ", "))
	}
	if len(protocol.RequiredEditCoverage) > 0 || len(protocol.SourceBodyObligations) > 0 || len(protocol.PathRoleSummary) > 0 {
		b.WriteString("\nPath roles: required_edit_paths=")
		b.WriteString(modelProtocolList(protocol.RequiredEditCoverage))
		b.WriteString("; source_body_obligations=")
		b.WriteString(modelProtocolList(protocol.SourceBodyObligations))
		b.WriteString("; roles=")
		b.WriteString(modelProtocolList(protocol.PathRoleSummary))
	}
	if verification := strings.Join(protocol.VerificationObligations, "; "); verification != "" {
		b.WriteString("\nVerification: ")
		b.WriteString(verification)
	}
	if alternatives := strings.Join(modelProtocolDisplayOrder(protocol.SafeAlternatives), "; "); alternatives != "" {
		b.WriteString("\nSafe alternatives: ")
		b.WriteString(alternatives)
	}
	return b.String()
}

func modelProtocolList(values []string) string {
	if len(values) == 0 {
		return "[]"
	}
	return "[" + strings.Join(values, ", ") + "]"
}

func modelProtocolDisplayOrder(values []string) []string {
	out := uniquePhaseStrings(values)
	order := map[string]int{
		string(autonomy.ActionClassPerEditWrite):    10,
		string(autonomy.ActionClassSearch):          20,
		string(autonomy.ActionClassSubmitPlan):      30,
		"Decision: source-satisfied":                40,
		"<<QUESTION>>:":                             50,
		string(autonomy.ActionClassBlocker):         60,
		string(autonomy.ActionClassReviewPass):      70,
		"Decision: pass":                            80,
		string(autonomy.ActionClassFinalAnswer):     90,
		string(autonomy.ActionClassProseFinal):      100,
		string(autonomy.ActionClassGenericProbe):    110,
		string(autonomy.ActionClassRead):            120,
		string(autonomy.ActionClassVerify):          130,
		string(autonomy.ActionClassSourceSatisfied): 140,
	}
	slices.SortFunc(out, func(a, b string) int {
		ao, aok := order[a]
		bo, bok := order[b]
		if aok && bok && ao != bo {
			return ao - bo
		}
		if aok != bok {
			if aok {
				return -1
			}
			return 1
		}
		return strings.Compare(a, b)
	})
	return out
}

func filterPhaseStrings(values []string, keep func(string) bool) []string {
	var out []string
	for _, value := range values {
		if keep == nil || keep(value) {
			out = append(out, value)
		}
	}
	return out
}
