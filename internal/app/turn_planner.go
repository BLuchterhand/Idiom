package app

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"

	"idiom/internal/agent"
	"idiom/internal/frontier/workerruntime"
	"idiom/internal/llama"
	"idiom/internal/util"
)

type turnRoutePlan struct {
	InitialLane        string      `json:"initial_lane,omitempty"`
	FollowupLane       string      `json:"followup_lane,omitempty"`
	Reason             string      `json:"reason,omitempty"`
	Contract           string      `json:"contract,omitempty"`
	Family             string      `json:"family,omitempty"`
	Thoroughness       string      `json:"thoroughness,omitempty"`
	RequestedOutcome   string      `json:"requested_outcome,omitempty"`
	WriteIntent        string      `json:"write_intent,omitempty"`
	ReadOnlyConstraint bool        `json:"read_only_constraint,omitempty"`
	EvidenceIntent     string      `json:"evidence_intent,omitempty"`
	ContinuationTarget string      `json:"continuation_target,omitempty"`
	ReasonCodes        []string    `json:"reason_codes,omitempty"`
	RouteSource        routeSource `json:"-"`
}

func routeClassifierFamilyEnumSentence() string {
	return routeClassifierFamilyEnumList() + "."
}

func routeClassifierFamilyEnumList() string {
	return strings.Join([]string{
		"conversation",
		"debug_logs",
		"verification_only",
		"locate",
		"locate_then_implement",
		"explore_then_implement",
		"investigate_then_implement",
		"review_then_implement",
		"security_review",
		"performance_review",
		"architecture",
		"architecture_then_implement",
		"implementation",
		string(workerruntime.TaskFamilyPlanThenExecutePlanner),
		"test_authoring",
		"config_edit",
		"script_edit",
		"gdscript_edit",
		"docs_edit",
		"prompt_edit_security",
		"failure_injection",
		"multi_turn_state",
	}, ", ")
}

func (m *Model) classifyTurnRoute(ctx context.Context, prompt string, mode agent.Mode) turnRoutePlan {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return fallbackTurnRoute(prompt, mode)
	}
	fallback := fallbackTurnRoute(prompt, mode)
	if planned, ok := m.classifyTurnRouteWithModel(ctx, prompt, mode); ok {
		classified := planned
		planned = correctTurnRoutePlanForPrompt(planned, prompt, mode)
		m.logTurnRouteCorrection(classified, planned)
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
	classifierPrompt := routeClassifierPromptForModel(prompt)
	messages := []llama.ChatMessage{
		{
			Role: "system",
			Content: strings.Join([]string{
				"You classify coding-agent turns into one workflow family before execution.",
				"Return strict JSON only.",
				"Choose the best family from this enum:",
				routeClassifierFamilyEnumSentence(),
				"Also return initial_lane, optional followup_lane, reason, contract, optional thoroughness, requested_outcome, write_intent, read_only_constraint, evidence_intent, continuation_target, and reason_codes.",
				"Use coarse OpenCode-shaped routing: conversation for normal question-answer turns, read-only investigation, verification-only, locate, architecture/review analysis, or implementation. The loop will enforce permissions later.",
				"Classify the requested outcome first. requested_outcome enum: answer, diagnose, change_workspace, diagnose_then_change, verify, plan, review, locate.",
				"write_intent enum: yes, no, ambiguous. Use yes only when the requested outcome is to edit files, repair behavior, add tests/docs/config, or otherwise change the workspace. Use no for explanation, how-to, status, and read-only investigation.",
				"evidence_intent enum: generic, relation_trace, refactor. Use relation_trace for read-only questions asking how symbols call, use, reference, delegate, dispatch, or hand off to each other. Use refactor for move, extract, split, rename, reshape, or reorganization work.",
				"continuation_target enum: prior_change, current_prompt_only, none. A post-completion defect report like 'that did not fix it' or 'it still fails' targets prior_change.",
				"Explicit read-only constraints such as 'do not edit', 'just explain', 'without changing files', or 'diagnose only' set read_only_constraint=true and keep the route read-only even if the topic mentions a possible change.",
				"Prefer conversation by default for questions, status requests, how-to requests, and lightweight repo questions. Conversation may use efficient read-only workspace tools when the answer depends on code or logs.",
				"Use conversation for read-only diagnosis and log inspection. Do not route normal user prompts to debug_logs; it is a legacy internal contract and the standard read lane can inspect logs when useful.",
				"Prefer verification_only for requests to run checks/tests and report results without editing.",
				"Prefer locate for finding where something is, and locate_then_implement when the user wants to find then change it.",
				"Prefer implementation-like families when the user gives a directive to perform file changes, tests, docs, config, scripts, prompts, or repairs.",
				fmt.Sprintf("Prefer %s when the requested outcome is to dispatch a durable implementation atom or brief through the plan-execute workflow, where valid terminal outcomes include a submit_plan artifact, source-satisfied closeout, or typed blocker before execution.", workerruntime.TaskFamilyPlanThenExecutePlanner),
				"Question-shaped wording is not a lane signal by itself. Classify by requested outcome: a polite request such as 'can you implement/fix/update/add...' is implementation when it asks the agent to perform the change now; a deliberative question such as 'can we/should we/how would we...' stays conversation when it asks about whether or how to change.",
				"Do not let can/could/would/should/how/what/why/whether/explain wording override an explicit implementation directive.",
				"After a completed implementation, do not keep a sticky implementation state. Route the new prompt from its requested_outcome: 'what did you change?' => answer/read-only; 'why did it fail?' => diagnose/read-only; 'it still goes off-screen, fix it' or 'that did not fix it' => change_workspace/prior_change/write_intent=yes unless a read-only constraint is present.",
				fmt.Sprintf("Semantic examples, not trigger terms: 'Can you implement this feature: docs/tdd-feature.md' => implementation; 'Can you send this product workflow atom through the TUI plan-execute flow?' => %s; 'Can we shorten this label?' => conversation; 'Shorten this label.' => implementation; 'I want to test the implementation lane.' => conversation.", workerruntime.TaskFamilyPlanThenExecutePlanner),
				"Prefer gdscript_edit for edits limited to .gd GDScript files.",
				"Prefer architecture for broad design, boundary, cross-component assessment, or repo-internal subsystem explanations where the answer depends on harness/tool/control-flow implementation evidence. Use conversation for ordinary explanation-only questions that do not need repository architecture evidence.",
				"Prefer conversation for deliberative design or planning questions such as 'can you help me work out a design', 'how should we design', 'let's figure out how to build', or 'design a plan for' — these are about shaping ideas, not performing file changes. Only route to implementation or plan-execute families when the user gives an explicit directive to create or modify files now.",
				"If the prompt is a continuation like 'continue investigating', 'go deeper', or 'be more confident', keep the turn read-only unless the user now asks for edits.",
				"Prefer performance_review, security_review, or architecture when the user asks for that style of analysis without edits.",
				"Use contracts from this enum: direct_answer, grounded_explanation, locate_targets, review_findings, architecture_assessment, implementation_result.",
				"Use lanes from this enum: main, explore, verification, locate, review, architecture, implement.",
			}, "\n"),
		},
		{
			Role:    "user",
			Content: fmt.Sprintf("Mode: %s\nRecent conversation:\n%s\nCurrent user prompt:\n%s\n\nReturn JSON now.", mode.Name, history, classifierPrompt),
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
	effectiveClient := client
	text, err := client.CompleteChat(classifyCtx, messages, opts)
	if err != nil && client != m.client && m.client != nil {
		effectiveClient = m.client
		text, err = effectiveClient.CompleteChat(classifyCtx, messages, opts)
	}
	if err != nil {
		m.logRouteClassifierDecision(map[string]any{"status": "unavailable", "reason": "completion_error", "error": truncateImplementationDispatchText(err.Error(), 240)})
		return turnRoutePlan{}, false
	}
	plan, err := parseTurnRoutePlan(text)
	if err != nil {
		repaired, repairErr := m.repairTurnRouteClassification(ctx, effectiveClient, mode, classifierPrompt, text, err)
		if repairErr != nil {
			m.logRouteClassifierDecision(map[string]any{
				"status":       "parse_error",
				"error":        truncateImplementationDispatchText(err.Error(), 240),
				"repair_error": truncateImplementationDispatchText(repairErr.Error(), 240),
			})
			return turnRoutePlan{}, false
		}
		plan = repaired
	}
	plan.RouteSource = routeSourceClassifier
	plan = normalizeTurnRoutePlan(plan, mode)
	m.logRouteClassifierDecision(map[string]any{
		"status":               "accepted",
		"family":               plan.Family,
		"initial_lane":         plan.InitialLane,
		"reason":               plan.Reason,
		"contract":             plan.Contract,
		"requested_outcome":    plan.RequestedOutcome,
		"write_intent":         plan.WriteIntent,
		"read_only_constraint": plan.ReadOnlyConstraint,
		"evidence_intent":      plan.EvidenceIntent,
		"continuation_target":  plan.ContinuationTarget,
		"reason_codes":         plan.ReasonCodes,
	})
	return plan, plan.Family != ""
}

func (m *Model) repairTurnRouteClassification(ctx context.Context, client llama.LLMClient, mode agent.Mode, classifierPrompt string, invalid string, parseErr error) (turnRoutePlan, error) {
	if client == nil {
		return turnRoutePlan{}, fmt.Errorf("no classifier client")
	}
	timeout := 10 * time.Second
	if m != nil && m.cfg.RouteClassifier.TimeoutSeconds > 0 {
		timeout = time.Duration(m.cfg.RouteClassifier.TimeoutSeconds) * time.Second
	}
	repairCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	thinkingOff := false
	text, err := client.CompleteChat(repairCtx, []llama.ChatMessage{
		{
			Role: "system",
			Content: strings.Join([]string{
				"You repair a coding-agent route classifier response.",
				"Return strict JSON only. Do not solve the user's task.",
				"Choose family from: " + routeClassifierFamilyEnumList() + ".",
				"Use contracts from: direct_answer, grounded_explanation, locate_targets, review_findings, architecture_assessment, implementation_result.",
				"Use lanes from: main, explore, verification, locate, review, architecture, implement.",
				"Include requested_outcome, write_intent, read_only_constraint, evidence_intent, continuation_target, and reason_codes. requested_outcome enum: answer, diagnose, change_workspace, diagnose_then_change, verify, plan, review, locate. write_intent enum: yes, no, ambiguous. evidence_intent enum: generic, relation_trace, refactor. continuation_target enum: prior_change, current_prompt_only, none. Use conversation for read-only diagnosis and log inspection; do not route normal user prompts to debug_logs.",
			}, "\n"),
		},
		{
			Role:    "user",
			Content: fmt.Sprintf("Mode: %s\nCurrent user prompt:\n%s\n\nPrevious invalid classifier response:\n%s\n\nParse error: %s\n\nReturn one corrected JSON object now.", mode.Name, classifierPrompt, truncateImplementationDispatchText(invalid, 1200), parseErr),
		},
	}, llama.ChatOptions{
		MaxTokens:          192,
		Temperature:        0,
		TopP:               1,
		EnableThinking:     &thinkingOff,
		ChatTemplateKwargs: true,
	})
	if err != nil {
		return turnRoutePlan{}, err
	}
	return parseTurnRoutePlan(text)
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

func (m *Model) logTurnRouteCorrection(before, after turnRoutePlan) {
	if m == nil || !routePlanCorrectionChanged(before, after) {
		return
	}
	fields := map[string]any{
		"schema_version":       "idiom.route_classifier_correction.v1",
		"before_family":        before.Family,
		"before_initial_lane":  before.InitialLane,
		"before_contract":      before.Contract,
		"after_family":         after.Family,
		"after_initial_lane":   after.InitialLane,
		"after_contract":       after.Contract,
		"requested_outcome":    after.RequestedOutcome,
		"write_intent":         after.WriteIntent,
		"read_only_constraint": after.ReadOnlyConstraint,
		"evidence_intent":      after.EvidenceIntent,
		"continuation_target":  after.ContinuationTarget,
		"reason_codes":         after.ReasonCodes,
	}
	if body, err := json.Marshal(fields); err == nil {
		m.logMessage("route_classifier_correction", string(body))
	}
}

func routePlanCorrectionChanged(before, after turnRoutePlan) bool {
	return before.Family != after.Family ||
		before.InitialLane != after.InitialLane ||
		before.FollowupLane != after.FollowupLane ||
		before.Contract != after.Contract ||
		before.RequestedOutcome != after.RequestedOutcome ||
		before.WriteIntent != after.WriteIntent ||
		before.ReadOnlyConstraint != after.ReadOnlyConstraint ||
		before.EvidenceIntent != after.EvidenceIntent ||
		before.ContinuationTarget != after.ContinuationTarget
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

func routeClassifierPromptForModel(prompt string) string {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return ""
	}
	var out []string
	inFence := false
	omittedFence := false
	for _, line := range strings.Split(prompt, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			if !inFence {
				inFence = true
				if !omittedFence {
					out = append(out, "```")
					out = append(out, "[code block omitted for route classification]")
					out = append(out, "```")
					omittedFence = true
				}
				continue
			}
			inFence = false
			continue
		}
		if inFence {
			continue
		}
		out = append(out, line)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
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
	source := plan.RouteSource
	if source != "" {
		source = normalizeRouteSource(source)
	}
	plan.InitialLane = normalizeRouteValue(plan.InitialLane)
	plan.FollowupLane = normalizeRouteValue(plan.FollowupLane)
	plan.Reason = normalizeRouteValue(plan.Reason)
	plan.Contract = normalizeRouteValue(plan.Contract)
	plan.Family = normalizeRouteValue(plan.Family)
	plan.Thoroughness = normalizeRouteValue(plan.Thoroughness)
	plan.RequestedOutcome = normalizeRouteOutcome(plan.RequestedOutcome)
	plan.WriteIntent = normalizeRouteWriteIntent(plan.WriteIntent)
	plan.EvidenceIntent = normalizeRouteEvidenceIntent(plan.EvidenceIntent)
	plan.ContinuationTarget = normalizeRouteContinuationTarget(plan.ContinuationTarget)
	plan.ReasonCodes = normalizeRouteReasonCodes(plan.ReasonCodes)
	plan.RouteSource = source
	switch plan.Family {
	case "plan_execute", "plan_execute_planner", "plan_then_execute":
		plan.Family = string(workerruntime.TaskFamilyPlanThenExecutePlanner)
	}
	if plan.Family == "" {
		plan.Family = familyForRouteOutcome(plan.RequestedOutcome, plan.WriteIntent)
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
		plan.Family = "conversation"
		plan.InitialLane = "main"
		plan.FollowupLane = ""
		plan.Contract = "direct_answer"
		plan.Reason = util.FirstNonEmpty(plan.Reason, "legacy_debug_logs_collapsed_to_conversation")
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
	case string(workerruntime.TaskFamilyPlanThenExecutePlanner):
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
		plan.Reason = util.FirstNonEmpty(plan.Reason, string(workerruntime.TaskFamilyPlanThenExecutePlanner))
	case string(workerruntime.TaskFamilyPlanThenExecuteReview):
		// Post-executor review phase for `idiom plan-execute`. This is
		// review-shaped, not implementation-shaped: the model may close
		// with a checklist-backed pass, or use submit_plan only for
		// corrective edits found by the review.
		plan.InitialLane = "review"
		plan.FollowupLane = ""
		plan.Contract = "review_findings"
		plan.Reason = util.FirstNonEmpty(plan.Reason, string(workerruntime.TaskFamilyPlanThenExecuteReview))
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
	normalized := normalizeTurnRoutePlan(plan, mode)
	if promptRequestsRepositoryArchitectureEvidence(prompt) && !isImplementationLikeFamily(normalized.Family) {
		normalized.Family = "architecture"
		normalized.InitialLane = "architecture"
		normalized.FollowupLane = ""
		normalized.Contract = "architecture_assessment"
		normalized.WriteIntent = "no"
		normalized.ReadOnlyConstraint = true
		normalized.Reason = util.FirstNonEmpty(normalized.Reason, "route_prompt_requests_architecture_evidence")
		normalized.ReasonCodes = appendUniqueBriefStrings(normalized.ReasonCodes, "route_prompt_requests_architecture_evidence")
		normalized = normalizeTurnRoutePlan(normalized, mode)
	}
	if promptRequestsReadOnlyEvidenceReport(prompt) {
		normalized.RequestedOutcome = "diagnose"
		normalized.WriteIntent = "no"
		normalized.ReadOnlyConstraint = true
		normalized.Reason = util.FirstNonEmpty(normalized.Reason, "route_prompt_requests_evidence_report")
		normalized.ReasonCodes = appendUniqueBriefStrings(normalized.ReasonCodes, "route_prompt_requests_evidence_report")
		if isImplementationLikeFamily(normalized.Family) {
			return readOnlyRouteForOutcome(normalized, mode, "route_prompt_requests_evidence_report")
		}
		normalized = normalizeTurnRoutePlan(normalized, mode)
	}
	if corrected, ok := routePlanCorrectedByOutcomeContract(normalized, mode); ok {
		normalized = corrected
	}
	return normalized
}

func promptRequestsRepositoryArchitectureEvidence(prompt string) bool {
	words := plannerWordText(prompt)
	if words == " " || plannerPromptHasExplicitMutationIntent(words) {
		return false
	}
	if plannerWordsContainAnyWholePhrase(words,
		"how do i run",
		"how do we run",
		"how to run",
		"run the tests",
		"run tests",
	) {
		return false
	}
	internalSurface := plannerWordsContainAny(words,
		"answer composer",
		"control rail",
		"control rails",
		"debug log",
		"debug logs",
		"discovery tool",
		"discovery tools",
		"harness",
		"runner",
		"subagent",
		"subagents",
		"sub agent",
		"sub agents",
	)
	if !internalSurface {
		return false
	}
	return plannerWordsContainAny(words,
		"architecture",
		"boundary",
		"how",
		"overall usefulness",
		"usefulness",
		"uses",
		"what can we add",
		"what does",
		"works",
	)
}

func routePlanCorrectedByOutcomeContract(plan turnRoutePlan, mode agent.Mode) (turnRoutePlan, bool) {
	if plan.ReadOnlyConstraint || plan.WriteIntent == "no" {
		if isImplementationLikeFamily(plan.Family) {
			return readOnlyRouteForOutcome(plan, mode, "route_outcome_read_only_constraint"), true
		}
		return plan, false
	}
	switch plan.RequestedOutcome {
	case "change_workspace":
		if !isImplementationLikeFamily(plan.Family) && plan.WriteIntent != "no" {
			plan.Family = "implementation"
			plan.InitialLane = "implement"
			plan.FollowupLane = ""
			plan.Contract = "implementation_result"
			plan.WriteIntent = "yes"
			plan.Reason = util.FirstNonEmpty(plan.Reason, "route_outcome_change_workspace")
			plan.ReasonCodes = appendUniqueBriefStrings(plan.ReasonCodes, "route_outcome_change_workspace")
			return normalizeTurnRoutePlan(plan, mode), true
		}
	case "diagnose_then_change":
		if !isImplementationLikeFamily(plan.Family) {
			plan.Family = "explore_then_implement"
			plan.InitialLane = "explore"
			plan.FollowupLane = "implement"
			plan.Contract = "implementation_result"
			plan.WriteIntent = "yes"
			plan.Reason = util.FirstNonEmpty(plan.Reason, "route_outcome_diagnose_then_change")
			plan.ReasonCodes = appendUniqueBriefStrings(plan.ReasonCodes, "route_outcome_diagnose_then_change")
			return normalizeTurnRoutePlan(plan, mode), true
		}
	case "verify":
		if plan.Family != "verification_only" && plan.WriteIntent != "yes" {
			plan.Family = "verification_only"
			plan.InitialLane = "main"
			plan.FollowupLane = ""
			plan.Contract = "direct_answer"
			plan.Reason = util.FirstNonEmpty(plan.Reason, "route_outcome_verify")
			plan.ReasonCodes = appendUniqueBriefStrings(plan.ReasonCodes, "route_outcome_verify")
			return normalizeTurnRoutePlan(plan, mode), true
		}
	}
	if plan.WriteIntent == "yes" && !isImplementationLikeFamily(plan.Family) {
		plan.Family = "implementation"
		plan.InitialLane = "implement"
		plan.FollowupLane = ""
		plan.Contract = "implementation_result"
		plan.RequestedOutcome = util.FirstNonEmpty(plan.RequestedOutcome, "change_workspace")
		plan.Reason = util.FirstNonEmpty(plan.Reason, "route_write_intent_yes")
		plan.ReasonCodes = appendUniqueBriefStrings(plan.ReasonCodes, "route_write_intent_yes")
		return normalizeTurnRoutePlan(plan, mode), true
	}
	return plan, false
}

func readOnlyRouteForOutcome(plan turnRoutePlan, mode agent.Mode, reasonCode string) turnRoutePlan {
	switch plan.RequestedOutcome {
	case "diagnose":
		plan.Family = "conversation"
	case "verify":
		plan.Family = "verification_only"
	case "locate":
		plan.Family = "locate"
	default:
		plan.Family = "conversation"
	}
	plan.InitialLane = ""
	plan.FollowupLane = ""
	plan.Contract = ""
	plan.WriteIntent = "no"
	plan.Reason = util.FirstNonEmpty(plan.Reason, reasonCode)
	plan.ReasonCodes = appendUniqueBriefStrings(plan.ReasonCodes, reasonCode)
	return normalizeTurnRoutePlan(plan, mode)
}

func promptRequestsReadOnlyEvidenceReport(prompt string) bool {
	words := plannerWordText(prompt)
	if words == " " {
		return false
	}
	if plannerPromptHasExplicitMutationIntent(words) {
		return false
	}
	hasDiagnosisVerb := plannerWordsContainAny(words,
		"debug", "diagnose", "investigate", "research", "look into", "root cause",
	)
	hasReportContract := plannerWordsContainAny(words,
		"present findings",
		"provide findings",
		"report findings",
		"structured evidence",
		"provide evidence",
		"files line numbers",
		"line numbers",
		"relevant symbols",
		"root cause analysis",
		"research assistant",
	)
	return hasDiagnosisVerb && hasReportContract
}

func plannerPromptHasExplicitMutationIntent(words string) bool {
	if plannerWordsContainAnyWholePhrase(words,
		"apply patch", "apply patches",
		"apply change", "apply changes",
		"apply edit", "apply edits",
		"apply fix", "apply fixes",
		"add test", "add tests",
		"add doc", "add docs",
		"add config", "add configs",
		"add file", "add files",
		"make change", "make changes", "make the change", "make the changes",
		"make code change", "make code changes",
	) {
		return true
	}
	if plannerWordsContainVerbObjectWithin(words, "add", 5,
		"test", "tests",
		"doc", "docs",
		"config", "configs",
		"file", "files",
	) {
		return true
	}
	if plannerWordsContainVerbObjectWithin(words, "write", 5,
		"test", "tests",
		"doc", "docs",
		"config", "configs",
		"file", "files",
		"code",
		"patch", "patches",
	) {
		return true
	}
	if plannerWordsContainDirectiveVerb(words,
		"fix", "fixing",
		"repair", "repairing",
		"implement", "implementing",
		"change", "changing",
		"edit", "editing",
		"modify", "modifying",
		"update", "updating",
		"patch", "patching",
		"commit", "committing",
		"refactor", "refactoring",
		"remove", "removing",
		"rename", "renaming",
		"delete", "deleting",
		"create", "creating",
		"apply", "applying",
		"add", "adding",
	) {
		return true
	}
	return false
}

func plannerWordsContainDirectiveVerb(words string, verbs ...string) bool {
	if len(verbs) == 0 {
		return false
	}
	verbSet := make(map[string]struct{}, len(verbs))
	for _, verb := range verbs {
		verb = strings.TrimSpace(plannerWordText(verb))
		if verb != "" {
			verbSet[verb] = struct{}{}
		}
	}
	fields := strings.Fields(plannerWordText(words))
	for i, field := range fields {
		if _, ok := verbSet[field]; !ok {
			continue
		}
		if plannerWordAtDirectiveBoundary(fields, i) {
			return true
		}
	}
	return false
}

func plannerWordAtDirectiveBoundary(fields []string, idx int) bool {
	if idx == 0 {
		return true
	}
	switch fields[idx-1] {
	case "please", "pls", "then", "also", "now", "start", "begin":
		return true
	}
	if idx >= 2 && fields[idx-1] == "and" {
		switch fields[idx-2] {
		case "debug", "diagnose", "investigate", "research", "find", "locate", "identify", "inspect":
			return true
		}
	}
	if idx >= 3 && fields[idx-2] == "you" && fields[idx-1] == "to" {
		switch fields[idx-3] {
		case "need", "want", "ask":
			return true
		}
	}
	if idx >= 2 && fields[idx-1] == "you" {
		switch fields[idx-2] {
		case "can", "could", "would", "will":
			return true
		}
	}
	return false
}

func plannerWordsContainVerbObjectWithin(words, verb string, maxDistance int, objects ...string) bool {
	if strings.TrimSpace(verb) == "" || maxDistance < 1 || len(objects) == 0 {
		return false
	}
	objectSet := make(map[string]struct{}, len(objects))
	for _, object := range objects {
		object = strings.TrimSpace(plannerWordText(object))
		if object != "" {
			objectSet[object] = struct{}{}
		}
	}
	if len(objectSet) == 0 {
		return false
	}
	fields := strings.Fields(plannerWordText(words))
	verb = strings.TrimSpace(plannerWordText(verb))
	for i, field := range fields {
		if field != verb {
			continue
		}
		limit := min(len(fields), i+maxDistance+1)
		for _, candidate := range fields[i+1 : limit] {
			if _, ok := objectSet[candidate]; ok {
				return true
			}
		}
	}
	return false
}

func plannerWordsContainAnyWholePhrase(words string, phrases ...string) bool {
	words = plannerWordText(words)
	for _, phrase := range phrases {
		if strings.Contains(words, plannerWordText(phrase)) {
			return true
		}
	}
	return false
}

func plannerWordsContainAny(words string, phrases ...string) bool {
	words = strings.ToLower(words)
	for _, phrase := range phrases {
		phraseWords := plannerWordText(phrase)
		if strings.Contains(words, strings.TrimSpace(phraseWords)) {
			return true
		}
	}
	return false
}

func normalizeRouteOutcome(value string) string {
	switch normalizeRouteValue(value) {
	case "answer", "direct_answer", "conversation":
		return "answer"
	case "diagnose", "debug", "debug_logs", "investigate", "explain_failure":
		return "diagnose"
	case "change", "change_workspace", "implementation", "implement", "repair", "fix":
		return "change_workspace"
	case "diagnose_then_change", "investigate_then_implement", "explore_then_implement", "repair_after_diagnosis":
		return "diagnose_then_change"
	case "verify", "verification", "verification_only", "test":
		return "verify"
	case "plan", "planner", "plan_then_execute", string(workerruntime.TaskFamilyPlanThenExecutePlanner):
		return "plan"
	case "review", "security_review", "performance_review", "architecture":
		return "review"
	case "locate", "find":
		return "locate"
	default:
		return ""
	}
}

func normalizeRouteWriteIntent(value string) string {
	switch normalizeRouteValue(value) {
	case "yes", "true", "write", "edit", "mutate", "mutation":
		return "yes"
	case "no", "false", "none", "read_only", "readonly":
		return "no"
	case "ambiguous", "unknown", "unclear":
		return "ambiguous"
	default:
		return ""
	}
}

func normalizeRouteEvidenceIntent(value string) string {
	switch normalizeRouteValue(value) {
	case "relation_trace", "relation", "trace", "calls", "call_trace", "reference_trace", "handoff_trace", "usage_trace":
		return "relation_trace"
	case "refactor", "refactoring", "move", "extract", "split", "rename", "reshape", "reorganize":
		return "refactor"
	case "generic", "general", "none", "unknown":
		return "generic"
	default:
		return ""
	}
}

func normalizeRouteContinuationTarget(value string) string {
	switch normalizeRouteValue(value) {
	case "prior_change", "previous_change", "last_change", "previous_fix", "prior_fix":
		return "prior_change"
	case "current_prompt_only", "current_prompt", "current":
		return "current_prompt_only"
	case "none", "no":
		return "none"
	default:
		return ""
	}
}

func normalizeRouteReasonCodes(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = normalizeRouteValue(value)
		if value != "" && !slices.Contains(out, value) {
			out = append(out, value)
		}
	}
	return out
}

func familyForRouteOutcome(outcome, writeIntent string) string {
	if writeIntent == "yes" {
		return "implementation"
	}
	switch outcome {
	case "answer":
		return "conversation"
	case "diagnose":
		return "conversation"
	case "change_workspace":
		return "implementation"
	case "diagnose_then_change":
		return "explore_then_implement"
	case "verify":
		return "verification_only"
	case "plan":
		return string(workerruntime.TaskFamilyPlanThenExecutePlanner)
	case "review":
		return "security_review"
	case "locate":
		return "locate"
	default:
		return ""
	}
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
		return normalizeTurnRoutePlan(turnRoutePlan{Family: "architecture", RouteSource: routeSourceConservativeFallback}, mode)
	}
	return normalizeTurnRoutePlan(turnRoutePlan{Family: "conversation", RouteSource: routeSourceConservativeFallback}, mode)
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
		string(workerruntime.TaskFamilyPlanThenExecutePlanner):
		return true
	default:
		return false
	}
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
	if plan.RequestedOutcome != "" {
		parts = append(parts, "requested_outcome="+plan.RequestedOutcome)
	}
	if plan.WriteIntent != "" {
		parts = append(parts, "write_intent="+plan.WriteIntent)
	}
	if plan.ReadOnlyConstraint {
		parts = append(parts, "read_only_constraint=true")
	}
	if plan.EvidenceIntent != "" {
		parts = append(parts, "evidence_intent="+plan.EvidenceIntent)
	}
	if plan.ContinuationTarget != "" {
		parts = append(parts, "continuation_target="+plan.ContinuationTarget)
	}
	if len(plan.ReasonCodes) > 0 {
		parts = append(parts, "reason_codes="+strings.Join(plan.ReasonCodes, ","))
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
	brief.RequestedOutcome = plan.RequestedOutcome
	brief.WriteIntent = plan.WriteIntent
	brief.ReadOnlyConstraint = plan.ReadOnlyConstraint
	brief.EvidenceIntent = plan.EvidenceIntent
	return workerruntime.NormalizeRuntimeBrief(brief)
}

func (m *Model) workspaceAllowedTools() []string {
	if m == nil || m.workspace == nil {
		return nil
	}
	return m.workspace.AllowedTools()
}

func normalizeRouteValue(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "-", "_")
	value = strings.ReplaceAll(value, " ", "_")
	return value
}
