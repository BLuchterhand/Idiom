package implementationdispatch

import (
	"strings"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("Accepted refine candidates", func() {
	ginkgo.It("sanitizes nonzero refine metadata without losing the dispatchable brief", func() {
		content := strings.Join([]string{
			"> WARNING: refine exited non-zero - reason=max-iters-exhausted after iter 3.",
			"",
			"<!-- idiom:refine-drafted title=tui-example iter=3 reason=max-iters-exhausted -->",
			"# Accepted",
			"",
			"## Files To Modify",
			"",
			"| File | Change |",
			"|---|---|",
			"| target.txt | NEW FILE: write target |",
			"",
		}, "\n")
		sanitized, changed := SanitizeNonzeroRefineAcceptedContent(content)
		gomega.Expect(changed).To(gomega.BeTrue())
		gomega.Expect(sanitized).NotTo(gomega.Or(
			gomega.ContainSubstring("refine exited non-zero"),
			gomega.ContainSubstring("idiom:refine-drafted"),
			gomega.ContainSubstring("reason=max-iters-exhausted"),
		))
		gomega.Expect(sanitized).To(gomega.And(
			gomega.ContainSubstring("idiom:tui-refine-accepted-after-structural-preflight"),
			gomega.ContainSubstring("## Files To Modify"),
		))
	})

	ginkgo.It("preserves authorized new files when replacing a plan document", func() {
		docs := []PlanDocument{{Path: "docs/source.md", AuthorizedNewFiles: []string{"target.txt"}}}
		got := ReplacePlanDocument(docs, "docs/source.md", PlanDocument{Path: ".idiom/refine/example/accepted.md"})
		gomega.Expect(got).To(gomega.ConsistOf(gomega.And(
			gomega.HaveField("Path", ".idiom/refine/example/accepted.md"),
			gomega.HaveField("AuthorizedNewFiles", []string{"target.txt"}),
		)))
	})
})
