package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
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
	var jsonOutput bool
	fs.StringVar(&projectRoot, "project-root", ".", "path to the project root")
	fs.BoolVar(&jsonOutput, "json", false, "emit structured JSON output")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	result, err := productworkflow.DiscoverProfiles(projectRoot)
	if err != nil {
		fmt.Fprintf(stderr, "product research: %v\n", err)
		return 2
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
