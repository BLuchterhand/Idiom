package app

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"idiom/internal/agent"
	"idiom/internal/frontier/pathparse"
	"idiom/internal/frontier/workerruntime"
	"idiom/internal/llama"
	"idiom/internal/util"
)

type turnRoutePlan struct {
	InitialLane  string `json:"initial_lane,omitempty"`
	FollowupLane string `json:"followup_lane,omitempty"`
	Reason       string `json:"reason,omitempty"`
	Contract     string `json:"contract,omitempty"`
	Family       string `json:"family,omitempty"`
	Thoroughness string `json:"thoroughness,omitempty"`
}

func (m *Model) classifyTurnRoute(ctx context.Context, prompt string, mode agent.Mode) turnRoutePlan {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return fallbackTurnRoute(prompt, mode)
	}
	fallback := fallbackTurnRoute(prompt, mode)
	if plan, ok := deterministicHardTurnRoute(prompt, fallback, mode); ok {
		return plan
	}
	if planned, ok := m.classifyTurnRouteWithModel(ctx, prompt, mode); ok {
		planned = correctTurnRoutePlanForPrompt(planned, prompt, mode)
		if shouldPreferFallbackRoute(fallback, planned, prompt) {
			return fallback
		}
		return planned
	}
	return fallback
}

func deterministicHardTurnRoute(prompt string, fallback turnRoutePlan, mode agent.Mode) (turnRoutePlan, bool) {
	words := plannerWordText(prompt)
	if (hasImplementationWorkflowContract(prompt, words) || hasPlanExecuteSourceSatisfiedWorkflowContract(prompt, words)) && (hasEffectiveEditIntent(words) || hasPlanExecuteSourceSatisfiedWorkflowContract(prompt, words)) && !isExplanationOnlyCodeQuestion(words) {
		return normalizeTurnRoutePlan(turnRoutePlan{
			Family: "implementation",
			Reason: "workflow_contract",
		}, mode), true
	}
	if deterministicTurnRouteIsDecisive(fallback, prompt, mode) {
		return fallback, true
	}
	return turnRoutePlan{}, false
}

func deterministicTurnRouteIsDecisive(plan turnRoutePlan, prompt string, mode agent.Mode) bool {
	plan = normalizeTurnRoutePlan(plan, mode)
	family := strings.TrimSpace(plan.Family)
	if family == "" || family == "direct_question" || family == "multi_turn_state" || isImplementationLikeFamily(family) {
		return false
	}
	words := plannerWordText(prompt)
	if isAnalysisContinuationPrompt(words) {
		return false
	}
	switch family {
	case "debug_logs", "verification_only", "locate":
		return true
	default:
		return false
	}
}

func (m *Model) classifyTurnRouteWithModel(ctx context.Context, prompt string, mode agent.Mode) (turnRoutePlan, bool) {
	if m == nil {
		return turnRoutePlan{}, false
	}
	if !m.cfg.RouteClassifier.Enabled {
		return turnRoutePlan{}, false
	}
	history := renderRecentConversationForRouting(m.messages, 6)
	messages := []llama.ChatMessage{
		{
			Role: "system",
			Content: strings.Join([]string{
				"You classify coding-agent turns into one workflow family before execution.",
				"Return strict JSON only.",
				"Choose the best family from this enum:",
				"direct_question, debug_logs, verification_only, locate, locate_then_implement, explore_then_implement, investigate_then_implement, review_then_implement, security_review, performance_review, architecture, architecture_then_implement, implementation, test_authoring, config_edit, script_edit, gdscript_edit, docs_edit, prompt_edit_security, failure_injection, multi_turn_state.",
				"Also return initial_lane, optional followup_lane, reason, contract, and optional thoroughness.",
				"Use coarse OpenCode-shaped routing: read-only investigation, verification-only, locate, architecture/review analysis, or implementation. The loop will enforce permissions later.",
				"Prefer debug_logs for requests about recent logs or explaining a failure from logs.",
				"Prefer direct_question only for questions answerable from conversation alone. If the user names a project feature, mode, runner, tool, or behavior and asks what it does, prefer architecture so the answer is grounded in code.",
				"Prefer verification_only for requests to run checks/tests and report results without editing.",
				"Prefer locate for finding where something is, and locate_then_implement when the user wants to find then change it.",
				"Prefer implementation-like families when the user is asking for file changes, tests, docs, config, scripts, prompts, or repairs.",
				"Prefer gdscript_edit for edits limited to .gd GDScript files.",
				"Prefer architecture for explanation-only questions about how a repo, harness, runner, tool, agent, workflow, or component works. Do not choose an implementation family unless the user is asking for edits.",
				"If the prompt is a continuation like 'continue investigating', 'go deeper', or 'be more confident', keep the turn read-only unless the user now asks for edits.",
				"Prefer performance_review, security_review, or architecture when the user asks for that style of analysis without edits.",
				"Use contracts from this enum: direct_answer, grounded_explanation, locate_targets, review_findings, architecture_assessment, implementation_result.",
				"Use lanes from this enum: main, explore, verification, locate, review, architecture, implement.",
			}, "\n"),
		},
		{
			Role:    "user",
			Content: fmt.Sprintf("Mode: %s\nRecent conversation:\n%s\nCurrent user prompt:\n%s\n\nReturn JSON now.", mode.Name, history, prompt),
		},
	}
	timeout := 10 * time.Second
	if m.cfg.RouteClassifier.TimeoutSeconds > 0 {
		timeout = time.Duration(m.cfg.RouteClassifier.TimeoutSeconds) * time.Second
	}
	classifyCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	thinkingOff := false
	opts := llama.ChatOptions{
		MaxTokens:          256,
		Temperature:        0.1,
		EnableThinking:     &thinkingOff,
		ChatTemplateKwargs: true,
	}
	client := m.routeClassifierChatClient()
	if client == nil {
		return turnRoutePlan{}, false
	}
	text, err := client.CompleteChat(classifyCtx, messages, opts)
	if err != nil && client != m.client && m.client != nil {
		text, err = m.client.CompleteChat(classifyCtx, messages, opts)
	}
	if err != nil {
		return turnRoutePlan{}, false
	}
	plan, err := parseTurnRoutePlan(text)
	if err != nil {
		return turnRoutePlan{}, false
	}
	plan = normalizeTurnRoutePlan(plan, mode)
	return plan, plan.Family != ""
}

func (m *Model) routeClassifierChatClient() llama.LLMClient {
	if m == nil {
		return nil
	}
	if m.summaryClient == nil || m.summaryClient == m.client {
		return m.client
	}
	pingCtx, cancel := context.WithTimeout(context.Background(), 750*time.Millisecond)
	_, err := m.summaryClient.ListModels(pingCtx)
	cancel()
	if err == nil {
		return m.summaryClient
	}
	m.logError("route_classifier", "unreachable; falling back to active planner model: "+err.Error())
	return m.client
}

func parseTurnRoutePlan(text string) (turnRoutePlan, error) {
	text = strings.TrimSpace(text)
	text = strings.TrimPrefix(text, "```json")
	text = strings.TrimPrefix(text, "```")
	text = strings.TrimSuffix(text, "```")
	text = strings.TrimSpace(text)
	start := strings.IndexByte(text, '{')
	end := strings.LastIndexByte(text, '}')
	if start >= 0 && end > start {
		text = text[start : end+1]
	}
	var plan turnRoutePlan
	if err := json.Unmarshal([]byte(text), &plan); err != nil {
		return turnRoutePlan{}, err
	}
	return plan, nil
}

func renderRecentConversationForRouting(messages []llama.ChatMessage, max int) string {
	if max <= 0 {
		max = 6
	}
	var filtered []llama.ChatMessage
	for _, msg := range messages {
		role := strings.TrimSpace(strings.ToLower(msg.Role))
		if role != "user" && role != "assistant" {
			continue
		}
		content := strings.TrimSpace(msg.Content)
		if content == "" {
			continue
		}
		filtered = append(filtered, llama.ChatMessage{Role: role, Content: content})
	}
	if len(filtered) > max {
		filtered = filtered[len(filtered)-max:]
	}
	if len(filtered) == 0 {
		return "(none)"
	}
	var b strings.Builder
	for _, msg := range filtered {
		content := collapseWhitespace(msg.Content)
		if len(content) > 240 {
			content = content[:240] + "..."
		}
		fmt.Fprintf(&b, "[%s] %s\n", msg.Role, content)
	}
	return strings.TrimSpace(b.String())
}

func normalizeTurnRoutePlan(plan turnRoutePlan, mode agent.Mode) turnRoutePlan {
	plan.InitialLane = normalizeRouteValue(plan.InitialLane)
	plan.FollowupLane = normalizeRouteValue(plan.FollowupLane)
	plan.Reason = normalizeRouteValue(plan.Reason)
	plan.Contract = normalizeRouteValue(plan.Contract)
	plan.Family = normalizeRouteValue(plan.Family)
	plan.Thoroughness = normalizeRouteValue(plan.Thoroughness)
	if plan.Family == "" {
		return fallbackTurnRoute("", mode)
	}
	switch plan.Family {
	case "direct_question":
		plan.InitialLane = "main"
		plan.FollowupLane = ""
		plan.Contract = "direct_answer"
		plan.Reason = util.FirstNonEmpty(plan.Reason, "direct_conversation")
	case "debug_logs":
		plan.InitialLane = "explore"
		plan.FollowupLane = ""
		plan.Contract = "grounded_explanation"
		plan.Reason = util.FirstNonEmpty(plan.Reason, "logs_failure_analysis")
	case "verification_only":
		plan.InitialLane = "main"
		plan.FollowupLane = ""
		plan.Contract = "direct_answer"
		plan.Reason = util.FirstNonEmpty(plan.Reason, "verification_request")
	case "locate":
		plan.InitialLane = "locate"
		plan.FollowupLane = ""
		plan.Contract = "locate_targets"
		plan.Reason = util.FirstNonEmpty(plan.Reason, "locate_request")
	case "locate_then_implement":
		plan.InitialLane = "locate"
		plan.FollowupLane = "implement"
		plan.Contract = "implementation_result"
		plan.Reason = util.FirstNonEmpty(plan.Reason, "locate_request")
	case "explore_then_implement", "investigate_then_implement":
		plan.InitialLane = "explore"
		plan.FollowupLane = "implement"
		plan.Contract = "implementation_result"
		plan.Reason = util.FirstNonEmpty(plan.Reason, "explore_request")
	case "review_then_implement":
		plan.InitialLane = "review"
		plan.FollowupLane = "implement"
		plan.Contract = "implementation_result"
		plan.Reason = util.FirstNonEmpty(plan.Reason, "review_request")
	case "security_review":
		plan.InitialLane = "review"
		plan.FollowupLane = ""
		plan.Contract = "review_findings"
		plan.Reason = util.FirstNonEmpty(plan.Reason, "security_review")
	case "performance_review":
		plan.InitialLane = "review"
		plan.FollowupLane = ""
		plan.Contract = "review_findings"
		plan.Reason = util.FirstNonEmpty(plan.Reason, "performance_review")
	case "architecture":
		plan.InitialLane = "architecture"
		plan.FollowupLane = ""
		plan.Contract = "architecture_assessment"
		plan.Reason = util.FirstNonEmpty(plan.Reason, "architecture_request")
	case "architecture_then_implement":
		plan.InitialLane = "architecture"
		plan.FollowupLane = "implement"
		plan.Contract = "implementation_result"
		plan.Reason = util.FirstNonEmpty(plan.Reason, "architecture_request")
	case "implementation", "test_authoring", "config_edit", "script_edit", "gdscript_edit", "docs_edit", "prompt_edit_security", "failure_injection":
		plan.InitialLane = "implement"
		plan.FollowupLane = ""
		plan.Contract = "implementation_result"
		plan.Reason = util.FirstNonEmpty(plan.Reason, "direct_implementation")
	case "plan_then_execute_planner":
		// Planner phase of `idiom plan-execute`. Same lane shape as
		// implementation (write-capable contract, single turn) but the
		// only exposed write tool is submit_plan; per-edit writes are
		// runtime-rejected. Without this case the family fell through
		// to fallbackTurnRoute → direct_answer, leaving the planner
		// with zero tools and no way to submit a plan.
		plan.InitialLane = "implement"
		plan.FollowupLane = ""
		plan.Contract = "implementation_result"
		plan.Reason = util.FirstNonEmpty(plan.Reason, "plan_then_execute_planner")
	case "plan_then_execute_review_repair":
		// Post-executor review phase for `idiom plan-execute`. This is
		// review-shaped, not implementation-shaped: the model may close
		// with a checklist-backed pass, or use submit_plan only for
		// corrective edits found by the review.
		plan.InitialLane = "review"
		plan.FollowupLane = ""
		plan.Contract = "review_findings"
		plan.Reason = util.FirstNonEmpty(plan.Reason, "plan_then_execute_review_repair")
	case "multi_turn_state":
		plan.InitialLane = "main"
		plan.Contract = "direct_answer"
		plan.Reason = util.FirstNonEmpty(plan.Reason, "context_followup")
	default:
		return fallbackTurnRoute("", mode)
	}
	return plan
}

func correctTurnRoutePlanForPrompt(plan turnRoutePlan, prompt string, mode agent.Mode) turnRoutePlan {
	words := plannerWordText(prompt)
	if (hasImplementationWorkflowContract(prompt, words) || hasPlanExecuteSourceSatisfiedWorkflowContract(prompt, words)) && (hasEffectiveEditIntent(words) || hasPlanExecuteSourceSatisfiedWorkflowContract(prompt, words)) && !isExplanationOnlyCodeQuestion(words) {
		return normalizeTurnRoutePlan(turnRoutePlan{
			Family: "implementation",
			Reason: "workflow_contract",
		}, mode)
	}
	if plan.Family == "verification_only" && hasEffectiveEditIntent(words) {
		return normalizeTurnRoutePlan(turnRoutePlan{
			Family: "implementation",
			Reason: "edit_intent_overrides_verification",
		}, mode)
	}
	if plan.Family == "debug_logs" && !util.ContainsAny(words, " latest ", " recent ", " most recent ", " failed ", " failure ", " why this failed ", " why it failed ", " why ") {
		return fallbackTurnRoute(prompt, mode)
	}
	if isImplementationLikeFamily(plan.Family) && isAnalysisContinuationPrompt(words) {
		return normalizeTurnRoutePlan(turnRoutePlan{Family: "architecture"}, mode)
	}
	if isImplementationLikeFamily(plan.Family) && isExplanationOnlyCodeQuestion(words) {
		return fallbackTurnRoute(prompt, mode)
	}
	return normalizeTurnRoutePlan(plan, mode)
}

func isAnalysisContinuationPrompt(words string) bool {
	if hasEditIntent(words) {
		return false
	}
	if !util.ContainsAny(words,
		" continue ",
		" continue your investigation ",
		" continue investigating ",
		" keep investigating ",
		" investigate further ",
		" go deeper ",
		" dig deeper ",
		" more confident ",
		" confident response ",
		" keep looking ",
	) {
		return false
	}
	return util.ContainsAny(words,
		" investigate ",
		" investigation ",
		" deeper ",
		" confidence ",
		" confident ",
		" response ",
		" explore ",
	)
}

func shouldPreferFallbackRoute(fallback turnRoutePlan, planned turnRoutePlan, prompt string) bool {
	fallbackFamily := strings.TrimSpace(fallback.Family)
	plannedFamily := strings.TrimSpace(planned.Family)
	if fallbackFamily == "" || plannedFamily == "" || fallbackFamily == plannedFamily {
		return false
	}
	words := plannerWordText(prompt)
	if isImplementationLikeFamily(fallbackFamily) && !isImplementationLikeFamily(plannedFamily) && !isExplanationOnlyCodeQuestion(words) {
		return true
	}
	return fallbackFamily != "direct_question" && plannedFamily == "direct_question"
}

func fallbackTurnRoute(prompt string, mode agent.Mode) turnRoutePlan {
	lower := " " + strings.ToLower(strings.TrimSpace(prompt)) + " "
	words := plannerWordText(prompt)
	mentionedPaths := pathparse.MentionedWorkspacePaths(prompt)
	editIntent := hasEffectiveEditIntent(words)
	namedPathWorkIntent := len(mentionedPaths) > 0 && !explicitlyForbidsEdits(words) && hasNamedWorkspaceTargetWorkIntent(words)
	namedPathOrEditIntent := namedPathWorkIntent || editIntent
	switch {
	case util.ContainsAny(words, " log ", " logs ", " jsonl ", " trace ") && util.ContainsAny(words, " fail ", " failed ", " error ", " why ", " latest ", " recent "):
		return normalizeTurnRoutePlan(turnRoutePlan{Family: "debug_logs"}, mode)
	case !editIntent && util.ContainsAny(words, " go test ", " test suite ", " run tests ", " run the tests ", " report failure ", " report failures ", " compile failure "):
		return normalizeTurnRoutePlan(turnRoutePlan{Family: "verification_only"}, mode)
	case !editIntent && util.ContainsAny(words, " smoke test ", " verify ", " test the ", " test this "):
		return normalizeTurnRoutePlan(turnRoutePlan{Family: "verification_only"}, mode)
	case isSuggestionOnlyArchitectureQuestion(words):
		return normalizeTurnRoutePlan(turnRoutePlan{Family: "architecture"}, mode)
	case util.ContainsAny(words, " find where ", " locate ", " where is ", " where the ") && editIntent:
		return normalizeTurnRoutePlan(turnRoutePlan{Family: "locate_then_implement"}, mode)
	case util.ContainsAny(words, " find where ", " locate ", " where is ", " where the "):
		return normalizeTurnRoutePlan(turnRoutePlan{Family: "locate"}, mode)
	case util.ContainsAny(words, " architecture ", " boundary ", " boundaries ", " coupling ", " refactor stage "):
		if editIntent {
			return normalizeTurnRoutePlan(turnRoutePlan{Family: "architecture_then_implement"}, mode)
		}
		return normalizeTurnRoutePlan(turnRoutePlan{Family: "architecture"}, mode)
	case isExplanationOnlyCodeQuestion(words):
		return normalizeTurnRoutePlan(turnRoutePlan{Family: "architecture"}, mode)
	case util.ContainsAny(words, " security ", " prompt injection ", " adversarial "):
		return normalizeTurnRoutePlan(turnRoutePlan{Family: "security_review"}, mode)
	case util.ContainsAny(words, " performance ", " perf ", " timeout ", " timed out ", " regression ", " slow "):
		if editIntent {
			return normalizeTurnRoutePlan(turnRoutePlan{Family: "review_then_implement"}, mode)
		}
		return normalizeTurnRoutePlan(turnRoutePlan{Family: "performance_review"}, mode)
	case util.ContainsAny(words, " review ", " investigate "):
		if editIntent {
			return normalizeTurnRoutePlan(turnRoutePlan{Family: "review_then_implement"}, mode)
		}
		return normalizeTurnRoutePlan(turnRoutePlan{Family: "performance_review"}, mode)
	case util.ContainsAny(words, " write test ", " add regression test ", " add a test "):
		return normalizeTurnRoutePlan(turnRoutePlan{Family: "test_authoring"}, mode)
	case util.ContainsAny(words, " readme ", " docs ", " documentation ", " doc ") && (namedPathOrEditIntent || util.ContainsAny(words, " write ", " writing ")):
		return normalizeTurnRoutePlan(turnRoutePlan{Family: "docs_edit"}, mode)
	case util.ContainsAny(lower, " config ", ".json", ".yaml", ".toml") && namedPathOrEditIntent:
		return normalizeTurnRoutePlan(turnRoutePlan{Family: "config_edit"}, mode)
	case strings.Contains(lower, ".gd") && namedPathOrEditIntent:
		return normalizeTurnRoutePlan(turnRoutePlan{Family: "gdscript_edit"}, mode)
	case util.ContainsAny(lower, " script ", ".sh", " shell ") && namedPathOrEditIntent:
		return normalizeTurnRoutePlan(turnRoutePlan{Family: "script_edit"}, mode)
	case util.ContainsAny(lower, " prompt ", " prompts ") && namedPathOrEditIntent:
		return normalizeTurnRoutePlan(turnRoutePlan{Family: "prompt_edit_security"}, mode)
	case namedPathWorkIntent:
		if routeNamedWorkspaceTargetAsDocsEdit(prompt, words) {
			return normalizeTurnRoutePlan(turnRoutePlan{Family: "docs_edit"}, mode)
		}
		return normalizeTurnRoutePlan(turnRoutePlan{Family: "implementation"}, mode)
	case editIntent:
		return normalizeTurnRoutePlan(turnRoutePlan{Family: "implementation"}, mode)
	}
	if util.ContainsAny(words,
		" help me ",
		" help test ",
		" help with ",
		" try out ",
		" test out ",
		" try the ",
		" test the ",
		" show me ",
		" walk me ",
		" walk through ",
		" walk me through ",
		" lets ",
		" let's ",
		" let me ",
		" i want to ",
		" i'd like to ",
		" i need to ",
	) {
		return normalizeTurnRoutePlan(turnRoutePlan{Family: "architecture"}, mode)
	}
	if strings.EqualFold(mode.Name, "plan") {
		return normalizeTurnRoutePlan(turnRoutePlan{Family: "architecture"}, mode)
	}
	return normalizeTurnRoutePlan(turnRoutePlan{Family: "direct_question"}, mode)
}

func isImplementationLikeFamily(family string) bool {
	switch strings.TrimSpace(family) {
	case "implementation",
		"test_authoring",
		"config_edit",
		"script_edit",
		"gdscript_edit",
		"docs_edit",
		"prompt_edit_security",
		"failure_injection",
		"locate_then_implement",
		"explore_then_implement",
		"investigate_then_implement",
		"review_then_implement",
		"architecture_then_implement",
		"plan_then_execute_planner":
		return true
	default:
		return false
	}
}

func isExplanationOnlyCodeQuestion(words string) bool {
	if hasEditIntent(words) || util.ContainsAny(words,
		" review ",
		" performance ",
		" perf ",
		" security ",
		" latest logs ",
		" recent logs ",
		" run tests ",
		" verify ",
		" where is ",
		" locate ",
	) {
		return false
	}
	if !util.ContainsAny(words,
		" how does ",
		" how do ",
		" how is ",
		" how are ",
		" explain how ",
		" explain what ",
		" what does ",
		" how this ",
	) {
		return false
	}
	return util.ContainsAny(words,
		" harness ",
		" codebase ",
		" repo ",
		" repository ",
		" runner ",
		" tool ",
		" tools ",
		" agent ",
		" agents ",
		" subagent ",
		" subagents ",
		" sub agent ",
		" workflow ",
		" component ",
		" system ",
		" this ",
		" mode ",
		" feature ",
		" behavior ",
		" debug log ",
		" redacted ",
	)
}

func hasEditIntent(words string) bool {
	return util.ContainsAny(words,
		" fix ",
		" fixing ",
		" change ",
		" changing ",
		" update ",
		" updating ",
		" edit ",
		" editing ",
		" modify ",
		" modifying ",
		" add ",
		" adding ",
		" remove ",
		" removing ",
		" delete ",
		" deleting ",
		" implement ",
		" implementing ",
		" refactor ",
		" refactoring ",
		" rename ",
		" renaming ",
		" replace ",
		" replacing ",
		" create ",
		" creating ",
		" patch ",
		" patching ",
	)
}

func hasEffectiveEditIntent(words string) bool {
	return (hasEditIntent(words) || hasUILayoutMutationIntent(words)) && !explicitlyForbidsEdits(words)
}

func hasImplementationWorkflowContract(prompt string, words string) bool {
	hasNamedRequiredPaths := len(pathparse.MentionedWorkspacePaths(prompt)) > 0 && hasRequiredEditPathContractWords(words)
	hasPlanExecuteWorkflow := hasPlanExecuteWorkflowWords(words)
	if hasNamedRequiredPaths && hasPlanExecuteWorkflow {
		return true
	}
	if hasNamedRequiredPaths && util.ContainsAny(words,
		" implementation ",
		" implement ",
		" source-satisfied ",
		" source satisfied ",
		" smoke test ",
		" tdd ",
	) {
		return true
	}
	if hasPlanExecuteWorkflow && util.ContainsAny(words,
		" smoke test ",
		" run ",
		" try ",
		" verify ",
		" tdd ",
		" through ",
	) {
		return true
	}
	return false
}

func hasPlanExecuteSourceSatisfiedWorkflowContract(prompt string, words string) bool {
	return util.ContainsAny(words, " source satisfied ") &&
		hasPlanExecuteWorkflowWords(words) &&
		(len(pathparse.MentionedWorkspacePaths(prompt)) > 0 || hasRequiredEditPathContractWords(words))
}

func hasRequiredEditPathContractWords(words string) bool {
	return util.ContainsAny(words,
		" required edit paths ",
		" required edit path ",
		" required paths ",
		" required path ",
	)
}

func hasPlanExecuteWorkflowWords(words string) bool {
	return util.ContainsAny(words,
		" plan execute ",
		" prd to plan to execute ",
		" prd to plan execute ",
		" prd to adr to plan execute ",
		" prd to adr to plan to execute ",
		" implementation lane ",
		" default implementation lane ",
	)
}

func hasUILayoutMutationIntent(words string) bool {
	if !util.ContainsAny(words,
		" input field ",
		" text input ",
		" textarea ",
		" terminal ",
		" tui ",
		" ui ",
		" viewport ",
		" footer ",
		" prompt box ",
	) {
		return false
	}
	return util.ContainsAny(words,
		" re align ",
		" realign ",
		" align ",
		" flush ",
		" bottom ",
		" wrap ",
		" height ",
		" off screen ",
		" offscreen ",
		" resize ",
		" reposition ",
		" move ",
	)
}

func explicitlyForbidsEdits(words string) bool {
	return util.ContainsAny(words,
		" do not edit ",
		" dont edit ",
		" don t edit ",
		" no edits ",
		" without edits ",
		" without editing ",
		" without changing ",
		" do not change files ",
		" do not modify ",
		" without modifying ",
		" no file changes ",
	)
}

func isSuggestionOnlyArchitectureQuestion(words string) bool {
	if !util.ContainsAny(words, " what can we ", " what should we ", " how can we ", " how should we ") {
		return false
	}
	if !util.ContainsAny(words, " add ", " improve ", " make ", " increasing ", " increase ", " better ", " useful ", " usefulness ") {
		return false
	}
	return util.ContainsAny(words,
		" tool ",
		" tools ",
		" harness ",
		" repo ",
		" repository ",
		" codebase ",
		" system ",
		" workflow ",
		" architecture ",
		" agent ",
		" agents ",
	)
}

func hasWorkOnTargetIntent(words string) bool {
	return util.ContainsAny(words,
		" start working on ",
		" start work on ",
		" start implementing ",
		" begin working on ",
		" begin work on ",
		" begin implementing ",
		" get started on ",
		" work on ",
		" working on ",
		" implement ",
		" implementing ",
	)
}

func hasNamedWorkspaceTargetWorkIntent(words string) bool {
	if hasWorkOnTargetIntent(words) {
		return true
	}
	return util.ContainsAny(words,
		" start ",
		" begin ",
		" started ",
		" work ",
		" working ",
		" woring ",
		" implement ",
		" implementing ",
		" update ",
		" edit ",
		" change ",
		" fix ",
		" add ",
		" create ",
	) && util.ContainsAny(words,
		" on ",
		" in ",
		" to ",
	)
}

func routeNamedWorkspaceTargetAsDocsEdit(prompt string, words string) bool {
	if util.ContainsAny(words, " docs ", " documentation ", " doc ", " readme ") {
		return true
	}
	for _, path := range pathparse.MentionedWorkspacePaths(prompt) {
		switch strings.ToLower(filepath.Ext(path)) {
		case ".md", ".mdx", ".markdown", ".rst", ".txt":
			return true
		}
		if strings.EqualFold(filepath.Base(path), "README") {
			return true
		}
	}
	return false
}

func plannerWordText(prompt string) string {
	prompt = strings.TrimSpace(strings.ToLower(prompt))
	if prompt == "" {
		return " "
	}
	var b strings.Builder
	b.Grow(len(prompt) + 2)
	b.WriteByte(' ')
	lastSpace := true
	for _, r := range prompt {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			lastSpace = false
			continue
		}
		if !lastSpace {
			b.WriteByte(' ')
			lastSpace = true
		}
	}
	if !lastSpace {
		b.WriteByte(' ')
	}
	return b.String()
}

func renderTurnRoutePlan(plan turnRoutePlan) string {
	var parts []string
	if plan.InitialLane != "" {
		parts = append(parts, "initial="+plan.InitialLane)
	}
	if plan.FollowupLane != "" {
		parts = append(parts, "followup="+plan.FollowupLane)
	}
	if plan.Reason != "" {
		parts = append(parts, "reason="+plan.Reason)
	}
	if plan.Contract != "" {
		parts = append(parts, "contract="+plan.Contract)
	}
	if plan.Family != "" {
		parts = append(parts, "task_family="+plan.Family)
	}
	if plan.Thoroughness != "" {
		parts = append(parts, "thoroughness="+plan.Thoroughness)
	}
	return strings.Join(parts, " ")
}

func turnRoutePlanRuntimeBrief(plan turnRoutePlan, mode agent.Mode, workspaceAllowed []string) workerruntime.WorkerRuntimeBrief {
	plan = normalizeTurnRoutePlan(plan, mode)
	brief := workerruntime.RuntimeBriefForTaskFamily(plan.Family, util.FirstNonEmpty(plan.InitialLane, "main"), workspaceAllowed)
	if plan.FollowupLane != "" {
		brief.Constraints = append(brief.Constraints, "followup_lane="+plan.FollowupLane)
	}
	if plan.Thoroughness != "" {
		brief.Constraints = append(brief.Constraints, "thoroughness="+plan.Thoroughness)
	}
	return workerruntime.NormalizeRuntimeBrief(brief)
}

func (m *Model) workspaceAllowedTools() []string {
	if m == nil || m.workspace == nil {
		return nil
	}
	return m.workspace.AllowedTools()
}

func appendRunnerStateStrings(values []string, extra ...string) []string {
	out := append([]string(nil), values...)
	for _, value := range extra {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		found := false
		for _, existing := range out {
			if existing == value {
				found = true
				break
			}
		}
		if !found {
			out = append(out, value)
		}
	}
	return out
}

func normalizeRouteValue(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "-", "_")
	value = strings.ReplaceAll(value, " ", "_")
	return value
}
