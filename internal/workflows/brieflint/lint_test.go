package brieflint

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("Brief linting", func() {
	ginkgo.It("embeds the preflight script with its usage anchor", func() {
		script, err := preflightFS.ReadFile("brief-preflight.py")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(string(script)).To(gomega.ContainSubstring("brief-preflight.py <brief.md>"))
	})

	ginkgo.DescribeTable("categorizes findings by diagnostic wording",
		func(raw string, want Finding) {
			gomega.Expect(ParseFindings("/b.md", raw)).To(gomega.Equal([]Finding{want}))
		},
		ginkgo.Entry("anchor specificity block", "idiom brief preflight: FAIL /b.md\n- exact-anchor specificity failed for x.go.\n", Finding{Severity: "BLOCK", Category: "anchor-specificity", Brief: "/b.md", Message: "exact-anchor specificity failed for x.go."}),
		ginkgo.Entry("missing current state warning", "- WARN: no obvious verified-current-state section found.\n", Finding{Severity: "WARN", Category: "missing-current-state", Brief: "/b.md", Message: "no obvious verified-current-state section found."}),
		ginkgo.Entry("exit-zero branching warning", "idiom brief preflight: OK /b.md\nidiom brief preflight: WARN branching language in Required Edit; replace with one unconditional edit\n", Finding{Severity: "WARN", Category: "branching-language", Brief: "/b.md", Message: "branching language in Required Edit; replace with one unconditional edit"}),
		ginkgo.Entry("informational finding", "- INFO: noteworthy detail\n", Finding{Severity: "INFO", Category: "other", Brief: "/b.md", Message: "noteworthy detail"}),
		ginkgo.Entry("unfilled placeholder", "- some unfilled placeholder somewhere\n", Finding{Severity: "BLOCK", Category: "unfilled-placeholder", Brief: "/b.md", Message: "some unfilled placeholder somewhere"}),
		ginkgo.Entry("unknown blocking diagnostic", "- something nobody planned for\n", Finding{Severity: "BLOCK", Category: "other", Brief: "/b.md", Message: "something nobody planned for"}),
	)

	ginkgo.It("runs the embedded preflight from the workspace directory", func() {
		if _, err := exec.LookPath("python3"); err != nil {
			ginkgo.Skip("python3 not available on PATH")
		}

		workspace := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.Mkdir(filepath.Join(workspace, ".git"), 0o755)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(workspace, "foo.go"), []byte("package main\n"), 0o644)).To(gomega.Succeed())

		briefPath := filepath.Join(workspace, "brief.md")
		body := "# Brief\n\n" +
			"## Files to Modify\n\n" +
			"| File | Change |\n" +
			"|---|---|\n" +
			"| `foo.go:1` | Read module declaration. |\n\n" +
			"## Verified Anchors\n\n" +
			"- `foo.go:1`: package main\n\n" +
			"## Required Edits\n\n" +
			"Read the file without changing it.\n\n" +
			"## Acceptance\n\n" +
			"- `go test ./...` succeeds.\n"
		gomega.Expect(os.WriteFile(briefPath, []byte(body), 0o644)).To(gomega.Succeed())

		result, err := RunEmbeddedPreflight(context.Background(), briefPath, workspace, 0)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(result.Err).NotTo(gomega.HaveOccurred(), result.Stdout, result.Stderr)
	})
})
