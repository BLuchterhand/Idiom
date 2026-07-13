package intentloop

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	intentloopcontract "idiom/internal/contracts/intentloop"
	plancontract "idiom/internal/contracts/plan"
	"idiom/internal/evidence/telemetry"
	"idiom/internal/platform/config"
	"idiom/internal/workflows/planexecute"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
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

var _ = ginkgo.Describe("Intent-loop execution", func() {
	ginkgo.It("requires an injected plan-execute runner", func() {
		var stderr bytes.Buffer

		result := Run(intentLoopTestConfig(ginkgo.GinkgoT().TempDir()), IntentLoopOptions{
			SessionID: "loop-missing-runner",
			PlanExecuteOptions: planexecute.Options{
				Prompt: "fix it",
			},
		}, nil, nil, &bytes.Buffer{}, &stderr)

		gomega.Expect(result.ExitCode).To(gomega.Equal(1))
		gomega.Expect(result.Status).To(gomega.Equal(intentloopcontract.StatusFailed))
		gomega.Expect(result.TerminationReason).To(gomega.ContainSubstring("injected plan-execute runner"))
		gomega.Expect(stderr.String()).To(gomega.ContainSubstring("injected plan-execute runner"))
	})

	ginkgo.It("completes after a recoverable verification gap", ginkgo.Label("development-scenario", "integration", "contract"), func() {
		root := ginkgo.GinkgoT().TempDir()
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
				gomega.Expect(opts.ScopeExtractionPolicy).To(gomega.Equal(planexecute.ScopeExtractionFrozen))
				gomega.Expect(opts.VerificationPlanExplicitCommands).To(gomega.Equal([]string{"go test ./..."}))

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

		gomega.Expect(result.ExitCode).To(gomega.Equal(0), "reason=%s", result.TerminationReason)
		gomega.Expect(result.Status).To(gomega.Equal(intentloopcontract.StatusCompleted))
		gomega.Expect(calls).To(gomega.Equal(2))
		gomega.Expect(result.State.Cycles).To(gomega.HaveLen(2))
		gomega.Expect(result.State.Cycles[0].RemainingFailures).NotTo(gomega.BeEmpty())
		gomega.Expect(result.State.Cycles[1].RemainingFailures).To(gomega.BeEmpty())
		gomega.Expect(childPrompts).To(gomega.HaveLen(2))
		gomega.Expect(childPrompts[1]).To(gomega.And(gomega.ContainSubstring("## Prior Cycles"), gomega.ContainSubstring("verification:go test ./...")))
		gomega.Expect(result.StatePath).To(gomega.Equal(".idiom/intent-loop/sessions/" + result.SessionID + ".json"))
	})

	ginkgo.It("stops before dispatch when the start JSON event cannot be written", func() {
		root := ginkgo.GinkgoT().TempDir()
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

		gomega.Expect(calls).To(gomega.BeZero())
		expectRunFailure(result, intentloopcontract.StatusFailed, "json_event_write_failed")
	})

	ginkgo.It("fails a terminal resume when its JSON event cannot be written", func() {
		root := ginkgo.GinkgoT().TempDir()
		state := NewState("terminal-json-failure", intentLoopRequiredTestPrompt("go test ./..."), IntentLoopContract{}, 3, time.Now())
		state.Status = intentloopcontract.StatusCompleted
		_, err := SaveState(root, &state)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

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

		gomega.Expect(calls).To(gomega.BeZero())
		expectRunFailure(result, intentloopcontract.StatusFailed, "json_event_write_failed")
	})

	ginkgo.It("fails an incomplete resume when cycle-complete JSON cannot be written", func() {
		root := ginkgo.GinkgoT().TempDir()
		prompt := intentLoopRequiredTestPrompt("go test ./...")
		cfg := intentLoopTestConfig(root)
		opts := IntentLoopOptions{PlanExecuteOptions: planexecute.Options{Prompt: prompt, JSONEvents: true}}
		state := NewState("resume-json-failure", prompt, IntentLoopContract{}, 1, time.Now())
		promptPath, promptHash, err := SaveCyclePrompt(root, state.SessionID, 1, "cycle prompt")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
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
		_, err = SaveState(root, &state)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		terminal := verificationTerminal("child-1", "go test ./...", true)
		_, err = planexecute.SavePlanExecuteTerminalCompletionRecord(root, terminal)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		stdout := &intentLoopFailAfterWriter{allowedWrites: 1, err: errors.New("stdout closed")}
		result := Run(cfg, IntentLoopOptions{
			PlanExecuteOptions: opts.PlanExecuteOptions,
			Resume:             state.SessionID,
			RunPlanExecute: func(config.Config, planexecute.Options, planexecute.PromptRunner, *telemetry.Recorder, io.Writer, io.Writer) planexecute.RunResult {
				ginkgo.Fail("existing terminal resume should not dispatch child")
				return planexecute.RunResult{}
			},
			EstimateCycleTokens: func(string, planexecute.RunResult, plancontract.PlanExecuteTerminalCompletionRecord) int {
				return 1
			},
		}, nil, nil, stdout, io.Discard)

		expectRunFailure(result, intentloopcontract.StatusFailed, "json_event_write_failed")
		gomega.Expect(stdout.writes).To(gomega.Equal(2))
	})

	ginkgo.It("stops when a failure set repeats", func() {
		root := ginkgo.GinkgoT().TempDir()
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

		gomega.Expect(result.Status).To(gomega.Equal(intentloopcontract.StatusNoProgress), "reason=%s", result.TerminationReason)
		gomega.Expect(result.ExitCode).To(gomega.Equal(1))
		gomega.Expect(calls).To(gomega.Equal(2))
		gomega.Expect(result.State.SeenFailureSets).To(gomega.HaveLen(1))
	})

	ginkgo.It("stops when the declared cycle budget is exhausted", func() {
		root := ginkgo.GinkgoT().TempDir()
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

		gomega.Expect(result.Status).To(gomega.Equal(intentloopcontract.StatusBudgetExhausted))
		gomega.Expect(result.State.BudgetExhausted).To(gomega.BeTrue())
		gomega.Expect(calls).To(gomega.Equal(1))
	})

	ginkgo.It("does not complete from a failed terminal record with a zero wrapper exit", func() {
		root := ginkgo.GinkgoT().TempDir()
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

		gomega.Expect(result.Status).NotTo(gomega.Equal(intentloopcontract.StatusCompleted))
		gomega.Expect(result.ExitCode).NotTo(gomega.BeZero())
		gomega.Expect(result.State.Cycles).To(gomega.HaveLen(1))
		gomega.Expect(result.State.Cycles[0].Status).To(gomega.Equal(intentloopcontract.CycleStatusFailed))
		gomega.Expect(result.State.Cycles[0].FailureKind).To(gomega.Equal(string(plancontract.PlanExecuteFailureExecutorFailed)))
	})

	ginkgo.It("does not complete from a terminal record with validation failures", func() {
		root := ginkgo.GinkgoT().TempDir()
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

		gomega.Expect(result.Status).NotTo(gomega.Equal(intentloopcontract.StatusCompleted))
		gomega.Expect(result.ExitCode).NotTo(gomega.BeZero())
		gomega.Expect(result.State.Cycles).To(gomega.HaveLen(1))
		gomega.Expect(result.State.Cycles[0].Status).To(gomega.Equal(intentloopcontract.CycleStatusFailed))
		gomega.Expect(evidenceGapsContain(result.State.Cycles[0].RemainingFailures, "terminal_completion", "terminal completion validation failure")).To(gomega.BeTrue())
	})

	ginkgo.It("returns a completed terminal session without dispatching a child", func() {
		root := ginkgo.GinkgoT().TempDir()
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
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

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

		gomega.Expect(calls).To(gomega.BeZero())
		gomega.Expect(result.ExitCode).To(gomega.BeZero())
		gomega.Expect(result.Status).To(gomega.Equal(intentloopcontract.StatusCompleted))
		gomega.Expect(result.StatePath).To(gomega.Equal(statePath))
		gomega.Expect(result.State.CurrentCycle).To(gomega.Equal(1))
		gomega.Expect(result.State.Cycles).To(gomega.HaveLen(1))
	})

	ginkgo.It("returns a blocked terminal session without dispatching a child", func() {
		root := ginkgo.GinkgoT().TempDir()
		state := NewState("blocked-session", intentLoopRequiredTestPrompt("go test ./..."), IntentLoopContract{}, 3, time.Now())
		state.Status = intentloopcontract.StatusBlocked
		state.TerminationReason = "manual blocker"
		_, err := SaveState(root, &state)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

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

		gomega.Expect(calls).To(gomega.BeZero())
		gomega.Expect(result.ExitCode).To(gomega.Equal(1))
		gomega.Expect(result.Status).To(gomega.Equal(intentloopcontract.StatusBlocked))
		gomega.Expect(result.TerminationReason).To(gomega.Equal("manual blocker"))
	})

	ginkgo.It("resumes and folds a successful incomplete child terminal", ginkgo.Label("development-scenario", "integration", "contract"), func() {
		root := ginkgo.GinkgoT().TempDir()
		prompt := intentLoopRequiredTestPrompt("go test ./...")
		cfg := intentLoopTestConfig(root)
		opts := IntentLoopOptions{
			PlanExecuteOptions: planexecute.Options{Prompt: prompt},
		}
		state := NewState("resume-incomplete", prompt, IntentLoopContract{}, 1, time.Now())
		promptPath, promptHash, err := SaveCyclePrompt(root, state.SessionID, 1, "cycle prompt")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
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
		_, err = SaveState(root, &state)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

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

		gomega.Expect(calls).To(gomega.Equal(1))
		gomega.Expect(result.ExitCode).To(gomega.BeZero(), "reason=%s", result.TerminationReason)
		gomega.Expect(result.Status).To(gomega.Equal(intentloopcontract.StatusCompleted))
		gomega.Expect(result.State.CurrentCycle).To(gomega.Equal(1))
		gomega.Expect(result.State.Cycles).To(gomega.HaveLen(1))
		gomega.Expect(result.State.Cycles[0].Status).To(gomega.Equal(intentloopcontract.CycleStatusCompleted))
	})

	ginkgo.It("folds an existing child terminal without redispatch", func() {
		root := ginkgo.GinkgoT().TempDir()
		prompt := intentLoopRequiredTestPrompt("go test ./...")
		cfg := intentLoopTestConfig(root)
		opts := IntentLoopOptions{
			PlanExecuteOptions: planexecute.Options{Prompt: prompt},
		}
		state := NewState("resume-existing-terminal", prompt, IntentLoopContract{}, 1, time.Now())
		promptPath, promptHash, err := SaveCyclePrompt(root, state.SessionID, 1, "cycle prompt")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
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
		_, err = SaveState(root, &state)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		terminal := verificationTerminal("child-1", "go test ./...", true)
		_, err = planexecute.SavePlanExecuteTerminalCompletionRecord(root, terminal)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

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

		gomega.Expect(calls).To(gomega.BeZero())
		gomega.Expect(result.ExitCode).To(gomega.BeZero(), "reason=%s", result.TerminationReason)
		gomega.Expect(result.Status).To(gomega.Equal(intentloopcontract.StatusCompleted))
		gomega.Expect(result.State.CurrentCycle).To(gomega.Equal(1))
		gomega.Expect(result.State.Cycles).To(gomega.HaveLen(1))
		gomega.Expect(result.State.Cycles[0].Status).To(gomega.Equal(intentloopcontract.CycleStatusCompleted))
	})

	ginkgo.It("blocks non-resumable child progress without redispatch", func() {
		root := ginkgo.GinkgoT().TempDir()
		prompt := intentLoopRequiredTestPrompt("go test ./...")
		cfg := intentLoopTestConfig(root)
		opts := IntentLoopOptions{
			PlanExecuteOptions: planexecute.Options{Prompt: prompt},
		}
		state := NewState("resume-nonresumable-progress", prompt, IntentLoopContract{}, 1, time.Now())
		promptPath, promptHash, err := SaveCyclePrompt(root, state.SessionID, 1, "cycle prompt")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
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
		_, err = SaveState(root, &state)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		progress := planexecute.NewProgressState("child-1", "cycle prompt", promptPath, nil, nil, time.Now())
		progress.Phase = plancontract.PlanExecuteProgressPhasePlanning
		progress.Status = plancontract.PlanExecuteProgressStatusRunning
		_, err = planexecute.SaveProgressState(root, &progress)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		var calls int
		opts.Resume = state.SessionID
		opts.RunPlanExecute = func(_ config.Config, _ planexecute.Options, _ planexecute.PromptRunner, _ *telemetry.Recorder, _ io.Writer, _ io.Writer) planexecute.RunResult {
			calls++
			return planexecute.RunResult{ExitCode: 99}
		}
		result := Run(cfg, opts, nil, nil, io.Discard, io.Discard)

		gomega.Expect(calls).To(gomega.BeZero())
		expectRunFailure(result, intentloopcontract.StatusBlocked, ResumeBlockChildProgressNotResumable)
		assertPersistedBlockedResumeCycle(root, state.SessionID, ResumeBlockChildProgressNotResumable)
	})

	ginkgo.It("persists a cycle-prompt mismatch blocker without redispatch", func() {
		root := ginkgo.GinkgoT().TempDir()
		prompt := intentLoopRequiredTestPrompt("go test ./...")
		cfg := intentLoopTestConfig(root)
		opts := IntentLoopOptions{PlanExecuteOptions: planexecute.Options{Prompt: prompt}}
		state := NewState("resume-prompt-mismatch", prompt, IntentLoopContract{}, 1, time.Now())
		promptPath, _, err := SaveCyclePrompt(root, state.SessionID, 1, "changed cycle prompt")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
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
		_, err = SaveState(root, &state)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		var calls int
		opts.Resume = state.SessionID
		opts.RunPlanExecute = func(_ config.Config, _ planexecute.Options, _ planexecute.PromptRunner, _ *telemetry.Recorder, _ io.Writer, _ io.Writer) planexecute.RunResult {
			calls++
			return planexecute.RunResult{ExitCode: 99}
		}
		result := Run(cfg, opts, nil, nil, io.Discard, io.Discard)

		gomega.Expect(calls).To(gomega.BeZero())
		expectRunFailure(result, intentloopcontract.StatusBlocked, ResumeBlockPromptMismatch)
		assertPersistedBlockedResumeCycle(root, state.SessionID, ResumeBlockPromptMismatch)
	})

	ginkgo.It("persists a child-identity mismatch blocker without redispatch", func() {
		root := ginkgo.GinkgoT().TempDir()
		prompt := intentLoopRequiredTestPrompt("go test ./...")
		cfg := intentLoopTestConfig(root)
		opts := IntentLoopOptions{PlanExecuteOptions: planexecute.Options{Prompt: prompt}}
		state := NewState("resume-child-identity-mismatch", prompt, IntentLoopContract{}, 1, time.Now())
		promptPath, promptHash, err := SaveCyclePrompt(root, state.SessionID, 1, "cycle prompt")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
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
		_, err = SaveState(root, &state)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		var calls int
		opts.Resume = state.SessionID
		opts.RunPlanExecute = func(_ config.Config, _ planexecute.Options, _ planexecute.PromptRunner, _ *telemetry.Recorder, _ io.Writer, _ io.Writer) planexecute.RunResult {
			calls++
			return planexecute.RunResult{ExitCode: 99}
		}
		result := Run(cfg, opts, nil, nil, io.Discard, io.Discard)

		gomega.Expect(calls).To(gomega.BeZero())
		expectRunFailure(result, intentloopcontract.StatusBlocked, ResumeBlockChildIdentityMismatch)
		assertPersistedBlockedResumeCycle(root, state.SessionID, ResumeBlockChildIdentityMismatch)
	})

	ginkgo.It("persists an external scoped-change blocker without redispatch", func() {
		root := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("current"), 0o600)).To(gomega.Succeed())
		prompt := intentLoopRequiredTestPrompt("go test ./...")
		cfg := intentLoopTestConfig(root)
		contract := IntentLoopContract{
			ScopeContract: plancontract.PlanScopeContract{RequiredCoveragePaths: []string{"tracked.txt"}},
		}
		opts := IntentLoopOptions{PlanExecuteOptions: planexecute.Options{Prompt: prompt}}
		state := NewState("resume-external-scoped-change", prompt, contract, 1, time.Now())
		promptPath, promptHash, err := SaveCyclePrompt(root, state.SessionID, 1, "cycle prompt")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
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
		_, err = SaveState(root, &state)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		var calls int
		opts.Resume = state.SessionID
		opts.RunPlanExecute = func(_ config.Config, _ planexecute.Options, _ planexecute.PromptRunner, _ *telemetry.Recorder, _ io.Writer, _ io.Writer) planexecute.RunResult {
			calls++
			return planexecute.RunResult{ExitCode: 99}
		}
		result := Run(cfg, opts, nil, nil, io.Discard, io.Discard)

		gomega.Expect(calls).To(gomega.BeZero())
		expectRunFailure(result, intentloopcontract.StatusBlocked, ResumeBlockExternalScopedChange)
		assertPersistedBlockedResumeCycle(root, state.SessionID, ResumeBlockExternalScopedChange)
	})

	ginkgo.It("rejects an unknown JSON event contract without output", func() {
		var stdout bytes.Buffer

		gomega.Expect(emitIntentLoopJSONEvent(&stdout, true, intentloopcontract.EventType("not_real"), nil)).To(gomega.HaveOccurred())
		gomega.Expect(stdout.String()).To(gomega.BeEmpty())
	})
})

func expectRunFailure(result IntentLoopResult, status intentloopcontract.Status, reasonSubstring string) {
	gomega.Expect(result.ExitCode).To(gomega.Equal(1))
	gomega.Expect(result.Status).To(gomega.Equal(status))
	gomega.Expect(result.TerminationReason).To(gomega.ContainSubstring(reasonSubstring))
}

func assertPersistedBlockedResumeCycle(root, sessionID, wantKind string) {
	loaded, _, err := LoadState(root, sessionID)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(loaded.Status).To(gomega.Equal(intentloopcontract.StatusBlocked))
	gomega.Expect(loaded.Cycles).To(gomega.HaveLen(1))
	cycle := loaded.Cycles[0]
	gomega.Expect(cycle.Status).To(gomega.Equal(intentloopcontract.CycleStatusFailed))
	gomega.Expect(cycle.FailureKind).To(gomega.ContainSubstring(wantKind))
	gomega.Expect(cycle.FailureReason).To(gomega.ContainSubstring(wantKind))
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
