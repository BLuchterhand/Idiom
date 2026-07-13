package app

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/toolpolicy"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/modepolicy"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

var _ = ginkgo.It("request approval auto denies in headless mode", ginkgo.SpecTimeout(500*time.Millisecond), func(ctx ginkgo.SpecContext) {
	// Headless flow: SetJSONEventStream is wired, no TUI is running.
	// requestApproval used to block forever on a channel that only the
	// TUI thread filled. The auto-deny path returns false promptly and
	// emits an approval_auto_denied event so the audit trail captures
	// what happened.
	model := &Model{}

	var buf bytes.Buffer

	model.SetJSONEventStream(&buf, "test-session")
	session := newDetachedPromptExecutionSession(ctx, model)

	done := make(chan bool, 1)
	go func() {
		done <- session.requestApproval(workspace.ToolRequest{Tool: "edit_file", Path: "a.txt"}, "implement")
	}()

	var got bool
	gomega.Eventually(ctx, done).Should(gomega.Receive(&got))
	gomega.Expect(got).To(gomega.BeFalse(), fmt.Sprintf("expected auto-deny in headless mode, got true"))

	emitted := buf.String()
	gomega.Expect(strings.Contains(emitted, "approval_auto_denied")).To(gomega.BeTrue(), fmt.Sprintf("expected approval_auto_denied event in JSON stream, got: %s", emitted))

	gomega.Expect(strings.Contains(emitted, `"tool":"edit_file"`)).To(gomega.BeTrue(), fmt.Sprintf("event should name the tool, got: %s", emitted))
})

var _ = ginkgo.It("request approval auto denies in non interactive prompt runner", ginkgo.SpecTimeout(500*time.Millisecond), func(ctx ginkgo.SpecContext) {
	model := &Model{}
	model.SetNonInteractive(true)
	session := newDetachedPromptExecutionSession(ctx, model)

	done := make(chan bool, 1)
	go func() {
		done <- session.requestApproval(workspace.ToolRequest{Tool: "edit_file", Path: "a.txt"}, "implement")
	}()

	var got bool
	gomega.Eventually(ctx, done).Should(gomega.Receive(&got))
	gomega.Expect(got).To(gomega.BeFalse(), fmt.Sprintf("expected auto-deny in non-interactive mode, got true"))
})

var _ = ginkgo.It("ignores an awaiting status after its approval was denied", func() {
	pending := &pendingApproval{
		request:  workspace.ToolRequest{Tool: workspace.ToolNameWriteFile, Path: "internal/catalog/service.go"},
		mode:     "build",
		response: make(chan approvalResponse, 1),
	}
	model := &Model{approvalPending: pending}

	model.slashDeny("/deny", []string{"/deny"})
	gomega.Expect(model.status).To(gomega.Equal("Denied: write_file: internal/catalog/service.go"))
	gomega.Expect(model.currentApproval()).To(gomega.BeNil())

	model.handleApprovalAwaiting(approvalAwaitingMsg{pending: pending})
	gomega.Expect(model.status).To(gomega.Equal("Denied: write_file: internal/catalog/service.go"))

	var response approvalResponse
	gomega.Expect(pending.response).To(gomega.Receive(&response))
	gomega.Expect(response.allow).To(gomega.BeFalse())
})

var _ = ginkgo.It("pre execute tool request config deny overrides mode allow", func() {
	model := &Model{
		workspace: workspace.NewManager(config.Workspace{
			ToolRules: map[string][]config.ToolRule{
				"read_file": {
					{Pattern: ".idiom/deny-read-test.txt", Decision: "deny"},
				},
			},
		}),
	}
	model.workspace.SetPermissionRules(map[string][]toolpolicy.Rule{
		"read_file": {
			{Pattern: "*", Decision: toolpolicy.Allow},
		},
	})
	session := newDetachedPromptExecutionSession(context.Background(), model)

	result := session.preExecuteToolRequest(1, workspace.ToolRequest{
		Tool: "read_file",
		Path: ".idiom/deny-read-test.txt",
	}, modepolicy.Mode{Name: "build"})
	gomega.Expect(result).NotTo(gomega.BeNil(), fmt.Sprint("expected TUI pre-execute hook to block configured deny"))

	gomega.Expect(result.Output).To(gomega.Equal(""), fmt.Sprintf("deny result must not include file output, got %q", result.Output))

	gomega.Expect(strings.Contains(result.Summary, "permission rules")).To(gomega.BeTrue(), fmt.Sprintf("unexpected deny summary %q", result.Summary))

	gomega.Expect(strings.Contains(result.Error, "deny read_file")).To(gomega.BeTrue(), fmt.Sprintf("unexpected deny error %q", result.Error))
})

var _ = ginkgo.It("workspace mutation authorization reports an unapproved TUI ask", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	model := &Model{
		workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true}),
	}
	model.SetJSONEventStream(&bytes.Buffer{}, "approval-required-test")
	model.workspace.SetProtectedWritePaths([]string{"prompts/base.md"})
	session := newDetachedPromptExecutionSession(workspace.WithExecutionSessionID(context.Background(), "approval-required-test"), model)

	result := model.workspace.Execute(session.promptExecutionContext, workspace.ToolRequest{
		Tool:        workspace.ToolNameWriteFile,
		Path:        "prompts/base.md",
		Content:     "must not be written\n",
		Description: "exercise headless TUI denial",
	})
	gomega.Expect(result.Summary).To(gomega.Equal("Mutation approval denied"))
	gomega.Expect(result.Error).To(gomega.ContainSubstring("operator did not approve"))
	_, err := os.Stat(filepath.Join(root, "prompts", "base.md"))
	gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue(), fmt.Sprintf("denied mutation unexpectedly wrote its target: %v", err))
})

var _ = ginkgo.It("pre execute tool request does not let shell bypass protected prompt approval", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	model := &Model{
		workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
	}
	model.SetJSONEventStream(&bytes.Buffer{}, "protected-shell-approval-test")
	model.workspace.SetProtectedWritePaths([]string{"prompts/base.md"})
	model.workspace.SetPermissionRules(map[string][]toolpolicy.Rule{
		workspace.ToolNameShell: {{Pattern: "*", Decision: toolpolicy.Allow}},
	})
	session := newDetachedPromptExecutionSession(context.Background(), model)

	result := session.preExecuteToolRequest(1, workspace.ToolRequest{
		Tool:    workspace.ToolNameShell,
		Command: `printf changed > prompts/base.md`,
		Workdir: ".",
	}, modepolicy.Mode{Name: "build"})
	gomega.Expect(result).NotTo(gomega.BeNil(), "expected protected shell mutation to require approval")
	gomega.Expect(result.Summary).To(gomega.Equal("Write approval required"))
	gomega.Expect(result.Error).To(gomega.ContainSubstring("write approval required before running shell"))
})
