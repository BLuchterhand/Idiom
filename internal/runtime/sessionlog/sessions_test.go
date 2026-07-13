package sessionlog

import (
	"os"
	"path/filepath"

	"idiom/internal/platform/config"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Recorded session summaries", func() {
	DescribeTable("normalizes session IDs from paths",
		func(path string, want string) {
			Expect(SessionIDFromPath(path)).To(Equal(want))
		},
		Entry("absent", "", ""),
		Entry("whitespace", "   ", ""),
		Entry("conversation log", " logs/conversations/session-1.jsonl ", "session-1"),
		Entry("plain identifier", " session-2 ", "session-2"),
	)

	It("redacts sensitive prompts while preserving benign summaries", func() {
		dir := GinkgoT().TempDir()
		cfg := config.Config{Chat: config.Chat{DebugLogDir: dir}}

		writeSessionLog(dir, "20260419-090000.000", []string{
			`{"type":"message","role":"user","content":"summarize the repository"}`,
			`{"type":"message","role":"assistant","content":"ok"}`,
		})
		writeSessionLog(dir, "20260419-100000.000", []string{
			`{"type":"message","role":"user","content":"raw_prompt: customer-acme token=abc123 /Users/alice/private/repo"}`,
			`{"type":"message","role":"assistant","content":"ok"}`,
		})

		sessions, err := ListRecordedSessions(cfg)
		Expect(err).NotTo(HaveOccurred())
		Expect(sessions).To(HaveLen(2))
		Expect(sessions[0].FirstPrompt).To(Equal("[redacted prompt summary]"))
		Expect(sessions[1].FirstPrompt).To(Equal("summarize the repository"))
	})

	DescribeTable("redacts sensitivity markers",
		func(input string) {
			Expect(SafePromptSummary(input, 80)).To(Equal("[redacted prompt summary]"))
		},
		Entry("raw prompt label", "raw_prompt: summarize the private repo"),
		Entry("token assignment", "token=abc123"),
		Entry("customer identifier", "customer-acme onboarding"),
		Entry("private absolute path", "open /Users/alice/private/repo/main.go"),
	)
})

func writeSessionLog(dir string, id string, lines []string) {
	path := filepath.Join(dir, id+".jsonl")
	data := []byte{}
	for _, line := range lines {
		data = append(data, line...)
		data = append(data, '\n')
	}

	Expect(os.WriteFile(path, data, 0o600)).To(Succeed())
}
