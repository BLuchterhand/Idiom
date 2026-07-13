package workspace

import (
	"context"
	"os"
	"path/filepath"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"

	"idiom/internal/platform/config"
	"idiom/internal/repository/comprehension"
)

var _ = ginkgo.It("propagates workspace request cancellation to the ranker state lock", func() {
	root := ginkgo.GinkgoT().TempDir()
	writeWorkspaceFile(filepath.Join(root, "go.mod"), "module example.com/contextcancel\n\ngo 1.22\n")
	writeWorkspaceFile(filepath.Join(root, "pkg", "owner.go"), "package pkg\n\nfunc Owner() string { return \"owner\" }\n")
	_, err := comprehension.Build(ginkgo.GinkgoT().Context(), comprehension.BuildOptions{Root: root, Language: "go"})
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	rankerDirectory := filepath.Join(root, ".idiom", "ranker")
	gomega.Expect(os.MkdirAll(rankerDirectory, 0o755)).To(gomega.Succeed())
	unlock, err := lockRankerStateFileForContextTest(filepath.Join(rankerDirectory, "state.lock"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	ginkgo.DeferCleanup(unlock)

	manager := NewManager(config.Workspace{Root: root, CWD: root})
	ctx, cancel := context.WithCancel(ginkgo.GinkgoT().Context())
	ginkgo.DeferCleanup(cancel)
	done := make(chan ToolResult, 1)
	go func() {
		done <- manager.contextPack(ctx, ToolRequest{Tool: ToolNameContextPack, Task: "change Owner", Path: "pkg"})
	}()

	gomega.Consistently(done).ShouldNot(gomega.Receive(), "context_pack must wait on the held ranker authority")
	cancel()
	var result ToolResult
	gomega.Eventually(done).Should(gomega.Receive(&result))
	gomega.Expect(result.Summary).To(gomega.Equal("Context pack canceled"))
	gomega.Expect(result.Error).To(gomega.ContainSubstring("canceled"))
})
