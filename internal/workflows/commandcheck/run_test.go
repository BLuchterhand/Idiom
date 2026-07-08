package commandcheck

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunRejectsUnsupportedArgvBeforeExecution(t *testing.T) {
	run := Run(t.Context(), Request{
		Argv:        []string{"definitely-not-run"},
		AllowedArgv: func([]string) bool { return false },
	})

	if run.Failure != FailureUnsupported {
		t.Fatalf("Failure = %q, want unsupported", run.Failure)
	}
	if run.ExitCode != -1 || run.Err != nil {
		t.Fatalf("not-run metadata = %#v", run)
	}
	if run.StartedAt.IsZero() || run.CompletedAt.IsZero() {
		t.Fatalf("timestamps not populated: %#v", run)
	}
}

func TestRunRequiresOperandAfterPrefix(t *testing.T) {
	run := Run(t.Context(), Request{
		Argv:                      []string{"go", "test"},
		AllowedArgv:               func([]string) bool { return true },
		PrefixArgv:                func(args []string) []string { return args },
		RequireOperandAfterPrefix: true,
	})

	if run.Failure != FailureMissingOperand {
		t.Fatalf("Failure = %q, want missing_operand", run.Failure)
	}
	if run.ExitCode != -1 || run.Err != nil {
		t.Fatalf("not-run metadata = %#v", run)
	}
}

func TestRunClassifiesExitAndAppliesDisplayPolicy(t *testing.T) {
	run := Run(t.Context(), Request{
		Argv:         commandCheckHelperArgv("exit-output"),
		Env:          commandCheckHelperEnv(),
		CaptureBytes: DiagnosticCaptureBytes,
		DisplayBytes: 16,
		Truncation:   TruncatePostflight,
		MergeOutput:  true,
	})

	if run.Failure != FailureExit || run.ExitCode != 7 {
		t.Fatalf("run = %#v, want exit 7", run)
	}
	if !strings.Contains(run.Output, "stdout-marker") || !strings.Contains(run.Output, "stderr-marker") {
		t.Fatalf("combined output missing markers: %q", run.Output)
	}
	if !strings.Contains(run.DisplayOutput, "... (truncated; rerun the check for full output)") {
		t.Fatalf("display output missing truncation marker: %q", run.DisplayOutput)
	}
}

func TestRunCapturesExitStdoutAndStderr(t *testing.T) {
	run := Run(t.Context(), Request{
		Argv: commandCheckHelperArgv("exit-output"),
		Env:  commandCheckHelperEnv(),
	})

	if run.Failure != FailureExit || run.ExitCode != 7 {
		t.Fatalf("run = %#v, want exit 7", run)
	}
	if !strings.Contains(run.Stdout, "stdout-marker") {
		t.Fatalf("Stdout = %q, want stdout marker", run.Stdout)
	}
	if !strings.Contains(run.Stderr, "stderr-marker") {
		t.Fatalf("Stderr = %q, want stderr marker", run.Stderr)
	}
}

func TestRunClassifiesTimeout(t *testing.T) {
	run := Run(t.Context(), Request{
		Argv:    commandCheckHelperArgv("sleep"),
		Env:     commandCheckHelperEnv(),
		Timeout: 20 * time.Millisecond,
	})

	if run.Failure != FailureTimeout {
		t.Fatalf("Failure = %q, want timeout; run=%#v", run.Failure, run)
	}
	if run.ExitCode != -1 {
		t.Fatalf("ExitCode = %d, want -1", run.ExitCode)
	}
}

func TestRunUsesWorkingDirectory(t *testing.T) {
	root := t.TempDir()
	run := Run(t.Context(), Request{
		Argv: commandCheckHelperArgv("pwd"),
		Dir:  root,
		Env:  commandCheckHelperEnv(),
	})

	if run.Failure != FailureNone {
		t.Fatalf("run failed: %#v", run)
	}
	got, err := filepath.EvalSymlinks(strings.TrimSpace(run.Stdout))
	if err != nil {
		t.Fatalf("cannot resolve working directory %q: %v", strings.TrimSpace(run.Stdout), err)
	}
	want, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("cannot resolve temp root %q: %v", root, err)
	}
	if got != want {
		t.Fatalf("working directory = %q, want %q", got, want)
	}
}

func TestProjectOutcomePreservesExitResultAndDiagnostic(t *testing.T) {
	runErr := errors.New("exit status 7")
	outcome := ProjectOutcome(Result{
		Failure:       FailureExit,
		ExitCode:      7,
		Err:           runErr,
		Stdout:        "out",
		Stderr:        "err",
		Output:        "out\nerr",
		DisplayOutput: "display text",
	}, OutcomePolicy{})

	if outcome.Failure != FailureExit {
		t.Fatalf("Failure = %q, want exit", outcome.Failure)
	}
	if outcome.Failure != FailureExit || outcome.ExitCode != 7 || outcome.Err != runErr {
		t.Fatalf("outcome metadata = %#v, want exit failure", outcome)
	}
	if outcome.Diagnostic != "display text" {
		t.Fatalf("Diagnostic = %q, want display text", outcome.Diagnostic)
	}
	if outcome.Stdout != "out" || outcome.Stderr != "err" || outcome.Output != "out\nerr" || outcome.DisplayOutput != "display text" {
		t.Fatalf("captured output changed: %#v", outcome)
	}
}

func TestProjectOutcomeUsesErrorAsFallbackDiagnostic(t *testing.T) {
	runErr := errors.New("spawn failed")
	outcome := ProjectOutcome(Result{
		Failure:  FailureSpawn,
		ExitCode: -1,
		Err:      runErr,
	}, OutcomePolicy{InfrastructureExitCode: 2})

	if outcome.ExitCode != 2 {
		t.Fatalf("ExitCode = %d, want policy code 2", outcome.ExitCode)
	}
	if outcome.Diagnostic != "spawn failed" {
		t.Fatalf("Diagnostic = %q, want err text", outcome.Diagnostic)
	}
}

func TestProjectOutcomeContextOverridePrecedesTimeoutPolicy(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	outcome := ProjectOutcome(Result{
		Failure:  FailureTimeout,
		ExitCode: -1,
		Err:      errors.New("process timeout"),
	}, OutcomePolicy{
		Context:         ctx,
		ContextExitCode: 124,
		TimeoutErr:      context.DeadlineExceeded,
		TimeoutExitCode: 125,
	})

	if outcome.ExitCode != 124 {
		t.Fatalf("ExitCode = %d, want context exit code", outcome.ExitCode)
	}
	if outcome.Err != context.Canceled {
		t.Fatalf("Err = %v, want context.Canceled", outcome.Err)
	}
	if outcome.Diagnostic != context.Canceled.Error() {
		t.Fatalf("Diagnostic = %q, want context error", outcome.Diagnostic)
	}
}

func TestProjectOutcomeAppliesTimeoutPolicy(t *testing.T) {
	outcome := ProjectOutcome(Result{
		Failure:  FailureTimeout,
		ExitCode: -1,
		Err:      errors.New("process timeout"),
	}, OutcomePolicy{
		TimeoutErr:      context.DeadlineExceeded,
		TimeoutExitCode: 124,
	})

	if outcome.ExitCode != 124 {
		t.Fatalf("ExitCode = %d, want timeout exit code", outcome.ExitCode)
	}
	if outcome.Err != context.DeadlineExceeded {
		t.Fatalf("Err = %v, want deadline exceeded", outcome.Err)
	}
	if outcome.Diagnostic != context.DeadlineExceeded.Error() {
		t.Fatalf("Diagnostic = %q, want timeout error", outcome.Diagnostic)
	}
}

func commandCheckHelperArgv(mode string) []string {
	return []string{os.Args[0], "-test.run=TestCommandCheckHelperProcess", "--", mode}
}

func commandCheckHelperEnv() []string {
	return []string{"GO_WANT_COMMANDCHECK_HELPER=1"}
}

func TestCommandCheckHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_COMMANDCHECK_HELPER") != "1" {
		return
	}

	sep := -1
	for i, arg := range os.Args {
		if arg == "--" {
			sep = i
			break
		}
	}
	if sep < 0 || sep+1 >= len(os.Args) {
		os.Exit(2)
	}

	switch os.Args[sep+1] {
	case "exit-output":
		fmt.Println("stdout-marker " + strings.Repeat("x", 80))
		fmt.Fprintln(os.Stderr, "stderr-marker")
		os.Exit(7)
	case "pwd":
		wd, err := os.Getwd()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		fmt.Fprint(os.Stdout, wd)
		os.Exit(0)
	case "sleep":
		time.Sleep(5 * time.Second)
		os.Exit(0)
	}

	os.Exit(2)
}
