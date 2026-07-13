package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"idiom/internal/platform/config"
)

var _ = ginkgo.Describe("tool invocation", func() {
	ginkgo.It("configured tool surface invoke tool denies before execution and projects payload", func() {
		root := ginkgo.GinkgoT().TempDir()
		surface := &ConfiguredToolSurface{
			Manager: NewManager(config.Workspace{Root: root, CWD: root}),
			PolicyContext: ToolProfilePolicyContext{
				Workspace: config.Workspace{Root: root, CWD: root},
			},
		}
		caller, err := NewToolSidecarCaller("cli-test", "req-1", "trace-1")
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), err)

		var events []string
		var records []map[string]any
		outcome := surface.InvokeTool(context.Background(), ToolRequest{
			Tool:    ToolNameWriteFile,
			Path:    "blocked.txt",
			Content: "blocked",
		}, ToolInvocationOptions{
			Profile:             ToolProfileDiscovery,
			Agent:               "direct",
			Transport:           "cli",
			Caller:              caller,
			RawInputBytes:       17,
			RequestFields:       ToolTelemetryRequestValues,
			PolicyMode:          ToolInvocationPolicyWorkspaceProfile,
			IncludeTimeoutOwner: true,
			TimeoutMargin:       5 * time.Second,
			Telemetry: func(event string, fields map[string]any) {
				events = append(events, event)
				records = append(records, fields)
			},
		})
		gomega.Expect(outcome.Result.Code).To(gomega.Equal("profile_denied"), fmt.Sprintf("expected profile denial, got %#v", outcome.Result))

		if _, err := os.Stat(filepath.Join(root, "blocked.txt")); !os.IsNotExist(err) {
			ginkgo.Fail(fmt.Sprintf("profile-denied invocation should not write blocked.txt, stat err=%v", err))
		}
		gomega.Expect(outcome.Payload.TimeoutOwner).To(gomega.Equal(ToolInvocationTimeoutOwnerWorkspaceWithMargin), fmt.Sprintf("timeout owner = %q", outcome.Payload.TimeoutOwner))
		gomega.Expect(outcome.Payload.Sidecar.Error).NotTo(gomega.BeNil(), fmt.Sprintf("payload sidecar should classify profile denial, got %#v", outcome.Payload.Sidecar.Error))
		gomega.Expect(outcome.Payload.Sidecar.Error.Class).To(gomega.Equal("profile_denied"), fmt.Sprintf("payload sidecar should classify profile denial, got %#v", outcome.Payload.Sidecar.Error))
		gomega.Expect(events).To(gomega.HaveLen(2), fmt.Sprintf("events = %#v", events))
		gomega.Expect(events[0]).To(gomega.Equal("tool_request"), fmt.Sprintf("events = %#v", events))
		gomega.Expect(events[1]).To(gomega.Equal("tool_result"), fmt.Sprintf("events = %#v", events))
		gomega.Expect(records[0]["timeout_owner"]).To(gomega.Equal(ToolInvocationTimeoutOwnerWorkspaceWithMargin), fmt.Sprintf("telemetry did not carry timeout owner: %#v", records))
		gomega.Expect(records[1]["timeout_owner"]).To(gomega.Equal(ToolInvocationTimeoutOwnerWorkspaceWithMargin), fmt.Sprintf("telemetry did not carry timeout owner: %#v", records))

	})
	ginkgo.It("configured tool surface invoke tool applies exposure before profile", func() {
		root := ginkgo.GinkgoT().TempDir()
		surface := &ConfiguredToolSurface{
			Manager: NewManager(config.Workspace{Root: root, CWD: root}),
			PolicyContext: ToolProfilePolicyContext{
				Workspace: config.Workspace{Root: root, CWD: root},
			},
		}

		outcome := surface.InvokeTool(context.Background(), ToolRequest{Tool: ToolNameWriteFile}, ToolInvocationOptions{
			Profile:    ToolProfileDefault,
			Agent:      "mcp",
			Transport:  "mcp_stdio",
			PolicyMode: ToolInvocationPolicyProfile,
			Exposure: ToolInvocationExposure{
				ToolNames:    []string{ToolNameContextStatus},
				DeniedReason: "tool is not exposed by the schema-derived MCP discovery profile",
			},
		})
		gomega.Expect(outcome.Result.Code).To(gomega.Equal("profile_denied"), fmt.Sprintf("expected schema exposure denial, got %#v", outcome.Result))
		gomega.Expect(outcome.Result.Error).To(gomega.ContainSubstring("schema-derived MCP discovery profile"), fmt.Sprintf("expected schema exposure denial, got %#v", outcome.Result))

	})
	ginkgo.It("configured tool surface invoke tool filters result before payload and telemetry", func() {
		root := ginkgo.GinkgoT().TempDir()
		surface := &ConfiguredToolSurface{
			Manager: NewManager(config.Workspace{Root: root, CWD: root}),
			PolicyContext: ToolProfilePolicyContext{
				Workspace: config.Workspace{Root: root, CWD: root},
			},
		}

		var resultRecord map[string]any
		outcome := surface.InvokeTool(context.Background(), ToolRequest{Tool: ToolNamePwd}, ToolInvocationOptions{
			Profile:    ToolProfileDefault,
			Agent:      "mcp",
			Transport:  "mcp_stdio",
			PolicyMode: ToolInvocationPolicyProfile,
			ResultFilter: func(result ToolResult) ToolResult {
				result.Summary = "Printed <redacted_path>"
				result.Output = "<redacted_path>"

				return result
			},
			Telemetry: func(event string, fields map[string]any) {
				if event == "tool_result" {
					resultRecord = fields
				}
			},
		})
		gomega.Expect(outcome.Result.Error).To(gomega.Equal(""), fmt.Sprintf("expected executed pwd result, got %#v", outcome))

		structuredOutput, ok := outcome.Payload.Sidecar.StructuredOutput.(map[string]any)
		gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("sidecar structured output = %#v", outcome.Payload.Sidecar.StructuredOutput))
		gomega.Expect(outcome.Payload.Output).To(gomega.Equal("<redacted_path>"), fmt.Sprintf("payload should use filtered result, got %#v", outcome.Payload))
		gomega.Expect(structuredOutput["text"]).To(gomega.Equal("<redacted_path>"), fmt.Sprintf("payload should use filtered result, got %#v", outcome.Payload))
		gomega.Expect(resultRecord["summary"]).To(gomega.Equal("Printed <redacted_path>"), fmt.Sprintf("telemetry should use filtered summary, got %#v", resultRecord))

	})
	ginkgo.It("tool invocation context uses margin only when requested", func() {
		marginCtx, marginCancel := toolInvocationContext(context.Background(), ToolTimeoutForRequest(ToolRequest{Tool: ToolNameDiscoveryQuery}), 5*time.Second)
		defer marginCancel()

		deadline, ok := marginCtx.Deadline()
		gomega.Expect(ok).To(gomega.BeTrue(), "invocation context with margin should have a deadline")

		remaining := time.Until(deadline)
		gomega.Expect(remaining).NotTo(gomega.BeNumerically("<", 90*time.Second), fmt.Sprintf("deadline = %v, want workspace timeout plus CLI margin", remaining))
		gomega.Expect(remaining).NotTo(gomega.BeNumerically(">", 96*time.Second), fmt.Sprintf("deadline = %v, want workspace timeout plus CLI margin", remaining))

		managerOwnedCtx, managerOwnedCancel := toolInvocationContext(context.Background(), ToolTimeoutForRequest(ToolRequest{Tool: ToolNameDiscoveryQuery}), 0)
		defer managerOwnedCancel()
		if _, ok := managerOwnedCtx.Deadline(); ok {
			ginkgo.Fail("invocation context without margin should leave deadline ownership to Manager.Execute")
		}

		shellCtx, shellCancel := toolInvocationContext(context.Background(), ToolTimeoutForRequest(ToolRequest{Tool: ToolNameShell}), 5*time.Second)
		defer shellCancel()
		if _, ok := shellCtx.Deadline(); ok {
			ginkgo.Fail("shell invocation context should defer to the shell tool's internal timeout")
		}
	})
})
