package codingrules

import "slices"

type EffectiveRule struct {
	ID          RuleID
	Enabled     bool
	Enforcement EnforcementLevel
	Severity    FindingSeverity
	Include     []string
	Exclude     []string
	AllowWhen   []AllowanceReason
	Options     RuleOptions
}

func EffectiveRules(policy PolicyFile) []EffectiveRule {
	policy = NormalizePolicy(policy)
	out := make([]EffectiveRule, 0, len(policy.Rules))

	for _, spec := range policy.Rules {
		out = append(out, EffectiveRule{
			ID:          spec.ID,
			Enabled:     spec.Enabled != nil && *spec.Enabled,
			Enforcement: spec.Enforcement,
			Severity:    spec.Severity,
			Include:     slices.Clone(spec.Include),
			Exclude:     effectiveRuleExclude(policy.Defaults.Exclude, spec.Exclude),
			AllowWhen:   effectiveRuleAllowances(spec),
			Options:     effectiveRuleOptions(spec),
		})
	}

	return out
}

func DefaultPassThroughWrapperOptions() PassThroughWrapperOptions {
	return PassThroughWrapperOptions{
		MaxBodyStatements: 1,
		IgnoreTests:       true,
		IgnoreGenerated:   true,
	}
}

func EffectivePassThroughWrapperOptions(options *PassThroughWrapperOptions) PassThroughWrapperOptions {
	if options == nil {
		return DefaultPassThroughWrapperOptions()
	}

	out := *options
	defaults := DefaultPassThroughWrapperOptions()
	if out.MaxBodyStatements == 0 {
		out.MaxBodyStatements = defaults.MaxBodyStatements
	}

	return out
}

func defaultPassThroughWrapperOptionsPtr() *PassThroughWrapperOptions {
	options := DefaultPassThroughWrapperOptions()
	return &options
}

func defaultPassThroughRuleConfig() RuleConfig {
	enabled := true
	return RuleConfig{
		ID:          RuleNoPassThroughWrapper,
		Enabled:     &enabled,
		Languages:   []LanguageID{LanguageGo},
		Kind:        RuleKindStructure,
		Enforcement: EnforcementAdvisory,
		Severity:    SeverityWarning,
		AllowWhen:   defaultRuleAllowances(RuleNoPassThroughWrapper),
		Options: RuleOptions{
			PassThroughWrapper: defaultPassThroughWrapperOptionsPtr(),
		},
	}
}

func effectiveRuleOptions(spec RuleConfig) RuleOptions {
	options := spec.Options
	if spec.ID != RuleNoPassThroughWrapper {
		return options
	}

	passThrough := EffectivePassThroughWrapperOptions(options.PassThroughWrapper)
	options.PassThroughWrapper = &passThrough
	return options
}

func effectiveRuleAllowances(spec RuleConfig) []AllowanceReason {
	if spec.AllowWhen != nil {
		return slices.Clone(spec.AllowWhen)
	}
	return defaultRuleAllowances(spec.ID)
}

func defaultRuleAllowances(id RuleID) []AllowanceReason {
	if id != RuleNoPassThroughWrapper {
		return nil
	}
	return []AllowanceReason{
		AllowImplementsInterface,
		AllowCompatibilityAPI,
		AllowBoundaryAdapter,
		AllowValidation,
		AllowErrorContext,
		AllowObservability,
		AllowPermissionCheck,
		AllowDomainUseCaseName,
	}
}

func effectiveRuleExclude(defaultExclude []string, ruleExclude []string) []string {
	if len(ruleExclude) == 0 || slices.Equal(defaultExclude, ruleExclude) {
		return slices.Clone(defaultExclude)
	}

	out := slices.Clone(defaultExclude)
	return append(out, ruleExclude...)
}
