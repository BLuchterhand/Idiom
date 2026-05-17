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
	fmt.Fprintln(w, "implementation_ledger.jsonl, prd.md, adrs/index.json, ADR markdown,")
	fmt.Fprintln(w, "and all JSON report files.")
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
	var parsedCards []productworkflow.EvidenceCard
	var parsedMatrix *productworkflow.TraceabilityMatrix
	var parsedPRD *productworkflow.PRDDocument
	var parsedADRIndex *productworkflow.ADRIndex
	var parsedTDD *productworkflow.TDDLedger
	var parsedLedger []productworkflow.ImplementationLedgerRecord

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
		l, err := productworkflow.ParseTDDLedgerJSON(data)
		if err == nil {
			parsedTDD = &l
		}
		return err
	})
	checkFile("evidence/evidence.jsonl", func(data []byte) error {
		c, err := productworkflow.ParseEvidenceCardsJSONL(data)
		if err == nil {
			parsedCards = c
		}
		return err
	})
	checkFile("evidence/traceability_matrix.json", func(data []byte) error {
		m, err := productworkflow.ParseTraceabilityMatrixJSON(data)
		if err == nil {
			parsedMatrix = &m
		}
		return err
	})
	checkFile("implementation_ledger.jsonl", func(data []byte) error {
		recs, err := productworkflow.ParseImplementationLedgerJSONL(data)
		if err == nil {
			parsedLedger = recs
		}
		return err
	})
	checkFile("prd.md", func(data []byte) error {
		d, err := productworkflow.ParsePRDMarkdown(data)
		if err == nil {
			parsedPRD = &d
		}
		return err
	})
	checkFile("adrs/index.json", func(data []byte) error {
		idx, err := productworkflow.ParseADRIndexJSON(data)
		if err == nil {
			parsedADRIndex = &idx
		}
		if err != nil {
			return err
		}
		for _, entry := range idx.ADRs {
			adrRelPath := filepath.Join("adrs", entry.Path)
			checkFile(adrRelPath, func(adrData []byte) error {
				_, adrErr := productworkflow.ParseADRMarkdown(adrData)
				return adrErr
			})
		}
		return nil
	})

	reportPaths := map[string]bool{}
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
			reportPaths[relPath] = true
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

	briefPaths := map[string]bool{}
	briefsDir := filepath.Join(root, "briefs")
	if briefEntries, briefErr := os.ReadDir(briefsDir); briefErr == nil {
		for _, be := range briefEntries {
			if !be.IsDir() {
				briefPaths[filepath.Join("briefs", be.Name())] = true
			}
		}
	}

	if parsedMatrix != nil {
		var evidenceIDs map[string]bool
		if parsedCards != nil {
			evidenceIDs = make(map[string]bool)
			for _, c := range parsedCards {
				evidenceIDs[c.CardID] = true
			}
		}
		var prdReqIDs map[string]bool
		if parsedPRD != nil {
			prdReqIDs = make(map[string]bool)
			for _, r := range parsedPRD.Requirements {
				prdReqIDs[r.ID] = true
			}
		}
		var adrIDSet map[string]bool
		if parsedADRIndex != nil {
			adrIDSet = make(map[string]bool)
			for _, a := range parsedADRIndex.ADRs {
				adrIDSet[a.ADRID] = true
			}
		}
		tddIDSet := make(map[string]bool)
		if parsedTDD != nil {
			for _, o := range parsedTDD.Obligations {
				tddIDSet[o.ID] = true
			}
		}
		for _, rec := range parsedLedger {
			for _, id := range rec.TDDObligationsSatisfied {
				tddIDSet[id] = true
			}
		}
		linkErrs := productworkflow.ValidateTraceabilityCrossLinks(
			*parsedMatrix, evidenceIDs, prdReqIDs, adrIDSet, tddIDSet, briefPaths, reportPaths,
		)
		for _, le := range linkErrs {
			addFinding("broken_link", "evidence/traceability_matrix.json", le.Message)
		}
	}

	if len(result.Findings) == 0 {
		result.Status = "pass"
	} else {
		result.Status = "fail"
	}

	wsData, wsErr := os.ReadFile(filepath.Join(root, "workflow_state.json"))
	if wsErr == nil {
		var ws struct {
			ArtifactPaths map[string]string `json:"artifact_paths"`
		}
		if json.Unmarshal(wsData, &ws) == nil {
			if rp, ok := ws.ArtifactPaths["research_profile"]; ok && rp != "" {
				checkFile(rp, func(data []byte) error {
					_, err := productworkflow.ParseResearchProfileJSON(data)
					return err
				})
			}
		}
		if len(result.Findings) == 0 {
			result.Status = "pass"
		} else {
			result.Status = "fail"
		}
	}

	return result
}
