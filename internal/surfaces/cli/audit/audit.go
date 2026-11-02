package audit

import (
	"bufio"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"idiom/internal/capabilities/workspace"
	plancontract "idiom/internal/contracts/plan"
	"idiom/internal/evidence/auditenum"
	"idiom/internal/evidence/telemetry"
	"idiom/internal/platform/config"
	"idiom/internal/platform/safepath"
	cmdconfig "idiom/internal/surfaces/cli/config"
)

const auditWatchPacketSchemaVersion = "idiom.audit_watch_packet.v1"

type auditWatchOptions struct {
	Config         string
	Paths          string
	OutputDir      string
	StatePath      string
	Interval       time.Duration
	Debounce       time.Duration
	Limit          int
	Once           bool
	ReviewExisting bool
	Quiet          bool
}

type auditWatchState struct {
	Initialized bool                          `json:"initialized"`
	Files       map[string]auditWatchSeenFile `json:"files"`
}

type auditWatchSeenFile struct {
	ModUnixNano int64 `json:"mod_unix_nano"`
	Size        int64 `json:"size"`
}

type auditWatchFile struct {
	Path        string
	DisplayPath string
	ModUnixNano int64
	Size        int64
}

type auditWatchChangedFile struct {
	File     auditWatchFile
	Previous auditWatchSeenFile
}

type auditWatchTriggerFile struct {
	Path           string                    `json:"path"`
	ModUnixNano    int64                     `json:"mod_unix_nano"`
	Size           int64                     `json:"size"`
	Reasons        []string                  `json:"reasons"`
	Sessions       []string                  `json:"sessions,omitempty"`
	EventCount     int                       `json:"event_count"`
	RecoveryEvents []auditWatchRecoveryEvent `json:"recovery_events,omitempty"`
}

type auditWatchReview struct {
	SchemaVersion             string                                `json:"schema"`
	GeneratedAt               string                                `json:"generated_at"`
	WorkspaceRoot             string                                `json:"workspace_root"`
	TriggerFiles              []auditWatchTriggerFile               `json:"trigger_files"`
	TriggerReasons            []string                              `json:"trigger_reasons,omitempty"`
	Sessions                  []string                              `json:"sessions,omitempty"`
	Audits                    []auditWatchRun                       `json:"audits"`
	Findings                  []auditWatchFinding                   `json:"findings,omitempty"`
	TopFailureClass           string                                `json:"top_failure_class,omitempty"`
	CandidateRegressions      []string                              `json:"candidate_regressions,omitempty"`
	CandidateLearningEvents   []string                              `json:"candidate_learning_events,omitempty"`
	OwnerSurface              string                                `json:"owner_surface,omitempty"`
	Recovery                  *auditWatchRecoverySummary            `json:"recovery,omitempty"`
	Status                    string                                `json:"status"`
	PromotionStatus           string                                `json:"promotion_status"`
	Reproduce                 auditWatchReproducer                  `json:"reproduce"`
	Closure                   auditWatchClosure                     `json:"closure"`
	RegressionPromotion       auditWatchRegressionPromotionWorkflow `json:"regression_promotion"`
	ClientClassifications     []auditWatchClientClassificationRow   `json:"client_classifications"`
	ProductSuccessGateImpacts []auditWatchProductSuccessGateImpact  `json:"product_success_gate_impacts"`
	Privacy                   auditWatchEvidencePrivacy             `json:"privacy"`
}

type auditWatchRecoveryEvent struct {
	Type            string `json:"type"`
	Status          string `json:"status"`
	SessionID       string `json:"session_id,omitempty"`
	FailureID       string `json:"failure_id,omitempty"`
	FailureClass    string `json:"failure_class,omitempty"`
	FailureKind     string `json:"failure_kind,omitempty"`
	PacketID        string `json:"packet_id,omitempty"`
	CandidateAtoms  int    `json:"candidate_atoms,omitempty"`
	NextValidAction string `json:"next_valid_action,omitempty"`
	ArtifactPath    string `json:"artifact_path,omitempty"`
}

type auditWatchRecoverySummary struct {
	Status                   string   `json:"status"`
	EventCount               int      `json:"event_count"`
	FailureRecordCount       int      `json:"failure_record_count"`
	DecompositionPacketCount int      `json:"decomposition_packet_count"`
	FailedRecoveryEventCount int      `json:"failed_recovery_event_count,omitempty"`
	CandidateAtomCount       int      `json:"candidate_atom_count,omitempty"`
	FailureClasses           []string `json:"failure_classes,omitempty"`
	NextValidActions         []string `json:"next_valid_actions,omitempty"`
	Sessions                 []string `json:"sessions,omitempty"`
	ArtifactPaths            []string `json:"artifact_paths,omitempty"`
}

type auditWatchRun struct {
	Path    string          `json:"path"`
	Summary string          `json:"summary"`
	Error   string          `json:"error,omitempty"`
	Report  json.RawMessage `json:"report,omitempty"`
}

type auditWatchFinding struct {
	ID             string                     `json:"id"`
	Severity       string                     `json:"severity"`
	Category       string                     `json:"category"`
	Source         string                     `json:"source"`
	Evidence       string                     `json:"evidence"`
	Recommendation string                     `json:"recommendation,omitempty"`
	Tools          []string                   `json:"tools,omitempty"`
	OwnerSurface   string                     `json:"owner_surface"`
	Status         string                     `json:"status"`
	Promotion      auditWatchFindingPromotion `json:"promotion"`
	Reproducer     auditWatchReproducer       `json:"reproducer"`
	Closure        auditWatchClosure          `json:"closure"`
}

type auditWatchFindingPromotion struct {
	Status           string   `json:"status"`
	CandidateTargets []string `json:"candidate_targets"`
	RequiredDecision string   `json:"required_decision"`
	SourceEvidence   []string `json:"source_evidence,omitempty"`
}

type auditWatchRegressionPromotionWorkflow struct {
	Status           string   `json:"status"`
	CandidateTargets []string `json:"candidate_targets"`
	RequiredDecision string   `json:"required_decision"`
	ClosureRule      string   `json:"closure_rule"`
}

type auditWatchReproducer struct {
	AuditCommand          string   `json:"audit_command"`
	ReplayCommandTemplate string   `json:"replay_command_template,omitempty"`
	Evidence              []string `json:"evidence,omitempty"`
}

type auditWatchClosure struct {
	Status           string                     `json:"status"`
	Ready            bool                       `json:"ready"`
	Required         bool                       `json:"required"`
	Reason           string                     `json:"reason"`
	BeforeReplay     []auditWatchReplayEvidence `json:"before_replay,omitempty"`
	AfterReplay      []auditWatchReplayEvidence `json:"after_replay,omitempty"`
	NoReplayReason   string                     `json:"no_replay_reason,omitempty"`
	RequiredEvidence []string                   `json:"required_evidence,omitempty"`
}

type auditWatchReplayEvidence struct {
	Command      string `json:"command"`
	EvidencePath string `json:"evidence_path,omitempty"`
	Result       string `json:"result"`
}

type auditWatchClientClassificationRow struct {
	ID           string `json:"id"`
	Label        string `json:"label"`
	Status       string `json:"status"`
	Evidence     string `json:"evidence"`
	ReviewFocus  string `json:"review_focus"`
	ExampleCheck string `json:"example_check"`
}

type auditWatchProductSuccessGateImpact struct {
	Gate     string `json:"gate"`
	Status   string `json:"status"`
	Evidence string `json:"evidence"`
}

type auditWatchEvidencePrivacy struct {
	EvidencePolicy      string `json:"evidence_policy"`
	PromptTextIncluded  bool   `json:"prompt_text_included"`
	FileBodiesIncluded  bool   `json:"file_bodies_included"`
	LogExcerptsIncluded bool   `json:"log_excerpts_included"`
	SecretsIncluded     bool   `json:"secrets_included"`
}

var defaultAuditWatchPaths = []string{
	".idiom/logs/conversations",
	"logs/conversations",
	".idiom/logs/e2e",
	"logs/e2e",
	"logs/telemetry",
}

func RunMainWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		printAuditUsage(stderr)

		return 0
	}

	telemetry.LogPhase(rec, "audit_start", map[string]any{"subcommand": args[0]})

	switch args[0] {
	case "watch":
		return runAuditWatch(args[1:], stdout, stderr, rec)
	case "enum-mirrors":
		return auditenum.Run(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown audit command: %s\n", args[0])
		printAuditUsage(stderr)

		return 2
	}
}

func runAuditWatch(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	if auditWatchHelpRequested(args) {
		printAuditUsage(stderr)

		return 0
	}

	opts := auditWatchOptions{
		Config:   "config.json",
		Paths:    strings.Join(defaultAuditWatchPaths, ","),
		Interval: 5 * time.Second,
		Debounce: 30 * time.Second,
		Limit:    50,
	}
	fs := flag.NewFlagSet("idiom audit watch", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&opts.Config, "config", opts.Config, "path to config file")
	fs.StringVar(&opts.Paths, "paths", opts.Paths, "comma-separated log files/directories to watch")
	fs.StringVar(&opts.OutputDir, "output", "", "directory for audit review JSON artifacts; default .idiom/audits/watch")
	fs.StringVar(&opts.StatePath, "state", "", "state file; default <output>/state.json")
	fs.DurationVar(&opts.Interval, "interval", opts.Interval, "watch polling interval")
	fs.DurationVar(&opts.Debounce, "debounce", opts.Debounce, "delay after a trigger before auditing, to let active sessions close")
	fs.IntVar(&opts.Limit, "limit", opts.Limit, "harness_audit log limit per watched path")
	fs.BoolVar(&opts.Once, "once", false, "scan once and exit")
	fs.BoolVar(&opts.ReviewExisting, "review-existing", false, "on first run, review existing trigger files instead of creating a baseline")
	fs.BoolVar(&opts.Quiet, "quiet", false, "suppress status output")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	if fs.NArg() != 0 {
		fmt.Fprintf(stderr, "unexpected positional argument: %s\n", fs.Arg(0))

		return 2
	}

	cfg, err := cmdconfig.Load(opts.Config)
	if err != nil {
		fmt.Fprintln(stderr, err)

		return 1
	}

	opts = auditWatchOptionsWithDefaults(opts, cfg)

	state, err := loadAuditWatchState(opts.StatePath)
	if err != nil {
		fmt.Fprintln(stderr, err)

		return 1
	}

	if err := os.MkdirAll(opts.OutputDir, 0o755); err != nil {
		fmt.Fprintf(stderr, "create audit watch output: %v\n", err)

		return 1
	}

	for {
		reviewPath, triggers, err := auditWatchScanAndReview(context.Background(), cfg, opts, &state)
		if err != nil {
			fmt.Fprintln(stderr, err)

			return 1
		}

		if err := saveAuditWatchState(opts.StatePath, state); err != nil {
			fmt.Fprintln(stderr, err)

			return 1
		}

		if reviewPath != "" {
			telemetry.LogPhase(rec, "audit_watch_review", map[string]any{"artifact": reviewPath, "trigger_files": len(triggers)})

			if !opts.Quiet {
				fmt.Fprintf(stdout, "audit review: %s\n", reviewPath)
			}
		} else if opts.Once && !opts.Quiet {
			fmt.Fprintln(stdout, "audit review: no triggers")
		}

		if opts.Once {
			return 0
		}

		time.Sleep(opts.Interval)
	}
}

func auditWatchOptionsWithDefaults(opts auditWatchOptions, cfg config.Config) auditWatchOptions {
	root := strings.TrimSpace(cfg.Workspace.Root)
	if root == "" {
		root = "."
	}

	if strings.TrimSpace(opts.OutputDir) == "" {
		opts.OutputDir = filepath.Join(root, ".idiom", "audits", "watch")
	} else if !filepath.IsAbs(opts.OutputDir) {
		opts.OutputDir = filepath.Join(root, opts.OutputDir)
	}

	if strings.TrimSpace(opts.StatePath) == "" {
		opts.StatePath = filepath.Join(opts.OutputDir, "state.json")
	} else if !filepath.IsAbs(opts.StatePath) {
		opts.StatePath = filepath.Join(root, opts.StatePath)
	}

	if opts.Limit <= 0 {
		opts.Limit = 50
	}

	if opts.Interval <= 0 {
		opts.Interval = 5 * time.Second
	}

	return opts
}

func auditWatchScanAndReview(ctx context.Context, cfg config.Config, opts auditWatchOptions, state *auditWatchState) (string, []auditWatchTriggerFile, error) {
	files, err := collectAuditWatchFiles(cfg.Workspace.Root, auditWatchPathList(opts.Paths))
	if err != nil {
		return "", nil, err
	}

	if state.Files == nil {
		state.Files = make(map[string]auditWatchSeenFile)
	}

	if !state.Initialized && !opts.ReviewExisting {
		auditWatchMarkFilesSeen(state, files)

		state.Initialized = true

		return "", nil, nil
	}

	var changed []auditWatchChangedFile

	for _, file := range files {
		previous := state.Files[file.Path]
		if previous.ModUnixNano != file.ModUnixNano || previous.Size != file.Size {
			changed = append(changed, auditWatchChangedFile{File: file, Previous: previous})
		}
	}

	auditWatchMarkFilesSeen(state, files)

	state.Initialized = true

	if len(changed) == 0 {
		return "", nil, nil
	}

	var triggers []auditWatchTriggerFile

	for _, changedFile := range changed {
		trigger, ok := auditWatchTriggerForFile(changedFile.File, changedFile.Previous)
		if ok {
			triggers = append(triggers, trigger)
		}
	}

	if len(triggers) == 0 {
		return "", nil, nil
	}

	if opts.Debounce > 0 {
		time.Sleep(opts.Debounce)
	}

	review, err := buildAuditWatchReview(ctx, cfg, opts, triggers)
	if err != nil {
		return "", triggers, err
	}

	path, err := writeAuditWatchReview(opts.OutputDir, review)
	if err != nil {
		return "", triggers, err
	}

	return path, triggers, nil
}

func buildAuditWatchReview(ctx context.Context, cfg config.Config, opts auditWatchOptions, triggers []auditWatchTriggerFile) (auditWatchReview, error) {
	mgr := workspace.NewManager(cfg.Workspace)
	paths := auditWatchPathList(opts.Paths)
	review := auditWatchReview{
		SchemaVersion:             auditWatchPacketSchemaVersion,
		GeneratedAt:               time.Now().UTC().Format(time.RFC3339Nano),
		WorkspaceRoot:             auditWatchSanitizeText(cfg.Workspace.Root, cfg.Workspace.Root, 128),
		TriggerFiles:              triggers,
		ClientClassifications:     auditWatchClientClassificationRows(),
		ProductSuccessGateImpacts: auditWatchProductSuccessGateImpacts(),
		Privacy:                   auditWatchPrivacyPolicy(),
	}

	for _, path := range paths {
		if !auditWatchPathExists(cfg.Workspace.Root, path) {
			continue
		}

		result := mgr.Execute(ctx, workspace.ToolRequest{
			Tool:    "harness_audit",
			Purpose: "behavior",
			Path:    path,
			Limit:   opts.Limit,
		})
		run := auditWatchRun{
			Path:    auditWatchSanitizeText(cfg.Workspace.Root, path, 240),
			Summary: auditWatchSanitizeText(cfg.Workspace.Root, result.Summary, 400),
			Error:   auditWatchSanitizeText(cfg.Workspace.Root, result.Error, 400),
		}

		if strings.TrimSpace(result.Output) != "" && json.Valid([]byte(result.Output)) {
			run.Report = auditWatchSanitizeJSONReport(cfg.Workspace.Root, result.Output)
		}

		review.Audits = append(review.Audits, run)
	}

	auditWatchFinalizeReview(&review, opts, cfg.Workspace.Root)

	return review, nil
}

func auditWatchFinalizeReview(review *auditWatchReview, opts auditWatchOptions, root string) {
	review.TriggerFiles = auditWatchSanitizeTriggerFiles(root, review.TriggerFiles)
	review.TriggerReasons = auditWatchTriggerReasonList(review.TriggerFiles)
	review.Sessions = auditWatchSessionList(review.TriggerFiles)
	review.Recovery = auditWatchRecoverySummaryFromTriggers(review.TriggerFiles)
	review.Findings = auditWatchDeduplicateFindings(append(
		auditWatchFindingsFromTriggers(review.TriggerFiles, opts, root),
		auditWatchFindingsFromAuditRuns(review.Audits, opts, root)...,
	))
	review.TopFailureClass = auditWatchTopFailureClass(review.Findings)
	review.OwnerSurface = auditWatchDominantOwnerSurface(review.Findings)
	review.Status = auditWatchPacketStatus(review.Findings)
	review.PromotionStatus = auditWatchPacketPromotionStatus(review.Findings)
	review.CandidateRegressions = auditWatchCandidateRegressionIDs(review.Findings)
	review.CandidateLearningEvents = auditWatchCandidateLearningEventIDs(review.Findings)
	review.Reproduce = auditWatchPacketReproducer(opts, review.TriggerFiles)
	review.Closure = auditWatchClosureForStatus(review.Status, nil, nil, "")
	review.RegressionPromotion = auditWatchRegressionPromotionWorkflowForFindings(review.Findings)
}

func auditWatchFindingsFromTriggers(triggers []auditWatchTriggerFile, opts auditWatchOptions, root string) []auditWatchFinding {
	var findings []auditWatchFinding

	for _, trigger := range triggers {
		for _, reason := range trigger.Reasons {
			category := auditWatchCategoryForReason(reason)
			source := auditWatchSanitizeText(root, trigger.Path, 240)
			evidence := fmt.Sprintf("trigger_file=%s; reason=%s; events=%d", source, reason, trigger.EventCount)
			recommendation := "triage the owner surface and promote the smallest reproducible check or document a bounded non-goal"

			if category == "failure_recovery" {
				recommendation = "inspect the recovery packet, promote the smallest gated atom, and convert repeats into eval or audit fixtures"
			}

			findings = append(findings, auditWatchNewFinding(auditWatchFinding{
				ID:             "trigger_" + auditWatchSlug(reason, 72),
				Severity:       auditWatchSeverityForReason(reason),
				Category:       category,
				Source:         source,
				Evidence:       evidence,
				Recommendation: recommendation,
				OwnerSurface:   auditWatchOwnerSurfaceForCategory(category),
			}, opts))
		}
	}

	return findings
}

func auditWatchFindingsFromAuditRuns(runs []auditWatchRun, opts auditWatchOptions, root string) []auditWatchFinding {
	type sourceFinding struct {
		ID             string   `json:"id"`
		Severity       string   `json:"severity"`
		Evidence       string   `json:"evidence"`
		Recommendation string   `json:"recommendation"`
		Tools          []string `json:"tools"`
	}

	type sourceReport struct {
		Findings []sourceFinding `json:"findings"`
	}

	var findings []auditWatchFinding

	for _, run := range runs {
		if len(run.Report) == 0 {
			continue
		}

		var report sourceReport

		if err := json.Unmarshal(run.Report, &report); err != nil {
			continue
		}

		for _, source := range report.Findings {
			severity := auditWatchSeverity(source.Severity)
			if severity != "high" && severity != "medium" {
				continue
			}

			id := strings.TrimSpace(source.ID)
			if id == "" {
				id = "harness_audit_finding"
			}

			category := auditWatchCategoryForAuditFinding(id, source.Evidence)

			findings = append(findings, auditWatchNewFinding(auditWatchFinding{
				ID:             "audit_" + auditWatchSlug(id, 72),
				Severity:       severity,
				Category:       category,
				Source:         auditWatchSanitizeText(root, run.Path, 240),
				Evidence:       auditWatchSanitizeText(root, source.Evidence, 500),
				Recommendation: auditWatchSanitizeText(root, source.Recommendation, 500),
				Tools:          auditWatchSanitizeStrings(root, source.Tools, 120),
				OwnerSurface:   auditWatchOwnerSurfaceForCategory(category),
			}, opts))
		}
	}

	return findings
}

func auditWatchNewFinding(finding auditWatchFinding, opts auditWatchOptions) auditWatchFinding {
	finding.ID = auditWatchSlug(firstAuditWatchString(finding.ID, "audit_watch_finding"), 96)
	finding.Severity = auditWatchSeverity(finding.Severity)
	finding.Category = firstAuditWatchString(finding.Category, "audit_coverage")
	finding.OwnerSurface = firstAuditWatchString(finding.OwnerSurface, auditWatchOwnerSurfaceForCategory(finding.Category))
	finding.Status = "open"
	finding.Promotion = auditWatchPromotionForFinding(finding, opts)
	finding.Reproducer = auditWatchFindingReproducer(finding.Source, opts)
	finding.Closure = auditWatchClosureForStatus(finding.Status, nil, nil, "")

	return finding
}

func auditWatchPromotionForFinding(finding auditWatchFinding, opts auditWatchOptions) auditWatchFindingPromotion {
	targets := []string{"regression_test", "eval_fixture", "audit_rule", "documented_non_goal"}

	switch finding.Category {
	case "context_staleness", "ranker_miss":
		targets = append(targets, "ranker_change")
	case "completion_contract", "verification_gap", "tool_loop", "tool_schema":
		targets = append(targets, "prompt_contract_fix")
	case "telemetry_gap", "audit_coverage":
		targets = append(targets, "telemetry_or_audit_rule")
	case "failure_recovery":
		targets = append(targets, "recovery_atom", "recovery_eval_fixture", "telemetry_or_audit_rule")
	}

	slices.Sort(targets)

	return auditWatchFindingPromotion{
		Status:           "open",
		CandidateTargets: auditWatchUniqueStrings(targets),
		RequiredDecision: "promote_to_regression_eval_audit_ranker_or_contract_fix_or_document_bounded_non_goal",
		SourceEvidence:   auditWatchNonEmptyStrings([]string{finding.Source, auditWatchHarnessAuditCommand(finding.Source, opts.Limit)}),
	}
}

func auditWatchFindingReproducer(source string, opts auditWatchOptions) auditWatchReproducer {
	return auditWatchReproducer{
		AuditCommand:          auditWatchHarnessAuditCommand(source, opts.Limit),
		ReplayCommandTemplate: "go run ./cmd/idiom-eval -log <source-log> -scenario <promoted-scenario>",
		Evidence:              auditWatchNonEmptyStrings([]string{source}),
	}
}

func auditWatchPacketReproducer(opts auditWatchOptions, triggers []auditWatchTriggerFile) auditWatchReproducer {
	return auditWatchReproducer{
		AuditCommand:          auditWatchHarnessAuditCommand("", opts.Limit),
		ReplayCommandTemplate: "go run ./cmd/idiom-eval -log <source-log> -scenario <promoted-scenario>",
		Evidence:              auditWatchTriggerPaths(triggers),
	}
}

func auditWatchHarnessAuditCommand(path string, limit int) string {
	input := map[string]any{
		"purpose": "behavior",
	}
	if strings.TrimSpace(path) != "" {
		input["path"] = path
	}

	if limit > 0 {
		input["limit"] = limit
	}

	data, err := json.Marshal(input)
	if err != nil {
		return "idiom tool harness_audit"
	}

	return "idiom tool harness_audit --input " + auditWatchShellSingleQuote(string(data))
}

func auditWatchRegressionPromotionWorkflowForFindings(findings []auditWatchFinding) auditWatchRegressionPromotionWorkflow {
	targets := make([]string, 0)
	for _, finding := range findings {
		targets = append(targets, finding.Promotion.CandidateTargets...)
	}

	status := "not_applicable"
	if len(findings) > 0 {
		status = "open"
	}

	return auditWatchRegressionPromotionWorkflow{
		Status:           status,
		CandidateTargets: auditWatchUniqueStrings(targets),
		RequiredDecision: "each high_or_medium finding must become a regression test, eval fixture, audit rule, ranker change, prompt/contract fix, or documented non-goal",
		ClosureRule:      "closed findings require before/after replay evidence or a bounded replay-not-applicable reason",
	}
}

func auditWatchClosureForStatus(status string, before []auditWatchReplayEvidence, after []auditWatchReplayEvidence, noReplayReason string) auditWatchClosure {
	status = strings.ToLower(strings.TrimSpace(status))

	closure := auditWatchClosure{
		Status:           "open",
		Required:         true,
		Reason:           "before_after_replay_required",
		BeforeReplay:     before,
		AfterReplay:      after,
		NoReplayReason:   strings.TrimSpace(noReplayReason),
		RequiredEvidence: []string{"before_replay", "after_replay", "promotion_or_non_goal_decision"},
	}

	if status == "" || status == "open" {
		return closure
	}

	if status == "no_action" || status == "not_applicable" {
		return auditWatchClosure{
			Status:   "not_required",
			Ready:    true,
			Required: false,
			Reason:   "no_high_or_medium_findings",
		}
	}

	if !auditWatchClosedFindingStatus(status) {
		closure.Reason = "status_is_not_terminal"
		return closure
	}

	if len(before) > 0 && len(after) > 0 {
		closure.Status = "closed"
		closure.Ready = true
		closure.Reason = "before_after_replay_present"

		return closure
	}

	if auditWatchBoundedNoReplayReason(noReplayReason) {
		closure.Status = "closed"
		closure.Ready = true
		closure.Reason = "replay_not_applicable_documented"

		return closure
	}

	closure.Reason = "closed_status_requires_before_after_replay_or_bounded_reason"

	return closure
}

func auditWatchClosedFindingStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "fixed", "promoted", "declined", "non_goal":
		return true
	default:
		return false
	}
}

func auditWatchBoundedNoReplayReason(reason string) bool {
	switch strings.ToLower(strings.TrimSpace(reason)) {
	case "live_only_failure", "environment_unavailable", "nondeterministic_external_dependency", "source_log_unavailable", "documented_non_goal":
		return true
	default:
		return false
	}
}

func auditWatchDeduplicateFindings(findings []auditWatchFinding) []auditWatchFinding {
	seen := make(map[string]struct{}, len(findings))
	out := make([]auditWatchFinding, 0, len(findings))

	for _, finding := range findings {
		key := finding.ID + "\x00" + finding.Source
		if _, ok := seen[key]; ok {
			continue
		}

		seen[key] = struct{}{}

		out = append(out, finding)
	}

	sort.Slice(out, func(i, j int) bool {
		if auditWatchSeverityRank(out[i].Severity) != auditWatchSeverityRank(out[j].Severity) {
			return auditWatchSeverityRank(out[i].Severity) < auditWatchSeverityRank(out[j].Severity)
		}

		if out[i].Category != out[j].Category {
			return out[i].Category < out[j].Category
		}

		return out[i].ID < out[j].ID
	})

	return out
}

func auditWatchTopFailureClass(findings []auditWatchFinding) string {
	if len(findings) == 0 {
		return ""
	}

	return findings[0].Category
}

func auditWatchDominantOwnerSurface(findings []auditWatchFinding) string {
	if len(findings) == 0 {
		return ""
	}

	return findings[0].OwnerSurface
}

func auditWatchPacketStatus(findings []auditWatchFinding) string {
	if len(findings) == 0 {
		return "no_action"
	}

	return "open"
}

func auditWatchPacketPromotionStatus(findings []auditWatchFinding) string {
	if len(findings) == 0 {
		return "not_applicable"
	}

	return "open"
}

func auditWatchCandidateRegressionIDs(findings []auditWatchFinding) []string {
	out := make([]string, 0, len(findings))
	for _, finding := range findings {
		out = append(out, "regression-"+auditWatchSlug(finding.ID, 80))
	}

	return auditWatchUniqueStrings(out)
}

func auditWatchCandidateLearningEventIDs(findings []auditWatchFinding) []string {
	out := make([]string, 0, len(findings))
	for _, finding := range findings {
		if finding.Category == "context_staleness" || finding.Category == "ranker_miss" || finding.OwnerSurface == "ranker" {
			out = append(out, "learning-"+auditWatchSlug(finding.ID, 80))
		}
	}

	return auditWatchUniqueStrings(out)
}

func auditWatchTriggerReasonList(triggers []auditWatchTriggerFile) []string {
	var values []string

	for _, trigger := range triggers {
		values = append(values, trigger.Reasons...)
	}

	return auditWatchUniqueStrings(values)
}

func auditWatchSessionList(triggers []auditWatchTriggerFile) []string {
	var values []string

	for _, trigger := range triggers {
		values = append(values, trigger.Sessions...)
	}

	return auditWatchUniqueStrings(values)
}

func auditWatchTriggerPaths(triggers []auditWatchTriggerFile) []string {
	values := make([]string, 0, len(triggers))
	for _, trigger := range triggers {
		values = append(values, trigger.Path)
	}

	return auditWatchUniqueStrings(values)
}

func auditWatchRecoverySummaryFromTriggers(triggers []auditWatchTriggerFile) *auditWatchRecoverySummary {
	summary := auditWatchRecoverySummary{
		Status: "ready",
	}

	var (
		classes       []string
		actions       []string
		sessions      []string
		artifactPaths []string
	)

	for _, trigger := range triggers {
		for _, event := range trigger.RecoveryEvents {
			summary.EventCount++

			switch event.Type {
			case plancontract.PlanExecuteEventName(plancontract.PlanExecuteEventRecoveryFailureRecordWritten):
				summary.FailureRecordCount++
			case plancontract.PlanExecuteEventName(plancontract.PlanExecuteEventRecoveryDecompositionWritten):
				summary.DecompositionPacketCount++

				summary.CandidateAtomCount += event.CandidateAtoms
			case plancontract.PlanExecuteEventName(plancontract.PlanExecuteEventRecoveryFailureRecordFailed), plancontract.PlanExecuteEventName(plancontract.PlanExecuteEventRecoveryDecompositionFailed):
				summary.FailedRecoveryEventCount++

				summary.Status = "blocked"
			}

			classes = append(classes, event.FailureClass)
			actions = append(actions, event.NextValidAction)
			sessions = append(sessions, event.SessionID)
			artifactPaths = append(artifactPaths, event.ArtifactPath)
		}
	}

	if summary.EventCount == 0 {
		return nil
	}

	if summary.Status != "blocked" && summary.DecompositionPacketCount == 0 {
		summary.Status = "recorded"
	}

	summary.FailureClasses = auditWatchUniqueStrings(classes)
	summary.NextValidActions = auditWatchUniqueStrings(actions)
	summary.Sessions = auditWatchUniqueStrings(sessions)
	summary.ArtifactPaths = auditWatchUniqueStrings(artifactPaths)

	return &summary
}

func auditWatchSanitizeTriggerFiles(root string, triggers []auditWatchTriggerFile) []auditWatchTriggerFile {
	out := make([]auditWatchTriggerFile, 0, len(triggers))
	for _, trigger := range triggers {
		trigger.Path = auditWatchSanitizeText(root, trigger.Path, 240)
		trigger.Reasons = auditWatchSanitizeStrings(root, trigger.Reasons, 160)
		trigger.Sessions = auditWatchSanitizeStrings(root, trigger.Sessions, 160)
		trigger.RecoveryEvents = auditWatchSanitizeRecoveryEvents(root, trigger.RecoveryEvents)
		out = append(out, trigger)
	}

	return out
}

func auditWatchSanitizeRecoveryEvents(root string, events []auditWatchRecoveryEvent) []auditWatchRecoveryEvent {
	out := make([]auditWatchRecoveryEvent, 0, len(events))
	for _, event := range events {
		event.Type = auditWatchSanitizeText(root, event.Type, 120)
		event.Status = auditWatchSanitizeText(root, event.Status, 80)
		event.SessionID = auditWatchSanitizeText(root, event.SessionID, 160)
		event.FailureID = auditWatchSanitizeText(root, event.FailureID, 160)
		event.FailureClass = auditWatchSanitizeText(root, event.FailureClass, 120)
		event.FailureKind = auditWatchSanitizeText(root, event.FailureKind, 120)
		event.PacketID = auditWatchSanitizeText(root, event.PacketID, 160)
		event.NextValidAction = auditWatchSanitizeText(root, event.NextValidAction, 120)
		event.ArtifactPath = auditWatchSanitizeText(root, event.ArtifactPath, 240)
		out = append(out, event)
	}

	return out
}

func auditWatchSeverity(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "high", "medium", "low", "info":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "medium"
	}
}

func auditWatchSeverityRank(severity string) int {
	switch auditWatchSeverity(severity) {
	case "high":
		return 0
	case "medium":
		return 1
	case "low":
		return 2
	default:
		return 3
	}
}

func auditWatchSeverityForReason(reason string) string {
	lower := strings.ToLower(strings.TrimSpace(reason))
	switch {
	case strings.Contains(lower, "recovery_artifact_write_failed"):
		return "high"
	case strings.Contains(lower, "completion_gate"),
		strings.Contains(lower, "completion_status_blocked"),
		strings.Contains(lower, "required_paths_remaining"),
		strings.Contains(lower, "source_edit_verification_required"),
		strings.Contains(lower, "process_failed"),
		strings.Contains(lower, "high_risk_stop_reason"):
		return "high"
	default:
		return "medium"
	}
}

func auditWatchCategoryForReason(reason string) string {
	lower := strings.ToLower(strings.TrimSpace(reason))
	switch {
	case strings.Contains(lower, "recovery_"):
		return "failure_recovery"
	case strings.Contains(lower, "completion_gate"), strings.Contains(lower, "completion_status_blocked"), strings.Contains(lower, "required_paths_remaining"):
		return "completion_contract"
	case strings.Contains(lower, "verification_required"):
		return "verification_gap"
	case strings.Contains(lower, "malformed"), strings.Contains(lower, "tool_input"):
		return "tool_schema"
	case strings.Contains(lower, "stale_context"):
		return "context_staleness"
	case strings.Contains(lower, "tool_step_budget"), strings.Contains(lower, "repeated"):
		return "tool_loop"
	case strings.Contains(lower, "process_failed"):
		return "telemetry_gap"
	default:
		return "audit_coverage"
	}
}

func auditWatchCategoryForAuditFinding(id string, evidence string) string {
	lower := strings.ToLower(id + " " + evidence)
	switch {
	case strings.Contains(lower, "completion"), strings.Contains(lower, "contract"):
		return "completion_contract"
	case strings.Contains(lower, "verification"):
		return "verification_gap"
	case strings.Contains(lower, "stale"), strings.Contains(lower, "freshness"), strings.Contains(lower, "cache"):
		return "context_staleness"
	case strings.Contains(lower, "ranker"), strings.Contains(lower, "learning"):
		return "ranker_miss"
	case strings.Contains(lower, "tool"), strings.Contains(lower, "schema"):
		return "tool_schema"
	case strings.Contains(lower, "telemetry"), strings.Contains(lower, "log"):
		return "telemetry_gap"
	default:
		return "audit_coverage"
	}
}

func auditWatchOwnerSurfaceForCategory(category string) string {
	switch category {
	case "failure_recovery":
		return "recovery"
	case "completion_contract":
		return "contract"
	case "verification_gap":
		return "verification"
	case "context_staleness":
		return "context_freshness"
	case "ranker_miss":
		return "ranker"
	case "tool_schema", "tool_loop":
		return "tool_schema"
	case "telemetry_gap":
		return "telemetry"
	default:
		return "audit_coverage"
	}
}

func auditWatchClientClassificationRows() []auditWatchClientClassificationRow {
	return []auditWatchClientClassificationRow{
		{ID: "finance", Label: "finance", Status: "covered", Evidence: "owner_status_replay_packet", ReviewFocus: "deterministic evidence without account or transaction content", ExampleCheck: "high_or_medium findings expose owner/status/reproducer and closure requirements"},
		{ID: "healthcare_life_sciences", Label: "healthcare/life sciences", Status: "covered", Evidence: "privacy_bounded_audit_packet", ReviewFocus: "no raw PHI-like prompt, transcript, or file-body evidence", ExampleCheck: "privacy flags remain false and embedded audit strings are redacted"},
		{ID: "public_sector", Label: "public-sector", Status: "covered", Evidence: "replayable_closure_contract", ReviewFocus: "procurement-friendly closure evidence and non-goal reasons", ExampleCheck: "terminal statuses require before/after replay or bounded no-replay reason"},
		{ID: "enterprise_saas", Label: "enterprise SaaS", Status: "covered", Evidence: "regression_promotion_workflow", ReviewFocus: "failed runs become owned checks before repeated team use", ExampleCheck: "candidate targets include regression, eval, audit, contract, and non-goal paths"},
		{ID: "tooling_vendor", Label: "tooling-vendor", Status: "covered", Evidence: "schema_versioned_packet", ReviewFocus: "stable local packet fields for sidecar/tool integrations", ExampleCheck: "packet schema and client rows are deterministic"},
		{ID: "local_model_private_code", Label: "local-model/private-code", Status: "covered", Evidence: "local_reproducer_templates", ReviewFocus: "local audit and replay commands without external service dependency", ExampleCheck: "reproducer commands use idiom local CLI surfaces"},
		{ID: "open_source", Label: "open-source", Status: "covered", Evidence: "transparent_triage_queue", ReviewFocus: "contributors can inspect why a finding is open", ExampleCheck: "packet carries readable owner, promotion, and closure fields"},
	}
}

func auditWatchProductSuccessGateImpacts() []auditWatchProductSuccessGateImpact {
	return []auditWatchProductSuccessGateImpact{
		{Gate: "security_privacy", Status: "strengthened", Evidence: "bounded metadata-only audit evidence and explicit raw-content privacy flags"},
		{Gate: "retained_use", Status: "strengthened", Evidence: "audit-watch packets preserve open findings for repeated review and promotion"},
		{Gate: "integration_proof", Status: "strengthened", Evidence: "packets join trigger logs, harness_audit summaries, owner surfaces, and reproducer commands"},
		{Gate: "client_demo_proof", Status: "strengthened", Evidence: "all client classifications carry reviewable example checks"},
		{Gate: "packaging_support", Status: "strengthened", Evidence: "schema-versioned JSON packet supports local CLI consumption"},
		{Gate: "local_product_evidence_export", Status: "strengthened", Evidence: "review packets are durable local artifacts with bounded evidence fields"},
	}
}

func auditWatchPrivacyPolicy() auditWatchEvidencePrivacy {
	return auditWatchEvidencePrivacy{
		EvidencePolicy:      "bounded_audit_metadata_only",
		PromptTextIncluded:  false,
		FileBodiesIncluded:  false,
		LogExcerptsIncluded: false,
		SecretsIncluded:     false,
	}
}

func auditWatchPathExists(root string, path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}

	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}

	_, err := os.Stat(path)

	return err == nil
}

func writeAuditWatchReview(outputDir string, review auditWatchReview) (string, error) {
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return "", fmt.Errorf("create audit watch output: %w", err)
	}

	name := time.Now().UTC().Format("20060102-150405.000000000") + ".json"
	path := filepath.Join(outputDir, name)

	data, err := json.MarshalIndent(review, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal audit review: %w", err)
	}

	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return "", fmt.Errorf("write audit review: %w", err)
	}

	return path, nil
}

func collectAuditWatchFiles(root string, paths []string) ([]auditWatchFile, error) {
	var files []auditWatchFile

	seen := make(map[string]struct{})

	for _, raw := range paths {
		path := strings.TrimSpace(raw)
		if path == "" {
			continue
		}

		abs := path
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(root, path)
		}

		info, err := os.Stat(abs)
		if os.IsNotExist(err) {
			continue
		}

		if err != nil {
			return nil, fmt.Errorf("stat audit watch path %s: %w", path, err)
		}

		if !info.IsDir() {
			if auditWatchIsJSONL(abs) {
				files = appendAuditWatchFile(files, seen, root, abs, info)
			}

			continue
		}

		if err := safepath.WalkDirUnderRoot(abs, abs, "audit watch path", func(child string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}

			if entry.IsDir() || !auditWatchIsJSONL(child) {
				return nil
			}

			info, err := entry.Info()
			if err != nil {
				return err
			}

			files = appendAuditWatchFile(files, seen, root, child, info)

			return nil
		}); err != nil {
			return nil, fmt.Errorf("walk audit watch path %s: %w", path, err)
		}
	}

	slices.SortFunc(files, func(a, b auditWatchFile) int {
		return cmp.Or(
			-cmp.Compare(a.ModUnixNano, b.ModUnixNano),
			cmp.Compare(a.Path, b.Path),
		)
	})

	return files, nil
}

func auditWatchIsJSONL(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".jsonl")
}

func appendAuditWatchFile(files []auditWatchFile, seen map[string]struct{}, root string, path string, info os.FileInfo) []auditWatchFile {
	clean := filepath.Clean(path)
	if _, ok := seen[clean]; ok {
		return files
	}

	seen[clean] = struct{}{}

	display := clean

	if rel, err := filepath.Rel(root, clean); err == nil && rel != "." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		display = filepath.ToSlash(rel)
	}

	return append(files, auditWatchFile{
		Path:        clean,
		DisplayPath: display,
		ModUnixNano: info.ModTime().UnixNano(),
		Size:        info.Size(),
	})
}

func auditWatchTriggerForFile(file auditWatchFile, previous auditWatchSeenFile) (auditWatchTriggerFile, bool) {
	f, err := os.Open(file.Path)
	if err != nil {
		return auditWatchTriggerFile{}, false
	}
	defer f.Close()

	if previous.Size > 0 && previous.Size < file.Size {
		if _, err := f.Seek(previous.Size, io.SeekStart); err != nil {
			return auditWatchTriggerFile{}, false
		}
	}

	reasons := make(map[string]struct{})
	sessions := make(map[string]struct{})

	var recoveryEvents []auditWatchRecoveryEvent

	eventCount := 0
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 64*1024*1024)

	for scanner.Scan() {
		var record map[string]any

		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			continue
		}

		if sessionID := auditWatchString(record, "session_id"); sessionID != "" {
			sessions[sessionID] = struct{}{}
		}

		if event, ok := auditWatchRecoveryEventForRecord(record); ok {
			recoveryEvents = append(recoveryEvents, event)
			if event.SessionID != "" {
				sessions[event.SessionID] = struct{}{}
			}
		}

		for _, reason := range auditWatchTriggerReasons(record) {
			reasons[reason] = struct{}{}
			eventCount++
		}
	}

	if len(reasons) == 0 {
		return auditWatchTriggerFile{}, false
	}

	out := auditWatchTriggerFile{
		Path:           file.DisplayPath,
		ModUnixNano:    file.ModUnixNano,
		Size:           file.Size,
		EventCount:     eventCount,
		Reasons:        make([]string, 0, len(reasons)),
		Sessions:       make([]string, 0, len(sessions)),
		RecoveryEvents: recoveryEvents,
	}
	for reason := range reasons {
		out.Reasons = append(out.Reasons, reason)
	}

	for sessionID := range sessions {
		out.Sessions = append(out.Sessions, sessionID)
	}

	slices.Sort(out.Reasons)
	slices.Sort(out.Sessions)

	return out, true
}

func auditWatchTriggerReasons(record map[string]any) []string {
	eventType := auditWatchString(record, "type")
	tool := auditWatchString(record, "tool")
	command := auditWatchString(record, "command")
	summary := strings.ToLower(auditWatchString(record, "summary"))
	errText := strings.ToLower(auditWatchString(record, "error"))
	stopReason := auditWatchString(record, "stop_reason")

	var reasons []string

	if eventType == "process_exit" && command == "implement" {
		if auditWatchNumber(record, "exit_code") != 0 {
			reasons = append(reasons, "implement_process_failed")
		} else {
			reasons = append(reasons, "implement_process_exit")
		}
	}

	if eventType == "tool_result" && (tool == "harness_feedback" || tool == "idiom_feedback") {
		reasons = append(reasons, "harness_feedback")
	}

	if eventType == "tool_result" && tool == "completion_gate" {
		reasons = append(reasons, "completion_gate")
	}

	if eventType == "turn_summary" && auditWatchHighRiskStopReason(stopReason) {
		reasons = append(reasons, "high_risk_stop_reason:"+stopReason)
	}

	if eventType == "completion_status" && strings.EqualFold(auditWatchString(record, "status"), "blocked") {
		reason := firstAuditWatchString(auditWatchString(record, "reason"), auditWatchString(record, "blocker_kind"), "blocked")

		reasons = append(reasons, "completion_status_blocked:"+auditWatchBoundedReasonFragment(reason))
	}

	switch eventType {
	case plancontract.PlanExecuteEventName(plancontract.PlanExecuteEventRecoveryFailureRecordWritten):
		class := auditWatchString(record, "failure_class")
		if class == "" {
			class = "unknown_failure"
		} else {
			class = auditWatchBoundedReasonFragment(class)
		}

		reasons = append(reasons, "recovery_failure_record_written:"+class)
	case plancontract.PlanExecuteEventName(plancontract.PlanExecuteEventRecoveryDecompositionWritten):
		reasons = append(reasons, "recovery_decomposition_packet_written")
	case plancontract.PlanExecuteEventName(plancontract.PlanExecuteEventRecoveryFailureRecordFailed), plancontract.PlanExecuteEventName(plancontract.PlanExecuteEventRecoveryDecompositionFailed):
		reasons = append(reasons, "recovery_artifact_write_failed")
	}

	combined := summary + " " + errText
	if strings.Contains(combined, "malformed") ||
		strings.Contains(combined, "invalid input json") ||
		strings.Contains(combined, "patch parse failed") ||
		strings.Contains(combined, "tool_parse") {

		reasons = append(reasons, "malformed_tool_input")
	}

	if strings.Contains(combined, "stale") && strings.Contains(combined, "context") {
		reasons = append(reasons, "stale_context")
	}

	return reasons
}

func auditWatchRecoveryEventForRecord(record map[string]any) (auditWatchRecoveryEvent, bool) {
	eventType := auditWatchString(record, "type")
	status := ""
	artifactPath := ""

	switch eventType {
	case plancontract.PlanExecuteEventName(plancontract.PlanExecuteEventRecoveryFailureRecordWritten):
		status = "recorded"
		artifactPath = auditWatchString(record, "record_path")
	case plancontract.PlanExecuteEventName(plancontract.PlanExecuteEventRecoveryDecompositionWritten):
		status = "decomposed"
		artifactPath = auditWatchString(record, "packet_path")
	case plancontract.PlanExecuteEventName(plancontract.PlanExecuteEventRecoveryFailureRecordFailed), plancontract.PlanExecuteEventName(plancontract.PlanExecuteEventRecoveryDecompositionFailed):
		status = "artifact_write_failed"
		artifactPath = firstAuditWatchString(auditWatchString(record, "record_path"), auditWatchString(record, "packet_path"))
	default:
		return auditWatchRecoveryEvent{}, false
	}

	return auditWatchRecoveryEvent{
		Type:            eventType,
		Status:          status,
		SessionID:       auditWatchString(record, "session_id"),
		FailureID:       auditWatchString(record, "failure_id"),
		FailureClass:    auditWatchString(record, "failure_class"),
		FailureKind:     auditWatchString(record, "failure_kind"),
		PacketID:        auditWatchString(record, "packet_id"),
		CandidateAtoms:  int(auditWatchNumber(record, "candidate_atoms")),
		NextValidAction: auditWatchString(record, "next_valid_action"),
		ArtifactPath:    artifactPath,
	}, true
}

func auditWatchHighRiskStopReason(reason string) bool {
	switch strings.TrimSpace(reason) {
	case "completion_gate_repeated_rejection",
		"completion_gate_required_paths_remaining_repeated",
		"rejection_retry_blocked",
		"required_edit_paths_remaining",
		"source_edit_verification_required",
		"tool_step_budget_exhausted",
		"write_only_recovery_required_paths_remaining",
		"exact_anchor_prewrite_hidden_tool_repeated",
		"exact_anchor_write_failure_repeated":
		return true
	default:
		return false
	}
}

func firstAuditWatchString(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}

	return ""
}

func auditWatchNumber(record map[string]any, key string) float64 {
	value, ok := record[key]
	if !ok {
		return 0
	}

	switch typed := value.(type) {
	case float64:
		return typed
	case int:
		return float64(typed)
	default:
		return 0
	}
}

func auditWatchSanitizeJSONReport(root string, output string) json.RawMessage {
	var value any

	if err := json.Unmarshal([]byte(output), &value); err != nil {
		return nil
	}

	sanitized := auditWatchSanitizeJSONValue(root, value)

	data, err := json.Marshal(sanitized)
	if err != nil {
		return nil
	}

	return json.RawMessage(data)
}

func auditWatchSanitizeJSONValue(root string, value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, child := range typed {
			if auditWatchSensitiveJSONKey(key) {
				out[key] = "[redacted]"
				continue
			}

			out[key] = auditWatchSanitizeJSONValue(root, child)
		}

		return out
	case []any:
		out := make([]any, 0, len(typed))
		for _, child := range typed {
			out = append(out, auditWatchSanitizeJSONValue(root, child))
		}

		return out
	case string:
		return auditWatchSanitizeText(root, typed, 600)
	default:
		return value
	}
}

func auditWatchSensitiveJSONKey(key string) bool {
	lower := strings.ToLower(strings.TrimSpace(key))

	return strings.Contains(lower, "raw_prompt") ||
		strings.Contains(lower, "prompt_text") ||
		strings.Contains(lower, "transcript") ||
		strings.Contains(lower, "log_excerpt") ||
		strings.Contains(lower, "file_body") ||
		strings.Contains(lower, "secret") ||
		strings.Contains(lower, "api_key") ||
		strings.Contains(lower, "password") ||
		strings.Contains(lower, "token") ||
		strings.Contains(lower, "authorization") ||
		strings.Contains(lower, "credential") ||
		strings.Contains(lower, "private_key") ||
		strings.Contains(lower, "private-key") ||
		strings.Contains(lower, "client_secret") ||
		strings.Contains(lower, "client-secret")
}

func auditWatchSanitizeText(root string, value string, limit int) string {
	original := strings.TrimSpace(strings.ReplaceAll(value, "\n", " "))
	text := original

	if text == "" {
		return ""
	}

	if root = strings.TrimSpace(root); root != "" {
		text = strings.ReplaceAll(text, root, "$WORKSPACE_ROOT")
		text = strings.ReplaceAll(text, filepath.ToSlash(root), "$WORKSPACE_ROOT")
	}

	if kind, ok := auditWatchSensitiveTextKind(text); ok {
		return auditWatchRedactedReference(kind, original)
	}

	if auditWatchContainsAbsoluteLocalPath(text) {
		return auditWatchRedactedReference("path", original)
	}

	if limit <= 0 {
		return text
	}

	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}

	return string(runes[:limit]) + "..."
}

func auditWatchSensitiveTextKind(text string) (string, bool) {
	lower := strings.ToLower(text)
	switch {
	case strings.Contains(lower, "begin private key") ||
		strings.Contains(lower, "api_key=") ||
		strings.Contains(lower, "password=") ||
		strings.Contains(lower, "secret=") ||
		strings.Contains(lower, "token="):
		return "secret", true
	case auditWatchContainsRawContentMarker(lower):
		return "raw_content", true
	case auditWatchContainsSensitivePathSegment(text):
		return "path", true
	case auditWatchContainsCustomerLikeIdentifier(lower):
		return "customer", true
	default:
		return "", false
	}
}

func auditWatchContainsRawContentMarker(lower string) bool {
	return strings.Contains(lower, "raw_prompt") ||
		strings.Contains(lower, "raw prompt") ||
		strings.Contains(lower, "prompt_text") ||
		strings.Contains(lower, "prompt text") ||
		strings.Contains(lower, "transcript") ||
		strings.Contains(lower, "file_body") ||
		strings.Contains(lower, "file body") ||
		strings.Contains(lower, "log_excerpt") ||
		strings.Contains(lower, "log excerpt")
}

func auditWatchContainsSensitivePathSegment(text string) bool {
	for _, field := range strings.Fields(text) {
		field = strings.Trim(field, `"'(),;[]{}<>`)
		if field == "" {
			continue
		}

		normalized := strings.ReplaceAll(field, "\\", "/")
		for _, segment := range strings.Split(normalized, "/") {
			segment = strings.Trim(strings.ToLower(segment), ".-_ ")
			switch segment {
			case "secret", "secrets", "credential", "credentials", "private", "keys":
				return true
			}
		}
	}

	return false
}

func auditWatchContainsCustomerLikeIdentifier(lower string) bool {
	if !(strings.Contains(lower, "customer-") ||
		strings.Contains(lower, "customer_") ||
		strings.Contains(lower, "customer/") ||
		strings.Contains(lower, "customer.")) {

		return false
	}

	return strings.Contains(lower, "session") ||
		strings.Contains(lower, "account") ||
		strings.Contains(lower, "tenant") ||
		strings.Contains(lower, "client") ||
		strings.Contains(lower, "logs/") ||
		strings.Contains(lower, "telemetry/")
}

func auditWatchContainsAbsoluteLocalPath(text string) bool {
	for _, field := range strings.Fields(text) {
		field = strings.Trim(field, `"'(),;[]{}<>`)
		if filepath.IsAbs(field) {
			return true
		}

		if len(field) >= 3 && field[1] == ':' && (field[2] == '\\' || field[2] == '/') {
			return true
		}
	}

	return false
}

func auditWatchRedactedReference(kind string, value string) string {
	kind = auditWatchSlug(kind, 32)
	if kind == "" {
		kind = "value"
	}

	sum := sha256.Sum256([]byte(value))

	return "[redacted_" + kind + " sha256:" + hex.EncodeToString(sum[:4]) + "]"
}

func auditWatchBoundedReasonFragment(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "blocked"
	}

	if _, ok := auditWatchSensitiveTextKind(value); ok {
		return "redacted"
	}

	if auditWatchContainsAbsoluteLocalPath(value) {
		return "redacted"
	}

	value = strings.ToLower(value)

	var b strings.Builder

	lastSep := false

	for _, r := range value {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' || r == ':'
		if ok {
			b.WriteRune(r)

			lastSep = false

			continue
		}

		if !lastSep && b.Len() > 0 {
			b.WriteByte('_')

			lastSep = true
		}
	}

	out := strings.Trim(b.String(), "_-:")
	if out == "" {
		return "redacted"
	}

	if len(out) > 80 {
		out = strings.Trim(out[:80], "_-:")
	}

	if out == "" {
		return "redacted"
	}

	return out
}

func auditWatchSanitizeStrings(root string, values []string, limit int) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = auditWatchSanitizeText(root, value, limit)
		if value != "" {
			out = append(out, value)
		}
	}

	return auditWatchUniqueStrings(out)
}

func auditWatchUniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))

	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}

		if _, ok := seen[value]; ok {
			continue
		}

		seen[value] = struct{}{}
		out = append(out, value)
	}

	slices.Sort(out)

	return out
}

func auditWatchNonEmptyStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}

	return out
}

func auditWatchSlug(value string, limit int) string {
	value = strings.ToLower(strings.TrimSpace(value))

	var b strings.Builder

	lastDash := false

	for _, r := range value {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if ok {
			b.WriteRune(r)

			lastDash = false

			continue
		}

		if !lastDash && b.Len() > 0 {
			b.WriteByte('-')

			lastDash = true
		}
	}

	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "finding"
	}

	if limit > 0 && len(out) > limit {
		out = strings.Trim(out[:limit], "-")
	}

	return out
}

func auditWatchShellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func auditWatchPathList(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		if _, ok := seen[part]; ok {
			continue
		}

		seen[part] = struct{}{}
		out = append(out, part)
	}

	return out
}

func auditWatchMarkFilesSeen(state *auditWatchState, files []auditWatchFile) {
	if state.Files == nil {
		state.Files = make(map[string]auditWatchSeenFile, len(files))
	}

	for _, file := range files {
		state.Files[file.Path] = auditWatchSeenFile{
			ModUnixNano: file.ModUnixNano,
			Size:        file.Size,
		}
	}
}

func auditWatchString(record map[string]any, key string) string {
	value, ok := record[key]
	if !ok {
		return ""
	}

	text, ok := value.(string)
	if !ok {
		return ""
	}

	return strings.TrimSpace(text)
}

func loadAuditWatchState(path string) (auditWatchState, error) {
	var state auditWatchState

	data, err := os.ReadFile(path)

	if os.IsNotExist(err) {
		return state, nil
	}

	if err != nil {
		return state, fmt.Errorf("read audit watch state: %w", err)
	}

	if len(strings.TrimSpace(string(data))) == 0 {
		return state, nil
	}

	if err := json.Unmarshal(data, &state); err != nil {
		return state, fmt.Errorf("decode audit watch state: %w", err)
	}

	return state, nil
}

func saveAuditWatchState(path string, state auditWatchState) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create audit watch state dir: %w", err)
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal audit watch state: %w", err)
	}

	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write audit watch state: %w", err)
	}

	return nil
}

func printAuditUsage(stderr io.Writer) {
	fmt.Fprintln(stderr, "Usage:")
	fmt.Fprintln(stderr, "  idiom audit watch [--config config.json] [--once] [--review-existing]")
	fmt.Fprintln(stderr, "  idiom audit enum-mirrors --enum <Name> [--root <path>] [--pattern <regex>] [--ext <ext>]")
	fmt.Fprintln(stderr)
	fmt.Fprintln(stderr, "Watch idiom telemetry/conversation logs, debounce trigger events, run harness_audit, and write deterministic review packets with ownership, promotion, and replay-closure fields.")
	fmt.Fprintln(stderr, "enum-mirrors: surface every site that breaks if a named symbol changes (atom-drafting aid).")
	fmt.Fprintln(stderr)
	fmt.Fprintln(stderr, "Flags:")
	fmt.Fprintln(stderr, "  -config string           path to config file (default \"config.json\")")
	fmt.Fprintln(stderr, "  -paths string            comma-separated log files/directories (default \"logs/telemetry,.idiom/logs/conversations\")")
	fmt.Fprintln(stderr, "  -output string           directory for review JSON artifacts (default \".idiom/audits/watch\")")
	fmt.Fprintln(stderr, "  -state string            state file (default \"<output>/state.json\")")
	fmt.Fprintln(stderr, "  -interval duration       polling interval (default 5s)")
	fmt.Fprintln(stderr, "  -debounce duration       delay before audit after trigger (default 30s)")
	fmt.Fprintln(stderr, "  -limit int               harness_audit log limit per watched path (default 50)")
	fmt.Fprintln(stderr, "  -once                    scan once and exit")
	fmt.Fprintln(stderr, "  -review-existing         review existing trigger files on first run instead of creating a baseline")
	fmt.Fprintln(stderr, "  -quiet                   suppress status output")
}

func auditWatchHelpRequested(args []string) bool {
	skipValueFor := map[string]bool{
		"-config":    true,
		"--config":   true,
		"-paths":     true,
		"--paths":    true,
		"-output":    true,
		"--output":   true,
		"-state":     true,
		"--state":    true,
		"-interval":  true,
		"--interval": true,
		"-debounce":  true,
		"--debounce": true,
		"-limit":     true,
		"--limit":    true,
	}

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--help" || arg == "-h" || arg == "help" {
			return true
		}

		if skipValueFor[arg] && i+1 < len(args) {
			i++
		}
	}

	return false
}
