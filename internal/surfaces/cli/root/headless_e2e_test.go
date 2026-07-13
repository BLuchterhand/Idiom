package root

import (
	"bytes"
	"path/filepath"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("Headless CLI", ginkgo.Label("e2e"), func() {
	ginkgo.It("rejects an invalid session before reaching the model", func() {
		t := ginkgo.GinkgoT()
		root := t.TempDir()
		configPath := writeMainConfigRoot(t, root)

		var stdout, stderr bytes.Buffer
		code := RunMain([]string{
			"run",
			"--config", configPath,
			"--prompt", "should not reach model",
			"--session", "../escape",
		}, &stdout, &stderr, Options{})

		gomega.Expect(code).To(gomega.Equal(2), "stdout=%s stderr=%s", stdout.String(), stderr.String())
		gomega.Expect(stderr.String()).To(gomega.ContainSubstring("invalid run id"))

		matches, err := filepath.Glob(filepath.Join(root, ".idiom", "runs", "*.completion.json"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(matches).To(gomega.BeEmpty())
	})
})
