package product

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"idiom/internal/evidence/telemetry"
	"idiom/internal/workflows/productworkflow"
)

func RunMainWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
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
	case "create":
		return runProductCreate(args[1:], stdout, stderr, rec)
	case "status":
		return runProductStatus(args[1:], stdout, stderr, rec)
	case "transition":
		return runProductTransition(args[1:], stdout, stderr, rec)
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
	fmt.Fprintln(w, "  idiom product create --effort-root <path> [--effort-id <id>] [--title <title>] [--json]")
	fmt.Fprintln(w, "  idiom product status --effort-root <path> [--operator-view] [--json]")
	fmt.Fprintln(w, "  idiom product transition --effort-root <path> --action <id> [--expected-revision <n>] [--json]")
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

	out := productworkflow.ValidateProductArtifact(productworkflow.ProductArtifactValidationOptions{
		Kind:          kind,
		Path:          path,
		PRDPath:       prdPath,
		TDDLedgerPath: tddLedgerPath,
	})

	telemetry.LogPhase(rec, "product_validate_artifact", map[string]any{
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
	} else if out.Status == productworkflow.ProductWorkflowStatusPass {
		fmt.Fprintf(stdout, "product validate-artifact: %s %s OK\n", out.Kind, out.Path)
		fmt.Fprintf(stdout, "  next: %s\n", out.NextValidAction)
	} else {
		fmt.Fprintf(stdout, "product validate-artifact: %s %s FAILED\n", out.Kind, out.Path)
		fmt.Fprintf(stdout, "  error: %s\n", out.Error)
		fmt.Fprintf(stdout, "  next: %s\n", out.NextValidAction)
	}

	if out.Status != productworkflow.ProductWorkflowStatusPass {
		return 1
	}

	return 0
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
	telemetry.LogPhase(rec, "product_lint", map[string]any{
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
		if result.Status == productworkflow.ProductWorkflowStatusPass {
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

	if result.Status != productworkflow.ProductWorkflowStatusPass {
		return 1
	}

	return 0
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

	auditResult := productworkflow.BuildCheckedProductAudit(absRoot)
	telemetry.LogPhase(rec, "product_audit", map[string]any{
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
		if auditResult.Status == productworkflow.ProductWorkflowStatusPass {
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

	if auditResult.Status != productworkflow.ProductWorkflowStatusPass {
		return 1
	}

	return 0
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

	telemetry.LogPhase(rec, "product_efforts", map[string]any{
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

	if result.Status != productworkflow.ProductWorkflowStatusPass {
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
	if result.Status != productworkflow.ProductWorkflowStatusPass {
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

	telemetry.LogPhase(rec, "product_status_migration", map[string]any{
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
		Status:             productworkflow.ProductWorkflowStatusBlocked,
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
	Status          productworkflow.ProductWorkflowStatus `json:"status"`
	Blockers        []productStatusUsageBlocker           `json:"blockers"`
	NextValidAction string                                `json:"next_valid_action"`
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
		Status: productworkflow.ProductWorkflowStatusBlocked,
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
	if result.Status == productworkflow.ProductWorkflowStatusPlanned || result.Status == productworkflow.ProductWorkflowStatusApplied {
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
	fmt.Fprintf(w, "product status-migration: %s %s\n", mode, strings.ToUpper(string(result.Status)))

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

	inspection := productworkflow.InspectProductStatus(absRoot)
	view := inspection.View
	exitCode := 0

	if inspection.WorkflowStateInvalid {
		exitCode = 1
	}

	if view.AuditStatus != productworkflow.ProductWorkflowStatusPass {
		exitCode = 1
	}

	displayNextActions := inspection.DisplayNextActions
	operatorStatus := inspection.OperatorView

	telemetry.LogPhase(rec, "product_status", map[string]any{
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

	if inspection.WorkflowStateInvalid {
		fmt.Fprintf(stdout, "product status: ERROR %s\n", filepath.Base(absRoot))
		fmt.Fprintln(stdout, "  status: ERROR")

		for _, b := range view.Blockers {
			fmt.Fprintf(stdout, "  [%s] %s\n", b.Code, b.Message)
		}

		if len(displayNextActions) > 0 {
			fmt.Fprintf(stdout, "  next: %s\n", displayNextActions[0])
		}

		return exitCode
	}

	auditLabel := "OK"
	if view.AuditStatus != productworkflow.ProductWorkflowStatusPass {
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

	if len(displayNextActions) > 0 {
		fmt.Fprintf(stdout, "  next_actions: %s\n", strings.Join(displayNextActions, ", "))
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

	if len(displayNextActions) > 0 {
		fmt.Fprintf(stdout, "  next: %s\n", displayNextActions[0])
	}

	return exitCode
}

func runProductCreate(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	fs := flag.NewFlagSet("idiom product create", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var (
		effortRoot string
		effortID   string
		title      string
		jsonOutput bool
	)

	fs.StringVar(&effortRoot, "effort-root", "", "path to the effort folder root")
	fs.StringVar(&effortID, "effort-id", "", "optional effort id")
	fs.StringVar(&title, "title", "", "optional effort title")
	fs.BoolVar(&jsonOutput, "json", false, "emit structured JSON output")

	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}

		return 2
	}

	if strings.TrimSpace(effortRoot) == "" {
		fmt.Fprintln(stderr, "product create: --effort-root is required")

		return 2
	}

	result, createErr := productworkflow.InitializeEffort(productworkflow.CreateEffortOptions{
		EffortRoot: effortRoot,
		EffortID:   effortID,
		Title:      title,
	})
	if createErr != nil {
		result.Status = productworkflow.ProductWorkflowStatusError
		result.NextValidAction = "fix product effort creation blockers before continuing"
		result.Blockers = []string{createErr.Error()}
		if strings.TrimSpace(result.EffortRoot) == "" {
			result.EffortRoot = effortRoot
		}
	}

	telemetry.LogPhase(rec, "product_create", map[string]any{
		"status":        result.Status,
		"effort_root":   result.EffortRoot,
		"effort_id":     result.EffortID,
		"current_phase": result.CurrentPhase,
		"blockers":      len(result.Blockers),
	})

	if jsonOutput {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")

		if err := enc.Encode(result); err != nil {
			fmt.Fprintf(stderr, "product create: encode JSON: %v\n", err)

			return 1
		}
	} else {
		printProductCreate(stdout, result)
	}

	if result.Status == productworkflow.ProductWorkflowStatusError || len(result.Blockers) > 0 {
		return 1
	}

	return 0
}

func printProductCreate(stdout io.Writer, result productworkflow.CreateResult) {
	fmt.Fprintf(stdout, "product create: %s\n", result.Status)

	if strings.TrimSpace(result.EffortRoot) != "" {
		fmt.Fprintf(stdout, "  effort_root: %s\n", result.EffortRoot)
	}

	if strings.TrimSpace(result.EffortID) != "" {
		fmt.Fprintf(stdout, "  effort_id: %s\n", result.EffortID)
	}

	if strings.TrimSpace(string(result.CurrentPhase)) != "" {
		fmt.Fprintf(stdout, "  phase: %s\n", result.CurrentPhase)
	}

	if len(result.Blockers) > 0 {
		fmt.Fprintf(stdout, "  blockers (%d):\n", len(result.Blockers))

		for _, blocker := range result.Blockers {
			fmt.Fprintf(stdout, "    %s\n", blocker)
		}
	}

	if strings.TrimSpace(result.NextValidAction) != "" {
		fmt.Fprintf(stdout, "  next: %s\n", result.NextValidAction)
	}
}

func runProductTransition(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	fs := flag.NewFlagSet("idiom product transition", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var (
		effortRoot       string
		action           string
		expectedRevision int
		jsonOutput       bool
	)

	fs.StringVar(&effortRoot, "effort-root", "", "path to the effort folder root")
	fs.StringVar(&action, "action", "", "workflow transition action to apply")
	fs.IntVar(&expectedRevision, "expected-revision", 0, "optional workflow state revision guard")
	fs.BoolVar(&jsonOutput, "json", false, "emit structured JSON output")

	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}

		return 2
	}

	if strings.TrimSpace(effortRoot) == "" {
		fmt.Fprintln(stderr, "product transition: --effort-root is required")

		return 2
	}

	if strings.TrimSpace(action) == "" {
		fmt.Fprintln(stderr, "product transition: --action is required")

		return 2
	}

	result := productworkflow.ApplyProductWorkflowTransition(productworkflow.ProductWorkflowTransitionApplyOptions{
		EffortRoot:            effortRoot,
		Action:                action,
		Actor:                 "operator",
		ExpectedStateRevision: expectedRevision,
	})

	telemetry.LogPhase(rec, "product_transition", map[string]any{
		"status":      result.Status,
		"action":      result.Action,
		"from_state":  result.FromState,
		"to_state":    result.ToState,
		"blockers":    len(result.Blockers),
		"effort_root": result.EffortRoot,
	})

	if jsonOutput {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")

		if err := enc.Encode(result); err != nil {
			fmt.Fprintf(stderr, "product transition: encode JSON: %v\n", err)

			return 1
		}
	} else {
		printProductTransition(stdout, result)
	}

	if result.Status != productworkflow.ProductWorkflowStatusApplied {
		return 1
	}

	return 0
}

func printProductTransition(stdout io.Writer, result productworkflow.ProductWorkflowTransitionApplyResult) {
	fmt.Fprintf(stdout, "product transition: %s\n", result.Status)
	fmt.Fprintf(stdout, "  action: %s\n", result.Action)

	if result.FromState != "" || result.ToState != "" {
		fmt.Fprintf(stdout, "  state: %s -> %s\n", result.FromState, result.ToState)
	}

	if result.Event.EventID != "" {
		fmt.Fprintf(stdout, "  event_id: %s\n", result.Event.EventID)
	}

	if len(result.Blockers) > 0 {
		fmt.Fprintf(stdout, "  blockers (%d):\n", len(result.Blockers))

		for _, blocker := range result.Blockers {
			if blocker.Path != "" {
				fmt.Fprintf(stdout, "    [%s] %s: %s\n", blocker.Code, blocker.Path, blocker.Message)
			} else {
				fmt.Fprintf(stdout, "    [%s] %s\n", blocker.Code, blocker.Message)
			}
		}
	}

	if strings.TrimSpace(result.NextValidAction) != "" {
		fmt.Fprintf(stdout, "  next: %s\n", result.NextValidAction)
	}
}

func printProductOperatorStatus(stdout io.Writer, effortName string, view productworkflow.ProductOperatorStatusView) {
	fmt.Fprint(stdout, productworkflow.FormatProductOperatorStatusText(effortName, view))
}

func printProductRecoveryStatus(stdout io.Writer, recoveryStatus *productworkflow.ProductRecoveryStatus) {
	fmt.Fprint(stdout, productworkflow.FormatProductRecoveryStatusText(recoveryStatus))
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

	if mergeEvidence {
		return runProductResearchMergeEvidence(projectRoot, effortRoot, researchOutputDir, researchProfile, jsonOutput, stdout, stderr, rec)
	}

	if planRuns {
		return runProductResearchPlanRuns(projectRoot, researchOutputDir, researchProfile, jsonOutput, stdout, stderr, rec)
	}

	if renderPrompts {
		return runProductResearchRenderPrompts(projectRoot, effortRoot, researchOutputDir, researchPromptDir, researchProfile, jsonOutput, stdout, stderr, rec)
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

	out, err := productworkflow.RenderPRDDraftPromptFromEffort(productworkflow.PRDDraftPromptRenderRequest{
		EffortRoot:    absRoot,
		PromptPath:    prdPromptPath,
		PRDOutputPath: prdOutputPath,
	})
	if err != nil {
		fmt.Fprintf(stderr, "product prd: %v\n", err)

		return 2
	}

	telemetry.LogPhase(rec, "product_prd_render_prompt", map[string]any{
		"finding_count":       len(out.Plan.Findings),
		"evidence_card_count": out.Plan.EvidenceCardCount,
		"prompt_write_count":  out.PromptWriteCount,
	})

	if jsonOutput {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")

		if err := enc.Encode(out); err != nil {
			fmt.Fprintf(stderr, "product prd: encode JSON: %v\n", err)

			return 1
		}
	} else {
		fmt.Fprintf(stdout, "product prd render-prompt: %s (%d findings)\n", out.Status, len(out.Plan.Findings))
	}

	if out.Status != productworkflow.ProductWorkflowStatusPass {
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

	out, err := productworkflow.RenderADRDraftPromptFromEffort(productworkflow.ADRDraftPromptRenderRequest{
		EffortRoot:    absRoot,
		PromptPath:    adrPromptPath,
		ADROutputPath: adrOutputPath,
	})
	if err != nil {
		fmt.Fprintf(stderr, "product adr: %v\n", err)

		return 2
	}

	telemetry.LogPhase(rec, "product_adr_render_prompt", map[string]any{
		"finding_count":         len(out.Plan.Findings),
		"evidence_card_count":   out.Plan.EvidenceCardCount,
		"prd_requirement_count": out.Plan.PRDRequirementCount,
		"prompt_write_count":    out.PromptWriteCount,
	})

	if jsonOutput {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")

		if err := enc.Encode(out); err != nil {
			fmt.Fprintf(stderr, "product adr: encode JSON: %v\n", err)

			return 1
		}
	} else {
		fmt.Fprintf(stdout, "product adr render-prompt: %s (%d findings)\n", out.Status, len(out.Plan.Findings))
	}

	if out.Status != productworkflow.ProductWorkflowStatusPass {
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
		promoteCoverage  string
		writePromotion   bool
		jsonOutput       bool
	)

	fs.StringVar(&effortRoot, "effort-root", "", "path to the effort folder root")
	fs.BoolVar(&planObligations, "plan-obligations", false, "plan deterministic TDD obligations from accepted PRD and ADR artifacts")
	fs.BoolVar(&evaluateCoverage, "evaluate-coverage", false, "evaluate deterministic coverage gate from accepted PRD/ADR/TDD/traceability artifacts")
	fs.StringVar(&promoteCoverage, "promote-coverage", "", "effort-relative behavioral coverage JSON artifact to validate and project into tdd.json")
	fs.BoolVar(&writePromotion, "write", false, "persist a successful --promote-coverage projection to tdd.json")
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

	modeCount := 0
	if planObligations {
		modeCount++
	}
	if evaluateCoverage {
		modeCount++
	}
	if strings.TrimSpace(promoteCoverage) != "" {
		modeCount++
	}

	if modeCount != 1 {
		if jsonOutput {
			code := "tdd_missing_mode_flag"
			if modeCount > 1 {
				code = "tdd_multiple_mode_flags"
			}

			return writeProductUsageJSON(stdout, stderr, "product tdd", productUsageResult(
				code,
				"mode",
				"exactly one of --plan-obligations, --evaluate-coverage, or --promote-coverage is required",
				"rerun with exactly one product tdd mode flag",
			))
		}

		fmt.Fprintln(stderr, "product tdd: exactly one of --plan-obligations, --evaluate-coverage, or --promote-coverage is required")

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

	if evaluateCoverage {
		out := productCoverageOutput{
			Status:     productworkflow.ProductWorkflowStatusBlocked,
			EffortRoot: absRoot,
		}
		loaded := productworkflow.LoadCoverageGateEvaluation(absRoot)
		out.Status = loaded.Evaluation.Status
		out.Evaluation = loaded.Evaluation
		if out.Evaluation.Criteria == nil {
			out.Evaluation.Criteria = []productworkflow.CoverageGateCriterion{}
		}
		if out.Evaluation.Findings == nil {
			out.Evaluation.Findings = []productworkflow.CoverageGateFinding{}
		}
		if out.Evaluation.Blockers == nil {
			out.Evaluation.Blockers = []productworkflow.CoverageGateBlocker{}
		}

		coverageGateEvent := productworkflow.BuildCoverageGateEvaluatedEvent(out.Evaluation, loaded.TDD)
		telemetry.LogPhase(rec, coverageGateEvent.Event, map[string]any{
			"event":           coverageGateEvent.Event,
			"effort_id":       coverageGateEvent.EffortID,
			"task_id":         coverageGateEvent.TaskID,
			"coverage":        coverageGateEvent.Coverage,
			"gate_result":     coverageGateEvent.GateResult,
			"blocked_reasons": coverageGateEvent.BlockedReasons,
			"waivers":         coverageGateEvent.Waivers,
		})

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

		if out.Status != productworkflow.ProductWorkflowStatusPass {
			return 1
		}

		return 0
	}

	if strings.TrimSpace(promoteCoverage) != "" {
		review := productworkflow.PromoteBehavioralCoverageFromEffort(productworkflow.BehavioralCoveragePromotionRequest{
			EffortRoot:   absRoot,
			ContractPath: promoteCoverage,
			Write:        writePromotion,
		})
		promotion := review.Promotion

		telemetry.LogPhase(rec, "product_tdd_behavioral_coverage_promoted", map[string]any{
			"status":           review.Status,
			"state":            promotion.State,
			"commit_status":    promotion.CommitStatus,
			"ledger_committed": promotion.LedgerCommitted,
			"commit_warning":   promotion.CommitWarning,
			"audit_status":     promotion.AuditStatus,
			"audit_error":      promotion.AuditError,
			"event_id":         promotion.EventID,
			"digest":           promotion.Digest,
			"superseded":       promotion.SupersededDigest,
			"finding_count":    len(review.Findings),
			"write":            writePromotion,
		})

		type promotionOutput struct {
			Status           productworkflow.ProductWorkflowStatus      `json:"status"`
			EffortRoot       string                                     `json:"effort_root"`
			PromoteCoverage  string                                     `json:"promote_coverage"`
			State            string                                     `json:"state"`
			CommitStatus     string                                     `json:"commit_status,omitempty"`
			LedgerCommitted  bool                                       `json:"ledger_committed"`
			CommitWarning    string                                     `json:"commit_warning,omitempty"`
			AuditStatus      string                                     `json:"audit_status"`
			AuditError       string                                     `json:"audit_error,omitempty"`
			EventID          string                                     `json:"event_id,omitempty"`
			SupersededDigest string                                     `json:"superseded_digest,omitempty"`
			Write            bool                                       `json:"write"`
			Digest           string                                     `json:"digest,omitempty"`
			Ledger           productworkflow.TDDLedger                  `json:"ledger"`
			Findings         []productworkflow.TDDObligationPlanFinding `json:"findings"`
		}

		out := promotionOutput{
			Status:           review.Status,
			EffortRoot:       absRoot,
			PromoteCoverage:  review.ContractPath,
			State:            promotion.State,
			CommitStatus:     promotion.CommitStatus,
			LedgerCommitted:  promotion.LedgerCommitted,
			CommitWarning:    promotion.CommitWarning,
			AuditStatus:      promotion.AuditStatus,
			AuditError:       promotion.AuditError,
			EventID:          promotion.EventID,
			SupersededDigest: promotion.SupersededDigest,
			Write:            writePromotion,
			Digest:           promotion.Digest,
			Ledger:           review.Ledger,
			Findings:         review.Findings,
		}
		if out.Digest == "" && review.Ledger.BehavioralCoverage != nil {
			out.Digest = productworkflow.BehavioralCoverageContractDigest(*review.Ledger.BehavioralCoverage)
		}
		if out.Findings == nil {
			out.Findings = []productworkflow.TDDObligationPlanFinding{}
		}
		if out.Ledger.Obligations == nil {
			out.Ledger.Obligations = []productworkflow.TDDObligation{}
		}

		if jsonOutput {
			enc := json.NewEncoder(stdout)
			enc.SetIndent("", "  ")

			if err := enc.Encode(out); err != nil {
				fmt.Fprintf(stderr, "product tdd: encode JSON: %v\n", err)

				return 1
			}
		} else {
			fmt.Fprintf(
				stdout,
				"product tdd promote-coverage: %s (%d findings, write=%v, commit=%s, audit=%s)\n",
				out.Status,
				len(out.Findings),
				out.Write,
				out.CommitStatus,
				out.AuditStatus,
			)
			if out.CommitWarning != "" {
				fmt.Fprintf(stdout, "commit warning: %s\n", out.CommitWarning)
			}
			if out.AuditError != "" {
				fmt.Fprintf(stdout, "audit degradation: %s\n", out.AuditError)
			}
		}

		if out.Status != productworkflow.ProductWorkflowStatusPass {
			return 1
		}

		return 0
	}

	planned := productworkflow.PlanTDDObligationsFromEffort(absRoot)
	plan := planned.Plan

	telemetry.LogPhase(rec, "product_tdd_plan_obligations", map[string]any{
		"status":           planned.Status,
		"finding_count":    len(plan.Findings),
		"obligation_count": len(plan.Obligations),
	})

	type tddOutput struct {
		Status     productworkflow.ProductWorkflowStatus      `json:"status"`
		EffortRoot string                                     `json:"effort_root"`
		Plan       productworkflow.TDDObligationSynthesisPlan `json:"plan"`
	}

	out := tddOutput{Status: planned.Status, EffortRoot: absRoot, Plan: plan}

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

	if out.Status != productworkflow.ProductWorkflowStatusPass {
		return 1
	}

	return 0
}

type productFeedbackOutput struct {
	Status          productworkflow.ProductWorkflowStatus    `json:"status"`
	EffortRoot      string                                   `json:"effort_root"`
	Summary         productworkflow.ExecutionFeedbackSummary `json:"summary"`
	NextValidAction string                                   `json:"next_valid_action"`
}

type productCoverageOutput struct {
	Status     productworkflow.ProductWorkflowStatus  `json:"status"`
	EffortRoot string                                 `json:"effort_root"`
	Evaluation productworkflow.CoverageGateEvaluation `json:"evaluation"`
}

type productFeedbackReconcileOutput struct {
	Status                productworkflow.ProductWorkflowStatus                 `json:"status"`
	EffortRoot            string                                                `json:"effort_root"`
	SummaryReferencePath  string                                                `json:"summary_reference_path"`
	ReconciledStateOutput string                                                `json:"reconciled_state_output"`
	Reconciliation        productworkflow.ExecutionFeedbackReconciliationResult `json:"reconciliation"`
	NextValidAction       string                                                `json:"next_valid_action"`
}

type productFeedbackRankerLearningOutput struct {
	Status                 productworkflow.ProductWorkflowStatus        `json:"status"`
	EffortRoot             string                                       `json:"effort_root"`
	SummaryReferencePath   string                                       `json:"summary_reference_path"`
	RankerLearningEvidence productworkflow.RankerLearningEvidenceResult `json:"ranker_learning_evidence"`
	NextValidAction        string                                       `json:"next_valid_action"`
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

	if rankerLearningMode {
		built, err := productworkflow.BuildRankerLearningEvidenceFromFeedback(productworkflow.RankerLearningFeedbackRequest{
			EffortRoot:  absRoot,
			SummaryPath: rankerLearningEvidenceSummary,
		})
		if err != nil {
			fmt.Fprintf(stderr, "product feedback: %v\n", err)

			return 1
		}
		adapted := built.Evidence
		telemetry.LogPhase(rec, "product_feedback_ranker_learning_evidence", map[string]any{
			"status":          adapted.Status,
			"finding_count":   len(adapted.Findings),
			"blocker_count":   len(adapted.Blockers),
			"next_valid_step": adapted.NextValidAction,
		})

		out := productFeedbackRankerLearningOutput{
			Status:                 adapted.Status,
			EffortRoot:             absRoot,
			SummaryReferencePath:   built.SummaryReferencePath,
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

		if out.Status != productworkflow.ProductWorkflowStatusPass {
			return 1
		}

		return 0
	}

	if reconcileMode {
		built, err := productworkflow.ReconcileExecutionFeedbackFromArtifacts(productworkflow.ExecutionFeedbackReconciliationRequest{
			EffortRoot:  absRoot,
			SummaryPath: reconcileSummary,
			OutputPath:  reconciledStateOutput,
		})
		if err != nil {
			fmt.Fprintf(stderr, "product feedback: %v\n", err)

			return 1
		}
		reconciled := built.Reconciliation
		out := productFeedbackReconcileOutput{
			Status:                reconciled.Status,
			EffortRoot:            absRoot,
			SummaryReferencePath:  built.SummaryReferencePath,
			ReconciledStateOutput: built.OutputPath,
			Reconciliation:        reconciled,
			NextValidAction:       reconciled.NextValidAction,
		}
		telemetry.LogPhase(rec, "product_feedback_reconcile", map[string]any{
			"status":          out.Status,
			"blocker_count":   len(out.Reconciliation.Blockers),
			"finding_count":   len(out.Reconciliation.Findings),
			"next_valid_step": out.NextValidAction,
		})

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

		if out.Status != productworkflow.ProductWorkflowStatusPass {
			return 1
		}

		return 0
	}

	summary, err := productworkflow.BuildExecutionFeedbackFromArtifacts(productworkflow.ExecutionFeedbackArtifactsRequest{
		EffortRoot:       absRoot,
		LaneHistoryPath:  laneHistoryFile,
		PlanProgressPath: planProgressFile,
		VerificationPath: verificationFile,
		ProductEvidence: productworkflow.EvidenceBundleRef{
			ID:   productEvidenceID,
			Path: productEvidencePath,
		},
		ReleaseEvidence: productworkflow.EvidenceBundleRef{
			ID:   releaseEvidenceID,
			Path: releaseEvidencePath,
		},
		ChangedPaths:      changedPaths.values,
		BriefPath:         briefPath,
		TDDObligationRefs: tddRefs.values,
	})
	if err != nil {
		fmt.Fprintf(stderr, "product feedback: %v\n", err)

		return 1
	}
	telemetry.LogPhase(rec, "product_feedback", map[string]any{
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

	if out.Status != productworkflow.ProductWorkflowStatusPass {
		return 1
	}

	return 0
}

// multiValueFlag is intentionally separate from cliflag.MultiFlag:
// it trims whitespace and rejects empty values, which cliflag.MultiFlag does not.
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

	out := productworkflow.RenderImplementationBriefFromEffort(productworkflow.BriefRenderRequest{
		EffortRoot: absRoot,
		OutputPath: briefOutput,
	})
	if out.CoverageEvent != nil {
		event := *out.CoverageEvent
		telemetry.LogPhase(rec, event.Event, map[string]any{
			"event":           event.Event,
			"effort_id":       event.EffortID,
			"task_id":         event.TaskID,
			"coverage":        event.Coverage,
			"gate_result":     event.GateResult,
			"blocked_reasons": event.BlockedReasons,
			"waivers":         event.Waivers,
			"source":          "product_brief_render",
		})
	}
	if out.Lint != nil {
		telemetry.LogPhase(rec, "brief_lint", map[string]any{"exit": out.Lint.Exit, "json": true, "findings": len(out.Lint.Findings)})
	}

	telemetry.LogPhase(rec, "product_brief_render", map[string]any{
		"status":        out.Status,
		"has_output":    out.BriefOutputPath != "",
		"blocker_count": len(out.Generated.Blockers),
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

		if out.Status != productworkflow.ProductWorkflowStatusPass {
			fmt.Fprintf(stdout, "blocker: %s\nnext: %s\n", out.Blocker, out.NextValidAction)
		} else if strings.TrimSpace(briefOutput) == "" {
			fmt.Fprintln(stdout, out.Generated.RenderedBrief)
		}
	}

	if out.Status != productworkflow.ProductWorkflowStatusPass {
		return 1
	}

	return 0
}

func runProductResearchPlanRuns(projectRoot, researchOutputDir, researchProfile string, jsonOutput bool, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	result, err := productworkflow.PlanResearchRunsFromProject(productworkflow.ResearchRunPlanningRequest{
		ProjectRoot:       projectRoot,
		ResearchOutputDir: researchOutputDir,
		Profile:           researchProfile,
	})
	if err != nil {
		fmt.Fprintf(stderr, "product research: %v\n", err)

		return 2
	}
	telemetry.LogPhase(rec, "product_research_plan_runs", map[string]any{
		"profile_count": result.Plan.ProfileCount,
		"run_count":     result.Plan.RunCount,
		"finding_count": len(result.Plan.Findings),
	})

	if jsonOutput {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")

		if err := enc.Encode(result.Plan); err != nil {
			fmt.Fprintf(stderr, "product research: encode JSON: %v\n", err)

			return 1
		}
	} else {
		fmt.Fprintf(stdout, "product research plan: %d runs (%d findings)\n", result.Plan.RunCount, len(result.Plan.Findings))
	}

	if result.Status != productworkflow.ProductWorkflowStatusPass {
		return 1
	}

	return 0
}

func runProductResearchRenderPrompts(projectRoot, effortRoot, researchOutputDir, researchPromptDir, researchProfile string, jsonOutput bool, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	result, err := productworkflow.RenderResearchPromptsFromProject(productworkflow.ResearchPromptRenderRequest{
		ProjectRoot:       projectRoot,
		EffortRoot:        effortRoot,
		ResearchOutputDir: researchOutputDir,
		ResearchPromptDir: researchPromptDir,
		Profile:           researchProfile,
	})
	if err != nil {
		fmt.Fprintf(stderr, "product research: %v\n", err)

		return 2
	}

	telemetry.LogPhase(rec, "product_research_render_prompts", map[string]any{
		"profile_count":      result.ProfileCount,
		"run_count":          result.Plan.RunCount,
		"finding_count":      len(result.Plan.Findings),
		"wrote_prompt_count": result.PromptWriteCount,
	})

	if jsonOutput {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")

		if err := enc.Encode(result); err != nil {
			fmt.Fprintf(stderr, "product research: encode JSON: %v\n", err)

			return 1
		}
	} else {
		fmt.Fprintf(stdout, "product research render-prompts: %s (%d prompts, %d findings)\n", result.Status, result.PromptWriteCount, len(result.Plan.Findings))
	}

	if result.Status != productworkflow.ProductWorkflowStatusPass {
		return 1
	}

	return 0
}

func runProductResearchMergeEvidence(projectRoot, effortRoot, researchOutputDir, researchProfile string, jsonOutput bool, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	result, err := productworkflow.MergeResearchEvidenceFromProject(productworkflow.ResearchEvidenceMergeRequest{
		ProjectRoot:       projectRoot,
		EffortRoot:        effortRoot,
		ResearchOutputDir: researchOutputDir,
		Profile:           researchProfile,
	})
	if err != nil {
		fmt.Fprintf(stderr, "product research: %v\n", err)

		return 2
	}

	telemetry.LogPhase(rec, "product_research_merge", map[string]any{
		"profile_count":      result.ProfileCount,
		"research_run_count": result.ResearchRunCount,
		"merged_card_count":  result.MergedCardCount,
		"conflict_count":     len(result.Conflicts),
		"finding_count":      len(result.Findings),
		"wrote_ledger":       result.WroteEvidenceLedger,
	})

	if jsonOutput {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")

		if err := enc.Encode(result); err != nil {
			fmt.Fprintf(stderr, "product research: encode JSON: %v\n", err)

			return 1
		}
	} else {
		fmt.Fprintf(stdout, "product research merge: %s (%d cards, %d conflicts, %d findings)\n", result.Status, result.MergedCardCount, len(result.Conflicts), len(result.Findings))
	}

	if result.Status != productworkflow.ProductWorkflowStatusPass {
		return 1
	}

	return 0
}
