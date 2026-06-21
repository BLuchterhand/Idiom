package headless

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"idiom/internal/evidence/telemetry"
	"idiom/internal/platform/config"
	cmdruntime "idiom/internal/surfaces/cli/runtime"
	"idiom/internal/surfaces/tui/app"
	"idiom/internal/workflows/planexecute"
)

const (
	RunCompletionSchemaVersion = "idiom.headless.run_completion.v1"
	RunCompletionRecordType    = "idiom.headless.run_completion"
)

type PromptRunner func(config.Config, string, cmdruntime.RunOptions, io.Writer) (string, error)

type RunCompletionRecord struct {
	SchemaVersion    string              `json:"schema_version"`
	Type             string              `json:"type"`
	RunID            string              `json:"run_id"`
	SessionID        string              `json:"session_id,omitempty"`
	DebugLogPath     string              `json:"debug_log_path,omitempty"`
	PromptSHA256     string              `json:"prompt_sha256,omitempty"`
	AnswerSHA256     string              `json:"answer_sha256,omitempty"`
	OutputMode       string              `json:"output_mode"`
	OutputSchema     string              `json:"output_schema,omitempty"`
	ExitCode         int                 `json:"exit_code"`
	TerminalClass    string              `json:"terminal_class"`
	Error            string              `json:"error,omitempty"`
	ConfigPath       string              `json:"config_path,omitempty"`
	WorkspaceRoot    string              `json:"workspace_root,omitempty"`
	Policy           HeadlessPolicy      `json:"policy"`
	Artifacts        map[string]string   `json:"artifacts,omitempty"`
	Checks           []HeadlessCheck     `json:"checks,omitempty"`
	StartedAt        time.Time           `json:"started_at"`
	CompletedAt      time.Time           `json:"completed_at"`
	DurationMS       int64               `json:"duration_ms"`
	Implementation   string              `json:"implementation"`
	UnsupportedFlags []string            `json:"unsupported_flags,omitempty"`
	Metadata         map[string][]string `json:"metadata,omitempty"`
}

type RunOutputRecord struct {
	RunCompletionRecord
	Answer string `json:"answer,omitempty"`
}

type HeadlessPolicy struct {
	PermissionProfile string   `json:"permission_profile,omitempty"`
	ApprovalPolicy    string   `json:"approval_policy,omitempty"`
	Ephemeral         bool     `json:"ephemeral,omitempty"`
	AllowTools        []string `json:"allow_tools,omitempty"`
	DenyTools         []string `json:"deny_tools,omitempty"`
	AllowWrites       []string `json:"allow_writes,omitempty"`
	DenyPaths         []string `json:"deny_paths,omitempty"`
	MaxToolSteps      int      `json:"max_tool_steps,omitempty"`
	Timeout           string   `json:"timeout,omitempty"`
}

type HeadlessCheck struct {
	Name       string `json:"name"`
	Command    string `json:"command"`
	ExitCode   int    `json:"exit_code"`
	Stdout     string `json:"stdout,omitempty"`
	Stderr     string `json:"stderr,omitempty"`
	DurationMS int64  `json:"duration_ms"`
}

type stringList []string

func (l *stringList) String() string {
	if l == nil {
		return ""
	}

	return strings.Join(*l, ",")
}

func (l *stringList) Set(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}

	*l = append(*l, value)

	return nil
}

type runRequest struct {
	ConfigPath        string
	Prompt            string
	PromptFile        string
	OutputMode        string
	OutputSchema      string
	SummaryOut        string
	PatchOut          string
	Resume            string
	SessionID         string
	ModelOverride     string
	JSONEvents        bool
	FailOnDegraded    bool
	Commit            bool
	CommitMessage     string
	TestCommand       string
	LintCommand       string
	Policy            HeadlessPolicy
	UnsupportedIntent []string
}

func Run(args []string, stdin io.Reader, stdout io.Writer, stderr io.Writer, promptRun PromptRunner, rec *telemetry.Recorder) int {
	req, err := parseRunRequest(args, stdin, stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		fmt.Fprintln(stderr, err)

		return 2
	}

	cfg, err := config.Load(req.ConfigPath)
	if err != nil {
		fmt.Fprintln(stderr, err)

		return 1
	}

	applyRunRequestToConfig(&cfg, req)
	if strings.TrimSpace(req.ModelOverride) != "" {
		cfg.LlamaServer.Model = strings.TrimSpace(req.ModelOverride)
	}

	runID := strings.TrimSpace(req.SessionID)
	if runID == "" {
		runID = newRunID()
	}
	cfg.Workspace.SessionID = runID

	started := time.Now().UTC()
	record := RunCompletionRecord{
		SchemaVersion:    RunCompletionSchemaVersion,
		Type:             RunCompletionRecordType,
		RunID:            runID,
		SessionID:        runID,
		PromptSHA256:     sha256Hex(req.Prompt),
		OutputMode:       req.OutputMode,
		OutputSchema:     strings.TrimSpace(req.OutputSchema),
		ConfigPath:       filepath.Clean(req.ConfigPath),
		WorkspaceRoot:    workspaceRoot(cfg),
		Policy:           req.Policy,
		StartedAt:        started,
		Implementation:   "idiom run",
		UnsupportedFlags: append([]string(nil), req.UnsupportedIntent...),
	}

	runWriter := stdout
	if req.OutputMode == "json" {
		runWriter = io.Discard
	}

	answer, runErr := promptRun(cfg, req.Prompt, cmdruntime.RunOptions{
		JSONEvents: req.JSONEvents || req.OutputMode == "jsonl",
		Resume:     req.Resume,
		Telemetry:  rec,
	}, runWriter)
	record.AnswerSHA256 = sha256Hex(answer)

	exitCode := 0
	if runErr != nil {
		exitCode = 1
		record.Error = runErr.Error()
		record.TerminalClass = "failed"
	} else {
		record.TerminalClass = "completed"
	}

	if runErr == nil && strings.TrimSpace(req.OutputSchema) != "" {
		if err := validateOutputSchema(answer, req.OutputSchema); err != nil {
			exitCode = 1
			record.Error = err.Error()
			record.TerminalClass = "output_schema_failed"
		}
	}

	if runErr == nil && exitCode == 0 {
		record.Checks = append(record.Checks, runOptionalCheck("lint", req.LintCommand, cfg)...)
		record.Checks = append(record.Checks, runOptionalCheck("test", req.TestCommand, cfg)...)
		for _, check := range record.Checks {
			if check.ExitCode != 0 {
				exitCode = 1
				record.TerminalClass = "verification_failed"
				if record.Error == "" {
					record.Error = fmt.Sprintf("%s command failed with exit code %d", check.Name, check.ExitCode)
				}
			}
		}
	}

	if req.PatchOut != "" {
		if err := writeGitDiff(req.PatchOut, cfg); err != nil && record.Error == "" {
			exitCode = 1
			record.TerminalClass = "artifact_failed"
			record.Error = err.Error()
		} else if err == nil {
			addArtifact(&record, "patch", req.PatchOut)
		}
	}

	if req.Commit && exitCode == 0 {
		check := runCommandWithConfigTimeout("commit", "git commit -am "+shellQuote(commitMessage(req)), cfg)
		record.Checks = append(record.Checks, check)
		if check.ExitCode != 0 {
			exitCode = 1
			record.TerminalClass = "commit_failed"
			record.Error = "git commit failed"
		}
	}

	record.ExitCode = exitCode
	record.CompletedAt = time.Now().UTC()
	record.DurationMS = record.CompletedAt.Sub(started).Milliseconds()

	if req.FailOnDegraded && len(req.UnsupportedIntent) > 0 {
		exitCode = 1
		record.ExitCode = exitCode
		record.TerminalClass = "degraded"
	}

	addArtifact(&record, "completion", RunCompletionRelPath(record.RunID))
	completionPath, saveErr := SaveRunCompletionRecord(workspaceRoot(cfg), record)
	if saveErr == nil {
		addArtifact(&record, "completion", completionPath)
	} else if record.Error == "" {
		exitCode = 1
		record.ExitCode = exitCode
		record.TerminalClass = "artifact_failed"
		record.Error = saveErr.Error()
	}

	if req.SummaryOut != "" {
		addArtifact(&record, "summary", req.SummaryOut)
		if err := writeJSONFile(req.SummaryOut, record); err != nil {
			fmt.Fprintln(stderr, err)

			return 1
		}
	}

	if req.OutputMode == "json" {
		out := RunOutputRecord{RunCompletionRecord: record}
		if runErr == nil && answer != "" {
			out.Answer = answer
		}
		if err := json.NewEncoder(stdout).Encode(out); err != nil {
			fmt.Fprintln(stderr, err)

			return 1
		}
	} else if req.OutputMode == "text" && runErr == nil {
		fmt.Fprintln(stdout, answer)
	}

	if runErr != nil {
		fmt.Fprintln(stderr, runErr)
	}

	return exitCode
}

func parseRunRequest(args []string, stdin io.Reader, stderr io.Writer) (runRequest, error) {
	req := runRequest{ConfigPath: "config.json", OutputMode: "text"}
	var allowTools, denyTools, allowWrites, denyPaths stringList

	fs := flag.NewFlagSet("idiom run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&req.ConfigPath, "config", req.ConfigPath, "path to config file")
	fs.StringVar(&req.Prompt, "prompt", "", "prompt text to run headlessly")
	fs.StringVar(&req.PromptFile, "prompt-file", "", "file containing prompt text")
	fs.StringVar(&req.OutputMode, "output", req.OutputMode, "output mode: text, json, or jsonl")
	fs.StringVar(&req.OutputSchema, "output-schema", "", "expected downstream output schema name recorded in the run contract")
	fs.BoolVar(&req.JSONEvents, "json", false, "alias for --output=jsonl")
	fs.BoolVar(&req.Policy.Ephemeral, "ephemeral", false, "disable durable conversation logging for this run")
	fs.StringVar(&req.Resume, "resume", "", "resume a prior session id before running")
	fs.StringVar(&req.SessionID, "session", "", "caller-supplied run/session id for artifacts")
	fs.StringVar(&req.ModelOverride, "model", "", "override llama_server.model from config")
	fs.StringVar(&req.Policy.PermissionProfile, "permission-profile", "", "run permission profile: default, read-only, or write")
	fs.Var(&allowTools, "allow-tool", "allow a tool by name for this run")
	fs.Var(&denyTools, "deny-tool", "deny a tool by name for this run")
	fs.Var(&allowWrites, "allow-write", "allow writes matching this path glob")
	fs.Var(&denyPaths, "deny-path", "deny writes matching this path glob")
	fs.StringVar(&req.Policy.ApprovalPolicy, "approval-policy", "", "approval policy recorded for audit: never, on-request, on-failure, or untrusted")
	fs.IntVar(&req.Policy.MaxToolSteps, "max-tool-steps", 0, "override chat.max_tool_steps")
	fs.StringVar(&req.Policy.Timeout, "timeout", "", "per-run request timeout, for example 120s or 5m")
	fs.BoolVar(&req.FailOnDegraded, "fail-on-degraded", false, "exit non-zero when a requested parity affordance is recorded as degraded")
	fs.StringVar(&req.SummaryOut, "summary-out", "", "write the run completion record to this path")
	fs.StringVar(&req.PatchOut, "patch-out", "", "write git diff --binary output to this path after the run")
	fs.StringVar(&req.TestCommand, "test-cmd", "", "shell command to run after a successful model turn")
	fs.StringVar(&req.LintCommand, "lint-cmd", "", "shell command to run after a successful model turn")
	fs.BoolVar(&req.Commit, "commit", false, "commit changed tracked files after successful run and checks")
	fs.StringVar(&req.CommitMessage, "commit-message", "", "commit message used with --commit")

	if err := fs.Parse(args); err != nil {
		return runRequest{}, err
	}

	req.Policy.AllowTools = []string(allowTools)
	req.Policy.DenyTools = []string(denyTools)
	req.Policy.AllowWrites = []string(allowWrites)
	req.Policy.DenyPaths = []string(denyPaths)

	if req.JSONEvents {
		req.OutputMode = "jsonl"
	}

	outputMode := strings.ToLower(strings.TrimSpace(req.OutputMode))
	switch outputMode {
	case "", "text":
		req.OutputMode = "text"
	case "json", "jsonl":
		req.OutputMode = outputMode
	default:
		return runRequest{}, fmt.Errorf("--output must be text, json, or jsonl")
	}

	positional := strings.TrimSpace(strings.Join(fs.Args(), " "))
	switch {
	case strings.TrimSpace(req.Prompt) != "" && strings.TrimSpace(req.PromptFile) != "":
		return runRequest{}, fmt.Errorf("--prompt and --prompt-file are mutually exclusive")
	case strings.TrimSpace(req.Prompt) != "" && positional != "":
		return runRequest{}, fmt.Errorf("--prompt and positional prompt are mutually exclusive")
	case strings.TrimSpace(req.PromptFile) != "":
		data, err := os.ReadFile(filepath.Clean(req.PromptFile))
		if err != nil {
			return runRequest{}, fmt.Errorf("read prompt file: %w", err)
		}
		req.Prompt = string(data)
	case strings.TrimSpace(req.Prompt) != "":
	case positional == "-":
		data, err := io.ReadAll(stdin)
		if err != nil {
			return runRequest{}, fmt.Errorf("read stdin prompt: %w", err)
		}
		req.Prompt = string(data)
	case positional != "":
		req.Prompt = positional
	case stdinHasData(stdin):
		data, err := io.ReadAll(stdin)
		if err != nil {
			return runRequest{}, fmt.Errorf("read stdin prompt: %w", err)
		}
		req.Prompt = string(data)
	default:
		return runRequest{}, fmt.Errorf("prompt is required; use --prompt, --prompt-file, positional text, or pipe stdin")
	}

	req.Prompt = strings.TrimSpace(req.Prompt)
	if req.Prompt == "" {
		return runRequest{}, fmt.Errorf("prompt is empty")
	}

	if err := validatePolicyFlags(req.Policy); err != nil {
		return runRequest{}, err
	}

	if req.Policy.ApprovalPolicy != "" {
		policy := strings.ToLower(strings.TrimSpace(req.Policy.ApprovalPolicy))
		switch policy {
		case "never":
		case "on-request", "on_failure", "on-failure", "untrusted":
			req.UnsupportedIntent = append(req.UnsupportedIntent, "approval_policy_requires_interactive_transport")
		default:
			return runRequest{}, fmt.Errorf("--approval-policy must be never, on-request, on-failure, or untrusted")
		}
	}

	return req, nil
}

func validatePolicyFlags(policy HeadlessPolicy) error {
	switch strings.ToLower(strings.TrimSpace(policy.PermissionProfile)) {
	case "", "default", "read-only", "readonly", "read_only", "write", "writes", "write-enabled":
	default:
		return fmt.Errorf("--permission-profile must be default, read-only, or write")
	}

	if policy.MaxToolSteps < 0 {
		return fmt.Errorf("--max-tool-steps must be non-negative")
	}

	if strings.TrimSpace(policy.Timeout) != "" {
		d, err := time.ParseDuration(strings.TrimSpace(policy.Timeout))
		if err != nil || d <= 0 {
			return fmt.Errorf("--timeout must be a positive duration")
		}
	}

	return nil
}

func applyRunRequestToConfig(cfg *config.Config, req runRequest) {
	if cfg == nil {
		return
	}

	if req.Policy.Ephemeral {
		cfg.Chat.DebugLogDir = ""
		cfg.Chat.SaveDir = ""
	}

	if req.Policy.MaxToolSteps > 0 {
		cfg.Chat.MaxToolSteps = req.Policy.MaxToolSteps
	}

	if strings.TrimSpace(req.Policy.Timeout) != "" {
		if d, err := time.ParseDuration(strings.TrimSpace(req.Policy.Timeout)); err == nil && d > 0 {
			cfg.Chat.RequestTimeoutSeconds = durationSecondsCeil(d)
			cfg.Chat.ServerStartTimeoutSecs = 0
		}
	}

	switch strings.ToLower(strings.TrimSpace(req.Policy.PermissionProfile)) {
	case "", "default":
	case "read-only", "readonly", "read_only":
		cfg.Workspace.EnableWrites = false
	case "write", "writes", "write-enabled":
		cfg.Workspace.EnableWrites = true
	}

	if len(cfg.Workspace.ToolRules) == 0 {
		cfg.Workspace.ToolRules = map[string][]config.ToolRule{}
	}

	for _, tool := range req.Policy.DenyTools {
		tool = strings.TrimSpace(tool)
		if tool != "" {
			cfg.Workspace.ToolRules[tool] = append([]config.ToolRule{{Pattern: "*", Decision: "deny"}}, cfg.Workspace.ToolRules[tool]...)
		}
	}

	for _, tool := range req.Policy.AllowTools {
		tool = strings.TrimSpace(tool)
		if tool != "" {
			cfg.Workspace.ToolRules[tool] = append(cfg.Workspace.ToolRules[tool], config.ToolRule{Pattern: "*", Decision: "allow"})
		}
	}

	writeTools := []string{"edit_file", "write_file", "append_file", "apply_patch"}
	for _, path := range req.Policy.DenyPaths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		for _, tool := range writeTools {
			cfg.Workspace.ToolRules[tool] = append([]config.ToolRule{{Pattern: path, Decision: "deny"}}, cfg.Workspace.ToolRules[tool]...)
		}
	}

	for _, path := range req.Policy.AllowWrites {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		cfg.Workspace.EnableWrites = true
		for _, tool := range writeTools {
			cfg.Workspace.ToolRules[tool] = append(cfg.Workspace.ToolRules[tool], config.ToolRule{Pattern: path, Decision: "allow"})
		}
	}
}

func RunSession(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 || helpRequested(args[0]) {
		fmt.Fprintln(stderr, "usage: idiom session <list|inspect|export|replay> [options]")

		return 0
	}

	switch args[0] {
	case "list":
		return runSessionList(args[1:], stdout, stderr)
	case "inspect":
		return runSessionInspect(args[1:], stdout, stderr)
	case "export":
		return runSessionExport(args[1:], stdout, stderr)
	case "replay":
		return runSessionReplay(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown session command %q\n", args[0])

		return 2
	}
}

func runSessionList(args []string, stdout io.Writer, stderr io.Writer) int {
	var configPath string
	var jsonOut bool
	fs := flag.NewFlagSet("idiom session list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&configPath, "config", "config.json", "path to config file")
	fs.BoolVar(&jsonOut, "json", false, "emit JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintln(stderr, err)

		return 1
	}

	sessions, err := app.ListRecordedSessions(cfg)
	if err != nil {
		fmt.Fprintln(stderr, err)

		return 1
	}

	if jsonOut {
		_ = json.NewEncoder(stdout).Encode(map[string]any{"schema_version": "idiom.headless.sessions.v1", "sessions": sessions})

		return 0
	}

	for _, s := range sessions {
		fmt.Fprintf(stdout, "%s\tmsgs=%d/%d\t%s\n", s.ID, s.UserMessages, s.AssistMessage, s.FirstPrompt)
	}

	return 0
}

func runSessionInspect(args []string, stdout io.Writer, stderr io.Writer) int {
	cfg, sessionID, jsonOut, code := parseSessionIDCommand("idiom session inspect", args, stderr)
	if code != 0 {
		return code
	}

	report, err := inspectSession(cfg, sessionID)
	if err != nil {
		fmt.Fprintln(stderr, err)

		return 1
	}

	if jsonOut {
		_ = json.NewEncoder(stdout).Encode(report)
	} else {
		fmt.Fprintf(stdout, "session_id: %s\npath: %s\nmessages: %d\nlast_event_type: %s\n", report["session_id"], report["path"], report["messages"], report["last_event_type"])
	}

	return 0
}

func runSessionExport(args []string, stdout io.Writer, stderr io.Writer) int {
	var configPath, sessionID, out string
	fs := flag.NewFlagSet("idiom session export", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&configPath, "config", "config.json", "path to config file")
	fs.StringVar(&sessionID, "session", "", "session id")
	fs.StringVar(&out, "out", "", "path to write sanitized session export")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if sessionID == "" && fs.NArg() > 0 {
		sessionID = fs.Arg(0)
	}
	if sessionID == "" {
		fmt.Fprintln(stderr, "--session is required")

		return 2
	}
	if out == "" {
		fmt.Fprintln(stderr, "--out is required")

		return 2
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintln(stderr, err)

		return 1
	}

	path := app.ResolveSessionLogPath(cfg, sessionID)
	records, err := sanitizedSessionRecords(path)
	if err != nil {
		fmt.Fprintln(stderr, err)

		return 1
	}

	if err := writeJSONFile(out, map[string]any{"schema_version": "idiom.headless.session_export.v1", "session_id": sessionID, "records": records}); err != nil {
		fmt.Fprintln(stderr, err)

		return 1
	}

	fmt.Fprintln(stdout, out)

	return 0
}

func runSessionReplay(args []string, stdout io.Writer, stderr io.Writer) int {
	cfg, sessionID, jsonOut, code := parseSessionIDCommand("idiom session replay", args, stderr)
	if code != 0 {
		return code
	}

	prompts, err := sessionUserPrompts(app.ResolveSessionLogPath(cfg, sessionID))
	if err != nil {
		fmt.Fprintln(stderr, err)

		return 1
	}

	if jsonOut {
		_ = json.NewEncoder(stdout).Encode(map[string]any{"schema_version": "idiom.headless.session_replay.v1", "session_id": sessionID, "prompts": prompts, "dry_run": true})
	} else {
		for _, prompt := range prompts {
			fmt.Fprintln(stdout, prompt)
		}
	}

	return 0
}

func RunContract(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 || helpRequested(args[0]) {
		fmt.Fprintln(stderr, "usage: idiom contract <inspect|obligations|children> [options]")

		return 0
	}

	switch args[0] {
	case "inspect", "obligations", "children":
		return runContractReport(args[0], args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown contract command %q\n", args[0])

		return 2
	}
}

func runContractReport(kind string, args []string, stdout io.Writer, stderr io.Writer) int {
	var configPath, sessionID string
	var jsonOut bool
	fs := flag.NewFlagSet("idiom contract "+kind, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&configPath, "config", "config.json", "path to config file")
	fs.StringVar(&sessionID, "session", "", "session id")
	fs.BoolVar(&jsonOut, "json", false, "emit JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintln(stderr, err)

		return 1
	}

	report := map[string]any{
		"schema_version": "idiom.headless.contract_report.v1",
		"kind":           kind,
		"workspace_root": workspaceRoot(cfg),
		"tool_rules":     cfg.Workspace.ToolRules,
		"enable_writes":  cfg.Workspace.EnableWrites,
		"enable_shell":   cfg.Workspace.EnableShell,
	}

	if sessionID != "" {
		report["session_id"] = sessionID
		if completion, path, found, err := planexecute.TryLoadPlanExecuteTerminalCompletionRecord(workspaceRoot(cfg), sessionID); err == nil && found {
			report["plan_execute_terminal_completion_path"] = path
			report["plan_execute_terminal_completion"] = completion
		}
	}

	if kind == "children" {
		report["children"] = []string{}
	}

	if jsonOut {
		_ = json.NewEncoder(stdout).Encode(report)
	} else {
		fmt.Fprintf(stdout, "kind: %s\nworkspace_root: %s\nenable_writes: %t\ntool_rule_count: %d\n", kind, workspaceRoot(cfg), cfg.Workspace.EnableWrites, len(cfg.Workspace.ToolRules))
	}

	return 0
}

func RunDecision(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 || helpRequested(args[0]) {
		fmt.Fprintln(stderr, "usage: idiom decision why --session <id> [--index N] [--json]")

		return 0
	}
	if args[0] != "why" {
		fmt.Fprintf(stderr, "unknown decision command %q\n", args[0])

		return 2
	}

	var configPath, sessionID string
	var index int
	var jsonOut bool
	fs := flag.NewFlagSet("idiom decision why", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&configPath, "config", "config.json", "path to config file")
	fs.StringVar(&sessionID, "session", "", "session id")
	fs.IntVar(&index, "index", -1, "zero-based matching decision event index")
	fs.BoolVar(&jsonOut, "json", false, "emit JSON")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if sessionID == "" {
		fmt.Fprintln(stderr, "--session is required")

		return 2
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintln(stderr, err)

		return 1
	}

	report, err := decisionWhy(app.ResolveSessionLogPath(cfg, sessionID), index)
	if err != nil {
		fmt.Fprintln(stderr, err)

		return 1
	}

	if jsonOut {
		_ = json.NewEncoder(stdout).Encode(report)
	} else {
		fmt.Fprintf(stdout, "session_id: %s\nmatched_events: %v\nlast_type: %v\n", sessionID, report["matched_events"], report["last_type"])
	}

	return 0
}

func RunServe(args []string, stdout io.Writer, stderr io.Writer) int {
	var host string
	var port int
	fs := flag.NewFlagSet("idiom serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&host, "host", "127.0.0.1", "listen host")
	fs.IntVar(&port, "port", 0, "listen port")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	listener, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		fmt.Fprintln(stderr, err)

		return 1
	}
	defer listener.Close()

	fmt.Fprintf(stdout, "idiom serve listening on http://%s\n", listener.Addr().String())
	if err := http.Serve(listener, newServeMux()); err != nil && err != http.ErrServerClosed {
		fmt.Fprintln(stderr, err)

		return 1
	}

	return 0
}

func newServeMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"schema_version": "idiom.headless.server.v1", "status": "ok"})
	})
	mux.HandleFunc("/v1/status", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"schema_version": "idiom.headless.server.v1", "capabilities": []string{"health", "status"}})
	})

	return mux
}

func parseSessionIDCommand(name string, args []string, stderr io.Writer) (config.Config, string, bool, int) {
	var configPath, sessionID string
	var jsonOut bool
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&configPath, "config", "config.json", "path to config file")
	fs.StringVar(&sessionID, "session", "", "session id")
	fs.BoolVar(&jsonOut, "json", false, "emit JSON")
	if err := fs.Parse(args); err != nil {
		return config.Config{}, "", false, 2
	}
	if sessionID == "" && fs.NArg() > 0 {
		sessionID = fs.Arg(0)
	}
	if sessionID == "" {
		fmt.Fprintln(stderr, "--session is required")

		return config.Config{}, "", false, 2
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintln(stderr, err)

		return config.Config{}, "", false, 1
	}

	return cfg, sessionID, jsonOut, 0
}

func inspectSession(cfg config.Config, sessionID string) (map[string]any, error) {
	path := app.ResolveSessionLogPath(cfg, sessionID)
	count := 0
	lastType := ""
	err := scanJSONL(path, func(record map[string]any) {
		count++
		if typ, _ := record["type"].(string); typ != "" {
			lastType = typ
		}
	})
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"schema_version":  "idiom.headless.session_inspect.v1",
		"session_id":      sessionID,
		"path":            path,
		"messages":        count,
		"last_event_type": lastType,
	}, nil
}

func sanitizedSessionRecords(path string) ([]map[string]any, error) {
	records := []map[string]any{}
	err := scanJSONL(path, func(record map[string]any) {
		clean := map[string]any{}
		for _, key := range []string{"schema_version", "type", "role", "tool", "summary", "error", "stop_reason", "session_id", "ts"} {
			if val, ok := record[key]; ok {
				clean[key] = val
			}
		}
		if _, ok := clean["type"]; ok {
			records = append(records, clean)
		}
	})

	return records, err
}

func sessionUserPrompts(path string) ([]string, error) {
	prompts := []string{}
	err := scanJSONL(path, func(record map[string]any) {
		if record["type"] != "message" || record["role"] != "user" {
			return
		}
		if content, _ := record["content"].(string); strings.TrimSpace(content) != "" {
			prompts = append(prompts, content)
		}
	})

	return prompts, err
}

func decisionWhy(path string, index int) (map[string]any, error) {
	matches := []map[string]any{}
	err := scanJSONL(path, func(record map[string]any) {
		typ, _ := record["type"].(string)
		if strings.Contains(typ, "decision") || strings.Contains(typ, "route") || strings.Contains(typ, "policy") || strings.Contains(typ, "reject") {
			matches = append(matches, record)
		}
	})
	if err != nil {
		return nil, err
	}

	selected := map[string]any(nil)
	if len(matches) > 0 {
		if index >= 0 && index < len(matches) {
			selected = matches[index]
		} else {
			selected = matches[len(matches)-1]
		}
	}

	lastType := ""
	if selected != nil {
		lastType, _ = selected["type"].(string)
	}

	return map[string]any{
		"schema_version": "idiom.headless.decision_why.v1",
		"matched_events": len(matches),
		"last_type":      lastType,
		"selected":       selected,
	}, nil
}

func scanJSONL(path string, visit func(map[string]any)) error {
	file, err := os.Open(filepath.Clean(path))
	if err != nil {
		return fmt.Errorf("open session log: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal(line, &record); err != nil {
			continue
		}
		visit(record)
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan session log: %w", err)
	}

	return nil
}

func SaveRunCompletionRecord(root string, record RunCompletionRecord) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", fmt.Errorf("workspace root is required")
	}
	if strings.TrimSpace(record.RunID) == "" {
		return "", fmt.Errorf("run id is required")
	}

	relPath := RunCompletionRelPath(record.RunID)
	fullPath := filepath.Join(root, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		return "", fmt.Errorf("mkdir headless run completion dir: %w", err)
	}

	if err := writeJSONFile(fullPath, record); err != nil {
		return "", err
	}

	return relPath, nil
}

func RunCompletionRelPath(runID string) string {
	return filepath.ToSlash(filepath.Join(".idiom", "runs", strings.TrimSpace(runID)+".completion.json"))
}

func validateOutputSchema(answer string, schemaVersion string) error {
	schemaVersion = strings.TrimSpace(schemaVersion)
	if schemaVersion == "" {
		return nil
	}

	raw, err := answerJSONObject(answer)
	if err != nil {
		return err
	}

	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return fmt.Errorf("output schema validation failed: answer is not a JSON object: %w", err)
	}

	got, _ := decoded["schema_version"].(string)
	if strings.TrimSpace(got) != schemaVersion {
		return fmt.Errorf("output schema validation failed: schema_version %q does not match %q", got, schemaVersion)
	}

	return nil
}

func answerJSONObject(answer string) ([]byte, error) {
	text := strings.TrimSpace(answer)
	if text == "" {
		return nil, fmt.Errorf("output schema validation failed: answer is empty")
	}

	if strings.HasPrefix(text, "```") {
		lines := strings.Split(text, "\n")
		if len(lines) >= 3 && strings.HasPrefix(strings.TrimSpace(lines[len(lines)-1]), "```") {
			text = strings.TrimSpace(strings.Join(lines[1:len(lines)-1], "\n"))
		}
	}

	if strings.HasPrefix(text, "{") && strings.HasSuffix(text, "}") {
		return []byte(text), nil
	}

	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start >= 0 && end > start {
		return []byte(text[start : end+1]), nil
	}

	return nil, fmt.Errorf("output schema validation failed: answer does not contain a JSON object")
}

func runOptionalCheck(name string, command string, cfg config.Config) []HeadlessCheck {
	if strings.TrimSpace(command) == "" {
		return nil
	}

	return []HeadlessCheck{runCommandWithConfigTimeout(name, command, cfg)}
}

func runCommandWithConfigTimeout(name string, command string, cfg config.Config) HeadlessCheck {
	timeout := time.Duration(cfg.Workspace.CommandTimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 120 * time.Second
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	return runCommand(ctx, name, command, cfg)
}

func runCommand(ctx context.Context, name string, command string, cfg config.Config) HeadlessCheck {
	start := time.Now()
	check := HeadlessCheck{Name: name, Command: command}
	cmd := exec.CommandContext(ctx, shellName(cfg), "-c", command)
	cmd.Dir = commandDir(cfg)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	check.DurationMS = time.Since(start).Milliseconds()
	check.Stdout = boundedString(stdout.String(), 4000)
	check.Stderr = boundedString(stderr.String(), 4000)
	if err == nil {
		check.ExitCode = 0

		return check
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		check.ExitCode = exitErr.ExitCode()
	} else {
		check.ExitCode = 1
		check.Stderr = boundedString(strings.TrimSpace(check.Stderr+"\n"+err.Error()), 4000)
	}

	return check
}

func writeGitDiff(path string, cfg config.Config) error {
	cmd := exec.Command(shellName(cfg), "-c", "git diff --binary")
	cmd.Dir = commandDir(cfg)
	data, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("git diff --binary: %w", err)
	}

	return os.WriteFile(filepath.Clean(path), data, 0o600)
}

func writeJSONFile(path string, value any) error {
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal json: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(filepath.Clean(path)), 0o755); err != nil && filepath.Dir(path) != "." {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
	}

	return os.WriteFile(filepath.Clean(path), append(body, '\n'), 0o600)
}

func shellName(cfg config.Config) string {
	if strings.TrimSpace(cfg.Workspace.Shell) != "" {
		return cfg.Workspace.Shell
	}

	return "sh"
}

func commandDir(cfg config.Config) string {
	if strings.TrimSpace(cfg.Workspace.CWD) != "" {
		return cfg.Workspace.CWD
	}
	if strings.TrimSpace(cfg.Workspace.Root) != "" {
		return cfg.Workspace.Root
	}
	wd, err := os.Getwd()
	if err == nil {
		return wd
	}

	return "."
}

func workspaceRoot(cfg config.Config) string {
	if strings.TrimSpace(cfg.Workspace.Root) != "" {
		return cfg.Workspace.Root
	}

	return commandDir(cfg)
}

func addArtifact(record *RunCompletionRecord, name string, path string) {
	if record.Artifacts == nil {
		record.Artifacts = map[string]string{}
	}
	record.Artifacts[name] = path
}

func newRunID() string {
	return time.Now().UTC().Format("20060102-150405.000")
}

func sha256Hex(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(value))

	return hex.EncodeToString(sum[:])
}

func stdinHasData(r io.Reader) bool {
	file, ok := r.(*os.File)
	if !ok {
		return r != nil
	}

	stat, err := file.Stat()
	if err != nil {
		return false
	}

	return (stat.Mode() & os.ModeCharDevice) == 0
}

func boundedString(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || len(value) <= limit {
		return value
	}

	return value[:limit]
}

func durationSecondsCeil(d time.Duration) int {
	if d <= 0 {
		return 0
	}

	seconds := int(d / time.Second)
	if d%time.Second != 0 {
		seconds++
	}
	if seconds == 0 {
		return 1
	}

	return seconds
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func commitMessage(req runRequest) string {
	if strings.TrimSpace(req.CommitMessage) != "" {
		return strings.TrimSpace(req.CommitMessage)
	}

	return "Apply idiom headless run changes"
}

func helpRequested(arg string) bool {
	return arg == "help" || arg == "--help" || arg == "-h"
}
