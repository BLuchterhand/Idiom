package pathparse

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Workspace path parsing", func() {
	It("does not extract backtick-delimited prose tokens", func() {
		text := "Each edit: `path` + (`old_text`+`new_text` | `start_line`+`end_line`+`content`)"
		for _, path := range MentionedWorkspacePaths(text) {
			Expect(path).NotTo(ContainSubstring("`"))
		}
	})

	It("returns an immutable copy of the plan file-scope vocabulary", func() {
		expected := []string{
			"expected file scope",
			"implementation file scope",
			"file scope",
			"files to modify",
			"files to edit",
			"write scope",
			"edit scope",
		}

		headings := PlanFileScopeHeadings()
		Expect(headings).To(Equal(expected))
		headings[0] = "mutated"
		Expect(PlanFileScopeHeadings()).To(Equal(expected))
	})

	It("recognizes Astro files as workspace files", func() {
		Expect(HasWorkspaceFileExtension("src/pages/contact.astro")).To(BeTrue())
	})

	DescribeTable("recognizes Windows absolute paths",
		func(path string, expected bool) {
			Expect(LooksLikeWindowsAbsolutePath(path)).To(Equal(expected))
		},
		Entry("uppercase drive and slashes", "C:/repo/file.go", true),
		Entry("lowercase drive and backslashes", `d:\repo\file.go`, true),
		Entry("numeric drive", "1:/repo/file.go", false),
		Entry("drive embedded in a relative path", "internal/C:/file.go", false),
		Entry("drive-relative path", "C:relative/file.go", false),
		Entry("Unix absolute path", "/repo/file.go", false),
		Entry("relative path", "repo/file.go", false),
		Entry("empty path", "", false),
	)

	DescribeTable("classifies absolute paths across platforms",
		func(path string, expected bool) {
			Expect(IsAbsolutePath(path)).To(Equal(expected))
		},
		Entry("Unix absolute", "/repo/file.go", true),
		Entry("Windows with slashes", "C:/repo/file.go", true),
		Entry("Windows with backslashes", `d:\repo\file.go`, true),
		Entry("relative", "repo/file.go", false),
		Entry("explicit relative", "./repo/file.go", false),
		Entry("Windows drive-relative", "C:relative/file.go", false),
		Entry("empty", "", false),
	)

	It("normalizes safe workspace-relative paths and rejects escapes", func() {
		for input, expected := range map[string]string{
			` ./pkg\feature.go `: "pkg/feature.go",
			"./pkg/../pkg.go":    "pkg.go",
			"pkg//nested//x.go":  "pkg/nested/x.go",
			"pkg/./feature.go":   "pkg/feature.go",
			"pkg/feature.go/":    "pkg/feature.go",
		} {
			Expect(NormalizeWorkspaceRelativePath(input)).To(Equal(expected), "input %q", input)
		}

		for _, path := range []string{"", ".", "..", "../outside.go", "/tmp/outside.go", `C:\tmp\outside.go`, "./C:/tmp/outside.go", "pkg/../..", "pkg/\x00file.go"} {
			Expect(NormalizeWorkspaceRelativePath(path)).To(BeEmpty(), "unsafe path %q", path)
		}
	})

	Describe("brief-body scoping", func() {
		It("extracts only the marked region", func() {
			composed := "preamble has `start_line`/`end_line` prose\n" +
				"<!-- IDIOM:BRIEF-BODY-START -->\n" +
				"internal/runtime/agent/runner.go\n" +
				"<!-- IDIOM:BRIEF-BODY-END -->\n" +
				"postamble has cmd/idiom/extra.go\n"

			scoped := ScopeToBriefBody(composed)
			Expect(scoped).To(ContainSubstring("internal/runtime/agent/runner.go"))
			Expect(scoped).NotTo(ContainSubstring("preamble"))
			Expect(scoped).NotTo(ContainSubstring("postamble"))
		})

		It("preserves input when markers are absent", func() {
			plain := "no markers here, internal/runtime/agent/runner.go"
			Expect(ScopeToBriefBody(plain)).To(Equal(plain))
		})

		It("keeps surrounding prompt paths out of extracted results", func() {
			composed := "preamble cites cmd/idiom/preamble_only.go\n" +
				"<!-- IDIOM:BRIEF-BODY-START -->\n" +
				"Files To Modify: internal/runtime/agent/runner.go\n" +
				"<!-- IDIOM:BRIEF-BODY-END -->\n"

			paths := MentionedWorkspacePaths(composed)
			Expect(paths).To(ContainElement("internal/runtime/agent/runner.go"))
			Expect(paths).NotTo(ContainElement("cmd/idiom/preamble_only.go"))
		})
	})

	Describe("workspace path token classification", func() {
		It("rejects tokens containing backticks without rejecting legitimate paths", func() {
			for _, candidate := range []string{"start_line`/`end_line", "`path`/foo.go", "foo.go`", "a`b/c.go"} {
				Expect(LooksLikeWorkspacePathToken(candidate)).To(BeFalse(), "candidate %q", candidate)
			}
			Expect(LooksLikeWorkspacePathToken("internal/runtime/agent/runner.go")).To(BeTrue())
		})

		It("rejects slash-separated prose while retaining workspace paths", func() {
			for _, candidate := range []string{"RIVAL/NEMESIS", "LONER/GREGARIOUS", "API/V1", "exclusive/missing"} {
				Expect(LooksLikeWorkspacePathToken(candidate)).To(BeFalse(), "candidate %q", candidate)
			}
			Expect(LooksLikeWorkspacePathToken("internal/API.md")).To(BeTrue())
			Expect(LooksLikeWorkspacePathToken("internal/capabilities/workspace")).To(BeTrue())
		})
	})

	It("ignores slash-separated prose in file-scope list rows", func() {
		text := strings.Join([]string{
			"Files To Modify:",
			"- native/tests/test_funeral.cpp — Add STRANGER, BEST_FRIEND, and RIVAL/NEMESIS coverage",
		}, "\n")
		Expect(MentionedWorkspacePathsUnderHeadings(text, "files to modify")).To(Equal([]string{"native/tests/test_funeral.cpp"}))
	})

	It("normalizes escaped-newline prefixes", func() {
		text := `"\ninternal/runtime/agent/runner_child_tasks.go" internal/runtime/agent/runner.go`
		Expect(MentionedWorkspacePaths(text)).To(Equal([]string{
			"internal/runtime/agent/runner_child_tasks.go",
			"internal/runtime/agent/runner.go",
		}))
	})

	It("extracts a backticked path before sentence punctuation", func() {
		Expect(MentionedWorkspacePaths("`cmd/idiom/product_workflow_test.go`.")).To(Equal([]string{"cmd/idiom/product_workflow_test.go"}))
	})

	It("extracts a backticked path with a line range", func() {
		Expect(MentionedWorkspacePaths("`internal/labels/labels.go:3-5`")).To(Equal([]string{"internal/labels/labels.go"}))
	})

	It("stops inline-list extraction after the path sentence", func() {
		text := "`cmd/idiom/product_workflow.go`, `cmd/idiom/product_workflow_test.go`. Add tests for mutually exclusive/missing mode flags and run `go test ./cmd/idiom`."
		Expect(MentionedWorkspacePathsInInlineList(text)).To(Equal([]string{
			"cmd/idiom/product_workflow.go",
			"cmd/idiom/product_workflow_test.go",
		}))
	})

	It("keeps only file-like targets from inline lists", func() {
		text := "`cmd/idiom`, `cmd/idiom/product_workflow.go`, `Makefile`. Run `go test ./cmd/idiom`."
		Expect(MentionedWorkspaceFilesInInlineList(text)).To(Equal([]string{"cmd/idiom/product_workflow.go", "Makefile"}))
	})

	It("extracts common source file extensions", func() {
		text := strings.Join([]string{
			"native/src/system.cpp",
			"native/include/system.hpp:12",
			"scripts/player.gd",
			"src/app.tsx",
			"pkg/service.py",
			"src/main.rs",
			"src/App.java",
			"tools/build.sh",
		}, " ")
		Expect(MentionedWorkspacePaths(text)).To(Equal([]string{
			"native/src/system.cpp",
			"native/include/system.hpp",
			"scripts/player.gd",
			"src/app.tsx",
			"pkg/service.py",
			"src/main.rs",
			"src/App.java",
			"tools/build.sh",
		}))
	})

	It("extracts paths only from selected headings", func() {
		text := strings.Join([]string{
			"# Narrow atom",
			"",
			"Files-to-Modify:",
			"- native/src/item_lifecycle.cpp",
			"- scripts/player.gd",
			"",
			"Evidence:",
			"- docs/design/spec.md",
		}, "\n")
		Expect(MentionedWorkspacePathsUnderHeadings(text, "files to modify", "files-to-modify")).To(Equal([]string{
			"native/src/item_lifecycle.cpp",
			"scripts/player.gd",
		}))
	})

	It("uses only the first cell of file-scope table rows", func() {
		text := strings.Join([]string{
			"# Narrow atom",
			"",
			"## Files To Modify",
			"",
			"| File | Change |",
			"| --- | --- |",
			"| native/tests/test_funeral.cpp | Add STRANGER, BEST_FRIEND, and RIVAL/NEMESIS coverage |",
			"| `native/src/simulation/creatures/social_welfare.cpp` | Adjust unburied penalty scaling |",
			"",
			"## Evidence",
			"- docs/design/spec.md",
		}, "\n")
		paths := MentionedWorkspacePathsUnderHeadings(text, "files to modify")
		Expect(paths).To(Equal([]string{
			"native/tests/test_funeral.cpp",
			"native/src/simulation/creatures/social_welfare.cpp",
		}))
		Expect(paths).NotTo(ContainElement("RIVAL/NEMESIS"))
	})
})
