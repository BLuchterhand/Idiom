package commandexec

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	"idiom/internal/platform/config"
)

var _ = ginkgo.Describe("Command execution", ginkgo.Label("integration"), func() {
	ginkgo.It("captures stdout, stderr, and a nonzero exit code", func(ctx ginkgo.SpecContext) {
		result := Run(ctx, helperSpec("exit", "7"))
		gomega.Expect(result.ExitCode).To(gomega.Equal(7))
		gomega.Expect(result.ExitError).To(gomega.BeTrue())
		gomega.Expect(strings.TrimSpace(result.Stdout)).To(gomega.Equal("stdout text"))
		gomega.Expect(strings.TrimSpace(result.Stderr)).To(gomega.Equal("stderr text"))
		gomega.Expect(result.Err).To(gomega.HaveOccurred())
		gomega.Expect(result.FailureError("")).To(gomega.MatchError(gomega.ContainSubstring("command failed:")))
	})

	ginkgo.It("combines trimmed stdout and stderr", func() {
		result := Result{Stdout: " stdout text \n", Stderr: "\nstderr text "}
		gomega.Expect(result.CombinedOutput()).To(gomega.Equal("stdout text\nstderr text"))
	})

	ginkgo.It("classifies a missing command as a non-exit spawn error", func(ctx ginkgo.SpecContext) {
		result := Run(ctx, Spec{Name: "definitely-not-an-idiom-test-command"})
		gomega.Expect(result.Err).To(gomega.HaveOccurred())
		gomega.Expect(result.ExitError).To(gomega.BeFalse())
		gomega.Expect(result.CommandNotFound).To(gomega.BeTrue())
		gomega.Expect(result.ExitCode).To(gomega.Equal(1))
	})

	ginkgo.It("bounds captured output before returning", func(ctx ginkgo.SpecContext) {
		spec := helperSpec("large-output")
		spec.MaxOutputBytes = 16
		result := Run(ctx, spec)
		gomega.Expect(result.StdoutTruncated).To(gomega.BeTrue())
		gomega.Expect(result.Stdout).To(gomega.HavePrefix(strings.Repeat("x", 16)))
		gomega.Expect(result.Stdout).To(gomega.ContainSubstring("...[truncated]"))
	})

	ginkgo.It("reports command timeouts", func(ctx ginkgo.SpecContext) {
		spec := helperSpec("sleep")
		spec.Timeout = 10 * time.Millisecond
		result := Run(ctx, spec)
		gomega.Expect(result.TimedOut).To(gomega.BeTrue())
		gomega.Expect(result.Err).To(gomega.HaveOccurred())
		gomega.Expect(result.FailureError("10ms")).To(gomega.MatchError("command timed out after 10ms"))
	})

	ginkgo.It("uses the canonical working directory in PWD", func(ctx ginkgo.SpecContext) {
		dir := ginkgo.GinkgoT().TempDir()
		result := Run(ctx, helperSpecWithDir(dir, "print-pwd"))
		gomega.Expect(result.Err).NotTo(gomega.HaveOccurred())
		gomega.Expect(strings.TrimSpace(result.Stdout)).To(gomega.Equal(config.CanonicalizeExistingPath(dir)))
	})

	ginkgo.It("overrides duplicate PWD entries with one canonical value", func() {
		dir := ginkgo.GinkgoT().TempDir()
		env := EnvWithCanonicalPWD([]string{"PATH=/bin", "PWD=/tmp/Logical"}, dir, []string{"PWD=/tmp/Other"})
		var pwdEntries []string
		for _, entry := range env {
			if strings.HasPrefix(entry, "PWD=") {
				pwdEntries = append(pwdEntries, strings.TrimPrefix(entry, "PWD="))
			}
		}
		gomega.Expect(pwdEntries).To(gomega.Equal([]string{config.CanonicalizeExistingPath(dir)}))
	})

	ginkgo.It("makes explicit environment values replace inherited duplicates in the child", func(ctx ginkgo.SpecContext) {
		wantHome := filepath.Join(ginkgo.GinkgoT().TempDir(), "isolated-home")
		spec := helperSpec("print-env", "HOME")
		spec.BaseEnv = []string{"HOME=/inherited-home", "HOME=/stale-home"}
		spec.Env = append(spec.Env, "HOME=/first-override", "HOME="+wantHome)

		result := Run(ctx, spec)
		gomega.Expect(result.Err).NotTo(gomega.HaveOccurred())
		gomega.Expect(result.Stdout).To(gomega.Equal(wantHome))

		var homeEntries []string
		for _, entry := range commandEnv(spec) {
			if strings.HasPrefix(entry, "HOME=") {
				homeEntries = append(homeEntries, entry)
			}
		}
		gomega.Expect(homeEntries).To(gomega.Equal([]string{"HOME=" + wantHome}))
	})

	ginkgo.It("runs a plain shell command", func(ctx ginkgo.SpecContext) {
		result := Run(ctx, Spec{
			Mode:           ModePlainShell,
			Shell:          "/bin/sh",
			Command:        "printf shell-mode",
			MaxOutputBytes: 64,
		})
		gomega.Expect(result.Err).NotTo(gomega.HaveOccurred())
		gomega.Expect(result.Stdout).To(gomega.Equal("shell-mode"))
	})

	ginkgo.It("passes static stdin to a captured command", func(ctx ginkgo.SpecContext) {
		spec := helperSpec("echo-stdin")
		spec.Stdin = `{"message":"hello"}`
		result := Run(ctx, spec)
		gomega.Expect(result.Err).NotTo(gomega.HaveOccurred())
		gomega.Expect(strings.TrimSpace(result.Stdout)).To(gomega.Equal(spec.Stdin))
	})

	ginkgo.It("applies one output limit when stdout and stderr are merged", func(ctx ginkgo.SpecContext) {
		spec := helperSpec("mixed-output")
		spec.MaxOutputBytes = 12
		spec.MergeOutput = true
		result := Run(ctx, spec)
		gomega.Expect(result.StdoutTruncated).To(gomega.BeTrue())
		gomega.Expect(result.Stderr).To(gomega.BeEmpty())
		gomega.Expect(result.StderrTruncated).To(gomega.BeFalse())
		gomega.Expect(len(strings.TrimSuffix(result.Stdout, "\n...[truncated]"))).To(gomega.BeNumerically("<=", 12))
	})

	ginkgo.It("streams foreground IO with the configured environment and directory", func(ctx ginkgo.SpecContext) {
		dir := ginkgo.GinkgoT().TempDir()
		spec := helperSpecWithDir(dir, "foreground-stream")
		spec.Env = append(spec.Env, "IDIOM_FOREGROUND_VALUE=from-env")
		var stdout strings.Builder
		var stderr strings.Builder

		result := RunForeground(ctx, spec, ForegroundIO{
			Stdin:  strings.NewReader("payload"),
			Stdout: &stdout,
			Stderr: &stderr,
		})
		gomega.Expect(result.Err).NotTo(gomega.HaveOccurred())
		gomega.Expect(stdout.String()).To(gomega.And(
			gomega.ContainSubstring("pwd="+config.CanonicalizeExistingPath(dir)),
			gomega.ContainSubstring("env=from-env"),
			gomega.ContainSubstring("stdin=payload"),
		))
		gomega.Expect(stderr.String()).To(gomega.ContainSubstring("stderr=payload"))
	})

	ginkgo.It("prepares a foreground command with streamed IO, environment, and directory", func(ctx ginkgo.SpecContext) {
		dir := ginkgo.GinkgoT().TempDir()
		spec := helperSpecWithDir(dir, "foreground-stream")
		spec.Env = append(spec.Env, "IDIOM_FOREGROUND_VALUE=from-env")
		spec.Timeout = 0
		var stdout strings.Builder
		var stderr strings.Builder

		cmd, err := PrepareForegroundCommand(ctx, spec, ForegroundIO{
			Stdin:  strings.NewReader("payload"),
			Stdout: &stdout,
			Stderr: &stderr,
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(cmd.Run()).To(gomega.Succeed())
		gomega.Expect(stdout.String()).To(gomega.And(
			gomega.ContainSubstring("pwd="+config.CanonicalizeExistingPath(dir)),
			gomega.ContainSubstring("env=from-env"),
			gomega.ContainSubstring("stdin=payload"),
		))
		gomega.Expect(stderr.String()).To(gomega.ContainSubstring("stderr=payload"))
	})

	ginkgo.It("propagates a foreground process exit code", func(ctx ginkgo.SpecContext) {
		result := RunForeground(ctx, helperSpec("exit", "7"), ForegroundIO{Stdout: io.Discard, Stderr: io.Discard})
		gomega.Expect(result.ExitCode).To(gomega.Equal(7))
		gomega.Expect(result.ExitError).To(gomega.BeTrue())
	})

	ginkgo.It("keeps direct exit classification separate from lifecycle failures", func(ctx ginkgo.SpecContext) {
		cmd, err := configuredCommand(ctx, helperSpec("exit", "7"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		directErr := cmd.Run()
		gomega.Expect(directErr).To(gomega.HaveOccurred())

		lifecycleErr := errors.New("injected lifecycle failure")
		result := resultFromRun(ctx, 0, managedCommandOutcome{
			commandErr:   directErr,
			lifecycleErr: lifecycleErr,
		})

		gomega.Expect(result.ExitCode).To(gomega.Equal(7))
		gomega.Expect(result.ExitError).To(gomega.BeTrue())
		gomega.Expect(errors.Is(result.Err, lifecycleErr)).To(gomega.BeTrue())
	})

	ginkgo.It("reports a missing foreground command", func(ctx ginkgo.SpecContext) {
		result := RunForeground(ctx, Spec{Name: "definitely-not-an-idiom-test-command"}, ForegroundIO{})
		gomega.Expect(result.Err).To(gomega.HaveOccurred())
		gomega.Expect(result.CommandNotFound).To(gomega.BeTrue())
		gomega.Expect(result.ExitCode).To(gomega.Equal(1))
	})

	ginkgo.It("honors foreground command timeouts", func(ctx ginkgo.SpecContext) {
		spec := helperSpec("sleep")
		spec.Timeout = 10 * time.Millisecond
		result := RunForeground(ctx, spec, ForegroundIO{Stdout: io.Discard, Stderr: io.Discard})
		gomega.Expect(result.TimedOut).To(gomega.BeTrue())
		gomega.Expect(result.Err).To(gomega.HaveOccurred())
	})

	ginkgo.DescribeTable("rejecting capture-only options for foreground execution",
		func(spec Spec, want string) {
			result := RunForeground(context.Background(), spec, ForegroundIO{})
			gomega.Expect(result.Err).To(gomega.MatchError(gomega.ContainSubstring(want)))
		},
		ginkgo.Entry("static stdin", Spec{Name: os.Args[0], Stdin: "input"}, "static stdin"),
		ginkgo.Entry("output log path", Spec{Name: os.Args[0], OutputLogPath: filepath.Join(os.TempDir(), "idiom-foreground-run.log")}, "output log path"),
		ginkgo.Entry("merge output", Spec{Name: os.Args[0], MergeOutput: true}, "merge output"),
	)

	ginkgo.DescribeTable("rejecting managed-only options while preparing foreground execution",
		func(spec Spec, want string) {
			_, err := PrepareForegroundCommand(context.Background(), spec, ForegroundIO{})
			gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring(want)))
		},
		ginkgo.Entry("static stdin", Spec{Name: os.Args[0], Stdin: "input"}, "static stdin"),
		ginkgo.Entry("output log path", Spec{Name: os.Args[0], OutputLogPath: filepath.Join(os.TempDir(), "idiom-prepared-run.log")}, "output log path"),
		ginkgo.Entry("merge output", Spec{Name: os.Args[0], MergeOutput: true}, "merge output"),
		ginkgo.Entry("timeout", Spec{Name: os.Args[0], Timeout: time.Second}, "timeout"),
	)

	ginkgo.It("provides streaming pipes with the configured environment and directory", func(ctx ginkgo.SpecContext) {
		dir := ginkgo.GinkgoT().TempDir()
		spec := helperSpecWithDir(dir, "stream-context")
		spec.Env = append(spec.Env, "IDIOM_STREAM_VALUE=from-env")
		process, err := Start(ctx, spec)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		ginkgo.DeferCleanup(process.Close)

		_, err = io.WriteString(process.Stdin(), "payload")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(process.Stdin().Close()).To(gomega.Succeed())
		data, err := io.ReadAll(process.Stdout())
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(process.wait()).To(gomega.Succeed())
		gomega.Expect(string(data)).To(gomega.And(
			gomega.ContainSubstring("pwd="+config.CanonicalizeExistingPath(dir)),
			gomega.ContainSubstring("env=from-env"),
			gomega.ContainSubstring("stdin=payload"),
		))
	})

	ginkgo.It("captures bounded stderr from a streaming process", func(ctx ginkgo.SpecContext) {
		spec := helperSpec("stderr-large")
		spec.MaxOutputBytes = 8
		process, err := Start(ctx, spec)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		ginkgo.DeferCleanup(process.Close)
		gomega.Expect(process.wait()).To(gomega.Succeed())

		stderr, truncated := process.Stderr()
		gomega.Expect(truncated).To(gomega.BeTrue())
		gomega.Expect(stderr).To(gomega.HavePrefix(strings.Repeat("e", 8)))
		gomega.Expect(stderr).To(gomega.ContainSubstring("...[truncated]"))
	})

	ginkgo.It("terminates a running streaming process when closed", func(ctx ginkgo.SpecContext) {
		process, err := Start(ctx, helperSpec("sleep"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		done := make(chan error, 1)
		go func() { done <- process.wait() }()
		gomega.Expect(process.Close()).To(gomega.Succeed())
		gomega.Eventually(done).Should(gomega.Receive())
	})

	ginkgo.It("surfaces lifecycle cleanup failures from Close without returning the forced exit", func(ctx ginkgo.SpecContext) {
		lifecycleErr := errors.New("injected close lifecycle failure")
		process := startProcessWithLifecycleFailure(ctx, lifecycleErr)

		closeErr := process.Close()
		gomega.Expect(errors.Is(closeErr, lifecycleErr)).To(gomega.BeTrue())
		var exitErr *exec.ExitError
		gomega.Expect(errors.As(closeErr, &exitErr)).To(gomega.BeFalse())
	})

	ginkgo.It("surfaces lifecycle cleanup failures from Stop", func(ctx ginkgo.SpecContext) {
		lifecycleErr := errors.New("injected stop lifecycle failure")
		process := startProcessWithLifecycleFailure(ctx, lifecycleErr)

		stopErr := process.Stop(ctx, os.Kill)
		gomega.Expect(errors.Is(stopErr, lifecycleErr)).To(gomega.BeTrue())
	})

	ginkgo.It("owns a logged process output and graceful-signal lifecycle", func(ctx ginkgo.SpecContext) {
		if runtime.GOOS == "windows" {
			ginkgo.Skip("shell signal trap is Unix-specific")
		}

		root := ginkgo.GinkgoT().TempDir()
		logPath := filepath.Join(root, "logs", "process.log")
		readyPath := filepath.Join(root, "ready")
		binary := filepath.Join(root, "logged-process")
		script := `#!/bin/sh
trap 'echo interrupted >&2; exit 0' TERM
echo ready
echo ready > "$READY_FILE"
while true; do sleep 0.05; done
`
		gomega.Expect(os.WriteFile(binary, []byte(script), 0o755)).To(gomega.Succeed())
		process, err := Start(ctx, Spec{
			Name:          binary,
			Env:           []string{"READY_FILE=" + readyPath},
			Timeout:       5 * time.Second,
			OutputLogPath: logPath,
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		ginkgo.DeferCleanup(process.Close)
		gomega.Expect(process.PID()).To(gomega.BeNumerically(">", 0))
		gomega.Expect(process.Alive()).To(gomega.BeTrue())
		gomega.Expect(process.Stdin()).To(gomega.BeNil())
		gomega.Expect(process.Stdout()).To(gomega.BeNil())
		waitForFileContent(readyPath, "ready")

		stopCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		ginkgo.DeferCleanup(cancel)
		gomega.Expect(process.Stop(stopCtx, syscall.SIGTERM)).To(gomega.Succeed())
		gomega.Expect(process.Alive()).To(gomega.BeFalse())
		body, err := os.ReadFile(logPath)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(string(body)).To(gomega.And(gomega.ContainSubstring("ready"), gomega.ContainSubstring("interrupted")))
	})

	ginkgo.It("rejects graceful Stop for a streaming process", func(ctx ginkgo.SpecContext) {
		process, err := Start(ctx, helperSpec("sleep"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		ginkgo.DeferCleanup(process.Close)
		gomega.Expect(process.Stop(ctx, syscall.SIGTERM)).To(gomega.MatchError(gomega.ContainSubstring("non-streaming")))
	})

	ginkgo.It("falls back to forced cleanup when a graceful signal fails", func(ctx ginkgo.SpecContext) {
		if runtime.GOOS == "windows" {
			ginkgo.Skip("invalid Unix signal fixture is Unix-specific")
		}
		root := ginkgo.GinkgoT().TempDir()
		logPath := filepath.Join(root, "logs", "process.log")
		readyPath := filepath.Join(root, "ready")
		binary := filepath.Join(root, "logged-process")
		script := `#!/bin/sh
echo ready > "$READY_FILE"
while true; do sleep 0.05; done
`
		gomega.Expect(os.WriteFile(binary, []byte(script), 0o755)).To(gomega.Succeed())
		process, err := Start(ctx, Spec{
			Name:          binary,
			Env:           []string{"READY_FILE=" + readyPath},
			Timeout:       5 * time.Second,
			OutputLogPath: logPath,
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		ginkgo.DeferCleanup(process.Close)
		waitForFileContent(readyPath, "ready")
		stopCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		ginkgo.DeferCleanup(cancel)
		gomega.Expect(process.Stop(stopCtx, syscall.Signal(-1))).To(gomega.Succeed())
		gomega.Expect(process.Alive()).To(gomega.BeFalse())
	})

	ginkgo.It("rejects a captured Run with an output log path", func(ctx ginkgo.SpecContext) {
		result := Run(ctx, Spec{Name: os.Args[0], OutputLogPath: filepath.Join(ginkgo.GinkgoT().TempDir(), "run.log")})
		gomega.Expect(result.Err).To(gomega.MatchError(gomega.ContainSubstring("output log path")))
	})

	ginkgo.It("rejects static stdin for a streaming process", func(ctx ginkgo.SpecContext) {
		spec := helperSpec("echo-stdin")
		spec.Stdin = "not a streaming pipe"
		_, err := Start(ctx, spec)
		gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("static stdin")))
	})

	ginkgo.DescribeTable("rejecting managed-only options for detached execution",
		func(spec Spec, want string) {
			_, err := StartDetached(context.Background(), spec)
			gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring(want)))
		},
		ginkgo.Entry("missing log", helperSpec("sleep"), "output log path"),
		ginkgo.Entry("static stdin", Spec{Name: os.Args[0], OutputLogPath: filepath.Join(os.TempDir(), "idiom-detached-stdin.log"), Stdin: "input"}, "static stdin"),
		ginkgo.Entry("merge output", Spec{Name: os.Args[0], OutputLogPath: filepath.Join(os.TempDir(), "idiom-detached-merged.log"), MergeOutput: true}, "merge output"),
		ginkgo.Entry("timeout", Spec{Name: os.Args[0], OutputLogPath: filepath.Join(os.TempDir(), "idiom-detached-timeout.log"), Timeout: time.Second}, "timeout"),
	)
})

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
	case "print-env":
		fmt.Fprint(os.Stdout, os.Getenv(args[1]))
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
	case "managed-tree-parent", "managed-tree-natural-parent", "managed-tree-parent-no-wait":
		if len(args) != 4 && len(args) != 5 {
			os.Exit(2)
		}
		if err := os.WriteFile(args[1], []byte(fmt.Sprintf("%d\n", os.Getpid())), 0o600); err != nil {
			os.Exit(3)
		}
		childArgs := []string{"-test.run=TestCommandExecHelperProcess", "--", "managed-tree-child"}
		if len(args) == 5 {
			childArgs = append(childArgs, args[4])
		}
		child := exec.Command(os.Args[0], childArgs...)
		child.Env = append(os.Environ(), "GO_WANT_COMMAND_EXEC_HELPER=1")
		child.Stdout = os.Stdout
		child.Stderr = os.Stderr
		if err := child.Start(); err != nil {
			os.Exit(4)
		}
		if err := os.WriteFile(args[2], []byte(fmt.Sprintf("%d\n", child.Process.Pid)), 0o600); err != nil {
			_ = child.Process.Kill()
			os.Exit(5)
		}
		if err := os.WriteFile(args[3], []byte("ready\n"), 0o600); err != nil {
			_ = child.Process.Kill()
			os.Exit(6)
		}
		if args[0] == "managed-tree-natural-parent" {
			fmt.Fprintln(os.Stdout, "natural-stdout")
			fmt.Fprintln(os.Stderr, "natural-stderr")
			os.Exit(0)
		}
		if args[0] == "managed-tree-parent-no-wait" {
			for {
				time.Sleep(time.Hour)
			}
		}
		_ = child.Wait()
		os.Exit(0)
	case "managed-tree-child":
		if len(args) == 2 {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				os.Exit(7)
			}
			if err := os.WriteFile(args[1], []byte(listener.Addr().String()+"\n"), 0o600); err != nil {
				_ = listener.Close()
				os.Exit(8)
			}
			for {
				connection, err := listener.Accept()
				if err != nil {
					os.Exit(9)
				}
				_ = connection.Close()
			}
		}
		for {
			time.Sleep(time.Hour)
		}
	case "foreground-job-wrapper":
		if len(args) != 2 {
			os.Exit(2)
		}
		spec := helperSpec("foreground-job-child", args[1])
		spec.Timeout = 0
		result := RunForeground(context.Background(), spec, ForegroundIO{
			Stdin:  os.Stdin,
			Stdout: os.Stdout,
			Stderr: os.Stderr,
		})
		if result.Err != nil {
			fmt.Fprintf(os.Stderr, "wrapper-error:%v\n", result.Err)
			os.Exit(1)
		}
		fmt.Fprintln(os.Stdout, "wrapper-exit")
		os.Exit(0)
	case "foreground-job-child":
		if len(args) != 2 {
			os.Exit(2)
		}
		if err := os.WriteFile(args[1], []byte(fmt.Sprintf("%d\n", os.Getpid())), 0o600); err != nil {
			os.Exit(3)
		}
		fmt.Fprintln(os.Stdout, "child-ready")
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			line := scanner.Text()
			fmt.Fprintf(os.Stdout, "child-input:%s\n", line)
			if line == "exit" {
				os.Exit(0)
			}
		}
		os.Exit(4)
	}

	os.Exit(2)
}

func startProcessWithLifecycleFailure(ctx context.Context, lifecycleErr error) *Process {
	ginkgo.GinkgoHelper()

	cmd, err := configuredCommand(ctx, helperSpec("sleep"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(cmd.Start()).To(gomega.Succeed())

	attachDone := make(chan struct{})
	close(attachDone)
	teardownDone := make(chan struct{})
	close(teardownDone)
	process := &Process{
		cmd: cmd,
		tree: &managedProcessTree{
			cmd:          cmd,
			attachDone:   attachDone,
			teardownDone: teardownDone,
			teardownErr:  lifecycleErr,
		},
		done: make(chan struct{}),
	}
	go process.wait()

	return process
}

func waitForFileContent(path string, want string) {
	ginkgo.GinkgoHelper()
	gomega.Eventually(func() string {
		body, _ := os.ReadFile(path)
		return string(body)
	}).WithTimeout(3 * time.Second).WithPolling(10 * time.Millisecond).Should(gomega.ContainSubstring(want))
}

func atoiDefault(value string, fallback int) int {
	var out int
	if _, err := fmt.Sscanf(value, "%d", &out); err != nil {
		return fallback
	}
	return out
}
