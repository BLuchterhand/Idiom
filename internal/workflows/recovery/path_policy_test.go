package recovery

import (
	"os"
	"path/filepath"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	recoverycontract "idiom/internal/contracts/recovery"
)

var _ = ginkgo.Describe("Recovery artifact path policy", func() {
	ginkgo.Describe("sibling reconciliation paths", func() {
		ginkgo.It("allows a sibling of a recovery packet", func() {
			root := ginkgo.GinkgoT().TempDir()
			packetPath := filepath.Join(root, ".idiom", "recovery", "failure-1", "packet.json")
			gomega.Expect(os.MkdirAll(filepath.Dir(packetPath), 0o755)).To(gomega.Succeed())

			fullPath, relPath, ok := SafeSiblingReconciliationPath(root, packetPath)
			gomega.Expect(ok).To(gomega.BeTrue())
			gomega.Expect(relPath).To(gomega.Equal(".idiom/recovery/failure-1/reconciliation.json"))
			gomega.Expect(fullPath).To(gomega.Equal(filepath.Join(root, ".idiom", "recovery", "failure-1", "reconciliation.json")))
		})

		ginkgo.It("rejects a protected logs sibling", func() {
			root := ginkgo.GinkgoT().TempDir()
			packetPath := filepath.Join(root, "logs", "packet.json")
			gomega.Expect(os.MkdirAll(filepath.Dir(packetPath), 0o755)).To(gomega.Succeed())

			_, _, ok := SafeSiblingReconciliationPath(root, packetPath)
			gomega.Expect(ok).To(gomega.BeFalse())
		})

		ginkgo.It("rejects a packet outside the workspace", func() {
			root := ginkgo.GinkgoT().TempDir()
			outside := ginkgo.GinkgoT().TempDir()

			_, _, ok := SafeSiblingReconciliationPath(root, filepath.Join(outside, "packet.json"))
			gomega.Expect(ok).To(gomega.BeFalse())
		})
	})

	ginkgo.Describe("safe packet loading", func() {
		ginkgo.It("rejects a symlinked packet path", func() {
			root := ginkgo.GinkgoT().TempDir()
			target := filepath.Join(root, ".idiom", "recovery", "failure-1", "packet.json")
			gomega.Expect(os.MkdirAll(filepath.Dir(target), 0o755)).To(gomega.Succeed())
			gomega.Expect(os.WriteFile(target, []byte("{}"), 0o600)).To(gomega.Succeed())
			link := filepath.Join(root, ".idiom", "recovery", "failure-1", "packet-link.json")
			if err := os.Symlink(target, link); err != nil {
				ginkgo.Skip("symlinks unavailable: " + err.Error())
			}

			_, _, err := SafePacketPath(root, link)
			gomega.Expect(err).To(gomega.HaveOccurred())
		})

		ginkgo.It("rejects an outside-root packet before reading it", func() {
			root := ginkgo.GinkgoT().TempDir()
			outsidePacket := filepath.Join(ginkgo.GinkgoT().TempDir(), "packet.json")
			body := []byte(`{"schema_version":"` + recoverycontract.DecompositionPacketSchemaVersion + `"}`)
			gomega.Expect(os.WriteFile(outsidePacket, body, 0o600)).To(gomega.Succeed())

			_, _, _, err := TryLoadSafeDecompositionPacket(root, outsidePacket)
			gomega.Expect(err).To(gomega.HaveOccurred())
		})

		ginkgo.It("rejects a symlinked packet before loading it", func() {
			root := ginkgo.GinkgoT().TempDir()
			target := filepath.Join(root, ".idiom", "recovery", "failure-1", "packet.json")
			gomega.Expect(os.MkdirAll(filepath.Dir(target), 0o755)).To(gomega.Succeed())
			body := []byte(`{"schema_version":"` + recoverycontract.DecompositionPacketSchemaVersion + `"}`)
			gomega.Expect(os.WriteFile(target, body, 0o600)).To(gomega.Succeed())
			link := filepath.Join(root, ".idiom", "recovery", "failure-1", "packet-link.json")
			if err := os.Symlink(target, link); err != nil {
				ginkgo.Skip("symlinks unavailable: " + err.Error())
			}

			_, _, _, err := TryLoadSafeDecompositionPacket(root, link)
			gomega.Expect(err).To(gomega.HaveOccurred())
		})
	})

	ginkgo.It("uses the recovery artifact directory for the default reconciliation path", func() {
		root := ginkgo.GinkgoT().TempDir()
		fullPath, relPath, err := SafeDefaultReconciliationPath(root, "failure-1")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(relPath).To(gomega.Equal(".idiom/recovery/failure-1/reconciliation.json"))
		gomega.Expect(fullPath).To(gomega.Equal(filepath.Join(root, ".idiom", "recovery", "failure-1", "reconciliation.json")))
	})

	ginkgo.It("rejects a protected explicit reconciliation path", func() {
		root := ginkgo.GinkgoT().TempDir()
		packetPath := filepath.Join(root, ".idiom", "recovery", "failure-1", "packet.json")
		gomega.Expect(os.MkdirAll(filepath.Dir(packetPath), 0o755)).To(gomega.Succeed())

		_, _, err := SafeReconciliationOutputPath(root, packetPath, "failure-1", "logs/reconciliation.json")
		gomega.Expect(err).To(gomega.HaveOccurred())
	})

	ginkgo.DescribeTable("enforces decomposition artifact ownership",
		func(outputPath func(root, outside string) string, wantRel string, wantErr bool) {
			root := ginkgo.GinkgoT().TempDir()
			failureID := "failure-1"
			gomega.Expect(os.MkdirAll(filepath.Join(root, ".idiom", "recovery", failureID), 0o755)).To(gomega.Succeed())
			gomega.Expect(os.MkdirAll(filepath.Join(root, ".git"), 0o755)).To(gomega.Succeed())
			gomega.Expect(os.MkdirAll(filepath.Join(root, "logs"), 0o755)).To(gomega.Succeed())

			outside := ginkgo.GinkgoT().TempDir()
			link := filepath.Join(root, ".idiom", "recovery", failureID, "linked")
			if err := os.Symlink(outside, link); err != nil {
				ginkgo.Skip("symlinks unavailable: " + err.Error())
			}

			_, relPath, err := SafeDecompositionOutputPath(root, failureID, outputPath(root, outside))
			if wantErr {
				gomega.Expect(err).To(gomega.HaveOccurred())
				return
			}
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(relPath).To(gomega.Equal(wantRel))
		},
		ginkgo.Entry("default path", func(_, _ string) string { return "" }, ".idiom/recovery/failure-1/packet.json", false),
		ginkgo.Entry("custom recovery packet", func(_, _ string) string { return ".idiom/recovery/failure-1/custom-packet.json" }, ".idiom/recovery/failure-1/custom-packet.json", false),
		ginkgo.Entry("parent escape", func(_, _ string) string { return "../escape.json" }, "", true),
		ginkgo.Entry("absolute outside path", func(_, outside string) string { return filepath.Join(outside, "packet.json") }, "", true),
		ginkgo.Entry("protected git path", func(_, _ string) string { return ".git/packet.json" }, "", true),
		ginkgo.Entry("protected logs path", func(_, _ string) string { return "logs/packet.json" }, "", true),
		ginkgo.Entry("protected environment file", func(_, _ string) string { return ".env" }, "", true),
		ginkgo.Entry("failure control artifact", func(_, _ string) string { return ".idiom/recovery/failure-1/failure.json" }, "", true),
		ginkgo.Entry("reconciliation control artifact", func(_, _ string) string { return ".idiom/recovery/failure-1/reconciliation.json" }, "", true),
		ginkgo.Entry("symlinked ancestor", func(_, _ string) string { return ".idiom/recovery/failure-1/linked/packet.json" }, "", true),
	)
})
