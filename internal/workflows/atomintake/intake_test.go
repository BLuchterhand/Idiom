package atomintake

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	atomintakecontract "idiom/internal/contracts/atomintake"
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

	if report.State != atomintakecontract.StateDispatchable || !report.Dispatchable {
		t.Fatalf("unexpected report: %+v", report)
	}
}

func TestAtomIntakeCanonicalizesAbsoluteInRootScope(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}

	absPath := filepath.Join(root, "src", "foo.go")
	if err := os.WriteFile(absPath, []byte("package src\n"), 0o644); err != nil {
		t.Fatalf("write foo: %v", err)
	}

	specPath := writeAtomSpec(t, root, "spec.md", "# Atom\n\nStatus: planned\n\n## Scope\n\n- `"+absPath+"`\n")

	report, code, stderr := runAtomIntakeJSONForTest(t, root, specPath)
	if code != 0 {
		t.Fatalf("expected dispatchable exit 0, got %d stderr=%s report=%+v", code, stderr, report)
	}

	if report.State != atomintakecontract.StateDispatchable || !report.Dispatchable {
		t.Fatalf("unexpected report: %+v", report)
	}

	if !reflect.DeepEqual(report.ScopePaths, []string{"src/foo.go"}) {
		t.Fatalf("scope paths=%#v, want [src/foo.go]", report.ScopePaths)
	}

	if !reflect.DeepEqual(report.Existing, []string{"src/foo.go"}) {
		t.Fatalf("existing paths=%#v, want [src/foo.go]", report.Existing)
	}

	for _, path := range append(append([]string(nil), report.ScopePaths...), report.Existing...) {
		if filepath.IsAbs(path) {
			t.Fatalf("accepted atom scope path must be workspace-relative, got %q", path)
		}
	}
}

func TestAtomIntakeRefusesAcceptedStatusBeforeScopeDetails(t *testing.T) {
	root := t.TempDir()
	specPath := writeAtomSpec(t, root, "spec.md", "# Atom\n\nStatus: accepted\n")

	report, code, stderr := runAtomIntakeJSONForTest(t, root, specPath)
	if code == 0 {
		t.Fatalf("expected non-dispatchable exit, got 0 stderr=%s", stderr)
	}

	if report.State != atomintakecontract.StateComplete {
		t.Fatalf("state=%q, want %q report=%+v", report.State, atomintakecontract.StateComplete, report)
	}
}

func TestAtomIntakeRefusesSourceSatisfiedStatus(t *testing.T) {
	root := t.TempDir()
	specPath := writeAtomSpec(t, root, "spec.md", "# Atom\n\nStatus: source-satisfied - do not dispatch\n\n## Scope\n\n- `foo.go`\n")

	report, code, stderr := runAtomIntakeJSONForTest(t, root, specPath)
	if code == 0 {
		t.Fatalf("expected non-dispatchable exit, got 0 stderr=%s", stderr)
	}

	if report.State != atomintakecontract.StateSourceSatisfied {
		t.Fatalf("state=%q, want %q report=%+v", report.State, atomintakecontract.StateSourceSatisfied, report)
	}
}

func TestAtomIntakeTerminalStatusClassifiesTrackerStatuses(t *testing.T) {
	for _, tc := range []struct {
		status string
		want   bool
	}{
		{status: "source-satisfied - do not dispatch", want: true},
		{status: "completed", want: true},
		{status: "closed", want: true},
		{status: "accepted", want: true},
		{status: "pending", want: false},
		{status: "planned", want: false},
	} {
		t.Run(tc.status, func(t *testing.T) {
			gotStatus, got := TerminalStatus(tc.status)
			if got != tc.want {
				t.Fatalf("TerminalStatus(%q)=%v, want %v", tc.status, got, tc.want)
			}
			if got && gotStatus != tc.status {
				t.Fatalf("status=%q, want %q", gotStatus, tc.status)
			}
		})
	}
}

func TestAtomIntakeSourceSatisfiedScopePreservesLiteralEventEvidence(t *testing.T) {
	root := t.TempDir()
	path := "internal/surfaces/tui/app/writer_route.go"
	full := filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}

	body := strings.Join([]string{
		"package app",
		"",
		"func emitWriterRouteDecision() {",
		"	fields[\"type\"] = \"writer_route_decision\"",
		"	const namespace = \"idiom.writer_route.\"",
		"}",
		"",
	}, "\n")
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}

	report, ok := EvaluateSourceSatisfiedScope(SourceSatisfiedScopeOptions{
		ProjectRoot: root,
		SpecPath:    ".idiom/atoms/design/atom-007.md",
		SpecBody:    "# Atom\n",
		ScopePath:   path,
		ScopeText:   "Build and emit TUI route decision after `idiom.writer_route_decision.v1` without editing `.idiom/atoms/design/atom-007.md`.",
	})
	if !ok {
		t.Fatalf("expected source-satisfied literal evidence")
	}
	if report.Path != path {
		t.Fatalf("path=%q, want %q", report.Path, path)
	}

	evidence := strings.Join(report.Evidence, "\n")
	if !strings.Contains(evidence, "writer_route_decision") || !strings.Contains(evidence, "idiom.writer_route.") {
		t.Fatalf("expected event leaf and namespace evidence, got %#v", report.Evidence)
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

	if report.State != atomintakecontract.StateSourceSatisfied || report.Dispatchable {
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

	if report.State != atomintakecontract.StateSourceSatisfied || report.Dispatchable {
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

	if report.State != atomintakecontract.StateStaleNeedsRebase || report.Dispatchable {
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

	if report.State != atomintakecontract.StateDispatchable || !report.Dispatchable {
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

	if report.State != atomintakecontract.StateStaleNeedsRebase || report.Dispatchable {
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

	if report.State != atomintakecontract.StateDispatchable || !report.Dispatchable {
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

	if report.State != atomintakecontract.StateDispatchable || !report.Dispatchable {
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

	if report.State != atomintakecontract.StateStaleNeedsRebase || report.Dispatchable {
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

	if report.State != atomintakecontract.StateDispatchable {
		t.Fatalf("state=%q, want %q report=%+v", report.State, atomintakecontract.StateDispatchable, report)
	}
}

func TestAtomIntakeDetectsSourceSatisfiedCurrentStateAnchorScope(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "internal", "contracts", "writerroute", "decision_test.go")

	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}

	current := strings.Join([]string{
		"package writerroute",
		"",
		"func TestWriterRouteDecision_NormalizationAndStableID(t *testing.T) {",
		"	t.Fatal(\"covers normalization stable id\")",
		"}",
		"",
		"func TestWriterRouteDecision_JSONEventFieldMapping(t *testing.T) {",
		"	t.Fatal(\"covers JSON event fields\")",
		"}",
		"",
	}, "\n")
	if err := os.WriteFile(target, []byte(current), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}

	specPath := writeAtomSpec(t, root, "spec.md", strings.Join([]string{
		"# Atom",
		"",
		"Status: pending",
		"",
		"## Files To Modify",
		"",
		"| File | Change |",
		"|---|---|",
		"| internal/contracts/writerroute/decision_test.go | Unit tests for normalization, stable ID, JSON/event fields |",
		"",
		"## Verified Current State",
		"",
		"- Host current-state anchors captured before dispatch:",
		"  - `internal/contracts/writerroute/decision_test.go:3`: `func TestWriterRouteDecision_NormalizationAndStableID(t *testing.T) {`",
		"",
		"## Required Behavior",
		"",
		"Implement only this atom's bounded slice for `internal/contracts/writerroute/decision_test.go`.",
		"",
	}, "\n"))

	report, code, stderr := runAtomIntakeJSONForTest(t, root, specPath)
	if code == 0 {
		t.Fatalf("expected source-satisfied exit, got 0 stderr=%s report=%+v", stderr, report)
	}

	if report.State != atomintakecontract.StateSourceSatisfied || report.Dispatchable {
		t.Fatalf("unexpected report: %+v", report)
	}

	if !reportReasonContains(report, "current source covers the atom's current-state anchored scope") {
		t.Fatalf("expected current-state source-satisfied reason, got %+v", report.Reasons)
	}
}

func TestAtomIntakeDoesNotTreatUncoveredCurrentStateAnchorsAsDone(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "foo.go"), []byte("package main\n\nconst A = \"old\"\n"), 0o644); err != nil {
		t.Fatalf("write foo: %v", err)
	}

	specPath := writeAtomSpec(t, root, "spec.md", strings.Join([]string{
		"# Atom",
		"",
		"Status: pending",
		"",
		"## Files To Modify",
		"",
		"| File | Change |",
		"|---|---|",
		"| foo.go | Replace fixture value with new behavior |",
		"",
		"## Verified Current State",
		"",
		"- Host current-state anchors captured before dispatch:",
		"  - `foo.go:3`: `const A = \"old\"`",
		"",
	}, "\n"))

	report, code, stderr := runAtomIntakeJSONForTest(t, root, specPath)
	if code != 0 {
		t.Fatalf("expected dispatchable exit 0, got %d stderr=%s report=%+v", code, stderr, report)
	}

	if report.State != atomintakecontract.StateDispatchable {
		t.Fatalf("state=%q, want %q report=%+v", report.State, atomintakecontract.StateDispatchable, report)
	}
}

func TestAtomIntakeOptionalCurrentStateScopeRequiresLiveAnchor(t *testing.T) {
	root := t.TempDir()
	current := "package main\n\ntype implementationDispatchPlanDocument struct {\n\tPath string\n}\n"

	if err := os.WriteFile(filepath.Join(root, "dispatch.go"), []byte(current), 0o644); err != nil {
		t.Fatalf("write dispatch: %v", err)
	}

	spec := strings.Join([]string{
		"# Atom",
		"",
		"Status: pending",
		"",
		"## Files To Modify",
		"",
		"| File | Change |",
		"|---|---|",
		"| dispatch.go | If needed, pass or annotate dispatch route without changing behavior |",
		"",
		"## Verified Current State",
		"",
		"- Host current-state anchors captured before dispatch:",
		"  - `dispatch.go:3`: `type implementationDispatchPlanDocument struct {`",
		"",
	}, "\n")
	specPath := writeAtomSpec(t, root, "spec.md", spec)

	report, code, stderr := runAtomIntakeJSONForTest(t, root, specPath)
	if code == 0 {
		t.Fatalf("expected source-satisfied exit, got 0 stderr=%s report=%+v", stderr, report)
	}

	if report.State != atomintakecontract.StateSourceSatisfied || !reportReasonContains(report, "optional current-state anchor remains current") {
		t.Fatalf("expected optional anchor source-satisfied evidence, got %+v", report)
	}
}

func TestAtomIntakeOptionalCurrentStateScopeDoesNotUseWeakSourceBodyTerms(t *testing.T) {
	root := t.TempDir()
	current := "package main\n\ntype implementationDispatchPlanDocument struct {\n\tPath string\n}\n"

	if err := os.WriteFile(filepath.Join(root, "dispatch.go"), []byte(current), 0o644); err != nil {
		t.Fatalf("write dispatch: %v", err)
	}

	spec := strings.Join([]string{
		"# Atom",
		"",
		"Status: pending",
		"",
		"## Files To Modify",
		"",
		"| File | Change |",
		"|---|---|",
		"| dispatch.go | If needed, pass or annotate dispatch route without changing behavior |",
		"",
		"## Verified Current State",
		"",
		"- Host current-state anchors captured before dispatch:",
		"  - `dispatch.go:3`: `func staleAnchor() {}`",
		"",
	}, "\n")
	specPath := writeAtomSpec(t, root, "spec.md", spec)

	report, code, stderr := runAtomIntakeJSONForTest(t, root, specPath)
	if code != 0 {
		t.Fatalf("expected dispatchable exit 0, got %d stderr=%s report=%+v", code, stderr, report)
	}

	if report.State != atomintakecontract.StateDispatchable {
		t.Fatalf("state=%q, want %q report=%+v", report.State, atomintakecontract.StateDispatchable, report)
	}
}

func TestAtomIntakeBlocksMissingScope(t *testing.T) {
	root := t.TempDir()
	specPath := writeAtomSpec(t, root, "spec.md", "# Atom\n\nStatus: planned\n\n## Acceptance\n\n- tests pass\n")

	report, code, stderr := runAtomIntakeJSONForTest(t, root, specPath)
	if code == 0 {
		t.Fatalf("expected non-dispatchable exit, got 0 stderr=%s", stderr)
	}

	if report.State != atomintakecontract.StateMissingScope {
		t.Fatalf("state=%q, want %q report=%+v", report.State, atomintakecontract.StateMissingScope, report)
	}
}

func TestAtomIntakeUsesExpectedFileScopeAndIgnoresDottedSymbolNonGoal(t *testing.T) {
	root := t.TempDir()
	specPath := writeAtomSpec(t, root, "spec.md", "# Atom\n\nStatus: implementation handoff\n\n## Expected File Scope\n\n| File | Purpose |\n|---|---|\n| `internal/contracts/writerroute/decision.go` | Domain type. |\n| `internal/surfaces/tui/app/prompt_routing.go` or new `internal/surfaces/tui/app/writer_route.go` | TUI event wiring. |\n\n## Non-Goals\n\n- Do not add `idiom.code_draft.v1` implementation.\n")

	report, code, stderr := runAtomIntakeJSONForTest(t, root, specPath)
	if code != 0 {
		t.Fatalf("expected dispatchable exit 0, got %d stderr=%s report=%+v", code, stderr, report)
	}

	if report.State != atomintakecontract.StateDispatchable {
		t.Fatalf("state=%q, want %q report=%+v", report.State, atomintakecontract.StateDispatchable, report)
	}

	want := []string{
		"internal/contracts/writerroute/decision.go",
		"internal/surfaces/tui/app/prompt_routing.go",
		"internal/surfaces/tui/app/writer_route.go",
	}
	if !reflect.DeepEqual(report.ScopePaths, want) {
		t.Fatalf("scope paths=%#v, want %#v", report.ScopePaths, want)
	}

	if len(report.Invalid) != 0 {
		t.Fatalf("unexpected invalid scope paths: %+v", report)
	}
}

func TestAtomIntakeBlocksScopeOutsideProjectRoot(t *testing.T) {
	root := t.TempDir()
	specPath := writeAtomSpec(t, root, "spec.md", "# Atom\n\nStatus: planned\n\n## Scope\n\n- `../outside.go`\n")

	report, code, stderr := runAtomIntakeJSONForTest(t, root, specPath)
	if code == 0 {
		t.Fatalf("expected non-dispatchable exit, got 0 stderr=%s", stderr)
	}

	if report.State != atomintakecontract.StateInvalidScope {
		t.Fatalf("state=%q, want %q report=%+v", report.State, atomintakecontract.StateInvalidScope, report)
	}

	if len(report.Invalid) != 1 || report.Invalid[0] != "../outside.go" {
		t.Fatalf("invalid=%v, want [../outside.go]", report.Invalid)
	}

	if got := report.InvalidReasons["../outside.go"]; got != atomintakecontract.InvalidScopeOutsideProjectRoot {
		t.Fatalf("invalid reason=%q, want %q; report=%+v", got, atomintakecontract.InvalidScopeOutsideProjectRoot, report)
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

	if report.State != atomintakecontract.StateInvalidScope {
		t.Fatalf("state=%q, want %q report=%+v", report.State, atomintakecontract.StateInvalidScope, report)
	}

	if len(report.Invalid) != 1 || report.Invalid[0] != "test_codex_generation.cpp" {
		t.Fatalf("invalid=%v, want [test_codex_generation.cpp]", report.Invalid)
	}

	if got := report.InvalidReasons["test_codex_generation.cpp"]; got != atomintakecontract.InvalidScopeAmbiguousBareShorthand {
		t.Fatalf("invalid reason=%q, want %q; report=%+v", got, atomintakecontract.InvalidScopeAmbiguousBareShorthand, report)
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

	if report.State != atomintakecontract.StateInvalidScope {
		t.Fatalf("state=%q, want %q report=%+v", report.State, atomintakecontract.StateInvalidScope, report)
	}

	if got := report.InvalidReasons["missing.cpp"]; got != atomintakecontract.InvalidScopeAmbiguousBareShorthand {
		t.Fatalf("missing.cpp invalid reason=%q, want %q; report=%+v", got, atomintakecontract.InvalidScopeAmbiguousBareShorthand, report)
	}

	if got := report.InvalidReasons["../outside.go"]; got != atomintakecontract.InvalidScopeOutsideProjectRoot {
		t.Fatalf("../outside.go invalid reason=%q, want %q; report=%+v", got, atomintakecontract.InvalidScopeOutsideProjectRoot, report)
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

	if report.State != atomintakecontract.StateInvalidScope {
		t.Fatalf("state=%q, want %q report=%+v", report.State, atomintakecontract.StateInvalidScope, report)
	}

	if len(report.Invalid) != 1 || report.Invalid[0] != ".unrecognized-toolrc" {
		t.Fatalf("invalid=%v, want [.unrecognized-toolrc]", report.Invalid)
	}

	if got := report.InvalidReasons[".unrecognized-toolrc"]; got != atomintakecontract.InvalidScopeAmbiguousBareShorthand {
		t.Fatalf("invalid reason=%q, want %q; report=%+v", got, atomintakecontract.InvalidScopeAmbiguousBareShorthand, report)
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

	if report.State != atomintakecontract.StateInvalidScope {
		t.Fatalf("state=%q, want %q report=%+v", report.State, atomintakecontract.StateInvalidScope, report)
	}

	if len(report.Invalid) != 1 || report.Invalid[0] != "link/secret.go" {
		t.Fatalf("invalid=%v, want [link/secret.go]", report.Invalid)
	}

	if got := report.InvalidReasons["link/secret.go"]; got != atomintakecontract.InvalidScopeOutsideProjectRoot {
		t.Fatalf("invalid reason=%q, want %q; report=%+v", got, atomintakecontract.InvalidScopeOutsideProjectRoot, report)
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

	if report.State != atomintakecontract.StateStaleNeedsRebase {
		t.Fatalf("state=%q, want %q report=%+v", report.State, atomintakecontract.StateStaleNeedsRebase, report)
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

	if report.State != atomintakecontract.StateFailedRefine {
		t.Fatalf("state=%q, want %q report=%+v", report.State, atomintakecontract.StateFailedRefine, report)
	}

	if got := SpecDispatchBlocker(mustReadString(t, specPath)); !strings.Contains(got, "max-iters-exhausted") {
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

	if report.State != atomintakecontract.StateDispatchable {
		t.Fatalf("state=%q, want %q report=%+v", report.State, atomintakecontract.StateDispatchable, report)
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

	if report.State != atomintakecontract.StateBlockedDirty {
		t.Fatalf("state=%q, want %q report=%+v", report.State, atomintakecontract.StateBlockedDirty, report)
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

	report, err := RunIntake(IntakeOptions{SpecPath: specPath, ProjectRoot: root, AllowDirty: true}, Dependencies{})
	if err != nil {
		t.Fatalf("RunIntake: %v", err)
	}

	if !report.Dispatchable || report.State != atomintakecontract.StateDispatchable {
		t.Fatalf("expected allow-dirty dispatchable report, got %+v", report)
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
		if got := ParseGitPorcelainPath(line); got != want {
			t.Fatalf("ParseGitPorcelainPath(%q) = %q, want %q", line, got, want)
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

func reportReasonContains(report IntakeReport, needle string) bool {
	for _, reason := range report.Reasons {
		if strings.Contains(reason, needle) {
			return true
		}
	}

	return false
}

func runAtomIntakeJSONForTest(t *testing.T, root, specPath string) (IntakeReport, int, string) {
	t.Helper()

	report, err := RunIntake(IntakeOptions{SpecPath: specPath, ProjectRoot: root}, Dependencies{})
	if err != nil {
		return IntakeReport{}, 1, err.Error()
	}

	if report.Type != "idiom.atom.intake" {
		t.Fatalf("unexpected report type %q", report.Type)
	}

	if strings.TrimSpace(report.SpecPath) == "" {
		t.Fatalf("report missing spec path: %+v", report)
	}

	if report.Dispatchable {
		return report, 0, ""
	}

	return report, 1, ""
}

func initGitRepoForStaleness(t *testing.T) string {
	t.Helper()

	root := t.TempDir()

	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "test"},
	} {
		cmd := exec.Command("git", args...)

		cmd.Dir = root

		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}

	return root
}

func gitCommit(t *testing.T, root, path, content, message string) {
	t.Helper()

	full := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}

	for _, args := range [][]string{
		{"add", path},
		{"commit", "-q", "-m", message},
	} {
		cmd := exec.Command("git", args...)

		cmd.Dir = root

		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
}
