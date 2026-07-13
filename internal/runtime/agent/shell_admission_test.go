package agent

import (
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/runtime/workerruntime"
)

var _ = ginkgo.Describe("Guarded implementation shell admission", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:           "implementation",
		Contract:             "implementation_result",
		PermissionProfile:    "guarded_workspace_write",
		RequiredEditPaths:    []string{"internal/allowed.go"},
		VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
	}

	ginkgo.DescribeTable("allows only producer-classified read-only or verification commands",
		func(command string) {
			_, _, rejected := guardedImplementationShellRejection(workspace.ToolRequest{Tool: workspace.ToolNameShell, Command: command}, brief)
			gomega.Expect(rejected).To(gomega.BeFalse(), command)
		},
		ginkgo.Entry("read-only status", "git status --short"),
		ginkgo.Entry("read-only inspection", "sed -n 1,40p internal/allowed.go"),
		ginkgo.Entry("focused test", "go test ./internal/runtime/agent"),
		ginkgo.Entry("patch hygiene", "git diff --check -- internal/allowed.go"),
	)

	ginkgo.DescribeTable("rejects commands that could mutate outside typed workspace authority",
		func(command string) {
			summary, detail, rejected := guardedImplementationShellRejection(workspace.ToolRequest{Tool: workspace.ToolNameShell, Command: command}, brief)
			gomega.Expect(rejected).To(gomega.BeTrue(), command)
			gomega.Expect(summary).To(gomega.Equal("Mutation-capable shell rejected"))
			gomega.Expect(detail).To(gomega.ContainSubstring("typed write tools"))
		},
		ginkgo.Entry("in-place sed", "sed -i '' s/old/new/ internal/outside.go"),
		ginkgo.Entry("redirection", "printf changed > internal/outside.go"),
		ginkgo.Entry("move", "mv internal/allowed.go internal/outside.go"),
		ginkgo.Entry("remove", "rm internal/outside.go"),
		ginkgo.Entry("opaque script", "./scripts/rewrite-workspace.sh"),
	)

	ginkgo.It("does not impose implementation policy on a read-only answer lane", func() {
		readOnly := workerruntime.WorkerRuntimeBrief{TaskFamily: "read_only", Contract: "direct_answer"}
		_, _, rejected := guardedImplementationShellRejection(
			workspace.ToolRequest{Tool: workspace.ToolNameShell, Command: "printf changed > internal/outside.go"},
			readOnly,
		)
		gomega.Expect(rejected).To(gomega.BeFalse())
	})
})
