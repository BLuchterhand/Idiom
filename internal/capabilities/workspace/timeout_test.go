package workspace

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"idiom/internal/capabilities/workspace/externaltool"
	contextpackcontract "idiom/internal/contracts/contextpack"
	"idiom/internal/platform/failures"
)

func TestToolTimeoutForKnownTools(t *testing.T) {
	cases := []struct {
		tool string
		want time.Duration
	}{
		{"read_file", 30 * time.Second},
		{"code_navigate", 30 * time.Second},
		{"cpp_read_symbol", 30 * time.Second},
		{"context_index", 5 * time.Minute},
		{"context_query", 90 * time.Second},
		{"context_trace_pack", 5 * time.Minute},
		{"discovery_query", 90 * time.Second},
		{"semgrep_scan", 5 * time.Minute},
		{"joern_scan", 5 * time.Minute},
		{"ctags_symbols", 90 * time.Second},
		{"shell", 0}, // shell is user-bounded by CommandTimeoutSeconds; no outer timeout
		{"unknown_made_up_tool", 30 * time.Second},
	}
	for _, tc := range cases {
		got := toolTimeoutFor(tc.tool)
		if got != tc.want {
			t.Errorf("toolTimeoutFor(%q) = %v, want %v", tc.tool, got, tc.want)
		}
	}
}

func TestToolTimeoutForRequestExtendsContextStatusRefresh(t *testing.T) {
	if got := ToolTimeoutForRequest(ToolRequest{Tool: "context_status"}); got != 30*time.Second {
		t.Fatalf("context_status without refresh should keep the normal stall fuse, got %v", got)
	}

	if got := ToolTimeoutForRequest(ToolRequest{Tool: "context_status", Refresh: true}); got != 5*time.Minute {
		t.Fatalf("context_status refresh should have index-build budget, got %v", got)
	}
}

func TestManagerToolTimeoutUsesExternalToolDeclaration(t *testing.T) {
	reg, err := externaltool.NewRegistry([]externaltool.Tool{
		{Name: "slow_external", Command: "echo", TimeoutSeconds: 75},
		{Name: "default_external", Command: "echo"},
	})
	if err != nil {
		t.Fatalf("externaltool.NewRegistry: %v", err)
	}

	mgr := NewManager(testWorkspaceConfig(t))
	mgr.toolSurface.SetExternalToolsRegistry(reg)

	if got := mgr.ToolTimeoutForRequest(ToolRequest{Tool: "slow_external"}); got != 75*time.Second {
		t.Fatalf("external timeout = %v, want 75s", got)
	}
	if got := mgr.ToolTimeoutForRequest(ToolRequest{Tool: "default_external"}); got != 30*time.Second {
		t.Fatalf("default external timeout = %v, want 30s", got)
	}
	if got := mgr.ToolTimeoutForRequest(ToolRequest{Tool: ToolNameShell}); got != 0 {
		t.Fatalf("built-in timeout fallback = %v, want shell internal timeout", got)
	}
}

func TestExecuteWithTimeoutFnReturnsResultPromptly(t *testing.T) {
	mgr := NewManager(testWorkspaceConfig(t))
	start := time.Now()
	res := mgr.executeWithTimeoutFn(context.Background(), ToolRequest{Tool: "fast"}, 1*time.Second, func(_ context.Context) ToolResult {
		return ToolResult{Tool: "fast", Summary: "ok"}
	})
	elapsed := time.Since(start)

	if res.Summary != "ok" {
		t.Fatalf("expected wrapper to return the dispatch's result, got %#v", res)
	}

	if elapsed > 200*time.Millisecond {
		t.Fatalf("fast dispatch should return promptly, elapsed=%v", elapsed)
	}
}

func TestExecuteWithTimeoutFnSynthesizesTimeoutOnStall(t *testing.T) {
	mgr := NewManager(testWorkspaceConfig(t))
	start := time.Now()
	res := mgr.executeWithTimeoutFn(context.Background(), ToolRequest{Tool: "slowProbe"}, 30*time.Millisecond, func(_ context.Context) ToolResult {
		time.Sleep(500 * time.Millisecond)

		return ToolResult{Tool: "slowProbe", Summary: "should never be returned"}
	})
	elapsed := time.Since(start)

	if !strings.Contains(res.Summary, "timed out") {
		t.Fatalf("expected timeout summary, got %q", res.Summary)
	}

	if !strings.Contains(res.Error, "slowProbe") {
		t.Fatalf("error should name the tool, got %q", res.Error)
	}

	if !strings.Contains(res.Error, "30ms") {
		t.Fatalf("error should name the configured timeout, got %q", res.Error)
	}

	if failures.CodeOf(res.Failure) != failures.DeadlineExceeded {
		t.Fatalf("timeout result should carry DeadlineExceeded failure metadata, got %#v", res.Failure)
	}

	if elapsed > 200*time.Millisecond {
		t.Fatalf("wrapper should fire the deadline, not wait for the slow dispatch (elapsed=%v)", elapsed)
	}
}

func TestExecuteWithTimeoutFnContextPackTimeoutCarriesFallbackPayload(t *testing.T) {
	mgr := NewManager(testWorkspaceConfig(t))
	req := ToolRequest{
		Tool:              ToolNameContextPack,
		Task:              "locate runtime prompt owner",
		Path:              "internal/runtime",
		Mode:              "semantic",
		Limit:             12,
		BudgetTokens:      2048,
		RequiredEditPaths: []string{"internal/runtime/agent/implementation_flow.go"},
	}

	res := mgr.executeWithTimeoutFn(context.Background(), req, 30*time.Millisecond, func(_ context.Context) ToolResult {
		time.Sleep(500 * time.Millisecond)

		return ToolResult{Tool: ToolNameContextPack, Summary: "should never be returned"}
	})

	if res.Tool != ToolNameContextPack || failures.CodeOf(res.Failure) != failures.DeadlineExceeded {
		t.Fatalf("context_pack timeout should keep tool/failure metadata, got %#v", res)
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(res.Output), &payload); err != nil {
		t.Fatalf("context_pack timeout should include JSON fallback output: %v\n%s", err, res.Output)
	}

	if payload["schema"] != contextpackcontract.FallbackSchema || payload["status"] != "unavailable" || payload["reason"] != "timeout" {
		t.Fatalf("unexpected fallback payload identity: %#v", payload)
	}

	if payload["retry_same_request"] != false {
		t.Fatalf("fallback should mark same-request retry unavailable, got %#v", payload["retry_same_request"])
	}

	capabilities, ok := payload["fallback_capabilities"].([]any)
	if !ok || len(capabilities) == 0 {
		t.Fatalf("fallback should expose read-only capability alternatives, got %#v", payload["fallback_capabilities"])
	}
}

func TestExecuteWithTimeoutFnRecoversPanic(t *testing.T) {
	mgr := NewManager(testWorkspaceConfig(t))
	res := mgr.executeWithTimeoutFn(context.Background(), ToolRequest{Tool: "panicProbe"}, 1*time.Second, func(_ context.Context) ToolResult {
		panic("synthetic panic")
	})

	if !strings.Contains(res.Summary, "panicked") {
		t.Fatalf("expected panic summary, got %q", res.Summary)
	}

	if !strings.Contains(res.Error, "synthetic panic") {
		t.Fatalf("error should carry the panic message, got %q", res.Error)
	}

	if failures.CodeOf(res.Failure) != failures.Internal {
		t.Fatalf("panic result should carry Internal failure metadata, got %#v", res.Failure)
	}
}

func TestExecuteWithTimeoutFnHonorsParentCancellation(t *testing.T) {
	mgr := NewManager(testWorkspaceConfig(t))
	ctx, cancel := context.WithCancel(context.Background())
	// Cancel after 20ms; the dispatch waits 500ms, so the wrapper should
	// see the parent cancellation propagate via timeoutCtx.Done() and
	// synthesize a "Tool canceled" result.
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	res := mgr.executeWithTimeoutFn(ctx, ToolRequest{Tool: "slowCancellable"}, 1*time.Second, func(_ context.Context) ToolResult {
		time.Sleep(500 * time.Millisecond)

		return ToolResult{Tool: "slowCancellable", Summary: "should never be returned"}
	})
	elapsed := time.Since(start)

	if !strings.Contains(res.Summary, "canceled") && !strings.Contains(res.Summary, "timed out") {
		t.Fatalf("expected cancellation/timeout summary, got %q", res.Summary)
	}

	if failures.CodeOf(res.Failure) != failures.Canceled && failures.CodeOf(res.Failure) != failures.DeadlineExceeded {
		t.Fatalf("cancellation result should carry canceled/timeout metadata, got %#v", res.Failure)
	}

	if elapsed > 200*time.Millisecond {
		t.Fatalf("wrapper should observe parent cancellation, elapsed=%v", elapsed)
	}
}
