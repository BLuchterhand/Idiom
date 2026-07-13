package lane

import (
	"bytes"
	"fmt"
	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"os"
	"path/filepath"
	"time"
)

var _ = ginkgo.Describe("staleness uncommitted", func() {
	ginkgo.It("check brief staleness fails on uncommitted modification", func() {
		root := initGitRepoForStaleness()
		gitCommit(root, "src/foo.go", "package src\n", "initial")

		briefMtime := time.Now().Add(-2 * time.Hour)
		briefBody := "# Test\n\n## Files To Modify\n\n| File | Change |\n|------|--------|\n| `src/foo.go` | edit |\n"
		briefPath := writeBriefFile(root, "brief.md", briefBody, briefMtime)

		full := filepath.Join(root, "src/foo.go")
		gomega.Expect(os.WriteFile(full, []byte("package src\n\nfunc Touched() {}\n"), 0o644)).To(gomega.Succeed())

		var stderr bytes.Buffer

		err := checkBriefStaleness(briefPath, root, false, &stderr)
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprintf("expected stale-brief error from uncommitted mtime change, got nil"))
		gomega.Expect(err.Error()).To(gomega.ContainSubstring("src/foo.go"), fmt.Sprintf("error should name the stale path, got %v", err))

	})
})
