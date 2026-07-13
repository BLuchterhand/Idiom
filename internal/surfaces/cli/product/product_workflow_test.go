package product

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"idiom/internal/workflows/productworkflow"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.It("product lint passes on current valid effort fixture", func() {
	t := ginkgo.GinkgoT()
	effortRoot := writeValidTestEffort(t)

	var stdout, stderr bytes.Buffer

	code := runProductLint([]string{"--effort-root", effortRoot, "--json"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(0), "expected lint to pass on current valid effort fixture, got code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	var result productworkflow.LintSummaryResult

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	gomega.Expect(result.Status).To(gomega.BeEquivalentTo(productworkflow.ProductWorkflowStatusPass), "expected status pass, got %q: %#v", result.Status, result)

	gomega.Expect(result.EffortRoot).NotTo(gomega.BeEquivalentTo(""), "effort_root should be set")

	gomega.Expect(len(result.CheckedFiles)).NotTo(gomega.BeEquivalentTo(0), "should have checked at least one file")

	gomega.Expect(len(result.Findings)).To(gomega.BeEquivalentTo(0), "expected zero findings, got %d: %#v", len(result.Findings), result.Findings)
})

var _ = ginkgo.It("product lint fails on missing required file", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()

	var stdout, stderr bytes.Buffer

	code := runProductLint([]string{"--effort-root", dir, "--json"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected lint to fail on empty dir, got code=%d", code)

	var result productworkflow.LintSummaryResult

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	gomega.Expect(result.Status).To(gomega.BeEquivalentTo(productworkflow.ProductWorkflowStatusFail), "expected status fail, got %q", result.Status)

	found := false

	for _, f := range result.Findings {
		if f.Code == "missing_file" && f.Path == "workflow_state.json" && strings.Contains(f.Message, "not found") {
			found = true
			break
		}
	}

	gomega.Expect(found).To(gomega.BeTrue(), "expected missing_file finding for workflow_state.json, got: %#v", result.Findings)
})

var _ = ginkgo.It("product lint fails on invalid implementation ledger closeout", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()
	writeTestFile(t, dir, "workflow_state.json", validTestWorkflowStateJSON())
	writeTestFile(t, dir, "tdd.json", validTestTDDJSON())
	writeTestFile(t, dir, "evidence/evidence.jsonl", validTestEvidenceJSONL())
	writeTestFile(t, dir, "evidence/traceability_matrix.json", validTestTraceabilityMatrixJSON())
	writeTestFile(t, dir, "implementation_ledger.jsonl", invalidCloseoutLedgerJSONL())
	writeTestFile(t, dir, "reports/test.json", "{}")

	var stdout, stderr bytes.Buffer

	code := runProductLint([]string{"--effort-root", dir, "--json"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected lint to fail on invalid closeout, got code=%d", code)

	var result productworkflow.LintSummaryResult

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	gomega.Expect(result.Status).To(gomega.BeEquivalentTo(productworkflow.ProductWorkflowStatusFail), "expected status fail, got %q", result.Status)

	found := false

	for _, f := range result.Findings {
		if f.Code == "parse_error" && f.Path == "implementation_ledger.jsonl" && strings.Contains(f.Message, "atom_closed") {
			found = true
			break
		}
	}

	gomega.Expect(found).To(gomega.BeTrue(), "expected parse_error finding mentioning atom_closed, got: %#v", result.Findings)
})

var _ = ginkgo.It("product lint includes prd and adr in checked files", func() {
	t := ginkgo.GinkgoT()
	effortRoot := writeValidTestEffort(t)

	var stdout, stderr bytes.Buffer

	code := runProductLint([]string{"--effort-root", effortRoot, "--json"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(0), "expected lint to pass, got code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	var result productworkflow.LintSummaryResult

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	wantFiles := []string{"prd.md", "adrs/index.json", "adrs/ADR-001-test.md"}
	for _, want := range wantFiles {
		found := false

		for _, f := range result.CheckedFiles {
			if f == want {
				found = true
				break
			}
		}

		gomega.Expect(found).To(gomega.BeTrue(), "expected checked_files to include %q, got %v", want, result.CheckedFiles)
	}
})

var _ = ginkgo.It("product lint usage errors", func() {
	var stdout, stderr bytes.Buffer

	code := runProductLint([]string{}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(2), "expected code 2 for missing --effort-root, got %d", code)
})

var _ = ginkgo.It("product lint no subcommand usage error", func() {
	var stdout, stderr bytes.Buffer

	code := RunMainWithTelemetry([]string{}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(2), "expected code 2 for no subcommand, got %d", code)
})

var _ = ginkgo.It("product lint help flags", func() {
	for _, args := range [][]string{{"-h"}, {"--help"}, {"help"}} {
		var stdout, stderr bytes.Buffer

		code := RunMainWithTelemetry(args, &stdout, &stderr, nil)

		gomega.Expect(code).To(gomega.BeEquivalentTo(0), "expected code 0 for help args %v, got %d", args, code)
	}
})

var _ = ginkgo.It("product lint checks optional research profile when configured", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()
	wsWithProfile := strings.ReplaceAll(validTestWorkflowStateJSON(), `"artifact_paths":{"events":"events.jsonl"}`, `"artifact_paths":{"events":"events.jsonl","research_profile":"research_profile.json"}`)
	writeTestFile(t, dir, "workflow_state.json", wsWithProfile)
	writeTestFile(t, dir, "tdd.json", validTestTDDJSON())
	writeTestFile(t, dir, "evidence/evidence.jsonl", validTestEvidenceJSONL())
	writeTestFile(t, dir, "evidence/traceability_matrix.json", validTestTraceabilityMatrixJSON())
	writeTestFile(t, dir, "implementation_ledger.jsonl", validTestImplementationLedgerJSONL())
	writeTestFile(t, dir, "prd.md", validTestPRDMarkdown())
	writeTestFile(t, dir, "adrs/index.json", validTestADRIndexJSON())
	writeTestFile(t, dir, "adrs/ADR-001-test.md", validTestADRMarkdown())
	writeTestFile(t, dir, "reports/test.json", "{}")
	writeTestFile(t, dir, "briefs/test.md", "# Test Brief")
	writeTestFile(t, dir, "research_profile.json", validTestResearchProfileJSON())

	var stdout, stderr bytes.Buffer

	code := runProductLint([]string{"--effort-root", dir, "--json"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(0), "expected lint to pass with valid research profile, got code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	var result productworkflow.LintSummaryResult

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	gomega.Expect(result.Status).To(gomega.BeEquivalentTo(productworkflow.ProductWorkflowStatusPass), "expected status pass, got %q: %#v", result.Status, result)

	found := false

	for _, f := range result.CheckedFiles {
		if f == "research_profile.json" {
			found = true
			break
		}
	}

	gomega.Expect(found).To(gomega.BeTrue(), "expected research_profile.json in checked_files, got %v", result.CheckedFiles)
})

var _ = ginkgo.It("product lint rejects invalid research profile", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()
	wsWithProfile := strings.ReplaceAll(validTestWorkflowStateJSON(), `"artifact_paths":{"events":"events.jsonl"}`, `"artifact_paths":{"events":"events.jsonl","research_profile":"research_profile.json"}`)
	writeTestFile(t, dir, "workflow_state.json", wsWithProfile)
	writeTestFile(t, dir, "tdd.json", validTestTDDJSON())
	writeTestFile(t, dir, "evidence/evidence.jsonl", validTestEvidenceJSONL())
	writeTestFile(t, dir, "evidence/traceability_matrix.json", validTestTraceabilityMatrixJSON())
	writeTestFile(t, dir, "implementation_ledger.jsonl", validTestImplementationLedgerJSONL())
	writeTestFile(t, dir, "prd.md", validTestPRDMarkdown())
	writeTestFile(t, dir, "adrs/index.json", validTestADRIndexJSON())
	writeTestFile(t, dir, "adrs/ADR-001-test.md", validTestADRMarkdown())
	writeTestFile(t, dir, "reports/test.json", "{}")
	writeTestFile(t, dir, "research_profile.json", `{"schema_version":"idiom.research_profile.v1","researchers":[],"merge_policy":{}}`)

	var stdout, stderr bytes.Buffer

	code := runProductLint([]string{"--effort-root", dir, "--json"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected lint to fail with invalid research profile, got code=%d", code)

	var result productworkflow.LintSummaryResult

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	found := false

	for _, f := range result.Findings {
		if f.Path == "research_profile.json" && f.Code == "parse_error" {
			found = true
			break
		}
	}

	gomega.Expect(found).To(gomega.BeTrue(), "expected parse_error finding for research_profile.json, got: %#v", result.Findings)
})

var _ = ginkgo.It("product lint reports a parse error for invalid TDD JSON", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()
	writeTestFile(t, dir, "workflow_state.json", validTestWorkflowStateJSON())
	writeTestFile(t, dir, "evidence/evidence.jsonl", validTestEvidenceJSONL())
	writeTestFile(t, dir, "evidence/traceability_matrix.json", validTestTraceabilityMatrixJSON())
	writeTestFile(t, dir, "implementation_ledger.jsonl", validTestImplementationLedgerJSONL())
	writeTestFile(t, dir, "reports/test.json", "{}")
	writeTestFile(t, dir, "tdd.json", `{not valid json`)

	var stdout, stderr bytes.Buffer

	code := runProductLint([]string{"--effort-root", dir, "--json"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected lint to fail on invalid tdd.json, got code=%d", code)

	var result productworkflow.LintSummaryResult

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	found := false

	for _, f := range result.Findings {
		if f.Code == "parse_error" && f.Path == "tdd.json" {
			found = true
			break
		}
	}

	gomega.Expect(found).To(gomega.BeTrue(), "expected parse_error finding for tdd.json, got: %#v", result.Findings)
})

var _ = ginkgo.It("product lint fails on invalid tdd coverage tier", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()
	writeTestFile(t, dir, "workflow_state.json", validTestWorkflowStateJSON())
	writeTestFile(t, dir, "evidence/evidence.jsonl", validTestEvidenceJSONL())
	writeTestFile(t, dir, "evidence/traceability_matrix.json", validTestTraceabilityMatrixJSON())
	writeTestFile(t, dir, "implementation_ledger.jsonl", validTestImplementationLedgerJSONL())
	writeTestFile(t, dir, "reports/test.json", "{}")
	writeTestFile(t, dir, "tdd.json", `{
		"schema_version":"idiom.tdd_obligations.v2",
		"effort_id":"test",
		"task_id":"test",
		"coverage_tier":"bogus_tier",
		"coverage":{"mutation_min_score":80},
		"obligations":[{"id":"TDD-1","type":"acceptance","behavior":"b","owner":"o","command":"c","required_before":"close","status":"pending"}]
	}`)

	var stdout, stderr bytes.Buffer

	code := runProductLint([]string{"--effort-root", dir, "--json"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected lint to fail on invalid coverage_tier, got code=%d", code)

	var result productworkflow.LintSummaryResult

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	found := false

	for _, f := range result.Findings {
		if f.Code == "parse_error" && f.Path == "tdd.json" && strings.Contains(f.Message, "invalid coverage_tier") {
			found = true
			break
		}
	}

	gomega.Expect(found).To(gomega.BeTrue(), "expected parse_error for invalid coverage_tier, got: %#v", result.Findings)
})

var _ = ginkgo.It("product lint passes with valid tdd coverage and evidence", func() {
	t := ginkgo.GinkgoT()
	dir := writeValidTestEffort(t)

	var stdout, stderr bytes.Buffer

	code := runProductLint([]string{"--effort-root", dir, "--json"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(0), "expected lint to pass with valid TDD coverage and evidence, got code=%d\n%s", code, stdout.String())

	var result productworkflow.LintSummaryResult

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	gomega.Expect(result.Status).To(gomega.BeEquivalentTo(productworkflow.ProductWorkflowStatusPass), "expected pass status, got %q: %#v", result.Status, result.Findings)
})

var _ = ginkgo.It("product lint detects broken traceability links", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()
	writeTestFile(t, dir, "workflow_state.json", validTestWorkflowStateJSON())
	writeTestFile(t, dir, "tdd.json", validTestTDDJSON())
	writeTestFile(t, dir, "evidence/evidence.jsonl", validTestEvidenceJSONL())
	writeTestFile(t, dir, "evidence/traceability_matrix.json", `{
		"schema_version":"idiom.traceability_matrix.v1.fixture",
		"effort_id":"test-effort",
		"task_id":"impl-test",
		"created_at":"2026-05-17T16:00:00Z",
		"matrix":[{
			"evidence_card":"EV-MISSING","prd_requirement":"REQ-MISSING","adr":"ADR-MISSING","tdd_obligation":"TDD-MISSING","brief":"briefs/missing.md","report":"reports/missing.json"
		}]
	}`)
	writeTestFile(t, dir, "implementation_ledger.jsonl", validTestImplementationLedgerJSONL())
	writeTestFile(t, dir, "prd.md", validTestPRDMarkdown())
	writeTestFile(t, dir, "adrs/index.json", validTestADRIndexJSON())
	writeTestFile(t, dir, "adrs/ADR-001-test.md", validTestADRMarkdown())
	writeTestFile(t, dir, "reports/test.json", "{}")

	var stdout, stderr bytes.Buffer

	code := runProductLint([]string{"--effort-root", dir, "--json"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected lint to fail with broken links, got code=%d", code)

	var result productworkflow.LintSummaryResult

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	wantFields := []string{"evidence_card", "prd_requirement", "adr", "tdd_obligation", "brief", "report"}
	for _, field := range wantFields {
		found := false

		for _, f := range result.Findings {
			if f.Code == "broken_link" && f.Path == "evidence/traceability_matrix.json" && strings.Contains(f.Message, field) {
				found = true
				break
			}
		}

		gomega.Expect(found).To(gomega.BeTrue(), "expected broken_link finding for field %q, got: %#v", field, result.Findings)
	}
})

var _ = ginkgo.It("product lint accepts historical TDD obligation IDs", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()
	writeTestFile(t, dir, "workflow_state.json", validTestWorkflowStateJSON())
	writeTestFile(t, dir, "tdd.json", validTestTDDJSON())
	writeTestFile(t, dir, "evidence/evidence.jsonl", validTestEvidenceJSONL())
	writeTestFile(t, dir, "evidence/traceability_matrix.json", `{
		"schema_version":"idiom.traceability_matrix.v1.fixture",
		"effort_id":"test-effort",
		"task_id":"impl-test",
		"created_at":"2026-05-17T16:00:00Z",
		"matrix":[{
			"evidence_card":"EV-001","prd_requirement":"REQ-TEST-001","adr":"ADR-001","tdd_obligation":"TDD-HIST-001","brief":"briefs/test.md","report":"reports/test.json"
		}]
	}`)
	writeTestFile(t, dir, "implementation_ledger.jsonl", strings.Join([]string{
		`{"record_type":"atom_planned","task_id":"impl-test","effort_id":"test-effort","timestamp":"2026-05-17T16:00:00Z","complexity_tier":"C1","context_pack_id":"pack","implementor":{"id":"impl","runtime":"opencode","model":"test","command":"run"},"reviewer":{"id":"rev","model":"test","reasoning_effort":"medium","runtime":"codex"},"scope":{"expected_files":["test.go"],"forbidden_patterns":["cmd/**"]},"required_verification":["go test"],"report_paths":{"implementation":"reports/impl.json","review":"reports/rev.json","investigation":"reports/inv.json"}}`,
		`{"record_type":"implementation_completed","task_id":"impl-test","effort_id":"test-effort","timestamp":"2026-05-17T16:01:00Z","implementor_id":"impl","commit_hash":"0000000000000000000000000000000000000000"}`,
		`{"record_type":"verification_completed","task_id":"impl-test","effort_id":"test-effort","timestamp":"2026-05-17T16:02:00Z","tdd_obligations_satisfied":["TDD-HIST-001"]}`,
		`{"record_type":"review_completed","task_id":"impl-test","effort_id":"test-effort","timestamp":"2026-05-17T16:03:00Z","reviewer_id":"rev","model":"test","reasoning_effort":"medium"}`,
		`{"record_type":"post_effort_investigation_completed","task_id":"impl-test","effort_id":"test-effort","timestamp":"2026-05-17T16:04:00Z"}`,
		`{"record_type":"atom_closed","task_id":"impl-test","effort_id":"test-effort","timestamp":"2026-05-17T16:05:00Z","open_blockers":[]}`,
	}, "\n"))
	writeTestFile(t, dir, "prd.md", validTestPRDMarkdown())
	writeTestFile(t, dir, "adrs/index.json", validTestADRIndexJSON())
	writeTestFile(t, dir, "adrs/ADR-001-test.md", validTestADRMarkdown())
	writeTestFile(t, dir, "reports/test.json", "{}")
	writeTestFile(t, dir, "briefs/test.md", "# Test Brief")

	var stdout, stderr bytes.Buffer

	code := runProductLint([]string{"--effort-root", dir, "--json"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(0), "expected lint to pass with historical TDD ID, got code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
})

var _ = ginkgo.It("product lint traceability matrix in checked files", func() {
	t := ginkgo.GinkgoT()
	effortRoot := writeValidTestEffort(t)

	var stdout, stderr bytes.Buffer

	code := runProductLint([]string{"--effort-root", effortRoot, "--json"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(0), "expected lint to pass, got code=%d", code)

	var result productworkflow.LintSummaryResult

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	found := false

	for _, f := range result.CheckedFiles {
		if f == "evidence/traceability_matrix.json" {
			found = true
			break
		}
	}

	gomega.Expect(found).To(gomega.BeTrue(), "expected evidence/traceability_matrix.json in checked_files, got %v", result.CheckedFiles)
})

var _ = ginkgo.It("product lint rejects invalid prd artifact status", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()
	writeTestFile(t, dir, "workflow_state.json", validTestWorkflowStateJSON())
	writeTestFile(t, dir, "tdd.json", validTestTDDJSON())
	writeTestFile(t, dir, "evidence/evidence.jsonl", validTestEvidenceJSONL())
	writeTestFile(t, dir, "evidence/traceability_matrix.json", validTestTraceabilityMatrixJSON())
	writeTestFile(t, dir, "implementation_ledger.jsonl", validTestImplementationLedgerJSONL())
	writeTestFile(t, dir, "prd.md", `---
schema_version: idiom.prd.v1
prd_id: prd-test
status: accepted_fixture
task_id: impl-test
created_at: 2026-05-17T00:00:00Z
---

# PRD: Test

## Requirements

### REQ-TEST-001

- **id**: REQ-TEST-001
- **priority**: must
- **statement**: Test requirement
- **evidence_refs**: ["EV-001"]
- **tdd_obligations**: ["TDD-001"]
`)
	writeTestFile(t, dir, "adrs/index.json", validTestADRIndexJSON())
	writeTestFile(t, dir, "adrs/ADR-001-test.md", validTestADRMarkdown())
	writeTestFile(t, dir, "reports/test.json", "{}")

	var stdout, stderr bytes.Buffer

	code := runProductLint([]string{"--effort-root", dir, "--json"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected lint to fail with invalid PRD status, got code=%d", code)

	var result productworkflow.LintSummaryResult

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	found := false

	for _, f := range result.Findings {
		if f.Code == "parse_error" && f.Path == "prd.md" && strings.Contains(f.Message, "accepted_fixture") {
			found = true
			break
		}
	}

	gomega.Expect(found).To(gomega.BeTrue(), "expected parse_error finding for prd.md mentioning accepted_fixture, got: %#v", result.Findings)
})

var _ = ginkgo.It("product lint rejects invalid adr index artifact status", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()
	writeTestFile(t, dir, "workflow_state.json", validTestWorkflowStateJSON())
	writeTestFile(t, dir, "tdd.json", validTestTDDJSON())
	writeTestFile(t, dir, "evidence/evidence.jsonl", validTestEvidenceJSONL())
	writeTestFile(t, dir, "evidence/traceability_matrix.json", validTestTraceabilityMatrixJSON())
	writeTestFile(t, dir, "implementation_ledger.jsonl", validTestImplementationLedgerJSONL())
	writeTestFile(t, dir, "prd.md", validTestPRDMarkdown())
	writeTestFile(t, dir, "adrs/index.json", `{"schema_version":"v1","effort_id":"test-effort","adrs":[{"adr_id":"ADR-001","title":"Test ADR","status":"accepted_fixture","path":"ADR-001-test.md","coverage_tier":"unit","prd_refs":["REQ-TEST-001"],"created_at":"2026-05-17T00:00:00Z"}]}`)
	writeTestFile(t, dir, "adrs/ADR-001-test.md", validTestADRMarkdown())
	writeTestFile(t, dir, "reports/test.json", "{}")

	var stdout, stderr bytes.Buffer

	code := runProductLint([]string{"--effort-root", dir, "--json"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected lint to fail with invalid ADR index status, got code=%d", code)

	var result productworkflow.LintSummaryResult

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	found := false

	for _, f := range result.Findings {
		if f.Code == "parse_error" && f.Path == "adrs/index.json" && strings.Contains(f.Message, "accepted_fixture") {
			found = true
			break
		}
	}

	gomega.Expect(found).To(gomega.BeTrue(), "expected parse_error finding for adrs/index.json mentioning accepted_fixture, got: %#v", result.Findings)
})

var _ = ginkgo.It("product lint rejects invalid adr markdown artifact status", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()
	writeTestFile(t, dir, "workflow_state.json", validTestWorkflowStateJSON())
	writeTestFile(t, dir, "tdd.json", validTestTDDJSON())
	writeTestFile(t, dir, "evidence/evidence.jsonl", validTestEvidenceJSONL())
	writeTestFile(t, dir, "evidence/traceability_matrix.json", validTestTraceabilityMatrixJSON())
	writeTestFile(t, dir, "implementation_ledger.jsonl", validTestImplementationLedgerJSONL())
	writeTestFile(t, dir, "prd.md", validTestPRDMarkdown())
	writeTestFile(t, dir, "adrs/index.json", validTestADRIndexJSON())
	writeTestFile(t, dir, "adrs/ADR-001-test.md", `---
schema_version: idiom.adr.v1
adr_id: ADR-001
status: accepted_fixture
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

Test.

## Decision

Test decision.

## Alternatives Considered

None.

## Evidence

- EV-001: Test.

## Consequences

None.

## Compatibility and Migration

Compatible.

## TDD Obligations

- TDD-001: Test.

## Rollback

Revert.

## Open Questions

None.
`)
	writeTestFile(t, dir, "reports/test.json", "{}")

	var stdout, stderr bytes.Buffer

	code := runProductLint([]string{"--effort-root", dir, "--json"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected lint to fail with invalid ADR markdown status, got code=%d", code)

	var result productworkflow.LintSummaryResult

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	found := false

	for _, f := range result.Findings {
		if f.Code == "parse_error" && f.Path == "adrs/ADR-001-test.md" && strings.Contains(f.Message, "accepted_fixture") {
			found = true
			break
		}
	}

	gomega.Expect(found).To(gomega.BeTrue(), "expected parse_error finding for ADR markdown mentioning accepted_fixture, got: %#v", result.Findings)
})

var _ = ginkgo.It("product lint rejects malformed implementation report", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()
	writeTestFile(t, dir, "workflow_state.json", validTestWorkflowStateJSON())
	writeTestFile(t, dir, "tdd.json", validTestTDDJSON())
	writeTestFile(t, dir, "evidence/evidence.jsonl", validTestEvidenceJSONL())
	writeTestFile(t, dir, "evidence/traceability_matrix.json", validTestTraceabilityMatrixJSON())
	writeTestFile(t, dir, "implementation_ledger.jsonl", validTestImplementationLedgerJSONL())
	writeTestFile(t, dir, "reports/impl-bad-implementation.json", `{"schema_version":""}`)

	var stdout, stderr bytes.Buffer

	code := runProductLint([]string{"--effort-root", dir, "--json"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected lint to fail on malformed implementation report, got code=%d", code)

	var result productworkflow.LintSummaryResult

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	found := false

	for _, f := range result.Findings {
		if f.Code == "parse_error" && f.Path == "reports/impl-bad-implementation.json" && strings.Contains(f.Message, "missing task_id") {
			found = true
			break
		}
	}

	gomega.Expect(found).To(gomega.BeTrue(), "expected parse_error finding for malformed implementation report, got: %#v", result.Findings)
})

var _ = ginkgo.It("product lint rejects malformed review report", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()
	writeTestFile(t, dir, "workflow_state.json", validTestWorkflowStateJSON())
	writeTestFile(t, dir, "tdd.json", validTestTDDJSON())
	writeTestFile(t, dir, "evidence/evidence.jsonl", validTestEvidenceJSONL())
	writeTestFile(t, dir, "evidence/traceability_matrix.json", validTestTraceabilityMatrixJSON())
	writeTestFile(t, dir, "implementation_ledger.jsonl", validTestImplementationLedgerJSONL())
	writeTestFile(t, dir, "reports/impl-bad-review.json", `{"schema_version":"v1","reviewer":{"independent_from_implementor":false}}`)

	var stdout, stderr bytes.Buffer

	code := runProductLint([]string{"--effort-root", dir, "--json"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected lint to fail on malformed review report, got code=%d", code)

	var result productworkflow.LintSummaryResult

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	found := false

	for _, f := range result.Findings {
		if f.Code == "parse_error" && f.Path == "reports/impl-bad-review.json" && strings.Contains(f.Message, "independent_from_implementor must be true") {
			found = true
			break
		}
	}

	gomega.Expect(found).To(gomega.BeTrue(), "expected parse_error for review report independence, got: %#v", result.Findings)
})

var _ = ginkgo.It("product lint rejects malformed investigation report", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()
	writeTestFile(t, dir, "workflow_state.json", validTestWorkflowStateJSON())
	writeTestFile(t, dir, "tdd.json", validTestTDDJSON())
	writeTestFile(t, dir, "evidence/evidence.jsonl", validTestEvidenceJSONL())
	writeTestFile(t, dir, "evidence/traceability_matrix.json", validTestTraceabilityMatrixJSON())
	writeTestFile(t, dir, "implementation_ledger.jsonl", validTestImplementationLedgerJSONL())
	writeTestFile(t, dir, "reports/impl-bad-investigation.json", `{"schema_version":"v1","status":"complete","blockers":["still_broken"]}`)

	var stdout, stderr bytes.Buffer

	code := runProductLint([]string{"--effort-root", dir, "--json"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected lint to fail on malformed investigation report, got code=%d", code)

	var result productworkflow.LintSummaryResult

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	found := false

	for _, f := range result.Findings {
		if f.Code == "parse_error" && f.Path == "reports/impl-bad-investigation.json" && strings.Contains(f.Message, "blockers is non-empty") {
			found = true
			break
		}
	}

	gomega.Expect(found).To(gomega.BeTrue(), "expected parse_error for investigation with blockers, got: %#v", result.Findings)
})

var _ = ginkgo.It("product lint accepts unknown report json", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()
	writeTestFile(t, dir, "workflow_state.json", validTestWorkflowStateJSON())
	writeTestFile(t, dir, "tdd.json", validTestTDDJSON())
	writeTestFile(t, dir, "evidence/evidence.jsonl", validTestEvidenceJSONL())
	writeTestFile(t, dir, "evidence/traceability_matrix.json", validTestTraceabilityMatrixJSON())
	writeTestFile(t, dir, "implementation_ledger.jsonl", validTestImplementationLedgerJSONL())
	writeTestFile(t, dir, "prd.md", validTestPRDMarkdown())
	writeTestFile(t, dir, "adrs/index.json", validTestADRIndexJSON())
	writeTestFile(t, dir, "adrs/ADR-001-test.md", validTestADRMarkdown())
	writeTestFile(t, dir, "briefs/test.md", "# Test Brief")
	writeTestFile(t, dir, "reports/test.json", `{"kind":"traceability_report"}`)
	writeTestFile(t, dir, "reports/other.json", `{"kind":"unknown","valid":true}`)

	var stdout, stderr bytes.Buffer

	code := runProductLint([]string{"--effort-root", dir, "--json"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(0), "expected lint to pass with unknown report JSON, got code=%d stdout=%s", code, stdout.String())
})

var _ = ginkgo.It("product lint passes with valid report fixtures", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()
	writeTestFile(t, dir, "workflow_state.json", validTestWorkflowStateJSON())
	writeTestFile(t, dir, "tdd.json", validTestTDDJSON())
	writeTestFile(t, dir, "evidence/evidence.jsonl", validTestEvidenceJSONL())
	writeTestFile(t, dir, "evidence/traceability_matrix.json", validTestTraceabilityMatrixJSON())
	writeTestFile(t, dir, "implementation_ledger.jsonl", validTestImplementationLedgerJSONL())
	writeTestFile(t, dir, "prd.md", validTestPRDMarkdown())
	writeTestFile(t, dir, "adrs/index.json", validTestADRIndexJSON())
	writeTestFile(t, dir, "adrs/ADR-001-test.md", validTestADRMarkdown())
	writeTestFile(t, dir, "briefs/test.md", "# Test Brief")
	writeTestFile(t, dir, "reports/test.json", "{}")
	writeTestFile(t, dir, "reports/test-implementation.json", validTestImplementationReportJSON())
	writeTestFile(t, dir, "reports/test-review.json", validTestReviewReportJSON())
	writeTestFile(t, dir, "reports/test-investigation.json", validTestTestInvestigationReportJSON())

	var stdout, stderr bytes.Buffer

	code := runProductLint([]string{"--effort-root", dir, "--json"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(0), "expected lint to pass with valid reports, got code=%d stdout=%s", code, stdout.String())
})

var _ = ginkgo.It("product obsidian parity durable fixture passes", func() {
	t := ginkgo.GinkgoT()
	effortRoot := writeValidTestEffort(t)
	writeTestFile(t, effortRoot, ".git/HEAD", "ref: refs/heads/main\n")
	writeTestFile(t, effortRoot, productworkflow.ObsidianProductWorkflowPath, `---
	status: current-with-open-work
	---
	> **Status: PARTIALLY IMPLEMENTED** As of 2026-05-18
	- [x] **Phase 0** complete
- [x] **Phase 1** complete
- [x] **Phase 2** complete
- [x] **Phase 3** complete
	- [x] **Phase 4** complete
	- [ ] **Phase 5** active
	`)
	writeTestFile(t, effortRoot, productworkflow.ObsidianRecoveryPath, `---
	status: current-with-open-work
	---
	# Recovery and Decomposition
	`)
	writeTestFile(t, effortRoot, "workflow_state.json", strings.ReplaceAll(strings.ReplaceAll(validTestWorkflowStateJSON(),
		`"effort_id":"test-effort"`, `"effort_id":"prd-adr-brief-workflow"`),
		`"task_id":"impl-test"`, `"task_id":"impl-0032-phase-5-obsidian-parity-check"`))
	writeTestFile(t, effortRoot, "implementation_ledger.jsonl",
		`{"record_type":"atom_planned","task_id":"impl-0032-phase-5-obsidian-parity-check","effort_id":"prd-adr-brief-workflow","timestamp":"2026-05-18T16:05:00Z","complexity_tier":"C2","context_pack_id":"pack","implementor":{"id":"impl","runtime":"codex","model":"test","command":"run"},"reviewer":{"id":"rev","model":"test","reasoning_effort":"medium","runtime":"codex"},"scope":{"expected_files":["cmd/idiom/product_workflow.go"],"forbidden":["internal/forbidden.go"],"forbidden_patterns":["docs/private/**"]},"required_verification":["go test"],"report_paths":{"implementation":"reports/impl.json","review":"reports/rev.json","investigation":"reports/inv.json"}}`+"\n"+
			`{"record_type":"verification_completed","task_id":"impl-test","effort_id":"test-effort","timestamp":"2026-05-18T16:06:00Z","tdd_obligations_satisfied":["TDD-001"]}`)

	var stdout, stderr bytes.Buffer

	code := runProductLint([]string{"--effort-root", effortRoot, "--json"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(0), "expected lint pass on durable fixture, got code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	var result productworkflow.LintSummaryResult

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "lint output should be valid JSON: %v", err)
	}

	for _, finding := range result.Findings {
		gomega.Expect(strings.HasPrefix(finding.Code, "obsidian_parity_")).To(gomega.BeFalse(), "unexpected obsidian parity finding on durable fixture: %#v", finding)
	}
})

var _ = ginkgo.It("product obsidian parity fails without no change rationale", func() {
	t := ginkgo.GinkgoT()
	dir := writeValidTestEffort(t)
	writeTestFile(t, dir, ".git/HEAD", "ref: refs/heads/main\n")
	writeTestFile(t, dir, "workflow_state.json", `{
		"schema_version":"idiom.product_workflow_state.v0.fixture",
		"effort_id":"prd-adr-brief-workflow",
		"task_id":"impl-0032-phase-5-obsidian-parity-check",
		"title":"Test Effort",
		"status_bucket":"started",
		"current_phase":"phase-5-execution-feedback",
		"current_state":"planned",
		"artifact_paths":{"events":"events.jsonl"},
		"context":{"context_pack_id":"pack","freshness_state":"current"},
		"coverage":{"coverage_tier":"unit"},
		"privacy":{"redaction_status":"applied"},
		"folder_path":"docs/test",
		"durability_level":"source_controlled",
		"next_valid_actions":["complete"]
	}`)
	writeTestFile(t, dir, productworkflow.ObsidianProductWorkflowPath, "> **Status: NOT STARTED**")
	writeTestFile(t, dir, productworkflow.ObsidianRecoveryPath, "# Recovery and Decomposition")
	writeTestFile(t, dir, "reports/impl-0032-investigation.json", validTestTestInvestigationReportJSON())

	result := productworkflow.LintEffortFolder(dir)
	gomega.Expect(result.Status).To(gomega.BeEquivalentTo(productworkflow.ProductWorkflowStatusFail), "expected lint fail, got %q", result.Status)

	found := false

	for _, finding := range result.Findings {
		if finding.Code == "obsidian_stale_status" {
			found = true
			break
		}
	}

	gomega.Expect(found).To(gomega.BeTrue(), "expected obsidian_stale_status finding, got %#v", result.Findings)
})

var _ = ginkgo.It("product obsidian parity fails closed when vault is present without notes", func() {
	t := ginkgo.GinkgoT()
	dir := writeValidTestEffort(t)
	{
		err := os.MkdirAll(filepath.Join(dir, "docs", "obsidian"), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "mkdir obsidian dir: %v", err)
	}
	writeTestFile(t, dir, "workflow_state.json", `{
		"schema_version":"idiom.product_workflow_state.v0.fixture",
		"effort_id":"prd-adr-brief-workflow",
		"task_id":"impl-0032-phase-5-obsidian-parity-check",
		"title":"Test Effort",
		"status_bucket":"started",
		"current_phase":"phase-5-execution-feedback",
		"current_state":"planned",
		"artifact_paths":{"events":"events.jsonl"},
		"context":{"context_pack_id":"pack","freshness_state":"current"},
		"coverage":{"coverage_tier":"unit"},
		"privacy":{"redaction_status":"applied"},
		"folder_path":"docs/test",
		"durability_level":"source_controlled",
		"next_valid_actions":["complete"]
	}`)

	result := productworkflow.LintEffortFolder(dir)
	gomega.Expect(result.Status).To(gomega.BeEquivalentTo(productworkflow.ProductWorkflowStatusFail), "expected lint fail when Obsidian vault is present but notes are missing, got %q", result.Status)

	var readErrors int

	for _, finding := range result.Findings {
		if finding.Code == "obsidian_read_error" {
			readErrors++

			gomega.Expect(strings.Contains(finding.Message, dir)).To(gomega.BeFalse(), "obsidian read finding leaked temp path: %#v", finding)
		}
	}

	gomega.Expect(readErrors).To(gomega.BeEquivalentTo(2), "expected two obsidian_read_error findings, got %d in %#v", readErrors, result.Findings)
})

var _ = ginkgo.It("product obsidian parity allows no change rationale", func() {
	t := ginkgo.GinkgoT()
	dir := writeValidTestEffort(t)
	writeTestFile(t, dir, ".git/HEAD", "ref: refs/heads/main\n")
	writeTestFile(t, dir, "workflow_state.json", `{
		"schema_version":"idiom.product_workflow_state.v0.fixture",
		"effort_id":"prd-adr-brief-workflow",
		"task_id":"impl-0032-phase-5-obsidian-parity-check",
		"title":"Test Effort",
		"status_bucket":"started",
		"current_phase":"phase-5-execution-feedback",
		"current_state":"planned",
		"artifact_paths":{"events":"events.jsonl"},
		"context":{"context_pack_id":"pack","freshness_state":"current"},
		"coverage":{"coverage_tier":"unit"},
		"privacy":{"redaction_status":"applied"},
		"folder_path":"docs/test",
		"durability_level":"source_controlled",
		"next_valid_actions":["complete"]
	}`)
	writeTestFile(t, dir, productworkflow.ObsidianProductWorkflowPath, "> **Status: NOT STARTED**")
	writeTestFile(t, dir, productworkflow.ObsidianRecoveryPath, "# Recovery and Decomposition")
	writeTestFile(t, dir, "reports/impl-0032-investigation.json", `{
		"schema_version":"idiom.investigation_report.v1.fixture",
		"effort_id":"prd-adr-brief-workflow",
		"task_id":"impl-0032-phase-5-obsidian-parity-check",
		"status":"pending",
		"created_at":"2026-05-17T16:00:00Z",
		"scope":{"obsidian_no_change_rationale":"No documentation edits needed; status text is intentionally deferred to closeout."},
		"tests":{"focused":"go test"},
		"coverage":{"tier":"unit"},
		"traceability":{"checked":false},
		"privacy_audit":{"no_raw_prompts":true},
		"reviewer_findings":[],
		"blockers":["implementation_pending"],
		"next_valid_action":"",
		"completed_at":""
	}`)

	result := productworkflow.LintEffortFolder(dir)
	for _, finding := range result.Findings {
		gomega.Expect(strings.HasPrefix(finding.Code, "obsidian_")).To(gomega.BeFalse(), "expected no obsidian findings with no-change rationale, got %#v", finding)
	}
})

var _ = ginkgo.It("product workflow metrics", func() {
	t := ginkgo.GinkgoT()
	dir := writeValidTestEffort(t)
	writeTestFile(t, dir, "reports/test-implementation.json", validTestImplementationReportJSON())
	writeTestFile(t, dir, "reports/test-review.json", validTestReviewReportJSON())
	writeTestFile(t, dir, "reports/test-investigation.json", validTestTestInvestigationReportJSON())

	var stdout, stderr bytes.Buffer

	code := runProductAudit([]string{"--effort-root", dir}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(0), "expected audit to pass, got code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	gomega.Expect(strings.Contains(stdout.String(), "metrics: phase=")).To(gomega.BeTrue(), "expected human output to include metrics summary, got:\n%s", stdout.String())
})

var _ = ginkgo.It("product lint text output is actionable", func() {
	t := ginkgo.GinkgoT()
	dir := writeValidTestEffort(t)

	var jsonStdout, jsonStderr bytes.Buffer

	code := runProductLint([]string{"--effort-root", dir, "--json"}, &jsonStdout, &jsonStderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(0), "expected pass, got code=%d stdout=%s stderr=%s", code, jsonStdout.String(), jsonStderr.String())

	var jsonResult productworkflow.LintSummaryResult

	{
		err := json.Unmarshal(jsonStdout.Bytes(), &jsonResult)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "invalid json: %v", err)
	}

	gomega.Expect(strings.TrimSpace(jsonResult.NextValidAction)).NotTo(gomega.BeEquivalentTo(""), "expected next_valid_action in lint json output")

	var textStdout, textStderr bytes.Buffer

	code = runProductLint([]string{"--effort-root", dir}, &textStdout, &textStderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(0), "expected pass text run, got code=%d stdout=%s stderr=%s", code, textStdout.String(), textStderr.String())

	gomega.Expect(strings.Contains(textStdout.String(), "next: "+jsonResult.NextValidAction)).To(gomega.BeTrue(), "expected text next-action parity, got:\n%s", textStdout.String())
})

var _ = ginkgo.It("product lint fail next valid action parity", func() {
	t := ginkgo.GinkgoT()
	dir := writeValidTestEffort(t)
	writeTestFile(t, dir, "tdd.json", `{not valid json`)

	var jsonStdout, jsonStderr bytes.Buffer

	code := runProductLint([]string{"--effort-root", dir, "--json"}, &jsonStdout, &jsonStderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected fail, got code=%d stdout=%s stderr=%s", code, jsonStdout.String(), jsonStderr.String())

	var jsonResult productworkflow.LintSummaryResult

	{
		err := json.Unmarshal(jsonStdout.Bytes(), &jsonResult)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "invalid json: %v", err)
	}

	var textStdout, textStderr bytes.Buffer

	code = runProductLint([]string{"--effort-root", dir}, &textStdout, &textStderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected fail text run, got code=%d stdout=%s stderr=%s", code, textStdout.String(), textStderr.String())

	gomega.Expect(strings.Contains(textStdout.String(), "next: "+jsonResult.NextValidAction)).To(gomega.BeTrue(), "expected text next-action parity, got:\n%s", textStdout.String())
})

var _ = ginkgo.It("product lint detects adr with missing evidence ref", func() {
	t := ginkgo.GinkgoT()
	dir := writeValidTestEffort(t)
	writeTestFile(t, dir, "adrs/ADR-001-test.md", `---
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
  - EV-MISSING
tdd_obligations:
  - TDD-001
coverage_tier: unit
---

# ADR-001: Test

## Context

Test.

## Decision

Test.

## Alternatives Considered

None.

## Evidence

- EV-001: Test.

## Consequences

None.

## Compatibility and Migration

Compatible.

## TDD Obligations

- TDD-001: Test.

## Rollback

Revert.

## Open Questions

None.
`)

	var stdout, stderr bytes.Buffer

	code := runProductLint([]string{"--effort-root", dir, "--json"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected lint to fail with broken ADR evidence ref, got code=%d", code)

	var result productworkflow.LintSummaryResult

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	found := false

	for _, f := range result.Findings {
		if f.Code == "broken_link" && strings.Contains(f.Path, "ADR-001") && strings.Contains(f.Message, "EV-MISSING") {
			found = true
			break
		}
	}

	gomega.Expect(found).To(gomega.BeTrue(), "expected broken_link finding for ADR evidence ref EV-MISSING, got: %#v", result.Findings)
})

var _ = ginkgo.It("product lint detects adr with missing tdd obligation", func() {
	t := ginkgo.GinkgoT()
	dir := writeValidTestEffort(t)
	writeTestFile(t, dir, "adrs/ADR-001-test.md", `---
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
  - TDD-MISSING
coverage_tier: unit
---

# ADR-001: Test

## Context

Test.

## Decision

Test.

## Alternatives Considered

None.

## Evidence

- EV-001: Test.

## Consequences

None.

## Compatibility and Migration

Compatible.

## TDD Obligations

- TDD-001: Test.

## Rollback

Revert.

## Open Questions

None.
`)

	var stdout, stderr bytes.Buffer

	code := runProductLint([]string{"--effort-root", dir, "--json"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected lint to fail with broken ADR TDD obligation ref, got code=%d", code)

	var result productworkflow.LintSummaryResult

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	found := false

	for _, f := range result.Findings {
		if f.Code == "broken_link" && strings.Contains(f.Path, "ADR-001") && strings.Contains(f.Message, "TDD-MISSING") {
			found = true
			break
		}
	}

	gomega.Expect(found).To(gomega.BeTrue(), "expected broken_link finding for ADR TDD ref TDD-MISSING, got: %#v", result.Findings)
})

var _ = ginkgo.It("product lint detects adr with missing prd ref", func() {
	t := ginkgo.GinkgoT()
	dir := writeValidTestEffort(t)
	writeTestFile(t, dir, "adrs/ADR-001-test.md", `---
schema_version: idiom.adr.v1
adr_id: ADR-001
status: accepted
accepted_at: 2026-05-17T00:00:00Z
accepted_by: test-reviewer
acceptance_profile: human-review
prd_refs:
  - REQ-MISSING
evidence_refs:
  - EV-001
tdd_obligations:
  - TDD-001
coverage_tier: unit
---

# ADR-001: Test

## Context

Test.

## Decision

Test.

## Alternatives Considered

None.

## Evidence

- EV-001: Test.

## Consequences

None.

## Compatibility and Migration

Compatible.

## TDD Obligations

- TDD-001: Test.

## Rollback

Revert.

## Open Questions

None.
`)

	var stdout, stderr bytes.Buffer

	code := runProductLint([]string{"--effort-root", dir, "--json"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected lint to fail with broken ADR PRD ref, got code=%d", code)

	var result productworkflow.LintSummaryResult

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	found := false

	for _, f := range result.Findings {
		if f.Code == "broken_link" && strings.Contains(f.Path, "ADR-001") && strings.Contains(f.Message, "REQ-MISSING") {
			found = true
			break
		}
	}

	gomega.Expect(found).To(gomega.BeTrue(), "expected broken_link finding for ADR PRD ref REQ-MISSING, got: %#v", result.Findings)
})

var _ = ginkgo.It("product lint acceptance rejects accepted prd without acceptance metadata", func() {
	t := ginkgo.GinkgoT()
	dir := writeValidTestEffort(t)
	writeTestFile(t, dir, "prd.md", `---
schema_version: idiom.prd.v1
prd_id: prd-test
status: accepted
task_id: impl-test
created_at: 2026-05-17T00:00:00Z
---

# PRD: Test

## Requirements

### REQ-TEST-001

- **id**: REQ-TEST-001
- **priority**: must
- **statement**: Test requirement
- **owner_alignment**: team
- **testability**: automated
- **metric**: coverage > 80%
- **evidence_refs**: ["EV-001"]
- **tdd_obligations**: ["TDD-001"]
- **open_questions**: []
`)

	var stdout, stderr bytes.Buffer

	code := runProductLint([]string{"--effort-root", dir, "--json"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected lint failure, got %d", code)

	var result productworkflow.LintSummaryResult

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	found := false

	for _, f := range result.Findings {
		if f.Path == "prd.md" && f.Code == "parse_error" && strings.Contains(f.Message, "missing accepted_at") {
			found = true
			break
		}
	}

	gomega.Expect(found).To(gomega.BeTrue(), "expected PRD accepted metadata parse error, got %#v", result.Findings)
})

var _ = ginkgo.It("product lint fails on a missing MUST-priority PRD TDD obligation", func() {
	t := ginkgo.GinkgoT()
	dir := writeValidTestEffort(t)
	writeTestFile(t, dir, "prd.md", `---
schema_version: idiom.prd.v1
prd_id: prd-test
status: draft
task_id: impl-test
created_at: 2026-05-17T00:00:00Z
---

# PRD: Test

## Requirements

### REQ-TEST-001

- **id**: REQ-TEST-001
- **priority**: must
- **statement**: Test requirement
- **owner_alignment**: team
- **testability**: automated
- **metric**: coverage > 80%
- **evidence_refs**: ["EV-001"]
- **tdd_obligations**: ["TDD-001", "TDD-MISSING"]
- **open_questions**: []
`)

	var stdout, stderr bytes.Buffer

	code := runProductLint([]string{"--effort-root", dir, "--json"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected lint to fail on missing must-priority PRD TDD obligation, got code=%d", code)

	var result productworkflow.LintSummaryResult

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	found := false

	for _, f := range result.Findings {
		if f.Code == "broken_link" && f.Path == "tdd.json" && strings.Contains(f.Message, "TDD-MISSING") && strings.Contains(f.Message, "REQ-TEST-001") {
			found = true
			break
		}
	}

	gomega.Expect(found).To(gomega.BeTrue(), "expected broken_link finding for tdd.json mentioning TDD-MISSING, got: %#v", result.Findings)
})

var _ = ginkgo.It("product lint accepts historical ledger satisfied tdd obligations for prd", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()
	writeTestFile(t, dir, "workflow_state.json", validTestWorkflowStateJSON())
	writeTestFile(t, dir, "tdd.json", validTestTDDJSON())
	writeTestFile(t, dir, "evidence/evidence.jsonl", validTestEvidenceJSONL())
	writeTestFile(t, dir, "evidence/traceability_matrix.json", `{
		"schema_version":"idiom.traceability_matrix.v1.fixture",
		"effort_id":"test-effort",
		"task_id":"impl-test",
		"created_at":"2026-05-17T16:00:00Z",
		"matrix":[{
			"evidence_card":"EV-001","prd_requirement":"REQ-TEST-001","adr":"ADR-001","tdd_obligation":"TDD-HIST-001","brief":"briefs/test.md","report":"reports/test.json"
		}]
	}`)
	writeTestFile(t, dir, "implementation_ledger.jsonl", strings.Join([]string{
		`{"record_type":"atom_planned","task_id":"impl-test","effort_id":"test-effort","timestamp":"2026-05-17T16:00:00Z","complexity_tier":"C1","context_pack_id":"pack","implementor":{"id":"impl","runtime":"opencode","model":"test","command":"run"},"reviewer":{"id":"rev","model":"test","reasoning_effort":"medium","runtime":"codex"},"scope":{"expected_files":["test.go"],"forbidden_patterns":["cmd/**"]},"required_verification":["go test"],"report_paths":{"implementation":"reports/impl.json","review":"reports/rev.json","investigation":"reports/inv.json"}}`,
		`{"record_type":"implementation_completed","task_id":"impl-test","effort_id":"test-effort","timestamp":"2026-05-17T16:01:00Z","implementor_id":"impl","commit_hash":"0000000000000000000000000000000000000000"}`,
		`{"record_type":"verification_completed","task_id":"impl-test","effort_id":"test-effort","timestamp":"2026-05-17T16:02:00Z","tdd_obligations_satisfied":["TDD-HIST-001"]}`,
		`{"record_type":"review_completed","task_id":"impl-test","effort_id":"test-effort","timestamp":"2026-05-17T16:03:00Z","reviewer_id":"rev","model":"test","reasoning_effort":"medium"}`,
		`{"record_type":"post_effort_investigation_completed","task_id":"impl-test","effort_id":"test-effort","timestamp":"2026-05-17T16:04:00Z"}`,
		`{"record_type":"atom_closed","task_id":"impl-test","effort_id":"test-effort","timestamp":"2026-05-17T16:05:00Z","open_blockers":[]}`,
	}, "\n"))
	writeTestFile(t, dir, "prd.md", `---
schema_version: idiom.prd.v1
prd_id: prd-test
status: draft
task_id: impl-test
created_at: 2026-05-17T00:00:00Z
---

# PRD: Test

## Requirements

### REQ-TEST-001

- **id**: REQ-TEST-001
- **priority**: must
- **statement**: Test requirement
- **owner_alignment**: team
- **testability**: automated
- **metric**: coverage > 80%
- **evidence_refs**: ["EV-001"]
- **tdd_obligations**: ["TDD-HIST-001"]
- **open_questions**: []
`)
	writeTestFile(t, dir, "adrs/index.json", validTestADRIndexJSON())
	writeTestFile(t, dir, "adrs/ADR-001-test.md", validTestADRMarkdown())
	writeTestFile(t, dir, "reports/test.json", "{}")
	writeTestFile(t, dir, "briefs/test.md", "# Test Brief")

	var stdout, stderr bytes.Buffer

	code := runProductLint([]string{"--effort-root", dir, "--json"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(0), "expected lint to pass with historical TDD obligation in PRD, got code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
})

var _ = ginkgo.It("product lint skips non-MUST-priority PRD TDD obligations", func() {
	t := ginkgo.GinkgoT()
	dir := writeValidTestEffort(t)
	writeTestFile(t, dir, "prd.md", `---
schema_version: idiom.prd.v1
prd_id: prd-test
status: draft
task_id: impl-test
created_at: 2026-05-17T00:00:00Z
---

# PRD: Test

## Requirements

### REQ-TEST-001

- **id**: REQ-TEST-001
- **priority**: should
- **statement**: Test requirement
- **owner_alignment**: team
- **testability**: automated
- **metric**: coverage > 80%
- **evidence_refs**: ["EV-001"]
- **tdd_obligations**: ["TDD-MISSING"]
- **open_questions**: []
`)

	var stdout, stderr bytes.Buffer

	code := runProductLint([]string{"--effort-root", dir, "--json"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(0), "expected lint to pass when non-must priority has missing TDD obligation, got code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
})

var _ = ginkgo.It("product lint next valid action parity", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()

	var jsonStdout, jsonStderr bytes.Buffer

	jsonCode := runProductLint([]string{"--effort-root", dir, "--json"}, &jsonStdout, &jsonStderr, nil)

	gomega.Expect(jsonCode).To(gomega.BeEquivalentTo(1), "expected lint failure exit code 1, got code=%d stdout=%s stderr=%s", jsonCode, jsonStdout.String(), jsonStderr.String())

	var out productworkflow.LintSummaryResult

	{
		err := json.Unmarshal(jsonStdout.Bytes(), &out)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "expected valid lint JSON: %v", err)
	}

	gomega.Expect(strings.TrimSpace(out.NextValidAction)).NotTo(gomega.BeEquivalentTo(""), "expected next_valid_action in JSON, got %#v", out)

	var textStdout, textStderr bytes.Buffer

	textCode := runProductLint([]string{"--effort-root", dir}, &textStdout, &textStderr, nil)

	gomega.Expect(textCode).To(gomega.BeEquivalentTo(jsonCode), "expected text and JSON exit code parity, got text=%d json=%d", textCode, jsonCode)

	text := textStdout.String()
	gomega.Expect(strings.Contains(text, "  status: FAIL")).To(gomega.BeTrue(), "expected deterministic FAIL status label, got:\n%s", text)

	gomega.Expect(strings.Contains(text, "  [missing_file] workflow_state.json: required file not found")).To(gomega.BeTrue(), "expected typed finding identity in text output, got:\n%s", text)

	gomega.Expect(strings.Contains(text, "  next: "+out.NextValidAction)).To(gomega.BeTrue(), "expected text next action parity with JSON value %q, got:\n%s", out.NextValidAction, text)
})

var _ = ginkgo.It("product lint scope gate fails on forbidden path", func() {
	t := ginkgo.GinkgoT()
	dir := writeScopeGateEffort(t, scopeGateLedgerForbiddenExactPathJSONL())
	result := productworkflow.LintEffortFolder(dir)

	gomega.Expect(result.Status).To(gomega.BeEquivalentTo(productworkflow.ProductWorkflowStatusFail), "expected fail, got %q", result.Status)

	found := false

	for _, f := range result.Findings {
		if f.Code == "scope_gate_forbidden_path" && strings.Contains(f.Message, "internal/workflows/productworkflow/scope.go") && strings.Contains(f.Message, "impl-scope-test") {
			found = true
		}
	}

	gomega.Expect(found).To(gomega.BeTrue(), "expected scope_gate_forbidden_path finding with task id and path, got %v", result.Findings)
})

var _ = ginkgo.It("product lint scope gate fails on forbidden pattern", func() {
	t := ginkgo.GinkgoT()
	dir := writeScopeGateEffort(t, scopeGateLedgerForbiddenPatternJSONL())
	result := productworkflow.LintEffortFolder(dir)

	gomega.Expect(result.Status).To(gomega.BeEquivalentTo(productworkflow.ProductWorkflowStatusFail), "expected fail, got %q", result.Status)

	found := false

	for _, f := range result.Findings {
		if f.Code == "scope_gate_forbidden_pattern" && strings.Contains(f.Message, "docs/design.md") && strings.Contains(f.Message, "docs/**") && strings.Contains(f.Message, "impl-scope-test") {
			found = true
		}
	}

	gomega.Expect(found).To(gomega.BeTrue(), "expected scope_gate_forbidden_pattern finding with task id, path, and pattern, got %v", result.Findings)
})

var _ = ginkgo.It("product lint scope gate fails on unexpected path", func() {
	t := ginkgo.GinkgoT()
	dir := writeScopeGateEffort(t, scopeGateLedgerUnexpectedPathJSONL())
	result := productworkflow.LintEffortFolder(dir)

	gomega.Expect(result.Status).To(gomega.BeEquivalentTo(productworkflow.ProductWorkflowStatusFail), "expected fail, got %q", result.Status)

	found := false

	for _, f := range result.Findings {
		if f.Code == "scope_gate_unexpected_path" && strings.Contains(f.Message, "internal/workflows/productworkflow/validate.go") && strings.Contains(f.Message, "impl-scope-test") {
			found = true
		}
	}

	gomega.Expect(found).To(gomega.BeTrue(), "expected scope_gate_unexpected_path finding with task id and path, got %v", result.Findings)
})

var _ = ginkgo.It("product lint scope gate fails on missing expected path", func() {
	t := ginkgo.GinkgoT()
	dir := writeScopeGateEffort(t, scopeGateLedgerMissingExpectedPathJSONL())
	result := productworkflow.LintEffortFolder(dir)

	gomega.Expect(result.Status).To(gomega.BeEquivalentTo(productworkflow.ProductWorkflowStatusFail), "expected fail, got %q", result.Status)

	found := false

	for _, f := range result.Findings {
		if f.Code == "scope_gate_missing_expected_path" && strings.Contains(f.Message, "cmd/idiom/main.go") && strings.Contains(f.Message, "impl-scope-test") {
			found = true
		}
	}

	gomega.Expect(found).To(gomega.BeTrue(), "expected scope_gate_missing_expected_path finding with task id and path, got %v", result.Findings)
})

var _ = ginkgo.It("product lint scope gate passing ledger passes", func() {
	t := ginkgo.GinkgoT()
	dir := writeScopeGateEffort(t, scopeGateLedgerPassingJSONL())
	result := productworkflow.LintEffortFolder(dir)

	gomega.Expect(result.Status).To(gomega.BeEquivalentTo(productworkflow.ProductWorkflowStatusPass), "expected pass, got %q with findings %v", result.Status, result.Findings)

	for _, f := range result.Findings {
		gomega.Expect(strings.HasPrefix(f.Code, "scope_gate_")).To(gomega.BeFalse(), "unexpected scope gate finding: %v", f)
	}
})

var _ = ginkgo.It("product lint scope gate closed atom not enforced", func() {
	t := ginkgo.GinkgoT()
	dir := writeScopeGateEffort(t, scopeGateLedgerForbiddenExactPathJSONL()+"\n"+
		`{"record_type":"verification_completed","task_id":"impl-scope-test","effort_id":"test-effort","timestamp":"2026-05-17T16:02:00Z"}`+"\n"+
		`{"record_type":"review_completed","task_id":"impl-scope-test","effort_id":"test-effort","timestamp":"2026-05-17T16:03:00Z","reviewer_id":"rev","model":"test","reasoning_effort":"medium"}`+"\n"+
		`{"record_type":"post_effort_investigation_completed","task_id":"impl-scope-test","effort_id":"test-effort","timestamp":"2026-05-17T16:04:00Z"}`+"\n"+
		`{"record_type":"atom_closed","task_id":"impl-scope-test","effort_id":"test-effort","timestamp":"2026-05-17T16:05:00Z","open_blockers":[]}`,
	)
	result := productworkflow.LintEffortFolder(dir)

	gomega.Expect(result.Status).To(gomega.BeEquivalentTo(productworkflow.ProductWorkflowStatusPass), "expected pass for closed atom (historical), got %q with findings %v", result.Status, result.Findings)

	for _, f := range result.Findings {
		gomega.Expect(strings.HasPrefix(f.Code, "scope_gate_")).To(gomega.BeFalse(), "scope gate should not be enforced for closed atoms: %v", f)
	}
})

var _ = ginkgo.It("product lint scope gate skips closed atoms but enforces active", func() {
	t := ginkgo.GinkgoT()
	closedAtomScope := `{"record_type":"atom_planned","task_id":"impl-closed","effort_id":"test-effort","timestamp":"2026-05-17T16:00:00Z","complexity_tier":"C1","context_pack_id":"pack","implementor":{"id":"impl","runtime":"opencode","model":"test","command":"run"},"reviewer":{"id":"rev","model":"test","reasoning_effort":"medium","runtime":"codex"},"scope":{"expected_files":["cmd/idiom/product_workflow.go"],"forbidden":["internal/workflows/productworkflow/scope.go"],"forbidden_patterns":["docs/**"]},"required_verification":["go test"],"report_paths":{"implementation":"reports/impl.json","review":"reports/rev.json","investigation":"reports/inv.json"}}` + "\n" +
		`{"record_type":"implementation_completed","task_id":"impl-closed","effort_id":"test-effort","timestamp":"2026-05-17T16:01:00Z","implementor_id":"impl","changed_paths":["cmd/idiom/product_workflow.go","internal/workflows/productworkflow/scope.go"]}` + "\n" +
		`{"record_type":"verification_completed","task_id":"impl-closed","effort_id":"test-effort","timestamp":"2026-05-17T16:02:00Z"}` + "\n" +
		`{"record_type":"review_completed","task_id":"impl-closed","effort_id":"test-effort","timestamp":"2026-05-17T16:03:00Z","reviewer_id":"rev","model":"test","reasoning_effort":"medium"}` + "\n" +
		`{"record_type":"post_effort_investigation_completed","task_id":"impl-closed","effort_id":"test-effort","timestamp":"2026-05-17T16:04:00Z"}` + "\n" +
		`{"record_type":"atom_closed","task_id":"impl-closed","effort_id":"test-effort","timestamp":"2026-05-17T16:05:00Z","open_blockers":[]}`
	activeAtomScope := scopeGatePlannedAtom + "\n" +
		`{"record_type":"implementation_completed","task_id":"impl-scope-test","effort_id":"test-effort","timestamp":"2026-05-17T16:06:00Z","implementor_id":"impl","changed_paths":["cmd/idiom/product_workflow.go","internal/workflows/productworkflow/validate.go"]}`
	dir := writeScopeGateEffort(t, closedAtomScope+"\n"+activeAtomScope)
	result := productworkflow.LintEffortFolder(dir)

	gomega.Expect(result.Status).To(gomega.BeEquivalentTo(productworkflow.ProductWorkflowStatusFail), "expected fail, got %q", result.Status)

	closedEnforced := false
	activeEnforced := false

	for _, f := range result.Findings {
		if strings.HasPrefix(f.Code, "scope_gate_") && strings.Contains(f.Message, "impl-closed") {
			closedEnforced = true
		}

		if f.Code == "scope_gate_unexpected_path" && strings.Contains(f.Message, "impl-scope-test") {
			activeEnforced = true
		}
	}

	gomega.Expect(closedEnforced).To(gomega.BeFalse(), "scope gate should not be enforced for closed atom impl-closed")

	gomega.Expect(activeEnforced).To(gomega.BeTrue(), "expected scope_gate_unexpected_path finding for active atom impl-scope-test, got %v", result.Findings)
})

var _ = ginkgo.It("product lint scope gate json valid failing", func() {
	t := ginkgo.GinkgoT()
	dir := writeScopeGateEffort(t, scopeGateLedgerForbiddenExactPathJSONL())

	var stdout, stderr bytes.Buffer

	code := runProductLint([]string{"--effort-root", dir, "--json"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected exit code 1, got %d", code)

	var result productworkflow.LintSummaryResult

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	gomega.Expect(result.Status).To(gomega.BeEquivalentTo(productworkflow.ProductWorkflowStatusFail), "expected status fail, got %q", result.Status)

	found := false

	for _, f := range result.Findings {
		if f.Code == "scope_gate_forbidden_path" {
			found = true

			gomega.Expect(f.Path).To(gomega.BeEquivalentTo("implementation_ledger.jsonl"), "expected finding path implementation_ledger.jsonl, got %q", f.Path)

			gomega.Expect(strings.Contains(f.Message, "impl-scope-test")).To(gomega.BeTrue(), "finding message should include task_id: %q", f.Message)
		}
	}

	gomega.Expect(found).To(gomega.BeTrue(), "expected scope_gate_forbidden_path finding, got %v", result.Findings)
})

var _ = ginkgo.It("product lint scope gate json valid passing", func() {
	t := ginkgo.GinkgoT()
	dir := writeScopeGateEffort(t, scopeGateLedgerPassingJSONL())

	var stdout, stderr bytes.Buffer

	code := runProductLint([]string{"--effort-root", dir, "--json"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(0), "expected exit code 0, got %d: %s", code, stderr.String())

	var result productworkflow.LintSummaryResult

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	gomega.Expect(result.Status).To(gomega.BeEquivalentTo(productworkflow.ProductWorkflowStatusPass), "expected status pass, got %q", result.Status)
})

var _ = ginkgo.It("product lint per atom commit coverage detects under reporting", func() {
	t := ginkgo.GinkgoT()
	dir := writeValidTestEffort(t)
	{
		err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "mkdir .git: %v", err)
	}

	ledgerWithCommit := validTestImplementationLedgerJSONL() + "\n" +
		`{"record_type":"implementation_completed","task_id":"impl-commit-test","effort_id":"test-effort","timestamp":"2026-05-18T00:00:00Z","implementor_id":"impl","commit_hash":"0000000000000000000000000000000000000000","changed_paths":["cmd/foo.go"]}`
	writeTestFile(t, dir, "implementation_ledger.jsonl", ledgerWithCommit)

	result := productworkflow.LintEffortFolder(dir)
	found := false

	for _, f := range result.Findings {
		if f.Code == "commit_hash_not_found" || f.Code == "atom_under_reports_path" {
			found = true
		}
	}

	gomega.Expect(found).To(gomega.BeTrue(), "expected per-atom commit coverage finding, got %v", result.Findings)
})

var _ = ginkgo.It("product lint per atom commit coverage fails without commit hash", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()
	writeTestFile(t, dir, "workflow_state.json", validTestWorkflowStateJSON())
	writeTestFile(t, dir, "tdd.json", validTestTDDJSON())
	writeTestFile(t, dir, "evidence/evidence.jsonl", validTestEvidenceJSONL())
	writeTestFile(t, dir, "evidence/traceability_matrix.json", validTestTraceabilityMatrixJSON())
	writeTestFile(t, dir, "implementation_ledger.jsonl", strings.Join([]string{
		`{"record_type":"atom_planned","task_id":"impl-test","effort_id":"test-effort","timestamp":"2026-05-18T00:00:00Z","complexity_tier":"C1","context_pack_id":"pack","implementor":{"id":"impl","runtime":"opencode","model":"test","command":"run"},"reviewer":{"id":"rev","model":"test","reasoning_effort":"medium","runtime":"codex"},"scope":{"expected_files":["test.go"],"forbidden_patterns":["cmd/**"]},"required_verification":["go test"],"report_paths":{"implementation":"reports/impl.json","review":"reports/rev.json","investigation":"reports/inv.json"}}`,
		`{"record_type":"implementation_completed","task_id":"impl-test","effort_id":"test-effort","timestamp":"2026-05-18T00:01:00Z","implementor_id":"impl"}`,
		`{"record_type":"verification_completed","task_id":"impl-test","effort_id":"test-effort","timestamp":"2026-05-18T00:02:00Z"}`,
		`{"record_type":"review_completed","task_id":"impl-test","effort_id":"test-effort","timestamp":"2026-05-18T00:03:00Z","reviewer_id":"rev","model":"test","reasoning_effort":"medium"}`,
		`{"record_type":"post_effort_investigation_completed","task_id":"impl-test","effort_id":"test-effort","timestamp":"2026-05-18T00:04:00Z"}`,
		`{"record_type":"atom_closed","task_id":"impl-test","effort_id":"test-effort","timestamp":"2026-05-18T00:05:00Z","open_blockers":[]}`,
	}, "\n"))
	writeTestFile(t, dir, "prd.md", validTestPRDMarkdown())
	writeTestFile(t, dir, "adrs/index.json", validTestADRIndexJSON())
	writeTestFile(t, dir, "adrs/ADR-001-test.md", validTestADRMarkdown())
	writeTestFile(t, dir, "briefs/test.md", "# Test Brief")
	writeTestFile(t, dir, "reports/test.json", "{}")

	result := productworkflow.LintEffortFolder(dir)
	gomega.Expect(result.Status).To(gomega.BeEquivalentTo(productworkflow.ProductWorkflowStatusFail), "expected fail for ledger without commit_hash, got %q", result.Status)

	found := false

	for _, f := range result.Findings {
		if f.Code == "commit_hash_missing" {
			found = true
			break
		}
	}

	gomega.Expect(found).To(gomega.BeTrue(), "expected commit_hash_missing finding, got %v", result.Findings)
})

var _ = ginkgo.It("product lint per atom real git under reporting", func() {
	t := ginkgo.GinkgoT()
	if _, err := exec.LookPath("git"); err != nil {
		ginkgo.Skip("git not available")
	}

	dir := t.TempDir()
	runGit := func(args ...string) {
		cmd := exec.Command("git", args...)

		cmd.Dir = dir

		{
			out, err := cmd.CombinedOutput()
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), "git %s: %s: %v", args[0], strings.TrimSpace(string(out)), err)
		}
	}
	runGit("init")
	runGit("config", "user.email", "test@test.com")
	runGit("config", "user.name", "Test")

	writeTestFile(t, dir, "effort/cmd/foo.go", "package foo")
	writeTestFile(t, dir, "effort/internal/bar.go", "package bar")
	writeTestFile(t, dir, "effort/internal/baz.go", "package baz")
	runGit("add", ".")
	runGit("commit", "-m", "initial")

	hashCmd := exec.Command("git", "-C", dir, "rev-parse", "HEAD")

	hashOut, err := hashCmd.Output()
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "git rev-parse: %v", err)

	commitHash := strings.TrimSpace(string(hashOut))

	effortDir := filepath.Join(dir, "effort")
	writeTestFile(t, effortDir, "workflow_state.json", validTestWorkflowStateJSON())
	writeTestFile(t, effortDir, "tdd.json", validTestTDDJSON())
	writeTestFile(t, effortDir, "evidence/evidence.jsonl", validTestEvidenceJSONL())
	writeTestFile(t, effortDir, "evidence/traceability_matrix.json", validTestTraceabilityMatrixJSON())
	writeTestFile(t, effortDir, "prd.md", validTestPRDMarkdown())
	writeTestFile(t, effortDir, "adrs/index.json", validTestADRIndexJSON())
	writeTestFile(t, effortDir, "adrs/ADR-001-test.md", validTestADRMarkdown())
	writeTestFile(t, effortDir, "briefs/test.md", "# Test Brief")
	writeTestFile(t, effortDir, "reports/test.json", "{}")

	ledgerEntry := `{"record_type":"implementation_completed","task_id":"impl-commit-test","effort_id":"test-effort","timestamp":"2026-05-18T00:06:00Z","implementor_id":"impl","commit_hash":"` + commitHash + `","changed_paths":["cmd/foo.go"]}`
	writeTestFile(t, effortDir, "implementation_ledger.jsonl", validTestImplementationLedgerJSONL()+"\n"+ledgerEntry)

	result := productworkflow.LintEffortFolder(effortDir)

	var underReportMsgs []string

	for _, f := range result.Findings {
		if f.Code == "atom_under_reports_path" {
			underReportMsgs = append(underReportMsgs, f.Message)
		}
	}

	gomega.Expect(len(underReportMsgs)).NotTo(gomega.BeNumerically("<", 2), "expected at least 2 atom_under_reports_path findings (bar.go and baz.go), got %d: %v", len(underReportMsgs), underReportMsgs)

	foundBar := false
	foundBaz := false

	for _, msg := range underReportMsgs {
		if strings.Contains(msg, "internal/bar.go") {
			foundBar = true
		}

		if strings.Contains(msg, "internal/baz.go") {
			foundBaz = true
		}
	}

	gomega.Expect(foundBar).To(gomega.BeTrue(), "expected findings for bar.go and baz.go, got %v", underReportMsgs)
	gomega.Expect(foundBaz).To(gomega.BeTrue(), "expected findings for bar.go and baz.go, got %v", underReportMsgs)
})

var _ = ginkgo.It("product lint per atom real git matching paths", func() {
	t := ginkgo.GinkgoT()
	if _, err := exec.LookPath("git"); err != nil {
		ginkgo.Skip("git not available")
	}

	dir := t.TempDir()
	runGit := func(args ...string) {
		cmd := exec.Command("git", args...)

		cmd.Dir = dir

		{
			out, err := cmd.CombinedOutput()
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), "git %s: %s: %v", args[0], strings.TrimSpace(string(out)), err)
		}
	}
	runGit("init")
	runGit("config", "user.email", "test@test.com")
	runGit("config", "user.name", "Test")

	writeTestFile(t, dir, "effort/cmd/foo.go", "package foo")
	writeTestFile(t, dir, "effort/internal/bar.go", "package bar")
	runGit("add", ".")
	runGit("commit", "-m", "initial")

	hashCmd := exec.Command("git", "-C", dir, "rev-parse", "HEAD")

	hashOut, err := hashCmd.Output()
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "git rev-parse: %v", err)

	commitHash := strings.TrimSpace(string(hashOut))

	effortDir := filepath.Join(dir, "effort")
	writeTestFile(t, effortDir, "workflow_state.json", validTestWorkflowStateJSON())
	writeTestFile(t, effortDir, "tdd.json", validTestTDDJSON())
	writeTestFile(t, effortDir, "evidence/evidence.jsonl", validTestEvidenceJSONL())
	writeTestFile(t, effortDir, "evidence/traceability_matrix.json", validTestTraceabilityMatrixJSON())
	writeTestFile(t, effortDir, "prd.md", validTestPRDMarkdown())
	writeTestFile(t, effortDir, "adrs/index.json", validTestADRIndexJSON())
	writeTestFile(t, effortDir, "adrs/ADR-001-test.md", validTestADRMarkdown())
	writeTestFile(t, effortDir, "briefs/test.md", "# Test Brief")
	writeTestFile(t, effortDir, "reports/test.json", "{}")

	ledgerEntry := `{"record_type":"implementation_completed","task_id":"impl-commit-test","effort_id":"test-effort","timestamp":"2026-05-18T00:06:00Z","implementor_id":"impl","commit_hash":"` + commitHash + `","changed_paths":["cmd/foo.go","internal/bar.go"]}`
	writeTestFile(t, effortDir, "implementation_ledger.jsonl", validTestImplementationLedgerJSONL()+"\n"+ledgerEntry)

	result := productworkflow.LintEffortFolder(effortDir)

	for _, f := range result.Findings {
		gomega.Expect(f.Code).NotTo(gomega.BeEquivalentTo("atom_under_reports_path"), "expected no atom_under_reports_path when paths match, got: %s: %s", f.Path, f.Message)
	}
})
