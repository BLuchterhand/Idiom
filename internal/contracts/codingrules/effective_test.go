package codingrules

import (
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("Effective coding rules", func() {
	ginkgo.It("exposes the complete default policy surface", func() {
		rules := EffectiveRules(PolicyFile{SchemaVersion: PolicySchemaVersion})

		gomega.Expect(rules).To(gomega.HaveLen(1))
		rule := rules[0]
		gomega.Expect(rule.ID).To(gomega.Equal(RuleNoPassThroughWrapper))
		gomega.Expect(rule.Enabled).To(gomega.BeTrue())
		gomega.Expect(rule.Enforcement).To(gomega.Equal(EnforcementAdvisory))
		gomega.Expect(rule.Severity).To(gomega.Equal(SeverityWarning))
		gomega.Expect(rule.AllowWhen).To(gomega.Equal(DefaultPolicy().Rules[0].AllowWhen))
		gomega.Expect(rule.Options.PassThroughWrapper).NotTo(gomega.BeNil())
		gomega.Expect(*rule.Options.PassThroughWrapper).To(gomega.Equal(DefaultPassThroughWrapperOptions()))
	})

	ginkgo.It("inherits pass-through defaults for a partial rule override", func() {
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

		gomega.Expect(rules).To(gomega.HaveLen(1))
		rule := rules[0]
		gomega.Expect(rule.Enforcement).To(gomega.Equal(EnforcementBlocking))
		gomega.Expect(rule.Severity).To(gomega.Equal(SeverityError))
		gomega.Expect(rule.AllowWhen).To(gomega.ContainElement(AllowBoundaryAdapter))
		gomega.Expect(rule.Options.PassThroughWrapper).NotTo(gomega.BeNil())
		gomega.Expect(*rule.Options.PassThroughWrapper).To(gomega.Equal(DefaultPassThroughWrapperOptions()))
	})

	ginkgo.It("preserves an explicitly empty allowance list", func() {
		policy := PolicyFile{
			SchemaVersion: PolicySchemaVersion,
			Rules: []RuleConfig{{
				ID:        RuleNoPassThroughWrapper,
				AllowWhen: []AllowanceReason{},
			}},
		}

		rules := EffectiveRules(policy)

		gomega.Expect(rules).To(gomega.HaveLen(1))
		gomega.Expect(rules[0].AllowWhen).NotTo(gomega.BeNil())
		gomega.Expect(rules[0].AllowWhen).To(gomega.BeEmpty())
	})

	ginkgo.It("merges default exclusions while preserving rule path filters", func() {
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

		gomega.Expect(rules).To(gomega.HaveLen(1))
		gomega.Expect(rules[0].Include).To(gomega.Equal([]string{"cmd/**"}))
		gomega.Expect(rules[0].Exclude).To(gomega.Equal([]string{"vendor/**", "cmd/testdata/**"}))
	})

	ginkgo.It("returns copies that cannot mutate the input policy or later results", func() {
		options := PassThroughWrapperOptions{MaxBodyStatements: 1, IgnoreTests: true, IgnoreGenerated: true}
		policy := PolicyFile{
			SchemaVersion: PolicySchemaVersion,
			Rules: []RuleConfig{{
				ID:        RuleNoPassThroughWrapper,
				Include:   []string{"internal/**"},
				Exclude:   []string{"internal/generated/**"},
				AllowWhen: []AllowanceReason{AllowBoundaryAdapter},
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
		gomega.Expect(again[0].Include).To(gomega.Equal([]string{"internal/**"}))
		gomega.Expect(again[0].Exclude).To(gomega.Equal([]string{"vendor/**", ".git/**", ".idiom/**", "internal/generated/**"}))
		gomega.Expect(again[0].AllowWhen).To(gomega.Equal([]AllowanceReason{AllowBoundaryAdapter}))
		gomega.Expect(again[0].Options.PassThroughWrapper.IgnoreTests).To(gomega.BeTrue())
		gomega.Expect(options.IgnoreTests).To(gomega.BeTrue())
	})

	ginkgo.Describe("enabled-state resolution", func() {
		ginkgo.It("inherits a disabled default without a rule override", func() {
			defaultDisabled := false
			rules := EffectiveRules(PolicyFile{
				SchemaVersion: PolicySchemaVersion,
				Defaults: Defaults{
					Enabled: &defaultDisabled,
				},
				Rules: []RuleConfig{{ID: RuleNoPassThroughWrapper}},
			})

			gomega.Expect(rules).To(gomega.HaveLen(1))
			gomega.Expect(rules[0].Enabled).To(gomega.BeFalse())
		})

		ginkgo.It("lets a rule override enable a disabled default", func() {
			defaultDisabled := false
			ruleEnabled := true
			rules := EffectiveRules(PolicyFile{
				SchemaVersion: PolicySchemaVersion,
				Defaults: Defaults{
					Enabled: &defaultDisabled,
				},
				Rules: []RuleConfig{{ID: RuleNoPassThroughWrapper, Enabled: &ruleEnabled}},
			})

			gomega.Expect(rules).To(gomega.HaveLen(1))
			gomega.Expect(rules[0].Enabled).To(gomega.BeTrue())
		})

		ginkgo.It("preserves an explicitly disabled rule", func() {
			ruleDisabled := false
			rules := EffectiveRules(PolicyFile{
				SchemaVersion: PolicySchemaVersion,
				Rules: []RuleConfig{{
					ID:      RuleNoPassThroughWrapper,
					Enabled: &ruleDisabled,
				}},
			})

			gomega.Expect(rules).To(gomega.HaveLen(1))
			gomega.Expect(rules[0].Enabled).To(gomega.BeFalse())
		})
	})

	ginkgo.Describe("pass-through-wrapper option resolution", func() {
		ginkgo.It("uses all defaults when options are absent", func() {
			gomega.Expect(EffectivePassThroughWrapperOptions(nil)).To(gomega.Equal(DefaultPassThroughWrapperOptions()))
		})

		ginkgo.It("defaults only the statement limit for explicit zero-value options", func() {
			options := EffectivePassThroughWrapperOptions(&PassThroughWrapperOptions{})

			gomega.Expect(options.MaxBodyStatements).To(gomega.Equal(1))
			gomega.Expect(options.IgnoreTests).To(gomega.BeFalse())
			gomega.Expect(options.IgnoreGenerated).To(gomega.BeFalse())
		})

		ginkgo.It("exposes the effective statement limit through resolved rules", func() {
			rules := EffectiveRules(PolicyFile{
				SchemaVersion: PolicySchemaVersion,
				Rules: []RuleConfig{{
					ID: RuleNoPassThroughWrapper,
					Options: RuleOptions{
						PassThroughWrapper: &PassThroughWrapperOptions{},
					},
				}},
			})

			gomega.Expect(rules).To(gomega.HaveLen(1))
			gomega.Expect(rules[0].Options.PassThroughWrapper).NotTo(gomega.BeNil())
			gomega.Expect(rules[0].Options.PassThroughWrapper.MaxBodyStatements).To(gomega.Equal(1))
		})
	})
})
