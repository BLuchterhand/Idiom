package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"idiom/internal/productworkflow"
	"idiom/internal/telemetry"
)

type productLintFinding struct {
	Code    string `json:"code"`
	Path    string `json:"path"`
	Message string `json:"message"`
}

type productLintResult struct {
	Status          string               `json:"status"`
	EffortRoot      string               `json:"effort_root"`
	CheckedFiles    []string             `json:"checked_files"`
	Findings        []productLintFinding `json:"findings"`
	NextValidAction string               `json:"next_valid_action"`
}

type productStatusMigrationResult struct {
	Status             string                                          `json:"status"`
	Mode               string                                          `json:"mode"`
	EffortRoot         string                                          `json:"effort_root"`
	EffortsRoot        string                                          `json:"efforts_root,omitempty"`
	EffortID           string                                          `json:"effort_id"`
	SourceStatusBucket string                                          `json:"source_status_bucket"`
	TargetStatusBucket string                                          `json:"target_status_bucket"`
	SourceFolderPath   string                                          `json:"source_folder_path"`
	TargetFolderPath   string                                          `json:"target_folder_path"`
	PriorFolderPaths   []string                                        `json:"prior_folder_paths"`
	PriorIndexPaths    []string                                        `json:"prior_index_paths"`
	Event              productworkflow.ProductStatusMigrationEvent     `json:"event"`
	EventID            string                                          `json:"event_id"`
	StateRevision      int                                             `json:"state_revision"`
	LastEventID        string                                          `json:"last_event_id"`
	Blockers           []productworkflow.ProductStatusMigrationBlocker `json:"blockers"`
	Findings           []productworkflow.ProductStatusMigrationBlocker `json:"findings"`
	NextValidAction    string                                          `json:"next_valid_action"`
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
	case "efforts":
		return runProductEfforts(args[1:], stdout, stderr, rec)
	case "status":
		return runProductStatus(args[1:], stdout, stderr, rec)
	case "status-migration":
		return runProductStatusMigration(args[1:], stdout, stderr, rec)
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
	case "feedback":
		return runProductFeedback(args[1:], stdout, stderr, rec)
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
	fmt.Fprintln(w, "  idiom product efforts [--root docs/product-workflows/efforts] [--status unstarted|started|closed|all] [--query <text>] [--json]")
	fmt.Fprintln(w, "  idiom product status --effort-root <path> [--operator-view] [--json]")
	fmt.Fprintln(w, "  idiom product status-migration --effort-root <path> [--efforts-root <path>] --target-status <bucket> (--plan|--apply) [--target-current-state <state>] [--updated-at <timestamp>] [--event-id <id>] [--workspace-fingerprint <value>] [--expected-revision <n>] [--expected-source-status <bucket>] [--json]")
	fmt.Fprintln(w, "  idiom product research --project-root <path> [--json]")
	fmt.Fprintln(w, "  idiom product research --project-root <path> --plan-runs [--profile <profile-id-or-name>] [--research-output-dir research/outputs] [--json]")
	fmt.Fprintln(w, "  idiom product research --project-root <path> --effort-root <path> --merge-evidence [--profile <profile-id-or-name>] [--research-output-dir research/outputs] [--json]")
	fmt.Fprintln(w, "  idiom product research --project-root <path> --effort-root <path> --render-prompts [--profile <profile-id-or-name>] [--research-output-dir research/outputs] [--research-prompt-dir research/prompts] [--json]")
	fmt.Fprintln(w, "  idiom product prd --effort-root <path> --render-prompt [--prd-prompt prd.prompt.md] [--prd-output prd.md] [--json]")
	fmt.Fprintln(w, "  idiom product adr --effort-root <path> --render-prompt [--adr-prompt adrs/adr.prompt.md] [--adr-output adrs/ADR-draft.md] [--json]")
	fmt.Fprintln(w, "  idiom product tdd --effort-root <path> --plan-obligations [--json]")
	fmt.Fprintln(w, "  idiom product tdd --effort-root <path> --evaluate-coverage [--json]")
	fmt.Fprintln(w, "  idiom product brief --effort-root <path> --render [--brief-output briefs/<task>.md] [--json]")
	fmt.Fprintln(w, "  idiom product feedback --effort-root <path> --lane-history-file <path> --plan-progress-file <path> --product-evidence-id <id> --product-evidence-path <path> --release-evidence-id <id> --release-evidence-path <path> --verification-file <path> --brief-path <path> --changed-path <path> --tdd-obligation-ref <id> [--json]")
	fmt.Fprintln(w, "  idiom product feedback --effort-root <path> --reconcile-summary <path> --reconciled-state-output <path> [--json]")
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
	result.NextValidAction = productLintNextValidAction(result.Status)
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
			fmt.Fprintln(stdout, "  status: PASS")
		} else {
			fmt.Fprintf(stdout, "product lint: %s FAILED (%d findings)\n", filepath.Base(absRoot), len(result.Findings))
			fmt.Fprintln(stdout, "  status: FAIL")
			for _, f := range result.Findings {
				fmt.Fprintf(stdout, "  [%s] %s: %s\n", f.Code, f.Path, f.Message)
			}
		}
		fmt.Fprintf(stdout, "  next: %s\n", result.NextValidAction)
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
	var parsedWorkflowState *productworkflow.WorkflowState
	var parsedLedger []productworkflow.ImplementationLedgerRecord
	var parsedADRs []productworkflow.ParsedADR
	var parsedInvestigations []productworkflow.InvestigationReport

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
		ws, err := productworkflow.ParseWorkflowStateJSON(data)
		if err == nil {
			parsedWorkflowState = &ws
		}
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
				report, err := productworkflow.ParseInvestigationReportJSON(data)
				if err != nil {
					addFinding("parse_error", relPath, err.Error())
				} else {
					parsedInvestigations = append(parsedInvestigations, report)
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
		repoRoot := findGitRepoRoot(root)
		if repoRoot == "" {
			repoRoot = root
		}
		if parsedWorkflowState != nil {
			parity := productworkflow.EvaluateObsidianParity(repoRoot, parsedWorkflowState, parsedLedger, parsedInvestigations)
			for _, checked := range parity.CheckedFiles {
				result.CheckedFiles = append(result.CheckedFiles, checked)
			}
			for _, finding := range parity.Findings {
				addFinding(finding.Code, finding.Path, finding.Message)
			}
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
	Status          string                                  `json:"status"`
	EffortRoot      string                                  `json:"effort_root"`
	CheckedFiles    []string                                `json:"checked_files"`
	Sections        []productAuditSection                   `json:"sections"`
	Findings        []productLintFinding                    `json:"findings"`
	Metrics         *productworkflow.ProductWorkflowMetrics `json:"metrics,omitempty"`
	NextValidAction string                                  `json:"next_valid_action"`
}

func buildAuditResult(lintResult productLintResult) productAuditResult {
	sectionMap := map[string]*productAuditSection{
		"prd":          {Name: "prd", Status: "pass"},
		"adr":          {Name: "adr", Status: "pass"},
		"tdd":          {Name: "tdd", Status: "pass"},
		"traceability": {Name: "traceability", Status: "pass"},
		"reports":      {Name: "reports", Status: "pass"},
		"obsidian":     {Name: "obsidian", Status: "pass"},
		"scope_gate":   {Name: "scope_gate", Status: "pass"},
		"metrics":      {Name: "metrics", Status: "pass"},
	}
	for _, f := range lintResult.Findings {
		section := productworkflow.ClassifySection(f.Path)
		if section == "" {
			if strings.HasPrefix(f.Code, "scope_gate_") {
				section = "scope_gate"
			} else if strings.HasPrefix(f.Code, "obsidian_") {
				section = "obsidian"
			} else {
				continue
			}
		}
		sec := sectionMap[section]
		sec.Findings = append(sec.Findings, f)
		sec.Status = "fail"
	}
	sections := make([]productAuditSection, 0, len(sectionMap))
	for _, name := range []string{"prd", "adr", "tdd", "traceability", "reports", "obsidian", "scope_gate", "metrics"} {
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
	metrics, metricsErr := productworkflow.BuildProductWorkflowMetrics(absRoot)
	if metricsErr != nil {
		finding := productMetricsFinding(metricsErr)
		auditResult.Status = "fail"
		auditResult.Findings = append(auditResult.Findings, finding)
		markAuditSectionFailure(&auditResult, "metrics", finding)
	} else {
		auditResult.Metrics = &metrics
	}
	auditResult.NextValidAction = productAuditNextValidAction(auditResult.Status)
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
			fmt.Fprintln(stdout, "  status: PASS")
		} else {
			fmt.Fprintf(stdout, "product audit: %s FAILED (%d findings)\n", filepath.Base(absRoot), len(auditResult.Findings))
			fmt.Fprintln(stdout, "  status: FAIL")
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
		if auditResult.Metrics != nil {
			metrics := *auditResult.Metrics
			fmt.Fprintf(stdout, "  metrics: phase=%s state=%s revision=%d gates=%d blockers=%d obligations=%d next_actions=%d\n",
				metrics.CurrentPhase,
				metrics.CurrentState,
				metrics.StateRevision,
				len(metrics.GateStatusCounts),
				metrics.BlockerCount,
				metrics.OpenObligationCount,
				metrics.NextValidActionCount,
			)
		}
		fmt.Fprintf(stdout, "  next: %s\n", auditResult.NextValidAction)
	}
	if auditResult.Status != "pass" {
		return 1
	}
	return 0
}

func productMetricsFinding(err error) productLintFinding {
	path := "workflow_state.json"
	var metricsErr productworkflow.ProductWorkflowMetricsError
	if errors.As(err, &metricsErr) && strings.TrimSpace(metricsErr.Path) != "" {
		path = metricsErr.Path
	}
	return productLintFinding{
		Code:    "metrics_error",
		Path:    path,
		Message: err.Error(),
	}
}

func markAuditSectionFailure(result *productAuditResult, name string, finding productLintFinding) {
	for i := range result.Sections {
		if result.Sections[i].Name == name {
			result.Sections[i].Status = "fail"
			result.Sections[i].Findings = append(result.Sections[i].Findings, finding)
			return
		}
	}
	result.Sections = append(result.Sections, productAuditSection{
		Name:     name,
		Status:   "fail",
		Findings: []productLintFinding{finding},
	})
}

func runProductEfforts(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	fs := flag.NewFlagSet("idiom product efforts", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := productworkflow.DefaultProductEffortsRoot
	statusFilter := "all"
	var query string
	var jsonOutput bool
	fs.StringVar(&root, "root", productworkflow.DefaultProductEffortsRoot, "path to the product efforts root")
	fs.StringVar(&statusFilter, "status", "all", "filter by status bucket: unstarted, started, closed, or all")
	fs.StringVar(&query, "query", "", "substring filter over effort summaries")
	fs.BoolVar(&jsonOutput, "json", false, "emit structured JSON output")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(stderr, "product efforts: unexpected arguments: %s\n", strings.Join(fs.Args(), " "))
		return 2
	}

	result, browseErr := productworkflow.BrowseProductEfforts(productworkflow.ProductEffortsOptions{
		Root:   root,
		Status: statusFilter,
		Query:  query,
	})

	logCLIPhase(rec, "product_efforts", map[string]any{
		"status":        result.Status,
		"effort_count":  result.Counts.EffortCount,
		"finding_count": result.Counts.FindingCount,
		"status_filter": result.StatusFilter,
	})

	if jsonOutput {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(result); err != nil {
			fmt.Fprintf(stderr, "product efforts: encode JSON: %v\n", err)
			return 1
		}
	} else {
		printProductEfforts(stdout, result)
	}

	if browseErr != nil {
		return productEffortsErrorCode(result)
	}
	if result.Status != "pass" {
		return 1
	}
	return 0
}

func productEffortsErrorCode(result productworkflow.ProductEffortsResult) int {
	for _, finding := range result.Findings {
		switch finding.Code {
		case "invalid_status_filter", "unsafe_efforts_root":
			return 2
		}
	}
	return 1
}

func printProductEfforts(w io.Writer, result productworkflow.ProductEffortsResult) {
	label := "OK"
	if result.Status != "pass" {
		label = "FAILED"
	}
	rootLabel := result.Root
	if rootLabel == "" {
		rootLabel = productworkflow.DefaultProductEffortsRoot
	}
	fmt.Fprintf(w, "product efforts: %s %s\n", filepath.Base(rootLabel), label)
	if result.Query != "" {
		fmt.Fprintf(w, "  filters: status=%s query=%q\n", result.StatusFilter, result.Query)
	} else {
		fmt.Fprintf(w, "  filters: status=%s\n", result.StatusFilter)
	}
	fmt.Fprintf(w, "  counts: efforts=%d unstarted=%d started=%d closed=%d findings=%d\n",
		result.Counts.EffortCount,
		result.Counts.Unstarted,
		result.Counts.Started,
		result.Counts.Closed,
		result.Counts.FindingCount,
	)
	if len(result.Efforts) > 0 {
		fmt.Fprintf(w, "  efforts (%d):\n", len(result.Efforts))
		for _, effort := range result.Efforts {
			fmt.Fprintf(w, "    [%s] %s  %s/%s  task=%s  title=%q  revision=%d  last_event=%s  blockers=%d obligations=%d  path=%s\n",
				effort.StatusBucket,
				effort.EffortID,
				effort.CurrentPhase,
				effort.CurrentState,
				effort.TaskID,
				effort.Title,
				effort.StateRevision,
				effort.LastEventID,
				effort.BlockerCount,
				effort.OpenObligationCount,
				effort.RelativeFolderPath,
			)
			if effort.ContextPackID != "" || effort.FreshnessState != "" {
				fmt.Fprintf(w, "      context_pack=%s freshness=%s\n", effort.ContextPackID, effort.FreshnessState)
			}
			if len(effort.NextValidActions) > 0 {
				fmt.Fprintf(w, "      next_actions: %s\n", strings.Join(effort.NextValidActions, ", "))
			}
		}
	}
	if len(result.Findings) > 0 {
		fmt.Fprintf(w, "  findings (%d):\n", len(result.Findings))
		for _, finding := range result.Findings {
			fmt.Fprintf(w, "    [%s] %s: %s\n", finding.ID, finding.Path, finding.Message)
		}
	}
	fmt.Fprintf(w, "  next: %s\n", result.NextValidAction)
}

func runProductStatusMigration(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	fs := flag.NewFlagSet("idiom product status-migration", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var effortRoot string
	var effortsRoot string
	var targetStatus string
	var targetCurrentState string
	var updatedAt string
	var eventID string
	var workspaceFingerprint string
	var expectedRevision int
	var expectedSourceStatus string
	var planMode bool
	var applyMode bool
	var jsonOutput bool
	fs.StringVar(&effortRoot, "effort-root", "", "path to the selected effort folder")
	fs.StringVar(&effortsRoot, "efforts-root", "", "optional override for the efforts root")
	fs.StringVar(&targetStatus, "target-status", "", "destination status bucket: unstarted, started, or closed")
	fs.StringVar(&targetCurrentState, "target-current-state", "", "override the destination current_state")
	fs.StringVar(&updatedAt, "updated-at", "", "updated_at timestamp recorded in workflow_state and index.json")
	fs.StringVar(&eventID, "event-id", "", "event id recorded for the migration")
	fs.StringVar(&workspaceFingerprint, "workspace-fingerprint", "", "optional replacement workspace fingerprint")
	fs.IntVar(&expectedRevision, "expected-revision", -1, "expected current workflow_state revision before apply")
	fs.StringVar(&expectedSourceStatus, "expected-source-status", "", "expected current source status bucket before apply")
	fs.BoolVar(&planMode, "plan", false, "compute a read-only status migration plan")
	fs.BoolVar(&applyMode, "apply", false, "move the effort folder and update workflow_state, events, and index")
	fs.BoolVar(&jsonOutput, "json", false, "emit structured JSON output")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(stderr, "product status-migration: unexpected arguments: %s\n", strings.Join(fs.Args(), " "))
		return 2
	}
	if strings.TrimSpace(effortRoot) == "" {
		fmt.Fprintln(stderr, "product status-migration: --effort-root is required")
		return 2
	}
	if strings.TrimSpace(targetStatus) == "" {
		fmt.Fprintln(stderr, "product status-migration: --target-status is required")
		return 2
	}
	if planMode == applyMode {
		fmt.Fprintln(stderr, "product status-migration: choose exactly one of --plan or --apply")
		return 2
	}
	now := time.Now().UTC()
	if strings.TrimSpace(updatedAt) == "" {
		updatedAt = now.Format(time.RFC3339Nano)
	}
	if strings.TrimSpace(eventID) == "" {
		eventID = fmt.Sprintf("evt-status-migration-%d", now.UnixNano())
	}
	if strings.TrimSpace(workspaceFingerprint) == "" {
		workspaceFingerprint = "idiom product status-migration CLI"
	}

	opts := productworkflow.ProductStatusMigrationApplyOptions{
		EffortRoot:                 effortRoot,
		EffortsRoot:                effortsRoot,
		TargetStatusBucket:         targetStatus,
		TargetCurrentState:         targetCurrentState,
		UpdatedAt:                  updatedAt,
		MigrationEventID:           eventID,
		WorkspaceFingerprint:       workspaceFingerprint,
		ExpectedStateRevision:      expectedRevision,
		ExpectedSourceStatusBucket: expectedSourceStatus,
	}

	var plan productworkflow.ProductStatusMigrationPlan
	modeLabel := "PLAN"
	logMode := "plan"
	if planMode {
		plan = productworkflow.PlanProductStatusMigrationFromEffortRoot(opts)
	} else {
		plan = productworkflow.ApplyProductStatusMigration(opts)
		modeLabel = "APPLY"
		logMode = "apply"
	}
	logCLIPhase(rec, "product_status_migration", map[string]any{
		"status":        plan.Status,
		"mode":          logMode,
		"effort_id":     plan.EffortID,
		"blocker_count": len(plan.Blockers),
		"target_status": plan.TargetStatusBucket,
	})

	if jsonOutput {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if encErr := enc.Encode(plan); encErr != nil {
			fmt.Fprintf(stderr, "product status-migration: encode JSON: %v\n", encErr)
			return 1
		}
	} else {
		printProductStatusMigration(stdout, modeLabel, plan)
	}

	return productStatusMigrationExitCode(plan)
}

func productStatusMigrationExitCode(result productworkflow.ProductStatusMigrationPlan) int {
	if result.Status == "planned" || result.Status == "applied" {
		return 0
	}
	for _, blocker := range result.Blockers {
		switch blocker.Code {
		case "missing_effort_root", "unsafe_effort_root", "unsafe_efforts_root":
			return 2
		}
	}
	return 1
}

func printProductStatusMigration(w io.Writer, mode string, result productworkflow.ProductStatusMigrationPlan) {
	fmt.Fprintf(w, "product status-migration: %s %s\n", mode, strings.ToUpper(result.Status))
	if result.EffortID != "" {
		fmt.Fprintf(w, "  effort_id: %s\n", result.EffortID)
	}
	if result.SourceStatusBucket != "" || result.TargetStatusBucket != "" {
		fmt.Fprintf(w, "  buckets: %s -> %s\n", result.SourceStatusBucket, result.TargetStatusBucket)
	}
	if result.SourceFolderPath != "" || result.TargetFolderPath != "" {
		fmt.Fprintf(w, "  paths: %s -> %s\n", result.SourceFolderPath, result.TargetFolderPath)
	}
	if result.Event.Event != "" || result.Event.EventID != "" {
		fmt.Fprintf(w, "  event: %s id=%s\n", result.Event.Event, result.Event.EventID)
	}
	if result.Event.Details.StateRevision > 0 {
		fmt.Fprintf(w, "  state_revision: %d\n", result.Event.Details.StateRevision)
	}
	if len(result.PriorFolderPaths) > 0 {
		fmt.Fprintf(w, "  prior_folder_paths: %s\n", strings.Join(result.PriorFolderPaths, ", "))
	}
	if len(result.PriorIndexPaths) > 0 {
		fmt.Fprintf(w, "  prior_index_paths: %s\n", strings.Join(result.PriorIndexPaths, ", "))
	}
	if len(result.Blockers) > 0 {
		fmt.Fprintf(w, "  blockers (%d):\n", len(result.Blockers))
		for _, blocker := range result.Blockers {
			if blocker.Path != "" {
				fmt.Fprintf(w, "    [%s] %s: %s\n", blocker.Code, blocker.Path, blocker.Message)
			} else {
				fmt.Fprintf(w, "    [%s] %s\n", blocker.Code, blocker.Message)
			}
		}
	}
	fmt.Fprintf(w, "  next: %s\n", result.NextValidAction)
}

func runProductStatus(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	fs := flag.NewFlagSet("idiom product status", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var effortRoot string
	var jsonOutput bool
	var operatorView bool
	fs.StringVar(&effortRoot, "effort-root", "", "path to the effort folder root")
	fs.BoolVar(&jsonOutput, "json", false, "emit structured JSON output")
	fs.BoolVar(&operatorView, "operator-view", false, "emit shared operator continuation status view")
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
	if wsErr == nil {
		metrics, metricsErr := productworkflow.BuildProductWorkflowMetrics(absRoot)
		if metricsErr != nil {
			view.AuditStatus = "fail"
			view.Findings = append(view.Findings, productworkflow.StatusBlocker{
				Code:    "metrics_error",
				Message: metricsErr.Error(),
			})
			view.Blockers = append(view.Blockers, productworkflow.StatusBlocker{
				Code:    "metrics_error",
				Message: metricsErr.Error(),
			})
			markStatusSectionFailure(&view, "metrics")
		} else {
			view.Metrics = &metrics
		}
	}
	exitCode := 0
	if wsErr != nil {
		exitCode = 1
	}
	if view.AuditStatus != "pass" {
		exitCode = 1
	}
	operatorInputView := view
	view.NextValidActions = productStatusNextValidActions(view, exitCode, wsErr != nil)
	operatorStatus := productworkflow.BuildProductOperatorStatusView(operatorInputView, readProductOperatorWorkflowState(absRoot, wsErr))

	logCLIPhase(rec, "product_status", map[string]any{
		"audit_status":  view.AuditStatus,
		"effort_id":     view.EffortID,
		"current_state": view.CurrentState,
	})

	if operatorView {
		if jsonOutput {
			enc := json.NewEncoder(stdout)
			enc.SetIndent("", "  ")
			if err := enc.Encode(operatorStatus); err != nil {
				fmt.Fprintf(stderr, "product status: encode operator JSON: %v\n", err)
				return 1
			}
			return exitCode
		}
		printProductOperatorStatus(stdout, filepath.Base(absRoot), operatorStatus)
		return exitCode
	}

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
		fmt.Fprintf(stdout, "product status: ERROR %s\n", filepath.Base(absRoot))
		fmt.Fprintln(stdout, "  status: ERROR")
		for _, b := range view.Blockers {
			fmt.Fprintf(stdout, "  [%s] %s\n", b.Code, b.Message)
		}
		fmt.Fprintf(stdout, "  next: %s\n", view.NextValidActions[0])
		return exitCode
	}

	auditLabel := "OK"
	if view.AuditStatus != "pass" {
		auditLabel = "FAILED"
	}
	fmt.Fprintf(stdout, "product status: %s %s\n", filepath.Base(absRoot), view.EffortID)
	fmt.Fprintf(stdout, "  state: %s/%s  audit: %s\n", view.CurrentPhase, view.CurrentState, auditLabel)
	fmt.Fprintf(stdout, "  status: %s\n", auditLabel)
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
	if view.Metrics != nil {
		metrics := *view.Metrics
		fmt.Fprintf(stdout, "  metrics: phase=%s state=%s revision=%d gates=%d blockers=%d obligations=%d next_actions=%d\n",
			metrics.CurrentPhase,
			metrics.CurrentState,
			metrics.StateRevision,
			len(metrics.GateStatusCounts),
			metrics.BlockerCount,
			metrics.OpenObligationCount,
			metrics.NextValidActionCount,
		)
	}
	for _, sec := range view.Sections {
		fmt.Fprintf(stdout, "  %s: %s", sec.Name, sec.Status)
		if sec.FindingCount > 0 {
			fmt.Fprintf(stdout, " (%d findings)", sec.FindingCount)
		}
		fmt.Fprintln(stdout)
	}
	fmt.Fprintf(stdout, "  next: %s\n", view.NextValidActions[0])

	return exitCode
}

func readProductOperatorWorkflowState(absRoot string, statusErr error) *productworkflow.WorkflowState {
	if statusErr != nil {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(absRoot, "workflow_state.json"))
	if err != nil {
		return nil
	}
	ws, err := productworkflow.ParseWorkflowStateJSON(data)
	if err != nil {
		return nil
	}
	return &ws
}

func printProductOperatorStatus(stdout io.Writer, effortName string, view productworkflow.ProductOperatorStatusView) {
	fmt.Fprintf(stdout, "product operator status: %s %s\n", effortName, view.EffortID)
	fmt.Fprintf(stdout, "  authority: %s\n", view.ContinuationAuthority)
	fmt.Fprintf(stdout, "  state: %s/%s  audit: %s\n", view.CurrentPhase, view.CurrentState, view.AuditStatus)
	fmt.Fprintf(stdout, "  context_pack: %s  freshness: %s\n", view.ContextPackID, view.FreshnessState)
	if len(view.Blockers) > 0 {
		fmt.Fprintf(stdout, "  blockers (%d):\n", len(view.Blockers))
		for _, blocker := range view.Blockers {
			fmt.Fprintf(stdout, "    [%s] %s (%s)\n", blocker.Code, blocker.Message, blocker.Source)
		}
	}
	if len(view.OpenObligations) > 0 {
		fmt.Fprintf(stdout, "  open_obligations (%d): %s\n", len(view.OpenObligations), strings.Join(view.OpenObligations, ", "))
	}
	if len(view.NextValidActions) > 0 {
		fmt.Fprintf(stdout, "  next_valid_actions (%d):\n", len(view.NextValidActions))
		for _, action := range view.NextValidActions {
			fmt.Fprintf(stdout, "    - %s [%s] owner=%s dispatchable=%t runnable_now=%t", action.Label, action.ID, action.Owner, action.Dispatchable, action.RunnableNow)
			if len(action.BlockedBy) > 0 {
				fmt.Fprintf(stdout, " blocked_by=%s", strings.Join(action.BlockedBy, ","))
			}
			if action.CommandHint != "" {
				fmt.Fprintf(stdout, " command_hint=%s", action.CommandHint)
			}
			if action.Message != "" {
				fmt.Fprintf(stdout, "  %s", action.Message)
			}
			fmt.Fprintln(stdout)
		}
	}
	if view.CheckedFileCount > 0 {
		fmt.Fprintf(stdout, "  files_checked: %d\n", view.CheckedFileCount)
	}
	if view.Metrics != nil {
		metrics := *view.Metrics
		fmt.Fprintf(stdout, "  metrics: phase=%s state=%s revision=%d gates=%d blockers=%d obligations=%d next_actions=%d\n",
			metrics.CurrentPhase,
			metrics.CurrentState,
			metrics.StateRevision,
			len(metrics.GateStatusCounts),
			metrics.BlockerCount,
			metrics.OpenObligationCount,
			metrics.NextValidActionCount,
		)
	}
	for _, sec := range view.Sections {
		fmt.Fprintf(stdout, "  %s: %s", sec.Name, sec.Status)
		if sec.FindingCount > 0 {
			fmt.Fprintf(stdout, " (%d findings)", sec.FindingCount)
		}
		fmt.Fprintln(stdout)
	}
	if len(view.ArtifactPaths) > 0 {
		fmt.Fprintf(stdout, "  artifacts (%d):\n", len(view.ArtifactPaths))
		for _, artifact := range view.ArtifactPaths {
			fmt.Fprintf(stdout, "    %s: %s\n", artifact.Name, artifact.Path)
		}
	}
}

func productLintNextValidAction(status string) string {
	if status == "pass" {
		return "run `idiom product audit --effort-root <path>` for section-level checks"
	}
	return "fix findings, then rerun `idiom product lint --effort-root <path>`"
}

func productAuditNextValidAction(status string) string {
	if status == "pass" {
		return "run `idiom product status --effort-root <path>` to inspect workflow state"
	}
	return "fix findings, then rerun `idiom product audit --effort-root <path>`"
}

func productStatusNextValidActions(view productworkflow.ProductStatusView, exitCode int, workflowStateErr bool) []string {
	if workflowStateErr {
		if len(view.NextValidActions) > 0 {
			return view.NextValidActions
		}
		return []string{"restore a valid workflow_state.json, then rerun `idiom product status --effort-root <path>`"}
	}
	if exitCode != 0 {
		action := "fix product workflow findings, then rerun `idiom product status --effort-root <path>`"
		if len(view.NextValidActions) == 0 {
			return []string{action}
		}
		if view.NextValidActions[0] == action {
			return view.NextValidActions
		}
		next := make([]string, 0, len(view.NextValidActions)+1)
		next = append(next, action)
		next = append(next, view.NextValidActions...)
		return next
	}
	if len(view.NextValidActions) > 0 {
		return view.NextValidActions
	}
	return []string{"run `idiom product audit --effort-root <path>` for detailed section findings"}
}

func markStatusSectionFailure(view *productworkflow.ProductStatusView, name string) {
	for i := range view.Sections {
		if view.Sections[i].Name == name {
			view.Sections[i].Status = "fail"
			view.Sections[i].FindingCount++
			return
		}
	}
	view.Sections = append(view.Sections, productworkflow.StatusSection{
		Name:         name,
		Status:       "fail",
		FindingCount: 1,
	})
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

type productFeedbackOutput struct {
	Status          string                                   `json:"status"`
	EffortRoot      string                                   `json:"effort_root"`
	Summary         productworkflow.ExecutionFeedbackSummary `json:"summary"`
	NextValidAction string                                   `json:"next_valid_action"`
}

type productFeedbackReconcileOutput struct {
	Status                string                                                `json:"status"`
	EffortRoot            string                                                `json:"effort_root"`
	SummaryReferencePath  string                                                `json:"summary_reference_path"`
	ReconciledStateOutput string                                                `json:"reconciled_state_output"`
	Reconciliation        productworkflow.ExecutionFeedbackReconciliationResult `json:"reconciliation"`
	NextValidAction       string                                                `json:"next_valid_action"`
}

type productFeedbackRankerLearningOutput struct {
	Status                 string                                       `json:"status"`
	EffortRoot             string                                       `json:"effort_root"`
	SummaryReferencePath   string                                       `json:"summary_reference_path"`
	RankerLearningEvidence productworkflow.RankerLearningEvidenceResult `json:"ranker_learning_evidence"`
	NextValidAction        string                                       `json:"next_valid_action"`
}

var productFeedbackPrivacyMarkers = []string{
	"raw prompt",
	"raw prompts",
	"raw transcript",
	"raw transcripts",
	"file body",
	"file bodies",
	"secret",
	"secrets",
	"secret=",
	"secret:",
	"secrets=",
	"secrets:",
	"customer-identifying",
	"customer identifying",
	"customer data",
}

func runProductFeedback(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	fs := flag.NewFlagSet("idiom product feedback", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var effortRoot string
	var laneHistoryFile string
	var planProgressFile string
	var verificationFile string
	var productEvidenceID string
	var productEvidencePath string
	var releaseEvidenceID string
	var releaseEvidencePath string
	var briefPath string
	var reconcileSummary string
	var reconciledStateOutput string
	var rankerLearningEvidenceSummary string
	var jsonOutput bool
	var changedPaths multiValueFlag
	var tddRefs multiValueFlag
	fs.StringVar(&effortRoot, "effort-root", "", "path to the effort folder root")
	fs.StringVar(&laneHistoryFile, "lane-history-file", "", "effort-relative JSON file with lane history references")
	fs.StringVar(&planProgressFile, "plan-progress-file", "", "effort-relative JSON file with plan progress reference")
	fs.StringVar(&verificationFile, "verification-file", "", "effort-relative JSON file with verification status and commands")
	fs.StringVar(&productEvidenceID, "product-evidence-id", "", "product evidence export id reference")
	fs.StringVar(&productEvidencePath, "product-evidence-path", "", "product evidence export path reference")
	fs.StringVar(&releaseEvidenceID, "release-evidence-id", "", "release evidence bundle id reference")
	fs.StringVar(&releaseEvidencePath, "release-evidence-path", "", "release evidence bundle path reference")
	fs.StringVar(&briefPath, "brief-path", "", "effort-relative brief path reference")
	fs.StringVar(&reconcileSummary, "reconcile-summary", "", "effort-relative JSON execution feedback summary to reconcile")
	fs.StringVar(&reconciledStateOutput, "reconciled-state-output", "", "effort-relative workflow-state JSON output path for reconciliation result")
	fs.StringVar(&rankerLearningEvidenceSummary, "ranker-learning-evidence-summary", "", "effort-relative JSON execution feedback summary to adapt into read-only ranker-learning evidence metadata")
	fs.Var(&changedPaths, "changed-path", "changed path reference (repeatable)")
	fs.Var(&tddRefs, "tdd-obligation-ref", "TDD obligation reference (repeatable)")
	fs.BoolVar(&jsonOutput, "json", false, "emit structured JSON output")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	reconcileMode := strings.TrimSpace(reconcileSummary) != "" || strings.TrimSpace(reconciledStateOutput) != ""
	rankerLearningMode := strings.TrimSpace(rankerLearningEvidenceSummary) != ""
	if reconcileMode && rankerLearningMode {
		fmt.Fprintln(stderr, "product feedback: --reconcile-summary and --ranker-learning-evidence-summary are mutually exclusive")
		return 2
	}
	if reconcileMode {
		if strings.TrimSpace(reconcileSummary) == "" || strings.TrimSpace(reconciledStateOutput) == "" {
			fmt.Fprintln(stderr, "product feedback: --reconcile-summary and --reconciled-state-output are both required in reconcile mode")
			return 2
		}
		if len(changedPaths.values) > 0 || len(tddRefs.values) > 0 ||
			strings.TrimSpace(laneHistoryFile) != "" || strings.TrimSpace(planProgressFile) != "" ||
			strings.TrimSpace(verificationFile) != "" || strings.TrimSpace(productEvidenceID) != "" ||
			strings.TrimSpace(productEvidencePath) != "" || strings.TrimSpace(releaseEvidenceID) != "" ||
			strings.TrimSpace(releaseEvidencePath) != "" || strings.TrimSpace(briefPath) != "" {
			fmt.Fprintln(stderr, "product feedback: reconcile mode only supports --effort-root, --reconcile-summary, --reconciled-state-output, and --json")
			return 2
		}
	}
	if rankerLearningMode {
		if len(changedPaths.values) > 0 || len(tddRefs.values) > 0 ||
			strings.TrimSpace(laneHistoryFile) != "" || strings.TrimSpace(planProgressFile) != "" ||
			strings.TrimSpace(verificationFile) != "" || strings.TrimSpace(productEvidenceID) != "" ||
			strings.TrimSpace(productEvidencePath) != "" || strings.TrimSpace(releaseEvidenceID) != "" ||
			strings.TrimSpace(releaseEvidencePath) != "" || strings.TrimSpace(briefPath) != "" {
			fmt.Fprintln(stderr, "product feedback: ranker-learning evidence mode only supports --effort-root, --ranker-learning-evidence-summary, and --json")
			return 2
		}
	}

	requirements := []struct {
		name  string
		value string
	}{
		{"--effort-root", effortRoot},
	}
	if !reconcileMode && !rankerLearningMode {
		requirements = append(requirements, []struct {
			name  string
			value string
		}{
			{"--lane-history-file", laneHistoryFile},
			{"--plan-progress-file", planProgressFile},
			{"--verification-file", verificationFile},
			{"--product-evidence-id", productEvidenceID},
			{"--product-evidence-path", productEvidencePath},
			{"--release-evidence-id", releaseEvidenceID},
			{"--release-evidence-path", releaseEvidencePath},
			{"--brief-path", briefPath},
		}...)
	}
	for _, req := range requirements {
		if strings.TrimSpace(req.value) == "" {
			fmt.Fprintf(stderr, "product feedback: %s is required\n", req.name)
			return 2
		}
	}
	if !reconcileMode && !rankerLearningMode && len(changedPaths.values) == 0 {
		fmt.Fprintln(stderr, "product feedback: at least one --changed-path is required")
		return 2
	}
	if !reconcileMode && !rankerLearningMode && len(tddRefs.values) == 0 {
		fmt.Fprintln(stderr, "product feedback: at least one --tdd-obligation-ref is required")
		return 2
	}

	absRoot, err := filepath.Abs(effortRoot)
	if err != nil {
		fmt.Fprintf(stderr, "product feedback: resolve effort root: %v\n", err)
		return 2
	}
	readJSONFile := func(kind, relPath string, target any, scanPrivacy bool) error {
		absPath, _, resolveErr := resolveProductFeedbackReferencePath(absRoot, relPath)
		if resolveErr != nil {
			return resolveErr
		}
		data, readErr := os.ReadFile(absPath)
		if readErr != nil {
			return readErr
		}
		if scanPrivacy {
			if marker := findProductFeedbackPrivacyMarker(string(data)); marker != "" {
				return fmt.Errorf("%s contains forbidden privacy marker %q", kind, marker)
			}
		}
		if err := json.Unmarshal(data, target); err != nil {
			return err
		}
		return nil
	}

	var workflowState productworkflow.WorkflowState
	if err := readJSONFile("workflow_state", "workflow_state.json", &workflowState, false); err != nil {
		fmt.Fprintf(stderr, "product feedback: workflow_state.json invalid: %v\n", err)
		return 1
	}
	if _, err := productworkflow.ParseWorkflowStateJSON(mustJSONMarshal(workflowState)); err != nil {
		fmt.Fprintf(stderr, "product feedback: workflow_state.json parse failed: %v\n", err)
		return 1
	}

	if rankerLearningMode {
		summaryAbs, summaryRel, err := resolveProductFeedbackReferencePath(absRoot, rankerLearningEvidenceSummary)
		if err != nil {
			fmt.Fprintf(stderr, "product feedback: ranker-learning evidence summary invalid: %v\n", err)
			return 1
		}
		summaryRaw, err := os.ReadFile(summaryAbs)
		if err != nil {
			fmt.Fprintf(stderr, "product feedback: ranker-learning evidence summary invalid: %v\n", err)
			return 1
		}
		if marker := findProductFeedbackPrivacyMarker(string(summaryRaw)); marker != "" {
			fmt.Fprintf(stderr, "product feedback: ranker-learning evidence summary contains forbidden privacy marker %q\n", marker)
			return 1
		}
		summary, wrappedStatus, err := parseProductFeedbackSummaryDocument(summaryRaw)
		if err != nil {
			fmt.Fprintf(stderr, "product feedback: %v\n", err)
			return 1
		}
		if wrappedStatus != "" && !executionFeedbackStatusPass(wrappedStatus) {
			fmt.Fprintf(stderr, "product feedback: ranker-learning evidence summary status must be pass, got %q\n", wrappedStatus)
			return 1
		}
		var tddLedger productworkflow.TDDLedger
		if err := readJSONFile("tdd", "tdd.json", &tddLedger, false); err != nil {
			fmt.Fprintf(stderr, "product feedback: tdd.json invalid: %v\n", err)
			return 1
		}
		if _, err := productworkflow.ParseTDDLedgerJSON(mustJSONMarshal(tddLedger)); err != nil {
			fmt.Fprintf(stderr, "product feedback: tdd.json parse failed: %v\n", err)
			return 1
		}
		knownObligations := make([]string, 0, len(tddLedger.Obligations))
		for _, obligation := range tddLedger.Obligations {
			knownObligations = append(knownObligations, obligation.ID)
		}
		adapted := productworkflow.AdaptExecutionFeedbackToRankerLearning(productworkflow.RankerLearningEvidenceInput{
			WorkflowState:        workflowState,
			KnownTDDObligations:  knownObligations,
			Summary:              summary,
			SummaryReferencePath: filepath.ToSlash(summaryRel),
		})
		logCLIPhase(rec, "product_feedback_ranker_learning_evidence", map[string]any{
			"status":          adapted.Status,
			"finding_count":   len(adapted.Findings),
			"blocker_count":   len(adapted.Blockers),
			"next_valid_step": adapted.NextValidAction,
		})
		out := productFeedbackRankerLearningOutput{
			Status:                 adapted.Status,
			EffortRoot:             absRoot,
			SummaryReferencePath:   filepath.ToSlash(summaryRel),
			RankerLearningEvidence: adapted,
			NextValidAction:        adapted.NextValidAction,
		}
		if jsonOutput {
			enc := json.NewEncoder(stdout)
			enc.SetIndent("", "  ")
			if err := enc.Encode(out); err != nil {
				fmt.Fprintf(stderr, "product feedback: encode JSON: %v\n", err)
				return 1
			}
		} else {
			fmt.Fprintf(stdout, "product feedback ranker-learning-evidence: %s\n", out.Status)
			fmt.Fprintf(stdout, "effort: %s\n", filepath.Base(absRoot))
			fmt.Fprintf(stdout, "summary: %s\n", out.SummaryReferencePath)
			fmt.Fprintf(stdout, "task: %s\n", adapted.Summary.TaskID)
			if adapted.Summary.ContextPackID != "" {
				fmt.Fprintf(stdout, "context_pack: %s (%s)\n", adapted.Summary.ContextPackID, adapted.Summary.FreshnessState)
			} else {
				fmt.Fprintf(stdout, "context_pack: (missing) (%s)\n", adapted.Summary.FreshnessState)
			}
			fmt.Fprintf(stdout, "changed_paths: %d\n", len(adapted.Summary.ChangedPaths))
			fmt.Fprintf(stdout, "verification: %s (%d commands)\n", adapted.Summary.VerificationStatus, adapted.Summary.VerificationCommandCount)
			fmt.Fprintf(stdout, "tdd_obligations: %d\n", adapted.Summary.TDDObligationCount)
			fmt.Fprintf(stdout, "lane_history: %d\n", adapted.Summary.LaneHistoryCount)
			fmt.Fprintf(stdout, "evidence_bundles: %d\n", adapted.Summary.EvidenceBundleCount)
			if adapted.Request != nil {
				fmt.Fprintf(stdout, "request: %s score=%.2f files=%d\n", adapted.Request.Action, adapted.Request.Score, len(adapted.Request.Files))
			}
			if len(adapted.Findings) > 0 {
				fmt.Fprintf(stdout, "findings: %d\n", len(adapted.Findings))
				for _, finding := range adapted.Findings {
					fmt.Fprintf(stdout, "  [%s] %s\n", finding.Kind, finding.Message)
				}
			}
			if len(adapted.Blockers) > 0 {
				fmt.Fprintf(stdout, "blockers: %d\n", len(adapted.Blockers))
				for _, blocker := range adapted.Blockers {
					fmt.Fprintf(stdout, "  [%s] %s\n", blocker.Kind, blocker.Message)
				}
			}
			fmt.Fprintf(stdout, "next: %s\n", out.NextValidAction)
		}
		if out.Status != "pass" {
			return 1
		}
		return 0
	}

	if reconcileMode {
		summaryAbs, summaryRel, err := resolveProductFeedbackReferencePath(absRoot, reconcileSummary)
		if err != nil {
			fmt.Fprintf(stderr, "product feedback: reconcile summary invalid: %v\n", err)
			return 1
		}
		summaryRaw, err := os.ReadFile(summaryAbs)
		if err != nil {
			fmt.Fprintf(stderr, "product feedback: reconcile summary invalid: %v\n", err)
			return 1
		}
		if marker := findProductFeedbackPrivacyMarker(string(summaryRaw)); marker != "" {
			fmt.Fprintf(stderr, "product feedback: reconcile summary contains forbidden privacy marker %q\n", marker)
			return 1
		}
		summary, wrappedStatus, err := parseProductFeedbackSummaryDocument(summaryRaw)
		if err != nil {
			fmt.Fprintf(stderr, "product feedback: %v\n", err)
			return 1
		}
		if wrappedStatus != "" && !executionFeedbackStatusPass(wrappedStatus) {
			fmt.Fprintf(stderr, "product feedback: reconcile summary status must be pass, got %q\n", wrappedStatus)
			return 1
		}
		var tddLedger productworkflow.TDDLedger
		if err := readJSONFile("tdd", "tdd.json", &tddLedger, false); err != nil {
			fmt.Fprintf(stderr, "product feedback: tdd.json invalid: %v\n", err)
			return 1
		}
		if _, err := productworkflow.ParseTDDLedgerJSON(mustJSONMarshal(tddLedger)); err != nil {
			fmt.Fprintf(stderr, "product feedback: tdd.json parse failed: %v\n", err)
			return 1
		}
		knownObligations := make([]string, 0, len(tddLedger.Obligations))
		for _, obligation := range tddLedger.Obligations {
			knownObligations = append(knownObligations, obligation.ID)
		}
		_, outputRel, err := resolveProductFeedbackReferencePath(absRoot, reconciledStateOutput)
		if err != nil {
			fmt.Fprintf(stderr, "product feedback: reconciled-state-output path invalid: %v\n", err)
			return 1
		}
		if filepath.ToSlash(outputRel) == "workflow_state.json" {
			fmt.Fprintln(stderr, "product feedback: reconciled-state-output must not overwrite workflow_state.json")
			return 1
		}
		reconciledAt := ""
		if len(summary.LaneHistory) > 0 {
			reconciledAt = strings.TrimSpace(summary.LaneHistory[len(summary.LaneHistory)-1].Timestamp)
		}
		if reconciledAt == "" {
			reconciledAt = strings.TrimSpace(workflowState.UpdatedAt)
		}
		reconciled := productworkflow.ReconcileExecutionFeedback(productworkflow.ExecutionFeedbackReconciliationInput{
			WorkflowState:        workflowState,
			Summary:              summary,
			ReconciledAt:         reconciledAt,
			SummaryReferencePath: filepath.ToSlash(summaryRel),
			ReconciledOutputPath: filepath.ToSlash(outputRel),
			KnownTDDObligations:  knownObligations,
		})
		out := productFeedbackReconcileOutput{
			Status:                reconciled.Status,
			EffortRoot:            absRoot,
			SummaryReferencePath:  filepath.ToSlash(summaryRel),
			ReconciledStateOutput: filepath.ToSlash(outputRel),
			Reconciliation:        reconciled,
			NextValidAction:       reconciled.NextValidAction,
		}
		logCLIPhase(rec, "product_feedback_reconcile", map[string]any{
			"status":          out.Status,
			"blocker_count":   len(out.Reconciliation.Blockers),
			"finding_count":   len(out.Reconciliation.Findings),
			"next_valid_step": out.NextValidAction,
		})
		if out.Status == "pass" {
			stateBytes, err := json.MarshalIndent(reconciled.WorkflowState, "", "  ")
			if err != nil {
				fmt.Fprintf(stderr, "product feedback: marshal reconciled workflow state: %v\n", err)
				return 1
			}
			if err := writeProductFeedbackReconciledState(absRoot, outputRel, append(stateBytes, '\n')); err != nil {
				fmt.Fprintf(stderr, "product feedback: write reconciled state output: %v\n", err)
				return 1
			}
		}
		if jsonOutput {
			enc := json.NewEncoder(stdout)
			enc.SetIndent("", "  ")
			if err := enc.Encode(out); err != nil {
				fmt.Fprintf(stderr, "product feedback: encode JSON: %v\n", err)
				return 1
			}
		} else {
			fmt.Fprintf(stdout, "product feedback reconcile: %s\n", out.Status)
			fmt.Fprintf(stdout, "effort: %s\n", filepath.Base(absRoot))
			fmt.Fprintf(stdout, "summary: %s\n", out.SummaryReferencePath)
			fmt.Fprintf(stdout, "reconciled-state-output: %s\n", out.ReconciledStateOutput)
			fmt.Fprintf(stdout, "evidence links: %d (product=%s release=%s)\n", out.Reconciliation.EvidenceLinks.LinkedArtifactPathCount, out.Reconciliation.EvidenceLinks.ProductEvidenceID, out.Reconciliation.EvidenceLinks.ReleaseEvidenceID)
			if len(out.Reconciliation.Blockers) > 0 {
				fmt.Fprintf(stdout, "blockers: %d\n", len(out.Reconciliation.Blockers))
				for _, blocker := range out.Reconciliation.Blockers {
					fmt.Fprintf(stdout, "  [%s] %s\n", blocker.Kind, blocker.Message)
				}
			}
			fmt.Fprintf(stdout, "next: %s\n", out.NextValidAction)
		}
		if out.Status != "pass" {
			return 1
		}
		return 0
	}

	var laneHistory []productworkflow.ExecutionLaneEventRef
	if err := readJSONFile("lane history reference", laneHistoryFile, &laneHistory, true); err != nil {
		fmt.Fprintf(stderr, "product feedback: lane history reference invalid: %v\n", err)
		return 1
	}
	var planProgress productworkflow.PlanExecuteProgressRef
	if err := readJSONFile("plan progress reference", planProgressFile, &planProgress, true); err != nil {
		fmt.Fprintf(stderr, "product feedback: plan progress reference invalid: %v\n", err)
		return 1
	}
	var verification productworkflow.VerificationRef
	if err := readJSONFile("verification reference", verificationFile, &verification, true); err != nil {
		fmt.Fprintf(stderr, "product feedback: verification reference invalid: %v\n", err)
		return 1
	}
	if marker := findProductFeedbackPrivacyMarker(strings.Join(changedPaths.values, "\n")); marker != "" {
		fmt.Fprintf(stderr, "product feedback: changed-path contains forbidden privacy marker %q\n", marker)
		return 1
	}
	if marker := findProductFeedbackPrivacyMarker(strings.Join(tddRefs.values, "\n")); marker != "" {
		fmt.Fprintf(stderr, "product feedback: tdd-obligation-ref contains forbidden privacy marker %q\n", marker)
		return 1
	}
	if marker := findProductFeedbackPrivacyMarker(strings.Join([]string{
		productEvidenceID, productEvidencePath, releaseEvidenceID, releaseEvidencePath, briefPath, verification.Status,
		strings.Join(verification.Commands, "\n"),
	}, "\n")); marker != "" {
		fmt.Fprintf(stderr, "product feedback: references contain forbidden privacy marker %q\n", marker)
		return 1
	}

	var tddLedger productworkflow.TDDLedger
	if err := readJSONFile("tdd", "tdd.json", &tddLedger, false); err != nil {
		fmt.Fprintf(stderr, "product feedback: tdd.json invalid: %v\n", err)
		return 1
	}
	if _, err := productworkflow.ParseTDDLedgerJSON(mustJSONMarshal(tddLedger)); err != nil {
		fmt.Fprintf(stderr, "product feedback: tdd.json parse failed: %v\n", err)
		return 1
	}
	summary := productworkflow.SummarizeExecutionFeedback(productworkflow.ExecutionFeedbackInput{
		EffortRoot:        absRoot,
		WorkflowState:     workflowState,
		TDDLedger:         tddLedger,
		LaneHistory:       laneHistory,
		PlanProgress:      planProgress,
		ProductEvidence:   productworkflow.EvidenceBundleRef{ID: strings.TrimSpace(productEvidenceID), Path: strings.TrimSpace(productEvidencePath)},
		ReleaseEvidence:   productworkflow.EvidenceBundleRef{ID: strings.TrimSpace(releaseEvidenceID), Path: strings.TrimSpace(releaseEvidencePath)},
		ChangedPaths:      changedPaths.values,
		Verification:      verification,
		BriefPath:         strings.TrimSpace(briefPath),
		TDDObligationRefs: tddRefs.values,
	})

	logCLIPhase(rec, "product_feedback", map[string]any{
		"status":          summary.Status,
		"finding_count":   len(summary.Findings),
		"blocker_count":   len(summary.Blockers),
		"next_valid_step": summary.NextValidAction,
	})
	out := productFeedbackOutput{
		Status:          summary.Status,
		EffortRoot:      absRoot,
		Summary:         summary,
		NextValidAction: summary.NextValidAction,
	}
	if jsonOutput {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(out); err != nil {
			fmt.Fprintf(stderr, "product feedback: encode JSON: %v\n", err)
			return 1
		}
	} else {
		fmt.Fprintf(stdout, "product feedback: %s\n", out.Status)
		fmt.Fprintf(stdout, "effort: %s\n", filepath.Base(absRoot))
		fmt.Fprintf(stdout, "phase: %s / %s\n", summary.PlanPhase, summary.PlanState)
		fmt.Fprintf(stdout, "verification: %s\n", summary.Verification.Status)
		if len(summary.Blockers) > 0 {
			fmt.Fprintf(stdout, "blockers: %d\n", len(summary.Blockers))
			for _, blocker := range summary.Blockers {
				fmt.Fprintf(stdout, "  [%s] %s\n", blocker.Kind, blocker.Message)
			}
		}
		fmt.Fprintf(stdout, "next: %s\n", summary.NextValidAction)
	}
	if out.Status != "pass" {
		return 1
	}
	return 0
}

func mustJSONMarshal(v any) []byte {
	data, err := json.Marshal(v)
	if err != nil {
		return []byte("{}")
	}
	return data
}

func parseProductFeedbackSummaryDocument(data []byte) (productworkflow.ExecutionFeedbackSummary, string, error) {
	var wrapped productFeedbackOutput
	if err := json.Unmarshal(data, &wrapped); err == nil && strings.TrimSpace(wrapped.Summary.Status) != "" {
		return wrapped.Summary, strings.TrimSpace(wrapped.Status), nil
	}
	var summary productworkflow.ExecutionFeedbackSummary
	if err := json.Unmarshal(data, &summary); err == nil && strings.TrimSpace(summary.Status) != "" {
		return summary, "", nil
	}
	return productworkflow.ExecutionFeedbackSummary{}, "", fmt.Errorf("feedback summary must be a feedback summary or full `product feedback --json` output")
}

func resolveProductFeedbackReferencePath(effortRoot, relPath string) (string, string, error) {
	clean, ok := cleanProductRelativePath(relPath)
	if !ok {
		return "", "", fmt.Errorf("path %q must be effort-relative and must not contain path traversal", relPath)
	}
	absPath := filepath.Join(effortRoot, clean)
	if err := rejectProductFeedbackSymlinkAncestors(effortRoot, clean); err != nil {
		return "", "", err
	}
	if info, err := os.Lstat(absPath); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", "", fmt.Errorf("path %q must not be a symlink", clean)
	} else if err != nil && !os.IsNotExist(err) {
		return "", "", fmt.Errorf("inspect path %q: %w", clean, err)
	}
	rootEval, err := filepath.EvalSymlinks(effortRoot)
	if err != nil {
		return "", "", fmt.Errorf("resolve effort root: %w", err)
	}
	existing := absPath
	for {
		eval, evalErr := filepath.EvalSymlinks(existing)
		if evalErr == nil {
			rel, relErr := filepath.Rel(filepath.Clean(rootEval), filepath.Clean(eval))
			if relErr != nil || rel == ".." || strings.HasPrefix(filepath.ToSlash(rel), "../") {
				return "", "", fmt.Errorf("path %q resolves outside effort root", clean)
			}
			return absPath, clean, nil
		}
		if !os.IsNotExist(evalErr) {
			return "", "", fmt.Errorf("resolve path %q: %w", clean, evalErr)
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return "", "", fmt.Errorf("resolve path %q: no existing ancestor", clean)
		}
		existing = parent
	}
}

func rejectProductFeedbackSymlinkAncestors(effortRoot, cleanRel string) error {
	parts := strings.Split(filepath.ToSlash(cleanRel), "/")
	current := filepath.Clean(effortRoot)
	for i := 0; i < len(parts)-1; i++ {
		current = filepath.Join(current, parts[i])
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("inspect path ancestor %q: %w", filepath.ToSlash(filepath.Join(parts[:i+1]...)), err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("path ancestor %q must not be a symlink", filepath.ToSlash(filepath.Join(parts[:i+1]...)))
		}
	}
	return nil
}

func findProductFeedbackPrivacyMarker(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	if normalized == "" {
		return ""
	}
	for _, marker := range productFeedbackPrivacyMarkers {
		if strings.Contains(normalized, marker) {
			return marker
		}
	}
	return ""
}

type multiValueFlag struct {
	values []string
}

func (m *multiValueFlag) String() string {
	return strings.Join(m.values, ",")
}

func (m *multiValueFlag) Set(value string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fmt.Errorf("empty value")
	}
	m.values = append(m.values, trimmed)
	return nil
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

func writeProductFeedbackReconciledState(effortRoot, relPath string, data []byte) error {
	clean := filepath.ToSlash(relPath)
	if _, _, err := resolveProductFeedbackReferencePath(effortRoot, clean); err != nil {
		return err
	}
	root, err := os.OpenRoot(effortRoot)
	if err != nil {
		return err
	}
	defer root.Close()
	dir := filepath.Dir(clean)
	if dir != "." {
		if err := root.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	if _, _, err := resolveProductFeedbackReferencePath(effortRoot, clean); err != nil {
		return err
	}
	return root.WriteFile(clean, data, 0o644)
}

func executionFeedbackStatusPass(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "pass", "passed", "complete", "completed", "success", "source-satisfied":
		return true
	default:
		return false
	}
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
