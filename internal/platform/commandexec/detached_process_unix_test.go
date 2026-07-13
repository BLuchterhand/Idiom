//go:build darwin || linux

package commandexec

import (
	"os"
	"path/filepath"
	"syscall"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	"idiom/internal/platform/config"
)

var _ = ginkgo.Describe("Detached command execution", ginkgo.Label("integration"), func() {
	ginkgo.It("starts a detached process in its own process group with an owned log", func(ctx ginkgo.SpecContext) {
		logPath := filepath.Join(ginkgo.GinkgoT().TempDir(), "detached.log")
		spec := helperSpec("log-and-sleep")
		spec.OutputLogPath = logPath
		spec.Timeout = 0
		pid, err := StartDetached(ctx, spec)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		ginkgo.DeferCleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })

		pgid, err := syscall.Getpgid(pid)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(pgid).To(gomega.Equal(pid))
		waitForFileContent(logPath, "detached-ready")
	})

	ginkgo.It("starts a detached process with the configured directory, environment, and canonical PWD", func(ctx ginkgo.SpecContext) {
		dir := ginkgo.GinkgoT().TempDir()
		logPath := filepath.Join(ginkgo.GinkgoT().TempDir(), "detached-context.log")
		spec := helperSpecWithDir(dir, "detached-context")
		spec.OutputLogPath = logPath
		spec.Timeout = 0
		spec.Env = append(spec.Env, "IDIOM_DETACHED_VALUE=from-env")
		pid, err := StartDetached(ctx, spec)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		ginkgo.DeferCleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })

		waitForFileContent(logPath, "env=from-env")
		body, err := os.ReadFile(logPath)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(string(body)).To(gomega.ContainSubstring("pwd=" + config.CanonicalizeExistingPath(dir)))
	})
})
