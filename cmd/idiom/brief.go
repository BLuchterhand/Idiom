package main

import (
	_ "embed"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"idiom/internal/telemetry"
)

//go:embed brief-preflight.py
var briefPreflightScript []byte

func runBriefMain(args []string, stdout io.Writer, stderr io.Writer) int {
	return runBriefMainWithTelemetry(args, stdout, stderr, nil)
}

func runBriefMainWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		printBriefUsage(stderr)
		return 0
	}
	logCLIPhase(rec, "brief_start", map[string]any{"subcommand": args[0]})
	switch args[0] {
	case "lint":
		return runBriefLint(args[1:], stdout, stderr, rec)
	default:
		fmt.Fprintf(stderr, "unknown brief command: %s\n", args[0])
		printBriefUsage(stderr)
		return 2
	}
}

func printBriefUsage(stderr io.Writer) {
	fmt.Fprintln(stderr, "Usage:")
	fmt.Fprintln(stderr, "  idiom brief lint <brief.md>")
	fmt.Fprintln(stderr)
	fmt.Fprintln(stderr, "Pre-dispatch lint for idiom implementation briefs. Checks single-purpose")
	fmt.Fprintln(stderr, "decomposition, prompt-magic triggers, fence safety, blockquoted code,")
	fmt.Fprintln(stderr, "test-anchor preload, verified-current-state presence, and exact-anchor")
	fmt.Fprintln(stderr, "specificity for every required edit path.")
	fmt.Fprintln(stderr)
	fmt.Fprintln(stderr, "Exit codes: 0 OK (with optional warnings), 1 lint failure, 2 usage error.")
}

func runBriefLint(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "brief lint: exactly one brief path is required")
		printBriefUsage(stderr)
		return 2
	}
	briefPath := strings.TrimSpace(args[0])
	if briefPath == "" {
		fmt.Fprintln(stderr, "brief lint: empty brief path")
		return 2
	}
	if _, err := os.Stat(briefPath); err != nil {
		fmt.Fprintf(stderr, "brief lint: cannot read brief: %v\n", err)
		return 2
	}
	scriptPath, cleanup, err := writeEmbeddedBriefPreflight()
	if err != nil {
		fmt.Fprintf(stderr, "brief lint: cannot stage preflight script: %v\n", err)
		return 2
	}
	defer cleanup()

	cmd := exec.Command("python3", scriptPath, briefPath)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			code := exitErr.ExitCode()
			logCLIPhase(rec, "brief_lint", map[string]any{"exit": code})
			return code
		}
		fmt.Fprintf(stderr, "brief lint: failed to run python3: %v\n", err)
		return 2
	}
	logCLIPhase(rec, "brief_lint", map[string]any{"exit": 0})
	return 0
}

func writeEmbeddedBriefPreflight() (path string, cleanup func(), err error) {
	tmpDir, err := os.MkdirTemp("", "idiom-brief-")
	if err != nil {
		return "", func() {}, err
	}
	cleanup = func() { _ = os.RemoveAll(tmpDir) }
	scriptPath := filepath.Join(tmpDir, "brief-preflight.py")
	if err := os.WriteFile(scriptPath, briefPreflightScript, 0o755); err != nil {
		cleanup()
		return "", func() {}, err
	}
	return scriptPath, cleanup, nil
}
