package postflight

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Generic postflight checks", func() {
	Describe("unresolved question markers", func() {
		It("flags a marker introduced on an added line", func() {
			diff := Diff{Files: []FileChange{{
				Path:       "src/broken.go",
				AddedLines: []AddedLine{{Line: 2, Body: "// <<QUESTION>>: did the model resolve this?"}},
			}}}

			findings, err := UnresolvedQuestionMarker{}.Run(diff, RunContext{})
			Expect(err).NotTo(HaveOccurred())
			Expect(findings).To(HaveLen(1))
			Expect(findings[0].Severity).To(Equal(SeverityError))
			Expect(findings[0].Path).To(Equal("src/broken.go"))
			Expect(findings[0].Line).To(Equal(2))
		})

		It("ignores a pre-existing marker outside added lines", func() {
			diff := Diff{Files: []FileChange{{
				Path:       "docs/protocol.md",
				AddedLines: []AddedLine{{Line: 12, Body: "New prose about the protocol."}},
			}}}
			findings, err := UnresolvedQuestionMarker{}.Run(diff, RunContext{})
			Expect(err).NotTo(HaveOccurred())
			Expect(findings).To(BeEmpty())
		})

		It("ignores deleted files", func() {
			findings, err := UnresolvedQuestionMarker{}.Run(
				Diff{Files: []FileChange{{Path: "gone.go", Deleted: true}}},
				RunContext{ProjectRoot: GinkgoT().TempDir()},
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(findings).To(BeEmpty())
		})

		It("returns no findings for a clean added line", func() {
			diff := Diff{Files: []FileChange{{
				Path:       "ok.go",
				AddedLines: []AddedLine{{Line: 2, Body: "func OK() {}"}},
			}}}
			findings, err := UnresolvedQuestionMarker{}.Run(diff, RunContext{})
			Expect(err).NotTo(HaveOccurred())
			Expect(findings).To(BeEmpty())
		})
	})

	Describe("file allowlists", func() {
		It("flags a write outside required edit paths", func() {
			diff := Diff{Files: []FileChange{{Path: "a/in_scope.go"}, {Path: "a/out_of_scope.go"}}}
			findings, err := FileAllowlistMismatch{}.Run(diff, RunContext{RequiredEditPaths: []string{"a/in_scope.go"}})
			Expect(err).NotTo(HaveOccurred())
			Expect(findings).To(HaveLen(1))
			Expect(findings[0].Path).To(Equal("a/out_of_scope.go"))
			Expect(findings[0].Severity).To(Equal(SeverityError))
		})

		It("does nothing when no allowlist is supplied", func() {
			findings, err := FileAllowlistMismatch{}.Run(
				Diff{Files: []FileChange{{Path: "a.go"}, {Path: "b.go"}}},
				RunContext{},
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(findings).To(BeEmpty())
		})

		It("normalizes common path prefixes", func() {
			findings, err := FileAllowlistMismatch{}.Run(
				Diff{Files: []FileChange{{Path: "a/b.go"}}},
				RunContext{RequiredEditPaths: []string{"./a/b.go", "other/dir/"}},
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(findings).To(BeEmpty())
		})
	})

	Describe("coding rules", func() {
		It("does nothing without a project policy", func() {
			findings, err := CodingRulesCheck{}.Run(
				Diff{Files: []FileChange{{Path: "wrapper.go"}}},
				RunContext{ProjectRoot: GinkgoT().TempDir()},
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(findings).To(BeEmpty())
		})

		It("maps a blocking violation to an error", func() {
			root := GinkgoT().TempDir()
			writePostflightFile(root, ".idiom/coding_rules.json", codingRulesPolicy("blocking"))
			writePostflightFile(root, "wrapper.go", passThroughWrapperSource)

			findings, err := CodingRulesCheck{}.Run(
				Diff{Files: []FileChange{{Path: "wrapper.go"}}},
				RunContext{ProjectRoot: root},
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(findings).To(HaveLen(1))
			Expect(findings[0].Severity).To(Equal(SeverityError))
			Expect(findings[0].Check).To(Equal("coding_rules"))
		})

		It("keeps an advisory error non-blocking", func() {
			root := GinkgoT().TempDir()
			writePostflightFile(root, ".idiom/coding_rules.json", codingRulesPolicy("advisory"))
			writePostflightFile(root, "wrapper.go", passThroughWrapperSource)

			findings, err := CodingRulesCheck{}.Run(
				Diff{Files: []FileChange{{Path: "wrapper.go"}}},
				RunContext{ProjectRoot: root},
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(findings).To(HaveLen(1))
			Expect(findings[0].Severity).To(Equal(SeverityWarning))
		})
	})

	Describe("narrating test comments", func() {
		It("flags added narration with its post-image line", func() {
			diff := Diff{Files: []FileChange{{
				Path: "foo/bar_test.go",
				AddedLines: []AddedLine{
					{Line: 1, Body: "//   This Test verifies   that Foo returns 7"},
					{Line: 2, Body: "func TestFooReturnsSeven(t *testing.T) {"},
					{Line: 3, Body: "\tif Foo() != 7 { t.Fatal() }"},
				},
			}}}

			findings, err := NarratingTestComments{}.Run(diff, RunContext{})
			Expect(err).NotTo(HaveOccurred())
			Expect(findings).To(HaveLen(1))
			Expect(findings[0].Severity).To(Equal(SeverityWarning))
			Expect(findings[0].Line).To(Equal(1))
		})

		It("does not flag borderline phrases", func() {
			diff := Diff{Files: []FileChange{{
				Path: "foo/bar_test.go",
				AddedLines: []AddedLine{
					{Line: 1, Body: "// verifying invariants below"},
					{Line: 2, Body: "// when we test the parser, make sure to seed"},
					{Line: 3, Body: "// does this test cover the empty case?"},
					{Line: 4, Body: "// see issue #123 for a related test"},
					{Line: 5, Body: "// TODO: add a test that handles negative input"},
				},
			}}}
			findings, err := NarratingTestComments{}.Run(diff, RunContext{})
			Expect(err).NotTo(HaveOccurred())
			Expect(findings).To(BeEmpty())
		})

		It("preserves non-contiguous post-image line numbers", func() {
			diff := Diff{Files: []FileChange{{
				Path: "foo/bar_test.go",
				AddedLines: []AddedLine{
					{Line: 42, Body: "// This test verifies parsing"},
					{Line: 99, Body: "// makes sure the result is non-nil"},
				},
			}}}
			findings, err := NarratingTestComments{}.Run(diff, RunContext{})
			Expect(err).NotTo(HaveOccurred())
			Expect(findings).To(HaveLen(2))
			Expect(findings[0].Line).To(Equal(42))
			Expect(findings[1].Line).To(Equal(99))
		})

		It("ignores production files", func() {
			diff := Diff{Files: []FileChange{{
				Path:       "foo/bar.go",
				AddedLines: []AddedLine{{Line: 1, Body: "// This function ensures that the Foo invariant holds"}},
			}}}
			findings, err := NarratingTestComments{}.Run(diff, RunContext{})
			Expect(err).NotTo(HaveOccurred())
			Expect(findings).To(BeEmpty())
		})

		It("accepts legitimate test comments", func() {
			diff := Diff{Files: []FileChange{{
				Path: "foo/bar_test.go",
				AddedLines: []AddedLine{
					{Line: 1, Body: "// see issue #123 for context"},
					{Line: 2, Body: "// TODO(2026-06-01): tighten the bound here"},
					{Line: 3, Body: "// Copyright 2026 Acme"},
				},
			}}}
			findings, err := NarratingTestComments{}.Run(diff, RunContext{})
			Expect(err).NotTo(HaveOccurred())
			Expect(findings).To(BeEmpty())
		})

		It("recognizes Python and C++ test filenames", func() {
			diff := Diff{Files: []FileChange{
				{Path: "tests/test_foo.py", AddedLines: []AddedLine{{Line: 1, Body: "# This test verifies parsing"}}},
				{Path: "tests/foo_test.cpp", AddedLines: []AddedLine{{Line: 1, Body: "// Make sure the parser handles UTF-8"}}},
			}}
			findings, err := NarratingTestComments{}.Run(diff, RunContext{})
			Expect(err).NotTo(HaveOccurred())
			Expect(findings).To(HaveLen(2))
			Expect([]string{findings[0].Path, findings[1].Path}).To(ConsistOf("tests/test_foo.py", "tests/foo_test.cpp"))
		})
	})

	DescribeTable("extracts comment bodies",
		func(input, expected string) {
			Expect(commentBody(input)).To(Equal(expected))
		},
		Entry("slash comment", "// hello", "hello"),
		Entry("spaced slash comment", "   //   hello   ", "hello"),
		Entry("hash comment", "# hello", "hello"),
		Entry("closed block comment", "/* block */", "block"),
		Entry("open block comment", "/* block", "block"),
		Entry("block continuation", " * continuation", "continuation"),
		Entry("non-comment", "not a comment", ""),
	)

	It("returns the canonical deterministic check set", func() {
		checks := GenericChecks()
		names := make([]string, 0, len(checks))
		for _, check := range checks {
			names = append(names, check.Name())
		}
		Expect(names).To(Equal([]string{
			"unresolved_question_marker",
			"file_allowlist_mismatch",
			"narrating_test_comments",
			"go_build_passes",
			"coding_rules",
		}))
		Expect(GenericChecks()).To(HaveLen(len(checks)))
		Expect(checks[0].Name()).NotTo(BeEmpty())
	})
})

const passThroughWrapperSource = `package sample

func target(v string) string { return v }

func Wrapper(v string) string {
	return target(v)
}
`

func codingRulesPolicy(enforcement string) string {
	return `{
		"schema_version":"idiom.coding_rules.v1",
		"rules":[{
			"id":"go.no_pass_through_wrapper",
			"enforcement":"` + enforcement + `",
			"severity":"error"
		}]
	}`
}

func writePostflightFile(root string, rel string, body string) {
	GinkgoHelper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(Succeed())
	Expect(os.WriteFile(path, []byte(body), 0o644)).To(Succeed())
}
