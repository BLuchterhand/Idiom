package codingrules

import (
	"slices"
	"testing"
)

func TestEffectiveRulesDefaultPolicySurface(t *testing.T) {
	rules := EffectiveRules(PolicyFile{SchemaVersion: PolicySchemaVersion})

	if len(rules) != 1 {
		t.Fatalf("EffectiveRules returned %d rule(s), want one default rule: %#v", len(rules), rules)
	}

	rule := rules[0]
	if rule.ID != RuleNoPassThroughWrapper {
		t.Fatalf("default rule ID = %s, want %s", rule.ID, RuleNoPassThroughWrapper)
	}
	if !rule.Enabled || rule.Enforcement != EnforcementAdvisory || rule.Severity != SeverityWarning {
		t.Fatalf("unexpected default rule policy: %#v", rule)
	}
	if len(rule.AllowWhen) != len(DefaultPolicy().Rules[0].AllowWhen) {
		t.Fatalf("default allowances = %#v, want %#v", rule.AllowWhen, DefaultPolicy().Rules[0].AllowWhen)
	}
	if rule.Options.PassThroughWrapper == nil || *rule.Options.PassThroughWrapper != DefaultPassThroughWrapperOptions() {
		t.Fatalf("default pass-through options = %#v, want %#v", rule.Options.PassThroughWrapper, DefaultPassThroughWrapperOptions())
	}
}

func TestEffectiveRulesInheritPassThroughDefaultsForPartialOverride(t *testing.T) {
	enabled := true
	policy := PolicyFile{
		SchemaVersion: PolicySchemaVersion,
		Defaults: Defaults{
			Enabled:     &enabled,
			Enforcement: EnforcementAdvisory,
			Severity:    SeverityWarning,
			Include:     []string{"**/*.go"},
		},
		Rules: []RuleConfig{{
			ID:          RuleNoPassThroughWrapper,
			Enforcement: EnforcementBlocking,
			Severity:    SeverityError,
		}},
	}

	rules := EffectiveRules(policy)
	if len(rules) != 1 {
		t.Fatalf("EffectiveRules returned %d rule(s), want one: %#v", len(rules), rules)
	}

	rule := rules[0]
	if rule.Enforcement != EnforcementBlocking || rule.Severity != SeverityError {
		t.Fatalf("explicit enforcement/severity not preserved: %#v", rule)
	}
	if !containsAllowance(rule.AllowWhen, AllowCompatibilityAPI) {
		t.Fatalf("partial override did not inherit default allowances: %#v", rule.AllowWhen)
	}
	if rule.Options.PassThroughWrapper == nil || *rule.Options.PassThroughWrapper != DefaultPassThroughWrapperOptions() {
		t.Fatalf("partial override options = %#v, want %#v", rule.Options.PassThroughWrapper, DefaultPassThroughWrapperOptions())
	}
}

func TestEffectiveRulesPreserveExplicitEmptyAllowWhen(t *testing.T) {
	policy := PolicyFile{
		SchemaVersion: PolicySchemaVersion,
		Rules: []RuleConfig{{
			ID:        RuleNoPassThroughWrapper,
			AllowWhen: []AllowanceReason{},
		}},
	}

	rules := EffectiveRules(policy)
	if len(rules) != 1 {
		t.Fatalf("EffectiveRules returned %d rule(s), want one: %#v", len(rules), rules)
	}
	if rules[0].AllowWhen == nil || len(rules[0].AllowWhen) != 0 {
		t.Fatalf("explicit empty allow_when should not inherit defaults: %#v", rules[0].AllowWhen)
	}
}

func TestEffectiveRulesMergesDefaultAndRulePathFilters(t *testing.T) {
	enabled := true
	policy := PolicyFile{
		SchemaVersion: PolicySchemaVersion,
		Defaults: Defaults{
			Enabled:     &enabled,
			Enforcement: EnforcementAdvisory,
			Severity:    SeverityWarning,
			Include:     []string{"internal/**"},
			Exclude:     []string{"vendor/**"},
		},
		Rules: []RuleConfig{{
			ID:      RuleNoPassThroughWrapper,
			Include: []string{"cmd/**"},
			Exclude: []string{"cmd/testdata/**"},
		}},
	}

	rules := EffectiveRules(policy)
	if len(rules) != 1 {
		t.Fatalf("EffectiveRules returned %d rule(s), want one: %#v", len(rules), rules)
	}
	if !slices.Equal(rules[0].Include, []string{"cmd/**"}) {
		t.Fatalf("rule include not preserved: %#v", rules[0].Include)
	}
	if !slices.Equal(rules[0].Exclude, []string{"vendor/**", "cmd/testdata/**"}) {
		t.Fatalf("rule exclude not merged with defaults: %#v", rules[0].Exclude)
	}
}

func TestEffectiveRulesReturnsIndependentPolicyCopies(t *testing.T) {
	options := PassThroughWrapperOptions{MaxBodyStatements: 1, IgnoreTests: true, IgnoreGenerated: true}
	policy := PolicyFile{
		SchemaVersion: PolicySchemaVersion,
		Rules: []RuleConfig{{
			ID:        RuleNoPassThroughWrapper,
			Include:   []string{"internal/**"},
			Exclude:   []string{"internal/generated/**"},
			AllowWhen: []AllowanceReason{AllowCompatibilityAPI},
			Options: RuleOptions{
				PassThroughWrapper: &options,
			},
		}},
	}

	rules := EffectiveRules(policy)
	rules[0].Include[0] = "changed/**"
	rules[0].Exclude[0] = "changed/**"
	rules[0].AllowWhen[0] = AllowValidation
	rules[0].Options.PassThroughWrapper.IgnoreTests = false

	again := EffectiveRules(policy)
	if !slices.Equal(again[0].Include, []string{"internal/**"}) {
		t.Fatalf("returned include aliases input policy: %#v", again[0].Include)
	}
	if !slices.Equal(again[0].Exclude, []string{"vendor/**", ".git/**", ".idiom/**", "internal/generated/**"}) {
		t.Fatalf("returned exclude aliases input policy or defaults: %#v", again[0].Exclude)
	}
	if len(again[0].AllowWhen) != 1 || again[0].AllowWhen[0] != AllowCompatibilityAPI {
		t.Fatalf("returned allow_when aliases input policy: %#v", again[0].AllowWhen)
	}
	if !again[0].Options.PassThroughWrapper.IgnoreTests || !options.IgnoreTests {
		t.Fatalf("returned pass-through options alias input policy/defaults: got %#v input %#v", again[0].Options.PassThroughWrapper, options)
	}
}

func TestEffectiveRulesResolvesEnabledDefaultsAndOverrides(t *testing.T) {
	defaultDisabled := false
	ruleEnabled := true
	rules := EffectiveRules(PolicyFile{
		SchemaVersion: PolicySchemaVersion,
		Defaults: Defaults{
			Enabled: &defaultDisabled,
		},
		Rules: []RuleConfig{{ID: RuleNoPassThroughWrapper}},
	})

	if len(rules) != 1 {
		t.Fatalf("EffectiveRules returned %d rule(s), want one: %#v", len(rules), rules)
	}
	if rules[0].Enabled {
		t.Fatalf("rule without override should inherit disabled default: %#v", rules[0])
	}

	rules = EffectiveRules(PolicyFile{
		SchemaVersion: PolicySchemaVersion,
		Defaults: Defaults{
			Enabled: &defaultDisabled,
		},
		Rules: []RuleConfig{{ID: RuleNoPassThroughWrapper, Enabled: &ruleEnabled}},
	})
	if len(rules) != 1 || !rules[0].Enabled {
		t.Fatalf("rule override should enable rule despite disabled default: %#v", rules)
	}

	ruleDisabled := false
	rules = EffectiveRules(PolicyFile{
		SchemaVersion: PolicySchemaVersion,
		Rules: []RuleConfig{{
			ID:      RuleNoPassThroughWrapper,
			Enabled: &ruleDisabled,
		}},
	})
	if len(rules) != 1 || rules[0].Enabled {
		t.Fatalf("explicit disabled rule should remain disabled: %#v", rules)
	}
}

func TestEffectivePassThroughWrapperOptionsDefaulting(t *testing.T) {
	if got := EffectivePassThroughWrapperOptions(nil); got != DefaultPassThroughWrapperOptions() {
		t.Fatalf("nil options = %#v, want %#v", got, DefaultPassThroughWrapperOptions())
	}

	got := EffectivePassThroughWrapperOptions(&PassThroughWrapperOptions{})
	if got.MaxBodyStatements != 1 {
		t.Fatalf("zero max body statements should default to one: %#v", got)
	}
	if got.IgnoreTests || got.IgnoreGenerated {
		t.Fatalf("non-nil options should not default boolean fields: %#v", got)
	}

	rules := EffectiveRules(PolicyFile{
		SchemaVersion: PolicySchemaVersion,
		Rules: []RuleConfig{{
			ID: RuleNoPassThroughWrapper,
			Options: RuleOptions{
				PassThroughWrapper: &PassThroughWrapperOptions{},
			},
		}},
	})
	if len(rules) != 1 || rules[0].Options.PassThroughWrapper == nil {
		t.Fatalf("EffectiveRules pass-through options missing: %#v", rules)
	}
	if rules[0].Options.PassThroughWrapper.MaxBodyStatements != 1 {
		t.Fatalf("EffectiveRules should expose effective max body statements: %#v", rules[0].Options.PassThroughWrapper)
	}
}

func containsAllowance(reasons []AllowanceReason, want AllowanceReason) bool {
	for _, reason := range reasons {
		if reason == want {
			return true
		}
	}
	return false
}
