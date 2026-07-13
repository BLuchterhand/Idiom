package codingrules

import (
	"context"
	"os"
	"path/filepath"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	rulescontract "idiom/internal/contracts/codingrules"
)

const passThroughWrapperFixture = `package sample

func target(v string) string { return v }

func Wrapper(v string) string {
	return target(v)
}
`

var _ = ginkgo.Describe("Coding-rule workflow", func() {
	ginkgo.It("flags a direct pass-through wrapper", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeCodingRuleFixture(root, "wrapper.go", `package sample

type repoType struct{}
func (repoType) Save(ctx string, user string) error { return nil }
var repo repoType
func Save(ctx string, user string) error { return repo.Save(ctx, user) }
`)
		result, err := CheckWorkspace(context.Background(), CheckOptions{ProjectRoot: root})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(result.Report.Findings).To(gomega.HaveLen(1))
		gomega.Expect(result.Report.Findings[0].RuleID).To(gomega.Equal(rulescontract.RuleNoPassThroughWrapper))
		gomega.Expect(result.Report.Findings[0].Symbol).To(gomega.Equal("Save"))
	})

	ginkgo.It("flags a wrapper that supplies a default argument", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeCodingRuleFixture(root, "wrapper.go", `package sample

type manager struct{}
func (manager) runWithEnv(ctx string, dir string, env []string, name string, args ...string) (string, error) { return "", nil }
func (m manager) run(ctx string, dir string, name string, args ...string) (string, error) {
	return m.runWithEnv(ctx, dir, nil, name, args...)
}
`)
		result, err := CheckWorkspace(context.Background(), CheckOptions{ProjectRoot: root})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(result.Report.Findings).To(gomega.HaveLen(1))
		finding := result.Report.Findings[0]
		gomega.Expect(finding.RuleID).To(gomega.Equal(rulescontract.RuleNoPassThroughWrapper))
		gomega.Expect(finding.Symbol).To(gomega.Equal("manager.run"))
		gomega.Expect(finding.Evidence[2].Detail).To(gomega.ContainSubstring("nil"))
	})

	ginkgo.It("rejects an explicitly configured missing policy", func() {
		root := ginkgo.GinkgoT().TempDir()
		_, _, _, err := LoadPolicy(root, filepath.Join(root, "missing.json"))
		gomega.Expect(err).To(gomega.HaveOccurred())
	})

	ginkgo.It("does not flag wrappers that validate or wrap errors", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeCodingRuleFixture(root, "not_wrapper.go", `package sample

import "fmt"
type repoType struct{}
func (repoType) Save(ctx string, user string) error { return nil }
var repo repoType
func Save(ctx string, user string) error {
	if user == "" { return fmt.Errorf("user is required") }
	return repo.Save(ctx, user)
}
func Wrapped(ctx string, user string) error { return fmt.Errorf("save user: %w", repo.Save(ctx, user)) }
`)
		result, err := CheckWorkspace(context.Background(), CheckOptions{ProjectRoot: root})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(result.Report.Findings).To(gomega.BeEmpty())
	})

	ginkgo.It("skips test files during default discovery", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeCodingRuleFixture(root, "wrapper.go", passThroughWrapperFixture)
		writeCodingRuleFixture(root, "wrapper_test.go", `package sample
func testTarget(v string) string { return v }
func TestWrapper(v string) string { return testTarget(v) }
`)
		result, err := CheckWorkspace(context.Background(), CheckOptions{ProjectRoot: root})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(result.Report.Summary.FilesScanned).To(gomega.Equal(1))
		gomega.Expect(result.Report.Findings).To(gomega.HaveLen(1))
		gomega.Expect(result.Report.Findings[0].Path).To(gomega.Equal("wrapper.go"))
	})

	ginkgo.It("accepts an inline boundary-adapter rationale", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeCodingRuleFixture(root, "allowed.go", `package sample
type repoType struct{}
func (repoType) Save(ctx string, user string) error { return nil }
var repo repoType
// idiom:allow go.no_pass_through_wrapper -- boundary adapter required by external Host interface
func Save(ctx string, user string) error { return repo.Save(ctx, user) }
`)
		result, err := CheckWorkspace(context.Background(), CheckOptions{ProjectRoot: root})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(result.Report.Findings).To(gomega.BeEmpty())
		gomega.Expect(result.Report.Summary.Suppressed).To(gomega.Equal(1))
	})

	ginkgo.It("rejects an inline compatibility rationale", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeCodingRuleFixture(root, "wrapper.go", `package sample
type repoType struct{}
func (repoType) Save(ctx string, user string) error { return nil }
var repo repoType
// idiom:allow go.no_pass_through_wrapper -- compatibility API for downstream callers
func Save(ctx string, user string) error { return repo.Save(ctx, user) }
`)
		result, err := CheckWorkspace(context.Background(), CheckOptions{ProjectRoot: root})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(result.Report.Findings).To(gomega.HaveLen(1))
		gomega.Expect(result.Report.Summary.Suppressed).To(gomega.Equal(0))
	})

	ginkgo.It("requires a policy suppression reason allowed by the rule", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeCodingRuleFixture(root, "wrapper.go", passThroughWrapperFixture)
		enabled := true
		policy := rulescontract.PolicyFile{
			SchemaVersion: rulescontract.PolicySchemaVersion,
			Defaults:      rulescontract.Defaults{Enabled: &enabled, Enforcement: rulescontract.EnforcementAdvisory, Severity: rulescontract.SeverityWarning, Include: []string{"**/*.go"}},
			Rules:         []rulescontract.RuleConfig{{ID: rulescontract.RuleNoPassThroughWrapper, Enabled: &enabled, Languages: []rulescontract.LanguageID{rulescontract.LanguageGo}, Kind: rulescontract.RuleKindStructure, AllowWhen: []rulescontract.AllowanceReason{rulescontract.AllowBoundaryAdapter}}},
			Suppressions:  []rulescontract.Suppression{{RuleID: rulescontract.RuleNoPassThroughWrapper, Path: "wrapper.go", Symbol: "Wrapper", Reason: rulescontract.AllowValidation, Rationale: "wrong reason for this rule"}},
		}
		result, err := CheckWorkspace(context.Background(), CheckOptions{ProjectRoot: root, Policy: &policy})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(result.Report.Findings).To(gomega.HaveLen(1))
		gomega.Expect(result.Report.Summary.Suppressed).To(gomega.Equal(0))
	})

	ginkgo.It("inherits default boundary suppression allowances through partial rule overrides", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeCodingRuleFixture(root, "wrapper.go", `package sample
func target(v string) string { return v }
func Save(v string) string { return target(v) }
`)
		enabled := true
		policy := rulescontract.PolicyFile{
			SchemaVersion: rulescontract.PolicySchemaVersion,
			Defaults:      rulescontract.Defaults{Enabled: &enabled, Enforcement: rulescontract.EnforcementAdvisory, Severity: rulescontract.SeverityWarning, Include: []string{"**/*.go"}},
			Rules:         []rulescontract.RuleConfig{{ID: rulescontract.RuleNoPassThroughWrapper, Enabled: &enabled, Languages: []rulescontract.LanguageID{rulescontract.LanguageGo}, Kind: rulescontract.RuleKindStructure, Enforcement: rulescontract.EnforcementBlocking, Severity: rulescontract.SeverityError}},
			Suppressions:  []rulescontract.Suppression{{RuleID: rulescontract.RuleNoPassThroughWrapper, Path: "wrapper.go", Symbol: "Save", Reason: rulescontract.AllowBoundaryAdapter, Rationale: "required boundary adapter for an external interface"}},
		}
		result, err := CheckWorkspace(context.Background(), CheckOptions{ProjectRoot: root, Policy: &policy})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(result.Report.Findings).To(gomega.BeEmpty())
		gomega.Expect(result.Report.Summary.Suppressed).To(gomega.Equal(1))
	})

	ginkgo.It("applies blocking policy", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeCodingRuleFixture(root, "wrapper.go", passThroughWrapperFixture)
		enabled := true
		policy := rulescontract.DefaultPolicy()
		policy.Rules = []rulescontract.RuleConfig{{ID: rulescontract.RuleNoPassThroughWrapper, Enabled: &enabled, Enforcement: rulescontract.EnforcementBlocking, Severity: rulescontract.SeverityError}}

		result, err := CheckWorkspace(context.Background(), CheckOptions{ProjectRoot: root, Policy: &policy})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(result.Report.Status).To(gomega.Equal(rulescontract.StatusFail))
		gomega.Expect(result.Report.Summary.Blocking).To(gomega.Equal(1))
	})

	ginkgo.It("rejects an explicit source path outside the project root", func() {
		root := ginkgo.GinkgoT().TempDir()
		outside := filepath.Join(ginkgo.GinkgoT().TempDir(), "outside.go")
		writeCodingRuleFixture(filepath.Dir(outside), filepath.Base(outside), passThroughWrapperFixture)
		_, err := CheckWorkspace(context.Background(), CheckOptions{ProjectRoot: root, Paths: []string{outside}})
		gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("escapes project root")))
	})

	ginkgo.It("rejects a symlinked source outside the project root", func() {
		root := ginkgo.GinkgoT().TempDir()
		outside := filepath.Join(ginkgo.GinkgoT().TempDir(), "outside.go")
		writeCodingRuleFixture(filepath.Dir(outside), filepath.Base(outside), passThroughWrapperFixture)
		gomega.Expect(os.Symlink(outside, filepath.Join(root, "linked.go"))).To(gomega.Succeed())
		_, err := CheckWorkspace(context.Background(), CheckOptions{ProjectRoot: root})
		gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("escapes project root")))
	})
})

func writeCodingRuleFixture(root, name, body string) {
	gomega.Expect(os.WriteFile(filepath.Join(root, name), []byte(body), 0o644)).To(gomega.Succeed())
}
