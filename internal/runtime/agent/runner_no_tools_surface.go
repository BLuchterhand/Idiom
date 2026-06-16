package agent

import (
	"context"
	"fmt"
	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/pathparse"
	"idiom/internal/platform/util"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/workerruntime"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func hideBroadDiscoveryTools(hidden map[string]bool) {
	for _, tool := range []string{
		workspace.ToolNameListFiles,
		workspace.ToolNameDiscoveryQuery, workspace.ToolNameDependencyGraph, workspace.ToolNameCtagsSymbols, workspace.ToolNameSemgrepScan, workspace.ToolNameJoernScan,
		"go_overview", "go_entrypoints",
		"cpp_overview", "cpp_entrypoints",
		"python_overview", "python_entrypoints",
		"ts_overview", "ts_entrypoints",
		"rust_overview", "rust_entrypoints",
		"java_overview", "java_entrypoints",
	} {
		hidden[tool] = true
	}
}

func (r *Runner) augmentReadOnlyChildPrompt(prompt string) string {
	guidance := []string{
		"A final answer is available once the evidence is sufficient. After readiness, take another read only for an exact material missing claim; do not continue reading for completeness — but when the delegated question asks about a mechanism's thresholds, derived values, or configurable parameters, reading the symbol body that defines them is part of understanding the mechanism, not optional completeness reading.",
		"Treat inherited instructions to explore directories, entrypoints, or patterns as background only unless the delegated question explicitly requires them.",
		"Use repository-relative workspace paths in the answer. Do not rely on transient file handles such as f23 or tool-local aliases.",
		"Prefer targeted symbol tools and bounded file reads over workspace-wide overviews.",
	}
	if namedPaths := pathparse.MentionedWorkspacePaths(prompt); len(namedPaths) > 0 {
		guidance = append(guidance, fmt.Sprintf("Start from these named workspace paths: %s.", strings.Join(namedPaths, ", ")))
	}

	return strings.TrimSpace(prompt) + "\n\nScope guidance:\n- " + strings.Join(guidance, "\n- ")
}

func (r *Runner) completeWithoutMoreTools(
	ctx context.Context,
	turnMessages []llama.ChatMessage,
	opts llama.ChatOptions,
	collectedResults []workspace.ToolResult,
	metrics *runnerMetrics,
) (string, error) {

	return r.completeWithoutMoreToolsWithReason(ctx, turnMessages, opts, collectedResults, "Tool budget is exhausted. Tools are now disabled.", metrics)
}

func answerFirstNoToolsPrompt(reason string) string {
	reason = strings.TrimSpace(reason)

	contract := "Final answer contract: tools are unavailable now. Write the normal final answer from the evidence already gathered. Do not emit XML tool-call tags, tool JSON, or tool-request syntax of any kind — write only plain prose. If the evidence supports a concrete answer, answer directly and do not lead with budget, blocker, partial-findings, or recommended-next-step framing. If one exact claim remains unsupported, state that uncertainty after the supported answer with the smallest useful next action.\n\nA good answer explains the mechanism and names the evidence: state what happens, which code paths are involved, and cite the files or functions you inspected. For example: 'Post-write validation runs through source_sanity (runner_verification.go:120-200) which checks changed files with go vet. The implement lane calls this directly after each write, while plan-execute defers verification to the review-repair phase.' Do not dump raw function signatures — synthesize what you learned."

	if reason == "" {
		return contract
	}

	if strings.Contains(reason, "Final answer contract:") {
		return reason
	}

	return strings.TrimSpace(reason + "\n\n" + contract)
}

func (r *Runner) completeWithoutMoreToolsWithReason(
	ctx context.Context,
	turnMessages []llama.ChatMessage,
	opts llama.ChatOptions,
	collectedResults []workspace.ToolResult,
	reason string,
	metrics *runnerMetrics,
) (string, error) {

	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "Tools are now disabled."
	}

	reason = answerFirstNoToolsPrompt(reason)

	preferModelSynthesis := shouldPreferModelBudgetSynthesis(r.agentName(), r.RuntimeBrief, collectedResults)

	if !preferModelSynthesis && postReadinessForcedCompletionReason(reason) && shouldPreferDeterministicNoToolsAnswer(r.agentName(), r.RuntimeBrief, collectedResults) {
		if answer, ok := deterministicNoToolsAnswer(r.agentName(), r.RuntimeBrief, collectedResults); ok {
			if !strings.HasPrefix(strings.TrimSpace(answer), "<<QUESTION>>") {
				return answer, nil
			}
		}
	}

	if !preferModelSynthesis {
		if answer, ok := immediateBudgetFallbackAnswer(r.agentName(), r.RuntimeBrief, reason, collectedResults); ok {
			return answer, nil
		}
	}

	if runnerBriefRequiresMutation(r.RuntimeBrief) && shouldPreferDeterministicNoToolsAnswer(r.agentName(), r.RuntimeBrief, collectedResults) {
		if answer, ok := deterministicNoToolsAnswer(r.agentName(), r.RuntimeBrief, collectedResults); ok {
			return answer, nil
		}
	}

	if preferModelSynthesis {
		reason = strings.TrimSpace(reason + "\nUse the evidence already gathered to answer the delegated question directly. Do not return a partial-findings template if a concise grounded answer is possible.")
		if paths := delegatedClosureEvidencePaths(r.RuntimeBrief.Objective, collectedResults, 4); len(paths) > 0 {
			reason = strings.TrimSpace(reason + "\nCarry forward the inspected implementation paths explicitly in the answer: " + summarizePostChildEvidencePaths(paths, 4) + ". Do not collapse the answer to a single file when the evidence already spans multiple relevant implementation paths.")
		}
	}

	// If any task_ledger items are still in_progress, prepend a closure
	// hint so the forced-completion reply actually resolves them instead
	// of leaving dangling in_progress todos in the transcript.
	if hint := taskLedgerClosureHint(collectedResults); hint != "" {
		reason = hint + "\n\n" + reason
	}

	turnMessages = append(turnMessages, llama.ChatMessage{
		Role:    "system",
		Content: reason,
	})

	if metrics != nil {
		metrics.ModelCalls++
	}

	// Strip orphaned tool_calls before the final API call so that
	// strict backends (DeepSeek) never see an assistant tool_calls
	// message without a matching role:"tool" response.
	turnMessages = stripOrphanedToolCalls(turnMessages)

	modelStart := time.Now()
	textOnlyOpts := textOnlyChatOptions(opts, r.RuntimeBrief)
	requestMessages := chatMessagesForOptions(turnMessages, textOnlyOpts)

	assistantMessage, err := r.Client.CompleteChatMessage(ctx, requestMessages, textOnlyOpts)
	if err != nil {
		return "", err
	}

	if metrics != nil {
		metrics.addUsage(assistantMessage.Usage)
	}

	r.emitModelCallTraceWithOptions(0, "forced_completion", requestMessages, assistantMessage, time.Since(modelStart).Milliseconds(), textOnlyOpts, r.supportsNativeTools(), collectedResults)

	reply := assistantMessage.Content
	r.emitThinkingTrace(0, "forced_completion_reply", assistantMessage, textOnlyOpts, false)

	preferDeterministicFallback := shouldPreferDeterministicNoToolsAnswer(r.agentName(), r.RuntimeBrief, collectedResults)

	if len(assistantMessage.ToolCalls) > 0 {
		if preferDeterministicFallback {
			if fallback, ok := deterministicNoToolsAnswer(r.agentName(), r.RuntimeBrief, collectedResults); ok {
				return fallback, nil
			}
		}

		if retry, ok, err := r.retryInvalidFinalAnswer(ctx, turnMessages, assistantMessage, textOnlyOpts, collectedResults, metrics, "native tool call"); err != nil {
			return "", err
		} else if ok {
			return retry, nil
		}

		if fallback, ok, err := r.repairOrFallbackFinalAnswer(ctx, turnMessages, textOnlyOpts, collectedResults, metrics); err != nil {
			return "", err
		} else if ok {
			return fallback, nil
		}

		return "", fmt.Errorf("tool loop exhausted and model still requested another tool instead of answering")
	}

	if looksLikeTextualToolCall(reply) {
		if preferDeterministicFallback {
			if fallback, ok := deterministicNoToolsAnswer(r.agentName(), r.RuntimeBrief, collectedResults); ok {
				return fallback, nil
			}
		}

		if retry, ok, err := r.retryInvalidFinalAnswer(ctx, turnMessages, assistantMessage, textOnlyOpts, collectedResults, metrics, "textual tool call"); err != nil {
			return "", err
		} else if ok {
			return retry, nil
		}

		if fallback, ok, err := r.repairOrFallbackFinalAnswer(ctx, turnMessages, textOnlyOpts, collectedResults, metrics); err != nil {
			return "", err
		} else if ok {
			return fallback, nil
		}

		return "", fmt.Errorf("tool loop exhausted and model still emitted textual tool call markup instead of answering")
	}

	if _, _, ok, parseErr := workspace.ParseToolRequest(reply); parseErr != nil {
		if preferDeterministicFallback {
			if fallback, ok := deterministicNoToolsAnswer(r.agentName(), r.RuntimeBrief, collectedResults); ok {
				return fallback, nil
			}
		}

		if retry, retryOK, err := r.retryInvalidFinalAnswer(ctx, turnMessages, assistantMessage, textOnlyOpts, collectedResults, metrics, "malformed tool request"); err != nil {
			return "", err
		} else if retryOK {
			return retry, nil
		}

		if fallback, ok, err := r.repairOrFallbackFinalAnswer(ctx, turnMessages, textOnlyOpts, collectedResults, metrics); err != nil {
			return "", err
		} else if ok {
			return fallback, nil
		}

		return "", parseErr
	} else if ok {
		if preferDeterministicFallback {
			if fallback, ok := deterministicNoToolsAnswer(r.agentName(), r.RuntimeBrief, collectedResults); ok {
				return fallback, nil
			}
		}

		if retry, retryOK, err := r.retryInvalidFinalAnswer(ctx, turnMessages, assistantMessage, textOnlyOpts, collectedResults, metrics, "tool request"); err != nil {
			return "", err
		} else if retryOK {
			return retry, nil
		}

		if fallback, ok, err := r.repairOrFallbackFinalAnswer(ctx, turnMessages, textOnlyOpts, collectedResults, metrics); err != nil {
			return "", err
		} else if ok {
			return fallback, nil
		}

		return "", fmt.Errorf("tool loop exhausted and model still requested another tool instead of answering")
	}

	answer, err := r.ensureNonEmptyAnswer(ctx, turnMessages, assistantMessage, textOnlyOpts, collectedResults, metrics)
	if err != nil {
		return "", err
	}

	if _, rejected := r.completionGateRejection(answer, collectedResults, 0, nil, false); rejected {
		if fallback, ok := deterministicNoToolsAnswer(r.agentName(), r.RuntimeBrief, collectedResults); ok {
			return fallback, nil
		}

		// No deterministic fallback available — return a clean user-facing
		// message instead of an error so the user sees something useful.
		return "I wasn't able to produce a clean answer from the evidence. Could you rephrase or narrow the question?", nil
	}

	if _, rejected := r.readOnlyNonSubstantiveCompletionRejection(answer, collectedResults); rejected {
		if fallback, ok := deterministicNoToolsAnswer(r.agentName(), r.RuntimeBrief, collectedResults); ok {
			return fallback, nil
		}

		return "I wasn't able to produce a clean answer from the evidence. Could you rephrase or narrow the question?", nil
	}

	if finalAnswerNeedsDecoration(answer) {
		if repaired, ok, err := r.repairInternalFinalAnswer(ctx, turnMessages, textOnlyOpts, answer, collectedResults, metrics); err != nil {
			return "", err
		} else if ok {
			if presentable, ok := presentableFallbackCandidate(r.agentName(), r.RuntimeBrief, collectedResults, repaired); ok {
				return presentable, nil
			}
		}

		if presentable, ok := presentableFallbackCandidate(r.agentName(), r.RuntimeBrief, collectedResults, answer); ok {
			return presentable, nil
		}
	}

	return answer, nil
}

func postReadinessForcedCompletionReason(reason string) bool {
	lower := strings.ToLower(strings.TrimSpace(reason))

	return strings.Contains(lower, "post-readiness") &&
		strings.Contains(lower, "tools are now disabled")
}

func (r *Runner) repairOrFallbackFinalAnswer(
	ctx context.Context,
	turnMessages []llama.ChatMessage,
	opts llama.ChatOptions,
	collectedResults []workspace.ToolResult,
	metrics *runnerMetrics,
) (string, bool, error) {

	rawFallback, ok := synthesizeNoToolsFallbackAnswer(r.agentName(), r.RuntimeBrief, collectedResults)
	if !ok {
		return "", false, nil
	}

	if finalAnswerNeedsDecoration(rawFallback) && r.userFacingFinalAnswer() {
		if repaired, ok, err := r.repairInternalFinalAnswer(ctx, turnMessages, opts, rawFallback, collectedResults, metrics); err != nil {
			return "", false, err
		} else if ok {
			if presentable, ok := presentableFallbackCandidate(r.agentName(), r.RuntimeBrief, collectedResults, repaired); ok {
				return presentable, true, nil
			}
		}
	}

	if presentable, ok := presentableFallbackCandidate(r.agentName(), r.RuntimeBrief, collectedResults, rawFallback); ok {
		return presentable, true, nil
	}

	return "", false, nil
}

func (r *Runner) repairInternalFinalAnswer(
	ctx context.Context,
	turnMessages []llama.ChatMessage,
	opts llama.ChatOptions,
	internalAnswer string,
	collectedResults []workspace.ToolResult,
	metrics *runnerMetrics,
) (string, bool, error) {

	internalAnswer = strings.TrimSpace(internalAnswer)
	if internalAnswer == "" {
		return "", false, nil
	}

	repairMessages := append([]llama.ChatMessage(nil), turnMessages...)

	repairMessages = append(repairMessages, llama.ChatMessage{
		Role: "system",
		Content: "The previous final synthesis still did not produce a usable user-facing answer. Tools remain disabled. " +
			"The text below is an internal evidence summary, not a final-answer template. Rewrite it into the normal final answer to the user's request using only claims supported by that evidence and the prior tool results. " +
			"Do not mention evidence packets, probes, tool calls, budgets, fallback behavior, or internal sections. If one exact claim is unsupported, state that uncertainty at the end.\n\nInternal evidence summary:\n" + internalAnswer,
	})

	if metrics != nil {
		metrics.ModelCalls++
	}

	// Strip orphaned tool_calls before the final API call so that
	// strict backends (DeepSeek) never see an assistant tool_calls
	// message without a matching role:"tool" response.
	repairMessages = stripOrphanedToolCalls(repairMessages)

	modelStart := time.Now()
	requestMessages := chatMessagesForOptions(repairMessages, opts)

	repairMessage, err := r.Client.CompleteChatMessage(ctx, requestMessages, opts)
	if err != nil {
		return "", false, err
	}

	if metrics != nil {
		metrics.addUsage(repairMessage.Usage)
	}

	r.emitModelCallTraceWithOptions(0, "fallback_final_answer_retry", requestMessages, repairMessage, time.Since(modelStart).Milliseconds(), opts, r.supportsNativeTools(), collectedResults)
	r.emitThinkingTrace(0, "fallback_final_answer_retry_reply", repairMessage, opts, false)

	if len(repairMessage.ToolCalls) > 0 {
		return "", false, nil
	}

	content := strings.TrimSpace(repairMessage.Content)
	if content == "" || looksLikeTextualToolCall(content) {
		return "", false, nil
	}

	if _, _, ok, parseErr := workspace.ParseToolRequest(content); ok || parseErr != nil {
		return "", false, nil
	}

	if cleaned, ok := cleanedSubstantiveFinalAnswer(content); ok {
		content = cleaned
	}

	if finalAnswerNeedsDecoration(content) {
		return "", false, nil
	}

	// Run the completion gate on the repaired answer. The retry path
	// (retryInvalidFinalAnswer) already runs this gate, but this
	// fallback-repair path was skipping it, allowing garbage answers
	// like raw tool result transcripts to reach the user.
	if _, rejected := r.completionGateRejection(content, collectedResults, 0, nil, false); rejected {
		return "", false, nil
	}

	r.LastAssistantMessage = repairMessage

	return content, true, nil
}

func (r *Runner) retryInvalidFinalAnswer(
	ctx context.Context,
	turnMessages []llama.ChatMessage,
	reply llama.ChatMessage,
	opts llama.ChatOptions,
	collectedResults []workspace.ToolResult,
	metrics *runnerMetrics,
	reason string,
) (string, bool, error) {

	retryMessages := append([]llama.ChatMessage(nil), turnMessages...)
	if strings.TrimSpace(reply.Content) != "" || strings.TrimSpace(reply.ReasoningContent) != "" {
		retryMessages = append(retryMessages, assistantHistoryMessageForOptions(llama.ChatMessage{
			Role:             util.FirstNonEmpty(strings.TrimSpace(reply.Role), "assistant"),
			Content:          reply.Content,
			ReasoningContent: reply.ReasoningContent,
		}, opts))
	}

	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "tool request"
	}

	retryMessages = append(retryMessages, llama.ChatMessage{
		Role: "system",
		Content: "Your previous final response was a " + reason + ", but tools are disabled for final synthesis. " +
			"Do not emit tool JSON, XML, tool blocks, or another probe. Reply in normal prose from the evidence already gathered. " +
			"If the evidence is sufficient, answer directly; if one exact claim remains unsupported, add that uncertainty after the supported answer in one sentence. " +
			"Do not list internal tool traces, exact probes, or inspected-path inventories unless they are necessary to explain the answer.",
	})

	if metrics != nil {
		metrics.ModelCalls++
	}

	// Strip orphaned tool_calls before the final API call so that
	// strict backends (DeepSeek) never see an assistant tool_calls
	// message without a matching role:"tool" response.
	retryMessages = stripOrphanedToolCalls(retryMessages)

	modelStart := time.Now()
	requestMessages := chatMessagesForOptions(retryMessages, opts)

	retryMessage, err := r.Client.CompleteChatMessage(ctx, requestMessages, opts)
	if err != nil {
		return "", false, err
	}

	if metrics != nil {
		metrics.addUsage(retryMessage.Usage)
	}

	r.emitModelCallTraceWithOptions(0, "invalid_final_answer_retry", requestMessages, retryMessage, time.Since(modelStart).Milliseconds(), opts, r.supportsNativeTools(), collectedResults)
	r.emitThinkingTrace(0, "invalid_final_answer_retry_reply", retryMessage, opts, false)

	if len(retryMessage.ToolCalls) > 0 {
		return "", false, nil
	}

	content := strings.TrimSpace(retryMessage.Content)
	if content == "" || looksLikeTextualToolCall(content) {
		return "", false, nil
	}

	if _, _, ok, parseErr := workspace.ParseToolRequest(content); ok || parseErr != nil {
		return "", false, nil
	}

	if cleaned, ok := cleanedSubstantiveFinalAnswer(content); ok {
		content = cleaned
	}

	if _, rejected := r.completionGateRejection(content, collectedResults, 0, nil, false); rejected {
		if fallback, ok := deterministicNoToolsAnswer(r.agentName(), r.RuntimeBrief, collectedResults); ok {
			return fallback, true, nil
		}

		return "", false, nil
	}

	if finalAnswerNeedsDecoration(content) {
		if repaired, ok, err := r.repairInternalFinalAnswer(ctx, turnMessages, opts, content, collectedResults, metrics); err != nil {
			return "", false, err
		} else if ok {
			if presentable, ok := presentableFallbackCandidate(r.agentName(), r.RuntimeBrief, collectedResults, repaired); ok {
				return presentable, true, nil
			}
		}

		if fallback, ok := deterministicNoToolsAnswer(r.agentName(), r.RuntimeBrief, collectedResults); ok {
			return fallback, true, nil
		}

		return "", false, nil
	}

	r.LastAssistantMessage = retryMessage

	return content, true, nil
}

func shouldPreferDeterministicNoToolsAnswer(agentName string, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if runnerBriefRequiresMutation(brief) {
		return len(changedPathsFromToolResults(results)) > 0 ||
			len(brief.RequiredEditPaths) > 0 ||
			len(brief.RequiredEditSnippets) > 0 ||
			targetPacketSupportsWrite(brief)
	}

	if runnerVerificationRequired(brief) {
		return false
	}

	return false
}

func deterministicNoToolsAnswer(agentName string, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) (string, bool) {
	brief = workerruntime.NormalizeRuntimeBrief(brief)
	if !runnerBriefRequiresMutation(brief) && workerruntime.PermissionProfileIsReadOnlyWorkspace(brief.PermissionProfile) {
		return "", false
	}

	answer, ok := synthesizeNoToolsFallbackAnswer(agentName, brief, results)
	if !ok {
		return "", false
	}

	return presentableFallbackCandidate(agentName, brief, results, answer)
}

func presentableFallbackCandidate(agentName string, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, answer string) (string, bool) {
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return "", false
	}

	if runnerBriefRequiresMutation(brief) {
		return answer, true
	}

	return readOnlyClosureDecisionForResults(brief, results).PresentableFallbackCandidate(agentName, answer)
}

func presentableOrReadOnlyNonAnswerReplacement(agentName string, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, answer string) (string, bool) {
	return readOnlyClosureDecisionForResults(brief, results).PresentableOrReadOnlyNonAnswerReplacement(agentName, answer)
}

func presentableBriefConversationFallbackCandidate(agentName string, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) (string, bool) {
	return readOnlyClosureDecisionForResults(brief, results).PresentableBriefConversationFallbackCandidate(agentName)
}


func fallbackCandidateUsesReadOnlyNonAnswerReplacement(agentName string, brief workerruntime.WorkerRuntimeBrief) bool {
	if strings.HasPrefix(agentName, "child:") || strings.HasPrefix(agentName, "child_write:") {
		return false
	}

	return readOnlyDirectAnswerLane(brief) && !runnerBriefRequiresMutation(brief)
}

func shouldPreferModelBudgetSynthesis(agentName string, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if runnerBriefRequiresMutation(brief) || runnerVerificationRequired(brief) {
		return false
	}

	if strings.HasPrefix(agentName, "child:") {
		return true
	}

	return postChildPacketClosureReadyWithBrief(brief, results) ||
		postChildSourceBodyPacketEvidenceAvailable(results)
}

func postChildSourceBodyPacketEvidenceAvailable(results []workspace.ToolResult) bool {
	for _, result := range results {
		if strings.TrimSpace(result.Tool) != workspace.ToolNameTask || strings.TrimSpace(result.Error) != "" {
			continue
		}

		if packet, ok := readOnlyEvidencePacketFromResult(result); ok {
			if len(packet.SourceBodyPaths) > 0 {
				return true
			}

			continue
		}

		if delegatedSourceBodyPacketHasGroundedFinding(result.Output) {
			return true
		}
	}

	return false
}

func runnerBriefAllowsExternalToolAppend(brief workerruntime.WorkerRuntimeBrief) bool {
	if strings.TrimSpace(brief.TaskFamily) == workerruntime.ArtifactFinalAnswerTaskFamily ||
		strings.TrimSpace(brief.Contract) == "artifact_final_answer" {

		return false
	}

	switch strings.TrimSpace(brief.PermissionProfile) {
	case string(workerruntime.PermissionPlanSubmissionReview), string(workerruntime.PermissionReadOnlyVerificationShell):
		return false
	}

	switch strings.TrimSpace(brief.TaskFamily) {
	case PlanThenExecuteReviewRepairTaskFamily, "debug_logs":
		return false
	default:
		return true
	}
}

func (r *Runner) advertisedAllowedTools(base []string) []string {
	if strings.TrimSpace(r.RuntimeBrief.TaskFamily) == workerruntime.ArtifactFinalAnswerTaskFamily ||
		strings.TrimSpace(r.RuntimeBrief.Contract) == "artifact_final_answer" {

		return nil
	}

	if workerruntime.PermissionProfileIsConversationOnly(r.RuntimeBrief.PermissionProfile) {
		// Even in conversation_only mode, expose project-defined external
		// tools so the model can call host-registered discovery commands
		// (e.g. project_search, api_access). Built-in mutating tools
		// stay hidden — that's what conversation_only is for.
		if r.Workspace == nil {
			return nil
		}

		if r.Workspace.ExternalToolsRegistry() == nil {
			return nil
		}

		names := r.Workspace.AvailableExternalToolNames()
		// Preserve order by filtering base to only externals; this also
		// drops any non-external entry that snuck into base via prior
		// pipeline mutations.
		known := make(map[string]struct{}, len(names))
		for _, n := range names {
			known[n] = struct{}{}
		}

		var out []string

		for _, name := range base {
			if _, ok := known[strings.TrimSpace(name)]; ok {
				out = append(out, name)
			}
		}

		// If base was empty (e.g. brief AllowedTools = nil), append
		// directly from the registry so the externals still surface.
		if len(out) == 0 {
			out = append(out, names...)
		}

		return out
	}

	allowed := append([]string(nil), base...)
	if len(r.RuntimeBrief.AllowedTools) > 0 {
		available := make(map[string]string, len(base))
		for _, tool := range base {
			trimmed := strings.TrimSpace(tool)
			if trimmed != "" {
				available[trimmed] = tool
			}
		}

		filtered := make([]string, 0, len(r.RuntimeBrief.AllowedTools))
		seen := make(map[string]struct{}, len(r.RuntimeBrief.AllowedTools))

		for _, tool := range r.RuntimeBrief.AllowedTools {
			tool = strings.TrimSpace(tool)
			if tool == "" {
				continue
			}

			if _, ok := seen[tool]; ok {
				continue
			}

			if original, ok := available[tool]; ok {
				filtered = append(filtered, original)
				seen[tool] = struct{}{}
			}
		}

		allowed = filtered
	}

	if r.DisableTaskTool {
		filtered := allowed[:0]
		for _, tool := range allowed {
			if tool != workspace.ToolNameTask {
				filtered = append(filtered, tool)
			}
		}

		allowed = filtered
	}

	return allowed
}

func textOnlyChatOptions(opts llama.ChatOptions, brief workerruntime.WorkerRuntimeBrief) llama.ChatOptions {
	opts.Tools = nil
	opts.ToolChoice = ""

	thinkingOff := false

	opts.EnableThinking = &thinkingOff
	opts.ChatTemplateKwargs = true
	opts.PreserveThinking = false

	limit := textOnlyMaxTokensForBrief(brief)

	if opts.MaxTokens <= 0 || opts.MaxTokens > limit {
		opts.MaxTokens = limit
	}

	return opts
}

func readOnlyEvidenceAvailableTerminalChatOptions(opts llama.ChatOptions, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) llama.ChatOptions {
	if !readOnlyEvidenceAvailableTerminalVisibleOutput(brief, results) {
		return opts
	}

	thinkingOff := false

	opts.EnableThinking = &thinkingOff
	opts.ChatTemplateKwargs = true
	opts.PreserveThinking = false

	return opts
}

func readOnlyEvidenceAvailableTerminalVisibleOutput(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	brief = workerruntime.NormalizeRuntimeBrief(brief)
	if runnerBriefRequiresMutation(brief) || runnerVerificationRequired(brief) || artifactFinalAnswerBrief(brief) {
		return false
	}

	if !workerruntime.PermissionProfileIsReadOnlyWorkspace(brief.PermissionProfile) {
		return false
	}

	if !readOnlyDirectAnswerLane(brief) {
		return false
	}

	return genericReadOnlyEvidenceReady(brief, results)
}

func textOnlyMaxTokensForBrief(brief workerruntime.WorkerRuntimeBrief) int {
	switch strings.TrimSpace(brief.Contract) {
	case string(workerruntime.ContractArtifactFinalAnswer):
		return 8192
	case string(workerruntime.ContractDirectAnswer):
		return 1024
	case string(workerruntime.ContractGroundedExplanation), string(workerruntime.ContractGroundedEvidence), string(workerruntime.ContractLocateTargets):
		return 1536
	case string(workerruntime.ContractArchitecture), string(workerruntime.ContractReviewFindings):
		return 2048
	default:
		return 768
	}
}

// IsTextualToolCall returns true when text looks like tool-call markup
// that should never reach the user-facing presentation layer.
func IsTextualToolCall(text string) bool {
	return looksLikeTextualToolCall(text)
}

// looksLikeXMLToolCall returns true when the text contains XML/HTML
// tool-call markup tags (llama.cpp DSML, janus, n_tool, etc.).
// Does not match line-oriented key-value tool requests.
func looksLikeXMLToolCall(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}

	return strings.Contains(lower, "<tool_call") ||
		strings.Contains(lower, "</tool_call>") ||
		strings.Contains(lower, "<function=") ||
		strings.Contains(lower, "</function>") ||
		strings.Contains(lower, "<invoke") ||
		strings.Contains(lower, "</invoke>") ||
		strings.Contains(lower, "<janus_tool") ||
		strings.Contains(lower, "</janus_tool") ||
		strings.Contains(lower, "<janus_invoke") ||
		strings.Contains(lower, "</janus_invoke") ||
		strings.Contains(lower, "<n_tool") ||
		strings.Contains(lower, "</n_tool") ||
		strings.Contains(lower, "<n_invoke") ||
		strings.Contains(lower, "</n_invoke") ||
		strings.Contains(lower, "<n_parameter") ||
		strings.Contains(lower, "</n_parameter") ||
		strings.Contains(lower, "dsml")
}

func looksLikeTextualToolCall(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}

	if strings.Contains(lower, "<tool_call") ||
		strings.Contains(lower, "</tool_call>") ||
		strings.Contains(lower, "<function=") ||
		strings.Contains(lower, "</function>") ||
		strings.Contains(lower, "<invoke") ||
		strings.Contains(lower, "</invoke>") ||
		strings.Contains(lower, "<janus_tool") ||
		strings.Contains(lower, "</janus_tool") ||
		strings.Contains(lower, "<janus_invoke") ||
		strings.Contains(lower, "</janus_invoke") ||
		strings.Contains(lower, "<n_tool") ||
		strings.Contains(lower, "</n_tool") ||
		strings.Contains(lower, "<n_invoke") ||
		strings.Contains(lower, "</n_invoke") ||
		strings.Contains(lower, "<n_parameter") ||
		strings.Contains(lower, "</n_parameter") ||
		strings.Contains(lower, "dsml") ||
		strings.Contains(lower, "[calling tool:") {

		return true
	}

	return looksLikeLineOrientedToolRequest(lower)
}

func looksLikeLineOrientedToolRequest(lower string) bool {
	lines := strings.Split(lower, "\n")
	if len(lines) < 2 || !strings.HasPrefix(strings.TrimSpace(lines[0]), "tool:") {
		return false
	}

	toolFields := 0

	for _, line := range lines[1:] {
		field := strings.TrimSpace(line)
		if strings.Contains(field, `"tool"`) {
			return true
		}

		if strings.HasPrefix(field, "{") && strings.Contains(field, `"tool"`) {
			return true
		}

		switch {
		case strings.HasPrefix(field, "capability:"),
			strings.HasPrefix(field, "path:"),
			strings.HasPrefix(field, "pattern:"),
			strings.HasPrefix(field, "query:"),
			strings.HasPrefix(field, "task:"),
			strings.HasPrefix(field, "mode:"),
			strings.HasPrefix(field, "limit:"),
			strings.HasPrefix(field, "command:"),
			strings.HasPrefix(field, "args:"):
			toolFields++
		}
	}

	return toolFields > 0
}

func toolResultMadeProgress(req workspace.ToolRequest, result workspace.ToolResult) bool {
	if strings.TrimSpace(result.Error) != "" {
		return false
	}

	switch strings.TrimSpace(req.Tool) {
	case workspace.ToolNameTaskLedger:
		return strings.TrimSpace(result.Output) != ""
	case workspace.ToolNameEditFile, workspace.ToolNameWriteFile, workspace.ToolNameAppendFile, workspace.ToolNameMkdir, workspace.ToolNameGoMoveDeclarations, workspace.ToolNameApplyPatch:
		summary := strings.ToLower(strings.TrimSpace(result.Summary))
		return summary != "" && !strings.Contains(summary, "no-op")
	case workspace.ToolNameReadFile, workspace.ToolNameSearchFiles, workspace.ToolNameSearchContent, workspace.ToolNameRg,
		"go_file_outline", "go_find_symbol", "go_find_type", "go_find_const", "go_read_symbol",
		"go_find_package", "go_related_files", "go_workspace_symbol",
		"go_definition", "go_references", "go_implementations",
		"go_calls_from", "go_calls_into", "go_extract_body", "go_importers", "go_diagnostics":
		return strings.TrimSpace(result.Output) != "" || strings.TrimSpace(result.Summary) != ""
	case workspace.ToolNameShell:
		return strings.TrimSpace(result.Output) != "" || strings.TrimSpace(result.Summary) != ""
	default:
		return strings.TrimSpace(result.Output) != "" || strings.TrimSpace(result.Summary) != ""
	}
}

func isMissingPathListFailure(req workspace.ToolRequest, result workspace.ToolResult) bool {
	if strings.TrimSpace(req.Tool) != workspace.ToolNameListFiles {
		return false
	}

	errText := strings.ToLower(strings.TrimSpace(result.Error))

	return strings.Contains(errText, "no such file or directory") || strings.Contains(errText, "cannot find the path")
}

// rewriteMissingRequiredNewFileProbe converts a "file not found" error
// on a probe targeting a required_edit_path, or a missing parent dir of
// required_edit_paths, into an actionable nudge: the planner can create
// those files via submit_plan's whole-file shape (replace_all=true with
// empty old_text + new_text=full body). Without this rewrite the planner
// sees the bare "no such file" error, treats it as a blocker, and gives
// up. That is the failure mode local Qwen hit on plan-execute dispatch.
//
// The rewrite preserves the result as an Error (so the planner can't
// use it as evidence of an actual file body) but replaces the message
// with one that names the right next step. We intentionally do NOT
// convert to a successful no-op: a successful probe of a non-existent
// path would leak into downstream "did the planner read this path"
// heuristics in misleading ways.
func rewriteMissingRequiredNewFileProbe(req workspace.ToolRequest, result workspace.ToolResult, brief workerruntime.WorkerRuntimeBrief) workspace.ToolResult {
	if !missingRequiredNewFileProbeTool(req.Tool) {
		return result
	}

	if strings.TrimSpace(result.Error) == "" {
		return result
	}

	lower := strings.ToLower(result.Error)
	if !strings.Contains(lower, "no such file") && !strings.Contains(lower, "does not exist") {
		return result
	}

	target := normalizeRunnerPath(strings.TrimSpace(req.Path))
	if target == "" {
		return result
	}

	matches := requiredNewFileProbeMatches(target, brief.RequiredEditPaths)
	if len(matches) == 0 {
		return result
	}

	if len(matches) == 1 && normalizeRunnerPath(matches[0]) == target {
		result.Summary = "Required new-file path does not exist yet"
		result.Error = fmt.Sprintf("path %q is in your required_edit_paths but does not exist on disk yet — this is a NEW FILE you must create. Do NOT keep trying to inspect it. In submit_plan, include a replace_all edit for %q with empty old_text, a complete new_text body from the brief or gathered evidence, and a specific purpose. Do not copy placeholder body text or schema examples. If the brief does not provide enough body evidence for a required source/test file, answer with <<QUESTION>> naming the missing body evidence. Large bodies are moved to artifact refs after submission.", target, target)

		return result
	}

	result.Summary = "Required new-file directory does not exist yet"
	result.Error = fmt.Sprintf("path %q is a missing parent for required_edit_paths: %s. These are NEW FILES you must create. Do NOT keep trying to inspect the missing directory. In submit_plan, include one replace_all edit per required new-file path with empty old_text and a full new_text body. If the brief does not provide enough body evidence for a required source/test file, answer with <<QUESTION>> naming that missing body evidence.", target, quotedRunnerStrings(matches))

	return result
}

func unavailableRequiredNewFileProbeNudge(req workspace.ToolRequest, brief workerruntime.WorkerRuntimeBrief, manager *workspace.Manager) (string, string, bool) {
	if !missingRequiredNewFileProbeTool(req.Tool) {
		return "", "", false
	}

	target := normalizeRunnerPath(strings.TrimSpace(req.Path))
	if target == "" {
		return "", "", false
	}

	matches := requiredNewFileProbeMatches(target, brief.RequiredEditPaths)
	if len(matches) == 0 {
		return "", "", false
	}

	if !requiredNewFileProbeTargetMissing(target, manager) {
		return "", "", false
	}

	if len(matches) == 1 && normalizeRunnerPath(matches[0]) == target {
		return "Required new-file path does not exist yet",
			fmt.Sprintf("path %q is in your required_edit_paths but does not exist on disk yet — this is a NEW FILE you must create. The requested probe tool %q is not available in the current allowed tool surface, and probing is unnecessary. In submit_plan, include a replace_all edit for %q with empty old_text, a complete new_text body from the brief or gathered evidence, and a specific purpose. Do not copy placeholder body text or schema examples. If the brief does not provide enough body evidence for a required source/test file, answer with <<QUESTION>> naming the missing body evidence.", target, strings.TrimSpace(req.Tool), target),
			true
	}

	return "Required new-file directory does not exist yet",
		fmt.Sprintf("path %q is a missing parent for required_edit_paths: %s. These are NEW FILES you must create. The requested probe tool %q is not available in the current allowed tool surface, and probing is unnecessary. In submit_plan, include one replace_all edit per required new-file path with empty old_text and a full new_text body. If the brief does not provide enough body evidence for a required source/test file, answer with <<QUESTION>> naming that missing body evidence.", target, quotedRunnerStrings(matches), strings.TrimSpace(req.Tool)),
		true
}

func requiredNewFileProbeTargetMissing(target string, manager *workspace.Manager) bool {
	root := ""
	if manager != nil {
		root = strings.TrimSpace(manager.Root())
	}

	if root == "" {
		return true
	}

	path := target
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, filepath.FromSlash(path))
	}

	_, err := os.Stat(path)

	return os.IsNotExist(err)
}

func missingRequiredNewFileProbeTool(tool string) bool {
	tool = strings.TrimSpace(tool)
	if tool == workspace.ToolNameReadFile || tool == workspace.ToolNameListFiles {
		return true
	}

	return strings.HasSuffix(tool, "_file_outline")
}

func requiredNewFileProbeMatches(target string, requiredPaths []string) []string {
	target = strings.Trim(strings.TrimSpace(normalizeRunnerPath(target)), "/")
	if target == "" {
		return nil
	}

	var matches []string

	for _, p := range requiredPaths {
		path := strings.Trim(strings.TrimSpace(normalizeRunnerPath(p)), "/")
		if path == "" {
			continue
		}

		if path == target || strings.HasPrefix(path, target+"/") || filepath.Dir(path) == target {
			matches = appendUniqueRunnerStrings(matches, path)
		}
	}

	return matches
}

func briefIsDocDeliverableLane(brief workerruntime.WorkerRuntimeBrief) bool {
	if len(brief.RequiredEditPaths) == 0 {
		return false
	}

	for _, p := range brief.RequiredEditPaths {
		if !writePathIsNonSource(p) {
			return false
		}
	}

	return true
}

func briefIsScratchpadDeliverableLane(brief workerruntime.WorkerRuntimeBrief) bool {
	if !briefIsDocDeliverableLane(brief) {
		return false
	}

	for _, p := range brief.RequiredEditPaths {
		normalized := normalizeRunnerPath(p)
		if !strings.HasPrefix(normalized, ".idiom/refine/") {
			return false
		}
	}

	return true
}

func scopedImplementationUsesBoundedSourceEdits(brief workerruntime.WorkerRuntimeBrief, paths []string) bool {
	if !implementationScopedEditSurfaceActive(brief, nil) || briefIsDocDeliverableLane(brief) {
		return false
	}

	for _, path := range paths {
		normalized := normalizeRunnerPath(path)
		if normalized == "" || writePathIsNonSource(normalized) {
			continue
		}

		if recoverySearchPathLooksEditableSource(normalized) {
			return true
		}
	}

	return false
}

// writeTouchesOnlyNonSourcePaths returns true when every path in a
// write result is a non-source file (markdown, plain text). Used to
// exempt doc-deliverable lanes from the post-write verification gate.
func writeTouchesOnlyNonSourcePaths(result workspace.ToolResult) bool {
	paths := changedPathsFromToolResult(result)
	if len(paths) == 0 {
		return false
	}

	for _, p := range paths {
		if !writePathIsNonSource(p) {
			return false
		}
	}

	return true
}

func mkdirTouchesOnlyRequiredPathParents(result workspace.ToolResult, requiredPaths []string) bool {
	if strings.TrimSpace(result.Tool) != workspace.ToolNameMkdir || len(requiredPaths) == 0 {
		return false
	}

	paths := changedPathsFromToolResult(result)
	if len(paths) == 0 {
		return false
	}

	for _, path := range paths {
		if !mkdirPathIsRequiredParent(path, requiredPaths) {
			return false
		}
	}

	return true
}

func mkdirPathIsRequiredParent(path string, requiredPaths []string) bool {
	path = strings.Trim(normalizeRunnerPath(path), "/")
	if path == "" {
		return false
	}

	for _, required := range requiredPaths {
		required = strings.Trim(normalizeRunnerPath(required), "/")
		if required == "" || !writePathIsNonSource(required) {
			continue
		}

		parent := strings.Trim(filepath.ToSlash(filepath.Dir(required)), "/")
		if path == parent || strings.HasPrefix(parent, path+"/") {
			return true
		}
	}

	return false
}

// writePathIsNonSource returns true for paths whose verification has
// no source-aware analog. Conservative list — adding extensions risks
// silently bypassing real verification gates.
func writePathIsNonSource(path string) bool {
	p := strings.ToLower(strings.TrimSpace(path))
	if p == "" {
		return false
	}

	return strings.HasSuffix(p, ".md") ||
		strings.HasSuffix(p, ".markdown") ||
		strings.HasSuffix(p, ".txt") ||
		strings.HasSuffix(p, ".rst")
}

func (r *Runner) ensureNonEmptyAnswer(
	ctx context.Context,
	turnMessages []llama.ChatMessage,
	reply llama.ChatMessage,
	opts llama.ChatOptions,
	collectedResults []workspace.ToolResult,
	metrics *runnerMetrics,
) (string, error) {

	trimmed := strings.TrimSpace(reply.Content)
	if trimmed != "" {
		if looksLikeTextualToolCall(trimmed) {
			if fallback, ok := deterministicNoToolsAnswer(r.agentName(), r.RuntimeBrief, collectedResults); ok {
				return fallback, nil
			}

			return "", fmt.Errorf("%s agent emitted textual tool call markup instead of a non-empty answer", r.agentName())
		}

		r.LastAssistantMessage = reply

		return trimmed, nil
	}

	retryMessages := append([]llama.ChatMessage(nil), turnMessages...)
	if strings.TrimSpace(reply.Content) != "" || strings.TrimSpace(reply.ReasoningContent) != "" {
		retryMessages = append(retryMessages, assistantHistoryMessageForOptions(llama.ChatMessage{
			Role:             util.FirstNonEmpty(strings.TrimSpace(reply.Role), "assistant"),
			Content:          reply.Content,
			ReasoningContent: reply.ReasoningContent,
		}, opts))
	}

	retryMessages = append(retryMessages, llama.ChatMessage{
		Role:    "system",
		Content: emptyAnswerRetryPrompt(r.RuntimeBrief),
	})

	if metrics != nil {
		metrics.ModelCalls++
	}

	// Strip orphaned tool_calls before the final API call so that
	// strict backends (DeepSeek) never see an assistant tool_calls
	// message without a matching role:"tool" response.
	retryMessages = stripOrphanedToolCalls(retryMessages)

	modelStart := time.Now()
	textOnlyOpts := textOnlyChatOptions(opts, r.RuntimeBrief)
	requestMessages := chatMessagesForOptions(retryMessages, textOnlyOpts)

	retryMessage, err := r.Client.CompleteChatMessage(ctx, requestMessages, textOnlyOpts)
	if err != nil {
		return "", err
	}

	if metrics != nil {
		metrics.addUsage(retryMessage.Usage)
	}

	r.emitModelCallTraceWithOptions(0, "empty_answer_retry", requestMessages, retryMessage, time.Since(modelStart).Milliseconds(), textOnlyOpts, r.supportsNativeTools(), collectedResults)

	retryReply := retryMessage.Content
	r.emitThinkingTrace(0, "empty_answer_retry_reply", retryMessage, textOnlyOpts, false)

	if len(retryMessage.ToolCalls) > 0 {
		if fallback, ok := deterministicNoToolsAnswer(r.agentName(), r.RuntimeBrief, collectedResults); ok {
			return fallback, nil
		}

		return "", fmt.Errorf("%s agent emitted a tool request instead of a non-empty answer", r.agentName())
	}

	if looksLikeTextualToolCall(retryReply) {
		if fallback, ok := deterministicNoToolsAnswer(r.agentName(), r.RuntimeBrief, collectedResults); ok {
			return fallback, nil
		}

		return "", fmt.Errorf("%s agent emitted textual tool call markup instead of a non-empty answer", r.agentName())
	}

	if _, _, ok, parseErr := workspace.ParseToolRequest(retryReply); parseErr != nil {
		return "", parseErr
	} else if ok {
		if fallback, ok := deterministicNoToolsAnswer(r.agentName(), r.RuntimeBrief, collectedResults); ok {
			return fallback, nil
		}

		return "", fmt.Errorf("%s agent emitted a tool request instead of a non-empty answer", r.agentName())
	}

	retryTrimmed := strings.TrimSpace(retryReply)
	if retryTrimmed == "" {
		if fallback, ok := deterministicNoToolsAnswer(r.agentName(), r.RuntimeBrief, collectedResults); ok {
			return fallback, nil
		}

		return "", fmt.Errorf("%s agent returned an empty response", r.agentName())
	}

	r.LastAssistantMessage = retryMessage

	return retryTrimmed, nil
}

func extractRunnerFilePath(summary string) string {
	path := extractReadFilePath(summary)
	if path == "" {
		return ""
	}

	fields := strings.Fields(path)
	if len(fields) >= 2 && looksLikeRunnerFileHandle(fields[0]) {
		return strings.TrimSpace(strings.Join(fields[1:], " "))
	}

	return path
}

func extractSemanticInspectionPath(summary string) string {
	trimmed := strings.TrimSpace(summary)
	if trimmed == "" {
		return ""
	}

	for _, marker := range []string{" lines ", ". Use "} {
		idx := strings.Index(trimmed, marker)
		if idx > 0 {
			trimmed = strings.TrimSpace(trimmed[:idx])
			break
		}
	}

	for _, prefix := range []string{"Read ", "Extracted ", "Outlined "} {
		if !strings.HasPrefix(trimmed, prefix) {
			continue
		}

		for _, separator := range []string{" in ", " from "} {
			idx := strings.LastIndex(trimmed, separator)
			if idx <= len(prefix) {
				continue
			}

			return strings.TrimSpace(trimmed[idx+len(separator):])
		}
	}

	return ""
}

func looksLikeRunnerFileHandle(token string) bool {
	if len(token) < 2 || token[0] != 'f' {
		return false
	}

	for _, r := range token[1:] {
		if r < '0' || r > '9' {
			return false
		}
	}

	return true
}

func containsAny(text string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}

	return false
}

// rewriteVisibilityNote returns a short system-message body explaining that
// the requested tool was redirected to a different tool by harness routing
// rules. Generic across rewrite kinds (delegated bootstrap, verification
// bootstrap, debug-log preflight, etc.) — it states the fact without
// hard-picking the next tool, leaving the model free to act on the result.
func rewriteVisibilityNote(originalTool, rewrittenTool string) string {
	original := strings.TrimSpace(originalTool)
	rewritten := strings.TrimSpace(rewrittenTool)

	switch {
	case original != "" && rewritten != "" && original != rewritten:
		return fmt.Sprintf("Note: your `%s` call was redirected to `%s` by lane routing. The rewritten call will execute and return its result; use that result, not another retry of `%s`.", original, rewritten, original)
	case rewritten != "":
		return fmt.Sprintf("Note: your tool call was redirected to `%s` by lane routing. The rewritten call will execute and return its result.", rewritten)
	default:
		return "Note: your tool call was rewritten by lane routing. The rewritten call will execute and return its result."
	}
}

// taskLedgerClosureHint inspects the collected tool-results set and, if the
// most recent task_ledger state still has any items flagged in_progress,
// returns a short reminder asking the model to close them with a final
// status in the upcoming reply. Returns the empty string when there is
// nothing open.
func taskLedgerClosureHint(results []workspace.ToolResult) string {
	var latest *workspace.ToolResult

	for i := len(results) - 1; i >= 0; i-- {
		if results[i].Tool == workspace.ToolNameTaskLedger && strings.TrimSpace(results[i].Error) == "" {
			copyResult := results[i]

			latest = &copyResult

			break
		}
	}

	if latest == nil {
		return ""
	}

	if !strings.Contains(strings.ToLower(latest.Output), "in_progress") {
		return ""
	}

	var openItems []string

	for _, line := range strings.Split(latest.Output, "\n") {
		lower := strings.ToLower(line)
		if strings.Contains(lower, "in_progress") {
			openItems = append(openItems, strings.TrimSpace(line))
		}
	}

	if len(openItems) == 0 {
		return ""
	}

	var b strings.Builder

	b.WriteString("Task ledger has open items you never closed. Resolve them in your reply (state each as completed / blocked / cancelled with one short evidence sentence):\n")

	for _, item := range openItems {
		b.WriteString("- ")
		b.WriteString(item)
		b.WriteString("\n")
	}

	return strings.TrimRight(b.String(), "\n")
}

// toolFailurePathStem returns the first two path segments of the request's
// target path (e.g. "internal/llm" for "internal/llm/llm.go"). Used by the
// same-stem failure counter so that five different hallucinated filenames
// inside the same made-up directory count as repeated failures, not fresh
// attempts.
func toolFailurePathStem(req workspace.ToolRequest) string {
	raw := strings.ReplaceAll(strings.TrimSpace(req.Path), "\\", "/")

	raw = strings.Trim(raw, "/")

	if raw == "" {
		return ""
	}

	parts := strings.Split(raw, "/")
	if len(parts) >= 2 {
		return parts[0] + "/" + parts[1]
	}

	return parts[0]
}

// recoveryNudgeInputs bundles the rejection-recovery-nudge state so
// each rule can be unit-tested in isolation without a 7-arg signature.
