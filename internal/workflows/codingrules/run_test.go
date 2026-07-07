package codingrules

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	rulescontract "idiom/internal/contracts/codingrules"
)

func TestCheckWorkspaceFlagsPassThroughWrapper(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "wrapper.go", `package sample

type repoType struct{}

func (repoType) Save(ctx string, user string) error { return nil }

var repo repoType

func Save(ctx string, user string) error {
	return repo.Save(ctx, user)
}
`)

	result, err := CheckWorkspace(context.Background(), CheckOptions{ProjectRoot: root})
	if err != nil {
		t.Fatalf("CheckWorkspace: %v", err)
	}

	if len(result.Report.Findings) != 1 {
		t.Fatalf("findings = %#v, want one", result.Report.Findings)
	}

	finding := result.Report.Findings[0]
	if finding.RuleID != rulescontract.RuleNoPassThroughWrapper || finding.Symbol != "Save" {
		t.Fatalf("unexpected finding: %#v", finding)
	}
}

func TestCheckWorkspaceFlagsDefaultArgumentWrapper(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "wrapper.go", `package sample

type manager struct{}

func (manager) runWithEnv(ctx string, dir string, env []string, name string, args ...string) (string, error) {
	return "", nil
}

func (m manager) run(ctx string, dir string, name string, args ...string) (string, error) {
	return m.runWithEnv(ctx, dir, nil, name, args...)
}
`)

	result, err := CheckWorkspace(context.Background(), CheckOptions{ProjectRoot: root})
	if err != nil {
		t.Fatalf("CheckWorkspace: %v", err)
	}

	if len(result.Report.Findings) != 1 {
		t.Fatalf("findings = %#v, want one default-argument wrapper", result.Report.Findings)
	}

	finding := result.Report.Findings[0]
	if finding.RuleID != rulescontract.RuleNoPassThroughWrapper || finding.Symbol != "manager.run" {
		t.Fatalf("unexpected finding: %#v", finding)
	}
	if !strings.Contains(finding.Evidence[2].Detail, "nil") {
		t.Fatalf("forwarding evidence = %#v, want default nil argument detail", finding.Evidence)
	}
}

func TestLoadPolicyRejectsExplicitMissingConfig(t *testing.T) {
	root := t.TempDir()

	_, _, _, err := LoadPolicy(root, filepath.Join(root, "missing.json"))
	if err == nil {
		t.Fatal("expected explicit missing config to fail")
	}
}

func TestCheckWorkspaceIgnoresValidationAndErrorWrapping(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "not_wrapper.go", `package sample

import "fmt"

type repoType struct{}

func (repoType) Save(ctx string, user string) error { return nil }

var repo repoType

func Save(ctx string, user string) error {
	if user == "" {
		return fmt.Errorf("user is required")
	}
	return repo.Save(ctx, user)
}

func Wrapped(ctx string, user string) error {
	return fmt.Errorf("save user: %w", repo.Save(ctx, user))
}
`)

	result, err := CheckWorkspace(context.Background(), CheckOptions{ProjectRoot: root})
	if err != nil {
		t.Fatalf("CheckWorkspace: %v", err)
	}

	if len(result.Report.Findings) != 0 {
		t.Fatalf("findings = %#v, want none", result.Report.Findings)
	}
}

func TestCheckWorkspaceDefaultDiscoverySkipsTestFiles(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "wrapper.go", `package sample

func target(v string) string { return v }

func Wrapper(v string) string {
	return target(v)
}
`)
	writeFile(t, root, "wrapper_test.go", `package sample

func testTarget(v string) string { return v }

func TestWrapper(v string) string {
	return testTarget(v)
}
`)

	result, err := CheckWorkspace(context.Background(), CheckOptions{ProjectRoot: root})
	if err != nil {
		t.Fatalf("CheckWorkspace: %v", err)
	}

	if result.Report.Summary.FilesScanned != 1 {
		t.Fatalf("FilesScanned = %d, want only the production Go file", result.Report.Summary.FilesScanned)
	}
	if len(result.Report.Findings) != 1 || result.Report.Findings[0].Path != "wrapper.go" {
		t.Fatalf("findings = %#v, want only production wrapper finding", result.Report.Findings)
	}
}

func TestCheckWorkspaceAllowsInlineRationale(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "allowed.go", `package sample

type repoType struct{}

func (repoType) Save(ctx string, user string) error { return nil }

var repo repoType

// idiom:allow go.no_pass_through_wrapper -- compatibility API for downstream callers
func Save(ctx string, user string) error {
	return repo.Save(ctx, user)
}
`)

	result, err := CheckWorkspace(context.Background(), CheckOptions{ProjectRoot: root})
	if err != nil {
		t.Fatalf("CheckWorkspace: %v", err)
	}

	if len(result.Report.Findings) != 0 || result.Report.Summary.Suppressed != 1 {
		t.Fatalf("report = %#v, want one suppression and no findings", result.Report)
	}
}

func TestCheckWorkspacePolicySuppressionRequiresRuleAllowance(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "wrapper.go", `package sample

func target(v string) string { return v }

func Wrapper(v string) string {
	return target(v)
}
`)

	enabled := true
	policy := rulescontract.PolicyFile{
		SchemaVersion: rulescontract.PolicySchemaVersion,
		Defaults: rulescontract.Defaults{
			Enabled:     &enabled,
			Enforcement: rulescontract.EnforcementAdvisory,
			Severity:    rulescontract.SeverityWarning,
			Include:     []string{"**/*.go"},
		},
		Rules: []rulescontract.RuleConfig{{
			ID:        rulescontract.RuleNoPassThroughWrapper,
			Enabled:   &enabled,
			Languages: []rulescontract.LanguageID{rulescontract.LanguageGo},
			Kind:      rulescontract.RuleKindStructure,
			AllowWhen: []rulescontract.AllowanceReason{rulescontract.AllowCompatibilityAPI},
		}},
		Suppressions: []rulescontract.Suppression{{
			RuleID:    rulescontract.RuleNoPassThroughWrapper,
			Path:      "wrapper.go",
			Symbol:    "Wrapper",
			Reason:    rulescontract.AllowValidation,
			Rationale: "wrong reason for this rule",
		}},
	}

	result, err := CheckWorkspace(context.Background(), CheckOptions{ProjectRoot: root, Policy: &policy})
	if err != nil {
		t.Fatalf("CheckWorkspace: %v", err)
	}

	if len(result.Report.Findings) != 1 || result.Report.Summary.Suppressed != 0 {
		t.Fatalf("report = %#v, want unsuppressed finding", result.Report)
	}
}

func TestCheckWorkspacePartialRuleOverrideInheritsDefaultSuppressionAllowances(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "wrapper.go", `package sample

func target(v string) string { return v }

func Save(v string) string {
	return target(v)
}
`)

	enabled := true
	policy := rulescontract.PolicyFile{
		SchemaVersion: rulescontract.PolicySchemaVersion,
		Defaults: rulescontract.Defaults{
			Enabled:     &enabled,
			Enforcement: rulescontract.EnforcementAdvisory,
			Severity:    rulescontract.SeverityWarning,
			Include:     []string{"**/*.go"},
		},
		Rules: []rulescontract.RuleConfig{{
			ID:          rulescontract.RuleNoPassThroughWrapper,
			Enabled:     &enabled,
			Languages:   []rulescontract.LanguageID{rulescontract.LanguageGo},
			Kind:        rulescontract.RuleKindStructure,
			Enforcement: rulescontract.EnforcementBlocking,
			Severity:    rulescontract.SeverityError,
		}},
		Suppressions: []rulescontract.Suppression{{
			RuleID:    rulescontract.RuleNoPassThroughWrapper,
			Path:      "wrapper.go",
			Symbol:    "Save",
			Reason:    rulescontract.AllowCompatibilityAPI,
			Rationale: "compatibility API while callers migrate",
		}},
	}

	result, err := CheckWorkspace(context.Background(), CheckOptions{ProjectRoot: root, Policy: &policy})
	if err != nil {
		t.Fatalf("CheckWorkspace: %v", err)
	}

	if len(result.Report.Findings) != 0 || result.Report.Summary.Suppressed != 1 {
		t.Fatalf("report = %#v, want inherited default suppression allowance", result.Report)
	}
}

func TestCheckWorkspaceUsesBlockingPolicy(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "wrapper.go", `package sample

func target(v string) string { return v }

func Wrapper(v string) string {
	return target(v)
}
`)

	enabled := true
	policy := rulescontract.DefaultPolicy()
	policy.Rules = []rulescontract.RuleConfig{{
		ID:          rulescontract.RuleNoPassThroughWrapper,
		Enabled:     &enabled,
		Enforcement: rulescontract.EnforcementBlocking,
		Severity:    rulescontract.SeverityError,
	}}

	result, err := CheckWorkspace(context.Background(), CheckOptions{ProjectRoot: root, Policy: &policy})
	if err != nil {
		t.Fatalf("CheckWorkspace: %v", err)
	}

	if result.Report.Status != rulescontract.StatusFail || result.Report.Summary.Blocking != 1 {
		t.Fatalf("report = %#v, want blocking failure", result.Report)
	}
}

func TestCheckWorkspaceRejectsExplicitPathOutsideProjectRoot(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.go")
	if err := os.WriteFile(outside, []byte(`package outside

func target(v string) string { return v }

func Wrapper(v string) string {
	return target(v)
}
`), 0o644); err != nil {
		t.Fatalf("write outside fixture: %v", err)
	}

	_, err := CheckWorkspace(context.Background(), CheckOptions{
		ProjectRoot: root,
		Paths:       []string{outside},
	})
	if err == nil {
		t.Fatal("expected outside path to be rejected")
	}
	if !strings.Contains(err.Error(), "escapes project root") {
		t.Fatalf("error = %v, want project-root escape rejection", err)
	}
}

func TestCheckWorkspaceRejectsSymlinkedSourceOutsideProjectRoot(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.go")
	if err := os.WriteFile(outside, []byte(`package outside

func target(v string) string { return v }

func Wrapper(v string) string {
	return target(v)
}
`), 0o644); err != nil {
		t.Fatalf("write outside fixture: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "linked.go")); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	_, err := CheckWorkspace(context.Background(), CheckOptions{ProjectRoot: root})
	if err == nil {
		t.Fatal("expected symlink escape to be rejected")
	}
	if !strings.Contains(err.Error(), "escapes project root") {
		t.Fatalf("error = %v, want project-root escape rejection", err)
	}
}

func writeFile(t *testing.T, root string, name string, body string) {
	t.Helper()

	path := filepath.Join(root, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}
