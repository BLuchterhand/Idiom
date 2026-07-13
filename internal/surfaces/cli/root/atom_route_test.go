package root

import (
	"bytes"
	"os"
	"path/filepath"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("Atom command routing", func() {
	ginkgo.It("routes atom intake through the root command", func() {
		root := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.WriteFile(filepath.Join(root, "foo.go"), []byte("package main\n"), 0o644)).To(gomega.Succeed())

		specPath := filepath.Join(root, "spec.md")
		gomega.Expect(os.WriteFile(specPath, []byte("# Atom\n\nStatus: planned\n\n## Scope\n\n- `foo.go`\n"), 0o644)).To(gomega.Succeed())

		var stdout, stderr bytes.Buffer
		code := runMain([]string{"atom", "intake", "--spec", specPath, "--project-root", root, "--json"}, &stdout, &stderr)

		gomega.Expect(code).To(gomega.Equal(0), "stderr=%s stdout=%s", stderr.String(), stdout.String())
	})
})
