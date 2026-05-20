package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"idiom/internal/agent"
	"idiom/internal/frontier/pathparse"
	"idiom/internal/frontier/workerruntime"
	"idiom/internal/llama"
	"idiom/internal/util"
)

// promptExecutionPlan is the minimal OpenCode-aligned carrier for a turn.
// All the classification fields from the prior lane-based shape are gone;
// the Mode the user picked is the only routing signal the harness cares
// about.
type promptExecutionPlan struct {
	mode         agent.Mode
	route        turnRoutePlan
	runtimeBrief workerruntime.WorkerRuntimeBrief
}

// RunPromptOnce is the single entry point every caller uses to send one
// prompt and get one reply. There are no resume variants, no lane
// dispatch, no task-memory gates — OpenCode's shape is one loop.
func (m *Model) RunPromptOnce(ctx context.Context, prompt string) (string, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return "", fmt.Errorf("prompt is required")
	}
	m.emitJSONStart(prompt)
	if err := m.ensureServerReady(ctx); err != nil {
		m.emitJSONError("server_not_ready", err.Error())
		return "", err
	}
	return m.runStandardPromptOnce(ctx, prompt)
}

func (m *Model) runStandardPromptOnce(ctx context.Context, prompt string) (string, error) {
	m.lastAssistantReasoningContent = ""
	plan := m.buildPromptExecutionPlan(ctx, prompt)
	chatOpts := tuneChatOptionsForPlan(m.chatOptions(), plan)
	chatOpts = applyMaxTokensOverride(chatOpts, m.maxTokensOverride)
	if m.workspace != nil {
		m.workspace.PrimeDiscoveredPaths(pathparse.MentionedWorkspacePaths(prompt))
	}
	m.messages = append(m.messages, llama.ChatMessage{Role: "user", Content: prompt})
	m.logMessage("user", prompt)
	m.logMessage("thinking_config", samplingSummaryForOptions(chatOpts))
	if summary := strings.TrimSpace(renderTurnRoutePlan(plan.route)); summary != "" {
		m.logMessage("route_plan", summary)
	}
	m.emitWriterRouteDecision(m.writerRouteDecisionForPrompt(prompt, plan))
	reply, err := m.runAgentTurn(ctx, prompt, m.requestMessages(), chatOpts, plan)
	if err != nil {
		m.emitJSONError("run_failed", err.Error())
		return "", err
	}
	return m.finishPromptOnceReply(reply), nil
}

func (m *Model) finishPromptOnceReply(reply string) string {
	m.takeLastAssistantReasoningContent()
	m.messages = append(m.messages, llama.ChatMessage{Role: "assistant", Content: reply})
	m.logMessage("assistant", reply)
	m.emitJSONFinal(reply)
	return reply
}

// currentMode resolves the Mode selected for this session. Defaults to
// BuildMode if the user hasn't picked one.
func (m *Model) currentMode() agent.Mode {
	if strings.TrimSpace(m.agentModeName) != "" {
		return agent.ModeOrDefault(m.agentModeName)
	}
	return agent.ModeOrDefault(m.cfg.Chat.AgentMode)
}

func (m *Model) buildPromptExecutionPlan(ctx context.Context, prompt string) promptExecutionPlan {
	mode := m.currentMode()
	if family := strings.TrimSpace(m.forcedTaskFamily); family != "" {
		route := normalizeTurnRoutePlan(turnRoutePlan{
			Family: family,
			Reason: "explicit_task_family",
		}, mode)
		brief := m.runtimeBriefForRoute(route, prompt)
		return promptExecutionPlan{
			mode:         mode,
			route:        route,
			runtimeBrief: brief,
		}
	}
	route := m.classifyTurnRoute(ctx, prompt, mode)
	brief := m.runtimeBriefForRoute(route, prompt)
	return promptExecutionPlan{
		mode:         mode,
		route:        route,
		runtimeBrief: brief,
	}
}

func (m *Model) runtimeBriefForRoute(route turnRoutePlan, prompt string) workerruntime.WorkerRuntimeBrief {
	mode := m.currentMode()
	brief := turnRoutePlanRuntimeBrief(route, mode, m.workspaceAllowedTools())
	brief.Objective = strings.TrimSpace(pathparse.ScopeToBriefBody(prompt))
	if brief.Objective == "" {
		brief.Objective = strings.TrimSpace(prompt)
	}
	brief.TargetRefs = appendUniqueBriefStrings(brief.TargetRefs, pathparse.MentionedWorkspacePaths(prompt)...)
	brief.EvidencePaths = appendUniqueBriefStrings(brief.EvidencePaths, promptContextEvidencePaths(prompt)...)
	brief.TargetPacket = appendUniqueBriefStrings(brief.TargetPacket, promptContextPacketMarkers(prompt)...)
	allowed := allowedEditPathsFromPrompt(prompt)
	required := requiredEditPathsFromPrompt(prompt)
	implementationLike := isImplementationLikeFamily(route.Family)
	if !implementationLike {
		if len(required) > 0 {
			brief.TargetRefs = appendUniqueBriefStrings(brief.TargetRefs, required...)
		}
		required = nil
	}
	if implementationLike && len(required) == 0 && len(allowed) > 0 && promptHasRequiredEditPayload(prompt) {
		required = appendUniqueBriefStrings(required, allowed...)
		allowed = nil
	}
	if len(allowed) > 0 {
		brief.TargetRefs = appendUniqueBriefStrings(brief.TargetRefs, allowed...)
		brief.SearchPolicy = appendUniqueBriefStrings(brief.SearchPolicy,
			"treat allowed_edit_paths as candidate write scope, not required coverage obligations",
		)
	}
	if len(required) > 0 {
		brief.RequiredEditPaths = appendUniqueBriefStrings(brief.RequiredEditPaths, required...)
		brief.RequiredEditSnippets = appendRequiredEditSnippets(brief.RequiredEditSnippets, requiredEditSnippetsFromPrompt(prompt, brief.RequiredEditPaths)...)
		brief.Constraints = appendUniqueBriefStrings(brief.Constraints, "required_edit_paths_define_write_scope")
		brief.RequiredEvidence = appendUniqueBriefStrings(brief.RequiredEvidence, "required_edit_paths_covered_or_blocked")
		brief.RequiredNext = appendUniqueBriefStrings(brief.RequiredNext,
			"use semantic/context/declared project discovery or narrow reads only for one named edit-safety fact; after that fact is resolved, edit an in-scope required path or report <<QUESTION>>",
		)
		brief.ReadinessGate = appendUniqueBriefStrings(brief.ReadinessGate, "required_edit_paths_satisfied")
		brief.SearchPolicy = appendUniqueBriefStrings(brief.SearchPolicy,
			"treat required_edit_paths plus packet evidence as edit scope; prefer semantic/context/declared project discovery before narrow required-path reads for named edit-safety facts",
		)
		brief.FinalCritic = appendUniqueBriefStrings(brief.FinalCritic, "required_edit_paths_reported")
		if len(brief.RequiredEditSnippets) > 0 {
			brief.RequiredEvidence = appendUniqueBriefStrings(brief.RequiredEvidence, "required_edit_snippets_present_or_blocked")
			brief.ReadinessGate = appendUniqueBriefStrings(brief.ReadinessGate, "required_edit_snippets_satisfied")
			brief.FinalCritic = appendUniqueBriefStrings(brief.FinalCritic, "required_edit_snippets_reported")
		}
	}
	if plan := defaultVerificationPlan(m.projectRoot()); len(brief.VerificationPlan) == 0 {
		brief.VerificationPlan = plan
	}
	if route.Family == "verification_only" {
		brief.RequiredNext = appendUniqueBriefStrings(brief.RequiredNext,
			"first tool call must run one verification command with shell",
		)
		brief.InvalidActions = appendUniqueBriefStrings(brief.InvalidActions,
			"pwd before verification",
			"ls before verification",
			"find before verification",
		)
		if len(brief.VerificationPlan) > 0 {
			brief.RequiredNext = appendUniqueBriefStrings(brief.RequiredNext,
				"prefer a command from verification_plan before any inspection",
			)
		}
	}
	if isEvidenceFirstMutationFamily(route.Family) && len(brief.TargetRefs) > 0 {
		brief.RequiredNext = appendUniqueBriefStrings(brief.RequiredNext,
			"gather source evidence before large reads of named target refs",
		)
		brief.InvalidActions = appendUniqueBriefStrings(brief.InvalidActions,
			"large read_file on named target refs before source evidence",
		)
	}
	if m.plannerOwnedVerification && isImplementationLikeFamily(route.Family) {
		brief.Constraints = appendUniqueBriefStrings(brief.Constraints,
			"planner_owned_verification",
		)
		brief.InvalidActions = appendUniqueBriefStrings(brief.InvalidActions,
			"build_or_test_shell",
		)
		brief.RequiredNext = appendUniqueBriefStrings(brief.RequiredNext,
			"after source edits run source_sanity; for non-source edits run only cheap non-build sanity checks or report planner-owned verification status",
		)
	}
	if isImplementationLikeFamily(route.Family) && promptContainsExactEditAnchor(prompt) {
		brief.Constraints = appendUniqueBriefStrings(brief.Constraints, "exact_anchor_implementation")
		brief.RequiredEvidence = appendUniqueBriefStrings(brief.RequiredEvidence, "exact_edit_anchor")
		brief.SearchPolicy = appendUniqueBriefStrings(brief.SearchPolicy, "attempt_exact_anchor_edit_before_broad_discovery")
		brief.InvalidActions = appendUniqueBriefStrings(brief.InvalidActions, "broad_read_before_first_edit_attempt")
	}
	return brief
}

// RequiredEditPathsFromPrompt is the exported view of the brief-
// parsing logic the runner uses to populate brief.RequiredEditPaths.
// External callers (today: cmd/idiom's `idiom lane postflight`) use
// this so the postflight allowlist check matches exactly what the
// runner saw — diverging implementations would produce different
// allowlists for the same brief, which is a footgun.
func RequiredEditPathsFromPrompt(prompt string) []string {
	return requiredEditPathsFromPrompt(prompt)
}

func promptHasRequiredEditPayload(prompt string) bool {
	if promptContainsExactEditAnchor(prompt) {
		return true
	}
	for _, marker := range promptContextPacketMarkers(prompt) {
		if marker == "prompt_context:required_edits" {
			return true
		}
	}
	return false
}

// AllowedEditPathsFromPrompt returns explicit candidate write-scope paths from
// prompts. Unlike RequiredEditPathsFromPrompt, these paths are not completion
// coverage obligations.
func AllowedEditPathsFromPrompt(prompt string) []string {
	return allowedEditPathsFromPrompt(prompt)
}

func requiredEditPathsFromPrompt(prompt string) []string {
	// Scope to the brief body when the lane wrapper has wrapped it
	// in IDIOM:BRIEF-BODY markers; otherwise scan the whole prompt
	// (back-compat for chat / direct API). pathparse.ScopeToBriefBody
	// is the authoritative helper.
	prompt = pathparse.ScopeToBriefBody(prompt)
	paths := requiredEditPathsUnderPromptHeadings(prompt, requiredEditPathHeadings(), true)
	paths = appendUniqueBriefStrings(paths, pathparse.FilterWorkspaceFilePaths(structuredRequiredEditPathsFromPrompt(prompt))...)
	paths = appendUniqueBriefStrings(paths, inlineRequiredEditPathsFromPrompt(prompt)...)
	return paths
}

func allowedEditPathsFromPrompt(prompt string) []string {
	prompt = pathparse.ScopeToBriefBody(prompt)
	paths := requiredEditPathsUnderPromptHeadings(prompt, allowedEditPathHeadings(), false)
	paths = appendUniqueBriefStrings(paths, structuredAllowedEditPathsFromPrompt(prompt)...)
	paths = appendUniqueBriefStrings(paths, inlineAllowedEditPathsFromPrompt(prompt)...)
	return paths
}

func inlineAllowedEditPathsFromPrompt(prompt string) []string {
	text := pathparse.ScopeToBriefBody(prompt)
	words := plannerWordText(text)
	hasInlineScope := false
	for _, phrase := range []string{
		" only edit ",
		" only modify ",
		" only change ",
		" edit only ",
		" modify only ",
		" change only ",
		" limit edits to ",
		" restrict edits to ",
		" limit changes to ",
		" restrict changes to ",
	} {
		if strings.Contains(words, phrase) {
			hasInlineScope = true
			break
		}
	}
	if !hasInlineScope {
		return nil
	}
	return pathparse.MentionedWorkspacePaths(text)
}

func inlineRequiredEditPathsFromPrompt(prompt string) []string {
	text := pathparse.ScopeToBriefBody(prompt)
	var paths []string
	for _, rawLine := range strings.Split(text, "\n") {
		line := strings.TrimSpace(rawLine)
		lower := strings.ToLower(line)
		for _, prefix := range []string{
			"required edit paths:",
			"required files:",
			"files to modify:",
			"files to edit:",
			"editable files:",
		} {
			idx := strings.Index(lower, prefix)
			if idx < 0 {
				continue
			}
			value := strings.TrimSpace(line[idx+len(prefix):])
			if value == "" {
				continue
			}
			paths = appendUniqueBriefStrings(paths, pathparse.MentionedWorkspaceFilesInInlineList(value)...)
		}
	}
	return paths
}

func requiredEditPathHeadings() []string {
	return []string{
		"files to modify",
		"files-to-modify",
		"files to edit",
		"edit files",
		"editable files",
		"required edit paths",
		"required files",
		"required paths",
	}
}

func allowedEditPathHeadings() []string {
	return []string{
		"candidate edit scope",
		"candidate edit scope from target references",
		"candidate paths",
		"candidate files",
		"files you may edit",
		"files you may modify",
		"files you can edit",
		"write scope",
		"allowed files",
		"allowed edit files",
		"allowed edit paths",
		"allowed source files",
		"edit allowlist",
		"file allowlist",
		"files allowlist",
		"source allowlist",
	}
}

func requiredEditPathsUnderPromptHeadings(prompt string, headings []string, fileOnly bool) []string {
	headingSet := promptHeadingSet(headings)
	var paths []string
	active := false
	sawPath := false
	for _, rawLine := range strings.Split(prompt, "\n") {
		line := strings.TrimSpace(rawLine)
		if _, ok := headingSet[normalizePromptSectionHeading(line)]; ok {
			active = true
			sawPath = false
			continue
		}
		if !active {
			continue
		}
		if line == "" {
			if sawPath {
				active = false
			}
			continue
		}
		if strings.HasPrefix(line, "|") && strings.Count(line, "|") >= 2 {
			linePaths := pathparse.MentionedWorkspacePathsInMarkdownTableFirstCell(line)
			if fileOnly {
				linePaths = pathparse.FilterWorkspaceFilePaths(linePaths)
			}
			if len(linePaths) > 0 {
				sawPath = true
				paths = appendUniqueBriefStrings(paths, linePaths...)
			}
			continue
		}
		linePaths := promptPacketWorkspacePaths(line)
		if fileOnly {
			linePaths = pathparse.FilterWorkspaceFilePaths(linePaths)
		}
		if len(linePaths) > 0 {
			sawPath = true
			paths = appendUniqueBriefStrings(paths, linePaths...)
			continue
		}
		if looksLikePromptSectionBoundary(line) {
			if !sawPath && looksLikeRequiredPathPrelude(line) {
				continue
			}
			active = false
			continue
		}
	}
	return paths
}

func structuredRequiredEditPathsFromPrompt(prompt string) []string {
	return structuredEditPathsFromPrompt(prompt, []string{
		"files_to_modify",
		"files_to_edit",
		"required_edit_paths",
		"editable_files",
	})
}

func structuredAllowedEditPathsFromPrompt(prompt string) []string {
	return structuredEditPathsFromPrompt(prompt, []string{
		"allowed_edit_paths",
		"allowed_files",
		"write_scope",
	})
}

func structuredEditPathsFromPrompt(prompt string, keys []string) []string {
	prompt = pathparse.ScopeToBriefBody(prompt)
	var paths []string
	active := false
	activeArray := false
	for _, rawLine := range strings.Split(prompt, "\n") {
		line := strings.TrimSpace(rawLine)
		started := false
		if !active && structuredEditPathKeyLine(line, keys) {
			active = true
			started = true
			activeArray = structuredEditPathLineStartsArray(line)
		}
		if !active {
			continue
		}
		scanLine := line
		if started {
			scanLine = structuredEditPathInlineValue(line)
		}
		linePaths := promptPacketWorkspacePaths(scanLine)
		if len(linePaths) > 0 {
			paths = appendUniqueBriefStrings(paths, linePaths...)
		}
		if started && !activeArray {
			if len(linePaths) > 0 || strings.Contains(line, "]") || strings.Contains(line, "}") {
				active = false
				activeArray = false
			}
			continue
		}
		if structuredEditPathBlockEnded(line, activeArray) {
			active = false
			activeArray = false
		}
	}
	return paths
}

func structuredEditPathKeyLine(line string, keys []string) bool {
	normalized := normalizeStructuredEditPathKeyPrefix(line)
	for _, key := range keys {
		switch {
		case normalized == key:
			return true
		case strings.HasPrefix(normalized, key+":") || strings.HasPrefix(normalized, key+"="):
			return true
		case strings.HasPrefix(normalized, `"`+key+`":`) || strings.HasPrefix(normalized, `"`+key+`"=`):
			return true
		case strings.HasPrefix(normalized, `'`+key+`':`) || strings.HasPrefix(normalized, `'`+key+`'=`):
			return true
		}
	}
	return false
}

func normalizeStructuredEditPathKeyPrefix(line string) string {
	normalized := strings.ToLower(strings.NewReplacer("-", "_", " ", "_").Replace(strings.TrimSpace(line)))
	normalized = strings.TrimLeft(normalized, "{[, ")
	if strings.HasPrefix(normalized, "-_") {
		normalized = strings.TrimSpace(strings.TrimPrefix(normalized, "-_"))
	}
	return normalized
}

func structuredEditPathLineStartsArray(line string) bool {
	value := structuredEditPathInlineRawValue(line)
	value = strings.TrimSpace(value)
	return strings.HasPrefix(value, "[") && structuredInlineClosingIndex(value, '[', ']') < 0
}

func structuredEditPathInlineValue(line string) string {
	return structuredEditPathValueSegment(structuredEditPathInlineRawValue(line))
}

func structuredEditPathInlineRawValue(line string) string {
	trimmed := strings.TrimSpace(line)
	trimmed = strings.TrimLeft(trimmed, "{[, ")
	if strings.HasPrefix(trimmed, "- ") {
		trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))
	}
	colon := strings.Index(trimmed, ":")
	equals := strings.Index(trimmed, "=")
	switch {
	case colon < 0 && equals < 0:
		return ""
	case colon < 0:
		return trimmed[equals+1:]
	case equals < 0:
		return trimmed[colon+1:]
	case colon < equals:
		return trimmed[colon+1:]
	default:
		return trimmed[equals+1:]
	}
}

func structuredEditPathValueSegment(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	switch value[0] {
	case '[':
		if end := structuredInlineClosingIndex(value, '[', ']'); end >= 0 {
			return strings.TrimSpace(value[:end+1])
		}
		return value
	case '{':
		if end := structuredInlineClosingIndex(value, '{', '}'); end >= 0 {
			return strings.TrimSpace(value[:end+1])
		}
		return value
	case '"', '\'':
		if end := structuredInlineQuoteClosingIndex(value, value[0]); end >= 0 {
			return strings.TrimSpace(value[:end+1])
		}
		return value
	default:
		end := len(value)
		if comma := strings.Index(value, ","); comma >= 0 && comma < end {
			end = comma
		}
		if comment := strings.Index(value, " #"); comment >= 0 && comment < end {
			end = comment
		}
		return strings.TrimSpace(value[:end])
	}
}

func structuredInlineClosingIndex(value string, open byte, close byte) int {
	depth := 0
	var quote byte
	escaped := false
	for i := 0; i < len(value); i++ {
		ch := value[i]
		if quote != 0 {
			if escaped {
				escaped = false
				continue
			}
			if ch == '\\' {
				escaped = true
				continue
			}
			if ch == quote {
				quote = 0
			}
			continue
		}
		if ch == '"' || ch == '\'' {
			quote = ch
			continue
		}
		if ch == open {
			depth++
			continue
		}
		if ch != close {
			continue
		}
		depth--
		if depth == 0 {
			return i
		}
	}
	return -1
}

func structuredInlineQuoteClosingIndex(value string, quote byte) int {
	escaped := false
	for i := 1; i < len(value); i++ {
		ch := value[i]
		if escaped {
			escaped = false
			continue
		}
		if ch == '\\' {
			escaped = true
			continue
		}
		if ch == quote {
			return i
		}
	}
	return -1
}

func structuredEditPathBlockEnded(line string, activeArray bool) bool {
	trimmed := strings.TrimSpace(strings.TrimRight(line, ","))
	if activeArray {
		return trimmed == "" || strings.Contains(trimmed, "]") || looksLikePromptSectionBoundary(trimmed)
	}
	return trimmed == "" ||
		strings.Contains(trimmed, "]") ||
		strings.Contains(trimmed, "}") ||
		looksLikePromptSectionBoundary(trimmed)
}

func looksLikePromptSectionBoundary(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}
	if strings.HasPrefix(trimmed, "#") {
		return true
	}
	if strings.HasPrefix(trimmed, "**") && strings.Contains(strings.TrimPrefix(trimmed, "**"), "**") {
		return true
	}
	return strings.HasSuffix(trimmed, ":")
}

func looksLikeMarkdownPromptSectionBoundary(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.HasPrefix(trimmed, "#") ||
		(strings.HasPrefix(trimmed, "**") && strings.Contains(strings.TrimPrefix(trimmed, "**"), "**"))
}

func looksLikeRequiredPathPrelude(line string) bool {
	if !looksLikePromptSectionBoundary(line) || looksLikeMarkdownPromptSectionBoundary(line) {
		return false
	}
	normalized := normalizePromptSectionHeading(line)
	return strings.Contains(normalized, "allowlist") ||
		strings.Contains(normalized, "scope") ||
		strings.Contains(normalized, "files") ||
		strings.Contains(normalized, "paths") ||
		strings.Contains(normalized, "table")
}

func promptHeadingSet(headings []string) map[string]struct{} {
	headingSet := make(map[string]struct{}, len(headings))
	for _, heading := range headings {
		if normalized := normalizePromptSectionHeading(heading); normalized != "" {
			headingSet[normalized] = struct{}{}
		}
	}
	return headingSet
}

func workspacePathsUnderPromptHeadings(prompt string, headings []string) []string {
	headingSet := promptHeadingSet(headings)
	var paths []string
	active := false
	sawPath := false
	for _, rawLine := range strings.Split(prompt, "\n") {
		line := strings.TrimSpace(rawLine)
		normalized := normalizePromptSectionHeading(line)
		if promptHeadingInSet(normalized, headingSet) {
			active = true
			sawPath = false
			continue
		}
		if !active {
			continue
		}
		if line == "" {
			if sawPath {
				active = false
			}
			continue
		}
		linePaths := promptPacketWorkspacePaths(line)
		if len(linePaths) > 0 {
			sawPath = true
			paths = appendUniqueBriefStrings(paths, linePaths...)
			continue
		}
		if looksLikePromptSectionBoundary(line) {
			if !sawPath && looksLikeRequiredPathPrelude(line) {
				continue
			}
			active = false
			continue
		}
	}
	return paths
}

func promptPacketWorkspacePaths(text string) []string {
	paths := appendUniqueBriefStrings(nil, pathparse.MentionedWorkspacePaths(text)...)
	for _, fragment := range pathparse.QuotedFragments(text) {
		path, ok := pathparse.NormalizeWorkspacePathToken(fragment)
		if !ok {
			continue
		}
		paths = appendUniqueBriefStrings(paths, path)
	}
	return paths
}

func promptContextEvidencePaths(prompt string) []string {
	headings := []string{
		"verified current state",
		"current state",
		"current-state anchors",
		"source evidence",
		"context packet",
		"context pack",
		"context view",
		"provided context",
		"implementation context",
		"discovery evidence",
		"evidence",
		"evidence paths",
		"primary evidence paths",
		"read-only evidence paths",
		"required reads",
		"required read",
		"input files",
		"relevant files",
		"candidate scope",
	}
	paths := workspacePathsUnderPromptHeadings(prompt, headings)
	for _, heading := range headings {
		section := markdownSectionByHeading(prompt, heading)
		if strings.TrimSpace(section) == "" {
			continue
		}
		paths = appendUniqueBriefStrings(paths, promptPacketWorkspacePaths(section)...)
	}
	return paths
}

func promptHasSectionHeading(prompt string, heading string) bool {
	if strings.TrimSpace(markdownSectionByHeading(prompt, heading)) != "" {
		return true
	}
	want := normalizePromptSectionHeading(heading)
	if want == "" {
		return false
	}
	inFence := false
	fence := ""
	lines := strings.Split(prompt, "\n")
	for i, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if marker, _, ok := markdownFenceOpen(line); ok {
			if !inFence {
				inFence = true
				fence = marker
			} else if strings.HasPrefix(line, fence) {
				inFence = false
				fence = ""
			}
			continue
		}
		if inFence {
			continue
		}
		if normalizePromptSectionHeading(line) == want {
			if loosePromptSectionHasContent(lines, i+1) {
				return true
			}
			continue
		}
	}
	return false
}

func loosePromptSectionHasContent(lines []string, start int) bool {
	if start < 0 || start >= len(lines) {
		return false
	}
	for _, rawLine := range lines[start:] {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		if len(promptPacketWorkspacePaths(line)) > 0 {
			return true
		}
		if looksLikePromptSectionBoundary(line) {
			return isPromptPacketNestedSection(line)
		}
		return true
	}
	return false
}

func isPromptPacketNestedSection(line string) bool {
	switch normalizePromptSectionHeading(line) {
	case "anchors",
		"candidate scope",
		"context",
		"context evidence",
		"current state",
		"evidence",
		"files",
		"source evidence",
		"symbols",
		"target refs",
		"tests",
		"verified current state":
		return true
	default:
		return false
	}
}

func promptContextPacketMarkers(prompt string) []string {
	type packetSection struct {
		heading string
		marker  string
	}
	sections := []packetSection{
		{heading: "verified current state", marker: "prompt_context:verified_current_state"},
		{heading: "current state", marker: "prompt_context:current_state"},
		{heading: "current-state anchors", marker: "prompt_context:current_state_anchors"},
		{heading: "source evidence", marker: "prompt_context:source_evidence"},
		{heading: "context packet", marker: "prompt_context:context_packet"},
		{heading: "context pack", marker: "prompt_context:context_packet"},
		{heading: "context view", marker: "prompt_context:context_view"},
		{heading: "provided context", marker: "prompt_context:provided_context"},
		{heading: "implementation context", marker: "prompt_context:implementation_context"},
		{heading: "discovery evidence", marker: "prompt_context:discovery_evidence"},
		{heading: "candidate scope", marker: "prompt_context:candidate_scope"},
		{heading: "required edits", marker: "prompt_context:required_edits"},
	}
	var markers []string
	for _, section := range sections {
		if !promptHasSectionHeading(prompt, section.heading) {
			continue
		}
		markers = appendUniqueBriefStrings(markers, section.marker)
	}
	if promptHasStructuredPacketMarker(prompt) {
		markers = appendUniqueBriefStrings(markers, "prompt_context:structured_packet")
	}
	return markers
}

func promptHasStructuredPacketMarker(prompt string) bool {
	lower := strings.ToLower(prompt)
	if util.ContainsAny(lower, `"context_packet"`, `"context_pack"`) ||
		(strings.Contains(lower, `"planning_packet`) && strings.Contains(lower, `"schema"`)) {
		return true
	}
	keys := []string{`"context_budget"`, `"candidate_scope"`, `"anchors"`, `"evidence"`, `"files_to_modify"`, `"required_edits"`}
	count := 0
	for _, key := range keys {
		if strings.Contains(lower, key) {
			count++
		}
	}
	return count >= 2
}

func requiredEditSnippetsFromPrompt(prompt string, requiredPaths []string) []workerruntime.RequiredEditSnippet {
	// Planner conventions vary: some briefs use "## Required Edits" with the
	// new code inline, others use "## Implementation Shape" or just
	// "## Implementation" for the same purpose. We scan candidates in order
	// and take the first one whose code fences survive the
	// skipRequiredEditCodeBlock filter, so an empty "required edits"
	// header doesn't shadow a populated "implementation shape" section.
	headings := []string{
		"required edits",
		"edits to apply",
		"edits",
		"implementation shape",
		"implementation",
	}
	var blocks []requiredEditBlock
	for _, heading := range headings {
		candidate := markdownSectionByHeading(prompt, heading)
		if strings.TrimSpace(candidate) == "" {
			continue
		}
		if found := requiredEditCodeBlocksWithPaths(candidate, requiredPaths); len(found) > 0 {
			blocks = found
			break
		}
	}
	if len(blocks) == 0 {
		return nil
	}
	path := ""
	if len(requiredPaths) == 1 {
		path = strings.TrimSpace(requiredPaths[0])
	}
	snippets := make([]workerruntime.RequiredEditSnippet, 0, len(blocks))
	for _, block := range blocks {
		content := strings.TrimSpace(block.Content)
		if content == "" {
			continue
		}
		blockPaths := block.Paths
		if len(blockPaths) == 0 && path != "" {
			blockPaths = []string{path}
		}
		if len(blockPaths) == 0 {
			blockPaths = []string{""}
		}
		for _, blockPath := range blockPaths {
			snippets = append(snippets, workerruntime.RequiredEditSnippet{
				ID:      fmt.Sprintf("required_edit_%d", len(snippets)+1),
				Path:    strings.TrimSpace(blockPath),
				Content: content,
			})
		}
	}
	if len(snippets) == 0 {
		return nil
	}
	return snippets
}

func markdownSectionByHeading(markdown string, headingName string) string {
	want := normalizeMarkdownHeadingText(headingName)
	lines := strings.Split(markdown, "\n")
	start := -1
	startLevel := 0
	inFence := false
	fence := ""
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if marker, _, ok := markdownFenceOpen(trimmed); ok {
			if !inFence {
				inFence = true
				fence = marker
			} else if strings.HasPrefix(trimmed, fence) {
				inFence = false
				fence = ""
			}
			continue
		}
		if inFence {
			continue
		}
		level, text, ok := markdownHeading(trimmed)
		if !ok || !markdownHeadingMatches(normalizeMarkdownHeadingText(text), want) {
			continue
		}
		start = i + 1
		startLevel = level
		break
	}
	if start < 0 {
		return ""
	}
	end := len(lines)
	inFence = false
	fence = ""
	for i := start; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if marker, _, ok := markdownFenceOpen(trimmed); ok {
			if !inFence {
				inFence = true
				fence = marker
			} else if strings.HasPrefix(trimmed, fence) {
				inFence = false
				fence = ""
			}
			continue
		}
		if inFence {
			continue
		}
		level, _, ok := markdownHeading(trimmed)
		if ok && level <= startLevel {
			end = i
			break
		}
	}
	return strings.Join(lines[start:end], "\n")
}

func markdownHeading(line string) (int, string, bool) {
	if !strings.HasPrefix(line, "#") {
		return 0, "", false
	}
	level := 0
	for level < len(line) && line[level] == '#' {
		level++
	}
	if level == 0 || level > 6 || level >= len(line) || line[level] != ' ' {
		return 0, "", false
	}
	text := strings.TrimSpace(line[level+1:])
	text = strings.TrimSpace(strings.TrimRight(text, "#"))
	if text == "" {
		return 0, "", false
	}
	return level, text, true
}

func normalizeMarkdownHeadingText(text string) string {
	text = strings.Trim(strings.ToLower(strings.TrimSpace(text)), ": ")
	return strings.Join(strings.Fields(text), " ")
}

func markdownHeadingMatches(candidate string, want string) bool {
	candidate = trimPromptHeadingQualifier(candidate)
	want = trimPromptHeadingQualifier(want)
	return candidate == want
}

func normalizePromptSectionHeading(text string) string {
	text = strings.TrimSpace(text)
	text = strings.Trim(text, "#*: `\"'")
	text = strings.TrimSuffix(text, ":")
	text = strings.ToLower(text)
	text = strings.NewReplacer("-", " ", "_", " ", "/", " ").Replace(text)
	return strings.Join(strings.Fields(text), " ")
}

func promptHeadingInSet(candidate string, headingSet map[string]struct{}) bool {
	if _, ok := headingSet[candidate]; ok {
		return true
	}
	if trimmed := trimPromptHeadingQualifier(candidate); trimmed != candidate {
		_, ok := headingSet[trimmed]
		return ok
	}
	return false
}

func trimPromptHeadingQualifier(heading string) string {
	heading = strings.TrimSpace(heading)
	for _, marker := range []string{" (", " [", " - ", " -- "} {
		if idx := strings.Index(heading, marker); idx > 0 {
			return strings.TrimSpace(heading[:idx])
		}
	}
	return heading
}

func requiredEditCodeBlocks(section string) []string {
	blocks := requiredEditCodeBlocksWithPaths(section, nil)
	out := make([]string, 0, len(blocks))
	for _, block := range blocks {
		out = append(out, block.Content)
	}
	return out
}

type requiredEditBlock struct {
	Paths   []string
	Content string
}

func requiredEditCodeBlocksWithPaths(section string, requiredPaths []string) []requiredEditBlock {
	var blocks []requiredEditBlock
	var current []string
	previousNonEmpty := ""
	var currentPaths []string
	var blockPaths []string
	fence := ""
	skipCurrent := false
	for _, rawLine := range strings.Split(section, "\n") {
		trimmed := strings.TrimSpace(rawLine)
		if fence == "" {
			if marker, info, ok := markdownFenceOpen(trimmed); ok {
				fence = marker
				current = nil
				blockPaths = append([]string(nil), currentPaths...)
				skipCurrent = skipRequiredEditCodeBlock(previousNonEmpty, info)
				continue
			}
			if trimmed != "" {
				if paths := requiredEditPathsMentionedInLine(trimmed, requiredPaths); len(paths) > 0 {
					currentPaths = paths
				}
				previousNonEmpty = trimmed
			}
			continue
		}
		if strings.HasPrefix(trimmed, fence) {
			if !skipCurrent {
				if block := strings.TrimSpace(strings.Join(current, "\n")); block != "" {
					blocks = append(blocks, requiredEditBlock{Paths: append([]string(nil), blockPaths...), Content: block})
				}
			}
			fence = ""
			current = nil
			blockPaths = nil
			skipCurrent = false
			previousNonEmpty = ""
			continue
		}
		current = append(current, rawLine)
	}
	return blocks
}

func requiredEditPathsMentionedInLine(line string, requiredPaths []string) []string {
	if len(requiredPaths) == 0 {
		return nil
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}
	matched := make(map[string]struct{}, len(requiredPaths))
	for _, path := range requiredPaths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		if strings.Contains(line, path) || strings.Contains(line, "`"+path+"`") {
			matched[path] = struct{}{}
		}
	}
	baseCounts := make(map[string]int, len(requiredPaths))
	for _, path := range requiredPaths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		baseCounts[filepath.Base(path)]++
	}
	for _, path := range requiredPaths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		base := filepath.Base(path)
		if base == "." || base == "/" || base == "" || baseCounts[base] != 1 {
			continue
		}
		if strings.Contains(line, "`"+base+"`") {
			matched[path] = struct{}{}
		}
	}
	if len(matched) == 0 {
		return nil
	}
	out := make([]string, 0, len(matched))
	for _, path := range requiredPaths {
		path = strings.TrimSpace(path)
		if _, ok := matched[path]; ok {
			out = append(out, path)
		}
	}
	return out
}

func markdownFenceOpen(line string) (marker string, info string, ok bool) {
	switch {
	case strings.HasPrefix(line, "```"):
		return "```", strings.TrimSpace(strings.TrimPrefix(line, "```")), true
	case strings.HasPrefix(line, "~~~"):
		return "~~~", strings.TrimSpace(strings.TrimPrefix(line, "~~~")), true
	default:
		return "", "", false
	}
}

func skipRequiredEditCodeBlock(label string, info string) bool {
	lowerInfo := strings.ToLower(strings.TrimSpace(info))
	if strings.Contains(lowerInfo, "diff") || strings.Contains(lowerInfo, "patch") {
		return true
	}
	lowerLabel := strings.Trim(strings.ToLower(strings.TrimSpace(label)), ":. ")
	return strings.HasPrefix(lowerLabel, "old") ||
		strings.HasPrefix(lowerLabel, "before") ||
		strings.HasPrefix(lowerLabel, "existing") ||
		strings.HasPrefix(lowerLabel, "current") ||
		strings.HasPrefix(lowerLabel, "replace this")
}

func appendRequiredEditSnippets(dst []workerruntime.RequiredEditSnippet, values ...workerruntime.RequiredEditSnippet) []workerruntime.RequiredEditSnippet {
	seen := make(map[string]struct{}, len(dst)+len(values))
	for _, value := range dst {
		content := strings.TrimSpace(value.Content)
		if content == "" {
			continue
		}
		seen[strings.TrimSpace(value.Path)+"\x00"+content] = struct{}{}
	}
	for _, value := range values {
		value.ID = strings.TrimSpace(value.ID)
		value.Path = strings.TrimSpace(value.Path)
		value.Content = strings.TrimSpace(value.Content)
		if value.Content == "" {
			continue
		}
		key := value.Path + "\x00" + value.Content
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		dst = append(dst, value)
	}
	return dst
}

func promptContainsExactEditAnchor(prompt string) bool {
	lower := strings.ToLower(prompt)
	if strings.Contains(prompt, "*** Begin Patch") ||
		strings.Contains(prompt, "```diff") ||
		(strings.Contains(prompt, "\n--- ") && strings.Contains(prompt, "\n+++ ") && strings.Contains(prompt, "\n@@")) {
		return true
	}
	return promptHasStructuredExactEditPair(lower)
}

func promptHasStructuredExactEditPair(lowerPrompt string) bool {
	type pair struct {
		left  string
		right string
	}
	pairs := []pair{
		{left: "old_text", right: "new_text"},
		{left: "old text", right: "new text"},
		{left: "old snippet", right: "new snippet"},
		{left: "before", right: "after"},
		{left: "replace", right: "with"},
		{left: "exact old", right: "exact new"},
	}
	labels := make(map[string]struct{}, len(pairs)*2)
	for _, line := range strings.Split(lowerPrompt, "\n") {
		label, ok := exactEditAnchorLineLabel(line)
		if !ok {
			continue
		}
		labels[label] = struct{}{}
	}
	for _, pair := range pairs {
		if _, ok := labels[pair.left]; !ok {
			continue
		}
		if _, ok := labels[pair.right]; ok {
			return true
		}
	}
	return false
}

func exactEditAnchorLineLabel(line string) (string, bool) {
	line = strings.TrimSpace(line)
	line = strings.TrimLeft(line, "-*")
	line = strings.TrimSpace(line)
	if line == "" {
		return "", false
	}
	separator := strings.Index(line, ":")
	if equals := strings.Index(line, "="); equals >= 0 && (separator < 0 || equals < separator) {
		separator = equals
	}
	if separator < 0 {
		return "", false
	}
	label := strings.TrimSpace(line[:separator])
	label = strings.Trim(label, "`'\"*_ {}")
	label = strings.NewReplacer("-", " ", "_", " ").Replace(label)
	label = strings.Join(strings.Fields(label), " ")
	switch label {
	case "old text", "new text", "old snippet", "new snippet", "before", "after", "replace", "with", "exact old", "exact new":
		return label, true
	case "old":
		return "old text", true
	case "new":
		return "new text", true
	default:
		return "", false
	}
}

func defaultVerificationPlan(root string) []string {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil
	}
	type manifestPlan struct {
		path string
		cmd  string
	}
	candidates := []manifestPlan{
		{path: "go.mod", cmd: "go test ./..."},
		{path: "go.work", cmd: "go test ./..."},
		{path: "Cargo.toml", cmd: "cargo test"},
		{path: "package.json", cmd: "npm test"},
		{path: "pyproject.toml", cmd: "pytest"},
		{path: "pytest.ini", cmd: "pytest"},
		{path: "pom.xml", cmd: "mvn test"},
		{path: "build.gradle", cmd: "gradle test"},
		{path: "build.gradle.kts", cmd: "gradle test"},
	}
	var plan []string
	for _, candidate := range candidates {
		if _, err := os.Stat(filepath.Join(root, candidate.path)); err == nil {
			plan = append(plan, candidate.cmd)
		}
	}
	return appendUniqueBriefStrings(nil, plan...)
}

func appendUniqueBriefStrings(dst []string, values ...string) []string {
	for _, v := range values {
		dst = util.AppendUnique(dst, v)
	}
	return dst
}

func isEvidenceFirstMutationFamily(family string) bool {
	switch strings.TrimSpace(family) {
	case "explore_then_implement", "investigate_then_implement", "review_then_implement", "architecture_then_implement":
		return true
	default:
		return false
	}
}

func tuneChatOptionsForPlan(opts llama.ChatOptions, plan promptExecutionPlan) llama.ChatOptions {
	profile := strings.TrimSpace(plan.runtimeBrief.PermissionProfile)
	contract := strings.TrimSpace(plan.runtimeBrief.Contract)
	taskFamily := strings.TrimSpace(plan.runtimeBrief.TaskFamily)
	if taskFamily == agent.PlanThenExecutePlannerTaskFamily {
		opts.Temperature = 0
		opts.TopP = 1
		if !opts.ModelThinkingFloor {
			thinkingOff := false
			opts.EnableThinking = &thinkingOff
			opts.PreserveThinking = false
			opts.ChatTemplateKwargs = true
		}
	}
	readOnly := profile == "conversation_only" || profile == "read_only_workspace" || profile == "read_only_verification_shell"
	if readOnly && !opts.ModelThinkingFloor {
		thinkingOff := false
		opts.EnableThinking = &thinkingOff
		opts.PreserveThinking = false
		opts.ChatTemplateKwargs = true
	}
	// MLX 0.31.3+ accepts per-request chat_template_kwargs, so this
	// now disables Qwen thinking for read-only routes without requiring
	// a separate nothink server. Older MLX builds may ignore the field;
	// launcher-level --chat-template-args remains the fallback there.
	switch contract {
	case "direct_answer":
		if opts.ModelThinkingFloor {
			opts.MaxTokens = clampPositiveMin(opts.MaxTokens, 4096)
		} else {
			opts.MaxTokens = clampPositiveMin(opts.MaxTokens, 1024)
		}
	case "grounded_explanation", "locate_targets":
		opts.MaxTokens = clampPositiveMin(opts.MaxTokens, 1536)
	case "review_findings", "architecture_assessment":
		opts.MaxTokens = clampPositiveMin(opts.MaxTokens, 2048)
	}
	return opts
}

func applyMaxTokensOverride(opts llama.ChatOptions, maxTokens int) llama.ChatOptions {
	if maxTokens > 0 {
		opts.MaxTokens = maxTokens
	}
	return opts
}

func clampPositiveMin(current, limit int) int {
	if limit <= 0 {
		return current
	}
	if current <= 0 || current > limit {
		return limit
	}
	return current
}
