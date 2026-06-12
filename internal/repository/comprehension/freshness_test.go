package comprehension

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
)

func TestEnsureFreshNoopFreshScopeReturnsNoRefresh(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/freshnoop\n\ngo 1.22\n")
	writeFile(t, filepath.Join(root, "pkg", "pkg.go"), "package pkg\n\nfunc Run() string { return \"ok\" }\n")

	if _, err := Build(context.Background(), BuildOptions{Root: root, Language: "go"}); err != nil {
		t.Fatalf("Build: %v", err)
	}

	decision, err := EnsureFresh(context.Background(), FreshnessRequest{
		Root:      root,
		Operation: "context_pack",
		Policy:    FreshnessReadThrough,
		Path:      "pkg",
		Language:  "go",
		MaxFiles:  200,
	})
	if err != nil {
		t.Fatalf("EnsureFresh: %v", err)
	}

	if !decision.FreshForScope || !decision.GlobalFresh || decision.Refresh != nil || decision.Reason != "" {
		t.Fatalf("expected fresh no-op decision, got %#v", decision)
	}
}

func TestEnsureFreshRefreshesBoundedStalePath(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/freshbounded\n\ngo 1.22\n")

	sourcePath := filepath.Join(root, "pkg", "pkg.go")
	writeFile(t, sourcePath, "package pkg\n\nfunc OldName() string { return \"old\" }\n")

	if _, err := Build(context.Background(), BuildOptions{Root: root, Language: "go"}); err != nil {
		t.Fatalf("Build: %v", err)
	}

	writeFile(t, sourcePath, "package pkg\n\nfunc NewName() string { return \"new\" }\n")

	decision, err := EnsureFresh(context.Background(), FreshnessRequest{
		Root:      root,
		Operation: "context_pack",
		Policy:    FreshnessReadThrough,
		Path:      "pkg/pkg.go",
		Language:  "go",
		MaxFiles:  200,
	})
	if err != nil {
		t.Fatalf("EnsureFresh: %v", err)
	}

	if decision.Refresh == nil || !decision.Refresh.Attempted || decision.Refresh.Reason != "stale_scope_files" {
		t.Fatalf("expected attempted scoped refresh, got %#v", decision)
	}

	if !decision.FreshForScope || !decision.GlobalFresh || !decision.Refresh.FreshAfter {
		t.Fatalf("expected fresh decision after refresh, got %#v", decision)
	}

	if !hasString(decision.Refresh.RefreshedFiles, "pkg/pkg.go") {
		t.Fatalf("expected refreshed file accounting, got %#v", decision.Refresh)
	}

	graph, err := LoadGraph(root)
	if err != nil {
		t.Fatalf("LoadGraph: %v", err)
	}

	if !hasNode(graph.Nodes, "symbol://pkg/pkg.go#NewName") || hasNode(graph.Nodes, "symbol://pkg/pkg.go#OldName") {
		t.Fatalf("expected refreshed source fingerprints and symbols, got nodes=%#v", graph.Nodes)
	}
}

func TestEnsureFreshCoalescesStaleFilesInSameDirectory(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/freshcoalesce\n\ngo 1.22\n")

	for i := 0; i < 5; i++ {
		writeFile(t, filepath.Join(root, "pkg", string(rune('a'+i))+".go"), fmt.Sprintf("package pkg\n\nfunc OldName%d() string { return \"old\" }\n", i))
	}

	if _, err := Build(context.Background(), BuildOptions{Root: root, Language: "go"}); err != nil {
		t.Fatalf("Build: %v", err)
	}

	for i := 0; i < 5; i++ {
		writeFile(t, filepath.Join(root, "pkg", string(rune('a'+i))+".go"), fmt.Sprintf("package pkg\n\nfunc NewName%d() string { return \"new\" }\n", i))
	}

	decision, err := EnsureFresh(context.Background(), FreshnessRequest{
		Root:      root,
		Operation: "context_status",
		Policy:    FreshnessReadThrough,
		Language:  "go",
	})
	if err != nil {
		t.Fatalf("EnsureFresh: %v", err)
	}

	if decision.Refresh == nil || !decision.Refresh.Attempted || !reflect.DeepEqual(decision.Refresh.RefreshScopes, []string{"pkg"}) {
		t.Fatalf("expected same-directory stale set to refresh one directory scope, got %#v", decision.Refresh)
	}

	if len(decision.Refresh.RefreshedFiles) != 5 || decision.Refresh.FilesIndexed != 5 {
		t.Fatalf("expected all stale files to remain accounted for, got %#v", decision.Refresh)
	}

	if !decision.FreshForScope || !decision.Refresh.FreshAfter {
		t.Fatalf("expected fresh decision after coalesced refresh, got %#v", decision)
	}
}

func TestFreshnessRefreshScopesPreserveExplicitMaxFileShape(t *testing.T) {
	paths := []string{"pkg/a.go", "pkg/b.go"}
	if got := freshnessRefreshScopes(paths, 2); !reflect.DeepEqual(got, paths) {
		t.Fatalf("explicit max-files refresh should preserve per-path scopes, got %#v", got)
	}
}

func TestFreshnessRefreshScopesCollapseHighFanoutDirectories(t *testing.T) {
	paths := []string{
		"generated/a/file.go",
		"generated/b/file.go",
		"generated/c/file.go",
		"generated/d/file.go",
		"generated/e/file.go",
		"generated/f/file.go",
		"generated/g/file.go",
		"generated/h/file.go",
	}
	if got := freshnessRefreshScopes(paths, 0); !reflect.DeepEqual(got, []string{"generated"}) {
		t.Fatalf("expected high-fanout stale set to collapse to parent scope, got %#v", got)
	}
}

func TestFreshnessRefreshScopesDoesNotOverCollapseNestedFanout(t *testing.T) {
	paths := []string{
		"native/godot-cpp/gen/include/godot_cpp/classes/a/file.hpp",
		"native/godot-cpp/gen/include/godot_cpp/classes/b/file.hpp",
		"native/godot-cpp/gen/include/godot_cpp/classes/c/file.hpp",
		"native/godot-cpp/gen/include/godot_cpp/classes/d/file.hpp",
		"native/godot-cpp/gen/include/godot_cpp/classes/e/file.hpp",
		"native/godot-cpp/gen/include/godot_cpp/classes/f/file.hpp",
		"native/godot-cpp/gen/include/godot_cpp/classes/g/file.hpp",
		"native/godot-cpp/gen/include/godot_cpp/classes/h/file.hpp",
		"scripts/tool.py",
	}
	want := []string{
		"native/godot-cpp/gen/include/godot_cpp/classes",
		"scripts/tool.py",
	}

	if got := freshnessRefreshScopes(paths, 0); !reflect.DeepEqual(got, want) {
		t.Fatalf("expected nested high-fanout collapse to stop at the narrow parent, got %#v", got)
	}
}

func TestCleanFreshnessPathRejectsTraversalAndNormalizesBackslashes(t *testing.T) {
	if got := cleanFreshnessPath(`pkg\file.go`); got != "pkg/file.go" {
		t.Fatalf("cleanFreshnessPath(backslash path) = %q, want pkg/file.go", got)
	}

	for _, path := range []string{"../outside.go", "/tmp/outside.go", `C:\tmp\outside.go`} {
		if got := cleanFreshnessPath(path); got != "" {
			t.Fatalf("cleanFreshnessPath(%q) = %q, want rejection", path, got)
		}
	}
}

func TestFreshnessDetectsPostIndexAddedSourceFile(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/freshadded\n\ngo 1.22\n")
	writeFile(t, filepath.Join(root, "pkg", "pkg.go"), "package pkg\n\nfunc Existing() string { return \"ok\" }\n")

	if _, err := Build(context.Background(), BuildOptions{Root: root, Language: "go"}); err != nil {
		t.Fatalf("Build: %v", err)
	}

	writeFile(t, filepath.Join(root, "pkg", "added.go"), "package pkg\n\nfunc Added() string { return \"new\" }\n")

	status, err := Status(root)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}

	if status.Fresh || status.StalePathCount == 0 || !hasString(status.StaleFiles, "pkg/added.go") {
		t.Fatalf("expected post-index source file to make status stale, got %#v", status)
	}

	scoped, err := StatusForScope(root, "pkg/added.go")
	if err != nil {
		t.Fatalf("StatusForScope: %v", err)
	}

	if scoped.Fresh || scoped.StalePathCount == 0 || !hasString(scoped.StaleFiles, "pkg/added.go") {
		t.Fatalf("expected post-index source file to make scoped status stale, got %#v", scoped)
	}

	decision, err := EnsureFresh(context.Background(), FreshnessRequest{
		Root:      root,
		Operation: "context_query",
		Policy:    FreshnessRequire,
		Language:  "go",
		MaxFiles:  1,
	})
	if err != nil {
		t.Fatalf("EnsureFresh: %v", err)
	}

	if decision.FreshForScope || !StaleEvidenceBlocksUse(decision) || !decision.BudgetExceeded || !hasString(decision.StalePaths, "pkg/added.go") {
		t.Fatalf("expected require_fresh to treat added source as stale evidence, got %#v", decision)
	}
}

func TestEnsureFreshIgnoresStalePathsOutsideBoundedScope(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/freshscope\n\ngo 1.22\n")
	writeFile(t, filepath.Join(root, "pkg", "pkg.go"), "package pkg\n\nfunc Keep() string { return \"ok\" }\n")

	otherPath := filepath.Join(root, "other", "other.go")
	writeFile(t, otherPath, "package other\n\nfunc OldOther() string { return \"old\" }\n")

	if _, err := Build(context.Background(), BuildOptions{Root: root, Language: "go"}); err != nil {
		t.Fatalf("Build: %v", err)
	}

	writeFile(t, otherPath, "package other\n\nfunc NewOther() string { return \"new\" }\n")

	decision, err := EnsureFresh(context.Background(), FreshnessRequest{
		Root:      root,
		Operation: "context_query",
		Policy:    FreshnessReadThrough,
		Path:      "pkg",
		Language:  "go",
		MaxFiles:  200,
	})
	if err != nil {
		t.Fatalf("EnsureFresh: %v", err)
	}

	if !decision.FreshForScope || decision.GlobalFresh || decision.Refresh != nil || len(decision.StalePaths) != 0 {
		t.Fatalf("expected scoped freshness despite unrelated stale path, got %#v", decision)
	}
}

func TestEnsureFreshBlocksUnboundedRefreshOverBudget(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/freshbudget\n\ngo 1.22\n")
	writeFile(t, filepath.Join(root, "pkg", "a.go"), "package pkg\n\nfunc OldA() string { return \"a\" }\n")
	writeFile(t, filepath.Join(root, "pkg", "b.go"), "package pkg\n\nfunc OldB() string { return \"b\" }\n")

	if _, err := Build(context.Background(), BuildOptions{Root: root, Language: "go"}); err != nil {
		t.Fatalf("Build: %v", err)
	}

	writeFile(t, filepath.Join(root, "pkg", "a.go"), "package pkg\n\nfunc NewA() string { return \"a\" }\n")
	writeFile(t, filepath.Join(root, "pkg", "b.go"), "package pkg\n\nfunc NewB() string { return \"b\" }\n")

	decision, err := EnsureFresh(context.Background(), FreshnessRequest{
		Root:      root,
		Operation: "context_status",
		Policy:    FreshnessReadThrough,
		Language:  "go",
		MaxFiles:  1,
	})
	if err != nil {
		t.Fatalf("EnsureFresh: %v", err)
	}

	if decision.Refresh == nil || decision.Refresh.Attempted || decision.Reason != "budget_exceeded" || !decision.BudgetExceeded || decision.Blocker == "" {
		t.Fatalf("expected budget freshness blocker, got %#v", decision)
	}

	if decision.FreshForScope {
		t.Fatalf("budget blocker must not report fresh scope: %#v", decision)
	}
}

func TestEnsureFreshAllowStaleReportsBudgetExceeded(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/freshallowstale\n\ngo 1.22\n")
	writeFile(t, filepath.Join(root, "pkg", "a.go"), "package pkg\n\nfunc OldA() string { return \"a\" }\n")
	writeFile(t, filepath.Join(root, "pkg", "b.go"), "package pkg\n\nfunc OldB() string { return \"b\" }\n")

	if _, err := Build(context.Background(), BuildOptions{Root: root, Language: "go"}); err != nil {
		t.Fatalf("Build: %v", err)
	}

	writeFile(t, filepath.Join(root, "pkg", "a.go"), "package pkg\n\nfunc NewA() string { return \"a\" }\n")
	writeFile(t, filepath.Join(root, "pkg", "b.go"), "package pkg\n\nfunc NewB() string { return \"b\" }\n")

	decision, err := EnsureFresh(context.Background(), FreshnessRequest{
		Root:      root,
		Operation: "context_pack",
		Policy:    FreshnessAllowStale,
		Language:  "go",
		MaxFiles:  1,
	})
	if err != nil {
		t.Fatalf("EnsureFresh: %v", err)
	}

	if decision.Refresh != nil || decision.FreshForScope || !decision.BudgetExceeded {
		t.Fatalf("allow_stale should not refresh, should remain stale, and should report budget pressure: %#v", decision)
	}
}

func TestStaleEvidenceSafetyDistinguishesBlockedReportedAndAllowed(t *testing.T) {
	base := FreshnessDecision{
		Policy:           FreshnessRequire,
		Reason:           FreshnessReasonBudgetExceeded,
		FreshForScope:    false,
		GlobalFresh:      false,
		StalePathCount:   2,
		MissingPathCount: 1,
	}
	if got := StaleEvidenceSafetyForFreshness(base); got.State != "blocked" || got.Policy != FreshnessRequire || got.Reason != FreshnessReasonBudgetExceeded || got.StalePathCount != 2 || got.MissingPathCount != 1 {
		t.Fatalf("expected blocked stale-evidence state, got %#v", got)
	}

	reportOnly := base

	reportOnly.Policy = FreshnessReportOnly

	if got := StaleEvidenceSafetyForFreshness(reportOnly); got.State != "reported" || StaleEvidenceBlocksUse(reportOnly) {
		t.Fatalf("expected report-only stale-evidence state, got %#v", got)
	}

	allowed := base

	allowed.Policy = FreshnessAllowStale

	if got := StaleEvidenceSafetyForFreshness(allowed); got.State != "allowed_stale" || StaleEvidenceBlocksUse(allowed) {
		t.Fatalf("expected allow-stale state, got %#v", got)
	}

	fresh := base

	fresh.FreshForScope = true
	fresh.GlobalFresh = true

	if got := StaleEvidenceSafetyForFreshness(fresh); got.State != "fresh" || StaleEvidenceBlocksUse(fresh) {
		t.Fatalf("expected fresh state, got %#v", got)
	}
}

func TestEnsureFreshBlocksOverBudgetMissingIndexRootRebuild(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/freshmissingbudget\n\ngo 1.22\n")
	writeFile(t, filepath.Join(root, "pkg", "a.go"), "package pkg\n\nfunc A() {}\n")
	writeFile(t, filepath.Join(root, "pkg", "b.go"), "package pkg\n\nfunc B() {}\n")
	writeFile(t, filepath.Join(root, "pkg", "c.go"), "package pkg\n\nfunc C() {}\n")

	decision, err := EnsureFresh(context.Background(), FreshnessRequest{
		Root:      root,
		Operation: "context_status",
		Policy:    FreshnessReadThrough,
		Language:  "go",
		MaxFiles:  2,
	})
	if err != nil {
		t.Fatalf("EnsureFresh: %v", err)
	}

	if decision.Refresh == nil || decision.Refresh.Attempted || decision.Refresh.Scope != "." || decision.Refresh.Reason != "budget_exceeded" || !decision.BudgetExceeded || decision.Blocker == "" {
		t.Fatalf("expected budget blocker instead of missing-index root rebuild, got %#v", decision)
	}

	if decision.FreshForScope {
		t.Fatalf("over-budget missing index must not report fresh scope: %#v", decision)
	}
}

func TestEnsureFreshBlocksOverBudgetCollectorMismatchRootRebuild(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/freshcollectorsbudget\n\ngo 1.22\n")
	writeFile(t, filepath.Join(root, "pkg", "a.go"), "package pkg\n\nfunc A() {}\n")
	writeFile(t, filepath.Join(root, "pkg", "b.go"), "package pkg\n\nfunc B() {}\n")
	writeFile(t, filepath.Join(root, "pkg", "c.go"), "package pkg\n\nfunc C() {}\n")

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

	decision, err := EnsureFresh(context.Background(), FreshnessRequest{
		Root:      root,
		Operation: "context_status",
		Policy:    FreshnessReadThrough,
		Language:  "go",
		MaxFiles:  2,
	})
	if err != nil {
		t.Fatalf("EnsureFresh: %v", err)
	}

	if decision.Refresh == nil || decision.Refresh.Attempted || decision.Refresh.Scope != "." || decision.Refresh.Reason != "budget_exceeded" || !decision.BudgetExceeded || decision.Blocker == "" {
		t.Fatalf("expected budget blocker instead of collector-mismatch root rebuild, got %#v", decision)
	}

	status, err := Status(root)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}

	if !status.CollectorVersionMismatch {
		t.Fatalf("over-budget collector mismatch should remain stale until explicit context_index, got %#v", status)
	}
}

func TestEnsureFreshRootRebuildsMissingIndexAndCollectorMismatch(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/freshroot\n\ngo 1.22\n")
	writeFile(t, filepath.Join(root, "pkg", "pkg.go"), "package pkg\n\nfunc Run() string { return \"ok\" }\n")

	missing, err := EnsureFresh(context.Background(), FreshnessRequest{
		Root:      root,
		Operation: "context_status",
		Policy:    FreshnessReadThrough,
		Language:  "go",
		MaxFiles:  25,
	})
	if err != nil {
		t.Fatalf("EnsureFresh missing index: %v", err)
	}

	if missing.Refresh == nil || !missing.Refresh.Attempted || missing.Refresh.Scope != "." || missing.Refresh.Reason != "missing_index" || !missing.FreshForScope || !missing.GlobalFresh {
		t.Fatalf("expected missing-index root rebuild, got %#v", missing)
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

	mismatch, err := EnsureFresh(context.Background(), FreshnessRequest{
		Root:      root,
		Operation: "context_status",
		Policy:    FreshnessReadThrough,
		Language:  "go",
		MaxFiles:  25,
	})
	if err != nil {
		t.Fatalf("EnsureFresh collector mismatch: %v", err)
	}

	if mismatch.Refresh == nil || !mismatch.Refresh.Attempted || mismatch.Refresh.Scope != "." || mismatch.Refresh.Reason != "collector_version_mismatch" || !mismatch.FreshForScope || !mismatch.GlobalFresh {
		t.Fatalf("expected collector-mismatch root rebuild, got %#v", mismatch)
	}

	status, err := Status(root)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}

	if !status.Fresh || status.CollectorVersionMismatch {
		t.Fatalf("expected fresh status after collector mismatch rebuild, got %#v", status)
	}
}

func TestEnsureFreshRootRebuildsCollectorMismatchForScopedRead(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/freshscopedcollector\n\ngo 1.22\n")
	writeFile(t, filepath.Join(root, "pkg", "pkg.go"), "package pkg\n\nfunc Run() string { return \"ok\" }\n")

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

	decision, err := EnsureFresh(context.Background(), FreshnessRequest{
		Root:      root,
		Operation: "context_query",
		Policy:    FreshnessReadThrough,
		Path:      "pkg/pkg.go",
		Language:  "go",
		MaxFiles:  25,
	})
	if err != nil {
		t.Fatalf("EnsureFresh collector mismatch scoped read: %v", err)
	}

	if decision.Refresh == nil || !decision.Refresh.Attempted || decision.Refresh.Scope != "." || decision.Refresh.Reason != "collector_version_mismatch" {
		t.Fatalf("expected collector mismatch to force root rebuild, got %#v", decision)
	}

	if !decision.FreshForScope || !decision.GlobalFresh {
		t.Fatalf("expected scoped read to be fresh after collector rebuild, got %#v", decision)
	}
}
