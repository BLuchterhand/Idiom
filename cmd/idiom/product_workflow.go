package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
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
	case "audit":
		return runProductAudit(args[1:], stdout, stderr, rec)
	case "status":
		return runProductStatus(args[1:], stdout, stderr, rec)
	case "research":
		return runProductResearch(args[1:], stdout, stderr, rec)
	case "prd":
		return runProductPRD(args[1:], stdout, stderr, rec)
	case "adr":
		return runProductADR(args[1:], stdout, stderr, rec)
	case "tdd":
		return runProductTDD(args[1:], stdout, stderr, rec)
	case "brief":
		return runProductBrief(args[1:], stdout, stderr, rec)
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
	fmt.Fprintln(w, "  idiom product audit --effort-root <path> [--json]")
	fmt.Fprintln(w, "  idiom product status --effort-root <path> [--json]")
	fmt.Fprintln(w, "  idiom product research --project-root <path> [--json]")
	fmt.Fprintln(w, "  idiom product research --project-root <path> --plan-runs [--profile <profile-id-or-name>] [--research-output-dir research/outputs] [--json]")
	fmt.Fprintln(w, "  idiom product research --project-root <path> --effort-root <path> --merge-evidence [--profile <profile-id-or-name>] [--research-output-dir research/outputs] [--json]")
	fmt.Fprintln(w, "  idiom product research --project-root <path> --effort-root <path> --render-prompts [--profile <profile-id-or-name>] [--research-output-dir research/outputs] [--research-prompt-dir research/prompts] [--json]")
	fmt.Fprintln(w, "  idiom product prd --effort-root <path> --render-prompt [--prd-prompt prd.prompt.md] [--prd-output prd.md] [--json]")
	fmt.Fprintln(w, "  idiom product adr --effort-root <path> --render-prompt [--adr-prompt adrs/adr.prompt.md] [--adr-output adrs/ADR-draft.md] [--json]")
	fmt.Fprintln(w, "  idiom product tdd --effort-root <path> --plan-obligations [--json]")
	fmt.Fprintln(w, "  idiom product tdd --effort-root <path> --evaluate-coverage [--json]")
	fmt.Fprintln(w, "  idiom product brief --effort-root <path> --render [--brief-output briefs/<task>.md] [--json]")
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
	var parsedADRs []productworkflow.ParsedADR

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
		if err != nil {
			return err
		}
		parsedADRIndex = &idx
		for _, entry := range idx.ADRs {
			adrRelPath := filepath.Join("adrs", entry.Path)
			checkFile(adrRelPath, func(adrData []byte) error {
				adrDoc, adrErr := productworkflow.ParseADRMarkdown(adrData)
				if adrErr != nil {
					return adrErr
				}
				parsedADRs = append(parsedADRs, productworkflow.ParsedADR{
					Doc:     adrDoc,
					RelPath: adrRelPath,
				})
				return nil
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
			reportType, known := productworkflow.ClassifyReportFileName(entry.Name())
			if !known {
				var raw json.RawMessage
				if err := json.Unmarshal(data, &raw); err != nil {
					addFinding("parse_error", relPath, fmt.Sprintf("invalid JSON: %v", err))
				}
				continue
			}
			switch reportType {
			case "implementation":
				if _, err := productworkflow.ParseImplementationReportJSON(data); err != nil {
					addFinding("parse_error", relPath, err.Error())
				}
			case "review":
				if _, err := productworkflow.ParseReviewReportJSON(data); err != nil {
					addFinding("parse_error", relPath, err.Error())
				}
			case "investigation":
				if _, err := productworkflow.ParseInvestigationReportJSON(data); err != nil {
					addFinding("parse_error", relPath, err.Error())
				}
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

	if parsedPRD != nil && parsedCards != nil {
		evidenceIDs := make(map[string]bool)
		for _, c := range parsedCards {
			evidenceIDs[c.CardID] = true
		}
		for _, req := range parsedPRD.Requirements {
			for _, ref := range req.EvidenceRefs {
				if !evidenceIDs[ref] {
					addFinding("broken_link", "prd.md", fmt.Sprintf(
						"PRD requirement %s references evidence card %q not found in evidence ledger", req.ID, ref,
					))
				}
			}
		}
	}

	if len(parsedADRs) > 0 {
		var prdReqIDs map[string]bool
		if parsedPRD != nil {
			prdReqIDs = make(map[string]bool)
			for _, r := range parsedPRD.Requirements {
				prdReqIDs[r.ID] = true
			}
		}
		var evidenceIDs map[string]bool
		if parsedCards != nil {
			evidenceIDs = make(map[string]bool)
			for _, c := range parsedCards {
				evidenceIDs[c.CardID] = true
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
		adrLinkErrs := productworkflow.ValidateADRCrossLinks(parsedADRs, prdReqIDs, evidenceIDs, tddIDSet)
		for _, le := range adrLinkErrs {
			addFinding("broken_link", le.ADRPath, le.Message)
		}
		supersessionErrs := productworkflow.ValidateADRSupersessionLinks(parsedADRs)
		for _, se := range supersessionErrs {
			addFinding(se.Code, se.ADRPath, se.Message)
		}
	}

	if parsedPRD != nil {
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
		prdTDDErrs := productworkflow.ValidatePRDTDDObligationCrossLinks(parsedPRD.Requirements, tddIDSet)
		for _, le := range prdTDDErrs {
			addFinding("broken_link", "tdd.json", le.Message)
		}
	}

	{
		plannedScopes := map[string]*productworkflow.AtomScope{}
		closedTasks := map[string]bool{}
		for _, rec := range parsedLedger {
			switch rec.RecordType {
			case "atom_planned":
				if rec.Scope != nil {
					plannedScopes[rec.TaskID] = rec.Scope
				}
			case "atom_closed":
				closedTasks[rec.TaskID] = true
			}
		}
		// Scope gate lint is a closeout-time guard for active/in-progress
		// atoms; historical closed atoms were closed before this gate existed
		// and remain covered by their recorded review/investigation.
		taskPaths := map[string][]string{}
		for _, rec := range parsedLedger {
			if rec.RecordType != "implementation_completed" && rec.RecordType != "fallback_completion" {
				continue
			}
			if closedTasks[rec.TaskID] {
				continue
			}
			taskPaths[rec.TaskID] = append(taskPaths[rec.TaskID], rec.ChangedPaths...)
		}
		sortedTIDs := make([]string, 0, len(taskPaths))
		for tid := range taskPaths {
			sortedTIDs = append(sortedTIDs, tid)
		}
		sort.Strings(sortedTIDs)
		for _, tid := range sortedTIDs {
			paths := taskPaths[tid]
			scope, ok := plannedScopes[tid]
			if !ok || scope == nil || len(paths) == 0 {
				continue
			}
			gateResult := productworkflow.EvaluateScopeGate(productworkflow.ScopeGateInput{
				ExpectedFiles:      scope.ExpectedFiles,
				AllowedFiles:       scope.AllowedFiles,
				ForbiddenFiles:     scope.Forbidden,
				ForbiddenPatterns:  scope.ForbiddenPatterns,
				ActualChangedPaths: paths,
			})
			for _, b := range gateResult.Blockers {
				msg := fmt.Sprintf("task %s scope_gate_%s: %s", tid, b.Code, b.Message)
				addFinding("scope_gate_"+b.Code, "implementation_ledger.jsonl", msg)
			}
		}
	}

	var atoms []productworkflow.AtomCoverageCheck
	for _, rec := range parsedLedger {
		if rec.RecordType == "implementation_completed" || rec.RecordType == "fallback_completion" || rec.RecordType == "partial_implementation_recorded" {
			atoms = append(atoms, productworkflow.AtomCoverageCheck{
				TaskID:       rec.TaskID,
				ChangedPaths: rec.ChangedPaths,
			})
		}
	}
	if len(atoms) > 0 {
		gitPaths := gitChangedPaths(root)
		if len(gitPaths) > 0 {
			rel, _ := filepath.Rel(findGitRepoRoot(root), root)
			coverageFindings := productworkflow.ValidateLedgerCoverage(atoms, gitPaths, rel)
			for _, f := range coverageFindings {
				addFinding(f.Code, "implementation_ledger.jsonl", f.Message)
			}
		}
	}

	{
		commitHashRequiredAfter := "2026-05-17T23:00:00Z"
		var commitAtoms []productworkflow.AtomCommitCoverage
		commitHashSet := map[string]bool{}
		for _, rec := range parsedLedger {
			if rec.RecordType == "implementation_completed" || rec.RecordType == "fallback_completion" || rec.RecordType == "partial_implementation_recorded" {
				if rec.Timestamp >= commitHashRequiredAfter {
					commitAtoms = append(commitAtoms, productworkflow.AtomCommitCoverage{
						TaskID:       rec.TaskID,
						CommitHash:   rec.CommitHash,
						ChangedPaths: rec.ChangedPaths,
					})
				}
				if rec.CommitHash != "" {
					commitHashSet[rec.CommitHash] = true
				}
			}
		}
		if len(commitAtoms) > 0 {
			repoRoot := findGitRepoRoot(root)
			commitPaths := map[string][]string{}
			if repoRoot != "" {
				effortRel, _ := filepath.Rel(repoRoot, root)
				hashes := make([]string, 0, len(commitHashSet))
				for h := range commitHashSet {
					hashes = append(hashes, h)
				}
				sort.Strings(hashes)
				rawCommitPaths := gitCommitPaths(repoRoot, hashes)
				prefix := effortRel + "/"
				for hash, paths := range rawCommitPaths {
					var stripped []string
					for _, p := range paths {
						if effortRel != "" && strings.HasPrefix(p, prefix) {
							stripped = append(stripped, strings.TrimPrefix(p, prefix))
						} else if effortRel != "" && p == effortRel {
							continue
						} else {
							stripped = append(stripped, p)
						}
					}
					commitPaths[hash] = stripped
				}
				for i := range commitAtoms {
					var norm []string
					for _, p := range commitAtoms[i].ChangedPaths {
						if effortRel != "" && strings.HasPrefix(p, prefix) {
							norm = append(norm, strings.TrimPrefix(p, prefix))
						} else {
							norm = append(norm, p)
						}
					}
					commitAtoms[i].ChangedPaths = norm
				}
			}
			perAtomFindings := productworkflow.ValidatePerAtomCoverage(commitAtoms, commitPaths)
			for _, f := range perAtomFindings {
				addFinding(f.Code, "implementation_ledger.jsonl", f.Message)
			}
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

type productAuditSection struct {
	Name     string               `json:"name"`
	Status   string               `json:"status"`
	Findings []productLintFinding `json:"findings"`
}

type productAuditResult struct {
	Status       string                `json:"status"`
	EffortRoot   string                `json:"effort_root"`
	CheckedFiles []string              `json:"checked_files"`
	Sections     []productAuditSection `json:"sections"`
	Findings     []productLintFinding  `json:"findings"`
}

func buildAuditResult(lintResult productLintResult) productAuditResult {
	sectionMap := map[string]*productAuditSection{
		"prd":          {Name: "prd", Status: "pass"},
		"adr":          {Name: "adr", Status: "pass"},
		"tdd":          {Name: "tdd", Status: "pass"},
		"traceability": {Name: "traceability", Status: "pass"},
		"reports":      {Name: "reports", Status: "pass"},
		"scope_gate":   {Name: "scope_gate", Status: "pass"},
	}
	for _, f := range lintResult.Findings {
		section := productworkflow.ClassifySection(f.Path)
		if section == "" {
			if strings.HasPrefix(f.Code, "scope_gate_") {
				section = "scope_gate"
			} else {
				continue
			}
		}
		sec := sectionMap[section]
		sec.Findings = append(sec.Findings, f)
		sec.Status = "fail"
	}
	sections := make([]productAuditSection, 0, len(sectionMap))
	for _, name := range []string{"prd", "adr", "tdd", "traceability", "reports", "scope_gate"} {
		sections = append(sections, *sectionMap[name])
	}
	status := "pass"
	if lintResult.Status == "fail" {
		status = "fail"
	}
	return productAuditResult{
		Status:       status,
		EffortRoot:   lintResult.EffortRoot,
		CheckedFiles: lintResult.CheckedFiles,
		Sections:     sections,
		Findings:     lintResult.Findings,
	}
}

func runProductAudit(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	fs := flag.NewFlagSet("idiom product audit", flag.ContinueOnError)
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
		fmt.Fprintln(stderr, "product audit: --effort-root is required")
		return 2
	}
	absRoot, err := filepath.Abs(effortRoot)
	if err != nil {
		fmt.Fprintf(stderr, "product audit: resolve effort root: %v\n", err)
		return 2
	}
	lintResult := lintEffortFolder(absRoot)
	auditResult := buildAuditResult(lintResult)
	logCLIPhase(rec, "product_audit", map[string]any{
		"status":        auditResult.Status,
		"finding_count": len(auditResult.Findings),
	})
	if jsonOutput {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(auditResult); err != nil {
			fmt.Fprintf(stderr, "product audit: encode JSON: %v\n", err)
			return 1
		}
	} else {
		if auditResult.Status == "pass" {
			fmt.Fprintf(stdout, "product audit: %s OK (%d files checked)\n", filepath.Base(absRoot), len(auditResult.CheckedFiles))
		} else {
			fmt.Fprintf(stdout, "product audit: %s FAILED (%d findings)\n", filepath.Base(absRoot), len(auditResult.Findings))
			for _, f := range auditResult.Findings {
				fmt.Fprintf(stdout, "  [%s] %s: %s\n", f.Code, f.Path, f.Message)
			}
		}
		for _, sec := range auditResult.Sections {
			fmt.Fprintf(stdout, "  %s: %s", sec.Name, sec.Status)
			if len(sec.Findings) > 0 {
				fmt.Fprintf(stdout, " (%d findings)", len(sec.Findings))
			}
			fmt.Fprintln(stdout)
		}
	}
	if auditResult.Status != "pass" {
		return 1
	}
	return 0
}

func runProductStatus(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	fs := flag.NewFlagSet("idiom product status", flag.ContinueOnError)
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
		fmt.Fprintln(stderr, "product status: --effort-root is required")
		return 2
	}
	absRoot, err := filepath.Abs(effortRoot)
	if err != nil {
		fmt.Fprintf(stderr, "product status: resolve effort root: %v\n", err)
		return 2
	}

	var lintSummary *productworkflow.LintSummaryResult
	lintResult := lintEffortFolder(absRoot)
	lintSummary = &productworkflow.LintSummaryResult{
		Status:       lintResult.Status,
		CheckedFiles: lintResult.CheckedFiles,
		Findings:     make([]productworkflow.LintSummaryFinding, len(lintResult.Findings)),
	}
	for i, f := range lintResult.Findings {
		lintSummary.Findings[i] = productworkflow.LintSummaryFinding{
			Code:    f.Code,
			Path:    f.Path,
			Message: f.Message,
		}
	}

	view, wsErr := productworkflow.LoadProductStatus(absRoot, lintSummary)
	exitCode := 0
	if wsErr != nil {
		exitCode = 1
	}
	if view.AuditStatus != "pass" {
		exitCode = 1
	}

	logCLIPhase(rec, "product_status", map[string]any{
		"audit_status":  view.AuditStatus,
		"effort_id":     view.EffortID,
		"current_state": view.CurrentState,
	})

	if jsonOutput {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(view); err != nil {
			fmt.Fprintf(stderr, "product status: encode JSON: %v\n", err)
			return 1
		}
		return exitCode
	}

	if wsErr != nil {
		fmt.Fprintf(stdout, "product status: ERROR %s\n", absRoot)
		for _, b := range view.Blockers {
			fmt.Fprintf(stdout, "  [%s] %s\n", b.Code, b.Message)
		}
		return exitCode
	}

	auditLabel := "OK"
	if view.AuditStatus != "pass" {
		auditLabel = "FAILED"
	}
	fmt.Fprintf(stdout, "product status: %s %s\n", filepath.Base(absRoot), view.EffortID)
	fmt.Fprintf(stdout, "  state: %s/%s  audit: %s\n", view.CurrentPhase, view.CurrentState, auditLabel)
	if view.StateRevision > 0 {
		fmt.Fprintf(stdout, "  revision: %d  last_event: %s\n", view.StateRevision, view.LastEventID)
	}
	fmt.Fprintf(stdout, "  context_pack: %s  freshness: %s\n", view.ContextPackID, view.FreshnessState)
	if len(view.Blockers) > 0 {
		fmt.Fprintf(stdout, "  blockers (%d):\n", len(view.Blockers))
		for _, b := range view.Blockers {
			fmt.Fprintf(stdout, "    [%s] %s\n", b.Code, b.Message)
		}
	}
	if len(view.OpenObligations) > 0 {
		fmt.Fprintf(stdout, "  open_obligations (%d): %s\n", len(view.OpenObligations), strings.Join(view.OpenObligations, ", "))
	}
	if len(view.NextValidActions) > 0 {
		fmt.Fprintf(stdout, "  next_actions: %s\n", strings.Join(view.NextValidActions, ", "))
	}
	if view.CheckedFileCount > 0 {
		fmt.Fprintf(stdout, "  files_checked: %d\n", view.CheckedFileCount)
	}
	for _, sec := range view.Sections {
		fmt.Fprintf(stdout, "  %s: %s", sec.Name, sec.Status)
		if sec.FindingCount > 0 {
			fmt.Fprintf(stdout, " (%d findings)", sec.FindingCount)
		}
		fmt.Fprintln(stdout)
	}

	return exitCode
}

func runProductResearch(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	fs := flag.NewFlagSet("idiom product research", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var projectRoot string
	var effortRoot string
	var researchOutputDir string
	var researchProfile string
	var researchPromptDir string
	var mergeEvidence bool
	var planRuns bool
	var renderPrompts bool
	var jsonOutput bool
	fs.StringVar(&projectRoot, "project-root", ".", "path to the project root")
	fs.StringVar(&effortRoot, "effort-root", "", "path to the effort folder root for evidence merge")
	fs.StringVar(&researchOutputDir, "research-output-dir", "research/outputs", "effort-relative directory containing researcher output JSON files")
	fs.StringVar(&researchPromptDir, "research-prompt-dir", "research/prompts", "effort-relative directory where rendered researcher prompt files are written")
	fs.StringVar(&researchProfile, "profile", "", "research profile_id or name to use for merge policy")
	fs.BoolVar(&mergeEvidence, "merge-evidence", false, "merge researcher output cards into the effort evidence ledger")
	fs.BoolVar(&planRuns, "plan-runs", false, "emit deterministic researcher run specs derived from the selected profile")
	fs.BoolVar(&renderPrompts, "render-prompts", false, "write deterministic no-tools artifact prompt files and command recipes for planned research runs")
	fs.BoolVar(&jsonOutput, "json", false, "emit structured JSON output")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	modeCount := 0
	if mergeEvidence {
		modeCount++
	}
	if planRuns {
		modeCount++
	}
	if renderPrompts {
		modeCount++
	}
	if modeCount > 1 {
		fmt.Fprintln(stderr, "product research: --merge-evidence, --plan-runs, and --render-prompts are mutually exclusive")
		return 2
	}
	result, err := productworkflow.DiscoverProfiles(projectRoot)
	if err != nil {
		fmt.Fprintf(stderr, "product research: %v\n", err)
		return 2
	}
	if mergeEvidence {
		return runProductResearchMergeEvidence(result, effortRoot, researchOutputDir, researchProfile, jsonOutput, stdout, stderr, rec)
	}
	if planRuns {
		return runProductResearchPlanRuns(result, researchOutputDir, researchProfile, jsonOutput, stdout, stderr, rec)
	}
	if renderPrompts {
		return runProductResearchRenderPrompts(result, effortRoot, researchOutputDir, researchPromptDir, researchProfile, jsonOutput, stdout, stderr, rec)
	}
	if jsonOutput {
		type jsonOutput struct {
			Profiles     []productworkflow.ResearchProfile `json:"profiles"`
			Findings     []productworkflow.ProfileFinding  `json:"findings"`
			ProfileCount int                               `json:"profile_count"`
			FindingCount int                               `json:"finding_count"`
		}
		out := jsonOutput{
			Profiles:     result.Profiles,
			Findings:     result.Findings,
			ProfileCount: len(result.Profiles),
			FindingCount: len(result.Findings),
		}
		if result.Profiles == nil {
			out.Profiles = []productworkflow.ResearchProfile{}
		}
		if result.Findings == nil {
			out.Findings = []productworkflow.ProfileFinding{}
		}
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(out); err != nil {
			fmt.Fprintf(stderr, "product research: encode JSON: %v\n", err)
			return 1
		}
	} else {
		fmt.Fprintf(stdout, "product research: %d profiles found\n", len(result.Profiles))
		for _, p := range result.Profiles {
			fmt.Fprintf(stdout, "  %s (%s)\n", p.Name, p.ProfileID)
		}
		if len(result.Findings) > 0 {
			fmt.Fprintf(stdout, "  %d findings:\n", len(result.Findings))
			for _, f := range result.Findings {
				fmt.Fprintf(stdout, "    [%s] %s: %s\n", f.Severity, f.File, f.Message)
			}
		}
	}
	for _, f := range result.Findings {
		if f.Severity == "error" {
			return 1
		}
	}
	return 0
}

func runProductPRD(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	fs := flag.NewFlagSet("idiom product prd", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var effortRoot string
	var renderPrompt bool
	var prdPromptPath string
	var prdOutputPath string
	var jsonOutput bool
	fs.StringVar(&effortRoot, "effort-root", "", "path to the effort folder root")
	fs.BoolVar(&renderPrompt, "render-prompt", false, "write deterministic PRD draft prompt file from evidence ledger")
	fs.StringVar(&prdPromptPath, "prd-prompt", "prd.prompt.md", "effort-relative prompt file path to write")
	fs.StringVar(&prdOutputPath, "prd-output", "prd.md", "effort-relative PRD output path used in command recipe")
	fs.BoolVar(&jsonOutput, "json", false, "emit structured JSON output")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if !renderPrompt {
		fmt.Fprintln(stderr, "product prd: --render-prompt is required")
		return 2
	}
	if strings.TrimSpace(effortRoot) == "" {
		fmt.Fprintln(stderr, "product prd: --effort-root is required")
		return 2
	}
	absRoot, err := filepath.Abs(effortRoot)
	if err != nil {
		fmt.Fprintf(stderr, "product prd: resolve effort root: %v\n", err)
		return 2
	}
	evidencePath := filepath.Join(absRoot, "evidence", "evidence.jsonl")
	evidenceData, err := os.ReadFile(evidencePath)
	var evidenceCards []productworkflow.EvidenceCard
	var findings []productworkflow.ProfileFinding
	if err != nil {
		if os.IsNotExist(err) {
			findings = append(findings, productworkflow.ProfileFinding{
				Kind:     "missing_evidence_ledger",
				Message:  "required evidence/evidence.jsonl not found",
				File:     "evidence/evidence.jsonl",
				Severity: "error",
			})
		} else {
			findings = append(findings, productworkflow.ProfileFinding{
				Kind:     "read_error",
				Message:  fmt.Sprintf("failed to read evidence/evidence.jsonl: %v", err),
				File:     "evidence/evidence.jsonl",
				Severity: "error",
			})
		}
	} else {
		evidenceCards, err = productworkflow.ParseEvidenceCardsJSONL(evidenceData)
		if err != nil {
			findings = append(findings, productworkflow.ProfileFinding{
				Kind:     "validation_error",
				Message:  fmt.Sprintf("existing evidence ledger failed validation: %v", err),
				File:     "evidence/evidence.jsonl",
				Severity: "error",
			})
		}
	}
	plan := productworkflow.PlanPRDDraftPrompt(absRoot, prdPromptPath, prdOutputPath, evidenceCards)
	plan.Findings = append(findings, plan.Findings...)
	productworkflow.SortProfileFindings(plan.Findings)
	writeBlocked := hasResearchErrorFinding(plan.Findings)
	promptWriteCount := 0
	if !writeBlocked {
		promptAbs := filepath.Join(absRoot, plan.PromptPath)
		if err := os.MkdirAll(filepath.Dir(promptAbs), 0o755); err != nil {
			fmt.Fprintf(stderr, "product prd: create prompt directory: %v\n", err)
			return 2
		}
		if err := os.WriteFile(promptAbs, []byte(productworkflow.RenderPRDDraftPromptText(plan)), 0o644); err != nil {
			fmt.Fprintf(stderr, "product prd: write prompt file: %v\n", err)
			return 2
		}
		promptWriteCount = 1
	}
	logCLIPhase(rec, "product_prd_render_prompt", map[string]any{
		"finding_count":       len(plan.Findings),
		"evidence_card_count": plan.EvidenceCardCount,
		"prompt_write_count":  promptWriteCount,
	})
	status := "pass"
	if writeBlocked {
		status = "blocked"
	}
	type prdOutput struct {
		Status           string                             `json:"status"`
		EffortRoot       string                             `json:"effort_root"`
		PromptWriteCount int                                `json:"prompt_write_count"`
		Plan             productworkflow.PRDDraftPromptPlan `json:"plan"`
	}
	out := prdOutput{
		Status:           status,
		EffortRoot:       absRoot,
		PromptWriteCount: promptWriteCount,
		Plan:             plan,
	}
	if out.Plan.EvidenceCardIDs == nil {
		out.Plan.EvidenceCardIDs = []string{}
	}
	if out.Plan.EvidenceSummaries == nil {
		out.Plan.EvidenceSummaries = []productworkflow.PRDEvidenceCardSummary{}
	}
	if out.Plan.Findings == nil {
		out.Plan.Findings = []productworkflow.ProfileFinding{}
	}
	if jsonOutput {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(out); err != nil {
			fmt.Fprintf(stderr, "product prd: encode JSON: %v\n", err)
			return 1
		}
	} else {
		fmt.Fprintf(stdout, "product prd render-prompt: %s (%d findings)\n", status, len(plan.Findings))
	}
	if writeBlocked {
		return 1
	}
	return 0
}

func runProductADR(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	fs := flag.NewFlagSet("idiom product adr", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var effortRoot string
	var renderPrompt bool
	var adrPromptPath string
	var adrOutputPath string
	var jsonOutput bool
	fs.StringVar(&effortRoot, "effort-root", "", "path to the effort folder root")
	fs.BoolVar(&renderPrompt, "render-prompt", false, "write deterministic ADR draft prompt file from evidence ledger and PRD")
	fs.StringVar(&adrPromptPath, "adr-prompt", "adrs/adr.prompt.md", "effort-relative prompt file path to write")
	fs.StringVar(&adrOutputPath, "adr-output", "adrs/ADR-draft.md", "effort-relative ADR output path used in command recipe")
	fs.BoolVar(&jsonOutput, "json", false, "emit structured JSON output")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if !renderPrompt {
		fmt.Fprintln(stderr, "product adr: --render-prompt is required")
		return 2
	}
	if strings.TrimSpace(effortRoot) == "" {
		fmt.Fprintln(stderr, "product adr: --effort-root is required")
		return 2
	}
	absRoot, err := filepath.Abs(effortRoot)
	if err != nil {
		fmt.Fprintf(stderr, "product adr: resolve effort root: %v\n", err)
		return 2
	}

	var evidenceCards []productworkflow.EvidenceCard
	var prdDoc productworkflow.PRDDocument
	var findings []productworkflow.ProfileFinding

	evidencePath := filepath.Join(absRoot, "evidence", "evidence.jsonl")
	evidenceData, err := os.ReadFile(evidencePath)
	if err != nil {
		if os.IsNotExist(err) {
			findings = append(findings, productworkflow.ProfileFinding{Kind: "missing_evidence_ledger", Message: "required evidence/evidence.jsonl not found", File: "evidence/evidence.jsonl", Severity: "error"})
		} else {
			findings = append(findings, productworkflow.ProfileFinding{Kind: "read_error", Message: fmt.Sprintf("failed to read evidence/evidence.jsonl: %v", err), File: "evidence/evidence.jsonl", Severity: "error"})
		}
	} else {
		evidenceCards, err = productworkflow.ParseEvidenceCardsJSONL(evidenceData)
		if err != nil {
			findings = append(findings, productworkflow.ProfileFinding{Kind: "validation_error", Message: fmt.Sprintf("existing evidence ledger failed validation: %v", err), File: "evidence/evidence.jsonl", Severity: "error"})
		}
	}

	prdPath := filepath.Join(absRoot, "prd.md")
	prdData, err := os.ReadFile(prdPath)
	if err != nil {
		if os.IsNotExist(err) {
			findings = append(findings, productworkflow.ProfileFinding{Kind: "missing_prd", Message: "required prd.md not found", File: "prd.md", Severity: "error"})
		} else {
			findings = append(findings, productworkflow.ProfileFinding{Kind: "read_error", Message: fmt.Sprintf("failed to read prd.md: %v", err), File: "prd.md", Severity: "error"})
		}
	} else {
		prdDoc, err = productworkflow.ParsePRDMarkdown(prdData)
		if err != nil {
			findings = append(findings, productworkflow.ProfileFinding{Kind: "validation_error", Message: fmt.Sprintf("existing prd.md failed validation: %v", err), File: "prd.md", Severity: "error"})
		}
	}

	plan := productworkflow.PlanADRDraftPrompt(absRoot, adrPromptPath, adrOutputPath, evidenceCards, prdDoc)
	plan.Findings = append(findings, plan.Findings...)
	productworkflow.SortProfileFindings(plan.Findings)
	writeBlocked := hasResearchErrorFinding(plan.Findings)
	promptWriteCount := 0
	if !writeBlocked {
		promptAbs := filepath.Join(absRoot, plan.PromptPath)
		if err := os.MkdirAll(filepath.Dir(promptAbs), 0o755); err != nil {
			fmt.Fprintf(stderr, "product adr: create prompt directory: %v\n", err)
			return 2
		}
		if err := os.WriteFile(promptAbs, []byte(productworkflow.RenderADRDraftPromptText(plan)), 0o644); err != nil {
			fmt.Fprintf(stderr, "product adr: write prompt file: %v\n", err)
			return 2
		}
		promptWriteCount = 1
	}
	logCLIPhase(rec, "product_adr_render_prompt", map[string]any{
		"finding_count":         len(plan.Findings),
		"evidence_card_count":   plan.EvidenceCardCount,
		"prd_requirement_count": plan.PRDRequirementCount,
		"prompt_write_count":    promptWriteCount,
	})
	status := "pass"
	if writeBlocked {
		status = "blocked"
	}
	type adrOutput struct {
		Status           string                             `json:"status"`
		EffortRoot       string                             `json:"effort_root"`
		PromptWriteCount int                                `json:"prompt_write_count"`
		Plan             productworkflow.ADRDraftPromptPlan `json:"plan"`
	}
	out := adrOutput{
		Status:           status,
		EffortRoot:       absRoot,
		PromptWriteCount: promptWriteCount,
		Plan:             plan,
	}
	if out.Plan.EvidenceCardIDs == nil {
		out.Plan.EvidenceCardIDs = []string{}
	}
	if out.Plan.PRDRequirementIDs == nil {
		out.Plan.PRDRequirementIDs = []string{}
	}
	if out.Plan.EvidenceSummaries == nil {
		out.Plan.EvidenceSummaries = []productworkflow.ADREvidenceCardSummary{}
	}
	if out.Plan.PRDRequirementSummary == nil {
		out.Plan.PRDRequirementSummary = []productworkflow.ADRPRDRequirementSummary{}
	}
	if out.Plan.Findings == nil {
		out.Plan.Findings = []productworkflow.ProfileFinding{}
	}
	if jsonOutput {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(out); err != nil {
			fmt.Fprintf(stderr, "product adr: encode JSON: %v\n", err)
			return 1
		}
	} else {
		fmt.Fprintf(stdout, "product adr render-prompt: %s (%d findings)\n", status, len(plan.Findings))
	}
	if writeBlocked {
		return 1
	}
	return 0
}

func runProductTDD(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	fs := flag.NewFlagSet("idiom product tdd", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var effortRoot string
	var planObligations bool
	var evaluateCoverage bool
	var jsonOutput bool
	fs.StringVar(&effortRoot, "effort-root", "", "path to the effort folder root")
	fs.BoolVar(&planObligations, "plan-obligations", false, "plan deterministic TDD obligations from accepted PRD and ADR artifacts")
	fs.BoolVar(&evaluateCoverage, "evaluate-coverage", false, "evaluate deterministic coverage gate from accepted PRD/ADR/TDD/traceability artifacts")
	fs.BoolVar(&jsonOutput, "json", false, "emit structured JSON output")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if planObligations == evaluateCoverage {
		fmt.Fprintln(stderr, "product tdd: exactly one of --plan-obligations or --evaluate-coverage is required")
		return 2
	}
	if strings.TrimSpace(effortRoot) == "" {
		fmt.Fprintln(stderr, "product tdd: --effort-root is required")
		return 2
	}
	absRoot, err := filepath.Abs(effortRoot)
	if err != nil {
		fmt.Fprintf(stderr, "product tdd: resolve effort root: %v\n", err)
		return 2
	}
	var findings []productworkflow.TDDObligationPlanFinding
	addFinding := func(kind, message, file string) {
		findings = append(findings, productworkflow.TDDObligationPlanFinding{
			Kind:     kind,
			Message:  message,
			File:     file,
			Severity: "error",
		})
	}
	readFile := func(relPath string) ([]byte, bool) {
		data, readErr := os.ReadFile(filepath.Join(absRoot, relPath))
		if readErr != nil {
			if os.IsNotExist(readErr) {
				addFinding("missing_file", "required file not found", relPath)
			} else {
				addFinding("read_error", readErr.Error(), relPath)
			}
			return nil, false
		}
		return data, true
	}
	var prdDoc productworkflow.PRDDocument
	if data, ok := readFile("prd.md"); ok {
		doc, parseErr := productworkflow.ParsePRDMarkdown(data)
		if parseErr != nil {
			addFinding("parse_error", parseErr.Error(), "prd.md")
		} else {
			prdDoc = doc
		}
	}
	var adrIndex productworkflow.ADRIndex
	adrPathByID := map[string]string{}
	if data, ok := readFile(filepath.Join("adrs", "index.json")); ok {
		idx, parseErr := productworkflow.ParseADRIndexJSON(data)
		if parseErr != nil {
			addFinding("parse_error", parseErr.Error(), filepath.Join("adrs", "index.json"))
		} else {
			adrIndex = idx
			for _, entry := range idx.ADRs {
				adrPathByID[entry.ADRID] = filepath.Join("adrs", entry.Path)
			}
		}
	}
	parsedADRs := make([]productworkflow.ParsedADR, 0, len(adrIndex.ADRs))
	for _, entry := range adrIndex.ADRs {
		relPath := filepath.Join("adrs", entry.Path)
		data, ok := readFile(relPath)
		if !ok {
			continue
		}
		doc, parseErr := productworkflow.ParseADRMarkdown(data)
		if parseErr != nil {
			addFinding("parse_error", parseErr.Error(), relPath)
			continue
		}
		parsedADRs = append(parsedADRs, productworkflow.ParsedADR{Doc: doc, RelPath: relPath})
	}
	var ledger productworkflow.TDDLedger
	if data, ok := readFile("tdd.json"); ok {
		parsed, parseErr := productworkflow.ParseTDDLedgerJSON(data)
		if parseErr != nil {
			addFinding("parse_error", parseErr.Error(), "tdd.json")
		} else {
			ledger = parsed
		}
	}

	if evaluateCoverage {
		var traceability productworkflow.TraceabilityMatrix
		if data, ok := readFile(filepath.Join("evidence", "traceability_matrix.json")); ok {
			parsed, parseErr := productworkflow.ParseTraceabilityMatrixJSON(data)
			if parseErr != nil {
				addFinding("parse_error", parseErr.Error(), filepath.Join("evidence", "traceability_matrix.json"))
			} else {
				traceability = parsed
			}
		}
		var implementationRecords []productworkflow.ImplementationLedgerRecord
		if data, ok := readFile("implementation_ledger.jsonl"); ok {
			parsed, parseErr := productworkflow.ParseImplementationLedgerJSONL(data)
			if parseErr != nil {
				addFinding("parse_error", parseErr.Error(), "implementation_ledger.jsonl")
			} else {
				implementationRecords = parsed
			}
		}

		eval := productworkflow.CoverageGateEvaluation{
			EffortID:     ledger.EffortID,
			TaskID:       ledger.TaskID,
			CoverageTier: ledger.CoverageTier,
			Status:       "blocked",
			Criteria:     []productworkflow.CoverageGateCriterion{},
			Findings:     []productworkflow.CoverageGateFinding{},
			Blockers:     []productworkflow.CoverageGateBlocker{},
		}
		if len(findings) == 0 {
			knownTDDIDs := productworkflow.BuildKnownTDDObligationIDs(ledger, implementationRecords, prdDoc.FrontMatter.TaskID)
			eval = productworkflow.EvaluateCoverageGate(prdDoc, adrIndex, parsedADRs, ledger, traceability, knownTDDIDs)
		} else {
			for _, f := range findings {
				msg := f.Message
				if strings.TrimSpace(msg) == "" {
					msg = f.Kind
				}
				eval.Findings = append(eval.Findings, productworkflow.CoverageGateFinding{
					Kind:      f.Kind,
					Criterion: "input",
					Message:   msg,
					File:      f.File,
					Severity:  f.Severity,
				})
				eval.Blockers = append(eval.Blockers, productworkflow.CoverageGateBlocker{
					Kind:      f.Kind,
					Criterion: "input",
					Message:   msg,
					File:      f.File,
				})
			}
		}
		logCLIPhase(rec, "product_tdd_coverage_gate_evaluated", map[string]any{
			"status":         eval.Status,
			"finding_count":  len(eval.Findings),
			"blocker_count":  len(eval.Blockers),
			"criteria_count": len(eval.Criteria),
		})
		type coverageOutput struct {
			Status     string                                 `json:"status"`
			EffortRoot string                                 `json:"effort_root"`
			Evaluation productworkflow.CoverageGateEvaluation `json:"evaluation"`
		}
		out := coverageOutput{
			Status:     eval.Status,
			EffortRoot: absRoot,
			Evaluation: eval,
		}
		if out.Evaluation.Criteria == nil {
			out.Evaluation.Criteria = []productworkflow.CoverageGateCriterion{}
		}
		if out.Evaluation.Findings == nil {
			out.Evaluation.Findings = []productworkflow.CoverageGateFinding{}
		}
		if out.Evaluation.Blockers == nil {
			out.Evaluation.Blockers = []productworkflow.CoverageGateBlocker{}
		}
		if jsonOutput {
			enc := json.NewEncoder(stdout)
			enc.SetIndent("", "  ")
			if err := enc.Encode(out); err != nil {
				fmt.Fprintf(stderr, "product tdd: encode JSON: %v\n", err)
				return 1
			}
		} else {
			fmt.Fprintf(stdout, "product tdd evaluate-coverage: %s (%d criteria, %d blockers)\n", out.Status, len(out.Evaluation.Criteria), len(out.Evaluation.Blockers))
		}
		if out.Status != "pass" {
			return 1
		}
		return 0
	}

	plan := productworkflow.TDDObligationSynthesisPlan{
		Obligations: []productworkflow.TDDObligationPlanItem{},
		Findings:    []productworkflow.TDDObligationPlanFinding{},
	}
	status := "blocked"
	if len(findings) == 0 {
		plan = productworkflow.PlanTDDObligations(prdDoc, parsedADRs, ledger, adrPathByID)
		if !productworkflow.TDDObligationPlanHasBlockers(plan) {
			status = "pass"
		}
	} else {
		plan.Findings = append(plan.Findings, findings...)
	}
	logCLIPhase(rec, "product_tdd_plan_obligations", map[string]any{
		"status":           status,
		"finding_count":    len(plan.Findings),
		"obligation_count": len(plan.Obligations),
	})
	type tddOutput struct {
		Status     string                                     `json:"status"`
		EffortRoot string                                     `json:"effort_root"`
		Plan       productworkflow.TDDObligationSynthesisPlan `json:"plan"`
	}
	out := tddOutput{Status: status, EffortRoot: absRoot, Plan: plan}
	if out.Plan.Obligations == nil {
		out.Plan.Obligations = []productworkflow.TDDObligationPlanItem{}
	}
	if out.Plan.Findings == nil {
		out.Plan.Findings = []productworkflow.TDDObligationPlanFinding{}
	}
	if jsonOutput {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(out); err != nil {
			fmt.Fprintf(stderr, "product tdd: encode JSON: %v\n", err)
			return 1
		}
	} else {
		fmt.Fprintf(stdout, "product tdd plan-obligations: %s (%d obligations, %d findings)\n", out.Status, len(out.Plan.Obligations), len(out.Plan.Findings))
	}
	if out.Status != "pass" {
		return 1
	}
	return 0
}

type productBriefOutput struct {
	Status          string                                `json:"status"`
	EffortRoot      string                                `json:"effort_root"`
	BriefOutputPath string                                `json:"brief_output_path,omitempty"`
	Generated       productworkflow.BriefGenerationResult `json:"generated"`
	Lint            *briefLintReport                      `json:"lint,omitempty"`
	Blocker         string                                `json:"blocker,omitempty"`
	NextValidAction string                                `json:"next_valid_action,omitempty"`
}

func runProductBrief(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	fs := flag.NewFlagSet("idiom product brief", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var effortRoot string
	var render bool
	var briefOutput string
	var jsonOutput bool
	fs.StringVar(&effortRoot, "effort-root", "", "path to the effort folder root")
	fs.BoolVar(&render, "render", false, "render deterministic implementation brief from accepted workflow artifacts")
	fs.StringVar(&briefOutput, "brief-output", "", "effort-relative brief output path to write")
	fs.BoolVar(&jsonOutput, "json", false, "emit structured JSON output")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if !render {
		fmt.Fprintln(stderr, "product brief: --render is required")
		return 2
	}
	if strings.TrimSpace(effortRoot) == "" {
		fmt.Fprintln(stderr, "product brief: --effort-root is required")
		return 2
	}
	absRoot, err := filepath.Abs(effortRoot)
	if err != nil {
		fmt.Fprintf(stderr, "product brief: resolve effort root: %v\n", err)
		return 2
	}

	input, blockers := loadBriefGenerationInput(absRoot)
	gen := productworkflow.GenerateImplementationBrief(input)
	allBlockers := append([]string{}, blockers...)
	allBlockers = append(allBlockers, gen.Blockers...)

	out := productBriefOutput{
		Status:     "blocked",
		EffortRoot: absRoot,
		Generated:  gen,
	}

	var cleanup func()
	var briefPathForLint string
	tmp, tmpErr := os.CreateTemp("", "idiom-product-brief-*.md")
	if tmpErr != nil {
		allBlockers = append(allBlockers, fmt.Sprintf("failed to create temp brief for lint: %v", tmpErr))
	} else {
		briefPathForLint = tmp.Name()
		_ = tmp.Close()
		cleanup = func() { _ = os.Remove(briefPathForLint) }
	}
	if strings.TrimSpace(briefOutput) != "" {
		_, rel, block := resolveProductBriefOutputPath(absRoot, briefOutput)
		if block != "" {
			allBlockers = append(allBlockers, block)
		} else {
			out.BriefOutputPath = filepath.ToSlash(rel)
		}
	}
	if cleanup != nil {
		defer cleanup()
	}
	if briefPathForLint != "" {
		if err := os.WriteFile(briefPathForLint, []byte(gen.RenderedBrief), 0o644); err != nil {
			allBlockers = append(allBlockers, fmt.Sprintf("failed to stage brief for lint: %v", err))
		} else {
			var lintStdout bytes.Buffer
			var lintStderr bytes.Buffer
			lintCode := runBriefLint([]string{briefPathForLint, "--json"}, &lintStdout, &lintStderr, rec)
			var lintReport briefLintReport
			if unmarshalErr := json.Unmarshal(lintStdout.Bytes(), &lintReport); unmarshalErr != nil {
				allBlockers = append(allBlockers, fmt.Sprintf("brief lint output parse failed: %v", unmarshalErr))
			} else {
				out.Lint = &lintReport
				if lintCode != 0 {
					allBlockers = append(allBlockers, fmt.Sprintf("brief lint failed (exit %d)", lintCode))
				}
			}
		}
	}

	if len(allBlockers) == 0 && strings.TrimSpace(briefOutput) != "" {
		absOut := filepath.Join(absRoot, filepath.FromSlash(out.BriefOutputPath))
		if err := os.MkdirAll(filepath.Dir(absOut), 0o755); err != nil {
			allBlockers = append(allBlockers, fmt.Sprintf("create brief output directory: %v", err))
		} else if err := os.WriteFile(absOut, []byte(gen.RenderedBrief), 0o644); err != nil {
			allBlockers = append(allBlockers, fmt.Sprintf("write brief output: %v", err))
		}
	}

	if len(allBlockers) == 0 {
		out.Status = "pass"
		gen.Status = "pass"
	} else {
		out.Blocker = strings.Join(allBlockers, "; ")
		gen.Status = "blocked"
		gen.Blockers = append([]string{}, allBlockers...)
	}
	out.NextValidAction = productBriefNextValidAction(out.Status, out.BriefOutputPath)
	out.Generated = gen

	logCLIPhase(rec, "product_brief_render", map[string]any{
		"status":        out.Status,
		"has_output":    out.BriefOutputPath != "",
		"blocker_count": len(allBlockers),
	})
	if jsonOutput {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(out); err != nil {
			fmt.Fprintf(stderr, "product brief: encode JSON: %v\n", err)
			return 1
		}
	} else {
		fmt.Fprintf(stdout, "product brief render: %s\n", out.Status)
		if out.BriefOutputPath != "" {
			fmt.Fprintf(stdout, "brief_output: %s\n", out.BriefOutputPath)
		}
		if out.Status != "pass" {
			fmt.Fprintf(stdout, "blocker: %s\nnext: %s\n", out.Blocker, out.NextValidAction)
		} else if strings.TrimSpace(briefOutput) == "" {
			fmt.Fprintln(stdout, out.Generated.RenderedBrief)
		}
	}
	if out.Status != "pass" {
		return 1
	}
	return 0
}

func resolveProductBriefOutputPath(effortRoot, relPath string) (string, string, string) {
	clean, ok := cleanProductRelativePath(relPath)
	if !ok {
		return "", "", fmt.Sprintf("brief output path %q must be effort-relative and must not contain path traversal", relPath)
	}
	if !strings.HasPrefix(filepath.ToSlash(clean), "briefs/") {
		return "", "", fmt.Sprintf("brief output path %q must be under briefs/", relPath)
	}
	absPath := filepath.Join(effortRoot, clean)
	if finding, outside := productworkflow.ValidateResearchPromptPathBoundary(effortRoot, absPath, clean); outside {
		return "", "", finding.Message
	}
	if info, err := os.Lstat(absPath); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", "", fmt.Sprintf("brief output path %q must not be a symlink", clean)
	}
	return absPath, clean, ""
}

func productBriefNextValidAction(status, briefOutputPath string) string {
	if status != "pass" {
		return "resolve blockers before running `idiom brief lint <brief.md>` or dispatching this brief"
	}
	if strings.TrimSpace(briefOutputPath) == "" {
		return "rerun with `--brief-output briefs/<task>.md` to persist the lint-passing brief"
	}
	return "run `idiom brief lint <brief.md>` or dispatch from this brief output"
}

func cleanProductRelativePath(relPath string) (string, bool) {
	if strings.TrimSpace(relPath) == "" || filepath.IsAbs(relPath) {
		return "", false
	}
	clean := filepath.Clean(relPath)
	slash := filepath.ToSlash(clean)
	if slash == "." || slash == ".." || strings.HasPrefix(slash, "../") {
		return "", false
	}
	return clean, true
}

func loadBriefGenerationInput(effortRoot string) (productworkflow.BriefGenerationInput, []string) {
	in := productworkflow.BriefGenerationInput{EffortRoot: effortRoot, ExistingWorkspacePath: map[string]bool{}}
	blockers := []string{}
	workspaceRoot := resolveProductBriefWorkspaceRoot(effortRoot)
	if rel, err := filepath.Rel(workspaceRoot, effortRoot); err == nil && rel != "." && !strings.HasPrefix(filepath.ToSlash(rel), "../") {
		in.ArtifactRootPath = filepath.ToSlash(rel)
	} else {
		in.ArtifactRootPath = filepath.ToSlash(effortRoot)
	}
	readAndParse := func(relPath string, parse func([]byte) error) {
		data, err := os.ReadFile(filepath.Join(effortRoot, relPath))
		if err != nil {
			if os.IsNotExist(err) {
				blockers = append(blockers, fmt.Sprintf("required file missing: %s", relPath))
			} else {
				blockers = append(blockers, fmt.Sprintf("read %s: %v", relPath, err))
			}
			return
		}
		if err := parse(data); err != nil {
			blockers = append(blockers, fmt.Sprintf("parse %s: %v", relPath, err))
		}
	}
	readAndParse("workflow_state.json", func(data []byte) error {
		parsed, err := productworkflow.ParseWorkflowStateJSON(data)
		if err == nil {
			in.WorkflowState = parsed
		}
		return err
	})
	readAndParse("prd.md", func(data []byte) error {
		parsed, err := productworkflow.ParsePRDMarkdown(data)
		if err == nil {
			in.PRD = parsed
		}
		return err
	})
	readAndParse(filepath.Join("adrs", "index.json"), func(data []byte) error {
		parsed, err := productworkflow.ParseADRIndexJSON(data)
		if err == nil {
			in.ADRIndex = parsed
		}
		return err
	})
	for _, entry := range in.ADRIndex.ADRs {
		rel := filepath.Join("adrs", entry.Path)
		readAndParse(rel, func(data []byte) error {
			parsed, err := productworkflow.ParseADRMarkdown(data)
			if err == nil {
				in.ADRs = append(in.ADRs, productworkflow.ParsedADR{Doc: parsed, RelPath: rel})
			}
			return err
		})
	}
	readAndParse("tdd.json", func(data []byte) error {
		parsed, err := productworkflow.ParseTDDLedgerJSON(data)
		if err == nil {
			in.TDD = parsed
		}
		return err
	})
	readAndParse(filepath.Join("evidence", "traceability_matrix.json"), func(data []byte) error {
		parsed, err := productworkflow.ParseTraceabilityMatrixJSON(data)
		if err == nil {
			in.Traceability = parsed
		}
		return err
	})
	readAndParse("implementation_ledger.jsonl", func(data []byte) error {
		parsed, err := productworkflow.ParseImplementationLedgerJSONL(data)
		if err == nil {
			in.ImplementationRecords = parsed
		}
		return err
	})
	for _, rec := range in.ImplementationRecords {
		if rec.RecordType != "atom_planned" || rec.Scope == nil {
			continue
		}
		for _, path := range append(append([]string{}, rec.Scope.ExpectedFiles...), rec.Scope.AllowedFiles...) {
			clean := filepath.ToSlash(strings.TrimSpace(path))
			if clean == "" {
				continue
			}
			if _, statErr := os.Stat(filepath.Join(workspaceRoot, filepath.FromSlash(clean))); statErr == nil {
				in.ExistingWorkspacePath[clean] = true
			}
		}
	}
	if len(blockers) == 0 {
		knownTDDIDs := productworkflow.BuildKnownTDDObligationIDs(in.TDD, in.ImplementationRecords, in.PRD.FrontMatter.TaskID)
		in.CoverageEvaluation = productworkflow.EvaluateCoverageGate(in.PRD, in.ADRIndex, in.ADRs, in.TDD, in.Traceability, knownTDDIDs)
		if productworkflow.CoverageGateHasBlockers(in.CoverageEvaluation) {
			blockers = append(blockers, "coverage gate evaluation is blocked")
		}
	}
	return in, blockers
}

func resolveProductBriefWorkspaceRoot(effortRoot string) string {
	dir := filepath.Clean(effortRoot)
	for {
		next := filepath.Dir(dir)
		if next == dir {
			return filepath.Clean(effortRoot)
		}
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		dir = next
	}
}

func runProductResearchPlanRuns(profiles *productworkflow.ProfileStoreResult, researchOutputDir, researchProfile string, jsonOutput bool, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	plan := productworkflow.PlanResearchRuns(profiles.Profiles, researchProfile, researchOutputDir)
	plan.Findings = append(append([]productworkflow.ProfileFinding{}, profiles.Findings...), plan.Findings...)
	logCLIPhase(rec, "product_research_plan_runs", map[string]any{
		"profile_count": len(profiles.Profiles),
		"run_count":     plan.RunCount,
		"finding_count": len(plan.Findings),
	})
	if jsonOutput {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(plan); err != nil {
			fmt.Fprintf(stderr, "product research: encode JSON: %v\n", err)
			return 1
		}
	} else {
		fmt.Fprintf(stdout, "product research plan: %d runs (%d findings)\n", plan.RunCount, len(plan.Findings))
	}
	if hasResearchErrorFinding(plan.Findings) {
		return 1
	}
	return 0
}

func runProductResearchRenderPrompts(profiles *productworkflow.ProfileStoreResult, effortRoot, researchOutputDir, researchPromptDir, researchProfile string, jsonOutput bool, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	if strings.TrimSpace(effortRoot) == "" {
		fmt.Fprintln(stderr, "product research: --effort-root is required with --render-prompts")
		return 2
	}
	absRoot, err := filepath.Abs(effortRoot)
	if err != nil {
		fmt.Fprintf(stderr, "product research: resolve effort root: %v\n", err)
		return 2
	}

	findings := append([]productworkflow.ProfileFinding{}, profiles.Findings...)
	if len(profiles.Profiles) == 0 {
		findings = append(findings, productworkflow.ProfileFinding{
			Kind:     "missing_research_profile",
			Message:  "no valid research profile is available for prompt rendering",
			File:     ".idiom/research/profiles",
			Severity: "error",
		})
	}
	runPlan := productworkflow.PlanResearchRuns(profiles.Profiles, researchProfile, researchOutputDir)
	runPlan.Findings = append(findings, runPlan.Findings...)
	promptPlan := productworkflow.PlanResearchArtifactPrompts(runPlan, researchPromptDir)

	promptDir := filepath.Join(absRoot, promptPlan.ResearchPromptDir)
	if finding, outside := productworkflow.ValidateResearchPromptDirBoundary(absRoot, promptDir, promptPlan.ResearchPromptDir); outside {
		promptPlan.Findings = append(promptPlan.Findings, finding)
	}
	productworkflow.SortProfileFindings(promptPlan.Findings)

	writeBlocked := hasResearchErrorFinding(promptPlan.Findings)
	wrotePromptCount := 0
	if !writeBlocked {
		for _, run := range promptPlan.Runs {
			promptPath := filepath.Join(absRoot, run.PromptPath)
			if finding, outside := productworkflow.ValidateResearchPromptPathBoundary(absRoot, promptPath, run.PromptPath); outside {
				promptPlan.Findings = append(promptPlan.Findings, finding)
			}
		}
		productworkflow.SortProfileFindings(promptPlan.Findings)
		writeBlocked = hasResearchErrorFinding(promptPlan.Findings)
	}
	if !writeBlocked {
		if err := os.MkdirAll(promptDir, 0o755); err != nil {
			fmt.Fprintf(stderr, "product research: create prompt directory: %v\n", err)
			return 2
		}
		for _, run := range promptPlan.Runs {
			promptPath := filepath.Join(absRoot, run.PromptPath)
			if err := os.MkdirAll(filepath.Dir(promptPath), 0o755); err != nil {
				fmt.Fprintf(stderr, "product research: create prompt directory: %v\n", err)
				return 2
			}
			if err := os.WriteFile(promptPath, []byte(productworkflow.RenderResearchArtifactPromptText(run)), 0o644); err != nil {
				fmt.Fprintf(stderr, "product research: write prompt file: %v\n", err)
				return 2
			}
			wrotePromptCount++
		}
	}

	logCLIPhase(rec, "product_research_render_prompts", map[string]any{
		"profile_count":      len(profiles.Profiles),
		"run_count":          promptPlan.RunCount,
		"finding_count":      len(promptPlan.Findings),
		"wrote_prompt_count": wrotePromptCount,
	})

	type renderOutput struct {
		Status           string                                     `json:"status"`
		EffortRoot       string                                     `json:"effort_root"`
		PromptWriteCount int                                        `json:"prompt_write_count"`
		Plan             productworkflow.ResearchArtifactPromptPlan `json:"plan"`
	}
	status := "pass"
	if writeBlocked {
		status = "blocked"
	}
	out := renderOutput{
		Status:           status,
		EffortRoot:       absRoot,
		PromptWriteCount: wrotePromptCount,
		Plan:             promptPlan,
	}
	if out.Plan.Runs == nil {
		out.Plan.Runs = []productworkflow.ResearchArtifactPromptRunSpec{}
	}
	if out.Plan.Findings == nil {
		out.Plan.Findings = []productworkflow.ProfileFinding{}
	}
	if jsonOutput {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(out); err != nil {
			fmt.Fprintf(stderr, "product research: encode JSON: %v\n", err)
			return 1
		}
	} else {
		fmt.Fprintf(stdout, "product research render-prompts: %s (%d prompts, %d findings)\n", status, wrotePromptCount, len(promptPlan.Findings))
	}
	if writeBlocked {
		return 1
	}
	return 0
}

func runProductResearchMergeEvidence(profiles *productworkflow.ProfileStoreResult, effortRoot, researchOutputDir, researchProfile string, jsonOutput bool, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	if strings.TrimSpace(effortRoot) == "" {
		fmt.Fprintln(stderr, "product research: --effort-root is required with --merge-evidence")
		return 2
	}
	absRoot, err := filepath.Abs(effortRoot)
	if err != nil {
		fmt.Fprintf(stderr, "product research: resolve effort root: %v\n", err)
		return 2
	}
	findings := append([]productworkflow.ProfileFinding{}, profiles.Findings...)
	if len(profiles.Profiles) == 0 {
		findings = append(findings, productworkflow.ProfileFinding{
			Kind:     "missing_research_profile",
			Message:  "no valid research profile is available for evidence merge",
			File:     ".idiom/research/profiles",
			Severity: "error",
		})
	}
	selectedProfile, profileFindings := productworkflow.SelectResearchProfile(profiles.Profiles, researchProfile)
	findings = append(findings, profileFindings...)
	var existing []productworkflow.EvidenceCard
	evidencePath := filepath.Join(absRoot, "evidence", "evidence.jsonl")
	if data, readErr := os.ReadFile(evidencePath); readErr == nil {
		parsed, parseErr := productworkflow.ParseEvidenceCardsJSONL(data)
		if parseErr != nil {
			findings = append(findings, productworkflow.ProfileFinding{
				Kind:     "validation_error",
				Message:  fmt.Sprintf("existing evidence ledger failed validation: %v", parseErr),
				File:     "evidence/evidence.jsonl",
				Severity: "error",
			})
		} else {
			existing = parsed
		}
	} else if os.IsNotExist(readErr) {
		findings = append(findings, productworkflow.ProfileFinding{
			Kind:     "missing_evidence_ledger",
			Message:  "required evidence/evidence.jsonl not found",
			File:     "evidence/evidence.jsonl",
			Severity: "error",
		})
	} else {
		findings = append(findings, productworkflow.ProfileFinding{
			Kind:     "read_error",
			Message:  fmt.Sprintf("failed to read evidence/evidence.jsonl: %v", readErr),
			File:     "evidence/evidence.jsonl",
			Severity: "error",
		})
	}
	docs, docFindings, discoverErr := productworkflow.DiscoverResearchCardDocuments(absRoot, researchOutputDir)
	if discoverErr != nil {
		fmt.Fprintf(stderr, "product research: %v\n", discoverErr)
		return 2
	}
	findings = append(findings, docFindings...)
	var policy productworkflow.MergePolicy
	if selectedProfile != nil {
		policy = selectedProfile.MergePolicy
	}
	merge := productworkflow.MergeResearchCardDocuments(existing, docs, policy)
	merge.Findings = append(findings, merge.Findings...)
	writeBlocked := hasResearchErrorFinding(merge.Findings) || len(merge.Conflicts) > 0
	wrote := false
	if !writeBlocked {
		if err := writeEvidenceCardsJSONL(evidencePath, merge.Cards); err != nil {
			fmt.Fprintf(stderr, "product research: write evidence ledger: %v\n", err)
			return 2
		}
		wrote = true
	}
	logCLIPhase(rec, "product_research_merge", map[string]any{
		"profile_count":      len(profiles.Profiles),
		"research_run_count": len(docs),
		"merged_card_count":  len(merge.Cards),
		"conflict_count":     len(merge.Conflicts),
		"finding_count":      len(merge.Findings),
		"wrote_ledger":       wrote,
	})
	type mergeOutput struct {
		Status              string                                     `json:"status"`
		EffortRoot          string                                     `json:"effort_root"`
		ResearchOutputDir   string                                     `json:"research_output_dir"`
		Profile             string                                     `json:"profile"`
		ProfileCount        int                                        `json:"profile_count"`
		ResearchRunCount    int                                        `json:"research_run_count"`
		MergedCardCount     int                                        `json:"merged_card_count"`
		MergedCardIDs       []string                                   `json:"merged_card_ids"`
		Conflicts           []productworkflow.ResearchEvidenceConflict `json:"conflicts"`
		Findings            []productworkflow.ProfileFinding           `json:"findings"`
		WroteEvidenceLedger bool                                       `json:"wrote_evidence_ledger"`
	}
	status := "pass"
	if writeBlocked {
		status = "blocked"
	}
	out := mergeOutput{
		Status:              status,
		EffortRoot:          absRoot,
		ResearchOutputDir:   researchOutputDir,
		Profile:             researchProfileOutput(selectedProfile),
		ProfileCount:        len(profiles.Profiles),
		ResearchRunCount:    len(docs),
		MergedCardCount:     len(merge.Cards),
		MergedCardIDs:       merge.MergedCardIDs,
		Conflicts:           merge.Conflicts,
		Findings:            merge.Findings,
		WroteEvidenceLedger: wrote,
	}
	if out.Conflicts == nil {
		out.Conflicts = []productworkflow.ResearchEvidenceConflict{}
	}
	if out.Findings == nil {
		out.Findings = []productworkflow.ProfileFinding{}
	}
	if out.MergedCardIDs == nil {
		out.MergedCardIDs = []string{}
	}
	if jsonOutput {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(out); err != nil {
			fmt.Fprintf(stderr, "product research: encode JSON: %v\n", err)
			return 1
		}
	} else {
		fmt.Fprintf(stdout, "product research merge: %s (%d cards, %d conflicts, %d findings)\n", status, out.MergedCardCount, len(out.Conflicts), len(out.Findings))
	}
	if writeBlocked {
		return 1
	}
	return 0
}

func researchProfileOutput(profile *productworkflow.ResearchProfile) string {
	if profile == nil {
		return ""
	}
	if strings.TrimSpace(profile.ProfileID) != "" {
		return profile.ProfileID
	}
	return profile.Name
}

func hasResearchErrorFinding(findings []productworkflow.ProfileFinding) bool {
	for _, finding := range findings {
		if finding.Severity == "error" {
			return true
		}
	}
	return false
}

func writeEvidenceCardsJSONL(path string, cards []productworkflow.EvidenceCard) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var b strings.Builder
	for _, card := range cards {
		data, err := json.Marshal(card)
		if err != nil {
			return err
		}
		b.Write(data)
		b.WriteByte('\n')
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func gitChangedPaths(effortRoot string) []string {
	repoRoot := findGitRepoRoot(effortRoot)
	if repoRoot == "" {
		return nil
	}
	rel, err := filepath.Rel(repoRoot, effortRoot)
	if err != nil {
		return nil
	}
	out, err := exec.Command("git", "-C", repoRoot, "log", "--reverse", "--format=%H", "--", rel).Output()
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) == 0 || lines[0] == "" {
		return nil
	}
	firstCommit := lines[0]
	diffOut, err := exec.Command("git", "-C", repoRoot, "diff", "--name-only", firstCommit+"^", "HEAD").Output()
	if err != nil {
		diffOut, err = exec.Command("git", "-C", repoRoot, "diff", "--name-only", firstCommit, "HEAD").Output()
		if err != nil {
			return nil
		}
	}
	var paths []string
	for _, line := range strings.Split(strings.TrimSpace(string(diffOut)), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			paths = append(paths, line)
		}
	}
	return paths
}

func gitCommitPaths(repoRoot string, hashes []string) map[string][]string {
	result := make(map[string][]string)
	for _, h := range hashes {
		out, err := exec.Command("git", "-C", repoRoot, "show", "--name-only", "--format=", h).Output()
		if err != nil {
			continue
		}
		var paths []string
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				paths = append(paths, line)
			}
		}
		result[h] = paths
	}
	return result
}

func findGitRepoRoot(start string) string {
	dir := start
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}
