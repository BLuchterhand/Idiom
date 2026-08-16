package commandexec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/term"

	"idiom/internal/platform/config"
)

type ShellMode string

const (
	ModeArgv       ShellMode = ""
	ModePlainShell ShellMode = "plain_shell"
	ModeLoginShell ShellMode = "login_shell"
)

type Spec struct {
	Mode ShellMode

	Name string
	Args []string

	Command string
	Shell   string
	Stdin   string

	Dir            string
	Env            []string
	BaseEnv        []string
	Timeout        time.Duration
	MaxOutputBytes int
	CanonicalPWD   bool
	MergeOutput    bool
	OutputLogPath  string
	Sandbox        *SandboxProfile
}

type Result struct {
	Stdout          string
	Stderr          string
	StdoutTruncated bool
	StderrTruncated bool
	ExitCode        int
	ExitError       bool
	CommandNotFound bool
	TimedOut        bool
	Duration        time.Duration
	Err             error
}

type ForegroundIO struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

func (r Result) FailureError(timeoutLabel string) error {
	if r.TimedOut {
		if strings.TrimSpace(timeoutLabel) != "" {
			return fmt.Errorf("command timed out after %s", strings.TrimSpace(timeoutLabel))
		}

		return errors.New("command timed out")
	}

	if r.Err != nil {
		return fmt.Errorf("command failed: %w", r.Err)
	}

	return nil
}

func (r Result) CombinedOutput() string {
	output := strings.TrimSpace(r.Stdout)
	errText := strings.TrimSpace(r.Stderr)

	if errText != "" {
		if output != "" {
			output += "\n"
		}

		output += errText
	}

	return output
}

const defaultProcessStderrLimit = 64 * 1024

const DiagnosticCaptureBytes = 1024 * 1024

func Run(parent context.Context, spec Spec) Result {
	if parent == nil {
		parent = context.Background()
	}
	if spec.OutputLogPath != "" {
		return Result{ExitCode: 1, Err: errors.New("one-shot command cannot use output log path")}
	}

	ctx := parent
	var cancel context.CancelFunc
	if spec.Timeout > 0 {
		ctx, cancel = context.WithTimeout(parent, spec.Timeout)
		defer cancel()
	}

	cmd, processTree, err := managedCommand(ctx, spec)
	if err != nil {
		return Result{ExitCode: 1, Err: err}
	}
	if spec.Stdin != "" {
		cmd.Stdin = strings.NewReader(spec.Stdin)
	}

	stdout := newLimitedOutputBuffer(spec.MaxOutputBytes)
	stderr := stdout
	if !spec.MergeOutput {
		stderr = newLimitedOutputBuffer(spec.MaxOutputBytes)
	}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	start := time.Now()
	outcome := runManagedCommand(cmd, processTree)
	duration := time.Since(start)
	stdoutText, stdoutTruncated := stdout.String()
	stderrText, stderrTruncated := stderr.String()
	if spec.MergeOutput {
		stderrText = ""
		stderrTruncated = false
	}

	result := resultFromRun(ctx, duration, outcome)
	result.Stdout = stdoutText
	result.Stderr = stderrText
	result.StdoutTruncated = stdoutTruncated
	result.StderrTruncated = stderrTruncated

	return result
}

func RunForeground(parent context.Context, spec Spec, streams ForegroundIO) Result {
	if parent == nil {
		parent = context.Background()
	}

	ctx := parent
	var cancel context.CancelFunc
	if spec.Timeout > 0 {
		ctx, cancel = context.WithTimeout(parent, spec.Timeout)
		defer cancel()
	}

	cmd, processTree, err := managedForegroundCommand(ctx, spec, streams)
	if err != nil {
		return Result{ExitCode: 1, Err: err}
	}

	start := time.Now()
	outcome := runManagedCommand(cmd, processTree)

	return resultFromRun(ctx, time.Since(start), outcome)
}

// PrepareForegroundCommand builds an exec.Cmd for framework integrations that
// must own foreground execution themselves, such as terminal handoff. Prefer
// RunForeground when commandexec can own the full process lifecycle.
// PrepareForegroundCommand deliberately leaves process-tree and terminal job
// control to the framework that starts the returned command.
func PrepareForegroundCommand(parent context.Context, spec Spec, streams ForegroundIO) (*exec.Cmd, error) {
	if parent == nil {
		parent = context.Background()
	}
	if spec.Timeout > 0 {
		return nil, errors.New("prepared foreground command cannot use timeout")
	}

	return foregroundCommand(parent, spec, streams)
}

func foregroundCommand(ctx context.Context, spec Spec, streams ForegroundIO) (*exec.Cmd, error) {
	if spec.OutputLogPath != "" {
		return nil, errors.New("foreground command cannot use output log path")
	}
	if spec.Stdin != "" {
		return nil, errors.New("foreground command cannot use static stdin")
	}
	if spec.MergeOutput {
		return nil, errors.New("foreground command cannot merge output")
	}

	cmd, err := configuredCommand(ctx, spec)
	if err != nil {
		return nil, err
	}

	cmd.Stdin = streams.Stdin
	cmd.Stdout = streams.Stdout
	cmd.Stderr = streams.Stderr

	return cmd, nil
}

func managedForegroundCommand(ctx context.Context, spec Spec, streams ForegroundIO) (*exec.Cmd, *managedProcessTree, error) {
	cmd, err := foregroundCommand(ctx, spec, streams)
	if err != nil {
		return nil, nil, err
	}

	terminalFD := -1
	if stdin, ok := streams.Stdin.(*os.File); ok && term.IsTerminal(int(stdin.Fd())) {
		terminalFD = int(stdin.Fd())
	}

	processTree, err := newManagedProcessTree(cmd, terminalFD)
	if err != nil {
		return nil, nil, err
	}
	cmd.Cancel = processTree.terminate

	return cmd, processTree, nil
}

type managedCommandOutcome struct {
	commandErr   error
	lifecycleErr error
}

func resultFromRun(ctx context.Context, duration time.Duration, outcome managedCommandOutcome) Result {
	resultErr := errors.Join(outcome.commandErr, outcome.lifecycleErr)
	result := Result{
		ExitCode: 0,
		Duration: duration,
		Err:      resultErr,
	}

	if resultErr != nil {
		result.ExitCode = 1
	}
	if outcome.commandErr != nil {
		var exitErr *exec.ExitError
		if errors.As(outcome.commandErr, &exitErr) {
			result.ExitCode = exitErr.ExitCode()
			result.ExitError = true
		} else if errors.Is(outcome.commandErr, exec.ErrNotFound) {
			result.CommandNotFound = true
		}
	}

	if ctx.Err() == context.DeadlineExceeded {
		result.TimedOut = true
	}

	return result
}

// Process is a started child process whose lifecycle is owned by commandexec.
type Process struct {
	cmd    *exec.Cmd
	cancel context.CancelFunc
	tree   *managedProcessTree

	stdin     io.WriteCloser
	stdout    io.ReadCloser
	stderr    *limitedOutputBuffer
	resources []io.Closer

	waitOnce sync.Once
	waitErr  error

	directWaitErr error
	lifecycleErr  error
	done          chan struct{}
}

// Start launches a streaming child process. commandexec owns command shape,
// environment overlay, pipe cleanup, stderr bounding, and process teardown.
func Start(parent context.Context, spec Spec) (*Process, error) {
	if parent == nil {
		parent = context.Background()
	}
	if spec.Stdin != "" {
		return nil, errors.New("streaming command cannot use static stdin")
	}
	if spec.MergeOutput {
		return nil, errors.New("streaming command cannot merge output")
	}

	ctx := parent
	var cancel context.CancelFunc
	if spec.Timeout > 0 {
		ctx, cancel = context.WithTimeout(parent, spec.Timeout)
	}

	cmd, processTree, err := managedCommand(ctx, spec)
	if err != nil {
		if cancel != nil {
			cancel()
		}

		return nil, err
	}

	stdin, stdout, stderr, resources, err := configureProcessIO(cmd, spec)
	if err != nil {
		_ = processTree.abortAttach()
		_ = processTree.close()
		if cancel != nil {
			cancel()
		}

		return nil, err
	}

	if err := cmd.Start(); err != nil {
		_ = processTree.abortAttach()
		_ = processTree.close()
		closeProcessResources(stdin, stdout, resources)
		if cancel != nil {
			cancel()
		}

		return nil, fmt.Errorf("start %s: %w", cmd.Path, err)
	}
	if err := processTree.attach(); err != nil {
		terminateErr := processTree.terminate()
		teardownErr := processTree.waitBeforeReap()
		waitErr := cmd.Wait()
		closeErr := processTree.close()
		closeProcessResources(stdin, stdout, resources)
		if cancel != nil {
			cancel()
		}

		return nil, errors.Join(
			fmt.Errorf("attach managed process tree for %s: %w", cmd.Path, err),
			terminateErr,
			teardownErr,
			waitErr,
			closeErr,
		)
	}
	closeProcessResources(nil, nil, resources)
	resources = nil

	process := &Process{
		cmd:       cmd,
		cancel:    cancel,
		tree:      processTree,
		stdin:     stdin,
		stdout:    stdout,
		stderr:    stderr,
		resources: resources,
		done:      make(chan struct{}),
	}

	if process.stdout == nil {
		go process.wait()
	}

	return process, nil
}

// StartDetached launches a long-lived child process that commandexec does not
// manage after start. commandexec still owns command shape, environment overlay,
// log redirection, process-group detachment, PID capture, and process release.
func StartDetached(parent context.Context, spec Spec) (int, error) {
	if parent != nil {
		select {
		case <-parent.Done():
			return 0, parent.Err()
		default:
		}
	}
	if strings.TrimSpace(spec.OutputLogPath) == "" {
		return 0, errors.New("detached command requires output log path")
	}
	if spec.Stdin != "" {
		return 0, errors.New("detached command cannot use static stdin")
	}
	if spec.MergeOutput {
		return 0, errors.New("detached command cannot merge output")
	}
	if spec.Timeout > 0 {
		return 0, errors.New("detached command cannot use timeout")
	}

	cmd, err := configuredCommand(context.Background(), spec)
	if err != nil {
		return 0, err
	}
	if err := configureDetachedProcessGroup(cmd); err != nil {
		return 0, err
	}

	stdin, stdout, _, resources, err := configureProcessIO(cmd, spec)
	if err != nil {
		return 0, err
	}

	if err := cmd.Start(); err != nil {
		closeProcessResources(stdin, stdout, resources)
		return 0, fmt.Errorf("start %s: %w", cmd.Path, err)
	}
	closeProcessResources(stdin, stdout, resources)

	if cmd.Process == nil {
		return 0, fmt.Errorf("start %s: no process state after Start", cmd.Path)
	}

	pid := cmd.Process.Pid
	_ = cmd.Process.Release()
	if pid <= 0 {
		return 0, fmt.Errorf("start %s: invalid pid %d", cmd.Path, pid)
	}

	return pid, nil
}

func streamingStderrLimit(limit int) int {
	if limit > 0 {
		return limit
	}

	return defaultProcessStderrLimit
}

func configureProcessIO(cmd *exec.Cmd, spec Spec) (io.WriteCloser, io.ReadCloser, *limitedOutputBuffer, []io.Closer, error) {
	logPath := strings.TrimSpace(spec.OutputLogPath)
	if logPath != "" {
		if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
			return nil, nil, nil, nil, fmt.Errorf("create log directory: %w", err)
		}

		logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return nil, nil, nil, nil, fmt.Errorf("open log file: %w", err)
		}

		cmd.Stdout = logFile
		cmd.Stderr = logFile

		return nil, nil, nil, []io.Closer{logFile}, nil
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, nil, nil, nil, fmt.Errorf("stdout pipe: %w", err)
	}

	stderr := newLimitedOutputBuffer(streamingStderrLimit(spec.MaxOutputBytes))
	cmd.Stderr = stderr

	return stdin, stdout, stderr, nil, nil
}

// Stdin returns the process stdin stream.
func (p *Process) Stdin() io.WriteCloser {
	if p == nil {
		return nil
	}

	return p.stdin
}

// Stdout returns the process stdout stream.
func (p *Process) Stdout() io.ReadCloser {
	if p == nil {
		return nil
	}

	return p.stdout
}

// Stderr returns bounded stderr captured so far.
func (p *Process) Stderr() (string, bool) {
	if p == nil || p.stderr == nil {
		return "", false
	}

	return p.stderr.String()
}

// Done closes once commandexec has observed process exit and released resources.
func (p *Process) Done() <-chan struct{} {
	if p == nil || p.done == nil {
		done := make(chan struct{})
		close(done)

		return done
	}

	return p.done
}

// Alive reports whether commandexec still considers the process running.
func (p *Process) Alive() bool {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return false
	}

	select {
	case <-p.Done():
		return false
	default:
		return true
	}
}

// PID returns the host process id, or 0 when no process is attached.
func (p *Process) PID() int {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return 0
	}

	return p.cmd.Process.Pid
}

// Stop asks a non-streaming process to exit, waits for commandexec cleanup, and
// falls back to Close when the context expires.
func (p *Process) Stop(ctx context.Context, graceful os.Signal) error {
	if p == nil {
		return nil
	}
	if !p.Alive() {
		return p.lifecycleFailure()
	}
	if p.stdout != nil || p.stdin != nil {
		return errors.New("graceful stop requires a non-streaming process")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	if graceful != nil {
		if err := p.cmd.Process.Signal(graceful); err != nil && !errors.Is(err, os.ErrProcessDone) {
			if closeErr := p.Close(); closeErr != nil {
				return closeErr
			}

			return nil
		}
	}

	select {
	case <-p.Done():
		return p.lifecycleFailure()
	case <-ctx.Done():
		return errors.Join(ctx.Err(), p.Close())
	}
}

func (p *Process) wait() error {
	if p == nil || p.cmd == nil {
		return nil
	}

	p.waitOnce.Do(func() {
		teardownErr := p.tree.waitBeforeReap()
		p.directWaitErr = p.cmd.Wait()
		closeErr := p.tree.close()
		p.lifecycleErr = errors.Join(teardownErr, closeErr)
		p.waitErr = errors.Join(p.directWaitErr, p.lifecycleErr)
		if p.cancel != nil {
			p.cancel()
		}
		closeProcessResources(nil, nil, p.resources)
		if p.done != nil {
			close(p.done)
		}
	})

	return p.waitErr
}

func (p *Process) lifecycleFailure() error {
	if p == nil {
		return nil
	}

	_ = p.wait()
	return p.lifecycleErr
}

func (p *Process) kill() error {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return nil
	}

	if err := p.tree.terminate(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}

	return nil
}

func closeProcessResources(stdin io.WriteCloser, stdout io.ReadCloser, resources []io.Closer) {
	if stdin != nil {
		_ = stdin.Close()
	}
	if stdout != nil {
		_ = stdout.Close()
	}
	for _, resource := range resources {
		if resource != nil {
			_ = resource.Close()
		}
	}
}

// Close closes process pipes, terminates the child, and waits for cleanup.
func (p *Process) Close() error {
	if p == nil {
		return nil
	}

	var closeErrors []error
	record := func(err error) {
		if err != nil && !errors.Is(err, os.ErrClosed) && !errors.Is(err, os.ErrProcessDone) {
			closeErrors = append(closeErrors, err)
		}
	}

	if p.stdin != nil {
		record(p.stdin.Close())
	}
	if p.stdout != nil {
		record(p.stdout.Close())
	}
	record(p.kill())
	_ = p.wait()
	record(p.lifecycleErr)

	return errors.Join(closeErrors...)
}

func configuredCommand(ctx context.Context, spec Spec) (*exec.Cmd, error) {
	name, args, err := commandArgv(spec)
	if err != nil {
		return nil, err
	}
	name, args, err = sandboxCommandArgv(spec, name, args)
	if err != nil {
		return nil, err
	}

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = spec.Dir
	cmd.Env = commandEnv(spec)

	return cmd, nil
}

func managedCommand(ctx context.Context, spec Spec) (*exec.Cmd, *managedProcessTree, error) {
	cmd, err := configuredCommand(ctx, spec)
	if err != nil {
		return nil, nil, err
	}
	processTree, err := newManagedProcessTree(cmd, -1)
	if err != nil {
		return nil, nil, err
	}
	cmd.Cancel = processTree.terminate

	return cmd, processTree, nil
}

func runManagedCommand(cmd *exec.Cmd, processTree *managedProcessTree) managedCommandOutcome {
	if err := cmd.Start(); err != nil {
		_ = processTree.abortAttach()
		closeErr := processTree.close()

		return managedCommandOutcome{commandErr: err, lifecycleErr: closeErr}
	}
	if err := processTree.attach(); err != nil {
		terminateErr := processTree.terminate()
		teardownErr := processTree.waitBeforeReap()
		waitErr := cmd.Wait()
		closeErr := processTree.close()

		return managedCommandOutcome{
			commandErr:   fmt.Errorf("attach managed process tree for %s: %w", cmd.Path, err),
			lifecycleErr: errors.Join(terminateErr, teardownErr, waitErr, closeErr),
		}
	}

	teardownErr := processTree.waitBeforeReap()
	waitErr := cmd.Wait()
	closeErr := processTree.close()

	return managedCommandOutcome{
		commandErr:   waitErr,
		lifecycleErr: errors.Join(teardownErr, closeErr),
	}
}

func EnvWithCanonicalPWD(base []string, dir string, extra []string) []string {
	env := envWithOverrides(base, extra)
	dir = strings.TrimSpace(dir)

	if dir == "" {
		return env
	}

	return setEnvValue(env, "PWD", config.CanonicalizeExistingPath(dir))
}

func envWithOverrides(base []string, overrides []string) []string {
	env := append([]string(nil), base...)
	for _, entry := range overrides {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || key == "" {
			env = append(env, entry)
			continue
		}
		env = setEnvValue(env, key, value)
	}
	return env
}

func commandArgv(spec Spec) (string, []string, error) {
	switch spec.Mode {
	case ModeArgv:
		if strings.TrimSpace(spec.Name) == "" {
			return "", nil, errors.New("command name is required")
		}

		return spec.Name, append([]string(nil), spec.Args...), nil

	case ModePlainShell:
		return shellArgv(spec, "-c")

	case ModeLoginShell:
		return shellArgv(spec, "-lc")

	default:
		return "", nil, errors.New("unsupported command execution mode")
	}
}

func shellArgv(spec Spec, flag string) (string, []string, error) {
	shell := strings.TrimSpace(spec.Shell)
	if shell == "" {
		shell = "/bin/sh"
	}

	if strings.TrimSpace(spec.Command) == "" {
		return "", nil, errors.New("shell command is required")
	}

	return shell, []string{flag, spec.Command}, nil
}

func commandEnv(spec Spec) []string {
	if spec.BaseEnv == nil && len(spec.Env) == 0 && !spec.CanonicalPWD {
		return nil
	}

	base := spec.BaseEnv
	if base == nil {
		base = os.Environ()
	}

	if spec.CanonicalPWD {
		return EnvWithCanonicalPWD(base, spec.Dir, spec.Env)
	}

	return envWithOverrides(base, spec.Env)
}

func setEnvValue(env []string, key string, value string) []string {
	prefix := key + "="
	out := make([]string, 0, len(env)+1)
	replaced := false

	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			if !replaced {
				out = append(out, prefix+value)
				replaced = true
			}

			continue
		}

		out = append(out, entry)
	}

	if !replaced {
		out = append(out, prefix+value)
	}

	return out
}

type limitedOutputBuffer struct {
	mu        sync.Mutex
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func newLimitedOutputBuffer(limit int) *limitedOutputBuffer {
	return &limitedOutputBuffer{limit: limit}
}

func (b *limitedOutputBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.limit <= 0 {
		return b.buf.Write(p)
	}

	remaining := b.limit - b.buf.Len()
	if remaining <= 0 {
		b.truncated = true

		return len(p), nil
	}

	if len(p) > remaining {
		_, _ = b.buf.Write(p[:remaining])
		b.truncated = true

		return len(p), nil
	}

	return b.buf.Write(p)
}

func (b *limitedOutputBuffer) String() (string, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	output := b.buf.String()
	if b.truncated {
		output += "\n...[truncated]"
	}

	return output, b.truncated
}
