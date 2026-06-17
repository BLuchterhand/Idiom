package agent

import (
	"encoding/json"
	"idiom/internal/capabilities/workspace"
	"idiom/internal/platform/util"
	"idiom/internal/runtime/llama"
	"regexp"
	"strconv"
	"strings"
	"idiom/internal/runtime/agent/agentutil"
)

func (r *Runner) salvageToolRequestFromReasoning(reply llama.ChatMessage, results []workspace.ToolResult) (workspace.ToolRequest, string, bool) {
	if r == nil || strings.TrimSpace(reply.Content) != "" {
		return workspace.ToolRequest{}, "", false
	}

	reasoning := strings.TrimSpace(reply.ReasoningContent)
	if reasoning == "" {
		return workspace.ToolRequest{}, "", false
	}

	for _, candidate := range reasoningToolRequestCandidates(reasoning) {
		req, raw, ok, err := workspace.ParseToolRequest(candidate)
		if !ok || err != nil {
			continue
		}

		if r.safeReasoningToolRequest(req, results) {
			return req, raw, true
		}
	}

	return workspace.ToolRequest{}, "", false
}

func (r *Runner) salvageVisibleToolRequestFromProse(reply string, results []workspace.ToolResult) (workspace.ToolRequest, string, bool) {
	if r == nil {
		return workspace.ToolRequest{}, "", false
	}

	trimmed := strings.TrimSpace(reply)
	if req, raw, ok := parseVisibleLineToolRequest(trimmed); ok && r.safeVisibleToolRequest(req, results) {
		return req, raw, true
	}

	if req, raw, ok := parseMalformedVisibleReadOnlyToolJSONPrefix(trimmed); ok && r.safeVisibleToolRequest(req, results) {
		return req, raw, true
	}

	start, end, ok := firstToolRequestShapedJSONObjectSpan(trimmed)
	if !ok {
		return workspace.ToolRequest{}, "", false
	}

	raw := trimmed[start:end]

	var fields map[string]json.RawMessage

	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		return workspace.ToolRequest{}, "", false
	}

	req, parsedRaw, ok := parseVisibleToolRequestJSONObject(raw, fields)
	if !ok {
		return workspace.ToolRequest{}, "", false
	}

	if strings.TrimSpace(req.Tool) != workspace.ToolNameSubmitPlan &&
		!looksLikeUnexecutedToolRequestAnswer(trimmed) &&
		!r.explicitVisibleToolJSONRecoveryAllowed(trimmed, fields, results) {

		return workspace.ToolRequest{}, "", false
	}

	if r.safeVisibleToolRequest(req, results) {
		return req, parsedRaw, true
	}

	if repaired, repairedRaw, repairedOK := r.delegatedReadOnlyVisibleDiscoveryRepair(results, req); repairedOK && r.safeVisibleToolRequest(repaired, results) {
		return repaired, repairedRaw, true
	}

	return workspace.ToolRequest{}, "", false
}

func (r *Runner) delegatedReadOnlyVisibleDiscoveryRepair(results []workspace.ToolResult, req workspace.ToolRequest) (workspace.ToolRequest, string, bool) {
	if r == nil || len(results) > 0 || strings.TrimSpace(req.Tool) != workspace.ToolNameDiscoveryQuery {
		return workspace.ToolRequest{}, "", false
	}

	repaired, ok := r.delegatedReadOnlyDiscoveryQueryRepairRequest(results, req)
	if !ok {
		return workspace.ToolRequest{}, "", false
	}

	raw := marshalToolRequestRaw(repaired)
	if raw == "" {
		return workspace.ToolRequest{}, "", false
	}

	return repaired, raw, true
}

func parseMalformedVisibleReadOnlyToolJSONPrefix(text string) (workspace.ToolRequest, string, bool) {
	text = strings.TrimSpace(text)
	if text == "" {
		return workspace.ToolRequest{}, "", false
	}

	if _, _, ok := firstToolRequestShapedJSONObjectSpan(text); ok {
		return workspace.ToolRequest{}, "", false
	}

	start := strings.Index(text, "{")
	if start < 0 || markdownFenceOpenBefore(text, start) {
		return workspace.ToolRequest{}, "", false
	}

	candidate := text[start:]
	if !strings.Contains(candidate, `"tool"`) {
		return workspace.ToolRequest{}, "", false
	}

	req := workspace.ToolRequest{
		Tool:            firstJSONLikeStringField(candidate, "tool"),
		Path:            firstJSONLikeStringField(candidate, "path"),
		Pattern:         firstJSONLikeStringField(candidate, "pattern"),
		Query:           firstJSONLikeStringField(candidate, "query"),
		Glob:            firstJSONLikeStringField(candidate, "glob"),
		Capability:      firstJSONLikeStringField(candidate, "capability"),
		NamedGap:        firstJSONLikeStringField(candidate, "named_gap"),
		WhyNotAnswering: firstJSONLikeStringField(candidate, "why_not_answering"),
		Symbol:          firstJSONLikeStringField(candidate, "symbol"),
	}
	if v, ok := firstJSONLikeIntField(candidate, "start_line"); ok {
		req.StartLine = v
	}

	if v, ok := firstJSONLikeIntField(candidate, "end_line"); ok {
		req.EndLine = v
	}

	if v, ok := firstJSONLikeIntField(candidate, "limit"); ok {
		req.Limit = v
	}

	if v, ok := firstJSONLikeIntField(candidate, "budget_tokens"); ok {
		req.BudgetTokens = v
	}

	if strings.TrimSpace(req.Tool) == "" {
		return workspace.ToolRequest{}, "", false
	}

	if !malformedVisibleReadOnlyToolCanSalvage(req.Tool) {
		return workspace.ToolRequest{}, "", false
	}

	raw := marshalToolRequestRaw(req)

	return req, raw, true
}

func malformedVisibleReadOnlyToolCanSalvage(tool string) bool {
	switch strings.TrimSpace(tool) {
	case workspace.ToolNameReadFile:
		return true
	case workspace.ToolNameSearchContent, workspace.ToolNameSearchFiles, workspace.ToolNameContextPack, workspace.ToolNameContextQuery, workspace.ToolNameContextZoom, workspace.ToolNameDiscoveryQuery, workspace.ToolNameStructuralSearch, workspace.ToolNameFileStats, workspace.ToolNameCodeNavigate:
		return true
	default:
		return false
	}
}

func firstJSONLikeStringField(text string, key string) string {
	pattern := regexp.MustCompile(`(?s)"` + regexp.QuoteMeta(key) + `"\s*:\s*"((?:\\.|[^"\\])*)"`)
	match := pattern.FindStringSubmatch(text)

	if len(match) != 2 {
		return ""
	}

	decoded, err := strconv.Unquote(`"` + match[1] + `"`)
	if err != nil {
		return strings.TrimSpace(match[1])
	}

	return strings.TrimSpace(decoded)
}

func firstJSONLikeIntField(text string, key string) (int, bool) {
	pattern := regexp.MustCompile(`(?s)"` + regexp.QuoteMeta(key) + `"\s*:\s*"?([0-9]+)"?`)
	match := pattern.FindStringSubmatch(text)

	if len(match) != 2 {
		return 0, false
	}

	value, err := strconv.Atoi(match[1])
	if err != nil {
		return 0, false
	}

	return value, true
}

func (r *Runner) explicitVisibleToolJSONRecoveryAllowed(text string, fields map[string]json.RawMessage, results []workspace.ToolResult) bool {
	if r == nil || !completionGateResultSeen(results) {
		return false
	}

	rawTool, ok := fields["tool"]
	if !ok {
		return false
	}

	var tool string

	if err := json.Unmarshal(rawTool, &tool); err != nil || strings.TrimSpace(tool) == "" {
		return false
	}

	return countToolRequestShapedJSONObjects(text) == 1
}

func countToolRequestShapedJSONObjects(text string) int {
	count := 0

	for searchFrom := 0; searchFrom < len(text); {
		start, end, ok := firstToolRequestShapedJSONObjectSpan(text[searchFrom:])
		if !ok {
			break
		}

		count++

		searchFrom += end

		if start == end {
			searchFrom++
		}
	}

	return count
}

func parseVisibleLineToolRequest(text string) (workspace.ToolRequest, string, bool) {
	text = strings.TrimSpace(text)
	if text == "" || strings.Contains(text, "{") || strings.Contains(text, "```") {
		return workspace.ToolRequest{}, "", false
	}

	// Scan for the first tool: line, skipping leading prose.
	// Models sometimes emit conversational text before a tool call.
	lines := strings.Split(text, "\n")
	toolLineIdx := -1
	proseLines := 0

	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		key, _, ok := strings.Cut(line, ":")
		if ok && util.EqualTrimFold(key, "tool") {
			toolLineIdx = i
			break
		}

		proseLines++
		if proseLines > 10 {
			return workspace.ToolRequest{}, "", false
		}
	}

	if toolLineIdx < 0 {
		return workspace.ToolRequest{}, "", false
	}

	// Parse from the tool: line onward.
	values := map[string]string{}

	for _, line := range lines[toolLineIdx:] {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		key, value, ok := strings.Cut(line, ":")
		if !ok {
			return workspace.ToolRequest{}, "", false
		}

		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.Trim(strings.TrimSpace(value), `"'`)

		if key == "" || value == "" {
			return workspace.ToolRequest{}, "", false
		}

		switch key {
		case "tool", "path", "pattern", "query", "capability", "mode", "task", "named_gap", "why_not_answering",
			"start_line", "end_line", "limit", "budget_tokens":
			values[key] = value
		default:
			return workspace.ToolRequest{}, "", false
		}
	}

	tool := strings.TrimSpace(values["tool"])
	if tool == "" {
		return workspace.ToolRequest{}, "", false
	}

	req := workspace.ToolRequest{
		Tool:            tool,
		Path:            values["path"],
		Pattern:         values["pattern"],
		Query:           values["query"],
		Capability:      values["capability"],
		Mode:            values["mode"],
		Task:            values["task"],
		NamedGap:        values["named_gap"],
		WhyNotAnswering: values["why_not_answering"],
	}
	for key, assign := range map[string]func(int){
		"start_line":    func(v int) { req.StartLine = v },
		"end_line":      func(v int) { req.EndLine = v },
		"limit":         func(v int) { req.Limit = v },
		"budget_tokens": func(v int) { req.BudgetTokens = v },
	} {
		if raw := strings.TrimSpace(values[key]); raw != "" {
			v, err := strconv.Atoi(raw)
			if err != nil {
				return workspace.ToolRequest{}, "", false
			}

			assign(v)
		}
	}

	return req, marshalToolRequestRaw(req), true
}

func parseVisibleToolRequestJSONObject(raw string, fields map[string]json.RawMessage) (workspace.ToolRequest, string, bool) {
	if _, hasExplicitTool := fields["tool"]; hasExplicitTool {
		req, parsedRaw, ok, err := workspace.ParseToolRequest(raw)
		if !ok || err != nil {
			return workspace.ToolRequest{}, "", false
		}

		return req, parsedRaw, true
	}

	if _, hasEdits := fields["edits"]; hasEdits {
		if _, hasRationale := fields["rationale"]; hasRationale {
			req, err := workspace.DecodeToolRequestJSON([]byte(raw))
			if err != nil {
				return workspace.ToolRequest{}, "", false
			}

			req.Tool = workspace.ToolNameSubmitPlan

			return req, marshalToolRequestRaw(req), true
		}
	}

	if _, hasPlanArtifact := fields["plan_artifact"]; hasPlanArtifact {
		req, err := workspace.DecodeToolRequestJSON([]byte(raw))
		if err != nil {
			return workspace.ToolRequest{}, "", false
		}

		req.Tool = workspace.ToolNameSubmitPlan

		return req, marshalToolRequestRaw(req), true
	}

	if _, hasCapability := fields["capability"]; !hasCapability {
		return workspace.ToolRequest{}, "", false
	}

	req, err := workspace.DecodeToolRequestJSON([]byte(raw))
	if err != nil {
		return workspace.ToolRequest{}, "", false
	}

	if strings.TrimSpace(req.Tool) == "" && strings.TrimSpace(req.Capability) != "" {
		if workspace.CodeNavigateCapabilityAlias(req.Capability) == "" {
			return workspace.ToolRequest{}, "", false
		}

		req.Tool = workspace.ToolNameCodeNavigate
	}

	if strings.TrimSpace(req.Tool) == "" {
		return workspace.ToolRequest{}, "", false
	}

	return req, marshalToolRequestRaw(req), true
}


func (r *Runner) safeVisibleToolRequest(req workspace.ToolRequest, results []workspace.ToolResult) bool {
	if strings.TrimSpace(req.Tool) == workspace.ToolNameSubmitPlan {
		return r.safeVisibleSubmitPlanRequest(req, results)
	}

	if r.safeVisiblePlanExecuteMissingTargetReadRequest(req) {
		return true
	}

	if r.safeVisibleWorkspaceWriteRequest(req, results) {
		return true
	}

	return r.safeReasoningToolRequest(req, results)
}

func (r *Runner) safeVisibleWorkspaceWriteRequest(req workspace.ToolRequest, results []workspace.ToolResult) bool {
	if r == nil || !runnerBriefRequiresMutation(r.RuntimeBrief) || !workspace.IsWorkspaceWriteTool(req.Tool) {
		return false
	}

	if reasoningToolRequestHasPlaceholder(req) || !safeWorkspaceWriteRequestShape(req) {
		return false
	}

	if _, _, reject := requiredEditPathScopeRejection(req, r.RuntimeBrief); reject {
		return false
	}

	if len(r.RuntimeBrief.RequiredEditPaths) > 0 {
		return r.safeReasoningWorkspaceWriteRequest(req)
	}

	if !completionGateResultSeen(results) {
		return false
	}

	paths := writeRequestPaths(req)
	if len(paths) == 0 {
		return false
	}

	for _, path := range paths {
		if !concreteSourceEvidenceReadForPath(path, results) {
			return false
		}
	}

	return true
}

func (r *Runner) safeVisiblePlanExecuteMissingTargetReadRequest(req workspace.ToolRequest) bool {
	if r == nil || !r.planThenExecutePlannerPhase() {
		return false
	}

	if strings.TrimSpace(req.Tool) != workspace.ToolNameReadFile {
		return false
	}

	return planThenExecuteReadRequestMissingConcreteTarget(req)
}

func (r *Runner) safeVisibleSubmitPlanRequest(req workspace.ToolRequest, results []workspace.ToolResult) bool {
	if r == nil || !r.planThenExecutePlannerPhase() || strings.TrimSpace(req.Tool) != workspace.ToolNameSubmitPlan {
		return false
	}

	if reasoningToolRequestHasPlaceholder(req) {
		return false
	}

	if !r.planThenExecuteSubmitPlanOnlyVisibleJSONSalvageAllowed(results) {
		return false
	}

	edits := planThenExecuteEffectiveSubmitPlanEdits(req)
	if len(edits) == 0 {
		return false
	}

	covered := make(map[string]struct{}, len(edits))
	for _, edit := range edits {
		path := agentutil.NormalizeRunnerPath(edit.Path)
		if path != "" {
			covered[path] = struct{}{}
		}
	}

	required := r.requiredEditCoveragePaths()
	if len(required) == 0 {
		return len(covered) > 0
	}

	return len(missingRequiredEditPaths(required, covered)) == 0
}

func (r *Runner) planThenExecuteSubmitPlanOnlyVisibleJSONSalvageAllowed(results []workspace.ToolResult) bool {
	if r == nil || !r.planThenExecutePlannerPhase() {
		return false
	}

	return planThenExecuteTerminalSubmitPlanAllowed(r.RuntimeBrief, results) ||
		planThenExecuteOpenCandidateSourceCanSubmit(r.RuntimeBrief, results) ||
		planThenExecuteRejectedPlanCorrectionOnly(r.RuntimeBrief, results)
}

func reasoningToolRequestCandidates(reasoning string) []string {
	reasoning = strings.TrimSpace(reasoning)
	if reasoning == "" {
		return nil
	}

	candidates := make([]string, 0, 3)

	for offset := 0; offset < len(reasoning); {
		idx := strings.Index(reasoning[offset:], "```tool")
		if idx < 0 {
			break
		}

		start := offset + idx
		rest := reasoning[start:]
		end := strings.Index(rest, "\n```")

		if end < 0 {
			break
		}

		candidates = append(candidates, rest[:end+4])
		offset = start + end + 4
	}

	if strings.Contains(reasoning, "<tool_call>") || strings.Contains(reasoning, "<function=") {
		candidates = append(candidates, reasoning)
	}

	if strings.Contains(reasoning, `"tool"`) {
		candidates = append(candidates, reasoning)
	}

	return candidates
}

func (r *Runner) safeReasoningToolRequest(req workspace.ToolRequest, results []workspace.ToolResult) bool {
	tool := strings.TrimSpace(req.Tool)
	if tool == "" || reasoningToolRequestHasPlaceholder(req) {
		return false
	}

	switch tool {
	case workspace.ToolNameReadFile:
		return strings.TrimSpace(req.Path) != ""
	case workspace.ToolNameSearchContent:
		return strings.TrimSpace(req.Pattern) != ""
	case workspace.ToolNameSearchFiles:
		return strings.TrimSpace(req.Pattern) != "" || strings.TrimSpace(req.Glob) != ""
	case workspace.ToolNameContextPack:
		return strings.TrimSpace(req.Task) != "" && !contextPackUnavailableForRecovery(results)
	case workspace.ToolNameContextQuery:
		return strings.TrimSpace(req.Query) != ""
	case workspace.ToolNameContextZoom:
		return strings.TrimSpace(req.Handle) != ""
	case workspace.ToolNameDiscoveryQuery:
		return safeDiscoveryQueryRequest(req)
	case workspace.ToolNameStructuralSearch:
		return strings.TrimSpace(req.Pattern) != ""
	case workspace.ToolNameCodeNavigate:
		return workspace.CodeNavigateCapabilityAlias(req.Capability) != "" &&
			(strings.TrimSpace(req.Path) != "" || strings.TrimSpace(req.Pattern) != "" || strings.TrimSpace(req.Query) != "")
	case workspace.ToolNameSubmitPlan:
		return false
	default:
		return r.safeReasoningWorkspaceWriteRequest(req)
	}
}

func safeDiscoveryQueryRequest(req workspace.ToolRequest) bool {
	if strings.TrimSpace(util.FirstNonEmpty(req.Purpose, req.Query, req.Pattern, req.Path)) == "" {
		return false
	}

	if purpose := strings.TrimSpace(req.Purpose); purpose != "" {
		return normalizeRunnerDiscoveryQueryPurpose(purpose) != ""
	}

	if query := strings.TrimSpace(req.Query); query != "" {
		return normalizeRunnerDiscoveryQueryPurpose(query) != ""
	}

	return true
}

func normalizeRunnerDiscoveryQueryPurpose(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "-", "_")
	value = strings.ReplaceAll(value, " ", "_")

	switch value {
	case "impact", "impact_analysis":
		return "impact_analysis"
	case "trace", "function_trace", "function_tracing":
		return "function_tracing"
	case "architecture", "architecture_boundary", "boundary", "tool_usefulness", "tooling_usefulness", "discovery_tooling", "discovery_tools", "usefulness":
		return "architecture_boundary"
	case "security", "security_review":
		return "security_review"
	case "history", "history_context":
		return "history_context"
	default:
		if strings.Contains(value, "security") || strings.Contains(value, "vulnerability") {
			return "security_review"
		}

		if strings.Contains(value, "history") || strings.Contains(value, "evolution") {
			return "history_context"
		}

		if strings.Contains(value, "impact") || strings.Contains(value, "blast_radius") {
			return "impact_analysis"
		}

		if strings.Contains(value, "trace") || strings.Contains(value, "tracing") || strings.Contains(value, "call_graph") {
			return "function_tracing"
		}

		if strings.Contains(value, "architecture") ||
			strings.Contains(value, "boundary") ||
			(strings.Contains(value, "discovery") && (strings.Contains(value, "implementation") || strings.Contains(value, "definition") || strings.Contains(value, "orchestration") || strings.Contains(value, "pattern"))) {

			return "architecture_boundary"
		}

		return ""
	}
}

func (r *Runner) safeReasoningWorkspaceWriteRequest(req workspace.ToolRequest) bool {
	if r == nil || !runnerBriefRequiresMutation(r.RuntimeBrief) || len(r.RuntimeBrief.RequiredEditPaths) == 0 {
		return false
	}

	if !workspace.IsWorkspaceWriteTool(req.Tool) {
		return false
	}

	paths := writeRequestPaths(req)
	if len(paths) == 0 {
		return false
	}

	if _, _, reject := requiredEditPathScopeRejection(req, r.RuntimeBrief); reject {
		return false
	}

	return safeWorkspaceWriteRequestShape(req)
}

func safeWorkspaceWriteRequestShape(req workspace.ToolRequest) bool {
	switch strings.TrimSpace(req.Tool) {
	case workspace.ToolNameEditFile:
		hasOldNew := strings.TrimSpace(req.OldText) != "" && (strings.TrimSpace(req.NewText) != "" || req.NewText == "")
		hasRange := req.StartLine > 0 && req.EndLine >= req.StartLine && strings.TrimSpace(req.Content) != ""

		return hasOldNew || hasRange
	case workspace.ToolNameWriteFile, workspace.ToolNameAppendFile:
		return strings.TrimSpace(req.Content) != ""
	case workspace.ToolNameApplyPatch:
		if strings.TrimSpace(req.Content) == "" {
			return false
		}

		_, err := workspace.ParsePatch(req.Content)

		return err == nil
	default:
		return false
	}
}

func reasoningToolRequestHasPlaceholder(req workspace.ToolRequest) bool {
	fields := []string{
		req.Path,
		req.Task,
		req.Pattern,
		req.Query,
		req.Content,
		req.OldText,
		req.NewText,
		req.NamedGap,
		req.WhyNotAnswering,
		req.Rationale,
	}
	for _, edit := range reasoningSubmitPlanEdits(req) {
		fields = append(fields,
			edit.Path,
			edit.OldText,
			edit.NewText,
			edit.NewTextRef,
			edit.Content,
			edit.ContentRef,
			edit.Purpose,
		)
	}

	for _, field := range fields {
		lower := strings.ToLower(strings.TrimSpace(field))
		if lower == "" {
			continue
		}

		if agentutil.ContainsAny(lower,
			"candidate/path.go",
			"relative/file",
			"exact current text",
			"exact current substring",
			"replacement text",
			"complete replacement text",
			"why these candidate path edits satisfy the objective",
			"one paragraph on the overall plan",
			"why this edit",
			"one sentence",
			"<full file body>",
			"<user objective>",
			"<path>",
			"<pattern>",
		) {

			return true
		}
	}

	return false
}

func reasoningSubmitPlanHasConcreteEdit(req workspace.ToolRequest) bool {
	edits := reasoningSubmitPlanEdits(req)
	if len(edits) == 0 {
		return false
	}

	for _, edit := range edits {
		if strings.TrimSpace(edit.Path) == "" {
			continue
		}

		hasOldText := strings.TrimSpace(edit.OldText) != ""
		hasContent := strings.TrimSpace(edit.Content) != "" || strings.TrimSpace(edit.ContentRef) != ""
		hasRange := edit.StartLine > 0 && edit.EndLine >= edit.StartLine && hasContent
		hasReplace := edit.ReplaceAll && (strings.TrimSpace(edit.NewText) != "" || strings.TrimSpace(edit.NewTextRef) != "")

		if (hasOldText && workspace.PlanEditHasExplicitReplacementText(edit)) || hasRange || hasReplace {
			return true
		}
	}

	return false
}

func reasoningSubmitPlanEdits(req workspace.ToolRequest) []workspace.PlanEdit {
	if req.PlanArtifact != nil && len(req.PlanArtifact.Edits) > 0 {
		return append([]workspace.PlanEdit(nil), req.PlanArtifact.Edits...)
	}

	return append([]workspace.PlanEdit(nil), req.Edits...)
}
