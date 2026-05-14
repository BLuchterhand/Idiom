package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAtomIntakeDispatchablePlannedScope(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "foo.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write foo: %v", err)
	}
	specPath := writeAtomSpec(t, root, "spec.md", "# Atom\n\nStatus: planned\n\n## Scope\n\n- `foo.go`\n")

	report, code, stderr := runAtomIntakeJSONForTest(t, root, specPath)
	if code != 0 {
		t.Fatalf("expected dispatchable exit 0, got %d stderr=%s report=%+v", code, stderr, report)
	}
	if report.State != atomStateDispatchable || !report.Dispatchable {
		t.Fatalf("unexpected report: %+v", report)
	}
}

func TestAtomIntakeRefusesAcceptedStatusBeforeScopeDetails(t *testing.T) {
	root := t.TempDir()
	specPath := writeAtomSpec(t, root, "spec.md", "# Atom\n\nStatus: accepted\n")

	report, code, stderr := runAtomIntakeJSONForTest(t, root, specPath)
	if code == 0 {
		t.Fatalf("expected non-dispatchable exit, got 0 stderr=%s", stderr)
	}
	if report.State != atomStateComplete {
		t.Fatalf("state=%q, want %q report=%+v", report.State, atomStateComplete, report)
	}
}

func TestAtomIntakeRefusesSourceSatisfiedStatus(t *testing.T) {
	root := t.TempDir()
	specPath := writeAtomSpec(t, root, "spec.md", "# Atom\n\nStatus: source-satisfied - do not dispatch\n\n## Scope\n\n- `foo.go`\n")

	report, code, stderr := runAtomIntakeJSONForTest(t, root, specPath)
	if code == 0 {
		t.Fatalf("expected non-dispatchable exit, got 0 stderr=%s", stderr)
	}
	if report.State != atomStateSourceSatisfied {
		t.Fatalf("state=%q, want %q report=%+v", report.State, atomStateSourceSatisfied, report)
	}
}

func TestAtomIntakeDetectsSourceSatisfiedRequiredEditSnippets(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "foo.go"), []byte("package main\n\nfunc bounded() int {\n\treturn 42\n}\n"), 0o644); err != nil {
		t.Fatalf("write foo: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "foo_test.go"), []byte("package main\n\nimport \"testing\"\n\nfunc TestBounded(t *testing.T) {\n\tif bounded() != 42 {\n\t\tt.Fatal(\"wrong value\")\n\t}\n}\n"), 0o644); err != nil {
		t.Fatalf("write foo_test: %v", err)
	}
	specPath := writeAtomSpec(t, root, "spec.md", "# Atom\n\nStatus: planned\n\n## Files To Modify\n\n| File | Change |\n|---|---|\n| `foo.go` | Add helper. |\n| `foo_test.go` | Add coverage. |\n\n## Required Edits\n\nIn `foo.go`, add:\n\n```go\nfunc bounded() int {\n    return 42\n}\n```\n\nIn `foo_test.go`, add:\n\n```go\nfunc TestBounded(t *testing.T) {\n    if bounded() != 42 {\n        t.Fatal(\"wrong value\")\n    }\n}\n```\n")

	report, code, stderr := runAtomIntakeJSONForTest(t, root, specPath)
	if code == 0 {
		t.Fatalf("expected source-satisfied exit, got 0 stderr=%s report=%+v", stderr, report)
	}
	if report.State != atomStateSourceSatisfied || report.Dispatchable {
		t.Fatalf("unexpected report: %+v", report)
	}
	if !reportReasonContains(report, "all required edit snippets already appear") {
		t.Fatalf("expected source-satisfied snippet reason, got %+v", report.Reasons)
	}
}

func TestAtomIntakeDuplicatesSharedRequiredEditSnippetPaths(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{"a.h", "a.cpp"} {
		if err := os.WriteFile(filepath.Join(root, path), []byte("// new shared comment\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	specPath := writeAtomSpec(t, root, "spec.md", "# Atom\n\nStatus: planned\n\n## Files To Modify\n\n| File | Change |\n|---|---|\n| `a.h` | Update header comment. |\n| `a.cpp` | Update implementation comment. |\n\n## Required Edits\n\nUpdate the `a.h` and `a.cpp` comments.\n\nold_text:\n\n```text\n// old shared comment\n```\n\nnew_text:\n\n```text\n// new shared comment\n```\n")

	report, code, stderr := runAtomIntakeJSONForTest(t, root, specPath)
	if code == 0 {
		t.Fatalf("expected source-satisfied exit, got 0 stderr=%s report=%+v", stderr, report)
	}
	if report.State != atomStateSourceSatisfied || report.Dispatchable {
		t.Fatalf("shared snippet should satisfy both mentioned paths, got %+v", report)
	}
}

func TestAtomIntakeBlocksPartiallyAppliedRequiredEditSnippets(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "foo.go"), []byte("package main\n\nfunc bounded() int {\n\treturn 42\n}\n"), 0o644); err != nil {
		t.Fatalf("write foo: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "foo_test.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write foo_test: %v", err)
	}
	specPath := writeAtomSpec(t, root, "spec.md", "# Atom\n\nStatus: planned\n\n## Files To Modify\n\n| File | Change |\n|---|---|\n| `foo.go` | Add helper. |\n| `foo_test.go` | Add coverage. |\n\n## Required Edits\n\nIn `foo.go`, add:\n\n```go\nfunc bounded() int {\n    return 42\n}\n```\n\nIn `foo_test.go`, add:\n\n```go\nfunc TestBounded(t *testing.T) {\n    if bounded() != 42 {\n        t.Fatal(\"wrong value\")\n    }\n}\n```\n")

	report, code, stderr := runAtomIntakeJSONForTest(t, root, specPath)
	if code == 0 {
		t.Fatalf("expected stale-needs-rebase exit, got 0 stderr=%s report=%+v", stderr, report)
	}
	if report.State != atomStateStaleNeedsRebase || report.Dispatchable {
		t.Fatalf("unexpected report: %+v", report)
	}
}

func TestAtomIntakeKeepsDispatchableWhenNoRequiredSnippetMatches(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "foo.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write foo: %v", err)
	}
	specPath := writeAtomSpec(t, root, "spec.md", "# Atom\n\nStatus: planned\n\n## Files To Modify\n\n| File | Change |\n|---|---|\n| `foo.go` | Add helper. |\n\n## Required Edits\n\nIn `foo.go`, add:\n\n```go\nfunc bounded() int {\n    return 42\n}\n```\n")

	report, code, stderr := runAtomIntakeJSONForTest(t, root, specPath)
	if code != 0 {
		t.Fatalf("expected dispatchable exit 0, got %d stderr=%s report=%+v", code, stderr, report)
	}
	if report.State != atomStateDispatchable || !report.Dispatchable {
		t.Fatalf("unexpected report: %+v", report)
	}
}

func TestAtomIntakeBlocksSupersededRequiredEditSnippet(t *testing.T) {
	root := t.TempDir()
	current := `package main

func buildCandidate() Candidate {
	return Candidate{
		Owner: owner,
		Slot: slot,
		Quality: quality,
		Ground: ownerIsGround,
	}
}
`
	if err := os.WriteFile(filepath.Join(root, "foo.go"), []byte(current), 0o644); err != nil {
		t.Fatalf("write foo: %v", err)
	}
	specPath := writeAtomSpec(t, root, "spec.md", "# Atom\n\nStatus: planned\n\n## Files To Modify\n\n| File | Change |\n|---|---|\n| `foo.go` | Build candidate through the bounded helper. |\n\n## Required Edits\n\nIn `foo.go`, replace the candidate block with:\n\n```go\nfunc buildCandidate() Candidate {\n    return Candidate{\n        Owner: owner,\n        Slot: slot,\n        Quality: quality,\n        Ground: false,\n    }\n}\n```\n")

	report, code, stderr := runAtomIntakeJSONForTest(t, root, specPath)
	if code == 0 {
		t.Fatalf("expected stale-needs-rebase exit, got 0 stderr=%s report=%+v", stderr, report)
	}
	if report.State != atomStateStaleNeedsRebase || report.Dispatchable {
		t.Fatalf("unexpected report: %+v", report)
	}
	if !reportReasonContains(report, "near-matches required_edit_1") {
		t.Fatalf("expected near-match evidence, got %+v", report.Reasons)
	}
}

func TestAtomIntakeAllowsAnchoredInsertionWhenOldTextStillMatches(t *testing.T) {
	root := t.TempDir()
	current := `package main

import (
	"a"
	"c"
)
`
	if err := os.WriteFile(filepath.Join(root, "foo.go"), []byte(current), 0o644); err != nil {
		t.Fatalf("write foo: %v", err)
	}
	specPath := writeAtomSpec(t, root, "spec.md", "# Atom\n\nStatus: planned\n\n## Files To Modify\n\n| File | Change |\n|---|---|\n| `foo.go` | Add the missing import. |\n\n## Required Edits\n\nIn `foo.go`, replace the import block.\n\nold_text:\n\n```go\nimport (\n    \"a\"\n    \"c\"\n)\n```\n\nnew_text:\n\n```go\nimport (\n    \"a\"\n    \"b\"\n    \"c\"\n)\n```\n")

	report, code, stderr := runAtomIntakeJSONForTest(t, root, specPath)
	if code != 0 {
		t.Fatalf("expected dispatchable exit 0, got %d stderr=%s report=%+v", code, stderr, report)
	}
	if report.State != atomStateDispatchable || !report.Dispatchable {
		t.Fatalf("anchored insertion should remain dispatchable, got %+v", report)
	}
	if reportReasonContains(report, "near-matches required_edit_1") {
		t.Fatalf("anchored insertion should not be reported stale, got %+v", report.Reasons)
	}
}

func TestAtomIntakeAllowsAnchoredDeletionWhenNewTextIsOrderedSubset(t *testing.T) {
	root := t.TempDir()
	current := `package main

import (
	"a"
	"b"
	"c"
)
`
	if err := os.WriteFile(filepath.Join(root, "foo.go"), []byte(current), 0o644); err != nil {
		t.Fatalf("write foo: %v", err)
	}
	specPath := writeAtomSpec(t, root, "spec.md", "# Atom\n\nStatus: planned\n\n## Files To Modify\n\n| File | Change |\n|---|---|\n| `foo.go` | Remove the obsolete import. |\n\n## Required Edits\n\nIn `foo.go`, replace the import block.\n\nold_text:\n\n```go\nimport (\n    \"a\"\n    \"b\"\n    \"c\"\n)\n```\n\nnew_text:\n\n```go\nimport (\n    \"a\"\n    \"c\"\n)\n```\n")

	report, code, stderr := runAtomIntakeJSONForTest(t, root, specPath)
	if code != 0 {
		t.Fatalf("expected dispatchable exit 0, got %d stderr=%s report=%+v", code, stderr, report)
	}
	if report.State != atomStateDispatchable || !report.Dispatchable {
		t.Fatalf("anchored deletion should remain dispatchable, got %+v", report)
	}
	if reportReasonContains(report, "contains required_edit_1") || reportReasonContains(report, "near-matches required_edit_1") {
		t.Fatalf("anchored deletion should not be reported stale, got %+v", report.Reasons)
	}
}

func TestAtomIntakeBlocksAnchoredNearMatchWhenOldTextNoLongerMatches(t *testing.T) {
	root := t.TempDir()
	current := `package main

func buildCandidate() Candidate {
	return Candidate{
		Owner: owner,
		Slot: slot,
		Quality: quality,
		Ground: ownerIsGround,
	}
}
`
	if err := os.WriteFile(filepath.Join(root, "foo.go"), []byte(current), 0o644); err != nil {
		t.Fatalf("write foo: %v", err)
	}
	specPath := writeAtomSpec(t, root, "spec.md", "# Atom\n\nStatus: planned\n\n## Files To Modify\n\n| File | Change |\n|---|---|\n| `foo.go` | Build candidate through the bounded helper. |\n\n## Required Edits\n\nIn `foo.go`, replace the candidate block.\n\nold_text:\n\n```go\nfunc buildCandidate() Candidate {\n    return Candidate{\n        Owner: owner,\n        Slot: slot,\n        Quality: quality,\n        Ground: true,\n    }\n}\n```\n\nnew_text:\n\n```go\nfunc buildCandidate() Candidate {\n    return Candidate{\n        Owner: owner,\n        Slot: slot,\n        Quality: quality,\n        Ground: false,\n    }\n}\n```\n")

	report, code, stderr := runAtomIntakeJSONForTest(t, root, specPath)
	if code == 0 {
		t.Fatalf("expected stale-needs-rebase exit, got 0 stderr=%s report=%+v", stderr, report)
	}
	if report.State != atomStateStaleNeedsRebase || report.Dispatchable {
		t.Fatalf("unexpected report: %+v", report)
	}
	if !reportReasonContains(report, "near-matches required_edit_1") {
		t.Fatalf("expected near-match evidence, got %+v", report.Reasons)
	}
}

func TestAtomIntakeIgnoresCurrentStateFencesForSourceSatisfied(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "foo.go"), []byte("package main\n\nfunc existing() {}\n"), 0o644); err != nil {
		t.Fatalf("write foo: %v", err)
	}
	specPath := writeAtomSpec(t, root, "spec.md", "# Atom\n\nStatus: planned\n\n## Scope\n\n- `foo.go`\n\n## Required Edits\n\nCurrent:\n\n```go\nfunc existing() {}\n```\n")

	report, code, stderr := runAtomIntakeJSONForTest(t, root, specPath)
	if code != 0 {
		t.Fatalf("expected dispatchable exit 0, got %d stderr=%s report=%+v", code, stderr, report)
	}
	if report.State != atomStateDispatchable {
		t.Fatalf("state=%q, want %q report=%+v", report.State, atomStateDispatchable, report)
	}
}

func TestAtomIntakeBlocksMissingScope(t *testing.T) {
	root := t.TempDir()
	specPath := writeAtomSpec(t, root, "spec.md", "# Atom\n\nStatus: planned\n\n## Acceptance\n\n- tests pass\n")

	report, code, stderr := runAtomIntakeJSONForTest(t, root, specPath)
	if code == 0 {
		t.Fatalf("expected non-dispatchable exit, got 0 stderr=%s", stderr)
	}
	if report.State != atomStateMissingScope {
		t.Fatalf("state=%q, want %q report=%+v", report.State, atomStateMissingScope, report)
	}
}

func TestAtomIntakeBlocksScopeOutsideProjectRoot(t *testing.T) {
	root := t.TempDir()
	specPath := writeAtomSpec(t, root, "spec.md", "# Atom\n\nStatus: planned\n\n## Scope\n\n- `../outside.go`\n")

	report, code, stderr := runAtomIntakeJSONForTest(t, root, specPath)
	if code == 0 {
		t.Fatalf("expected non-dispatchable exit, got 0 stderr=%s", stderr)
	}
	if report.State != atomStateInvalidScope {
		t.Fatalf("state=%q, want %q report=%+v", report.State, atomStateInvalidScope, report)
	}
	if len(report.Invalid) != 1 || report.Invalid[0] != "../outside.go" {
		t.Fatalf("invalid=%v, want [../outside.go]", report.Invalid)
	}
	if got := report.InvalidReasons["../outside.go"]; got != atomInvalidScopeOutsideProjectRoot {
		t.Fatalf("invalid reason=%q, want %q; report=%+v", got, atomInvalidScopeOutsideProjectRoot, report)
	}
	if !reportReasonContains(report, "outside the project root") {
		t.Fatalf("expected outside-root reason, got %+v", report.Reasons)
	}
}

func TestAtomIntakeRejectsMissingBareFileShorthand(t *testing.T) {
	root := t.TempDir()
	specPath := writeAtomSpec(t, root, "spec.md", "# Atom\n\nStatus: planned\n\n## Files To Modify\n\n| File | Change |\n|---|---|\n| `test_codex_generation.cpp` | Shorthand basename from prose should not authorize a root write. |\n| `native/tests/test_codex_generation.cpp` | Concrete repo-relative new file. |\n")

	report, code, stderr := runAtomIntakeJSONForTest(t, root, specPath)
	if code == 0 {
		t.Fatalf("expected invalid scope exit, got 0 stderr=%s report=%+v", stderr, report)
	}
	if report.State != atomStateInvalidScope {
		t.Fatalf("state=%q, want %q report=%+v", report.State, atomStateInvalidScope, report)
	}
	if len(report.Invalid) != 1 || report.Invalid[0] != "test_codex_generation.cpp" {
		t.Fatalf("invalid=%v, want [test_codex_generation.cpp]", report.Invalid)
	}
	if got := report.InvalidReasons["test_codex_generation.cpp"]; got != atomInvalidScopeAmbiguousBareShorthand {
		t.Fatalf("invalid reason=%q, want %q; report=%+v", got, atomInvalidScopeAmbiguousBareShorthand, report)
	}
	if reportReasonContains(report, "outside the project root") {
		t.Fatalf("ambiguous bare shorthand reason must not imply outside-root, got %+v", report.Reasons)
	}
	if !reportReasonContains(report, "ambiguous bare shorthand") {
		t.Fatalf("expected ambiguous bare shorthand reason, got %+v", report.Reasons)
	}
	if len(report.Missing) != 1 || report.Missing[0] != "native/tests/test_codex_generation.cpp" {
		t.Fatalf("missing=%v, want [native/tests/test_codex_generation.cpp]", report.Missing)
	}
}

func TestAtomIntakeReportsMixedInvalidScopeReasons(t *testing.T) {
	root := t.TempDir()
	specPath := writeAtomSpec(t, root, "spec.md", "# Atom\n\nStatus: planned\n\n## Scope\n\n- `missing.cpp`\n- `../outside.go`\n")

	report, code, stderr := runAtomIntakeJSONForTest(t, root, specPath)
	if code == 0 {
		t.Fatalf("expected invalid scope exit, got 0 stderr=%s report=%+v", stderr, report)
	}
	if report.State != atomStateInvalidScope {
		t.Fatalf("state=%q, want %q report=%+v", report.State, atomStateInvalidScope, report)
	}
	if got := report.InvalidReasons["missing.cpp"]; got != atomInvalidScopeAmbiguousBareShorthand {
		t.Fatalf("missing.cpp invalid reason=%q, want %q; report=%+v", got, atomInvalidScopeAmbiguousBareShorthand, report)
	}
	if got := report.InvalidReasons["../outside.go"]; got != atomInvalidScopeOutsideProjectRoot {
		t.Fatalf("../outside.go invalid reason=%q, want %q; report=%+v", got, atomInvalidScopeOutsideProjectRoot, report)
	}
	if !reportReasonContains(report, "outside the project root") || !reportReasonContains(report, "ambiguous bare shorthand") {
		t.Fatalf("expected mixed invalid-scope summary, got %+v", report.Reasons)
	}
}

func TestAtomIntakeMissingBareFileShorthandUsesKnownBasenameAllowlist(t *testing.T) {
	root := t.TempDir()
	specPath := writeAtomSpec(t, root, "spec.md", "# Atom\n\nStatus: planned\n\n## Files To Modify\n\n| File | Change |\n|---|---|\n| `.gitignore` | Known repository-root basename remains allowed. |\n| `.unrecognized-toolrc` | Unknown bare dotfile shorthand should not authorize a root write. |\n")

	report, code, stderr := runAtomIntakeJSONForTest(t, root, specPath)
	if code == 0 {
		t.Fatalf("expected invalid scope exit, got 0 stderr=%s report=%+v", stderr, report)
	}
	if report.State != atomStateInvalidScope {
		t.Fatalf("state=%q, want %q report=%+v", report.State, atomStateInvalidScope, report)
	}
	if len(report.Invalid) != 1 || report.Invalid[0] != ".unrecognized-toolrc" {
		t.Fatalf("invalid=%v, want [.unrecognized-toolrc]", report.Invalid)
	}
	if got := report.InvalidReasons[".unrecognized-toolrc"]; got != atomInvalidScopeAmbiguousBareShorthand {
		t.Fatalf("invalid reason=%q, want %q; report=%+v", got, atomInvalidScopeAmbiguousBareShorthand, report)
	}
	if len(report.Missing) != 1 || report.Missing[0] != ".gitignore" {
		t.Fatalf("missing=%v, want [.gitignore]", report.Missing)
	}
}

func TestAtomIntakeBlocksSymlinkEscapeFromProjectRoot(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.go"), []byte("package outside\n"), 0o644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	specPath := writeAtomSpec(t, root, "spec.md", "# Atom\n\nStatus: planned\n\n## Scope\n\n- `link/secret.go`\n")

	report, code, stderr := runAtomIntakeJSONForTest(t, root, specPath)
	if code == 0 {
		t.Fatalf("expected non-dispatchable exit, got 0 stderr=%s", stderr)
	}
	if report.State != atomStateInvalidScope {
		t.Fatalf("state=%q, want %q report=%+v", report.State, atomStateInvalidScope, report)
	}
	if len(report.Invalid) != 1 || report.Invalid[0] != "link/secret.go" {
		t.Fatalf("invalid=%v, want [link/secret.go]", report.Invalid)
	}
	if got := report.InvalidReasons["link/secret.go"]; got != atomInvalidScopeOutsideProjectRoot {
		t.Fatalf("invalid reason=%q, want %q; report=%+v", got, atomInvalidScopeOutsideProjectRoot, report)
	}
}

func TestAtomIntakeDetectsAcceptedResultContradiction(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "foo.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write foo: %v", err)
	}
	specPath := writeAtomSpec(t, root, "spec.md", "# Atom\n\nStatus: planned\n\n## Scope\n\n- `foo.go`\n\n## Result\n\n- Accepted after verification.\n")

	report, code, stderr := runAtomIntakeJSONForTest(t, root, specPath)
	if code == 0 {
		t.Fatalf("expected non-dispatchable exit, got 0 stderr=%s", stderr)
	}
	if report.State != atomStateStaleNeedsRebase {
		t.Fatalf("state=%q, want %q report=%+v", report.State, atomStateStaleNeedsRebase, report)
	}
}

func TestAtomIntakeBlocksFailedRefineBestDraft(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "foo.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write foo: %v", err)
	}
	specPath := writeAtomSpec(t, root, "brief.md", "> WARNING: refine exited non-zero — reason=max-iters-exhausted after iter 3.\n\n<!-- idiom:refine-drafted title=atom iter=3 reason=max-iters-exhausted -->\n# Brief\n\n## Files To Modify\n\n| File | Change |\n|---|---|\n| `foo.go` | Modify source. |\n")

	report, code, stderr := runAtomIntakeJSONForTest(t, root, specPath)
	if code == 0 {
		t.Fatalf("expected non-dispatchable exit, got 0 stderr=%s", stderr)
	}
	if report.State != atomStateFailedRefine {
		t.Fatalf("state=%q, want %q report=%+v", report.State, atomStateFailedRefine, report)
	}
	if got := atomSpecDispatchBlocker(mustReadString(t, specPath)); !strings.Contains(got, "max-iters-exhausted") {
		t.Fatalf("expected refine blocker to mention max-iters-exhausted, got %q", got)
	}
}

func TestAtomIntakeAllowsShipAsIsRefineDraft(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "foo.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write foo: %v", err)
	}
	specPath := writeAtomSpec(t, root, "brief.md", "<!-- idiom:refine-drafted title=atom iter=1 reason=ship-as-is -->\n# Brief\n\n## Files To Modify\n\n| File | Change |\n|---|---|\n| `foo.go` | Modify source. |\n")

	report, code, stderr := runAtomIntakeJSONForTest(t, root, specPath)
	if code != 0 {
		t.Fatalf("expected dispatchable exit 0, got %d stderr=%s report=%+v", code, stderr, report)
	}
	if report.State != atomStateDispatchable {
		t.Fatalf("state=%q, want %q report=%+v", report.State, atomStateDispatchable, report)
	}
}

func TestAtomIntakeBlocksDirtyScopedPaths(t *testing.T) {
	root := initGitRepoForStaleness(t)
	gitCommit(t, root, "foo.go", "package main\n", "initial")
	if err := os.WriteFile(filepath.Join(root, "foo.go"), []byte("package main\n\nfunc dirty() {}\n"), 0o644); err != nil {
		t.Fatalf("dirty foo: %v", err)
	}
	specPath := writeAtomSpec(t, root, "spec.md", "# Atom\n\nStatus: planned\n\n## Scope\n\n- `foo.go`\n")

	report, code, stderr := runAtomIntakeJSONForTest(t, root, specPath)
	if code == 0 {
		t.Fatalf("expected non-dispatchable exit, got 0 stderr=%s", stderr)
	}
	if report.State != atomStateBlockedDirty {
		t.Fatalf("state=%q, want %q report=%+v", report.State, atomStateBlockedDirty, report)
	}
	if len(report.Dirty) != 1 || report.Dirty[0] != "foo.go" {
		t.Fatalf("dirty=%v, want [foo.go]", report.Dirty)
	}
}

func TestAtomIntakeAllowDirtyMakesDirtyScopeDispatchable(t *testing.T) {
	root := initGitRepoForStaleness(t)
	gitCommit(t, root, "foo.go", "package main\n", "initial")
	if err := os.WriteFile(filepath.Join(root, "foo.go"), []byte("package main\n\nfunc dirty() {}\n"), 0o644); err != nil {
		t.Fatalf("dirty foo: %v", err)
	}
	specPath := writeAtomSpec(t, root, "spec.md", "# Atom\n\nStatus: planned\n\n## Scope\n\n- `foo.go`\n")

	var stdout, stderr bytes.Buffer
	code := runAtomIntakeMain([]string{"--spec", specPath, "--project-root", root, "--json", "--allow-dirty"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected allow-dirty dispatchable exit, got %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
}

func TestRunMainRoutesAtomCommand(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "foo.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write foo: %v", err)
	}
	specPath := writeAtomSpec(t, root, "spec.md", "# Atom\n\nStatus: planned\n\n## Scope\n\n- `foo.go`\n")

	var stdout, stderr bytes.Buffer
	code := runMain([]string{"atom", "intake", "--spec", specPath, "--project-root", root, "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected routed atom intake exit 0, got %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
}

func TestParseGitPorcelainPathSupportsStrictAndCompactStatus(t *testing.T) {
	cases := map[string]string{
		" M foo.go":               "foo.go",
		"M  foo.go":               "foo.go",
		"?? foo.go":               "foo.go",
		"M foo.go":                "foo.go",
		"R  old.go -> new.go":     "new.go",
		` M "path/with space.go"`: "path/with space.go",
	}
	for line, want := range cases {
		if got := parseGitPorcelainPath(line); got != want {
			t.Fatalf("parseGitPorcelainPath(%q) = %q, want %q", line, got, want)
		}
	}
}

func writeAtomSpec(t *testing.T, root, name, body string) string {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir spec: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	return path
}

func mustReadString(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(body)
}

func reportReasonContains(report atomIntakeReport, needle string) bool {
	for _, reason := range report.Reasons {
		if strings.Contains(reason, needle) {
			return true
		}
	}
	return false
}

func runAtomIntakeJSONForTest(t *testing.T, root, specPath string) (atomIntakeReport, int, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := runAtomIntakeMain([]string{"--spec", specPath, "--project-root", root, "--json"}, &stdout, &stderr)
	if stderr.Len() != 0 && stdout.Len() == 0 {
		return atomIntakeReport{}, code, stderr.String()
	}
	var report atomIntakeReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode report from %q: %v; stderr=%s", stdout.String(), err, stderr.String())
	}
	if report.Type != "idiom.atom.intake" {
		t.Fatalf("unexpected report type %q", report.Type)
	}
	if strings.TrimSpace(report.SpecPath) == "" {
		t.Fatalf("report missing spec path: %+v", report)
	}
	return report, code, stderr.String()
}
