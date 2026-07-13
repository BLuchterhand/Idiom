package localops

import (
	"encoding/json"
	"os"
	"path/filepath"

	"idiom/internal/capabilities/workspace"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Quickstart evidence", func() {
	It("requires stable packet identifiers", func() {
		packet := PacketEvidence(workspace.ToolResult{}, map[string]any{
			"pack_id":         "pack-1",
			"index_id":        "index-1",
			"fresh":           true,
			"candidate_files": []any{map[string]any{"path": "main.go"}},
		}, "localize quickstart")
		Expect(packet.Status).To(Equal("pass"))
		Expect(packet.PacketID).To(Equal("pack-1"))
		Expect(packet.FreshnessState).To(Equal("fresh"))

		blocked := PacketEvidence(workspace.ToolResult{}, map[string]any{}, "localize quickstart")
		Expect(blocked.Status).To(Equal("blocked"))
		Expect(blocked.BlockerReason).NotTo(BeEmpty())
	})

	It("removes only context-view artifacts during cleanup", func() {
		root := GinkgoT().TempDir()
		viewDir := filepath.Join(root, ".idiom", "comprehension", "views")
		Expect(os.MkdirAll(viewDir, 0o755)).To(Succeed())
		viewPath := filepath.Join(viewDir, "pack.json")
		Expect(os.WriteFile(viewPath, []byte("{}"), 0o644)).To(Succeed())

		cleanup := QuickstartCleanupEvidence(viewPath, "remove-smoke-artifacts", "pack-1", root, true)
		Expect(cleanup.Status).To(Equal("pass"))
		Expect(cleanup.RemovedArtifactCount).To(Equal(1))
		_, err := os.Stat(viewPath)
		Expect(os.IsNotExist(err)).To(BeTrue())

		outside := QuickstartCleanupEvidence(filepath.Join(root, "README.md"), "remove-smoke-artifacts", "pack-1", root, true)
		Expect(outside.Status).To(Equal("blocked"))
		Expect(outside.BlockerReason).NotTo(BeEmpty())
	})

	It("retains local state under report-only cleanup", func() {
		root := GinkgoT().TempDir()
		stateDir := filepath.Join(root, ".idiom", "comprehension")
		Expect(os.MkdirAll(stateDir, 0o755)).To(Succeed())

		cleanup := QuickstartCleanupEvidence("", "report-only", "pack-1", root, true)
		Expect(cleanup.Status).To(Equal("retained_by_policy"))
		Expect(cleanup.ExitStatus).To(Equal("not_run"))
		Expect(cleanup.RetainedStatePolicy).NotTo(BeEmpty())
		_, err := os.Stat(stateDir)
		Expect(err).NotTo(HaveOccurred())
	})

	It("bounds and sanitizes suggested verification commands", func() {
		commands := SuggestedVerificationCommands(map[string]any{
			"tests": []any{
				map[string]any{"command": "go test ./..."},
				map[string]any{"command": "API_TOKEN=abc go test ./internal/runtime/localops"},
				map[string]any{"command": "go test ./cmd/idiom"},
				map[string]any{"command": "go test ./internal/runtime/agent"},
			},
		})
		Expect(commands).To(Equal([]string{
			"go test ./...",
			"[redacted] go test ./internal/runtime/localops",
			"go test ./cmd/idiom",
		}))
		Expect(VerificationLabel(commands[0])).To(Equal("go-test"))
	})

	It("blocks and redacts forbidden product evidence", func() {
		smoke := QuickstartSmoke{
			Status: "pass",
			ProductEvidenceSafeOutput: ActivationProductEvidence{
				Status:                 "pending",
				ForbiddenFieldCheck:    "pending",
				RedactionStatus:        "pending",
				P7_11Linkage:           "deferred_to_p7.11_export",
				P9_6Linkage:            "activation_report_input",
				HostedTelemetryDefault: false,
			},
			FirstUsefulPacket: ActivationPacketEvidence{
				Task: ActivationTaskEvidence{RedactionStatus: "hashed"},
			},
			PrivacyRedactionStatus: ActivationPrivacyEvidence{
				Status:              "pass",
				ForbiddenFieldCheck: "pass",
			},
		}
		FinalizeQuickstartProductEvidence(&smoke)
		Expect(smoke.ProductEvidenceSafeOutput.Status).To(Equal("pass"))
		Expect(smoke.ProductEvidenceSafeOutput.ArtifactHash).NotTo(BeEmpty())
		expectedHash, err := quickstartProductEvidenceHash(smoke)
		Expect(err).NotTo(HaveOccurred())
		Expect(smoke.ProductEvidenceSafeOutput.ArtifactHash).To(Equal(expectedHash))

		smoke.FirstUsefulPacket.PacketID = "/home/example/private-repo"
		FinalizeQuickstartProductEvidence(&smoke)
		Expect(smoke.Status).To(Equal("blocked"))
		Expect(smoke.ProductEvidenceSafeOutput.ForbiddenFieldCheck).NotTo(Equal("pass"))
		expectedHash, err = quickstartProductEvidenceHash(smoke)
		Expect(err).NotTo(HaveOccurred())
		Expect(smoke.ProductEvidenceSafeOutput.ArtifactHash).To(Equal(expectedHash))

		body, err := json.Marshal(smoke)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(body)).NotTo(ContainSubstring("/home/example/private-repo"))
	})
})
