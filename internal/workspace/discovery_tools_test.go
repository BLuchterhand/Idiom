package workspace

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"idiom/internal/comprehension"
	"idiom/internal/config"
	"idiom/internal/eval"
)

func TestGenericDiscoveryToolsAreAdvertised(t *testing.T) {
	tools := ToolManifestTools(ToolManifestToolOptions{})
	for _, want := range []string{
		"harness_audit",
		"harness_feedback",
		"file_stats",
		"context_index",
		"context_status",
		"context_query",
		"context_zoom",
		"context_pack",
		"context_trace_pack",
		"discovery_query",
		"structural_search",
		"dependency_graph",
		"history_range",
		"history_symbol",
		"blame_range",
		"ctags_symbols",
		"semgrep_scan",
		"joern_scan",
	} {
		if !slices.Contains(tools, want) {
			t.Fatalf("expected manifest tools to include %q, got %#v", want, tools)
		}
	}

	defs := BuildToolDefinitions(tools)
	var names []string
	for _, def := range defs {
		names = append(names, def.Name)
	}
	for _, want := range []string{"harness_audit", "harness_feedback", "file_stats", "context_index", "context_status", "context_query", "context_zoom", "context_pack", "context_trace_pack", "discovery_query", "structural_search", "dependency_graph", "history_range", "blame_range"} {
		if !slices.Contains(names, want) {
			t.Fatalf("expected tool definitions to include %q, got %#v", want, names)
		}
	}
}

func TestContextToolsBuildQueryAndReportStaleSymbols(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, filepath.Join(root, "go.mod"), "module example.com/contexttools\n\ngo 1.22\n")
	sourcePath := filepath.Join(root, "pkg", "pkg.go")
	writeWorkspaceFile(t, sourcePath, "package pkg\n\nfunc Run() string { return \"ok\" }\n")
	mgr := NewManager(config.Workspace{Root: root, CWD: root})

	result := mgr.Execute(context.Background(), ToolRequest{Tool: "context_index", Language: "go"})
	if result.Error != "" {
		t.Fatalf("context_index returned error: %v\n%s", result.Error, result.Output)
	}
	var buildReport struct {
		FilesIndexed     int      `json:"files_indexed"`
		SymbolsIndexed   int      `json:"symbols_indexed"`
		Languages        []string `json:"languages"`
		CollectorReports []struct {
			Name      string   `json:"name"`
			Languages []string `json:"languages"`
		} `json:"collector_reports"`
	}
	if err := json.Unmarshal([]byte(result.Output), &buildReport); err != nil {
		t.Fatalf("decode context_index output: %v\n%s", err, result.Output)
	}
	if buildReport.FilesIndexed != 1 || buildReport.SymbolsIndexed == 0 || !slices.Contains(buildReport.Languages, "go") {
		t.Fatalf("unexpected index report: %#v", buildReport)
	}
	if !slices.ContainsFunc(buildReport.CollectorReports, func(report struct {
		Name      string   `json:"name"`
		Languages []string `json:"languages"`
	}) bool {
		return report.Name == "gopls" && slices.Contains(report.Languages, "go")
	}) {
		t.Fatalf("expected context_index to report Go backend enrichment, got %#v", buildReport.CollectorReports)
	}

	result = mgr.Execute(context.Background(), ToolRequest{Tool: "context_query", Pattern: "Run", Resolution: "symbol", IncludeEvidence: true})
	if result.Error != "" {
		t.Fatalf("context_query returned error: %v\n%s", result.Error, result.Output)
	}
	var queryReport struct {
		Nodes []struct {
			ID         string `json:"id"`
			Confidence string `json:"confidence"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal([]byte(result.Output), &queryReport); err != nil {
		t.Fatalf("decode context_query output: %v\n%s", err, result.Output)
	}
	if !slices.ContainsFunc(queryReport.Nodes, func(node struct {
		ID         string `json:"id"`
		Confidence string `json:"confidence"`
	}) bool {
		return node.ID == "symbol://pkg/pkg.go#Run" && node.Confidence != ""
	}) {
		t.Fatalf("expected Run symbol handle with confidence, got %#v", queryReport.Nodes)
	}

	result = mgr.Execute(context.Background(), ToolRequest{Tool: "context_pack", Task: "Plan a change to Run", RequiredTests: []string{"go test ./..."}})
	if result.Error != "" {
		t.Fatalf("context_pack returned error: %v\n%s", result.Error, result.Output)
	}
	var packReport struct {
		ArtifactPath   string `json:"artifact_path"`
		CandidateFiles []struct {
			Path string `json:"path"`
			Role string `json:"role"`
		} `json:"candidate_files"`
		Tests []struct {
			Command string `json:"command"`
		} `json:"tests"`
	}
	if err := json.Unmarshal([]byte(result.Output), &packReport); err != nil {
		t.Fatalf("decode context_pack output: %v\n%s", err, result.Output)
	}
	if packReport.ArtifactPath == "" || !slices.ContainsFunc(packReport.CandidateFiles, func(file struct {
		Path string `json:"path"`
		Role string `json:"role"`
	}) bool {
		return file.Path == "pkg/pkg.go" && file.Role == "editable"
	}) {
		t.Fatalf("expected context_pack artifact and editable candidate, got %#v", packReport)
	}
	if !slices.ContainsFunc(packReport.Tests, func(test struct {
		Command string `json:"command"`
	}) bool {
		return test.Command == "go test ./..."
	}) {
		t.Fatalf("expected context_pack to preserve required tests, got %#v", packReport.Tests)
	}

	writeWorkspaceFile(t, sourcePath, "package pkg\n\nfunc Run() string { return \"changed\" }\n")
	result = mgr.Execute(context.Background(), ToolRequest{Tool: "context_status"})
	if result.Error != "" {
		t.Fatalf("context_status returned error: %v\n%s", result.Error, result.Output)
	}
	var statusReport struct {
		Fresh        bool     `json:"fresh"`
		StaleFiles   []string `json:"stale_files"`
		StaleSymbols int      `json:"stale_symbols"`
	}
	if err := json.Unmarshal([]byte(result.Output), &statusReport); err != nil {
		t.Fatalf("decode context_status output: %v\n%s", err, result.Output)
	}
	if statusReport.Fresh || !slices.Contains(statusReport.StaleFiles, "pkg/pkg.go") || statusReport.StaleSymbols == 0 {
		t.Fatalf("expected stale file and symbols, got %#v", statusReport)
	}
}

func TestContextPackAutoRefreshesBoundedStaleScope(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, filepath.Join(root, "go.mod"), "module example.com/autorefresh\n\ngo 1.22\n")
	sourcePath := filepath.Join(root, "pkg", "pkg.go")
	writeWorkspaceFile(t, sourcePath, "package pkg\n\nfunc Run() string { return \"ok\" }\n")
	mgr := NewManager(config.Workspace{Root: root, CWD: root})

	result := mgr.Execute(context.Background(), ToolRequest{Tool: "context_index", Language: "go"})
	if result.Error != "" {
		t.Fatalf("context_index returned error: %v\n%s", result.Error, result.Output)
	}
	writeWorkspaceFile(t, sourcePath, "package pkg\n\nfunc Run() string { return \"changed\" }\n")

	result = mgr.Execute(context.Background(), ToolRequest{Tool: "context_pack", Task: "Plan a change to Run", Path: "pkg", Language: "go"})
	if result.Error != "" {
		t.Fatalf("context_pack returned error: %v\n%s", result.Error, result.Output)
	}
	var packReport struct {
		Fresh       bool `json:"fresh"`
		AutoRefresh struct {
			Attempted    bool   `json:"attempted"`
			Scope        string `json:"scope"`
			Reason       string `json:"reason"`
			FilesIndexed int    `json:"files_indexed"`
			FreshAfter   bool   `json:"fresh_after"`
		} `json:"auto_refresh"`
		CandidateFiles []struct {
			Path  string `json:"path"`
			Stale bool   `json:"stale"`
		} `json:"candidate_files"`
	}
	if err := json.Unmarshal([]byte(result.Output), &packReport); err != nil {
		t.Fatalf("decode context_pack output: %v\n%s", err, result.Output)
	}
	if !packReport.AutoRefresh.Attempted || packReport.AutoRefresh.Scope != "pkg" || packReport.AutoRefresh.Reason != "stale_scope_files" || packReport.AutoRefresh.FilesIndexed != 1 || !packReport.AutoRefresh.FreshAfter {
		t.Fatalf("expected bounded stale-scope auto-refresh, got %#v", packReport.AutoRefresh)
	}
	if !packReport.Fresh {
		t.Fatalf("expected pack to use refreshed graph, got %#v", packReport)
	}
	if !slices.ContainsFunc(packReport.CandidateFiles, func(file struct {
		Path  string `json:"path"`
		Stale bool   `json:"stale"`
	}) bool {
		return file.Path == "pkg/pkg.go" && !file.Stale
	}) {
		t.Fatalf("expected refreshed non-stale candidate, got %#v", packReport.CandidateFiles)
	}
}

func TestContextStatusRefreshesBoundedStaleScope(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, filepath.Join(root, "go.mod"), "module example.com/statusbounded\n\ngo 1.22\n")
	sourcePath := filepath.Join(root, "pkg", "pkg.go")
	writeWorkspaceFile(t, sourcePath, "package pkg\n\nfunc Run() string { return \"ok\" }\n")
	mgr := NewManager(config.Workspace{Root: root, CWD: root})

	result := mgr.Execute(context.Background(), ToolRequest{Tool: "context_index", Language: "go"})
	if result.Error != "" {
		t.Fatalf("context_index returned error: %v\n%s", result.Error, result.Output)
	}
	writeWorkspaceFile(t, sourcePath, "package pkg\n\nfunc Run() string { return \"changed\" }\n")

	result = mgr.Execute(context.Background(), ToolRequest{Tool: "context_status", Refresh: true, Path: "pkg", Language: "go"})
	if result.Error != "" {
		t.Fatalf("context_status returned error: %v\n%s", result.Error, result.Output)
	}
	var statusReport struct {
		Fresh       bool     `json:"fresh"`
		StaleFiles  []string `json:"stale_files"`
		AutoRefresh struct {
			Attempted      bool     `json:"attempted"`
			Scope          string   `json:"scope"`
			Reason         string   `json:"reason"`
			FilesIndexed   int      `json:"files_indexed"`
			FreshAfter     bool     `json:"fresh_after"`
			RefreshedFiles []string `json:"refreshed_files"`
		} `json:"auto_refresh"`
	}
	if err := json.Unmarshal([]byte(result.Output), &statusReport); err != nil {
		t.Fatalf("decode context_status output: %v\n%s", err, result.Output)
	}
	if !statusReport.AutoRefresh.Attempted || statusReport.AutoRefresh.Scope != "pkg" || statusReport.AutoRefresh.Reason != "stale_scope_files" || statusReport.AutoRefresh.FilesIndexed != 1 || !statusReport.AutoRefresh.FreshAfter {
		t.Fatalf("expected bounded stale-scope status refresh, got %#v", statusReport.AutoRefresh)
	}
	if !statusReport.Fresh || len(statusReport.StaleFiles) != 0 || !slices.Contains(statusReport.AutoRefresh.RefreshedFiles, "pkg/pkg.go") {
		t.Fatalf("expected fresh status after bounded refresh, got %#v", statusReport)
	}
}

func TestContextStatusRefreshesSmallStaleFileSetWithoutScope(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, filepath.Join(root, "go.mod"), "module example.com/statussmall\n\ngo 1.22\n")
	sourcePath := filepath.Join(root, "pkg", "pkg.go")
	writeWorkspaceFile(t, sourcePath, "package pkg\n\nfunc Run() string { return \"ok\" }\n")
	mgr := NewManager(config.Workspace{Root: root, CWD: root})

	result := mgr.Execute(context.Background(), ToolRequest{Tool: "context_index", Language: "go"})
	if result.Error != "" {
		t.Fatalf("context_index returned error: %v\n%s", result.Error, result.Output)
	}
	writeWorkspaceFile(t, sourcePath, "package pkg\n\nfunc Run() string { return \"changed\" }\n")

	result = mgr.Execute(context.Background(), ToolRequest{Tool: "context_status", Refresh: true, Language: "go"})
	if result.Error != "" {
		t.Fatalf("context_status returned error: %v\n%s", result.Error, result.Output)
	}
	var statusReport struct {
		Fresh       bool     `json:"fresh"`
		StaleFiles  []string `json:"stale_files"`
		AutoRefresh struct {
			Attempted      bool     `json:"attempted"`
			Scope          string   `json:"scope"`
			Reason         string   `json:"reason"`
			FilesIndexed   int      `json:"files_indexed"`
			FreshAfter     bool     `json:"fresh_after"`
			RefreshedFiles []string `json:"refreshed_files"`
		} `json:"auto_refresh"`
	}
	if err := json.Unmarshal([]byte(result.Output), &statusReport); err != nil {
		t.Fatalf("decode context_status output: %v\n%s", err, result.Output)
	}
	if !statusReport.AutoRefresh.Attempted || statusReport.AutoRefresh.Reason != "small_stale_file_set" || statusReport.AutoRefresh.FilesIndexed != 1 || !statusReport.AutoRefresh.FreshAfter {
		t.Fatalf("expected capped small-stale-set status refresh, got %#v", statusReport.AutoRefresh)
	}
	if !statusReport.Fresh || len(statusReport.StaleFiles) != 0 || !slices.Contains(statusReport.AutoRefresh.RefreshedFiles, "pkg/pkg.go") {
		t.Fatalf("expected fresh status after small stale-file refresh, got %#v", statusReport)
	}
}

func TestContextStatusRefreshBuildsMissingRootIndexWithoutScope(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, filepath.Join(root, "go.mod"), "module example.com/statusmissingroot\n\ngo 1.22\n")
	writeWorkspaceFile(t, filepath.Join(root, "pkg", "pkg.go"), "package pkg\n\nfunc Run() string { return \"ok\" }\n")
	mgr := NewManager(config.Workspace{Root: root, CWD: root})

	result := mgr.Execute(context.Background(), ToolRequest{Tool: "context_status", Refresh: true, Language: "go"})
	if result.Error != "" {
		t.Fatalf("context_status returned error: %v\n%s", result.Error, result.Output)
	}
	var statusReport struct {
		Fresh       bool `json:"fresh"`
		Indexed     bool `json:"indexed"`
		AutoRefresh struct {
			Attempted    bool   `json:"attempted"`
			Scope        string `json:"scope"`
			Reason       string `json:"reason"`
			FilesIndexed int    `json:"files_indexed"`
			FreshAfter   bool   `json:"fresh_after"`
		} `json:"auto_refresh"`
	}
	if err := json.Unmarshal([]byte(result.Output), &statusReport); err != nil {
		t.Fatalf("decode context_status output: %v\n%s", err, result.Output)
	}
	if !statusReport.Indexed || !statusReport.Fresh || !statusReport.AutoRefresh.Attempted || statusReport.AutoRefresh.Scope != "." || statusReport.AutoRefresh.Reason != "missing_index" || statusReport.AutoRefresh.FilesIndexed != 1 || !statusReport.AutoRefresh.FreshAfter {
		t.Fatalf("expected missing root index refresh, got %#v", statusReport)
	}
}

func TestContextStatusRefreshRebuildsCollectorMismatchWithoutScope(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, filepath.Join(root, "go.mod"), "module example.com/statuscollectors\n\ngo 1.22\n")
	writeWorkspaceFile(t, filepath.Join(root, "pkg", "pkg.go"), "package pkg\n\nfunc Run() string { return \"ok\" }\n")
	mgr := NewManager(config.Workspace{Root: root, CWD: root})

	refresh := mgr.contextStatusAutoRefresh(context.Background(), ToolRequest{Tool: "context_status", Refresh: true, Language: "go"}, comprehension.StatusReport{
		Indexed:                  true,
		CollectorVersionMismatch: true,
	})
	if refresh == nil || !refresh.Attempted || refresh.Scope != "." || refresh.Reason != "collector_version_mismatch" || refresh.FilesIndexed != 1 || !refresh.FreshAfter {
		t.Fatalf("expected collector mismatch root rebuild, got %#v", refresh)
	}
	status, err := comprehension.Status(root)
	if err != nil {
		t.Fatalf("context status after refresh: %v", err)
	}
	if !status.Fresh || status.CollectorVersionMismatch {
		t.Fatalf("expected fresh graph after collector mismatch rebuild, got %#v", status)
	}
}

func TestContextStatusRefreshRemovesSmallMissingFileSetWithoutScope(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, filepath.Join(root, "go.mod"), "module example.com/statusmissing\n\ngo 1.22\n")
	missingPath := filepath.Join(root, "pkg", "gone.go")
	writeWorkspaceFile(t, missingPath, "package pkg\n\nfunc Gone() string { return \"gone\" }\n")
	writeWorkspaceFile(t, filepath.Join(root, "pkg", "keep.go"), "package pkg\n\nfunc Keep() string { return \"keep\" }\n")
	mgr := NewManager(config.Workspace{Root: root, CWD: root})

	result := mgr.Execute(context.Background(), ToolRequest{Tool: "context_index", Language: "go"})
	if result.Error != "" {
		t.Fatalf("context_index returned error: %v\n%s", result.Error, result.Output)
	}
	if err := os.Remove(missingPath); err != nil {
		t.Fatalf("remove stale file: %v", err)
	}

	result = mgr.Execute(context.Background(), ToolRequest{Tool: "context_status", Refresh: true, Language: "go"})
	if result.Error != "" {
		t.Fatalf("context_status returned error: %v\n%s", result.Error, result.Output)
	}
	var statusReport struct {
		Fresh        bool     `json:"fresh"`
		MissingFiles []string `json:"missing_files"`
		AutoRefresh  struct {
			Attempted      bool     `json:"attempted"`
			Reason         string   `json:"reason"`
			FreshAfter     bool     `json:"fresh_after"`
			RefreshedFiles []string `json:"refreshed_files"`
		} `json:"auto_refresh"`
	}
	if err := json.Unmarshal([]byte(result.Output), &statusReport); err != nil {
		t.Fatalf("decode context_status output: %v\n%s", err, result.Output)
	}
	if !statusReport.AutoRefresh.Attempted || statusReport.AutoRefresh.Reason != "small_stale_file_set" || !statusReport.AutoRefresh.FreshAfter {
		t.Fatalf("expected capped missing-file status refresh, got %#v", statusReport.AutoRefresh)
	}
	if !statusReport.Fresh || len(statusReport.MissingFiles) != 0 || !slices.Contains(statusReport.AutoRefresh.RefreshedFiles, "pkg/gone.go") {
		t.Fatalf("expected fresh status after missing-file refresh, got %#v", statusReport)
	}
}

func TestContextQueryAutoRefreshesMissingBoundedIndex(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, filepath.Join(root, "go.mod"), "module example.com/autorefresh-query\n\ngo 1.22\n")
	writeWorkspaceFile(t, filepath.Join(root, "pkg", "pkg.go"), "package pkg\n\nfunc Run() string { return \"ok\" }\n")
	mgr := NewManager(config.Workspace{Root: root, CWD: root})

	result := mgr.Execute(context.Background(), ToolRequest{Tool: "context_query", Pattern: "Run", Path: "pkg", Language: "go", Resolution: "symbol"})
	if result.Error != "" {
		t.Fatalf("context_query returned error: %v\n%s", result.Error, result.Output)
	}
	var queryReport struct {
		AutoRefresh struct {
			Attempted    bool   `json:"attempted"`
			Scope        string `json:"scope"`
			Reason       string `json:"reason"`
			FilesIndexed int    `json:"files_indexed"`
		} `json:"auto_refresh"`
		Nodes []struct {
			ID string `json:"id"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal([]byte(result.Output), &queryReport); err != nil {
		t.Fatalf("decode context_query output: %v\n%s", err, result.Output)
	}
	if !queryReport.AutoRefresh.Attempted || queryReport.AutoRefresh.Scope != "pkg" || queryReport.AutoRefresh.Reason != "missing_index" || queryReport.AutoRefresh.FilesIndexed != 1 {
		t.Fatalf("expected missing-index auto-refresh, got %#v", queryReport.AutoRefresh)
	}
	if !slices.ContainsFunc(queryReport.Nodes, func(node struct {
		ID string `json:"id"`
	}) bool {
		return node.ID == "symbol://pkg/pkg.go#Run"
	}) {
		t.Fatalf("expected Run node after auto-refresh, got %#v", queryReport.Nodes)
	}
}

func TestContextTracePackCapturesFailingCommandAsEvidence(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, filepath.Join(root, "app.py"), "def run():\n    raise AssertionError('bad')\n")
	cfg := config.Default().Workspace
	cfg.Root = root
	cfg.CWD = root
	mgr := NewManager(cfg)

	command := "printf '%s\\n' 'Traceback (most recent call last):' '  File \"" + filepath.ToSlash(filepath.Join(root, "app.py")) + "\", line 2, in run' 'AssertionError: bad' >&2; exit 1"
	result := mgr.Execute(context.Background(), ToolRequest{Tool: "context_trace_pack", Command: command, Language: "auto"})
	if result.Error != "" {
		t.Fatalf("context_trace_pack should return evidence for failing commands, got error: %v\n%s", result.Error, result.Output)
	}
	var trace struct {
		Success        bool   `json:"success"`
		ArtifactPath   string `json:"artifact_path"`
		FailingAnchors []struct {
			Path string `json:"path"`
			Line int    `json:"line"`
		} `json:"failing_anchors"`
	}
	if err := json.Unmarshal([]byte(result.Output), &trace); err != nil {
		t.Fatalf("decode context_trace_pack output: %v\n%s", err, result.Output)
	}
	if trace.Success || trace.ArtifactPath == "" || !slices.ContainsFunc(trace.FailingAnchors, func(anchor struct {
		Path string `json:"path"`
		Line int    `json:"line"`
	}) bool {
		return anchor.Path == "app.py" && anchor.Line == 2
	}) {
		t.Fatalf("expected saved failing trace with app.py anchor, got %#v", trace)
	}
}

func TestReadToolSchemasAdvertisePostReadinessGapFields(t *testing.T) {
	defs := BuildToolDefinitions([]string{"search_content", "read_file"})
	for _, want := range []string{"search_content", "read_file"} {
		var matched bool
		for _, def := range defs {
			if def.Name != want {
				continue
			}
			matched = true
			var schema struct {
				Properties map[string]json.RawMessage `json:"properties"`
			}
			if err := json.Unmarshal([]byte(def.ParametersJSON), &schema); err != nil {
				t.Fatalf("unmarshal schema for %s: %v", want, err)
			}
			if _, ok := schema.Properties["named_gap"]; !ok {
				t.Fatalf("expected %s schema to advertise named_gap, got %#v", want, schema.Properties)
			}
			if _, ok := schema.Properties["why_not_answering"]; !ok {
				t.Fatalf("expected %s schema to advertise why_not_answering, got %#v", want, schema.Properties)
			}
		}
		if !matched {
			t.Fatalf("missing tool definition for %s", want)
		}
	}
}

func TestEditFileSchemaAdvertisesBothReplacementShapes(t *testing.T) {
	defs := BuildToolDefinitions([]string{"edit_file"})
	if len(defs) != 1 {
		t.Fatalf("expected one edit_file definition, got %#v", defs)
	}

	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	if err := json.Unmarshal([]byte(defs[0].ParametersJSON), &schema); err != nil {
		t.Fatalf("unmarshal edit_file schema: %v", err)
	}

	for _, want := range []string{"path", "old_text", "new_text", "start_line", "end_line", "content", "description"} {
		if _, ok := schema.Properties[want]; !ok {
			t.Fatalf("expected edit_file schema to expose %q, got %#v", want, schema.Properties)
		}
	}
	for _, want := range []string{"path", "description"} {
		if !slices.Contains(schema.Required, want) {
			t.Fatalf("expected edit_file schema to require %q, got %#v", want, schema.Required)
		}
	}
	for _, invalid := range []string{"content", "old_text", "new_text", "start_line", "end_line"} {
		if slices.Contains(schema.Required, invalid) {
			t.Fatalf("edit_file schema should not require shape-specific field %q, got %#v", invalid, schema.Required)
		}
	}
	if !strings.Contains(defs[0].Description, "old_text+new_text") || !strings.Contains(defs[0].Description, "start_line+end_line+content") {
		t.Fatalf("edit_file description should name both valid shapes, got %q", defs[0].Description)
	}
}

func TestHarnessAuditReportsToolSurfaceAndLanguageCapabilities(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, filepath.Join(root, "go.mod"), "module example.com/audit\n\ngo 1.22\n")
	writeWorkspaceFile(t, filepath.Join(root, "main.go"), "package main\n\nfunc main() {}\n")
	mgr := NewManager(config.Workspace{Root: root, CWD: root})

	if result := mgr.Execute(context.Background(), ToolRequest{Tool: "context_index", Language: "go"}); result.Error != "" {
		t.Fatalf("context_index returned error: %v\n%s", result.Error, result.Output)
	}
	result := mgr.Execute(context.Background(), ToolRequest{Tool: "harness_audit", Purpose: "tool_surface"})
	if result.Error != "" {
		t.Fatalf("harness_audit returned error: %v\n%s", result.Error, result.Output)
	}
	var report harnessAuditReport
	if err := json.Unmarshal([]byte(result.Output), &report); err != nil {
		t.Fatalf("decode audit report: %v\n%s", err, result.Output)
	}
	if report.ToolSurface == nil || report.ToolSurface.ManifestToolCount == 0 {
		t.Fatalf("expected manifest tool count, got %#v", report.ToolSurface)
	}
	if len(report.ToolSurface.ManifestWithoutSchema) != 0 {
		t.Fatalf("manifest tools missing schemas: %#v", report.ToolSurface.ManifestWithoutSchema)
	}
	if !slices.ContainsFunc(report.LanguageTooling, func(lang harnessAuditLanguage) bool {
		return lang.Language == "go" && lang.Active && slices.ContainsFunc(lang.Capabilities, func(tool harnessAuditLanguageTool) bool {
			return tool.Tool == "go_workspace_symbol" && tool.InManifest && tool.HasSchema
		})
	}) {
		t.Fatalf("expected active Go language tooling with go_workspace_symbol, got %#v", report.LanguageTooling)
	}
	if !slices.ContainsFunc(report.BackendAvailability, func(backend harnessAuditBackend) bool {
		return backend.Name == "context_graph_sqlite" && backend.Active && backend.Available && strings.Contains(backend.Path, "graph.sqlite")
	}) {
		t.Fatalf("expected harness audit to report active SQLite context graph backend, got %#v", report.BackendAvailability)
	}
}

func TestHarnessAuditBehaviorScansLogs(t *testing.T) {
	root := t.TempDir()
	logPath := filepath.Join(root, "logs", "conversations", "session.jsonl")
	writeWorkspaceFile(t, logPath, strings.Join([]string{
		`{"type":"message","role":"user","content":"find missing code"}`,
		`{"type":"tool_request","tool":"search_files","action":"search_files:_:Nope:_","raw":"{\"tool\":\"search_files\",\"pattern\":\"Nope\"}"}`,
		`{"type":"tool_result","tool":"search_files","summary":"No file matches for \"Nope\"","error":""}`,
		"",
	}, "\n"))
	mgr := NewManager(config.Workspace{Root: root, CWD: root})

	result := mgr.Execute(context.Background(), ToolRequest{Tool: "harness_audit", Purpose: "behavior", Path: "logs/conversations", Limit: 5})
	if result.Error != "" {
		t.Fatalf("harness_audit returned error: %v\n%s", result.Error, result.Output)
	}
	var report harnessAuditReport
	if err := json.Unmarshal([]byte(result.Output), &report); err != nil {
		t.Fatalf("decode audit report: %v\n%s", err, result.Output)
	}
	if report.LogBehavior == nil || report.LogBehavior.LogsScanned != 1 {
		t.Fatalf("expected one scanned log, got %#v", report.LogBehavior)
	}
	if !slices.ContainsFunc(report.LogBehavior.TopPatterns, func(hit eval.PerfPatternHit) bool {
		return hit.Name == "zero_hit_search" && hit.TotalCount == 1
	}) {
		t.Fatalf("expected zero_hit_search pattern, got %#v", report.LogBehavior.TopPatterns)
	}
}

func TestHarnessFeedbackReturnsStructuredTelemetryFields(t *testing.T) {
	root := t.TempDir()
	mgr := NewManager(config.Workspace{Root: root, CWD: root})

	result := mgr.Execute(context.Background(), ToolRequest{
		Tool:         "harness_feedback",
		Category:     "prompt_confusion",
		Component:    "context_pack",
		Expected:     "packet names the symbol edge needed to choose the edit target",
		Observed:     "packet named both files but not the deciding relationship",
		Workaround:   "used python_references before editing",
		Impact:       "extra read loop before a bounded edit",
		Evidence:     []string{"logs/conversations/session.jsonl step 8"},
		Confidence:   "observed",
		Severity:     "medium",
		SuggestedFix: "carry the top relationship edge into the packet when confidence is high",
	})
	if result.Error != "" {
		t.Fatalf("harness_feedback returned error: %v\n%s", result.Error, result.Output)
	}
	var report harnessFeedbackReport
	if err := json.Unmarshal([]byte(result.Output), &report); err != nil {
		t.Fatalf("decode feedback report: %v\n%s", err, result.Output)
	}
	if report.Category != "prompt_gap" || report.Confidence != "verified" || report.Severity != "medium" {
		t.Fatalf("unexpected normalized feedback report: %#v", report)
	}
	fields := ToolTelemetryFields(result.Tool, result.Output)
	if fields["feedback_category"] != "prompt_gap" || fields["feedback_component"] != "context_pack" || fields["feedback_confidence"] != "verified" || fields["feedback_severity"] != "medium" || fields["feedback_evidence_count"] != 1 {
		t.Fatalf("unexpected feedback telemetry fields: %#v", fields)
	}
}

func TestHarnessFeedbackRejectsUnstructuredReports(t *testing.T) {
	root := t.TempDir()
	mgr := NewManager(config.Workspace{Root: root, CWD: root})

	result := mgr.Execute(context.Background(), ToolRequest{
		Tool:      "harness_feedback",
		Category:  "tool_gap",
		Component: "context_pack",
		Observed:  "missing expected/impact",
	})
	if result.Error == "" {
		t.Fatalf("expected harness_feedback to reject incomplete reports, got %#v", result)
	}
}

func TestFileStatsCountsFileAndDirectoryScopes(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, filepath.Join(root, "a.go"), "package main\n\nfunc main() {}\n")
	writeWorkspaceFile(t, filepath.Join(root, "nested", "b.go"), "package nested\n")
	writeWorkspaceFile(t, filepath.Join(root, "nested", "note.txt"), "hello\nworld\n")
	mgr := NewManager(config.Workspace{Root: root, CWD: root})

	result := mgr.Execute(context.Background(), ToolRequest{Tool: "file_stats", Path: "a.go"})
	if result.Error != "" {
		t.Fatalf("file_stats file returned error: %v", result.Error)
	}
	var fileReport fileStatsReport
	if err := json.Unmarshal([]byte(result.Output), &fileReport); err != nil {
		t.Fatalf("decode file stats report: %v\n%s", err, result.Output)
	}
	if fileReport.Files != 1 || fileReport.Lines != 3 || fileReport.Bytes == 0 {
		t.Fatalf("unexpected file stats: %#v", fileReport)
	}

	result = mgr.Execute(context.Background(), ToolRequest{Tool: "file_stats", Path: ".", Recursive: true, Glob: "*.go", MaxDepth: 2})
	if result.Error != "" {
		t.Fatalf("file_stats directory returned error: %v", result.Error)
	}
	var dirReport fileStatsReport
	if err := json.Unmarshal([]byte(result.Output), &dirReport); err != nil {
		t.Fatalf("decode dir stats report: %v\n%s", err, result.Output)
	}
	if dirReport.Files != 2 || dirReport.Lines != 4 {
		t.Fatalf("unexpected directory stats: %#v", dirReport)
	}
}

func TestHarnessAuditBehaviorDefaultIncludesTelemetryLogs(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{
		filepath.Join(root, "logs", "conversations", "session.jsonl"),
		filepath.Join(root, "logs", "telemetry", "tool.jsonl"),
	} {
		lines := []string{
			`{"type":"tool_request","tool":"pwd","action":"pwd"}`,
			`{"type":"tool_result","tool":"pwd","summary":"Printed current working directory","error":""}`,
		}
		if strings.Contains(path, "telemetry") {
			lines = append(lines,
				`{"type":"tool_request","tool":"context_status","action":"context_status","discovery_class":"context_status"}`,
				`{"type":"tool_result","tool":"context_status","summary":"Context index is fresh","error":"","context_store_kind":"sqlite-graph-v1","context_active_backend":"sqlite-graph-v1","context_active_backend_status":"active","context_storage_backends":["json-graph-v1:export","sqlite-graph-v1:active"],"context_indexed":true,"context_fresh":true,"context_files_indexed":2,"context_symbols_indexed":5,"context_edges_indexed":8}`,
				`{"type":"tool_request","tool":"harness_feedback","action":"harness_feedback","discovery_class":"harness_feedback"}`,
				`{"type":"tool_result","tool":"harness_feedback","summary":"Recorded tool_gap feedback for context_pack","error":"","feedback_category":"tool_gap","feedback_component":"context_pack","feedback_confidence":"verified","feedback_severity":"high","feedback_evidence_count":2}`,
			)
		}
		lines = append(lines, "")
		writeWorkspaceFile(t, path, strings.Join(lines, "\n"))
	}
	mgr := NewManager(config.Workspace{Root: root, CWD: root})

	result := mgr.Execute(context.Background(), ToolRequest{Tool: "harness_audit", Purpose: "behavior", Limit: 5})
	if result.Error != "" {
		t.Fatalf("harness_audit returned error: %v\n%s", result.Error, result.Output)
	}
	var report harnessAuditReport
	if err := json.Unmarshal([]byte(result.Output), &report); err != nil {
		t.Fatalf("decode audit report: %v\n%s", err, result.Output)
	}
	if report.LogBehavior == nil || report.LogBehavior.LogsScanned != 2 {
		t.Fatalf("expected conversation and telemetry logs to be scanned, got %#v", report.LogBehavior)
	}
	if report.LogBehavior.Aggregate.Context == nil || report.LogBehavior.Aggregate.Context.StoreKindCounts["sqlite-graph-v1"] != 1 {
		t.Fatalf("expected audit analyzer to ingest context telemetry fields, got %#v", report.LogBehavior.Aggregate.Context)
	}
	if report.LogBehavior.Aggregate.Feedback == nil || report.LogBehavior.Aggregate.Feedback.Total != 1 || report.LogBehavior.Aggregate.Feedback.ByCategory["tool_gap"] != 1 || report.LogBehavior.Aggregate.Feedback.HighSeverity != 1 {
		t.Fatalf("expected audit analyzer to ingest harness feedback telemetry fields, got %#v", report.LogBehavior.Aggregate.Feedback)
	}
	if !slices.ContainsFunc(report.Findings, func(finding harnessAuditFinding) bool {
		return finding.ID == "agent_harness_feedback_reported" && finding.Severity == "high"
	}) {
		t.Fatalf("expected feedback finding, got %#v", report.Findings)
	}
}

func TestHarnessAuditFlagsIncompleteImplementLifecycle(t *testing.T) {
	root := t.TempDir()
	conversationPath := filepath.Join(root, ".idiom", "logs", "conversations", "session.jsonl")
	telemetryPath := filepath.Join(root, "logs", "telemetry", "implement.jsonl")
	writeWorkspaceFile(t, conversationPath, strings.Join([]string{
		`{"type":"session_start","workspace_root":"` + root + `"}`,
		`{"type":"message","role":"control_rails","content":"{\"contract\":\"implementation_result\",\"task_family\":\"implementation\"}"}`,
		"",
	}, "\n"))
	writeWorkspaceFile(t, telemetryPath, strings.Join([]string{
		`{"type":"session_start","command":"implement","workspace_root":"` + root + `"}`,
		`{"type":"process_start","command":"implement","pid":123,"workspace_root":"` + root + `"}`,
		`{"type":"model_session","session_id":"session","debug_log_path":"` + conversationPath + `"}`,
		"",
	}, "\n"))
	mgr := NewManager(config.Workspace{Root: root, CWD: root})

	result := mgr.Execute(context.Background(), ToolRequest{Tool: "harness_audit", Purpose: "behavior", Path: "logs/telemetry", Limit: 5})
	if result.Error != "" {
		t.Fatalf("harness_audit returned error: %v\n%s", result.Error, result.Output)
	}
	var report harnessAuditReport
	if err := json.Unmarshal([]byte(result.Output), &report); err != nil {
		t.Fatalf("decode audit report: %v\n%s", err, result.Output)
	}
	if report.LogBehavior == nil || len(report.LogBehavior.IncompleteSessions) != 1 {
		t.Fatalf("expected one incomplete session, got %#v", report.LogBehavior)
	}
	session := report.LogBehavior.IncompleteSessions[0]
	if session.TelemetryLog != "logs/telemetry/implement.jsonl" ||
		session.ConversationLog != ".idiom/logs/conversations/session.jsonl" ||
		!slices.Contains(session.Missing, "process_exit") ||
		!slices.Contains(session.Missing, "session_summary") {
		t.Fatalf("unexpected incomplete lifecycle session: %#v", session)
	}
	if !slices.ContainsFunc(report.Findings, func(finding harnessAuditFinding) bool {
		return finding.ID == "incomplete_implement_session_lifecycle" && finding.Severity == "high"
	}) {
		t.Fatalf("expected incomplete lifecycle finding, got %#v", report.Findings)
	}
}

func TestContextTelemetryFieldsIncludeCollectorReports(t *testing.T) {
	fields := ContextTelemetryFields("context_index", `{
  "store_kind": "sqlite-graph-v1",
  "collector_version": "old",
  "current_collector_version": "new",
  "collector_version_mismatch": true,
  "incremental": true,
  "reused_indexed_files": 5,
  "expanded_scope": "pkg",
  "dependency_refresh_files": ["pkg/a.go", "pkg/b.go"],
  "collector_coverage": ["python_index", "language_environment"],
  "stale_symbol_samples": [{"id": "symbol://app.py#run"}],
  "refreshed_stale_symbols": 1,
  "nodes": [
    {"id": "summary://repository", "kind": "summary", "properties": {"model_authored": false}},
    {"id": "summary://package/pkg", "kind": "summary", "properties": {"model_authored": true}}
  ],
  "collector_reports": [
    {"name": "python_lsp", "available": false, "warnings": ["missing"]},
    {"name": "workspace_tool_enrichment", "available": true, "diagnostics": 2, "properties": {"normalized_relationship_edges": 3}}
  ]
}`)
	if fields["context_collector_reports"] != 2 || fields["context_collector_unavailable"] != 1 || fields["context_collector_warnings"] != 1 || fields["context_enrichment_diagnostics"] != 2 || fields["context_normalized_relationship_edges"] != 3 {
		t.Fatalf("unexpected collector telemetry fields: %#v", fields)
	}
	if fields["context_collector_coverage"] != 2 || fields["context_stale_symbol_samples"] != 1 || fields["context_refreshed_stale_symbols"] != 1 {
		t.Fatalf("unexpected collector coverage/stale telemetry fields: %#v", fields)
	}
	if fields["context_incremental"] != true || fields["context_reused_indexed_files"] != 5 || fields["context_dependency_refresh_files"] != 2 || fields["context_summary_nodes"] != 2 || fields["context_model_summary_nodes"] != 1 || fields["context_expanded_scope"] != "pkg" {
		t.Fatalf("unexpected incremental/summary telemetry fields: %#v", fields)
	}
	if fields["context_collector_version"] != "old" || fields["context_current_collector_version"] != "new" || fields["context_collector_version_mismatch"] != true {
		t.Fatalf("unexpected collector version telemetry fields: %#v", fields)
	}
	names, ok := fields["context_collector_report_names"].([]string)
	if !ok || !slices.Contains(names, "python_lsp") || !slices.Contains(names, "workspace_tool_enrichment") {
		t.Fatalf("missing collector report names: %#v", fields)
	}
	coverage, ok := fields["context_collector_coverage_names"].([]string)
	if !ok || !slices.Contains(coverage, "python_index") {
		t.Fatalf("missing collector coverage names: %#v", fields)
	}
}

func TestWorkspaceToolEnrichmentNormalizesRelationshipEdges(t *testing.T) {
	graph := comprehension.Graph{
		Root: "/workspace",
		Nodes: []comprehension.Node{
			{ID: "file://app.py", Kind: "file", Path: "app.py", Language: "python"},
			{ID: "file://lib.py", Kind: "file", Path: "lib.py", Language: "python"},
			{ID: "symbol://app.py#run", Kind: "func", Path: "app.py", Language: "python", QualifiedName: "run", StartLine: 1, EndLine: 3, Properties: map[string]any{"name": "run"}},
			{ID: "symbol://lib.py#helper", Kind: "func", Path: "lib.py", Language: "python", QualifiedName: "helper", StartLine: 1, EndLine: 1, Properties: map[string]any{"name": "helper"}},
			{ID: "symbol://lib.py#caller", Kind: "func", Path: "lib.py", Language: "python", QualifiedName: "caller", StartLine: 3, EndLine: 5, Properties: map[string]any{"name": "caller"}},
		},
	}
	lookup := newGraphRelationshipLookup(graph)
	subject := graph.Nodes[2]

	outgoing := normalizeToolRelationshipEdges(graph, lookup, subject, "calls_from", "python_calls_from", ToolResult{Output: "app.py:2 -> helper in lib.py:1", PrecisionTier: PrecisionTierLSP})
	if !hasWorkspaceRelationshipEdge(outgoing, "calls", "symbol://app.py#run", "symbol://lib.py#helper") {
		t.Fatalf("expected normalized outgoing call edge, got %#v", outgoing)
	}

	incoming := normalizeToolRelationshipEdges(graph, lookup, graph.Nodes[3], "calls_into", "python_calls_into", ToolResult{Output: "lib.py:3 caller", PrecisionTier: PrecisionTierSyntaxIndex})
	if !hasWorkspaceRelationshipEdge(incoming, "calls", "symbol://lib.py#caller", "symbol://lib.py#helper") {
		t.Fatalf("expected normalized incoming call edge, got %#v", incoming)
	}

	references := normalizeToolRelationshipEdges(graph, lookup, graph.Nodes[3], "references", "python_references", ToolResult{Output: "lib.py:3:5 caller()", PrecisionTier: PrecisionTierSyntaxIndex})
	if !hasWorkspaceRelationshipEdge(references, "references", "symbol://lib.py#caller", "symbol://lib.py#helper") {
		t.Fatalf("expected normalized reference edge, got %#v", references)
	}

	typed := normalizeToolRelationshipEdges(graph, lookup, subject, "calls_from", "python_calls_from", ToolResult{
		PrecisionTier: PrecisionTierLSP,
		Relationships: []ToolRelationship{{
			Kind:         "calls",
			SourcePath:   "app.py",
			SourceLine:   2,
			SourceSymbol: "run",
			TargetPath:   "lib.py",
			TargetLine:   1,
			TargetSymbol: "helper",
			Evidence:     "typed relationship payload",
		}},
	})
	if !hasWorkspaceRelationshipEdge(typed, "calls", "symbol://app.py#run", "symbol://lib.py#helper") {
		t.Fatalf("expected structured relationship payload to normalize, got %#v", typed)
	}
}

func TestLanguageToolResultsExposeStructuredRelationships(t *testing.T) {
	result := annotateLanguageRelationships(ToolResult{
		Tool:   "python_calls_from",
		Output: "app.py:2 -> helper in lib.py:1",
	})
	if len(result.Relationships) != 1 {
		t.Fatalf("expected parsed relationship payload, got %#v", result)
	}
	relationship := result.Relationships[0]
	if relationship.Kind != "calls" || relationship.SourcePath != "app.py" || relationship.SourceLine != 2 || relationship.TargetPath != "lib.py" || relationship.TargetLine != 1 || relationship.TargetSymbol != "helper" {
		t.Fatalf("unexpected relationship payload: %#v", relationship)
	}
}

func TestLSPCallHierarchyFormattersReturnStructuredRelationships(t *testing.T) {
	root := t.TempDir()
	app := filepath.Join(root, "app.py")
	lib := filepath.Join(root, "lib.py")
	writeWorkspaceFile(t, app, "def run():\n    return helper()\n")
	writeWorkspaceFile(t, lib, "def helper():\n    return 'ok'\n")
	mgr := NewManager(config.Workspace{Root: root, CWD: root, MaxDirectoryEntries: 16})

	payload := mgr.formatPylspOutgoingCalls(app, "run", []pylspOutgoingCall{{
		To: pylspCallHierarchyItem{
			Name: "helper",
			URI:  pylspFileURI(lib),
			SelectionRange: pylspRange{
				Start: pylspPosition{Line: 0, Character: 4},
			},
		},
		FromRanges: []pylspRange{{
			Start: pylspPosition{Line: 1, Character: 11},
		}},
	}}, "")
	if strings.TrimSpace(payload.Output) == "" || len(payload.Relationships) != 1 {
		t.Fatalf("expected output and structured relationship payload, got %#v", payload)
	}
	relationship := payload.Relationships[0]
	if relationship.SourcePath != "app.py" || relationship.SourceLine != 2 || relationship.SourceSymbol != "run" || relationship.TargetPath != "lib.py" || relationship.TargetLine != 1 || relationship.TargetSymbol != "helper" {
		t.Fatalf("unexpected LSP relationship payload: %#v", relationship)
	}
}

func TestDependencyGraphExtractsTypeScriptImportsWithoutMadge(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, filepath.Join(root, "src", "main.ts"), `import {x} from "./lib";
const fs = require("fs");
export { y } from "./other";
`)
	writeWorkspaceFile(t, filepath.Join(root, "src", "lib.ts"), `export const x = 1;`)

	mgr := NewManager(config.Workspace{
		Root:                  root,
		CWD:                   root,
		CommandTimeoutSeconds: 30,
		MaxOutputBytes:        65536,
		MaxFileBytes:          65536,
		EnableShell:           true,
		EnableWrites:          true,
	})
	target := filepath.Join(root, "src")
	result := mgr.regexDependencyGraph(target, "typescript", []string{".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs"}, tsImportRe)
	if result.Error != "" {
		t.Fatalf("dependency_graph returned error: %v\n%s", result.Error, result.Output)
	}
	for _, want := range []string{"src/main.ts -> ./lib", "src/main.ts -> ./other", "src/main.ts -> fs"} {
		if !strings.Contains(result.Output, want) {
			t.Fatalf("expected dependency graph to include %q, got:\n%s", want, result.Output)
		}
	}
}

func TestDependencyGraphExtractsPythonImportsWithoutPydeps(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, filepath.Join(root, "pkg", "main.py"), `import os, sys
from pkg.helpers import run
`)

	mgr := NewManager(config.Workspace{
		Root:                  root,
		CWD:                   root,
		CommandTimeoutSeconds: 30,
		MaxOutputBytes:        65536,
		MaxFileBytes:          65536,
		EnableShell:           true,
		EnableWrites:          true,
	})
	target := filepath.Join(root, "pkg")
	result := mgr.regexDependencyGraph(target, "python", []string{".py", ".pyi"}, pythonImportRe)
	if result.Error != "" {
		t.Fatalf("dependency_graph returned error: %v\n%s", result.Error, result.Output)
	}
	for _, want := range []string{"pkg/main.py -> os", "pkg/main.py -> sys", "pkg/main.py -> pkg.helpers"} {
		if !strings.Contains(result.Output, want) {
			t.Fatalf("expected dependency graph to include %q, got:\n%s", want, result.Output)
		}
	}
}

func TestHistoryAndBlameToolsUseGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	root := t.TempDir()
	runTestCommand(t, root, "git", "init")
	runTestCommand(t, root, "git", "config", "user.email", "test@example.com")
	runTestCommand(t, root, "git", "config", "user.name", "Test User")
	writeWorkspaceFile(t, filepath.Join(root, "sample.go"), "package main\n\nfunc add(a int, b int) int {\n\treturn a + b\n}\n")
	runTestCommand(t, root, "git", "add", "sample.go")
	runTestCommand(t, root, "git", "commit", "-m", "initial")
	writeWorkspaceFile(t, filepath.Join(root, "sample.go"), "package main\n\nfunc add(a int, b int) int {\n\treturn a - b\n}\n")
	runTestCommand(t, root, "git", "commit", "-am", "change add")

	mgr := NewManager(config.Workspace{
		Root:                  root,
		CWD:                   root,
		CommandTimeoutSeconds: 30,
		MaxOutputBytes:        65536,
		MaxFileBytes:          65536,
		EnableShell:           true,
		EnableWrites:          true,
	})

	history := mgr.Execute(context.Background(), ToolRequest{Tool: "history_range", Path: "sample.go", StartLine: 3, EndLine: 5})
	if history.Error != "" {
		t.Fatalf("history_range returned error: %v\n%s", history.Error, history.Output)
	}
	if !strings.Contains(history.Output, "change add") || !strings.Contains(history.Output, "initial") {
		t.Fatalf("expected git log -L output to include both commits, got:\n%s", history.Output)
	}

	symbolHistory := mgr.Execute(context.Background(), ToolRequest{Tool: "history_symbol", Path: "sample.go", Pattern: "add"})
	if symbolHistory.Error != "" {
		t.Fatalf("history_symbol returned error: %v\n%s", symbolHistory.Error, symbolHistory.Output)
	}
	if !strings.Contains(symbolHistory.Output, "change add") || !strings.Contains(symbolHistory.Output, "initial") {
		t.Fatalf("expected git log -L symbol output to include both commits, got:\n%s", symbolHistory.Output)
	}

	blame := mgr.Execute(context.Background(), ToolRequest{Tool: "blame_range", Path: "sample.go", StartLine: 4, EndLine: 4})
	if blame.Error != "" {
		t.Fatalf("blame_range returned error: %v\n%s", blame.Error, blame.Output)
	}
	if !strings.Contains(blame.Output, "return a - b") {
		t.Fatalf("expected blame output for changed line, got:\n%s", blame.Output)
	}
}

func TestStructuralSearchRejectsMissingPattern(t *testing.T) {
	mgr := NewManager(testWorkspaceConfig())
	result := mgr.Execute(context.Background(), ToolRequest{Tool: "structural_search", Path: "."})
	if result.Error == "" || !strings.Contains(result.Error, "pattern is required") {
		t.Fatalf("expected missing-pattern error, got %#v", result)
	}
}

func TestOptionalExternalDiscoveryToolsReportUnavailable(t *testing.T) {
	t.Setenv("PATH", "")
	mgr := NewManager(testWorkspaceConfig())

	for _, req := range []ToolRequest{
		{Tool: "structural_search", Path: ".", Pattern: "$X"},
		{Tool: "ctags_symbols", Path: "."},
		{Tool: "semgrep_scan", Path: "."},
		{Tool: "joern_scan", Path: "."},
	} {
		result := mgr.Execute(context.Background(), req)
		if result.Error == "" || !strings.Contains(result.Summary, "unavailable") {
			t.Fatalf("expected %s to report unavailable, got %#v", req.Tool, result)
		}
	}
}

func writeWorkspaceFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile %s: %v", path, err)
	}
}

func runTestCommand(t *testing.T, dir string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s failed: %v\n%s", name, strings.Join(args, " "), err, string(output))
	}
}

func hasWorkspaceRelationshipEdge(edges []comprehension.Edge, kind string, sourceID string, targetID string) bool {
	for _, edge := range edges {
		if edge.Kind == kind && edge.SourceID == sourceID && edge.TargetID == targetID {
			return true
		}
	}
	return false
}
