package codingrules

import (
	"strings"
	"testing"
)

func TestDefaultPolicyValidates(t *testing.T) {
	if err := ValidatePolicy(DefaultPolicy()); err != nil {
		t.Fatalf("DefaultPolicy should validate: %v", err)
	}
}

func TestDecodePolicyRejectsTopLevelUnknownKeys(t *testing.T) {
	_, err := DecodePolicy(strings.NewReader(`{
		"schema_version":"idiom.coding_rules.v1",
		"unexpected":true
	}`))
	if err == nil {
		t.Fatalf("DecodePolicy should reject unknown keys")
	}
}

func TestValidatePolicyRequiresSuppressionRationale(t *testing.T) {
	policy := DefaultPolicy()
	policy.Suppressions = []Suppression{{
		RuleID: RuleNoPassThroughWrapper,
		Path:   "internal/example.go",
		Reason: AllowCompatibilityAPI,
	}}

	if err := ValidatePolicy(policy); err == nil {
		t.Fatalf("ValidatePolicy should reject suppression without rationale")
	}
}

func TestFindingHashStable(t *testing.T) {
	finding := Finding{
		RuleID:      RuleNoPassThroughWrapper,
		Severity:    SeverityWarning,
		Enforcement: EnforcementAdvisory,
		Path:        "internal/example.go",
		Line:        12,
		Symbol:      "Save",
	}

	first := FindingHash(finding)
	second := FindingHash(finding)
	if first == "" || first != second {
		t.Fatalf("FindingHash should be stable and non-empty: %q %q", first, second)
	}
}
