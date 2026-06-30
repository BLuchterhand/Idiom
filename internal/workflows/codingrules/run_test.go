package codingrules

import (
	"context"
	"os"
	"path/filepath"
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

func writeFile(t *testing.T, root string, name string, body string) {
	t.Helper()

	path := filepath.Join(root, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}
