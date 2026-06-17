package agent

import (
	"encoding/json"
	"fmt"
	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/pathparse"
	"idiom/internal/platform/util"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/workerruntime"
	"strings"
	"idiom/internal/runtime/agent/agentutil"
)

func (r *Runner) completionGateRejection(reply string, results []workspace.ToolResult, step int, taskLedger *taskLedgerState, declarationMovesForbidden bool) (workspace.ToolResult, bool) {
	if result, rejected := r.completionGatePipeline(step, taskLedger, declarationMovesForbidden).Evaluate(reply, results); rejected {
		return result, true
	}

	if r.CompletionGate == nil {
		return workspace.ToolResult{}, false
	}

	gate := r.CompletionGate(CompletionGateInput{
		AgentName: r.agentName(),
		Reply:     reply,
		Results:   append([]workspace.ToolResult(nil), results...),
		Steps:     step,
	})
	if gate.Accept {
		return workspace.ToolResult{}, false
	}

	summary := strings.TrimSpace(gate.Summary)
	if summary == "" {
		summary = "Final answer rejected"
	}

	errorText := strings.TrimSpace(gate.Error)
	if errorText == "" {
		errorText = "the answer does not satisfy the task completion gate; continue with the next tool call or state a concrete blocker"
	}

	return workspace.ToolResult{
		Tool:    "completion_gate",
		Summary: summary,
		Error:   errorText,
	}, true
}

func rawToolResultTranscriptCompletionRejection(reply string) (workspace.ToolResult, bool) {
	if !looksLikeRawToolResultTranscript(reply) {
		return workspace.ToolResult{}, false
	}

	return workspace.ToolResult{
		Tool:    "completion_gate",
		Summary: "Raw tool result transcript rejected as final answer",
		Error:   "the reply is an internal tool-result transcript, not a user-facing answer. Synthesize the answer from the gathered evidence, or state the concrete blocker/no-op reason; do not copy tool/status/output fields.",
	}, true
}

func (r *Runner) readOnlyEvidenceCompletionRejection(reply string, results []workspace.ToolResult) (workspace.ToolResult, bool) {
	if r == nil {
		return workspace.ToolResult{}, false
	}

	brief := workerruntime.NormalizeRuntimeBrief(r.RuntimeBrief)
	if !workerruntime.PermissionProfileIsReadOnlyWorkspace(brief.PermissionProfile) {
		return workspace.ToolResult{}, false
	}

	if brief.Readiness != nil && brief.Readiness.ReadyToAnswer {
		return workspace.ToolResult{}, false
	}

	directCandidateOnlyLedger, directCandidateOnly := readOnlyDirectAnswerCandidateOnlyLedger(brief, results)
	if brief.Readiness == nil &&
		!readOnlyExplanationGateApplies(brief) &&
		!workerruntime.TaskFamilyMatches(brief.TaskFamily, workerruntime.TaskFamilyDirectAnswer) &&
		!directCandidateOnly {

		return workspace.ToolResult{}, false
	}

	if len(genericReadOnlyEvidenceTools(brief.AllowedTools)) == 0 {
		return workspace.ToolResult{}, false
	}

	if strings.HasPrefix(strings.TrimSpace(reply), "<<QUESTION>>") {
		return workspace.ToolResult{}, false
	}

	if readOnlyDirectAnswerLane(brief) && readOnlyAnswerReportsInsufficientEvidence(reply) {
		ledger := readOnlyAnswerabilityForResults(readOnlyDirectAnswerAcceptanceLedgerBrief(brief), results)
		if len(ledger.SourceBodyPaths) > 0 || ledger.DiagnosticEvidence {
			return workspace.ToolResult{}, false
		}
	}

	if readOnlyDirectAnswerLane(brief) && readOnlyAnswerHasConcreteEvidence(results) {
		if acceptance := r.evaluateFinalAnswerAcceptance(reply, results); acceptance.BlockerKind == "" {
			return workspace.ToolResult{}, false
		}
	}

	if directCandidateOnly {
		candidates := closurePromptEvidencePaths(directCandidateOnlyLedger.CandidatePaths, 4)
		if len(candidates) > 0 {
			return workspace.ToolResult{
				Tool:    "completion_gate",
				Summary: "Read-only completion gate rejected final answer",
				Error:   "read-only workspace answers require bounded source-body or diagnostic evidence tied to the user's objective before a final answer. Candidate/search/context paths are not source-body evidence. Candidate path(s): " + strings.Join(candidates, ", ") + ". Inspect one candidate with source-body evidence, use one materially different source-owner probe if these candidates do not establish the claim, or answer `<<QUESTION>>:` with the exact unavailable evidence source.",
			}, true
		}
	}

	if genericReadOnlyAnswerEvidenceReady(brief, results) {
		return workspace.ToolResult{}, false
	}

	if readPaths := sortedRunnerPathKeys(readPathsFromResults(results)); len(readPaths) > 0 {
		return workspace.ToolResult{
			Tool:    "completion_gate",
			Summary: "Read-only completion gate rejected final answer",
			Error:   "read-only workspace answers require bounded source-body or diagnostic evidence tied to the user's objective before a final answer. Already inspected path(s) should not be reread: " + strings.Join(readPaths, ", ") + ". Use one targeted source search/read/navigation request for a different concrete code path tied to the explicit question, or answer `<<QUESTION>>:` with the exact unavailable evidence source.",
		}, true
	}

	return workspace.ToolResult{
		Tool:    "completion_gate",
		Summary: "Read-only completion gate rejected final answer",
		Error:   "read-only workspace answers require bounded source-body or diagnostic evidence tied to the user's objective before a final answer. Use one targeted read/search/discovery tool for the explicit question, or answer `<<QUESTION>>:` with the exact unavailable evidence source.",
	}, true
}

func readOnlyAnswerHasConcreteEvidence(results []workspace.ToolResult) bool {
	for _, result := range results {
		if concreteSourceEvidenceResult(result) || readOnlyDiagnosticEvidenceResult(result) {
			return true
		}
	}

	return false
}

func (r *Runner) readOnlyNonSubstantiveCompletionRejection(reply string, results []workspace.ToolResult) (workspace.ToolResult, bool) {
	if r == nil {
		return workspace.ToolResult{}, false
	}

	brief := workerruntime.NormalizeRuntimeBrief(r.RuntimeBrief)
	if !workerruntime.PermissionProfileIsReadOnlyWorkspace(brief.PermissionProfile) {
		return workspace.ToolResult{}, false
	}

	if !genericReadOnlyAnswerEvidenceReady(brief, results) {
		return workspace.ToolResult{}, false
	}

	trimmed := strings.TrimSpace(reply)
	if trimmed == "" || strings.HasPrefix(trimmed, "<<QUESTION>>") {
		return workspace.ToolResult{}, false
	}

	if len(strings.Fields(trimmed)) > 2 {
		return workspace.ToolResult{}, false
	}

	if len(pathparse.MentionedWorkspacePaths(trimmed)) > 0 {
		return workspace.ToolResult{}, false
	}

	return workspace.ToolResult{
		Tool:    "completion_gate",
		Summary: "Read-only completion gate rejected non-substantive final answer",
		Error:   "read-only workspace evidence has been gathered, but the reply is not a substantive user-facing answer. A substantive final answer should carry forward the inspected path and the concrete value or fact needed by the question.",
	}, true
}

func unresolvedProcessIntentCompletionRejection(reply string) (workspace.ToolResult, bool) {
	trimmed := strings.TrimSpace(reply)
	if trimmed == "" {
		return workspace.ToolResult{}, false
	}

	inspect := stripQuotedEvidenceForProcessIntent(trimmed)
	if strings.TrimSpace(inspect) == "" {
		return workspace.ToolResult{}, false
	}

	lower := " " + strings.ToLower(strings.Join(strings.Fields(inspect), " ")) + " "
	if agentutil.ContainsAny(lower,
		" blocked ",
		" blocker ",
		" cannot ",
		" can't ",
		" could not ",
		" unable ",
		" no access ",
		" permission ",
		" not available ",
	) {

		return workspace.ToolResult{}, false
	}

	if !agentutil.ContainsAny(lower,
		" let me search ", " let me inspect ", " let me read ", " let me refresh ", " let me run ", " let me check ", " let me audit ", " let me investigate ", " let me explore ", " let me look ",
		" let me call ", " let me use ",
		" i need to search ", " i need to inspect ", " i need to find ", " i need to read ", " i need to refresh ", " i need to run ", " i need to check ", " i need to audit ", " i need to investigate ", " i need to explore ", " i need to look ",
		" i need to call ", " i need to use ",
		" i should search ", " i should inspect ", " i should find ", " i should read ", " i should refresh ", " i should run ", " i should check ", " i should audit ", " i should investigate ", " i should explore ", " i should look ",
		" i should call ", " i should use ",
		" i'll search ", " i'll inspect ", " i'll read ", " i'll refresh ", " i'll run ", " i'll check ", " i'll audit ", " i'll investigate ", " i'll explore ", " i'll look ",
		" i'll call ", " i'll use ",
		" i will search ", " i will inspect ", " i will read ", " i will refresh ", " i will run ", " i will check ", " i will audit ", " i will investigate ", " i will explore ", " i will look ",
		" i will call ", " i will use ",
		" i'm going to search ", " i'm going to inspect ", " i'm going to read ", " i'm going to run ", " i'm going to check ", " i'm going to audit ", " i'm going to investigate ", " i'm going to explore ", " i'm going to look ",
		" i'm going to call ", " i'm going to use ",
		" i'll start by ", " i will start by ",
		" i'll ",
		" i will ",
	) {

		return workspace.ToolResult{}, false
	}

	if agentutil.ContainsAny(lower, " i'll ", " i will ") && !agentutil.ContainsAny(lower,
		" search ", " inspect ", " read ", " refresh ", " run ", " check ", " verify ",
	) {

		return workspace.ToolResult{}, false
	}

	return workspace.ToolResult{
		Tool:    "completion_gate",
		Summary: "Completion gate rejected unresolved process narration",
		Error:   "the reply describes an intended future search/read/inspection instead of completing the answer. Issue the needed tool call now, answer from gathered evidence, or state a concrete blocker.",
	}, true
}

func stripQuotedEvidenceForProcessIntent(text string) string {
	var out []string

	inFence := false

	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
			continue
		}

		if inFence {
			continue
		}

		if strings.HasPrefix(trimmed, ">") {
			continue
		}

		out = append(out, line)
	}

	return strings.Join(out, "\n")
}

// looksLikeToolResultTranscript returns true when the reply looks like
// a raw tool result transcript (e.g. "The inspected source in X contains
// func Foo, if bar != nil, and return baz"). Catches the pattern where
// the model dumps raw function signatures instead of synthesizing prose.
func looksLikeToolResultTranscript(lower string) bool {
	// "The inspected source in `path` contains `code`"
	if strings.Contains(lower, "the inspected source in") &&
		strings.Contains(lower, " contains `") {

		return true
	}

	if looksLikePathShowsSourceInventory(lower) {
		return true
	}

	if looksLikeInspectedBodySourceInventory(lower) {
		return true
	}

	if looksLikeLineOrientedSourceSnippetAnswer(lower) {
		return true
	}

	// " contains `f" where f is any function/symbol name
	if strings.Contains(lower, " contains `func ") ||
		strings.Contains(lower, " contains `type ") ||
		strings.Contains(lower, " contains `var ") ||
		strings.Contains(lower, " contains `const ") {

		return true
	}

	return false
}

func (r *Runner) readOnlySearchHitInventoryCompletionRejection(reply string, results []workspace.ToolResult) (workspace.ToolResult, bool) {
	if r == nil {
		return workspace.ToolResult{}, false
	}

	brief := workerruntime.NormalizeRuntimeBrief(r.RuntimeBrief)
	if !workerruntime.PermissionProfileIsReadOnlyWorkspace(brief.PermissionProfile) {
		return workspace.ToolResult{}, false
	}

	// A tool-result transcript answer is garbage regardless of
	// whether the evidence gate declares evidence ready. Check format
	// first, evidence status second.
	trimmed := strings.TrimSpace(reply)
	if trimmed == "" || strings.HasPrefix(trimmed, "<<QUESTION>>") {
		return workspace.ToolResult{}, false
	}

	lower := strings.ToLower(trimmed)
	if !looksLikeToolResultTranscript(lower) && !toolLocalSearchHandlePattern.MatchString(trimmed) {
		return workspace.ToolResult{}, false
	}

	return workspace.ToolResult{
		Tool:    "completion_gate",
		Summary: "Read-only completion gate rejected search-hit inventory",
		Error:   "the reply inventories search handles or raw hit lines instead of answering the user's question. Synthesize the answer from the inspected evidence; do not expose tool-local handles or search-result scaffolding.",
	}, true
}

func (r *Runner) unexecutedToolRequestCompletionRejection(reply string) (workspace.ToolResult, bool) {
	if !looksLikeUnexecutedToolRequestAnswer(reply) {
		return workspace.ToolResult{}, false
	}

	return workspace.ToolResult{
		Tool:    "completion_gate",
		Summary: "Unexecuted tool request rejected as final answer",
		Error: strings.Join([]string{
			"the reply is a workspace-tool request payload, not a final answer.",
			"If a workspace lookup is needed, issue it as a parser-readable tool call with the `tool` field and wait for the result.",
			"If no lookup is needed, answer the user in normal prose.",
			"Do not stop with raw JSON.",
		}, " "),
	}, true
}

func looksLikeUnexecutedToolRequestAnswer(reply string) bool {
	trimmed := strings.TrimSpace(reply)
	if trimmed == "" {
		return false
	}

	start, end, ok := firstToolRequestShapedJSONObjectSpan(trimmed)
	if !ok {
		return false
	}

	if markdownFenceOpenBefore(trimmed, start) {
		return false
	}

	outside := strings.TrimSpace(trimmed[:start] + "\n" + trimmed[end:])

	outside = strings.Trim(outside, "` \t\r\n:")

	if outside == "" {
		return true
	}

	if toolRequestJSONObjectAppearsAsStandaloneBlock(trimmed, start, end) {
		return true
	}

	fields := strings.Fields(outside)
	if len(fields) > 24 {
		return false
	}

	lower := " " + strings.ToLower(outside) + " "

	return util.ContainsAny(lower,
		" let me ",
		" i need ",
		" i will ",
		" i'll ",
		" start by ",
		" first ",
		" understanding ",
		" locating ",
		" inspect ",
		" checking ",
		" call ",
		" calling ",
		" use ",
	)
}

func toolRequestJSONObjectAppearsAsStandaloneBlock(text string, start int, end int) bool {
	if markdownFenceOpenBefore(text, start) {
		return false
	}

	lineStart := strings.LastIndexByte(text[:start], '\n') + 1
	if strings.TrimSpace(text[lineStart:start]) != "" {
		return false
	}

	lineEnd := len(text)
	if nextNewline := strings.IndexByte(text[end:], '\n'); nextNewline >= 0 {
		lineEnd = end + nextNewline
	}

	return strings.TrimSpace(text[end:lineEnd]) == ""
}

func markdownFenceOpenBefore(text string, pos int) bool {
	open := false

	for _, line := range strings.Split(text[:pos], "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			open = !open
		}
	}

	return open
}

func firstToolRequestShapedJSONObjectSpan(text string) (int, int, bool) {
	for searchFrom := 0; searchFrom < len(text); {
		relativeStart := strings.IndexByte(text[searchFrom:], '{')
		if relativeStart < 0 {
			return 0, 0, false
		}

		start := searchFrom + relativeStart
		object, end, ok := extractJSONObjectAt(text, start)

		if !ok {
			return 0, 0, false
		}

		if markdownFenceOpenBefore(text, start) {
			searchFrom = end
			continue
		}

		var fields map[string]json.RawMessage

		if err := json.Unmarshal([]byte(object), &fields); err == nil && toolRequestShapedJSONFields(fields) {
			return start, end, true
		}

		searchFrom = start + 1
	}

	return 0, 0, false
}

func extractJSONObjectAt(text string, start int) (string, int, bool) {
	if start < 0 || start >= len(text) || text[start] != '{' {
		return "", 0, false
	}

	depth := 0
	inString := false
	escaped := false

	for i := start; i < len(text); i++ {
		c := text[i]
		switch {
		case escaped:
			escaped = false
		case c == '\\' && inString:
			escaped = true
		case c == '"':
			inString = !inString
		case !inString && c == '{':
			depth++
		case !inString && c == '}':
			depth--
			if depth == 0 {
				return strings.TrimSpace(text[start : i+1]), i + 1, true
			}
		}
	}

	return "", 0, false
}

func toolRequestShapedJSONFields(fields map[string]json.RawMessage) bool {
	has := func(keys ...string) bool {
		for _, key := range keys {
			if _, ok := fields[key]; ok {
				return true
			}
		}

		return false
	}
	if has("tool") {
		return true
	}

	if has("edits") && has("rationale") {
		return true
	}

	if has("plan_artifact") {
		return true
	}

	if has("task") && has("mode", "limit", "budget_tokens", "required_tests", "required_edit_paths", "forbidden_paths", "scope", "path") {
		return true
	}

	if has("query") && has("resolution", "limit", "mode", "path", "language") {
		return true
	}

	if has("command") && has("workdir", "timeout_ms", "yield_time_ms", "sandbox_permissions", "justification") {
		return true
	}

	if has("agent") && has("message", "task", "items") {
		return true
	}

	if has("todos") && has("task_id", "items") {
		return true
	}

	if has("path") && has("old_text", "new_text", "replace_all") {
		return true
	}

	if has("capability") && has("path", "pattern", "query", "language") {
		return true
	}

	if has("path") && has("start_line", "end_line", "pattern", "glob", "language", "content", "recursive", "include_source", "include_files", "refresh", "freshness_policy", "handle", "symbol") {
		return true
	}

	if has("pattern") && has("path", "glob") {
		return true
	}

	return false
}

func syntheticLegacyToolCall(step int, req workspace.ToolRequest, raw string) llama.ToolCall {
	name := strings.TrimSpace(req.Tool)
	if name == "" {
		name = "workspace_tool"
	}

	args := strings.TrimSpace(raw)
	if args == "" {
		args = marshalToolRequestRaw(req)
	}

	return llama.ToolCall{
		ID:   fmt.Sprintf("idiom_legacy_tool_%d", step),
		Type: "function",
		Function: llama.ToolCallFunc{
			Name:      name,
			Arguments: args,
		},
	}
}

func syntheticLegacyParseToolCall(step int, raw string) llama.ToolCall {
	args := "{}"

	if trimmed := strings.TrimSpace(raw); trimmed != "" {
		if data, err := json.Marshal(map[string]string{"raw": util.Truncate(strings.TrimSpace(trimmed), 1000)}); err == nil {
			args = string(data)
		}
	}

	return llama.ToolCall{
		ID:   fmt.Sprintf("idiom_legacy_tool_%d", step),
		Type: "function",
		Function: llama.ToolCallFunc{
			Name:      "tool_parse",
			Arguments: args,
		},
	}
}

func legacyToolCallHistoryMessage(reply llama.ChatMessage, call llama.ToolCall) llama.ChatMessage {
	return llama.ChatMessage{
		Role:             util.FirstNonEmpty(strings.TrimSpace(reply.Role), "assistant"),
		ReasoningContent: reply.ReasoningContent,
		ToolCalls:        []llama.ToolCall{call},
	}
}

func textualToolRequestHistoryMessage(reply llama.ChatMessage, req workspace.ToolRequest, raw string, opts llama.ChatOptions) llama.ChatMessage {
	content := strings.TrimSpace(raw)
	if content == "" || !strings.Contains(content, `"tool"`) {
		content = marshalToolRequestRaw(req)
	}

	content = strings.TrimSpace(content)

	message := llama.ChatMessage{
		Role:             util.FirstNonEmpty(strings.TrimSpace(reply.Role), "assistant"),
		Content:          content,
		ReasoningContent: reply.ReasoningContent,
	}

	return assistantHistoryMessageForOptions(message, opts)
}
