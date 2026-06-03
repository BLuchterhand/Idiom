package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	toolcmd "idiom/internal/cmd/tool"
	"idiom/internal/config"
	mcppkg "idiom/internal/mcp"
	"idiom/internal/telemetry"
	"idiom/internal/workspace"
)

const DiscoveryProfile = workspace.ToolProfileMCPDiscovery
const StdioTransport = "mcp_stdio"
const defaultVersion = "dev"

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
	Meta      json.RawMessage `json:"_meta,omitempty"`
}

func RunWithTelemetryAndVersion(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder, version string) int {
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
	profileFlag := fs.String("profile", DiscoveryProfile, "tool permission profile: discovery, mcp_discovery")
	callerID := fs.String("caller-id", "", "opaque caller identifier for sidecar.caller metadata")
	requestID := fs.String("request-id", "", "opaque request identifier for sidecar.caller metadata")
	traceID := fs.String("trace-id", "", "opaque trace identifier for sidecar.caller metadata")
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
	if profile != workspace.ToolProfileDiscovery && profile != workspace.ToolProfileMCPDiscovery {
		fmt.Fprintln(stderr, "idiom mcp serve supports --profile discovery or mcp_discovery")
		return 2
	}
	caller, err := workspace.NewToolSidecarCaller(*callerID, *requestID, *traceID)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	cfg, err := toolcmd.LoadConfig(*configPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	server, err := NewDiscoveryServerWithOptions(cfg, profile, DiscoveryServerOptions{
		Caller:    caller,
		Telemetry: rec,
		Version:   version,
	})
	if err != nil {
		fmt.Fprintf(stderr, "initialize mcp server: %v\n", err)
		return 1
	}
	if err := server.Serve(context.Background(), os.Stdin, stdout); err != nil {
		fmt.Fprintf(stderr, "mcp serve: %v\n", err)
		return 1
	}
	return 0
}

func printMCPUsage(stderr io.Writer) {
	fmt.Fprintln(stderr, "Usage:")
	fmt.Fprintln(stderr, "  idiom mcp serve --stdio --profile discovery [--config config.json] [--caller-id id] [--request-id id] [--trace-id id]")
	fmt.Fprintln(stderr, "  idiom mcp serve --stdio --profile mcp_discovery [--config config.json] [--caller-id id] [--request-id id] [--trace-id id]")
	fmt.Fprintln(stderr)
	fmt.Fprintln(stderr, "Serves the MCP stdio discovery surface: initialize, tools/list, tools/call, roots/list, resources/list, and metadata-only resources/read.")
	fmt.Fprintln(stderr, "The mcp_discovery profile (default) exposes all semantic discovery tools including context_pack, context_impact, and discovery_query.")
	fmt.Fprintln(stderr, "The discovery profile exposes only graph-backed context_* tools with no local artifact writes.")
}

type DiscoveryServerOptions struct {
	Caller    workspace.ToolSidecarCaller
	Telemetry *telemetry.Recorder
	Version   string
}

type DiscoveryServer struct {
	cfg        config.Config
	profile    string
	manager    *workspace.Manager
	tools      []workspace.ExportedToolSchema
	toolByName map[string]workspace.ExportedToolSchema
	caller     workspace.ToolSidecarCaller
	rec        *telemetry.Recorder
	version    string
	requestSeq atomic.Int64
}

func NewDiscoveryServer(cfg config.Config, profile string) (*DiscoveryServer, error) {
	return NewDiscoveryServerWithOptions(cfg, profile, DiscoveryServerOptions{})
}

func NewDiscoveryServerWithOptions(cfg config.Config, profile string, opts DiscoveryServerOptions) (*DiscoveryServer, error) {
	tools, err := workspace.BuildMCPDiscoveryToolSchemas(profile)
	if err != nil {
		return nil, err
	}
	toolByName := make(map[string]workspace.ExportedToolSchema, len(tools))
	for _, tool := range tools {
		toolByName[tool.Name] = tool
	}
	mgr := workspace.NewManager(cfg.Workspace)
	mgr.SetBypassReadBeforeEdit(true)
	version := strings.TrimSpace(opts.Version)
	if version == "" {
		version = defaultVersion
	}
	return &DiscoveryServer{
		cfg:        cfg,
		profile:    profile,
		manager:    mgr,
		tools:      tools,
		toolByName: toolByName,
		caller:     opts.Caller,
		rec:        opts.Telemetry,
		version:    version,
	}, nil
}

func (s *DiscoveryServer) Serve(ctx context.Context, stdin io.Reader, stdout io.Writer) error {
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

func (s *DiscoveryServer) handleFrame(ctx context.Context, payload []byte) (mcpRPCResponse, bool) {
	var req mcpRPCRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return mcpRPCResponse{JSONRPC: "2.0", Error: &mcpRPCError{Code: -32700, Message: "parse error"}}, true
	}
	if len(req.ID) == 0 {
		return mcpRPCResponse{}, false
	}
	switch strings.TrimSpace(req.Method) {
	case "initialize":
		if err := validateMCPObjectParams(req.Params, true); err != nil {
			return mcpRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: &mcpRPCError{Code: -32602, Message: err.Error()}}, true
		}
		return mcpRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: s.initializeResult()}, true
	case "tools/list":
		if err := validateMCPObjectParams(req.Params, true); err != nil {
			return mcpRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: &mcpRPCError{Code: -32602, Message: err.Error()}}, true
		}
		return mcpRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"tools": s.mcpTools()}}, true
	case "tools/call":
		result, err := s.callTool(ctx, req.Params)
		if err != nil {
			return mcpRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: &mcpRPCError{Code: -32602, Message: err.Error()}}, true
		}
		return mcpRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: result}, true
	case "roots/list":
		if err := validateMCPObjectParams(req.Params, true); err != nil {
			return mcpRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: &mcpRPCError{Code: -32602, Message: err.Error()}}, true
		}
		return mcpRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"roots": s.mcpRoots()}}, true
	case "resources/list":
		if err := validateMCPObjectParams(req.Params, true); err != nil {
			return mcpRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: &mcpRPCError{Code: -32602, Message: err.Error()}}, true
		}
		return mcpRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"resources": s.mcpResources()}}, true
	case "resources/read":
		result, err := s.readResource(req.Params)
		if err != nil {
			return mcpRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: &mcpRPCError{Code: -32602, Message: err.Error()}}, true
		}
		return mcpRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: result}, true
	default:
		return mcpRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: &mcpRPCError{Code: -32601, Message: "method not found"}}, true
	}
}

func (s *DiscoveryServer) initializeResult() map[string]any {
	return map[string]any{
		"protocolVersion": mcppkg.ProtocolVersion,
		"capabilities": map[string]any{
			"tools":     map[string]any{"listChanged": false},
			"resources": map[string]any{"listChanged": false},
		},
		"serverInfo": map[string]any{
			"name":    "idiom",
			"version": s.version,
		},
		"instructions": fmt.Sprintf("Stdio discovery server (profile=%s) exposes schema-derived discovery tools plus metadata-only, root-clamped resources. Tool calls use shared sidecar caller attribution with transport=mcp_stdio. Resource responses report freshness without refreshing.", s.profile),
	}
}

func (s *DiscoveryServer) mcpTools() []map[string]any {
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

func (s *DiscoveryServer) mcpRoots() []map[string]any {
	return []map[string]any{{
		"uri":  "idiom://workspace/root",
		"name": "project",
		"_meta": map[string]any{
			"idiom_root_scope":    "project",
			"idiom_metadata_only": true,
			"absolute_path":       "<redacted_path>",
		},
	}}
}

func (s *DiscoveryServer) mcpResources() []map[string]any {
	return []map[string]any{
		{
			"uri":         "idiom://workspace/root",
			"name":        "workspace_root",
			"description": "Metadata-only project root identity; absolute path is redacted.",
			"mimeType":    "application/json",
			"_meta": map[string]any{
				"idiom_metadata_only": true,
				"idiom_root_clamped":  true,
			},
		},
		{
			"uri":         "idiom://workspace/discovery-tools",
			"name":        "discovery_tools",
			"description": "Metadata-only view of the schema-derived discovery tool surface.",
			"mimeType":    "application/json",
			"_meta": map[string]any{
				"idiom_metadata_only": true,
				"idiom_profile":       s.profile,
			},
		},
		{
			"uri":         "idiom://workspace/resource-policy",
			"name":        "resource_policy",
			"description": "Metadata-only resource safety policy and current limitations.",
			"mimeType":    "application/json",
			"_meta": map[string]any{
				"idiom_metadata_only": true,
				"p8_6_replay_fixture": "testdata/sidecar/mcp-stdio-integration-proof.json",
			},
		},
	}
}

func (s *DiscoveryServer) readResource(params json.RawMessage) (mcppkg.ResourceReadResult, error) {
	var req mcppkg.ResourceReadParams
	if len(params) == 0 {
		return mcppkg.ResourceReadResult{}, fmt.Errorf("resources/read params are required")
	}
	if err := json.Unmarshal(params, &req); err != nil {
		return mcppkg.ResourceReadResult{}, fmt.Errorf("invalid resources/read params")
	}
	uri := strings.TrimSpace(req.URI)
	if uri == "" {
		return mcppkg.ResourceReadResult{}, fmt.Errorf("resources/read uri is required")
	}
	payload, err := s.resourcePayload(uri)
	if err != nil {
		return mcppkg.ResourceReadResult{}, err
	}
	text, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return mcppkg.ResourceReadResult{}, fmt.Errorf("encode resource metadata")
	}
	return mcppkg.ResourceReadResult{Contents: []mcppkg.ResourceContent{{
		URI:      canonicalMCPResourceURI(uri),
		MimeType: "application/json",
		Text:     s.redactText(string(text)),
	}}}, nil
}

func (s *DiscoveryServer) resourcePayload(uri string) (map[string]any, error) {
	switch strings.TrimSpace(uri) {
	case "idiom://workspace/root":
		return map[string]any{
			"schema_version":    "idiom.mcp_resource.v1",
			"kind":              "workspace_root",
			"root_scope":        "project",
			"absolute_path":     "<redacted_path>",
			"metadata_only":     true,
			"content_available": false,
			"freshness":         s.resourceFreshness("."),
		}, nil
	case "idiom://workspace/discovery-tools":
		names := make([]string, 0, len(s.tools))
		for _, tool := range s.tools {
			names = append(names, tool.Name)
		}
		return map[string]any{
			"schema_version": "idiom.mcp_resource.v1",
			"kind":           "discovery_tools",
			"profile":        s.profile,
			"tool_count":     len(names),
			"tools":          names,
			"metadata_only":  true,
			"freshness":      s.resourceFreshness("."),
		}, nil
	case "idiom://workspace/resource-policy":
		return map[string]any{
			"schema_version":      "idiom.mcp_resource.v1",
			"kind":                "resource_policy",
			"metadata_only":       true,
			"root_clamped":        true,
			"p8_6_replay_fixture": "testdata/sidecar/mcp-stdio-integration-proof.json",
			"freshness": map[string]any{
				"policy":             "report_only",
				"source_operation":   "context_status",
				"refresh_allowed":    false,
				"resource_semantics": "metadata_only",
			},
			"protected_locations": []string{
				".git/**",
				".idiom/**",
				".codex/**",
				".agents/**",
				"logs/telemetry/**",
				"logs/conversations/**",
				"secrets/**",
				"secret/credential/token/password path segments",
			},
			"limitations": []string{
				"file bodies are not returned by P8.2 resources/read",
				"MCP integration parity is covered by the P8.6 replay fixture",
			},
		}, nil
	default:
		resourcePath, rel, err := s.resolveResourceFileURI(uri)
		if err != nil {
			return nil, err
		}
		info, err := os.Lstat(resourcePath)
		switch {
		case err == nil:
			return map[string]any{
				"schema_version":    "idiom.mcp_resource.v1",
				"kind":              "workspace_path",
				"relative_path":     filepath.ToSlash(rel),
				"exists":            true,
				"type":              mcpResourceFileType(info),
				"size_bytes":        info.Size(),
				"metadata_only":     true,
				"content_available": false,
				"freshness":         s.resourceFreshness(rel),
			}, nil
		case os.IsNotExist(err):
			return nil, fmt.Errorf("resource not found or unavailable")
		default:
			return nil, fmt.Errorf("resource not available")
		}
	}
}

func (s *DiscoveryServer) resourceFreshness(rel string) map[string]any {
	req := workspace.ToolRequest{
		Tool:            "context_status",
		Path:            filepath.ToSlash(rel),
		FreshnessPolicy: "report_only",
		Limit:           1,
	}
	if strings.TrimSpace(req.Path) == "" {
		req.Path = "."
	}
	result := s.manager.Execute(context.Background(), req)
	evidence := map[string]any{
		"policy":            "report_only",
		"source_operation":  "context_status",
		"refresh_allowed":   false,
		"refresh_attempted": false,
	}
	if result.Error != "" {
		evidence["state"] = "unavailable"
		evidence["error_class"] = "context_status_unavailable"
		return evidence
	}
	var status map[string]any
	if err := json.Unmarshal([]byte(result.Output), &status); err != nil {
		evidence["state"] = "unavailable"
		evidence["error_class"] = "context_status_decode_failed"
		return evidence
	}
	if freshness, ok := status["freshness"].(map[string]any); ok {
		for key, value := range freshness {
			evidence[key] = value
		}
	}
	if stale, ok := status["stale_evidence"].(map[string]any); ok {
		evidence["stale_evidence"] = stale
	}
	for _, key := range []string{"indexed", "fresh", "index_id", "store_kind"} {
		if value, ok := status[key]; ok {
			evidence[key] = value
		}
	}
	return evidence
}

func (s *DiscoveryServer) resolveResourceFileURI(rawURI string) (string, string, error) {
	root := filepath.Clean(config.CanonicalizeExistingPath(s.cfg.Workspace.Root))
	if root == "" || root == "." {
		return "", "", fmt.Errorf("workspace root is not configured")
	}
	parsed, err := url.Parse(strings.TrimSpace(rawURI))
	if err != nil {
		return "", "", fmt.Errorf("invalid resource uri")
	}
	var candidate string
	switch parsed.Scheme {
	case "idiom":
		if parsed.Host != "workspace" || !strings.HasPrefix(parsed.Path, "/file/") {
			return "", "", fmt.Errorf("unsupported resource uri")
		}
		rel := strings.TrimPrefix(parsed.EscapedPath(), "/file/")
		unescaped, err := url.PathUnescape(rel)
		if err != nil {
			return "", "", fmt.Errorf("invalid resource uri")
		}
		candidate = filepath.Join(root, filepath.FromSlash(unescaped))
	case "file":
		if parsed.Path == "" {
			return "", "", fmt.Errorf("invalid resource uri")
		}
		candidate = filepath.Clean(parsed.Path)
	default:
		return "", "", fmt.Errorf("unsupported resource uri")
	}
	rel, err := mcpResourceRel(root, candidate)
	if err != nil {
		return "", "", err
	}
	if denied, reason := ResourcePathDenied(rel); denied {
		return "", "", fmt.Errorf("resource denied: %s", reason)
	}
	if evaluated, err := filepath.EvalSymlinks(candidate); err == nil {
		evaluatedRel, err := mcpResourceRel(root, evaluated)
		if err != nil {
			return "", "", err
		}
		if denied, reason := ResourcePathDenied(evaluatedRel); denied {
			return "", "", fmt.Errorf("resource denied: %s", reason)
		}
		candidate = evaluated
		rel = evaluatedRel
	}
	return candidate, rel, nil
}

func mcpResourceRel(root string, candidate string) (string, error) {
	rel, err := filepath.Rel(root, filepath.Clean(candidate))
	if err != nil {
		return "", fmt.Errorf("resource path error")
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("resource is outside workspace root")
	}
	if rel == "." {
		return ".", nil
	}
	return rel, nil
}

func ResourcePathDenied(rel string) (bool, string) {
	slash := strings.Trim(filepath.ToSlash(filepath.Clean(rel)), "/")
	if slash == "" || slash == "." {
		return false, ""
	}
	segments := strings.Split(slash, "/")
	switch segments[0] {
	case ".git", ".idiom", ".codex", ".agents":
		return true, "protected path"
	case "logs":
		if len(segments) > 1 && (segments[1] == "telemetry" || segments[1] == "conversations") {
			return true, "protected log path"
		}
	}
	for i, segment := range segments {
		lower := strings.ToLower(segment)
		switch lower {
		case ".git", ".idiom", ".codex", ".agents":
			return true, "protected path"
		case "logs":
			if i+1 < len(segments) {
				next := strings.ToLower(segments[i+1])
				if next == "telemetry" || next == "conversations" {
					return true, "protected log path"
				}
			}
		}
		if lower == "secrets" ||
			lower == ".env" ||
			strings.HasPrefix(lower, ".env.") ||
			strings.Contains(lower, "secret") ||
			strings.Contains(lower, "credential") ||
			strings.Contains(lower, "token") ||
			strings.Contains(lower, "password") ||
			strings.Contains(lower, "private-key") ||
			strings.Contains(lower, "private_key") {
			return true, "protected path"
		}
	}
	return false, ""
}

func mcpResourceFileType(info os.FileInfo) string {
	switch {
	case info.IsDir():
		return "directory"
	case info.Mode()&os.ModeSymlink != 0:
		return "symlink"
	default:
		return "file"
	}
}

func canonicalMCPResourceURI(uri string) string {
	trimmed := strings.TrimSpace(uri)
	if strings.HasPrefix(trimmed, "file://") {
		return "idiom://workspace/file/<redacted_path>"
	}
	return trimmed
}

func validateMCPObjectParams(params json.RawMessage, allowEmpty bool) error {
	trimmed := strings.TrimSpace(string(params))
	if trimmed == "" || trimmed == "null" {
		if allowEmpty {
			return nil
		}
		return fmt.Errorf("params are required")
	}
	var value any
	if err := json.Unmarshal(params, &value); err != nil {
		return fmt.Errorf("invalid params")
	}
	if _, ok := value.(map[string]any); !ok {
		return fmt.Errorf("params must be an object")
	}
	return nil
}

func (s *DiscoveryServer) callTool(ctx context.Context, params json.RawMessage) (mcpToolCallResult, error) {
	var call mcpToolCallParams
	if len(params) == 0 {
		return mcpToolCallResult{}, fmt.Errorf("tools/call params are required")
	}
	if err := json.Unmarshal(params, &call); err != nil {
		return mcpToolCallResult{}, fmt.Errorf("invalid tools/call params")
	}
	caller, err := s.callerForToolCall(call.Meta)
	if err != nil {
		return mcpToolCallResult{}, err
	}
	toolName := strings.TrimSpace(call.Name)
	if toolName == "" {
		return mcpToolCallResult{}, fmt.Errorf("tools/call name is required")
	}
	args := bytesForMCPArguments(call.Arguments)
	if mcpArgumentsContainPrivatePath(args) {
		req := workspace.ToolRequest{Tool: toolName, Args: json.RawMessage(args)}
		s.logMCPToolRequest(req, len(args), caller)
		result := workspace.ToolProfileDeniedResult(toolName, s.profile, workspace.ToolProfileDecision{
			Allowed: false,
			Code:    "profile_denied",
			Reason:  "private or temp absolute path request shapes are deferred until MCP root/resource containment",
		})
		result = s.redactToolResult(result)
		s.logMCPToolResult(req, result, 0, caller)
		return s.mcpToolResult(req, result, caller), nil
	}
	req, err := workspace.DecodeToolRequestJSON(args)
	if err != nil {
		req = workspace.ToolRequest{Tool: toolName, Args: json.RawMessage(args)}
		s.logMCPToolRequest(req, len(args), caller)
		result := workspace.ToolResult{
			Tool:    toolName,
			Summary: "MCP tool input rejected",
			Error:   "invalid_input: tool arguments must be a JSON object matching the exported schema",
		}
		result = s.redactToolResult(result)
		s.logMCPToolResult(req, result, 0, caller)
		return s.mcpToolResult(req, result, caller), nil
	}
	req.Tool = toolName
	req.Args = json.RawMessage(args)
	s.logMCPToolRequest(req, len(args), caller)
	if _, ok := s.toolByName[toolName]; !ok {
		result := workspace.ToolProfileDeniedResult(toolName, s.profile, workspace.ToolProfileDecision{
			Allowed: false,
			Code:    "profile_denied",
			Reason:  "tool is not exposed by the schema-derived MCP discovery profile",
		})
		result = s.redactToolResult(result)
		s.logMCPToolResult(req, result, 0, caller)
		return s.mcpToolResult(req, result, caller), nil
	}
	if decision := workspace.ToolRequestAllowedForProfile(req, s.profile); !decision.Allowed {
		result := workspace.ToolProfileDeniedResult(req.Tool, s.profile, decision)
		result = s.redactToolResult(result)
		s.logMCPToolResult(req, result, 0, caller)
		return s.mcpToolResult(req, result, caller), nil
	}
	toolCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	startedAt := time.Now()
	result := s.manager.Execute(toolCtx, req)
	result = s.redactToolResult(result)
	s.logMCPToolResult(req, result, time.Since(startedAt).Milliseconds(), caller)
	return s.mcpToolResult(req, result, caller), nil
}

type mcpCallerMeta struct {
	CallerID  string         `json:"caller_id,omitempty"`
	RequestID string         `json:"request_id,omitempty"`
	TraceID   string         `json:"trace_id,omitempty"`
	Idiom     *mcpCallerMeta `json:"idiom,omitempty"`
}

func (s *DiscoveryServer) callerForToolCall(meta json.RawMessage) (workspace.ToolSidecarCaller, error) {
	callerID := s.caller.CallerID
	requestID := s.caller.RequestID
	traceID := s.caller.TraceID
	if strings.TrimSpace(requestID) == "" {
		requestID = fmt.Sprintf("mcp:req-%d", s.requestSeq.Add(1))
	}
	if len(meta) > 0 && strings.TrimSpace(string(meta)) != "" && strings.TrimSpace(string(meta)) != "null" {
		var decoded mcpCallerMeta
		if err := json.Unmarshal(meta, &decoded); err != nil {
			return workspace.ToolSidecarCaller{}, fmt.Errorf("invalid tools/call _meta")
		}
		if decoded.Idiom != nil {
			decoded = *decoded.Idiom
		}
		callerID = toolcmd.FirstNonEmptyString(decoded.CallerID, callerID)
		requestID = toolcmd.FirstNonEmptyString(decoded.RequestID, requestID)
		traceID = toolcmd.FirstNonEmptyString(decoded.TraceID, traceID)
	}
	caller, err := workspace.NewToolSidecarCaller(callerID, requestID, traceID)
	if err != nil {
		return workspace.ToolSidecarCaller{}, err
	}
	return caller, nil
}

func (s *DiscoveryServer) logMCPToolRequest(req workspace.ToolRequest, rawInputBytes int, caller workspace.ToolSidecarCaller) {
	if s.rec == nil {
		return
	}
	fields := map[string]any{
		"step":            1,
		"agent":           "mcp",
		"tool":            strings.TrimSpace(req.Tool),
		"raw_input_bytes": rawInputBytes,
		"has_path":        strings.TrimSpace(req.Path) != "",
		"has_workdir":     strings.TrimSpace(req.Workdir) != "",
		"has_purpose":     strings.TrimSpace(req.Purpose) != "",
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
	toolcmd.AddToolAttributionTelemetry(fields, workspace.ToolSidecarEnvelopeVersion, StdioTransport, s.profile, caller, strings.TrimSpace(req.Tool))
	if caller.RequestID != "" {
		fields["telemetry_event_id"] = caller.RequestID + ":request"
	}
	profile := workspace.DiscoveryProfileForRequest(req)
	if profile.Discovery {
		fields["discovery_class"] = profile.Class
		fields["discovery_scope"] = profile.Scope
		fields["discovery_cost"] = profile.Cost
	}
	_ = s.rec.Log("tool_request", fields)
}

func (s *DiscoveryServer) logMCPToolResult(req workspace.ToolRequest, result workspace.ToolResult, durationMillis int64, caller workspace.ToolSidecarCaller) {
	if s.rec == nil {
		return
	}
	tool := toolcmd.FirstNonEmptyString(result.Tool, req.Tool)
	fields := map[string]any{
		"step":        1,
		"agent":       "mcp",
		"tool":        tool,
		"summary":     s.redactText(result.Summary),
		"error":       s.redactText(result.Error),
		"duration_ms": durationMillis,
	}
	toolcmd.AddToolAttributionTelemetry(fields, workspace.ToolSidecarEnvelopeVersion, StdioTransport, s.profile, caller, tool)
	if caller.RequestID != "" {
		fields["telemetry_event_id"] = caller.RequestID + ":result"
	}
	if result.Output != "" {
		fields["output_chars"] = len(result.Output)
		fields["output_lines"] = toolcmd.OutputLineCount(result.Output)
	}
	if result.PrecisionTier != "" {
		fields["precision_tier"] = result.PrecisionTier
	}
	envelope := workspace.BuildToolSidecarEnvelopeWithOptions(req, result, s.profile, workspace.ToolSidecarOptions{
		Caller:    caller,
		Transport: StdioTransport,
	})
	if len(envelope.AuditEventIDs) > 0 {
		fields["audit_event_ids"] = append([]string(nil), envelope.AuditEventIDs...)
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
	_ = s.rec.Log("tool_result", fields)
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
	for _, re := range mcpPrivatePathRegexPatterns {
		if re.MatchString(text) {
			return true
		}
	}
	return false
}

func (s *DiscoveryServer) mcpToolResult(req workspace.ToolRequest, result workspace.ToolResult, caller workspace.ToolSidecarCaller) mcpToolCallResult {
	payload := struct {
		workspace.ToolResult
		Sidecar workspace.ToolSidecarEnvelope `json:"sidecar"`
	}{
		ToolResult: result,
		Sidecar: workspace.BuildToolSidecarEnvelopeWithOptions(req, result, s.profile, workspace.ToolSidecarOptions{
			Caller:    caller,
			Transport: StdioTransport,
		}),
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

func (s *DiscoveryServer) redactToolResult(result workspace.ToolResult) workspace.ToolResult {
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

func (s *DiscoveryServer) redactText(text string) string {
	if text == "" {
		return ""
	}
	for _, re := range mcpPrivatePathRegexPatterns {
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

var mcpPrivatePathRegexPatterns = []*regexp.Regexp{
	regexp.MustCompile(`/private<redacted_path>`),
	regexp.MustCompile(`/private/var/folders/[^\s"']+`),
	regexp.MustCompile(`/var/folders/[^\s"']+`),
	regexp.MustCompile(`/tmp/[^\s"']+`),
	regexp.MustCompile(`/Users/[^\s"']+`),
	regexp.MustCompile(`logs/(telemetry|conversations)/[^\s"']+`),
}
