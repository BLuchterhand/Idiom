package workspace

import (
	"context"
	"fmt"

	"idiom/internal/platform/commandexec"
)

func (m *Manager) runCommand(parent context.Context, dir, name string, args ...string) (string, error) {
	stdout, stderr, err := m.runCommandDetailedWithEnv(parent, dir, nil, name, args...)
	output := m.truncateCommandOutput(commandexec.CombineOutput(stdout, stderr))

	if err != nil {
		return output, err
	}

	return output, nil
}

func (m *Manager) truncateCommandOutput(output string) string {
	if m.cfg.MaxOutputBytes > 0 && len(output) > m.cfg.MaxOutputBytes {
		return output[:m.cfg.MaxOutputBytes] + "\n...[truncated]"
	}

	return output
}

func (m *Manager) runCommandDetailedWithEnv(parent context.Context, dir string, extraEnv []string, name string, args ...string) (string, string, error) {
	timeoutSeconds := workspaceCommandTimeoutSeconds(m.cfg.CommandTimeoutSeconds)
	result := commandexec.Run(parent, commandexec.Spec{
		Mode:           commandexec.ModeArgv,
		Name:           name,
		Args:           args,
		Dir:            dir,
		Env:            extraEnv,
		Timeout:        timeDurationSeconds(timeoutSeconds),
		MaxOutputBytes: workspaceCommandCaptureBytes(m.cfg.MaxOutputBytes),
		CanonicalPWD:   true,
	})

	if result.TimedOut {
		return result.Stdout, result.Stderr, fmt.Errorf("command timed out after %ds", timeoutSeconds)
	}

	if result.Err != nil {
		return result.Stdout, result.Stderr, fmt.Errorf("command failed: %w", result.Err)
	}

	return result.Stdout, result.Stderr, nil
}

func workspaceCommandTimeoutSeconds(seconds int) int {
	if seconds <= 0 {
		return 120
	}

	return seconds
}

func workspaceCommandCaptureBytes(displayBytes int) int {
	const diagnosticCaptureBytes = 1024 * 1024

	if displayBytes <= 0 {
		return 0
	}

	if displayBytes > diagnosticCaptureBytes {
		return displayBytes
	}

	return diagnosticCaptureBytes
}
