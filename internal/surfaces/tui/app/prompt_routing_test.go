package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"idiom/internal/capabilities/workspace"
	plancontract "idiom/internal/contracts/plan"
	"idiom/internal/contracts/workflowstate"
	"idiom/internal/platform/config"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

var _ = ginkgo.It("has readable workspace files detects project evidence", func() {
	t := ginkgo.GinkgoT()
	tests := []struct {
		name  string
		files []string
		want  bool
	}{
		{
			name: "empty workspace",
			want: false,
		},
		{
			name:  "root readme",
			files: []string{"README.md"},
			want:  true,
		},
		{
			name:  "nested docs only workspace",
			files: []string{"docs/evidence.md"},
			want:  true,
		},
		{
			name:  "nested source workspace",
			files: []string{"cmd/app/main.go"},
			want:  true,
		},
		{
			name:  "ignored metadata only",
			files: []string{".git/config", ".idiom/cache/state.json", "node_modules/pkg/index.js"},
			want:  false,
		},
	}

	for _, tt := range tests {
		ginkgo.By(fmt.Sprint(tt.name), func() {
			root := t.TempDir()
			for _, file := range tt.files {
				writePromptRoutingTestFile(t, root, file)
			}

			{
				got := hasReadableWorkspaceFiles(root)
				gomega.Expect(got).To(gomega.Equal(tt.want), fmt.Sprintf("hasReadableWorkspaceFiles()=%t, want %t", got, tt.want))
			}
		})
	}
})

var _ = ginkgo.It("prompt context packet projection carries terminal state when blocked", func() {
	projection := plancontract.PromptContextPacketProjectionFromPrompt("plain prompt without packet headings")

	gomega.Expect(projection.State).To(gomega.Equal(workflowstate.StateTerminal), fmt.Sprintf("state = %q, want terminal", projection.State))
	gomega.Expect(projection.TerminalState).To(gomega.Equal(workflowstate.TerminalStateBlocked), fmt.Sprintf("terminal_state = %q, want blocked", projection.TerminalState))
	gomega.Expect(projection.Dispatchable).To(gomega.BeFalse(), fmt.Sprint("missing context packet must not be dispatchable"))
})

var _ = ginkgo.It("greenfield creation confirmation skips read only design question", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	model := &Model{
		workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
	}
	session := newDetachedPromptExecutionSession(context.Background(), model)
	plan := promptExecutionPlan{
		route: turnRoutePlan{
			Family:      "conversation",
			InitialLane: "main",
			Contract:    "direct_answer",
			WriteIntent: "no",
		},
	}
	prompt := `What would a decent "LLM brain" project look like? something that can maintain context across projects and reduce overall token consumption through clever documentation management?`

	gomega.Expect(session.greenfieldCreationRequiresConfirmation(prompt, plan)).To(gomega.BeFalse(), fmt.Sprint("greenfield design question should reach the direct-answer model path"))
})

var _ = ginkgo.It("greenfield creation confirmation still catches misrouted creation prompt", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	model := &Model{
		workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
	}
	session := newDetachedPromptExecutionSession(context.Background(), model)
	readOnlyPlan := promptExecutionPlan{
		route: turnRoutePlan{
			Family:      "conversation",
			InitialLane: "main",
			Contract:    "direct_answer",
			WriteIntent: "no",
		},
	}
	prompt := "Create a simple browser app."

	gomega.Expect(session.greenfieldCreationRequiresConfirmation(prompt, readOnlyPlan)).To(gomega.BeTrue(), fmt.Sprint("misrouted greenfield creation prompt should still ask for lane confirmation"))

	writePromptRoutingTestFile(t, root, "README.md")
	gomega.Expect(session.greenfieldCreationRequiresConfirmation(prompt, readOnlyPlan)).To(gomega.BeFalse(), fmt.Sprint("non-empty workspace should not trigger greenfield creation confirmation"))
})

var _ = ginkgo.It("read only edit request requires implementation confirmation", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	writePromptRoutingTestFile(t, root, "README.md")
	model := &Model{
		workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
	}
	session := newDetachedPromptExecutionSession(context.Background(), model)
	readOnlyPlan := promptExecutionPlan{
		route: turnRoutePlan{
			Family:      "conversation",
			InitialLane: "main",
			Contract:    "direct_answer",
			WriteIntent: "no",
		},
	}

	gomega.Expect(session.readOnlyEditRequestRequiresConfirmation("Please make the requested edits in internal/runtime/agent/runner_core.go.", readOnlyPlan)).To(gomega.BeTrue(), fmt.Sprint("misrouted explicit edit request should ask for implementation confirmation before a read-only model turn"))

	gomega.Expect(session.readOnlyEditRequestRequiresConfirmation("How would you make the routing code easier to understand?", readOnlyPlan)).To(gomega.BeFalse(), fmt.Sprint("read-only design question should not ask for implementation confirmation"))

	writePlan := promptExecutionPlan{
		route: turnRoutePlan{
			Family:      "implementation",
			InitialLane: "implement",
			Contract:    "implementation_result",
			WriteIntent: "yes",
		},
	}
	gomega.Expect(session.readOnlyEditRequestRequiresConfirmation("Please make the requested edits.", writePlan)).To(gomega.BeFalse(), fmt.Sprint("write-capable route should not ask for read-only edit confirmation"))

	session.planExecuteActive = true
	gomega.Expect(session.readOnlyEditRequestRequiresConfirmation("Submit a corrective plan with executable edits only.", readOnlyPlan)).To(gomega.BeFalse(), fmt.Sprint("internal plan-execute prompt runner should not ask for user-facing implementation confirmation"))
})

var _ = ginkgo.It("greenfield creation confirmation uses shared presenter", func() {
	readOnlyPlan := promptExecutionPlan{
		route: turnRoutePlan{
			Family:      "conversation",
			InitialLane: "main",
			Contract:    "direct_answer",
			WriteIntent: "no",
		},
	}

	answer := greenfieldCreationConfirmationQuestion(readOnlyPlan)
	for _, want := range []string{
		"This project appears to be empty, and this request would create files.",
		"`/implement <objective>`",
		"write-capable implement lane",
		"ask your question as-is",
	} {
		gomega.Expect(strings.Contains(answer, want)).To(gomega.BeTrue(), fmt.Sprintf("greenfield confirmation missing %q:\n%s", want, answer))
	}

	for _, forbidden := range []string{
		"Proposed paths:",
		"new_file_authorization",
		"State saved at",
		"Feature planner needs your approval",
	} {
		gomega.Expect(strings.Contains(answer, forbidden)).To(gomega.BeFalse(), fmt.Sprintf("greenfield confirmation leaked scope-authorization text %q:\n%s", forbidden, answer))
	}
})

var _ = ginkgo.It("workspace edit confirmation question uses implementation command", func() {
	answer := workspaceEditConfirmationQuestion(promptExecutionPlan{
		route: turnRoutePlan{
			Family:      "conversation",
			InitialLane: "main",
			Contract:    "direct_answer",
			WriteIntent: "no",
		},
	})

	for _, want := range []string{
		"selected route is read-only",
		"`/implement <objective>`",
		"write-capable implement lane",
		"answer in read-only mode",
	} {
		gomega.Expect(strings.Contains(answer, want)).To(gomega.BeTrue(), fmt.Sprintf("workspace edit confirmation missing %q:\n%s", want, answer))
	}
})

func writePromptRoutingTestFile(t ginkgo.FullGinkgoTInterface, root string, rel string) {
	ginkgo.GinkgoHelper()

	path := filepath.Join(root, rel)
	{
		err := os.MkdirAll(filepath.Dir(path), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir %s: %v", filepath.Dir(path), err))
	}

	{
		err := os.WriteFile(path, []byte("test\n"), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write %s: %v", path, err))
	}
}
