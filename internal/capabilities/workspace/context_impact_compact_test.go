package workspace

import (
	"strings"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"idiom/internal/repository/comprehension"
)

var _ = ginkgo.Describe("context_impact compact format", func() {
	ginkgo.It("renders one role-and-reason line per file, deterministically", func() {
		pack := comprehension.PackResult{
			IndexID: "idx1", Fresh: true, ArtifactPath: ".idiom/comprehension/views/p.json",
			CandidateFiles: []comprehension.PackFile{
				{Path: "core/world/day_clock.cpp", Kind: comprehension.PackFileKindDef, Why: `matches "clock" in path`},
				{Path: "client/session.cpp", Kind: comprehension.PackFileKindCaller, Why: "calls fill_sky"},
				{Path: "core/world/day_clock.h", Kind: comprehension.PackFileKindDef, Why: "defines symbols matching task terms"},
			},
			Tests: []comprehension.PackTest{{Command: "run_tests day_clock"}, {Path: "x_test.cpp"}},
		}

		want := "context_impact index_id=idx1 fresh=true view=.idiom/comprehension/views/p.json\n" +
			"1 core/world/day_clock.cpp  def  matches \"clock\" in path\n" +
			"2 client/session.cpp  caller  calls fill_sky\n" +
			"test run_tests day_clock\n" +
			"+1 more files; 0 symbols; omit format=compact for the full report\n"
		gomega.Expect(renderContextImpactCompact(pack, 2)).To(gomega.Equal(want))
		gomega.Expect(renderContextImpactCompact(pack, 2)).To(gomega.Equal(renderContextImpactCompact(pack, 2)))
	})

	ginkgo.It("stays under 1.5 KB for eight worst-case files", func() {
		var files []comprehension.PackFile
		for i := range 8 {
			files = append(files, comprehension.PackFile{
				Path: "internal/repository/comprehension/some_long_file_name_" + string(rune('a'+i)) + ".go",
				Kind: comprehension.PackFileKindCaller, Why: strings.Repeat("w", comprehension.PackWhyMaxChars),
			})
		}

		const budgetBytes = 1536

		got := renderContextImpactCompact(comprehension.PackResult{IndexID: "0123456789abcdef", CandidateFiles: files}, 8)
		gomega.Expect(len(got)).To(gomega.BeNumerically("<=", budgetBytes))
	})

	ginkgo.It("stays under 1.5 KB with multi-byte truncated reasons", func() {
		var files []comprehension.PackFile
		for i := range 8 {
			files = append(files, comprehension.PackFile{
				Path: "internal/repository/comprehension/some_long_file_name_" + string(rune('a'+i)) + ".go",
				Kind: comprehension.PackFileKindCaller,
				Why:  strings.Repeat("w", comprehension.PackWhyMaxChars-1) + "…",
			})
		}

		const budgetBytes = 1536

		got := renderContextImpactCompact(comprehension.PackResult{IndexID: "0123456789abcdef", CandidateFiles: files}, 8)
		gomega.Expect(len(got)).To(gomega.BeNumerically("<=", budgetBytes))
		gomega.Expect(strings.Count(got, "…")).To(gomega.Equal(8))
	})

	ginkgo.It("accepts default, json and compact formats and rejects others", func() {
		for _, ok := range []string{"", "json", "compact"} {
			gomega.Expect(validContextImpactFormat(ok)).To(gomega.BeTrue(), ok)
		}

		gomega.Expect(validContextImpactFormat("xml")).To(gomega.BeFalse())
	})
})
