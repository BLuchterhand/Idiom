package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"idiom/internal/config"
	"idiom/internal/frontier"
	"idiom/internal/frontier/workerruntime"
	"idiom/internal/llama"
	"idiom/internal/workspace"
)

func (r *Runner) runChildTask(ctx context.Context, req workspace.ToolRequest, opts llama.ChatOptions) workspace.ToolResult {
	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		prompt = strings.TrimSpace(req.Content)
	}
	if prompt == "" {
		return workspace.ToolResult{
			Tool:    "task",
			Summary: "Child task rejected",
			Error:   "task prompt is required",
		}
	}
	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	if mode == "" {
		mode = "read_only"
	}
	if isWriteChildMode(mode) {
		return r.runWorkbranchChildTask(ctx, req, prompt, opts)
	}
	if mode != "read_only" && mode != "readonly" && mode != "explore" {
		return workspace.ToolResult{
			Tool:    "task",
			Summary: "Child task rejected",
			Error:   "only read_only child tasks are currently supported",
		}
	}
	agentName := strings.TrimSpace(req.Agent)
	if agentName == "" {
		agentName = "child"
	}
	prompt = normalizeReadOnlyChildPrompt(prompt)
	prompt = r.augmentReadOnlyChildPrompt(prompt)
	childPolicy := RunPolicy{MaxToolSteps: 6, MaxNoProgressTurns: 3, MaxModelCalls: 8, MaxDuration: 2 * time.Minute}
	if strings.EqualFold(strings.TrimSpace(r.RuntimeBrief.Contract), "direct_answer") {
		childPolicy.MaxToolSteps = 4
		childPolicy.MaxModelCalls = 6
		childPolicy.MaxDuration = 45 * time.Second
	}
	if effective := r.effectivePolicy(); effective.MaxContextMessages > 0 || effective.MaxContextChars > 0 {
		childPolicy.MaxContextMessages = effective.MaxContextMessages
		childPolicy.MaxContextChars = effective.MaxContextChars
	}
	childBrief := workerruntime.RuntimeBriefForRole("child_read_only", "child:"+agentName, prompt)
	if len(frontier.PromptMentionedWorkspacePaths(prompt)) > 0 {
		childBrief.AllowedTools = filterOutRunnerTools(childBrief.AllowedTools,
			"go_overview", "go_entrypoints",
			"cpp_overview", "cpp_entrypoints",
			"python_overview", "python_entrypoints",
			"ts_overview", "ts_entrypoints",
			"rust_overview", "rust_entrypoints",
			"java_overview", "java_entrypoints",
		)
		childBrief.SearchPolicy = appendUniqueRunnerState(childBrief.SearchPolicy, "start_from_named_paths_before_workspace_overview")
		childBrief.Constraints = appendUniqueRunnerState(childBrief.Constraints, "named_paths_scoped")
	}
	child := &Runner{
		Client:           r.Client,
		Workspace:        r.Workspace,
		Policy:           childPolicy,
		AgentName:        "child:" + agentName,
		PermissionPolicy: readOnlyChildTaskPermissionPolicy(),
		TaskLedger:       TaskLedgerPolicy{Disabled: true},
		DisableTaskTool:  true,
		RuntimeBrief:     childBrief,
		OnEvent: func(event RunEvent) {
			if strings.TrimSpace(event.Role) == "" {
				return
			}
			r.emitEvent(RunEvent{Role: "child_" + event.Role, Content: event.Content})
		},
	}
	childResults := make([]workspace.ToolResult, 0, 8)
	answer, err := child.Run(
		ctx,
		[]llama.ChatMessage{
			{
				Role: "system",
				Content: strings.Join([]string{
					"You are a read-only delegated child task.",
					"Answer the delegated question directly using concise grounded evidence.",
					"For broad architecture, impact, tracing, security, or history questions, prefer one discovery_query first; it returns a layered evidence packet.",
					"After one sufficient packet or exact probe, answer instead of reopening search.",
					"Prefer the minimal implementation surface needed to answer the question. Do not expand into side-path comparison, mode-by-mode breakdowns, or policy tables unless the question explicitly requires that detail.",
					"If you continue, name one exact missing claim, one exact probe that would resolve it, and what would be materially incomplete if you answered now.",
					"After readiness, stay inside packet-named files or symbols. Do not switch to README, docs, or repo-wide search unless that exact artifact is the named gap.",
					"Prefer structured symbol/body tools, discovered handles, or narrow line ranges over raw path-wide rereads.",
					"Do not edit files, create files, install packages, push, publish, deploy, or spawn more tasks.",
					"Use repository-relative paths and cite the files you inspected.",
				}, "\n"),
			},
			{Role: "user", Content: prompt},
		},
		opts,
		nil,
		func(event ToolRequestEvent) {
			r.emitEvent(marshalRunEvent("child_tool_request", event))
		},
		func(event ToolResultEvent) {
			childResults = append(childResults, event.Result)
			r.emitEvent(marshalRunEvent("child_tool_result", event))
		},
	)
	if err != nil {
		return workspace.ToolResult{
			Tool:    "task",
			Summary: "Child task failed",
			Error:   err.Error(),
		}
	}
	answer = augmentDelegatedReadOnlyPacket(answer, prompt, childResults)
	answer = normalizeDelegatedReadOnlyPacket(answer)
	return workspace.ToolResult{
		Tool:    "task",
		Summary: "Child task completed",
		Output:  strings.TrimSpace(answer),
	}
}

func normalizeReadOnlyChildPrompt(prompt string) string {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return ""
	}
	lines := strings.Split(prompt, "\n")
	kept := make([]string, 0, len(lines))
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if len(kept) == 0 {
			line = normalizeReadOnlyChildLead(line)
			kept = append(kept, line)
			continue
		}
		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "answer ") ||
			strings.HasPrefix(lower, "named workspace scope:") ||
			strings.HasPrefix(lower, "key terms:") {
			kept = appendUniqueString(kept, line)
		}
	}
	if namedPaths := frontier.PromptMentionedWorkspacePaths(prompt); len(namedPaths) > 0 {
		kept = appendUniqueString(kept, "Named workspace scope: "+strings.Join(namedPaths, ", "))
	}
	if keyTerms := readOnlyChildKeyTerms(prompt); len(keyTerms) > 0 {
		kept = appendUniqueString(kept, "Key terms: "+strings.Join(keyTerms, ", "))
	}
	if len(kept) == 0 {
		return prompt
	}
	return strings.Join(kept, "\n")
}

func normalizeReadOnlyChildLead(line string) string {
	line = strings.TrimSpace(line)
	lower := strings.ToLower(line)
	for _, prefix := range []string{
		"explore this codebase and ",
		"explore the codebase and ",
		"inspect this codebase and ",
		"inspect the codebase and ",
	} {
		if strings.HasPrefix(lower, prefix) {
			remainder := strings.TrimSpace(line[len(prefix):])
			if remainder == "" {
				return line
			}
			return strings.ToUpper(remainder[:1]) + remainder[1:]
		}
	}
	if idx := strings.Index(line, "?"); idx >= 0 {
		question := strings.TrimSpace(line[:idx+1])
		if question != "" {
			return question
		}
	}
	if narrowed := rewriteBroadReadOnlyChildLead(line); narrowed != "" {
		return narrowed
	}
	if trimmed := trimReadOnlyChildLeadAtBroadMarker(line); trimmed != "" {
		return trimmed
	}
	return line
}

func rewriteBroadReadOnlyChildLead(line string) string {
	lower := strings.ToLower(strings.TrimSpace(line))
	if !containsAny(lower,
		"cmd directory",
		"entry point",
		"entrypoint",
		"main entry",
		"look for pattern",
		"focus on ",
		"explore ",
	) {
		return ""
	}
	for _, marker := range []string{"explain ", "describe ", "summarize ", "how ", "what ", "why "} {
		if idx := strings.Index(lower, marker); idx >= 0 {
			rewritten := strings.TrimSpace(line[idx:])
			if rewritten == "" {
				continue
			}
			return strings.ToUpper(rewritten[:1]) + rewritten[1:]
		}
	}
	return ""
}

func trimReadOnlyChildLeadAtBroadMarker(line string) string {
	lower := strings.ToLower(line)
	cut := -1
	for _, marker := range []string{
		" explore the ",
		" explore ",
		" look for ",
		" focus on ",
		" specifically ",
		" emphasize ",
		" start from ",
		" trace through ",
	} {
		if idx := strings.Index(lower, marker); idx > 0 && (cut == -1 || idx < cut) {
			cut = idx
		}
	}
	if cut <= 0 {
		return ""
	}
	trimmed := strings.TrimSpace(strings.TrimRight(line[:cut], ",;:- "))
	if trimmed == "" {
		return ""
	}
	return trimmed
}

func readOnlyChildKeyTerms(prompt string) []string {
	terms := make([]string, 0, 6)
	for _, fragment := range frontier.QuotedPromptFragments(prompt) {
		fragment = strings.TrimSpace(fragment)
		if fragment == "" || frontier.LooksLikeWorkspacePathToken(fragment) {
			continue
		}
		lower := strings.ToLower(fragment)
		if readOnlyChildKeyTermStopWord(lower) {
			continue
		}
		terms = appendUniqueString(terms, fragment)
	}
	for _, candidate := range strings.Fields(prompt) {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" || frontier.LooksLikeWorkspacePathToken(candidate) {
			continue
		}
		lower := strings.ToLower(strings.Trim(candidate, "`'\"()[]{}<>,.:;!?"))
		if readOnlyChildKeyTermStopWord(lower) {
			continue
		}
		if len(lower) < 5 {
			continue
		}
		terms = appendUniqueString(terms, lower)
		if len(terms) >= 8 {
			break
		}
	}
	return terms
}

func readOnlyChildKeyTermStopWord(lower string) bool {
	switch lower {
	case "",
		"how", "does", "this", "explain", "inspect", "explore", "look", "overall", "return", "focus", "specifically", "emphasize",
		"use", "uses", "used", "using", "utilize", "utilizes", "utilized", "utilise", "utilises", "make", "makes", "made",
		"files", "file", "paths", "path", "findings", "finding", "confidence", "gap", "gaps",
		"codebase", "architecture", "config", "configuration", "agent", "agents", "harness",
		"start", "source", "truth", "source-of-truth", "source_of_truth", "implementation", "before", "tests", "test", "readme", "docs",
		"named", "workspace", "scope", "key", "terms", "question", "questions", "directly", "materially", "incomplete",
		"exact", "probe", "sufficient", "evidence", "entry", "entrypoint", "entrypoints", "directory", "directories",
		"internal", "repository", "relative", "delegated", "guidance", "prefer", "targeted", "symbol", "symbols", "bounded", "read", "reads", "overview", "overviews", "inspected":
		return true
	default:
		return false
	}
}

func appendUniqueString(values []string, value string) []string {
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

func isWriteChildMode(mode string) bool {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "write", "workbranch", "workbranch_write", "isolated_write", "patch":
		return true
	default:
		return false
	}
}

func (r *Runner) runWorkbranchChildTask(ctx context.Context, req workspace.ToolRequest, prompt string, opts llama.ChatOptions) workspace.ToolResult {
	if r == nil || r.Workspace == nil || strings.TrimSpace(r.Workspace.Root()) == "" {
		return workspace.ToolResult{Tool: "task", Summary: "Workbranch child task rejected", Error: "parent workspace root is required for isolated write child tasks"}
	}
	agentName := strings.TrimSpace(req.Agent)
	if agentName == "" {
		agentName = "worker"
	}
	manager := WorkbranchManager{Root: r.Workspace.Root()}
	checkout, err := manager.Create(ctx, agentName)
	if err != nil {
		return workspace.ToolResult{Tool: "task", Summary: "Workbranch child task failed", Error: err.Error()}
	}
	defer func() {
		if err := manager.Remove(context.Background(), checkout); err != nil {
			r.emitEvent(marshalRunEvent("child_workbranch_cleanup_error", map[string]string{
				"path":  checkout.Path,
				"error": err.Error(),
			}))
		}
	}()
	r.emitEvent(marshalRunEvent("child_workbranch", checkout))

	childWorkspace := workspace.NewManager(config.Workspace{
		Root:                  checkout.Path,
		CWD:                   checkout.Path,
		Shell:                 "/bin/sh",
		CommandTimeoutSeconds: 120,
		MaxOutputBytes:        131072,
		MaxFileBytes:          262144,
		MaxDirectoryEntries:   2000,
		EnableShell:           true,
		EnableWrites:          true,
	})
	childPolicy := RunPolicy{MaxToolSteps: 10, MaxNoProgressTurns: 4, MaxModelCalls: 16, MaxDuration: 4 * time.Minute}
	if effective := r.effectivePolicy(); effective.MaxContextMessages > 0 || effective.MaxContextChars > 0 {
		childPolicy.MaxContextMessages = effective.MaxContextMessages
		childPolicy.MaxContextChars = effective.MaxContextChars
	}
	childClient := r.Client
	if r.ImplementerClient != nil {
		childClient = r.ImplementerClient
	}
	child := &Runner{
		Client:           childClient,
		Workspace:        childWorkspace,
		Policy:           childPolicy,
		AgentName:        "child_write:" + agentName,
		PermissionPolicy: workbranchChildTaskPermissionPolicy(),
		TaskLedger:       TaskLedgerPolicy{Required: true},
		DisableTaskTool:  true,
		CheckpointPolicy: CheckpointPolicy{Enabled: true},
		RuntimeBrief:     workerruntime.RuntimeBriefForRole("child_write", "child_write:"+agentName, prompt),
		OnEvent: func(event RunEvent) {
			if strings.TrimSpace(event.Role) == "" {
				return
			}
			r.emitEvent(RunEvent{Role: "child_" + event.Role, Content: event.Content})
		},
	}
	answer, runErr := child.Run(
		ctx,
		[]llama.ChatMessage{
			{
				Role: "system",
				Content: strings.Join([]string{
					"You are a bounded isolated implementation child task running in a temporary git worktree.",
					"Make only the delegated change. Do not push, publish, deploy, or spawn more tasks.",
					"Keep task_ledger current. Verify the changed surface when practical.",
					"Final answer must include changed files, verification status, and confidence.",
					childWorkspace.Instructions(),
				}, "\n"),
			},
			{Role: "user", Content: prompt},
		},
		opts,
		nil,
		func(event ToolRequestEvent) {
			r.emitEvent(marshalRunEvent("child_tool_request", event))
		},
		func(event ToolResultEvent) {
			r.emitEvent(marshalRunEvent("child_tool_result", event))
		},
	)
	diff, diffErr := manager.Diff(ctx, checkout)
	if runErr != nil {
		return workspace.ToolResult{Tool: "task", Summary: "Workbranch child task failed", Error: runErr.Error(), Output: renderWorkbranchChildOutput(checkout, diff, answer)}
	}
	if diffErr != nil {
		return workspace.ToolResult{Tool: "task", Summary: "Workbranch child diff failed", Error: diffErr.Error(), Output: strings.TrimSpace(answer)}
	}
	return workspace.ToolResult{
		Tool:    "task",
		Summary: fmt.Sprintf("Workbranch child task completed with %d changed path(s)", len(diff.ChangedPaths)),
		Output:  renderWorkbranchChildOutput(checkout, diff, answer),
	}
}

func renderWorkbranchChildOutput(checkout WorkbranchCheckout, diff WorkbranchDiff, answer string) string {
	var b strings.Builder
	b.WriteString("Isolated workbranch result\n")
	b.WriteString("branch: " + checkout.Branch + "\n")
	b.WriteString("base: " + checkout.BaseSHA + "\n")
	if len(diff.ChangedPaths) > 0 {
		b.WriteString("changed paths:\n")
		for _, path := range diff.ChangedPaths {
			b.WriteString("- " + path + "\n")
		}
	}
	if strings.TrimSpace(diff.Stat) != "" {
		b.WriteString("\nstat:\n")
		b.WriteString(diff.Stat + "\n")
	}
	if strings.TrimSpace(answer) != "" {
		b.WriteString("\nchild answer:\n")
		b.WriteString(strings.TrimSpace(answer) + "\n")
	}
	if strings.TrimSpace(diff.Patch) != "" {
		b.WriteString("\npatch:\n```diff\n")
		b.WriteString(strings.TrimSpace(diff.Patch))
		b.WriteString("\n```\n")
	}
	if len(diff.ChangedPaths) > 0 {
		b.WriteString("\nParent must inspect this diff and explicitly apply or reject it; the main workspace was not mutated by the child.\n")
	}
	return strings.TrimSpace(b.String())
}

func readOnlyChildTaskPermissionPolicy() ToolPermissionPolicy {
	allowTool := func(tool string) ToolPermissionRule {
		return ToolPermissionRule{Tool: tool, Decision: PermissionAllow}
	}
	allowShellPrefix := func(prefix string) ToolPermissionRule {
		return ToolPermissionRule{Tool: "shell", CommandPrefix: prefix, Decision: PermissionAllow}
	}
	rules := make([]ToolPermissionRule, 0, len(childAllowedTools())+20)
	for _, tool := range childAllowedTools() {
		if tool == "shell" {
			continue
		}
		rules = append(rules, allowTool(tool))
	}
	rules = append(rules,
		allowShellPrefix("rg "),
		allowShellPrefix("grep "),
		allowShellPrefix("awk "),
		allowShellPrefix("sed -n"),
		allowShellPrefix("cat "),
		allowShellPrefix("head "),
		allowShellPrefix("tail "),
		allowShellPrefix("wc "),
		allowShellPrefix("pwd"),
		allowShellPrefix("ls "),
		allowShellPrefix("find "),
		allowShellPrefix("git status"),
		allowShellPrefix("git diff"),
		allowShellPrefix("git log"),
		allowShellPrefix("git show"),
		allowShellPrefix("git grep"),
		allowShellPrefix("go test"),
		allowShellPrefix("go list"),
		allowShellPrefix("go env"),
	)
	return ToolPermissionPolicy{
		Default: PermissionDeny,
		Rules:   rules,
	}
}

func workbranchChildTaskPermissionPolicy() ToolPermissionPolicy {
	deny := func(tool string) ToolPermissionRule {
		return ToolPermissionRule{Tool: tool, Decision: PermissionDeny}
	}
	denyShellPrefix := func(prefix string) ToolPermissionRule {
		return ToolPermissionRule{Tool: "shell", CommandPrefix: prefix, Decision: PermissionDeny}
	}
	rules := []ToolPermissionRule{deny("task")}
	for _, prefix := range workbranchDeniedShellPrefixes(workerruntime.RoleSpecFor("child_write")) {
		rules = append(rules, denyShellPrefix(prefix))
	}
	return ToolPermissionPolicy{
		Default: PermissionAllow,
		Rules:   rules,
	}
}

func childAllowedTools() []string {
	return workerruntime.RoleSpecFor("child_read_only").AllowedTools
}

func workbranchDeniedShellPrefixes(spec workerruntime.RoleSpec) []string {
	denied := []string{"gh pr create"}
	for _, forbidden := range spec.ForbiddenTools {
		switch strings.TrimSpace(forbidden) {
		case "git_push":
			denied = append(denied, "git push", "gh pr merge")
		case "git_history_mutation":
			denied = append(denied, "git commit", "git reset", "git checkout", "git switch", "git worktree", "git branch", "git clean")
		case "git_index_mutation":
			denied = append(denied, "git add", "git restore")
		case "publish":
			denied = append(denied, "npm publish", "pnpm publish", "yarn publish", "docker push")
		case "deploy":
			denied = append(denied, "kubectl ", "deploy", "release", "gh release")
		}
	}
	return mergeRunnerStateStrings(denied)
}
