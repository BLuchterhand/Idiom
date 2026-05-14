package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"idiom/internal/app"
	"idiom/internal/config"
	"idiom/internal/llama"
	"idiom/internal/telemetry"
	"idiom/internal/workspace"
)

type runOptions struct {
	JSONEvents               bool
	Resume                   string
	TaskFamily               string
	PlannerOwnedVerification bool
	MaxTokensOverride        int
	DisableThinking          bool
	Telemetry                *telemetry.Recorder
}

type promptRunner func(config.Config, string, runOptions, io.Writer) (string, error)
type promptsRunner func(config.Config, []string, runOptions, io.Writer) ([]string, error)
type modeRunner func(config.Config, runOptions) error
type listSessionsRunner func(config.Config, io.Writer) error

func main() {
	os.Exit(runMain(os.Args[1:], os.Stdout, os.Stderr))
}

func runMain(args []string, stdout io.Writer, stderr io.Writer) (code int) {
	rec := startCommandTelemetry(args)
	if rec != nil {
		stopTelemetrySignalHandler := installTelemetrySignalHandler(rec)
		defer func() {
			stopTelemetrySignalHandler()
			if recovered := recover(); recovered != nil {
				_ = rec.CloseWithFields(2, map[string]any{
					"reason": "panic",
					"panic":  fmt.Sprint(recovered),
				})
				panic(recovered)
			}
			_ = rec.Close(code)
		}()
	}
	if len(args) > 0 && args[0] == "tool" {
		return runToolMainWithTelemetry(args[1:], stdout, stderr, rec)
	}
	if len(args) > 0 && args[0] == "context" {
		return runContextMainWithTelemetry(args[1:], stdout, stderr, rec)
	}
	if len(args) > 0 && args[0] == "atom" {
		return runAtomMainWithTelemetry(args[1:], stdout, stderr, rec)
	}
	if len(args) > 0 && args[0] == "artifact" {
		return runArtifactMainWithTelemetry(args[1:], stdout, stderr, runPromptOnce, rec)
	}
	if len(args) > 0 && args[0] == "implement" {
		return runImplementMainWithTelemetry(args[1:], stdout, stderr, runPromptOnce, rec)
	}
	if len(args) > 0 && args[0] == "plan-execute" {
		return runPlanExecuteMainWithTelemetry(args[1:], stdout, stderr, runPromptOnce, rec)
	}
	if len(args) > 0 && args[0] == "lane" {
		return runLaneMainWithTelemetry(args[1:], stdout, stderr, rec)
	}
	if len(args) > 0 && args[0] == "backend" {
		return runBackendMainWithTelemetry(args[1:], stdout, stderr, rec)
	}
	if len(args) > 0 && args[0] == "architect" {
		return runArchitectMainWithTelemetry(args[1:], stdout, stderr, rec)
	}
	if len(args) > 0 && args[0] == "audit" {
		return runAuditMainWithTelemetry(args[1:], stdout, stderr, rec)
	}
	if len(args) > 0 && args[0] == "ranker" {
		return runRankerMainWithTelemetry(args[1:], stdout, stderr, rec)
	}
	if len(args) > 0 && args[0] == "brief" {
		return runBriefMainWithTelemetry(args[1:], stdout, stderr, rec)
	}
	if len(args) > 0 && args[0] == "probe" {
		return runProbeMainWithTelemetry(args[1:], stdout, stderr, rec)
	}
	if len(args) > 0 && args[0] == "queue" {
		return runQueueMainWithTelemetry(args[1:], stdout, stderr, rec)
	}
	if len(args) > 0 && args[0] == "local-ops" {
		return runLocalOpsMainWithTelemetry(args[1:], stdout, stderr, rec)
	}
	if len(args) > 0 && args[0] == "product-evidence" {
		return runProductEvidenceMainWithTelemetry(args[1:], stdout, stderr, rec)
	}
	if len(args) > 0 && args[0] == "init" {
		return runInitMain(args[1:], stdout, stderr)
	}
	if len(args) > 0 && args[0] == "skills" {
		return runSkillsMain(args[1:], stdout, stderr)
	}
	if len(args) > 0 && args[0] == "version" {
		return runVersionMain(args[1:], stdout, stderr)
	}
	if len(args) > 0 && args[0] == "doctor" {
		return runDoctorMainWithTelemetry(args[1:], stdout, stderr, rec)
	}
	return runWithTelemetry(args, stdout, stderr, runPromptOnce, runPromptsOnce, runTUI, runServerOnly, runListSessions, rec)
}

func installTelemetrySignalHandler(rec *telemetry.Recorder) func() {
	if rec == nil {
		return func() {}
	}
	signals := make(chan os.Signal, 1)
	done := make(chan struct{})
	var stopOnce sync.Once
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	go func() {
		select {
		case sig := <-signals:
			exitCode := telemetrySignalExitCode(sig)
			_ = rec.CloseWithFields(exitCode, map[string]any{
				"reason": "signal",
				"signal": sig.String(),
			})
			os.Exit(exitCode)
		case <-done:
			return
		}
	}()
	return func() {
		stopOnce.Do(func() {
			signal.Stop(signals)
			close(done)
		})
	}
}

func telemetrySignalExitCode(sig os.Signal) int {
	switch sig {
	case os.Interrupt:
		return 130
	case syscall.SIGTERM:
		return 143
	default:
		return 1
	}
}

func run(args []string, stdout io.Writer, stderr io.Writer, promptRun promptRunner, promptsRun promptsRunner, tuiRun modeRunner, serverRun modeRunner, listSessions listSessionsRunner) int {
	return runWithTelemetry(args, stdout, stderr, promptRun, promptsRun, tuiRun, serverRun, listSessions, nil)
}

func runWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, promptRun promptRunner, promptsRun promptsRunner, tuiRun modeRunner, serverRun modeRunner, listSessions listSessionsRunner, rec *telemetry.Recorder) int {
	var configPath string
	var serverOnly bool
	var prompt string
	var turnsFile string
	var jsonEvents bool
	var resume string
	var listSessionsFlag bool
	var modelOverride string
	var showVersion bool

	fs := flag.NewFlagSet("idiom", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&configPath, "config", "config.json", "path to config file")
	fs.BoolVar(&serverOnly, "server-only", false, "run only llama-server and block")
	fs.StringVar(&prompt, "prompt", "", "run one prompt headlessly, print the answer, and exit")
	fs.StringVar(&turnsFile, "turns-file", "", "run newline-delimited or JSON-array prompts headlessly in one session, print answers, and exit")
	fs.BoolVar(&jsonEvents, "json", false, "emit structured JSONL events to stdout (only with --prompt)")
	fs.StringVar(&resume, "resume", "", "resume a prior session by id (filename without .jsonl) before running")
	fs.BoolVar(&listSessionsFlag, "list-sessions", false, "print recent session ids from the debug log directory and exit")
	fs.StringVar(&modelOverride, "model", "", "override llama_server.model from config (e.g. an alias the proxy resolves)")
	fs.BoolVar(&showVersion, "version", false, "print version and exit (same output as `idiom version`)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	// --version short-circuits before config load so `idiom --version`
	// works against any cwd, including dirs without a config.json. This
	// is the conventional CLI behavior (`tool --version` always prints
	// version regardless of other state) and keeps the flag useful for
	// debugging — pointing at the binary in $PATH and asking what
	// version it is should never fail.
	if showVersion {
		return runVersionMain(nil, stdout, stderr)
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if strings.TrimSpace(modelOverride) != "" {
		cfg.LlamaServer.Model = modelOverride
	}

	if listSessionsFlag {
		logCLIPhase(rec, "list_sessions", nil)
		if err := listSessions(cfg, stdout); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	}

	if serverOnly {
		if err := serverRun(cfg, runOptions{Telemetry: rec}); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	}

	if turnsFile != "" {
		if prompt != "" {
			fmt.Fprintln(stderr, "--prompt and --turns-file are mutually exclusive")
			return 2
		}
		prompts, err := loadPromptTurns(turnsFile)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		opts := runOptions{JSONEvents: jsonEvents, Resume: resume, Telemetry: rec}
		answers, err := promptsRun(cfg, prompts, opts, stdout)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		if !jsonEvents {
			for _, answer := range answers {
				fmt.Fprintln(stdout, answer)
			}
		}
		return 0
	}

	if prompt != "" {
		opts := runOptions{JSONEvents: jsonEvents, Resume: resume, Telemetry: rec}
		answer, err := promptRun(cfg, prompt, opts, stdout)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		if !jsonEvents {
			fmt.Fprintln(stdout, answer)
		}
		return 0
	}

	if err := tuiRun(cfg, runOptions{Resume: resume, Telemetry: rec}); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func loadPromptTurns(path string) ([]string, error) {
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("read turns file: %w", err)
	}
	var prompts []string
	if err := json.Unmarshal(data, &prompts); err == nil {
		return cleanPromptTurns(prompts)
	}
	scanner := bufio.NewScanner(bytesReader(data))
	for scanner.Scan() {
		prompts = append(prompts, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan turns file: %w", err)
	}
	return cleanPromptTurns(prompts)
}

func bytesReader(data []byte) io.Reader {
	return strings.NewReader(string(data))
}

func cleanPromptTurns(prompts []string) ([]string, error) {
	cleaned := make([]string, 0, len(prompts))
	for _, prompt := range prompts {
		prompt = strings.TrimSpace(prompt)
		if prompt != "" {
			cleaned = append(cleaned, prompt)
		}
	}
	if len(cleaned) == 0 {
		return nil, fmt.Errorf("turns file did not contain any prompts")
	}
	return cleaned, nil
}

func runToolMain(args []string, stdout io.Writer, stderr io.Writer) int {
	return runToolMainWithTelemetry(args, stdout, stderr, nil)
}

func runToolMainWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		printToolUsage(stderr)
		return 0
	}
	if args[0] == "schemas" {
		code := runToolSchemas(stdout, stderr)
		logCLIPhase(rec, "tool_schema_export", map[string]any{"exit_code": code})
		return code
	}
	if args[0] == "list" {
		// Allow `--config` after `list` to honour project-local externals.
		listFs := flag.NewFlagSet("idiom tool list", flag.ContinueOnError)
		listFs.SetOutput(stderr)
		listConfigPath := listFs.String("config", "config.json", "path to config file")
		listProfile := listFs.String("profile", workspace.ToolProfileDefault, "tool permission profile: default or discovery")
		if err := listFs.Parse(args[1:]); err != nil {
			return 2
		}
		profile, err := workspace.NormalizeToolProfile(*listProfile)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
		code := runToolListWithConfig(stdout, *listConfigPath, profile)
		logCLIPhase(rec, "tool_list", map[string]any{"exit_code": code})
		return code
	}

	toolName := args[0]
	if toolHelpRequested(args[1:]) {
		printToolHelp(stdout, toolName)
		logCLIPhase(rec, "tool_help", map[string]any{"tool": toolName, "exit_code": 0})
		return 0
	}
	toolFs := flag.NewFlagSet("idiom tool "+toolName, flag.ContinueOnError)
	toolFs.SetOutput(stderr)
	configPath := toolFs.String("config", "config.json", "path to config file")
	profileFlag := toolFs.String("profile", workspace.ToolProfileDefault, "tool permission profile: default or discovery")
	callerID := toolFs.String("caller-id", "", "opaque caller identifier for sidecar.caller metadata")
	requestID := toolFs.String("request-id", "", "opaque request identifier for sidecar.caller metadata")
	traceID := toolFs.String("trace-id", "", "opaque trace identifier for sidecar.caller metadata")
	inputJSON := toolFs.String("input", "", "JSON object with tool request parameters (reads from stdin if omitted and not a terminal)")
	if err := toolFs.Parse(args[1:]); err != nil {
		return 2
	}
	profile, err := workspace.NormalizeToolProfile(*profileFlag)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	caller, err := workspace.NewToolSidecarCaller(*callerID, *requestID, *traceID)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}

	req := workspace.ToolRequest{Tool: toolName}
	sidecarOptions := workspace.ToolSidecarOptions{Caller: caller}
	requestLogged := false
	logRequest := func(rawInputBytes int) {
		if requestLogged {
			return
		}
		logDirectToolRequest(rec, req, rawInputBytes)
		requestLogged = true
	}
	logFailureResult := func(summary string, errText string) {
		logDirectToolResult(rec, req, workspace.ToolResult{
			Tool:    toolName,
			Summary: summary,
			Error:   errText,
		}, 0)
	}

	cfg, err := loadToolConfig(*configPath)
	if err != nil {
		logRequest(0)
		logFailureResult("Direct tool setup failed", err.Error())
		fmt.Fprintln(stderr, err)
		return 1
	}

	var rawInput []byte
	if *inputJSON != "" {
		rawInput = []byte(*inputJSON)
	} else {
		stat, _ := os.Stdin.Stat()
		if (stat.Mode() & os.ModeCharDevice) == 0 {
			data, err := io.ReadAll(os.Stdin)
			if err != nil {
				errText := fmt.Sprintf("read stdin: %v", err)
				logRequest(0)
				logFailureResult("Direct tool input failed", errText)
				fmt.Fprintln(stderr, errText)
				return 1
			}
			rawInput = data
		}
	}
	if len(rawInput) > 0 {
		decoded, err := workspace.DecodeToolRequestJSON(rawInput)
		if err != nil {
			errText := fmt.Sprintf("invalid input JSON: %v", err)
			logRequest(len(rawInput))
			logFailureResult("Direct tool input rejected", errText)
			fmt.Fprintln(stderr, errText)
			return 1
		}
		req = decoded
		// Preserve the full input as Args so external tools (and any
		// future MCP-shaped tools) can pull arbitrary fields, not just
		// the ones declared on ToolRequest.
		req.Args = json.RawMessage(rawInput)
	}
	req.Tool = toolName
	logRequest(len(rawInput))

	extReg, extErr := loadExternalToolsRegistry(cfg.Workspace)
	if extErr != nil {
		errText := fmt.Sprintf("external tools setup: %v", extErr)
		logFailureResult("Direct tool setup failed", errText)
		fmt.Fprintln(stderr, errText)
		return 1
	}
	if decision := toolAllowedForCLIProfile(req, profile, extReg); !decision.Allowed {
		result := workspace.ToolProfileDeniedResult(req.Tool, profile, decision)
		logDirectToolResult(rec, req, result, 0)
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		payload := struct {
			workspace.ToolResult
			Sidecar workspace.ToolSidecarEnvelope `json:"sidecar"`
		}{
			ToolResult: result,
			Sidecar:    workspace.BuildToolSidecarEnvelopeWithOptions(req, result, profile, sidecarOptions),
		}
		if err := enc.Encode(payload); err != nil {
			fmt.Fprintf(stderr, "encode result: %v\n", err)
			return 1
		}
		return 1
	}

	mgr := workspace.NewManager(cfg.Workspace)
	mgr.SetBypassReadBeforeEdit(true)
	if extReg != nil {
		mgr.SetExternalToolsRegistry(extReg)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	startedAt := time.Now()
	result := mgr.Execute(ctx, req)
	logDirectToolResult(rec, req, result, time.Since(startedAt).Milliseconds())

	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	payload := struct {
		workspace.ToolResult
		Sidecar workspace.ToolSidecarEnvelope `json:"sidecar"`
	}{
		ToolResult: result,
		Sidecar:    workspace.BuildToolSidecarEnvelopeWithOptions(req, result, profile, sidecarOptions),
	}
	if err := enc.Encode(payload); err != nil {
		fmt.Fprintf(stderr, "encode result: %v\n", err)
		return 1
	}

	if result.Error != "" {
		return 1
	}
	return 0
}

func toolAllowedForCLIProfile(req workspace.ToolRequest, profile string, extReg *workspace.ExternalToolRegistry) workspace.ToolProfileDecision {
	decision := workspace.ToolRequestAllowedForProfile(req, profile)
	if decision.Allowed || profile != workspace.ToolProfileDiscovery || workspace.IsBuiltInToolName(req.Tool) {
		return decision
	}
	if extReg != nil && extReg.IsReadOnlyDiscovery(req.Tool) {
		return workspace.ToolProfileDecision{Allowed: true}
	}
	return decision
}

func toolHelpRequested(args []string) bool {
	skipValue := false
	for _, arg := range args {
		if skipValue {
			skipValue = false
			continue
		}
		arg = strings.TrimSpace(arg)
		switch strings.TrimSpace(arg) {
		case "help", "--help", "-h":
			return true
		case "--input", "-input", "--config", "-config", "--caller-id", "-caller-id", "--request-id", "-request-id", "--trace-id", "-trace-id":
			skipValue = true
		}
	}
	return false
}

func printToolHelp(stdout io.Writer, toolName string) {
	toolName = strings.TrimSpace(toolName)
	fmt.Fprintf(stdout, "Usage:\n  idiom tool %s [--profile default|discovery] --input '<json>'\n\n", toolName)
	fmt.Fprintln(stdout, "Output:")
	fmt.Fprintln(stdout, "  JSON with legacy Tool/Summary/Output/Error fields plus sidecar.schema_version=\"idiom.tool.v1\".")
	fmt.Fprintln(stdout, "  The sidecar envelope carries profile, caller metadata, contract, freshness, warnings, audit_event_ids, structured_output, and typed error fields.")
	fmt.Fprintln(stdout, "  --profile discovery is read-only and denies mutation, shell, destructive, and unknown side-effect tools before execution.")
	fmt.Fprintln(stdout)
	defs := workspace.BuildToolDefinitions([]string{toolName})
	if len(defs) > 0 {
		def := defs[0]
		fmt.Fprintf(stdout, "%s\n\n", def.Description)
		fmt.Fprintln(stdout, "Schema:")
		fmt.Fprintln(stdout, def.ParametersJSON)
		if example, ok := toolManifestExample(toolName); ok {
			fmt.Fprintln(stdout)
			fmt.Fprintln(stdout, "Example:")
			fmt.Fprintln(stdout, example.Request)
			if strings.TrimSpace(example.Purpose) != "" {
				fmt.Fprintf(stdout, "Purpose: %s\n", example.Purpose)
			}
		}
		return
	}
	if example, ok := toolManifestExample(toolName); ok {
		fmt.Fprintf(stdout, "%s\n\nExample:\n%s\n", example.Purpose, example.Request)
		return
	}
	fmt.Fprintf(stdout, "No built-in schema found for %q. If this is a project external tool, run `idiom tool list --config <path>` and inspect the project tool docs.\n", toolName)
}

func toolManifestExample(toolName string) (workspace.ToolManifestEntry, bool) {
	for _, entry := range workspace.BuildToolManifest([]string{toolName}) {
		if strings.TrimSpace(entry.Tool) == toolName {
			return entry, true
		}
	}
	return workspace.ToolManifestEntry{}, false
}

func runToolList(stdout io.Writer) int {
	return runToolListWithConfig(stdout, "config.json", workspace.ToolProfileDefault)
}

func runToolListWithConfig(stdout io.Writer, configPath string, profile string) int {
	allTools := workspace.ToolManifestTools(workspace.ToolManifestToolOptions{
		IncludeTaskLedger: true,
		IncludeTask:       true,
		IncludeWriteTools: true,
		IncludeGo:         true,
		IncludeCpp:        true,
		IncludePython:     true,
		IncludeTypescript: true,
		IncludeRust:       true,
		IncludeJava:       true,
	})
	for _, t := range allTools {
		if decision := workspace.ToolAllowedForProfile(t, profile); !decision.Allowed {
			continue
		}
		fmt.Fprintln(stdout, t)
	}
	// Append project-defined external tools when a config is loadable.
	// Silent on errors — `idiom tool list` is informational; if the
	// config is missing or malformed the user will see the same error
	// the moment they invoke any tool.
	if cfg, err := loadToolConfig(configPath); err == nil {
		if reg, regErr := loadExternalToolsRegistry(cfg.Workspace); regErr == nil && reg != nil {
			baseDir := cfg.Workspace.CWD
			if strings.TrimSpace(baseDir) == "" {
				baseDir = cfg.Workspace.Root
			}
			names := reg.AvailableNames(baseDir)
			sort.Strings(names)
			for _, name := range names {
				if profile == workspace.ToolProfileDiscovery && !reg.IsReadOnlyDiscovery(name) {
					continue
				}
				fmt.Fprintln(stdout, name)
			}
		}
	}
	return 0
}

func runToolSchemas(stdout io.Writer, stderr io.Writer) int {
	export, err := workspace.BuildToolSchemaExport(nil)
	if err != nil {
		fmt.Fprintf(stderr, "export tool schemas: %v\n", err)
		return 1
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(export); err != nil {
		fmt.Fprintf(stderr, "encode schema export: %v\n", err)
		return 1
	}
	return 0
}

func loadToolConfig(path string) (config.Config, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		cfg := config.Default()
		if wd, e := os.Getwd(); e == nil {
			wd = config.CanonicalizeExistingPath(wd)
			cfg.Workspace.Root = wd
			cfg.Workspace.CWD = wd
		}
		return cfg, nil
	}
	return config.Load(path)
}

func printToolUsage(stderr io.Writer) {
	fmt.Fprintln(stderr, "Usage:")
	fmt.Fprintln(stderr, "  idiom tool <tool-name> [--profile default|discovery] [--input '<json>']")
	fmt.Fprintln(stderr, "  idiom tool <tool-name> [flags]  # reads JSON from stdin when piped")
	fmt.Fprintln(stderr, "  idiom tool list [--profile default|discovery]")
	fmt.Fprintln(stderr, "  idiom tool schemas")
	fmt.Fprintln(stderr)
	fmt.Fprintln(stderr, "Run a single workspace tool and print its result as JSON.")
	fmt.Fprintln(stderr, "Output preserves legacy Tool/Summary/Output/Error fields and adds sidecar.schema_version=\"idiom.tool.v1\" for structured integrations.")
	fmt.Fprintln(stderr, "The sidecar envelope includes profile, caller metadata, contract, freshness, warnings, audit_event_ids, structured_output, and typed error fields.")
	fmt.Fprintln(stderr, "Caller metadata flags accept short opaque identifiers only and reject common secret-, token-, email-, and path-shaped values: --caller-id, --request-id, and --trace-id.")
	fmt.Fprintln(stderr, "--profile discovery is read-only/default-deny for mutation, shell, destructive, and unknown side-effect tools; denied calls return a structured sidecar error without executing.")
	fmt.Fprintln(stderr, "Does not require llama-server.")
	fmt.Fprintln(stderr, "For persistent codebase context, use `idiom context build|status|query|zoom|pack|impact|trace`.")
	fmt.Fprintln(stderr)
	fmt.Fprintln(stderr, "Discovery tools:")
	fmt.Fprintln(stderr, "  harness_audit      Audit idiom tool surfaces, backends, and behavior logs")
	fmt.Fprintln(stderr, "  harness_feedback   Record structured idiom harness/tool/prompt feedback into telemetry")
	fmt.Fprintln(stderr, "  context_index      Build or refresh the persistent codebase comprehension graph")
	fmt.Fprintln(stderr, "  context_status     Report context graph freshness, stale files, and stale symbols")
	fmt.Fprintln(stderr, "  context_query      Query graph nodes and relationships at repo/package/file/symbol resolution")
	fmt.Fprintln(stderr, "  context_zoom       Expand one graph handle with neighboring evidence")
	fmt.Fprintln(stderr, "  context_pack       Generate a packet-ready context view")
	fmt.Fprintln(stderr, "  context_impact     Rank task-relevant files, symbols, and tests")
	fmt.Fprintln(stderr, "  context_trace_pack Save language-agnostic runtime trace evidence from a command")
	fmt.Fprintln(stderr, "  discovery_query     Multi-layer analysis (impact, tracing, architecture, security, history)")
	fmt.Fprintln(stderr, "  structural_search   AST-aware pattern search (requires ast-grep)")
	fmt.Fprintln(stderr, "  dependency_graph    Language-aware dependency analysis")
	fmt.Fprintln(stderr, "  history_range       Git log for a line range")
	fmt.Fprintln(stderr, "  history_symbol      Git log for a symbol")
	fmt.Fprintln(stderr, "  blame_range         Git blame for a line range")
	fmt.Fprintln(stderr, "  ctags_symbols       Symbol indexing (requires ctags)")
	fmt.Fprintln(stderr, "  semgrep_scan        Semgrep security/pattern scan")
	fmt.Fprintln(stderr, "  joern_scan          Joern code property graph scan")
	fmt.Fprintln(stderr)
	fmt.Fprintln(stderr, "Use \"idiom tool list\" to see all available tools.")
	fmt.Fprintln(stderr)
	fmt.Fprintln(stderr, "Flags:")
	fmt.Fprintln(stderr, "  -config string  path to config file (default \"config.json\")")
	fmt.Fprintln(stderr, "  -input string   JSON object with tool request parameters")
}

func runTUI(cfg config.Config, opts runOptions) error {
	model := app.New(cfg)
	closeModel := modelCloseout(opts.Telemetry, model, "tui", opts.TaskFamily)
	unregisterCloseout := opts.Telemetry.RegisterCloseout(closeModel)
	defer closeModel()
	defer unregisterCloseout()
	logModelSessionLink(opts.Telemetry, "tui", model.DebugLogPath())

	if opts.Resume != "" {
		if err := model.ResumeSession(opts.Resume); err != nil {
			return fmt.Errorf("resume %q: %w", opts.Resume, err)
		}
	}

	program := tea.NewProgram(model, tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := program.Run(); err != nil {
		return err
	}
	return nil
}

func runPromptOnce(cfg config.Config, prompt string, opts runOptions, stdout io.Writer) (string, error) {
	model := app.New(cfg)
	closeModel := modelCloseout(opts.Telemetry, model, "prompt", opts.TaskFamily)
	unregisterCloseout := opts.Telemetry.RegisterCloseout(closeModel)
	defer closeModel()
	defer unregisterCloseout()
	model.SetForcedTaskFamily(opts.TaskFamily)
	model.SetPlannerOwnedVerification(opts.PlannerOwnedVerification)
	model.SetMaxTokensOverride(opts.MaxTokensOverride)
	model.SetDisableThinkingOverride(opts.DisableThinking)
	logModelSessionLink(opts.Telemetry, "prompt", model.DebugLogPath())

	if opts.Resume != "" {
		path := app.ResolveSessionLogPath(cfg, opts.Resume)
		if err := model.LoadHistoryFromSession(path); err != nil {
			return "", fmt.Errorf("resume %q: %w", opts.Resume, err)
		}
	}

	if opts.JSONEvents {
		sessionID := ""
		if path := model.DebugLogPath(); path != "" {
			sessionID = sessionIDFromPath(path)
		}
		model.SetJSONEventStream(stdout, sessionID)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.Chat.RequestTimeoutSeconds+cfg.Chat.ServerStartTimeoutSecs)*time.Second)
	defer cancel()
	return model.RunPromptOnce(ctx, prompt)
}

func runPromptsOnce(cfg config.Config, prompts []string, opts runOptions, stdout io.Writer) ([]string, error) {
	model := app.New(cfg)
	closeModel := modelCloseout(opts.Telemetry, model, "turns_file", opts.TaskFamily)
	unregisterCloseout := opts.Telemetry.RegisterCloseout(closeModel)
	defer closeModel()
	defer unregisterCloseout()
	model.SetForcedTaskFamily(opts.TaskFamily)
	model.SetPlannerOwnedVerification(opts.PlannerOwnedVerification)
	model.SetMaxTokensOverride(opts.MaxTokensOverride)
	model.SetDisableThinkingOverride(opts.DisableThinking)
	logModelSessionLink(opts.Telemetry, "turns_file", model.DebugLogPath())

	if opts.Resume != "" {
		path := app.ResolveSessionLogPath(cfg, opts.Resume)
		if err := model.LoadHistoryFromSession(path); err != nil {
			return nil, fmt.Errorf("resume %q: %w", opts.Resume, err)
		}
	}

	if opts.JSONEvents {
		sessionID := ""
		if path := model.DebugLogPath(); path != "" {
			sessionID = sessionIDFromPath(path)
		}
		model.SetJSONEventStream(stdout, sessionID)
	}

	answers := make([]string, 0, len(prompts))
	for _, prompt := range prompts {
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.Chat.RequestTimeoutSeconds+cfg.Chat.ServerStartTimeoutSecs)*time.Second)
		answer, err := model.RunPromptOnce(ctx, prompt)
		cancel()
		if err != nil {
			return answers, err
		}
		answers = append(answers, answer)
	}
	return answers, nil
}

func modelCloseout(rec *telemetry.Recorder, model *app.Model, mode string, taskFamily string) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			sessionID := ""
			debugLogPath := ""
			if model != nil {
				debugLogPath = model.DebugLogPath()
				if debugLogPath != "" {
					sessionID = sessionIDFromPath(debugLogPath)
				}
			}
			if strings.TrimSpace(taskFamily) == "implementation" {
				fields := map[string]any{
					"mode":          mode,
					"task_family":   taskFamily,
					"session_id":    sessionID,
					"summary_event": "session_summary",
				}
				if strings.TrimSpace(debugLogPath) != "" {
					fields["debug_log_path"] = filepath.Clean(debugLogPath)
				}
				logCLIPhase(rec, "implement_session_closeout", fields)
			}
			if model != nil {
				model.Close()
			}
		})
	}
}

func sessionIDFromPath(path string) string {
	base := filepath.Base(path)
	if ext := filepath.Ext(base); ext != "" {
		base = base[:len(base)-len(ext)]
	}
	return base
}

func startCommandTelemetry(args []string) *telemetry.Recorder {
	wd, _ := os.Getwd()
	wd = config.CanonicalizeExistingPath(wd)
	rec, err := telemetry.Open(telemetry.Options{
		Command:       commandTelemetryName(args),
		Args:          commandTelemetryArgs(args),
		WorkspaceRoot: wd,
		// Stamp the binary version into telemetry so debugging from
		// a log file alone can attribute behavior to a specific build
		// (e.g. "this run was on v0.2.1"). The version variable is
		// the same one `idiom version` and `idiom --version` print —
		// see cmd/idiom/version.go.
		Version: version,
	})
	if err != nil {
		return nil
	}
	return rec
}

func commandTelemetryArgs(args []string) []string {
	if len(args) >= 2 && args[0] == "local-ops" && args[1] == "benchmark" {
		return sanitizeLocalOpsBenchmarkTelemetryArgs(args)
	}
	if len(args) >= 2 && args[0] == "product-evidence" && args[1] == "export" {
		return sanitizeProductEvidenceTelemetryArgs(args)
	}
	return args
}

func sanitizeLocalOpsBenchmarkTelemetryArgs(args []string) []string {
	out := append([]string(nil), args...)
	for i := 0; i < len(out); i++ {
		arg := strings.TrimSpace(out[i])
		if flag, value, ok := strings.Cut(arg, "="); ok {
			switch flag {
			case "--replay-log", "--replay-glob":
				out[i] = flag + "=" + redactedLocalOpsReplayArg(value)
			}
			continue
		}
		switch arg {
		case "--replay-log", "--replay-glob":
			if i+1 < len(out) {
				i++
				out[i] = redactedLocalOpsReplayArg(out[i])
			}
		}
	}
	return out
}

func redactedLocalOpsReplayArg(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "<empty>"
	}
	return "<redacted_replay_log id=" + localOpsStableID(value) + ">"
}

func commandTelemetryName(args []string) string {
	if len(args) == 0 {
		return "tui"
	}
	switch args[0] {
	case "tool":
		if len(args) > 1 {
			return "tool:" + args[1]
		}
		return "tool"
	case "context":
		if len(args) > 1 {
			return "context:" + args[1]
		}
		return "context"
	case "artifact", "implement", "doctor":
		return args[0]
	case "local-ops":
		if len(args) > 1 {
			return "local-ops:" + args[1]
		}
		return "local-ops"
	case "product-evidence":
		if len(args) > 1 {
			return "product-evidence:" + args[1]
		}
		return "product-evidence"
	case "lane":
		if len(args) > 1 {
			return "lane:" + args[1]
		}
		return "lane"
	case "architect":
		if len(args) > 1 {
			return "architect:" + args[1]
		}
		return "architect"
	case "audit":
		if len(args) > 1 {
			return "audit:" + args[1]
		}
		return "audit"
	case "ranker":
		if len(args) > 1 {
			return "ranker:" + args[1]
		}
		return "ranker"
	case "brief":
		if len(args) > 1 {
			return "brief:" + args[1]
		}
		return "brief"
	default:
		for _, arg := range args {
			if strings.HasPrefix(arg, "-prompt=") || strings.HasPrefix(arg, "--prompt=") {
				return "prompt"
			}
			if strings.HasPrefix(arg, "-turns-file=") || strings.HasPrefix(arg, "--turns-file=") {
				return "turns_file"
			}
			switch arg {
			case "-server-only", "--server-only":
				return "server"
			case "-prompt", "--prompt":
				return "prompt"
			case "-turns-file", "--turns-file":
				return "turns_file"
			case "-list-sessions", "--list-sessions":
				return "list_sessions"
			}
		}
		return "tui"
	}
}

func logCLIPhase(rec *telemetry.Recorder, eventType string, fields map[string]any) {
	if rec == nil {
		return
	}
	if fields == nil {
		fields = map[string]any{}
	}
	_ = rec.Log(eventType, fields)
}

func logModelSessionLink(rec *telemetry.Recorder, mode string, debugLogPath string) {
	if rec == nil || strings.TrimSpace(debugLogPath) == "" {
		return
	}
	_ = rec.Log("model_session", map[string]any{
		"mode":           mode,
		"debug_log_path": filepath.Clean(debugLogPath),
		"session_id":     sessionIDFromPath(debugLogPath),
	})
}

func logDirectToolRequest(rec *telemetry.Recorder, req workspace.ToolRequest, rawInputBytes int) {
	if rec == nil {
		return
	}
	profile := workspace.DiscoveryProfileForRequest(req)
	fields := map[string]any{
		"step":            1,
		"agent":           "direct",
		"tool":            strings.TrimSpace(req.Tool),
		"raw_input_bytes": rawInputBytes,
		"path":            strings.TrimSpace(req.Path),
		"workdir":         strings.TrimSpace(req.Workdir),
		"purpose":         strings.TrimSpace(req.Purpose),
		"language":        strings.TrimSpace(req.Language),
		"has_pattern":     strings.TrimSpace(req.Pattern) != "",
		"has_query":       strings.TrimSpace(req.Query) != "",
		"has_command":     strings.TrimSpace(req.Command) != "",
		"has_content":     strings.TrimSpace(req.Content) != "",
		"start_line":      req.StartLine,
		"end_line":        req.EndLine,
		"limit":           req.Limit,
		"recursive":       req.Recursive,
	}
	if profile.Discovery {
		fields["discovery_class"] = profile.Class
		fields["discovery_scope"] = profile.Scope
		fields["discovery_cost"] = profile.Cost
	}
	_ = rec.Log("tool_request", fields)
}

func logDirectToolResult(rec *telemetry.Recorder, req workspace.ToolRequest, result workspace.ToolResult, durationMillis int64) {
	if rec == nil {
		return
	}
	outputChars := len(result.Output)
	tool := firstNonEmptyCLIString(result.Tool, req.Tool)
	fields := map[string]any{
		"step":        1,
		"agent":       "direct",
		"tool":        tool,
		"summary":     result.Summary,
		"error":       result.Error,
		"duration_ms": durationMillis,
	}
	if outputChars > 0 {
		fields["output_chars"] = outputChars
		fields["output_lines"] = outputLineCount(result.Output)
	}
	if result.PrecisionTier != "" {
		fields["precision_tier"] = result.PrecisionTier
	}
	profile := workspace.DiscoveryProfileForRequest(req)
	if profile.Discovery {
		fields["discovery_class"] = profile.Class
		fields["discovery_scope"] = profile.Scope
		fields["discovery_cost"] = profile.Cost
	}
	for key, value := range workspace.ToolTelemetryFields(tool, result.Output) {
		fields[key] = value
	}
	_ = rec.Log("tool_result", fields)
}

func outputLineCount(output string) int {
	if output == "" {
		return 0
	}
	lines := strings.Count(output, "\n")
	if !strings.HasSuffix(output, "\n") {
		lines++
	}
	return lines
}

func firstNonEmptyCLIString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func runListSessions(cfg config.Config, stdout io.Writer) error {
	sessions, err := app.ListRecordedSessions(cfg)
	if err != nil {
		return err
	}
	if len(sessions) == 0 {
		fmt.Fprintln(stdout, "no sessions found")
		return nil
	}
	for _, s := range sessions {
		summary := s.FirstPrompt
		if summary == "" {
			summary = "(no user prompt recorded)"
		}
		fmt.Fprintf(stdout, "%s\tmsgs=%d/%d\t%s\n", s.ID, s.UserMessages, s.AssistMessage, summary)
	}
	return nil
}

// loadExternalToolsRegistry mirrors internal/app/external_tools_setup.go's
// setup function for the standalone `idiom tool` CLI path. Kept here as a
// thin local helper so cmd/ stays decoupled from internal/app — the
// shared semantics are documented in workspace.NewExternalToolRegistry.
func loadExternalToolsRegistry(ws config.Workspace) (*workspace.ExternalToolRegistry, error) {
	tools := make([]workspace.ExternalTool, 0, len(ws.ExternalTools))
	for _, def := range ws.ExternalTools {
		tools = append(tools, workspace.ExternalTool{
			Name:           def.Name,
			Description:    def.Description,
			Command:        def.Command,
			Args:           append([]string(nil), def.Args...),
			Cwd:            def.Cwd,
			TimeoutSeconds: def.TimeoutSeconds,
			InputSchema:    def.InputSchema,
			ReadOnly:       def.ReadOnly,
			Discovery:      def.Discovery,
		})
	}
	if dir := ws.ExternalToolsDir; dir != "" {
		dirPath := dir
		if !filepath.IsAbs(dirPath) && ws.Root != "" {
			dirPath = filepath.Join(ws.Root, dirPath)
		}
		entries, err := os.ReadDir(dirPath)
		if err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("external_tools_dir %q: %w", dirPath, err)
		}
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
				continue
			}
			path := filepath.Join(dirPath, entry.Name())
			raw, err := os.ReadFile(path)
			if err != nil {
				return nil, fmt.Errorf("external tool file %q: %w", path, err)
			}
			var def config.ExternalToolDef
			if err := json.Unmarshal(raw, &def); err != nil {
				return nil, fmt.Errorf("external tool file %q: parse: %w", path, err)
			}
			tools = append(tools, workspace.ExternalTool{
				Name:           def.Name,
				Description:    def.Description,
				Command:        def.Command,
				Args:           append([]string(nil), def.Args...),
				Cwd:            def.Cwd,
				TimeoutSeconds: def.TimeoutSeconds,
				InputSchema:    def.InputSchema,
				ReadOnly:       def.ReadOnly,
				Discovery:      def.Discovery,
			})
		}
	}
	if len(tools) == 0 {
		return nil, nil
	}
	return workspace.NewExternalToolRegistry(tools)
}

func runServerOnly(cfg config.Config, opts runOptions) error {
	return runServerOnlyWithTelemetry(cfg, opts)
}

func runServerOnlyWithTelemetry(cfg config.Config, opts runOptions) error {
	client := llama.NewClient(cfg.BaseURL(), 0).WithBackend(cfg.LlamaServer.Kind)
	controller := llama.NewServerController(cfg.LlamaServer, client)
	logCLIPhase(opts.Telemetry, "server_starting", map[string]any{
		"base_url": cfg.BaseURL(),
		"backend":  cfg.LlamaServer.Kind,
	})

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.Chat.ServerStartTimeoutSecs)*time.Second)
	defer cancel()

	if err := controller.Start(ctx); err != nil {
		logCLIPhase(opts.Telemetry, "server_error", map[string]any{"stage": "start", "error": err.Error()})
		return err
	}
	if err := controller.WaitUntilReady(ctx); err != nil {
		logCLIPhase(opts.Telemetry, "server_error", map[string]any{"stage": "wait_until_ready", "error": err.Error()})
		return err
	}
	logCLIPhase(opts.Telemetry, "server_ready", map[string]any{
		"base_url": cfg.BaseURL(),
		"log_path": controller.LogPath(),
	})

	fmt.Printf("llama-server ready on %s\n", cfg.BaseURL())
	fmt.Printf("model: %s\n", cfg.LlamaServer.Model)
	fmt.Printf("log: %s\n", controller.LogPath())

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	<-sigCh

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer stopCancel()

	if err := controller.Stop(stopCtx); err != nil {
		logCLIPhase(opts.Telemetry, "server_error", map[string]any{"stage": "stop", "error": err.Error()})
		return err
	}
	logCLIPhase(opts.Telemetry, "server_stopped", map[string]any{
		"base_url": cfg.BaseURL(),
	})
	return nil
}
