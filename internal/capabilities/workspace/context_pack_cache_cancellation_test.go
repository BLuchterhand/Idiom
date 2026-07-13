package workspace

import (
	"context"
	"encoding/json"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"

	"idiom/internal/platform/config"
	"idiom/internal/repository/comprehension"
)

type cancelAtCachedViewFreshnessContext struct {
	context.Context
	cancel context.CancelFunc
	once   sync.Once
}

func (ctx *cancelAtCachedViewFreshnessContext) Err() error {
	callers := make([]uintptr, 24)
	count := runtime.Callers(2, callers)
	frames := runtime.CallersFrames(callers[:count])
	for {
		frame, more := frames.Next()
		if strings.HasSuffix(frame.Function, ".validateCachedViewFreshnessStatus") {
			ctx.once.Do(ctx.cancel)
			break
		}
		if !more {
			break
		}
	}
	return ctx.Context.Err()
}

var _ = ginkgo.It("cancels a real workspace context_pack while validating a cached view", func() {
	root := ginkgo.GinkgoT().TempDir()
	writeWorkspaceFile(filepath.Join(root, "go.mod"), "module example.com/cachecancel\n\ngo 1.22\n")
	writeWorkspaceFile(filepath.Join(root, "pkg", "owner.go"), "package pkg\n\nfunc Owner() string { return \"owner\" }\n")
	_, err := comprehension.Build(ginkgo.GinkgoT().Context(), comprehension.BuildOptions{Root: root, Language: "go"})
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	manager := NewManager(config.Workspace{Root: root, CWD: root})
	request := ToolRequest{Tool: ToolNameContextPack, Task: "change Owner", Path: "pkg", Mode: "plan"}
	first := manager.contextPack(ginkgo.GinkgoT().Context(), request)
	gomega.Expect(first.Error).To(gomega.BeEmpty(), first.Output)
	second := manager.contextPack(ginkgo.GinkgoT().Context(), request)
	gomega.Expect(second.Error).To(gomega.BeEmpty(), second.Output)
	var cached comprehension.PackResult
	gomega.Expect(json.Unmarshal([]byte(second.Output), &cached)).To(gomega.Succeed())
	gomega.Expect(cached.Cache.State).To(gomega.Equal("hit"), "the cancellation probe must start from an established cache hit")

	base, cancel := context.WithCancel(ginkgo.GinkgoT().Context())
	ginkgo.DeferCleanup(cancel)
	ctx := &cancelAtCachedViewFreshnessContext{Context: base, cancel: cancel}
	result := manager.contextPack(ctx, request)

	gomega.Expect(result.Summary).To(gomega.Equal("Context pack canceled"))
	gomega.Expect(result.Error).To(gomega.ContainSubstring("canceled"))
})
