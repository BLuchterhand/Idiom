package workspace

import (
	"context"
	"fmt"
	"time"

	"idiom/internal/platform/commandexec"
)

func (m *Manager) runCommand(parent context.Context, dir, name string, args ...string) (string, error) {
	result := m.runCommandResult(parent, commandexec.Spec{Mode: commandexec.ModeArgv, Dir: dir, Name: name, Args: args})
	output := result.DisplayProjection(m.cfg.MaxOutputBytes).Text

	if err := result.FailureError(fmt.Sprintf("%ds", workspaceCommandTimeoutSeconds(m.cfg.CommandTimeoutSeconds))); err != nil {
		return output, err
	}

	return output, nil
}

func (m *Manager) runCommandResult(parent context.Context, spec commandexec.Spec) commandexec.Result {
	timeoutSeconds := workspaceCommandTimeoutSeconds(m.cfg.CommandTimeoutSeconds)
	spec.Timeout = time.Duration(timeoutSeconds) * time.Second
	spec.MaxOutputBytes = workspaceCommandCaptureBytes(m.cfg.MaxOutputBytes)
	spec.CanonicalPWD = true

	return commandexec.Run(parent, spec)
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
