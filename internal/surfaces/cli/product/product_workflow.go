package product

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"idiom/internal/evidence/telemetry"
	briefcmd "idiom/internal/surfaces/cli/brief"
	toolcmd "idiom/internal/surfaces/cli/tool"
	"idiom/internal/workflows/productworkflow"
)

type briefLintReport = briefcmd.LintReport

func RunMainWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	return runProductMainWithTelemetry(args, stdout, stderr, rec)
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
	case "validate-artifact":
		return runProductValidateArtifact(args[1:], stdout, stderr, rec)
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
	fmt.Fprintln(w, "  idiom product validate-artifact --kind prd|adr --path <artifact-path> [--json]")
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

type productValidateArtifactOutput struct {
	Status          string `json:"status"`
	Kind            string `json:"kind"`
	Path            string `json:"path"`
	Error           string `json:"error,omitempty"`
	NextValidAction string `json:"next_valid_action"`
}

func runProductValidateArtifact(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	fs := flag.NewFlagSet("idiom product validate-artifact", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var (
		kind          string
		path          string
		prdPath       string
		tddLedgerPath string
		jsonOutput    bool
	)

	fs.StringVar(&kind, "kind", "", "artifact kind to validate: prd or adr")
	fs.StringVar(&path, "path", "", "path to the generated artifact")
	fs.StringVar(&prdPath, "prd", "", "optional PRD path for ADR cross-link validation")
	fs.StringVar(&tddLedgerPath, "tdd-ledger", "", "optional tdd.json path for PRD/ADR cross-link validation")
	fs.BoolVar(&jsonOutput, "json", false, "emit structured JSON output")

	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}

		return 2
	}

	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind != "prd" && kind != "adr" {
		fmt.Fprintln(stderr, "product validate-artifact: --kind must be prd or adr")

		return 2
	}

	if strings.TrimSpace(path) == "" {
		fmt.Fprintln(stderr, "product validate-artifact: --path is required")

		return 2
	}

	cleanPath := filepath.Clean(path)
	data, err := os.ReadFile(cleanPath)
	out := productValidateArtifactOutput{
		Status:          "pass",
		Kind:            kind,
		Path:            cleanPath,
		NextValidAction: "artifact is parser-valid for product workflow intake",
	}

	if err != nil {
		out.Status = "fail"
		out.Error = fmt.Sprintf("read artifact: %v", err)
		out.NextValidAction = "regenerate or repair the artifact before continuing"
	} else {
		switch kind {
		case "prd":
			var prd productworkflow.PRDDocument

			prd, err = productworkflow.ParsePRDMarkdown(data)

			if err == nil && strings.TrimSpace(tddLedgerPath) != "" {
				var tdd productworkflow.TDDLedger

				tdd, err = parseProductValidatorTDDLedger(tddLedgerPath)
				if err == nil {
					tddIDs := map[string]bool{}

					for _, obligation := range tdd.Obligations {
						id := strings.TrimSpace(obligation.ID)
						if id != "" {
							tddIDs[id] = true
						}
					}

					if linkErrs := productworkflow.ValidatePRDTDDObligationCrossLinks(prd.Requirements, tddIDs); len(linkErrs) > 0 {
						err = errors.New(linkErrs[0].Message)
					}
				}
			}
		case "adr":
			var adr productworkflow.ADRDocument

			adr, err = productworkflow.ParseADRMarkdown(data)

			if err == nil && (strings.TrimSpace(prdPath) != "" || strings.TrimSpace(tddLedgerPath) != "") {
				var prdIDs map[string]bool

				if strings.TrimSpace(prdPath) != "" {
					var prd productworkflow.PRDDocument

					prd, err = parseProductValidatorPRD(prdPath)
					if err == nil {
						prdIDs = map[string]bool{}

						for _, req := range prd.Requirements {
							id := strings.TrimSpace(req.ID)
							if id != "" {
								prdIDs[id] = true
							}
						}
					}
				}

				var tddIDs map[string]bool

				if err == nil && strings.TrimSpace(tddLedgerPath) != "" {
					var tdd productworkflow.TDDLedger

					tdd, err = parseProductValidatorTDDLedger(tddLedgerPath)
					if err == nil {
						tddIDs = map[string]bool{}

						for _, obligation := range tdd.Obligations {
							id := strings.TrimSpace(obligation.ID)
							if id != "" {
								tddIDs[id] = true
							}
						}
					}
				}

				if err == nil {
					linkErrs := productworkflow.ValidateADRCrossLinks([]productworkflow.ParsedADR{{RelPath: cleanPath, Doc: adr}}, prdIDs, nil, tddIDs)
					if len(linkErrs) > 0 {
						err = errors.New(linkErrs[0].Message)
					}
				}
			}
		}

		if err != nil {
			out.Status = "fail"
			out.Error = err.Error()
			out.NextValidAction = "regenerate or repair the artifact before continuing"
		}
	}

	logProductPhase(rec, "product_validate_artifact", map[string]any{
		"status": out.Status,
		"kind":   kind,
	})

	if jsonOutput {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")

		if err := enc.Encode(out); err != nil {
			fmt.Fprintf(stderr, "product validate-artifact: encode JSON: %v\n", err)

			return 1
		}
	} else if out.Status == "pass" {
		fmt.Fprintf(stdout, "product validate-artifact: %s %s OK\n", out.Kind, out.Path)
		fmt.Fprintf(stdout, "  next: %s\n", out.NextValidAction)
	} else {
		fmt.Fprintf(stdout, "product validate-artifact: %s %s FAILED\n", out.Kind, out.Path)
		fmt.Fprintf(stdout, "  error: %s\n", out.Error)
		fmt.Fprintf(stdout, "  next: %s\n", out.NextValidAction)
	}

	if out.Status != "pass" {
		return 1
	}

	return 0
}

func parseProductValidatorPRD(path string) (productworkflow.PRDDocument, error) {
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return productworkflow.PRDDocument{}, fmt.Errorf("read PRD cross-link input: %w", err)
	}

	return productworkflow.ParsePRDMarkdown(data)
}

func parseProductValidatorTDDLedger(path string) (productworkflow.TDDLedger, error) {
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return productworkflow.TDDLedger{}, fmt.Errorf("read TDD cross-link input: %w", err)
	}

	return productworkflow.ParseTDDLedgerJSON(data)
}

func runProductLint(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	fs := flag.NewFlagSet("idiom product lint", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var (
		effortRoot string
		jsonOutput bool
	)

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

	result := productworkflow.LintEffortFolder(absRoot)

	result.NextValidAction = productLintNextValidAction(result.Status)
	logProductPhase(rec, "product_lint", map[string]any{
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

type productAuditSection struct {
	Name     string                               `json:"name"`
	Status   string                               `json:"status"`
	Findings []productworkflow.LintSummaryFinding `json:"findings"`
}

type productAuditResult struct {
	Status          string                                  `json:"status"`
	EffortRoot      string                                  `json:"effort_root"`
	CheckedFiles    []string                                `json:"checked_files"`
	Sections        []productAuditSection                   `json:"sections"`
	Findings        []productworkflow.LintSummaryFinding    `json:"findings"`
	Metrics         *productworkflow.ProductWorkflowMetrics `json:"metrics,omitempty"`
	NextValidAction string                                  `json:"next_valid_action"`
}

func buildAuditResult(lintResult productworkflow.LintSummaryResult) productAuditResult {
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
			} else if strings.HasPrefix(f.Code, "coverage_") {
				section = "tdd"
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

	var (
		effortRoot string
		jsonOutput bool
	)

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

	lintResult := productworkflow.LintEffortFolder(absRoot)
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
	logProductPhase(rec, "product_audit", map[string]any{
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

func productMetricsFinding(err error) productworkflow.LintSummaryFinding {
	path := "workflow_state.json"

	var metricsErr productworkflow.ProductWorkflowMetricsError

	if errors.As(err, &metricsErr) && strings.TrimSpace(metricsErr.Path) != "" {
		path = metricsErr.Path
	}

	return productworkflow.LintSummaryFinding{
		Code:    "metrics_error",
		Path:    path,
		Message: err.Error(),
	}
}

func markAuditSectionFailure(result *productAuditResult, name string, finding productworkflow.LintSummaryFinding) {
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
		Findings: []productworkflow.LintSummaryFinding{finding},
	})
}

func runProductEfforts(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	fs := flag.NewFlagSet("idiom product efforts", flag.ContinueOnError)
	fs.SetOutput(stderr)

	root := productworkflow.DefaultProductEffortsRoot
	statusFilter := "all"

	var (
		query      string
		jsonOutput bool
	)

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

	logProductPhase(rec, "product_efforts", map[string]any{
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

	var (
		effortRoot           string
		effortsRoot          string
		targetStatus         string
		targetCurrentState   string
		updatedAt            string
		eventID              string
		workspaceFingerprint string
		expectedRevision     int
		expectedSourceStatus string
		planMode             bool
		applyMode            bool
		jsonOutput           bool
	)

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

		if productStatusMigrationArgsRequestJSON(args) {
			return writeProductStatusMigrationUsageError(stdout, stderr, true, "invalid_arguments", "arguments", err.Error(), targetStatus)
		}

		return 2
	}

	if fs.NArg() != 0 {
		return writeProductStatusMigrationUsageError(stdout, stderr, jsonOutput, "unexpected_arguments", "arguments", fmt.Sprintf("unexpected arguments: %s", strings.Join(fs.Args(), " ")), targetStatus)
	}

	if strings.TrimSpace(effortRoot) == "" {
		return writeProductStatusMigrationUsageError(stdout, stderr, jsonOutput, "missing_effort_root", "effort_root", "--effort-root is required", targetStatus)
	}

	if strings.TrimSpace(targetStatus) == "" {
		return writeProductStatusMigrationUsageError(stdout, stderr, jsonOutput, "missing_target_status", "target_status", "--target-status is required", targetStatus)
	}

	if planMode == applyMode {
		return writeProductStatusMigrationUsageError(stdout, stderr, jsonOutput, "invalid_migration_mode", "mode", "choose exactly one of --plan or --apply", targetStatus)
	}

	if applyMode && expectedRevision < 0 {
		return writeProductStatusMigrationUsageError(stdout, stderr, jsonOutput, "missing_expected_revision", "expected_revision", "--apply requires --expected-revision", targetStatus)
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

	logProductPhase(rec, "product_status_migration", map[string]any{
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

func writeProductStatusMigrationUsageError(stdout io.Writer, stderr io.Writer, jsonOutput bool, code, path, message, targetStatus string) int {
	if jsonOutput {
		result := productStatusMigrationUsageResult(code, path, message, targetStatus)
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")

		if err := enc.Encode(result); err != nil {
			fmt.Fprintf(stderr, "product status-migration: encode JSON: %v\n", err)

			return 1
		}

		return 2
	}

	fmt.Fprintf(stderr, "product status-migration: %s\n", message)

	return 2
}

func productStatusMigrationUsageResult(code, path, message, targetStatus string) productworkflow.ProductStatusMigrationPlan {
	return productworkflow.ProductStatusMigrationPlan{
		Status:             "blocked",
		TargetStatusBucket: strings.ToLower(strings.TrimSpace(targetStatus)),
		Blockers: []productworkflow.ProductStatusMigrationBlocker{{
			Code:    code,
			Path:    path,
			Message: message,
		}},
		NextValidAction: "fix status-migration arguments and rerun the command",
	}
}

type productStatusUsageJSONResult struct {
	Status          string                      `json:"status"`
	Blockers        []productStatusUsageBlocker `json:"blockers"`
	NextValidAction string                      `json:"next_valid_action"`
}

type productStatusUsageBlocker struct {
	Code    string `json:"code"`
	Path    string `json:"path,omitempty"`
	Message string `json:"message"`
}

func productStatusUsageResult(code, path, message string) productStatusUsageJSONResult {
	return productUsageResult(code, path, message, "rerun with --effort-root <effort-root>")
}

func productUsageResult(code, path, message, nextValidAction string) productStatusUsageJSONResult {
	return productStatusUsageJSONResult{
		Status: "blocked",
		Blockers: []productStatusUsageBlocker{{
			Code:    code,
			Path:    path,
			Message: message,
		}},
		NextValidAction: nextValidAction,
	}
}

func writeProductUsageJSON(stdout io.Writer, stderr io.Writer, command string, result productStatusUsageJSONResult) int {
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")

	if err := enc.Encode(result); err != nil {
		fmt.Fprintf(stderr, "%s: encode JSON: %v\n", command, err)

		return 1
	}

	return 2
}

func productStatusMigrationArgsRequestJSON(args []string) bool {
	for _, arg := range args {
		if arg == "--json" || arg == "-json" || strings.HasPrefix(arg, "--json=") || strings.HasPrefix(arg, "-json=") {
			return true
		}
	}

	return false
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

	var (
		effortRoot   string
		jsonOutput   bool
		operatorView bool
	)

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
		if jsonOutput {
			result := productStatusUsageResult("missing-argument", "--effort-root", "--effort-root is required")
			enc := json.NewEncoder(stdout)
			enc.SetIndent("", "  ")

			if err := enc.Encode(result); err != nil {
				fmt.Fprintf(stderr, "product status: encode JSON: %v\n", err)

				return 1
			}

			return 2
		}

		fmt.Fprintln(stderr, "product status: --effort-root is required")

		return 2
	}

	absRoot, err := filepath.Abs(effortRoot)
	if err != nil {
		fmt.Fprintf(stderr, "product status: resolve effort root: %v\n", err)

		return 2
	}

	view, workflowState, wsErr := productworkflow.LoadCheckedProductStatus(absRoot)
	exitCode := 0

	if wsErr != nil {
		exitCode = 1
	}

	if view.AuditStatus != "pass" {
		exitCode = 1
	}

	operatorInputView := view

	view.NextValidActions = productStatusNextValidActions(view, exitCode, wsErr != nil)

	operatorStatus := productworkflow.BuildProductOperatorStatusView(operatorInputView, workflowState)

	logProductPhase(rec, "product_status", map[string]any{
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
	printProductRecoveryStatus(stdout, view.Recovery)

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

func printProductOperatorStatus(stdout io.Writer, effortName string, view productworkflow.ProductOperatorStatusView) {
	fmt.Fprint(stdout, productworkflow.FormatProductOperatorStatusText(effortName, view))
}

func printProductRecoveryStatus(stdout io.Writer, recoveryStatus *productworkflow.ProductRecoveryStatus) {
	fmt.Fprint(stdout, productworkflow.FormatProductRecoveryStatusText(recoveryStatus))
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

func runProductResearch(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	fs := flag.NewFlagSet("idiom product research", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var (
		projectRoot       string
		effortRoot        string
		researchOutputDir string
		researchProfile   string
		researchPromptDir string
		mergeEvidence     bool
		planRuns          bool
		renderPrompts     bool
		jsonOutput        bool
	)

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

	var (
		effortRoot    string
		renderPrompt  bool
		prdPromptPath string
		prdOutputPath string
		jsonOutput    bool
	)

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

	var (
		evidenceCards []productworkflow.EvidenceCard
		tddLedger     productworkflow.TDDLedger
		findings      []productworkflow.ProfileFinding
	)

	if err != nil {
		if os.IsNotExist(err) {
			findings = append(findings, productworkflow.ProfileFinding{
				Kind:     "missing_evidence_ledger",
				Message:  "required evidence/evidence.jsonl not found",
				File:     "evidence/evidence.jsonl",
				Severity: productworkflow.ProductWorkflowSeverityError,
			})
		} else {
			findings = append(findings, productworkflow.ProfileFinding{
				Kind:     "read_error",
				Message:  fmt.Sprintf("failed to read evidence/evidence.jsonl: %v", err),
				File:     "evidence/evidence.jsonl",
				Severity: productworkflow.ProductWorkflowSeverityError,
			})
		}
	} else {
		evidenceCards, err = productworkflow.ParseEvidenceCardsJSONL(evidenceData)
		if err != nil {
			findings = append(findings, productworkflow.ProfileFinding{
				Kind:     "validation_error",
				Message:  fmt.Sprintf("existing evidence ledger failed validation: %v", err),
				File:     "evidence/evidence.jsonl",
				Severity: productworkflow.ProductWorkflowSeverityError,
			})
		}
	}

	tddPath := filepath.Join(absRoot, "tdd.json")

	tddData, err := os.ReadFile(tddPath)
	if err != nil {
		if os.IsNotExist(err) {
			findings = append(findings, productworkflow.ProfileFinding{
				Kind:     "missing_tdd_ledger",
				Message:  "required tdd.json not found",
				File:     "tdd.json",
				Severity: productworkflow.ProductWorkflowSeverityError,
			})
		} else {
			findings = append(findings, productworkflow.ProfileFinding{
				Kind:     "read_error",
				Message:  fmt.Sprintf("failed to read tdd.json: %v", err),
				File:     "tdd.json",
				Severity: productworkflow.ProductWorkflowSeverityError,
			})
		}
	} else {
		tddLedger, err = productworkflow.ParseTDDLedgerJSON(tddData)
		if err != nil {
			findings = append(findings, productworkflow.ProfileFinding{
				Kind:     "validation_error",
				Message:  fmt.Sprintf("existing tdd.json failed validation: %v", err),
				File:     "tdd.json",
				Severity: productworkflow.ProductWorkflowSeverityError,
			})
		}
	}

	plan := productworkflow.PlanPRDDraftPrompt(absRoot, prdPromptPath, prdOutputPath, evidenceCards, tddLedger)

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

	logProductPhase(rec, "product_prd_render_prompt", map[string]any{
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

	var (
		effortRoot    string
		renderPrompt  bool
		adrPromptPath string
		adrOutputPath string
		jsonOutput    bool
	)

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

	var (
		evidenceCards []productworkflow.EvidenceCard
		prdDoc        productworkflow.PRDDocument
		findings      []productworkflow.ProfileFinding
	)

	evidencePath := filepath.Join(absRoot, "evidence", "evidence.jsonl")

	evidenceData, err := os.ReadFile(evidencePath)
	if err != nil {
		if os.IsNotExist(err) {
			findings = append(findings, productworkflow.ProfileFinding{Kind: "missing_evidence_ledger", Message: "required evidence/evidence.jsonl not found", File: "evidence/evidence.jsonl", Severity: productworkflow.ProductWorkflowSeverityError})
		} else {
			findings = append(findings, productworkflow.ProfileFinding{Kind: "read_error", Message: fmt.Sprintf("failed to read evidence/evidence.jsonl: %v", err), File: "evidence/evidence.jsonl", Severity: productworkflow.ProductWorkflowSeverityError})
		}
	} else {
		evidenceCards, err = productworkflow.ParseEvidenceCardsJSONL(evidenceData)
		if err != nil {
			findings = append(findings, productworkflow.ProfileFinding{Kind: "validation_error", Message: fmt.Sprintf("existing evidence ledger failed validation: %v", err), File: "evidence/evidence.jsonl", Severity: productworkflow.ProductWorkflowSeverityError})
		}
	}

	prdPath := filepath.Join(absRoot, "prd.md")

	prdData, err := os.ReadFile(prdPath)
	if err != nil {
		if os.IsNotExist(err) {
			findings = append(findings, productworkflow.ProfileFinding{Kind: "missing_prd", Message: "required prd.md not found", File: "prd.md", Severity: productworkflow.ProductWorkflowSeverityError})
		} else {
			findings = append(findings, productworkflow.ProfileFinding{Kind: "read_error", Message: fmt.Sprintf("failed to read prd.md: %v", err), File: "prd.md", Severity: productworkflow.ProductWorkflowSeverityError})
		}
	} else {
		prdDoc, err = productworkflow.ParsePRDMarkdown(prdData)
		if err != nil {
			findings = append(findings, productworkflow.ProfileFinding{Kind: "validation_error", Message: fmt.Sprintf("existing prd.md failed validation: %v", err), File: "prd.md", Severity: productworkflow.ProductWorkflowSeverityError})
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

	logProductPhase(rec, "product_adr_render_prompt", map[string]any{
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

	jsonRequested := productStatusMigrationArgsRequestJSON(args)
	if jsonRequested {
		fs.SetOutput(io.Discard)
	}

	var (
		effortRoot       string
		planObligations  bool
		evaluateCoverage bool
		jsonOutput       bool
	)

	fs.StringVar(&effortRoot, "effort-root", "", "path to the effort folder root")
	fs.BoolVar(&planObligations, "plan-obligations", false, "plan deterministic TDD obligations from accepted PRD and ADR artifacts")
	fs.BoolVar(&evaluateCoverage, "evaluate-coverage", false, "evaluate deterministic coverage gate from accepted PRD/ADR/TDD/traceability artifacts")
	fs.BoolVar(&jsonOutput, "json", false, "emit structured JSON output")

	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}

		if jsonOutput || jsonRequested {
			return writeProductUsageJSON(stdout, stderr, "product tdd", productUsageResult(
				"tdd_flag_parse_error",
				"flags",
				err.Error(),
				"rerun with valid product tdd flags",
			))
		}

		return 2
	}

	if planObligations == evaluateCoverage {
		if jsonOutput {
			code := "tdd_missing_mode_flag"
			if planObligations && evaluateCoverage {
				code = "tdd_multiple_mode_flags"
			}

			return writeProductUsageJSON(stdout, stderr, "product tdd", productUsageResult(
				code,
				"mode",
				"exactly one of --plan-obligations or --evaluate-coverage is required",
				"rerun with exactly one of --plan-obligations or --evaluate-coverage",
			))
		}

		fmt.Fprintln(stderr, "product tdd: exactly one of --plan-obligations or --evaluate-coverage is required")

		return 2
	}

	if strings.TrimSpace(effortRoot) == "" {
		if jsonOutput {
			return writeProductUsageJSON(stdout, stderr, "product tdd", productUsageResult(
				"tdd_missing_effort_root",
				"--effort-root",
				"--effort-root is required",
				"rerun with --effort-root <effort-root>",
			))
		}

		fmt.Fprintln(stderr, "product tdd: --effort-root is required")

		return 2
	}

	absRoot, err := filepath.Abs(effortRoot)
	if err != nil {
		if jsonOutput {
			return writeProductUsageJSON(stdout, stderr, "product tdd", productUsageResult(
				"tdd_invalid_effort_root",
				"--effort-root",
				"resolve effort root: "+err.Error(),
				"rerun with a valid --effort-root <effort-root>",
			))
		}

		fmt.Fprintf(stderr, "product tdd: resolve effort root: %v\n", err)

		return 2
	}

	var findings []productworkflow.TDDObligationPlanFinding

	addFinding := func(kind, message, file string) {
		findings = append(findings, productworkflow.TDDObligationPlanFinding{
			Kind:     kind,
			Message:  message,
			File:     file,
			Severity: productworkflow.ProductWorkflowSeverityError,
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

		parsedADRs = append(parsedADRs, productworkflow.ParsedADR{Doc: doc, RelPath: relPath, RawMarkdown: string(data)})
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

		lintResult := productworkflow.LintEffortFolder(absRoot)

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
			satisfiedTDDIDs := productworkflow.BuildSatisfiedTDDObligationIDs(ledger, implementationRecords)

			eval = productworkflow.EvaluateCoverageGateWithAuthorityPreflight(prdDoc, adrIndex, parsedADRs, ledger, traceability, lintResult.Findings, satisfiedTDDIDs)
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

		if len(findings) > 0 {
			authorityPreflightFindings := append([]productworkflow.LintSummaryFinding(nil), lintResult.Findings...)
			for _, f := range findings {
				authorityPreflightFindings = append(authorityPreflightFindings, productworkflow.LintSummaryFinding{
					Code:    f.Kind,
					Path:    f.File,
					Message: f.Message,
				})
			}

			eval = productworkflow.ApplyCoverageGateAuthorityPreflight(eval, authorityPreflightFindings)
		}

		logProductPhase(rec, "product_tdd_coverage_gate_evaluated", map[string]any{
			"status":         eval.Status,
			"finding_count":  len(eval.Findings),
			"blocker_count":  len(eval.Blockers),
			"criteria_count": len(eval.Criteria),
		})

		coverageGateEvent := productworkflow.BuildCoverageGateEvaluatedEvent(eval, ledger)
		logProductPhase(rec, coverageGateEvent.Event, map[string]any{
			"event":           coverageGateEvent.Event,
			"effort_id":       coverageGateEvent.EffortID,
			"task_id":         coverageGateEvent.TaskID,
			"coverage":        coverageGateEvent.Coverage,
			"gate_result":     coverageGateEvent.GateResult,
			"blocked_reasons": coverageGateEvent.BlockedReasons,
			"waivers":         coverageGateEvent.Waivers,
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

	logProductPhase(rec, "product_tdd_plan_obligations", map[string]any{
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

	var (
		effortRoot                    string
		laneHistoryFile               string
		planProgressFile              string
		verificationFile              string
		productEvidenceID             string
		productEvidencePath           string
		releaseEvidenceID             string
		releaseEvidencePath           string
		briefPath                     string
		reconcileSummary              string
		reconciledStateOutput         string
		rankerLearningEvidenceSummary string
		jsonOutput                    bool
		changedPaths                  multiValueFlag
		tddRefs                       multiValueFlag
	)

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
		logProductPhase(rec, "product_feedback_ranker_learning_evidence", map[string]any{
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
		logProductPhase(rec, "product_feedback_reconcile", map[string]any{
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

	logProductPhase(rec, "product_feedback", map[string]any{
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

// multiValueFlag is intentionally separate from util.MultiFlag:
// it trims whitespace and rejects empty values, which util.MultiFlag does not.
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
	Status             string                                  `json:"status"`
	EffortRoot         string                                  `json:"effort_root"`
	BriefOutputPath    string                                  `json:"brief_output_path,omitempty"`
	Generated          productworkflow.BriefGenerationResult   `json:"generated"`
	CoverageEvaluation *productworkflow.CoverageGateEvaluation `json:"coverage_evaluation,omitempty"`
	Lint               *briefLintReport                        `json:"lint,omitempty"`
	Blocker            string                                  `json:"blocker,omitempty"`
	NextValidAction    string                                  `json:"next_valid_action,omitempty"`
}

func runProductBrief(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	fs := flag.NewFlagSet("idiom product brief", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var (
		effortRoot  string
		render      bool
		briefOutput string
		jsonOutput  bool
	)

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
	if strings.TrimSpace(input.CoverageEvaluation.Status) != "" {
		coverageEvaluation := input.CoverageEvaluation

		out.CoverageEvaluation = &coverageEvaluation

		coverageGateEvent := productworkflow.BuildCoverageGateEvaluatedEvent(coverageEvaluation, input.TDD)
		logProductPhase(rec, coverageGateEvent.Event, map[string]any{
			"event":           coverageGateEvent.Event,
			"effort_id":       coverageGateEvent.EffortID,
			"task_id":         coverageGateEvent.TaskID,
			"coverage":        coverageGateEvent.Coverage,
			"gate_result":     coverageGateEvent.GateResult,
			"blocked_reasons": coverageGateEvent.BlockedReasons,
			"waivers":         coverageGateEvent.Waivers,
			"source":          "product_brief_render",
		})
	}

	var (
		cleanup          func()
		briefPathForLint string
	)

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
			var (
				lintStdout bytes.Buffer
				lintStderr bytes.Buffer
			)

			lintCode := briefcmd.RunLint([]string{briefPathForLint, "--json"}, &lintStdout, &lintStderr, rec)

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

	logProductPhase(rec, "product_brief_render", map[string]any{
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
				in.ADRs = append(in.ADRs, productworkflow.ParsedADR{Doc: parsed, RelPath: rel, RawMarkdown: string(data)})
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

			for _, candidate := range productBriefExistingPathCandidates(clean, in.WorkflowState.Context.PathAliases) {
				if _, statErr := os.Stat(filepath.Join(workspaceRoot, filepath.FromSlash(candidate))); statErr == nil {
					in.ExistingWorkspacePath[candidate] = true
				}
			}
		}
	}

	if len(blockers) == 0 {
		satisfiedTDDIDs := productworkflow.BuildSatisfiedTDDObligationIDs(in.TDD, in.ImplementationRecords)
		lintResult := productworkflow.LintEffortFolder(effortRoot)

		in.CoverageEvaluation = productworkflow.EvaluateCoverageGateWithAuthorityPreflight(in.PRD, in.ADRIndex, in.ADRs, in.TDD, in.Traceability, lintResult.Findings, satisfiedTDDIDs)

		if productworkflow.CoverageGateHasBlockers(in.CoverageEvaluation) {
			blockers = append(blockers, "coverage gate evaluation is blocked")
		}
	}

	return in, blockers
}

func productBriefExistingPathCandidates(path string, aliases map[string]string) []string {
	candidates := []string{path}
	if alias := strings.TrimSpace(aliases[path]); alias != "" {
		candidates = append(candidates, filepath.ToSlash(alias))
	}

	return candidates
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
	logProductPhase(rec, "product_research_plan_runs", map[string]any{
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
			Severity: productworkflow.ProductWorkflowSeverityError,
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

	logProductPhase(rec, "product_research_render_prompts", map[string]any{
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
			Severity: productworkflow.ProductWorkflowSeverityError,
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
				Severity: productworkflow.ProductWorkflowSeverityError,
			})
		} else {
			existing = parsed
		}
	} else if os.IsNotExist(readErr) {
		findings = append(findings, productworkflow.ProfileFinding{
			Kind:     "missing_evidence_ledger",
			Message:  "required evidence/evidence.jsonl not found",
			File:     "evidence/evidence.jsonl",
			Severity: productworkflow.ProductWorkflowSeverityError,
		})
	} else {
		findings = append(findings, productworkflow.ProfileFinding{
			Kind:     "read_error",
			Message:  fmt.Sprintf("failed to read evidence/evidence.jsonl: %v", readErr),
			File:     "evidence/evidence.jsonl",
			Severity: productworkflow.ProductWorkflowSeverityError,
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

	logProductPhase(rec, "product_research_merge", map[string]any{
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

func logProductPhase(rec *telemetry.Recorder, eventType string, fields map[string]any) {
	toolcmd.LogCLIPhase(rec, eventType, fields)
}
