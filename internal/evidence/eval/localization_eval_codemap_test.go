package eval

import (
	"context"
	"io"
	"os"
	"path/filepath"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

const codeMapHelperEnv = "IDIOM_TEST_CODEMAP_HELPER"

// init lets the test binary stand in for a code map generator: run as a helper
// it writes the map into the current directory and exits.
func init() {
	switch os.Getenv(codeMapHelperEnv) {
	case "write":
		_ = os.MkdirAll(filepath.Join("docs", "engineering"), 0o755)
		_ = os.WriteFile(filepath.Join("docs", "engineering", "code-map.md"), []byte("## core\n- a.cpp (3) Purpose\n"), 0o644)
		os.Exit(0)
	case "fail":
		os.Exit(3)
	}
}

var _ = ginkgo.Describe("localization eval code map", func() {
	var runner *localizationEvalRunner
	var worktree string

	ginkgo.BeforeEach(func() {
		worktree = ginkgo.GinkgoT().TempDir()
		runner = &localizationEvalRunner{opts: LocalizationEvalRunOptions{CacheDir: ginkgo.GinkgoT().TempDir(), Log: io.Discard, CodeMapCmd: os.Args[0]}}
	})

	ginkgo.It("moves the generated map out of the worktree and hands idiom its path", func() {
		ginkgo.GinkgoT().Setenv(codeMapHelperEnv, "write")
		gomega.Expect(runner.prepareCodeMap(context.Background(), worktree, "case1")).To(gomega.Succeed())

		gomega.Expect(runner.codeMapPath).To(gomega.Equal(filepath.Join(runner.opts.CacheDir, "codemaps", "case1.md")))
		gomega.Expect(runner.codeMapPath).To(gomega.BeAnExistingFile())
		gomega.Expect(filepath.Join(worktree, "docs", "engineering", "code-map.md")).NotTo(gomega.BeAnExistingFile())
	})

	ginkgo.It("leaves the case without a map when the generator fails", func() {
		ginkgo.GinkgoT().Setenv(codeMapHelperEnv, "fail")
		gomega.Expect(runner.prepareCodeMap(context.Background(), worktree, "case2")).To(gomega.Succeed())
		gomega.Expect(runner.codeMapPath).To(gomega.BeEmpty())
	})

	ginkgo.It("does nothing without a command", func() {
		runner.opts.CodeMapCmd = ""
		gomega.Expect(runner.prepareCodeMap(context.Background(), worktree, "case3")).To(gomega.Succeed())
		gomega.Expect(runner.codeMapPath).To(gomega.BeEmpty())
	})
})
