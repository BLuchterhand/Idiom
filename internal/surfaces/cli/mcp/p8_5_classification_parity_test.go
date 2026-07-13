package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/autonomy"
	"idiom/internal/platform/config"
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

var _ = ginkgo.It("p85 mcp classification parity report", func() {
	repoRoot := repoRootForCmdTest()
	fixturePath := filepath.Join(repoRoot, "testdata", "sidecar", "mcp-classification-parity.json")

	data, err := os.ReadFile(fixturePath)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read P8.5 fixture: %v", err))

	assertP85NoForbiddenFixtureEvidence(string(data))
	assertNoMCPStrategyLeakage(string(data))

	report := decodeP85MCPClassificationParityReport(data)
	gomega.Expect(report.SchemaVersion).To(gomega.Equal("idiom.mcp_classification_parity.v1"), fmt.Sprintf("unexpected P8.5 report identity: %#v", report))
	gomega.Expect(report.Name).To(gomega.Equal("p8.5-mcp-classification-parity"), fmt.Sprintf("unexpected P8.5 report identity: %#v", report))
	gomega.Expect(report.SharedContract.Transport).To(gomega.Equal(mcpStdioTransport), fmt.Sprintf("unexpected P8.5 report identity: %#v", report))

	assertP85SharedContract(report.SharedContract)
	assertP85CoversDefaultClassifications(report)
	assertP85RowsMapToP76Commands(report)
	assertP85CoverageSemantics(report)

	docData, err := os.ReadFile(filepath.Join(repoRoot, "docs", "mcp-classification-parity.md"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read P8.5 docs: %v", err))

	doc := string(docData)
	for _, required := range []string{
		"testdata/sidecar/mcp-classification-parity.json",
		"p85 mcp classification parity report",
		"p85 mcp classification parity examples use generic primitives",
		"-ginkgo.fail-on-empty",
		"P8.6",
	} {
		gomega.Expect(doc).To(gomega.ContainSubstring(required), fmt.Sprintf("P8.5 docs missing %q", required))

	}
})
var _ = ginkgo.It("p85 mcp classification parity examples use generic primitives", func() {
	root := ginkgo.GinkgoT().TempDir()
	writeP85MCPWorkspace(root)

	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root

	mgr := workspace.NewManager(cfg.Workspace)
	indexResult := mgr.Execute(testContext(), workspace.ToolRequest{Tool: "context_index", Language: "go"})
	gomega.Expect(indexResult.Error).To(gomega.Equal(""), fmt.Sprintf("context_index fixture failed: %s\n%s", indexResult.Error, indexResult.Output))

	graphPath := filepath.Join(root, ".idiom", "comprehension", "graph.sqlite")
	graphBefore := mustStatForTest(graphPath)
	sourcePath := filepath.Join(root, "pkg", "pkg.go")
	gomega.Expect(os.WriteFile(sourcePath, []byte("package pkg\n\nfunc Run() string { return \"new\" }\n"), 0o644)).To(gomega.Succeed())

	server := newTestMCPDiscoveryServer(cfg)
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
	gomega.Expect(server.Serve(testContext(), strings.NewReader(input), &stdout)).To(gomega.Succeed())

	output := stdout.String()
	frames := decodeMCPFramesAllowErrors(output)
	gomega.Expect(frames).To(gomega.HaveLen(8), fmt.Sprintf("expected 8 P8.5 MCP frames, got %d:\n%s", len(frames), output))

	assertP85DeniedToolWithoutLeakage(frames[0], output, "write_file", "P85_FINANCE_SENTINEL_DO_NOT_LEAK")
	assertP85ProtectedResourceDeniedWithoutMetadata(frames[1], "P85_HEALTHCARE_SENTINEL_DO_NOT_LEAK")

	publicDenial := assertP85DeniedToolWithoutLeakage(frames[2], output, "shell", "P85_PUBLIC_SENTINEL_DO_NOT_LEAK")
	assertP85ToolPayloadCallerMetadata(publicDenial, "public-ci", "p85-public", "trace:p85-public")

	if _, err := os.Stat(filepath.Join(root, "regulated", "ledger.txt")); !os.IsNotExist(err) {
		ginkgo.Fail(fmt.Sprintf("MCP finance denial should not write regulated fixture; stat err=%v", err))
	}

	if _, err := os.Stat(filepath.Join(root, "network-side-effect.txt")); !os.IsNotExist(err) {
		ginkgo.Fail(fmt.Sprintf("MCP public-sector denial should not create side-effect marker; stat err=%v", err))
	}

	assertP85CallerMetadata(mcpToolResultFromFrame(frames[3]), "enterprise-ci", "p85-enterprise", "trace:p85-enterprise")
	assertP85CallerMetadata(mcpToolResultFromFrame(frames[4]), "tooling-vendor", "p85-tooling", "trace:p85-tooling")

	tools := mcpToolsFromFrame(frames[5])
	gomega.Expect(p85MCPToolsContain(tools, "context_status")).To(gomega.BeTrue(), fmt.Sprintf("tooling-vendor MCP parity should expose schema-derived context_status: %#v", tools))

	discoveryToolsPayload := decodeMCPResourcePayload(frames[6])
	gomega.Expect(discoveryToolsPayload["kind"]).To(gomega.Equal("discovery_tools"), fmt.Sprintf("tooling-vendor discovery-tools resource missing metadata-only contract: %#v", discoveryToolsPayload))
	gomega.Expect(discoveryToolsPayload["metadata_only"]).To(gomega.BeTrue(), fmt.Sprintf("tooling-vendor discovery-tools resource missing metadata-only contract: %#v", discoveryToolsPayload))
	gomega.Expect(discoveryToolsPayload["profile"]).To(gomega.Equal(workspace.ToolProfileDiscovery), fmt.Sprintf("tooling-vendor discovery-tools resource missing metadata-only contract: %#v", discoveryToolsPayload))

	openSourcePayload := decodeMCPResourcePayload(frames[7])
	gomega.Expect(openSourcePayload["relative_path"]).To(gomega.Equal("pkg/pkg.go"), fmt.Sprintf("open-source resource metadata mismatch: %#v", openSourcePayload))
	gomega.Expect(openSourcePayload["metadata_only"]).To(gomega.BeTrue(), fmt.Sprintf("open-source resource metadata mismatch: %#v", openSourcePayload))
	gomega.Expect(openSourcePayload["content_available"]).To(gomega.BeFalse(), fmt.Sprintf("open-source resource metadata mismatch: %#v", openSourcePayload))

	freshness, ok := openSourcePayload["freshness"].(map[string]any)
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("open-source resource freshness should be stale report-only/no-refresh: %#v", freshness))
	gomega.Expect(freshness["policy"]).To(gomega.Equal("report_only"), fmt.Sprintf("open-source resource freshness should be stale report-only/no-refresh: %#v", freshness))
	gomega.Expect(freshness["refresh_allowed"]).To(gomega.BeFalse(), fmt.Sprintf("open-source resource freshness should be stale report-only/no-refresh: %#v", freshness))
	gomega.Expect(freshness["refresh_attempted"]).To(gomega.BeFalse(), fmt.Sprintf("open-source resource freshness should be stale report-only/no-refresh: %#v", freshness))
	gomega.Expect(freshness["fresh_for_scope"]).To(gomega.BeFalse(), fmt.Sprintf("open-source resource freshness should be stale report-only/no-refresh: %#v", freshness))

	graphAfter := mustStatForTest(graphPath)
	gomega.Expect(graphAfter.ModTime().Equal(graphBefore.ModTime())).To(gomega.BeTrue(), fmt.Sprintf("P8.5 MCP report-only resource read changed graph mtime: before=%s after=%s", graphBefore.ModTime(), graphAfter.ModTime()))

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
		if strings.TrimSpace(forbidden) != "" {
			gomega.Expect(output).NotTo(gomega.ContainSubstring(forbidden), fmt.Sprintf("P8.5 MCP parity output leaked forbidden value %q:\n%s", forbidden, output))
		}

	}

	assertNoMCPStrategyLeakage(output)
})

func decodeP85MCPClassificationParityReport(data []byte) p85MCPClassificationParityReport {

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()

	var report p85MCPClassificationParityReport
	gomega.Expect(dec.Decode(&report)).To(gomega.Succeed())
	gomega.Expect(dec.More()).To(gomega.BeFalse(), fmt.Sprintf("P8.5 report has unexpected trailing JSON"))

	return report
}

func assertP85SharedContract(shared p85MCPSharedContract) {

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
		gomega.Expect(slices.Contains(shared.GenericPrimitives, required)).To(gomega.BeTrue(), fmt.Sprintf("P8.5 shared primitives missing %q: %#v", required, shared.GenericPrimitives))

	}

	for _, forbidden := range []string{"raw_prompt", "raw_file_body", "secret_value", "private_absolute_path", "unredacted_telemetry_path"} {
		gomega.Expect(slices.Contains(shared.ForbiddenEvidenceFields, forbidden)).To(gomega.BeTrue(), fmt.Sprintf("P8.5 forbidden evidence fields missing %q: %#v", forbidden, shared.ForbiddenEvidenceFields))

	}
}

func assertP85CoversDefaultClassifications(report p85MCPClassificationParityReport) {

	seen := map[autonomy.ClientClassification]p85MCPClassificationCoverage{}
	for _, row := range report.Classifications {
		gomega.Expect(seen[row.Classification].Classification).To(gomega.BeEmpty(), fmt.Sprintf("duplicate P8.5 classification row for %q", row.Classification))

		seen[row.Classification] = row
	}

	for _, classification := range autonomy.DefaultClientClassifications() {
		if _, ok := seen[classification]; !ok {
			ginkgo.Fail(fmt.Sprintf("P8.5 report missing classification %q", classification))
		}
	}
	gomega.Expect(seen).To(gomega.HaveLen(len(autonomy.DefaultClientClassifications())), fmt.Sprintf("P8.5 report includes non-default classifications: %#v", seen))

}

func assertP85RowsMapToP76Commands(report p85MCPClassificationParityReport) {

	p76Commands := p85P76CommandIDs()
	for _, row := range report.Classifications {
		gomega.Expect(row.P76CommandID).To(gomega.Equal(p76Commands[row.Classification]), fmt.Sprintf("P8.5 row %q P7.6 command = %q, want %q", row.Classification, row.P76CommandID, p76Commands[row.Classification]))
		gomega.Expect(row.EvidenceCaseID).NotTo(gomega.BeEmpty(), fmt.Sprintf("P8.5 row %q missing evidence case or limitation: %#v", row.Classification, row))
		gomega.Expect(row.TransportLimitation).NotTo(gomega.BeEmpty(), fmt.Sprintf("P8.5 row %q missing evidence case or limitation: %#v", row.Classification, row))
		if row.Status == "not_applicable" {
			gomega.Expect(strings.TrimSpace(row.NotApplicableRationale)).NotTo(gomega.BeEmpty(), fmt.Sprintf("P8.5 row %q is not applicable without rationale: %#v", row.Classification, row))
		}
		if row.Status == "covered" {
			gomega.Expect(row.RequiredEvidenceFields).NotTo(gomega.BeEmpty(), fmt.Sprintf("P8.5 covered row %q must declare evidence fields: %#v", row.Classification, row))
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

func assertP85CoverageSemantics(report p85MCPClassificationParityReport) {

	rows := map[autonomy.ClientClassification]p85MCPClassificationCoverage{}
	for _, row := range report.Classifications {
		rows[row.Classification] = row
		for _, primitive := range row.GenericPrimitives {
			gomega.Expect(slices.Contains(report.SharedContract.GenericPrimitives, primitive)).To(gomega.BeTrue(), fmt.Sprintf("P8.5 row %q uses non-shared primitive %q", row.Classification, primitive))

			for _, classification := range autonomy.DefaultClientClassifications() {
				gomega.Expect(primitive).NotTo(gomega.ContainSubstring(string(classification)), fmt.Sprintf("P8.5 primitive %q is classification-specific", primitive))

			}
		}
	}

	for _, classification := range []autonomy.ClientClassification{autonomy.ClientFinance, autonomy.ClientHealthcare, autonomy.ClientPublicSector} {
		row := rows[classification]
		gomega.Expect(row.Status).To(gomega.Equal("covered"), fmt.Sprintf("regulated P8.5 row %q must be covered by redaction/resource/protected-path semantics: %#v", classification, row))
		gomega.Expect(p85HasAny(row.ClaimDependencies, "redaction_semantics", "protected_path_denial", "resource_semantics")).To(gomega.BeTrue(), fmt.Sprintf("regulated P8.5 row %q must be covered by redaction/resource/protected-path semantics: %#v", classification, row))

	}

	for _, classification := range []autonomy.ClientClassification{autonomy.ClientEnterpriseSaaS, autonomy.ClientToolingVendor} {
		row := rows[classification]
		gomega.Expect(row.Status).To(gomega.Equal("covered"), fmt.Sprintf("integration P8.5 row %q must cover caller metadata: %#v", classification, row))
		gomega.Expect(row.ClaimDependencies).To(gomega.ContainElement("caller_metadata_attribution"), fmt.Sprintf("integration P8.5 row %q must cover caller metadata: %#v", classification, row))

	}

	local := rows[autonomy.ClientLocalModel]
	gomega.Expect(local.Status).To(gomega.Equal("not_applicable"), fmt.Sprintf("local-model P8.5 row should carry explicit not-applicable rationale: %#v", local))
	gomega.Expect(strings.TrimSpace(local.NotApplicableRationale)).NotTo(gomega.BeEmpty(), fmt.Sprintf("local-model P8.5 row should carry explicit not-applicable rationale: %#v", local))
	gomega.Expect(local.TransportLimitation).To(gomega.ContainSubstring("does not expose local_ops_benchmark"), fmt.Sprintf("local-model P8.5 row should carry explicit not-applicable rationale: %#v", local))

	openSource := rows[autonomy.ClientOpenSource]
	gomega.Expect(openSource.Status).To(gomega.Equal("covered"), fmt.Sprintf("open-source P8.5 row should cover MCP metadata/freshness with context_pack limitation: %#v", openSource))
	gomega.Expect(openSource.ClaimDependencies).To(gomega.ContainElement("freshness_report_only"), fmt.Sprintf("open-source P8.5 row should cover MCP metadata/freshness with context_pack limitation: %#v", openSource))
	gomega.Expect(openSource.TransportLimitation).To(gomega.ContainSubstring("does not expose context_pack"), fmt.Sprintf("open-source P8.5 row should cover MCP metadata/freshness with context_pack limitation: %#v", openSource))

}

func writeP85MCPWorkspace(root string) {

	writeP85MCPFile(filepath.Join(root, "go.mod"), "module example.com/p85\n\ngo 1.22\n")
	writeP85MCPFile(filepath.Join(root, "README.md"), "P85_README_SENTINEL_DO_NOT_LEAK\n")
	writeP85MCPFile(filepath.Join(root, "pkg", "pkg.go"), "package pkg\n\nfunc Run() string { return \"old\" }\n")
	writeP85MCPFile(filepath.Join(root, "secrets", "records.json"), "P85_HEALTHCARE_SENTINEL_DO_NOT_LEAK\n")
}

func writeP85MCPFile(path string, content string) {
	gomega.Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(gomega.Succeed())
	gomega.Expect(os.WriteFile(path, []byte(content), 0o644)).To(gomega.Succeed())

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

func assertP85DeniedToolWithoutLeakage(frame decodedMCPFrame, output string, tool string, forbidden string) decodedMCPToolPayload {

	call := mcpToolResultFromFrame(frame)
	gomega.Expect(call.IsError).To(gomega.BeTrue(), fmt.Sprintf("%s should be denied by MCP discovery profile: %#v", tool, call))

	payload := decodeMCPToolTextPayload(call)
	gomega.Expect(payload.Tool).To(gomega.Equal(tool), fmt.Sprintf("unexpected P8.5 denied %s payload: %#v", tool, payload))
	gomega.Expect(payload.Sidecar.Error).NotTo(gomega.BeNil(), fmt.Sprintf("unexpected P8.5 denied %s payload: %#v", tool, payload))
	gomega.Expect(payload.Sidecar.Error.Class).To(gomega.Equal("profile_denied"), fmt.Sprintf("unexpected P8.5 denied %s payload: %#v", tool, payload))
	gomega.Expect(payload.Sidecar.Error.Code).To(gomega.Equal("profile_denied"), fmt.Sprintf("unexpected P8.5 denied %s payload: %#v", tool, payload))
	gomega.Expect(payload.Sidecar.Contract.Transport).To(gomega.Equal(mcpStdioTransport), fmt.Sprintf("unexpected P8.5 denied %s payload: %#v", tool, payload))
	gomega.Expect(output).NotTo(gomega.ContainSubstring(forbidden), fmt.Sprintf("denied %s leaked forbidden marker %q:\n%s", tool, forbidden, output))

	return payload
}

func assertP85ProtectedResourceDeniedWithoutMetadata(frame decodedMCPFrame, forbidden string) {

	errFrame := mcpErrorFromFrame(frame)
	gomega.Expect(errFrame.Code).To(gomega.Equal(-32602), fmt.Sprintf("protected resource denial should be invalid params, got %#v", errFrame))

	rendered := mcpJSONForTest(frame)
	for _, marker := range []string{forbidden, "secrets/records.json", "relative_path", "size_bytes", "contents"} {
		gomega.Expect(rendered).NotTo(gomega.ContainSubstring(marker), fmt.Sprintf("protected resource denial leaked marker %q:\n%s", marker, rendered))

	}
}

func assertP85CallerMetadata(result decodedMCPToolResult, callerID string, requestID string, traceID string) {
	gomega.Expect(result.IsError).To(gomega.BeFalse(), fmt.Sprintf("caller metadata proof should use allowed context_status call: %#v", result))

	payload := decodeMCPToolTextPayload(result)
	gomega.Expect(payload.Tool).To(gomega.Equal("context_status"), fmt.Sprintf("P8.5 caller metadata mismatch: %#v", payload.Sidecar))
	gomega.Expect(payload.Sidecar.Contract.Transport).To(gomega.Equal(mcpStdioTransport), fmt.Sprintf("P8.5 caller metadata mismatch: %#v", payload.Sidecar))

	assertP85ToolPayloadCallerMetadata(payload, callerID, requestID, traceID)
}

func assertP85ToolPayloadCallerMetadata(payload decodedMCPToolPayload, callerID string, requestID string, traceID string) {
	gomega.Expect(payload.Sidecar.Caller.CallerID).To(gomega.Equal(callerID), fmt.Sprintf("P8.5 caller metadata mismatch: %#v", payload.Sidecar))
	gomega.Expect(payload.Sidecar.Caller.RequestID).To(gomega.Equal(requestID), fmt.Sprintf("P8.5 caller metadata mismatch: %#v", payload.Sidecar))
	gomega.Expect(payload.Sidecar.Caller.TraceID).To(gomega.Equal(traceID), fmt.Sprintf("P8.5 caller metadata mismatch: %#v", payload.Sidecar))
	gomega.Expect(payload.Sidecar.Contract.Transport).To(gomega.Equal(mcpStdioTransport), fmt.Sprintf("P8.5 caller metadata mismatch: %#v", payload.Sidecar))

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

func assertP85NoForbiddenFixtureEvidence(text string) {

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
		gomega.Expect(text).NotTo(gomega.ContainSubstring(forbidden), fmt.Sprintf("P8.5 fixture contains forbidden privacy pattern %q", forbidden))

	}
}

func repoRootForCmdTest() string {

	dir, err := os.Getwd()
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("get cwd: %v", err))

	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}

		parent := filepath.Dir(dir)
		gomega.Expect(parent).NotTo(gomega.Equal(dir), fmt.Sprintf("repo root not found from cwd"))

		dir = parent
	}
}
