package mcp
import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"idiom/internal/autonomy"
	"idiom/internal/config"
	"idiom/internal/workspace"
)

type p85MCPClassificationParityReport struct {
	SchemaVersion   string                         `json:"schema_version"`
	Name            string                         `json:"name"`
	Description     string                         `json:"description"`
	SharedContract  p85MCPSharedContract           `json:"shared_contract"`
	Classifications []p85MCPClassificationCoverage `json:"classifications"`
}

type p85MCPSharedContract struct {
	Transport               string   `json:"transport"`
	DecisionBoundary        string   `json:"decision_boundary"`
	GenericPrimitives       []string `json:"generic_primitives"`
	ForbiddenEvidenceFields []string `json:"forbidden_evidence_fields"`
}

type p85MCPClassificationCoverage struct {
	Classification         autonomy.ClientClassification `json:"classification"`
	P76CommandID           string                        `json:"p7_6_command_id"`
	Status                 string                        `json:"status"`
	ClaimDependencies      []string                      `json:"claim_dependencies"`
	EvidenceCaseID         string                        `json:"evidence_case_id"`
	GenericPrimitives      []string                      `json:"generic_primitives"`
	RequiredEvidenceFields []string                      `json:"required_evidence_fields"`
	TransportLimitation    string                        `json:"transport_limitation"`
	NotApplicableRationale string                        `json:"not_applicable_rationale"`
}

func TestP85MCPClassificationParityReport(t *testing.T) {
	repoRoot := repoRootForCmdTest(t)
	fixturePath := filepath.Join(repoRoot, "testdata", "sidecar", "mcp-classification-parity.json")
	data, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read P8.5 fixture: %v", err)
	}
	assertP85NoForbiddenFixtureEvidence(t, string(data))
	assertNoMCPStrategyLeakage(t, string(data))

	report := decodeP85MCPClassificationParityReport(t, data)
	if report.SchemaVersion != "idiom.mcp_classification_parity.v1" ||
		report.Name != "p8.5-mcp-classification-parity" ||
		report.SharedContract.Transport != mcpStdioTransport {
		t.Fatalf("unexpected P8.5 report identity: %#v", report)
	}
	assertP85SharedContract(t, report.SharedContract)
	assertP85CoversDefaultClassifications(t, report)
	assertP85RowsMapToP76Commands(t, report)
	assertP85CoverageSemantics(t, report)

	docData, err := os.ReadFile(filepath.Join(repoRoot, "docs", "mcp-classification-parity.md"))
	if err != nil {
		t.Fatalf("read P8.5 docs: %v", err)
	}
	doc := string(docData)
	for _, required := range []string{
		"testdata/sidecar/mcp-classification-parity.json",
		"TestP85MCPClassificationParityReport",
		"TestP85MCPClassificationParityExamplesUseGenericPrimitives",
		"P8.6",
	} {
		if !strings.Contains(doc, required) {
			t.Fatalf("P8.5 docs missing %q", required)
		}
	}
}

func TestP85MCPClassificationParityExamplesUseGenericPrimitives(t *testing.T) {
	root := t.TempDir()
	writeP85MCPWorkspace(t, root)
	cfg := config.Default()
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root

	mgr := workspace.NewManager(cfg.Workspace)
	indexResult := mgr.Execute(testContext(t), workspace.ToolRequest{Tool: "context_index", Language: "go"})
	if indexResult.Error != "" {
		t.Fatalf("context_index fixture failed: %s\n%s", indexResult.Error, indexResult.Output)
	}
	graphPath := filepath.Join(root, ".idiom", "comprehension", "graph.sqlite")
	graphBefore := mustStatForTest(t, graphPath)
	sourcePath := filepath.Join(root, "pkg", "pkg.go")
	if err := os.WriteFile(sourcePath, []byte("package pkg\n\nfunc Run() string { return \"new\" }\n"), 0o644); err != nil {
		t.Fatalf("stale source: %v", err)
	}

	server := newTestMCPDiscoveryServer(t, cfg)
	input := strings.Join([]string{
		p85ToolCallFrame(1, "write_file", `{"path":"regulated/ledger.txt","content":"P85_FINANCE_SENTINEL_DO_NOT_LEAK"}`, nil),
		p85ResourceReadFrame(2, "idiom://workspace/file/secrets/records.json"),
		p85ToolCallFrame(3, "shell", `{"command":"printf P85_PUBLIC_SENTINEL_DO_NOT_LEAK > network-side-effect.txt"}`, map[string]any{
			"caller_id":  "public-ci",
			"request_id": "p85-public",
			"trace_id":   "trace:p85-public",
		}),
		p85ToolCallFrame(4, "context_status", `{"limit":1}`, map[string]any{
			"caller_id":  "enterprise-ci",
			"request_id": "p85-enterprise",
			"trace_id":   "trace:p85-enterprise",
		}),
		p85ToolCallFrame(5, "context_status", `{"limit":1}`, map[string]any{
			"idiom": map[string]any{
				"caller_id":  "tooling-vendor",
				"request_id": "p85-tooling",
				"trace_id":   "trace:p85-tooling",
			},
		}),
		`{"jsonrpc":"2.0","id":6,"method":"tools/list","params":{}}`,
		`{"jsonrpc":"2.0","id":7,"method":"resources/read","params":{"uri":"idiom://workspace/discovery-tools"}}`,
		p85ResourceReadFrame(8, "idiom://workspace/file/pkg/pkg.go"),
	}, "\n") + "\n"

	var stdout bytes.Buffer
	if err := server.Serve(testContext(t), strings.NewReader(input), &stdout); err != nil {
		t.Fatalf("serve P8.5 MCP frames: %v", err)
	}
	output := stdout.String()
	frames := decodeMCPFramesAllowErrors(t, output)
	if len(frames) != 8 {
		t.Fatalf("expected 8 P8.5 MCP frames, got %d:\n%s", len(frames), output)
	}

	assertP85DeniedToolWithoutLeakage(t, frames[0], output, "write_file", "P85_FINANCE_SENTINEL_DO_NOT_LEAK")
	assertP85ProtectedResourceDeniedWithoutMetadata(t, frames[1], "P85_HEALTHCARE_SENTINEL_DO_NOT_LEAK")
	publicDenial := assertP85DeniedToolWithoutLeakage(t, frames[2], output, "shell", "P85_PUBLIC_SENTINEL_DO_NOT_LEAK")
	assertP85ToolPayloadCallerMetadata(t, publicDenial, "public-ci", "p85-public", "trace:p85-public")
	if _, err := os.Stat(filepath.Join(root, "regulated", "ledger.txt")); !os.IsNotExist(err) {
		t.Fatalf("MCP finance denial should not write regulated fixture; stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "network-side-effect.txt")); !os.IsNotExist(err) {
		t.Fatalf("MCP public-sector denial should not create side-effect marker; stat err=%v", err)
	}

	assertP85CallerMetadata(t, mcpToolResultFromFrame(t, frames[3]), "enterprise-ci", "p85-enterprise", "trace:p85-enterprise")
	assertP85CallerMetadata(t, mcpToolResultFromFrame(t, frames[4]), "tooling-vendor", "p85-tooling", "trace:p85-tooling")
	tools := mcpToolsFromFrame(t, frames[5])
	if !p85MCPToolsContain(tools, "context_status") {
		t.Fatalf("tooling-vendor MCP parity should expose schema-derived context_status: %#v", tools)
	}
	discoveryToolsPayload := decodeMCPResourcePayload(t, frames[6])
	if discoveryToolsPayload["kind"] != "discovery_tools" ||
		discoveryToolsPayload["metadata_only"] != true ||
		discoveryToolsPayload["profile"] != workspace.ToolProfileDiscovery {
		t.Fatalf("tooling-vendor discovery-tools resource missing metadata-only contract: %#v", discoveryToolsPayload)
	}

	openSourcePayload := decodeMCPResourcePayload(t, frames[7])
	if openSourcePayload["relative_path"] != "pkg/pkg.go" ||
		openSourcePayload["metadata_only"] != true ||
		openSourcePayload["content_available"] != false {
		t.Fatalf("open-source resource metadata mismatch: %#v", openSourcePayload)
	}
	freshness, ok := openSourcePayload["freshness"].(map[string]any)
	if !ok ||
		freshness["policy"] != "report_only" ||
		freshness["refresh_allowed"] != false ||
		freshness["refresh_attempted"] != false ||
		freshness["fresh_for_scope"] != false {
		t.Fatalf("open-source resource freshness should be stale report-only/no-refresh: %#v", freshness)
	}
	graphAfter := mustStatForTest(t, graphPath)
	if !graphAfter.ModTime().Equal(graphBefore.ModTime()) {
		t.Fatalf("P8.5 MCP report-only resource read changed graph mtime: before=%s after=%s", graphBefore.ModTime(), graphAfter.ModTime())
	}

	for _, forbidden := range []string{
		root,
		os.TempDir(),
		"package pkg",
		"return \"new\"",
		sourcePath,
		"P85_FINANCE_SENTINEL_DO_NOT_LEAK",
		"P85_HEALTHCARE_SENTINEL_DO_NOT_LEAK",
		"P85_PUBLIC_SENTINEL_DO_NOT_LEAK",
	} {
		if strings.TrimSpace(forbidden) != "" && strings.Contains(output, forbidden) {
			t.Fatalf("P8.5 MCP parity output leaked forbidden value %q:\n%s", forbidden, output)
		}
	}
	assertNoMCPStrategyLeakage(t, output)
}

func decodeP85MCPClassificationParityReport(t *testing.T, data []byte) p85MCPClassificationParityReport {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var report p85MCPClassificationParityReport
	if err := dec.Decode(&report); err != nil {
		t.Fatalf("decode P8.5 report: %v", err)
	}
	if dec.More() {
		t.Fatalf("P8.5 report has unexpected trailing JSON")
	}
	return report
}

func assertP85SharedContract(t *testing.T, shared p85MCPSharedContract) {
	t.Helper()
	for _, required := range []string{
		"mcp_stdio_transport",
		"tool_sidecar_envelope",
		"caller_metadata_attribution",
		"metadata_only_resources",
		"root_clamped_resources",
		"protected_path_denial",
		"redaction_no_raw_prompt_or_file_body",
		"freshness_report_only",
		"schema_derived_tool_surface",
		"discovery_profile_policy",
	} {
		if !slices.Contains(shared.GenericPrimitives, required) {
			t.Fatalf("P8.5 shared primitives missing %q: %#v", required, shared.GenericPrimitives)
		}
	}
	for _, forbidden := range []string{"raw_prompt", "raw_file_body", "secret_value", "private_absolute_path", "unredacted_telemetry_path"} {
		if !slices.Contains(shared.ForbiddenEvidenceFields, forbidden) {
			t.Fatalf("P8.5 forbidden evidence fields missing %q: %#v", forbidden, shared.ForbiddenEvidenceFields)
		}
	}
}

func assertP85CoversDefaultClassifications(t *testing.T, report p85MCPClassificationParityReport) {
	t.Helper()
	seen := map[autonomy.ClientClassification]p85MCPClassificationCoverage{}
	for _, row := range report.Classifications {
		if seen[row.Classification].Classification != "" {
			t.Fatalf("duplicate P8.5 classification row for %q", row.Classification)
		}
		seen[row.Classification] = row
	}
	for _, classification := range autonomy.DefaultClientClassifications() {
		if _, ok := seen[classification]; !ok {
			t.Fatalf("P8.5 report missing classification %q", classification)
		}
	}
	if len(seen) != len(autonomy.DefaultClientClassifications()) {
		t.Fatalf("P8.5 report includes non-default classifications: %#v", seen)
	}
}

func assertP85RowsMapToP76Commands(t *testing.T, report p85MCPClassificationParityReport) {
	t.Helper()
	p76Commands := p85P76CommandIDs()
	for _, row := range report.Classifications {
		if row.P76CommandID != p76Commands[row.Classification] {
			t.Fatalf("P8.5 row %q P7.6 command = %q, want %q", row.Classification, row.P76CommandID, p76Commands[row.Classification])
		}
		if row.EvidenceCaseID == "" || row.TransportLimitation == "" {
			t.Fatalf("P8.5 row %q missing evidence case or limitation: %#v", row.Classification, row)
		}
		if row.Status == "not_applicable" && strings.TrimSpace(row.NotApplicableRationale) == "" {
			t.Fatalf("P8.5 row %q is not applicable without rationale: %#v", row.Classification, row)
		}
		if row.Status == "covered" && len(row.RequiredEvidenceFields) == 0 {
			t.Fatalf("P8.5 covered row %q must declare evidence fields: %#v", row.Classification, row)
		}
	}
}

func p85P76CommandIDs() map[autonomy.ClientClassification]string {
	return map[autonomy.ClientClassification]string{
		autonomy.ClientFinance:        "finance-discovery-policy",
		autonomy.ClientHealthcare:     "healthcare-redaction-denial",
		autonomy.ClientPublicSector:   "public-sector-offline-side-effect-denial",
		autonomy.ClientEnterpriseSaaS: "enterprise-source-sanity-contract",
		autonomy.ClientToolingVendor:  "tooling-vendor-schema-metadata",
		autonomy.ClientLocalModel:     "local-model-readiness-evidence",
		autonomy.ClientOpenSource:     "open-source-context-pack",
	}
}

func assertP85CoverageSemantics(t *testing.T, report p85MCPClassificationParityReport) {
	t.Helper()
	rows := map[autonomy.ClientClassification]p85MCPClassificationCoverage{}
	for _, row := range report.Classifications {
		rows[row.Classification] = row
		for _, primitive := range row.GenericPrimitives {
			if !slices.Contains(report.SharedContract.GenericPrimitives, primitive) {
				t.Fatalf("P8.5 row %q uses non-shared primitive %q", row.Classification, primitive)
			}
			for _, classification := range autonomy.DefaultClientClassifications() {
				if strings.Contains(primitive, string(classification)) {
					t.Fatalf("P8.5 primitive %q is classification-specific", primitive)
				}
			}
		}
	}
	for _, classification := range []autonomy.ClientClassification{autonomy.ClientFinance, autonomy.ClientHealthcare, autonomy.ClientPublicSector} {
		row := rows[classification]
		if row.Status != "covered" || !p85HasAny(row.ClaimDependencies, "redaction_semantics", "protected_path_denial", "resource_semantics") {
			t.Fatalf("regulated P8.5 row %q must be covered by redaction/resource/protected-path semantics: %#v", classification, row)
		}
	}
	for _, classification := range []autonomy.ClientClassification{autonomy.ClientEnterpriseSaaS, autonomy.ClientToolingVendor} {
		row := rows[classification]
		if row.Status != "covered" || !slices.Contains(row.ClaimDependencies, "caller_metadata_attribution") {
			t.Fatalf("integration P8.5 row %q must cover caller metadata: %#v", classification, row)
		}
	}
	local := rows[autonomy.ClientLocalModel]
	if local.Status != "not_applicable" ||
		strings.TrimSpace(local.NotApplicableRationale) == "" ||
		!strings.Contains(local.TransportLimitation, "does not expose local_ops_benchmark") {
		t.Fatalf("local-model P8.5 row should carry explicit not-applicable rationale: %#v", local)
	}
	openSource := rows[autonomy.ClientOpenSource]
	if openSource.Status != "covered" ||
		!slices.Contains(openSource.ClaimDependencies, "freshness_report_only") ||
		!strings.Contains(openSource.TransportLimitation, "does not expose context_pack") {
		t.Fatalf("open-source P8.5 row should cover MCP metadata/freshness with context_pack limitation: %#v", openSource)
	}
}

func writeP85MCPWorkspace(t *testing.T, root string) {
	t.Helper()
	writeP85MCPFile(t, filepath.Join(root, "go.mod"), "module example.com/p85\n\ngo 1.22\n")
	writeP85MCPFile(t, filepath.Join(root, "README.md"), "P85_README_SENTINEL_DO_NOT_LEAK\n")
	writeP85MCPFile(t, filepath.Join(root, "pkg", "pkg.go"), "package pkg\n\nfunc Run() string { return \"old\" }\n")
	writeP85MCPFile(t, filepath.Join(root, "secrets", "records.json"), "P85_HEALTHCARE_SENTINEL_DO_NOT_LEAK\n")
}

func writeP85MCPFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func p85ToolCallFrame(id int, name string, arguments string, meta map[string]any) string {
	params := map[string]any{
		"name":      name,
		"arguments": json.RawMessage(arguments),
	}
	if meta != nil {
		params["_meta"] = meta
	}
	return p85RPCFrame(id, "tools/call", params)
}

func p85ResourceReadFrame(id int, uri string) string {
	return p85RPCFrame(id, "resources/read", map[string]any{"uri": uri})
}

func p85RPCFrame(id int, method string, params map[string]any) string {
	payload := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		panic(fmt.Sprintf("marshal P8.5 MCP frame: %v", err))
	}
	return string(data)
}

func assertP85DeniedToolWithoutLeakage(t *testing.T, frame decodedMCPFrame, output string, tool string, forbidden string) decodedMCPToolPayload {
	t.Helper()
	call := mcpToolResultFromFrame(t, frame)
	if !call.IsError {
		t.Fatalf("%s should be denied by MCP discovery profile: %#v", tool, call)
	}
	payload := decodeMCPToolTextPayload(t, call)
	if payload.Tool != tool ||
		payload.Sidecar.Error == nil ||
		payload.Sidecar.Error.Class != "profile_denied" ||
		payload.Sidecar.Error.Code != "profile_denied" ||
		payload.Sidecar.Contract.Transport != mcpStdioTransport {
		t.Fatalf("unexpected P8.5 denied %s payload: %#v", tool, payload)
	}
	if strings.Contains(output, forbidden) {
		t.Fatalf("denied %s leaked forbidden marker %q:\n%s", tool, forbidden, output)
	}
	return payload
}

func assertP85ProtectedResourceDeniedWithoutMetadata(t *testing.T, frame decodedMCPFrame, forbidden string) {
	t.Helper()
	errFrame := mcpErrorFromFrame(t, frame)
	if errFrame.Code != -32602 {
		t.Fatalf("protected resource denial should be invalid params, got %#v", errFrame)
	}
	rendered := mcpJSONForTest(t, frame)
	for _, marker := range []string{forbidden, "secrets/records.json", "relative_path", "size_bytes", "contents"} {
		if strings.Contains(rendered, marker) {
			t.Fatalf("protected resource denial leaked marker %q:\n%s", marker, rendered)
		}
	}
}

func assertP85CallerMetadata(t *testing.T, result decodedMCPToolResult, callerID string, requestID string, traceID string) {
	t.Helper()
	if result.IsError {
		t.Fatalf("caller metadata proof should use allowed context_status call: %#v", result)
	}
	payload := decodeMCPToolTextPayload(t, result)
	if payload.Tool != "context_status" ||
		payload.Sidecar.Contract.Transport != mcpStdioTransport {
		t.Fatalf("P8.5 caller metadata mismatch: %#v", payload.Sidecar)
	}
	assertP85ToolPayloadCallerMetadata(t, payload, callerID, requestID, traceID)
}

func assertP85ToolPayloadCallerMetadata(t *testing.T, payload decodedMCPToolPayload, callerID string, requestID string, traceID string) {
	t.Helper()
	if payload.Sidecar.Caller.CallerID != callerID ||
		payload.Sidecar.Caller.RequestID != requestID ||
		payload.Sidecar.Caller.TraceID != traceID ||
		payload.Sidecar.Contract.Transport != mcpStdioTransport {
		t.Fatalf("P8.5 caller metadata mismatch: %#v", payload.Sidecar)
	}
}

func p85MCPToolsContain(tools []map[string]any, name string) bool {
	for _, tool := range tools {
		if tool["name"] == name {
			return true
		}
	}
	return false
}

func p85HasAny(values []string, wants ...string) bool {
	for _, want := range wants {
		if slices.Contains(values, want) {
			return true
		}
	}
	return false
}

func assertP85NoForbiddenFixtureEvidence(t *testing.T, text string) {
	t.Helper()
	for _, forbidden := range []string{
		"/Users/",
		"/private/var/",
		"/tmp/",
		"\\Users\\",
		"CustomerX",
		"private-repo",
		"sk-",
		"ghp_",
		"github_pat_",
		"xoxb-",
		"BEGIN PRIVATE KEY",
		"patient_name:",
		"account_number:",
		"logs/telemetry/",
		"logs/conversations/",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("P8.5 fixture contains forbidden privacy pattern %q", forbidden)
		}
	}
}

func repoRootForCmdTest(t *testing.T) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get cwd: %v", err)
	}
	return filepath.Clean(filepath.Join(cwd, "../../.."))
}
