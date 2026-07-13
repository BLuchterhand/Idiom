package productworkflow

import (
	"fmt"
	"os"
	"path/filepath"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("obsidian parity", func() {
	ginkgo.It("ObsidianParityDetectsStaleStatusWithoutRationale", func() {
		root := ginkgo.GinkgoT().TempDir()
		mustWrite(filepath.Join(root, ObsidianProductWorkflowPath), "> **Status: NOT STARTED**")
		mustWrite(filepath.Join(root, ObsidianRecoveryPath), "# Recovery and Decomposition")
		mustWrite(filepath.Join(root, "reports", "impl-0032-investigation.json"), "{}")

		ws := &WorkflowState{
			EffortID:     "prd-adr-brief-workflow",
			TaskID:       "impl-0032-phase-5-obsidian-parity-check",
			CurrentPhase: "phase-5-execution-feedback",
		}
		got := EvaluateObsidianParity(root, ws, nil, nil)
		gomega.Expect(len(got.Findings) == 0).To(gomega.BeFalse(), fmt.Sprintf("expected stale parity findings, got none"))

	})
	ginkgo.It("ObsidianParityAllowsNoChangeRationale", func() {
		root := ginkgo.GinkgoT().TempDir()
		mustWrite(filepath.Join(root, ObsidianProductWorkflowPath), "> **Status: NOT STARTED**")
		mustWrite(filepath.Join(root, ObsidianRecoveryPath), "# Recovery and Decomposition")

		ws := &WorkflowState{
			EffortID:     "prd-adr-brief-workflow",
			TaskID:       "impl-0032-phase-5-obsidian-parity-check",
			CurrentPhase: "phase-5-execution-feedback",
		}
		investigation := InvestigationReport{
			TaskID: ws.TaskID,
			Scope: InvestigationScope{
				ObsidianNoChangeRationale: "No updates required because durable and Obsidian statuses already align for this slice.",
			},
		}
		got := EvaluateObsidianParity(root, ws, nil, []InvestigationReport{investigation})
		gomega.Expect(len(got.Findings) != 0).To(gomega.BeFalse(), fmt.Sprintf("expected no findings with no-change rationale, got %#v", got.Findings))

	})
	ginkgo.It("ObsidianParitySkipsUnrelatedEffortWithoutCheckedFiles", func() {
		root := ginkgo.GinkgoT().TempDir()
		mustWrite(filepath.Join(root, ObsidianProductWorkflowPath), "> **Status: NOT STARTED**")
		mustWrite(filepath.Join(root, ObsidianRecoveryPath), "# Recovery and Decomposition")

		ws := &WorkflowState{
			EffortID:     "other-effort",
			TaskID:       "impl-test",
			CurrentPhase: "phase-5-execution-feedback",
		}
		got := EvaluateObsidianParity(root, ws, nil, nil)
		gomega.Expect(len(got.CheckedFiles) != 0).To(gomega.BeFalse(), fmt.Sprintf("expected no checked files for skipped effort, got %#v", got.CheckedFiles))
		gomega.Expect(len(got.Findings) != 0).To(gomega.BeFalse(), fmt.Sprintf("expected no findings for skipped effort, got %#v", got.Findings))

	})
	ginkgo.It("ObsidianParitySkipsWhenVaultIsAbsent", func() {
		root := ginkgo.GinkgoT().TempDir()
		ws := &WorkflowState{
			EffortID:     "prd-adr-brief-workflow",
			TaskID:       "impl-0032-phase-5-obsidian-parity-check",
			CurrentPhase: "phase-5-execution-feedback",
		}

		got := EvaluateObsidianParity(root, ws, nil, nil)
		gomega.Expect(len(got.CheckedFiles) != 0).To(gomega.BeFalse(), fmt.Sprintf("expected no checked files without Obsidian vault, got %#v", got.CheckedFiles))
		gomega.Expect(len(got.Findings) != 0).To(gomega.BeFalse(), fmt.Sprintf("expected no findings without Obsidian vault, got %#v", got.Findings))

	})
	ginkgo.It("ObsidianParityDoesNotReadAncestorDocs", func() {
		parent := ginkgo.GinkgoT().TempDir()
		nested := filepath.Join(parent, "nested", "effort")
		mustWrite(filepath.Join(parent, ObsidianProductWorkflowPath), "> **Status: PARTIALLY IMPLEMENTED**")
		mustWrite(filepath.Join(parent, ObsidianRecoveryPath), "# Recovery and Decomposition")
		{
			err := os.MkdirAll(filepath.Join(nested, "docs", "obsidian"), 0o755)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir nested obsidian dir: %v", err))
		}

		ws := &WorkflowState{
			EffortID:     "prd-adr-brief-workflow",
			TaskID:       "impl-0032-phase-5-obsidian-parity-check",
			CurrentPhase: "phase-5-execution-feedback",
		}

		got := EvaluateObsidianParity(nested, ws, nil, nil)
		gomega.Expect(len(got.Findings) != 2).To(gomega.BeFalse(), fmt.Sprintf("expected missing-file findings scoped to nested root, got %#v", got.Findings))

		for _, finding := range got.Findings {
			gomega.Expect(finding.Code != "obsidian_read_error").To(gomega.BeFalse(), fmt.Sprintf("expected obsidian_read_error, got %#v", finding))

		}
	})
	ginkgo.It("ObsidianParityRejectsSymlinkedDocs", func() {
		root := ginkgo.GinkgoT().TempDir()
		outside := filepath.Join(ginkgo.GinkgoT().TempDir(), "product.md")
		mustWrite(outside, "> **Status: NOT STARTED**")
		mustWrite(filepath.Join(root, ObsidianRecoveryPath), "# Recovery and Decomposition")
		{

			err := os.MkdirAll(filepath.Dir(filepath.Join(root, ObsidianProductWorkflowPath)), 0o755)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprint(err))
		}
		{

			err := os.Symlink(outside, filepath.Join(root, ObsidianProductWorkflowPath))
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprint(err))
		}

		ws := &WorkflowState{
			EffortID:     "prd-adr-brief-workflow",
			TaskID:       "impl-0032-phase-5-obsidian-parity-check",
			CurrentPhase: "phase-5-execution-feedback",
		}

		got := EvaluateObsidianParity(root, ws, nil, nil)
		gomega.Expect(got.Findings).To(gomega.HaveLen(1), fmt.Sprintf("expected symlinked Obsidian doc read error, got %#v", got.Findings))
		gomega.Expect(got.Findings[0].Code).To(gomega.Equal("obsidian_read_error"), fmt.Sprintf("expected symlinked Obsidian doc read error, got %#v", got.Findings))
		gomega.Expect(got.Findings[0].Path).To(gomega.Equal(ObsidianProductWorkflowPath), fmt.Sprintf("expected symlinked Obsidian doc read error, got %#v", got.Findings))

	})
})

func mustWrite(path, content string) {
	ginkgo.GinkgoHelper()
	{

		err := os.MkdirAll(filepath.Dir(path), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir %s: %v", filepath.Dir(path), err))
	}
	{

		err := os.WriteFile(path, []byte(content), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write %s: %v", path, err))
	}

}
