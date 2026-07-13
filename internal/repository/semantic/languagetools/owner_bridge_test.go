package languagetools

import (
	"os"
	"path/filepath"
	"runtime"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
)

var _ = Describe("Language-tool host bridge", func() {
	It("includes workspace-root Go files as diagnostic targets", func() {
		root := GinkgoT().TempDir()
		writeOwnerTestFile(filepath.Join(root, "go.mod"), "module example.com/diagnostics\n\ngo 1.22\n")
		writeOwnerTestFile(filepath.Join(root, "main.go"), "package main\n\nfunc main() {}\n")

		controller := gomock.NewController(GinkgoT())
		host := NewMockHost(controller)
		host.EXPECT().Root().Return(root).Times(4)
		host.EXPECT().Enabled().Return(true).Times(2)
		host.EXPECT().ResolvePath(".", "").Return(root, nil).Times(1)
		manager := NewManager(host)

		files, err := manager.goDiagnosticTargets(ToolRequest{Tool: "go_diagnostics", Path: "."})
		Expect(err).NotTo(HaveOccurred())
		Expect(files).NotTo(BeEmpty())
		Expect(files[0]).To(Equal("main.go"))
	})

	It("finds Go tools in GOPATH/bin", func() {
		root := GinkgoT().TempDir()
		gopath := filepath.Join(root, "gopath")
		toolPath := filepath.Join(gopath, "bin", "gopls")
		writeOwnerTestFile(toolPath, "#!/bin/sh\n")

		controller := gomock.NewController(GinkgoT())
		host := NewMockHost(controller)
		host.EXPECT().Root().Return(root).Times(2)
		host.EXPECT().RunCommand(gomock.Any(), root, nil, "go", "env", "GOBIN").Return(CommandResult{}).Times(1)
		host.EXPECT().RunCommand(gomock.Any(), root, nil, "go", "env", "GOPATH").Return(CommandResult{
			Output: gopath, Stdout: gopath,
		}).Times(1)

		Expect(NewManager(host).findGoTool("gopls")).To(Equal(toolPath))
	})

	It("uses PATH for language-server binary lookups", func() {
		requirePOSIXExecutableFixture()
		root := GinkgoT().TempDir()
		binDir := filepath.Join(root, "bin")
		for _, name := range []string{"clangd", "rust-analyzer", "jdtls", "typescript-language-server"} {
			writeOwnerTestExecutable(filepath.Join(binDir, name))
		}
		GinkgoT().Setenv("PATH", binDir)

		controller := gomock.NewController(GinkgoT())
		manager := NewManager(NewMockHost(controller))
		Expect(map[string]string{
			"clangd":                     manager.clangdBinary(),
			"rust-analyzer":              manager.rustAnalyzerBinary(),
			"jdtls":                      manager.jdtlsBinary(),
			"typescript-language-server": manager.tsserverBinary(),
		}).To(Equal(map[string]string{
			"clangd":                     filepath.Join(binDir, "clangd"),
			"rust-analyzer":              filepath.Join(binDir, "rust-analyzer"),
			"jdtls":                      filepath.Join(binDir, "jdtls"),
			"typescript-language-server": filepath.Join(binDir, "typescript-language-server"),
		}))
	})

	It("prefers pyright over pylsp", func() {
		requirePOSIXExecutableFixture()
		root := GinkgoT().TempDir()
		binDir := filepath.Join(root, "bin")
		writeOwnerTestExecutable(filepath.Join(binDir, "pylsp"))
		writeOwnerTestExecutable(filepath.Join(binDir, "pyright-langserver"))
		GinkgoT().Setenv("PATH", binDir)

		controller := gomock.NewController(GinkgoT())
		path, server := NewManager(NewMockHost(controller)).pythonLSPBinary()
		Expect(path).To(Equal(filepath.Join(binDir, "pyright-langserver")))
		Expect(server).To(Equal("pyright"))
	})

	It("formats pylsp outgoing calls as structured relationships", func() {
		root := GinkgoT().TempDir()
		app := filepath.Join(root, "app.py")
		lib := filepath.Join(root, "lib.py")
		writeOwnerTestFile(app, "def run():\n    return helper()\n")
		writeOwnerTestFile(lib, "def helper():\n    return 'ok'\n")

		controller := gomock.NewController(GinkgoT())
		host := NewMockHost(controller)
		host.EXPECT().Root().Return(root).Times(1)
		host.EXPECT().DisplayPath(lib).Return("lib.py").Times(2)
		host.EXPECT().DisplayPath(app).Return("app.py").Times(1)
		host.EXPECT().HandleDisplay(app).Return("app.py").Times(1)
		host.EXPECT().HandleDisplay(lib).Return("lib.py").Times(1)
		host.EXPECT().MaxDirectoryEntries().Return(16).Times(2)
		manager := NewManager(host)

		payload := manager.formatPylspOutgoingCalls(app, "run", []LSPOutgoingCall{{
			To: LSPCallHierarchyItem{
				Name: "helper",
				URI:  LSPFileURI(lib),
				SelectionRange: LSPRange{
					Start: LSPPosition{Line: 0, Character: 4},
				},
			},
			FromRanges: []LSPRange{{Start: LSPPosition{Line: 1, Character: 11}}},
		}}, "")

		Expect(payload.Output).NotTo(BeEmpty())
		Expect(payload.Relationships).To(HaveLen(1))
		Expect(payload.Relationships[0]).To(Equal(ToolRelationship{
			Kind:         "calls",
			SourcePath:   "app.py",
			SourceLine:   2,
			SourceSymbol: "run",
			TargetPath:   "lib.py",
			TargetLine:   1,
			TargetSymbol: "helper",
			Evidence:     "app.py:2 -> helper in lib.py:1",
		}))
	})
})

func writeOwnerTestFile(path, content string) {
	GinkgoHelper()
	Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(Succeed())
	Expect(os.WriteFile(path, []byte(content), 0o644)).To(Succeed())
}

func writeOwnerTestExecutable(path string) {
	GinkgoHelper()
	Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(Succeed())
	Expect(os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755)).To(Succeed())
}

func requirePOSIXExecutableFixture() {
	GinkgoHelper()
	if runtime.GOOS == "windows" {
		Skip("executable fixture uses POSIX mode bits")
	}
}
