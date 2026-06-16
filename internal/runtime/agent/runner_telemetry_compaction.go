package agent

import (
	"encoding/json"
	"fmt"
	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/pathparse"
	"idiom/internal/platform/util"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/workerruntime"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func (r *Runner) emitBudgetLedger(policy RunPolicy, metrics runnerMetrics, stopReason string, exhausted bool) {
	if r == nil || r.OnEvent == nil {
		return
	}

	ledger := runnerBudgetLedger{
		Lane:                       r.agentName(),
		UsedModelCalls:             metrics.ModelCalls,
		MaxModelCalls:              policy.MaxModelCalls,
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

	r.OnEvent(RunEvent{Role: "budget_ledger", Content: string(data)})
}

func (r *Runner) emitCompletionStatus(payload runnerCompletionStatus) {
	if r == nil || r.OnEvent == nil {
		return
	}

	r.emitEvent(marshalRunEvent("completion_status", payload))
}

// emitCompletionStatusFromParams is a convenience wrapper that constructs a runnerCompletionStatus
// from individual parameters. It is retained so the many call-sites in runner.go's Run() method
// (and runner_terminal_answer.go) do not all need to be rewritten. The canonical construction
// happens in runner_runstate.go where both the legacy and autonomy events share the same data.
func (r *Runner) emitCompletionStatusFromParams(status, reason, blockerKind string, results []workspace.ToolResult, userVisibleBlocker bool) {
	changedPaths := mapKeys(changedPathsFromToolResults(results))
	payload := runnerCompletionStatus{
		Lane:               r.agentName(),
		TaskFamily:         strings.TrimSpace(r.RuntimeBrief.TaskFamily),
		Contract:           strings.TrimSpace(r.RuntimeBrief.Contract),
		Status:             strings.TrimSpace(status),
		Reason:             strings.TrimSpace(reason),
		BlockerKind:        strings.TrimSpace(blockerKind),
		RequiresEdit:       runnerBriefRequiresMutation(r.RuntimeBrief),
		EditAttempted:      len(changedPaths) > 0,
		ChangedPaths:       changedPaths,
		UserVisibleBlocker: userVisibleBlocker,
	}
	r.emitCompletionStatus(payload)
}

func workspaceToolRulesPermissionDeny(decision PermissionDecisionSnapshot) bool {
	return decision.Decision == PermissionDeny && strings.Contains(strings.TrimSpace(decision.Rule), "workspace.tool_rules")
}

func workspaceToolRulesPermissionDenyAnswer(req workspace.ToolRequest, result workspace.ToolResult) string {
	tool := strings.TrimSpace(req.Tool)
	if tool == "" {
		tool = strings.TrimSpace(result.Tool)
	}

	target := permissionTarget(req)
	if target == "" {
		target = "the requested target"
	}

	return fmt.Sprintf("I could not read `%s` because workspace `tool_rules` deny `%s` for that path. No file contents were returned.", target, tool)
}

func (r *Runner) emitModelCallTraceWithOptions(step int, label string, prompt []llama.ChatMessage, reply llama.ChatMessage, durationMs int64, opts llama.ChatOptions, supportsNativeTools bool, results []workspace.ToolResult) {
	if r == nil || r.OnEvent == nil {
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

	parserContract := runnerTurnPhaseParserContract(opts, r.RuntimeBrief, results, supportsNativeTools)
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

	payload["ctx_size"] = r.ContextSize
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

	payload["backend_capability_native_tools"] = backendCaps.NativeTools
	payload["backend_capability_json_object"] = backendCaps.JSONObject
	payload["backend_capability_strict_json_schema"] = backendCaps.StrictJSONSchema
	payload["backend_capability_grammar"] = backendCaps.Grammar
	payload["backend_capability_thinking_controls"] = backendCaps.ThinkingControls
	payload["backend_capability_request_timeout_telemetry"] = backendCaps.RequestTimeoutTelemetry
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

	r.OnEvent(RunEvent{Role: "model_call", Content: string(data)})
}

func (r *Runner) emitToolRequestNormalizerEvent(outcome string, transport string, req workspace.ToolRequest, parseErr error, parserFailureClass string) {
	if r == nil || r.OnEvent == nil {
		return
	}

	payload := map[string]any{
		"outcome":   strings.TrimSpace(outcome),
		"transport": strings.TrimSpace(transport),
		"tool":      strings.TrimSpace(req.Tool),
		"path":      normalizeRunnerPath(planThenExecuteRequestTargetPath(req)),
	}
	if parseErr == nil && strings.TrimSpace(req.Tool) != "" && toolRequestNormalizerTransportIsModelTextual(transport) {
		payload["parsed_textual_tool"] = strings.TrimSpace(req.Tool)
		payload["textual_tool_transport"] = strings.TrimSpace(transport)
	}

	if parseErr != nil {
		payload["error"] = strings.TrimSpace(parseErr.Error())
		if parserFailureClass == "" {
			parserFailureClass = toolRequestParserFailureClass("", parseErr)
		}

		if parserFailureClass != "" {
			payload["parser_failure_class"] = parserFailureClass
		}
	}

	r.emitEvent(marshalRunEvent("tool_request_normalizer", payload))
}

func toolRequestNormalizerTransportIsModelTextual(transport string) bool {
	switch strings.TrimSpace(transport) {
	case toolRequestNormalizerTransportTextual,
		toolRequestNormalizerTransportFencedJSON,
		toolRequestNormalizerTransportKeyValue,
		toolRequestNormalizerTransportKeyValueArgs,
		toolRequestNormalizerTransportFunctionCall,
		toolRequestNormalizerTransportNestedArguments,
		toolRequestNormalizerTransportIdiomCLI,
		toolRequestNormalizerTransportXMLToolMarkup,
		toolRequestNormalizerTransportStringifiedSubmitRepair:
		return true
	default:
		return false
	}
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

	return util.Truncate(alias, 96)
}

func (r *Runner) emitEvent(event RunEvent) {
	if r == nil || r.OnEvent == nil || strings.TrimSpace(event.Role) == "" {
		return
	}

	r.OnEvent(event)
}

func (r *Runner) emitDelegatedReadOnlyAnswerReadyEvent(req workspace.ToolRequest, results []workspace.ToolResult) {
	if r == nil || r.OnEvent == nil {
		return
	}

	paths := delegatedClosureEvidencePaths(r.RuntimeBrief.Objective, results, 8)
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

	r.OnEvent(RunEvent{Role: "delegated_read_only_answer_ready", Content: string(data)})
}

func (r *Runner) emitReadOnlyAnswerReadyEvent(req workspace.ToolRequest, results []workspace.ToolResult) {
	if r == nil || r.OnEvent == nil {
		return
	}

	paths := readOnlyPresentationEvidencePathsForBrief(r.RuntimeBrief, results, 8)
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

	r.OnEvent(RunEvent{Role: "read_only_answer_ready", Content: string(data)})
}

func (r *Runner) compactTurnMessagesIfNeeded(messages *[]llama.ChatMessage, policy RunPolicy, ledger *taskLedgerState, results []workspace.ToolResult) bool {
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

		s := scoreMessageForRetention(msg, result, r.RuntimeBrief, results)

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

	*messages = compacted
	r.emitContextCompaction(runnerContextCompaction{
		Lane:             r.agentName(),
		DroppedMessages:  len(current) - len(compacted),
		RetainedMessages: len(compacted),
		MaxHistory:       policy.MaxContextMessages,
		MaxChars:         policy.MaxContextChars,
		PinnedPaths:      runnerPinnedPathsForBrief(r.RuntimeBrief, results, 8),
		PinnedMemoryKind: "runner_task_state",
		Strategy:         "retain_system_and_user_objective_with_evidence_weighted_middle",
	})

	return true
}

func (r *Runner) emitContextCompaction(compaction runnerContextCompaction) {
	if r == nil || r.OnEvent == nil {
		return
	}

	data, err := json.Marshal(compaction)
	if err != nil {
		return
	}

	r.OnEvent(RunEvent{Role: "context_compaction", Content: string(data)})
}

func (r *Runner) runnerCompactionSummary(policy RunPolicy, ledger *taskLedgerState, results []workspace.ToolResult) string {
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

	if paths := runnerPinnedPathsForBrief(r.RuntimeBrief, results, 8); len(paths) > 0 {
		b.WriteString("\nPinned paths:\n")

		for _, path := range paths {
			b.WriteString("- ")
			b.WriteString(path)
			b.WriteString("\n")
		}
	}

	if failed, ok := failedVerificationAfterLatestWrite(results, r.RuntimeBrief); ok {
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
			b.WriteString(util.Truncate(strings.TrimSpace(detail), 500))
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
func scoreMessageForRetention(msg llama.ChatMessage, result *workspace.ToolResult, brief workerruntime.WorkerRuntimeBrief, allResults []workspace.ToolResult) float64 {
	if strings.TrimSpace(msg.Content) == "" {
		return 0.0
	}

	var score float64

	if result != nil {
		if strings.TrimSpace(result.Error) != "" {
			if resultIsVerificationAttempt(*result) {
				score += 0.40
			} else {
				score += 0.10
			}
		}

		if workspace.IsWorkspaceWriteTool(strings.TrimSpace(result.Tool)) && strings.TrimSpace(result.Error) == "" {
			score += 0.20
		}

		tool := strings.TrimSpace(result.Tool)
		if tool == workspace.ToolNameReadFile || tool == workspace.ToolNameCodeNavigate || tool == workspace.ToolNameSourceSanity ||
			tool == "go_read_symbol" || tool == "go_extract_body" ||
			tool == "cpp_read_symbol" || tool == "cpp_extract_body" ||
			tool == "python_read_symbol" || tool == "python_extract_body" {

			score += 0.10
		}
	}

	content := strings.ToLower(msg.Content)

	editPaths := append([]string{}, brief.RequiredEditPaths...)

	editPaths = append(editPaths, mapKeys(changedPathsFromToolResults(allResults))...)

	editScore := 0.0

	for _, path := range editPaths {
		normalized := normalizeRunnerPath(path)
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
		normalized := normalizeRunnerPath(path)
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

func latestUserObjective(messages []llama.ChatMessage) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if strings.ToLower(strings.TrimSpace(messages[i].Role)) != "user" {
			continue
		}

		if content := strings.TrimSpace(messages[i].Content); content != "" {
			if scoped := strings.TrimSpace(pathparse.ScopeToBriefBody(content)); scoped != "" {
				content = scoped
			}

			return util.Truncate(strings.TrimSpace(content), 500)
		}
	}

	return ""
}

func pinnedPathsFromToolResults(results []workspace.ToolResult, max int) []string {
	seen := make(map[string]struct{})

	var paths []string

	for _, result := range results {
		for _, field := range []string{result.Summary, result.Output, result.Error} {
			for _, path := range pathparse.MentionedWorkspacePaths(normalizeWorkspacePathScanText(field)) {
				if _, ok := seen[path]; ok {
					continue
				}

				seen[path] = struct{}{}
				paths = append(paths, path)
			}
		}
	}

	paths = prioritizedClosureEvidencePaths(paths, results, len(paths))

	return limitRunnerStrings(paths, max)
}

func directProbePathsFromToolResults(results []workspace.ToolResult, max int) []string {
	if max <= 0 {
		max = 8
	}

	seen := make(map[string]struct{}, max)

	var paths []string

	for _, result := range results {
		if !concreteSourceEvidenceResult(result) {
			continue
		}

		for _, path := range pathparse.MentionedWorkspacePaths(normalizeWorkspacePathScanText(result.Summary)) {
			path = normalizeDelegatedDiscoveryEvidencePath(path)
			if path == "" || isNonSourceEvidenceArtifactPath(path) || !delegatedDiscoveryLooksLikeEvidencePath(path) {
				continue
			}

			if _, ok := seen[path]; ok {
				continue
			}

			seen[path] = struct{}{}
			paths = append(paths, path)

			if len(paths) >= max {
				return paths
			}
		}
	}

	return paths
}

func promptEvidencePathsFromToolResults(results []workspace.ToolResult, max int) []string {
	if max <= 0 {
		max = 8
	}

	paths := make([]string, 0, max*2)
	directPaths := directProbePathsFromToolResults(results, max)

	for _, packet := range readOnlyEvidencePacketsFromResults(results) {
		paths = append(paths, packet.SourceBodyPaths...)
		paths = append(paths, packet.InspectedPaths...)
	}

	paths = append(paths, delegatedReadOnlyInspectedImplementationPaths(results)...)
	paths = append(paths, delegatedDiscoveryQueryEvidencePaths(results, max*4)...)
	paths = append(paths, pinnedPathsFromToolResults(results, max*6)...)
	paths = prioritizedClosureEvidencePaths(paths, results, max*3)

	return limitRunnerStrings(uniqueDelegatedDiscoveryPaths(append(directPaths, closurePromptEvidencePaths(paths, max*2)...)), max)
}

func runnerEvidencePathsForBrief(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, max int) []string {
	if max <= 0 {
		max = 8
	}

	if !runnerBriefRequiresMutation(brief) {
		if readOnlyDirectAnswerLane(brief) {
			return readOnlyDirectAnswerPresentationEvidencePaths(brief, results, max)
		}

		return promptEvidencePathsFromToolResults(results, max)
	}

	scope := mutationEvidenceScope(brief, results)
	paths := changedPathListFromToolResults(results)

	for _, path := range directProbePathsFromToolResults(results, max*2) {
		normalized := normalizeRunnerPath(path)
		if normalized == "" || !scope[normalized] {
			continue
		}

		paths = append(paths, normalized)
	}

	for _, path := range brief.EvidencePaths {
		normalized := normalizeRunnerPath(path)
		if normalized == "" || !scope[normalized] {
			continue
		}

		paths = append(paths, normalized)
	}

	return closurePromptEvidencePathsPreservingTests(paths, max)
}

func readOnlyPresentationEvidencePathsForBrief(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, max int) []string {
	if readOnlyDirectAnswerLane(brief) {
		return readOnlyDirectAnswerPresentationEvidencePaths(brief, results, max)
	}

	return promptEvidencePathsFromToolResults(results, max)
}

func readOnlyDirectAnswerPresentationEvidencePaths(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, max int) []string {
	if max <= 0 {
		max = 8
	}

	ledger := readOnlyAnswerabilityForResults(readOnlyDirectAnswerAcceptanceLedgerBrief(brief), results)
	paths := append([]string{}, ledger.RelevantSourceBodyPaths...)

	if objectiveAsksImplementationShape(ledger.Objective) {
		paths = mergeRunnerStateStrings(paths, ledger.ImplementationOwnerPaths())
	}

	if ledger.DiagnosticRelevant {
		paths = mergeRunnerStateStrings(paths, readOnlySourceLikePaths(ledger.DiagnosticPaths, max*2))
	}

	return closurePromptEvidencePaths(paths, max)
}

func runnerPinnedPathsForBrief(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, max int) []string {
	if max <= 0 {
		max = 8
	}

	if !runnerBriefRequiresMutation(brief) {
		if readOnlyDirectAnswerLane(brief) {
			return readOnlyDirectAnswerPresentationEvidencePaths(brief, results, max)
		}

		return promptEvidencePathsFromToolResults(results, max)
	}

	paths := append([]string{}, brief.RequiredEditPaths...)

	paths = append(paths, mapKeys(changedPathsFromToolResults(results))...)
	paths = append(paths, runnerEvidencePathsForBrief(brief, results, max*2)...)

	return closurePromptEvidencePathsPreservingTests(paths, max)
}

func mutationEvidenceScope(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) map[string]bool {
	scope := map[string]bool{}

	for _, values := range [][]string{
		brief.RequiredEditPaths,
		brief.TargetRefs,
		brief.ChangedPaths,
		brief.EvidencePaths,
		mapKeys(changedPathsFromToolResults(results)),
	} {
		for _, raw := range values {
			path := normalizeRunnerPath(raw)
			if path != "" {
				scope[path] = true
			}
		}
	}

	return scope
}

func postChildClosureEvidencePaths(objective string, results []workspace.ToolResult, max int) []string {
	paths := make([]string, 0, max)
	followupAnchors := make([]string, 0, max)
	acceptedPacket := false

	for _, result := range results {
		if strings.TrimSpace(result.Tool) != workspace.ToolNameTask || strings.TrimSpace(result.Error) != "" {
			continue
		}

		if packet, ok := readOnlyEvidencePacketFromResult(result); ok {
			packetAnswerable := readOnlyEvidencePacketAnswerableForObjective(packet, objective)
			if strings.TrimSpace(objective) == "" {
				packetAnswerable = readOnlyEvidencePacketAnswerable(packet)
			}

			if !packetAnswerable {
				followupAnchors = append(followupAnchors, readOnlyChildPacketFollowupAnchors(objective, packet)...)
				continue
			}

			acceptedPacket = true

			paths = append(paths, packet.SourceBodyPaths...)
			paths = append(paths, packet.InspectedPaths...)
			paths = append(paths, packet.CandidatePaths...)

			continue
		}

		sourcePaths := sourceBodyEvidencePathsFromDelegatedPacket(result.Output)
		if len(sourcePaths) == 0 && delegatedTaskOutputHasFindingEvidence(result.Output) {
			sourcePaths = groundedChildPacketEvidencePaths(objective, result.Output)
		}

		if len(sourcePaths) == 0 {
			followupAnchors = append(followupAnchors, delegatedChildPacketFollowupAnchors(objective, result.Output)...)
			continue
		}

		if !childPacketContainsGroundedAnswerEvidence(objective, result.Output) &&
			!delegatedPacketAddressesObjective(objective, result.Output) {

			followupAnchors = append(followupAnchors, delegatedChildPacketFollowupAnchors(objective, result.Output)...)
			continue
		}

		acceptedPacket = true

		paths = append(paths, sourcePaths...)
	}

	if acceptedPacket {
		paths = append(paths, pinnedPathsFromToolResults(results, max*4)...)
	}

	if len(paths) == 0 {
		paths = append(paths, followupAnchors...)
	}

	paths = prioritizedClosureEvidencePaths(delegatedDiscoveryAnswerEvidencePaths(objective, paths), results, max)

	return closurePromptEvidencePaths(paths, max)
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

func delegatedChildPacketFollowupAnchors(objective string, output string) []string {
	if !weakChildPacketFollowupAnchorsAllowed(objective) {
		return nil
	}

	if !delegatedChildPacketNamesFollowupEvidence(output) {
		return nil
	}

	return filterChildPacketFollowupAnchors(objective, groundedChildPacketEvidencePaths(objective, output))
}

func weakChildPacketFollowupAnchorsAllowed(objective string) bool {
	return objectiveAsksImplementationShape(objective) || delegatedReadOnlyChildMechanismQuestion(objective)
}

func delegatedChildPacketNamesFollowupEvidence(output string) bool {
	lower := strings.ToLower(strings.TrimSpace(output))
	if lower == "" || readOnlyToolControlGuidanceText(lower) {
		return false
	}

	return containsAny(lower,
		"inspected paths:",
		"inspected evidence:",
		"candidate paths:",
		"candidate files:",
		"evidence paths:",
		"relevant paths:",
		"relevant files:",
		"key findings:",
	)
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
