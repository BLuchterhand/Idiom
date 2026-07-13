package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	mcppkg "idiom/internal/capabilities/mcp"
	"idiom/internal/capabilities/workspace"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/localops"
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

var _ = ginkgo.Describe("P8.6 MCP stdio proof", ginkgo.Label("integration"), func() {
	ginkgo.It("replays the release proof without leaking protected evidence", func() {
		t := ginkgo.GinkgoT()
		proof, fixtureData := readP86MCPIntegrationProof()
		gomega.Expect(proof.SchemaVersion).To(gomega.Equal("idiom.mcp_stdio_integration_proof.v1"))
		gomega.Expect(proof.ProofID).To(gomega.Equal("p8.6-mcp-stdio"))
		gomega.Expect(proof.LinkedP78ProofID).To(gomega.Equal("p7.8-mcp-stdio"))
		gomega.Expect(proof.ProductEvidenceSignal).To(gomega.Equal("integration_proof"))

		for _, gate := range []string{"P7.8", "P8.6", "P9.8"} {
			gomega.Expect(proof.ReleaseGates).To(gomega.ContainElement(gate))
		}

		for field, leaked := range proof.Privacy {
			gomega.Expect(leaked).To(gomega.BeFalse(), "privacy field %s reports leaked content", field)
		}

		assertP86ClassificationCoverage(proof.ClientClassificationCoverage)

		gomega.Expect(localops.ForbiddenEvidenceLeak(string(fixtureData))).To(gomega.BeEmpty())

		root := t.TempDir()
		privateBody := "p86 nonpublic file body marker"
		protectedBody := "p86 protected metadata marker"

		gomega.Expect(os.WriteFile(filepath.Join(root, "README.md"), []byte(privateBody+"\n"), 0o644)).To(gomega.Succeed())

		protectedPath := filepath.Join(root, ".git", "config")
		gomega.Expect(os.MkdirAll(filepath.Dir(protectedPath), 0o755)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(protectedPath, []byte(protectedBody+"\n"), 0o644)).To(gomega.Succeed())

		cfg := config.Default()
		cfg.Workspace.Root = root
		cfg.Workspace.CWD = root

		caller, err := workspace.NewToolSidecarCaller(proof.DefaultCaller.CallerID, proof.DefaultCaller.RequestID, proof.DefaultCaller.TraceID)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		server, err := NewDiscoveryServerWithOptions(cfg, workspace.ToolProfileDiscovery, DiscoveryServerOptions{Caller: caller})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		var input bytes.Buffer

		for _, step := range proof.Requests {
			gomega.Expect(strings.TrimSpace(step.StepID)).NotTo(gomega.BeEmpty())

			frame, err := json.Marshal(step.Request)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), "step %s", step.StepID)
			input.Write(frame)
			input.WriteByte('\n')
		}

		var stdout bytes.Buffer
		gomega.Expect(server.Serve(testContext(), &input, &stdout)).To(gomega.Succeed())

		frames := decodeMCPFramesAllowErrors(stdout.String())
		gomega.Expect(frames).To(gomega.HaveLen(len(proof.Requests)), stdout.String())

		framesByStep := map[string]decodedMCPFrame{}
		for i, step := range proof.Requests {
			framesByStep[step.StepID] = frames[i]
		}

		assertP86InitializeFrame(framesByStep["initialize"])
		assertP86ToolsListFrame(framesByStep["tools-list"])
		assertP86AllowedToolCall(framesByStep["allowed-tools-call"])
		assertP86StructuredDenial(framesByStep["structured-denial"])
		assertP86RootsListFrame(framesByStep["roots-list"], root)
		assertP86ResourcesListFrame(framesByStep["resources-list"])
		assertP86MetadataResource(framesByStep["resource-root-read"], "workspace_root")
		assertP86MetadataResource(framesByStep["resource-file-read"], "workspace_path")

		errFrame := mcpErrorFromFrame(framesByStep["protected-resource-denial"])
		gomega.Expect(errFrame.Code).To(gomega.Equal(-32602))

		for _, forbidden := range []string{root, os.TempDir(), privateBody, protectedBody, protectedPath, ".git/config"} {
			gomega.Expect(stdout.String()).NotTo(gomega.ContainSubstring(forbidden))
		}

		assertMCPPublicPayloadSafe(stdout.String(), root)
		assertNoMCPStrategyLeakage(stdout.String())
	})
})

func readP86MCPIntegrationProof() (p86MCPIntegrationProof, []byte) {

	path := filepath.Join(repoRootForCmdTest(), "testdata", "sidecar", "mcp-stdio-integration-proof.json")

	data, err := os.ReadFile(path)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read P8.6 MCP proof fixture: %v", err))

	var proof p86MCPIntegrationProof
	gomega.Expect(json.Unmarshal(data, &proof)).To(gomega.Succeed())

	return proof, data
}

func assertP86ClassificationCoverage(rows []p86MCPClassificationRow) {

	seen := map[string]bool{}

	for _, row := range rows {
		gomega.Expect(strings.TrimSpace(row.Coverage)).NotTo(gomega.Equal(""), fmt.Sprintf("classification row missing generic coverage note: %#v", row))

		seen[row.Classification] = true
	}

	for _, class := range []string{"finance", "healthcare", "public-sector", "enterprise-saas", "tooling-vendor", "local-model", "open-source"} {
		gomega.Expect(seen[class]).To(gomega.BeTrue(), fmt.Sprintf("P8.6 MCP proof missing classification coverage for %s", class))

	}
}

func assertP86InitializeFrame(frame decodedMCPFrame) {
	gomega.Expect(frame.Result["protocolVersion"]).To(gomega.Equal(mcppkg.ProtocolVersion), fmt.Sprintf("initialize protocol version mismatch: %#v", frame.Result))

	caps, _ := frame.Result["capabilities"].(map[string]any)
	gomega.Expect(caps["tools"]).NotTo(gomega.BeNil(), fmt.Sprintf("initialize response missing tool/resource capabilities: %#v", frame.Result))
	gomega.Expect(caps["resources"]).NotTo(gomega.BeNil(), fmt.Sprintf("initialize response missing tool/resource capabilities: %#v", frame.Result))

}

func assertP86ToolsListFrame(frame decodedMCPFrame) {

	tools := mcpToolsFromFrame(frame)
	gomega.Expect(slices.ContainsFunc(tools, func(tool map[string]any) bool { return tool["name"] == "context_status" })).To(gomega.BeTrue(), fmt.Sprintf("tools/list missing context_status: %#v", tools))

	for _, tool := range tools {
		name, _ := tool["name"].(string)
		gomega.Expect(name).To(gomega.Or(
			gomega.HavePrefix("context_"),
			gomega.Equal("recovery_inspect"),
			gomega.Equal("recovery_status"),
		), fmt.Sprintf("MCP discovery proof exposed unsupported discovery tool %q", name))

		meta, _ := tool["_meta"].(map[string]any)
		gomega.Expect(meta["idiom_schema_version"]).To(gomega.Equal(workspace.ToolSchemaExportVersion), fmt.Sprintf("tool missing shared schema metadata: %#v", tool))

	}
}

func assertP86AllowedToolCall(frame decodedMCPFrame) {

	result := mcpToolResultFromFrame(frame)
	gomega.Expect(result.IsError).To(gomega.BeFalse(), fmt.Sprintf("allowed context_status call returned error: %#v", result))

	payload := p86ToolPayloadMap(result)
	assertP86ToolSidecar(payload, "p86-allowed", false)

	if got, ok := jsonValueAtDottedPath(payload, "sidecar.freshness.policy"); !ok || got != "report_only" {
		ginkgo.Fail(fmt.Sprintf("allowed call missing report-only freshness: %#v", payload))
	}
}

func assertP86StructuredDenial(frame decodedMCPFrame) {

	result := mcpToolResultFromFrame(frame)
	gomega.Expect(result.IsError).To(gomega.BeTrue(), fmt.Sprintf("refresh request should be a structured denial: %#v", result))

	payload := p86ToolPayloadMap(result)
	assertP86ToolSidecar(payload, "p86-denied", true)

	if got, ok := jsonValueAtDottedPath(payload, "sidecar.error.class"); !ok || got != "profile_denied" {
		ginkgo.Fail(fmt.Sprintf("denial missing structured profile error: %#v", payload))
	}
}

func assertP86ToolSidecar(payload map[string]any, requestID string, wantError bool) {

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
		gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("sidecar path %s = %#v, want %q in %#v", path, got, want, payload))
		gomega.Expect(got).To(gomega.Equal(want), fmt.Sprintf("sidecar path %s = %#v, want %q in %#v", path, got, want, payload))

	}

	errValue, _ := jsonValueAtDottedPath(payload, "sidecar.error")
	if wantError {
		gomega.Expect(errValue).NotTo(gomega.BeNil(), fmt.Sprintf("expected sidecar error in %#v", payload))
	} else {
		gomega.Expect(errValue).To(gomega.BeNil(), fmt.Sprintf("unexpected sidecar error in %#v", payload))
	}

}

func assertP86RootsListFrame(frame decodedMCPFrame, root string) {

	roots, ok := frame.Result["roots"].([]any)
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("roots/list should expose one clamped root: %#v", frame.Result))
	gomega.Expect(roots).To(gomega.HaveLen(1), fmt.Sprintf("roots/list should expose one clamped root: %#v", frame.Result))

	rendered := mcpJSONForTest(roots[0])
	gomega.Expect(rendered).To(gomega.ContainSubstring("idiom://workspace/root"), fmt.Sprintf("roots/list did not preserve redacted root metadata: %s", rendered))
	gomega.Expect(rendered).NotTo(gomega.ContainSubstring(root), fmt.Sprintf("roots/list did not preserve redacted root metadata: %s", rendered))

}

func assertP86ResourcesListFrame(frame decodedMCPFrame) {

	resources, ok := frame.Result["resources"].([]any)
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("resources/list missing resources: %#v", frame.Result))
	gomega.Expect(resources).NotTo(gomega.BeEmpty(), fmt.Sprintf("resources/list missing resources: %#v", frame.Result))

	rendered := mcpJSONForTest(resources)
	for _, uri := range []string{"idiom://workspace/root", "idiom://workspace/discovery-tools", "idiom://workspace/resource-policy"} {
		gomega.Expect(rendered).To(gomega.ContainSubstring(uri), fmt.Sprintf("resources/list missing %s in %s", uri, rendered))

	}
	gomega.Expect(rendered).To(gomega.ContainSubstring("idiom_metadata_only"), fmt.Sprintf("resources/list should declare metadata-only semantics: %s", rendered))
	gomega.Expect(rendered).NotTo(gomega.ContainSubstring("p8_6_deferred"), fmt.Sprintf("resources/list must not report stale deferred P8.6 metadata: %s", rendered))
	gomega.Expect(rendered).To(gomega.ContainSubstring("p8_6_replay_fixture"), fmt.Sprintf("resources/list should link P8.6 replay fixture metadata: %s", rendered))

}

func assertP86MetadataResource(frame decodedMCPFrame, kind string) {

	payload := decodeMCPResourcePayload(frame)
	gomega.Expect(payload["kind"]).To(gomega.Equal(kind), fmt.Sprintf("unexpected metadata resource payload: %#v", payload))
	gomega.Expect(payload["metadata_only"]).To(gomega.BeTrue(), fmt.Sprintf("unexpected metadata resource payload: %#v", payload))
	gomega.Expect(payload["content_available"]).To(gomega.BeFalse(), fmt.Sprintf("unexpected metadata resource payload: %#v", payload))

	freshness, ok := payload["freshness"].(map[string]any)
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("metadata resource missing freshness: %#v", payload))
	gomega.Expect(freshness["policy"]).To(gomega.Equal("report_only"), fmt.Sprintf("resource freshness should be report-only with no refresh: %#v", freshness))
	gomega.Expect(freshness["refresh_allowed"]).To(gomega.BeFalse(), fmt.Sprintf("resource freshness should be report-only with no refresh: %#v", freshness))
	gomega.Expect(freshness["refresh_attempted"]).To(gomega.BeFalse(), fmt.Sprintf("resource freshness should be report-only with no refresh: %#v", freshness))

}

func p86ToolPayloadMap(result decodedMCPToolResult) map[string]any {

	var payload map[string]any
	gomega.Expect(json.Unmarshal([]byte(result.Content[0].Text), &payload)).To(gomega.Succeed())

	return payload
}
