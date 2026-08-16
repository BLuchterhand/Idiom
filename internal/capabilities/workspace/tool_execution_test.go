package workspace

import (
	"context"
	"os"
	"path/filepath"

	"idiom/internal/contracts/toolpolicy"
	"idiom/internal/platform/config"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("tool execution envelopes", func() {
	It("records allowed reads and privacy-safe evidence", func() {
		root := GinkgoT().TempDir()
		path := filepath.Join(root, "README.md")
		Expect(os.WriteFile(path, []byte("hello"), 0o600)).To(Succeed())
		manager := NewManager(config.Workspace{Root: root, CWD: root})
		var observed ToolExecutionEnvelope
		manager.SetToolExecutionObserver(func(envelope ToolExecutionEnvelope) { observed = envelope })

		result := manager.Execute(context.Background(), ToolRequest{Tool: ToolNameReadFile, Path: "README.md"})

		Expect(result.Error).To(BeEmpty())
		Expect(observed.Validate()).To(Succeed())
		Expect(observed.Transport).To(Equal("workspace"))
		Expect(observed.Status).To(Equal(ToolExecutionCompleted))
		Expect(observed.PreflightDecision).To(Equal(toolpolicy.Allow))
		Expect(observed.RequestFingerprint).NotTo(ContainSubstring("hello"))
	})

	It("records configured denial before dispatch", func() {
		root := GinkgoT().TempDir()
		manager := NewManager(config.Workspace{Root: root, CWD: root, ToolRules: map[string][]config.ToolRule{ToolNameReadFile: {{Decision: string(toolpolicy.Deny)}}}})
		var observed ToolExecutionEnvelope
		manager.SetToolExecutionObserver(func(envelope ToolExecutionEnvelope) { observed = envelope })

		result := manager.Execute(context.Background(), ToolRequest{Tool: ToolNameReadFile, Path: "secret.txt"})

		Expect(result.Error).NotTo(BeEmpty())
		Expect(observed.Status).To(Equal(ToolExecutionDenied))
		Expect(observed.PreflightDecision).To(Equal(toolpolicy.Deny))
		Expect(observed.EvidenceProduced).To(BeFalse())
	})
})
