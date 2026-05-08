package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"idiom/internal/config"
	"idiom/internal/telemetry"
	"idiom/internal/workspace"
)

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
	Path        string   `json:"path"`
	ModUnixNano int64    `json:"mod_unix_nano"`
	Size        int64    `json:"size"`
	Reasons     []string `json:"reasons"`
	EventCount  int      `json:"event_count"`
}

type auditWatchReview struct {
	GeneratedAt   string                  `json:"generated_at"`
	WorkspaceRoot string                  `json:"workspace_root"`
	TriggerFiles  []auditWatchTriggerFile `json:"trigger_files"`
	Audits        []auditWatchRun         `json:"audits"`
}

type auditWatchRun struct {
	Path    string          `json:"path"`
	Summary string          `json:"summary"`
	Error   string          `json:"error,omitempty"`
	Report  json.RawMessage `json:"report,omitempty"`
}

func runAuditMain(args []string, stdout io.Writer, stderr io.Writer) int {
	return runAuditMainWithTelemetry(args, stdout, stderr, nil)
}

func runAuditMainWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		printAuditUsage(stderr)
		return 0
	}
	logCLIPhase(rec, "audit_start", map[string]any{"subcommand": args[0]})
	switch args[0] {
	case "watch":
		return runAuditWatch(args[1:], stdout, stderr, rec)
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
		Paths:    "logs/telemetry,.idiom/logs/conversations",
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

	cfg, err := loadToolConfig(opts.Config)
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
			logCLIPhase(rec, "audit_watch_review", map[string]any{"artifact": reviewPath, "trigger_files": len(triggers)})
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
		GeneratedAt:   time.Now().UTC().Format(time.RFC3339Nano),
		WorkspaceRoot: cfg.Workspace.Root,
		TriggerFiles:  triggers,
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
			Path:    path,
			Summary: strings.TrimSpace(result.Summary),
			Error:   strings.TrimSpace(result.Error),
		}
		if strings.TrimSpace(result.Output) != "" && json.Valid([]byte(result.Output)) {
			run.Report = json.RawMessage(result.Output)
		}
		review.Audits = append(review.Audits, run)
	}
	return review, nil
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
			if filepath.Ext(abs) == ".jsonl" {
				files = appendAuditWatchFile(files, seen, root, abs, info)
			}
			continue
		}
		if err := filepath.WalkDir(abs, func(child string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || filepath.Ext(child) != ".jsonl" {
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
	sort.Slice(files, func(i, j int) bool {
		if files[i].ModUnixNano == files[j].ModUnixNano {
			return files[i].Path < files[j].Path
		}
		return files[i].ModUnixNano > files[j].ModUnixNano
	})
	return files, nil
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
	eventCount := 0
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 64*1024*1024)
	for scanner.Scan() {
		var record map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			continue
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
		Path:        file.DisplayPath,
		ModUnixNano: file.ModUnixNano,
		Size:        file.Size,
		EventCount:  eventCount,
		Reasons:     make([]string, 0, len(reasons)),
	}
	for reason := range reasons {
		out.Reasons = append(out.Reasons, reason)
	}
	sort.Strings(out.Reasons)
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
		reasons = append(reasons, "implement_process_exit")
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

func auditWatchHighRiskStopReason(reason string) bool {
	switch strings.TrimSpace(reason) {
	case "completion_gate_repeated_rejection",
		"rejection_retry_blocked",
		"source_edit_verification_required",
		"tool_step_budget_exhausted",
		"read_budget_exhausted",
		"exact_anchor_prewrite_hidden_tool_repeated",
		"exact_anchor_write_failure_repeated":
		return true
	default:
		return false
	}
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
	fmt.Fprintln(stderr)
	fmt.Fprintln(stderr, "Watch idiom telemetry/conversation logs, debounce trigger events, run harness_audit, and write deterministic review packets.")
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
