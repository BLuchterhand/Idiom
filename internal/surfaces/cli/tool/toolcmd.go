package tool

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"idiom/internal/capabilities/mcp"
	"idiom/internal/capabilities/workspace"
	"idiom/internal/evidence/telemetry"
	"idiom/internal/platform/config"
)

func Run(args []string, stdout io.Writer, stderr io.Writer) int {
	return RunWithTelemetry(args, stdout, stderr, nil)
}

func RunWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		printUsage(stderr)

		return 0
	}

	if args[0] == "schemas" {
		code := RunSchemas(stdout, stderr)
		LogCLIPhase(rec, "tool_schema_export", map[string]any{"exit_code": code})

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

		code := RunListWithConfig(stdout, *listConfigPath, profile)
		LogCLIPhase(rec, "tool_list", map[string]any{"exit_code": code})

		return code
	}

	toolName := args[0]
	if HelpRequested(args[1:]) {
		printHelp(stdout, toolName)
		LogCLIPhase(rec, "tool_help", map[string]any{"tool": toolName, "exit_code": 0})

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

		LogDirectToolRequest(rec, req, rawInputBytes, profile, caller)

		requestLogged = true
	}
	logFailureResult := func(summary string, errText string) {
		LogDirectToolResult(rec, req, workspace.ToolResult{
			Tool:    toolName,
			Summary: summary,
			Error:   errText,
		}, 0, profile, caller)
	}

	cfg, err := LoadConfig(*configPath)
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

	extReg, extErr := LoadExternalToolsRegistry(cfg.Workspace)
	if extErr != nil {
		errText := fmt.Sprintf("external tools setup: %v", extErr)
		logFailureResult("Direct tool setup failed", errText)
		fmt.Fprintln(stderr, errText)

		return 1
	}

	if decision := directToolAllowedForProfile(req, profile, cfg.Workspace, extReg); !decision.Allowed {
		result := workspace.ToolProfileDeniedResult(req.Tool, profile, decision)
		LogDirectToolResult(rec, req, result, 0, profile, caller)

		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")

		payload := struct {
			workspace.ToolResult
			Sidecar      workspace.ToolSidecarEnvelope `json:"sidecar"`
			TimeoutOwner string                        `json:"timeout_owner,omitempty"`
		}{
			ToolResult:   result,
			Sidecar:      workspace.BuildToolSidecarEnvelopeWithOptions(req, result, profile, sidecarOptions),
			TimeoutOwner: directToolTimeoutOwner(req),
		}
		if err := enc.Encode(payload); err != nil {
			fmt.Fprintf(stderr, "encode result: %v\n", err)

			return 1
		}

		return 1
	}

	var mcpReg *mcp.Registry
	if strings.HasPrefix(req.Tool, "mcp:") {
		mcpCtx, mcpCancel := context.WithTimeout(context.Background(), 30*time.Second)
		var mcpFailures []string
		mcpReg, mcpFailures = mcp.SetupRegistry(mcpCtx, cfg.MCP, cfg.Workspace.AllowPrivateNetwork)
		mcpCancel()
		if mcpReg != nil {
			defer mcpReg.Close()
		}

		if len(mcpFailures) > 0 && (mcpReg == nil || !mcpReg.Has(req.Tool)) {
			errText := "mcp setup: " + strings.Join(mcpFailures, "; ")
			logFailureResult("Direct tool setup failed", errText)
			fmt.Fprintln(stderr, errText)

			return 1
		}
	}

	mgr := workspace.NewManager(cfg.Workspace)
	mgr.SetBypassReadBeforeEdit(true)

	if extReg != nil {
		mgr.SetExternalToolsRegistry(extReg)
	}

	if mcpReg != nil {
		mgr.SetMCPRegistry(mcpReg)
	}

	ctx, cancel := directToolContext(req)
	defer cancel()

	startedAt := time.Now()
	result := mgr.Execute(ctx, req)
	LogDirectToolResult(rec, req, result, time.Since(startedAt).Milliseconds(), profile, caller)

	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")

	payload := struct {
		workspace.ToolResult
		Sidecar      workspace.ToolSidecarEnvelope `json:"sidecar"`
		TimeoutOwner string                        `json:"timeout_owner,omitempty"`
	}{
		ToolResult:   result,
		Sidecar:      workspace.BuildToolSidecarEnvelopeWithOptions(req, result, profile, sidecarOptions),
		TimeoutOwner: directToolTimeoutOwner(req),
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

func directToolContext(req workspace.ToolRequest) (context.Context, context.CancelFunc) {
	timeout := workspace.ToolTimeoutForRequest(req)
	if timeout <= 0 {
		return context.WithCancel(context.Background())
	}

	return context.WithTimeout(context.Background(), timeout+5*time.Second)
}

func directToolTimeoutOwner(req workspace.ToolRequest) string {
	timeout := workspace.ToolTimeoutForRequest(req)
	if timeout <= 0 {
		return "tool_internal_timeout"
	}

	return "workspace_tool_timeout_with_cli_margin"
}

func directToolAllowedForProfile(req workspace.ToolRequest, profile string, cfg config.Workspace, extReg *workspace.ExternalToolRegistry) workspace.ToolProfileDecision {
	decision := workspace.ToolRequestAllowedForProfile(req, profile)
	if decision.Allowed || profile != workspace.ToolProfileDiscovery || workspace.IsBuiltInToolName(req.Tool) {
		return decision
	}

	if extReg != nil && extReg.IsReadOnlyDiscovery(req.Tool) {
		return workspace.ToolProfileDecision{Allowed: true}
	}

	if strings.HasPrefix(strings.TrimSpace(req.Tool), "mcp:") && workspace.MatchConfiguredToolRule(req.Tool, req, cfg.ToolRules) == workspace.ToolRuleAllow {
		return workspace.ToolProfileDecision{Allowed: true}
	}

	return decision
}

func HelpRequested(args []string) bool {
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

func printHelp(stdout io.Writer, toolName string) {
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

func RunListWithConfig(stdout io.Writer, configPath string, profile string) int {
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
	// Silent on errors: `idiom tool list` is informational; if the
	// config is missing or malformed the user will see the same error
	// the moment they invoke any tool.
	if cfg, err := LoadConfig(configPath); err == nil {
		if reg, regErr := LoadExternalToolsRegistry(cfg.Workspace); regErr == nil && reg != nil {
			baseDir := cfg.Workspace.CWD
			if strings.TrimSpace(baseDir) == "" {
				baseDir = cfg.Workspace.Root
			}

			names := reg.AvailableNames(baseDir)
			slices.Sort(names)

			for _, name := range names {
				if profile == workspace.ToolProfileDiscovery && !reg.IsReadOnlyDiscovery(name) {
					continue
				}

				fmt.Fprintln(stdout, name)
			}
		}

		if profile != workspace.ToolProfileDiscovery || hasConfiguredMCPAllowRule(cfg.Workspace.ToolRules) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			mcpReg, _ := mcp.SetupRegistry(ctx, cfg.MCP, cfg.Workspace.AllowPrivateNetwork)
			cancel()
			if mcpReg != nil {
				defer mcpReg.Close()

				names := make([]string, 0, len(mcpReg.Tools()))
				for name := range mcpReg.Tools() {
					if profile == workspace.ToolProfileDiscovery &&
						workspace.MatchConfiguredToolRule(name, workspace.ToolRequest{Tool: name}, cfg.Workspace.ToolRules) != workspace.ToolRuleAllow {
						continue
					}
					names = append(names, name)
				}
				slices.Sort(names)

				for _, name := range names {
					fmt.Fprintln(stdout, name)
				}
			}
		}
	}

	return 0
}

func hasConfiguredMCPAllowRule(rules map[string][]config.ToolRule) bool {
	for tool, list := range rules {
		if !strings.HasPrefix(strings.TrimSpace(tool), "mcp:") {
			continue
		}
		for _, rule := range list {
			if strings.EqualFold(strings.TrimSpace(rule.Decision), "allow") {
				return true
			}
		}
	}

	return false
}

func RunSchemas(stdout io.Writer, stderr io.Writer) int {
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

func LoadConfig(path string) (config.Config, error) {
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

func printUsage(stderr io.Writer) {
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

func LogCLIPhase(rec *telemetry.Recorder, eventType string, fields map[string]any) {
	if rec == nil {
		return
	}

	if fields == nil {
		fields = map[string]any{}
	}

	_ = rec.Log(eventType, fields)
}

func LogDirectToolRequest(rec *telemetry.Recorder, req workspace.ToolRequest, rawInputBytes int, profile string, caller workspace.ToolSidecarCaller) {
	if rec == nil {
		return
	}

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
		"timeout_owner":   directToolTimeoutOwner(req),
	}
	AddToolAttributionTelemetry(fields, workspace.ToolSidecarEnvelopeVersion, "cli", strings.TrimSpace(profile), caller, strings.TrimSpace(req.Tool))

	discoveryProfile := workspace.DiscoveryProfileForRequest(req)
	if discoveryProfile.Discovery {
		fields["discovery_class"] = discoveryProfile.Class
		fields["discovery_scope"] = discoveryProfile.Scope
		fields["discovery_cost"] = discoveryProfile.Cost
	}

	_ = rec.Log("tool_request", fields)
}

func LogDirectToolResult(rec *telemetry.Recorder, req workspace.ToolRequest, result workspace.ToolResult, durationMillis int64, profile string, caller workspace.ToolSidecarCaller) {
	if rec == nil {
		return
	}

	outputChars := len(result.Output)
	tool := FirstNonEmptyString(result.Tool, req.Tool)
	fields := map[string]any{
		"step":          1,
		"agent":         "direct",
		"tool":          tool,
		"summary":       result.Summary,
		"error":         result.Error,
		"duration_ms":   durationMillis,
		"timeout_owner": directToolTimeoutOwner(req),
	}
	AddToolAttributionTelemetry(fields, workspace.ToolSidecarEnvelopeVersion, "cli", strings.TrimSpace(profile), caller, tool)

	if outputChars > 0 {
		fields["output_chars"] = outputChars
		fields["output_lines"] = OutputLineCount(result.Output)
	}

	if result.PrecisionTier != "" {
		fields["precision_tier"] = result.PrecisionTier
	}

	discoveryProfile := workspace.DiscoveryProfileForRequest(req)
	if discoveryProfile.Discovery {
		fields["discovery_class"] = discoveryProfile.Class
		fields["discovery_scope"] = discoveryProfile.Scope
		fields["discovery_cost"] = discoveryProfile.Cost
	}

	for key, value := range workspace.ToolTelemetryFields(tool, result.Output) {
		fields[key] = value
	}

	_ = rec.Log("tool_result", fields)
}

func AddToolAttributionTelemetry(fields map[string]any, sidecarSchemaVersion string, transport string, profile string, caller workspace.ToolSidecarCaller, tool string) {
	fields["transport"] = transport
	fields["profile"] = profile
	fields["sidecar_schema_version"] = sidecarSchemaVersion
	fields["contract_id"] = "idiom.tool.v1:" + strings.TrimSpace(tool)
	fields["contract_kind"] = "workspace_tool_result"
	fields["contract_schema_version"] = workspace.ToolSchemaExportVersion

	if caller.CallerID != "" {
		fields["caller_id"] = caller.CallerID
	}

	if caller.RequestID != "" {
		fields["request_id"] = caller.RequestID
	}

	if caller.TraceID != "" {
		fields["trace_id"] = caller.TraceID
	}
}

func OutputLineCount(output string) int {
	if output == "" {
		return 0
	}

	lines := strings.Count(output, "\n")
	if !strings.HasSuffix(output, "\n") {
		lines++
	}

	return lines
}

func FirstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}

	return ""
}

// LoadExternalToolsRegistry mirrors internal/surfaces/tui/app/external_tools_setup.go's
// setup function for the standalone `idiom tool` CLI path. The shared external
// tool execution semantics remain in workspace.NewExternalToolRegistry.
func LoadExternalToolsRegistry(ws config.Workspace) (*workspace.ExternalToolRegistry, error) {
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
