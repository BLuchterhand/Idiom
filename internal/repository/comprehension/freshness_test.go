package comprehension

import (
	"context"
	"fmt"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
)

var _ = It("ensure fresh no-op fresh scope returns no refresh", func() {
	root := GinkgoT().TempDir()
	writeFile(filepath.Join(root, "go.mod"), "module example.com/freshnoop\n\ngo 1.22\n")
	writeFile(filepath.Join(root, "pkg", "pkg.go"), "package pkg\n\nfunc Run() string { return \"ok\" }\n")
	{
		_, err := Build(context.Background(), BuildOptions{Root: root, Language: "go"})
		Expect(err).NotTo(HaveOccurred(), "Build: %v", err)
	}

	decision, err := EnsureFresh(context.Background(), FreshnessRequest{
		Root:      root,
		Operation: "context_pack",
		Policy:    FreshnessReadThrough,
		Path:      "pkg",
		Language:  "go",
		MaxFiles:  200,
	})
	Expect(err).NotTo(HaveOccurred(), "EnsureFresh: %v", err)
	Expect(decision.FreshForScope).To(BeTrue(), "expected fresh no-op decision, got %#v", decision)
	Expect(decision.GlobalFresh).To(BeTrue(), "expected fresh no-op decision, got %#v", decision)
	Expect(decision.Refresh).To(BeNil(), "expected fresh no-op decision, got %#v", decision)
	Expect(decision.Reason).To(BeEquivalentTo(""), "expected fresh no-op decision, got %#v", decision)
})
var _ = It("ensure fresh refreshes bounded stale path", func() {
	root := GinkgoT().TempDir()
	writeFile(filepath.Join(root, "go.mod"), "module example.com/freshbounded\n\ngo 1.22\n")

	sourcePath := filepath.Join(root, "pkg", "pkg.go")
	writeFile(sourcePath, "package pkg\n\nfunc OldName() string { return \"old\" }\n")
	{
		_, err := Build(context.Background(), BuildOptions{Root: root, Language: "go"})
		Expect(err).NotTo(HaveOccurred(), "Build: %v", err)
	}

	writeFile(sourcePath, "package pkg\n\nfunc NewName() string { return \"new\" }\n")

	decision, err := EnsureFresh(context.Background(), FreshnessRequest{
		Root:      root,
		Operation: "context_pack",
		Policy:    FreshnessReadThrough,
		Path:      "pkg/pkg.go",
		Language:  "go",
		MaxFiles:  200,
	})
	Expect(err).NotTo(HaveOccurred(), "EnsureFresh: %v", err)
	Expect(decision.Refresh).NotTo(BeNil(), "expected attempted scoped refresh, got %#v", decision)
	Expect(decision.Refresh.Attempted).To(BeTrue(), "expected attempted scoped refresh, got %#v", decision)
	Expect(decision.Refresh.Reason).To(BeEquivalentTo("stale_scope_files"), "expected attempted scoped refresh, got %#v", decision)
	Expect(decision.FreshForScope).To(BeTrue(), "expected fresh decision after refresh, got %#v", decision)
	Expect(decision.GlobalFresh).To(BeTrue(), "expected fresh decision after refresh, got %#v", decision)
	Expect(decision.Refresh.FreshAfter).To(BeTrue(), "expected fresh decision after refresh, got %#v", decision)
	Expect(hasString(decision.Refresh.RefreshedFiles, "pkg/pkg.go")).To(BeTrue(), "expected refreshed file accounting, got %#v", decision.Refresh)

	graph, err := LoadGraph(root)
	Expect(err).NotTo(HaveOccurred(), "LoadGraph: %v", err)
	Expect(hasNode(graph.Nodes, "symbol://pkg/pkg.go#NewName")).To(BeTrue(), "expected refreshed source fingerprints and symbols, got nodes=%#v", graph.Nodes)
	Expect(hasNode(graph.Nodes, "symbol://pkg/pkg.go#OldName")).To(BeFalse(), "expected refreshed source fingerprints and symbols, got nodes=%#v", graph.Nodes)
})
var _ = It("ensure fresh coalesces stale files in same directory", func() {
	root := GinkgoT().TempDir()
	writeFile(filepath.Join(root, "go.mod"), "module example.com/freshcoalesce\n\ngo 1.22\n")

	for i := 0; i < 5; i++ {
		writeFile(filepath.Join(root, "pkg", string(rune('a'+i))+".go"), fmt.Sprintf("package pkg\n\nfunc OldName%d() string { return \"old\" }\n", i))
	}
	{
		_, err := Build(context.Background(), BuildOptions{Root: root, Language: "go"})
		Expect(err).NotTo(HaveOccurred(), "Build: %v", err)
	}

	for i := 0; i < 5; i++ {
		writeFile(filepath.Join(root, "pkg", string(rune('a'+i))+".go"), fmt.Sprintf("package pkg\n\nfunc NewName%d() string { return \"new\" }\n", i))
	}

	decision, err := EnsureFresh(context.Background(), FreshnessRequest{
		Root:      root,
		Operation: "context_status",
		Policy:    FreshnessReadThrough,
		Language:  "go",
	})
	Expect(err).NotTo(HaveOccurred(), "EnsureFresh: %v", err)
	Expect(decision.Refresh).NotTo(BeNil(), "expected same-directory stale set to refresh one directory scope, got %#v", decision.Refresh)
	Expect(decision.Refresh.Attempted).To(BeTrue(), "expected same-directory stale set to refresh one directory scope, got %#v", decision.Refresh)
	Expect(decision.Refresh.RefreshScopes).To(Equal([]string{"pkg"}), "expected same-directory stale set to refresh one directory scope, got %#v", decision.Refresh)
	Expect(len(decision.Refresh.RefreshedFiles)).To(BeEquivalentTo(5), "expected all stale files to remain accounted for, got %#v", decision.Refresh)
	Expect(decision.Refresh.FilesIndexed).To(BeEquivalentTo(5), "expected all stale files to remain accounted for, got %#v", decision.Refresh)
	Expect(decision.FreshForScope).To(BeTrue(), "expected fresh decision after coalesced refresh, got %#v", decision)
	Expect(decision.Refresh.FreshAfter).To(BeTrue(), "expected fresh decision after coalesced refresh, got %#v", decision)
})
var _ = It("ensure fresh repairs residual stale after root refresh", func() {
	root := GinkgoT().TempDir()
	writeFile(filepath.Join(root, "go.mod"), "module example.com/freshresidual\n\ngo 1.22\n")

	sourcePath := filepath.Join(root, "pkg", "pkg.go")
	writeFile(sourcePath, "package pkg\n\nfunc OldName() string { return \"old\" }\n")
	{
		_, err := Build(context.Background(), BuildOptions{Root: root, Language: "go"})
		Expect(err).NotTo(HaveOccurred(), "Build: %v", err)
	}

	writeFile(sourcePath, "package pkg\n\nfunc RootRefreshName() string { return \"root\" }\n")

	controller := gomock.NewController(GinkgoT())
	enricher := NewMockGraphEnricher(controller)
	enricher.EXPECT().
		EnrichGraph(
			gomock.Not(gomock.Nil()),
			gomock.Cond(func(graph Graph) bool { return graph.Root == root }),
			gomock.Cond(func(options EnrichmentOptions) bool {
				return options.Root == root && options.Scope == "." && options.Language == "go"
			}),
		).
		DoAndReturn(func(context.Context, Graph, EnrichmentOptions) (GraphEnrichment, error) {
			writeFile(sourcePath, "package pkg\n\nfunc ResidualRefreshName() string { return \"residual\" }\n")
			return GraphEnrichment{}, nil
		}).
		Times(1)

	decision, err := EnsureFresh(context.Background(), FreshnessRequest{
		Root:      root,
		Operation: "context_pack",
		Policy:    FreshnessReadThrough,
		Language:  "go",
		MaxFiles:  25,
		Enrichers: []GraphEnricher{enricher},
	})
	Expect(err).NotTo(HaveOccurred(), "EnsureFresh: %v", err)
	Expect(decision.FreshForScope).To(BeTrue(), "expected residual stale path to be repaired, got %#v", decision)
	Expect(decision.GlobalFresh).To(BeTrue(), "expected residual stale path to be repaired, got %#v", decision)
	Expect(decision.Refresh).NotTo(BeNil(), "expected root refresh plus one follow-up refresh, got %#v", decision.Refresh)
	Expect(decision.Refresh.Scope).To(BeEquivalentTo("."), "expected root refresh plus one follow-up refresh, got %#v", decision.Refresh)
	Expect(len(decision.Refresh.FollowUpRefreshes)).To(BeEquivalentTo(1), "expected root refresh plus one follow-up refresh, got %#v", decision.Refresh)

	followUp := decision.Refresh.FollowUpRefreshes[0]
	Expect(followUp.Attempted).To(BeTrue(), "expected exact residual file refresh accounting, got %#v", followUp)
	Expect(followUp.Reason).To(BeEquivalentTo(FreshnessReasonStaleScopeFiles), "expected exact residual file refresh accounting, got %#v", followUp)
	Expect(hasString(followUp.RefreshedFiles, "pkg/pkg.go")).To(BeTrue(), "expected exact residual file refresh accounting, got %#v", followUp)

	graph, err := LoadGraph(root)
	Expect(err).NotTo(HaveOccurred(), "LoadGraph: %v", err)
	Expect(hasNode(graph.Nodes, "symbol://pkg/pkg.go#ResidualRefreshName")).To(BeTrue(), "expected follow-up refresh to index current source, got nodes=%#v", graph.Nodes)
	Expect(hasNode(graph.Nodes, "symbol://pkg/pkg.go#RootRefreshName")).To(BeFalse(), "expected follow-up refresh to index current source, got nodes=%#v", graph.Nodes)
})
var _ = It("ensure fresh does not repair large residual set after root refresh", func() {
	root := GinkgoT().TempDir()
	writeFile(filepath.Join(root, "go.mod"), "module example.com/freshresidualcap\n\ngo 1.22\n")
	writeFile(filepath.Join(root, "pkg", "pkg.go"), "package pkg\n\nfunc OldName() string { return \"old\" }\n")
	{
		_, err := Build(context.Background(), BuildOptions{Root: root, Language: "go"})
		Expect(err).NotTo(HaveOccurred(), "Build: %v", err)
	}

	writeFile(filepath.Join(root, "pkg", "pkg.go"), "package pkg\n\nfunc RootRefreshName() string { return \"root\" }\n")

	controller := gomock.NewController(GinkgoT())
	enricher := NewMockGraphEnricher(controller)
	enricher.EXPECT().
		EnrichGraph(
			gomock.Not(gomock.Nil()),
			gomock.Cond(func(graph Graph) bool { return graph.Root == root }),
			gomock.Cond(func(options EnrichmentOptions) bool {
				return options.Root == root && options.Scope == "." && options.Language == "go"
			}),
		).
		DoAndReturn(func(context.Context, Graph, EnrichmentOptions) (GraphEnrichment, error) {
			for i := 0; i < residualFreshnessRepairMaxFiles+1; i++ {
				writeFile(filepath.Join(root, "pkg", fmt.Sprintf("added_%02d.go", i)), fmt.Sprintf("package pkg\n\nfunc Added%02d() string { return \"new\" }\n", i))
			}
			return GraphEnrichment{}, nil
		}).
		Times(1)

	decision, err := EnsureFresh(context.Background(), FreshnessRequest{
		Root:      root,
		Operation: "context_pack",
		Policy:    FreshnessReadThrough,
		Language:  "go",
		Enrichers: []GraphEnricher{enricher},
	})
	Expect(err).NotTo(HaveOccurred(), "EnsureFresh: %v", err)
	Expect(decision.FreshForScope).To(BeFalse(), "expected large residual set to remain blocked, got %#v", decision)
	Expect(StaleEvidenceBlocksUse(decision)).To(BeTrue(), "expected large residual set to remain blocked, got %#v", decision)
	Expect(decision.Refresh).NotTo(BeNil(), "large residual set must not trigger follow-up refresh, got %#v", decision.Refresh)
	Expect(decision.Refresh.Scope).To(BeEquivalentTo("."), "large residual set must not trigger follow-up refresh, got %#v", decision.Refresh)
	Expect(len(decision.Refresh.FollowUpRefreshes)).To(BeEquivalentTo(0), "large residual set must not trigger follow-up refresh, got %#v", decision.Refresh)
	Expect(decision.StalePathCount <= residualFreshnessRepairMaxFiles).To(BeFalse(), "expected residual stale count over cap, got %#v", decision)
})

var _ = It("freshness refresh scopes preserve explicit max file shape", func() {
	paths := []string{"pkg/a.go", "pkg/b.go"}
	{
		got := freshnessRefreshScopes(paths, 2)
		Expect(got).To(Equal(paths), "explicit max-files refresh should preserve per-path scopes, got %#v", got)
	}
})
var _ = It("freshness refresh scopes collapse high fanout directories", func() {
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
	{
		got := freshnessRefreshScopes(paths, 0)
		Expect(got).To(Equal([]string{"generated"}), "expected high-fanout stale set to collapse to parent scope, got %#v", got)
	}
})
var _ = It("freshness refresh scopes does not over collapse nested fanout", func() {
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
	{
		got := freshnessRefreshScopes(paths, 0)
		Expect(got).To(Equal(want), "expected nested high-fanout collapse to stop at the narrow parent, got %#v", got)
	}
})
var _ = It("clean freshness paths rejects traversal and normalizes backslashes", func() {
	{
		got := cleanFreshnessPaths([]string{`pkg\file.go`})
		Expect(got).To(Equal([]string{"pkg/file.go"}), "cleanFreshnessPaths(backslash path) = %#v, want pkg/file.go", got)
	}
	{
		got := cleanFreshnessPaths([]string{"../outside.go", "/tmp/outside.go", `C:\tmp\outside.go`})
		Expect(len(got)).To(BeEquivalentTo(0), "cleanFreshnessPaths(unsafe paths) = %#v, want rejection", got)
	}
})
var _ = It("freshness detects post index added source file", func() {
	root := GinkgoT().TempDir()
	writeFile(filepath.Join(root, "go.mod"), "module example.com/freshadded\n\ngo 1.22\n")
	writeFile(filepath.Join(root, "pkg", "pkg.go"), "package pkg\n\nfunc Existing() string { return \"ok\" }\n")
	{
		_, err := Build(context.Background(), BuildOptions{Root: root, Language: "go"})
		Expect(err).NotTo(HaveOccurred(), "Build: %v", err)
	}

	writeFile(filepath.Join(root, "pkg", "added.go"), "package pkg\n\nfunc Added() string { return \"new\" }\n")

	status, err := Status(root)
	Expect(err).NotTo(HaveOccurred(), "Status: %v", err)
	Expect(status.Fresh).To(BeFalse(), "expected post-index source file to make status stale, got %#v", status)
	Expect(status.StalePathCount).NotTo(BeEquivalentTo(0), "expected post-index source file to make status stale, got %#v", status)
	Expect(hasString(status.StaleFiles, "pkg/added.go")).To(BeTrue(), "expected post-index source file to make status stale, got %#v", status)

	scoped, err := StatusForScope(root, "pkg/added.go")
	Expect(err).NotTo(HaveOccurred(), "StatusForScope: %v", err)
	Expect(scoped.Fresh).To(BeFalse(), "expected post-index source file to make scoped status stale, got %#v", scoped)
	Expect(scoped.StalePathCount).NotTo(BeEquivalentTo(0), "expected post-index source file to make scoped status stale, got %#v", scoped)
	Expect(hasString(scoped.StaleFiles, "pkg/added.go")).To(BeTrue(), "expected post-index source file to make scoped status stale, got %#v", scoped)

	decision, err := EnsureFresh(context.Background(), FreshnessRequest{
		Root:      root,
		Operation: "context_query",
		Policy:    FreshnessRequire,
		Language:  "go",
		MaxFiles:  1,
	})
	Expect(err).NotTo(HaveOccurred(), "EnsureFresh: %v", err)
	Expect(decision.FreshForScope).To(BeFalse(), "expected require_fresh to treat added source as stale evidence, got %#v", decision)
	Expect(StaleEvidenceBlocksUse(decision)).To(BeTrue(), "expected require_fresh to treat added source as stale evidence, got %#v", decision)
	Expect(decision.BudgetExceeded).To(BeTrue(), "expected require_fresh to treat added source as stale evidence, got %#v", decision)
	Expect(hasString(decision.StalePaths, "pkg/added.go")).To(BeTrue(), "expected require_fresh to treat added source as stale evidence, got %#v", decision)
})
var _ = It("ensure fresh ignores stale paths outside bounded scope", func() {
	root := GinkgoT().TempDir()
	writeFile(filepath.Join(root, "go.mod"), "module example.com/freshscope\n\ngo 1.22\n")
	writeFile(filepath.Join(root, "pkg", "pkg.go"), "package pkg\n\nfunc Keep() string { return \"ok\" }\n")

	otherPath := filepath.Join(root, "other", "other.go")
	writeFile(otherPath, "package other\n\nfunc OldOther() string { return \"old\" }\n")
	{
		_, err := Build(context.Background(), BuildOptions{Root: root, Language: "go"})
		Expect(err).NotTo(HaveOccurred(), "Build: %v", err)
	}

	writeFile(otherPath, "package other\n\nfunc NewOther() string { return \"new\" }\n")

	decision, err := EnsureFresh(context.Background(), FreshnessRequest{
		Root:      root,
		Operation: "context_query",
		Policy:    FreshnessReadThrough,
		Path:      "pkg",
		Language:  "go",
		MaxFiles:  200,
	})
	Expect(err).NotTo(HaveOccurred(), "EnsureFresh: %v", err)
	Expect(decision.FreshForScope).To(BeTrue(), "expected scoped freshness despite unrelated stale path, got %#v", decision)
	Expect(decision.GlobalFresh).To(BeFalse(), "expected scoped freshness despite unrelated stale path, got %#v", decision)
	Expect(decision.Refresh).To(BeNil(), "expected scoped freshness despite unrelated stale path, got %#v", decision)
	Expect(len(decision.StalePaths)).To(BeEquivalentTo(0), "expected scoped freshness despite unrelated stale path, got %#v", decision)
})
var _ = It("ensure fresh blocks unbounded refresh over budget", func() {
	root := GinkgoT().TempDir()
	writeFile(filepath.Join(root, "go.mod"), "module example.com/freshbudget\n\ngo 1.22\n")
	writeFile(filepath.Join(root, "pkg", "a.go"), "package pkg\n\nfunc OldA() string { return \"a\" }\n")
	writeFile(filepath.Join(root, "pkg", "b.go"), "package pkg\n\nfunc OldB() string { return \"b\" }\n")
	{
		_, err := Build(context.Background(), BuildOptions{Root: root, Language: "go"})
		Expect(err).NotTo(HaveOccurred(), "Build: %v", err)
	}

	writeFile(filepath.Join(root, "pkg", "a.go"), "package pkg\n\nfunc NewA() string { return \"a\" }\n")
	writeFile(filepath.Join(root, "pkg", "b.go"), "package pkg\n\nfunc NewB() string { return \"b\" }\n")

	decision, err := EnsureFresh(context.Background(), FreshnessRequest{
		Root:      root,
		Operation: "context_status",
		Policy:    FreshnessReadThrough,
		Language:  "go",
		MaxFiles:  1,
	})
	Expect(err).NotTo(HaveOccurred(), "EnsureFresh: %v", err)
	Expect(decision.Refresh).NotTo(BeNil(), "expected budget freshness blocker, got %#v", decision)
	Expect(decision.Refresh.Attempted).To(BeFalse(), "expected budget freshness blocker, got %#v", decision)
	Expect(decision.Reason).To(BeEquivalentTo("budget_exceeded"), "expected budget freshness blocker, got %#v", decision)
	Expect(decision.BudgetExceeded).To(BeTrue(), "expected budget freshness blocker, got %#v", decision)
	Expect(decision.Blocker).NotTo(BeEquivalentTo(""), "expected budget freshness blocker, got %#v", decision)
	Expect(decision.FreshForScope).To(BeFalse(), "budget blocker must not report fresh scope: %#v", decision)
})
var _ = It("ensure fresh allow stale reports budget exceeded", func() {
	root := GinkgoT().TempDir()
	writeFile(filepath.Join(root, "go.mod"), "module example.com/freshallowstale\n\ngo 1.22\n")
	writeFile(filepath.Join(root, "pkg", "a.go"), "package pkg\n\nfunc OldA() string { return \"a\" }\n")
	writeFile(filepath.Join(root, "pkg", "b.go"), "package pkg\n\nfunc OldB() string { return \"b\" }\n")
	{
		_, err := Build(context.Background(), BuildOptions{Root: root, Language: "go"})
		Expect(err).NotTo(HaveOccurred(), "Build: %v", err)
	}

	writeFile(filepath.Join(root, "pkg", "a.go"), "package pkg\n\nfunc NewA() string { return \"a\" }\n")
	writeFile(filepath.Join(root, "pkg", "b.go"), "package pkg\n\nfunc NewB() string { return \"b\" }\n")

	decision, err := EnsureFresh(context.Background(), FreshnessRequest{
		Root:      root,
		Operation: "context_pack",
		Policy:    FreshnessAllowStale,
		Language:  "go",
		MaxFiles:  1,
	})
	Expect(err).NotTo(HaveOccurred(), "EnsureFresh: %v", err)
	Expect(decision.Refresh).To(BeNil(), "allow_stale should not refresh, should remain stale, and should report budget pressure: %#v", decision)
	Expect(decision.FreshForScope).To(BeFalse(), "allow_stale should not refresh, should remain stale, and should report budget pressure: %#v", decision)
	Expect(decision.BudgetExceeded).To(BeTrue(), "allow_stale should not refresh, should remain stale, and should report budget pressure: %#v", decision)
})
var _ = It("stale evidence safety distinguishes blocked reported and allowed", func() {
	base := FreshnessDecision{
		Policy:           FreshnessRequire,
		Reason:           FreshnessReasonBudgetExceeded,
		FreshForScope:    false,
		GlobalFresh:      false,
		StalePathCount:   2,
		MissingPathCount: 1,
	}
	{
		got := StaleEvidenceSafetyForFreshness(base)
		Expect(got.State).To(BeEquivalentTo("blocked"), "expected blocked stale-evidence state, got %#v", got)
		Expect(got.Policy).To(BeEquivalentTo(FreshnessRequire), "expected blocked stale-evidence state, got %#v", got)
		Expect(got.Reason).To(BeEquivalentTo(FreshnessReasonBudgetExceeded), "expected blocked stale-evidence state, got %#v", got)
		Expect(got.StalePathCount).To(BeEquivalentTo(2), "expected blocked stale-evidence state, got %#v", got)
		Expect(got.MissingPathCount).To(BeEquivalentTo(1), "expected blocked stale-evidence state, got %#v", got)
	}

	reportOnly := base

	reportOnly.Policy = FreshnessReportOnly
	{
		got := StaleEvidenceSafetyForFreshness(reportOnly)
		Expect(got.State).To(BeEquivalentTo("reported"), "expected report-only stale-evidence state, got %#v", got)
		Expect(StaleEvidenceBlocksUse(reportOnly)).To(BeFalse(), "expected report-only stale-evidence state, got %#v", got)
	}

	allowed := base

	allowed.Policy = FreshnessAllowStale
	{
		got := StaleEvidenceSafetyForFreshness(allowed)
		Expect(got.State).To(BeEquivalentTo("allowed_stale"), "expected allow-stale state, got %#v", got)
		Expect(StaleEvidenceBlocksUse(allowed)).To(BeFalse(), "expected allow-stale state, got %#v", got)
	}

	fresh := base

	fresh.FreshForScope = true
	fresh.GlobalFresh = true
	{
		got := StaleEvidenceSafetyForFreshness(fresh)
		Expect(got.State).To(BeEquivalentTo("fresh"), "expected fresh state, got %#v", got)
		Expect(StaleEvidenceBlocksUse(fresh)).To(BeFalse(), "expected fresh state, got %#v", got)
	}
})
var _ = It("ensure fresh blocks over budget missing index root rebuild", func() {
	root := GinkgoT().TempDir()
	writeFile(filepath.Join(root, "go.mod"), "module example.com/freshmissingbudget\n\ngo 1.22\n")
	writeFile(filepath.Join(root, "pkg", "a.go"), "package pkg\n\nfunc A() {}\n")
	writeFile(filepath.Join(root, "pkg", "b.go"), "package pkg\n\nfunc B() {}\n")
	writeFile(filepath.Join(root, "pkg", "c.go"), "package pkg\n\nfunc C() {}\n")

	decision, err := EnsureFresh(context.Background(), FreshnessRequest{
		Root:      root,
		Operation: "context_status",
		Policy:    FreshnessReadThrough,
		Language:  "go",
		MaxFiles:  2,
	})
	Expect(err).NotTo(HaveOccurred(), "EnsureFresh: %v", err)
	Expect(decision.Refresh).NotTo(BeNil(), "expected budget blocker instead of missing-index root rebuild, got %#v", decision)
	Expect(decision.Refresh.Attempted).To(BeFalse(), "expected budget blocker instead of missing-index root rebuild, got %#v", decision)
	Expect(decision.Refresh.Scope).To(BeEquivalentTo("."), "expected budget blocker instead of missing-index root rebuild, got %#v", decision)
	Expect(decision.Refresh.Reason).To(BeEquivalentTo("budget_exceeded"), "expected budget blocker instead of missing-index root rebuild, got %#v", decision)
	Expect(decision.BudgetExceeded).To(BeTrue(), "expected budget blocker instead of missing-index root rebuild, got %#v", decision)
	Expect(decision.Blocker).NotTo(BeEquivalentTo(""), "expected budget blocker instead of missing-index root rebuild, got %#v", decision)
	Expect(decision.FreshForScope).To(BeFalse(), "over-budget missing index must not report fresh scope: %#v", decision)
})
var _ = It("ensure fresh blocks over budget collector mismatch root rebuild", func() {
	root := GinkgoT().TempDir()
	writeFile(filepath.Join(root, "go.mod"), "module example.com/freshcollectorsbudget\n\ngo 1.22\n")
	writeFile(filepath.Join(root, "pkg", "a.go"), "package pkg\n\nfunc A() {}\n")
	writeFile(filepath.Join(root, "pkg", "b.go"), "package pkg\n\nfunc B() {}\n")
	writeFile(filepath.Join(root, "pkg", "c.go"), "package pkg\n\nfunc C() {}\n")
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

	decision, err := EnsureFresh(context.Background(), FreshnessRequest{
		Root:      root,
		Operation: "context_status",
		Policy:    FreshnessReadThrough,
		Language:  "go",
		MaxFiles:  2,
	})
	Expect(err).NotTo(HaveOccurred(), "EnsureFresh: %v", err)
	Expect(decision.Refresh).NotTo(BeNil(), "expected budget blocker instead of collector-mismatch root rebuild, got %#v", decision)
	Expect(decision.Refresh.Attempted).To(BeFalse(), "expected budget blocker instead of collector-mismatch root rebuild, got %#v", decision)
	Expect(decision.Refresh.Scope).To(BeEquivalentTo("."), "expected budget blocker instead of collector-mismatch root rebuild, got %#v", decision)
	Expect(decision.Refresh.Reason).To(BeEquivalentTo("budget_exceeded"), "expected budget blocker instead of collector-mismatch root rebuild, got %#v", decision)
	Expect(decision.BudgetExceeded).To(BeTrue(), "expected budget blocker instead of collector-mismatch root rebuild, got %#v", decision)
	Expect(decision.Blocker).NotTo(BeEquivalentTo(""), "expected budget blocker instead of collector-mismatch root rebuild, got %#v", decision)

	status, err := Status(root)
	Expect(err).NotTo(HaveOccurred(), "Status: %v", err)
	Expect(status.CollectorVersionMismatch).To(BeTrue(), "over-budget collector mismatch should remain stale until explicit context_index, got %#v", status)
})
var _ = It("ensure fresh root rebuilds missing index and collector mismatch", func() {
	root := GinkgoT().TempDir()
	writeFile(filepath.Join(root, "go.mod"), "module example.com/freshroot\n\ngo 1.22\n")
	writeFile(filepath.Join(root, "pkg", "pkg.go"), "package pkg\n\nfunc Run() string { return \"ok\" }\n")

	missing, err := EnsureFresh(context.Background(), FreshnessRequest{
		Root:      root,
		Operation: "context_status",
		Policy:    FreshnessReadThrough,
		Language:  "go",
		MaxFiles:  25,
	})
	Expect(err).NotTo(HaveOccurred(), "EnsureFresh missing index: %v", err)
	Expect(missing.Refresh).NotTo(BeNil(), "expected missing-index root rebuild, got %#v", missing)
	Expect(missing.Refresh.Attempted).To(BeTrue(), "expected missing-index root rebuild, got %#v", missing)
	Expect(missing.Refresh.Scope).To(BeEquivalentTo("."), "expected missing-index root rebuild, got %#v", missing)
	Expect(missing.Refresh.Reason).To(BeEquivalentTo("missing_index"), "expected missing-index root rebuild, got %#v", missing)
	Expect(missing.FreshForScope).To(BeTrue(), "expected missing-index root rebuild, got %#v", missing)
	Expect(missing.GlobalFresh).To(BeTrue(), "expected missing-index root rebuild, got %#v", missing)

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

	mismatch, err := EnsureFresh(context.Background(), FreshnessRequest{
		Root:      root,
		Operation: "context_status",
		Policy:    FreshnessReadThrough,
		Language:  "go",
		MaxFiles:  25,
	})
	Expect(err).NotTo(HaveOccurred(), "EnsureFresh collector mismatch: %v", err)
	Expect(mismatch.Refresh).NotTo(BeNil(), "expected collector-mismatch root rebuild, got %#v", mismatch)
	Expect(mismatch.Refresh.Attempted).To(BeTrue(), "expected collector-mismatch root rebuild, got %#v", mismatch)
	Expect(mismatch.Refresh.Scope).To(BeEquivalentTo("."), "expected collector-mismatch root rebuild, got %#v", mismatch)
	Expect(mismatch.Refresh.Reason).To(BeEquivalentTo("collector_version_mismatch"), "expected collector-mismatch root rebuild, got %#v", mismatch)
	Expect(mismatch.FreshForScope).To(BeTrue(), "expected collector-mismatch root rebuild, got %#v", mismatch)
	Expect(mismatch.GlobalFresh).To(BeTrue(), "expected collector-mismatch root rebuild, got %#v", mismatch)

	status, err := Status(root)
	Expect(err).NotTo(HaveOccurred(), "Status: %v", err)
	Expect(status.Fresh).To(BeTrue(), "expected fresh status after collector mismatch rebuild, got %#v", status)
	Expect(status.CollectorVersionMismatch).To(BeFalse(), "expected fresh status after collector mismatch rebuild, got %#v", status)
})
var _ = It("ensure fresh root rebuilds collector mismatch for scoped read", func() {
	root := GinkgoT().TempDir()
	writeFile(filepath.Join(root, "go.mod"), "module example.com/freshscopedcollector\n\ngo 1.22\n")
	writeFile(filepath.Join(root, "pkg", "pkg.go"), "package pkg\n\nfunc Run() string { return \"ok\" }\n")
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

	decision, err := EnsureFresh(context.Background(), FreshnessRequest{
		Root:      root,
		Operation: "context_query",
		Policy:    FreshnessReadThrough,
		Path:      "pkg/pkg.go",
		Language:  "go",
		MaxFiles:  25,
	})
	Expect(err).NotTo(HaveOccurred(), "EnsureFresh collector mismatch scoped read: %v", err)
	Expect(decision.Refresh).NotTo(BeNil(), "expected collector mismatch to force root rebuild, got %#v", decision)
	Expect(decision.Refresh.Attempted).To(BeTrue(), "expected collector mismatch to force root rebuild, got %#v", decision)
	Expect(decision.Refresh.Scope).To(BeEquivalentTo("."), "expected collector mismatch to force root rebuild, got %#v", decision)
	Expect(decision.Refresh.Reason).To(BeEquivalentTo("collector_version_mismatch"), "expected collector mismatch to force root rebuild, got %#v", decision)
	Expect(decision.FreshForScope).To(BeTrue(), "expected scoped read to be fresh after collector rebuild, got %#v", decision)
	Expect(decision.GlobalFresh).To(BeTrue(), "expected scoped read to be fresh after collector rebuild, got %#v", decision)
})
