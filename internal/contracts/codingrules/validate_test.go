package codingrules

import (
	"slices"
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

func TestValidatePolicyRejectsUnknownAllowanceReasons(t *testing.T) {
	policy := DefaultPolicy()
	policy.Rules[0].AllowWhen = append(policy.Rules[0].AllowWhen, AllowanceReason("because_i_said_so"))
	if err := ValidatePolicy(policy); err == nil || !strings.Contains(err.Error(), "rules[0].allow_when") {
		t.Fatalf("expected unknown allow_when reason error, got %v", err)
	}

	policy = DefaultPolicy()
	policy.Suppressions = []Suppression{{
		RuleID:    RuleNoPassThroughWrapper,
		Path:      "internal/example.go",
		Reason:    AllowanceReason("because_i_said_so"),
		Rationale: "test fixture",
	}}
	if err := ValidatePolicy(policy); err == nil || !strings.Contains(err.Error(), "suppressions[0].reason") {
		t.Fatalf("expected unknown suppression reason error, got %v", err)
	}
}

func TestNormalizePolicyInheritsDefaults(t *testing.T) {
	enabled := false
	policy := NormalizePolicy(PolicyFile{
		SchemaVersion: PolicySchemaVersion,
		Defaults: Defaults{
			Enabled:     &enabled,
			Enforcement: EnforcementBlocking,
			Severity:    SeverityError,
			Include:     []string{"internal/**"},
			Exclude:     []string{"vendor/**"},
		},
		Rules: []RuleConfig{{
			ID: RuleNoPassThroughWrapper,
		}},
	})

	if len(policy.Rules) != 1 {
		t.Fatalf("normalized rules = %#v, want one rule", policy.Rules)
	}
	rule := policy.Rules[0]
	if rule.Enabled == nil || *rule.Enabled {
		t.Fatalf("rule enabled = %#v, want inherited false", rule.Enabled)
	}
	if rule.Enforcement != EnforcementBlocking || rule.Severity != SeverityError {
		t.Fatalf("rule enforcement/severity = %#v, want inherited blocking/error", rule)
	}
	if !slices.Equal(rule.Include, []string{"internal/**"}) || !slices.Equal(rule.Exclude, []string{"vendor/**"}) {
		t.Fatalf("rule include/exclude = %#v/%#v, want inherited defaults", rule.Include, rule.Exclude)
	}
	if len(rule.Languages) != 1 || rule.Languages[0] != LanguageGo || rule.Kind != RuleKindStructure {
		t.Fatalf("rule language/kind = %#v/%#v, want Go/structure", rule.Languages, rule.Kind)
	}
	if rule.Options.PassThroughWrapper == nil || *rule.Options.PassThroughWrapper != DefaultPassThroughWrapperOptions() {
		t.Fatalf("rule pass-through options = %#v, want defaults", rule.Options.PassThroughWrapper)
	}
}

func TestNormalizePolicyPreservesExplicitPassThroughOptions(t *testing.T) {
	policy := NormalizePolicy(PolicyFile{
		SchemaVersion: PolicySchemaVersion,
		Rules: []RuleConfig{{
			ID: RuleNoPassThroughWrapper,
			Options: RuleOptions{
				PassThroughWrapper: &PassThroughWrapperOptions{
					MaxBodyStatements: 0,
					IgnoreTests:       false,
					IgnoreGenerated:   false,
				},
			},
		}},
	})

	if len(policy.Rules) != 1 {
		t.Fatalf("normalized rules = %#v, want one rule", policy.Rules)
	}
	got := policy.Rules[0].Options.PassThroughWrapper
	if got == nil {
		t.Fatal("explicit pass-through options were removed")
	}
	if got.MaxBodyStatements != 0 || got.IgnoreTests || got.IgnoreGenerated {
		t.Fatalf("explicit pass-through options = %#v, want preserved zero/false values", got)
	}
}
