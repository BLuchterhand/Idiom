package intentloop

import (
	"fmt"
	"io"
	"strings"

	intentloopcontract "idiom/internal/contracts/intentloop"
	plancontract "idiom/internal/contracts/plan"
	"idiom/internal/evidence/telemetry"
	"idiom/internal/platform/config"
	"idiom/internal/workflows/planexecute"
)

const (
	ResumeBlockPromptMismatch            = "blocked_cycle_prompt_mismatch"
	ResumeBlockChildIdentityMismatch     = "blocked_child_identity_mismatch"
	ResumeBlockExternalScopedChange      = "blocked_external_scoped_change"
	ResumeBlockChildProgressNotResumable = "blocked_child_progress_not_resumable"
	ResumeBlockAssessmentFailed          = "blocked_resume_assessment_failed"
	ResumeBlockTerminalCompletionLoad    = "blocked_terminal_completion_load_failed"
)

type ResumeAssessmentInput struct {
	Root                     string
	State                    IntentLoopState
	CurrentChildIdentity     *ChildRunIdentity
	CurrentScopedFingerprint string
}

type ResumeAssessment struct {
	HasIncompleteCycle bool
	CycleIndex         int
	Cycle              CycleRecord
	CyclePrompt        string
	ChildResumeID      string
	ChildProgressPath  string
	CanResumeChild     bool
	Blocked            bool
	BlockReason        string
	BlockDetail        string
}

func AssessResume(input ResumeAssessmentInput) (ResumeAssessment, error) {
	assessment := ResumeAssessment{}
	cycleIndex, cycle, ok := IncompleteCycle(input.State)
	if !ok {
		return assessment, nil
	}

	assessment.HasIncompleteCycle = true
	assessment.CycleIndex = cycleIndex
	assessment.Cycle = cycle
	assessment.ChildResumeID = strings.TrimSpace(cycle.PlanExecuteSessionID)

	if reason, detail := resumeScopeBlock(input.State, input.CurrentScopedFingerprint); reason != "" {
		assessment.Blocked = true
		assessment.BlockReason = reason
		assessment.BlockDetail = detail
		return assessment, nil
	}

	if mismatch := ChildIdentityMismatch(cycle.ChildIdentity, input.CurrentChildIdentity); mismatch != "" {
		assessment.Blocked = true
		assessment.BlockReason = ResumeBlockChildIdentityMismatch
		assessment.BlockDetail = mismatch
		return assessment, nil
	}

	prompt, err := LoadCyclePrompt(input.Root, cycle.CyclePromptPath, cycle.CyclePromptSHA256)
	if err != nil {
		assessment.Blocked = true
		assessment.BlockReason = ResumeBlockPromptMismatch
		assessment.BlockDetail = err.Error()
		return assessment, nil
	}
	assessment.CyclePrompt = prompt

	if assessment.ChildResumeID == "" {
		return assessment, nil
	}

	progress, progressPath, found, err := planexecute.TryLoadProgressState(input.Root, assessment.ChildResumeID)
	if err != nil {
		return assessment, err
	}
	if !found {
		return assessment, nil
	}

	if progress.PromptSHA256 != "" && progress.PromptSHA256 != sha256Hex(prompt) {
		assessment.Blocked = true
		assessment.BlockReason = ResumeBlockPromptMismatch
		assessment.BlockDetail = fmt.Sprintf("child progress prompt hash mismatch for %s", progressPath)
		return assessment, nil
	}

	assessment.ChildProgressPath = progressPath
	assessment.CanResumeChild = progress.ReviewResumeReady()
	return assessment, nil
}

func resumeIncompleteCycle(cfg config.Config, opts IntentLoopOptions, promptRun planexecute.PromptRunner, rec *telemetry.Recorder, stdout io.Writer, stderr io.Writer, state *IntentLoopState) (IntentLoopResult, *plancontract.PlanExecuteTerminalCompletionRecord) {
	if state == nil {
		return IntentLoopResult{}, nil
	}

	commands := childVerificationCommands(*state, opts)
	identity := childIdentity(cfg, opts, state.Contract, commands)
	assessment, err := AssessResume(ResumeAssessmentInput{
		Root:                     cfg.Workspace.Root,
		State:                    *state,
		CurrentChildIdentity:     &identity,
		CurrentScopedFingerprint: scopedWorkspaceFingerprint(cfg.Workspace.Root, state.Contract.ScopeContract),
	})
	if err != nil {
		statePath, saveErr := blockIncompleteResumeCycle(cfg.Workspace.Root, state, assessment, ResumeBlockAssessmentFailed, err.Error())
		if saveErr != nil {
			return intentLoopJSONEventFailureResult(state.SessionID, statePath, fmt.Errorf("persist blocked intent loop state: %w", saveErr), stderr), nil
		}
		return resultFromState(*state, statePath, 1, nil, ""), nil
	}
	if !assessment.HasIncompleteCycle {
		return IntentLoopResult{}, nil
	}
	if assessment.Blocked {
		statePath, saveErr := blockIncompleteResumeCycle(cfg.Workspace.Root, state, assessment, assessment.BlockReason, assessment.BlockDetail)
		if saveErr != nil {
			return intentLoopJSONEventFailureResult(state.SessionID, statePath, fmt.Errorf("persist blocked intent loop state: %w", saveErr), stderr), nil
		}
		if err := emitIntentLoopJSONEvent(stdout, opts.PlanExecuteOptions.JSONEvents, intentloopcontract.EventBlocked, map[string]any{
			"session_id":       state.SessionID,
			"cycle_number":     assessment.Cycle.CycleNumber,
			"blocker_summary":  state.TerminationReason,
			"child_session_id": assessment.ChildResumeID,
		}); err != nil {
			return intentLoopJSONEventFailureResult(state.SessionID, statePath, err, stderr), nil
		}
		if stderr != nil {
			fmt.Fprintln(stderr, state.TerminationReason)
		}
		return resultFromState(*state, statePath, 1, nil, ""), nil
	}

	childOpts := childPlanExecuteOptions(opts, *state, assessment.CyclePrompt, assessment.ChildResumeID, commands)
	if assessment.CanResumeChild {
		childOpts.Resume = assessment.ChildResumeID
	}

	if assessment.ChildResumeID != "" {
		record, completionPath, found, err := planexecute.TryLoadPlanExecuteTerminalCompletionRecord(cfg.Workspace.Root, assessment.ChildResumeID)
		if err != nil {
			statePath, saveErr := blockIncompleteResumeCycle(cfg.Workspace.Root, state, assessment, ResumeBlockTerminalCompletionLoad, err.Error())
			if saveErr != nil {
				return intentLoopJSONEventFailureResult(state.SessionID, statePath, fmt.Errorf("persist blocked intent loop state: %w", saveErr), stderr), nil
			}
			return resultFromState(*state, statePath, 1, nil, completionPath), nil
		}
		if found {
			terminal := plancontract.NormalizePlanExecuteTerminalCompletionRecord(record)
			childResult := planexecute.RunResult{
				ExitCode:       terminal.ExitCode,
				SessionID:      assessment.ChildResumeID,
				FailureKind:    string(terminal.FailureKind),
				FailureReason:  terminal.FailureReason,
				Completion:     &terminal,
				CompletionPath: completionPath,
			}
			gaps := AnalyzeEvidenceGaps(state.Contract, terminal)
			finalizeCycleRecord(&state.Cycles[assessment.CycleIndex], childResult, terminal, completionPath, gaps, cfg.Workspace.Root)
			state.Cycles[assessment.CycleIndex].TokensUsed = estimatedCycleTokens(opts, assessment.CyclePrompt, childResult, terminal)
			state.TokensUsed += state.Cycles[assessment.CycleIndex].TokensUsed
			if err := emitIntentLoopJSONEvent(stdout, opts.PlanExecuteOptions.JSONEvents, intentloopcontract.EventCycleComplete, map[string]any{
				"session_id":      state.SessionID,
				"cycle_number":    state.Cycles[assessment.CycleIndex].CycleNumber,
				"remaining_gaps":  sanitizedGapSummaries(gaps),
				"gap_count":       len(gaps),
				"tokens_used":     state.TokensUsed,
				"child_exit_code": childResult.ExitCode,
			}); err != nil {
				return intentLoopJSONEventFailureResult(state.SessionID, "", err, stderr), nil
			}
			if result, terminalResult := foldCompletedCycle(cfg, opts, stdout, stderr, state, assessment.CycleIndex, &identity, &terminal, completionPath, gaps, childResult.ExitCode); terminalResult {
				return result, &terminal
			}
			return IntentLoopResult{}, &terminal
		}
	}
	if assessment.ChildProgressPath != "" && !assessment.CanResumeChild {
		statePath, saveErr := blockIncompleteResumeCycle(cfg.Workspace.Root, state, assessment, ResumeBlockChildProgressNotResumable, assessment.ChildProgressPath)
		if saveErr != nil {
			return intentLoopJSONEventFailureResult(state.SessionID, statePath, fmt.Errorf("persist blocked intent loop state: %w", saveErr), stderr), nil
		}
		if err := emitIntentLoopJSONEvent(stdout, opts.PlanExecuteOptions.JSONEvents, intentloopcontract.EventBlocked, map[string]any{
			"session_id":       state.SessionID,
			"cycle_number":     assessment.Cycle.CycleNumber,
			"blocker_summary":  state.TerminationReason,
			"child_session_id": assessment.ChildResumeID,
			"child_state_path": assessment.ChildProgressPath,
		}); err != nil {
			return intentLoopJSONEventFailureResult(state.SessionID, statePath, err, stderr), nil
		}
		if stderr != nil {
			fmt.Fprintln(stderr, state.TerminationReason)
		}
		return resultFromState(*state, statePath, 1, nil, ""), nil
	}

	childResult := opts.RunPlanExecute(cfg, childOpts, promptRun, rec, stdout, stderr)
	terminal, completionPath, completionErr := loadChildTerminalCompletion(cfg.Workspace.Root, childResult)
	if completionErr != nil {
		state.Cycles[assessment.CycleIndex].FailureKind = "missing_terminal_record"
		state.Cycles[assessment.CycleIndex].FailureReason = completionErr.Error()
		state.Cycles[assessment.CycleIndex].Status = intentloopcontract.CycleStatusFailed
		state.Cycles[assessment.CycleIndex].ExitCode = 1
		statePath, _ := SaveState(cfg.Workspace.Root, state)
		return resultFromState(*state, statePath, 1, nil, completionPath), nil
	}

	gaps := AnalyzeEvidenceGaps(state.Contract, *terminal)
	finalizeCycleRecord(&state.Cycles[assessment.CycleIndex], childResult, *terminal, completionPath, gaps, cfg.Workspace.Root)
	state.Cycles[assessment.CycleIndex].TokensUsed = estimatedCycleTokens(opts, assessment.CyclePrompt, childResult, *terminal)
	state.TokensUsed += state.Cycles[assessment.CycleIndex].TokensUsed
	if err := emitIntentLoopJSONEvent(stdout, opts.PlanExecuteOptions.JSONEvents, intentloopcontract.EventCycleComplete, map[string]any{
		"session_id":      state.SessionID,
		"cycle_number":    state.Cycles[assessment.CycleIndex].CycleNumber,
		"remaining_gaps":  sanitizedGapSummaries(gaps),
		"gap_count":       len(gaps),
		"tokens_used":     state.TokensUsed,
		"child_exit_code": childResult.ExitCode,
	}); err != nil {
		return intentLoopJSONEventFailureResult(state.SessionID, "", err, stderr), nil
	}
	if result, terminalResult := foldCompletedCycle(cfg, opts, stdout, stderr, state, assessment.CycleIndex, &identity, terminal, completionPath, gaps, childResult.ExitCode); terminalResult {
		return result, terminal
	}
	return IntentLoopResult{}, terminal
}

func blockIncompleteResumeCycle(root string, state *IntentLoopState, assessment ResumeAssessment, failureKind string, detail string) (string, error) {
	if state == nil {
		return "", fmt.Errorf("intent loop state is nil")
	}
	failureKind = strings.TrimSpace(failureKind)
	detail = strings.TrimSpace(detail)
	reason := failureKind
	if detail != "" {
		if reason != "" {
			reason += ":" + detail
		} else {
			reason = detail
		}
	}
	if reason == "" {
		reason = "blocked_incomplete_resume_cycle"
	}

	state.Status = intentloopcontract.StatusBlocked
	state.TerminationReason = reason
	if assessment.CycleIndex >= 0 && assessment.CycleIndex < len(state.Cycles) {
		cycle := &state.Cycles[assessment.CycleIndex]
		if cycle.Status == intentloopcontract.CycleStatusDispatched {
			cycle.Status = intentloopcontract.CycleStatusFailed
		}
		cycle.FailureKind = firstNonEmpty(failureKind, reason)
		cycle.FailureReason = reason
		if cycle.ExitCode == 0 {
			cycle.ExitCode = 1
		}
	}
	return SaveState(root, state)
}

func IncompleteCycle(state IntentLoopState) (int, CycleRecord, bool) {
	if len(state.Cycles) == 0 {
		return -1, CycleRecord{}, false
	}

	index := len(state.Cycles) - 1
	cycle := state.Cycles[index]
	if cycle.Status != intentloopcontract.CycleStatusDispatched {
		return -1, CycleRecord{}, false
	}
	return index, cycle, true
}

func resumeScopeBlock(state IntentLoopState, currentFingerprint string) (string, string) {
	currentFingerprint = strings.TrimSpace(currentFingerprint)
	if currentFingerprint == "" {
		return "", ""
	}

	savedFingerprint := lastSavedScopedFingerprint(state)
	if savedFingerprint == "" || savedFingerprint == currentFingerprint {
		return "", ""
	}

	return ResumeBlockExternalScopedChange, "scoped fingerprint changed since last saved loop fingerprint"
}

func lastSavedScopedFingerprint(state IntentLoopState) string {
	for i := len(state.Cycles) - 1; i >= 0; i-- {
		if fingerprint := strings.TrimSpace(state.Cycles[i].PostRunScopedFingerprint); fingerprint != "" {
			return fingerprint
		}
	}
	for i := len(state.Cycles) - 1; i >= 0; i-- {
		if fingerprint := strings.TrimSpace(state.Cycles[i].PreRunScopedFingerprint); fingerprint != "" {
			return fingerprint
		}
	}
	return ""
}
