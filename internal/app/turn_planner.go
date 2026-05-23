package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode"

	"idiom/internal/agent"
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
	if planned, ok := m.classifyTurnRouteWithModel(ctx, prompt, mode); ok {
		planned = correctTurnRoutePlanForPrompt(planned, prompt, mode)
		if shouldPreferFallbackRoute(fallback, planned, prompt) {
			return fallback
		}
		return planned
	}
	return fallback
}

func (m *Model) classifyTurnRouteWithModel(ctx context.Context, prompt string, mode agent.Mode) (turnRoutePlan, bool) {
	if m == nil {
		return turnRoutePlan{}, false
	}
	if !m.cfg.RouteClassifier.Enabled {
		m.logRouteClassifierDecision(map[string]any{"status": "disabled"})
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
				"conversation, debug_logs, verification_only, locate, locate_then_implement, explore_then_implement, investigate_then_implement, review_then_implement, security_review, performance_review, architecture, architecture_then_implement, implementation, plan_then_execute_planner, test_authoring, config_edit, script_edit, gdscript_edit, docs_edit, prompt_edit_security, failure_injection, multi_turn_state.",
				"Also return initial_lane, optional followup_lane, reason, contract, and optional thoroughness.",
				"Use coarse OpenCode-shaped routing: conversation for normal question-answer turns, read-only investigation, verification-only, locate, architecture/review analysis, or implementation. The loop will enforce permissions later.",
				"Prefer conversation by default for questions, status requests, how-to requests, and lightweight repo questions. Conversation may use efficient read-only workspace tools when the answer depends on code or logs.",
				"Prefer debug_logs for requests about recent logs or explaining a failure from logs.",
				"Prefer verification_only for requests to run checks/tests and report results without editing.",
				"Prefer locate for finding where something is, and locate_then_implement when the user wants to find then change it.",
				"Prefer implementation-like families when the user gives a directive to perform file changes, tests, docs, config, scripts, prompts, or repairs.",
				"Prefer plan_then_execute_planner when the requested outcome is to dispatch a durable implementation atom or brief through the plan-execute workflow, where valid terminal outcomes include a submit_plan artifact, source-satisfied closeout, or typed blocker before execution.",
				"Question-shaped wording is not a lane signal by itself. Classify by requested outcome: a polite request such as 'can you implement/fix/update/add...' is implementation when it asks the agent to perform the change now; a deliberative question such as 'can we/should we/how would we...' stays conversation when it asks about whether or how to change.",
				"Do not let can/could/would/should/how/what/why/whether/explain wording override an explicit implementation directive.",
				"Semantic examples, not trigger terms: 'Can you implement this feature: docs/tdd-feature.md' => implementation; 'Can you send this product workflow atom through the TUI plan-execute flow?' => plan_then_execute_planner; 'Can we shorten this label?' => conversation; 'Shorten this label.' => implementation; 'I want to test the implementation lane.' => conversation.",
				"Prefer gdscript_edit for edits limited to .gd GDScript files.",
				"Prefer architecture for broad design, boundary, or cross-component assessment. Use conversation for ordinary explanation-only questions unless the user asks for deep architecture/review.",
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
		TopP:               1,
		EnableThinking:     &thinkingOff,
		ChatTemplateKwargs: true,
	}
	client := m.routeClassifierChatClient()
	if client == nil {
		m.logRouteClassifierDecision(map[string]any{"status": "unavailable", "reason": "no_client"})
		return turnRoutePlan{}, false
	}
	text, err := client.CompleteChat(classifyCtx, messages, opts)
	if err != nil && client != m.client && m.client != nil {
		text, err = m.client.CompleteChat(classifyCtx, messages, opts)
	}
	if err != nil {
		m.logRouteClassifierDecision(map[string]any{"status": "unavailable", "reason": "completion_error", "error": truncateImplementationDispatchText(err.Error(), 240)})
		return turnRoutePlan{}, false
	}
	plan, err := parseTurnRoutePlan(text)
	if err != nil {
		m.logRouteClassifierDecision(map[string]any{"status": "parse_error", "error": truncateImplementationDispatchText(err.Error(), 240)})
		return turnRoutePlan{}, false
	}
	plan = normalizeTurnRoutePlan(plan, mode)
	m.logRouteClassifierDecision(map[string]any{
		"status":       "accepted",
		"family":       plan.Family,
		"initial_lane": plan.InitialLane,
		"reason":       plan.Reason,
		"contract":     plan.Contract,
	})
	return plan, plan.Family != ""
}

func (m *Model) logRouteClassifierDecision(fields map[string]any) {
	if m == nil {
		return
	}
	if fields == nil {
		fields = map[string]any{}
	}
	fields["schema_version"] = "idiom.route_classifier_decision.v1"
	if body, err := json.Marshal(fields); err == nil {
		m.logMessage("route_classifier_decision", string(body))
	}
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
	switch plan.Family {
	case "plan_execute", "plan_execute_planner", "plan_then_execute":
		plan.Family = agent.PlanThenExecutePlannerTaskFamily
	}
	if plan.Family == "" {
		return fallbackTurnRoute("", mode)
	}
	switch plan.Family {
	case workerruntime.ArtifactFinalAnswerTaskFamily:
		plan.InitialLane = "main"
		plan.FollowupLane = ""
		plan.Contract = "artifact_final_answer"
		plan.Reason = util.FirstNonEmpty(plan.Reason, "artifact_final_answer")
	case "direct_question", "conversation":
		plan.Family = "conversation"
		plan.InitialLane = "main"
		plan.FollowupLane = ""
		plan.Contract = "direct_answer"
		plan.Reason = util.FirstNonEmpty(plan.Reason, "conversation")
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
	case agent.PlanThenExecutePlannerTaskFamily:
		// Planner phase of `idiom plan-execute`. Same lane shape as
		// implementation (write-capable contract, single turn) but the
		// only exposed write tool is submit_plan; per-edit writes are
		// runtime-rejected. Without this case the family fell through
		// to fallbackTurnRoute -> conversation/direct_answer,
		// leaving the planner with zero write tools and no way to
		// submit a plan.
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
		plan.Family = "conversation"
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
	return fallbackFamily != "conversation" && plannedFamily == "conversation"
}

func fallbackTurnRoute(prompt string, mode agent.Mode) turnRoutePlan {
	// Human-facing TUI lane changes must never be inferred from prompt wording.
	// If the classifier is unavailable or declines to route, stay conservative:
	// natural language remains conversation, while typed slash commands enter
	// their lanes through explicit command handlers outside this fallback.
	if strings.EqualFold(mode.Name, "plan") {
		return normalizeTurnRoutePlan(turnRoutePlan{Family: "architecture"}, mode)
	}
	return normalizeTurnRoutePlan(turnRoutePlan{Family: "conversation"}, mode)
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
	if util.ContainsAny(words,
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
		" explain ",
		" explain how ",
		" explain what ",
		" what does ",
		" what is ",
		" what are ",
		" difference between ",
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
	if explicitlyForbidsEdits(words) || isQuestionShapedRequest(words) {
		return false
	}
	return hasEditIntent(words)
}

func isQuestionShapedRequest(words string) bool {
	return util.ContainsAny(words,
		" can we ",
		" can you ",
		" could we ",
		" could you ",
		" would we ",
		" would you ",
		" should we ",
		" should you ",
		" how can ",
		" how could ",
		" how should ",
		" what would ",
		" what should ",
		" why ",
		" whether ",
		" explain ",
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
