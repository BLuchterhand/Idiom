package language

import (
	"os"
	"path/filepath"
	"slices"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Language registry", func() {
	It("registers the canonical read-symbol tool families", func() {
		Expect(LanguageRegistry()).NotTo(BeEmpty())
		for name, tool := range map[string]string{
			"go":         "go_read_symbol",
			"cpp":        "cpp_read_symbol",
			"python":     "python_read_symbol",
			"typescript": "ts_read_symbol",
			"rust":       "rust_read_symbol",
			"java":       "java_read_symbol",
		} {
			lang, ok := LanguageForName(name)
			Expect(ok).To(BeTrue(), "language %q should be registered", name)
			Expect(lang.Tool(CapReadSymbol)).To(Equal(tool))
		}
	})

	It("registers GDScript references and incoming calls", func() {
		lang, ok := LanguageForName("gdscript")
		Expect(ok).To(BeTrue())
		Expect(lang.Extensions).To(ContainElement(".gd"))
		Expect(lang.Tool(CapReferences)).To(Equal("gdscript_references"))
		Expect(lang.Tool(CapCallsInto)).To(Equal("gdscript_calls_into"))
		Expect(lang.Tool(CapCallsFrom)).To(BeEmpty())
	})

	DescribeTable("resolves aliases and extensions",
		func(resolve func() (LanguageSupport, bool), expectedName string) {
			lang, ok := resolve()
			Expect(ok).To(BeTrue())
			Expect(lang.Name).To(Equal(expectedName))
		},
		Entry("JavaScript alias", func() (LanguageSupport, bool) { return LanguageForName("js") }, "typescript"),
		Entry("TSX extension", func() (LanguageSupport, bool) { return LanguageForExtension(".tsx") }, "typescript"),
		Entry("C++ header extension", func() (LanguageSupport, bool) { return LanguageForExtension(".hpp") }, "cpp"),
	)

	DescribeTable("selects diagnostic tools through registry aliases",
		func(name, expected string) {
			Expect(DiagnosticToolForLanguage(name)).To(Equal(expected))
		},
		Entry("Go", "go", "go_diagnostics"),
		Entry("JavaScript", "javascript", "ts_diagnostics"),
		Entry("JS alias", "js", "ts_diagnostics"),
		Entry("unknown language", "unknown", ""),
		Entry("empty language", "", ""),
	)

	DescribeTable("detects active languages from markers and source extensions",
		func(file, expectedLanguage string) {
			root := GinkgoT().TempDir()
			writeRegistryTestFile(filepath.Join(root, file))
			Expect(activeLanguageNames(root)).To(ContainElement(expectedLanguage))
		},
		Entry("Go marker", "go.mod", "go"),
		Entry("C++ marker", "compile_commands.json", "cpp"),
		Entry("C++ extension", "src/main.cpp", "cpp"),
		Entry("Python marker", "pyproject.toml", "python"),
		Entry("Python extension", "pkg/mod.py", "python"),
		Entry("TypeScript marker", "package.json", "typescript"),
		Entry("TypeScript extension", "src/app.tsx", "typescript"),
		Entry("Rust marker", "Cargo.toml", "rust"),
		Entry("Rust extension", "src/lib.rs", "rust"),
		Entry("Java marker", "pom.xml", "java"),
		Entry("Java extension", "src/Main.java", "java"),
		Entry("Godot project marker", "project.godot", "gdscript"),
		Entry("GDScript extension", "scripts/main.gd", "gdscript"),
	)

	DescribeTable("ignores hidden files and skipped directories",
		func(file, language string) {
			root := GinkgoT().TempDir()
			writeRegistryTestFile(filepath.Join(root, file))
			Expect(activeLanguageNames(root)).NotTo(ContainElement(language))
		},
		Entry("hidden C++ file", ".main.cpp", "cpp"),
		Entry("Python virtual environment", ".venv/pkg/mod.py", "python"),
		Entry("TypeScript node modules", "node_modules/app.ts", "typescript"),
		Entry("Rust git directory", ".git/src/lib.rs", "rust"),
		Entry("Java build directory", "build/Main.java", "java"),
	)

	It("ignores a node_modules workspace root", func() {
		root := filepath.Join(GinkgoT().TempDir(), "node_modules")
		writeRegistryTestFile(filepath.Join(root, "app.ts"))
		Expect(activeLanguageNames(root)).NotTo(ContainElement("typescript"))
	})
})

func activeLanguageNames(root string) []string {
	var names []string
	for _, lang := range ActiveLanguages(root) {
		names = append(names, lang.Name)
	}
	slices.Sort(names)
	return names
}

func writeRegistryTestFile(path string) {
	Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(Succeed())
	Expect(os.WriteFile(path, []byte("fixture\n"), 0o644)).To(Succeed())
}
