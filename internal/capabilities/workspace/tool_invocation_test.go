package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"idiom/internal/platform/config"
)

func TestConfiguredToolSurfaceInvokeToolDeniesBeforeExecutionAndProjectsPayload(t *testing.T) {
	root := t.TempDir()
	surface := &ConfiguredToolSurface{
		Manager: NewManager(config.Workspace{Root: root, CWD: root}),
		PolicyContext: ToolProfilePolicyContext{
			Workspace: config.Workspace{Root: root, CWD: root},
		},
	}
	caller, err := NewToolSidecarCaller("cli-test", "req-1", "trace-1")
	if err != nil {
		t.Fatal(err)
	}

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

	if outcome.Result.Code != "profile_denied" {
		t.Fatalf("expected profile denial, got %#v", outcome.Result)
	}
	if _, err := os.Stat(filepath.Join(root, "blocked.txt")); !os.IsNotExist(err) {
		t.Fatalf("profile-denied invocation should not write blocked.txt, stat err=%v", err)
	}
	if outcome.Payload.TimeoutOwner != ToolInvocationTimeoutOwnerWorkspaceWithMargin {
		t.Fatalf("timeout owner = %q", outcome.Payload.TimeoutOwner)
	}
	if outcome.Payload.Sidecar.Error == nil || outcome.Payload.Sidecar.Error.Class != "profile_denied" {
		t.Fatalf("payload sidecar should classify profile denial, got %#v", outcome.Payload.Sidecar.Error)
	}
	if len(events) != 2 || events[0] != "tool_request" || events[1] != "tool_result" {
		t.Fatalf("events = %#v", events)
	}
	if records[0]["timeout_owner"] != ToolInvocationTimeoutOwnerWorkspaceWithMargin ||
		records[1]["timeout_owner"] != ToolInvocationTimeoutOwnerWorkspaceWithMargin {
		t.Fatalf("telemetry did not carry timeout owner: %#v", records)
	}
}

func TestConfiguredToolSurfaceInvokeToolAppliesExposureBeforeProfile(t *testing.T) {
	root := t.TempDir()
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

	if outcome.Result.Code != "profile_denied" || !strings.Contains(outcome.Result.Error, "schema-derived MCP discovery profile") {
		t.Fatalf("expected schema exposure denial, got %#v", outcome.Result)
	}
}

func TestConfiguredToolSurfaceInvokeToolFiltersResultBeforePayloadAndTelemetry(t *testing.T) {
	root := t.TempDir()
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

	if outcome.Result.Error != "" {
		t.Fatalf("expected executed pwd result, got %#v", outcome)
	}
	structuredOutput, ok := outcome.Payload.Sidecar.StructuredOutput.(map[string]any)
	if !ok {
		t.Fatalf("sidecar structured output = %#v", outcome.Payload.Sidecar.StructuredOutput)
	}
	if outcome.Payload.Output != "<redacted_path>" || structuredOutput["text"] != "<redacted_path>" {
		t.Fatalf("payload should use filtered result, got %#v", outcome.Payload)
	}
	if resultRecord["summary"] != "Printed <redacted_path>" {
		t.Fatalf("telemetry should use filtered summary, got %#v", resultRecord)
	}
}

func TestToolInvocationContextUsesMarginOnlyWhenRequested(t *testing.T) {
	marginCtx, marginCancel := toolInvocationContext(context.Background(), ToolTimeoutForRequest(ToolRequest{Tool: ToolNameDiscoveryQuery}), 5*time.Second)
	defer marginCancel()

	deadline, ok := marginCtx.Deadline()
	if !ok {
		t.Fatal("invocation context with margin should have a deadline")
	}
	remaining := time.Until(deadline)
	if remaining < 90*time.Second || remaining > 96*time.Second {
		t.Fatalf("deadline = %v, want workspace timeout plus CLI margin", remaining)
	}

	managerOwnedCtx, managerOwnedCancel := toolInvocationContext(context.Background(), ToolTimeoutForRequest(ToolRequest{Tool: ToolNameDiscoveryQuery}), 0)
	defer managerOwnedCancel()
	if _, ok := managerOwnedCtx.Deadline(); ok {
		t.Fatal("invocation context without margin should leave deadline ownership to Manager.Execute")
	}

	shellCtx, shellCancel := toolInvocationContext(context.Background(), ToolTimeoutForRequest(ToolRequest{Tool: ToolNameShell}), 5*time.Second)
	defer shellCancel()
	if _, ok := shellCtx.Deadline(); ok {
		t.Fatal("shell invocation context should defer to the shell tool's internal timeout")
	}
}
