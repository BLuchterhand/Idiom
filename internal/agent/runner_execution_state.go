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
	pendingSearchOutput              string
	pendingDiscoveryConsumption      bool
	pendingDiscoveryTool             string
	pendingDiscoverySummary          string
	pendingDiscoveryOutput           string
	pendingDiscoveryCandidates       []string
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

func (s *runnerExecutionState) shouldRequireVerification(req workspace.ToolRequest, brief workerruntime.WorkerRuntimeBrief) bool {
	if s == nil {
		return false
	}
	if plannerOwnsVerification(brief) || verificationContractSkipsGate(brief.VerificationContract) {
		return false
	}
	if workspace.IsWorkspaceWriteTool(req.Tool) {
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
		// Any verification attempt closes the "run verification before more
		// inspection" guard. A failed verifier opens a repair path instead;
		// completion readiness is still enforced from ToolResult evidence.
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
	sourceBodyConsumed := concreteSourceEvidenceResult(result) || (toolResultMadeProgress(req, result) && isWriteTool(req))
	if successfulSearchDiscoveryResult(req, result) {
		s.pendingSearchInspection = true
		s.pendingSearchTool = strings.TrimSpace(req.Tool)
		s.pendingSearchSummary = truncForRetryReason(result.Summary)
		s.pendingSearchOutput = truncForRetryReason(result.Output)
	} else if sourceBodyConsumed {
		s.pendingSearchInspection = false
		s.pendingSearchTool = ""
		s.pendingSearchSummary = ""
		s.pendingSearchOutput = ""
	}
	if successfulDiscoveryConsumptionResult(req, result) {
		s.pendingDiscoveryConsumption = true
		s.pendingDiscoveryTool = strings.TrimSpace(req.Tool)
		s.pendingDiscoverySummary = truncForRetryReason(result.Summary)
		s.pendingDiscoveryOutput = truncForRetryReason(result.Output)
		s.pendingDiscoveryCandidates = discoveryConsumptionCandidatesFromResult(result, 6)
	} else if sourceBodyConsumed {
		s.clearPendingDiscoveryConsumption()
	}
	if requestBudget != nil {
		if currentFingerprint != "" && strings.TrimSpace(result.Error) == "" && workerruntime.IsZeroHitSearchResult(result) {
			requestBudget.fingerprint.zeroHit[currentFingerprint] = truncForRetryReason(result.Summary)
		}
		if currentFingerprint != "" && strings.TrimSpace(result.Error) != "" && strings.Contains(strings.ToLower(result.Summary), "rejected") {
			requestBudget.fingerprint.rejected[currentFingerprint] = truncForRetryReason(result.Summary)
		}
	}
	return update
}

func (s *runnerExecutionState) clearPendingDiscoveryConsumption() {
	if s == nil {
		return
	}
	s.pendingDiscoveryConsumption = false
	s.pendingDiscoveryTool = ""
	s.pendingDiscoverySummary = ""
	s.pendingDiscoveryOutput = ""
	s.pendingDiscoveryCandidates = nil
}

func verificationPhaseState(result workspace.ToolResult) (state string, detail string) {
	detail = strings.TrimSpace(result.Summary)
	if strings.TrimSpace(result.Error) != "" {
		state = "failed"
		detail = strings.TrimSpace(result.Error)
	} else if verificationResultIsPatchHygieneOnly(result) {
		state = "hygiene"
		if detail == "" {
			detail = "patch hygiene completed"
		} else {
			detail += "; patch hygiene completed"
		}
	} else if resultCountsAsSuccessfulVerification(result) {
		state = "done"
	} else {
		state = "insufficient"
		if detail == "" {
			detail = "verification evidence was insufficient"
		} else {
			detail += "; verification evidence was insufficient"
		}
	}
	if strings.TrimSpace(detail) == "" {
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

func successfulDiscoveryConsumptionResult(req workspace.ToolRequest, result workspace.ToolResult) bool {
	if strings.TrimSpace(result.Error) != "" || workerruntime.IsZeroHitSearchResult(result) {
		return false
	}
	if concreteSourceEvidenceResult(result) {
		return false
	}
	if strings.TrimSpace(result.Output) == "" && strings.TrimSpace(result.Summary) == "" {
		return false
	}
	switch strings.TrimSpace(req.Tool) {
	case "context_pack":
		return contextPackResultHasTargets(result)
	case "search_files", "search_content", "rg", "grep",
		"go_workspace_symbol", "cpp_workspace_symbol", "python_workspace_symbol", "ts_workspace_symbol", "rust_workspace_symbol", "java_workspace_symbol",
		"structural_search", "discovery_query", "context_query", "context_zoom", "context_impact":
		return len(discoveryConsumptionCandidatesFromResult(result, 1)) > 0
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

func (s *runnerExecutionState) shouldConsumeDiscoveryBeforeMoreBroadDiscovery(req workspace.ToolRequest, brief workerruntime.WorkerRuntimeBrief) bool {
	if s == nil || !s.pendingDiscoveryConsumption {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(brief.TaskFamily), "conversation") ||
		!strings.EqualFold(strings.TrimSpace(brief.Contract), "direct_answer") ||
		runnerBriefRequiresMutation(brief) ||
		runnerVerificationRequired(brief) {
		return false
	}
	return conversationBroadDiscoveryRequest(req)
}

func (s *runnerExecutionState) discoveryConsumptionError() string {
	base := "a previous discovery result already returned concrete candidates. Before more broad discovery, inspect one returned handle or path with source-body evidence, answer from the evidence already gathered, or state the exact blocker/missing claim."
	if s == nil {
		return base
	}
	var parts []string
	if len(s.pendingDiscoveryCandidates) > 0 {
		parts = append(parts, "Candidate(s): "+strings.Join(s.pendingDiscoveryCandidates, ", "))
	}
	if summary := strings.TrimSpace(s.pendingDiscoverySummary); summary != "" {
		parts = append(parts, "Last useful result: "+summary)
	}
	if output := strings.TrimSpace(s.pendingDiscoveryOutput); output != "" && len(s.pendingDiscoveryCandidates) == 0 {
		parts = append(parts, "Returned evidence: "+output)
	}
	if len(parts) == 0 {
		return base
	}
	if tool := strings.TrimSpace(s.pendingDiscoveryTool); tool != "" {
		return fmt.Sprintf("%s The last useful %s result was: %s", base, tool, strings.Join(parts, ". "))
	}
	return fmt.Sprintf("%s %s", base, strings.Join(parts, ". "))
}

func (s *runnerExecutionState) searchHitConsumptionError() string {
	if s == nil {
		return "you already have a successful search result with concrete hits. Inspect one returned file or symbol now before doing another search."
	}
	base := "you already have a successful search result with concrete hits. Inspect one returned file or symbol now before doing another search."
	detail := strings.TrimSpace(s.pendingSearchSummary)
	if output := strings.TrimSpace(s.pendingSearchOutput); output != "" {
		if detail != "" {
			detail += ". "
		}
		detail += "Returned hit(s): " + output
	}
	if s.pendingSearchTool != "" && detail != "" {
		return fmt.Sprintf("%s The last useful %s result was: %s", base, s.pendingSearchTool, detail)
	}
	if detail != "" {
		return fmt.Sprintf("%s The last useful result was: %s", base, detail)
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
