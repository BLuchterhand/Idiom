package app

import (
	"context"
	"fmt"

	"idiom/internal/platform/config"
	"idiom/internal/runtime/backends"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

var _ = ginkgo.It("writer route model for role prefers direct config over alias", func() {
	m := &Model{cfg: config.Config{LlamaServer: config.LlamaServer{Model: "deepseek-v4-flash"}}}
	session := newDetachedPromptExecutionSession(context.Background(), m)
	aliases := map[string]string{backends.RoleClassifier: "deepseek-v4-pro"}

	{
		got := session.writerRouteModelForRole(aliases, backends.RoleClassifier)
		gomega.Expect(got).To(gomega.Equal("deepseek-v4-flash"), fmt.Sprintf("classifier model = %q, want direct configured model", got))
	}
})

var _ = ginkgo.It("writer route model for role uses alias when no direct model configured", func() {
	m := &Model{}
	session := newDetachedPromptExecutionSession(context.Background(), m)
	aliases := map[string]string{backends.RoleClassifier: "deepseek-v4-pro"}

	{
		got := session.writerRouteModelForRole(aliases, backends.RoleClassifier)
		gomega.Expect(got).To(gomega.Equal("deepseek-v4-pro"), fmt.Sprintf("classifier model = %q, want alias fallback", got))
	}
})
