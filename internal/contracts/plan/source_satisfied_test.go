package plan

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Source-satisfied answers", func() {
	It("requires an explicit decision covering every required path", func() {
		answer := "Decision: source-satisfied\n- a.txt: already present.\n- b.txt: current-state evidence."
		Expect(SourceSatisfiedAnswerCoversPaths(answer, []string{"a.txt", "b.txt"})).To(BeTrue())
		Expect(SourceSatisfiedAnswerCoversPaths("a.txt and b.txt already present", []string{"a.txt", "b.txt"})).To(BeFalse())
		Expect(SourceSatisfiedAnswerCoversPaths(answer, []string{"a.txt", "c.txt"})).To(BeFalse())
	})

	It("rejects stale-anchor intent", func() {
		answer := "Decision: source-satisfied\n- pkg/x.go: stale anchor; old_text no longer applies."
		Expect(SourceSatisfiedAnswerIntent(answer)).To(BeFalse())
		Expect(SourceSatisfiedAnswerCoversPaths(answer, []string{"pkg/x.go"})).To(BeFalse())
	})

	DescribeTable("requires exact path mentions",
		func(answer string) {
			Expect(SourceSatisfiedAnswerCoversPaths(answer, []string{"a.txt"})).To(BeFalse())
		},
		Entry("sibling prefix", "Decision: source-satisfied\n- ba.txt: already present."),
		Entry("backup suffix", "Decision: source-satisfied\n- a.txt.bak: already present."),
		Entry("old suffix in directory", "Decision: source-satisfied\n- dir/a.txt.old: already present."),
	)

	It("reports exact missing coverage paths", func() {
		answer := "Decision: source-satisfied\n- a.txt.bak: already present.\n- b.txt: already present.\n- ./Dockerfile: already present."
		Expect(SourceSatisfiedMissingCoveragePaths(answer, []string{"a.txt", "b.txt", "Dockerfile"})).To(Equal([]string{"a.txt"}))
	})

	It("uses scope path normalization", func() {
		answer := "Decision: source-satisfied\n- pkg/feature.go: already present."
		Expect(SourceSatisfiedAnswerCoversPaths(answer, []string{`pkg\feature.go`})).To(BeTrue())
		Expect(SourceSatisfiedMissingCoveragePaths(answer, []string{"../secret.go", "/tmp/abs.go", "pkg/./bad.go"})).To(BeEmpty())
	})

	It("requires decision intent before reporting coverage", func() {
		Expect(SourceSatisfiedMissingCoveragePaths(
			"a.txt and b.txt are already present", []string{"b.txt", "a.txt"},
		)).To(Equal([]string{"a.txt", "b.txt"}))
	})

	DescribeTable("accepts exact extensionless paths",
		func(answer string, requiredPaths []string) {
			Expect(SourceSatisfiedAnswerCoversPaths(answer, requiredPaths)).To(BeTrue())
		},
		Entry("Dockerfile", "Decision: source-satisfied\n- Dockerfile: already present.", []string{"Dockerfile"}),
		Entry("build file", "Decision: source-satisfied\n- BUILD: already present.", []string{"BUILD"}),
		Entry("nested Makefile", "Decision: source-satisfied\n- cmd/foo/Makefile: already present.", []string{"cmd/foo/Makefile"}),
		Entry("dot-slash Dockerfile", "Decision: source-satisfied\n- ./Dockerfile: already present.", []string{"Dockerfile"}),
		Entry("backticked Dockerfile", "Decision: source-satisfied\n- `Dockerfile`: already present.", []string{"Dockerfile"}),
		Entry("bold Dockerfile", "Decision: source-satisfied\n- **Dockerfile**: already present.", []string{"Dockerfile"}),
	)

	DescribeTable("rejects near matches for extensionless paths",
		func(answer string, requiredPaths []string) {
			Expect(SourceSatisfiedAnswerCoversPaths(answer, requiredPaths)).To(BeFalse())
		},
		Entry("backup suffix", "Decision: source-satisfied\n- Dockerfile.bak: already present.", []string{"Dockerfile"}),
		Entry("sibling prefix", "Decision: source-satisfied\n- OldDockerfile: already present.", []string{"Dockerfile"}),
		Entry("nested is not root", "Decision: source-satisfied\n- deploy/Dockerfile: already present.", []string{"Dockerfile"}),
		Entry("root is not nested", "Decision: source-satisfied\n- Dockerfile: already present.", []string{"deploy/Dockerfile"}),
		Entry("prose does not cover root", "Decision: source-satisfied\n- deploy/Dockerfile: already present; root Dockerfile is not covered.", []string{"Dockerfile"}),
	)

	It("extracts normalized evidence paths only from an explicit decision", func() {
		answer := "Decision: source-satisfied\n- ./b.txt: already present.\n- a.txt: current-state evidence.\n- b.txt: duplicate."
		Expect(SourceSatisfiedEvidencePaths(answer)).To(Equal([]string{"a.txt", "b.txt"}))
		blocker := "<<QUESTION>>: use Decision: source-satisfied if pkg/parser.go is already implemented"
		Expect(SourceSatisfiedEvidencePaths(blocker)).To(BeEmpty())
	})
})
