package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	mcppkg "idiom/internal/capabilities/mcp"
	"idiom/internal/capabilities/workspace"
	"idiom/internal/evidence/telemetry"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/localops"
	toolcmd "idiom/internal/surfaces/cli/tool"
	"idiom/internal/workflows/recovery"
)

var _ = ginkgo.It("mcp discovery stdio initialize list and call uses shared schemas", func() {
	root := ginkgo.GinkgoT().TempDir()
	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root

	server := newTestMCPDiscoveryServer(cfg)

	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"context_status","arguments":{"limit":1}}}`,
	}, "\n") + "\n"

	var stdout bytes.Buffer
	gomega.Expect(server.Serve(testContext(), strings.NewReader(input), &stdout)).To(gomega.Succeed())

	frames := decodeMCPFrames(stdout.String())
	gomega.Expect(frames).To(gomega.HaveLen(3), fmt.Sprintf("expected 3 responses (notification ignored), got %d:\n%s", len(frames), stdout.String()))
	gomega.Expect(frames[0].ID).To(gomega.Equal(float64(1)), fmt.Sprintf("unexpected initialize response: %#v", frames[0]))
	gomega.Expect(frames[0].Result["protocolVersion"]).NotTo(gomega.BeEmpty(), fmt.Sprintf("unexpected initialize response: %#v", frames[0]))

	tools := mcpToolsFromFrame(frames[1])
	contextStatusSchema := sharedMCPDiscoverySchema("context_status")

	var sawContextStatus bool

	for _, tool := range tools {
		name, _ := tool["name"].(string)
		gomega.Expect(slices.Contains([]string{"read_file", "write_file", "shell", "context_pack", "go_read_symbol", "edit_file", "apply_patch", "context_trace_pack"}, name)).To(gomega.BeFalse(), fmt.Sprintf("MCP discovery list exposed disallowed tool %q: %#v", name, tool))

		meta, _ := tool["_meta"].(map[string]any)
		profiles := stringSliceFromAny(meta["profiles"])
		gomega.Expect(slices.Contains(profiles, workspace.ToolProfileDiscovery)).To(gomega.BeTrue(), fmt.Sprintf("listed tool %s missing discovery profile metadata: %#v", name, tool))

		if name == "context_status" {
			sawContextStatus = true

			gotSchema, _ := json.Marshal(tool["inputSchema"])
			gomega.Expect(json.Valid(gotSchema)).To(gomega.BeTrue(), fmt.Sprintf("context_status MCP schema drifted from workspace export:\n got %s\nwant %s", gotSchema, contextStatusSchema.InputSchema))
			gomega.Expect(jsonObjectsEqual(gotSchema, contextStatusSchema.InputSchema)).To(gomega.BeTrue(), fmt.Sprintf("context_status MCP schema drifted from workspace export:\n got %s\nwant %s", gotSchema, contextStatusSchema.InputSchema))

		}
	}
	gomega.Expect(sawContextStatus).To(gomega.BeTrue(), fmt.Sprintf("tools/list missing context_status: %#v", tools))

	call := mcpToolResultFromFrame(frames[2])
	gomega.Expect(call.IsError).To(gomega.BeFalse(), fmt.Sprintf("context_status call should succeed: %#v", call))

	payload := decodeMCPToolTextPayload(call)
	gomega.Expect(payload.Tool).To(gomega.Equal("context_status"), fmt.Sprintf("unexpected context_status MCP payload: %#v", payload))
	gomega.Expect(payload.Sidecar.Profile).To(gomega.Equal(workspace.ToolProfileDiscovery), fmt.Sprintf("unexpected context_status MCP payload: %#v", payload))
	gomega.Expect(payload.Sidecar.Error).To(gomega.BeNil(), fmt.Sprintf("unexpected context_status MCP payload: %#v", payload))

	assertMCPPublicPayloadSafe(stdout.String(), root)
	assertNoMCPStrategyLeakage(stdout.String())
})
var _ = ginkgo.It("mcp discovery mcp discovery profile exposes expanded surface", func() {
	root := ginkgo.GinkgoT().TempDir()
	if _, _, err := recovery.WritePlanExecuteFailureRecord(root, recovery.PlanExecuteFailureInput{
		SessionID:         "sid-mcp",
		Phase:             "executor",
		Status:            "failed",
		FailureKind:       "executor_failed",
		FailureReason:     "exact edit did not apply",
		RequiredEditPaths: []string{"internal/surfaces/tui/app/foo.go"},
	}, time.Now().UTC()); err != nil {
		ginkgo.Fail(fmt.Sprintf("write recovery fixture: %v", err))
	}

	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root

	server, err := NewDiscoveryServerWithOptions(cfg, workspace.ToolProfileMCPDiscovery, DiscoveryServerOptions{})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("new MCP mcp_discovery server: %v", err))

	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"recovery_inspect","arguments":{"session_id":"sid-mcp"}}}`,
	}, "\n") + "\n"

	var stdout bytes.Buffer
	gomega.Expect(server.Serve(testContext(), strings.NewReader(input), &stdout)).To(gomega.Succeed())

	frames := decodeMCPFrames(stdout.String())
	gomega.Expect(frames).To(gomega.HaveLen(3), fmt.Sprintf("expected 2 responses, got %d:\n%s", len(frames), stdout.String()))

	tools := mcpToolsFromFrame(frames[1])
	toolNames := make(map[string]bool, len(tools))

	for _, tool := range tools {
		name, _ := tool["name"].(string)
		gomega.Expect(slices.Contains([]string{"read_file", "write_file", "shell", "edit_file", "apply_patch", "context_trace_pack"}, name)).To(gomega.BeFalse(), fmt.Sprintf("mcp_discovery list exposed disallowed tool %q: %#v", name, tool))

		toolNames[name] = true
	}

	for _, required := range []string{
		"context_status", "context_query", "context_zoom",
		"context_pack", "context_impact", "discovery_query",
		"structural_search", "dependency_graph",
		"history_range", "history_symbol", "blame_range",
		"ctags_symbols", "code_navigate", "harness_audit",
		"recovery_inspect", "recovery_status",
	} {
		gomega.Expect(toolNames[required]).To(gomega.BeTrue(), fmt.Sprintf("mcp_discovery surface should expose %s", required))

	}
	gomega.Expect(toolNames["write_file"]).To(gomega.BeFalse(), fmt.Sprintf("mcp_discovery surface should not expose write or shell tools"))
	gomega.Expect(toolNames["shell"]).To(gomega.BeFalse(), fmt.Sprintf("mcp_discovery surface should not expose write or shell tools"))
	gomega.Expect(toolNames["edit_file"]).To(gomega.BeFalse(), fmt.Sprintf("mcp_discovery surface should not expose write or shell tools"))

	call := mcpToolResultFromFrame(frames[2])
	gomega.Expect(call.IsError).To(gomega.BeFalse(), fmt.Sprintf("recovery_inspect should be allowed: %#v", call))

	payload := decodeMCPToolTextPayload(call)
	gomega.Expect(payload.Tool).To(gomega.Equal("recovery_inspect"), fmt.Sprintf("unexpected recovery_inspect MCP payload: %#v", payload))
	gomega.Expect(payload.Sidecar.Profile).To(gomega.Equal(workspace.ToolProfileMCPDiscovery), fmt.Sprintf("unexpected recovery_inspect MCP payload: %#v", payload))
	gomega.Expect(payload.Sidecar.Contract.Transport).To(gomega.Equal(StdioTransport), fmt.Sprintf("unexpected recovery_inspect MCP payload: %#v", payload))
	gomega.Expect(payload.Sidecar.Error).To(gomega.BeNil(), fmt.Sprintf("unexpected recovery_inspect MCP payload: %#v", payload))

	assertMCPPublicPayloadSafe(stdout.String(), root)
	assertNoMCPStrategyLeakage(stdout.String())
})
var _ = ginkgo.It("mcp client stdio smoke telemetry and caller parity", func() {
	root := ginkgo.GinkgoT().TempDir()
	gomega.Expect(os.WriteFile(filepath.Join(root, "README.md"), []byte("P83_RAW_FILE_BODY_SENTINEL_DO_NOT_LEAK\n"), 0o644)).To(gomega.Succeed())

	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root

	caller, err := workspace.NewToolSidecarCaller("mcp-smoke", "req-p83", "trace:p83")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("caller metadata: %v", err))

	telemetryDir := ginkgo.GinkgoT().TempDir()

	rec, err := telemetry.Open(telemetry.Options{
		Dir:           telemetryDir,
		Command:       "mcp:serve",
		Args:          []string{"mcp", "serve", "--stdio", "--profile", "discovery", "--caller-id", "mcp-smoke", "--request-id", "req-p83", "--trace-id", "trace:p83"},
		WorkspaceRoot: "<redacted_path>",
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("open telemetry: %v", err))

	server, err := NewDiscoveryServerWithOptions(cfg, workspace.ToolProfileDiscovery, DiscoveryServerOptions{
		Caller:    caller,
		Telemetry: rec,
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("new MCP discovery server: %v", err))

	transport := startTestMCPStdioTransport(server)
	client := mcppkg.NewClient(transport)
	ctx := testContext()
	gomega.Expect(client.Start(ctx)).To(gomega.Succeed())
	gomega.Expect(client.Initialize(ctx, "p83-smoke-client")).To(gomega.Succeed())

	tools, err := client.ListTools(ctx)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("tools/list: %v", err))
	gomega.Expect(mcpClientToolsContain(tools, "context_status")).To(gomega.BeTrue(), fmt.Sprintf("tools/list missing context_status: %#v", tools))

	allowed, err := client.CallTool(ctx, "context_status", json.RawMessage(`{"limit":1}`))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("allowed tools/call: %v", err))
	gomega.Expect(allowed.IsError).To(gomega.BeFalse(), fmt.Sprintf("context_status should be allowed: %#v", allowed))

	allowedPayload := decodeMCPClientToolPayload(allowed)
	gomega.Expect(allowedPayload.Sidecar.SchemaVersion).To(gomega.Equal(workspace.ToolSidecarEnvelopeVersion), fmt.Sprintf("allowed MCP sidecar attribution mismatch: %#v", allowedPayload.Sidecar))
	gomega.Expect(allowedPayload.Sidecar.Caller.CallerID).To(gomega.Equal("mcp-smoke"), fmt.Sprintf("allowed MCP sidecar attribution mismatch: %#v", allowedPayload.Sidecar))
	gomega.Expect(allowedPayload.Sidecar.Caller.RequestID).To(gomega.Equal("req-p83"), fmt.Sprintf("allowed MCP sidecar attribution mismatch: %#v", allowedPayload.Sidecar))
	gomega.Expect(allowedPayload.Sidecar.Caller.TraceID).To(gomega.Equal("trace:p83"), fmt.Sprintf("allowed MCP sidecar attribution mismatch: %#v", allowedPayload.Sidecar))
	gomega.Expect(allowedPayload.Sidecar.Contract.Transport).To(gomega.Equal(StdioTransport), fmt.Sprintf("allowed MCP sidecar attribution mismatch: %#v", allowedPayload.Sidecar))
	gomega.Expect(allowedPayload.Sidecar.Contract.SchemaVersion).To(gomega.Equal(workspace.ToolSchemaExportVersion), fmt.Sprintf("allowed MCP sidecar attribution mismatch: %#v", allowedPayload.Sidecar))
	gomega.Expect(allowedPayload.Sidecar.Contract.Transport).NotTo(gomega.Equal("cli"), fmt.Sprintf("MCP response must not identify as CLI transport: %#v", allowedPayload.Sidecar.Contract))

	resources, err := client.ListResources(ctx)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("resources/list: %v", err))
	gomega.Expect(resources).NotTo(gomega.BeEmpty(), fmt.Sprintf("resources/list returned no resources"))

	readme, err := client.ReadResource(ctx, "idiom://workspace/file/README.md")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("metadata resources/read: %v", err))
	gomega.Expect(readme.Contents).To(gomega.HaveLen(1), fmt.Sprintf("README resource should be metadata-only: %#v", readme))
	gomega.Expect(readme.Contents[0].Text).To(gomega.ContainSubstring(`"metadata_only": true`), fmt.Sprintf("README resource should be metadata-only: %#v", readme))

	denied, err := client.CallTool(ctx, "context_status", json.RawMessage(`{"refresh":true}`))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("structured denial tools/call: %v", err))
	gomega.Expect(denied.IsError).To(gomega.BeTrue(), fmt.Sprintf("refresh request should be a structured tool denial: %#v", denied))

	deniedPayload := decodeMCPClientToolPayload(denied)
	gomega.Expect(deniedPayload.Sidecar.Error).NotTo(gomega.BeNil(), fmt.Sprintf("unexpected MCP structured denial: %#v", deniedPayload.Sidecar))
	gomega.Expect(deniedPayload.Sidecar.Error.Class).To(gomega.Equal("profile_denied"), fmt.Sprintf("unexpected MCP structured denial: %#v", deniedPayload.Sidecar))
	gomega.Expect(deniedPayload.Sidecar.Contract.Transport).To(gomega.Equal(StdioTransport), fmt.Sprintf("unexpected MCP structured denial: %#v", deniedPayload.Sidecar))

	if _, err := client.ReadResource(ctx, "idiom://workspace/file/.git/config"); err == nil {
		ginkgo.Fail(fmt.Sprintf("protected resource read should return JSON-RPC denial"))
	}
	gomega.Expect(client.Close()).To(gomega.Succeed())
	gomega.Expect(rec.CloseWithFields(0, nil)).To(gomega.Succeed())

	entries, err := os.ReadDir(telemetryDir)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read telemetry dir: %v", err))
	gomega.Expect(entries).To(gomega.HaveLen(1), fmt.Sprintf("expected one telemetry file, got %d", len(entries)))

	records := readMainTelemetryRecords(filepath.Join(telemetryDir, entries[0].Name()))
	assertP83MCPTelemetryParity(records, root)

	publicOutput := strings.Join([]string{
		mcpClientContentText(allowed),
		readme.Contents[0].Text,
		mcpClientContentText(denied),
	}, "\n")
	for _, forbidden := range []string{root, os.TempDir(), "P83_RAW_FILE_BODY_SENTINEL_DO_NOT_LEAK", `"transport": "cli"`, `"transport":"cli"`} {
		gomega.Expect(publicOutput).NotTo(gomega.ContainSubstring(forbidden), fmt.Sprintf("MCP smoke output leaked forbidden value %q:\n%s", forbidden, publicOutput))

	}

	assertMCPPublicPayloadSafe(publicOutput, root)
	assertNoMCPStrategyLeakage(publicOutput)
})
var _ = ginkgo.It("mcp discovery denies refresh shape before execution", func() {
	root := ginkgo.GinkgoT().TempDir()
	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root

	server := newTestMCPDiscoveryServer(cfg)

	input := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"context_status","arguments":{"refresh":true}}}` + "\n"

	var stdout bytes.Buffer
	gomega.Expect(server.Serve(testContext(), strings.NewReader(input), &stdout)).To(gomega.Succeed())

	frames := decodeMCPFrames(stdout.String())
	call := mcpToolResultFromFrame(frames[0])
	gomega.Expect(call.IsError).To(gomega.BeTrue(), fmt.Sprintf("refresh-capable request should return MCP tool error result: %#v", call))

	payload := decodeMCPToolTextPayload(call)
	gomega.Expect(payload.Sidecar.Error).NotTo(gomega.BeNil(), fmt.Sprintf("unexpected structured denial: %#v", payload.Sidecar.Error))
	gomega.Expect(payload.Sidecar.Error.Class).To(gomega.Equal("profile_denied"), fmt.Sprintf("unexpected structured denial: %#v", payload.Sidecar.Error))
	gomega.Expect(payload.Sidecar.Error.Code).To(gomega.Equal("profile_denied"), fmt.Sprintf("unexpected structured denial: %#v", payload.Sidecar.Error))
	gomega.Expect(payload.Sidecar.Error.Message).To(gomega.ContainSubstring("read_only_profile"), fmt.Sprintf("unexpected structured denial: %#v", payload.Sidecar.Error))

	if _, err := os.Stat(filepath.Join(root, ".idiom", "comprehension")); !os.IsNotExist(err) {
		ginkgo.Fail(fmt.Sprintf("denied MCP refresh should not create comprehension state; stat err=%v", err))
	}

	assertMCPPublicPayloadSafe(stdout.String(), root)
})
var _ = ginkgo.It("mcp discovery denies unlisted mutation without leaking input", func() {
	root := ginkgo.GinkgoT().TempDir()
	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root

	server := newTestMCPDiscoveryServer(cfg)
	privatePath := filepath.Join(root, "private", ".env")
	secret := "P81_SECRET_SENTINEL_DO_NOT_LEAK"
	args := map[string]any{"path": privatePath, "content": secret}
	argsJSON, _ := json.Marshal(args)

	input := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"write_file","arguments":` + string(argsJSON) + `}}` + "\n"

	var stdout bytes.Buffer
	gomega.Expect(server.Serve(testContext(), strings.NewReader(input), &stdout)).To(gomega.Succeed())

	frames := decodeMCPFrames(stdout.String())
	call := mcpToolResultFromFrame(frames[0])
	gomega.Expect(call.IsError).To(gomega.BeTrue(), fmt.Sprintf("mutation request should return MCP tool error result: %#v", call))

	payload := decodeMCPToolTextPayload(call)
	gomega.Expect(payload.Tool).To(gomega.Equal("write_file"), fmt.Sprintf("unexpected mutation denial payload: %#v", payload))
	gomega.Expect(payload.Sidecar.Error).NotTo(gomega.BeNil(), fmt.Sprintf("unexpected mutation denial payload: %#v", payload))
	gomega.Expect(payload.Sidecar.Error.Class).To(gomega.Equal("profile_denied"), fmt.Sprintf("unexpected mutation denial payload: %#v", payload))
	gomega.Expect(stdout.String()).NotTo(gomega.ContainSubstring(privatePath), fmt.Sprintf("MCP denial leaked request values:\n%s", stdout.String()))
	gomega.Expect(stdout.String()).NotTo(gomega.ContainSubstring(secret), fmt.Sprintf("MCP denial leaked request values:\n%s", stdout.String()))

	if _, err := os.Stat(privatePath); !os.IsNotExist(err) {
		ginkgo.Fail(fmt.Sprintf("denied MCP mutation should not create %s; stat err=%v", privatePath, err))
	}

	assertMCPPublicPayloadSafe(stdout.String(), root)
})
var _ = ginkgo.It("mcp discovery resources and roots are metadata only", func() {
	root := ginkgo.GinkgoT().TempDir()
	gomega.Expect(os.WriteFile(filepath.Join(root, "README.md"), []byte("P82_RAW_FILE_BODY_SENTINEL_DO_NOT_LEAK\n"), 0o644)).To(gomega.Succeed())

	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root

	server := newTestMCPDiscoveryServer(cfg)

	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"roots/list","params":{}}`,
		`{"jsonrpc":"2.0","id":3,"method":"resources/list","params":{}}`,
		`{"jsonrpc":"2.0","id":4,"method":"resources/read","params":{"uri":"idiom://workspace/root"}}`,
		`{"jsonrpc":"2.0","id":5,"method":"resources/read","params":{"uri":"idiom://workspace/file/README.md"}}`,
	}, "\n") + "\n"

	var stdout bytes.Buffer
	gomega.Expect(server.Serve(testContext(), strings.NewReader(input), &stdout)).To(gomega.Succeed())

	frames := decodeMCPFrames(stdout.String())
	gomega.Expect(frames).To(gomega.HaveLen(5), fmt.Sprintf("expected 5 responses, got %d:\n%s", len(frames), stdout.String()))

	caps, _ := frames[0].Result["capabilities"].(map[string]any)
	gomega.Expect(caps["resources"]).NotTo(gomega.BeNil(), fmt.Sprintf("initialize response missing resources capability: %#v", frames[0].Result))

	roots, ok := frames[1].Result["roots"].([]any)
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("roots/list response missing one clamped root: %#v", frames[1].Result))
	gomega.Expect(roots).To(gomega.HaveLen(1), fmt.Sprintf("roots/list response missing one clamped root: %#v", frames[1].Result))

	rootEntry, _ := roots[0].(map[string]any)
	gomega.Expect(rootEntry["uri"]).To(gomega.Equal("idiom://workspace/root"), fmt.Sprintf("roots/list exposed unclamped root: %#v", rootEntry))
	gomega.Expect(fmt.Sprint(rootEntry)).NotTo(gomega.ContainSubstring(root), fmt.Sprintf("roots/list exposed unclamped root: %#v", rootEntry))

	resources, ok := frames[2].Result["resources"].([]any)
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("resources/list response missing resources: %#v", frames[2].Result))
	gomega.Expect(resources).NotTo(gomega.BeEmpty(), fmt.Sprintf("resources/list response missing resources: %#v", frames[2].Result))
	gomega.Expect(stdout.String()).To(gomega.Or(
		gomega.ContainSubstring("metadata-only"),
		gomega.ContainSubstring("metadata_only"),
	), fmt.Sprintf("resource surface should declare metadata-only behavior:\n%s", stdout.String()))

	rootPayload := decodeMCPResourcePayload(frames[3])
	gomega.Expect(rootPayload["kind"]).To(gomega.Equal("workspace_root"), fmt.Sprintf("unexpected root resource payload: %#v", rootPayload))
	gomega.Expect(rootPayload["content_available"]).To(gomega.BeFalse(), fmt.Sprintf("unexpected root resource payload: %#v", rootPayload))

	filePayload := decodeMCPResourcePayload(frames[4])
	gomega.Expect(filePayload["relative_path"]).To(gomega.Equal("README.md"), fmt.Sprintf("unexpected file resource metadata: %#v", filePayload))
	gomega.Expect(filePayload["content_available"]).To(gomega.BeFalse(), fmt.Sprintf("unexpected file resource metadata: %#v", filePayload))

	for _, forbidden := range []string{root, "P82_RAW_FILE_BODY_SENTINEL_DO_NOT_LEAK"} {
		gomega.Expect(stdout.String()).NotTo(gomega.ContainSubstring(forbidden), fmt.Sprintf("metadata-only resources leaked forbidden value %q:\n%s", forbidden, stdout.String()))

	}

	assertMCPPublicPayloadSafe(stdout.String(), root)
	assertNoMCPStrategyLeakage(stdout.String())
})
var _ = ginkgo.It("mcp discovery resource freshness matches cli sidecar report only", func() {
	root := ginkgo.GinkgoT().TempDir()
	gomega.Expect(os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/p84\n\ngo 1.22\n"), 0o644)).To(gomega.Succeed())

	sourcePath := filepath.Join(root, "pkg", "pkg.go")
	gomega.Expect(os.MkdirAll(filepath.Dir(sourcePath), 0o755)).To(gomega.Succeed())
	gomega.Expect(os.WriteFile(sourcePath, []byte("package pkg\n\nfunc Run() string { return \"old\" }\n"), 0o644)).To(gomega.Succeed())

	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root

	mgr := workspace.NewManager(cfg.Workspace)
	indexResult := mgr.Execute(testContext(), workspace.ToolRequest{Tool: "context_index", Language: "go"})
	gomega.Expect(indexResult.Error).To(gomega.Equal(""), fmt.Sprintf("context_index fixture failed: %s\n%s", indexResult.Error, indexResult.Output))

	graphPath := filepath.Join(root, ".idiom", "comprehension", "graph.sqlite")
	graphBefore := mustStatForTest(graphPath)
	gomega.Expect(os.WriteFile(sourcePath, []byte("package pkg\n\nfunc Run() string { return \"new\" }\n"), 0o644)).To(gomega.Succeed())

	configPath := writeMainConfigRoot(root)

	var cliStdout, cliStderr bytes.Buffer

	code := toolcmd.RunWithTelemetry([]string{
		"context_status",
		"-config", configPath,
		"--profile", "discovery",
		"--input", `{"path":"pkg/pkg.go","freshness_policy":"report_only","limit":1}`,
	}, &cliStdout, &cliStderr, nil)
	gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("CLI context_status failed with %d stdout=%s stderr=%s", code, cliStdout.String(), cliStderr.String()))

	cliPayload := decodeP84CLISidecarPayload(cliStdout.Bytes())
	gomega.Expect(cliPayload.Sidecar.Freshness["policy"]).To(gomega.Equal("report_only"), fmt.Sprintf("expected stale report-only CLI freshness, got %#v", cliPayload.Sidecar.Freshness))
	gomega.Expect(cliPayload.Sidecar.Freshness["operation"]).To(gomega.Equal("context_status"), fmt.Sprintf("expected stale report-only CLI freshness, got %#v", cliPayload.Sidecar.Freshness))
	gomega.Expect(cliPayload.Sidecar.Freshness["fresh_for_scope"]).To(gomega.BeFalse(), fmt.Sprintf("expected stale report-only CLI freshness, got %#v", cliPayload.Sidecar.Freshness))

	server := newTestMCPDiscoveryServer(cfg)
	input := `{"jsonrpc":"2.0","id":1,"method":"resources/read","params":{"uri":"idiom://workspace/file/pkg/pkg.go"}}` + "\n"

	var mcpStdout bytes.Buffer
	gomega.Expect(server.Serve(testContext(), strings.NewReader(input), &mcpStdout)).To(gomega.Succeed())

	frames := decodeMCPFrames(mcpStdout.String())
	resourcePayload := decodeMCPResourcePayload(frames[0])
	freshness, ok := resourcePayload["freshness"].(map[string]any)
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("resource payload missing freshness: %#v", resourcePayload))

	for _, key := range []string{"policy", "operation", "fresh_for_scope", "stale_path_count", "missing_path_count", "scope"} {
		gomega.Expect(reflect.DeepEqual(freshness[key], cliPayload.Sidecar.Freshness[key])).To(gomega.BeTrue(), fmt.Sprintf("MCP freshness %s = %#v, want CLI sidecar %#v\nmcp=%#v\ncli=%#v", key, freshness[key], cliPayload.Sidecar.Freshness[key], freshness, cliPayload.Sidecar.Freshness))

	}
	gomega.Expect(reflect.DeepEqual(cliPayload.Sidecar.Contract, workspace.BuildToolSidecarContract("context_status", workspace.ToolProfileDiscovery, "cli"))).To(gomega.BeTrue(), fmt.Sprintf("CLI sidecar contract drifted from shared builder: %#v", cliPayload.Sidecar.Contract))

	toolCallInput := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"context_status","arguments":{"path":"pkg/pkg.go","freshness_policy":"report_only","limit":1}}}` + "\n"
	var mcpToolStdout bytes.Buffer
	gomega.Expect(newTestMCPDiscoveryServer(cfg).Serve(testContext(), strings.NewReader(toolCallInput), &mcpToolStdout)).To(gomega.Succeed())

	toolFrames := decodeMCPFrames(mcpToolStdout.String())
	mcpToolPayload := decodeMCPToolTextPayload(mcpToolResultFromFrame(toolFrames[0]))
	gomega.Expect(reflect.DeepEqual(mcpToolPayload.Sidecar.Contract, workspace.BuildToolSidecarContract("context_status", workspace.ToolProfileDiscovery, StdioTransport))).To(gomega.BeTrue(), fmt.Sprintf("MCP sidecar contract drifted from shared builder: %#v", mcpToolPayload.Sidecar.Contract))
	gomega.Expect(cliPayload.Sidecar.Contract.ID).To(gomega.Equal(mcpToolPayload.Sidecar.Contract.ID), fmt.Sprintf("CLI/MCP sidecar contract metadata mismatch:\ncli=%#v\nmcp=%#v", cliPayload.Sidecar.Contract, mcpToolPayload.Sidecar.Contract))
	gomega.Expect(cliPayload.Sidecar.Contract.SchemaVersion).To(gomega.Equal(mcpToolPayload.Sidecar.Contract.SchemaVersion), fmt.Sprintf("CLI/MCP sidecar contract metadata mismatch:\ncli=%#v\nmcp=%#v", cliPayload.Sidecar.Contract, mcpToolPayload.Sidecar.Contract))
	gomega.Expect(reflect.DeepEqual(cliPayload.Sidecar.Contract.Metadata, mcpToolPayload.Sidecar.Contract.Metadata)).To(gomega.BeTrue(), fmt.Sprintf("CLI/MCP sidecar contract metadata mismatch:\ncli=%#v\nmcp=%#v", cliPayload.Sidecar.Contract, mcpToolPayload.Sidecar.Contract))
	gomega.Expect(freshness["refresh_allowed"]).To(gomega.BeFalse(), fmt.Sprintf("MCP resource freshness must remain report-only/no-refresh: %#v", freshness))
	gomega.Expect(freshness["refresh_attempted"]).To(gomega.BeFalse(), fmt.Sprintf("MCP resource freshness must remain report-only/no-refresh: %#v", freshness))

	staleEvidence, ok := freshness["stale_evidence"].(map[string]any)
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("MCP resource should make stale state explicit, got %#v", freshness))
	gomega.Expect(staleEvidence["state"]).NotTo(gomega.Equal("fresh"), fmt.Sprintf("MCP resource should make stale state explicit, got %#v", freshness))

	graphAfter := mustStatForTest(graphPath)
	gomega.Expect(graphAfter.ModTime().Equal(graphBefore.ModTime())).To(gomega.BeTrue(), fmt.Sprintf("MCP report-only resource read changed graph mtime: before=%s after=%s", graphBefore.ModTime(), graphAfter.ModTime()))

	for _, forbidden := range []string{root, os.TempDir(), "package pkg", "return \"new\"", sourcePath} {
		gomega.Expect(mcpStdout.String()).NotTo(gomega.ContainSubstring(forbidden), fmt.Sprintf("freshness parity payload leaked forbidden value %q\nmcp=%s", forbidden, mcpStdout.String()))

	}

	assertMCPPublicPayloadSafe(mcpStdout.String(), root)
	assertNoMCPStrategyLeakage(mcpStdout.String())
})
var _ = ginkgo.It("mcp discovery resource read denies out of root protected and symlink escapes", func() {
	root := ginkgo.GinkgoT().TempDir()
	outside := ginkgo.GinkgoT().TempDir()
	outsidePath := filepath.Join(outside, "outside-secret.txt")
	protectedPath := filepath.Join(root, ".git", "config")
	gomega.Expect(os.MkdirAll(filepath.Dir(protectedPath), 0o755)).To(gomega.Succeed())

	for path, body := range map[string]string{
		outsidePath:                 "P82_OUTSIDE_SENTINEL_DO_NOT_LEAK",
		protectedPath:               "P82_PROTECTED_SENTINEL_DO_NOT_LEAK",
		filepath.Join(root, ".env"): "P82_ENV_SENTINEL_DO_NOT_LEAK",
	} {
		gomega.Expect(os.WriteFile(path, []byte(body), 0o644)).To(gomega.Succeed())

	}
	gomega.Expect(os.Symlink(outsidePath, filepath.Join(root, "link-out"))).To(gomega.Succeed())

	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root

	server := newTestMCPDiscoveryServer(cfg)

	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"resources/read","params":{"uri":"file://` + filepath.ToSlash(outsidePath) + `"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"resources/read","params":{"uri":"idiom://workspace/file/.git/config"}}`,
		`{"jsonrpc":"2.0","id":3,"method":"resources/read","params":{"uri":"idiom://workspace/file/.env"}}`,
		`{"jsonrpc":"2.0","id":4,"method":"resources/read","params":{"uri":"idiom://workspace/file/link-out"}}`,
	}, "\n") + "\n"

	var stdout bytes.Buffer
	gomega.Expect(server.Serve(testContext(), strings.NewReader(input), &stdout)).To(gomega.Succeed())

	frames := decodeMCPFramesAllowErrors(stdout.String())
	gomega.Expect(frames).To(gomega.HaveLen(4), fmt.Sprintf("expected 4 denial responses, got %d:\n%s", len(frames), stdout.String()))

	for _, frame := range frames {
		errFrame := mcpErrorFromFrame(frame)
		gomega.Expect(errFrame.Code).To(gomega.Equal(-32602), fmt.Sprintf("resource denial should be invalid params, got %#v", errFrame))

	}

	for _, forbidden := range []string{
		root,
		outside,
		outsidePath,
		protectedPath,
		"P82_OUTSIDE_SENTINEL_DO_NOT_LEAK",
		"P82_PROTECTED_SENTINEL_DO_NOT_LEAK",
		"P82_ENV_SENTINEL_DO_NOT_LEAK",
	} {
		gomega.Expect(stdout.String()).NotTo(gomega.ContainSubstring(forbidden), fmt.Sprintf("resource denial leaked forbidden value %q:\n%s", forbidden, stdout.String()))

	}

	assertMCPPublicPayloadSafe(stdout.String(), root)
})
var _ = ginkgo.It("mcp discovery resource read denies secrets path before metadata", func() {
	root := ginkgo.GinkgoT().TempDir()
	secretPath := filepath.Join(root, "secrets", "keys.json")
	gomega.Expect(os.MkdirAll(filepath.Dir(secretPath), 0o755)).To(gomega.Succeed())
	gomega.Expect(os.WriteFile(secretPath, []byte("P87_SECRET_RESOURCE_SENTINEL_DO_NOT_LEAK\n"), 0o644)).To(gomega.Succeed())

	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root

	server := newTestMCPDiscoveryServer(cfg)

	input := `{"jsonrpc":"2.0","id":1,"method":"resources/read","params":{"uri":"idiom://workspace/file/secrets/keys.json"}}` + "\n"

	var stdout bytes.Buffer
	gomega.Expect(server.Serve(testContext(), strings.NewReader(input), &stdout)).To(gomega.Succeed())

	frames := decodeMCPFramesAllowErrors(stdout.String())
	gomega.Expect(frames).To(gomega.HaveLen(1), fmt.Sprintf("expected one denial response, got %d:\n%s", len(frames), stdout.String()))

	errFrame := mcpErrorFromFrame(frames[0])
	gomega.Expect(errFrame.Code).To(gomega.Equal(-32602), fmt.Sprintf("secrets resource denial should be invalid params, got %#v", errFrame))

	for _, forbidden := range []string{
		root,
		secretPath,
		"secrets/keys.json",
		"relative_path",
		"size_bytes",
		"contents",
		"P87_SECRET_RESOURCE_SENTINEL_DO_NOT_LEAK",
	} {
		gomega.Expect(stdout.String()).NotTo(gomega.ContainSubstring(forbidden), fmt.Sprintf("secrets resource denial leaked forbidden value %q:\n%s", forbidden, stdout.String()))

	}

	assertMCPPublicPayloadSafe(stdout.String(), root)
})
var _ = ginkgo.It("mcp discovery resource read denies hyphenated private key segment before metadata", func() {
	root := ginkgo.GinkgoT().TempDir()
	privateKeyPath := filepath.Join(root, "safe", "private-key-store", "config.json")
	gomega.Expect(os.MkdirAll(filepath.Dir(privateKeyPath), 0o755)).To(gomega.Succeed())
	gomega.Expect(os.WriteFile(privateKeyPath, []byte("P88_PRIVATE_KEY_RESOURCE_SENTINEL_DO_NOT_LEAK\n"), 0o644)).To(gomega.Succeed())

	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root

	server := newTestMCPDiscoveryServer(cfg)

	input := `{"jsonrpc":"2.0","id":1,"method":"resources/read","params":{"uri":"idiom://workspace/file/safe/private-key-store/config.json"}}` + "\n"

	var stdout bytes.Buffer
	gomega.Expect(server.Serve(testContext(), strings.NewReader(input), &stdout)).To(gomega.Succeed())

	frames := decodeMCPFramesAllowErrors(stdout.String())
	gomega.Expect(frames).To(gomega.HaveLen(1), fmt.Sprintf("expected one denial response, got %d:\n%s", len(frames), stdout.String()))

	errFrame := mcpErrorFromFrame(frames[0])
	gomega.Expect(errFrame.Code).To(gomega.Equal(-32602), fmt.Sprintf("private-key resource denial should be invalid params, got %#v", errFrame))

	for _, forbidden := range []string{
		root,
		privateKeyPath,
		"safe/private-key-store/config.json",
		"private-key-store",
		"relative_path",
		"size_bytes",
		"contents",
		"P88_PRIVATE_KEY_RESOURCE_SENTINEL_DO_NOT_LEAK",
	} {
		gomega.Expect(stdout.String()).NotTo(gomega.ContainSubstring(forbidden), fmt.Sprintf("private-key resource denial leaked forbidden value %q:\n%s", forbidden, stdout.String()))

	}

	assertMCPPublicPayloadSafe(stdout.String(), root)
})
var _ = ginkgo.DescribeTable("denies sensitive markers in any resource path segment",
	func(path string) {
		denied, _ := ResourcePathDenied(path)
		gomega.Expect(denied).To(gomega.BeTrue())
	},
	ginkgo.Entry("git metadata", "safe/.git/config"),
	ginkgo.Entry("idiom state", "safe/.idiom/comprehension/graph.sqlite"),
	ginkgo.Entry("Codex state", "safe/.codex/project-context.md"),
	ginkgo.Entry("agent state", "safe/.agents/config.json"),
	ginkgo.Entry("telemetry log", "safe/logs/telemetry/session.jsonl"),
	ginkgo.Entry("conversation log", "safe/logs/conversations/session.jsonl"),
	ginkgo.Entry("secret cache", "safe/secret-cache/config.json"),
	ginkgo.Entry("credentials", "safe/credentials/config.json"),
	ginkgo.Entry("token cache", "safe/token-cache/config.json"),
	ginkgo.Entry("password store", "safe/password-store/config.json"),
	ginkgo.Entry("hyphenated private-key store", "safe/private-key-store/config.json"),
	ginkgo.Entry("underscored private-key store", "safe/private_key_store/config.json"),
	ginkgo.Entry("environment directory", "safe/.env.d/config.json"),
)
var _ = ginkgo.It("mcp discovery resource read denies protected segments before metadata", func() {
	root := ginkgo.GinkgoT().TempDir()
	fixtures := map[string]string{
		"safe/.git/config":                       "P84_GIT_SENTINEL_DO_NOT_LEAK",
		"safe/.idiom/comprehension/graph.sqlite": "P84_IDIOM_SENTINEL_DO_NOT_LEAK",
		"safe/.codex/project-context.md":         "P84_CODEX_SENTINEL_DO_NOT_LEAK",
		"safe/.agents/config.json":               "P84_AGENTS_SENTINEL_DO_NOT_LEAK",
		"safe/logs/telemetry/session.jsonl":      "P84_TELEMETRY_SENTINEL_DO_NOT_LEAK",
		"safe/logs/conversations/session.jsonl":  "P84_CONVERSATION_SENTINEL_DO_NOT_LEAK",
	}

	var input []string

	for rel, body := range fixtures {
		path := filepath.Join(root, filepath.FromSlash(rel))
		gomega.Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(path, []byte(body), 0o644)).To(gomega.Succeed())

		input = append(input, fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"resources/read","params":{"uri":"idiom://workspace/file/%s"}}`, len(input)+1, rel))
	}

	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root

	server := newTestMCPDiscoveryServer(cfg)

	var stdout bytes.Buffer
	gomega.Expect(server.Serve(testContext(), strings.NewReader(strings.Join(input, "\n")+"\n"), &stdout)).To(gomega.Succeed())

	frames := decodeMCPFramesAllowErrors(stdout.String())
	gomega.Expect(frames).To(gomega.HaveLen(len(fixtures)), fmt.Sprintf("expected %d denial responses, got %d:\n%s", len(fixtures), len(frames), stdout.String()))

	for _, frame := range frames {
		if errFrame := mcpErrorFromFrame(frame); errFrame.Code != -32602 {
			ginkgo.Fail(fmt.Sprintf("protected resource denial should be invalid params, got %#v", errFrame))
		}
	}

	for rel, body := range fixtures {
		for _, forbidden := range []string{rel, filepath.Join(root, filepath.FromSlash(rel)), body, "relative_path", "size_bytes", "contents"} {
			gomega.Expect(stdout.String()).NotTo(gomega.ContainSubstring(forbidden), fmt.Sprintf("protected resource denial leaked forbidden value %q:\n%s", forbidden, stdout.String()))

		}
	}

	assertMCPPublicPayloadSafe(stdout.String(), root)
})
var _ = ginkgo.It("mcp discovery protocol and error frames", func() {
	root := ginkgo.GinkgoT().TempDir()
	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root

	server := newTestMCPDiscoveryServer(cfg)

	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"unknown/method","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":[]}`,
		`{"jsonrpc":"2.0","id":3,"method":"resources/read","params":{}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"write_file","arguments":{"path":"safe.txt","content":"P82_DENIED_MUTATION_SENTINEL"}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"context_status","arguments":{"refresh":true}}}`,
		`{malformed json`,
	}, "\n") + "\n"

	var stdout bytes.Buffer
	gomega.Expect(server.Serve(testContext(), strings.NewReader(input), &stdout)).To(gomega.Succeed())

	frames := decodeMCPFramesAllowErrors(stdout.String())
	gomega.Expect(frames).To(gomega.HaveLen(6), fmt.Sprintf("expected 6 responses, got %d:\n%s", len(frames), stdout.String()))

	if got := mcpErrorFromFrame(frames[0]).Code; got != -32601 {
		ginkgo.Fail(fmt.Sprintf("unknown method error code = %d", got))
	}

	for _, frame := range frames[1:3] {
		if got := mcpErrorFromFrame(frame).Code; got != -32602 {
			ginkgo.Fail(fmt.Sprintf("invalid params error code = %d", got))
		}
	}

	for _, frame := range frames[3:5] {
		call := mcpToolResultFromFrame(frame)
		gomega.Expect(call.IsError).To(gomega.BeTrue(), fmt.Sprintf("denied tool call should be MCP tool error result: %#v", call))

		payload := decodeMCPToolTextPayload(call)
		gomega.Expect(payload.Sidecar.Error).NotTo(gomega.BeNil(), fmt.Sprintf("denied tool call missing structured profile denial: %#v", payload))
		gomega.Expect(payload.Sidecar.Error.Class).To(gomega.Equal("profile_denied"), fmt.Sprintf("denied tool call missing structured profile denial: %#v", payload))

	}

	if got := mcpErrorFromFrame(frames[5]).Code; got != -32700 {
		ginkgo.Fail(fmt.Sprintf("malformed JSON error code = %d", got))
	}
	gomega.Expect(stdout.String()).NotTo(gomega.ContainSubstring("P82_DENIED_MUTATION_SENTINEL"), fmt.Sprintf("denied mutation leaked request content:\n%s", stdout.String()))

	assertMCPPublicPayloadSafe(stdout.String(), root)
})

type decodedMCPFrame struct {
	ID     any            `json:"id"`
	Result map[string]any `json:"result"`
	Error  any            `json:"error"`
}

type decodedMCPError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type decodedMCPToolResult struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	IsError bool `json:"isError"`
}

type decodedMCPToolPayload struct {
	Tool    string `json:"Tool"`
	Error   string `json:"Error"`
	Sidecar struct {
		SchemaVersion string `json:"schema_version"`
		Profile       string `json:"profile"`
		Caller        struct {
			CallerID  string `json:"caller_id"`
			RequestID string `json:"request_id"`
			TraceID   string `json:"trace_id"`
		} `json:"caller"`
		Contract workspace.ToolSidecarContract `json:"contract"`
		Error    *struct {
			Class   string `json:"class"`
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	} `json:"sidecar"`
}

type decodedP84CLISidecarPayload struct {
	Tool    string `json:"Tool"`
	Output  string `json:"Output"`
	Sidecar struct {
		Profile   string                        `json:"profile"`
		Freshness map[string]any                `json:"freshness"`
		Contract  workspace.ToolSidecarContract `json:"contract"`
		Error     any                           `json:"error"`
	} `json:"sidecar"`
}

// test-double: scripted-protocol-peer
type testMCPStdioTransport struct {
	stdin   *io.PipeWriter
	stdout  *bufio.Reader
	close   func() error
	writeMu sync.Mutex
}

func startTestMCPStdioTransport(server *DiscoveryServer) *testMCPStdioTransport {

	serverIn, clientOut := io.Pipe()
	clientIn, serverOut := io.Pipe()
	done := make(chan error, 1)
	ctx := testContext()

	go func() {
		err := server.Serve(ctx, serverIn, serverOut)

		_ = serverOut.Close()

		done <- err
	}()

	transport := &testMCPStdioTransport{
		stdin:  clientOut,
		stdout: bufio.NewReaderSize(clientIn, 1<<16),
	}

	transport.close = func() error {
		_ = clientOut.Close()
		_ = clientIn.Close()

		select {
		case err := <-done:
			if err != nil && !strings.Contains(err.Error(), "closed pipe") {
				return err
			}
		case <-time.After(2 * time.Second):
			return fmt.Errorf("test MCP stdio server did not close")
		}

		return nil
	}
	ginkgo.DeferCleanup(func() {
		_ = transport.Close()
	})

	return transport
}

func (t *testMCPStdioTransport) Send(ctx context.Context, payload []byte) error {
	_ = ctx
	t.writeMu.Lock()
	defer t.writeMu.Unlock()

	if _, err := t.stdin.Write(payload); err != nil {
		return err
	}

	if _, err := t.stdin.Write([]byte("\n")); err != nil {
		return err
	}

	return nil
}

func (t *testMCPStdioTransport) Receive(ctx context.Context) (mcppkg.TransportMessage, error) {
	_ = ctx
	payload, err := t.stdout.ReadBytes('\n')
	return mcppkg.TransportMessage{Payload: payload}, err
}

func (t *testMCPStdioTransport) Close() error {
	if t == nil || t.close == nil {
		return nil
	}

	closeFn := t.close

	t.close = nil

	return closeFn()
}

func newTestMCPDiscoveryServer(cfg config.Config) *DiscoveryServer {

	server, err := NewDiscoveryServerWithOptions(cfg, workspace.ToolProfileDiscovery, DiscoveryServerOptions{})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("new MCP discovery server: %v", err))

	return server
}

func testContext() context.Context {

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	ginkgo.DeferCleanup(cancel)

	return ctx
}

func decodeMCPFrames(text string) []decodedMCPFrame {

	var frames []decodedMCPFrame

	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}

		var frame decodedMCPFrame
		gomega.Expect(json.Unmarshal([]byte(line), &frame)).To(gomega.Succeed())
		gomega.Expect(frame.Error).To(gomega.BeNil(), fmt.Sprintf("unexpected JSON-RPC error frame: %#v", frame))

		frames = append(frames, frame)
	}

	return frames
}

func decodeMCPFramesAllowErrors(text string) []decodedMCPFrame {

	var frames []decodedMCPFrame

	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}

		var frame decodedMCPFrame
		gomega.Expect(json.Unmarshal([]byte(line), &frame)).To(gomega.Succeed())

		frames = append(frames, frame)
	}

	return frames
}

func mcpErrorFromFrame(frame decodedMCPFrame) decodedMCPError {
	gomega.Expect(frame.Error).NotTo(gomega.BeNil(), fmt.Sprintf("expected JSON-RPC error frame, got %#v", frame))

	data, err := json.Marshal(frame.Error)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal error frame: %v", err))

	var rpcErr decodedMCPError
	gomega.Expect(json.Unmarshal(data, &rpcErr)).To(gomega.Succeed())
	gomega.Expect(strings.TrimSpace(rpcErr.Message)).NotTo(gomega.Equal(""), fmt.Sprintf("error frame missing message: %#v", rpcErr))

	return rpcErr
}

func mcpToolsFromFrame(frame decodedMCPFrame) []map[string]any {

	rawTools, ok := frame.Result["tools"].([]any)
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("tools/list response missing tools: %#v", frame))

	tools := make([]map[string]any, 0, len(rawTools))
	for _, raw := range rawTools {
		tool, ok := raw.(map[string]any)
		gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("unexpected tool entry: %#v", raw))

		tools = append(tools, tool)
	}

	return tools
}

func mcpToolResultFromFrame(frame decodedMCPFrame) decodedMCPToolResult {

	data, err := json.Marshal(frame.Result)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal tool result: %v", err))

	var result decodedMCPToolResult
	gomega.Expect(json.Unmarshal(data, &result)).To(gomega.Succeed())
	gomega.Expect(result.Content).To(gomega.HaveLen(1), fmt.Sprintf("expected one text content item, got %#v", result.Content))
	gomega.Expect(result.Content[0].Type).To(gomega.Equal("text"), fmt.Sprintf("expected one text content item, got %#v", result.Content))

	return result
}

func decodeMCPResourcePayload(frame decodedMCPFrame) map[string]any {

	rawContents, ok := frame.Result["contents"].([]any)
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("resource read response missing one content item: %#v", frame.Result))
	gomega.Expect(rawContents).To(gomega.HaveLen(1), fmt.Sprintf("resource read response missing one content item: %#v", frame.Result))

	item, ok := rawContents[0].(map[string]any)
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("unexpected resource content item: %#v", rawContents[0]))

	text, ok := item["text"].(string)
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("resource content missing text payload: %#v", item))
	gomega.Expect(strings.TrimSpace(text)).NotTo(gomega.BeEmpty(), fmt.Sprintf("resource content missing text payload: %#v", item))

	var payload map[string]any
	gomega.Expect(json.Unmarshal([]byte(text), &payload)).To(gomega.Succeed())

	return payload
}

func decodeMCPToolTextPayload(result decodedMCPToolResult) decodedMCPToolPayload {

	var payload decodedMCPToolPayload
	gomega.Expect(json.Unmarshal([]byte(result.Content[0].Text), &payload)).To(gomega.Succeed())

	return payload
}

func decodeP84CLISidecarPayload(data []byte) decodedP84CLISidecarPayload {

	var payload decodedP84CLISidecarPayload
	gomega.Expect(json.Unmarshal(data, &payload)).To(gomega.Succeed())
	gomega.Expect(payload.Tool).To(gomega.Equal("context_status"), fmt.Sprintf("unexpected CLI sidecar payload: %#v", payload))
	gomega.Expect(payload.Sidecar.Profile).To(gomega.Equal(workspace.ToolProfileDiscovery), fmt.Sprintf("unexpected CLI sidecar payload: %#v", payload))
	gomega.Expect(payload.Sidecar.Error).To(gomega.BeNil(), fmt.Sprintf("unexpected CLI sidecar payload: %#v", payload))
	gomega.Expect(payload.Sidecar.Freshness).NotTo(gomega.BeEmpty(), fmt.Sprintf("CLI payload missing sidecar freshness: %#v", payload))

	return payload
}

func decodeMCPClientToolPayload(result mcppkg.ToolCallResult) decodedMCPToolPayload {
	gomega.Expect(result.Content).To(gomega.HaveLen(1), fmt.Sprintf("expected one MCP client content item, got %#v", result.Content))

	var content struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	gomega.Expect(json.Unmarshal(result.Content[0], &content)).To(gomega.Succeed())
	gomega.Expect(content.Type).To(gomega.Equal("text"), fmt.Sprintf("unexpected MCP client content: %#v", content))
	gomega.Expect(strings.TrimSpace(content.Text)).NotTo(gomega.BeEmpty(), fmt.Sprintf("unexpected MCP client content: %#v", content))

	var payload decodedMCPToolPayload
	gomega.Expect(json.Unmarshal([]byte(content.Text), &payload)).To(gomega.Succeed())

	return payload
}

func mcpClientToolsContain(tools []mcppkg.Tool, name string) bool {
	for _, tool := range tools {
		if tool.Name == name {
			return true
		}
	}

	return false
}

func assertP83MCPTelemetryParity(records []map[string]any, root string) {

	var sawRequest, sawResult bool

	for _, record := range records {
		rendered := mcpJSONForTest(record)
		for _, forbidden := range []string{root, os.TempDir(), "P83_RAW_FILE_BODY_SENTINEL_DO_NOT_LEAK", `"transport":"cli"`, `"transport": "cli"`} {
			gomega.Expect(rendered).NotTo(gomega.ContainSubstring(forbidden), fmt.Sprintf("MCP telemetry leaked forbidden value %q in %s", forbidden, rendered))

		}

		if record["type"] != "tool_request" && record["type"] != "tool_result" {
			continue
		}
		gomega.Expect(record["agent"]).To(gomega.Equal("mcp"), fmt.Sprintf("MCP telemetry attribution mismatch: %#v", record))
		gomega.Expect(record["transport"]).To(gomega.Equal(StdioTransport), fmt.Sprintf("MCP telemetry attribution mismatch: %#v", record))
		gomega.Expect(record["profile"]).To(gomega.Equal(workspace.ToolProfileDiscovery), fmt.Sprintf("MCP telemetry attribution mismatch: %#v", record))
		gomega.Expect(record["caller_id"]).To(gomega.Equal("mcp-smoke"), fmt.Sprintf("MCP telemetry attribution mismatch: %#v", record))
		gomega.Expect(record["request_id"]).To(gomega.Equal("req-p83"), fmt.Sprintf("MCP telemetry attribution mismatch: %#v", record))
		gomega.Expect(record["trace_id"]).To(gomega.Equal("trace:p83"), fmt.Sprintf("MCP telemetry attribution mismatch: %#v", record))
		gomega.Expect(record["sidecar_schema_version"]).To(gomega.Equal(workspace.ToolSidecarEnvelopeVersion), fmt.Sprintf("MCP telemetry attribution mismatch: %#v", record))
		gomega.Expect(record["contract_schema_version"]).To(gomega.Equal(workspace.ToolSchemaExportVersion), fmt.Sprintf("MCP telemetry attribution mismatch: %#v", record))
		gomega.Expect(strings.HasPrefix(stringFieldForTest(record, "contract_id"), "idiom.tool.v1:")).To(gomega.BeTrue(), fmt.Sprintf("MCP telemetry missing contract id: %#v", record))
		gomega.Expect(strings.TrimSpace(stringFieldForTest(record, "telemetry_event_id"))).NotTo(gomega.Equal(""), fmt.Sprintf("MCP telemetry missing telemetry_event_id: %#v", record))

		if record["type"] == "tool_request" && record["tool"] == "context_status" {
			sawRequest = true
		}

		if record["type"] == "tool_result" && record["tool"] == "context_status" {
			sawResult = true
		}
	}
	gomega.Expect(sawRequest).To(gomega.BeTrue(), fmt.Sprintf("missing MCP tool request/result telemetry in %#v", records))
	gomega.Expect(sawResult).To(gomega.BeTrue(), fmt.Sprintf("missing MCP tool request/result telemetry in %#v", records))

}

func mcpClientContentText(result mcppkg.ToolCallResult) string {
	gomega.Expect(result.Content).To(gomega.HaveLen(1), fmt.Sprintf("expected one MCP client content item, got %#v", result.Content))

	var content struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	gomega.Expect(json.Unmarshal(result.Content[0], &content)).To(gomega.Succeed())
	gomega.Expect(content.Type).To(gomega.Equal("text"), fmt.Sprintf("unexpected MCP client content type: %#v", content))

	return content.Text
}

func mustStatForTest(path string) os.FileInfo {

	info, err := os.Stat(path)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("stat %s: %v", path, err))

	return info
}

func mcpJSONForTest(value any) string {

	data, err := json.Marshal(value)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal test JSON: %v", err))

	return string(data)
}

func sharedMCPDiscoverySchema(name string) workspace.ExportedToolSchema {

	tools, err := workspace.BuildMCPDiscoveryToolSchemas(workspace.ToolProfileMCPDiscovery)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("build shared MCP discovery schemas: %v", err))

	for _, tool := range tools {
		if tool.Name == name {
			return tool
		}
	}
	ginkgo.Fail(fmt.Sprintf("missing shared MCP discovery schema for %s", name))

	return workspace.ExportedToolSchema{}
}

func stringSliceFromAny(value any) []string {
	raw, _ := value.([]any)
	out := make([]string, 0, len(raw))

	for _, item := range raw {
		if text, ok := item.(string); ok {
			out = append(out, text)
		}
	}

	return out
}

func jsonObjectsEqual(a []byte, b []byte) bool {
	var (
		left  any
		right any
	)

	if json.Unmarshal(a, &left) != nil || json.Unmarshal(b, &right) != nil {
		return false
	}

	return reflect.DeepEqual(left, right)
}

func assertMCPPublicPayloadSafe(publicPayload string, root string) {

	for _, forbidden := range []string{root, os.TempDir(), "/private/var/", "/Users/"} {
		if strings.TrimSpace(forbidden) != "" {
			gomega.Expect(publicPayload).NotTo(gomega.ContainSubstring(forbidden), fmt.Sprintf("MCP payload leaked private/temp path marker %q:\n%s", forbidden, publicPayload))
		}

	}

	if finding := localops.ForbiddenEvidenceLeak(publicPayload); finding != "" {
		ginkgo.Fail(fmt.Sprintf("MCP payload failed forbidden evidence scan: %s\n%s", finding, publicPayload))
	}
}

func assertNoMCPStrategyLeakage(publicPayload string) {

	lower := strings.ToLower(publicPayload)
	for _, forbidden := range []string{"next action", "next step", "you should", "should call", "must call", "pick a different approach"} {
		gomega.Expect(lower).NotTo(gomega.ContainSubstring(forbidden), fmt.Sprintf("MCP payload contains strategy-prescriptive wording %q:\n%s", forbidden, publicPayload))

	}
}

func writeMainConfigRoot(root string) string {
	ginkgo.GinkgoT().
		Setenv("IDIOM_QUEUE_SOCKET", filepath.Join(root, "missing-runner.sock"))

	path := filepath.Join(ginkgo.GinkgoT().TempDir(), "config.json")
	configJSON := fmt.Sprintf(`{"workspace":{"root":%q,"cwd":%q}}`, root, root)
	gomega.Expect(os.WriteFile(path, []byte(configJSON+"\n"), 0o644)).To(gomega.Succeed())

	return path
}

func stringFieldForTest(record map[string]any, key string) string {
	value, _ := record[key].(string)
	return value
}

func readMainTelemetryRecords(path string) []map[string]any {

	data, err := os.ReadFile(path)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read telemetry log: %v", err))

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	records := make([]map[string]any, 0, len(lines))

	for _, line := range lines {
		var record map[string]any
		gomega.Expect(json.Unmarshal([]byte(line), &record)).To(gomega.Succeed())

		records = append(records, record)
	}

	return records
}
