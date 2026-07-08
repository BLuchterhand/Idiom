package commandexec

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
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

	if err := result.FailureError(""); err == nil || !strings.Contains(err.Error(), "command failed:") {
		t.Fatalf("FailureError = %v, want command failed error", err)
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

	if err := result.FailureError("10ms"); err == nil || err.Error() != "command timed out after 10ms" {
		t.Fatalf("FailureError = %v", err)
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

func TestRunForegroundStreamsProvidedIOEnvAndDir(t *testing.T) {
	dir := t.TempDir()
	spec := helperSpecWithDir(dir, "foreground-stream")
	spec.Env = append(spec.Env, "IDIOM_FOREGROUND_VALUE=from-env")

	var stdout strings.Builder
	var stderr strings.Builder
	result := RunForeground(t.Context(), spec, ForegroundIO{
		Stdin:  strings.NewReader("payload"),
		Stdout: &stdout,
		Stderr: &stderr,
	})

	if result.Err != nil {
		t.Fatalf("RunForeground failed: %v", result.Err)
	}

	wantPWD := "pwd=" + config.CanonicalizeExistingPath(dir)
	if !strings.Contains(stdout.String(), wantPWD) {
		t.Fatalf("stdout missing %q: %q", wantPWD, stdout.String())
	}
	if !strings.Contains(stdout.String(), "env=from-env") {
		t.Fatalf("stdout missing env overlay: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "stdin=payload") {
		t.Fatalf("stdout missing stdin: %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "stderr=payload") {
		t.Fatalf("stderr missing streamed payload: %q", stderr.String())
	}
}

func TestPrepareForegroundCommandStreamsProvidedIOEnvAndDir(t *testing.T) {
	dir := t.TempDir()
	spec := helperSpecWithDir(dir, "foreground-stream")
	spec.Env = append(spec.Env, "IDIOM_FOREGROUND_VALUE=from-env")
	spec.Timeout = 0

	var stdout strings.Builder
	var stderr strings.Builder
	cmd, err := PrepareForegroundCommand(t.Context(), spec, ForegroundIO{
		Stdin:  strings.NewReader("payload"),
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if err != nil {
		t.Fatalf("PrepareForegroundCommand failed: %v", err)
	}
	if err := cmd.Run(); err != nil {
		t.Fatalf("prepared command failed: %v", err)
	}

	wantPWD := "pwd=" + config.CanonicalizeExistingPath(dir)
	if !strings.Contains(stdout.String(), wantPWD) {
		t.Fatalf("stdout missing %q: %q", wantPWD, stdout.String())
	}
	if !strings.Contains(stdout.String(), "env=from-env") {
		t.Fatalf("stdout missing env overlay: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "stdin=payload") {
		t.Fatalf("stdout missing stdin: %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "stderr=payload") {
		t.Fatalf("stderr missing streamed payload: %q", stderr.String())
	}
}

func TestRunForegroundPropagatesExitCode(t *testing.T) {
	result := RunForeground(t.Context(), helperSpec("exit", "7"), ForegroundIO{
		Stdout: io.Discard,
		Stderr: io.Discard,
	})

	if result.ExitCode != 7 || !result.ExitError {
		t.Fatalf("RunForeground result = %#v, want exit code 7 with ExitError", result)
	}
}

func TestRunForegroundReportsMissingCommand(t *testing.T) {
	result := RunForeground(t.Context(), Spec{Name: "definitely-not-an-idiom-test-command"}, ForegroundIO{})

	if result.Err == nil || !result.CommandNotFound || result.ExitCode != 1 {
		t.Fatalf("RunForeground missing command result = %#v", result)
	}
}

func TestRunForegroundHonorsTimeout(t *testing.T) {
	spec := helperSpec("sleep")
	spec.Timeout = 10 * time.Millisecond

	result := RunForeground(t.Context(), spec, ForegroundIO{
		Stdout: io.Discard,
		Stderr: io.Discard,
	})

	if !result.TimedOut {
		t.Fatalf("TimedOut = false; result=%#v", result)
	}
	if result.Err == nil {
		t.Fatalf("Err is nil for timed out foreground command")
	}
}

func TestRunForegroundRejectsCaptureOnlyOptions(t *testing.T) {
	cases := []struct {
		name string
		spec Spec
		want string
	}{
		{name: "static stdin", spec: Spec{Name: os.Args[0], Stdin: "input"}, want: "static stdin"},
		{name: "output log path", spec: Spec{Name: os.Args[0], OutputLogPath: filepath.Join(t.TempDir(), "run.log")}, want: "output log path"},
		{name: "merge output", spec: Spec{Name: os.Args[0], MergeOutput: true}, want: "merge output"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := RunForeground(t.Context(), tc.spec, ForegroundIO{})
			if result.Err == nil || !strings.Contains(result.Err.Error(), tc.want) {
				t.Fatalf("RunForeground result = %#v, want error containing %q", result, tc.want)
			}
		})
	}
}

func TestPrepareForegroundCommandRejectsManagedOnlyOptions(t *testing.T) {
	cases := []struct {
		name string
		spec Spec
		want string
	}{
		{name: "static stdin", spec: Spec{Name: os.Args[0], Stdin: "input"}, want: "static stdin"},
		{name: "output log path", spec: Spec{Name: os.Args[0], OutputLogPath: filepath.Join(t.TempDir(), "run.log")}, want: "output log path"},
		{name: "merge output", spec: Spec{Name: os.Args[0], MergeOutput: true}, want: "merge output"},
		{name: "timeout", spec: Spec{Name: os.Args[0], Timeout: time.Second}, want: "timeout"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := PrepareForegroundCommand(t.Context(), tc.spec, ForegroundIO{})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("PrepareForegroundCommand error = %v, want containing %q", err, tc.want)
			}
		})
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

func TestStartLoggedProcessOwnsOutputAndSignalLifecycle(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell signal trap is Unix-specific")
	}

	root := t.TempDir()
	logPath := filepath.Join(root, "logs", "process.log")
	readyPath := filepath.Join(root, "ready")
	binary := filepath.Join(root, "logged-process")
	script := `#!/bin/sh
trap 'echo interrupted >&2; exit 0' TERM
echo ready
echo ready > "$READY_FILE"
while true; do sleep 0.05; done
`
	if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
		t.Fatalf("write helper script: %v", err)
	}

	spec := Spec{
		Name:          binary,
		Env:           []string{"READY_FILE=" + readyPath},
		Timeout:       5 * time.Second,
		OutputLogPath: logPath,
	}

	process, err := Start(t.Context(), spec)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer process.Close()

	if process.PID() <= 0 {
		t.Fatalf("PID = %d, want positive process id", process.PID())
	}
	if !process.Alive() {
		t.Fatal("process should be alive after start")
	}
	if process.Stdin() != nil || process.Stdout() != nil {
		t.Fatalf("logged process should not expose streaming pipes: stdin=%#v stdout=%#v", process.Stdin(), process.Stdout())
	}

	waitForFileContent(t, readyPath, "ready")

	stopCtx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if err := process.Stop(stopCtx, syscall.SIGTERM); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}

	if process.Alive() {
		t.Fatal("process should not be alive after Done closes")
	}

	body, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if got := string(body); !strings.Contains(got, "ready") || !strings.Contains(got, "interrupted") {
		t.Fatalf("log missing lifecycle output: %q", got)
	}
}

func TestStartDetachedOwnsProcessGroupAndLog(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("detached process groups are POSIX-specific")
	}

	logPath := filepath.Join(t.TempDir(), "detached.log")
	spec := helperSpec("log-and-sleep")
	spec.OutputLogPath = logPath
	spec.Timeout = 0

	pid, err := StartDetached(t.Context(), spec)
	if err != nil {
		t.Fatalf("StartDetached failed: %v", err)
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })

	pgid, err := syscall.Getpgid(pid)
	if err != nil {
		t.Fatalf("Getpgid(%d): %v", pid, err)
	}
	if pgid != pid {
		t.Fatalf("detached process should own its process group: pgid=%d pid=%d", pgid, pid)
	}

	waitForFileContent(t, logPath, "detached-ready")
}

func TestStartDetachedUsesDirEnvAndCanonicalPWD(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("detached process groups are POSIX-specific")
	}

	dir := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "detached-context.log")
	spec := helperSpecWithDir(dir, "detached-context")
	spec.OutputLogPath = logPath
	spec.Timeout = 0
	spec.Env = append(spec.Env, "IDIOM_DETACHED_VALUE=from-env")

	pid, err := StartDetached(t.Context(), spec)
	if err != nil {
		t.Fatalf("StartDetached failed: %v", err)
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })

	waitForFileContent(t, logPath, "env=from-env")
	body, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	got := string(body)
	wantPWD := "pwd=" + config.CanonicalizeExistingPath(dir)
	if !strings.Contains(got, wantPWD) {
		t.Fatalf("log missing %q: %q", wantPWD, got)
	}
}

func TestStopRejectsStreamingProcess(t *testing.T) {
	process, err := Start(t.Context(), helperSpec("sleep"))
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer process.Close()

	err = process.Stop(t.Context(), syscall.SIGTERM)
	if err == nil || !strings.Contains(err.Error(), "non-streaming") {
		t.Fatalf("Stop should reject streaming processes, got %v", err)
	}
}

func TestStopFallsBackToCloseWhenGracefulSignalFails(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("invalid Unix signal fixture is Unix-specific")
	}

	root := t.TempDir()
	logPath := filepath.Join(root, "logs", "process.log")
	readyPath := filepath.Join(root, "ready")
	binary := filepath.Join(root, "logged-process")
	script := `#!/bin/sh
echo ready > "$READY_FILE"
while true; do sleep 0.05; done
`
	if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
		t.Fatalf("write helper script: %v", err)
	}

	process, err := Start(t.Context(), Spec{
		Name:          binary,
		Env:           []string{"READY_FILE=" + readyPath},
		Timeout:       5 * time.Second,
		OutputLogPath: logPath,
	})
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer process.Close()

	waitForFileContent(t, readyPath, "ready")

	stopCtx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if err := process.Stop(stopCtx, syscall.Signal(-1)); err != nil {
		t.Fatalf("Stop should fall back to forced cleanup after signal failure: %v", err)
	}
	if process.Alive() {
		t.Fatal("process should not be alive after fallback cleanup")
	}
}

func TestRunRejectsOutputLogPath(t *testing.T) {
	result := Run(t.Context(), Spec{Name: os.Args[0], OutputLogPath: filepath.Join(t.TempDir(), "run.log")})

	if result.Err == nil || !strings.Contains(result.Err.Error(), "output log path") {
		t.Fatalf("Run should reject output log path, result=%#v", result)
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

func TestStartDetachedRejectsManagedOnlyOptions(t *testing.T) {
	cases := []struct {
		name string
		spec Spec
		want string
	}{
		{name: "missing log", spec: helperSpec("sleep"), want: "output log path"},
		{name: "static stdin", spec: Spec{Name: os.Args[0], OutputLogPath: filepath.Join(t.TempDir(), "run.log"), Stdin: "input"}, want: "static stdin"},
		{name: "merge output", spec: Spec{Name: os.Args[0], OutputLogPath: filepath.Join(t.TempDir(), "run.log"), MergeOutput: true}, want: "merge output"},
		{name: "timeout", spec: Spec{Name: os.Args[0], OutputLogPath: filepath.Join(t.TempDir(), "run.log"), Timeout: time.Second}, want: "timeout"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := StartDetached(t.Context(), tc.spec)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("StartDetached error = %v, want containing %q", err, tc.want)
			}
		})
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

	case "foreground-stream":
		data, _ := io.ReadAll(os.Stdin)
		fmt.Fprintf(os.Stdout, "pwd=%s\nenv=%s\nstdin=%s", filepath.Clean(os.Getenv("PWD")), os.Getenv("IDIOM_FOREGROUND_VALUE"), string(data))
		fmt.Fprintf(os.Stderr, "stderr=%s", string(data))
		os.Exit(0)

	case "stream-context":
		data, _ := io.ReadAll(os.Stdin)
		fmt.Fprintf(os.Stdout, "pwd=%s\nenv=%s\nstdin=%s", filepath.Clean(os.Getenv("PWD")), os.Getenv("IDIOM_STREAM_VALUE"), string(data))
		os.Exit(0)

	case "stderr-large":
		fmt.Fprint(os.Stderr, strings.Repeat("e", 64))
		os.Exit(0)

	case "log-and-sleep":
		fmt.Fprintln(os.Stdout, "detached-ready")
		fmt.Fprintln(os.Stderr, "detached-stderr")
		time.Sleep(200 * time.Millisecond)
		os.Exit(0)

	case "detached-context":
		fmt.Fprintf(os.Stdout, "pwd=%s\nenv=%s\n", filepath.Clean(os.Getenv("PWD")), os.Getenv("IDIOM_DETACHED_VALUE"))
		time.Sleep(200 * time.Millisecond)
		os.Exit(0)

	}

	os.Exit(2)
}

func waitForFileContent(t *testing.T, path string, want string) {
	t.Helper()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		body, err := os.ReadFile(path)
		if err == nil && strings.Contains(string(body), want) {
			return
		}

		time.Sleep(10 * time.Millisecond)
	}

	body, _ := os.ReadFile(path)
	t.Fatalf("timed out waiting for %q in %s; content=%q", want, path, string(body))
}

func atoiDefault(value string, fallback int) int {
	var out int
	if _, err := fmt.Sscanf(value, "%d", &out); err != nil {
		return fallback
	}

	return out
}
