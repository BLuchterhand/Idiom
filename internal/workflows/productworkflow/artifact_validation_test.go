package productworkflow

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("artifact validation", func() {
	ginkgo.It("ValidateProductArtifactRejectsInvalidPRD", func() {
		root := ginkgo.GinkgoT().TempDir()
		path := filepath.Join(root, "prd.md")
		writeProductArtifactValidationFile(path, `---
schema_version: idiom.prd.v1
prd_id: prd-test
status: draft
task_id: impl-test
created_at: 2026-05-17T00:00:00Z
---

# PRD: Test

## Requirements

| id | priority | statement |
| --- | --- | --- |
| REQ-1 | must | do it |
`)

		result := ValidateProductArtifact(ProductArtifactValidationOptions{
			Kind: "prd",
			Path: path,
		})
		gomega.Expect(result.Status).To(gomega.Equal(ProductWorkflowStatusFail), fmt.Sprintf("expected parser failure, got %#v", result))
		gomega.Expect(result.Error).To(gomega.ContainSubstring("PRD has no requirements"), fmt.Sprintf("expected parser failure, got %#v", result))

	})
	ginkgo.It("ValidateProductArtifactRejectsUnknownADRTDDObligation", func() {
		root := ginkgo.GinkgoT().TempDir()
		prdPath := filepath.Join(root, "prd.md")
		tddPath := filepath.Join(root, "tdd.json")
		adrPath := filepath.Join(root, "adr.md")

		writeProductArtifactValidationFile(prdPath, RenderPRDMarkdown(validPRDDoc()))
		writeProductArtifactValidationJSON(tddPath, TDDLedger{
			SchemaVersion: "idiom.tdd_obligations.v2.fixture",
			EffortID:      "test-effort",
			TaskID:        "task-001",
			CoverageTier:  "unit",
			Coverage:      TDDCoverage{MutationMinScore: 80},
			Obligations: []TDDObligation{{
				ID:             "T1",
				Type:           "acceptance",
				Behavior:       "test",
				Owner:          "test",
				Command:        "go test",
				RequiredBefore: "close",
				Status:         "pending",
			}},
		})

		adr := validADRDoc()
		adr.FrontMatter.TDDObligations = []string{"missing-generated-obligation"}
		writeProductArtifactValidationFile(adrPath, RenderADRMarkdown(adr))

		result := ValidateProductArtifact(ProductArtifactValidationOptions{
			Kind:          "adr",
			Path:          adrPath,
			PRDPath:       prdPath,
			TDDLedgerPath: tddPath,
		})
		gomega.Expect(result.Status).To(gomega.Equal(ProductWorkflowStatusFail), fmt.Sprintf("expected ADR TDD cross-link failure, got %#v", result))
		gomega.Expect(result.Error).To(gomega.ContainSubstring("tdd_obligation"), fmt.Sprintf("expected ADR TDD cross-link failure, got %#v", result))

	})
})

func writeProductArtifactValidationFile(path string, body string) {
	ginkgo.GinkgoHelper()
	{
		err := os.MkdirAll(filepath.Dir(path), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir %s: %v", filepath.Dir(path), err))
	}
	{

		err := os.WriteFile(path, []byte(body), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write %s: %v", path, err))
	}

}

func writeProductArtifactValidationJSON(path string, value any) {
	ginkgo.GinkgoHelper()
	data, err := json.Marshal(value)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal fixture: %v", err))

	writeProductArtifactValidationFile(path, string(data))
}
