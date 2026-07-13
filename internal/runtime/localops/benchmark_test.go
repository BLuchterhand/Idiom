package localops

import (
	"encoding/json"
	"errors"
	"path/filepath"

	"idiom/internal/platform/config"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Local operations benchmark reports", func() {
	It("assembles metadata-only evidence", func() {
		root := GinkgoT().TempDir()
		GinkgoT().Chdir(root)

		report, err := BuildBenchmarkReport(BenchmarkOptions{
			ConfigPath:    filepath.Join(root, ".idiom", "config.json"),
			Profile:       "eval",
			OutputPath:    filepath.Join(root, "reports", "local-ops.json"),
			TelemetryPath: filepath.Join(root, "logs", "telemetry.jsonl"),
		}, QuickstartDeps{})
		Expect(err).NotTo(HaveOccurred())
		Expect(report.SchemaVersion).To(Equal(BenchmarkSchemaVersion))
		Expect(report.Profiles).To(HaveLen(1))
		Expect(report.Profiles[0].Name).To(Equal("eval"))
		Expect(report.Replay.Status).To(Equal("unsupported"))
		Expect(report.Replay.Blockers).NotTo(BeEmpty())
		Expect(report.Telemetry).NotTo(BeNil())
		Expect(report.Telemetry.Path).NotTo(BeEmpty())
		Expect(report.Telemetry.ID).NotTo(BeEmpty())
		Expect(report.Privacy.MetadataOnly).To(BeTrue())
		Expect(report.Privacy.RawPromptCaptured).To(BeFalse())
		Expect(report.Privacy.RawFileBodyCaptured).To(BeFalse())
		Expect(report.Privacy.SecretCaptureAllowed).To(BeFalse())

		body, err := json.Marshal(report)
		Expect(err).NotTo(HaveOccurred())
		Expect(ForbiddenEvidenceLeak(string(body))).To(BeEmpty())
	})

	It("keeps stable JSON contract keys", func() {
		report := BenchmarkReport{
			SchemaVersion: BenchmarkSchemaVersion,
			Quickstart: &QuickstartSmoke{
				ProductEvidenceSafeOutput: ActivationProductEvidence{},
				Cleanup:                   ActivationCleanupEvidence{RemovedArtifactCount: 2},
			},
		}

		body, err := json.Marshal(report)
		Expect(err).NotTo(HaveOccurred())
		for _, key := range []string{
			`"schema_version"`,
			`"quickstart_smoke"`,
			`"product_evidence_safe_output"`,
			`"removed_artifact_count"`,
		} {
			Expect(string(body)).To(ContainSubstring(key))
		}
	})

	It("records a sanitized quickstart setup failure", func() {
		root := GinkgoT().TempDir()
		GinkgoT().Chdir(root)

		report, err := BuildBenchmarkReport(BenchmarkOptions{
			ConfigPath:        filepath.Join(root, ".idiom", "config.json"),
			Profile:           "sidecar",
			QuickstartSmoke:   true,
			QuickstartTask:    "localize quickstart smoke",
			QuickstartCleanup: "report-only",
		}, QuickstartDeps{
			LoadConfig: func(string) (config.Config, error) {
				return config.Config{}, errors.New("load /home/example/CustomerX-private-repo failed")
			},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(report.Quickstart).NotTo(BeNil())
		Expect(report.Quickstart.Status).To(Equal("blocked"))
		Expect(report.Blockers).NotTo(BeEmpty())

		body, err := json.Marshal(report)
		Expect(err).NotTo(HaveOccurred())
		Expect(ForbiddenEvidenceLeak(string(body))).To(BeEmpty())
	})
})
