package comprehension

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Incremental refresh and a cold rebuild of the same tree must produce the
// same graph: nodes, edges and the derived counts the manifest and status
// report. Only the repository node and the repository summary are left out of
// the content comparison: their ids hash the root path, which differs between
// the two temp directories. Both still count in the derived totals.

func equivalenceBaseFiles() map[string]string {
	return map[string]string{
		"go.mod": "module example.com/equiv\n\ngo 1.22\n",
		"core/a.cpp": `#include "a.h"
namespace eq {
int alpha(int x) {
    return beta(x) + 1;
}
}  // namespace eq
`,
		"core/a.h": "namespace eq { int alpha(int x); int beta(int x); int gamma(int x); }\n",
		"core/b.cpp": `#include "a.h"
namespace eq {
int beta(int x) {
    return gamma(x) * 2;
}
}  // namespace eq
`,
		"core/c.cpp": `#include "a.h"
namespace eq {
int gamma(int x) { return x; }
}  // namespace eq
`,
		"core/b_test.cpp": `#include "a.h"
namespace eq {
void test_beta() { beta(1); }
}  // namespace eq
`,
		"pkg/x.go": "package pkg\n\nfunc Run() string { return helper() }\n\nfunc helper() string { return \"x\" }\n",
		"pkg/y.go": "package pkg\n\nfunc Other() string { return Run() }\n",
		"pkg/z.go": "package pkg\n\nfunc Gone() string { return Other() }\n",
	}
}

// equivalenceEdits applies an edit, a rename, a delete and an add to root and
// returns the repository-relative paths that changed (old and new names).
func equivalenceEdits(root string) []string {
	writeFile(filepath.Join(root, "core/a.cpp"), `#include "a.h"
namespace eq {
int alpha2(int x) {
    return beta(x) + gamma(x);
}
}  // namespace eq
`)
	Expect(os.Rename(filepath.Join(root, "core/c.cpp"), filepath.Join(root, "core/d.cpp"))).To(Succeed())
	Expect(os.Remove(filepath.Join(root, "core/b_test.cpp"))).To(Succeed())
	writeFile(filepath.Join(root, "core/e.cpp"), `#include "a.h"
namespace eq {
int epsilon(int x) { return alpha2(x); }
}  // namespace eq
`)
	writeFile(filepath.Join(root, "pkg/x.go"), "package pkg\n\nfunc Run() string { return helper2() }\n\nfunc helper2() string { return \"y\" }\n")
	Expect(os.Remove(filepath.Join(root, "pkg/z.go"))).To(Succeed())

	return []string{"core/a.cpp", "core/c.cpp", "core/d.cpp", "core/b_test.cpp", "core/e.cpp", "pkg/x.go", "pkg/z.go"}
}

func writeEquivalenceFiles(root string, files map[string]string) {
	for path, content := range files {
		writeFile(filepath.Join(root, path), content)
	}
}

type normalizedGraph struct {
	nodes   []string
	edges   []string
	derived []string
	lexical []string
}

// lexicalRowsForEquivalence dumps the lexical index with document ids
// resolved to paths, so two stores compare by content, not rowid.
func lexicalRowsForEquivalence(root string) []string {
	db, err := openSQLite(sqliteGraphPath(root))
	Expect(err).NotTo(HaveOccurred())

	defer db.Close()

	var rows []string

	docs, err := db.Query(`SELECT path, fingerprint, length FROM lexical_docs`)
	Expect(err).NotTo(HaveOccurred())

	for docs.Next() {
		var path, fingerprint string

		var length int

		Expect(docs.Scan(&path, &fingerprint, &length)).To(Succeed())

		rows = append(rows, fmt.Sprintf("doc|%s|%s|%d", path, fingerprint, length))
	}

	Expect(docs.Close()).To(Succeed())

	postings, err := db.Query(`SELECT p.term, d.path, p.tf FROM lexical_postings p JOIN lexical_docs d ON d.doc_id = p.doc_id`)
	Expect(err).NotTo(HaveOccurred())

	for postings.Next() {
		var term, path string

		var tf int

		Expect(postings.Scan(&term, &path, &tf)).To(Succeed())

		rows = append(rows, fmt.Sprintf("posting|%s|%s|%d", term, path, tf))
	}

	Expect(postings.Close()).To(Succeed())

	return rows
}

func normalizeGraphForEquivalence(root string) normalizedGraph {
	graph, err := LoadGraph(root)
	Expect(err).NotTo(HaveOccurred())

	skipped := map[string]struct{}{}
	counts := map[string]int{}

	var out normalizedGraph

	for _, node := range graph.Nodes {
		counts["node:"+node.Kind]++

		if node.Kind == "repo" || strings.HasPrefix(node.ID, "summary://repository/") {
			skipped[node.ID] = struct{}{}
			continue
		}

		props, _ := json.Marshal(node.Properties)
		out.nodes = append(out.nodes, fmt.Sprintf("%s|%s|%s|%s|%d-%d|%s|%s|%s", node.ID, node.Kind, node.Path, node.QualifiedName, node.StartLine, node.EndLine, node.Fingerprint, node.Collector, props))
	}

	for _, edge := range graph.Edges {
		counts["edge:"+edge.Kind]++

		_, srcSkipped := skipped[edge.SourceID]
		_, dstSkipped := skipped[edge.TargetID]

		if srcSkipped || dstSkipped {
			continue
		}

		evidence, _ := json.Marshal(edge.Evidence)
		props, _ := json.Marshal(edge.Properties)
		out.edges = append(out.edges, fmt.Sprintf("%s|%s>%s|%s|%s|%s", edge.Kind, edge.SourceID, edge.TargetID, edge.Collector, evidence, props))
	}

	for key, count := range counts {
		out.derived = append(out.derived, fmt.Sprintf("%s=%d", key, count))
	}

	status, err := Status(root)
	Expect(err).NotTo(HaveOccurred())

	out.derived = append(out.derived,
		fmt.Sprintf("status.files=%d", status.FilesIndexed),
		fmt.Sprintf("status.symbols=%d", status.SymbolsIndexed),
		fmt.Sprintf("status.edges=%d", status.EdgesIndexed),
		fmt.Sprintf("status.languages=%s", strings.Join(status.Languages, ",")),
	)

	slices.Sort(out.nodes)
	slices.Sort(out.edges)
	slices.Sort(out.derived)

	out.lexical = lexicalRowsForEquivalence(root)
	slices.Sort(out.lexical)

	return out
}

func diffSorted(label string, incremental []string, cold []string) []string {
	var diffs []string

	have := map[string]struct{}{}
	for _, item := range incremental {
		have[item] = struct{}{}
	}

	want := map[string]struct{}{}

	for _, item := range cold {
		want[item] = struct{}{}
		if _, ok := have[item]; !ok {
			diffs = append(diffs, label+" missing from incremental: "+item)
		}
	}

	for _, item := range incremental {
		if _, ok := want[item]; !ok {
			diffs = append(diffs, label+" extra in incremental: "+item)
		}
	}

	return diffs
}

func refreshChangedPaths(root string, paths []string, exact bool) {
	GinkgoHelper()

	req := FreshnessRequest{Root: root, PatchActiveStore: true, ExactScopeRefresh: exact}
	report := refreshFreshnessFileSet(context.Background(), root, req, paths, FreshnessReasonStaleScopeFiles, "auto", "standard")
	Expect(report.Error).To(BeEmpty())
}

var _ = DescribeTable("incremental refresh equals a cold rebuild",
	func(exact bool) {
		live := GinkgoT().TempDir()
		cold := GinkgoT().TempDir()

		writeEquivalenceFiles(live, equivalenceBaseFiles())
		_, err := Build(context.Background(), BuildOptions{Root: live, Language: "auto"})
		Expect(err).NotTo(HaveOccurred())

		changed := equivalenceEdits(live)
		refreshChangedPaths(live, changed, exact)

		// The cold tree is the live tree's final contents, built from nothing.
		writeEquivalenceFiles(cold, equivalenceBaseFiles())
		equivalenceEdits(cold)
		_, err = Build(context.Background(), BuildOptions{Root: cold, Language: "auto"})
		Expect(err).NotTo(HaveOccurred())

		got := normalizeGraphForEquivalence(live)
		want := normalizeGraphForEquivalence(cold)

		var diffs []string

		diffs = append(diffs, diffSorted("node", got.nodes, want.nodes)...)
		diffs = append(diffs, diffSorted("edge", got.edges, want.edges)...)
		diffs = append(diffs, diffSorted("derived", got.derived, want.derived)...)
		diffs = append(diffs, diffSorted("lexical", got.lexical, want.lexical)...)
		Expect(want.lexical).NotTo(BeEmpty(), "cold build stored no lexical index")
		Expect(diffs).To(BeEmpty(), strings.Join(diffs, "\n"))
	},
	Entry("dependency-expanding patch", false),
	Entry("exact-scope patch", true),
)
