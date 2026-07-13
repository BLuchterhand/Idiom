package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	"idiom/internal/contracts/toolfacts"
	"idiom/internal/contracts/toolpolicy"
	"idiom/internal/platform/config"
)

var _ = ginkgo.Describe("read_file log artifact classification", ginkgo.Label("e2e", "integration", "race-contract"), func() {
	ginkgo.DescribeTable("keeps source files whose names contain log source-grounded",
		func(sourcePath string) {
			root := ginkgo.GinkgoT().TempDir()
			writeTestFile(filepath.Join(root, sourcePath), "package sample\n\nfunc Run() {}\n")
			manager := NewManager(config.Workspace{Root: root, CWD: root})

			result := manager.Execute(context.Background(), ToolRequest{
				Tool:      ToolNameReadFile,
				Path:      sourcePath,
				StartLine: 1,
				EndLine:   3,
			})

			gomega.Expect(result.Error).To(gomega.BeEmpty())
			gomega.Expect(result.SourceWindows()).To(gomega.HaveLen(1))
			gomega.Expect(result.Facts).NotTo(gomega.BeNil())
			gomega.Expect(result.Facts.SourceWindows).To(gomega.HaveLen(1))
			gomega.Expect(result.Facts.DiagnosticLogReads).To(gomega.BeEmpty())
			gomega.Expect(result.SourceWindows()[0].Evidence.CanonicalID).To(gomega.Equal(sourcePath))
		},
		ginkgo.Entry("debuglog.go", "internal/evidence/transcript/debuglog.go"),
		ginkgo.Entry("catalog.go", "internal/catalog/catalog.go"),
	)

	ginkgo.DescribeTable("retains diagnostic treatment for explicit log artifacts",
		func(logPath string) {
			root := ginkgo.GinkgoT().TempDir()
			writeTestFile(filepath.Join(root, logPath), "first event\nsecond event\n")
			manager := NewManager(config.Workspace{Root: root, CWD: root})

			result := manager.Execute(context.Background(), ToolRequest{
				Tool: ToolNameReadFile,
				Path: logPath,
			})

			gomega.Expect(result.Error).To(gomega.BeEmpty())
			gomega.Expect(result.SourceWindows()).To(gomega.BeEmpty())
			gomega.Expect(result.Facts).NotTo(gomega.BeNil())
			gomega.Expect(result.Facts.SourceWindows).To(gomega.BeEmpty())
			gomega.Expect(result.Facts.DiagnosticLogReads).To(gomega.Equal([]toolfacts.DiagnosticLogReadEvidence{{
				CanonicalID: logPath, PolicySubject: logPath, PolicyDecision: toolpolicy.None,
				StartLine: 1, EndLine: 2, TotalLines: 2,
			}}))

			encoded, err := json.Marshal(result.Facts)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(string(encoded)).NotTo(gomega.ContainSubstring("first event"))
			gomega.Expect(string(encoded)).NotTo(gomega.ContainSubstring(root))
		},
		ginkgo.Entry(".log extension", "artifacts/events.log"),
		ginkgo.Entry(".jsonl extension", "artifacts/events.jsonl"),
		ginkgo.Entry("logs directory", "logs/session.txt"),
	)

	ginkgo.It("records the actual returned default tail and explicit bounded window", func() {
		root := ginkgo.GinkgoT().TempDir()
		lines := make([]string, 0, 100)
		for line := 1; line <= 100; line++ {
			lines = append(lines, fmt.Sprintf("event-%03d", line))
		}
		writeTestFile(filepath.Join(root, "logs", "session.log"), strings.Join(lines, "\n"))
		manager := NewManager(config.Workspace{Root: root, CWD: root, MaxFileBytes: 1 << 20})

		tail := manager.Execute(context.Background(), ToolRequest{Tool: ToolNameReadFile, Path: "logs/session.log"})
		gomega.Expect(tail.Error).To(gomega.BeEmpty())
		gomega.Expect(tail.Facts).NotTo(gomega.BeNil())
		gomega.Expect(tail.Facts.DiagnosticLogReads).To(gomega.Equal([]toolfacts.DiagnosticLogReadEvidence{{
			CanonicalID: "logs/session.log", PolicySubject: "logs/session.log", PolicyDecision: toolpolicy.None,
			StartLine: 21, EndLine: 100, TotalLines: 100, Truncated: true,
		}}))

		bounded := manager.Execute(context.Background(), ToolRequest{
			Tool: ToolNameReadFile, Path: "logs/session.log", StartLine: 5, EndLine: 7,
		})
		gomega.Expect(bounded.Error).To(gomega.BeEmpty())
		gomega.Expect(bounded.Facts).NotTo(gomega.BeNil())
		gomega.Expect(bounded.Facts.DiagnosticLogReads).To(gomega.Equal([]toolfacts.DiagnosticLogReadEvidence{{
			CanonicalID: "logs/session.log", PolicySubject: "logs/session.log", PolicyDecision: toolpolicy.None,
			StartLine: 5, EndLine: 7, TotalLines: 100,
		}}))
	})

	ginkgo.It("preserves physical identity and the authorized alias subject", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeTestFile(filepath.Join(root, "logs", "physical.log"), "first event\nsecond event\n")
		if err := os.Symlink(filepath.Join("logs", "physical.log"), filepath.Join(root, "alias.log")); err != nil {
			ginkgo.Skip("symlinks unavailable: " + err.Error())
		}
		manager := NewManager(config.Workspace{Root: root, CWD: root})
		manager.SetPermissionRules(map[string][]toolpolicy.Rule{
			ToolNameReadFile: {{Pattern: "alias.log", Decision: toolpolicy.Ask}},
		})

		result := manager.Execute(context.Background(), ToolRequest{Tool: ToolNameReadFile, Path: "alias.log"})
		gomega.Expect(result.Error).To(gomega.BeEmpty())
		gomega.Expect(result.Facts).NotTo(gomega.BeNil())
		gomega.Expect(result.Facts.DiagnosticLogReads).To(gomega.Equal([]toolfacts.DiagnosticLogReadEvidence{{
			CanonicalID: "logs/physical.log", PolicySubject: "alias.log", PolicyDecision: toolpolicy.Ask,
			StartLine: 1, EndLine: 2, TotalLines: 2,
		}}))
	})

	ginkgo.It("marks per-line diagnostic output caps as truncated", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeTestFile(filepath.Join(root, "events.log"), strings.Repeat("x", defaultLogLineCharCap+50)+"\n")
		manager := NewManager(config.Workspace{Root: root, CWD: root, MaxFileBytes: 1 << 20})

		result := manager.Execute(context.Background(), ToolRequest{
			Tool: ToolNameReadFile, Path: "events.log", StartLine: 1, EndLine: 1,
		})
		gomega.Expect(result.Error).To(gomega.BeEmpty())
		gomega.Expect(result.Facts).NotTo(gomega.BeNil())
		gomega.Expect(result.Facts.DiagnosticLogReads).To(gomega.Equal([]toolfacts.DiagnosticLogReadEvidence{{
			CanonicalID: "events.log", PolicySubject: "events.log", PolicyDecision: toolpolicy.None,
			StartLine: 1, EndLine: 1, TotalLines: 1, Truncated: true,
		}}))
	})

	ginkgo.It("does not invent workspace-relative facts for trusted user-global logs", func() {
		root := ginkgo.GinkgoT().TempDir()
		home := ginkgo.GinkgoT().TempDir()
		ginkgo.GinkgoT().Setenv("HOME", home)
		ginkgo.GinkgoT().Setenv("USERPROFILE", home)
		logPath := filepath.Join(home, ".idiom", "logs", "conversations", "session.jsonl")
		writeTestFile(logPath, "first event\nsecond event\n")
		manager := NewManager(config.Workspace{Root: root, CWD: root, MaxFileBytes: 1 << 20})

		result := manager.Execute(context.Background(), ToolRequest{Tool: ToolNameReadFile, Path: logPath})
		gomega.Expect(result.Error).To(gomega.BeEmpty())
		if result.Facts != nil {
			gomega.Expect(result.Facts.DiagnosticLogReads).To(gomega.BeEmpty())
		}
	})
})
