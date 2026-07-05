package productworkflow

import (
	"reflect"
	"testing"
)

func TestCoverageTierPolicySemantics(t *testing.T) {
	wantOrder := []string{"documentation", "unit", "local_low", "behavior_standard", "contract_high", "formal_optional"}
	if got := AllowedCoverageTiers(); !reflect.DeepEqual(got, wantOrder) {
		t.Fatalf("AllowedCoverageTiers() = %#v, want %#v", got, wantOrder)
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
		t.Run(tt.tier, func(t *testing.T) {
			if got := tierRequiresBehaviorCoverage(tt.tier); got != tt.requiresBehavior {
				t.Fatalf("tierRequiresBehaviorCoverage(%q) = %t, want %t", tt.tier, got, tt.requiresBehavior)
			}
			if got := tierRequiresContractCoverage(tt.tier); got != tt.requiresContract {
				t.Fatalf("tierRequiresContractCoverage(%q) = %t, want %t", tt.tier, got, tt.requiresContract)
			}
			gotType, gotCommand := suggestedTDDPlanForTier(tt.tier)
			if gotType != tt.suggestedType || gotCommand != tt.command {
				t.Fatalf("suggestedTDDPlanForTier(%q) = (%q, %q), want (%q, %q)", tt.tier, gotType, gotCommand, tt.suggestedType, tt.command)
			}
		})
	}

	for _, tier := range []string{" CONTRACT_HIGH ", "FORMAL_OPTIONAL\n"} {
		if !tierRequiresBehaviorCoverage(tier) || !tierRequiresContractCoverage(tier) {
			t.Fatalf("expected normalized contract-tier helpers to accept %q", tier)
		}
		gotType, gotCommand := suggestedTDDPlanForTier(tier)
		if gotType != "contract" || gotCommand != "go test ./..." {
			t.Fatalf("normalized tier suggestion for %q = (%q, %q), want contract go test ./...", tier, gotType, gotCommand)
		}
	}

	gotType, gotCommand := suggestedTDDPlanForTier("unknown")
	if gotType != "acceptance" || gotCommand != "go test ./..." {
		t.Fatalf("unknown tier suggestion = (%q, %q), want acceptance go test ./...", gotType, gotCommand)
	}
}

func TestCoverageTierValidationIsCanonicalAndExact(t *testing.T) {
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
		if err := validateTDDLedger(ledger); err == nil {
			t.Fatalf("expected non-canonical coverage_tier %q to fail validation", tier)
		}
	}

	doc := makeValidADRDocument()
	doc.FrontMatter.CoverageTier = " UNIT "
	result := ValidateADRIntake(doc, "ADR-001.md")
	if result.Passed || !hasCode(result, "invalid_adr_coverage_tier") {
		t.Fatalf("expected ADR intake to reject non-canonical coverage_tier, got passed=%t failures=%+v", result.Passed, result.Failures)
	}
}
