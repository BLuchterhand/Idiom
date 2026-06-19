package intentloop

import "testing"

func TestExtractContractPreservesThresholdAndFreezesCLIScope(t *testing.T) {
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
	if err != nil {
		t.Fatalf("ExtractContract() error = %v", err)
	}

	if contract.AtomID != "impl" {
		t.Fatalf("AtomID = %q, want impl", contract.AtomID)
	}
	if !contract.RequireSourceSanity || !contract.RequireVerification || !contract.RequireRequiredTests {
		t.Fatalf("terminal requirements not preserved: %+v", contract)
	}
	if got := contract.RequiredTestCommandStrings(); len(got) != 1 || got[0] != "go test ./internal/foo/..." {
		t.Fatalf("RequiredTestCommandStrings() = %#v", got)
	}
	if len(contract.AcceptanceObligations) != 1 || contract.AcceptanceObligations[0].Threshold != ">=80%" {
		t.Fatalf("AcceptanceObligations = %#v", contract.AcceptanceObligations)
	}
	if !contains(contract.ScopeContract.RequiredCoveragePaths, "internal/foo/foo.go") ||
		!contains(contract.ScopeContract.RequiredCoveragePaths, "internal/foo/cli.go") {
		t.Fatalf("required scope not frozen from contract+CLI: %#v", contract.ScopeContract.RequiredCoveragePaths)
	}
	if contains(contract.ScopeContract.AllowedCandidatePaths, "internal/not-authority/not.go") ||
		contains(contract.ScopeContract.RequiredCoveragePaths, "internal/not-authority/not.go") {
		t.Fatalf("history path leaked into scope: %+v", contract.ScopeContract)
	}
	if contract.ContractHash == "" {
		t.Fatal("ContractHash is empty")
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
