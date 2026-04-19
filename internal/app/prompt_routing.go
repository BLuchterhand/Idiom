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
	lane         analysisLane
	followupLane analysisLane
	thoroughness workspace.ExploreThoroughness
	reason       promptRouteReason
	contract     answerContract
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
	plan := classifyPromptExecutionPlan(prompt, m.workspace)
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
	if looksLikeResumeTaskMemoryPrompt(prompt) && taskMemoryResumable(m.currentTaskMemory()) {
		m.resumeTaskMemoryAsync(prompt)
		return
	}
	plan := classifyPromptExecutionPlan(prompt, m.workspace)
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
			m.events <- assistantReplyMsg{text: reply}
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
) (string, error) {
	plan = m.normalizePromptPlanThoroughness(plan)
	effort := buildEffortPolicySnapshot(plan, m.cfg.Chat.MaxToolSteps, m.cfg.Chat.RequestTimeoutSeconds, m.cfg.Chat.MaxTokens, m.cfg.LlamaServer.ReasoningBudget)
	m.logEffortPolicy(effort)
	m.logRouteAmbiguity(buildRouteAmbiguitySnapshot(prompt, plan))
	rails := buildDeterministicControlRails(prompt, plan)
	m.logDeterministicControlRails(rails)
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
		outcome, err := m.runArchitectureSubagent(ctx, prompt, opts, plan.thoroughness)
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
		return m.runImplementAgent(ctx, prompt, history, opts)
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
	Signals      []string `json:"signals,omitempty"`
	Ambiguous    bool     `json:"ambiguous"`
	TieBreak     string   `json:"tie_break,omitempty"`
}

func buildRouteAmbiguitySnapshot(prompt string, plan promptExecutionPlan) routeAmbiguitySnapshot {
	normalized := strings.ToLower(strings.TrimSpace(prompt))
	signals := make([]string, 0, 5)
	if containsAny(normalized, "find ", "where ", "locate", "identify where") {
		signals = append(signals, "locate")
	}
	if containsAny(normalized, "explore", "investigate", "understand", "walkthrough", "how does") {
		signals = append(signals, "explore")
	}
	if looksLikeLogOrFailureInspectionPrompt(normalized) {
		signals = append(signals, "debug_logs")
	}
	if containsAny(normalized, "review", "bug", "regression", "vulnerab", "security", "performance") {
		signals = append(signals, "review")
	}
	if containsAny(normalized, "architecture", "structure", "boundary", "package", "refactor", "modular") {
		signals = append(signals, "architecture")
	}
	if promptRequestsImplementation(normalized) || containsAny(normalized, "implement", "apply", "edit", "change", "update", "fix") {
		signals = append(signals, "implement")
	}
	signals = uniqueNonEmptyStrings(signals)
	ambiguous := len(signals) > 1
	tieBreak := ""
	if ambiguous {
		tieBreak = "ordered host route precedence selected " + string(plan.lane)
	}
	return routeAmbiguitySnapshot{
		SelectedLane: string(plan.lane),
		Reason:       string(plan.reason),
		Signals:      signals,
		Ambiguous:    ambiguous,
		TieBreak:     tieBreak,
	}
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
	normalized           string
	wantsLocate          bool
	wantsDirectImplement bool
	wantsImplementation  bool
	wantsExploration     bool
	wantsReview          bool
	wantsArchitecture    bool
	hasConcreteTarget    bool
	thoroughness         workspace.ExploreThoroughness
}

func classifyPromptExecutionPlan(prompt string, mgr *workspace.Manager) promptExecutionPlan {
	if mgr == nil || !mgr.Enabled() || mgr.Mode() != workspace.ModeNormal {
		return promptExecutionPlan{}
	}
	profile := buildPromptRouteProfile(prompt)
	lane, reason := promptInitialRoute(profile)
	thoroughness := profile.thoroughness
	if analysisLaneUsesThoroughness(lane) && thoroughness == "" {
		thoroughness = mgr.ExploreThoroughness()
	}
	return promptExecutionPlan{
		lane:         lane,
		followupLane: promptFollowupRoute(profile, lane),
		thoroughness: thoroughness,
		reason:       reason,
		contract:     answerContractForLane(lane),
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
	if looksLikeLogOrFailureInspectionPrompt(normalized) {
		wantsExploration = true
		if thoroughness == "" {
			thoroughness = workspace.ThoroughnessMedium
		}
	}
	return promptRouteProfile{
		normalized:           normalized,
		wantsLocate:          looksLikeLocatePrompt(prompt, normalized) || shouldRouteUILiteralEditThroughLocate(prompt, normalized),
		wantsDirectImplement: looksLikeDirectImplementationPrompt(prompt, normalized),
		wantsImplementation:  promptRequestsImplementation(prompt),
		wantsExploration:     wantsExploration,
		wantsReview:          looksLikeReviewPrompt(normalized),
		wantsArchitecture:    looksLikeArchitecturePrompt(normalized),
		hasConcreteTarget:    len(promptMentionedWorkspacePaths(prompt)) > 0,
		thoroughness:         thoroughness,
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
	case profile.wantsExploration && profile.wantsReview:
		return analysisLaneReview, promptRouteReasonReviewRequest
	case profile.wantsExploration:
		return analysisLaneExplore, promptRouteReasonExploreRequest
	default:
		return analysisLaneMain, promptRouteReasonDirectConversation
	}
}

func promptFollowupRoute(profile promptRouteProfile, lane analysisLane) analysisLane {
	if !profile.wantsImplementation {
		return ""
	}
	switch lane {
	case analysisLaneLocate, analysisLaneExplore, analysisLaneReview, analysisLaneArchitecture:
		return analysisLaneImplement
	default:
		return ""
	}
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
		"structural improvement",
		"structural improvements",
		"package boundary",
		"package boundaries",
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
	if hasStructuralImprovementRequest {
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
