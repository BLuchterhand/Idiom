package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"idiom/internal/capabilities/workspace/externaltool"
	contextpackcontract "idiom/internal/contracts/contextpack"
	"idiom/internal/platform/failures"
)

var _ = ginkgo.Describe("timeout", func() {
	ginkgo.It("tool timeout for known tools", func() {
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
			gomega.Expect(got).To(gomega.Equal(tc.want), fmt.Sprintf("toolTimeoutFor(%q) = %v, want %v", tc.tool, got, tc.want))

		}
	})
	ginkgo.It("tool timeout for request extends context status refresh", func() {
		if got := ToolTimeoutForRequest(ToolRequest{Tool: "context_status"}); got != 30*time.Second {
			ginkgo.Fail(fmt.Sprintf("context_status without refresh should keep the normal stall fuse, got %v", got))
		}

		if got := ToolTimeoutForRequest(ToolRequest{Tool: "context_status", Refresh: true}); got != 5*time.Minute {
			ginkgo.Fail(fmt.Sprintf("context_status refresh should have index-build budget, got %v", got))
		}
	})
	ginkgo.It("manager tool timeout uses external tool declaration", func() {
		reg, err := externaltool.NewRegistry([]externaltool.Tool{
			{Name: "slow_external", Command: "echo", TimeoutSeconds: 75},
			{Name: "default_external", Command: "echo"},
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("externaltool.NewRegistry: %v", err))

		mgr := NewManager(testWorkspaceConfig())
		mgr.toolSurface.SetExternalToolsRegistry(reg)

		if got := mgr.ToolTimeoutForRequest(ToolRequest{Tool: "slow_external"}); got != 75*time.Second {
			ginkgo.Fail(fmt.Sprintf("external timeout = %v, want 75s", got))
		}
		if got := mgr.ToolTimeoutForRequest(ToolRequest{Tool: "default_external"}); got != 30*time.Second {
			ginkgo.Fail(fmt.Sprintf("default external timeout = %v, want 30s", got))
		}
		if got := mgr.ToolTimeoutForRequest(ToolRequest{Tool: ToolNameShell}); got != 0 {
			ginkgo.Fail(fmt.Sprintf("built-in timeout fallback = %v, want shell internal timeout", got))
		}
	})
	ginkgo.It("execute with timeout fn returns result promptly", func() {
		mgr := NewManager(testWorkspaceConfig())
		start := time.Now()
		res := mgr.executeWithTimeoutFn(context.Background(), ToolRequest{Tool: "fast"}, 1*time.Second, func(_ context.Context) ToolResult {
			return ToolResult{Tool: "fast", Summary: "ok"}
		})
		elapsed := time.Since(start)
		gomega.Expect(res.Summary).To(gomega.Equal("ok"), fmt.Sprintf("expected wrapper to return the dispatch's result, got %#v", res))
		gomega.Expect(elapsed > 200*time.Millisecond).To(gomega.BeFalse(), fmt.Sprintf("fast dispatch should return promptly, elapsed=%v", elapsed))

	})
	ginkgo.It("execute with timeout fn synthesizes timeout on stall", func() {
		mgr := NewManager(testWorkspaceConfig())
		start := time.Now()
		res := mgr.executeWithTimeoutFn(context.Background(), ToolRequest{Tool: "slowProbe"}, 30*time.Millisecond, func(_ context.Context) ToolResult {
			time.Sleep(500 * time.Millisecond)

			return ToolResult{Tool: "slowProbe", Summary: "should never be returned"}
		})
		elapsed := time.Since(start)
		gomega.Expect(res.Summary).To(gomega.ContainSubstring("timed out"), fmt.Sprintf("expected timeout summary, got %q", res.Summary))
		gomega.Expect(res.Error).To(gomega.ContainSubstring("slowProbe"), fmt.Sprintf("error should name the tool, got %q", res.Error))
		gomega.Expect(res.Error).To(gomega.ContainSubstring("30ms"), fmt.Sprintf("error should name the configured timeout, got %q", res.Error))
		gomega.Expect(failures.CodeOf(res.Failure)).To(gomega.Equal(failures.DeadlineExceeded), fmt.Sprintf("timeout result should carry DeadlineExceeded failure metadata, got %#v", res.Failure))
		gomega.Expect(elapsed > 200*time.Millisecond).To(gomega.BeFalse(), fmt.Sprintf("wrapper should fire the deadline, not wait for the slow dispatch (elapsed=%v)", elapsed))

	})
	ginkgo.It("execute with timeout fn context pack timeout carries fallback payload", func() {
		mgr := NewManager(testWorkspaceConfig())
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
		gomega.Expect(res.Tool).To(gomega.Equal(ToolNameContextPack), fmt.Sprintf("context_pack timeout should keep tool/failure metadata, got %#v", res))
		gomega.Expect(failures.CodeOf(res.Failure)).To(gomega.Equal(failures.DeadlineExceeded), fmt.Sprintf("context_pack timeout should keep tool/failure metadata, got %#v", res))

		var payload map[string]any
		gomega.Expect(json.Unmarshal([]byte(res.Output), &payload)).To(gomega.Succeed())
		gomega.Expect(payload["schema"]).To(gomega.Equal(contextpackcontract.FallbackSchema), fmt.Sprintf("unexpected fallback payload identity: %#v", payload))
		gomega.Expect(payload["status"]).To(gomega.Equal("unavailable"), fmt.Sprintf("unexpected fallback payload identity: %#v", payload))
		gomega.Expect(payload["reason"]).To(gomega.Equal("timeout"), fmt.Sprintf("unexpected fallback payload identity: %#v", payload))
		gomega.Expect(payload["retry_same_request"]).To(gomega.Equal(false), fmt.Sprintf("fallback should mark same-request retry unavailable, got %#v", payload["retry_same_request"]))

		capabilities, ok := payload["fallback_capabilities"].([]any)
		gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("fallback should expose read-only capability alternatives, got %#v", payload["fallback_capabilities"]))
		gomega.Expect(capabilities).NotTo(gomega.BeEmpty(), fmt.Sprintf("fallback should expose read-only capability alternatives, got %#v", payload["fallback_capabilities"]))

	})
	ginkgo.It("execute with timeout fn recovers panic", func() {
		mgr := NewManager(testWorkspaceConfig())
		res := mgr.executeWithTimeoutFn(context.Background(), ToolRequest{Tool: "panicProbe"}, 1*time.Second, func(_ context.Context) ToolResult {
			panic("synthetic panic")
		})
		gomega.Expect(res.Summary).To(gomega.ContainSubstring("panicked"), fmt.Sprintf("expected panic summary, got %q", res.Summary))
		gomega.Expect(res.Error).To(gomega.ContainSubstring("synthetic panic"), fmt.Sprintf("error should carry the panic message, got %q", res.Error))
		gomega.Expect(failures.CodeOf(res.Failure)).To(gomega.Equal(failures.Internal), fmt.Sprintf("panic result should carry Internal failure metadata, got %#v", res.Failure))

	})
	ginkgo.It("execute with timeout fn honors parent cancellation", func() {
		mgr := NewManager(testWorkspaceConfig())
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
		gomega.Expect(res.Summary).To(gomega.Or(
			gomega.ContainSubstring("canceled"),
			gomega.ContainSubstring("timed out"),
		), fmt.Sprintf("expected cancellation/timeout summary, got %q", res.Summary))
		gomega.Expect(failures.CodeOf(res.Failure)).To(gomega.Or(
			gomega.Equal(failures.Canceled),
			gomega.Equal(failures.DeadlineExceeded),
		), fmt.Sprintf("cancellation result should carry canceled/timeout metadata, got %#v", res.Failure))
		gomega.Expect(elapsed > 200*time.Millisecond).To(gomega.BeFalse(), fmt.Sprintf("wrapper should observe parent cancellation, elapsed=%v", elapsed))

	})
})
