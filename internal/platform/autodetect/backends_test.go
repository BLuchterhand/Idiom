package autodetect

import (
	"os"
	"path/filepath"
	"runtime"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("Backend autodetection", func() {
	ginkgo.BeforeEach(func() {
		if runtime.GOOS == "windows" {
			ginkgo.Skip("backend autodetection currently targets Unix hosts")
		}
	})

	ginkgo.It("finds a backend executable on PATH", func() {
		dir := ginkgo.GinkgoT().TempDir()
		stub := filepath.Join(dir, "mlx_lm.server")
		gomega.Expect(os.WriteFile(stub, []byte("#!/bin/sh\n"), 0o755)).To(gomega.Succeed())
		ginkgo.GinkgoT().Setenv("PATH", dir)

		backends := DetectBackends()
		gomega.Expect(backends).NotTo(gomega.BeEmpty())
		var detected *Backend
		for index := range backends {
			if backends[index].BinaryName == "mlx_lm.server" {
				detected = &backends[index]
				break
			}
		}
		gomega.Expect(detected).NotTo(gomega.BeNil())
		gomega.Expect(detected.Kind).To(gomega.Equal("mlx"))
		gomega.Expect(detected.BinaryPath).To(gomega.Equal(stub))
	})

	ginkgo.It("returns a non-nil empty slice when PATH has no candidates", func() {
		ginkgo.GinkgoT().Setenv("PATH", ginkgo.GinkgoT().TempDir())
		backends := DetectBackends()
		gomega.Expect(backends).NotTo(gomega.BeNil())
		gomega.Expect(backends).To(gomega.BeEmpty())
	})

	ginkgo.It("keeps the known backend name contract stable", func() {
		gomega.Expect(KnownBackendNames()).To(gomega.ConsistOf("mlx_lm.server", "llama-server", "ollama"))
	})

	ginkgo.It("preserves backend preference order", func() {
		dir := ginkgo.GinkgoT().TempDir()
		for _, name := range []string{"mlx_lm.server", "llama-server", "ollama"} {
			gomega.Expect(os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"), 0o755)).To(gomega.Succeed())
		}
		ginkgo.GinkgoT().Setenv("PATH", dir)

		backends := DetectBackends()
		gomega.Expect(backends).To(gomega.HaveLen(3))
		gomega.Expect([]string{
			backends[0].BinaryName,
			backends[1].BinaryName,
			backends[2].BinaryName,
		}).To(gomega.Equal([]string{"mlx_lm.server", "llama-server", "ollama"}))
	})
})
