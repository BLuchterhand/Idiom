package atom

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	atomintakecontract "idiom/internal/contracts/atomintake"
	"idiom/internal/workflows/atomintake"
)

var _ = ginkgo.Describe("Atom intake CLI", func() {
	ginkgo.It("emits a dispatchable workflow report as JSON", func() {
		root := ginkgo.GinkgoT().TempDir()
		specPath := writeAtomSpec(root, "spec.md", "# Atom\n\nStatus: planned\n\n## Scope\n\n- `foo.go`\n")
		gomega.Expect(os.WriteFile(filepath.Join(root, "foo.go"), []byte("package main\n"), 0o644)).To(gomega.Succeed())

		var stdout, stderr bytes.Buffer
		code := RunIntakeMain([]string{"--spec", specPath, "--project-root", root, "--json"}, &stdout, &stderr, atomintake.Dependencies{})
		gomega.Expect(code).To(gomega.Equal(0), stderr.String(), stdout.String())

		var report atomintake.IntakeReport
		gomega.Expect(json.Unmarshal(stdout.Bytes(), &report)).To(gomega.Succeed())
		gomega.Expect(report.State).To(gomega.Equal(atomintakecontract.StateDispatchable))
		gomega.Expect(report.Dispatchable).To(gomega.BeTrue())
	})

	ginkgo.It("renders blocking workflow state in text output", func() {
		root := ginkgo.GinkgoT().TempDir()
		specPath := writeAtomSpec(root, "spec.md", "# Atom\n\nStatus: accepted\n")

		var stdout, stderr bytes.Buffer
		code := RunIntakeMain([]string{"--spec", specPath, "--project-root", root}, &stdout, &stderr, atomintake.Dependencies{})
		gomega.Expect(code).NotTo(gomega.Equal(0))
		gomega.Expect(stdout.String()).To(gomega.And(
			gomega.ContainSubstring("state: complete"),
			gomega.ContainSubstring("dispatchable: false"),
		))
	})
})

func writeAtomSpec(root, name, body string) string {
	path := filepath.Join(root, name)
	gomega.Expect(os.WriteFile(path, []byte(body), 0o644)).To(gomega.Succeed())
	return path
}
