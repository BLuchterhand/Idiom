package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"idiom/internal/contracts/pathparse"
	"idiom/internal/platform/util"
	"idiom/internal/runtime/agent"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/workerruntime"
	"idiom/internal/workflows/planexecute"
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
	if objective, ok := typedPromptCommandObjective(prompt, "/implement"); ok {
		return m.runImplementCommandOnce(ctx, objective)
	}
	if objective, ok := typedPromptCommandObjective(prompt, "/plan-execute"); ok {
		return m.runPlanExecuteCommandOnce(ctx, objective)
	}
	m.emitJSONStart(prompt)
	if err := m.ensureServerReady(ctx); err != nil {
		m.emitJSONError("server_not_ready", err.Error())
		return "", err
	}
	return m.runStandardPromptOnce(ctx, prompt)
}

func typedPromptCommandObjective(prompt string, command string) (string, bool) {
	fields := strings.Fields(prompt)
	if len(fields) == 0 || fields[0] != command {
		return "", false
	}
	objective := strings.TrimSpace(strings.TrimPrefix(prompt, command))
	return objective, objective != ""
}

func (m *Model) runStandardPromptOnce(ctx context.Context, prompt string) (string, error) {
	m.lastAssistantReasoningContent = ""
	plan := m.buildPromptExecutionPlan(ctx, prompt)
	if routeRequiresMutationConfirmation(plan) {
		m.recordPromptRouteWithoutExecution(prompt, plan)
		return m.finishPromptOnceReply(mutationRouteConfirmationQuestion(plan)), nil
	}
	plan = m.recoverDirectImplementationContext(prompt, plan)
	restoreRoute := m.applyDirectImplementationPromptRoute(ctx, prompt, plan)
	defer restoreRoute()
	chatOpts := tuneChatOptionsForPlan(m.chatOptions(), plan)
	chatOpts = applyMaxTokensOverride(chatOpts, m.maxTokensOverride)
	if m.workspace != nil {
		m.workspace.PrimeDiscoveredPaths(pathparse.MentionedWorkspacePaths(prompt))
	}
	suppressTranscript := m.suppressPromptTranscript
	if !suppressTranscript {
		m.messages = append(m.messages, llama.ChatMessage{Role: "user", Content: prompt})
		m.logMessage("user", prompt)
	} else if m.debugLogger != nil {
		_ = m.debugLogger.LogRunEvent("internal_prompt", fmt.Sprintf("suppressed nested prompt (%d chars)", len(prompt)))
	}
	m.logMessage("thinking_config", samplingSummaryForOptions(chatOpts))
	if summary := strings.TrimSpace(renderTurnRoutePlan(plan.route)); summary != "" {
		m.logMessage("route_plan", summary)
		m.enqueueHistoryAuditEvent(historyAuditEvent{kind: "lane", label: "route selected", detail: summary, status: plan.route.InitialLane})
	}
	m.emitWriterRouteDecision(m.writerRouteDecisionForPrompt(prompt, plan))
	history := m.requestMessages()
	if suppressTranscript {
		history = append(history, llama.ChatMessage{Role: "user", Content: prompt})
	}
	reply, err := m.runAgentTurn(ctx, prompt, history, chatOpts, plan)
	if err != nil {
		m.emitJSONError("run_failed", err.Error())
		return "", err
	}
	return m.finishPromptOnceReply(reply), nil
}

func (m *Model) recordPromptRouteWithoutExecution(prompt string, plan promptExecutionPlan) {
	suppressTranscript := m.suppressPromptTranscript
	if !suppressTranscript {
		m.messages = append(m.messages, llama.ChatMessage{Role: "user", Content: prompt})
		m.logMessage("user", prompt)
	} else if m.debugLogger != nil {
		_ = m.debugLogger.LogRunEvent("internal_prompt", fmt.Sprintf("suppressed confirmation prompt (%d chars)", len(prompt)))
	}
	if summary := strings.TrimSpace(renderTurnRoutePlan(plan.route)); summary != "" {
		m.logMessage("route_plan", summary)
		m.enqueueHistoryAuditEvent(historyAuditEvent{kind: "lane", label: "route selected", detail: summary, status: plan.route.InitialLane})
	}
	m.logMessage("route_confirmation", renderRouteContractProjection(projectRouteContract(plan.route, plan.mode)))
	m.emitWriterRouteDecision(m.writerRouteDecisionForPrompt(prompt, plan))
}

func mutationRouteConfirmationQuestion(plan promptExecutionPlan) string {
	command := "/implement"
	lane := "implement"
	if workerruntime.TaskFamilyIsPlanThenExecutePlanner(plan.route.Family) {
		command = "/plan-execute"
		lane = "plan-execute"
	}
	return fmt.Sprintf("This looks like a workspace-changing request. Do you want me to switch to the write-capable %s lane for it? Reply with `%s <objective>` to confirm, or tell me to keep this read-only.", lane, command)
}

func (m *Model) applyDirectImplementationPromptRoute(ctx context.Context, prompt string, plan promptExecutionPlan) func() {
	if m == nil {
		return func() {}
	}
	if !isImplementationLikeFamily(plan.route.Family) {
		return func() {}
	}
	if workerruntime.TaskFamilyIsPlanThenExecutePlanner(plan.route.Family) {
		return func() {}
	}
	return m.applyExecutorPromptRoute(ctx, "implementation direct runner", false)
}

func (m *Model) recoverDirectImplementationContext(prompt string, plan promptExecutionPlan) promptExecutionPlan {
	if !m.shouldPreloadDirectImplementationContext(prompt, plan) {
		return plan
	}
	var diagnostics strings.Builder
	section := planexecute.BuildPromptContextPackSection(m.cfg, planexecute.PromptContextPackOptions{
		Mode:        "implement",
		Title:       prompt,
		Brief:       prompt,
		ProjectRoot: m.projectRoot(),
		Limit:       8,
	}, &diagnostics)
	if diag := strings.TrimSpace(diagnostics.String()); diag != "" {
		m.logMessage("direct_context_recovery", truncateImplementationDispatchText(diag, 700))
	}
	if strings.TrimSpace(section) == "" {
		return plan
	}
	plan.runtimeBrief.TargetPacket = appendUniqueBriefStrings(plan.runtimeBrief.TargetPacket, directContextPacketLines(section)...)
	plan.runtimeBrief.SearchPolicy = appendUniqueBriefStrings(plan.runtimeBrief.SearchPolicy,
		"direct_implement_context_preloaded_before_model_turn",
		"do_not_repeat_context_pack_if_preload_reports_unavailable",
	)
	m.logMessage("direct_context_recovery", "preloaded context packet for direct implementation turn")
	return plan
}

func (m *Model) shouldPreloadDirectImplementationContext(prompt string, plan promptExecutionPlan) bool {
	if m == nil || m.workspace == nil {
		return false
	}
	if !isImplementationLikeFamily(plan.route.Family) || isPlanExecuteInternalFamily(plan.route.Family) {
		return false
	}
	if len(plan.runtimeBrief.TargetPacket) > 0 || len(plan.runtimeBrief.RequiredEditPaths) > 0 || len(plan.runtimeBrief.AllowedEditPaths) > 0 {
		return false
	}
	if len(pathparse.MentionedWorkspacePaths(prompt)) > 0 {
		return false
	}
	return strings.TrimSpace(m.cfg.Chat.ImplementationDispatch) == "plan_execute"
}

func directContextPacketLines(section string) []string {
	lines := []string{"prompt_context:direct_implementation_preload"}
	usedBytes := 0
	for _, line := range strings.Split(section, "\n") {
		line = strings.TrimRight(line, " \t")
		if strings.TrimSpace(line) == "" {
			continue
		}
		usedBytes += len(line)
		if usedBytes > 12000 || len(lines) >= 120 {
			lines = append(lines, "prompt_context:direct_implementation_preload_truncated")
			break
		}
		lines = append(lines, line)
	}
	return lines
}

func (m *Model) finishPromptOnceReply(reply string) string {
	m.takeLastAssistantReasoningContent()
	if m.suppressPromptTranscript {
		if m.debugLogger != nil {
			_ = m.debugLogger.LogRunEvent("internal_assistant", fmt.Sprintf("suppressed nested assistant reply (%d chars)", len(reply)))
		}
		return reply
	}
	// Common presentation layer: no tool-call XML ever reaches the user.
	// Double-check: IsTextualToolCall is supposed to catch everything,
	// but as a last resort also check raw substrings for the known
	// DeepSeek XML tool-call namespaces.
	if agent.IsTextualToolCall(reply) ||
		strings.Contains(reply, "<n_tool") ||
		strings.Contains(reply, "<n_invoke") ||
		strings.Contains(reply, "<janus_tool") ||
		strings.Contains(reply, "<invoke name=") ||
		strings.Contains(reply, "DSML") {
		if m.debugLogger != nil {
			_ = m.debugLogger.LogRunEvent("presentation_layer_xml_rejected", fmt.Sprintf("final answer contained tool-call markup (%d chars); replaced with clean message", len(reply)))
		}
		reply = "I wasn't able to produce a clean answer from the evidence. Could you rephrase or narrow the question?"
	}
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
		route = validateRouteFamilyAgainstExplicitEditScope(route, prompt, mode)
		brief := m.runtimeBriefForRoute(route, prompt)
		return promptExecutionPlan{
			mode:         mode,
			route:        route,
			runtimeBrief: brief,
		}
	}
	route := m.classifyTurnRoute(ctx, prompt, mode)
	route = validateRouteFamilyAgainstExplicitEditScope(route, prompt, mode)
	brief := m.runtimeBriefForRoute(route, prompt)
	return promptExecutionPlan{
		mode:         mode,
		route:        route,
		runtimeBrief: brief,
	}
}

func validateRouteFamilyAgainstExplicitEditScope(route turnRoutePlan, prompt string, mode agent.Mode) turnRoutePlan {
	route = normalizeTurnRoutePlan(route, mode)
	if route.Family != "gdscript_edit" {
		return route
	}
	paths := appendUniqueBriefStrings(nil, requiredEditPathsFromPrompt(prompt)...)
	paths = appendUniqueBriefStrings(paths, allowedEditPathsFromPrompt(prompt)...)
	paths = appendUniqueBriefStrings(paths, pathparse.MentionedWorkspacePaths(prompt)...)
	if len(paths) == 0 {
		return route
	}
	for _, path := range paths {
		if !routePathLooksLikeGDScript(path) {
			route.Family = "implementation"
			route.Reason = "gdscript_scope_mismatch"
			return normalizeTurnRoutePlan(route, mode)
		}
	}
	return route
}

func routePathLooksLikeGDScript(path string) bool {
	return strings.EqualFold(filepath.Ext(strings.TrimSpace(filepath.ToSlash(path))), ".gd")
}

func (m *Model) planExecuteCommandExecutionPlan(objective string) promptExecutionPlan {
	mode := agent.BuildMode()
	route := normalizeTurnRoutePlan(turnRoutePlan{
		Family:      string(workerruntime.TaskFamilyPlanThenExecutePlanner),
		Reason:      "typed_plan_execute_command",
		RouteSource: routeSourceTypedCommand,
	}, mode)
	return promptExecutionPlan{
		mode:         mode,
		route:        route,
		runtimeBrief: m.runtimeBriefForRouteMode(route, mode, objective),
	}
}

func (m *Model) implementCommandExecutionPlan(objective string) promptExecutionPlan {
	mode := agent.BuildMode()
	route := normalizeTurnRoutePlan(turnRoutePlan{
		Family:      "implementation",
		Reason:      "typed_implement_command",
		RouteSource: routeSourceTypedCommand,
	}, mode)
	return promptExecutionPlan{
		mode:         mode,
		route:        route,
		runtimeBrief: m.runtimeBriefForRouteMode(route, mode, objective),
	}
}

func (m *Model) runPlanExecuteCommandOnce(ctx context.Context, objective string) (string, error) {
	objective = strings.TrimSpace(objective)
	if objective == "" {
		return "", fmt.Errorf("plan-execute objective is required")
	}
	m.lastPlanExecuteRunResult = nil
	m.emitJSONStart(objective)
	plan := m.planExecuteCommandExecutionPlan(objective)
	if m.workspace != nil {
		m.workspace.PrimeDiscoveredPaths(pathparse.MentionedWorkspacePaths(objective))
	}
	suppressTranscript := m.suppressPromptTranscript
	if !suppressTranscript {
		m.messages = append(m.messages, llama.ChatMessage{Role: "user", Content: objective})
		m.logMessage("user", objective)
	} else if m.debugLogger != nil {
		_ = m.debugLogger.LogRunEvent("internal_prompt", fmt.Sprintf("suppressed nested plan-execute command (%d chars)", len(objective)))
	}
	if summary := strings.TrimSpace(renderTurnRoutePlan(plan.route)); summary != "" {
		m.logMessage("route_plan", summary)
		m.enqueueHistoryAuditEvent(historyAuditEvent{kind: "lane", label: "route selected", detail: summary, status: plan.route.InitialLane})
	}
	m.emitWriterRouteDecision(m.writerRouteDecisionForPrompt(objective, plan))
	reply, err := m.runImplementationViaPlanExecute(ctx, objective, plan)
	if err != nil {
		m.emitJSONError("run_failed", err.Error())
		return "", err
	}
	return m.finishPromptOnceReply(reply), nil
}

func (m *Model) runImplementCommandOnce(ctx context.Context, objective string) (string, error) {
	objective = strings.TrimSpace(objective)
	if objective == "" {
		return "", fmt.Errorf("implement objective is required")
	}
	m.emitJSONStart(objective)
	if err := m.ensureServerReady(ctx); err != nil {
		m.emitJSONError("server_not_ready", err.Error())
		return "", err
	}
	plan := m.implementCommandExecutionPlan(objective)
	plan = m.recoverDirectImplementationContext(objective, plan)
	restoreRoute := m.applyDirectImplementationPromptRoute(ctx, objective, plan)
	defer restoreRoute()
	chatOpts := tuneChatOptionsForPlan(m.chatOptions(), plan)
	chatOpts = applyMaxTokensOverride(chatOpts, m.maxTokensOverride)
	if m.workspace != nil {
		m.workspace.PrimeDiscoveredPaths(pathparse.MentionedWorkspacePaths(objective))
	}
	suppressTranscript := m.suppressPromptTranscript
	if !suppressTranscript {
		m.messages = append(m.messages, llama.ChatMessage{Role: "user", Content: objective})
		m.logMessage("user", objective)
	} else if m.debugLogger != nil {
		_ = m.debugLogger.LogRunEvent("internal_prompt", fmt.Sprintf("suppressed nested implement command (%d chars)", len(objective)))
	}
	m.logMessage("thinking_config", samplingSummaryForOptions(chatOpts))
	if summary := strings.TrimSpace(renderTurnRoutePlan(plan.route)); summary != "" {
		m.logMessage("route_plan", summary)
		m.enqueueHistoryAuditEvent(historyAuditEvent{kind: "lane", label: "route selected", detail: summary, status: plan.route.InitialLane})
	}
	m.emitWriterRouteDecision(m.writerRouteDecisionForPrompt(objective, plan))
	history := m.requestMessages()
	if suppressTranscript {
		history = append(history, llama.ChatMessage{Role: "user", Content: objective})
	}
	reply, err := m.runAgentTurn(ctx, objective, history, chatOpts, plan)
	if err != nil {
		m.emitJSONError("run_failed", err.Error())
		return "", err
	}
	return m.finishPromptOnceReply(reply), nil
}

func (m *Model) runtimeBriefForRoute(route turnRoutePlan, prompt string) workerruntime.WorkerRuntimeBrief {
	return m.runtimeBriefForRouteMode(route, m.currentMode(), prompt)
}

func (m *Model) runtimeBriefForRouteMode(route turnRoutePlan, mode agent.Mode, prompt string) workerruntime.WorkerRuntimeBrief {
	brief := turnRoutePlanRuntimeBrief(route, mode, m.workspaceAllowedTools())
	if m != nil && m.planExecuteActive && strings.TrimSpace(m.forcedTaskFamily) == "direct_question" {
		brief.PermissionProfile = "conversation_only"
		brief.AllowedTools = nil
		brief.RequiredEvidence = nil
	}
	brief.Objective = strings.TrimSpace(pathparse.ScopeToBriefBody(prompt))
	if brief.Objective == "" {
		brief.Objective = strings.TrimSpace(prompt)
	}
	mentionedPaths := pathparse.MentionedWorkspacePaths(prompt)
	if isImplementationLikeFamily(route.Family) && !workerruntime.TaskFamilyIsPlanThenExecutePlanner(route.Family) {
		brief.EvidencePaths = appendUniqueBriefStrings(brief.EvidencePaths, mentionedPaths...)
	} else {
		brief.TargetRefs = appendUniqueBriefStrings(brief.TargetRefs, mentionedPaths...)
	}
	brief.EvidencePaths = appendUniqueBriefStrings(brief.EvidencePaths, promptContextEvidencePaths(prompt)...)
	brief.TargetPacket = appendUniqueBriefStrings(brief.TargetPacket, promptContextPacketMarkers(prompt)...)
	scopeContract, useTypedPlanExecuteScope := m.planExecuteRuntimeScopeContract(route)
	allowed := allowedEditPathsFromPrompt(prompt)
	required := requiredEditPathsFromPrompt(prompt)
	if useTypedPlanExecuteScope {
		required = scopeContract.RequiredCoveragePaths
		allowed = scopeContract.AllowedCandidatePaths
	}
	implementationLike := isImplementationLikeFamily(route.Family)
	if !implementationLike {
		if len(required) > 0 {
			brief.TargetRefs = appendUniqueBriefStrings(brief.TargetRefs, required...)
		}
		required = nil
	}
	if !useTypedPlanExecuteScope && implementationLike && len(required) == 0 && len(allowed) > 0 && promptHasRequiredEditPayload(prompt) {
		required = appendUniqueBriefStrings(required, requiredEditPayloadPathsFromPrompt(prompt, allowed)...)
	}
	if len(allowed) > 0 {
		brief.AllowedEditPaths = appendUniqueBriefStrings(brief.AllowedEditPaths, allowed...)
		brief.TargetRefs = appendUniqueBriefStrings(brief.TargetRefs, allowed...)
		brief.SearchPolicy = appendUniqueBriefStrings(brief.SearchPolicy,
			"treat allowed_edit_paths as candidate write scope, not required coverage obligations",
		)
	}
	snippetScope := required
	if !useTypedPlanExecuteScope && len(snippetScope) == 0 && implementationLike && len(allowed) > 0 && promptHasRequiredEditPayload(prompt) {
		snippetScope = allowed
	}
	if useTypedPlanExecuteScope && len(snippetScope) == 0 && implementationLike && len(allowed) > 0 {
		brief.RequiredEditSnippets = appendRequiredEditSnippets(brief.RequiredEditSnippets, requiredEditSnippetsFromPrompt(prompt, allowed)...)
	} else if len(snippetScope) > 0 {
		brief.RequiredEditSnippets = appendRequiredEditSnippets(brief.RequiredEditSnippets, requiredEditSnippetsFromPrompt(prompt, snippetScope)...)
	}
	if len(required) > 0 {
		brief.RequiredEditPaths = appendUniqueBriefStrings(brief.RequiredEditPaths, required...)
		brief.Constraints = appendUniqueBriefStrings(brief.Constraints, "required_edit_paths_define_write_scope")
		brief.RequiredEvidence = appendUniqueBriefStrings(brief.RequiredEvidence, "required_edit_paths_covered_or_blocked")
		brief.ReadinessGate = appendUniqueBriefStrings(brief.ReadinessGate, "required_edit_paths_satisfied")
		if workerruntime.TaskFamilyIsPlanThenExecutePlanner(route.Family) {
			brief.RequiredNext = appendUniqueBriefStrings(brief.RequiredNext,
				"for existing required paths, satisfy source-body obligations before submit_plan unless the packet is exact-plan-ready/source-satisfied; then call submit_plan, answer Decision: source-satisfied with per-path evidence, or report <<QUESTION>>",
			)
			brief.SearchPolicy = appendUniqueBriefStrings(brief.SearchPolicy,
				"treat required_edit_paths plus packet evidence as submit_plan scope; packet anchors establish scope but existing paths still need source-body obligations satisfied before submit_plan",
			)
		} else {
			brief.RequiredNext = appendUniqueBriefStrings(brief.RequiredNext,
				"use semantic/context/declared project discovery or narrow reads only for one named edit-safety fact; after that fact is resolved, edit an in-scope required path or report <<QUESTION>>",
			)
			brief.SearchPolicy = appendUniqueBriefStrings(brief.SearchPolicy,
				"treat required_edit_paths plus packet evidence as edit scope; prefer semantic/context/declared project discovery before narrow required-path reads for named edit-safety facts",
			)
		}
		brief.FinalCritic = appendUniqueBriefStrings(brief.FinalCritic, "required_edit_paths_reported")
	}
	if len(brief.RequiredEditSnippets) > 0 {
		brief.RequiredEvidence = appendUniqueBriefStrings(brief.RequiredEvidence, "required_edit_snippets_present_or_blocked")
		brief.ReadinessGate = appendUniqueBriefStrings(brief.ReadinessGate, "required_edit_snippets_satisfied")
		brief.FinalCritic = appendUniqueBriefStrings(brief.FinalCritic, "required_edit_snippets_reported")
	}
	if useTypedPlanExecuteScope && workerruntime.TaskFamilyIsPlanThenExecutePlanner(route.Family) {
		brief = runtimeBriefWithPlanExecuteEditSetObligations(brief, scopeContract.EditSetObligations)
	}
	if len(m.forcedReadinessOpenObligations) > 0 {
		if brief.Readiness == nil {
			brief.Readiness = &workerruntime.ReadinessState{}
		}
		brief.Readiness.OpenObligations = appendUniqueBriefStrings(brief.Readiness.OpenObligations, m.forcedReadinessOpenObligations...)
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
		if workerruntime.TaskFamilyIsPlanThenExecutePlanner(route.Family) {
			brief.InvalidActions = appendUniqueBriefStrings(brief.InvalidActions,
				"source_sanity_in_planner_phase",
			)
			brief.RequiredNext = appendUniqueBriefStrings(brief.RequiredNext,
				"do not run build, test, shell, git, or source_sanity in the plan-execute planner phase; close with submit_plan, Decision: source-satisfied, or <<QUESTION>>",
			)
		} else {
			brief.RequiredNext = appendUniqueBriefStrings(brief.RequiredNext,
				"after source edits run source_sanity; for non-source edits run only cheap non-build sanity checks or report planner-owned verification status",
			)
		}
	}
	if useTypedPlanExecuteScope && isImplementationLikeFamily(route.Family) && len(brief.RequiredEditSnippets) > 0 {
		brief.Constraints = appendUniqueBriefStrings(brief.Constraints, "exact_anchor_implementation")
		brief.RequiredEvidence = appendUniqueBriefStrings(brief.RequiredEvidence, "exact_edit_anchor")
		if workerruntime.TaskFamilyIsPlanThenExecutePlanner(route.Family) {
			brief.SearchPolicy = appendUniqueBriefStrings(brief.SearchPolicy, "preserve_exact_anchor_snippets_in_submit_plan_before_broad_discovery")
			brief.InvalidActions = appendUniqueBriefStrings(brief.InvalidActions, "broad_read_before_exact_anchor_plan_attempt")
		} else {
			brief.SearchPolicy = appendUniqueBriefStrings(brief.SearchPolicy, "attempt_exact_anchor_edit_before_broad_discovery")
			brief.InvalidActions = appendUniqueBriefStrings(brief.InvalidActions, "broad_read_before_first_edit_attempt")
		}
	} else if !useTypedPlanExecuteScope && isImplementationLikeFamily(route.Family) && promptContainsExactEditAnchor(prompt) {
		brief.Constraints = appendUniqueBriefStrings(brief.Constraints, "exact_anchor_implementation")
		brief.RequiredEvidence = appendUniqueBriefStrings(brief.RequiredEvidence, "exact_edit_anchor")
		brief.SearchPolicy = appendUniqueBriefStrings(brief.SearchPolicy, "attempt_exact_anchor_edit_before_broad_discovery")
		brief.InvalidActions = appendUniqueBriefStrings(brief.InvalidActions, "broad_read_before_first_edit_attempt")
	}
	if workerruntime.TaskFamilyIsPlanThenExecutePlanner(route.Family) && !m.workspaceHasGitMetadata() {
		brief = runtimeBriefWithoutGitHistoryRequirement(brief)
	}
	return brief
}

const (
	runtimeBriefGitHistoryEvidence = "git_history_rationale_for_intended_logic"
	runtimeBriefGitHistoryTool     = "history_range_or_history_symbol"
)

func (m *Model) workspaceHasGitMetadata() bool {
	if m == nil || m.workspace == nil {
		return false
	}
	root := strings.TrimSpace(m.workspace.Root())
	if root == "" {
		return false
	}
	if _, err := os.Stat(filepath.Join(root, ".git")); err == nil {
		return true
	}
	return false
}

func runtimeBriefWithoutGitHistoryRequirement(brief workerruntime.WorkerRuntimeBrief) workerruntime.WorkerRuntimeBrief {
	brief.RequiredEvidence = filterBriefStrings(brief.RequiredEvidence, func(value string) bool {
		return strings.TrimSpace(value) != runtimeBriefGitHistoryEvidence
	})
	brief.RequiredTools = filterBriefStrings(brief.RequiredTools, func(value string) bool {
		return strings.TrimSpace(value) != runtimeBriefGitHistoryTool
	})
	brief.SearchPolicy = filterBriefStrings(brief.SearchPolicy, func(value string) bool {
		lower := strings.ToLower(strings.TrimSpace(value))
		return !strings.Contains(lower, "history_required_before_submit_plan") &&
			!strings.Contains(lower, "read_commit_descriptions_for_intended_logic_before_submit_plan")
	})
	return brief
}

func filterBriefStrings(values []string, keep func(string) bool) []string {
	out := values[:0]
	for _, value := range values {
		if keep(value) {
			out = append(out, value)
		}
	}
	return out
}

func runtimeBriefWithPlanExecuteEditSetObligations(brief workerruntime.WorkerRuntimeBrief, obligations planexecute.EditSetObligations) workerruntime.WorkerRuntimeBrief {
	obligations = obligations.Normalized()
	if !runtimeBriefHasNonPromptEditSetObligation(obligations, planexecute.EditSetObligationKindTestEdit) &&
		!runtimeBriefHasNonPromptEditSetObligation(obligations, planexecute.EditSetObligationKindImplementationEdit) {
		return brief
	}
	if runtimeBriefHasNonPromptEditSetObligation(obligations, planexecute.EditSetObligationKindTestEdit) {
		brief.Constraints = appendUniqueBriefStrings(brief.Constraints, "missing_requested_test_edit_recovery")
		brief.RequiredEvidence = appendUniqueBriefStrings(brief.RequiredEvidence, "test_edit_or_test_path_source_satisfied_evidence")
		brief.RequiredNext = appendUniqueBriefStrings(brief.RequiredNext,
			"Missing Requested Test Edit Recovery: terminal outcome must be submit_plan with at least one real test edit, Decision: source-satisfied naming both source and test evidence paths when the final state already exists, or <<QUESTION>> with the exact blocker; do not submit another production-only plan",
		)
	}
	if runtimeBriefHasNonPromptEditSetObligation(obligations, planexecute.EditSetObligationKindImplementationEdit) {
		brief.Constraints = appendUniqueBriefStrings(brief.Constraints, "missing_requested_implementation_edit_recovery")
		brief.RequiredEvidence = appendUniqueBriefStrings(brief.RequiredEvidence, "implementation_edit_or_implementation_path_source_satisfied_evidence")
		brief.RequiredNext = appendUniqueBriefStrings(brief.RequiredNext,
			"Missing Requested Implementation Edit Recovery: terminal outcome must be submit_plan with at least one real non-test implementation edit, Decision: source-satisfied naming implementation and test/source evidence paths when the final state already exists, or <<QUESTION>> with the exact blocker; do not submit another test-only plan",
		)
	}
	return brief
}

func runtimeBriefHasNonPromptEditSetObligation(obligations planexecute.EditSetObligations, kind planexecute.EditSetObligationKind) bool {
	for _, obligation := range obligations.Normalized() {
		if obligation.Kind != kind {
			continue
		}
		source := strings.TrimSpace(obligation.Source)
		return source == "" || source != "prompt"
	}
	return false
}

func (m *Model) planExecuteRuntimeScopeContract(route turnRoutePlan) (planexecute.PlanScopeContract, bool) {
	if m == nil || !m.planExecuteActive || !workerruntime.TaskFamilyIsPlanThenExecutePlanner(route.Family) {
		return planexecute.PlanScopeContract{}, false
	}
	contract := m.planExecuteScopeContract.Normalized()
	return contract, true
}

// RequiredEditPathsFromPrompt is the exported view of the brief-
// parsing logic the runner uses to populate brief.RequiredEditPaths.
// External callers (today: cmd/idiom's `idiom lane postflight`) use
// this so the postflight allowlist check matches exactly what the
// runner saw — diverging implementations would produce different
// allowlists for the same brief, which is a footgun.
func RequiredEditPathsFromPrompt(prompt string) []string {
	return planexecute.RequiredEditPathsFromPrompt(prompt)
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

func requiredEditPayloadPathsFromPrompt(prompt string, candidatePaths []string) []string {
	if len(candidatePaths) == 0 || !promptHasRequiredEditPayload(prompt) {
		return nil
	}
	var paths []string
	for _, snippet := range requiredEditSnippetsFromPrompt(prompt, candidatePaths) {
		if path := strings.TrimSpace(snippet.Path); path != "" {
			paths = appendUniqueBriefStrings(paths, path)
		}
	}
	return paths
}

// AllowedEditPathsFromPrompt returns explicit candidate write-scope paths from
// prompts. Unlike RequiredEditPathsFromPrompt, these paths are not completion
// coverage obligations.
func AllowedEditPathsFromPrompt(prompt string) []string {
	return planexecute.AllowedEditPathsFromPrompt(prompt)
}

func requiredEditPathsFromPrompt(prompt string) []string {
	return planexecute.RequiredEditPathsFromPrompt(prompt)
}

func allowedEditPathsFromPrompt(prompt string) []string {
	return planexecute.AllowedEditPathsFromPrompt(prompt)
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
	if workerruntime.TaskFamilyIsPlanThenExecutePlanner(taskFamily) {
		opts = tuneChatOptionsForDeterministicState(opts, 0)
		if !opts.ModelThinkingFloor {
			thinkingOff := false
			opts.EnableThinking = &thinkingOff
			opts.PreserveThinking = false
			opts.ChatTemplateKwargs = true
		}
	}
	readOnly := profile == "conversation_only" || profile == "read_only_workspace" || profile == "read_only_verification_shell"
	if readOnly && !opts.ModelThinkingFloor {
		// direct_answer (conversation lane) preserves thinking so the model
		// maintains context across turns; other read-only contracts
		// still disable it to reduce token overhead on focused probes.
		if contract != "direct_answer" {
			thinkingOff := false
			opts.EnableThinking = &thinkingOff
			opts.PreserveThinking = false
		}
		opts.ChatTemplateKwargs = true
	}
	if readOnly {
		opts = tuneChatOptionsForDeterministicState(opts, 0.2)
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
			opts.MaxTokens = clampPositiveMin(opts.MaxTokens, 3072)
		}
	case "grounded_explanation", "locate_targets":
		opts.MaxTokens = clampPositiveMin(opts.MaxTokens, 1536)
	case "review_findings", "architecture_assessment":
		opts.MaxTokens = clampPositiveMin(opts.MaxTokens, 2048)
	}
	return opts
}

func tuneChatOptionsForDeterministicState(opts llama.ChatOptions, maxTemperature float64) llama.ChatOptions {
	if maxTemperature < 0 {
		maxTemperature = 0
	}
	if opts.Temperature > maxTemperature {
		opts.Temperature = maxTemperature
	}
	opts.TopP = 1
	return opts
}

func applyMaxTokensOverride(opts llama.ChatOptions, maxTokens int) llama.ChatOptions {
	if maxTokens > 0 {
		opts.MaxTokens = maxTokens
	}
	return opts
}

func clampPositiveMin(current, limit int) int {
	return max(current, limit)
}
