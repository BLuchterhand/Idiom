package intentloop

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	intentloopcontract "idiom/internal/contracts/intentloop"
	plancontract "idiom/internal/contracts/plan"
	"idiom/internal/evidence/telemetry"
	"idiom/internal/platform/config"
	"idiom/internal/platform/jsonevents"
	"idiom/internal/platform/sessionid"
	"idiom/internal/workflows/planexecute"
)

const (
	DefaultMaxCycles = 5
)

type PlanExecuteRunner func(config.Config, planexecute.Options, planexecute.PromptRunner, *telemetry.Recorder, io.Writer, io.Writer) planexecute.RunResult

type IntentLoopOptions struct {
	PlanExecuteOptions          planexecute.Options
	OriginalPrompt              string
	SessionID                   string
	Resume                      string
	MaxCycles                   int
	CycleBudgetTokens           int
	ExtraVerificationCommands   []string
	DetectExternalScopedChanges bool
	BackendProfile              string
	PlannerModel                string
	ExecutorModel               string
	ToolManifestHash            string
	Now                         func() time.Time
	NewChildSessionID           func() string
	EstimateCycleTokens         func(string, planexecute.RunResult, plancontract.PlanExecuteTerminalCompletionRecord) int
	RunPlanExecute              PlanExecuteRunner
}

type IntentLoopResult struct {
	ExitCode          int
	SessionID         string
	StatePath         string
	Status            intentloopcontract.Status
	TerminationReason string
	State             IntentLoopState
	Completion        *plancontract.PlanExecuteTerminalCompletionRecord
	CompletionPath    string
}

func Run(cfg config.Config, opts IntentLoopOptions, promptRun planexecute.PromptRunner, rec *telemetry.Recorder, stdout io.Writer, stderr io.Writer) IntentLoopResult {
	if opts.RunPlanExecute == nil {
		return failedResult(opts.SessionID, "", intentloopcontract.StatusFailed, "intent-loop requires an injected plan-execute runner", stderr)
	}
	if opts.Now == nil {
		opts.Now = func() time.Time { return time.Now().UTC() }
	}
	if opts.NewChildSessionID == nil {
		opts.NewChildSessionID = sessionid.New
	}
	if opts.MaxCycles <= 0 {
		opts.MaxCycles = DefaultMaxCycles
	}
	if opts.CycleBudgetTokens < 0 {
		return failedResult("", "", intentloopcontract.StatusFailed, "cycle budget must be >= 0", stderr)
	}

	originalPrompt, err := resolveOriginalPrompt(opts)
	if err != nil {
		return failedResult("", "", intentloopcontract.StatusFailed, err.Error(), stderr)
	}

	state, statePath, err := loadOrCreateLoopState(cfg, opts, originalPrompt)
	if err != nil {
		return failedResult(opts.SessionID, "", intentloopcontract.StatusFailed, err.Error(), stderr)
	}

	if strings.TrimSpace(opts.Resume) != "" && intentloopcontract.StatusTerminal(state.Status) {
		if err := emitIntentLoopJSONEvent(stdout, opts.PlanExecuteOptions.JSONEvents, intentloopcontract.EventResumeTerminal, map[string]any{
			"session_id": state.SessionID,
			"status":     state.Status,
			"reason":     state.TerminationReason,
		}); err != nil {
			return intentLoopJSONEventFailureResult(state.SessionID, statePath, err, stderr)
		}
		return resultFromState(state, statePath, intentloopcontract.TerminalExitCode(state.Status), nil, "")
	}

	if err := emitIntentLoopJSONEvent(stdout, opts.PlanExecuteOptions.JSONEvents, intentloopcontract.EventStart, map[string]any{
		"session_id":             state.SessionID,
		"max_cycles":             state.MaxCycles,
		"budget_tokens":          state.BudgetTokens,
		"contract_id":            state.Contract.ScopeContract.ContractID,
		"contract_hash":          state.Contract.ContractHash,
		"required_tests":         len(state.Contract.RequiredTestCommands),
		"acceptance_obligations": len(state.Contract.AcceptanceObligations),
	}); err != nil {
		return intentLoopJSONEventFailureResult(state.SessionID, statePath, err, stderr)
	}

	if result, terminal := resumeIncompleteCycle(cfg, opts, promptRun, rec, stdout, stderr, &state); result.Status != "" {
		return result
	} else if terminal != nil {
		savedStatePath, saveErr := SaveState(cfg.Workspace.Root, &state)
		if saveErr != nil {
			return intentLoopStatePersistenceFailureResult(state.SessionID, statePath, saveErr, stderr)
		}
		statePath = savedStatePath
	}

	if opts.DetectExternalScopedChanges {
		if reason := externalScopedChangeReason(cfg.Workspace.Root, state); reason != "" {
			state.Status = intentloopcontract.StatusBlocked
			state.TerminationReason = reason
			savedStatePath, saveErr := SaveState(cfg.Workspace.Root, &state)
			if saveErr != nil {
				return intentLoopStatePersistenceFailureResult(state.SessionID, statePath, saveErr, stderr)
			}
			statePath = savedStatePath
			if err := emitIntentLoopJSONEvent(stdout, opts.PlanExecuteOptions.JSONEvents, intentloopcontract.EventBlocked, map[string]any{
				"session_id": state.SessionID,
				"reason":     reason,
			}); err != nil {
				return intentLoopJSONEventFailureResult(state.SessionID, statePath, err, stderr)
			}
			return resultFromState(state, statePath, 1, nil, "")
		}
	}

	for cycle := state.CurrentCycle + 1; cycle <= state.MaxCycles; cycle++ {
		state.CurrentCycle = cycle
		result, terminal, completionPath := runOneCycle(cfg, opts, promptRun, rec, stdout, stderr, &state, cycle)
		savedStatePath, saveErr := SaveState(cfg.Workspace.Root, &state)
		if saveErr != nil {
			return intentLoopStatePersistenceFailureResult(state.SessionID, statePath, saveErr, stderr)
		}
		statePath = savedStatePath
		if result.Status != "" {
			return result
		}
		if terminal == nil {
			state.Status = intentloopcontract.StatusBlocked
			state.TerminationReason = "missing_terminal_record"
			savedStatePath, saveErr = SaveState(cfg.Workspace.Root, &state)
			if saveErr != nil {
				return intentLoopStatePersistenceFailureResult(state.SessionID, statePath, saveErr, stderr)
			}
			statePath = savedStatePath
			if err := emitIntentLoopJSONEvent(stdout, opts.PlanExecuteOptions.JSONEvents, intentloopcontract.EventBlocked, map[string]any{
				"session_id":      state.SessionID,
				"cycle_number":    cycle,
				"blocker_summary": state.TerminationReason,
			}); err != nil {
				return intentLoopJSONEventFailureResult(state.SessionID, statePath, err, stderr)
			}
			return resultFromState(state, statePath, 1, nil, completionPath)
		}
	}

	state.Status = intentloopcontract.StatusMaxCyclesExceeded
	state.TerminationReason = "max cycles exceeded"
	return persistStateResult(cfg.Workspace.Root, &state, 1, nil, "", stderr)
}

func runOneCycle(cfg config.Config, opts IntentLoopOptions, promptRun planexecute.PromptRunner, rec *telemetry.Recorder, stdout io.Writer, stderr io.Writer, state *IntentLoopState, cycle int) (IntentLoopResult, *plancontract.PlanExecuteTerminalCompletionRecord, string) {
	childSessionID := opts.NewChildSessionID()
	commands := childVerificationCommands(*state, opts)
	identity := childIdentity(cfg, opts, state.Contract, commands)
	preFingerprint := scopedWorkspaceFingerprint(cfg.Workspace.Root, state.Contract.ScopeContract)

	record := CycleRecord{
		CycleNumber:             cycle,
		PlanExecuteSessionID:    childSessionID,
		Phase:                   "dispatched",
		Status:                  intentloopcontract.CycleStatusDispatched,
		PreRunScopedFingerprint: preFingerprint,
		ChildIdentity:           &identity,
	}
	state.Cycles = append(state.Cycles, record)
	cycleIndex := len(state.Cycles) - 1
	state.Status = intentloopcontract.StatusRunning
	statePath, err := SaveState(cfg.Workspace.Root, state)
	if err != nil {
		return intentLoopStatePersistenceFailureResult(state.SessionID, statePath, err, stderr), nil, ""
	}

	cyclePrompt := BuildCyclePrompt(state.OriginalPrompt, *state)
	promptPath, promptHash, err := SaveCyclePrompt(cfg.Workspace.Root, state.SessionID, cycle, cyclePrompt)
	if err != nil {
		return finishLoopFailure(cfg, opts, stdout, stderr, state, cycleIndex, "cycle_prompt_save_failed", err.Error()), nil, ""
	}
	state.Cycles[cycleIndex].CyclePromptPath = promptPath
	state.Cycles[cycleIndex].CyclePromptSHA256 = promptHash
	statePath, err = SaveState(cfg.Workspace.Root, state)
	if err != nil {
		return intentLoopStatePersistenceFailureResult(state.SessionID, statePath, err, stderr), nil, ""
	}

	if err := emitIntentLoopJSONEvent(stdout, opts.PlanExecuteOptions.JSONEvents, intentloopcontract.EventCycleStart, map[string]any{
		"session_id":          state.SessionID,
		"cycle_number":        cycle,
		"cycle_prompt_sha256": promptHash,
		"child_session_id":    childSessionID,
	}); err != nil {
		return intentLoopJSONEventFailureResult(state.SessionID, "", err, stderr), nil, ""
	}

	childOpts := childPlanExecuteOptions(opts, *state, cyclePrompt, childSessionID, commands)
	childResult := opts.RunPlanExecute(cfg, childOpts, promptRun, rec, stdout, stderr)
	terminal, completionPath, completionErr := loadChildTerminalCompletion(cfg.Workspace.Root, childResult)
	if completionErr != nil {
		state.Cycles[cycleIndex].FailureKind = "missing_terminal_record"
		state.Cycles[cycleIndex].FailureReason = completionErr.Error()
		state.Cycles[cycleIndex].Status = intentloopcontract.CycleStatusFailed
		state.Cycles[cycleIndex].ExitCode = 1
		return IntentLoopResult{}, nil, completionPath
	}

	gaps := AnalyzeEvidenceGaps(state.Contract, *terminal)
	finalizeCycleRecord(&state.Cycles[cycleIndex], childResult, *terminal, completionPath, gaps, cfg.Workspace.Root)
	state.Cycles[cycleIndex].TokensUsed = estimatedCycleTokens(opts, cyclePrompt, childResult, *terminal)
	state.TokensUsed += state.Cycles[cycleIndex].TokensUsed

	if err := emitIntentLoopJSONEvent(stdout, opts.PlanExecuteOptions.JSONEvents, intentloopcontract.EventCycleComplete, map[string]any{
		"session_id":      state.SessionID,
		"cycle_number":    cycle,
		"remaining_gaps":  sanitizedGapSummaries(gaps),
		"gap_count":       len(gaps),
		"tokens_used":     state.TokensUsed,
		"child_exit_code": childResult.ExitCode,
	}); err != nil {
		return intentLoopJSONEventFailureResult(state.SessionID, "", err, stderr), nil, completionPath
	}

	if result, terminalResult := foldCompletedCycle(cfg, opts, stdout, stderr, state, cycleIndex, &identity, terminal, completionPath, gaps, childResult.ExitCode); terminalResult {
		return result, terminal, completionPath
	}

	return IntentLoopResult{}, terminal, completionPath
}

func foldCompletedCycle(cfg config.Config, opts IntentLoopOptions, stdout io.Writer, stderr io.Writer, state *IntentLoopState, cycleIndex int, identity *ChildRunIdentity, terminal *plancontract.PlanExecuteTerminalCompletionRecord, completionPath string, gaps []EvidenceGap, childExitCode int) (IntentLoopResult, bool) {
	if state == nil || cycleIndex < 0 || cycleIndex >= len(state.Cycles) {
		return IntentLoopResult{}, false
	}
	cycle := state.Cycles[cycleIndex].CycleNumber

	if len(gaps) == 0 && childExitCode == 0 {
		state.Status = intentloopcontract.StatusCompleted
		state.TerminationReason = ""
		_, _ = CompactChildArtifacts(cfg.Workspace.Root, state)
		if err := emitIntentLoopJSONEvent(stdout, opts.PlanExecuteOptions.JSONEvents, intentloopcontract.EventComplete, map[string]any{
			"session_id":    state.SessionID,
			"total_cycles":  len(state.Cycles),
			"total_tokens":  state.TokensUsed,
			"changed_paths": cumulativeChangedPaths(state.Cycles),
		}); err != nil {
			return intentLoopJSONEventFailureResult(state.SessionID, "", err, stderr), true
		}
		return persistStateResult(cfg.Workspace.Root, state, 0, terminal, completionPath, stderr), true
	}

	outcome := ClassifyCycleOutcome(state.Cycles[cycleIndex], identity)
	if outcome.Blocking {
		state.Status = intentloopcontract.StatusBlocked
		state.TerminationReason = outcome.Reason
		if err := emitIntentLoopJSONEvent(stdout, opts.PlanExecuteOptions.JSONEvents, intentloopcontract.EventBlocked, map[string]any{
			"session_id":         state.SessionID,
			"cycle_number":       cycle,
			"blocker_summary":    outcome.Reason,
			"child_failure_kind": state.Cycles[cycleIndex].FailureKind,
		}); err != nil {
			return intentLoopJSONEventFailureResult(state.SessionID, "", err, stderr), true
		}
		return persistStateResult(cfg.Workspace.Root, state, 1, terminal, completionPath, stderr), true
	}

	failureSet := GapFingerprint(gaps)
	if failureSet != "" {
		if state.SeenFailureSets[failureSet] {
			state.Status = intentloopcontract.StatusNoProgress
			state.TerminationReason = "failure set " + failureSet + " seen in prior cycle"
			if err := emitIntentLoopJSONEvent(stdout, opts.PlanExecuteOptions.JSONEvents, intentloopcontract.EventConvergenceDetected, map[string]any{
				"session_id":   state.SessionID,
				"cycle_number": cycle,
				"failure_set":  failureSet,
			}); err != nil {
				return intentLoopJSONEventFailureResult(state.SessionID, "", err, stderr), true
			}
			return persistStateResult(cfg.Workspace.Root, state, 1, terminal, completionPath, stderr), true
		}
		state.SeenFailureSets[failureSet] = true
	}

	if opts.CycleBudgetTokens > 0 {
		remainingCycles := max(1, state.MaxCycles-cycle+1)
		softCap := opts.CycleBudgetTokens / remainingCycles
		if softCap > 0 && state.Cycles[cycleIndex].TokensUsed > softCap*2 {
			if err := emitIntentLoopJSONEvent(stdout, opts.PlanExecuteOptions.JSONEvents, intentloopcontract.EventCycleBudgetWarning, map[string]any{
				"session_id":      state.SessionID,
				"cycle_number":    cycle,
				"cycle_tokens":    state.Cycles[cycleIndex].TokensUsed,
				"soft_cap_tokens": softCap,
				"tokens_used":     state.TokensUsed,
				"budget_tokens":   opts.CycleBudgetTokens,
			}); err != nil {
				return intentLoopJSONEventFailureResult(state.SessionID, "", err, stderr), true
			}
		}
		if state.TokensUsed >= opts.CycleBudgetTokens {
			state.Status = intentloopcontract.StatusBudgetExhausted
			state.BudgetExhausted = true
			state.TerminationReason = "cycle budget exhausted"
			if err := emitIntentLoopJSONEvent(stdout, opts.PlanExecuteOptions.JSONEvents, intentloopcontract.EventBudgetExhausted, map[string]any{
				"session_id":    state.SessionID,
				"tokens_used":   state.TokensUsed,
				"budget_tokens": opts.CycleBudgetTokens,
			}); err != nil {
				return intentLoopJSONEventFailureResult(state.SessionID, "", err, stderr), true
			}
			return persistStateResult(cfg.Workspace.Root, state, 1, terminal, completionPath, stderr), true
		}
	}

	statePath, err := SaveState(cfg.Workspace.Root, state)
	if err != nil {
		return intentLoopStatePersistenceFailureResult(state.SessionID, statePath, err, stderr), true
	}
	return IntentLoopResult{}, false
}

func childPlanExecuteOptions(opts IntentLoopOptions, state IntentLoopState, cyclePrompt string, childSessionID string, commands []string) planexecute.Options {
	child := opts.PlanExecuteOptions
	child.Prompt = cyclePrompt
	child.PromptFile = ""
	child.Resume = ""
	child.SessionID = childSessionID
	child.ScopeContract = state.Contract.ScopeContract
	child.ScopeExtractionPolicy = planexecute.ScopeExtractionFrozen
	child.VerificationPlanExplicitCommands = commands
	child.AuthorizedNewFiles = authorizedNewFilePaths(state.Contract)
	return child
}

func childVerificationCommands(state IntentLoopState, opts IntentLoopOptions) []string {
	commands := append([]string(nil), state.Contract.RequiredTestCommandStrings()...)
	commands = append(commands, opts.ExtraVerificationCommands...)
	commands = append(commands, opts.PlanExecuteOptions.VerificationPlanExplicitCommands...)
	return uniqueStrings(commands)
}

func loadOrCreateLoopState(cfg config.Config, opts IntentLoopOptions, originalPrompt string) (IntentLoopState, string, error) {
	sessionID := strings.TrimSpace(firstNonEmpty(opts.Resume, opts.SessionID, opts.PlanExecuteOptions.SessionID))
	if sessionID != "" && strings.TrimSpace(opts.Resume) != "" {
		state, path, err := LoadState(cfg.Workspace.Root, sessionID)
		if err != nil {
			return IntentLoopState{}, path, err
		}
		if state.OriginalPrompt != originalPrompt && sha256Hex(originalPrompt) != state.PromptSHA256 {
			return IntentLoopState{}, path, fmt.Errorf("intent loop resume prompt mismatch for session %s", sessionID)
		}
		state.MaxCycles = opts.MaxCycles
		if state.BudgetTokens == 0 {
			state.BudgetTokens = opts.CycleBudgetTokens
		}
		return state, path, nil
	}

	if sessionID == "" {
		sessionID = sessionid.New()
	}
	contract, err := ExtractContract(ContractExtractionInput{
		OriginalPrompt:        originalPrompt,
		RequiredCoveragePaths: []string(opts.PlanExecuteOptions.RequiredEditPaths),
		AllowedCandidatePaths: []string(opts.PlanExecuteOptions.AllowedEditPaths),
		WriteAllowedPaths:     []string(opts.PlanExecuteOptions.WriteAllowedPaths),
	})
	if err != nil {
		return IntentLoopState{}, "", err
	}
	state := NewState(sessionID, originalPrompt, contract, opts.MaxCycles, opts.Now())
	state.BudgetTokens = opts.CycleBudgetTokens
	path, err := SaveState(cfg.Workspace.Root, &state)
	return state, path, err
}

func resolveOriginalPrompt(opts IntentLoopOptions) (string, error) {
	if prompt := strings.TrimSpace(opts.OriginalPrompt); prompt != "" {
		return opts.OriginalPrompt, nil
	}
	if prompt := strings.TrimSpace(opts.PlanExecuteOptions.Prompt); prompt != "" {
		return opts.PlanExecuteOptions.Prompt, nil
	}
	if path := strings.TrimSpace(opts.PlanExecuteOptions.PromptFile); path != "" {
		body, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("read intent-loop prompt file: %w", err)
		}
		return string(body), nil
	}
	return "", fmt.Errorf("intent loop requires a prompt or prompt file")
}

func loadChildTerminalCompletion(root string, result planexecute.RunResult) (*plancontract.PlanExecuteTerminalCompletionRecord, string, error) {
	if result.Completion != nil {
		completion := plancontract.NormalizePlanExecuteTerminalCompletionRecord(*result.Completion)
		return &completion, result.CompletionPath, nil
	}
	sessionID := strings.TrimSpace(result.SessionID)
	if sessionID == "" {
		return nil, result.CompletionPath, fmt.Errorf("child run did not return a session id or terminal completion")
	}
	record, path, found, err := planexecute.TryLoadPlanExecuteTerminalCompletionRecord(root, sessionID)
	if err != nil {
		return nil, path, err
	}
	if !found {
		return nil, path, fmt.Errorf("plan-execute terminal completion not found for child session %s", sessionID)
	}
	return &record, path, nil
}

func finalizeCycleRecord(record *CycleRecord, result planexecute.RunResult, terminal plancontract.PlanExecuteTerminalCompletionRecord, completionPath string, gaps []EvidenceGap, root string) {
	record.Phase = strings.TrimSpace(string(terminal.Phase))
	record.Status = intentloopcontract.CycleStatusCompleted
	if result.ExitCode != 0 || strings.TrimSpace(result.FailureKind) != "" ||
		strings.TrimSpace(string(terminal.Status)) == string(plancontract.PlanExecuteProgressStatusFailed) ||
		strings.TrimSpace(string(terminal.FailureKind)) != "" ||
		strings.TrimSpace(terminal.FailureReason) != "" ||
		terminalValidationFailuresBlockIntentLoop(terminal.ValidationFailures) {
		record.Status = intentloopcontract.CycleStatusFailed
	}
	record.TerminalCompletionPath = completionPath
	record.ExitCode = result.ExitCode
	record.FailureKind = firstNonEmpty(result.FailureKind, string(terminal.FailureKind))
	record.FailureReason = firstNonEmpty(result.FailureReason, terminal.FailureReason)
	record.ChangedPaths = append([]string(nil), terminal.ChangedPaths...)
	record.MutationDeltas = mutationDeltas(root, terminal.ChangedPaths)
	record.SourceSanityPassed = len(sourceSanityGaps(terminal)) == 0
	record.VerificationPassed = len(terminal.VerificationRejects) == 0 && allPhaseChecksPassed(terminal.Verification)
	record.ReviewPassed = reviewPassed(terminal)
	record.RemainingFailures = append([]EvidenceGap(nil), gaps...)
	record.ResumeStatePath = result.ResumeStatePath
	record.PostRunScopedFingerprint = scopedWorkspaceFingerprint(root, plancontract.NewPlanScopeContract(terminal.RequiredEditPaths, terminal.AllowedEditPaths, nil).WithWriteAllowedPaths(terminal.WriteAllowedPaths))
}

func estimatedCycleTokens(opts IntentLoopOptions, cyclePrompt string, result planexecute.RunResult, terminal plancontract.PlanExecuteTerminalCompletionRecord) int {
	if opts.EstimateCycleTokens != nil {
		if tokens := opts.EstimateCycleTokens(cyclePrompt, result, terminal); tokens > 0 {
			return tokens
		}
	}

	chars := len(cyclePrompt) + len(result.PlannerAnswer) + len(terminal.FailureReason)
	for _, gap := range AnalyzeEvidenceGaps(IntentLoopContract{}, terminal) {
		chars += len(gap.Detail)
	}
	if chars <= 0 {
		return 0
	}
	return (chars + 3) / 4
}

func allPhaseChecksPassed(checks []plancontract.PhaseEvidenceCheck) bool {
	if len(checks) == 0 {
		return false
	}
	for _, check := range checks {
		if !phaseCheckPassed(check) {
			return false
		}
	}
	return true
}

func childIdentity(cfg config.Config, opts IntentLoopOptions, contract IntentLoopContract, commands []string) ChildRunIdentity {
	root, _ := filepath.Abs(cfg.Workspace.Root)
	if root == "" {
		root = cfg.Workspace.Root
	}
	model := strings.TrimSpace(firstNonEmpty(opts.PlanExecuteOptions.Model, cfg.LlamaServer.Model))
	return ChildRunIdentity{
		WorkspaceRootSHA256:        sha256Hex(root),
		ScopeContractSHA256:        sha256Hex(stableJSONHash(contract.ScopeContract.Normalized())),
		VerificationCommandsSHA256: sha256Hex(stableJSONHash(commands)),
		ToolManifestSHA256:         firstNonEmpty(opts.ToolManifestHash, sha256Hex("planexecute-default-tool-manifest")),
		BackendProfile:             strings.TrimSpace(opts.BackendProfile),
		PlannerModel:               firstNonEmpty(opts.PlannerModel, model),
		ExecutorModel:              firstNonEmpty(opts.ExecutorModel, model),
	}
}

func scopedWorkspaceFingerprint(root string, scope plancontract.PlanScopeContract) string {
	scope = scope.Normalized()
	paths := uniqueStrings(append(append([]string(nil), scope.RequiredCoveragePaths...), append(scope.AllowedCandidatePaths, scope.WriteAllowedPaths...)...))
	if len(paths) == 0 {
		return ""
	}
	var parts []string
	for _, rel := range paths {
		clean := plancontract.NormalizeScopePath(rel)
		if clean == "" {
			continue
		}
		full := filepath.Join(root, filepath.FromSlash(clean))
		info, err := os.Stat(full)
		switch {
		case err != nil:
			parts = append(parts, clean+":missing")
		case info.IsDir():
			parts = append(parts, clean+":dir")
		default:
			body, err := os.ReadFile(full)
			if err != nil {
				parts = append(parts, clean+":unreadable")
				continue
			}
			parts = append(parts, clean+":"+sha256Hex(string(body)))
		}
	}
	sort.Strings(parts)
	return sha256Hex(strings.Join(parts, "\n"))
}

func externalScopedChangeReason(root string, state IntentLoopState) string {
	if len(state.Cycles) == 0 {
		return ""
	}
	for i := len(state.Cycles) - 1; i >= 0; i-- {
		if state.Cycles[i].PostRunScopedFingerprint == "" {
			continue
		}
		current := scopedWorkspaceFingerprint(root, state.Contract.ScopeContract)
		if current != "" && current != state.Cycles[i].PostRunScopedFingerprint {
			return "blocked_external_scoped_change"
		}
		break
	}
	return ""
}

func mutationDeltas(root string, paths []string) []CycleMutationDelta {
	var deltas []CycleMutationDelta
	for _, path := range uniqueStrings(paths) {
		clean := plancontract.NormalizeScopePath(path)
		if clean == "" {
			continue
		}
		full := filepath.Join(root, filepath.FromSlash(clean))
		body, err := os.ReadFile(full)
		kind := "modify"
		hashInput := clean
		if err != nil {
			kind = "delete"
			hashInput += ":missing"
		} else {
			hashInput += ":" + string(body)
		}
		deltas = append(deltas, CycleMutationDelta{
			Path:       clean,
			Kind:       kind,
			DiffSHA256: sha256Prefixed(hashInput),
		})
	}
	return deltas
}

func authorizedNewFilePaths(contract IntentLoopContract) []string {
	var paths []string
	for _, file := range contract.AuthorizedNewFiles {
		if path := plancontract.NormalizeScopePath(file.Path); path != "" {
			paths = append(paths, path)
		}
	}
	return uniqueStrings(paths)
}

func sanitizedGapSummaries(gaps []EvidenceGap) []map[string]any {
	out := make([]map[string]any, 0, len(gaps))
	for _, gap := range gaps {
		out = append(out, map[string]any{
			"requirement":      gap.Requirement,
			"status":           gap.Status,
			"path":             gap.Path,
			"detail_hash":      gap.DetailHash,
			"threshold_result": gap.ThresholdResult,
		})
	}
	return out
}

func emitIntentLoopJSONEvent(stdout io.Writer, jsonEvents bool, eventType intentloopcontract.EventType, fields map[string]any) error {
	if !intentloopcontract.KnownEventType(eventType) {
		return fmt.Errorf("unknown intent-loop event type %q", eventType)
	}

	return jsonevents.Emit(stdout, jsonEvents, intentloopcontract.EventName(eventType), fields)
}

func intentLoopJSONEventFailureResult(sessionID string, statePath string, err error, stderr io.Writer) IntentLoopResult {
	return failedResult(sessionID, statePath, intentloopcontract.StatusFailed, fmt.Sprintf("json_event_write_failed: %v", err), stderr)
}

func intentLoopStatePersistenceFailureResult(sessionID string, statePath string, err error, stderr io.Writer) IntentLoopResult {
	return failedResult(sessionID, statePath, intentloopcontract.StatusFailed, fmt.Sprintf("intent_loop_state_write_failed: %v", err), stderr)
}

func persistStateResult(root string, state *IntentLoopState, exitCode int, completion *plancontract.PlanExecuteTerminalCompletionRecord, completionPath string, stderr io.Writer) IntentLoopResult {
	statePath, err := SaveState(root, state)
	if err != nil {
		return intentLoopStatePersistenceFailureResult(state.SessionID, statePath, err, stderr)
	}

	return resultFromState(*state, statePath, exitCode, completion, completionPath)
}

func failedResult(sessionID string, statePath string, status intentloopcontract.Status, reason string, stderr io.Writer) IntentLoopResult {
	if reason != "" && stderr != nil {
		fmt.Fprintln(stderr, reason)
	}
	return IntentLoopResult{
		ExitCode:          1,
		SessionID:         sessionID,
		StatePath:         statePath,
		Status:            status,
		TerminationReason: reason,
	}
}

func finishLoopFailure(cfg config.Config, opts IntentLoopOptions, stdout io.Writer, stderr io.Writer, state *IntentLoopState, cycleIndex int, kind string, reason string) IntentLoopResult {
	state.Cycles[cycleIndex].Status = intentloopcontract.CycleStatusFailed
	state.Cycles[cycleIndex].FailureKind = kind
	state.Cycles[cycleIndex].FailureReason = reason
	state.Status = intentloopcontract.StatusFailed
	state.TerminationReason = reason
	if err := emitIntentLoopJSONEvent(stdout, opts.PlanExecuteOptions.JSONEvents, intentloopcontract.EventBlocked, map[string]any{
		"session_id":         state.SessionID,
		"cycle_number":       state.Cycles[cycleIndex].CycleNumber,
		"blocker_summary":    reason,
		"child_failure_kind": kind,
	}); err != nil {
		return intentLoopJSONEventFailureResult(state.SessionID, "", err, stderr)
	}
	statePath, err := SaveState(cfg.Workspace.Root, state)
	if err != nil {
		return intentLoopStatePersistenceFailureResult(state.SessionID, statePath, err, stderr)
	}
	if stderr != nil && reason != "" {
		fmt.Fprintln(stderr, reason)
	}
	return resultFromState(*state, statePath, 1, nil, "")
}

func resultFromState(state IntentLoopState, statePath string, exitCode int, completion *plancontract.PlanExecuteTerminalCompletionRecord, completionPath string) IntentLoopResult {
	return IntentLoopResult{
		ExitCode:          exitCode,
		SessionID:         state.SessionID,
		StatePath:         statePath,
		Status:            state.Status,
		TerminationReason: state.TerminationReason,
		State:             state,
		Completion:        completion,
		CompletionPath:    completionPath,
	}
}
