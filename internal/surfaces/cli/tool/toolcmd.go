package tool

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/evidence/telemetry"
	cmdconfig "idiom/internal/surfaces/cli/config"
)

func RunWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		printUsage(stderr)

		return 0
	}

	if args[0] == "schemas" {
		code := RunSchemas(stdout, stderr)
		telemetry.LogPhase(rec, "tool_schema_export", map[string]any{"exit_code": code})

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
		telemetry.LogPhase(rec, "tool_list", map[string]any{"exit_code": code})

		return code
	}

	toolName := args[0]
	if HelpRequested(args[1:]) {
		printHelp(stdout, toolName)
		telemetry.LogPhase(rec, "tool_help", map[string]any{"tool": toolName, "exit_code": 0})

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
	effectiveTimeout := workspace.ToolTimeoutForRequest(req)
	requestLogged := false
	logRequest := func(rawInputBytes int) {
		if requestLogged {
			return
		}

		if rec != nil {
			_ = rec.Log("tool_request", workspace.ToolRequestTelemetryFields(req, workspace.ToolTelemetryRequestOptions{
				Agent:         "direct",
				Transport:     "cli",
				Profile:       profile,
				Caller:        caller,
				RawInputBytes: rawInputBytes,
				TimeoutOwner:  directToolTimeoutOwner(effectiveTimeout),
				RequestFields: workspace.ToolTelemetryRequestValues,
			}))
		}

		requestLogged = true
	}
	logResult := func(result workspace.ToolResult, durationMillis int64) {
		if rec != nil {
			_ = rec.Log("tool_result", workspace.ToolResultTelemetryFields(req, result, workspace.ToolTelemetryResultOptions{
				Agent:          "direct",
				Transport:      "cli",
				Profile:        profile,
				Caller:         caller,
				DurationMillis: durationMillis,
				TimeoutOwner:   directToolTimeoutOwner(effectiveTimeout),
			}))
		}
	}
	logFailureResult := func(summary string, errText string) {
		logResult(workspace.ToolResult{
			Tool:    toolName,
			Summary: summary,
			Error:   errText,
		}, 0)
	}

	cfg, err := cmdconfig.Load(*configPath)
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

	surfaceSetup := workspace.ConfigureToolSurface(context.Background(), cfg, workspace.ConfiguredToolSurfaceOptions{
		Profile: profile,
		Request: req,
	})
	defer surfaceSetup.Close()

	if extErr := surfaceSetup.ExternalToolsError; extErr != nil {
		errText := fmt.Sprintf("external tools setup: %v", extErr)
		logRequest(len(rawInput))
		logFailureResult("Direct tool setup failed", errText)
		fmt.Fprintln(stderr, errText)

		return 1
	}

	mgr := surfaceSetup.Manager
	mgr.SetBypassReadBeforeEdit(true)
	effectiveTimeout = mgr.ToolTimeoutForRequest(req)
	logRequest(len(rawInput))

	if decision := workspace.ToolRequestAllowedForWorkspaceProfile(req, profile, surfaceSetup.PolicyContext); !decision.Allowed {
		result := workspace.ToolProfileDeniedResult(req.Tool, profile, decision)
		logResult(result, 0)

		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")

		payload := struct {
			workspace.ToolResult
			Sidecar      workspace.ToolSidecarEnvelope `json:"sidecar"`
			TimeoutOwner string                        `json:"timeout_owner,omitempty"`
		}{
			ToolResult:   result,
			Sidecar:      workspace.BuildToolSidecarEnvelopeWithOptions(req, result, profile, sidecarOptions),
			TimeoutOwner: directToolTimeoutOwner(effectiveTimeout),
		}
		if err := enc.Encode(payload); err != nil {
			fmt.Fprintf(stderr, "encode result: %v\n", err)

			return 1
		}

		return 1
	}

	if mcpFailures := surfaceSetup.MCPFailures; len(mcpFailures) > 0 &&
		(surfaceSetup.MCPRegistry == nil || !surfaceSetup.MCPRegistry.Has(req.Tool)) {

		errText := "mcp setup: " + strings.Join(mcpFailures, "; ")
		logFailureResult("Direct tool setup failed", errText)
		fmt.Fprintln(stderr, errText)

		return 1
	}

	ctx, cancel := directToolContext(effectiveTimeout)
	defer cancel()

	startedAt := time.Now()
	result := mgr.Execute(ctx, req)
	logResult(result, time.Since(startedAt).Milliseconds())

	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")

	payload := struct {
		workspace.ToolResult
		Sidecar      workspace.ToolSidecarEnvelope `json:"sidecar"`
		TimeoutOwner string                        `json:"timeout_owner,omitempty"`
	}{
		ToolResult:   result,
		Sidecar:      workspace.BuildToolSidecarEnvelopeWithOptions(req, result, profile, sidecarOptions),
		TimeoutOwner: directToolTimeoutOwner(effectiveTimeout),
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

func directToolContext(timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return context.WithCancel(context.Background())
	}

	return context.WithTimeout(context.Background(), timeout+5*time.Second)
}

func directToolTimeoutOwner(timeout time.Duration) string {
	if timeout <= 0 {
		return "tool_internal_timeout"
	}

	return "workspace_tool_timeout_with_cli_margin"
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
	cfg, _ := cmdconfig.Load(configPath)

	surfaceSetup := workspace.ConfigureToolSurface(context.Background(), cfg, workspace.ConfiguredToolSurfaceOptions{
		Profile: profile,
		List:    true,
	})
	defer surfaceSetup.Close()

	for _, tool := range surfaceSetup.ListToolNamesForProfile(profile) {
		fmt.Fprintln(stdout, tool)
	}

	return 0
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
	fmt.Fprintln(stderr, "For persistent codebase context, use `idiom tool context_status --input '{...}'` and related `context_*` tools.")
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
