package agent

import (
	"strings"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/workerruntime"
)

var _ = ginkgo.Describe("Runtime tool surface filtering", func() {
	ginkgo.It("keeps the workspace surface when no runtime allowlist is present", func() {
		root := ginkgo.GinkgoT().TempDir()
		run := newRunnerRunFixture(&Runner{
			Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
			RuntimeBrief: workerruntime.WorkerRuntimeBrief{
				PermissionProfile: "read_only_workspace",
			},
		})

		run.rebuildRunTools()

		gomega.Expect(run.state.opts.Tools).NotTo(gomega.BeEmpty())
	})

	ginkgo.It("does not widen an explicit allowlist whose tools are unavailable", func() {
		root := ginkgo.GinkgoT().TempDir()
		run := newRunnerRunFixture(&Runner{
			Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
			RuntimeBrief: workerruntime.WorkerRuntimeBrief{
				PermissionProfile: "read_only_workspace",
				AllowedTools:      []string{"removed_external_tool"},
			},
		})

		run.rebuildRunTools()

		gomega.Expect(run.state.opts.Tools).To(gomega.BeEmpty())
		gomega.Expect(run.state.currentAllowedTools).To(gomega.BeEmpty())
	})

	ginkgo.It("keeps verification-only surface empty when shell is unavailable", func() {
		root := ginkgo.GinkgoT().TempDir()
		run := newRunnerRunFixture(&Runner{
			Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
			RuntimeBrief: workerruntime.WorkerRuntimeBrief{
				PermissionProfile: "read_only_verification_shell",
				AllowedTools:      []string{workspace.ToolNameReadFile},
			},
		})

		run.rebuildRunTools()

		gomega.Expect(run.state.opts.Tools).To(gomega.BeEmpty())
		gomega.Expect(run.state.currentAllowedTools).To(gomega.BeEmpty())
		gomega.Expect(strings.TrimSpace(run.state.opts.ToolChoice)).To(gomega.BeEmpty())
	})
})
