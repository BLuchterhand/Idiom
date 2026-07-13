package productworkflow

import (
	"fmt"
	"reflect"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("coverage tiers", func() {
	ginkgo.It("CoverageTierPolicySemantics", func() {
		wantOrder := []string{"documentation", "unit", "local_low", "behavior_standard", "contract_high", "formal_optional"}
		{
			got := AllowedCoverageTiers()
			gomega.Expect(!reflect.DeepEqual(got, wantOrder)).To(gomega.BeFalse(), fmt.Sprintf("AllowedCoverageTiers() = %#v, want %#v", got, wantOrder))
		}

		tests := []struct {
			tier             string
			requiresBehavior bool
			requiresContract bool
			suggestedType    string
			command          string
		}{
			{tier: "documentation", suggestedType: "audit", command: "git diff --check"},
			{tier: "unit", suggestedType: "acceptance", command: "go test ./..."},
			{tier: "local_low", suggestedType: "source_sanity", command: "go test ./..."},
			{tier: "behavior_standard", requiresBehavior: true, suggestedType: "acceptance", command: "go test ./..."},
			{tier: "contract_high", requiresBehavior: true, requiresContract: true, suggestedType: "contract", command: "go test ./..."},
			{tier: "formal_optional", requiresBehavior: true, requiresContract: true, suggestedType: "contract", command: "go test ./..."},
		}

		for _, tt := range tests {
			ginkgo.By(tt.tier, func() {
				{
					got := tierRequiresBehaviorCoverage(tt.tier)
					gomega.Expect(got != tt.requiresBehavior).To(gomega.BeFalse(), fmt.Sprintf("tierRequiresBehaviorCoverage(%q) = %t, want %t", tt.tier, got, tt.requiresBehavior))
				}
				{

					got := tierRequiresContractCoverage(tt.tier)
					gomega.Expect(got != tt.requiresContract).To(gomega.BeFalse(), fmt.Sprintf("tierRequiresContractCoverage(%q) = %t, want %t", tt.tier, got, tt.requiresContract))
				}

				gotType, gotCommand := suggestedTDDPlanForTier(tt.tier)
				gomega.Expect(gotType).To(gomega.Equal(tt.suggestedType), fmt.Sprintf("suggestedTDDPlanForTier(%q) = (%q, %q), want (%q, %q)", tt.tier, gotType, gotCommand, tt.suggestedType, tt.command))
				gomega.Expect(gotCommand).To(gomega.Equal(tt.command), fmt.Sprintf("suggestedTDDPlanForTier(%q) = (%q, %q), want (%q, %q)", tt.tier, gotType, gotCommand, tt.suggestedType, tt.command))

			})
		}

		for _, tier := range []string{" CONTRACT_HIGH ", "FORMAL_OPTIONAL\n"} {
			gomega.Expect(tierRequiresBehaviorCoverage(tier)).To(gomega.BeTrue(), fmt.Sprintf("expected normalized contract-tier helpers to accept %q", tier))
			gomega.Expect(tierRequiresContractCoverage(tier)).To(gomega.BeTrue(), fmt.Sprintf("expected normalized contract-tier helpers to accept %q", tier))

			gotType, gotCommand := suggestedTDDPlanForTier(tier)
			gomega.Expect(gotType).To(gomega.Equal("contract"), fmt.Sprintf("normalized tier suggestion for %q = (%q, %q), want contract go test ./...", tier, gotType, gotCommand))
			gomega.Expect(gotCommand).To(gomega.Equal("go test ./..."), fmt.Sprintf("normalized tier suggestion for %q = (%q, %q), want contract go test ./...", tier, gotType, gotCommand))

		}

		gotType, gotCommand := suggestedTDDPlanForTier("unknown")
		gomega.Expect(gotType).To(gomega.Equal("acceptance"), fmt.Sprintf("unknown tier suggestion = (%q, %q), want acceptance go test ./...", gotType, gotCommand))
		gomega.Expect(gotCommand).To(gomega.Equal("go test ./..."), fmt.Sprintf("unknown tier suggestion = (%q, %q), want acceptance go test ./...", gotType, gotCommand))

	})
	ginkgo.It("CoverageTierValidationIsCanonicalAndExact", func() {
		for _, tier := range []string{" unit ", "UNIT", "behavior_standard\n"} {
			ledger := TDDLedger{
				SchemaVersion: "idiom.tdd_obligations.v2",
				EffortID:      "test",
				TaskID:        "test",
				CoverageTier:  tier,
				Coverage:      TDDCoverage{MutationMinScore: 80},
				Obligations: []TDDObligation{
					{ID: "TDD-1", Type: "acceptance", Behavior: "b", Owner: "o", Command: "c", RequiredBefore: "close", Status: "pending"},
				},
			}
			{
				err := validateTDDLedger(ledger)
				gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprintf("expected non-canonical coverage_tier %q to fail validation", tier))
			}

		}

		doc := makeValidADRDocument()
		doc.FrontMatter.CoverageTier = " UNIT "
		result := ValidateADRIntake(doc, "ADR-001.md")
		gomega.Expect(result.Passed).To(gomega.BeFalse(), fmt.Sprintf("expected ADR intake to reject non-canonical coverage_tier, got passed=%t failures=%+v", result.Passed, result.Failures))
		gomega.Expect(hasCode(result, "invalid_adr_coverage_tier")).To(gomega.BeTrue(), fmt.Sprintf("expected ADR intake to reject non-canonical coverage_tier, got passed=%t failures=%+v", result.Passed, result.Failures))

	})
})
