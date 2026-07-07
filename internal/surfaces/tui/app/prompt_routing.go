package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/autonomy"
	"idiom/internal/contracts/pathparse"
	plancontract "idiom/internal/contracts/plan"
	"idiom/internal/platform/textutil"
	"idiom/internal/runtime/agent"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/modepolicy"
	"idiom/internal/runtime/workerruntime"
	"idiom/internal/workflows/planexecute"
	planexecuteadapters "idiom/internal/workflows/planexecute/adapters"
)

// promptExecutionPlan is the minimal OpenCode-aligned carrier for a turn.
// All the classification fields from the prior lane-based shape are gone;
// the Mode the user picked is the only routing signal the harness cares
// about.
type promptExecutionPlan struct {
	mode         modepolicy.Mode
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

	if objective, ok := typedPromptCommandObjective(prompt, "/feature-planner"); ok {
		return m.runFeaturePlannerCommandOnce(ctx, objective)
	}

	if m.directPlanExecuteApprovalShouldConsumeOperatorInput(prompt) {
		return m.runDirectPlanExecuteApprovalAnswerOnce(prompt)
	}

	m.emitJSONStart(prompt)

	if err := m.ensureServerReady(ctx); err != nil {
		m.emitJSONError("server_not_ready", err.Error())

		return "", err
	}

	if m.directFeaturePlannerShouldConsumeOperatorInput(prompt) {
		return m.runDirectFeaturePlannerAnswerOnce(ctx, prompt)
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
	if !m.suppressPromptTranscript {
		m.thoughts.reset()
	}

	plan := m.buildPromptExecutionPlan(ctx, prompt)

	plan = m.recoverDirectImplementationContext(prompt, plan)
	if m.greenfieldCreationRequiresConfirmation(prompt, plan) {
		m.recordPromptRouteWithoutExecution(prompt, plan)

		return m.finishPromptOnceReply(greenfieldCreationConfirmationQuestion(plan)), nil
	}

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

	history := m.requestMessages()
	if suppressTranscript {
		history = append(history, llama.ChatMessage{Role: "user", Content: prompt})
	}
	conversationHistory := m.conversationFollowupHistory(prompt, suppressTranscript)
	if activePlanContext := m.activeFeaturePlannerPlanContext(); activePlanContext != "" && (plan.route.Family == string(autonomy.TaskFamilyConversation) || plan.route.Contract == string(workerruntime.ContractDirectAnswer)) {
		history = append(history, llama.ChatMessage{Role: "system", Content: activePlanContext})
		m.logMessage("active_feature_planner_plan_context", activePlanContext)
	}

	plan = promptExecutionPlanWithConversationContext(prompt, conversationHistory, plan, m.projectRoot())

	m.logMessage("thinking_config", samplingSummaryForOptions(chatOpts))

	if summary := strings.TrimSpace(renderTurnRoutePlan(plan.route)); summary != "" {
		m.logMessage("route_plan", summary)
		m.enqueueHistoryAuditEvent(historyAuditEvent{kind: "lane", label: "route selected", detail: summary, status: plan.route.InitialLane})
	}

	m.emitWriterRouteDecision(m.writerRouteDecisionForPrompt(prompt, plan))

	reply, err := m.runAgentTurn(ctx, prompt, history, chatOpts, plan)
	if err != nil {
		m.emitJSONError("run_failed", err.Error())

		return "", err
	}

	reply = m.applyLaneSwitchReferralIfAny(prompt, reply)

	return m.finishPromptOnceReply(reply), nil
}

// applyLaneSwitchReferralIfAny scans the assistant reply for lane-refusal
// language and surfaces the lane-switch affordance when matched. In TUI mode
// it opens the interactive popup (the reply itself is preserved in the
// transcript); in headless mode it appends a numbered text block to the
// reply so the JSON event stream consumer sees both the refusal and the
// re-dispatch options. Returns the (possibly augmented) reply.
//
// Detection is conservative and sniffs the MODEL'S OWN refusal language,
// never the user's prompt — see laneSwitchReferralPatterns.
func (m *Model) applyLaneSwitchReferralIfAny(prompt, reply string) string {
	recommended, ok := laneSwitchReferralFromReply(reply)
	if !ok {
		return reply
	}

	if m.isHeadless() {
		return strings.TrimSpace(reply) + "\n\n" + laneSwitchHeadlessReply(prompt, recommended)
	}

	m.openLaneSwitchPopup(prompt, recommended)

	return reply
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

func (m *Model) applyDirectImplementationPromptRoute(ctx context.Context, prompt string, plan promptExecutionPlan) func() {
	if m == nil {
		return func() {}
	}

	if !workerruntime.TaskFamilyMutationCapable(plan.route.Family) {
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

	section := planexecute.BuildPromptContextPackSectionWithRunner(m.cfg, planexecute.PromptContextPackOptions{
		Mode:        "implement",
		Title:       prompt,
		Brief:       prompt,
		ProjectRoot: m.projectRoot(),
		Limit:       8,
		Timeout:     directImplementContextPreloadTimeout,
	}, &diagnostics, planexecuteadapters.Dependencies().RunPromptContextTool)

	if diag := strings.TrimSpace(diagnostics.String()); diag != "" {
		m.logMessage("direct_context_recovery", textutil.TruncateWithByteCount(diag, 700))
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

	if !workerruntime.TaskFamilyMutationCapable(plan.route.Family) || isPlanExecuteInternalFamily(plan.route.Family) {
		return false
	}

	if len(plan.runtimeBrief.TargetPacket) > 0 || len(plan.runtimeBrief.RequiredEditPaths) > 0 || len(plan.runtimeBrief.AllowedEditPaths) > 0 {
		return false
	}

	if len(pathparse.MentionedWorkspacePaths(prompt)) > 0 {
		return false
	}

	if conversationAlreadyCarriesSubstantiveContext(m.messages) {
		return false
	}

	return strings.TrimSpace(m.cfg.Chat.ImplementationDispatch) == "plan_execute"
}

// directImplementContextPreloadTimeout bounds the optional context_pack
// preload run before a direct implementation turn. The preload is an
// optimization that falls back gracefully when skipped or stalled, so it
// must never block the whole turn on a stalled query. A fresh, healthy
// context_pack completes in ~1-2s; this budget gives generous headroom for
// cold caches and language-backend startup while still failing fast when the
// in-process pack is contending for a resource (the observed failure mode
// was a full 30s tool-timeout stall every turn).
const directImplementContextPreloadTimeout = 8 * time.Second

// directImplementContextPreloadPriorAssistantMinLen is the minimum content
// length of a prior assistant turn for the conversation to count as already
// carrying substantive context. Tuned to exclude trivial ack/error replies
// while capturing any real prior answer or synthesized deliverable.
const directImplementContextPreloadPriorAssistantMinLen = 400

// conversationAlreadyCarriesSubstantiveContext reports whether the live
// transcript holds a prior assistant turn with non-trivial content. When that
// exists, the model already has conversational workspace context and can
// self-localize via read/search tools, so the optional context_pack preload
// is redundant. Compacted-away history no longer contains such a turn, so the
// predicate naturally falls back to preloading after aggressive compaction.
func conversationAlreadyCarriesSubstantiveContext(messages []llama.ChatMessage) bool {
	for _, msg := range messages {
		if strings.EqualFold(strings.TrimSpace(msg.Role), "assistant") &&
			len(strings.TrimSpace(msg.Content)) >= directImplementContextPreloadPriorAssistantMinLen {
			return true
		}
	}
	return false
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
func (m *Model) currentMode() modepolicy.Mode {
	if strings.TrimSpace(m.agentModeName) != "" {
		return modepolicy.ModeOrDefault(m.agentModeName)
	}

	return modepolicy.ModeOrDefault(m.cfg.Chat.AgentMode)
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

func validateRouteFamilyAgainstExplicitEditScope(route turnRoutePlan, prompt string, mode modepolicy.Mode) turnRoutePlan {
	route = normalizeTurnRoutePlan(route, mode)
	if route.Family != "gdscript_edit" {
		return route
	}

	paths := appendUniqueBriefStrings(nil, plancontract.RequiredEditPathsFromPrompt(prompt)...)

	paths = appendUniqueBriefStrings(paths, plancontract.AllowedEditPathsFromPrompt(prompt)...)
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
	mode := modepolicy.BuildMode()
	route := normalizeTurnRoutePlan(turnRoutePlan{
		Family:      string(autonomy.TaskFamilyPlanThenExecutePlanner),
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
	mode := modepolicy.BuildMode()
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

	if !m.suppressPromptTranscript {
		m.thoughts.reset()
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

	history := m.requestMessages()
	if suppressTranscript {
		history = append(history, llama.ChatMessage{Role: "user", Content: objective})
	}

	plan = promptExecutionPlanWithConversationContext(objective, m.conversationFollowupHistory(objective, suppressTranscript), plan, m.projectRoot())

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

	if !m.suppressPromptTranscript {
		m.thoughts.reset()
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

	history := m.requestMessages()
	if suppressTranscript {
		history = append(history, llama.ChatMessage{Role: "user", Content: objective})
	}

	plan = promptExecutionPlanWithConversationContext(objective, m.conversationFollowupHistory(objective, suppressTranscript), plan, m.projectRoot())

	m.logMessage("thinking_config", samplingSummaryForOptions(chatOpts))

	if summary := strings.TrimSpace(renderTurnRoutePlan(plan.route)); summary != "" {
		m.logMessage("route_plan", summary)
		m.enqueueHistoryAuditEvent(historyAuditEvent{kind: "lane", label: "route selected", detail: summary, status: plan.route.InitialLane})
	}

	m.emitWriterRouteDecision(m.writerRouteDecisionForPrompt(objective, plan))

	reply, err := m.runAgentTurn(ctx, objective, history, chatOpts, plan)
	if err != nil {
		m.emitJSONError("run_failed", err.Error())

		return "", err
	}

	return m.finishPromptOnceReply(reply), nil
}

func (m *Model) conversationFollowupHistory(currentPrompt string, suppressTranscript bool) []llama.ChatMessage {
	if m == nil {
		return nil
	}

	history := m.messages
	if m.requestHistoryOverride != nil {
		history = *m.requestHistoryOverride
	}

	history = append([]llama.ChatMessage(nil), history...)
	if suppressTranscript {
		history = append(history, llama.ChatMessage{Role: "user", Content: currentPrompt})
	}

	return modelPayloadHistory(history)
}

func (m *Model) runtimeBriefForRoute(route turnRoutePlan, prompt string) workerruntime.WorkerRuntimeBrief {
	return m.runtimeBriefForRouteMode(route, m.currentMode(), prompt)
}

func (m *Model) runtimeBriefForRouteMode(route turnRoutePlan, mode modepolicy.Mode, prompt string) workerruntime.WorkerRuntimeBrief {
	route = normalizeTurnRoutePlan(route, mode)
	brief := turnRoutePlanRuntimeBrief(route, mode, m.workspaceAllowedToolsForTaskFamily(route.Family))
	if m != nil && m.planExecuteActive && strings.TrimSpace(m.forcedTaskFamily) == string(autonomy.TaskFamilyDirectQuestion) {
		brief.PermissionProfile = string(workerruntime.PermissionConversationOnly)
		brief.AllowedTools = nil
		brief.RequiredEvidence = nil
	}

	brief.Objective = strings.TrimSpace(pathparse.ScopeToBriefBody(prompt))
	if brief.Objective == "" {
		brief.Objective = strings.TrimSpace(prompt)
	}

	mentionedPaths := pathparse.MentionedWorkspacePaths(prompt)
	if workerruntime.TaskFamilyMutationCapable(route.Family) && !workerruntime.TaskFamilyIsPlanThenExecutePlanner(route.Family) {
		brief.EvidencePaths = appendUniqueBriefStrings(brief.EvidencePaths, mentionedPaths...)
	} else {
		brief.TargetRefs = appendUniqueBriefStrings(brief.TargetRefs, mentionedPaths...)
	}

	promptPacket := plancontract.PromptPacketFromPrompt(prompt)

	brief.EvidencePaths = appendUniqueBriefStrings(brief.EvidencePaths, promptPacket.EvidencePaths...)
	brief.TargetPacket = appendUniqueBriefStrings(brief.TargetPacket, promptPacket.TargetPacketMarkers...)

	scopeContract, useTypedPlanExecuteScope := m.planExecuteRuntimeScopeContract(route)
	allowed := promptPacket.AllowedEditPaths
	required := promptPacket.RequiredEditPaths

	if useTypedPlanExecuteScope {
		required = scopeContract.RequiredCoveragePaths
		allowed = scopeContract.AllowedCandidatePaths
	}

	implementationLike := workerruntime.TaskFamilyMutationCapable(route.Family)
	if !implementationLike {
		if len(required) > 0 {
			brief.TargetRefs = appendUniqueBriefStrings(brief.TargetRefs, required...)
		}

		required = nil
	}

	if !useTypedPlanExecuteScope && implementationLike && len(required) == 0 && len(allowed) > 0 && promptPacket.HasRequiredEditPayload {
		required = appendUniqueBriefStrings(required, requiredEditPayloadPathsFromPrompt(prompt, allowed, promptPacket.HasRequiredEditPayload)...)
	}

	if len(allowed) > 0 {
		brief.AllowedEditPaths = appendUniqueBriefStrings(brief.AllowedEditPaths, allowed...)
		brief.TargetRefs = appendUniqueBriefStrings(brief.TargetRefs, allowed...)
		brief.SearchPolicy = appendUniqueBriefStrings(brief.SearchPolicy,
			"treat allowed_edit_paths as candidate write scope, not required coverage obligations",
		)
	}

	if useTypedPlanExecuteScope && len(m.planExecuteAuthorizedNewFiles) > 0 {
		brief.AuthorizedNewFiles = appendUniqueBriefStrings(brief.AuthorizedNewFiles, m.planExecuteAuthorizedNewFiles...)
	}

	snippetScope := required
	if !useTypedPlanExecuteScope && len(snippetScope) == 0 && implementationLike && len(allowed) > 0 && promptPacket.HasRequiredEditPayload {
		snippetScope = allowed
	}

	if useTypedPlanExecuteScope && len(snippetScope) == 0 && implementationLike && len(allowed) > 0 {
		brief.RequiredEditSnippets = appendRequiredEditSnippets(brief.RequiredEditSnippets, plancontract.RequiredEditSnippetsFromPrompt(prompt, allowed)...)
	} else if len(snippetScope) > 0 {
		brief.RequiredEditSnippets = appendRequiredEditSnippets(brief.RequiredEditSnippets, plancontract.RequiredEditSnippetsFromPrompt(prompt, snippetScope)...)
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

	// Forced readiness obligations from implementation dispatch must not
	// leak into conversation follow-up turns. Conversation routes handle
	// their own readiness state from the runtime brief and tool results.
	if len(m.forcedReadinessOpenObligations) > 0 && !workerruntime.TaskFamilyIsConversation(route.Family) && !workerruntime.RuntimeContractIsDirectAnswer(route.Contract) {
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

	if workerruntime.TaskFamilyEvidenceFirstMutation(route.Family) && len(brief.TargetRefs) > 0 {
		brief.RequiredNext = appendUniqueBriefStrings(brief.RequiredNext,
			"gather source evidence before large reads of named target refs",
		)
		brief.InvalidActions = appendUniqueBriefStrings(brief.InvalidActions,
			"large read_file on named target refs before source evidence",
		)
	}

	if m.plannerOwnedVerification && workerruntime.TaskFamilyMutationCapable(route.Family) {
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

	if useTypedPlanExecuteScope && workerruntime.TaskFamilyMutationCapable(route.Family) && len(brief.RequiredEditSnippets) > 0 {
		brief.Constraints = appendUniqueBriefStrings(brief.Constraints, "exact_anchor_implementation")
		brief.RequiredEvidence = appendUniqueBriefStrings(brief.RequiredEvidence, "exact_edit_anchor")

		if workerruntime.TaskFamilyIsPlanThenExecutePlanner(route.Family) {
			brief.SearchPolicy = appendUniqueBriefStrings(brief.SearchPolicy, "preserve_exact_anchor_snippets_in_submit_plan_before_broad_discovery")
			brief.InvalidActions = appendUniqueBriefStrings(brief.InvalidActions, "broad_read_before_exact_anchor_plan_attempt")
		} else {
			brief.SearchPolicy = appendUniqueBriefStrings(brief.SearchPolicy, "attempt_exact_anchor_edit_before_broad_discovery")
			brief.InvalidActions = appendUniqueBriefStrings(brief.InvalidActions, "broad_read_before_first_edit_attempt")
		}
	} else if !useTypedPlanExecuteScope && workerruntime.TaskFamilyMutationCapable(route.Family) && promptPacket.HasExactEditAnchor {
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
		return strings.TrimSpace(value) != string(workerruntime.RequiredEvidenceGitHistoryRationale)
	})
	brief.RequiredTools = filterBriefStrings(brief.RequiredTools, func(value string) bool {
		return strings.TrimSpace(value) != workspace.RequiredToolGitHistoryLookup
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

func runtimeBriefWithPlanExecuteEditSetObligations(brief workerruntime.WorkerRuntimeBrief, obligations plancontract.EditSetObligations) workerruntime.WorkerRuntimeBrief {
	obligations = obligations.Normalized()
	if !runtimeBriefHasNonPromptEditSetObligation(obligations, plancontract.EditSetObligationKindTestEdit) &&
		!runtimeBriefHasNonPromptEditSetObligation(obligations, plancontract.EditSetObligationKindImplementationEdit) {

		return brief
	}

	if runtimeBriefHasNonPromptEditSetObligation(obligations, plancontract.EditSetObligationKindTestEdit) {
		brief.Constraints = appendUniqueBriefStrings(brief.Constraints, "missing_requested_test_edit_recovery")
		brief.RequiredEvidence = appendUniqueBriefStrings(brief.RequiredEvidence, "test_edit_or_test_path_source_satisfied_evidence")
		brief.RequiredNext = appendUniqueBriefStrings(brief.RequiredNext,
			"Missing Requested Test Edit Recovery: terminal outcome must be submit_plan with at least one real test edit, Decision: source-satisfied naming both source and test evidence paths when the final state already exists, or <<QUESTION>> with the exact blocker; do not submit another production-only plan",
		)
	}

	if runtimeBriefHasNonPromptEditSetObligation(obligations, plancontract.EditSetObligationKindImplementationEdit) {
		brief.Constraints = appendUniqueBriefStrings(brief.Constraints, "missing_requested_implementation_edit_recovery")
		brief.RequiredEvidence = appendUniqueBriefStrings(brief.RequiredEvidence, "implementation_edit_or_implementation_path_source_satisfied_evidence")
		brief.RequiredNext = appendUniqueBriefStrings(brief.RequiredNext,
			"Missing Requested Implementation Edit Recovery: terminal outcome must be submit_plan with at least one real non-test implementation edit, Decision: source-satisfied naming implementation and test/source evidence paths when the final state already exists, or <<QUESTION>> with the exact blocker; do not submit another test-only plan",
		)
	}

	return brief
}

func runtimeBriefHasNonPromptEditSetObligation(obligations plancontract.EditSetObligations, kind plancontract.EditSetObligationKind) bool {
	for _, obligation := range obligations.Normalized() {
		if obligation.Kind != kind {
			continue
		}

		source := strings.TrimSpace(obligation.Source)

		return source == "" || source != "prompt"
	}

	return false
}

func (m *Model) planExecuteRuntimeScopeContract(route turnRoutePlan) (plancontract.PlanScopeContract, bool) {
	if m == nil || !m.planExecuteActive || !workerruntime.TaskFamilyIsPlanThenExecutePlanner(route.Family) {
		return plancontract.PlanScopeContract{}, false
	}

	contract := m.planExecuteScopeContract.Normalized()

	return contract, true
}

func requiredEditPayloadPathsFromPrompt(prompt string, candidatePaths []string, hasRequiredEditPayload bool) []string {
	if len(candidatePaths) == 0 || !hasRequiredEditPayload {
		return nil
	}

	var paths []string

	for _, snippet := range plancontract.RequiredEditSnippetsFromPrompt(prompt, candidatePaths) {
		if path := strings.TrimSpace(snippet.Path); path != "" {
			paths = appendUniqueBriefStrings(paths, path)
		}
	}

	return paths
}

func appendRequiredEditSnippets(dst []plancontract.RequiredEditSnippet, values ...plancontract.RequiredEditSnippet) []plancontract.RequiredEditSnippet {
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
		dst = textutil.AppendUnique(dst, v)
	}

	return dst
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

	readOnly := profile == string(workerruntime.PermissionConversationOnly) || profile == string(workerruntime.PermissionReadOnlyWorkspace) || profile == string(workerruntime.PermissionReadOnlyVerificationShell)
	if readOnly && !opts.ModelThinkingFloor {
		// direct_answer (conversation lane) preserves thinking so the model
		// maintains context across turns; other read-only contracts
		// still disable it to reduce token overhead on focused probes.
		if contract != string(workerruntime.ContractDirectAnswer) {
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
	case string(workerruntime.ContractDirectAnswer):
		if opts.ModelThinkingFloor {
			opts.MaxTokens = max(opts.MaxTokens, 4096)
		} else {
			opts.MaxTokens = max(opts.MaxTokens, 3072)
		}
	case "grounded_explanation", "locate_targets":
		opts.MaxTokens = max(opts.MaxTokens, 1536)
	case "review_findings", "architecture_assessment":
		opts.MaxTokens = max(opts.MaxTokens, 2048)
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

func (m *Model) greenfieldCreationRequiresConfirmation(prompt string, plan promptExecutionPlan) bool {
	if m == nil || m.workspace == nil || m.suppressGreenfieldConfirmation {
		return false
	}

	// Only applies when the classifier chose read-only conversation for a
	// concrete creation request. Empty workspaces are still valid places to ask
	// design, architecture, or general questions.
	route := plan.route
	if route.WriteIntent == "yes" || workerruntime.TaskFamilyMutationCapable(route.Family) {
		return false
	}

	if !promptRequestsConcreteWorkspaceCreation(prompt) {
		return false
	}

	// Check workspace is greenfield (no readable project files found). The
	// confirmation protects ambiguous creation/scaffolding requests from being
	// answered in a read-only lane when there is no existing project evidence.
	return !hasReadableWorkspaceFiles(m.workspace.Root())
}

const (
	workspacePresenceProbeMaxEntries = 2000
	workspacePresenceProbeMaxDepth   = 6
)

var (
	errStopWorkspacePresenceProbe = errors.New("stop workspace presence probe")

	workspacePresenceIgnoredDirs = map[string]struct{}{
		".cache":       {},
		".codex":       {},
		".direnv":      {},
		".git":         {},
		".idiom":       {},
		"build":        {},
		"dist":         {},
		"logs":         {},
		"node_modules": {},
		"target":       {},
		"transcripts":  {},
		"vendor":       {},
	}

	workspacePresenceFileExtensions = map[string]struct{}{
		".adoc":    {},
		".bash":    {},
		".c":       {},
		".cc":      {},
		".cpp":     {},
		".css":     {},
		".gd":      {},
		".go":      {},
		".graphql": {},
		".h":       {},
		".hpp":     {},
		".html":    {},
		".java":    {},
		".js":      {},
		".json":    {},
		".jsx":     {},
		".kt":      {},
		".kts":     {},
		".lock":    {},
		".md":      {},
		".mdx":     {},
		".mod":     {},
		".proto":   {},
		".py":      {},
		".rs":      {},
		".rst":     {},
		".sh":      {},
		".sql":     {},
		".sum":     {},
		".toml":    {},
		".ts":      {},
		".tsx":     {},
		".txt":     {},
		".xml":     {},
		".yaml":    {},
		".yml":     {},
		".zsh":     {},
	}

	workspacePresenceBasenames = map[string]struct{}{
		".dockerignore":     {},
		".editorconfig":     {},
		".env.example":      {},
		".gitignore":        {},
		"agents.md":         {},
		"build.gradle":      {},
		"build.gradle.kts":  {},
		"cargo.lock":        {},
		"cargo.toml":        {},
		"dockerfile":        {},
		"go.mod":            {},
		"go.sum":            {},
		"makefile":          {},
		"package-lock.json": {},
		"package.json":      {},
		"pom.xml":           {},
		"pyproject.toml":    {},
		"readme":            {},
		"readme.md":         {},
		"requirements.txt":  {},
	}
)

func hasReadableWorkspaceFiles(root string) bool {
	root = filepath.Clean(root)

	found := false
	inspectedFiles := 0
	budgetExceeded := false
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			if path == root {
				return walkErr
			}

			if entry != nil && entry.IsDir() {
				return filepath.SkipDir
			}

			return nil
		}

		if path == root {
			return nil
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}

		depth := workspacePresencePathDepth(rel)
		if entry.IsDir() {
			if workspacePresenceIgnoredDir(entry.Name()) || depth >= workspacePresenceProbeMaxDepth {
				return filepath.SkipDir
			}

			return nil
		}

		inspectedFiles++
		if inspectedFiles > workspacePresenceProbeMaxEntries {
			budgetExceeded = true
			return errStopWorkspacePresenceProbe
		}

		if workspacePresenceReadableFile(entry.Name()) {
			found = true
			return errStopWorkspacePresenceProbe
		}

		return nil
	})

	if found || budgetExceeded {
		return true
	}

	return err == errStopWorkspacePresenceProbe
}

func workspacePresencePathDepth(rel string) int {
	rel = strings.Trim(filepath.ToSlash(rel), "/")
	if rel == "" || rel == "." {
		return 0
	}

	return strings.Count(rel, "/") + 1
}

func workspacePresenceIgnoredDir(name string) bool {
	_, ignored := workspacePresenceIgnoredDirs[strings.ToLower(name)]
	return ignored
}

func workspacePresenceReadableFile(name string) bool {
	name = strings.ToLower(name)
	if _, ok := workspacePresenceBasenames[name]; ok {
		return true
	}

	_, ok := workspacePresenceFileExtensions[filepath.Ext(name)]

	return ok
}
