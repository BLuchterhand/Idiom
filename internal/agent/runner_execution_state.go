package agent

import (
	"encoding/json"
	"fmt"
	"strings"

	"idiom/internal/frontier/workerruntime"
	"idiom/internal/llama"
	"idiom/internal/workspace"
)

type runnerExecutionState struct {
	verificationAttempted            bool
	needsNarrowingAfterBroad         bool
	broadInspectionCount             int
	consecutiveListFiles             int
	pendingSearchInspection          bool
	pendingSearchTool                string
	pendingSearchSummary             string
	checkpointCreated                bool
	writesSinceVerification          int
	consecutiveMissingPathListErrors int
	consecutiveToolFailures          int
	lastFailedPathStem               string
	softFailureNudgeUsed             bool
}

type runnerExecutionUpdate struct {
	ResetVerificationGuards bool
	VerificationState       string
	VerificationDetail      string
}

func newRunnerExecutionState(initialNeedsNarrowing bool, initialBroadInspects int) runnerExecutionState {
	return runnerExecutionState{
		needsNarrowingAfterBroad: initialNeedsNarrowing,
		broadInspectionCount:     initialBroadInspects,
	}
}

func (s *runnerExecutionState) shouldRequireVerification(req workspace.ToolRequest) bool {
	if s == nil {
		return false
	}
	return s.writesSinceVerification >= 2 && !isVerificationRequest(req) && strings.TrimSpace(req.Tool) != "task_ledger"
}

func (s *runnerExecutionState) needsCheckpoint(policy CheckpointPolicy, req workspace.ToolRequest) bool {
	if s == nil {
		return false
	}
	return policy.Enabled && !s.checkpointCreated && isWriteTool(req)
}

func (s *runnerExecutionState) markCheckpointCreated() {
	if s == nil {
		return
	}
	s.checkpointCreated = true
}

func (s *runnerExecutionState) applyResult(req workspace.ToolRequest, result workspace.ToolResult, currentFingerprint string, mgr *workspace.Manager, requestBudget *runnerRequestBudgetState) runnerExecutionUpdate {
	if s == nil {
		return runnerExecutionUpdate{}
	}
	update := runnerExecutionUpdate{}
	if isVerificationRequest(req) {
		s.verificationAttempted = true
		s.writesSinceVerification = 0
		if requestBudget != nil {
			requestBudget.noteVerification()
		}
		update.ResetVerificationGuards = true
		update.VerificationState, update.VerificationDetail = verificationPhaseState(result)
	} else if toolResultMadeProgress(req, result) && isWriteTool(req) {
		s.writesSinceVerification++
		if requestBudget != nil {
			requestBudget.noteWriteProgress()
		}
	}
	switch {
	case workspace.IsBroadInspectionRequest(req):
		s.needsNarrowingAfterBroad = true
		s.broadInspectionCount++
	case mgr != nil && mgr.ShouldClearBroadInspectionLock(req, result):
		s.needsNarrowingAfterBroad = false
	}
	if strings.EqualFold(strings.TrimSpace(req.Tool), "list_files") {
		s.consecutiveListFiles++
	} else if isContentInspectionTool(req.Tool) {
		s.consecutiveListFiles = 0
	}
	switch {
	case successfulSearchDiscoveryResult(req, result):
		s.pendingSearchInspection = true
		s.pendingSearchTool = strings.TrimSpace(req.Tool)
		s.pendingSearchSummary = truncForRetryReason(result.Summary)
	case isConcreteInspectionTool(req.Tool) || (toolResultMadeProgress(req, result) && isWriteTool(req)):
		s.pendingSearchInspection = false
		s.pendingSearchTool = ""
		s.pendingSearchSummary = ""
	}
	if requestBudget != nil {
		if currentFingerprint != "" && strings.TrimSpace(result.Error) == "" && workerruntime.IsZeroHitSearchResult(result) {
			requestBudget.zeroHitFingerprints[currentFingerprint] = truncForRetryReason(result.Summary)
		}
		if currentFingerprint != "" && strings.TrimSpace(result.Error) != "" && strings.Contains(strings.ToLower(result.Summary), "rejected") {
			requestBudget.rejectedFingerprints[currentFingerprint] = truncForRetryReason(result.Summary)
		}
	}
	return update
}

func verificationPhaseState(result workspace.ToolResult) (state string, detail string) {
	state = "done"
	detail = strings.TrimSpace(result.Summary)
	if strings.TrimSpace(result.Error) != "" {
		state = "failed"
		detail = strings.TrimSpace(result.Error)
	}
	if detail == "" {
		detail = "verification completed"
	}
	return state, detail
}

func (s *runnerExecutionState) noteToolFailure(req workspace.ToolRequest, result workspace.ToolResult) int {
	if s == nil {
		return 0
	}
	if strings.TrimSpace(result.Error) == "" {
		s.consecutiveToolFailures = 0
		s.lastFailedPathStem = ""
		return 0
	}
	stem := toolFailurePathStem(req)
	if stem != "" && stem == s.lastFailedPathStem {
		s.consecutiveToolFailures++
	} else {
		s.consecutiveToolFailures = 1
		s.lastFailedPathStem = stem
	}
	return s.consecutiveToolFailures
}

func (s *runnerExecutionState) noteMissingPathListFailure(exploreMode bool, req workspace.ToolRequest, result workspace.ToolResult) int {
	if s == nil {
		return 0
	}
	if exploreMode && isMissingPathListFailure(req, result) {
		s.consecutiveMissingPathListErrors++
		return s.consecutiveMissingPathListErrors
	}
	s.consecutiveMissingPathListErrors = 0
	return 0
}

func successfulSearchDiscoveryResult(req workspace.ToolRequest, result workspace.ToolResult) bool {
	if strings.TrimSpace(result.Error) != "" || strings.TrimSpace(result.Output) == "" || workerruntime.IsZeroHitSearchResult(result) {
		return false
	}
	switch strings.TrimSpace(req.Tool) {
	case "search_files", "search_content", "rg", "grep",
		"go_workspace_symbol", "cpp_workspace_symbol", "python_workspace_symbol", "ts_workspace_symbol", "rust_workspace_symbol", "java_workspace_symbol":
		return true
	default:
		return false
	}
}

func (s *runnerExecutionState) shouldConsumeSearchHitBeforeMoreSearch(req workspace.ToolRequest) bool {
	if s == nil || !s.pendingSearchInspection {
		return false
	}
	switch strings.TrimSpace(req.Tool) {
	case "search_files", "search_content", "rg", "grep":
		return true
	default:
		return false
	}
}

func (s *runnerExecutionState) searchHitConsumptionError() string {
	if s == nil {
		return "you already have a successful search result with concrete hits. Inspect one returned file or symbol now before doing another search."
	}
	base := "you already have a successful search result with concrete hits. Inspect one returned file or symbol now before doing another search."
	if s.pendingSearchTool != "" && s.pendingSearchSummary != "" {
		return fmt.Sprintf("%s The last useful %s result was: %s", base, s.pendingSearchTool, s.pendingSearchSummary)
	}
	if s.pendingSearchSummary != "" {
		return fmt.Sprintf("%s The last useful result was: %s", base, s.pendingSearchSummary)
	}
	return base
}

func (r *Runner) emitVerificationPhaseEvent(state string, detail string) {
	if r == nil || r.OnEvent == nil || strings.TrimSpace(state) == "" {
		return
	}
	data, err := json.Marshal(map[string]string{
		"lane":   "implement",
		"phase":  "verify",
		"state":  state,
		"detail": detail,
	})
	if err != nil {
		return
	}
	r.emitEvent(RunEvent{Role: "phase", Content: string(data)})
}

func (r *Runner) attachInstructionsForTouchedPaths(turnMessages *[]llama.ChatMessage, req workspace.ToolRequest, result workspace.ToolResult) {
	if r == nil || r.InstructionAttacher == nil || turnMessages == nil || strings.TrimSpace(result.Error) != "" {
		return
	}
	for _, touched := range toolResultTouchedPaths(req, result) {
		if attachment := r.InstructionAttacher.OnFileTouch(touched); attachment != nil {
			content := attachment.Render()
			if content != "" {
				*turnMessages = append(*turnMessages, llama.ChatMessage{Role: "system", Content: content})
				r.emitEvent(RunEvent{Role: "instructions_attached", Content: content})
			}
		}
	}
}
