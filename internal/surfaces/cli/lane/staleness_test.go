package lane

import (
	"bytes"
	"fmt"
	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

func initGitRepoForStaleness() string {

	root := ginkgo.GinkgoT().TempDir()

	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "test"},
	} {
		cmd := exec.Command("git", args...)

		cmd.Dir = root

		if out, err := cmd.CombinedOutput(); err != nil {
			ginkgo.Fail(fmt.Sprintf("git %v: %v: %s", args, err, out))
		}
	}

	return root
}

func gitCommit(root, path, content, message string) {

	full := filepath.Join(root, path)
	gomega.Expect(os.MkdirAll(filepath.Dir(full), 0o755)).To(gomega.Succeed())
	gomega.Expect(os.WriteFile(full, []byte(content), 0o644)).To(gomega.Succeed())

	for _, args := range [][]string{
		{"add", path},
		{"commit", "-q", "-m", message},
	} {
		cmd := exec.Command("git", args...)

		cmd.Dir = root

		if out, err := cmd.CombinedOutput(); err != nil {
			ginkgo.Fail(fmt.Sprintf("git %v: %v: %s", args, err, out))
		}
	}
}

func writeBriefFile(dir, name, content string, mtime time.Time) string {

	p := filepath.Join(dir, name)
	gomega.Expect(os.WriteFile(p, []byte(content), 0o644)).To(gomega.Succeed())
	gomega.Expect(os.Chtimes(p, mtime, mtime)).To(gomega.Succeed())

	return p
}

var _ = ginkgo.Describe("staleness", func() {
	ginkgo.It("check brief staleness fails when commit newer than brief", func() {
		root := initGitRepoForStaleness()
		gitCommit(root, "src/foo.go", "package src\n", "initial")

		briefMtime := time.Now().Add(-2 * time.Hour)
		briefBody := "# Test\n\n## Files To Modify\n\n| File | Change |\n|------|--------|\n| `src/foo.go` | edit |\n"
		briefPath := writeBriefFile(root, "brief.md", briefBody, briefMtime)
		gitCommit(root, "src/foo.go", "package src\n\nfunc Bar() {}\n", "after-brief")

		var stderr bytes.Buffer

		err := checkBriefStaleness(briefPath, root, false, &stderr)
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprintf("expected stale-brief error, got nil"))
		gomega.Expect(err.Error()).To(gomega.ContainSubstring("src/foo.go"), fmt.Sprintf("error should name the stale path, got %v", err))

	})
	ginkgo.It("check brief staleness passes when brief newer than commit", func() {
		root := initGitRepoForStaleness()
		gitCommit(root, "src/foo.go", "package src\n", "initial")

		briefMtime := time.Now().Add(2 * time.Hour)
		briefBody := "# Test\n\n## Files To Modify\n\n| File | Change |\n|------|--------|\n| `src/foo.go` | edit |\n"
		briefPath := writeBriefFile(root, "brief.md", briefBody, briefMtime)

		var stderr bytes.Buffer
		gomega.Expect(checkBriefStaleness(briefPath, root, false, &stderr)).To(gomega.Succeed())

	})
	ginkgo.It("check brief staleness exempts new files", func() {
		root := initGitRepoForStaleness()
		gitCommit(root, "other.go", "package main\n", "seed repo")

		briefMtime := time.Now().Add(-1 * time.Hour)
		briefBody := "# Test\n\n## Files To Modify\n\n| File | Change |\n|------|--------|\n| `new/file.go` | NEW FILE |\n"
		briefPath := writeBriefFile(root, "brief.md", briefBody, briefMtime)

		var stderr bytes.Buffer
		gomega.Expect(checkBriefStaleness(briefPath, root, false, &stderr)).To(gomega.Succeed())

	})
	ginkgo.It("check brief staleness allow override downgrades to warn", func() {
		root := initGitRepoForStaleness()
		gitCommit(root, "src/foo.go", "package src\n", "initial")

		briefMtime := time.Now().Add(-2 * time.Hour)
		briefBody := "# Test\n\n## Files To Modify\n\n| File | Change |\n|------|--------|\n| `src/foo.go` | edit |\n"
		briefPath := writeBriefFile(root, "brief.md", briefBody, briefMtime)
		gitCommit(root, "src/foo.go", "package src\n\nfunc Bar() {}\n", "after-brief")

		var stderr bytes.Buffer
		gomega.Expect(checkBriefStaleness(briefPath, root, true, &stderr)).To(gomega.Succeed())
		gomega.Expect(stderr.String()).To(gomega.ContainSubstring("WARN brief is stale"), fmt.Sprintf("expected stderr WARN message, got: %s", stderr.String()))

	})
})
