package ranker

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"idiom/internal/repository/comprehension"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("Ranker CLI", func() {
	ginkgo.It("restores weights from a learning event", func() {
		root := ginkgo.GinkgoT().TempDir()
		configPath := filepath.Join(root, "config.json")
		gomega.Expect(os.WriteFile(
			configPath,
			[]byte(fmt.Sprintf(`{"workspace":{"root":%q}}`, root)),
			0o644,
		)).To(gomega.Succeed())

		event, weights, err := comprehension.ApplyVerifiedRankerLearning(ginkgo.GinkgoT().Context(), root, comprehension.VerifiedRankerLearningRequest{
			Source:   "eval",
			Action:   "boost",
			Files:    []string{"internal/runtime/agent/runner.go"},
			Score:    0.9,
			Verified: true,
			VerificationEvidence: &comprehension.VerificationEvidence{
				EditPaths:           []string{"internal/runtime/agent/runner.go"},
				VerificationCommand: "go test ./...",
				VerificationPassed:  true,
			},
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(weights["internal/runtime/agent/runner.go"]).To(gomega.BeNumerically(">", comprehension.LearnedWeightBaseline))
		var stdout, stderr bytes.Buffer

		gomega.Expect(Run(
			[]string{"rollback", "--config", configPath, "--event", event.EventID},
			&stdout,
			&stderr,
			nil,
			Deps{},
		)).To(gomega.Equal(0), "stderr=%s stdout=%s", stderr.String(), stdout.String())
		gomega.Expect(stdout.String()).To(gomega.ContainSubstring("ranker weights restored from learning event " + event.EventID))

		restored, err := comprehension.LoadLearnedWeights(ginkgo.GinkgoT().Context(), root)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(restored).To(gomega.BeEmpty())
		entries, err := comprehension.ReadLearningLog(ginkgo.GinkgoT().Context(), root)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		summary := comprehension.SummarizeLearningLog(entries)
		gomega.Expect(summary.RestoreCount).To(gomega.Equal(1))
		gomega.Expect(summary.AcceptedReasonCounts[comprehension.LearningReasonRollbackRestore]).To(gomega.Equal(1))
	})

	ginkgo.It("sanitizes a manual note before recording it", func() {
		root := ginkgo.GinkgoT().TempDir()
		configPath := filepath.Join(root, "config.json")
		gomega.Expect(os.WriteFile(
			configPath,
			[]byte(fmt.Sprintf(`{"workspace":{"root":%q}}`, root)),
			0o644,
		)).To(gomega.Succeed())
		sentinel := "raw_prompt BEGIN OPENSSH PRIVATE KEY transcript protected content customer freeform"
		var stdout, stderr bytes.Buffer

		gomega.Expect(Run([]string{
			"boost",
			"--config", configPath,
			"--note", sentinel,
			"internal/runtime/agent/runner.go",
		}, &stdout, &stderr, nil, Deps{})).To(gomega.Equal(0), "stderr=%s stdout=%s", stderr.String(), stdout.String())

		data, err := os.ReadFile(filepath.Join(root, ".idiom", "ranker", "learning_log.jsonl"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		body := strings.ToLower(string(data))
		gomega.Expect(body).NotTo(gomega.ContainSubstring("raw_prompt"))
		gomega.Expect(body).NotTo(gomega.ContainSubstring("begin openssh"))
		gomega.Expect(body).NotTo(gomega.ContainSubstring("transcript"))
		gomega.Expect(body).NotTo(gomega.ContainSubstring("protected content"))
		gomega.Expect(body).NotTo(gomega.ContainSubstring("customer freeform"))
		gomega.Expect(body).To(gomega.ContainSubstring(`"note":"` + comprehension.LearningLogNotePresent + `"`))

		entries, err := comprehension.ReadLearningLog(ginkgo.GinkgoT().Context(), root)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(entries).To(gomega.HaveLen(1))
		gomega.Expect(entries[0].Note).To(gomega.Equal(comprehension.LearningLogNotePresent))
		summary := comprehension.SummarizeLearningLog(entries)
		gomega.Expect(summary.BoostCount).To(gomega.Equal(1))
		gomega.Expect(summary.BySource["cli"]).To(gomega.Equal(1))
	})

	ginkgo.It("uses request id to make an ambiguous manual retry idempotent", func() {
		root := ginkgo.GinkgoT().TempDir()
		configPath := filepath.Join(root, "config.json")
		gomega.Expect(os.WriteFile(
			configPath,
			[]byte(fmt.Sprintf(`{"workspace":{"root":%q}}`, root)),
			0o644,
		)).To(gomega.Succeed())
		args := []string{
			"boost",
			"--config", configPath,
			"--request-id", "cli-ambiguous-retry",
			"internal/runtime/agent/runner.go",
		}

		for range 2 {
			var stdout, stderr bytes.Buffer
			gomega.Expect(Run(args, &stdout, &stderr, nil, Deps{})).To(gomega.Equal(0), "stderr=%s", stderr.String())
		}
		snapshot, err := comprehension.LoadRankerStateSnapshot(context.Background(), root)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(snapshot.LearningEntries()).To(gomega.HaveLen(1))
		gomega.Expect(snapshot.LearnedWeights()["internal/runtime/agent/runner.go"]).To(gomega.BeNumerically("~", 1.15, 1e-12))

		var stdout, stderr bytes.Buffer
		gomega.Expect(Run([]string{
			"boost",
			"--config", configPath,
			"--request-id", "cli-ambiguous-retry",
			"different.go",
		}, &stdout, &stderr, nil, Deps{})).To(gomega.Equal(1))
		gomega.Expect(stderr.String()).To(gomega.ContainSubstring("already used for different canonical content"))
	})

	ginkgo.It("show fails closed on a forged learning event", func() {
		root := ginkgo.GinkgoT().TempDir()
		configPath := filepath.Join(root, "config.json")
		gomega.Expect(os.WriteFile(
			configPath,
			[]byte(fmt.Sprintf(`{"workspace":{"root":%q}}`, root)),
			0o644,
		)).To(gomega.Succeed())
		_, _, err := comprehension.ApplyManualRankerNudge(ginkgo.GinkgoT().Context(), root, "boost", []string{"forged.go"}, "", "forged-show")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		path := filepath.Join(root, ".idiom", "ranker", "learning_log.jsonl")
		body, err := os.ReadFile(path)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		body = bytes.Replace(body, []byte(`"event_id":"ranker_`), []byte(`"event_id":"ranker_0000000000000000","discarded":"`), 1)
		gomega.Expect(os.WriteFile(path, body, 0o644)).To(gomega.Succeed())

		var stdout, stderr bytes.Buffer
		gomega.Expect(Run([]string{"show", "--config", configPath}, &stdout, &stderr, nil, Deps{})).To(gomega.Equal(1))
		gomega.Expect(stderr.String()).To(gomega.ContainSubstring("ranker show failed"))
	})

	ginkgo.It("requires an event before executing rollback", func() {
		var stdout, stderr bytes.Buffer

		gomega.Expect(Run([]string{"rollback"}, &stdout, &stderr, nil, Deps{})).To(gomega.Equal(2))
		gomega.Expect(stderr.String()).To(gomega.ContainSubstring("--event is required"))
	})
})
