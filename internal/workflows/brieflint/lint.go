package brieflint

import (
	"context"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"idiom/internal/platform/commandexec"
)

//go:embed brief-preflight.py
var preflightFS embed.FS

type Finding struct {
	Severity string `json:"severity"`
	Category string `json:"category"`
	Brief    string `json:"brief"`
	Message  string `json:"message"`
}

type Report struct {
	Brief    string    `json:"brief"`
	Exit     int       `json:"exit"`
	Findings []Finding `json:"findings"`
}

func BuildReport(ctx context.Context, briefPath string, workdir string, timeout time.Duration) (Report, error) {
	result, err := RunEmbeddedPreflight(ctx, briefPath, workdir, timeout)
	if err != nil {
		return Report{}, err
	}

	exit := 0
	if result.Err != nil {
		if !result.ExitError {
			return Report{}, fmt.Errorf("failed to run python3: %w", result.Err)
		}

		exit = result.ExitCode
	}

	combined := result.Stdout + result.Stderr

	return Report{
		Brief:    briefPath,
		Exit:     exit,
		Findings: ParseFindings(briefPath, combined),
	}, nil
}

func RunEmbeddedPreflight(ctx context.Context, briefPath string, workdir string, timeout time.Duration) (commandexec.Result, error) {
	scriptPath, cleanup, err := WriteEmbeddedPreflight()
	if err != nil {
		return commandexec.Result{}, fmt.Errorf("cannot stage preflight script: %w", err)
	}
	defer cleanup()

	return commandexec.Run(ctx, commandexec.Spec{
		Name:    "python3",
		Args:    []string{scriptPath, briefPath},
		Dir:     workdir,
		Timeout: timeout,
	}), nil
}

func WriteEmbeddedPreflight() (path string, cleanup func(), err error) {
	tmpDir, err := os.MkdirTemp("", "idiom-brief-")
	if err != nil {
		return "", func() {}, err
	}

	cleanup = func() { _ = os.RemoveAll(tmpDir) }

	scriptPath := filepath.Join(tmpDir, "brief-preflight.py")
	script, err := preflightFS.ReadFile("brief-preflight.py")
	if err != nil {
		cleanup()

		return "", func() {}, err
	}

	if err := os.WriteFile(scriptPath, script, 0o755); err != nil {
		cleanup()

		return "", func() {}, err
	}

	return scriptPath, cleanup, nil
}

func ParseFindings(briefPath, combined string) []Finding {
	var findings []Finding

	for _, raw := range strings.Split(combined, "\n") {
		line := strings.TrimRight(raw, "\r")
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "idiom brief preflight: WARN ") {
			message := strings.TrimSpace(strings.TrimPrefix(trimmed, "idiom brief preflight: WARN "))
			if message != "" {
				findings = append(findings, Finding{
					Severity: "WARN",
					Category: classifyMessage(message),
					Brief:    briefPath,
					Message:  message,
				})
			}

			continue
		}

		if !strings.HasPrefix(trimmed, "- ") {
			continue
		}

		body := strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))
		severity := "BLOCK"
		message := body

		switch {
		case strings.HasPrefix(body, "WARN:"):
			severity = "WARN"
			message = strings.TrimSpace(strings.TrimPrefix(body, "WARN:"))
		case strings.HasPrefix(body, "INFO:"):
			severity = "INFO"
			message = strings.TrimSpace(strings.TrimPrefix(body, "INFO:"))
		}

		findings = append(findings, Finding{
			Severity: severity,
			Category: classifyMessage(message),
			Brief:    briefPath,
			Message:  message,
		})
	}

	return findings
}

func classifyMessage(msg string) string {
	low := strings.ToLower(msg)
	switch {
	case strings.Contains(low, "exact-anchor specificity"), strings.Contains(low, "anchor specificity"):
		return "anchor-specificity"
	case strings.Contains(low, "verified-current-state"), strings.Contains(low, "verified current state"):
		return "missing-current-state"
	case strings.Contains(low, "anchor verification skipped"):
		return "anchor-verification-skipped"
	case strings.Contains(low, "placeholder"):
		return "unfilled-placeholder"
	case strings.Contains(low, "content fence"), strings.Contains(low, "content-fence"):
		return "content-fence"
	case strings.Contains(low, "multi-action"):
		return "multi-action-row"
	case strings.Contains(low, "discovery-mandate"), strings.Contains(low, "discovery mandate"):
		return "discovery-mandate"
	case strings.Contains(low, "branching"):
		return "branching-language"
	case strings.Contains(low, "prose-anchor"), strings.Contains(low, "prose anchor"):
		return "prose-anchor"
	case strings.Contains(low, "outer ```"):
		return "outer-fence"
	}

	return "other"
}
