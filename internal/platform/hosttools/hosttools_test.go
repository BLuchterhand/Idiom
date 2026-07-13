package hosttools

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("Host tools", func() {
	ginkgo.BeforeEach(func() {
		if runtime.GOOS == "windows" {
			ginkgo.Skip("executable fixtures use POSIX mode bits")
		}
	})

	ginkgo.It("selects the first named tool using PATH order", func() {
		firstDir := ginkgo.GinkgoT().TempDir()
		secondDir := ginkgo.GinkgoT().TempDir()
		writeExecutableHostTool(filepath.Join(secondDir, "tool-a"), "#!/bin/sh\nexit 0\n")
		writeExecutableHostTool(filepath.Join(firstDir, "tool-b"), "#!/bin/sh\nexit 0\n")
		writeExecutableHostTool(filepath.Join(secondDir, "tool-b"), "#!/bin/sh\nexit 0\n")
		ginkgo.GinkgoT().Setenv("PATH", firstDir+string(os.PathListSeparator)+secondDir)

		gomega.Expect(FirstAvailable("missing-tool", "tool-b", "tool-a")).To(gomega.Equal(filepath.Join(firstDir, "tool-b")))
	})

	ginkgo.It("captures merged version output and rejects failed commands", func(ctx ginkgo.SpecContext) {
		stdoutTool := writeExecutableHostTool(
			filepath.Join(ginkgo.GinkgoT().TempDir(), "stdout-tool"),
			"#!/bin/sh\nprintf '%s\\n' 'tool 1.0'\n",
		)
		output, err := VersionOutput(ctx, stdoutTool, 1024)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(strings.TrimSpace(output)).To(gomega.Equal("tool 1.0"))

		stderrTool := writeExecutableHostTool(
			filepath.Join(ginkgo.GinkgoT().TempDir(), "stderr-tool"),
			"#!/bin/sh\nprintf '%s\\n' 'tool 2.0' >&2\n",
		)
		output, err = VersionOutput(ctx, stderrTool, 1024)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(strings.TrimSpace(output)).To(gomega.Equal("tool 2.0"))

		failingTool := writeExecutableHostTool(
			filepath.Join(ginkgo.GinkgoT().TempDir(), "failing-tool"),
			"#!/bin/sh\nprintf '%s\\n' 'tool 3.0'\nexit 2\n",
		)
		_, err = VersionOutput(ctx, failingTool, 1024)
		gomega.Expect(err).To(gomega.HaveOccurred())
	})

	ginkgo.It("matches version markers case-insensitively", func(ctx ginkgo.SpecContext) {
		tool := writeExecutableHostTool(
			filepath.Join(ginkgo.GinkgoT().TempDir(), "ctags"),
			"#!/bin/sh\nprintf '%s\\n' 'Universal Ctags 6.0'\n",
		)

		gomega.Expect(VersionOutputContains(ctx, tool, "universal ctags")).To(gomega.BeTrue())
		gomega.Expect(VersionOutputContains(ctx, tool, "gnu grep")).To(gomega.BeFalse())
	})
})

func writeExecutableHostTool(path, body string) string {
	gomega.Expect(os.WriteFile(path, []byte(body), 0o755)).To(gomega.Succeed())
	return path
}
