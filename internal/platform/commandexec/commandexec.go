package commandexec

import (
	"bytes"
	"context"
	"errors"
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
	TimedOut        bool
	Duration        time.Duration
	Err             error
}

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

	name, args, err := commandArgv(spec)
	if err != nil {
		return Result{ExitCode: 1, Err: err}
	}

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = spec.Dir
	cmd.Env = commandEnv(spec)
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
		}
	}

	if ctx.Err() == context.DeadlineExceeded {
		result.TimedOut = true
	}

	return result
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
