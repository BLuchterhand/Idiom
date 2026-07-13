package languagetools_test

import (
	"context"
	"path/filepath"
	"strings"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/platform/config"
	"idiom/internal/repository/semantic/language"
	"idiom/internal/repository/semantic/languagetools"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Code navigation", func() {
	DescribeTable("normalizes capability aliases",
		func(capability, expectedName string, expectedCapability language.Capability) {
			spec, err := languagetools.CodeNavigationSpecFor(capability)
			Expect(err).NotTo(HaveOccurred())
			Expect(spec.Name).To(Equal(expectedName))
			Expect(spec.Capability).To(Equal(expectedCapability))
		},
		Entry("definition", "definition", "definition", language.CapDefinition),
		Entry("find definition", "find_definition", "definition", language.CapDefinition),
		Entry("find definitions", "find_definitions", "definition", language.CapDefinition),
		Entry("hyphen definition", "find-definition", "definition", language.CapDefinition),
		Entry("space definition", "find definition", "definition", language.CapDefinition),
		Entry("find references", "find_references", "references", language.CapReferences),
		Entry("hyphen references", "find-references", "references", language.CapReferences),
		Entry("space references", "find references", "references", language.CapReferences),
		Entry("extract body", "extract_body", "extract_body", language.CapExtractBody),
		Entry("full body", "full_body", "extract_body", language.CapExtractBody),
	)

	It("keeps advertised capability names and helpers aligned", func() {
		names := languagetools.CodeNavigationCapabilityNames()
		Expect(names).NotTo(BeEmpty())

		for _, name := range names {
			spec, err := languagetools.CodeNavigationSpecFor(name)
			Expect(err).NotTo(HaveOccurred())
			Expect(spec.Name).To(Equal(name))
		}

		_, err := languagetools.CodeNavigationSpecFor("not_a_capability")
		Expect(err).To(HaveOccurred())
		for _, name := range names {
			Expect(err.Error()).To(ContainSubstring(name))
		}

		Expect(languagetools.CodeNavigationCapabilityList()).To(Equal(strings.Join(names, ", ")))
	})

	It("routes a definition alias from a symbol pattern", func() {
		root := GinkgoT().TempDir()
		writeTestFile(filepath.Join(root, "go.mod"), "module example.com/navalias\n\ngo 1.22\n")
		writeTestFile(filepath.Join(root, "internal", "transcript", "debuglog.go"), `package transcript

type DebugLogMode string

const DebugLogModeRedacted DebugLogMode = "redacted"
`)

		manager := workspace.NewManager(config.Workspace{Root: root, CWD: root})
		result := manager.Execute(context.Background(), workspace.ToolRequest{
			Tool:       "code_navigate",
			Capability: "find_definition",
			Pattern:    "DebugLogMode",
			Language:   "go",
		})

		Expect(result.Error).To(BeEmpty())
		Expect(result.Output).To(And(
			ContainSubstring("capability: definition"),
			ContainSubstring("routed_tool: go_definition"),
			ContainSubstring("route_summary: Resolved Go definition"),
		))
	})
})
