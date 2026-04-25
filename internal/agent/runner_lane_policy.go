package agent

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"idiom/internal/frontier"
	"idiom/internal/workspace"
)

type runnerLanePreflightInput struct {
	Request               workspace.ToolRequest
	Raw                   string
	Results               []workspace.ToolResult
	VerificationAttempted bool
}

type runnerLanePreflightDecision struct {
	Request          workspace.ToolRequest
	Raw              string
	RejectSummary    string
	RejectError      string
	RejectStopReason string
	ForceAnswer      *runnerForcedAnswer
}

type runnerForcedAnswer struct {
	Reason string
	Prompt string
}

func (r *Runner) applyLanePreflight(input runnerLanePreflightInput) runnerLanePreflightDecision {
	decision := runnerLanePreflightDecision{
		Request: input.Request,
		Raw:     input.Raw,
	}

	if strings.EqualFold(strings.TrimSpace(r.RuntimeBrief.PermissionProfile), "conversation_only") {
		decision.RejectSummary = "Tool request rejected by conversation-only contract"
		decision.RejectError = "this turn is conversation-only. Do not call any tools; answer directly from the prompt and prior visible context."
		decision.RejectStopReason = "conversation_only_tool_rejected"
		return decision
	}

	if redirected, ok := r.mainReadOnlyDelegationBootstrapRequest(input.Results, decision.Request); ok {
		decision.Request = redirected
		decision.Raw = marshalToolRequestRaw(redirected)
	}
	if redirected, ok := r.delegatedReadOnlyBootstrapQueryRequest(input.Results, decision.Request); ok {
		decision.Request = redirected
		decision.Raw = marshalToolRequestRaw(redirected)
	}
	if redirected, ok := r.delegatedReadOnlyBootstrapRequest(input.Results, decision.Request); ok {
		decision.Request = redirected
		decision.Raw = marshalToolRequestRaw(redirected)
	}
	if redirected, ok := r.delegatedReadOnlySecondaryEvidenceRedirect(input.Results, decision.Request); ok {
		decision.Request = redirected
		decision.Raw = marshalToolRequestRaw(redirected)
	}
	if redirected, ok := r.delegatedReadOnlyNamedFileReadRedirect(input.Results, decision.Request); ok {
		decision.Request = redirected
		decision.Raw = marshalToolRequestRaw(redirected)
	}
	if redirected, ok := r.delegatedReadOnlySearchHitReadRedirect(input.Results, decision.Request); ok {
		decision.Request = redirected
		decision.Raw = marshalToolRequestRaw(redirected)
	}
	if redirected, ok := r.delegatedReadOnlyIntegrationPivotRedirect(input.Results, decision.Request); ok {
		decision.Request = redirected
		decision.Raw = marshalToolRequestRaw(redirected)
	}

	if strings.EqualFold(strings.TrimSpace(r.RuntimeBrief.PermissionProfile), "read_only_verification_shell") &&
		!isVerificationRequest(decision.Request) {
		if !input.VerificationAttempted {
			if redirected, ok := r.verificationBootstrapRequest(); ok {
				decision.Request = redirected
				decision.Raw = marshalToolRequestRaw(redirected)
			} else {
				decision.RejectSummary = "Verification-first contract rejected non-verification action"
				decision.RejectError = "this turn is verification-only. Run the requested verification command first with shell (for example go test ./..., go build ./..., npm test, or the seeded failing command), then inspect files only if the output needs explanation."
				decision.RejectStopReason = "verification_first_contract_exhausted"
				return decision
			}
		} else {
			decision.RejectSummary = "Verification-only contract rejected post-verification drift"
			decision.RejectError = "this turn is verification-only and a verification command has already run. Report the pass/fail result directly unless another verification command is strictly necessary."
			decision.RejectStopReason = "verification_only_post_check_drift"
			return decision
		}
	}

	if summary, errorText, reject := prematureTargetReadRejection(decision.Request, input.Results, r.RuntimeBrief); reject {
		decision.RejectSummary = summary
		decision.RejectError = errorText
		decision.RejectStopReason = "premature_target_read_exhausted"
		return decision
	}
	if summary, errorText, reject := evidenceFirstTargetReadBudgetRejection(decision.Request, input.Results, r.RuntimeBrief); reject {
		decision.RejectSummary = summary
		decision.RejectError = errorText
		decision.RejectStopReason = "target_read_budget_exhausted"
		return decision
	}
	if redirected, ok := evidenceFirstTargetReadRedirect(r.RuntimeBrief, input.Results, decision.Request); ok {
		decision.Request = redirected
		decision.Raw = marshalToolRequestRaw(redirected)
	}
	if summary, errorText, reject := evidenceFirstTargetWritePhaseRejection(decision.Request, input.Results, r.RuntimeBrief); reject {
		decision.RejectSummary = summary
		decision.RejectError = errorText
		decision.RejectStopReason = "target_write_phase_enforced"
		return decision
	}

	if redirected, ok := r.debugLogPreflightRequest(input.Results, decision.Request); ok {
		decision.Request = redirected
		decision.Raw = marshalToolRequestRaw(redirected)
	}
	if summary, errorText, reject := debugLogPhaseScopeRejection(decision.Request, input.Results, r.RuntimeBrief); reject {
		if summary == "Blind log reread rejected" {
			decision.RejectSummary = summary
			decision.RejectError = errorText
			decision.ForceAnswer = &runnerForcedAnswer{
				Reason: "debug_logs_tail_answer_forced",
				Prompt: "You already have the most recent log tail. Do not request any more tools. Answer directly from that log evidence: identify the weird decisions visible in the logs, say whether the harness can be made smarter, and name the most relevant generic improvement or the remaining uncertainty.",
			}
			return decision
		}
		decision.RejectSummary = summary
		decision.RejectError = errorText
		decision.RejectStopReason = "debug_logs_phase_scope_enforced"
		return decision
	}
	if redirected, ok := debugLogFollowupRequest(r.RuntimeBrief, input.Results, decision.Request); ok {
		decision.Request = redirected
		decision.Raw = marshalToolRequestRaw(redirected)
	}
	if debugLogEvidenceSatisfied(r.RuntimeBrief, input.Results) && isReadClassTool(decision.Request) {
		decision.RejectSummary = "Debug-log contract rejected extra exploration after evidence was satisfied"
		decision.RejectError = "you already have a concrete failure hit from the selected log and a nearby read window. Answer from that evidence now unless you can name a specific missing datum."
		decision.RejectStopReason = "debug_logs_post_evidence_drift"
		return decision
	}

	return decision
}

func (r *Runner) mainReadOnlyDelegationBootstrapRequest(results []workspace.ToolResult, req workspace.ToolRequest) (workspace.ToolRequest, bool) {
	if r == nil || strings.HasPrefix(r.agentName(), "child:") || strings.HasPrefix(r.agentName(), "child_write:") {
		return workspace.ToolRequest{}, false
	}
	if len(results) > 0 || !prefersEarlyReadOnlyDelegation(r.RuntimeBrief) || strings.TrimSpace(req.Tool) == "task" || !isReadClassTool(req) {
		return workspace.ToolRequest{}, false
	}
	if r.Workspace == nil || !containsRunnerTool(r.advertisedAllowedTools(r.Workspace.AllowedTools()), "task") {
		return workspace.ToolRequest{}, false
	}
	prompt := delegatedReadOnlyTaskPrompt(r.RuntimeBrief.Objective, r.Workspace.Root())
	if strings.TrimSpace(prompt) == "" {
		return workspace.ToolRequest{}, false
	}
	return workspace.ToolRequest{
		Tool:   "task",
		Agent:  "explorer",
		Mode:   "read_only",
		Prompt: prompt,
	}, true
}

func (r *Runner) delegatedReadOnlyBootstrapRequest(results []workspace.ToolResult, req workspace.ToolRequest) (workspace.ToolRequest, bool) {
	if r == nil || !strings.HasPrefix(r.agentName(), "child:") || strings.TrimSpace(r.RuntimeBrief.TaskFamily) != "delegated_read_only" || len(results) > 0 {
		return workspace.ToolRequest{}, false
	}
	switch strings.TrimSpace(req.Tool) {
	case "list_files":
		return delegatedReadOnlyBootstrapSearchRequest(r.RuntimeBrief.Objective)
	case "search_files":
		pattern := strings.TrimSpace(req.Pattern)
		if pattern == "" || pattern == "*" || pattern == "*.go" || strings.HasPrefix(pattern, "*") {
			return delegatedReadOnlyBootstrapSearchRequest(r.RuntimeBrief.Objective)
		}
	case "search_content":
		path := strings.TrimSpace(req.Path)
		if path == "" || path == "." || !frontier.HasWorkspaceFileExtension(path) {
			return delegatedReadOnlyBootstrapSearchRequest(r.RuntimeBrief.Objective)
		}
	}
	return workspace.ToolRequest{}, false
}

func (r *Runner) delegatedReadOnlyBootstrapQueryRequest(results []workspace.ToolResult, req workspace.ToolRequest) (workspace.ToolRequest, bool) {
	if r == nil || !strings.HasPrefix(r.agentName(), "child:") || strings.TrimSpace(r.RuntimeBrief.TaskFamily) != "delegated_read_only" || len(results) > 0 {
		return workspace.ToolRequest{}, false
	}
	if strings.TrimSpace(req.Tool) == "discovery_query" || !isReadClassTool(req) {
		return workspace.ToolRequest{}, false
	}
	if !containsRunnerTool(r.RuntimeBrief.AllowedTools, "discovery_query") {
		return workspace.ToolRequest{}, false
	}
	for _, path := range frontier.PromptMentionedWorkspacePaths(r.RuntimeBrief.Objective) {
		if frontier.HasWorkspaceFileExtension(path) {
			return workspace.ToolRequest{}, false
		}
	}
	pattern := delegatedReadOnlyBootstrapPattern(r.RuntimeBrief.Objective)
	if pattern == "" {
		return workspace.ToolRequest{}, false
	}
	return workspace.ToolRequest{
		Tool:     "discovery_query",
		Purpose:  delegatedReadOnlyDiscoveryPurpose(r.RuntimeBrief.Objective),
		Path:     delegatedReadOnlyBootstrapScope(r.RuntimeBrief.Objective),
		Pattern:  pattern,
		MaxDepth: 2,
	}, true
}

func delegatedReadOnlyDiscoveryPurpose(objective string) string {
	lower := strings.ToLower(objective)
	switch {
	case containsAny(lower, "security", "unsafe", "vulnerability", "injection", "secret", "token"):
		return "security_review"
	case containsAny(lower, "history", "evolution", "changed over time", "regression introduced"):
		return "history_context"
	case containsAny(lower, "call", "calls", "trace", "tracing", "invoked", "invoke", "entrypoint"):
		return "function_tracing"
	case containsAny(lower, "impact", "blast radius", "affected", "change"):
		return "impact_analysis"
	default:
		return "architecture_boundary"
	}
}

func delegatedReadOnlyTaskPrompt(objective string, workspaceRoot string) string {
	objective = strings.TrimSpace(objective)
	if objective == "" {
		return ""
	}
	parts := []string{objective}
	if scope := delegatedReadOnlyLikelySourceScope(objective, workspaceRoot); scope != "" {
		parts = append(parts, "Named workspace scope: "+scope)
	}
	parts = append(parts,
		"Start from source-of-truth implementation files before tests, README, or docs.",
		"Return inspected paths, key findings, confidence, and named gaps.",
	)
	return strings.Join(parts, "\n")
}

func delegatedReadOnlyLikelySourceScope(objective string, workspaceRoot string) string {
	if strings.TrimSpace(workspaceRoot) == "" {
		return ""
	}
	scopes := make([]string, 0, 6)
	for _, candidate := range delegatedReadOnlyLikelySourceCandidates(objective) {
		info, err := os.Stat(filepath.Join(workspaceRoot, candidate))
		if err != nil || !info.IsDir() {
			continue
		}
		scopes = append(scopes, candidate)
	}
	return strings.Join(scopes, ", ")
}

func delegatedReadOnlyLikelySourceCandidates(objective string) []string {
	if delegatedObjectiveLooksLikeDebugLogRedaction(objective) {
		return []string{"internal/transcript", "internal/app", "internal/agent", "internal/workspace", "cmd", "internal", "pkg", "src"}
	}
	return []string{"internal/agent", "internal/workspace", "cmd", "internal", "pkg", "src"}
}

func delegatedReadOnlyBootstrapSearchRequest(objective string) (workspace.ToolRequest, bool) {
	pattern := delegatedReadOnlyBootstrapPattern(objective)
	if pattern == "" {
		return workspace.ToolRequest{}, false
	}
	req := workspace.ToolRequest{Tool: "search_content", Pattern: pattern}
	if scope := delegatedReadOnlyBootstrapScope(objective); scope != "" && scope != "." {
		req.Path = scope
	}
	if !frontier.HasWorkspaceFileExtension(req.Path) {
		req.Glob = "*.go"
	}
	return req, true
}

func delegatedReadOnlyBootstrapScope(objective string) string {
	for _, line := range strings.Split(objective, "\n") {
		line = strings.TrimSpace(line)
		lower := strings.ToLower(line)
		if !strings.HasPrefix(lower, "named workspace scope:") {
			continue
		}
		scopeList := strings.Split(line[len("Named workspace scope:"):], ",")
		for _, candidate := range scopeList {
			candidate = strings.TrimSpace(strings.Trim(candidate, "`'\""))
			if candidate == "" {
				continue
			}
			return filepath.ToSlash(strings.TrimSuffix(candidate, "/"))
		}
	}
	for _, candidate := range frontier.PromptMentionedWorkspacePaths(objective) {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if frontier.HasWorkspaceFileExtension(candidate) {
			dir := filepath.ToSlash(filepath.Dir(candidate))
			if dir != "." && dir != "" {
				return dir
			}
			return candidate
		}
		return filepath.ToSlash(candidate)
	}
	return "."
}

func delegatedReadOnlyBootstrapPattern(objective string) string {
	terms := make([]string, 0, 6)
	for _, line := range strings.Split(objective, "\n") {
		line = strings.TrimSpace(line)
		lower := strings.ToLower(line)
		if !strings.HasPrefix(lower, "key terms:") {
			continue
		}
		for _, candidate := range strings.Split(line[len("Key terms:"):], ",") {
			if term, ok := delegatedReadOnlySearchTerm(candidate); ok {
				terms = appendUniqueSearchTerm(terms, term)
			}
		}
	}
	for _, fragment := range frontier.QuotedPromptFragments(objective) {
		if term, ok := delegatedReadOnlySearchTerm(fragment); ok {
			terms = appendUniqueSearchTerm(terms, term)
		}
	}
	if len(terms) == 0 {
		for _, candidate := range strings.Fields(objective) {
			if term, ok := delegatedReadOnlySearchTerm(candidate); ok {
				terms = appendUniqueSearchTerm(terms, term)
			}
			if len(terms) >= 4 {
				break
			}
		}
	}
	for _, term := range delegatedReadOnlyMechanismTerms(objective) {
		if normalized, ok := delegatedReadOnlySearchTerm(term); ok {
			terms = appendUniqueSearchTerm(terms, normalized)
		}
	}
	return strings.Join(terms, "|")
}

func delegatedReadOnlySearchTerm(raw string) (string, bool) {
	value := strings.ToLower(strings.TrimSpace(raw))
	value = strings.Trim(value, "`'\"()[]{}<>,:;!?")
	value = strings.TrimSpace(strings.TrimPrefix(value, "./"))
	if value == "" || frontier.LooksLikeWorkspacePathToken(value) {
		return "", false
	}
	switch value {
	case "agent", "codebase", "architecture", "configuration", "config", "configs", "prompt", "prompts", "main", "files", "file", "directory", "directories", "entry", "entrypoints", "line", "lines", "harness", "implementation", "source", "truth", "before", "tests", "test", "readme", "docs", "start":
		return "", false
	}
	if len(value) < 4 {
		return "", false
	}
	var b strings.Builder
	for _, r := range value {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r):
			b.WriteString(regexp.QuoteMeta(string(r)))
		case r == '-', r == '_', unicode.IsSpace(r):
			b.WriteString("[-_ ]?")
		}
	}
	pattern := strings.TrimSpace(b.String())
	if pattern == "" {
		return "", false
	}
	return pattern, true
}

func appendUniqueSearchTerm(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func (r *Runner) delegatedReadOnlySecondaryEvidenceRedirect(results []workspace.ToolResult, req workspace.ToolRequest) (workspace.ToolRequest, bool) {
	if r == nil || !strings.HasPrefix(r.agentName(), "child:") || strings.TrimSpace(r.RuntimeBrief.TaskFamily) != "delegated_read_only" {
		return workspace.ToolRequest{}, false
	}
	targetPath := r.delegatedReadOnlyRequestPath(req)
	if targetPath == "" || !delegatedReadOnlySecondaryEvidencePath(targetPath) {
		return workspace.ToolRequest{}, false
	}
	if redirected, ok := delegatedReadOnlyNamedPrimaryPathRedirect(r.RuntimeBrief.Objective, targetPath); ok {
		return redirected, true
	}
	if strings.HasSuffix(targetPath, "_test.go") {
		sourcePath := strings.TrimSuffix(targetPath, "_test.go") + ".go"
		pattern := delegatedReadOnlyImplementationPattern(r.RuntimeBrief.Objective, sourcePath)
		if pattern != "" {
			return workspace.ToolRequest{Tool: "search_content", Path: sourcePath, Pattern: pattern}, true
		}
	}
	return delegatedReadOnlyBootstrapSearchRequest(r.RuntimeBrief.Objective)
}

func (r *Runner) delegatedReadOnlyRequestPath(req workspace.ToolRequest) string {
	if r == nil {
		return ""
	}
	if path := strings.TrimSpace(req.Path); path != "" {
		return filepath.ToSlash(path)
	}
	if r.Workspace == nil || strings.TrimSpace(req.ID) == "" {
		return ""
	}
	path, err := r.Workspace.ResolveHandlePath(req.ID)
	if err != nil {
		return ""
	}
	return filepath.ToSlash(path)
}

func delegatedReadOnlySecondaryEvidencePath(path string) bool {
	path = filepath.ToSlash(strings.TrimSpace(path))
	if path == "" {
		return false
	}
	lower := strings.ToLower(path)
	if strings.HasSuffix(lower, "_test.go") || strings.HasPrefix(lower, "docs/") || strings.Contains(lower, "/docs/") {
		return true
	}
	base := strings.ToLower(filepath.Base(lower))
	return base == "readme.md" || strings.HasSuffix(base, ".md")
}

func delegatedReadOnlyNamedPrimaryPathRedirect(objective string, blockedPath string) (workspace.ToolRequest, bool) {
	blockedPath = filepath.ToSlash(strings.TrimSpace(blockedPath))
	for _, candidate := range frontier.PromptMentionedWorkspacePaths(objective) {
		candidate = filepath.ToSlash(strings.TrimSpace(candidate))
		if candidate == "" || candidate == blockedPath || delegatedReadOnlySecondaryEvidencePath(candidate) {
			continue
		}
		pattern := delegatedReadOnlyImplementationPattern(objective, candidate)
		if pattern == "" {
			continue
		}
		return workspace.ToolRequest{Tool: "search_content", Path: candidate, Pattern: pattern}, true
	}
	return workspace.ToolRequest{}, false
}

func delegatedReadOnlyImplementationPattern(objective string, targetPath string) string {
	terms := make([]string, 0, 8)
	for _, part := range strings.Split(delegatedReadOnlyBootstrapPattern(objective), "|") {
		terms = appendUniqueSearchTerm(terms, part)
	}
	base := strings.TrimSuffix(filepath.Base(targetPath), filepath.Ext(targetPath))
	base = strings.TrimSuffix(base, "_test")
	for _, fragment := range strings.FieldsFunc(base, func(r rune) bool {
		return r == '_' || r == '-' || unicode.IsSpace(r)
	}) {
		if term, ok := delegatedReadOnlySearchTerm(fragment); ok {
			terms = appendUniqueSearchTerm(terms, term)
		}
	}
	for _, term := range delegatedReadOnlyMechanismTerms(objective) {
		if normalized, ok := delegatedReadOnlySearchTerm(term); ok {
			terms = appendUniqueSearchTerm(terms, normalized)
		}
	}
	return strings.Join(terms, "|")
}

func delegatedReadOnlyMechanismTerms(objective string) []string {
	lower := strings.ToLower(delegatedDisplayQuestion(objective))
	terms := make([]string, 0, 6)
	if containsAny(lower, "sub-agent", "subagent", "sub agent", "delegate", "delegated", "child agent", "child task") {
		for _, term := range []string{"delegate", "task", "runner_child_tasks", "run child task"} {
			terms = appendUniqueSearchTerm(terms, term)
		}
	}
	if containsAny(lower, "invoke", "invoked", "execute", "executed", "run", "runner", "workflow", "wired") {
		for _, term := range []string{"invoke", "execute"} {
			terms = appendUniqueSearchTerm(terms, term)
		}
	}
	return terms
}

func (r *Runner) delegatedReadOnlyNamedFileReadRedirect(results []workspace.ToolResult, req workspace.ToolRequest) (workspace.ToolRequest, bool) {
	if r == nil || !strings.HasPrefix(r.agentName(), "child:") || strings.TrimSpace(r.RuntimeBrief.TaskFamily) != "delegated_read_only" {
		return workspace.ToolRequest{}, false
	}
	if len(results) > 0 {
		return workspace.ToolRequest{}, false
	}
	if strings.TrimSpace(req.Tool) != "read_file" || req.StartLine > 0 || req.EndLine > 0 {
		return workspace.ToolRequest{}, false
	}
	path := strings.TrimSpace(req.Path)
	if !strings.HasSuffix(path, ".go") {
		return workspace.ToolRequest{}, false
	}
	pattern := delegatedReadOnlyBootstrapPattern(r.RuntimeBrief.Objective)
	if pattern == "" {
		return workspace.ToolRequest{}, false
	}
	return workspace.ToolRequest{Tool: "search_content", Path: path, Pattern: pattern}, true
}

func (r *Runner) delegatedReadOnlyIntegrationPivotRedirect(results []workspace.ToolResult, req workspace.ToolRequest) (workspace.ToolRequest, bool) {
	if r == nil || !strings.HasPrefix(r.agentName(), "child:") || strings.TrimSpace(r.RuntimeBrief.TaskFamily) != "delegated_read_only" {
		return workspace.ToolRequest{}, false
	}
	if !isReadClassTool(req) {
		return workspace.ToolRequest{}, false
	}
	currentPath := r.delegatedReadOnlyRequestPath(req)
	if currentPath == "" || !strings.HasSuffix(currentPath, ".go") || strings.HasSuffix(currentPath, "_test.go") {
		return workspace.ToolRequest{}, false
	}
	if !delegatedReadOnlyHasBodyRead(results, currentPath) {
		return workspace.ToolRequest{}, false
	}
	targetPath := delegatedReadOnlySiblingImplementationPath(results, currentPath)
	if targetPath == "" || targetPath == currentPath {
		return workspace.ToolRequest{}, false
	}
	if delegatedReadOnlyHasBodyRead(results, targetPath) {
		return workspace.ToolRequest{}, false
	}
	if start, end, ok := delegatedReadOnlySearchHitWindow(results, targetPath); ok {
		return workspace.ToolRequest{
			Tool:      "read_file",
			Path:      targetPath,
			StartLine: start,
			EndLine:   end,
		}, true
	}
	pattern := delegatedReadOnlyImplementationPattern(r.RuntimeBrief.Objective, targetPath)
	if pattern == "" {
		return workspace.ToolRequest{}, false
	}
	return workspace.ToolRequest{Tool: "search_content", Path: targetPath, Pattern: pattern}, true
}

func (r *Runner) delegatedReadOnlySearchHitReadRedirect(results []workspace.ToolResult, req workspace.ToolRequest) (workspace.ToolRequest, bool) {
	if r == nil || !strings.HasPrefix(r.agentName(), "child:") || strings.TrimSpace(r.RuntimeBrief.TaskFamily) != "delegated_read_only" {
		return workspace.ToolRequest{}, false
	}
	if strings.TrimSpace(req.Tool) != "read_file" {
		return workspace.ToolRequest{}, false
	}
	path := r.delegatedReadOnlyRequestPath(req)
	if path == "" || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
		return workspace.ToolRequest{}, false
	}
	start, end, ok := delegatedReadOnlySearchHitWindow(results, path)
	if !ok {
		return workspace.ToolRequest{}, false
	}
	if req.StartLine <= 0 || req.EndLine <= 0 {
		return workspace.ToolRequest{}, false
	}
	if req.StartLine == start && req.EndLine == end {
		return workspace.ToolRequest{}, false
	}
	if start >= req.StartLine-20 && end <= req.EndLine+20 {
		return workspace.ToolRequest{}, false
	}
	return workspace.ToolRequest{
		Tool:      "read_file",
		Path:      path,
		StartLine: start,
		EndLine:   end,
	}, true
}

func (r *Runner) delegatedReadOnlyAnswerReadyAfterResult(req workspace.ToolRequest, result workspace.ToolResult, results []workspace.ToolResult) bool {
	if r == nil || strings.TrimSpace(result.Error) != "" {
		return false
	}
	if !strings.HasPrefix(r.agentName(), "child:") || strings.TrimSpace(r.RuntimeBrief.TaskFamily) != "delegated_read_only" {
		return false
	}
	if runnerBriefRequiresMutation(r.RuntimeBrief) || runnerVerificationRequired(r.RuntimeBrief) {
		return false
	}
	tool := strings.TrimSpace(firstNonEmpty(result.Tool, req.Tool))
	switch {
	case tool == "discovery_query":
		if !delegatedReadOnlyHasCompositeDiscoveryPacket(results) {
			return false
		}
	case isReadClassTool(workspace.ToolRequest{Tool: tool}):
		if len(delegatedReadOnlyInspectedImplementationPaths(results)) < 2 {
			return false
		}
	default:
		return false
	}
	return true
}

func delegatedReadOnlyHasCompositeDiscoveryPacket(results []workspace.ToolResult) bool {
	for _, result := range results {
		if strings.TrimSpace(result.Tool) == "discovery_query" &&
			strings.TrimSpace(result.Error) == "" &&
			strings.Contains(result.Output, `"artifact_path"`) &&
			strings.Contains(result.Output, `"evidence"`) {
			return true
		}
	}
	return false
}

func delegatedReadOnlyInspectedImplementationPaths(results []workspace.ToolResult) []string {
	seen := make(map[string]struct{})
	paths := make([]string, 0, 4)
	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" || strings.TrimSpace(result.Output) == "" {
			continue
		}
		switch strings.TrimSpace(result.Tool) {
		case "read_file", "go_read_symbol", "go_file_outline":
		default:
			continue
		}
		for _, path := range pinnedPathsFromToolResults([]workspace.ToolResult{result}, 4) {
			path = filepath.ToSlash(strings.TrimSpace(path))
			if path == "" || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				continue
			}
			if _, ok := seen[path]; ok {
				continue
			}
			seen[path] = struct{}{}
			paths = append(paths, path)
		}
	}
	return paths
}

func delegatedReadOnlyHasBodyRead(results []workspace.ToolResult, wantPath string) bool {
	wantPath = filepath.ToSlash(strings.TrimSpace(wantPath))
	if wantPath == "" {
		return false
	}
	for _, path := range delegatedReadOnlyInspectedImplementationPaths(results) {
		if path == wantPath {
			return true
		}
	}
	return false
}

func delegatedReadOnlySiblingImplementationPath(results []workspace.ToolResult, currentPath string) string {
	currentPath = filepath.ToSlash(strings.TrimSpace(currentPath))
	if currentPath == "" {
		return ""
	}
	candidates := make([]string, 0, 6)
	dir := filepath.ToSlash(filepath.Dir(currentPath))
	base := strings.TrimSuffix(filepath.Base(currentPath), filepath.Ext(currentPath))
	parts := strings.FieldsFunc(base, func(r rune) bool { return r == '_' || r == '-' })
	for shrink := len(parts) - 1; shrink >= 1; shrink-- {
		candidate := filepath.ToSlash(filepath.Join(dir, strings.Join(parts[:shrink], "_")+".go"))
		if candidate != currentPath {
			candidates = append(candidates, candidate)
		}
	}
	for _, path := range pinnedPathsFromToolResults(results, 32) {
		path = filepath.ToSlash(strings.TrimSpace(path))
		if path == "" || path == currentPath || strings.HasSuffix(path, "_test.go") || !strings.HasSuffix(path, ".go") {
			continue
		}
		if filepath.ToSlash(filepath.Dir(path)) != dir {
			continue
		}
		candidates = append(candidates, path)
	}
	seen := make(map[string]struct{})
	for _, candidate := range candidates {
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		for _, result := range results {
			if toolResultMentionsPath(result, candidate) {
				return candidate
			}
		}
	}
	return ""
}

func delegatedReadOnlySearchHitWindow(results []workspace.ToolResult, path string) (int, int, bool) {
	path = filepath.ToSlash(strings.TrimSpace(path))
	if path == "" {
		return 0, 0, false
	}
	pattern := regexp.MustCompile(regexp.QuoteMeta(path) + `:(\d+):`)
	for _, result := range results {
		if strings.TrimSpace(result.Tool) != "search_content" || strings.TrimSpace(result.Output) == "" {
			continue
		}
		matches := pattern.FindStringSubmatch(result.Output)
		if len(matches) != 2 {
			continue
		}
		line, err := strconv.Atoi(matches[1])
		if err != nil || line <= 0 {
			continue
		}
		start := line - 20
		if start < 1 {
			start = 1
		}
		return start, line + 80, true
	}
	return 0, 0, false
}

func toolResultMentionsPath(result workspace.ToolResult, path string) bool {
	path = filepath.ToSlash(strings.TrimSpace(path))
	if path == "" {
		return false
	}
	for _, field := range []string{result.Summary, result.Output, result.Error} {
		if strings.Contains(filepath.ToSlash(field), path) {
			return true
		}
	}
	return false
}
