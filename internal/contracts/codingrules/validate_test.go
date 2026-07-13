package codingrules

import (
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("Policy validation and normalization", func() {
	ginkgo.Describe("decoding policy files", func() {
		ginkgo.It("fills in the default pass-through-wrapper rule", func() {
			policy, err := DecodePolicyFile([]byte(`{"schema_version":"idiom.coding_rules.v1"}`))

			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(policy.Rules).To(gomega.HaveLen(1))
			rule := policy.Rules[0]
			gomega.Expect(rule.ID).To(gomega.Equal(RuleNoPassThroughWrapper))
			gomega.Expect(rule.Enabled).NotTo(gomega.BeNil())
			gomega.Expect(*rule.Enabled).To(gomega.BeTrue())
			gomega.Expect(rule.Enforcement).To(gomega.Equal(EnforcementAdvisory))
			gomega.Expect(rule.Severity).To(gomega.Equal(SeverityWarning))
		})

		ginkgo.It("rejects unknown rule keys with their policy path", func() {
			_, err := DecodePolicyFile([]byte(`{"schema_version":"idiom.coding_rules.v1","rules":[{"id":"go.no_pass_through_wrapper","surprise":true}]}`))

			gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("rules[0].surprise")))
		})

		ginkgo.It("rejects unsupported pass-through statement limits", func() {
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

			gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("max_body_statements must be 0 or 1")))
		})
	})

	ginkgo.Describe("validating allowance reasons", func() {
		ginkgo.It("rejects an unknown rule allowance", func() {
			policy := DefaultPolicy()
			policy.Rules[0].AllowWhen = append(policy.Rules[0].AllowWhen, AllowanceReason("because_i_said_so"))

			gomega.Expect(ValidatePolicy(policy)).To(gomega.MatchError(gomega.ContainSubstring("rules[0].allow_when")))
		})

		ginkgo.It("rejects an unknown suppression reason", func() {
			policy := DefaultPolicy()
			policy.Suppressions = []Suppression{{
				RuleID:    RuleNoPassThroughWrapper,
				Path:      "internal/example.go",
				Reason:    AllowanceReason("because_i_said_so"),
				Rationale: "test fixture",
			}}

			gomega.Expect(ValidatePolicy(policy)).To(gomega.MatchError(gomega.ContainSubstring("suppressions[0].reason")))
		})
	})

	ginkgo.Describe("normalizing policies", func() {
		ginkgo.It("inherits policy defaults for unspecified rule fields", func() {
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

			gomega.Expect(policy.Rules).To(gomega.HaveLen(1))
			rule := policy.Rules[0]
			gomega.Expect(rule.Enabled).NotTo(gomega.BeNil())
			gomega.Expect(*rule.Enabled).To(gomega.BeFalse())
			gomega.Expect(rule.Enforcement).To(gomega.Equal(EnforcementBlocking))
			gomega.Expect(rule.Severity).To(gomega.Equal(SeverityError))
			gomega.Expect(rule.Include).To(gomega.Equal([]string{"internal/**"}))
			gomega.Expect(rule.Exclude).To(gomega.Equal([]string{"vendor/**"}))
			gomega.Expect(rule.Languages).To(gomega.Equal([]LanguageID{LanguageGo}))
			gomega.Expect(rule.Kind).To(gomega.Equal(RuleKindStructure))
			gomega.Expect(rule.Options.PassThroughWrapper).NotTo(gomega.BeNil())
			gomega.Expect(*rule.Options.PassThroughWrapper).To(gomega.Equal(DefaultPassThroughWrapperOptions()))
		})

		ginkgo.It("preserves explicitly disabled pass-through options", func() {
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

			gomega.Expect(policy.Rules).To(gomega.HaveLen(1))
			options := policy.Rules[0].Options.PassThroughWrapper
			gomega.Expect(options).NotTo(gomega.BeNil())
			gomega.Expect(options.MaxBodyStatements).To(gomega.BeZero())
			gomega.Expect(options.IgnoreTests).To(gomega.BeFalse())
			gomega.Expect(options.IgnoreGenerated).To(gomega.BeFalse())
		})
	})
})
