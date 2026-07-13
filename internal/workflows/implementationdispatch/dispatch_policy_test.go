package implementationdispatch

import (
	"strings"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("Implementation dispatch policy", func() {
	ginkgo.It("requires refine for atomized plan documents", func() {
		gomega.Expect(PlanDocumentsNeedRefineBeforeDispatch([]PlanDocument{
			{Path: "docs/design.md"},
			{Path: ".idiom/atoms/design/atom-001.md"},
		})).To(gomega.BeTrue())
		gomega.Expect(PlanDocumentsNeedRefineBeforeDispatch([]PlanDocument{{Path: "docs/design.md"}})).To(gomega.BeFalse())
	})

	ginkgo.It("deduplicates test obligations while preserving order", func() {
		got := TestObligationsFromDocuments([]PlanDocument{
			{TestObligations: []string{"go test ./pkg", "go test ./pkg"}},
			{TestObligations: []string{"go test ./other"}},
		})
		gomega.Expect(got).To(gomega.Equal([]string{"go test ./pkg", "go test ./other"}))
	})

	ginkgo.It("carries typed new-file authority from the prompt", func() {
		prompt := strings.Join([]string{
			"Implement atom.",
			"Typed plan execution contract:",
			`{"authorized_new_files":[{"path":"pkg/new_helper.go"},{"path":"pkg/new_helper_test.go"}]}`,
			"Typed plan authority projection:",
			`{"atom_id":"atom-1"}`,
		}, "\n")
		gomega.Expect(AuthorizedNewFiles(prompt, nil)).To(gomega.Equal([]string{"pkg/new_helper.go", "pkg/new_helper_test.go"}))
	})

	ginkgo.It("carries typed new-file authority from a plan document", func() {
		doc := PlanDocument{Content: strings.Join([]string{
			"Plan document.",
			"Typed plan execution contract:",
			`{"authorized_new_files":[{"path":"pkg/from_doc.go"}]}`,
			"Typed plan authority projection:",
			`{"atom_id":"atom-1"}`,
		}, "\n")}
		gomega.Expect(AuthorizedNewFiles("Implement atom.", []PlanDocument{doc})).To(gomega.Equal([]string{"pkg/from_doc.go"}))
	})

	ginkgo.It("does not infer new-file authority from prose or malformed contracts", func() {
		prompt := strings.Join([]string{
			"Create new file pkg/new_helper.go with the following content.",
			"Typed plan execution contract:",
			`{"authorized_new_files":`,
			"Typed plan authority projection:",
			`{"atom_id":"atom-1"}`,
		}, "\n")
		gomega.Expect(AuthorizedNewFiles(prompt, nil)).To(gomega.BeEmpty())
	})

	ginkgo.DescribeTable("selecting the plan-execute token envelope from typed scope",
		func(prompt string, newFiles []string, existingFiles []string, docs []PlanDocument, want int) {
			gomega.Expect(PlanExecuteMaxTokens(prompt, newFiles, existingFiles, docs)).To(gomega.Equal(want))
		},
		ginkgo.Entry("bounded existing-file plan",
			"reject temp symlinks and add regression tests proving no partial mutation",
			nil,
			[]string{"internal/workflows/productworkflow/migration_apply.go", "internal/workflows/productworkflow/migration_apply_test.go"},
			nil,
			8192,
		),
		ginkgo.Entry("new-file plan",
			"create new file with the full file body",
			[]string{"internal/workflows/productworkflow/new_feature.go"},
			nil,
			nil,
			16384,
		),
		ginkgo.Entry("test prose does not inflate the envelope",
			"add regression tests proving the parser rejects invalid routes",
			[]string{"internal/parser/route_test.go"},
			nil,
			nil,
			8192,
		),
		ginkgo.Entry("plan-document new-file authority",
			"implement the referenced plan",
			[]string{"internal/contracts/writerroute/decision.go"},
			nil,
			[]PlanDocument{{
				Content:   "## Files To Modify\n\n- internal/contracts/writerroute/decision.go - NEW FILE\n\nUse replace_all:true with the full file body.",
				EditPaths: []string{"internal/contracts/writerroute/decision.go"},
			}},
			16384,
		),
		ginkgo.Entry("plan-document test prose does not inflate the envelope",
			"implement docs/parser-plan.md",
			nil,
			nil,
			[]PlanDocument{{
				Content:   "## Files To Modify\n\n- tests/parser_test.py\n\nAdd a regression test for invalid parser routes.",
				EditPaths: []string{"tests/parser_test.py"},
			}},
			8192,
		),
	)
})
