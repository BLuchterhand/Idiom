package intentloop

import (
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("Contract extraction", func() {
	ginkgo.It("preserves thresholds and freezes typed contract plus CLI scope", func() {
		prompt := `Implement coverage.

Typed plan execution contract:
{
  "atoms": [{
    "atom_id": "impl",
    "atom_title": "Implementation",
    "scope": {
      "required_coverage_paths": ["internal/foo/foo.go"],
      "allowed_candidate_paths": ["internal/foo/foo_test.go"]
    },
    "required_tests": [
      {"id": "unit", "command": "go test ./internal/foo/..."}
    ],
    "acceptance_obligations": [
      {"id": "coverage-threshold", "description": "coverage >= 80%", "required": true, "threshold": ">=80%"}
    ],
    "terminal_evidence_requirements": {
      "require_source_sanity": true,
      "require_verification": true,
      "require_required_tests": true
    }
  }]
}

History says internal/not-authority/not.go failed.`

		contract, err := ExtractContract(ContractExtractionInput{
			OriginalPrompt:        prompt,
			RequiredCoveragePaths: []string{"internal/foo/cli.go"},
			AllowedCandidatePaths: []string{"internal/foo/cli_test.go"},
			WriteAllowedPaths:     []string{"internal/foo"},
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(contract.AtomID).To(gomega.Equal("impl"))
		gomega.Expect(contract.RequireSourceSanity).To(gomega.BeTrue())
		gomega.Expect(contract.RequireVerification).To(gomega.BeTrue())
		gomega.Expect(contract.RequireRequiredTests).To(gomega.BeTrue())
		gomega.Expect(contract.RequiredTestCommandStrings()).To(gomega.Equal([]string{"go test ./internal/foo/..."}))
		gomega.Expect(contract.AcceptanceObligations).To(gomega.HaveExactElements(gomega.HaveField("Threshold", ">=80%")))
		gomega.Expect(contract.ScopeContract.RequiredCoveragePaths).To(gomega.ContainElements("internal/foo/foo.go", "internal/foo/cli.go"))
		gomega.Expect(contract.ScopeContract.AllowedCandidatePaths).NotTo(gomega.ContainElement("internal/not-authority/not.go"))
		gomega.Expect(contract.ScopeContract.RequiredCoveragePaths).NotTo(gomega.ContainElement("internal/not-authority/not.go"))
		gomega.Expect(contract.ContractHash).NotTo(gomega.BeEmpty())
	})
})
