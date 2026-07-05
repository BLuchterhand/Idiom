package language

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestLanguageRegistryIncludesCanonicalToolFamilies(t *testing.T) {
	registry := LanguageRegistry()
	if len(registry) == 0 {
		t.Fatal("LanguageRegistry returned no languages")
	}

	want := map[string]string{
		"go":         "go_read_symbol",
		"cpp":        "cpp_read_symbol",
		"python":     "python_read_symbol",
		"typescript": "ts_read_symbol",
		"rust":       "rust_read_symbol",
		"java":       "java_read_symbol",
	}
	for name, tool := range want {
		lang, ok := LanguageForName(name)
		if !ok {
			t.Fatalf("LanguageForName(%q) not found", name)
		}

		if got := lang.Tool(CapReadSymbol); got != tool {
			t.Fatalf("%s read-symbol tool = %q, want %q", name, got, tool)
		}
	}
}

func TestLanguageAliasesAndExtensions(t *testing.T) {
	if lang, ok := LanguageForName("js"); !ok || lang.Name != "typescript" {
		t.Fatalf("LanguageForName(js) = %#v, %v; want typescript", lang, ok)
	}

	if lang, ok := LanguageForExtension(".tsx"); !ok || lang.Name != "typescript" {
		t.Fatalf("LanguageForExtension(.tsx) = %#v, %v; want typescript", lang, ok)
	}

	if lang, ok := LanguageForExtension(".hpp"); !ok || lang.Name != "cpp" {
		t.Fatalf("LanguageForExtension(.hpp) = %#v, %v; want cpp", lang, ok)
	}
}

func TestDiagnosticToolForLanguageUsesRegistryAliases(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{name: "go", want: "go_diagnostics"},
		{name: "javascript", want: "ts_diagnostics"},
		{name: "js", want: "ts_diagnostics"},
		{name: "unknown", want: ""},
		{name: "", want: ""},
	}

	for _, tc := range tests {
		if got := DiagnosticToolForLanguage(tc.name); got != tc.want {
			t.Fatalf("DiagnosticToolForLanguage(%q) = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestActiveLanguagesDetectsMarkersAndExtensions(t *testing.T) {
	tests := []struct {
		name string
		file string
		lang string
	}{
		{name: "go marker", file: "go.mod", lang: "go"},
		{name: "cpp marker", file: "compile_commands.json", lang: "cpp"},
		{name: "cpp extension", file: "src/main.cpp", lang: "cpp"},
		{name: "python marker", file: "pyproject.toml", lang: "python"},
		{name: "python extension", file: "pkg/mod.py", lang: "python"},
		{name: "typescript marker", file: "package.json", lang: "typescript"},
		{name: "typescript extension", file: "src/app.tsx", lang: "typescript"},
		{name: "rust marker", file: "Cargo.toml", lang: "rust"},
		{name: "rust extension", file: "src/lib.rs", lang: "rust"},
		{name: "java marker", file: "pom.xml", lang: "java"},
		{name: "java extension", file: "src/Main.java", lang: "java"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			writeRegistryTestFile(t, filepath.Join(root, tt.file))

			if !activeLanguageNames(root).contains(tt.lang) {
				t.Fatalf("ActiveLanguages(%s) did not include %s", tt.file, tt.lang)
			}
		})
	}
}

func TestActiveLanguagesIgnoreHiddenFilesAndSkippedDirs(t *testing.T) {
	tests := []struct {
		name string
		file string
		lang string
	}{
		{name: "hidden cpp file", file: ".main.cpp", lang: "cpp"},
		{name: "python virtual env", file: ".venv/pkg/mod.py", lang: "python"},
		{name: "typescript node modules", file: "node_modules/app.ts", lang: "typescript"},
		{name: "rust git dir", file: ".git/src/lib.rs", lang: "rust"},
		{name: "java build dir", file: "build/Main.java", lang: "java"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			writeRegistryTestFile(t, filepath.Join(root, tt.file))

			if activeLanguageNames(root).contains(tt.lang) {
				t.Fatalf("ActiveLanguages(%s) included skipped language %s", tt.file, tt.lang)
			}
		})
	}
}

func TestTypeScriptDetectorSkipsNodeModulesRoot(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "node_modules")
	writeRegistryTestFile(t, filepath.Join(root, "app.ts"))

	if activeLanguageNames(root).contains("typescript") {
		t.Fatal("ActiveLanguages included typescript for node_modules workspace root")
	}
}

type languageNames []string

func activeLanguageNames(root string) languageNames {
	var names languageNames
	for _, lang := range ActiveLanguages(root) {
		names = append(names, lang.Name)
	}
	slices.Sort(names)
	return names
}

func (names languageNames) contains(name string) bool {
	return slices.Contains(names, name)
}

func writeRegistryTestFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte("fixture\n"), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
