package commandcheck

import (
	"context"
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
	if run.Diagnostic != run.DisplayOutput {
		t.Fatalf("Diagnostic = %q, want display output", run.Diagnostic)
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

func TestRunAppliesInfrastructureExitCode(t *testing.T) {
	run := Run(t.Context(), Request{
		Argv:                   []string{"definitely-not-run"},
		AllowedArgv:            func([]string) bool { return false },
		InfrastructureExitCode: 2,
	})

	if run.Failure != FailureUnsupported || run.ExitCode != 2 {
		t.Fatalf("run = %#v, want unsupported with policy exit 2", run)
	}
}

func TestRunUsesErrorAsFallbackDiagnostic(t *testing.T) {
	run := Run(t.Context(), Request{
		Argv:                   []string{"definitely-not-a-real-idiom-commandcheck-binary"},
		InfrastructureExitCode: 2,
	})

	if run.Failure != FailureSpawn || run.ExitCode != 2 {
		t.Fatalf("run = %#v, want spawn failure with policy exit 2", run)
	}
	if run.Diagnostic == "" || !strings.Contains(run.Diagnostic, "definitely-not-a-real-idiom-commandcheck-binary") {
		t.Fatalf("Diagnostic = %q, want spawn error text", run.Diagnostic)
	}
}

func TestRunContextOverridePrecedesTimeoutPolicy(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	run := Run(t.Context(), Request{
		Argv:            []string{"definitely-not-run"},
		AllowedArgv:     func([]string) bool { return false },
		Context:         ctx,
		ContextExitCode: 124,
		TimeoutErr:      context.DeadlineExceeded,
		TimeoutExitCode: 125,
	})

	if run.ExitCode != 124 {
		t.Fatalf("ExitCode = %d, want context exit code", run.ExitCode)
	}
	if run.Err != context.Canceled {
		t.Fatalf("Err = %v, want context.Canceled", run.Err)
	}
	if run.Diagnostic != context.Canceled.Error() {
		t.Fatalf("Diagnostic = %q, want context error", run.Diagnostic)
	}
}

func TestRunAppliesTimeoutPolicy(t *testing.T) {
	run := Run(t.Context(), Request{
		Argv:            commandCheckHelperArgv("sleep"),
		Env:             commandCheckHelperEnv(),
		Timeout:         20 * time.Millisecond,
		TimeoutErr:      context.DeadlineExceeded,
		TimeoutExitCode: 124,
	})

	if run.Failure != FailureTimeout || run.ExitCode != 124 {
		t.Fatalf("run = %#v, want timeout with exit 124", run)
	}
	if run.Err != context.DeadlineExceeded {
		t.Fatalf("Err = %v, want deadline exceeded", run.Err)
	}
	if run.Diagnostic != context.DeadlineExceeded.Error() {
		t.Fatalf("Diagnostic = %q, want timeout error", run.Diagnostic)
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
