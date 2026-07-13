package agent

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/autonomy"
	contractcompile "idiom/internal/contracts/compile"
	lanecontract "idiom/internal/contracts/lane"
	"idiom/internal/contracts/pathparse"
	plancontract "idiom/internal/contracts/plan"
	recoverycontract "idiom/internal/contracts/recovery"
	"idiom/internal/contracts/toolfacts"
	"idiom/internal/platform/textutil"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/workerruntime"
	"idiom/internal/workflows/planintent"
)

func (r *runExecution) completionGatePipeline(step int, taskLedger *taskLedgerState, declarationMovesForbidden bool) *completionGatePipeline {
	if r != nil {
		return newCompletionGatePipeline(r.completionGateChecks(step, taskLedger, declarationMovesForbidden)...)
	}

	return newCompletionGatePipeline(
		typedCompletionGateCheck("completion_gate.protocol.textual_tool_call", "textual_tool_call", completionGateViolationContract, textualToolCallGateCheck),
		typedCompletionGateCheck("completion_gate.protocol.unexecuted_tool_request", "unexecuted_tool_request", completionGateViolationContract, unexecutedToolRequestGateCheck),
		typedCompletionGateCheck("completion_gate.answer.raw_tool_transcript", "raw_tool_transcript", completionGateViolationContract, rawToolResultTranscriptGateCheck),
		typedCompletionGateCheck("completion_gate.answer.unresolved_process_intent", "unresolved_process_intent", completionGateViolationContract, unresolvedProcessIntentGateCheck),
	)
}

func (r *runExecution) completionGateChecks(_ int, taskLedger *taskLedgerState, _ bool) []completionGateCheck {
	return []completionGateCheck{
		typedCompletionGateCheck("completion_gate.protocol.textual_tool_call", "textual_tool_call", completionGateViolationContract, func(reply string, results []workspace.ToolResult) (workspace.ToolResult, bool) {
			return textualToolCallCompletionRejection(r, reply, results)
		}),
		typedCompletionGateCheck("completion_gate.protocol.unexecuted_tool_request", "unexecuted_tool_request", completionGateViolationContract, func(reply string, _ []workspace.ToolResult) (workspace.ToolResult, bool) {
			return r.unexecutedToolRequestCompletionRejection(reply)
		}),
		typedCompletionGateCheck("completion_gate.answer.raw_tool_transcript", "raw_tool_transcript", completionGateViolationContract, rawToolResultTranscriptGateCheck),
		typedCompletionGateCheck("completion_gate.answer.unresolved_process_intent", "unresolved_process_intent", completionGateViolationContract, unresolvedProcessIntentGateCheck),
		typedCompletionGateCheck("completion_gate.task_ledger", "task_ledger_incomplete", completionGateViolationContract, func(reply string, _ []workspace.ToolResult) (workspace.ToolResult, bool) {
			if taskLedger == nil {
				return workspace.ToolResult{}, false
			}

			return taskLedger.completionRejection(reply, r.state.runtimeBrief)
		}),
		typedCompletionGateCheck("completion_gate.answer.read_only_substantive", "non_substantive_answer", completionGateViolationContract, func(reply string, results []workspace.ToolResult) (workspace.ToolResult, bool) {
			return r.readOnlyNonSubstantiveCompletionRejection(reply, results)
		}),
		typedCompletionGateCheck("completion_gate.answer.read_only_explanation", "explanation_contract", completionGateViolationContract, func(reply string, results []workspace.ToolResult) (workspace.ToolResult, bool) {
			return r.readOnlyExplanationCompletionRejection(reply, results)
		}),
		typedCompletionGateCheck("completion_gate.answer.search_inventory", "search_inventory", completionGateViolationContract, func(reply string, results []workspace.ToolResult) (workspace.ToolResult, bool) {
			return r.readOnlySearchHitInventoryCompletionRejection(reply, results)
		}),
		typedCompletionGateCheck(completionGateInvariantMutationWrite, "mutation_contract", completionGateViolationMutation, func(reply string, results []workspace.ToolResult) (workspace.ToolResult, bool) {
			return r.mutationCompletionRejection(reply, results)
		}),
		typedCompletionGateCheck(completionGateInvariantPlanSubmission, "review_repair_closeout", completionGateViolationPlanSubmission, func(reply string, results []workspace.ToolResult) (workspace.ToolResult, bool) {
			return r.planThenExecuteReviewRepairCompletionRejection(reply, results)
		}),
		typedCompletionGateCheck(completionGateInvariantFinalAnswerAcceptance, "final_answer_acceptance", completionGateViolationContract, func(reply string, results []workspace.ToolResult) (workspace.ToolResult, bool) {
			return r.finalAnswerAcceptanceCompletionRejection(reply, results)
		}),
	}
}

var textualToolCallGateCheck completionGateCheck = func(reply string, _ []workspace.ToolResult) (workspace.ToolResult, bool) {
	return textualToolCallCompletionRejection(nil, reply, nil)
}

func textualToolCallCompletionRejection(r *runExecution, reply string, results []workspace.ToolResult) (workspace.ToolResult, bool) {
	if tool, transport, ok := unsafeTextualWriteAttempt(reply); ok && r.unsafeTextualWriteRecoverySurfaceActive(results) {
		msg := "the reply contains a " + transport + " " + tool + " write attempt that could not be converted into a safe write request. Emit the write through the native tool-call mechanism, as one visible JSON object with top-level `tool`, `path`, `old_text`, `new_text`, and `description` fields copied from source evidence, or as Qwen key-value `tool:` syntax with those same fields. Function-call write syntax is not executable."
		if r != nil && r.planThenExecutePlannerPhase() {
			msg = "the reply contains a " + transport + " " + tool + " write attempt, but the plan-execute planner phase does not execute per-edit writes. Emit one visible JSON object for `submit_plan` with top-level `tool`, `rationale`, and `edits`. Function-call write syntax is not executable."
		}

		return workspace.ToolResult{
			Tool:    completionGateToolName,
			Summary: "Textual write syntax rejected as final answer",
			Error:   msg,
		}, true
	}

	if !IsTextualToolCall(reply) {
		return workspace.ToolResult{}, false
	}

	msg := "the reply contains textual tool-call markup instead of prose. Emit a tool call through the normal tool-call mechanism, or answer in plain prose."
	if workspace.LooksLikeXMLToolCallMarkup(reply) {
		msg = "the reply contains XML tool-call markup (e.g. <n_tool_calls>, <invoke>, <janus_tool>) instead of prose. Emit a tool call through the normal tool-call mechanism, or answer in plain prose. Do not emit XML or HTML markup."
	}

	return workspace.ToolResult{
		Tool:    completionGateToolName,
		Summary: "Textual tool-call markup rejected as final answer",
		Error:   msg,
	}, true
}

func (r *runExecution) unsafeTextualWriteRecoverySurfaceActive(results []workspace.ToolResult) bool {
	if r == nil || !runnerBriefRequiresMutation(r.state.runtimeBrief) {
		return false
	}

	if len(r.completionChangedPaths(results)) > 0 {
		return false
	}

	if r.planThenExecutePlannerPhase() {
		return len(newToolResultEvidenceProjection(results).SourceWindowPaths()) > 0
	}

	return implementationTargetWriteReadySurfaceActive(r.state.runtimeBrief, results, len(r.completionChangedPaths(results)) > 0)
}

var unexecutedToolRequestGateCheck completionGateCheck = func(reply string, _ []workspace.ToolResult) (workspace.ToolResult, bool) {
	if !looksLikeUnexecutedToolRequestAnswer(reply) {
		return workspace.ToolResult{}, false
	}

	return workspace.ToolResult{
		Tool:    completionGateToolName,
		Summary: "Unexecuted tool request in final answer",
		Error:   "the reply contains what looks like an attempt to call a tool inside prose. Emit a tool call through the normal tool-call mechanism, or answer in plain prose. Do not emit tool JSON, XML, or CLI syntax inside a final answer.",
	}, true
}

var rawToolResultTranscriptGateCheck completionGateCheck = func(reply string, _ []workspace.ToolResult) (workspace.ToolResult, bool) {
	return rawToolResultTranscriptCompletionRejection(reply)
}

var unresolvedProcessIntentGateCheck completionGateCheck = func(reply string, _ []workspace.ToolResult) (workspace.ToolResult, bool) {
	return unresolvedProcessIntentCompletionRejection(reply)
}

func (r *runExecution) compileModelProtocolProjection(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, phase TurnPhaseContract, policy RunPolicy, supportsNativeTools bool) (contractcompile.ModelProtocolProjection, bool) {
	var (
		protocol contractcompile.ModelProtocolProjection
		err      error
	)

	inspection := r.permissionAuthorizedInspection(textutil.Merge(
		r.workspaceAdvertisedAllowedTools(),
		brief.AllowedTools,
		phase.AllowedTools,
		phase.ActionPolicy.CallableToolNames,
	))
	if input, ok := runnerPlanExecutePlannerInput(brief, results, phase, policy, supportsNativeTools, strings.TrimSpace(phase.ContractID), inspection, r.planThenExecutePathAuthority(), r.completionChangedPaths(results)); ok {
		var compiled contractcompile.CompileResult
		compiled, err = contractcompile.CompilePlanExecutePlanner(input)
		protocol = protocolWithActiveAuthority(compiled.Protocol, compiled.ActiveAuthority)
	} else {
		input, ok := r.runnerReadOnlyModelProtocolInput(brief, results, phase, policy, supportsNativeTools)
		if !ok {
			return contractcompile.ModelProtocolProjection{}, false
		}

		protocol, err = contractcompile.CompileModelProtocol(input)
	}

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

	return protocol, true
}

func (r *runExecution) runnerReadOnlyModelProtocolInput(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, phase TurnPhaseContract, policy RunPolicy, supportsNativeTools bool) (contractcompile.ModelProtocolInput, bool) {
	brief = workerruntime.NormalizeRuntimeBrief(brief)
	if !workerruntime.PermissionProfileIsReadOnlyWorkspace(brief.PermissionProfile) {
		return contractcompile.ModelProtocolInput{}, false
	}
	// Autonomy event recording is optional, so a read-only model call may not
	// have a lane-level contract ID. Its typed model-call ID still gives the
	// per-call protocol a stable identity without making visibility depend on
	// telemetry being enabled.
	contractID := textutil.FirstNonEmpty(strings.TrimSpace(phase.ContractID), strings.TrimSpace(phase.ModelCallID))
	if contractID == "" {
		return contractcompile.ModelProtocolInput{}, false
	}

	visibleTools := textutil.Unique(append(append([]string(nil), phase.AllowedTools...), phase.ActionPolicy.CallableToolNames...))
	callableTools := textutil.Unique(phase.ActionPolicy.CallableToolNames)
	callableActionClasses := actionClassesAllowedByPolicy(phase.ActionPolicy.AllowedActionClasses, phase.ActionPolicy.InvalidActionClasses)
	projection := newToolResultEvidenceProjection(results)
	allGroundedPaths := modelProtocolGroundedEvidencePaths(projection)
	allCandidateOnlyPaths := modelProtocolCandidateOnlyEvidencePaths(projection, allGroundedPaths)
	allSourceBodyObligations := readOnlyModelProtocolSourceBodyObligations(r, phase, allGroundedPaths)
	groundedPaths := sampleModelProtocolEvidencePaths(allGroundedPaths, modelProtocolEvidencePathLimit)
	candidateOnlyPaths := sampleModelProtocolEvidencePaths(allCandidateOnlyPaths, modelProtocolEvidencePathLimit)
	sourceBodyObligations := sampleModelProtocolEvidencePaths(allSourceBodyObligations, modelProtocolEvidencePathLimit)

	pathRefs := append([]string(nil), groundedPaths.Paths...)
	pathRefs = append(pathRefs, candidateOnlyPaths.Paths...)
	pathRefs = append(pathRefs, sourceBodyObligations.Paths...)

	return contractcompile.ModelProtocolInput{
		ContractID:      contractID,
		ContractVersion: autonomy.SchemaVersion,
		Runtime: contractcompile.RuntimeEnvelopeInput{
			BackendKind:         phase.RuntimeCall.BackendKind,
			ModelAlias:          phase.RuntimeCall.ModelAlias,
			ModelRole:           textutil.FirstNonEmpty(strings.TrimSpace(phase.TypedRoute.ActorRole), "answerer"),
			ParserTransportMode: modelProtocolParserTransportMode(phase),
			ToolTransportMode:   modelProtocolToolTransportMode(phase),
			ParserContract:      phase.RuntimeCall.ParserContract,
			NativeToolPolicy:    modelProtocolNativeToolPolicy(phase, supportsNativeTools),
			SupportsNativeTools: supportsNativeTools,
			ForceNativeTools:    textutil.EqualTrimFold(phase.RuntimeCall.ToolTransportMode, "native") && textutil.EqualTrimFold(phase.RuntimeCall.ToolChoice, "required"),
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
			PhaseID:                       string(phase.PhaseID),
			ParserMode:                    phase.RuntimeCall.ParserContract,
			ExpectedVisibleOutput:         phase.RuntimeCall.ExpectedVisibleOutput,
			VisibleTools:                  visibleTools,
			HiddenTools:                   append([]string(nil), phase.HiddenTools...),
			CallableTools:                 callableTools,
			CallableToolsSpecified:        true,
			CallableActionClasses:         callableActionClasses,
			InvalidActionClasses:          textutil.Unique(phase.ActionPolicy.InvalidActionClasses),
			TerminalResponseShapes:        append([]string(nil), phase.ActionPolicy.TerminalResponseShapes...),
			StructuredSchemaNames:         modelProtocolStructuredSchemas(phase),
			MalformedOutputPolicy:         "retry_same_protocol_hash",
			SourceBodyObligations:         sourceBodyObligations.Paths,
			SourceBodyObligationTotal:     sourceBodyObligations.Total,
			SourceBodyObligationOmitted:   sourceBodyObligations.Omitted,
			GroundedSourceBodyPaths:       groundedPaths.Paths,
			GroundedSourceBodyPathTotal:   groundedPaths.Total,
			GroundedSourceBodyPathOmitted: groundedPaths.Omitted,
			CandidateOnlyPaths:            candidateOnlyPaths.Paths,
			CandidateOnlyPathTotal:        candidateOnlyPaths.Total,
			CandidateOnlyPathOmitted:      candidateOnlyPaths.Omitted,
		},
		RequiredEditCoverage:    append([]string(nil), phase.TypedRoute.RequiredCoveragePaths...),
		VerificationObligations: append([]string(nil), phase.EvidenceState.VerificationEvidenceRefs...),
		RequiredTools:           modelProtocolVisibleRequiredTools(brief.RequiredTools, visibleTools),
		ReadinessGate:           append([]string(nil), brief.ReadinessGate...),
		Constraints:             append([]string(nil), phase.Constraints...),
		InvalidActions:          append([]string(nil), phase.InvalidNext...),
		ContextPacket: contractcompile.ModelProtocolContextPacketInput{
			TargetRefs:   append([]string(nil), brief.TargetRefs...),
			PathRefs:     pathRefs,
			EvidenceRefs: append([]string(nil), phase.EvidenceState.AcceptanceRefs...),
		},
	}, true
}

const modelProtocolEvidencePathLimit = 12

type modelProtocolEvidencePathSample struct {
	Paths   []string
	Total   int
	Omitted int
}

func modelProtocolGroundedEvidencePaths(projection toolResultEvidenceProjection) []string {
	paths := append([]string(nil), projection.SourceWindowPaths()...)
	for _, observation := range projection.ReadOnlyEvidenceObservations() {
		if observation.Kind == readOnlyEvidenceSourceWindow || observation.Kind == readOnlyEvidenceExternalDocument {
			paths = append(paths, observation.Path)
		}
	}
	for _, fact := range projection.ReadOnlyDelegatedEvidenceFacts() {
		paths = append(paths, fact.SourceBodyPaths...)
	}

	return normalizeModelProtocolEvidencePaths(paths)
}

func modelProtocolCandidateOnlyEvidencePaths(projection toolResultEvidenceProjection, groundedPaths []string) []string {
	grounded := modelProtocolEvidencePathSet(groundedPaths)
	var candidates []string
	for _, path := range normalizeModelProtocolEvidencePaths(projection.CandidatePaths()) {
		if _, ok := grounded[path]; !ok {
			candidates = append(candidates, path)
		}
	}

	return candidates
}

func sampleModelProtocolEvidencePaths(paths []string, limit int) modelProtocolEvidencePathSample {
	paths = normalizeModelProtocolEvidencePaths(paths)
	total := len(paths)
	if limit > 0 && len(paths) > limit {
		paths = append([]string(nil), paths[:limit]...)
	} else {
		paths = append([]string(nil), paths...)
	}

	return modelProtocolEvidencePathSample{
		Paths:   paths,
		Total:   total,
		Omitted: total - len(paths),
	}
}

func normalizeModelProtocolEvidencePaths(paths []string) []string {
	seen := make(map[string]struct{}, len(paths))
	normalized := make([]string, 0, len(paths))
	for _, raw := range paths {
		path := normalizeModelProtocolEvidencePath(raw)
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}

		seen[path] = struct{}{}
		normalized = append(normalized, path)
	}
	slices.Sort(normalized)

	return normalized
}

func normalizeModelProtocolEvidencePath(path string) string {
	if external := normalizeReadOnlyExternalDocumentPath(path); external != "" {
		if !readOnlyExternalDocumentBodyPath(external) {
			return ""
		}
		return external
	}

	path = NormalizeRunnerPath(path)
	if path == "" || IsNonSourceEvidenceArtifactPath(path) {
		return ""
	}
	path = filepath.ToSlash(filepath.Clean(path))
	if path == "." {
		return ""
	}

	return strings.TrimPrefix(path, "./")
}

func modelProtocolEvidencePathSet(paths []string) map[string]struct{} {
	set := make(map[string]struct{}, len(paths))
	for _, path := range normalizeModelProtocolEvidencePaths(paths) {
		set[path] = struct{}{}
	}
	return set
}

func readOnlyModelProtocolSourceBodyObligations(r *runExecution, phase TurnPhaseContract, groundedPaths []string) []string {
	obligations := append([]string(nil), phase.EvidenceState.SourceBodyObligations...)
	if r != nil && r.state.lastCompletionGateSourceGrounding.hasTypedSignal() {
		obligations = append(obligations, r.state.lastCompletionGateSourceGrounding.ReadableRecoveryPaths()...)
	}

	grounded := modelProtocolEvidencePathSet(groundedPaths)

	var pending []string
	for _, path := range normalizeModelProtocolEvidencePaths(obligations) {
		if _, ok := grounded[path]; !ok {
			pending = append(pending, path)
		}
	}

	return pending
}

func modelProtocolVisibleRequiredTools(required []string, visibleTools []string) []string {
	visible := make(map[string]struct{}, len(visibleTools))
	for _, tool := range visibleTools {
		visible[strings.TrimSpace(tool)] = struct{}{}
	}

	var tools []string
	for _, requirement := range required {
		for _, tool := range workspace.ConcreteRequiredTools(requirement) {
			if _, ok := visible[strings.TrimSpace(tool)]; ok {
				tools = append(tools, tool)
			}
		}
	}

	return textutil.Unique(tools)
}

func runnerPlanExecutePlannerInput(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, phase TurnPhaseContract, policy RunPolicy, supportsNativeTools bool, parentID string, inspection workspace.PermissionAuthorizedInspectionProjection, authority planThenExecutePathAuthority, changedPaths []string) (contractcompile.PlanExecutePlannerInput, bool) {
	brief = workerruntime.NormalizeRuntimeBrief(brief)
	if strings.TrimSpace(brief.TaskFamily) != PlanThenExecutePlannerTaskFamily {
		return contractcompile.PlanExecutePlannerInput{}, false
	}

	phaseSpecified := runnerPlanExecutePlannerPhaseSpecified(phase)
	inspectionSurface := modelProtocolPlannerInspectionSurface(inspection)
	visibleTools := textutil.Merge(modelProtocolVisibleTools(phase), inspectionSurface.Tools)
	callableTools := textutil.Merge(phase.ActionPolicy.CallableToolNames, inspectionSurface.Tools)
	allowedTools := textutil.Merge(phase.AllowedTools, callableTools)

	if phaseSpecified && (len(visibleTools) == 0 || len(callableTools) == 0) {
		state := derivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{
			TextualToolParser: textutil.EqualTrimFold(phase.RuntimeCall.ToolTransportMode, "textual") ||
				textutil.EqualTrimFold(phase.RuntimeCall.ParserContract, "source_refresh"),
			SupportsNativeTools: textutil.EqualTrimFold(phase.RuntimeCall.ToolTransportMode, "native"),
			ForceNativeTools:    textutil.EqualTrimFold(phase.RuntimeCall.ToolTransportMode, "native") && textutil.EqualTrimFold(phase.RuntimeCall.ToolChoice, "required"),
			ResponseFormatJSON:  textutil.EqualTrimFold(phase.RuntimeCall.ResponseFormat, "json"),
		}, authority)
		if len(visibleTools) == 0 {
			visibleTools = textutil.Merge(state.ParserVisibleTools, inspectionSurface.Tools)
		}

		if len(callableTools) == 0 {
			callableTools = textutil.Merge(state.CallableTools, inspectionSurface.Tools)
		}

		if len(allowedTools) == 0 {
			allowedTools = textutil.Merge(visibleTools, callableTools)
		}
	}

	if !phaseSpecified {
		allowedTools = append([]string(nil), inspectionSurface.Tools...)
		if slices.Contains(brief.AllowedTools, workspace.ToolNameSubmitPlan) {
			allowedTools = append(allowedTools, workspace.ToolNameSubmitPlan)
		}
		visibleTools = append([]string(nil), allowedTools...)
		callableTools = append([]string(nil), allowedTools...)
	}

	callableActionClasses := actionClassesAllowedByPolicy(phase.ActionPolicy.AllowedActionClasses, phase.ActionPolicy.InvalidActionClasses)
	invalidActionClasses := textutil.Unique(phase.ActionPolicy.InvalidActionClasses)

	return contractcompile.PlanExecutePlannerInput{
		ParentID:             strings.TrimSpace(parentID),
		LaneRunID:            time.Now().UTC().Format(time.RFC3339Nano),
		Lane:                 textutil.FirstNonEmpty(strings.TrimSpace(brief.Lane), contractcompile.DefaultPlanExecuteLane),
		Phase:                contractcompile.DefaultPlannerPhase,
		TaskFamily:           contractcompile.InvocationPlanExecutePlanner,
		RuntimeContract:      strings.TrimSpace(brief.Contract),
		PermissionProfile:    strings.TrimSpace(brief.PermissionProfile),
		TargetRefs:           append([]string(nil), brief.TargetRefs...),
		RequiredEditPaths:    append([]string(nil), brief.RequiredEditPaths...),
		AllowedEditPaths:     append([]string(nil), brief.AllowedEditPaths...),
		EvidencePaths:        append(append([]string(nil), brief.EvidencePaths...), sortedRunnerPathKeys(newToolResultEvidenceProjection(results).ReadPaths())...),
		ChangedPaths:         uniqueSortedRunnerPaths(changedPaths),
		UserBoundaries:       append([]string(nil), brief.UserBoundaries...),
		AllowedTools:         allowedTools,
		ForbiddenTools:       workspace.PlanExecuteKnownPlannerToolNames(brief.ForbiddenTools),
		RequiredEvidence:     append([]string(nil), brief.RequiredEvidence...),
		RequiredTools:        modelProtocolRequiredTools(brief.RequiredTools, phase, visibleTools),
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
			MaxToolSteps:             policy.MaxToolSteps,
			MaxModelCalls:            policy.MaxModelCalls,
			MaxOperationalModelCalls: policy.MaxOperationalModelCalls,
			MaxContextMessages:       policy.MaxContextMessages,
			MaxContextChars:          policy.MaxContextChars,
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
			ForceNativeTools:    textutil.EqualTrimFold(phase.RuntimeCall.ToolTransportMode, "native") && textutil.EqualTrimFold(phase.RuntimeCall.ToolChoice, "required"),
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
			PhaseID:                  string(phase.PhaseID),
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
			SourceBodyObligations:    modelProtocolSourceBodyObligations(brief, results, phase, authority),
			CompletedGitHistoryPaths: modelProtocolCompletedGitHistoryPaths(brief, results),
			PendingGitHistoryPaths:   modelProtocolPendingGitHistoryPaths(brief, results, authority),
		},
		ToolMetadata: inspectionSurface.Metadata,
		MetadataTags: []string{"phase1_report_only", "model_protocol_projection"},
	}, true
}

func runnerPlanExecutePlannerPhaseSpecified(phase TurnPhaseContract) bool {
	return strings.TrimSpace(string(phase.PhaseID)) != "" ||
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

type modelProtocolPlannerInspection struct {
	Tools    []string
	Metadata []contractcompile.PlanExecutePlannerToolMetadata
}

// modelProtocolPlannerInspectionSurface translates the workspace-owned
// permission projection into compiler vocabulary. Built-in planner metadata is
// preserved verbatim. Only projected names without built-in metadata receive
// the generic external read-only discovery contract.
func modelProtocolPlannerInspectionSurface(inspection workspace.PermissionAuthorizedInspectionProjection) modelProtocolPlannerInspection {
	entries := workspace.PlanExecutePlannerToolMetadataEntries()
	metadata := make([]contractcompile.PlanExecutePlannerToolMetadata, 0, len(entries))
	known := make(map[string]workspace.PlanExecutePlannerToolMetadata, len(entries))
	for _, entry := range entries {
		known[entry.Tool] = entry
		metadata = append(metadata, contractcompile.PlanExecutePlannerToolMetadata{
			Tool:           entry.Tool,
			Capabilities:   append([]autonomy.Capability(nil), entry.Capabilities...),
			SideEffect:     entry.SideEffect,
			PlannerAllowed: entry.PlannerAllowed,
		})
	}

	var tools []string
	for _, tool := range inspection.ToolNames() {
		if entry, ok := known[tool]; ok {
			if entry.PlannerAllowed {
				tools = append(tools, tool)
			}
			continue
		}

		tools = append(tools, tool)
		metadata = append(metadata, contractcompile.PlanExecutePlannerToolMetadata{
			Tool:           tool,
			Capabilities:   []autonomy.Capability{autonomy.CapabilitySemanticDiscovery},
			SideEffect:     autonomy.SideEffectReadOnly,
			PlannerAllowed: true,
		})
	}

	return modelProtocolPlannerInspection{
		Tools:    textutil.Unique(tools),
		Metadata: metadata,
	}
}

func modelProtocolRequiredTools(required []string, phase TurnPhaseContract, visibleTools []string) []string {
	var concrete []string
	for _, tool := range required {
		concrete = append(concrete, workspace.ConcreteRequiredTools(tool)...)
	}

	tools := modelProtocolVisibleRequiredTools(concrete, visibleTools)
	if slices.Contains(phase.ActionPolicy.CallableToolNames, workspace.ToolNameSubmitPlan) {
		tools = append(tools, workspace.ToolNameSubmitPlan)
	}

	return textutil.Unique(tools)
}

func modelProtocolVisibleTools(phase TurnPhaseContract) []string {
	tools := textutil.Merge(phase.AllowedTools, phase.ActionPolicy.CallableToolNames)
	if slices.Contains(phase.ActionPolicy.TerminalResponseShapes, workspace.ToolNameSubmitPlan) ||
		slices.Contains(phase.ActionPolicy.CallableToolNames, workspace.ToolNameSubmitPlan) {

		tools = append(tools, workspace.ToolNameSubmitPlan)
	}

	return textutil.Unique(tools)
}

func modelProtocolStructuredSchemas(phase TurnPhaseContract) []string {
	if slices.Contains(phase.ActionPolicy.TerminalResponseShapes, workspace.ToolNameSubmitPlan) ||
		slices.Contains(phase.ActionPolicy.CallableToolNames, workspace.ToolNameSubmitPlan) {

		return []string{workspace.ToolNameSubmitPlan}
	}

	return nil
}

func modelProtocolSourceBodyObligations(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, phase TurnPhaseContract, authority planThenExecutePathAuthority) []string {
	if phase.PhaseID != contractcompile.TurnPhaseRequiredSource && phase.PhaseID != contractcompile.TurnPhaseStaleAnchorRefresh {
		return nil
	}

	var paths []string

	for _, path := range brief.RequiredEditPaths {
		normalized := NormalizeRunnerPath(path)
		if normalized == "" {
			continue
		}

		state := planThenExecuteRequiredPathState(authority, results, normalized)
		switch phase.PhaseID {
		case contractcompile.TurnPhaseRequiredSource:
			if state.requiresSourceBodyRead() {
				paths = append(paths, normalized)
			}
		case contractcompile.TurnPhaseStaleAnchorRefresh:
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
	for _, path := range modelProtocolGitHistoryIntentPaths(brief, results, planThenExecutePathAuthority{}) {
		if successfulGitHistoryResultForPath(path, results) {
			paths = append(paths, path)
		}
	}

	return uniqueSortedRunnerPaths(paths)
}

func modelProtocolPendingGitHistoryPaths(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, authority planThenExecutePathAuthority) []string {
	if !mutationHistoryEvidenceRequired(brief) {
		return nil
	}

	intentPaths := modelProtocolGitHistoryIntentPaths(brief, results, authority)
	if len(authority.requiredCoveragePaths(brief)) > 0 {
		return planThenExecuteHistoryPendingPaths(brief, results, intentPaths, authority)
	}

	// An unscoped submit-plan deferral is emitted only after its selected
	// source paths passed submit preflight. Preserve that typed intent in the
	// protocol even when the source-body result is not part of this projection.
	var pending []string
	for _, path := range intentPaths {
		if !successfulGitHistoryResultForPath(path, results) {
			pending = append(pending, path)
		}
	}

	return uniqueSortedRunnerPaths(pending)
}

func modelProtocolGitHistoryIntentPaths(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, authority planThenExecutePathAuthority) []string {
	if required := authority.requiredCoveragePaths(brief); len(required) > 0 {
		return required
	}

	return planThenExecuteDeferredPlanIntentPaths(results)
}

func modelProtocolParserTransportMode(phase TurnPhaseContract) string {
	if textutil.EqualTrimFold(phase.RuntimeCall.ToolTransportMode, "native") {
		return "native_tools"
	}

	if textutil.EqualTrimFold(phase.RuntimeCall.ResponseFormat, "json") {
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
	if textutil.EqualTrimFold(phase.RuntimeCall.ToolTransportMode, "native") {
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
		SchemaVersion:                 autonomy.ModelProtocolProjectionSchemaVersion,
		ContractID:                    protocol.ContractID,
		ContractVersion:               protocol.ContractVersion,
		ProtocolHash:                  protocol.ProtocolHash,
		PhaseID:                       protocol.PhaseID,
		ParserMode:                    protocol.ParserMode,
		ToolTransportMode:             protocol.ToolTransportMode,
		ParserTransportMode:           protocol.ParserTransportMode,
		ParserContract:                protocol.ParserContract,
		NativeToolPolicy:              protocol.NativeToolPolicy,
		SupportsNativeTools:           protocol.SupportsNativeTools,
		ForceNativeTools:              protocol.ForceNativeTools,
		ToolChoice:                    protocol.ToolChoice,
		ResponseFormat:                protocol.ResponseFormat,
		ExpectedVisibleOutput:         protocol.ExpectedVisibleOutput,
		VisibleTools:                  append([]string(nil), protocol.VisibleTools...),
		CallableTools:                 append([]string(nil), protocol.CallableTools...),
		CallableActionClasses:         append([]string(nil), protocol.CallableActionClasses...),
		InvalidActionClasses:          append([]string(nil), protocol.InvalidActionClasses...),
		TerminalResponseShapes:        append([]string(nil), protocol.TerminalResponseShapes...),
		SourceBodyObligations:         append([]string(nil), protocol.SourceBodyObligations...),
		SourceBodyObligationTotal:     protocol.SourceBodyObligationTotal,
		SourceBodyObligationOmitted:   protocol.SourceBodyObligationOmitted,
		GroundedSourceBodyPaths:       append([]string(nil), protocol.GroundedSourceBodyPaths...),
		GroundedSourceBodyPathTotal:   protocol.GroundedSourceBodyPathTotal,
		GroundedSourceBodyPathOmitted: protocol.GroundedSourceBodyPathOmitted,
		CandidateOnlyPaths:            append([]string(nil), protocol.CandidateOnlyPaths...),
		CandidateOnlyPathTotal:        protocol.CandidateOnlyPathTotal,
		CandidateOnlyPathOmitted:      protocol.CandidateOnlyPathOmitted,
		CompletedGitHistoryPaths:      append([]string(nil), protocol.CompletedGitHistoryPaths...),
		PendingGitHistoryPaths:        append([]string(nil), protocol.PendingGitHistoryPaths...),
		RequiredEditCoverage:          append([]string(nil), protocol.RequiredEditCoverage...),
		PathRoleSummary:               append([]string(nil), protocol.PathRoleSummary...),
		VerificationObligations:       append([]string(nil), protocol.VerificationObligations...),
		SafeAlternatives:              append([]string(nil), protocol.SafeAlternatives...),
		MalformedOutputPolicy:         protocol.MalformedOutputPolicy,
		ActiveInvariantID:             protocol.ActiveInvariantID,
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

const turnPhaseContractSchemaVersion = "idiom.turn_phase_contract.v1"

const (
	actionRead             = string(autonomy.ActionClassRead)
	actionSearch           = string(autonomy.ActionClassSearch)
	actionSubmitPlan       = string(autonomy.ActionClassSubmitPlan)
	actionPerEditWrite     = string(autonomy.ActionClassPerEditWrite)
	actionDeliverableWrite = string(autonomy.ActionClassDeliverableWrite)
	actionSourceSatisfied  = string(autonomy.ActionClassSourceSatisfied)
	actionBlocker          = string(autonomy.ActionClassBlocker)
	actionVerify           = string(autonomy.ActionClassVerify)
	actionFinalAnswer      = string(autonomy.ActionClassFinalAnswer)
	actionReviewPass       = string(autonomy.ActionClassReviewPass)
	actionGenericProbe     = string(autonomy.ActionClassGenericProbe)
	actionProseFinal       = string(autonomy.ActionClassProseFinal)
)

// TurnPhaseContract is a shadow projection over the current runner/runtime
// state. It describes the active phase contract without mutating tool
// visibility, emitting events, or replacing existing gates.
type TurnPhaseContract struct {
	SchemaVersion    string
	ContractID       string
	PhaseID          contractcompile.TurnPhaseID
	PhaseEpoch       int
	ModelCallID      string
	Step             int
	Phase            string
	DerivationReason string
	InvariantIDs     []string

	PermissionProfile string
	AllowedTools      []string
	HiddenTools       []string
	RequiredNext      []string
	InvalidNext       []string
	StopConditions    []string
	TerminalOutcomes  []string
	Constraints       []string

	ActionPolicy    TurnActionClassPolicy
	TypedRoute      TypedRouteContract
	RuntimeCall     RuntimeCallContract
	LifecycleState  LifecycleState
	ActiveSurface   ActiveSurface
	EvidenceState   PhaseEvidenceState
	EnforcementMode string
}

type TurnActionClassPolicy struct {
	AllowedActionClasses   []string
	InvalidActionClasses   []string
	TerminalResponseShapes []string
	CallableToolNames      []string
}

type TypedRouteContract struct {
	Lane                  string
	TaskFamily            string
	WorkflowKind          string
	Phase                 string
	ActorRole             string
	CommandMode           string
	RouteSource           string
	RequiredCoveragePaths []string
	AllowedCandidatePaths []string
	EvidencePaths         []string
	ForbiddenPaths        []string
	ExactSnippets         []string
	EditSetObligations    []string
	TerminalOutcomeFamily string
}

type RuntimeCallContract struct {
	BackendKind              string
	ModelAlias               string
	ToolTransportMode        string
	ParserContract           string
	ResponseFormat           string
	Temperature              float64
	TopP                     float64
	TopK                     int
	MinP                     float64
	PresencePenalty          float64
	RepetitionPenalty        float64
	ToolChoice               string
	ThinkingPolicy           string
	MaxTokens                int
	TimeoutMillis            int
	EffectiveFallbackState   string
	ExpectedVisibleOutput    string
	ExpressibleActionClasses []string
}

type LifecycleState struct {
	TypedPhase         string
	AllowedTransitions []string
	TerminalStates     []string
	CloseoutOwner      string
	CancellationState  string
	TimeoutState       string
	EventOrdering      []string
}

func (contract TurnPhaseContract) RuntimeProjection() TurnContract {
	obligations := append([]string(nil), contract.TypedRoute.EditSetObligations...)
	obligations = append(obligations, contract.EvidenceState.SourceBodyObligations...)
	obligations = append(obligations, contract.EvidenceState.TDDObligationRefs...)
	obligations = append(obligations, contract.EvidenceState.VerificationEvidenceRefs...)

	return TurnContract{
		ContractID:    strings.TrimSpace(contract.ContractID),
		ProtocolHash:  strings.TrimSpace(contract.ModelCallID),
		PhaseID:       strings.TrimSpace(string(contract.PhaseID)),
		InvariantIDs:  textutil.Unique(contract.InvariantIDs),
		ObligationIDs: textutil.Unique(obligations),
	}
}

func (contract TurnPhaseContract) autonomyEventPayload() autonomy.TurnPhaseContractEvent {
	return autonomy.TurnPhaseContractEvent{
		SchemaVersion:    contract.SchemaVersion,
		ContractID:       contract.ContractID,
		PhaseID:          string(contract.PhaseID),
		PhaseEpoch:       contract.PhaseEpoch,
		ModelCallID:      contract.ModelCallID,
		Step:             contract.Step,
		Phase:            contract.Phase,
		DerivationReason: contract.DerivationReason,
		InvariantIDs:     append([]string(nil), contract.InvariantIDs...),

		PermissionProfile: contract.PermissionProfile,
		AllowedTools:      append([]string(nil), contract.AllowedTools...),
		HiddenTools:       append([]string(nil), contract.HiddenTools...),
		RequiredNext:      append([]string(nil), contract.RequiredNext...),
		InvalidNext:       append([]string(nil), contract.InvalidNext...),
		StopConditions:    append([]string(nil), contract.StopConditions...),
		TerminalOutcomes:  append([]string(nil), contract.TerminalOutcomes...),
		Constraints:       append([]string(nil), contract.Constraints...),

		ActionPolicy: autonomy.TurnActionClassPolicyEvent{
			AllowedActionClasses:   append([]string(nil), contract.ActionPolicy.AllowedActionClasses...),
			InvalidActionClasses:   append([]string(nil), contract.ActionPolicy.InvalidActionClasses...),
			TerminalResponseShapes: append([]string(nil), contract.ActionPolicy.TerminalResponseShapes...),
			CallableToolNames:      append([]string(nil), contract.ActionPolicy.CallableToolNames...),
		},
		TypedRoute: autonomy.TypedRouteContractEvent{
			Lane:                  contract.TypedRoute.Lane,
			TaskFamily:            contract.TypedRoute.TaskFamily,
			WorkflowKind:          contract.TypedRoute.WorkflowKind,
			Phase:                 contract.TypedRoute.Phase,
			ActorRole:             contract.TypedRoute.ActorRole,
			CommandMode:           contract.TypedRoute.CommandMode,
			RouteSource:           contract.TypedRoute.RouteSource,
			RequiredCoveragePaths: append([]string(nil), contract.TypedRoute.RequiredCoveragePaths...),
			AllowedCandidatePaths: append([]string(nil), contract.TypedRoute.AllowedCandidatePaths...),
			EvidencePaths:         append([]string(nil), contract.TypedRoute.EvidencePaths...),
			ForbiddenPaths:        append([]string(nil), contract.TypedRoute.ForbiddenPaths...),
			ExactSnippets:         append([]string(nil), contract.TypedRoute.ExactSnippets...),
			EditSetObligations:    append([]string(nil), contract.TypedRoute.EditSetObligations...),
			TerminalOutcomeFamily: contract.TypedRoute.TerminalOutcomeFamily,
		},
		RuntimeCall: autonomy.RuntimeCallContractEvent{
			BackendKind:              contract.RuntimeCall.BackendKind,
			ModelAlias:               contract.RuntimeCall.ModelAlias,
			ToolTransportMode:        contract.RuntimeCall.ToolTransportMode,
			ParserContract:           contract.RuntimeCall.ParserContract,
			ResponseFormat:           contract.RuntimeCall.ResponseFormat,
			Temperature:              contract.RuntimeCall.Temperature,
			TopP:                     contract.RuntimeCall.TopP,
			TopK:                     contract.RuntimeCall.TopK,
			MinP:                     contract.RuntimeCall.MinP,
			PresencePenalty:          contract.RuntimeCall.PresencePenalty,
			RepetitionPenalty:        contract.RuntimeCall.RepetitionPenalty,
			ToolChoice:               contract.RuntimeCall.ToolChoice,
			ThinkingPolicy:           contract.RuntimeCall.ThinkingPolicy,
			MaxTokens:                contract.RuntimeCall.MaxTokens,
			TimeoutMillis:            contract.RuntimeCall.TimeoutMillis,
			EffectiveFallbackState:   contract.RuntimeCall.EffectiveFallbackState,
			ExpectedVisibleOutput:    contract.RuntimeCall.ExpectedVisibleOutput,
			ExpressibleActionClasses: append([]string(nil), contract.RuntimeCall.ExpressibleActionClasses...),
		},
		LifecycleState: autonomy.LifecycleStateEvent{
			TypedPhase:         contract.LifecycleState.TypedPhase,
			AllowedTransitions: append([]string(nil), contract.LifecycleState.AllowedTransitions...),
			TerminalStates:     append([]string(nil), contract.LifecycleState.TerminalStates...),
			CloseoutOwner:      contract.LifecycleState.CloseoutOwner,
			CancellationState:  contract.LifecycleState.CancellationState,
			TimeoutState:       contract.LifecycleState.TimeoutState,
			EventOrdering:      append([]string(nil), contract.LifecycleState.EventOrdering...),
		},
		EvidenceState: autonomy.PhaseEvidenceStateEvent{
			AcceptanceRefs:           append([]string(nil), contract.EvidenceState.AcceptanceRefs...),
			TDDObligationRefs:        append([]string(nil), contract.EvidenceState.TDDObligationRefs...),
			VerificationEvidenceRefs: append([]string(nil), contract.EvidenceState.VerificationEvidenceRefs...),
			VerificationStatus:       contract.EvidenceState.VerificationStatus,
			ProductWorkflowState:     contract.EvidenceState.ProductWorkflowState,
			AtomStatus:               contract.EvidenceState.AtomStatus,
			RecoveryPacketRefs:       append([]string(nil), contract.EvidenceState.RecoveryPacketRefs...),
			ResidualBlockers:         append([]string(nil), contract.EvidenceState.ResidualBlockers...),
			SourceBodyObligations:    append([]string(nil), contract.EvidenceState.SourceBodyObligations...),
			CompletedGitHistoryPaths: append([]string(nil), contract.EvidenceState.CompletedGitHistoryPaths...),
			PendingGitHistoryPaths:   append([]string(nil), contract.EvidenceState.PendingGitHistoryPaths...),
		},
		EnforcementMode: contract.EnforcementMode,
	}
}

type PhaseEvidenceState struct {
	AcceptanceRefs           []string
	TDDObligationRefs        []string
	VerificationEvidenceRefs []string
	VerificationStatus       string
	ProductWorkflowState     string
	AtomStatus               string
	RecoveryPacketRefs       []string
	ResidualBlockers         []string
	SourceBodyObligations    []string // paths pending source-body evidence
	CompletedGitHistoryPaths []string // paths with satisfied git history evidence
	PendingGitHistoryPaths   []string // paths still needing git history evidence
}

func (state PhaseEvidenceState) autonomyEvent() autonomy.PhaseEvidenceStateEvent {
	return autonomy.PhaseEvidenceStateEvent{
		AcceptanceRefs:           append([]string(nil), state.AcceptanceRefs...),
		TDDObligationRefs:        append([]string(nil), state.TDDObligationRefs...),
		VerificationEvidenceRefs: append([]string(nil), state.VerificationEvidenceRefs...),
		VerificationStatus:       state.VerificationStatus,
		ProductWorkflowState:     state.ProductWorkflowState,
		AtomStatus:               state.AtomStatus,
		RecoveryPacketRefs:       append([]string(nil), state.RecoveryPacketRefs...),
		ResidualBlockers:         append([]string(nil), state.ResidualBlockers...),
		SourceBodyObligations:    append([]string(nil), state.SourceBodyObligations...),
		CompletedGitHistoryPaths: append([]string(nil), state.CompletedGitHistoryPaths...),
		PendingGitHistoryPaths:   append([]string(nil), state.PendingGitHistoryPaths...),
	}
}

type TurnPhaseContractInput struct {
	ContractID  string
	PhaseEpoch  int
	ModelCallID string
	Step        int
	PhaseHint   contractcompile.TurnPhaseID

	Brief       workerruntime.WorkerRuntimeBrief
	Results     []workspace.ToolResult
	LastRequest workspace.ToolRequest
	LastResult  workspace.ToolResult

	ImplementationCompletion *implementationCompletionState
	mutationAuthority        planThenExecutePathAuthority
	InspectionAuthority      workspace.PermissionAuthorizedInspectionProjection

	BaseAllowedTools []string
	HiddenTools      []string

	BackendKind       string
	ModelAlias        string
	ToolTransportMode string
	ParserContract    string
	ResponseFormat    string
	Temperature       float64
	TopP              float64
	TopK              int
	MinP              float64
	PresencePenalty   float64
	RepetitionPenalty float64
	ToolChoice        string
	ThinkingPolicy    string
	MaxTokens         int
	TimeoutMillis     int

	SupportsNativeTools    bool
	TextualToolParser      bool
	ActiveSurface          ActiveSurface
	EffectiveFallbackState string

	AcceptanceRefs           []string
	TDDObligationRefs        []string
	VerificationEvidenceRefs []string
	VerificationStatus       string
	ProductWorkflowState     string
	AtomStatus               string
	RecoveryPacketRefs       []string
	ResidualBlockers         []string
	SourceBodyObligations    []string // paths pending source-body evidence
	CompletedGitHistoryPaths []string // paths with satisfied git history evidence
	PendingGitHistoryPaths   []string // paths still needing git history evidence
}

func deriveTurnPhaseContract(input TurnPhaseContractInput) TurnPhaseContract {
	brief := workerruntime.NormalizeRuntimeBrief(input.Brief)
	baseTools := phaseContractBaseTools(input, brief)
	contract := TurnPhaseContract{
		SchemaVersion:     turnPhaseContractSchemaVersion,
		ContractID:        strings.TrimSpace(input.ContractID),
		PhaseEpoch:        input.PhaseEpoch,
		ModelCallID:       strings.TrimSpace(input.ModelCallID),
		Step:              input.Step,
		PermissionProfile: brief.PermissionProfile,
		AllowedTools:      textutil.Unique(baseTools),
		HiddenTools:       textutil.Unique(input.HiddenTools),
		RequiredNext:      textutil.Unique(brief.RequiredNext),
		InvalidNext:       textutil.Unique(brief.InvalidActions),
		StopConditions:    textutil.Unique(brief.StopConditions),
		Constraints:       textutil.Unique(brief.Constraints),
		RuntimeCall: RuntimeCallContract{
			BackendKind:            strings.TrimSpace(input.BackendKind),
			ModelAlias:             strings.TrimSpace(input.ModelAlias),
			ToolTransportMode:      strings.TrimSpace(input.ToolTransportMode),
			ParserContract:         strings.TrimSpace(input.ParserContract),
			ResponseFormat:         strings.TrimSpace(input.ResponseFormat),
			Temperature:            input.Temperature,
			TopP:                   input.TopP,
			TopK:                   input.TopK,
			MinP:                   input.MinP,
			PresencePenalty:        input.PresencePenalty,
			RepetitionPenalty:      input.RepetitionPenalty,
			ToolChoice:             strings.TrimSpace(input.ToolChoice),
			ThinkingPolicy:         strings.TrimSpace(input.ThinkingPolicy),
			MaxTokens:              input.MaxTokens,
			TimeoutMillis:          input.TimeoutMillis,
			EffectiveFallbackState: strings.TrimSpace(input.EffectiveFallbackState),
			ExpectedVisibleOutput:  expectedVisibleOutput(input),
		},
		EvidenceState: PhaseEvidenceState{
			AcceptanceRefs:           textutil.Unique(input.AcceptanceRefs),
			TDDObligationRefs:        textutil.Unique(input.TDDObligationRefs),
			VerificationEvidenceRefs: textutil.Unique(input.VerificationEvidenceRefs),
			VerificationStatus:       strings.TrimSpace(input.VerificationStatus),
			ProductWorkflowState:     strings.TrimSpace(input.ProductWorkflowState),
			AtomStatus:               strings.TrimSpace(input.AtomStatus),
			RecoveryPacketRefs:       textutil.Unique(input.RecoveryPacketRefs),
			ResidualBlockers:         textutil.Unique(input.ResidualBlockers),
			SourceBodyObligations:    textutil.Unique(input.SourceBodyObligations),
			CompletedGitHistoryPaths: textutil.Unique(input.CompletedGitHistoryPaths),
			PendingGitHistoryPaths:   textutil.Unique(input.PendingGitHistoryPaths),
		},
		EnforcementMode: "observe_only",
	}

	applyTurnPhase(&contract, input, brief, baseTools)
	contract.TypedRoute = deriveTypedRouteContract(brief, contract, input.mutationAuthority)
	contract.LifecycleState = deriveLifecycleState(brief, contract, input)

	contract.EvidenceState.SourceBodyObligations = modelProtocolSourceBodyObligations(brief, input.Results, contract, input.mutationAuthority)
	contract.EvidenceState.CompletedGitHistoryPaths = modelProtocolCompletedGitHistoryPaths(brief, input.Results)
	contract.EvidenceState.PendingGitHistoryPaths = modelProtocolPendingGitHistoryPaths(brief, input.Results, input.mutationAuthority)
	pruneFinalAnswerWhenEvidenceOpen(&contract)

	if phaseContractTextualOnly(input, contract) {
		contract.AllowedTools = nil
		contract.ActionPolicy.CallableToolNames = nil
	}

	if len(input.HiddenTools) > 0 {
		contract.AllowedTools = removePhaseTools(contract.AllowedTools, input.HiddenTools)
		contract.ActionPolicy.CallableToolNames = removePhaseTools(contract.ActionPolicy.CallableToolNames, input.HiddenTools)
	}

	if activeSurfaceProvided(input.ActiveSurface) {
		contract.ActiveSurface = input.ActiveSurface
		contract.AllowedTools = contract.ActiveSurface.FilterVisible(contract.AllowedTools)
		contract.ActionPolicy.CallableToolNames = contract.ActiveSurface.FilterVisible(contract.ActionPolicy.CallableToolNames)
		contract.HiddenTools = textutil.Unique(append(contract.HiddenTools, contract.ActiveSurface.HiddenTools...))
	}

	if len(contract.ActionPolicy.InvalidActionClasses) > 0 {
		contract.AllowedTools = removePhaseToolsForInvalidActionClasses(contract.AllowedTools, contract.ActionPolicy.InvalidActionClasses)
		contract.ActionPolicy.CallableToolNames = removePhaseToolsForInvalidActionClasses(contract.ActionPolicy.CallableToolNames, contract.ActionPolicy.InvalidActionClasses)
	}

	if len(input.HiddenTools) > 0 || len(contract.ActionPolicy.CallableToolNames) > 0 {
		contract.ActionPolicy.AllowedActionClasses = pruneToolActionClassesWithoutCallableTool(contract.ActionPolicy.AllowedActionClasses, contract.ActionPolicy.CallableToolNames)
		contract.RuntimeCall.ExpressibleActionClasses = pruneToolActionClassesWithoutCallableTool(contract.RuntimeCall.ExpressibleActionClasses, contract.ActionPolicy.CallableToolNames)
	}

	contract.AllowedTools = textutil.Unique(contract.AllowedTools)
	contract.HiddenTools = textutil.Unique(append(contract.HiddenTools, hiddenPhaseTools(baseTools, contract.AllowedTools)...))
	contract.RequiredNext = textutil.Unique(contract.RequiredNext)
	contract.InvalidNext = textutil.Unique(contract.InvalidNext)
	contract.StopConditions = textutil.Unique(contract.StopConditions)
	contract.TerminalOutcomes = textutil.Unique(contract.TerminalOutcomes)
	contract.Constraints = textutil.Unique(contract.Constraints)
	contract.ActionPolicy.AllowedActionClasses = textutil.Unique(contract.ActionPolicy.AllowedActionClasses)
	contract.ActionPolicy.InvalidActionClasses = textutil.Unique(contract.ActionPolicy.InvalidActionClasses)
	contract.ActionPolicy.AllowedActionClasses = actionClassesAllowedByPolicy(contract.ActionPolicy.AllowedActionClasses, contract.ActionPolicy.InvalidActionClasses)
	contract.ActionPolicy.TerminalResponseShapes = textutil.Unique(contract.ActionPolicy.TerminalResponseShapes)
	contract.ActionPolicy.CallableToolNames = textutil.Unique(contract.ActionPolicy.CallableToolNames)
	contract.RuntimeCall.ExpressibleActionClasses = textutil.Unique(contract.RuntimeCall.ExpressibleActionClasses)
	contract.RuntimeCall.ExpressibleActionClasses = actionClassesAllowedByPolicy(contract.RuntimeCall.ExpressibleActionClasses, contract.ActionPolicy.InvalidActionClasses)
	if !activeSurfaceProvided(contract.ActiveSurface) {
		contract.ActiveSurface = NewActiveSurface(contract.AllowedTools, contract.HiddenTools)
	} else {
		contract.ActiveSurface.SetVisibleTools(contract.AllowedTools)
	}

	return contract
}

func pruneFinalAnswerWhenEvidenceOpen(contract *TurnPhaseContract) {
	if contract == nil || !phaseEvidenceHasOpenObligation(contract.EvidenceState) {
		return
	}

	contract.ActionPolicy.AllowedActionClasses = removePhaseActionClass(contract.ActionPolicy.AllowedActionClasses, actionFinalAnswer)
	contract.RuntimeCall.ExpressibleActionClasses = removePhaseActionClass(contract.RuntimeCall.ExpressibleActionClasses, actionFinalAnswer)
	contract.TerminalOutcomes = removePhaseTerminalShape(contract.TerminalOutcomes, "final answer")
	contract.ActionPolicy.TerminalResponseShapes = removePhaseTerminalShape(contract.ActionPolicy.TerminalResponseShapes, "final answer")
	contract.ActionPolicy.InvalidActionClasses = append(contract.ActionPolicy.InvalidActionClasses, actionFinalAnswer)
}

func activeSurfaceProvided(surface ActiveSurface) bool {
	return len(surface.VisibleTools) > 0 ||
		len(surface.HiddenTools) > 0 ||
		len(surface.CallableActionClasses) > 0 ||
		len(surface.InvalidActionClasses) > 0 ||
		len(surface.TerminalResponseShapes) > 0 ||
		len(surface.RequiredActionClasses) > 0
}

func deriveTypedRouteContract(brief workerruntime.WorkerRuntimeBrief, contract TurnPhaseContract, authority planThenExecutePathAuthority) TypedRouteContract {
	route := TypedRouteContract{
		Lane:                  strings.TrimSpace(brief.Lane),
		TaskFamily:            strings.TrimSpace(brief.TaskFamily),
		WorkflowKind:          workflowKindForBrief(brief),
		Phase:                 strings.TrimSpace(string(contract.PhaseID)),
		ActorRole:             actorRoleForBrief(brief),
		CommandMode:           commandModeForBrief(brief),
		RouteSource:           strings.TrimSpace(contract.DerivationReason),
		RequiredCoveragePaths: authority.requiredCoveragePaths(brief),
		AllowedCandidatePaths: textutil.Unique(brief.AllowedEditPaths),
		EvidencePaths:         textutil.Unique(brief.EvidencePaths),
		ExactSnippets:         requiredSnippetIDs(brief.RequiredEditSnippets),
		TerminalOutcomeFamily: terminalOutcomeFamilyForContract(contract),
	}
	route.EditSetObligations = editSetObligationsForBrief(brief)
	return route
}

func workflowKindForBrief(brief workerruntime.WorkerRuntimeBrief) string {
	switch {
	case strings.TrimSpace(brief.TaskFamily) == PlanThenExecutePlannerTaskFamily,
		strings.TrimSpace(brief.TaskFamily) == PlanThenExecuteReviewRepairTaskFamily,
		strings.TrimSpace(brief.Lane) == string(lanecontract.LanePlanExecute),
		strings.TrimSpace(brief.Contract) == workspace.ToolNameSubmitPlan:
		return "plan_execute"
	case workerruntime.TaskFamilyMatches(brief.TaskFamily, autonomy.TaskFamilyImplementation),
		workerruntime.TaskFamilyMatches(brief.TaskFamily, autonomy.TaskFamilyDelegatedIsolatedImpl):
		return "implementation"
	case workerruntime.RuntimeContractIsDirectAnswer(brief.Contract):
		return "direct_answer"
	default:
		return "generic"
	}
}

func actorRoleForBrief(brief workerruntime.WorkerRuntimeBrief) string {
	switch workflowKindForBrief(brief) {
	case "plan_execute":
		return "planner"
	case "implementation":
		return "executor"
	case "direct_answer":
		return "answerer"
	default:
		return "model"
	}
}

func commandModeForBrief(brief workerruntime.WorkerRuntimeBrief) string {
	if workerruntime.PermissionProfileIsReadOnlyWorkspace(brief.PermissionProfile) {
		return "read_only"
	}
	return "write_capable"
}

func terminalOutcomeFamilyForContract(contract TurnPhaseContract) string {
	classes := append([]string(nil), contract.ActionPolicy.AllowedActionClasses...)
	terminalShapes := append([]string(nil), contract.ActionPolicy.TerminalResponseShapes...)
	switch {
	case slices.Contains(classes, actionSubmitPlan) || slices.Contains(terminalShapes, workspace.ToolNameSubmitPlan):
		return "plan_submission"
	case slices.Contains(classes, actionSourceSatisfied):
		return "source_satisfied"
	case slices.Contains(classes, actionReviewPass):
		return "review_closeout"
	case slices.Contains(classes, actionFinalAnswer):
		return "final_answer"
	case slices.Contains(classes, actionBlocker):
		return "blocker"
	default:
		return "tool_call"
	}
}

func requiredSnippetIDs(snippets []plancontract.RequiredEditSnippet) []string {
	ids := make([]string, 0, len(snippets))
	for _, snippet := range snippets {
		if id := strings.TrimSpace(snippet.ID); id != "" {
			ids = append(ids, id)
		}
	}
	return textutil.Unique(ids)
}

func editSetObligationsForBrief(brief workerruntime.WorkerRuntimeBrief) []string {
	obligations := make([]string, 0, len(brief.RequiredEditPaths)+len(brief.RequiredEditSnippets))
	for _, path := range brief.RequiredEditPaths {
		if path = strings.TrimSpace(path); path != "" {
			obligations = append(obligations, "required_edit:"+path)
		}
	}
	for _, snippet := range brief.RequiredEditSnippets {
		if path := strings.TrimSpace(snippet.Path); path != "" {
			obligations = append(obligations, "required_snippet:"+path)
		}
	}
	return textutil.Unique(obligations)
}

func deriveLifecycleState(brief workerruntime.WorkerRuntimeBrief, contract TurnPhaseContract, input TurnPhaseContractInput) LifecycleState {
	timeoutState := "not_configured"
	if input.TimeoutMillis > 0 {
		timeoutState = "armed"
	}

	return LifecycleState{
		TypedPhase:         firstPhaseString(contract.Phase, string(contract.PhaseID), "generic"),
		AllowedTransitions: lifecycleAllowedTransitions(contract),
		TerminalStates:     append([]string(nil), contract.TerminalOutcomes...),
		CloseoutOwner:      lifecycleCloseoutOwner(brief, contract),
		CancellationState:  "available",
		TimeoutState:       timeoutState,
		EventOrdering:      []string{"turn_phase_contract", "model_call", "tool_result_or_terminal"},
	}
}

func lifecycleAllowedTransitions(contract TurnPhaseContract) []string {
	transitions := []string{"model_call", "tool_result"}
	if len(contract.TerminalOutcomes) > 0 {
		transitions = append(transitions, "terminal_outcome")
	}
	return textutil.Unique(transitions)
}

func lifecycleCloseoutOwner(brief workerruntime.WorkerRuntimeBrief, contract TurnPhaseContract) string {
	if contract.PhaseID == contractcompile.TurnPhaseSourceSatisfiedTerminal {
		return "planner"
	}
	if role := actorRoleForBrief(brief); role != "" && role != "model" {
		return role
	}
	return "runner"
}

func actionClassesAllowedByPolicy(allowed []string, invalid []string) []string {
	invalidSet := make(map[string]struct{}, len(invalid))
	for _, value := range invalid {
		value = strings.TrimSpace(value)
		if value != "" {
			invalidSet[value] = struct{}{}
		}
	}

	out := make([]string, 0, len(allowed))
	for _, value := range allowed {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}

		if _, blocked := invalidSet[value]; blocked {
			continue
		}

		out = append(out, value)
	}

	return textutil.Unique(out)
}

func applyTurnPhase(contract *TurnPhaseContract, input TurnPhaseContractInput, brief workerruntime.WorkerRuntimeBrief, baseTools []string) {
	planningTransport := planExecutePlanningTransportFromPhaseInput(input)
	recoverySurface := derivePlanExecuteRecoverySurface(brief, input.Results, planningTransport, input.mutationAuthority)
	planningState := recoverySurface.State
	completionState := implementationCompletionState{
		MissingRequiredPaths: append([]string(nil), brief.RequiredEditPaths...),
	}
	if input.ImplementationCompletion != nil {
		completionState = *input.ImplementationCompletion
	}
	mutationCommitted := len(completionState.ChangedPaths) > 0

	switch {
	case contractcompile.TurnPhaseID(strings.TrimSpace(string(input.PhaseHint))) == contractcompile.TurnPhaseProductAcceptanceCloseout:
		applyProductAcceptanceCloseout(contract)
	case artifactFinalAnswerBrief(brief):
		applyArtifactFinalAnswer(contract)
	case recoverySurface.Kind == PlanExecuteRecoverySurfaceAnchorRefresh:
		applyStaleAnchorRefresh(contract, input, baseTools, planningState)
	case recoverySurface.Kind == PlanExecuteRecoverySurfaceHistoryPending:
		applyHistoryPending(contract, baseTools, planningState)
	case recoverySurface.Kind == PlanExecuteRecoverySurfaceSourceSatisfiedTerminal:
		applySourceSatisfiedTerminal(contract, baseTools, planningState)
	case recoverySurface.Kind == PlanExecuteRecoverySurfaceSubmitCorrection:
		applySubmitReady(contract, input, baseTools, planningState)
	case planThenExecuteSubmitReadyParseFailureSeen(brief, input.Results, input.mutationAuthority):
		applyMalformedSubmitPlanRecovery(contract, input, baseTools)
	case planThenExecuteSubmitPlanOnlyReady(brief, input.Results, input.mutationAuthority):
		applySubmitReady(contract, input, baseTools, planningState)
	case planThenExecuteUnreadRequiredSourceNudge(brief, input.Results, input.mutationAuthority) != "":
		applyRequiredSource(contract, baseTools, planningState)
	case planThenExecuteCandidateSourcePending(brief, input.Results, input.mutationAuthority):
		applyCandidateSource(contract, baseTools, planningState)
	case strings.TrimSpace(brief.TaskFamily) == PlanThenExecuteReviewRepairTaskFamily &&
		(contractcompile.TurnPhaseID(strings.TrimSpace(string(input.PhaseHint))) == contractcompile.TurnPhaseReviewRepairTerminal ||
			len(planThenExecuteReviewRepairFailedPhaseObligations(brief)) > 0 ||
			reviewRepairCompletionGateRejectionCount(input.Results) > 0):
		applyReviewRepairTerminal(contract, brief, baseTools, input.InspectionAuthority.ToolNames())
	case runnerBriefRequiresMutation(brief) && completionState.VerificationFailed:
		applyImplementationVerificationRepair(contract, brief, baseTools, input.InspectionAuthority.ToolNames())
	case runnerBriefRequiresMutation(brief) && len(completionState.MissingRequiredSnippetIDs) > 0:
		applyImplementationRequiredSnippetRecovery(contract, baseTools, input.InspectionAuthority.ToolNames(), completionState)
	case runnerBriefRequiresMutation(brief) && completionState.HasPendingRejectedMutation():
		applyImplementationRejectedMutationRecovery(contract, baseTools, input.InspectionAuthority.ToolNames(), completionState)
	case runnerBriefRequiresMutation(brief) && len(completionState.OpenTaskLedgerObligationIDs) > 0:
		applyImplementationTaskLedgerRecovery(contract, baseTools, completionState)
	case runnerBriefRequiresMutation(brief) && len(completionState.MissingRequiredPaths) > 0:
		applyImplementationWriteReady(contract, baseTools, input.InspectionAuthority.ToolNames())
	case runnerBriefRequiresMutation(brief) &&
		(completionState.VerificationPending || completionState.SemanticVerificationPending):
		applyImplementationVerifyReady(contract, brief, baseTools, input.InspectionAuthority.ToolNames())
	case implementationTargetWriteReadySurfaceActive(brief, input.Results, mutationCommitted):
		applyImplementationTargetWriteReady(contract, brief, baseTools, input.InspectionAuthority.ToolNames(), input.Results, mutationCommitted)
	case implementationTargetSourcePending(brief, input.Results, mutationCommitted):
		applyImplementationTargetSource(contract, baseTools, input.InspectionAuthority.ToolNames())
	case implementationScopedEditSurfaceActive(brief, input.Results, completionState.MissingRequiredPaths, mutationCommitted):
		applyImplementationWriteReady(contract, baseTools, input.InspectionAuthority.ToolNames())
	default:
		applyGenericTurnPhase(contract, brief, input.Results)
	}

	if planExecuteInspectionAdditiveBrief(brief) {
		applyPermissionAuthorizedPlanInspection(contract, baseTools, input.InspectionAuthority)
	}
}

func planExecuteInspectionAdditiveBrief(brief workerruntime.WorkerRuntimeBrief) bool {
	switch strings.TrimSpace(brief.TaskFamily) {
	case PlanThenExecutePlannerTaskFamily, PlanThenExecuteReviewRepairTaskFamily:
		return true
	default:
		return false
	}
}

// applyPermissionAuthorizedPlanInspection normalizes every non-executing
// planner phase to the project-wide strategy boundary: readiness may require a
// submit/history/anchor outcome, but it may not hide or invalidate an already
// permission-authorized inspection request. Request-specific shell admission
// remains enforced by the workspace/runtime preflight.
func applyPermissionAuthorizedPlanInspection(contract *TurnPhaseContract, baseTools []string, authority workspace.PermissionAuthorizedInspectionProjection) {
	if contract == nil {
		return
	}
	inspectionTools := workspace.FilterToolNames(baseTools, authority.ToolNames()...)
	if len(inspectionTools) == 0 {
		return
	}

	inspectionClasses := recoveryCardCallableActionClassesForTools(inspectionTools, nil)
	contract.AllowedTools = textutil.Merge(contract.AllowedTools, inspectionTools)
	contract.ActionPolicy.CallableToolNames = textutil.Merge(contract.ActionPolicy.CallableToolNames, inspectionTools)
	contract.ActionPolicy.AllowedActionClasses = textutil.Merge(contract.ActionPolicy.AllowedActionClasses, inspectionClasses)
	contract.ActionPolicy.InvalidActionClasses = removeRunnerStrings(contract.ActionPolicy.InvalidActionClasses, inspectionClasses...)
	contract.RuntimeCall.ExpressibleActionClasses = textutil.Merge(contract.RuntimeCall.ExpressibleActionClasses, inspectionClasses)

	var invalidNext []string
	for _, item := range contract.InvalidNext {
		lower := strings.ToLower(strings.TrimSpace(item))
		if strings.Contains(lower, "read") || strings.Contains(lower, "search") || strings.Contains(lower, "inspection") {
			continue
		}
		invalidNext = append(invalidNext, item)
	}
	contract.InvalidNext = invalidNext
}

func removeRunnerStrings(values []string, removed ...string) []string {
	removeSet := make(map[string]struct{}, len(removed))
	for _, value := range removed {
		if value = strings.TrimSpace(value); value != "" {
			removeSet[value] = struct{}{}
		}
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if _, remove := removeSet[strings.TrimSpace(value)]; !remove {
			out = append(out, value)
		}
	}
	return textutil.Unique(out)
}

func applyHistoryPending(contract *TurnPhaseContract, baseTools []string, state PlanExecutePlanningState) {
	applyPlanExecutePlanningStateProjection(contract, baseTools, state, state.CallableTools)
	contract.RequiredNext = []string{"call " + workspace.PlanExecuteHistoryPendingToolListForPrompt() + " for the intended logic, Decision: source-satisfied with per-path current-state evidence when no edit is needed, or <<QUESTION>> with the exact blocker"}
	contract.InvalidNext = []string{"submit_plan before required git history evidence", "per-edit write in planner phase", "verify/build/test in planner phase"}
	contract.RuntimeCall.ParserContract = "source_refresh"
	contract.RuntimeCall.ExpressibleActionClasses = []string{actionRead, actionSourceSatisfied, actionBlocker}
}

func applyRequiredSource(contract *TurnPhaseContract, baseTools []string, state PlanExecutePlanningState) {
	applyPlanExecutePlanningStateProjection(contract, baseTools, state, state.CallableTools)
	contract.RequiredNext = []string{"source-body evidence remains pending for required_edit_paths; choose any permission-authorized inspection that resolves a concrete edit-safety fact, Decision: source-satisfied with per-path current-state evidence, or <<QUESTION>> with the exact blocker"}
	contract.InvalidNext = []string{"submit_plan before source-body evidence", "per-edit write in planner phase"}
	contract.RuntimeCall.ParserContract = "source_refresh"
	contract.RuntimeCall.ExpressibleActionClasses = []string{actionRead, actionSourceSatisfied, actionBlocker}
}

func applyCandidateSource(contract *TurnPhaseContract, baseTools []string, state PlanExecutePlanningState) {
	applyPlanExecutePlanningStateProjection(contract, baseTools, state, state.CallableTools)
	contract.RequiredNext = []string{"current typed state: " + string(state.Mode) + "; valid terminal actions: Decision: source-satisfied with observed current-state evidence or <<QUESTION>> with the exact blocker; if no source-body evidence exists yet, use one bounded localization/read action"}
	contract.InvalidNext = []string{"submit_plan before source-body evidence for each planned edit path", "per-edit write in planner phase", "verify/build/test in planner phase"}

	if slices.Contains(state.AllowedActionClasses, actionSubmitPlan) {
		contract.RequiredNext = []string{"current typed state: " + string(state.Mode) + "; valid terminal actions: submit_plan, Decision: source-satisfied, or <<QUESTION>> with the exact blocker; textual submit_plan must be one visible JSON object with real rationale and executable edits, not placeholders; continue targeted source/test localization only for a named missing edit-safety fact"}
		contract.InvalidNext = []string{"submit_plan edits without source-body evidence for each edited path", "per-edit write in planner phase", "verify/build/test in planner phase"}
	}

	contract.RuntimeCall.ParserContract = firstPhaseString(contract.RuntimeCall.ParserContract, "source_refresh")
	contract.RuntimeCall.ExpressibleActionClasses = []string{actionRead, actionSearch, actionBlocker}

	if slices.Contains(state.AllowedActionClasses, actionSubmitPlan) {
		contract.RuntimeCall.ExpressibleActionClasses = append(contract.RuntimeCall.ExpressibleActionClasses, actionSubmitPlan, actionSourceSatisfied)
	}

}

func planExecutePlanningTransportFromPhaseInput(input TurnPhaseContractInput) PlanExecutePlanningTransport {
	return planExecuteRuntimeCallPolicyFromPhaseInput(input).PlanningTransport()
}

func applySubmitReady(contract *TurnPhaseContract, input TurnPhaseContractInput, baseTools []string, state PlanExecutePlanningState) {
	transport := planExecutePlanningTransportFromPhaseInput(input)

	if state.Mode != PlanExecutePlanningModeSubmitReady && state.Mode != PlanExecutePlanningModeSubmitCorrection {
		state = PlanExecutePlanningState{Mode: PlanExecutePlanningModeSubmitReady}
		state.applyProjection(transport)
	}

	nativeRequired := planExecuteSubmitReadyNativeRequired(transport)

	applyPlanExecutePlanningStateProjection(contract, baseTools, state, state.ParserVisibleTools)
	contract.RequiredNext = []string{"submit_plan using edits[] for changed paths and source_satisfied_paths for already-final paths, Decision: source-satisfied with per-path current-state evidence, or <<QUESTION>> with the exact blocker"}
	contract.InvalidNext = []string{"verify in planner phase", "per-edit write in planner phase", "prose-only final answer"}

	if state.Mode == PlanExecutePlanningModeSubmitCorrection {
		contract.RequiredNext = []string{"corrected submit_plan using edits[] for changed paths and source_satisfied_paths for already-final paths"}
		contract.InvalidNext = []string{"terminal Decision: source-satisfied after an incomplete submit_plan; use submit_plan.source_satisfied_paths for already-final paths instead", "<<QUESTION>> after source evidence is sufficient for correction", "verify in planner phase", "per-edit write in planner phase", "prose-only final answer"}
		if state.SubmitCorrectionBlockerAllowed {
			contract.RequiredNext = []string{"corrected submit_plan using edits[] for changed paths, or <<QUESTION>> with the exact path-specific missing body/anchor blocker after repeated rejected submit_plan attempts"}
			contract.InvalidNext = []string{"terminal Decision: source-satisfied after an incomplete submit_plan; use submit_plan.source_satisfied_paths for already-final paths instead", "generic <<QUESTION>> without exact path-specific missing body/anchor blocker", "verify in planner phase", "per-edit write in planner phase", "prose-only final answer"}
		}
	}

	contract.RuntimeCall.ParserContract = firstPhaseString(contract.RuntimeCall.ParserContract, "submit_plan_json")
	contract.RuntimeCall.ResponseFormat = firstPhaseString(contract.RuntimeCall.ResponseFormat, "json")
	contract.RuntimeCall.ExpressibleActionClasses = []string{actionRead, actionSearch, actionSubmitPlan, actionSourceSatisfied, actionBlocker}

	if state.Mode == PlanExecutePlanningModeSubmitCorrection {
		contract.RuntimeCall.ExpressibleActionClasses = []string{actionRead, actionSearch, actionSubmitPlan}
		if state.SubmitCorrectionBlockerAllowed {
			contract.RuntimeCall.ExpressibleActionClasses = []string{actionRead, actionSearch, actionSubmitPlan, actionBlocker}
		}
	}

	if nativeRequired {
		contract.PhaseID = contractcompile.TurnPhaseSubmitReadyNativeRequired
		contract.Phase = "submit_ready_native"
		contract.RequiredNext = []string{"native submit_plan tool call when the gathered evidence is sufficient; permission-authorized read/search tools remain callable"}
		contract.TerminalOutcomes = workspace.PlanExecuteTerminalSubmitPlanOnly()
		contract.RuntimeCall.ToolTransportMode = firstPhaseString(contract.RuntimeCall.ToolTransportMode, "native")
		contract.RuntimeCall.ToolChoice = "auto"
		contract.RuntimeCall.ParserContract = firstPhaseString(input.ParserContract, "native_submit_plan")
		contract.RuntimeCall.ResponseFormat = ""
		contract.RuntimeCall.ExpressibleActionClasses = []string{actionRead, actionSearch, actionSubmitPlan}
	}
}

func applyMalformedSubmitPlanRecovery(contract *TurnPhaseContract, input TurnPhaseContractInput, baseTools []string) {
	applySubmitReady(contract, TurnPhaseContractInput{TextualToolParser: true, ParserContract: input.ParserContract}, baseTools, PlanExecutePlanningState{})

	contract.PhaseID = contractcompile.TurnPhaseMalformedSubmitPlanRecovery
	contract.Phase = "malformed_submit_plan_recovery"
	contract.DerivationReason = "submit_ready_parse_failure"
	contract.RequiredNext = []string{"parser-readable submit_plan using edits[] for changed paths and source_satisfied_paths for already-final paths, Decision: source-satisfied with per-path current-state evidence, or <<QUESTION>> with the exact blocker"}
	contract.RuntimeCall.ToolTransportMode = firstPhaseString(input.ToolTransportMode, "textual")
	contract.RuntimeCall.ParserContract = firstPhaseString(input.ParserContract, "submit_plan_json")
	contract.RuntimeCall.ResponseFormat = firstPhaseString(input.ResponseFormat, "json")
	contract.RuntimeCall.ExpectedVisibleOutput = "visible_text_tool_request"
	contract.RuntimeCall.ExpressibleActionClasses = []string{actionSubmitPlan, actionSourceSatisfied, actionBlocker}
}

func applySourceSatisfiedTerminal(contract *TurnPhaseContract, baseTools []string, state PlanExecutePlanningState) {
	applyPlanExecutePlanningStateProjection(contract, baseTools, state, state.CallableTools)
	contract.PhaseID = contractcompile.TurnPhaseSourceSatisfiedTerminal
	contract.Phase = "source_satisfied_terminal"
	contract.DerivationReason = "plan_execute_source_satisfied_terminal_required"
	contract.RequiredNext = []string{"visible Decision: source-satisfied with per-path current-state evidence, <<QUESTION>> with the exact blocker, or a fresh permission-authorized read/search request"}
	contract.InvalidNext = []string{"submit_plan after source-satisfied/no-op submit_plan rejection", "per-edit write in planner phase", "verify in planner phase", "prose-only final answer without Decision or <<QUESTION>>"}
	contract.TerminalOutcomes = workspace.PlanExecuteTerminalSourceSatisfiedOrQuestion()
	contract.ActionPolicy = TurnActionClassPolicy{
		AllowedActionClasses:   []string{actionRead, actionSearch, actionSourceSatisfied, actionBlocker},
		InvalidActionClasses:   []string{actionSubmitPlan, actionPerEditWrite, actionVerify, actionGenericProbe, actionProseFinal},
		TerminalResponseShapes: workspace.PlanExecuteTerminalSourceSatisfiedOrQuestion(),
		CallableToolNames:      append([]string(nil), contract.AllowedTools...),
	}

	if len(state.TerminalResponseShapes) > 0 {
		contract.ActionPolicy.TerminalResponseShapes = filterPhaseTerminalShapes(state.TerminalResponseShapes, "Decision: source-satisfied", "<<QUESTION>>:")
		contract.TerminalOutcomes = append([]string(nil), contract.ActionPolicy.TerminalResponseShapes...)
	}

	contract.RuntimeCall.ParserContract = "source_satisfied_terminal"
	contract.RuntimeCall.ResponseFormat = ""
	contract.RuntimeCall.ToolChoice = "auto"
	contract.RuntimeCall.ExpectedVisibleOutput = "visible_terminal_outcome"
	contract.RuntimeCall.ExpressibleActionClasses = []string{actionRead, actionSearch, actionSourceSatisfied, actionBlocker}
}

func applyStaleAnchorRefresh(contract *TurnPhaseContract, input TurnPhaseContractInput, baseTools []string, state PlanExecutePlanningState) {
	if state.Mode != PlanExecutePlanningModeAnchorRefresh {
		state = PlanExecutePlanningState{Mode: PlanExecutePlanningModeAnchorRefresh}
		state.applyProjection(planExecutePlanningTransportFromPhaseInput(input))
	}

	applyPlanExecutePlanningStateProjection(contract, baseTools, state, state.CallableTools)
	contract.RequiredNext = []string{"current source-body evidence remains pending for stale or missing anchors; choose any permission-authorized inspection that resolves a concrete anchor fact before a terminal planner outcome"}

	if len(state.RefreshPaths) > 0 {
		contract.RequiredNext = []string{"current anchor evidence remains pending for: " + strings.Join(nonEmptyRunnerStrings(state.RefreshPaths, 4), ", ") + "; choose any permission-authorized inspection that resolves a concrete anchor fact before submit_plan"}
	}

	contract.InvalidNext = []string{"submit_plan before bounded anchor refresh", "per-edit write in planner phase"}
	contract.StopConditions = []string{"bounded_source_refresh_returned"}

	contract.RuntimeCall.ParserContract = firstPhaseString(input.ParserContract, "source_refresh")

	contract.RuntimeCall.ExpressibleActionClasses = append([]string(nil), state.AllowedActionClasses...)
}

func applyPlanExecutePlanningStateProjection(contract *TurnPhaseContract, baseTools []string, state PlanExecutePlanningState, allowedTools []string) {
	contract.PhaseID = state.PhaseID
	contract.Phase = state.Phase
	contract.DerivationReason = state.DerivationReason
	contract.AllowedTools = workspace.FilterToolNames(baseTools, allowedTools...)
	contract.TerminalOutcomes = append([]string(nil), state.TerminalResponseShapes...)
	contract.ActionPolicy = TurnActionClassPolicy{
		AllowedActionClasses:   append([]string(nil), state.AllowedActionClasses...),
		InvalidActionClasses:   append([]string(nil), state.InvalidActionClasses...),
		TerminalResponseShapes: append([]string(nil), state.TerminalResponseShapes...),
		CallableToolNames:      workspace.FilterToolNames(baseTools, state.CallableTools...),
	}
}

func applyReviewRepairTerminal(contract *TurnPhaseContract, brief workerruntime.WorkerRuntimeBrief, baseTools []string, inspectionTools []string) {
	contract.PhaseID = contractcompile.TurnPhaseReviewRepairTerminal
	contract.Phase = "review_repair_terminal"
	contract.DerivationReason = "plan_execute_review_repair_terminal"
	contract.AllowedTools = textutil.Merge(
		workspace.FilterToolNames(baseTools, workspace.ToolNameSubmitPlan),
		workspace.FilterToolNames(baseTools, inspectionTools...),
	)
	contract.InvalidNext = []string{"verify after review-repair terminal readiness", "prose-only final answer"}

	if len(planThenExecuteReviewRepairFailedPhaseObligations(brief)) > 0 {
		contract.RequiredNext = []string{"corrective submit_plan, or <<QUESTION>> with the exact failed obligation blocker"}
		contract.InvalidNext = append(contract.InvalidNext, "Decision: pass while failed phase obligations remain")
		contract.TerminalOutcomes = []string{workspace.ToolNameSubmitPlan, "<<QUESTION>>:"}
		contract.ActionPolicy = TurnActionClassPolicy{
			AllowedActionClasses:   []string{actionRead, actionSearch, actionSubmitPlan, actionBlocker},
			InvalidActionClasses:   []string{actionReviewPass, actionVerify, actionProseFinal},
			TerminalResponseShapes: []string{workspace.ToolNameSubmitPlan, "<<QUESTION>>:"},
			CallableToolNames:      contract.AllowedTools,
		}
		contract.RuntimeCall.ExpressibleActionClasses = []string{actionRead, actionSearch, actionSubmitPlan, actionBlocker}

		return
	}

	contract.RequiredNext = []string{"Decision: pass with checklist, corrective submit_plan, or <<QUESTION>>"}
	contract.TerminalOutcomes = []string{"Decision: pass", workspace.ToolNameSubmitPlan, "<<QUESTION>>:"}
	contract.ActionPolicy = TurnActionClassPolicy{
		AllowedActionClasses:   []string{actionRead, actionSearch, actionReviewPass, actionSubmitPlan, actionBlocker},
		InvalidActionClasses:   []string{actionVerify, actionProseFinal},
		TerminalResponseShapes: []string{"Decision: pass", workspace.ToolNameSubmitPlan, "<<QUESTION>>:"},
		CallableToolNames:      contract.AllowedTools,
	}
	contract.RuntimeCall.ExpressibleActionClasses = []string{actionRead, actionSearch, actionReviewPass, actionSubmitPlan, actionBlocker}
}

func applyImplementationTargetSource(contract *TurnPhaseContract, baseTools []string, inspectionTools []string) {
	contract.PhaseID = contractcompile.TurnPhaseImplementationTargetSource
	contract.Phase = "target_source_pending"
	contract.DerivationReason = "guarded_mutation_target_source_pending"
	contract.AllowedTools = textutil.Merge(
		workspace.ToolSurfacePhaseAllowedTools(baseTools, workspace.ToolSurfacePhaseImplementationTargetSource),
		inspectionTools,
	)
	contract.RequiredNext = []string{"localize the concrete edit target, read bounded source-body evidence and git history/commit-description evidence for established logic, make one parser-readable write only after exact source-body evidence, history rationale, and replacement shape are known, write_file directly to a genuinely new (non-existent) path with full content when creating a new file (no source-body evidence, history rationale, or replacement shape is required for an additive new-file write), or <<QUESTION>> with the exact blocker/no-op evidence"}
	contract.InvalidNext = []string{"final answer before a successful write, no-op, or blocker", "write that modifies existing source before exact source-body evidence, git history rationale, and replacement shape", "verification before a successful write"}
	contract.TerminalOutcomes = []string{"<<QUESTION>>:"}
	contract.ActionPolicy = TurnActionClassPolicy{
		AllowedActionClasses:   []string{actionRead, actionSearch, actionPerEditWrite, actionBlocker},
		InvalidActionClasses:   []string{actionFinalAnswer, actionVerify, actionProseFinal},
		TerminalResponseShapes: []string{"<<QUESTION>>:"},
		CallableToolNames:      contract.AllowedTools,
	}
	contract.RuntimeCall.ParserContract = firstPhaseString(contract.RuntimeCall.ParserContract, "target_source_or_write")
	contract.RuntimeCall.ExpectedVisibleOutput = "parser_readable_tool_or_blocker"
	contract.RuntimeCall.ExpressibleActionClasses = []string{actionRead, actionSearch, actionPerEditWrite, actionBlocker}
}

func applyImplementationTargetWriteReady(contract *TurnPhaseContract, brief workerruntime.WorkerRuntimeBrief, baseTools []string, inspectionTools []string, results []workspace.ToolResult, mutationCommitted bool) {
	sourceRefresh := implementationTargetWriteReadySourceRefreshProjectionActive(brief, results, baseTools, mutationCommitted)

	contract.PhaseID = contractcompile.TurnPhaseImplementationWriteReady
	contract.Phase = "target_write_ready"
	contract.DerivationReason = "guarded_mutation_target_source_ready"
	contract.AllowedTools = textutil.Merge(
		implementationTargetWriteReadyAllowedToolsForBrief(baseTools, brief, results, mutationCommitted),
		inspectionTools,
	)
	contract.RequiredNext = []string{"git history lookup for the intended established logic if not already observed, parser-readable write to the sourced target path, bounded " + implementationTargetWriteReadySourceProbeToolsPhrase + " source refresh with named_gap for a precise edit-safety fact, or <<QUESTION>> with the exact edit blocker/no-op evidence"}
	contract.InvalidNext = []string{"verification before a successful write", "prose-only final answer"}
	contract.TerminalOutcomes = []string{"<<QUESTION>>:"}
	contract.ActionPolicy = TurnActionClassPolicy{
		AllowedActionClasses:   []string{actionRead, actionSearch, actionPerEditWrite, actionBlocker},
		InvalidActionClasses:   []string{actionVerify, actionProseFinal, actionFinalAnswer},
		TerminalResponseShapes: []string{"<<QUESTION>>:"},
		CallableToolNames:      contract.AllowedTools,
	}
	contract.RuntimeCall.ParserContract = firstPhaseString(contract.RuntimeCall.ParserContract, "target_write_or_bounded_source_refresh")
	contract.RuntimeCall.ExpectedVisibleOutput = "parser_readable_bounded_source_refresh_write_or_blocker"
	contract.RuntimeCall.ExpressibleActionClasses = []string{actionRead, actionSearch, actionPerEditWrite, actionBlocker}

	if sourceRefresh {
		contract.RequiredNext = []string{"git history lookup for the intended established logic if not already observed, parser-readable write to the sourced target path, bounded " + implementationTargetWriteReadySourceProbeToolsPhrase + " source refresh with named_gap for a precise edit-safety fact, or <<QUESTION>> with the exact edit blocker/no-op evidence"}
		contract.InvalidNext = []string{"verification before a successful write", "prose-only final answer"}
		contract.ActionPolicy.AllowedActionClasses = []string{actionRead, actionSearch, actionPerEditWrite, actionBlocker}
		contract.ActionPolicy.InvalidActionClasses = []string{actionVerify, actionProseFinal, actionFinalAnswer}
		contract.ActionPolicy.CallableToolNames = contract.AllowedTools
		contract.RuntimeCall.ParserContract = "target_write_or_bounded_source_refresh"
		contract.RuntimeCall.ExpectedVisibleOutput = "parser_readable_bounded_source_refresh_write_or_blocker"
		contract.RuntimeCall.ExpressibleActionClasses = []string{actionRead, actionSearch, actionPerEditWrite, actionBlocker}
		contract.Constraints = textutil.Unique(append([]string{"bounded_source_refresh_allowance: " + implementationTargetWriteReadySourceProbeToolsPhrase + " require named_gap and exact scope"}, contract.Constraints...))
	}

}

func applyImplementationWriteReady(contract *TurnPhaseContract, baseTools []string, inspectionTools []string) {
	contract.PhaseID = contractcompile.TurnPhaseImplementationWriteReady
	contract.Phase = "write_ready"
	contract.DerivationReason = "implementation_required_paths_remaining"
	contract.AllowedTools = textutil.Merge(
		workspace.ImplementationScopedEditAllowedToolsWithExternalReadOnlyDiscovery(baseTools, nil),
		inspectionTools,
	)
	contract.RequiredNext = []string{"git history lookup for intended established logic if missing, scoped required-path edit, or <<QUESTION>> with the exact edit blocker"}
	contract.InvalidNext = []string{"unrelated per-edit write", "write before git history rationale for existing logic"}
	contract.TerminalOutcomes = []string{"<<QUESTION>>:"}
	contract.ActionPolicy = TurnActionClassPolicy{
		AllowedActionClasses:   []string{actionRead, actionSearch, actionPerEditWrite, actionBlocker},
		TerminalResponseShapes: []string{"<<QUESTION>>:"},
		CallableToolNames:      contract.AllowedTools,
	}
	contract.RuntimeCall.ExpressibleActionClasses = []string{actionRead, actionSearch, actionPerEditWrite, actionBlocker}
}

func applyImplementationRejectedMutationRecovery(contract *TurnPhaseContract, baseTools []string, inspectionTools []string, state implementationCompletionState) {
	contract.PhaseID = contractcompile.TurnPhaseImplementationWriteReady
	contract.Phase = "write_recovery"
	contract.DerivationReason = "implementation_rejected_mutation_paths_remaining"
	contract.AllowedTools = textutil.Merge(
		workspace.ImplementationScopedEditAllowedToolsWithExternalReadOnlyDiscovery(baseTools, nil),
		inspectionTools,
	)
	contract.RequiredNext = []string{"retry the unresolved rejected mutation path, take one bounded source/history probe for an exact edit-safety fact, or report <<QUESTION>> with the exact path-specific blocker"}
	contract.InvalidNext = []string{"final answer while rejected mutation paths remain unresolved", "unrelated write", "verification before the pending mutation is resolved"}
	contract.TerminalOutcomes = []string{"<<QUESTION>>:"}
	contract.ActionPolicy = TurnActionClassPolicy{
		AllowedActionClasses:   []string{actionRead, actionSearch, actionPerEditWrite, actionBlocker},
		InvalidActionClasses:   []string{actionVerify, actionProseFinal, actionFinalAnswer},
		TerminalResponseShapes: []string{"<<QUESTION>>:"},
		CallableToolNames:      contract.AllowedTools,
	}
	contract.RuntimeCall.ParserContract = firstPhaseString(contract.RuntimeCall.ParserContract, "rejected_mutation_write_recovery")
	contract.RuntimeCall.ExpectedVisibleOutput = "parser_readable_rejected_mutation_write_or_blocker"
	contract.RuntimeCall.ExpressibleActionClasses = []string{actionRead, actionSearch, actionPerEditWrite, actionBlocker}
	if state.VerificationPending || state.SemanticVerificationPending {
		contract.AllowedTools = textutil.Merge(contract.AllowedTools, workspace.ToolSurfacePhaseAllowedTools(baseTools, workspace.ToolSurfacePhaseImplementationVerificationReady))
		contract.RequiredNext = []string{"retry the unresolved rejected mutation path, run pending verification for committed effects, take a bounded probe for an exact repair fact, or report <<QUESTION>> with the exact blocker"}
		contract.InvalidNext = []string{"final answer while rejected mutation paths or verification remain unresolved", "unrelated write"}
		contract.ActionPolicy.AllowedActionClasses = textutil.AppendUnique(contract.ActionPolicy.AllowedActionClasses, actionVerify)
		contract.ActionPolicy.InvalidActionClasses = rejectRunnerStateStrings(contract.ActionPolicy.InvalidActionClasses, func(value string) bool {
			return value == actionVerify
		})
		contract.ActionPolicy.CallableToolNames = contract.AllowedTools
		contract.RuntimeCall.ExpressibleActionClasses = textutil.AppendUnique(contract.RuntimeCall.ExpressibleActionClasses, actionVerify)
	}
	contract.Constraints = textutil.Unique(append(
		[]string{"unresolved_rejected_mutation_paths: " + strings.Join(state.UnresolvedRejectedMutationPaths, ", ")},
		contract.Constraints...,
	))
}

func applyImplementationRequiredSnippetRecovery(contract *TurnPhaseContract, baseTools []string, inspectionTools []string, state implementationCompletionState) {
	contract.PhaseID = contractcompile.TurnPhaseImplementationWriteReady
	contract.Phase = "write_recovery"
	contract.DerivationReason = "implementation_required_edit_snippets_remaining"
	contract.AllowedTools = textutil.Merge(
		workspace.ImplementationScopedEditAllowedToolsWithExternalReadOnlyDiscovery(baseTools, nil),
		inspectionTools,
	)
	contract.RequiredNext = []string{"make a source-backed write that satisfies the remaining required edit snippet, or report <<QUESTION>> with the exact snippet-specific blocker"}
	contract.InvalidNext = []string{"final answer while required edit snippets remain absent", "unrelated write", "verification before the required snippet is present"}
	contract.TerminalOutcomes = []string{"<<QUESTION>>:"}
	contract.ActionPolicy = TurnActionClassPolicy{
		AllowedActionClasses:   []string{actionRead, actionSearch, actionPerEditWrite, actionBlocker},
		InvalidActionClasses:   []string{actionVerify, actionProseFinal, actionFinalAnswer},
		TerminalResponseShapes: []string{"<<QUESTION>>:"},
		CallableToolNames:      contract.AllowedTools,
	}
	contract.RuntimeCall.ParserContract = firstPhaseString(contract.RuntimeCall.ParserContract, "required_snippet_write_recovery")
	contract.RuntimeCall.ExpectedVisibleOutput = "parser_readable_required_snippet_write_or_blocker"
	contract.RuntimeCall.ExpressibleActionClasses = []string{actionRead, actionSearch, actionPerEditWrite, actionBlocker}
	contract.Constraints = textutil.Unique(append(
		[]string{"missing_required_edit_snippets: " + strings.Join(state.MissingRequiredSnippetIDs, ", ")},
		contract.Constraints...,
	))
}

func applyImplementationTaskLedgerRecovery(contract *TurnPhaseContract, baseTools []string, state implementationCompletionState) {
	contract.PhaseID = contractcompile.TurnPhaseImplementationWriteReady
	contract.Phase = "task_ledger_recovery"
	contract.DerivationReason = "implementation_task_ledger_obligations_remaining"
	contract.AllowedTools = textutil.Unique(baseTools)
	contract.RequiredNext = []string{"continue the action-backed unfinished work and update task_ledger with evidence, or report <<QUESTION>> with the exact task-specific blocker"}
	contract.InvalidNext = []string{"successful final answer while required task-ledger items remain open", "unrelated work"}
	contract.TerminalOutcomes = []string{"<<QUESTION>>:"}
	contract.ActionPolicy = TurnActionClassPolicy{
		AllowedActionClasses:   []string{actionRead, actionSearch, actionPerEditWrite, actionVerify, actionBlocker},
		InvalidActionClasses:   []string{actionProseFinal, actionFinalAnswer},
		TerminalResponseShapes: []string{"<<QUESTION>>:"},
		CallableToolNames:      contract.AllowedTools,
	}
	contract.RuntimeCall.ExpectedVisibleOutput = "task_ledger_progress_or_blocker"
	contract.RuntimeCall.ExpressibleActionClasses = []string{actionRead, actionSearch, actionPerEditWrite, actionVerify, actionBlocker}
	contract.Constraints = textutil.Unique(append(
		[]string{"open_task_ledger_obligations: " + strings.Join(state.OpenTaskLedgerObligationIDs, ", ")},
		contract.Constraints...,
	))
}

func applyImplementationVerifyReady(contract *TurnPhaseContract, brief workerruntime.WorkerRuntimeBrief, baseTools []string, inspectionTools []string) {
	contract.PhaseID = contractcompile.TurnPhaseImplementationVerifyReady
	contract.Phase = "verification"
	contract.DerivationReason = "implementation_post_write_verification_pending"
	contract.AllowedTools = textutil.Merge(workspace.ToolSurfacePhaseAllowedTools(baseTools, workspace.ToolSurfacePhaseImplementationVerificationReady), inspectionTools)

	contract.RequiredNext = []string{"finish any remaining in-scope implementation writes, run one focused language-appropriate verification command/tool for the final changed set, use compiler-backed diagnostics, or report <<QUESTION>> with the exact verification blocker"}
	contract.InvalidNext = []string{"final answer before post-write verification", "out-of-scope or unauthorized writes"}
	contract.TerminalOutcomes = []string{"<<QUESTION>>:"}
	contract.ActionPolicy = TurnActionClassPolicy{
		AllowedActionClasses:   []string{actionRead, actionSearch, actionPerEditWrite, actionVerify, actionBlocker},
		InvalidActionClasses:   []string{actionFinalAnswer, actionGenericProbe, actionProseFinal},
		TerminalResponseShapes: []string{"<<QUESTION>>:"},
		CallableToolNames:      contract.AllowedTools,
	}
	contract.RuntimeCall.ParserContract = firstPhaseString(contract.RuntimeCall.ParserContract, "in_scope_write_or_verification_tool")
	contract.RuntimeCall.ExpressibleActionClasses = []string{actionRead, actionSearch, actionPerEditWrite, actionVerify, actionBlocker}
}

func applyImplementationVerificationRepair(contract *TurnPhaseContract, brief workerruntime.WorkerRuntimeBrief, baseTools []string, inspectionTools []string) {
	contract.PhaseID = contractcompile.TurnPhaseImplementationVerifyReady
	contract.Phase = "verification_repair"
	contract.DerivationReason = "implementation_post_write_verification_failed"
	contract.AllowedTools = textutil.Merge(
		workspace.ToolSurfacePhaseAllowedTools(baseTools, workspace.ToolSurfacePhaseImplementationVerificationRepair),
		inspectionTools,
	)
	contract.RequiredNext = []string{"repair the failing in-scope changed/required path, use permission-authorized inspection for any missing repair fact, rerun language-appropriate verification after the repair write, or <<QUESTION>> with the exact external blocker"}
	contract.InvalidNext = []string{"final answer before repair and post-repair verification"}
	contract.TerminalOutcomes = []string{"<<QUESTION>>:"}
	contract.ActionPolicy = TurnActionClassPolicy{
		AllowedActionClasses:   []string{actionPerEditWrite, actionRead, actionSearch, actionGenericProbe, actionVerify, actionBlocker},
		InvalidActionClasses:   []string{actionFinalAnswer, actionProseFinal},
		TerminalResponseShapes: []string{"<<QUESTION>>:"},
		CallableToolNames:      contract.AllowedTools,
	}
	contract.RuntimeCall.ParserContract = firstPhaseString(contract.RuntimeCall.ParserContract, "verification_repair_tool_or_blocker")
	contract.RuntimeCall.ExpressibleActionClasses = []string{actionPerEditWrite, actionRead, actionSearch, actionGenericProbe, actionVerify, actionBlocker}
}

func applyArtifactFinalAnswer(contract *TurnPhaseContract) {
	contract.PhaseID = contractcompile.TurnPhaseArtifactFinalAnswer
	contract.Phase = "artifact_final_answer"
	contract.DerivationReason = "artifact_no_tools_final_answer"
	contract.AllowedTools = nil
	contract.RequiredNext = []string{"artifact body as final answer, or <<QUESTION>> with the exact blocker"}
	contract.InvalidNext = []string{"tool call", "workspace read/search", "workspace write", "shell command", "submit_plan", "delegated task", "generic probe", "process narration before artifact"}
	contract.StopConditions = []string{"artifact_body_returned", "blocked_by_missing_information"}
	contract.TerminalOutcomes = []string{"artifact final answer", "<<QUESTION>>:"}
	contract.ActionPolicy = TurnActionClassPolicy{
		AllowedActionClasses:   []string{actionFinalAnswer, actionBlocker},
		InvalidActionClasses:   []string{actionRead, actionSearch, actionSubmitPlan, actionPerEditWrite, actionDeliverableWrite, actionVerify, actionGenericProbe},
		TerminalResponseShapes: []string{"artifact final answer", "<<QUESTION>>:"},
		CallableToolNames:      nil,
	}
	contract.RuntimeCall.ExpectedVisibleOutput = "final_answer_artifact"
	contract.RuntimeCall.ExpressibleActionClasses = []string{actionFinalAnswer, actionBlocker}
}

func applyProductAcceptanceCloseout(contract *TurnPhaseContract) {
	contract.PhaseID = contractcompile.TurnPhaseProductAcceptanceCloseout
	contract.Phase = "terminal_ready"
	contract.DerivationReason = "product_acceptance_closeout"
	contract.AllowedTools = nil
	contract.RequiredNext = []string{"final answer or <<QUESTION>> with the exact unresolved obligation"}
	contract.InvalidNext = []string{"final answer while TDD, verification, product workflow, recovery, or review obligations remain open"}
	contract.TerminalOutcomes = []string{"final answer", "<<QUESTION>>:"}
	contract.ActionPolicy = TurnActionClassPolicy{
		AllowedActionClasses:   []string{actionFinalAnswer, actionBlocker},
		InvalidActionClasses:   []string{},
		TerminalResponseShapes: []string{"final answer", "<<QUESTION>>:"},
		CallableToolNames:      nil,
	}

	if phaseEvidenceHasOpenObligation(contract.EvidenceState) {
		contract.ActionPolicy.AllowedActionClasses = []string{actionBlocker}
		contract.ActionPolicy.InvalidActionClasses = []string{actionFinalAnswer}
		contract.RuntimeCall.ExpressibleActionClasses = []string{actionBlocker}
	}

	if len(contract.RuntimeCall.ExpressibleActionClasses) == 0 {
		contract.RuntimeCall.ExpressibleActionClasses = []string{actionFinalAnswer, actionBlocker}
	}
}

func applyGenericTurnPhase(contract *TurnPhaseContract, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) {
	contract.PhaseID = contractcompile.TurnPhaseGeneric
	contract.Phase = "generic"
	contract.DerivationReason = "no_specialized_phase_matched"

	allowedClasses := genericAllowedActionClasses(contract.AllowedTools)

	contract.TerminalOutcomes = []string{"final answer", "<<QUESTION>>:"}
	contract.ActionPolicy = TurnActionClassPolicy{
		AllowedActionClasses:   allowedClasses,
		InvalidActionClasses:   nil,
		TerminalResponseShapes: []string{"final answer", "<<QUESTION>>:"},
		CallableToolNames:      contract.AllowedTools,
	}

	if phaseEvidenceHasOpenObligation(contract.EvidenceState) {
		contract.ActionPolicy.AllowedActionClasses = removePhaseActionClass(contract.ActionPolicy.AllowedActionClasses, actionFinalAnswer)
		contract.ActionPolicy.InvalidActionClasses = append(contract.ActionPolicy.InvalidActionClasses, actionFinalAnswer)
	}

	if brief.Readiness != nil && brief.Readiness.ReadyToVerify {
		contract.ActionPolicy.AllowedActionClasses = []string{actionVerify, actionBlocker}
		contract.ActionPolicy.InvalidActionClasses = []string{actionFinalAnswer}
	}

	if genericReadOnlyEvidenceReady(brief, results) {
		contract.Phase = "read_only_evidence_available"
		contract.DerivationReason = "read_only_workspace_evidence_available"
		contract.AllowedTools = append([]string(nil), contract.AllowedTools...)
		if !slices.Contains(contract.AllowedTools, workspace.ToolNameTask) {
			contract.AllowedTools = append(contract.AllowedTools, workspace.ToolNameTask)
		}
		contract.RequiredNext = []string{"final answer from the gathered evidence, one bounded proof read for an exact unsupported claim, or <<QUESTION>> with the exact blocker"}
		if summary := readOnlySourceBodyEvidenceSummary(results); summary != "" {
			contract.RequiredNext = append(contract.RequiredNext, "", summary)
		}
		contract.InvalidNext = []string{"workspace write", "shell/mutating command", "unsupported factual final answer"}
		contract.ActionPolicy = TurnActionClassPolicy{
			AllowedActionClasses:   []string{actionFinalAnswer, actionBlocker, actionGenericProbe, actionRead, actionSearch},
			InvalidActionClasses:   []string{actionPerEditWrite, actionVerify},
			TerminalResponseShapes: []string{"final answer", "<<QUESTION>>:"},
			CallableToolNames:      contract.AllowedTools,
		}
		contract.RuntimeCall.ExpressibleActionClasses = append([]string(nil), contract.ActionPolicy.AllowedActionClasses...)

		return
	}

	if genericReadOnlyEvidencePending(brief, contract.AllowedTools, results) {
		contract.Phase = "read_only_evidence_pending"
		contract.DerivationReason = "read_only_workspace_evidence_pending"
		contract.AllowedTools = genericReadOnlyEvidenceTools(contract.AllowedTools)

		if readOnlyDirectAnswerLane(brief) {
			contract.RequiredNext = []string{"one bounded read/search for an exact unsupported claim, or <<QUESTION>> with the exact blocker; answer only if the gathered evidence supports every material claim or explicitly marks unsupported claims as unverified"}
			contract.InvalidNext = []string{"workspace write", "shell/mutating command", "unsupported factual final answer"}
			contract.TerminalOutcomes = []string{"final answer", "<<QUESTION>>:"}
			contract.ActionPolicy = TurnActionClassPolicy{
				AllowedActionClasses:   textutil.Unique([]string{actionFinalAnswer, actionRead, actionSearch, actionBlocker}),
				InvalidActionClasses:   []string{actionPerEditWrite, actionVerify},
				TerminalResponseShapes: []string{"final answer", "<<QUESTION>>:"},
				CallableToolNames:      contract.AllowedTools,
			}
		} else {
			contract.RequiredNext = []string{"bounded read/search evidence for the question, or <<QUESTION>> with the exact blocker"}
			contract.InvalidNext = []string{"workspace write", "shell/mutating command", "final answer before sufficient evidence"}
			contract.TerminalOutcomes = []string{"<<QUESTION>>:"}
			contract.ActionPolicy = TurnActionClassPolicy{
				AllowedActionClasses:   []string{actionRead, actionSearch, actionBlocker},
				InvalidActionClasses:   []string{actionPerEditWrite, actionVerify, actionFinalAnswer},
				TerminalResponseShapes: []string{"<<QUESTION>>:"},
				CallableToolNames:      contract.AllowedTools,
			}
		}
	}

	contract.RuntimeCall.ExpressibleActionClasses = append([]string(nil), contract.ActionPolicy.AllowedActionClasses...)
}

func genericAllowedActionClasses(tools []string) []string {
	classes := []string{actionFinalAnswer, actionBlocker}

	for _, tool := range tools {
		if actionClass := autonomy.ActionClassToken(workspace.ToolActionClass(tool)); actionClass != "" {
			classes = append(classes, actionClass)
		}
	}

	return textutil.Unique(classes)
}

func pruneToolActionClassesWithoutCallableTool(classes []string, callableTools []string) []string {
	if len(classes) == 0 {
		return nil
	}

	callableClasses := make(map[string]struct{})

	for _, tool := range callableTools {
		if actionClass := autonomy.ActionClassToken(workspace.ToolActionClass(tool)); actionClass != "" {
			callableClasses[actionClass] = struct{}{}
		}
	}

	var out []string

	for _, class := range classes {
		class = strings.TrimSpace(class)
		if class == "" {
			continue
		}

		if isToolActionClass(class) {
			if _, ok := callableClasses[class]; !ok {
				continue
			}
		}

		out = append(out, class)
	}

	return textutil.Unique(out)
}

func removePhaseToolsForInvalidActionClasses(tools []string, invalidClasses []string) []string {
	if len(tools) == 0 || len(invalidClasses) == 0 {
		return tools
	}

	invalid := make(map[string]struct{}, len(invalidClasses))
	for _, class := range invalidClasses {
		if class = strings.TrimSpace(class); class != "" {
			invalid[class] = struct{}{}
		}
	}

	var out []string
	for _, tool := range tools {
		tool = strings.TrimSpace(tool)
		if tool == "" {
			continue
		}
		if class := autonomy.ActionClassToken(workspace.ToolActionClass(tool)); class != "" {
			if _, denied := invalid[class]; denied {
				continue
			}
		}
		out = append(out, tool)
	}
	return textutil.Unique(out)
}

func isToolActionClass(class string) bool {
	return autonomy.IsToolActionClass(autonomy.ActionClass(strings.TrimSpace(class)))
}

func removePhaseActionClass(classes []string, remove string) []string {
	var out []string

	for _, class := range classes {
		if strings.TrimSpace(class) == remove {
			continue
		}

		out = append(out, class)
	}

	return textutil.Unique(out)
}

func removePhaseTerminalShape(shapes []string, remove string) []string {
	var out []string

	for _, shape := range shapes {
		if strings.TrimSpace(shape) == remove {
			continue
		}

		out = append(out, shape)
	}

	return textutil.Unique(out)
}

func readOnlySourceBodyEvidenceSummary(results []workspace.ToolResult) string {
	bodyPaths := newToolResultEvidenceProjection(results).SourceWindowPaths()
	candidatePaths := newToolResultEvidenceProjection(results).CandidatePaths()

	var b strings.Builder

	if len(bodyPaths) > 0 {
		b.WriteString("Source-body evidence available for: ")
		b.WriteString(strings.Join(bodyPaths, ", "))
	}

	if len(candidatePaths) > 0 {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString("Candidate paths (not yet read as body): ")
		b.WriteString(strings.Join(candidatePaths, ", "))
	}

	if b.Len() > 0 {
		b.WriteString("\n")
		b.WriteString("When answering, cite only the body-backed paths above, or qualify unread paths as unverified.")
		return b.String()
	}

	return ""
}

func genericReadOnlyEvidenceReady(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if !workerruntime.PermissionProfileIsReadOnlyWorkspace(brief.PermissionProfile) {
		return false
	}

	if slices.Contains(brief.Constraints, "answer_readiness_reached") {
		return true
	}

	if readOnlyDirectAnswerLane(brief) {
		return conversationEvidenceSatisfied(brief, results)
	}

	return genericReadOnlyAnswerEvidenceReady(brief, results)
}

func genericReadOnlyEvidencePending(brief workerruntime.WorkerRuntimeBrief, allowedTools []string, results []workspace.ToolResult) bool {
	if !workerruntime.PermissionProfileIsReadOnlyWorkspace(brief.PermissionProfile) {
		return false
	}

	isReadOnlyConversation := readOnlyDirectAnswerLane(brief)
	if brief.Readiness == nil && !isReadOnlyConversation {
		return false
	}

	if brief.Readiness != nil && brief.Readiness.ReadyToAnswer {
		return false
	}

	if isReadOnlyConversation {
		if genericReadOnlyEvidenceReady(brief, results) {
			return false
		}
	} else if genericReadOnlyAnswerEvidenceReady(brief, results) {
		return false
	}

	return len(genericReadOnlyEvidenceTools(allowedTools)) > 0
}

func genericReadOnlyAnswerEvidenceReady(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if workerruntime.TaskFamilyMatches(brief.TaskFamily, autonomy.TaskFamilyDirectAnswer) &&
		workerruntime.RuntimeContractIsDirectAnswer(brief.Contract) &&
		strings.TrimSpace(brief.Objective) != "" {

		brief = readOnlyDirectAnswerAcceptanceLedgerBrief(brief)
	}

	return readOnlyClosureDecisionForResults(brief, results).Answerable()
}

func genericReadOnlyEvidenceTools(tools []string) []string {
	return workspace.FilterToolNames(tools, workspace.RuntimeReadOnlyInvestigationToolNames()...)
}

func artifactFinalAnswerBrief(brief workerruntime.WorkerRuntimeBrief) bool {
	return strings.TrimSpace(brief.TaskFamily) == workerruntime.ArtifactFinalAnswerTaskFamily ||
		strings.TrimSpace(brief.Contract) == "artifact_final_answer"
}

func implementationTargetSourcePending(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, mutationCommitted bool) bool {
	if strings.TrimSpace(brief.TaskFamily) == PlanThenExecutePlannerTaskFamily {
		return false
	}

	if !guardedMutationBriefRequiresTargetSourceContract(brief) {
		return false
	}

	if len(brief.RequiredEditPaths) > 0 || len(brief.RequiredEditSnippets) > 0 {
		return false
	}

	if mutationCommitted {
		return false
	}

	if implementationTargetSourceEvidenceReady(brief, results) {
		return false
	}

	return brief.Readiness == nil || !brief.Readiness.ReadyToWrite
}

func implementationTargetWriteReadyAllowedToolsForBrief(baseTools []string, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, mutationCommitted bool) []string {
	allowed := workspace.ToolSurfacePhaseAllowedTools(baseTools, workspace.ToolSurfacePhaseImplementationTargetWriteReady)

	if implementationTargetWriteReadySourceRefreshProjectionActive(brief, results, baseTools, mutationCommitted) {
		allowed = append(allowed, workspace.ImplementationTargetWriteReadySourceRefreshTools(baseTools)...)
	}

	return textutil.Unique(allowed)
}

func implementationTargetWriteReadySourceRefreshProjectionActive(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, baseTools []string, mutationCommitted bool) bool {
	return implementationTargetWriteReadySurfaceActive(brief, results, mutationCommitted) && len(workspace.ImplementationTargetWriteReadySourceRefreshTools(baseTools)) > 0
}

func implementationTargetWriteReadySurfaceActive(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, mutationCommitted bool) bool {
	if strings.TrimSpace(brief.TaskFamily) == PlanThenExecutePlannerTaskFamily {
		return false
	}

	if !guardedMutationBriefRequiresTargetSourceContract(brief) {
		return false
	}

	if len(brief.RequiredEditPaths) > 0 || len(brief.RequiredEditSnippets) > 0 {
		return false
	}

	if mutationCommitted {
		return false
	}

	return implementationTargetSourceEvidenceReady(brief, results)
}

func implementationTargetSourceEvidenceReady(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	for _, path := range newToolResultEvidenceProjection(results).SourceWindowPaths() {
		lower := strings.ToLower(path)
		if strings.HasSuffix(lower, ".md") || strings.HasSuffix(lower, ".mdx") {
			continue
		}

		if implementationTargetSourceEvidencePathPlausible(brief, path) {
			return true
		}
	}

	return false
}

func implementationTargetSourceEvidencePathPlausible(brief workerruntime.WorkerRuntimeBrief, path string) bool {
	path = NormalizeRunnerPath(path)
	if path == "" {
		return false
	}

	if implementationTargetSourceExplicitPathMatch(brief, path) {
		return true
	}

	if implementationTargetPacketEditablePathMatch(brief, path) {
		return true
	}

	if !implementationTargetSourceHasScopeHints(brief) {
		return true
	}

	return implementationSourcePathOverlapsObjective(path, brief.Objective)
}

func implementationTargetSourceHasScopeHints(brief workerruntime.WorkerRuntimeBrief) bool {
	return len(brief.TargetPacket) > 0 ||
		len(brief.TargetRefs) > 0 ||
		len(brief.EvidencePaths) > 0 ||
		len(brief.UserBoundaries) > 0
}

func implementationTargetSourceExplicitPathMatch(brief workerruntime.WorkerRuntimeBrief, path string) bool {
	path = NormalizeRunnerPath(path)
	if path == "" {
		return false
	}

	candidates := append([]string{}, brief.RequiredEditPaths...)

	candidates = append(candidates, brief.TargetRefs...)
	candidates = append(candidates, brief.EvidencePaths...)
	candidates = append(candidates, pathparse.MentionedWorkspacePaths(strings.Join(brief.UserBoundaries, "\n"))...)
	candidates = append(candidates, pathparse.MentionedWorkspacePaths(brief.Objective)...)

	if targetPacketSupportsWrite(brief) {
		candidates = append(candidates, targetPacketWritableMentionedPaths(brief.TargetPacket)...)
	}

	for _, candidate := range candidates {
		if NormalizeRunnerPath(candidate) == path {
			return true
		}
	}

	return false
}

func implementationTargetPacketEditablePathMatch(brief workerruntime.WorkerRuntimeBrief, path string) bool {
	path = NormalizeRunnerPath(path)
	if path == "" {
		return false
	}

	for _, candidate := range targetPacketWritableMentionedPaths(brief.TargetPacket) {
		if NormalizeRunnerPath(candidate) == path {
			return true
		}
	}

	return false
}

func targetPacketWritableMentionedPaths(lines []string) []string {
	var paths []string

	for _, line := range lines {
		normalizedLine := strings.ToLower(strings.TrimSpace(line))
		switch {
		case strings.Contains(normalizedLine, "read_only_evidence"),
			strings.Contains(normalizedLine, "weak packet candidate"),
			strings.Contains(normalizedLine, "forbidden"),
			strings.Contains(normalizedLine, "protected"):
			continue
		case strings.Contains(normalizedLine, "editable"),
			strings.Contains(normalizedLine, "candidate file:"):
			paths = append(paths, pathparse.MentionedWorkspacePaths(line)...)
		}
	}

	return textutil.Unique(paths)
}

func implementationPathTerms(path string) []string {
	path = strings.TrimSuffix(NormalizeRunnerPath(path), ".go")

	parts := strings.FieldsFunc(path, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9')
	})
	seen := map[string]struct{}{}

	var terms []string

	for _, part := range parts {
		for _, token := range splitImplementationPathToken(part) {
			token = SingularObjectiveWord(token)
			if len(token) < 4 {
				continue
			}

			switch token {
			case "internal", "pkg", "cmd", "test", "tests":
				continue
			}

			if _, ok := seen[token]; ok {
				continue
			}

			seen[token] = struct{}{}
			terms = append(terms, token)
		}
	}

	return terms
}

func guardedMutationBriefRequiresTargetSourceContract(brief workerruntime.WorkerRuntimeBrief) bool {
	if workerruntime.TaskFamilyMatches(brief.TaskFamily, autonomy.TaskFamilyDelegatedIsolatedImpl) {
		return false
	}

	permission := strings.ToLower(strings.TrimSpace(brief.PermissionProfile))

	return workerruntime.PermissionProfileMatches(permission, workerruntime.PermissionGuardedWorkspaceWrite) ||
		permission == "workspace_write" ||
		permission == "write_enabled" ||
		workerruntime.TaskFamilyMatches(brief.TaskFamily, autonomy.TaskFamilyImplementation)
}

func phaseContractBaseTools(input TurnPhaseContractInput, brief workerruntime.WorkerRuntimeBrief) []string {
	if len(input.BaseAllowedTools) > 0 {
		return textutil.Unique(input.BaseAllowedTools)
	}

	return textutil.Unique(brief.AllowedTools)
}

func filterPhaseTerminalShapes(values []string, allowed ...string) []string {
	if len(values) == 0 || len(allowed) == 0 {
		return nil
	}

	allowedSet := make(map[string]struct{}, len(allowed))
	for _, value := range allowed {
		value = strings.TrimSpace(value)
		if value != "" {
			allowedSet[value] = struct{}{}
		}
	}

	var out []string

	for _, value := range values {
		value = strings.TrimSpace(value)
		if _, ok := allowedSet[value]; ok {
			out = append(out, value)
		}
	}

	return textutil.Unique(out)
}

func removePhaseTools(tools []string, hidden []string) []string {
	if len(tools) == 0 || len(hidden) == 0 {
		return tools
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

	return textutil.Unique(out)
}

func hiddenPhaseTools(baseTools []string, allowedTools []string) []string {
	allowed := make(map[string]struct{}, len(allowedTools))
	for _, tool := range allowedTools {
		tool = strings.TrimSpace(tool)
		if tool != "" {
			allowed[tool] = struct{}{}
		}
	}

	var hidden []string

	for _, tool := range baseTools {
		tool = strings.TrimSpace(tool)
		if tool == "" {
			continue
		}

		if _, ok := allowed[tool]; !ok {
			hidden = append(hidden, tool)
		}
	}

	return hidden
}

func firstPhaseString(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}

	return ""
}

func expectedVisibleOutput(input TurnPhaseContractInput) string {
	switch {
	case input.SupportsNativeTools && !input.TextualToolParser:
		return "native_tool_call"
	case input.TextualToolParser || textutil.EqualTrimFold(input.ToolTransportMode, "textual"):
		return "visible_text_tool_request"
	default:
		return ""
	}
}

func phaseContractTextualOnly(input TurnPhaseContractInput, contract TurnPhaseContract) bool {
	if !input.TextualToolParser && !textutil.EqualTrimFold(input.ToolTransportMode, "textual") {
		return false
	}

	if phaseContractKeepsTextualCallableTools(input, contract) {
		return false
	}

	return contract.PhaseID != ""
}

func phaseContractKeepsTextualCallableTools(input TurnPhaseContractInput, contract TurnPhaseContract) bool {
	switch contractcompile.TurnPhaseID(strings.TrimSpace(string(contract.PhaseID))) {
	case contractcompile.TurnPhaseHistoryPending, contractcompile.TurnPhaseMalformedSubmitPlanRecovery:
		return true
	}

	if !workerruntime.PermissionProfileIsReadOnlyWorkspace(contract.PermissionProfile) {
		return false
	}

	if !workerruntime.RuntimeContractIsDirectAnswer(input.Brief.Contract) {
		return false
	}

	if !readOnlyDirectAnswerTaskFamily(input.Brief.TaskFamily) {
		return false
	}

	switch strings.TrimSpace(contract.Phase) {
	case "generic", "read_only_evidence_pending", "read_only_evidence_available":
		return true
	default:
		return false
	}
}

func phaseEvidenceHasOpenObligation(state PhaseEvidenceState) bool {
	return autonomy.PhaseEvidenceStateHasOpenObligation(state.autonomyEvent())
}

type PlanExecutePlanningMode string

const (
	PlanExecutePlanningModeNone             PlanExecutePlanningMode = ""
	PlanExecutePlanningModeRequiredSource   PlanExecutePlanningMode = "required_source"
	PlanExecutePlanningModeOpenCandidate    PlanExecutePlanningMode = "open_candidate"
	PlanExecutePlanningModeAllowedCandidate PlanExecutePlanningMode = "allowed_candidate"
	PlanExecutePlanningModeHistoryPending   PlanExecutePlanningMode = "history_pending"
	PlanExecutePlanningModeAnchorRefresh    PlanExecutePlanningMode = "anchor_refresh"
	PlanExecutePlanningModeSubmitReady      PlanExecutePlanningMode = "submit_ready"
	PlanExecutePlanningModeSubmitCorrection PlanExecutePlanningMode = "submit_correction"
	PlanExecutePlanningModeReviewRepair     PlanExecutePlanningMode = "review_repair"
	PlanExecutePlanningModeDone             PlanExecutePlanningMode = "done"
)

type PlanExecuteRecoverySurfaceKind string

const (
	PlanExecuteRecoverySurfaceNone                    PlanExecuteRecoverySurfaceKind = ""
	PlanExecuteRecoverySurfaceAnchorRefresh           PlanExecuteRecoverySurfaceKind = "anchor_refresh"
	PlanExecuteRecoverySurfaceHistoryPending          PlanExecuteRecoverySurfaceKind = "history_pending"
	PlanExecuteRecoverySurfaceSubmitCorrection        PlanExecuteRecoverySurfaceKind = "submit_correction"
	PlanExecuteRecoverySurfaceSubmitReady             PlanExecuteRecoverySurfaceKind = "submit_ready"
	PlanExecuteRecoverySurfaceSourceSatisfiedTerminal PlanExecuteRecoverySurfaceKind = "source_satisfied_terminal"
)

type ToolCallTransportMode string

const (
	ToolCallTransportModeTextual     ToolCallTransportMode = "textual_tools"
	ToolCallTransportModeTextualJSON ToolCallTransportMode = "textual_json_terminal"
	ToolCallTransportModeNative      ToolCallTransportMode = "native_tools"
)

type PlanExecutePlanningTransport struct {
	SupportsNativeTools bool
	ForceNativeTools    bool
	TextualToolParser   bool
	TerminalOnly        bool
	ResponseFormatJSON  bool
}

type PlanExecutePlanningState struct {
	Mode                           PlanExecutePlanningMode
	PhaseID                        contractcompile.TurnPhaseID
	Phase                          string
	DerivationReason               string
	RequiredCoveragePaths          []string
	RequiredPathStates             []string
	AllowedCandidatePaths          []string
	EvidencePaths                  []string
	SelectedEditPaths              []string
	RefreshPaths                   []string
	TerminalOptions                []string
	ParserVisibleTools             []string
	CallableTools                  []string
	AllowedActionClasses           []string
	InvalidActionClasses           []string
	TerminalResponseShapes         []string
	TransportMode                  ToolCallTransportMode
	LastInvalidPaths               []string
	SubmitCorrectionBlockerAllowed bool
}

const planExecuteRecoveryStateSchemaVersion = "idiom.plan_execute_recovery_state.v1"

type PlanExecuteRecoveryState struct {
	SchemaVersion                    string                         `json:"schema_version,omitempty"`
	Surface                          PlanExecuteRecoverySurfaceKind `json:"surface,omitempty"`
	Mode                             PlanExecutePlanningMode        `json:"mode,omitempty"`
	ContractID                       string                         `json:"contract_id,omitempty"`
	PhaseID                          contractcompile.TurnPhaseID    `json:"phase_id,omitempty"`
	RecoveryID                       string                         `json:"recovery_id,omitempty"`
	RecoveryCardProtocolProjectionID string                         `json:"recovery_card_protocol_projection_id,omitempty"`
	FailedInvariantID                string                         `json:"failed_invariant_id,omitempty"`
	ActionClass                      string                         `json:"action_class,omitempty"`
	RejectedSubject                  string                         `json:"rejected_subject,omitempty"`
	TerminalBlockerAllowed           bool                           `json:"terminal_blocker_allowed,omitempty"`
	VisibleSafeAlternatives          []string                       `json:"visible_safe_alternatives,omitempty"`
}

type PlanExecuteRecoverySurface struct {
	Kind  PlanExecuteRecoverySurfaceKind
	State PlanExecutePlanningState
}

type planExecuteRecoverySurfaceSpec struct {
	mode              PlanExecutePlanningMode
	phaseID           contractcompile.TurnPhaseID
	failedInvariantID string
}

var planExecuteRecoverySurfaceSpecs = map[PlanExecuteRecoverySurfaceKind]planExecuteRecoverySurfaceSpec{
	PlanExecuteRecoverySurfaceAnchorRefresh: {
		mode:              PlanExecutePlanningModeAnchorRefresh,
		phaseID:           contractcompile.TurnPhaseStaleAnchorRefresh,
		failedInvariantID: "planexecute.anchor_refresh",
	},
	PlanExecuteRecoverySurfaceHistoryPending: {
		mode:              PlanExecutePlanningModeHistoryPending,
		phaseID:           contractcompile.TurnPhaseHistoryPending,
		failedInvariantID: "planexecute.history_required",
	},
	PlanExecuteRecoverySurfaceSubmitCorrection: {
		mode:              PlanExecutePlanningModeSubmitCorrection,
		phaseID:           contractcompile.TurnPhaseSubmitReadyTextual,
		failedInvariantID: "planexecute.submit_plan",
	},
	PlanExecuteRecoverySurfaceSubmitReady: {
		mode:              PlanExecutePlanningModeSubmitReady,
		phaseID:           contractcompile.TurnPhaseSubmitReadyTextual,
		failedInvariantID: "planexecute.submit_ready",
	},
	PlanExecuteRecoverySurfaceSourceSatisfiedTerminal: {
		mode:              PlanExecutePlanningModeSubmitReady,
		phaseID:           contractcompile.TurnPhaseSourceSatisfiedTerminal,
		failedInvariantID: "planexecute.source_satisfied_terminal",
	},
}

func planExecuteRecoverySurfaceSpecFor(kind PlanExecuteRecoverySurfaceKind) planExecuteRecoverySurfaceSpec {
	if spec, ok := planExecuteRecoverySurfaceSpecs[kind]; ok {
		return spec
	}
	return planExecuteRecoverySurfaceSpec{
		mode:              PlanExecutePlanningModeNone,
		phaseID:           contractcompile.TurnPhaseGeneric,
		failedInvariantID: "planexecute.recovery",
	}
}

type planExecuteRecoveryPhaseSurfaceOverride struct {
	phaseID                          contractcompile.TurnPhaseID
	kind                             PlanExecuteRecoverySurfaceKind
	preserveSubmitCorrectionFallback bool
}

var planExecuteRecoveryPhaseSurfaceOverrides = []planExecuteRecoveryPhaseSurfaceOverride{
	{phaseID: contractcompile.TurnPhaseStaleAnchorRefresh, kind: PlanExecuteRecoverySurfaceAnchorRefresh},
	{phaseID: contractcompile.TurnPhaseHistoryPending, kind: PlanExecuteRecoverySurfaceHistoryPending},
	{phaseID: contractcompile.TurnPhaseSourceSatisfiedTerminal, kind: PlanExecuteRecoverySurfaceSourceSatisfiedTerminal},
	{phaseID: contractcompile.TurnPhaseMalformedSubmitPlanRecovery, kind: PlanExecuteRecoverySurfaceSubmitReady},
	{phaseID: contractcompile.TurnPhaseSubmitReadyNativeRequired, kind: PlanExecuteRecoverySurfaceSubmitReady, preserveSubmitCorrectionFallback: true},
	{phaseID: contractcompile.TurnPhaseSubmitReadyTextual, kind: PlanExecuteRecoverySurfaceSubmitReady, preserveSubmitCorrectionFallback: true},
}

func derivePlanExecuteRecoverySurface(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, transport PlanExecutePlanningTransport, authority planThenExecutePathAuthority) PlanExecuteRecoverySurface {
	state := derivePlanExecutePlanningState(brief, results, transport, authority)

	return PlanExecuteRecoverySurface{
		Kind:  planExecuteRecoverySurfaceKind(brief, results, state, authority),
		State: state,
	}
}

func planExecuteRecoverySurfaceKind(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, state PlanExecutePlanningState, authority planThenExecutePathAuthority) PlanExecuteRecoverySurfaceKind {
	if !planThenExecutePlannerBrief(brief) {
		return PlanExecuteRecoverySurfaceNone
	}

	if state.Mode == PlanExecutePlanningModeAnchorRefresh && len(state.RefreshPaths) > 0 {
		return PlanExecuteRecoverySurfaceAnchorRefresh
	}

	if state.Mode == PlanExecutePlanningModeHistoryPending {
		return PlanExecuteRecoverySurfaceHistoryPending
	}

	if planExecuteSourceSatisfiedTerminalEvidenceComplete(brief, results, state, authority) {
		return PlanExecuteRecoverySurfaceSourceSatisfiedTerminal
	}

	if state.Mode == PlanExecutePlanningModeSubmitCorrection {
		return PlanExecuteRecoverySurfaceSubmitCorrection
	}

	if state.Mode == PlanExecutePlanningModeSubmitReady || planThenExecuteTerminalSubmitPlanAllowed(brief, results, authority) {
		return PlanExecuteRecoverySurfaceSubmitReady
	}

	return PlanExecuteRecoverySurfaceNone
}

func derivePlanExecuteRecoveryState(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, transport PlanExecutePlanningTransport, contract TurnPhaseContract, req workspace.ToolRequest, rejected workspace.ToolResult, authority planThenExecutePathAuthority) (PlanExecuteRecoveryState, bool) {
	if !planThenExecutePlannerBrief(brief) || !planExecuteRecoveryRejectedResult(req, rejected) {
		return PlanExecuteRecoveryState{}, false
	}

	surface := derivePlanExecuteRecoverySurface(brief, results, transport, authority)
	kind := recoverySurfaceKindFromContract(contract, surface.Kind)
	if kind == PlanExecuteRecoverySurfaceNone {
		return PlanExecuteRecoveryState{}, false
	}

	mode := surface.State.Mode
	if mode == PlanExecutePlanningModeNone {
		mode = recoveryModeFromSurface(kind)
	}

	actionClass := recoveryCardRejectedActionClass(req, rejected)
	if recoveryRejectedResultIsSubmitPlan(req, rejected, contract) {
		actionClass = actionSubmitPlan
	}

	subject := firstPhaseString(
		workerruntime.ToolRequestTarget(req),
		firstRecoveryStatePath(surface.State.RefreshPaths),
		firstRecoveryStatePath(surface.State.SelectedEditPaths),
		firstRecoveryStatePath(surface.State.RequiredCoveragePaths),
	)

	state := PlanExecuteRecoveryState{
		SchemaVersion:           planExecuteRecoveryStateSchemaVersion,
		Surface:                 kind,
		Mode:                    mode,
		ContractID:              strings.TrimSpace(contract.ContractID),
		PhaseID:                 contractcompile.TurnPhaseID(strings.TrimSpace(string(contract.PhaseID))),
		FailedInvariantID:       recoveryStateFailedInvariantID(kind, contract),
		ActionClass:             actionClass,
		RejectedSubject:         subject,
		TerminalBlockerAllowed:  recoveryCardBlockerAllowed(contract),
		VisibleSafeAlternatives: recoveryStateVisibleSafeAlternatives(contract),
	}
	if state.ContractID == "" {
		state.ContractID = strings.TrimSpace(brief.Contract)
	}
	if state.PhaseID == "" {
		state.PhaseID = recoveryPhaseIDFromSurface(kind)
	}
	state.RecoveryID = recoveryStateID(state.PhaseID, subject, kind)
	state.RecoveryCardProtocolProjectionID = recoveryStateProjectionID(state.ContractID, state.PhaseID, state.RecoveryID)

	return state, true
}

func recoverySurfaceKindFromContract(contract TurnPhaseContract, fallback PlanExecuteRecoverySurfaceKind) PlanExecuteRecoverySurfaceKind {
	for _, override := range planExecuteRecoveryPhaseSurfaceOverrides {
		if contract.PhaseID != override.phaseID {
			continue
		}
		if override.preserveSubmitCorrectionFallback && fallback == PlanExecuteRecoverySurfaceSubmitCorrection {
			return fallback
		}
		return override.kind
	}
	return fallback
}

func recoveryModeFromSurface(kind PlanExecuteRecoverySurfaceKind) PlanExecutePlanningMode {
	return planExecuteRecoverySurfaceSpecFor(kind).mode
}

func planExecuteRecoveryRejectedResult(req workspace.ToolRequest, result workspace.ToolResult) bool {
	if strings.TrimSpace(result.Tool) == "" && strings.TrimSpace(req.Tool) == "" {
		return false
	}
	if strings.TrimSpace(result.Error) != "" || strings.TrimSpace(result.Code) != "" {
		return true
	}
	if result.Facts == nil {
		return false
	}
	if result.Facts.RejectionDisposition != "" || len(result.Facts.SubmitPlanDiagnostics) > 0 {
		return true
	}
	_, hasRecoveryEnvelope := completionGateRecoveryEnvelopeFromResult(result)
	return hasRecoveryEnvelope
}

func recoveryRejectedResultIsSubmitPlan(req workspace.ToolRequest, result workspace.ToolResult, contracts ...TurnPhaseContract) bool {
	requestTool := strings.TrimSpace(req.Tool)
	resultTool := strings.TrimSpace(result.Tool)
	if requestTool == workspace.ToolNameSubmitPlan || resultTool == workspace.ToolNameSubmitPlan {
		return true
	}
	if strings.HasPrefix(strings.TrimSpace(result.Code), workspace.ToolNameSubmitPlan+".") {
		return true
	}
	if requestTool != parserRecoveryToolName && resultTool != parserRecoveryToolName {
		return false
	}
	for _, contract := range contracts {
		if contract.PhaseID == contractcompile.TurnPhaseMalformedSubmitPlanRecovery {
			return true
		}
	}
	return false
}

func firstRecoveryStatePath(paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	return strings.TrimSpace(paths[0])
}

func recoveryStateFailedInvariantID(kind PlanExecuteRecoverySurfaceKind, contract TurnPhaseContract) string {
	if len(contract.InvariantIDs) > 0 {
		return contract.InvariantIDs[0]
	}
	return planExecuteRecoverySurfaceSpecFor(kind).failedInvariantID
}

func recoveryStateVisibleSafeAlternatives(contract TurnPhaseContract) []string {
	return recoverySafeActionAlternatives(recoveryCardCallableActionClasses(contract, contract.ActionPolicy.CallableToolNames))
}

func recoverySafeActionAlternatives(classes []string) []string {
	var out []string
	for _, class := range classes {
		switch class {
		case actionSubmitPlan, actionSourceSatisfied, actionBlocker, actionRead, actionSearch, actionPerEditWrite:
			out = append(out, class)
		}
	}
	return textutil.Unique(out)
}

func recoveryPhaseIDFromSurface(kind PlanExecuteRecoverySurfaceKind) contractcompile.TurnPhaseID {
	return planExecuteRecoverySurfaceSpecFor(kind).phaseID
}

func recoveryStateID(phaseID contractcompile.TurnPhaseID, subject string, kind PlanExecuteRecoverySurfaceKind) string {
	parts := []string{"planexecute_recovery", string(kind), string(phaseID), subject}
	return strings.Join(nonEmptyRecoveryStateIDParts(parts), ":")
}

func recoveryStateProjectionID(contractID string, phaseID contractcompile.TurnPhaseID, recoveryID string) string {
	return strings.Join(nonEmptyRecoveryStateIDParts([]string{"recovery_card", contractID, string(phaseID), recoveryID}), ":")
}

func nonEmptyRecoveryStateIDParts(parts []string) []string {
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		part = strings.ReplaceAll(part, "`", "")
		part = strings.ReplaceAll(part, " ", "_")
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func planExecuteSourceSatisfiedTerminalEvidenceComplete(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, state PlanExecutePlanningState, authority planThenExecutePathAuthority) bool {
	if !planThenExecuteSourceSatisfiedTerminalRequiredActive(results) || len(state.RefreshPaths) > 0 {
		return false
	}

	if len(state.RequiredCoveragePaths) > 0 {
		return len(planThenExecuteUnreadRequiredPaths(brief, results, authority)) == 0
	}

	return len(state.EvidencePaths) > 0 || len(state.SelectedEditPaths) > 0
}

func (r *runExecution) derivePlanExecutePlanningState(results []workspace.ToolResult, transport PlanExecutePlanningTransport) PlanExecutePlanningState {
	if r == nil {
		return PlanExecutePlanningState{}
	}
	state := derivePlanExecutePlanningState(r.state.runtimeBrief, results, transport, r.planThenExecutePathAuthority())
	if planExecuteInspectionAdditiveBrief(r.state.runtimeBrief) {
		state.addPermissionAuthorizedInspection(r.permissionAuthorizedInspection(r.workspaceAdvertisedAllowedTools()).ToolNames())
	}
	return state
}

func (state *PlanExecutePlanningState) addPermissionAuthorizedInspection(tools []string) {
	if state == nil || len(tools) == 0 {
		return
	}
	classes := recoveryCardCallableActionClassesForTools(tools, nil)
	state.ParserVisibleTools = textutil.Merge(state.ParserVisibleTools, tools)
	state.CallableTools = textutil.Merge(state.CallableTools, tools)
	state.AllowedActionClasses = textutil.Merge(state.AllowedActionClasses, classes)
	state.InvalidActionClasses = removeRunnerStrings(state.InvalidActionClasses, classes...)
}

func derivePlanExecutePlanningState(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, transport PlanExecutePlanningTransport, authority planThenExecutePathAuthority) PlanExecutePlanningState {
	state := PlanExecutePlanningState{
		TransportMode: planExecuteTransportMode(transport),
	}
	if strings.TrimSpace(brief.TaskFamily) == PlanThenExecuteReviewRepairTaskFamily {
		state.Mode = PlanExecutePlanningModeReviewRepair
		return state
	}

	if !planThenExecutePlannerBrief(brief) {
		return state
	}

	if successfulSubmitPlanResultCount(results) > 0 {
		state.Mode = PlanExecutePlanningModeDone
		state.TerminalOptions = []string{"plan_artifact"}

		return state
	}

	state.RequiredCoveragePaths = authority.requiredCoveragePaths(brief)
	state.RequiredPathStates = planThenExecuteRequiredPathStateSummaries(brief, results, authority)
	state.AllowedCandidatePaths = candidateRecoveryScopePaths(brief, results, 16)
	state.EvidencePaths = sortedRunnerPathKeys(newToolResultEvidenceProjection(results).ReadPaths())
	state.LastInvalidPaths = planThenExecuteLastInvalidSubmitPlanPaths(results)
	state.TerminalOptions = []string{"source_satisfied", "blocker"}
	state.SelectedEditPaths = planThenExecuteSelectedEditPaths(brief, results, state)

	unscopedEmptySubmitRecovery := planThenExecuteUnscopedEmptySubmitPlanRejected(brief, results)

	if rejected, rejectedAt, ok := lastRejectedSubmitPlanCoverageResult(results); ok {
		state.RefreshPaths = planThenExecuteRejectedPlanRefreshPaths(brief, results, rejected, rejectedAt)
	}
	if len(state.RefreshPaths) == 0 && !planThenExecuteLastRejectionSuppressesStreakRefresh(brief, results) {
		state.RefreshPaths = planThenExecuteRejectedSubmitPlanStreakRefreshPaths(brief, results)
	}
	if len(state.RefreshPaths) == 0 {
		stalePaths := planThenExecuteStaleAnchorPathsFromStateSummaries(state.RequiredPathStates)
		state.RefreshPaths = uniqueSortedRunnerPaths(append(state.RefreshPaths, stalePaths...))
	}

	switch {
	case len(state.RefreshPaths) > 0:
		state.Mode = PlanExecutePlanningModeAnchorRefresh
	case planThenExecuteRejectedPlanCorrectionOnly(brief, results):
		state.Mode = PlanExecutePlanningModeSubmitCorrection
	case len(state.RequiredCoveragePaths) > 0:
		if planThenExecuteSubmitPlanOnlyReady(brief, results, authority) {
			state.Mode = PlanExecutePlanningModeSubmitReady
		} else {
			state.Mode = PlanExecutePlanningModeRequiredSource
		}
	case len(normalizedRequiredEditPaths(brief.AllowedEditPaths)) > 0:
		if len(state.SelectedEditPaths) > 0 {
			state.Mode = PlanExecutePlanningModeSubmitReady
		} else {
			state.Mode = PlanExecutePlanningModeAllowedCandidate
		}
	default:
		if planThenExecuteSubmitPlanOnlyReady(brief, results, authority) {
			state.Mode = PlanExecutePlanningModeSubmitReady
		} else if len(state.EvidencePaths) > 0 {
			state.Mode = PlanExecutePlanningModeOpenCandidate
		} else {
			state.Mode = PlanExecutePlanningModeAllowedCandidate
		}
	}

	state.SubmitCorrectionBlockerAllowed = planThenExecuteSubmitCorrectionBlockerAllowed(brief, results)
	if len(state.RefreshPaths) == 0 && !unscopedEmptySubmitRecovery && planThenExecuteHistoryPendingEligible(brief, results, state, authority) {
		if pending := planThenExecuteHistoryPendingPaths(brief, results, state.SelectedEditPaths, authority); len(pending) > 0 {
			state.Mode = PlanExecutePlanningModeHistoryPending
			state.SelectedEditPaths = pending
		}
	}

	if state.Mode == PlanExecutePlanningModeSubmitReady ||
		state.Mode == PlanExecutePlanningModeSubmitCorrection ||
		unscopedEmptySubmitRecovery ||
		(state.Mode != PlanExecutePlanningModeHistoryPending &&
			state.Mode != PlanExecutePlanningModeAnchorRefresh &&
			len(state.SelectedEditPaths) > 0) {

		state.TerminalOptions = append(state.TerminalOptions, workspace.ToolNameSubmitPlan)
	}

	state.TerminalOptions = textutil.Unique(state.TerminalOptions)
	state.applyProjection(transport)

	return state
}

func planThenExecuteStaleAnchorPathsFromStateSummaries(summaries []string) []string {
	var paths []string
	for _, summary := range summaries {
		path, state, ok := strings.Cut(strings.TrimSpace(summary), ":")
		if !ok || strings.TrimSpace(state) != string(planThenExecuteRequiredPathStaleAnchor) {
			continue
		}
		if normalized := NormalizeRunnerPath(path); normalized != "" {
			paths = append(paths, normalized)
		}
	}

	return uniqueSortedRunnerPaths(paths)
}

func (state *PlanExecutePlanningState) applyProjection(transport PlanExecutePlanningTransport) {
	switch state.Mode {
	case PlanExecutePlanningModeRequiredSource:
		state.PhaseID = contractcompile.TurnPhaseRequiredSource
		state.Phase = "needs_source_body"
		state.DerivationReason = "plan_execute_required_source_pending"
		state.applyPlanExecuteToolSurfaceProjection(workspace.PlanExecuteToolSurfaceProjectionForPhase(workspace.ToolSurfacePhasePlanExecuteRequiredSource))
	case PlanExecutePlanningModeAllowedCandidate, PlanExecutePlanningModeOpenCandidate:
		state.PhaseID = contractcompile.TurnPhaseCandidateSource
		state.Phase = "candidate_source_pending"
		state.DerivationReason = "plan_execute_unscoped_candidate_source_pending"
		projection := workspace.PlanExecuteToolSurfaceProjectionForPhase(workspace.ToolSurfacePhasePlanExecuteCandidateSource)

		if slices.Contains(state.TerminalOptions, workspace.PlanExecuteTerminalSubmitPlan) {
			projection = projection.WithSubmitPlanTerminal()
		}
		state.applyPlanExecuteToolSurfaceProjection(projection)
	case PlanExecutePlanningModeHistoryPending:
		state.PhaseID = contractcompile.TurnPhaseHistoryPending
		state.Phase = "history_pending"
		state.DerivationReason = "plan_execute_history_evidence_pending"
		state.applyPlanExecuteToolSurfaceProjection(workspace.PlanExecuteToolSurfaceProjectionForPhase(workspace.ToolSurfacePhasePlanExecuteHistoryPending))
	case PlanExecutePlanningModeAnchorRefresh:
		state.PhaseID = contractcompile.TurnPhaseStaleAnchorRefresh
		state.Phase = "anchor_refresh"
		state.DerivationReason = "rejected_submit_plan_needs_source_refresh"
		state.applyPlanExecuteToolSurfaceProjection(workspace.PlanExecuteToolSurfaceProjectionForPhase(workspace.ToolSurfacePhasePlanExecuteAnchorRefresh))
		state.TerminalOptions = nil
	case PlanExecutePlanningModeSubmitReady, PlanExecutePlanningModeSubmitCorrection:
		state.PhaseID = contractcompile.TurnPhaseSubmitReadyTextual
		state.Phase = "submit_ready_textual"
		state.DerivationReason = "plan_execute_source_ready"

		if state.Mode == PlanExecutePlanningModeSubmitCorrection {
			state.DerivationReason = "plan_execute_submit_plan_correction"
		}

		projection := workspace.PlanExecuteToolSurfaceProjectionForPhase(workspace.ToolSurfacePhasePlanExecuteSubmitPlanOnly)
		if state.Mode == PlanExecutePlanningModeSubmitCorrection {
			projection = workspace.PlanExecuteSubmitCorrectionProjection(state.SubmitCorrectionBlockerAllowed)
		}

		if planExecuteSubmitReadyNativeRequired(transport) {
			state.PhaseID = contractcompile.TurnPhaseSubmitReadyNativeRequired
			state.Phase = "submit_ready_native"
			projection = projection.WithNativeSubmitPlanOnly()
		}
		state.applyPlanExecuteToolSurfaceProjection(projection)
	}

	state.ParserVisibleTools = textutil.Unique(state.ParserVisibleTools)
	state.CallableTools = textutil.Unique(state.CallableTools)
	state.AllowedActionClasses = textutil.Unique(state.AllowedActionClasses)
	state.InvalidActionClasses = textutil.Unique(state.InvalidActionClasses)
	state.TerminalResponseShapes = textutil.Unique(state.TerminalResponseShapes)
}

func (state *PlanExecutePlanningState) applyPlanExecuteToolSurfaceProjection(projection workspace.PlanExecuteToolSurfaceProjection) {
	state.ParserVisibleTools = append([]string(nil), projection.ParserVisibleTools...)
	state.CallableTools = append([]string(nil), projection.CallableTools...)
	state.AllowedActionClasses = append([]string(nil), projection.AllowedActionClasses...)
	state.InvalidActionClasses = append([]string(nil), projection.InvalidActionClasses...)
	state.TerminalResponseShapes = append([]string(nil), projection.TerminalResponseShapes...)
}

func planExecuteSubmitReadyNativeRequired(transport PlanExecutePlanningTransport) bool {
	return transport.ForceNativeTools || (transport.SupportsNativeTools && !transport.TextualToolParser)
}

func planExecuteTransportMode(transport PlanExecutePlanningTransport) ToolCallTransportMode {
	if transport.ForceNativeTools || (transport.SupportsNativeTools && !transport.TextualToolParser && !transport.ResponseFormatJSON) {
		return ToolCallTransportModeNative
	}

	if transport.TerminalOnly || transport.ResponseFormatJSON {
		return ToolCallTransportModeTextualJSON
	}

	return ToolCallTransportModeTextual
}

func planThenExecuteHistoryPendingPaths(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, selected []string, authority planThenExecutePathAuthority) []string {
	if !mutationHistoryEvidenceRequired(brief) {
		return nil
	}

	paths := uniqueSortedRunnerPaths(selected)
	if len(paths) == 0 {
		paths = authority.requiredCoveragePaths(brief)
	}

	var pending []string

	for _, path := range paths {
		switch planThenExecuteRequiredPathState(authority, results, path) {
		case planThenExecuteRequiredPathNewFileBodyRequired:
			continue
		case planThenExecuteRequiredPathSourceSatisfied:
		default:
			if !newToolResultEvidenceProjection(results).HasSourceWindow(path) {
				continue
			}
		}

		if successfulGitHistoryResultForPath(path, results) {
			continue
		}

		pending = append(pending, path)
	}

	return uniqueSortedRunnerPaths(pending)
}

func planThenExecuteHistoryPendingEligible(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, state PlanExecutePlanningState, authority planThenExecutePathAuthority) bool {
	if len(state.RequiredCoveragePaths) == 0 {
		return true
	}

	return len(planThenExecuteUnreadRequiredPaths(brief, results, authority)) == 0
}

func planThenExecuteSelectedEditPaths(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, state PlanExecutePlanningState) []string {
	if len(state.RequiredCoveragePaths) > 0 {
		return append([]string(nil), state.RequiredCoveragePaths...)
	}

	if len(state.LastInvalidPaths) > 0 {
		return append([]string(nil), state.LastInvalidPaths...)
	}

	if deferred := planThenExecuteDeferredPlanIntentPaths(results); len(deferred) > 0 {
		return deferred
	}

	candidates := planThenExecuteSelectableEditPaths(brief, results, 16)
	if len(candidates) == 0 {
		return nil
	}

	evidence := newToolResultEvidenceProjection(results)

	var selected []string

	for _, path := range candidates {
		normalized := NormalizeRunnerPath(path)
		if normalized == "" {
			continue
		}

		if evidence.SourceEvidenceForPath(normalized).Level == planThenExecuteEvidenceAnchorReady {
			selected = append(selected, normalized)
		}
	}

	return uniqueSortedRunnerPaths(selected)
}

func planThenExecuteDeferredPlanIntentPaths(results []workspace.ToolResult) []string {
	var paths []string
	for _, result := range results {
		if strings.TrimSpace(result.Tool) != workspace.ToolNameSubmitPlan ||
			strings.TrimSpace(result.Code) != planThenExecuteSubmitPlanHistoryDeferredCode ||
			result.Facts == nil ||
			result.Facts.RejectionDisposition != toolfacts.ToolRejectionDeferredByPrerequisite {

			continue
		}

		paths = textutil.Merge(paths, canonicalPersistedRunnerPaths(result.Facts.DeferredPlanPaths))
	}

	return uniqueSortedRunnerPaths(paths)
}

func planThenExecuteSubmitPlanSkeletonPaths(state PlanExecutePlanningState) []string {
	switch state.Mode {
	case PlanExecutePlanningModeSubmitCorrection:
		if len(state.RequiredCoveragePaths) > 0 {
			return uniqueSortedRunnerPaths(state.RequiredCoveragePaths)
		}

		return uniqueSortedRunnerPaths(state.LastInvalidPaths)
	case PlanExecutePlanningModeSubmitReady:
		if len(state.RequiredCoveragePaths) > 0 {
			return uniqueSortedRunnerPaths(state.RequiredCoveragePaths)
		}

		return uniqueSortedRunnerPaths(state.SelectedEditPaths)
	case PlanExecutePlanningModeRequiredSource:
		return uniqueSortedRunnerPaths(state.RequiredCoveragePaths)
	case PlanExecutePlanningModeAllowedCandidate, PlanExecutePlanningModeOpenCandidate:
		return uniqueSortedRunnerPaths(state.SelectedEditPaths)
	default:
		return nil
	}
}

func planThenExecuteSelectableEditPaths(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, max int) []string {
	if max <= 0 {
		max = 4
	}

	paths := make([]string, 0, max)
	seen := map[string]struct{}{}
	add := func(path string) {
		normalized := NormalizeRunnerPath(path)
		if normalized == "" {
			return
		}

		if _, ok := seen[normalized]; ok {
			return
		}

		seen[normalized] = struct{}{}
		paths = append(paths, normalized)
	}

	for _, path := range normalizedRequiredEditPaths(brief.AllowedEditPaths) {
		add(path)

		if len(paths) >= max {
			return paths
		}
	}

	for _, path := range typedDiscoveryCandidatePathsFromResults(results, max) {
		add(path)

		if len(paths) >= max {
			return paths
		}
	}

	return paths
}

func planThenExecuteLastInvalidSubmitPlanPaths(results []workspace.ToolResult) []string {
	rejected, _, ok := lastRejectedSubmitPlanCoverageResult(results)
	if !ok {
		return nil
	}

	pathSet := map[string]struct{}{}
	add := func(path string) {
		if normalized := NormalizeRunnerPath(path); normalized != "" {
			pathSet[normalized] = struct{}{}
		}
	}

	for path := range submitPlanInvalidAnchorPaths(rejected) {
		add(path)
	}

	for path := range submitPlanMissingRequiredPaths(rejected) {
		add(path)
	}

	for path := range submitPlanMissingSourceGroundingPaths(rejected) {
		add(path)
	}

	if len(pathSet) == 0 {
		for path := range planThenExecuteRejectedSubmitPlanStreakInvalidPaths(results) {
			add(path)
		}
	}

	return sortedRunnerPathKeys(pathSet)
}

func sortedRunnerPathKeys(paths map[string]struct{}) []string {
	out := make([]string, 0, len(paths))
	for path := range paths {
		if normalized := NormalizeRunnerPath(path); normalized != "" {
			out = append(out, normalized)
		}
	}

	return uniqueSortedRunnerPaths(out)
}

func uniqueSortedRunnerPaths(paths []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(paths))

	for _, path := range paths {
		normalized := NormalizeRunnerPath(path)
		if normalized == "" {
			continue
		}

		if _, ok := seen[normalized]; ok {
			continue
		}

		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}

	sort.Strings(out)

	return out
}

type PlanExecuteRecoveryKind string

const (
	PlanExecuteRecoveryKindGroundingRequired      PlanExecuteRecoveryKind = "grounding_required"
	PlanExecuteRecoveryKindEmptySubmitPlan        PlanExecuteRecoveryKind = "empty_submit_plan"
	PlanExecuteRecoveryKindSubmitPlanParse        PlanExecuteRecoveryKind = "submit_plan_parse"
	PlanExecuteRecoveryKindSubmitCorrection       PlanExecuteRecoveryKind = "submit_correction"
	PlanExecuteRecoveryKindNativeSubmitCorrection PlanExecuteRecoveryKind = "native_submit_correction"
	PlanExecuteRecoveryKindRepeatedCorrection     PlanExecuteRecoveryKind = "repeated_correction"
	PlanExecuteRecoveryKindNativeNoCallFallback   PlanExecuteRecoveryKind = "native_no_call_fallback"
)

type PlanExecuteRecoveryPlan struct {
	Kind            PlanExecuteRecoveryKind
	TerminalOptions []string
	Summary         string
	Instructions    []string
	SubmitPlanShape string
}

func RenderPlanExecuteRecoveryPlan(plan PlanExecuteRecoveryPlan) string {
	parts := make([]string, 0, 5+len(plan.Instructions))
	if strings.TrimSpace(plan.Summary) != "" {
		parts = append(parts, strings.TrimSpace(plan.Summary))
	}

	if len(plan.TerminalOptions) > 0 {
		parts = append(parts, "Valid terminal actions now: "+strings.Join(plan.TerminalOptions, ", ")+".")
	}

	parts = append(parts, plan.Instructions...)
	if strings.TrimSpace(plan.SubmitPlanShape) != "" {
		parts = append(parts, strings.TrimSpace(plan.SubmitPlanShape))
	}

	return strings.Join(nonEmptyPlanExecuteRecoveryPromptParts(parts), "\n\n")
}

func nonEmptyPlanExecuteRecoveryPromptParts(parts []string) []string {
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}

	return out
}

func planThenExecuteGroundingRequiredRecoveryPlan() PlanExecuteRecoveryPlan {
	return PlanExecuteRecoveryPlan{
		Kind:            PlanExecuteRecoveryKindGroundingRequired,
		TerminalOptions: workspace.PlanExecuteTerminalSourceSatisfiedOrQuestion(),
		Summary:         "The previous `submit_plan` was rejected because this unscoped plan-execute atom has no grounded edit paths yet.",
		Instructions: []string{
			"Do not repeat `submit_plan`.",
			"Next visible action must be one read-only localization or source-evidence request, or a terminal closeout.",
			"For source-evidence requests, use `context_pack`, `product_efforts`, `product_status`, targeted `search_content`, `read_file`, or `" + workspace.ToolNameCodeNavigate + "` with " + workspace.CodeNavigateSourceBodyCapabilityPhrase() + ".",
			"For terminal closeout, answer `Decision: source-satisfied` with observed current-state evidence, or answer `<<QUESTION>>:` with the exact blocker.",
		},
	}
}

func planThenExecuteUnscopedEmptySubmitPlanRecoveryPlan() PlanExecuteRecoveryPlan {
	return PlanExecuteRecoveryPlan{
		Kind:            PlanExecuteRecoveryKindEmptySubmitPlan,
		TerminalOptions: workspace.PlanExecuteTerminalSubmitPlanSourceSatisfiedQuestion(),
		Summary:         "The previous `submit_plan` was rejected before it became a valid plan artifact because it contained no executable edits.",
		Instructions: []string{
			"No required_edit_paths or allowed_edit_paths list is active, so the harness cannot invent coverage paths for this planner turn.",
			"Do not repeat an empty `submit_plan`. If current evidence supports a concrete change, submit a non-empty `edits[]` array using a real candidate path and one executable edit shape.",
			"If one edit-safety fact is missing, make exactly one bounded localization/source request for a candidate path from the packet or observed source evidence.",
			"If no concrete candidate edit path can be identified, answer `<<QUESTION>>:` with the exact blocker instead of retrying `submit_plan`.",
		},
		SubmitPlanShape: "For textual `submit_plan`, emit one visible JSON object with top-level `tool`, `rationale`, and non-empty `edits` array. Each edit must use an actual candidate path from observed evidence, not an example or placeholder path, and exactly one executable shape such as exact `old_text`/`new_text` or bounded `start_line`/`end_line`/`content`.",
	}
}

func planThenExecuteSubmitPlanParseRecoveryPlan(stringifiedEdits bool, requiredPaths []string) PlanExecuteRecoveryPlan {
	summary := "SUBMIT_PLAN PARSE RECOVERY: the previous visible tool object was malformed and did not count as a plan artifact."
	if stringifiedEdits {
		summary = "SUBMIT_PLAN PARSE RECOVERY: the previous `submit_plan` did not count because `edits` was emitted as a quoted JSON string."
	}

	instructions := []string{
		"The required source-body obligations are already satisfied. Submit the corrected artifact when ready; permission-authorized inspection remains callable for a fresh, concrete missing fact.",
		"Next visible action MUST be exactly one of:",
		"  1. A parser-readable `submit_plan` request whose top-level `edits` value is a JSON array, not a string. Each edit object must contain a path from the required edit scope, one executable edit shape, and a one-sentence purpose.\n     Use `insert_after_line` plus small inline non-empty `content` for small insertions/appends after an existing line. Use `old_text`/`new_text` for exact replacements/deletions. Use numeric `start_line`/`end_line` plus complete `content` to replace that exact inclusive current-source range; for large additions, include the unchanged anchor line(s) plus inserted content.",
		"  2. `Decision: source-satisfied` naming every required path and the observed current-state evidence.",
		"  3. `<<QUESTION>>:` with the exact path-specific blocker that prevents drafting the plan.",
	}
	if len(requiredPaths) > 0 {
		instructions = append(instructions, "Required edit paths: "+strings.Join(requiredPaths, ", ")+".")
	}

	return PlanExecuteRecoveryPlan{
		Kind:            PlanExecuteRecoveryKindSubmitPlanParse,
		TerminalOptions: workspace.PlanExecuteTerminalSubmitPlanSourceSatisfiedQuestion(),
		Summary:         summary,
		Instructions:    instructions,
	}
}

// planThenExecuteSubmitCorrectionRecoveryPlan returns a recovery plan for the generic
// submit-plan correction path after a rejected submit_plan.
func planThenExecuteSubmitCorrectionRecoveryPlan(brief workerruntime.WorkerRuntimeBrief, result workspace.ToolResult, refreshNote string, blockerAllowed bool) PlanExecuteRecoveryPlan {
	required := nonEmptyRunnerStrings(brief.RequiredEditPaths, 12)
	terminal := workspace.PlanExecuteTerminalSubmitPlanOnly()
	summary := "The previous `submit_plan` was rejected before it became a valid plan artifact."

	if brief := lastSubmitPlanCorrectionRejectionBrief(result); brief != "" {
		summary += " " + brief
	}

	outcome := "Do not answer `Decision: source-satisfied` or `<<QUESTION>>:` while the rejected artifact is still correctable. Permission-authorized inspection remains callable for a fresh, concrete missing fact."
	if blockerAllowed {
		terminal = workspace.PlanExecuteTerminalSubmitPlanQuestion()
		outcome = "Do not answer `Decision: source-satisfied`. Permission-authorized inspection remains callable. If no safe executable edit body can be produced after these rejected submit_plan attempts, answer `<<QUESTION>>:` with the exact path-specific missing body/anchor blocker."
	}

	instructions := []string{
		"The next visible action must be a corrected parser-readable `submit_plan` request.",
		planThenExecuteSubmitPlanCoverageSentence(),
		planThenExecuteSubmitPlanCorrectionInstructionForBrief(brief, result),
		outcome,
	}
	if refreshNote != "" {
		instructions = append(instructions, refreshNote)
	}

	if len(required) > 0 {
		instructions = append(instructions, "Required edit paths: "+strings.Join(required, ", ")+".")
	}

	return PlanExecuteRecoveryPlan{
		Kind:            PlanExecuteRecoveryKindSubmitCorrection,
		TerminalOptions: terminal,
		Summary:         summary,
		Instructions:    instructions,
	}
}

// planThenExecuteNativeSubmitPlanCorrectionRecoveryPlan returns a recovery plan
// for the native submit_plan tool path after a rejection.
func planThenExecuteNativeSubmitPlanCorrectionRecoveryPlan(brief workerruntime.WorkerRuntimeBrief, result workspace.ToolResult) PlanExecuteRecoveryPlan {
	required := nonEmptyRunnerStrings(brief.RequiredEditPaths, 12)
	summary := "The previous native `submit_plan` tool call was rejected before it became a valid plan artifact."

	if brief := lastSubmitPlanCorrectionRejectionBrief(result); brief != "" {
		summary += " " + brief
	}

	instructions := []string{
		"Call native `submit_plan` again now.",
		"The next tool call must be a corrected `submit_plan`.",
		planThenExecuteSubmitPlanCoverageSentence(),
		"Permission-authorized inspection remains callable for a fresh, concrete missing fact; mutation, verification, and terminal acceptance remain governed by the active phase contract.",
	}
	if details := planThenExecuteNativeSubmitPlanCorrectionDetails(brief, result); details != "" {
		instructions = append(instructions, details)
	}

	if len(required) > 0 {
		instructions = append(instructions, "Required edit paths: "+strings.Join(required, ", ")+".")
	}

	return PlanExecuteRecoveryPlan{
		Kind:            PlanExecuteRecoveryKindNativeSubmitCorrection,
		TerminalOptions: workspace.PlanExecuteTerminalSubmitPlanOnly(),
		Summary:         summary,
		Instructions:    instructions,
	}
}

// planThenExecuteRepeatedSubmitCorrectionRecoveryPlan for repeated invalid submit_plan.
func planThenExecuteRepeatedSubmitCorrectionRecoveryPlan(lastResult workspace.ToolResult) PlanExecuteRecoveryPlan {
	summary := "The last request repeated the same invalid `submit_plan`; it was not executed again."
	if brief := lastSubmitPlanCorrectionRejectionBrief(lastResult); brief != "" {
		summary += " " + brief
	}

	instructions := []string{
		"Submit a corrected parser-readable `submit_plan` now.",
		planThenExecuteSubmitPlanCoverageSentence(),
		"Do not answer `Decision: source-satisfied` or `<<QUESTION>>:` while the rejected artifact remains correctable. Permission-authorized inspection remains callable for a fresh, concrete missing fact.",
		planThenExecuteSubmitPlanCorrectionInstructionForBrief(workerruntime.WorkerRuntimeBrief{}, lastResult),
	}

	return PlanExecuteRecoveryPlan{
		Kind:            PlanExecuteRecoveryKindRepeatedCorrection,
		TerminalOptions: workspace.PlanExecuteTerminalSubmitPlanOnly(),
		Summary:         summary,
		Instructions:    instructions,
	}
}

// planThenExecuteRepeatedCorrectionExhaustedRejection returns summary and error text.
func planThenExecuteRepeatedCorrectionExhaustedRejection(lastResult workspace.ToolResult) (string, string) {
	reason := ""
	if result := lastSubmitPlanCorrectionRejectionBrief(lastResult); result != "" {
		reason = result
	}

	return "Repeated invalid submit_plan rejected",
		"this exact invalid `submit_plan` was repeated after submit-correction prompts. " + reason + "A corrected parser-readable `submit_plan` is still required. " + planThenExecuteSubmitPlanCoverageSentence() + " Permission-authorized inspection remains callable for a fresh, concrete missing fact."
}

// planThenExecuteNativeNoCallFallbackRecoveryPlan returns a recovery plan for when
// the native submit_plan tool-call transport failed before producing a call.
func planThenExecuteNativeNoCallFallbackRecoveryPlan(requiredPaths []string) PlanExecuteRecoveryPlan {
	instructions := []string{
		"Emit exactly one visible JSON object for `submit_plan` now. Do not write prose before or after it.",
		planThenExecuteSubmitPlanCoverageSentence(),
	}
	if len(requiredPaths) > 0 {
		instructions = append(instructions, "Required edit paths: "+strings.Join(requiredPaths, ", ")+".")
	}

	return PlanExecuteRecoveryPlan{
		Kind:            PlanExecuteRecoveryKindNativeNoCallFallback,
		TerminalOptions: workspace.PlanExecuteTerminalSubmitPlanOnly(),
		Summary:         "Native submit_plan tool-call transport failed before the model produced a tool call. For this one recovery retry, native tools are disabled and the textual tool parser is active.",
		Instructions:    instructions,
	}
}

func DerivePlanExecuteRuntimeCallPolicy(input RuntimeCallPolicyInput) RuntimeCallPolicy {
	terminalOnly := input.TerminalOnly || textutil.EqualTrimFold(input.ParserContract, "submit_plan_json")
	runtimePolicy := DeriveRuntimeCallPolicy(RuntimeCallPolicyInput{
		SupportsNativeTools:      input.SupportsNativeTools,
		ForceNativeTools:         input.ForceNativeTools,
		TextualToolParser:        input.TextualToolParser,
		TerminalOnly:             terminalOnly,
		ResponseFormatJSON:       input.ResponseFormatJSON,
		Temperature:              input.Temperature,
		TopP:                     input.TopP,
		TopK:                     input.TopK,
		MinP:                     input.MinP,
		PresencePenalty:          input.PresencePenalty,
		RepetitionPenalty:        input.RepetitionPenalty,
		ToolChoice:               input.ToolChoice,
		ParserContract:           input.ParserContract,
		ResponseFormat:           input.ResponseFormat,
		ThinkingPolicy:           input.ThinkingPolicy,
		EnableThinking:           input.EnableThinking,
		PreserveThinking:         input.PreserveThinking,
		ThinkingBudget:           input.ThinkingBudget,
		ChatTemplateKwargs:       input.ChatTemplateKwargs,
		MaxTokens:                input.MaxTokens,
		TimeoutMillis:            input.TimeoutMillis,
		ExpressibleActionClasses: input.ExpressibleActionClasses,
	})
	expressibleActionClasses := append([]string(nil), runtimePolicy.ExpressibleActionClasses...)
	if len(expressibleActionClasses) == 0 && terminalOnly {
		switch runtimePolicy.TransportMode {
		case ToolCallTransportModeNative:
			expressibleActionClasses = []string{actionSubmitPlan}
		case ToolCallTransportModeTextualJSON:
			expressibleActionClasses = []string{actionSourceSatisfied, actionBlocker, actionFinalAnswer}
		default:
			expressibleActionClasses = []string{actionSubmitPlan, actionSourceSatisfied, actionBlocker}
		}
	}
	runtimePolicy.ExpressibleActionClasses = textutil.Unique(expressibleActionClasses)
	return runtimePolicy
}

func PlanExecuteRuntimeCallPolicyFromChatOptions(opts llama.ChatOptions, supportsNativeTools bool, terminalOnly bool) RuntimeCallPolicy {
	return DerivePlanExecuteRuntimeCallPolicy(RuntimeCallPolicyInput{
		SupportsNativeTools: supportsNativeTools,
		ForceNativeTools:    opts.ForceNativeTools,
		TextualToolParser:   planExecuteTextualToolParserFromChatOptions(opts, supportsNativeTools),
		TerminalOnly:        terminalOnly,
		ResponseFormatJSON:  opts.ResponseFormatJSON,
		Temperature:         opts.Temperature,
		TopP:                opts.TopP,
		TopK:                opts.TopK,
		MinP:                opts.MinP,
		PresencePenalty:     opts.PresencePenalty,
		RepetitionPenalty:   opts.RepetitionPenalty,
		ToolChoice:          opts.ToolChoice,
		ThinkingPolicy:      runnerTurnPhaseThinkingPolicy(opts),
		EnableThinking:      opts.EnableThinking,
		PreserveThinking:    opts.PreserveThinking,
		ThinkingBudget:      opts.ThinkingBudget,
		ChatTemplateKwargs:  opts.ChatTemplateKwargs,
		MaxTokens:           opts.MaxTokens,
	})
}

func planExecuteRuntimeCallPolicyFromPhaseInput(input TurnPhaseContractInput) RuntimeCallPolicy {
	return DerivePlanExecuteRuntimeCallPolicy(RuntimeCallPolicyInput{
		SupportsNativeTools: input.SupportsNativeTools,
		TextualToolParser:   input.TextualToolParser,
		TerminalOnly:        false,
		ResponseFormatJSON:  textutil.EqualTrimFold(input.ResponseFormat, "json"),
		Temperature:         input.Temperature,
		TopP:                input.TopP,
		TopK:                input.TopK,
		MinP:                input.MinP,
		PresencePenalty:     input.PresencePenalty,
		RepetitionPenalty:   input.RepetitionPenalty,
		ToolChoice:          input.ToolChoice,
		ParserContract:      input.ParserContract,
		ResponseFormat:      input.ResponseFormat,
		ThinkingPolicy:      input.ThinkingPolicy,
		MaxTokens:           input.MaxTokens,
	})
}

func (policy RuntimeCallPolicy) PlanningTransport() PlanExecutePlanningTransport {
	return PlanExecutePlanningTransport{
		SupportsNativeTools: policy.SupportsNativeTools,
		ForceNativeTools:    policy.ForceNativeTools,
		TextualToolParser:   policy.TextualToolParser,
		TerminalOnly:        policy.TerminalOnly,
		ResponseFormatJSON:  policy.ResponseFormatJSON,
	}
}

func (policy RuntimeCallPolicy) TelemetryFields() map[string]any {
	responseFormat := strings.TrimSpace(policy.ResponseFormat)
	if policy.ResponseFormatJSON {
		responseFormat = "json"
	}

	fields := map[string]any{
		"transport_mode":         policy.ToolTransportMode(),
		"runtime_transport_mode": string(policy.TransportMode),
		"parser_contract":        strings.TrimSpace(policy.ParserContract),
		"response_format":        responseFormat,
		"response_format_mode":   policy.ResponseFormatMode(),
		"response_format_json":   policy.ResponseFormatJSON,
		"temperature":            policy.Temperature,
		"top_p":                  policy.TopP,
		"top_k":                  policy.TopK,
		"min_p":                  policy.MinP,
		"presence_penalty":       policy.PresencePenalty,
		"repetition_penalty":     policy.RepetitionPenalty,
		"max_tokens":             policy.MaxTokens,
		"effective_max_tokens":   policy.MaxTokens,
		"thinking_policy":        strings.TrimSpace(policy.ThinkingPolicy),
		"thinking_preserved":     policy.PreserveThinking,
		"thinking_budget":        policy.ThinkingBudget,
		"chat_template_kwargs":   policy.ChatTemplateKwargs,
		"tool_choice":            strings.TrimSpace(policy.ToolChoice),
		"tool_choice_required":   policy.ToolChoiceRequired,
		"force_native_tools":     policy.ForceNativeTools,
		"supports_native_tools":  policy.SupportsNativeTools,
		"textual_tool_parser":    policy.TextualToolParser,
	}
	if policy.EnableThinking != nil {
		fields["thinking_enabled"] = *policy.EnableThinking
	}

	if policy.TimeoutMillis > 0 {
		fields["effective_timeout_ms"] = policy.TimeoutMillis
	}

	if classes := textutil.Unique(policy.ExpressibleActionClasses); len(classes) > 0 {
		fields["expressible_action_classes"] = classes
	}

	return fields
}

func planExecuteTextualToolParserFromChatOptions(opts llama.ChatOptions, supportsNativeTools bool) bool {
	return opts.ResponseFormatJSON || len(opts.Tools) == 0 || (!supportsNativeTools && !opts.ForceNativeTools)
}

func planExecuteForceVisibleActionChatOptions(opts llama.ChatOptions) llama.ChatOptions {
	thinkingOff := false

	opts.EnableThinking = &thinkingOff
	opts.ChatTemplateKwargs = true
	opts.PreserveThinking = false

	return opts
}

func planExecuteSubmitPlanTerminalChatOptions(opts llama.ChatOptions, supportsNativeTools bool) llama.ChatOptions {
	opts = planExecuteForceVisibleActionChatOptions(opts)
	if !supportsNativeTools {
		opts.Tools = nil
		opts.ToolChoice = ""
		opts.ResponseFormatJSON = true
	} else {
		opts.ResponseFormatJSON = false
	}

	return opts
}

func planExecuteSetExactMaxTokens(opts llama.ChatOptions, target int) llama.ChatOptions {
	if target <= 0 {
		return opts
	}

	if opts.MaxTokens <= 0 || opts.MaxTokens < target || opts.MaxTokens > target {
		opts.MaxTokens = target
	}

	return opts
}

func planExecuteCapMaxTokens(opts llama.ChatOptions, limit int) llama.ChatOptions {
	if limit <= 0 {
		return opts
	}

	if opts.MaxTokens <= 0 || opts.MaxTokens > limit {
		opts.MaxTokens = limit
	}

	return opts
}

func (r *runExecution) compilePlanIntentToolRequest(text string) (workspace.ToolRequest, string, bool) {
	if !r.planIntentCompilationAllowed() {
		return workspace.ToolRequest{}, "", false
	}

	payload, ok := exactPlanIntentPayload(text)
	if !ok {
		return workspace.ToolRequest{}, "", false
	}

	var artifact planintent.Artifact

	if err := json.Unmarshal([]byte(payload), &artifact); err != nil {
		r.emitPlanIntentStageEvent("edit_intent_validated", map[string]any{
			"schema_version":    planintent.SchemaVersion,
			"validation_result": "rejected",
			"failure_class":     "malformed_json",
		})

		return workspace.ToolRequest{}, "", false
	}

	if artifact.SchemaVersion != planintent.SchemaVersion {
		return workspace.ToolRequest{}, "", false
	}

	contract := planintent.Contract{
		RequiredEditPaths:  r.state.runtimeBrief.RequiredEditPaths,
		CandidateEditPaths: r.state.runtimeBrief.AllowedEditPaths,
	}
	if err := planintent.Validate(artifact, contract); err != nil {
		r.emitPlanIntentStageEvent("edit_intent_validated", planIntentValidationPayload(artifact, "rejected", err))

		return workspace.ToolRequest{}, "", false
	}

	r.emitPlanIntentStageEvent("edit_intent_validated", planIntentValidationPayload(artifact, "accepted", nil))
	r.emitPlanIntentStageEvent("patch_compile_started", planIntentCompilePayload(artifact, "started", nil, 0))

	root := ""
	if r != nil && r.runner.Workspace != nil {
		root = r.runner.Workspace.Root()
	}

	edits, err := planintent.Compile(artifact, planintent.CompileOptions{
		WorkspaceRoot: root,
		Contract:      contract,
	})
	if err != nil {
		r.emitPlanIntentStageEvent("patch_compile_result", planIntentCompilePayload(artifact, "rejected", err, 0))

		return workspace.ToolRequest{}, "", false
	}

	req := workspace.ToolRequest{
		Tool:      workspace.ToolNameSubmitPlan,
		Rationale: planIntentRationale(artifact),
		Edits:     edits,
	}

	raw, err := json.Marshal(req)
	if err != nil {
		r.emitPlanIntentStageEvent("patch_compile_result", planIntentCompilePayload(artifact, "rejected", err, 0))

		return workspace.ToolRequest{}, "", false
	}

	r.emitPlanIntentStageEvent("patch_compile_result", planIntentCompilePayload(artifact, "accepted", nil, len(edits)))

	return req, string(raw), true
}

func exactPlanIntentPayload(text string) (string, bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return "", false
	}

	if strings.HasPrefix(trimmed, "{") {
		return trimmed, strings.Contains(trimmed, `"schema_version"`) && strings.Contains(trimmed, planintent.SchemaVersion)
	}

	if payload, ok, unsafe := exactFencedJSONPayload(trimmed); ok && !unsafe {
		return payload, strings.Contains(payload, `"schema_version"`) && strings.Contains(payload, planintent.SchemaVersion)
	}

	return "", false
}

func planIntentRationale(artifact planintent.Artifact) string {
	summary := strings.TrimSpace(artifact.TaskSummary)
	if summary == "" {
		return "compiled from non-executable idiom.edit_intent.v1 artifact"
	}

	return "compiled from non-executable idiom.edit_intent.v1 artifact: " + summary
}

func planIntentValidationPayload(artifact planintent.Artifact, result string, err error) map[string]any {
	payload := map[string]any{
		"schema_version":     strings.TrimSpace(artifact.SchemaVersion),
		"source":             string(artifact.Source),
		"validation_result":  result,
		"required_paths":     safeTelemetryPathList(artifact.Scope.RequiredEditPaths),
		"candidate_paths":    safeTelemetryPathList(artifact.Scope.CandidateEditPaths),
		"intent_count":       len(artifact.Intents),
		"blocker_count":      len(artifact.Blockers),
		"evidence_ref_count": len(artifact.Scope.EvidenceRefs),
	}
	if err != nil {
		payload["failure_class"] = planIntentErrorCode(err)
	}

	return payload
}

func planIntentCompilePayload(artifact planintent.Artifact, result string, err error, editCount int) map[string]any {
	payload := map[string]any{
		"schema_version":  strings.TrimSpace(artifact.SchemaVersion),
		"source":          string(artifact.Source),
		"compile_outcome": result,
		"required_paths":  safeTelemetryPathList(artifact.Scope.RequiredEditPaths),
		"candidate_paths": safeTelemetryPathList(artifact.Scope.CandidateEditPaths),
		"intent_count":    len(artifact.Intents),
		"edit_count":      editCount,
	}
	if err != nil {
		payload["failure_class"] = planIntentErrorCode(err)
	}

	return payload
}

func planIntentErrorCode(err error) string {
	if err == nil {
		return ""
	}

	if validationErr, ok := err.(*planintent.ValidationError); ok {
		return validationErr.Code
	}

	if compileErr, ok := err.(*planintent.CompileError); ok {
		return compileErr.Code
	}

	return "unknown"
}

func safeTelemetryPathList(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		path = NormalizeRunnerPath(path)
		if path != "" {
			out = append(out, path)
		}
	}

	return out
}

func (r *runExecution) emitPlanIntentStageEvent(name string, payload map[string]any) {
	if r == nil {
		return
	}

	r.emitEvent(marshalRunEvent(name, payload))
}

func (r *runExecution) planIntentCompilationAllowed() bool {
	if r == nil {
		return false
	}

	if strings.TrimSpace(r.state.runtimeBrief.TaskFamily) != PlanThenExecutePlannerTaskFamily {
		return false
	}

	return stringsInSetContains(r.state.runtimeBrief.AllowedTools, workspace.ToolNameSubmitPlan) || stringsInSetContains(r.state.runtimeBrief.RequiredTools, workspace.ToolNameSubmitPlan)
}

func stringsInSetContains(values []string, want string) bool {
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

const recoveryCardSchemaVersion = "idiom.recovery_card.v1"

// RecoveryCard is a generic model-facing recovery packet for a rejected or
// low-value action. It explains the active invariant and valid recovery surface
// without choosing strategy for the model.
type RecoveryCard struct {
	SchemaVersion string

	ContractID   string
	PhaseID      contractcompile.TurnPhaseID
	InvariantID  string
	EvidenceRefs []string

	RejectedAction RecoveryRejectedAction
	Invariant      string
	InvalidRepeats []string

	CallableTools           []string
	CallableActionClasses   []string
	TerminalOutcomes        []string
	VisibleSafeAlternatives []string
	RecoveryState           *PlanExecuteRecoveryState
	BlockedTaskLedgerItems  []recoverycontract.ImplementationBlockedTaskItem

	ExactBlockerShape string
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

	InvalidRepeats         []string
	RecoveryState          *PlanExecuteRecoveryState
	BlockedTaskLedgerItems []recoverycontract.ImplementationBlockedTaskItem
	ExactBlockerShape      string
}

func BuildRecoveryCard(input RecoveryCardInput) RecoveryCard {
	contract := input.PhaseContract
	invariantID := recoveryCardInvariantID(input)
	callableTools := recoveryCardCallableTools(contract, input.CurrentAllowedTools)
	callableActionClasses := recoveryCardCallableActionClassesForSurface(contract, callableTools, input.CurrentAllowedTools)

	return NormalizeRecoveryCard(RecoveryCard{
		ContractID:  contract.ContractID,
		PhaseID:     contract.PhaseID,
		InvariantID: invariantID,
		EvidenceRefs: textutil.Merge(
			input.EvidenceRefs,
			recoveryCardEvidenceRefsFromContract(contract),
		),
		RejectedAction: RecoveryRejectedAction{
			Tool:        recoveryCardRejectedTool(input.RejectedRequest, input.RejectedResult),
			ActionClass: recoveryCardRejectedActionClass(input.RejectedRequest, input.RejectedResult),
			Target:      workerruntime.ToolRequestTarget(input.RejectedRequest),
			Reason:      recoveryCardRejectedReason(input.RejectedResult),
		},
		Invariant:               recoveryCardInvariantText(invariantID, contract),
		InvalidRepeats:          textutil.Merge(input.InvalidRepeats, recoveryCardInvalidRepeats(invariantID, contract)),
		CallableTools:           callableTools,
		CallableActionClasses:   callableActionClasses,
		TerminalOutcomes:        recoveryCardTerminalOutcomes(contract),
		RecoveryState:           copyPlanExecuteRecoveryState(input.RecoveryState),
		BlockedTaskLedgerItems:  copyRecoveryCardBlockedTaskLedgerItems(input.BlockedTaskLedgerItems),
		VisibleSafeAlternatives: recoveryCardVisibleSafeAlternatives(input.RecoveryState, callableActionClasses),
		ExactBlockerShape:       recoveryCardExactBlockerShape(input.ExactBlockerShape, contract),
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

func (r *runExecution) renderRecoveryCardForRejectedActionWithTransport(req workspace.ToolRequest, result workspace.ToolResult, results []workspace.ToolResult, currentAllowedTools []string, transport PlanExecutePlanningTransport) string {
	if r == nil {
		return ""
	}

	brief := r.visibleBriefForToolSurface(r.state.runtimeBrief, results)
	completionState := r.implementationCompletionState(results)
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief:                    runnerBriefWithPhaseHints(brief, results, r.planThenExecutePathAuthority(), completionState),
		Results:                  results,
		LastRequest:              req,
		LastResult:               result,
		ImplementationCompletion: &completionState,
		mutationAuthority:        r.planThenExecutePathAuthority(),
		InspectionAuthority:      r.permissionAuthorizedInspection(r.workspaceAdvertisedAllowedTools()),
		BaseAllowedTools:         currentAllowedTools,
		SupportsNativeTools:      transport.SupportsNativeTools,
		TextualToolParser:        transport.TextualToolParser,
	})
	recoveryState, hasRecoveryState := derivePlanExecuteRecoveryState(brief, results, transport, contract, req, result, r.planThenExecutePathAuthority())
	var recoveryStatePtr *PlanExecuteRecoveryState
	if hasRecoveryState {
		recoveryStatePtr = &recoveryState
	}

	return RenderRecoveryCard(BuildRecoveryCard(RecoveryCardInput{
		PhaseContract:          contract,
		CurrentAllowedTools:    effectiveRecoveryCardAllowedTools(currentAllowedTools, brief, results, contract),
		RejectedRequest:        req,
		RejectedResult:         result,
		EvidenceRefs:           newToolResultEvidenceProjection(results).evidencePathsForBrief(brief, 6),
		RecoveryState:          recoveryStatePtr,
		BlockedTaskLedgerItems: implementationLifecycleBlockedTaskItems(completionState.BlockedTaskLedgerItems),
	}))
}

func NormalizeRecoveryCard(card RecoveryCard) RecoveryCard {
	if strings.TrimSpace(card.SchemaVersion) == "" {
		card.SchemaVersion = recoveryCardSchemaVersion
	}

	card.ContractID = strings.TrimSpace(card.ContractID)
	card.PhaseID = contractcompile.TurnPhaseID(strings.TrimSpace(string(card.PhaseID)))
	card.InvariantID = strings.TrimSpace(card.InvariantID)
	card.EvidenceRefs = textutil.Unique(card.EvidenceRefs)
	card.RejectedAction = normalizeRecoveryRejectedAction(card.RejectedAction)
	card.Invariant = strings.TrimSpace(card.Invariant)
	card.InvalidRepeats = textutil.Unique(card.InvalidRepeats)
	card.CallableTools = textutil.Unique(card.CallableTools)
	card.CallableActionClasses = textutil.Unique(card.CallableActionClasses)
	card.TerminalOutcomes = textutil.Unique(card.TerminalOutcomes)
	card.VisibleSafeAlternatives = textutil.Unique(card.VisibleSafeAlternatives)
	card.RecoveryState = copyPlanExecuteRecoveryState(card.RecoveryState)
	card.BlockedTaskLedgerItems = copyRecoveryCardBlockedTaskLedgerItems(card.BlockedTaskLedgerItems)
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
	writeRecoveryCardLine(&b, "visible safe alternatives", recoveryCardListLabel(card.VisibleSafeAlternatives, "none"))
	if card.RecoveryState != nil {
		writeRecoveryCardLine(&b, "recovery state", card.RecoveryState.label())
	}
	writeRecoveryCardLine(&b, "typed task blockers", recoveryCardBlockedTaskLedgerItemsLabel(card.BlockedTaskLedgerItems))
	writeRecoveryCardLine(&b, "exact blocker shape", recoveryCardOptionalLabel(card.ExactBlockerShape, "none available"))

	return strings.TrimRight(b.String(), "\n")
}

func copyRecoveryCardBlockedTaskLedgerItems(items []recoverycontract.ImplementationBlockedTaskItem) []recoverycontract.ImplementationBlockedTaskItem {
	if len(items) == 0 {
		return nil
	}

	copied := make([]recoverycontract.ImplementationBlockedTaskItem, 0, len(items))
	for _, item := range items {
		item.ID = strings.TrimSpace(item.ID)
		item.Kind = strings.TrimSpace(item.Kind)
		item.SubjectNamespace = strings.TrimSpace(item.SubjectNamespace)
		item.SubjectID = strings.TrimSpace(item.SubjectID)
		item.Reason = strings.TrimSpace(item.Reason)
		item.Paths = append([]string(nil), item.Paths...)
		copied = append(copied, item)
	}

	return copied
}

func recoveryCardBlockedTaskLedgerItemsLabel(items []recoverycontract.ImplementationBlockedTaskItem) string {
	if len(items) == 0 {
		return ""
	}

	labels := make([]string, 0, len(items))
	for _, item := range items {
		subject := strings.TrimSpace(item.SubjectNamespace) + ":" + strings.TrimSpace(item.SubjectID)
		labels = append(labels, fmt.Sprintf(
			"id=%q kind=%q subject=%q reason=%q",
			strings.TrimSpace(item.ID),
			strings.TrimSpace(item.Kind),
			subject,
			strings.TrimSpace(item.Reason),
		))
	}

	return strings.Join(labels, "; ")
}

func (card RecoveryCard) TurnPhaseSurfaceSummary(contract TurnPhaseContract) autonomy.TurnPhaseSurfaceSummaryEvent {
	card = NormalizeRecoveryCard(card)

	return autonomy.TurnPhaseSurfaceSummaryEvent{
		SchemaVersion:           autonomy.TurnPhaseSurfaceSummarySchemaVersion,
		SurfaceID:               "recovery_card",
		ContractID:              card.ContractID,
		PhaseID:                 firstRecoveryCardString(string(card.PhaseID), string(contract.PhaseID)),
		ModelCallID:             contract.ModelCallID,
		ProtocolHash:            contract.autonomyEventPayload().ProtocolHash,
		InvariantID:             card.InvariantID,
		AdvertisedActionClasses: append([]string(nil), card.CallableActionClasses...),
		CallableTools:           append([]string(nil), card.CallableTools...),
		TerminalOutcomes:        append([]string(nil), card.TerminalOutcomes...),
		TerminalResponseShapes:  append([]string(nil), card.TerminalOutcomes...),
		ObligationRefs:          append([]string(nil), card.EvidenceRefs...),
		RecoveryID:              recoveryCardRecoveryID(card.RecoveryState),
		RecoveryProjectionID:    recoveryCardRecoveryProjectionID(card.RecoveryState),
		TerminalBlockerAllowed:  recoveryCardRecoveryTerminalBlockerAllowed(card.RecoveryState),
		SafeAlternatives:        recoveryCardSurfaceSafeAlternatives(card),
	}
}

func copyPlanExecuteRecoveryState(state *PlanExecuteRecoveryState) *PlanExecuteRecoveryState {
	if state == nil {
		return nil
	}
	copy := *state
	copy.VisibleSafeAlternatives = append([]string(nil), state.VisibleSafeAlternatives...)
	return &copy
}

func (state PlanExecuteRecoveryState) label() string {
	parts := []string{
		"recovery_id=" + quoteRecoveryCardValue(state.RecoveryID),
		"projection_id=" + strings.TrimSpace(string(state.PhaseID)),
		"protocol_projection_id=" + quoteRecoveryCardValue(state.RecoveryCardProtocolProjectionID),
		"surface=" + quoteRecoveryCardValue(string(state.Surface)),
		"phase_id=" + quoteRecoveryCardValue(string(state.PhaseID)),
	}
	if state.TerminalBlockerAllowed {
		parts = append(parts, "terminal_blocker_allowed=true")
	}
	if len(state.VisibleSafeAlternatives) > 0 {
		parts = append(parts, "safe_alternatives="+quoteRecoveryCardValue(strings.Join(state.VisibleSafeAlternatives, ",")))
	}
	return strings.Join(parts, " ")
}

func recoveryCardRecoveryID(state *PlanExecuteRecoveryState) string {
	if state == nil {
		return ""
	}
	return state.RecoveryID
}

func recoveryCardRecoveryProjectionID(state *PlanExecuteRecoveryState) string {
	if state == nil {
		return ""
	}
	return state.RecoveryCardProtocolProjectionID
}

func recoveryCardRecoveryTerminalBlockerAllowed(state *PlanExecuteRecoveryState) bool {
	return state != nil && state.TerminalBlockerAllowed
}

func recoveryCardRecoverySafeAlternatives(state *PlanExecuteRecoveryState) []string {
	if state == nil {
		return nil
	}
	return append([]string(nil), state.VisibleSafeAlternatives...)
}

func recoveryCardSurfaceSafeAlternatives(card RecoveryCard) []string {
	if values := recoveryCardRecoverySafeAlternatives(card.RecoveryState); len(values) > 0 {
		return values
	}
	return append([]string(nil), card.VisibleSafeAlternatives...)
}

func recoveryCardVisibleSafeAlternatives(state *PlanExecuteRecoveryState, callableActionClasses []string) []string {
	if state != nil && len(state.VisibleSafeAlternatives) > 0 {
		return append([]string(nil), state.VisibleSafeAlternatives...)
	}
	return recoverySafeActionAlternatives(callableActionClasses)
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

	if action.Tool == completionGateToolName {
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
		parts = append(parts, "phase_id="+quoteRecoveryCardValue(string(card.PhaseID)))
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
	case strings.Contains(lower, "completion gate") && input.PhaseContract.PhaseID == contractcompile.TurnPhaseImplementationVerifyReady:
		return "completion.post_write_verification"
	case strings.Contains(lower, "completion gate"):
		return "completion.phase_contract"
	case strings.Contains(lower, workspace.ToolNameSubmitPlan):
		return "parser.submit_plan_required"
	case strings.Contains(lower, "not available") || strings.Contains(lower, "hidden"):
		return "surface.current_phase_tools"
	default:
		if strings.TrimSpace(string(input.PhaseContract.PhaseID)) != "" && input.PhaseContract.PhaseID != contractcompile.TurnPhaseGeneric {
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

	return textutil.Merge(repeats, contract.InvalidNext)
}

func recoveryCardCallableTools(contract TurnPhaseContract, currentAllowedTools []string) []string {
	phaseTools := firstNonEmptyRecoveryCardStrings(contract.ActionPolicy.CallableToolNames, contract.AllowedTools)
	current := textutil.Unique(currentAllowedTools)

	phaseTools = textutil.Remove(phaseTools, contract.HiddenTools)
	current = textutil.Remove(current, contract.HiddenTools)

	if len(phaseTools) == 0 && recoveryCardActionPolicyHasNoToolClasses(contract.ActionPolicy.AllowedActionClasses) {
		return nil
	}

	if len(current) == 0 {
		return textutil.Unique(phaseTools)
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

	return textutil.Unique(out)
}

func recoveryCardActionPolicyHasNoToolClasses(classes []string) bool {
	classes = textutil.Unique(classes)
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
	return recoveryCardCallableActionClassesForSurface(contract, recoveryCardCallableTools(contract, currentAllowedTools), currentAllowedTools)
}

func recoveryCardCallableActionClassesForSurface(contract TurnPhaseContract, callableTools []string, currentAllowedTools []string) []string {
	contractClasses := firstNonEmptyRecoveryCardStrings(
		contract.RuntimeCall.ExpressibleActionClasses,
		contract.ActionPolicy.AllowedActionClasses,
	)

	contractClasses = actionClassesAllowedByPolicy(contractClasses, contract.ActionPolicy.InvalidActionClasses)

	toolClasses := recoveryCardCallableActionClassesForTools(callableTools, contractClasses)
	if len(currentAllowedTools) > 0 && len(toolClasses) > 0 {
		return actionClassesAllowedByPolicy(
			textutil.Merge(toolClasses, recoveryCardTerminalOnlyActionClasses(contractClasses)),
			contract.ActionPolicy.InvalidActionClasses,
		)
	}

	if len(toolClasses) == 0 {
		return contractClasses
	}

	return actionClassesAllowedByPolicy(textutil.Merge(toolClasses, contractClasses), contract.ActionPolicy.InvalidActionClasses)
}

func recoveryCardTerminalOnlyActionClasses(classes []string) []string {
	var out []string
	for _, class := range classes {
		switch strings.TrimSpace(class) {
		case actionBlocker, actionFinalAnswer, actionSourceSatisfied, actionReviewPass:
			out = append(out, class)
		}
	}
	return textutil.Unique(out)
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
		case tool == workspace.ToolNameShell:
			if slicesContainRecoveryCardString(contractClasses, actionVerify) {
				classes = append(classes, actionVerify)
			} else {
				classes = append(classes, actionGenericProbe)
			}
		default:
			if class := autonomy.ActionClassToken(workspace.ToolActionClass(tool)); class != "" {
				classes = append(classes, class)
			}
		}
	}

	return textutil.Unique(classes)
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
	return textutil.Merge(
		contract.EvidenceState.AcceptanceRefs,
		contract.EvidenceState.VerificationEvidenceRefs,
		contract.EvidenceState.RecoveryPacketRefs,
		contract.EvidenceState.ResidualBlockers,
	)
}

func recoveryCardRejectedTool(req workspace.ToolRequest, result workspace.ToolResult) string {
	if recoveryRejectedResultIsSubmitPlan(req, result) {
		return workspace.ToolNameSubmitPlan
	}
	if tool := strings.TrimSpace(req.Tool); tool != "" {
		if tool == parserRecoveryToolName {
			return ""
		}
		return tool
	}

	tool := strings.TrimSpace(result.Tool)
	if tool == parserRecoveryToolName {
		return ""
	}

	return tool
}

func recoveryCardRejectedActionClass(req workspace.ToolRequest, result workspace.ToolResult) string {
	tool := recoveryCardRejectedTool(req, result)
	switch {
	case tool == completionGateToolName:
		return actionFinalAnswer
	case tool == workspace.ToolNameSubmitPlan:
		return actionSubmitPlan
	case workspace.IsWorkspaceWriteTool(tool):
		return actionPerEditWrite
	case workspace.AnalyzeVerificationRequest(workspace.ToolRequest{Tool: tool, Command: req.Command, Path: req.Path}).Attempted:
		return actionVerify
	case workspace.IsSearchToolName(tool):
		return actionSearch
	case workspace.IsReadTool(strings.TrimSpace(tool)):
		return actionRead
	default:
		return strings.TrimSpace(toolActionClass(req))
	}
}

func recoveryCardRejectedReason(result workspace.ToolResult) string {
	return firstRecoveryCardString(result.Summary, result.Error)
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
		if len(textutil.Unique(group)) > 0 {
			return textutil.Unique(group)
		}
	}

	return nil
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

func (r *runExecution) deriveCurrentTurnPhaseContract(input runnerTurnPhaseContractInput) TurnPhaseContract {
	brief := workerruntime.NormalizeRuntimeBrief(input.Brief)
	completionState := r.implementationCompletionState(input.Results)
	runtimePolicy := PlanExecuteRuntimeCallPolicyFromChatOptions(input.Options, input.SupportsNativeTools, false)
	responseFormat := ""
	if runtimePolicy.ResponseFormatJSON {
		responseFormat = "json"
	}

	return deriveTurnPhaseContract(TurnPhaseContractInput{
		ContractID:               strings.TrimSpace(input.ContractID),
		PhaseEpoch:               input.ModelCallNumber,
		ModelCallID:              runnerTurnPhaseModelCallID(input.ContractID, input.ModelCallNumber),
		Step:                     input.Step,
		PhaseHint:                input.PhaseHint,
		Brief:                    brief,
		Results:                  input.Results,
		ImplementationCompletion: &completionState,
		InspectionAuthority:      r.permissionAuthorizedInspection(r.workspaceAdvertisedAllowedTools()),
		BaseAllowedTools:         append([]string(nil), input.AllowedTools...),
		ActiveSurface:            input.ActiveSurface,
		BackendKind:              runnerTurnPhaseBackendKind(r),
		ModelAlias:               input.Options.Model,
		ToolTransportMode:        runtimePolicy.ToolTransportMode(),
		ParserContract:           runnerTurnPhaseParserContract(input.Options, brief, input.Results, input.SupportsNativeTools, r.planThenExecutePathAuthority()),
		ResponseFormat:           responseFormat,
		Temperature:              runtimePolicy.Temperature,
		TopP:                     runtimePolicy.TopP,
		TopK:                     runtimePolicy.TopK,
		MinP:                     runtimePolicy.MinP,
		PresencePenalty:          runtimePolicy.PresencePenalty,
		RepetitionPenalty:        runtimePolicy.RepetitionPenalty,
		ToolChoice:               runtimePolicy.ToolChoice,
		ThinkingPolicy:           runtimePolicy.ThinkingPolicy,
		MaxTokens:                runtimePolicy.MaxTokens,
		SupportsNativeTools:      input.SupportsNativeTools,
		TextualToolParser:        runtimePolicy.TextualToolParser,
		AcceptanceRefs:           append([]string(nil), brief.RequiredEvidence...),
		VerificationEvidenceRefs: runnerVerificationEvidenceRefs(input.Results),
		VerificationStatus:       runnerAutonomyVerificationStatus(input.Results, brief, completionState),
		ResidualBlockers:         runnerTurnPhaseResidualBlockers(input.Results),
	})
}

func applyTurnPhaseContractToNextActionState(state runnerNextActionState, contract TurnPhaseContract) runnerNextActionState {
	if contract.PhaseID == "" {
		return state
	}

	if contract.PhaseID == contractcompile.TurnPhaseGeneric && strings.TrimSpace(contract.Phase) == "generic" {
		return state
	}

	if strings.TrimSpace(contract.Phase) != "" {
		state.Phase = contract.Phase
	}

	if len(contract.RequiredNext) > 0 {
		state.NextValidActions = append([]string(nil), contract.RequiredNext...)
	} else if turnPhaseContractTerminalOutcomesDriveNextActions(contract) {
		state.NextValidActions = append([]string(nil), contract.TerminalOutcomes...)
		if slices.Contains(contract.TerminalOutcomes, actionSubmitPlan) {
			state.NextValidActions = []string{"submit_plan using edits[] for changed paths and source_satisfied_paths for already-final paths, Decision: source-satisfied with per-path current-state evidence, or <<QUESTION>> with the exact blocker"}
		}
	}

	if len(contract.InvalidNext) > 0 {
		state.InvalidNextActions = append([]string(nil), contract.InvalidNext...)
	}

	state.Constraints = textutil.Merge(state.Constraints, contract.Constraints)
	state.StateSummary = runnerStateSummary(state)

	return state
}

func turnPhaseSurfaceSummaryFromNextActionState(state runnerNextActionState, contract TurnPhaseContract) autonomy.TurnPhaseSurfaceSummaryEvent {
	payload := contract.autonomyEventPayload()
	advertisedClasses := turnPhaseSurfaceAdvertisedActionClasses(contract)
	invalidClasses := removeAllowedTurnPhaseInvalidActionClasses(
		append([]string(nil), contract.ActionPolicy.InvalidActionClasses...),
		advertisedClasses,
	)

	return autonomy.TurnPhaseSurfaceSummaryEvent{
		SchemaVersion:           autonomy.TurnPhaseSurfaceSummarySchemaVersion,
		SurfaceID:               "next_action_state",
		ContractID:              contract.ContractID,
		PhaseID:                 string(contract.PhaseID),
		ModelCallID:             contract.ModelCallID,
		ProtocolHash:            payload.ProtocolHash,
		AdvertisedActionClasses: advertisedClasses,
		InvalidActionClasses:    invalidClasses,
		ObligationRefs:          append([]string(nil), state.EvidencePaths...),
		RuntimeCall:             payload.RuntimeCall,
	}
}

func turnPhaseSurfaceAdvertisedActionClasses(contract TurnPhaseContract) []string {
	allowed := actionClassesAllowedByPolicy(contract.ActionPolicy.AllowedActionClasses, contract.ActionPolicy.InvalidActionClasses)
	if len(contract.RuntimeCall.ExpressibleActionClasses) > 0 {
		if len(allowed) == 0 {
			allowed = actionClassesAllowedByPolicy(contract.RuntimeCall.ExpressibleActionClasses, contract.ActionPolicy.InvalidActionClasses)
		} else {
			allowed = actionClassIntersection(allowed, contract.RuntimeCall.ExpressibleActionClasses)
		}
	}

	return textutil.Unique(allowed)
}

func actionClassIntersection(values []string, allowed []string) []string {
	if len(values) == 0 || len(allowed) == 0 {
		return nil
	}

	allowedSet := map[string]struct{}{}
	for _, value := range allowed {
		value = strings.TrimSpace(value)
		if value != "" {
			allowedSet[value] = struct{}{}
		}
	}

	var out []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}

		if _, ok := allowedSet[value]; !ok {
			continue
		}

		out = append(out, value)
	}

	return textutil.Unique(out)
}

func turnPhaseContractTerminalOutcomesDriveNextActions(contract TurnPhaseContract) bool {
	if len(contract.TerminalOutcomes) == 0 {
		return false
	}

	phaseID := strings.TrimSpace(string(contract.PhaseID))

	return strings.Contains(phaseID, "submit_ready") ||
		strings.Contains(phaseID, "source_satisfied_terminal") ||
		strings.Contains(phaseID, "review_repair") ||
		strings.Contains(phaseID, "final")
}

func removeAllowedTurnPhaseInvalidActionClasses(invalidClasses []string, allowedClasses []string) []string {
	if len(invalidClasses) == 0 || len(allowedClasses) == 0 {
		return invalidClasses
	}

	out := invalidClasses[:0]
	for _, actionClass := range invalidClasses {
		if slices.Contains(allowedClasses, actionClass) {
			continue
		}

		out = append(out, actionClass)
	}

	return out
}

type runnerTurnPhaseContractInput struct {
	ContractID          string
	ModelCallNumber     int
	Step                int
	PhaseHint           contractcompile.TurnPhaseID
	Brief               workerruntime.WorkerRuntimeBrief
	Results             []workspace.ToolResult
	AllowedTools        []string
	ActiveSurface       ActiveSurface
	Options             llama.ChatOptions
	SupportsNativeTools bool
}

func runnerTurnPhaseModelCallID(contractID string, modelCallNumber int) string {
	contractID = strings.TrimSpace(contractID)
	if contractID == "" {
		return fmt.Sprintf("model_call_%d", modelCallNumber)
	}

	return fmt.Sprintf("%s:model_call_%d", contractID, modelCallNumber)
}

func runnerTurnPhaseBackendKind(r *runExecution) string {
	if r == nil || r.runner.Client == nil {
		return ""
	}

	return r.runner.Client.Backend()
}

func runnerTurnPhaseParserContract(opts llama.ChatOptions, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, supportsNativeTools bool, authority planThenExecutePathAuthority) string {
	state := derivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true}, authority)
	if state.Mode == PlanExecutePlanningModeAnchorRefresh {
		return "source_refresh"
	}

	if opts.ResponseFormatJSON {
		return "submit_plan_json"
	}

	if len(opts.Tools) == 1 && strings.TrimSpace(opts.Tools[0].Function.Name) == workspace.ToolNameSubmitPlan && supportsNativeTools {
		return "native_submit_plan"
	}

	return ""
}

func runnerTurnPhaseThinkingPolicy(opts llama.ChatOptions) string {
	policy := thinkingOptionForLog(opts.EnableThinking)
	if opts.PreserveThinking {
		return policy + "_preserve"
	}

	return policy + "_visible"
}

func runnerVerificationEvidenceRefs(results []workspace.ToolResult) []string {
	var refs []string

	for _, result := range results {
		if !workspace.AnalyzeVerificationResult(result).Successful {
			continue
		}

		summary := strings.TrimSpace(result.Summary)
		if summary == "" {
			summary = strings.TrimSpace(result.Tool)
		}

		if summary != "" {
			refs = append(refs, summary)
		}
	}

	return textutil.Unique(refs)
}

func runnerTurnPhaseResidualBlockers(results []workspace.ToolResult) []string {
	var blockers []string

	for _, result := range results {
		if strings.TrimSpace(result.Error) == "" {
			continue
		}

		if strings.TrimSpace(result.Tool) == completionGateToolName {
			continue
		}

		if strings.Contains(strings.ToLower(result.Summary), "blocked") {
			blockers = append(blockers, result.Summary)
		}
	}

	return textutil.Unique(blockers)
}

// completionGateCheck inspects a model reply and accumulated results, returning a rejection
// ToolResult with shouldReject=true, or an empty ToolResult with false.
type completionGateCheck func(reply string, results []workspace.ToolResult) (rejection workspace.ToolResult, shouldReject bool)

func typedCompletionGateCheck(invariantID string, violationID string, kind completionGateViolationClass, check completionGateCheck) completionGateCheck {
	return func(reply string, results []workspace.ToolResult) (workspace.ToolResult, bool) {
		result, rejected := check(reply, results)
		if !rejected {
			return result, false
		}
		if _, typed := completionGateRecoveryEnvelopeFromResult(result); typed {
			return result, true
		}
		return annotateCompletionGateRecoveryEnvelope(
			result,
			newCompletionGateRecoveryEnvelope(invariantID, violationID, kind, nil),
		), true
	}
}

// completionGatePipeline evaluates checks in registration order and returns the first
// rejection. If no check rejects, the pipeline passes.
type completionGatePipeline struct {
	checks []completionGateCheck
}

func newCompletionGatePipeline(checks ...completionGateCheck) *completionGatePipeline {
	return &completionGatePipeline{checks: append([]completionGateCheck(nil), checks...)}
}

func (p *completionGatePipeline) Evaluate(reply string, results []workspace.ToolResult) (workspace.ToolResult, bool) {
	if p == nil {
		return workspace.ToolResult{}, false
	}

	for _, check := range p.checks {
		if rejection, rejected := check(reply, results); rejected {
			return rejection, true
		}
	}

	return workspace.ToolResult{}, false
}

type RuntimeCallPolicyInput struct {
	SupportsNativeTools      bool
	ForceNativeTools         bool
	TextualToolParser        bool
	TerminalOnly             bool
	ResponseFormatJSON       bool
	Temperature              float64
	TopP                     float64
	TopK                     int
	MinP                     float64
	PresencePenalty          float64
	RepetitionPenalty        float64
	ToolChoice               string
	ParserContract           string
	ResponseFormat           string
	ThinkingPolicy           string
	EnableThinking           *bool
	PreserveThinking         bool
	ThinkingBudget           int
	ChatTemplateKwargs       bool
	MaxTokens                int
	TimeoutMillis            int
	ExpressibleActionClasses []string
}

type RuntimeCallPolicy struct {
	SupportsNativeTools      bool
	ForceNativeTools         bool
	TextualToolParser        bool
	TerminalOnly             bool
	ResponseFormatJSON       bool
	Temperature              float64
	TopP                     float64
	TopK                     int
	MinP                     float64
	PresencePenalty          float64
	RepetitionPenalty        float64
	ToolChoice               string
	ToolChoiceRequired       bool
	ParserContract           string
	ResponseFormat           string
	ThinkingPolicy           string
	EnableThinking           *bool
	PreserveThinking         bool
	ThinkingBudget           int
	ChatTemplateKwargs       bool
	MaxTokens                int
	TimeoutMillis            int
	TransportMode            ToolCallTransportMode
	ExpressibleActionClasses []string
}

func DeriveRuntimeCallPolicy(input RuntimeCallPolicyInput) RuntimeCallPolicy {
	responseFormatJSON := input.ResponseFormatJSON || textutil.EqualTrimFold(input.ResponseFormat, "json")
	textualParser := input.TextualToolParser || responseFormatJSON || (!input.SupportsNativeTools && !input.ForceNativeTools)
	policy := RuntimeCallPolicy{
		SupportsNativeTools:      input.SupportsNativeTools,
		ForceNativeTools:         input.ForceNativeTools,
		TextualToolParser:        textualParser,
		TerminalOnly:             input.TerminalOnly,
		ResponseFormatJSON:       responseFormatJSON,
		Temperature:              input.Temperature,
		TopP:                     input.TopP,
		TopK:                     input.TopK,
		MinP:                     input.MinP,
		PresencePenalty:          input.PresencePenalty,
		RepetitionPenalty:        input.RepetitionPenalty,
		ToolChoice:               strings.TrimSpace(input.ToolChoice),
		ToolChoiceRequired:       textutil.EqualTrimFold(input.ToolChoice, "required"),
		ParserContract:           strings.TrimSpace(input.ParserContract),
		ResponseFormat:           strings.TrimSpace(input.ResponseFormat),
		ThinkingPolicy:           strings.TrimSpace(input.ThinkingPolicy),
		EnableThinking:           cloneBoolPtr(input.EnableThinking),
		PreserveThinking:         input.PreserveThinking,
		ThinkingBudget:           input.ThinkingBudget,
		ChatTemplateKwargs:       input.ChatTemplateKwargs,
		MaxTokens:                input.MaxTokens,
		TimeoutMillis:            input.TimeoutMillis,
		ExpressibleActionClasses: textutil.Unique(input.ExpressibleActionClasses),
	}

	policy.TransportMode = deriveRuntimeCallTransportMode(policy)

	return policy
}

func (policy RuntimeCallPolicy) ToolTransportMode() string {
	switch policy.TransportMode {
	case ToolCallTransportModeNative:
		return "native"
	case ToolCallTransportModeTextual, ToolCallTransportModeTextualJSON:
		return "textual"
	default:
		return ""
	}
}

func (policy RuntimeCallPolicy) ResponseFormatMode() string {
	if policy.ResponseFormatJSON {
		return "json"
	}

	if responseFormat := strings.TrimSpace(policy.ResponseFormat); responseFormat != "" {
		return responseFormat
	}

	return "default"
}

func deriveRuntimeCallTransportMode(policy RuntimeCallPolicy) ToolCallTransportMode {
	if policy.ForceNativeTools || (policy.SupportsNativeTools && !policy.TextualToolParser && !policy.ResponseFormatJSON) {
		return ToolCallTransportModeNative
	}

	if policy.TerminalOnly || policy.ResponseFormatJSON {
		return ToolCallTransportModeTextualJSON
	}

	return ToolCallTransportModeTextual
}

func cloneBoolPtr(value *bool) *bool {
	if value == nil {
		return nil
	}

	cloned := *value

	return &cloned
}

type Owner string

const (
	OwnerImplementationScoped Owner = "implementation_scoped"
	OwnerSourceGrounding      Owner = "source_grounding"
	OwnerChildPacket          Owner = "child_packet"
	OwnerLoopPolicy           Owner = "loop_policy"
	OwnerListFilesGate        Owner = "list_files_gate"
	OwnerTaskDelegation       Owner = "task_delegation"
)

// ActiveSurface is the runtime projection of callable tools, hidden tools, and
// terminal response shapes for one model turn.
type ActiveSurface struct {
	VisibleTools           []string
	HiddenTools            []string
	CallableActionClasses  []string
	InvalidActionClasses   []string
	TerminalResponseShapes []string
	RequiredActionClasses  []string

	hiddenOwners map[string]map[Owner]struct{}
}

type ToolRejection struct {
	Summary string
	Error   string
	Reject  bool
}

func NewActiveSurface(visibleTools []string, hiddenTools []string) ActiveSurface {
	surface := ActiveSurface{}
	surface.SetVisibleTools(visibleTools)
	for _, tool := range uniqueTrimmed(hiddenTools) {
		surface.Hide("", tool)
	}
	return surface
}

func (s *ActiveSurface) SetVisibleTools(tools []string) {
	if s == nil {
		return
	}
	s.VisibleTools = s.FilterVisible(tools)
}

func (s *ActiveSurface) Hide(owner Owner, tools ...string) {
	if s == nil {
		return
	}
	if s.hiddenOwners == nil {
		s.hiddenOwners = make(map[string]map[Owner]struct{})
	}
	for _, tool := range uniqueTrimmed(tools) {
		owners := s.hiddenOwners[tool]
		if owners == nil {
			owners = make(map[Owner]struct{})
			s.hiddenOwners[tool] = owners
		}
		owners[owner] = struct{}{}
	}
	s.refreshHiddenProjection()
}

func (s *ActiveSurface) HideExcept(owner Owner, candidates []string, allow func(string) bool) {
	if s == nil {
		return
	}
	for _, tool := range uniqueTrimmed(candidates) {
		if allow != nil && allow(tool) {
			continue
		}
		s.Hide(owner, tool)
	}
}

func (s *ActiveSurface) Release(owner Owner) {
	if s == nil || s.hiddenOwners == nil {
		return
	}
	for tool, owners := range s.hiddenOwners {
		delete(owners, owner)
		if len(owners) == 0 {
			delete(s.hiddenOwners, tool)
		}
	}
	s.refreshHiddenProjection()
}

func (s ActiveSurface) HiddenByOwner(owner Owner, tool string) bool {
	tool = strings.TrimSpace(tool)
	if tool == "" || s.hiddenOwners == nil {
		return false
	}

	owners := s.hiddenOwners[tool]
	_, ok := owners[owner]
	return ok
}

func (s ActiveSurface) AllowsTool(tool string) bool {
	return slices.Contains(s.VisibleTools, strings.TrimSpace(tool))
}

func (s ActiveSurface) HidesTool(tool string) bool {
	return slices.Contains(s.HiddenTools, strings.TrimSpace(tool))
}

func (s ActiveSurface) RejectionForTool(tool string) ToolRejection {
	tool = strings.TrimSpace(tool)
	if tool == "" || s.AllowsTool(tool) {
		return ToolRejection{}
	}

	if len(s.VisibleTools) == 0 {
		return ToolRejection{
			Summary: "Tool not available in current runtime contract",
			Error:   tool + " is not available because the current runtime contract exposes no tools. Answer from gathered evidence or report a concrete blocker.",
			Reject:  true,
		}
	}

	return ToolRejection{
		Summary: "Tool not available in current runtime contract",
		Error:   tool + " is not in the current allowed tool surface. Available tools: " + strings.Join(s.VisibleTools, ", ") + ". Use an available tool, answer from gathered evidence, or report a concrete blocker.",
		Reject:  true,
	}
}

func (s ActiveSurface) FilterVisible(tools []string) []string {
	hidden := s.hiddenSet()
	visible := uniqueTrimmed(tools)
	filteredVisible := visible[:0]
	for _, tool := range visible {
		if _, ok := hidden[tool]; ok {
			continue
		}
		filteredVisible = append(filteredVisible, tool)
	}
	return append([]string(nil), filteredVisible...)
}

func (s *ActiveSurface) refreshHiddenProjection() {
	if s == nil {
		return
	}
	s.HiddenTools = mapKeysSorted(s.hiddenOwners)
	s.VisibleTools = s.FilterVisible(s.VisibleTools)
}

func (s ActiveSurface) hiddenSet() map[string]struct{} {
	hidden := make(map[string]struct{}, len(s.HiddenTools)+len(s.hiddenOwners))
	for _, tool := range s.HiddenTools {
		tool = strings.TrimSpace(tool)
		if tool != "" {
			hidden[tool] = struct{}{}
		}
	}
	for tool, owners := range s.hiddenOwners {
		if strings.TrimSpace(tool) != "" && len(owners) > 0 {
			hidden[tool] = struct{}{}
		}
	}
	return hidden
}

func uniqueTrimmed(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func mapKeysSorted(values map[string]map[Owner]struct{}) []string {
	keys := make([]string, 0, len(values))
	for key, owners := range values {
		key = strings.TrimSpace(key)
		if key == "" || len(owners) == 0 {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
