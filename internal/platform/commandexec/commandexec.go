package commandexec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

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

const defaultProcessStderrLimit = 64 * 1024

func Run(parent context.Context, spec Spec) Result {
	if parent == nil {
		parent = context.Background()
	}

	ctx := parent
	var cancel context.CancelFunc
	if spec.Timeout > 0 {
		ctx, cancel = context.WithTimeout(parent, spec.Timeout)
		defer cancel()
	}

	cmd, err := configuredCommand(ctx, spec)
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
	runErr := cmd.Run()
	duration := time.Since(start)
	stdoutText, stdoutTruncated := stdout.String()
	stderrText, stderrTruncated := stderr.String()
	if spec.MergeOutput {
		stderrText = ""
		stderrTruncated = false
	}

	result := Result{
		Stdout:          stdoutText,
		Stderr:          stderrText,
		StdoutTruncated: stdoutTruncated,
		StderrTruncated: stderrTruncated,
		ExitCode:        0,
		Duration:        duration,
		Err:             runErr,
	}

	if runErr != nil {
		result.ExitCode = 1
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			result.ExitCode = exitErr.ExitCode()
			result.ExitError = true
		} else if errors.Is(runErr, exec.ErrNotFound) {
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

	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr *limitedOutputBuffer

	waitOnce sync.Once
	waitErr  error
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

	cmd, err := configuredCommand(ctx, spec)
	if err != nil {
		if cancel != nil {
			cancel()
		}

		return nil, err
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		if cancel != nil {
			cancel()
		}

		return nil, fmt.Errorf("stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		if cancel != nil {
			cancel()
		}

		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	stderr := newLimitedOutputBuffer(streamingStderrLimit(spec.MaxOutputBytes))
	cmd.Stderr = stderr

	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		if cancel != nil {
			cancel()
		}

		return nil, fmt.Errorf("start %s: %w", cmd.Path, err)
	}

	return &Process{
		cmd:    cmd,
		cancel: cancel,
		stdin:  stdin,
		stdout: stdout,
		stderr: stderr,
	}, nil
}

func streamingStderrLimit(limit int) int {
	if limit > 0 {
		return limit
	}

	return defaultProcessStderrLimit
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

func (p *Process) wait() error {
	if p == nil || p.cmd == nil {
		return nil
	}

	p.waitOnce.Do(func() {
		p.waitErr = p.cmd.Wait()
		if p.cancel != nil {
			p.cancel()
		}
	})

	return p.waitErr
}

func (p *Process) kill() error {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return nil
	}

	if err := p.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}

	return nil
}

// Close closes process pipes, terminates the child, and waits for cleanup.
func (p *Process) Close() error {
	if p == nil {
		return nil
	}

	var firstErr error
	record := func(err error) {
		if err != nil && firstErr == nil && !errors.Is(err, os.ErrClosed) && !errors.Is(err, os.ErrProcessDone) {
			firstErr = err
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

	return firstErr
}

func CombineOutput(stdout string, stderr string) string {
	output := strings.TrimSpace(stdout)
	errText := strings.TrimSpace(stderr)

	if errText != "" {
		if output != "" {
			output += "\n"
		}

		output += errText
	}

	return output
}

func configuredCommand(ctx context.Context, spec Spec) (*exec.Cmd, error) {
	name, args, err := commandArgv(spec)
	if err != nil {
		return nil, err
	}

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = spec.Dir
	cmd.Env = commandEnv(spec)

	return cmd, nil
}

func EnvWithCanonicalPWD(base []string, dir string, extra []string) []string {
	env := append([]string(nil), base...)

	env = append(env, extra...)
	dir = strings.TrimSpace(dir)

	if dir == "" {
		return env
	}

	return setEnvValue(env, "PWD", config.CanonicalizeExistingPath(dir))
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

	env := append([]string(nil), base...)

	return append(env, spec.Env...)
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
