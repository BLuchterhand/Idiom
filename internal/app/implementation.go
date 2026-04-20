package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"qwen-bubbles/internal/agent"
	"qwen-bubbles/internal/frontier"
	"qwen-bubbles/internal/llama"
	"qwen-bubbles/internal/semantic"
	"qwen-bubbles/internal/workspace"
	"slices"
	"strconv"
	"strings"
	"time"
)

type structuralRefactorStage struct {
	Active       bool
	Stage        int
	SourceLane   string
	FocusPath    string
	Changed      []string
	Remaining    []string
	Strategy     []string
	NextStep     string
	Verification string
}

var implementationShortcutModelTimeout = 12 * time.Second

func (m *Model) currentStructuralRefactorStage() *structuralRefactorStage {
	if m == nil {
		return nil
	}
	if m.lastJournal != nil && m.lastJournal.Refactor != nil {
		return m.lastJournal.Refactor
	}
	return m.refactorStage
}

func structuralRefactorStageResumable(stage *structuralRefactorStage) bool {
	if stage == nil || len(stage.Remaining) == 0 {
		return false
	}
	return !strings.Contains(strings.ToLower(stage.NextStep), "repair or roll back")
}

type persistedStructuralRefactorStage struct {
	Version       int                     `json:"version"`
	WorkspaceRoot string                  `json:"workspace_root,omitempty"`
	UpdatedAt     time.Time               `json:"updated_at"`
	Stage         structuralRefactorStage `json:"stage"`
}

const structuralRefactorStageStateVersion = 1

func (m *Model) structuralRefactorStageStatePath() string {
	if m == nil || strings.TrimSpace(m.cfg.Chat.SaveDir) == "" {
		return ""
	}
	return filepath.Join(m.cfg.Chat.SaveDir, "state", "structural-refactor-stage.json")
}

func cloneStructuralRefactorStage(stage *structuralRefactorStage) *structuralRefactorStage {
	if stage == nil {
		return nil
	}
	cloned := *stage
	cloned.Changed = append([]string(nil), stage.Changed...)
	cloned.Remaining = append([]string(nil), stage.Remaining...)
	cloned.Strategy = append([]string(nil), stage.Strategy...)
	return &cloned
}

func (m *Model) loadPersistedStructuralRefactorStage() {
	path := m.structuralRefactorStageStatePath()
	if path == "" {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var state persistedStructuralRefactorStage
	if err := json.Unmarshal(data, &state); err != nil {
		return
	}
	if state.Version != structuralRefactorStageStateVersion {
		return
	}
	if root := m.workspaceRootForState(); root != "" && state.WorkspaceRoot != "" && filepath.Clean(state.WorkspaceRoot) != root {
		return
	}
	stage := cloneStructuralRefactorStage(&state.Stage)
	if !structuralRefactorStageResumable(stage) {
		return
	}
	m.refactorStage = stage
}

func (m *Model) persistStructuralRefactorStage(stage *structuralRefactorStage) {
	path := m.structuralRefactorStageStatePath()
	if path == "" {
		return
	}
	if !structuralRefactorStageResumable(stage) {
		m.removePersistedStructuralRefactorStage()
		return
	}
	state := persistedStructuralRefactorStage{
		Version:       structuralRefactorStageStateVersion,
		WorkspaceRoot: m.workspaceRootForState(),
		UpdatedAt:     time.Now(),
		Stage:         *cloneStructuralRefactorStage(stage),
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	_ = os.WriteFile(path, append(data, '\n'), 0o644)
}

func (m *Model) removePersistedStructuralRefactorStage() {
	path := m.structuralRefactorStageStatePath()
	if path == "" {
		return
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return
	}
}

func (m *Model) clearPersistedStructuralRefactorStage() {
	if m == nil {
		return
	}
	m.refactorStage = nil
	m.removePersistedStructuralRefactorStage()
}

func buildRepairTaskMemory(prompt string, changedPaths []string, verify implementationVerification, targetPacket implementationTargetPacket) *longHorizonTaskMemory {
	targets := uniqueLimitedStrings(append(uniqueNormalizedPaths(changedPaths), targetPacket.PrimaryPaths...), 16)
	history := verificationHistoryForMemory(verify)
	nextActions := []string{"repair failing verification before widening implementation scope"}
	if len(targets) > 0 {
		nextActions = append(nextActions, "start from changed target "+targets[0])
	}
	gaps := []string{"verification is failing for the latest bounded implementation"}
	gaps = append(gaps, verify.expectedFailures...)
	return &longHorizonTaskMemory{
		Active:              true,
		Kind:                "partial_repair",
		Prompt:              strings.TrimSpace(prompt),
		Lane:                string(analysisLaneImplement),
		Summary:             "Latest bounded implementation needs repair before the task can continue.",
		OpenGaps:            uniqueNonEmptyStrings(gaps),
		AttemptedSearches:   taskMemoryAttemptsFromEvidence(targets),
		ChosenTargets:       targets,
		VerificationHistory: history,
		NextActions:         uniqueNonEmptyStrings(nextActions),
		CreatedAt:           time.Now(),
	}
}

func verificationHistoryForMemory(verify implementationVerification) []string {
	history := make([]string, 0, len(verify.results)+len(verify.commands))
	for i, command := range verify.commands {
		status := "passed"
		if i < len(verify.results) && strings.TrimSpace(verify.results[i].Error) != "" {
			status = "failed: " + truncateText(verify.results[i].Error, 160)
		}
		history = append(history, command+" -> "+status)
	}
	if len(history) == 0 && verify.failed {
		history = append(history, "verification failed")
	}
	return uniqueNonEmptyStrings(history)
}

func buildImplementationTargetPacket(prompt string, history []llama.ChatMessage) implementationTargetPacket {
	return frontier.BuildTargetPacket(prompt, history)
}

func applyTaskResumeContextToImplementationTargetPacket(packet implementationTargetPacket, resume taskResumeContext) implementationTargetPacket {
	return frontier.ApplyResumeContextToTargetPacket(packet, frontier.TargetResumeContext{
		Active:                resume.Active,
		SourceLane:            resume.SourceLane,
		ApprovedEditTargets:   append([]string(nil), resume.ApprovedEditTargets...),
		ReadOnlyEvidencePaths: append([]string(nil), resume.ReadOnlyEvidencePaths...),
		AllowedDestinations:   append([]string(nil), resume.AllowedDestinations...),
		ForbiddenPaths:        append([]string(nil), resume.ForbiddenPaths...),
		VerificationPlan:      append([]string(nil), resume.VerificationPlan...),
	})
}

func implementationResumeContextFromPromptPlan(mgr *workspace.Manager, prompt string, history []llama.ChatMessage, plan promptExecutionPlan, base taskResumeContext) taskResumeContext {
	root := ""
	if mgr != nil {
		root = mgr.Root()
	}
	frontierResume := frontier.BuildTargetResumeContextFromPlan(frontier.TargetResumePlanInput{
		Root:             root,
		Prompt:           prompt,
		History:          history,
		IntentTargetRefs: append([]string(nil), plan.intent.TargetRefs...),
		Base: frontier.TargetResumeContext{
			Active:                base.Active,
			SourceLane:            base.SourceLane,
			ApprovedEditTargets:   append([]string(nil), base.ApprovedEditTargets...),
			ReadOnlyEvidencePaths: append([]string(nil), base.ReadOnlyEvidencePaths...),
			AllowedDestinations:   append([]string(nil), base.AllowedDestinations...),
			ForbiddenPaths:        append([]string(nil), base.ForbiddenPaths...),
			VerificationPlan:      append([]string(nil), base.VerificationPlan...),
		},
	})
	resume := base
	resume.Active = frontierResume.Active
	resume.SourceLane = frontierResume.SourceLane
	resume.ApprovedEditTargets = append([]string(nil), frontierResume.ApprovedEditTargets...)
	resume.ReadOnlyEvidencePaths = append([]string(nil), frontierResume.ReadOnlyEvidencePaths...)
	resume.AllowedDestinations = append([]string(nil), frontierResume.AllowedDestinations...)
	resume.ForbiddenPaths = append([]string(nil), frontierResume.ForbiddenPaths...)
	resume.VerificationPlan = append([]string(nil), frontierResume.VerificationPlan...)
	return resume
}

func resolveExistingImplementationRef(root string, ref string) (string, bool) {
	return frontier.ResolveExistingImplementationRef(root, ref)
}

func implementationRefLooksLikeNewDestination(ref string) bool {
	return frontier.ImplementationRefLooksLikeNewDestination(ref)
}

func recentImplementationPlanWorkspaceRefs(prompt string, history []llama.ChatMessage) []string {
	return frontier.RecentImplementationPlanWorkspaceRefs(prompt, history)
}

func recentImplementationPlanConstraintHints(prompt string, history []llama.ChatMessage) []string {
	return frontier.RecentImplementationPlanConstraintHints(prompt, history)
}

func implementationApprovalShouldUseRecentPlan(prompt string) bool {
	return frontier.ImplementationApprovalShouldUseRecentPlan(prompt)
}

func implementationPlanRelevantSection(prompt string, content string) string {
	return frontier.ImplementationPlanRelevantSection(prompt, content)
}

func implementationPromptPrimaryPaths(prompt string) []string {
	return frontier.PromptPrimaryPaths(prompt)
}

func enrichImplementationTargetPacket(mgr *workspace.Manager, packet implementationTargetPacket) implementationTargetPacket {
	return frontier.EnrichTargetPacket(mgr, packet)
}

func resolveImplementationPrimaryPaths(root string, paths []string) []string {
	return frontier.ResolvePrimaryPaths(root, paths)
}

func workspaceRelativeFileExists(root string, path string) bool {
	return frontier.WorkspaceRelativeFileExists(root, path)
}

func findWorkspaceFilesByBase(root string, base string, limit int) []string {
	return frontier.FindWorkspaceFilesByBase(root, base, limit)
}

func implementationTargetPaths(prompt string, history []llama.ChatMessage) []string {
	seen := make(map[string]struct{})
	packet := buildImplementationTargetPacket(prompt, history)
	paths := make([]string, 0, len(packet.PrimaryPaths))
	add := func(path string) {
		path = normalizeGuardedPath(path)
		if path == "" {
			return
		}
		if _, ok := seen[path]; ok {
			return
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	for _, path := range packet.PrimaryPaths {
		add(path)
	}
	return paths
}

func implementationAnalogyPaths(prompt string) []string {
	return frontier.TargetAnalogyPaths(prompt)
}

func implementationLocationHints(prompt string) []string {
	return frontier.TargetLocationHints(prompt)
}

func implementationSymbolHints(prompt string) []string {
	return frontier.TargetSymbolHints(prompt)
}

func resolveImplementationSymbolAnchors(idx *semantic.GoIndex, packet implementationTargetPacket) []semantic.SymbolMatch {
	return frontier.ResolveSymbolAnchors(idx, packet)
}

func routeContinuesToImplement(plan promptExecutionPlan) bool {
	return frontier.RouteContinuesToImplement(plan.lane, plan.followupLane)
}

func implementationQualifiedSymbol(match semantic.SymbolMatch) string {
	return frontier.QualifiedSymbol(match)
}

func renderImplementationTargetPacket(packet implementationTargetPacket) string {
	return frontier.RenderTargetPacket(packet)
}

func analysisSummaryRefactorStage(summary string) int {
	return frontier.AnalysisSummaryRefactorStage(summary)
}

func (m *Model) runImplementAgent(
	ctx context.Context,
	prompt string,
	history []llama.ChatMessage,
	opts llama.ChatOptions,
	resumeContexts ...taskResumeContext,
) (string, error) {
	return m.runImplementAgentWithGrant(ctx, prompt, history, opts, nil, resumeContexts...)
}

func (m *Model) runImplementAgentWithGrant(
	ctx context.Context,
	prompt string,
	history []llama.ChatMessage,
	opts llama.ChatOptions,
	grant *writeApprovalGrant,
	resumeContexts ...taskResumeContext,
) (string, error) {
	implementWorkspace := m.newWorkspaceManager()
	journalRecorder := newExecutionJournalRecorder(implementWorkspace, prompt)
	m.emitPhaseCard(analysisLaneImplement, "targeting", phaseStateActive, "Resolving anchored files, lines, and symbols before editing.")
	var resumeContext taskResumeContext
	if len(resumeContexts) > 0 {
		resumeContext = resumeContexts[0]
	}
	targetPacket := buildImplementationTargetPacket(prompt, history)
	targetPacket = applyTaskResumeContextToImplementationTargetPacket(targetPacket, resumeContext)
	targetPacket = enrichImplementationTargetPacket(implementWorkspace, targetPacket)
	writeGuard := newImplementWriteGuard(implementWorkspace, prompt)
	writeGuard.grant = grant
	targetPaths := append([]string(nil), targetPacket.PrimaryPaths...)
	implementWorkspace.PrimeDiscoveredPaths(uniqueLimitedStrings(append(append(append([]string(nil), targetPaths...), targetPacket.EvidencePaths...), targetPacket.AnalogyPaths...), 12))
	bootstrap := m.bootstrapImplementationEvidence(ctx, implementWorkspace, prompt, targetPacket, history)
	if len(bootstrap.messages) > 0 {
		history = append(history, bootstrap.messages...)
		for i, result := range bootstrap.results {
			m.logToolResult(agent.ToolResultEvent{Step: -(i + 1), Agent: "implement", Result: result})
			m.events <- toolTraceMsg{text: workspace.FormatToolTrace(result)}
		}
	}
	renderedTargetPacket := renderImplementationTargetPacket(targetPacket)
	if renderedTargetPacket != "" {
		history = append(history, llama.ChatMessage{Role: "system", Content: renderedTargetPacket})
		m.logMessage("implement_targets", renderedTargetPacket)
	}
	var pendingApproval *writeApprovalProposal
	preChangedPaths := make([]string, 0, 2)
	taskPrompt := buildImplementTaskPrompt(prompt)
	finalizeImplementation := func(summary string, changedPaths []string, verify implementationVerification) string {
		verify = applyImplementationGoalChecks(implementWorkspace, prompt, targetPacket, changedPaths, verify)
		refactorStage := buildStructuralRefactorStage(targetPacket, changedPaths, verify)
		reply := composeImplementationSummary(summary, changedPaths, verify, refactorStage)
		reply = m.applyFinalAnswerCritic(reply, changedPaths, verify, refactorStage)
		rails := buildDeterministicControlRails(prompt, promptExecutionPlan{
			lane:     analysisLaneImplement,
			contract: answerContractForLane(analysisLaneImplement),
		})
		reply = m.applyControlRailsFinalAnswer(rails, reply, nil, changedPaths, len(verify.results) > 0)
		m.rememberExecutionJournal(journalRecorder, changedPaths, reply, refactorStage, verify)
		if verify.failed {
			m.rememberTaskMemory(buildRepairTaskMemory(prompt, changedPaths, verify, targetPacket), "implementation verification failed with resumable repair context")
		} else {
			m.clearPersistedTaskMemory("implementation completed successfully")
		}
		return reply
	}
	handleTerminalShortcut := func(shortcut directImplementationShortcut) (string, bool) {
		if shortcut.proposal != nil {
			pendingApproval = shortcut.proposal
			m.events <- writeApprovalProposalMsg{proposal: *shortcut.proposal}
		}
		if shortcut.terminal && pendingApproval != nil {
			return pendingApproval.renderSummary(), true
		}
		return "", false
	}
	guardBlocked := func(req workspace.ToolRequest, blocked workspace.ToolResult) workspace.ToolResult {
		if proposal := buildWriteApprovalProposal(ctx, implementWorkspace, prompt, req, writeGuard, blocked); proposal != nil {
			pendingApproval = proposal
			m.events <- writeApprovalProposalMsg{proposal: *proposal}
			return approvalRequiredToolResult(*proposal)
		}
		return blocked
	}
	if shortcut := m.tryDirectPromptEdit(ctx, implementWorkspace, prompt, len(bootstrap.results)+1, journalRecorder, writeGuard, guardBlocked); shortcut.applied {
		verify := m.runImplementationVerification(ctx, implementWorkspace, shortcut.nextStep, shortcut.changedPaths...)
		if !verify.failed {
			return finalizeImplementation(shortcut.summary, shortcut.changedPaths, verify), nil
		}
		if repair := m.tryDeterministicVerificationRepair(ctx, implementWorkspace, verify, shortcut.nextStep+len(verify.results), journalRecorder, writeGuard, guardBlocked); repair.applied {
			changedPaths := uniqueNonEmptyStrings(append(shortcut.changedPaths, repair.changedPaths...))
			verify = m.runImplementationVerification(ctx, implementWorkspace, repair.nextStep, changedPaths...)
			if !verify.failed {
				return finalizeImplementation(shortcut.summary+" "+repair.summary, changedPaths, verify), nil
			}
			preChangedPaths = uniqueNonEmptyStrings(append(preChangedPaths, changedPaths...))
			history = append(history, llama.ChatMessage{Role: "system", Content: "Direct implementation edits already changed the workspace:\n" + shortcut.summary + "\n" + repair.summary})
		} else {
			preChangedPaths = uniqueNonEmptyStrings(append(preChangedPaths, shortcut.changedPaths...))
			history = append(history, llama.ChatMessage{Role: "system", Content: "A direct implementation shortcut already changed the workspace:\n" + shortcut.summary})
		}
		for _, result := range verify.results {
			history = append(history, llama.ChatMessage{Role: "system", Content: workspace.FormatToolResultForModel(result)})
		}
		taskPrompt = buildImplementRepairPrompt(prompt)
		m.emitPhaseCard(analysisLaneImplement, "repair", phaseStateActive, "Verification failed. Repairing the bounded change before continuing.")
		m.events <- infoMsg{text: "Verification failed after the direct edit. Qwen is repairing the change."}
	} else if summary, ok := handleTerminalShortcut(shortcut); ok {
		return summary, nil
	}
	if len(preChangedPaths) == 0 {
		if shortcut := m.tryDirectConfigFieldUpdate(ctx, implementWorkspace, prompt, targetPacket, len(bootstrap.results)+1, journalRecorder, writeGuard, guardBlocked); shortcut.applied {
			verify := m.runImplementationVerification(ctx, implementWorkspace, shortcut.nextStep, shortcut.changedPaths...)
			if !verify.failed {
				return finalizeImplementation(shortcut.summary, shortcut.changedPaths, verify), nil
			}
			if repair := m.tryDeterministicVerificationRepair(ctx, implementWorkspace, verify, shortcut.nextStep+len(verify.results), journalRecorder, writeGuard, guardBlocked); repair.applied {
				changedPaths := uniqueNonEmptyStrings(append(shortcut.changedPaths, repair.changedPaths...))
				verify = m.runImplementationVerification(ctx, implementWorkspace, repair.nextStep, changedPaths...)
				if !verify.failed {
					return finalizeImplementation(shortcut.summary+" "+repair.summary, changedPaths, verify), nil
				}
				preChangedPaths = uniqueNonEmptyStrings(append(preChangedPaths, changedPaths...))
				history = append(history, llama.ChatMessage{Role: "system", Content: "A direct config field update already changed the workspace:\n" + shortcut.summary + "\n" + repair.summary})
			} else {
				preChangedPaths = uniqueNonEmptyStrings(append(preChangedPaths, shortcut.changedPaths...))
				history = append(history, llama.ChatMessage{Role: "system", Content: "A direct config field update already changed the workspace:\n" + shortcut.summary})
			}
			for _, result := range verify.results {
				history = append(history, llama.ChatMessage{Role: "system", Content: workspace.FormatToolResultForModel(result)})
			}
			taskPrompt = buildImplementRepairPrompt(prompt)
			m.emitPhaseCard(analysisLaneImplement, "repair", phaseStateActive, "Verification failed. Repairing the bounded change before continuing.")
			m.events <- infoMsg{text: "Verification failed after the direct config field update. Qwen is repairing the change."}
		} else if summary, ok := handleTerminalShortcut(shortcut); ok {
			return summary, nil
		}
	}
	if len(preChangedPaths) == 0 {
		if shortcut := m.tryDirectLocateRemoval(ctx, implementWorkspace, prompt, history, len(bootstrap.results)+1, journalRecorder, writeGuard, guardBlocked); shortcut.applied {
			verify := m.runImplementationVerification(ctx, implementWorkspace, shortcut.nextStep, shortcut.changedPaths...)
			if !verify.failed {
				return finalizeImplementation(shortcut.summary, shortcut.changedPaths, verify), nil
			}
			if repair := m.tryDeterministicVerificationRepair(ctx, implementWorkspace, verify, shortcut.nextStep+len(verify.results), journalRecorder, writeGuard, guardBlocked); repair.applied {
				changedPaths := uniqueNonEmptyStrings(append(shortcut.changedPaths, repair.changedPaths...))
				verify = m.runImplementationVerification(ctx, implementWorkspace, repair.nextStep, changedPaths...)
				if !verify.failed {
					return finalizeImplementation(shortcut.summary+" "+repair.summary, changedPaths, verify), nil
				}
				preChangedPaths = uniqueNonEmptyStrings(append(preChangedPaths, changedPaths...))
				history = append(history, llama.ChatMessage{Role: "system", Content: "Direct locate-based edits already changed the workspace:\n" + shortcut.summary + "\n" + repair.summary})
			} else {
				preChangedPaths = uniqueNonEmptyStrings(append(preChangedPaths, shortcut.changedPaths...))
				history = append(history, llama.ChatMessage{Role: "system", Content: "A direct locate-based removal already changed the workspace:\n" + shortcut.summary})
			}
			for _, result := range verify.results {
				history = append(history, llama.ChatMessage{Role: "system", Content: workspace.FormatToolResultForModel(result)})
			}
			taskPrompt = buildImplementRepairPrompt(prompt)
			m.emitPhaseCard(analysisLaneImplement, "repair", phaseStateActive, "Verification failed. Repairing the bounded change before continuing.")
			m.events <- infoMsg{text: "Verification failed after the direct locate-based edit. Qwen is repairing the change."}
		} else if summary, ok := handleTerminalShortcut(shortcut); ok {
			return summary, nil
		}
	}
	if len(preChangedPaths) == 0 {
		if shortcut := m.tryDirectUISpacingEdit(ctx, implementWorkspace, prompt, targetPacket, bootstrap, len(bootstrap.results)+1, journalRecorder, writeGuard, guardBlocked); shortcut.applied {
			verify := m.runImplementationVerification(ctx, implementWorkspace, shortcut.nextStep, shortcut.changedPaths...)
			if !verify.failed {
				return finalizeImplementation(shortcut.summary, shortcut.changedPaths, verify), nil
			}
			if repair := m.tryDeterministicVerificationRepair(ctx, implementWorkspace, verify, shortcut.nextStep+len(verify.results), journalRecorder, writeGuard, guardBlocked); repair.applied {
				changedPaths := uniqueNonEmptyStrings(append(shortcut.changedPaths, repair.changedPaths...))
				verify = m.runImplementationVerification(ctx, implementWorkspace, repair.nextStep, changedPaths...)
				if !verify.failed {
					return finalizeImplementation(shortcut.summary+" "+repair.summary, changedPaths, verify), nil
				}
				preChangedPaths = uniqueNonEmptyStrings(append(preChangedPaths, changedPaths...))
				history = append(history, llama.ChatMessage{Role: "system", Content: "A direct UI spacing edit already changed the workspace:\n" + shortcut.summary + "\n" + repair.summary})
			} else {
				preChangedPaths = uniqueNonEmptyStrings(append(preChangedPaths, shortcut.changedPaths...))
				history = append(history, llama.ChatMessage{Role: "system", Content: "A direct UI spacing edit already changed the workspace:\n" + shortcut.summary})
			}
			for _, result := range verify.results {
				history = append(history, llama.ChatMessage{Role: "system", Content: workspace.FormatToolResultForModel(result)})
			}
			taskPrompt = buildImplementRepairPrompt(prompt)
			m.emitPhaseCard(analysisLaneImplement, "repair", phaseStateActive, "Verification failed. Repairing the bounded change before continuing.")
			m.events <- infoMsg{text: "Verification failed after the direct UI spacing edit. Qwen is repairing the change."}
		} else if summary, ok := handleTerminalShortcut(shortcut); ok {
			return summary, nil
		}
	}
	if len(preChangedPaths) == 0 {
		if shortcut := m.tryFocusedStructuredExecutionPlan(ctx, implementWorkspace, prompt, opts, targetPacket, bootstrap, len(bootstrap.results)+1, journalRecorder, writeGuard, guardBlocked); shortcut.applied {
			verify := m.runImplementationVerification(ctx, implementWorkspace, shortcut.nextStep, shortcut.changedPaths...)
			if !verify.failed {
				return finalizeImplementation(shortcut.summary, shortcut.changedPaths, verify), nil
			}
			if repair := m.tryDeterministicVerificationRepair(ctx, implementWorkspace, verify, shortcut.nextStep+len(verify.results), journalRecorder, writeGuard, guardBlocked); repair.applied {
				changedPaths := uniqueNonEmptyStrings(append(shortcut.changedPaths, repair.changedPaths...))
				verify = m.runImplementationVerification(ctx, implementWorkspace, repair.nextStep, changedPaths...)
				if !verify.failed {
					return finalizeImplementation(shortcut.summary+" "+repair.summary, changedPaths, verify), nil
				}
				preChangedPaths = uniqueNonEmptyStrings(append(preChangedPaths, changedPaths...))
				history = append(history, llama.ChatMessage{Role: "system", Content: "A focused structured edit plan already changed the workspace:\n" + shortcut.summary + "\n" + repair.summary})
			} else {
				preChangedPaths = uniqueNonEmptyStrings(append(preChangedPaths, shortcut.changedPaths...))
				history = append(history, llama.ChatMessage{Role: "system", Content: "A focused structured edit plan already changed the workspace:\n" + shortcut.summary})
			}
			for _, result := range verify.results {
				history = append(history, llama.ChatMessage{Role: "system", Content: workspace.FormatToolResultForModel(result)})
			}
			taskPrompt = buildImplementRepairPrompt(prompt)
			m.emitPhaseCard(analysisLaneImplement, "repair", phaseStateActive, "Verification failed. Repairing the bounded change before continuing.")
			m.events <- infoMsg{text: "Verification failed after the focused structured edit plan. Qwen is repairing the change."}
		} else if summary, ok := handleTerminalShortcut(shortcut); ok {
			return summary, nil
		}
	}
	if len(preChangedPaths) == 0 {
		if shortcut := m.tryFocusedWindowRewrite(ctx, implementWorkspace, prompt, opts, targetPacket, bootstrap, len(bootstrap.results)+1, journalRecorder, writeGuard, guardBlocked); shortcut.applied {
			verify := m.runImplementationVerification(ctx, implementWorkspace, shortcut.nextStep, shortcut.changedPaths...)
			if !verify.failed {
				return finalizeImplementation(shortcut.summary, shortcut.changedPaths, verify), nil
			}
			if repair := m.tryDeterministicVerificationRepair(ctx, implementWorkspace, verify, shortcut.nextStep+len(verify.results), journalRecorder, writeGuard, guardBlocked); repair.applied {
				changedPaths := uniqueNonEmptyStrings(append(shortcut.changedPaths, repair.changedPaths...))
				verify = m.runImplementationVerification(ctx, implementWorkspace, repair.nextStep, changedPaths...)
				if !verify.failed {
					return finalizeImplementation(shortcut.summary+" "+repair.summary, changedPaths, verify), nil
				}
				preChangedPaths = uniqueNonEmptyStrings(append(preChangedPaths, changedPaths...))
				history = append(history, llama.ChatMessage{Role: "system", Content: "A focused window rewrite already changed the workspace:\n" + shortcut.summary + "\n" + repair.summary})
			} else {
				preChangedPaths = uniqueNonEmptyStrings(append(preChangedPaths, shortcut.changedPaths...))
				history = append(history, llama.ChatMessage{Role: "system", Content: "A focused window rewrite already changed the workspace:\n" + shortcut.summary})
			}
			for _, result := range verify.results {
				history = append(history, llama.ChatMessage{Role: "system", Content: workspace.FormatToolResultForModel(result)})
			}
			taskPrompt = buildImplementRepairPrompt(prompt)
			m.emitPhaseCard(analysisLaneImplement, "repair", phaseStateActive, "Verification failed. Repairing the bounded change before continuing.")
			m.events <- infoMsg{text: "Verification failed after the focused rewrite. Qwen is repairing the change."}
		} else if summary, ok := handleTerminalShortcut(shortcut); ok {
			return summary, nil
		}
	}
	if len(preChangedPaths) == 0 {
		if shortcut := m.tryStructuredExecutionPlan(ctx, implementWorkspace, prompt, history, opts, targetPacket, len(bootstrap.results)+1, journalRecorder, writeGuard, guardBlocked); shortcut.applied {
			verify := m.runImplementationVerification(ctx, implementWorkspace, shortcut.nextStep, shortcut.changedPaths...)
			if !verify.failed {
				return finalizeImplementation(shortcut.summary, shortcut.changedPaths, verify), nil
			}
			if repair := m.tryDeterministicVerificationRepair(ctx, implementWorkspace, verify, shortcut.nextStep+len(verify.results), journalRecorder, writeGuard, guardBlocked); repair.applied {
				changedPaths := uniqueNonEmptyStrings(append(shortcut.changedPaths, repair.changedPaths...))
				verify = m.runImplementationVerification(ctx, implementWorkspace, repair.nextStep, changedPaths...)
				if !verify.failed {
					return finalizeImplementation(shortcut.summary+" "+repair.summary, changedPaths, verify), nil
				}
				preChangedPaths = uniqueNonEmptyStrings(append(preChangedPaths, changedPaths...))
				history = append(history, llama.ChatMessage{Role: "system", Content: "A structured execution plan already changed the workspace:\n" + shortcut.summary + "\n" + repair.summary})
			} else {
				preChangedPaths = uniqueNonEmptyStrings(append(preChangedPaths, shortcut.changedPaths...))
				history = append(history, llama.ChatMessage{Role: "system", Content: "A structured execution plan already changed the workspace:\n" + shortcut.summary})
			}
			for _, result := range verify.results {
				history = append(history, llama.ChatMessage{Role: "system", Content: workspace.FormatToolResultForModel(result)})
			}
			taskPrompt = buildImplementRepairPrompt(prompt)
			m.emitPhaseCard(analysisLaneImplement, "repair", phaseStateActive, "Verification failed. Repairing the bounded change before continuing.")
			m.events <- infoMsg{text: "Verification failed after the structured execution plan. Qwen is repairing the change."}
		} else if summary, ok := handleTerminalShortcut(shortcut); ok {
			return summary, nil
		}
	}
	if pendingApproval != nil {
		return pendingApproval.renderSummary(), nil
	}
	history = replaceSystemPrompt(history, buildImplementSystemPrompt(m.promptStack.Text, implementWorkspace.Instructions()))
	history = append(history, llama.ChatMessage{Role: "user", Content: taskPrompt})

	requestByStep := make(map[int]agent.ToolRequestEvent)
	resultByStep := make(map[int]agent.ToolResultEvent)
	targetGuard := newImplementTargetGuard(implementWorkspace, targetPacket, bootstrap.results)
	targetGuard.allowFullTargetRead = promptRequestsWholeFileRefactor(prompt)
	implementPolicy := implementRunPolicyForRequest(m.cfg.Chat.MaxToolSteps, prompt, targetPacket)
	initialVerificationPlan := implementationVerificationPlan(implementWorkspace, uniqueNonEmptyStrings(append(targetPacket.PrimaryPaths, preChangedPaths...))).Commands
	railsPlan := promptExecutionPlan{
		lane:     analysisLaneImplement,
		reason:   promptRouteReasonDirectImplementation,
		contract: answerContractForLane(analysisLaneImplement),
	}
	railsPlan.intent = buildPromptIntentDecision(prompt, buildPromptRouteProfile(prompt), m.recentConversationText(6), railsPlan)
	rails := buildDeterministicControlRails(prompt, railsPlan)
	if implementPolicy.MaxToolSteps <= 0 {
		m.logMessage("implement_tool_budget", fmt.Sprintf("using model-decided completion with no fixed step cap; no-progress fuse=%d", implementPolicy.MaxNoProgressTurns))
	} else if baseBudget := implementToolBudget(m.cfg.Chat.MaxToolSteps); implementPolicy.MaxToolSteps != baseBudget {
		m.logMessage("implement_tool_budget", fmt.Sprintf("expanded implement tool budget from %d to %d for structural request", baseBudget, implementPolicy.MaxToolSteps))
	}
	runner := &agent.Runner{
		Client:         m.client,
		Workspace:      implementWorkspace,
		MaxToolSteps:   implementPolicy.MaxToolSteps,
		Policy:         implementPolicy,
		AgentName:      "implement",
		SeedResults:    append([]workspace.ToolResult(nil), bootstrap.results...),
		CompletionGate: implementationCompletionGate(prompt, preChangedPaths, requestByStep, resultByStep),
		TaskLedger: agent.TaskLedgerPolicy{
			Required: requestNeedsExpandedImplementBudget(prompt, targetPacket),
		},
		PermissionPolicy: controlRailPermissionPolicy(rails),
		RuntimeBrief: buildWorkerRuntimeBrief(
			prompt,
			rails,
			targetPacket,
			renderedTargetPacket,
			initialVerificationPlan,
			implementPolicy,
			targetPacket.StrategyHints,
			[]string{"consume target packet before broad discovery", "apply the smallest correct edit", "verify changed surface before final success"},
			nil,
		),
		OnEvent: func(event agent.RunEvent) {
			m.logMessage(event.Role, event.Content)
		},
		PreExecute: func(step int, req workspace.ToolRequest) *workspace.ToolResult {
			if blocked := controlRailPreExecute(rails, req); blocked != nil {
				return blocked
			}
			if blocked := targetGuard.PreExecute(req); blocked != nil {
				return blocked
			}
			blocked := writeGuard.PreExecute(req)
			if blocked != nil {
				result := guardBlocked(req, *blocked)
				return &result
			}
			if result := captureExecutionJournalResult(journalRecorder, req); result != nil {
				return result
			}
			return nil
		},
	}
	m.events <- infoMsg{text: "Implementation agent is applying the requested change."}
	m.emitPhaseCard(analysisLaneImplement, "apply", phaseStateActive, "Executing the bounded implementation plan against the targeted files.")
	reply, err := runner.Run(
		ctx,
		history,
		opts,
		func(status string) {
			m.events <- infoMsg{text: "Implementation agent: " + status}
		},
		func(event agent.ToolRequestEvent) {
			requestByStep[event.Step] = event
			m.logToolRequest(event)
		},
		func(event agent.ToolResultEvent) {
			resultByStep[event.Step] = event
			m.logToolResult(event)
			if shouldInvalidateExploreCacheForTool(event.Result) {
				m.events <- exploreCacheInvalidatedMsg{
					reason: fmt.Sprintf("workspace changed via %s; clearing cached explore profile", event.Result.Tool),
				}
			}
			m.events <- toolTraceMsg{text: workspace.FormatToolTrace(event.Result)}
		},
	)
	if err != nil {
		return "", err
	}
	if pendingApproval != nil {
		return pendingApproval.renderSummary(), nil
	}

	changedPaths := uniqueNonEmptyStrings(append(preChangedPaths, changedPathsFromImplementEvents(requestByStep, resultByStep)...))
	if len(changedPaths) == 0 {
		if implementationReplyReportsNoChangeBlocker(reply) {
			m.emitPhaseCard(analysisLaneImplement, "complete", phaseStateFailed, "Implementation stopped with a blocker before applying workspace changes.")
		} else {
			m.emitPhaseCard(analysisLaneImplement, "complete", phaseStateDone, "No workspace changes were needed after consuming the target packet.")
		}
		return buildImplementationNoopSummary(reply, resultByStep), nil
	}

	verify := m.runImplementationVerification(ctx, implementWorkspace, len(resultByStep)+1, changedPaths...)
	if verify.failed {
		repair, repairErr := m.runImplementationVerificationRepair(
			ctx,
			implementWorkspace,
			prompt,
			history,
			opts,
			targetPacket,
			renderedTargetPacket,
			verify,
			len(resultByStep)+len(verify.results)+1,
			journalRecorder,
			targetGuard,
			writeGuard,
			guardBlocked,
		)
		if repairErr != nil {
			m.logMessage("implementation_repair", "repair loop failed: "+repairErr.Error())
		} else if repair.Attempted {
			if strings.TrimSpace(repair.Summary) != "" {
				reply = strings.TrimSpace(reply + "\n\nRepair attempt:\n" + repair.Summary)
			}
			if len(repair.ChangedPaths) > 0 {
				changedPaths = uniqueNonEmptyStrings(append(changedPaths, repair.ChangedPaths...))
			}
			verify = repair.Verification
		}
	}
	return finalizeImplementation(reply, changedPaths, verify), nil
}

type implementationRepairAttempt struct {
	Attempted    bool
	Summary      string
	ChangedPaths []string
	Verification implementationVerification
}

func (m *Model) runImplementationVerificationRepair(
	ctx context.Context,
	mgr *workspace.Manager,
	prompt string,
	history []llama.ChatMessage,
	opts llama.ChatOptions,
	targetPacket implementationTargetPacket,
	renderedTargetPacket string,
	failedVerify implementationVerification,
	startStep int,
	journal *executionJournalRecorder,
	targetGuard *implementTargetGuard,
	writeGuard *implementWriteGuard,
	onBlocked func(req workspace.ToolRequest, blocked workspace.ToolResult) workspace.ToolResult,
) (implementationRepairAttempt, error) {
	if mgr == nil || !failedVerify.failed || len(failedVerify.results) == 0 {
		return implementationRepairAttempt{}, nil
	}
	m.emitPhaseCard(analysisLaneImplement, "repair", phaseStateActive, "Verification failed. Feeding the failure back into a bounded repair worker.")
	repairMessages := append([]llama.ChatMessage(nil), history...)
	repairMessages = append(repairMessages, llama.ChatMessage{
		Role: "system",
		Content: strings.TrimSpace(strings.Join([]string{
			"Post-edit verification failed. Repair the smallest surface necessary while preserving the requested behavior change.",
			"Verification commands:",
			"- " + strings.Join(uniqueNonEmptyStrings(failedVerify.commands), "\n- "),
			"Repair policy: " + strings.TrimSpace(failedVerify.repairPolicy),
		}, "\n")),
	})
	for _, result := range failedVerify.results {
		repairMessages = append(repairMessages, llama.ChatMessage{Role: "system", Content: workspace.FormatToolResultForModel(result)})
	}
	repairMessages = append(repairMessages, llama.ChatMessage{Role: "user", Content: buildImplementRepairPrompt(prompt)})

	repairRequests := make(map[int]agent.ToolRequestEvent)
	repairResults := make(map[int]agent.ToolResultEvent)
	railsPlan := promptExecutionPlan{
		lane:     analysisLaneImplement,
		reason:   promptRouteReasonDirectImplementation,
		contract: answerContractForLane(analysisLaneImplement),
	}
	railsPlan.intent = buildPromptIntentDecision(prompt, buildPromptRouteProfile(prompt), m.recentConversationText(6), railsPlan)
	rails := buildDeterministicControlRails(prompt, railsPlan)
	repairPolicy := frontier.ImplementRepairRunPolicyForRequest(m.cfg.Chat.MaxToolSteps, prompt, targetPacket)
	runner := &agent.Runner{
		Client:       m.client,
		Workspace:    mgr,
		MaxToolSteps: repairPolicy.MaxToolSteps,
		Policy:       repairPolicy,
		AgentName:    "implement_repair",
		SeedResults:  append([]workspace.ToolResult(nil), failedVerify.results...),
		CompletionGate: frontier.ImplementationRepairCompletionGate(prompt, func() []string {
			return changedPathsFromImplementEvents(repairRequests, repairResults)
		}),
		TaskLedger: agent.TaskLedgerPolicy{
			Required: requestNeedsExpandedImplementBudget(prompt, targetPacket),
		},
		PermissionPolicy: controlRailPermissionPolicy(rails),
		RuntimeBrief: buildWorkerRuntimeBrief(
			buildImplementRepairPrompt(prompt),
			rails,
			targetPacket,
			renderedTargetPacket,
			failedVerify.commands,
			repairPolicy,
			uniqueNonEmptyStrings(append(targetPacket.StrategyHints, "preserve the requested behavior change", "repair failing verification before widening scope")),
			[]string{"inspect failing verification output", "apply the smallest repair edit", "rerun verification before claiming success"},
			nil,
		),
		OnEvent: func(event agent.RunEvent) {
			m.logMessage(event.Role, event.Content)
		},
		PreExecute: func(step int, req workspace.ToolRequest) *workspace.ToolResult {
			if blocked := controlRailPreExecute(rails, req); blocked != nil {
				return blocked
			}
			if targetGuard != nil {
				if blocked := targetGuard.PreExecute(req); blocked != nil {
					return blocked
				}
			}
			if writeGuard != nil {
				if blocked := writeGuard.PreExecute(req); blocked != nil {
					result := *blocked
					if onBlocked != nil {
						result = onBlocked(req, result)
					}
					return &result
				}
			}
			if result := captureExecutionJournalResult(journal, req); result != nil {
				return result
			}
			return nil
		},
	}
	m.events <- infoMsg{text: "Implementation repair agent is addressing the failing verification output."}
	reply, err := runner.Run(
		ctx,
		repairMessages,
		opts,
		func(status string) {
			m.events <- infoMsg{text: "Implementation repair agent: " + status}
		},
		func(event agent.ToolRequestEvent) {
			repairRequests[event.Step] = event
			m.logToolRequest(event)
		},
		func(event agent.ToolResultEvent) {
			repairResults[event.Step] = event
			m.logToolResult(event)
			if shouldInvalidateExploreCacheForTool(event.Result) {
				m.events <- exploreCacheInvalidatedMsg{
					reason: fmt.Sprintf("workspace changed via %s; clearing cached explore profile", event.Result.Tool),
				}
			}
			m.events <- toolTraceMsg{text: workspace.FormatToolTrace(event.Result)}
		},
	)
	if err != nil {
		return implementationRepairAttempt{}, err
	}
	changedPaths := changedPathsFromImplementEvents(repairRequests, repairResults)
	if len(changedPaths) == 0 {
		m.emitPhaseCard(analysisLaneImplement, "repair", phaseStateFailed, "Repair worker stopped without applying a workspace change.")
		return implementationRepairAttempt{
			Attempted:    true,
			Summary:      reply,
			Verification: failedVerify,
		}, nil
	}
	verify := m.runImplementationVerification(ctx, mgr, startStep+len(repairResults)+1, changedPaths...)
	return implementationRepairAttempt{
		Attempted:    true,
		Summary:      reply,
		ChangedPaths: changedPaths,
		Verification: verify,
	}, nil
}

type directImplementationShortcut struct {
	applied      bool
	changedPaths []string
	summary      string
	nextStep     int
	terminal     bool
	proposal     *writeApprovalProposal
}

type structuredExecutionPlan struct {
	Summary    string                    `json:"summary"`
	Operations []structuredExecutionEdit `json:"operations"`
}

type structuredExecutionEdit struct {
	Tool      string `json:"tool"`
	Path      string `json:"path"`
	StartLine int    `json:"start_line,omitempty"`
	EndLine   int    `json:"end_line,omitempty"`
	Content   string `json:"content,omitempty"`
	OldText   string `json:"old_text,omitempty"`
	NewText   string `json:"new_text,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

type implementationBootstrap struct {
	results  []workspace.ToolResult
	messages []llama.ChatMessage
}

type implementTargetGuard struct {
	inner               *frontier.TargetGuard
	allowFullTargetRead bool
}

func newImplementTargetGuard(mgr *workspace.Manager, packet implementationTargetPacket, bootstrap []workspace.ToolResult) *implementTargetGuard {
	return &implementTargetGuard{
		inner: frontier.NewTargetGuard(mgr, packet, bootstrap),
	}
}

func (g *implementTargetGuard) pathAllowed(path string) bool {
	if g == nil || g.inner == nil {
		return false
	}
	return g.inner.PathAllowed(path)
}

func (g *implementTargetGuard) pathReadableAllowed(path string) bool {
	if g == nil || g.inner == nil {
		return false
	}
	return g.inner.PathReadableAllowed(path)
}

func (g *implementTargetGuard) pathEditableAllowed(path string) bool {
	if g == nil || g.inner == nil {
		return false
	}
	return g.inner.PathEditableAllowed(path)
}

func (g *implementTargetGuard) pathPrimaryAllowed(path string) bool {
	if g == nil || g.inner == nil {
		return false
	}
	return g.inner.PathPrimaryAllowed(path)
}

func (g *implementTargetGuard) pathForbidden(path string) bool {
	if g == nil || g.inner == nil {
		return false
	}
	return g.inner.PathForbidden(path)
}

func (g *implementTargetGuard) blockWriteOutsideEditableTargets(req workspace.ToolRequest) *workspace.ToolResult {
	if g == nil || g.inner == nil {
		return nil
	}
	return g.inner.BlockWriteOutsideEditableTargets(req)
}

func (m *Model) bootstrapImplementationEvidence(ctx context.Context, mgr *workspace.Manager, prompt string, packet implementationTargetPacket, history []llama.ChatMessage) implementationBootstrap {
	if mgr == nil {
		return implementationBootstrap{}
	}
	paths := append([]string(nil), packet.PrimaryPaths...)
	discovered := uniqueLimitedStrings(append(append(append([]string(nil), packet.PrimaryPaths...), packet.EvidencePaths...), packet.AnalogyPaths...), 12)
	if len(discovered) > 0 {
		mgr.PrimeDiscoveredPaths(discovered)
	}

	bootstrap := implementationBootstrap{}
	defer func() {
		if len(bootstrap.results) > 0 {
			m.logMessage("implement_bootstrap", formatImplementationBootstrapDiagnostics(packet, bootstrap))
		}
	}()
	normalized := strings.ToLower(prompt)
	appendResult := func(result workspace.ToolResult) {
		bootstrap.results = append(bootstrap.results, result)
		bootstrap.messages = append(bootstrap.messages, llama.ChatMessage{
			Role:    "system",
			Content: workspace.FormatToolResultForModel(result),
		})
	}
	ensurePrimaryPathDiscovered := func(path string) string {
		path = normalizeGuardedPath(path)
		if path == "" {
			return ""
		}
		dir := filepath.Dir(path)
		if dir == "" {
			dir = "."
		}
		search := mgr.Execute(ctx, workspace.ToolRequest{
			Tool:    "search_files",
			Pattern: filepath.Base(path),
			Path:    dir,
		})
		appendResult(search)
		if search.Error != "" {
			return ""
		}
		files, _ := parseDiscoveredPaths(search.Output)
		return strings.TrimSpace(files[path])
	}
	readDiscoveredTarget := func(path string, startLine int, endLine int) workspace.ToolResult {
		path = normalizeGuardedPath(path)
		handle := ensurePrimaryPathDiscovered(path)
		req := workspace.ToolRequest{
			Tool:      "read_file",
			Path:      path,
			StartLine: startLine,
			EndLine:   endLine,
		}
		if handle != "" {
			req.ID = handle
			req.Path = ""
		}
		result := mgr.Execute(ctx, req)
		if result.Error == "start_line must be <= end_line" && startLine > 1 {
			req.StartLine = 1
			req.EndLine = 260
			result = mgr.Execute(ctx, req)
		}
		appendResult(result)
		return result
	}
	readPrimaryTargetWindow := func() {
		for _, path := range paths {
			if normalizeGuardedPath(path) == "" {
				continue
			}
			if read := readDiscoveredTarget(path, 1, 180); read.Error == "" {
				break
			}
		}
	}
	readPrimaryTargetWindow()
	for _, symbol := range packet.Symbols {
		if normalizeGuardedPath(symbol.Path) == "" {
			continue
		}
		start := symbol.StartLine
		end := symbol.EndLine
		if start <= 0 || end <= 0 || end < start {
			start = maxInt(1, symbol.Line-6)
			end = symbol.Line + 24
		} else {
			start = maxInt(1, start-2)
			end += 4
		}
		read := readDiscoveredTarget(symbol.Path, start, end)
		if read.Error == "" {
			break
		}
	}
	for _, target := range packet.Anchors {
		read := readDiscoveredTarget(target.Path, maxInt(1, target.Line-3), target.Line+6)
		if read.Error == "" {
			break
		}
	}
	if len(packet.LiteralFragments) > 0 {
		oldText := strings.TrimSpace(packet.LiteralFragments[0])
		if oldText != "" {
			for _, path := range paths {
				search := mgr.Execute(ctx, workspace.ToolRequest{
					Tool:    "search_content",
					Pattern: locateLiteralSearchPattern(oldText),
					Path:    path,
				})
				appendResult(search)
				line, ok := firstSearchMatchLine(search.Output, path)
				if search.Error != "" || !ok {
					continue
				}
				readDiscoveredTarget(path, maxInt(1, line-2), line+2)
				break
			}
		}
	}
	if hasUILocationHint(normalized) {
		for _, path := range paths {
			for _, pattern := range uiImplementationBootstrapPatterns(normalized) {
				search := mgr.Execute(ctx, workspace.ToolRequest{
					Tool:    "search_content",
					Pattern: pattern,
					Path:    path,
				})
				appendResult(search)
				line, ok := firstSearchMatchLine(search.Output, path)
				if search.Error != "" || !ok {
					continue
				}
				readDiscoveredTarget(path, maxInt(1, line-1), line+18)
				return bootstrap
			}
		}
	}
	for _, path := range packet.AnalogyPaths {
		read := readDiscoveredTarget(path, 1, 180)
		if read.Error == "" {
			break
		}
	}
	for _, path := range packet.EvidencePaths {
		read := readDiscoveredTarget(path, 1, 180)
		if read.Error == "" {
			break
		}
	}

	for _, target := range analysisSummaryTargets(history) {
		read := readDiscoveredTarget(target.Path, maxInt(1, target.Line-2), target.Line+2)
		if read.Error == "" {
			break
		}
	}
	if len(paths) > 1 {
		seenRead := make(map[string]struct{})
		for _, result := range bootstrap.results {
			if result.Tool != "read_file" || result.Error != "" {
				continue
			}
			path, _, _, ok := parseReadWindowSummary(result.Summary)
			if ok {
				seenRead[path] = struct{}{}
			}
		}
		readCount := 0
		for _, path := range paths {
			if _, ok := seenRead[normalizeGuardedPath(path)]; ok {
				readCount++
				if readCount >= minInt(2, len(paths)) {
					break
				}
				continue
			}
			read := readDiscoveredTarget(path, 1, 180)
			if read.Error == "" {
				readCount++
			}
			if readCount >= minInt(2, len(paths)) {
				break
			}
		}
		if readCount >= minInt(2, len(paths)) {
			return bootstrap
		}
	}
	if len(bootstrap.results) == 0 {
		for _, path := range paths {
			read := readDiscoveredTarget(path, 1, 180)
			if read.Error == "" {
				break
			}
		}
	}
	return bootstrap
}

func formatImplementationBootstrapDiagnostics(packet implementationTargetPacket, bootstrap implementationBootstrap) string {
	lines := []string{"Implementation bootstrap diagnostics."}
	if len(packet.PrimaryPaths) > 0 {
		lines = append(lines, "- primary files: "+strings.Join(packet.PrimaryPaths, ", "))
	}
	if len(packet.EvidencePaths) > 0 {
		lines = append(lines, "- evidence files: "+strings.Join(packet.EvidencePaths, ", "))
	}
	for i, result := range bootstrap.results {
		label := fmt.Sprintf("- result %d: %s | %s", i+1, result.Tool, strings.TrimSpace(result.Summary))
		if path, startLine, endLine, ok := parseReadWindowSummary(result.Summary); ok {
			label = fmt.Sprintf("- result %d: read_file %s:%d-%d", i+1, path, startLine, endLine)
		}
		if strings.TrimSpace(result.Error) != "" {
			label += " | error: " + truncateText(strings.TrimSpace(result.Error), 180)
		}
		if strings.TrimSpace(result.Output) != "" {
			label += fmt.Sprintf(" | output_chars=%d", len(result.Output))
		}
		lines = append(lines, label)
	}
	return strings.Join(lines, "\n")
}

func uiImplementationBootstrapPatterns(normalized string) []string {
	patterns := make([]string, 0, 4)
	add := func(pattern string) {
		for _, existing := range patterns {
			if existing == pattern {
				return
			}
		}
		patterns = append(patterns, pattern)
	}
	if containsAny(normalized, "top", "header", "banner", "window") {
		add("header :=|header \\+=")
	}
	if containsAny(normalized, "footer", "bottom", "underneath", "below") {
		add("footer :=|footer \\+=|return lipgloss\\.JoinVertical")
	}
	if containsAny(normalized, "badge", "chat", "menu") {
		add("case \"assistant\"|assistantStyle\\.Render|content = m\\.spinner\\.View")
	}
	if len(patterns) == 0 {
		add("header :=|footer :=|return lipgloss\\.JoinVertical")
	}
	return patterns
}

type implementationVerification struct {
	commands         []string
	reasons          []string
	results          []workspace.ToolResult
	failed           bool
	coverage         string
	confidence       string
	riskLevel        string
	expectedFailures []string
	repairPolicy     string
}

func applyImplementationGoalChecks(mgr *workspace.Manager, prompt string, packet implementationTargetPacket, changedPaths []string, verify implementationVerification) implementationVerification {
	if mgr == nil || !promptRequestsWholeFileRefactor(prompt) {
		return verify
	}
	source := ""
	for _, path := range packet.PrimaryPaths {
		if strings.HasSuffix(path, ".go") {
			source = path
			break
		}
	}
	if source == "" {
		return verify
	}
	source = normalizeGuardedPath(source)
	sourceDir := filepath.Dir(source)
	changed := make(map[string]struct{}, len(changedPaths))
	for _, path := range changedPaths {
		changed[normalizeGuardedPath(path)] = struct{}{}
	}
	_, sourceChanged := changed[source]
	movedTargets := 0
	for path := range changed {
		if path == source {
			continue
		}
		if filepath.Dir(path) == sourceDir && strings.HasSuffix(path, ".go") {
			movedTargets++
		}
	}
	fullPath := filepath.Join(mgr.Root(), source)
	lineCount := 0
	if data, err := os.ReadFile(fullPath); err == nil {
		lineCount = strings.Count(string(data), "\n")
		if len(data) > 0 && data[len(data)-1] != '\n' {
			lineCount++
		}
	}
	if sourceChanged && movedTargets >= 2 && (lineCount == 0 || lineCount < 5000) {
		return verify
	}

	verify.failed = true
	reason := fmt.Sprintf("whole-file refactor goal check failed for %s: changed source=%t, destination Go files in same package=%d, remaining source lines=%d", source, sourceChanged, movedTargets, lineCount)
	verify.results = append(verify.results, workspace.ToolResult{
		Tool:    "implementation_goal_check",
		Summary: "Whole-file refactor goal check failed",
		Error:   reason,
	})
	return verify
}

func (m *Model) runImplementationVerification(ctx context.Context, mgr *workspace.Manager, startStep int, changedPaths ...string) implementationVerification {
	plan := implementationVerificationPlan(mgr, changedPaths)
	if len(plan.Commands) == 0 {
		return implementationVerification{}
	}
	m.setExecutionVerificationPlan(plan)
	out := implementationVerification{
		commands:         append([]string(nil), plan.Commands...),
		reasons:          append([]string(nil), plan.Reasons...),
		results:          make([]workspace.ToolResult, 0, len(plan.Commands)),
		coverage:         strings.TrimSpace(plan.Coverage),
		confidence:       strings.TrimSpace(plan.Confidence),
		riskLevel:        strings.TrimSpace(plan.RiskLevel),
		expectedFailures: append([]string(nil), plan.ExpectedFailures...),
		repairPolicy:     strings.TrimSpace(plan.RepairPolicy),
	}
	m.emitPhaseCard(analysisLaneImplement, "verify", phaseStateActive, fmt.Sprintf("Running %d verification command(s).", len(plan.Commands)))
	for i, command := range plan.Commands {
		m.emitPhaseCard(analysisLaneImplement, "verify", phaseStateActive, fmt.Sprintf("Running check %d/%d: %s", i+1, len(plan.Commands), command))
		req := workspace.ToolRequest{Tool: "shell", Command: command, Workdir: "."}
		m.logToolRequest(agent.ToolRequestEvent{
			Step:      startStep + i,
			Agent:     "implement_verify",
			Raw:       fmt.Sprintf("{\"tool\":\"shell\",\"command\":%q,\"workdir\":\".\"}", command),
			Request:   req,
			Mode:      workspace.ModeNormal,
			Broad:     false,
			Narrowing: true,
			Action:    "shell_narrow:" + command,
		})
		result := mgr.Execute(ctx, req)
		out.results = append(out.results, result)
		m.logToolResult(agent.ToolResultEvent{Step: startStep + i, Agent: "implement_verify", Result: result})
		m.events <- toolTraceMsg{text: workspace.FormatToolTrace(result)}
		if shouldInvalidateExploreCacheForTool(result) {
			m.events <- exploreCacheInvalidatedMsg{
				reason: fmt.Sprintf("workspace changed via %s; clearing cached explore profile", result.Tool),
			}
		}
		if result.Error != "" {
			out.failed = true
		}
	}
	if out.failed {
		m.emitPhaseCard(analysisLaneImplement, "verify", phaseStateFailed, "Verification failed. Review the failing check output before trusting the edit.")
	} else {
		detail := fmt.Sprintf("Verification passed with %d successful check(s).", len(out.results))
		if out.confidence != "" {
			detail += " Confidence: " + out.confidence
			if out.coverage != "" {
				detail += " (" + out.coverage + ")."
			} else {
				detail += "."
			}
		}
		if plan.UsesFullSuite {
			detail += " Full-suite escalation was required by the computed blast radius."
		}
		m.emitPhaseCard(analysisLaneImplement, "verify", phaseStateDone, detail)
	}
	m.logVerificationFeedback(verificationFeedbackFromResult(plan, out))
	return out
}

func verificationFeedbackFromResult(plan verificationPlan, verify implementationVerification) verificationFeedbackSnapshot {
	failed := make([]string, 0)
	for i, result := range verify.results {
		if strings.TrimSpace(result.Error) == "" {
			continue
		}
		command := ""
		if i < len(verify.commands) {
			command = verify.commands[i]
		}
		if command == "" {
			command = result.Tool
		}
		failed = append(failed, command)
	}
	recommendation := "keep current verification plan"
	if verify.failed {
		classes := frontier.ClassifyVerificationFailures(verify.commands, verify.results)
		if len(classes) > 0 {
			recommendation = "repair " + strings.Join(classes, ", ") + " before widening scope"
		} else {
			recommendation = "repair failing checks before widening scope"
		}
	} else if plan.UsesFullSuite {
		recommendation = "record full-suite success for similar high-risk surfaces"
	} else if strings.EqualFold(plan.Confidence, "low") {
		recommendation = "consider escalating to targeted tests when similar files change again"
	}
	return verificationFeedbackSnapshot{
		RiskLevel:             strings.TrimSpace(plan.RiskLevel),
		Coverage:              strings.TrimSpace(plan.Coverage),
		Confidence:            strings.TrimSpace(plan.Confidence),
		Commands:              append([]string(nil), verify.commands...),
		FailedCommands:        uniqueNonEmptyStrings(failed),
		FailureClasses:        frontier.ClassifyVerificationFailures(verify.commands, verify.results),
		LearnedRecommendation: recommendation,
	}
}

func implementationVerifyCommands(mgr *workspace.Manager) []string {
	return implementationVerificationPlan(mgr, nil).Commands
}

func changedPathsFromImplementEvents(requests map[int]agent.ToolRequestEvent, results map[int]agent.ToolResultEvent) []string {
	seen := make(map[string]struct{})
	paths := make([]string, 0, 4)
	for step, resultEvent := range results {
		req, ok := requests[step]
		if !ok {
			continue
		}
		if !writeToolResultChanged(req.Request, resultEvent.Result) {
			continue
		}
		for _, path := range pathsFromWriteRequest(req.Request) {
			if path == "" {
				continue
			}
			if _, ok := seen[path]; ok {
				continue
			}
			seen[path] = struct{}{}
			paths = append(paths, path)
		}
	}
	slices.Sort(paths)
	return paths
}

func composeImplementationSummary(reply string, changedPaths []string, verify implementationVerification, refactorStage *structuralRefactorStage) string {
	sections := make([]string, 0, 4)
	if strings.TrimSpace(reply) != "" {
		sections = append(sections, strings.TrimSpace(reply))
	}
	if len(changedPaths) > 0 {
		var b strings.Builder
		b.WriteString("Changed files:\n")
		for _, path := range changedPaths {
			b.WriteString("- ")
			b.WriteString(path)
			b.WriteString("\n")
		}
		sections = append(sections, strings.TrimSpace(b.String()))
	}
	if len(verify.results) > 0 {
		var b strings.Builder
		b.WriteString("Verification:\n")
		if verify.confidence != "" {
			b.WriteString("- confidence: ")
			b.WriteString(verify.confidence)
			if verify.coverage != "" {
				b.WriteString(" (")
				b.WriteString(verify.coverage)
				b.WriteString(")")
			}
			b.WriteString("\n")
		}
		if strings.TrimSpace(verify.riskLevel) != "" {
			b.WriteString("- risk: ")
			b.WriteString(strings.TrimSpace(verify.riskLevel))
			b.WriteString("\n")
		}
		if len(verify.expectedFailures) > 0 {
			b.WriteString("- expected failure modes: ")
			b.WriteString(strings.Join(uniqueLimitedStrings(verify.expectedFailures, 4), ", "))
			b.WriteString("\n")
		}
		if strings.TrimSpace(verify.repairPolicy) != "" {
			b.WriteString("- repair policy: ")
			b.WriteString(truncateText(strings.TrimSpace(verify.repairPolicy), 180))
			b.WriteString("\n")
		}
		for i, result := range verify.results {
			command := fmt.Sprintf("check %d", i+1)
			if i < len(verify.commands) && strings.TrimSpace(verify.commands[i]) != "" {
				command = "`" + verify.commands[i] + "`"
			}
			status := "passed"
			if result.Error != "" {
				status = "failed"
			}
			b.WriteString("- ")
			b.WriteString(command)
			b.WriteString(": ")
			b.WriteString(status)
			b.WriteString("\n")
		}
		sections = append(sections, strings.TrimSpace(b.String()))
	}
	if refactorStage != nil && strings.TrimSpace(refactorStage.NextStep) != "" {
		var b strings.Builder
		b.WriteString("Refactor stage:\n")
		b.WriteString("- stage: ")
		b.WriteString(strconv.Itoa(refactorStage.Stage))
		b.WriteString("\n")
		if strings.TrimSpace(refactorStage.FocusPath) != "" {
			b.WriteString("- focus: ")
			b.WriteString(refactorStage.FocusPath)
			b.WriteString("\n")
		}
		if len(refactorStage.Remaining) > 0 {
			b.WriteString("- remaining: ")
			b.WriteString(strings.Join(refactorStage.Remaining, ", "))
			b.WriteString("\n")
		}
		b.WriteString("- next: ")
		b.WriteString(refactorStage.NextStep)
		sections = append(sections, strings.TrimSpace(b.String()))
	}
	return strings.Join(sections, "\n\n")
}

type finalAnswerCriticResult struct {
	Contract string   `json:"contract,omitempty"`
	Passed   bool     `json:"passed"`
	Action   string   `json:"action,omitempty"`
	Repaired bool     `json:"repaired,omitempty"`
	Findings []string `json:"findings,omitempty"`
}

func (m *Model) applyFinalAnswerCritic(answer string, changedPaths []string, verify implementationVerification, refactorStage *structuralRefactorStage) string {
	critic := critiqueImplementationFinalAnswer(answer, changedPaths, verify)
	if critic.Passed {
		critic.Action = "accept"
		m.logFinalAnswerCritic(critic)
		return answer
	}
	critic.Action = "repair"
	critic.Repaired = true
	repaired := composeImplementationSummary("", changedPaths, verify, refactorStage)
	if strings.TrimSpace(repaired) == "" {
		repaired = "Implementation completed, but the final answer critic could not reconstruct a deterministic summary."
	}
	repairedCritic := critiqueImplementationFinalAnswer(repaired, changedPaths, verify)
	if !repairedCritic.Passed {
		critic.Findings = append(critic.Findings, prefixedCriticFindings("repair", repairedCritic.Findings)...)
		critic.Repaired = false
	}
	m.logFinalAnswerCritic(critic)
	return repaired
}

func critiqueImplementationFinalAnswer(answer string, changedPaths []string, verify implementationVerification) finalAnswerCriticResult {
	result := finalAnswerCriticResult{
		Contract: string(answerContractImplementation),
		Passed:   true,
	}
	addFinding := func(finding string) {
		result.Passed = false
		result.Findings = append(result.Findings, finding)
	}
	normalized := strings.ToLower(strings.TrimSpace(answer))
	if strings.TrimSpace(answer) == "" {
		addFinding("final answer is empty")
		return result
	}
	if len(changedPaths) > 0 {
		if !strings.Contains(normalized, "changed files:") {
			addFinding("missing changed files section")
		}
		for _, path := range changedPaths {
			path = strings.TrimSpace(path)
			if path != "" && !strings.Contains(normalized, strings.ToLower(path)) {
				addFinding("missing changed file path: " + path)
			}
		}
	}
	if len(verify.results) > 0 {
		if !strings.Contains(normalized, "verification:") {
			addFinding("missing verification section")
		}
		for _, command := range verify.commands {
			command = strings.TrimSpace(command)
			if command != "" && !strings.Contains(normalized, strings.ToLower(command)) {
				addFinding("missing verification command: " + command)
			}
		}
		if strings.TrimSpace(verify.riskLevel) != "" && !strings.Contains(normalized, "risk:") {
			addFinding("missing verification risk")
		}
		if verify.failed && containsAny(normalized, "verification passed", "all tests passed", "tests passed", "checks passed") {
			addFinding("overconfident verification claim after failed verification")
		}
		if !verify.failed && len(changedPaths) > 0 && containsAny(normalized, "did not complete cleanly", "did not complete the requested edit", "final blocker:", "blocker:") {
			addFinding("stale blocker language after verified implementation")
		}
	}
	return result
}

func (m *Model) logFinalAnswerCritic(critic finalAnswerCriticResult) {
	if m == nil || m.debugLogger == nil {
		return
	}
	data, err := json.Marshal(critic)
	if err != nil {
		return
	}
	m.logMessage("final_answer_critic", string(data))
}

func buildImplementationNoopSummary(reply string, results map[int]agent.ToolResultEvent) string {
	if strings.TrimSpace(reply) != "" && !strings.Contains(strings.ToLower(reply), "recommended next step") {
		return strings.TrimSpace(reply)
	}
	var lastSummary string
	var lastError string
	steps := make([]int, 0, len(results))
	for step := range results {
		steps = append(steps, step)
	}
	slices.Sort(steps)
	for _, step := range steps {
		result := results[step].Result
		if strings.TrimSpace(result.Summary) != "" {
			lastSummary = strings.TrimSpace(result.Summary)
		}
		if strings.TrimSpace(result.Error) != "" {
			lastError = strings.TrimSpace(result.Error)
		}
	}
	if lastSummary == "" {
		lastSummary = "implementation agent stopped before making a change"
	}
	if lastError == "" {
		lastError = "no file edits were applied"
	}
	return "Implementation agent did not complete the requested edit.\n\nLast step:\n- " + lastSummary + "\n\nBlocker:\n- " + lastError
}

func implementationReplyReportsNoChangeBlocker(reply string) bool {
	normalized := strings.ToLower(strings.TrimSpace(reply))
	return containsAny(
		normalized,
		"did not complete",
		"could not complete",
		"failed",
		"blocker",
		"blocked",
		"not an approved",
		"budget exhausted",
	)
}

func implementationSummaryReportsNoop(summary string) bool {
	normalized := strings.ToLower(strings.TrimSpace(summary))
	return containsAny(
		normalized,
		"no changes",
		"no workspace changes",
		"no workspace edit",
		"no file edits",
		"no file edit",
		"already satisfied",
		"already present",
	)
}

func (m *Model) tryDirectPromptEdit(
	ctx context.Context,
	mgr *workspace.Manager,
	prompt string,
	startStep int,
	journal *executionJournalRecorder,
	guard *implementWriteGuard,
	onBlocked func(req workspace.ToolRequest, blocked workspace.ToolResult) workspace.ToolResult,
) directImplementationShortcut {
	if mgr == nil {
		return directImplementationShortcut{}
	}
	if promptShouldSkipDirectQuotedEdit(prompt) {
		return directImplementationShortcut{}
	}
	paths := promptMentionedWorkspacePaths(prompt)
	quoted := quotedPromptFragments(prompt)
	if len(paths) == 0 || len(quoted) < 2 {
		return directImplementationShortcut{}
	}

	oldText := strings.TrimSpace(quoted[0])
	newText := strings.TrimSpace(quoted[1])
	if oldText == "" || newText == "" || oldText == newText {
		return directImplementationShortcut{}
	}

	step := startStep
	for _, path := range paths {
		lineNumber := firstPromptEditLine(mgr, path, oldText)
		req := workspace.ToolRequest{
			Tool:    "edit_file",
			Path:    path,
			OldText: oldText,
			NewText: newText,
		}
		if guard != nil {
			if blocked := guard.PreExecute(req); blocked != nil {
				result := *blocked
				if onBlocked != nil {
					result = onBlocked(req, result)
				}
				m.logToolResult(agent.ToolResultEvent{Step: step, Agent: "implement", Result: result})
				m.events <- toolTraceMsg{text: workspace.FormatToolTrace(result)}
				step++
				if isWriteApprovalRequiredResult(result) {
					return directImplementationShortcut{terminal: true, nextStep: step}
				}
				continue
			}
		}
		m.logToolRequest(agent.ToolRequestEvent{
			Step:      step,
			Agent:     "implement",
			Raw:       fmt.Sprintf("{\"tool\":\"edit_file\",\"path\":%q,\"old_text\":%q,\"new_text\":%q}", path, oldText, newText),
			Request:   req,
			Mode:      workspace.ModeNormal,
			Broad:     false,
			Narrowing: true,
			Action:    "edit_file:" + path,
		})
		if capture := captureExecutionJournalResult(journal, req); capture != nil {
			m.logToolResult(agent.ToolResultEvent{Step: step, Agent: "implement", Result: *capture})
			m.events <- toolTraceMsg{text: workspace.FormatToolTrace(*capture)}
			step++
			continue
		}
		result := mgr.Execute(ctx, req)
		m.logToolResult(agent.ToolResultEvent{Step: step, Agent: "implement", Result: result})
		m.events <- toolTraceMsg{text: workspace.FormatToolTrace(result)}
		if shouldInvalidateExploreCacheForTool(result) {
			m.events <- exploreCacheInvalidatedMsg{
				reason: fmt.Sprintf("workspace changed via %s; clearing cached explore profile", result.Tool),
			}
		}
		step++
		if result.Error != "" {
			continue
		}
		summary := fmt.Sprintf("Applied a targeted edit in %s by replacing %q with %q.", path, oldText, newText)
		if lineNumber > 0 {
			summary = fmt.Sprintf("Applied a targeted edit in %s line %d by replacing %q with %q.", path, lineNumber, oldText, newText)
		}
		return directImplementationShortcut{
			applied:      true,
			changedPaths: []string{path},
			summary:      summary,
			nextStep:     step,
		}
	}
	return directImplementationShortcut{}
}

func promptShouldSkipDirectQuotedEdit(prompt string) bool {
	normalized := strings.ToLower(prompt)
	return containsAny(normalized,
		"go_file_outline",
		"go_move_declarations",
		"pattern:\"tag:",
		"pattern:'tag:",
		"multi-file refactor",
		"large go refactor",
		"split the monolithic file",
		"split into cohesive",
	)
}

func (m *Model) tryDeterministicVerificationRepair(
	ctx context.Context,
	mgr *workspace.Manager,
	verify implementationVerification,
	startStep int,
	journal *executionJournalRecorder,
	guard *implementWriteGuard,
	onBlocked func(req workspace.ToolRequest, blocked workspace.ToolResult) workspace.ToolResult,
) directImplementationShortcut {
	if mgr == nil {
		return directImplementationShortcut{}
	}
	diagnostics := parseUnusedVariableDiagnostics(verify)
	if len(diagnostics) == 0 {
		return directImplementationShortcut{}
	}

	step := startStep
	changedPaths := make([]string, 0, len(diagnostics))
	repairs := make([]string, 0, len(diagnostics))
	for _, diag := range diagnostics {
		readReq := workspace.ToolRequest{
			Tool:      "read_file",
			Path:      diag.Path,
			StartLine: diag.Line,
			EndLine:   diag.Line,
		}
		m.logToolRequest(agent.ToolRequestEvent{
			Step:      step,
			Agent:     "implement",
			Raw:       fmt.Sprintf("{\"tool\":\"read_file\",\"path\":%q,\"start_line\":%d,\"end_line\":%d}", diag.Path, diag.Line, diag.Line),
			Request:   readReq,
			Mode:      workspace.ModeNormal,
			Broad:     false,
			Narrowing: true,
			Action:    fmt.Sprintf("read_file:%s:%d", diag.Path, diag.Line),
		})
		readResult := mgr.Execute(ctx, readReq)
		m.logToolResult(agent.ToolResultEvent{Step: step, Agent: "implement", Result: readResult})
		m.events <- toolTraceMsg{text: workspace.FormatToolTrace(readResult)}
		step++
		if readResult.Error != "" {
			continue
		}
		lineText, ok := extractReadFileLine(readResult.Output, diag.Line)
		if !ok || !looksLikeUnusedVariableDeclaration(lineText, diag.Name) {
			continue
		}

		editReq := workspace.ToolRequest{
			Tool:      "edit_file",
			Path:      diag.Path,
			StartLine: diag.Line,
			EndLine:   diag.Line,
			Content:   "",
		}
		if guard != nil {
			if blocked := guard.PreExecute(editReq); blocked != nil {
				result := *blocked
				if onBlocked != nil {
					result = onBlocked(editReq, result)
				}
				m.logToolResult(agent.ToolResultEvent{Step: step, Agent: "implement", Result: result})
				m.events <- toolTraceMsg{text: workspace.FormatToolTrace(result)}
				step++
				if isWriteApprovalRequiredResult(result) {
					return directImplementationShortcut{terminal: true, nextStep: step}
				}
				continue
			}
		}
		m.logToolRequest(agent.ToolRequestEvent{
			Step:      step,
			Agent:     "implement",
			Raw:       fmt.Sprintf("{\"tool\":\"edit_file\",\"path\":%q,\"start_line\":%d,\"end_line\":%d,\"content\":\"\"}", diag.Path, diag.Line, diag.Line),
			Request:   editReq,
			Mode:      workspace.ModeNormal,
			Broad:     false,
			Narrowing: true,
			Action:    fmt.Sprintf("edit_file:%s:%d-remove-unused", diag.Path, diag.Line),
		})
		if capture := captureExecutionJournalResult(journal, editReq); capture != nil {
			m.logToolResult(agent.ToolResultEvent{Step: step, Agent: "implement", Result: *capture})
			m.events <- toolTraceMsg{text: workspace.FormatToolTrace(*capture)}
			step++
			continue
		}
		editResult := mgr.Execute(ctx, editReq)
		m.logToolResult(agent.ToolResultEvent{Step: step, Agent: "implement", Result: editResult})
		m.events <- toolTraceMsg{text: workspace.FormatToolTrace(editResult)}
		if shouldInvalidateExploreCacheForTool(editResult) {
			m.events <- exploreCacheInvalidatedMsg{
				reason: fmt.Sprintf("workspace changed via %s; clearing cached explore profile", editResult.Tool),
			}
		}
		step++
		if editResult.Error != "" {
			continue
		}
		changedPaths = append(changedPaths, diag.Path)
		repairs = append(repairs, fmt.Sprintf("removed unused declaration %q in %s line %d", diag.Name, diag.Path, diag.Line))
	}

	if len(changedPaths) == 0 {
		return directImplementationShortcut{}
	}
	return directImplementationShortcut{
		applied:      true,
		changedPaths: uniqueNonEmptyStrings(changedPaths),
		summary:      "Applied deterministic verification cleanup: " + strings.Join(repairs, "; ") + ".",
		nextStep:     step,
	}
}

func (m *Model) tryStructuredExecutionPlan(
	ctx context.Context,
	mgr *workspace.Manager,
	prompt string,
	history []llama.ChatMessage,
	opts llama.ChatOptions,
	packet implementationTargetPacket,
	startStep int,
	journal *executionJournalRecorder,
	guard *implementWriteGuard,
	onBlocked func(req workspace.ToolRequest, blocked workspace.ToolResult) workspace.ToolResult,
) directImplementationShortcut {
	if mgr == nil || len(packet.PrimaryPaths) == 0 {
		return directImplementationShortcut{}
	}
	feedback := ""
	var plan structuredExecutionPlan
	var rawReply string
	for attempt := 0; attempt < 2; attempt++ {
		var err error
		plan, rawReply, err = m.proposeStructuredExecutionPlan(ctx, mgr, history, opts, prompt, packet, feedback)
		if err != nil {
			if isShortcutContextError(err) {
				m.logMessage("implement_execution_plan", "structured plan skipped after shortcut timeout: "+err.Error())
				if preview := previewReplyForLog(rawReply); preview != "" {
					m.logMessage("implement_execution_plan", "structured plan raw reply: "+preview)
				}
				return directImplementationShortcut{}
			}
			if attempt == 0 {
				feedback = "Previous reply was rejected: " + err.Error()
				continue
			}
			m.logMessage("implement_execution_plan", "structured plan rejected: "+err.Error())
			if preview := previewReplyForLog(rawReply); preview != "" {
				m.logMessage("implement_execution_plan", "structured plan raw reply: "+preview)
			}
			return directImplementationShortcut{}
		}
		if len(plan.Operations) == 0 {
			if attempt == 0 {
				feedback = "Previous reply proposed no usable operations. Return at least one valid bounded edit or an explicit empty plan only if the request cannot be safely applied."
				continue
			}
			summary := strings.TrimSpace(plan.Summary)
			if summary == "" {
				summary = "no operations proposed"
			}
			m.logMessage("implement_execution_plan", "structured plan produced no operations: "+summary)
			if preview := previewReplyForLog(rawReply); preview != "" {
				m.logMessage("implement_execution_plan", "structured plan raw reply: "+preview)
			}
			return directImplementationShortcut{}
		}
		if err := validateStructuredExecutionPlan(packet, plan); err != nil {
			if attempt == 0 {
				feedback = "Previous proposed plan was rejected: " + err.Error()
				continue
			}
			m.logMessage("implement_execution_plan", "structured plan rejected after policy validation: "+err.Error())
			if preview := previewReplyForLog(rawReply); preview != "" {
				m.logMessage("implement_execution_plan", "structured plan raw reply: "+preview)
			}
			return directImplementationShortcut{}
		}
		validationErr := ""
		reqs := make([]workspace.ToolRequest, 0, len(plan.Operations))
		for _, op := range plan.Operations {
			req := workspace.ToolRequest{
				Tool:      strings.TrimSpace(op.Tool),
				Path:      normalizeGuardedPath(op.Path),
				StartLine: op.StartLine,
				EndLine:   op.EndLine,
				Content:   op.Content,
				OldText:   op.OldText,
				NewText:   op.NewText,
			}
			if blocked := validateStructuredExecutionEdit(packet, req); blocked != nil {
				validationErr = blocked.Error
				break
			}
			reqs = append(reqs, req)
		}
		if validationErr == "" {
			if err := preflightStructuredExecutionPlan(mgr, reqs); err != nil {
				validationErr = err.Error()
			}
		}
		if validationErr == "" {
			break
		}
		if attempt == 0 {
			feedback = "Previous proposed plan was rejected: " + validationErr
			continue
		}
		m.logMessage("implement_execution_plan", "structured plan rejected after preflight: "+validationErr)
		if preview := previewReplyForLog(rawReply); preview != "" {
			m.logMessage("implement_execution_plan", "structured plan raw reply: "+preview)
		}
		return directImplementationShortcut{}
	}
	m.logMessage("implement_execution_plan", fmt.Sprintf("structured plan proposed %d operation(s): %s", len(plan.Operations), firstNonEmptyString(strings.TrimSpace(plan.Summary), "no summary")))
	m.setExecutionPlanSummary(
		analysisLaneImplement,
		firstNonEmptyString(strings.TrimSpace(plan.Summary), "Prepared a bounded structured edit plan."),
		formatStructuredExecutionPlanSteps(plan),
	)
	reqs := make([]workspace.ToolRequest, 0, len(plan.Operations))
	for _, op := range plan.Operations {
		reqs = append(reqs, workspace.ToolRequest{
			Tool:      strings.TrimSpace(op.Tool),
			Path:      normalizeGuardedPath(op.Path),
			StartLine: op.StartLine,
			EndLine:   op.EndLine,
			Content:   op.Content,
			OldText:   op.OldText,
			NewText:   op.NewText,
		})
	}
	if proposal := buildWriteApprovalProposalForRequests(ctx, mgr, prompt, guard, reqs); proposal != nil {
		m.logMessage("implement_execution_plan", "staged guarded write approval for structured plan scope: "+strings.Join(proposal.Paths, ", "))
		return directImplementationShortcut{
			terminal: true,
			proposal: proposal,
		}
	}
	changedPaths := make([]string, 0, len(plan.Operations))
	appliedSummaries := make([]string, 0, len(plan.Operations)+1)
	step := startStep
	for _, op := range plan.Operations {
		req := workspace.ToolRequest{
			Tool:      strings.TrimSpace(op.Tool),
			Path:      normalizeGuardedPath(op.Path),
			StartLine: op.StartLine,
			EndLine:   op.EndLine,
			Content:   op.Content,
			OldText:   op.OldText,
			NewText:   op.NewText,
		}
		if blocked := validateStructuredExecutionEdit(packet, req); blocked != nil {
			return directImplementationShortcut{}
		}
		if guard != nil {
			if blocked := guard.PreExecute(req); blocked != nil {
				result := *blocked
				if onBlocked != nil {
					result = onBlocked(req, result)
				}
				m.logToolResult(agent.ToolResultEvent{Step: step, Agent: "implement", Result: result})
				m.events <- toolTraceMsg{text: workspace.FormatToolTrace(result)}
				step++
				if isWriteApprovalRequiredResult(result) {
					return directImplementationShortcut{terminal: true, nextStep: step}
				}
				return directImplementationShortcut{}
			}
		}
		raw := marshalStructuredExecutionRequest(req)
		m.logToolRequest(agent.ToolRequestEvent{
			Step:      step,
			Agent:     "implement",
			Raw:       raw,
			Request:   req,
			Mode:      workspace.ModeNormal,
			Broad:     false,
			Narrowing: true,
			Action:    "structured_" + toolActionForStructuredPlan(req),
		})
		if capture := captureExecutionJournalResult(journal, req); capture != nil {
			m.logToolResult(agent.ToolResultEvent{Step: step, Agent: "implement", Result: *capture})
			m.events <- toolTraceMsg{text: workspace.FormatToolTrace(*capture)}
			step++
			return directImplementationShortcut{}
		}
		result := mgr.Execute(ctx, req)
		m.logToolResult(agent.ToolResultEvent{Step: step, Agent: "implement", Result: result})
		m.events <- toolTraceMsg{text: workspace.FormatToolTrace(result)}
		if writeToolResultChanged(req, result) && shouldInvalidateExploreCacheForTool(result) {
			m.events <- exploreCacheInvalidatedMsg{
				reason: fmt.Sprintf("workspace changed via %s; clearing cached explore profile", result.Tool),
			}
		}
		step++
		if result.Error != "" {
			return directImplementationShortcut{}
		}
		if writeToolResultChanged(req, result) {
			changedPaths = append(changedPaths, req.Path)
			if strings.TrimSpace(op.Reason) != "" {
				appliedSummaries = append(appliedSummaries, fmt.Sprintf("%s (%s)", req.Path, strings.TrimSpace(op.Reason)))
			} else {
				appliedSummaries = append(appliedSummaries, req.Path)
			}
		}
	}
	if len(changedPaths) == 0 {
		m.logMessage("implement_execution_plan", "structured plan produced only no-op edits")
		return directImplementationShortcut{}
	}
	summary := strings.TrimSpace(plan.Summary)
	if summary == "" {
		summary = "Applied a structured execution plan to the targeted files."
	}
	if len(appliedSummaries) > 0 {
		summary = strings.TrimSpace(summary + " Operations: " + strings.Join(appliedSummaries, "; ") + ".")
	}
	return directImplementationShortcut{
		applied:      true,
		changedPaths: uniqueNonEmptyStrings(changedPaths),
		summary:      summary,
		nextStep:     step,
	}
}

func (m *Model) tryFocusedWindowRewrite(
	ctx context.Context,
	mgr *workspace.Manager,
	prompt string,
	opts llama.ChatOptions,
	packet implementationTargetPacket,
	bootstrap implementationBootstrap,
	startStep int,
	journal *executionJournalRecorder,
	guard *implementWriteGuard,
	onBlocked func(req workspace.ToolRequest, blocked workspace.ToolResult) workspace.ToolResult,
) directImplementationShortcut {
	if mgr == nil || len(packet.PrimaryPaths) != 1 {
		m.logMessage("implement_focused_rewrite", "focused rewrite skipped: requires exactly one primary target file")
		return directImplementationShortcut{}
	}
	path, startLine, endLine, snippet, ok := focusedRewriteWindow(packet, bootstrap)
	if !ok {
		m.logMessage("implement_focused_rewrite", "focused rewrite skipped: no suitable bootstrapped read window")
		return directImplementationShortcut{}
	}
	m.logMessage("implement_focused_rewrite", fmt.Sprintf("focused rewrite window: %s:%d-%d snippet_chars=%d", path, startLine, endLine, len(snippet)))
	reply, err := m.proposeFocusedWindowRewrite(ctx, mgr, prompt, opts, packet, path, startLine, endLine, snippet, false)
	if err != nil {
		m.logMessage("implement_focused_rewrite", "focused rewrite rejected: "+err.Error())
		if preview := previewReplyForLog(reply); preview != "" {
			m.logMessage("implement_focused_rewrite", "focused rewrite raw reply: "+preview)
		}
		return directImplementationShortcut{}
	}
	content := cleanFocusedRewriteReply(reply)
	if content == "" || content == snippet {
		retryReply, retryErr := m.proposeFocusedWindowRewrite(ctx, mgr, prompt, opts, packet, path, startLine, endLine, snippet, true)
		if retryErr != nil {
			m.logMessage("implement_focused_rewrite", "focused rewrite retry rejected: "+retryErr.Error())
			if preview := previewReplyForLog(retryReply); preview != "" {
				m.logMessage("implement_focused_rewrite", "focused rewrite retry raw reply: "+preview)
			}
			return directImplementationShortcut{}
		}
		content = cleanFocusedRewriteReply(retryReply)
		if content == "" || content == snippet {
			m.logMessage("implement_focused_rewrite", "focused rewrite produced no usable change")
			if preview := previewReplyForLog(reply); preview != "" {
				m.logMessage("implement_focused_rewrite", "focused rewrite first raw reply: "+preview)
			}
			if preview := previewReplyForLog(retryReply); preview != "" {
				m.logMessage("implement_focused_rewrite", "focused rewrite retry raw reply: "+preview)
			}
			return directImplementationShortcut{}
		}
	}
	req := workspace.ToolRequest{
		Tool:      "edit_file",
		Path:      path,
		StartLine: startLine,
		EndLine:   endLine,
		Content:   content,
	}
	if err := preflightStructuredExecutionRequest(mgr, req); err != nil {
		m.logMessage("implement_focused_rewrite", "focused rewrite rejected by preflight: "+err.Error())
		return directImplementationShortcut{}
	}
	if guard != nil {
		if blocked := guard.PreExecute(req); blocked != nil {
			result := *blocked
			if onBlocked != nil {
				result = onBlocked(req, result)
			}
			m.logToolResult(agent.ToolResultEvent{Step: startStep, Agent: "implement", Result: result})
			m.events <- toolTraceMsg{text: workspace.FormatToolTrace(result)}
			if isWriteApprovalRequiredResult(result) {
				return directImplementationShortcut{terminal: true, nextStep: startStep + 1}
			}
			return directImplementationShortcut{}
		}
	}
	raw := marshalStructuredExecutionRequest(req)
	m.logMessage("implement_focused_rewrite", fmt.Sprintf("focused rewrite proposed for %s:%d-%d", path, startLine, endLine))
	m.logToolRequest(agent.ToolRequestEvent{
		Step:      startStep,
		Agent:     "implement",
		Raw:       raw,
		Request:   req,
		Mode:      workspace.ModeNormal,
		Broad:     false,
		Narrowing: true,
		Action:    "focused_window_rewrite",
	})
	if capture := captureExecutionJournalResult(journal, req); capture != nil {
		m.logToolResult(agent.ToolResultEvent{Step: startStep, Agent: "implement", Result: *capture})
		m.events <- toolTraceMsg{text: workspace.FormatToolTrace(*capture)}
		return directImplementationShortcut{}
	}
	result := mgr.Execute(ctx, req)
	m.logToolResult(agent.ToolResultEvent{Step: startStep, Agent: "implement", Result: result})
	m.events <- toolTraceMsg{text: workspace.FormatToolTrace(result)}
	if shouldInvalidateExploreCacheForTool(result) {
		m.events <- exploreCacheInvalidatedMsg{
			reason: fmt.Sprintf("workspace changed via %s; clearing cached explore profile", result.Tool),
		}
	}
	if result.Error != "" {
		return directImplementationShortcut{}
	}
	if !writeToolResultChanged(req, result) {
		m.logMessage("implement_focused_rewrite", "focused rewrite produced only a no-op edit")
		return directImplementationShortcut{}
	}
	return directImplementationShortcut{
		applied:      true,
		changedPaths: []string{path},
		summary:      fmt.Sprintf("Applied a focused rewrite to %s lines %d-%d.", path, startLine, endLine),
		nextStep:     startStep + 1,
	}
}

func (m *Model) tryFocusedStructuredExecutionPlan(
	ctx context.Context,
	mgr *workspace.Manager,
	prompt string,
	opts llama.ChatOptions,
	packet implementationTargetPacket,
	bootstrap implementationBootstrap,
	startStep int,
	journal *executionJournalRecorder,
	guard *implementWriteGuard,
	onBlocked func(req workspace.ToolRequest, blocked workspace.ToolResult) workspace.ToolResult,
) directImplementationShortcut {
	if mgr == nil || len(packet.PrimaryPaths) != 1 {
		m.logMessage("implement_focused_plan", "focused structured plan skipped: requires exactly one primary target file")
		return directImplementationShortcut{}
	}
	path, windowStart, windowEnd, snippet, ok := focusedRewriteWindow(packet, bootstrap)
	if !ok {
		m.logMessage("implement_focused_plan", "focused structured plan skipped: no suitable bootstrapped read window")
		return directImplementationShortcut{}
	}
	m.logMessage("implement_focused_plan", fmt.Sprintf("focused structured plan window: %s:%d-%d snippet_chars=%d", path, windowStart, windowEnd, len(snippet)))
	feedback := ""
	var plan structuredExecutionPlan
	var rawReply string
	for attempt := 0; attempt < 2; attempt++ {
		var err error
		plan, rawReply, err = m.proposeFocusedStructuredExecutionPlan(ctx, mgr, prompt, opts, packet, path, windowStart, windowEnd, snippet, feedback)
		if err != nil {
			if isShortcutContextError(err) {
				m.logMessage("implement_focused_plan", "focused structured plan skipped after shortcut timeout: "+err.Error())
				if preview := previewReplyForLog(rawReply); preview != "" {
					m.logMessage("implement_focused_plan", "focused structured plan raw reply: "+preview)
				}
				return directImplementationShortcut{}
			}
			if attempt == 0 {
				feedback = "Previous reply was rejected: " + err.Error()
				continue
			}
			m.logMessage("implement_focused_plan", "focused structured plan rejected: "+err.Error())
			if preview := previewReplyForLog(rawReply); preview != "" {
				m.logMessage("implement_focused_plan", "focused structured plan raw reply: "+preview)
			}
			return directImplementationShortcut{}
		}
		if len(plan.Operations) == 0 {
			if attempt == 0 {
				feedback = "Previous reply proposed no usable operations. Return at least one valid bounded edit within the current file window."
				continue
			}
			summary := strings.TrimSpace(plan.Summary)
			if summary == "" {
				summary = "no operations proposed"
			}
			m.logMessage("implement_focused_plan", "focused structured plan produced no operations: "+summary)
			if preview := previewReplyForLog(rawReply); preview != "" {
				m.logMessage("implement_focused_plan", "focused structured plan raw reply: "+preview)
			}
			return directImplementationShortcut{}
		}
		if err := validateStructuredExecutionPlan(packet, plan); err != nil {
			if attempt == 0 {
				feedback = "Previous proposed edit was rejected: " + err.Error()
				continue
			}
			m.logMessage("implement_focused_plan", "focused structured plan rejected after policy validation: "+err.Error())
			if preview := previewReplyForLog(rawReply); preview != "" {
				m.logMessage("implement_focused_plan", "focused structured plan raw reply: "+preview)
			}
			return directImplementationShortcut{}
		}
		validationErr := ""
		for _, op := range plan.Operations {
			req := workspace.ToolRequest{
				Tool:      strings.TrimSpace(op.Tool),
				Path:      normalizeGuardedPath(op.Path),
				StartLine: op.StartLine,
				EndLine:   op.EndLine,
				Content:   op.Content,
				OldText:   op.OldText,
				NewText:   op.NewText,
			}
			if blocked := validateFocusedStructuredExecutionEdit(path, windowStart, windowEnd, snippet, req); blocked != nil {
				validationErr = blocked.Error
				break
			}
			if err := preflightStructuredExecutionRequest(mgr, req); err != nil {
				validationErr = err.Error()
				break
			}
		}
		if validationErr == "" {
			break
		}
		if attempt == 0 {
			feedback = "Previous proposed edit was rejected: " + validationErr
			continue
		}
		m.logMessage("implement_focused_plan", "focused structured plan op rejected: "+validationErr)
		if preview := previewReplyForLog(rawReply); preview != "" {
			m.logMessage("implement_focused_plan", "focused structured plan raw reply: "+preview)
		}
		return directImplementationShortcut{}
	}
	m.logMessage("implement_focused_plan", fmt.Sprintf("focused structured plan proposed %d operation(s): %s", len(plan.Operations), firstNonEmptyString(strings.TrimSpace(plan.Summary), "no summary")))
	m.setExecutionPlanSummary(
		analysisLaneImplement,
		firstNonEmptyString(strings.TrimSpace(plan.Summary), "Prepared a focused structured edit plan."),
		formatStructuredExecutionPlanSteps(plan),
	)
	reqs := make([]workspace.ToolRequest, 0, len(plan.Operations))
	for _, op := range plan.Operations {
		reqs = append(reqs, workspace.ToolRequest{
			Tool:      strings.TrimSpace(op.Tool),
			Path:      normalizeGuardedPath(op.Path),
			StartLine: op.StartLine,
			EndLine:   op.EndLine,
			Content:   op.Content,
			OldText:   op.OldText,
			NewText:   op.NewText,
		})
	}
	if proposal := buildWriteApprovalProposalForRequests(ctx, mgr, prompt, guard, reqs); proposal != nil {
		m.logMessage("implement_focused_plan", "staged guarded write approval for focused plan scope: "+strings.Join(proposal.Paths, ", "))
		return directImplementationShortcut{
			terminal: true,
			proposal: proposal,
		}
	}

	step := startStep
	changedPaths := make([]string, 0, len(plan.Operations))
	appliedSummaries := make([]string, 0, len(plan.Operations)+1)
	for _, op := range plan.Operations {
		req := workspace.ToolRequest{
			Tool:      strings.TrimSpace(op.Tool),
			Path:      normalizeGuardedPath(op.Path),
			StartLine: op.StartLine,
			EndLine:   op.EndLine,
			Content:   op.Content,
			OldText:   op.OldText,
			NewText:   op.NewText,
		}
		if guard != nil {
			if blocked := guard.PreExecute(req); blocked != nil {
				result := *blocked
				if onBlocked != nil {
					result = onBlocked(req, result)
				}
				m.logToolResult(agent.ToolResultEvent{Step: step, Agent: "implement", Result: result})
				m.events <- toolTraceMsg{text: workspace.FormatToolTrace(result)}
				step++
				if isWriteApprovalRequiredResult(result) {
					return directImplementationShortcut{terminal: true, nextStep: step}
				}
				return directImplementationShortcut{}
			}
		}
		raw := marshalStructuredExecutionRequest(req)
		m.logToolRequest(agent.ToolRequestEvent{
			Step:      step,
			Agent:     "implement",
			Raw:       raw,
			Request:   req,
			Mode:      workspace.ModeNormal,
			Broad:     false,
			Narrowing: true,
			Action:    "focused_structured_" + toolActionForStructuredPlan(req),
		})
		if capture := captureExecutionJournalResult(journal, req); capture != nil {
			m.logToolResult(agent.ToolResultEvent{Step: step, Agent: "implement", Result: *capture})
			m.events <- toolTraceMsg{text: workspace.FormatToolTrace(*capture)}
			step++
			return directImplementationShortcut{}
		}
		result := mgr.Execute(ctx, req)
		m.logToolResult(agent.ToolResultEvent{Step: step, Agent: "implement", Result: result})
		m.events <- toolTraceMsg{text: workspace.FormatToolTrace(result)}
		if writeToolResultChanged(req, result) && shouldInvalidateExploreCacheForTool(result) {
			m.events <- exploreCacheInvalidatedMsg{
				reason: fmt.Sprintf("workspace changed via %s; clearing cached explore profile", result.Tool),
			}
		}
		step++
		if result.Error != "" {
			return directImplementationShortcut{}
		}
		if writeToolResultChanged(req, result) {
			changedPaths = append(changedPaths, req.Path)
			if strings.TrimSpace(op.Reason) != "" {
				appliedSummaries = append(appliedSummaries, fmt.Sprintf("%s (%s)", req.Path, strings.TrimSpace(op.Reason)))
			} else {
				appliedSummaries = append(appliedSummaries, req.Path)
			}
		}
	}
	if len(changedPaths) == 0 {
		m.logMessage("implement_focused_plan", "focused structured plan produced only no-op edits")
		return directImplementationShortcut{}
	}
	summary := strings.TrimSpace(plan.Summary)
	if summary == "" {
		summary = fmt.Sprintf("Applied a focused structured edit plan to %s lines %d-%d.", path, windowStart, windowEnd)
	}
	if len(appliedSummaries) > 0 {
		summary = strings.TrimSpace(summary + " Operations: " + strings.Join(appliedSummaries, "; ") + ".")
	}
	return directImplementationShortcut{
		applied:      true,
		changedPaths: uniqueNonEmptyStrings(changedPaths),
		summary:      summary,
		nextStep:     step,
	}
}

func (m *Model) proposeStructuredExecutionPlan(
	ctx context.Context,
	mgr *workspace.Manager,
	history []llama.ChatMessage,
	opts llama.ChatOptions,
	prompt string,
	packet implementationTargetPacket,
	feedback string,
) (structuredExecutionPlan, string, error) {
	planMessages := append([]llama.ChatMessage(nil), history...)
	workspaceInstructions := ""
	if mgr != nil {
		workspaceInstructions = mgr.Instructions()
	} else if m.workspace != nil {
		workspaceInstructions = m.workspace.Instructions()
	}
	planMessages = replaceSystemPrompt(planMessages, buildImplementSystemPrompt(m.promptStack.Text, workspaceInstructions))
	planMessages = append(planMessages, llama.ChatMessage{
		Role: "system",
		Content: "You are in STRUCTURED EXECUTION PLAN mode.\n" +
			"Do not request tools. Reply with ONLY one JSON object.\n" +
			"Schema:\n" +
			"{\"summary\":\"...\",\"operations\":[{\"tool\":\"edit_file\",\"path\":\"...\",\"start_line\":123,\"end_line\":123,\"content\":\"...\",\"reason\":\"...\"}]}\n" +
			"Rules:\n" + structuredExecutionPlanRules(packet) + "\n",
	})
	planMessages = append(planMessages, llama.ChatMessage{
		Role:    "user",
		Content: "Propose a bounded structured execution plan for this request.\n\nRequest:\n" + strings.TrimSpace(prompt) + "\n\nTarget packet:\n" + renderImplementationTargetPacket(packet),
	})
	if strings.TrimSpace(feedback) != "" {
		planMessages = append(planMessages, llama.ChatMessage{
			Role:    "system",
			Content: "Previous proposal feedback:\n" + strings.TrimSpace(feedback),
		})
	}
	planOpts := opts
	planOpts.Temperature = 0.2
	planOpts.TopP = 0.9
	planOpts.PreserveThinking = false
	thinking := false
	planOpts.EnableThinking = &thinking
	limitShortcutTokens(&planOpts)
	callCtx, cancel := context.WithTimeout(ctx, implementationShortcutModelTimeout)
	defer cancel()
	reply, err := m.client.CompleteChat(callCtx, planMessages, planOpts)
	if err != nil {
		return structuredExecutionPlan{}, reply, err
	}
	plan, err := parseStructuredExecutionPlan(reply)
	return plan, reply, err
}

func (m *Model) proposeFocusedStructuredExecutionPlan(
	ctx context.Context,
	mgr *workspace.Manager,
	prompt string,
	opts llama.ChatOptions,
	packet implementationTargetPacket,
	path string,
	windowStart int,
	windowEnd int,
	snippet string,
	feedback string,
) (structuredExecutionPlan, string, error) {
	workspaceInstructions := ""
	if mgr != nil {
		workspaceInstructions = mgr.Instructions()
	} else if m.workspace != nil {
		workspaceInstructions = m.workspace.Instructions()
	}
	messages := []llama.ChatMessage{{
		Role:    "system",
		Content: buildImplementSystemPrompt(m.promptStack.Text, workspaceInstructions),
	}, {
		Role: "system",
		Content: "You are in FOCUSED STRUCTURED EDIT mode.\n" +
			"Do not request tools. Reply with ONLY one JSON object.\n" +
			"Schema:\n" +
			"{\"summary\":\"...\",\"operations\":[{\"tool\":\"edit_file\",\"path\":\"" + path + "\",\"start_line\":" + strconv.Itoa(windowStart) + ",\"end_line\":" + strconv.Itoa(windowEnd) + ",\"content\":\"...\",\"reason\":\"...\"}]}\n" +
			"Rules:\n" + focusedStructuredExecutionPlanRules(packet, path, windowStart, windowEnd) + "\n",
	}, {
		Role: "user",
		Content: "Request:\n" + strings.TrimSpace(prompt) +
			"\n\nTarget packet:\n" + renderImplementationTargetPacket(packet) +
			fmt.Sprintf("\n\nFile window %s lines %d-%d:\n%s", path, windowStart, windowEnd, snippet),
	}}
	if strings.TrimSpace(feedback) != "" {
		messages = append(messages, llama.ChatMessage{
			Role:    "system",
			Content: "Previous proposal feedback:\n" + strings.TrimSpace(feedback),
		})
	}
	planOpts := opts
	planOpts.Temperature = 0.15
	planOpts.TopP = 0.85
	planOpts.PreserveThinking = false
	thinking := false
	planOpts.EnableThinking = &thinking
	limitShortcutTokens(&planOpts)
	callCtx, cancel := context.WithTimeout(ctx, implementationShortcutModelTimeout)
	defer cancel()
	reply, err := m.client.CompleteChat(callCtx, messages, planOpts)
	if err != nil {
		return structuredExecutionPlan{}, reply, err
	}
	plan, err := parseStructuredExecutionPlan(reply)
	return plan, reply, err
}

func (m *Model) proposeFocusedWindowRewrite(
	ctx context.Context,
	mgr *workspace.Manager,
	prompt string,
	opts llama.ChatOptions,
	packet implementationTargetPacket,
	path string,
	startLine int,
	endLine int,
	snippet string,
	forceChange bool,
) (string, error) {
	workspaceInstructions := ""
	if mgr != nil {
		workspaceInstructions = mgr.Instructions()
	} else if m.workspace != nil {
		workspaceInstructions = m.workspace.Instructions()
	}
	messages := []llama.ChatMessage{{
		Role:    "system",
		Content: buildImplementSystemPrompt(m.promptStack.Text, workspaceInstructions),
	}, {
		Role: "system",
		Content: "You are in FOCUSED WINDOW REWRITE mode.\n" +
			"Do not request tools.\n" +
			"Reply with ONLY the replacement code for the specified line window.\n" +
			"Do not include markdown fences, commentary, or line numbers.\n" +
			"Keep the edit minimal and preserve surrounding behavior outside the requested change.\n" +
			"Preserve indentation and tab structure exactly.\n" +
			"Make sure the rewritten window remains syntactically valid when spliced back into the file.\n" +
			"Do not drop continued expressions, trailing operators, or closing delimiters that belong to the current block.\n",
	}, {
		Role: "user",
		Content: "Request:\n" + strings.TrimSpace(prompt) +
			"\n\nTarget packet:\n" + renderImplementationTargetPacket(packet) +
			fmt.Sprintf("\n\nRewrite only this file window from %s lines %d-%d:\n%s", path, startLine, endLine, snippet),
	}}
	if forceChange {
		messages = append(messages, llama.ChatMessage{
			Role: "system",
			Content: "Your previous attempt did not make a usable edit.\n" +
				"You must return a different replacement window than the current snippet.\n" +
				"Make the smallest possible valid change that satisfies the request.\n",
		})
	}
	rewriteOpts := opts
	rewriteOpts.Temperature = 0.2
	rewriteOpts.TopP = 0.9
	rewriteOpts.PreserveThinking = false
	thinking := false
	rewriteOpts.EnableThinking = &thinking
	limitShortcutTokens(&rewriteOpts)
	callCtx, cancel := context.WithTimeout(ctx, implementationShortcutModelTimeout)
	defer cancel()
	reply, err := m.client.CompleteChat(callCtx, messages, rewriteOpts)
	if err != nil {
		return "", err
	}
	return reply, nil
}

func limitShortcutTokens(opts *llama.ChatOptions) {
	if opts == nil {
		return
	}
	const maxShortcutTokens = 2048
	if opts.MaxTokens <= 0 || opts.MaxTokens > maxShortcutTokens {
		opts.MaxTokens = maxShortcutTokens
	}
}

func isShortcutContextError(err error) bool {
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)
}

func parseStructuredExecutionPlan(reply string) (structuredExecutionPlan, error) {
	payload := strings.TrimSpace(reply)
	if strings.HasPrefix(payload, "```") {
		start := strings.Index(payload, "\n")
		end := strings.LastIndex(payload, "```")
		if start > 0 && end > start {
			payload = strings.TrimSpace(payload[start:end])
		}
	}
	if object, ok := extractFirstJSONObjectForPlan(payload); ok {
		payload = object
	}
	var plan structuredExecutionPlan
	if err := json.Unmarshal([]byte(payload), &plan); err != nil {
		return structuredExecutionPlan{}, err
	}
	if len(plan.Operations) > 3 {
		plan.Operations = nil
	}
	return plan, nil
}

func focusedRewriteWindow(packet implementationTargetPacket, bootstrap implementationBootstrap) (string, int, int, string, bool) {
	for _, result := range bootstrap.results {
		if result.Tool != "read_file" || result.Error != "" {
			continue
		}
		path, startLine, endLine, ok := parseReadWindowSummary(result.Summary)
		if !ok || !focusedRewritePathAllowed(packet, path) {
			continue
		}
		if endLine < startLine || endLine-startLine > 220 {
			continue
		}
		snippet := cleanReadWindowSnippet(result.Output)
		if strings.TrimSpace(snippet) == "" {
			continue
		}
		return path, startLine, endLine, snippet, true
	}
	return "", 0, 0, "", false
}

func focusedRewritePathAllowed(packet implementationTargetPacket, path string) bool {
	path = normalizeGuardedPath(path)
	if path == "" {
		return false
	}
	for _, candidate := range packet.PrimaryPaths {
		if normalizeGuardedPath(candidate) == path {
			return true
		}
	}
	return false
}

func cleanFocusedRewriteReply(reply string) string {
	reply = strings.TrimSpace(reply)
	if strings.HasPrefix(reply, "```") {
		start := strings.Index(reply, "\n")
		end := strings.LastIndex(reply, "```")
		if start > 0 && end > start {
			reply = strings.TrimSpace(reply[start:end])
		}
	}
	if strings.HasPrefix(reply, "tool") {
		return ""
	}
	return strings.TrimSpace(reply)
}

func preflightStructuredExecutionRequest(mgr *workspace.Manager, req workspace.ToolRequest) error {
	if mgr == nil || req.Tool != "edit_file" {
		return nil
	}
	path, err := preflightResolvePath(mgr, req.Path, req.Workdir)
	if err != nil {
		return err
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	updated, err := applyStructuredExecutionEdit(string(content), req)
	if err != nil {
		return err
	}
	if updated == string(content) {
		return nil
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		fset := token.NewFileSet()
		if _, err := parser.ParseFile(fset, path, updated, parser.AllErrors); err != nil {
			return fmt.Errorf("preflight syntax validation failed for %s: %w", req.Path, err)
		}
	case ".json":
		if !json.Valid([]byte(updated)) {
			return fmt.Errorf("preflight JSON validation failed for %s", req.Path)
		}
	default:
		return nil
	}
	return nil
}

func preflightStructuredExecutionPlan(mgr *workspace.Manager, reqs []workspace.ToolRequest) error {
	if mgr == nil || len(reqs) == 0 {
		return nil
	}
	order := make([]string, 0, len(reqs))
	byPath := make(map[string][]workspace.ToolRequest)
	for _, req := range reqs {
		path, err := preflightResolvePath(mgr, req.Path, req.Workdir)
		if err != nil {
			return err
		}
		if _, ok := byPath[path]; !ok {
			order = append(order, path)
		}
		reqCopy := req
		reqCopy.Path = path
		byPath[path] = append(byPath[path], reqCopy)
	}
	for _, path := range order {
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		updated := string(content)
		for _, req := range byPath[path] {
			next, err := applyStructuredExecutionEdit(updated, req)
			if err != nil {
				return err
			}
			updated = next
		}
		if updated == string(content) {
			continue
		}
		switch strings.ToLower(filepath.Ext(path)) {
		case ".go":
			fset := token.NewFileSet()
			if _, err := parser.ParseFile(fset, path, updated, parser.AllErrors); err != nil {
				return fmt.Errorf("preflight syntax validation failed for %s: %w", path, err)
			}
		case ".json":
			if !json.Valid([]byte(updated)) {
				return fmt.Errorf("preflight JSON validation failed for %s", path)
			}
		}
	}
	return nil
}

func validateStructuredExecutionEdit(packet implementationTargetPacket, req workspace.ToolRequest) *workspace.ToolResult {
	path := normalizeGuardedPath(req.Path)
	if path == "" {
		return &workspace.ToolResult{Tool: req.Tool, Summary: "Structured execution plan rejected", Error: "structured execution plan requires a concrete path"}
	}
	allowed := false
	for _, candidate := range packet.PrimaryPaths {
		if normalizeGuardedPath(candidate) == path {
			allowed = true
			break
		}
	}
	if !allowed {
		return &workspace.ToolResult{Tool: req.Tool, Summary: "Structured execution plan rejected", Error: fmt.Sprintf("structured execution plan may only edit target packet primary files, not %s", path)}
	}
	switch req.Tool {
	case "edit_file":
		if strings.TrimSpace(req.OldText) == "" && (req.StartLine <= 0 || req.EndLine < req.StartLine) {
			return &workspace.ToolResult{Tool: req.Tool, Summary: "Structured execution plan rejected", Error: "edit_file requires old_text or a valid start_line/end_line range"}
		}
	case "write_file", "append_file":
		if strings.TrimSpace(req.Content) == "" {
			return &workspace.ToolResult{Tool: req.Tool, Summary: "Structured execution plan rejected", Error: req.Tool + " requires content"}
		}
	default:
		return &workspace.ToolResult{Tool: req.Tool, Summary: "Structured execution plan rejected", Error: fmt.Sprintf("unsupported structured execution tool %q", req.Tool)}
	}
	return nil
}

func validateStructuredExecutionPlan(packet implementationTargetPacket, plan structuredExecutionPlan) error {
	if len(plan.Operations) == 0 {
		return nil
	}
	if implementationTargetPacketNeedsStructuralPolicy(packet) {
		if len(plan.Operations) > 2 {
			return fmt.Errorf("structural refactor plans may propose at most 2 operations")
		}
		touchedPaths := make(map[string]struct{})
		for _, op := range plan.Operations {
			path := normalizeGuardedPath(op.Path)
			if path != "" {
				touchedPaths[path] = struct{}{}
			}
			if !structuredReasonLooksStructural(op.Reason, packet.StrategyHints) {
				return fmt.Errorf("structural refactor plans require an operation reason grounded in the boundary or responsibility change")
			}
		}
		if len(touchedPaths) > 1 && !structuralPlanAllowsMultiFileStage(packet, plan) {
			return fmt.Errorf("structural refactor first stage should edit one primary file before widening")
		}
	}
	return nil
}

func implementationTargetPacketNeedsStructuralPolicy(packet implementationTargetPacket) bool {
	return frontier.TargetPacketNeedsStructuralPolicy(packet)
}

func structuralRefactorStageHint(packet implementationTargetPacket) string {
	return frontier.StructuralStageHint(packet)
}

func buildStructuralRefactorStage(packet implementationTargetPacket, changedPaths []string, verify implementationVerification) *structuralRefactorStage {
	if !implementationTargetPacketNeedsStructuralPolicy(packet) {
		return nil
	}
	changed := uniqueNormalizedPaths(changedPaths)
	if len(changed) == 0 {
		return nil
	}
	primary := uniqueNormalizedPaths(packet.PrimaryPaths)
	changedSet := make(map[string]struct{}, len(changed))
	for _, path := range changed {
		changedSet[path] = struct{}{}
	}
	remaining := make([]string, 0, len(primary))
	for _, path := range primary {
		if _, ok := changedSet[path]; ok {
			continue
		}
		remaining = append(remaining, path)
	}
	focus := changed[0]
	if len(primary) > 0 {
		if _, ok := changedSet[primary[0]]; ok {
			focus = primary[0]
		}
	}
	coverage := strings.TrimSpace(verify.coverage)
	confidence := strings.TrimSpace(verify.confidence)
	verification := ""
	switch {
	case verify.failed:
		verification = "failed"
	case confidence != "" && coverage != "":
		verification = confidence + " (" + coverage + ")"
	case confidence != "":
		verification = confidence
	case coverage != "":
		verification = coverage
	}
	next := structuralRefactorNextStep(focus, remaining, packet.StrategyHints, verify)
	stageNumber := packet.RefactorStage
	if stageNumber <= 0 {
		stageNumber = 1
	}
	return &structuralRefactorStage{
		Active:       true,
		Stage:        stageNumber,
		SourceLane:   strings.TrimSpace(packet.SourceLane),
		FocusPath:    focus,
		Changed:      changed,
		Remaining:    remaining,
		Strategy:     append([]string(nil), packet.StrategyHints...),
		NextStep:     next,
		Verification: verification,
	}
}

func validateFocusedStructuredExecutionEdit(path string, windowStart int, windowEnd int, snippet string, req workspace.ToolRequest) *workspace.ToolResult {
	path = normalizeGuardedPath(path)
	if path == "" {
		return &workspace.ToolResult{Tool: req.Tool, Summary: "Focused structured edit rejected", Error: "focused structured edit requires a concrete path"}
	}
	req.Path = normalizeGuardedPath(req.Path)
	if req.Path != path {
		return &workspace.ToolResult{Tool: req.Tool, Summary: "Focused structured edit rejected", Error: fmt.Sprintf("focused structured edit may only target %s, not %s", path, req.Path)}
	}
	if req.Tool != "edit_file" {
		return &workspace.ToolResult{Tool: req.Tool, Summary: "Focused structured edit rejected", Error: fmt.Sprintf("focused structured edit only supports edit_file, not %q", req.Tool)}
	}
	if strings.TrimSpace(req.OldText) != "" {
		if !strings.Contains(snippet, req.OldText) {
			return &workspace.ToolResult{Tool: req.Tool, Summary: "Focused structured edit rejected", Error: "focused structured edit old_text must appear in the bootstrapped window"}
		}
		if req.OldText == req.NewText {
			return &workspace.ToolResult{Tool: req.Tool, Summary: "Focused structured edit rejected", Error: "focused structured edit old_text/new_text pair is a no-op"}
		}
		return nil
	}
	if req.StartLine <= 0 || req.EndLine < req.StartLine {
		return &workspace.ToolResult{Tool: req.Tool, Summary: "Focused structured edit rejected", Error: "focused structured edit requires old_text/new_text or a valid start_line/end_line range"}
	}
	if req.StartLine < windowStart || req.EndLine > windowEnd {
		return &workspace.ToolResult{Tool: req.Tool, Summary: "Focused structured edit rejected", Error: fmt.Sprintf("focused structured edit must stay within lines %d-%d", windowStart, windowEnd)}
	}
	if req.StartLine == windowStart && req.EndLine == windowEnd && req.Content == snippet {
		return &workspace.ToolResult{Tool: req.Tool, Summary: "Focused structured edit rejected", Error: "focused structured edit line-range content is identical to the current window"}
	}
	return nil
}

func marshalStructuredExecutionRequest(req workspace.ToolRequest) string {
	payload, err := json.Marshal(req)
	if err != nil {
		return fmt.Sprintf("{\"tool\":%q,\"path\":%q}", req.Tool, req.Path)
	}
	return string(payload)
}

func applyStructuredExecutionEdit(current string, req workspace.ToolRequest) (string, error) {
	if req.StartLine > 0 || req.EndLine > 0 {
		startLine := req.StartLine
		endLine := req.EndLine
		if startLine <= 0 {
			return "", fmt.Errorf("start_line must be greater than 0")
		}
		if endLine == 0 {
			endLine = startLine
		}
		if endLine < startLine {
			return "", fmt.Errorf("end_line must be greater than or equal to start_line")
		}
		spans := preflightLineSpans(current)
		if len(spans) == 0 {
			return "", fmt.Errorf("cannot apply a line edit to an empty file")
		}
		if startLine > len(spans) || endLine > len(spans) {
			return "", fmt.Errorf("requested lines %d-%d are outside the file (file has %d lines)", startLine, endLine, len(spans))
		}
		startOffset := spans[startLine-1].start
		endOffset := spans[endLine-1].end
		return current[:startOffset] + req.Content + current[endOffset:], nil
	}
	if strings.TrimSpace(req.OldText) == "" {
		return "", fmt.Errorf("old_text is required unless you provide start_line/end_line/content")
	}
	matchCount := strings.Count(current, req.OldText)
	if matchCount == 0 {
		return "", fmt.Errorf("old_text did not appear in %s", req.Path)
	}
	if matchCount > 1 && !req.ReplaceAll {
		return "", fmt.Errorf("old_text matched %d locations in %s; provide a more specific old_text or set replace_all=true", matchCount, req.Path)
	}
	if req.ReplaceAll {
		return strings.ReplaceAll(current, req.OldText, req.NewText), nil
	}
	return strings.Replace(current, req.OldText, req.NewText, 1), nil
}

func buildImplementSystemPrompt(basePrompt string, workspaceInstructions string) string {
	return frontier.BuildImplementSystemPrompt(basePrompt, workspaceInstructions)
}

func buildImplementTaskPrompt(prompt string) string {
	return frontier.BuildImplementTaskPrompt(prompt)
}

func implementationPromptForbidsDeclarationMoves(prompt string) bool {
	return frontier.PromptForbidsDeclarationMoves(prompt)
}

func structuredExecutionPlanRules(packet implementationTargetPacket) string {
	return frontier.StructuredExecutionPlanRules(packet)
}

func focusedStructuredExecutionPlanRules(packet implementationTargetPacket, path string, windowStart int, windowEnd int) string {
	return frontier.FocusedStructuredExecutionPlanRules(packet, path, windowStart, windowEnd)
}

func buildImplementRepairPrompt(prompt string) string {
	return frontier.BuildImplementRepairPrompt(prompt)
}

func implementToolBudget(maxToolSteps int) int {
	return frontier.ImplementToolBudget(maxToolSteps)
}

func implementToolBudgetForRequest(maxToolSteps int, prompt string, packet implementationTargetPacket) int {
	return frontier.ImplementToolBudgetForRequest(maxToolSteps, prompt, packet)
}

func implementRunPolicyForRequest(maxToolSteps int, prompt string, packet implementationTargetPacket) agent.RunPolicy {
	return frontier.ImplementRunPolicyForRequest(maxToolSteps, prompt, packet)
}

func implementationCompletionGate(prompt string, preChangedPaths []string, requests map[int]agent.ToolRequestEvent, results map[int]agent.ToolResultEvent) agent.CompletionGate {
	return frontier.ImplementationCompletionGate(prompt, func() []string {
		return uniqueNonEmptyStrings(append(append([]string(nil), preChangedPaths...), changedPathsFromImplementEvents(requests, results)...))
	})
}

func implementationFinalReplyHasTerminalReason(reply string) bool {
	return frontier.ImplementationFinalReplyHasTerminalReason(reply)
}

func successfulToolResult(results []workspace.ToolResult, tool string) bool {
	return frontier.SuccessfulToolResult(results, tool)
}

func requestNeedsExpandedImplementBudget(prompt string, packet implementationTargetPacket) bool {
	return frontier.RequestNeedsExpandedImplementBudget(prompt, packet)
}

func explicitGoMoveBatchHintCount(prompt string) int {
	return frontier.ExplicitGoMoveBatchHintCount(prompt)
}

type verificationFeedbackSnapshot struct {
	RiskLevel             string   `json:"risk_level,omitempty"`
	Coverage              string   `json:"coverage,omitempty"`
	Confidence            string   `json:"confidence,omitempty"`
	Commands              []string `json:"commands,omitempty"`
	FailedCommands        []string `json:"failed_commands,omitempty"`
	FailureClasses        []string `json:"failure_classes,omitempty"`
	LearnedRecommendation string   `json:"learned_recommendation,omitempty"`
}

func (m *Model) logVerificationFeedback(feedback verificationFeedbackSnapshot) {
	if m == nil || m.debugLogger == nil {
		return
	}
	data, err := json.Marshal(feedback)
	if err != nil {
		return
	}
	m.logMessage("verification_feedback", string(data))
}

func formatStructuredExecutionPlanSteps(plan structuredExecutionPlan) []string {
	steps := make([]string, 0, len(plan.Operations))
	for _, op := range plan.Operations {
		path := normalizeGuardedPath(op.Path)
		if path == "" {
			path = "target file"
		}
		action := strings.TrimSpace(op.Tool)
		if action == "" {
			action = "edit_file"
		}
		line := action + " " + path
		if op.StartLine > 0 && op.EndLine >= op.StartLine {
			line += fmt.Sprintf(" lines %d-%d", op.StartLine, op.EndLine)
		}
		if reason := strings.TrimSpace(op.Reason); reason != "" {
			line += " (" + reason + ")"
		}
		steps = append(steps, line)
	}
	return uniqueNonEmptyStrings(steps)
}

func (m *Model) setExecutionVerificationPlan(plan verificationPlan) {
	m.executionPanel.Verification = verificationPlan{
		Commands:         append([]string(nil), plan.Commands...),
		Reasons:          append([]string(nil), plan.Reasons...),
		UsesFullSuite:    plan.UsesFullSuite,
		Coverage:         strings.TrimSpace(plan.Coverage),
		Confidence:       strings.TrimSpace(plan.Confidence),
		RiskLevel:        strings.TrimSpace(plan.RiskLevel),
		ExpectedFailures: append([]string(nil), plan.ExpectedFailures...),
		RepairPolicy:     strings.TrimSpace(plan.RepairPolicy),
	}
	m.executionPanel.UpdatedAt = time.Now()
	m.logVerificationPlan(plan)
}

func (m *Model) logVerificationPlan(plan verificationPlan) {
	if m.debugLogger == nil {
		return
	}
	payload := struct {
		Commands         []string `json:"commands,omitempty"`
		Reasons          []string `json:"reasons,omitempty"`
		UsesFullSuite    bool     `json:"uses_full_suite,omitempty"`
		Coverage         string   `json:"coverage,omitempty"`
		Confidence       string   `json:"confidence,omitempty"`
		RiskLevel        string   `json:"risk_level,omitempty"`
		ExpectedFailures []string `json:"expected_failures,omitempty"`
		RepairPolicy     string   `json:"repair_policy,omitempty"`
	}{
		Commands:         append([]string(nil), plan.Commands...),
		Reasons:          append([]string(nil), plan.Reasons...),
		UsesFullSuite:    plan.UsesFullSuite,
		Coverage:         strings.TrimSpace(plan.Coverage),
		Confidence:       strings.TrimSpace(plan.Confidence),
		RiskLevel:        strings.TrimSpace(plan.RiskLevel),
		ExpectedFailures: append([]string(nil), plan.ExpectedFailures...),
		RepairPolicy:     strings.TrimSpace(plan.RepairPolicy),
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	m.logMessage("verification_plan", string(data))
}

func promptRequestsAnalysisBeforeImplementation(normalized string) bool {
	return frontier.PromptRequestsAnalysisBeforeImplementation(normalized)
}

func promptRequestsImplementation(prompt string) bool {
	return frontier.PromptRequestsImplementation(prompt)
}

func looksLikeDirectImplementationPrompt(prompt string, normalized string) bool {
	return frontier.LooksLikeDirectImplementationPrompt(prompt, normalized)
}

func looksLikePlanOnlyPrompt(normalized string) bool {
	return frontier.LooksLikePlanOnlyPrompt(normalized)
}

func looksLikeReadOnlyBoundaryPrompt(normalized string) bool {
	return frontier.LooksLikeReadOnlyBoundaryPrompt(normalized)
}
