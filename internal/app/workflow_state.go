package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"qwen-bubbles/internal/llama"
	"strings"
	"time"
)

type agentWorkflowKind string

const (
	agentWorkflowKindOptimizationRefactor agentWorkflowKind = "optimization_refactor"
	agentWorkflowKindDurablePlan          agentWorkflowKind = "durable_plan"
)

type agentWorkflowPhase string

const (
	agentWorkflowPhaseDiscovered       agentWorkflowPhase = "discovered"
	agentWorkflowPhasePlanning         agentWorkflowPhase = "planning"
	agentWorkflowPhaseAwaitingApproval agentWorkflowPhase = "awaiting_approval"
	agentWorkflowPhaseBuilding         agentWorkflowPhase = "building"
	agentWorkflowPhaseComplete         agentWorkflowPhase = "complete"
)

type agentWorkflowState struct {
	Active              bool                     `json:"active"`
	Kind                agentWorkflowKind        `json:"kind,omitempty"`
	Phase               agentWorkflowPhase       `json:"phase,omitempty"`
	Objective           string                   `json:"objective,omitempty"`
	LastPrompt          string                   `json:"last_prompt,omitempty"`
	SourceLane          string                   `json:"source_lane,omitempty"`
	PlanSummary         string                   `json:"plan_summary,omitempty"`
	TargetRefs          []string                 `json:"target_refs,omitempty"`
	EvidencePaths       []string                 `json:"evidence_paths,omitempty"`
	EditableTargets     []string                 `json:"editable_targets,omitempty"`
	ReadOnlyEvidence    []string                 `json:"read_only_evidence,omitempty"`
	AllowedDestinations []string                 `json:"allowed_destinations,omitempty"`
	ForbiddenPaths      []string                 `json:"forbidden_paths,omitempty"`
	Verification        []string                 `json:"verification,omitempty"`
	NextActions         []string                 `json:"next_actions,omitempty"`
	FileFingerprints    map[string]taskFileStamp `json:"file_fingerprints,omitempty"`
	CreatedAt           time.Time                `json:"created_at,omitempty"`
	UpdatedAt           time.Time                `json:"updated_at,omitempty"`
}

type persistedAgentWorkflowState struct {
	Version       int                `json:"version"`
	WorkspaceRoot string             `json:"workspace_root,omitempty"`
	UpdatedAt     time.Time          `json:"updated_at"`
	State         agentWorkflowState `json:"state"`
}

const agentWorkflowStateVersion = 2

func (m *Model) workflowStatePath() string {
	if m == nil || strings.TrimSpace(m.cfg.Chat.SaveDir) == "" {
		return ""
	}
	return filepath.Join(m.cfg.Chat.SaveDir, "state", "workflow-state.json")
}

func cloneWorkflowState(state *agentWorkflowState) *agentWorkflowState {
	if state == nil {
		return nil
	}
	cloned := *state
	cloned.TargetRefs = append([]string(nil), state.TargetRefs...)
	cloned.EvidencePaths = append([]string(nil), state.EvidencePaths...)
	cloned.EditableTargets = append([]string(nil), state.EditableTargets...)
	cloned.ReadOnlyEvidence = append([]string(nil), state.ReadOnlyEvidence...)
	cloned.AllowedDestinations = append([]string(nil), state.AllowedDestinations...)
	cloned.ForbiddenPaths = append([]string(nil), state.ForbiddenPaths...)
	cloned.Verification = append([]string(nil), state.Verification...)
	cloned.NextActions = append([]string(nil), state.NextActions...)
	if len(state.FileFingerprints) > 0 {
		cloned.FileFingerprints = make(map[string]taskFileStamp, len(state.FileFingerprints))
		for path, stamp := range state.FileFingerprints {
			cloned.FileFingerprints[path] = stamp
		}
	}
	return &cloned
}

func workflowStateResumable(state *agentWorkflowState) bool {
	if state == nil || !state.Active || state.Kind == "" {
		return false
	}
	switch state.Phase {
	case "", agentWorkflowPhaseComplete:
		return false
	default:
		return true
	}
}

func (m *Model) currentWorkflowState() *agentWorkflowState {
	if m == nil || !workflowStateResumable(m.workflowState) {
		return nil
	}
	if stale, reason := m.workflowStateStale(m.workflowState); stale {
		m.clearPersistedWorkflowState(reason)
		return nil
	}
	return m.workflowState
}

func (m *Model) loadPersistedWorkflowState() {
	path := m.workflowStatePath()
	if path == "" {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var state persistedAgentWorkflowState
	if err := json.Unmarshal(data, &state); err != nil {
		return
	}
	if state.Version != agentWorkflowStateVersion {
		m.removePersistedWorkflowState()
		return
	}
	if root := m.workspaceRootForState(); root != "" && state.WorkspaceRoot != "" && filepath.Clean(state.WorkspaceRoot) != root {
		return
	}
	workflow := cloneWorkflowState(&state.State)
	if !workflowStateResumable(workflow) {
		m.removePersistedWorkflowState()
		return
	}
	if stale, reason := m.workflowStateStaleOnLoad(workflow); stale {
		m.logWorkflowState("clear", workflow, reason)
		m.removePersistedWorkflowState()
		return
	}
	m.workflowState = workflow
}

func (m *Model) persistWorkflowState(state *agentWorkflowState) {
	path := m.workflowStatePath()
	if path == "" {
		m.workflowState = cloneWorkflowState(state)
		return
	}
	if !workflowStateResumable(state) {
		m.removePersistedWorkflowState()
		return
	}
	cloned := cloneWorkflowState(state)
	cloned.FileFingerprints = m.taskMemoryFileFingerprints(workflowStateFingerprintPaths(cloned))
	now := time.Now()
	if cloned.CreatedAt.IsZero() {
		cloned.CreatedAt = now
	}
	cloned.UpdatedAt = now
	persisted := persistedAgentWorkflowState{
		Version:       agentWorkflowStateVersion,
		WorkspaceRoot: m.workspaceRootForState(),
		UpdatedAt:     now,
		State:         *cloned,
	}
	data, err := json.MarshalIndent(persisted, "", "  ")
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err == nil {
		m.workflowState = cloned
	}
}

func (m *Model) removePersistedWorkflowState() {
	path := m.workflowStatePath()
	if path == "" {
		return
	}
	_ = os.Remove(path)
}

func (m *Model) clearPersistedWorkflowState(reason string) {
	if m == nil {
		return
	}
	if m.workflowState != nil {
		m.logWorkflowState("clear", m.workflowState, reason)
	}
	m.workflowState = nil
	m.removePersistedWorkflowState()
}

func (m *Model) rememberWorkflowState(state *agentWorkflowState, reason string) {
	if m == nil || !workflowStateResumable(state) {
		return
	}
	m.persistWorkflowState(state)
	m.logWorkflowState("persist", state, reason)
}

func workflowStateFingerprintPaths(state *agentWorkflowState) []string {
	if state == nil {
		return nil
	}
	return uniqueLimitedStrings(append(append(append(append(
		append([]string(nil), state.EditableTargets...),
		state.ReadOnlyEvidence...),
		state.EvidencePaths...),
		state.TargetRefs...),
		state.AllowedDestinations...,
	), 32)
}

func (m *Model) workflowStateStaleOnLoad(state *agentWorkflowState) (bool, string) {
	if state == nil {
		return false, ""
	}
	if len(state.FileFingerprints) == 0 && len(workflowStateFingerprintPaths(state)) > 0 {
		return true, "persisted workflow state predates target fingerprints; re-plan before build-mode execution"
	}
	return m.workflowStateStale(state)
}

func (m *Model) workflowStateStale(state *agentWorkflowState) (bool, string) {
	if state == nil || len(state.FileFingerprints) == 0 {
		return false, ""
	}
	root := m.workspaceRootForState()
	if root == "" {
		return false, ""
	}
	for path, expected := range state.FileFingerprints {
		full := filepath.Join(root, filepath.FromSlash(path))
		stat, err := os.Stat(full)
		if err != nil {
			return true, "tracked workflow target is no longer readable: " + path
		}
		actual := taskFileStamp{Size: stat.Size(), ModTimeUnix: stat.ModTime().UnixNano()}
		if actual != expected {
			return true, "tracked workflow target changed outside the saved workflow state: " + path
		}
	}
	return false, ""
}

func (m *Model) logWorkflowState(action string, state *agentWorkflowState, reason string) {
	if m == nil || strings.TrimSpace(action) == "" {
		return
	}
	payload := struct {
		Action              string   `json:"action"`
		Kind                string   `json:"kind,omitempty"`
		Phase               string   `json:"phase,omitempty"`
		SourceLane          string   `json:"source_lane,omitempty"`
		Objective           string   `json:"objective,omitempty"`
		TargetRefs          []string `json:"target_refs,omitempty"`
		EditableTargets     []string `json:"editable_targets,omitempty"`
		ReadOnlyEvidence    []string `json:"read_only_evidence,omitempty"`
		EvidencePaths       []string `json:"evidence_paths,omitempty"`
		AllowedDestinations []string `json:"allowed_destinations,omitempty"`
		Verification        []string `json:"verification,omitempty"`
		NextActions         []string `json:"next_actions,omitempty"`
		FileFingerprints    []string `json:"file_fingerprints,omitempty"`
		Resumable           bool     `json:"resumable"`
		Reason              string   `json:"reason,omitempty"`
	}{
		Action:    strings.TrimSpace(action),
		Resumable: workflowStateResumable(state),
		Reason:    strings.TrimSpace(reason),
	}
	if state != nil {
		payload.Kind = string(state.Kind)
		payload.Phase = string(state.Phase)
		payload.SourceLane = strings.TrimSpace(state.SourceLane)
		payload.Objective = strings.TrimSpace(state.Objective)
		payload.TargetRefs = uniqueNormalizedPaths(state.TargetRefs)
		payload.EditableTargets = uniqueNormalizedPaths(state.EditableTargets)
		payload.ReadOnlyEvidence = uniqueNormalizedPaths(state.ReadOnlyEvidence)
		payload.EvidencePaths = uniqueNormalizedPaths(state.EvidencePaths)
		payload.AllowedDestinations = uniqueNormalizedPaths(state.AllowedDestinations)
		payload.Verification = uniqueNonEmptyStrings(state.Verification)
		payload.NextActions = uniqueNonEmptyStrings(state.NextActions)
		payload.FileFingerprints = workflowFingerprintPaths(state.FileFingerprints)
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	m.logMessage("workflow_state", string(data))
}

func workflowFingerprintPaths(fingerprints map[string]taskFileStamp) []string {
	if len(fingerprints) == 0 {
		return nil
	}
	paths := make(map[string]struct{}, len(fingerprints))
	for path := range fingerprints {
		paths[path] = struct{}{}
	}
	return mapKeysSorted(paths)
}

func (m *Model) rememberWorkflowFromTaskMemory(prompt string, lane analysisLane, memory *longHorizonTaskMemory, evidencePaths []string, nextAction string) {
	if m == nil || lane != analysisLaneArchitecture || memory == nil {
		return
	}
	profile := buildPromptRouteProfile(prompt)
	current := cloneWorkflowState(m.currentWorkflowState())
	if current == nil && !looksLikeOptimizationWorkflowPrompt(prompt, profile) && strings.TrimSpace(memory.Kind) != "durable_plan" {
		return
	}
	if current == nil {
		current = &agentWorkflowState{
			Active:    true,
			Kind:      inferWorkflowKind(prompt, memory, profile),
			Phase:     agentWorkflowPhaseDiscovered,
			Objective: strings.TrimSpace(prompt),
			CreatedAt: time.Now(),
		}
	}
	current.Active = true
	if current.Kind == "" {
		current.Kind = inferWorkflowKind(prompt, memory, profile)
	}
	if strings.TrimSpace(current.Objective) == "" {
		current.Objective = strings.TrimSpace(prompt)
	}
	current.LastPrompt = strings.TrimSpace(prompt)
	current.SourceLane = string(lane)
	current.TargetRefs = uniqueLimitedStrings(append(append([]string(nil), current.TargetRefs...), memory.ChosenTargets...), 16)
	current.EvidencePaths = uniqueLimitedStrings(append(append([]string(nil), current.EvidencePaths...), evidencePaths...), 24)
	current.NextActions = uniqueNonEmptyStrings(append(append([]string(nil), memory.NextActions...), nextAction))
	if strings.TrimSpace(memory.Kind) == "durable_plan" {
		current.Phase = agentWorkflowPhaseAwaitingApproval
		current.PlanSummary = strings.TrimSpace(memory.Summary)
		current.EditableTargets = uniqueLimitedStrings(taskMemoryTargetsForResume(memory, ""), 8)
		current.ReadOnlyEvidence = uniqueLimitedStrings(taskMemoryEvidencePaths(memory, current.EditableTargets), 16)
		current.AllowedDestinations = uniqueLimitedStrings(durablePlanAllowedDestinations(memory.Summary), 8)
		current.Verification = uniqueLimitedStrings(taskMemoryVerificationPlan(memory), 8)
		current.NextActions = uniqueNonEmptyStrings(append([]string{
			"wait for explicit approval before build-mode execution",
			"on approval, resume from typed workflow state instead of reconstructing from prose",
		}, current.NextActions...))
	} else if profile.wantsPlanOnly {
		current.Phase = agentWorkflowPhasePlanning
		current.PlanSummary = strings.TrimSpace(memory.Summary)
	} else if current.Phase == "" || current.Phase == agentWorkflowPhaseComplete {
		current.Phase = agentWorkflowPhaseDiscovered
	}
	m.rememberWorkflowState(current, "architecture workflow state updated from analysis lane")
}

func looksLikeOptimizationWorkflowPrompt(prompt string, profile promptRouteProfile) bool {
	normalized := strings.ToLower(strings.TrimSpace(prompt))
	if normalized == "" || !profile.wantsArchitecture {
		return false
	}
	return containsAny(
		normalized,
		"optimization opportunity",
		"optimization opportunities",
		"optimize our architecture",
		"optimize the architecture",
		"refactor opportunity",
		"refactor opportunities",
		"refactoring opportunity",
		"refactoring opportunities",
		"plan a refactor",
		"plan the refactor",
		"refactor plan",
		"refactoring plan",
		"maintainability",
		"package boundary",
		"package boundaries",
	)
}

func inferWorkflowKind(prompt string, memory *longHorizonTaskMemory, profile promptRouteProfile) agentWorkflowKind {
	if looksLikeOptimizationWorkflowPrompt(prompt, profile) {
		return agentWorkflowKindOptimizationRefactor
	}
	if strings.TrimSpace(memoryKind(memory)) == "durable_plan" {
		return agentWorkflowKindDurablePlan
	}
	return agentWorkflowKindOptimizationRefactor
}

func workflowApprovalDetected(prompt string, state *agentWorkflowState) bool {
	if !workflowStateResumable(state) {
		return false
	}
	if state.Phase != agentWorkflowPhaseAwaitingApproval {
		return false
	}
	if !workflowStateHasApprovalScope(state) {
		return false
	}
	return looksLikeWorkflowApprovalPrompt(prompt)
}

func workflowStateHasApprovalScope(state *agentWorkflowState) bool {
	if state == nil {
		return false
	}
	return len(uniqueNonEmptyStrings(state.EditableTargets)) > 0 || len(uniqueNonEmptyStrings(state.AllowedDestinations)) > 0
}

func looksLikeWorkflowApprovalPrompt(prompt string) bool {
	normalized := strings.ToLower(strings.TrimSpace(prompt))
	normalized = strings.ReplaceAll(normalized, "’", "'")
	trimmed := strings.Trim(normalized, " \t\r\n.!?")
	if trimmed == "" {
		return false
	}
	if containsAny(trimmed, "don't", "do not", "not yet", "hold off", "wait", "without performing", "without applying", "without implementing") {
		return false
	}
	if looksLikeDurablePlanApprovalPrompt(trimmed) || looksLikeTerseDurablePlanApprovalPrompt(trimmed) {
		return true
	}
	switch trimmed {
	case "commence",
		"please commence",
		"let's commence",
		"lets commence",
		"build it",
		"please build it",
		"build this",
		"build that",
		"execute it",
		"please execute it",
		"apply all of it",
		"implement everything",
		"perform it":
		return true
	default:
		return containsAny(trimmed, "commence", "start building", "begin building", "perform the approved plan", "execute the approved plan")
	}
}

func shouldResumeWorkflowPrompt(prompt string, state *agentWorkflowState) bool {
	return workflowApprovalDetected(prompt, state)
}

func (m *Model) applyWorkflowStateToPromptExecutionPlan(prompt string, plan promptExecutionPlan) promptExecutionPlan {
	state := m.currentWorkflowState()
	if state == nil {
		return plan
	}
	if workflowApprovalDetected(prompt, state) {
		plan.lane = analysisLaneImplement
		plan.followupLane = ""
		plan.reason = promptRouteReasonDirectImplementation
		plan.contract = answerContractForLane(analysisLaneImplement)
		plan.workflowKind = state.Kind
		plan.workflowPhase = agentWorkflowPhaseBuilding
		plan.intent.WantsEdits = true
		plan.intent.WantsPlanOnly = false
		plan.intent.Vetoes = removePromptIntentVetoes(plan.intent.Vetoes, "plan_only_blocks_edits", "read_only_boundary_blocks_edits")
		return finalizePromptExecutionPlan(prompt, buildPromptRouteProfile(prompt), plan)
	}
	if workflowPlanningFollowupPrompt(prompt, state) {
		plan.lane = analysisLaneArchitecture
		plan.followupLane = ""
		plan.reason = promptRouteReasonArchitectureRequest
		plan.contract = answerContractForLane(analysisLaneArchitecture)
		plan.workflowKind = state.Kind
		plan.workflowPhase = agentWorkflowPhasePlanning
		plan.intent.WantsEdits = false
		plan.intent.WantsPlanOnly = true
		plan.intent.Vetoes = uniqueNonEmptyStrings(append(plan.intent.Vetoes, "workflow_plan_phase_blocks_edits"))
		return finalizePromptExecutionPlan(prompt, buildPromptRouteProfile(prompt), plan)
	}
	plan.workflowKind = state.Kind
	plan.workflowPhase = state.Phase
	return plan
}

func workflowPlanningFollowupPrompt(prompt string, state *agentWorkflowState) bool {
	if !workflowStateResumable(state) {
		return false
	}
	if state.Phase != agentWorkflowPhaseDiscovered && state.Phase != agentWorkflowPhasePlanning {
		return false
	}
	normalized := strings.ToLower(strings.TrimSpace(prompt))
	return looksLikePlanOnlyPrompt(normalized) &&
		containsAny(normalized, "refactor", "refactoring", "optimize", "optimization", "architecture", "architectural", "this file", "this one", "the chosen", "selected")
}

func buildWorkflowResumeContext(state *agentWorkflowState) taskResumeContext {
	if !workflowStateResumable(state) {
		return taskResumeContext{}
	}
	approved := uniqueLimitedStrings(firstNonEmptyStringSlice(state.EditableTargets, state.TargetRefs), 8)
	evidence := uniqueLimitedStrings(append(append([]string(nil), state.ReadOnlyEvidence...), state.EvidencePaths...), 16)
	return taskResumeContext{
		Active:                true,
		Kind:                  "workflow_plan",
		SourceLane:            analysisLaneArchitecture,
		ApprovedEditTargets:   approved,
		ReadOnlyEvidencePaths: durablePlanReadOnlyEvidence(evidence, approved),
		AllowedDestinations:   uniqueLimitedStrings(state.AllowedDestinations, 8),
		ForbiddenPaths:        uniqueLimitedStrings(state.ForbiddenPaths, 16),
		VerificationPlan:      uniqueLimitedStrings(state.Verification, 8),
		OpenGaps:              uniqueNonEmptyStrings(state.NextActions),
	}
}

func firstNonEmptyStringSlice(candidates ...[]string) []string {
	for _, candidate := range candidates {
		if values := uniqueNonEmptyStrings(candidate); len(values) > 0 {
			return values
		}
	}
	return nil
}

func buildWorkflowResumePrompt(state *agentWorkflowState, userPrompt string) string {
	if state == nil {
		return ""
	}
	lines := []string{
		"Resume the typed agent workflow from persisted workflow state.",
	}
	if strings.TrimSpace(userPrompt) != "" {
		lines = append(lines, "User resume request: "+strings.TrimSpace(userPrompt))
	}
	lines = append(lines,
		"Workflow kind: "+string(state.Kind),
		"Workflow phase: "+string(state.Phase),
	)
	if strings.TrimSpace(state.Objective) != "" {
		lines = append(lines, "Original objective: "+strings.TrimSpace(state.Objective))
	}
	lines = append(lines,
		"Plan approval: explicit approval detected; switch to build mode.",
		"Build-switch reminder: restate objective, constraints, target files, and verification checks before the first write.",
	)
	if len(state.EditableTargets) > 0 {
		lines = append(lines, "Workflow-approved editable targets: "+strings.Join(uniqueNormalizedPaths(state.EditableTargets), ", "))
	}
	if len(state.ReadOnlyEvidence) > 0 {
		lines = append(lines, "Workflow read-only evidence: "+strings.Join(uniqueNormalizedPaths(state.ReadOnlyEvidence), ", "))
	}
	if len(state.AllowedDestinations) > 0 {
		lines = append(lines, "Workflow-approved new destinations: "+strings.Join(uniqueNormalizedPaths(state.AllowedDestinations), ", "))
	}
	if len(state.Verification) > 0 {
		lines = append(lines, "Workflow verification expectations: "+strings.Join(uniqueNonEmptyStrings(state.Verification), " | "))
	}
	if strings.TrimSpace(state.PlanSummary) != "" {
		lines = append(lines, "Prior plan artifact:\n"+truncateText(state.PlanSummary, 1400))
	}
	lines = append(lines,
		"Rules:",
		"- Do not repeat broad discovery that is already represented in workflow state.",
		"- Treat editable targets, read-only evidence, allowed destinations, forbidden paths, and verification expectations as the authoritative host contract.",
		"- Do not edit read-only evidence paths or forbidden paths.",
		"- Run targeted verification after the bounded implementation or report the concrete blocker.",
	)
	return strings.Join(lines, "\n")
}

func renderWorkflowStateForModel(state *agentWorkflowState) string {
	if !workflowStateResumable(state) {
		return ""
	}
	var b strings.Builder
	b.WriteString("Persisted workflow state:\n")
	b.WriteString("- kind: " + string(state.Kind) + "\n")
	b.WriteString("- phase: " + string(state.Phase) + "\n")
	if strings.TrimSpace(state.Objective) != "" {
		b.WriteString("- objective: " + truncateText(state.Objective, 240) + "\n")
	}
	if len(state.EditableTargets) > 0 {
		b.WriteString("- editable targets: " + strings.Join(uniqueNormalizedPaths(state.EditableTargets), ", ") + "\n")
	}
	if len(state.ReadOnlyEvidence) > 0 {
		b.WriteString("- read-only evidence: " + strings.Join(uniqueNormalizedPaths(state.ReadOnlyEvidence), ", ") + "\n")
	}
	if len(state.AllowedDestinations) > 0 {
		b.WriteString("- allowed destinations: " + strings.Join(uniqueNormalizedPaths(state.AllowedDestinations), ", ") + "\n")
	}
	if len(state.Verification) > 0 {
		b.WriteString("- verification: " + strings.Join(uniqueNonEmptyStrings(state.Verification), " | ") + "\n")
	}
	return strings.TrimSpace(b.String())
}

func (m *Model) renderRoutingMemoryForVerifier() string {
	parts := make([]string, 0, 2)
	if task := renderTaskMemoryForModel(m.currentTaskMemory()); task != "" {
		parts = append(parts, task)
	}
	if workflow := renderWorkflowStateForModel(m.currentWorkflowState()); workflow != "" {
		parts = append(parts, workflow)
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, "\n\n")
}

func (m *Model) resumeWorkflowAsync(userPrompt string) {
	if m.streaming {
		m.status = "Wait for the current response to finish before resuming the workflow."
		return
	}
	state := cloneWorkflowState(m.currentWorkflowState())
	if !shouldResumeWorkflowPrompt(userPrompt, state) {
		m.status = "No approved workflow plan is available to resume."
		return
	}
	prompt := buildWorkflowResumePrompt(state, userPrompt)
	plan := promptExecutionPlan{
		lane:          analysisLaneImplement,
		reason:        promptRouteReasonDirectImplementation,
		contract:      answerContractForLane(analysisLaneImplement),
		workflowKind:  state.Kind,
		workflowPhase: agentWorkflowPhaseBuilding,
	}
	plan.intent = buildPromptIntentDecision(userPrompt, buildPromptRouteProfile(userPrompt), m.recentConversationText(6), plan)
	plan.intent.WantsEdits = true
	plan.intent.WantsPlanOnly = false
	plan.intent.Vetoes = removePromptIntentVetoes(plan.intent.Vetoes, "plan_only_blocks_edits", "read_only_boundary_blocks_edits")
	plan = finalizePromptExecutionPlan(userPrompt, buildPromptRouteProfile(userPrompt), plan)
	resumeContext := buildWorkflowResumeContext(state)
	state.Phase = agentWorkflowPhaseBuilding
	state.LastPrompt = strings.TrimSpace(userPrompt)
	m.rememberWorkflowState(state, "workflow plan approved for build-mode execution")
	if m.workspace != nil {
		m.workspace.PrimeDiscoveredPaths(uniqueLimitedStrings(append(append([]string(nil), resumeContext.ApprovedEditTargets...), resumeContext.ReadOnlyEvidencePaths...), 12))
	}
	m.resetCompactTrace()
	m.resetExecutionPanel(prompt, plan)

	displayPrompt := strings.TrimSpace(userPrompt)
	if displayPrompt == "" {
		displayPrompt = "/workflow implement-plan"
	}
	m.messages = append(m.messages, llama.ChatMessage{Role: "user", Content: displayPrompt})
	m.messages = append(m.messages, llama.ChatMessage{Role: "assistant", Content: ""})
	m.logMessage("user", displayPrompt)
	m.logMessage("workflow_resume_prompt", prompt)
	if summary := formatPromptExecutionPlan(plan); summary != "" {
		m.logMessage("route_plan", summary)
	}
	m.logWorkflowState("resume", state, "resuming typed workflow state")
	m.streaming = true
	m.workingFrame = 0
	m.clearRuntimeError()
	m.setPhaseCard(analysisLaneImplement, "targeting", phaseStateActive, "Resuming approved workflow plan with typed target roles.")
	m.status = "Resuming approved workflow plan."
	m.renderMessages(true)

	history := m.requestMessages()
	history = append(history,
		llama.ChatMessage{Role: "system", Content: "Internal workflow resume packet:\n" + prompt},
		llama.ChatMessage{Role: "system", Content: renderWorkflowStateForModel(state)},
	)
	opts := m.chatOptions()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(m.cfg.Chat.RequestTimeoutSeconds)*time.Second)
		defer cancel()

		previousJournal := m.lastJournal
		reply, err := m.runPromptPlan(ctx, prompt, history, opts, plan, resumeContext)
		if err == nil {
			if m.workflowImplementationAccepted(state, previousJournal) {
				m.markWorkflowComplete("implementation run verified and produced an accepted execution journal")
			} else {
				m.markWorkflowIncomplete(state, "implementation returned without a verified accepted execution journal")
			}
			m.emitAssistantReply(reply)
		}
		m.events <- streamDoneMsg{err: err}
	}()
}

func (m *Model) workflowImplementationAccepted(state *agentWorkflowState, previousJournal *executionJournal) bool {
	if !workflowStateResumable(state) || m == nil || m.lastJournal == nil {
		return false
	}
	journal := m.lastJournal
	if journal == previousJournal {
		return false
	}
	if !journal.VerificationPassed || journal.VerificationFailed {
		return false
	}
	normalized := strings.ToLower(strings.TrimSpace(journal.Summary))
	if normalized == "" {
		return false
	}
	if len(journal.Paths) == 0 && !implementationSummaryReportsNoop(normalized) {
		return false
	}
	if containsAny(normalized, "control-rail gaps:", "verification_contract_unresolved", "final blocker:", "implementation agent did not complete", "verification failed", ": failed", "not run", "skipped") {
		return false
	}
	return true
}

func (m *Model) markWorkflowIncomplete(state *agentWorkflowState, reason string) {
	if !workflowStateResumable(state) {
		return
	}
	next := cloneWorkflowState(state)
	if next == nil {
		return
	}
	next.Phase = agentWorkflowPhaseAwaitingApproval
	next.NextActions = uniqueNonEmptyStrings(append([]string{
		"implementation did not reach verified completion; inspect the latest task memory or run targeted repair before marking the workflow complete",
	}, next.NextActions...))
	m.rememberWorkflowState(next, reason)
}

func (m *Model) markWorkflowComplete(reason string) {
	state := cloneWorkflowState(m.currentWorkflowState())
	if state == nil {
		return
	}
	state.Phase = agentWorkflowPhaseComplete
	state.Active = false
	m.workflowState = state
	m.removePersistedWorkflowState()
	m.logWorkflowState("complete", state, reason)
}
