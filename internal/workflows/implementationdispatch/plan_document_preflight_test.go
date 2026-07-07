package implementationdispatch

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	atomintakecontract "idiom/internal/contracts/atomintake"
	"idiom/internal/workflows/brieflint"
)

func TestPlanDocumentPreflightRejectsMissingWorkspaceRoot(t *testing.T) {
	result := PreflightPlanDocument(context.Background(), PlanDocumentPreflightInput{
		Document: PlanDocument{Path: "docs/atom.md"},
	})

	if result.OK || !containsString(result.Blockers, "workspace root is unavailable; cannot run dispatch preflight") {
		t.Fatalf("result = %#v, want missing workspace blocker", result)
	}
}

func TestPlanDocumentPreflightRejectsPlanDocumentOutsideWorkspace(t *testing.T) {
	root := t.TempDir()
	result := PreflightPlanDocument(context.Background(), PlanDocumentPreflightInput{
		WorkspaceRoot: root,
		Document:      PlanDocument{Path: "../outside.md"},
	})

	if result.OK || !containsString(result.Blockers, "referenced plan document path is outside the workspace") {
		t.Fatalf("result = %#v, want outside-workspace blocker", result)
	}
}

func TestPlanDocumentPreflightStopsBeforeBriefLintWhenAtomIsSourceSatisfied(t *testing.T) {
	root := t.TempDir()
	writeImplementationDispatchPreflightFile(t, root, "atom.md", "# Atom\n\nStatus: source-satisfied - do not dispatch\n")

	result := PreflightPlanDocument(context.Background(), PlanDocumentPreflightInput{
		WorkspaceRoot: root,
		Document:      PlanDocument{Path: "atom.md"},
	})

	if result.OK {
		t.Fatalf("source-satisfied atom should block dispatch: %#v", result)
	}
	if result.AtomState != atomintakecontract.StateSourceSatisfied {
		t.Fatalf("atom state = %q, want source-satisfied", result.AtomState)
	}
	if len(result.LintBlockers) != 0 {
		t.Fatalf("source-satisfied atom should skip brief lint, got %#v", result.LintBlockers)
	}
}

func TestPlanDocumentPreflightContinuesToBriefLintAfterMissingScope(t *testing.T) {
	root := t.TempDir()
	writeImplementationDispatchPreflightFile(t, root, "atom.md", "# Atom\n\nStatus: planned\n")

	result := PreflightPlanDocument(context.Background(), PlanDocumentPreflightInput{
		WorkspaceRoot: root,
		Document:      PlanDocument{Path: "atom.md"},
	})

	if result.AtomState != atomintakecontract.StateMissingScope {
		t.Fatalf("atom state = %q, want missing-scope; result=%#v", result.AtomState, result)
	}
	if len(result.LintBlockers) == 0 {
		t.Fatalf("missing-scope should still run brief lint and keep lint blockers: %#v", result)
	}
}

func TestPlanDocumentPreflightPassesAllowDirtyToAtomIntake(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available on PATH")
	}

	root := initImplementationDispatchPreflightGitRepo(t)
	writeImplementationDispatchPreflightFile(t, root, "foo.go", "package main\n")
	runImplementationDispatchPreflightGit(t, root, "add", "foo.go")
	runImplementationDispatchPreflightGit(t, root, "commit", "-m", "initial")
	writeImplementationDispatchPreflightFile(t, root, "foo.go", "package main\n\nfunc dirty() {}\n")
	writeImplementationDispatchPreflightFile(t, root, "atom.md", dispatchableBriefBody("foo.go"))

	blocked := PreflightPlanDocument(context.Background(), PlanDocumentPreflightInput{
		WorkspaceRoot: root,
		Document:      PlanDocument{Path: "atom.md"},
	})
	if blocked.AtomState != atomintakecontract.StateBlockedDirty {
		t.Fatalf("atom state without allow-dirty = %q, want blocked dirty; result=%#v", blocked.AtomState, blocked)
	}

	allowed := PreflightPlanDocument(context.Background(), PlanDocumentPreflightInput{
		WorkspaceRoot: root,
		Document:      PlanDocument{Path: "atom.md", AllowDirtyAtomScope: true},
	})
	if allowed.AtomState != "" || len(allowed.Blockers) != 0 {
		t.Fatalf("allow-dirty should make atom intake dispatchable and lint-clean, got %#v", allowed)
	}
}

func TestPlanDocumentPreflightKeepsBriefLintWarningsNonBlocking(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available on PATH")
	}

	root := t.TempDir()
	writeImplementationDispatchPreflightFile(t, root, ".git/HEAD", "ref: refs/heads/main\n")
	writeImplementationDispatchPreflightFile(t, root, "internal/foo/bar.go", "package foo\nfunc Existing() {}\n")
	writeImplementationDispatchPreflightFile(t, root, "atom.md", warningBriefBody("internal/foo/bar.go"))

	result := PreflightPlanDocument(context.Background(), PlanDocumentPreflightInput{
		WorkspaceRoot: root,
		Document:      PlanDocument{Path: "atom.md"},
	})

	if !result.OK || len(result.Blockers) != 0 || len(result.LintBlockers) != 0 {
		t.Fatalf("warning-only lint should not block: %#v", result)
	}
	if len(result.Warnings) == 0 || !strings.Contains(strings.Join(result.Warnings, "\n"), "branching language in Required Edit") {
		t.Fatalf("expected branching lint warning, got %#v", result.Warnings)
	}
}

func TestPlanDocumentPreflightPreservesLongBriefLintBlocker(t *testing.T) {
	longBlocker := "exact-anchor specificity failed for required edit path(s): " + strings.Repeat("internal/pkg/path.go ", 40) + "Each required edit path must carry at least one `<path>:<line>` reference."
	report := brieflint.Report{
		Exit: 1,
		Findings: []brieflint.Finding{{
			Severity: "BLOCK",
			Category: "anchor-specificity",
			Message:  longBlocker,
		}},
	}

	blockers := briefLintBlockers(report)
	joined := strings.Join(blockers, "\n")

	if !strings.Contains(joined, longBlocker) {
		t.Fatalf("lint blockers lost actionable blocker:\n%s", joined)
	}
	if strings.Contains(joined, "[truncated") {
		t.Fatalf("lint blockers should not include truncation marker:\n%s", joined)
	}
}

func writeImplementationDispatchPreflightFile(t *testing.T, root string, rel string, body string) {
	t.Helper()

	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir parent for %s: %v", rel, err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

func dispatchableBriefBody(path string) string {
	return "# Atom\n\n" +
		"Status: planned\n\n" +
		"## Scope\n\n" +
		"- `" + path + "`\n\n" +
		"## Files to Modify\n\n" +
		"| File | Change |\n" +
		"|---|---|\n" +
		"| `" + path + ":1` | Read current source. |\n\n" +
		"## Verified Current State\n\n" +
		"`" + path + ":1` is current source.\n\n" +
		"## Required Edits\n\n" +
		"Read the file without changing it.\n\n" +
		"## Acceptance\n\n" +
		"- `go test ./...` succeeds.\n"
}

func warningBriefBody(path string) string {
	return "# Atom\n\n" +
		"Status: planned\n\n" +
		"## Scope\n\n" +
		"- `" + path + "`\n\n" +
		"## Files to Modify\n\n" +
		"| File | Change |\n" +
		"|---|---|\n" +
		"| `" + path + "` | ADD `func Added()` at line 2. |\n\n" +
		"## Current state\n\n" +
		"Line 2 is `func Existing() {}`.\n\n" +
		"## Required Edits\n\n" +
		"### Edit 1 - add Added\n\n" +
		"Either add the function alongside Existing or define it in a new file.\n\n" +
		"## Acceptance\n\n" +
		"- `go test ./...` succeeds.\n"
}

func initImplementationDispatchPreflightGitRepo(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	runImplementationDispatchPreflightGit(t, root, "init")
	runImplementationDispatchPreflightGit(t, root, "config", "user.email", "test@example.com")
	runImplementationDispatchPreflightGit(t, root, "config", "user.name", "Test User")

	return root
}

func runImplementationDispatchPreflightGit(t *testing.T, root string, args ...string) {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}

	return false
}
