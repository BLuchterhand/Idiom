package agent

import (
	"strings"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/autonomy"
	"idiom/internal/contracts/compile"
	"idiom/internal/platform/util"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/workerruntime"
)

const modelProtocolProjectionSchemaVersion = "idiom.model_protocol_projection.v1"

func (r *Runner) compileModelProtocolProjection(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, phase TurnPhaseContract, policy RunPolicy, supportsNativeTools bool) (contractcompile.ModelProtocolProjection, bool) {
	input, ok := runnerPlanExecutePlannerInput(brief, results, phase, policy, supportsNativeTools, strings.TrimSpace(phase.ContractID))
	if !ok {
		return contractcompile.ModelProtocolProjection{}, false
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

	return protocolWithActiveAuthority(compiled.Protocol, compiled.ActiveAuthority), true
}

func runnerPlanExecutePlannerInput(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, phase TurnPhaseContract, policy RunPolicy, supportsNativeTools bool, parentID string) (contractcompile.PlanExecutePlannerInput, bool) {
	brief = workerruntime.NormalizeRuntimeBrief(brief)
	if strings.TrimSpace(brief.TaskFamily) != PlanThenExecutePlannerTaskFamily {
		return contractcompile.PlanExecutePlannerInput{}, false
	}

	phaseSpecified := runnerPlanExecutePlannerPhaseSpecified(phase)
	visibleTools := modelProtocolVisibleTools(phase)
	callableTools := modelProtocolProjectionTools(phase.ActionPolicy.CallableToolNames)
	allowedTools := modelProtocolProjectionTools(phase.AllowedTools, phase.ActionPolicy.CallableToolNames)

	if phaseSpecified && (len(visibleTools) == 0 || len(callableTools) == 0) {
		state := DerivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{
			TextualToolParser: util.EqualTrimFold(phase.RuntimeCall.ToolTransportMode, "textual") ||
				util.EqualTrimFold(phase.RuntimeCall.ParserContract, "source_refresh"),
			SupportsNativeTools: util.EqualTrimFold(phase.RuntimeCall.ToolTransportMode, "native"),
			ForceNativeTools:    util.EqualTrimFold(phase.RuntimeCall.ToolTransportMode, "native") && util.EqualTrimFold(phase.RuntimeCall.ToolChoice, "required"),
			ResponseFormatJSON:  util.EqualTrimFold(phase.RuntimeCall.ResponseFormat, "json"),
			EffectiveMaxTokens:  phase.RuntimeCall.MaxTokens,
		})
		if len(visibleTools) == 0 {
			visibleTools = modelProtocolProjectionTools(state.ParserVisibleTools)
		}

		if len(callableTools) == 0 {
			callableTools = modelProtocolProjectionTools(state.CallableTools)
		}

		if len(allowedTools) == 0 {
			allowedTools = modelProtocolProjectionTools(visibleTools, callableTools)
		}
	}

	if !phaseSpecified && len(allowedTools) == 0 {
		allowedTools = modelProtocolProjectionTools(brief.AllowedTools)
	}

	if !phaseSpecified && len(visibleTools) == 0 {
		visibleTools = append([]string(nil), allowedTools...)
	}

	if !phaseSpecified && len(callableTools) == 0 {
		callableTools = append([]string(nil), allowedTools...)
	}

	callableActionClasses := actionClassesAllowedByPolicy(phase.ActionPolicy.AllowedActionClasses, phase.ActionPolicy.InvalidActionClasses)
	invalidActionClasses := uniquePhaseStrings(phase.ActionPolicy.InvalidActionClasses)

	return contractcompile.PlanExecutePlannerInput{
		ParentID:             strings.TrimSpace(parentID),
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
		AllowedTools:         allowedTools,
		ForbiddenTools:       modelProtocolKnownPlannerTools(brief.ForbiddenTools),
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
			ForceNativeTools:    util.EqualTrimFold(phase.RuntimeCall.ToolTransportMode, "native") && util.EqualTrimFold(phase.RuntimeCall.ToolChoice, "required"),
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
			PhaseID:                  phase.PhaseID,
			ParserMode:               phase.RuntimeCall.ParserContract,
			ExpectedVisibleOutput:    phase.RuntimeCall.ExpectedVisibleOutput,
			VisibleTools:             visibleTools,
			CallableTools:            callableTools,
			CallableToolsSpecified:   true,
			CallableActionClasses:    callableActionClasses,
			InvalidActionClasses:     invalidActionClasses,
			TerminalResponseShapes:   append([]string(nil), phase.ActionPolicy.TerminalResponseShapes...),
			StructuredSchemaNames:    modelProtocolStructuredSchemas(phase),
			MalformedOutputPolicy:    "retry_same_protocol_hash",
			StaleAnchorPolicy:        "bounded_source_refresh_before_submit_plan",
			SourceBodyObligations:    modelProtocolSourceBodyObligations(brief, results, phase),
			CompletedGitHistoryPaths: modelProtocolCompletedGitHistoryPaths(brief, results),
			PendingGitHistoryPaths:   modelProtocolPendingGitHistoryPaths(brief, results),
			RecoveryBudget:           modelProtocolRecoveryBudget(phase),
		},
		MetadataTags: []string{"phase1_report_only", "model_protocol_projection"},
	}, true
}

func runnerPlanExecutePlannerPhaseSpecified(phase TurnPhaseContract) bool {
	return strings.TrimSpace(phase.PhaseID) != "" ||
		strings.TrimSpace(phase.Phase) != "" ||
		strings.TrimSpace(phase.RuntimeCall.ParserContract) != "" ||
		len(phase.AllowedTools) > 0 ||
		len(phase.ActionPolicy.CallableToolNames) > 0 ||
		len(phase.ActionPolicy.AllowedActionClasses) > 0 ||
		len(phase.ActionPolicy.InvalidActionClasses) > 0 ||
		len(phase.ActionPolicy.TerminalResponseShapes) > 0
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
	return contractcompile.IsPlanExecutePlannerToolAllowed(tool)
}

func modelProtocolKnownPlannerTools(values []string) []string {
	known := make(map[string]struct{})
	for _, entry := range contractcompile.PlanExecutePlannerToolVocabulary() {
		known[strings.TrimSpace(entry.Tool)] = struct{}{}
	}

	var tools []string

	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}

		if _, ok := known[value]; !ok {
			continue
		}

		tools = append(tools, value)
	}

	return uniquePhaseStrings(tools)
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
		containsPhaseString(phase.ActionPolicy.CallableToolNames, "submit_plan") {

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
		if normalized == "" {
			continue
		}

		state := planThenExecuteRequiredPathState(brief, results, normalized)
		switch phase.PhaseID {
		case turnPhaseRequiredSource:
			if state.requiresSourceBodyRead() {
				paths = append(paths, normalized)
			}
		case turnPhaseStaleAnchorRefresh:
			if state == planThenExecuteRequiredPathStaleAnchor {
				paths = append(paths, normalized)
			}
		}
	}

	return uniqueSortedRunnerPaths(paths)
}

func modelProtocolCompletedGitHistoryPaths(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) []string {
	if !mutationHistoryEvidenceRequired(brief) {
		return nil
	}

	var paths []string
	for _, path := range planThenExecuteRequiredCoveragePaths(brief.RequiredEditPaths, "") {
		if successfulGitHistoryResultForPath(path, results) {
			paths = append(paths, path)
		}
	}

	return uniqueSortedRunnerPaths(paths)
}

func modelProtocolPendingGitHistoryPaths(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) []string {
	if !mutationHistoryEvidenceRequired(brief) {
		return nil
	}

	return planThenExecuteHistoryPendingPaths(brief, results, planThenExecuteRequiredCoveragePaths(brief.RequiredEditPaths, ""))
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
	if util.EqualTrimFold(phase.RuntimeCall.ToolTransportMode, "native") {
		return "native_tools"
	}

	if util.EqualTrimFold(phase.RuntimeCall.ResponseFormat, "json") {
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
	if util.EqualTrimFold(phase.RuntimeCall.ToolTransportMode, "native") {
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
		SchemaVersion:            modelProtocolProjectionSchemaVersion,
		ContractID:               protocol.ContractID,
		ContractVersion:          protocol.ContractVersion,
		ProtocolHash:             protocol.ProtocolHash,
		PhaseID:                  protocol.PhaseID,
		ParserMode:               protocol.ParserMode,
		ToolTransportMode:        protocol.ToolTransportMode,
		ParserTransportMode:      protocol.ParserTransportMode,
		ParserContract:           protocol.ParserContract,
		NativeToolPolicy:         protocol.NativeToolPolicy,
		SupportsNativeTools:      protocol.SupportsNativeTools,
		ForceNativeTools:         protocol.ForceNativeTools,
		ToolChoice:               protocol.ToolChoice,
		ResponseFormat:           protocol.ResponseFormat,
		ExpectedVisibleOutput:    protocol.ExpectedVisibleOutput,
		VisibleTools:             append([]string(nil), protocol.VisibleTools...),
		CallableTools:            append([]string(nil), protocol.CallableTools...),
		CallableActionClasses:    append([]string(nil), protocol.CallableActionClasses...),
		InvalidActionClasses:     append([]string(nil), protocol.InvalidActionClasses...),
		TerminalResponseShapes:   append([]string(nil), protocol.TerminalResponseShapes...),
		SourceBodyObligations:    append([]string(nil), protocol.SourceBodyObligations...),
		CompletedGitHistoryPaths: append([]string(nil), protocol.CompletedGitHistoryPaths...),
		PendingGitHistoryPaths:   append([]string(nil), protocol.PendingGitHistoryPaths...),
		RequiredEditCoverage:     append([]string(nil), protocol.RequiredEditCoverage...),
		PathRoleSummary:          append([]string(nil), protocol.PathRoleSummary...),
		VerificationObligations:  append([]string(nil), protocol.VerificationObligations...),
		SafeAlternatives:         append([]string(nil), protocol.SafeAlternatives...),
		MalformedOutputPolicy:    protocol.MalformedOutputPolicy,
		RecoveryBudget:           protocol.RecoveryBudget,
		ActiveInvariantID:        protocol.ActiveInvariantID,
	}
}

func modelMessagesWithProtocolProjection(messages []llama.ChatMessage, protocol contractcompile.ModelProtocolProjection) []llama.ChatMessage {
	card := strings.TrimSpace(workerruntime.RenderActiveProtocolCard(protocol))
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
