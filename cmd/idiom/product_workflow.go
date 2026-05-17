package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"idiom/internal/productworkflow"
	"idiom/internal/telemetry"
)

type productLintFinding struct {
	Code    string `json:"code"`
	Path    string `json:"path"`
	Message string `json:"message"`
}

type productLintResult struct {
	Status       string               `json:"status"`
	EffortRoot   string               `json:"effort_root"`
	CheckedFiles []string             `json:"checked_files"`
	Findings     []productLintFinding `json:"findings"`
}

func runProductMainWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	if len(args) == 0 {
		printProductUsage(stderr)
		return 2
	}
	switch args[0] {
	case "lint":
		return runProductLint(args[1:], stdout, stderr, rec)
	case "-h", "--help", "help":
		printProductUsage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "product: unknown subcommand %q\n", args[0])
		printProductUsage(stderr)
		return 2
	}
}

func printProductUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  idiom product lint --effort-root <path> [--json]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Deterministic lint for product workflow effort folders.")
	fmt.Fprintln(w, "Validates workflow_state.json, tdd.json, evidence artifacts,")
	fmt.Fprintln(w, "implementation_ledger.jsonl, and all JSON report files.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Exit codes: 0 pass, 1 lint findings, 2 usage error.")
}

func runProductLint(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	fs := flag.NewFlagSet("idiom product lint", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var effortRoot string
	var jsonOutput bool
	fs.StringVar(&effortRoot, "effort-root", "", "path to the effort folder root")
	fs.BoolVar(&jsonOutput, "json", false, "emit structured JSON output")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if strings.TrimSpace(effortRoot) == "" {
		fmt.Fprintln(stderr, "product lint: --effort-root is required")
		return 2
	}
	absRoot, err := filepath.Abs(effortRoot)
	if err != nil {
		fmt.Fprintf(stderr, "product lint: resolve effort root: %v\n", err)
		return 2
	}
	result := lintEffortFolder(absRoot)
	logCLIPhase(rec, "product_lint", map[string]any{
		"status":        result.Status,
		"finding_count": len(result.Findings),
	})
	if jsonOutput {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(result); err != nil {
			fmt.Fprintf(stderr, "product lint: encode JSON: %v\n", err)
			return 1
		}
	} else {
		if result.Status == "pass" {
			fmt.Fprintf(stdout, "product lint: %s OK (%d files checked)\n", filepath.Base(absRoot), len(result.CheckedFiles))
		} else {
			fmt.Fprintf(stdout, "product lint: %s FAILED (%d findings)\n", filepath.Base(absRoot), len(result.Findings))
			for _, f := range result.Findings {
				fmt.Fprintf(stdout, "  [%s] %s: %s\n", f.Code, f.Path, f.Message)
			}
		}
	}
	if result.Status != "pass" {
		return 1
	}
	return 0
}

func lintEffortFolder(root string) productLintResult {
	result := productLintResult{
		EffortRoot:   root,
		CheckedFiles: []string{},
		Findings:     []productLintFinding{},
	}
	addFinding := func(code, relPath, message string) {
		result.Findings = append(result.Findings, productLintFinding{
			Code:    code,
			Path:    relPath,
			Message: message,
		})
	}
	checkFile := func(relPath string, validate func([]byte) error) {
		fullPath := filepath.Join(root, relPath)
		result.CheckedFiles = append(result.CheckedFiles, relPath)
		data, err := os.ReadFile(fullPath)
		if err != nil {
			if os.IsNotExist(err) {
				addFinding("missing_file", relPath, "required file not found")
			} else {
				addFinding("read_error", relPath, err.Error())
			}
			return
		}
		if err := validate(data); err != nil {
			addFinding("parse_error", relPath, err.Error())
		}
	}

	checkFile("workflow_state.json", func(data []byte) error {
		_, err := productworkflow.ParseWorkflowStateJSON(data)
		return err
	})
	checkFile("tdd.json", func(data []byte) error {
		_, err := productworkflow.ParseTDDLedgerJSON(data)
		return err
	})
	checkFile("evidence/evidence.jsonl", func(data []byte) error {
		_, err := productworkflow.ParseEvidenceCardsJSONL(data)
		return err
	})
	checkFile("evidence/traceability_matrix.json", func(data []byte) error {
		_, err := productworkflow.ParseTraceabilityMatrixJSON(data)
		return err
	})
	checkFile("implementation_ledger.jsonl", func(data []byte) error {
		_, err := productworkflow.ParseImplementationLedgerJSONL(data)
		return err
	})

	reportsDir := filepath.Join(root, "reports")
	entries, err := os.ReadDir(reportsDir)
	if err != nil {
		if os.IsNotExist(err) {
			addFinding("missing_dir", "reports/", "required directory not found")
		} else {
			addFinding("read_error", "reports/", err.Error())
		}
	} else {
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			relPath := filepath.Join("reports", entry.Name())
			result.CheckedFiles = append(result.CheckedFiles, relPath)
			data, readErr := os.ReadFile(filepath.Join(reportsDir, entry.Name()))
			if readErr != nil {
				addFinding("read_error", relPath, readErr.Error())
				continue
			}
			var raw json.RawMessage
			if err := json.Unmarshal(data, &raw); err != nil {
				addFinding("parse_error", relPath, fmt.Sprintf("invalid JSON: %v", err))
			}
		}
	}

	if len(result.Findings) == 0 {
		result.Status = "pass"
	} else {
		result.Status = "fail"
	}
	return result
}
