package comprehension

import (
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"idiom/internal/repository/semantic"
)

func TestBuildQueryZoomAndStatus(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/demo\n\ngo 1.22\n")
	writeFile(t, filepath.Join(root, "service", "service.go"), `package service

import "fmt"

type Runner struct{}

func Run() string {
	return format("ok")
}

func format(value string) string {
	return fmt.Sprintf("value=%s", value)
}

func Sprintf() string {
	return "local"
}
`)
	writeFile(t, filepath.Join(root, "service", "service_test.go"), `package service

import "testing"

func TestRun(t *testing.T) {
	if Run() == "" {
		t.Fatal("empty")
	}
}
`)
	writeFile(t, filepath.Join(root, "client", "client.go"), `package client

import "example.com/demo/service"

func CallRun() string {
	return service.Run()
}
`)
	writeFile(t, filepath.Join(root, "other", "other.go"), `package other

func format(value string) string {
	return value
}
`)

	report, err := Build(context.Background(), BuildOptions{Root: root, Language: "go"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if report.FilesIndexed != 4 || report.SymbolsIndexed == 0 || report.EdgesIndexed == 0 {
		t.Fatalf("unexpected build report: %#v", report)
	}

	if report.CollectorVersion == "" || report.CollectorVersions["go_index"] == "" {
		t.Fatalf("expected build report to include collector version metadata, got %#v", report)
	}

	graph, err := LoadGraph(root)
	if err != nil {
		t.Fatalf("LoadGraph: %v", err)
	}

	if !hasNode(graph.Nodes, "external://fmt") || !hasEdge(graph.Edges, "imports", "file://service/service.go", "external://fmt") {
		t.Fatalf("expected import relationship for fmt in graph: nodes=%#v edges=%#v", graph.Nodes, graph.Edges)
	}

	if !hasEdge(graph.Edges, "tests", "symbol://service/service_test.go#TestRun", "package://service") {
		t.Fatalf("expected TestRun to map to service package test relationship: %#v", graph.Edges)
	}

	if !hasEdge(graph.Edges, "tests", "symbol://service/service_test.go#TestRun", "symbol://service/service.go#Run") {
		t.Fatalf("expected TestRun to map to Run symbol relationship: %#v", graph.Edges)
	}

	if !hasNode(graph.Nodes, "external_symbol://go/fmt.Sprintf") || !hasEdge(graph.Edges, "calls", "symbol://service/service.go#format", "external_symbol://go/fmt.Sprintf") {
		t.Fatalf("expected fmt.Sprintf to resolve as an external call, nodes=%#v edges=%#v", graph.Nodes, graph.Edges)
	}

	if hasEdge(graph.Edges, "calls", "symbol://service/service.go#format", "symbol://service/service.go#Sprintf") {
		t.Fatalf("qualified fmt.Sprintf call must not resolve to local Sprintf symbol: %#v", graph.Edges)
	}

	if !hasEdge(graph.Edges, "calls", "symbol://service/service.go#Run", "symbol://service/service.go#format") {
		t.Fatalf("expected unqualified Go call to resolve within its own package, got %#v", graph.Edges)
	}

	if hasEdge(graph.Edges, "calls", "symbol://service/service.go#Run", "symbol://other/other.go#format") {
		t.Fatalf("unqualified Go call must not resolve across packages, got %#v", graph.Edges)
	}

	if !hasEdge(graph.Edges, "calls", "symbol://client/client.go#CallRun", "symbol://service/service.go#Run") {
		t.Fatalf("expected local module import call to resolve to indexed service.Run symbol, got %#v", graph.Edges)
	}

	status, err := Status(root)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}

	if !status.Indexed || !status.Fresh || status.StaleSymbols != 0 {
		t.Fatalf("unexpected fresh status: %#v", status)
	}

	if status.CollectorVersion == "" || status.CurrentCollectorVersion == "" || status.CollectorVersionMismatch {
		t.Fatalf("expected fresh collector-version status, got %#v", status)
	}

	if report.SymbolsIndexed != status.SymbolsIndexed {
		t.Fatalf("build and status should report the same graph symbol count, build=%d status=%d", report.SymbolsIndexed, status.SymbolsIndexed)
	}

	query, err := Query(QueryOptions{Root: root, Symbol: "Run", Resolution: "symbol", IncludeEvidence: true})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	if len(query.Nodes) == 0 {
		t.Fatalf("expected query nodes, got %#v", query)
	}

	var runHandle string

	for _, node := range query.Nodes {
		if strings.Contains(node.QualifiedName, "Run") && node.Kind == "func" {
			runHandle = node.ID
			break
		}
	}

	if runHandle == "" {
		t.Fatalf("expected Run handle in query: %#v", query.Nodes)
	}

	for _, node := range query.Nodes {
		if node.Confidence == "" {
			t.Fatalf("query results must include confidence tiers: %#v", query.Nodes)
		}
	}

	zoom, err := Zoom(ZoomOptions{Root: root, Handle: runHandle})
	if err != nil {
		t.Fatalf("Zoom: %v", err)
	}

	if zoom.Node == nil || zoom.Node.ID != runHandle || len(zoom.Edges) == 0 {
		t.Fatalf("unexpected zoom result: %#v", zoom)
	}

	pkgQuery, err := Query(QueryOptions{Root: root, Query: "what packages are indexed", Resolution: "package", IncludeEvidence: true})
	if err != nil {
		t.Fatalf("package Query: %v", err)
	}

	if !hasNodeSummary(pkgQuery.Nodes, "package://service") || len(pkgQuery.Edges) == 0 {
		t.Fatalf("expected package-level query with relationship evidence, got %#v", pkgQuery)
	}

	summaryQuery, err := Query(QueryOptions{Root: root, Query: "repository summary", Resolution: "summary", IncludeEvidence: true})
	if err != nil {
		t.Fatalf("summary Query: %v", err)
	}

	if !hasSummaryResolution(summaryQuery.Nodes, "repository") || len(summaryQuery.Edges) == 0 {
		t.Fatalf("expected durable summary node with evidence, got %#v", summaryQuery)
	}

	if !hasSummaryQuality(summaryQuery.Nodes, "deterministic", false) {
		t.Fatalf("expected deterministic summary quality metadata, got %#v", summaryQuery.Nodes)
	}
}

func TestCleanScopeRejectsTraversalAndNormalizesBackslashes(t *testing.T) {
	if got := cleanScope(`pkg\file.go`); got != "pkg/file.go" {
		t.Fatalf("cleanScope(backslash path) = %q, want pkg/file.go", got)
	}

	for _, path := range []string{"../outside.go", "/tmp/outside.go", `C:\tmp\outside.go`} {
		if got := cleanScope(path); got != "." {
			t.Fatalf("cleanScope(%q) = %q, want root scope", path, got)
		}
	}

	if !pathInScope(`pkg\file.go`, "pkg") {
		t.Fatalf("pathInScope should normalize backslash paths")
	}

	if pathInScope("../outside.go", "pkg") {
		t.Fatalf("pathInScope should reject traversal paths")
	}
}

func TestStatusReportsCollectorVersionMismatch(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/collector\n\ngo 1.22\n")
	writeFile(t, filepath.Join(root, "pkg", "pkg.go"), "package pkg\n\nfunc Run() string { return \"ok\" }\n")

	if _, err := Build(context.Background(), BuildOptions{Root: root, Language: "go"}); err != nil {
		t.Fatalf("Build: %v", err)
	}

	db, err := openSQLite(sqliteGraphPath(root))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}

	if _, err := db.Exec(`UPDATE graph_metadata SET value = ? WHERE key = 'collector_version'`, "stale-version"); err != nil {
		_ = db.Close()

		t.Fatalf("update collector version: %v", err)
	}

	if err := db.Close(); err != nil {
		t.Fatalf("close sqlite: %v", err)
	}

	status, err := Status(root)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}

	if status.Fresh || !status.CollectorVersionMismatch || status.CollectorVersion != "stale-version" || status.CurrentCollectorVersion == "" {
		t.Fatalf("expected collector version mismatch to mark graph stale, got %#v", status)
	}

	query, err := Query(QueryOptions{Root: root, Symbol: "Run", Resolution: "symbol"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	if !hasGapContaining(query.UnresolvedGaps, "reason=collector_version_mismatch") {
		t.Fatalf("expected collector-version gap, got %#v", query.UnresolvedGaps)
	}

	report, err := Build(context.Background(), BuildOptions{Root: root, Language: "go"})
	if err != nil {
		t.Fatalf("refresh Build: %v", err)
	}

	if report.CollectorVersion == "stale-version" || report.CollectorVersion == "" {
		t.Fatalf("expected refreshed collector version, got %#v", report)
	}

	status, err = Status(root)
	if err != nil {
		t.Fatalf("Status after refresh: %v", err)
	}

	if !status.Fresh || status.CollectorVersionMismatch {
		t.Fatalf("expected refreshed collector version status, got %#v", status)
	}
}

func TestStatusReportsStaleSymbolsAndBuildRefreshesThem(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/stale\n\ngo 1.22\n")

	sourcePath := filepath.Join(root, "pkg", "pkg.go")
	writeFile(t, sourcePath, "package pkg\n\nfunc OldName() string { return \"old\" }\n")

	if _, err := Build(context.Background(), BuildOptions{Root: root, Language: "go"}); err != nil {
		t.Fatalf("Build: %v", err)
	}

	writeFile(t, sourcePath, "package pkg\n\nfunc NewName() string { return \"new\" }\n")

	status, err := Status(root)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}

	if len(status.StaleFiles) != 1 || status.StaleFiles[0] != "pkg/pkg.go" || status.StaleSymbols == 0 {
		t.Fatalf("expected stale file and symbols, got %#v", status)
	}

	report, err := Build(context.Background(), BuildOptions{Root: root, Language: "go"})
	if err != nil {
		t.Fatalf("refresh Build: %v", err)
	}

	if len(report.RefreshedStaleFiles) != 1 || report.RefreshedStaleFiles[0] != "pkg/pkg.go" {
		t.Fatalf("expected refreshed stale file, got %#v", report.RefreshedStaleFiles)
	}

	query, err := Query(QueryOptions{Root: root, Symbol: "NewName", Resolution: "symbol"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	if len(query.Nodes) != 1 || query.Nodes[0].QualifiedName != "NewName" {
		t.Fatalf("expected refreshed NewName symbol, got %#v", query.Nodes)
	}
}

func TestPathScopedBuildMergesExistingGraph(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/incremental\n\ngo 1.22\n")

	sourcePath := filepath.Join(root, "pkg", "a.go")
	writeFile(t, sourcePath, "package pkg\n\nfunc OldName() string { return \"old\" }\n")
	writeFile(t, filepath.Join(root, "pkg", "b.go"), "package pkg\n\nfunc KeepName() string { return OldName() }\n")
	writeFile(t, filepath.Join(root, "cmd", "main.go"), "package main\n\nfunc main() {}\n")

	if _, err := Build(context.Background(), BuildOptions{Root: root, Language: "go"}); err != nil {
		t.Fatalf("Build: %v", err)
	}

	writeFile(t, sourcePath, "package pkg\n\nfunc NewName() string { return \"new\" }\n")

	report, err := Build(context.Background(), BuildOptions{Root: root, Path: "pkg/a.go", Language: "go"})
	if err != nil {
		t.Fatalf("incremental Build: %v", err)
	}

	if !report.Incremental || report.ReusedIndexedFiles == 0 {
		t.Fatalf("expected path-scoped incremental merge, got %#v", report)
	}

	if report.ExpandedScope != "pkg" || !hasString(report.DependencyRefreshFiles, "pkg/b.go") {
		t.Fatalf("expected dependency-aware refresh to include caller file, got %#v", report)
	}

	if len(report.RefreshedStaleFiles) != 1 || report.RefreshedStaleFiles[0] != "pkg/a.go" || report.RefreshedStaleSymbols == 0 {
		t.Fatalf("expected scoped stale refresh accounting, got %#v", report)
	}

	graph, err := LoadGraph(root)
	if err != nil {
		t.Fatalf("LoadGraph: %v", err)
	}

	if !hasNode(graph.Nodes, "symbol://pkg/a.go#NewName") || hasNode(graph.Nodes, "symbol://pkg/a.go#OldName") || !hasNode(graph.Nodes, "symbol://pkg/b.go#KeepName") || !hasNode(graph.Nodes, "symbol://cmd/main.go#main") {
		t.Fatalf("expected refreshed scoped file and retained out-of-scope file, got nodes=%#v", graph.Nodes)
	}

	status, err := Status(root)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}

	if !status.Fresh || status.FilesIndexed != 3 {
		t.Fatalf("expected fresh merged graph with both files, got %#v", status)
	}
}

func TestPathScopedBuildExactScopeSkipsDependencyExpansion(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/exactscope\n\ngo 1.22\n")

	sourcePath := filepath.Join(root, "pkg", "a.go")
	writeFile(t, sourcePath, "package pkg\n\nfunc OldName() string { return \"old\" }\n")
	writeFile(t, filepath.Join(root, "pkg", "b.go"), "package pkg\n\nfunc KeepName() string { return OldName() }\n")

	if _, err := Build(context.Background(), BuildOptions{Root: root, Language: "go"}); err != nil {
		t.Fatalf("Build: %v", err)
	}

	writeFile(t, sourcePath, "package pkg\n\nfunc NewName() string { return \"new\" }\n")

	report, err := Build(context.Background(), BuildOptions{Root: root, Path: "pkg/a.go", Language: "go", ExactScope: true})
	if err != nil {
		t.Fatalf("exact-scope Build: %v", err)
	}

	if !report.Incremental || report.ExpandedScope != "" || len(report.DependencyRefreshFiles) != 0 || report.PartialRefreshReason != "exact_scope_refresh" {
		t.Fatalf("expected exact scoped refresh without dependency expansion, got %#v", report)
	}

	if report.FilesIndexed != 1 {
		t.Fatalf("expected exact scoped refresh to index one file, got %#v", report)
	}

	graph, err := LoadGraph(root)
	if err != nil {
		t.Fatalf("LoadGraph: %v", err)
	}

	if !hasNode(graph.Nodes, "symbol://pkg/a.go#NewName") || hasNode(graph.Nodes, "symbol://pkg/a.go#OldName") || !hasNode(graph.Nodes, "symbol://pkg/b.go#KeepName") {
		t.Fatalf("expected refreshed target file and retained dependency graph, got nodes=%#v", graph.Nodes)
	}
}

func TestPatchActiveStorePreservesInboundEdgesAndDropsSummaries(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/patchactive\n\ngo 1.22\n")

	sourcePath := filepath.Join(root, "pkg", "a.go")
	writeFile(t, sourcePath, "package pkg\n\nfunc Run() string { return \"old\" }\n")
	writeFile(t, filepath.Join(root, "pkg", "b.go"), "package pkg\n\nfunc KeepName() string { return Run() }\n")

	if _, err := Build(context.Background(), BuildOptions{Root: root, Language: "go"}); err != nil {
		t.Fatalf("Build: %v", err)
	}

	writeFile(t, sourcePath, "package pkg\n\nfunc Run() string { return \"new\" }\n")

	report, err := Build(context.Background(), BuildOptions{Root: root, Path: "pkg/a.go", Language: "go", ExactScope: true, PatchActiveStore: true})
	if err != nil {
		t.Fatalf("patch-active Build: %v", err)
	}

	if !report.Incremental || report.PartialRefreshReason != "exact_scope_refresh" || report.IndexID == "" {
		t.Fatalf("expected exact patch-active refresh, got %#v", report)
	}

	graph, err := LoadGraph(root)
	if err != nil {
		t.Fatalf("LoadGraph: %v", err)
	}

	if !hasNode(graph.Nodes, "symbol://pkg/a.go#Run") || !hasNode(graph.Nodes, "symbol://pkg/b.go#KeepName") {
		t.Fatalf("expected refreshed target and retained caller nodes, got nodes=%#v", graph.Nodes)
	}

	if !hasEdge(graph.Edges, "calls", "symbol://pkg/b.go#KeepName", "symbol://pkg/a.go#Run") {
		t.Fatalf("expected inbound caller edge to survive exact patch, got edges=%#v", graph.Edges)
	}

	for _, node := range graph.Nodes {
		if node.Kind == "summary" || node.Collector == "summary_index" {
			t.Fatalf("patch-active refresh must not leave stale summary nodes, got %#v", node)
		}
	}

	status, err := Status(root)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}

	for _, report := range status.CollectorReports {
		if report.Name == "summary_index" {
			t.Fatalf("patch-active refresh must not advertise deleted summary_index reports, got %#v", status.CollectorReports)
		}
	}
}

func TestPatchActiveStoreExpandsDependenciesWithoutFullRewrite(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/patchdeps\n\ngo 1.22\n")

	sourcePath := filepath.Join(root, "pkg", "a.go")
	writeFile(t, sourcePath, "package pkg\n\nfunc OldName() string { return \"old\" }\n")
	writeFile(t, filepath.Join(root, "pkg", "b.go"), "package pkg\n\nfunc KeepName() string { return OldName() }\n")
	writeFile(t, filepath.Join(root, "cmd", "main.go"), "package main\n\nfunc main() {}\n")

	initial, err := Build(context.Background(), BuildOptions{Root: root, Language: "go"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	writeFile(t, sourcePath, "package pkg\n\nfunc NewName() string { return \"new\" }\n")

	report, err := Build(context.Background(), BuildOptions{Root: root, Path: "pkg/a.go", Language: "go", PatchActiveStore: true})
	if err != nil {
		t.Fatalf("patch-active dependency Build: %v", err)
	}

	if !report.Incremental || report.IndexID != initial.IndexID || report.ExpandedScope != "pkg" || !hasString(report.DependencyRefreshFiles, "pkg/b.go") || report.PartialRefreshReason != "sqlite_scope_patch" {
		t.Fatalf("expected dependency-aware sqlite patch, got %#v", report)
	}

	graph, err := LoadGraph(root)
	if err != nil {
		t.Fatalf("LoadGraph: %v", err)
	}

	if !hasNode(graph.Nodes, "symbol://pkg/a.go#NewName") || hasNode(graph.Nodes, "symbol://pkg/a.go#OldName") || !hasNode(graph.Nodes, "symbol://pkg/b.go#KeepName") || !hasNode(graph.Nodes, "symbol://cmd/main.go#main") {
		t.Fatalf("expected refreshed dependency scope and retained out-of-scope graph, got nodes=%#v", graph.Nodes)
	}

	if hasEdge(graph.Edges, "calls", "symbol://pkg/b.go#KeepName", "symbol://pkg/a.go#OldName") {
		t.Fatalf("stale dependency edge to removed symbol survived patch: %#v", graph.Edges)
	}

	status, err := Status(root)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}

	if !status.Fresh || status.FilesIndexed != 3 {
		t.Fatalf("expected fresh total status after dependency-aware patch, got %#v", status)
	}
}

func TestPatchActiveStoreRemovesDeletedScopeAndOrphans(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/patchdelete\n\ngo 1.22\n")

	sourcePath := filepath.Join(root, "pkg", "a.go")
	writeFile(t, sourcePath, "package pkg\n\nimport \"fmt\"\n\nfunc Run() string { return fmt.Sprint(\"old\") }\n")

	if _, err := Build(context.Background(), BuildOptions{Root: root, Language: "go"}); err != nil {
		t.Fatalf("Build: %v", err)
	}

	if err := os.Remove(sourcePath); err != nil {
		t.Fatalf("remove source: %v", err)
	}

	report, err := Build(context.Background(), BuildOptions{Root: root, Path: "pkg/a.go", Language: "go", ExactScope: true, PatchActiveStore: true})
	if err != nil {
		t.Fatalf("patch-active delete Build: %v", err)
	}

	if !report.Incremental || report.FilesIndexed != 0 || report.PartialRefreshReason != "exact_scope_refresh" {
		t.Fatalf("expected exact delete patch, got %#v", report)
	}

	graph, err := LoadGraph(root)
	if err != nil {
		t.Fatalf("LoadGraph: %v", err)
	}

	for _, id := range []string{"file://pkg/a.go", "symbol://pkg/a.go#Run", "package://pkg", "external://fmt"} {
		if hasNode(graph.Nodes, id) {
			t.Fatalf("deleted scope left stale node %s in graph: %#v", id, graph.Nodes)
		}
	}

	for _, edge := range graph.Edges {
		if strings.Contains(edge.SourceID, "pkg/a.go") || strings.Contains(edge.TargetID, "pkg/a.go") || edge.SourceID == "package://pkg" || edge.TargetID == "package://pkg" || edge.SourceID == "external://fmt" || edge.TargetID == "external://fmt" {
			t.Fatalf("deleted scope left stale edge %#v", edge)
		}
	}

	status, err := Status(root)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}

	if status.FilesIndexed != 0 {
		t.Fatalf("deleted final source file should clear indexed file counts, got %#v", status)
	}
}

func TestPatchActiveStoreRemovesDeletedNativeLanguageScope(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "app.py")
	writeFile(t, sourcePath, "def run():\n    return 'old'\n")

	if _, err := Build(context.Background(), BuildOptions{Root: root, Language: "python"}); err != nil {
		t.Fatalf("Build: %v", err)
	}

	if err := os.Remove(sourcePath); err != nil {
		t.Fatalf("remove source: %v", err)
	}

	if _, err := Build(context.Background(), BuildOptions{Root: root, Path: "app.py", Language: "python", ExactScope: true, PatchActiveStore: true}); err != nil {
		t.Fatalf("patch-active python delete Build: %v", err)
	}

	graph, err := LoadGraph(root)
	if err != nil {
		t.Fatalf("LoadGraph: %v", err)
	}

	if hasNode(graph.Nodes, "file://app.py") {
		t.Fatalf("deleted Python scope left stale file node: %#v", graph.Nodes)
	}

	status, err := Status(root)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}

	if len(status.MissingFiles) != 0 || status.FilesIndexed != 0 {
		t.Fatalf("deleted Python scope should be removed from status, got %#v", status)
	}
}

func TestPathScopedBuildPreservesGraphWhenCollectorVersionIsStale(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/stale-scoped\n\ngo 1.22\n")

	sourcePath := filepath.Join(root, "pkg", "a.go")
	writeFile(t, sourcePath, "package pkg\n\nfunc OldName() string { return \"old\" }\n")
	writeFile(t, filepath.Join(root, "pkg", "b.go"), "package pkg\n\nfunc KeepName() string { return OldName() }\n")
	writeFile(t, filepath.Join(root, "cmd", "main.go"), "package main\n\nfunc main() {}\n")

	if _, err := Build(context.Background(), BuildOptions{Root: root, Language: "go"}); err != nil {
		t.Fatalf("Build: %v", err)
	}

	graph, err := LoadGraph(root)
	if err != nil {
		t.Fatalf("LoadGraph: %v", err)
	}

	graph.CollectorVersion = "old-collector-version"
	graph.CollectorVersions = map[string]string{"context_index": "old-collector-version"}

	if err := saveGraph(root, graph); err != nil {
		t.Fatalf("saveGraph: %v", err)
	}

	if err := saveSQLiteGraph(root, graph); err != nil {
		t.Fatalf("saveSQLiteGraph: %v", err)
	}

	status, err := Status(root)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}

	if !status.CollectorVersionMismatch {
		t.Fatalf("expected collector version mismatch before scoped build, got %#v", status)
	}

	writeFile(t, sourcePath, "package pkg\n\nfunc NewName() string { return \"new\" }\n")

	report, err := Build(context.Background(), BuildOptions{Root: root, Path: "pkg/a.go", Language: "go"})
	if err != nil {
		t.Fatalf("scoped Build: %v", err)
	}

	if !report.Incremental || report.ReusedIndexedFiles == 0 {
		t.Fatalf("expected scoped refresh to merge existing graph, got %#v", report)
	}

	if report.PartialRefreshReason != "collector_version_mismatch_preserved_for_scoped_refresh" {
		t.Fatalf("expected collector mismatch partial refresh reason, got %#v", report)
	}

	if report.CollectorVersion != "old-collector-version" {
		t.Fatalf("expected scoped refresh to preserve stale collector version, got %#v", report)
	}

	graph, err = LoadGraph(root)
	if err != nil {
		t.Fatalf("LoadGraph after scoped build: %v", err)
	}

	if !hasNode(graph.Nodes, "symbol://pkg/a.go#NewName") || hasNode(graph.Nodes, "symbol://pkg/a.go#OldName") || !hasNode(graph.Nodes, "symbol://cmd/main.go#main") {
		t.Fatalf("expected refreshed scoped file and retained out-of-scope graph, got nodes=%#v", graph.Nodes)
	}

	status, err = Status(root)
	if err != nil {
		t.Fatalf("Status after scoped build: %v", err)
	}

	if !status.CollectorVersionMismatch || status.FilesIndexed != 3 {
		t.Fatalf("expected stale collector warning with complete merged graph, got %#v", status)
	}
}

func TestConcurrentScopedBuildsPreserveAllRefreshes(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/concurrent-refresh\n\ngo 1.22\n")
	writeFile(t, filepath.Join(root, "pkg", "a.go"), "package pkg\n\nfunc OldA() string { return \"a\" }\n")
	writeFile(t, filepath.Join(root, "pkg", "b.go"), "package pkg\n\nfunc OldB() string { return \"b\" }\n")
	writeFile(t, filepath.Join(root, "pkg", "c.go"), "package pkg\n\nfunc KeepC() string { return \"c\" }\n")

	if _, err := Build(context.Background(), BuildOptions{Root: root, Language: "go"}); err != nil {
		t.Fatalf("Build: %v", err)
	}

	writeFile(t, filepath.Join(root, "pkg", "a.go"), "package pkg\n\nfunc NewA() string { return \"a2\" }\n")
	writeFile(t, filepath.Join(root, "pkg", "b.go"), "package pkg\n\nfunc NewB() string { return \"b2\" }\n")

	var wg sync.WaitGroup

	errs := make(chan error, 2)

	for _, path := range []string{"pkg/a.go", "pkg/b.go"} {
		path := path

		wg.Add(1)

		go func() {
			defer wg.Done()

			_, err := Build(context.Background(), BuildOptions{Root: root, Path: path, Language: "go"})
			errs <- err
		}()
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent scoped Build: %v", err)
		}
	}

	graph, err := LoadGraph(root)
	if err != nil {
		t.Fatalf("LoadGraph: %v", err)
	}

	for _, want := range []string{"symbol://pkg/a.go#NewA", "symbol://pkg/b.go#NewB", "symbol://pkg/c.go#KeepC"} {
		if !hasNode(graph.Nodes, want) {
			t.Fatalf("expected graph to retain %s after concurrent refreshes, got nodes=%#v", want, graph.Nodes)
		}
	}

	for _, old := range []string{"symbol://pkg/a.go#OldA", "symbol://pkg/b.go#OldB"} {
		if hasNode(graph.Nodes, old) {
			t.Fatalf("expected old stale symbol %s to be gone, got nodes=%#v", old, graph.Nodes)
		}
	}

	status, err := Status(root)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}

	if !status.Fresh || len(status.StaleFiles) != 0 || status.FilesIndexed != 3 {
		t.Fatalf("expected fresh graph after serialized concurrent scoped refreshes, got %#v", status)
	}
}

func TestGraphBuildLockRespectsContextWhileHeld(t *testing.T) {
	root := t.TempDir()

	unlock, err := lockGraphBuild(context.Background(), root)
	if err != nil {
		t.Fatalf("lockGraphBuild: %v", err)
	}
	defer unlock()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := lockGraphBuild(ctx, root); err == nil {
		t.Fatal("expected canceled context while waiting for graph build lock")
	}
}

func TestCppCallTargetResolverMatchesScopedPreferenceRules(t *testing.T) {
	index := &semantic.CppIndex{Files: []semantic.CppFile{
		{
			Path: "src/a.cpp",
			Symbols: []semantic.CppSymbol{
				{Name: "Run", Kind: "func", HasBody: false},
				{Name: "Run", Qualified: "Worker::Run", Scope: "Worker", Kind: "method", HasBody: true},
			},
		},
		{
			Path: "other/b.cpp",
			Symbols: []semantic.CppSymbol{
				{Name: "Run", Kind: "func", HasBody: true},
			},
		},
	}}

	resolver := newCppCallTargetResolver(index, "src")
	if got, want := resolver.targetID(semantic.CppCall{Name: "Run"}), "symbol://src/a.cpp#Worker::Run"; got != want {
		t.Fatalf("expected scoped method body target %q, got %q", want, got)
	}

	if got, want := resolver.targetID(semantic.CppCall{Name: "Worker::Run"}), "symbol://src/a.cpp#Worker::Run"; got != want {
		t.Fatalf("expected qualified target %q, got %q", want, got)
	}

	if got := resolver.targetID(semantic.CppCall{Name: "Missing"}); got != "" {
		t.Fatalf("expected missing call target to stay unresolved, got %q", got)
	}
}

func TestBuildRecordsLanguageEnvironmentEnrichment(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "pyproject.toml"), "[project]\nname = \"demo\"\n")
	writeFile(t, filepath.Join(root, "app.py"), "def run():\n    return 'ok'\n")

	report, err := Build(context.Background(), BuildOptions{Root: root, Language: "python"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if !hasCollectorReport(report.CollectorReports, "python_lsp", "python") {
		t.Fatalf("expected Python LSP availability report, got %#v", report.CollectorReports)
	}

	graph, err := LoadGraph(root)
	if err != nil {
		t.Fatalf("LoadGraph: %v", err)
	}

	if !hasNodeKind(graph.Nodes, "toolchain://python/python_lsp", "toolchain_backend") {
		t.Fatalf("expected Python toolchain backend node, got %#v", graph.Nodes)
	}

	if !hasNode(graph.Nodes, "manifest://pyproject.toml") || !hasEdge(graph.Edges, "configures", "repo://"+rootFingerprint(root), "manifest://pyproject.toml") {
		t.Fatalf("expected pyproject manifest enrichment, nodes=%#v edges=%#v", graph.Nodes, graph.Edges)
	}

	status, err := Status(root)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}

	if !hasCollectorReport(status.CollectorReports, "python_lsp", "python") {
		t.Fatalf("expected status to load collector reports from SQLite, got %#v", status.CollectorReports)
	}

	query, err := Query(QueryOptions{Root: root, Query: "python_lsp", Resolution: "any"})
	if err != nil {
		t.Fatalf("Query toolchain: %v", err)
	}

	if len(query.Nodes) == 0 {
		t.Fatalf("expected toolchain backend to be queryable, got %#v", query)
	}
}

func TestBuildAppliesExternalGraphEnricher(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/enriched\n\ngo 1.22\n")
	writeFile(t, filepath.Join(root, "main.go"), "package main\n\nfunc Run() {}\n")

	report, err := Build(context.Background(), BuildOptions{
		Root:      root,
		Language:  "go",
		Enrichers: []GraphEnricher{fakeGraphEnricher{}},
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if !hasCollectorReport(report.CollectorReports, "fake_lsp_enricher", "go") {
		t.Fatalf("expected fake enricher report, got %#v", report.CollectorReports)
	}

	graph, err := LoadGraph(root)
	if err != nil {
		t.Fatalf("LoadGraph: %v", err)
	}

	if !hasNode(graph.Nodes, "evidence://fake/run") || !hasEdge(graph.Edges, "enriched_by", "symbol://main.go#Run", "evidence://fake/run") {
		t.Fatalf("expected fake enrichment node and edge, nodes=%#v edges=%#v", graph.Nodes, graph.Edges)
	}
}

func TestStorageVersionStatusReportsSQLiteCompatibility(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/storage\n\ngo 1.22\n")
	writeFile(t, filepath.Join(root, "pkg", "pkg.go"), "package pkg\n\nfunc Run() string { return \"ok\" }\n")

	report, err := Build(context.Background(), BuildOptions{Root: root, Language: "go"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if report.StoreKind != sqliteStoreKind || !strings.HasSuffix(report.ArtifactPath, "graph.sqlite") {
		t.Fatalf("expected active SQLite artifact, got %#v", report)
	}

	if !hasStorageBackend(report.StorageBackends, sqliteStoreKind, "active", true) || !hasStorageBackend(report.StorageBackends, storeKind, "export", true) {
		t.Fatalf("expected active SQLite store and compatible JSON export, got %#v", report.StorageBackends)
	}

	status, err := Status(root)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}

	if status.StoreKind != sqliteStoreKind || !hasStorageBackend(status.StorageBackends, sqliteStoreKind, "active", true) || !hasStorageBackend(status.StorageBackends, storeKind, "export", true) {
		t.Fatalf("expected storage backend status in context_status, got %#v", status.StorageBackends)
	}

	writeFile(t, filepath.Join(root, ".idiom", "comprehension", "graph.sqlite"), "SQLite format 3\x00placeholder")

	if err := writeSQLiteMetadata(root, SQLiteMetadata{StoreKind: sqliteStoreKind, SchemaVersion: sqliteGraphSchemaVersion + 1}); err != nil {
		t.Fatalf("write sqlite metadata: %v", err)
	}

	status, err = Status(root)
	if err != nil {
		t.Fatalf("Status with sqlite metadata: %v", err)
	}

	if !hasStorageBackend(status.StorageBackends, sqliteStoreKind, "incompatible", false) {
		t.Fatalf("expected newer SQLite schema to be reported incompatible, got %#v", status.StorageBackends)
	}

	if _, err := Query(QueryOptions{Root: root, Symbol: "Run", Resolution: "symbol"}); err == nil {
		t.Fatalf("expected context_query to reject incompatible active SQLite instead of silently using JSON export")
	}
}

func TestBuildReplacesInvalidSQLiteStore(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/recover\n\ngo 1.22\n")
	writeFile(t, filepath.Join(root, "pkg", "pkg.go"), "package pkg\n\nfunc Run() string { return \"ok\" }\n")

	if _, err := Build(context.Background(), BuildOptions{Root: root, Language: "go"}); err != nil {
		t.Fatalf("initial Build: %v", err)
	}

	writeFile(t, filepath.Join(root, ".idiom", "comprehension", "graph.sqlite"), "SQLite format 3\x00placeholder")

	if err := writeSQLiteMetadata(root, SQLiteMetadata{StoreKind: sqliteStoreKind, SchemaVersion: sqliteGraphSchemaVersion + 1}); err != nil {
		t.Fatalf("write incompatible sqlite metadata: %v", err)
	}

	report, err := Build(context.Background(), BuildOptions{Root: root, Language: "go"})
	if err != nil {
		t.Fatalf("refresh Build should replace incompatible SQLite: %v", err)
	}

	if report.StoreKind != sqliteStoreKind || !hasStorageBackend(report.StorageBackends, sqliteStoreKind, "active", true) {
		t.Fatalf("expected refreshed active SQLite backend, got %#v", report)
	}

	query, err := Query(QueryOptions{Root: root, Symbol: "Run", Resolution: "symbol"})
	if err != nil {
		t.Fatalf("Query after SQLite replacement: %v", err)
	}

	if len(query.Nodes) == 0 {
		t.Fatalf("expected rebuilt SQLite graph to answer query, got %#v", query)
	}
}

func TestStatusRejectsMalformedCurrentSQLiteStore(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/malformed\n\ngo 1.22\n")
	writeFile(t, filepath.Join(root, "pkg", "pkg.go"), "package pkg\n\nfunc Run() string { return \"ok\" }\n")

	if _, err := Build(context.Background(), BuildOptions{Root: root, Language: "go"}); err != nil {
		t.Fatalf("Build: %v", err)
	}

	writeFile(t, filepath.Join(root, ".idiom", "comprehension", "graph.sqlite"), "SQLite format 3\x00placeholder")

	if err := writeSQLiteMetadata(root, SQLiteMetadata{StoreKind: sqliteStoreKind, SchemaVersion: sqliteGraphSchemaVersion}); err != nil {
		t.Fatalf("write current sqlite metadata: %v", err)
	}

	status, err := Status(root)
	if err != nil {
		t.Fatalf("Status should report malformed SQLite through backend status: %v", err)
	}

	if !hasStorageBackend(status.StorageBackends, sqliteStoreKind, "invalid", false) {
		t.Fatalf("expected malformed current SQLite to be invalid, got %#v", status.StorageBackends)
	}

	if _, err := Query(QueryOptions{Root: root, Symbol: "Run", Resolution: "symbol"}); err == nil {
		t.Fatalf("expected query to reject malformed active SQLite")
	}

	report, err := Build(context.Background(), BuildOptions{Root: root, Language: "go"})
	if err != nil {
		t.Fatalf("refresh Build should replace malformed SQLite: %v", err)
	}

	if !hasStorageBackend(report.StorageBackends, sqliteStoreKind, "active", true) {
		t.Fatalf("expected rebuilt active SQLite backend, got %#v", report.StorageBackends)
	}
}

func TestStatusDetectsFreelistCorruptionAndBuildRecovers(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/freelist\n\ngo 1.22\n")
	writeFile(t, filepath.Join(root, "pkg", "pkg.go"), "package pkg\n\nfunc Run() string { return \"ok\" }\n")

	if _, err := Build(context.Background(), BuildOptions{Root: root, Language: "go"}); err != nil {
		t.Fatalf("Build: %v", err)
	}

	corruptSQLiteFreelistCount(t, root)

	status, err := Status(root)
	if err != nil {
		t.Fatalf("Status should report freelist corruption through backend status: %v", err)
	}

	if status.Indexed {
		t.Fatalf("corrupt SQLite status should not be indexed, got %#v", status)
	}

	if !hasStorageBackend(status.StorageBackends, sqliteStoreKind, "invalid", false) {
		t.Fatalf("expected corrupt SQLite backend to be invalid, got %#v", status.StorageBackends)
	}

	report, err := Build(context.Background(), BuildOptions{Root: root, Language: "go"})
	if err != nil {
		t.Fatalf("Build should replace freelist-corrupt SQLite: %v", err)
	}

	if !hasStorageBackend(report.StorageBackends, sqliteStoreKind, "active", true) {
		t.Fatalf("expected rebuilt active SQLite backend, got %#v", report.StorageBackends)
	}
}

func corruptSQLiteFreelistCount(t *testing.T, root string) {
	t.Helper()

	db, err := openSQLite(sqliteGraphPath(root))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}

	if _, err := db.Exec(`CREATE TABLE graph_corruption_junk(x BLOB)`); err != nil {
		_ = db.Close()

		t.Fatalf("create corruption junk table: %v", err)
	}

	if _, err := db.Exec(`INSERT INTO graph_corruption_junk VALUES(zeroblob(100000))`); err != nil {
		_ = db.Close()

		t.Fatalf("fill corruption junk table: %v", err)
	}

	if _, err := db.Exec(`DROP TABLE graph_corruption_junk`); err != nil {
		_ = db.Close()

		t.Fatalf("drop corruption junk table: %v", err)
	}

	if err := db.Close(); err != nil {
		t.Fatalf("close sqlite: %v", err)
	}

	path := sqliteGraphPath(root)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read sqlite: %v", err)
	}

	if len(data) < 40 {
		t.Fatalf("sqlite file too small to corrupt freelist count: %d", len(data))
	}

	if binary.BigEndian.Uint32(data[36:40]) == 0 {
		t.Fatalf("expected dropped junk table to create freelist pages")
	}

	binary.BigEndian.PutUint32(data[36:40], 0)

	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write corrupted sqlite: %v", err)
	}
}

func TestStatusReportsInvalidSQLiteWhenJSONExportIsMissing(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/missingjson\n\ngo 1.22\n")
	writeFile(t, filepath.Join(root, "pkg", "pkg.go"), "package pkg\n\nfunc Run() string { return \"ok\" }\n")

	if _, err := Build(context.Background(), BuildOptions{Root: root, Language: "go"}); err != nil {
		t.Fatalf("Build: %v", err)
	}

	writeFile(t, filepath.Join(root, ".idiom", "comprehension", "graph.sqlite"), "SQLite format 3\x00placeholder")

	if err := writeSQLiteMetadata(root, SQLiteMetadata{StoreKind: sqliteStoreKind, SchemaVersion: sqliteGraphSchemaVersion}); err != nil {
		t.Fatalf("write current sqlite metadata: %v", err)
	}

	if err := os.Remove(filepath.Join(root, ".idiom", "comprehension", "graph.json")); err != nil {
		t.Fatalf("remove JSON export: %v", err)
	}

	status, err := Status(root)
	if err != nil {
		t.Fatalf("Status should report invalid backend instead of failing when JSON export is missing: %v", err)
	}

	if status.Indexed || status.Fresh || !hasStorageBackend(status.StorageBackends, sqliteStoreKind, "invalid", false) {
		t.Fatalf("expected unindexed invalid SQLite status, got %#v", status)
	}

	report, err := Build(context.Background(), BuildOptions{Root: root, Language: "go"})
	if err != nil {
		t.Fatalf("refresh Build should replace invalid SQLite without JSON export: %v", err)
	}

	if report.StoreKind != sqliteStoreKind || !hasStorageBackend(report.StorageBackends, sqliteStoreKind, "active", true) {
		t.Fatalf("expected rebuilt active SQLite backend, got %#v", report)
	}
}

func TestLoadGraphUsesActiveSQLiteWithoutJSONExport(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/sqlite\n\ngo 1.22\n")
	writeFile(t, filepath.Join(root, "pkg", "pkg.go"), "package pkg\n\nfunc Run() string { return \"ok\" }\n")

	report, err := Build(context.Background(), BuildOptions{Root: root, Language: "go"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if report.StoreKind != sqliteStoreKind {
		t.Fatalf("expected SQLite store, got %#v", report)
	}

	if err := os.Remove(filepath.Join(root, ".idiom", "comprehension", "graph.json")); err != nil {
		t.Fatalf("remove JSON export: %v", err)
	}

	graph, err := LoadGraph(root)
	if err != nil {
		t.Fatalf("LoadGraph from SQLite: %v", err)
	}

	if graph.StoreKind != sqliteStoreKind || !hasNode(graph.Nodes, "symbol://pkg/pkg.go#Run") {
		t.Fatalf("expected graph loaded from SQLite, got store=%q nodes=%#v", graph.StoreKind, graph.Nodes)
	}

	status, err := Status(root)
	if err != nil {
		t.Fatalf("Status from SQLite: %v", err)
	}

	if !status.Indexed || status.StoreKind != sqliteStoreKind || !hasStorageBackend(status.StorageBackends, storeKind, "absent", true) {
		t.Fatalf("expected indexed SQLite status with missing JSON export, got %#v", status)
	}
}

func TestPackProducesPacketReadyContextView(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/pack\n\ngo 1.22\n")
	writeFile(t, filepath.Join(root, "pkg", "runner.go"), `package pkg

func Run() string {
	return helper()
}

func helper() string {
	return "ok"
}
`)
	writeFile(t, filepath.Join(root, "pkg", "runner_test.go"), `package pkg

import "testing"

func TestRun(t *testing.T) {
	if Run() == "" {
		t.Fatal("empty")
	}
}
`)

	if _, err := Build(context.Background(), BuildOptions{Root: root, Language: "go"}); err != nil {
		t.Fatalf("Build: %v", err)
	}

	pack, err := Pack(PackOptions{
		Root:           root,
		Task:           "Plan a change to Run and preserve the helper behavior",
		Mode:           "plan",
		RequiredTests:  []string{"go test ./..."},
		ForbiddenPaths: []string{"vendor"},
		Now:            testTime(),
	})
	if err != nil {
		t.Fatalf("Pack: %v", err)
	}

	if pack.PackID == "" || pack.ArtifactPath == "" || !pack.Fresh {
		t.Fatalf("expected fresh saved pack identity, got %#v", pack)
	}

	if !hasPackFile(pack.CandidateFiles, "pkg/runner.go", "editable") {
		t.Fatalf("expected editable runner candidate, got %#v", pack.CandidateFiles)
	}

	if !hasPackSymbol(pack.CandidateSymbols, "symbol://pkg/runner.go#Run") {
		t.Fatalf("expected Run symbol candidate, got %#v", pack.CandidateSymbols)
	}

	if !hasPackTestCommand(pack.Tests, "go test ./...") || !hasPackTestCommand(pack.Tests, "go test ./pkg") {
		t.Fatalf("expected required and inferred Go tests, got %#v", pack.Tests)
	}

	if !hasPackEvidence(pack.Evidence, "context_view", pack.ArtifactPath) {
		t.Fatalf("expected context_view evidence pointing at artifact, got %#v", pack.Evidence)
	}

	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(pack.ArtifactPath))); err != nil {
		t.Fatalf("expected pack artifact to be written: %v", err)
	}

	writeFile(t, filepath.Join(root, "pkg", "runner.go"), "package pkg\n\nfunc Run() string { return \"changed\" }\n")

	stalePack, err := Pack(PackOptions{Root: root, Task: "Plan a change to Run", Now: testTime()})
	if err != nil {
		t.Fatalf("stale Pack: %v", err)
	}

	if stalePack.Fresh || len(stalePack.UnresolvedGaps) == 0 || len(stalePack.Risks) == 0 {
		t.Fatalf("expected stale pack to carry gaps and risks, got %#v", stalePack)
	}

	if !hasStalePackFile(stalePack.CandidateFiles, "pkg/runner.go") {
		t.Fatalf("expected stale candidate marker, got %#v", stalePack.CandidateFiles)
	}
}

func TestPackPrioritizesRequiredEditPaths(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/requiredpath\n\ngo 1.22\n")
	writeFile(t, filepath.Join(root, "pkg", "target.go"), `package pkg

func Target() string {
	return "target"
}
`)
	writeFile(t, filepath.Join(root, "pkg", "noisy.go"), `package pkg

func Noisy() string {
	return "noisy"
}
`)

	if _, err := Build(context.Background(), BuildOptions{Root: root, Language: "go"}); err != nil {
		t.Fatalf("Build: %v", err)
	}

	pack, err := Pack(PackOptions{
		Root:              root,
		Task:              "Plan a change to Noisy",
		Mode:              "plan",
		Limit:             1,
		RequiredEditPaths: []string{"pkg/target.go"},
		Now:               testTime(),
	})
	if err != nil {
		t.Fatalf("Pack: %v", err)
	}

	if len(pack.CandidateFiles) != 1 || pack.CandidateFiles[0].Path != "pkg/target.go" {
		t.Fatalf("required edit path should outrank task-term matches, got %#v", pack.CandidateFiles)
	}

	if len(pack.RequiredEditPaths) != 1 || pack.RequiredEditPaths[0] != "pkg/target.go" {
		t.Fatalf("expected required edit paths in result contract, got %#v", pack.RequiredEditPaths)
	}

	if !slicesContainsRankerReason(pack.CandidateFiles[0].RankerReasons, "required_edit_path") {
		t.Fatalf("expected required_edit_path ranker reason, got %#v", pack.CandidateFiles[0].RankerReasons)
	}
}

func TestPackIncludesSpecificTermMatchesWhenGenericTermsAreNoisy(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/specificterms\n\ngo 1.22\n")

	for i := 0; i < 14; i++ {
		writeFile(t, filepath.Join(root, "internal", "runtime", "agent", fmt.Sprintf("conversation_answer_%02d.go", i)), fmt.Sprintf(`package agent

func ConversationAnswerDebug%02d() string {
	return "conversation answer debug"
}
`, i))
	}

	writeFile(t, filepath.Join(root, "internal", "surfaces", "tui", "app", "rendering.go"), `package app

type Model struct{}

func (m Model) Resize() {}

func (m Model) UpdateViewport() {}

func ScrollableViewportKey() string {
	return "viewport"
}
`)

	if _, err := Build(context.Background(), BuildOptions{Root: root, Language: "go"}); err != nil {
		t.Fatalf("Build: %v", err)
	}

	pack, err := Pack(PackOptions{
		Root:  root,
		Task:  "you are a research assistant for another agentic process; please provide structured evidence in the form of files, line numbers, and relevant symbols. We are trying to debug an issue where resizing the TUI mid-answer caused the conversation to become non-scrollable. Please debug and present your findings.",
		Mode:  "plan",
		Limit: 3,
		Now:   testTime(),
	})
	if err != nil {
		t.Fatalf("Pack: %v", err)
	}

	if !hasPackFile(pack.CandidateFiles, "internal/surfaces/tui/app/rendering.go", "editable") {
		t.Fatalf("expected specific resize/viewport candidate despite noisy answer/conversation matches, got %#v", pack.CandidateFiles)
	}
}

func TestPackBoundsFileHandlesForSmallModelPackets(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/bounded-pack\n\ngo 1.22\n")

	var source strings.Builder

	source.WriteString("package pkg\n\n")

	for i := 0; i < 30; i++ {
		source.WriteString("func Function")
		source.WriteString(string(rune('A' + i%26)))
		source.WriteString("() {}\n")
	}

	writeFile(t, filepath.Join(root, "pkg", "many.go"), source.String())

	if _, err := Build(context.Background(), BuildOptions{Root: root, Language: "go"}); err != nil {
		t.Fatalf("Build: %v", err)
	}

	pack, err := Pack(PackOptions{Root: root, Task: "func", Limit: 8, Now: testTime()})
	if err != nil {
		t.Fatalf("Pack: %v", err)
	}

	var candidate PackFile

	for _, file := range pack.CandidateFiles {
		if file.Path == "pkg/many.go" {
			candidate = file
			break
		}
	}

	if candidate.Path == "" {
		t.Fatalf("expected many.go candidate, got %#v", pack.CandidateFiles)
	}

	if len(candidate.Handles) > maxPackFileHandles || candidate.OmittedHandles == 0 {
		t.Fatalf("expected bounded file handles with omission count, got %#v", candidate)
	}
}

func TestTracePackParsesRuntimeEvidenceAndEnrichesPack(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/trace\n\ngo 1.22\n")
	writeFile(t, filepath.Join(root, "pkg", "runner.go"), `package pkg

func Run() string {
	return helper()
}

func helper() string {
	return "ok"
}
`)
	writeFile(t, filepath.Join(root, "pkg", "runner_test.go"), `package pkg

import "testing"

func TestRun(t *testing.T) {
	if Run() == "" {
		t.Fatal("empty")
	}
}
`)
	writeFile(t, filepath.Join(root, "cover.out"), "mode: set\npkg/runner.go:3.1,5.2 1 1\npkg/runner.go:7.1,9.2 1 0\n")

	if _, err := Build(context.Background(), BuildOptions{Root: root, Language: "go"}); err != nil {
		t.Fatalf("Build: %v", err)
	}

	stdout := `{"Action":"run","Package":"example.com/trace/pkg","Test":"TestRun"}
{"Action":"output","Package":"example.com/trace/pkg","Test":"TestRun","Output":"    ` + filepath.ToSlash(filepath.Join(root, "pkg", "runner.go")) + `:4: empty\n"}
{"Action":"fail","Package":"example.com/trace/pkg","Test":"TestRun","Elapsed":0.01}
{"Action":"fail","Package":"example.com/trace/pkg","Elapsed":0.01}
`

	trace, err := Trace(TraceOptions{
		Root:      root,
		Command:   "go test ./pkg -json -coverprofile=cover.out",
		Language:  "auto",
		Stdout:    stdout,
		ExitCode:  1,
		ExecError: "command failed: exit status 1",
		Now:       testTime(),
	})
	if err != nil {
		t.Fatalf("Trace: %v", err)
	}

	if trace.TraceID == "" || trace.ArtifactPath == "" || trace.Success || !trace.Fresh {
		t.Fatalf("expected saved failing fresh trace identity, got %#v", trace)
	}

	if !hasTraceAnchor(trace.FailingAnchors, "pkg/runner.go", 4) {
		t.Fatalf("expected failing file anchor, got %#v", trace.FailingAnchors)
	}

	if !hasTraceCoverage(trace.Coverage, "pkg/runner.go") {
		t.Fatalf("expected coverage for runner.go, got %#v", trace.Coverage)
	}

	if !hasEdgeSummary(trace.RuntimeEdges, "runtime_reads", "runtime_trace://"+trace.TraceID, "file://pkg/runner.go") {
		t.Fatalf("expected runtime file edge, got %#v", trace.RuntimeEdges)
	}

	if !hasEdgeSummary(trace.RuntimeEdges, "runtime_calls", "runtime_trace://"+trace.TraceID, "symbol://pkg/runner_test.go#TestRun") {
		t.Fatalf("expected runtime test edge, got %#v", trace.RuntimeEdges)
	}

	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(trace.ArtifactPath))); err != nil {
		t.Fatalf("expected trace artifact to be written: %v", err)
	}

	pack, err := Pack(PackOptions{Root: root, Task: "Fix failing TestRun in runner", Now: testTime()})
	if err != nil {
		t.Fatalf("Pack with runtime overlay: %v", err)
	}

	if !hasPackEvidence(pack.Evidence, "runtime_trace", trace.ArtifactPath) {
		t.Fatalf("expected runtime trace evidence in pack, got %#v", pack.Evidence)
	}

	if !hasPackEvidence(pack.Evidence, "runtime_anchor", "pkg/runner.go") {
		t.Fatalf("expected runtime anchor evidence in pack, got %#v", pack.Evidence)
	}

	if !hasPackFile(pack.CandidateFiles, "pkg/runner.go", "editable") && !hasPackFile(pack.CandidateFiles, "pkg/runner.go", "read_only_evidence") {
		t.Fatalf("expected runner.go candidate from static or runtime evidence, got %#v", pack.CandidateFiles)
	}

	if !hasPackTestCommand(pack.Tests, "go test ./pkg -json -coverprofile=cover.out") {
		t.Fatalf("expected failing runtime command to be carried as a test, got %#v", pack.Tests)
	}
}

func TestPackExcludesStaleRuntimeTraceEvidenceUntilRerun(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/staletrace\n\ngo 1.22\n")

	sourcePath := filepath.Join(root, "pkg", "runner.go")
	writeFile(t, sourcePath, `package pkg

func Run() string {
	return "old"
}
`)

	if _, err := Build(context.Background(), BuildOptions{Root: root, Language: "go"}); err != nil {
		t.Fatalf("Build: %v", err)
	}

	stdout := `{"Action":"run","Package":"example.com/staletrace/pkg","Test":"TestRun"}
{"Action":"output","Package":"example.com/staletrace/pkg","Test":"TestRun","Output":"    ` + filepath.ToSlash(sourcePath) + `:3: stale\n"}
{"Action":"fail","Package":"example.com/staletrace/pkg","Test":"TestRun","Elapsed":0.01}
`

	staleTrace, err := Trace(TraceOptions{
		Root:      root,
		Command:   "go test ./pkg -json",
		Language:  "auto",
		Stdout:    stdout,
		ExitCode:  1,
		ExecError: "command failed: exit status 1",
		Now:       testTime(),
	})
	if err != nil {
		t.Fatalf("Trace: %v", err)
	}

	writeFile(t, sourcePath, `package pkg

func Run() string {
	return "new"
}
`)

	if _, err := Build(context.Background(), BuildOptions{Root: root, Language: "go"}); err != nil {
		t.Fatalf("refresh Build: %v", err)
	}

	pack, err := Pack(PackOptions{Root: root, Task: "Fix failing TestRun in runner", Now: testTime().Add(time.Minute)})
	if err != nil {
		t.Fatalf("Pack with stale trace: %v", err)
	}

	if hasPackEvidence(pack.Evidence, "runtime_trace", staleTrace.ArtifactPath) {
		t.Fatalf("stale trace artifact should not be promoted into packet evidence: %#v", pack.Evidence)
	}

	if hasPackEvidence(pack.Evidence, "runtime_anchor", "pkg/runner.go") {
		t.Fatalf("stale trace anchor should not be promoted into packet evidence: %#v", pack.Evidence)
	}

	if !hasGapContaining(pack.UnresolvedGaps, "reason=stale_runtime_trace") || !hasGapContaining(pack.UnresolvedGaps, "stale_path_count=1") {
		t.Fatalf("expected stale trace gap, got %#v", pack.UnresolvedGaps)
	}

	debugPack, err := Pack(PackOptions{Root: root, Task: "Fix failing TestRun in runner", IncludeStaleRuntime: true, Now: testTime().Add(90 * time.Second)})
	if err != nil {
		t.Fatalf("debug Pack with stale trace: %v", err)
	}

	if !hasStalePackEvidence(debugPack.Evidence, "runtime_trace", staleTrace.ArtifactPath) {
		t.Fatalf("debug stale-runtime opt-in should label stale trace evidence, got %#v", debugPack.Evidence)
	}

	freshTrace, err := Trace(TraceOptions{
		Root:      root,
		Command:   "go test ./pkg -json",
		Language:  "auto",
		Stdout:    stdout,
		ExitCode:  1,
		ExecError: "command failed: exit status 1",
		Now:       testTime().Add(2 * time.Minute),
	})
	if err != nil {
		t.Fatalf("rerun Trace: %v", err)
	}

	pack, err = Pack(PackOptions{Root: root, Task: "Fix failing TestRun in runner", Now: testTime().Add(3 * time.Minute)})
	if err != nil {
		t.Fatalf("Pack with fresh trace: %v", err)
	}

	if !hasPackEvidence(pack.Evidence, "runtime_trace", freshTrace.ArtifactPath) {
		t.Fatalf("fresh rerun trace should appear in packet evidence, got %#v", pack.Evidence)
	}
}

func TestTracePackParsesLanguageAgnosticStackFrames(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "app.py"), "def run():\n    raise AssertionError('bad')\n")
	writeFile(t, filepath.Join(root, "scripts", "player.gd"), "func _ready():\n    push_error(\"bad\")\n")
	writeFile(t, filepath.Join(root, "src", "app.ts"), "export function run() {\n  throw new Error('bad')\n}\n")

	if _, err := Build(context.Background(), BuildOptions{Root: root, Language: "auto"}); err != nil {
		t.Fatalf("Build auto: %v", err)
	}

	stderr := `Traceback (most recent call last):
  File "` + filepath.ToSlash(filepath.Join(root, "app.py")) + `", line 2, in run
AssertionError: bad
res://scripts/player.gd:2: bad
    at run (` + filepath.ToSlash(filepath.Join(root, "src", "app.ts")) + `:2:9)
`

	trace, err := Trace(TraceOptions{
		Root:      root,
		Command:   "pytest tests && godot --headless --test && npm test",
		Language:  "auto",
		Stderr:    stderr,
		ExitCode:  1,
		ExecError: "command failed: exit status 1",
		Now:       testTime(),
	})
	if err != nil {
		t.Fatalf("Trace language agnostic: %v", err)
	}

	for _, want := range []struct {
		path string
		line int
	}{
		{"app.py", 2},
		{"scripts/player.gd", 2},
		{"src/app.ts", 2},
	} {
		if !hasTraceAnchor(trace.FailingAnchors, want.path, want.line) {
			t.Fatalf("expected stack anchor %s:%d, got %#v", want.path, want.line, trace.FailingAnchors)
		}
	}

	if !hasTraceFile(trace.ExecutedFiles, "app.py", "python") || !hasTraceFile(trace.ExecutedFiles, "scripts/player.gd", "gdscript") || !hasTraceFile(trace.ExecutedFiles, "src/app.ts", "typescript") {
		t.Fatalf("expected executed files with language labels, got %#v", trace.ExecutedFiles)
	}
}

func TestBuildIndexesPythonWithNativeCollector(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "services", "planner.py"), `from typing import Any
import json

class Planner:
    def plan(self, task: str) -> Any:
        return json.loads(task)
`)

	report, err := Build(context.Background(), BuildOptions{Root: root, Language: "python"})
	if err != nil {
		t.Fatalf("Build python: %v", err)
	}

	if report.FilesIndexed != 1 || report.SymbolsIndexed < 2 || !hasString(report.Languages, "python") || !hasString(report.CollectorCoverage, "python_index") {
		t.Fatalf("unexpected python build report: %#v", report)
	}

	graph, err := LoadGraph(root)
	if err != nil {
		t.Fatalf("LoadGraph: %v", err)
	}

	if !hasNode(graph.Nodes, "external://python:typing") || !hasEdge(graph.Edges, "imports", "file://services/planner.py", "external://python:typing") {
		t.Fatalf("expected generic import relationship for typing: nodes=%#v edges=%#v", graph.Nodes, graph.Edges)
	}

	query, err := Query(QueryOptions{Root: root, Symbol: "Planner", Resolution: "symbol", IncludeEvidence: true})
	if err != nil {
		t.Fatalf("Query python symbol: %v", err)
	}

	if !hasNodeSummary(query.Nodes, "symbol://services/planner.py#Planner") || len(query.Edges) == 0 {
		t.Fatalf("expected Python Planner symbol and evidence, got %#v", query)
	}

	sourcePath := filepath.Join(root, "services", "planner.py")
	writeFile(t, sourcePath, `class Planner:
    def plan_next(self, task: str) -> str:
        return task
`)

	status, err := Status(root)
	if err != nil {
		t.Fatalf("Status python: %v", err)
	}

	if status.Fresh || !hasString(status.StaleFiles, "services/planner.py") || status.StaleSymbols == 0 || len(status.StaleSymbolSamples) == 0 {
		t.Fatalf("expected stale Python symbols, got %#v", status)
	}

	refresh, err := Build(context.Background(), BuildOptions{Root: root, Language: "python"})
	if err != nil {
		t.Fatalf("refresh Build python: %v", err)
	}

	if refresh.RefreshedStaleSymbols == 0 || !hasString(refresh.RefreshedStaleFiles, "services/planner.py") {
		t.Fatalf("expected refresh to report stale symbol/file recovery, got %#v", refresh)
	}

	query, err = Query(QueryOptions{Root: root, Symbol: "plan_next", Resolution: "symbol"})
	if err != nil {
		t.Fatalf("Query refreshed python symbol: %v", err)
	}

	if !hasNodeSummary(query.Nodes, "symbol://services/planner.py#Planner.plan_next") {
		t.Fatalf("expected refreshed plan_next symbol, got %#v", query.Nodes)
	}
}

func TestBuildReportsGenericLanguageEnrichmentForUnregisteredLanguage(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "lib", "worker.rb"), `class Worker
  def run
  end
end
`)

	report, err := Build(context.Background(), BuildOptions{Root: root, Language: "ruby"})
	if err != nil {
		t.Fatalf("Build ruby: %v", err)
	}

	if !hasString(report.Languages, "ruby") || !hasCollectorReport(report.CollectorReports, "generic_language_model", "ruby") {
		t.Fatalf("expected generic language enrichment for Ruby, got %#v", report)
	}

	graph, err := LoadGraph(root)
	if err != nil {
		t.Fatalf("LoadGraph ruby: %v", err)
	}

	if !hasNodeKind(graph.Nodes, "toolchain://ruby/generic_language_model", "toolchain_backend") {
		t.Fatalf("expected generic backend node for Ruby, got %#v", graph.Nodes)
	}
}

func TestSourceLanguageForPathCoversSupportedExtensionVariants(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{path: "pkg/types.pyi", want: "python"},
		{path: "src/entry.mts", want: "typescript"},
		{path: "src/common.cts", want: "typescript"},
		{path: "types/external.d.ts", want: "typescript"},
		{path: "types/external.d.mts", want: "typescript"},
		{path: "types/external.d.cts", want: "typescript"},
		{path: "include/detail.hh", want: "cpp"},
		{path: "include/detail.tpp", want: "cpp"},
		{path: "scripts/player.gd", want: "gdscript"},
	}
	for _, tc := range tests {
		if got := sourceLanguageForPath(tc.path); got != tc.want {
			t.Fatalf("sourceLanguageForPath(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

func TestBuildIndexesTypeScriptModuleExtensions(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "src", "helper.cts"), `export function helper() {
  return "ok";
}
`)
	writeFile(t, filepath.Join(root, "src", "main.mts"), `import { helper } from "./helper";

export function main() {
  return helper();
}
`)

	report, err := Build(context.Background(), BuildOptions{Root: root, Language: "typescript"})
	if err != nil {
		t.Fatalf("Build typescript module extensions: %v", err)
	}

	if !hasString(report.Languages, "typescript") || !hasString(report.CollectorCoverage, "typescript_index") {
		t.Fatalf("expected TypeScript collector coverage, got languages=%#v collectors=%#v", report.Languages, report.CollectorCoverage)
	}

	graph, err := LoadGraph(root)
	if err != nil {
		t.Fatalf("LoadGraph typescript module extensions: %v", err)
	}

	if !hasFileLanguage(graph.Nodes, "src/main.mts", "typescript") || !hasFileLanguage(graph.Nodes, "src/helper.cts", "typescript") {
		t.Fatalf("expected .mts/.cts files to be indexed as TypeScript, got %#v", graph.Nodes)
	}

	if !hasEdge(graph.Edges, "imports", "file://src/main.mts", "file://src/helper.cts") {
		t.Fatalf("expected TypeScript .mts -> .cts import edge, got %#v", graph.Edges)
	}

	if !hasEdge(graph.Edges, "calls", "symbol://src/main.mts#main", "symbol://src/helper.cts#helper") {
		t.Fatalf("expected TypeScript call edge across module extensions, got %#v", graph.Edges)
	}
}

func TestNativeCollectorsAddLocalRelationshipsAcrossLanguageFamilies(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "pkg", "helper.py"), `def run():
    return "ok"

class Runner:
    def execute(self):
        return run()
`)
	writeFile(t, filepath.Join(root, "tests", "test_helper.py"), `from pkg.helper import run

def test_run():
    return run()
`)
	writeFile(t, filepath.Join(root, "pkg", "plugin.py"), `def activate():
    return 1
`)
	writeFile(t, filepath.Join(root, "pkg", "dynamic.py"), `from . import plugin
import importlib

def load():
    module = importlib.import_module("pkg.helper")
    return module.run() + plugin.activate()
`)
	writeFile(t, filepath.Join(root, "src", "util.ts"), `export function greet() {
  return "hi";
}

export class Greeter {
  say() {
    return greet();
  }
}
`)
	writeFile(t, filepath.Join(root, "src", "util.test.ts"), `import { greet } from "./util";

function testGreet() {
  return greet();
}
`)
	writeFile(t, filepath.Join(root, "src", "reexport.ts"), `export { greet } from "./util";
`)
	writeFile(t, filepath.Join(root, "src", "lib.rs"), `pub fn run() {
    helper();
}

fn helper() {}
`)
	writeFile(t, filepath.Join(root, "src", "main", "java", "com", "example", "Helper.java"), `package com.example;

class Helper {}
`)
	writeFile(t, filepath.Join(root, "src", "main", "java", "com", "example", "App.java"), `package com.example;

import com.example.Helper;

class App {
  Helper run() {
    return new Helper();
  }
}
`)

	report, err := Build(context.Background(), BuildOptions{Root: root, Language: "auto"})
	if err != nil {
		t.Fatalf("Build auto generic relationships: %v", err)
	}

	for _, language := range []string{"python", "typescript", "rust", "java"} {
		if !hasString(report.Languages, language) {
			t.Fatalf("expected language %q in report, got %#v", language, report.Languages)
		}
	}

	for _, collector := range []string{"python_index", "typescript_index", "rust_index", "java_index"} {
		if !hasString(report.CollectorCoverage, collector) {
			t.Fatalf("expected collector %q in report, got %#v", collector, report.CollectorCoverage)
		}
	}

	graph, err := LoadGraph(root)
	if err != nil {
		t.Fatalf("LoadGraph generic relationships: %v", err)
	}

	if !hasEdge(graph.Edges, "imports", "file://tests/test_helper.py", "file://pkg/helper.py") {
		t.Fatalf("expected Python local import edge, got %#v", graph.Edges)
	}

	if !hasEdge(graph.Edges, "imports", "file://pkg/dynamic.py", "file://pkg/plugin.py") || !hasEdge(graph.Edges, "imports", "file://pkg/dynamic.py", "file://pkg/helper.py") {
		t.Fatalf("expected Python relative and dynamic import edges, got %#v", graph.Edges)
	}

	if !hasNodeKind(graph.Nodes, "symbol://tests/test_helper.py#test_run", "test") || !hasEdge(graph.Edges, "tests", "symbol://tests/test_helper.py#test_run", "package://tests") {
		t.Fatalf("expected Python test symbol and test edge, nodes=%#v edges=%#v", graph.Nodes, graph.Edges)
	}

	if !hasEdge(graph.Edges, "calls", "symbol://tests/test_helper.py#test_run", "symbol://pkg/helper.py#run") {
		t.Fatalf("expected Python call edge from test to helper, got %#v", graph.Edges)
	}

	if !hasEdge(graph.Edges, "contains", "symbol://pkg/helper.py#Runner", "symbol://pkg/helper.py#Runner.execute") || !hasEdge(graph.Edges, "calls", "symbol://pkg/helper.py#Runner.execute", "symbol://pkg/helper.py#run") {
		t.Fatalf("expected Python class method containment and call edges, got %#v", graph.Edges)
	}

	if !hasEdge(graph.Edges, "imports", "file://src/util.test.ts", "file://src/util.ts") {
		t.Fatalf("expected TypeScript relative import edge, got %#v", graph.Edges)
	}

	if !hasEdge(graph.Edges, "imports", "file://src/reexport.ts", "file://src/util.ts") {
		t.Fatalf("expected TypeScript re-export dependency edge, got %#v", graph.Edges)
	}

	if !hasNodeKind(graph.Nodes, "symbol://src/util.test.ts#testGreet", "test") || !hasEdge(graph.Edges, "calls", "symbol://src/util.test.ts#testGreet", "symbol://src/util.ts#greet") {
		t.Fatalf("expected TypeScript test and call edges, nodes=%#v edges=%#v", graph.Nodes, graph.Edges)
	}

	if !hasEdge(graph.Edges, "contains", "symbol://src/util.ts#Greeter", "symbol://src/util.ts#Greeter.say") || !hasEdge(graph.Edges, "calls", "symbol://src/util.ts#Greeter.say", "symbol://src/util.ts#greet") {
		t.Fatalf("expected TypeScript class method containment and call edges, got %#v", graph.Edges)
	}

	if !hasEdge(graph.Edges, "calls", "symbol://src/lib.rs#run", "symbol://src/lib.rs#helper") {
		t.Fatalf("expected Rust same-file call edge, got %#v", graph.Edges)
	}

	if !hasEdge(graph.Edges, "imports", "file://src/main/java/com/example/App.java", "file://src/main/java/com/example/Helper.java") {
		t.Fatalf("expected Java import edge to local file, got %#v", graph.Edges)
	}

	if !hasEdge(graph.Edges, "contains", "symbol://src/main/java/com/example/App.java#com.example.App", "symbol://src/main/java/com/example/App.java#com.example.App#run") || !hasEdge(graph.Edges, "calls", "symbol://src/main/java/com/example/App.java#com.example.App#run", "symbol://src/main/java/com/example/Helper.java#com.example.Helper") {
		t.Fatalf("expected Java method containment and constructor call edges, got %#v", graph.Edges)
	}
}

func TestBuildIndexesCppWithRichCollector(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "include", "entity_manager.h"), `#pragma once
struct EntityManager {
    void tick();
};
`)
	writeFile(t, filepath.Join(root, "include", "templates.hpp"), `#pragma once
template <typename T>
T identity(T value) {
    return value;
}
`)
	writeFile(t, filepath.Join(root, "alt", "entity_manager.h"), `#pragma once
struct OtherEntityManager {};
`)
	writeFile(t, filepath.Join(root, "src", "entity_manager.cpp"), `#include "include/entity_manager.h"

int helper() {
    return 1;
}

void EntityManager::tick() {
    helper();
}
`)
	writeFile(t, filepath.Join(root, "src", "main.cpp"), `#include "include/entity_manager.h"
#include "include/templates.hpp"
#include <vector>

int main() {
    EntityManager manager;
    manager.tick();
    return helper() + identity<int>(1);
}
`)
	writeFile(t, filepath.Join(root, "src", "ambiguous.cpp"), `#include "entity_manager.h"

int ambiguous() {
    return 0;
}
`)

	report, err := Build(context.Background(), BuildOptions{Root: root, Language: "cpp"})
	if err != nil {
		t.Fatalf("Build cpp: %v", err)
	}

	if report.FilesIndexed != 6 || report.SymbolsIndexed == 0 || !hasString(report.Languages, "cpp") || !hasString(report.CollectorCoverage, "cpp_index") {
		t.Fatalf("unexpected C++ build report: %#v", report)
	}

	graph, err := LoadGraph(root)
	if err != nil {
		t.Fatalf("LoadGraph cpp: %v", err)
	}

	if !hasEdge(graph.Edges, "imports", "file://src/main.cpp", "file://include/entity_manager.h") {
		t.Fatalf("expected C++ include relationship to resolved header, got %#v", graph.Edges)
	}

	if !hasEdge(graph.Edges, "imports", "file://src/main.cpp", "file://include/templates.hpp") || !hasNode(graph.Nodes, "symbol://include/templates.hpp#identity") {
		t.Fatalf("expected C++ template header and symbol indexing, nodes=%#v edges=%#v", graph.Nodes, graph.Edges)
	}

	if !hasNode(graph.Nodes, "external://cpp:vector") || !hasEdge(graph.Edges, "imports", "file://src/main.cpp", "external://cpp:vector") {
		t.Fatalf("expected C++ system include relationship for vector, nodes=%#v edges=%#v", graph.Nodes, graph.Edges)
	}

	if !hasEdge(graph.Edges, "imports", "file://src/ambiguous.cpp", "external://cpp:entity_manager.h") {
		t.Fatalf("expected ambiguous C++ include to stay external instead of guessing a header, got %#v", graph.Edges)
	}

	query, err := Query(QueryOptions{Root: root, Symbol: "tick", Resolution: "symbol", IncludeEvidence: true})
	if err != nil {
		t.Fatalf("Query cpp tick: %v", err)
	}

	if !hasNodeSummary(query.Nodes, "symbol://src/entity_manager.cpp#EntityManager::tick") || len(query.Edges) == 0 {
		t.Fatalf("expected C++ method symbol and evidence, got %#v", query)
	}

	calls, err := Zoom(ZoomOptions{Root: root, Handle: "symbol://src/main.cpp#main"})
	if err != nil {
		t.Fatalf("Zoom cpp main: %v", err)
	}

	if calls.Node == nil || !hasEdgeSummary(calls.Edges, "calls", "symbol://src/main.cpp#main", "symbol://src/entity_manager.cpp#helper") {
		t.Fatalf("expected C++ call relationship from main to helper, got %#v", calls)
	}

	if !hasEdgeSummary(calls.Edges, "calls", "symbol://src/main.cpp#main", "symbol://src/entity_manager.cpp#EntityManager::tick") {
		t.Fatalf("expected C++ method call to prefer the definition over the header declaration, got %#v", calls)
	}

	if !hasEdgeSummary(calls.Edges, "calls", "symbol://src/main.cpp#main", "symbol://include/templates.hpp#identity") {
		t.Fatalf("expected C++ template call relationship from main to identity, got %#v", calls)
	}

	if hasEdgeSummary(calls.Edges, "calls", "symbol://src/main.cpp#main", "symbol://include/entity_manager.h#tick") {
		t.Fatalf("C++ method call should not target the header declaration when a definition is available, got %#v", calls)
	}
}

func TestBuildIndexesGDScriptWithDedicatedCollector(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "project.godot"), `[application]
config/name="Demo"

[autoload]
Game="*res://scripts/player.gd"
`)
	writeFile(t, filepath.Join(root, "scripts", "base.gd"), `class_name BaseController

func reset():
    pass
`)
	writeFile(t, filepath.Join(root, "scripts", "player.gd"), `extends "res://scripts/base.gd"
class_name PlayerController

signal died
const Base = preload("res://scripts/base.gd")
@export var spawn_scene = preload("res://resources/spawn.tres")
export(PackedScene) var legacy_enemy_scene = preload("res://scenes/enemy.tscn")
@onready var hud = $Hud
var health = 10

func _ready():
    reset()
`)
	writeFile(t, filepath.Join(root, "scripts", "enemy.gd"), `extends Node
class_name EnemyController

func spawn():
    pass
`)
	writeFile(t, filepath.Join(root, "resources", "spawn.tres"), `[gd_resource type="Resource" load_steps=2 format=3]

[ext_resource type="Script" path="res://scripts/base.gd" id="1_base"]
`)
	writeFile(t, filepath.Join(root, "scenes", "main.tscn"), `[gd_scene load_steps=2 format=3]

[ext_resource type="Script" path="res://scripts/player.gd" id="1_player"]

[node name="Player" type="Node"]
script = ExtResource("1_player")
`)
	writeFile(t, filepath.Join(root, "scenes", "enemy.tscn"), `[gd_scene load_steps=2 format=3]

[ext_resource type="Script" path="res://scripts/enemy.gd" id="1_enemy"]

[node name="Enemy" type="Node"]
script = ExtResource("1_enemy")
`)

	report, err := Build(context.Background(), BuildOptions{Root: root, Language: "gdscript"})
	if err != nil {
		t.Fatalf("Build gdscript: %v", err)
	}

	if report.FilesIndexed != 7 || report.SymbolsIndexed < 10 || !hasString(report.Languages, "gdscript") || !hasString(report.CollectorCoverage, "gdscript_index") {
		t.Fatalf("unexpected GDScript build report: %#v", report)
	}

	if !hasCollectorReport(report.CollectorReports, "godot_project_model", "gdscript") {
		t.Fatalf("expected Godot project-model enrichment report, got %#v", report.CollectorReports)
	}

	graph, err := LoadGraph(root)
	if err != nil {
		t.Fatalf("LoadGraph gdscript: %v", err)
	}

	if !hasEdge(graph.Edges, "imports", "file://scripts/player.gd", "file://scripts/base.gd") {
		t.Fatalf("expected GDScript resource dependency edge to base script, got %#v", graph.Edges)
	}

	if !hasEdge(graph.Edges, "imports", "file://scenes/main.tscn", "file://scripts/player.gd") {
		t.Fatalf("expected GDScript scene dependency edge to player script, got %#v", graph.Edges)
	}

	if !hasEdge(graph.Edges, "imports", "file://scripts/player.gd", "file://resources/spawn.tres") {
		t.Fatalf("expected GDScript preload edge to internal resource, got %#v", graph.Edges)
	}

	if !hasEdge(graph.Edges, "imports", "file://scripts/player.gd", "file://scenes/enemy.tscn") || !hasEdge(graph.Edges, "imports", "file://scenes/enemy.tscn", "file://scripts/enemy.gd") {
		t.Fatalf("expected GDScript legacy export PackedScene and nested scene dependency edges, got %#v", graph.Edges)
	}

	if !hasEdge(graph.Edges, "imports", "file://resources/spawn.tres", "file://scripts/base.gd") {
		t.Fatalf("expected GDScript resource dependency edge to script, got %#v", graph.Edges)
	}

	if !hasEdge(graph.Edges, "imports", "file://project.godot", "file://scripts/player.gd") || !hasNode(graph.Nodes, "symbol://project.godot#Game") {
		t.Fatalf("expected Godot autoload symbol and dependency edge, nodes=%#v edges=%#v", graph.Nodes, graph.Edges)
	}

	if !hasNode(graph.Nodes, "symbol://scripts/player.gd#spawn_scene") {
		t.Fatalf("expected annotated GDScript exported var symbol, got %#v", graph.Nodes)
	}

	if !hasNode(graph.Nodes, "symbol://scripts/player.gd#legacy_enemy_scene") || !hasNode(graph.Nodes, "symbol://scripts/player.gd#hud") {
		t.Fatalf("expected legacy export and onready GDScript var symbols, got %#v", graph.Nodes)
	}

	if !hasNodeKind(graph.Nodes, "toolchain://gdscript/godot_project_model", "toolchain_backend") || !hasNode(graph.Nodes, "manifest://project.godot") {
		t.Fatalf("expected Godot toolchain and project manifest enrichment, got %#v", graph.Nodes)
	}

	query, err := Query(QueryOptions{Root: root, Symbol: "PlayerController", Resolution: "symbol", IncludeEvidence: true})
	if err != nil {
		t.Fatalf("Query gdscript class_name: %v", err)
	}

	if !hasNodeSummary(query.Nodes, "symbol://scripts/player.gd#PlayerController") || len(query.Edges) == 0 {
		t.Fatalf("expected GDScript class_name symbol and evidence, got %#v", query)
	}

	signalQuery, err := Query(QueryOptions{Root: root, Symbol: "died", Resolution: "symbol"})
	if err != nil {
		t.Fatalf("Query gdscript signal: %v", err)
	}

	if !hasNodeSummary(signalQuery.Nodes, "symbol://scripts/player.gd#died") {
		t.Fatalf("expected GDScript signal symbol, got %#v", signalQuery.Nodes)
	}
}

func writeFile(t *testing.T, path string, content string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func hasNode(nodes []Node, id string) bool {
	for _, node := range nodes {
		if node.ID == id {
			return true
		}
	}

	return false
}

func hasNodeKind(nodes []Node, id string, kind string) bool {
	for _, node := range nodes {
		if node.ID == id && node.Kind == kind {
			return true
		}
	}

	return false
}

func hasFileLanguage(nodes []Node, path string, language string) bool {
	for _, node := range nodes {
		if node.ID == "file://"+path && node.Kind == "file" && node.Language == language {
			return true
		}
	}

	return false
}

func hasNodeSummary(nodes []NodeSummary, id string) bool {
	for _, node := range nodes {
		if node.ID == id {
			return true
		}
	}

	return false
}

func hasSummaryResolution(nodes []NodeSummary, resolution string) bool {
	for _, node := range nodes {
		if node.Kind != "summary" {
			continue
		}

		if got, _ := node.Properties["resolution"].(string); got == resolution {
			return true
		}
	}

	return false
}

func hasSummaryQuality(nodes []NodeSummary, quality string, modelAuthored bool) bool {
	for _, node := range nodes {
		if node.Kind != "summary" {
			continue
		}

		gotQuality, _ := node.Properties["quality_tier"].(string)
		gotModelAuthored, _ := node.Properties["model_authored"].(bool)

		if gotQuality == quality && gotModelAuthored == modelAuthored {
			return true
		}
	}

	return false
}

func hasGapContaining(gaps []string, text string) bool {
	for _, gap := range gaps {
		if strings.Contains(gap, text) {
			return true
		}
	}

	return false
}

func hasEdge(edges []Edge, kind string, sourceID string, targetID string) bool {
	for _, edge := range edges {
		if edge.Kind == kind && edge.SourceID == sourceID && edge.TargetID == targetID {
			return true
		}
	}

	return false
}

func hasEdgeSummary(edges []EdgeSummary, kind string, sourceID string, targetID string) bool {
	for _, edge := range edges {
		if edge.Kind == kind && edge.SourceID == sourceID && edge.TargetID == targetID {
			return true
		}
	}

	return false
}

func hasString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}

	return false
}

func hasCollectorReport(reports []CollectorReport, name string, language string) bool {
	for _, report := range reports {
		if report.Name != name {
			continue
		}

		if language == "" || hasString(report.Languages, language) {
			return true
		}
	}

	return false
}

type fakeGraphEnricher struct{}

func (fakeGraphEnricher) EnrichGraph(_ context.Context, graph Graph, _ EnrichmentOptions) (GraphEnrichment, error) {
	enrichment := GraphEnrichment{
		Reports: []CollectorReport{{
			Name:         "fake_lsp_enricher",
			Kind:         "test_enricher",
			Languages:    []string{"go"},
			Active:       true,
			Available:    true,
			Tools:        []string{"go_references"},
			Capabilities: []string{"references"},
		}},
		Nodes: []Node{{
			ID:            "evidence://fake/run",
			Kind:          "tool_evidence",
			Path:          "main.go",
			Language:      "go",
			QualifiedName: "references",
			Collector:     "fake_lsp_enricher",
			Confidence:    "lsp",
		}},
		Edges: []Edge{{
			SourceID:   "symbol://main.go#Run",
			TargetID:   "evidence://fake/run",
			Kind:       "enriched_by",
			Collector:  "fake_lsp_enricher",
			Confidence: "lsp",
		}},
	}
	if graph.ID == "" {
		return enrichment, nil
	}

	return enrichment, nil
}

func hasPackFile(files []PackFile, path string, role string) bool {
	for _, file := range files {
		if file.Path == path && file.Role == role {
			return true
		}
	}

	return false
}

func slicesContainsRankerReason(reasons []RankerReason, reason string) bool {
	for _, got := range reasons {
		if got.Reason == reason {
			return true
		}
	}

	return false
}

func hasStalePackFile(files []PackFile, path string) bool {
	for _, file := range files {
		if file.Path == path && file.Stale {
			return true
		}
	}

	return false
}

func hasPackSymbol(symbols []PackSymbol, id string) bool {
	for _, symbol := range symbols {
		if symbol.ID == id {
			return true
		}
	}

	return false
}

func hasPackTestCommand(tests []PackTest, command string) bool {
	for _, test := range tests {
		if test.Command == command {
			return true
		}
	}

	return false
}

func hasPackEvidence(evidence []PackEvidence, kind string, path string) bool {
	for _, ev := range evidence {
		if ev.Kind == kind && ev.Path == path {
			return true
		}
	}

	return false
}

func hasStalePackEvidence(evidence []PackEvidence, kind string, path string) bool {
	for _, ev := range evidence {
		if ev.Kind == kind && ev.Path == path && ev.Stale {
			return true
		}
	}

	return false
}

func hasTraceAnchor(anchors []TraceAnchor, path string, line int) bool {
	for _, anchor := range anchors {
		if anchor.Path == path && anchor.Line == line {
			return true
		}
	}

	return false
}

func hasTraceCoverage(items []TraceCoverage, path string) bool {
	for _, item := range items {
		if item.Path == path {
			return true
		}
	}

	return false
}

func hasTraceFile(files []TraceFile, path string, language string) bool {
	for _, file := range files {
		if file.Path == path && file.Language == language {
			return true
		}
	}

	return false
}

func hasStorageBackend(backends []StoreBackendReport, kind string, status string, compatible bool) bool {
	for _, backend := range backends {
		if backend.Kind == kind && backend.Status == status && backend.Compatible == compatible {
			return true
		}
	}

	return false
}

func testTime() time.Time {
	return time.Date(2026, 5, 7, 1, 0, 0, 0, time.UTC)
}

func TestGraphIDContentAddressedAcrossNoOpRebuilds(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/graphid\n\ngo 1.22\n")
	writeFile(t, filepath.Join(root, "pkg", "x.go"), "package pkg\n\nfunc X() string { return \"x\" }\n")

	first, err := Build(t.Context(), BuildOptions{Root: root, Language: "go"})
	if err != nil {
		t.Fatalf("first Build: %v", err)
	}

	// Sleep briefly so GeneratedAt would differ if it were still in the
	// hash. The point of the test is that it isn't.
	time.Sleep(2 * time.Millisecond)

	second, err := Build(t.Context(), BuildOptions{Root: root, Language: "go"})
	if err != nil {
		t.Fatalf("second Build: %v", err)
	}

	if first.IndexID != second.IndexID {
		t.Fatalf("graphID must be content-addressed; got %q vs %q across no-op rebuilds", first.IndexID, second.IndexID)
	}

	// Editing a file must change the index_id.
	writeFile(t, filepath.Join(root, "pkg", "x.go"), "package pkg\n\nfunc X() string { return \"changed\" }\n")

	third, err := Build(t.Context(), BuildOptions{Root: root, Language: "go"})
	if err != nil {
		t.Fatalf("third Build: %v", err)
	}

	if third.IndexID == first.IndexID {
		t.Fatalf("graphID must change when file content changes; got %q identical across edit", third.IndexID)
	}
}

func TestGraphIDChangesWhenCollectorVersionChanges(t *testing.T) {
	graph := Graph{
		Root:              "/repo",
		GitHead:           "abc123",
		CollectorVersion:  "collector-v1",
		CollectorVersions: map[string]string{"context_index": "collector-v1", "go_index": "collector-v1"},
		Nodes: []Node{{
			ID:          "file://pkg/x.go",
			Kind:        "file",
			Path:        "pkg/x.go",
			Fingerprint: "source-fingerprint",
		}},
		Edges: []Edge{{ID: "edge://one"}},
	}
	first := graphID(graph)

	graph.CollectorVersion = "collector-v2"
	graph.CollectorVersions = map[string]string{"context_index": "collector-v2", "go_index": "collector-v2"}

	second := graphID(graph)

	if first == second {
		t.Fatalf("graphID must change when collector versions change; got %q for both", first)
	}
}
