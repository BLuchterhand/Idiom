package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestRunImplementationDispatchCommandCapturesExitAndOutput(t *testing.T) {
	result := runImplementationDispatchCommand(context.Background(), t.TempDir(), time.Second, os.Args[0], implementationDispatchCommandHelperArgs("exit", "7")...)

	if result.ExitCode != 7 {
		t.Fatalf("ExitCode = %d, want 7; result=%#v", result.ExitCode, result)
	}
	if result.Err == nil {
		t.Fatalf("Err is nil for non-zero exit; result=%#v", result)
	}
	if strings.TrimSpace(result.Stdout) != "stdout text" {
		t.Fatalf("Stdout = %q", result.Stdout)
	}
	if strings.TrimSpace(result.Stderr) != "stderr text" {
		t.Fatalf("Stderr = %q", result.Stderr)
	}
}

func TestRunImplementationDispatchCommandUsesWorkingDirectory(t *testing.T) {
	root := t.TempDir()
	result := runImplementationDispatchCommand(context.Background(), root, time.Second, os.Args[0], implementationDispatchCommandHelperArgs("pwd")...)

	if result.Err != nil {
		t.Fatalf("command failed: %#v", result)
	}
	if got := filepath.Clean(strings.TrimSpace(result.Stdout)); got != filepath.Clean(root) {
		t.Fatalf("working directory = %q, want %q", got, filepath.Clean(root))
	}
}

func TestRunImplementationDispatchCommandMissingBinaryIsLaunchFailure(t *testing.T) {
	result := runImplementationDispatchCommand(context.Background(), t.TempDir(), time.Second, filepath.Join(t.TempDir(), "missing-binary"))

	if result.ExitCode != 2 {
		t.Fatalf("ExitCode = %d, want launch failure code 2; result=%#v", result.ExitCode, result)
	}
	if result.Err == nil {
		t.Fatalf("Err is nil for missing binary; result=%#v", result)
	}
}

func TestRunImplementationDispatchCommandTimeoutUsesContractCode(t *testing.T) {
	result := runImplementationDispatchCommand(context.Background(), t.TempDir(), 10*time.Millisecond, os.Args[0], implementationDispatchCommandHelperArgs("sleep")...)

	if result.ExitCode != 124 {
		t.Fatalf("ExitCode = %d, want timeout code 124; result=%#v", result.ExitCode, result)
	}
	if result.Err == nil {
		t.Fatalf("Err is nil for timeout; result=%#v", result)
	}
}

func TestRunImplementationDispatchCommandParentCancelUsesContractCode(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result := runImplementationDispatchCommand(ctx, t.TempDir(), time.Second, os.Args[0], implementationDispatchCommandHelperArgs("sleep")...)

	if result.ExitCode != 124 {
		t.Fatalf("ExitCode = %d, want cancellation code 124; result=%#v", result.ExitCode, result)
	}
	if !errors.Is(result.Err, context.Canceled) {
		t.Fatalf("Err = %v, want context.Canceled; result=%#v", result.Err, result)
	}
}

func implementationDispatchCommandHelperArgs(args ...string) []string {
	out := []string{"-test.run=TestImplementationDispatchCommandHelperProcess", "--"}
	return append(out, args...)
}

func TestImplementationDispatchCommandHelperProcess(t *testing.T) {
	args := os.Args
	sep := -1
	for i, arg := range args {
		if arg == "--" {
			sep = i
			break
		}
	}
	if sep < 0 || sep+1 >= len(args) {
		return
	}

	switch args[sep+1] {
	case "exit":
		fmt.Fprintln(os.Stdout, "stdout text")
		fmt.Fprintln(os.Stderr, "stderr text")
		os.Exit(implementationDispatchCommandAtoiDefault(args[sep+2], 1))
	case "pwd":
		wd, err := os.Getwd()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		fmt.Fprint(os.Stdout, wd)
		os.Exit(0)
	case "sleep":
		time.Sleep(200 * time.Millisecond)
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "unknown helper command %q", args[sep+1])
		os.Exit(2)
	}
}

func implementationDispatchCommandAtoiDefault(value string, fallback int) int {
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}

	return parsed
}
