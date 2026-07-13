package languagetools_test

import (
	"os"
	"path/filepath"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/repository/semantic/language"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func languageDetects(root string, name string) bool {
	lang, ok := language.LanguageForName(name)
	return ok && lang.Detect != nil && lang.Detect(root)
}

func writeTestFile(path string, content string) {
	GinkgoHelper()
	Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(Succeed())
	Expect(os.WriteFile(path, []byte(content), 0o644)).To(Succeed())
}

func expectToolSuccess(result workspace.ToolResult, outputSubstrings ...string) {
	GinkgoHelper()
	Expect(result.Error).To(BeEmpty())
	for _, substring := range outputSubstrings {
		Expect(result.Output).To(ContainSubstring(substring))
	}
}
