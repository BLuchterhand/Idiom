package languagetools_test

import (
	"os"
	"path/filepath"
	"testing"

	"idiom/internal/repository/semantic/language"
)

func languageDetects(root string, name string) bool {
	lang, ok := language.LanguageForName(name)
	return ok && lang.Detect != nil && lang.Detect(root)
}

func writeTestFile(t *testing.T, path string, content string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
