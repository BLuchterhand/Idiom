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
	"regexp"
	"strings"
	"time"

	"idiom/internal/config"
	mcppkg "idiom/internal/mcp"
	"idiom/internal/workspace"
)

const mcpDiscoveryProfile = workspace.ToolProfileDiscovery

type mcpRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type mcpRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *mcpRPCError    `json:"error,omitempty"`
}

type mcpRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type mcpToolContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type mcpToolCallResult struct {
	Content []mcpToolContent `json:"content"`
	IsError bool             `json:"isError,omitempty"`
}

type mcpToolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

func runMCPMainWithTelemetry(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		printMCPUsage(stderr)
		return 0
	}
	if args[0] != "serve" {
		fmt.Fprintf(stderr, "unknown mcp subcommand %q\n", args[0])
		printMCPUsage(stderr)
		return 2
	}
	fs := flag.NewFlagSet("idiom mcp serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "config.json", "path to config file")
	stdio := fs.Bool("stdio", false, "serve MCP over newline-delimited stdio")
	profileFlag := fs.String("profile", mcpDiscoveryProfile, "tool permission profile: discovery")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if !*stdio {
		fmt.Fprintln(stderr, "idiom mcp serve currently requires --stdio")
		return 2
	}
	profile, err := workspace.NormalizeToolProfile(*profileFlag)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if profile != mcpDiscoveryProfile {
		fmt.Fprintln(stderr, "idiom mcp serve currently supports only --profile discovery")
		return 2
	}
	cfg, err := loadToolConfig(*configPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	server, err := newMCPDiscoveryServer(cfg, profile)
	if err != nil {
		fmt.Fprintf(stderr, "initialize mcp server: %v\n", err)
		return 1
	}
	if err := server.serve(context.Background(), os.Stdin, stdout); err != nil {
		fmt.Fprintf(stderr, "mcp serve: %v\n", err)
		return 1
	}
	return 0
}

func printMCPUsage(stderr io.Writer) {
	fmt.Fprintln(stderr, "Usage:")
	fmt.Fprintln(stderr, "  idiom mcp serve --stdio --profile discovery [--config config.json]")
	fmt.Fprintln(stderr)
	fmt.Fprintln(stderr, "Serves the P8.1 MCP stdio surface: initialize, tools/list, and tools/call for schema-derived discovery tools.")
	fmt.Fprintln(stderr, "Resources, roots, telemetry parity, and broader protocol coverage are deferred to later MCP work packages.")
}

type mcpDiscoveryServer struct {
	cfg        config.Config
	profile    string
	manager    *workspace.Manager
	tools      []workspace.ExportedToolSchema
	toolByName map[string]workspace.ExportedToolSchema
}

func newMCPDiscoveryServer(cfg config.Config, profile string) (*mcpDiscoveryServer, error) {
	tools, err := workspace.BuildMCPDiscoveryToolSchemas()
	if err != nil {
		return nil, err
	}
	toolByName := make(map[string]workspace.ExportedToolSchema, len(tools))
	for _, tool := range tools {
		toolByName[tool.Name] = tool
	}
	mgr := workspace.NewManager(cfg.Workspace)
	mgr.SetBypassReadBeforeEdit(true)
	return &mcpDiscoveryServer{
		cfg:        cfg,
		profile:    profile,
		manager:    mgr,
		tools:      tools,
		toolByName: toolByName,
	}, nil
}

func (s *mcpDiscoveryServer) serve(ctx context.Context, stdin io.Reader, stdout io.Writer) error {
	scanner := bufio.NewScanner(stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	enc := json.NewEncoder(stdout)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		resp, ok := s.handleFrame(ctx, []byte(line))
		if !ok {
			continue
		}
		if err := enc.Encode(resp); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return nil
}

func (s *mcpDiscoveryServer) handleFrame(ctx context.Context, payload []byte) (mcpRPCResponse, bool) {
	var req mcpRPCRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return mcpRPCResponse{JSONRPC: "2.0", Error: &mcpRPCError{Code: -32700, Message: "parse error"}}, true
	}
	if len(req.ID) == 0 {
		return mcpRPCResponse{}, false
	}
	switch strings.TrimSpace(req.Method) {
	case "initialize":
		return mcpRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: s.initializeResult()}, true
	case "tools/list":
		return mcpRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"tools": s.mcpTools()}}, true
	case "tools/call":
		result, err := s.callTool(ctx, req.Params)
		if err != nil {
			return mcpRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: &mcpRPCError{Code: -32602, Message: err.Error()}}, true
		}
		return mcpRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: result}, true
	default:
		return mcpRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: &mcpRPCError{Code: -32601, Message: "method not found"}}, true
	}
}

func (s *mcpDiscoveryServer) initializeResult() map[string]any {
	return map[string]any{
		"protocolVersion": mcppkg.ProtocolVersion,
		"capabilities": map[string]any{
			"tools": map[string]any{"listChanged": false},
		},
		"serverInfo": map[string]any{
			"name":    "idiom",
			"version": version,
		},
		"instructions": "P8.1 stdio server exposes schema-derived discovery tools only. Resources, roots, and telemetry parity are deferred.",
	}
}

func (s *mcpDiscoveryServer) mcpTools() []map[string]any {
	out := make([]map[string]any, 0, len(s.tools))
	for _, tool := range s.tools {
		out = append(out, map[string]any{
			"name":        tool.Name,
			"inputSchema": tool.InputSchema,
			"_meta": map[string]any{
				"idiom_schema_version": workspace.ToolSchemaExportVersion,
				"idiom_schema_source":  "internal/workspace.BuildToolSchemaExport",
				"profiles":             tool.Profiles,
				"policy":               tool.Policy,
			},
		})
	}
	return out
}

func (s *mcpDiscoveryServer) callTool(ctx context.Context, params json.RawMessage) (mcpToolCallResult, error) {
	var call mcpToolCallParams
	if len(params) == 0 {
		return mcpToolCallResult{}, fmt.Errorf("tools/call params are required")
	}
	if err := json.Unmarshal(params, &call); err != nil {
		return mcpToolCallResult{}, fmt.Errorf("invalid tools/call params")
	}
	toolName := strings.TrimSpace(call.Name)
	if toolName == "" {
		return mcpToolCallResult{}, fmt.Errorf("tools/call name is required")
	}
	args := bytesForMCPArguments(call.Arguments)
	if mcpArgumentsContainPrivatePath(args) {
		req := workspace.ToolRequest{Tool: toolName, Args: json.RawMessage(args)}
		result := workspace.ToolProfileDeniedResult(toolName, s.profile, workspace.ToolProfileDecision{
			Allowed: false,
			Code:    "profile_denied",
			Reason:  "private or temp absolute path request shapes are deferred until MCP root/resource containment",
		})
		return s.mcpToolResult(req, result), nil
	}
	req, err := workspace.DecodeToolRequestJSON(args)
	if err != nil {
		result := workspace.ToolResult{
			Tool:    toolName,
			Summary: "MCP tool input rejected",
			Error:   "invalid_input: tool arguments must be a JSON object matching the exported schema",
		}
		return s.mcpToolResult(req, result), nil
	}
	req.Tool = toolName
	req.Args = json.RawMessage(args)
	if _, ok := s.toolByName[toolName]; !ok {
		result := workspace.ToolProfileDeniedResult(toolName, s.profile, workspace.ToolProfileDecision{
			Allowed: false,
			Code:    "profile_denied",
			Reason:  "tool is not exposed by the schema-derived MCP discovery profile",
		})
		return s.mcpToolResult(req, result), nil
	}
	if decision := workspace.ToolRequestAllowedForProfile(req, s.profile); !decision.Allowed {
		result := workspace.ToolProfileDeniedResult(req.Tool, s.profile, decision)
		return s.mcpToolResult(req, result), nil
	}
	toolCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	result := s.manager.Execute(toolCtx, req)
	return s.mcpToolResult(req, result), nil
}

func bytesForMCPArguments(args json.RawMessage) []byte {
	trimmed := strings.TrimSpace(string(args))
	if trimmed == "" || trimmed == "null" {
		return []byte("{}")
	}
	return []byte(trimmed)
}

func mcpArgumentsContainPrivatePath(args []byte) bool {
	text := string(args)
	for _, re := range mcpPrivatePathRegexes() {
		if re.MatchString(text) {
			return true
		}
	}
	return false
}

func (s *mcpDiscoveryServer) mcpToolResult(req workspace.ToolRequest, result workspace.ToolResult) mcpToolCallResult {
	result = s.redactToolResult(result)
	payload := struct {
		workspace.ToolResult
		Sidecar workspace.ToolSidecarEnvelope `json:"sidecar"`
	}{
		ToolResult: result,
		Sidecar:    workspace.BuildToolSidecarEnvelope(req, result, s.profile),
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		data = []byte(`{"Error":"encode MCP tool result"}`)
	}
	text := s.redactText(string(data))
	return mcpToolCallResult{
		Content: []mcpToolContent{{Type: "text", Text: text}},
		IsError: strings.TrimSpace(result.Error) != "",
	}
}

func (s *mcpDiscoveryServer) redactToolResult(result workspace.ToolResult) workspace.ToolResult {
	result.Summary = s.redactText(result.Summary)
	result.Output = s.redactText(result.Output)
	result.Error = s.redactText(result.Error)
	for i := range result.Relationships {
		result.Relationships[i].SourcePath = s.redactText(result.Relationships[i].SourcePath)
		result.Relationships[i].TargetPath = s.redactText(result.Relationships[i].TargetPath)
		result.Relationships[i].Evidence = s.redactText(result.Relationships[i].Evidence)
	}
	return result
}

func (s *mcpDiscoveryServer) redactText(text string) string {
	if text == "" {
		return ""
	}
	for _, re := range mcpPrivatePathRegexes() {
		text = re.ReplaceAllString(text, "<redacted_path>")
	}
	replacements := []string{
		s.cfg.Workspace.Root,
		s.cfg.Workspace.CWD,
		config.CanonicalizeExistingPath(s.cfg.Workspace.Root),
		config.CanonicalizeExistingPath(s.cfg.Workspace.CWD),
		os.TempDir(),
		config.CanonicalizeExistingPath(os.TempDir()),
	}
	for _, value := range replacements {
		value = strings.TrimSpace(filepath.Clean(value))
		if value == "" || value == "." {
			continue
		}
		text = strings.ReplaceAll(text, value, "<redacted_path>")
	}
	return text
}

func mcpPrivatePathRegexes() []*regexp.Regexp {
	return []*regexp.Regexp{
		regexp.MustCompile(`/private<redacted_path>`),
		regexp.MustCompile(`/private/var/folders/[^\s"']+`),
		regexp.MustCompile(`/var/folders/[^\s"']+`),
		regexp.MustCompile(`/tmp/[^\s"']+`),
		regexp.MustCompile(`/Users/[^\s"']+`),
		regexp.MustCompile(`logs/(telemetry|conversations)/[^\s"']+`),
	}
}
