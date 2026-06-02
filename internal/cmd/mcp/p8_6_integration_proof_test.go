package mcp

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"idiom/internal/config"
	"idiom/internal/localops"
	mcppkg "idiom/internal/mcp"
	"idiom/internal/workspace"
)

type p86MCPIntegrationProof struct {
	SchemaVersion                string                     `json:"schema_version"`
	ProofID                      string                     `json:"proof_id"`
	LinkedP78ProofID             string                     `json:"linked_p7_8_proof_id"`
	ProductEvidenceSignal        string                     `json:"product_evidence_signal"`
	ReleaseGates                 []string                   `json:"release_gates"`
	DefaultCaller                p86MCPCaller               `json:"default_caller"`
	ClientClassificationCoverage []p86MCPClassificationRow  `json:"client_classification_coverage"`
	Privacy                      map[string]bool            `json:"privacy"`
	Limitations                  []string                   `json:"limitations"`
	Requests                     []p86MCPIntegrationRequest `json:"requests"`
}

type p86MCPCaller struct {
	CallerID  string `json:"caller_id"`
	RequestID string `json:"request_id"`
	TraceID   string `json:"trace_id"`
}

type p86MCPClassificationRow struct {
	Classification string `json:"classification"`
	Coverage       string `json:"coverage"`
}

type p86MCPIntegrationRequest struct {
	StepID  string         `json:"step_id"`
	Request map[string]any `json:"request"`
}

func TestP86MCPStdioIntegrationProofReplay(t *testing.T) {
	proof, fixtureData := readP86MCPIntegrationProof(t)
	if proof.SchemaVersion != "idiom.mcp_stdio_integration_proof.v1" ||
		proof.ProofID != "p8.6-mcp-stdio" ||
		proof.LinkedP78ProofID != "p7.8-mcp-stdio" ||
		proof.ProductEvidenceSignal != "integration_proof" {
		t.Fatalf("unexpected P8.6 proof identity: %#v", proof)
	}
	for _, gate := range []string{"P7.8", "P8.6", "P9.8"} {
		if !slices.Contains(proof.ReleaseGates, gate) {
			t.Fatalf("P8.6 proof missing release gate %s: %#v", gate, proof.ReleaseGates)
		}
	}
	for field, leaked := range proof.Privacy {
		if leaked {
			t.Fatalf("P8.6 proof privacy field %s reports leaked content", field)
		}
	}
	assertP86ClassificationCoverage(t, proof.ClientClassificationCoverage)
	if finding := localops.ForbiddenEvidenceLeak(string(fixtureData)); finding != "" {
		t.Fatalf("P8.6 proof fixture leaked forbidden evidence class %q:\n%s", finding, string(fixtureData))
	}

	root := t.TempDir()
	privateBody := "p86 nonpublic file body marker"
	protectedBody := "p86 protected metadata marker"
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte(privateBody+"\n"), 0o644); err != nil {
		t.Fatalf("write public fixture: %v", err)
	}
	protectedPath := filepath.Join(root, ".git", "config")
	if err := os.MkdirAll(filepath.Dir(protectedPath), 0o755); err != nil {
		t.Fatalf("mkdir protected fixture: %v", err)
	}
	if err := os.WriteFile(protectedPath, []byte(protectedBody+"\n"), 0o644); err != nil {
		t.Fatalf("write protected fixture: %v", err)
	}

	cfg := config.Default()
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	caller, err := workspace.NewToolSidecarCaller(proof.DefaultCaller.CallerID, proof.DefaultCaller.RequestID, proof.DefaultCaller.TraceID)
	if err != nil {
		t.Fatalf("proof caller metadata: %v", err)
	}
	server, err := NewDiscoveryServerWithOptions(cfg, workspace.ToolProfileDiscovery, DiscoveryServerOptions{Caller: caller})
	if err != nil {
		t.Fatalf("new MCP discovery server: %v", err)
	}

	var input bytes.Buffer
	for _, step := range proof.Requests {
		if strings.TrimSpace(step.StepID) == "" {
			t.Fatalf("proof request missing step_id: %#v", step)
		}
		frame, err := json.Marshal(step.Request)
		if err != nil {
			t.Fatalf("marshal proof request %s: %v", step.StepID, err)
		}
		input.Write(frame)
		input.WriteByte('\n')
	}
	var stdout bytes.Buffer
	if err := server.Serve(testContext(t), &input, &stdout); err != nil {
		t.Fatalf("replay P8.6 MCP proof: %v", err)
	}
	frames := decodeMCPFramesAllowErrors(t, stdout.String())
	if len(frames) != len(proof.Requests) {
		t.Fatalf("expected %d replay responses, got %d:\n%s", len(proof.Requests), len(frames), stdout.String())
	}
	framesByStep := map[string]decodedMCPFrame{}
	for i, step := range proof.Requests {
		framesByStep[step.StepID] = frames[i]
	}

	assertP86InitializeFrame(t, framesByStep["initialize"])
	assertP86ToolsListFrame(t, framesByStep["tools-list"])
	assertP86AllowedToolCall(t, framesByStep["allowed-tools-call"])
	assertP86StructuredDenial(t, framesByStep["structured-denial"])
	assertP86RootsListFrame(t, framesByStep["roots-list"], root)
	assertP86ResourcesListFrame(t, framesByStep["resources-list"])
	assertP86MetadataResource(t, framesByStep["resource-root-read"], "workspace_root")
	assertP86MetadataResource(t, framesByStep["resource-file-read"], "workspace_path")
	if errFrame := mcpErrorFromFrame(t, framesByStep["protected-resource-denial"]); errFrame.Code != -32602 {
		t.Fatalf("protected resource denial should be invalid params, got %#v", errFrame)
	}

	for _, forbidden := range []string{root, os.TempDir(), privateBody, protectedBody, protectedPath, ".git/config"} {
		if strings.Contains(stdout.String(), forbidden) {
			t.Fatalf("P8.6 MCP replay leaked forbidden value %q:\n%s", forbidden, stdout.String())
		}
	}
	assertMCPPublicPayloadSafe(t, stdout.String(), root)
	assertNoMCPStrategyLeakage(t, stdout.String())
}

func readP86MCPIntegrationProof(t *testing.T) (p86MCPIntegrationProof, []byte) {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get cwd: %v", err)
	}
	path := filepath.Join(filepath.Clean(filepath.Join(cwd, "../../..")), "testdata", "sidecar", "mcp-stdio-integration-proof.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read P8.6 MCP proof fixture: %v", err)
	}
	var proof p86MCPIntegrationProof
	if err := json.Unmarshal(data, &proof); err != nil {
		t.Fatalf("decode P8.6 MCP proof fixture: %v", err)
	}
	return proof, data
}

func assertP86ClassificationCoverage(t *testing.T, rows []p86MCPClassificationRow) {
	t.Helper()
	seen := map[string]bool{}
	for _, row := range rows {
		if strings.TrimSpace(row.Coverage) == "" {
			t.Fatalf("classification row missing generic coverage note: %#v", row)
		}
		seen[row.Classification] = true
	}
	for _, class := range []string{"finance", "healthcare", "public-sector", "enterprise-saas", "tooling-vendor", "local-model", "open-source"} {
		if !seen[class] {
			t.Fatalf("P8.6 MCP proof missing classification coverage for %s", class)
		}
	}
}

func assertP86InitializeFrame(t *testing.T, frame decodedMCPFrame) {
	t.Helper()
	if frame.Result["protocolVersion"] != mcppkg.ProtocolVersion {
		t.Fatalf("initialize protocol version mismatch: %#v", frame.Result)
	}
	caps, _ := frame.Result["capabilities"].(map[string]any)
	if caps["tools"] == nil || caps["resources"] == nil {
		t.Fatalf("initialize response missing tool/resource capabilities: %#v", frame.Result)
	}
}

func assertP86ToolsListFrame(t *testing.T, frame decodedMCPFrame) {
	t.Helper()
	tools := mcpToolsFromFrame(t, frame)
	if !slices.ContainsFunc(tools, func(tool map[string]any) bool { return tool["name"] == "context_status" }) {
		t.Fatalf("tools/list missing context_status: %#v", tools)
	}
	for _, tool := range tools {
		name, _ := tool["name"].(string)
		if !strings.HasPrefix(name, "context_") && name != "recovery_inspect" && name != "recovery_status" {
			t.Fatalf("MCP discovery proof exposed unsupported discovery tool %q", name)
		}
		meta, _ := tool["_meta"].(map[string]any)
		if meta["idiom_schema_version"] != workspace.ToolSchemaExportVersion {
			t.Fatalf("tool missing shared schema metadata: %#v", tool)
		}
	}
}

func assertP86AllowedToolCall(t *testing.T, frame decodedMCPFrame) {
	t.Helper()
	result := mcpToolResultFromFrame(t, frame)
	if result.IsError {
		t.Fatalf("allowed context_status call returned error: %#v", result)
	}
	payload := p86ToolPayloadMap(t, result)
	assertP86ToolSidecar(t, payload, "p86-allowed", false)
	if got, ok := jsonValueAtDottedPath(payload, "sidecar.freshness.policy"); !ok || got != "report_only" {
		t.Fatalf("allowed call missing report-only freshness: %#v", payload)
	}
}

func assertP86StructuredDenial(t *testing.T, frame decodedMCPFrame) {
	t.Helper()
	result := mcpToolResultFromFrame(t, frame)
	if !result.IsError {
		t.Fatalf("refresh request should be a structured denial: %#v", result)
	}
	payload := p86ToolPayloadMap(t, result)
	assertP86ToolSidecar(t, payload, "p86-denied", true)
	if got, ok := jsonValueAtDottedPath(payload, "sidecar.error.class"); !ok || got != "profile_denied" {
		t.Fatalf("denial missing structured profile error: %#v", payload)
	}
}

func assertP86ToolSidecar(t *testing.T, payload map[string]any, requestID string, wantError bool) {
	t.Helper()
	expected := map[string]string{
		"sidecar.schema_version":              workspace.ToolSidecarEnvelopeVersion,
		"sidecar.profile":                     workspace.ToolProfileDiscovery,
		"sidecar.caller.caller_id":            "mcp-proof",
		"sidecar.caller.request_id":           requestID,
		"sidecar.caller.trace_id":             "trace:p86-proof",
		"sidecar.contract.transport":          mcpStdioTransport,
		"sidecar.contract.schema_version":     workspace.ToolSchemaExportVersion,
		"sidecar.contract.decision_authority": "runtime_contract",
	}
	for path, want := range expected {
		got, ok := jsonValueAtDottedPath(payload, path)
		if !ok || got != want {
			t.Fatalf("sidecar path %s = %#v, want %q in %#v", path, got, want, payload)
		}
	}
	errValue, _ := jsonValueAtDottedPath(payload, "sidecar.error")
	if wantError && errValue == nil {
		t.Fatalf("expected sidecar error in %#v", payload)
	}
	if !wantError && errValue != nil {
		t.Fatalf("unexpected sidecar error in %#v", payload)
	}
}

func assertP86RootsListFrame(t *testing.T, frame decodedMCPFrame, root string) {
	t.Helper()
	roots, ok := frame.Result["roots"].([]any)
	if !ok || len(roots) != 1 {
		t.Fatalf("roots/list should expose one clamped root: %#v", frame.Result)
	}
	rendered := mcpJSONForTest(t, roots[0])
	if !strings.Contains(rendered, "idiom://workspace/root") || strings.Contains(rendered, root) {
		t.Fatalf("roots/list did not preserve redacted root metadata: %s", rendered)
	}
}

func assertP86ResourcesListFrame(t *testing.T, frame decodedMCPFrame) {
	t.Helper()
	resources, ok := frame.Result["resources"].([]any)
	if !ok || len(resources) == 0 {
		t.Fatalf("resources/list missing resources: %#v", frame.Result)
	}
	rendered := mcpJSONForTest(t, resources)
	for _, uri := range []string{"idiom://workspace/root", "idiom://workspace/discovery-tools", "idiom://workspace/resource-policy"} {
		if !strings.Contains(rendered, uri) {
			t.Fatalf("resources/list missing %s in %s", uri, rendered)
		}
	}
	if !strings.Contains(rendered, "idiom_metadata_only") {
		t.Fatalf("resources/list should declare metadata-only semantics: %s", rendered)
	}
	if strings.Contains(rendered, "p8_6_deferred") {
		t.Fatalf("resources/list must not report stale deferred P8.6 metadata: %s", rendered)
	}
	if !strings.Contains(rendered, "p8_6_replay_fixture") {
		t.Fatalf("resources/list should link P8.6 replay fixture metadata: %s", rendered)
	}
}

func assertP86MetadataResource(t *testing.T, frame decodedMCPFrame, kind string) {
	t.Helper()
	payload := decodeMCPResourcePayload(t, frame)
	if payload["kind"] != kind || payload["metadata_only"] != true || payload["content_available"] != false {
		t.Fatalf("unexpected metadata resource payload: %#v", payload)
	}
	freshness, ok := payload["freshness"].(map[string]any)
	if !ok {
		t.Fatalf("metadata resource missing freshness: %#v", payload)
	}
	if freshness["policy"] != "report_only" || freshness["refresh_allowed"] != false || freshness["refresh_attempted"] != false {
		t.Fatalf("resource freshness should be report-only with no refresh: %#v", freshness)
	}
}

func p86ToolPayloadMap(t *testing.T, result decodedMCPToolResult) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal([]byte(result.Content[0].Text), &payload); err != nil {
		t.Fatalf("decode MCP tool payload: %v\n%s", err, result.Content[0].Text)
	}
	return payload
}
