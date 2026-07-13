package productworkflow

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("lint", func() {
	ginkgo.It("LintEffortFolderRejectsSymlinkedRequiredFile", func() {
		root := ginkgo.GinkgoT().TempDir()
		outside := filepath.Join(ginkgo.GinkgoT().TempDir(), "workflow_state.json")
		{

			err := os.WriteFile(outside, []byte("{}\n"), 0o644)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprint(err))
		}
		{

			err := os.Symlink(outside, filepath.Join(root, "workflow_state.json"))
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprint(err))
		}

		result := LintEffortFolder(root)
		gomega.Expect(result.Findings).To(gomega.ContainElement(gomega.SatisfyAll(
			gomega.HaveField("Code", "read_error"),
			gomega.HaveField("Path", "workflow_state.json"),
			gomega.HaveField("Message", gomega.ContainSubstring("must not be a symlink")),
		)), fmt.Sprintf("expected a symlink read rejection for workflow_state.json, got %#v", result.Findings))

	})
	ginkgo.It("LintEffortFolderRejectsSymlinkedReportFile", func() {
		root := ginkgo.GinkgoT().TempDir()
		reportsDir := filepath.Join(root, "reports")
		{

			err := os.MkdirAll(reportsDir, 0o755)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprint(err))
		}

		outside := filepath.Join(ginkgo.GinkgoT().TempDir(), "review.report.json")
		{
			err := os.WriteFile(outside, []byte("{}\n"), 0o644)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprint(err))
		}
		{

			err := os.Symlink(outside, filepath.Join(reportsDir, "review.report.json"))
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprint(err))
		}

		result := LintEffortFolder(root)
		gomega.Expect(!lintSummaryHasFinding(result, "symlink_file", filepath.Join("reports", "review.report.json"))).To(gomega.BeFalse(), fmt.Sprintf("expected symlink_file finding for report, got %#v", result.Findings))

	})
	ginkgo.It("LintEffortFolderReportTypeParsing", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeLintFixtureFile(root, "reports/bad-review.json", `{"schema_version":"idiom.review_report.v1.fixture","effort_id":"effort","task_id":"task","reviewer":{"agent_id":"rev","model":"m","reasoning_effort":"medium","independent_from_implementor":true},"status":"surprise","findings":[],"checks":{},"reviewed_at":"","commands_reviewed":[],"created_at":"2026-05-17T00:00:00Z"}`)
		writeLintFixtureFile(root, "reports/unknown-invalid.json", `{`)
		writeLintFixtureFile(root, "reports/unknown-valid.json", `{"notes":"free-form report"}`)

		result := LintEffortFolder(root)
		gomega.Expect(!lintSummaryHasFinding(result, "parse_error", filepath.Join("reports", "bad-review.json"))).To(gomega.BeFalse(), fmt.Sprintf("expected known review report to use typed parser, got %#v", result.Findings))
		gomega.Expect(!lintSummaryHasFinding(result, "parse_error", filepath.Join("reports", "unknown-invalid.json"))).To(gomega.BeFalse(), fmt.Sprintf("expected unknown invalid JSON report to fail JSON parsing, got %#v", result.Findings))
		gomega.Expect(lintSummaryHasFinding(result, "parse_error", filepath.Join("reports", "unknown-valid.json"))).To(gomega.BeFalse(), fmt.Sprintf("expected unknown valid JSON report to avoid typed parsing, got %#v", result.Findings))

	})
	ginkgo.It("LintEffortFolderDetectsADRIndexFrontMatterDivergence", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeLintFixtureFile(root, "adrs/index.json", `{"schema_version":"v1","effort_id":"test-effort","adrs":[{"adr_id":"ADR-001","title":"Test ADR","status":"accepted","path":"ADR-001-test.md","coverage_tier":"unit","prd_refs":["REQ-INDEX"]}]}`)
		writeLintFixtureFile(root, "adrs/ADR-001-test.md", strings.ReplaceAll(validLintADRMarkdown(), "REQ-TEST-001", "REQ-MARKDOWN"))

		result := LintEffortFolder(root)
		gomega.Expect(!lintSummaryHasFinding(result, "adr_index_frontmatter_mismatch", "adrs/index.json")).To(gomega.BeFalse(), fmt.Sprintf("expected adr_index_frontmatter_mismatch finding, got %#v", result.Findings))

	})
	ginkgo.It("LintEffortFolderFlagsUngroundedImplementationAffectingClaims", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeLintFixtureFile(root, "adrs/index.json", `{"schema_version":"v1","effort_id":"test-effort","adrs":[{"adr_id":"ADR-001","title":"Test ADR","status":"accepted","path":"ADR-001-test.md","coverage_tier":"unit","prd_refs":["REQ-TEST-001"]}]}`)
		writeLintFixtureFile(root, "adrs/ADR-001-test.md", strings.Replace(validLintADRMarkdown(), "Test decision.", "Use the ExampleCloud platform for implementation.", 1))

		result := LintEffortFolder(root)
		gomega.Expect(!lintSummaryHasFinding(result, "ungrounded_implementation_claim", "adrs/ADR-001-test.md")).To(gomega.BeFalse(), fmt.Sprintf("expected ungrounded_implementation_claim finding, got %#v", result.Findings))

	})
})

func lintSummaryHasFinding(result LintSummaryResult, code, path string) bool {
	for _, finding := range result.Findings {
		if finding.Code == code && finding.Path == path {
			return true
		}
	}

	return false
}

func writeLintFixtureFile(root, relPath, body string) {
	ginkgo.GinkgoHelper()

	fullPath := filepath.Join(root, relPath)
	{
		err := os.MkdirAll(filepath.Dir(fullPath), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir %s: %v", relPath, err))
	}
	{

		err := os.WriteFile(fullPath, []byte(body), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write %s: %v", relPath, err))
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
