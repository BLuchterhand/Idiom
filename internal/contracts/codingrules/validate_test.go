package codingrules

import (
	"strings"
	"testing"
)

func TestDecodePolicyFileNormalizesDefaults(t *testing.T) {
	policy, err := DecodePolicyFile([]byte(`{"schema_version":"idiom.coding_rules.v1"}`))
	if err != nil {
		t.Fatalf("DecodePolicyFile returned error: %v", err)
	}

	if len(policy.Rules) != 1 || policy.Rules[0].ID != RuleNoPassThroughWrapper {
		t.Fatalf("default rule missing: %#v", policy.Rules)
	}
	if policy.Rules[0].Enabled == nil || !*policy.Rules[0].Enabled {
		t.Fatalf("default rule should be enabled: %#v", policy.Rules[0].Enabled)
	}
	if policy.Rules[0].Enforcement != EnforcementAdvisory || policy.Rules[0].Severity != SeverityWarning {
		t.Fatalf("unexpected default enforcement/severity: %#v", policy.Rules[0])
	}
}

func TestDecodePolicyFileRejectsUnknownKeys(t *testing.T) {
	_, err := DecodePolicyFile([]byte(`{"schema_version":"idiom.coding_rules.v1","rules":[{"id":"go.no_pass_through_wrapper","surprise":true}]}`))
	if err == nil || !strings.Contains(err.Error(), "rules[0].surprise") {
		t.Fatalf("expected unknown key error, got %v", err)
	}
}

func TestDecodePolicyFileRejectsUnsupportedPassThroughStatementLimit(t *testing.T) {
	_, err := DecodePolicyFile([]byte(`{
  "schema_version": "idiom.coding_rules.v1",
  "rules": [
    {
      "id": "go.no_pass_through_wrapper",
      "languages": ["go"],
      "kind": "structure",
      "options": {
        "pass_through_wrapper": {
          "max_body_statements": 2
        }
      }
    }
  ]
}`))
	if err == nil || !strings.Contains(err.Error(), "max_body_statements must be 0 or 1") {
		t.Fatalf("expected unsupported statement limit error, got %v", err)
	}
}
