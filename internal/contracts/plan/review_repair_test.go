package plan

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Review-repair terminal answers", func() {
	DescribeTable("recognizes only terminal pass shapes",
		func(text string, expected bool) {
			Expect(ReviewRepairAnswerDeclaresPass(text)).To(Equal(expected))
		},
		Entry("decision first", "Decision: pass\n\nVerification Checklist:\n- ok", true),
		Entry("checklist first", "Verification Checklist:\n- ok\n\nDecision: pass", true),
		Entry("template prose before decision", "Your visible response must use exactly one of these shapes.\nDecision: pass\n\nVerification Checklist:\n- ok", false),
		Entry("embedded decision", "I will answer Decision: pass after review.\nVerification Checklist:\n- ok", false),
	)

	DescribeTable("recognizes the owned checklist heading",
		func(text string, expected bool) {
			Expect(ReviewRepairTextHasChecklist(text)).To(Equal(expected))
		},
		Entry("heading", "Decision: pass\n\nVerification Checklist:\n- ok", true),
		Entry("serialized prefix", "Verification Checklist: - checked fact", true),
		Entry("generic heading", "Checklist:\n- ok", false),
	)

	It("keeps terminal templates parser-readable and non-copyable", func() {
		templates := ReviewRepairTerminalTemplates()
		Expect(templates).To(And(
			ContainSubstring("Decision: pass"),
			ContainSubstring("Verification Checklist:"),
			ContainSubstring("parser-readable submit_plan object"),
			ContainSubstring("<<QUESTION>>:"),
		))
		Expect(templates).NotTo(ContainSubstring("cite a checked fact"))
	})

	DescribeTable("detects placeholder checklist lines",
		func(line string, expected bool) {
			Expect(ReviewRepairLineIsPlaceholderChecklist(line)).To(Equal(expected))
		},
		Entry("required behavior placeholder", "- Required behavior: cite a checked fact from Phase/Diff Evidence.", true),
		Entry("scope safety placeholder", "- Scope safety: cite a checked fact from changed paths and allowed repair paths.", true),
		Entry("test evidence placeholder", "- Test or command evidence: cite a checked fact, or say it is pending outside this lane.", true),
		Entry("concrete fact", "- Required behavior: src/app.go now returns the requested label in line 12.", false),
	)
})
