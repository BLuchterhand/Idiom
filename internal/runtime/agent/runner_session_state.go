package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/autonomy"
	contractcompile "idiom/internal/contracts/compile"
	"idiom/internal/contracts/pathparse"
	recoverycontract "idiom/internal/contracts/recovery"
	"idiom/internal/contracts/toolfacts"
	"idiom/internal/contracts/toolpolicy"
	compactionevidence "idiom/internal/evidence/compaction"
	"idiom/internal/platform/textutil"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/workerruntime"
)

func (r *runExecution) emitThinkingTrace(step int, reason string, message llama.ChatMessage, opts llama.ChatOptions, preservedInHistory bool) {
	if r != nil && r.runner.OnThought != nil && !preservedInHistory {
		for _, text := range thoughtSegmentsFromMessage(message) {
			r.runner.OnThought(ThoughtEvent{Step: step, Text: text})
		}
	}

	if r == nil || r.runner.OnEvent == nil {
		return
	}

	if opts.EnableThinking == nil && !opts.PreserveThinking && strings.TrimSpace(message.ReasoningContent) == "" {
		return
	}

	trace := runnerThinkingTrace{
		Lane:               r.agentName(),
		Step:               step,
		Reason:             strings.TrimSpace(reason),
		EnableThinking:     thinkingOptionForLog(opts.EnableThinking),
		PreserveThinking:   opts.PreserveThinking,
		ContentChars:       len(message.Content),
		ReasoningChars:     len(message.ReasoningContent),
		PreservedInHistory: preservedInHistory && strings.TrimSpace(message.ReasoningContent) != "",
	}
	if strings.TrimSpace(reason) == "completion_gate_reply_preserved" {
		trace.ContentPreview = textutil.Truncate(strings.TrimSpace(message.Content), 500)
	}

	data, err := json.Marshal(trace)
	if err != nil {
		return
	}

	r.runner.OnEvent(RunEvent{Role: "thinking_trace", Content: string(data)})
}

func thoughtSegmentsFromMessage(message llama.ChatMessage) []string {
	var segments []string
	segments = appendThoughtSegments(segments, message.ReasoningContent)
	segments = appendThoughtSegments(segments, extractThinkTagContent(message.Content)...)
	return segments
}

func appendThoughtSegments(segments []string, values ...string) []string {
	for _, value := range values {
		for _, segment := range splitThoughtSegments(value) {
			if segment != "" {
				segments = append(segments, segment)
			}
		}
	}

	return segments
}

func splitThoughtSegments(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}

	chunks := strings.Split(text, "\n\n")
	if len(chunks) == 1 {
		chunks = strings.Split(text, "\n")
	}

	var out []string
	for _, chunk := range chunks {
		chunk = strings.TrimSpace(chunk)
		if chunk == "" {
			continue
		}

		out = append(out, chunk)
	}

	return out
}

func extractThinkTagContent(text string) []string {
	var out []string
	for {
		start := strings.Index(text, "<think>")
		if start < 0 {
			return out
		}

		rest := text[start+len("<think>"):]
		end := strings.Index(rest, "</think>")
		if end < 0 {
			if segment := strings.TrimSpace(rest); segment != "" {
				out = append(out, segment)
			}

			return out
		}

		if segment := strings.TrimSpace(rest[:end]); segment != "" {
			out = append(out, segment)
		}

		text = rest[end+len("</think>"):]
	}
}

func (r *runExecution) chatMessagesForOptions(messages []llama.ChatMessage, opts llama.ChatOptions) []llama.ChatMessage {
	return llama.PrepareChatMessagesForBackend(messages, opts, r.clientBackend())
}

func (r *runExecution) assistantHistoryMessageForOptions(message llama.ChatMessage, opts llama.ChatOptions) llama.ChatMessage {
	return llama.PrepareAssistantHistoryMessageForBackend(message, opts, r.clientBackend())
}

func (r *runExecution) clientBackend() string {
	if r == nil || r.runner.Client == nil {
		return ""
	}

	return r.runner.Client.Backend()
}

func thinkingOptionForLog(enableThinking *bool) string {
	if enableThinking == nil {
		return "auto"
	}

	if *enableThinking {
		return "on"
	}

	return "off"
}

func retryableModelTransportError(err error) bool {
	if err == nil {
		return false
	}

	text := strings.ToLower(strings.TrimSpace(err.Error()))
	if text == "" {
		return false
	}

	if text == "eof" || strings.HasSuffix(text, ": eof") {
		return true
	}

	for _, pattern := range []string{
		"transport failed",
		"connection reset",
		"connection refused",
		"deadline exceeded",
		"i/o timeout",
		"network is unreachable",
		"no such host",
	} {
		if strings.Contains(text, pattern) {
			return true
		}
	}

	return false
}

func modelCapacityFailure(err error) bool {
	if err == nil {
		return false
	}

	text := strings.ToLower(strings.TrimSpace(err.Error()))
	return textutil.ContainsAny(text,
		"metal::malloc",
		"resource limit",
		"out of memory",
		"memory allocation failed",
		"allocation failed",
		"cuda out of memory",
	)
}

func modelCapacityRecoveryOptions(opts llama.ChatOptions) llama.ChatOptions {
	thinkingOff := false
	opts.EnableThinking = &thinkingOff
	opts.PreserveThinking = false
	opts.ThinkingBudget = 0

	if opts.MaxTokens > 0 {
		// Keep the recovery request proportional to the configured budget while
		// ensuring it remains large enough to emit one useful answer or tool call.
		reduced := opts.MaxTokens / 4
		if reduced < 1024 {
			reduced = 1024
		}
		opts.MaxTokens = reduced
	}

	return opts
}

var (
	stableHandlePathLinePattern     = regexp.MustCompile(`(?m)^f\d+\s+(.+)$`)
	stableHandleInlinePattern       = regexp.MustCompile(`\bf\d+\s+((?:[[:alnum:]_.-]+/)*[[:alnum:]_.-]+)\b`)
	delegatedPacketPathLinePattern  = regexp.MustCompile(`((?:[[:alnum:]_.-]+/)*[[:alnum:]_.-]+\.(?:go|c|cc|cpp|cxx|h|hh|hpp|hxx|ipp|inl|tpp|gd|tscn|tres|py|pyi|ts|tsx|js|jsx|mjs|cjs|rs|java|kt|kts|sh|bash|zsh|fish|ps1|md|mdx|json|jsonl|yaml|yml|toml|xml|ini|cfg|conf|env|txt|csv|tsv)):\d+(?:-\d+)?(?:[:][^\s` + "`" + `"'(),.;:]+)?`)
	delegatedPacketPathQuotePattern = regexp.MustCompile("((?:[[:alnum:]_.-]+/)*[[:alnum:]_.-]+\\.(?:go|c|cc|cpp|cxx|h|hh|hpp|hxx|ipp|inl|tpp|gd|tscn|tres|py|pyi|ts|tsx|js|jsx|mjs|cjs|rs|java|kt|kts|sh|bash|zsh|fish|ps1|md|mdx|json|jsonl|yaml|yml|toml|xml|ini|cfg|conf|env|txt|csv|tsv))[`'\"]{2,}")
	closurePathLineSymbolPattern    = regexp.MustCompile(`(?m)(?:[[:alnum:]_.-]+/)*[[:alnum:]_.-]+\.(?:go|c|cc|cpp|cxx|h|hh|hpp|hxx|ipp|inl|tpp|gd|py|pyi|ts|tsx|js|jsx|rs|java|kt|kts|sh|md|json|yaml|yml|toml|txt):\d+(?::\d+)?[: ]+([A-Za-z_][A-Za-z0-9_]{3,})\b`)
	closurePathScopedSymbolPattern  = regexp.MustCompile(`(?m)((?:[[:alnum:]_.-]+/)*[[:alnum:]_.-]+\.(?:go|c|cc|cpp|cxx|h|hh|hpp|hxx|ipp|inl|tpp|gd|py|pyi|ts|tsx|js|jsx|rs|java|kt|kts|sh|md|json|yaml|yml|toml|txt)):\d+(?::\d+)?[: ]+([A-Za-z_][A-Za-z0-9_]{3,})\b`)
	closurePathCallPattern          = regexp.MustCompile(`(?m)((?:[[:alnum:]_.-]+/)*[[:alnum:]_.-]+\.(?:go|c|cc|cpp|cxx|h|hh|hpp|hxx|ipp|inl|tpp|gd|py|pyi|ts|tsx|js|jsx|rs|java|kt|kts|sh|md|json|yaml|yml|toml|txt)):\d+(?::\d+)?:[^\n]*?\b([A-Za-z_][A-Za-z0-9_]{3,})\s*\(`)
	callLikeIdentifierPattern       = regexp.MustCompile(`\b([A-Za-z_][A-Za-z0-9_]{3,})\s*\(`)
	qualifiedIdentifierPattern      = regexp.MustCompile(`\b([A-Za-z_][A-Za-z0-9_]{1,}\.[A-Za-z_][A-Za-z0-9_]{3,})\b`)
	backtickedTokenPattern          = regexp.MustCompile("`([^`]+)`")
	toolLocalSearchHandlePattern    = regexp.MustCompile(`\bf\d+\s+(?:[[:alnum:]_.-]+/)+[[:alnum:]_.-]+\.[[:alnum:]]+:\d+:`)
	goFuncDeclPattern               = regexp.MustCompile(`(?m)^func\s+(?:\([^)]+\)\s*)?([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
	codeShapedTokenPattern          = regexp.MustCompile(`\b[A-Za-z][a-z]+(?:[A-Z][a-z0-9]+){1,}\b`)
	codeShapedSnakeCasePattern      = regexp.MustCompile(`\b[a-z][a-z0-9]*(?:_[a-z0-9]+){1,}\b`)
	fileOutlineDeclarationPattern   = regexp.MustCompile(`(?m)^-\s+([A-Za-z_][A-Za-z0-9_\.]*)\s+lines\s+(\d+)-(\d+)\b`)
	fileOutlinePreviewPattern       = regexp.MustCompile(`preview declarations:\s*\[([^\]]*)\]`)
)

func runnerLivePhase(brief workerruntime.WorkerRuntimeBrief, req workspace.ToolRequest, result workspace.ToolResult, results []workspace.ToolResult, mutationCommitted bool) string {
	if phase := debugLogPhase(brief, results); phase != "" {
		return phase
	}

	if phase := evidenceFirstMutationPhase(brief, results, mutationCommitted); phase != "" {
		return phase
	}

	return workerruntime.ClassifyPhase(req, result)
}

func debugLogPhase(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) string {
	if !debugLogContractApplies(brief, results) {
		return ""
	}

	if debugLogEvidenceSatisfied(brief, results) {
		return "log_analysis"
	}

	if _, ok := latestDebugLogSearchHit(results); ok {
		return "failure_window"
	}

	selection, ok := latestDebugLogSelection(results)
	if !ok {
		return "latest_log_selection"
	}

	if hasDebugLogReadForSelection(results, selection) {
		return "selected_log_analysis"
	}

	return "latest_log_tail"
}

func evidenceFirstMutationPhase(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, mutationCommitted bool) string {
	if !workerruntime.TaskFamilyEvidenceFirstMutation(brief.TaskFamily) || len(brief.TargetRefs) == 0 {
		return ""
	}

	if mutationCommitted {
		return "post_target_write"
	}

	targetReadCount := successfulTargetReadCount(results, brief.TargetRefs)
	if !hasFocusedNonTargetEvidence(results, brief.TargetRefs) {
		return "source_evidence"
	}

	if targetReadCount == 0 {
		return "target_entry"
	}

	return "target_inspection"
}

func buildRunnerNextActionStateWithAllowedToolsAndContract(agent string, step int, reason string, req workspace.ToolRequest, result workspace.ToolResult, results []workspace.ToolResult, declarationMovesForbidden bool, brief workerruntime.WorkerRuntimeBrief, currentAllowedTools []string, options ...any) (runnerNextActionState, TurnPhaseContract) {
	planningTransport := runnerNextActionPlanningTransport(options)
	completionState := runnerNextActionCompletionSnapshot(options)
	mutationAuthority := runnerNextActionMutationAuthority(options)
	inspectionAuthority := runnerNextActionInspectionAuthority(options)
	surfaceReq, surfaceResult := modelFacingParserRejectionSurface(reason, req, result, recoveryToolTransportMode(planningTransport))
	tool := strings.TrimSpace(req.Tool)
	if tool == "" {
		tool = strings.TrimSpace(result.Tool)
	}
	surfaceTool := strings.TrimSpace(surfaceReq.Tool)
	if surfaceTool == "" {
		surfaceTool = strings.TrimSpace(surfaceResult.Tool)
	}

	phaseCompletion := implementationCompletionState{}
	if completionState != nil {
		phaseCompletion = *completionState
	}
	brief = runnerBriefWithPhaseHints(brief, results, mutationAuthority, phaseCompletion)

	guidance := workerruntime.BuildActionGuidance(workerruntime.ActionGuidanceInput{
		Reason:                    reason,
		Request:                   req,
		Result:                    result,
		Constraints:               runnerGuidanceConstraints(agent, brief, results, mutationAuthority),
		DeclarationMovesForbidden: declarationMovesForbidden,
		EvidenceIntent:            brief.EvidenceIntent,
		WriteCapable:              runnerHasWorkspaceWriteCapability(brief, brief.AllowedTools),
		ToolTransportMode:         recoveryToolTransportMode(planningTransport),
	})
	projection := newToolResultEvidenceProjection(results)
	state := runnerNextActionState{
		Lane:              strings.TrimSpace(agent),
		Phase:             runnerLivePhase(brief, surfaceReq, surfaceResult, results, phaseCompletion.HasChanges()),
		Step:              step,
		Reason:            strings.TrimSpace(reason),
		LastTool:          surfaceTool,
		LastTarget:        workerruntime.ToolRequestTarget(surfaceReq),
		ResultSummary:     textutil.Truncate(strings.TrimSpace(surfaceResult.Summary), 220),
		ResultError:       textutil.Truncate(strings.TrimSpace(surfaceResult.Error), 260),
		ResultOutputChars: len(surfaceResult.Output),
		EvidencePaths:     projection.evidencePathsForBrief(brief, 6),
	}
	// Surface producer-ranked context symbols as actionable annotations.
	if strings.TrimSpace(tool) == workspace.ToolNameContextPack && strings.TrimSpace(result.Error) == "" && result.Facts != nil {
		state.CandidateSymbols = textutil.Prefix(result.Facts.CandidateSymbols, 5)
	}

	missingRequiredPaths := append([]string(nil), brief.RequiredEditPaths...)
	if completionState != nil {
		missingRequiredPaths = append([]string(nil), completionState.MissingRequiredPaths...)
	}
	if status := scratchpadDeliverableFailedEvidenceStatus(brief, results, missingRequiredPaths); status != "" {
		state.EvidenceStatus = []string{status}
	}

	if strings.HasPrefix(strings.TrimSpace(agent), "child:") && delegatedReadOnlyClosureReady(agent, brief, results) {
		state.EvidencePaths = projection.delegatedClosureEvidencePaths(brief.Objective, 6)
	}

	if !runnerBriefRequiresMutation(brief) &&
		!strings.HasPrefix(strings.TrimSpace(agent), "child:") &&
		projection.postChildPacketClosureReady(brief) {

		state.EvidencePaths = projection.postChildClosureEvidencePaths(brief.Objective, 6)
	}

	state.Constraints = textutil.Merge(brief.Constraints, guidance.Constraints)
	state.InvalidNextActions = textutil.Merge(brief.InvalidActions, guidance.InvalidNext)
	state.NextValidActions = textutil.Merge(brief.RequiredNext, guidance.ValidNext)

	var phaseHint contractcompile.TurnPhaseID

	if reviewRepairTerminalOutcomeRequiredState(brief, result) {
		phaseHint = contractcompile.TurnPhaseReviewRepairTerminal
	}

	phaseContract := deriveTurnPhaseContract(TurnPhaseContractInput{
		PhaseHint:                phaseHint,
		Brief:                    brief,
		Results:                  results,
		LastRequest:              req,
		LastResult:               result,
		ImplementationCompletion: completionState,
		mutationAuthority:        mutationAuthority,
		InspectionAuthority:      inspectionAuthority,
		BaseAllowedTools:         runnerCurrentAllowedOrBriefTools(currentAllowedTools, brief.AllowedTools),
		SupportsNativeTools:      planningTransport.SupportsNativeTools,
		TextualToolParser:        planningTransport.TextualToolParser,
	})

	state = applyTurnPhaseContractToNextActionState(state, phaseContract)
	if recoveryState, ok := derivePlanExecuteRecoveryState(brief, results, planningTransport, phaseContract, req, result, mutationAuthority); ok {
		state.RecoveryState = &recoveryState
	}

	if strings.TrimSpace(result.Error) != "" {
		card := BuildRecoveryCard(RecoveryCardInput{
			PhaseContract:       phaseContract,
			CurrentAllowedTools: effectiveRecoveryCardAllowedTools(currentAllowedTools, brief, results, phaseContract),
			RejectedRequest:     surfaceReq,
			RejectedResult:      surfaceResult,
			EvidenceRefs:        state.EvidencePaths,
			RecoveryState:       state.RecoveryState,
		})
		state.recoveryCard = &card
		state.RecoveryCard = RenderRecoveryCard(card)
	}

	if strings.TrimSpace(reason) == parserRecoveryRecordReason {
		state.InvalidNextActions = textutil.Merge(state.InvalidNextActions, guidance.InvalidNext)
		if len(guidance.ValidNext) > 0 {
			state.NextValidActions = append([]string(nil), guidance.ValidNext...)
		}
		state.StateSummary = runnerStateSummary(state)

		return state, phaseContract
	}

	if reviewRepairTerminalOutcomeRequiredState(brief, result) {
		state.Constraints = textutil.Merge(state.Constraints, []string{
			"review_repair_closeout_gate_fired",
			"terminal_review_repair_outcome_required",
		})
		state.InvalidNextActions = reviewRepairTerminalInvalidNextActions()
		state.NextValidActions = reviewRepairTerminalValidNextActions()
		state.StateSummary = runnerStateSummary(state)

		return state, phaseContract
	}

	state = applyDelegatedReadOnlyClosureBias(state, agent, brief, results)
	state = applyPostChildClosureBias(state, agent, brief, results)
	state = applyReadOnlyDiscoveryPendingGuidance(state, brief, req, result, results)
	state = applyClosurePromptRefinement(state)
	state.StateSummary = runnerStateSummary(state)

	return state, phaseContract
}

func runnerNextActionPlanningTransport(options []any) PlanExecutePlanningTransport {
	transport := PlanExecutePlanningTransport{TextualToolParser: true}
	for _, option := range options {
		if candidate, ok := option.(PlanExecutePlanningTransport); ok {
			return candidate
		}
	}
	return transport
}

func runnerNextActionCompletionSnapshot(options []any) *implementationCompletionState {
	for _, option := range options {
		if candidate, ok := option.(*implementationCompletionState); ok {
			return candidate
		}
	}
	return nil
}

func runnerNextActionMutationAuthority(options []any) planThenExecutePathAuthority {
	for _, option := range options {
		if candidate, ok := option.(planThenExecutePathAuthority); ok {
			return candidate
		}
	}
	return planThenExecutePathAuthority{}
}

func runnerNextActionInspectionAuthority(options []any) workspace.PermissionAuthorizedInspectionProjection {
	for _, option := range options {
		if candidate, ok := option.(workspace.PermissionAuthorizedInspectionProjection); ok {
			return candidate
		}
	}
	return workspace.PermissionAuthorizedInspectionProjection{}
}

func applyReadOnlyDiscoveryPendingGuidance(state runnerNextActionState, brief workerruntime.WorkerRuntimeBrief, req workspace.ToolRequest, result workspace.ToolResult, results []workspace.ToolResult) runnerNextActionState {
	if strings.TrimSpace(state.Phase) != "read_only_evidence_pending" || !readOnlyDirectAnswerLane(brief) {
		return state
	}

	if len(newToolResultEvidenceProjection(results).evidencePathsForBrief(brief, 1)) > 0 {
		return state
	}

	discoveryOutcome := discoveryResultOutcomeFromResult(req, result, 4)
	if discoveryOutcome.Strength == "" {
		return state
	}

	candidates := readOnlySourceLikePaths(discoveryOutcome.Candidates, 4)
	if len(candidates) == 0 {
		return state
	}

	tool := strings.TrimSpace(req.Tool)
	if tool == "" {
		tool = strings.TrimSpace(result.Tool)
	}

	candidateSummary := strings.Join(candidates, ", ")

	state.Constraints = textutil.Merge(state.Constraints, []string{"source_body_evidence_missing_after_discovery"})
	state.InvalidNextActions = textutil.Merge(state.InvalidNextActions, []string{
		"final answer before source-body evidence",
		"treating discovery candidates, context packets, or search hits as source-body evidence",
	})

	switch discoveryOutcome.Strength {
	case toolfacts.DiscoveryStrengthCandidateOnly:
		state.EvidenceStatus = textutil.Merge(state.EvidenceStatus, []string{
			"discovery_status: candidate-only; source owner and source-body evidence are still missing",
		})
		state.NextValidActions = prependRunnerStateStrings([]string{
			"source owner was not established by " + tool + "; use one materially different source-owner or source-body probe scoped to the question, or <<QUESTION>> with the exact unavailable evidence",
			"if candidates are plausible, inspect one returned path with source-body evidence; otherwise switch to an exact lexical/symbol/source-owner probe drawn from the user's claim and the code vocabulary",
		}, state.NextValidActions)
	case toolfacts.DiscoveryStrengthSourceOwner:
		state.EvidenceStatus = textutil.Merge(state.EvidenceStatus, []string{
			"discovery_status: candidate owner paths returned, but source-body evidence is still missing",
		})
		state.NextValidActions = prependRunnerStateStrings([]string{
			"inspect one returned path or handle with source-body evidence, take one targeted refinement with concrete scope or named_gap only if the returned owner does not establish the claim, or <<QUESTION>> with the exact blocker",
			"candidate path(s): " + candidateSummary,
		}, state.NextValidActions)
	}

	return state
}

func buildRunnerFailureFeedbackPacketWithAllowedToolsAndTransport(agent string, step int, reason string, req workspace.ToolRequest, result workspace.ToolResult, results []workspace.ToolResult, declarationMovesForbidden bool, brief workerruntime.WorkerRuntimeBrief, currentAllowedTools []string, transport PlanExecutePlanningTransport, completionState implementationCompletionState, mutationAuthority planThenExecutePathAuthority, inspection ...workspace.PermissionAuthorizedInspectionProjection) runnerFailureFeedbackPacket {
	options := []any{transport, &completionState, mutationAuthority}
	if len(inspection) > 0 {
		options = append(options, inspection[0])
	}
	state, _ := buildRunnerNextActionStateWithAllowedToolsAndContract(agent, step, reason, req, result, results, declarationMovesForbidden, brief, currentAllowedTools, options...)
	recoveryReq, recoveryResult := modelFacingParserRejectionSurface(reason, req, result, recoveryToolTransportMode(transport))
	recovery := workerruntime.BuildRecoveryPlan(workerruntime.RecoveryInput{
		Lane:                      agent,
		Step:                      step,
		Reason:                    reason,
		Request:                   recoveryReq,
		Result:                    recoveryResult,
		EvidencePaths:             state.EvidencePaths,
		Constraints:               state.Constraints,
		DeclarationMovesForbidden: declarationMovesForbidden,
		EvidenceIntent:            brief.EvidenceIntent,
		WriteCapable:              runnerHasWorkspaceWriteCapability(brief, brief.AllowedTools),
		ToolTransportMode:         recoveryToolTransportMode(transport),
	})

	// failure_feedback is emitted right after next_action_state, which
	// already carries Lane/Step/Reason/InvalidNext/ValidNext/StateSummary
	// for the same step. Re-emitting those fields wastes context. Keep
	// only the failure-specific signals (FailedTool/FailedTarget for
	// quick reference, FailureClass, the recovery-specific
	// DoNotRepeat/RequiredNext, and the UserVisibleBlocker flag). Tests
	// still rely on RequiredNext/ValidNext carrying closure-biased
	// options, so ValidNext keeps recovery.ValidNext (the *new* options
	// the recovery plan introduces) but drops the duplicates that
	// next_action_state already published.
	packet := runnerFailureFeedbackPacket{
		Lane:               state.Lane,
		Step:               state.Step,
		Reason:             state.Reason,
		FailedTool:         state.LastTool,
		FailedTarget:       state.LastTarget,
		FailureClass:       string(recovery.FailureClass),
		ResultSummary:      state.ResultSummary,
		ResultError:        state.ResultError,
		EvidenceStatus:     append([]string(nil), state.EvidenceStatus...),
		ValidNext:          append([]string(nil), recovery.ValidNext...),
		RecoveryCard:       state.RecoveryCard,
		UserVisibleBlocker: recovery.UserVisibleBlocker,
	}

	packet.DoNotRepeat = append([]string(nil), recovery.DoNotRepeat...)
	packet.RequiredNext = append([]string(nil), recovery.RequiredNext...)

	if phaseContract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief:                    runnerBriefWithPhaseHints(brief, results, mutationAuthority, completionState),
		Results:                  results,
		LastRequest:              req,
		LastResult:               result,
		ImplementationCompletion: &completionState,
		mutationAuthority:        mutationAuthority,
		InspectionAuthority:      runnerNextActionInspectionAuthority(options),
		BaseAllowedTools:         runnerCurrentAllowedOrBriefTools(currentAllowedTools, brief.AllowedTools),
		SupportsNativeTools:      transport.SupportsNativeTools,
		TextualToolParser:        transport.TextualToolParser,
	}); phaseContract.PhaseID != "" && phaseContract.PhaseID != contractcompile.TurnPhaseGeneric {
		packet.ValidNext = append([]string(nil), phaseContract.RequiredNext...)
		packet.RequiredNext = append([]string(nil), phaseContract.RequiredNext...)
		packet.InvalidNext = append([]string(nil), phaseContract.InvalidNext...)
	}

	if reviewRepairTerminalOutcomeRequiredState(brief, result) {
		packet.FailureClass = "terminal_outcome_required"
		packet.DoNotRepeat = reviewRepairTerminalInvalidNextActions()
		packet.RequiredNext = []string{
			"choose exactly one terminal review-repair outcome using the gathered evidence",
		}
		packet.ValidNext = reviewRepairTerminalValidNextActions()
		packet.InvalidNext = reviewRepairTerminalInvalidNextActions()
		packet.StateSummary = runnerStateSummary(state)
	}

	return packet
}

func modelFacingParserRejectionSurface(reason string, req workspace.ToolRequest, result workspace.ToolResult, transportMode string) (workspace.ToolRequest, workspace.ToolResult) {
	if strings.TrimSpace(reason) != parserRecoveryRecordReason {
		return req, result
	}

	surfaceReq := req
	surfaceResult := result
	if recoveryRejectedResultIsSubmitPlan(req, result) {
		surfaceReq.Tool = workspace.ToolNameSubmitPlan
		surfaceResult.Tool = workspace.ToolNameSubmitPlan
	} else {
		surfaceReq.Tool = ""
		surfaceResult.Tool = ""
	}
	surfaceResult.Error = modelFacingToolParseError(surfaceResult.Error, transportMode)

	return surfaceReq, surfaceResult
}

func modelFacingToolParseError(message string, transportMode string) string {
	message = strings.TrimSpace(message)
	if !strings.EqualFold(strings.TrimSpace(transportMode), workerruntime.RecoveryToolTransportNative) {
		return message
	}

	replacements := []string{
		". Emit exactly one valid tool call or answer normally.",
		" Emit exactly one valid tool call or answer normally.",
		". Emit exactly one valid JSON tool object.",
		" Emit exactly one valid JSON tool object.",
	}
	for _, replacement := range replacements {
		message = strings.ReplaceAll(message, replacement, "")
	}
	if message == "" {
		message = parserRecoverySummaryMalformed + "."
	}
	if !strings.Contains(strings.ToLower(message), "native tool-call transport") {
		message = strings.TrimRight(message, ".") + ". Use the active native tool-call transport for tools, or answer in plain prose if the task is complete."
	}

	return message
}

func recoveryToolTransportMode(transport PlanExecutePlanningTransport) string {
	return DeriveRuntimeCallPolicy(RuntimeCallPolicyInput{
		SupportsNativeTools: transport.SupportsNativeTools,
		ForceNativeTools:    transport.ForceNativeTools,
		TextualToolParser:   transport.TextualToolParser,
		ResponseFormatJSON:  transport.ResponseFormatJSON,
		TerminalOnly:        transport.TerminalOnly,
	}).ToolTransportMode()
}

func effectiveRecoveryCardAllowedTools(currentAllowedTools []string, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, contract TurnPhaseContract) []string {
	if len(contract.AllowedTools) > 0 {
		return append([]string(nil), contract.AllowedTools...)
	}

	if len(contract.ActionPolicy.CallableToolNames) > 0 {
		return append([]string(nil), contract.ActionPolicy.CallableToolNames...)
	}

	if contractcompile.TurnPhaseID(strings.TrimSpace(string(contract.PhaseID))) == contractcompile.TurnPhaseStaleAnchorRefresh {
		return workspace.ToolSurfacePhaseToolNames(workspace.ToolSurfacePhasePlanExecuteAnchorRefresh)
	}

	if len(currentAllowedTools) > 0 {
		return append([]string(nil), currentAllowedTools...)
	}

	if len(brief.AllowedTools) > 0 {
		return append([]string(nil), brief.AllowedTools...)
	}

	return nil
}

// ReadinessNudge carries the parameters that distinguish the three
// post-readiness nudge variants (child-packet boundary, delegated
// read-only answer ready, and direct read-only answer ready).
type ReadinessNudge struct {
	// Headline is the first line of the nudge.
	Headline string

	// EvidencePathLabel is the label shown before evidence paths
	// (e.g. "Packet-named paths: " or "Core evidence paths: ").
	// Must include the trailing space if a separator is needed.
	EvidencePathLabel string

	// EvidencePaths is the set of paths to display (unfiltered).
	EvidencePaths []string

	// Objective is the original question/objective string, used for
	// the closure decision rationale.
	Objective string

	// RuntimeBrief is the actual lane contract for ledger guidance.
	RuntimeBrief workerruntime.WorkerRuntimeBrief

	// Results are the collected tool results.
	Results []workspace.ToolResult

	// ContinuationScope is passed to postReadinessContinuationContract.
	ContinuationScope string

	// FooterLines are appended after the continuation contract.
	FooterLines []string

	// IncludeProbeSuggestions controls whether closureProbeSuggestionLine
	// is included. Only used by the child-packet variant.
	IncludeProbeSuggestions bool
}

// buildReadinessNudge produces the nudge text from the given parameters.
func buildReadinessNudge(ctx ReadinessNudge) string {
	evidencePaths := closurePromptEvidencePaths(ctx.EvidencePaths, 4)
	validity, _ := closureDecisionRationale(ctx.Objective, ctx.Results, evidencePaths)

	lines := []string{ctx.Headline}

	if validity != "" {
		lines = append(lines, "Why a final answer is now valid: "+validity)
	}

	if len(evidencePaths) > 0 {
		lines = append(lines, ctx.EvidencePathLabel+summarizePostChildEvidencePaths(evidencePaths, 3))
	}

	if ledger := readOnlyEvidenceLedgerGuidanceForBrief(ctx.RuntimeBrief, ctx.Results); ledger != "" {
		lines = append(lines, ledger)
	}

	if ctx.IncludeProbeSuggestions {
		if suggestion := newToolResultEvidenceProjection(ctx.Results).closureProbeSuggestionLine(3); suggestion != "" {
			lines = append(lines, suggestion)
		}
	}

	lines = append(lines, postReadinessContinuationContract(ctx.ContinuationScope))
	lines = append(lines, ctx.FooterLines...)

	return strings.Join(lines, "\n")
}

type postReadinessToolContract struct {
	RequireSourceRefreshContract bool
	ReadinessAdvisory            bool
	Reason                       string
}

func postReadinessToolContractForBrief(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, delegatedReadOnlyAnswerReady bool, postChildPacketActive bool) postReadinessToolContract {
	if writeReadyPacketReadContractActive(brief, false) || implementationScopedEditSurfaceActive(brief, results, brief.RequiredEditPaths, false) {
		return postReadinessToolContract{
			RequireSourceRefreshContract: true,
			Reason:                       "mutation_source_refresh_contract",
		}
	}

	if delegatedReadOnlyAnswerReady || postChildPacketActive {
		return postReadinessToolContract{
			ReadinessAdvisory: true,
			Reason:            "read_only_readiness_advisory",
		}
	}

	return postReadinessToolContract{Reason: "none"}
}

// postReadinessContinuationContract returns advisory closeout guidance after
// readiness or a child packet. It must not describe read-only readiness as a
// permission gate; explicit source-refresh gates are selected by
// postReadinessToolContractForBrief.
func postReadinessContinuationContract(scope string) string {
	anchor := "prefer a symbol, branch, body, or narrow line-range probe over file-level raw reads"
	if strings.TrimSpace(scope) == "child_packet_available" {
		anchor = "anchor to a named path, symbol, discovered handle, branch, body, or narrow line-range probe"
	} else if strings.TrimSpace(scope) == "answer_readiness_reached" {
		anchor = "target a symbol, call/reference, branch, body, or narrow line-range probe tied to inspected paths"
	}

	lines := []string{
		"A final answer is available unless one exact unresolved claim remains.",
		"If one more source read would materially change the answer, take a bounded follow-up through the normal tool policy; " + anchor + ".",
		"The follow-up should name the missing claim and target a code-shaped token (symbol name, file path, or narrow line range), not paraphrase the original question.",
	}

	return strings.Join(lines, "\n")
}

func delegatedReadOnlyDiminishingReturnsNudge(objective string, results []workspace.ToolResult, followups int) string {
	if followups <= 0 || followups > 2 {
		return ""
	}

	// The readiness nudge already disclosed the advisory continuation shape.
	// Repeating that prose burns context for no behavior change. Emit a
	// ledger note instead and let the model decide strategy.
	paths := closurePromptEvidencePaths(newToolResultEvidenceProjection(results).delegatedClosureEvidencePaths(objective, 4), 3)
	line := "The evidence ledger already supports a final answer. Continue with tools only if you can name an exact material missing claim and a bounded source that would resolve it."

	if len(paths) > 0 {
		line += " Inspected paths: " + summarizePostChildEvidencePaths(paths, 3) + "."
	}

	return line
}

func containsExactProbeCandidate(candidates []string, pattern string) bool {
	pattern, ok := delegatedReadOnlyLiteralSymbolCandidate(pattern)
	if !ok {
		pattern = strings.TrimSpace(pattern)
	}

	pattern = strings.ToLower(pattern)
	for _, candidate := range candidates {
		if strings.ToLower(strings.TrimSpace(candidate)) == pattern {
			return true
		}
	}

	return false
}

func quotedRunnerStrings(values []string) string {
	if len(values) == 0 {
		return ""
	}

	quoted := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}

		quoted = append(quoted, "`"+value+"`")
	}

	return strings.Join(quoted, ", ")
}

func postChildDiminishingReturnsNudge(objective string, results []workspace.ToolResult, followups int) string {
	if followups <= 0 || followups > 2 {
		return ""
	}

	// The child-packet boundary nudge already disclosed the continuation
	// contract; this is a ledger note, not another prose block telling the
	// model what was already said.
	paths := newToolResultEvidenceProjection(results).postChildClosureEvidencePaths(objective, 3)
	line := "The child-packet evidence ledger already supports a final answer. Continue with tools only if you can name an exact material missing claim and a bounded source that would resolve it."

	if len(paths) > 0 {
		line += " Packet paths: " + summarizePostChildEvidencePaths(paths, 3) + "."
	}

	return line
}

func closureDecisionRationale(objective string, results []workspace.ToolResult, fallbackPaths []string) (string, string) {
	projection := newToolResultEvidenceProjection(results)
	paths := closurePromptEvidencePaths(fallbackPaths, 4)
	if len(paths) == 0 {
		paths = projection.delegatedClosureEvidencePaths(objective, 4)
	}

	switch {
	case len(paths) > 0 && len(projection.closureProbeCandidates(1)) > 0:
		return "the current evidence already names inspected source paths " + summarizePostChildEvidencePaths(paths, 3) + " and includes at least one exact inspected probe", "another follow-up is only likely to help if it changes one named claim; otherwise it will mostly restate adjacent detail"
	case len(paths) > 0:
		return "the current evidence already names inspected source paths " + summarizePostChildEvidencePaths(paths, 3), "another follow-up is only likely to help if it closes one named gap; otherwise additional reads mostly restate nearby files"
	default:
		return "the current evidence already covers the named scope well enough to support a grounded answer", "another follow-up is only likely to help if it closes one named gap"
	}
}

func applyClosurePromptRefinement(state runnerNextActionState) runnerNextActionState {
	scope := ""

	switch {
	case slices.Contains(state.Constraints, "child_packet_available"):
		scope = "child_packet_available"
	case slices.Contains(state.Constraints, "answer_readiness_reached"):
		scope = "answer_readiness_reached"
	default:
		return state
	}

	state.NextValidActions = prependRunnerStateStrings(closurePromptNextActions(scope), filterRunnerStateStrings(state.NextValidActions, func(value string) bool {
		lower := strings.ToLower(strings.TrimSpace(value))
		switch {
		case strings.Contains(lower, workspace.ToolNameSearchFiles),
			strings.Contains(lower, "todo|fixme"),
			strings.Contains(lower, "panic\\("),
			strings.Contains(lower, "use a targeted read-only shell search"),
			strings.Contains(lower, "produce the missing evidence or edit required by the gate"),
			strings.Contains(lower, "update task_ledger"),
			strings.Contains(lower, "gather or edit the missing evidence"),
			strings.Contains(lower, workspace.ToolNameEditFile),
			strings.Contains(lower, "verification"),
			strings.Contains(lower, "changed file"),
			strings.Contains(lower, "target-scope update"),
			strings.Contains(lower, workspace.ToolNameGoMoveDeclarations),
			strings.Contains(lower, workspace.ToolNameListFiles):
			return true
		default:
			return false
		}
	}))
	state.InvalidNextActions = textutil.Merge(state.InvalidNextActions, closurePromptInvalidActions(scope))

	return state
}

func closurePromptNextActions(scope string) []string {
	switch strings.TrimSpace(scope) {
	case "child_packet_available":
		return []string{
			"final answer from the child packet evidence is available",
			"if one exact missing claim remains, use one bounded probe against a named path, symbol, discovered handle, or narrow range",
		}
	default:
		return []string{
			"final answer from the evidence already gathered is available",
			"if one exact missing claim remains, use one bounded probe against a named path, symbol, discovered handle, or narrow range",
		}
	}
}

func closurePromptInvalidActions(scope string) []string {
	// Answer readiness is advisory. It must not turn otherwise-authorized
	// inspection into an invalid action; exact-fingerprint, no-progress, doom,
	// and global tool-step bounds remain the owners of exploration termination.
	return nil
}

const modelCallPrefixHashVersion = "v1-leading-system-tools-parser"

func modelCallPrefixDiagnostics(prompt []llama.ChatMessage, opts llama.ChatOptions, parserContract string) map[string]any {
	prefix := leadingSystemPromptPrefix(prompt)
	toolSchemaHash := stableHash(opts.Tools)
	payload := map[string]any{
		"prefix_hash_version":          modelCallPrefixHashVersion,
		"prefix_hash":                  stableHash([]any{modelCallPrefixHashVersion, prefix, toolSchemaHash, opts.ToolChoice, parserContract}),
		"prompt_prefix_message_count":  len(prefix),
		"tool_schema_hash":             toolSchemaHash,
		"prefix_hash_components":       []string{"leading_system_messages", "tool_schemas", "tool_choice", "parser_contract"},
		"prefix_change_reason_fields":  []string{"system_prompt_changed", "tool_schema_changed", "tool_choice_changed", "parser_contract_changed"},
		"prefix_cache_metadata_scope":  "telemetry_only",
		"prefix_cache_signal_present":  false,
		"prefix_cache_signal_provider": "",
	}

	return payload
}

func leadingSystemPromptPrefix(prompt []llama.ChatMessage) []llama.ChatMessage {
	var prefix []llama.ChatMessage

	for _, msg := range prompt {
		if msg.Role != "system" {
			break
		}

		prefix = append(prefix, llama.ChatMessage{Role: msg.Role, Content: msg.Content})
	}

	return prefix
}

func stableHash(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		data = []byte("{}")
	}

	sum := sha256.Sum256(data)

	return hex.EncodeToString(sum[:])
}

type RunPolicy struct {
	MaxToolSteps       int
	MaxNoProgressTurns int
	MaxContextMessages int
	MaxContextChars    int
	// MaxModelCalls is an advisory accounting threshold. It may appear in
	// telemetry and ledgers, but it must not terminally steer model strategy.
	MaxModelCalls int
	// MaxOperationalModelCalls is the explicit hard provider/cost fuse. Zero
	// disables it; callers must opt in when a terminal model-call stop is a
	// real operational invariant.
	MaxOperationalModelCalls int
	// SynthesisNudgeAt injects a one-shot "pause and synthesize" system
	// message when ToolCalls first reaches this value. 0 disables. Mitigates
	// long tool chains that produce short answers.
	SynthesisNudgeAt int
	// FingerprintNudgeAt injects a one-shot system message per fingerprint
	// when a given tool+args pair has been called this many non-consecutive
	// times. 0 disables. Complements the DoomLoopGate (which only catches
	// consecutive triples).
	FingerprintNudgeAt int
}

type TaskLedgerPolicy struct {
	Disabled        bool
	Required        bool
	RequireEvidence bool
}

type CompletionGate func(CompletionGateInput) CompletionGateResult

type CompletionGateInput struct {
	AgentName string
	Reply     string
	Results   []workspace.ToolResult
	Steps     int
}

type CompletionGateResult struct {
	Accept  bool
	Summary string
	Error   string
}

type RunEvent struct {
	Role    string
	Content string
}

type ThoughtEvent struct {
	Step int
	Text string
}

type ToolRequestEvent struct {
	Step              int
	Agent             string
	ChildInvocationID string
	ChildContractID   string
	Raw               string
	Request           workspace.ToolRequest
	Mode              workspace.Mode
	Broad             bool
	Narrowing         bool
	Action            string
	DiscoveryClass    string
	DiscoveryScope    string
	DiscoveryCost     string
}

type ToolResultEvent struct {
	Step              int
	Agent             string
	ChildInvocationID string
	ChildContractID   string
	Request           workspace.ToolRequest
	Result            workspace.ToolResult
}

type runnerFailureFeedbackPacket struct {
	Lane               string   `json:"lane,omitempty"`
	Step               int      `json:"step,omitempty"`
	Reason             string   `json:"reason,omitempty"`
	FailedTool         string   `json:"failed_tool,omitempty"`
	FailedTarget       string   `json:"failed_target,omitempty"`
	FailureClass       string   `json:"failure_class,omitempty"`
	ResultSummary      string   `json:"result_summary,omitempty"`
	ResultError        string   `json:"result_error,omitempty"`
	EvidenceStatus     []string `json:"evidence_status,omitempty"`
	DoNotRepeat        []string `json:"do_not_repeat,omitempty"`
	RequiredNext       []string `json:"required_next,omitempty"`
	InvalidNext        []string `json:"invalid_next,omitempty"`
	ValidNext          []string `json:"valid_next,omitempty"`
	StateSummary       string   `json:"state_summary,omitempty"`
	RecoveryCard       string   `json:"recovery_card,omitempty"`
	UserVisibleBlocker bool     `json:"user_visible_blocker,omitempty"`
}

func (r *runExecution) effectivePolicy() RunPolicy {
	policy := r.runner.Policy
	if policy.MaxToolSteps == 0 && r.runner.MaxToolSteps != 0 {
		policy.MaxToolSteps = r.runner.MaxToolSteps
	}

	if briefLimit := r.state.runtimeBrief.Budget.MaxToolSteps; briefLimit > 0 && (policy.MaxToolSteps <= 0 || briefLimit < policy.MaxToolSteps) {
		policy.MaxToolSteps = briefLimit
	}

	if policy.MaxNoProgressTurns <= 0 {
		policy.MaxNoProgressTurns = 6
	}

	if runnerBriefRequiresMutation(r.state.runtimeBrief) {
		policy = scaleMaxToolStepsByRequiredPaths(policy, len(r.state.runtimeBrief.RequiredEditPaths))
	}

	if briefLimit := r.state.runtimeBrief.Budget.MaxToolSteps; briefLimit > 0 && (policy.MaxToolSteps <= 0 || briefLimit < policy.MaxToolSteps) {
		policy.MaxToolSteps = briefLimit
	}

	if briefLimit := r.state.runtimeBrief.Budget.MaxModelCalls; briefLimit > 0 && (policy.MaxModelCalls <= 0 || briefLimit < policy.MaxModelCalls) {
		policy.MaxModelCalls = briefLimit
	}

	if briefLimit := r.state.runtimeBrief.Budget.MaxOperationalModelCalls; briefLimit > 0 && (policy.MaxOperationalModelCalls <= 0 || briefLimit < policy.MaxOperationalModelCalls) {
		policy.MaxOperationalModelCalls = briefLimit
	}

	// Default synthesis nudge: fire at 10 tool calls for any lane that
	// doesn't explicitly skip it. The TUI already sets this via ModePolicy;
	// this ensures CLI mode gets the same advisory nudge so the model is
	// prompted to pause and answer rather than exploring indefinitely.
	if policy.SynthesisNudgeAt <= 0 && !r.lanePolicy().skipSynthesisNudge {
		policy.SynthesisNudgeAt = 10
	}

	return policy
}

// scaleMaxToolStepsByRequiredPaths grows the per-run tool-step ceiling for
// multi-file briefs. Single-file atoms keep the configured default; atoms
// with N required edit paths expand to base + 4*(N-1) capped at the
// ceiling.
//
// Scoping rules:
//   - MaxToolSteps == 0 (unbounded) is left alone.
//   - MaxToolSteps < base (explicit tight constraint, e.g. tests forcing
//     budget exhaustion at step 1) is left alone — we treat values below
//     the base as deliberate.
//   - MaxToolSteps >= base is raised to the scaled value when scaled is
//     larger; already-higher policies are left alone.
func scaleMaxToolStepsByRequiredPaths(policy RunPolicy, pathCount int) RunPolicy {
	if pathCount <= 1 {
		return policy
	}

	scaled := maxToolStepsBase + 4*(pathCount-1)
	if scaled > maxToolStepsCeiling {
		scaled = maxToolStepsCeiling
	}

	if policy.MaxToolSteps >= maxToolStepsBase && policy.MaxToolSteps < scaled {
		policy.MaxToolSteps = scaled
	}

	return policy
}

const (
	// maxToolStepsBase is the conservative default budget for a single-file
	// implementation atom. Multi-file briefs scale up via
	// scaleMaxToolStepsByRequiredPaths so a 5-file atom doesn't run out of
	// tool steps doing legitimate per-file source/history reads plus a
	// submit_plan correction or bounded anchor-refresh recovery.
	maxToolStepsBase = 16
	// maxToolStepsCeiling caps the scaled budget so a sprawling brief
	// can't run away even if it slipped past the brief lint.
	maxToolStepsCeiling = 56
	// postCompletionVerificationCutoff bounds how many non-write tool calls
	// the agent can issue after all required edit paths have been written.
	// A small budget lets the model do legitimate "did my edit land?" reads
	// without letting verification thrash exhaust the tool-step budget and
	// produce a false failure signal. Past this count the runner
	// force-answers with a success completion.
	postCompletionVerificationCutoff = 3
)

func (p RunPolicy) unbounded() bool {
	return p.MaxToolSteps <= 0
}

func (p RunPolicy) shouldStopForNoProgress(consecutiveNoProgress int) bool {
	return p.MaxNoProgressTurns > 0 && consecutiveNoProgress >= p.MaxNoProgressTurns
}

func (p RunPolicy) shouldStopForOperationalModelCalls(used int) bool {
	return p.MaxOperationalModelCalls > 0 && used >= p.MaxOperationalModelCalls
}

type runnerMetrics struct {
	startedAt                  time.Time
	ModelCalls                 int  `json:"used_model_calls"`
	ToolCalls                  int  `json:"used_tool_results"`
	PromptTokens               int  `json:"prompt_tokens,omitempty"`
	CompletionTokens           int  `json:"completion_tokens,omitempty"`
	TotalTokens                int  `json:"total_tokens,omitempty"`
	PromptCacheHitTokens       int  `json:"prompt_cache_hit_tokens,omitempty"`
	PromptCacheMissTokens      int  `json:"prompt_cache_miss_tokens,omitempty"`
	PromptCacheAccountingKnown bool `json:"prompt_cache_accounting_known,omitempty"`
	ReasoningTokens            int  `json:"reasoning_tokens,omitempty"`
	ProgressTurns              int  `json:"progress_turns"`
	RejectedActions            int  `json:"rejected_actions"`
	CompletionGateRejections   int  `json:"completion_gate_rejections"`
	Compactions                int  `json:"context_compactions"`
	ReadClassCalls             int  `json:"read_class_calls"`
}

type runnerBudgetLedger struct {
	Lane                       string   `json:"lane"`
	UsedModelCalls             int      `json:"used_model_calls"`
	MaxModelCalls              int      `json:"max_model_calls,omitempty"`
	MaxOperationalModelCalls   int      `json:"max_operational_model_calls,omitempty"`
	UsedToolResults            int      `json:"used_tool_results"`
	MaxToolSteps               int      `json:"max_tool_steps,omitempty"`
	PromptTokens               int      `json:"prompt_tokens,omitempty"`
	CompletionTokens           int      `json:"completion_tokens,omitempty"`
	TotalTokens                int      `json:"total_tokens,omitempty"`
	PromptCacheHitTokens       int      `json:"prompt_cache_hit_tokens,omitempty"`
	PromptCacheMissTokens      int      `json:"prompt_cache_miss_tokens,omitempty"`
	PromptCacheAccountingKnown bool     `json:"prompt_cache_accounting_known,omitempty"`
	ReasoningTokens            int      `json:"reasoning_tokens,omitempty"`
	ProgressTurns              int      `json:"progress_turns"`
	RejectedActions            int      `json:"rejected_actions"`
	CompletionGateRejections   int      `json:"completion_gate_rejections"`
	ContextCompactions         int      `json:"context_compactions"`
	ReadClassCalls             int      `json:"read_class_calls,omitempty"`
	DurationMillis             int64    `json:"duration_millis"`
	StopReason                 string   `json:"stop_reason"`
	BudgetExhausted            bool     `json:"budget_exhausted"`
	NextAllowedActions         []string `json:"next_allowed_actions,omitempty"`
	UserVisibleUncertainty     string   `json:"user_visible_uncertainty,omitempty"`
}

type runnerCompletionStatus struct {
	Lane          string `json:"lane"`
	TaskFamily    string `json:"task_family,omitempty"`
	Contract      string `json:"contract,omitempty"`
	Status        string `json:"status"`
	Reason        string `json:"reason,omitempty"`
	BlockerKind   string `json:"blocker_kind,omitempty"`
	RequiresEdit  bool   `json:"requires_edit,omitempty"`
	EditAttempted bool   `json:"edit_attempted"`
	recoverycontract.ImplementationState
	UserVisibleBlocker bool `json:"user_visible_blocker,omitempty"`
}

func (m *runnerMetrics) addUsage(usage llama.Usage) {
	if m == nil {
		return
	}

	m.PromptTokens += nonNegativeInt(usage.PromptTokens)
	m.CompletionTokens += nonNegativeInt(usage.CompletionTokens)
	m.PromptCacheHitTokens += nonNegativeInt(usage.PromptCacheHitTokens)
	m.PromptCacheMissTokens += nonNegativeInt(usage.PromptCacheMissTokens)

	m.PromptCacheAccountingKnown = m.PromptCacheAccountingKnown || usage.PromptCacheAccountingKnown

	m.ReasoningTokens += nonNegativeInt(usage.ReasoningTokens)

	total := usage.TotalTokens

	if total <= 0 {
		total = usage.PromptTokens + usage.CompletionTokens
	}

	m.TotalTokens += nonNegativeInt(total)
}

func nonNegativeInt(value int) int {
	if value < 0 {
		return 0
	}

	return value
}

type runnerNextActionState struct {
	Lane               string                    `json:"lane"`
	Phase              string                    `json:"phase,omitempty"`
	Step               int                       `json:"step"`
	Reason             string                    `json:"reason"`
	LastTool           string                    `json:"last_tool,omitempty"`
	LastTarget         string                    `json:"last_target,omitempty"`
	ResultSummary      string                    `json:"result_summary,omitempty"`
	ResultError        string                    `json:"result_error,omitempty"`
	ResultOutputChars  int                       `json:"result_output_chars,omitempty"`
	EvidencePaths      []string                  `json:"evidence_paths,omitempty"`
	CandidateSymbols   []string                  `json:"candidate_symbols,omitempty"`
	EvidenceStatus     []string                  `json:"evidence_status,omitempty"`
	Constraints        []string                  `json:"constraints,omitempty"`
	InvalidNextActions []string                  `json:"invalid_next_actions,omitempty"`
	NextValidActions   []string                  `json:"next_valid_actions,omitempty"`
	StateSummary       string                    `json:"state_summary,omitempty"`
	RecoveryCard       string                    `json:"recovery_card,omitempty"`
	RecoveryState      *PlanExecuteRecoveryState `json:"recovery_state,omitempty"`
	recoveryCard       *RecoveryCard
}

type runnerThinkingTrace struct {
	Lane               string `json:"lane"`
	Step               int    `json:"step"`
	Reason             string `json:"reason"`
	EnableThinking     string `json:"enable_thinking"`
	PreserveThinking   bool   `json:"preserve_thinking"`
	ContentChars       int    `json:"content_chars"`
	ContentPreview     string `json:"content_preview,omitempty"`
	ReasoningChars     int    `json:"reasoning_chars"`
	PreservedInHistory bool   `json:"preserved_in_history"`
}

type runnerContextCompaction struct {
	Lane             string                        `json:"lane"`
	DroppedMessages  int                           `json:"dropped_messages"`
	RetainedMessages int                           `json:"retained_messages"`
	MaxHistory       int                           `json:"max_history,omitempty"`
	MaxChars         int                           `json:"max_chars,omitempty"`
	PinnedPaths      []string                      `json:"pinned_paths,omitempty"`
	PinnedMemoryKind string                        `json:"pinned_memory_kind"`
	Strategy         string                        `json:"strategy"`
	Provenance       compactionevidence.Provenance `json:"provenance"`
}

func (r *runExecution) emitNextActionState(step int, reason string, req workspace.ToolRequest, result workspace.ToolResult, results []workspace.ToolResult, declarationMovesForbidden bool) {
	if r == nil || r.runner.OnEvent == nil {
		return
	}
	runState := &r.state

	brief := r.visibleBriefForToolSurface(r.state.runtimeBrief, results)
	completionState := r.implementationCompletionState(results)
	state, phaseContract := buildRunnerNextActionStateWithAllowedToolsAndContract(r.agentName(), step, reason, req, result, results, declarationMovesForbidden, brief, runState.currentAllowedTools, runState.planningTransport(), &completionState, r.planThenExecutePathAuthority(), r.permissionAuthorizedInspection(r.workspaceAdvertisedAllowedTools()))

	if runState.autonomyRecorder != nil {
		runState.autonomyRecorder.emitTurnPhaseSurfaceSummary(turnPhaseSurfaceSummaryFromNextActionState(state, phaseContract), phaseContract)

		if state.recoveryCard != nil {
			runState.autonomyRecorder.emitTurnPhaseSurfaceSummary(state.recoveryCard.TurnPhaseSurfaceSummary(phaseContract), phaseContract)
		}
	}

	data, err := json.Marshal(state)
	if err != nil {
		return
	}

	r.runner.OnEvent(RunEvent{Role: "next_action_state", Content: string(data)})
}

func (r *runExecution) runtimeBriefWithLiveState(policy RunPolicy, messages []llama.ChatMessage, results []workspace.ToolResult) workerruntime.WorkerRuntimeBrief {
	if r == nil {
		return workerruntime.WorkerRuntimeBrief{}
	}

	brief := r.state.runtimeBrief
	if strings.TrimSpace(brief.Lane) == "" {
		brief.Lane = r.agentName()
	}

	if strings.TrimSpace(brief.Objective) == "" {
		brief.Objective = latestUserObjective(messages)
	}

	if strings.TrimSpace(brief.TaskFamily) == PlanThenExecuteReviewRepairTaskFamily {
		brief.TargetPacket = textutil.Merge(brief.TargetPacket, reviewRepairPhaseEvidencePacket(brief.Objective))
	}

	if brief.Budget == (workerruntime.BudgetBrief{}) {
		brief.Budget = workerruntime.BudgetBrief{
			MaxToolSteps:             policy.MaxToolSteps,
			MaxModelCalls:            policy.MaxModelCalls,
			MaxOperationalModelCalls: policy.MaxOperationalModelCalls,
			MaxContextMessages:       policy.MaxContextMessages,
			MaxContextChars:          policy.MaxContextChars,
		}
	}

	liveEvidencePaths := newToolResultEvidenceProjection(results).evidencePathsForBrief(brief, 8)
	if readOnlyDirectAnswerLane(brief) {
		if len(liveEvidencePaths) > 0 {
			brief.EvidencePaths = liveEvidencePaths
		}
	} else {
		brief.EvidencePaths = textutil.Merge(brief.EvidencePaths, liveEvidencePaths)
	}

	completionState := r.implementationCompletionState(results)
	brief.ChangedPaths = textutil.Merge(brief.ChangedPaths, completionState.ChangedPaths)
	brief = runnerBriefWithPhaseHints(brief, results, r.planThenExecutePathAuthority(), completionState)
	brief.Readiness = runnerReadinessState(r.agentName(), brief, results, &completionState, r.planThenExecutePathAuthority())

	return workerruntime.NormalizeRuntimeBrief(brief)
}

func runnerReadinessState(agent string, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, terminalSnapshot *implementationCompletionState, authority planThenExecutePathAuthority) *workerruntime.ReadinessState {
	brief = workerruntime.NormalizeRuntimeBrief(brief)

	readiness := &workerruntime.ReadinessState{}
	readOnlyBrief := brief

	if readOnlyDirectAnswerLane(brief) {
		readOnlyBrief = readOnlyDirectAnswerAcceptanceLedgerBrief(brief)
	}

	readOnlyLedger := readOnlyAnswerabilityForResults(readOnlyBrief, results)
	addClosed := func(values ...string) {
		readiness.ClosedClaims = textutil.Merge(readiness.ClosedClaims, values)
	}
	addOpen := func(values ...string) {
		readiness.OpenObligations = textutil.Merge(readiness.OpenObligations, values)
	}
	addSource := func(values ...string) {
		readiness.EvidenceSources = textutil.Merge(readiness.EvidenceSources, values)
	}

	if len(brief.TargetRefs) > 0 {
		addSource("prompt_target_refs")
	}

	if len(brief.TargetPacket) > 0 {
		addSource("context_packet")
		addClosed("context_packet_available")
	}

	if len(brief.EvidencePaths) > 0 {
		addSource("evidence_paths")
	}

	if len(brief.RequiredEditPaths) > 0 {
		addSource("required_edit_paths")
		addClosed("required_edit_paths_define_write_scope")
	}

	if len(brief.RequiredEditSnippets) > 0 {
		addSource("required_edit_snippets")
		addClosed("required_edit_snippets_available")
	}

	if len(results) > 0 {
		addSource("tool_results")
	}

	if delegatedReadOnlyHasCompositeDiscoveryPacket(results) {
		addSource("discovery_packet")
		addClosed("composite_discovery_packet_available")
	}

	if readOnlyLedger.HasKind(readOnlyEvidenceContextPacket) {
		addSource("context_packet")
		addClosed("context_packet_localized_candidates")
	}

	if readOnlyLedger.HasKind(readOnlyEvidenceSearchHit) {
		addSource("search_hits")
		addClosed("search_hits_localized_candidates")
	}

	if readOnlyLedger.HasKind(readOnlyEvidenceOutline) {
		addSource("source_outline")
		addClosed("source_outline_localized_candidates")
	}

	if len(readOnlyLedger.RelevantSourceBodyPaths) > 0 {
		addSource("source_body")
		addClosed("relevant_source_body_evidence")
	}

	if len(readOnlyLedger.RelevantExternalDocumentPaths) > 0 {
		addSource("external_document_body")
		addClosed("relevant_external_document_evidence")
	}

	if readOnlyLedger.DiagnosticEvidence {
		addSource("diagnostic_evidence")
		addClosed("diagnostic_evidence_available")
	}

	if strings.HasPrefix(strings.TrimSpace(agent), "child:") &&
		strings.TrimSpace(brief.TaskFamily) == "delegated_read_only" &&
		delegatedReadOnlyClosureReady(agent, brief, results) {

		readiness.ReadyToAnswer = true

		addClosed("answer_readiness_reached")

		readiness.FurtherReadsRequire = "one exact missing claim, one exact probe, and why the current evidence would be materially incomplete"
	}

	if !strings.HasPrefix(strings.TrimSpace(agent), "child:") &&
		newToolResultEvidenceProjection(results).postChildPacketClosureReady(brief) {

		readiness.ReadyToAnswer = true

		addClosed("child_packet_available")

		readiness.FurtherReadsRequire = "one exact packet-anchored missing claim and one exact probe"
	}

	completionState := deriveImplementationProgressState(brief, results)
	if terminalSnapshot != nil {
		completionState = *terminalSnapshot
	}
	if len(completionState.ChangedPaths) > 0 {
		addSource("changed_paths")
		addClosed("workspace_changes_present")
	}

	if hasSuccessfulVerificationResult(results) {
		addSource("verification_result")
		addClosed("verification_result_observed")
	}

	if sourceEditsAreSemanticallyVerified(brief, results) {
		addClosed("source_aware_verification_after_source_edit")
	}

	if runnerBriefRequiresMutation(brief) {
		if completionState.TerminalDisposition == implementationTerminalDispositionPlanSubmitted {
			addSource("submit_plan_result")
			addClosed("plan_submitted")
			if completionState.CloseoutReady() {
				readiness.ReadyToAnswer = true
				readiness.ReadyToWrite = false
				readiness.ReadyToVerify = false
				return workerruntime.NormalizeReadinessState(readiness)
			}
		}

		if planThenExecutePlannerBrief(brief) && len(brief.RequiredEditPaths) > 0 && successfulSubmitPlanResultCount(results) == 0 {
			addSource("required_source_evidence_contract")

			if pending := planThenExecutePendingRequiredSourceEvidenceSummary(brief, results, 6, authority); pending != "" {
				addOpen("pending_required_source_anchors: " + pending)

				readiness.FurtherReadsRequire = "anchor-ready source evidence for pending required_edit_paths before submit_plan"
			} else {
				addClosed("required_source_anchors_ready")
			}
		} else if len(brief.RequiredEditPaths) > 0 {
			addSource("required_source_evidence_contract")

			if implementationRequiredPathAnchorsReady(brief, results, completionState.ChangedPaths) {
				addClosed("required_source_anchors_ready")
			} else if len(results) > 0 {
				if pending := planThenExecutePendingRequiredSourceEvidenceSummary(brief, results, 6, authority); pending != "" {
					addOpen("pending_required_source_anchors: " + pending)
				}
			}
		}

		missing := completionState.MissingRequiredPaths
		switch {
		case len(brief.RequiredEditPaths) > 0 && len(completionState.ChangedPaths) == 0:
			readiness.ReadyToAnswer = false
			readiness.ReadyToWrite = true

			addOpen("required_edit_paths_not_changed")

			if planThenExecutePlannerBrief(brief) && planThenExecutePendingRequiredSourceEvidenceSummary(brief, results, 1, authority) != "" {
				readiness.FurtherReadsRequire = "anchor-ready source evidence for pending required_edit_paths before submit_plan"
			} else if exactAnchorPreWriteSurfaceActive(brief, results, completionState.HasChanges()) {
				readiness.FurtherReadsRequire = "permission-authorized inspection remains callable when another fact is needed; otherwise make the first exact-anchor write or report <<QUESTION>> with the stale or missing anchor"
			} else {
				readiness.FurtherReadsRequire = "permission-authorized inspection remains callable when another edit-safety fact is needed; otherwise edit an in-scope required path or report <<QUESTION>> with the exact path-specific blocker"
			}
		case len(missing) > 0:
			readiness.ReadyToAnswer = false
			readiness.ReadyToWrite = true

			addOpen("required_edit_paths_remaining: " + strings.Join(missing, ", "))

			readiness.FurtherReadsRequire = "permission-authorized inspection remains callable when another fact is needed; resolve each remaining required path with a write or <<QUESTION>> naming the exact blocker"
		case len(brief.RequiredEditPaths) > 0:
			addClosed("required_edit_paths_changed")
		case len(completionState.ChangedPaths) == 0 && targetPacketSupportsWrite(brief):
			readiness.ReadyToWrite = true

			addOpen("workspace_change_not_attempted")

			readiness.FurtherReadsRequire = "one exact edit-safety gap grounded in the context packet"
		}

		if completionState.VerificationFailed {
			// Failed verification is an active repair obligation. It outranks
			// ordinary pending mutation work, while keeping both repair writes
			// and verification expressible.
			readiness.ReadyToAnswer = false
			readiness.ReadyToWrite = true
			readiness.ReadyToVerify = true
			addOpen("verification_failed_after_write")
			readiness.FurtherReadsRequire = "one exact repair fact for the failed verification; otherwise repair an in-scope changed/required path, rerun verification, or report the exact external blocker"
		} else {
			// Concrete mutation and task-ledger obligations own guidance ahead
			// of merely pending verification. Readiness flags are additive: a
			// run can remain write-ready while also being ready to verify the
			// current changed set.
			switch {
			case len(completionState.MissingRequiredSnippetIDs) > 0:
				readiness.ReadyToAnswer = false
				readiness.ReadyToWrite = true
				addOpen("required_edit_snippets_remaining: " + strings.Join(completionState.MissingRequiredSnippetIDs, ", "))
				readiness.FurtherReadsRequire = "one exact edit-safety fact for a missing required snippet; otherwise make the snippet-backed write or report the exact snippet-specific blocker"
			case completionState.HasPendingRejectedMutation():
				readiness.ReadyToAnswer = false
				readiness.ReadyToWrite = true
				addOpen("rejected_mutation_paths_remaining: " + strings.Join(completionState.UnresolvedRejectedMutationPaths, ", "))
				readiness.FurtherReadsRequire = "one exact edit-safety gap for an unresolved rejected mutation path; otherwise retry the write or report the exact path-specific blocker"
			case len(completionState.OpenTaskLedgerObligationIDs) > 0:
				readiness.ReadyToAnswer = false
				readiness.ReadyToWrite = true
				addOpen("task_ledger_obligations_remaining: " + strings.Join(completionState.OpenTaskLedgerObligationIDs, ", "))
				readiness.FurtherReadsRequire = "continue only action-backed unfinished task-ledger work and update the ledger with evidence, or report the exact task-specific blocker"
			}

			if completionState.VerificationPending {
				readiness.ReadyToAnswer = false
				if plannerOwnsVerification(brief) {
					if sourceVerificationGateApplies(brief, results) {
						addOpen("planner_owned_source_lane_sanity_pending")
					} else {
						addOpen("planner_owned_verification_status_pending")
					}
				} else {
					readiness.ReadyToVerify = true
					if sourceVerificationGateApplies(brief, results) {
						addOpen("source_aware_verification_pending")
					} else {
						addOpen("verification_pending_after_write")
					}
				}
			} else if completionState.SemanticVerificationPending {
				readiness.ReadyToAnswer = false
				readiness.ReadyToVerify = true
				if plannerOwnsVerification(brief) {
					addOpen("planner_owned_source_lane_sanity_pending")
					if readiness.FurtherReadsRequire == "" {
						readiness.FurtherReadsRequire = "compiler-backed source-sanity or language diagnostics for the changed source path, or a concrete lane-sanity blocker if source sanity is unavailable"
					}
				} else {
					addOpen("source_aware_verification_pending")
				}
			}

			if completionState.CloseoutReady() {
				readiness.ReadyToAnswer = true
			}
		}
	} else if runnerVerificationRequired(brief) && !hasSuccessfulVerificationResult(results) {
		readiness.ReadyToAnswer = false
		readiness.ReadyToVerify = true

		addOpen("verification_result_missing")
	} else if readOnlyDirectAnswerLane(brief) && readOnlyLedger.Answerable() {
		readiness.ReadyToAnswer = true

		addClosed("read_only_evidence_ready")

		readiness.FurtherReadsRequire = "one exact missing claim and one exact bounded probe"
	} else if readOnlyDirectAnswerLane(brief) {
		if len(readOnlyLedger.CandidatePaths) > 0 {
			addOpen("read_only_source_body_evidence_pending")

			readiness.FurtherReadsRequire = "one source-body read from a localized candidate, or one exact missing claim and bounded probe"
		}
	} else if readOnlyAnalysisReadyFromEvidence(results) {
		readiness.ReadyToAnswer = true

		addClosed("read_only_evidence_ready")

		readiness.FurtherReadsRequire = "one exact missing claim and one exact bounded probe"
	} else if len(readOnlyLedger.CandidatePaths) > 0 {
		addOpen("read_only_source_body_evidence_pending")

		readiness.FurtherReadsRequire = "one source-body read from a localized candidate, or one exact missing claim and bounded probe"
	}

	return workerruntime.NormalizeReadinessState(readiness)
}

func targetPacketSupportsWrite(brief workerruntime.WorkerRuntimeBrief) bool {
	return len(brief.TargetPacket) > 0 &&
		(len(brief.TargetRefs) > 0 ||
			len(brief.EvidencePaths) > 0 ||
			len(brief.RequiredEditPaths) > 0 ||
			len(brief.RequiredEditSnippets) > 0)
}

func readOnlyAnalysisReadyFromEvidence(results []workspace.ToolResult) bool {
	if !readOnlyClosureDecisionForResults(workerruntime.WorkerRuntimeBrief{}, results).Answerable() {
		return false
	}

	if delegatedReadOnlyHasCompositeDiscoveryPacket(results) {
		return readOnlyAnalysisExactProbeCount(results) >= 1 && len(newToolResultEvidenceProjection(results).promptEvidencePaths(6)) >= 2
	}

	return readOnlyAnalysisExactProbeCount(results) >= 2 && len(newToolResultEvidenceProjection(results).promptEvidencePaths(6)) >= 1
}

func runnerCurrentAllowedOrBriefTools(currentAllowedTools []string, briefAllowedTools []string) []string {
	if len(textutil.Unique(currentAllowedTools)) > 0 {
		return append([]string(nil), currentAllowedTools...)
	}

	return append([]string(nil), briefAllowedTools...)
}

func runnerStateSummary(state runnerNextActionState) string {
	parts := []string{state.Reason}
	if state.LastTool != "" {
		if state.LastTarget != "" {
			parts = append(parts, state.LastTool+" "+state.LastTarget)
		} else {
			parts = append(parts, state.LastTool)
		}
	}

	if state.ResultError != "" {
		parts = append(parts, "blocked: "+state.ResultError)
	} else if state.ResultSummary != "" {
		parts = append(parts, state.ResultSummary)
	}

	return textutil.Truncate(strings.TrimSpace(strings.Join(parts, " | ")), 320)
}

func prependRunnerStateStrings(preferred []string, existing []string) []string {
	out := make([]string, 0, len(preferred)+len(existing))
	for _, value := range preferred {
		out = textutil.AppendUnique(out, value)
	}

	for _, value := range existing {
		out = textutil.AppendUnique(out, value)
	}

	return out
}

func filterRunnerStateStrings(values []string, drop func(string) bool) []string {
	if len(values) == 0 {
		return values
	}

	filtered := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" || drop(trimmed) {
			continue
		}

		filtered = append(filtered, trimmed)
	}

	return filtered
}

func (r *runExecution) emitBudgetLedger(policy RunPolicy, metrics runnerMetrics, stopReason string, exhausted bool) {
	if r == nil || r.runner.OnEvent == nil {
		return
	}

	ledger := runnerBudgetLedger{
		Lane:                       r.agentName(),
		UsedModelCalls:             metrics.ModelCalls,
		MaxModelCalls:              policy.MaxModelCalls,
		MaxOperationalModelCalls:   policy.MaxOperationalModelCalls,
		UsedToolResults:            metrics.ToolCalls,
		PromptTokens:               metrics.PromptTokens,
		CompletionTokens:           metrics.CompletionTokens,
		TotalTokens:                metrics.TotalTokens,
		PromptCacheHitTokens:       metrics.PromptCacheHitTokens,
		PromptCacheMissTokens:      metrics.PromptCacheMissTokens,
		PromptCacheAccountingKnown: metrics.PromptCacheAccountingKnown,
		ReasoningTokens:            metrics.ReasoningTokens,
		ProgressTurns:              metrics.ProgressTurns,
		RejectedActions:            metrics.RejectedActions,
		CompletionGateRejections:   metrics.CompletionGateRejections,
		ContextCompactions:         metrics.Compactions,
		ReadClassCalls:             metrics.ReadClassCalls,
		DurationMillis:             time.Since(metrics.startedAt).Milliseconds(),
		StopReason:                 strings.TrimSpace(stopReason),
		BudgetExhausted:            exhausted,
	}
	if policy.MaxToolSteps > 0 {
		ledger.MaxToolSteps = policy.MaxToolSteps
	}

	if exhausted {
		ledger.NextAllowedActions = []string{"answer_with_explicit_uncertainty", "ask_user_for_narrower_scope", "resume_with_fresh_budget"}
		ledger.UserVisibleUncertainty = "answer from gathered evidence and mention only unsupported claims that remain material"
	}

	data, err := json.Marshal(ledger)
	if err != nil {
		return
	}

	r.runner.OnEvent(RunEvent{Role: "budget_ledger", Content: string(data)})
}

func (r *runExecution) emitCompletionStatus(payload runnerCompletionStatus) {
	if r == nil || r.runner.OnEvent == nil {
		return
	}

	r.emitEvent(marshalRunEvent("completion_status", payload))
}

func workspaceToolRulesPermissionDeny(decision PermissionDecisionSnapshot) bool {
	return decision.Decision == toolpolicy.Deny && strings.Contains(strings.TrimSpace(decision.Rule), "workspace.tool_rules")
}

func workspaceToolRulesPermissionDenyAnswer(req workspace.ToolRequest, decision PermissionDecisionSnapshot) string {
	tool := strings.TrimSpace(req.Tool)
	if tool == "" {
		tool = strings.TrimSpace(decision.Tool)
	}

	target := strings.TrimSpace(decision.Target)
	if target == "" {
		target = "the requested target"
	}
	if workspace.IsWorkspaceWriteTool(tool) {
		return fmt.Sprintf("I could not run `%s` on `%s` because workspace `tool_rules` deny that write. No changes were made.", tool, target)
	}

	return fmt.Sprintf("I could not read `%s` because workspace `tool_rules` deny `%s` for that path. No file contents were returned.", target, tool)
}

func (r *runExecution) emitModelCallTraceWithOptions(step int, label string, prompt []llama.ChatMessage, reply llama.ChatMessage, durationMs int64, opts llama.ChatOptions, supportsNativeTools bool, results []workspace.ToolResult) {
	if r == nil || r.runner.OnEvent == nil {
		return
	}

	chosen := "<answer>"
	if len(reply.ToolCalls) > 0 && strings.TrimSpace(reply.ToolCalls[0].Function.Name) != "" {
		chosen = strings.TrimSpace(reply.ToolCalls[0].Function.Name)
	}

	roleChars := make(map[string]int, 4)

	for _, m := range prompt {
		role := strings.TrimSpace(m.Role)
		if role == "" {
			role = "unknown"
		}

		roleChars[role] += len(m.Content)
	}

	parserContract := runnerTurnPhaseParserContract(opts, r.state.runtimeBrief, results, supportsNativeTools, r.planThenExecutePathAuthority())
	policy := PlanExecuteRuntimeCallPolicyFromChatOptions(opts, supportsNativeTools, parserContract == "submit_plan_json")
	payload := policy.TelemetryFields()

	payload["step"] = step
	payload["label"] = label
	payload["agent"] = r.agentName()
	payload["prompt_tokens"] = reply.Usage.PromptTokens
	payload["completion_tokens"] = reply.Usage.CompletionTokens
	payload["total_tokens"] = reply.Usage.TotalTokens

	if reply.Usage.PromptCacheAccountingKnown {
		payload["prompt_cache_hit_tokens"] = reply.Usage.PromptCacheHitTokens
		payload["prompt_cache_miss_tokens"] = reply.Usage.PromptCacheMissTokens
		payload["prompt_cache_accounting_known"] = true
	}

	if reply.Usage.ReasoningTokens > 0 {
		payload["reasoning_tokens"] = reply.Usage.ReasoningTokens
	}

	payload["ctx_size"] = r.runner.ContextSize
	payload["message_count"] = len(prompt)
	payload["role_chars"] = roleChars
	payload["chosen_tool"] = chosen

	if textual := modelCallParsedTextualToolTelemetry(reply); textual != nil {
		for key, value := range textual {
			payload[key] = value
		}
	}

	payload["duration_ms"] = durationMs

	backendKind := runnerTurnPhaseBackendKind(r)

	payload["backend_kind"] = backendKind

	backendCaps := llama.BackendStructuredOutputCapabilities(backendKind)
	modelProfile := llama.BackendModelCapabilityProfile(backendKind, opts.Model)

	payload["backend_capability_native_tools"] = backendCaps.NativeTools
	payload["backend_capability_json_object"] = backendCaps.JSONObject
	payload["backend_capability_strict_json_schema"] = backendCaps.StrictJSONSchema
	payload["backend_capability_grammar"] = backendCaps.Grammar
	payload["backend_capability_thinking_controls"] = backendCaps.ThinkingControls
	payload["backend_capability_request_timeout_telemetry"] = backendCaps.RequestTimeoutTelemetry
	payload["model_capability_profile_schema"] = modelProfile.SchemaVersion
	payload["model_capability_profile_fingerprint"] = modelProfile.Fingerprint
	payload["model_capability_profile_source"] = modelProfile.Source
	payload["model_capability_context_status"] = modelProfile.ContextWindow.Status
	payload["model_capability_max_output_status"] = modelProfile.MaxOutputTokens.Status
	payload["model_capability_system_messages_must_lead"] = modelProfile.ChatTemplate.SystemMessagesMustLead
	payload["model_alias"] = safeModelAliasForTelemetry(opts.Model)
	payload["parser_contract"] = parserContract

	for key, value := range modelCallPrefixDiagnostics(prompt, opts, parserContract) {
		payload[key] = value
	}

	if finish := strings.TrimSpace(reply.FinishReason); finish != "" {
		payload["finish_reason"] = finish
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return
	}

	r.runner.OnEvent(RunEvent{Role: "model_call", Content: string(data)})
}

func (r *runExecution) emitToolRequestNormalizerEvent(outcome string, transport string, req workspace.ToolRequest, parseErr error, parserFailureClass autonomy.ParserFailureClass) {
	if r == nil || r.runner.OnEvent == nil {
		return
	}

	payload := map[string]any{
		"outcome":   strings.TrimSpace(outcome),
		"transport": strings.TrimSpace(transport),
		"tool":      strings.TrimSpace(req.Tool),
		"path":      NormalizeRunnerPath(planThenExecuteRequestTargetPath(req)),
	}
	if parseErr == nil && strings.TrimSpace(req.Tool) != "" && toolRequestNormalizerTransportIsModelTextual(transport) {
		payload["parsed_textual_tool"] = strings.TrimSpace(req.Tool)
		payload["textual_tool_transport"] = strings.TrimSpace(transport)
	}

	if parseErr != nil {
		payload["error"] = strings.TrimSpace(parseErr.Error())
		_, parserFailureClass = parserPolicyMetadata("", parseErr, "", parserFailureClass)

		if parserFailureClass != "" && parserFailureClass != autonomy.ParserFailureNone {
			payload["parser_failure_class"] = string(parserFailureClass)
		}
	}

	r.emitEvent(marshalRunEvent("tool_request_normalizer", payload))
}

func toolRequestNormalizerTransportIsModelTextual(transport string) bool {
	return toolRequestAutonomyTransport(transport) == autonomy.ToolTransportTextualCall
}

func modelCallParsedTextualToolTelemetry(reply llama.ChatMessage) map[string]any {
	if len(reply.ToolCalls) > 0 {
		return nil
	}

	normalization := normalizeToolRequestSyntax(reply)
	if !normalization.OK || strings.TrimSpace(normalization.Request.Tool) == "" {
		return nil
	}

	transport := strings.TrimSpace(normalization.EventTransport)
	if transport == "" || transport == toolRequestNormalizerTransportNative {
		return nil
	}

	return map[string]any{
		"parsed_textual_tool":       strings.TrimSpace(normalization.Request.Tool),
		"parsed_tool_transport":     transport,
		"parsed_tool_request_valid": true,
	}
}

func safeModelAliasForTelemetry(model string) string {
	alias := strings.TrimSpace(model)
	if alias == "" {
		return ""
	}

	if strings.ContainsAny(alias, `/\`) {
		alias = filepath.Base(alias)
	}

	return textutil.Truncate(alias, 96)
}

func (r *runExecution) emitEvent(event RunEvent) {
	if r == nil || r.runner.OnEvent == nil || strings.TrimSpace(event.Role) == "" {
		return
	}

	r.runner.OnEvent(event)
}

func (r *runExecution) emitDelegatedReadOnlyAnswerReadyEvent(req workspace.ToolRequest, results []workspace.ToolResult) {
	if r == nil || r.runner.OnEvent == nil {
		return
	}

	paths := newToolResultEvidenceProjection(results).delegatedClosureEvidencePaths(r.state.runtimeBrief.Objective, 8)
	payload := map[string]any{
		"agent":                      r.agentName(),
		"reason":                     "sufficient_delegated_read_only_evidence",
		"trigger_tool":               strings.TrimSpace(req.Tool),
		"composite_discovery_packet": delegatedReadOnlyHasCompositeDiscoveryPacket(results),
		"evidence_paths":             paths,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return
	}

	r.runner.OnEvent(RunEvent{Role: "delegated_read_only_answer_ready", Content: string(data)})
}

func (r *runExecution) emitReadOnlyAnswerReadyEvent(req workspace.ToolRequest, results []workspace.ToolResult) {
	if r == nil || r.runner.OnEvent == nil {
		return
	}

	paths := newToolResultEvidenceProjection(results).evidencePathsForBrief(r.state.runtimeBrief, 8)
	payload := map[string]any{
		"agent":          r.agentName(),
		"reason":         "sufficient_read_only_evidence",
		"trigger_tool":   strings.TrimSpace(req.Tool),
		"evidence_paths": paths,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return
	}

	r.runner.OnEvent(RunEvent{Role: "read_only_answer_ready", Content: string(data)})
}

func (r *runExecution) compactTurnMessagesIfNeeded(messages *[]llama.ChatMessage, policy RunPolicy, ledger *taskLedgerState, results []workspace.ToolResult) bool {
	if messages == nil || len(*messages) == 0 {
		return false
	}

	if policy.MaxContextMessages <= 0 && policy.MaxContextChars <= 0 {
		return false
	}

	current := *messages
	tooManyMessages := policy.MaxContextMessages > 0 && len(current) > policy.MaxContextMessages
	tooManyChars := policy.MaxContextChars > 0 && chatMessagesCharCount(current) > policy.MaxContextChars

	if !tooManyMessages && !tooManyChars {
		return false
	}

	if len(current) <= 3 {
		return false
	}

	prefixEnd := runnerCompactionPrefixEnd(current)
	middleBudget, tailFloor := compactionBudget(policy)

	// Always keep a tail floor for conversation continuity.
	tailStart := len(current) - tailFloor
	if tailStart < prefixEnd {
		tailStart = prefixEnd
	}

	// Score middle messages (between prefix and tail floor) for evidence value.
	middleEnd := tailStart
	if middleEnd <= prefixEnd {
		middleEnd = prefixEnd
	}

	type scoredMsg struct {
		msg     llama.ChatMessage
		origIdx int
		score   float64
	}

	var scored []scoredMsg

	usedChars := 0

	for i := prefixEnd; i < middleEnd; i++ {
		msg := current[i]
		resultIdx := resultIndexForMessage(current, i, len(results))

		var result *workspace.ToolResult

		if resultIdx >= 0 {
			result = &results[resultIdx]
		}

		s := scoreMessageForRetention(msg, result, r.state.runtimeBrief, r.completionChangedPaths(results))

		scored = append(scored, scoredMsg{msg: msg, origIdx: i, score: s})
	}

	// Sort by score descending; tie-break by position (earlier first).
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			return scored[i].score > scored[j].score
		}

		return scored[i].origIdx < scored[j].origIdx
	})

	// Keep scored messages verbatim up to the evidence budget.
	keptMiddle := make(map[int]bool)

	const minScoreThreshold = 0.15

	for _, sm := range scored {
		if sm.score < minScoreThreshold {
			break
		}

		chars := msgCharCount(sm.msg)
		if usedChars+chars > middleBudget {
			break
		}

		usedChars += chars

		keptMiddle[sm.origIdx] = true
	}

	summary := llama.ChatMessage{
		Role:    "system",
		Content: r.runnerCompactionSummary(policy, ledger, results),
	}

	// Build compacted: prefix + kept middle + summary + tail floor.
	compacted := make([]llama.ChatMessage, 0, prefixEnd+len(keptMiddle)+1+tailFloor)

	compacted = append(compacted, current[:prefixEnd]...)

	for i := prefixEnd; i < middleEnd; i++ {
		if keptMiddle[i] {
			compacted = append(compacted, current[i])
		}
	}

	compacted = append(compacted, summary)
	compacted = append(compacted, current[tailStart:]...)

	if len(compacted) >= len(current) {
		return false
	}

	pinnedPaths := newToolResultEvidenceProjection(results).pinnedPathsForBrief(r.state.runtimeBrief, 8, r.completionChangedPaths(results))
	requiredPaths := append([]string(nil), r.state.runtimeBrief.RequiredEditPaths...)
	requiredPaths = append(requiredPaths, r.completionChangedPaths(results)...)
	provenance := compactionevidence.New(
		messageDigest(current),
		messageDigest(compacted),
		summary.Content,
		len(current),
		len(compacted),
		pinnedPaths,
		requiredPaths,
		"retain_system_and_user_objective_with_evidence_weighted_middle",
		"context_budget",
	)
	if !provenance.Acceptable() {
		return false
	}

	*messages = compacted
	r.emitContextCompaction(runnerContextCompaction{
		Lane:             r.agentName(),
		DroppedMessages:  len(current) - len(compacted),
		RetainedMessages: len(compacted),
		MaxHistory:       policy.MaxContextMessages,
		MaxChars:         policy.MaxContextChars,
		PinnedPaths:      pinnedPaths,
		PinnedMemoryKind: "runner_task_state",
		Strategy:         "retain_system_and_user_objective_with_evidence_weighted_middle",
		Provenance:       provenance,
	})

	return true
}

func messageDigest(messages []llama.ChatMessage) string {
	data, err := json.Marshal(messages)
	if err != nil {
		return ""
	}
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func (r *runExecution) emitContextCompaction(compaction runnerContextCompaction) {
	if r == nil || r.runner.OnEvent == nil {
		return
	}

	data, err := json.Marshal(compaction)
	if err != nil {
		return
	}

	r.runner.OnEvent(RunEvent{Role: "context_compaction", Content: string(data)})
}

func (r *runExecution) runnerCompactionSummary(policy RunPolicy, ledger *taskLedgerState, results []workspace.ToolResult) string {
	var b strings.Builder

	b.WriteString("[runner context compaction]\n")
	b.WriteString("agent: ")
	b.WriteString(r.agentName())
	b.WriteString("\nstrategy: older runner turns were compacted; preserve this state when deciding the next action.\n")

	if brief := workerruntime.RenderRuntimeBriefSummary(r.runtimeBriefWithLiveState(policy, nil, results)); brief != "" {
		b.WriteString("\n")
		b.WriteString(brief)
		b.WriteString("\n")
	}

	if ledger != nil && ledger.created {
		b.WriteString("\nTask ledger:\n")
		b.WriteString(ledger.render())
		b.WriteString("\n")
	}

	if paths := newToolResultEvidenceProjection(results).pinnedPathsForBrief(r.state.runtimeBrief, 8, r.completionChangedPaths(results)); len(paths) > 0 {
		b.WriteString("\nPinned paths:\n")

		for _, path := range paths {
			b.WriteString("- ")
			b.WriteString(path)
			b.WriteString("\n")
		}
	}

	if coverage := implementationSourceCoverageSummary(results, 8); coverage != "" {
		b.WriteString("\nSource coverage already read:\n")
		b.WriteString(coverage)
		b.WriteString("\n")
	}

	if implementationTargetWriteReadySurfaceActive(r.state.runtimeBrief, results, len(r.completionChangedPaths(results)) > 0) {
		b.WriteString("\nImplementation write-ready ledger:\n")
		b.WriteString("- remaining obligation: parser-readable write to a sourced target path, exact no-op evidence, or <<QUESTION>> with the exact blocker\n")
		b.WriteString("- permission-authorized reads/searches remain callable; typed pending paths and edit-safety facts are advisory evidence targets\n")
		if sourced := implementationTargetWriteReadySourcedPathSummary(results, 8); sourced != "" {
			b.WriteString("- source-body evidence paths: ")
			b.WriteString(sourced)
			b.WriteString("\n")
		}
		if inspected := conversationInspectedPathSummary(results); inspected != "" {
			b.WriteString("- recent inspected evidence: ")
			b.WriteString(inspected)
			b.WriteString("\n")
		}
	}

	if failed, ok := r.failedVerificationAfterLatestWrite(results); ok {
		b.WriteString("\nOpen failed verification repair packet:\n")
		b.WriteString("- tool: ")
		b.WriteString(strings.TrimSpace(failed.Tool))
		b.WriteString("\n")

		if summary := strings.TrimSpace(failed.Summary); summary != "" {
			b.WriteString("- summary: ")
			b.WriteString(summary)
			b.WriteString("\n")
		}

		if detail := strings.TrimSpace(failed.Error); detail != "" {
			b.WriteString("- error: ")
			b.WriteString(textutil.Truncate(strings.TrimSpace(detail), 500))
			b.WriteString("\n")
		}

		b.WriteString("- required next: repair the failing changed/required path, gather one exact edit-safety fact, rerun verification after repair, or answer <<QUESTION>> only for an external blocker.\n")
	}

	if len(results) > 0 {
		b.WriteString("\nRecent tool evidence:\n")

		for _, result := range tailToolResults(results, 8) {
			b.WriteString("- ")
			b.WriteString(result.Tool)

			if strings.TrimSpace(result.Summary) != "" {
				b.WriteString(": ")
				b.WriteString(result.Summary)
			}

			if strings.TrimSpace(result.Error) != "" {
				b.WriteString(" [error: ")
				b.WriteString(result.Error)
				b.WriteString("]")
			}

			b.WriteString("\n")
		}
	}

	return strings.TrimSpace(b.String())
}

func runnerCompactionPrefixEnd(messages []llama.ChatMessage) int {
	prefixEnd := 0
	for prefixEnd < len(messages) && messages[prefixEnd].Role == "system" {
		prefixEnd++
	}

	for i := prefixEnd; i < len(messages); i++ {
		if messages[i].Role == "user" {
			return i + 1
		}
	}

	if prefixEnd > 0 {
		return prefixEnd
	}

	if len(messages) < 2 {
		return len(messages)
	}

	return 2
}

func chatMessagesCharCount(messages []llama.ChatMessage) int {
	total := 0
	for _, msg := range messages {
		total += len(msg.Role) + len(msg.Content) + len(msg.ReasoningContent)
	}

	return total
}

func msgCharCount(msg llama.ChatMessage) int {
	return len(msg.Role) + len(msg.Content) + len(msg.ReasoningContent)
}

func compactionBudget(policy RunPolicy) (middleBudget int, tailFloor int) {
	const estCharsPerMsg = 500

	totalBudget := policy.MaxContextChars

	if totalBudget <= 0 && policy.MaxContextMessages > 0 {
		totalBudget = policy.MaxContextMessages * estCharsPerMsg
	}

	if totalBudget <= 0 {
		totalBudget = 40 * estCharsPerMsg
	}

	// 30% middle evidence, 50% tail continuity, 20% buffer.
	middleBudget = totalBudget * 30 / 100

	tailChars := totalBudget * 50 / 100

	tailFloor = tailChars / estCharsPerMsg

	if tailFloor < 2 {
		tailFloor = 2
	}

	if tailFloor > 8 {
		tailFloor = 8
	}

	return middleBudget, tailFloor
}

func resultIndexForMessage(messages []llama.ChatMessage, msgIndex int, resultsLen int) int {
	if resultsLen == 0 {
		return -1
	}

	toolResultCount := 0

	for i := 0; i <= msgIndex && i < len(messages); i++ {
		role := strings.ToLower(strings.TrimSpace(messages[i].Role))
		if role == "tool" || role == "tool_result" {
			toolResultCount++
		}

		if role == "system" && strings.Contains(messages[i].Content, "Tool:") {
			toolResultCount++
		}
	}

	idx := toolResultCount - 1
	if idx >= 0 && idx < resultsLen {
		return idx
	}

	return -1
}

// scoreMessageForRetention scores a message in the compaction middle range.
// Returns 0.0-1.0 where higher means "keep this verbatim."
// Weights reflect evidence hierarchy (verification > edit paths > evidence paths > tool type),
// not tuned constants. Tune only when eval runs show systematic quality degradation.
func scoreMessageForRetention(msg llama.ChatMessage, result *workspace.ToolResult, brief workerruntime.WorkerRuntimeBrief, changedPaths []string) float64 {
	if strings.TrimSpace(msg.Content) == "" {
		return 0.0
	}

	var score float64

	if result != nil {
		if strings.TrimSpace(result.Error) != "" {
			if workspace.AnalyzeVerificationResult(*result).Attempted {
				score += 0.40
			} else {
				score += 0.10
			}
		}

		if len(committedMutationEffectIDsForResult(*result)) > 0 {
			score += 0.20
		}

		tool := strings.TrimSpace(result.Tool)
		if tool == workspace.ToolNameReadFile || tool == workspace.ToolNameCodeNavigate || tool == workspace.ToolNameSourceSanity ||
			workspace.IsLanguageSourceBodyTool(tool) {

			score += 0.10
		}
	}

	content := strings.ToLower(msg.Content)

	editPaths := append([]string{}, brief.RequiredEditPaths...)

	editPaths = append(editPaths, changedPaths...)

	editScore := 0.0

	for _, path := range editPaths {
		normalized := NormalizeRunnerPath(path)
		if normalized != "" && strings.Contains(content, strings.ToLower(normalized)) {
			editScore += 0.25
			break
		}
	}

	if editScore > 0.50 {
		editScore = 0.50
	}

	score += editScore

	evidenceScore := 0.0

	for _, path := range brief.EvidencePaths {
		normalized := NormalizeRunnerPath(path)
		if normalized != "" && strings.Contains(content, strings.ToLower(normalized)) {
			evidenceScore += 0.15
			break
		}
	}

	if evidenceScore > 0.30 {
		evidenceScore = 0.30
	}

	score += evidenceScore
	if score > 1.0 {
		score = 1.0
	}

	return score
}

func tailToolResults(results []workspace.ToolResult, max int) []workspace.ToolResult {
	if max <= 0 || len(results) <= max {
		return results
	}

	return results[len(results)-max:]
}

type implementationSourceCoverage struct {
	Path          string
	TotalLines    int
	Ranges        []lineRange
	UnknownBody   bool
	CompleteRead  bool
	LatestSummary string
}

type lineRange struct {
	Start int
	End   int
}

func implementationSourceCoverageSummary(results []workspace.ToolResult, maxPaths int) string {
	coverage := implementationSourceCoverageByPath(results)
	if len(coverage) == 0 {
		return ""
	}

	paths := make([]string, 0, len(coverage))
	for path := range coverage {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	if maxPaths <= 0 || maxPaths > len(paths) {
		maxPaths = len(paths)
	}

	var b strings.Builder
	for _, path := range paths[:maxPaths] {
		item := coverage[path]
		b.WriteString("  - ")
		b.WriteString(path)
		b.WriteString(": ")
		b.WriteString(item.describe())
		b.WriteString("\n")
	}

	if len(paths) > maxPaths {
		b.WriteString("  - +")
		b.WriteString(strconv.Itoa(len(paths) - maxPaths))
		b.WriteString(" more source-covered path(s)\n")
	}

	return strings.TrimRight(b.String(), "\n")
}

func implementationSourceCoverageByPath(results []workspace.ToolResult) map[string]*implementationSourceCoverage {
	coverage := make(map[string]*implementationSourceCoverage)

	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" {
			continue
		}

		paths := newToolResultEvidenceRecord(result).ReadPaths()
		if len(paths) == 0 {
			continue
		}

		bodyPathSet := make(map[string]struct{})
		record := newToolResultEvidenceRecord(result)
		for _, path := range record.SourceWindowPaths() {
			bodyPathSet[path] = struct{}{}
		}

		for _, rawPath := range paths {
			path := NormalizeRunnerPath(rawPath)
			if path == "" || IsNonSourceEvidenceArtifactPath(path) {
				continue
			}
			_, hasBody := bodyPathSet[path]
			facts := record.SourceWindowFacts(path)
			hasRange := false
			for _, fact := range facts {
				if fact.StartLine > 0 && fact.EndLine >= fact.StartLine {
					hasRange = true
					break
				}
			}
			if !hasBody && !hasRange {
				continue
			}

			item := coverage[path]
			if item == nil {
				item = &implementationSourceCoverage{Path: path}
				coverage[path] = item
			}

			for _, fact := range facts {
				if fact.TotalLines > item.TotalLines {
					item.TotalLines = fact.TotalLines
				}
				if fact.StartLine > 0 && fact.EndLine >= fact.StartLine {
					item.Ranges = append(item.Ranges, lineRange{Start: fact.StartLine, End: fact.EndLine})
				}
			}
			if !hasRange && hasBody {
				item.UnknownBody = true
			}
			if summary := strings.TrimSpace(result.Summary); summary != "" {
				item.LatestSummary = summary
			}
		}
	}

	for _, item := range coverage {
		item.Ranges = mergeLineRanges(item.Ranges)
		if item.TotalLines > 0 {
			for _, r := range item.Ranges {
				if r.Start <= 1 && r.End >= item.TotalLines {
					item.CompleteRead = true
					break
				}
			}
		}
	}

	return coverage
}

func mergeLineRanges(ranges []lineRange) []lineRange {
	if len(ranges) == 0 {
		return nil
	}

	sort.Slice(ranges, func(i, j int) bool {
		if ranges[i].Start == ranges[j].Start {
			return ranges[i].End < ranges[j].End
		}
		return ranges[i].Start < ranges[j].Start
	})

	merged := make([]lineRange, 0, len(ranges))
	for _, r := range ranges {
		if r.Start <= 0 || r.End < r.Start {
			continue
		}
		if len(merged) == 0 || r.Start > merged[len(merged)-1].End+1 {
			merged = append(merged, r)
			continue
		}
		if r.End > merged[len(merged)-1].End {
			merged[len(merged)-1].End = r.End
		}
	}

	return merged
}

func (c implementationSourceCoverage) describe() string {
	parts := make([]string, 0, 4)
	if len(c.Ranges) > 0 {
		rangeParts := make([]string, 0, len(c.Ranges))
		for _, r := range c.Ranges {
			rangeParts = append(rangeParts, fmt.Sprintf("%d-%d", r.Start, r.End))
		}
		parts = append(parts, "lines "+strings.Join(rangeParts, ", "))
	}
	if c.TotalLines > 0 {
		parts = append(parts, fmt.Sprintf("total lines %d", c.TotalLines))
	}
	if c.CompleteRead {
		parts = append(parts, "complete file read")
	} else if c.UnknownBody {
		parts = append(parts, "source body observed")
	}
	if len(parts) == 0 && strings.TrimSpace(c.LatestSummary) != "" {
		parts = append(parts, textutil.Truncate(strings.TrimSpace(c.LatestSummary), 180))
	}
	return strings.Join(parts, "; ")
}

func latestUserObjective(messages []llama.ChatMessage) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if strings.ToLower(strings.TrimSpace(messages[i].Role)) != "user" {
			continue
		}

		if content := strings.TrimSpace(messages[i].Content); content != "" {
			if scoped := strings.TrimSpace(pathparse.ScopeToBriefBody(content)); scoped != "" {
				content = scoped
			}

			return textutil.Truncate(strings.TrimSpace(content), 500)
		}
	}

	return ""
}

func readOnlyChildPacketFollowupAnchors(objective string, packet ReadOnlyEvidencePacket) []string {
	if !weakChildPacketFollowupAnchorsAllowed(objective) {
		return nil
	}

	packet = normalizeReadOnlyEvidencePacket(packet)

	var paths []string
	paths = append(paths, packet.InspectedPaths...)
	paths = append(paths, packet.CandidatePaths...)

	return filterChildPacketFollowupAnchors(objective, paths)
}

func weakChildPacketFollowupAnchorsAllowed(objective string) bool {
	return ObjectiveAsksImplementationShape(objective) || delegatedReadOnlyChildMechanismQuestion(objective)
}

func filterChildPacketFollowupAnchors(objective string, paths []string) []string {
	filtered := make([]string, 0, len(paths))

	for _, raw := range paths {
		path := normalizeDelegatedDiscoveryEvidencePath(raw)
		if !isGroundedChildPacketEvidencePath(objective, path) {
			continue
		}

		filtered = append(filtered, path)
	}

	return delegatedDiscoveryAnswerEvidencePaths(objective, filtered)
}
