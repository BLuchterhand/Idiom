package app

import (
	"context"
	"os"
	"path/filepath"
	"time"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"idiom/internal/capabilities/workspace"
	planartifact "idiom/internal/contracts/planartifact"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/sessionlog"
	planexecuteadapters "idiom/internal/workflows/planexecute/adapters"
)

var _ = ginkgo.Describe("prompt execution identity", ginkgo.Label("race-contract"), func() {
	ginkgo.It("binds the generated log owner when workspace session configuration is empty", func() {
		root := ginkgo.GinkgoT().TempDir()
		cfg := config.Default()
		cfg.Chat.AutoStartServer = false
		cfg.Chat.AttachInstructionFiles = false
		cfg.Chat.SystemPromptFiles = nil
		cfg.Chat.DefaultProfiles = nil
		cfg.Chat.DebugLogDir = filepath.Join(root, "logs", "conversations")
		cfg.Workspace.Root = root
		cfg.Workspace.CWD = root
		cfg.Workspace.SessionID = ""
		cfg.MCP = nil

		model := New(cfg)
		ginkgo.DeferCleanup(func() { _ = model.Close() })
		expected := sessionlog.SessionIDFromPath(model.debugLogPath)
		gomega.Expect(expected).NotTo(gomega.BeEmpty())

		session := newDetachedPromptExecutionSession(context.Background(), model)
		actual, bound := workspace.ExecutionSessionIDFromContext(session.promptExecutionContext)
		gomega.Expect(bound).To(gomega.BeTrue())
		gomega.Expect(actual).To(gomega.Equal(expected))
		gomega.Expect(model.workspace.SessionID()).To(gomega.BeEmpty())
	})

	ginkgo.It("keeps configured identity separate from the transcript owner", func() {
		root := ginkgo.GinkgoT().TempDir()
		cfg := config.Default()
		cfg.Workspace.Root = root
		cfg.Workspace.CWD = root
		cfg.Workspace.SessionID = "configured-session"
		model := &Model{
			cfg:       cfg,
			workspace: workspace.NewManager(cfg.Workspace),
		}

		configured := newDetachedPromptExecutionSession(context.Background(), model)
		actual, bound := workspace.ExecutionSessionIDFromContext(configured.promptExecutionContext)
		gomega.Expect(bound).To(gomega.BeTrue())
		gomega.Expect(actual).To(gomega.Equal("configured-session"))

		model.debugLogPath = filepath.Join(root, "logs", "resumed-session.jsonl")
		resumed := newDetachedPromptExecutionSession(context.Background(), model)
		actual, bound = workspace.ExecutionSessionIDFromContext(resumed.promptExecutionContext)
		gomega.Expect(bound).To(gomega.BeTrue())
		gomega.Expect(actual).To(gomega.Equal("configured-session"))
		gomega.Expect(model.workspace.SessionID()).To(gomega.Equal("configured-session"))
	})

	ginkgo.It("preserves an execution identity already bound by the caller", func() {
		root := ginkgo.GinkgoT().TempDir()
		cfg := config.Default()
		cfg.Workspace.Root = root
		cfg.Workspace.CWD = root
		cfg.Workspace.SessionID = "configured-session"
		model := &Model{
			cfg:          cfg,
			workspace:    workspace.NewManager(cfg.Workspace),
			debugLogPath: filepath.Join(root, "logs", "transcript-session.jsonl"),
		}

		ctx := workspace.WithExecutionSessionID(context.Background(), "caller-session")
		session := newDetachedPromptExecutionSession(ctx, model)
		actual, bound := workspace.ExecutionSessionIDFromContext(session.promptExecutionContext)
		gomega.Expect(bound).To(gomega.BeTrue())
		gomega.Expect(actual).To(gomega.Equal("caller-session"))
	})

	ginkgo.It("keeps a headless plan artifact discoverable by the configured execution session", func() {
		root := ginkgo.GinkgoT().TempDir()
		cfg := config.Default()
		cfg.Workspace.Root = root
		cfg.Workspace.CWD = root
		cfg.Workspace.EnableWrites = true
		cfg.Workspace.SessionID = "headless-plan-session"
		model := &Model{
			cfg:          cfg,
			workspace:    workspace.NewManager(cfg.Workspace),
			debugLogPath: filepath.Join(root, "logs", "20260712-120000-headless-plan-session.jsonl"),
		}

		session := newDetachedPromptExecutionSession(context.Background(), model)
		result := model.workspace.Execute(session.promptExecutionContext, workspace.ToolRequest{
			Tool:      workspace.ToolNameSubmitPlan,
			Rationale: "Create the configured-session handoff fixture.",
			Edits: []planartifact.PlanEdit{{
				Path:       "handoff.txt",
				NewText:    "configured session\n",
				ReplaceAll: true,
				Purpose:    "Prove the executor can find the planner artifact by the configured session.",
			}},
		})
		gomega.Expect(result.Error).To(gomega.BeEmpty())

		planPath, err := planexecuteadapters.FindLatestPlanArtifact(root, time.Time{}, cfg.Workspace.SessionID)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		artifact, err := workspace.LoadPlanArtifact(planPath)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(artifact.SessionID).To(gomega.Equal(cfg.Workspace.SessionID))
		gomega.Expect(artifact.SessionID).NotTo(gomega.Equal(sessionlog.SessionIDFromPath(model.debugLogPath)))
	})

	ginkgo.It("does not expose an old session grant after resuming another log", func() {
		parent := ginkgo.GinkgoT().TempDir()
		root := filepath.Join(parent, "workspace")
		sibling := filepath.Join(parent, "sibling")
		gomega.Expect(os.MkdirAll(root, 0o755)).To(gomega.Succeed())
		gomega.Expect(os.MkdirAll(sibling, 0o755)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(sibling, "README.md"), []byte("sibling\n"), 0o644)).To(gomega.Succeed())

		logDir := filepath.Join(root, "logs", "conversations")
		cfg := config.Default()
		cfg.Chat.AutoStartServer = false
		cfg.Chat.AttachInstructionFiles = false
		cfg.Chat.SystemPromptFiles = nil
		cfg.Chat.DefaultProfiles = nil
		cfg.Chat.DebugLogDir = logDir
		cfg.Workspace.Root = root
		cfg.Workspace.CWD = root
		cfg.Workspace.SessionID = ""
		cfg.MCP = nil

		model := New(cfg)
		ginkgo.DeferCleanup(func() { _ = model.Close() })
		oldSession := newDetachedPromptExecutionSession(context.Background(), model)
		oldID, bound := workspace.ExecutionSessionIDFromContext(oldSession.promptExecutionContext)
		gomega.Expect(bound).To(gomega.BeTrue())
		gomega.Expect(oldID).NotTo(gomega.BeEmpty())

		approver := NewMockSystemSearchAccessApprover(gomock.NewController(ginkgo.GinkgoT()))
		approver.EXPECT().
			ApproveSystemSearchAccess(gomock.Any(), gomock.Any()).
			Return(workspace.SystemSearchAccessDecision{
				Approved: true,
				Scope:    workspace.SystemSearchAccessScopeContents,
			}, nil).
			Times(1)
		grantCtx := workspace.WithSystemSearchAccessApprover(oldSession.promptExecutionContext, approver)
		grant := model.workspace.Execute(grantCtx, workspace.ToolRequest{
			Tool:    workspace.ToolNameSystemSearchAccess,
			Path:    "../sibling",
			Purpose: "establish an old-session grant",
		})
		gomega.Expect(grant.Error).To(gomega.BeEmpty())
		oldRead := model.workspace.Execute(oldSession.promptExecutionContext, workspace.ToolRequest{Tool: workspace.ToolNameListFiles, Path: "../sibling"})
		gomega.Expect(oldRead.Error).To(gomega.BeEmpty())

		writeSessionLog(ginkgo.GinkgoT(), logDir, "resumed-session.jsonl", []string{
			`{"type":"message","role":"user","content":"resume this session"}`,
			`{"type":"message","role":"assistant","content":"session resumed"}`,
		})
		gomega.Expect(model.ResumeSession("resumed-session")).To(gomega.Succeed())

		resumedSession := newDetachedPromptExecutionSession(context.Background(), model)
		resumedID, bound := workspace.ExecutionSessionIDFromContext(resumedSession.promptExecutionContext)
		gomega.Expect(bound).To(gomega.BeTrue())
		gomega.Expect(resumedID).To(gomega.Equal("resumed-session"))
		gomega.Expect(resumedID).NotTo(gomega.Equal(oldID))
		gomega.Expect(model.workspace.SessionID()).To(gomega.BeEmpty())

		resumedRead := model.workspace.Execute(resumedSession.promptExecutionContext, workspace.ToolRequest{Tool: workspace.ToolNameListFiles, Path: "../sibling"})
		gomega.Expect(resumedRead.Error).To(gomega.ContainSubstring("has not been approved"))
		oldRead = model.workspace.Execute(oldSession.promptExecutionContext, workspace.ToolRequest{Tool: workspace.ToolNameListFiles, Path: "../sibling"})
		gomega.Expect(oldRead.Error).To(gomega.BeEmpty())
	})
})
