package recover

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"idiom/internal/evidence/telemetry"
	"idiom/internal/platform/config"
	toolcmd "idiom/internal/surfaces/cli/tool"
	"idiom/internal/workflows/planexecute"
	"idiom/internal/workflows/recovery"
)

type recoverInspectOptions struct {
	ConfigPath string
	SessionID  string
	JSON       bool
}

type recoverInspectReport struct {
	Type              string                  `json:"type"`
	Status            string                  `json:"status"`
	SessionID         string                  `json:"session_id,omitempty"`
	FailureID         string                  `json:"failure_id,omitempty"`
	RecordPath        string                  `json:"record_path,omitempty"`
	ProgressStatePath string                  `json:"progress_state_path,omitempty"`
	Recorded          bool                    `json:"recorded"`
	FailureClass      string                  `json:"failure_class,omitempty"`
	FailureKind       string                  `json:"failure_kind,omitempty"`
	NextAction        string                  `json:"next_valid_action"`
	Summary           string                  `json:"summary,omitempty"`
	PartialState      recovery.PartialState   `json:"partial_state"`
	Record            *recovery.FailureRecord `json:"record,omitempty"`
	Error             string                  `json:"error,omitempty"`
}

func RunMainWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	subcommand := ""
	if len(args) > 0 {
		subcommand = args[0]
	}

	code := RunMain(args, stdout, stderr)
	toolcmd.LogCLIPhase(rec, "recover", map[string]any{"subcommand": subcommand, "exit_code": code})

	return code
}

func RunMain(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		printRecoverUsage(stderr)

		return 2
	}

	switch args[0] {
	case "inspect":
		return runRecoverInspectMain(args[1:], stdout, stderr)
	case "decompose":
		return runRecoverDecomposeMain(args[1:], stdout, stderr)
	case "promote":
		return runRecoverPromoteMain(args[1:], stdout, stderr)
	case "reconcile":
		return runRecoverReconcileMain(args[1:], stdout, stderr)
	case "status":
		return runRecoverStatusMain(args[1:], stdout, stderr)
	case "-h", "--help", "help":
		printRecoverUsage(stdout)

		return 0
	default:
		fmt.Fprintf(stderr, "idiom recover: unknown subcommand %q\n", args[0])
		printRecoverUsage(stderr)

		return 2
	}
}

func printRecoverUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  idiom recover inspect --session <id> [--config <config.json>] [--json]")
	fmt.Fprintln(w, "  idiom recover decompose --session <id> [--config <config.json>] [--output <packet.json>] [--json]")
	fmt.Fprintln(w, "  idiom recover promote --packet <packet.json> --atom <id> --output <brief.md> [--config <config.json>] [--json] [--allow-dirty]")
	fmt.Fprintln(w, "  idiom recover reconcile --packet <packet.json> --completed <atom-id>[,<atom-id>...] [--config <config.json>] [--output <reconciliation.json>] [--json]")
	fmt.Fprintln(w, "  idiom recover status --packet <packet.json> [--config <config.json>] [--json]")
}

func runRecoverInspectMain(args []string, stdout io.Writer, stderr io.Writer) int {
	opts := recoverInspectOptions{ConfigPath: "config.json"}
	fs := flag.NewFlagSet("idiom recover inspect", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&opts.ConfigPath, "config", opts.ConfigPath, "path to config file")
	fs.StringVar(&opts.SessionID, "session", "", "recovery session id to inspect")
	fs.BoolVar(&opts.JSON, "json", false, "emit machine-readable recovery report")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	if fs.NArg() != 0 {
		return emitRecoverInspectError(stdout, stderr, opts.JSON, 2, "unexpected positional argument: "+fs.Arg(0))
	}

	if strings.TrimSpace(opts.SessionID) == "" {
		return emitRecoverInspectError(stdout, stderr, opts.JSON, 2, "recover inspect: --session is required")
	}

	cfg, err := config.Load(opts.ConfigPath)
	if err != nil {
		return emitRecoverInspectError(stdout, stderr, opts.JSON, 1, err.Error())
	}

	report, err := buildRecoverInspectReport(cfg.Workspace.Root, opts.SessionID)
	if err != nil {
		report = recoverInspectReport{
			Type:       "idiom.recover.inspect.v1",
			Status:     "error",
			SessionID:  strings.TrimSpace(opts.SessionID),
			NextAction: recovery.NextActionInspectFailure,
			Error:      err.Error(),
		}

		return writeRecoverInspectReport(stdout, stderr, opts.JSON, report, 1)
	}

	return writeRecoverInspectReport(stdout, stderr, opts.JSON, report, 0)
}

func buildRecoverInspectReport(root string, sessionID string) (recoverInspectReport, error) {
	sessionID = strings.TrimSpace(sessionID)

	record, recordPath, found, err := recovery.TryLoadFailureRecordForSession(root, sessionID)
	if err != nil {
		return recoverInspectReport{}, err
	}

	if found {
		return recoverInspectReportFromRecord(root, record, recordPath, true), nil
	}

	state, statePath, stateFound, err := planexecute.TryLoadProgressState(root, sessionID)
	if err != nil {
		return recoverInspectReport{}, err
	}

	if !stateFound {
		return recoverInspectReport{}, fmt.Errorf("no recovery record or plan-execute progress state found for session %s", sessionID)
	}

	if state.Status != planexecute.ProgressStatusFailed {
		return recoverInspectReport{
			Type:              "idiom.recover.inspect.v1",
			Status:            state.Status,
			SessionID:         state.SessionID,
			ProgressStatePath: statePath,
			Recorded:          false,
			NextAction:        recovery.NextActionNone,
			Summary:           fmt.Sprintf("plan-execute session is %s, not failed", state.Status),
		}, nil
	}

	computed, err := recovery.BuildPlanExecuteFailureRecord(planexecute.RecoveryInputFromProgress(state, statePath), time.Now().UTC())
	if err != nil {
		return recoverInspectReport{}, err
	}

	report := recoverInspectReportFromRecord(root, computed, "", false)

	report.ProgressStatePath = statePath

	return report, nil
}

func recoverInspectReportFromRecord(root string, record recovery.FailureRecord, recordPath string, recorded bool) recoverInspectReport {
	partial := inspectRecoveryPartialState(root, record)

	record.PartialState = partial
	record.NextAction = recovery.NextActionForPartialState(record, partial)

	return recoverInspectReport{
		Type:              "idiom.recover.inspect.v1",
		Status:            "failed",
		SessionID:         record.SourceRun.SessionID,
		FailureID:         record.FailureID,
		RecordPath:        recordPath,
		ProgressStatePath: record.SourceRun.ProgressStatePath,
		Recorded:          recorded,
		FailureClass:      record.Failure.Class,
		FailureKind:       record.Failure.Kind,
		NextAction:        record.NextAction,
		Summary:           record.Failure.Summary,
		PartialState:      partial,
		Record:            &record,
	}
}

func emitRecoverInspectError(stdout io.Writer, stderr io.Writer, jsonOut bool, code int, message string) int {
	report := recoverInspectReport{
		Type:       "idiom.recover.inspect.v1",
		Status:     "error",
		NextAction: recovery.NextActionInspectFailure,
		Error:      message,
	}

	return writeRecoverInspectReport(stdout, stderr, jsonOut, report, code)
}

func writeRecoverInspectReport(stdout io.Writer, stderr io.Writer, jsonOut bool, report recoverInspectReport, code int) int {
	if jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")

		if err := enc.Encode(report); err != nil {
			fmt.Fprintf(stderr, "recover inspect: encode report: %v\n", err)

			return 1
		}

		return code
	}

	if report.Error != "" {
		fmt.Fprintln(stderr, report.Error)
		fmt.Fprintf(stderr, "next_valid_action: %s\n", report.NextAction)

		return code
	}

	lane := ""
	if report.Record != nil {
		lane = strings.TrimSpace(report.Record.SourceRun.Lane)
	}

	if lane == "" {
		lane = "recovery"
	}

	fmt.Fprintf(stdout, "recover inspect: %s session %s\n", lane, report.SessionID)
	fmt.Fprintf(stdout, "status: %s\n", report.Status)

	if report.FailureClass != "" {
		fmt.Fprintf(stdout, "failure_class: %s\n", report.FailureClass)
	}

	if report.FailureKind != "" {
		fmt.Fprintf(stdout, "failure_kind: %s\n", report.FailureKind)
	}

	if report.RecordPath != "" {
		fmt.Fprintf(stdout, "record: %s\n", report.RecordPath)
	}

	if report.PartialState.DirtyStatus != "" {
		fmt.Fprintf(stdout, "dirty_status: %s\n", report.PartialState.DirtyStatus)
	}

	if report.PartialState.ScopeStatus != "" {
		fmt.Fprintf(stdout, "scope_status: %s\n", report.PartialState.ScopeStatus)
	}

	fmt.Fprintf(stdout, "next_valid_action: %s\n", report.NextAction)

	if report.Summary != "" {
		fmt.Fprintf(stdout, "summary: %s\n", report.Summary)
	}

	return code
}

func inspectRecoveryPartialState(root string, record recovery.FailureRecord) recovery.PartialState {
	dirtyPaths, err := gitDirtyPaths(root)
	if err != nil {
		return record.PartialState
	}

	return recovery.PartialStateForDirtyPaths(
		dirtyPaths,
		record.PartialState.ChangedPaths,
		record.SourceRun.RequiredEditPaths,
		record.SourceRun.AllowedEditPaths,
		record.PartialState.TotalEdits,
	)
}

func gitDirtyPaths(root string) ([]string, error) {
	cmd := exec.Command("git", "-C", root, "status", "--porcelain", "--untracked-files=all")

	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	return parseGitPorcelainPaths(string(out)), nil
}

func parseGitPorcelainPaths(output string) []string {
	seen := map[string]bool{}

	var paths []string

	for _, line := range strings.Split(output, "\n") {
		if len(line) < 4 {
			continue
		}

		path := strings.TrimSpace(line[3:])
		if strings.Contains(path, " -> ") {
			parts := strings.Split(path, " -> ")

			path = parts[len(parts)-1]
		}

		path = filepath.ToSlash(strings.Trim(path, `"`))
		if path == "" || isRecoveryInternalPath(path) || seen[path] {
			continue
		}

		seen[path] = true
		paths = append(paths, path)
	}

	return paths
}

func isRecoveryInternalPath(path string) bool {
	return strings.HasPrefix(path, ".idiom/recovery/") ||
		strings.HasPrefix(path, ".idiom/plan-execute/")
}
