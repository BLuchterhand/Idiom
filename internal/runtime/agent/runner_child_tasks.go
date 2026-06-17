package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/autonomy"
	"idiom/internal/contracts/pathparse"
	"idiom/internal/platform/config"
	"idiom/internal/platform/util"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/workerruntime"
	"idiom/internal/runtime/agent/agentutil"
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

	prompt = r.prepareReadOnlyChildPrompt(prompt)

	childPolicy := RunPolicy{MaxToolSteps: 4, MaxNoProgressTurns: 3, MaxModelCalls: 6, MaxDuration: 75 * time.Second}

	if workerruntime.RuntimeContractIsDirectAnswer(r.RuntimeBrief.Contract) {
		childPolicy.MaxToolSteps = 4
		childPolicy.MaxModelCalls = 6
		childPolicy.MaxDuration = 45 * time.Second
	}

	if effective := r.effectivePolicy(); effective.MaxContextMessages > 0 || effective.MaxContextChars > 0 {
		childPolicy.MaxContextMessages = effective.MaxContextMessages
		childPolicy.MaxContextChars = effective.MaxContextChars
	}

	lifecycle := r.beginReadOnlyChildLifecycle(agentName, opts, childPolicy)

	childBrief := workerruntime.RuntimeBriefForRole("child_read_only", "child:"+agentName, prompt)
	if len(pathparse.MentionedWorkspacePaths(prompt)) > 0 {
		childBrief.AllowedTools = filterOutRunnerTools(childBrief.AllowedTools,
			"go_overview", "go_entrypoints",
			"cpp_overview", "cpp_entrypoints",
			"python_overview", "python_entrypoints",
			"ts_overview", "ts_entrypoints",
			"rust_overview", "rust_entrypoints",
			"java_overview", "java_entrypoints",
		)
		childBrief.SearchPolicy = util.AppendUnique(childBrief.SearchPolicy, "start_from_named_paths_before_workspace_overview")
		childBrief.Constraints = util.AppendUnique(childBrief.Constraints, "named_paths_scoped")
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
					"For broad architecture, impact, tracing, security, or history questions, a layered discovery evidence packet satisfies orientation only; read at least one relevant source body before answering.",
					"After one sufficient source-body probe, answer unless one exact missing claim would materially change the answer.",
					"Prefer the minimal implementation surface needed to answer the question. Do not expand into side-path comparison, mode-by-mode breakdowns, or policy tables unless the question explicitly requires that detail.",
					"After readiness, treat packet-named files or symbols as evidence, not an allowlist. Either answer or take one exact follow-up that resolves a named missing claim.",
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
			event.ChildInvocationID = lifecycle.InvocationID
			event.ChildContractID = lifecycle.ChildContractID
			r.emitEvent(marshalRunEvent("child_tool_request", event))
		},
		func(event ToolResultEvent) {
			childResults = append(childResults, event.Result)
			event.ChildInvocationID = lifecycle.InvocationID
			event.ChildContractID = lifecycle.ChildContractID
			r.emitEvent(marshalRunEvent("child_tool_result", event))
		},
	)
	if err != nil {
		result := workspace.ToolResult{
			Tool:    "task",
			Summary: "Child task failed",
			Error:   err.Error(),
		}
		r.emitReadOnlyChildTerminal(lifecycle, result, childResults, autonomy.ChildTerminalFailed)
		return result
	}

	fallbackSourceReads := 0
	maxFallbackSourceReads := 1

	for r.Workspace != nil && fallbackSourceReads < maxFallbackSourceReads && delegatedChildFallbackSourceReadNeeded(prompt, childResults) {
		if req, result, ok := r.delegatedChildFallbackSourceRead(ctx, prompt, childResults); ok {
			step := len(childResults) + 1
			profile := workspace.DiscoveryProfileForRequest(req)
			r.emitEvent(marshalRunEvent("child_tool_request", ToolRequestEvent{
				Step:              step,
				Agent:             child.agentName(),
				ChildInvocationID: lifecycle.InvocationID,
				ChildContractID:   lifecycle.ChildContractID,
				Raw:               marshalToolRequestRaw(req),
				Request:           req,
				Mode:              r.Workspace.Mode(),
				Broad:             workspace.IsBroadInspectionRequest(req),
				Narrowing:         workspace.IsNarrowingRequest(req),
				Action:            toolActionClass(req),
				DiscoveryClass:    profile.Class,
				DiscoveryScope:    profile.Scope,
				DiscoveryCost:     profile.Cost,
			}))
			r.emitEvent(marshalRunEvent("child_tool_result", ToolResultEvent{
				Step:              step,
				Agent:             child.agentName(),
				ChildInvocationID: lifecycle.InvocationID,
				ChildContractID:   lifecycle.ChildContractID,
				Result:            result,
			}))

			childResults = append(childResults, result)
			fallbackSourceReads++

			continue
		}

		break
	}

	if blockerKind, _ := child.finalAnswerBlockerKind(answer, childResults); strings.TrimSpace(answer) == "" || strings.HasPrefix(strings.TrimSpace(answer), "<<QUESTION>>") || blockerKind != "" {
		if synthesized, ok := synthesizeDelegatedReadOnlyAnswer(prompt, childResults); ok {
			if blockerKind, _ := child.finalAnswerBlockerKind(synthesized, childResults); blockerKind == "" {
				answer = synthesized
			}
		} else if fallback, ok := deterministicNoToolsAnswer(child.agentName(), child.RuntimeBrief, childResults); ok {
			if blockerKind, _ := child.finalAnswerBlockerKind(fallback, childResults); blockerKind == "" {
				answer = fallback
			}
		}
	}

	answer = augmentDelegatedReadOnlyPacket(answer, prompt, childResults)
	answer = normalizeDelegatedReadOnlyPacket(answer)

	result := workspace.ToolResult{
		Tool:    "task",
		Summary: "Child task completed",
		Output:  strings.TrimSpace(answer),
	}

	if packet, ok := buildReadOnlyEvidencePacket(prompt, answer, childResults); ok {
		result.Output = renderReadOnlyEvidencePacketForToolOutput(packet, answer)
		result = readOnlyEvidencePacketToolResult(result, packet)
	}

	r.emitReadOnlyChildTerminal(lifecycle, result, childResults, autonomy.ChildTerminalCompleted)

	return result
}

func (r *Runner) delegatedChildFallbackSourceRead(ctx context.Context, prompt string, results []workspace.ToolResult) (workspace.ToolRequest, workspace.ToolResult, bool) {
	if r == nil || r.Workspace == nil {
		return workspace.ToolRequest{}, workspace.ToolResult{}, false
	}

	paths := newDelegatedDiscoveryPlan(prompt).consumableSourceBodyCandidatePaths(results, 6)
	for _, path := range paths {
		path = agentutil.NormalizeRunnerPath(path)
		if path == "" || agentutil.IsNonSourceEvidenceArtifactPath(path) || concreteSourceEvidenceReadForPath(path, results) {
			continue
		}

		req := workspace.ToolRequest{
			Tool:            "read_file",
			Path:            path,
			StartLine:       1,
			EndLine:         160,
			NamedGap:        "source-body evidence for delegated read-only synthesis",
			WhyNotAnswering: "the child has discovery evidence but needs one source-body read before a grounded answer",
		}
		result := r.Workspace.Execute(ctx, req)

		if strings.TrimSpace(result.Error) != "" || !readFileResultHasConcreteSourceBody(result) {
			continue
		}

		return req, result, true
	}

	return workspace.ToolRequest{}, workspace.ToolResult{}, false
}

func delegatedChildFallbackSourceReadNeeded(prompt string, results []workspace.ToolResult) bool {
	return len(delegatedReadOnlySourceBodyEvidencePaths(results)) == 0
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

			kept = util.AppendUnique(kept, line)
		}
	}

	if namedPaths := pathparse.MentionedWorkspacePaths(prompt); len(namedPaths) > 0 {
		kept = util.AppendUnique(kept, "Named workspace scope: "+strings.Join(namedPaths, ", "))
	}

	if keyTerms := readOnlyChildKeyTerms(prompt); len(keyTerms) > 0 {
		kept = util.AppendUnique(kept, "Key terms: "+strings.Join(keyTerms, ", "))
	}

	if len(kept) == 0 {
		return prompt
	}

	return strings.Join(kept, "\n")
}

func (r *Runner) prepareReadOnlyChildPrompt(prompt string) string {
	prompt = normalizeReadOnlyChildPrompt(prompt)
	if prompt == "" {
		return ""
	}

	if !readOnlyChildPromptHasPrimaryNamedScope(prompt) && r != nil && r.Workspace != nil {
		if scope := delegatedReadOnlyLikelySourceScope(prompt, r.Workspace.Root()); scope != "" {
			prompt += "\nNamed workspace scope: " + scope
		}
	}

	return r.augmentReadOnlyChildPrompt(prompt)
}

func readOnlyChildPromptHasPrimaryNamedScope(prompt string) bool {
	return newDelegatedDiscoveryPlan(prompt).hasPrimaryScope
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
	if !agentutil.ContainsAny(lower,
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
	return newDelegatedDiscoveryPlan(prompt).promptKeyTerms
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
	lifecycle := r.beginWorkbranchChildLifecycle(agentName, opts, childPolicy, childClient)

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
			event.ChildInvocationID = lifecycle.InvocationID
			event.ChildContractID = lifecycle.ChildContractID
			r.emitEvent(marshalRunEvent("child_tool_request", event))
		},
		func(event ToolResultEvent) {
			event.ChildInvocationID = lifecycle.InvocationID
			event.ChildContractID = lifecycle.ChildContractID
			r.emitEvent(marshalRunEvent("child_tool_result", event))
		},
	)
	diff, diffErr := manager.Diff(ctx, checkout)

	if runErr != nil {
		result := workspace.ToolResult{Tool: "task", Summary: "Workbranch child task failed", Error: runErr.Error(), Output: renderWorkbranchChildOutput(checkout, diff, answer)}
		r.emitWorkbranchChildTerminal(lifecycle, checkout, result, diff, autonomy.ChildTerminalFailed)
		return result
	}

	if diffErr != nil {
		result := workspace.ToolResult{Tool: "task", Summary: "Workbranch child diff failed", Error: diffErr.Error(), Output: strings.TrimSpace(answer)}
		r.emitWorkbranchChildTerminal(lifecycle, checkout, result, diff, autonomy.ChildTerminalFailed)
		return result
	}

	result := workspace.ToolResult{
		Tool:    "task",
		Summary: fmt.Sprintf("Workbranch child task completed with %d changed path(s)", len(diff.ChangedPaths)),
		Output:  renderWorkbranchChildOutput(checkout, diff, answer),
	}
	r.emitWorkbranchChildTerminal(lifecycle, checkout, result, diff, autonomy.ChildTerminalCompleted)
	return result
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
	allowTool := func(tool string) ToolPermissionRule {
		return ToolPermissionRule{Tool: tool, Decision: PermissionAllow}
	}
	allowShellPrefix := func(prefix string) ToolPermissionRule {
		return ToolPermissionRule{Tool: "shell", CommandPrefix: prefix, Decision: PermissionAllow}
	}
	deny := func(tool string) ToolPermissionRule {
		return ToolPermissionRule{Tool: tool, Decision: PermissionDeny}
	}
	denyShellPrefix := func(prefix string) ToolPermissionRule {
		return ToolPermissionRule{Tool: "shell", CommandPrefix: prefix, Decision: PermissionDeny}
	}
	denyShellContains := func(value string) ToolPermissionRule {
		return ToolPermissionRule{Tool: "shell", CommandContains: value, Decision: PermissionDeny}
	}
	spec := workerruntime.RoleSpecFor("child_write")
	workbranchAllowPrefixes := workbranchAllowedShellPrefixes()
	rules := make([]ToolPermissionRule, 0, len(spec.AllowedTools)+len(workbranchAllowPrefixes)+len(workbranchDeniedShellPrefixes(spec))+2)

	for _, tool := range spec.AllowedTools {
		tool = strings.TrimSpace(tool)
		if tool == "" || tool == "task" || tool == "shell" {
			continue
		}

		rules = append(rules, allowTool(tool))
	}

	for _, prefix := range workbranchAllowPrefixes {
		rules = append(rules, allowShellPrefix(prefix))
	}

	rules = append(rules, deny("task"))
	for _, prefix := range workbranchDeniedShellPrefixes(spec) {
		rules = append(rules, denyShellPrefix(prefix))
	}

	rules = append(rules, denyShellPrefix("git -C"))
	for _, value := range workbranchDeniedShellFragments() {
		rules = append(rules, denyShellContains(value))
	}

	return ToolPermissionPolicy{
		Default: PermissionDeny,
		Rules:   rules,
	}
}

func workbranchAllowedShellPrefixes() []string {
	return []string{
		"go test", "go list",
		"git status", "git diff", "git log", "git show", "git grep",
		"pwd", "ls ",
		"rg ", "grep ",
		"cat ", "head ", "tail ", "wc ",
	}
}

func workbranchDeniedShellFragments() []string {
	return []string{"\n", "\r", "&&", "||", ";", "|", ">", "<", "$(", "`", "go env -w", "go env -u", "--output="}
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

	return util.Merge(denied)
}
