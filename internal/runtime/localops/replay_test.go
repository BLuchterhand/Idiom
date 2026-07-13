package localops

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Local replay reports", func() {
	It("reports replay as unsupported when no logs are available", func() {
		report, err := BuildReplayReport("", "")
		Expect(err).NotTo(HaveOccurred())
		Expect(report.Status).To(Equal("unsupported"))
		Expect(report.Blockers).To(HaveLen(1))
	})

	It("summarizes metadata-only conversation logs", func() {
		path := filepath.Join(GinkgoT().TempDir(), "conversation.jsonl")
		body := `{"type":"tool_result","tool":"read_file","summary":"metadata only"}` + "\n" +
			`{"type":"turn_summary","stop_reason":"budget_exhausted","duration_ms":1000}` + "\n"
		Expect(os.WriteFile(path, []byte(body), 0o644)).To(Succeed())

		report, err := BuildReplayReport(path, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(report.Status).To(Equal("pass"))
		Expect(report.LogCount).To(Equal(1))
		Expect(report.Aggregate).NotTo(BeNil())
		Expect(report.Signals.ToolCalls).To(BeNumerically(">", 0))
		Expect(report.Signals.BudgetExhausted).To(BeNumerically(">", 0))
	})
})
