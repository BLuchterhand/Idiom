package codingrules

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	rulescontract "idiom/internal/contracts/codingrules"
	"idiom/internal/evidence/telemetry"
	"idiom/internal/surfaces/cli/cliflag"
	"idiom/internal/workflows/codingrules"
	"idiom/internal/workflows/postflight"
)

func Run(args []string, stdout io.Writer, stderr io.Writer) int {
	return RunWithTelemetry(args, stdout, stderr, nil)
}

func RunWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		printUsage(stderr)

		return 0
	}

	switch args[0] {
	case "check":
		code := runCheck(args[1:], stdout, stderr)
		telemetry.LogPhase(rec, "coding_rules_check", map[string]any{"exit_code": code})

		return code
	default:
		fmt.Fprintf(stderr, "coding-rules: unknown subcommand %q\n", args[0])
		printUsage(stderr)

		return 2
	}
}

func runCheck(args []string, stdout io.Writer, stderr io.Writer) int {
	fs := flag.NewFlagSet("idiom coding-rules check", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var paths cliflag.MultiFlag
	projectRoot := fs.String("project-root", "", "project root (defaults to cwd)")
	configPath := fs.String("config", "", "coding rules policy path (defaults to .idiom/coding_rules.json when present)")
	diffPath := fs.String("diff", "", "unified diff path; limits the check to modified paths")
	jsonOut := fs.Bool("json", false, "emit the report as JSON")
	failOn := fs.String("fail-on", "blocking", "exit 3 on blocking, error, or warning findings")
	fs.Var(&paths, "path", "file or directory to check; repeatable")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}

		return 2
	}

	root, err := resolveRoot(*projectRoot)
	if err != nil {
		fmt.Fprintln(stderr, err)

		return 1
	}

	checkPaths := []string(paths)
	if strings.TrimSpace(*diffPath) != "" {
		diff, err := postflight.ParseDiffFile(*diffPath)
		if err != nil {
			fmt.Fprintln(stderr, err)

			return 1
		}

		checkPaths = append(checkPaths, diff.ModifiedPaths()...)
	}

	result, err := codingrules.CheckWorkspace(context.Background(), codingrules.CheckOptions{
		ProjectRoot: root,
		ConfigPath:  *configPath,
		Paths:       checkPaths,
	})
	if err != nil {
		fmt.Fprintln(stderr, err)

		return 1
	}

	if *jsonOut {
		if err := emitJSON(stdout, result.Report); err != nil {
			fmt.Fprintln(stderr, err)

			return 1
		}
	} else {
		emitHuman(stdout, result.Report)
	}

	shouldFail, err := reportFails(result.Report, *failOn)
	if err != nil {
		fmt.Fprintln(stderr, err)

		return 2
	}
	if shouldFail {
		return 3
	}

	return 0
}

func resolveRoot(root string) (string, error) {
	if strings.TrimSpace(root) == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}

		root = cwd
	}

	abs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("coding-rules: resolve project root %q: %w", root, err)
	}

	return abs, nil
}

func emitJSON(stdout io.Writer, report rulescontract.Report) error {
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")

	return encoder.Encode(report)
}

func emitHuman(stdout io.Writer, report rulescontract.Report) {
	if len(report.Findings) == 0 {
		fmt.Fprintln(stdout, "coding-rules: PASS")

		return
	}

	for _, finding := range report.Findings {
		location := finding.Path
		if finding.Line > 0 {
			location = fmt.Sprintf("%s:%d", location, finding.Line)
		}

		symbol := finding.Symbol
		if symbol != "" {
			symbol = " " + symbol
		}

		fmt.Fprintf(stdout, "[%s] %s: %s%s - %s\n", finding.Severity, location, finding.RuleID, symbol, finding.Message)
	}

	if report.Status == rulescontract.StatusFail {
		fmt.Fprintln(stdout, "coding-rules: FAIL")
	} else {
		fmt.Fprintln(stdout, "coding-rules: PASS (warnings only)")
	}
}

func reportFails(report rulescontract.Report, failOn string) (bool, error) {
	switch strings.TrimSpace(failOn) {
	case "", "blocking":
		return report.Summary.Blocking > 0 || report.Status == rulescontract.StatusFail, nil
	case "error":
		return report.Summary.Blocking > 0 || report.Summary.Errors > 0, nil
	case "warning":
		return report.Summary.Blocking > 0 || report.Summary.Errors > 0 || report.Summary.Warnings > 0, nil
	default:
		return false, fmt.Errorf("coding-rules check: --fail-on must be blocking, error, or warning")
	}
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, `usage:
  idiom coding-rules check [--project-root DIR] [--config PATH] [--path PATH] [--diff DIFF] [--json] [--fail-on blocking|error|warning]

coding-rules checks project-local coding policy and reports structured findings.`)
}
