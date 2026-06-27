package intentloop

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	intentloopcontract "idiom/internal/contracts/intentloop"
	plancontract "idiom/internal/contracts/plan"
	"idiom/internal/evidence/telemetry"
	"idiom/internal/platform/config"
	"idiom/internal/workflows/planexecute"
)

type intentLoopFailingWriter struct {
	err error
}

func (w intentLoopFailingWriter) Write([]byte) (int, error) {
	return 0, w.err
}

type intentLoopFailAfterWriter struct {
	allowedWrites int
	writes        int
	err           error
}

func (w *intentLoopFailAfterWriter) Write(p []byte) (int, error) {
	w.writes++
	if w.writes > w.allowedWrites {
		return 0, w.err
	}
	return len(p), nil
}

func TestRunRequiresInjectedPlanExecuteRunner(t *testing.T) {
	var stderr bytes.Buffer

	result := Run(intentLoopTestConfig(t.TempDir()), IntentLoopOptions{
		SessionID: "loop-missing-runner",
		PlanExecuteOptions: planexecute.Options{
			Prompt: "fix it",
		},
	}, nil, nil, &bytes.Buffer{}, &stderr)

	if result.ExitCode != 1 || result.Status != intentloopcontract.StatusFailed {
		t.Fatalf("result = exit %d status %q", result.ExitCode, result.Status)
	}
	if !strings.Contains(result.TerminationReason, "injected plan-execute runner") {
		t.Fatalf("TerminationReason = %q", result.TerminationReason)
	}
	if !strings.Contains(stderr.String(), "injected plan-execute runner") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunCompletesAfterRecoverableVerificationGap(t *testing.T) {
	root := t.TempDir()
	var calls int
	var childPrompts []string

	result := Run(intentLoopTestConfig(root), IntentLoopOptions{
		PlanExecuteOptions: planexecute.Options{
			Prompt:     intentLoopRequiredTestPrompt("go test ./..."),
			JSONEvents: true,
		},
		MaxCycles: 3,
		NewChildSessionID: func() string {
			calls++
			return "child-" + string(rune('0'+calls))
		},
		RunPlanExecute: func(_ config.Config, opts planexecute.Options, _ planexecute.PromptRunner, _ *telemetry.Recorder, _ io.Writer, _ io.Writer) planexecute.RunResult {
			childPrompts = append(childPrompts, opts.Prompt)
			if opts.ScopeExtractionPolicy != planexecute.ScopeExtractionFrozen {
				t.Fatalf("ScopeExtractionPolicy = %q, want frozen", opts.ScopeExtractionPolicy)
			}
			if len(opts.VerificationPlanExplicitCommands) != 1 || opts.VerificationPlanExplicitCommands[0] != "go test ./..." {
				t.Fatalf("explicit commands = %#v", opts.VerificationPlanExplicitCommands)
			}

			record := verificationTerminal(opts.SessionID, "go test ./...", false)
			exitCode := 1
			if calls == 2 {
				record = verificationTerminal(opts.SessionID, "go test ./...", true)
				exitCode = 0
			}
			return planexecute.RunResult{
				ExitCode:       exitCode,
				SessionID:      opts.SessionID,
				Completion:     &record,
				CompletionPath: ".idiom/plan-execute/sessions/" + opts.SessionID + ".terminal.json",
			}
		},
		EstimateCycleTokens: func(string, planexecute.RunResult, plancontract.PlanExecuteTerminalCompletionRecord) int {
			return 10
		},
	}, nil, nil, &bytes.Buffer{}, &bytes.Buffer{})

	if result.ExitCode != 0 || result.Status != intentloopcontract.StatusCompleted {
		t.Fatalf("result = exit %d status %q reason %q", result.ExitCode, result.Status, result.TerminationReason)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
	if len(result.State.Cycles) != 2 || len(result.State.Cycles[0].RemainingFailures) == 0 || len(result.State.Cycles[1].RemainingFailures) != 0 {
		t.Fatalf("cycles = %#v", result.State.Cycles)
	}
	if len(childPrompts) != 2 || !strings.Contains(childPrompts[1], "## Prior Cycles") || !strings.Contains(childPrompts[1], "verification:go test ./...") {
		t.Fatalf("second prompt did not carry prior failure evidence:\n%s", childPrompts[1])
	}
	if result.StatePath != ".idiom/intent-loop/sessions/"+result.SessionID+".json" {
		t.Fatalf("StatePath = %q", result.StatePath)
	}
}

func TestRunStopsOnStartJSONEventWriteFailure(t *testing.T) {
	root := t.TempDir()
	var calls int
	result := Run(intentLoopTestConfig(root), IntentLoopOptions{
		PlanExecuteOptions: planexecute.Options{
			Prompt:     intentLoopRequiredTestPrompt("go test ./..."),
			JSONEvents: true,
		},
		RunPlanExecute: func(config.Config, planexecute.Options, planexecute.PromptRunner, *telemetry.Recorder, io.Writer, io.Writer) planexecute.RunResult {
			calls++
			return planexecute.RunResult{ExitCode: 99}
		},
	}, nil, nil, intentLoopFailingWriter{err: errors.New("stdout closed")}, io.Discard)

	if calls != 0 {
		t.Fatalf("child run dispatched after JSON event write failure: %d", calls)
	}
	if result.ExitCode != 1 || result.Status != intentloopcontract.StatusFailed || !strings.Contains(result.TerminationReason, "json_event_write_failed") {
		t.Fatalf("result = exit %d status %q reason %q", result.ExitCode, result.Status, result.TerminationReason)
	}
}

func TestRunResumeTerminalStopsOnJSONEventWriteFailure(t *testing.T) {
	root := t.TempDir()
	state := NewState("terminal-json-failure", intentLoopRequiredTestPrompt("go test ./..."), IntentLoopContract{}, 3, time.Now())
	state.Status = intentloopcontract.StatusCompleted
	if _, err := SaveState(root, &state); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	var calls int
	result := Run(intentLoopTestConfig(root), IntentLoopOptions{
		PlanExecuteOptions: planexecute.Options{
			Prompt:     intentLoopRequiredTestPrompt("go test ./..."),
			JSONEvents: true,
		},
		Resume: state.SessionID,
		RunPlanExecute: func(config.Config, planexecute.Options, planexecute.PromptRunner, *telemetry.Recorder, io.Writer, io.Writer) planexecute.RunResult {
			calls++
			return planexecute.RunResult{ExitCode: 99}
		},
	}, nil, nil, intentLoopFailingWriter{err: errors.New("stdout closed")}, io.Discard)

	if calls != 0 {
		t.Fatalf("terminal resume dispatched child after JSON event write failure: %d", calls)
	}
	if result.ExitCode != 1 || result.Status != intentloopcontract.StatusFailed || !strings.Contains(result.TerminationReason, "json_event_write_failed") {
		t.Fatalf("result = exit %d status %q reason %q", result.ExitCode, result.Status, result.TerminationReason)
	}
}

func TestRunResumeIncompleteCycleStopsOnCycleCompleteJSONEventWriteFailure(t *testing.T) {
	root := t.TempDir()
	prompt := intentLoopRequiredTestPrompt("go test ./...")
	cfg := intentLoopTestConfig(root)
	opts := IntentLoopOptions{PlanExecuteOptions: planexecute.Options{Prompt: prompt, JSONEvents: true}}
	state := NewState("resume-json-failure", prompt, IntentLoopContract{}, 1, time.Now())
	promptPath, promptHash, err := SaveCyclePrompt(root, state.SessionID, 1, "cycle prompt")
	if err != nil {
		t.Fatalf("SaveCyclePrompt: %v", err)
	}
	identity := childIdentity(cfg, opts, state.Contract, childVerificationCommands(state, opts))
	state.Status = intentloopcontract.StatusRunning
	state.CurrentCycle = 1
	state.Cycles = []CycleRecord{{
		CycleNumber:          1,
		Status:               "dispatched",
		PlanExecuteSessionID: "child-1",
		CyclePromptPath:      promptPath,
		CyclePromptSHA256:    promptHash,
		ChildIdentity:        &identity,
	}}
	if _, err := SaveState(root, &state); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
	terminal := verificationTerminal("child-1", "go test ./...", true)
	if _, err := planexecute.SavePlanExecuteTerminalCompletionRecord(root, terminal); err != nil {
		t.Fatalf("SavePlanExecuteTerminalCompletionRecord: %v", err)
	}

	stdout := &intentLoopFailAfterWriter{allowedWrites: 1, err: errors.New("stdout closed")}
	result := Run(cfg, IntentLoopOptions{
		PlanExecuteOptions: opts.PlanExecuteOptions,
		Resume:             state.SessionID,
		RunPlanExecute: func(config.Config, planexecute.Options, planexecute.PromptRunner, *telemetry.Recorder, io.Writer, io.Writer) planexecute.RunResult {
			t.Fatal("existing terminal resume should not dispatch child")
			return planexecute.RunResult{}
		},
		EstimateCycleTokens: func(string, planexecute.RunResult, plancontract.PlanExecuteTerminalCompletionRecord) int {
			return 1
		},
	}, nil, nil, stdout, io.Discard)

	if result.ExitCode != 1 || result.Status != intentloopcontract.StatusFailed || !strings.Contains(result.TerminationReason, "json_event_write_failed") {
		t.Fatalf("result = exit %d status %q reason %q", result.ExitCode, result.Status, result.TerminationReason)
	}
	if stdout.writes != 2 {
		t.Fatalf("writes = %d, want start event then failing cycle_complete", stdout.writes)
	}
}

func TestRunStopsOnRepeatedFailureSet(t *testing.T) {
	root := t.TempDir()
	var calls int

	result := Run(intentLoopTestConfig(root), IntentLoopOptions{
		PlanExecuteOptions: planexecute.Options{
			Prompt: intentLoopRequiredTestPrompt("go vet ./..."),
		},
		MaxCycles: 3,
		NewChildSessionID: func() string {
			calls++
			return "repeat-child"
		},
		RunPlanExecute: func(_ config.Config, opts planexecute.Options, _ planexecute.PromptRunner, _ *telemetry.Recorder, _ io.Writer, _ io.Writer) planexecute.RunResult {
			record := verificationTerminal(opts.SessionID, "go vet ./...", false)
			return planexecute.RunResult{ExitCode: 1, SessionID: opts.SessionID, Completion: &record}
		},
		EstimateCycleTokens: func(string, planexecute.RunResult, plancontract.PlanExecuteTerminalCompletionRecord) int {
			return 1
		},
	}, nil, nil, io.Discard, io.Discard)

	if result.Status != intentloopcontract.StatusNoProgress || result.ExitCode != 1 {
		t.Fatalf("result status = %q exit=%d reason=%q", result.Status, result.ExitCode, result.TerminationReason)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want convergence on second repeated failure", calls)
	}
	if len(result.State.SeenFailureSets) != 1 {
		t.Fatalf("SeenFailureSets = %#v", result.State.SeenFailureSets)
	}
}

func TestRunStopsOnCycleBudgetExhaustion(t *testing.T) {
	root := t.TempDir()
	var calls int

	result := Run(intentLoopTestConfig(root), IntentLoopOptions{
		PlanExecuteOptions: planexecute.Options{
			Prompt: intentLoopRequiredTestPrompt("go vet ./..."),
		},
		MaxCycles:         3,
		CycleBudgetTokens: 5,
		NewChildSessionID: func() string {
			calls++
			return "budget-child"
		},
		RunPlanExecute: func(_ config.Config, opts planexecute.Options, _ planexecute.PromptRunner, _ *telemetry.Recorder, _ io.Writer, _ io.Writer) planexecute.RunResult {
			record := verificationTerminal(opts.SessionID, "go vet ./...", false)
			record.Verification[0].Error = "different enough to avoid convergence"
			return planexecute.RunResult{ExitCode: 1, SessionID: opts.SessionID, Completion: &record}
		},
		EstimateCycleTokens: func(string, planexecute.RunResult, plancontract.PlanExecuteTerminalCompletionRecord) int {
			return 6
		},
	}, nil, nil, io.Discard, io.Discard)

	if result.Status != intentloopcontract.StatusBudgetExhausted || !result.State.BudgetExhausted {
		t.Fatalf("result status = %q exhausted=%v", result.Status, result.State.BudgetExhausted)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want budget exhaustion after first cycle", calls)
	}
}

func TestRunDoesNotCompleteWhenTerminalRecordFailedDespiteZeroExit(t *testing.T) {
	root := t.TempDir()
	result := Run(intentLoopTestConfig(root), IntentLoopOptions{
		PlanExecuteOptions: planexecute.Options{
			Prompt: intentLoopRequiredTestPrompt("go test ./..."),
		},
		MaxCycles: 1,
		NewChildSessionID: func() string {
			return "failed-terminal-child"
		},
		RunPlanExecute: func(_ config.Config, opts planexecute.Options, _ planexecute.PromptRunner, _ *telemetry.Recorder, _ io.Writer, _ io.Writer) planexecute.RunResult {
			record := verificationTerminal(opts.SessionID, "go test ./...", true)
			record.FailureKind = plancontract.PlanExecuteFailureExecutorFailed
			record.FailureReason = "terminal record failed after wrapper returned success"
			record = plancontract.NormalizePlanExecuteTerminalCompletionRecord(record)
			return planexecute.RunResult{ExitCode: 0, SessionID: opts.SessionID, Completion: &record}
		},
		EstimateCycleTokens: func(string, planexecute.RunResult, plancontract.PlanExecuteTerminalCompletionRecord) int {
			return 1
		},
	}, nil, nil, io.Discard, io.Discard)

	if result.Status == intentloopcontract.StatusCompleted || result.ExitCode == 0 {
		t.Fatalf("failed terminal record must not complete loop: status=%q exit=%d", result.Status, result.ExitCode)
	}
	if len(result.State.Cycles) != 1 || result.State.Cycles[0].Status != "failed" {
		t.Fatalf("cycle should persist failed terminal authority: %#v", result.State.Cycles)
	}
	if got := result.State.Cycles[0].FailureKind; got != string(plancontract.PlanExecuteFailureExecutorFailed) {
		t.Fatalf("cycle failure kind = %q, want %q", got, plancontract.PlanExecuteFailureExecutorFailed)
	}
}

func TestRunDoesNotCompleteWhenTerminalRecordHasValidationFailures(t *testing.T) {
	root := t.TempDir()
	result := Run(intentLoopTestConfig(root), IntentLoopOptions{
		PlanExecuteOptions: planexecute.Options{
			Prompt: intentLoopRequiredTestPrompt("go test ./..."),
		},
		MaxCycles: 1,
		NewChildSessionID: func() string {
			return "invalid-terminal-child"
		},
		RunPlanExecute: func(_ config.Config, opts planexecute.Options, _ planexecute.PromptRunner, _ *telemetry.Recorder, _ io.Writer, _ io.Writer) planexecute.RunResult {
			record := verificationTerminal(opts.SessionID, "go test ./...", true)
			record.RequiredEditPaths = []string{"pkg/feature.go"}
			record = plancontract.NormalizePlanExecuteTerminalCompletionRecord(record)
			return planexecute.RunResult{ExitCode: 0, SessionID: opts.SessionID, Completion: &record}
		},
		EstimateCycleTokens: func(string, planexecute.RunResult, plancontract.PlanExecuteTerminalCompletionRecord) int {
			return 1
		},
	}, nil, nil, io.Discard, io.Discard)

	if result.Status == intentloopcontract.StatusCompleted || result.ExitCode == 0 {
		t.Fatalf("invalid terminal record must not complete loop: status=%q exit=%d", result.Status, result.ExitCode)
	}
	if len(result.State.Cycles) != 1 || result.State.Cycles[0].Status != "failed" {
		t.Fatalf("cycle should persist failed terminal validation: %#v", result.State.Cycles)
	}
	if !evidenceGapsContain(result.State.Cycles[0].RemainingFailures, "terminal_completion", "terminal completion validation failure") {
		t.Fatalf("cycle should persist terminal validation gap: %#v", result.State.Cycles[0].RemainingFailures)
	}
}

func TestRunResumeTerminalSessionDoesNotDispatchChild(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 6, 26, 12, 0, 0, 0, time.UTC)
	state := NewState("terminal-session", intentLoopRequiredTestPrompt("go test ./..."), IntentLoopContract{}, 3, now)
	state.Status = intentloopcontract.StatusCompleted
	state.CurrentCycle = 1
	state.Cycles = []CycleRecord{{
		CycleNumber:          1,
		PlanExecuteSessionID: "child-1",
		Status:               "completed",
		ExitCode:             0,
	}}
	statePath, err := SaveState(root, &state)
	if err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	var calls int
	result := Run(intentLoopTestConfig(root), IntentLoopOptions{
		PlanExecuteOptions: planexecute.Options{
			Prompt: intentLoopRequiredTestPrompt("go test ./..."),
		},
		Resume: "terminal-session",
		RunPlanExecute: func(config.Config, planexecute.Options, planexecute.PromptRunner, *telemetry.Recorder, io.Writer, io.Writer) planexecute.RunResult {
			calls++
			return planexecute.RunResult{ExitCode: 99}
		},
	}, nil, nil, io.Discard, io.Discard)

	if calls != 0 {
		t.Fatalf("terminal resume dispatched %d child run(s)", calls)
	}
	if result.ExitCode != 0 || result.Status != intentloopcontract.StatusCompleted || result.StatePath != statePath {
		t.Fatalf("result = exit %d status %q path %q", result.ExitCode, result.Status, result.StatePath)
	}
	if result.State.CurrentCycle != 1 || len(result.State.Cycles) != 1 {
		t.Fatalf("terminal state should be unchanged, got cycle=%d cycles=%d", result.State.CurrentCycle, len(result.State.Cycles))
	}
}

func TestRunResumeBlockedTerminalSessionDoesNotDispatchChild(t *testing.T) {
	root := t.TempDir()
	state := NewState("blocked-session", intentLoopRequiredTestPrompt("go test ./..."), IntentLoopContract{}, 3, time.Now())
	state.Status = intentloopcontract.StatusBlocked
	state.TerminationReason = "manual blocker"
	if _, err := SaveState(root, &state); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	var calls int
	result := Run(intentLoopTestConfig(root), IntentLoopOptions{
		PlanExecuteOptions: planexecute.Options{
			Prompt: intentLoopRequiredTestPrompt("go test ./..."),
		},
		Resume: "blocked-session",
		RunPlanExecute: func(config.Config, planexecute.Options, planexecute.PromptRunner, *telemetry.Recorder, io.Writer, io.Writer) planexecute.RunResult {
			calls++
			return planexecute.RunResult{ExitCode: 99}
		},
	}, nil, nil, io.Discard, io.Discard)

	if calls != 0 {
		t.Fatalf("blocked terminal resume dispatched %d child run(s)", calls)
	}
	if result.ExitCode != 1 || result.Status != intentloopcontract.StatusBlocked || result.TerminationReason != "manual blocker" {
		t.Fatalf("result = exit %d status %q reason %q", result.ExitCode, result.Status, result.TerminationReason)
	}
}

func TestRunResumeIncompleteCycleFoldsSuccessfulChildTerminal(t *testing.T) {
	root := t.TempDir()
	prompt := intentLoopRequiredTestPrompt("go test ./...")
	cfg := intentLoopTestConfig(root)
	opts := IntentLoopOptions{
		PlanExecuteOptions: planexecute.Options{Prompt: prompt},
	}
	state := NewState("resume-incomplete", prompt, IntentLoopContract{}, 1, time.Now())
	promptPath, promptHash, err := SaveCyclePrompt(root, state.SessionID, 1, "cycle prompt")
	if err != nil {
		t.Fatalf("SaveCyclePrompt: %v", err)
	}
	identity := childIdentity(cfg, opts, state.Contract, childVerificationCommands(state, opts))
	state.Status = intentloopcontract.StatusRunning
	state.CurrentCycle = 1
	state.Cycles = []CycleRecord{{
		CycleNumber:          1,
		Status:               "dispatched",
		PlanExecuteSessionID: "child-1",
		CyclePromptPath:      promptPath,
		CyclePromptSHA256:    promptHash,
		ChildIdentity:        &identity,
	}}
	if _, err := SaveState(root, &state); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	var calls int
	opts.Resume = state.SessionID
	opts.RunPlanExecute = func(_ config.Config, opts planexecute.Options, _ planexecute.PromptRunner, _ *telemetry.Recorder, _ io.Writer, _ io.Writer) planexecute.RunResult {
		calls++
		record := verificationTerminal(opts.SessionID, "go test ./...", true)
		return planexecute.RunResult{ExitCode: 0, SessionID: opts.SessionID, Completion: &record}
	}
	opts.EstimateCycleTokens = func(string, planexecute.RunResult, plancontract.PlanExecuteTerminalCompletionRecord) int {
		return 1
	}
	result := Run(cfg, opts, nil, nil, io.Discard, io.Discard)

	if calls != 1 {
		t.Fatalf("calls = %d, want resumed child only", calls)
	}
	if result.ExitCode != 0 || result.Status != intentloopcontract.StatusCompleted {
		t.Fatalf("result = exit %d status %q reason %q", result.ExitCode, result.Status, result.TerminationReason)
	}
	if result.State.CurrentCycle != 1 || len(result.State.Cycles) != 1 || result.State.Cycles[0].Status != "completed" {
		t.Fatalf("resumed cycle not folded into completed state: %#v", result.State.Cycles)
	}
}

func TestRunResumeIncompleteCycleFoldsExistingChildTerminalWithoutDispatch(t *testing.T) {
	root := t.TempDir()
	prompt := intentLoopRequiredTestPrompt("go test ./...")
	cfg := intentLoopTestConfig(root)
	opts := IntentLoopOptions{
		PlanExecuteOptions: planexecute.Options{Prompt: prompt},
	}
	state := NewState("resume-existing-terminal", prompt, IntentLoopContract{}, 1, time.Now())
	promptPath, promptHash, err := SaveCyclePrompt(root, state.SessionID, 1, "cycle prompt")
	if err != nil {
		t.Fatalf("SaveCyclePrompt: %v", err)
	}
	identity := childIdentity(cfg, opts, state.Contract, childVerificationCommands(state, opts))
	state.Status = intentloopcontract.StatusRunning
	state.CurrentCycle = 1
	state.Cycles = []CycleRecord{{
		CycleNumber:          1,
		Status:               "dispatched",
		PlanExecuteSessionID: "child-1",
		CyclePromptPath:      promptPath,
		CyclePromptSHA256:    promptHash,
		ChildIdentity:        &identity,
	}}
	if _, err := SaveState(root, &state); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	terminal := verificationTerminal("child-1", "go test ./...", true)
	if _, err := planexecute.SavePlanExecuteTerminalCompletionRecord(root, terminal); err != nil {
		t.Fatalf("SavePlanExecuteTerminalCompletionRecord: %v", err)
	}

	var calls int
	opts.Resume = state.SessionID
	opts.RunPlanExecute = func(_ config.Config, _ planexecute.Options, _ planexecute.PromptRunner, _ *telemetry.Recorder, _ io.Writer, _ io.Writer) planexecute.RunResult {
		calls++
		return planexecute.RunResult{ExitCode: 99}
	}
	opts.EstimateCycleTokens = func(string, planexecute.RunResult, plancontract.PlanExecuteTerminalCompletionRecord) int {
		return 1
	}
	result := Run(cfg, opts, nil, nil, io.Discard, io.Discard)

	if calls != 0 {
		t.Fatalf("resume dispatched %d child run(s), want 0", calls)
	}
	if result.ExitCode != 0 || result.Status != intentloopcontract.StatusCompleted {
		t.Fatalf("result = exit %d status %q reason %q", result.ExitCode, result.Status, result.TerminationReason)
	}
	if result.State.CurrentCycle != 1 || len(result.State.Cycles) != 1 || result.State.Cycles[0].Status != "completed" {
		t.Fatalf("existing terminal was not folded into completed state: %#v", result.State.Cycles)
	}
}

func TestRunResumeIncompleteCycleBlocksNonResumableChildProgressWithoutDispatch(t *testing.T) {
	root := t.TempDir()
	prompt := intentLoopRequiredTestPrompt("go test ./...")
	cfg := intentLoopTestConfig(root)
	opts := IntentLoopOptions{
		PlanExecuteOptions: planexecute.Options{Prompt: prompt},
	}
	state := NewState("resume-nonresumable-progress", prompt, IntentLoopContract{}, 1, time.Now())
	promptPath, promptHash, err := SaveCyclePrompt(root, state.SessionID, 1, "cycle prompt")
	if err != nil {
		t.Fatalf("SaveCyclePrompt: %v", err)
	}
	identity := childIdentity(cfg, opts, state.Contract, childVerificationCommands(state, opts))
	state.Status = intentloopcontract.StatusRunning
	state.CurrentCycle = 1
	state.Cycles = []CycleRecord{{
		CycleNumber:          1,
		Status:               "dispatched",
		PlanExecuteSessionID: "child-1",
		CyclePromptPath:      promptPath,
		CyclePromptSHA256:    promptHash,
		ChildIdentity:        &identity,
	}}
	if _, err := SaveState(root, &state); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	progress := planexecute.NewProgressState("child-1", "cycle prompt", promptPath, nil, nil, time.Now())
	progress.Phase = plancontract.PlanExecuteProgressPhasePlanning
	progress.Status = plancontract.PlanExecuteProgressStatusRunning
	if _, err := planexecute.SaveProgressState(root, &progress); err != nil {
		t.Fatalf("SaveProgressState: %v", err)
	}

	var calls int
	opts.Resume = state.SessionID
	opts.RunPlanExecute = func(_ config.Config, _ planexecute.Options, _ planexecute.PromptRunner, _ *telemetry.Recorder, _ io.Writer, _ io.Writer) planexecute.RunResult {
		calls++
		return planexecute.RunResult{ExitCode: 99}
	}
	result := Run(cfg, opts, nil, nil, io.Discard, io.Discard)

	if calls != 0 {
		t.Fatalf("resume dispatched %d child run(s), want 0", calls)
	}
	if result.ExitCode != 1 || result.Status != intentloopcontract.StatusBlocked || !strings.Contains(result.TerminationReason, ResumeBlockChildProgressNotResumable) {
		t.Fatalf("result = exit %d status %q reason %q", result.ExitCode, result.Status, result.TerminationReason)
	}
	assertPersistedBlockedResumeCycle(t, root, state.SessionID, ResumeBlockChildProgressNotResumable)
}

func TestRunResumeIncompleteCyclePersistsPromptMismatchBlocker(t *testing.T) {
	root := t.TempDir()
	prompt := intentLoopRequiredTestPrompt("go test ./...")
	cfg := intentLoopTestConfig(root)
	opts := IntentLoopOptions{PlanExecuteOptions: planexecute.Options{Prompt: prompt}}
	state := NewState("resume-prompt-mismatch", prompt, IntentLoopContract{}, 1, time.Now())
	promptPath, _, err := SaveCyclePrompt(root, state.SessionID, 1, "changed cycle prompt")
	if err != nil {
		t.Fatalf("SaveCyclePrompt: %v", err)
	}
	identity := childIdentity(cfg, opts, state.Contract, childVerificationCommands(state, opts))
	state.Status = intentloopcontract.StatusRunning
	state.CurrentCycle = 1
	state.Cycles = []CycleRecord{{
		CycleNumber:          1,
		Status:               intentloopcontract.CycleStatusDispatched,
		PlanExecuteSessionID: "child-1",
		CyclePromptPath:      promptPath,
		CyclePromptSHA256:    sha256Hex("original cycle prompt"),
		ChildIdentity:        &identity,
	}}
	if _, err := SaveState(root, &state); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	var calls int
	opts.Resume = state.SessionID
	opts.RunPlanExecute = func(_ config.Config, _ planexecute.Options, _ planexecute.PromptRunner, _ *telemetry.Recorder, _ io.Writer, _ io.Writer) planexecute.RunResult {
		calls++
		return planexecute.RunResult{ExitCode: 99}
	}
	result := Run(cfg, opts, nil, nil, io.Discard, io.Discard)

	if calls != 0 {
		t.Fatalf("resume dispatched %d child run(s), want 0", calls)
	}
	if result.ExitCode != 1 || result.Status != intentloopcontract.StatusBlocked || !strings.Contains(result.TerminationReason, ResumeBlockPromptMismatch) {
		t.Fatalf("result = exit %d status %q reason %q", result.ExitCode, result.Status, result.TerminationReason)
	}
	assertPersistedBlockedResumeCycle(t, root, state.SessionID, ResumeBlockPromptMismatch)
}

func TestRunResumeIncompleteCyclePersistsChildIdentityMismatchBlocker(t *testing.T) {
	root := t.TempDir()
	prompt := intentLoopRequiredTestPrompt("go test ./...")
	cfg := intentLoopTestConfig(root)
	opts := IntentLoopOptions{PlanExecuteOptions: planexecute.Options{Prompt: prompt}}
	state := NewState("resume-child-identity-mismatch", prompt, IntentLoopContract{}, 1, time.Now())
	promptPath, promptHash, err := SaveCyclePrompt(root, state.SessionID, 1, "cycle prompt")
	if err != nil {
		t.Fatalf("SaveCyclePrompt: %v", err)
	}
	identity := childIdentity(cfg, opts, state.Contract, childVerificationCommands(state, opts))
	identity.ToolManifestSHA256 = "stale-tool-manifest"
	state.Status = intentloopcontract.StatusRunning
	state.CurrentCycle = 1
	state.Cycles = []CycleRecord{{
		CycleNumber:          1,
		Status:               intentloopcontract.CycleStatusDispatched,
		PlanExecuteSessionID: "child-1",
		CyclePromptPath:      promptPath,
		CyclePromptSHA256:    promptHash,
		ChildIdentity:        &identity,
	}}
	if _, err := SaveState(root, &state); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	var calls int
	opts.Resume = state.SessionID
	opts.RunPlanExecute = func(_ config.Config, _ planexecute.Options, _ planexecute.PromptRunner, _ *telemetry.Recorder, _ io.Writer, _ io.Writer) planexecute.RunResult {
		calls++
		return planexecute.RunResult{ExitCode: 99}
	}
	result := Run(cfg, opts, nil, nil, io.Discard, io.Discard)

	if calls != 0 {
		t.Fatalf("resume dispatched %d child run(s), want 0", calls)
	}
	if result.ExitCode != 1 || result.Status != intentloopcontract.StatusBlocked || !strings.Contains(result.TerminationReason, ResumeBlockChildIdentityMismatch) {
		t.Fatalf("result = exit %d status %q reason %q", result.ExitCode, result.Status, result.TerminationReason)
	}
	assertPersistedBlockedResumeCycle(t, root, state.SessionID, ResumeBlockChildIdentityMismatch)
}

func TestRunResumeIncompleteCyclePersistsExternalScopedChangeBlocker(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("current"), 0o600); err != nil {
		t.Fatalf("write scoped file: %v", err)
	}
	prompt := intentLoopRequiredTestPrompt("go test ./...")
	cfg := intentLoopTestConfig(root)
	contract := IntentLoopContract{
		ScopeContract: plancontract.PlanScopeContract{RequiredCoveragePaths: []string{"tracked.txt"}},
	}
	opts := IntentLoopOptions{PlanExecuteOptions: planexecute.Options{Prompt: prompt}}
	state := NewState("resume-external-scoped-change", prompt, contract, 1, time.Now())
	promptPath, promptHash, err := SaveCyclePrompt(root, state.SessionID, 1, "cycle prompt")
	if err != nil {
		t.Fatalf("SaveCyclePrompt: %v", err)
	}
	identity := childIdentity(cfg, opts, state.Contract, childVerificationCommands(state, opts))
	state.Status = intentloopcontract.StatusRunning
	state.CurrentCycle = 1
	state.Cycles = []CycleRecord{{
		CycleNumber:              1,
		Status:                   intentloopcontract.CycleStatusDispatched,
		PlanExecuteSessionID:     "child-1",
		CyclePromptPath:          promptPath,
		CyclePromptSHA256:        promptHash,
		PostRunScopedFingerprint: "stale-fingerprint",
		ChildIdentity:            &identity,
	}}
	if _, err := SaveState(root, &state); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	var calls int
	opts.Resume = state.SessionID
	opts.RunPlanExecute = func(_ config.Config, _ planexecute.Options, _ planexecute.PromptRunner, _ *telemetry.Recorder, _ io.Writer, _ io.Writer) planexecute.RunResult {
		calls++
		return planexecute.RunResult{ExitCode: 99}
	}
	result := Run(cfg, opts, nil, nil, io.Discard, io.Discard)

	if calls != 0 {
		t.Fatalf("resume dispatched %d child run(s), want 0", calls)
	}
	if result.ExitCode != 1 || result.Status != intentloopcontract.StatusBlocked || !strings.Contains(result.TerminationReason, ResumeBlockExternalScopedChange) {
		t.Fatalf("result = exit %d status %q reason %q", result.ExitCode, result.Status, result.TerminationReason)
	}
	assertPersistedBlockedResumeCycle(t, root, state.SessionID, ResumeBlockExternalScopedChange)
}

func assertPersistedBlockedResumeCycle(t *testing.T, root string, sessionID string, wantKind string) {
	t.Helper()
	loaded, _, err := LoadState(root, sessionID)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if loaded.Status != intentloopcontract.StatusBlocked {
		t.Fatalf("persisted status = %q, want %q", loaded.Status, intentloopcontract.StatusBlocked)
	}
	if len(loaded.Cycles) != 1 {
		t.Fatalf("persisted cycles = %d, want 1", len(loaded.Cycles))
	}
	cycle := loaded.Cycles[0]
	if cycle.Status != intentloopcontract.CycleStatusFailed {
		t.Fatalf("persisted cycle status = %q, want %q", cycle.Status, intentloopcontract.CycleStatusFailed)
	}
	if !strings.Contains(cycle.FailureKind, wantKind) {
		t.Fatalf("persisted failure kind = %q, want containing %q", cycle.FailureKind, wantKind)
	}
	if !strings.Contains(cycle.FailureReason, wantKind) {
		t.Fatalf("persisted failure reason = %q, want containing %q", cycle.FailureReason, wantKind)
	}
}

func TestEmitIntentLoopJSONEventRejectsUnknownContractEvent(t *testing.T) {
	var stdout bytes.Buffer

	if err := emitIntentLoopJSONEvent(&stdout, true, intentloopcontract.EventType("not_real"), nil); err == nil {
		t.Fatal("expected unknown event type error")
	}

	if stdout.Len() != 0 {
		t.Fatalf("unexpected JSON output for rejected event: %s", stdout.String())
	}
}

func intentLoopTestConfig(root string) config.Config {
	return config.Config{
		Workspace: config.Workspace{Root: root},
	}
}

func intentLoopRequiredTestPrompt(command string) string {
	return `Implement this.

Typed plan execution contract:
{
  "atoms": [{
    "atom_id": "impl",
    "required_tests": [{"id": "required", "command": "` + command + `"}],
    "terminal_evidence_requirements": {
      "explicit": true,
      "require_verification": true,
      "require_required_tests": true
    }
  }]
}`
}

func verificationTerminal(sessionID string, command string, passed bool) plancontract.PlanExecuteTerminalCompletionRecord {
	status := plancontract.PhaseEvidenceStatusFailed
	errText := command + " failed"
	exitCode := 1
	if passed {
		status = plancontract.PhaseEvidenceStatusPassed
		errText = ""
		exitCode = 0
	}
	return plancontract.NormalizePlanExecuteTerminalCompletionRecord(plancontract.PlanExecuteTerminalCompletionRecord{
		SchemaVersion:        plancontract.PlanExecuteTerminalCompletionSchemaVersion,
		Type:                 plancontract.PlanExecuteTerminalCompletionRecordType,
		TS:                   time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC).Format(time.RFC3339Nano),
		SessionID:            sessionID,
		Phase:                plancontract.PlanExecuteProgressPhaseComplete,
		Status:               plancontract.PlanExecuteTerminalStatusCompleted,
		ReviewStatus:         plancontract.PlanExecuteReviewStatusSkipped,
		ExitCode:             exitCode,
		ReviewRepairSkipped:  true,
		VerificationCommands: []plancontract.VerificationCommand{{Command: command, Source: "test"}},
		Verification: []plancontract.PhaseEvidenceCheck{{
			Kind:    "verification",
			Command: command,
			Status:  status,
			Error:   errText,
		}},
	})
}

func evidenceGapsContain(gaps []EvidenceGap, requirement string, detailSubstring string) bool {
	for _, gap := range gaps {
		if gap.Requirement == requirement && strings.Contains(gap.Detail, detailSubstring) {
			return true
		}
	}
	return false
}
