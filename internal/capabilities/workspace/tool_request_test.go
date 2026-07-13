package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"idiom/internal/platform/config"
	semanticlanguage "idiom/internal/repository/semantic/language"
	"idiom/internal/repository/semantic/languagetools"
)

var _ = ginkgo.Describe("tool request", func() {
	ginkgo.It("parse tool request extracts first json object from tool fence", func() {
		req, raw, ok, err := ParseToolRequest("before\n```tool\n{\"tool\":\"read_file\",\"path\":\"README.md\"}\n```\nafter")
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("ParseToolRequest: %v", err))
		gomega.Expect(ok).To(gomega.BeTrue(), "expected tool request")
		gomega.Expect(req.Tool).To(gomega.Equal("read_file"), fmt.Sprintf("unexpected request: %#v", req))
		gomega.Expect(req.Path).To(gomega.Equal("README.md"), fmt.Sprintf("unexpected request: %#v", req))
		gomega.Expect(raw).To(gomega.Equal(`{"tool":"read_file","path":"README.md"}`), fmt.Sprintf("unexpected raw payload: %q", raw))

	})
})
var _ = ginkgo.Describe("parse tool request accepts plain tool prefix json object", func() {

	cases := []struct {
		name string
		text string
	}{
		{name: "newline", text: "tool\n{\"tool\":\"context_pack\",\"task\":\"explain debug logs\",\"mode\":\"plan\",\"limit\":8}"},
		{name: "adjacent", text: "tool{\"tool\":\"context_pack\",\"task\":\"explain subagents\",\"mode\":\"plan\",\"limit\":10}"},
		{name: "colon", text: "tool: {\"tool\":\"read_file\",\"path\":\"README.md\"}"},
	}
	for _, tc := range cases {
		ginkgo.It(tc.name, func() {
			req, raw, ok, err := ParseToolRequest(tc.text)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("ParseToolRequest: %v", err))
			gomega.Expect(ok).To(gomega.BeTrue(), "expected tool request")
			gomega.Expect(req.Tool).NotTo(gomega.Equal(""), fmt.Sprintf("missing parsed tool: %#v raw=%q", req, raw))
			gomega.Expect(strings.HasPrefix(raw, `{"tool":`)).To(gomega.BeTrue(), fmt.Sprintf("expected JSON object payload, got %q", raw))

		})
	}
})
var _ = ginkgo.Describe("tool request", func() {
	ginkgo.It("parse tool request ignores bare json missing tool", func() {
		_, _, ok, err := ParseToolRequest(`{"path":"internal/example/target.go","start_line":1,"end_line":300}`)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("ParseToolRequest: %v", err))
		gomega.Expect(ok).To(gomega.BeFalse(), "bare JSON without an explicit tool should remain answer content")

	})
})
var _ = ginkgo.Describe("parse tool request rejects bare json side effect aliases", func() {

	cases := []string{
		`{"command":"go test ./..."}`,
		`{"prompt":"inspect this","agent":"reviewer"}`,
		`{"path":"target.go","old_text":"before","new_text":"after"}`,
		`{"rationale":"Verification Checklist:\n- Failing item: needs repair","edits":[{"path":"target.go"}]}`,
		`{"id":"REQ-1","status":"ok"}`,
	}
	for _, tc := range cases {
		ginkgo.It(tc, func() {
			_, _, ok, err := ParseToolRequest(tc)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("ParseToolRequest: %v", err))
			gomega.Expect(ok).To(gomega.BeFalse(), fmt.Sprintf("bare JSON side-effect or answer-shaped payload should remain answer content: %s", tc))

		})
	}
})
var _ = ginkgo.Describe("tool request", func() {
	ginkgo.It("parse tool request ignores bare json without tool shape", func() {
		_, _, ok, err := ParseToolRequest(`{"status":"ok","summary":"done"}`)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("ParseToolRequest: %v", err))
		gomega.Expect(ok).To(gomega.BeFalse(), "ordinary bare JSON should remain answer content")

	})
	ginkgo.It("parse tool request does not treat tool prose as request", func() {
		_, _, ok, err := ParseToolRequest("tool usage should stay prose unless followed by a JSON object")
		gomega.Expect(err).To(gomega.BeNil(), fmt.Sprintf("expected prose to remain a final answer, ok=%t err=%v", ok, err))
		gomega.Expect(ok).To(gomega.BeFalse(), fmt.Sprintf("expected prose to remain a final answer, ok=%t err=%v", ok, err))

	})
	ginkgo.It("parse tool request preserves post readiness gap fields", func() {
		req, _, ok, err := ParseToolRequest("```tool\n{\"tool\":\"search_content\",\"pattern\":\"runner\",\"path\":\"internal\",\"named_gap\":\"find the exact stop-condition branch\",\"why_not_answering\":\"I still need the deciding condition\"}\n```")
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("ParseToolRequest: %v", err))
		gomega.Expect(ok).To(gomega.BeTrue(), "expected tool request")
		gomega.Expect(req.NamedGap).To(gomega.Equal("find the exact stop-condition branch"), fmt.Sprintf("unexpected named gap: %#v", req))
		gomega.Expect(req.WhyNotAnswering).To(gomega.Equal("I still need the deciding condition"), fmt.Sprintf("unexpected why_not_answering: %#v", req))

	})
	ginkgo.It("parse tool request repairs raw control chars inside string values", func() {
		raw := "{\n" +
			"  \"tool\":\"edit_file\",\n" +
			"  \"path\":\"internal/labels/labels.go\",\n" +
			"  \"old_text\":\"func ShortLabel() string {\\n\treturn \\\"Save changes\\\"\\n}\",\n" +
			"  \"new_text\":\"func ShortLabel() string {\\n\treturn \\\"Save\\\"\\n}\",\n" +
			"  \"description\":\"shorten label\"\n" +
			"}"

		req, _, ok, err := ParseToolRequest("```tool\n" + raw + "\n```")
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("ParseToolRequest should repair raw tabs in JSON strings: %v", err))
		gomega.Expect(ok).To(gomega.BeTrue(), "expected tool request")
		gomega.Expect(req.Tool).To(gomega.Equal("edit_file"), fmt.Sprintf("unexpected request: %#v", req))
		gomega.Expect(req.Path).To(gomega.Equal("internal/labels/labels.go"), fmt.Sprintf("unexpected request: %#v", req))
		gomega.Expect(req.OldText).To(gomega.ContainSubstring("\treturn \"Save changes\""), fmt.Sprintf("expected repaired text fields to preserve tabs, old=%q new=%q", req.OldText, req.NewText))
		gomega.Expect(req.NewText).To(gomega.ContainSubstring("\treturn \"Save\""), fmt.Sprintf("expected repaired text fields to preserve tabs, old=%q new=%q", req.OldText, req.NewText))

	})
	ginkgo.It("parse tool request repairs malformed tool name object payload", func() {
		req, _, ok, err := ParseToolRequest(`{"tool":"code_navigate":{"capability":"read_symbol","path":"cmd/idiom/audit.go","pattern":"runAuditWatchMain"}}`)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("ParseToolRequest should repair malformed tool-name object: %v", err))
		gomega.Expect(ok).To(gomega.BeTrue(), "expected repaired tool request")
		gomega.Expect(req.Tool).To(gomega.Equal("code_navigate"), fmt.Sprintf("unexpected request: %#v", req))
		gomega.Expect(req.Capability).To(gomega.Equal("read_symbol"), fmt.Sprintf("unexpected request: %#v", req))
		gomega.Expect(req.Path).To(gomega.Equal("cmd/idiom/audit.go"), fmt.Sprintf("unexpected request: %#v", req))
		gomega.Expect(req.Pattern).To(gomega.Equal("runAuditWatchMain"), fmt.Sprintf("unexpected request: %#v", req))

	})
	ginkgo.It("parse tool request unwraps nested request payload", func() {
		req, _, ok, err := ParseToolRequest("```tool\n{\"tool\":\"code_navigate\",\"request\":\"{\\\"tool\\\":\\\"code_navigate\\\",\\\"capability\\\":\\\"read_symbol\\\",\\\"path\\\":\\\"cmd/idiom/main.go\\\",\\\"pattern\\\":\\\"Run\\\"}\"}\n```")
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("ParseToolRequest: %v", err))
		gomega.Expect(ok).To(gomega.BeTrue(), "expected tool request")
		gomega.Expect(req.Tool).To(gomega.Equal("code_navigate"), fmt.Sprintf("unexpected nested request repair: %#v", req))
		gomega.Expect(req.Capability).To(gomega.Equal("read_symbol"), fmt.Sprintf("unexpected nested request repair: %#v", req))
		gomega.Expect(req.Path).To(gomega.Equal("cmd/idiom/main.go"), fmt.Sprintf("unexpected nested request repair: %#v", req))
		gomega.Expect(req.Pattern).To(gomega.Equal("Run"), fmt.Sprintf("unexpected nested request repair: %#v", req))

	})
	ginkgo.It("parse tool request unwraps nested request object payload", func() {
		req, _, ok, err := ParseToolRequest("```tool\n{\"tool\":\"search_content\",\"request\":{\"pattern\":\"redacted\",\"path\":\"internal\"}}\n```")
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("ParseToolRequest: %v", err))
		gomega.Expect(ok).To(gomega.BeTrue(), "expected tool request")
		gomega.Expect(req.Tool).To(gomega.Equal("search_content"), fmt.Sprintf("unexpected nested object request repair: %#v", req))
		gomega.Expect(req.Pattern).To(gomega.Equal("redacted"), fmt.Sprintf("unexpected nested object request repair: %#v", req))
		gomega.Expect(req.Path).To(gomega.Equal("internal"), fmt.Sprintf("unexpected nested object request repair: %#v", req))

	})
	ginkgo.It("parse tool request nested code navigate graph resolution does not become capability", func() {
		req, _, ok, err := ParseToolRequest("```tool\n{\"tool\":\"code_navigate\",\"request\":{\"tool\":\"code_navigate\",\"query\":\"task tool implementation\",\"resolution\":\"symbol\",\"include_evidence\":\"true\"}}\n```")
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("ParseToolRequest: %v", err))
		gomega.Expect(ok).To(gomega.BeTrue(), "expected tool request")
		gomega.Expect(req.Tool).To(gomega.Equal("code_navigate"), fmt.Sprintf("unexpected nested object request repair: %#v", req))
		gomega.Expect(req.Query).To(gomega.Equal("task tool implementation"), fmt.Sprintf("unexpected nested object request repair: %#v", req))
		gomega.Expect(req.Resolution).To(gomega.Equal("symbol"), fmt.Sprintf("unexpected nested object request repair: %#v", req))
		gomega.Expect(req.IncludeEvidence).To(gomega.BeTrue(), fmt.Sprintf("unexpected nested object request repair: %#v", req))
		gomega.Expect(req.Capability).To(gomega.Equal(""), fmt.Sprintf("graph resolution must not be treated as code_navigate capability: %#v", req))

	})
	ginkgo.It("parse tool request rejects nested input tool mismatch", func() {
		_, _, _, err := ParseToolRequest("```tool\n{\"tool\":\"code_navigate\",\"input\":{\"tool\":\"read_symbol\",\"path\":\"internal/frontier/discovery.go\",\"pattern\":\"DiscoveryQuery\"}}\n```")
		gomega.Expect(err).To(gomega.HaveOccurred(), "expected nested tool mismatch to be rejected")
		gomega.Expect(err.Error()).To(gomega.ContainSubstring(`nested request tool "read_symbol" does not match envelope tool "code_navigate"`), fmt.Sprintf("unexpected error: %v", err))

	})
	ginkgo.It("code navigate file outline directory returns candidates", func() {
		dir := ginkgo.GinkgoT().TempDir()
		writeTestFile(filepath.Join(dir, "internal/surfaces/tui/app/lifecycle.go"), "package app\n")
		writeTestFile(filepath.Join(dir, "internal/surfaces/tui/app/rendering.go"), "package app\n")

		mgr := NewManager(config.Workspace{Root: dir, CWD: dir})

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:       "code_navigate",
			Capability: "file_outline",
			Path:       "internal/surfaces/tui/app",
		})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("directory outline should return candidate evidence, got error: %s", result.Error))
		gomega.Expect(result.Output).To(gomega.ContainSubstring("routed_tool: directory_candidates"), fmt.Sprintf("directory outline missing candidate files:\n%s", result.Output))
		gomega.Expect(result.Output).To(gomega.ContainSubstring("candidate_file: internal/surfaces/tui/app/lifecycle.go"), fmt.Sprintf("directory outline missing candidate files:\n%s", result.Output))
		gomega.Expect(result.Output).To(gomega.ContainSubstring("candidate_file: internal/surfaces/tui/app/rendering.go"), fmt.Sprintf("directory outline missing candidate files:\n%s", result.Output))

	})
	ginkgo.It("code navigate rejects graph resolution without navigation capability", func() {
		dir := ginkgo.GinkgoT().TempDir()
		writeTestFile(filepath.Join(dir, "go.mod"), "module example.com/test\n\ngo 1.22\n")
		writeTestFile(filepath.Join(dir, "internal/surfaces/tui/app/lifecycle.go"), "package app\nfunc Run() {}\n")

		mgr := NewManager(config.Workspace{Root: dir, CWD: dir})

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:            "code_navigate",
			Query:           "task tool implementation",
			Resolution:      "symbol",
			IncludeEvidence: true,
		})
		gomega.Expect(result.Error).NotTo(gomega.Equal(""), fmt.Sprintf("expected graph-resolution-shaped code_navigate request to be rejected, got %#v", result))
		gomega.Expect(result.Error).To(gomega.ContainSubstring("use context_query"), fmt.Sprintf("rejection should explain the contract mismatch, got %q", result.Error))
		gomega.Expect(result.Error).To(gomega.ContainSubstring("set capability"), fmt.Sprintf("rejection should explain the contract mismatch, got %q", result.Error))

	})
	ginkgo.It("parse tool request does not treat symbol as pattern alias", func() {
		req, _, ok, err := ParseToolRequest("```tool\n{\"tool\":\"go_read_symbol\",\"path\":\"internal/runtime/agent/runner.go\",\"symbol\":\"Runner.Run\"}\n```")
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("ParseToolRequest: %v", err))
		gomega.Expect(ok).To(gomega.BeTrue(), "expected tool request")
		gomega.Expect(req.Pattern).To(gomega.Equal(""), fmt.Sprintf("symbol must not populate pattern, got %q", req.Pattern))

	})
	ginkgo.It("parse tool request does not treat search query as pattern alias", func() {
		req, _, ok, err := ParseToolRequest("```tool\n{\"tool\":\"search_content\",\"path\":\"internal/workflows/productworkflow\",\"query\":\"lint audit authority\"}\n```")
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("ParseToolRequest: %v", err))
		gomega.Expect(ok).To(gomega.BeTrue(), "expected tool request")
		gomega.Expect(req.Tool).To(gomega.Equal("search_content"), fmt.Sprintf("tool = %q, want search_content", req.Tool))
		gomega.Expect(req.Pattern).To(gomega.BeEmpty(), fmt.Sprintf("query must remain query without populating pattern: %#v", req))
		gomega.Expect(req.Query).To(gomega.Equal("lint audit authority"), fmt.Sprintf("query must remain query without populating pattern: %#v", req))

	})
	ginkgo.It("parse tool request does not treat operation as capability alias", func() {
		req, _, ok, err := ParseToolRequest("```tool\n{\"tool\":\"code_navigate\",\"operation\":\"read_symbol\",\"path\":\"internal/workflows/productworkflow/coverage_gate.go\",\"symbol\":\"EvaluateCoverageGate\"}\n```")
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("ParseToolRequest: %v", err))
		gomega.Expect(ok).To(gomega.BeTrue(), "expected tool request")
		gomega.Expect(req.Tool).To(gomega.Equal("code_navigate"), fmt.Sprintf("unexpected request: %#v", req))
		gomega.Expect(req.Capability).To(gomega.BeEmpty(), fmt.Sprintf("unexpected request: %#v", req))
		gomega.Expect(req.Pattern).To(gomega.BeEmpty(), fmt.Sprintf("unexpected request: %#v", req))

	})
	ginkgo.It("parse tool request does not treat action as code navigate capability alias", func() {
		req, _, ok, err := ParseToolRequest("```tool\n{\"tool\":\"code_navigate\",\"action\":\"read_symbol\",\"extract_body\":true,\"path\":\"internal/workflows/productworkflow/coverage_gate.go\",\"symbol\":\"CoverageResult\"}\n```")
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("ParseToolRequest: %v", err))
		gomega.Expect(ok).To(gomega.BeTrue(), "expected tool request")
		gomega.Expect(req.Tool).To(gomega.Equal("code_navigate"), fmt.Sprintf("unexpected request: %#v", req))
		gomega.Expect(req.Capability).To(gomega.BeEmpty(), fmt.Sprintf("unexpected request: %#v", req))
		gomega.Expect(req.Pattern).To(gomega.BeEmpty(), fmt.Sprintf("unexpected request: %#v", req))

	})
	ginkgo.It("parse tool request does not treat resolution as code navigate capability alias", func() {
		req, _, ok, err := ParseToolRequest("```tool\n{\"tool\":\"code_navigate\",\"resolution\":\"definition\",\"path\":\"internal/workflows/productworkflow/coverage_gate.go\",\"symbol\":\"EvaluateCoverageGate\"}\n```")
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("ParseToolRequest: %v", err))
		gomega.Expect(ok).To(gomega.BeTrue(), "expected tool request")
		gomega.Expect(req.Tool).To(gomega.Equal("code_navigate"), fmt.Sprintf("unexpected request: %#v", req))
		gomega.Expect(req.Capability).To(gomega.BeEmpty(), fmt.Sprintf("unexpected request: %#v", req))
		gomega.Expect(req.Pattern).To(gomega.BeEmpty(), fmt.Sprintf("unexpected request: %#v", req))
		gomega.Expect(req.Resolution).To(gomega.Equal("definition"), fmt.Sprintf("unexpected request: %#v", req))

	})
	ginkgo.It("parse tool request does not treat graph resolution as code navigate capability", func() {
		req, _, ok, err := ParseToolRequest("```tool\n{\"tool\":\"code_navigate\",\"resolution\":\"symbol\",\"query\":\"task tool implementation\"}\n```")
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("ParseToolRequest: %v", err))
		gomega.Expect(ok).To(gomega.BeTrue(), "expected tool request")
		gomega.Expect(req.Tool).To(gomega.Equal("code_navigate"), fmt.Sprintf("unexpected request: %#v", req))
		gomega.Expect(req.Resolution).To(gomega.Equal("symbol"), fmt.Sprintf("unexpected request: %#v", req))
		gomega.Expect(req.Query).To(gomega.Equal("task tool implementation"), fmt.Sprintf("unexpected request: %#v", req))
		gomega.Expect(req.Capability).To(gomega.Equal(""), fmt.Sprintf("graph resolution must not become capability: %#v", req))

	})
	ginkgo.It("parse tool request accepts line anchored tool json object after prose", func() {
		req, raw, ok, err := ParseToolRequest("I need to inspect the source first.\n\ntool: read_file {\"path\":\"cmd/idiom/product_workflow.go\",\"start_line\":1,\"end_line\":80}")
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("ParseToolRequest: %v", err))
		gomega.Expect(ok).To(gomega.BeTrue(), "expected line-anchored tool request")
		gomega.Expect(req.Tool).To(gomega.Equal("read_file"), fmt.Sprintf("unexpected request: %#v", req))
		gomega.Expect(req.Path).To(gomega.Equal("cmd/idiom/product_workflow.go"), fmt.Sprintf("unexpected request: %#v", req))
		gomega.Expect(req.StartLine).To(gomega.BeNumerically("==", 1), fmt.Sprintf("unexpected request: %#v", req))
		gomega.Expect(req.EndLine).To(gomega.BeNumerically("==", 80), fmt.Sprintf("unexpected request: %#v", req))
		gomega.Expect(raw).To(gomega.ContainSubstring(`"tool":"read_file"`), fmt.Sprintf("expected normalized JSON raw payload, got %q", raw))

	})
	ginkgo.It("parse tool request accepts tagged tool code json object after prose", func() {
		req, _, ok, err := ParseToolRequest("I need to inspect the source.\n\n<tool_code>\n{\"tool\":\"read_file\",\"path\":\"internal/workflows/productworkflow/coverage_gate.go\"}\n</tool_code>")
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("ParseToolRequest: %v", err))
		gomega.Expect(ok).To(gomega.BeTrue(), "expected tagged tool_code request")
		gomega.Expect(req.Tool).To(gomega.Equal("read_file"), fmt.Sprintf("unexpected request: %#v", req))
		gomega.Expect(req.Path).To(gomega.Equal("internal/workflows/productworkflow/coverage_gate.go"), fmt.Sprintf("unexpected request: %#v", req))

	})
	ginkgo.It("parse tool request rejects mismatched line anchored tool json object", func() {
		_, _, ok, err := ParseToolRequest("tool: read_file {\"tool\":\"submit_plan\",\"path\":\"target.go\"}")
		gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("expected mismatched line-anchored tool request to surface a parse error, ok=%t err=%v", ok, err))
		gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("expected mismatched line-anchored tool request to surface a parse error, ok=%t err=%v", ok, err))

	})
	ginkgo.It("parse tool request accepts bare tool json object", func() {
		req, raw, ok, err := ParseToolRequest(`submit_plan

{
  "rationale": "Verification Checklist:\n- source_sanity failed for target.txt, so submit a corrective replacement.",
  "edits": [{"path":"target.txt","old_text":"old","new_text":"new","purpose":"repair target"}]
}`)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("ParseToolRequest: %v", err))
		gomega.Expect(ok).To(gomega.BeTrue(), "expected bare tool JSON request")
		gomega.Expect(req.Tool).To(gomega.Equal("submit_plan"), fmt.Sprintf("unexpected request: %#v", req))
		gomega.Expect(req.Rationale).NotTo(gomega.BeEmpty(), fmt.Sprintf("unexpected request: %#v", req))
		gomega.Expect(req.Edits).To(gomega.HaveLen(1), fmt.Sprintf("unexpected request: %#v", req))
		gomega.Expect(req.Edits[0].Path).To(gomega.Equal("target.txt"), fmt.Sprintf("unexpected request: %#v", req))
		gomega.Expect(raw).To(gomega.ContainSubstring(`"tool":"submit_plan"`), fmt.Sprintf("expected normalized raw payload to include tool, got %q", raw))

	})
	ginkgo.It("parse tool request rejects mismatched bare tool json object", func() {
		_, _, ok, err := ParseToolRequest("read_file\n{\"tool\":\"submit_plan\",\"path\":\"target.go\"}")
		gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("expected mismatched bare tool request to surface a parse error, ok=%t err=%v", ok, err))
		gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("expected mismatched bare tool request to surface a parse error, ok=%t err=%v", ok, err))

	})
	ginkgo.It("parse tool request accepts qwen xml function envelope", func() {
		req, raw, ok, err := ParseToolRequest(`<tool_call>
<function=submit_plan>
<parameter=rationale>
Add the required policy gate.
</parameter>
<parameter=edits>
[{"path":"scripts/affected_suites.py","start_line":"10","end_line":"12","content":"new","purpose":"update inventory"}]
</parameter>
</function>
</tool_call>`)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("ParseToolRequest: %v", err))
		gomega.Expect(ok).To(gomega.BeTrue(), "expected tool request")
		gomega.Expect(req.Tool).To(gomega.Equal("submit_plan"), fmt.Sprintf("unexpected request: %#v", req))
		gomega.Expect(req.Rationale).To(gomega.Equal("Add the required policy gate."), fmt.Sprintf("unexpected request: %#v", req))
		gomega.Expect(req.Edits).To(gomega.HaveLen(1), fmt.Sprintf("unexpected edits: %#v", req.Edits))
		gomega.Expect(req.Edits[0].Path).To(gomega.Equal("scripts/affected_suites.py"), fmt.Sprintf("unexpected edits: %#v", req.Edits))
		gomega.Expect(req.Edits[0].StartLine).To(gomega.BeNumerically("==", 10), fmt.Sprintf("unexpected edits: %#v", req.Edits))
		gomega.Expect(req.Edits[0].EndLine).To(gomega.BeNumerically("==", 12), fmt.Sprintf("unexpected edits: %#v", req.Edits))
		gomega.Expect(raw).To(gomega.ContainSubstring(`"tool":"submit_plan"`), fmt.Sprintf("expected normalized JSON raw payload, got %q", raw))

	})
	ginkgo.It("parse tool request repairs unclosed prefixed qwen xml tool", func() {
		req, raw, ok, err := ParseToolRequest(`<tool_call>
<tool_code_navigate>
<parameter=capability>
read_symbol
</parameter>
<parameter=path>
internal/workflows/productworkflow/coverage_gate.go
</parameter>
<parameter=pattern>
CoverageGate
</parameter>
</tool_call>`)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("ParseToolRequest: %v", err))
		gomega.Expect(ok).To(gomega.BeTrue(), "expected repaired prefixed XML tool request")
		gomega.Expect(req.Tool).To(gomega.Equal("code_navigate"), fmt.Sprintf("unexpected request: %#v", req))
		gomega.Expect(req.Capability).To(gomega.Equal("read_symbol"), fmt.Sprintf("unexpected request: %#v", req))
		gomega.Expect(req.Path).To(gomega.Equal("internal/workflows/productworkflow/coverage_gate.go"), fmt.Sprintf("unexpected request: %#v", req))
		gomega.Expect(req.Pattern).To(gomega.Equal("CoverageGate"), fmt.Sprintf("unexpected request: %#v", req))
		gomega.Expect(raw).To(gomega.ContainSubstring(`"tool":"code_navigate"`), fmt.Sprintf("expected normalized JSON raw payload, got %q", raw))

	})
	ginkgo.It("parse tool request repairs split qwen named tool tags", func() {
		req, raw, ok, err := ParseToolRequest(`I'll start by exploring the relevant source.

<tool_call>
<
tool_name>
read_file
</
tool_name>
<
arguments>
{"path":"internal/workflows/productworkflow/coverage_gate.go","start_line":1,"end_line":120}
</
arguments>
<
tool_name>
>`)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("ParseToolRequest: %v", err))
		gomega.Expect(ok).To(gomega.BeTrue(), "expected repaired split-tag tool request")
		gomega.Expect(req.Tool).To(gomega.Equal("read_file"), fmt.Sprintf("unexpected repaired request: %#v", req))
		gomega.Expect(req.Path).To(gomega.Equal("internal/workflows/productworkflow/coverage_gate.go"), fmt.Sprintf("unexpected repaired request: %#v", req))
		gomega.Expect(req.StartLine).To(gomega.BeNumerically("==", 1), fmt.Sprintf("unexpected repaired request: %#v", req))
		gomega.Expect(req.EndLine).To(gomega.BeNumerically("==", 120), fmt.Sprintf("unexpected repaired request: %#v", req))
		gomega.Expect(raw).To(gomega.ContainSubstring(`"tool":"read_file"`), fmt.Sprintf("expected normalized JSON raw payload, got %q", raw))
		gomega.Expect(raw).To(gomega.ContainSubstring(`"path":"internal/workflows/productworkflow/coverage_gate.go"`), fmt.Sprintf("expected normalized JSON raw payload, got %q", raw))

	})
	ginkgo.It("parse tool request recovers embedded qwen parameters in rationale", func() {
		req, _, ok, err := ParseToolRequest(`{"tool":"submit_plan","rationale":"Add the required policy gate.\n<parameter=edits>\n[{\"path\":\"target.txt\",\"old_text\":\"old\",\"new_text\":\"new\",\"purpose\":\"replace target\"}]\n</parameter>"}`)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("ParseToolRequest: %v", err))
		gomega.Expect(ok).To(gomega.BeTrue(), "expected tool request")
		gomega.Expect(req.Tool).To(gomega.Equal("submit_plan"), fmt.Sprintf("unexpected tool: %q", req.Tool))
		gomega.Expect(req.Rationale).To(gomega.Equal("Add the required policy gate."), fmt.Sprintf("rationale = %q", req.Rationale))
		gomega.Expect(req.Edits).To(gomega.HaveLen(1), fmt.Sprintf("embedded edits were not recovered: %#v", req.Edits))
		gomega.Expect(req.Edits[0].Path).To(gomega.Equal("target.txt"), fmt.Sprintf("embedded edits were not recovered: %#v", req.Edits))
		gomega.Expect(req.Edits[0].OldText).To(gomega.Equal("old"), fmt.Sprintf("embedded edits were not recovered: %#v", req.Edits))
		gomega.Expect(req.Edits[0].NewText).To(gomega.Equal("new"), fmt.Sprintf("embedded edits were not recovered: %#v", req.Edits))

	})
	ginkgo.It("parse tool request accepts qwen json tool call envelope", func() {
		req, raw, ok, err := ParseToolRequest(`<tool_call>
{"tool":"read_file","path":"README.md","start_line":1,"end_line":20}
</tool_call>`)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("ParseToolRequest: %v", err))
		gomega.Expect(ok).To(gomega.BeTrue(), "expected tool request")
		gomega.Expect(req.Tool).To(gomega.Equal("read_file"), fmt.Sprintf("unexpected request: %#v", req))
		gomega.Expect(req.Path).To(gomega.Equal("README.md"), fmt.Sprintf("unexpected request: %#v", req))
		gomega.Expect(req.StartLine).To(gomega.BeNumerically("==", 1), fmt.Sprintf("unexpected request: %#v", req))
		gomega.Expect(req.EndLine).To(gomega.BeNumerically("==", 20), fmt.Sprintf("unexpected request: %#v", req))
		gomega.Expect(raw).To(gomega.Equal(`{"tool":"read_file","path":"README.md","start_line":1,"end_line":20}`), fmt.Sprintf("unexpected raw payload: %q", raw))

	})
	ginkgo.It("parse tool request accepts unterminated qwen json tool call envelope", func() {
		req, raw, ok, err := ParseToolRequest(`<tool_call>
{"tool":"read_file","path":"README.md","start_line":1,"end_line":20}`)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("ParseToolRequest: %v", err))
		gomega.Expect(ok).To(gomega.BeTrue(), "expected tool request")
		gomega.Expect(req.Tool).To(gomega.Equal("read_file"), fmt.Sprintf("unexpected request: %#v", req))
		gomega.Expect(req.Path).To(gomega.Equal("README.md"), fmt.Sprintf("unexpected request: %#v", req))
		gomega.Expect(req.StartLine).To(gomega.BeNumerically("==", 1), fmt.Sprintf("unexpected request: %#v", req))
		gomega.Expect(req.EndLine).To(gomega.BeNumerically("==", 20), fmt.Sprintf("unexpected request: %#v", req))
		gomega.Expect(raw).To(gomega.Equal(`{"tool":"read_file","path":"README.md","start_line":1,"end_line":20}`), fmt.Sprintf("unexpected raw payload: %q", raw))

	})
	ginkgo.It("parse tool request accepts qwen json name arguments envelope", func() {
		req, raw, ok, err := ParseToolRequest(`<tool_call>
{"name":"submit_plan","arguments":{"rationale":"Update target.","edits":[{"path":"target.txt","old_text":"old\n","new_text":"new\n","purpose":"replace target"}]}}
</tool_call>`)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("ParseToolRequest: %v", err))
		gomega.Expect(ok).To(gomega.BeTrue(), "expected tool request")
		gomega.Expect(req.Tool).To(gomega.Equal("submit_plan"), fmt.Sprintf("unexpected request: %#v", req))
		gomega.Expect(req.Rationale).To(gomega.Equal("Update target."), fmt.Sprintf("unexpected request: %#v", req))
		gomega.Expect(req.Edits).To(gomega.HaveLen(1), fmt.Sprintf("unexpected edits: %#v", req.Edits))
		gomega.Expect(req.Edits[0].Path).To(gomega.Equal("target.txt"), fmt.Sprintf("unexpected edits: %#v", req.Edits))
		gomega.Expect(req.Edits[0].OldText).To(gomega.Equal("old\n"), fmt.Sprintf("unexpected edits: %#v", req.Edits))
		gomega.Expect(req.Edits[0].NewText).To(gomega.Equal("new\n"), fmt.Sprintf("unexpected edits: %#v", req.Edits))
		gomega.Expect(raw).To(gomega.ContainSubstring(`"tool":"submit_plan"`), fmt.Sprintf("expected normalized JSON raw payload, got %q", raw))

	})
	ginkgo.It("parse tool request accepts qwen prefixed tool tag", func() {
		req, raw, ok, err := ParseToolRequest(`I need one bounded symbol read.

<tool_code_navigate>
<parameter=path>
internal/workflows/productworkflow/coverage_gate.go
</parameter>
<parameter=capability>
read_symbol
</parameter>
<parameter=pattern>
EvaluateCoverageGate
</parameter>
</tool_code_navigate>`)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("ParseToolRequest: %v", err))
		gomega.Expect(ok).To(gomega.BeTrue(), "expected prefixed XML tool request")
		gomega.Expect(req.Tool).To(gomega.Equal("code_navigate"), fmt.Sprintf("unexpected request: %#v", req))
		gomega.Expect(req.Capability).To(gomega.Equal("read_symbol"), fmt.Sprintf("unexpected request: %#v", req))
		gomega.Expect(req.Path).To(gomega.Equal("internal/workflows/productworkflow/coverage_gate.go"), fmt.Sprintf("unexpected request: %#v", req))
		gomega.Expect(req.Pattern).To(gomega.Equal("EvaluateCoverageGate"), fmt.Sprintf("unexpected request: %#v", req))
		gomega.Expect(raw).To(gomega.ContainSubstring(`"tool":"code_navigate"`), fmt.Sprintf("expected normalized JSON raw payload, got %q", raw))
		gomega.Expect(raw).To(gomega.ContainSubstring(`"capability":"read_symbol"`), fmt.Sprintf("expected normalized JSON raw payload, got %q", raw))

	})
	ginkgo.It("parse tool request accepts qwen prefixed tool tag json arguments", func() {
		req, raw, ok, err := ParseToolRequest(`I'll start by exploring the relevant source files.

<tool_code_navigate>
{"language":"go","path":"internal/workflows/productworkflow/coverage_gate.go","capability":"read_symbol","pattern":"CoverageGate"}
</tool_code_navigate>`)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("ParseToolRequest: %v", err))
		gomega.Expect(ok).To(gomega.BeTrue(), "expected prefixed XML tool request with JSON arguments")
		gomega.Expect(req.Tool).To(gomega.Equal("code_navigate"), fmt.Sprintf("unexpected request: %#v", req))
		gomega.Expect(req.Capability).To(gomega.Equal("read_symbol"), fmt.Sprintf("unexpected request: %#v", req))
		gomega.Expect(req.Path).To(gomega.Equal("internal/workflows/productworkflow/coverage_gate.go"), fmt.Sprintf("unexpected request: %#v", req))
		gomega.Expect(req.Pattern).To(gomega.Equal("CoverageGate"), fmt.Sprintf("unexpected request: %#v", req))
		gomega.Expect(raw).To(gomega.ContainSubstring(`"tool":"code_navigate"`), fmt.Sprintf("expected normalized JSON raw payload, got %q", raw))
		gomega.Expect(raw).To(gomega.ContainSubstring(`"capability":"read_symbol"`), fmt.Sprintf("expected normalized JSON raw payload, got %q", raw))

	})
	ginkgo.It("parse tool request accepts qwen prefixed tool tag json arguments without capability", func() {
		req, raw, ok, err := ParseToolRequest(`I'll start by exploring the relevant source files.

<tool_code_navigate>
{"language":"go","path":"internal/workflows/productworkflow/coverage_gate.go","precision_tier":"syntax-index","pattern":"CoverageGate"}
</tool_code_navigate>`)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("ParseToolRequest: %v", err))
		gomega.Expect(ok).To(gomega.BeTrue(), "expected prefixed XML tool request with JSON arguments to remain a tool request")
		gomega.Expect(req.Tool).To(gomega.Equal("code_navigate"), fmt.Sprintf("unexpected request: %#v", req))
		gomega.Expect(req.Capability).To(gomega.BeEmpty(), fmt.Sprintf("unexpected request: %#v", req))
		gomega.Expect(req.Path).To(gomega.Equal("internal/workflows/productworkflow/coverage_gate.go"), fmt.Sprintf("unexpected request: %#v", req))
		gomega.Expect(req.Pattern).To(gomega.Equal("CoverageGate"), fmt.Sprintf("unexpected request: %#v", req))
		gomega.Expect(raw).To(gomega.ContainSubstring(`"tool":"code_navigate"`), fmt.Sprintf("expected normalized JSON raw payload, got %q", raw))

	})
	ginkgo.It("parse tool request rejects qwen prefixed tool tag json tool mismatch", func() {
		_, _, ok, err := ParseToolRequest(`<tool_code_navigate>
{"tool":"read_file","path":"internal/workflows/productworkflow/coverage_gate.go"}
</tool_code_navigate>`)
		gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("expected prefixed XML tool mismatch error, ok=%t err=%v", ok, err))
		gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("expected prefixed XML tool mismatch error, ok=%t err=%v", ok, err))
		gomega.Expect(err.Error()).To(gomega.ContainSubstring("does not match JSON tool"), fmt.Sprintf("expected prefixed XML tool mismatch error, ok=%t err=%v", ok, err))

	})
	ginkgo.It("parse tool request ignores prose prefixed tool reference", func() {
		_, _, ok, err := ParseToolRequest("Use `<tool_read_file>` only when emitting an actual tool envelope.")
		gomega.Expect(err).To(gomega.BeNil(), fmt.Sprintf("expected prose prefixed-tool reference to remain answer content, ok=%t err=%v", ok, err))
		gomega.Expect(ok).To(gomega.BeFalse(), fmt.Sprintf("expected prose prefixed-tool reference to remain answer content, ok=%t err=%v", ok, err))

	})
	ginkgo.It("parse tool request preserves angle text inside qwen parameters", func() {
		req, _, ok, err := ParseToolRequest(`<tool_call>
<function=submit_plan>
<parameter=rationale>
Replace comparison examples.
</parameter>
<parameter=edits>
[{"path":"target.go","old_text":"if a < name > args {\n\treturn old\n}","new_text":"if a < name > args {\n\treturn next\n}","purpose":"replace comparison branch"}]
</parameter>
</function>
</tool_call>`)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("ParseToolRequest: %v", err))
		gomega.Expect(ok).To(gomega.BeTrue(), "expected qwen XML submit_plan request")
		gomega.Expect(req.Tool).To(gomega.Equal("submit_plan"), fmt.Sprintf("unexpected request: %#v", req))
		gomega.Expect(req.Edits).To(gomega.HaveLen(1), fmt.Sprintf("unexpected request: %#v", req))
		gomega.Expect(req.Edits[0].OldText).To(gomega.Equal("if a < name > args {\n\treturn old\n}"), fmt.Sprintf("angle comparison text was not preserved: %#v", req.Edits[0]))
		gomega.Expect(req.Edits[0].NewText).To(gomega.Equal("if a < name > args {\n\treturn next\n}"), fmt.Sprintf("angle comparison text was not preserved: %#v", req.Edits[0]))

	})
	ginkgo.It("parse tool request ignores prose qwen xml function reference", func() {
		_, _, ok, err := ParseToolRequest("Use `<function=submit_plan>` only inside a tool envelope.")
		gomega.Expect(err).To(gomega.BeNil(), fmt.Sprintf("expected prose reference to remain a final answer, ok=%t err=%v", ok, err))
		gomega.Expect(ok).To(gomega.BeFalse(), fmt.Sprintf("expected prose reference to remain a final answer, ok=%t err=%v", ok, err))

	})
	ginkgo.It("parse tool request ignores prose protocol terms", func() {
		for _, text := range []string{
			"Chat Completions returns `tool_calls` when the assistant requests a tool.",
			"DSML is a protocol label, not a tool request by itself.",
			"The prior response was invoked from a child runner.",
		} {
			_, _, ok, err := ParseToolRequest(text)
			gomega.Expect(err).To(gomega.BeNil(), fmt.Sprintf("expected prose protocol term to remain a final answer, ok=%t err=%v text=%q", ok, err, text))
			gomega.Expect(ok).To(gomega.BeFalse(), fmt.Sprintf("expected prose protocol term to remain a final answer, ok=%t err=%v text=%q", ok, err, text))

		}
	})
	ginkgo.It("looks like xml tool call markup ignores prose protocol terms", func() {
		for _, text := range []string{
			"Chat Completions returns `tool_calls` when the assistant requests a tool.",
			"DSML is a protocol label, not a tool request by itself.",
			"Use `<function=submit_plan>` only inside a tool envelope.",
		} {
			gomega.Expect(LooksLikeXMLToolCallMarkup(text)).To(gomega.BeFalse(), fmt.Sprintf("prose protocol term should not look like XML tool markup: %q", text))

		}
	})
	ginkgo.It("parse tool request reports malformed qwen xml object parameter", func() {
		_, _, ok, err := ParseToolRequest(`<function=submit_plan>
<parameter=edits>
[{"path":"a.py","old_text":"def f():
    pass"}
</parameter>
</function>`)
		gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("expected malformed edits error, ok=%t err=%v", ok, err))
		gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("expected malformed edits error, ok=%t err=%v", ok, err))
		gomega.Expect(err.Error()).To(gomega.ContainSubstring("edits"), fmt.Sprintf("expected malformed edits error, ok=%t err=%v", ok, err))

	})
	ginkgo.It("parse tool request rejects malformed or missing tool", func() {
		_, _, ok, err := ParseToolRequest("no tool here")
		gomega.Expect(err).To(gomega.BeNil(), fmt.Sprintf("expected no request, ok=%t err=%v", ok, err))
		gomega.Expect(ok).To(gomega.BeFalse(), fmt.Sprintf("expected no request, ok=%t err=%v", ok, err))

		_, _, ok, err = ParseToolRequest("```tool\n{\"path\":\"README.md\"}\n```")
		gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("expected missing tool error, ok=%t err=%v", ok, err))
		gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("expected missing tool error, ok=%t err=%v", ok, err))
		gomega.Expect(err.Error()).To(gomega.ContainSubstring("missing tool"), fmt.Sprintf("expected missing tool error, ok=%t err=%v", ok, err))

	})
})
var _ = ginkgo.Describe("parse tool request rejects missing tool in tool transport", func() {

	cases := []struct {
		name string
		text string
	}{
		{
			name: "go outline path only",
			text: "```tool\n{\"path\":\"internal/surfaces/tui/app/analysis_agents.go\"}\n```",
		},
		{
			name: "move declaration tag",
			text: "```tool\n{\"path\":\"internal/surfaces/tui/app/app.go\",\"destination\":\"internal/surfaces/tui/app/rendering.go\",\"pattern\":\"tag:rendering\"}\n```",
		},
		{
			name: "shell command",
			text: "```tool\n{\"command\":\"go test ./internal/surfaces/tui/app\"}\n```",
		},
		{
			name: "child task",
			text: "```tool\n{\"agent\":\"explorer\",\"prompt\":\"inspect routing\"}\n```",
		},
		{
			name: "line edit",
			text: "```tool\n{\"path\":\"internal/surfaces/tui/app/app.go\",\"start_line\":10,\"end_line\":10,\"content\":\"replacement\"}\n```",
		},
	}

	for _, tc := range cases {
		ginkgo.It(tc.name, func() {
			_, _, ok, err := ParseToolRequest(tc.text)
			gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("expected missing-tool rejection, ok=%t err=%v", ok, err))
			gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("expected missing-tool rejection, ok=%t err=%v", ok, err))
			gomega.Expect(err.Error()).To(gomega.ContainSubstring("missing tool"), fmt.Sprintf("expected missing-tool rejection, ok=%t err=%v", ok, err))

		})
	}
})
var _ = ginkgo.Describe("tool request", func() {
	ginkgo.It("parse tool request repairs numeric string fields", func() {
		req, _, ok, err := ParseToolRequest("```tool\n{\"tool\":\"edit_file\",\"path\":\"README.md\",\"start_line\":\"10\",\"end_line\":\"12\",\"content\":\"updated\"}\n```")
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("ParseToolRequest: %v", err))
		gomega.Expect(ok).To(gomega.BeTrue(), "expected tool request")
		gomega.Expect(req.StartLine).To(gomega.BeNumerically("==", 10), fmt.Sprintf("line range = %d-%d, want 10-12", req.StartLine, req.EndLine))
		gomega.Expect(req.EndLine).To(gomega.BeNumerically("==", 12), fmt.Sprintf("line range = %d-%d, want 10-12", req.StartLine, req.EndLine))

	})
	ginkgo.It("parse tool request repairs stringified structured fields", func() {
		req, _, ok, err := ParseToolRequest("```tool\n{\"tool\":\"context_pack\",\"task\":\"plan tests\",\"required_tests\":\"[\\\"go test ./cmd/idiom\\\"]\"}\n```")
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("ParseToolRequest: %v", err))
		gomega.Expect(ok).To(gomega.BeTrue(), "expected tool request")
		gomega.Expect(req.Tool).To(gomega.Equal("context_pack"), fmt.Sprintf("unexpected request: %#v", req))
		gomega.Expect(req.RequiredTests).To(gomega.HaveLen(1), fmt.Sprintf("unexpected request: %#v", req))
		gomega.Expect(req.RequiredTests[0]).To(gomega.Equal("go test ./cmd/idiom"), fmt.Sprintf("unexpected repaired tests: %#v", req.RequiredTests))

	})
	ginkgo.It("decode tool request json repairs native stringified submit plan edits", func() {
		inner := `[{"path":"cmd/idiom/product_workflow.go","old_text":"func runProductTDD() {
	return 0
}","new_text":"func runProductTDD() {
	return 1
}","purpose":"replace the confirmed function body"}]`

		payload, err := json.Marshal(map[string]any{
			"rationale": "plan",
			"edits":     inner,
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal payload: %v", err))

		req, err := DecodeToolRequestJSON(payload)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("DecodeToolRequestJSON: %v", err))
		gomega.Expect(req.Edits).To(gomega.HaveLen(1), fmt.Sprintf("expected stringified submit_plan edits to repair into typed edits, got %#v", req.Edits))
		gomega.Expect(req.Edits[0].Path).To(gomega.Equal("cmd/idiom/product_workflow.go"), fmt.Sprintf("expected stringified submit_plan edits to repair into typed edits, got %#v", req.Edits))

	})
	ginkgo.It("decode tool request json repairs stringified submit plan edits with trailing prose", func() {
		inner := `[{"path":"cmd/idiom/product_workflow.go","start_line":10,"end_line":12,"content":"updated\n","purpose":"replace the confirmed line range"}]

This is only explanatory prose after the structured edit array.`

		payload, err := json.Marshal(map[string]any{
			"tool":      "submit_plan",
			"rationale": "plan",
			"edits":     inner,
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal payload: %v", err))

		req, err := DecodeToolRequestJSON(payload)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("DecodeToolRequestJSON: %v", err))
		gomega.Expect(req.Edits).To(gomega.HaveLen(1), fmt.Sprintf("expected trailing prose repair to preserve the edit array, got %#v", req.Edits))
		gomega.Expect(req.Edits[0].Path).To(gomega.Equal("cmd/idiom/product_workflow.go"), fmt.Sprintf("expected trailing prose repair to preserve the edit array, got %#v", req.Edits))
		gomega.Expect(req.Edits[0].Content).To(gomega.Equal("updated\n"), fmt.Sprintf("expected trailing prose repair to preserve the edit array, got %#v", req.Edits))

	})
	ginkgo.It("decode tool request json repairs stringified empty submit plan edits", func() {
		req, err := DecodeToolRequestJSON([]byte(`{"tool":"submit_plan","rationale":"empty plan","edits":"[]"}`))
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("DecodeToolRequestJSON should parse stringified empty edits and leave validation to submit_plan: %v", err))
		gomega.Expect(req.Tool).To(gomega.Equal("submit_plan"), fmt.Sprintf("unexpected tool after repair: %#v", req))
		gomega.Expect(req.Edits).To(gomega.BeEmpty(), fmt.Sprintf("expected empty repaired edits, got %#v", req.Edits))

	})
	ginkgo.It("decode tool request json repairs stringified empty structured arrays", func() {
		payload := []byte(`{"tool":"context_pack","task":"localize","evidence":"[]","required_tests":"[]","required_edit_paths":"[]","forbidden_paths":"[]","declarations":"[]","moves":"[]","todos":"[]","items":"[]"}`)

		req, err := DecodeToolRequestJSON(payload)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("DecodeToolRequestJSON should repair stringified empty arrays: %v", err))
		gomega.Expect(req.Tool).To(gomega.Equal("context_pack"), fmt.Sprintf("unexpected repaired request: %#v", req))
		gomega.Expect(req.Task).To(gomega.Equal("localize"), fmt.Sprintf("unexpected repaired request: %#v", req))
		gomega.Expect(req.Evidence).To(gomega.BeEmpty(), fmt.Sprintf("expected all stringified empty arrays to decode as empty typed fields: %#v", req))
		gomega.Expect(req.RequiredTests).To(gomega.BeEmpty(), fmt.Sprintf("expected all stringified empty arrays to decode as empty typed fields: %#v", req))
		gomega.Expect(req.RequiredEditPaths).To(gomega.BeEmpty(), fmt.Sprintf("expected all stringified empty arrays to decode as empty typed fields: %#v", req))
		gomega.Expect(req.ForbiddenPaths).To(gomega.BeEmpty(), fmt.Sprintf("expected all stringified empty arrays to decode as empty typed fields: %#v", req))
		gomega.Expect(req.Declarations).To(gomega.BeEmpty(), fmt.Sprintf("expected all stringified empty arrays to decode as empty typed fields: %#v", req))
		gomega.Expect(req.Moves).To(gomega.BeEmpty(), fmt.Sprintf("expected all stringified empty arrays to decode as empty typed fields: %#v", req))
		gomega.Expect(req.Todos).To(gomega.BeEmpty(), fmt.Sprintf("expected all stringified empty arrays to decode as empty typed fields: %#v", req))
		gomega.Expect(req.Items).To(gomega.BeEmpty(), fmt.Sprintf("expected all stringified empty arrays to decode as empty typed fields: %#v", req))

	})
	ginkgo.It("decode tool request json repairs object evidence for submit plan", func() {
		payload := []byte(`{"tool":"submit_plan","rationale":"plan","evidence":{"native/tests/test_quality_inventory_bindings.cpp":"read lines 1-180"},"edits":[{"path":"native/tests/test_quality_inventory_bindings.cpp","old_text":"old","new_text":"new","purpose":"replace test expectation"}]}`)

		req, err := DecodeToolRequestJSON(payload)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("DecodeToolRequestJSON should tolerate object evidence metadata: %v", err))
		gomega.Expect(req.Tool).To(gomega.Equal("submit_plan"), fmt.Sprintf("unexpected repaired submit_plan request: %#v", req))
		gomega.Expect(req.Edits).To(gomega.HaveLen(1), fmt.Sprintf("unexpected repaired submit_plan request: %#v", req))
		gomega.Expect(req.Evidence).To(gomega.HaveLen(1), fmt.Sprintf("object evidence should be preserved as compact evidence text, got %#v", req.Evidence))
		gomega.Expect(req.Evidence[0]).To(gomega.ContainSubstring("native/tests/test_quality_inventory_bindings.cpp"), fmt.Sprintf("object evidence should be preserved as compact evidence text, got %#v", req.Evidence))

	})
	ginkgo.It("decode tool request json repairs structured source satisfied paths", func() {
		payload := []byte(`{"tool":"submit_plan","rationale":"mixed plan","source_satisfied_paths":[{"path":"b.txt","evidence":"current source already final"},{"file":"c.txt"}],"edits":[{"path":"a.txt","old_text":"old","new_text":"new","purpose":"replace fixture"}]}`)

		req, err := DecodeToolRequestJSON(payload)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("DecodeToolRequestJSON should repair structured source_satisfied_paths: %v", err))
		gomega.Expect(req.Tool).To(gomega.Equal("submit_plan"), fmt.Sprintf("unexpected repaired submit_plan request: %#v", req))
		gomega.Expect(req.Edits).To(gomega.HaveLen(1), fmt.Sprintf("unexpected repaired submit_plan request: %#v", req))

		if got, want := req.SourceSatisfiedPaths, []string{"b.txt", "c.txt"}; !reflect.DeepEqual(got, want) {
			ginkgo.Fail(fmt.Sprintf("source_satisfied_paths = %#v, want %#v", got, want))
		}
	})
	ginkgo.It("decode tool request json repairs object source satisfied paths", func() {
		payload := []byte(`{"tool":"submit_plan","rationale":"mixed plan","source_satisfied_paths":{"b.txt":"current source already final","c.txt":"current source already final"},"edits":[{"path":"a.txt","old_text":"old","new_text":"new","purpose":"replace fixture"}]}`)

		req, err := DecodeToolRequestJSON(payload)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("DecodeToolRequestJSON should repair object source_satisfied_paths: %v", err))

		if got, want := req.SourceSatisfiedPaths, []string{"b.txt", "c.txt"}; !reflect.DeepEqual(got, want) {
			ginkgo.Fail(fmt.Sprintf("source_satisfied_paths = %#v, want %#v", got, want))
		}
	})
	ginkgo.It("decode tool request json rejects metadata only source satisfied path objects", func() {
		payload := []byte(`{"tool":"submit_plan","rationale":"mixed plan","source_satisfied_paths":[{"evidence":"current source already final"}],"edits":[{"path":"a.txt","old_text":"old","new_text":"new","purpose":"replace fixture"}]}`)
		_, err := DecodeToolRequestJSON(payload)
		gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("metadata-only source_satisfied_paths object should remain a decode error, got %v", err))
		gomega.Expect(err.Error()).To(gomega.ContainSubstring("source_satisfied_paths"), fmt.Sprintf("metadata-only source_satisfied_paths object should remain a decode error, got %v", err))

	})
	ginkgo.It("decode tool request json repairs stringified source satisfied paths with trailing prose", func() {
		inner := `["b.txt"]

These are already final.`

		payload, err := json.Marshal(map[string]any{
			"tool":                   "submit_plan",
			"rationale":              "mixed plan",
			"source_satisfied_paths": inner,
			"edits":                  []map[string]any{{"path": "a.txt", "old_text": "old", "new_text": "new", "purpose": "replace fixture"}},
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal payload: %v", err))

		req, err := DecodeToolRequestJSON(payload)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("DecodeToolRequestJSON should repair stringified source_satisfied_paths: %v", err))

		if got, want := req.SourceSatisfiedPaths, []string{"b.txt"}; !reflect.DeepEqual(got, want) {
			ginkgo.Fail(fmt.Sprintf("source_satisfied_paths = %#v, want %#v", got, want))
		}
	})
	ginkgo.It("decode tool request json rejects malformed stringified submit plan edits", func() {
		payload, err := json.Marshal(map[string]any{
			"rationale": "plan",
			"edits": `[{"path":"cmd/idiom/product_workflow.go","old_text":"fs := flag.NewFlagSet("idiom product tdd", flag.ContinueOnError)
","new_text":"fs := flag.NewFlagSet("idiom product tdd", flag.ContinueOnError)
// updated
","purpose":"replace the confirmed function body"}]`,
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal payload: %v", err))

		_, err = DecodeToolRequestJSON(payload)
		gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("expected malformed stringified edits to remain a decode error, got %v", err))
		gomega.Expect(err.Error()).To(gomega.ContainSubstring("ToolRequest.edits"), fmt.Sprintf("expected malformed stringified edits to remain a decode error, got %v", err))

	})
	ginkgo.It("decode tool request json rejects malformed stringified submit plan edits without dropping tail", func() {
		inner := `[{"path":"a.go","old_text":"func a() {
	return 1
}","new_text":"func a() {
	return 2
}","purpose":"replace a"},{"path":"b.go","old_text":"fmt.Println("badly escaped")
","new_text":"fmt.Println("still badly escaped")
","purpose":"replace b"}]`

		payload, err := json.Marshal(map[string]any{
			"tool":      "submit_plan",
			"rationale": "plan",
			"edits":     inner,
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal payload: %v", err))

		_, err = DecodeToolRequestJSON(payload)
		gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("expected malformed stringified edits to remain a decode error, got %v", err))
		gomega.Expect(err.Error()).To(gomega.ContainSubstring("ToolRequest.edits"), fmt.Sprintf("expected malformed stringified edits to remain a decode error, got %v", err))

	})
	ginkgo.It("parse tool request repairs stringified multiline structured fields", func() {
		inner := `["old line
second line"]`

		payload, err := json.Marshal(map[string]any{
			"tool":     "harness_feedback",
			"category": "bug",
			"evidence": inner,
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal payload: %v", err))

		req, _, ok, err := ParseToolRequest("```tool\n" + string(payload) + "\n```")
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("ParseToolRequest: %v", err))
		gomega.Expect(ok).To(gomega.BeTrue(), "expected tool request")
		gomega.Expect(req.Tool).To(gomega.Equal("harness_feedback"), fmt.Sprintf("unexpected request: %#v", req))
		gomega.Expect(req.Evidence).To(gomega.HaveLen(1), fmt.Sprintf("unexpected request: %#v", req))
		gomega.Expect(req.Evidence[0]).To(gomega.Equal("old line\nsecond line"), fmt.Sprintf("unexpected repaired evidence: %#v", req.Evidence))

	})
	ginkgo.It("parse tool request repairs bool string fields", func() {
		req, _, ok, err := ParseToolRequest("```tool\n{\"tool\":\"context_query\",\"query\":\"what calls Run\",\"resolution\":\"symbol\",\"include_evidence\":\"true\"}\n```")
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("ParseToolRequest: %v", err))
		gomega.Expect(ok).To(gomega.BeTrue(), "expected tool request")
		gomega.Expect(req.IncludeEvidence).To(gomega.BeTrue(), fmt.Sprintf("include_evidence was not repaired: %#v", req))

	})
	ginkgo.It("parse tool request repairs combined numeric and bool string fields", func() {
		req, _, ok, err := ParseToolRequest("```tool\n{\"tool\":\"read_file\",\"path\":\"a.py\",\"start_line\":\"10\",\"end_line\":\"12\",\"include_source\":\"true\"}\n```")
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("ParseToolRequest: %v", err))
		gomega.Expect(ok).To(gomega.BeTrue(), "expected tool request")
		gomega.Expect(req.Tool).To(gomega.Equal("read_file"), fmt.Sprintf("unexpected request: %#v", req))
		gomega.Expect(req.Path).To(gomega.Equal("a.py"), fmt.Sprintf("unexpected repaired request: %#v", req))
		gomega.Expect(req.StartLine).To(gomega.BeNumerically("==", 10), fmt.Sprintf("unexpected repaired request: %#v", req))
		gomega.Expect(req.EndLine).To(gomega.BeNumerically("==", 12), fmt.Sprintf("unexpected repaired request: %#v", req))
		gomega.Expect(req.IncludeSource).To(gomega.BeTrue(), fmt.Sprintf("unexpected repaired request: %#v", req))

	})
	ginkgo.It("parse tool request repairs singleton array fields", func() {
		req, _, ok, err := ParseToolRequest("```tool\n{\"tool\":\"submit_plan\",\"rationale\":\"plan\",\"required_edit_paths\":\"a.py\",\"edits\":{\"path\":\"a.py\",\"old_text\":\"old\",\"new_text\":\"new\",\"purpose\":\"update a\"}}\n```")
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("ParseToolRequest: %v", err))
		gomega.Expect(ok).To(gomega.BeTrue(), "expected tool request")
		gomega.Expect(req.RequiredEditPaths).To(gomega.HaveLen(1), fmt.Sprintf("unexpected required edit paths: %#v", req.RequiredEditPaths))
		gomega.Expect(req.RequiredEditPaths[0]).To(gomega.Equal("a.py"), fmt.Sprintf("unexpected required edit paths: %#v", req.RequiredEditPaths))
		gomega.Expect(req.Edits).To(gomega.HaveLen(1), fmt.Sprintf("unexpected singleton edit repair: %#v", req.Edits))
		gomega.Expect(req.Edits[0].Path).To(gomega.Equal("a.py"), fmt.Sprintf("unexpected singleton edit repair: %#v", req.Edits))

	})
	ginkgo.It("parse tool request repairs embedded stringified submit plan edits", func() {
		req, _, ok, err := ParseToolRequest("```tool\n{\"tool\":\"submit_plan\",\"rationale\":\"plan\",\"edits\":\"Here are the edits:\\n```json\\n[{\\\"path\\\":\\\"a.py\\\",\\\"old_text\\\":\\\"old\\\",\\\"new_text\\\":\\\"new\\\",\\\"purpose\\\":\\\"update a\\\"}]\\n```\"}\n```")
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("ParseToolRequest: %v", err))
		gomega.Expect(ok).To(gomega.BeTrue(), "expected tool request")
		gomega.Expect(req.Edits).To(gomega.HaveLen(1), fmt.Sprintf("expected embedded stringified edits to repair into typed edits, got %#v", req.Edits))
		gomega.Expect(req.Edits[0].Path).To(gomega.Equal("a.py"), fmt.Sprintf("expected embedded stringified edits to repair into typed edits, got %#v", req.Edits))

	})
	ginkgo.It("parse tool request rejects unsafe numeric string fields", func() {
		_, _, ok, err := ParseToolRequest("```tool\n{\"tool\":\"read_file\",\"path\":\"README.md\",\"start_line\":\"ten\"}\n```")
		gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("expected original decode error for unsafe numeric string, ok=%t err=%v", ok, err))
		gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("expected original decode error for unsafe numeric string, ok=%t err=%v", ok, err))
		gomega.Expect(err.Error()).To(gomega.ContainSubstring("cannot unmarshal string"), fmt.Sprintf("expected original decode error for unsafe numeric string, ok=%t err=%v", ok, err))

	})
	ginkgo.It("parse tool request rejects unsafe bool string fields", func() {
		_, _, ok, err := ParseToolRequest("```tool\n{\"tool\":\"context_query\",\"query\":\"what calls Run\",\"include_evidence\":\"maybe\"}\n```")
		gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("expected original decode error for unsafe bool string, ok=%t err=%v", ok, err))
		gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("expected original decode error for unsafe bool string, ok=%t err=%v", ok, err))
		gomega.Expect(err.Error()).To(gomega.ContainSubstring("cannot unmarshal string"), fmt.Sprintf("expected original decode error for unsafe bool string, ok=%t err=%v", ok, err))

	})
})

// TestParseToolRequestRejectsProseJSONInsideQwenPrefixedTags ensures that
// XML-tagged JSON that describes tool behaviour rather than invoking a tool
// is not misclassified as a tool request. Qwen in thinking mode may produce
// prose like <tool_note>{"key":"evidence","content":"found X"}</tool_note>
// when it is explaining what it would do, not actually calling note.
var _ = ginkgo.Describe("parse tool request rejects prose json inside qwen prefixed tags", func() {

	cases := []struct {
		name string
		text string
	}{
		{
			name: "note tool descriptive JSON",
			text: `<tool_note>
{"key":"evidence","content":"Found the runner gate in internal/runtime/agent/runner.go"}
</tool_note>`,
		},
		{
			name: "note tool descriptive JSON without tool fields",
			text: `<tool_note>
{"key":"plan-cache","content":"The context_pack cache uses content-addressed keys with workspace fingerprints"}
</tool_note>`,
		},
		{
			name: "prose inside arbitrary tool tag",
			text: `<tool_read_file>
{"status":"ok","summary":"done"}
</tool_read_file>`,
		},
		{
			name: "descriptive JSON with only generic fields",
			text: `<tool_context_pack>
{"description":"I need to find the runner source","confidence":"high"}
</tool_context_pack>`,
		},
	}
	for _, tc := range cases {
		ginkgo.It(tc.name, func() {
			_, _, ok, err := ParseToolRequest(tc.text)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("ParseToolRequest returned unexpected error: %v", err))
			gomega.Expect(ok).To(gomega.BeFalse(), fmt.Sprintf("expected prose JSON inside prefixed tool tags to remain answer content, got ok=true for: %s", tc.name))

		})
	}
})
var

// TestParseToolRequestAcceptsDeepSeekNativeJSONFormat verifies that the
// standard {"tool":"name",...} JSON format used by DeepSeek and other
// native-tool-call backends parses correctly after the Qwen XML guard.
_ = ginkgo.Describe("parse tool request accepts deep seek native json format", func() {

	cases := []struct {
		name string
		text string
		want string
	}{
		{
			name: "read_file bare JSON",
			text: `{"tool":"read_file","path":"README.md","start_line":1,"end_line":20}`,
			want: "read_file",
		},
		{
			name: "search_content bare JSON",
			text: `{"tool":"search_content","pattern":"ParseToolRequest","path":"internal/capabilities/workspace"}`,
			want: "search_content",
		},
		{
			name: "code_navigate bare JSON",
			text: `{"tool":"code_navigate","capability":"read_symbol","path":"internal/runtime/agent/runner.go","pattern":"Runner.Run"}`,
			want: "code_navigate",
		},
		{
			name: "context_pack bare JSON",
			text: `{"tool":"context_pack","task":"find the runner gate","mode":"plan","limit":8}`,
			want: "context_pack",
		},
		{
			name: "fenced tool JSON (markdown style)",
			text: "```tool\n" + `{"tool":"read_file","path":"internal/runtime/agent/runner.go"}` + "\n```",
			want: "read_file",
		},
		{
			name: "line-anchored tool JSON",
			text: "tool: read_file {\"path\":\"README.md\",\"start_line\":1,\"end_line\":80}",
			want: "read_file",
		},
	}
	for _, tc := range cases {
		ginkgo.It(tc.name, func() {
			req, _, ok, err := ParseToolRequest(tc.text)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("ParseToolRequest: %v", err))
			gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("expected DeepSeek-native tool request to be accepted: %s", tc.name))
			gomega.Expect(req.Tool).To(gomega.Equal(tc.want), fmt.Sprintf("tool = %q, want %q", req.Tool, tc.want))

		})
	}
})
var _ = ginkgo.Describe("tool request", func() {
	ginkgo.It("parse tool request accepts deep seek xml read tools", func() {
		text := `<｜tool_calls_begin｜><｜tool_call_begin｜><function name="read_file">
<parameter name="path">internal/runtime/agent/runner.go</parameter>
<parameter name="start_line">1</parameter>
<parameter name="end_line">20</parameter>
</function><｜tool_call_end｜><｜tool_calls_end｜>`

		req, _, ok, err := ParseToolRequest(text)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("ParseToolRequest: %v", err))
		gomega.Expect(ok).To(gomega.BeTrue(), "expected DeepSeek XML read_file request to parse")
		gomega.Expect(req.Tool).To(gomega.Equal("read_file"), fmt.Sprintf("unexpected request: %#v", req))
		gomega.Expect(req.Path).To(gomega.Equal("internal/runtime/agent/runner.go"), fmt.Sprintf("unexpected request: %#v", req))
		gomega.Expect(req.StartLine).To(gomega.BeNumerically("==", 1), fmt.Sprintf("unexpected request: %#v", req))
		gomega.Expect(req.EndLine).To(gomega.BeNumerically("==", 20), fmt.Sprintf("unexpected request: %#v", req))

	})
	ginkgo.It("parse tool request accepts open deep seek xml read tool", func() {
		text := `<n_tool_calls>
<n_invoke name="read_file">
<n_parameter name="path" string="true">internal/runtime/agent/runner.go</n_parameter>
</n_tool_calls>`

		req, raw, ok, err := ParseToolRequest(text)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("ParseToolRequest: %v", err))
		gomega.Expect(ok).To(gomega.BeTrue(), "expected open DeepSeek XML read_file request to parse")
		gomega.Expect(req.Tool).To(gomega.Equal("read_file"), fmt.Sprintf("unexpected request: %#v", req))
		gomega.Expect(req.Path).To(gomega.Equal("internal/runtime/agent/runner.go"), fmt.Sprintf("unexpected request: %#v", req))
		gomega.Expect(raw).To(gomega.ContainSubstring(`"tool":"read_file"`), fmt.Sprintf("expected canonical raw request with tool/path, got %s", raw))
		gomega.Expect(raw).To(gomega.ContainSubstring(`"path":"internal/runtime/agent/runner.go"`), fmt.Sprintf("expected canonical raw request with tool/path, got %s", raw))

	})
	ginkgo.It("parse tool request accepts open deep seek xml tool name for normal validation", func() {
		text := `<n_tool_calls>
<n_invoke name="search_content">
</n_tool_calls>`

		req, raw, ok, err := ParseToolRequest(text)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("ParseToolRequest: %v", err))
		gomega.Expect(ok).To(gomega.BeTrue(), "expected open DeepSeek XML search_content request to parse")
		gomega.Expect(req.Tool).To(gomega.Equal("search_content"), fmt.Sprintf("unexpected request: %#v", req))
		gomega.Expect(req.Pattern).To(gomega.BeEmpty(), fmt.Sprintf("unexpected request: %#v", req))
		gomega.Expect(req.Path).To(gomega.BeEmpty(), fmt.Sprintf("unexpected request: %#v", req))
		gomega.Expect(raw).To(gomega.Equal(`{"tool":"search_content"}`), fmt.Sprintf("expected canonical raw request with only tool name, got %s", raw))

	})
	ginkgo.It("parse tool request rejects deep seek xml write tools", func() {
		text := `<｜tool_calls_begin｜><｜tool_call_begin｜><function name="submit_plan">
<parameter name="rationale">submit edits</parameter>
</function><｜tool_call_end｜><｜tool_calls_end｜>`
		_, _, ok, err := ParseToolRequest(text)
		gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("expected DeepSeek XML write tool to be rejected, ok=%t err=%v", ok, err))
		gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("expected DeepSeek XML write tool to be rejected, ok=%t err=%v", ok, err))
		gomega.Expect(err.Error()).To(gomega.ContainSubstring("not a read/search/discovery tool"), fmt.Sprintf("unexpected error: %v", err))

	})
})

// TestParseToolRequestAcceptsAllQwenFormatsAfterGuard verifies that every
// Qwen tool-call format continues to parse correctly after the XML guard
// that rejects descriptive prose.
var _ = ginkgo.Describe("parse tool request accepts all qwen formats after guard", func() {

	cases := []struct {
		name string
		text string
		want string
	}{
		{
			name: "Qwen JSON envelope",
			text: `<tool_call>
{"tool":"read_file","path":"README.md","start_line":1,"end_line":20}
</tool_call>`,
			want: "read_file",
		},
		{
			name: "Qwen name+arguments envelope",
			text: `<tool_call>
{"name":"submit_plan","arguments":{"rationale":"plan","edits":[{"path":"a.go","old_text":"old","new_text":"new","purpose":"update"}]}}
</tool_call>`,
			want: "submit_plan",
		},
		{
			name: "Qwen function envelope",
			text: `<tool_call>
<function=submit_plan>
<parameter=rationale>Add the gate.</parameter>
<parameter=edits>[{"path":"a.go","old_text":"old","new_text":"new","purpose":"update"}]</parameter>
</function>
</tool_call>`,
			want: "submit_plan",
		},
		{
			name: "Qwen prefixed tag with parameters",
			text: `<tool_code_navigate>
<parameter=path>internal/surfaces/tui/app/lifecycle.go</parameter>
<parameter=capability>read_symbol</parameter>
<parameter=pattern>Lifecycle.Run</parameter>
</tool_code_navigate>`,
			want: "code_navigate",
		},
		{
			name: "Qwen prefixed tag with JSON arguments",
			text: `<tool_code_navigate>
{"path":"internal/workflows/productworkflow/coverage_gate.go","capability":"read_symbol","pattern":"CoverageGate"}
</tool_code_navigate>`,
			want: "code_navigate",
		},
		{
			name: "Qwen prefixed tag with JSON without capability",
			text: `<tool_code_navigate>
{"language":"go","path":"internal/workflows/productworkflow/coverage_gate.go","precision_tier":"syntax-index","pattern":"CoverageGate"}
</tool_code_navigate>`,
			want: "code_navigate",
		},
		{
			name: "Qwen unclosed prefixed tag",
			text: `<tool_call>
<tool_code_navigate>
<parameter=capability>read_symbol</parameter>
<parameter=path>internal/workflows/productworkflow/coverage_gate.go</parameter>
<parameter=pattern>CoverageGate</parameter>
</tool_call>`,
			want: "code_navigate",
		},
	}
	for _, tc := range cases {
		ginkgo.It(tc.name, func() {
			req, _, ok, err := ParseToolRequest(tc.text)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("ParseToolRequest: %v", err))
			gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("expected Qwen tool request to be accepted after guard: %s", tc.name))
			gomega.Expect(req.Tool).To(gomega.Equal(tc.want), fmt.Sprintf("tool = %q, want %q", req.Tool, tc.want))

		})
	}
})
var

// TestParseToolRequestRejectsProseWithToolPrefixNoJSON ensures that prose
// containing <tool_ references without a valid tool-call body is still
// treated as a final answer.
_ = ginkgo.Describe("tool request", func() {
	ginkgo.It("parse tool request rejects prose with tool prefix no json", func() {
		cases := []string{
			"Use `<tool_read_file>` only when emitting an actual tool envelope.",
			"After the <tool_code_navigate> step, inspect the results.",
			"Do not write <tool_note> tags in your final answer prose.",
		}
		for _, tc := range cases {
			_, _, ok, err := ParseToolRequest(tc)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("ParseToolRequest returned unexpected error for prose: %v (text=%q)", err, tc))
			gomega.Expect(ok).To(gomega.BeFalse(), fmt.Sprintf("expected prose with <tool_ references to remain answer content: %q", tc))

		}
	})
	ginkgo.It("explore shell classification and validation", func() {
		gomega.Expect(IsBroadInspectionRequest(ToolRequest{Tool: "shell", Command: "find . -maxdepth 2"})).To(gomega.BeTrue(), "expected find shell command to be broad inspection")
		gomega.Expect(IsNarrowingRequest(ToolRequest{Tool: "shell", Command: "rg TODO internal"})).To(gomega.BeTrue(), "expected rg shell command to be narrowing")

		if analysis := AnalyzeShellCommand("git status --short"); !analysis.ExploreAllowed {
			ginkgo.Fail(fmt.Sprintf("expected git status to be allowed: %#v", analysis))
		}

		for _, command := range []string{"git checkout main", "git diff --output=report.txt", "git diff --output report.txt", "python3 script.py", "rg TODO > out.txt", "cat $(pwd)"} {
			if analysis := AnalyzeShellCommand(command); analysis.ExploreAllowed {
				ginkgo.Fail(fmt.Sprintf("expected command %q to be rejected: %#v", command, analysis))
			}
		}
	})
	ginkgo.It("explore shell rejects write capable git diff before execution", func() {
		dir := ginkgo.GinkgoT().TempDir()
		for name, body := range map[string]string{
			"left.txt":  "left\n",
			"right.txt": "right\n",
		} {
			gomega.Expect(os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644)).To(gomega.Succeed())

		}

		mgr := NewManager(config.Workspace{
			Root:        dir,
			CWD:         dir,
			Shell:       "/bin/sh",
			EnableShell: true,
		})
		gomega.Expect(mgr.SetMode(ModeExplore)).To(gomega.Succeed())

		result := mgr.Execute(context.Background(), ToolRequest{Tool: ToolNameShell, Command: "git diff --no-index --output=report.txt left.txt right.txt"})
		gomega.Expect(result.Error).NotTo(gomega.BeEmpty(), fmt.Sprintf("expected explore shell rejection, got %#v", result))
		gomega.Expect(result.Error).To(gomega.ContainSubstring("explore mode only allows read-only inspection shell commands"), fmt.Sprintf("expected explore shell rejection, got %#v", result))

		if _, err := os.Stat(filepath.Join(dir, "report.txt")); !os.IsNotExist(err) {
			ginkgo.Fail(fmt.Sprintf("write-capable git diff should not create report.txt before rejection; stat err=%v", err))
		}
	})
	ginkgo.It("should clear broad inspection lock requires relevant successful narrowing", func() {
		mgr := NewManager(testWorkspaceConfig())
		gomega.Expect(mgr.ShouldClearBroadInspectionLock(ToolRequest{Tool: "search_content", Pattern: "route"}, ToolResult{Summary: "ok"})).To(gomega.BeTrue(), "search_content should clear broad inspection lock")
		gomega.Expect(mgr.ShouldClearBroadInspectionLock(ToolRequest{Tool: "list_files", Path: "internal", Recursive: true}, ToolResult{Summary: "ok"})).To(gomega.BeTrue(), "scoped list_files should clear broad inspection lock")
		gomega.Expect(mgr.ShouldClearBroadInspectionLock(ToolRequest{Tool: "search_content"}, ToolResult{Error: "failed"})).To(gomega.BeFalse(), "failed tool result should not clear broad inspection lock")
		gomega.Expect(mgr.ShouldClearBroadInspectionLock(ToolRequest{Tool: "list_files"}, ToolResult{Summary: "ok"})).To(gomega.BeFalse(), "broad inspection should not clear broad inspection lock")

	})
	ginkgo.It("structured narrowing uses shared registry predicate", func() {
		mgr := NewManager(testWorkspaceConfig())

		for _, lang := range semanticlanguage.LanguageRegistry() {
			for capability, tool := range lang.Tools {
				req := ToolRequest{Tool: tool}
				gomega.Expect(IsNarrowingRequest(req)).To(gomega.BeTrue(), fmt.Sprintf("%s tool %s for %s should be narrowing", lang.Name, tool, capability))
				gomega.Expect(mgr.ShouldClearBroadInspectionLock(req, ToolResult{Tool: tool, Summary: "ok"})).To(gomega.BeTrue(), fmt.Sprintf("%s tool %s for %s should clear broad inspection lock", lang.Name, tool, capability))

			}
		}

		for _, spec := range languagetools.ToolCatalog() {
			if !spec.Supplemental {
				continue
			}

			tool := spec.Tool
			req := ToolRequest{Tool: tool}
			gomega.Expect(IsNarrowingRequest(req)).To(gomega.BeTrue(), fmt.Sprintf("supplemental language tool %s should be narrowing", tool))
			gomega.Expect(mgr.ShouldClearBroadInspectionLock(req, ToolResult{Tool: tool, Summary: "ok"})).To(gomega.BeTrue(), fmt.Sprintf("supplemental language tool %s should clear broad inspection lock", tool))

		}

		for _, tool := range []string{"mcp:semantic_search", "recovery_inspect", "context_unregistered"} {
			req := ToolRequest{Tool: tool}
			gomega.Expect(IsNarrowingRequest(req)).To(gomega.BeFalse(), fmt.Sprintf("%s should not be narrowing", tool))
			gomega.Expect(mgr.ShouldClearBroadInspectionLock(req, ToolResult{Tool: tool, Summary: "ok"})).To(gomega.BeFalse(), fmt.Sprintf("%s should not clear broad inspection lock", tool))

		}
	})
	ginkgo.It("scoped list files is narrowing not broad inspection", func() {
		gomega.Expect(IsBroadInspectionRequest(ToolRequest{Tool: "list_files", Path: "."})).To(gomega.BeTrue(), "root list_files should be broad inspection")
		gomega.Expect(IsBroadInspectionRequest(ToolRequest{Tool: "list_files", Path: "internal", Recursive: true})).To(gomega.BeFalse(), "scoped list_files should not be broad inspection")
		gomega.Expect(IsNarrowingRequest(ToolRequest{Tool: "list_files", Path: "internal", Recursive: true})).To(gomega.BeTrue(), "scoped list_files should be a narrowing request")

	})
	ginkgo.It("runtime inspection request uses tool surface metadata", func() {
		for _, req := range []ToolRequest{
			{Tool: ToolNameContextQuery, Query: "callers of Run"},
			{Tool: ToolNameFileStats, Path: "internal"},
			{Tool: ToolNameTask, Agent: "explorer", Mode: "read_only"},
			{Tool: ToolNameSearchContent, Pattern: "Run", Path: "internal"},
			{Tool: ToolNameShell, Command: "rg Run internal"},
		} {
			gomega.Expect(IsRuntimeInspectionRequest(req)).To(gomega.BeTrue(), fmt.Sprintf("%s should be runtime inspection", req.Tool))

		}

		for _, req := range []ToolRequest{
			{Tool: ToolNameSourceSanity, Path: "internal/runtime/agent/runner.go"},
			{Tool: ToolNameHarnessFeedback},
			{Tool: ToolNameEditFile, Path: "internal/runtime/agent/runner.go"},
			{Tool: ToolNameSubmitPlan},
			{Tool: ToolNameGitDiff},
		} {
			gomega.Expect(IsRuntimeInspectionRequest(req)).To(gomega.BeFalse(), fmt.Sprintf("%s should not be runtime inspection", req.Tool))

		}
	})
	ginkgo.It("parse tool request accepts calling tool envelope", func() {
		// Exact format emitted by Qwen 3.6b-35b in session 20260530-052348.879
		text := `[Calling tool: search_content({"glob":"*.go","pattern":"type.*Evidence","named_gap":"exact definition of Evidence type/struct/interface","path":".","tool":"search_content","why_not_answering":"Without the Evidence type definition, I cannot describe how evidence is structured in the idiom harness."})]`

		req, raw, ok, err := ParseToolRequest(text)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("ParseToolRequest: %v", err))
		gomega.Expect(ok).To(gomega.BeTrue(), "expected tool request for [Calling tool: ...] format")
		gomega.Expect(req.Tool).To(gomega.Equal("search_content"), fmt.Sprintf("expected tool=search_content, got %q", req.Tool))
		gomega.Expect(req.Pattern).To(gomega.Equal("type.*Evidence"), fmt.Sprintf("expected pattern=type.*Evidence, got %q", req.Pattern))
		gomega.Expect(req.Path).To(gomega.Equal("."), fmt.Sprintf("expected path=., got %q", req.Path))
		gomega.Expect(req.NamedGap).To(gomega.Equal("exact definition of Evidence type/struct/interface"), fmt.Sprintf("expected named_gap to be preserved, got %q", req.NamedGap))
		gomega.Expect(strings.HasPrefix(raw, `{"`)).To(gomega.BeTrue(), fmt.Sprintf("expected JSON object payload, got %q", raw))

	})
	ginkgo.It("parse tool request accepts calling tool envelope lowercase", func() {
		text := `[calling tool: search_content({"glob":"*.go","pattern":"type.*Evidence","path":".","tool":"search_content"})]`

		req, _, ok, err := ParseToolRequest(text)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("ParseToolRequest: %v", err))
		gomega.Expect(ok).To(gomega.BeTrue(), "expected tool request for [calling tool: ...] format (lowercase)")
		gomega.Expect(req.Tool).To(gomega.Equal("search_content"), fmt.Sprintf("expected tool=search_content, got %q", req.Tool))

	})
	ginkgo.It("parse tool request rejects calling tool without json", func() {
		text := `[Calling tool: search_content not valid json]`
		_, _, ok, _ := ParseToolRequest(text)
		gomega.Expect(ok).To(gomega.BeFalse(), "expected rejection for [Calling tool: ...] without JSON object")

	})
	ginkgo.It("parse tool request rejects calling tool without tool key", func() {
		text := `[Calling tool: search_content({"pattern":"type.*Evidence","path":"."})]`
		_, _, ok, _ := ParseToolRequest(text)
		gomega.Expect(ok).To(gomega.BeFalse(), "expected rejection for [Calling tool: ...] without tool key in JSON")

	})
	ginkgo.It("parse tool request accepts calling tool envelope with whitespace", func() {
		text := `[Calling tool:   search_content ( {"glob":"*.go","pattern":"type.*Evidence","path":".","tool":"search_content"} ) ]`

		req, _, ok, err := ParseToolRequest(text)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("ParseToolRequest: %v", err))
		gomega.Expect(ok).To(gomega.BeTrue(), "expected tool request for [Calling tool: ...] with whitespace")
		gomega.Expect(req.Tool).To(gomega.Equal("search_content"), fmt.Sprintf("expected tool=search_content, got %q", req.Tool))

	})
	ginkgo.It("parse tool request rejects calling tool prose without json", func() {
		for _, text := range []string{
			`I am [Calling tool: the workshop about tooling] today`,
			`[Calling tool: read_file not a json object]`,
			`Use [Calling tool: read_file] to read the file`,
			`[Calling conventions: we use {"status":"ok"} for responses]`,
		} {
			_, _, ok, _ := ParseToolRequest(text)
			gomega.Expect(ok).To(gomega.BeFalse(), fmt.Sprintf("expected rejection for prose: %q", text))

		}
	})
})
