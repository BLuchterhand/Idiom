package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"qwen-bubbles/internal/agent"
	"qwen-bubbles/internal/llama"
	"qwen-bubbles/internal/promptstack"
	"qwen-bubbles/internal/workspace"
	"strings"
	"time"
)

type analysisLane string

type promptRouteReason string

type answerContract string

type promptExecutionPlan struct {
	lane              analysisLane
	followupLane      analysisLane
	thoroughness      workspace.ExploreThoroughness
	reason            promptRouteReason
	contract          answerContract
	taskFamily        string
	permissionProfile string
	agentMode         agentDecisionMode
	intent            promptIntentDecision
}

type promptIntentDecision struct {
	Intent         string   `json:"intent,omitempty"`
	SelectedLane   string   `json:"selected_lane,omitempty"`
	FollowupLane   string   `json:"followup_lane,omitempty"`
	WantsEdits     bool     `json:"wants_edits"`
	WantsPlanOnly  bool     `json:"wants_plan_only"`
	WantsVerify    bool     `json:"wants_verify"`
	Risk           string   `json:"risk,omitempty"`
	Confidence     string   `json:"confidence,omitempty"`
	Signals        []string `json:"signals,omitempty"`
	Vetoes         []string `json:"vetoes,omitempty"`
	UserBoundaries []string `json:"user_boundaries,omitempty"`
	TargetRefs     []string `json:"target_refs,omitempty"`
	AgentMode      string   `json:"agent_mode,omitempty"`
	Fallback       string   `json:"fallback,omitempty"`
	Source         string   `json:"source,omitempty"`
}

type promptIntentVerifierResult struct {
	PrimaryIntent       string   `json:"primary_intent,omitempty"`
	SecondaryIntents    []string `json:"secondary_intents,omitempty"`
	WantsEdits          bool     `json:"wants_edits"`
	WantsPlanOnly       bool     `json:"wants_plan_only"`
	NeedsVerification   bool     `json:"needs_verification"`
	ApprovalDetected    bool     `json:"approval_detected"`
	Risk                string   `json:"risk,omitempty"`
	Confidence          string   `json:"confidence,omitempty"`
	Reason              string   `json:"reason,omitempty"`
	RecommendedLane     string   `json:"recommended_lane,omitempty"`
	RecommendedFollowup string   `json:"recommended_followup_lane,omitempty"`
	RecommendedContract string   `json:"recommended_contract,omitempty"`
	TaskFamily          string   `json:"task_family,omitempty"`
	PermissionProfile   string   `json:"permission_profile,omitempty"`
	RequiredBoundaries  []string `json:"required_boundaries,omitempty"`
	TargetRefs          []string `json:"target_refs,omitempty"`
}

type promptIntentReconciliationSnapshot struct {
	Triggered        bool     `json:"triggered"`
	Reasons          []string `json:"reasons,omitempty"`
	HostLane         string   `json:"host_lane,omitempty"`
	HostFollowupLane string   `json:"host_followup_lane,omitempty"`
	VerifierLane     string   `json:"verifier_lane,omitempty"`
	VerifierFollowup string   `json:"verifier_followup_lane,omitempty"`
	FinalLane        string   `json:"final_lane,omitempty"`
	FinalFollowup    string   `json:"final_followup_lane,omitempty"`
	Changed          bool     `json:"changed"`
	SafetyOverride   bool     `json:"safety_override"`
	Notes            []string `json:"notes,omitempty"`
}

type laneContractPolicy struct {
	Contract  answerContract
	Expected  []string
	Forbidden []string
	Fallback  string
}

func (m *Model) RunPromptOnce(ctx context.Context, prompt string) (string, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return "", fmt.Errorf("prompt is required")
	}
	if err := m.ensureServerReady(ctx); err != nil {
		return "", err
	}
	plan := m.classifyPromptExecutionPlan(prompt)
	if m.workspace != nil {
		m.workspace.PrimeDiscoveredPaths(promptMentionedWorkspacePaths(prompt))
	}
	m.resetCompactTrace()
	m.resetExecutionPanel(prompt, plan)
	m.messages = append(m.messages, llama.ChatMessage{Role: "user", Content: prompt})
	m.logMessage("user", prompt)
	if summary := formatPromptExecutionPlan(plan); summary != "" {
		m.logMessage("route_plan", summary)
	}
	reply, err := m.runPromptPlan(ctx, prompt, m.requestMessages(), m.chatOptions(), plan)
	if err != nil {
		return "", err
	}
	m.messages = append(m.messages, llama.ChatMessage{Role: "assistant", Content: reply})
	m.logMessage("assistant", reply)
	return reply, nil
}

func (m *Model) lastUserPrompt() string {
	if m == nil {
		return ""
	}
	for i := len(m.messages) - 1; i >= 0; i-- {
		if normalizeHistoryRole(m.messages[i].Role) == "user" && strings.TrimSpace(m.messages[i].Content) != "" {
			return strings.TrimSpace(m.messages[i].Content)
		}
	}
	return ""
}

func routeReasonForLane(lane analysisLane) promptRouteReason {
	switch lane {
	case analysisLaneLocate:
		return promptRouteReasonLocateRequest
	case analysisLaneExplore:
		return promptRouteReasonExploreRequest
	case analysisLaneReview:
		return promptRouteReasonReviewRequest
	case analysisLaneArchitecture:
		return promptRouteReasonArchitectureRequest
	case analysisLaneImplement:
		return promptRouteReasonDirectImplementation
	default:
		return promptRouteReasonDirectConversation
	}
}

func (m *Model) sendPromptAsync(prompt string) {
	if looksLikeResumeStructuralRefactorPrompt(prompt) && m.canResumeStructuralRefactor() {
		m.resumeStructuralRefactorAsync(prompt)
		return
	}
	if shouldResumeTaskMemoryPrompt(prompt, m.currentTaskMemory()) {
		m.resumeTaskMemoryAsync(prompt)
		return
	}
	plan := m.classifyPromptExecutionPlan(prompt)
	if m.workspace != nil {
		m.workspace.PrimeDiscoveredPaths(promptMentionedWorkspacePaths(prompt))
	}
	m.resetCompactTrace()
	m.resetExecutionPanel(prompt, plan)

	m.messages = append(m.messages, llama.ChatMessage{Role: "user", Content: prompt})
	m.messages = append(m.messages, llama.ChatMessage{Role: "assistant", Content: ""})
	m.logMessage("user", prompt)
	if summary := formatPromptExecutionPlan(plan); summary != "" {
		m.logMessage("route_plan", summary)
	}
	m.streaming = true
	m.workingFrame = 0
	m.clearRuntimeError()
	switch plan.lane {
	case analysisLaneReview:
		m.setPhaseCard(analysisLaneReview, "triage", phaseStateActive, "Gathering bounded evidence and ranking likely findings.")
		if routeContinuesToImplement(plan) {
			m.status = "Review agent is investigating before Qwen applies changes."
		} else {
			m.status = "Review agent is triaging the codebase."
		}
	case analysisLaneExplore:
		if routeContinuesToImplement(plan) {
			m.status = "Explore agent is investigating before Qwen applies changes."
		} else {
			m.status = "Explore agent is investigating the codebase."
		}
	case analysisLaneLocate:
		m.status = "Locate agent is identifying the requested code."
	case analysisLaneImplement:
		m.setPhaseCard(analysisLaneImplement, "targeting", phaseStateActive, "Consuming the target packet and preparing a bounded edit.")
		m.status = "Implementation agent is applying your requested change."
	default:
		m.setPhaseCard("", "", "", "")
		m.status = "Qwen is working on your request."
	}
	m.renderMessages(true)

	history := m.requestMessages()
	opts := m.chatOptions()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(m.cfg.Chat.RequestTimeoutSeconds)*time.Second)
		defer cancel()

		reply, err := m.runPromptPlan(ctx, prompt, history, opts, plan)
		if err == nil {
			m.emitAssistantReply(reply)
		}
		m.events <- streamDoneMsg{err: err}
	}()
}

func promptMentionedWorkspacePaths(prompt string) []string {
	if strings.TrimSpace(prompt) == "" {
		return nil
	}
	seen := make(map[string]struct{})
	paths := make([]string, 0, 4)
	for _, token := range strings.Fields(prompt) {
		candidate := strings.Trim(token, "`'\"()[]{}<>,:;!?")
		candidate = strings.TrimPrefix(candidate, "@")
		candidate = strings.TrimSpace(strings.TrimPrefix(candidate, "./"))
		candidate = strings.TrimRight(candidate, ".")
		lowerCandidate := strings.ToLower(candidate)
		if strings.HasPrefix(lowerCandidate, "destination:") {
			continue
		}
		for _, prefix := range []string{"path:", "file:", "source:"} {
			if strings.HasPrefix(lowerCandidate, prefix) {
				candidate = strings.TrimSpace(candidate[len(prefix):])
				candidate = strings.Trim(candidate, "`'\"")
				break
			}
		}
		if !looksLikeWorkspacePathToken(candidate) {
			continue
		}
		candidate = filepath.Clean(candidate)
		if candidate == "." {
			continue
		}
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		paths = append(paths, candidate)
	}
	return paths
}

func formatPromptExecutionPlan(plan promptExecutionPlan) string {
	contract := promptPlanAnswerContract(plan)
	if plan.lane == "" && plan.followupLane == "" && plan.reason == "" && contract == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("initial=")
	b.WriteString(string(plan.lane))
	if plan.followupLane != "" {
		b.WriteString(" followup=")
		b.WriteString(string(plan.followupLane))
	}
	if plan.reason != "" {
		b.WriteString(" reason=")
		b.WriteString(string(plan.reason))
	}
	if contract != "" {
		b.WriteString(" contract=")
		b.WriteString(string(contract))
	}
	if plan.taskFamily != "" {
		b.WriteString(" family=")
		b.WriteString(plan.taskFamily)
	}
	if plan.permissionProfile != "" {
		b.WriteString(" permissions=")
		b.WriteString(plan.permissionProfile)
	}
	if plan.agentMode != "" {
		b.WriteString(" mode=")
		b.WriteString(string(plan.agentMode))
	}
	if plan.intent.Intent != "" {
		b.WriteString(" intent=")
		b.WriteString(plan.intent.Intent)
	}
	if plan.intent.Confidence != "" {
		b.WriteString(" confidence=")
		b.WriteString(plan.intent.Confidence)
	}
	if plan.intent.Risk != "" {
		b.WriteString(" risk=")
		b.WriteString(plan.intent.Risk)
	}
	if plan.thoroughness != "" {
		b.WriteString(" thoroughness=")
		b.WriteString(string(plan.thoroughness))
	}
	return b.String()
}

func promptPlanAnswerContract(plan promptExecutionPlan) answerContract {
	if plan.contract != "" {
		return plan.contract
	}
	return answerContractForLane(plan.lane)
}

func answerContractForLane(lane analysisLane) answerContract {
	switch lane {
	case analysisLaneLocate:
		return answerContractLocateTargets
	case analysisLaneExplore:
		return answerContractGroundedExplanation
	case analysisLaneReview:
		return answerContractReviewFindings
	case analysisLaneArchitecture:
		return answerContractArchitecture
	case analysisLaneImplement:
		return answerContractImplementation
	case analysisLaneMain:
		return answerContractDirectConversation
	default:
		return ""
	}
}

func laneContractPolicyForLane(lane analysisLane) laneContractPolicy {
	policy := laneContractPolicy{Contract: answerContractForLane(lane)}
	switch lane {
	case analysisLaneLocate:
		policy.Expected = []string{"exact file path", "line number or focused snippet", "why this is the best target"}
		policy.Forbidden = []string{"repo-wide speculation", "edits"}
		policy.Fallback = "If no exact target is found, say what was searched and name the next focused search."
	case analysisLaneExplore:
		policy.Expected = []string{"major files or packages involved", "what was verified", "what is inferred", "confidence", "next validation step"}
		policy.Forbidden = []string{"edits", "ungrounded architecture advice", "claims without cited paths"}
		policy.Fallback = "If evidence is weak, return the concrete paths inspected and the narrowest next read/search."
	case analysisLaneReview:
		policy.Expected = []string{"one strongest finding", "exact file path", "why it is a real issue or cleanup", "evidence", "confidence"}
		policy.Forbidden = []string{"structure-only advice", "multiple weak findings", "edits"}
		policy.Fallback = "If no bounded finding is supported, say the evidence is inconclusive and stop before editing."
	case analysisLaneArchitecture:
		policy.Expected = []string{"current structural shape", "top structural opportunities", "exact hotspot paths", "boundary or ownership direction", "confidence"}
		policy.Forbidden = []string{"bug-triage framing", "line-specific defect requirement", "edits inside the analysis lane"}
		policy.Fallback = "If model synthesis is weak, use deterministic hotspot evidence for a grounded structural summary."
	case analysisLaneImplement:
		policy.Expected = []string{"changed files", "outcome", "verification status"}
		policy.Forbidden = []string{"broad discovery after target packet", "unbounded refactor", "rewriting unrelated files"}
		policy.Fallback = "If the target is unclear, inspect the bounded target packet before widening."
	case analysisLaneMain:
		policy.Expected = []string{"direct answer"}
		policy.Forbidden = []string{"workspace writes unless routed to implement"}
	}
	return policy
}

func renderLaneContractPolicy(lane analysisLane) string {
	policy := laneContractPolicyForLane(lane)
	if policy.Contract == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("Answer contract: ")
	b.WriteString(string(policy.Contract))
	if len(policy.Expected) > 0 {
		b.WriteString("\nExpected output:")
		for _, item := range policy.Expected {
			b.WriteString("\n- ")
			b.WriteString(item)
		}
	}
	if len(policy.Forbidden) > 0 {
		b.WriteString("\nForbidden:")
		for _, item := range policy.Forbidden {
			b.WriteString("\n- ")
			b.WriteString(item)
		}
	}
	if strings.TrimSpace(policy.Fallback) != "" {
		b.WriteString("\nFallback policy:\n- ")
		b.WriteString(strings.TrimSpace(policy.Fallback))
	}
	return b.String()
}

func (m *Model) runPromptPlan(
	ctx context.Context,
	prompt string,
	history []llama.ChatMessage,
	opts llama.ChatOptions,
	plan promptExecutionPlan,
	resumeContexts ...taskResumeContext,
) (string, error) {
	var resumeContext taskResumeContext
	if len(resumeContexts) > 0 {
		resumeContext = resumeContexts[0]
	}
	plan = m.normalizePromptPlanThoroughness(plan)
	hostRails := buildDeterministicControlRails(prompt, plan)
	if verifiedPlan, ok := m.verifyAndReconcilePromptPlan(ctx, prompt, history, opts, plan, hostRails); ok {
		plan = m.normalizePromptPlanThoroughness(verifiedPlan)
		if summary := formatPromptExecutionPlan(plan); summary != "" {
			m.logMessage("route_plan_final", summary)
		}
	}
	effort := buildEffortPolicySnapshot(plan, m.cfg.Chat.MaxToolSteps, m.cfg.Chat.RequestTimeoutSeconds, m.cfg.Chat.MaxTokens, m.cfg.LlamaServer.ReasoningBudget)
	m.logEffortPolicy(effort)
	m.logRouteAmbiguity(buildRouteAmbiguitySnapshot(prompt, plan))
	rails := buildDeterministicControlRails(prompt, plan)
	m.logDeterministicControlRails(rails)
	if policy := renderDynamicToolPolicySnapshot(rails); policy != "" {
		m.logMessage("tool_policy", policy)
	}
	switch plan.lane {
	case analysisLaneLocate:
		summary, err := m.runLocateAgent(ctx, prompt)
		if err != nil {
			return "", err
		}
		m.logMessage("locate_summary", summary)
		if next, ok := analysisTransition(plan, analysisLaneLocate); !ok || next != analysisLaneImplement {
			m.rememberAnalysisTaskMemory(prompt, analysisLaneLocate, summary, promptMentionedWorkspacePaths(summary), "resume locate with a narrower target search before editing")
			m.applyAnalysisAnswerCritic(summary, analysisLaneLocate, promptMentionedWorkspacePaths(summary))
			return m.applyControlRailsFinalAnswer(rails, summary, promptMentionedWorkspacePaths(summary), nil, false), nil
		}
		m.events <- infoMsg{text: "Locate agent found the target. Qwen is applying the requested change."}
		m.emitPhaseCard(analysisLaneImplement, "targeting", phaseStateActive, "Locate finished. Reusing the located target for the bounded edit.")
		return m.runImplementAgent(ctx, prompt, appendAnalysisSummary(history, "locate", summary), opts)
	case analysisLaneArchitecture:
		m.emitPhaseCard(analysisLaneArchitecture, "mapping", phaseStateActive, "Gathering structural evidence and ranking architecture hotspots.")
		outcome, err := m.runArchitectureSubagent(ctx, prompt, opts, plan.thoroughness, plan.intent.TargetRefs)
		if err != nil {
			return "", err
		}
		summary := outcome.Summary
		m.logMessage("architecture_summary", summary)
		m.emitPhaseCard(analysisLaneArchitecture, "complete", phaseStateDone, "Finished architecture synthesis from grounded hotspot evidence.")
		if next, ok := analysisTransition(plan, analysisLaneArchitecture); !ok || next != analysisLaneImplement {
			nextAction := "resume architecture validation from the cited hotspots"
			if target, reason, ok := analysisReplanRecommendationWithEvidence(analysisLaneArchitecture, summary, outcome.EvidencePaths); ok {
				m.logMessage("replan_recommendation", fmt.Sprintf("architecture -> %s: %s", target, reason))
				m.rememberAnalysisReplan(analysisLaneArchitecture, target, reason, summary)
				summary = appendAnalysisReplanRecommendation(summary, target, reason)
				nextAction = taskMemoryNextActionForReplan(target, reason)
			}
			m.rememberAnalysisTaskMemory(prompt, analysisLaneArchitecture, summary, outcome.EvidencePaths, nextAction)
			m.applyAnalysisAnswerCritic(summary, analysisLaneArchitecture, outcome.EvidencePaths)
			return m.applyControlRailsFinalAnswer(rails, summary, outcome.EvidencePaths, nil, false), nil
		}
		m.events <- infoMsg{text: "Architecture agent finished. Qwen is applying changes from that structural plan."}
		m.emitPhaseCard(analysisLaneImplement, "targeting", phaseStateActive, "Architecture mapping finished. Preparing the bounded implementation plan.")
		return m.runImplementAgent(ctx, prompt, appendAnalysisSummary(history, "architecture", summary), opts)
	case analysisLaneReview:
		m.emitPhaseCard(analysisLaneReview, "triage", phaseStateActive, "Gathering evidence and ranking candidate files.")
		outcome, err := m.runReviewSubagent(ctx, prompt, opts, plan.thoroughness)
		if err != nil {
			return "", err
		}
		summary := outcome.Summary
		m.logMessage("review_summary", summary)
		if outcome.Finding != nil {
			m.emitPhaseCard(analysisLaneReview, "complete", phaseStateDone, "Confirmed a review finding with bounded evidence.")
		} else {
			m.emitPhaseCard(analysisLaneReview, "complete", phaseStateDone, "Finished review evidence gathering without a bounded finding.")
		}
		if next, ok := analysisTransition(plan, analysisLaneReview); !ok || next != analysisLaneImplement {
			nextAction := "resume review from the confirmed candidate evidence before widening search"
			if target, reason, ok := analysisReplanRecommendationWithEvidence(analysisLaneReview, summary, outcome.EvidencePaths); ok {
				m.logMessage("replan_recommendation", fmt.Sprintf("review -> %s: %s", target, reason))
				m.rememberAnalysisReplan(analysisLaneReview, target, reason, summary)
				summary = appendAnalysisReplanRecommendation(summary, target, reason)
				nextAction = taskMemoryNextActionForReplan(target, reason)
			}
			m.rememberAnalysisTaskMemory(prompt, analysisLaneReview, summary, outcome.EvidencePaths, nextAction)
			m.applyAnalysisAnswerCritic(summary, analysisLaneReview, outcome.EvidencePaths)
			return m.applyControlRailsFinalAnswer(rails, summary, outcome.EvidencePaths, nil, false), nil
		}
		if outcome.Finding == nil {
			m.events <- infoMsg{text: "Review did not confirm a bounded actionable finding. Qwen is stopping before auto-editing."}
			nextAction := "resume review with narrower evidence before any implementation"
			if target, reason, ok := analysisReplanRecommendationWithEvidence(analysisLaneReview, summary, outcome.EvidencePaths); ok {
				m.logMessage("replan_recommendation", fmt.Sprintf("review -> %s: %s", target, reason))
				m.rememberAnalysisReplan(analysisLaneReview, target, reason, summary)
				summary = appendAnalysisReplanRecommendation(summary, target, reason)
				nextAction = taskMemoryNextActionForReplan(target, reason)
			}
			m.rememberAnalysisTaskMemory(prompt, analysisLaneReview, summary, outcome.EvidencePaths, nextAction)
			m.applyAnalysisAnswerCritic(summary, analysisLaneReview, outcome.EvidencePaths)
			return m.applyControlRailsFinalAnswer(rails, summary, outcome.EvidencePaths, nil, false), nil
		}
		if !reviewFindingAllowsAutoApply(*outcome.Finding) {
			m.events <- infoMsg{text: "Review found a larger-scope change. Qwen is stopping at the plan instead of auto-editing."}
			m.rememberAnalysisTaskMemory(prompt, analysisLaneReview, summary, outcome.EvidencePaths, "resume only after scoping or approving the larger change")
			m.applyAnalysisAnswerCritic(summary, analysisLaneReview, outcome.EvidencePaths)
			return m.applyControlRailsFinalAnswer(rails, summary, outcome.EvidencePaths, nil, false), nil
		}
		m.events <- infoMsg{text: "Review agent finished. Qwen is applying changes from that review."}
		m.emitPhaseCard(analysisLaneImplement, "targeting", phaseStateActive, "Review finished. Preparing the bounded implementation plan.")
		return m.runImplementAgent(ctx, prompt, appendAnalysisSummary(history, "review", summary), opts)
	case analysisLaneExplore:
		outcome, err := m.runExploreSubagent(ctx, prompt, opts, plan.thoroughness)
		if err != nil {
			return "", err
		}
		summary := outcome.Summary
		m.logMessage("explore_summary", summary)
		if next, ok := analysisTransition(plan, analysisLaneExplore); !ok || next != analysisLaneImplement {
			nextAction := "resume exploration from the cited evidence paths before broadening search"
			if target, reason, ok := analysisReplanRecommendationWithEvidence(analysisLaneExplore, summary, outcome.EvidencePaths); ok {
				m.logMessage("replan_recommendation", fmt.Sprintf("explore -> %s: %s", target, reason))
				m.rememberAnalysisReplan(analysisLaneExplore, target, reason, summary)
				summary = appendAnalysisReplanRecommendation(summary, target, reason)
				nextAction = taskMemoryNextActionForReplan(target, reason)
			}
			m.rememberAnalysisTaskMemory(prompt, analysisLaneExplore, summary, outcome.EvidencePaths, nextAction)
			m.applyAnalysisAnswerCritic(summary, analysisLaneExplore, outcome.EvidencePaths)
			return m.applyControlRailsFinalAnswer(rails, summary, outcome.EvidencePaths, nil, false), nil
		}
		m.events <- infoMsg{text: "Explore agent finished. Qwen is applying changes from that summary."}
		m.emitPhaseCard(analysisLaneImplement, "targeting", phaseStateActive, "Explore finished. Preparing the bounded implementation plan.")
		return m.runImplementAgent(ctx, prompt, appendAnalysisSummary(history, "explore", summary), opts)
	case analysisLaneImplement:
		m.emitPhaseCard(analysisLaneImplement, "targeting", phaseStateActive, "Consuming the target packet and preparing a bounded edit.")
		return m.runImplementAgent(ctx, prompt, history, opts, resumeContext)
	default:
		reply, err := m.runMainAgent(ctx, prompt, history, opts, rails)
		if err != nil {
			return "", err
		}
		return m.applyControlRailsFinalAnswer(rails, reply, nil, nil, false), nil
	}
}

func promptRequestsWholeFileRefactor(prompt string) bool {
	normalized := strings.ToLower(strings.TrimSpace(prompt))
	if normalized == "" {
		return false
	}
	return containsAny(normalized, "refactor", "split", "reorganize", "move code") &&
		containsAny(normalized, "whole file", "entire file", "in one shot", "split existing code", "monolithic", "cohesive new files", "multiple files", "multi-file")
}

func (m *Model) locatePromptTarget(ctx context.Context, mgr *workspace.Manager, prompt string, literal string) (locateOutcome, error) {
	outcome := locateOutcome{Literal: literal}
	if mgr == nil {
		return outcome, fmt.Errorf("workspace access is unavailable")
	}

	step := 1
	candidates := make([]locateCandidate, 0, 8)
	scopes := locateSearchScopes(prompt)
	literalPattern := locateLiteralSearchPattern(literal)
	for _, scope := range scopes {
		req := workspace.ToolRequest{
			Tool:    "search_content",
			Pattern: literalPattern,
			Path:    scope,
			Glob:    "*.go",
		}
		result := m.executeDeterministicLaneTool(ctx, "locate", mgr, step, req, false, true, fmt.Sprintf("search_content:%s:%s:*.go", scope, literalPattern))
		step++
		if result.Error != "" || strings.TrimSpace(result.Output) == "" {
			continue
		}
		candidates = parseLocateCandidates(result.Output)
		if len(candidates) > 0 {
			break
		}
	}
	if len(candidates) == 0 {
		seen := make(map[string]struct{})
		for _, pattern := range locateFallbackPatterns(prompt, literal) {
			for _, scope := range scopes {
				req := workspace.ToolRequest{
					Tool:    "search_content",
					Pattern: pattern,
					Path:    scope,
					Glob:    "*.go",
				}
				result := m.executeDeterministicLaneTool(ctx, "locate", mgr, step, req, false, true, fmt.Sprintf("search_content:%s:%s:*.go", scope, pattern))
				step++
				if result.Error != "" || strings.TrimSpace(result.Output) == "" {
					continue
				}
				for _, candidate := range parseLocateCandidates(result.Output) {
					key := fmt.Sprintf("%s:%d", candidate.Path, candidate.Line)
					if _, ok := seen[key]; ok {
						continue
					}
					seen[key] = struct{}{}
					candidates = append(candidates, candidate)
				}
			}
		}
	}
	if len(candidates) == 0 {
		return outcome, fmt.Errorf("could not locate %q in workspace source files", literal)
	}

	for i := range candidates {
		candidates[i].Region = classifyLocateRegion(candidates[i])
		candidates[i].Score = scoreLocateCandidate(prompt, candidates[i])
	}
	sortLocateCandidates(candidates)

	readLimit := 1
	if len(candidates) < readLimit {
		readLimit = len(candidates)
	}
	for i := 0; i < readLimit; i++ {
		candidate := candidates[i]
		req := workspace.ToolRequest{
			Tool:      "read_file",
			Path:      candidate.Path,
			StartLine: maxInt(1, candidate.Line-3),
			EndLine:   candidate.Line + 3,
		}
		result := m.executeDeterministicLaneTool(ctx, "locate", mgr, step, req, false, true, fmt.Sprintf("read_file:%s", candidate.Path))
		step++
		if result.Error != "" {
			continue
		}
		candidates[i].Snippet = strings.TrimSpace(result.Output)
		candidates[i].Region = classifyLocateRegion(candidates[i])
		candidates[i].Score = scoreLocateCandidate(prompt, candidates[i])
	}
	sortLocateCandidates(candidates)

	outcome.Best = candidates[0]
	outcome.Alternates = selectLocateAlternates(candidates)
	return outcome, nil
}

func (m *Model) executeDeterministicLaneTool(
	ctx context.Context,
	agentName string,
	mgr *workspace.Manager,
	step int,
	req workspace.ToolRequest,
	broad bool,
	narrowing bool,
	action string,
) workspace.ToolResult {
	raw := marshalToolRequest(req)
	m.logToolRequest(agent.ToolRequestEvent{
		Step:      step,
		Agent:     agentName,
		Raw:       raw,
		Request:   req,
		Mode:      mgr.Mode(),
		Broad:     broad,
		Narrowing: narrowing,
		Action:    action,
	})
	result := mgr.Execute(ctx, req)
	m.logToolResult(agent.ToolResultEvent{Step: step, Agent: agentName, Result: result})
	if shouldInvalidateExploreCacheForTool(result) {
		m.events <- exploreCacheInvalidatedMsg{
			reason: fmt.Sprintf("workspace changed via %s; clearing cached explore profile", result.Tool),
		}
	}
	m.events <- toolTraceMsg{text: workspace.FormatToolTrace(result)}
	return result
}

func quotedPromptFragments(prompt string) []string {
	fragments := make([]string, 0, 4)
	for i := 0; i < len(prompt); i++ {
		quote := prompt[i]
		if quote != '\'' && quote != '"' && quote != '`' {
			continue
		}
		start := i + 1
		end := start
		for end < len(prompt) && prompt[end] != quote {
			end++
		}
		if end >= len(prompt) {
			break
		}
		fragment := strings.TrimSpace(prompt[start:end])
		if fragment != "" {
			fragments = append(fragments, fragment)
		}
		i = end
	}
	return fragments
}

func looksLikeUISpacingPrompt(prompt string) bool {
	normalized := strings.ToLower(prompt)
	return containsAny(normalized, "padding", "space", "spacing", "margin") &&
		containsAny(normalized, "under", "underneath", "below", "beneath") &&
		containsAny(normalized, "banner", "header")
}

func firstPromptEditLine(mgr *workspace.Manager, path string, target string) int {
	if mgr == nil || strings.TrimSpace(path) == "" || strings.TrimSpace(target) == "" {
		return 0
	}
	fullPath := filepath.Join(mgr.Root(), path)
	content, err := os.ReadFile(fullPath)
	if err != nil {
		return 0
	}
	for i, line := range strings.Split(string(content), "\n") {
		if strings.Contains(line, target) {
			return i + 1
		}
	}
	return 0
}

func replaceSystemPrompt(history []llama.ChatMessage, systemPrompt string) []llama.ChatMessage {
	cloned := append([]llama.ChatMessage(nil), history...)
	for i := range cloned {
		if normalizeHistoryRole(cloned[i].Role) == "system" {
			cloned[i].Content = systemPrompt
			return cloned
		}
	}
	return append([]llama.ChatMessage{{Role: "system", Content: systemPrompt}}, cloned...)
}

func buildSubagentSystemPrompt(basePrompt string, workspaceInstructions string) string {
	sections := make([]string, 0, 2)
	if prompt := strings.TrimSpace(basePrompt); prompt != "" {
		sections = append(sections, prompt)
	}
	if prompt := strings.TrimSpace(workspaceInstructions); prompt != "" {
		sections = append(sections, prompt)
	}
	return strings.Join(sections, "\n\n")
}

func buildExploreTaskPrompt(prompt string) string {
	return "Investigate this request in read-only mode and return only a concise exploration summary.\n\n" +
		renderLaneContractPolicy(analysisLaneExplore) + "\n\n" +
		"Required output:\n" +
		"- likely files\n" +
		"- suspected issue or relevant finding\n" +
		"- confidence\n" +
		"- recommended next step\n\n" +
		"Investigation rules:\n" +
		"- Do not ask for permission to edit.\n" +
		"- Do not propose broad directory listing loops.\n" +
		"- The harness may already provide an overview and bootstrap evidence. Use that first instead of repeating the same discovery step.\n" +
		"- If a top-level listing shows source directories or concrete files, inspect at least two concrete files before concluding the repo is empty or claiming there is nothing actionable.\n" +
		"- For open-ended bug hunts, prefer targeted content search after the first orientation step, then read concrete source files.\n" +
		"- Ignore repo metadata or generated-output files such as .git state, caches, logs, and model artifacts unless the request is explicitly about them.\n" +
		"- Never send both a file handle id and a path unless they refer to the same discovered file.\n" +
		"- In Go projects, prioritize README.md, go.mod, cmd/, internal/, and scripts/ before less relevant files.\n" +
		"- If you conclude there is no actionable issue, cite the concrete files you inspected.\n" +
		"Use the available tools to inspect, then summarize evidence clearly.\n\n" +
		"Request:\n" + strings.TrimSpace(prompt)
}

func buildReviewSystemPrompt(basePrompt string) string {
	sections := make([]string, 0, 2)
	if prompt := strings.TrimSpace(basePrompt); prompt != "" {
		sections = append(sections, prompt)
	}
	sections = append(sections,
		"You are in REVIEW mode.",
		"This is a read-only summary lane. No further tool calls are available.",
		"Base your answer only on the supplied evidence and pick the single best issue or cleanup candidate.",
		"Mention at least one exact candidate file path from the supplied evidence.",
		"Prefer one concrete, defensible finding over a vague sweep of multiple weak ideas.",
	)
	return strings.Join(sections, "\n\n")
}

func buildArchitectureSystemPrompt(basePrompt string) string {
	sections := make([]string, 0, 2)
	if prompt := strings.TrimSpace(basePrompt); prompt != "" {
		sections = append(sections, prompt)
	}
	sections = append(sections,
		"You are in ARCHITECTURE mode.",
		"This is a read-only structure-analysis lane. No further tool calls are available.",
		"Base your answer only on the supplied evidence and identify how the system is organized, where the main structural pressure points are, and what refactor direction is most defensible.",
		"Mention at least one exact hotspot file path from the supplied evidence.",
		"Prefer a small number of high-confidence structural opportunities over an ungrounded sweep.",
	)
	return strings.Join(sections, "\n\n")
}

func buildReviewTaskPrompt(prompt string) string {
	return "Review this request in read-only mode and return one concrete finding.\n\n" +
		renderLaneContractPolicy(analysisLaneReview) + "\n\n" +
		"Required output:\n" +
		"- finding title\n" +
		"- file or files involved\n" +
		"- why it is a real issue or worthwhile cleanup\n" +
		"- the specific evidence you relied on\n" +
		"- confidence\n" +
		"- recommended next step\n\n" +
		"Review rules:\n" +
		"- Do not emit tool requests.\n" +
		"- Do not hedge with a broad list unless the evidence is genuinely inconclusive.\n" +
		"- Cite at least one exact candidate file path from the provided evidence.\n" +
		"- Prefer the strongest single issue supported by the evidence over multiple weak candidates.\n" +
		"- If the evidence is inconclusive, say that directly and identify the best next file to inspect.\n\n" +
		"Request:\n" + strings.TrimSpace(prompt)
}

func buildArchitectureTaskPrompt(prompt string) string {
	return "Analyze this request in read-only mode and return a grounded architecture summary.\n\n" +
		renderLaneContractPolicy(analysisLaneArchitecture) + "\n\n" +
		"Required output:\n" +
		"- current structural shape or extension path\n" +
		"- top 2-3 structural opportunities or pressure points\n" +
		"- the exact hotspot files involved\n" +
		"- why each hotspot matters\n" +
		"- suggested refactor or boundary direction\n" +
		"- confidence\n" +
		"- recommended next step\n\n" +
		"Architecture rules:\n" +
		"- Do not emit tool requests.\n" +
		"- Ground the answer in the repository profile and hotspot evidence provided by the harness.\n" +
		"- Cite at least one exact hotspot file path from the provided evidence.\n" +
		"- Focus on structure, boundaries, ownership, extension points, and coupling instead of bug triage.\n" +
		"- If the evidence is inconclusive, say that directly and identify the next structural hotspot to inspect.\n\n" +
		"Request:\n" + strings.TrimSpace(prompt)
}

type routeAmbiguitySnapshot struct {
	SelectedLane string   `json:"selected_lane,omitempty"`
	Reason       string   `json:"reason,omitempty"`
	Intent       string   `json:"intent,omitempty"`
	Risk         string   `json:"risk,omitempty"`
	Confidence   string   `json:"confidence,omitempty"`
	Signals      []string `json:"signals,omitempty"`
	Vetoes       []string `json:"vetoes,omitempty"`
	Ambiguous    bool     `json:"ambiguous"`
	TieBreak     string   `json:"tie_break,omitempty"`
}

func buildRouteAmbiguitySnapshot(prompt string, plan promptExecutionPlan) routeAmbiguitySnapshot {
	profile := buildPromptRouteProfile(prompt)
	signals := append([]string(nil), plan.intent.Signals...)
	if len(signals) == 0 {
		signals = promptIntentSignals(profile, profile.normalized)
	}
	signals = routeAmbiguitySignals(signals)
	ambiguous := countRouteIntentSignals(signals) > 1
	tieBreak := ""
	if ambiguous {
		tieBreak = "ordered host route precedence selected " + string(plan.lane)
	}
	return routeAmbiguitySnapshot{
		SelectedLane: string(plan.lane),
		Reason:       string(plan.reason),
		Intent:       plan.intent.Intent,
		Risk:         plan.intent.Risk,
		Confidence:   plan.intent.Confidence,
		Signals:      signals,
		Vetoes:       append([]string(nil), plan.intent.Vetoes...),
		Ambiguous:    ambiguous,
		TieBreak:     tieBreak,
	}
}

func routeAmbiguitySignals(signals []string) []string {
	out := make([]string, 0, len(signals))
	for _, signal := range signals {
		if signal == "implementation" {
			signal = "implement"
		}
		out = append(out, signal)
	}
	return uniqueNonEmptyStrings(out)
}

func (m *Model) logRouteAmbiguity(ambiguity routeAmbiguitySnapshot) {
	if m == nil || m.debugLogger == nil {
		return
	}
	data, err := json.Marshal(ambiguity)
	if err != nil {
		return
	}
	m.logMessage("route_ambiguity", string(data))
}

func (m *Model) verifyAndReconcilePromptPlan(
	ctx context.Context,
	prompt string,
	history []llama.ChatMessage,
	opts llama.ChatOptions,
	plan promptExecutionPlan,
	rails deterministicControlRailsSnapshot,
) (promptExecutionPlan, bool) {
	if !m.routeClassifierEnabled() {
		return plan, false
	}
	reasons := promptIntentVerifierReasons(plan, rails)
	if m.cfg.RouteClassifier.AlwaysOn || len(reasons) > 0 {
		reasons = append([]string{"route_classifier_enabled"}, reasons...)
	}
	if len(reasons) == 0 {
		return plan, false
	}
	snapshot := promptIntentReconciliationSnapshot{
		Triggered:        true,
		Reasons:          reasons,
		HostLane:         string(plan.lane),
		HostFollowupLane: string(plan.followupLane),
		FinalLane:        string(plan.lane),
		FinalFollowup:    string(plan.followupLane),
	}
	verifier, err := m.runPromptIntentVerifier(ctx, prompt, history, opts, plan, rails, reasons)
	if err != nil {
		snapshot.Notes = append(snapshot.Notes, "verifier_failed: "+err.Error())
		m.noteRouteClassifierFailure(err)
		m.logPromptIntentReconciliation(snapshot)
		return plan, false
	}
	m.noteRouteClassifierHealthy()
	m.logPromptIntentVerifier(verifier)
	reconciled, changed, safetyOverride, notes := reconcilePromptPlanWithVerifier(prompt, plan, verifier, m.currentTaskMemory())
	snapshot.VerifierLane = verifier.RecommendedLane
	snapshot.VerifierFollowup = verifier.RecommendedFollowup
	snapshot.FinalLane = string(reconciled.lane)
	snapshot.FinalFollowup = string(reconciled.followupLane)
	snapshot.Changed = changed
	snapshot.SafetyOverride = safetyOverride
	snapshot.Notes = append(snapshot.Notes, notes...)
	m.logPromptIntentReconciliation(snapshot)
	if !changed {
		return plan, true
	}
	return reconciled, true
}

func (m *Model) routeClassifierEnabled() bool {
	return m != nil && m.cfg.RouteClassifier.Enabled
}

func (m *Model) noteRouteClassifierFailure(err error) {
	if m == nil || err == nil || strings.TrimSpace(m.cfg.RouteClassifierBaseURL()) == "" {
		return
	}
	if m.routeClassifierOfflineNotified {
		return
	}
	m.routeClassifierOfflineNotified = true
	message := "Route classifier offline; using host routing only: " + err.Error()
	m.logMessage("route_classifier_health", message)
	if m.events != nil {
		m.events <- infoMsg{text: message}
	}
}

func (m *Model) noteRouteClassifierHealthy() {
	if m == nil {
		return
	}
	if m.routeClassifierOfflineNotified {
		m.logMessage("route_classifier_health", "Route classifier recovered.")
	}
	m.routeClassifierOfflineNotified = false
}

func promptIntentVerifierReasons(plan promptExecutionPlan, rails deterministicControlRailsSnapshot) []string {
	reasons := make([]string, 0, 6)
	if plan.intent.Confidence != "" && plan.intent.Confidence != "high" {
		reasons = append(reasons, "host_intent_confidence_"+plan.intent.Confidence)
	}
	if plan.intent.Risk == "high" {
		reasons = append(reasons, "high_risk_intent")
	}
	if len(plan.intent.UserBoundaries) > 0 {
		reasons = append(reasons, "user_boundary_present")
	}
	if len(plan.intent.Vetoes) > 0 {
		reasons = append(reasons, "host_veto_present")
	}
	if countRouteIntentSignals(plan.intent.Signals) > 1 {
		reasons = append(reasons, "multiple_route_signals")
	}
	if rails.TaskFamily == "debug_logs" && plan.lane == analysisLaneImplement {
		reasons = append(reasons, "lane_task_family_conflict")
	}
	if rails.TaskFamily == "implementation" && !plan.intent.WantsEdits && plan.lane == analysisLaneImplement {
		reasons = append(reasons, "implementation_without_edit_intent")
	}
	return uniqueNonEmptyStrings(reasons)
}

func countRouteIntentSignals(signals []string) int {
	count := 0
	for _, signal := range uniqueNonEmptyStrings(signals) {
		switch signal {
		case "locate", "explore", "review", "architecture", "implementation", "implement", "verification_only", "debug_logs":
			count++
		}
	}
	return count
}

func (m *Model) runPromptIntentVerifier(
	ctx context.Context,
	prompt string,
	history []llama.ChatMessage,
	opts llama.ChatOptions,
	plan promptExecutionPlan,
	rails deterministicControlRailsSnapshot,
	reasons []string,
) (promptIntentVerifierResult, error) {
	if m == nil {
		return promptIntentVerifierResult{}, fmt.Errorf("model client unavailable")
	}
	if err := m.ensureRouteClassifierReady(ctx); err != nil {
		return promptIntentVerifierResult{}, err
	}
	classifierCtx := ctx
	cancel := func() {}
	if m.cfg.RouteClassifier.TimeoutSeconds > 0 {
		classifierCtx, cancel = context.WithTimeout(ctx, time.Duration(m.cfg.RouteClassifier.TimeoutSeconds)*time.Second)
	}
	defer cancel()
	classifierClient := m.routeClassifierClient(time.Duration(maxInt(m.cfg.RouteClassifier.TimeoutSeconds, 1)) * time.Second)
	if classifierClient == nil {
		return promptIntentVerifierResult{}, fmt.Errorf("model client unavailable")
	}
	verifierOpts := opts
	if model := strings.TrimSpace(m.cfg.RouteClassifier.Model); model != "" {
		verifierOpts.Model = model
	} else if strings.TrimSpace(m.cfg.RouteClassifierBaseURL()) != "" {
		if models, err := classifierClient.ListModels(classifierCtx); err == nil && len(models) > 0 {
			verifierOpts.Model = models[0].ID
		} else {
			verifierOpts.Model = "local-model"
		}
	}
	verifierOpts.Temperature = 0
	verifierOpts.TopP = 1
	if m.cfg.RouteClassifier.MaxTokens > 0 {
		verifierOpts.MaxTokens = m.cfg.RouteClassifier.MaxTokens
	} else {
		verifierOpts.MaxTokens = clampPositiveInt(verifierOpts.MaxTokens, 512, 1024)
	}
	verifierOpts.PreserveThinking = false
	thinking := false
	verifierOpts.EnableThinking = &thinking
	messages := []llama.ChatMessage{
		{Role: "system", Content: buildPromptIntentVerifierSystemPrompt()},
		{Role: "user", Content: buildPromptIntentVerifierTaskPrompt(prompt, history, plan, rails, reasons, renderTaskMemoryForModel(m.currentTaskMemory()))},
	}
	reply, err := classifierClient.CompleteChat(classifierCtx, messages, verifierOpts)
	if err != nil {
		return promptIntentVerifierResult{}, err
	}
	result, err := parsePromptIntentVerifierResult(reply)
	if err != nil {
		return promptIntentVerifierResult{}, err
	}
	return normalizePromptIntentVerifierResult(result), nil
}

func buildPromptIntentVerifierSystemPrompt() string {
	return strings.Join([]string{
		"You are a narrow route classifier for a coding-agent harness.",
		"Your job is semantic classification only. Do not solve the task. Do not call tools. Do not grant risky permission.",
		"Return exactly one JSON object and no markdown, prose, code fences, XML, or thinking text.",
		"Required JSON shape:",
		`{"primary_intent":"","secondary_intents":[],"wants_edits":false,"wants_plan_only":false,"needs_verification":false,"approval_detected":false,"risk":"low","confidence":"high","reason":"","recommended_lane":"main","recommended_followup_lane":"","recommended_contract":"direct_answer","task_family":"","permission_profile":"","required_boundaries":[],"target_refs":[]}`,
		"Allowed primary_intent values: direct_answer, locate_code, explore_codebase, review_code, plan_architecture, implement, verify, debug_logs.",
		"Allowed recommended_lane values: main, locate, explore, review, architecture, implement.",
		"Allowed recommended_followup_lane values: implement or empty string.",
		"Allowed recommended_contract values: direct_answer, locate_result, exploration_result, review_findings, architecture_assessment, implementation_result.",
		"Allowed risk values: low, medium, high. Allowed confidence values: low, medium, high.",
		"Routing rules:",
		"- Plan-only requests must set wants_plan_only=true, wants_edits=false, and use a read-only lane such as architecture, explore, review, locate, or main.",
		"- Explicit edit/fix/update/implement requests must set wants_edits=true and recommended_lane=implement unless the user explicitly says not to edit.",
		"- Failure wording inside a fix request is context, not a reason to downgrade away from implementation.",
		"- Verification-only requests must set needs_verification=true and should not set wants_edits unless the user also asks to fix failures.",
		"- Latest-log/debug inspection requests without an edit request should use primary_intent=debug_logs and a read-only lane.",
		"- Durable-plan approvals such as do it, begin, proceed, go ahead, or commence must set approval_detected=true and recommended_lane=implement when active durable-plan memory is present.",
		"- User boundaries such as don't push, don't deploy, don't commit, wait for review, or ask before writing must be copied into required_boundaries.",
		"- Put mentioned file paths, symbols, packages, commands, or logs in target_refs when present.",
		"Examples:",
		`Prompt: "check the latest log and explain why it failed" -> {"primary_intent":"debug_logs","secondary_intents":[],"wants_edits":false,"wants_plan_only":false,"needs_verification":false,"approval_detected":false,"risk":"low","confidence":"high","reason":"The user asks for failure inspection, not edits.","recommended_lane":"main","recommended_followup_lane":"","recommended_contract":"direct_answer","task_family":"debug_logs","permission_profile":"read_only_evidence","required_boundaries":[],"target_refs":["latest log"]}`,
		`Prompt: "check the latest log and fix it" -> {"primary_intent":"implement","secondary_intents":["debug_logs"],"wants_edits":true,"wants_plan_only":false,"needs_verification":true,"approval_detected":false,"risk":"medium","confidence":"high","reason":"The user asks to inspect failure context and apply a fix.","recommended_lane":"implement","recommended_followup_lane":"","recommended_contract":"implementation_result","task_family":"implementation","permission_profile":"guarded_workspace_writes","required_boundaries":[],"target_refs":["latest log"]}`,
		`Prompt: "prepare a refactor plan for internal/app/app.go" -> {"primary_intent":"plan_architecture","secondary_intents":[],"wants_edits":false,"wants_plan_only":true,"needs_verification":false,"approval_detected":false,"risk":"low","confidence":"high","reason":"The user asks for a plan artifact only.","recommended_lane":"architecture","recommended_followup_lane":"","recommended_contract":"architecture_assessment","task_family":"planning","permission_profile":"read_only_evidence","required_boundaries":[],"target_refs":["internal/app/app.go"]}`,
	}, "\n")
}

func buildPromptIntentVerifierTaskPrompt(prompt string, history []llama.ChatMessage, plan promptExecutionPlan, rails deterministicControlRailsSnapshot, reasons []string, taskMemory string) string {
	route := map[string]any{
		"lane":          plan.lane,
		"followup_lane": plan.followupLane,
		"reason":        plan.reason,
		"contract":      promptPlanAnswerContract(plan),
		"intent":        plan.intent,
		"rails":         rails,
		"reasons":       reasons,
	}
	data, _ := json.Marshal(route)
	return strings.Join([]string{
		"Verify the prompt-routing decision below.",
		"Return exactly one JSON object with these keys: primary_intent, secondary_intents, wants_edits, wants_plan_only, needs_verification, approval_detected, risk, confidence, reason, recommended_lane, recommended_followup_lane, recommended_contract, task_family, permission_profile, required_boundaries, target_refs.",
		"If the host route is safe and semantically correct, return the same lane/contract with high confidence. If the user request is ambiguous, classify conservatively and preserve user-stated boundaries.",
		"Recent conversation excerpt:",
		truncateText(renderIntentVerifierHistory(history), 1600),
		"Active task memory:",
		truncateText(taskMemory, 1200),
		"User prompt:",
		strings.TrimSpace(prompt),
		"Host route snapshot:",
		string(data),
	}, "\n\n")
}

func renderIntentVerifierHistory(history []llama.ChatMessage) string {
	if len(history) == 0 {
		return ""
	}
	start := len(history) - 6
	if start < 0 {
		start = 0
	}
	parts := make([]string, 0, len(history)-start)
	for _, msg := range history[start:] {
		role := normalizeHistoryRole(msg.Role)
		if role != "user" && role != "assistant" && role != "system" {
			continue
		}
		content := strings.TrimSpace(msg.Content)
		if content == "" {
			continue
		}
		parts = append(parts, role+": "+truncateText(content, 320))
	}
	return strings.Join(parts, "\n")
}

func parsePromptIntentVerifierResult(reply string) (promptIntentVerifierResult, error) {
	payload := strings.TrimSpace(reply)
	if object, ok := extractFirstJSONObjectForPlan(payload); ok {
		payload = object
	}
	var raw struct {
		PrimaryIntent       string          `json:"primary_intent,omitempty"`
		SecondaryIntents    json.RawMessage `json:"secondary_intents,omitempty"`
		WantsEdits          bool            `json:"wants_edits"`
		WantsPlanOnly       bool            `json:"wants_plan_only"`
		NeedsVerification   bool            `json:"needs_verification"`
		ApprovalDetected    bool            `json:"approval_detected"`
		Risk                string          `json:"risk,omitempty"`
		Confidence          string          `json:"confidence,omitempty"`
		Reason              string          `json:"reason,omitempty"`
		RecommendedLane     string          `json:"recommended_lane,omitempty"`
		RecommendedFollowup string          `json:"recommended_followup_lane,omitempty"`
		RecommendedContract string          `json:"recommended_contract,omitempty"`
		TaskFamily          string          `json:"task_family,omitempty"`
		PermissionProfile   string          `json:"permission_profile,omitempty"`
		RequiredBoundaries  json.RawMessage `json:"required_boundaries,omitempty"`
		TargetRefs          json.RawMessage `json:"target_refs,omitempty"`
	}
	if err := json.Unmarshal([]byte(payload), &raw); err != nil {
		return promptIntentVerifierResult{}, err
	}
	result := promptIntentVerifierResult{
		PrimaryIntent:       raw.PrimaryIntent,
		SecondaryIntents:    decodeFlexibleStringSlice(raw.SecondaryIntents),
		WantsEdits:          raw.WantsEdits,
		WantsPlanOnly:       raw.WantsPlanOnly,
		NeedsVerification:   raw.NeedsVerification,
		ApprovalDetected:    raw.ApprovalDetected,
		Risk:                raw.Risk,
		Confidence:          raw.Confidence,
		Reason:              raw.Reason,
		RecommendedLane:     raw.RecommendedLane,
		RecommendedFollowup: raw.RecommendedFollowup,
		RecommendedContract: raw.RecommendedContract,
		TaskFamily:          raw.TaskFamily,
		PermissionProfile:   raw.PermissionProfile,
		RequiredBoundaries:  decodeFlexibleStringSlice(raw.RequiredBoundaries),
		TargetRefs:          decodeFlexibleStringSlice(raw.TargetRefs),
	}
	return result, nil
}

func decodeFlexibleStringSlice(raw json.RawMessage) []string {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var values []string
	if err := json.Unmarshal(raw, &values); err == nil {
		return values
	}
	var value string
	if err := json.Unmarshal(raw, &value); err == nil && strings.TrimSpace(value) != "" {
		return []string{value}
	}
	return nil
}

func normalizePromptIntentVerifierResult(result promptIntentVerifierResult) promptIntentVerifierResult {
	result.PrimaryIntent = normalizeIntentToken(result.PrimaryIntent)
	result.RecommendedLane = normalizeIntentLane(result.RecommendedLane)
	result.RecommendedFollowup = normalizeIntentLane(result.RecommendedFollowup)
	if result.RecommendedFollowup != string(analysisLaneImplement) {
		result.RecommendedFollowup = ""
	}
	result.RecommendedContract = normalizeRecommendedContract(result.RecommendedContract)
	result.Risk = normalizeIntentRisk(result.Risk)
	result.Confidence = normalizeIntentConfidence(result.Confidence)
	for i, intent := range result.SecondaryIntents {
		result.SecondaryIntents[i] = normalizeIntentToken(intent)
	}
	result.SecondaryIntents = uniqueNonEmptyStrings(result.SecondaryIntents)
	result.RequiredBoundaries = normalizeIntentBoundaries(result.RequiredBoundaries)
	result.TargetRefs = uniqueNormalizedPaths(result.TargetRefs)
	result.TaskFamily = normalizeIntentToken(result.TaskFamily)
	result.PermissionProfile = normalizeIntentToken(result.PermissionProfile)
	result.Reason = strings.TrimSpace(result.Reason)
	return result
}

func normalizeRecommendedContract(contract string) string {
	contract = strings.TrimSpace(contract)
	switch answerContract(contract) {
	case answerContractDirectConversation, answerContractLocateTargets, answerContractGroundedExplanation, answerContractReviewFindings, answerContractArchitecture, answerContractImplementation:
		return contract
	default:
		return ""
	}
}

func reconcilePromptPlanWithVerifier(prompt string, plan promptExecutionPlan, verifier promptIntentVerifierResult, memory ...*longHorizonTaskMemory) (promptExecutionPlan, bool, bool, []string) {
	original := plan
	notes := make([]string, 0, 4)
	safetyOverride := false
	hostProfile := buildPromptRouteProfile(prompt)
	var activeMemory *longHorizonTaskMemory
	if len(memory) > 0 {
		activeMemory = memory[0]
	}
	durablePlanApproval := verifier.ApprovalDetected && strings.TrimSpace(memoryKind(activeMemory)) == "durable_plan"
	if durablePlanApproval {
		verifier.WantsEdits = true
		verifier.WantsPlanOnly = false
		if verifier.RecommendedLane == "" || verifier.RecommendedLane == string(analysisLaneMain) {
			verifier.RecommendedLane = string(analysisLaneImplement)
		}
		plan.intent.WantsPlanOnly = false
		plan.intent.Vetoes = removePromptIntentVetoes(plan.intent.Vetoes, "plan_only_blocks_edits", "read_only_boundary_blocks_edits")
		notes = append(notes, "accepted classifier durable-plan approval signal")
	}
	hostBlocksEdits := plan.intent.WantsPlanOnly || hostProfile.hasReadOnlyBoundary || hostProfile.wantsVerificationOnly
	if durablePlanApproval && !hostProfile.wantsVerificationOnly {
		hostBlocksEdits = false
	}
	if verifier.WantsPlanOnly {
		hostBlocksEdits = true
		plan.intent.Vetoes = append(plan.intent.Vetoes, "verifier_plan_only_blocks_edits")
	}
	supportedVerifierBoundaries := supportedPromptVerifierBoundaries(prompt, plan.intent.UserBoundaries, verifier.RequiredBoundaries)
	if len(supportedVerifierBoundaries) > 0 {
		plan.intent.UserBoundaries = uniqueNonEmptyStrings(append(plan.intent.UserBoundaries, supportedVerifierBoundaries...))
		for _, boundary := range supportedVerifierBoundaries {
			switch boundary {
			case "no_push":
				plan.intent.Vetoes = append(plan.intent.Vetoes, "verifier_no_push_boundary_blocks_git_push")
			case "no_deploy":
				plan.intent.Vetoes = append(plan.intent.Vetoes, "verifier_no_deploy_boundary_blocks_deploy")
			case "wait_for_review":
				plan.intent.Vetoes = append(plan.intent.Vetoes, "verifier_wait_for_review_blocks_external_release")
			}
		}
	}
	if len(verifier.RequiredBoundaries) > len(supportedVerifierBoundaries) {
		notes = append(notes, "ignored verifier boundary not supported by prompt or host context")
	}
	if verifier.Risk != "" {
		plan.intent.Risk = verifier.Risk
	}
	if verifier.Confidence != "" {
		plan.intent.Confidence = verifier.Confidence
	}
	if verifier.PrimaryIntent != "" {
		plan.intent.Intent = verifier.PrimaryIntent
	}
	if verifier.NeedsVerification {
		plan.intent.WantsVerify = true
	}
	if len(verifier.TargetRefs) > 0 {
		plan.intent.TargetRefs = uniqueNormalizedPaths(append(plan.intent.TargetRefs, verifier.TargetRefs...))
	}
	plan.intent.Source = "host_structured_intent+model_verifier"
	plan.intent.Vetoes = uniqueNonEmptyStrings(plan.intent.Vetoes)
	recommendedLane, inferredLane := verifierRecommendedLane(verifier)
	if recommendedLane != "" {
		recommended := recommendedLane
		if hostBlocksEdits && recommended == analysisLaneImplement {
			safetyOverride = true
			notes = append(notes, "ignored verifier implementation recommendation because host/verifier veto blocks edits")
		} else if shouldPreserveHostArchitectureLane(original, verifier, recommended, hostProfile) {
			if plan.intent.Intent == "explore_codebase" || plan.intent.Intent == string(analysisLaneExplore) {
				plan.intent.Intent = "plan_architecture"
			}
			notes = append(notes, "preserved host architecture lane because verifier still identified architecture task family")
		} else if shouldAcceptVerifierLane(original, verifier, recommended, hostProfile) {
			plan.lane = recommended
			plan.reason = routeReasonForLane(recommended)
			note := "accepted verifier lane " + string(recommended)
			if inferredLane {
				note = "accepted verifier intent lane " + string(recommended)
			}
			notes = append(notes, note)
		}
	}
	if hostBlocksEdits {
		if plan.lane == analysisLaneImplement {
			plan.lane = safestReadOnlyLaneForIntent(hostProfile, verifier, plan)
			plan.reason = routeReasonForLane(plan.lane)
			safetyOverride = true
			notes = append(notes, "downgraded implementation to read-only due to edit veto")
		} else if plan.lane == analysisLaneMain {
			if lane := safestReadOnlyLaneForIntent(hostProfile, verifier, plan); lane != analysisLaneMain {
				plan.lane = lane
				plan.reason = routeReasonForLane(plan.lane)
				notes = append(notes, "routed plan-only/read-only intent to "+string(lane))
			}
		}
		plan.followupLane = ""
	} else if verifier.RecommendedFollowup == string(analysisLaneImplement) && plan.lane != analysisLaneImplement {
		plan.followupLane = analysisLaneImplement
		notes = append(notes, "accepted verifier implementation follow-up")
	} else if verifier.WantsEdits && plan.lane != analysisLaneImplement && verifier.RecommendedLane == string(analysisLaneImplement) {
		plan.lane = analysisLaneImplement
		plan.followupLane = ""
		plan.reason = promptRouteReasonDirectImplementation
		notes = append(notes, "accepted verifier direct implementation")
	}
	plan.contract = answerContractForLane(plan.lane)
	plan.intent.SelectedLane = string(plan.lane)
	plan.intent.FollowupLane = string(plan.followupLane)
	plan.intent.WantsEdits = (original.intent.WantsEdits || verifier.WantsEdits) && !hostBlocksEdits
	plan.intent.WantsPlanOnly = plan.intent.WantsPlanOnly || verifier.WantsPlanOnly
	if durablePlanApproval {
		plan.intent.WantsPlanOnly = false
	}
	plan = finalizePromptExecutionPlan(prompt, hostProfile, plan)
	changed := plan.lane != original.lane ||
		plan.followupLane != original.followupLane ||
		plan.taskFamily != original.taskFamily ||
		plan.permissionProfile != original.permissionProfile ||
		plan.intent.WantsEdits != original.intent.WantsEdits ||
		plan.intent.WantsPlanOnly != original.intent.WantsPlanOnly ||
		plan.intent.WantsVerify != original.intent.WantsVerify ||
		plan.intent.Intent != original.intent.Intent ||
		plan.intent.Risk != original.intent.Risk ||
		plan.intent.Confidence != original.intent.Confidence ||
		strings.Join(plan.intent.TargetRefs, "\x00") != strings.Join(original.intent.TargetRefs, "\x00") ||
		strings.Join(plan.intent.UserBoundaries, "\x00") != strings.Join(original.intent.UserBoundaries, "\x00") ||
		strings.Join(plan.intent.Vetoes, "\x00") != strings.Join(original.intent.Vetoes, "\x00")
	return plan, changed, safetyOverride, uniqueNonEmptyStrings(notes)
}

func shouldPreserveHostArchitectureLane(plan promptExecutionPlan, verifier promptIntentVerifierResult, recommended analysisLane, profile promptRouteProfile) bool {
	if plan.lane != analysisLaneArchitecture || !profile.wantsArchitecture {
		return false
	}
	if recommended == analysisLaneArchitecture || recommended == analysisLaneImplement {
		return false
	}
	return verifierIndicatesArchitecture(verifier)
}

func verifierIndicatesArchitecture(verifier promptIntentVerifierResult) bool {
	if answerContract(verifier.RecommendedContract) == answerContractArchitecture {
		return true
	}
	switch normalizeIntentToken(verifier.TaskFamily) {
	case "architecture", "architectural", "planning", "plan_architecture":
		return true
	}
	for _, intent := range append([]string{verifier.PrimaryIntent}, verifier.SecondaryIntents...) {
		switch normalizeIntentToken(intent) {
		case "plan_architecture", "architecture", "architectural":
			return true
		}
	}
	return false
}

func removePromptIntentVetoes(vetoes []string, remove ...string) []string {
	if len(vetoes) == 0 || len(remove) == 0 {
		return uniqueNonEmptyStrings(vetoes)
	}
	removeSet := make(map[string]struct{}, len(remove))
	for _, veto := range remove {
		removeSet[strings.TrimSpace(veto)] = struct{}{}
	}
	out := make([]string, 0, len(vetoes))
	for _, veto := range vetoes {
		veto = strings.TrimSpace(veto)
		if veto == "" {
			continue
		}
		if _, ok := removeSet[veto]; ok {
			continue
		}
		out = append(out, veto)
	}
	return uniqueNonEmptyStrings(out)
}

func verifierRecommendedLane(verifier promptIntentVerifierResult) (analysisLane, bool) {
	if verifier.RecommendedLane != "" && verifier.RecommendedLane != string(analysisLaneMain) {
		return analysisLane(verifier.RecommendedLane), false
	}
	if lane := readOnlyLaneForVerifierIntent(verifier); lane != "" {
		return lane, true
	}
	if verifier.RecommendedLane == string(analysisLaneMain) {
		return analysisLaneMain, false
	}
	return "", false
}

func supportedPromptVerifierBoundaries(prompt string, existing []string, proposed []string) []string {
	supported := make(map[string]struct{})
	for _, boundary := range normalizeIntentBoundaries(existing) {
		supported[boundary] = struct{}{}
	}
	for _, boundary := range promptUserBoundaries(prompt, "") {
		supported[boundary] = struct{}{}
	}
	out := make([]string, 0, len(proposed))
	for _, boundary := range normalizeIntentBoundaries(proposed) {
		if _, ok := supported[boundary]; ok {
			out = append(out, boundary)
		}
	}
	return uniqueNonEmptyStrings(out)
}

func shouldAcceptVerifierLane(plan promptExecutionPlan, verifier promptIntentVerifierResult, recommended analysisLane, profile promptRouteProfile) bool {
	if !validAnalysisLane(recommended) {
		return false
	}
	if recommended == plan.lane {
		return false
	}
	if verifier.Confidence == "low" {
		return false
	}
	if plan.lane == analysisLaneImplement && profile.wantsImplementation && !verifier.WantsPlanOnly && verifier.WantsEdits {
		return false
	}
	if recommended == analysisLaneImplement {
		return verifier.WantsEdits
	}
	if plan.lane == analysisLaneMain && recommended != analysisLaneMain && verifierIntentSupportsLane(verifier, recommended) {
		return true
	}
	if plan.intent.Confidence != "high" || countRouteIntentSignals(plan.intent.Signals) > 1 {
		return true
	}
	return false
}

func safestReadOnlyLaneForIntent(profile promptRouteProfile, verifier promptIntentVerifierResult, plan promptExecutionPlan) analysisLane {
	if profile.wantsArchitecture || verifierIndicatesArchitecture(verifier) {
		return analysisLaneArchitecture
	}
	if verifier.RecommendedLane != "" && verifier.RecommendedLane != string(analysisLaneImplement) && verifier.RecommendedLane != string(analysisLaneMain) {
		lane := analysisLane(verifier.RecommendedLane)
		if validAnalysisLane(lane) {
			return lane
		}
	}
	if lane := readOnlyLaneForVerifierIntent(verifier); lane != "" {
		return lane
	}
	switch {
	case profile.wantsReview:
		return analysisLaneReview
	case profile.wantsDebugLogs || profile.wantsExploration:
		return analysisLaneExplore
	case profile.wantsLocate:
		return analysisLaneLocate
	case plan.lane != analysisLaneImplement && validAnalysisLane(plan.lane):
		return plan.lane
	default:
		return analysisLaneMain
	}
}

func readOnlyLaneForVerifierIntent(verifier promptIntentVerifierResult) analysisLane {
	for _, intent := range append([]string{verifier.PrimaryIntent}, verifier.SecondaryIntents...) {
		switch normalizeIntentToken(intent) {
		case "plan_architecture", "architecture", "architectural":
			return analysisLaneArchitecture
		case "debug_logs", "debug", "investigate", "investigation", "explore":
			return analysisLaneExplore
		case "review", "security_review", "performance_review":
			return analysisLaneReview
		case "locate":
			return analysisLaneLocate
		}
	}
	return ""
}

func verifierIntentSupportsLane(verifier promptIntentVerifierResult, lane analysisLane) bool {
	return readOnlyLaneForVerifierIntent(verifier) == lane
}

func validAnalysisLane(lane analysisLane) bool {
	switch lane {
	case analysisLaneMain, analysisLaneLocate, analysisLaneExplore, analysisLaneReview, analysisLaneArchitecture, analysisLaneImplement:
		return true
	default:
		return false
	}
}

func normalizeIntentLane(value string) string {
	value = normalizeIntentToken(value)
	switch value {
	case "main", "direct", "conversation", "direct_answer":
		return string(analysisLaneMain)
	case "locate":
		return string(analysisLaneLocate)
	case "explore", "investigate", "debug_logs", "debug":
		return string(analysisLaneExplore)
	case "review", "security_review":
		return string(analysisLaneReview)
	case "architecture", "architectural", "plan_architecture":
		return string(analysisLaneArchitecture)
	case "implement", "implementation", "edit", "fix":
		return string(analysisLaneImplement)
	default:
		return ""
	}
}

func normalizeIntentToken(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "-", "_")
	value = strings.ReplaceAll(value, " ", "_")
	return value
}

func normalizeIntentRisk(value string) string {
	switch normalizeIntentToken(value) {
	case "high":
		return "high"
	case "medium", "med":
		return "medium"
	default:
		return "low"
	}
}

func normalizeIntentConfidence(value string) string {
	switch normalizeIntentToken(value) {
	case "high":
		return "high"
	case "low":
		return "low"
	default:
		return "medium"
	}
}

func normalizeIntentBoundaries(boundaries []string) []string {
	normalized := make([]string, 0, len(boundaries))
	for _, boundary := range boundaries {
		switch normalizeIntentToken(boundary) {
		case "no_push", "dont_push", "do_not_push":
			normalized = append(normalized, "no_push")
		case "no_deploy", "dont_deploy", "do_not_deploy":
			normalized = append(normalized, "no_deploy")
		case "wait_for_review", "review_required", "requires_review":
			normalized = append(normalized, "wait_for_review")
		}
	}
	return uniqueNonEmptyStrings(normalized)
}

func clampPositiveInt(value int, fallback int, maxValue int) int {
	if value <= 0 {
		value = fallback
	}
	if maxValue > 0 && value > maxValue {
		return maxValue
	}
	return value
}

func (m *Model) logPromptIntentVerifier(result promptIntentVerifierResult) {
	if m == nil || m.debugLogger == nil {
		return
	}
	data, err := json.Marshal(result)
	if err != nil {
		return
	}
	m.logMessage("intent_verifier", string(data))
}

func (m *Model) logPromptIntentReconciliation(snapshot promptIntentReconciliationSnapshot) {
	if m == nil || m.debugLogger == nil {
		return
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		return
	}
	m.logMessage("intent_reconciliation", string(data))
}

func buildPromptIntentDecision(prompt string, profile promptRouteProfile, recent string, plan promptExecutionPlan) promptIntentDecision {
	normalized := strings.ToLower(strings.TrimSpace(prompt))
	signals := promptIntentSignals(profile, normalized)
	boundaries := promptUserBoundaries(normalized, recent)
	vetoes := make([]string, 0, 4)
	if profile.wantsPlanOnly {
		vetoes = append(vetoes, "plan_only_blocks_edits")
	}
	if profile.hasReadOnlyBoundary {
		vetoes = append(vetoes, "read_only_boundary_blocks_edits")
	}
	if profile.wantsVerificationOnly {
		vetoes = append(vetoes, "verification_only_blocks_edits")
	}
	if len(boundaries) > 0 {
		for _, boundary := range boundaries {
			switch boundary {
			case "no_push":
				vetoes = append(vetoes, "no_push_boundary_blocks_git_push")
			case "no_deploy":
				vetoes = append(vetoes, "no_deploy_boundary_blocks_deploy")
			case "wait_for_review":
				vetoes = append(vetoes, "wait_for_review_blocks_external_release")
			}
		}
	}
	intent := promptIntentForPlan(profile, plan, normalized)
	risk := promptIntentRisk(normalized, profile, boundaries)
	confidence := promptIntentConfidence(profile, plan, signals, vetoes)
	fallback := ""
	if confidence == "low" || len(vetoes) > 0 {
		fallback = "prefer the safest read-only lane or ask for explicit authorization before mutating state"
	}
	return promptIntentDecision{
		Intent:         intent,
		SelectedLane:   string(plan.lane),
		FollowupLane:   string(plan.followupLane),
		WantsEdits:     profileWantsEditIntent(profile) && !profile.wantsPlanOnly && !profile.hasReadOnlyBoundary && !profile.wantsVerificationOnly,
		WantsPlanOnly:  profile.wantsPlanOnly,
		WantsVerify:    profile.wantsVerificationOnly,
		Risk:           risk,
		Confidence:     confidence,
		Signals:        uniqueNonEmptyStrings(signals),
		Vetoes:         uniqueNonEmptyStrings(vetoes),
		UserBoundaries: uniqueNonEmptyStrings(boundaries),
		TargetRefs:     uniqueNormalizedPaths(promptMentionedWorkspacePaths(prompt)),
		Fallback:       fallback,
		Source:         "host_structured_intent",
	}
}

func applyPromptIntentVetoes(plan promptExecutionPlan, profile promptRouteProfile) promptExecutionPlan {
	if profile.wantsVerificationOnly {
		plan.lane = analysisLaneMain
		plan.followupLane = ""
		plan.reason = promptRouteReasonDirectConversation
		return plan
	}
	if profile.wantsPlanOnly || profile.hasReadOnlyBoundary {
		plan.followupLane = ""
		if plan.lane == analysisLaneImplement {
			switch {
			case profile.wantsArchitecture || containsAny(profile.normalized, "architecture", "architectural", "structure", "boundary", "refactor", "split"):
				plan.lane = analysisLaneArchitecture
				plan.reason = promptRouteReasonArchitectureRequest
			case profile.wantsReview:
				plan.lane = analysisLaneReview
				plan.reason = promptRouteReasonReviewRequest
			case profile.wantsExploration || profile.hasConcreteTarget:
				plan.lane = analysisLaneExplore
				plan.reason = promptRouteReasonExploreRequest
			default:
				plan.lane = analysisLaneMain
				plan.reason = promptRouteReasonDirectConversation
			}
		}
		return plan
	}
	if profile.wantsDebugLogs && !profile.wantsImplementation {
		plan.lane = analysisLaneExplore
		plan.followupLane = ""
		plan.reason = promptRouteReasonExploreRequest
	}
	return plan
}

func promptIntentSignals(profile promptRouteProfile, normalized string) []string {
	signals := make([]string, 0, 10)
	if profile.wantsLocate {
		signals = append(signals, "locate")
	}
	if profile.wantsExploration {
		signals = append(signals, "explore")
	}
	if profile.wantsReview {
		signals = append(signals, "review")
	}
	if profile.wantsArchitecture {
		signals = append(signals, "architecture")
	}
	if profileWantsEditIntent(profile) {
		signals = append(signals, "implementation")
	}
	if profile.wantsPlanOnly {
		signals = append(signals, "plan_only")
	}
	if profile.wantsVerificationOnly {
		signals = append(signals, "verification_only")
	}
	if profile.wantsDebugLogs {
		signals = append(signals, "debug_logs")
	}
	if profile.hasConcreteTarget {
		signals = append(signals, "concrete_target")
	}
	if profile.hasReadOnlyBoundary {
		signals = append(signals, "read_only_boundary")
	}
	if containsAny(normalized, "push", "deploy", "delete", "remove", "rm -rf", "credential", "secret", ".env") {
		signals = append(signals, "risk_signal")
	}
	return signals
}

func promptIntentForPlan(profile promptRouteProfile, plan promptExecutionPlan, normalized string) string {
	switch {
	case profile.wantsVerificationOnly:
		return "verification_only"
	case profile.wantsDebugLogs && !profile.wantsImplementation:
		return "debug_logs"
	case profile.wantsPlanOnly:
		if containsAny(normalized, "architecture", "architectural", "structure", "boundary", "refactor", "split") {
			return "plan_architecture"
		}
		return "plan_only"
	case plan.followupLane == analysisLaneImplement:
		return string(plan.lane) + "_then_implement"
	case plan.lane == analysisLaneImplement:
		return "implement"
	case plan.lane != "":
		return string(plan.lane)
	default:
		return "direct_answer"
	}
}

func promptIntentRisk(normalized string, profile promptRouteProfile, boundaries []string) string {
	switch {
	case containsAny(normalized, "rm -rf", "delete all", "wipe", "drop database", "production", "deploy", "credential", "secret", ".env", "git push", "push to"):
		return "high"
	case len(boundaries) > 0:
		return "medium"
	case profileWantsEditIntent(profile):
		return "medium"
	case profile.wantsVerificationOnly:
		return "low"
	default:
		return "low"
	}
}

func promptIntentConfidence(profile promptRouteProfile, plan promptExecutionPlan, signals []string, vetoes []string) string {
	strongSignals := 0
	for _, signal := range signals {
		switch signal {
		case "locate", "explore", "review", "architecture", "implementation", "verification_only", "debug_logs":
			strongSignals++
		}
	}
	if len(vetoes) > 0 && plan.followupLane == analysisLaneImplement {
		return "low"
	}
	if profile.wantsPlanOnly && profile.wantsImplementation {
		return "medium"
	}
	if strongSignals > 2 {
		return "medium"
	}
	if plan.lane == analysisLaneImplement && !profile.hasConcreteTarget && profile.wantsExploration {
		return "low"
	}
	return "high"
}

func promptUserBoundaries(normalized string, recent string) []string {
	combined := strings.ToLower(strings.TrimSpace(normalized + "\n" + recent))
	boundaries := make([]string, 0, 4)
	if containsAny(combined, "don't push", "do not push", "no push", "wait to push", "before pushing") {
		boundaries = append(boundaries, "no_push")
	}
	if containsAny(combined, "don't deploy", "do not deploy", "no deploy", "wait to deploy", "before deploying") {
		boundaries = append(boundaries, "no_deploy")
	}
	if containsAny(combined, "wait until i review", "wait for my review", "before i review", "after i approve") {
		boundaries = append(boundaries, "wait_for_review")
	}
	return boundaries
}

func (m *Model) reloadPromptStack() error {
	return m.reloadPromptStackWithProfiles(m.activeProfiles)
}

func (m *Model) reloadPromptStackWithProfiles(profiles []string) error {
	stack, err := promptstack.Build(m.cfg.Chat, profiles)
	if err != nil {
		return err
	}
	m.promptStack = stack
	m.logPromptContext()
	return nil
}

type promptRouteProfile struct {
	normalized            string
	wantsLocate           bool
	wantsDirectImplement  bool
	wantsImplementation   bool
	wantsExploration      bool
	wantsReview           bool
	wantsArchitecture     bool
	wantsPlanOnly         bool
	wantsVerificationOnly bool
	wantsDebugLogs        bool
	hasReadOnlyBoundary   bool
	hasConcreteTarget     bool
	thoroughness          workspace.ExploreThoroughness
}

func classifyPromptExecutionPlan(prompt string, mgr *workspace.Manager) promptExecutionPlan {
	if mgr == nil || !mgr.Enabled() || mgr.Mode() != workspace.ModeNormal {
		return promptExecutionPlan{}
	}
	profile := buildPromptRouteProfile(prompt)
	lane, reason := promptInitialRoute(profile)
	followupLane := promptFollowupRoute(profile, lane)
	plan := promptExecutionPlan{
		lane:         lane,
		followupLane: followupLane,
		reason:       reason,
		contract:     answerContractForLane(lane),
	}
	plan.intent = buildPromptIntentDecision(prompt, profile, "", plan)
	plan = applyPromptIntentVetoes(plan, profile)
	plan.intent = buildPromptIntentDecision(prompt, profile, "", plan)
	thoroughness := profile.thoroughness
	if analysisLaneUsesThoroughness(plan.lane) && thoroughness == "" {
		thoroughness = mgr.ExploreThoroughness()
	}
	plan.thoroughness = thoroughness
	return finalizePromptExecutionPlan(prompt, profile, plan)
}

func (m *Model) classifyPromptExecutionPlan(prompt string) promptExecutionPlan {
	if m == nil {
		return promptExecutionPlan{}
	}
	plan := classifyPromptExecutionPlan(prompt, m.workspace)
	if plan.lane == analysisLaneMain && plan.followupLane == "" {
		if contextual, ok := m.contextualPromptExecutionPlan(prompt, plan); ok {
			plan = contextual
		}
	}
	plan = m.attachPromptIntentDecision(prompt, plan)
	return m.applyAgentDecisionMode(prompt, plan)
}

func (m *Model) attachPromptIntentDecision(prompt string, plan promptExecutionPlan) promptExecutionPlan {
	profile := buildPromptRouteProfile(prompt)
	recent := ""
	if m != nil {
		recent = m.recentConversationText(6)
	}
	plan.intent = buildPromptIntentDecision(prompt, profile, recent, plan)
	return finalizePromptExecutionPlan(prompt, profile, plan)
}

func finalizePromptExecutionPlan(prompt string, profile promptRouteProfile, plan promptExecutionPlan) promptExecutionPlan {
	plan.contract = answerContractForLane(plan.lane)
	plan.taskFamily = canonicalPromptTaskFamily(profile, plan)
	plan.permissionProfile = controlRailPermissionProfile(plan.taskFamily, plan)
	if plan.intent.SelectedLane != string(plan.lane) || plan.intent.FollowupLane != string(plan.followupLane) {
		plan.intent.SelectedLane = string(plan.lane)
		plan.intent.FollowupLane = string(plan.followupLane)
	}
	if plan.agentMode == "" {
		plan.agentMode = agentDecisionModeAuto
	}
	plan.intent.AgentMode = string(plan.agentMode)
	return plan
}

func (m *Model) contextualPromptExecutionPlan(prompt string, base promptExecutionPlan) (promptExecutionPlan, bool) {
	normalized := strings.ToLower(strings.TrimSpace(prompt))
	if normalized == "" || m == nil || m.workspace == nil || !m.workspace.Enabled() || m.workspace.Mode() != workspace.ModeNormal {
		return promptExecutionPlan{}, false
	}
	recent := m.recentConversationText(6)
	if (looksLikeArchitectureValidationFollowup(normalized) || looksLikeArchitecturePlanningFollowup(normalized)) && recentContextSupportsArchitectureValidation(recent) {
		thoroughness := m.workspace.ExploreThoroughness()
		if thoroughness == "" {
			thoroughness = workspace.ThoroughnessMedium
		}
		return promptExecutionPlan{
			lane:         analysisLaneArchitecture,
			thoroughness: thoroughness,
			reason:       promptRouteReasonArchitectureRequest,
			contract:     answerContractForLane(analysisLaneArchitecture),
		}, true
	}
	if looksLikeEllipticalImplementationFollowup(normalized) && m.recentContextSupportsImplementation(recent) {
		return promptExecutionPlan{
			lane:     analysisLaneImplement,
			reason:   promptRouteReasonDirectImplementation,
			contract: answerContractForLane(analysisLaneImplement),
		}, true
	}
	return base, false
}

func (m *Model) recentConversationText(limit int) string {
	if m == nil || limit <= 0 {
		return ""
	}
	parts := make([]string, 0, limit)
	for i := len(m.messages) - 1; i >= 0 && len(parts) < limit; i-- {
		content := strings.TrimSpace(m.messages[i].Content)
		if content == "" {
			continue
		}
		role := normalizeHistoryRole(m.messages[i].Role)
		if role != "user" && role != "assistant" {
			continue
		}
		parts = append(parts, strings.ToLower(content))
	}
	return strings.Join(parts, "\n\n")
}

func (m *Model) recentContextSupportsImplementation(recent string) bool {
	if m != nil && m.lastReplan != nil && m.lastReplan.ToLane == analysisLaneImplement {
		return true
	}
	if strings.TrimSpace(recent) == "" {
		return false
	}
	hasConcreteTarget := len(promptMentionedWorkspacePaths(recent)) > 0
	hasImplementationCue := containsAny(
		recent,
		"recommended implementation order",
		"implementation order",
		"recommended next step",
		"suggested refactor",
		"apply changes",
		"apply the fix",
		"make the change",
		"implement it",
		"then implement",
		"split one responsibility",
		"extract state management",
		"create prompt generation",
		"extract output formatting",
		"consolidate error",
	)
	hasStructuralCue := containsAny(recent, "architecture", "architectural", "refactor", "split", "boundary", "hotspot", "optimization opportunity")
	return hasConcreteTarget && hasImplementationCue && hasStructuralCue
}

func looksLikeArchitectureValidationFollowup(normalized string) bool {
	return containsAny(normalized, "validate", "confirm", "assess", "check") &&
		containsAny(normalized, "option", "recommendation", "analysis", "architecture", "architectural", "refactor", "split", "boundary")
}

func looksLikeArchitecturePlanningFollowup(normalized string) bool {
	return looksLikePlanOnlyPrompt(normalized) &&
		containsAny(normalized, "update", "refactor", "optimize", "architecture", "architectural", "split", "boundary", "option", "this one", "this file")
}

func recentContextSupportsArchitectureValidation(recent string) bool {
	if strings.TrimSpace(recent) == "" {
		return false
	}
	return len(promptMentionedWorkspacePaths(recent)) > 0 &&
		containsAny(recent, "architecture", "architectural", "hotspot", "structural", "boundary", "refactor", "optimization opportunity")
}

func looksLikeEllipticalImplementationFollowup(normalized string) bool {
	switch strings.Trim(normalized, ".! ") {
	case "do it",
		"please do",
		"please do it",
		"go ahead",
		"go ahead and do it",
		"do this",
		"do that",
		"make it happen",
		"proceed",
		"begin",
		"start",
		"implement it",
		"apply it",
		"add it",
		"add it all",
		"add all of it",
		"implement all of it",
		"implement that",
		"implement those changes",
		"make the change",
		"make those changes":
		return true
	default:
		return false
	}
}

func (m *Model) normalizePromptPlanThoroughness(plan promptExecutionPlan) promptExecutionPlan {
	if !analysisLaneUsesThoroughness(plan.lane) || plan.thoroughness != "" {
		return plan
	}
	if m != nil && m.workspace != nil {
		plan.thoroughness = m.workspace.ExploreThoroughness()
	}
	if plan.thoroughness == "" {
		plan.thoroughness = workspace.ThoroughnessMedium
	}
	return plan
}

func analysisLaneUsesThoroughness(lane analysisLane) bool {
	switch lane {
	case analysisLaneExplore, analysisLaneReview, analysisLaneArchitecture:
		return true
	default:
		return false
	}
}

func buildPromptRouteProfile(prompt string) promptRouteProfile {
	normalized := strings.ToLower(prompt)
	thoroughness, wantsExploration := autoExploreThoroughness(prompt)
	wantsDebugLogs := looksLikeLogOrFailureInspectionPrompt(normalized)
	wantsVerificationOnly := looksLikeVerificationOnlyPrompt(normalized)
	wantsPlanOnly := looksLikePlanOnlyPrompt(normalized)
	hasReadOnlyBoundary := looksLikeReadOnlyBoundaryPrompt(normalized)
	if wantsDebugLogs {
		wantsExploration = true
		if thoroughness == "" {
			thoroughness = workspace.ThoroughnessMedium
		}
	}
	wantsArchitecture := looksLikeArchitecturePrompt(normalized) || looksLikeArchitecturePlanRequest(normalized)
	return promptRouteProfile{
		normalized:            normalized,
		wantsLocate:           looksLikeLocatePrompt(prompt, normalized) || shouldRouteUILiteralEditThroughLocate(prompt, normalized),
		wantsDirectImplement:  looksLikeDirectImplementationPrompt(prompt, normalized),
		wantsImplementation:   promptRequestsImplementation(prompt),
		wantsExploration:      wantsExploration,
		wantsReview:           looksLikeReviewPrompt(normalized),
		wantsArchitecture:     wantsArchitecture,
		wantsPlanOnly:         wantsPlanOnly,
		wantsVerificationOnly: wantsVerificationOnly,
		wantsDebugLogs:        wantsDebugLogs,
		hasReadOnlyBoundary:   hasReadOnlyBoundary,
		hasConcreteTarget:     len(promptMentionedWorkspacePaths(prompt)) > 0,
		thoroughness:          thoroughness,
	}
}

func promptInitialRoute(profile promptRouteProfile) (analysisLane, promptRouteReason) {
	switch {
	case profile.wantsLocate:
		return analysisLaneLocate, promptRouteReasonLocateRequest
	case profile.wantsDirectImplement && !promptRequestsAnalysisBeforeImplementation(profile.normalized) && (profile.hasConcreteTarget || !profile.wantsExploration):
		return analysisLaneImplement, promptRouteReasonDirectImplementation
	case profile.wantsArchitecture:
		return analysisLaneArchitecture, promptRouteReasonArchitectureRequest
	case profile.wantsDebugLogs:
		return analysisLaneExplore, promptRouteReasonExploreRequest
	case profile.wantsExploration && profile.wantsReview:
		return analysisLaneReview, promptRouteReasonReviewRequest
	case profile.wantsExploration:
		return analysisLaneExplore, promptRouteReasonExploreRequest
	default:
		return analysisLaneMain, promptRouteReasonDirectConversation
	}
}

func promptFollowupRoute(profile promptRouteProfile, lane analysisLane) analysisLane {
	if !profileWantsEditIntent(profile) {
		return ""
	}
	switch lane {
	case analysisLaneLocate, analysisLaneExplore, analysisLaneReview, analysisLaneArchitecture:
		return analysisLaneImplement
	default:
		return ""
	}
}

func profileWantsEditIntent(profile promptRouteProfile) bool {
	return profile.wantsImplementation || profile.wantsDirectImplement
}

func looksLikeArchitecturePlanRequest(normalized string) bool {
	return looksLikePlanOnlyPrompt(normalized) &&
		containsAny(
			normalized,
			"architecture",
			"architectural",
			"structure",
			"structural",
			"boundary",
			"boundaries",
			"refactor",
			"refactoring",
			"split",
			"decompose",
			"modular",
			"responsibility",
			"responsibilities",
		)
}

func autoExploreThoroughness(prompt string) (workspace.ExploreThoroughness, bool) {
	normalized := strings.ToLower(prompt)
	if !looksLikeInvestigationPrompt(normalized) {
		return "", false
	}
	switch {
	case looksLikeNoEarlyStopPrompt(normalized):
		return workspace.ThoroughnessThorough, true
	case containsAny(normalized, "thorough", "deep", "exhaustive"):
		return workspace.ThoroughnessThorough, true
	case containsAny(normalized, "quick", "brief", "fast"):
		return workspace.ThoroughnessQuick, true
	default:
		return workspace.ThoroughnessMedium, true
	}
}

func looksLikeNoEarlyStopPrompt(normalized string) bool {
	return containsAny(
		normalized,
		"longer search",
		"give up early",
		"not super confident",
		"not confident",
		"do not stop",
		"don't stop",
		"continue until",
		"keep searching",
		"proper frontier",
		"frontier-grade",
		"whole codebase",
		"entire codebase",
		"literally the whole codebase",
		"comprehensive",
	)
}

func looksLikeInvestigationPrompt(normalized string) bool {
	if containsAny(normalized, "just explore", "what's wrong", "what is wrong") {
		return true
	}

	hasInvestigationVerb := containsAny(
		normalized,
		"explore",
		"investigate",
		"investigation",
		"inspect",
		"audit",
		"review",
		"look for",
		"look through",
		"search",
		"scan",
		"identify",
		"find",
		"analyze",
		"diagnose",
	)
	hasTarget := containsAny(
		normalized,
		"bug",
		"issue",
		"problem",
		"improvement",
		"improvements",
		"optimization",
		"optimize",
		"cleanup",
		"clean up",
		"regression",
		"performance",
		"code smell",
		"routing",
		"flow",
		"architecture",
		"architectural",
		"structure",
	)
	hasScope := containsAny(
		normalized,
		"codebase",
		"repo",
		"repository",
		"project",
		"harness",
		"code",
	)
	hasArchitectureQuestion := containsAny(
		normalized,
		"can you identify",
		"how does",
		"how do",
		"how are",
		"how is",
		"why does",
		"why is",
	)
	hasArchitectureTarget := containsAny(
		normalized,
		"support",
		"supported",
		"extensible",
		"extension",
		"architecture",
		"pipeline",
		"workflow",
		"language",
		"languages",
	)
	hasOpenEndedReviewQuestion := containsAny(
		normalized,
		"what can we",
		"what should we",
		"anything to",
		"where should we",
	)

	return (hasInvestigationVerb && hasTarget) ||
		(hasInvestigationVerb && hasScope) ||
		(hasOpenEndedReviewQuestion && hasTarget && hasScope) ||
		(hasArchitectureQuestion && hasScope && hasArchitectureTarget)
}

func looksLikeArchitecturePrompt(normalized string) bool {
	hasScope := containsAny(
		normalized,
		"codebase",
		"repo",
		"repository",
		"project",
		"harness",
		"system",
		"code",
	)
	hasStructureTarget := containsAny(
		normalized,
		"structure",
		"architecture",
		"architectural",
		"organize",
		"organization",
		"modular",
		"modularity",
		"boundary",
		"boundaries",
		"split",
		"decompose",
		"extensible",
		"extension",
		"support",
		"supported",
		"capability",
		"capabilities",
		"language",
		"languages",
		"pipeline",
		"workflow",
		"maintainability",
		"long-term maintainability",
		"long term maintainability",
		"responsibility",
		"responsibilities",
	)
	hasArchitectureQuestion := containsAny(
		normalized,
		"how does",
		"how do",
		"how are",
		"how is",
		"how should",
		"why does",
		"why is",
		"what can we",
		"what should we",
		"where should we",
		"which language",
		"which languages",
	)
	hasStructuralImprovementRequest := containsAny(
		normalized,
		"architectural improvement",
		"architectural improvements",
		"architecture improvement",
		"architecture improvements",
		"optimize our architecture",
		"optimize the architecture",
		"optimize architecture",
		"optimize our architectural",
		"improve our architecture",
		"improve the architecture",
		"improve architecture",
		"structural improvement",
		"structural improvements",
		"maintainability",
		"long-term maintainability",
		"long term maintainability",
		"refactor opportunities",
		"refactoring opportunities",
		"long-term refactor",
		"long term refactor",
		"package boundary",
		"package boundaries",
	)
	hasExplicitArchitectureRequest := containsAny(
		normalized,
		"analyze the architecture",
		"analyse the architecture",
		"review the architecture",
		"assess the architecture",
		"map the architecture",
		"architecture and then",
		"architecture plan",
		"architecture approach",
	)
	hasRefactorVerb := containsAny(
		normalized,
		"optimize",
		"optimization",
		"improve",
		"refactor",
		"cleanup",
		"clean up",
		"reorganize",
	)
	if hasStructuralImprovementRequest || hasExplicitArchitectureRequest {
		return true
	}
	return (hasScope && hasStructureTarget && hasArchitectureQuestion) ||
		(hasScope && hasStructureTarget && hasRefactorVerb) ||
		(hasScope && hasStructureTarget && containsAny(normalized, "identify any", "analyze", "review"))
}

func looksLikeLocatePrompt(prompt string, normalized string) bool {
	hasLocateVerb := containsAny(
		normalized,
		"where is",
		"locate",
		"identify",
		"show me",
		"which code",
		"what code",
		"find the code",
		"find where",
	)
	hasVerificationGuard := containsAny(
		normalized,
		"identify it first",
		"show it first",
		"so i can verify",
		"before changing",
		"before you change it",
		"don't change it yet",
		"do not change it yet",
	)
	hasLiteral := primaryLocateLiteral(prompt) != ""
	hasUILocation := containsAny(
		normalized,
		"top",
		"header",
		"footer",
		"badge",
		"window",
		"chat",
		"menu",
		"status",
		"banner",
	)
	if hasVerificationGuard && (hasLiteral || hasUILocation) {
		return true
	}
	return hasLocateVerb && (hasLiteral || hasUILocation)
}

func shouldRouteUILiteralEditThroughLocate(prompt string, normalized string) bool {
	return promptRequestsImplementation(prompt) && primaryLocateLiteral(prompt) != "" && hasUILocationHint(normalized)
}

func promptRequestsRemoval(prompt string) bool {
	normalized := strings.ToLower(prompt)
	return containsAny(
		normalized,
		"remove it",
		"remove this",
		"remove the",
		"delete it",
		"delete this",
		"delete the",
		"take it out",
	)
}

func looksLikeReviewPrompt(normalized string) bool {
	return containsAny(
		normalized,
		"review",
		"audit",
		"bug",
		"issue",
		"problem",
		"code smell",
		"cleanup",
		"clean up",
		"refactor",
		"optimization",
		"optimize",
		"regression",
		"error",
		"identify any",
		"find one",
		"should be fixed",
	)
}

func looksLikeIssueHuntPrompt(prompt string) bool {
	normalized := strings.ToLower(prompt)
	return containsAny(
		normalized,
		"bug",
		"issue",
		"problem",
		"optimization",
		"optimize",
		"cleanup",
		"clean up",
		"regression",
		"performance",
		"code smell",
		"error",
	)
}

func (m *Model) systemPrompt() string {
	sections := make([]string, 0, 3)
	if prompt := strings.TrimSpace(m.promptStack.Text); prompt != "" {
		sections = append(sections, prompt)
	}
	if m.workspace != nil {
		if instructions := strings.TrimSpace(m.workspace.Instructions()); instructions != "" {
			sections = append(sections, instructions)
		}
	}
	return strings.Join(sections, "\n\n")
}

func (m *Model) logPromptContext() {
	if m.debugLogger == nil {
		return
	}
	workspaceMode, thoroughness := m.currentModeDetails()
	_ = m.debugLogger.LogContext(m.contextSourceNames(), m.systemPrompt(), workspaceMode, thoroughness)
}

func looksLikeCodeSmellPrompt(prompt string) bool {
	normalized := strings.ToLower(strings.TrimSpace(prompt))
	return containsAny(normalized, "code smell", "cleanup", "clean up", "refactor")
}
