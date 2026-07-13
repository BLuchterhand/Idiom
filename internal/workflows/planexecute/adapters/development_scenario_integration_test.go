package adapters

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	planartifact "idiom/internal/contracts/planartifact"
	"idiom/internal/platform/config"
	"idiom/internal/workflows/planexecute"
)

var _ = ginkgo.It("submits, selects, applies, verifies, and retires a session-owned plan", ginkgo.Label("development-scenario", "integration", "contract"), func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	path := filepath.Join(root, "pkg", "value.go")
	gomega.Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(gomega.Succeed())
	gomega.Expect(os.WriteFile(path, []byte("package pkg\n\nfunc Value() int { return 1 }\n"), 0o644)).To(gomega.Succeed())

	workspaceConfig := config.Workspace{
		Root:         root,
		CWD:          root,
		EnableWrites: true,
		SessionID:    "adapter-development-scenario",
	}
	submitted := SubmitAutoPlan(context.Background(), workspaceConfig, planexecute.AutoPlanSubmitRequest{
		Rationale: "Repair Value without changing its public signature.",
		Edits: []planartifact.PlanEdit{{
			Path:    "pkg/value.go",
			OldText: "return 1",
			NewText: "return 2",
			Purpose: "correct the returned value",
		}},
	})
	gomega.Expect(submitted.Error).To(gomega.BeEmpty())

	planPath, err := FindLatestPlanArtifact(root, time.Time{}, workspaceConfig.SessionID)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(filepath.Base(planPath)).NotTo(gomega.Equal("last.plan.json"))

	dependencies := Dependencies()
	plan, err := dependencies.LoadPlanArtifact(planPath)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(plan.SessionID).To(gomega.Equal(workspaceConfig.SessionID))
	gomega.Expect(plan.Edits).To(gomega.HaveLen(1))

	edit := plan.Edits[0]
	applied := dependencies.ApplyExecutorEdit(context.Background(), workspaceConfig, planexecute.ExecutorEditRequest{
		Tool:        "edit_file",
		Path:        edit.Path,
		OldText:     edit.OldText,
		NewText:     edit.NewText,
		StartLine:   edit.StartLine,
		EndLine:     edit.EndLine,
		Content:     edit.Content,
		ReplaceAll:  edit.ReplaceAll,
		Description: edit.Purpose,
	})
	gomega.Expect(applied.Error).To(gomega.BeEmpty())
	body, err := os.ReadFile(path)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(string(body)).To(gomega.ContainSubstring("return 2"))

	sanity := dependencies.SourceSanity(context.Background(), workspaceConfig, "pkg/value.go")
	gomega.Expect(sanity.Error).To(gomega.BeEmpty(), "summary=%s", sanity.Summary)

	staleTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	gomega.Expect(os.Chtimes(planPath, staleTime, staleTime)).To(gomega.Succeed())
	dependencies.CleanupPlanArtifacts(root, 24*time.Hour, ginkgo.GinkgoWriter)
	_, err = os.Stat(planPath)
	gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue())
	_, err = os.Stat(filepath.Join(root, ".idiom", "plans", "last.plan.json"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "rolling plan pointer must survive stale immutable artifact cleanup")
})
