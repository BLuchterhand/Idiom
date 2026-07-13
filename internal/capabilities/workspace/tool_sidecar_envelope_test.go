package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	contextpackcontract "idiom/internal/contracts/contextpack"
	"idiom/internal/platform/failures"
)

var _ = ginkgo.Describe("tool sidecar envelope", func() {
	ginkgo.It("build tool sidecar envelope includes structured freshness", func() {
		result := ToolResult{
			Tool:    "context_status",
			Summary: "Context index is fresh",
			Output:  `{"indexed":true,"freshness":{"policy":"report_only","operation":"context_status","fresh_for_scope":true},"warnings":["sample warning"],"audit_event_ids":["event-0"],"events":[{"event_id":"event-1"}]}`,
		}
		envelope := BuildToolSidecarEnvelopeWithOptions(ToolRequest{Tool: "context_status"}, result, "", ToolSidecarOptions{})
		gomega.Expect(envelope.SchemaVersion).To(gomega.Equal(ToolSidecarEnvelopeVersion), fmt.Sprintf("schema version = %q, want %q", envelope.SchemaVersion, ToolSidecarEnvelopeVersion))
		gomega.Expect(envelope.Tool).To(gomega.Equal("context_status"), fmt.Sprintf("unexpected tool/profile: %#v", envelope))
		gomega.Expect(envelope.Profile).To(gomega.Equal("default"), fmt.Sprintf("unexpected tool/profile: %#v", envelope))
		gomega.Expect(envelope.Contract.ID).To(gomega.Equal("idiom.tool.v1:context_status"), fmt.Sprintf("missing contract metadata/id: %#v", envelope.Contract))
		gomega.Expect(envelope.Contract.Metadata.Tags).NotTo(gomega.BeNil(), fmt.Sprintf("missing contract metadata/id: %#v", envelope.Contract))

		freshness, ok := envelope.Freshness.(map[string]any)
		gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("freshness should be structured, got %#v", envelope.Freshness))
		gomega.Expect(freshness["operation"]).To(gomega.Equal("context_status"), fmt.Sprintf("freshness should be structured, got %#v", envelope.Freshness))
		gomega.Expect(freshness["policy"]).To(gomega.Equal("report_only"), fmt.Sprintf("freshness should be structured, got %#v", envelope.Freshness))
		gomega.Expect(envelope.Warnings).To(gomega.HaveLen(1), fmt.Sprintf("warnings not extracted: %#v", envelope.Warnings))
		gomega.Expect(envelope.Warnings[0].Message).To(gomega.Equal("sample warning"), fmt.Sprintf("warnings not extracted: %#v", envelope.Warnings))
		gomega.Expect(envelope.AuditEventIDs).To(gomega.HaveLen(2), fmt.Sprintf("audit event ids not extracted: %#v", envelope.AuditEventIDs))
		gomega.Expect(envelope.AuditEventIDs[0]).To(gomega.Equal("event-0"), fmt.Sprintf("audit event ids not extracted: %#v", envelope.AuditEventIDs))
		gomega.Expect(envelope.AuditEventIDs[1]).To(gomega.Equal("event-1"), fmt.Sprintf("audit event ids not extracted: %#v", envelope.AuditEventIDs))

		if _, ok := envelope.StructuredOutput.(map[string]any); !ok {
			ginkgo.Fail(fmt.Sprintf("structured output should retain parsed JSON, got %#v", envelope.StructuredOutput))
		}
		gomega.Expect(envelope.Error).To(gomega.BeNil(), fmt.Sprintf("success envelope should not include error: %#v", envelope.Error))

	})
	ginkgo.It("build tool sidecar envelope uses shared contract shape", func() {
		result := ToolResult{
			Tool:    "context_status",
			Summary: "Context index is fresh",
			Output:  `{"indexed":true}`,
		}
		envelope := BuildToolSidecarEnvelopeWithOptions(
			ToolRequest{Tool: "context_status"},
			result,
			ToolProfileDiscovery,
			ToolSidecarOptions{Transport: "mcp_stdio"},
		)
		want := BuildToolSidecarContract("context_status", ToolProfileDiscovery, "mcp_stdio")
		gomega.Expect(reflect.DeepEqual(envelope.Contract, want)).To(gomega.BeTrue(), fmt.Sprintf("envelope contract drifted from shared contract shape:\n got %#v\nwant %#v", envelope.Contract, want))
		gomega.Expect(reflect.DeepEqual(envelope.Metadata, want.Metadata)).To(gomega.BeTrue(), fmt.Sprintf("envelope metadata should mirror shared contract metadata:\n got %#v\nwant %#v", envelope.Metadata, want.Metadata))

	})
	ginkgo.It("build tool sidecar envelope omits child context by default", func() {
		envelope := BuildToolSidecarEnvelopeWithOptions(
			ToolRequest{Tool: "context_pack"},
			ToolResult{Tool: "context_pack", Summary: "Context pack", Output: `{"files":[]}`},
			"default",
			ToolSidecarOptions{},
		)
		gomega.Expect(envelope.ChildContext).To(gomega.BeNil(), fmt.Sprintf("child context should be omitted by default: %#v", envelope.ChildContext))

		data, err := json.Marshal(envelope)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal envelope: %v", err))
		gomega.Expect(string(data)).NotTo(gomega.ContainSubstring("child_context"), fmt.Sprintf("default envelope should not serialize child_context: %s", data))

	})
	ginkgo.It("build tool sidecar envelope includes child context when supplied", func() {
		envelope := BuildToolSidecarEnvelopeWithOptions(
			ToolRequest{Tool: "context_pack"},
			ToolResult{Tool: "context_pack", Summary: "Context pack", Output: `{"files":[]}`},
			"discovery",
			ToolSidecarOptions{
				ChildContext: &ToolSidecarChildContext{
					SchemaVersion:    "legacy.child_context",
					ParentContractID: "parent-1",
					ChildContractID:  "child-1",
					InvocationID:     "inv-1",
					TerminalID:       "term-1",
					Role:             "child_read_only:explorer",
					Lane:             "implement",
					Phase:            "child_read_only",
					WorkspaceMode:    "no_write",
					PackID:           "pack-1",
				},
			},
		)
		gomega.Expect(envelope.ChildContext).NotTo(gomega.BeNil(), "expected child context")
		gomega.Expect(envelope.ChildContext.SchemaVersion).To(gomega.Equal(ToolSidecarChildContextVersion), fmt.Sprintf("child context schema version = %q", envelope.ChildContext.SchemaVersion))
		gomega.Expect(envelope.ChildContext.InvocationID).To(gomega.Equal("inv-1"), fmt.Sprintf("child context metadata not preserved: %#v", envelope.ChildContext))
		gomega.Expect(envelope.ChildContext.Role).To(gomega.Equal("child_read_only:explorer"), fmt.Sprintf("child context metadata not preserved: %#v", envelope.ChildContext))

		data, err := json.Marshal(envelope)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal envelope: %v", err))
		gomega.Expect(string(data)).To(gomega.ContainSubstring(`"child_context"`), fmt.Sprintf("serialized envelope missing child_context: %s", data))
		gomega.Expect(string(data)).To(gomega.ContainSubstring(ToolSidecarChildContextVersion), fmt.Sprintf("serialized envelope missing child_context: %s", data))

	})
	ginkgo.It("build tool sidecar envelope drops unsafe child context", func() {
		envelope := BuildToolSidecarEnvelopeWithOptions(
			ToolRequest{Tool: "context_pack"},
			ToolResult{Tool: "context_pack", Summary: "Context pack", Output: `{"files":[]}`},
			"discovery",
			ToolSidecarOptions{
				ChildContext: &ToolSidecarChildContext{
					ParentContractID: "/Users/alice/private/repo",
					InvocationID:     "inv-1",
				},
			},
		)
		gomega.Expect(envelope.ChildContext).To(gomega.BeNil(), fmt.Sprintf("unsafe child context should be omitted: %#v", envelope.ChildContext))

	})
	ginkgo.It("build tool sidecar envelope includes allowlisted caller metadata", func() {
		caller, err := NewToolSidecarCaller("codex-cli", "req-123", "trace:abc")
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("caller metadata should validate: %v", err))

		envelope := BuildToolSidecarEnvelopeWithOptions(
			ToolRequest{Tool: "pwd"},
			ToolResult{Tool: "pwd", Summary: "Workspace", Output: "/tmp/workspace"},
			"default",
			ToolSidecarOptions{Caller: caller},
		)
		gomega.Expect(envelope.Caller.CallerID).To(gomega.Equal("codex-cli"), fmt.Sprintf("caller metadata not carried through: %#v", envelope.Caller))
		gomega.Expect(envelope.Caller.RequestID).To(gomega.Equal("req-123"), fmt.Sprintf("caller metadata not carried through: %#v", envelope.Caller))
		gomega.Expect(envelope.Caller.TraceID).To(gomega.Equal("trace:abc"), fmt.Sprintf("caller metadata not carried through: %#v", envelope.Caller))
		gomega.Expect(envelope.Contract.Transport).To(gomega.Equal("cli"), fmt.Sprintf("default sidecar transport should remain cli, got %#v", envelope.Contract))

	})
	ginkgo.It("build tool sidecar envelope can attribute mcp stdio transport", func() {
		caller, err := NewToolSidecarCaller("mcp-client", "req-456", "trace:mcp")
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("caller metadata should validate: %v", err))

		envelope := BuildToolSidecarEnvelopeWithOptions(
			ToolRequest{Tool: "context_status"},
			ToolResult{Tool: "context_status", Summary: "Context status", Output: `{"freshness":{"policy":"report_only"}}`},
			"discovery",
			ToolSidecarOptions{Caller: caller, Transport: "mcp_stdio"},
		)
		gomega.Expect(envelope.Caller.CallerID).To(gomega.Equal("mcp-client"), fmt.Sprintf("MCP caller metadata not carried through: %#v", envelope.Caller))
		gomega.Expect(envelope.Caller.RequestID).To(gomega.Equal("req-456"), fmt.Sprintf("MCP caller metadata not carried through: %#v", envelope.Caller))
		gomega.Expect(envelope.Caller.TraceID).To(gomega.Equal("trace:mcp"), fmt.Sprintf("MCP caller metadata not carried through: %#v", envelope.Caller))
		gomega.Expect(envelope.Contract.Transport).To(gomega.Equal("mcp_stdio"), fmt.Sprintf("MCP sidecar transport should be mcp_stdio, got %#v", envelope.Contract))

	})
})
var _ = ginkgo.Describe("new tool sidecar caller rejects leaky values", func() {

	jwtLike := strings.Join([]string{
		"eyJhbGciOiJIUzI1NiJ9",
		"eyJzdWIiOiIxMjM0In0",
		"sflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c",
	}, ".")

	for _, tc := range []struct {
		name      string
		callerID  string
		requestID string
		traceID   string
	}{
		{name: "private path", callerID: "/home/alice/private/repo"},
		{name: "home path", callerID: "~/private/repo"},
		{name: "windows path", callerID: `C:\Users\alice\repo`},
		{name: "ssh key path", callerID: ".ssh/id_rsa"},
		{name: "email address", callerID: "alice@example.com"},
		{name: "openai key prefix", callerID: "sk-proj-abc123"},
		{name: "openai underscore prefix", callerID: "sk_abc123"},
		{name: "github token prefix", callerID: "ghp_abc123"},
		{name: "github pat prefix", callerID: "github_pat_abc123"},
		{name: "slack token prefix", callerID: "xoxb-abc123"},
		{name: "jwt shaped", callerID: jwtLike},
		{name: "long hex blob", callerID: "0123456789abcdef0123456789abcdef"},
		{name: "long base64 blob", callerID: "dGhpc2lzYXZlcnlsb25nYmFzZTY0bGlrZXZhbHVldGhhdGxvb2tzbGlrZWFzZWNyZXQ"},
		{name: "secret word", requestID: "prod-token"},
		{name: "non ascii", traceID: "trace-π"},
	} {
		ginkgo.It(tc.name, func() {
			if _, err := NewToolSidecarCaller(tc.callerID, tc.requestID, tc.traceID); err == nil {
				ginkgo.Fail(fmt.Sprintf("expected leaky caller metadata to be rejected"))
			}
		})
	}
})
var _ = ginkgo.Describe("tool sidecar envelope", func() {
	ginkgo.It("build tool sidecar envelope extracts stale freshness without graph state", func() {
		result := ToolResult{
			Tool:    "context_query",
			Summary: "Context query returned stale report-only evidence",
			Output:  `{"freshness":{"policy":"allow_stale","operation":"context_query","fresh_for_scope":false,"global_fresh":false,"stale_path_count":2,"missing_path_count":1},"nodes":[],"edges":[]}`,
		}
		envelope := BuildToolSidecarEnvelopeWithOptions(ToolRequest{Tool: "context_query"}, result, "discovery", ToolSidecarOptions{})
		freshness, ok := envelope.Freshness.(map[string]any)
		gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("freshness should be structured, got %#v", envelope.Freshness))
		gomega.Expect(freshness["policy"]).To(gomega.Equal("allow_stale"), fmt.Sprintf("unexpected freshness identity: %#v", freshness))
		gomega.Expect(freshness["operation"]).To(gomega.Equal("context_query"), fmt.Sprintf("unexpected freshness identity: %#v", freshness))
		gomega.Expect(freshness["fresh_for_scope"]).To(gomega.BeFalse(), fmt.Sprintf("stale freshness booleans not preserved: %#v", freshness))
		gomega.Expect(freshness["global_fresh"]).To(gomega.BeFalse(), fmt.Sprintf("stale freshness booleans not preserved: %#v", freshness))
		gomega.Expect(freshness["stale_path_count"]).To(gomega.Equal(float64(2)), fmt.Sprintf("stale counts not preserved: %#v", freshness))
		gomega.Expect(freshness["missing_path_count"]).To(gomega.Equal(float64(1)), fmt.Sprintf("stale counts not preserved: %#v", freshness))

	})
	ginkgo.It("build tool sidecar envelope preserves text output", func() {
		output := "  first line\nsecond line\n"
		envelope := BuildToolSidecarEnvelopeWithOptions(ToolRequest{Tool: "read_file"}, ToolResult{
			Tool:    "read_file",
			Summary: "Read file",
			Output:  output,
		}, "default", ToolSidecarOptions{})
		structured, ok := envelope.StructuredOutput.(map[string]any)
		gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("expected wrapped text output, got %#v", envelope.StructuredOutput))
		gomega.Expect(structured["format"]).To(gomega.Equal("text"), fmt.Sprintf("text output should be preserved exactly, got %#v", structured))
		gomega.Expect(structured["text"]).To(gomega.Equal(output), fmt.Sprintf("text output should be preserved exactly, got %#v", structured))
		gomega.Expect(envelope.Freshness).To(gomega.BeNil(), fmt.Sprintf("text output should not be scraped for freshness: %#v", envelope.Freshness))

	})
	ginkgo.It("build tool sidecar envelope uses typed failure for unsupported tool", func() {
		result := ToolResult{
			Tool:    "not_a_tool",
			Summary: "Tool request rejected",
			Error:   `unsupported tool "not_a_tool"`,
			Failure: failures.New(failures.Unimplemented, reasonToolUnsupported, `unsupported tool "not_a_tool"`),
		}
		envelope := BuildToolSidecarEnvelopeWithOptions(ToolRequest{Tool: "not_a_tool"}, result, "default", ToolSidecarOptions{})
		gomega.Expect(envelope.SchemaVersion).To(gomega.Equal(ToolSidecarEnvelopeVersion), fmt.Sprintf("schema version = %q, want %q", envelope.SchemaVersion, ToolSidecarEnvelopeVersion))
		gomega.Expect(envelope.Error).NotTo(gomega.BeNil(), fmt.Sprintf("expected structured error"))
		gomega.Expect(envelope.Error.Class).To(gomega.Equal("unsupported_tool"), fmt.Sprintf("unexpected error classification: %#v", envelope.Error))
		gomega.Expect(envelope.Error.Code).To(gomega.Equal("unsupported_tool"), fmt.Sprintf("unexpected error classification: %#v", envelope.Error))
		gomega.Expect(envelope.Error.Message).To(gomega.Equal(result.Error), fmt.Sprintf("error message changed: %#v", envelope.Error))

	})
	ginkgo.It("build tool sidecar envelope prefers typed failure metadata", func() {
		result := ToolResult{
			Tool:    "context_query",
			Summary: "Context query failed",
			Error:   "invalid input: pattern is required",
			Failure: failures.New(failures.PermissionDenied, failures.NoReason, "permission denied by policy"),
		}
		envelope := BuildToolSidecarEnvelopeWithOptions(ToolRequest{Tool: "context_query"}, result, "default", ToolSidecarOptions{})
		gomega.Expect(envelope.Error).NotTo(gomega.BeNil(), fmt.Sprintf("expected structured error"))
		gomega.Expect(envelope.Error.Class).To(gomega.Equal("permission_policy"), fmt.Sprintf("typed failure should override message classification, got %#v", envelope.Error))
		gomega.Expect(envelope.Error.Code).To(gomega.Equal("permission_policy"), fmt.Sprintf("typed failure should override message classification, got %#v", envelope.Error))
		gomega.Expect(envelope.Error.Message).To(gomega.Equal(result.Error), fmt.Sprintf("error message should preserve legacy text, got %#v", envelope.Error))

	})
	ginkgo.It("build tool sidecar envelope preserves structured result code", func() {
		result := ToolResult{
			Tool:    "context_status",
			Code:    "profile_denied",
			Summary: "Tool request denied by discovery profile",
			Error:   "profile_denied: read_only_profile: refresh requests are not allowed",
			Failure: failures.New(failures.PermissionDenied, failures.NoReason, "refresh requests are not allowed"),
		}
		envelope := BuildToolSidecarEnvelopeWithOptions(ToolRequest{Tool: "context_status"}, result, "discovery", ToolSidecarOptions{})
		gomega.Expect(envelope.Error).NotTo(gomega.BeNil(), fmt.Sprintf("expected structured error"))
		gomega.Expect(envelope.Error.Class).To(gomega.Equal("profile_denied"), fmt.Sprintf("structured result code should override generic typed failure, got %#v", envelope.Error))
		gomega.Expect(envelope.Error.Code).To(gomega.Equal("profile_denied"), fmt.Sprintf("structured result code should override generic typed failure, got %#v", envelope.Error))

	})
	ginkgo.It("build tool sidecar envelope uses generic class for untyped error text", func() {
		result := ToolResult{
			Tool:    "context_query",
			Summary: "Context query rejected",
			Error:   "invalid input: pattern is required",
		}
		envelope := BuildToolSidecarEnvelopeWithOptions(ToolRequest{Tool: "context_query"}, result, "default", ToolSidecarOptions{})
		gomega.Expect(envelope.Error).NotTo(gomega.BeNil(), fmt.Sprintf("expected structured error"))
		gomega.Expect(envelope.Error.Class).To(gomega.Equal("tool_error"), fmt.Sprintf("untyped error text should not become semantic authority, got %#v", envelope.Error))
		gomega.Expect(envelope.Error.Code).To(gomega.Equal("tool_error"), fmt.Sprintf("untyped error text should not become semantic authority, got %#v", envelope.Error))

	})
	ginkgo.It("build tool sidecar envelope renders typed failure without legacy error", func() {
		result := ToolResult{
			Tool:    "context_query",
			Summary: "Context query failed",
			Failure: failures.New(failures.DeadlineExceeded, failures.NoReason, "context_query exceeded its deadline"),
		}
		envelope := BuildToolSidecarEnvelopeWithOptions(ToolRequest{Tool: "context_query"}, result, "default", ToolSidecarOptions{})
		gomega.Expect(envelope.Error).NotTo(gomega.BeNil(), fmt.Sprintf("expected structured error"))
		gomega.Expect(envelope.Error.Class).To(gomega.Equal("timeout"), fmt.Sprintf("typed failure should classify timeout, got %#v", envelope.Error))
		gomega.Expect(envelope.Error.Code).To(gomega.Equal("timeout"), fmt.Sprintf("typed failure should classify timeout, got %#v", envelope.Error))
		gomega.Expect(envelope.Error.Message).To(gomega.ContainSubstring("context_query exceeded its deadline"), fmt.Sprintf("typed failure message should be rendered when legacy Error is empty, got %#v", envelope.Error))

	})
	ginkgo.It("build tool sidecar envelope includes context pack fallback structured output", func() {
		result := contextPackFallbackResult(ToolRequest{Tool: ToolNameContextPack, Task: "locate failure"}, 30*time.Millisecond, context.DeadlineExceeded)
		envelope := BuildToolSidecarEnvelopeWithOptions(ToolRequest{Tool: ToolNameContextPack}, result, "discovery", ToolSidecarOptions{})
		gomega.Expect(envelope.Error).NotTo(gomega.BeNil(), fmt.Sprintf("context_pack fallback should remain a typed timeout, got %#v", envelope.Error))
		gomega.Expect(envelope.Error.Class).To(gomega.Equal("timeout"), fmt.Sprintf("context_pack fallback should remain a typed timeout, got %#v", envelope.Error))
		gomega.Expect(envelope.Error.Code).To(gomega.Equal("timeout"), fmt.Sprintf("context_pack fallback should remain a typed timeout, got %#v", envelope.Error))

		structured, ok := envelope.StructuredOutput.(map[string]any)
		gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("context_pack fallback should be structured JSON output, got %#v", envelope.StructuredOutput))
		gomega.Expect(structured["schema"]).To(gomega.Equal(contextpackcontract.FallbackSchema), fmt.Sprintf("unexpected structured fallback payload: %#v", structured))
		gomega.Expect(structured["status"]).To(gomega.Equal("unavailable"), fmt.Sprintf("unexpected structured fallback payload: %#v", structured))
		gomega.Expect(structured["retry_same_request"]).To(gomega.BeFalse(), fmt.Sprintf("unexpected structured fallback payload: %#v", structured))

	})
	ginkgo.It("build tool sidecar envelope bounds generic structured error", func() {
		longMessage := ""
		for len(longMessage) <= maxToolSidecarErrorMessageBytes+50 {
			longMessage += "tool failed with repeated diagnostic detail "
		}

		envelope := BuildToolSidecarEnvelopeWithOptions(ToolRequest{Tool: "context_query"}, ToolResult{
			Tool:    "context_query",
			Summary: "Context query failed",
			Error:   longMessage,
		}, "default", ToolSidecarOptions{})
		gomega.Expect(envelope.Error).NotTo(gomega.BeNil(), fmt.Sprintf("expected structured error"))
		gomega.Expect(envelope.Error.Class).To(gomega.Equal("tool_error"), fmt.Sprintf("unexpected generic error classification: %#v", envelope.Error))
		gomega.Expect(envelope.Error.Code).To(gomega.Equal("tool_error"), fmt.Sprintf("unexpected generic error classification: %#v", envelope.Error))
		gomega.Expect(len(envelope.Error.Message) > maxToolSidecarErrorMessageBytes+len("...(truncated)")).To(gomega.BeFalse(), fmt.Sprintf("structured error message was not bounded: len=%d", len(envelope.Error.Message)))

	})
})
