package comprehension

import (
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	semanticcpp "idiom/internal/repository/semantic/cpp"
)

var _ = It("build query zoom and status", func() {
	root := GinkgoT().TempDir()
	writeFile(filepath.Join(root, "go.mod"), "module example.com/demo\n\ngo 1.22\n")
	writeFile(filepath.Join(root, "service", "service.go"), `package service

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
	writeFile(filepath.Join(root, "service", "service_test.go"), `package service

import "testing"

func TestRun(t *testing.T) {
	if Run() == "" {
		t.Fatal("empty")
	}
}
`)
	writeFile(filepath.Join(root, "client", "client.go"), `package client

import "example.com/demo/service"

func CallRun() string {
	return service.Run()
}
`)
	writeFile(filepath.Join(root, "other", "other.go"), `package other

func format(value string) string {
	return value
}
`)

	report, err := Build(context.Background(), BuildOptions{Root: root, Language: "go"})
	Expect(err).NotTo(HaveOccurred(), "Build: %v", err)
	Expect(report.FilesIndexed).To(BeEquivalentTo(4), "unexpected build report: %#v", report)
	Expect(report.SymbolsIndexed).NotTo(BeEquivalentTo(0), "unexpected build report: %#v", report)
	Expect(report.EdgesIndexed).NotTo(BeEquivalentTo(0), "unexpected build report: %#v", report)
	Expect(report.CollectorVersion).NotTo(BeEquivalentTo(""), "expected build report to include collector version metadata, got %#v", report)
	Expect(report.CollectorVersions["go_index"]).NotTo(BeEquivalentTo(""), "expected build report to include collector version metadata, got %#v", report)

	graph, err := LoadGraph(root)
	Expect(err).NotTo(HaveOccurred(), "LoadGraph: %v", err)
	Expect(hasNode(graph.Nodes, "external://fmt")).To(BeTrue(), "expected import relationship for fmt in graph: nodes=%#v edges=%#v", graph.Nodes, graph.Edges)
	Expect(hasEdge(graph.Edges, "imports", "file://service/service.go", "external://fmt")).To(BeTrue(), "expected import relationship for fmt in graph: nodes=%#v edges=%#v", graph.Nodes, graph.Edges)
	Expect(hasEdge(graph.Edges, "tests", "symbol://service/service_test.go#TestRun", "package://service")).To(BeTrue(), "expected TestRun to map to service package test relationship: %#v", graph.Edges)
	Expect(hasEdge(graph.Edges, "tests", "symbol://service/service_test.go#TestRun", "symbol://service/service.go#Run")).To(BeTrue(), "expected TestRun to map to Run symbol relationship: %#v", graph.Edges)
	Expect(hasNode(graph.Nodes, "external_symbol://go/fmt.Sprintf")).To(BeTrue(), "expected fmt.Sprintf to resolve as an external call, nodes=%#v edges=%#v", graph.Nodes, graph.Edges)
	Expect(hasEdge(graph.Edges, "calls", "symbol://service/service.go#format", "external_symbol://go/fmt.Sprintf")).To(BeTrue(), "expected fmt.Sprintf to resolve as an external call, nodes=%#v edges=%#v", graph.Nodes, graph.Edges)
	Expect(hasEdge(graph.Edges, "calls", "symbol://service/service.go#format", "symbol://service/service.go#Sprintf")).To(BeFalse(), "qualified fmt.Sprintf call must not resolve to local Sprintf symbol: %#v", graph.Edges)
	Expect(hasEdge(graph.Edges, "calls", "symbol://service/service.go#Run", "symbol://service/service.go#format")).To(BeTrue(), "expected unqualified Go call to resolve within its own package, got %#v", graph.Edges)
	Expect(hasEdge(graph.Edges, "calls", "symbol://service/service.go#Run", "symbol://other/other.go#format")).To(BeFalse(), "unqualified Go call must not resolve across packages, got %#v", graph.Edges)
	Expect(hasEdge(graph.Edges, "calls", "symbol://client/client.go#CallRun", "symbol://service/service.go#Run")).To(BeTrue(), "expected local module import call to resolve to indexed service.Run symbol, got %#v", graph.Edges)

	status, err := Status(root)
	Expect(err).NotTo(HaveOccurred(), "Status: %v", err)
	Expect(status.Indexed).To(BeTrue(), "unexpected fresh status: %#v", status)
	Expect(status.Fresh).To(BeTrue(), "unexpected fresh status: %#v", status)
	Expect(status.StaleSymbols).To(BeEquivalentTo(0), "unexpected fresh status: %#v", status)
	Expect(status.CollectorVersion).NotTo(BeEquivalentTo(""), "expected fresh collector-version status, got %#v", status)
	Expect(status.CurrentCollectorVersion).NotTo(BeEquivalentTo(""), "expected fresh collector-version status, got %#v", status)
	Expect(status.CollectorVersionMismatch).To(BeFalse(), "expected fresh collector-version status, got %#v", status)
	Expect(report.SymbolsIndexed).To(BeEquivalentTo(status.SymbolsIndexed), "build and status should report the same graph symbol count, build=%d status=%d", report.SymbolsIndexed, status.SymbolsIndexed)

	query, err := Query(QueryOptions{Root: root, Symbol: "Run", Resolution: "symbol", IncludeEvidence: true})
	Expect(err).NotTo(HaveOccurred(), "Query: %v", err)
	Expect(len(query.Nodes)).NotTo(BeEquivalentTo(0), "expected query nodes, got %#v", query)

	var runHandle string

	for _, node := range query.Nodes {
		if strings.Contains(node.QualifiedName, "Run") && node.Kind == "func" {
			runHandle = node.ID
			break
		}
	}
	Expect(runHandle).NotTo(BeEquivalentTo(""), "expected Run handle in query: %#v", query.Nodes)

	for _, node := range query.Nodes {
		Expect(node.Confidence).NotTo(BeEquivalentTo(""), "query results must include confidence tiers: %#v", query.Nodes)
		{
			_, ok := node.Properties["match_score"]
			Expect(ok).To(BeFalse(), "query result properties must not expose internal ranking scores: %#v", node)
		}
	}

	zoom, err := Zoom(ZoomOptions{Root: root, Handle: runHandle})
	Expect(err).NotTo(HaveOccurred(), "Zoom: %v", err)
	Expect(zoom.Node).NotTo(BeNil(), "unexpected zoom result: %#v", zoom)
	Expect(zoom.Node.ID).To(BeEquivalentTo(runHandle), "unexpected zoom result: %#v", zoom)
	Expect(len(zoom.Edges)).NotTo(BeEquivalentTo(0), "unexpected zoom result: %#v", zoom)

	pkgQuery, err := Query(QueryOptions{Root: root, Query: "what packages are indexed", Resolution: "package", IncludeEvidence: true})
	Expect(err).NotTo(HaveOccurred(), "package Query: %v", err)
	Expect(hasNodeSummary(pkgQuery.Nodes, "package://service")).To(BeTrue(), "expected package-level query with relationship evidence, got %#v", pkgQuery)
	Expect(len(pkgQuery.Edges)).NotTo(BeEquivalentTo(0), "expected package-level query with relationship evidence, got %#v", pkgQuery)

	summaryQuery, err := Query(QueryOptions{Root: root, Query: "repository summary", Resolution: "summary", IncludeEvidence: true})
	Expect(err).NotTo(HaveOccurred(), "summary Query: %v", err)
	Expect(hasSummaryResolution(summaryQuery.Nodes, "repository")).To(BeTrue(), "expected durable summary node with evidence, got %#v", summaryQuery)
	Expect(len(summaryQuery.Edges)).NotTo(BeEquivalentTo(0), "expected durable summary node with evidence, got %#v", summaryQuery)
	Expect(hasSummaryQuality(summaryQuery.Nodes, "deterministic", false)).To(BeTrue(), "expected deterministic summary quality metadata, got %#v", summaryQuery.Nodes)
})
var _ = It("clean scope rejects traversal and normalizes backslashes", func() {
	{
		got := cleanScope(`pkg\file.go`)
		Expect(got).To(BeEquivalentTo("pkg/file.go"), "cleanScope(backslash path) = %q, want pkg/file.go", got)
	}

	for _, path := range []string{"../outside.go", "/tmp/outside.go", `C:\tmp\outside.go`} {
		{
			got := cleanScope(path)
			Expect(got).To(BeEquivalentTo("."), "cleanScope(%q) = %q, want root scope", path, got)
		}
	}
	Expect(pathInScope(`pkg\file.go`, "pkg")).To(BeTrue(), "pathInScope should normalize backslash paths")
	Expect(pathInScope("../outside.go", "pkg")).To(BeFalse(), "pathInScope should reject traversal paths")
})
var _ = It("status reports collector version mismatch", func() {
	root := GinkgoT().TempDir()
	writeFile(filepath.Join(root, "go.mod"), "module example.com/collector\n\ngo 1.22\n")
	writeFile(filepath.Join(root, "pkg", "pkg.go"), "package pkg\n\nfunc Run() string { return \"ok\" }\n")
	{
		_, err := Build(context.Background(), BuildOptions{Root: root, Language: "go"})
		Expect(err).NotTo(HaveOccurred(), "Build: %v", err)
	}

	db, err := openSQLite(sqliteGraphPath(root))
	Expect(err).NotTo(HaveOccurred(), "open sqlite: %v", err)

	_, err = db.Exec(`UPDATE graph_metadata SET value = ? WHERE key = 'collector_version'`, "stale-version")
	if err != nil {
		_ = db.Close()
	}
	Expect(err).NotTo(HaveOccurred(), "update collector version: %v", err)
	{
		err := db.Close()
		Expect(err).NotTo(HaveOccurred(), "close sqlite: %v", err)
	}

	status, err := Status(root)
	Expect(err).NotTo(HaveOccurred(), "Status: %v", err)
	Expect(status.Fresh).To(BeFalse(), "expected collector version mismatch to mark graph stale, got %#v", status)
	Expect(status.CollectorVersionMismatch).To(BeTrue(), "expected collector version mismatch to mark graph stale, got %#v", status)
	Expect(status.CollectorVersion).To(BeEquivalentTo("stale-version"), "expected collector version mismatch to mark graph stale, got %#v", status)
	Expect(status.CurrentCollectorVersion).NotTo(BeEquivalentTo(""), "expected collector version mismatch to mark graph stale, got %#v", status)

	query, err := Query(QueryOptions{Root: root, Symbol: "Run", Resolution: "symbol"})
	Expect(err).NotTo(HaveOccurred(), "Query: %v", err)
	Expect(hasGapContaining(query.UnresolvedGaps, "reason=collector_version_mismatch")).To(BeTrue(), "expected collector-version gap, got %#v", query.UnresolvedGaps)

	report, err := Build(context.Background(), BuildOptions{Root: root, Language: "go"})
	Expect(err).NotTo(HaveOccurred(), "refresh Build: %v", err)
	Expect(report.CollectorVersion).NotTo(BeEquivalentTo("stale-version"), "expected refreshed collector version, got %#v", report)
	Expect(report.CollectorVersion).NotTo(BeEquivalentTo(""), "expected refreshed collector version, got %#v", report)

	status, err = Status(root)
	Expect(err).NotTo(HaveOccurred(), "Status after refresh: %v", err)
	Expect(status.Fresh).To(BeTrue(), "expected refreshed collector version status, got %#v", status)
	Expect(status.CollectorVersionMismatch).To(BeFalse(), "expected refreshed collector version status, got %#v", status)
})
var _ = It("status reports stale symbols and build refreshes them", func() {
	root := GinkgoT().TempDir()
	writeFile(filepath.Join(root, "go.mod"), "module example.com/stale\n\ngo 1.22\n")

	sourcePath := filepath.Join(root, "pkg", "pkg.go")
	writeFile(sourcePath, "package pkg\n\nfunc OldName() string { return \"old\" }\n")
	{
		_, err := Build(context.Background(), BuildOptions{Root: root, Language: "go"})
		Expect(err).NotTo(HaveOccurred(), "Build: %v", err)
	}

	writeFile(sourcePath, "package pkg\n\nfunc NewName() string { return \"new\" }\n")

	status, err := Status(root)
	Expect(err).NotTo(HaveOccurred(), "Status: %v", err)
	Expect(len(status.StaleFiles)).To(BeEquivalentTo(1), "expected stale file and symbols, got %#v", status)
	Expect(status.StaleFiles[0]).To(BeEquivalentTo("pkg/pkg.go"), "expected stale file and symbols, got %#v", status)
	Expect(status.StaleSymbols).NotTo(BeEquivalentTo(0), "expected stale file and symbols, got %#v", status)

	report, err := Build(context.Background(), BuildOptions{Root: root, Language: "go"})
	Expect(err).NotTo(HaveOccurred(), "refresh Build: %v", err)
	Expect(len(report.RefreshedStaleFiles)).To(BeEquivalentTo(1), "expected refreshed stale file, got %#v", report.RefreshedStaleFiles)
	Expect(report.RefreshedStaleFiles[0]).To(BeEquivalentTo("pkg/pkg.go"), "expected refreshed stale file, got %#v", report.RefreshedStaleFiles)

	query, err := Query(QueryOptions{Root: root, Symbol: "NewName", Resolution: "symbol"})
	Expect(err).NotTo(HaveOccurred(), "Query: %v", err)
	Expect(len(query.Nodes)).To(BeEquivalentTo(1), "expected refreshed NewName symbol, got %#v", query.Nodes)
	Expect(query.Nodes[0].QualifiedName).To(BeEquivalentTo("NewName"), "expected refreshed NewName symbol, got %#v", query.Nodes)
})
var _ = It("path scoped build merges existing graph", func() {
	root := GinkgoT().TempDir()
	writeFile(filepath.Join(root, "go.mod"), "module example.com/incremental\n\ngo 1.22\n")

	sourcePath := filepath.Join(root, "pkg", "a.go")
	writeFile(sourcePath, "package pkg\n\nfunc OldName() string { return \"old\" }\n")
	writeFile(filepath.Join(root, "pkg", "b.go"), "package pkg\n\nfunc KeepName() string { return OldName() }\n")
	writeFile(filepath.Join(root, "cmd", "main.go"), "package main\n\nfunc main() {}\n")
	{
		_, err := Build(context.Background(), BuildOptions{Root: root, Language: "go"})
		Expect(err).NotTo(HaveOccurred(), "Build: %v", err)
	}

	writeFile(sourcePath, "package pkg\n\nfunc NewName() string { return \"new\" }\n")

	report, err := Build(context.Background(), BuildOptions{Root: root, Path: "pkg/a.go", Language: "go"})
	Expect(err).NotTo(HaveOccurred(), "incremental Build: %v", err)
	Expect(report.Incremental).To(BeTrue(), "expected path-scoped incremental merge, got %#v", report)
	Expect(report.ReusedIndexedFiles).NotTo(BeEquivalentTo(0), "expected path-scoped incremental merge, got %#v", report)
	Expect(report.ExpandedScope).To(BeEquivalentTo("pkg"), "expected dependency-aware refresh to include caller file, got %#v", report)
	Expect(hasString(report.DependencyRefreshFiles, "pkg/b.go")).To(BeTrue(), "expected dependency-aware refresh to include caller file, got %#v", report)
	Expect(len(report.RefreshedStaleFiles)).To(BeEquivalentTo(1), "expected scoped stale refresh accounting, got %#v", report)
	Expect(report.RefreshedStaleFiles[0]).To(BeEquivalentTo("pkg/a.go"), "expected scoped stale refresh accounting, got %#v", report)
	Expect(report.RefreshedStaleSymbols).NotTo(BeEquivalentTo(0), "expected scoped stale refresh accounting, got %#v", report)

	graph, err := LoadGraph(root)
	Expect(err).NotTo(HaveOccurred(), "LoadGraph: %v", err)
	Expect(hasNode(graph.Nodes, "symbol://pkg/a.go#NewName")).To(BeTrue(), "expected refreshed scoped file and retained out-of-scope file, got nodes=%#v", graph.Nodes)
	Expect(hasNode(graph.Nodes, "symbol://pkg/a.go#OldName")).To(BeFalse(), "expected refreshed scoped file and retained out-of-scope file, got nodes=%#v", graph.Nodes)
	Expect(hasNode(graph.Nodes, "symbol://pkg/b.go#KeepName")).To(BeTrue(), "expected refreshed scoped file and retained out-of-scope file, got nodes=%#v", graph.Nodes)
	Expect(hasNode(graph.Nodes, "symbol://cmd/main.go#main")).To(BeTrue(), "expected refreshed scoped file and retained out-of-scope file, got nodes=%#v", graph.Nodes)

	status, err := Status(root)
	Expect(err).NotTo(HaveOccurred(), "Status: %v", err)
	Expect(status.Fresh).To(BeTrue(), "expected fresh merged graph with both files, got %#v", status)
	Expect(status.FilesIndexed).To(BeEquivalentTo(3), "expected fresh merged graph with both files, got %#v", status)
})
var _ = It("path scoped build exact scope skips dependency expansion", func() {
	root := GinkgoT().TempDir()
	writeFile(filepath.Join(root, "go.mod"), "module example.com/exactscope\n\ngo 1.22\n")

	sourcePath := filepath.Join(root, "pkg", "a.go")
	writeFile(sourcePath, "package pkg\n\nfunc OldName() string { return \"old\" }\n")
	writeFile(filepath.Join(root, "pkg", "b.go"), "package pkg\n\nfunc KeepName() string { return OldName() }\n")
	{
		_, err := Build(context.Background(), BuildOptions{Root: root, Language: "go"})
		Expect(err).NotTo(HaveOccurred(), "Build: %v", err)
	}

	writeFile(sourcePath, "package pkg\n\nfunc NewName() string { return \"new\" }\n")

	report, err := Build(context.Background(), BuildOptions{Root: root, Path: "pkg/a.go", Language: "go", ExactScope: true})
	Expect(err).NotTo(HaveOccurred(), "exact-scope Build: %v", err)
	Expect(report.Incremental).To(BeTrue(), "expected exact scoped refresh without dependency expansion, got %#v", report)
	Expect(report.ExpandedScope).To(BeEquivalentTo(""), "expected exact scoped refresh without dependency expansion, got %#v", report)
	Expect(len(report.DependencyRefreshFiles)).To(BeEquivalentTo(0), "expected exact scoped refresh without dependency expansion, got %#v", report)
	Expect(report.PartialRefreshReason).To(BeEquivalentTo("exact_scope_refresh"), "expected exact scoped refresh without dependency expansion, got %#v", report)
	Expect(report.FilesIndexed).To(BeEquivalentTo(1), "expected exact scoped refresh to index one file, got %#v", report)

	graph, err := LoadGraph(root)
	Expect(err).NotTo(HaveOccurred(), "LoadGraph: %v", err)
	Expect(hasNode(graph.Nodes, "symbol://pkg/a.go#NewName")).To(BeTrue(), "expected refreshed target file and retained dependency graph, got nodes=%#v", graph.Nodes)
	Expect(hasNode(graph.Nodes, "symbol://pkg/a.go#OldName")).To(BeFalse(), "expected refreshed target file and retained dependency graph, got nodes=%#v", graph.Nodes)
	Expect(hasNode(graph.Nodes, "symbol://pkg/b.go#KeepName")).To(BeTrue(), "expected refreshed target file and retained dependency graph, got nodes=%#v", graph.Nodes)
})
var _ = It("patch active store preserves inbound edges and drops summaries", func() {
	root := GinkgoT().TempDir()
	writeFile(filepath.Join(root, "go.mod"), "module example.com/patchactive\n\ngo 1.22\n")

	sourcePath := filepath.Join(root, "pkg", "a.go")
	writeFile(sourcePath, "package pkg\n\nfunc Run() string { return \"old\" }\n")
	writeFile(filepath.Join(root, "pkg", "b.go"), "package pkg\n\nfunc KeepName() string { return Run() }\n")
	{
		_, err := Build(context.Background(), BuildOptions{Root: root, Language: "go"})
		Expect(err).NotTo(HaveOccurred(), "Build: %v", err)
	}

	writeFile(sourcePath, "package pkg\n\nfunc Run() string { return \"new\" }\n")

	report, err := Build(context.Background(), BuildOptions{Root: root, Path: "pkg/a.go", Language: "go", ExactScope: true, PatchActiveStore: true})
	Expect(err).NotTo(HaveOccurred(), "patch-active Build: %v", err)
	Expect(report.Incremental).To(BeTrue(), "expected exact patch-active refresh, got %#v", report)
	Expect(report.PartialRefreshReason).To(BeEquivalentTo("exact_scope_refresh"), "expected exact patch-active refresh, got %#v", report)
	Expect(report.IndexID).NotTo(BeEquivalentTo(""), "expected exact patch-active refresh, got %#v", report)

	graph, err := LoadGraph(root)
	Expect(err).NotTo(HaveOccurred(), "LoadGraph: %v", err)
	Expect(hasNode(graph.Nodes, "symbol://pkg/a.go#Run")).To(BeTrue(), "expected refreshed target and retained caller nodes, got nodes=%#v", graph.Nodes)
	Expect(hasNode(graph.Nodes, "symbol://pkg/b.go#KeepName")).To(BeTrue(), "expected refreshed target and retained caller nodes, got nodes=%#v", graph.Nodes)
	Expect(hasEdge(graph.Edges, "calls", "symbol://pkg/b.go#KeepName", "symbol://pkg/a.go#Run")).To(BeTrue(), "expected inbound caller edge to survive exact patch, got edges=%#v", graph.Edges)

	for _, node := range graph.Nodes {
		Expect(node.Kind).NotTo(BeEquivalentTo("summary"), "patch-active refresh must not leave stale summary nodes, got %#v", node)
		Expect(node.Collector).NotTo(BeEquivalentTo("summary_index"), "patch-active refresh must not leave stale summary nodes, got %#v", node)
	}

	status, err := Status(root)
	Expect(err).NotTo(HaveOccurred(), "Status: %v", err)

	for _, report := range status.CollectorReports {
		Expect(report.Name).NotTo(BeEquivalentTo("summary_index"), "patch-active refresh must not advertise deleted summary_index reports, got %#v", status.CollectorReports)
	}
})
var _ = It("patch active store expands dependencies without full rewrite", func() {
	root := GinkgoT().TempDir()
	writeFile(filepath.Join(root, "go.mod"), "module example.com/patchdeps\n\ngo 1.22\n")

	sourcePath := filepath.Join(root, "pkg", "a.go")
	writeFile(sourcePath, "package pkg\n\nfunc OldName() string { return \"old\" }\n")
	writeFile(filepath.Join(root, "pkg", "b.go"), "package pkg\n\nfunc KeepName() string { return OldName() }\n")
	writeFile(filepath.Join(root, "cmd", "main.go"), "package main\n\nfunc main() {}\n")

	initial, err := Build(context.Background(), BuildOptions{Root: root, Language: "go"})
	Expect(err).NotTo(HaveOccurred(), "Build: %v", err)

	writeFile(sourcePath, "package pkg\n\nfunc NewName() string { return \"new\" }\n")

	report, err := Build(context.Background(), BuildOptions{Root: root, Path: "pkg/a.go", Language: "go", PatchActiveStore: true})
	Expect(err).NotTo(HaveOccurred(), "patch-active dependency Build: %v", err)
	Expect(report.Incremental).To(BeTrue(), "expected dependency-aware sqlite patch, got %#v", report)
	Expect(report.IndexID).To(BeEquivalentTo(initial.IndexID), "expected dependency-aware sqlite patch, got %#v", report)
	Expect(report.ExpandedScope).To(BeEquivalentTo("pkg"), "expected dependency-aware sqlite patch, got %#v", report)
	Expect(hasString(report.DependencyRefreshFiles, "pkg/b.go")).To(BeTrue(), "expected dependency-aware sqlite patch, got %#v", report)
	Expect(report.PartialRefreshReason).To(BeEquivalentTo("sqlite_scope_patch"), "expected dependency-aware sqlite patch, got %#v", report)

	graph, err := LoadGraph(root)
	Expect(err).NotTo(HaveOccurred(), "LoadGraph: %v", err)
	Expect(hasNode(graph.Nodes, "symbol://pkg/a.go#NewName")).To(BeTrue(), "expected refreshed dependency scope and retained out-of-scope graph, got nodes=%#v", graph.Nodes)
	Expect(hasNode(graph.Nodes, "symbol://pkg/a.go#OldName")).To(BeFalse(), "expected refreshed dependency scope and retained out-of-scope graph, got nodes=%#v", graph.Nodes)
	Expect(hasNode(graph.Nodes, "symbol://pkg/b.go#KeepName")).To(BeTrue(), "expected refreshed dependency scope and retained out-of-scope graph, got nodes=%#v", graph.Nodes)
	Expect(hasNode(graph.Nodes, "symbol://cmd/main.go#main")).To(BeTrue(), "expected refreshed dependency scope and retained out-of-scope graph, got nodes=%#v", graph.Nodes)
	Expect(hasEdge(graph.Edges, "calls", "symbol://pkg/b.go#KeepName", "symbol://pkg/a.go#OldName")).To(BeFalse(), "stale dependency edge to removed symbol survived patch: %#v", graph.Edges)

	status, err := Status(root)
	Expect(err).NotTo(HaveOccurred(), "Status: %v", err)
	Expect(status.Fresh).To(BeTrue(), "expected fresh total status after dependency-aware patch, got %#v", status)
	Expect(status.FilesIndexed).To(BeEquivalentTo(3), "expected fresh total status after dependency-aware patch, got %#v", status)
})
var _ = It("patch active store removes deleted scope and orphans", func() {
	root := GinkgoT().TempDir()
	writeFile(filepath.Join(root, "go.mod"), "module example.com/patchdelete\n\ngo 1.22\n")

	sourcePath := filepath.Join(root, "pkg", "a.go")
	writeFile(sourcePath, "package pkg\n\nimport \"fmt\"\n\nfunc Run() string { return fmt.Sprint(\"old\") }\n")
	{
		_, err := Build(context.Background(), BuildOptions{Root: root, Language: "go"})
		Expect(err).NotTo(HaveOccurred(), "Build: %v", err)
	}
	{
		err := os.Remove(sourcePath)
		Expect(err).NotTo(HaveOccurred(), "remove source: %v", err)
	}

	report, err := Build(context.Background(), BuildOptions{Root: root, Path: "pkg/a.go", Language: "go", ExactScope: true, PatchActiveStore: true})
	Expect(err).NotTo(HaveOccurred(), "patch-active delete Build: %v", err)
	Expect(report.Incremental).To(BeTrue(), "expected exact delete patch, got %#v", report)
	Expect(report.FilesIndexed).To(BeEquivalentTo(0), "expected exact delete patch, got %#v", report)
	Expect(report.PartialRefreshReason).To(BeEquivalentTo("exact_scope_refresh"), "expected exact delete patch, got %#v", report)

	graph, err := LoadGraph(root)
	Expect(err).NotTo(HaveOccurred(), "LoadGraph: %v", err)

	for _, id := range []string{"file://pkg/a.go", "symbol://pkg/a.go#Run", "package://pkg", "external://fmt"} {
		Expect(hasNode(graph.Nodes, id)).To(BeFalse(), "deleted scope left stale node %s in graph: %#v", id, graph.Nodes)
	}

	for _, edge := range graph.Edges {
		Expect(edge.SourceID).NotTo(ContainSubstring("pkg/a.go"), "deleted scope left stale edge %#v", edge)
		Expect(edge.TargetID).NotTo(ContainSubstring("pkg/a.go"), "deleted scope left stale edge %#v", edge)
		Expect(edge.SourceID).NotTo(BeEquivalentTo("package://pkg"), "deleted scope left stale edge %#v", edge)
		Expect(edge.TargetID).NotTo(BeEquivalentTo("package://pkg"), "deleted scope left stale edge %#v", edge)
		Expect(edge.SourceID).NotTo(BeEquivalentTo("external://fmt"), "deleted scope left stale edge %#v", edge)
		Expect(edge.TargetID).NotTo(BeEquivalentTo("external://fmt"), "deleted scope left stale edge %#v", edge)
	}

	status, err := Status(root)
	Expect(err).NotTo(HaveOccurred(), "Status: %v", err)
	Expect(status.FilesIndexed).To(BeEquivalentTo(0), "deleted final source file should clear indexed file counts, got %#v", status)
})
var _ = It("patch active store removes deleted native language scope", func() {
	root := GinkgoT().TempDir()
	sourcePath := filepath.Join(root, "app.py")
	writeFile(sourcePath, "def run():\n    return 'old'\n")
	{
		_, err := Build(context.Background(), BuildOptions{Root: root, Language: "python"})
		Expect(err).NotTo(HaveOccurred(), "Build: %v", err)
	}
	{
		err := os.Remove(sourcePath)
		Expect(err).NotTo(HaveOccurred(), "remove source: %v", err)
	}
	{
		_, err := Build(context.Background(), BuildOptions{Root: root, Path: "app.py", Language: "python", ExactScope: true, PatchActiveStore: true})
		Expect(err).NotTo(HaveOccurred(), "patch-active python delete Build: %v", err)
	}

	graph, err := LoadGraph(root)
	Expect(err).NotTo(HaveOccurred(), "LoadGraph: %v", err)
	Expect(hasNode(graph.Nodes, "file://app.py")).To(BeFalse(), "deleted Python scope left stale file node: %#v", graph.Nodes)

	status, err := Status(root)
	Expect(err).NotTo(HaveOccurred(), "Status: %v", err)
	Expect(len(status.MissingFiles)).To(BeEquivalentTo(0), "deleted Python scope should be removed from status, got %#v", status)
	Expect(status.FilesIndexed).To(BeEquivalentTo(0), "deleted Python scope should be removed from status, got %#v", status)
})
var _ = It("path scoped build preserves graph when collector version is stale", func() {
	root := GinkgoT().TempDir()
	writeFile(filepath.Join(root, "go.mod"), "module example.com/stale-scoped\n\ngo 1.22\n")

	sourcePath := filepath.Join(root, "pkg", "a.go")
	writeFile(sourcePath, "package pkg\n\nfunc OldName() string { return \"old\" }\n")
	writeFile(filepath.Join(root, "pkg", "b.go"), "package pkg\n\nfunc KeepName() string { return OldName() }\n")
	writeFile(filepath.Join(root, "cmd", "main.go"), "package main\n\nfunc main() {}\n")
	{
		_, err := Build(context.Background(), BuildOptions{Root: root, Language: "go"})
		Expect(err).NotTo(HaveOccurred(), "Build: %v", err)
	}

	graph, err := LoadGraph(root)
	Expect(err).NotTo(HaveOccurred(), "LoadGraph: %v", err)

	graph.CollectorVersion = "old-collector-version"
	graph.CollectorVersions = map[string]string{"context_index": "old-collector-version"}
	{
		err := saveGraph(root, graph)
		Expect(err).NotTo(HaveOccurred(), "saveGraph: %v", err)
	}
	{
		err := saveSQLiteGraph(root, graph)
		Expect(err).NotTo(HaveOccurred(), "saveSQLiteGraph: %v", err)
	}

	status, err := Status(root)
	Expect(err).NotTo(HaveOccurred(), "Status: %v", err)
	Expect(status.CollectorVersionMismatch).To(BeTrue(), "expected collector version mismatch before scoped build, got %#v", status)

	writeFile(sourcePath, "package pkg\n\nfunc NewName() string { return \"new\" }\n")

	report, err := Build(context.Background(), BuildOptions{Root: root, Path: "pkg/a.go", Language: "go"})
	Expect(err).NotTo(HaveOccurred(), "scoped Build: %v", err)
	Expect(report.Incremental).To(BeTrue(), "expected scoped refresh to merge existing graph, got %#v", report)
	Expect(report.ReusedIndexedFiles).NotTo(BeEquivalentTo(0), "expected scoped refresh to merge existing graph, got %#v", report)
	Expect(report.PartialRefreshReason).To(BeEquivalentTo("collector_version_mismatch_preserved_for_scoped_refresh"), "expected collector mismatch partial refresh reason, got %#v", report)
	Expect(report.CollectorVersion).To(BeEquivalentTo("old-collector-version"), "expected scoped refresh to preserve stale collector version, got %#v", report)

	graph, err = LoadGraph(root)
	Expect(err).NotTo(HaveOccurred(), "LoadGraph after scoped build: %v", err)
	Expect(hasNode(graph.Nodes, "symbol://pkg/a.go#NewName")).To(BeTrue(), "expected refreshed scoped file and retained out-of-scope graph, got nodes=%#v", graph.Nodes)
	Expect(hasNode(graph.Nodes, "symbol://pkg/a.go#OldName")).To(BeFalse(), "expected refreshed scoped file and retained out-of-scope graph, got nodes=%#v", graph.Nodes)
	Expect(hasNode(graph.Nodes, "symbol://cmd/main.go#main")).To(BeTrue(), "expected refreshed scoped file and retained out-of-scope graph, got nodes=%#v", graph.Nodes)

	status, err = Status(root)
	Expect(err).NotTo(HaveOccurred(), "Status after scoped build: %v", err)
	Expect(status.CollectorVersionMismatch).To(BeTrue(), "expected stale collector warning with complete merged graph, got %#v", status)
	Expect(status.FilesIndexed).To(BeEquivalentTo(3), "expected stale collector warning with complete merged graph, got %#v", status)
})
var _ = It("concurrent scoped builds preserve all refreshes", func() {
	root := GinkgoT().TempDir()
	writeFile(filepath.Join(root, "go.mod"), "module example.com/concurrent-refresh\n\ngo 1.22\n")
	writeFile(filepath.Join(root, "pkg", "a.go"), "package pkg\n\nfunc OldA() string { return \"a\" }\n")
	writeFile(filepath.Join(root, "pkg", "b.go"), "package pkg\n\nfunc OldB() string { return \"b\" }\n")
	writeFile(filepath.Join(root, "pkg", "c.go"), "package pkg\n\nfunc KeepC() string { return \"c\" }\n")
	{
		_, err := Build(context.Background(), BuildOptions{Root: root, Language: "go"})
		Expect(err).NotTo(HaveOccurred(), "Build: %v", err)
	}

	writeFile(filepath.Join(root, "pkg", "a.go"), "package pkg\n\nfunc NewA() string { return \"a2\" }\n")
	writeFile(filepath.Join(root, "pkg", "b.go"), "package pkg\n\nfunc NewB() string { return \"b2\" }\n")

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
		Expect(err).NotTo(HaveOccurred(), "concurrent scoped Build: %v", err)
	}

	graph, err := LoadGraph(root)
	Expect(err).NotTo(HaveOccurred(), "LoadGraph: %v", err)

	for _, want := range []string{"symbol://pkg/a.go#NewA", "symbol://pkg/b.go#NewB", "symbol://pkg/c.go#KeepC"} {
		Expect(hasNode(graph.Nodes, want)).To(BeTrue(), "expected graph to retain %s after concurrent refreshes, got nodes=%#v", want, graph.Nodes)
	}

	for _, old := range []string{"symbol://pkg/a.go#OldA", "symbol://pkg/b.go#OldB"} {
		Expect(hasNode(graph.Nodes, old)).To(BeFalse(), "expected old stale symbol %s to be gone, got nodes=%#v", old, graph.Nodes)
	}

	status, err := Status(root)
	Expect(err).NotTo(HaveOccurred(), "Status: %v", err)
	Expect(status.Fresh).To(BeTrue(), "expected fresh graph after serialized concurrent scoped refreshes, got %#v", status)
	Expect(len(status.StaleFiles)).To(BeEquivalentTo(0), "expected fresh graph after serialized concurrent scoped refreshes, got %#v", status)
	Expect(status.FilesIndexed).To(BeEquivalentTo(3), "expected fresh graph after serialized concurrent scoped refreshes, got %#v", status)
})
var _ = It("graph build lock respects context while held", func() {
	root := GinkgoT().TempDir()

	unlock, err := lockGraphBuild(context.Background(), root)
	Expect(err).NotTo(HaveOccurred(), "lockGraphBuild: %v", err)

	defer unlock()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	{
		_, err := lockGraphBuild(ctx, root)
		Expect(err).To(HaveOccurred(), "expected canceled context while waiting for graph build lock")
	}
})
var _ = It("C++ call target resolver matches scoped preference rules", func() {
	index := &semanticcpp.CppIndex{Files: []semanticcpp.CppFile{
		{
			Path: "src/a.cpp",
			Symbols: []semanticcpp.CppSymbol{
				{Name: "Run", Kind: "func", HasBody: false},
				{Name: "Run", Qualified: "Worker::Run", Scope: "Worker", Kind: "method", HasBody: true},
			},
		},
		{
			Path: "other/b.cpp",
			Symbols: []semanticcpp.CppSymbol{
				{Name: "Run", Kind: "func", HasBody: true},
			},
		},
	}}

	resolver := newCppCallTargetResolver(index, "src")
	{
		got, want := resolver.targetID(semanticcpp.CppCall{Name: "Run"}), "symbol://src/a.cpp#Worker::Run"
		Expect(got).To(BeEquivalentTo(want), "expected scoped method body target %q, got %q", want, got)
	}
	{
		got, want := resolver.targetID(semanticcpp.CppCall{Name: "Worker::Run"}), "symbol://src/a.cpp#Worker::Run"
		Expect(got).To(BeEquivalentTo(want), "expected qualified target %q, got %q", want, got)
	}
	{
		got := resolver.targetID(semanticcpp.CppCall{Name: "Missing"})
		Expect(got).To(BeEquivalentTo(""), "expected missing call target to stay unresolved, got %q", got)
	}
})
var _ = It("build records language environment enrichment", func() {
	root := GinkgoT().TempDir()
	writeFile(filepath.Join(root, "pyproject.toml"), "[project]\nname = \"demo\"\n")
	writeFile(filepath.Join(root, "app.py"), "def run():\n    return 'ok'\n")

	report, err := Build(context.Background(), BuildOptions{Root: root, Language: "python"})
	Expect(err).NotTo(HaveOccurred(), "Build: %v", err)
	Expect(hasCollectorReport(report.CollectorReports, "python_lsp", "python")).To(BeTrue(), "expected Python LSP availability report, got %#v", report.CollectorReports)

	graph, err := LoadGraph(root)
	Expect(err).NotTo(HaveOccurred(), "LoadGraph: %v", err)
	Expect(hasNodeKind(graph.Nodes, "toolchain://python/python_lsp", "toolchain_backend")).To(BeTrue(), "expected Python toolchain backend node, got %#v", graph.Nodes)
	Expect(hasNode(graph.Nodes, "manifest://pyproject.toml")).To(BeTrue(), "expected pyproject manifest enrichment, nodes=%#v edges=%#v", graph.Nodes, graph.Edges)
	Expect(hasEdge(graph.Edges, "configures", "repo://"+rootFingerprint(root), "manifest://pyproject.toml")).To(BeTrue(), "expected pyproject manifest enrichment, nodes=%#v edges=%#v", graph.Nodes, graph.Edges)

	status, err := Status(root)
	Expect(err).NotTo(HaveOccurred(), "Status: %v", err)
	Expect(hasCollectorReport(status.CollectorReports, "python_lsp", "python")).To(BeTrue(), "expected status to load collector reports from SQLite, got %#v", status.CollectorReports)

	query, err := Query(QueryOptions{Root: root, Query: "python_lsp", Resolution: "any"})
	Expect(err).NotTo(HaveOccurred(), "Query toolchain: %v", err)
	Expect(len(query.Nodes)).NotTo(BeEquivalentTo(0), "expected toolchain backend to be queryable, got %#v", query)
})
var _ = It("build applies external graph enricher", func() {
	root := GinkgoT().TempDir()
	writeFile(filepath.Join(root, "go.mod"), "module example.com/enriched\n\ngo 1.22\n")
	writeFile(filepath.Join(root, "main.go"), "package main\n\nfunc Run() {}\n")

	controller := gomock.NewController(GinkgoT())
	enricher := NewMockGraphEnricher(controller)
	enricher.EXPECT().
		EnrichGraph(
			gomock.Not(gomock.Nil()),
			gomock.Cond(func(graph Graph) bool {
				return graph.Root == root && hasNode(graph.Nodes, "symbol://main.go#Run")
			}),
			gomock.Cond(func(options EnrichmentOptions) bool {
				return options.Root == root && options.Scope == "." && options.Language == "go"
			}),
		).
		Return(GraphEnrichment{
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
		}, nil).
		Times(1)

	report, err := Build(context.Background(), BuildOptions{
		Root:      root,
		Language:  "go",
		Enrichers: []GraphEnricher{enricher},
	})
	Expect(err).NotTo(HaveOccurred(), "Build: %v", err)
	Expect(hasCollectorReport(report.CollectorReports, "fake_lsp_enricher", "go")).To(BeTrue(), "expected fake enricher report, got %#v", report.CollectorReports)

	graph, err := LoadGraph(root)
	Expect(err).NotTo(HaveOccurred(), "LoadGraph: %v", err)
	Expect(hasNode(graph.Nodes, "evidence://fake/run")).To(BeTrue(), "expected fake enrichment node and edge, nodes=%#v edges=%#v", graph.Nodes, graph.Edges)
	Expect(hasEdge(graph.Edges, "enriched_by", "symbol://main.go#Run", "evidence://fake/run")).To(BeTrue(), "expected fake enrichment node and edge, nodes=%#v edges=%#v", graph.Nodes, graph.Edges)
})
var _ = It("storage version status reports SQLite compatibility", func() {
	root := GinkgoT().TempDir()
	writeFile(filepath.Join(root, "go.mod"), "module example.com/storage\n\ngo 1.22\n")
	writeFile(filepath.Join(root, "pkg", "pkg.go"), "package pkg\n\nfunc Run() string { return \"ok\" }\n")

	report, err := Build(context.Background(), BuildOptions{Root: root, Language: "go"})
	Expect(err).NotTo(HaveOccurred(), "Build: %v", err)
	Expect(report.StoreKind).To(BeEquivalentTo(sqliteStoreKind), "expected active SQLite artifact, got %#v", report)
	Expect(report.ArtifactPath).To(HaveSuffix("graph.sqlite"), "expected active SQLite artifact, got %#v", report)
	Expect(hasStorageBackend(report.StorageBackends, sqliteStoreKind, "active", true)).To(BeTrue(), "expected active SQLite store and compatible JSON export, got %#v", report.StorageBackends)
	Expect(hasStorageBackend(report.StorageBackends, storeKind, "export", true)).To(BeTrue(), "expected active SQLite store and compatible JSON export, got %#v", report.StorageBackends)

	status, err := Status(root)
	Expect(err).NotTo(HaveOccurred(), "Status: %v", err)
	Expect(status.StoreKind).To(BeEquivalentTo(sqliteStoreKind), "expected storage backend status in context_status, got %#v", status.StorageBackends)
	Expect(hasStorageBackend(status.StorageBackends, sqliteStoreKind, "active", true)).To(BeTrue(), "expected storage backend status in context_status, got %#v", status.StorageBackends)
	Expect(hasStorageBackend(status.StorageBackends, storeKind, "export", true)).To(BeTrue(), "expected storage backend status in context_status, got %#v", status.StorageBackends)

	writeFile(filepath.Join(root, ".idiom", "comprehension", "graph.sqlite"), "SQLite format 3\x00placeholder")
	{
		err := writeSQLiteMetadata(root, SQLiteMetadata{StoreKind: sqliteStoreKind, SchemaVersion: sqliteGraphSchemaVersion + 1})
		Expect(err).NotTo(HaveOccurred(), "write sqlite metadata: %v", err)
	}

	status, err = Status(root)
	Expect(err).NotTo(HaveOccurred(), "Status with sqlite metadata: %v", err)
	Expect(hasStorageBackend(status.StorageBackends, sqliteStoreKind, "incompatible", false)).To(BeTrue(), "expected newer SQLite schema to be reported incompatible, got %#v", status.StorageBackends)
	{
		_, err := Query(QueryOptions{Root: root, Symbol: "Run", Resolution: "symbol"})
		Expect(err).To(HaveOccurred(), "expected context_query to reject incompatible active SQLite instead of silently using JSON export")
	}
})
var _ = It("build replaces invalid SQLite store", func() {
	root := GinkgoT().TempDir()
	writeFile(filepath.Join(root, "go.mod"), "module example.com/recover\n\ngo 1.22\n")
	writeFile(filepath.Join(root, "pkg", "pkg.go"), "package pkg\n\nfunc Run() string { return \"ok\" }\n")
	{
		_, err := Build(context.Background(), BuildOptions{Root: root, Language: "go"})
		Expect(err).NotTo(HaveOccurred(), "initial Build: %v", err)
	}

	writeFile(filepath.Join(root, ".idiom", "comprehension", "graph.sqlite"), "SQLite format 3\x00placeholder")
	{
		err := writeSQLiteMetadata(root, SQLiteMetadata{StoreKind: sqliteStoreKind, SchemaVersion: sqliteGraphSchemaVersion + 1})
		Expect(err).NotTo(HaveOccurred(), "write incompatible sqlite metadata: %v", err)
	}

	report, err := Build(context.Background(), BuildOptions{Root: root, Language: "go"})
	Expect(err).NotTo(HaveOccurred(), "refresh Build should replace incompatible SQLite: %v", err)
	Expect(report.StoreKind).To(BeEquivalentTo(sqliteStoreKind), "expected refreshed active SQLite backend, got %#v", report)
	Expect(hasStorageBackend(report.StorageBackends, sqliteStoreKind, "active", true)).To(BeTrue(), "expected refreshed active SQLite backend, got %#v", report)

	query, err := Query(QueryOptions{Root: root, Symbol: "Run", Resolution: "symbol"})
	Expect(err).NotTo(HaveOccurred(), "Query after SQLite replacement: %v", err)
	Expect(len(query.Nodes)).NotTo(BeEquivalentTo(0), "expected rebuilt SQLite graph to answer query, got %#v", query)
})
var _ = It("status rejects malformed current SQLite store", func() {
	root := GinkgoT().TempDir()
	writeFile(filepath.Join(root, "go.mod"), "module example.com/malformed\n\ngo 1.22\n")
	writeFile(filepath.Join(root, "pkg", "pkg.go"), "package pkg\n\nfunc Run() string { return \"ok\" }\n")
	{
		_, err := Build(context.Background(), BuildOptions{Root: root, Language: "go"})
		Expect(err).NotTo(HaveOccurred(), "Build: %v", err)
	}

	writeFile(filepath.Join(root, ".idiom", "comprehension", "graph.sqlite"), "SQLite format 3\x00placeholder")
	{
		err := writeSQLiteMetadata(root, SQLiteMetadata{StoreKind: sqliteStoreKind, SchemaVersion: sqliteGraphSchemaVersion})
		Expect(err).NotTo(HaveOccurred(), "write current sqlite metadata: %v", err)
	}

	status, err := Status(root)
	Expect(err).NotTo(HaveOccurred(), "Status should report malformed SQLite through backend status: %v", err)
	Expect(hasStorageBackend(status.StorageBackends, sqliteStoreKind, "invalid", false)).To(BeTrue(), "expected malformed current SQLite to be invalid, got %#v", status.StorageBackends)
	{
		_, err := Query(QueryOptions{Root: root, Symbol: "Run", Resolution: "symbol"})
		Expect(err).To(HaveOccurred(), "expected query to reject malformed active SQLite")
	}

	report, err := Build(context.Background(), BuildOptions{Root: root, Language: "go"})
	Expect(err).NotTo(HaveOccurred(), "refresh Build should replace malformed SQLite: %v", err)
	Expect(hasStorageBackend(report.StorageBackends, sqliteStoreKind, "active", true)).To(BeTrue(), "expected rebuilt active SQLite backend, got %#v", report.StorageBackends)
})
var _ = It("status detects freelist corruption and build recovers", func() {
	root := GinkgoT().TempDir()
	writeFile(filepath.Join(root, "go.mod"), "module example.com/freelist\n\ngo 1.22\n")
	writeFile(filepath.Join(root, "pkg", "pkg.go"), "package pkg\n\nfunc Run() string { return \"ok\" }\n")
	{
		_, err := Build(context.Background(), BuildOptions{Root: root, Language: "go"})
		Expect(err).NotTo(HaveOccurred(), "Build: %v", err)
	}

	corruptSQLiteFreelistCount(root)

	status, err := Status(root)
	Expect(err).NotTo(HaveOccurred(), "Status should report freelist corruption through backend status: %v", err)
	Expect(status.Indexed).To(BeFalse(), "corrupt SQLite status should not be indexed, got %#v", status)
	Expect(hasStorageBackend(status.StorageBackends, sqliteStoreKind, "invalid", false)).To(BeTrue(), "expected corrupt SQLite backend to be invalid, got %#v", status.StorageBackends)

	report, err := Build(context.Background(), BuildOptions{Root: root, Language: "go"})
	Expect(err).NotTo(HaveOccurred(), "Build should replace freelist-corrupt SQLite: %v", err)
	Expect(hasStorageBackend(report.StorageBackends, sqliteStoreKind, "active", true)).To(BeTrue(), "expected rebuilt active SQLite backend, got %#v", report.StorageBackends)
})

func corruptSQLiteFreelistCount(root string) {
	GinkgoHelper()

	db, err := openSQLite(sqliteGraphPath(root))
	Expect(err).NotTo(HaveOccurred(), "open sqlite: %v", err)

	_, err = db.Exec(`CREATE TABLE graph_corruption_junk(x BLOB)`)
	if err != nil {
		_ = db.Close()
	}
	Expect(err).NotTo(HaveOccurred(), "create corruption junk table: %v", err)

	_, err = db.Exec(`INSERT INTO graph_corruption_junk VALUES(zeroblob(100000))`)
	if err != nil {
		_ = db.Close()
	}
	Expect(err).NotTo(HaveOccurred(), "fill corruption junk table: %v", err)

	_, err = db.Exec(`DROP TABLE graph_corruption_junk`)
	if err != nil {
		_ = db.Close()
	}
	Expect(err).NotTo(HaveOccurred(), "drop corruption junk table: %v", err)
	{
		err := db.Close()
		Expect(err).NotTo(HaveOccurred(), "close sqlite: %v", err)
	}

	path := sqliteGraphPath(root)

	data, err := os.ReadFile(path)
	Expect(err).NotTo(HaveOccurred(), "read sqlite: %v", err)
	Expect(len(data) < 40).To(BeFalse(), "sqlite file too small to corrupt freelist count: %d", len(data))
	Expect(binary.BigEndian.Uint32(data[36:40])).NotTo(BeEquivalentTo(0), "expected dropped junk table to create freelist pages")

	binary.BigEndian.PutUint32(data[36:40], 0)
	{
		err := os.WriteFile(path, data, 0o644)
		Expect(err).NotTo(HaveOccurred(), "write corrupted sqlite: %v", err)
	}
}

var _ = It("status reports invalid SQLite when json export is missing", func() {
	root := GinkgoT().TempDir()
	writeFile(filepath.Join(root, "go.mod"), "module example.com/missingjson\n\ngo 1.22\n")
	writeFile(filepath.Join(root, "pkg", "pkg.go"), "package pkg\n\nfunc Run() string { return \"ok\" }\n")
	{
		_, err := Build(context.Background(), BuildOptions{Root: root, Language: "go"})
		Expect(err).NotTo(HaveOccurred(), "Build: %v", err)
	}

	writeFile(filepath.Join(root, ".idiom", "comprehension", "graph.sqlite"), "SQLite format 3\x00placeholder")
	{
		err := writeSQLiteMetadata(root, SQLiteMetadata{StoreKind: sqliteStoreKind, SchemaVersion: sqliteGraphSchemaVersion})
		Expect(err).NotTo(HaveOccurred(), "write current sqlite metadata: %v", err)
	}
	{
		err := os.Remove(filepath.Join(root, ".idiom", "comprehension", "graph.json"))
		Expect(err).NotTo(HaveOccurred(), "remove JSON export: %v", err)
	}

	status, err := Status(root)
	Expect(err).NotTo(HaveOccurred(), "Status should report invalid backend instead of failing when JSON export is missing: %v", err)
	Expect(status.Indexed).To(BeFalse(), "expected unindexed invalid SQLite status, got %#v", status)
	Expect(status.Fresh).To(BeFalse(), "expected unindexed invalid SQLite status, got %#v", status)
	Expect(hasStorageBackend(status.StorageBackends, sqliteStoreKind, "invalid", false)).To(BeTrue(), "expected unindexed invalid SQLite status, got %#v", status)

	report, err := Build(context.Background(), BuildOptions{Root: root, Language: "go"})
	Expect(err).NotTo(HaveOccurred(), "refresh Build should replace invalid SQLite without JSON export: %v", err)
	Expect(report.StoreKind).To(BeEquivalentTo(sqliteStoreKind), "expected rebuilt active SQLite backend, got %#v", report)
	Expect(hasStorageBackend(report.StorageBackends, sqliteStoreKind, "active", true)).To(BeTrue(), "expected rebuilt active SQLite backend, got %#v", report)
})
var _ = It("load graph uses active SQLite without json export", func() {
	root := GinkgoT().TempDir()
	writeFile(filepath.Join(root, "go.mod"), "module example.com/sqlite\n\ngo 1.22\n")
	writeFile(filepath.Join(root, "pkg", "pkg.go"), "package pkg\n\nfunc Run() string { return \"ok\" }\n")

	report, err := Build(context.Background(), BuildOptions{Root: root, Language: "go"})
	Expect(err).NotTo(HaveOccurred(), "Build: %v", err)
	Expect(report.StoreKind).To(BeEquivalentTo(sqliteStoreKind), "expected SQLite store, got %#v", report)
	{
		err := os.Remove(filepath.Join(root, ".idiom", "comprehension", "graph.json"))
		Expect(err).NotTo(HaveOccurred(), "remove JSON export: %v", err)
	}

	graph, err := LoadGraph(root)
	Expect(err).NotTo(HaveOccurred(), "LoadGraph from SQLite: %v", err)
	Expect(graph.StoreKind).To(BeEquivalentTo(sqliteStoreKind), "expected graph loaded from SQLite, got store=%q nodes=%#v", graph.StoreKind, graph.Nodes)
	Expect(hasNode(graph.Nodes, "symbol://pkg/pkg.go#Run")).To(BeTrue(), "expected graph loaded from SQLite, got store=%q nodes=%#v", graph.StoreKind, graph.Nodes)

	status, err := Status(root)
	Expect(err).NotTo(HaveOccurred(), "Status from SQLite: %v", err)
	Expect(status.Indexed).To(BeTrue(), "expected indexed SQLite status with missing JSON export, got %#v", status)
	Expect(status.StoreKind).To(BeEquivalentTo(sqliteStoreKind), "expected indexed SQLite status with missing JSON export, got %#v", status)
	Expect(hasStorageBackend(status.StorageBackends, storeKind, "absent", true)).To(BeTrue(), "expected indexed SQLite status with missing JSON export, got %#v", status)
})
var _ = It("pack produces packet ready context view", func() {
	root := GinkgoT().TempDir()
	writeFile(filepath.Join(root, "go.mod"), "module example.com/pack\n\ngo 1.22\n")
	writeFile(filepath.Join(root, "pkg", "runner.go"), `package pkg

func Run() string {
	return helper()
}

func helper() string {
	return "ok"
}
`)
	writeFile(filepath.Join(root, "pkg", "runner_test.go"), `package pkg

import "testing"

func TestRun(t *testing.T) {
	if Run() == "" {
		t.Fatal("empty")
	}
}
`)
	{
		_, err := Build(context.Background(), BuildOptions{Root: root, Language: "go"})
		Expect(err).NotTo(HaveOccurred(), "Build: %v", err)
	}

	pack, err := Pack(context.Background(), PackOptions{
		Root:           root,
		Task:           "Plan a change to Run and preserve the helper behavior",
		Mode:           "plan",
		RequiredTests:  []string{"go test ./..."},
		ForbiddenPaths: []string{"vendor"},
		Now:            testTime(),
	})
	Expect(err).NotTo(HaveOccurred(), "Pack: %v", err)
	Expect(pack.PackID).NotTo(BeEquivalentTo(""), "expected fresh saved pack identity, got %#v", pack)
	Expect(pack.ArtifactPath).NotTo(BeEquivalentTo(""), "expected fresh saved pack identity, got %#v", pack)
	Expect(pack.Fresh).To(BeTrue(), "expected fresh saved pack identity, got %#v", pack)
	Expect(hasPackFile(pack.CandidateFiles, "pkg/runner.go", "editable")).To(BeTrue(), "expected editable runner candidate, got %#v", pack.CandidateFiles)
	Expect(hasPackSymbol(pack.CandidateSymbols, "symbol://pkg/runner.go#Run")).To(BeTrue(), "expected Run symbol candidate, got %#v", pack.CandidateSymbols)
	Expect(hasPackTestCommand(pack.Tests, "go test ./...")).To(BeTrue(), "expected required and inferred Go tests, got %#v", pack.Tests)
	Expect(hasPackTestCommand(pack.Tests, "go test ./pkg")).To(BeTrue(), "expected required and inferred Go tests, got %#v", pack.Tests)
	Expect(hasPackEvidence(pack.Evidence, "context_view", pack.ArtifactPath)).To(BeTrue(), "expected context_view evidence pointing at artifact, got %#v", pack.Evidence)
	{
		_, err := os.Stat(filepath.Join(root, filepath.FromSlash(pack.ArtifactPath)))
		Expect(err).NotTo(HaveOccurred(), "expected pack artifact to be written: %v", err)
	}

	writeFile(filepath.Join(root, "pkg", "runner.go"), "package pkg\n\nfunc Run() string { return \"changed\" }\n")

	stalePack, err := Pack(context.Background(), PackOptions{Root: root, Task: "Plan a change to Run", Now: testTime()})
	Expect(err).NotTo(HaveOccurred(), "stale Pack: %v", err)
	Expect(stalePack.Fresh).To(BeFalse(), "expected stale pack to carry gaps and risks, got %#v", stalePack)
	Expect(len(stalePack.UnresolvedGaps)).NotTo(BeEquivalentTo(0), "expected stale pack to carry gaps and risks, got %#v", stalePack)
	Expect(len(stalePack.Risks)).NotTo(BeEquivalentTo(0), "expected stale pack to carry gaps and risks, got %#v", stalePack)
	Expect(hasStalePackFile(stalePack.CandidateFiles, "pkg/runner.go")).To(BeTrue(), "expected stale candidate marker, got %#v", stalePack.CandidateFiles)
})
var _ = It("pack prioritizes required edit paths", func() {
	root := GinkgoT().TempDir()
	writeFile(filepath.Join(root, "go.mod"), "module example.com/requiredpath\n\ngo 1.22\n")
	writeFile(filepath.Join(root, "pkg", "target.go"), `package pkg

func Target() string {
	return "target"
}
`)
	writeFile(filepath.Join(root, "pkg", "noisy.go"), `package pkg

func Noisy() string {
	return "noisy"
}
`)
	{
		_, err := Build(context.Background(), BuildOptions{Root: root, Language: "go"})
		Expect(err).NotTo(HaveOccurred(), "Build: %v", err)
	}

	pack, err := Pack(context.Background(), PackOptions{
		Root:              root,
		Task:              "Plan a change to Noisy",
		Mode:              "plan",
		Limit:             1,
		RequiredEditPaths: []string{"pkg/target.go"},
		Now:               testTime(),
	})
	Expect(err).NotTo(HaveOccurred(), "Pack: %v", err)
	Expect(len(pack.CandidateFiles)).To(BeEquivalentTo(1), "required edit path should outrank task-term matches, got %#v", pack.CandidateFiles)
	Expect(pack.CandidateFiles[0].Path).To(BeEquivalentTo("pkg/target.go"), "required edit path should outrank task-term matches, got %#v", pack.CandidateFiles)
	Expect(len(pack.RequiredEditPaths)).To(BeEquivalentTo(1), "expected required edit paths in result contract, got %#v", pack.RequiredEditPaths)
	Expect(pack.RequiredEditPaths[0]).To(BeEquivalentTo("pkg/target.go"), "expected required edit paths in result contract, got %#v", pack.RequiredEditPaths)
	Expect(slicesContainsRankerReason(pack.CandidateFiles[0].RankerReasons, "required_edit_path")).To(BeTrue(), "expected required_edit_path ranker reason, got %#v", pack.CandidateFiles[0].RankerReasons)
})
var _ = It("pack includes specific term matches when generic terms are noisy", func() {
	root := GinkgoT().TempDir()
	writeFile(filepath.Join(root, "go.mod"), "module example.com/specificterms\n\ngo 1.22\n")

	for i := 0; i < 14; i++ {
		writeFile(filepath.Join(root, "internal", "runtime", "agent", fmt.Sprintf("conversation_answer_%02d.go", i)), fmt.Sprintf(`package agent

func ConversationAnswerDebug%02d() string {
	return "conversation answer debug"
}
`, i))
	}

	writeFile(filepath.Join(root, "internal", "surfaces", "tui", "app", "rendering.go"), `package app

type Model struct{}

func (m Model) Resize() {}

func (m Model) UpdateViewport() {}

func ScrollableViewportKey() string {
	return "viewport"
}
`)
	{
		_, err := Build(context.Background(), BuildOptions{Root: root, Language: "go"})
		Expect(err).NotTo(HaveOccurred(), "Build: %v", err)
	}

	pack, err := Pack(context.Background(), PackOptions{
		Root:  root,
		Task:  "you are a research assistant for another agentic process; please provide structured evidence in the form of files, line numbers, and relevant symbols. We are trying to debug an issue where resizing the TUI mid-answer caused the conversation to become non-scrollable. Please debug and present your findings.",
		Mode:  "plan",
		Limit: 3,
		Now:   testTime(),
	})
	Expect(err).NotTo(HaveOccurred(), "Pack: %v", err)
	Expect(hasPackFile(pack.CandidateFiles, "internal/surfaces/tui/app/rendering.go", "editable")).To(BeTrue(), "expected specific resize/viewport candidate despite noisy answer/conversation matches, got %#v", pack.CandidateFiles)
})
var _ = It("pack bounds file handles for small model packets", func() {
	root := GinkgoT().TempDir()
	writeFile(filepath.Join(root, "go.mod"), "module example.com/bounded-pack\n\ngo 1.22\n")

	var source strings.Builder

	source.WriteString("package pkg\n\n")

	for i := 0; i < 30; i++ {
		source.WriteString("func Function")
		source.WriteString(string(rune('A' + i%26)))
		source.WriteString("() {}\n")
	}

	writeFile(filepath.Join(root, "pkg", "many.go"), source.String())
	{
		_, err := Build(context.Background(), BuildOptions{Root: root, Language: "go"})
		Expect(err).NotTo(HaveOccurred(), "Build: %v", err)
	}

	pack, err := Pack(context.Background(), PackOptions{Root: root, Task: "func", Limit: 8, Now: testTime()})
	Expect(err).NotTo(HaveOccurred(), "Pack: %v", err)

	var candidate PackFile

	for _, file := range pack.CandidateFiles {
		if file.Path == "pkg/many.go" {
			candidate = file
			break
		}
	}
	Expect(candidate.Path).NotTo(BeEquivalentTo(""), "expected many.go candidate, got %#v", pack.CandidateFiles)
	Expect(len(candidate.Handles)).To(BeNumerically("<=", maxPackFileHandles), "expected bounded file handles with omission count, got %#v", candidate)
	Expect(candidate.OmittedHandles).NotTo(BeEquivalentTo(0), "expected bounded file handles with omission count, got %#v", candidate)
})
var _ = It("trace pack parses runtime evidence and enriches pack", func() {
	root := GinkgoT().TempDir()
	writeFile(filepath.Join(root, "go.mod"), "module example.com/trace\n\ngo 1.22\n")
	writeFile(filepath.Join(root, "pkg", "runner.go"), `package pkg

func Run() string {
	return helper()
}

func helper() string {
	return "ok"
}
`)
	writeFile(filepath.Join(root, "pkg", "runner_test.go"), `package pkg

import "testing"

func TestRun(t *testing.T) {
	if Run() == "" {
		t.Fatal("empty")
	}
}
`)
	writeFile(filepath.Join(root, "cover.out"), "mode: set\npkg/runner.go:3.1,5.2 1 1\npkg/runner.go:7.1,9.2 1 0\n")
	{
		_, err := Build(context.Background(), BuildOptions{Root: root, Language: "go"})
		Expect(err).NotTo(HaveOccurred(), "Build: %v", err)
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
	Expect(err).NotTo(HaveOccurred(), "Trace: %v", err)
	Expect(trace.TraceID).NotTo(BeEquivalentTo(""), "expected saved failing fresh trace identity, got %#v", trace)
	Expect(trace.ArtifactPath).NotTo(BeEquivalentTo(""), "expected saved failing fresh trace identity, got %#v", trace)
	Expect(trace.Success).To(BeFalse(), "expected saved failing fresh trace identity, got %#v", trace)
	Expect(trace.Fresh).To(BeTrue(), "expected saved failing fresh trace identity, got %#v", trace)
	Expect(hasTraceAnchor(trace.FailingAnchors, "pkg/runner.go", 4)).To(BeTrue(), "expected failing file anchor, got %#v", trace.FailingAnchors)
	Expect(hasTraceCoverage(trace.Coverage, "pkg/runner.go")).To(BeTrue(), "expected coverage for runner.go, got %#v", trace.Coverage)
	Expect(hasEdgeSummary(trace.RuntimeEdges, "runtime_reads", "runtime_trace://"+trace.TraceID, "file://pkg/runner.go")).To(BeTrue(), "expected runtime file edge, got %#v", trace.RuntimeEdges)
	Expect(hasEdgeSummary(trace.RuntimeEdges, "runtime_calls", "runtime_trace://"+trace.TraceID, "symbol://pkg/runner_test.go#TestRun")).To(BeTrue(), "expected runtime test edge, got %#v", trace.RuntimeEdges)
	{
		_, err := os.Stat(filepath.Join(root, filepath.FromSlash(trace.ArtifactPath)))
		Expect(err).NotTo(HaveOccurred(), "expected trace artifact to be written: %v", err)
	}

	pack, err := Pack(context.Background(), PackOptions{Root: root, Task: "Fix failing TestRun in runner", Now: testTime()})
	Expect(err).NotTo(HaveOccurred(), "Pack with runtime overlay: %v", err)
	Expect(hasPackEvidence(pack.Evidence, "runtime_trace", trace.ArtifactPath)).To(BeTrue(), "expected runtime trace evidence in pack, got %#v", pack.Evidence)
	Expect(hasPackEvidence(pack.Evidence, "runtime_anchor", "pkg/runner.go")).To(BeTrue(), "expected runtime anchor evidence in pack, got %#v", pack.Evidence)
	Expect(pack.CandidateFiles).To(ContainElement(And(
		HaveField("Path", Equal("pkg/runner.go")),
		HaveField("Role", Or(Equal("editable"), Equal("read_only_evidence"))),
	)), "expected runner.go candidate from static or runtime evidence, got %#v", pack.CandidateFiles)
	Expect(hasPackTestCommand(pack.Tests, "go test ./pkg -json -coverprofile=cover.out")).To(BeTrue(), "expected failing runtime command to be carried as a test, got %#v", pack.Tests)
})
var _ = It("pack excludes stale runtime trace evidence until rerun", func() {
	root := GinkgoT().TempDir()
	writeFile(filepath.Join(root, "go.mod"), "module example.com/staletrace\n\ngo 1.22\n")

	sourcePath := filepath.Join(root, "pkg", "runner.go")
	writeFile(sourcePath, `package pkg

func Run() string {
	return "old"
}
`)
	{
		_, err := Build(context.Background(), BuildOptions{Root: root, Language: "go"})
		Expect(err).NotTo(HaveOccurred(), "Build: %v", err)
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
	Expect(err).NotTo(HaveOccurred(), "Trace: %v", err)

	writeFile(sourcePath, `package pkg

func Run() string {
	return "new"
}
`)
	{
		_, err := Build(context.Background(), BuildOptions{Root: root, Language: "go"})
		Expect(err).NotTo(HaveOccurred(), "refresh Build: %v", err)
	}

	pack, err := Pack(context.Background(), PackOptions{Root: root, Task: "Fix failing TestRun in runner", Now: testTime().Add(time.Minute)})
	Expect(err).NotTo(HaveOccurred(), "Pack with stale trace: %v", err)
	Expect(hasPackEvidence(pack.Evidence, "runtime_trace", staleTrace.ArtifactPath)).To(BeFalse(), "stale trace artifact should not be promoted into packet evidence: %#v", pack.Evidence)
	Expect(hasPackEvidence(pack.Evidence, "runtime_anchor", "pkg/runner.go")).To(BeFalse(), "stale trace anchor should not be promoted into packet evidence: %#v", pack.Evidence)
	Expect(hasGapContaining(pack.UnresolvedGaps, "reason=stale_runtime_trace")).To(BeTrue(), "expected stale trace gap, got %#v", pack.UnresolvedGaps)
	Expect(hasGapContaining(pack.UnresolvedGaps, "stale_path_count=1")).To(BeTrue(), "expected stale trace gap, got %#v", pack.UnresolvedGaps)

	debugPack, err := Pack(context.Background(), PackOptions{Root: root, Task: "Fix failing TestRun in runner", IncludeStaleRuntime: true, Now: testTime().Add(90 * time.Second)})
	Expect(err).NotTo(HaveOccurred(), "debug Pack with stale trace: %v", err)
	Expect(hasStalePackEvidence(debugPack.Evidence, "runtime_trace", staleTrace.ArtifactPath)).To(BeTrue(), "debug stale-runtime opt-in should label stale trace evidence, got %#v", debugPack.Evidence)

	freshTrace, err := Trace(TraceOptions{
		Root:      root,
		Command:   "go test ./pkg -json",
		Language:  "auto",
		Stdout:    stdout,
		ExitCode:  1,
		ExecError: "command failed: exit status 1",
		Now:       testTime().Add(2 * time.Minute),
	})
	Expect(err).NotTo(HaveOccurred(), "rerun Trace: %v", err)

	pack, err = Pack(context.Background(), PackOptions{Root: root, Task: "Fix failing TestRun in runner", Now: testTime().Add(3 * time.Minute)})
	Expect(err).NotTo(HaveOccurred(), "Pack with fresh trace: %v", err)
	Expect(hasPackEvidence(pack.Evidence, "runtime_trace", freshTrace.ArtifactPath)).To(BeTrue(), "fresh rerun trace should appear in packet evidence, got %#v", pack.Evidence)
})
var _ = It("trace pack parses language agnostic stack frames", func() {
	root := GinkgoT().TempDir()
	writeFile(filepath.Join(root, "app.py"), "def run():\n    raise AssertionError('bad')\n")
	writeFile(filepath.Join(root, "scripts", "player.gd"), "func _ready():\n    push_error(\"bad\")\n")
	writeFile(filepath.Join(root, "src", "app.ts"), "export function run() {\n  throw new Error('bad')\n}\n")
	{
		_, err := Build(context.Background(), BuildOptions{Root: root, Language: "auto"})
		Expect(err).NotTo(HaveOccurred(), "Build auto: %v", err)
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
	Expect(err).NotTo(HaveOccurred(), "Trace language agnostic: %v", err)

	for _, want := range []struct {
		path string
		line int
	}{
		{"app.py", 2},
		{"scripts/player.gd", 2},
		{"src/app.ts", 2},
	} {
		Expect(hasTraceAnchor(trace.FailingAnchors, want.path, want.line)).To(BeTrue(), "expected stack anchor %s:%d, got %#v", want.path, want.line, trace.FailingAnchors)
	}
	Expect(hasTraceFile(trace.ExecutedFiles, "app.py", "python")).To(BeTrue(), "expected executed files with language labels, got %#v", trace.ExecutedFiles)
	Expect(hasTraceFile(trace.ExecutedFiles, "scripts/player.gd", "gdscript")).To(BeTrue(), "expected executed files with language labels, got %#v", trace.ExecutedFiles)
	Expect(hasTraceFile(trace.ExecutedFiles, "src/app.ts", "typescript")).To(BeTrue(), "expected executed files with language labels, got %#v", trace.ExecutedFiles)
})
var _ = It("build indexes python with native collector", func() {
	root := GinkgoT().TempDir()
	writeFile(filepath.Join(root, "services", "planner.py"), `from typing import Any
import json

class Planner:
    def plan(self, task: str) -> Any:
        return json.loads(task)
`)

	report, err := Build(context.Background(), BuildOptions{Root: root, Language: "python"})
	Expect(err).NotTo(HaveOccurred(), "Build python: %v", err)
	Expect(report.FilesIndexed).To(BeEquivalentTo(1), "unexpected python build report: %#v", report)
	Expect(report.SymbolsIndexed).To(BeNumerically(">=", 2), "unexpected python build report: %#v", report)
	Expect(hasString(report.Languages, "python")).To(BeTrue(), "unexpected python build report: %#v", report)
	Expect(hasString(report.CollectorCoverage, "python_index")).To(BeTrue(), "unexpected python build report: %#v", report)

	graph, err := LoadGraph(root)
	Expect(err).NotTo(HaveOccurred(), "LoadGraph: %v", err)
	Expect(hasNode(graph.Nodes, "external://python:typing")).To(BeTrue(), "expected generic import relationship for typing: nodes=%#v edges=%#v", graph.Nodes, graph.Edges)
	Expect(hasEdge(graph.Edges, "imports", "file://services/planner.py", "external://python:typing")).To(BeTrue(), "expected generic import relationship for typing: nodes=%#v edges=%#v", graph.Nodes, graph.Edges)

	query, err := Query(QueryOptions{Root: root, Symbol: "Planner", Resolution: "symbol", IncludeEvidence: true})
	Expect(err).NotTo(HaveOccurred(), "Query python symbol: %v", err)
	Expect(hasNodeSummary(query.Nodes, "symbol://services/planner.py#Planner")).To(BeTrue(), "expected Python Planner symbol and evidence, got %#v", query)
	Expect(len(query.Edges)).NotTo(BeEquivalentTo(0), "expected Python Planner symbol and evidence, got %#v", query)

	sourcePath := filepath.Join(root, "services", "planner.py")
	writeFile(sourcePath, `class Planner:
    def plan_next(self, task: str) -> str:
        return task
`)

	status, err := Status(root)
	Expect(err).NotTo(HaveOccurred(), "Status python: %v", err)
	Expect(status.Fresh).To(BeFalse(), "expected stale Python symbols, got %#v", status)
	Expect(hasString(status.StaleFiles, "services/planner.py")).To(BeTrue(), "expected stale Python symbols, got %#v", status)
	Expect(status.StaleSymbols).NotTo(BeEquivalentTo(0), "expected stale Python symbols, got %#v", status)
	Expect(len(status.StaleSymbolSamples)).NotTo(BeEquivalentTo(0), "expected stale Python symbols, got %#v", status)

	refresh, err := Build(context.Background(), BuildOptions{Root: root, Language: "python"})
	Expect(err).NotTo(HaveOccurred(), "refresh Build python: %v", err)
	Expect(refresh.RefreshedStaleSymbols).NotTo(BeEquivalentTo(0), "expected refresh to report stale symbol/file recovery, got %#v", refresh)
	Expect(hasString(refresh.RefreshedStaleFiles, "services/planner.py")).To(BeTrue(), "expected refresh to report stale symbol/file recovery, got %#v", refresh)

	query, err = Query(QueryOptions{Root: root, Symbol: "plan_next", Resolution: "symbol"})
	Expect(err).NotTo(HaveOccurred(), "Query refreshed python symbol: %v", err)
	Expect(hasNodeSummary(query.Nodes, "symbol://services/planner.py#Planner.plan_next")).To(BeTrue(), "expected refreshed plan_next symbol, got %#v", query.Nodes)
})
var _ = It("build reports generic language enrichment for unregistered language", func() {
	root := GinkgoT().TempDir()
	writeFile(filepath.Join(root, "lib", "worker.rb"), `class Worker
  def run
  end
end
`)

	report, err := Build(context.Background(), BuildOptions{Root: root, Language: "ruby"})
	Expect(err).NotTo(HaveOccurred(), "Build ruby: %v", err)
	Expect(hasString(report.Languages, "ruby")).To(BeTrue(), "expected generic language enrichment for Ruby, got %#v", report)
	Expect(hasCollectorReport(report.CollectorReports, "generic_language_model", "ruby")).To(BeTrue(), "expected generic language enrichment for Ruby, got %#v", report)

	graph, err := LoadGraph(root)
	Expect(err).NotTo(HaveOccurred(), "LoadGraph ruby: %v", err)
	Expect(hasNodeKind(graph.Nodes, "toolchain://ruby/generic_language_model", "toolchain_backend")).To(BeTrue(), "expected generic backend node for Ruby, got %#v", graph.Nodes)
})
var _ = It("source language for path covers supported extension variants", func() {
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
		{
			got := sourceLanguageForPath(tc.path)
			Expect(got).To(BeEquivalentTo(tc.want), "sourceLanguageForPath(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
})
var _ = It("build indexes TypeScript module extensions", func() {
	root := GinkgoT().TempDir()
	writeFile(filepath.Join(root, "src", "helper.cts"), `export function helper() {
  return "ok";
}
`)
	writeFile(filepath.Join(root, "src", "main.mts"), `import { helper } from "./helper";

export function main() {
  return helper();
}
`)

	report, err := Build(context.Background(), BuildOptions{Root: root, Language: "typescript"})
	Expect(err).NotTo(HaveOccurred(), "Build typescript module extensions: %v", err)
	Expect(hasString(report.Languages, "typescript")).To(BeTrue(), "expected TypeScript collector coverage, got languages=%#v collectors=%#v", report.Languages, report.CollectorCoverage)
	Expect(hasString(report.CollectorCoverage, "typescript_index")).To(BeTrue(), "expected TypeScript collector coverage, got languages=%#v collectors=%#v", report.Languages, report.CollectorCoverage)

	graph, err := LoadGraph(root)
	Expect(err).NotTo(HaveOccurred(), "LoadGraph typescript module extensions: %v", err)
	Expect(hasFileLanguage(graph.Nodes, "src/main.mts", "typescript")).To(BeTrue(), "expected .mts/.cts files to be indexed as TypeScript, got %#v", graph.Nodes)
	Expect(hasFileLanguage(graph.Nodes, "src/helper.cts", "typescript")).To(BeTrue(), "expected .mts/.cts files to be indexed as TypeScript, got %#v", graph.Nodes)
	Expect(hasEdge(graph.Edges, "imports", "file://src/main.mts", "file://src/helper.cts")).To(BeTrue(), "expected TypeScript .mts -> .cts import edge, got %#v", graph.Edges)
	Expect(hasEdge(graph.Edges, "calls", "symbol://src/main.mts#main", "symbol://src/helper.cts#helper")).To(BeTrue(), "expected TypeScript call edge across module extensions, got %#v", graph.Edges)
})
var _ = It("native collectors add local relationships across language families", func() {
	root := GinkgoT().TempDir()
	writeFile(filepath.Join(root, "pkg", "helper.py"), `def run():
    return "ok"

class Runner:
    def execute(self):
        return run()
`)
	writeFile(filepath.Join(root, "tests", "test_helper.py"), `from pkg.helper import run

def test_run():
    return run()
`)
	writeFile(filepath.Join(root, "pkg", "plugin.py"), `def activate():
    return 1
`)
	writeFile(filepath.Join(root, "pkg", "dynamic.py"), `from . import plugin
import importlib

def load():
    module = importlib.import_module("pkg.helper")
    return module.run() + plugin.activate()
`)
	writeFile(filepath.Join(root, "src", "util.ts"), `export function greet() {
  return "hi";
}

export class Greeter {
  say() {
    return greet();
  }
}
`)
	writeFile(filepath.Join(root, "src", "util.test.ts"), `import { greet } from "./util";

function testGreet() {
  return greet();
}
`)
	writeFile(filepath.Join(root, "src", "reexport.ts"), `export { greet } from "./util";
`)
	writeFile(filepath.Join(root, "src", "lib.rs"), `pub fn run() {
    helper();
}

fn helper() {}
`)
	writeFile(filepath.Join(root, "src", "main", "java", "com", "example", "Helper.java"), `package com.example;

class Helper {}
`)
	writeFile(filepath.Join(root, "src", "main", "java", "com", "example", "App.java"), `package com.example;

import com.example.Helper;

class App {
  Helper run() {
    return new Helper();
  }
}
`)

	report, err := Build(context.Background(), BuildOptions{Root: root, Language: "auto"})
	Expect(err).NotTo(HaveOccurred(), "Build auto generic relationships: %v", err)

	for _, language := range []string{"python", "typescript", "rust", "java"} {
		Expect(hasString(report.Languages, language)).To(BeTrue(), "expected language %q in report, got %#v", language, report.Languages)
	}

	for _, collector := range []string{"python_index", "typescript_index", "rust_index", "java_index"} {
		Expect(hasString(report.CollectorCoverage, collector)).To(BeTrue(), "expected collector %q in report, got %#v", collector, report.CollectorCoverage)
	}

	graph, err := LoadGraph(root)
	Expect(err).NotTo(HaveOccurred(), "LoadGraph generic relationships: %v", err)
	Expect(hasEdge(graph.Edges, "imports", "file://tests/test_helper.py", "file://pkg/helper.py")).To(BeTrue(), "expected Python local import edge, got %#v", graph.Edges)
	Expect(hasEdge(graph.Edges, "imports", "file://pkg/dynamic.py", "file://pkg/plugin.py")).To(BeTrue(), "expected Python relative and dynamic import edges, got %#v", graph.Edges)
	Expect(hasEdge(graph.Edges, "imports", "file://pkg/dynamic.py", "file://pkg/helper.py")).To(BeTrue(), "expected Python relative and dynamic import edges, got %#v", graph.Edges)
	Expect(hasNodeKind(graph.Nodes, "symbol://tests/test_helper.py#test_run", "test")).To(BeTrue(), "expected Python test symbol and test edge, nodes=%#v edges=%#v", graph.Nodes, graph.Edges)
	Expect(hasEdge(graph.Edges, "tests", "symbol://tests/test_helper.py#test_run", "package://tests")).To(BeTrue(), "expected Python test symbol and test edge, nodes=%#v edges=%#v", graph.Nodes, graph.Edges)
	Expect(hasEdge(graph.Edges, "calls", "symbol://tests/test_helper.py#test_run", "symbol://pkg/helper.py#run")).To(BeTrue(), "expected Python call edge from test to helper, got %#v", graph.Edges)
	Expect(hasEdge(graph.Edges, "contains", "symbol://pkg/helper.py#Runner", "symbol://pkg/helper.py#Runner.execute")).To(BeTrue(), "expected Python class method containment and call edges, got %#v", graph.Edges)
	Expect(hasEdge(graph.Edges, "calls", "symbol://pkg/helper.py#Runner.execute", "symbol://pkg/helper.py#run")).To(BeTrue(), "expected Python class method containment and call edges, got %#v", graph.Edges)
	Expect(hasEdge(graph.Edges, "imports", "file://src/util.test.ts", "file://src/util.ts")).To(BeTrue(), "expected TypeScript relative import edge, got %#v", graph.Edges)
	Expect(hasEdge(graph.Edges, "imports", "file://src/reexport.ts", "file://src/util.ts")).To(BeTrue(), "expected TypeScript re-export dependency edge, got %#v", graph.Edges)
	Expect(hasNodeKind(graph.Nodes, "symbol://src/util.test.ts#testGreet", "test")).To(BeTrue(), "expected TypeScript test and call edges, nodes=%#v edges=%#v", graph.Nodes, graph.Edges)
	Expect(hasEdge(graph.Edges, "calls", "symbol://src/util.test.ts#testGreet", "symbol://src/util.ts#greet")).To(BeTrue(), "expected TypeScript test and call edges, nodes=%#v edges=%#v", graph.Nodes, graph.Edges)
	Expect(hasEdge(graph.Edges, "contains", "symbol://src/util.ts#Greeter", "symbol://src/util.ts#Greeter.say")).To(BeTrue(), "expected TypeScript class method containment and call edges, got %#v", graph.Edges)
	Expect(hasEdge(graph.Edges, "calls", "symbol://src/util.ts#Greeter.say", "symbol://src/util.ts#greet")).To(BeTrue(), "expected TypeScript class method containment and call edges, got %#v", graph.Edges)
	Expect(hasEdge(graph.Edges, "calls", "symbol://src/lib.rs#run", "symbol://src/lib.rs#helper")).To(BeTrue(), "expected Rust same-file call edge, got %#v", graph.Edges)
	Expect(hasEdge(graph.Edges, "imports", "file://src/main/java/com/example/App.java", "file://src/main/java/com/example/Helper.java")).To(BeTrue(), "expected Java import edge to local file, got %#v", graph.Edges)
	Expect(hasEdge(graph.Edges, "contains", "symbol://src/main/java/com/example/App.java#com.example.App", "symbol://src/main/java/com/example/App.java#com.example.App#run")).To(BeTrue(), "expected Java method containment and constructor call edges, got %#v", graph.Edges)
	Expect(hasEdge(graph.Edges, "calls", "symbol://src/main/java/com/example/App.java#com.example.App#run", "symbol://src/main/java/com/example/Helper.java#com.example.Helper")).To(BeTrue(), "expected Java method containment and constructor call edges, got %#v", graph.Edges)
})
var _ = It("build indexes C++ with rich collector", func() {
	root := GinkgoT().TempDir()
	writeFile(filepath.Join(root, "include", "entity_manager.h"), `#pragma once
struct EntityManager {
    void tick();
};
`)
	writeFile(filepath.Join(root, "include", "templates.hpp"), `#pragma once
template <typename T>
T identity(T value) {
    return value;
}
`)
	writeFile(filepath.Join(root, "alt", "entity_manager.h"), `#pragma once
struct OtherEntityManager {};
`)
	writeFile(filepath.Join(root, "src", "entity_manager.cpp"), `#include "include/entity_manager.h"

int helper() {
    return 1;
}

void EntityManager::tick() {
    helper();
}
`)
	writeFile(filepath.Join(root, "src", "main.cpp"), `#include "include/entity_manager.h"
#include "include/templates.hpp"
#include <vector>

int main() {
    EntityManager manager;
    manager.tick();
    return helper() + identity<int>(1);
}
`)
	writeFile(filepath.Join(root, "src", "ambiguous.cpp"), `#include "entity_manager.h"

int ambiguous() {
    return 0;
}
`)

	report, err := Build(context.Background(), BuildOptions{Root: root, Language: "cpp"})
	Expect(err).NotTo(HaveOccurred(), "Build cpp: %v", err)
	Expect(report.FilesIndexed).To(BeEquivalentTo(6), "unexpected C++ build report: %#v", report)
	Expect(report.SymbolsIndexed).NotTo(BeEquivalentTo(0), "unexpected C++ build report: %#v", report)
	Expect(hasString(report.Languages, "cpp")).To(BeTrue(), "unexpected C++ build report: %#v", report)
	Expect(hasString(report.CollectorCoverage, "cpp_index")).To(BeTrue(), "unexpected C++ build report: %#v", report)

	graph, err := LoadGraph(root)
	Expect(err).NotTo(HaveOccurred(), "LoadGraph cpp: %v", err)
	Expect(hasEdge(graph.Edges, "imports", "file://src/main.cpp", "file://include/entity_manager.h")).To(BeTrue(), "expected C++ include relationship to resolved header, got %#v", graph.Edges)
	Expect(hasEdge(graph.Edges, "imports", "file://src/main.cpp", "file://include/templates.hpp")).To(BeTrue(), "expected C++ template header and symbol indexing, nodes=%#v edges=%#v", graph.Nodes, graph.Edges)
	Expect(hasNode(graph.Nodes, "symbol://include/templates.hpp#identity")).To(BeTrue(), "expected C++ template header and symbol indexing, nodes=%#v edges=%#v", graph.Nodes, graph.Edges)
	Expect(hasNode(graph.Nodes, "external://cpp:vector")).To(BeTrue(), "expected C++ system include relationship for vector, nodes=%#v edges=%#v", graph.Nodes, graph.Edges)
	Expect(hasEdge(graph.Edges, "imports", "file://src/main.cpp", "external://cpp:vector")).To(BeTrue(), "expected C++ system include relationship for vector, nodes=%#v edges=%#v", graph.Nodes, graph.Edges)
	Expect(hasEdge(graph.Edges, "imports", "file://src/ambiguous.cpp", "external://cpp:entity_manager.h")).To(BeTrue(), "expected ambiguous C++ include to stay external instead of guessing a header, got %#v", graph.Edges)

	query, err := Query(QueryOptions{Root: root, Symbol: "tick", Resolution: "symbol", IncludeEvidence: true})
	Expect(err).NotTo(HaveOccurred(), "Query cpp tick: %v", err)
	Expect(hasNodeSummary(query.Nodes, "symbol://src/entity_manager.cpp#EntityManager::tick")).To(BeTrue(), "expected C++ method symbol and evidence, got %#v", query)
	Expect(len(query.Edges)).NotTo(BeEquivalentTo(0), "expected C++ method symbol and evidence, got %#v", query)

	calls, err := Zoom(ZoomOptions{Root: root, Handle: "symbol://src/main.cpp#main"})
	Expect(err).NotTo(HaveOccurred(), "Zoom cpp main: %v", err)
	Expect(calls.Node).NotTo(BeNil(), "expected C++ call relationship from main to helper, got %#v", calls)
	Expect(hasEdgeSummary(calls.Edges, "calls", "symbol://src/main.cpp#main", "symbol://src/entity_manager.cpp#helper")).To(BeTrue(), "expected C++ call relationship from main to helper, got %#v", calls)
	Expect(hasEdgeSummary(calls.Edges, "calls", "symbol://src/main.cpp#main", "symbol://src/entity_manager.cpp#EntityManager::tick")).To(BeTrue(), "expected C++ method call to prefer the definition over the header declaration, got %#v", calls)
	Expect(hasEdgeSummary(calls.Edges, "calls", "symbol://src/main.cpp#main", "symbol://include/templates.hpp#identity")).To(BeTrue(), "expected C++ template call relationship from main to identity, got %#v", calls)
	Expect(hasEdgeSummary(calls.Edges, "calls", "symbol://src/main.cpp#main", "symbol://include/entity_manager.h#tick")).To(BeFalse(), "C++ method call should not target the header declaration when a definition is available, got %#v", calls)
})
var _ = It("build indexes GDScript with dedicated collector", func() {
	root := GinkgoT().TempDir()
	writeFile(filepath.Join(root, "project.godot"), `[application]
config/name="Demo"

[autoload]
Game="*res://scripts/player.gd"
`)
	writeFile(filepath.Join(root, "scripts", "base.gd"), `class_name BaseController

func reset():
    pass
`)
	writeFile(filepath.Join(root, "scripts", "player.gd"), `extends "res://scripts/base.gd"
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
	writeFile(filepath.Join(root, "scripts", "enemy.gd"), `extends Node
class_name EnemyController

func spawn():
    pass
`)
	writeFile(filepath.Join(root, "resources", "spawn.tres"), `[gd_resource type="Resource" load_steps=2 format=3]

[ext_resource type="Script" path="res://scripts/base.gd" id="1_base"]
`)
	writeFile(filepath.Join(root, "scenes", "main.tscn"), `[gd_scene load_steps=2 format=3]

[ext_resource type="Script" path="res://scripts/player.gd" id="1_player"]

[node name="Player" type="Node"]
script = ExtResource("1_player")
`)
	writeFile(filepath.Join(root, "scenes", "enemy.tscn"), `[gd_scene load_steps=2 format=3]

[ext_resource type="Script" path="res://scripts/enemy.gd" id="1_enemy"]

[node name="Enemy" type="Node"]
script = ExtResource("1_enemy")
`)

	report, err := Build(context.Background(), BuildOptions{Root: root, Language: "gdscript"})
	Expect(err).NotTo(HaveOccurred(), "Build gdscript: %v", err)
	Expect(report.FilesIndexed).To(BeEquivalentTo(7), "unexpected GDScript build report: %#v", report)
	Expect(report.SymbolsIndexed).To(BeNumerically(">=", 10), "unexpected GDScript build report: %#v", report)
	Expect(hasString(report.Languages, "gdscript")).To(BeTrue(), "unexpected GDScript build report: %#v", report)
	Expect(hasString(report.CollectorCoverage, "gdscript_index")).To(BeTrue(), "unexpected GDScript build report: %#v", report)
	Expect(hasCollectorReport(report.CollectorReports, "godot_project_model", "gdscript")).To(BeTrue(), "expected Godot project-model enrichment report, got %#v", report.CollectorReports)

	graph, err := LoadGraph(root)
	Expect(err).NotTo(HaveOccurred(), "LoadGraph gdscript: %v", err)
	Expect(hasEdge(graph.Edges, "imports", "file://scripts/player.gd", "file://scripts/base.gd")).To(BeTrue(), "expected GDScript resource dependency edge to base script, got %#v", graph.Edges)
	Expect(hasEdge(graph.Edges, "imports", "file://scenes/main.tscn", "file://scripts/player.gd")).To(BeTrue(), "expected GDScript scene dependency edge to player script, got %#v", graph.Edges)
	Expect(hasEdge(graph.Edges, "imports", "file://scripts/player.gd", "file://resources/spawn.tres")).To(BeTrue(), "expected GDScript preload edge to internal resource, got %#v", graph.Edges)
	Expect(hasEdge(graph.Edges, "imports", "file://scripts/player.gd", "file://scenes/enemy.tscn")).To(BeTrue(), "expected GDScript legacy export PackedScene and nested scene dependency edges, got %#v", graph.Edges)
	Expect(hasEdge(graph.Edges, "imports", "file://scenes/enemy.tscn", "file://scripts/enemy.gd")).To(BeTrue(), "expected GDScript legacy export PackedScene and nested scene dependency edges, got %#v", graph.Edges)
	Expect(hasEdge(graph.Edges, "imports", "file://resources/spawn.tres", "file://scripts/base.gd")).To(BeTrue(), "expected GDScript resource dependency edge to script, got %#v", graph.Edges)
	Expect(hasEdge(graph.Edges, "imports", "file://project.godot", "file://scripts/player.gd")).To(BeTrue(), "expected Godot autoload symbol and dependency edge, nodes=%#v edges=%#v", graph.Nodes, graph.Edges)
	Expect(hasNode(graph.Nodes, "symbol://project.godot#Game")).To(BeTrue(), "expected Godot autoload symbol and dependency edge, nodes=%#v edges=%#v", graph.Nodes, graph.Edges)
	Expect(hasNode(graph.Nodes, "symbol://scripts/player.gd#spawn_scene")).To(BeTrue(), "expected annotated GDScript exported var symbol, got %#v", graph.Nodes)
	Expect(hasNode(graph.Nodes, "symbol://scripts/player.gd#legacy_enemy_scene")).To(BeTrue(), "expected legacy export and onready GDScript var symbols, got %#v", graph.Nodes)
	Expect(hasNode(graph.Nodes, "symbol://scripts/player.gd#hud")).To(BeTrue(), "expected legacy export and onready GDScript var symbols, got %#v", graph.Nodes)
	Expect(hasNodeKind(graph.Nodes, "toolchain://gdscript/godot_project_model", "toolchain_backend")).To(BeTrue(), "expected Godot toolchain and project manifest enrichment, got %#v", graph.Nodes)
	Expect(hasNode(graph.Nodes, "manifest://project.godot")).To(BeTrue(), "expected Godot toolchain and project manifest enrichment, got %#v", graph.Nodes)

	query, err := Query(QueryOptions{Root: root, Symbol: "PlayerController", Resolution: "symbol", IncludeEvidence: true})
	Expect(err).NotTo(HaveOccurred(), "Query gdscript class_name: %v", err)
	Expect(hasNodeSummary(query.Nodes, "symbol://scripts/player.gd#PlayerController")).To(BeTrue(), "expected GDScript class_name symbol and evidence, got %#v", query)
	Expect(len(query.Edges)).NotTo(BeEquivalentTo(0), "expected GDScript class_name symbol and evidence, got %#v", query)

	signalQuery, err := Query(QueryOptions{Root: root, Symbol: "died", Resolution: "symbol"})
	Expect(err).NotTo(HaveOccurred(), "Query gdscript signal: %v", err)
	Expect(hasNodeSummary(signalQuery.Nodes, "symbol://scripts/player.gd#died")).To(BeTrue(), "expected GDScript signal symbol, got %#v", signalQuery.Nodes)
})

func writeFile(path string, content string) {
	GinkgoHelper()
	{
		err := os.MkdirAll(filepath.Dir(path), 0o755)
		Expect(err).NotTo(HaveOccurred(), "mkdir %s: %v", filepath.Dir(path), err)
	}
	{
		err := os.WriteFile(path, []byte(content), 0o644)
		Expect(err).NotTo(HaveOccurred(), "write %s: %v", path, err)
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

var _ = It("graph ID content addressed across no-op rebuilds", func() {
	root := GinkgoT().TempDir()
	writeFile(filepath.Join(root, "go.mod"), "module example.com/graphid\n\ngo 1.22\n")
	writeFile(filepath.Join(root, "pkg", "x.go"), "package pkg\n\nfunc X() string { return \"x\" }\n")

	first, err := Build(GinkgoT().Context(), BuildOptions{Root: root, Language: "go"})
	Expect(err).NotTo(HaveOccurred(), "first Build: %v", err)

	// Sleep briefly so GeneratedAt would differ if it were still in the
	// hash. The point of the test is that it isn't.
	time.Sleep(2 * time.Millisecond)

	second, err := Build(GinkgoT().Context(), BuildOptions{Root: root, Language: "go"})
	Expect(err).NotTo(HaveOccurred(), "second Build: %v", err)
	Expect(first.IndexID).To(BeEquivalentTo(second.IndexID), "graphID must be content-addressed; got %q vs %q across no-op rebuilds", first.IndexID, second.IndexID)

	// Editing a file must change the index_id.
	writeFile(filepath.Join(root, "pkg", "x.go"), "package pkg\n\nfunc X() string { return \"changed\" }\n")

	third, err := Build(GinkgoT().Context(), BuildOptions{Root: root, Language: "go"})
	Expect(err).NotTo(HaveOccurred(), "third Build: %v", err)
	Expect(third.IndexID).NotTo(BeEquivalentTo(first.IndexID), "graphID must change when file content changes; got %q identical across edit", third.IndexID)
})
var _ = It("graph ID changes when collector version changes", func() {
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
	Expect(first).NotTo(BeEquivalentTo(second), "graphID must change when collector versions change; got %q for both", first)
})
