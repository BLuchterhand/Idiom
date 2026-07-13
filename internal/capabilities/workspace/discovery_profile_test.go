package workspace

import (
	"fmt"
	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("discovery profile classifies generic tools for balance", func() {

	tests := []struct {
		name      string
		req       ToolRequest
		wantClass string
		wantScope string
		wantCost  string
	}{
		{name: "composite query", req: ToolRequest{Tool: "discovery_query", Purpose: "impact_analysis", Path: ".", Pattern: "Execute"}, wantClass: "composite_query", wantScope: "targeted", wantCost: "medium"},
		{name: "composite security query", req: ToolRequest{Tool: "discovery_query", Purpose: "security_review", Path: "."}, wantClass: "composite_query", wantScope: "broad", wantCost: "high"},
		{name: "context pack", req: ToolRequest{Tool: "context_pack", Task: "Plan Execute", Path: "."}, wantClass: "context_pack", wantScope: "targeted", wantCost: "medium"},
		{name: "context trace pack", req: ToolRequest{Tool: "context_trace_pack", Command: "pytest tests/test_app.py", Path: "."}, wantClass: "runtime_trace", wantScope: "targeted", wantCost: "medium"},
		{name: "harness feedback", req: ToolRequest{Tool: "harness_feedback", Component: "context_pack"}, wantClass: "harness_feedback", wantScope: "targeted", wantCost: "low"},
		{name: "code navigation", req: ToolRequest{Tool: "code_navigate", Capability: "definition", Path: "src/app.cpp", Pattern: "Run"}, wantClass: "language_relationship", wantScope: "targeted", wantCost: "medium"},
		{name: "read-only capability", req: ToolRequest{Tool: "read_only_capability", Capability: "definition", Path: "src/app.cpp", Pattern: "Run"}, wantClass: "language_relationship", wantScope: "targeted", wantCost: "medium"},
		{name: "read-only inspect symbol capability", req: ToolRequest{Tool: "read_only_capability", Capability: "inspect-symbol", Path: "src/app.cpp", Pattern: "Run"}, wantClass: "language_symbol", wantScope: "targeted", wantCost: "low"},
		{name: "read-only calls-from capability", req: ToolRequest{Tool: "read_only_capability", Capability: "calls-from", Path: "src/app.cpp", Pattern: "Run"}, wantClass: "language_relationship", wantScope: "targeted", wantCost: "medium"},
		{name: "read-only calls-into capability", req: ToolRequest{Tool: "read_only_capability", Capability: "calls-into", Path: "src/app.cpp", Pattern: "Run"}, wantClass: "language_relationship", wantScope: "targeted", wantCost: "medium"},
		{name: "read-only implementations capability", req: ToolRequest{Tool: "read_only_capability", Capability: "find-implementations", Path: "src/app.cpp", Pattern: "Run"}, wantClass: "language_relationship", wantScope: "targeted", wantCost: "medium"},
		{name: "source sanity", req: ToolRequest{Tool: "source_sanity", Path: "src/app.cpp"}, wantClass: "diagnostics", wantScope: "targeted", wantCost: "medium"},
		{name: "structural", req: ToolRequest{Tool: "structural_search", Path: "internal", Pattern: "if $X { $Y }"}, wantClass: "ast_search", wantScope: "targeted", wantCost: "medium"},
		{name: "dependency root", req: ToolRequest{Tool: "dependency_graph", Path: "."}, wantClass: "dependency_graph", wantScope: "broad", wantCost: "medium"},
		{name: "dependency scoped", req: ToolRequest{Tool: "dependency_graph", Path: "internal/capabilities/workspace"}, wantClass: "dependency_graph", wantScope: "targeted", wantCost: "medium"},
		{name: "history symbol", req: ToolRequest{Tool: "history_symbol", Path: "internal/capabilities/workspace/manager.go", Pattern: "Execute"}, wantClass: "history", wantScope: "targeted", wantCost: "medium"},
		{name: "blame", req: ToolRequest{Tool: "blame_range", Path: "internal/capabilities/workspace/manager.go", StartLine: 1, EndLine: 3}, wantClass: "history", wantScope: "targeted", wantCost: "medium"},
		{name: "ctags broad", req: ToolRequest{Tool: "ctags_symbols", Path: "."}, wantClass: "symbol_index", wantScope: "broad", wantCost: "medium"},
		{name: "ctags filtered", req: ToolRequest{Tool: "ctags_symbols", Path: ".", Pattern: "Execute"}, wantClass: "symbol_index", wantScope: "targeted", wantCost: "medium"},
		{name: "semgrep", req: ToolRequest{Tool: "semgrep_scan", Path: "."}, wantClass: "security_scan", wantScope: "broad", wantCost: "high"},
		{name: "joern", req: ToolRequest{Tool: "joern_scan", Path: "src"}, wantClass: "security_scan", wantScope: "targeted", wantCost: "high"},
	}
	for _, tc := range tests {
		ginkgo.It(tc.name, func() {
			got := DiscoveryProfileForRequest(tc.req)
			gomega.Expect(got.Class).To(gomega.Equal(tc.wantClass), fmt.Sprintf("DiscoveryProfileForRequest(%#v) = %#v, want class=%q scope=%q cost=%q discovery", tc.req, got, tc.wantClass, tc.wantScope, tc.wantCost))
			gomega.Expect(got.Scope).To(gomega.Equal(tc.wantScope), fmt.Sprintf("DiscoveryProfileForRequest(%#v) = %#v, want class=%q scope=%q cost=%q discovery", tc.req, got, tc.wantClass, tc.wantScope, tc.wantCost))
			gomega.Expect(got.Cost).To(gomega.Equal(tc.wantCost), fmt.Sprintf("DiscoveryProfileForRequest(%#v) = %#v, want class=%q scope=%q cost=%q discovery", tc.req, got, tc.wantClass, tc.wantScope, tc.wantCost))
			gomega.Expect(got.Discovery).To(gomega.BeTrue(), fmt.Sprintf("DiscoveryProfileForRequest(%#v) = %#v, want class=%q scope=%q cost=%q discovery", tc.req, got, tc.wantClass, tc.wantScope, tc.wantCost))
			if tc.req.Tool != "source_sanity" {
				gomega.Expect(got.Generic).To(gomega.BeTrue(), fmt.Sprintf("DiscoveryProfileForRequest(%#v) = %#v, want generic discovery", tc.req, got))
			}

		})
	}
})
var _ = ginkgo.Describe("discovery profile", func() {
	ginkgo.It("discovery profile classifies language tools for balance", func() {
		tests := []struct {
			tool      string
			wantClass string
			wantScope string
			wantCost  string
		}{
			{tool: "go_overview", wantClass: "language_overview", wantScope: "broad", wantCost: "medium"},
			{tool: "go_workspace_symbol", wantClass: "language_symbol", wantScope: "targeted", wantCost: "low"},
			{tool: "cpp_references", wantClass: "language_relationship", wantScope: "targeted", wantCost: "medium"},
			{tool: "python_diagnostics", wantClass: "diagnostics", wantScope: "targeted", wantCost: "medium"},
			{tool: "ts_file_outline", wantClass: "language_symbol", wantScope: "targeted", wantCost: "low"},
		}
		for _, tc := range tests {
			got := DiscoveryProfileForRequest(ToolRequest{Tool: tc.tool})
			gomega.Expect(got.Class).To(gomega.Equal(tc.wantClass), fmt.Sprintf("%s profile = %#v, want class=%q scope=%q cost=%q", tc.tool, got, tc.wantClass, tc.wantScope, tc.wantCost))
			gomega.Expect(got.Scope).To(gomega.Equal(tc.wantScope), fmt.Sprintf("%s profile = %#v, want class=%q scope=%q cost=%q", tc.tool, got, tc.wantClass, tc.wantScope, tc.wantCost))
			gomega.Expect(got.Cost).To(gomega.Equal(tc.wantCost), fmt.Sprintf("%s profile = %#v, want class=%q scope=%q cost=%q", tc.tool, got, tc.wantClass, tc.wantScope, tc.wantCost))

		}
	})
	ginkgo.It("discovery profile does not count verification shell as discovery", func() {
		got := DiscoveryProfileForRequest(ToolRequest{Tool: "shell", Command: "go test ./..."})
		gomega.Expect(got.Discovery).To(gomega.BeFalse(), fmt.Sprintf("verification shell should not count as discovery, got %#v", got))
		gomega.Expect(got.Class).To(gomega.Equal("other"), fmt.Sprintf("verification shell should not count as discovery, got %#v", got))

		got = DiscoveryProfileForRequest(ToolRequest{Tool: "shell", Command: "rg TODO internal"})
		gomega.Expect(got.Discovery).To(gomega.BeTrue(), fmt.Sprintf("targeted shell search should count as discovery, got %#v", got))
		gomega.Expect(got.Class).To(gomega.Equal("shell_discovery"), fmt.Sprintf("targeted shell search should count as discovery, got %#v", got))
		gomega.Expect(got.Scope).To(gomega.Equal("targeted"), fmt.Sprintf("targeted shell search should count as discovery, got %#v", got))

	})
	ginkgo.It("generic discovery tools count as narrowing evidence", func() {
		mgr := NewManager(testWorkspaceConfig())

		for _, req := range []ToolRequest{
			{Tool: "harness_feedback", Component: "context_pack", Expected: "x", Observed: "y", Impact: "z"},
			{Tool: "code_navigate", Capability: "read_symbol", Path: "src/app.cpp", Pattern: "Run"},
			{Tool: "read_only_capability", Capability: "definition", Path: "src/app.cpp", Pattern: "Run"},
			{Tool: "source_sanity", Path: "src/app.cpp"},
			{Tool: "context_pack", Task: "Plan Execute", Path: "."},
			{Tool: "context_trace_pack", Command: "pytest tests/test_app.py", Path: "."},
			{Tool: "discovery_query", Purpose: "impact_analysis", Path: ".", Pattern: "Execute"},
			{Tool: "structural_search", Pattern: "if $X { $Y }", Path: "."},
			{Tool: "dependency_graph", Language: "go", Path: "."},
			{Tool: "history_range", Path: "internal/capabilities/workspace/manager.go", StartLine: 1, EndLine: 3},
			{Tool: "history_symbol", Path: "internal/capabilities/workspace/manager.go", Pattern: "Execute"},
			{Tool: "blame_range", Path: "internal/capabilities/workspace/manager.go", StartLine: 1, EndLine: 3},
			{Tool: "ctags_symbols", Pattern: "Execute", Path: "."},
			{Tool: "semgrep_scan", Path: "."},
			{Tool: "joern_scan", Path: "."},
		} {
			gomega.Expect(IsNarrowingRequest(req)).To(gomega.BeTrue(), fmt.Sprintf("%s should count as a narrowing request", req.Tool))
			gomega.Expect(mgr.ShouldClearBroadInspectionLock(req, ToolResult{Tool: req.Tool, Summary: "ok"})).To(gomega.BeTrue(), fmt.Sprintf("%s should clear broad-inspection lock after a successful result", req.Tool))

		}
	})
})
