package intentloop

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"

	"idiom/internal/evidence/telemetry"
	"idiom/internal/platform/config"
	"idiom/internal/workflows/planexecute"
)

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
		EstimateCycleTokens: func(string, planexecute.RunResult, planexecute.PlanExecuteTerminalCompletionRecord) int {
			return 10
		},
	}, nil, nil, &bytes.Buffer{}, &bytes.Buffer{})

	if result.ExitCode != 0 || result.Status != StatusCompleted {
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
		EstimateCycleTokens: func(string, planexecute.RunResult, planexecute.PlanExecuteTerminalCompletionRecord) int {
			return 1
		},
	}, nil, nil, io.Discard, io.Discard)

	if result.Status != StatusNoProgress || result.ExitCode != 1 {
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
		EstimateCycleTokens: func(string, planexecute.RunResult, planexecute.PlanExecuteTerminalCompletionRecord) int {
			return 6
		},
	}, nil, nil, io.Discard, io.Discard)

	if result.Status != StatusBudgetExhausted || !result.State.BudgetExhausted {
		t.Fatalf("result status = %q exhausted=%v", result.Status, result.State.BudgetExhausted)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want budget exhaustion after first cycle", calls)
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

func verificationTerminal(sessionID string, command string, passed bool) planexecute.PlanExecuteTerminalCompletionRecord {
	status := "failed"
	errText := command + " failed"
	exitCode := 1
	if passed {
		status = "passed"
		errText = ""
		exitCode = 0
	}
	return planexecute.NormalizePlanExecuteTerminalCompletionRecord(planexecute.PlanExecuteTerminalCompletionRecord{
		SchemaVersion:        planexecute.PlanExecuteTerminalCompletionSchemaVersion,
		Type:                 planexecute.PlanExecuteTerminalCompletionRecordType,
		TS:                   time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC).Format(time.RFC3339Nano),
		SessionID:            sessionID,
		Phase:                planexecute.ProgressPhaseComplete,
		Status:               planexecute.ProgressStatusCompleted,
		ReviewStatus:         planexecute.ProgressStatusSkipped,
		ExitCode:             exitCode,
		ReviewRepairSkipped:  true,
		VerificationCommands: []planexecute.VerificationCommand{{Command: command, Source: "test"}},
		Verification: []planexecute.PhaseEvidenceCheck{{
			Kind:    "verification",
			Command: command,
			Status:  status,
			Error:   errText,
		}},
	})
}
