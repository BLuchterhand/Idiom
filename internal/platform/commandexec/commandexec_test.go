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
	if !result.ExitError {
		t.Fatalf("ExitError = false for non-zero process exit; result=%#v", result)
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

func TestRunMissingCommandReportsNonExitError(t *testing.T) {
	result := Run(t.Context(), Spec{Name: "definitely-not-an-idiom-test-command"})

	if result.Err == nil {
		t.Fatalf("Err is nil for missing command")
	}
	if result.ExitError {
		t.Fatalf("ExitError = true for spawn failure; result=%#v", result)
	}
	if !result.CommandNotFound {
		t.Fatalf("CommandNotFound = false for missing command; result=%#v", result)
	}
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1; result=%#v", result.ExitCode, result)
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

func TestStartProvidesStreamingPipesEnvAndDir(t *testing.T) {
	dir := t.TempDir()
	spec := helperSpecWithDir(dir, "stream-context")
	spec.Env = append(spec.Env, "IDIOM_STREAM_VALUE=from-env")

	process, err := Start(t.Context(), spec)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer process.Close()

	if _, err := io.WriteString(process.Stdin(), "payload"); err != nil {
		t.Fatalf("write stdin: %v", err)
	}
	if err := process.Stdin().Close(); err != nil {
		t.Fatalf("close stdin: %v", err)
	}

	data, err := io.ReadAll(process.Stdout())
	if err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	if err := process.wait(); err != nil {
		t.Fatalf("Wait failed: %v", err)
	}

	got := string(data)
	wantPWD := "pwd=" + config.CanonicalizeExistingPath(dir)
	if !strings.Contains(got, wantPWD) {
		t.Fatalf("stdout missing %q: %q", wantPWD, got)
	}
	if !strings.Contains(got, "env=from-env") {
		t.Fatalf("stdout missing env overlay: %q", got)
	}
	if !strings.Contains(got, "stdin=payload") {
		t.Fatalf("stdout missing streamed stdin: %q", got)
	}
}

func TestStartCapturesBoundedStderr(t *testing.T) {
	spec := helperSpec("stderr-large")
	spec.MaxOutputBytes = 8

	process, err := Start(t.Context(), spec)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer process.Close()

	if err := process.wait(); err != nil {
		t.Fatalf("Wait failed: %v", err)
	}

	stderr, truncated := process.Stderr()
	if !truncated {
		t.Fatalf("Stderr truncated = false; stderr=%q", stderr)
	}
	if !strings.HasPrefix(stderr, strings.Repeat("e", 8)) {
		t.Fatalf("stderr should keep the first bounded bytes, got %q", stderr)
	}
	if !strings.Contains(stderr, "...[truncated]") {
		t.Fatalf("stderr should include truncation marker, got %q", stderr)
	}
}

func TestStartCloseTerminatesRunningProcess(t *testing.T) {
	process, err := Start(t.Context(), helperSpec("sleep"))
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- process.wait()
	}()

	time.Sleep(10 * time.Millisecond)
	if err := process.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("process did not terminate after Close")
	}
}

func TestStartRejectsStaticStdin(t *testing.T) {
	spec := helperSpec("echo-stdin")
	spec.Stdin = "not a streaming pipe"

	_, err := Start(t.Context(), spec)
	if err == nil {
		t.Fatal("expected Start to reject static stdin")
	}
	if !strings.Contains(err.Error(), "static stdin") {
		t.Fatalf("unexpected error: %v", err)
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
		Timeout:        5 * time.Second,
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

	case "stream-context":
		data, _ := io.ReadAll(os.Stdin)
		fmt.Fprintf(os.Stdout, "pwd=%s\nenv=%s\nstdin=%s", filepath.Clean(os.Getenv("PWD")), os.Getenv("IDIOM_STREAM_VALUE"), string(data))
		os.Exit(0)

	case "stderr-large":
		fmt.Fprint(os.Stderr, strings.Repeat("e", 64))
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
