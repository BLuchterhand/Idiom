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
	if deterministicTurnRouteIsDecisive(fallback, prompt, mode) {
		return fallback
	}
	if planned, ok := m.classifyTurnRouteWithModel(ctx, prompt, mode); ok {
		planned = correctTurnRoutePlanForPrompt(planned, prompt, mode)
		if shouldPreferFallbackRoute(fallback, planned) {
			return fallback
		}
		return planned
	}
	return fallback
}

func deterministicTurnRouteIsDecisive(plan turnRoutePlan, prompt string, mode agent.Mode) bool {
	plan = normalizeTurnRoutePlan(plan, mode)
	family := strings.TrimSpace(plan.Family)
	if family == "" || family == "direct_question" || family == "multi_turn_state" {
		return false
	}
	words := plannerWordText(prompt)
	if isAnalysisContinuationPrompt(words) {
		return false
	}
	return true
}

func (m *Model) classifyTurnRouteWithModel(ctx context.Context, prompt string, mode agent.Mode) (turnRoutePlan, bool) {
	if m == nil {
		return turnRoutePlan{}, false
	}
	if !m.cfg.RouteClassifier.Enabled {
		return turnRoutePlan{}, false
	}
	_ = m.ensureSummaryServer()
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
	text, err := m.completeChatWithFallback(classifyCtx, messages, llama.ChatOptions{
		MaxTokens:          256,
		Temperature:        0.1,
		EnableThinking:     &thinkingOff,
		ChatTemplateKwargs: true,
	})
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
		plan.Reason = firstNonEmptyString(plan.Reason, "direct_conversation")
	case "debug_logs":
		plan.InitialLane = "explore"
		plan.FollowupLane = ""
		plan.Contract = "grounded_explanation"
		plan.Reason = firstNonEmptyString(plan.Reason, "logs_failure_analysis")
	case "verification_only":
		plan.InitialLane = "main"
		plan.FollowupLane = ""
		plan.Contract = "direct_answer"
		plan.Reason = firstNonEmptyString(plan.Reason, "verification_request")
	case "locate":
		plan.InitialLane = "locate"
		plan.FollowupLane = ""
		plan.Contract = "locate_targets"
		plan.Reason = firstNonEmptyString(plan.Reason, "locate_request")
	case "locate_then_implement":
		plan.InitialLane = "locate"
		plan.FollowupLane = "implement"
		plan.Contract = "implementation_result"
		plan.Reason = firstNonEmptyString(plan.Reason, "locate_request")
	case "explore_then_implement", "investigate_then_implement":
		plan.InitialLane = "explore"
		plan.FollowupLane = "implement"
		plan.Contract = "implementation_result"
		plan.Reason = firstNonEmptyString(plan.Reason, "explore_request")
	case "review_then_implement":
		plan.InitialLane = "review"
		plan.FollowupLane = "implement"
		plan.Contract = "implementation_result"
		plan.Reason = firstNonEmptyString(plan.Reason, "review_request")
	case "security_review":
		plan.InitialLane = "review"
		plan.FollowupLane = ""
		plan.Contract = "review_findings"
		plan.Reason = firstNonEmptyString(plan.Reason, "security_review")
	case "performance_review":
		plan.InitialLane = "review"
		plan.FollowupLane = ""
		plan.Contract = "review_findings"
		plan.Reason = firstNonEmptyString(plan.Reason, "performance_review")
	case "architecture":
		plan.InitialLane = "architecture"
		plan.FollowupLane = ""
		plan.Contract = "architecture_assessment"
		plan.Reason = firstNonEmptyString(plan.Reason, "architecture_request")
	case "architecture_then_implement":
		plan.InitialLane = "architecture"
		plan.FollowupLane = "implement"
		plan.Contract = "implementation_result"
		plan.Reason = firstNonEmptyString(plan.Reason, "architecture_request")
	case "implementation", "test_authoring", "config_edit", "script_edit", "gdscript_edit", "docs_edit", "prompt_edit_security", "failure_injection":
		plan.InitialLane = "implement"
		plan.FollowupLane = ""
		plan.Contract = "implementation_result"
		plan.Reason = firstNonEmptyString(plan.Reason, "direct_implementation")
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
		plan.Reason = firstNonEmptyString(plan.Reason, "plan_then_execute_planner")
	case "plan_then_execute_review_repair":
		// Post-executor review phase for `idiom plan-execute`. This is
		// review-shaped, not implementation-shaped: the model may close
		// with a checklist-backed pass, or use submit_plan only for
		// corrective edits found by the review.
		plan.InitialLane = "review"
		plan.FollowupLane = ""
		plan.Contract = "review_findings"
		plan.Reason = firstNonEmptyString(plan.Reason, "plan_then_execute_review_repair")
	case "multi_turn_state":
		plan.InitialLane = "main"
		plan.Contract = "direct_answer"
		plan.Reason = firstNonEmptyString(plan.Reason, "context_followup")
	default:
		return fallbackTurnRoute("", mode)
	}
	return plan
}

func correctTurnRoutePlanForPrompt(plan turnRoutePlan, prompt string, mode agent.Mode) turnRoutePlan {
	words := plannerWordText(prompt)
	if plan.Family == "verification_only" && hasEditIntent(words) {
		return normalizeTurnRoutePlan(turnRoutePlan{
			Family: "implementation",
			Reason: "edit_intent_overrides_verification",
		}, mode)
	}
	if plan.Family == "debug_logs" && !containsAny(words, " latest ", " recent ", " most recent ", " failed ", " failure ", " why this failed ", " why it failed ", " why ") {
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
	if !containsAny(words,
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
	return containsAny(words,
		" investigate ",
		" investigation ",
		" deeper ",
		" confidence ",
		" confident ",
		" response ",
		" explore ",
	)
}

func shouldPreferFallbackRoute(fallback turnRoutePlan, planned turnRoutePlan) bool {
	fallbackFamily := strings.TrimSpace(fallback.Family)
	plannedFamily := strings.TrimSpace(planned.Family)
	if fallbackFamily == "" || plannedFamily == "" || fallbackFamily == plannedFamily {
		return false
	}
	return fallbackFamily != "direct_question" && plannedFamily == "direct_question"
}

func fallbackTurnRoute(prompt string, mode agent.Mode) turnRoutePlan {
	lower := " " + strings.ToLower(strings.TrimSpace(prompt)) + " "
	words := plannerWordText(prompt)
	switch {
	case containsAny(words, " log ", " logs ", " jsonl ", " trace ") && containsAny(words, " fail ", " failed ", " error ", " why ", " latest ", " recent "):
		return normalizeTurnRoutePlan(turnRoutePlan{Family: "debug_logs"}, mode)
	case !hasEditIntent(words) && containsAny(words, " go test ", " test suite ", " run tests ", " run the tests ", " report failure ", " report failures ", " compile failure "):
		return normalizeTurnRoutePlan(turnRoutePlan{Family: "verification_only"}, mode)
	case containsAny(words, " find where ", " locate ", " where is ", " where the ") && containsAny(words, " remove ", " fix ", " update ", " edit ", " change ", " add "):
		return normalizeTurnRoutePlan(turnRoutePlan{Family: "locate_then_implement"}, mode)
	case containsAny(words, " find where ", " locate ", " where is ", " where the "):
		return normalizeTurnRoutePlan(turnRoutePlan{Family: "locate"}, mode)
	case containsAny(words, " architecture ", " boundary ", " boundaries ", " coupling ", " refactor stage "):
		if containsAny(words, " fix ", " change ", " implement ", " refactor ", " update ", " edit ") {
			return normalizeTurnRoutePlan(turnRoutePlan{Family: "architecture_then_implement"}, mode)
		}
		return normalizeTurnRoutePlan(turnRoutePlan{Family: "architecture"}, mode)
	case isExplanationOnlyCodeQuestion(words):
		return normalizeTurnRoutePlan(turnRoutePlan{Family: "architecture"}, mode)
	case containsAny(words, " security ", " prompt injection ", " adversarial "):
		return normalizeTurnRoutePlan(turnRoutePlan{Family: "security_review"}, mode)
	case containsAny(words, " performance ", " perf ", " timeout ", " timed out ", " regression ", " slow "):
		if containsAny(words, " fix ", " change ", " implement ", " update ", " edit ") {
			return normalizeTurnRoutePlan(turnRoutePlan{Family: "review_then_implement"}, mode)
		}
		return normalizeTurnRoutePlan(turnRoutePlan{Family: "performance_review"}, mode)
	case containsAny(words, " review ", " investigate "):
		if containsAny(words, " fix ", " change ", " implement ", " update ", " edit ") {
			return normalizeTurnRoutePlan(turnRoutePlan{Family: "review_then_implement"}, mode)
		}
		return normalizeTurnRoutePlan(turnRoutePlan{Family: "performance_review"}, mode)
	case containsAny(words, " write test ", " add regression test ", " add a test "):
		return normalizeTurnRoutePlan(turnRoutePlan{Family: "test_authoring"}, mode)
	case containsAny(words, " readme ", " docs ", " documentation ", " doc "):
		if containsAny(words, " update ", " add ", " edit ", " write ", " change ") {
			return normalizeTurnRoutePlan(turnRoutePlan{Family: "docs_edit"}, mode)
		}
	case containsAny(lower, " config ", ".json", ".yaml", ".toml"):
		if containsAny(words, " update ", " edit ", " change ", " add ") {
			return normalizeTurnRoutePlan(turnRoutePlan{Family: "config_edit"}, mode)
		}
	case strings.Contains(lower, ".gd"):
		if containsAny(words, " update ", " edit ", " change ", " add ", " fix ", " remove ", " delete ", " rename ", " replace ") {
			return normalizeTurnRoutePlan(turnRoutePlan{Family: "gdscript_edit"}, mode)
		}
	case containsAny(lower, " script ", ".sh", " shell "):
		if containsAny(words, " update ", " edit ", " change ", " add ", " fix ") {
			return normalizeTurnRoutePlan(turnRoutePlan{Family: "script_edit"}, mode)
		}
	case containsAny(lower, " prompt ", " prompts "):
		if containsAny(words, " update ", " edit ", " change ", " add ", " fix ") {
			return normalizeTurnRoutePlan(turnRoutePlan{Family: "prompt_edit_security"}, mode)
		}
	case hasEditIntent(words):
		return normalizeTurnRoutePlan(turnRoutePlan{Family: "implementation"}, mode)
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
	if hasEditIntent(words) || containsAny(words,
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
	if !containsAny(words,
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
	return containsAny(words,
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
	return containsAny(words,
		" fix ",
		" change ",
		" update ",
		" edit ",
		" modify ",
		" add ",
		" remove ",
		" delete ",
		" implement ",
		" refactor ",
		" rename ",
		" replace ",
		" create ",
		" patch ",
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
	if plan.Thoroughness != "" {
		parts = append(parts, "thoroughness="+plan.Thoroughness)
	}
	return strings.Join(parts, " ")
}

func turnRoutePlanRuntimeBrief(plan turnRoutePlan, mode agent.Mode, workspaceAllowed []string) workerruntime.WorkerRuntimeBrief {
	plan = normalizeTurnRoutePlan(plan, mode)
	brief := workerruntime.RuntimeBriefForTaskFamily(plan.Family, firstNonEmptyString(plan.InitialLane, "main"), workspaceAllowed)
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
