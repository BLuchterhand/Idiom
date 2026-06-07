package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"idiom/internal/contracts/autonomy"
	"idiom/internal/evidence/eval"
	"idiom/internal/platform/config"
	"idiom/internal/repository/comprehension"
)

func withWorkspaceGateAuditFields(t *testing.T, gate autonomy.ContractGateEvent, packID, enforcementMode string, observedFacts, safeAlternatives []string) autonomy.ContractGateEvent {
	t.Helper()
	value := reflect.ValueOf(&gate).Elem()
	setWorkspaceGateStringField(t, value, "PackID", packID)
	setWorkspaceGateStringField(t, value, "EnforcementMode", enforcementMode)
	setWorkspaceGateStringSliceField(t, value, "ObservedFacts", observedFacts)
	setWorkspaceGateStringSliceField(t, value, "SafeAlternatives", safeAlternatives)
	return gate
}

func setWorkspaceGateStringField(t *testing.T, value reflect.Value, name, fieldValue string) {
	t.Helper()
	field := value.FieldByName(name)
	if !field.IsValid() {
		return
	}
	if field.Kind() != reflect.String || !field.CanSet() {
		t.Fatalf("ContractGateEvent.%s is not a settable string field", name)
	}
	field.SetString(fieldValue)
}

func setWorkspaceGateStringSliceField(t *testing.T, value reflect.Value, name string, fieldValue []string) {
	t.Helper()
	field := value.FieldByName(name)
	if !field.IsValid() {
		return
	}
	if field.Kind() != reflect.Slice || field.Type().Elem().Kind() != reflect.String || !field.CanSet() {
		t.Fatalf("ContractGateEvent.%s is not a settable []string field", name)
	}
	slice := reflect.MakeSlice(field.Type(), len(fieldValue), len(fieldValue))
	for i, value := range fieldValue {
		slice.Index(i).SetString(value)
	}
	field.Set(slice)
}

func TestGenericDiscoveryToolsAreAdvertised(t *testing.T) {
	tools := ToolManifestTools(ToolManifestToolOptions{})
	for _, want := range []string{
		"harness_audit",
		"harness_feedback",
		"file_stats",
		"code_navigate",
		"read_only_capability",
		"source_sanity",
		"context_index",
		"context_status",
		"context_query",
		"context_zoom",
		"context_pack",
		"context_impact",
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
	for _, want := range []string{"harness_audit", "harness_feedback", "file_stats", "code_navigate", "read_only_capability", "source_sanity", "context_index", "context_status", "context_query", "context_zoom", "context_pack", "context_impact", "context_trace_pack", "discovery_query", "structural_search", "dependency_graph", "history_range", "blame_range"} {
		if !slices.Contains(names, want) {
			t.Fatalf("expected tool definitions to include %q, got %#v", want, names)
		}
	}
}

func TestContextImpactSchemaAllowsPathOnlyRequests(t *testing.T) {
	defs := BuildToolDefinitions([]string{"context_impact"})
	if len(defs) != 1 {
		t.Fatalf("expected one context_impact definition, got %#v", defs)
	}
	var schema struct {
		Required []string `json:"required"`
	}
	if err := json.Unmarshal([]byte(defs[0].ParametersJSON), &schema); err != nil {
		t.Fatalf("decode context_impact schema: %v\n%s", err, defs[0].ParametersJSON)
	}
	if slices.Contains(schema.Required, "task") {
		t.Fatalf("context_impact schema should allow path-only requests, required=%#v", schema.Required)
	}
}

func TestHistoryRangeSchemaAllowsPathOnlyRequests(t *testing.T) {
	defs := BuildToolDefinitions([]string{"history_range"})
	if len(defs) != 1 {
		t.Fatalf("expected one history_range definition, got %#v", defs)
	}
	var schema struct {
		Required []string `json:"required"`
	}
	if err := json.Unmarshal([]byte(defs[0].ParametersJSON), &schema); err != nil {
		t.Fatalf("decode history_range schema: %v\n%s", err, defs[0].ParametersJSON)
	}
	if !slices.Contains(schema.Required, "path") {
		t.Fatalf("history_range schema must require path, required=%#v", schema.Required)
	}
	for _, field := range []string{"start_line", "end_line"} {
		if slices.Contains(schema.Required, field) {
			t.Fatalf("history_range schema should allow path-only requests, required=%#v", schema.Required)
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

	result = mgr.Execute(context.Background(), ToolRequest{Tool: "context_impact", Task: "Plan a change to Run", RequiredTests: []string{"go test ./..."}})
	if result.Error != "" {
		t.Fatalf("context_impact returned error: %v\n%s", result.Error, result.Output)
	}
	var impactReport struct {
		ContextViewPath string `json:"context_view_path"`
		ImpactScope     []struct {
			Kind    string  `json:"kind"`
			Path    string  `json:"path"`
			Rank    float64 `json:"rank"`
			Signals []struct {
				Kind string `json:"kind"`
			} `json:"signals"`
		} `json:"impact_scope"`
	}
	if err := json.Unmarshal([]byte(result.Output), &impactReport); err != nil {
		t.Fatalf("decode context_impact output: %v\n%s", err, result.Output)
	}
	if impactReport.ContextViewPath == "" || !slices.ContainsFunc(impactReport.ImpactScope, func(item struct {
		Kind    string  `json:"kind"`
		Path    string  `json:"path"`
		Rank    float64 `json:"rank"`
		Signals []struct {
			Kind string `json:"kind"`
		} `json:"signals"`
	}) bool {
		return item.Kind == "file" && item.Path == "pkg/pkg.go" && item.Rank > 0 && len(item.Signals) > 0
	}) {
		t.Fatalf("expected context_impact ranking with file signal, got %#v", impactReport)
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

func TestContextImpactSupportsPathOnlyRequest(t *testing.T) {
	root := t.TempDir()
	targetPath := filepath.Join("internal", "comprehension", "pack.go")
	writeWorkspaceFile(t, filepath.Join(root, "go.mod"), "module example.com/pathimpact\n\ngo 1.22\n")
	writeWorkspaceFile(t, filepath.Join(root, targetPath), "package comprehension\n\nfunc Pack() string { return \"DO_NOT_LEAK_BODY\" }\n")
	mgr := NewManager(config.Workspace{Root: root, CWD: root})

	if result := mgr.Execute(context.Background(), ToolRequest{Tool: "context_index", Language: "go"}); result.Error != "" {
		t.Fatalf("context_index returned error: %v\n%s", result.Error, result.Output)
	}

	result := mgr.Execute(context.Background(), ToolRequest{Tool: "context_impact", Path: targetPath})
	if result.Error != "" {
		t.Fatalf("context_impact returned error: %v\n%s", result.Error, result.Output)
	}
	assertNoPrescriptiveGraphOutput(t, "context_impact path-only", result.Summary+"\n"+result.Output)
	if strings.Contains(result.Output, "DO_NOT_LEAK_BODY") {
		t.Fatalf("context_impact path-only output leaked raw source body:\n%s", result.Output)
	}

	var report struct {
		Task            string `json:"task"`
		ContextViewPath string `json:"context_view_path"`
		Confidence      struct {
			Level string `json:"level"`
		} `json:"confidence"`
		Provenance struct {
			Operation string `json:"operation"`
			IndexID   string `json:"index_id"`
		} `json:"provenance"`
		IndexID     string `json:"index_id"`
		ImpactScope []struct {
			Kind       string `json:"kind"`
			Path       string `json:"path"`
			Confidence string `json:"confidence"`
			Signals    []struct {
				Kind string `json:"kind"`
			} `json:"signals"`
		} `json:"impact_scope"`
	}
	if err := json.Unmarshal([]byte(result.Output), &report); err != nil {
		t.Fatalf("decode context_impact output: %v\n%s", err, result.Output)
	}
	if report.Task != "Graph-backed impact evidence for "+targetPath {
		t.Fatalf("expected derived path evidence task, got %q", report.Task)
	}
	if report.ContextViewPath == "" || report.Confidence.Level != "high" || report.Provenance.Operation != "context_impact" || report.Provenance.IndexID != report.IndexID {
		t.Fatalf("expected structured graph metadata, got %#v", report)
	}
	if !slices.ContainsFunc(report.ImpactScope, func(item struct {
		Kind       string `json:"kind"`
		Path       string `json:"path"`
		Confidence string `json:"confidence"`
		Signals    []struct {
			Kind string `json:"kind"`
		} `json:"signals"`
	}) bool {
		return item.Kind == "file" && item.Path == targetPath && item.Confidence != "" && len(item.Signals) > 0
	}) {
		t.Fatalf("expected path-only impact scope for %s, got %#v", targetPath, report.ImpactScope)
	}
}

func TestContextStatusIncludesStructuredFreshnessConfidenceAndProvenance(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, filepath.Join(root, "go.mod"), "module example.com/statusmetadata\n\ngo 1.22\n")
	writeWorkspaceFile(t, filepath.Join(root, "pkg", "pkg.go"), "package pkg\n\nfunc Run() string { return \"ok\" }\n")
	mgr := NewManager(config.Workspace{Root: root, CWD: root})

	if result := mgr.Execute(context.Background(), ToolRequest{Tool: "context_index", Language: "go"}); result.Error != "" {
		t.Fatalf("context_index returned error: %v\n%s", result.Error, result.Output)
	}
	result := mgr.Execute(context.Background(), ToolRequest{Tool: "context_status"})
	if result.Error != "" {
		t.Fatalf("context_status returned error: %v\n%s", result.Error, result.Output)
	}
	var status struct {
		IndexID          string `json:"index_id"`
		StoreKind        string `json:"store_kind"`
		GeneratedAt      string `json:"generated_at"`
		Fresh            bool   `json:"fresh"`
		StaleFileCount   int    `json:"stale_file_count"`
		MissingFileCount int    `json:"missing_file_count"`
		StalePathCount   int    `json:"stale_path_count"`
		Confidence       struct {
			Level  string `json:"level"`
			Source string `json:"source"`
		} `json:"confidence"`
		Provenance struct {
			Source      string `json:"source"`
			Operation   string `json:"operation"`
			StoreKind   string `json:"store_kind"`
			IndexID     string `json:"index_id"`
			GeneratedAt string `json:"generated_at"`
		} `json:"provenance"`
		Freshness struct {
			Policy           string `json:"policy"`
			Operation        string `json:"operation"`
			IndexID          string `json:"index_id"`
			StoreKind        string `json:"store_kind"`
			GeneratedAt      string `json:"generated_at"`
			FreshForScope    bool   `json:"fresh_for_scope"`
			GlobalFresh      bool   `json:"global_fresh"`
			CheckedPathCount int    `json:"checked_path_count"`
			StalePathCount   int    `json:"stale_path_count"`
			MissingPathCount int    `json:"missing_path_count"`
		} `json:"freshness"`
	}
	if err := json.Unmarshal([]byte(result.Output), &status); err != nil {
		t.Fatalf("decode context_status output: %v\n%s", err, result.Output)
	}
	if !status.Fresh || status.IndexID == "" || status.StoreKind != "sqlite-graph-v1" || status.GeneratedAt == "" {
		t.Fatalf("expected fresh indexed status metadata, got %#v", status)
	}
	if status.StaleFileCount != 0 || status.MissingFileCount != 0 || status.StalePathCount != 0 {
		t.Fatalf("fresh status should report zero stale/missing counts, got %#v", status)
	}
	if status.Confidence.Level != "high" || status.Confidence.Source != "freshness_decision" {
		t.Fatalf("expected high confidence backed by freshness decision, got %#v", status.Confidence)
	}
	if status.Provenance.Source != "context_graph" || status.Provenance.Operation != "context_status" || status.Provenance.IndexID != status.IndexID || status.Provenance.GeneratedAt != status.GeneratedAt || status.Provenance.StoreKind != status.StoreKind {
		t.Fatalf("unexpected status provenance: %#v", status.Provenance)
	}
	if status.Freshness.Policy != "report_only" || status.Freshness.Operation != "context_status" || status.Freshness.IndexID != status.IndexID || status.Freshness.StoreKind != status.StoreKind || status.Freshness.GeneratedAt != status.GeneratedAt || !status.Freshness.FreshForScope || !status.Freshness.GlobalFresh {
		t.Fatalf("unexpected status freshness contract: %#v", status.Freshness)
	}
}

func TestContextStatusReportsStructuredStalePathCounts(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, filepath.Join(root, "go.mod"), "module example.com/statusstale\n\ngo 1.22\n")
	sourcePath := filepath.Join(root, "pkg", "pkg.go")
	writeWorkspaceFile(t, sourcePath, "package pkg\n\nfunc Run() string { return \"ok\" }\n")
	mgr := NewManager(config.Workspace{Root: root, CWD: root})

	if result := mgr.Execute(context.Background(), ToolRequest{Tool: "context_index", Language: "go"}); result.Error != "" {
		t.Fatalf("context_index returned error: %v\n%s", result.Error, result.Output)
	}
	writeWorkspaceFile(t, sourcePath, "package pkg\n\nfunc Run() string { return \"changed\" }\n")

	result := mgr.Execute(context.Background(), ToolRequest{Tool: "context_status"})
	if result.Error != "" {
		t.Fatalf("context_status returned error: %v\n%s", result.Error, result.Output)
	}
	var status struct {
		Fresh          bool     `json:"fresh"`
		StaleFiles     []string `json:"stale_files"`
		StaleFileCount int      `json:"stale_file_count"`
		StalePathCount int      `json:"stale_path_count"`
		Confidence     struct {
			Level  string `json:"level"`
			Source string `json:"source"`
			Reason string `json:"reason"`
		} `json:"confidence"`
		Freshness struct {
			FreshForScope  bool     `json:"fresh_for_scope"`
			GlobalFresh    bool     `json:"global_fresh"`
			Reason         string   `json:"reason"`
			StalePaths     []string `json:"stale_paths"`
			StalePathCount int      `json:"stale_path_count"`
		} `json:"freshness"`
	}
	if err := json.Unmarshal([]byte(result.Output), &status); err != nil {
		t.Fatalf("decode context_status output: %v\n%s", err, result.Output)
	}
	if status.Fresh || status.StaleFileCount != 1 || status.StalePathCount != 1 || !slices.Contains(status.StaleFiles, "pkg/pkg.go") {
		t.Fatalf("expected one stale path in status, got %#v", status)
	}
	if status.Freshness.FreshForScope || status.Freshness.GlobalFresh || status.Freshness.Reason != "stale_unbounded_graph" || status.Freshness.StalePathCount != 1 || !slices.Contains(status.Freshness.StalePaths, "pkg/pkg.go") {
		t.Fatalf("expected structured stale freshness fields, got %#v", status.Freshness)
	}
	if status.Confidence.Level != "low" || status.Confidence.Source != "freshness_decision" || status.Confidence.Reason != "stale_unbounded_graph" {
		t.Fatalf("expected low confidence from stale freshness decision, got %#v", status.Confidence)
	}
}

func TestGraphBackedOutputsCarryConfidenceProvenanceAndLegacyFields(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, filepath.Join(root, "go.mod"), "module example.com/graphoutputs\n\ngo 1.22\n")
	writeWorkspaceFile(t, filepath.Join(root, "pkg", "pkg.go"), "package pkg\n\nfunc Run() string { return helper() }\n\nfunc helper() string { return \"ok\" }\n")
	mgr := NewManager(config.Workspace{Root: root, CWD: root})

	if result := mgr.Execute(context.Background(), ToolRequest{Tool: "context_index", Language: "go"}); result.Error != "" {
		t.Fatalf("context_index returned error: %v\n%s", result.Error, result.Output)
	}

	query := mgr.Execute(context.Background(), ToolRequest{Tool: "context_query", Pattern: "Run", Resolution: "symbol", IncludeEvidence: true})
	if query.Error != "" {
		t.Fatalf("context_query returned error: %v\n%s", query.Error, query.Output)
	}
	var queryReport struct {
		IndexID     string `json:"index_id"`
		StoreKind   string `json:"store_kind"`
		GeneratedAt string `json:"generated_at"`
		Confidence  struct {
			Level string `json:"level"`
		} `json:"confidence"`
		Provenance struct {
			Source    string `json:"source"`
			Operation string `json:"operation"`
			IndexID   string `json:"index_id"`
		} `json:"provenance"`
		Nodes []struct {
			ID         string `json:"id"`
			Confidence string `json:"confidence"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal([]byte(query.Output), &queryReport); err != nil {
		t.Fatalf("decode context_query output: %v\n%s", err, query.Output)
	}
	if queryReport.IndexID == "" || queryReport.StoreKind != "sqlite-graph-v1" || queryReport.GeneratedAt == "" || queryReport.Confidence.Level != "high" || queryReport.Provenance.Source != "context_graph" || queryReport.Provenance.Operation != "context_query" || queryReport.Provenance.IndexID != queryReport.IndexID {
		t.Fatalf("expected query confidence/provenance metadata, got %#v", queryReport)
	}
	if !slices.ContainsFunc(queryReport.Nodes, func(node struct {
		ID         string `json:"id"`
		Confidence string `json:"confidence"`
	}) bool {
		return node.ID == "symbol://pkg/pkg.go#Run" && node.Confidence != ""
	}) {
		t.Fatalf("expected node-level confidence to remain present, got %#v", queryReport.Nodes)
	}

	zoom := mgr.Execute(context.Background(), ToolRequest{Tool: "context_zoom", Handle: "symbol://pkg/pkg.go#Run", Resolution: "symbol"})
	if zoom.Error != "" {
		t.Fatalf("context_zoom returned error: %v\n%s", zoom.Error, zoom.Output)
	}
	var zoomReport struct {
		IndexID    string `json:"index_id"`
		Confidence struct {
			Level string `json:"level"`
		} `json:"confidence"`
		Provenance struct {
			Operation string `json:"operation"`
			IndexID   string `json:"index_id"`
		} `json:"provenance"`
		Node *struct {
			ID string `json:"id"`
		} `json:"node"`
	}
	if err := json.Unmarshal([]byte(zoom.Output), &zoomReport); err != nil {
		t.Fatalf("decode context_zoom output: %v\n%s", err, zoom.Output)
	}
	if zoomReport.Node == nil || zoomReport.Node.ID != "symbol://pkg/pkg.go#Run" || zoomReport.Confidence.Level != "high" || zoomReport.Provenance.Operation != "context_zoom" || zoomReport.Provenance.IndexID != zoomReport.IndexID {
		t.Fatalf("expected zoom metadata and node, got %#v", zoomReport)
	}

	pack := mgr.Execute(context.Background(), ToolRequest{Tool: "context_pack", Task: "Plan a change to Run", RequiredTests: []string{"go test ./..."}})
	if pack.Error != "" {
		t.Fatalf("context_pack returned error: %v\n%s", pack.Error, pack.Output)
	}
	var packReport struct {
		ArtifactPath string `json:"artifact_path"`
		IndexID      string `json:"index_id"`
		Fresh        bool   `json:"fresh"`
		Confidence   struct {
			Level string `json:"level"`
		} `json:"confidence"`
		Provenance struct {
			Source       string `json:"source"`
			Operation    string `json:"operation"`
			IndexID      string `json:"index_id"`
			ArtifactPath string `json:"artifact_path"`
		} `json:"provenance"`
		Cache struct {
			State        string `json:"state"`
			Source       string `json:"source"`
			CacheKey     string `json:"cache_key"`
			Invalidation struct {
				Policy  string `json:"policy"`
				Sources []struct {
					Source string `json:"source"`
					State  string `json:"state"`
				} `json:"sources"`
			} `json:"invalidation"`
		} `json:"cache"`
		Contract struct {
			SchemaVersion string `json:"schema_version"`
			Cache         struct {
				State string `json:"state"`
			} `json:"cache"`
			UnresolvedGaps []struct {
				Reason string `json:"reason"`
				State  string `json:"state"`
			} `json:"unresolved_gaps"`
		} `json:"contract"`
		Freshness struct {
			IndexID       string `json:"index_id"`
			FreshForScope bool   `json:"fresh_for_scope"`
		} `json:"freshness"`
		CandidateFiles []struct {
			Path          string `json:"path"`
			Role          string `json:"role"`
			Confidence    string `json:"confidence"`
			RankerReasons []struct {
				Reason string `json:"reason"`
				State  string `json:"state"`
			} `json:"ranker_reasons"`
		} `json:"candidate_files"`
		Tests []struct {
			Command string `json:"command"`
		} `json:"tests"`
	}
	if err := json.Unmarshal([]byte(pack.Output), &packReport); err != nil {
		t.Fatalf("decode context_pack output: %v\n%s", err, pack.Output)
	}
	if packReport.ArtifactPath == "" || !packReport.Fresh || packReport.Confidence.Level != "high" || packReport.Provenance.Source != "context_graph" || packReport.Provenance.Operation != "context_pack" || packReport.Provenance.IndexID != packReport.IndexID || packReport.Provenance.ArtifactPath != packReport.ArtifactPath || packReport.Freshness.IndexID != packReport.IndexID || !packReport.Freshness.FreshForScope {
		t.Fatalf("expected pack metadata and legacy fields, got %#v", packReport)
	}
	if packReport.Cache.State == "" || packReport.Cache.CacheKey == "" || packReport.Cache.Invalidation.Policy != "content_addressed_read_through" || packReport.Contract.SchemaVersion != comprehension.GraphViewResultContractVersion || packReport.Contract.Cache.State != packReport.Cache.State {
		t.Fatalf("expected graph-view contract/cache metadata, got cache=%#v contract=%#v", packReport.Cache, packReport.Contract)
	}
	if !packReportSourcePresent(packReport.Cache.Invalidation.Sources, "ranker_weights") || !packReportSourcePresent(packReport.Cache.Invalidation.Sources, "runtime_traces") {
		t.Fatalf("expected ranker/runtime invalidation sources, got %#v", packReport.Cache.Invalidation.Sources)
	}
	if !slices.ContainsFunc(packReport.CandidateFiles, func(file struct {
		Path          string `json:"path"`
		Role          string `json:"role"`
		Confidence    string `json:"confidence"`
		RankerReasons []struct {
			Reason string `json:"reason"`
			State  string `json:"state"`
		} `json:"ranker_reasons"`
	}) bool {
		return file.Path == "pkg/pkg.go" && file.Role == "editable" && file.Confidence != "" && packReportRankerReasonPresent(file.RankerReasons, "role_floor")
	}) || !slices.ContainsFunc(packReport.Tests, func(test struct {
		Command string `json:"command"`
	}) bool {
		return test.Command == "go test ./..."
	}) {
		t.Fatalf("expected legacy pack candidates/tests with confidence, got files=%#v tests=%#v", packReport.CandidateFiles, packReport.Tests)
	}

	impact := mgr.Execute(context.Background(), ToolRequest{Tool: "context_impact", Task: "Plan a change to Run"})
	if impact.Error != "" {
		t.Fatalf("context_impact returned error: %v\n%s", impact.Error, impact.Output)
	}
	var impactReport struct {
		ContextViewPath string `json:"context_view_path"`
		Confidence      struct {
			Level string `json:"level"`
		} `json:"confidence"`
		Provenance struct {
			Operation string `json:"operation"`
			IndexID   string `json:"index_id"`
		} `json:"provenance"`
		IndexID     string `json:"index_id"`
		ImpactScope []struct {
			Path       string `json:"path"`
			Confidence string `json:"confidence"`
		} `json:"impact_scope"`
	}
	if err := json.Unmarshal([]byte(impact.Output), &impactReport); err != nil {
		t.Fatalf("decode context_impact output: %v\n%s", err, impact.Output)
	}
	if impactReport.ContextViewPath == "" || impactReport.Confidence.Level != "high" || impactReport.Provenance.Operation != "context_impact" || impactReport.Provenance.IndexID != impactReport.IndexID {
		t.Fatalf("expected impact metadata and context view, got %#v", impactReport)
	}
	if !slices.ContainsFunc(impactReport.ImpactScope, func(item struct {
		Path       string `json:"path"`
		Confidence string `json:"confidence"`
	}) bool {
		return item.Path == "pkg/pkg.go" && item.Confidence != ""
	}) {
		t.Fatalf("expected impact scope confidence, got %#v", impactReport.ImpactScope)
	}
}

func TestGraphBackedContextOutputsDoNotPrescribeNextTools(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, filepath.Join(root, "go.mod"), "module example.com/neutraloutputs\n\ngo 1.22\n")
	sourcePath := filepath.Join(root, "pkg", "pkg.go")
	writeWorkspaceFile(t, sourcePath, "package pkg\n\nfunc Run() string { return \"ok\" }\n")
	mgr := NewManager(config.Workspace{
		Root:                  root,
		CWD:                   root,
		CommandTimeoutSeconds: 30,
		MaxOutputBytes:        65536,
		MaxFileBytes:          65536,
		EnableShell:           true,
		EnableWrites:          true,
	})

	if result := mgr.Execute(context.Background(), ToolRequest{Tool: "context_index", Language: "go"}); result.Error != "" {
		t.Fatalf("context_index returned error: %v\n%s", result.Error, result.Output)
	}

	outputs := map[string]string{}
	for name, req := range map[string]ToolRequest{
		"context_status": {Tool: "context_status"},
		"context_query":  {Tool: "context_query", Query: "zzzz-no-match", Resolution: "symbol"},
		"context_zoom":   {Tool: "context_zoom", Handle: "symbol://pkg/pkg.go#Missing", Resolution: "symbol"},
		"context_pack":   {Tool: "context_pack", Task: "zzzz-no-match"},
		"context_impact": {Tool: "context_impact", Task: "zzzz-no-match"},
	} {
		result := mgr.Execute(context.Background(), req)
		if result.Error != "" && result.Error != "freshness_blocker" {
			t.Fatalf("%s returned error: %v\n%s", name, result.Error, result.Output)
		}
		outputs[name] = result.Summary + "\n" + result.Error + "\n" + result.Output
	}

	discovery := mgr.Execute(context.Background(), ToolRequest{
		Tool:     "discovery_query",
		Purpose:  "impact_analysis",
		Path:     ".",
		Pattern:  "Run",
		Language: "go",
		MaxDepth: 2,
	})
	if discovery.Error != "" {
		t.Fatalf("discovery_query returned error: %v\n%s", discovery.Error, discovery.Output)
	}
	packet := readDiscoveryQueryPacket(t, root, discovery.Output)
	if !packetHasLayer(packet, "context_pack") {
		t.Fatalf("expected graph-backed discovery_query context_pack layer, got %#v", packetLayerNames(packet))
	}
	outputs["discovery_query"] = discovery.Summary + "\n" + discovery.Output + "\n" + mustMarshalTestJSON(t, packet)

	writeWorkspaceFile(t, sourcePath, "package pkg\n\nfunc Run() string { return \"changed\" }\n")
	staleStatus := mgr.Execute(context.Background(), ToolRequest{Tool: "context_status"})
	if staleStatus.Error != "" {
		t.Fatalf("stale context_status returned error: %v\n%s", staleStatus.Error, staleStatus.Output)
	}
	outputs["context_status_stale"] = staleStatus.Summary + "\n" + staleStatus.Output

	for name, output := range outputs {
		assertNoPrescriptiveGraphOutput(t, name, output)
	}
}

func TestContextPackExposesStructuredUnresolvedGaps(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, filepath.Join(root, "go.mod"), "module example.com/structuredgaps\n\ngo 1.22\n")
	writeWorkspaceFile(t, filepath.Join(root, "pkg", "pkg.go"), "package pkg\n\nfunc Run() string { return \"ok\" }\n")
	mgr := NewManager(config.Workspace{Root: root, CWD: root})

	if result := mgr.Execute(context.Background(), ToolRequest{Tool: "context_index", Language: "go"}); result.Error != "" {
		t.Fatalf("context_index returned error: %v\n%s", result.Error, result.Output)
	}
	result := mgr.Execute(context.Background(), ToolRequest{Tool: "context_pack", Task: "zzzzzzzzqqq", Mode: "plan"})
	if result.Error != "" {
		t.Fatalf("context_pack returned error: %v\n%s", result.Error, result.Output)
	}
	var report struct {
		UnresolvedGaps       []string `json:"unresolved_gaps"`
		UnresolvedGapDetails []struct {
			Reason             string `json:"reason"`
			State              string `json:"state"`
			CandidateFileCount int    `json:"candidate_file_count"`
		} `json:"unresolved_gap_details"`
		Contract struct {
			UnresolvedGaps []struct {
				Reason string `json:"reason"`
				State  string `json:"state"`
			} `json:"unresolved_gaps"`
		} `json:"contract"`
	}
	if err := json.Unmarshal([]byte(result.Output), &report); err != nil {
		t.Fatalf("decode context_pack output: %v\n%s", err, result.Output)
	}
	if !slices.ContainsFunc(report.UnresolvedGapDetails, func(gap struct {
		Reason             string `json:"reason"`
		State              string `json:"state"`
		CandidateFileCount int    `json:"candidate_file_count"`
	}) bool {
		return gap.Reason == "no_graph_matches" && gap.State == "empty" && gap.CandidateFileCount == 0
	}) {
		t.Fatalf("expected structured no-match gap, got legacy=%#v details=%#v", report.UnresolvedGaps, report.UnresolvedGapDetails)
	}
	if !slices.ContainsFunc(report.Contract.UnresolvedGaps, func(gap struct {
		Reason string `json:"reason"`
		State  string `json:"state"`
	}) bool {
		return gap.Reason == "no_graph_matches" && gap.State == "empty"
	}) {
		t.Fatalf("expected contract unresolved gap mirror, got %#v", report.Contract.UnresolvedGaps)
	}
	assertNoPrescriptiveGraphOutput(t, "context_pack_structured_gaps", result.Output)
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

func TestContextPackRequiredEditPathsBoundFreshness(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, filepath.Join(root, "go.mod"), "module example.com/requiredfreshness\n\ngo 1.22\n")
	writeWorkspaceFile(t, filepath.Join(root, "pkg", "target.go"), "package pkg\n\nfunc Target() string { return \"ok\" }\n")
	writeWorkspaceFile(t, filepath.Join(root, "pkg", "noisy_a.go"), "package pkg\n\nfunc NoisyA() string { return \"old\" }\n")
	writeWorkspaceFile(t, filepath.Join(root, "pkg", "noisy_b.go"), "package pkg\n\nfunc NoisyB() string { return \"old\" }\n")
	mgr := NewManager(config.Workspace{Root: root, CWD: root})

	result := mgr.Execute(context.Background(), ToolRequest{Tool: "context_index", Language: "go"})
	if result.Error != "" {
		t.Fatalf("context_index returned error: %v\n%s", result.Error, result.Output)
	}
	writeWorkspaceFile(t, filepath.Join(root, "pkg", "noisy_a.go"), "package pkg\n\nfunc NoisyA() string { return \"new\" }\n")
	writeWorkspaceFile(t, filepath.Join(root, "pkg", "noisy_b.go"), "package pkg\n\nfunc NoisyB() string { return \"new\" }\n")

	result = mgr.Execute(context.Background(), ToolRequest{
		Tool:              "context_pack",
		Task:              "Plan a noisy change",
		RequiredEditPaths: []string{"pkg/target.go"},
		Limit:             1,
		FreshnessMaxFiles: 1,
	})
	if result.Error != "" {
		t.Fatalf("context_pack should use required_edit_paths as freshness scope, got error=%q output=%s", result.Error, result.Output)
	}
	var packReport struct {
		Freshness      comprehension.FreshnessDecision `json:"freshness"`
		CandidateFiles []struct {
			Path          string                       `json:"path"`
			RankerReasons []comprehension.RankerReason `json:"ranker_reasons"`
		} `json:"candidate_files"`
	}
	if err := json.Unmarshal([]byte(result.Output), &packReport); err != nil {
		t.Fatalf("decode context_pack output: %v\n%s", err, result.Output)
	}
	if !packReport.Freshness.FreshForScope || packReport.Freshness.BudgetExceeded || packReport.Freshness.Scope != "pkg/target.go" {
		t.Fatalf("expected required edit path to bound freshness, got %#v", packReport.Freshness)
	}
	if len(packReport.CandidateFiles) != 1 || packReport.CandidateFiles[0].Path != "pkg/target.go" {
		t.Fatalf("expected required edit path to outrank noisy task text, got %#v", packReport.CandidateFiles)
	}
	if !slices.ContainsFunc(packReport.CandidateFiles[0].RankerReasons, func(reason comprehension.RankerReason) bool {
		return reason.Reason == "required_edit_path"
	}) {
		t.Fatalf("expected required_edit_path ranker reason, got %#v", packReport.CandidateFiles[0].RankerReasons)
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
			Attempted            bool     `json:"attempted"`
			Scope                string   `json:"scope"`
			Reason               string   `json:"reason"`
			FilesIndexed         int      `json:"files_indexed"`
			PartialRefreshReason string   `json:"partial_refresh_reason"`
			FullRebuildReason    string   `json:"full_rebuild_reason"`
			FreshAfter           bool     `json:"fresh_after"`
			RefreshedFiles       []string `json:"refreshed_files"`
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

func TestContextStatusRefreshesSingleFileWithDependencyAwarePatch(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, filepath.Join(root, "go.mod"), "module example.com/statussinglefile\n\ngo 1.22\n")
	sourcePath := filepath.Join(root, "pkg", "a.go")
	writeWorkspaceFile(t, sourcePath, "package pkg\n\nfunc OldName() string { return \"old\" }\n")
	writeWorkspaceFile(t, filepath.Join(root, "pkg", "b.go"), "package pkg\n\nfunc KeepName() string { return OldName() }\n")
	mgr := NewManager(config.Workspace{Root: root, CWD: root})

	result := mgr.Execute(context.Background(), ToolRequest{Tool: "context_index", Language: "go"})
	if result.Error != "" {
		t.Fatalf("context_index returned error: %v\n%s", result.Error, result.Output)
	}
	var initialIndex struct {
		IndexID string `json:"index_id"`
	}
	if err := json.Unmarshal([]byte(result.Output), &initialIndex); err != nil {
		t.Fatalf("decode context_index output: %v\n%s", err, result.Output)
	}
	writeWorkspaceFile(t, sourcePath, "package pkg\n\nfunc NewName() string { return \"new\" }\n")

	result = mgr.Execute(context.Background(), ToolRequest{Tool: "context_status", Refresh: true, Path: "pkg/a.go", Language: "go"})
	if result.Error != "" {
		t.Fatalf("context_status returned error: %v\n%s", result.Error, result.Output)
	}
	var statusReport struct {
		IndexID     string   `json:"index_id"`
		Fresh       bool     `json:"fresh"`
		StaleFiles  []string `json:"stale_files"`
		AutoRefresh struct {
			Attempted            bool     `json:"attempted"`
			Scope                string   `json:"scope"`
			Reason               string   `json:"reason"`
			FilesIndexed         int      `json:"files_indexed"`
			PartialRefreshReason string   `json:"partial_refresh_reason"`
			FreshAfter           bool     `json:"fresh_after"`
			RefreshedFiles       []string `json:"refreshed_files"`
		} `json:"auto_refresh"`
	}
	if err := json.Unmarshal([]byte(result.Output), &statusReport); err != nil {
		t.Fatalf("decode context_status output: %v\n%s", err, result.Output)
	}
	if !statusReport.AutoRefresh.Attempted || statusReport.AutoRefresh.Scope != "pkg/a.go" || statusReport.AutoRefresh.FilesIndexed != 2 || statusReport.AutoRefresh.PartialRefreshReason != "sqlite_scope_patch" || !statusReport.AutoRefresh.FreshAfter {
		t.Fatalf("expected dependency-aware status refresh patch, got %#v", statusReport.AutoRefresh)
	}
	if !statusReport.Fresh || len(statusReport.StaleFiles) != 0 || !slices.Contains(statusReport.AutoRefresh.RefreshedFiles, "pkg/a.go") {
		t.Fatalf("expected fresh status after exact single-file refresh, got %#v", statusReport)
	}
	if statusReport.IndexID != initialIndex.IndexID {
		t.Fatalf("dependency-aware patch should preserve the active graph index id, got before=%s after=%s", initialIndex.IndexID, statusReport.IndexID)
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
			Attempted            bool     `json:"attempted"`
			Scope                string   `json:"scope"`
			Reason               string   `json:"reason"`
			FilesIndexed         int      `json:"files_indexed"`
			PartialRefreshReason string   `json:"partial_refresh_reason"`
			FullRebuildReason    string   `json:"full_rebuild_reason"`
			FreshAfter           bool     `json:"fresh_after"`
			RefreshedFiles       []string `json:"refreshed_files"`
		} `json:"auto_refresh"`
	}
	if err := json.Unmarshal([]byte(result.Output), &statusReport); err != nil {
		t.Fatalf("decode context_status output: %v\n%s", err, result.Output)
	}
	if !statusReport.AutoRefresh.Attempted || statusReport.AutoRefresh.Reason != "small_stale_file_set" || statusReport.AutoRefresh.FilesIndexed != 1 || !statusReport.AutoRefresh.FreshAfter {
		t.Fatalf("expected small-stale-set status refresh, got %#v", statusReport.AutoRefresh)
	}
	if statusReport.AutoRefresh.PartialRefreshReason != "exact_scope_refresh" || statusReport.AutoRefresh.FullRebuildReason != "" {
		t.Fatalf("expected unscoped small stale-set repair to stay exact and bounded, got %#v", statusReport.AutoRefresh)
	}
	if !statusReport.Fresh || len(statusReport.StaleFiles) != 0 || !slices.Contains(statusReport.AutoRefresh.RefreshedFiles, "pkg/pkg.go") {
		t.Fatalf("expected fresh status after small stale-file refresh, got %#v", statusReport)
	}
}

func TestContextStatusRefreshMaxFilesUsesDefaultBudgetForUnscopedStatus(t *testing.T) {
	staleStatus := comprehension.StatusReport{Indexed: true, Fresh: false}

	if got := contextStatusRefreshMaxFiles(ToolRequest{Tool: "context_status"}, staleStatus); got != defaultContextStatusUnboundedRefreshMaxFiles {
		t.Fatalf("expected default unscoped refresh budget %d, got %d", defaultContextStatusUnboundedRefreshMaxFiles, got)
	}
	if got := contextStatusRefreshMaxFiles(ToolRequest{Tool: "context_status", Limit: 12}, staleStatus); got != 12 {
		t.Fatalf("expected limit to win over default unscoped refresh budget, got %d", got)
	}
	if got := contextStatusRefreshMaxFiles(ToolRequest{Tool: "context_status", FreshnessMaxFiles: 7, Limit: 12}, staleStatus); got != 7 {
		t.Fatalf("expected freshness_max_files to win over limit, got %d", got)
	}
	if got := contextStatusRefreshMaxFiles(ToolRequest{Tool: "context_status", Path: "pkg"}, staleStatus); got != 0 {
		t.Fatalf("expected bounded path refresh to remain uncapped by default, got %d", got)
	}
	if got := contextStatusRefreshMaxFiles(ToolRequest{Tool: "context_status", RequiredEditPaths: []string{"pkg/a.go"}}, staleStatus); got != 0 {
		t.Fatalf("expected required edit path refresh to remain uncapped by default, got %d", got)
	}
	if got := contextStatusRefreshMaxFiles(ToolRequest{Tool: "context_status"}, comprehension.StatusReport{Indexed: true, Fresh: false, CollectorVersionMismatch: true}); got != 0 {
		t.Fatalf("expected collector-version rebuild path to remain uncapped by stale-file default, got %d", got)
	}
	if got := contextStatusRefreshMaxFiles(ToolRequest{Tool: "context_status"}, comprehension.StatusReport{Indexed: false, Fresh: false}); got != 0 {
		t.Fatalf("expected missing-index rebuild path to remain uncapped by stale-file default, got %d", got)
	}
	if got := contextStatusRefreshMaxFiles(ToolRequest{Tool: "context_status"}, comprehension.StatusReport{Indexed: true, Fresh: true}); got != 0 {
		t.Fatalf("expected fresh status to need no refresh budget, got %d", got)
	}
}

func TestContextStatusRefreshDefaultBudgetBlocksLargeUnboundedStaleSet(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, filepath.Join(root, "go.mod"), "module example.com/statusdefaultbudget\n\ngo 1.22\n")
	const staleFileCount = defaultContextStatusUnboundedRefreshMaxFiles + 5
	for i := 0; i < staleFileCount; i++ {
		writeWorkspaceFile(t, filepath.Join(root, "pkg", fmt.Sprintf("file_%03d.go", i)), fmt.Sprintf("package pkg\n\nfunc File%03d() string { return \"old\" }\n", i))
	}
	mgr := NewManager(config.Workspace{Root: root, CWD: root})

	result := mgr.Execute(context.Background(), ToolRequest{Tool: "context_index", Language: "go"})
	if result.Error != "" {
		t.Fatalf("context_index returned error: %v\n%s", result.Error, result.Output)
	}
	for i := 0; i < staleFileCount; i++ {
		writeWorkspaceFile(t, filepath.Join(root, "pkg", fmt.Sprintf("file_%03d.go", i)), fmt.Sprintf("package pkg\n\nfunc File%03d() string { return \"new\" }\n", i))
	}

	result = mgr.Execute(context.Background(), ToolRequest{Tool: "context_status", Refresh: true, Language: "go"})
	if result.Error != "" {
		t.Fatalf("context_status returned error: %v\n%s", result.Error, result.Output)
	}
	var statusReport struct {
		Fresh          bool     `json:"fresh"`
		StaleFileCount int      `json:"stale_file_count"`
		StaleFiles     []string `json:"stale_files"`
		AutoRefresh    struct {
			Attempted      bool     `json:"attempted"`
			Reason         string   `json:"reason"`
			SkippedReason  string   `json:"skipped_reason"`
			FilesIndexed   int      `json:"files_indexed"`
			FreshAfter     bool     `json:"fresh_after"`
			StalePathCount int      `json:"stale_path_count"`
			MaxFiles       int      `json:"max_files"`
			RefreshScopes  []string `json:"refresh_scopes"`
		} `json:"auto_refresh"`
		Freshness struct {
			FreshForScope  bool `json:"fresh_for_scope"`
			BudgetExceeded bool `json:"budget_exceeded"`
		} `json:"freshness"`
	}
	if err := json.Unmarshal([]byte(result.Output), &statusReport); err != nil {
		t.Fatalf("decode context_status output: %v\n%s", err, result.Output)
	}
	if statusReport.Fresh || statusReport.StaleFileCount != staleFileCount || len(statusReport.StaleFiles) != defaultContextStatusOutputLimit {
		t.Fatalf("expected stale status with capped stale-file sample, got %#v", statusReport)
	}
	if statusReport.AutoRefresh.Attempted || statusReport.AutoRefresh.Reason != "budget_exceeded" || statusReport.AutoRefresh.SkippedReason != "budget_exceeded" {
		t.Fatalf("expected default-budget blocker without refresh attempt, got %#v", statusReport.AutoRefresh)
	}
	if statusReport.AutoRefresh.FilesIndexed != 0 || statusReport.AutoRefresh.FreshAfter {
		t.Fatalf("expected no refresh to run after default-budget blocker, got %#v", statusReport.AutoRefresh)
	}
	if statusReport.AutoRefresh.StalePathCount != staleFileCount || statusReport.AutoRefresh.MaxFiles != defaultContextStatusUnboundedRefreshMaxFiles {
		t.Fatalf("expected default-budget fields on blocker, got %#v", statusReport.AutoRefresh)
	}
	if len(statusReport.AutoRefresh.RefreshScopes) != 0 {
		t.Fatalf("expected no refresh scopes after default-budget blocker, got %#v", statusReport.AutoRefresh.RefreshScopes)
	}
	if statusReport.Freshness.FreshForScope || !statusReport.Freshness.BudgetExceeded {
		t.Fatalf("expected freshness budget blocker, got %#v", statusReport.Freshness)
	}
}

func TestContextStatusRefreshLimitCapsUnboundedStaleSet(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, filepath.Join(root, "go.mod"), "module example.com/statuslimit\n\ngo 1.22\n")
	for i := 0; i < 3; i++ {
		writeWorkspaceFile(t, filepath.Join(root, "pkg", fmt.Sprintf("file_%02d.go", i)), fmt.Sprintf("package pkg\n\nfunc File%02d() string { return \"old\" }\n", i))
	}
	mgr := NewManager(config.Workspace{Root: root, CWD: root})

	result := mgr.Execute(context.Background(), ToolRequest{Tool: "context_index", Language: "go"})
	if result.Error != "" {
		t.Fatalf("context_index returned error: %v\n%s", result.Error, result.Output)
	}
	for i := 0; i < 3; i++ {
		writeWorkspaceFile(t, filepath.Join(root, "pkg", fmt.Sprintf("file_%02d.go", i)), fmt.Sprintf("package pkg\n\nfunc File%02d() string { return \"new\" }\n", i))
	}

	result = mgr.Execute(context.Background(), ToolRequest{Tool: "context_status", Refresh: true, Language: "go", Limit: 2})
	if result.Error != "" {
		t.Fatalf("context_status should return a freshness report, not a tool error: %v\n%s", result.Error, result.Output)
	}
	var statusReport struct {
		Fresh          bool     `json:"fresh"`
		StalePathCount int      `json:"stale_path_count"`
		StaleFileCount int      `json:"stale_file_count"`
		StaleFiles     []string `json:"stale_files"`
		AutoRefresh    struct {
			Attempted      bool   `json:"attempted"`
			Reason         string `json:"reason"`
			SkippedReason  string `json:"skipped_reason"`
			StalePathCount int    `json:"stale_path_count"`
			MaxFiles       int    `json:"max_files"`
		} `json:"auto_refresh"`
		Freshness struct {
			FreshForScope  bool     `json:"fresh_for_scope"`
			Reason         string   `json:"reason"`
			Blocker        string   `json:"blocker"`
			BudgetExceeded bool     `json:"budget_exceeded"`
			StalePathCount int      `json:"stale_path_count"`
			StalePaths     []string `json:"stale_paths"`
		} `json:"freshness"`
	}
	if err := json.Unmarshal([]byte(result.Output), &statusReport); err != nil {
		t.Fatalf("decode context_status output: %v\n%s", err, result.Output)
	}
	if statusReport.Fresh || statusReport.StalePathCount != 3 {
		t.Fatalf("expected stale unbounded status to remain reported, got %#v", statusReport)
	}
	if statusReport.StaleFileCount != 3 || len(statusReport.StaleFiles) != 2 {
		t.Fatalf("expected --limit to cap stale file display while preserving counts, got %#v", statusReport)
	}
	if statusReport.AutoRefresh.Attempted || statusReport.AutoRefresh.Reason != "budget_exceeded" || statusReport.AutoRefresh.SkippedReason != "budget_exceeded" || statusReport.AutoRefresh.StalePathCount != 3 || statusReport.AutoRefresh.MaxFiles != 2 {
		t.Fatalf("expected limit-driven budget blocker, got %#v", statusReport.AutoRefresh)
	}
	if statusReport.Freshness.FreshForScope || !statusReport.Freshness.BudgetExceeded || statusReport.Freshness.Blocker != "budget_exceeded" {
		t.Fatalf("expected freshness budget blocker, got %#v", statusReport.Freshness)
	}
	if statusReport.Freshness.StalePathCount != 3 || len(statusReport.Freshness.StalePaths) != 2 {
		t.Fatalf("expected --limit to cap freshness stale path display while preserving counts, got %#v", statusReport.Freshness)
	}
}

func TestContextStatusDefaultCapsStaleAndMissingDisplayWithoutLosingCounts(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, filepath.Join(root, "go.mod"), "module example.com/statusdisplay\n\ngo 1.22\n")
	for i := 0; i < defaultContextStatusOutputLimit+3; i++ {
		writeWorkspaceFile(t, filepath.Join(root, "pkg", fmt.Sprintf("stale_%02d.go", i)), fmt.Sprintf("package pkg\n\nfunc Stale%02d() string { return \"old\" }\n", i))
	}
	for i := 0; i < defaultContextStatusOutputLimit+2; i++ {
		writeWorkspaceFile(t, filepath.Join(root, "pkg", fmt.Sprintf("missing_%02d.go", i)), fmt.Sprintf("package pkg\n\nfunc Missing%02d() string { return \"old\" }\n", i))
	}
	mgr := NewManager(config.Workspace{Root: root, CWD: root})

	result := mgr.Execute(context.Background(), ToolRequest{Tool: "context_index", Language: "go"})
	if result.Error != "" {
		t.Fatalf("context_index returned error: %v\n%s", result.Error, result.Output)
	}
	for i := 0; i < defaultContextStatusOutputLimit+3; i++ {
		writeWorkspaceFile(t, filepath.Join(root, "pkg", fmt.Sprintf("stale_%02d.go", i)), fmt.Sprintf("package pkg\n\nfunc Stale%02d() string { return \"new\" }\n", i))
	}
	for i := 0; i < defaultContextStatusOutputLimit+2; i++ {
		if err := os.Remove(filepath.Join(root, "pkg", fmt.Sprintf("missing_%02d.go", i))); err != nil {
			t.Fatalf("remove missing fixture: %v", err)
		}
	}

	result = mgr.Execute(context.Background(), ToolRequest{Tool: "context_status", Language: "go"})
	if result.Error != "" {
		t.Fatalf("context_status returned error: %v\n%s", result.Error, result.Output)
	}
	var statusReport struct {
		StaleFileCount   int      `json:"stale_file_count"`
		MissingFileCount int      `json:"missing_file_count"`
		StalePathCount   int      `json:"stale_path_count"`
		StaleFiles       []string `json:"stale_files"`
		MissingFiles     []string `json:"missing_files"`
		Freshness        struct {
			StalePathCount   int      `json:"stale_path_count"`
			MissingPathCount int      `json:"missing_path_count"`
			StalePaths       []string `json:"stale_paths"`
			MissingPaths     []string `json:"missing_paths"`
		} `json:"freshness"`
	}
	if err := json.Unmarshal([]byte(result.Output), &statusReport); err != nil {
		t.Fatalf("decode context_status output: %v\n%s", err, result.Output)
	}
	if statusReport.StaleFileCount != defaultContextStatusOutputLimit+3 || statusReport.MissingFileCount != defaultContextStatusOutputLimit+2 || statusReport.StalePathCount != (defaultContextStatusOutputLimit*2)+5 {
		t.Fatalf("expected full stale counts to survive display cap, got %#v", statusReport)
	}
	if len(statusReport.StaleFiles) != defaultContextStatusOutputLimit {
		t.Fatalf("expected default stale file display cap, got %d file(s)", len(statusReport.StaleFiles))
	}
	if len(statusReport.MissingFiles) != defaultContextStatusOutputLimit {
		t.Fatalf("expected default missing file display cap, got %d file(s)", len(statusReport.MissingFiles))
	}
	if statusReport.Freshness.StalePathCount != defaultContextStatusOutputLimit+3 || len(statusReport.Freshness.StalePaths) != defaultContextStatusOutputLimit {
		t.Fatalf("expected default freshness stale path display cap with full count, got %#v", statusReport.Freshness)
	}
	if statusReport.Freshness.MissingPathCount != defaultContextStatusOutputLimit+2 || len(statusReport.Freshness.MissingPaths) != defaultContextStatusOutputLimit {
		t.Fatalf("expected default freshness missing path display cap with full count, got %#v", statusReport.Freshness)
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

func TestContextStatusRefreshBuildsLargeMissingRootIndex(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, filepath.Join(root, "go.mod"), "module example.com/statusmissinglarge\n\ngo 1.22\n")
	for i := 0; i < 30; i++ {
		writeWorkspaceFile(t, filepath.Join(root, "pkg", fmt.Sprintf("file_%02d.go", i)), fmt.Sprintf("package pkg\n\nfunc File%02d() {}\n", i))
	}
	mgr := NewManager(config.Workspace{Root: root, CWD: root})

	result := mgr.Execute(context.Background(), ToolRequest{Tool: "context_status", Refresh: true, Language: "go"})
	if result.Error != "" {
		t.Fatalf("context_status should return a freshness blocker report, not a tool timeout/error: %v\n%s", result.Error, result.Output)
	}
	var statusReport struct {
		Indexed     bool `json:"indexed"`
		Fresh       bool `json:"fresh"`
		AutoRefresh struct {
			Attempted    bool   `json:"attempted"`
			Scope        string `json:"scope"`
			Reason       string `json:"reason"`
			FilesIndexed int    `json:"files_indexed"`
			FreshAfter   bool   `json:"fresh_after"`
		} `json:"auto_refresh"`
		Freshness struct {
			FreshForScope  bool `json:"fresh_for_scope"`
			BudgetExceeded bool `json:"budget_exceeded"`
		} `json:"freshness"`
	}
	if err := json.Unmarshal([]byte(result.Output), &statusReport); err != nil {
		t.Fatalf("decode context_status output: %v\n%s", err, result.Output)
	}
	if !statusReport.Indexed || !statusReport.Fresh {
		t.Fatalf("large missing index should build a fresh root graph, got %#v", statusReport)
	}
	if !statusReport.AutoRefresh.Attempted || statusReport.AutoRefresh.Scope != "." || statusReport.AutoRefresh.Reason != "missing_index" || statusReport.AutoRefresh.FilesIndexed != 30 || !statusReport.AutoRefresh.FreshAfter {
		t.Fatalf("expected large missing-index refresh, got %#v", statusReport.AutoRefresh)
	}
	if !statusReport.Freshness.FreshForScope || statusReport.Freshness.BudgetExceeded {
		t.Fatalf("expected fresh read-through decision without budget blocker, got %#v", statusReport.Freshness)
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

func TestContextQueryRefreshesUnboundedStaleGraphWithoutBudgetBlocker(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, filepath.Join(root, "go.mod"), "module example.com/querylarge\n\ngo 1.22\n")
	for i := 0; i < 30; i++ {
		writeWorkspaceFile(t, filepath.Join(root, "pkg", fmt.Sprintf("f%03d.go", i)), fmt.Sprintf("package pkg\n\nfunc F%03d() string { return \"old\" }\n", i))
	}
	mgr := NewManager(config.Workspace{Root: root, CWD: root})
	if result := mgr.Execute(context.Background(), ToolRequest{Tool: "context_index", Language: "go"}); result.Error != "" {
		t.Fatalf("context_index returned error: %v\n%s", result.Error, result.Output)
	}
	for i := 0; i < 30; i++ {
		writeWorkspaceFile(t, filepath.Join(root, "pkg", fmt.Sprintf("f%03d.go", i)), fmt.Sprintf("package pkg\n\nfunc F%03d() string { return \"new\" }\n", i))
	}
	result := mgr.Execute(context.Background(), ToolRequest{Tool: "context_query", Query: "what functions changed", Language: "go", Resolution: "symbol"})
	if result.Error != "" {
		t.Fatalf("context_query should refresh large stale scopes instead of returning a budget blocker, got error=%q output=%s", result.Error, result.Output)
	}
	if strings.Contains(result.Output, `"budget_exceeded": true`) || strings.Contains(result.Output, `"error": "freshness_blocker"`) {
		t.Fatalf("expected no budget freshness blocker, got %s", result.Output)
	}
	if !strings.Contains(result.Output, `"reason": "stale_unbounded_graph"`) {
		t.Fatalf("expected stale unbounded graph refresh evidence, got %s", result.Output)
	}
}

func TestContextQueryStaleEvidenceBlockerAndReportOnlyModes(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, filepath.Join(root, "go.mod"), "module example.com/staleevidence\n\ngo 1.22\n")
	for i := 0; i < 3; i++ {
		writeWorkspaceFile(t, filepath.Join(root, "pkg", fmt.Sprintf("f%d.go", i)), fmt.Sprintf("package pkg\n\nfunc Old%d() string { return \"old\" }\n", i))
	}
	mgr := NewManager(config.Workspace{Root: root, CWD: root})
	if result := mgr.Execute(context.Background(), ToolRequest{Tool: "context_index", Language: "go"}); result.Error != "" {
		t.Fatalf("context_index returned error: %v\n%s", result.Error, result.Output)
	}
	for i := 0; i < 3; i++ {
		writeWorkspaceFile(t, filepath.Join(root, "pkg", fmt.Sprintf("f%d.go", i)), fmt.Sprintf("package pkg\n\nfunc New%d() string { return \"new\" }\n", i))
	}

	blocked := mgr.Execute(context.Background(), ToolRequest{
		Tool:              "context_query",
		Query:             "Old",
		Language:          "go",
		Resolution:        "symbol",
		FreshnessPolicy:   "require_fresh",
		FreshnessMaxFiles: 1,
	})
	if blocked.Error != "freshness_blocker" {
		t.Fatalf("expected freshness blocker, got error=%q output=%s", blocked.Error, blocked.Output)
	}
	var blockerReport struct {
		Error         string                            `json:"error"`
		Freshness     comprehension.FreshnessDecision   `json:"freshness"`
		StaleEvidence comprehension.StaleEvidenceSafety `json:"stale_evidence"`
	}
	if err := json.Unmarshal([]byte(blocked.Output), &blockerReport); err != nil {
		t.Fatalf("decode blocker output: %v\n%s", err, blocked.Output)
	}
	if blockerReport.StaleEvidence.State != "blocked" || blockerReport.StaleEvidence.Policy != comprehension.FreshnessRequire || !blockerReport.Freshness.BudgetExceeded {
		t.Fatalf("expected blocked stale-evidence state, got %#v", blockerReport)
	}

	reported := mgr.Execute(context.Background(), ToolRequest{
		Tool:              "context_query",
		Query:             "Old",
		Language:          "go",
		Resolution:        "symbol",
		FreshnessPolicy:   "report_only",
		FreshnessMaxFiles: 1,
	})
	if reported.Error != "" {
		t.Fatalf("report-only context_query should not hard block, got error=%q output=%s", reported.Error, reported.Output)
	}
	var reportOnly struct {
		Fresh         bool                              `json:"fresh"`
		Freshness     comprehension.FreshnessDecision   `json:"freshness"`
		StaleEvidence comprehension.StaleEvidenceSafety `json:"stale_evidence"`
		Confidence    struct {
			Level  string `json:"level"`
			Reason string `json:"reason"`
		} `json:"confidence"`
		Nodes []struct {
			ID string `json:"id"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal([]byte(reported.Output), &reportOnly); err != nil {
		t.Fatalf("decode report-only output: %v\n%s", err, reported.Output)
	}
	if reportOnly.Fresh || reportOnly.Freshness.Policy != comprehension.FreshnessReportOnly || reportOnly.StaleEvidence.State != "reported" || reportOnly.Confidence.Level != "low" {
		t.Fatalf("expected report-only stale evidence with low confidence, got %#v", reportOnly)
	}
	if len(reportOnly.Nodes) == 0 {
		t.Fatalf("expected stale graph nodes to remain observable in report-only mode, got %#v", reportOnly)
	}
	fields := ContextTelemetryFields("context_query", reported.Output)
	if fields["context_stale_evidence_state"] != "reported" || fields["context_stale_use_reported"] != true || fields["context_freshness_state"] != "reported" || fields["context_freshness_blocked"] == true {
		t.Fatalf("expected stale-use report telemetry, got %#v", fields)
	}
}

func TestContextZoomPreservesFreshnessPolicyAndMaxFiles(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, filepath.Join(root, "go.mod"), "module example.com/zoompolicy\n\ngo 1.22\n")
	for i := 0; i < 3; i++ {
		writeWorkspaceFile(t, filepath.Join(root, "pkg", fmt.Sprintf("f%d.go", i)), fmt.Sprintf("package pkg\n\nfunc Old%d() string { return \"old\" }\n", i))
	}
	mgr := NewManager(config.Workspace{Root: root, CWD: root})
	if result := mgr.Execute(context.Background(), ToolRequest{Tool: "context_index", Language: "go"}); result.Error != "" {
		t.Fatalf("context_index returned error: %v\n%s", result.Error, result.Output)
	}
	for i := 0; i < 3; i++ {
		writeWorkspaceFile(t, filepath.Join(root, "pkg", fmt.Sprintf("f%d.go", i)), fmt.Sprintf("package pkg\n\nfunc New%d() string { return \"new\" }\n", i))
	}

	blocked := mgr.Execute(context.Background(), ToolRequest{
		Tool:              "context_zoom",
		Handle:            "symbol://pkg/f0.go#Old0",
		Path:              "pkg",
		Language:          "go",
		FreshnessPolicy:   "require_fresh",
		FreshnessMaxFiles: 1,
	})
	if blocked.Error != "freshness_blocker" {
		t.Fatalf("expected require_fresh zoom budget blocker, got error=%q output=%s", blocked.Error, blocked.Output)
	}
	var blockerReport struct {
		Freshness     comprehension.FreshnessDecision   `json:"freshness"`
		StaleEvidence comprehension.StaleEvidenceSafety `json:"stale_evidence"`
	}
	if err := json.Unmarshal([]byte(blocked.Output), &blockerReport); err != nil {
		t.Fatalf("decode blocker output: %v\n%s", err, blocked.Output)
	}
	if blockerReport.Freshness.Policy != comprehension.FreshnessRequire || !blockerReport.Freshness.BudgetExceeded || blockerReport.StaleEvidence.State != "blocked" {
		t.Fatalf("expected propagated require_fresh policy and max-file budget, got %#v", blockerReport)
	}

	reported := mgr.Execute(context.Background(), ToolRequest{
		Tool:              "context_zoom",
		Handle:            "symbol://pkg/f0.go#Old0",
		Path:              "pkg",
		Language:          "go",
		FreshnessPolicy:   "report_only",
		FreshnessMaxFiles: 1,
	})
	if reported.Error != "" {
		t.Fatalf("report-only context_zoom should not hard block, got error=%q output=%s", reported.Error, reported.Output)
	}
	var zoomReport struct {
		Freshness     comprehension.FreshnessDecision   `json:"freshness"`
		StaleEvidence comprehension.StaleEvidenceSafety `json:"stale_evidence"`
	}
	if err := json.Unmarshal([]byte(reported.Output), &zoomReport); err != nil {
		t.Fatalf("decode report-only zoom output: %v\n%s", err, reported.Output)
	}
	if zoomReport.Freshness.Policy != comprehension.FreshnessReportOnly || zoomReport.StaleEvidence.State != "reported" || zoomReport.Freshness.Refresh != nil {
		t.Fatalf("expected report-only policy to remain report-only, got %#v", zoomReport)
	}
	assertNoPrescriptiveGraphOutput(t, "context_zoom report-only stale output", reported.Output)
	fields := ContextTelemetryFields("context_zoom", reported.Output)
	if fields["context_freshness_state"] != "reported" || fields["context_freshness_blocked"] == true || fields["context_stale_use_reported"] != true {
		t.Fatalf("expected report-only zoom telemetry without hard blocker, got %#v", fields)
	}
}

func TestContextZoomRefreshesHandleFileBeforeReturningGraphFacts(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, filepath.Join(root, "go.mod"), "module example.com/zoomfresh\n\ngo 1.22\n")
	sourcePath := filepath.Join(root, "pkg", "pkg.go")
	writeWorkspaceFile(t, sourcePath, "package pkg\n\nfunc Run() string { return \"old\" }\n")
	mgr := NewManager(config.Workspace{Root: root, CWD: root})
	if result := mgr.Execute(context.Background(), ToolRequest{Tool: "context_index", Language: "go"}); result.Error != "" {
		t.Fatalf("context_index returned error: %v\n%s", result.Error, result.Output)
	}
	writeWorkspaceFile(t, sourcePath, "package pkg\n\nfunc Run() string { return \"new\" }\n")
	result := mgr.Execute(context.Background(), ToolRequest{Tool: "context_zoom", ID: "symbol://pkg/pkg.go#Run", Language: "go"})
	if result.Error != "" {
		t.Fatalf("context_zoom returned error: %v\n%s", result.Error, result.Output)
	}
	var zoom struct {
		Freshness *comprehension.FreshnessDecision `json:"freshness"`
		Node      *struct {
			ID string `json:"id"`
		} `json:"node"`
	}
	if err := json.Unmarshal([]byte(result.Output), &zoom); err != nil {
		t.Fatalf("decode context_zoom output: %v\n%s", err, result.Output)
	}
	if zoom.Node == nil || zoom.Node.ID != "symbol://pkg/pkg.go#Run" {
		t.Fatalf("expected refreshed Run node, got %#v", zoom.Node)
	}
	if zoom.Freshness == nil || !zoom.Freshness.FreshForScope || zoom.Freshness.Refresh == nil || !zoom.Freshness.Refresh.Attempted {
		t.Fatalf("expected context_zoom freshness refresh, got %#v", zoom.Freshness)
	}
}

func TestContextZoomAcceptsHandleAlias(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, filepath.Join(root, "go.mod"), "module example.com/zoomhandle\n\ngo 1.22\n")
	writeWorkspaceFile(t, filepath.Join(root, "pkg", "pkg.go"), "package pkg\n\nfunc Run() string { return \"ok\" }\n")
	mgr := NewManager(config.Workspace{Root: root, CWD: root})
	if result := mgr.Execute(context.Background(), ToolRequest{Tool: "context_index", Language: "go"}); result.Error != "" {
		t.Fatalf("context_index returned error: %v\n%s", result.Error, result.Output)
	}
	result := mgr.Execute(context.Background(), ToolRequest{Tool: "context_zoom", Handle: "symbol://pkg/pkg.go#Run", Language: "go"})
	if result.Error != "" {
		t.Fatalf("context_zoom with handle alias returned error: %v\n%s", result.Error, result.Output)
	}
	var zoom struct {
		Node *struct {
			ID string `json:"id"`
		} `json:"node"`
	}
	if err := json.Unmarshal([]byte(result.Output), &zoom); err != nil {
		t.Fatalf("decode context_zoom output: %v\n%s", err, result.Output)
	}
	if zoom.Node == nil || zoom.Node.ID != "symbol://pkg/pkg.go#Run" {
		t.Fatalf("expected Run node via handle alias, got %#v", zoom.Node)
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
	if report.ToolSurface.ExportedSchemaToolCount != report.ToolSurface.ManifestToolCount || report.ToolSurface.DriftFindingCount != 0 {
		t.Fatalf("expected manifest/exported schema alignment, got %#v", report.ToolSurface)
	}
	if len(report.ToolSurface.ManifestWithoutSchema) != 0 {
		t.Fatalf("manifest tools missing schemas: %#v", report.ToolSurface.ManifestWithoutSchema)
	}
	if len(report.ToolSurface.SchemaWithoutManifest) != 0 {
		t.Fatalf("schemas missing from built-in tool surfaces: %#v", report.ToolSurface.SchemaWithoutManifest)
	}
	if !slices.Contains(toolManifestToolsAcrossBuiltInSurfaces(), "submit_plan") {
		t.Fatalf("built-in surface union should include conditional submit_plan")
	}
	if !slices.ContainsFunc(report.ToolSurface.StaleUseBlockerCoverage, func(row harnessAuditStaleUseCoverage) bool {
		return row.Tool == "context_query" && row.RuntimeBlocker && row.SchemaErrorClass && row.State == "blocked"
	}) {
		t.Fatalf("expected context_query stale-use blocker coverage, got %#v", report.ToolSurface.StaleUseBlockerCoverage)
	}
	if !slices.Contains(report.ToolSurface.StaleUseReportOnlyTools, "context_status") {
		t.Fatalf("expected context_status report-only stale-use coverage, got %#v", report.ToolSurface.StaleUseReportOnlyTools)
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
		`{"type":"tool_request","tool":"code_navigate","action":"code_navigate:read_symbol:a.go:main","raw":"{\"tool\":\"code_navigate\",\"path\":\"a.go\",\"symbol\":\"main\",\"capability\":\"read_symbol\"}"}`,
		`{"type":"tool_result","tool":"code_navigate","summary":"Read symbol main","error":""}`,
		"",
	}, "\n"))
	writeWorkspaceFile(t, filepath.Join(root, "atom.md"), "# atom\n")
	writeWorkspaceFile(t, filepath.Join(root, ".idiom", "lane-history.jsonl"), strings.Join([]string{
		`{"ts":"2026-05-13T00:00:00Z","mode":"plan-execute","brief":"` + filepath.ToSlash(filepath.Join(root, "atom.md")) + `","exit_code":1,"final_state":"stale-needs-rebase","model_calls":0,"tool_calls":0,"total_tokens":0,"duration_ms":0}`,
		`{"ts":"2026-05-13T00:01:00Z","mode":"plan-execute","brief":"atom-2.md","exit_code":0,"final_state":"success","model_calls":2,"tool_calls":3,"total_tokens":1200,"duration_ms":4500}`,
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
	if !slices.ContainsFunc(report.LogBehavior.CapabilityMix, func(row harnessAuditCapabilityMix) bool {
		return row.Capability == "find_references" && row.Calls == 1
	}) {
		t.Fatalf("expected observed behavior capability mix for search_files, got %#v", report.LogBehavior.CapabilityMix)
	}
	if !slices.ContainsFunc(report.LogBehavior.CapabilityMix, func(row harnessAuditCapabilityMix) bool {
		return row.Capability == "inspect_symbol" && row.Calls == 1
	}) {
		t.Fatalf("expected observed behavior capability mix for code_navigate read_symbol, got %#v", report.LogBehavior.CapabilityMix)
	}
	if slices.ContainsFunc(report.LogBehavior.CapabilityMix, func(row harnessAuditCapabilityMix) bool {
		return row.Capability == "find_definition" && row.Calls != 0
	}) {
		t.Fatalf("code_navigate read_symbol must not expand to every static code_navigate capability, got %#v", report.LogBehavior.CapabilityMix)
	}
	if !slices.ContainsFunc(report.LogBehavior.TopCapabilities, func(row harnessAuditCapabilityMix) bool {
		return row.Capability == "find_references" && row.Calls == 1
	}) {
		t.Fatalf("expected top capability rollup, got %#v", report.LogBehavior.TopCapabilities)
	}
	if report.LogBehavior.LaneHistory == nil ||
		report.LogBehavior.LaneHistory.Runs != 2 ||
		report.LogBehavior.LaneHistory.ByFinalState["stale-needs-rebase"] != 1 ||
		report.LogBehavior.LaneHistory.TotalTokens != 1200 ||
		report.LogBehavior.LaneHistory.Recent[0].Brief != "atom.md" {
		t.Fatalf("expected lane history aggregate, got %#v", report.LogBehavior.LaneHistory)
	}
}

func TestHarnessAuditBehaviorSummarizesMalformedOutputStages(t *testing.T) {
	root := t.TempDir()
	logPath := filepath.Join(root, "logs", "telemetry", "malformed.jsonl")
	writeWorkspaceFile(t, logPath, strings.Join([]string{
		`{"type":"run_event","role":"tool_request_normalizer","content":"{\"outcome\":\"unsafe_to_infer\",\"parser_failure_class\":\"missing_tool\"}"}`,
		`{"type":"run_event","role":"edit_intent_validated","content":"{\"schema_version\":\"idiom.edit_intent.v1\",\"validation_result\":\"accepted\"}"}`,
		`{"type":"run_event","role":"patch_compile_result","content":"{\"compile_outcome\":\"rejected\",\"failure_class\":\"non_unique_anchor\"}"}`,
		`{"type":"message","role":"malformed_output_recovery_exhausted","content":"{\"recovery_outcome\":\"exhausted\",\"failure_class\":\"parser_failure_budget\"}"}`,
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
	stages := report.LogBehavior.MalformedOutput
	if stages == nil {
		t.Fatalf("expected malformed output stage summary, got %#v", report.LogBehavior)
	}
	if stages.LogsHit != 1 ||
		stages.EventCounts["tool_request_normalizer"] != 1 ||
		stages.EventCounts["edit_intent_validated"] != 1 ||
		stages.EventCounts["patch_compile_result"] != 1 ||
		stages.EventCounts["malformed_output_recovery_exhausted"] != 1 ||
		stages.ValidationResultCounts["accepted"] != 1 ||
		stages.FailureClassCounts["non_unique_anchor"] != 1 ||
		stages.CompileOutcomeCounts["rejected"] != 1 ||
		stages.CompileOutcomeCounts["unsafe_to_infer"] != 0 ||
		stages.RecoveryOutcomeCounts["exhausted"] != 1 ||
		stages.SchemaVersionCounts["idiom.edit_intent.v1"] != 1 {
		t.Fatalf("unexpected malformed output stage summary: %#v", stages)
	}
	if strings.Contains(result.Output, "old_text") || strings.Contains(result.Output, "raw transcript") {
		t.Fatalf("malformed output audit should expose counts, not raw payload bodies: %s", result.Output)
	}
}

func TestHarnessAuditBehaviorRedactsWorstOffenderPrompts(t *testing.T) {
	root := t.TempDir()
	logPath := filepath.Join(root, "logs", "conversations", "session.jsonl")
	writeWorkspaceFile(t, logPath, strings.Join([]string{
		`{"type":"message","role":"user","content":"private customer prompt with secret"}`,
		`{"type":"tool_request","tool":"read_file","action":"read_file:a.go","raw":"{\"tool\":\"read_file\",\"path\":\"a.go\"}"}`,
		`{"type":"tool_result","tool":"read_file","summary":"Read a.go","error":""}`,
		`{"type":"tool_request","tool":"read_file","action":"read_file:a.go","raw":"{\"tool\":\"read_file\",\"path\":\"a.go\"}"}`,
		`{"type":"tool_result","tool":"read_file","summary":"Read a.go","error":""}`,
		`{"type":"tool_request","tool":"read_file","action":"read_file:a.go","raw":"{\"tool\":\"read_file\",\"path\":\"a.go\"}"}`,
		`{"type":"tool_result","tool":"read_file","summary":"Read a.go","error":""}`,
		"",
	}, "\n"))
	mgr := NewManager(config.Workspace{Root: root, CWD: root})

	result := mgr.Execute(context.Background(), ToolRequest{Tool: "harness_audit", Purpose: "behavior", Path: "logs/conversations", Limit: 5})
	if result.Error != "" {
		t.Fatalf("harness_audit returned error: %v\n%s", result.Error, result.Output)
	}
	if strings.Contains(result.Output, "user_prompt") ||
		strings.Contains(result.Output, "private customer prompt") ||
		strings.Contains(result.Output, "secret") {
		t.Fatalf("harness_audit leaked prompt data: %s", result.Output)
	}
	var report harnessAuditReport
	if err := json.Unmarshal([]byte(result.Output), &report); err != nil {
		t.Fatalf("decode audit report: %v\n%s", err, result.Output)
	}
	if report.LogBehavior == nil ||
		len(report.LogBehavior.WorstOffenders) != 1 ||
		!report.LogBehavior.WorstOffenders[0].LogExcerptRedacted {
		t.Fatalf("expected redacted worst offender marker, got %#v", report.LogBehavior)
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

func TestHarnessFeedbackPlanningQualityFeedsAuditClusters(t *testing.T) {
	root := t.TempDir()
	mgr := NewManager(config.Workspace{Root: root, CWD: root})

	result := mgr.Execute(context.Background(), ToolRequest{
		Tool:         "harness_feedback",
		Category:     "impact_ranking",
		Component:    "context_impact",
		Expected:     "ranked scope names the file that owns the planning behavior",
		Observed:     "ranking omitted the owner file even though context_pack had it",
		Impact:       "planner needed extra reads before producing bounded atoms",
		Confidence:   "verified",
		Severity:     "high",
		SuggestedFix: "include context_pack file candidates in impact ranking",
	})
	if result.Error != "" {
		t.Fatalf("harness_feedback returned error: %v\n%s", result.Error, result.Output)
	}
	var report harnessFeedbackReport
	if err := json.Unmarshal([]byte(result.Output), &report); err != nil {
		t.Fatalf("decode feedback report: %v\n%s", err, result.Output)
	}
	if report.Category != "impact_ranking" {
		t.Fatalf("expected planning-quality category normalization, got %#v", report)
	}
	fields := ToolTelemetryFields(result.Tool, result.Output)
	logPath := filepath.Join(root, "logs", "telemetry", "feedback.jsonl")
	writeWorkspaceFile(t, logPath, strings.Join([]string{
		`{"type":"tool_request","tool":"harness_feedback","action":"harness_feedback"}`,
		mustMarshalTestJSON(t, map[string]any{
			"type":                "tool_result",
			"tool":                "harness_feedback",
			"error":               "",
			"summary":             result.Summary,
			"feedback_category":   fields["feedback_category"],
			"feedback_component":  fields["feedback_component"],
			"feedback_confidence": fields["feedback_confidence"],
			"feedback_severity":   fields["feedback_severity"],
		}),
		"",
	}, "\n"))

	result = mgr.Execute(context.Background(), ToolRequest{Tool: "harness_audit", Purpose: "planning_quality", Path: "logs/telemetry", Limit: 5})
	if result.Error != "" {
		t.Fatalf("harness_audit returned error: %v\n%s", result.Error, result.Output)
	}
	var audit harnessAuditReport
	if err := json.Unmarshal([]byte(result.Output), &audit); err != nil {
		t.Fatalf("decode audit report: %v\n%s", err, result.Output)
	}
	if audit.PlanningQuality == nil || audit.PlanningQuality.PlanningFeedback != 1 || audit.PlanningQuality.PlanningCategories["impact_ranking"] != 1 {
		t.Fatalf("expected planning-quality audit cluster, got %#v", audit.PlanningQuality)
	}
	if !slices.ContainsFunc(audit.Findings, func(finding harnessAuditFinding) bool {
		return finding.ID == "planning_quality_feedback_reported" && finding.Severity == "high"
	}) {
		t.Fatalf("expected planning-quality finding, got %#v", audit.Findings)
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
		filepath.Join(root, "logs", "conversations", "session.JSONL"),
		filepath.Join(root, "logs", "telemetry", "tool.JSONL"),
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
	if report.ContractCoverage != nil {
		t.Fatalf("expected no contract coverage without autonomy events, got %#v", report.ContractCoverage)
	}
	if strings.Contains(result.Output, `"contract_coverage"`) {
		t.Fatalf("expected no serialized contract_coverage without autonomy events, got %s", result.Output)
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
	if !slices.ContainsFunc(report.LogBehavior.Sources, func(source harnessAuditLogSource) bool {
		return source.Pattern == "logs/telemetry/*.jsonl" && source.Matched == 1 && source.Selected == 1
	}) {
		t.Fatalf("expected audit source coverage for telemetry logs, got %#v", report.LogBehavior.Sources)
	}
}

func TestHarnessAuditBehaviorIncludesContractCoverage(t *testing.T) {
	root := t.TempDir()
	contract, err := autonomy.NewContract(autonomy.ContractInput{
		Scope: autonomy.ContractScope{
			InvocationKind:    autonomy.InvocationSidecarCall,
			TaskFamily:        "sidecar_discovery",
			RuntimeContract:   "grounded_evidence_summary",
			PermissionProfile: "read_only_workspace",
			Sidecar: &autonomy.SidecarContext{
				Transport: "cli",
				Caller:    "codex",
				Profile:   "discovery",
				RequestID: "req-contract-coverage",
				Tool:      "context_pack",
			},
			ToolCall: &autonomy.ToolCallContext{
				ToolCallID: "tool-contract-coverage",
				ToolName:   "context_pack",
				Capability: autonomy.CapabilitySemanticDiscovery,
				SideEffect: autonomy.SideEffectReadOnly,
			},
		},
		Obligations: []autonomy.Obligation{
			{ObligationID: "obl-evidence", Kind: autonomy.ObligationEvidence, Status: autonomy.ObligationOpen, Owner: autonomy.OwnerSidecar, RequiredBy: "sidecar_request"},
		},
	})
	if err != nil {
		t.Fatalf("NewContract returned error: %v", err)
	}
	occurredAt := time.Date(2026, 5, 13, 21, 0, 0, 0, time.UTC)
	events := []autonomy.Event{
		mustWorkspaceAutonomyEvent(autonomy.NewContractCompiledEvent(contract, occurredAt, 1)),
		mustWorkspaceAutonomyEvent(autonomy.NewToolPolicyDecisionEvent(contract, autonomy.ContractPolicyEvent{
			ToolCallID: "tool-contract-coverage",
			ToolName:   "context_pack",
			Capability: autonomy.CapabilitySemanticDiscovery,
			SideEffect: autonomy.SideEffectReadOnly,
			Decision:   autonomy.PolicyObserve,
		}, occurredAt.Add(time.Second), 2)),
		mustWorkspaceAutonomyEvent(autonomy.NewObligationStateEvent(autonomy.EventObligationOpened, contract, autonomy.Obligation{
			ObligationID: "obl-evidence",
			Kind:         autonomy.ObligationEvidence,
			Status:       autonomy.ObligationOpen,
			Owner:        autonomy.OwnerSidecar,
			RequiredBy:   "sidecar_request",
		}, occurredAt.Add(2*time.Second), 3)),
		mustWorkspaceAutonomyEvent(autonomy.NewObligationStateEvent(autonomy.EventObligationResolved, contract, autonomy.Obligation{
			ObligationID: "obl-evidence",
			Kind:         autonomy.ObligationEvidence,
			Status:       autonomy.ObligationResolved,
			Owner:        autonomy.OwnerSidecar,
			RequiredBy:   "sidecar_request",
			EvidenceRef:  "event-context-pack-result",
		}, occurredAt.Add(3*time.Second), 4)),
	}
	var lines []string
	for _, event := range events {
		encoded, err := json.Marshal(event)
		if err != nil {
			t.Fatalf("marshal event: %v", err)
		}
		lines = append(lines, string(encoded))
	}
	gateEvent := mustWorkspaceAutonomyEvent(autonomy.NewGateDecisionEvent(contract, withWorkspaceGateAuditFields(t, autonomy.ContractGateEvent{
		Gate:                autonomy.GateReadOnly,
		Decision:            autonomy.PolicyObserve,
		InvariantID:         "sidecar.read_only_profile",
		ReasonCode:          "read_only_contract",
		ToolCallID:          "tool-contract-coverage",
		ToolName:            "context_pack",
		ObligationRefs:      []string{"obl-evidence"},
		ResolvedObligations: []string{"obl-evidence"},
		Source:              "harness_audit_test",
	}, string(autonomy.ContractPackCompletionReadiness), string(autonomy.EnforcementModeObserveOnly), []string{"resolved_obligation=obl-evidence"}, nil), occurredAt.Add(4*time.Second), 5))
	encodedGateEvent, err := json.Marshal(gateEvent)
	if err != nil {
		t.Fatalf("marshal gate event: %v", err)
	}
	wrappedGateLine, err := json.Marshal(map[string]string{
		"type":    "message",
		"role":    "autonomy_event",
		"content": string(encodedGateEvent),
	})
	if err != nil {
		t.Fatalf("marshal wrapped gate event: %v", err)
	}
	lines = append(lines, string(wrappedGateLine))
	frictionEvent := mustWorkspaceAutonomyEvent(autonomy.NewGuardFrictionEvent(contract, autonomy.ContractGuardFrictionEvent{
		Step:                 5,
		Lane:                 "sidecar",
		Phase:                "discovery",
		PhaseID:              "sidecar.discovery",
		ProtocolHash:         "protocol-test",
		InvariantID:          "completion.readiness",
		Subject:              autonomy.GuardFrictionSubjectFinalAnswer,
		Decision:             autonomy.GuardFrictionDecisionReject,
		GuardSurface:         autonomy.GuardFrictionSurfaceCompletionGate,
		FirstInvalidAction:   "final_answer",
		VisibleActionClasses: []string{"final_answer", "blocker"},
		VisibleTools:         []string{"context_pack"},
		SafeAlternatives:     []string{"<<QUESTION>>:"},
		ModelContractVisible: true,
		RetryOutcome:         autonomy.GuardFrictionRetryNotApplicable,
		Avoidability:         autonomy.GuardFrictionAvoidabilityUnknown,
		EvidenceRefs:         []string{"event-contract-coverage"},
	}, occurredAt.Add(5*time.Second), 6))
	encodedFrictionEvent, err := json.Marshal(frictionEvent)
	if err != nil {
		t.Fatalf("marshal friction event: %v", err)
	}
	wrappedFrictionLine, err := json.Marshal(map[string]string{
		"type":    "message",
		"role":    "autonomy_event",
		"content": string(encodedFrictionEvent),
	})
	if err != nil {
		t.Fatalf("marshal wrapped friction event: %v", err)
	}
	lines = append(lines, string(wrappedFrictionLine))
	lines = append(lines, "")
	writeWorkspaceFile(t, filepath.Join(root, "logs", "telemetry", "contracts.jsonl"), strings.Join(lines, "\n"))
	mgr := NewManager(config.Workspace{Root: root, CWD: root})

	result := mgr.Execute(context.Background(), ToolRequest{Tool: "harness_audit", Purpose: "behavior", Path: "logs/telemetry", Limit: 5})
	if result.Error != "" {
		t.Fatalf("harness_audit returned error: %v\n%s", result.Error, result.Output)
	}
	var report harnessAuditReport
	if err := json.Unmarshal([]byte(result.Output), &report); err != nil {
		t.Fatalf("decode audit report: %v\n%s", err, result.Output)
	}
	if report.ContractCoverage == nil || report.ContractCoverage.TotalEvents != 6 {
		t.Fatalf("expected contract coverage for six events, got %#v", report.ContractCoverage)
	}
	if report.LogBehavior == nil || report.LogBehavior.GuardFriction == nil ||
		report.LogBehavior.GuardFriction.Total != 1 ||
		report.LogBehavior.GuardFriction.UnclassifiedFriction != 1 {
		t.Fatalf("expected log behavior guard friction rollup, got %#v", report.LogBehavior)
	}
	if !report.ContractCoverage.AllClear ||
		report.ContractCoverage.PolicyCoverage.DecisionEvents != 1 ||
		report.ContractCoverage.ObligationCoverage.Resolved != 1 ||
		report.ContractCoverage.GateCoverage.DecisionEvents != 1 ||
		report.ContractCoverage.GateCoverage.KindCounts[string(autonomy.GateReadOnly)] != 1 ||
		report.ContractCoverage.GuardFrictionCoverage.Total != 1 ||
		report.ContractCoverage.GuardFrictionCoverage.GuardSurfaceCounts[string(autonomy.GuardFrictionSurfaceCompletionGate)] != 1 ||
		report.ContractCoverage.SidecarToolCorrelation.CorrelatedEventCount == 0 {
		t.Fatalf("unexpected contract coverage: %#v", report.ContractCoverage)
	}
	if len(report.ContractCoverage.GateCoverage.OpenObligationRefs) != 0 ||
		len(report.ContractCoverage.GateCoverage.InconsistentOpenObligationRefs) != 0 {
		t.Fatalf("expected resolved gate obligations with no open refs, got %#v", report.ContractCoverage.GateCoverage)
	}
	if !strings.Contains(result.Summary, "contract_events=6") {
		t.Fatalf("expected summary to include contract event count, got %q", result.Summary)
	}
}

func TestHarnessAuditIgnoresLegacyTurnPhaseProjectionContradictions(t *testing.T) {
	encoded := mustWorkspaceTextualTurnPhaseProjectionEvent(t, harnessAuditTurnPhaseProjectionIntroducedAt.Add(-time.Minute))

	events, warnings := decodeHarnessAuditAutonomyEvents([]byte(encoded))
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %#v", warnings)
	}
	report := autonomy.AuditEvents(events, autonomy.EventAuditOptions{})
	if report.TurnPhaseCoverage.Total != 0 {
		t.Fatalf("legacy turn-phase projection should not feed current coverage, got %#v", report.TurnPhaseCoverage)
	}
	if slices.ContainsFunc(report.Findings, func(finding autonomy.EventAuditFinding) bool {
		return finding.ID == "turn_phase_contract_contradiction"
	}) {
		t.Fatalf("legacy turn-phase projection should not create current contradiction findings, got %#v", report.Findings)
	}

	historicalEvents, historicalWarnings := decodeHarnessAuditAutonomyEventsWithOptions([]byte(encoded), harnessAuditAutonomyDecodeOptions{
		PreserveLegacyTurnPhaseProjection: true,
	})
	if len(historicalWarnings) != 0 {
		t.Fatalf("unexpected historical warnings: %#v", historicalWarnings)
	}
	historicalFindings := historicalTurnPhaseProjectionFindingsFromEvents(historicalEvents)
	if !slices.ContainsFunc(historicalFindings, func(finding harnessAuditFinding) bool {
		return finding.ID == "historical_turn_phase_contract_contradiction" &&
			strings.Contains(finding.Evidence, "pre-current turn-phase projection contradiction(s)")
	}) {
		t.Fatalf("legacy turn-phase contradiction should be retained as historical finding")
	}
}

func TestHarnessAuditStillFlagsCurrentTurnPhaseProjectionContradictions(t *testing.T) {
	encoded := mustWorkspaceTextualTurnPhaseProjectionEvent(t, harnessAuditTurnPhaseProjectionIntroducedAt.Add(time.Minute))

	events, warnings := decodeHarnessAuditAutonomyEvents([]byte(encoded))
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %#v", warnings)
	}
	report := autonomy.AuditEvents(events, autonomy.EventAuditOptions{})
	if report.TurnPhaseCoverage.Total == 0 {
		t.Fatalf("current turn-phase projection should feed coverage")
	}
	if !slices.ContainsFunc(report.Findings, func(finding autonomy.EventAuditFinding) bool {
		return finding.ID == "turn_phase_contract_contradiction"
	}) {
		t.Fatalf("current malformed turn-phase projection should still be flagged, got %#v", report.Findings)
	}
}

func mustWorkspaceTextualTurnPhaseProjectionEvent(t *testing.T, occurredAt time.Time) string {
	t.Helper()
	contract, err := autonomy.NewContract(autonomy.ContractInput{
		Scope: autonomy.ContractScope{
			InvocationKind:    autonomy.InvocationLane,
			Lane:              "explore",
			LaneRunID:         "turn-phase-projection-session",
			TaskFamily:        "debug_logs",
			RuntimeContract:   "grounded_explanation",
			PermissionProfile: "read_only_workspace",
		},
	})
	if err != nil {
		t.Fatalf("NewContract returned error: %v", err)
	}
	phase := autonomy.TurnPhaseContractEvent{
		SchemaVersion: "idiom.turn_phase_contract.v1",
		ContractID:    contract.ContractID,
		PhaseID:       "generic",
		ModelCallID:   contract.ContractID + ":model_call_1",
		ProtocolHash:  "sha256:turn-phase-projection-test",
		Phase:         "generic",
		AllowedTools:  []string{"read_file"},
		ActionPolicy: autonomy.TurnActionClassPolicyEvent{
			AllowedActionClasses:   []string{"final_answer", "read"},
			TerminalResponseShapes: []string{"final answer"},
			CallableToolNames:      []string{"read_file"},
		},
		RuntimeCall: autonomy.RuntimeCallContractEvent{
			ToolTransportMode:        "textual",
			ExpectedVisibleOutput:    "visible_text_tool_request",
			ExpressibleActionClasses: []string{"final_answer"},
		},
		TerminalOutcomes: []string{"final answer"},
		EnforcementMode:  "observe_only",
	}
	event := mustWorkspaceAutonomyEvent(autonomy.NewContractPresentedEvent(contract, autonomy.ContractLifecycleEvent{
		Reason:            "turn_phase_contract",
		ProtocolHash:      phase.ProtocolHash,
		TurnPhaseContract: &phase,
		SurfaceSummaries: []autonomy.TurnPhaseSurfaceSummaryEvent{
			autonomy.SurfaceSummaryFromTurnPhaseContract(phase),
		},
	}, occurredAt, 1))
	return mustMarshalWorkspaceAutonomyEvent(t, event)
}

func TestHarnessAuditBackfillsLegacyGatePackAndEnforcementMode(t *testing.T) {
	contract, err := autonomy.NewContract(autonomy.ContractInput{
		Scope: autonomy.ContractScope{
			InvocationKind: autonomy.InvocationLane,
			Lane:           "implement",
			LaneRunID:      "legacy-gate-session",
		},
	})
	if err != nil {
		t.Fatalf("NewContract returned error: %v", err)
	}
	gateEvent := mustWorkspaceAutonomyEvent(autonomy.NewGateDecisionEvent(contract, autonomy.ContractGateEvent{
		Gate:        autonomy.GateReadOnly,
		Decision:    autonomy.PolicyObserve,
		InvariantID: "read_only.readiness",
		ReasonCode:  "read_only_contract",
		Source:      "legacy_harness_audit_test",
	}, harnessAuditGateMetadataIntroducedAt.Add(-time.Minute), 1))
	encoded := mustMarshalWorkspaceAutonomyEvent(t, gateEvent)
	legacy := mutateWorkspaceAutonomyGateFields(t, encoded, func(gate map[string]any) {
		delete(gate, "pack_id")
		delete(gate, "enforcement_mode")
	})

	events, warnings := decodeHarnessAuditAutonomyEvents([]byte(legacy))
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %#v", warnings)
	}
	report := autonomy.AuditEvents(events, autonomy.EventAuditOptions{})
	if len(report.GateCoverage.MissingPackIDRefs) != 0 || len(report.GateCoverage.MissingEnforcementModeRefs) != 0 {
		t.Fatalf("legacy absent fields should be backfilled, got %#v", report.GateCoverage)
	}
	if report.GateCoverage.DecisionCountsByPackID[string(autonomy.ContractPackCompletionReadiness)] != 1 ||
		report.GateCoverage.DecisionCountsByEnforcementMode[string(autonomy.EnforcementModeObserveOnly)] != 1 {
		t.Fatalf("expected backfilled pack/mode counts, got %#v", report.GateCoverage)
	}
}

func TestHarnessAuditStillFlagsCurrentAbsentGatePackAndEnforcementMode(t *testing.T) {
	contract, err := autonomy.NewContract(autonomy.ContractInput{
		Scope: autonomy.ContractScope{
			InvocationKind: autonomy.InvocationLane,
			Lane:           "implement",
			LaneRunID:      "current-absent-gate-session",
		},
	})
	if err != nil {
		t.Fatalf("NewContract returned error: %v", err)
	}
	gateEvent := mustWorkspaceAutonomyEvent(autonomy.NewGateDecisionEvent(contract, autonomy.ContractGateEvent{
		Gate:        autonomy.GateReadOnly,
		Decision:    autonomy.PolicyObserve,
		InvariantID: "read_only.readiness",
		ReasonCode:  "read_only_contract",
		Source:      "current_harness_audit_test",
	}, harnessAuditGateMetadataIntroducedAt.Add(time.Minute), 1))
	encoded := mustMarshalWorkspaceAutonomyEvent(t, gateEvent)
	currentAbsent := mutateWorkspaceAutonomyGateFields(t, encoded, func(gate map[string]any) {
		delete(gate, "pack_id")
		delete(gate, "enforcement_mode")
	})

	events, warnings := decodeHarnessAuditAutonomyEvents([]byte(currentAbsent))
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %#v", warnings)
	}
	report := autonomy.AuditEvents(events, autonomy.EventAuditOptions{})
	if len(report.GateCoverage.MissingPackIDRefs) != 1 || len(report.GateCoverage.MissingEnforcementModeRefs) != 1 {
		t.Fatalf("current absent fields should still be flagged, got %#v", report.GateCoverage)
	}
}

func TestHarnessAuditStillFlagsExplicitEmptyGatePackAndEnforcementMode(t *testing.T) {
	contract, err := autonomy.NewContract(autonomy.ContractInput{
		Scope: autonomy.ContractScope{
			InvocationKind: autonomy.InvocationLane,
			Lane:           "implement",
			LaneRunID:      "explicit-empty-gate-session",
		},
	})
	if err != nil {
		t.Fatalf("NewContract returned error: %v", err)
	}
	gateEvent := mustWorkspaceAutonomyEvent(autonomy.NewGateDecisionEvent(contract, autonomy.ContractGateEvent{
		Gate:        autonomy.GateReadOnly,
		Decision:    autonomy.PolicyObserve,
		InvariantID: "read_only.readiness",
		ReasonCode:  "read_only_contract",
		Source:      "legacy_harness_audit_test",
	}, harnessAuditGateMetadataIntroducedAt.Add(-time.Minute), 1))
	encoded := mustMarshalWorkspaceAutonomyEvent(t, gateEvent)
	explicitEmpty := mutateWorkspaceAutonomyGateFields(t, encoded, func(gate map[string]any) {
		gate["pack_id"] = ""
		gate["enforcement_mode"] = ""
	})

	events, warnings := decodeHarnessAuditAutonomyEvents([]byte(explicitEmpty))
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %#v", warnings)
	}
	report := autonomy.AuditEvents(events, autonomy.EventAuditOptions{})
	if len(report.GateCoverage.MissingPackIDRefs) != 1 || len(report.GateCoverage.MissingEnforcementModeRefs) != 1 {
		t.Fatalf("explicit empty fields should still be flagged, got %#v", report.GateCoverage)
	}
}

func TestHarnessAuditBackfillsLegacyBlockingGateSafeAlternatives(t *testing.T) {
	contract, err := autonomy.NewContract(autonomy.ContractInput{
		Scope: autonomy.ContractScope{
			InvocationKind: autonomy.InvocationLane,
			Lane:           "implement",
			LaneRunID:      "legacy-blocking-gate-session",
		},
	})
	if err != nil {
		t.Fatalf("NewContract returned error: %v", err)
	}
	gateEvent := mustWorkspaceAutonomyEvent(autonomy.NewGateDecisionEvent(contract, autonomy.ContractGateEvent{
		Gate:        autonomy.GateCompletion,
		Decision:    autonomy.PolicyReject,
		InvariantID: "completion.readiness",
		ReasonCode:  "required_obligations_open",
		Source:      "legacy_harness_audit_test",
	}, harnessAuditGateMetadataIntroducedAt.Add(-time.Minute), 1))
	encoded := mustMarshalWorkspaceAutonomyEvent(t, gateEvent)
	legacy := mutateWorkspaceAutonomyGateFields(t, encoded, func(gate map[string]any) {
		delete(gate, "pack_id")
		delete(gate, "enforcement_mode")
		delete(gate, "safe_alternatives")
	})

	events, warnings := decodeHarnessAuditAutonomyEvents([]byte(legacy))
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %#v", warnings)
	}
	report := autonomy.AuditEvents(events, autonomy.EventAuditOptions{})
	if len(report.GateCoverage.MissingPackIDRefs) != 0 ||
		len(report.GateCoverage.MissingEnforcementModeRefs) != 0 ||
		len(report.GateCoverage.MissingSafeAlternativesRefs) != 0 {
		t.Fatalf("legacy blocking gate fields should be backfilled, got %#v", report.GateCoverage)
	}
	if report.GateCoverage.EventsWithSafeAlternatives != 1 {
		t.Fatalf("expected backfilled safe alternatives count, got %#v", report.GateCoverage)
	}
}

func TestHarnessAuditStillFlagsCurrentAbsentBlockingGateSafeAlternatives(t *testing.T) {
	contract, err := autonomy.NewContract(autonomy.ContractInput{
		Scope: autonomy.ContractScope{
			InvocationKind: autonomy.InvocationLane,
			Lane:           "implement",
			LaneRunID:      "current-absent-safe-alternatives-session",
		},
	})
	if err != nil {
		t.Fatalf("NewContract returned error: %v", err)
	}
	gateEvent := mustWorkspaceAutonomyEvent(autonomy.NewGateDecisionEvent(contract, autonomy.ContractGateEvent{
		Gate:            autonomy.GateCompletion,
		Decision:        autonomy.PolicyReject,
		PackID:          autonomy.ContractPackCompletionReadiness,
		EnforcementMode: autonomy.EnforcementModeEnforce,
		InvariantID:     "completion.readiness",
		ReasonCode:      "required_obligations_open",
		Source:          "current_harness_audit_test",
	}, harnessAuditGateMetadataIntroducedAt.Add(time.Minute), 1))
	encoded := mustMarshalWorkspaceAutonomyEvent(t, gateEvent)
	currentAbsent := mutateWorkspaceAutonomyGateFields(t, encoded, func(gate map[string]any) {
		delete(gate, "safe_alternatives")
	})

	events, warnings := decodeHarnessAuditAutonomyEvents([]byte(currentAbsent))
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %#v", warnings)
	}
	report := autonomy.AuditEvents(events, autonomy.EventAuditOptions{})
	if len(report.GateCoverage.MissingSafeAlternativesRefs) != 1 {
		t.Fatalf("current absent safe alternatives should still be flagged, got %#v", report.GateCoverage)
	}
}

func TestHarnessAuditStillFlagsExplicitEmptyBlockingGateSafeAlternatives(t *testing.T) {
	contract, err := autonomy.NewContract(autonomy.ContractInput{
		Scope: autonomy.ContractScope{
			InvocationKind: autonomy.InvocationLane,
			Lane:           "implement",
			LaneRunID:      "explicit-empty-safe-alternatives-session",
		},
	})
	if err != nil {
		t.Fatalf("NewContract returned error: %v", err)
	}
	gateEvent := mustWorkspaceAutonomyEvent(autonomy.NewGateDecisionEvent(contract, autonomy.ContractGateEvent{
		Gate:            autonomy.GateCompletion,
		Decision:        autonomy.PolicyReject,
		PackID:          autonomy.ContractPackCompletionReadiness,
		EnforcementMode: autonomy.EnforcementModeEnforce,
		InvariantID:     "completion.readiness",
		ReasonCode:      "required_obligations_open",
		Source:          "legacy_harness_audit_test",
	}, harnessAuditGateMetadataIntroducedAt.Add(-time.Minute), 1))
	encoded := mustMarshalWorkspaceAutonomyEvent(t, gateEvent)
	explicitEmpty := mutateWorkspaceAutonomyGateFields(t, encoded, func(gate map[string]any) {
		gate["safe_alternatives"] = []any{}
	})

	events, warnings := decodeHarnessAuditAutonomyEvents([]byte(explicitEmpty))
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %#v", warnings)
	}
	report := autonomy.AuditEvents(events, autonomy.EventAuditOptions{})
	if len(report.GateCoverage.MissingSafeAlternativesRefs) != 1 {
		t.Fatalf("explicit empty safe alternatives should still be flagged, got %#v", report.GateCoverage)
	}
}

func TestHarnessAuditContractCoverageSortsEventsBeforeAuditing(t *testing.T) {
	root := t.TempDir()
	contract, err := autonomy.NewContract(autonomy.ContractInput{
		Scope: autonomy.ContractScope{
			InvocationKind:  autonomy.InvocationLane,
			Lane:            "implement",
			RuntimeContract: "implementation_result",
		},
		Obligations: []autonomy.Obligation{
			{ObligationID: "obl-write-file", Kind: autonomy.ObligationWrite, Status: autonomy.ObligationOpen, Owner: autonomy.OwnerLane, RequiredBy: "required_edit_paths"},
		},
	})
	if err != nil {
		t.Fatalf("NewContract returned error: %v", err)
	}
	occurredAt := time.Date(2026, 5, 13, 21, 0, 0, 0, time.UTC)
	compiled := mustWorkspaceAutonomyEvent(autonomy.NewContractCompiledEvent(contract, occurredAt, 1))
	opened := mustWorkspaceAutonomyEvent(autonomy.NewObligationStateEvent(autonomy.EventObligationOpened, contract, autonomy.Obligation{
		ObligationID: "obl-write-file",
		Kind:         autonomy.ObligationWrite,
		Status:       autonomy.ObligationOpen,
		Owner:        autonomy.OwnerLane,
		RequiredBy:   "required_edit_paths",
	}, occurredAt.Add(time.Second), 2))
	resolved := mustWorkspaceAutonomyEvent(autonomy.NewObligationStateEvent(autonomy.EventObligationResolved, contract, autonomy.Obligation{
		ObligationID: "obl-write-file",
		Kind:         autonomy.ObligationWrite,
		Status:       autonomy.ObligationResolved,
		Owner:        autonomy.OwnerLane,
		RequiredBy:   "required_edit_paths",
		EvidenceRef:  "event-write",
	}, occurredAt.Add(2*time.Second), 3))
	openPath := filepath.Join(root, "logs", "telemetry", "open.jsonl")
	resolvePath := filepath.Join(root, "logs", "telemetry", "resolve.jsonl")
	writeWorkspaceFile(t, openPath, strings.Join([]string{
		mustMarshalWorkspaceAutonomyEvent(t, compiled),
		mustMarshalWorkspaceAutonomyEvent(t, opened),
		"",
	}, "\n"))
	writeWorkspaceFile(t, resolvePath, strings.Join([]string{
		mustMarshalWorkspaceAutonomyEvent(t, resolved),
		mustMarshalWorkspaceAutonomyEvent(t, resolved),
		"",
	}, "\n"))
	mgr := NewManager(config.Workspace{Root: root, CWD: root})

	report, warnings := mgr.auditContractEventCoverage([]string{resolvePath, openPath})
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %#v", warnings)
	}
	if report == nil {
		t.Fatal("expected contract coverage report")
	}
	if len(report.ObligationCoverage.UnmatchedObligations) != 0 {
		t.Fatalf("out-of-order and duplicate autonomy events should not create unmatched obligations: %#v", report.ObligationCoverage.UnmatchedObligations)
	}
	if report.TotalEvents != 3 || report.ObligationCoverage.Resolved != 1 {
		t.Fatalf("expected sorted/deduped event coverage, got %#v", report)
	}
}

func TestHarnessAuditBehaviorRejectsInvalidAutonomyEvents(t *testing.T) {
	root := t.TempDir()
	lines := []string{
		`{"schema_version":"idiom.autonomy.event.v1","event_id":"event-invalid","type":"not_a_real_event","effect":"observe_only","occurred_at":"2026-05-13T21:01:00Z","sequence":1,"contract_id":"contract_invalid","lifecycle":{"reason":"bad type"}}`,
		"",
	}
	writeWorkspaceFile(t, filepath.Join(root, "logs", "telemetry", "contracts.jsonl"), strings.Join(lines, "\n"))
	mgr := NewManager(config.Workspace{Root: root, CWD: root})

	result := mgr.Execute(context.Background(), ToolRequest{Tool: "harness_audit", Purpose: "behavior", Path: "logs/telemetry", Limit: 5})
	if result.Error != "" {
		t.Fatalf("harness_audit returned error: %v\n%s", result.Error, result.Output)
	}
	var report harnessAuditReport
	if err := json.Unmarshal([]byte(result.Output), &report); err != nil {
		t.Fatalf("decode audit report: %v\n%s", err, result.Output)
	}
	if report.ContractCoverage != nil {
		t.Fatalf("invalid autonomy event should not enter contract coverage, got %#v", report.ContractCoverage)
	}
	if report.LogBehavior == nil || !slices.ContainsFunc(report.LogBehavior.Warnings, func(warning string) bool {
		return strings.Contains(warning, "invalid autonomy event")
	}) {
		t.Fatalf("expected invalid autonomy event warning, got %#v", report.LogBehavior)
	}
	if !slices.ContainsFunc(report.Findings, func(finding harnessAuditFinding) bool {
		return finding.ID == "contract_event_invalid" && finding.Severity == "medium"
	}) {
		t.Fatalf("expected invalid contract event finding, got %#v", report.Findings)
	}
}

func mustWorkspaceAutonomyEvent(event autonomy.Event, err error) autonomy.Event {
	if err != nil {
		panic(err)
	}
	return event
}

func mustMarshalWorkspaceAutonomyEvent(t *testing.T, event autonomy.Event) string {
	t.Helper()
	encoded, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	return string(encoded)
}

func mutateWorkspaceAutonomyGateFields(t *testing.T, encoded string, mutate func(map[string]any)) string {
	t.Helper()
	var record map[string]any
	if err := json.Unmarshal([]byte(encoded), &record); err != nil {
		t.Fatalf("decode event: %v", err)
	}
	gate, ok := record["gate"].(map[string]any)
	if !ok {
		t.Fatalf("event has no gate object: %s", encoded)
	}
	mutate(gate)
	out, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("marshal mutated event: %v", err)
	}
	return string(out)
}

func TestHarnessAuditBehaviorDefaultIncludesIdiomConversationLogs(t *testing.T) {
	root := t.TempDir()
	logPath := filepath.Join(root, ".idiom", "logs", "conversations", "session.jsonl")
	writeWorkspaceFile(t, logPath, strings.Join([]string{
		`{"type":"message","role":"user","content":"write the brief"}`,
		`{"type":"tool_result","tool":"completion_gate","step":3,"summary":"Mutation completion gate rejected final answer","error":"this task asks for a workspace change, but no successful write tool result is present"}`,
		`{"type":"tool_request","tool":"read_file","action":"read_file:native/src/example.cpp","path":"native/src/example.cpp"}`,
		`{"type":"tool_result","tool":"read_file","summary":"Read native/src/example.cpp","error":""}`,
		"",
	}, "\n"))
	mgr := NewManager(config.Workspace{Root: root, CWD: root})

	result := mgr.Execute(context.Background(), ToolRequest{Tool: "harness_audit", Purpose: "behavior", Limit: 5})
	if result.Error != "" {
		t.Fatalf("harness_audit returned error: %v\n%s", result.Error, result.Output)
	}
	var report harnessAuditReport
	if err := json.Unmarshal([]byte(result.Output), &report); err != nil {
		t.Fatalf("decode audit report: %v\n%s", err, result.Output)
	}
	if report.LogBehavior == nil || report.LogBehavior.LogsScanned != 1 {
		t.Fatalf("expected idiom conversation log to be scanned, got %#v", report.LogBehavior)
	}
	if !slices.ContainsFunc(report.LogBehavior.Sources, func(source harnessAuditLogSource) bool {
		return source.Pattern == ".idiom/logs/conversations/*.jsonl" && source.Matched == 1 && source.Selected == 1
	}) {
		t.Fatalf("expected audit source coverage for .idiom conversation logs, got %#v", report.LogBehavior.Sources)
	}
	if !slices.ContainsFunc(report.LogBehavior.TopPatterns, func(hit eval.PerfPatternHit) bool {
		return hit.Name == "completion_gate_reopened_discovery" && hit.TotalCount == 1
	}) {
		t.Fatalf("expected completion_gate_reopened_discovery pattern, got %#v", report.LogBehavior.TopPatterns)
	}
	questions, err := json.Marshal(report.ImprovementQuestions)
	if err != nil {
		t.Fatalf("marshal improvement questions: %v", err)
	}
	if !strings.Contains(string(questions), "recovery contract outcomes") {
		t.Fatalf("expected behavior audit to describe recovery contract outcomes, got %s", questions)
	}
	if strings.Contains(string(questions), "write the required artifact") || strings.Contains(string(questions), "next-action state") {
		t.Fatalf("behavior audit should avoid strategy-prescriptive completion recovery text, got %s", questions)
	}
}

func TestHarnessAuditDefaultLimitIsPerLogSource(t *testing.T) {
	root := t.TempDir()
	conversationPath := filepath.Join(root, "logs", "conversations", "session.jsonl")
	writeWorkspaceFile(t, conversationPath, strings.Join([]string{
		`{"type":"message","role":"user","content":"find missing code"}`,
		`{"type":"tool_request","tool":"search_files","action":"search_files:_:Nope:_"}`,
		`{"type":"tool_result","tool":"search_files","summary":"No file matches for \"Nope\"","error":""}`,
		"",
	}, "\n"))
	baseTime := time.Now().Add(-time.Hour)
	if err := os.Chtimes(conversationPath, baseTime, baseTime); err != nil {
		t.Fatalf("chtimes conversation log: %v", err)
	}
	for i := 0; i < 3; i++ {
		telemetryPath := filepath.Join(root, "logs", "telemetry", fmt.Sprintf("tool-%d.jsonl", i))
		writeWorkspaceFile(t, telemetryPath, strings.Join([]string{
			`{"type":"session_start","command":"tool:pwd"}`,
			`{"type":"tool_request","tool":"pwd","action":"pwd"}`,
			`{"type":"tool_result","tool":"pwd","summary":"Printed current working directory","error":""}`,
			"",
		}, "\n"))
		telemetryTime := baseTime.Add(time.Duration(i+1) * time.Minute)
		if err := os.Chtimes(telemetryPath, telemetryTime, telemetryTime); err != nil {
			t.Fatalf("chtimes telemetry log: %v", err)
		}
	}
	mgr := NewManager(config.Workspace{Root: root, CWD: root})

	result := mgr.Execute(context.Background(), ToolRequest{Tool: "harness_audit", Purpose: "behavior", Limit: 2})
	if result.Error != "" {
		t.Fatalf("harness_audit returned error: %v\n%s", result.Error, result.Output)
	}
	var report harnessAuditReport
	if err := json.Unmarshal([]byte(result.Output), &report); err != nil {
		t.Fatalf("decode audit report: %v\n%s", err, result.Output)
	}
	if report.LogBehavior == nil || report.LogBehavior.LogsScanned != 3 {
		t.Fatalf("expected one conversation plus two telemetry logs, got %#v", report.LogBehavior)
	}
	if !slices.ContainsFunc(report.LogBehavior.TopPatterns, func(hit eval.PerfPatternHit) bool {
		return hit.Name == "zero_hit_search" && hit.TotalCount == 1
	}) {
		t.Fatalf("expected default audit to retain conversation behavior findings, got %#v", report.LogBehavior.TopPatterns)
	}
	if !slices.Contains(report.LogBehavior.Warnings, "log scan capped at 2 newest file(s) out of 3 matched for logs/telemetry/*.jsonl") {
		t.Fatalf("expected per-source telemetry cap warning, got %#v", report.LogBehavior.Warnings)
	}
}

func TestHarnessAuditAllowsLargeExplicitLogLimit(t *testing.T) {
	if got := harnessAuditLogLimit(3000); got != 3000 {
		t.Fatalf("expected explicit all-log audit limit to be honored, got %d", got)
	}
	if got := harnessAuditLogLimit(harnessAuditMaxLogLimit + 1); got != harnessAuditMaxLogLimit {
		t.Fatalf("expected extreme limit to cap at %d, got %d", harnessAuditMaxLogLimit, got)
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
		fmt.Sprintf(`{"type":"process_start","command":"implement","pid":%d,"workspace_root":"%s"}`, harnessAuditTestStoppedPID(t), root),
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

func TestHarnessAuditAcceptsModelCloseoutTelemetryAsSessionSummaryEvidence(t *testing.T) {
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
		fmt.Sprintf(`{"type":"process_start","command":"implement","pid":%d,"workspace_root":"%s"}`, harnessAuditTestStoppedPID(t), root),
		`{"type":"model_session","session_id":"session","debug_log_path":"` + conversationPath + `"}`,
		`{"type":"model_session_closeout","session_id":"session","debug_log_path":"` + conversationPath + `","summary_event":"session_summary","model_closed":true}`,
		`{"type":"process_exit","command":"implement","exit_code":0}`,
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
	if report.LogBehavior != nil && len(report.LogBehavior.IncompleteSessions) != 0 {
		t.Fatalf("closeout telemetry should satisfy session-summary lifecycle evidence, got %#v", report.LogBehavior.IncompleteSessions)
	}
	if slices.ContainsFunc(report.Findings, func(finding harnessAuditFinding) bool {
		return finding.ID == "incomplete_implement_session_lifecycle"
	}) {
		t.Fatalf("expected no incomplete lifecycle finding with model closeout telemetry, got %#v", report.Findings)
	}
}

func harnessAuditTestStoppedPID(t *testing.T) int {
	t.Helper()
	for pid := 999999; pid > 900000; pid-- {
		if !processIDAppearsRunning(pid) {
			return pid
		}
	}
	t.Fatal("could not find a non-running PID for harness audit fixture")
	return 0
}

func TestHarnessAuditClassifiesActiveImplementLifecycleAsInProgress(t *testing.T) {
	root := t.TempDir()
	conversationPath := filepath.Join(root, ".idiom", "logs", "conversations", "active.jsonl")
	telemetryPath := filepath.Join(root, "logs", "telemetry", "active-implement.jsonl")
	writeWorkspaceFile(t, conversationPath, strings.Join([]string{
		`{"type":"session_start","workspace_root":"` + root + `"}`,
		`{"type":"message","role":"control_rails","content":"{\"contract\":\"implementation_result\",\"task_family\":\"implementation\"}"}`,
		"",
	}, "\n"))
	writeWorkspaceFile(t, telemetryPath, strings.Join([]string{
		`{"type":"session_start","command":"implement","workspace_root":"` + root + `"}`,
		fmt.Sprintf(`{"type":"process_start","command":"implement","pid":%d,"workspace_root":"%s"}`, os.Getpid(), root),
		`{"type":"model_session","session_id":"active","debug_log_path":"` + conversationPath + `"}`,
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
	if report.LogBehavior == nil || len(report.LogBehavior.InProgressSessions) != 1 {
		t.Fatalf("expected one in-progress session, got %#v", report.LogBehavior)
	}
	if len(report.LogBehavior.IncompleteSessions) != 0 {
		t.Fatalf("active session should not be incomplete, got %#v", report.LogBehavior.IncompleteSessions)
	}
	if slices.ContainsFunc(report.Findings, func(finding harnessAuditFinding) bool {
		return finding.ID == "incomplete_implement_session_lifecycle"
	}) {
		t.Fatalf("expected no incomplete lifecycle finding for active process, got %#v", report.Findings)
	}
}

func TestHarnessAuditClassifiesActiveTUIImplementationConversationAsInProgress(t *testing.T) {
	root := t.TempDir()
	conversationPath := filepath.Join(root, "logs", "conversations", "active-tui.jsonl")
	telemetryPath := filepath.Join(root, "logs", "telemetry", "active-tui.jsonl")
	writeWorkspaceFile(t, conversationPath, strings.Join([]string{
		`{"type":"session_start","workspace_root":"` + root + `"}`,
		`{"type":"message","role":"control_rails","content":"{\"contract\":\"implementation_result\",\"task_family\":\"implementation\"}"}`,
		"",
	}, "\n"))
	writeWorkspaceFile(t, telemetryPath, strings.Join([]string{
		`{"type":"session_start","command":"tui","workspace_root":"` + root + `"}`,
		fmt.Sprintf(`{"type":"process_start","command":"tui","pid":%d,"workspace_root":"%s"}`, os.Getpid(), root),
		`{"type":"model_session","session_id":"active-tui","debug_log_path":"` + conversationPath + `"}`,
		"",
	}, "\n"))
	mgr := NewManager(config.Workspace{Root: root, CWD: root})

	result := mgr.Execute(context.Background(), ToolRequest{Tool: "harness_audit", Purpose: "behavior", Limit: 5})
	if result.Error != "" {
		t.Fatalf("harness_audit returned error: %v\n%s", result.Error, result.Output)
	}
	var report harnessAuditReport
	if err := json.Unmarshal([]byte(result.Output), &report); err != nil {
		t.Fatalf("decode audit report: %v\n%s", err, result.Output)
	}
	if report.LogBehavior == nil || len(report.LogBehavior.InProgressSessions) != 1 {
		t.Fatalf("expected one in-progress TUI session, got %#v", report.LogBehavior)
	}
	session := report.LogBehavior.InProgressSessions[0]
	if session.TelemetryLog != "logs/telemetry/active-tui.jsonl" ||
		session.ConversationLog != "logs/conversations/active-tui.jsonl" ||
		session.Command != "tui" {
		t.Fatalf("unexpected in-progress TUI session: %#v", session)
	}
	if len(report.LogBehavior.IncompleteSessions) != 0 {
		t.Fatalf("active TUI conversation should not be incomplete, got %#v", report.LogBehavior.IncompleteSessions)
	}
	if slices.ContainsFunc(report.Findings, func(finding harnessAuditFinding) bool {
		return finding.ID == "incomplete_implement_session_lifecycle"
	}) {
		t.Fatalf("expected no incomplete lifecycle finding for active TUI process, got %#v", report.Findings)
	}
}

func TestHarnessAuditLinksStoppedTUIImplementationConversationToTelemetryLifecycle(t *testing.T) {
	root := t.TempDir()
	conversationPath := filepath.Join(root, "logs", "conversations", "20260520-090102.111.jsonl")
	telemetryPath := filepath.Join(root, "logs", "telemetry", "20260520-090101.999-tui-123.jsonl")
	writeWorkspaceFile(t, conversationPath, strings.Join([]string{
		`{"type":"session_start","workspace_root":"` + root + `"}`,
		`{"type":"message","role":"control_rails","content":"{\"contract\":\"implementation_result\",\"task_family\":\"implementation\"}"}`,
		"",
	}, "\n"))
	writeWorkspaceFile(t, telemetryPath, strings.Join([]string{
		`{"type":"session_start","command":"tui","workspace_root":"` + root + `"}`,
		fmt.Sprintf(`{"type":"process_start","command":"tui","pid":%d,"workspace_root":"%s"}`, harnessAuditTestStoppedPID(t), root),
		`{"type":"model_session","session_id":"20260520-090102.111","debug_log_path":"` + conversationPath + `"}`,
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
	if report.LogBehavior == nil || len(report.LogBehavior.IncompleteSessions) != 1 {
		t.Fatalf("expected one incomplete TUI-backed implementation session, got %#v", report.LogBehavior)
	}
	session := report.LogBehavior.IncompleteSessions[0]
	if session.TelemetryLog != "logs/telemetry/20260520-090101.999-tui-123.jsonl" ||
		session.ConversationLog != "logs/conversations/20260520-090102.111.jsonl" ||
		session.Command != "tui" ||
		!slices.Contains(session.Missing, "process_exit") ||
		!slices.Contains(session.Missing, "session_summary") {
		t.Fatalf("unexpected stopped TUI lifecycle session: %#v", session)
	}
	if slices.ContainsFunc(report.Findings, func(finding harnessAuditFinding) bool {
		return finding.ID == "incomplete_implement_session_lifecycle"
	}) {
		t.Fatalf("pre-closeout TUI lifecycle gaps should not raise current high finding, got %#v", report.Findings)
	}
	if !slices.ContainsFunc(report.HistoricalFindings, func(finding harnessAuditFinding) bool {
		return finding.ID == "historical_incomplete_implement_session_lifecycle" && finding.Severity == "info"
	}) {
		t.Fatalf("expected historical lifecycle finding, got %#v", report.HistoricalFindings)
	}
}

func TestHarnessAuditTimestampFromLabelParsesDatedReplayLogs(t *testing.T) {
	got, ok := harnessAuditTimestampFromLabel("tool-governance-scripted-contract-replay-20260525")
	if !ok {
		t.Fatal("expected dated replay label to parse")
	}
	want := time.Date(2026, 5, 25, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("timestamp = %s, want %s", got, want)
	}
}

func TestHarnessAuditAcceptsTUIModelCloseoutTelemetryAsSessionSummaryEvidence(t *testing.T) {
	root := t.TempDir()
	conversationPath := filepath.Join(root, "logs", "conversations", "closed-tui.jsonl")
	telemetryPath := filepath.Join(root, "logs", "telemetry", "closed-tui.jsonl")
	writeWorkspaceFile(t, conversationPath, strings.Join([]string{
		`{"type":"session_start","workspace_root":"` + root + `"}`,
		`{"type":"message","role":"control_rails","content":"{\"contract\":\"implementation_result\",\"task_family\":\"implementation\"}"}`,
		"",
	}, "\n"))
	writeWorkspaceFile(t, telemetryPath, strings.Join([]string{
		`{"type":"session_start","command":"tui","workspace_root":"` + root + `"}`,
		fmt.Sprintf(`{"type":"process_start","command":"tui","pid":%d,"workspace_root":"%s"}`, harnessAuditTestStoppedPID(t), root),
		`{"type":"model_session","session_id":"closed-tui","debug_log_path":"` + conversationPath + `"}`,
		`{"type":"model_session_closeout","session_id":"closed-tui","debug_log_path":"` + conversationPath + `","summary_event":"session_summary","model_closed":true}`,
		`{"type":"process_exit","command":"tui","exit_code":0}`,
		"",
	}, "\n"))
	mgr := NewManager(config.Workspace{Root: root, CWD: root})

	result := mgr.Execute(context.Background(), ToolRequest{Tool: "harness_audit", Purpose: "behavior", Limit: 5})
	if result.Error != "" {
		t.Fatalf("harness_audit returned error: %v\n%s", result.Error, result.Output)
	}
	var report harnessAuditReport
	if err := json.Unmarshal([]byte(result.Output), &report); err != nil {
		t.Fatalf("decode audit report: %v\n%s", err, result.Output)
	}
	if report.LogBehavior != nil && len(report.LogBehavior.IncompleteSessions) != 0 {
		t.Fatalf("TUI closeout telemetry should satisfy session-summary lifecycle evidence, got %#v", report.LogBehavior.IncompleteSessions)
	}
	if slices.ContainsFunc(report.Findings, func(finding harnessAuditFinding) bool {
		return finding.ID == "incomplete_implement_session_lifecycle"
	}) {
		t.Fatalf("expected no incomplete lifecycle finding with TUI model closeout telemetry, got %#v", report.Findings)
	}
}

func TestHarnessAuditIgnoresImplementProcessWithoutModelSessionSummary(t *testing.T) {
	root := t.TempDir()
	telemetryPath := filepath.Join(root, "logs", "telemetry", "implement-help.jsonl")
	writeWorkspaceFile(t, telemetryPath, strings.Join([]string{
		`{"type":"session_start","command":"implement","workspace_root":"` + root + `"}`,
		`{"type":"process_start","command":"implement","pid":123,"workspace_root":"` + root + `"}`,
		`{"type":"process_exit","command":"implement","exit_code":2}`,
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
	if report.LogBehavior != nil && len(report.LogBehavior.IncompleteSessions) != 0 {
		t.Fatalf("expected no incomplete model session for process-only implement command, got %#v", report.LogBehavior.IncompleteSessions)
	}
	if slices.ContainsFunc(report.Findings, func(finding harnessAuditFinding) bool {
		return finding.ID == "incomplete_implement_session_lifecycle"
	}) {
		t.Fatalf("expected no incomplete lifecycle finding for process-only implement command, got %#v", report.Findings)
	}
}

func TestHarnessAuditSurfacesLocalOpsDegradedTelemetry(t *testing.T) {
	root := t.TempDir()
	telemetryPath := filepath.Join(root, "logs", "telemetry", "local-ops.jsonl")
	writeWorkspaceFile(t, telemetryPath, strings.Join([]string{
		`{"type":"session_start","command":"implement","workspace_root":"` + root + `"}`,
		`{"type":"implement_start","local_ops":{"backend_configured":true,"backend_kind":"mlx","context_budget_state":"degraded","classifier_state":"disabled_fallback"}}`,
		`{"type":"implement_queue","state":"degraded","pool":"qwen-coder","wait_ms":25,"degraded":true}`,
		`{"type":"doctor_readiness_profile","profile":"implement","state":"degraded","degraded":true,"queue_state":"unavailable","context_budget_state":"degraded","route_classifier_state":"degraded","backend_state":"pass"}`,
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
	if report.LogBehavior == nil || report.LogBehavior.Aggregate.LocalOps == nil {
		t.Fatalf("expected local ops aggregate, got %#v", report.LogBehavior)
	}
	if report.LogBehavior.Aggregate.LocalOps.QueueDegraded != 1 || report.LogBehavior.Aggregate.LocalOps.ContextBudgetDegraded == 0 {
		t.Fatalf("unexpected local ops aggregate: %#v", report.LogBehavior.Aggregate.LocalOps)
	}
	if !slices.ContainsFunc(report.Findings, func(finding harnessAuditFinding) bool {
		return finding.ID == "local_ops_degraded_mode_reported"
	}) {
		t.Fatalf("expected local ops degraded finding, got %#v", report.Findings)
	}
}

func TestContextTelemetryFieldsIncludeCollectorReports(t *testing.T) {
	fields := ContextTelemetryFields("context_index", `{
  "store_kind": "sqlite-graph-v1",
  "collector_version": "old",
  "current_collector_version": "new",
  "collector_version_mismatch": true,
  "stale_file_count": 2,
  "missing_file_count": 1,
  "stale_path_count": 3,
  "confidence": {
    "level": "low",
    "source": "freshness_decision",
    "reason": "budget_exceeded"
  },
  "provenance": {
    "source": "context_graph",
    "operation": "context_pack",
    "store_kind": "sqlite-graph-v1",
    "index_id": "idx123",
    "generated_at": "2026-05-13T09:00:00Z",
    "collector_version": "old",
    "current_collector_version": "new",
    "artifact_path": ".idiom/comprehension/views/abc.json"
  },
  "cache": {
    "state": "hit",
    "source": "graph_views",
    "reason": "fresh_cached_row",
    "cache_key": "abc",
    "pack_id": "abc",
    "workspace_state_fingerprint": "fp123",
    "ranker_weights_fingerprint": "ranker123",
    "runtime_traces_fingerprint": "trace123",
    "freshness_policy": "read_through",
    "read_through_refresh": true,
    "invalidation": {
      "policy": "content_addressed_read_through",
      "state": "valid",
      "stale_result": "cache_miss_recompute",
      "sources": [
        {"source": "index_id", "state": "keyed"},
        {"source": "ranker_weights", "state": "keyed"}
      ]
    }
  },
  "unresolved_gap_details": [
    {"reason": "stale_graph", "state": "stale"}
  ],
  "incremental": true,
  "reused_indexed_files": 5,
  "expanded_scope": "pkg",
  "dependency_refresh_files": ["pkg/a.go", "pkg/b.go"],
  "collector_coverage": ["python_index", "language_environment"],
  "freshness": {
    "policy": "read_through",
    "operation": "context_pack",
    "fresh_for_scope": false,
    "global_fresh": false,
    "scope": "pkg",
    "reason": "budget_exceeded",
    "blocker": "freshness refresh exceeded budget",
    "budget_exceeded": true,
    "checked_path_count": 1,
    "stale_path_count": 1,
    "missing_path_count": 1,
    "checked_paths": ["pkg/a.go"],
    "stale_paths": ["pkg/a.go"],
    "missing_paths": ["pkg/missing.go"],
    "refresh": {
      "attempted": false,
      "scope": "pkg",
      "reason": "budget_exceeded",
      "skipped_reason": "cap exceeded"
    }
  },
  "stale_evidence": {
    "state": "blocked",
    "policy": "read_through",
    "reason": "budget_exceeded",
    "fresh_for_scope": false,
    "global_fresh": false,
    "stale_path_count": 1,
    "missing_path_count": 1
  },
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
	if fields["context_freshness_policy"] != "read_through" || fields["context_freshness_operation"] != "context_pack" || fields["context_fresh_for_scope"] != false || fields["context_freshness_state"] != "blocked" || fields["context_freshness_blocked"] != true || fields["context_freshness_budget_exceeded"] != true {
		t.Fatalf("unexpected freshness telemetry fields: %#v", fields)
	}
	if fields["context_freshness_checked_paths"] != 1 || fields["context_freshness_stale_paths"] != 1 || fields["context_freshness_missing_paths"] != 1 || fields["context_freshness_refresh_attempted"] != false || fields["context_freshness_refresh_skipped_reason"] != "cap exceeded" {
		t.Fatalf("unexpected freshness path/refresh telemetry fields: %#v", fields)
	}
	if fields["context_stale_evidence_state"] != "blocked" || fields["context_stale_use_blocked"] != true || fields["context_stale_evidence_stale_path_count"] != 1 {
		t.Fatalf("unexpected stale-evidence telemetry fields: %#v", fields)
	}
	if fields["context_stale_file_count"] != 2 || fields["context_missing_file_count"] != 1 || fields["context_stale_path_count"] != 3 || fields["context_freshness_checked_path_count"] != 1 || fields["context_freshness_stale_path_count"] != 1 || fields["context_freshness_missing_path_count"] != 1 {
		t.Fatalf("unexpected structured stale count telemetry fields: %#v", fields)
	}
	if fields["context_confidence_level"] != "low" || fields["context_confidence_source"] != "freshness_decision" || fields["context_confidence_reason"] != "budget_exceeded" {
		t.Fatalf("unexpected confidence telemetry fields: %#v", fields)
	}
	if fields["context_provenance_source"] != "context_graph" || fields["context_provenance_operation"] != "context_pack" || fields["context_provenance_index_id"] != "idx123" || fields["context_provenance_artifact_path"] != ".idiom/comprehension/views/abc.json" {
		t.Fatalf("unexpected provenance telemetry fields: %#v", fields)
	}
	if fields["context_cache_state"] != "hit" || fields["context_cache_source"] != "graph_views" || fields["context_cache_key"] != "abc" || fields["context_cache_workspace_state_fingerprint"] != "fp123" || fields["context_cache_ranker_weights_fingerprint"] != "ranker123" || fields["context_cache_runtime_traces_fingerprint"] != "trace123" || fields["context_cache_read_through_refresh"] != true || fields["context_cache_invalidation_policy"] != "content_addressed_read_through" || fields["context_cache_invalidation_sources"] != 2 || fields["context_unresolved_gap_details"] != 1 {
		t.Fatalf("unexpected cache/gap telemetry fields: %#v", fields)
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

func TestLanguageToolResultsNormalizeBackslashRelationshipPaths(t *testing.T) {
	result := annotateLanguageRelationships(ToolResult{
		Tool:   "python_calls_from",
		Output: `src\app.py:2 -> helper in src\lib.py:1`,
	})
	if len(result.Relationships) != 1 {
		t.Fatalf("expected parsed relationship payload, got %#v", result)
	}
	relationship := result.Relationships[0]
	if relationship.SourcePath != "src/app.py" || relationship.TargetPath != "src/lib.py" {
		t.Fatalf("expected slash-normalized relationship paths, got %#v", relationship)
	}
}

func TestLSPCallHierarchyFormattersReturnStructuredRelationships(t *testing.T) {
	root := t.TempDir()
	app := filepath.Join(root, "app.py")
	lib := filepath.Join(root, "lib.py")
	writeWorkspaceFile(t, app, "def run():\n    return helper()\n")
	writeWorkspaceFile(t, lib, "def helper():\n    return 'ok'\n")
	mgr := NewManager(config.Workspace{Root: root, CWD: root, MaxDirectoryEntries: 16})

	payload := mgr.formatPylspOutgoingCalls(app, "run", []LSPOutgoingCall{{
		To: LSPCallHierarchyItem{
			Name: "helper",
			URI:  pylspFileURI(lib),
			SelectionRange: LSPRange{
				Start: LSPPosition{Line: 0, Character: 4},
			},
		},
		FromRanges: []LSPRange{{
			Start: LSPPosition{Line: 1, Character: 11},
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
	writeWorkspaceFile(t, filepath.Join(root, "src", "module.mts"), `import {x} from "./lib";
export {x};
`)
	writeWorkspaceFile(t, filepath.Join(root, "src", "common.cts"), `const lib = require("./lib");
module.exports = lib;
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
	result := mgr.regexDependencyGraph(target, "typescript", typescriptDependencyGraphExtensions(), tsImportRe)
	if result.Error != "" {
		t.Fatalf("dependency_graph returned error: %v\n%s", result.Error, result.Output)
	}
	for _, want := range []string{"src/main.ts -> ./lib", "src/main.ts -> ./other", "src/main.ts -> fs", "src/module.mts -> ./lib", "src/common.cts -> ./lib"} {
		if !strings.Contains(result.Output, want) {
			t.Fatalf("expected dependency graph to include %q, got:\n%s", want, result.Output)
		}
	}
}

func TestInferDependencyLanguageRecognizesTypeScriptModuleExtensions(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, filepath.Join(root, "src", "entry.mts"), `export const entry = true;`)
	mgr := NewManager(config.Workspace{Root: root, CWD: root, MaxDirectoryEntries: 16})
	if got := mgr.inferDependencyLanguage(filepath.Join(root, "src")); got != "typescript" {
		t.Fatalf("inferDependencyLanguage(.mts workspace) = %q, want typescript", got)
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
	if history.Code != "" {
		t.Fatalf("tracked history_range Code = %q, want empty; result=%#v", history.Code, history)
	}
	if !strings.Contains(history.Output, "change add") || !strings.Contains(history.Output, "initial") {
		t.Fatalf("expected git log -L output to include both commits, got:\n%s", history.Output)
	}

	pathOnlyHistory := mgr.Execute(context.Background(), ToolRequest{Tool: "history_range", Path: "sample.go"})
	if pathOnlyHistory.Error != "" {
		t.Fatalf("path-only history_range returned error: %v\n%s", pathOnlyHistory.Error, pathOnlyHistory.Output)
	}
	if pathOnlyHistory.Code != "" {
		t.Fatalf("path-only history_range Code = %q, want empty; result=%#v", pathOnlyHistory.Code, pathOnlyHistory)
	}
	if !strings.Contains(pathOnlyHistory.Summary, "lines 1-5") {
		t.Fatalf("expected path-only history_range to default to bounded file lines, got summary %q", pathOnlyHistory.Summary)
	}
	if !strings.Contains(pathOnlyHistory.Output, "change add") || !strings.Contains(pathOnlyHistory.Output, "initial") {
		t.Fatalf("expected path-only git log -L output to include both commits, got:\n%s", pathOnlyHistory.Output)
	}

	symbolHistory := mgr.Execute(context.Background(), ToolRequest{Tool: "history_symbol", Path: "sample.go", Pattern: "add"})
	if symbolHistory.Error != "" {
		t.Fatalf("history_symbol returned error: %v\n%s", symbolHistory.Error, symbolHistory.Output)
	}
	if symbolHistory.Code != "" {
		t.Fatalf("tracked history_symbol Code = %q, want empty; result=%#v", symbolHistory.Code, symbolHistory)
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

func TestHistoryRangeReportsNotApplicableForNonGitRoot(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	root := t.TempDir()
	writeWorkspaceFile(t, filepath.Join(root, "sample.go"), "package main\n")
	mgr := historyTestManager(root)

	result := mgr.Execute(context.Background(), ToolRequest{Tool: "history_range", Path: "sample.go", StartLine: 1, EndLine: 1})

	assertHistoryNotApplicable(t, result, "non_git_root", "sample.go")
}

func TestHistoryRangeReportsNotApplicableForGitRepoWithoutHead(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	root := t.TempDir()
	runTestCommand(t, root, "git", "init")
	writeWorkspaceFile(t, filepath.Join(root, "sample.go"), "package main\n")
	mgr := historyTestManager(root)

	result := mgr.Execute(context.Background(), ToolRequest{Tool: "history_range", Path: "sample.go", StartLine: 1, EndLine: 1})

	assertHistoryNotApplicable(t, result, "no_head", "sample.go")
}

func TestHistoryRangeReportsNotApplicableForUntrackedGitPath(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	root := t.TempDir()
	runTestCommand(t, root, "git", "init")
	runTestCommand(t, root, "git", "config", "user.email", "test@example.com")
	runTestCommand(t, root, "git", "config", "user.name", "Test User")
	writeWorkspaceFile(t, filepath.Join(root, "README.md"), "baseline\n")
	runTestCommand(t, root, "git", "add", "README.md")
	runTestCommand(t, root, "git", "commit", "-m", "initial")
	writeWorkspaceFile(t, filepath.Join(root, "sample.go"), "package main\n")
	mgr := historyTestManager(root)

	result := mgr.Execute(context.Background(), ToolRequest{Tool: "history_range", Path: "sample.go", StartLine: 1, EndLine: 1})

	assertHistoryNotApplicable(t, result, "untracked_path", "sample.go")
}

func TestHistoryRangeReportsNotApplicableForTrackedPathWithoutCommittedHistory(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	root := t.TempDir()
	runTestCommand(t, root, "git", "init")
	runTestCommand(t, root, "git", "config", "user.email", "test@example.com")
	runTestCommand(t, root, "git", "config", "user.name", "Test User")
	writeWorkspaceFile(t, filepath.Join(root, "README.md"), "baseline\n")
	runTestCommand(t, root, "git", "add", "README.md")
	runTestCommand(t, root, "git", "commit", "-m", "initial")
	writeWorkspaceFile(t, filepath.Join(root, "sample.go"), "package main\n")
	runTestCommand(t, root, "git", "add", "sample.go")
	mgr := historyTestManager(root)

	result := mgr.Execute(context.Background(), ToolRequest{Tool: "history_range", Path: "sample.go", StartLine: 1, EndLine: 1})

	assertHistoryNotApplicable(t, result, "path_no_committed_history", "sample.go")
}

func TestHistoryRangeRejectsPartialRange(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, filepath.Join(root, "sample.go"), "package main\n")
	mgr := historyTestManager(root)

	result := mgr.Execute(context.Background(), ToolRequest{Tool: "history_range", Path: "sample.go", StartLine: 1})

	if result.Error == "" {
		t.Fatalf("partial history_range should be rejected, got %#v", result)
	}
	if !strings.Contains(result.Error, "start_line and end_line are required") {
		t.Fatalf("partial history_range should explain required bounds, got %#v", result)
	}
}

func TestHistoryRangeMissingPathRemainsError(t *testing.T) {
	root := t.TempDir()
	mgr := historyTestManager(root)

	result := mgr.Execute(context.Background(), ToolRequest{Tool: "history_range", Path: "missing.go", StartLine: 1, EndLine: 1})

	if result.Error == "" {
		t.Fatalf("missing path should remain an error, got %#v", result)
	}
	if result.Code == HistoryNotApplicableCode {
		t.Fatalf("missing path must not be reported as not applicable: %#v", result)
	}
}

func TestHistoryRangeMissingGitBinaryRemainsError(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, filepath.Join(root, "sample.go"), "package main\n")
	t.Setenv("PATH", "")
	mgr := historyTestManager(root)

	result := mgr.Execute(context.Background(), ToolRequest{Tool: "history_range", Path: "sample.go", StartLine: 1, EndLine: 1})

	if result.Error == "" || !strings.Contains(result.Error, "git is not installed") {
		t.Fatalf("missing git binary should remain an error, got %#v", result)
	}
	if result.Code == HistoryNotApplicableCode {
		t.Fatalf("missing git binary must not be reported as not applicable: %#v", result)
	}
}

func TestHistorySymbolMalformedPatternRemainsError(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	root := t.TempDir()
	runTestCommand(t, root, "git", "init")
	runTestCommand(t, root, "git", "config", "user.email", "test@example.com")
	runTestCommand(t, root, "git", "config", "user.name", "Test User")
	writeWorkspaceFile(t, filepath.Join(root, "sample.go"), "package main\n\nfunc add(a int, b int) int { return a + b }\n")
	runTestCommand(t, root, "git", "add", "sample.go")
	runTestCommand(t, root, "git", "commit", "-m", "initial")
	mgr := historyTestManager(root)

	result := mgr.Execute(context.Background(), ToolRequest{Tool: "history_symbol", Path: "sample.go", Pattern: "[["})

	if result.Error == "" {
		t.Fatalf("malformed history_symbol pattern should remain an error, got %#v", result)
	}
	if result.Code == HistoryNotApplicableCode {
		t.Fatalf("malformed history_symbol pattern must not be reported as not applicable: %#v", result)
	}
}

func historyTestManager(root string) *Manager {
	return NewManager(config.Workspace{
		Root:                  root,
		CWD:                   root,
		CommandTimeoutSeconds: 30,
		MaxOutputBytes:        65536,
		MaxFileBytes:          65536,
		EnableShell:           true,
		EnableWrites:          true,
	})
}

func assertHistoryNotApplicable(t *testing.T, result ToolResult, reason string, path string) {
	t.Helper()
	if result.Error != "" {
		t.Fatalf("history result should be non-error not_applicable, got error=%q output=%s", result.Error, result.Output)
	}
	if result.Code != HistoryNotApplicableCode {
		t.Fatalf("history result Code = %q, want %q; result=%#v", result.Code, HistoryNotApplicableCode, result)
	}
	var payload struct {
		Status string `json:"status"`
		Code   string `json:"code"`
		Reason string `json:"reason"`
		Path   string `json:"path"`
	}
	if err := json.Unmarshal([]byte(result.Output), &payload); err != nil {
		t.Fatalf("history not-applicable output should be small JSON, got %q: %v", result.Output, err)
	}
	if payload.Status != "not_applicable" || payload.Code != HistoryNotApplicableCode || payload.Reason != reason || payload.Path != path {
		t.Fatalf("unexpected history not-applicable payload: %#v", payload)
	}
}

func TestStructuralSearchRejectsMissingPattern(t *testing.T) {
	mgr := NewManager(testWorkspaceConfig(t))
	result := mgr.Execute(context.Background(), ToolRequest{Tool: "structural_search", Path: "."})
	if result.Error == "" || !strings.Contains(result.Error, "pattern is required") {
		t.Fatalf("expected missing-pattern error, got %#v", result)
	}
}

func TestOptionalExternalDiscoveryToolsReportUnavailable(t *testing.T) {
	t.Setenv("PATH", "")
	mgr := NewManager(testWorkspaceConfig(t))

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

func assertNoPrescriptiveGraphOutput(t *testing.T, name string, output string) {
	t.Helper()
	lower := strings.ToLower(output)
	for _, forbidden := range []string{
		"run context_index",
		"run context_pack",
		"rerun context_trace_pack",
		"use context_pack",
		"provide a bounded path",
		"identify one exact",
		"language-aware symbol tool",
		"before relying",
		"next action",
		"next step",
		"invoke",
	} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("%s output contains prescriptive phrase %q:\n%s", name, forbidden, output)
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

func mustMarshalTestJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal JSON: %v", err)
	}
	return string(data)
}

func packReportSourcePresent(sources []struct {
	Source string `json:"source"`
	State  string `json:"state"`
}, source string) bool {
	for _, item := range sources {
		if item.Source == source {
			return true
		}
	}
	return false
}

func packReportRankerReasonPresent(reasons []struct {
	Reason string `json:"reason"`
	State  string `json:"state"`
}, reason string) bool {
	for _, item := range reasons {
		if item.Reason == reason {
			return true
		}
	}
	return false
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

func TestHarnessAuditReportsContextCacheState(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, filepath.Join(root, "go.mod"), "module example.com/cache-audit\n\ngo 1.22\n")
	writeWorkspaceFile(t, filepath.Join(root, "main.go"), "package main\n\nfunc main() {}\n")
	// Seed an empty conversation log so the behavior-purpose audit can run.
	writeWorkspaceFile(t, filepath.Join(root, "logs", "conversations", "seed.jsonl"), "")
	mgr := NewManager(config.Workspace{Root: root, CWD: root})

	if result := mgr.Execute(context.Background(), ToolRequest{Tool: "context_index", Language: "go"}); result.Error != "" {
		t.Fatalf("context_index returned error: %v\n%s", result.Error, result.Output)
	}
	// Populate at least one cache row so the audit has something to report.
	if result := mgr.Execute(context.Background(), ToolRequest{Tool: "context_pack", Task: "audit cache exercise", Mode: "plan"}); result.Error != "" {
		t.Fatalf("context_pack returned error: %v\n%s", result.Error, result.Output)
	}

	result := mgr.Execute(context.Background(), ToolRequest{Tool: "harness_audit", Purpose: "behavior", Path: "logs/conversations"})
	if result.Error != "" {
		t.Fatalf("harness_audit returned error: %v\n%s", result.Error, result.Output)
	}
	var report harnessAuditReport
	if err := json.Unmarshal([]byte(result.Output), &report); err != nil {
		t.Fatalf("decode audit report: %v\n%s", err, result.Output)
	}
	if report.ContextCache == nil {
		t.Fatalf("expected context_cache section in audit report, got %s", result.Output)
	}
	if report.ContextCache.Rows < 1 {
		t.Fatalf("expected at least one graph_views row after context_pack, got %d", report.ContextCache.Rows)
	}
	if report.ContextCache.SessionWrites < 1 {
		t.Fatalf("expected at least one session write counter, got %+v", report.ContextCache)
	}
	if report.ContextCache.ContractRows < 1 {
		t.Fatalf("expected graph-view contract row count in cache audit, got %+v", report.ContextCache)
	}
	if report.FreshnessHealth == nil || report.FreshnessHealth.ReadThroughAttempts < 1 {
		t.Fatalf("expected freshness health counters in audit report, got %#v", report.FreshnessHealth)
	}
	// Confirm the comprehension package's view stats type is the same shape
	// the audit wraps; this catches drift if either side adds a field
	// without updating the mirror.
	stats, err := comprehension.ReadViewStats(root)
	if err != nil {
		t.Fatalf("ReadViewStats: %v", err)
	}
	if stats.Rows != report.ContextCache.Rows {
		t.Fatalf("audit row count drifted from comprehension stats: audit=%d stats=%d", report.ContextCache.Rows, stats.Rows)
	}
}

func TestHarnessAuditFreshnessHealthIncludesLogDerivedTelemetry(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, filepath.Join(root, "logs", "conversations", "freshness.jsonl"), strings.Join([]string{
		`{"type":"tool_result","tool":"context_query","context_freshness_policy":"read_through","context_freshness_blocked":true,"context_freshness_budget_exceeded":true,"context_freshness_refresh_attempted":true,"context_freshness_refresh_error":"refresh failed","context_freshness_refresh_files_indexed":3,"context_stale_evidence_state":"blocked","context_stale_use_blocked":true}`,
		`{"type":"tool_result","tool":"context_zoom","context_freshness_policy":"report_only","context_stale_evidence_state":"reported","context_stale_use_reported":true}`,
		`{"type":"tool_result","tool":"context_status","context_freshness_policy":"report_only","context_stale_evidence_state":"fresh","context_stale_use_fresh":true}`,
		"",
	}, "\n"))
	mgr := NewManager(config.Workspace{Root: root, CWD: root})

	result := mgr.Execute(context.Background(), ToolRequest{Tool: "harness_audit", Purpose: "behavior", Path: "logs/conversations"})
	if result.Error != "" {
		t.Fatalf("harness_audit returned error: %v\n%s", result.Error, result.Output)
	}
	var report harnessAuditReport
	if err := json.Unmarshal([]byte(result.Output), &report); err != nil {
		t.Fatalf("decode audit report: %v\n%s", err, result.Output)
	}
	if report.FreshnessHealth == nil {
		t.Fatalf("expected freshness_health section, got %s", result.Output)
	}
	if report.FreshnessHealth.TelemetryReadThrough != 1 || report.FreshnessHealth.TelemetryBlockers != 1 || report.FreshnessHealth.TelemetryBudgetExceeded != 1 || report.FreshnessHealth.TelemetryRefreshFailures != 1 || report.FreshnessHealth.TelemetryRefreshFiles != 3 {
		t.Fatalf("unexpected log-derived freshness health: %#v", report.FreshnessHealth)
	}
	if _, ok := report.FreshnessHealth.TelemetryPolicyCounts["read_through"]; !ok {
		t.Fatalf("expected policy counts to include read_through: %#v", report.FreshnessHealth.TelemetryPolicyCounts)
	}
	if report.FreshnessHealth.StaleUseBlocked != 1 || report.FreshnessHealth.StaleUseReported != 1 || report.FreshnessHealth.StaleUseFresh != 1 || report.FreshnessHealth.StaleUseStateCounts["blocked"] != 1 || report.FreshnessHealth.StaleUseStateCounts["reported"] != 1 || report.FreshnessHealth.StaleUseStateCounts["fresh"] != 1 {
		t.Fatalf("expected stale-use blocker coverage in freshness health, got %#v", report.FreshnessHealth)
	}
}
