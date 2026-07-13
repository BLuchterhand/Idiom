package codingrules

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	rulescontract "idiom/internal/contracts/codingrules"
)

var _ = ginkgo.Describe("Coding rules CLI", func() {
	const wrapperSource = `package sample

func target(v string) string { return v }

func Wrapper(v string) string {
	return target(v)
}
`

	ginkgo.It("reports pass-through wrappers as JSON", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeCodingRulesTestFile(root, "wrapper.go", wrapperSource)

		var stdout, stderr bytes.Buffer
		code := RunWithTelemetry([]string{"check", "--project-root", root, "--json"}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(0), stderr.String())

		var report rulescontract.Report
		gomega.Expect(json.Unmarshal(stdout.Bytes(), &report)).To(gomega.Succeed())
		gomega.Expect(report.Findings).To(gomega.HaveLen(1))
		gomega.Expect(report.Findings[0].RuleID).To(gomega.Equal(rulescontract.RuleNoPassThroughWrapper))
	})

	ginkgo.It("uses the requested fail-on threshold", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeCodingRulesTestFile(root, "wrapper.go", wrapperSource)

		var stdout, stderr bytes.Buffer
		code := RunWithTelemetry([]string{"check", "--project-root", root, "--fail-on", "warning"}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(3), stdout.String(), stderr.String())
		gomega.Expect(stdout.String()).To(gomega.ContainSubstring("go.no_pass_through_wrapper"))
	})

	ginkgo.It("rejects an unknown fail-on threshold", func() {
		var stdout, stderr bytes.Buffer
		code := RunWithTelemetry([]string{"check", "--project-root", ginkgo.GinkgoT().TempDir(), "--fail-on", "always"}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(2))
		gomega.Expect(stderr.String()).To(gomega.ContainSubstring("--fail-on"))
	})
})

func writeCodingRulesTestFile(root, name, body string) {
	gomega.Expect(os.WriteFile(filepath.Join(root, name), []byte(body), 0o644)).To(gomega.Succeed())
}
