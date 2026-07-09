package workspace

import "testing"

func TestDiscoveryProfileClassifiesGenericToolsForBalance(t *testing.T) {
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
		t.Run(tc.name, func(t *testing.T) {
			got := DiscoveryProfileForRequest(tc.req)
			if got.Class != tc.wantClass || got.Scope != tc.wantScope || got.Cost != tc.wantCost || !got.Discovery {
				t.Fatalf("DiscoveryProfileForRequest(%#v) = %#v, want class=%q scope=%q cost=%q discovery", tc.req, got, tc.wantClass, tc.wantScope, tc.wantCost)
			}

			if tc.req.Tool != "source_sanity" && !got.Generic {
				t.Fatalf("DiscoveryProfileForRequest(%#v) = %#v, want generic discovery", tc.req, got)
			}
		})
	}
}

func TestDiscoveryProfileClassifiesLanguageToolsForBalance(t *testing.T) {
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
		if got.Class != tc.wantClass || got.Scope != tc.wantScope || got.Cost != tc.wantCost {
			t.Fatalf("%s profile = %#v, want class=%q scope=%q cost=%q", tc.tool, got, tc.wantClass, tc.wantScope, tc.wantCost)
		}
	}
}

func TestDiscoveryProfileDoesNotCountVerificationShellAsDiscovery(t *testing.T) {
	got := DiscoveryProfileForRequest(ToolRequest{Tool: "shell", Command: "go test ./..."})
	if got.Discovery || got.Class != "other" {
		t.Fatalf("verification shell should not count as discovery, got %#v", got)
	}

	got = DiscoveryProfileForRequest(ToolRequest{Tool: "shell", Command: "rg TODO internal"})
	if !got.Discovery || got.Class != "shell_discovery" || got.Scope != "targeted" {
		t.Fatalf("targeted shell search should count as discovery, got %#v", got)
	}
}

func TestGenericDiscoveryToolsCountAsNarrowingEvidence(t *testing.T) {
	mgr := NewManager(testWorkspaceConfig(t))

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
		if !IsNarrowingRequest(req) {
			t.Fatalf("%s should count as a narrowing request", req.Tool)
		}

		if !mgr.ShouldClearBroadInspectionLock(req, ToolResult{Tool: req.Tool, Summary: "ok"}) {
			t.Fatalf("%s should clear broad-inspection lock after a successful result", req.Tool)
		}
	}
}
