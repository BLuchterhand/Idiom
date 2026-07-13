package plan

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Typed execution contracts in prompts", func() {
	It("extracts acceptance evidence IDs from an atom contract", func() {
		body, err := json.Marshal(AtomExecutionContract{
			AcceptanceObligations: []AcceptanceEvidenceRequired{
				{ID: "coverage-threshold", Required: true},
				{ID: "optional-note", Required: false},
				{ID: "review-evidence", Required: true},
			},
		})
		Expect(err).NotTo(HaveOccurred())
		prompt := "brief\n\n" + PromptAtomExecutionContractMarker + "\n" + string(body)
		Expect(RequiredAcceptanceEvidenceIDsFromPrompt(prompt)).To(Equal([]string{
			"coverage-threshold", "optional-note", "review-evidence",
		}))
	})

	It("extracts plan evidence before a malformed atom projection", func() {
		body, err := json.Marshal(PlanExecutionContract{Atoms: []AtomExecutionContract{{
			AcceptanceObligations: []AcceptanceEvidenceRequired{{ID: "plan-evidence", Required: true}},
		}}})
		Expect(err).NotTo(HaveOccurred())
		prompt := "brief\n\n" + PromptPlanExecutionContractMarker + "\n" + string(body) + "\n" +
			PromptAtomExecutionContractMarker + "\nnot json"
		Expect(RequiredAcceptanceEvidenceIDsFromPrompt(prompt)).To(Equal([]string{"plan-evidence"}))
	})

	It("parses nested JSON containing braces inside strings", func() {
		prompt := `brief

Typed plan authority projection:
{
  "atom_id": "atom-braces",
  "scope": {"required_coverage_paths": ["pkg/feature.go"]},
  "acceptance_obligations": [{
    "id": "braced-string",
    "description": "keep a literal { brace } inside this string",
    "required": true
  }]
}`
		atom, ok := AtomExecutionContractFromPrompt(prompt)
		Expect(ok).To(BeTrue())
		Expect(atom.AtomID).To(Equal("atom-braces"))
		Expect(RequiredAcceptanceEvidenceIDsFromPrompt(prompt)).To(Equal([]string{"braced-string"}))
	})

	It("prefers atom authority over plan authority", func() {
		planBody, err := json.Marshal(PlanExecutionContract{Atoms: []AtomExecutionContract{{
			AcceptanceObligations: []AcceptanceEvidenceRequired{{ID: "plan-only", Required: true}},
		}}})
		Expect(err).NotTo(HaveOccurred())
		atomBody, err := json.Marshal(AtomExecutionContract{
			AcceptanceObligations: []AcceptanceEvidenceRequired{{ID: "atom-wins", Required: true}},
		})
		Expect(err).NotTo(HaveOccurred())
		prompt := PromptPlanExecutionContractMarker + "\n" + string(planBody) + "\n\n" +
			PromptAtomExecutionContractMarker + "\n" + string(atomBody)
		Expect(RequiredAcceptanceEvidenceIDsFromPrompt(prompt)).To(Equal([]string{"atom-wins"}))
	})

	DescribeTable("ignores malformed or missing acceptance contracts",
		func(prompt string) {
			Expect(RequiredAcceptanceEvidenceIDsFromPrompt(prompt)).To(BeEmpty())
		},
		Entry("missing", "brief without a typed contract"),
		Entry("malformed atom", PromptAtomExecutionContractMarker+"\nnot json"),
		Entry("malformed plan", PromptPlanExecutionContractMarker+"\n{\"atoms\":["),
	)

	It("stops authorized-new-file parsing before atom projection", func() {
		body, err := json.Marshal(PlanExecutionContract{
			AuthorizedNewFiles: []AuthorizedNewFile{{Path: "pkg/new_helper.go"}},
		})
		Expect(err).NotTo(HaveOccurred())
		prompt := "brief\n\n" + PromptPlanExecutionContractMarker + "\n" + string(body) + "\n" +
			PromptAtomExecutionContractMarker + "\nnot json"
		Expect(AuthorizedNewFilePathsFromPrompt(prompt)).To(Equal([]string{"pkg/new_helper.go"}))
	})

	It("normalizes and deduplicates authorized new files", func() {
		body, err := json.Marshal(PlanExecutionContract{AuthorizedNewFiles: []AuthorizedNewFile{
			{Path: "pkg/b.go"}, {Path: " ./pkg/a.go "}, {Path: "pkg/b.go"}, {Path: "../outside.go"},
		}})
		Expect(err).NotTo(HaveOccurred())
		prompt := PromptPlanExecutionContractMarker + "\n" + string(body)
		Expect(AuthorizedNewFilePathsFromPrompt(prompt)).To(Equal([]string{"pkg/a.go", "pkg/b.go"}))
	})

	DescribeTable("ignores malformed or missing authorized-file contracts",
		func(prompt string) {
			Expect(AuthorizedNewFilePathsFromPrompt(prompt)).To(BeEmpty())
		},
		Entry("missing", "brief without typed plan contract"),
		Entry("malformed", PromptPlanExecutionContractMarker+"\nnot json"),
		Entry("truncated", PromptPlanExecutionContractMarker+"\n{\"authorized_new_files\":["),
	)
})
