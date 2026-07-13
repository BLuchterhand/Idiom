package intentloop

import (
	"strings"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("Cycle prompts", func() {
	ginkgo.It("builds, saves, and reloads the bounded prompt with a stable hash", func() {
		root := ginkgo.GinkgoT().TempDir()
		originalPrompt := `Implement the feature.

Typed plan execution contract:
{"atoms":[{"atom_id":"impl","required_tests":[{"command":"go test ./..."}]}]}`
		state := NewState("session-1", originalPrompt, IntentLoopContract{}, 5, testNow())
		state.Cycles = []CycleRecord{{
			CycleNumber:        1,
			Status:             "completed",
			ChangedPaths:       []string{"internal/foo/foo.go"},
			SourceSanityPassed: true,
			RemainingFailures: []EvidenceGap{{
				Requirement: "verification:go vet ./...",
				Status:      "failed",
				Detail:      strings.Repeat("x", maxPromptGapDetailRunes+20),
				DetailHash:  sha256Prefixed(strings.Repeat("x", maxPromptGapDetailRunes+20)),
			}},
		}}

		prompt := BuildCyclePrompt(originalPrompt, state)
		for _, want := range []string{
			"## Original Intent",
			"Typed plan execution contract:",
			"## Prior Cycles",
			"## Remaining Requirements",
			"## Instructions",
			"detail_hash: `sha256:",
		} {
			gomega.Expect(prompt).To(gomega.ContainSubstring(want))
		}
		gomega.Expect(prompt).NotTo(gomega.ContainSubstring(strings.Repeat("x", maxPromptGapDetailRunes+1)))

		relPath, hash, err := SaveCyclePrompt(root, state.SessionID, 2, prompt)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(hash).To(gomega.Equal(sha256Hex(prompt)))
		loaded, err := LoadCyclePrompt(root, relPath, hash)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(loaded).To(gomega.Equal(prompt))
	})
})

func testNow() time.Time {
	return time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
}
