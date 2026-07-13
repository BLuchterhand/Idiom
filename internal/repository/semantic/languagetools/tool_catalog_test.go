package languagetools

import (
	"idiom/internal/repository/semantic/language"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Language tool catalog", func() {
	It("owns language and code-navigation capabilities", func() {
		Expect(ToolCatalog()).NotTo(BeEmpty())
		for _, expected := range []struct {
			tool         string
			language     string
			capability   language.Capability
			supplemental bool
		}{
			{tool: "go_find_function", language: "go", capability: language.CapFindSymbol, supplemental: true},
			{tool: "go_extract_body", language: "go", capability: language.CapExtractBody},
			{tool: "cpp_declaration", language: "cpp", capability: language.CapDefinition, supplemental: true},
			{tool: "rust_file_outline", language: "rust", capability: language.CapFileOutline},
		} {
			spec, ok := ToolSpecForTool(expected.tool)
			Expect(ok).To(BeTrue(), "tool %q should be cataloged", expected.tool)
			Expect(spec.Language).To(Equal(expected.language))
			Expect(spec.Capability).To(Equal(expected.capability))
			Expect(spec.Supplemental).To(Equal(expected.supplemental))
		}

		for _, navigation := range codeNavigationSpecs {
			Expect(navigation.Capability).NotTo(BeEmpty())

			for _, supportedLanguage := range language.LanguageRegistry() {
				tool := supportedLanguage.Tool(navigation.Capability)
				if tool == "" {
					continue
				}

				spec, ok := ToolSpecForTool(tool)
				Expect(ok).To(BeTrue(), "navigation capability %q routes uncataloged tool %q", navigation.Name, tool)
				Expect(spec.Capability).To(Equal(navigation.Capability))
			}
		}

		names := make([]string, 0, len(codeNavigationSpecs))
		for _, navigation := range codeNavigationSpecs {
			names = append(names, navigation.Name)
		}
		Expect(CodeNavigationCapabilityNames()).To(Equal(names))
	})

	It("splits only cataloged tool names", func() {
		languageName, suffix, ok := ToolParts("ts_file_outline")
		Expect(ok).To(BeTrue())
		Expect(languageName).To(Equal("typescript"))
		Expect(suffix).To(Equal("file_outline"))
		_, _, ok = ToolParts("go_fake_tool")
		Expect(ok).To(BeFalse())
	})
})
