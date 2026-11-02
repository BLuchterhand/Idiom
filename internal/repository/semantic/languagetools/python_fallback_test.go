package languagetools

import (
	"context"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
)

var _ = Describe("Python index fallbacks", func() {
	It("reports symbols and references when workspace path access is denied", func() {
		root := GinkgoT().TempDir()
		writePythonFallbackFixture(filepath.Join(root, "tools", "check", "arch_lint.py"), `from pathlib import Path

def run_scan(scripts: Path, rules=None) -> dict:
	return {}
`)
		writePythonFallbackFixture(filepath.Join(root, "tools", "check", "run.py"), `from arch_lint import run_scan

def main():
	return run_scan(None)
`)
		writePythonFallbackFixture(filepath.Join(root, "tools", "check", "notes.py"), `# run_scan is mentioned in this comment, not used here.
`)
		writePythonFallbackFixture(filepath.Join(root, "other", "run_scan.py"), `def run_scan():
	return None
`)

		controller := gomock.NewController(GinkgoT())
		host := NewMockHost(controller)
		searchCalls := 0
		host.EXPECT().Enabled().Return(true).AnyTimes()
		host.EXPECT().Root().Return(root).AnyTimes()
		host.EXPECT().ResolvePath(gomock.Any(), gomock.Any()).Return("", os.ErrPermission).AnyTimes()
		host.EXPECT().SearchContent(gomock.Any(), gomock.Any()).DoAndReturn(func(context.Context, SearchContentRequest) ToolResult {
			searchCalls++
			if searchCalls == 1 {
				return ToolResult{Tool: "search_content", Error: "Access is denied."}
			}
			return ToolResult{Tool: "search_content", Summary: "No content matches"}
		}).AnyTimes()
		manager := NewManager(host)

		found := manager.Dispatch(context.Background(), ToolRequest{Tool: "python_find_symbol", Pattern: "run_scan", Path: "tools/check"})
		Expect(found.Error).To(BeEmpty())
		Expect(found.Summary).To(ContainSubstring("Found 1 matching Python symbol"))
		Expect(found.Output).To(ContainSubstring("tools/check/arch_lint.py"))

		references := manager.Dispatch(context.Background(), ToolRequest{Tool: "python_references", Pattern: "run_scan", Path: "tools/check"})
		Expect(references.Error).To(BeEmpty())
		Expect(references.PrecisionTier).To(Equal(PrecisionTierSyntaxIndex))
		Expect(references.Output).To(ContainSubstring("tools/check/run.py"))
		Expect(references.Output).NotTo(ContainSubstring("tools/check/notes.py"))
		Expect(references.Output).NotTo(ContainSubstring("other/run_scan.py"))

		noContentMatches := manager.Dispatch(context.Background(), ToolRequest{Tool: "python_references", Pattern: "run_scan", Path: "tools/check"})
		Expect(noContentMatches.Error).To(BeEmpty())
		Expect(noContentMatches.PrecisionTier).To(Equal(PrecisionTierSyntaxIndex))
		Expect(noContentMatches.Output).To(ContainSubstring("tools/check/run.py"))
	})
})

func writePythonFallbackFixture(path string, content string) {
	GinkgoHelper()
	Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(Succeed())
	Expect(os.WriteFile(path, []byte(content), 0o644)).To(Succeed())
}
