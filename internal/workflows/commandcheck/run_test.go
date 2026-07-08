package commandcheck

import (
	"fmt"
	"os"
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
	case "sleep":
		time.Sleep(5 * time.Second)
		os.Exit(0)
	}

	os.Exit(2)
}
