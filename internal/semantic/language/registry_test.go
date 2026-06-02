package language

import "testing"

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
