package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"idiom/internal/config"
	"idiom/internal/workspace"
)

func TestRunMainMCPServeCommandExists(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runMain([]string{"mcp", "serve", "--profile", "discovery"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("expected usage exit 2 without --stdio, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "requires --stdio") {
		t.Fatalf("expected mcp serve usage diagnostic, got %q", stderr.String())
	}
}

func TestMCPDiscoveryStdioInitializeListAndCallUsesSharedSchemas(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	server := newTestMCPDiscoveryServer(t, cfg)

	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"context_status","arguments":{"limit":1}}}`,
	}, "\n") + "\n"

	var stdout bytes.Buffer
	if err := server.serve(testContext(t), strings.NewReader(input), &stdout); err != nil {
		t.Fatalf("serve MCP frames: %v", err)
	}
	frames := decodeMCPFrames(t, stdout.String())
	if len(frames) != 3 {
		t.Fatalf("expected 3 responses (notification ignored), got %d:\n%s", len(frames), stdout.String())
	}
	if frames[0].ID != float64(1) || frames[0].Result["protocolVersion"] == "" {
		t.Fatalf("unexpected initialize response: %#v", frames[0])
	}

	tools := mcpToolsFromFrame(t, frames[1])
	contextStatusSchema := sharedMCPDiscoverySchema(t, "context_status")
	var sawContextStatus bool
	for _, tool := range tools {
		name, _ := tool["name"].(string)
		if slices.Contains([]string{"read_file", "write_file", "shell", "context_pack", "go_read_symbol", "history_range", "blame_range"}, name) {
			t.Fatalf("P8.1 MCP discovery list exposed deferred or disallowed tool %q: %#v", name, tool)
		}
		if !strings.HasPrefix(name, "context_") {
			t.Fatalf("P8.1 MCP discovery list exposed non-context tool %q: %#v", name, tool)
		}
		meta, _ := tool["_meta"].(map[string]any)
		profiles := stringSliceFromAny(meta["profiles"])
		if !slices.Contains(profiles, workspace.ToolProfileDiscovery) {
			t.Fatalf("listed tool %s missing shared discovery profile metadata: %#v", name, tool)
		}
		if name == "context_status" {
			sawContextStatus = true
			gotSchema, _ := json.Marshal(tool["inputSchema"])
			if !json.Valid(gotSchema) || !jsonObjectsEqual(gotSchema, contextStatusSchema.InputSchema) {
				t.Fatalf("context_status MCP schema drifted from workspace export:\n got %s\nwant %s", gotSchema, contextStatusSchema.InputSchema)
			}
		}
	}
	if !sawContextStatus {
		t.Fatalf("tools/list missing context_status: %#v", tools)
	}

	call := mcpToolResultFromFrame(t, frames[2])
	if call.IsError {
		t.Fatalf("context_status call should succeed: %#v", call)
	}
	payload := decodeMCPToolTextPayload(t, call)
	if payload.Tool != "context_status" || payload.Sidecar.Profile != "discovery" || payload.Sidecar.Error != nil {
		t.Fatalf("unexpected context_status MCP payload: %#v", payload)
	}
	assertMCPPublicPayloadSafe(t, stdout.String(), root)
	assertNoMCPStrategyLeakage(t, stdout.String())
}

func TestMCPDiscoveryDeniesRefreshShapeBeforeExecution(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	server := newTestMCPDiscoveryServer(t, cfg)

	input := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"context_status","arguments":{"refresh":true}}}` + "\n"
	var stdout bytes.Buffer
	if err := server.serve(testContext(t), strings.NewReader(input), &stdout); err != nil {
		t.Fatalf("serve MCP denial frame: %v", err)
	}
	frames := decodeMCPFrames(t, stdout.String())
	call := mcpToolResultFromFrame(t, frames[0])
	if !call.IsError {
		t.Fatalf("refresh-capable request should return MCP tool error result: %#v", call)
	}
	payload := decodeMCPToolTextPayload(t, call)
	if payload.Sidecar.Error == nil ||
		payload.Sidecar.Error.Class != "profile_denied" ||
		payload.Sidecar.Error.Code != "profile_denied" ||
		!strings.Contains(payload.Sidecar.Error.Message, "read_only_profile") {
		t.Fatalf("unexpected structured denial: %#v", payload.Sidecar.Error)
	}
	if _, err := os.Stat(filepath.Join(root, ".idiom", "comprehension")); !os.IsNotExist(err) {
		t.Fatalf("denied MCP refresh should not create comprehension state; stat err=%v", err)
	}
	assertMCPPublicPayloadSafe(t, stdout.String(), root)
}

func TestMCPDiscoveryDeniesUnlistedMutationWithoutLeakingInput(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	server := newTestMCPDiscoveryServer(t, cfg)
	privatePath := filepath.Join(root, "private", ".env")
	secret := "P81_SECRET_SENTINEL_DO_NOT_LEAK"
	args := map[string]any{"path": privatePath, "content": secret}
	argsJSON, _ := json.Marshal(args)

	input := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"write_file","arguments":` + string(argsJSON) + `}}` + "\n"
	var stdout bytes.Buffer
	if err := server.serve(testContext(t), strings.NewReader(input), &stdout); err != nil {
		t.Fatalf("serve MCP mutation denial frame: %v", err)
	}
	frames := decodeMCPFrames(t, stdout.String())
	call := mcpToolResultFromFrame(t, frames[0])
	if !call.IsError {
		t.Fatalf("mutation request should return MCP tool error result: %#v", call)
	}
	payload := decodeMCPToolTextPayload(t, call)
	if payload.Tool != "write_file" || payload.Sidecar.Error == nil || payload.Sidecar.Error.Class != "profile_denied" {
		t.Fatalf("unexpected mutation denial payload: %#v", payload)
	}
	if strings.Contains(stdout.String(), privatePath) || strings.Contains(stdout.String(), secret) {
		t.Fatalf("MCP denial leaked request values:\n%s", stdout.String())
	}
	if _, err := os.Stat(privatePath); !os.IsNotExist(err) {
		t.Fatalf("denied MCP mutation should not create %s; stat err=%v", privatePath, err)
	}
	assertMCPPublicPayloadSafe(t, stdout.String(), root)
}

type decodedMCPFrame struct {
	ID     any            `json:"id"`
	Result map[string]any `json:"result"`
	Error  any            `json:"error"`
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
		Profile string `json:"profile"`
		Error   *struct {
			Class   string `json:"class"`
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	} `json:"sidecar"`
}

func newTestMCPDiscoveryServer(t *testing.T, cfg config.Config) *mcpDiscoveryServer {
	t.Helper()
	server, err := newMCPDiscoveryServer(cfg, workspace.ToolProfileDiscovery)
	if err != nil {
		t.Fatalf("new MCP discovery server: %v", err)
	}
	return server
}

func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func decodeMCPFrames(t *testing.T, text string) []decodedMCPFrame {
	t.Helper()
	var frames []decodedMCPFrame
	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var frame decodedMCPFrame
		if err := json.Unmarshal([]byte(line), &frame); err != nil {
			t.Fatalf("decode MCP frame: %v\nline=%s\nall=%s", err, line, text)
		}
		if frame.Error != nil {
			t.Fatalf("unexpected JSON-RPC error frame: %#v", frame)
		}
		frames = append(frames, frame)
	}
	return frames
}

func mcpToolsFromFrame(t *testing.T, frame decodedMCPFrame) []map[string]any {
	t.Helper()
	rawTools, ok := frame.Result["tools"].([]any)
	if !ok {
		t.Fatalf("tools/list response missing tools: %#v", frame)
	}
	tools := make([]map[string]any, 0, len(rawTools))
	for _, raw := range rawTools {
		tool, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("unexpected tool entry: %#v", raw)
		}
		tools = append(tools, tool)
	}
	return tools
}

func mcpToolResultFromFrame(t *testing.T, frame decodedMCPFrame) decodedMCPToolResult {
	t.Helper()
	data, err := json.Marshal(frame.Result)
	if err != nil {
		t.Fatalf("marshal tool result: %v", err)
	}
	var result decodedMCPToolResult
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("decode tool result: %v\n%s", err, string(data))
	}
	if len(result.Content) != 1 || result.Content[0].Type != "text" {
		t.Fatalf("expected one text content item, got %#v", result.Content)
	}
	return result
}

func decodeMCPToolTextPayload(t *testing.T, result decodedMCPToolResult) decodedMCPToolPayload {
	t.Helper()
	var payload decodedMCPToolPayload
	if err := json.Unmarshal([]byte(result.Content[0].Text), &payload); err != nil {
		t.Fatalf("decode MCP tool text payload: %v\n%s", err, result.Content[0].Text)
	}
	return payload
}

func sharedMCPDiscoverySchema(t *testing.T, name string) workspace.ExportedToolSchema {
	t.Helper()
	tools, err := workspace.BuildMCPDiscoveryToolSchemas()
	if err != nil {
		t.Fatalf("build shared MCP discovery schemas: %v", err)
	}
	for _, tool := range tools {
		if tool.Name == name {
			return tool
		}
	}
	t.Fatalf("missing shared MCP discovery schema for %s", name)
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
	var left any
	var right any
	if json.Unmarshal(a, &left) != nil || json.Unmarshal(b, &right) != nil {
		return false
	}
	return reflect.DeepEqual(left, right)
}

func assertMCPPublicPayloadSafe(t *testing.T, publicPayload string, root string) {
	t.Helper()
	for _, forbidden := range []string{root, os.TempDir(), "/private/var/", "/Users/"} {
		if strings.TrimSpace(forbidden) != "" && strings.Contains(publicPayload, forbidden) {
			t.Fatalf("MCP payload leaked private/temp path marker %q:\n%s", forbidden, publicPayload)
		}
	}
	if finding := localOpsForbiddenEvidenceLeak(publicPayload); finding != "" {
		t.Fatalf("MCP payload failed forbidden evidence scan: %s\n%s", finding, publicPayload)
	}
}

func assertNoMCPStrategyLeakage(t *testing.T, publicPayload string) {
	t.Helper()
	lower := strings.ToLower(publicPayload)
	for _, forbidden := range []string{"next action", "next step", "you should", "should call", "must call", "pick a different approach"} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("MCP payload contains strategy-prescriptive wording %q:\n%s", forbidden, publicPayload)
		}
	}
}
