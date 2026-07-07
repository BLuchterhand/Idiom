package workspace

import (
	"context"
	"fmt"
	"time"

	"idiom/internal/platform/commandexec"
)

func (m *Manager) runCommand(parent context.Context, dir, name string, args ...string) (string, error) {
	result := m.runCommandResultWithEnv(parent, dir, nil, name, args...)
	output := m.truncateCommandOutput(commandexec.CombineOutput(result.Stdout, result.Stderr))

	if err := result.FailureError(fmt.Sprintf("%ds", workspaceCommandTimeoutSeconds(m.cfg.CommandTimeoutSeconds))); err != nil {
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

func (m *Manager) runCommandResultWithEnv(parent context.Context, dir string, extraEnv []string, name string, args ...string) commandexec.Result {
	timeoutSeconds := workspaceCommandTimeoutSeconds(m.cfg.CommandTimeoutSeconds)
	return commandexec.Run(parent, commandexec.Spec{
		Mode:           commandexec.ModeArgv,
		Name:           name,
		Args:           args,
		Dir:            dir,
		Env:            extraEnv,
		Timeout:        time.Duration(timeoutSeconds) * time.Second,
		MaxOutputBytes: workspaceCommandCaptureBytes(m.cfg.MaxOutputBytes),
		CanonicalPWD:   true,
	})
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
