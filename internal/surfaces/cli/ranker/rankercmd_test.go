package ranker

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("Ranker command dispatch", func() {
	ginkgo.It("requires an event when dispatching rollback", func() {
		var stdout, stderr bytes.Buffer

		gomega.Expect(Run([]string{"rollback"}, &stdout, &stderr, nil, Deps{})).To(gomega.Equal(2))
		gomega.Expect(stderr.String()).To(gomega.ContainSubstring("--event is required"))
	})

	ginkgo.It("emits ranker state as JSON", func() {
		root := ginkgo.GinkgoT().TempDir()
		configPath := filepath.Join(root, "config.json")
		gomega.Expect(os.WriteFile(
			configPath,
			[]byte(fmt.Sprintf(`{"workspace":{"root":%q}}`, root)),
			0o644,
		)).To(gomega.Succeed())
		var stdout, stderr bytes.Buffer

		gomega.Expect(Run(
			[]string{"show", "--config", configPath, "--json"},
			&stdout,
			&stderr,
			nil,
			Deps{},
		)).To(gomega.Equal(0), "stderr=%s stdout=%s", stderr.String(), stdout.String())
		gomega.Expect(stdout.String()).To(gomega.SatisfyAll(
			gomega.ContainSubstring(`"workspace_root"`),
			gomega.ContainSubstring(`"weights"`),
			gomega.ContainSubstring(`"log_summary"`),
		))
	})
})
