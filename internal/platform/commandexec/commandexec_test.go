package commandexec

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"idiom/internal/platform/config"
)

func TestRunCapturesStdoutStderrAndExitCode(t *testing.T) {
	result := Run(t.Context(), helperSpec("exit", "7"))

	if result.ExitCode != 7 {
		t.Fatalf("ExitCode = %d, want 7; result=%#v", result.ExitCode, result)
	}

	if strings.TrimSpace(result.Stdout) != "stdout text" {
		t.Fatalf("Stdout = %q", result.Stdout)
	}

	if strings.TrimSpace(result.Stderr) != "stderr text" {
		t.Fatalf("Stderr = %q", result.Stderr)
	}

	if result.Err == nil {
		t.Fatalf("Err is nil for non-zero exit")
	}
}

func TestRunBoundsOutputBeforeReturning(t *testing.T) {
	spec := helperSpec("large-output")
	spec.MaxOutputBytes = 16
	result := Run(t.Context(), spec)

	if !result.StdoutTruncated {
		t.Fatalf("StdoutTruncated = false; stdout=%q", result.Stdout)
	}

	if !strings.HasPrefix(result.Stdout, strings.Repeat("x", 16)) {
		t.Fatalf("Stdout should keep the first bounded bytes, got %q", result.Stdout)
	}

	if !strings.Contains(result.Stdout, "...[truncated]") {
		t.Fatalf("Stdout should include truncation marker, got %q", result.Stdout)
	}
}

func TestRunReportsTimeout(t *testing.T) {
	spec := helperSpec("sleep")
	spec.Timeout = 10 * time.Millisecond

	result := Run(t.Context(), spec)

	if !result.TimedOut {
		t.Fatalf("TimedOut = false; result=%#v", result)
	}

	if result.Err == nil {
		t.Fatalf("Err is nil for timed out command")
	}
}

func TestRunUsesCanonicalPWDOverlay(t *testing.T) {
	dir := t.TempDir()
	result := Run(t.Context(), helperSpecWithDir(dir, "print-pwd"))

	if result.Err != nil {
		t.Fatalf("Run failed: %v", result.Err)
	}

	want := config.CanonicalizeExistingPath(dir)
	if got := strings.TrimSpace(result.Stdout); got != want {
		t.Fatalf("PWD = %q, want %q", got, want)
	}
}

func TestEnvWithCanonicalPWDOverridesDuplicates(t *testing.T) {
	dir := t.TempDir()
	env := EnvWithCanonicalPWD([]string{"PATH=/bin", "PWD=/tmp/Logical"}, dir, []string{"PWD=/tmp/Other"})
	count := 0

	for _, entry := range env {
		if strings.HasPrefix(entry, "PWD=") {
			count++

			if got := strings.TrimPrefix(entry, "PWD="); got != config.CanonicalizeExistingPath(dir) {
				t.Fatalf("PWD = %q, want %q", got, config.CanonicalizeExistingPath(dir))
			}
		}
	}

	if count != 1 {
		t.Fatalf("PWD count = %d in %#v, want 1", count, env)
	}
}

func TestRunPlainShellMode(t *testing.T) {
	result := Run(t.Context(), Spec{
		Mode:           ModePlainShell,
		Shell:          "/bin/sh",
		Command:        "printf shell-mode",
		MaxOutputBytes: 64,
	})

	if result.Err != nil {
		t.Fatalf("Run failed: %v", result.Err)
	}

	if result.Stdout != "shell-mode" {
		t.Fatalf("Stdout = %q, want shell-mode", result.Stdout)
	}
}

func TestRunPassesStdin(t *testing.T) {
	spec := helperSpec("echo-stdin")
	spec.Stdin = `{"message":"hello"}`

	result := Run(t.Context(), spec)

	if result.Err != nil {
		t.Fatalf("Run failed: %v", result.Err)
	}

	if strings.TrimSpace(result.Stdout) != spec.Stdin {
		t.Fatalf("Stdout = %q, want stdin %q", result.Stdout, spec.Stdin)
	}
}

func TestRunMergeOutputUsesSingleOutputLimit(t *testing.T) {
	spec := helperSpec("mixed-output")
	spec.MaxOutputBytes = 12
	spec.MergeOutput = true

	result := Run(t.Context(), spec)

	if !result.StdoutTruncated {
		t.Fatalf("StdoutTruncated = false; result=%#v", result)
	}

	if result.Stderr != "" || result.StderrTruncated {
		t.Fatalf("merged output should be returned as stdout only, result=%#v", result)
	}

	if len(strings.TrimSuffix(result.Stdout, "\n...[truncated]")) > 12 {
		t.Fatalf("merged output exceeded single limit: %q", result.Stdout)
	}
}

func helperSpec(args ...string) Spec {
	return helperSpecWithDir("", args...)
}

func helperSpecWithDir(dir string, args ...string) Spec {
	return Spec{
		Name:           os.Args[0],
		Args:           append([]string{"-test.run=TestCommandExecHelperProcess", "--"}, args...),
		Dir:            dir,
		Env:            []string{"GO_WANT_COMMAND_EXEC_HELPER=1"},
		Timeout:        time.Second,
		MaxOutputBytes: 4096,
		CanonicalPWD:   true,
	}
}

func TestCommandExecHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_COMMAND_EXEC_HELPER") != "1" {
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

	args := os.Args[sep+1:]
	switch args[0] {
	case "exit":
		fmt.Fprintln(os.Stdout, "stdout text")
		fmt.Fprintln(os.Stderr, "stderr text")
		os.Exit(atoiDefault(args[1], 1))

	case "large-output":
		fmt.Fprint(os.Stdout, strings.Repeat("x", 64))
		os.Exit(0)

	case "sleep":
		time.Sleep(200 * time.Millisecond)
		os.Exit(0)

	case "print-pwd":
		fmt.Fprint(os.Stdout, filepath.Clean(os.Getenv("PWD")))
		os.Exit(0)

	case "echo-stdin":
		data, _ := io.ReadAll(os.Stdin)
		fmt.Fprint(os.Stdout, string(data))
		os.Exit(0)

	case "mixed-output":
		fmt.Fprint(os.Stdout, strings.Repeat("o", 16))
		fmt.Fprint(os.Stderr, strings.Repeat("e", 16))
		os.Exit(0)
	}

	os.Exit(2)
}

func atoiDefault(value string, fallback int) int {
	var out int
	if _, err := fmt.Sscanf(value, "%d", &out); err != nil {
		return fallback
	}

	return out
}
