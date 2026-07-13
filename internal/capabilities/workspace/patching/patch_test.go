package patching

import (
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("Workspace patch format", func() {
	ginkgo.It("parses add, update, move, and delete operations", func() {
		patch := "*** Begin Patch\n" +
			"*** Add File: new/hello.txt\n" +
			"+hello\n" +
			"+world\n" +
			"*** Update File: existing.go\n" +
			"*** Move to: renamed.go\n" +
			"@@\n" +
			" package main\n" +
			"-old line\n" +
			"+new line\n" +
			"*** Delete File: gone.txt\n" +
			"*** End Patch"

		ops, err := Parse(patch)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(ops).To(gomega.HaveLen(3))
		gomega.Expect(ops[0]).To(gomega.SatisfyAll(
			gomega.HaveField("Kind", OpAdd),
			gomega.HaveField("Path", "new/hello.txt"),
			gomega.HaveField("NewBody", "hello\nworld"),
		))
		gomega.Expect(ops[1]).To(gomega.SatisfyAll(
			gomega.HaveField("Kind", OpUpdate),
			gomega.HaveField("Path", "existing.go"),
			gomega.HaveField("MoveTo", "renamed.go"),
		))
		gomega.Expect(ops[1].Hunks).To(gomega.HaveLen(1))
		gomega.Expect(ops[1].Hunks[0].Lines).To(gomega.HaveLen(3))
		gomega.Expect(ops[2]).To(gomega.SatisfyAll(
			gomega.HaveField("Kind", OpDelete),
			gomega.HaveField("Path", "gone.txt"),
		))
	})

	ginkgo.DescribeTable("rejects malformed envelopes and sections",
		func(patch string) {
			_, err := Parse(patch)
			gomega.Expect(err).To(gomega.HaveOccurred())
		},
		ginkgo.Entry("missing envelope", "*** Add File: x\n+y\n"),
		ginkgo.Entry("orphan line", "*** Begin Patch\n+orphan\n*** End Patch"),
		ginkgo.Entry("invalid add line", "*** Begin Patch\n*** Add File: f\nbad-prefix\n*** End Patch"),
		ginkgo.Entry("update without hunk", "*** Begin Patch\n*** Update File: f\nbad-line\n*** End Patch"),
		ginkgo.Entry("move without update", "*** Begin Patch\n*** Move to: x\n*** End Patch"),
		ginkgo.Entry("empty patch", "*** Begin Patch\n*** End Patch"),
	)

	ginkgo.It("round-trips add, update, and delete operations", func() {
		original := "*** Begin Patch\n" +
			"*** Add File: created/note.md\n" +
			"+# Note\n" +
			"+body\n" +
			"*** Update File: existing.txt\n" +
			"@@\n" +
			" alpha\n" +
			"-beta\n" +
			"+BETA\n" +
			" gamma\n" +
			"*** Delete File: doomed.txt\n" +
			"*** End Patch\n"

		ops, err := Parse(original)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		round, err := Parse(Serialize(ops))
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(round).To(gomega.HaveLen(len(ops)))
		for i := range ops {
			gomega.Expect(round[i].Kind).To(gomega.Equal(ops[i].Kind))
			gomega.Expect(round[i].Path).To(gomega.Equal(ops[i].Path))
			gomega.Expect(round[i].NewBody).To(gomega.Equal(ops[i].NewBody))
			gomega.Expect(round[i].Hunks).To(gomega.HaveLen(len(ops[i].Hunks)))
		}
	})

	ginkgo.It("tolerates unknown triple-star commentary directives", func() {
		patch := "*** Begin Patch\n" +
			"*** Sanity Check\n" +
			"*** Update File: existing.txt\n" +
			"@@\n" +
			" alpha\n" +
			"-beta\n" +
			"+BETA\n" +
			" gamma\n" +
			"*** Note: this is a model commentary directive\n" +
			"*** Add File: new.txt\n" +
			"+content\n" +
			"*** Verification\n" +
			"*** End Patch"

		ops, err := Parse(patch)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(ops).To(gomega.HaveLen(2))
		gomega.Expect(ops[0]).To(gomega.SatisfyAll(
			gomega.HaveField("Kind", OpUpdate),
			gomega.HaveField("Path", "existing.txt"),
		))
		gomega.Expect(ops[1]).To(gomega.SatisfyAll(
			gomega.HaveField("Kind", OpAdd),
			gomega.HaveField("Path", "new.txt"),
		))
	})

	ginkgo.It("rejects bare prose outside a file section", func() {
		_, err := Parse("*** Begin Patch\n" +
			"this is just prose, not a directive\n" +
			"*** Update File: existing.txt\n" +
			"@@\n" +
			" alpha\n" +
			"+beta\n" +
			"*** End Patch")
		gomega.Expect(err).To(gomega.HaveOccurred())
	})
})

var _ = ginkgo.Describe("Patch operation paths", func() {
	ginkgo.It("returns unique source and move destinations in encounter order", func() {
		ops := []Op{
			{Kind: OpUpdate, Path: "a.go", MoveTo: "b.go"},
			{Kind: OpAdd, Path: "c.go"},
			{Kind: OpDelete, Path: "a.go"},
		}
		gomega.Expect(OpPaths(ops)).To(gomega.Equal([]string{"a.go", "b.go", "c.go"}))
	})
})
