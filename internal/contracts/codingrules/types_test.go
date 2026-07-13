package codingrules

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("Coding-rule policy types", func() {
	ginkgo.Describe("the default policy", func() {
		ginkgo.It("is valid", func() {
			gomega.Expect(ValidatePolicy(DefaultPolicy())).To(gomega.Succeed())
		})

		ginkgo.It("keeps the pass-through-wrapper contract stable", func() {
			policy := DefaultPolicy()

			gomega.Expect(policy.SchemaVersion).To(gomega.Equal(PolicySchemaVersion))
			gomega.Expect(policy.Defaults.Enabled).NotTo(gomega.BeNil())
			gomega.Expect(*policy.Defaults.Enabled).To(gomega.BeTrue())
			gomega.Expect(policy.Defaults.Enforcement).To(gomega.Equal(EnforcementAdvisory))
			gomega.Expect(policy.Defaults.Severity).To(gomega.Equal(SeverityWarning))
			gomega.Expect(policy.Defaults.Include).To(gomega.Equal([]string{"**/*.go"}))
			gomega.Expect(policy.Defaults.Exclude).To(gomega.Equal([]string{"vendor/**", ".git/**", ".idiom/**"}))
			gomega.Expect(policy.Rules).To(gomega.HaveLen(1))

			rule := policy.Rules[0]
			gomega.Expect(rule.ID).To(gomega.Equal(RuleNoPassThroughWrapper))
			gomega.Expect(rule.Languages).To(gomega.Equal([]LanguageID{LanguageGo}))
			gomega.Expect(rule.Kind).To(gomega.Equal(RuleKindStructure))
			gomega.Expect(rule.Enforcement).To(gomega.Equal(EnforcementAdvisory))
			gomega.Expect(rule.Severity).To(gomega.Equal(SeverityWarning))
			gomega.Expect(rule.AllowWhen).To(gomega.Equal([]AllowanceReason{
				AllowImplementsInterface,
				AllowBoundaryAdapter,
				AllowValidation,
				AllowErrorContext,
				AllowObservability,
				AllowPermissionCheck,
			}))
			gomega.Expect(rule.Options.PassThroughWrapper).NotTo(gomega.BeNil())
			gomega.Expect(*rule.Options.PassThroughWrapper).To(gomega.Equal(DefaultPassThroughWrapperOptions()))
		})
	})

	ginkgo.Describe("decoding and validation", func() {
		ginkgo.It("rejects unknown top-level keys", func() {
			_, err := DecodePolicy(strings.NewReader(`{
		"schema_version":"idiom.coding_rules.v1",
		"unexpected":true
	}`))

			gomega.Expect(err).To(gomega.HaveOccurred())
		})

		ginkgo.It("requires a rationale for every suppression", func() {
			policy := DefaultPolicy()
			policy.Suppressions = []Suppression{{
				RuleID: RuleNoPassThroughWrapper,
				Path:   "internal/example.go",
				Reason: AllowBoundaryAdapter,
			}}

			gomega.Expect(ValidatePolicy(policy)).To(gomega.HaveOccurred())
		})
	})

	ginkgo.DescribeTable("checked-in policy examples",
		func(rel string) {
			body, err := os.ReadFile(filepath.Join("..", "..", "..", rel))
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			policy, err := DecodePolicyFile(body)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			var passThrough RuleConfig
			for _, rule := range policy.Rules {
				if rule.ID == RuleNoPassThroughWrapper {
					passThrough = rule
					break
				}
			}
			gomega.Expect(passThrough.ID).To(gomega.Equal(RuleNoPassThroughWrapper))
			gomega.Expect(passThrough.AllowWhen).To(gomega.ContainElements(DefaultPolicy().Rules[0].AllowWhen))
		},
		ginkgo.Entry("validates coding_rules.example.json with every default allowance", "coding_rules.example.json"),
	)

	ginkgo.It("produces a stable, non-empty finding hash", func() {
		finding := Finding{
			RuleID:      RuleNoPassThroughWrapper,
			Severity:    SeverityWarning,
			Enforcement: EnforcementAdvisory,
			Path:        "internal/example.go",
			Line:        12,
			Symbol:      "Save",
		}

		first := FindingHash(finding)
		gomega.Expect(first).NotTo(gomega.BeEmpty())
		gomega.Expect(FindingHash(finding)).To(gomega.Equal(first))
	})
})
