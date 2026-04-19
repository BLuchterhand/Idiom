package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"qwen-bubbles/internal/llama"
	"qwen-bubbles/internal/workspace"
	"strings"
	"time"
)

type longHorizonTaskMemory struct {
	Active              bool                     `json:"active"`
	Kind                string                   `json:"kind,omitempty"`
	Prompt              string                   `json:"prompt,omitempty"`
	Lane                string                   `json:"lane,omitempty"`
	Summary             string                   `json:"summary,omitempty"`
	OpenGaps            []string                 `json:"open_gaps,omitempty"`
	AttemptedSearches   []string                 `json:"attempted_searches,omitempty"`
	ChosenTargets       []string                 `json:"chosen_targets,omitempty"`
	VerificationHistory []string                 `json:"verification_history,omitempty"`
	NextActions         []string                 `json:"next_actions,omitempty"`
	FileFingerprints    map[string]taskFileStamp `json:"file_fingerprints,omitempty"`
	CreatedAt           time.Time                `json:"created_at,omitempty"`
	UpdatedAt           time.Time                `json:"updated_at,omitempty"`
}

type taskResumeContext struct {
	Active                bool
	Kind                  string
	SourceLane            analysisLane
	ApprovedEditTargets   []string
	ReadOnlyEvidencePaths []string
	AllowedDestinations   []string
	ForbiddenPaths        []string
	AttemptedSearches     []string
	OpenGaps              []string
	VerificationPlan      []string
}

type durablePlanArtifact struct {
	Version       int                       `json:"version"`
	Objective     string                    `json:"objective,omitempty"`
	SourceLane    string                    `json:"source_lane,omitempty"`
	Summary       string                    `json:"summary,omitempty"`
	ApprovalScope durablePlanApprovalScope  `json:"approval_scope"`
	PlanExit      string                    `json:"plan_exit,omitempty"`
}

type durablePlanApprovalScope struct {
	EditableTargets     []string `json:"editable_targets,omitempty"`
	ReadOnlyEvidence    []string `json:"read_only_evidence,omitempty"`
	AllowedDestinations []string `json:"allowed_destinations,omitempty"`
	ForbiddenPaths      []string `json:"forbidden_paths,omitempty"`
	Verification        []string `json:"verification,omitempty"`
}

type analysisReplanRecommendationState struct {
	FromLane analysisLane
	ToLane   analysisLane
	Reason   string
	Summary  string
}

func (m *Model) canResumeStructuralRefactor() bool {
	return structuralRefactorStageResumable(m.currentStructuralRefactorStage())
}

type persistedTaskMemory struct {
	Version       int                   `json:"version"`
	WorkspaceRoot string                `json:"workspace_root,omitempty"`
	UpdatedAt     time.Time             `json:"updated_at"`
	Memory        longHorizonTaskMemory `json:"memory"`
}

const taskMemoryStateVersion = 1

func (m *Model) taskMemoryStatePath() string {
	if m == nil || strings.TrimSpace(m.cfg.Chat.SaveDir) == "" {
		return ""
	}
	return filepath.Join(m.cfg.Chat.SaveDir, "state", "task-memory.json")
}

func cloneTaskMemory(memory *longHorizonTaskMemory) *longHorizonTaskMemory {
	if memory == nil {
		return nil
	}
	cloned := *memory
	cloned.OpenGaps = append([]string(nil), memory.OpenGaps...)
	cloned.AttemptedSearches = append([]string(nil), memory.AttemptedSearches...)
	cloned.ChosenTargets = append([]string(nil), memory.ChosenTargets...)
	cloned.VerificationHistory = append([]string(nil), memory.VerificationHistory...)
	cloned.NextActions = append([]string(nil), memory.NextActions...)
	if len(memory.FileFingerprints) > 0 {
		cloned.FileFingerprints = make(map[string]taskFileStamp, len(memory.FileFingerprints))
		for path, stamp := range memory.FileFingerprints {
			cloned.FileFingerprints[path] = stamp
		}
	}
	return &cloned
}

func (m *Model) logTaskMemoryState(action string, memory *longHorizonTaskMemory, reason string) {
	if m == nil {
		return
	}
	action = strings.TrimSpace(action)
	if action == "" {
		return
	}
	payload := struct {
		Action              string   `json:"action"`
		MemoryKind          string   `json:"memory_kind,omitempty"`
		SourceLane          string   `json:"source_lane,omitempty"`
		OpenGaps            []string `json:"open_gaps,omitempty"`
		AttemptedSearches   []string `json:"attempted_searches,omitempty"`
		ChosenTargets       []string `json:"chosen_targets,omitempty"`
		VerificationHistory []string `json:"verification_history,omitempty"`
		NextActions         []string `json:"next_actions,omitempty"`
		Resumable           bool     `json:"resumable"`
		Reason              string   `json:"reason,omitempty"`
	}{
		Action:    action,
		Resumable: taskMemoryResumable(memory),
		Reason:    strings.TrimSpace(reason),
	}
	if memory != nil {
		payload.MemoryKind = strings.TrimSpace(memory.Kind)
		payload.SourceLane = strings.TrimSpace(memory.Lane)
		payload.OpenGaps = uniqueNonEmptyStrings(memory.OpenGaps)
		payload.AttemptedSearches = uniqueNonEmptyStrings(memory.AttemptedSearches)
		payload.ChosenTargets = uniqueNonEmptyStrings(memory.ChosenTargets)
		payload.VerificationHistory = uniqueNonEmptyStrings(memory.VerificationHistory)
		payload.NextActions = uniqueNonEmptyStrings(memory.NextActions)
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	m.logMessage("long_horizon_state", string(data))
}

func taskMemoryResumable(memory *longHorizonTaskMemory) bool {
	if memory == nil || !memory.Active {
		return false
	}
	return len(memory.NextActions) > 0 || len(memory.OpenGaps) > 0 || len(memory.ChosenTargets) > 0 || len(memory.VerificationHistory) > 0
}

func (m *Model) loadPersistedTaskMemory() {
	path := m.taskMemoryStatePath()
	if path == "" {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var state persistedTaskMemory
	if err := json.Unmarshal(data, &state); err != nil {
		return
	}
	if state.Version != taskMemoryStateVersion {
		return
	}
	if root := m.workspaceRootForState(); root != "" && state.WorkspaceRoot != "" && filepath.Clean(state.WorkspaceRoot) != root {
		return
	}
	memory := cloneTaskMemory(&state.Memory)
	if !taskMemoryResumable(memory) {
		return
	}
	if stale, reason := m.taskMemoryStale(memory); stale {
		m.logTaskMemoryState("clear", memory, reason)
		m.removePersistedTaskMemory()
		return
	}
	m.taskMemory = memory
}

func (m *Model) persistTaskMemory(memory *longHorizonTaskMemory) {
	path := m.taskMemoryStatePath()
	if path == "" {
		return
	}
	if !taskMemoryResumable(memory) {
		m.removePersistedTaskMemory()
		return
	}
	cloned := cloneTaskMemory(memory)
	now := time.Now()
	if cloned.CreatedAt.IsZero() {
		cloned.CreatedAt = now
	}
	cloned.UpdatedAt = now
	cloned.FileFingerprints = m.taskMemoryFileFingerprints(cloned.ChosenTargets)
	state := persistedTaskMemory{
		Version:       taskMemoryStateVersion,
		WorkspaceRoot: m.workspaceRootForState(),
		UpdatedAt:     now,
		Memory:        *cloned,
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err == nil {
		m.taskMemory = cloned
	}
}

func (m *Model) removePersistedTaskMemory() {
	path := m.taskMemoryStatePath()
	if path == "" {
		return
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return
	}
}

func (m *Model) clearPersistedTaskMemory(reason string) {
	if m == nil {
		return
	}
	if m.taskMemory != nil {
		m.logTaskMemoryState("clear", m.taskMemory, reason)
	}
	m.taskMemory = nil
	m.removePersistedTaskMemory()
}

func (m *Model) rememberTaskMemory(memory *longHorizonTaskMemory, reason string) {
	if m == nil || !taskMemoryResumable(memory) {
		return
	}
	m.persistTaskMemory(memory)
	m.logTaskMemoryState("persist", memory, reason)
}

func (m *Model) taskMemoryFileFingerprints(paths []string) map[string]taskFileStamp {
	root := m.workspaceRootForState()
	if root == "" {
		return nil
	}
	out := make(map[string]taskFileStamp)
	for _, path := range uniqueNormalizedPaths(paths) {
		full := filepath.Join(root, filepath.FromSlash(path))
		stat, err := os.Stat(full)
		if err != nil || stat.IsDir() {
			continue
		}
		out[path] = taskFileStamp{Size: stat.Size(), ModTimeUnix: stat.ModTime().UnixNano()}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (m *Model) taskMemoryStale(memory *longHorizonTaskMemory) (bool, string) {
	if memory == nil || len(memory.FileFingerprints) == 0 {
		return false, ""
	}
	root := m.workspaceRootForState()
	if root == "" {
		return false, ""
	}
	for path, expected := range memory.FileFingerprints {
		full := filepath.Join(root, filepath.FromSlash(path))
		stat, err := os.Stat(full)
		if err != nil {
			return true, "tracked task-memory target is no longer readable: " + path
		}
		actual := taskFileStamp{Size: stat.Size(), ModTimeUnix: stat.ModTime().UnixNano()}
		if actual != expected {
			return true, "tracked task-memory target changed outside the saved task state: " + path
		}
	}
	return false, ""
}

func looksLikeResumeStructuralRefactorPrompt(prompt string) bool {
	normalized := strings.ToLower(strings.TrimSpace(prompt))
	if normalized == "" {
		return false
	}
	return containsAny(normalized, "continue", "resume", "next stage", "next refactor") &&
		containsAny(normalized, "refactor", "structural", "architecture", "stage")
}

func looksLikeResumeTaskMemoryPrompt(prompt string) bool {
	normalized := strings.ToLower(strings.TrimSpace(prompt))
	if normalized == "" {
		return false
	}
	return containsAny(normalized, "continue", "resume", "follow up", "next step", "pick up") &&
		containsAny(normalized, "task", "investigation", "search", "repair", "evidence", "debug", "issue", "failure")
}

func shouldResumeTaskMemoryPrompt(prompt string, memory *longHorizonTaskMemory) bool {
	if !taskMemoryResumable(memory) {
		return false
	}
	if durablePlanApprovalDetected(prompt, memory) {
		return true
	}
	return looksLikeResumeTaskMemoryPrompt(prompt)
}

func durablePlanApprovalDetected(prompt string, memory *longHorizonTaskMemory) bool {
	if strings.TrimSpace(memoryKind(memory)) != "durable_plan" {
		return false
	}
	return looksLikeDurablePlanApprovalPrompt(prompt) || looksLikeTerseDurablePlanApprovalPrompt(prompt)
}

func memoryKind(memory *longHorizonTaskMemory) string {
	if memory == nil {
		return ""
	}
	return strings.TrimSpace(memory.Kind)
}

func looksLikeDurablePlanApprovalPrompt(prompt string) bool {
	normalized := strings.ToLower(strings.TrimSpace(prompt))
	if normalized == "" {
		return false
	}
	return containsAny(normalized,
		"implement this plan",
		"implement the plan",
		"approve this plan",
		"approved, implement",
		"build this plan",
		"apply this plan",
		"apply the plan",
		"proceed with this plan",
		"go ahead with the plan",
	)
}

func looksLikeTerseDurablePlanApprovalPrompt(prompt string) bool {
	normalized := strings.ToLower(strings.TrimSpace(prompt))
	normalized = strings.ReplaceAll(normalized, "’", "'")
	normalized = strings.Trim(normalized, " \t\r\n.!?")
	if normalized == "" {
		return false
	}
	switch normalized {
	case "do it",
		"please do it",
		"go ahead",
		"please go ahead",
		"proceed",
		"please proceed",
		"start",
		"please start",
		"begin",
		"please begin",
		"yes",
		"yes please",
		"approved",
		"looks good",
		"ship it":
		return true
	default:
		return containsAny(normalized,
			"let's do it",
			"lets do it",
			"let us do it",
			"please begin",
			"please start",
			"please proceed",
			"please go ahead",
			"go ahead",
			"ship it",
		) && !containsAny(normalized, "don't", "do not", "not yet", "hold off", "wait")
	}
}

func (m *Model) resumeStructuralRefactorAsync(userPrompt string) {
	if m.streaming {
		m.status = "Wait for the current response to finish before resuming the refactor."
		return
	}
	stage := m.currentStructuralRefactorStage()
	if stage == nil {
		m.status = "No resumable structural refactor stage is available."
		return
	}
	if len(stage.Remaining) == 0 {
		m.status = "The last structural refactor stage has no remaining target files to resume."
		return
	}
	if !structuralRefactorStageResumable(stage) {
		m.status = "The last structural refactor stage must be repaired or rolled back before resuming."
		return
	}

	prompt := buildStructuralRefactorResumePrompt(stage, userPrompt)
	analysisSummary := buildStructuralRefactorResumeAnalysisSummary(stage)
	plan := promptExecutionPlan{
		lane:         analysisLaneImplement,
		thoroughness: workspace.ThoroughnessMedium,
		reason:       promptRouteReasonDirectImplementation,
	}
	plan = finalizeTaskMemoryExecutionPlan(userPrompt, prompt, plan)
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
	m.logLongHorizonState("resume", stage, "resuming next bounded structural refactor stage")
	m.streaming = true
	m.workingFrame = 0
	m.clearRuntimeError()
	m.setPhaseCard(analysisLaneImplement, "targeting", phaseStateActive, "Resuming the next bounded structural refactor stage.")
	m.status = "Resuming structural refactor stage."
	m.renderMessages(true)

	history := m.requestMessages()
	history = appendAnalysisSummary(history, "architecture", analysisSummary)
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

func (m *Model) currentTaskMemory() *longHorizonTaskMemory {
	if m == nil {
		return nil
	}
	if stale, reason := m.taskMemoryStale(m.taskMemory); stale {
		m.clearPersistedTaskMemory(reason)
		return nil
	}
	return m.taskMemory
}

func (m *Model) resumeTaskMemoryAsync(userPrompt string) {
	if m.streaming {
		m.status = "Wait for the current response to finish before resuming the saved task."
		return
	}
	memory := m.currentTaskMemory()
	if !taskMemoryResumable(memory) {
		m.status = "No resumable investigation or repair task is available."
		return
	}
	prompt := buildTaskMemoryResumePrompt(memory, userPrompt)
	lane := laneForTaskMemoryWithPrompt(memory, userPrompt)
	plan := promptExecutionPlan{
		lane:         lane,
		thoroughness: workspace.ThoroughnessMedium,
		reason:       routeReasonForLane(lane),
		contract:     answerContractForLane(lane),
	}
	plan = finalizeTaskMemoryExecutionPlan(userPrompt, prompt, plan)
	resumeContext := buildTaskResumeContext(memory, userPrompt)
	if m.workspace != nil {
		m.workspace.PrimeDiscoveredPaths(uniqueLimitedStrings(append(append([]string(nil), resumeContext.ApprovedEditTargets...), resumeContext.ReadOnlyEvidencePaths...), 12))
	}
	m.resetCompactTrace()
	m.resetExecutionPanel(prompt, plan)

	displayPrompt := taskMemoryResumeDisplayPrompt(userPrompt)
	m.messages = append(m.messages, llama.ChatMessage{Role: "user", Content: displayPrompt})
	m.messages = append(m.messages, llama.ChatMessage{Role: "assistant", Content: ""})
	m.logMessage("user", displayPrompt)
	m.logMessage("task_memory_resume_prompt", prompt)
	if summary := formatPromptExecutionPlan(plan); summary != "" {
		m.logMessage("route_plan", summary)
	}
	m.logTaskMemoryState("resume", memory, "resuming saved investigation or repair task")
	m.streaming = true
	m.workingFrame = 0
	m.clearRuntimeError()
	m.setPhaseCard(plan.lane, "resume", phaseStateActive, "Resuming saved task memory without repeating completed broad search.")
	m.status = "Resuming saved task memory."
	m.renderMessages(true)

	history := m.requestMessages()
	history = append(history,
		llama.ChatMessage{Role: "system", Content: "Internal task-memory resume packet:\n" + prompt},
		llama.ChatMessage{Role: "system", Content: renderTaskMemoryForModel(memory)},
	)
	opts := m.chatOptions()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(m.cfg.Chat.RequestTimeoutSeconds)*time.Second)
		defer cancel()

		reply, err := m.runPromptPlan(ctx, prompt, history, opts, plan, resumeContext)
		if err == nil {
			m.emitAssistantReply(reply)
		}
		m.events <- streamDoneMsg{err: err}
	}()
}

func taskMemoryResumeDisplayPrompt(userPrompt string) string {
	userPrompt = strings.TrimSpace(userPrompt)
	if userPrompt == "" {
		return "/resume task"
	}
	return userPrompt
}

func buildTaskResumeContext(memory *longHorizonTaskMemory, userPrompt string) taskResumeContext {
	if memory == nil {
		return taskResumeContext{}
	}
	approved := taskMemoryTargetsForResume(memory, userPrompt)
	evidence := taskMemoryEvidencePaths(memory, approved)
	return taskResumeContext{
		Active:                true,
		Kind:                  strings.TrimSpace(memory.Kind),
		SourceLane:            analysisLane(strings.TrimSpace(memory.Lane)),
		ApprovedEditTargets:   uniqueLimitedStrings(approved, 8),
		ReadOnlyEvidencePaths: uniqueLimitedStrings(evidence, 16),
		AllowedDestinations:   durablePlanAllowedDestinations(memory.Summary),
		AttemptedSearches:     append([]string(nil), memory.AttemptedSearches...),
		OpenGaps:              append([]string(nil), memory.OpenGaps...),
		VerificationPlan:      taskMemoryVerificationPlan(memory),
	}
}

func taskMemoryEvidencePaths(memory *longHorizonTaskMemory, approved []string) []string {
	if memory == nil {
		return nil
	}
	approvedSet := make(map[string]struct{}, len(approved))
	for _, path := range approved {
		path = normalizeGuardedPath(path)
		if path != "" {
			approvedSet[path] = struct{}{}
		}
	}
	paths := make([]string, 0, len(memory.AttemptedSearches)+len(memory.ChosenTargets))
	add := func(path string) {
		path = normalizeGuardedPath(path)
		if path == "" {
			return
		}
		if _, ok := approvedSet[path]; ok {
			return
		}
		paths = append(paths, path)
	}
	for _, attempt := range memory.AttemptedSearches {
		for _, path := range promptMentionedWorkspacePaths(attempt) {
			add(path)
		}
	}
	for _, path := range memory.ChosenTargets {
		add(path)
	}
	return uniqueLimitedStrings(paths, 16)
}

func taskMemoryVerificationPlan(memory *longHorizonTaskMemory) []string {
	if memory == nil {
		return nil
	}
	plan := make([]string, 0, len(memory.VerificationHistory)+len(memory.NextActions))
	for _, entry := range memory.VerificationHistory {
		if strings.TrimSpace(entry) != "" {
			plan = append(plan, entry)
		}
	}
	for _, action := range memory.NextActions {
		if containsAny(strings.ToLower(action), "verify", "test", "check") {
			plan = append(plan, action)
		}
	}
	return uniqueLimitedStrings(plan, 6)
}

func laneForTaskMemory(memory *longHorizonTaskMemory) analysisLane {
	return laneForTaskMemoryWithPrompt(memory, "")
}

func finalizeTaskMemoryExecutionPlan(userPrompt string, resumePrompt string, plan promptExecutionPlan) promptExecutionPlan {
	profilePrompt := firstNonEmptyString(userPrompt, resumePrompt)
	profile := buildPromptRouteProfile(profilePrompt)
	plan.intent = buildPromptIntentDecision(profilePrompt, profile, "", plan)
	if plan.lane == analysisLaneImplement && !plan.intent.WantsPlanOnly && !plan.intent.WantsVerify {
		plan.intent.WantsEdits = true
	}
	return finalizePromptExecutionPlan(profilePrompt, profile, plan)
}

func laneForTaskMemoryWithPrompt(memory *longHorizonTaskMemory, userPrompt string) analysisLane {
	if memory == nil {
		return analysisLaneExplore
	}
	if durablePlanApprovalDetected(userPrompt, memory) {
		return analysisLaneImplement
	}
	switch analysisLane(strings.TrimSpace(memory.Lane)) {
	case analysisLaneLocate, analysisLaneExplore, analysisLaneReview, analysisLaneArchitecture, analysisLaneImplement:
		return analysisLane(strings.TrimSpace(memory.Lane))
	default:
		return analysisLaneExplore
	}
}

func buildTaskMemoryResumePrompt(memory *longHorizonTaskMemory, userPrompt string) string {
	if memory == nil {
		return ""
	}
	originalPrompt := canonicalTaskMemoryPrompt(memory.Prompt)
	lines := []string{
		"Resume the saved long-horizon task from persisted memory.",
	}
	if strings.TrimSpace(userPrompt) != "" {
		lines = append(lines, "User resume request: "+strings.TrimSpace(userPrompt))
	}
	if strings.TrimSpace(originalPrompt) != "" {
		lines = append(lines, "Original request: "+strings.TrimSpace(originalPrompt))
	}
	if strings.TrimSpace(memory.Lane) != "" {
		lines = append(lines, "Prior lane: "+strings.TrimSpace(memory.Lane))
	}
	if strings.TrimSpace(memory.Kind) == "durable_plan" {
		if durablePlanApprovalDetected(userPrompt, memory) {
			lines = append(lines,
				"Plan approval: explicit approval detected; switch to build mode.",
				"Build-switch reminder: restate objective, constraints, target files, and verification checks before the first write.",
			)
		} else {
			lines = append(lines,
				"Plan approval: not detected; remain in read-only plan validation mode.",
			)
		}
	}
	targets := taskMemoryTargetsForResume(memory, userPrompt)
	if len(targets) > 0 {
		lines = append(lines, "Previously validated targets: "+strings.Join(targets, ", "))
	}
	if allowed := durablePlanAllowedDestinations(memory.Summary); len(allowed) > 0 {
		lines = append(lines, "Plan-approved new destinations: "+strings.Join(allowed, ", "))
	}
	if len(memory.AttemptedSearches) > 0 {
		lines = append(lines, "Already attempted: "+strings.Join(memory.AttemptedSearches, " | "))
	}
	if len(memory.OpenGaps) > 0 {
		lines = append(lines, "Open evidence gaps: "+strings.Join(memory.OpenGaps, " | "))
	}
	if len(memory.VerificationHistory) > 0 {
		lines = append(lines, "Verification history: "+strings.Join(memory.VerificationHistory, " | "))
	}
	if len(memory.NextActions) > 0 {
		lines = append(lines, "Next actions: "+strings.Join(memory.NextActions, " | "))
	}
	lines = append(lines,
		"Rules:",
		"- Do not repeat broad search that is already recorded above.",
		"- Start with the next action or the smallest unresolved evidence gap.",
		"- Revalidate any cited target before relying on it.",
		"- Do not edit files unless the resumed lane is implementation or legally transitions to implementation.",
	)
	return strings.Join(lines, "\n")
}

func renderTaskMemoryForModel(memory *longHorizonTaskMemory) string {
	if memory == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("Persisted task memory:\n")
	if strings.TrimSpace(memory.Kind) != "" {
		b.WriteString("- kind: " + strings.TrimSpace(memory.Kind) + "\n")
	}
	if strings.TrimSpace(memory.Summary) != "" {
		b.WriteString("- prior summary: " + truncateText(memory.Summary, 800) + "\n")
	}
	if len(memory.ChosenTargets) > 0 {
		b.WriteString("- chosen targets: " + strings.Join(memory.ChosenTargets, ", ") + "\n")
	}
	if len(memory.OpenGaps) > 0 {
		b.WriteString("- open gaps: " + strings.Join(memory.OpenGaps, " | ") + "\n")
	}
	if len(memory.NextActions) > 0 {
		b.WriteString("- next actions: " + strings.Join(memory.NextActions, " | ") + "\n")
	}
	return strings.TrimSpace(b.String())
}

func buildAnalysisTaskMemory(prompt string, lane analysisLane, summary string, evidencePaths []string, nextAction string) *longHorizonTaskMemory {
	prompt = canonicalTaskMemoryPrompt(prompt)
	targets := uniqueLimitedStrings(append(promptMentionedWorkspacePaths(summary), evidencePaths...), 16)
	attempts := taskMemoryAttemptsFromEvidence(targets)
	gaps := inferTaskMemoryOpenGaps(summary, lane)
	nextActions := uniqueNonEmptyStrings([]string{nextAction})
	if len(nextActions) == 0 {
		if len(gaps) > 0 {
			nextActions = append(nextActions, "resume with targeted validation of the unresolved evidence gaps")
		} else if len(targets) > 0 {
			nextActions = append(nextActions, "resume from the validated target set before widening search")
		}
	}
	return &longHorizonTaskMemory{
		Active:            true,
		Kind:              "investigation",
		Prompt:            strings.TrimSpace(prompt),
		Lane:              string(lane),
		Summary:           strings.TrimSpace(summary),
		OpenGaps:          gaps,
		AttemptedSearches: attempts,
		ChosenTargets:     targets,
		NextActions:       nextActions,
		CreatedAt:         time.Now(),
	}
}

func decorateDurablePlanArtifactMemory(memory *longHorizonTaskMemory, prompt string, lane analysisLane, summary string, evidencePaths []string, nextAction string) *longHorizonTaskMemory {
	if memory == nil {
		return nil
	}
	prompt = canonicalTaskMemoryPrompt(prompt)
	memory.Prompt = prompt
	memory.Kind = "durable_plan"
	memory.Summary = renderDurablePlanArtifact(prompt, lane, summary, evidencePaths, nextAction)
	if primaryTargets := durablePlanPrimaryTargets(prompt, memory.Summary, memory.ChosenTargets); len(primaryTargets) > 0 {
		memory.ChosenTargets = primaryTargets
	}
	memory.OpenGaps = uniqueNonEmptyStrings(append(memory.OpenGaps, "plan requires explicit user approval before build-mode execution"))
	memory.NextActions = uniqueNonEmptyStrings(append([]string{
		"wait for explicit approval or a clear plan-exit instruction before implementation",
		"on approval, restate objective, constraints, and verification checks before writing",
	}, memory.NextActions...))
	return memory
}

func canonicalTaskMemoryPrompt(prompt string) string {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return ""
	}
	if !strings.HasPrefix(prompt, "Resume the saved long-horizon task from persisted memory.") {
		return prompt
	}
	candidates := make([]string, 0, 2)
	for _, line := range strings.Split(prompt, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "Original request:") {
			candidate := strings.TrimSpace(strings.TrimPrefix(trimmed, "Original request:"))
			if candidate != "" {
				candidates = append(candidates, candidate)
			}
		}
	}
	for i := len(candidates) - 1; i >= 0; i-- {
		candidate := canonicalTaskMemoryPrompt(candidates[i])
		if candidate != "" && !strings.HasPrefix(candidate, "Resume the saved long-horizon task from persisted memory.") {
			return candidate
		}
	}
	if len(candidates) > 0 {
		return canonicalTaskMemoryPrompt(candidates[len(candidates)-1])
	}
	return prompt
}

func taskMemoryTargetsForResume(memory *longHorizonTaskMemory, userPrompt string) []string {
	if memory == nil {
		return nil
	}
	if durablePlanApprovalDetected(userPrompt, memory) {
		if primaryTargets := durablePlanPrimaryTargets(memory.Prompt, memory.Summary, memory.ChosenTargets); len(primaryTargets) > 0 {
			return primaryTargets
		}
	}
	return append([]string(nil), memory.ChosenTargets...)
}

func durablePlanPrimaryTargets(prompt string, summary string, fallbackTargets []string) []string {
	if targets := explicitDurablePlanTargets(summary); len(targets) > 0 {
		return uniqueLimitedStrings(targets, 4)
	}
	if targets := promptMentionedWorkspacePaths(prompt); len(targets) > 0 {
		return uniqueLimitedStrings(targets, 4)
	}
	if len(fallbackTargets) == 1 {
		return uniqueLimitedStrings(fallbackTargets, 1)
	}
	return nil
}

func explicitDurablePlanTargets(summary string) []string {
	if strings.TrimSpace(summary) == "" {
		return nil
	}
	targets := make([]string, 0, 2)
	for _, line := range strings.Split(summary, "\n") {
		trimmed := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "-"))
		lower := strings.ToLower(trimmed)
		if !strings.HasPrefix(lower, "target:") && !strings.HasPrefix(lower, "target file:") && !strings.HasPrefix(lower, "target files:") {
			continue
		}
		targets = append(targets, promptMentionedWorkspacePaths(trimmed)...)
	}
	return uniqueLimitedStrings(targets, 4)
}

func shouldPersistDurablePlanArtifact(prompt string, lane analysisLane) bool {
	if lane != analysisLaneArchitecture {
		return false
	}
	profile := buildPromptRouteProfile(prompt)
	return profile.wantsPlanOnly
}

func renderDurablePlanArtifact(prompt string, lane analysisLane, summary string, evidencePaths []string, nextAction string) string {
	artifact := buildDurablePlanArtifact(prompt, lane, summary, evidencePaths, nextAction)
	var b strings.Builder
	b.WriteString("Durable plan artifact\n\n")
	b.WriteString("Objective:\n")
	b.WriteString("- ")
	b.WriteString(strings.TrimSpace(prompt))
	b.WriteString("\n\n")
	b.WriteString("Mode:\n")
	b.WriteString("- read-only ")
	if lane != "" {
		b.WriteString(string(lane))
		b.WriteString(" ")
	}
	b.WriteString("planning; no writes until explicit approval.\n\n")
	b.WriteString("Approval scope:\n")
	b.WriteString("- editable targets: ")
	b.WriteString(joinOrNone(artifact.ApprovalScope.EditableTargets))
	b.WriteString("\n")
	b.WriteString("- read-only evidence: ")
	b.WriteString(joinOrNone(artifact.ApprovalScope.ReadOnlyEvidence))
	b.WriteString("\n")
	b.WriteString("- allowed destinations: ")
	b.WriteString(joinOrNone(artifact.ApprovalScope.AllowedDestinations))
	b.WriteString("\n")
	b.WriteString("- forbidden paths: ")
	b.WriteString(joinOrNone(artifact.ApprovalScope.ForbiddenPaths))
	b.WriteString("\n\n")
	b.WriteString("Plan artifact schema:\n")
	if data, err := json.MarshalIndent(artifact, "", "  "); err == nil {
		b.WriteString("```json\n")
		b.Write(data)
		b.WriteString("\n```\n\n")
	}
	b.WriteString("Explore-before-plan evidence:\n")
	if paths := uniqueNormalizedPaths(evidencePaths); len(paths) > 0 {
		for _, path := range paths {
			b.WriteString("- ")
			b.WriteString(path)
			b.WriteString("\n")
		}
	} else {
		b.WriteString("- no concrete evidence paths were captured; validate targets before build mode.\n")
	}
	b.WriteString("\nProposed plan:\n")
	if strings.TrimSpace(summary) != "" {
		b.WriteString(strings.TrimSpace(summary))
		b.WriteString("\n")
	} else {
		b.WriteString("- no model plan summary was captured.\n")
	}
	b.WriteString("\nVerification section:\n")
	b.WriteString("- identify focused tests or checks before implementation.\n")
	b.WriteString("- after implementation, report pass/fail status and exact failure output if checks fail.\n")
	b.WriteString("- do not mark the plan complete until changed files and verification evidence are named.\n\n")
	b.WriteString("Plan exit / build handoff:\n")
	b.WriteString("- wait for explicit user approval, such as \"implement this plan\", before writing.\n")
	b.WriteString("- build-switch reminder: restate the objective, constraints, target files, and verification checks before the first write.\n")
	if strings.TrimSpace(nextAction) != "" {
		b.WriteString("- next action after approval: ")
		b.WriteString(strings.TrimSpace(nextAction))
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

func buildDurablePlanArtifact(prompt string, lane analysisLane, summary string, evidencePaths []string, nextAction string) durablePlanArtifact {
	editable := durablePlanPrimaryTargets(prompt, summary, nil)
	evidence := durablePlanReadOnlyEvidence(evidencePaths, editable)
	destinations := durablePlanCandidateDestinations(summary, editable, evidence)
	verification := []string{
		"identify focused tests or checks before implementation",
		"run verification after implementation and report pass/fail status",
	}
	if strings.TrimSpace(nextAction) != "" && containsAny(strings.ToLower(nextAction), "verify", "test", "check") {
		verification = append(verification, strings.TrimSpace(nextAction))
	}
	return durablePlanArtifact{
		Version:    1,
		Objective:  strings.TrimSpace(prompt),
		SourceLane: string(lane),
		Summary:    strings.TrimSpace(summary),
		ApprovalScope: durablePlanApprovalScope{
			EditableTargets:     uniqueLimitedStrings(editable, 8),
			ReadOnlyEvidence:    uniqueLimitedStrings(evidence, 16),
			AllowedDestinations: uniqueLimitedStrings(destinations, 8),
			ForbiddenPaths:      nil,
			Verification:        uniqueNonEmptyStrings(verification),
		},
		PlanExit: durablePlanExitInstruction(nextAction),
	}
}

func durablePlanReadOnlyEvidence(evidencePaths []string, editable []string) []string {
	editableSet := make(map[string]struct{}, len(editable))
	for _, path := range editable {
		if normalized := normalizeGuardedPath(path); normalized != "" {
			editableSet[normalized] = struct{}{}
		}
	}
	var evidence []string
	for _, path := range evidencePaths {
		path = normalizeGuardedPath(path)
		if path == "" {
			continue
		}
		if _, ok := editableSet[path]; ok {
			continue
		}
		evidence = append(evidence, path)
	}
	return uniqueLimitedStrings(evidence, 16)
}

func durablePlanCandidateDestinations(summary string, editable []string, evidence []string) []string {
	paths := promptMentionedWorkspacePaths(summary)
	blocked := make(map[string]struct{}, len(editable)+len(evidence))
	for _, path := range append(append([]string(nil), editable...), evidence...) {
		if normalized := normalizeGuardedPath(path); normalized != "" {
			blocked[normalized] = struct{}{}
		}
	}
	destinations := make([]string, 0, len(paths))
	for _, path := range paths {
		path = normalizeGuardedPath(path)
		if path == "" {
			continue
		}
		if _, ok := blocked[path]; ok {
			continue
		}
		destinations = append(destinations, path)
	}
	return uniqueLimitedStrings(destinations, 8)
}

func durablePlanExitInstruction(nextAction string) string {
	if strings.TrimSpace(nextAction) == "" {
		return "wait for explicit user approval before implementation"
	}
	return "after explicit user approval: " + strings.TrimSpace(nextAction)
}

func durablePlanAllowedDestinations(summary string) []string {
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return nil
	}
	if artifact, ok := parseDurablePlanArtifact(summary); ok {
		return uniqueLimitedStrings(artifact.ApprovalScope.AllowedDestinations, 8)
	}
	for _, line := range strings.Split(summary, "\n") {
		trimmed := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "-"))
		lower := strings.ToLower(trimmed)
		if !strings.HasPrefix(lower, "allowed destinations:") && !strings.HasPrefix(lower, "plan-approved new destinations:") {
			continue
		}
		_, tail, _ := strings.Cut(trimmed, ":")
		return uniqueLimitedStrings(promptMentionedWorkspacePaths(tail), 8)
	}
	return nil
}

func parseDurablePlanArtifact(summary string) (durablePlanArtifact, bool) {
	start := strings.Index(summary, "```json")
	if start < 0 {
		return durablePlanArtifact{}, false
	}
	rest := summary[start+len("```json"):]
	end := strings.Index(rest, "```")
	if end < 0 {
		return durablePlanArtifact{}, false
	}
	var artifact durablePlanArtifact
	if err := json.Unmarshal([]byte(strings.TrimSpace(rest[:end])), &artifact); err != nil {
		return durablePlanArtifact{}, false
	}
	return artifact, true
}

func taskMemoryAttemptsFromEvidence(paths []string) []string {
	paths = uniqueNormalizedPaths(paths)
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		out = append(out, "validated target "+path)
	}
	return out
}

func inferTaskMemoryOpenGaps(summary string, lane analysisLane) []string {
	normalized := strings.ToLower(strings.TrimSpace(summary))
	gaps := make([]string, 0, 3)
	if reason, low := lowConfidenceReadinessReason(summary); low {
		gaps = append(gaps, reason)
	}
	if containsAny(normalized, "not found", "no match", "no matches") {
		gaps = append(gaps, "requested target was not found in the searched evidence")
	}
	if containsAny(normalized, "not enough evidence", "insufficient evidence", "unable to confirm", "cannot confirm") {
		gaps = append(gaps, "claim still needs narrower evidence before it can be treated as confirmed")
	}
	switch lane {
	case analysisLaneReview:
		if containsAny(normalized, "no bounded", "without a bounded", "no actionable finding") {
			gaps = append(gaps, "no bounded actionable finding was confirmed")
		}
	case analysisLaneArchitecture:
		if containsAny(normalized, "needs deeper validation", "deeper validation", "next validation") {
			gaps = append(gaps, "structural recommendation needs deeper validation before implementation")
		}
	}
	return uniqueNonEmptyStrings(gaps)
}

func taskMemoryNextActionForReplan(target analysisLane, reason string) string {
	if target == "" {
		return ""
	}
	return fmt.Sprintf("resume via %s lane: %s", target, strings.TrimSpace(reason))
}

func (m *Model) rememberAnalysisTaskMemory(prompt string, lane analysisLane, summary string, evidencePaths []string, nextAction string) {
	memory := buildAnalysisTaskMemory(prompt, lane, summary, evidencePaths, nextAction)
	reason := "analysis stopped with resumable evidence and next actions"
	if shouldPersistDurablePlanArtifact(prompt, lane) {
		memory = decorateDurablePlanArtifactMemory(memory, prompt, lane, summary, evidencePaths, nextAction)
		reason = "plan-only analysis produced a durable plan artifact"
	}
	m.rememberTaskMemory(memory, reason)
}

func (m *Model) rememberAnalysisReplan(from analysisLane, to analysisLane, reason string, summary string) {
	if m == nil || from == "" || to == "" || strings.TrimSpace(reason) == "" {
		return
	}
	m.lastReplan = &analysisReplanRecommendationState{
		FromLane: from,
		ToLane:   to,
		Reason:   strings.TrimSpace(reason),
		Summary:  strings.TrimSpace(summary),
	}
}

func (m *Model) runRecommendedReplanAsync() {
	if m.streaming {
		m.status = "Wait for the current response to finish before replanning."
		return
	}
	if m.lastReplan == nil || m.lastReplan.ToLane == "" {
		m.status = "No replan recommendation is available."
		return
	}
	replan := *m.lastReplan
	prompt := m.lastUserPrompt()
	if strings.TrimSpace(prompt) == "" {
		m.status = "No prior user prompt is available for replanning."
		return
	}
	plan := promptExecutionPlan{
		lane:         replan.ToLane,
		thoroughness: workspace.ThoroughnessMedium,
		reason:       routeReasonForLane(replan.ToLane),
		contract:     answerContractForLane(replan.ToLane),
	}
	replanPrompt := buildReplanPrompt(prompt, replan)
	if m.workspace != nil {
		m.workspace.PrimeDiscoveredPaths(promptMentionedWorkspacePaths(replan.Summary))
	}
	m.resetCompactTrace()
	m.resetExecutionPanel(replanPrompt, plan)
	m.messages = append(m.messages, llama.ChatMessage{Role: "user", Content: replanPrompt})
	m.messages = append(m.messages, llama.ChatMessage{Role: "assistant", Content: ""})
	m.logMessage("user", replanPrompt)
	if summary := formatPromptExecutionPlan(plan); summary != "" {
		m.logMessage("route_plan", summary)
	}
	m.streaming = true
	m.workingFrame = 0
	m.clearRuntimeError()
	m.setPhaseCard(replan.ToLane, "replan", phaseStateActive, "Executing the last explicit replan recommendation.")
	m.status = "Running recommended replan through " + string(replan.ToLane) + " lane."
	m.renderMessages(true)

	history := m.requestMessages()
	if strings.TrimSpace(replan.Summary) != "" {
		history = appendAnalysisSummary(history, string(replan.FromLane), replan.Summary)
	}
	opts := m.chatOptions()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(m.cfg.Chat.RequestTimeoutSeconds)*time.Second)
		defer cancel()

		reply, err := m.runPromptPlan(ctx, replanPrompt, history, opts, plan)
		if err == nil {
			m.emitAssistantReply(reply)
		}
		m.events <- streamDoneMsg{err: err}
	}()
}

func buildReplanPrompt(originalPrompt string, replan analysisReplanRecommendationState) string {
	lines := []string{
		"Execute the prior replan recommendation explicitly.",
		"Original request: " + strings.TrimSpace(originalPrompt),
		"Recommended lane: " + string(replan.ToLane),
		"Reason: " + strings.TrimSpace(replan.Reason),
	}
	if strings.TrimSpace(replan.Summary) != "" {
		lines = append(lines, "Prior evidence summary:\n"+strings.TrimSpace(replan.Summary))
	}
	lines = append(lines, "Stay within the recommended lane contract and do not apply edits unless that lane explicitly transitions to implementation.")
	return strings.Join(lines, "\n\n")
}

func (m *Model) resumeApprovedWriteAsync(proposal writeApprovalProposal) {
	note := proposal.approvalNote()
	m.messages = append(m.messages, llama.ChatMessage{Role: "tool", Content: note})
	m.logMessage("tool", note)
	m.messages = append(m.messages, llama.ChatMessage{Role: "assistant", Content: ""})
	m.streaming = true
	m.workingFrame = 0
	m.clearRuntimeError()
	m.setExecutionPlanSummary(
		analysisLaneImplement,
		"Scoped approval granted. Resuming the bounded implementation plan.",
		proposalPanelSteps(&proposal),
	)
	m.setPhaseCard(analysisLaneImplement, "apply", phaseStateActive, "Scoped approval granted. Resuming the bounded implementation plan.")
	m.status = "Approved guarded write scope. Implementation agent is resuming."
	m.renderMessages(true)

	history := m.requestMessages()
	opts := m.chatOptions()
	grant := proposal.grant()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(m.cfg.Chat.RequestTimeoutSeconds)*time.Second)
		defer cancel()

		reply, err := m.runImplementAgentWithGrant(ctx, proposal.Prompt, history, opts, grant)
		if err == nil {
			m.emitAssistantReply(reply)
		}
		m.events <- streamDoneMsg{err: err}
	}()
}

func analysisReplanRecommendation(completedLane analysisLane, summary string) (analysisLane, string, bool) {
	return analysisReplanRecommendationWithEvidence(completedLane, summary, nil)
}

func analysisReplanRecommendationWithEvidence(completedLane analysisLane, summary string, evidencePaths []string) (analysisLane, string, bool) {
	paths := uniqueLimitedStrings(append(promptMentionedWorkspacePaths(summary), evidencePaths...), 12)
	if len(paths) == 0 {
		return "", "", false
	}
	switch completedLane {
	case analysisLaneReview:
		if analysisReplyAcceptedForReplan(summary, paths, analysisLaneArchitecture) && !analysisReplyAcceptedForReplan(summary, paths, analysisLaneReview) {
			return analysisLaneArchitecture, "review produced structural evidence without a bounded finding", true
		}
	case analysisLaneExplore:
		if analysisReplyAcceptedForReplan(summary, paths, analysisLaneArchitecture) {
			return analysisLaneArchitecture, "explore produced architecture-shaped evidence", true
		}
		if analysisReplyAcceptedForReplan(summary, paths, analysisLaneReview) {
			return analysisLaneReview, "explore produced review-shaped evidence", true
		}
	case analysisLaneArchitecture:
		if analysisReplyAcceptedForReplan(summary, paths, analysisLaneReview) && !analysisReplyAcceptedForReplan(summary, paths, analysisLaneArchitecture) {
			return analysisLaneReview, "architecture produced defect-shaped evidence", true
		}
	}
	return "", "", false
}

func analysisReplyAcceptedForReplan(reply string, evidencePaths []string, lane analysisLane) bool {
	if analysisReplyGrounded(reply, evidencePaths) {
		return analysisReplyAccepted(reply, evidencePaths, lane)
	}
	if len(evidencePaths) == 0 {
		return false
	}
	return analysisReplyAccepted(reply, nil, lane)
}

func appendAnalysisReplanRecommendation(summary string, target analysisLane, reason string) string {
	if strings.TrimSpace(summary) == "" || target == "" || strings.TrimSpace(reason) == "" {
		return strings.TrimSpace(summary)
	}
	var b strings.Builder
	b.WriteString(strings.TrimSpace(summary))
	b.WriteString("\n\nReplan recommendation:\n")
	b.WriteString("- next lane: ")
	b.WriteString(string(target))
	b.WriteString("\n")
	b.WriteString("- reason: ")
	b.WriteString(strings.TrimSpace(reason))
	return b.String()
}

func structuralRefactorNextStep(focus string, remaining []string, strategyHints []string, verify implementationVerification) string {
	if verify.failed {
		return "repair or roll back the current structural stage before widening the refactor"
	}
	if len(remaining) > 0 {
		return "review verification, then continue the structural refactor with " + remaining[0]
	}
	if len(strategyHints) > 0 {
		return "review verification, then decide whether the structural hint is complete: " + truncateText(strategyHints[0], 120)
	}
	if strings.TrimSpace(focus) != "" {
		return "review verification, then decide whether another bounded stage is needed after " + focus
	}
	return "review verification, then decide whether another bounded structural stage is needed"
}

func buildStructuralRefactorResumePrompt(stage *structuralRefactorStage, userPrompt string) string {
	if stage == nil {
		return ""
	}
	target := ""
	if len(stage.Remaining) > 0 {
		target = normalizeGuardedPath(stage.Remaining[0])
	}
	nextStage := stage.Stage + 1
	lines := []string{
		fmt.Sprintf("Implement structural refactor stage %d as a bounded follow-up.", nextStage),
	}
	if strings.TrimSpace(userPrompt) != "" {
		lines = append(lines, "User resume request: "+strings.TrimSpace(userPrompt))
	}
	if strings.TrimSpace(stage.FocusPath) != "" {
		lines = append(lines, "Previous stage focused on @"+stage.FocusPath+".")
	}
	if len(stage.Changed) > 0 {
		lines = append(lines, "Previous changed files: "+strings.Join(stage.Changed, ", ")+".")
	}
	if target != "" {
		lines = append(lines, "Next bounded target: @"+target+".")
	}
	if len(stage.Strategy) > 0 {
		lines = append(lines, "Structural strategy: "+strings.Join(stage.Strategy, " | "))
	}
	lines = append(lines,
		"Rules:",
		"- Treat this as one bounded resume stage, not the whole refactor.",
		"- Edit the next bounded target first.",
		"- Do not widen beyond the target packet unless verification proves a necessary follow-up.",
	)
	return strings.Join(lines, "\n")
}

func buildStructuralRefactorResumeAnalysisSummary(stage *structuralRefactorStage) string {
	if stage == nil {
		return ""
	}
	nextStage := stage.Stage + 1
	target := ""
	if len(stage.Remaining) > 0 {
		target = normalizeGuardedPath(stage.Remaining[0])
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Refactor stage: %d\n", nextStage))
	if target != "" {
		b.WriteString("- ")
		b.WriteString(target)
		b.WriteString(" should receive the next bounded structural refactor stage.\n")
	}
	if len(stage.Changed) > 0 {
		b.WriteString("- Previous stage changed: ")
		b.WriteString(strings.Join(stage.Changed, ", "))
		b.WriteString(".\n")
	}
	if len(stage.Strategy) > 0 {
		b.WriteString("- Suggested direction: ")
		b.WriteString(strings.Join(stage.Strategy, " | "))
		b.WriteString("\n")
	}
	if strings.TrimSpace(stage.NextStep) != "" {
		b.WriteString("- Next step: ")
		b.WriteString(stage.NextStep)
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}
