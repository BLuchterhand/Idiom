package productworkflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLintEffortFolderRejectsSymlinkedRequiredFile(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "workflow_state.json")
	if err := os.WriteFile(outside, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "workflow_state.json")); err != nil {
		t.Fatal(err)
	}

	result := LintEffortFolder(root)
	if !lintSummaryHasFinding(result, "symlink_file", "workflow_state.json") {
		t.Fatalf("expected symlink_file finding for workflow_state.json, got %#v", result.Findings)
	}
}

func TestLintEffortFolderRejectsSymlinkedReportFile(t *testing.T) {
	root := t.TempDir()
	reportsDir := filepath.Join(root, "reports")
	if err := os.MkdirAll(reportsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "review.report.json")
	if err := os.WriteFile(outside, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(reportsDir, "review.report.json")); err != nil {
		t.Fatal(err)
	}

	result := LintEffortFolder(root)
	if !lintSummaryHasFinding(result, "symlink_file", filepath.Join("reports", "review.report.json")) {
		t.Fatalf("expected symlink_file finding for report, got %#v", result.Findings)
	}
}

func TestLintEffortFolderDetectsADRIndexFrontMatterDivergence(t *testing.T) {
	root := t.TempDir()
	writeLintFixtureFile(t, root, "adrs/index.json", `{"schema_version":"v1","effort_id":"test-effort","adrs":[{"adr_id":"ADR-001","title":"Test ADR","status":"accepted","path":"ADR-001-test.md","coverage_tier":"unit","prd_refs":["REQ-INDEX"]}]}`)
	writeLintFixtureFile(t, root, "adrs/ADR-001-test.md", strings.ReplaceAll(validLintADRMarkdown(), "REQ-TEST-001", "REQ-MARKDOWN"))

	result := LintEffortFolder(root)
	if !lintSummaryHasFinding(result, "adr_index_frontmatter_mismatch", "adrs/index.json") {
		t.Fatalf("expected adr_index_frontmatter_mismatch finding, got %#v", result.Findings)
	}
}

func TestLintEffortFolderFlagsUngroundedImplementationAffectingClaims(t *testing.T) {
	root := t.TempDir()
	writeLintFixtureFile(t, root, "adrs/index.json", `{"schema_version":"v1","effort_id":"test-effort","adrs":[{"adr_id":"ADR-001","title":"Test ADR","status":"accepted","path":"ADR-001-test.md","coverage_tier":"unit","prd_refs":["REQ-TEST-001"]}]}`)
	writeLintFixtureFile(t, root, "adrs/ADR-001-test.md", strings.Replace(validLintADRMarkdown(), "Test decision.", "Use the ExampleCloud platform for implementation.", 1))

	result := LintEffortFolder(root)
	if !lintSummaryHasFinding(result, "ungrounded_implementation_claim", "adrs/ADR-001-test.md") {
		t.Fatalf("expected ungrounded_implementation_claim finding, got %#v", result.Findings)
	}
}

func lintSummaryHasFinding(result LintSummaryResult, code, path string) bool {
	for _, finding := range result.Findings {
		if finding.Code == code && finding.Path == path {
			return true
		}
	}
	return false
}

func writeLintFixtureFile(t *testing.T, root, relPath, body string) {
	t.Helper()
	fullPath := filepath.Join(root, relPath)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", relPath, err)
	}
	if err := os.WriteFile(fullPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", relPath, err)
	}
}

func validLintADRMarkdown() string {
	return `---
schema_version: idiom.adr.v1
adr_id: ADR-001
status: accepted
accepted_at: 2026-05-17T00:00:00Z
accepted_by: test-reviewer
acceptance_profile: human-review
prd_refs:
  - REQ-TEST-001
evidence_refs:
  - EV-001
tdd_obligations:
  - TDD-001
coverage_tier: unit
---

# ADR-001: Test

## Context

Test context.

## Decision

Test decision.

## Alternatives Considered

None.

## Evidence

- EV-001: Test evidence.

## Consequences

Test consequences.

## Compatibility and Migration

Compatible.

## TDD Obligations

- TDD-001: Test obligation.

## Rollback

Revert.

## Open Questions

None.
`
}
