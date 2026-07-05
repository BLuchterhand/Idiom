package codingrules

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestDefaultPolicyValidates(t *testing.T) {
	if err := ValidatePolicy(DefaultPolicy()); err != nil {
		t.Fatalf("DefaultPolicy should validate: %v", err)
	}
}

func TestDefaultPolicyPassThroughDefaultsAreStable(t *testing.T) {
	policy := DefaultPolicy()
	if policy.SchemaVersion != PolicySchemaVersion {
		t.Fatalf("schema version = %q, want %q", policy.SchemaVersion, PolicySchemaVersion)
	}
	if policy.Defaults.Enabled == nil || !*policy.Defaults.Enabled {
		t.Fatalf("default enabled = %#v, want true", policy.Defaults.Enabled)
	}
	if policy.Defaults.Enforcement != EnforcementAdvisory || policy.Defaults.Severity != SeverityWarning {
		t.Fatalf("defaults = %#v, want advisory/warning", policy.Defaults)
	}
	if !slices.Equal(policy.Defaults.Include, []string{"**/*.go"}) {
		t.Fatalf("default include = %#v", policy.Defaults.Include)
	}
	if !slices.Equal(policy.Defaults.Exclude, []string{"vendor/**", ".git/**", ".idiom/**"}) {
		t.Fatalf("default exclude = %#v", policy.Defaults.Exclude)
	}
	if len(policy.Rules) != 1 {
		t.Fatalf("default policy has %d rules, want only pass-through", len(policy.Rules))
	}

	rule := policy.Rules[0]
	if rule.ID != RuleNoPassThroughWrapper || len(rule.Languages) != 1 || rule.Languages[0] != LanguageGo || rule.Kind != RuleKindStructure {
		t.Fatalf("default rule identity = %#v", rule)
	}
	if rule.Enforcement != EnforcementAdvisory || rule.Severity != SeverityWarning {
		t.Fatalf("default rule policy = %#v, want advisory/warning", rule)
	}
	wantAllowances := []AllowanceReason{
		AllowImplementsInterface,
		AllowCompatibilityAPI,
		AllowBoundaryAdapter,
		AllowValidation,
		AllowErrorContext,
		AllowObservability,
		AllowPermissionCheck,
		AllowDomainUseCaseName,
	}
	if !slices.Equal(rule.AllowWhen, wantAllowances) {
		t.Fatalf("default rule allowances = %#v, want %#v", rule.AllowWhen, wantAllowances)
	}
	if rule.Options.PassThroughWrapper == nil {
		t.Fatal("default pass-through rule missing options")
	}
	if got := *rule.Options.PassThroughWrapper; got != DefaultPassThroughWrapperOptions() {
		t.Fatalf("default pass-through options = %#v, want %#v", got, DefaultPassThroughWrapperOptions())
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

func TestCheckedInCodingRuleExampleValidatesAndMatchesDefaultAllowances(t *testing.T) {
	for _, rel := range []string{
		"coding_rules.example.json",
	} {
		t.Run(rel, func(t *testing.T) {
			body, err := os.ReadFile(filepath.Join("..", "..", "..", rel))
			if err != nil {
				t.Fatalf("read template: %v", err)
			}

			policy, err := DecodePolicyFile(body)
			if err != nil {
				t.Fatalf("DecodePolicyFile: %v", err)
			}

			defaultAllowances := map[AllowanceReason]struct{}{}
			for _, reason := range DefaultPolicy().Rules[0].AllowWhen {
				defaultAllowances[reason] = struct{}{}
			}

			var passThrough RuleConfig
			for _, rule := range policy.Rules {
				if rule.ID == RuleNoPassThroughWrapper {
					passThrough = rule
					break
				}
			}
			if passThrough.ID == "" {
				t.Fatal("template missing go.no_pass_through_wrapper")
			}
			for _, reason := range passThrough.AllowWhen {
				delete(defaultAllowances, reason)
			}
			if len(defaultAllowances) != 0 {
				t.Fatalf("template pass-through rule missing default allowance(s): %#v", defaultAllowances)
			}
		})
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
