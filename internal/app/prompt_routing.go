package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"qwen-bubbles/internal/agent"
	"qwen-bubbles/internal/frontier"
	"qwen-bubbles/internal/llama"
	"qwen-bubbles/internal/promptstack"
	"qwen-bubbles/internal/workspace"
	"strings"
	"time"
)

type promptExecutionPlan struct {
	lane              analysisLane
	followupLane      analysisLane
	thoroughness      workspace.ExploreThoroughness
	reason            promptRouteReason
	contract          answerContract
	taskFamily        string
	permissionProfile string
	agentMode         agentDecisionMode
	workflowKind      agentWorkflowKind
	workflowPhase     agentWorkflowPhase
	intent            promptIntentDecision
}

func (m *Model) RunPromptOnce(ctx context.Context, prompt string) (string, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return "", fmt.Errorf("prompt is required")
	}
	m.emitJSONStart(prompt)
	if err := m.ensureServerReady(ctx); err != nil {
		m.emitJSONError("server_not_ready", err.Error())
		return "", err
	}
	if looksLikeResumeStructuralRefactorPrompt(prompt) && m.canResumeStructuralRefactor() {
		return m.runStructuralRefactorResumeOnce(ctx, prompt)
	}
	if shouldResumeWorkflowPrompt(prompt, m.currentWorkflowState()) {
		return m.runWorkflowResumeOnce(ctx, prompt)
	}
	if shouldResumeTaskMemoryPrompt(prompt, m.currentTaskMemory()) {
		return m.runTaskMemoryResumeOnce(ctx, prompt)
	}
	return m.runStandardPromptOnce(ctx, prompt)
}

func (m *Model) runStandardPromptOnce(ctx context.Context, prompt string) (string, error) {
	m.lastAssistantReasoningContent = ""
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
	if reply, ok := deterministicConversationMemoryNoteReply(prompt, plan); ok {
		return m.finishPromptOnceReply(reply), nil
	}
	m.logMessage("thinking_config", m.samplingSummary())
	reply, err := m.runPromptPlan(ctx, prompt, m.requestMessages(), m.chatOptions(), plan)
	if err != nil {
		m.emitJSONError("run_failed", err.Error())
		return "", err
	}
	return m.finishPromptOnceReply(reply), nil
}

func (m *Model) finishPromptOnceReply(reply string) string {
	m.messages = append(m.messages, llama.ChatMessage{Role: "assistant", Content: reply, ReasoningContent: m.takeLastAssistantReasoningContent()})
	m.logMessage("assistant", reply)
	m.emitJSONFinal(reply)
	return reply
}

func (m *Model) runStructuralRefactorResumeOnce(ctx context.Context, userPrompt string) (string, error) {
	stage := m.currentStructuralRefactorStage()
	if !structuralRefactorStageResumable(stage) {
		return "", fmt.Errorf("no resumable structural refactor stage is available")
	}
	prompt := buildStructuralRefactorResumePrompt(stage, userPrompt)
	analysisSummary := buildStructuralRefactorResumeAnalysisSummary(stage)
	plan := fromFrontierPromptPlan(frontier.BuildResumePromptPlan(frontier.ResumePromptPlanInput{
		UserPrompt:   userPrompt,
		ResumePrompt: prompt,
		Lane:         frontier.LaneImplement,
		Thoroughness: string(workspace.ThoroughnessMedium),
		Reason:       frontier.RouteReasonDirectImplementation,
	}))
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
	m.logLongHorizonState("resume", stage, "resuming next bounded structural refactor stage")
	history := m.requestMessages()
	history = appendAnalysisSummary(history, "architecture", analysisSummary)
	reply, err := m.runPromptPlan(ctx, prompt, history, m.chatOptions(), plan)
	if err != nil {
		m.emitJSONError("run_failed", err.Error())
		return "", err
	}
	return m.finishPromptOnceReply(reply), nil
}

func (m *Model) runTaskMemoryResumeOnce(ctx context.Context, userPrompt string) (string, error) {
	memory := m.currentTaskMemory()
	if !taskMemoryResumable(memory) {
		return "", fmt.Errorf("no resumable investigation or repair task is available")
	}
	prompt := buildTaskMemoryResumePrompt(memory, userPrompt)
	planInput := frontierTaskMemoryResumeInput(memory, userPrompt)
	planInput.ResumePrompt = prompt
	plan := fromFrontierPromptPlan(frontier.BuildTaskMemoryPromptPlan(planInput, string(workspace.ThoroughnessMedium)))
	resumeContext := buildTaskResumeContext(memory, userPrompt)
	if m.workspace != nil {
		m.workspace.PrimeDiscoveredPaths(uniqueLimitedStrings(append(append([]string(nil), resumeContext.ApprovedEditTargets...), resumeContext.ReadOnlyEvidencePaths...), 12))
	}
	m.resetCompactTrace()
	m.resetExecutionPanel(prompt, plan)
	displayPrompt := taskMemoryResumeDisplayPrompt(userPrompt)
	m.messages = append(m.messages, llama.ChatMessage{Role: "user", Content: displayPrompt})
	m.logMessage("user", displayPrompt)
	m.logMessage("task_memory_resume_prompt", prompt)
	if summary := formatPromptExecutionPlan(plan); summary != "" {
		m.logMessage("route_plan", summary)
	}
	m.logTaskMemoryState("resume", memory, "resuming saved investigation or repair task")
	history := m.requestMessages()
	history = append(history,
		llama.ChatMessage{Role: "system", Content: "Internal task-memory resume packet:\n" + prompt},
		llama.ChatMessage{Role: "system", Content: renderTaskMemoryForModel(memory)},
	)
	reply, err := m.runPromptPlan(ctx, prompt, history, m.chatOptions(), plan, resumeContext)
	if err != nil {
		m.emitJSONError("run_failed", err.Error())
		return "", err
	}
	return m.finishPromptOnceReply(reply), nil
}

func (m *Model) runWorkflowResumeOnce(ctx context.Context, userPrompt string) (string, error) {
	state := m.currentWorkflowState()
	if !workflowStateResumable(state) {
		return "", fmt.Errorf("no resumable workflow state is available")
	}
	prompt := buildWorkflowResumePrompt(state, userPrompt)
	enabled := m.workspace != nil && m.workspace.Enabled()
	normal := enabled && m.workspace.Mode() == workspace.ModeNormal
	defaultThoroughness := ""
	if m.workspace != nil {
		defaultThoroughness = string(m.workspace.ExploreThoroughness())
	}
	plan := fromFrontierPromptPlan(frontier.ClassifyPromptPlan(frontier.PromptPlanInput{
		Prompt:                     userPrompt,
		WorkspaceEnabled:           enabled,
		WorkspaceNormal:            normal,
		DefaultThoroughness:        defaultThoroughness,
		Recent:                     m.recentConversationText(6),
		HasRecentReplanToImplement: m.lastReplan != nil && m.lastReplan.ToLane == analysisLaneImplement,
		AgentMode:                  string(m.currentAgentDecisionMode()),
		Workflow:                   frontierWorkflowState(state),
	}))
	resumeContext := buildWorkflowResumeContext(state)
	requiresConcreteAnchor := workflowResumeShouldStopForConcreteAnchor(userPrompt, state)
	if !requiresConcreteAnchor {
		state.Phase = agentWorkflowPhaseBuilding
		state.LastPrompt = strings.TrimSpace(userPrompt)
		m.rememberWorkflowState(state, "workflow plan approved for build-mode execution")
		if m.workspace != nil {
			m.workspace.PrimeDiscoveredPaths(uniqueLimitedStrings(append(append([]string(nil), resumeContext.ApprovedEditTargets...), resumeContext.ReadOnlyEvidencePaths...), 12))
		}
	}
	m.resetCompactTrace()
	m.resetExecutionPanel(prompt, plan)
	displayPrompt := strings.TrimSpace(userPrompt)
	if displayPrompt == "" {
		displayPrompt = "/workflow implement-plan"
	}
	m.messages = append(m.messages, llama.ChatMessage{Role: "user", Content: displayPrompt})
	m.logMessage("user", displayPrompt)
	m.logMessage("workflow_resume_prompt", prompt)
	if summary := formatPromptExecutionPlan(plan); summary != "" {
		m.logMessage("route_plan", summary)
	}
	if requiresConcreteAnchor {
		reply := workflowConcreteAnchorRequiredReply(state)
		m.logWorkflowState("resume", state, "workflow resume stopped before editing because only file-level targets are approved")
		return m.finishPromptOnceReply(reply), nil
	}
	m.logWorkflowState("resume", state, "resuming typed workflow state")
	history := m.requestMessages()
	history = append(history,
		llama.ChatMessage{Role: "system", Content: "Internal workflow resume packet:\n" + prompt},
		llama.ChatMessage{Role: "system", Content: renderWorkflowStateForModel(state)},
	)
	previousJournal := m.lastJournal
	reply, err := m.runPromptPlan(ctx, prompt, history, m.chatOptions(), plan, resumeContext)
	if err != nil {
		m.markWorkflowIncomplete(state, "implementation resume failed: "+err.Error())
		m.emitJSONError("run_failed", err.Error())
		return "", err
	}
	if m.workflowImplementationAccepted(state, previousJournal) {
		m.markWorkflowComplete("implementation run verified and produced an accepted execution journal")
	} else {
		m.markWorkflowIncomplete(state, "implementation returned without a verified accepted execution journal")
	}
	return m.finishPromptOnceReply(reply), nil
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

func (m *Model) sendPromptAsync(prompt string) {
	if looksLikeResumeStructuralRefactorPrompt(prompt) && m.canResumeStructuralRefactor() {
		m.resumeStructuralRefactorAsync(prompt)
		return
	}
	if shouldResumeWorkflowPrompt(prompt, m.currentWorkflowState()) {
		m.resumeWorkflowAsync(prompt)
		return
	}
	if shouldResumeTaskMemoryPrompt(prompt, m.currentTaskMemory()) {
		m.resumeTaskMemoryAsync(prompt)
		return
	}
	plan := m.classifyPromptExecutionPlan(prompt)
	m.lastAssistantReasoningContent = ""
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
	if reply, ok := deterministicConversationMemoryNoteReply(prompt, plan); ok {
		m.messages = append(m.messages, llama.ChatMessage{Role: "assistant", Content: reply})
		m.logMessage("assistant", reply)
		m.emitJSONFinal(reply)
		m.status = "Noted."
		m.renderMessages(true)
		return
	}
	m.messages = append(m.messages, llama.ChatMessage{Role: "assistant", Content: ""})
	m.logMessage("thinking_config", m.samplingSummary())
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
	return frontier.PromptMentionedWorkspacePaths(prompt)
}

func deterministicConversationMemoryNoteReply(prompt string, plan promptExecutionPlan) (string, bool) {
	if plan.lane != analysisLaneMain || plan.reason != promptRouteReasonDirectConversation {
		return "", false
	}
	if !frontier.LooksLikeConversationMemoryNotePrompt(strings.ToLower(prompt)) {
		return "", false
	}
	return "Understood. I will keep that constraint for the next requested step.", true
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
	if plan.workflowKind != "" {
		b.WriteString(" workflow=")
		b.WriteString(string(plan.workflowKind))
	}
	if plan.workflowPhase != "" {
		b.WriteString(" phase=")
		b.WriteString(string(plan.workflowPhase))
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
		resumeContext = implementationResumeContextFromPromptPlan(m.workspace, prompt, history, plan, resumeContext)
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
	return frontier.PromptRequestsWholeFileRefactor(prompt)
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
	return frontier.QuotedPromptFragments(prompt)
}

func looksLikeUISpacingPrompt(prompt string) bool {
	return frontier.LooksLikeUISpacingPrompt(prompt)
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
	return frontier.BuildSubagentSystemPrompt(basePrompt, workspaceInstructions)
}

func buildExploreTaskPrompt(prompt string) string {
	return frontier.BuildExploreTaskPrompt(prompt)
}

func buildReviewSystemPrompt(basePrompt string) string {
	return frontier.BuildReviewSystemPrompt(basePrompt)
}

func buildArchitectureSystemPrompt(basePrompt string) string {
	return frontier.BuildArchitectureSystemPrompt(basePrompt)
}

func buildReviewTaskPrompt(prompt string) string {
	return frontier.BuildReviewTaskPrompt(prompt)
}

func buildArchitectureTaskPrompt(prompt string) string {
	return frontier.BuildArchitectureTaskPrompt(prompt)
}

func buildRouteAmbiguitySnapshot(prompt string, plan promptExecutionPlan) routeAmbiguitySnapshot {
	return frontier.BuildRouteAmbiguitySnapshot(prompt, toFrontierPromptPlan(plan))
}

func routeAmbiguitySignals(signals []string) []string {
	return frontier.RouteAmbiguitySignals(signals)
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
	return frontier.PromptIntentVerifierReasons(toFrontierPromptPlan(plan), rails)
}

func countRouteIntentSignals(signals []string) int {
	return frontier.CountRouteIntentSignals(signals)
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
		{Role: "user", Content: buildPromptIntentVerifierTaskPrompt(prompt, history, plan, rails, reasons, m.renderRoutingMemoryForVerifier())},
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
	return frontier.BuildPromptIntentVerifierSystemPrompt()
}

func buildPromptIntentVerifierTaskPrompt(prompt string, history []llama.ChatMessage, plan promptExecutionPlan, rails deterministicControlRailsSnapshot, reasons []string, taskMemory string) string {
	return frontier.BuildPromptIntentVerifierTaskPrompt(frontier.IntentVerifierTaskPromptInput{
		Prompt:     prompt,
		History:    renderIntentVerifierHistory(history),
		Plan:       toFrontierPromptPlan(plan),
		Rails:      rails,
		Reasons:    reasons,
		TaskMemory: taskMemory,
	})
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
	return frontier.ParseIntentVerifierResult(reply)
}

func normalizePromptIntentVerifierResult(result promptIntentVerifierResult) promptIntentVerifierResult {
	return frontier.NormalizeIntentVerifierResult(result)
}

func reconcilePromptPlanWithVerifier(prompt string, plan promptExecutionPlan, verifier promptIntentVerifierResult, memory ...*longHorizonTaskMemory) (promptExecutionPlan, bool, bool, []string) {
	memoryKindValue := ""
	if len(memory) > 0 {
		memoryKindValue = memoryKind(memory[0])
	}
	result := frontier.ReconcilePromptPlanWithVerifier(prompt, toFrontierPromptPlan(plan), verifier, memoryKindValue)
	return fromFrontierPromptPlan(result.Plan), result.Changed, result.SafetyOverride, result.Notes
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

func toFrontierPromptPlan(plan promptExecutionPlan) frontier.PromptPlan {
	return frontier.PromptPlan{
		Lane:              plan.lane,
		FollowupLane:      plan.followupLane,
		Thoroughness:      string(plan.thoroughness),
		Reason:            plan.reason,
		Contract:          plan.contract,
		TaskFamily:        plan.taskFamily,
		PermissionProfile: plan.permissionProfile,
		AgentMode:         string(plan.agentMode),
		WorkflowKind:      string(plan.workflowKind),
		WorkflowPhase:     string(plan.workflowPhase),
		Intent:            plan.intent,
	}
}

func fromFrontierPromptPlan(plan frontier.PromptPlan) promptExecutionPlan {
	return promptExecutionPlan{
		lane:              plan.Lane,
		followupLane:      plan.FollowupLane,
		thoroughness:      workspace.ExploreThoroughness(plan.Thoroughness),
		reason:            plan.Reason,
		contract:          plan.Contract,
		taskFamily:        plan.TaskFamily,
		permissionProfile: plan.PermissionProfile,
		agentMode:         agentDecisionMode(plan.AgentMode),
		workflowKind:      agentWorkflowKind(plan.WorkflowKind),
		workflowPhase:     agentWorkflowPhase(plan.WorkflowPhase),
		intent:            plan.Intent,
	}
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

func classifyPromptExecutionPlan(prompt string, mgr *workspace.Manager) promptExecutionPlan {
	enabled := mgr != nil && mgr.Enabled()
	normal := enabled && mgr.Mode() == workspace.ModeNormal
	defaultThoroughness := ""
	if mgr != nil {
		defaultThoroughness = string(mgr.ExploreThoroughness())
	}
	return fromFrontierPromptPlan(frontier.ClassifyPromptPlan(frontier.PromptPlanInput{
		Prompt:              prompt,
		WorkspaceEnabled:    enabled,
		WorkspaceNormal:     normal,
		DefaultThoroughness: defaultThoroughness,
	}))
}

func (m *Model) classifyPromptExecutionPlan(prompt string) promptExecutionPlan {
	if m == nil {
		return promptExecutionPlan{}
	}
	enabled := m.workspace != nil && m.workspace.Enabled()
	normal := enabled && m.workspace.Mode() == workspace.ModeNormal
	defaultThoroughness := ""
	if m.workspace != nil {
		defaultThoroughness = string(m.workspace.ExploreThoroughness())
	}
	hasRecentReplanToImplement := m.lastReplan != nil && m.lastReplan.ToLane == analysisLaneImplement
	plan := fromFrontierPromptPlan(frontier.ClassifyPromptPlan(frontier.PromptPlanInput{
		Prompt:                     prompt,
		WorkspaceEnabled:           enabled,
		WorkspaceNormal:            normal,
		DefaultThoroughness:        defaultThoroughness,
		Recent:                     m.recentConversationText(6),
		HasRecentReplanToImplement: hasRecentReplanToImplement,
		AgentMode:                  string(m.currentAgentDecisionMode()),
		Workflow:                   frontierWorkflowState(m.currentWorkflowState()),
	}))
	return plan
}

func shouldIgnoreVerifierTargetRefsForPrompt(prompt string, targetRefs []string) bool {
	return frontier.ShouldIgnoreVerifierTargetRefsForPrompt(prompt, targetRefs)
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
	return frontier.LaneUsesThoroughness(lane)
}

func promptRequestsRemoval(prompt string) bool {
	return frontier.PromptRequestsRemoval(prompt)
}

func looksLikeIssueHuntPrompt(prompt string) bool {
	return frontier.LooksLikeIssueHuntPrompt(prompt)
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
	return frontier.LooksLikeCodeSmellPrompt(prompt)
}
