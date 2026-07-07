package workspace

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"idiom/internal/platform/config"
)

func (m *Manager) runCommand(parent context.Context, dir, name string, args ...string) (string, error) {
	stdout, stderr, err := m.runCommandDetailed(parent, dir, name, args...)
	output := m.truncateCommandOutput(combineCommandOutput(stdout, stderr))

	if err != nil {
		return output, err
	}

	return output, nil
}

func combineCommandOutput(stdout string, stderr string) string {
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

func (m *Manager) truncateCommandOutput(output string) string {
	if m.cfg.MaxOutputBytes > 0 && len(output) > m.cfg.MaxOutputBytes {
		return output[:m.cfg.MaxOutputBytes] + "\n...[truncated]"
	}

	return output
}

func (m *Manager) runCommandDetailed(parent context.Context, dir, name string, args ...string) (string, string, error) {
	return m.runCommandDetailedWithEnv(parent, dir, nil, name, args...)
}

func (m *Manager) runCommandDetailedWithEnv(parent context.Context, dir string, extraEnv []string, name string, args ...string) (string, string, error) {
	timeoutSeconds := workspaceCommandTimeoutSeconds(m.cfg.CommandTimeoutSeconds)
	timeout := timeDurationSeconds(timeoutSeconds)
	ctx, cancel := context.WithTimeout(parent, timeout)

	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)

	cmd.Dir = dir
	cmd.Env = envWithCanonicalPWD(os.Environ(), dir, extraEnv)

	var (
		stdout bytes.Buffer
		stderr bytes.Buffer
	)

	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	if ctx.Err() == context.DeadlineExceeded {
		return stdout.String(), stderr.String(), fmt.Errorf("command timed out after %ds", timeoutSeconds)
	}

	if err != nil {
		return stdout.String(), stderr.String(), fmt.Errorf("command failed: %w", err)
	}

	return stdout.String(), stderr.String(), nil
}

func workspaceCommandTimeoutSeconds(seconds int) int {
	if seconds <= 0 {
		return 120
	}

	return seconds
}

func envWithCanonicalPWD(base []string, dir string, extra []string) []string {
	env := append([]string(nil), base...)

	env = append(env, extra...)
	dir = strings.TrimSpace(dir)

	if dir == "" {
		return env
	}

	env = setEnvValue(env, "PWD", config.CanonicalizeExistingPath(dir))

	return env
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
