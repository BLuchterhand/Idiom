package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"idiom/internal/productworkflow"
	"idiom/internal/recovery"
)

func TestProductLintPassesOnCurrentDurableEffortFixture(t *testing.T) {
	root := repoRoot(t)
	effortRoot := filepath.Join(root, "docs", "product-workflows", "efforts", "closed", "prd-adr-brief-workflow")

	var stdout, stderr bytes.Buffer
	code := runProductLint([]string{"--effort-root", effortRoot, "--json"}, &stdout, &stderr, nil)
	if code != 0 {
		t.Fatalf("expected lint to pass on current durable effort fixture, got code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var result productworkflow.LintSummaryResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	if result.Status != "pass" {
		t.Fatalf("expected status pass, got %q: %#v", result.Status, result)
	}
	if result.EffortRoot == "" {
		t.Fatal("effort_root should be set")
	}
	if len(result.CheckedFiles) == 0 {
		t.Fatal("should have checked at least one file")
	}
	if len(result.Findings) != 0 {
		t.Fatalf("expected zero findings, got %d: %#v", len(result.Findings), result.Findings)
	}
}

func TestProductLintFailsOnMissingRequiredFile(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := runProductLint([]string{"--effort-root", dir, "--json"}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("expected lint to fail on empty dir, got code=%d", code)
	}
	var result productworkflow.LintSummaryResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	if result.Status != "fail" {
		t.Fatalf("expected status fail, got %q", result.Status)
	}
	found := false
	for _, f := range result.Findings {
		if f.Code == "missing_file" && f.Path == "workflow_state.json" && strings.Contains(f.Message, "not found") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected missing_file finding for workflow_state.json, got: %#v", result.Findings)
	}
}

func TestProductLintFailsOnInvalidImplementationLedgerCloseout(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "workflow_state.json", validTestWorkflowStateJSON())
	writeTestFile(t, dir, "tdd.json", validTestTDDJSON())
	writeTestFile(t, dir, "evidence/evidence.jsonl", validTestEvidenceJSONL())
	writeTestFile(t, dir, "evidence/traceability_matrix.json", validTestTraceabilityMatrixJSON())
	writeTestFile(t, dir, "implementation_ledger.jsonl", invalidCloseoutLedgerJSONL())
	writeTestFile(t, dir, "reports/test.json", "{}")

	var stdout, stderr bytes.Buffer
	code := runProductLint([]string{"--effort-root", dir, "--json"}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("expected lint to fail on invalid closeout, got code=%d", code)
	}
	var result productworkflow.LintSummaryResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	if result.Status != "fail" {
		t.Fatalf("expected status fail, got %q", result.Status)
	}
	found := false
	for _, f := range result.Findings {
		if f.Code == "parse_error" && f.Path == "implementation_ledger.jsonl" && strings.Contains(f.Message, "atom_closed") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected parse_error finding mentioning atom_closed, got: %#v", result.Findings)
	}
}

func TestProductLintIncludesPRDAndADRInCheckedFiles(t *testing.T) {
	root := repoRoot(t)
	effortRoot := filepath.Join(root, "docs", "product-workflows", "efforts", "closed", "prd-adr-brief-workflow")

	var stdout, stderr bytes.Buffer
	code := runProductLint([]string{"--effort-root", effortRoot, "--json"}, &stdout, &stderr, nil)
	if code != 0 {
		t.Fatalf("expected lint to pass, got code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var result productworkflow.LintSummaryResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	wantFiles := []string{"prd.md", "adrs/index.json", "adrs/ADR-0001-durable-workflow-substrate.md"}
	for _, want := range wantFiles {
		found := false
		for _, f := range result.CheckedFiles {
			if f == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected checked_files to include %q, got %v", want, result.CheckedFiles)
		}
	}
}

func TestProductLintTelemetryClassifiesAsProductLint(t *testing.T) {
	name := commandTelemetryName([]string{"product", "lint", "--effort-root", "."})
	if name != "product:lint" {
		t.Fatalf("expected telemetry name product:lint, got %q", name)
	}
}

func TestProductLintUsageErrors(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runProductLint([]string{}, &stdout, &stderr, nil)
	if code != 2 {
		t.Fatalf("expected code 2 for missing --effort-root, got %d", code)
	}
}

func TestProductLintNoSubcommandUsageError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runProductMainWithTelemetry([]string{}, &stdout, &stderr, nil)
	if code != 2 {
		t.Fatalf("expected code 2 for no subcommand, got %d", code)
	}
}

func TestProductLintHelpFlags(t *testing.T) {
	for _, args := range [][]string{{"-h"}, {"--help"}, {"help"}} {
		var stdout, stderr bytes.Buffer
		code := runProductMainWithTelemetry(args, &stdout, &stderr, nil)
		if code != 0 {
			t.Fatalf("expected code 0 for help args %v, got %d", args, code)
		}
	}
}

func TestProductLintChecksOptionalResearchProfileWhenConfigured(t *testing.T) {
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
	if code != 0 {
		t.Fatalf("expected lint to pass with valid research profile, got code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var result productworkflow.LintSummaryResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	if result.Status != "pass" {
		t.Fatalf("expected status pass, got %q: %#v", result.Status, result)
	}
	found := false
	for _, f := range result.CheckedFiles {
		if f == "research_profile.json" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected research_profile.json in checked_files, got %v", result.CheckedFiles)
	}
}

func TestProductLintRejectsInvalidResearchProfile(t *testing.T) {
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
	if code != 1 {
		t.Fatalf("expected lint to fail with invalid research profile, got code=%d", code)
	}
	var result productworkflow.LintSummaryResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	found := false
	for _, f := range result.Findings {
		if f.Path == "research_profile.json" && f.Code == "parse_error" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected parse_error finding for research_profile.json, got: %#v", result.Findings)
	}
}

func TestProductLintParseErrorOnInvalidTDDJSON(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "workflow_state.json", validTestWorkflowStateJSON())
	writeTestFile(t, dir, "evidence/evidence.jsonl", validTestEvidenceJSONL())
	writeTestFile(t, dir, "evidence/traceability_matrix.json", validTestTraceabilityMatrixJSON())
	writeTestFile(t, dir, "implementation_ledger.jsonl", validTestImplementationLedgerJSONL())
	writeTestFile(t, dir, "reports/test.json", "{}")
	writeTestFile(t, dir, "tdd.json", `{not valid json`)

	var stdout, stderr bytes.Buffer
	code := runProductLint([]string{"--effort-root", dir, "--json"}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("expected lint to fail on invalid tdd.json, got code=%d", code)
	}
	var result productworkflow.LintSummaryResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	found := false
	for _, f := range result.Findings {
		if f.Code == "parse_error" && f.Path == "tdd.json" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected parse_error finding for tdd.json, got: %#v", result.Findings)
	}
}

func TestProductLintFailsOnInvalidTDDCoverageTier(t *testing.T) {
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
	if code != 1 {
		t.Fatalf("expected lint to fail on invalid coverage_tier, got code=%d", code)
	}
	var result productworkflow.LintSummaryResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	found := false
	for _, f := range result.Findings {
		if f.Code == "parse_error" && f.Path == "tdd.json" && strings.Contains(f.Message, "invalid coverage_tier") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected parse_error for invalid coverage_tier, got: %#v", result.Findings)
	}
}

func TestProductTDDCoverageGateReportsMissingTDDEvidence(t *testing.T) {
	dir := writeValidTestEffort(t)
	writeTestFile(t, dir, "implementation_ledger.jsonl", implementationLedgerWithoutTDDEvidenceJSONL())

	var stdout, stderr bytes.Buffer
	code := runProductLint([]string{"--effort-root", dir, "--json"}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("expected lint to fail on missing TDD evidence, got code=%d", code)
	}
	var result productworkflow.LintSummaryResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	found := false
	for _, f := range result.Findings {
		if f.Code == "coverage_tdd_evidence_missing" && f.Path == "tdd.json" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected coverage_tdd_evidence_missing finding, got: %#v", result.Findings)
	}
}

func TestProductLintPassesWithValidTDDCoverageAndEvidence(t *testing.T) {
	dir := writeValidTestEffort(t)

	var stdout, stderr bytes.Buffer
	code := runProductLint([]string{"--effort-root", dir, "--json"}, &stdout, &stderr, nil)
	if code != 0 {
		t.Fatalf("expected lint to pass with valid TDD coverage and evidence, got code=%d\n%s", code, stdout.String())
	}
	var result productworkflow.LintSummaryResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	if result.Status != "pass" {
		t.Fatalf("expected pass status, got %q: %#v", result.Status, result.Findings)
	}
}

func TestProductLintDetectsBrokenTraceabilityLinks(t *testing.T) {
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
	if code != 1 {
		t.Fatalf("expected lint to fail with broken links, got code=%d", code)
	}
	var result productworkflow.LintSummaryResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
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
		if !found {
			t.Fatalf("expected broken_link finding for field %q, got: %#v", field, result.Findings)
		}
	}
}

func TestProductLintAcceptsHistoricalTDDObligationIDs(t *testing.T) {
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
	if code != 0 {
		t.Fatalf("expected lint to pass with historical TDD ID, got code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
}

func TestProductLintTraceabilityMatrixInCheckedFiles(t *testing.T) {
	root := repoRoot(t)
	effortRoot := filepath.Join(root, "docs", "product-workflows", "efforts", "closed", "prd-adr-brief-workflow")

	var stdout, stderr bytes.Buffer
	code := runProductLint([]string{"--effort-root", effortRoot, "--json"}, &stdout, &stderr, nil)
	if code != 0 {
		t.Fatalf("expected lint to pass, got code=%d", code)
	}
	var result productworkflow.LintSummaryResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	found := false
	for _, f := range result.CheckedFiles {
		if f == "evidence/traceability_matrix.json" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected evidence/traceability_matrix.json in checked_files, got %v", result.CheckedFiles)
	}
}

func TestProductLintRejectsInvalidPRDArtifactStatus(t *testing.T) {
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
	if code != 1 {
		t.Fatalf("expected lint to fail with invalid PRD status, got code=%d", code)
	}
	var result productworkflow.LintSummaryResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	found := false
	for _, f := range result.Findings {
		if f.Code == "parse_error" && f.Path == "prd.md" && strings.Contains(f.Message, "accepted_fixture") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected parse_error finding for prd.md mentioning accepted_fixture, got: %#v", result.Findings)
	}
}

func TestProductLintRejectsInvalidADRIndexArtifactStatus(t *testing.T) {
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
	if code != 1 {
		t.Fatalf("expected lint to fail with invalid ADR index status, got code=%d", code)
	}
	var result productworkflow.LintSummaryResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	found := false
	for _, f := range result.Findings {
		if f.Code == "parse_error" && f.Path == "adrs/index.json" && strings.Contains(f.Message, "accepted_fixture") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected parse_error finding for adrs/index.json mentioning accepted_fixture, got: %#v", result.Findings)
	}
}

func TestProductLintRejectsInvalidADRMarkdownArtifactStatus(t *testing.T) {
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
	if code != 1 {
		t.Fatalf("expected lint to fail with invalid ADR markdown status, got code=%d", code)
	}
	var result productworkflow.LintSummaryResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	found := false
	for _, f := range result.Findings {
		if f.Code == "parse_error" && f.Path == "adrs/ADR-001-test.md" && strings.Contains(f.Message, "accepted_fixture") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected parse_error finding for ADR markdown mentioning accepted_fixture, got: %#v", result.Findings)
	}
}

func TestProductLintRejectsMalformedImplementationReport(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "workflow_state.json", validTestWorkflowStateJSON())
	writeTestFile(t, dir, "tdd.json", validTestTDDJSON())
	writeTestFile(t, dir, "evidence/evidence.jsonl", validTestEvidenceJSONL())
	writeTestFile(t, dir, "evidence/traceability_matrix.json", validTestTraceabilityMatrixJSON())
	writeTestFile(t, dir, "implementation_ledger.jsonl", validTestImplementationLedgerJSONL())
	writeTestFile(t, dir, "reports/impl-bad-implementation.json", `{"schema_version":""}`)

	var stdout, stderr bytes.Buffer
	code := runProductLint([]string{"--effort-root", dir, "--json"}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("expected lint to fail on malformed implementation report, got code=%d", code)
	}
	var result productworkflow.LintSummaryResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	found := false
	for _, f := range result.Findings {
		if f.Code == "parse_error" && f.Path == "reports/impl-bad-implementation.json" && strings.Contains(f.Message, "missing task_id") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected parse_error finding for malformed implementation report, got: %#v", result.Findings)
	}
}

func TestProductLintRejectsMalformedReviewReport(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "workflow_state.json", validTestWorkflowStateJSON())
	writeTestFile(t, dir, "tdd.json", validTestTDDJSON())
	writeTestFile(t, dir, "evidence/evidence.jsonl", validTestEvidenceJSONL())
	writeTestFile(t, dir, "evidence/traceability_matrix.json", validTestTraceabilityMatrixJSON())
	writeTestFile(t, dir, "implementation_ledger.jsonl", validTestImplementationLedgerJSONL())
	writeTestFile(t, dir, "reports/impl-bad-review.json", `{"schema_version":"v1","reviewer":{"independent_from_implementor":false}}`)

	var stdout, stderr bytes.Buffer
	code := runProductLint([]string{"--effort-root", dir, "--json"}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("expected lint to fail on malformed review report, got code=%d", code)
	}
	var result productworkflow.LintSummaryResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	found := false
	for _, f := range result.Findings {
		if f.Code == "parse_error" && f.Path == "reports/impl-bad-review.json" && strings.Contains(f.Message, "independent_from_implementor must be true") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected parse_error for review report independence, got: %#v", result.Findings)
	}
}

func TestProductLintRejectsMalformedInvestigationReport(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "workflow_state.json", validTestWorkflowStateJSON())
	writeTestFile(t, dir, "tdd.json", validTestTDDJSON())
	writeTestFile(t, dir, "evidence/evidence.jsonl", validTestEvidenceJSONL())
	writeTestFile(t, dir, "evidence/traceability_matrix.json", validTestTraceabilityMatrixJSON())
	writeTestFile(t, dir, "implementation_ledger.jsonl", validTestImplementationLedgerJSONL())
	writeTestFile(t, dir, "reports/impl-bad-investigation.json", `{"schema_version":"v1","status":"complete","blockers":["still_broken"]}`)

	var stdout, stderr bytes.Buffer
	code := runProductLint([]string{"--effort-root", dir, "--json"}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("expected lint to fail on malformed investigation report, got code=%d", code)
	}
	var result productworkflow.LintSummaryResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	found := false
	for _, f := range result.Findings {
		if f.Code == "parse_error" && f.Path == "reports/impl-bad-investigation.json" && strings.Contains(f.Message, "blockers is non-empty") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected parse_error for investigation with blockers, got: %#v", result.Findings)
	}
}

func TestProductLintAcceptsUnknownReportJSON(t *testing.T) {
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
	if code != 0 {
		t.Fatalf("expected lint to pass with unknown report JSON, got code=%d stdout=%s", code, stdout.String())
	}
}

func TestProductLintPassesWithValidReportFixtures(t *testing.T) {
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
	if code != 0 {
		t.Fatalf("expected lint to pass with valid reports, got code=%d stdout=%s", code, stdout.String())
	}
}

func TestProductAuditPassesOnDurableEffortFixture(t *testing.T) {
	root := repoRoot(t)
	effortRoot := filepath.Join(root, "docs", "product-workflows", "efforts", "closed", "prd-adr-brief-workflow")

	var stdout, stderr bytes.Buffer
	code := runProductAudit([]string{"--effort-root", effortRoot, "--json"}, &stdout, &stderr, nil)
	if code != 0 {
		t.Fatalf("expected audit to pass on durable effort fixture, got code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var result productAuditResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	if result.Status != "pass" {
		t.Fatalf("expected status pass, got %q", result.Status)
	}
	if result.EffortRoot == "" {
		t.Fatal("effort_root should be set")
	}
	if len(result.CheckedFiles) == 0 {
		t.Fatal("should have checked at least one file")
	}
	requiredSections := []string{"prd", "adr", "tdd", "traceability", "reports", "obsidian", "scope_gate", "metrics"}
	if len(result.Sections) != len(requiredSections) {
		t.Fatalf("expected %d sections, got %d", len(requiredSections), len(result.Sections))
	}
	for i, name := range requiredSections {
		if result.Sections[i].Name != name {
			t.Fatalf("expected section %d to be %q, got %q", i, name, result.Sections[i].Name)
		}
		if result.Sections[i].Status != "pass" {
			t.Fatalf("expected section %q to pass, got %q", name, result.Sections[i].Status)
		}
	}
}

func TestProductAuditJSONIncludesAllRequiredSections(t *testing.T) {
	dir := writeValidTestEffort(t)

	var stdout, stderr bytes.Buffer
	code := runProductAudit([]string{"--effort-root", dir, "--json"}, &stdout, &stderr, nil)
	if code != 0 {
		t.Fatalf("expected audit to pass, got code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var result productAuditResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	sectionNames := map[string]bool{}
	for _, sec := range result.Sections {
		sectionNames[sec.Name] = true
	}
	for _, name := range []string{"prd", "adr", "tdd", "traceability", "reports", "obsidian", "scope_gate"} {
		if !sectionNames[name] {
			t.Fatalf("expected section %q in audit result", name)
		}
	}
}

func TestProductObsidianParityDurableFixturePasses(t *testing.T) {
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
	if code != 0 {
		t.Fatalf("expected lint pass on durable fixture, got code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var result productworkflow.LintSummaryResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("lint output should be valid JSON: %v", err)
	}
	for _, finding := range result.Findings {
		if strings.HasPrefix(finding.Code, "obsidian_parity_") {
			t.Fatalf("unexpected obsidian parity finding on durable fixture: %#v", finding)
		}
	}
}

func TestProductObsidianParityFailsWithoutNoChangeRationale(t *testing.T) {
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
	if result.Status != "fail" {
		t.Fatalf("expected lint fail, got %q", result.Status)
	}
	found := false
	for _, finding := range result.Findings {
		if finding.Code == "obsidian_stale_status" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected obsidian_stale_status finding, got %#v", result.Findings)
	}
}

func TestProductObsidianParityFailsClosedWithoutGitRoot(t *testing.T) {
	dir := writeValidTestEffort(t)
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
	if result.Status != "fail" {
		t.Fatalf("expected lint fail when Obsidian notes are missing without git root, got %q", result.Status)
	}
	var readErrors int
	for _, finding := range result.Findings {
		if finding.Code == "obsidian_read_error" {
			readErrors++
			if strings.Contains(finding.Message, dir) {
				t.Fatalf("obsidian read finding leaked temp path: %#v", finding)
			}
		}
	}
	if readErrors != 2 {
		t.Fatalf("expected two obsidian_read_error findings, got %d in %#v", readErrors, result.Findings)
	}
}

func TestProductObsidianParityAllowsNoChangeRationale(t *testing.T) {
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
		if strings.HasPrefix(finding.Code, "obsidian_") {
			t.Fatalf("expected no obsidian findings with no-change rationale, got %#v", finding)
		}
	}
}

func TestProductStatusObsidianParityFailureSections(t *testing.T) {
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

	var jsonStdout, jsonStderr bytes.Buffer
	code := runProductStatus([]string{"--effort-root", dir, "--json"}, &jsonStdout, &jsonStderr, nil)
	if code != 1 {
		t.Fatalf("expected status JSON to fail on Obsidian parity, got code=%d stdout=%s stderr=%s", code, jsonStdout.String(), jsonStderr.String())
	}
	var view productworkflow.ProductStatusView
	if err := json.Unmarshal(jsonStdout.Bytes(), &view); err != nil {
		t.Fatalf("status output should be valid JSON: %v\n%s", err, jsonStdout.String())
	}
	found := false
	for _, sec := range view.Sections {
		if sec.Name == "obsidian" {
			found = true
			if sec.Status != "fail" || sec.FindingCount == 0 {
				t.Fatalf("expected failing obsidian section, got %#v", sec)
			}
		}
	}
	if !found {
		t.Fatalf("expected obsidian section in status JSON, got %#v", view.Sections)
	}

	var textStdout, textStderr bytes.Buffer
	code = runProductStatus([]string{"--effort-root", dir}, &textStdout, &textStderr, nil)
	if code != 1 {
		t.Fatalf("expected status text to fail on Obsidian parity, got code=%d stdout=%s stderr=%s", code, textStdout.String(), textStderr.String())
	}
	if !strings.Contains(textStdout.String(), "obsidian: fail") || !strings.Contains(textStdout.String(), "findings") {
		t.Fatalf("expected text output to include failing obsidian section, got:\n%s", textStdout.String())
	}
}

func TestProductAuditMetrics(t *testing.T) {
	dir := writeValidTestEffort(t)
	writeTestFile(t, dir, "reports/test-implementation.json", validTestImplementationReportJSON())
	writeTestFile(t, dir, "reports/test-review.json", validTestReviewReportJSON())
	writeTestFile(t, dir, "reports/test-investigation.json", validTestTestInvestigationReportJSON())

	var stdout, stderr bytes.Buffer
	code := runProductAudit([]string{"--effort-root", dir, "--json"}, &stdout, &stderr, nil)
	if code != 0 {
		t.Fatalf("expected audit to pass, got code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var result productAuditResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	if result.Metrics == nil {
		t.Fatal("expected metrics in product audit JSON")
	}
	if result.Metrics.CurrentPhase == "" || result.Metrics.CurrentState == "" {
		t.Fatalf("expected phase/state metrics, got %#v", result.Metrics)
	}
	if result.Metrics.NextValidActionCount <= 0 {
		t.Fatalf("expected next_valid_action_count > 0, got %d", result.Metrics.NextValidActionCount)
	}
	if result.Metrics.TDDObligationTotal != 1 {
		t.Fatalf("expected tdd_obligation_total=1, got %d", result.Metrics.TDDObligationTotal)
	}
}

func TestProductAuditMetricsErrorUsesRelativeArtifactPath(t *testing.T) {
	dir := writeValidTestEffort(t)
	writeTestFile(t, dir, "tdd.json", `{not valid json`)

	var stdout, stderr bytes.Buffer
	code := runProductAudit([]string{"--effort-root", dir, "--json"}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("expected audit failure, got code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var result productAuditResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	found := false
	for _, finding := range result.Findings {
		if finding.Code != "metrics_error" {
			continue
		}
		found = true
		if finding.Path != "tdd.json" {
			t.Fatalf("expected metrics_error path tdd.json, got %#v", finding)
		}
		if strings.Contains(finding.Message, dir) {
			t.Fatalf("metrics_error should not leak absolute effort path, got %q", finding.Message)
		}
	}
	if !found {
		t.Fatalf("expected metrics_error finding, got %#v", result.Findings)
	}
}

func TestProductAuditMetricsFailsClosedOnUnknownGateStatus(t *testing.T) {
	dir := writeValidTestEffort(t)
	writeTestFile(t, dir, "reports/test-implementation.json", validTestImplementationReportJSON())
	writeTestFile(t, dir, "reports/test-review.json", validTestReviewReportJSON())
	writeTestFile(t, dir, "reports/test-investigation.json", validTestTestInvestigationReportJSON())
	writeTestFile(t, dir, "workflow_state.json", strings.Replace(
		validTestWorkflowStateJSON(),
		`"coverage":{"coverage_tier":"unit"}`,
		`"coverage":{"coverage_tier":"unit","gate_results":[{"gate":"metrics","status":"surprise"}]}`,
		1,
	))

	var stdout, stderr bytes.Buffer
	code := runProductAudit([]string{"--effort-root", dir, "--json"}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("expected audit to fail closed, got code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var result productAuditResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	if result.Status != "fail" {
		t.Fatalf("expected status fail, got %q", result.Status)
	}
	metricsSection := findAuditSection(t, result, "metrics")
	if metricsSection.Status != "fail" || len(metricsSection.Findings) != 1 {
		t.Fatalf("expected metrics section failure, got %#v", metricsSection)
	}
	if result.Metrics != nil {
		t.Fatalf("expected metrics omitted on failure, got %#v", result.Metrics)
	}
	if !strings.Contains(metricsSection.Findings[0].Message, "unsupported") {
		t.Fatalf("expected unsupported-value metrics finding, got %#v", metricsSection.Findings[0])
	}
}

func TestProductWorkflowMetrics(t *testing.T) {
	dir := writeValidTestEffort(t)
	writeTestFile(t, dir, "reports/test-implementation.json", validTestImplementationReportJSON())
	writeTestFile(t, dir, "reports/test-review.json", validTestReviewReportJSON())
	writeTestFile(t, dir, "reports/test-investigation.json", validTestTestInvestigationReportJSON())

	var stdout, stderr bytes.Buffer
	code := runProductAudit([]string{"--effort-root", dir}, &stdout, &stderr, nil)
	if code != 0 {
		t.Fatalf("expected audit to pass, got code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "metrics: phase=") {
		t.Fatalf("expected human output to include metrics summary, got:\n%s", stdout.String())
	}
}

func TestProductAuditSurfacesParseErrorInPRDSection(t *testing.T) {
	dir := writeValidTestEffort(t)
	writeTestFile(t, dir, "prd.md", `not valid prd`)

	var stdout, stderr bytes.Buffer
	code := runProductAudit([]string{"--effort-root", dir, "--json"}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("expected audit to fail on invalid PRD, got code=%d", code)
	}
	var result productAuditResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	prdSection := findAuditSection(t, result, "prd")
	if prdSection.Status != "fail" {
		t.Fatalf("expected prd section to fail, got %q", prdSection.Status)
	}
	found := false
	for _, f := range prdSection.Findings {
		if f.Code == "parse_error" && f.Path == "prd.md" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected parse_error finding in prd section, got: %#v", prdSection.Findings)
	}
}

func TestProductAuditSurfacesParseErrorInADRSection(t *testing.T) {
	dir := writeValidTestEffort(t)
	writeTestFile(t, dir, "adrs/index.json", `not valid json`)

	var stdout, stderr bytes.Buffer
	code := runProductAudit([]string{"--effort-root", dir, "--json"}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("expected audit to fail on invalid ADR index, got code=%d", code)
	}
	var result productAuditResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	adrSection := findAuditSection(t, result, "adr")
	if adrSection.Status != "fail" {
		t.Fatalf("expected adr section to fail, got %q", adrSection.Status)
	}
	found := false
	for _, f := range adrSection.Findings {
		if f.Code == "parse_error" && f.Path == "adrs/index.json" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected parse_error finding in adr section, got: %#v", adrSection.Findings)
	}
}

func TestProductAuditSurfacesParseErrorInTDDSection(t *testing.T) {
	dir := writeValidTestEffort(t)
	writeTestFile(t, dir, "tdd.json", `{not valid json`)

	var stdout, stderr bytes.Buffer
	code := runProductAudit([]string{"--effort-root", dir, "--json"}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("expected audit to fail on invalid TDD, got code=%d", code)
	}
	var result productAuditResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	tddSection := findAuditSection(t, result, "tdd")
	if tddSection.Status != "fail" {
		t.Fatalf("expected tdd section to fail, got %q", tddSection.Status)
	}
	found := false
	for _, f := range tddSection.Findings {
		if f.Code == "parse_error" && f.Path == "tdd.json" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected parse_error finding in tdd section, got: %#v", tddSection.Findings)
	}
}

func TestProductAuditDoesNotChangeLintBehavior(t *testing.T) {
	dir := writeValidTestEffort(t)

	var lintStdout, lintStderr bytes.Buffer
	lintCode := runProductLint([]string{"--effort-root", dir, "--json"}, &lintStdout, &lintStderr, nil)
	var auditStdout, auditStderr bytes.Buffer
	auditCode := runProductAudit([]string{"--effort-root", dir, "--json"}, &auditStdout, &auditStderr, nil)

	if lintCode != 0 {
		t.Fatalf("expected lint to pass, got code=%d stdout=%s", lintCode, lintStdout.String())
	}
	if auditCode != 0 {
		t.Fatalf("expected audit to pass, got code=%d stdout=%s", auditCode, auditStdout.String())
	}

	var lintResult productworkflow.LintSummaryResult
	if err := json.Unmarshal(lintStdout.Bytes(), &lintResult); err != nil {
		t.Fatalf("lint output should be valid JSON: %v", err)
	}
	var auditResult productAuditResult
	if err := json.Unmarshal(auditStdout.Bytes(), &auditResult); err != nil {
		t.Fatalf("audit output should be valid JSON: %v", err)
	}

	if lintResult.Status != auditResult.Status {
		t.Fatalf("lint status %q != audit status %q", lintResult.Status, auditResult.Status)
	}
	if len(lintResult.Findings) != len(auditResult.Findings) {
		t.Fatalf("lint findings count %d != audit findings count %d", len(lintResult.Findings), len(auditResult.Findings))
	}

	lintCode2 := runProductLint([]string{"--effort-root", dir, "--json"}, &lintStdout, &lintStderr, nil)
	if lintCode != lintCode2 {
		t.Fatalf("lint is not deterministic: first=%d second=%d", lintCode, lintCode2)
	}
}

func TestProductLintTextOutputIsActionable(t *testing.T) {
	dir := writeValidTestEffort(t)
	var jsonStdout, jsonStderr bytes.Buffer
	code := runProductLint([]string{"--effort-root", dir, "--json"}, &jsonStdout, &jsonStderr, nil)
	if code != 0 {
		t.Fatalf("expected pass, got code=%d stdout=%s stderr=%s", code, jsonStdout.String(), jsonStderr.String())
	}
	var jsonResult productworkflow.LintSummaryResult
	if err := json.Unmarshal(jsonStdout.Bytes(), &jsonResult); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if strings.TrimSpace(jsonResult.NextValidAction) == "" {
		t.Fatal("expected next_valid_action in lint json output")
	}

	var textStdout, textStderr bytes.Buffer
	code = runProductLint([]string{"--effort-root", dir}, &textStdout, &textStderr, nil)
	if code != 0 {
		t.Fatalf("expected pass text run, got code=%d stdout=%s stderr=%s", code, textStdout.String(), textStderr.String())
	}
	if !strings.Contains(textStdout.String(), "next: "+jsonResult.NextValidAction) {
		t.Fatalf("expected text next-action parity, got:\n%s", textStdout.String())
	}
}

func TestProductLintFailNextValidActionParity(t *testing.T) {
	dir := writeValidTestEffort(t)
	writeTestFile(t, dir, "tdd.json", `{not valid json`)
	var jsonStdout, jsonStderr bytes.Buffer
	code := runProductLint([]string{"--effort-root", dir, "--json"}, &jsonStdout, &jsonStderr, nil)
	if code != 1 {
		t.Fatalf("expected fail, got code=%d stdout=%s stderr=%s", code, jsonStdout.String(), jsonStderr.String())
	}
	var jsonResult productworkflow.LintSummaryResult
	if err := json.Unmarshal(jsonStdout.Bytes(), &jsonResult); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	var textStdout, textStderr bytes.Buffer
	code = runProductLint([]string{"--effort-root", dir}, &textStdout, &textStderr, nil)
	if code != 1 {
		t.Fatalf("expected fail text run, got code=%d stdout=%s stderr=%s", code, textStdout.String(), textStderr.String())
	}
	if !strings.Contains(textStdout.String(), "next: "+jsonResult.NextValidAction) {
		t.Fatalf("expected text next-action parity, got:\n%s", textStdout.String())
	}
}

func TestProductAuditFailNextValidActionParity(t *testing.T) {
	dir := writeValidTestEffort(t)
	writeTestFile(t, dir, "tdd.json", `{not valid json`)
	var jsonStdout, jsonStderr bytes.Buffer
	code := runProductAudit([]string{"--effort-root", dir, "--json"}, &jsonStdout, &jsonStderr, nil)
	if code != 1 {
		t.Fatalf("expected fail, got code=%d stdout=%s stderr=%s", code, jsonStdout.String(), jsonStderr.String())
	}
	var jsonResult productAuditResult
	if err := json.Unmarshal(jsonStdout.Bytes(), &jsonResult); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if strings.TrimSpace(jsonResult.NextValidAction) == "" {
		t.Fatal("expected next_valid_action in audit json output")
	}
	var textStdout, textStderr bytes.Buffer
	code = runProductAudit([]string{"--effort-root", dir}, &textStdout, &textStderr, nil)
	if code != 1 {
		t.Fatalf("expected fail text run, got code=%d stdout=%s stderr=%s", code, textStdout.String(), textStderr.String())
	}
	if !strings.Contains(textStdout.String(), "next: "+jsonResult.NextValidAction) {
		t.Fatalf("expected text next-action parity, got:\n%s", textStdout.String())
	}
}

func TestProductAuditUsageMirrorsLint(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runProductAudit([]string{}, &stdout, &stderr, nil)
	if code != 2 {
		t.Fatalf("expected code 2 for missing --effort-root, got %d", code)
	}
}

func TestProductAuditHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runProductAudit([]string{"-h"}, &stdout, &stderr, nil)
	if code != 0 {
		t.Fatalf("expected code 0 for -h, got %d", code)
	}
}

func TestProductAuditTelemetryName(t *testing.T) {
	name := commandTelemetryName([]string{"product", "audit", "--effort-root", "."})
	if name != "product:audit" {
		t.Fatalf("expected telemetry name product:audit, got %q", name)
	}
}

func TestProductAuditFailsWithPRDBrokenLinkWhenEvidenceRefMissing(t *testing.T) {
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
- **evidence_refs**: ["EV-001", "EV-MISSING"]
- **tdd_obligations**: ["TDD-001"]
- **open_questions**: []
`)

	var stdout, stderr bytes.Buffer
	code := runProductAudit([]string{"--effort-root", dir, "--json"}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("expected audit to fail with broken PRD evidence ref, got code=%d", code)
	}
	var result productAuditResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	prdSection := findAuditSection(t, result, "prd")
	if prdSection.Status != "fail" {
		t.Fatalf("expected prd section to fail, got %q", prdSection.Status)
	}
	found := false
	for _, f := range prdSection.Findings {
		if f.Code == "broken_link" && f.Path == "prd.md" && strings.Contains(f.Message, "EV-MISSING") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected broken_link finding in prd section mentioning EV-MISSING, got: %#v", prdSection.Findings)
	}
}

func TestProductLintDetectsADRWithMissingEvidenceRef(t *testing.T) {
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
	if code != 1 {
		t.Fatalf("expected lint to fail with broken ADR evidence ref, got code=%d", code)
	}
	var result productworkflow.LintSummaryResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	found := false
	for _, f := range result.Findings {
		if f.Code == "broken_link" && strings.Contains(f.Path, "ADR-001") && strings.Contains(f.Message, "EV-MISSING") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected broken_link finding for ADR evidence ref EV-MISSING, got: %#v", result.Findings)
	}
}

func TestProductLintDetectsADRWithMissingTDDObligation(t *testing.T) {
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
	if code != 1 {
		t.Fatalf("expected lint to fail with broken ADR TDD obligation ref, got code=%d", code)
	}
	var result productworkflow.LintSummaryResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	found := false
	for _, f := range result.Findings {
		if f.Code == "broken_link" && strings.Contains(f.Path, "ADR-001") && strings.Contains(f.Message, "TDD-MISSING") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected broken_link finding for ADR TDD ref TDD-MISSING, got: %#v", result.Findings)
	}
}

func TestProductLintDetectsADRWithMissingPRDRef(t *testing.T) {
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
	if code != 1 {
		t.Fatalf("expected lint to fail with broken ADR PRD ref, got code=%d", code)
	}
	var result productworkflow.LintSummaryResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	found := false
	for _, f := range result.Findings {
		if f.Code == "broken_link" && strings.Contains(f.Path, "ADR-001") && strings.Contains(f.Message, "REQ-MISSING") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected broken_link finding for ADR PRD ref REQ-MISSING, got: %#v", result.Findings)
	}
}

func TestProductAuditFailsOnADRMissingEvidenceRef(t *testing.T) {
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
	code := runProductAudit([]string{"--effort-root", dir, "--json"}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("expected audit to fail with broken ADR evidence ref, got code=%d", code)
	}
	var result productAuditResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	adrSection := findAuditSection(t, result, "adr")
	if adrSection.Status != "fail" {
		t.Fatalf("expected adr section to fail, got %q", adrSection.Status)
	}
	found := false
	for _, f := range adrSection.Findings {
		if f.Code == "broken_link" && strings.Contains(f.Message, "EV-MISSING") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected broken_link finding in adr section mentioning EV-MISSING, got: %#v", adrSection.Findings)
	}
}

func TestProductAuditFailsOnADRMissingTDDObligationRef(t *testing.T) {
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
	code := runProductAudit([]string{"--effort-root", dir, "--json"}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("expected audit to fail with broken ADR TDD obligation ref, got code=%d", code)
	}
	var result productAuditResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	adrSection := findAuditSection(t, result, "adr")
	if adrSection.Status != "fail" {
		t.Fatalf("expected adr section to fail, got %q", adrSection.Status)
	}
	found := false
	for _, f := range adrSection.Findings {
		if f.Code == "broken_link" && strings.Contains(f.Message, "TDD-MISSING") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected broken_link finding in adr section mentioning TDD-MISSING, got: %#v", adrSection.Findings)
	}
}

func TestProductAuditFailsOnADRMissingPRDRef(t *testing.T) {
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
	code := runProductAudit([]string{"--effort-root", dir, "--json"}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("expected audit to fail with broken ADR PRD ref, got code=%d", code)
	}
	var result productAuditResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	adrSection := findAuditSection(t, result, "adr")
	if adrSection.Status != "fail" {
		t.Fatalf("expected adr section to fail, got %q", adrSection.Status)
	}
	found := false
	for _, f := range adrSection.Findings {
		if f.Code == "broken_link" && strings.Contains(f.Message, "REQ-MISSING") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected broken_link finding in adr section mentioning REQ-MISSING, got: %#v", adrSection.Findings)
	}
}

func TestProductLintAcceptanceRejectsAcceptedPRDWithoutAcceptanceMetadata(t *testing.T) {
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
	if code != 1 {
		t.Fatalf("expected lint failure, got %d", code)
	}
	var result productworkflow.LintSummaryResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	found := false
	for _, f := range result.Findings {
		if f.Path == "prd.md" && f.Code == "parse_error" && strings.Contains(f.Message, "missing accepted_at") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected PRD accepted metadata parse error, got %#v", result.Findings)
	}
}

func TestProductAuditSupersessionDetectsNonReciprocalADRLinks(t *testing.T) {
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
coverage_tier: unit
supersedes:
  - ADR-002
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
	writeTestFile(t, dir, "adrs/index.json", `{"schema_version":"v1","effort_id":"test-effort","adrs":[{"adr_id":"ADR-001","title":"Test ADR 1","status":"accepted","path":"ADR-001-test.md","coverage_tier":"unit","prd_refs":["REQ-TEST-001"]},{"adr_id":"ADR-002","title":"Test ADR 2","status":"accepted","path":"ADR-002-test.md","coverage_tier":"unit","prd_refs":["REQ-TEST-001"]}]}`)
	writeTestFile(t, dir, "adrs/ADR-002-test.md", `---
schema_version: idiom.adr.v1
adr_id: ADR-002
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

# ADR-002: Test
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
	code := runProductAudit([]string{"--effort-root", dir, "--json"}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("expected audit failure, got %d", code)
	}
	var result productAuditResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	adrSection := findAuditSection(t, result, "adr")
	found := false
	for _, f := range adrSection.Findings {
		if f.Code == "adr_supersession_non_reciprocal" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected supersession non-reciprocal finding, got %#v", adrSection.Findings)
	}
}

func TestProductAuditSupersessionDetectsBrokenADRIndexLinks(t *testing.T) {
	dir := writeValidTestEffort(t)
	writeTestFile(t, dir, "adrs/index.json", `{"schema_version":"v1","effort_id":"test-effort","adrs":[{"adr_id":"ADR-001","title":"Test ADR","status":"accepted","path":"ADR-001-test.md","coverage_tier":"unit","prd_refs":["REQ-TEST-001"],"supersedes":["ADR-MISSING"]}]}`)

	var stdout, stderr bytes.Buffer
	code := runProductAudit([]string{"--effort-root", dir, "--json"}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("expected audit failure, got %d", code)
	}
	var result productAuditResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	adrSection := findAuditSection(t, result, "adr")
	found := false
	for _, f := range adrSection.Findings {
		if f.Path == "adrs/index.json" && f.Code == "parse_error" && strings.Contains(f.Message, "ADR-MISSING") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected ADR index supersession finding, got %#v", adrSection.Findings)
	}
}

func TestProductLintFailsOnMissingMustPriorityPRDTDDObligation(t *testing.T) {
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
	if code != 1 {
		t.Fatalf("expected lint to fail on missing must-priority PRD TDD obligation, got code=%d", code)
	}
	var result productworkflow.LintSummaryResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	found := false
	for _, f := range result.Findings {
		if f.Code == "broken_link" && f.Path == "tdd.json" && strings.Contains(f.Message, "TDD-MISSING") && strings.Contains(f.Message, "REQ-TEST-001") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected broken_link finding for tdd.json mentioning TDD-MISSING, got: %#v", result.Findings)
	}
}

func TestProductAuditFailsOnMissingMustPriorityPRDTDDObligation(t *testing.T) {
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
	code := runProductAudit([]string{"--effort-root", dir, "--json"}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("expected audit to fail on missing PRD TDD obligation, got code=%d", code)
	}
	var result productAuditResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	tddSection := findAuditSection(t, result, "tdd")
	if tddSection.Status != "fail" {
		t.Fatalf("expected tdd section to fail, got %q", tddSection.Status)
	}
	found := false
	for _, f := range tddSection.Findings {
		if f.Code == "broken_link" && strings.Contains(f.Message, "TDD-MISSING") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected broken_link finding in tdd section mentioning TDD-MISSING, got: %#v", tddSection.Findings)
	}
}

func TestProductLintAcceptsHistoricalLedgerSatisfiedTDDObligationsForPRD(t *testing.T) {
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
	if code != 0 {
		t.Fatalf("expected lint to pass with historical TDD obligation in PRD, got code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
}

func TestProductLintSkipsNonMustPriorityPRDTDDObligations(t *testing.T) {
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
	if code != 0 {
		t.Fatalf("expected lint to pass when non-must priority has missing TDD obligation, got code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
}

func TestProductStatusJSONOnDurableFixture(t *testing.T) {
	root := repoRoot(t)
	effortRoot := filepath.Join(root, "docs", "product-workflows", "efforts", "closed", "prd-adr-brief-workflow")

	ws := loadDurableFixtureWorkflowState(t)

	var stdout, stderr bytes.Buffer
	code := runProductStatus([]string{"--effort-root", effortRoot, "--json"}, &stdout, &stderr, nil)
	if code != 0 {
		t.Fatalf("expected status to pass on durable fixture, got code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	if result["effort_id"] != ws["effort_id"] {
		t.Fatalf("expected effort_id %v, got %v", ws["effort_id"], result["effort_id"])
	}
	if result["task_id"] != ws["task_id"] {
		t.Fatalf("expected task_id %v, got %v", ws["task_id"], result["task_id"])
	}
	if result["current_state"] != ws["current_state"] {
		t.Fatalf("expected current_state %v, got %v", ws["current_state"], result["current_state"])
	}
	ctx, _ := ws["context"].(map[string]any)
	if result["context_pack_id"] != ctx["context_pack_id"] {
		t.Fatalf("expected context_pack_id %v, got %v", ctx["context_pack_id"], result["context_pack_id"])
	}
	requiredFields := []string{
		"audit_status", "effort_root", "effort_id", "task_id",
		"status_bucket", "current_phase", "current_state",
		"state_revision", "last_event_id", "context_pack_id",
		"freshness_state", "checked_file_count", "sections",
		"blockers", "open_obligations", "next_valid_actions", "metrics",
	}
	for _, field := range requiredFields {
		if _, ok := result[field]; !ok {
			t.Fatalf("expected field %q in JSON output", field)
		}
	}
	if _, ok := result["operator"]; ok {
		t.Fatalf("default status JSON must not include operator view: %#v", result["operator"])
	}
}

func TestProductStatusMetrics(t *testing.T) {
	dir := writeValidTestEffort(t)
	writeTestFile(t, dir, "reports/test-implementation.json", validTestImplementationReportJSON())
	writeTestFile(t, dir, "reports/test-review.json", validTestReviewReportJSON())
	writeTestFile(t, dir, "reports/test-investigation.json", validTestTestInvestigationReportJSON())

	var stdout, stderr bytes.Buffer
	code := runProductStatus([]string{"--effort-root", dir, "--json"}, &stdout, &stderr, nil)
	if code != 0 {
		t.Fatalf("expected status to pass, got code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var result productworkflow.ProductStatusView
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	if result.Metrics == nil {
		t.Fatal("expected metrics in product status JSON")
	}
	if result.Metrics.CurrentPhase == "" || result.Metrics.CurrentState == "" {
		t.Fatalf("expected metrics phase/state, got %#v", result.Metrics)
	}
}

func TestProductStatusMetricsFailsClosedOnUnknownGateStatus(t *testing.T) {
	dir := writeValidTestEffort(t)
	writeTestFile(t, dir, "reports/test-implementation.json", validTestImplementationReportJSON())
	writeTestFile(t, dir, "reports/test-review.json", validTestReviewReportJSON())
	writeTestFile(t, dir, "reports/test-investigation.json", validTestTestInvestigationReportJSON())
	writeTestFile(t, dir, "workflow_state.json", strings.Replace(
		validTestWorkflowStateJSON(),
		`"coverage":{"coverage_tier":"unit"}`,
		`"coverage":{"coverage_tier":"unit","gate_results":[{"gate":"metrics","status":"surprise"}]}`,
		1,
	))

	var stdout, stderr bytes.Buffer
	code := runProductStatus([]string{"--effort-root", dir, "--json"}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("expected status to fail closed, got code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var result productworkflow.ProductStatusView
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	if result.AuditStatus != "fail" {
		t.Fatalf("expected audit_status fail, got %q", result.AuditStatus)
	}
	metricsSection := findStatusSection(t, result.Sections, "metrics")
	if metricsSection.Status != "fail" || metricsSection.FindingCount != 1 {
		t.Fatalf("expected metrics section failure, got %#v", metricsSection)
	}
	if result.Metrics != nil {
		t.Fatalf("expected metrics omitted on failure, got %#v", result.Metrics)
	}
}

func TestProductStatusTextIncludesKeyFields(t *testing.T) {
	root := repoRoot(t)
	effortRoot := filepath.Join(root, "docs", "product-workflows", "efforts", "closed", "prd-adr-brief-workflow")

	ws := loadDurableFixtureWorkflowState(t)
	currentState, _ := ws["current_state"].(string)

	var stdout, stderr bytes.Buffer
	code := runProductStatus([]string{"--effort-root", effortRoot}, &stdout, &stderr, nil)
	if code != 0 {
		t.Fatalf("expected status to pass on durable fixture, got code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	output := stdout.String()
	for _, want := range []string{"prd-adr-brief-workflow", currentState, "audit:", "next_actions:", "metrics: phase=", "next:"} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected text output to contain %q, got:\n%s", want, output)
		}
	}
	if strings.Contains(output, "continuation_actions") {
		t.Fatalf("default status text must not include operator continuation actions, got:\n%s", output)
	}
}

func TestProductStatusOperatorViewJSON(t *testing.T) {
	dir := writeValidTestEffort(t)
	ws := strings.Replace(
		validTestWorkflowStateJSON(),
		`"current_state":"implementing"`,
		`"current_state":"planned"`,
		1,
	)
	ws = strings.Replace(
		ws,
		`"next_valid_actions":["complete"]`,
		`"next_valid_actions":["submit_for_human_verification"]`,
		1,
	)
	writeTestFile(t, dir, "workflow_state.json", ws)

	var stdout, stderr bytes.Buffer
	code := runProductStatus([]string{"--effort-root", dir, "--operator-view", "--json"}, &stdout, &stderr, nil)
	if code != 0 {
		t.Fatalf("expected operator status to pass, got code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}

	var result productworkflow.ProductOperatorStatusView
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("operator output should be valid JSON: %v\n%s", err, stdout.String())
	}
	if result.SchemaVersion != "idiom.product_operator_status.v1" {
		t.Fatalf("expected operator schema version, got %q", result.SchemaVersion)
	}
	if result.ContinuationAuthority != "workflow_state" {
		t.Fatalf("expected workflow_state continuation authority, got %q", result.ContinuationAuthority)
	}
	if len(result.ArtifactPaths) != 1 || result.ArtifactPaths[0].Name != "events" || result.ArtifactPaths[0].Path != "events.jsonl" {
		t.Fatalf("expected artifact paths from workflow_state, got %#v", result.ArtifactPaths)
	}
	if len(result.NextValidActions) != 1 {
		t.Fatalf("expected typed continuation actions for planned state, got %#v", result.NextValidActions)
	}
	var action productworkflow.ProductOperatorAction
	for _, candidate := range result.NextValidActions {
		if candidate.ID == "submit_for_human_verification" {
			action = candidate
			break
		}
	}
	if action.ID != "submit_for_human_verification" || action.Label != "submit for human verification" || action.Owner != "planner" || !action.Dispatchable || !action.RunnableNow {
		t.Fatalf("expected typed human-verification transition action, got %#v", action)
	}
}

func TestProductStatusOperatorViewBlocksLegacyStartImplementationWithoutHumanVerification(t *testing.T) {
	dir := writeValidTestEffort(t)
	ws := strings.Replace(
		validTestWorkflowStateJSON(),
		`"current_state":"implementing"`,
		`"current_state":"planned"`,
		1,
	)
	ws = strings.Replace(
		ws,
		`"next_valid_actions":["complete"]`,
		`"next_valid_actions":["start_implementation"]`,
		1,
	)
	writeTestFile(t, dir, "workflow_state.json", ws)

	var stdout, stderr bytes.Buffer
	code := runProductStatus([]string{"--effort-root", dir, "--operator-view", "--json"}, &stdout, &stderr, nil)
	if code != 0 {
		t.Fatalf("expected operator status to preserve status exit behavior, got code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}

	var result productworkflow.ProductOperatorStatusView
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("operator output should be valid JSON: %v\n%s", err, stdout.String())
	}
	action := findProductOperatorAction(t, result.NextValidActions, "start_implementation")
	if action.Dispatchable || action.RunnableNow {
		t.Fatalf("legacy start_implementation must be blocked until human verification, got %#v", action)
	}
	if !containsString(action.BlockedBy, "human_verification_required") {
		t.Fatalf("expected human_verification_required blocker, got %#v", action)
	}
}

func TestProductStatusOperatorViewIncludesRecoveryPacket(t *testing.T) {
	dir := writeValidTestEffort(t)
	writeTestFile(t, dir, "references/recovery_packet.json", productWorkflowTestRecoveryPacketJSON(t))
	ws := strings.Replace(
		validTestWorkflowStateJSON(),
		`"artifact_paths":{"events":"events.jsonl"}`,
		`"artifact_paths":{"events":"events.jsonl","recovery_packet":"references/recovery_packet.json"}`,
		1,
	)
	writeTestFile(t, dir, "workflow_state.json", ws)

	var stdout, stderr bytes.Buffer
	code := runProductStatus([]string{"--effort-root", dir, "--operator-view", "--json"}, &stdout, &stderr, nil)
	if code != 0 {
		t.Fatalf("expected operator status to pass, got code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var result productworkflow.ProductOperatorStatusView
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("operator output should be valid JSON: %v\n%s", err, stdout.String())
	}
	if result.Recovery == nil || result.Recovery.Status != "ready" || result.Recovery.CandidateAtomCount != 1 {
		t.Fatalf("expected recovery status projection, got %#v", result.Recovery)
	}
	workflowAction := findProductOperatorAction(t, result.NextValidActions, "complete")
	if workflowAction.Dispatchable || workflowAction.RunnableNow {
		t.Fatalf("expected workflow action blocked while recovery is open, got %#v", workflowAction)
	}
	recoveryAction := findProductOperatorAction(t, result.NextValidActions, "recovery_review_decomposition")
	if recoveryAction.Dispatchable || !recoveryAction.RunnableNow || recoveryAction.CommandHint != "idiom recover status --packet references/recovery_packet.json" {
		t.Fatalf("expected runnable recovery inspect action, got %#v", recoveryAction)
	}

	stdout.Reset()
	stderr.Reset()
	code = runProductStatus([]string{"--effort-root", dir, "--operator-view"}, &stdout, &stderr, nil)
	if code != 0 {
		t.Fatalf("expected operator status text to pass, got code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	text := stdout.String()
	for _, want := range []string{
		"recovery: ready next=review_decomposition candidate_atoms=1 packet=references/recovery_packet.json",
		"[recovery_packet_open] review recovery packet before continuing workflow dispatch",
		"command_hint=idiom recover status --packet references/recovery_packet.json",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected operator recovery text to contain %q, got:\n%s", want, text)
		}
	}
}

func TestProductStatusOperatorViewTextFailsClosedOnStaleContext(t *testing.T) {
	dir := writeValidTestEffort(t)
	ws := strings.Replace(validTestWorkflowStateJSON(), `"freshness_state":"current"`, `"freshness_state":"stale"`, 1)
	ws = strings.Replace(ws, `"next_valid_actions":["complete"]`, `"next_valid_actions":["submit_for_human_verification"]`, 1)
	writeTestFile(t, dir, "workflow_state.json", ws)

	var stdout, stderr bytes.Buffer
	code := runProductStatus([]string{"--effort-root", dir, "--operator-view"}, &stdout, &stderr, nil)
	if code != 0 {
		t.Fatalf("expected status exit code to preserve existing behavior, got code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	text := stdout.String()
	for _, want := range []string{
		"product operator status:",
		"authority: workflow_state",
		"[stale_context] workflow freshness_state \"stale\" is not current",
		"next_valid_actions (1):",
		"dispatchable=false",
		"runnable_now=false",
		"blocked_by=stale_context",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected operator text to contain %q, got:\n%s", want, text)
		}
	}
}

func TestProductStatusOperatorViewUsesWorkflowActionsOnAuditFail(t *testing.T) {
	dir := writeValidTestEffort(t)
	writeTestFile(t, dir, "tdd.json", `{not valid json`)

	var stdout, stderr bytes.Buffer
	code := runProductStatus([]string{"--effort-root", dir, "--operator-view", "--json"}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("expected operator status audit fail code 1, got code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}

	var result productworkflow.ProductOperatorStatusView
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("operator audit-fail output should be valid JSON: %v\n%s", err, stdout.String())
	}
	if len(result.NextValidActions) != 1 {
		t.Fatalf("expected durable workflow action only, got %#v", result.NextValidActions)
	}
	action := result.NextValidActions[0]
	if action.ID != "complete" || action.Action != "complete" {
		t.Fatalf("expected operator view to preserve workflow_state next action, got %#v", action)
	}
	if action.Dispatchable || action.RunnableNow {
		t.Fatalf("expected audit failure to block workflow action, got %#v", action)
	}
	if !strings.Contains(strings.Join(action.BlockedBy, ","), "audit_not_passing") {
		t.Fatalf("expected audit_not_passing blocker, got %#v", action)
	}
	if strings.Contains(action.Action, "fix product workflow findings") || strings.Contains(action.ID, "fix_product_workflow_findings") {
		t.Fatalf("operator view must not use CLI remediation prose as continuation authority, got %#v", action)
	}
}

func TestProductStatusJSONDoesNotIncludeOperatorContinuationView(t *testing.T) {
	dir := writeValidTestEffort(t)

	var stdout, stderr bytes.Buffer
	code := runProductStatus([]string{"--effort-root", dir, "--json"}, &stdout, &stderr, nil)
	if code != 0 {
		t.Fatalf("expected status to pass, got code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}

	var raw map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &raw); err != nil {
		t.Fatalf("status JSON should decode to raw map: %v\n%s", err, stdout.String())
	}
	if _, ok := raw["operator"]; ok {
		t.Fatalf("default status JSON must not include operator view: %s", stdout.String())
	}
}

func TestProductStatusOperatorNoRuntimeMutation(t *testing.T) {
	dir := writeValidTestEffort(t)
	before, err := os.ReadFile(filepath.Join(dir, "workflow_state.json"))
	if err != nil {
		t.Fatalf("read workflow_state.json before status: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := runProductStatus([]string{"--effort-root", dir, "--json"}, &stdout, &stderr, nil)
	if code != 0 {
		t.Fatalf("expected status to pass, got code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}

	after, err := os.ReadFile(filepath.Join(dir, "workflow_state.json"))
	if err != nil {
		t.Fatalf("read workflow_state.json after status: %v", err)
	}
	if string(after) != string(before) {
		t.Fatal("product status must not mutate workflow_state.json")
	}
	if _, err := os.Stat(filepath.Join(dir, ".idiom")); !os.IsNotExist(err) {
		t.Fatalf("product status must not create .idiom runtime state, err=%v", err)
	}
}

func TestProductLintNextValidActionParity(t *testing.T) {
	dir := t.TempDir()

	var jsonStdout, jsonStderr bytes.Buffer
	jsonCode := runProductLint([]string{"--effort-root", dir, "--json"}, &jsonStdout, &jsonStderr, nil)
	if jsonCode != 1 {
		t.Fatalf("expected lint failure exit code 1, got code=%d stdout=%s stderr=%s", jsonCode, jsonStdout.String(), jsonStderr.String())
	}
	var out productworkflow.LintSummaryResult
	if err := json.Unmarshal(jsonStdout.Bytes(), &out); err != nil {
		t.Fatalf("expected valid lint JSON: %v", err)
	}
	if strings.TrimSpace(out.NextValidAction) == "" {
		t.Fatalf("expected next_valid_action in JSON, got %#v", out)
	}

	var textStdout, textStderr bytes.Buffer
	textCode := runProductLint([]string{"--effort-root", dir}, &textStdout, &textStderr, nil)
	if textCode != jsonCode {
		t.Fatalf("expected text and JSON exit code parity, got text=%d json=%d", textCode, jsonCode)
	}
	text := textStdout.String()
	if !strings.Contains(text, "  status: FAIL") {
		t.Fatalf("expected deterministic FAIL status label, got:\n%s", text)
	}
	if !strings.Contains(text, "  [missing_file] workflow_state.json: required file not found") {
		t.Fatalf("expected typed finding identity in text output, got:\n%s", text)
	}
	if !strings.Contains(text, "  next: "+out.NextValidAction) {
		t.Fatalf("expected text next action parity with JSON value %q, got:\n%s", out.NextValidAction, text)
	}
}

func TestProductAuditNextValidActionParity(t *testing.T) {
	dir := t.TempDir()

	var jsonStdout, jsonStderr bytes.Buffer
	jsonCode := runProductAudit([]string{"--effort-root", dir, "--json"}, &jsonStdout, &jsonStderr, nil)
	if jsonCode != 1 {
		t.Fatalf("expected audit failure exit code 1, got code=%d stdout=%s stderr=%s", jsonCode, jsonStdout.String(), jsonStderr.String())
	}
	var out productAuditResult
	if err := json.Unmarshal(jsonStdout.Bytes(), &out); err != nil {
		t.Fatalf("expected valid audit JSON: %v", err)
	}
	if strings.TrimSpace(out.NextValidAction) == "" {
		t.Fatalf("expected next_valid_action in JSON, got %#v", out)
	}

	var textStdout, textStderr bytes.Buffer
	textCode := runProductAudit([]string{"--effort-root", dir}, &textStdout, &textStderr, nil)
	if textCode != jsonCode {
		t.Fatalf("expected text and JSON exit code parity, got text=%d json=%d", textCode, jsonCode)
	}
	text := textStdout.String()
	if !strings.Contains(text, "  status: FAIL") {
		t.Fatalf("expected deterministic FAIL status label, got:\n%s", text)
	}
	if !strings.Contains(text, "  [missing_file] workflow_state.json: required file not found") {
		t.Fatalf("expected typed finding identity in text output, got:\n%s", text)
	}
	if !strings.Contains(text, "  next: "+out.NextValidAction) {
		t.Fatalf("expected text next action parity with JSON value %q, got:\n%s", out.NextValidAction, text)
	}
}

func TestProductStatusTextOutputIsActionable(t *testing.T) {
	t.Run("pass", func(t *testing.T) {
		root := repoRoot(t)
		effortRoot := filepath.Join(root, "docs", "product-workflows", "efforts", "closed", "prd-adr-brief-workflow")

		var jsonStdout, jsonStderr bytes.Buffer
		jsonCode := runProductStatus([]string{"--effort-root", effortRoot, "--json"}, &jsonStdout, &jsonStderr, nil)
		if jsonCode != 0 {
			t.Fatalf("expected pass status code 0, got code=%d stdout=%s stderr=%s", jsonCode, jsonStdout.String(), jsonStderr.String())
		}
		var out productworkflow.ProductStatusView
		if err := json.Unmarshal(jsonStdout.Bytes(), &out); err != nil {
			t.Fatalf("expected valid status JSON: %v", err)
		}
		if len(out.NextValidActions) == 0 || strings.TrimSpace(out.NextValidActions[0]) == "" {
			t.Fatalf("expected next_valid_actions for pass output, got %#v", out.NextValidActions)
		}

		var textStdout, textStderr bytes.Buffer
		textCode := runProductStatus([]string{"--effort-root", effortRoot}, &textStdout, &textStderr, nil)
		if textCode != 0 {
			t.Fatalf("expected pass text code 0, got code=%d stdout=%s stderr=%s", textCode, textStdout.String(), textStderr.String())
		}
		text := textStdout.String()
		if !strings.Contains(text, "  status: OK") {
			t.Fatalf("expected deterministic OK status label, got:\n%s", text)
		}
		if !strings.Contains(text, "  next: "+out.NextValidActions[0]) {
			t.Fatalf("expected text next action parity with JSON value %q, got:\n%s", out.NextValidActions[0], text)
		}
	})

	t.Run("audit_fail", func(t *testing.T) {
		dir := writeValidTestEffort(t)
		writeTestFile(t, dir, "tdd.json", `{not valid json`)

		var jsonStdout, jsonStderr bytes.Buffer
		jsonCode := runProductStatus([]string{"--effort-root", dir, "--json"}, &jsonStdout, &jsonStderr, nil)
		if jsonCode != 1 {
			t.Fatalf("expected audit-fail status code 1, got code=%d stdout=%s stderr=%s", jsonCode, jsonStdout.String(), jsonStderr.String())
		}
		var out productworkflow.ProductStatusView
		if err := json.Unmarshal(jsonStdout.Bytes(), &out); err != nil {
			t.Fatalf("expected valid status JSON: %v", err)
		}
		if len(out.NextValidActions) == 0 || strings.TrimSpace(out.NextValidActions[0]) == "" {
			t.Fatalf("expected next_valid_actions for fail output, got %#v", out.NextValidActions)
		}
		if !strings.Contains(out.NextValidActions[0], "fix product workflow findings") {
			t.Fatalf("expected current failure remediation as first next action, got %#v", out.NextValidActions)
		}
		if out.NextValidActions[0] == "complete" {
			t.Fatalf("expected not to surface stale workflow progression as first next action, got %#v", out.NextValidActions)
		}

		var textStdout, textStderr bytes.Buffer
		textCode := runProductStatus([]string{"--effort-root", dir}, &textStdout, &textStderr, nil)
		if textCode != 1 {
			t.Fatalf("expected audit-fail text code 1, got code=%d stdout=%s stderr=%s", textCode, textStdout.String(), textStderr.String())
		}
		text := textStdout.String()
		if !strings.Contains(text, "  status: FAILED") {
			t.Fatalf("expected deterministic FAILED status label, got:\n%s", text)
		}
		if !strings.Contains(text, "    [metrics_error] ") {
			t.Fatalf("expected typed blocker identity in text output, got:\n%s", text)
		}
		if !strings.Contains(text, "  next: "+out.NextValidActions[0]) {
			t.Fatalf("expected text next action parity with JSON value %q, got:\n%s", out.NextValidActions[0], text)
		}
	})

	t.Run("workflow_state_error", func(t *testing.T) {
		dir := t.TempDir()

		var jsonStdout, jsonStderr bytes.Buffer
		jsonCode := runProductStatus([]string{"--effort-root", dir, "--json"}, &jsonStdout, &jsonStderr, nil)
		if jsonCode != 1 {
			t.Fatalf("expected error status code 1, got code=%d stdout=%s stderr=%s", jsonCode, jsonStdout.String(), jsonStderr.String())
		}
		var out productworkflow.ProductStatusView
		if err := json.Unmarshal(jsonStdout.Bytes(), &out); err != nil {
			t.Fatalf("expected valid status JSON: %v", err)
		}
		if len(out.NextValidActions) == 0 || strings.TrimSpace(out.NextValidActions[0]) == "" {
			t.Fatalf("expected next_valid_actions for error output, got %#v", out.NextValidActions)
		}

		var textStdout, textStderr bytes.Buffer
		textCode := runProductStatus([]string{"--effort-root", dir}, &textStdout, &textStderr, nil)
		if textCode != 1 {
			t.Fatalf("expected error text code 1, got code=%d stdout=%s stderr=%s", textCode, textStdout.String(), textStderr.String())
		}
		text := textStdout.String()
		if strings.Contains(text, dir) {
			t.Fatalf("expected privacy-safe blocker details, got absolute path in text output:\n%s", text)
		}
		if !strings.Contains(text, "  status: ERROR") {
			t.Fatalf("expected deterministic ERROR status label, got:\n%s", text)
		}
		if !strings.Contains(text, "  [missing_workflow_state] workflow_state.json not found") {
			t.Fatalf("expected typed blocker identity in text output, got:\n%s", text)
		}
		if !strings.Contains(text, "  next: "+out.NextValidActions[0]) {
			t.Fatalf("expected text next action parity with JSON value %q, got:\n%s", out.NextValidActions[0], text)
		}
	})
}

func TestProductStatusErrorNextValidActionsParity(t *testing.T) {
	dir := t.TempDir()
	var jsonStdout, jsonStderr bytes.Buffer
	code := runProductStatus([]string{"--effort-root", dir, "--json"}, &jsonStdout, &jsonStderr, nil)
	if code != 1 {
		t.Fatalf("expected error code, got %d", code)
	}
	var jsonResult productworkflow.ProductStatusView
	if err := json.Unmarshal(jsonStdout.Bytes(), &jsonResult); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if len(jsonResult.NextValidActions) == 0 {
		t.Fatal("expected next_valid_actions for status error path")
	}
	var textStdout, textStderr bytes.Buffer
	code = runProductStatus([]string{"--effort-root", dir}, &textStdout, &textStderr, nil)
	if code != 1 {
		t.Fatalf("expected error text code, got %d", code)
	}
	if !strings.Contains(textStdout.String(), "next: "+jsonResult.NextValidActions[0]) {
		t.Fatalf("expected text next-action parity, got:\n%s", textStdout.String())
	}
}

func TestProductStatusAuditFailTextIncludesNextAction(t *testing.T) {
	dir := writeValidTestEffort(t)
	writeTestFile(t, dir, "tdd.json", `{not valid json`)
	var stdout, stderr bytes.Buffer
	code := runProductStatus([]string{"--effort-root", dir}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("expected audit fail code, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "next: fix product workflow findings") {
		t.Fatalf("expected actionable next line, got:\n%s", stdout.String())
	}
	if strings.Contains(stdout.String(), "next: complete") {
		t.Fatalf("expected status failure not to surface stale workflow progression, got:\n%s", stdout.String())
	}
}

func TestProductStatusMissingWorkflowStateExits1(t *testing.T) {
	dir := t.TempDir()

	var stdout, stderr bytes.Buffer
	code := runProductStatus([]string{"--effort-root", dir, "--json"}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("expected code 1 for missing workflow_state.json, got %d", code)
	}
	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("JSON output should be valid even on error: %v\n%s", err, stdout.String())
	}
	if result["audit_status"] != "error" {
		t.Fatalf("expected audit_status error, got %v", result["audit_status"])
	}
	blockers, ok := result["blockers"].([]any)
	if !ok || len(blockers) == 0 {
		t.Fatal("expected at least one blocker")
	}
}

func TestProductStatusInvalidWorkflowStateExits1(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "workflow_state.json", `{not valid json`)

	var stdout, stderr bytes.Buffer
	code := runProductStatus([]string{"--effort-root", dir, "--json"}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("expected code 1 for invalid workflow_state.json, got %d", code)
	}
	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("JSON output must be valid even on parse error: %v\n%s", err, stdout.String())
	}
	if result["audit_status"] != "error" {
		t.Fatalf("expected audit_status error, got %v", result["audit_status"])
	}
}

func TestProductStatusSemanticInvalidWorkflowStateExits1(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "workflow_state.json", `{"schema_version":"idiom.product_workflow_state.v0.fixture"}`)

	var stdout, stderr bytes.Buffer
	code := runProductStatus([]string{"--effort-root", dir, "--json"}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("expected code 1 for semantically invalid workflow_state.json, got %d", code)
	}
	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("JSON output must be valid even on validation error: %v\n%s", err, stdout.String())
	}
	if result["audit_status"] != "error" {
		t.Fatalf("expected audit_status error, got %v", result["audit_status"])
	}
	blockers, ok := result["blockers"].([]any)
	if !ok || len(blockers) == 0 {
		t.Fatal("expected at least one blocker")
	}
	firstBlocker, ok := blockers[0].(map[string]any)
	if !ok {
		t.Fatal("expected blocker to be a map")
	}
	if firstBlocker["code"] != "invalid_workflow_state" {
		t.Fatalf("expected blocker code invalid_workflow_state, got %v", firstBlocker["code"])
	}
}

func TestProductEffortsJSONListsFiltersAndSummarizes(t *testing.T) {
	root := t.TempDir()
	effortsRoot := filepath.Join(root, "docs", "product-workflows", "efforts")
	writeTestFile(t, effortsRoot, filepath.Join("started", "beta", "workflow_state.json"), productEffortsCLIWorkflowState("beta-effort", "Beta Work", "started", "implementing", "beta"))
	writeTestFile(t, effortsRoot, filepath.Join("unstarted", "alpha", "workflow_state.json"), productEffortsCLIWorkflowState("alpha-effort", "Alpha Work", "unstarted", "planned", "alpha"))

	var stdout, stderr bytes.Buffer
	code := runProductEfforts([]string{"--root", effortsRoot, "--status", "started", "--query", "beta", "--json"}, &stdout, &stderr, nil)
	if code != 0 {
		t.Fatalf("expected efforts to pass, got code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var result productworkflow.ProductEffortsResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	if result.Status != "pass" || result.Counts.EffortCount != 1 || len(result.Efforts) != 1 {
		t.Fatalf("expected one matching effort, got %#v", result)
	}
	effort := result.Efforts[0]
	if effort.EffortID != "beta-effort" || effort.RelativeFolderPath != "started/beta" {
		t.Fatalf("unexpected effort summary: %#v", effort)
	}
	if effort.ContextPackID != "pack-beta-effort" || effort.FreshnessState != "current" {
		t.Fatalf("expected context summary, got %#v", effort)
	}
	if effort.BlockerCount != 1 || effort.OpenObligationCount != 1 {
		t.Fatalf("expected blocker and obligation counts, got %#v", effort)
	}
	if len(effort.NextValidActions) != 1 || effort.NextValidActions[0] != "submit_for_verification" {
		t.Fatalf("expected next action summary, got %#v", effort.NextValidActions)
	}
}

func TestProductEffortsTextReportsTypedWorkflowStateFindings(t *testing.T) {
	root := t.TempDir()
	effortsRoot := filepath.Join(root, "docs", "product-workflows", "efforts")
	if err := os.MkdirAll(filepath.Join(effortsRoot, "started", "missing"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, effortsRoot, filepath.Join("started", "bad", "workflow_state.json"), `{not valid json`)

	var stdout, stderr bytes.Buffer
	code := runProductEfforts([]string{"--root", effortsRoot, "--status", "started"}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("expected malformed effort state to fail, got code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{
		"product efforts: efforts FAILED",
		"[invalid_workflow_state:started/bad/workflow_state.json]",
		"[missing_workflow_state:started/missing/workflow_state.json]",
		"next: fix product effort workflow_state findings before relying on the effort browser",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected text output to contain %q, got:\n%s", want, out)
		}
	}
}

func TestProductEffortsTextIncludesFullSummaryFields(t *testing.T) {
	root := t.TempDir()
	effortsRoot := filepath.Join(root, "docs", "product-workflows", "efforts")
	writeTestFile(t, effortsRoot, filepath.Join("started", "beta", "workflow_state.json"), productEffortsCLIWorkflowState("beta-effort", "Beta Work", "started", "implementing", "beta"))

	var stdout, stderr bytes.Buffer
	code := runProductEfforts([]string{"--root", effortsRoot}, &stdout, &stderr, nil)
	if code != 0 {
		t.Fatalf("expected efforts to pass, got code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{
		"title=\"Beta Work\"",
		"last_event=evt-007",
		"context_pack=pack-beta-effort",
		"freshness=current",
		"next_actions: submit_for_verification",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected text output to contain %q, got:\n%s", want, out)
		}
	}
}

func TestProductEffortsUsageErrorsAreTyped(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := runProductEfforts([]string{"--root", root, "--status", "later", "--json"}, &stdout, &stderr, nil)
	if code != 2 {
		t.Fatalf("expected unknown status usage error, got code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var result productworkflow.ProductEffortsResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	if result.Status != "fail" || len(result.Findings) != 1 || result.Findings[0].Code != "invalid_status_filter" {
		t.Fatalf("expected typed invalid status finding, got %#v", result)
	}

	stdout.Reset()
	stderr.Reset()
	code = runProductEfforts([]string{"--root", filepath.Join("..", "efforts"), "--json"}, &stdout, &stderr, nil)
	if code != 2 {
		t.Fatalf("expected unsafe root usage error, got code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	if len(result.Findings) != 1 || result.Findings[0].Code != "unsafe_efforts_root" {
		t.Fatalf("expected unsafe root finding, got %#v", result)
	}

	stdout.Reset()
	stderr.Reset()
	code = runProductEfforts([]string{"--root", root, "extra"}, &stdout, &stderr, nil)
	if code != 2 {
		t.Fatalf("expected stray positional argument usage error, got code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "unexpected arguments: extra") {
		t.Fatalf("expected unexpected argument message, got stderr=%s", stderr.String())
	}
}

func TestProductStatusMigrationApplyJSONPlanIsReadOnly(t *testing.T) {
	effortsRoot, effortRoot := writeProductStatusMigrationCLIFixture(t, "started", "reviewing")
	beforeWorkflow, err := os.ReadFile(filepath.Join(effortRoot, "workflow_state.json"))
	if err != nil {
		t.Fatalf("read workflow_state.json before plan: %v", err)
	}
	beforeEvents, err := os.ReadFile(filepath.Join(effortRoot, "events.jsonl"))
	if err != nil {
		t.Fatalf("read events.jsonl before plan: %v", err)
	}
	beforeIndex, err := os.ReadFile(filepath.Join(effortsRoot, "index.json"))
	if err != nil {
		t.Fatalf("read index.json before plan: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := runProductStatusMigration([]string{"--effort-root", effortRoot, "--target-status", "closed", "--plan", "--json"}, &stdout, &stderr, nil)
	if code != 0 {
		t.Fatalf("expected plan success, got code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var result productworkflow.ProductStatusMigrationPlan
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("plan output should be valid JSON: %v\n%s", err, stdout.String())
	}
	if result.Status != "planned" {
		t.Fatalf("expected planned CLI result, got %#v", result)
	}
	if result.SourceFolderPath != "started/test-effort" || result.TargetFolderPath != "closed/test-effort" {
		t.Fatalf("unexpected CLI plan paths: %#v", result)
	}
	if result.Event.EventID == "" || result.Event.Details.StateRevision != 8 {
		t.Fatalf("expected CLI plan event and revision, got %#v", result)
	}

	afterWorkflow, err := os.ReadFile(filepath.Join(effortRoot, "workflow_state.json"))
	if err != nil {
		t.Fatalf("read workflow_state.json after plan: %v", err)
	}
	afterEvents, err := os.ReadFile(filepath.Join(effortRoot, "events.jsonl"))
	if err != nil {
		t.Fatalf("read events.jsonl after plan: %v", err)
	}
	afterIndex, err := os.ReadFile(filepath.Join(effortsRoot, "index.json"))
	if err != nil {
		t.Fatalf("read index.json after plan: %v", err)
	}
	if string(beforeWorkflow) != string(afterWorkflow) {
		t.Fatal("CLI plan must not mutate workflow_state.json")
	}
	if string(beforeEvents) != string(afterEvents) {
		t.Fatal("CLI plan must not mutate events.jsonl")
	}
	if string(beforeIndex) != string(afterIndex) {
		t.Fatal("CLI plan must not mutate index.json")
	}
}

func TestProductStatusMigrationApplyJSONApplyMovesEffort(t *testing.T) {
	effortsRoot, effortRoot := writeProductStatusMigrationCLIFixture(t, "started", "reviewing")

	var stdout, stderr bytes.Buffer
	code := runProductStatusMigration([]string{
		"--effort-root", effortRoot,
		"--target-status", "closed",
		"--apply",
		"--expected-revision", "7",
		"--json",
	}, &stdout, &stderr, nil)
	if code != 0 {
		t.Fatalf("expected apply success, got code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}

	var result productworkflow.ProductStatusMigrationPlan
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("apply output should be valid JSON: %v\n%s", err, stdout.String())
	}
	if result.Status != "applied" {
		t.Fatalf("expected applied CLI result, got %#v", result)
	}
	if result.Event.Event != "status_migration_applied" || result.Event.EventID == "" {
		t.Fatalf("unexpected CLI apply event payload: %#v", result.Event)
	}
	targetRoot := filepath.Join(effortsRoot, "closed", "test-effort")
	if _, err := os.Stat(effortRoot); !os.IsNotExist(err) {
		t.Fatalf("expected CLI apply to move source effort folder, stat err=%v", err)
	}
	workflowData, err := os.ReadFile(filepath.Join(targetRoot, "workflow_state.json"))
	if err != nil {
		t.Fatalf("read migrated workflow_state.json: %v", err)
	}
	ws, err := productworkflow.ParseWorkflowStateJSON(workflowData)
	if err != nil {
		t.Fatalf("parse migrated workflow_state.json: %v", err)
	}
	if ws.StatusBucket != "closed" || ws.CurrentState != "closed" || ws.StateRevision != 8 {
		t.Fatalf("unexpected migrated workflow state: %#v", ws)
	}
	if ws.LastEventID != result.Event.EventID {
		t.Fatalf("expected workflow_state last_event_id %q, got %q", result.Event.EventID, ws.LastEventID)
	}
}

func TestProductStatusMigrationApplyUsageErrorsAreTyped(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "unsafe traversal effort root",
			args: []string{
				"--effort-root", filepath.Join("..", "efforts", "started", "test-effort"),
				"--target-status", "closed",
				"--plan",
				"--json",
			},
			want: "unsafe_effort_root",
		},
		{
			name: "missing effort root",
			args: []string{
				"--target-status", "closed",
				"--plan",
				"--json",
			},
			want: "missing_effort_root",
		},
		{
			name: "missing target status",
			args: []string{
				"--effort-root", filepath.Join("started", "test-effort"),
				"--plan",
				"--json",
			},
			want: "missing_target_status",
		},
		{
			name: "mode conflict",
			args: []string{
				"--effort-root", filepath.Join("started", "test-effort"),
				"--target-status", "closed",
				"--plan",
				"--apply",
				"--json",
			},
			want: "invalid_migration_mode",
		},
		{
			name: "apply missing expected revision",
			args: []string{
				"--effort-root", filepath.Join("started", "test-effort"),
				"--target-status", "closed",
				"--apply",
				"--json",
			},
			want: "missing_expected_revision",
		},
		{
			name: "unknown flag",
			args: []string{
				"--json",
				"--bogus",
			},
			want: "invalid_arguments",
		},
		{
			name: "malformed expected revision",
			args: []string{
				"--effort-root", filepath.Join("started", "test-effort"),
				"--target-status", "closed",
				"--apply",
				"--expected-revision", "not-a-number",
				"--json",
			},
			want: "invalid_arguments",
		},
		{
			name: "json true malformed expected revision",
			args: []string{
				"--json=true",
				"--effort-root", filepath.Join("started", "test-effort"),
				"--target-status", "closed",
				"--apply",
				"--expected-revision", "not-a-number",
			},
			want: "invalid_arguments",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := runProductStatusMigration(tc.args, &stdout, &stderr, nil)
			if code != 2 {
				t.Fatalf("expected typed usage error code 2, got code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
			}
			var result productworkflow.ProductStatusMigrationPlan
			if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
				t.Fatalf("usage error output should still be valid JSON: %v\n%s", err, stdout.String())
			}
			if result.Status != "blocked" || len(result.Blockers) != 1 || result.Blockers[0].Code != tc.want {
				t.Fatalf("expected %s blocker, got %#v", tc.want, result)
			}
		})
	}
}

func TestProductStatusUsageErrors(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runProductStatus([]string{}, &stdout, &stderr, nil)
	if code != 2 {
		t.Fatalf("expected code 2 for missing --effort-root, got %d", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected no stdout for text usage error, got %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "product status: --effort-root is required") {
		t.Fatalf("expected missing effort root usage error, got %q", stderr.String())
	}
}

func TestProductStatusJSONUsageErrorsAreTyped(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runProductStatus([]string{"--json"}, &stdout, &stderr, nil)
	if code != 2 {
		t.Fatalf("expected code 2 for JSON missing --effort-root, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if strings.TrimSpace(stderr.String()) != "" {
		t.Fatalf("expected JSON usage error to keep stderr empty, got %q", stderr.String())
	}
	var result productStatusUsageJSONResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("usage error output should be valid JSON: %v\n%s", err, stdout.String())
	}
	if result.Status != "blocked" || len(result.Blockers) != 1 {
		t.Fatalf("expected blocked status with one blocker, got %#v", result)
	}
	blocker := result.Blockers[0]
	if blocker.Code != "missing-argument" || blocker.Path != "--effort-root" || blocker.Message != "--effort-root is required" {
		t.Fatalf("unexpected usage blocker: %#v", blocker)
	}
	if !strings.Contains(result.NextValidAction, "--effort-root") {
		t.Fatalf("expected next valid action to mention --effort-root, got %q", result.NextValidAction)
	}
}

func TestProductStatusHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runProductStatus([]string{"-h"}, &stdout, &stderr, nil)
	if code != 0 {
		t.Fatalf("expected code 0 for -h, got %d", code)
	}
}

func TestProductStatusTelemetryName(t *testing.T) {
	name := commandTelemetryName([]string{"product", "status", "--effort-root", "."})
	if name != "product:status" {
		t.Fatalf("expected telemetry name product:status, got %q", name)
	}
}

func TestProductStatusAuditFailReturnsCode1(t *testing.T) {
	dir := writeValidTestEffort(t)
	writeTestFile(t, dir, "tdd.json", `{not valid json`)

	var stdout, stderr bytes.Buffer
	code := runProductStatus([]string{"--effort-root", dir, "--json"}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("expected code 1 when audit_status != pass, got %d", code)
	}
	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("JSON output must be valid even on audit fail: %v\n%s", err, stdout.String())
	}
	if result["audit_status"] != "fail" {
		t.Fatalf("expected audit_status fail, got %v", result["audit_status"])
	}
	findings, ok := result["findings"].([]any)
	if !ok || len(findings) == 0 {
		t.Fatal("expected findings in failed status output")
	}
	metricErr := false
	for _, raw := range findings {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if entry["code"] == "metrics_error" {
			metricErr = true
			break
		}
	}
	if !metricErr {
		t.Fatalf("expected metrics_error finding in status output, got %#v", findings)
	}
}

func TestProductStatusUsageIncludesStatus(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runProductMainWithTelemetry([]string{"--help"}, &stdout, &stderr, nil)
	if code != 0 {
		t.Fatalf("expected code 0 for help, got %d", code)
	}
	if !strings.Contains(stdout.String(), "status") {
		t.Fatalf("expected usage to mention status, got:\n%s", stdout.String())
	}
}

func findAuditSection(t *testing.T, result productAuditResult, name string) productAuditSection {
	t.Helper()
	for _, sec := range result.Sections {
		if sec.Name == name {
			return sec
		}
	}
	t.Fatalf("section %q not found", name)
	return productAuditSection{}
}

func findStatusSection(t *testing.T, sections []productworkflow.StatusSection, name string) productworkflow.StatusSection {
	t.Helper()
	for _, sec := range sections {
		if sec.Name == name {
			return sec
		}
	}
	t.Fatalf("status section %q not found", name)
	return productworkflow.StatusSection{}
}

func writeValidTestEffort(t *testing.T) string {
	t.Helper()
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
	return dir
}

func writeProductStatusMigrationCLIFixture(t *testing.T, bucket, state string) (string, string) {
	t.Helper()
	root := t.TempDir()
	effortsRoot := filepath.Join(root, "docs", "product-workflows", "efforts")
	effortRoot := filepath.Join(effortsRoot, bucket, "test-effort")
	writeTestFile(t, effortRoot, "workflow_state.json", productStatusMigrationCLIWorkflowState(bucket, state))
	writeTestFile(t, effortRoot, "events.jsonl", `{"event":"workflow_state_created","event_id":"evt-0001","effort_id":"test-effort"}`+"\n")
	writeTestFile(t, effortsRoot, "index.json", productStatusMigrationCLIEffortIndex(bucket, state))
	return effortsRoot, effortRoot
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		next := filepath.Dir(dir)
		if next == dir {
			t.Fatal("could not find repo root")
		}
		dir = next
	}
}

func loadDurableFixtureWorkflowState(t *testing.T) map[string]any {
	t.Helper()
	root := repoRoot(t)
	wsPath := filepath.Join(root, "docs", "product-workflows", "efforts", "closed", "prd-adr-brief-workflow", "workflow_state.json")
	data, err := os.ReadFile(wsPath)
	if err != nil {
		t.Fatalf("failed to read workflow_state.json: %v", err)
	}
	var ws map[string]any
	if err := json.Unmarshal(data, &ws); err != nil {
		t.Fatalf("failed to parse workflow_state.json: %v", err)
	}
	return ws
}

func writeTestFile(t *testing.T, dir, relPath, content string) {
	t.Helper()
	fullPath := filepath.Join(dir, relPath)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(fullPath), err)
	}
	if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", fullPath, err)
	}
}

func productWorkflowTestRecoveryPacketJSON(t *testing.T) string {
	t.Helper()
	record, err := recovery.BuildPlanExecuteFailureRecord(recovery.PlanExecuteFailureInput{
		SessionID:         "session-product-cli",
		FailureKind:       "executor_failed",
		FailureReason:     "edit failed",
		PromptFile:        "docs/product-workflows/prd.md",
		RequiredEditPaths: []string{"cmd/idiom/product_workflow.go"},
		ChangedPaths:      []string{"cmd/idiom/product_workflow.go"},
	}, time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("BuildPlanExecuteFailureRecord: %v", err)
	}
	packet, err := recovery.BuildDecompositionPacket(record, recovery.DecompositionOptions{
		Now: time.Date(2026, 5, 18, 12, 1, 0, 0, time.UTC),
		PartialState: recovery.PartialStateForDirtyPaths(
			nil,
			record.PartialState.ChangedPaths,
			record.SourceRun.RequiredEditPaths,
			record.SourceRun.AllowedEditPaths,
			record.PartialState.TotalEdits,
		),
	})
	if err != nil {
		t.Fatalf("BuildDecompositionPacket: %v", err)
	}
	body, err := json.MarshalIndent(packet, "", "  ")
	if err != nil {
		t.Fatalf("marshal recovery packet: %v", err)
	}
	return string(body)
}

func findProductOperatorAction(t *testing.T, actions []productworkflow.ProductOperatorAction, id string) productworkflow.ProductOperatorAction {
	t.Helper()
	for _, action := range actions {
		if action.ID == id {
			return action
		}
	}
	t.Fatalf("operator action %q not found in %#v", id, actions)
	return productworkflow.ProductOperatorAction{}
}

func validTestWorkflowStateJSON() string {
	return `{
		"schema_version":"idiom.product_workflow_state.v0.fixture",
		"effort_id":"test-effort",
		"task_id":"impl-test",
		"title":"Test Effort",
		"status_bucket":"started",
		"current_phase":"phase-1",
		"current_state":"implementing",
		"artifact_paths":{"events":"events.jsonl"},
		"context":{"context_pack_id":"pack","freshness_state":"current"},
		"coverage":{"coverage_tier":"unit"},
		"privacy":{"redaction_status":"applied"},
		"folder_path":"docs/test",
		"durability_level":"source_controlled",
		"next_valid_actions":["complete"]
	}`
}

func productEffortsCLIWorkflowState(effortID, title, bucket, state, folderName string) string {
	return `{
		"schema_version":"idiom.product_workflow_state.v0.fixture",
		"effort_id":"` + effortID + `",
		"task_id":"impl-test",
		"title":"` + title + `",
		"status_bucket":"` + bucket + `",
		"current_phase":"phase-1",
		"current_state":"` + state + `",
		"state_revision":7,
		"last_event_id":"evt-007",
		"artifact_paths":{"events":"events.jsonl"},
		"context":{"context_pack_id":"pack-` + effortID + `","freshness_state":"current"},
		"coverage":{"coverage_tier":"unit"},
		"open_obligations":["TDD-001"],
		"blockers":["blocked-a"],
		"privacy":{"redaction_status":"applied"},
		"folder_path":"docs/product-workflows/efforts/` + bucket + `/` + folderName + `",
		"next_valid_actions":["submit_for_verification"],
		"durability_level":"source_controlled"
	}`
}

func productStatusMigrationCLIWorkflowState(bucket, state string) string {
	return `{
		"schema_version":"idiom.product_workflow_state.v0.fixture",
		"effort_id":"test-effort",
		"task_id":"impl-test",
		"title":"Test Effort",
		"created_at":"2026-05-18T21:00:00Z",
		"updated_at":"2026-05-18T21:00:00Z",
		"state_revision":7,
		"last_event_id":"evt-007",
		"workspace_fingerprint":"idiom@test",
		"status_bucket":"` + bucket + `",
		"current_phase":"phase-test",
		"current_state":"` + state + `",
		"artifact_paths":{"events":"events.jsonl"},
		"context":{"context_pack_id":"pack","freshness_state":"current","referenced_paths":[],"unresolved_gaps":[],"refresh_policy":"on_demand"},
		"coverage":{"coverage_tier":"unit","gate_results":[],"blocking_criteria":[],"advisory_criteria":[]},
		"open_obligations":[],
		"open_questions":[],
		"blockers":[],
		"privacy":{"redaction_status":"applied","fields_excluded":[]},
		"session_ids":{"codex":"session-1"},
		"folder_path":"docs/product-workflows/efforts/` + bucket + `/test-effort",
		"prior_folder_paths":[],
		"next_valid_actions":["complete"],
		"durability_level":"source_controlled"
	}`
}

func productStatusMigrationCLIEffortIndex(bucket, state string) string {
	return `{
		"schema_version":"idiom.effort_index.v1",
		"updated_at":"2026-05-18T21:00:00Z",
		"efforts":[{
			"effort_id":"test-effort",
			"title":"Test Effort",
			"status_bucket":"` + bucket + `",
			"current_phase":"phase-test",
			"current_state":"` + state + `",
			"folder_path":"` + bucket + `/test-effort",
			"task_id":"impl-test",
			"created_at":"2026-05-18T21:00:00Z",
			"prior_paths":[]
		}]
	}`
}

func validTestTDDJSON() string {
	return `{
		"schema_version":"idiom.tdd_obligations.v2.fixture",
		"effort_id":"test-effort",
		"task_id":"impl-test",
		"coverage_tier":"unit",
		"coverage":{"mutation_min_score":80},
		"obligations":[{
			"id":"TDD-001","type":"acceptance","behavior":"test","owner":"test","command":"go test","required_before":"close","status":"pending"
		}]
	}`
}

func validTestEvidenceJSONL() string {
	return `{"card_id":"EV-001","kind":"finding","source_handle":"file://test","claim":"test claim","confidence":"high","freshness":"current","uncertainty":"","evidence_refs":["ref"],"researcher_id":"test","timestamp":"2026-05-17T16:00:00Z"}`
}

func validTestTraceabilityMatrixJSON() string {
	return `{
		"schema_version":"idiom.traceability_matrix.v1.fixture",
		"effort_id":"test-effort",
		"task_id":"impl-test",
		"created_at":"2026-05-17T16:00:00Z",
		"matrix":[{
			"evidence_card":"EV-001","prd_requirement":"REQ-TEST-001","adr":"ADR-001","tdd_obligation":"TDD-001","brief":"briefs/test.md","report":"reports/test.json"
		}]
	}`
}

func invalidCloseoutLedgerJSONL() string {
	return strings.Join([]string{
		`{"record_type":"atom_planned","task_id":"impl-test","effort_id":"test-effort","timestamp":"2026-05-17T16:00:00Z","complexity_tier":"C1","context_pack_id":"pack","implementor":{"id":"impl","runtime":"opencode","model":"test","command":"run"},"reviewer":{"id":"rev","model":"test","reasoning_effort":"medium","runtime":"codex"},"scope":{"expected_files":["test.go"],"forbidden_patterns":["cmd/**"]},"required_verification":["go test"],"report_paths":{"implementation":"reports/impl.json","review":"reports/rev.json","investigation":"reports/inv.json"}}`,
		`{"record_type":"atom_closed","task_id":"impl-test","effort_id":"test-effort","timestamp":"2026-05-17T16:05:00Z","open_blockers":["review_pending"]}`,
	}, "\n")
}

func implementationLedgerWithoutTDDEvidenceJSONL() string {
	return strings.Join([]string{
		`{"record_type":"atom_planned","task_id":"impl-test","effort_id":"test-effort","timestamp":"2026-05-17T16:00:00Z","complexity_tier":"C1","context_pack_id":"pack","implementor":{"id":"impl","runtime":"opencode","model":"test","command":"run"},"reviewer":{"id":"rev","model":"test","reasoning_effort":"medium","runtime":"codex"},"scope":{"expected_files":["test.go"],"forbidden_patterns":["cmd/**"]},"required_verification":["go test"],"report_paths":{"implementation":"reports/impl.json","review":"reports/rev.json","investigation":"reports/inv.json"}}`,
		`{"record_type":"implementation_completed","task_id":"impl-test","effort_id":"test-effort","timestamp":"2026-05-17T16:01:00Z","implementor_id":"impl","commit_hash":"0000000000000000000000000000000000000000"}`,
		`{"record_type":"verification_completed","task_id":"impl-test","effort_id":"test-effort","timestamp":"2026-05-17T16:02:00Z"}`,
		`{"record_type":"review_completed","task_id":"impl-test","effort_id":"test-effort","timestamp":"2026-05-17T16:03:00Z","reviewer_id":"rev","model":"test","reasoning_effort":"medium"}`,
		`{"record_type":"post_effort_investigation_completed","task_id":"impl-test","effort_id":"test-effort","timestamp":"2026-05-17T16:04:00Z"}`,
		`{"record_type":"atom_closed","task_id":"impl-test","effort_id":"test-effort","timestamp":"2026-05-17T16:05:00Z","open_blockers":[]}`,
	}, "\n")
}

func validTestImplementationLedgerJSONL() string {
	return strings.Replace(
		implementationLedgerWithoutTDDEvidenceJSONL(),
		`{"record_type":"verification_completed","task_id":"impl-test","effort_id":"test-effort","timestamp":"2026-05-17T16:02:00Z"}`,
		`{"record_type":"verification_completed","task_id":"impl-test","effort_id":"test-effort","timestamp":"2026-05-17T16:02:00Z","tdd_obligations_satisfied":["TDD-001"]}`,
		1,
	)
}

func validTestResearchProfileJSON() string {
	return validTestResearchProfileJSONWithPolicy("test-profile", "test-profile", "block_prd_audit")
}

func validTestResearchProfileJSONWithPolicy(name, profileID, unsupportedClaimsPolicy string) string {
	return `{
			"schema_version": "idiom.research_profile.v1",
			"name": "` + name + `",
			"profile_id": "` + profileID + `",
			"researchers": [
				{
					"id": "r1",
					"alignment": "technical",
					"question": "What changed?",
				"allowed_capabilities": ["query_context"],
				"output": "evidence_cards"
			}
			],
			"merge_policy": {
				"conflicts": "preserve_until_adr",
				"unsupported_claims": "` + unsupportedClaimsPolicy + `",
				"stale_local_context": "refresh_or_mark_gap"
			}
		}`
}

func writeProductResearchProfile(t *testing.T, dir, content string) {
	t.Helper()
	writeProductResearchProfileFile(t, dir, "test.json", content)
}

func writeProductResearchProfileFile(t *testing.T, dir, name, content string) {
	t.Helper()
	profilesDir := filepath.Join(dir, ".idiom", "research", "profiles")
	if err := os.MkdirAll(profilesDir, 0o755); err != nil {
		t.Fatalf("mkdir research profiles: %v", err)
	}
	if err := os.WriteFile(filepath.Join(profilesDir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write research profile: %v", err)
	}
}

func validTestResearchCardsJSON(cardID, researcherID, claim string) string {
	return `{
		"schema_version": "idiom.research_cards.v1",
		"researcher_id": "` + researcherID + `",
		"alignment": "technical",
		"cards": [
			{
				"card_id": "` + cardID + `",
				"kind": "finding",
				"source_handle": "file://README.md",
				"claim": "` + claim + `",
				"confidence": "high",
				"freshness": "current",
				"uncertainty": "",
				"evidence_refs": ["README.md:1"],
				"researcher_id": "` + researcherID + `",
				"timestamp": "2026-05-18T00:00:00Z"
			}
		]
	}`
}

func validTestPRDMarkdown() string {
	return `---
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
- **tdd_obligations**: ["TDD-001"]
- **open_questions**: []
`
}

func validTestADRIndexJSON() string {
	return `{"schema_version":"v1","effort_id":"test-effort","adrs":[{"adr_id":"ADR-001","title":"Test ADR","status":"accepted","path":"ADR-001-test.md","coverage_tier":"unit","prd_refs":["REQ-TEST-001"],"created_at":"2026-05-17T00:00:00Z"}]}`
}

func validTestADRMarkdown() string {
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

test.go

~~~text
package test
~~~

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

func validTestImplementationReportJSON() string {
	return `{
		"schema_version":"idiom.implementation_report.v1.fixture",
		"task_id":"impl-test",
		"effort_id":"test-effort",
		"implementor_id":"test-agent",
		"runtime":"opencode",
		"model":"test-model",
		"completed_at":"2026-05-17T16:00:00Z",
		"complexity_tier":"C1",
		"scope_statement":"Test scope",
		"code_changes":false,
		"changed_paths":[],
		"verification":{"run_by_implementor":false,"commands":[],"status":"pending"},
		"privacy_note":"No raw prompts, raw transcripts, file bodies, secrets, or customer data stored.",
		"open_blockers":["reviewer_pending"],
		"reviewer_id":"test-reviewer"
	}`
}

func validTestReviewReportJSON() string {
	return `{
		"schema_version":"idiom.review_report.v1.fixture",
		"effort_id":"test-effort",
		"task_id":"impl-test",
		"reviewer":{
			"agent_id":"test-reviewer",
			"model":"GPT-5.4",
			"reasoning_effort":"medium",
			"independent_from_implementor":true
		},
		"status":"pending",
		"findings":[],
		"checks":{},
		"reviewed_at":"",
		"commands_reviewed":[],
		"created_at":"2026-05-17T16:00:00Z"
	}`
}

func validTestTestInvestigationReportJSON() string {
	return `{
		"schema_version":"idiom.investigation_report.v1.fixture",
		"effort_id":"test-effort",
		"task_id":"impl-test",
		"status":"pending",
		"created_at":"2026-05-17T16:00:00Z",
		"scope":{"files":["a.go"]},
		"tests":{"focused":"go test"},
		"coverage":{"tier":"unit"},
		"traceability":{"checked":false},
		"privacy_audit":{"no_raw_prompts":true},
		"reviewer_findings":[],
		"blockers":["implementation_pending"],
		"next_valid_action":"",
		"completed_at":""
	}`
}

const scopeGatePlannedAtom = `{"record_type":"atom_planned","task_id":"impl-scope-test","effort_id":"test-effort","timestamp":"2026-05-17T16:00:00Z","complexity_tier":"C1","context_pack_id":"pack","implementor":{"id":"impl","runtime":"opencode","model":"test","command":"run"},"reviewer":{"id":"rev","model":"test","reasoning_effort":"medium","runtime":"codex"},"scope":{"expected_files":["cmd/idiom/product_workflow.go"],"forbidden":["internal/productworkflow/scope.go"],"forbidden_patterns":["docs/**"]},"required_verification":["go test"],"report_paths":{"implementation":"reports/impl.json","review":"reports/rev.json","investigation":"reports/inv.json"}}`

func scopeGateLedgerForbiddenExactPathJSONL() string {
	return scopeGatePlannedAtom + "\n" +
		`{"record_type":"implementation_completed","task_id":"impl-scope-test","effort_id":"test-effort","timestamp":"2026-05-17T16:01:00Z","implementor_id":"impl","changed_paths":["cmd/idiom/product_workflow.go","internal/productworkflow/scope.go"]}`
}

func scopeGateLedgerForbiddenPatternJSONL() string {
	return scopeGatePlannedAtom + "\n" +
		`{"record_type":"implementation_completed","task_id":"impl-scope-test","effort_id":"test-effort","timestamp":"2026-05-17T16:01:00Z","implementor_id":"impl","changed_paths":["cmd/idiom/product_workflow.go","docs/design.md"]}`
}

func scopeGateLedgerUnexpectedPathJSONL() string {
	return scopeGatePlannedAtom + "\n" +
		`{"record_type":"implementation_completed","task_id":"impl-scope-test","effort_id":"test-effort","timestamp":"2026-05-17T16:01:00Z","implementor_id":"impl","changed_paths":["cmd/idiom/product_workflow.go","internal/productworkflow/validate.go"]}`
}

func scopeGateLedgerMissingExpectedPathJSONL() string {
	return `{"record_type":"atom_planned","task_id":"impl-scope-test","effort_id":"test-effort","timestamp":"2026-05-17T16:00:00Z","complexity_tier":"C1","context_pack_id":"pack","implementor":{"id":"impl","runtime":"opencode","model":"test","command":"run"},"reviewer":{"id":"rev","model":"test","reasoning_effort":"medium","runtime":"codex"},"scope":{"expected_files":["cmd/idiom/product_workflow.go","cmd/idiom/main.go"],"forbidden":["internal/productworkflow/scope.go"],"forbidden_patterns":["docs/**"]},"required_verification":["go test"],"report_paths":{"implementation":"reports/impl.json","review":"reports/rev.json","investigation":"reports/inv.json"}}` + "\n" +
		`{"record_type":"implementation_completed","task_id":"impl-scope-test","effort_id":"test-effort","timestamp":"2026-05-17T16:01:00Z","implementor_id":"impl","changed_paths":["cmd/idiom/product_workflow.go"]}`
}

func scopeGateLedgerPassingJSONL() string {
	return scopeGatePlannedAtom + "\n" +
		`{"record_type":"implementation_completed","task_id":"impl-scope-test","effort_id":"test-effort","timestamp":"2026-05-17T16:01:00Z","implementor_id":"impl","changed_paths":["cmd/idiom/product_workflow.go"]}`
}

func writeScopeGateEffort(t *testing.T, ledgerJSONL string) string {
	t.Helper()
	dir := writeValidTestEffort(t)
	if !strings.Contains(ledgerJSONL, "tdd_obligations_satisfied") {
		ledgerJSONL += "\n" + `{"record_type":"verification_completed","task_id":"impl-test","effort_id":"test-effort","timestamp":"2026-05-17T16:02:00Z","tdd_obligations_satisfied":["TDD-001"]}`
	}
	writeTestFile(t, dir, "implementation_ledger.jsonl", ledgerJSONL)
	return dir
}

func TestProductLintScopeGateFailsOnForbiddenPath(t *testing.T) {
	dir := writeScopeGateEffort(t, scopeGateLedgerForbiddenExactPathJSONL())
	result := productworkflow.LintEffortFolder(dir)
	if result.Status != "fail" {
		t.Fatalf("expected fail, got %q", result.Status)
	}
	found := false
	for _, f := range result.Findings {
		if f.Code == "scope_gate_forbidden_path" && strings.Contains(f.Message, "internal/productworkflow/scope.go") && strings.Contains(f.Message, "impl-scope-test") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected scope_gate_forbidden_path finding with task id and path, got %v", result.Findings)
	}
}

func TestProductLintScopeGateFailsOnForbiddenPattern(t *testing.T) {
	dir := writeScopeGateEffort(t, scopeGateLedgerForbiddenPatternJSONL())
	result := productworkflow.LintEffortFolder(dir)
	if result.Status != "fail" {
		t.Fatalf("expected fail, got %q", result.Status)
	}
	found := false
	for _, f := range result.Findings {
		if f.Code == "scope_gate_forbidden_pattern" && strings.Contains(f.Message, "docs/design.md") && strings.Contains(f.Message, "docs/**") && strings.Contains(f.Message, "impl-scope-test") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected scope_gate_forbidden_pattern finding with task id, path, and pattern, got %v", result.Findings)
	}
}

func TestProductLintScopeGateFailsOnUnexpectedPath(t *testing.T) {
	dir := writeScopeGateEffort(t, scopeGateLedgerUnexpectedPathJSONL())
	result := productworkflow.LintEffortFolder(dir)
	if result.Status != "fail" {
		t.Fatalf("expected fail, got %q", result.Status)
	}
	found := false
	for _, f := range result.Findings {
		if f.Code == "scope_gate_unexpected_path" && strings.Contains(f.Message, "internal/productworkflow/validate.go") && strings.Contains(f.Message, "impl-scope-test") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected scope_gate_unexpected_path finding with task id and path, got %v", result.Findings)
	}
}

func TestProductLintScopeGateFailsOnMissingExpectedPath(t *testing.T) {
	dir := writeScopeGateEffort(t, scopeGateLedgerMissingExpectedPathJSONL())
	result := productworkflow.LintEffortFolder(dir)
	if result.Status != "fail" {
		t.Fatalf("expected fail, got %q", result.Status)
	}
	found := false
	for _, f := range result.Findings {
		if f.Code == "scope_gate_missing_expected_path" && strings.Contains(f.Message, "cmd/idiom/main.go") && strings.Contains(f.Message, "impl-scope-test") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected scope_gate_missing_expected_path finding with task id and path, got %v", result.Findings)
	}
}

func TestProductLintScopeGatePassingLedgerPasses(t *testing.T) {
	dir := writeScopeGateEffort(t, scopeGateLedgerPassingJSONL())
	result := productworkflow.LintEffortFolder(dir)
	if result.Status != "pass" {
		t.Fatalf("expected pass, got %q with findings %v", result.Status, result.Findings)
	}
	for _, f := range result.Findings {
		if strings.HasPrefix(f.Code, "scope_gate_") {
			t.Fatalf("unexpected scope gate finding: %v", f)
		}
	}
}

func TestProductLintScopeGateClosedAtomNotEnforced(t *testing.T) {
	dir := writeScopeGateEffort(t, scopeGateLedgerForbiddenExactPathJSONL()+"\n"+
		`{"record_type":"verification_completed","task_id":"impl-scope-test","effort_id":"test-effort","timestamp":"2026-05-17T16:02:00Z"}`+"\n"+
		`{"record_type":"review_completed","task_id":"impl-scope-test","effort_id":"test-effort","timestamp":"2026-05-17T16:03:00Z","reviewer_id":"rev","model":"test","reasoning_effort":"medium"}`+"\n"+
		`{"record_type":"post_effort_investigation_completed","task_id":"impl-scope-test","effort_id":"test-effort","timestamp":"2026-05-17T16:04:00Z"}`+"\n"+
		`{"record_type":"atom_closed","task_id":"impl-scope-test","effort_id":"test-effort","timestamp":"2026-05-17T16:05:00Z","open_blockers":[]}`,
	)
	result := productworkflow.LintEffortFolder(dir)
	if result.Status != "pass" {
		t.Fatalf("expected pass for closed atom (historical), got %q with findings %v", result.Status, result.Findings)
	}
	for _, f := range result.Findings {
		if strings.HasPrefix(f.Code, "scope_gate_") {
			t.Fatalf("scope gate should not be enforced for closed atoms: %v", f)
		}
	}
}

func TestProductLintScopeGateSkipsClosedAtomsButEnforcesActive(t *testing.T) {
	closedAtomScope := `{"record_type":"atom_planned","task_id":"impl-closed","effort_id":"test-effort","timestamp":"2026-05-17T16:00:00Z","complexity_tier":"C1","context_pack_id":"pack","implementor":{"id":"impl","runtime":"opencode","model":"test","command":"run"},"reviewer":{"id":"rev","model":"test","reasoning_effort":"medium","runtime":"codex"},"scope":{"expected_files":["cmd/idiom/product_workflow.go"],"forbidden":["internal/productworkflow/scope.go"],"forbidden_patterns":["docs/**"]},"required_verification":["go test"],"report_paths":{"implementation":"reports/impl.json","review":"reports/rev.json","investigation":"reports/inv.json"}}` + "\n" +
		`{"record_type":"implementation_completed","task_id":"impl-closed","effort_id":"test-effort","timestamp":"2026-05-17T16:01:00Z","implementor_id":"impl","changed_paths":["cmd/idiom/product_workflow.go","internal/productworkflow/scope.go"]}` + "\n" +
		`{"record_type":"verification_completed","task_id":"impl-closed","effort_id":"test-effort","timestamp":"2026-05-17T16:02:00Z"}` + "\n" +
		`{"record_type":"review_completed","task_id":"impl-closed","effort_id":"test-effort","timestamp":"2026-05-17T16:03:00Z","reviewer_id":"rev","model":"test","reasoning_effort":"medium"}` + "\n" +
		`{"record_type":"post_effort_investigation_completed","task_id":"impl-closed","effort_id":"test-effort","timestamp":"2026-05-17T16:04:00Z"}` + "\n" +
		`{"record_type":"atom_closed","task_id":"impl-closed","effort_id":"test-effort","timestamp":"2026-05-17T16:05:00Z","open_blockers":[]}`
	activeAtomScope := scopeGatePlannedAtom + "\n" +
		`{"record_type":"implementation_completed","task_id":"impl-scope-test","effort_id":"test-effort","timestamp":"2026-05-17T16:06:00Z","implementor_id":"impl","changed_paths":["cmd/idiom/product_workflow.go","internal/productworkflow/validate.go"]}`
	dir := writeScopeGateEffort(t, closedAtomScope+"\n"+activeAtomScope)
	result := productworkflow.LintEffortFolder(dir)
	if result.Status != "fail" {
		t.Fatalf("expected fail, got %q", result.Status)
	}
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
	if closedEnforced {
		t.Fatal("scope gate should not be enforced for closed atom impl-closed")
	}
	if !activeEnforced {
		t.Fatalf("expected scope_gate_unexpected_path finding for active atom impl-scope-test, got %v", result.Findings)
	}
}

func TestProductLintScopeGateJSONValidFailing(t *testing.T) {
	dir := writeScopeGateEffort(t, scopeGateLedgerForbiddenExactPathJSONL())
	var stdout, stderr bytes.Buffer
	code := runProductLint([]string{"--effort-root", dir, "--json"}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}
	var result productworkflow.LintSummaryResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	if result.Status != "fail" {
		t.Fatalf("expected status fail, got %q", result.Status)
	}
	found := false
	for _, f := range result.Findings {
		if f.Code == "scope_gate_forbidden_path" {
			found = true
			if f.Path != "implementation_ledger.jsonl" {
				t.Fatalf("expected finding path implementation_ledger.jsonl, got %q", f.Path)
			}
			if !strings.Contains(f.Message, "impl-scope-test") {
				t.Fatalf("finding message should include task_id: %q", f.Message)
			}
		}
	}
	if !found {
		t.Fatalf("expected scope_gate_forbidden_path finding, got %v", result.Findings)
	}
}

func TestProductLintScopeGateJSONValidPassing(t *testing.T) {
	dir := writeScopeGateEffort(t, scopeGateLedgerPassingJSONL())
	var stdout, stderr bytes.Buffer
	code := runProductLint([]string{"--effort-root", dir, "--json"}, &stdout, &stderr, nil)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d: %s", code, stderr.String())
	}
	var result productworkflow.LintSummaryResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	if result.Status != "pass" {
		t.Fatalf("expected status pass, got %q", result.Status)
	}
}

func TestProductAuditScopeGateFailingIncludesSection(t *testing.T) {
	dir := writeScopeGateEffort(t, scopeGateLedgerForbiddenExactPathJSONL())
	var stdout, stderr bytes.Buffer
	code := runProductAudit([]string{"--effort-root", dir, "--json"}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}
	var result productAuditResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	scopeSection := findAuditSection(t, result, "scope_gate")
	if scopeSection.Status != "fail" {
		t.Fatalf("expected scope_gate section to fail, got %q", scopeSection.Status)
	}
	found := false
	for _, f := range scopeSection.Findings {
		if f.Code == "scope_gate_forbidden_path" && strings.Contains(f.Message, "impl-scope-test") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected scope_gate_forbidden_path in scope_gate section, got %v", scopeSection.Findings)
	}
}

func TestProductResearchJSONSurface(t *testing.T) {
	dir := t.TempDir()
	profilesDir := filepath.Join(dir, ".idiom", "research", "profiles")
	if err := os.MkdirAll(profilesDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(profilesDir, "test.json"), []byte(validTestResearchProfileJSON()), 0o644); err != nil {
		t.Fatalf("write profile: %v", err)
	}
	var stdout, stderr bytes.Buffer
	code := runProductResearch([]string{"--project-root", dir, "--json"}, &stdout, &stderr, nil)
	if code != 0 {
		t.Fatalf("expected code 0, got %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	profiles, ok := result["profiles"].([]any)
	if !ok {
		t.Fatalf("expected profiles array, got %T", result["profiles"])
	}
	if len(profiles) != 1 {
		t.Fatalf("expected 1 profile, got %d", len(profiles))
	}
	p := profiles[0].(map[string]any)
	if p["name"] != "test-profile" {
		t.Fatalf("expected name test-profile, got %v", p["name"])
	}
	if _, ok := p["researchers"]; !ok {
		t.Fatal("expected researchers field")
	}
	if _, ok := p["merge_policy"]; !ok {
		t.Fatal("expected merge_policy field")
	}
	if result["profile_count"].(float64) != 1 {
		t.Fatalf("expected profile_count 1, got %v", result["profile_count"])
	}
	findings, ok := result["findings"].([]any)
	if !ok {
		t.Fatalf("expected findings array, got %T", result["findings"])
	}
	if len(findings) != 0 {
		t.Fatalf("expected 0 findings, got %d", len(findings))
	}
}

func TestProductResearchInvalidProfileReturnsExit1(t *testing.T) {
	dir := t.TempDir()
	profilesDir := filepath.Join(dir, ".idiom", "research", "profiles")
	if err := os.MkdirAll(profilesDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(profilesDir, "bad.json"), []byte(`{not valid json`), 0o644); err != nil {
		t.Fatalf("write profile: %v", err)
	}
	var stdout, stderr bytes.Buffer
	code := runProductResearch([]string{"--project-root", dir, "--json"}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("expected code 1, got %d", code)
	}
	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	findings, ok := result["findings"].([]any)
	if !ok || len(findings) == 0 {
		t.Fatalf("expected at least one finding, got %v", result["findings"])
	}
}

func TestProductResearchMergeEvidenceWritesMergedLedger(t *testing.T) {
	dir := t.TempDir()
	writeProductResearchProfile(t, dir, validTestResearchProfileJSON())
	writeTestFile(t, dir, "evidence/evidence.jsonl", validTestEvidenceJSONL())
	writeTestFile(t, dir, "research/outputs/technical.json", validTestResearchCardsJSON("EV-002", "r1", "Merged research claim"))

	var stdout, stderr bytes.Buffer
	code := runProductResearch([]string{"--project-root", dir, "--effort-root", dir, "--merge-evidence", "--json"}, &stdout, &stderr, nil)
	if code != 0 {
		t.Fatalf("expected code 0, got %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	if result["status"] != "pass" {
		t.Fatalf("expected status pass, got %v", result["status"])
	}
	if result["wrote_evidence_ledger"] != true {
		t.Fatalf("expected wrote_evidence_ledger true, got %v", result["wrote_evidence_ledger"])
	}
	merged, err := os.ReadFile(filepath.Join(dir, "evidence", "evidence.jsonl"))
	if err != nil {
		t.Fatalf("read merged evidence: %v", err)
	}
	cards, err := productworkflow.ParseEvidenceCardsJSONL(merged)
	if err != nil {
		t.Fatalf("merged evidence should parse: %v\n%s", err, string(merged))
	}
	if len(cards) != 2 {
		t.Fatalf("expected 2 merged cards, got %d: %#v", len(cards), cards)
	}
	if cards[1].CardID != "EV-002" {
		t.Fatalf("expected EV-002 to be merged after existing card, got %#v", cards)
	}
}

func TestProductResearchMergeEvidenceBlocksOnConflict(t *testing.T) {
	dir := t.TempDir()
	writeProductResearchProfile(t, dir, validTestResearchProfileJSON())
	writeTestFile(t, dir, "evidence/evidence.jsonl", validTestEvidenceJSONL())
	writeTestFile(t, dir, "research/outputs/product.json", validTestResearchCardsJSON("EV-001", "r1", "Conflicting claim"))

	var stdout, stderr bytes.Buffer
	code := runProductResearch([]string{"--project-root", dir, "--effort-root", dir, "--merge-evidence", "--json"}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("expected code 1 for conflict, got %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	if result["status"] != "blocked" {
		t.Fatalf("expected status blocked, got %v", result["status"])
	}
	conflicts, ok := result["conflicts"].([]any)
	if !ok || len(conflicts) != 1 {
		t.Fatalf("expected one conflict, got %v", result["conflicts"])
	}
	if result["wrote_evidence_ledger"] != false {
		t.Fatalf("expected no write on conflict, got %v", result["wrote_evidence_ledger"])
	}
}

func TestProductResearchMergeEvidenceBlocksOnInvalidResearchOutput(t *testing.T) {
	dir := t.TempDir()
	writeProductResearchProfile(t, dir, validTestResearchProfileJSON())
	writeTestFile(t, dir, "evidence/evidence.jsonl", validTestEvidenceJSONL())
	writeTestFile(t, dir, "research/outputs/bad.json", `{"schema_version":"idiom.research_cards.v1","researcher_id":"r1","alignment":"technical","cards":[]}`)

	var stdout, stderr bytes.Buffer
	code := runProductResearch([]string{"--project-root", dir, "--effort-root", dir, "--merge-evidence", "--json"}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("expected code 1 for invalid research output, got %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	findings, ok := result["findings"].([]any)
	if !ok || len(findings) == 0 {
		t.Fatalf("expected findings for invalid research output, got %v", result["findings"])
	}
}

func TestProductResearchMergeEvidenceDefersUnsupportedClaimsToPRDAudit(t *testing.T) {
	dir := t.TempDir()
	writeProductResearchProfile(t, dir, validTestResearchProfileJSON())
	writeTestFile(t, dir, "evidence/evidence.jsonl", validTestEvidenceJSONL())
	unsupported := strings.Replace(
		validTestResearchCardsJSON("EV-002", "r1", "Unsupported claim"),
		`"kind": "finding"`,
		`"kind": "unsupported_claim"`,
		1,
	)
	writeTestFile(t, dir, "research/outputs/product.json", unsupported)

	var stdout, stderr bytes.Buffer
	code := runProductResearch([]string{"--project-root", dir, "--effort-root", dir, "--merge-evidence", "--json"}, &stdout, &stderr, nil)
	if code != 0 {
		t.Fatalf("expected code 0 when unsupported claims are deferred to PRD audit, got %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	if result["wrote_evidence_ledger"] != true {
		t.Fatalf("expected merge to write ledger, got %v", result["wrote_evidence_ledger"])
	}
	findings := result["findings"].([]any)
	if len(findings) != 1 {
		t.Fatalf("expected one unsupported_claim warning, got %v", findings)
	}
	first := findings[0].(map[string]any)
	if first["kind"] != "unsupported_claim" || first["severity"] != "warning" {
		t.Fatalf("expected unsupported_claim warning, got %v", first)
	}
}

func TestProductResearchMergeEvidenceRequiresProfileWhenMultipleProfilesExist(t *testing.T) {
	dir := t.TempDir()
	writeProductResearchProfileFile(t, dir, "a.json", validTestResearchProfileJSONWithPolicy("warn-profile", "warn-profile", "warn"))
	writeProductResearchProfileFile(t, dir, "b.json", validTestResearchProfileJSONWithPolicy("block-profile", "block-profile", "block_research_merge"))
	writeTestFile(t, dir, "evidence/evidence.jsonl", validTestEvidenceJSONL())
	writeTestFile(t, dir, "research/outputs/product.json", validTestResearchCardsJSON("EV-002", "r1", "Merged research claim"))

	var stdout, stderr bytes.Buffer
	code := runProductResearch([]string{"--project-root", dir, "--effort-root", dir, "--merge-evidence", "--json"}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("expected code 1 for ambiguous research profile, got %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	findings, ok := result["findings"].([]any)
	if !ok || len(findings) == 0 {
		t.Fatalf("expected findings for ambiguous research profile, got %v", result["findings"])
	}
	found := false
	for _, raw := range findings {
		finding := raw.(map[string]any)
		if finding["kind"] == "ambiguous_research_profile" && finding["severity"] == "error" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected ambiguous_research_profile error, got %v", findings)
	}
	if result["wrote_evidence_ledger"] != false {
		t.Fatalf("expected no ledger write for ambiguous profile, got %v", result["wrote_evidence_ledger"])
	}
}

func TestProductResearchMergeEvidenceUsesSelectedProfilePolicy(t *testing.T) {
	dir := t.TempDir()
	writeProductResearchProfileFile(t, dir, "a.json", validTestResearchProfileJSONWithPolicy("warn-profile", "warn-profile", "warn"))
	writeProductResearchProfileFile(t, dir, "b.json", validTestResearchProfileJSONWithPolicy("block-profile", "block-profile", "block_research_merge"))
	writeTestFile(t, dir, "evidence/evidence.jsonl", validTestEvidenceJSONL())
	unsupported := strings.Replace(
		validTestResearchCardsJSON("EV-002", "r1", "Unsupported claim"),
		`"kind": "finding"`,
		`"kind": "unsupported_claim"`,
		1,
	)
	writeTestFile(t, dir, "research/outputs/product.json", unsupported)

	var stdout, stderr bytes.Buffer
	code := runProductResearch([]string{"--project-root", dir, "--effort-root", dir, "--merge-evidence", "--profile", "block-profile", "--json"}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("expected code 1 from selected block-profile policy, got %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	if result["profile"] != "block-profile" {
		t.Fatalf("expected selected profile in output, got %v", result["profile"])
	}
	findings, ok := result["findings"].([]any)
	if !ok || len(findings) == 0 {
		t.Fatalf("expected unsupported claim finding, got %v", result["findings"])
	}
	found := false
	for _, raw := range findings {
		finding := raw.(map[string]any)
		if finding["kind"] == "unsupported_claim" && finding["severity"] == "error" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected unsupported_claim error from selected profile, got %v", findings)
	}
	if result["wrote_evidence_ledger"] != false {
		t.Fatalf("expected no ledger write for selected blocking profile, got %v", result["wrote_evidence_ledger"])
	}
}

func TestProductResearchMergeEvidenceRejectsTraversalOutputDir(t *testing.T) {
	dir := t.TempDir()
	writeProductResearchProfile(t, dir, validTestResearchProfileJSON())
	writeTestFile(t, dir, "evidence/evidence.jsonl", validTestEvidenceJSONL())

	var stdout, stderr bytes.Buffer
	code := runProductResearch([]string{"--project-root", dir, "--effort-root", dir, "--merge-evidence", "--research-output-dir", "../outside", "--json"}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("expected code 1 for traversal output dir, got %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	findings, ok := result["findings"].([]any)
	if !ok || len(findings) == 0 {
		t.Fatalf("expected findings for traversal output dir, got %v", result["findings"])
	}
	first := findings[0].(map[string]any)
	if first["kind"] != "invalid_research_output_dir" {
		t.Fatalf("expected invalid_research_output_dir, got %v", first)
	}
}

func TestProductResearchMergeEvidenceRejectsSymlinkOutputDir(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	writeProductResearchProfile(t, dir, validTestResearchProfileJSON())
	writeTestFile(t, dir, "evidence/evidence.jsonl", validTestEvidenceJSONL())
	if err := os.Symlink(outside, filepath.Join(dir, "linked")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := runProductResearch([]string{"--project-root", dir, "--effort-root", dir, "--merge-evidence", "--research-output-dir", "linked", "--json"}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("expected code 1 for symlink output dir, got %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	findings, ok := result["findings"].([]any)
	if !ok || len(findings) == 0 {
		t.Fatalf("expected findings for symlink output dir, got %v", result["findings"])
	}
	first := findings[0].(map[string]any)
	if first["kind"] != "research_output_dir_outside_effort" {
		t.Fatalf("expected research_output_dir_outside_effort, got %v", first)
	}
}

func TestProductResearchPlanRunsJSONSurface(t *testing.T) {
	dir := t.TempDir()
	writeProductResearchProfile(t, dir, validTestResearchProfileJSON())
	var stdout, stderr bytes.Buffer
	code := runProductResearch([]string{"--project-root", dir, "--plan-runs", "--json"}, &stdout, &stderr, nil)
	if code != 0 {
		t.Fatalf("expected code 0, got %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	if result["selected_profile_id"] != "test-profile" {
		t.Fatalf("expected selected_profile_id test-profile, got %v", result["selected_profile_id"])
	}
	if result["profile_count"].(float64) != 1 {
		t.Fatalf("expected profile_count 1, got %v", result["profile_count"])
	}
	if result["run_count"].(float64) != 1 {
		t.Fatalf("expected run_count 1, got %v", result["run_count"])
	}
	runs := result["runs"].([]any)
	if len(runs) != 1 {
		t.Fatalf("expected one run, got %d", len(runs))
	}
	run := runs[0].(map[string]any)
	if run["output_path"] != "research/outputs/test-profile--r1.json" {
		t.Fatalf("unexpected output_path: %v", run["output_path"])
	}
	contract := run["output_contract"].(map[string]any)
	if contract["schema_version"] != "idiom.research_cards.v1" {
		t.Fatalf("unexpected output contract schema: %v", contract["schema_version"])
	}
}

func TestProductResearchPlanRunsRequiresProfileWhenMultipleProfilesExist(t *testing.T) {
	dir := t.TempDir()
	writeProductResearchProfileFile(t, dir, "a.json", validTestResearchProfileJSONWithPolicy("warn-profile", "warn-profile", "warn"))
	writeProductResearchProfileFile(t, dir, "b.json", validTestResearchProfileJSONWithPolicy("block-profile", "block-profile", "block_research_merge"))
	var stdout, stderr bytes.Buffer
	code := runProductResearch([]string{"--project-root", dir, "--plan-runs", "--json"}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("expected code 1 for ambiguous profile, got %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	findings := result["findings"].([]any)
	found := false
	for _, raw := range findings {
		f := raw.(map[string]any)
		if f["kind"] == "ambiguous_research_profile" && f["severity"] == "error" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected ambiguous_research_profile error, got %v", findings)
	}
}

func TestProductResearchPlanRunsUsesSelectedProfile(t *testing.T) {
	dir := t.TempDir()
	writeProductResearchProfileFile(t, dir, "a.json", validTestResearchProfileJSONWithPolicy("warn-profile", "warn-profile", "warn"))
	writeProductResearchProfileFile(t, dir, "b.json", validTestResearchProfileJSONWithPolicy("block-profile", "block-profile", "block_research_merge"))
	var stdout, stderr bytes.Buffer
	code := runProductResearch([]string{"--project-root", dir, "--plan-runs", "--profile", "block-profile", "--json"}, &stdout, &stderr, nil)
	if code != 0 {
		t.Fatalf("expected code 0, got %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	if result["selected_profile_id"] != "block-profile" {
		t.Fatalf("expected selected_profile_id block-profile, got %v", result["selected_profile_id"])
	}
	runs := result["runs"].([]any)
	run := runs[0].(map[string]any)
	if run["output_path"] != "research/outputs/block-profile--r1.json" {
		t.Fatalf("unexpected output path for selected profile: %v", run["output_path"])
	}
}

func TestProductResearchPlanRunsDoesNotWriteEvidenceLedger(t *testing.T) {
	dir := t.TempDir()
	writeProductResearchProfile(t, dir, validTestResearchProfileJSON())
	var stdout, stderr bytes.Buffer
	code := runProductResearch([]string{"--project-root", dir, "--plan-runs", "--json"}, &stdout, &stderr, nil)
	if code != 0 {
		t.Fatalf("expected code 0, got %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "evidence", "evidence.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("expected no evidence/evidence.jsonl write during planning, stat err=%v", err)
	}
}

func TestProductResearchPlanRunsRejectsMergeEvidenceFlag(t *testing.T) {
	dir := t.TempDir()
	writeProductResearchProfile(t, dir, validTestResearchProfileJSON())
	writeTestFile(t, dir, "evidence/evidence.jsonl", validTestEvidenceJSONL())
	writeTestFile(t, dir, "research/outputs/technical.json", validTestResearchCardsJSON("EV-002", "r1", "Should not merge"))

	var stdout, stderr bytes.Buffer
	code := runProductResearch([]string{"--project-root", dir, "--effort-root", dir, "--plan-runs", "--merge-evidence", "--json"}, &stdout, &stderr, nil)
	if code != 2 {
		t.Fatalf("expected usage code 2 for mutually exclusive flags, got %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "mutually exclusive") {
		t.Fatalf("expected mutual exclusion error, got stderr=%q", stderr.String())
	}
	merged, err := os.ReadFile(filepath.Join(dir, "evidence", "evidence.jsonl"))
	if err != nil {
		t.Fatalf("read evidence ledger: %v", err)
	}
	cards, err := productworkflow.ParseEvidenceCardsJSONL(merged)
	if err != nil {
		t.Fatalf("evidence ledger should remain parseable: %v", err)
	}
	if len(cards) != 1 || cards[0].CardID != "EV-001" {
		t.Fatalf("expected merge not to run, got cards %#v", cards)
	}
}

func TestProductResearchRenderPromptsJSONSurfaceWritesPromptFilesOnly(t *testing.T) {
	dir := t.TempDir()
	writeProductResearchProfile(t, dir, validTestResearchProfileJSON())

	var stdout, stderr bytes.Buffer
	code := runProductResearch([]string{
		"--project-root", dir,
		"--effort-root", dir,
		"--render-prompts",
		"--research-prompt-dir", "research/prompts",
		"--json",
	}, &stdout, &stderr, nil)
	if code != 0 {
		t.Fatalf("expected code 0, got %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	if result["status"] != "pass" {
		t.Fatalf("expected status pass, got %v", result["status"])
	}
	if result["prompt_write_count"].(float64) != 1 {
		t.Fatalf("expected prompt_write_count 1, got %v", result["prompt_write_count"])
	}
	plan := result["plan"].(map[string]any)
	runs := plan["runs"].([]any)
	run := runs[0].(map[string]any)
	if run["prompt_path"] != "research/prompts/test-profile--r1.prompt.txt" {
		t.Fatalf("unexpected prompt path: %v", run["prompt_path"])
	}
	command := run["command"].(string)
	if !strings.Contains(command, "idiom artifact --prompt-file research/prompts/test-profile--r1.prompt.txt") || !strings.Contains(command, "--no-tools --mechanical-repair") {
		t.Fatalf("unexpected command recipe: %q", command)
	}
	promptData, err := os.ReadFile(filepath.Join(dir, "research", "prompts", "test-profile--r1.prompt.txt"))
	if err != nil {
		t.Fatalf("read prompt file: %v", err)
	}
	prompt := string(promptData)
	if !strings.Contains(prompt, "Write JSON only.") || !strings.Contains(prompt, "Do not call tools or inspect the workspace") {
		t.Fatalf("prompt missing no-tools/json requirements:\n%s", prompt)
	}
	if _, err := os.Stat(filepath.Join(dir, "research", "outputs", "test-profile--r1.json")); !os.IsNotExist(err) {
		t.Fatalf("expected no research output write, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "evidence", "evidence.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("expected no evidence write, stat err=%v", err)
	}
}

func TestProductResearchRenderPromptsUsesSelectedProfile(t *testing.T) {
	dir := t.TempDir()
	writeProductResearchProfileFile(t, dir, "a.json", validTestResearchProfileJSONWithPolicy("warn-profile", "warn-profile", "warn"))
	writeProductResearchProfileFile(t, dir, "b.json", validTestResearchProfileJSONWithPolicy("block-profile", "block-profile", "block_research_merge"))
	var stdout, stderr bytes.Buffer
	code := runProductResearch([]string{
		"--project-root", dir,
		"--effort-root", dir,
		"--render-prompts",
		"--profile", "block-profile",
		"--json",
	}, &stdout, &stderr, nil)
	if code != 0 {
		t.Fatalf("expected code 0, got %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	plan := result["plan"].(map[string]any)
	if plan["selected_profile_id"] != "block-profile" {
		t.Fatalf("expected selected_profile_id block-profile, got %v", plan["selected_profile_id"])
	}
}

func TestProductResearchRenderPromptsRejectsMutuallyExclusiveModes(t *testing.T) {
	dir := t.TempDir()
	writeProductResearchProfile(t, dir, validTestResearchProfileJSON())
	var stdout, stderr bytes.Buffer
	code := runProductResearch([]string{
		"--project-root", dir,
		"--effort-root", dir,
		"--render-prompts",
		"--plan-runs",
		"--json",
	}, &stdout, &stderr, nil)
	if code != 2 {
		t.Fatalf("expected usage code 2 for mutually exclusive flags, got %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "mutually exclusive") {
		t.Fatalf("expected mutual exclusion error, got stderr=%q", stderr.String())
	}
}

func TestProductResearchRenderPromptsFailsClosedWhenProfilesMissing(t *testing.T) {
	dir := t.TempDir()

	var stdout, stderr bytes.Buffer
	code := runProductResearch([]string{
		"--project-root", dir,
		"--effort-root", dir,
		"--render-prompts",
		"--json",
	}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("expected code 1 when profiles are missing, got %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	if result["status"] != "blocked" {
		t.Fatalf("expected blocked status, got %v", result["status"])
	}
	if result["prompt_write_count"].(float64) != 0 {
		t.Fatalf("expected no prompt writes, got %v", result["prompt_write_count"])
	}
	plan := result["plan"].(map[string]any)
	findings := plan["findings"].([]any)
	first := findings[0].(map[string]any)
	if first["kind"] != "missing_research_profile" {
		t.Fatalf("expected missing_research_profile, got %v", first)
	}
	if _, err := os.Stat(filepath.Join(dir, "research", "prompts")); !os.IsNotExist(err) {
		t.Fatalf("expected no prompt directory write, stat err=%v", err)
	}
}

func TestProductResearchRenderPromptsRejectsSymlinkPromptDir(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	writeProductResearchProfile(t, dir, validTestResearchProfileJSON())
	if err := os.Symlink(outside, filepath.Join(dir, "linked")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := runProductResearch([]string{
		"--project-root", dir,
		"--effort-root", dir,
		"--render-prompts",
		"--research-prompt-dir", "linked/prompts",
		"--json",
	}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("expected code 1 for symlink prompt dir, got %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	plan := result["plan"].(map[string]any)
	findings := plan["findings"].([]any)
	first := findings[0].(map[string]any)
	if first["kind"] != "research_prompt_dir_outside_effort" {
		t.Fatalf("expected research_prompt_dir_outside_effort, got %v", first)
	}
	if _, err := os.Stat(filepath.Join(outside, "prompts")); !os.IsNotExist(err) {
		t.Fatalf("expected no prompt directory outside effort, stat err=%v", err)
	}
}

func TestProductResearchRenderPromptsRejectsSymlinkPromptFileBeforePartialWrites(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	writeProductResearchProfileFile(t, dir, "profile.json", `{
  "schema_version": "idiom.research_profile.v1",
  "name": "multi",
  "profile_id": "multi",
  "researchers": [
    {"id": "a", "alignment": "technical", "question": "qa", "required_sources": ["repo"], "allowed_capabilities": ["context_query"], "output": "evidence_cards"},
    {"id": "b", "alignment": "product", "question": "qb", "required_sources": ["repo"], "allowed_capabilities": ["context_query"], "output": "evidence_cards"}
  ],
  "merge_policy": {"conflicts": "preserve", "unsupported_claims": "block_research_merge", "stale_local_context": "warn"}
}`)
	if err := os.MkdirAll(filepath.Join(dir, "research", "prompts"), 0o755); err != nil {
		t.Fatalf("mkdir prompts: %v", err)
	}
	if err := os.Symlink(filepath.Join(outside, "b.prompt.txt"), filepath.Join(dir, "research", "prompts", "multi--b.prompt.txt")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := runProductResearch([]string{
		"--project-root", dir,
		"--effort-root", dir,
		"--render-prompts",
		"--json",
	}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("expected code 1 for symlink prompt file, got %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	plan := result["plan"].(map[string]any)
	findings := plan["findings"].([]any)
	first := findings[0].(map[string]any)
	if first["kind"] != "research_prompt_symlink" {
		t.Fatalf("expected research_prompt_symlink, got %v", first)
	}
	if _, err := os.Stat(filepath.Join(dir, "research", "prompts", "multi--a.prompt.txt")); !os.IsNotExist(err) {
		t.Fatalf("expected no partial prompt write for first run, stat err=%v", err)
	}
}

func TestProductPRDRenderPromptWritesPromptOnly(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "evidence/evidence.jsonl", validTestEvidenceJSONL())
	writeTestFile(t, dir, "tdd.json", validTestTDDJSON())

	var stdout, stderr bytes.Buffer
	code := runProductPRD([]string{
		"--effort-root", dir,
		"--render-prompt",
		"--json",
	}, &stdout, &stderr, nil)
	if code != 0 {
		t.Fatalf("expected code 0, got %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	if result["status"] != "pass" {
		t.Fatalf("expected status pass, got %v", result["status"])
	}
	if strings.Contains(stdout.String(), "Draft a Product Requirements Document") {
		t.Fatalf("JSON output should not include rendered prompt body:\n%s", stdout.String())
	}
	if result["prompt_write_count"].(float64) != 1 {
		t.Fatalf("expected prompt_write_count=1, got %v", result["prompt_write_count"])
	}
	promptData, err := os.ReadFile(filepath.Join(dir, "prd.prompt.md"))
	if err != nil {
		t.Fatalf("read prompt: %v", err)
	}
	prompt := string(promptData)
	if !strings.Contains(prompt, "PRD parser/audit contract") || !strings.Contains(prompt, "Do not claim acceptance") || !strings.Contains(prompt, "Known TDD obligations") || !strings.Contains(prompt, "TDD-001") {
		t.Fatalf("missing PRD contract guidance in prompt:\n%s", prompt)
	}
	if _, err := os.Stat(filepath.Join(dir, "prd.md")); !os.IsNotExist(err) {
		t.Fatalf("expected no prd.md write, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "events", "prd-draft.events.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("expected no events write, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "reports", "prd-draft.report.json")); !os.IsNotExist(err) {
		t.Fatalf("expected no report write, stat err=%v", err)
	}
}

func TestProductPRDRenderPromptFailsClosedOnInvalidEvidenceLedger(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "evidence/evidence.jsonl", "{bad json")
	writeTestFile(t, dir, "tdd.json", validTestTDDJSON())
	var stdout, stderr bytes.Buffer
	code := runProductPRD([]string{
		"--effort-root", dir,
		"--render-prompt",
		"--json",
	}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("expected code 1 for invalid evidence ledger, got %d", code)
	}
	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	if result["status"] != "blocked" {
		t.Fatalf("expected blocked status, got %v", result["status"])
	}
	plan := result["plan"].(map[string]any)
	findings := plan["findings"].([]any)
	if findings[0].(map[string]any)["kind"] != "validation_error" {
		t.Fatalf("expected validation_error finding, got %#v", findings)
	}
	if _, err := os.Stat(filepath.Join(dir, "prd.prompt.md")); !os.IsNotExist(err) {
		t.Fatalf("expected no prompt write, stat err=%v", err)
	}
}

func TestProductPRDRenderPromptFailsClosedWhenEvidenceLedgerMissing(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "tdd.json", validTestTDDJSON())
	var stdout, stderr bytes.Buffer
	code := runProductPRD([]string{
		"--effort-root", dir,
		"--render-prompt",
		"--json",
	}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("expected code 1 for missing evidence ledger, got %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	plan := result["plan"].(map[string]any)
	findings := plan["findings"].([]any)
	if findings[0].(map[string]any)["kind"] != "missing_evidence_ledger" {
		t.Fatalf("expected missing_evidence_ledger finding, got %#v", findings)
	}
	if _, err := os.Stat(filepath.Join(dir, "prd.prompt.md")); !os.IsNotExist(err) {
		t.Fatalf("expected no prompt write, stat err=%v", err)
	}
}

func TestProductPRDRenderPromptRejectsUnsafePromptOrOutputPath(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "evidence/evidence.jsonl", validTestEvidenceJSONL())
	writeTestFile(t, dir, "tdd.json", validTestTDDJSON())

	var stdout, stderr bytes.Buffer
	code := runProductPRD([]string{
		"--effort-root", dir,
		"--render-prompt",
		"--prd-prompt", "../bad.prompt.md",
		"--json",
	}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("expected blocked code for traversal prompt path, got %d", code)
	}
	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output should be valid JSON: %v", err)
	}
	plan := result["plan"].(map[string]any)
	findings := plan["findings"].([]any)
	if findings[0].(map[string]any)["kind"] != "invalid_prd_prompt_path" {
		t.Fatalf("expected invalid_prd_prompt_path, got %#v", findings)
	}
	stdout.Reset()
	stderr.Reset()
	code = runProductPRD([]string{
		"--effort-root", dir,
		"--render-prompt",
		"--prd-output", "../bad.md",
		"--json",
	}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("expected blocked code for traversal output path, got %d", code)
	}
}

func TestProductValidateArtifactFailsClosedForInvalidGeneratedPRD(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "prd.md", `---
schema_version: idiom.prd.v1
prd_id: prd-test
status: draft
task_id: impl-test
created_at: 2026-05-17T00:00:00Z
---

# PRD: Test

## Requirements

| id | priority | statement |
| --- | --- | --- |
| REQ-1 | must | do it |
`)
	var stdout, stderr bytes.Buffer
	code := runProductValidateArtifact([]string{
		"--kind", "prd",
		"--path", filepath.Join(dir, "prd.md"),
		"--json",
	}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("expected code 1 for parser-invalid PRD, got %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var out productValidateArtifactOutput
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	if out.Status != "fail" || !strings.Contains(out.Error, "PRD has no requirements") {
		t.Fatalf("expected PRD parser failure, got %#v", out)
	}
}

func TestProductValidateArtifactFailsClosedForUnknownPRDTDDObligation(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "tdd.json", validTestTDDJSON())
	writeTestFile(t, dir, "prd.md", strings.Replace(validTestPRDMarkdown(), `["TDD-001"]`, `["The generated test must pass."]`, 1))
	var stdout, stderr bytes.Buffer
	code := runProductValidateArtifact([]string{
		"--kind", "prd",
		"--path", filepath.Join(dir, "prd.md"),
		"--tdd-ledger", filepath.Join(dir, "tdd.json"),
		"--json",
	}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("expected code 1 for traceability-invalid PRD, got %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var out productValidateArtifactOutput
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	if out.Status != "fail" || !strings.Contains(out.Error, "references TDD obligation") {
		t.Fatalf("expected PRD TDD cross-link failure, got %#v", out)
	}
}

func TestProductADRRenderPromptWritesPromptOnly(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "evidence/evidence.jsonl", validTestEvidenceJSONL())
	writeTestFile(t, dir, "prd.md", validTestPRDMarkdown())

	var stdout, stderr bytes.Buffer
	code := runProductADR([]string{
		"--effort-root", dir,
		"--render-prompt",
		"--json",
	}, &stdout, &stderr, nil)
	if code != 0 {
		t.Fatalf("expected code 0, got %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	if result["status"] != "pass" {
		t.Fatalf("expected status pass, got %v", result["status"])
	}
	if strings.Contains(stdout.String(), "Draft an Architecture Decision Record") {
		t.Fatalf("JSON output should not include rendered prompt body:\n%s", stdout.String())
	}
	if result["prompt_write_count"].(float64) != 1 {
		t.Fatalf("expected prompt_write_count=1, got %v", result["prompt_write_count"])
	}
	promptData, err := os.ReadFile(filepath.Join(dir, "adrs", "adr.prompt.md"))
	if err != nil {
		t.Fatalf("read prompt: %v", err)
	}
	prompt := string(promptData)
	if !strings.Contains(prompt, "ADR parser/audit contract") || !strings.Contains(prompt, "Do not claim acceptance, approval, or supersession.") {
		t.Fatalf("missing ADR contract guidance in prompt:\n%s", prompt)
	}
	if _, err := os.Stat(filepath.Join(dir, "adrs", "ADR-draft.md")); !os.IsNotExist(err) {
		t.Fatalf("expected no ADR write, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "events", "adr-draft.events.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("expected no events write, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "reports", "adr-draft.report.json")); !os.IsNotExist(err) {
		t.Fatalf("expected no report write, stat err=%v", err)
	}
}

func TestProductADRRenderPromptFailsClosedOnInvalidEvidenceOrPRD(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "evidence/evidence.jsonl", "{bad json")
	writeTestFile(t, dir, "prd.md", validTestPRDMarkdown())
	var stdout, stderr bytes.Buffer
	code := runProductADR([]string{
		"--effort-root", dir,
		"--render-prompt",
		"--json",
	}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("expected code 1 for invalid evidence, got %d", code)
	}
	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	plan := result["plan"].(map[string]any)
	findings := plan["findings"].([]any)
	if findings[0].(map[string]any)["kind"] != "validation_error" {
		t.Fatalf("expected validation_error finding, got %#v", findings)
	}

	dir2 := t.TempDir()
	writeTestFile(t, dir2, "evidence/evidence.jsonl", validTestEvidenceJSONL())
	writeTestFile(t, dir2, "prd.md", "not valid prd")
	stdout.Reset()
	stderr.Reset()
	code = runProductADR([]string{
		"--effort-root", dir2,
		"--render-prompt",
		"--json",
	}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("expected code 1 for invalid prd, got %d", code)
	}
	if _, err := os.Stat(filepath.Join(dir2, "adrs", "adr.prompt.md")); !os.IsNotExist(err) {
		t.Fatalf("expected no prompt write for invalid prd, stat err=%v", err)
	}
}

func TestProductADRRenderPromptFailsClosedWhenInputsMissing(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := runProductADR([]string{
		"--effort-root", dir,
		"--render-prompt",
		"--json",
	}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("expected code 1 for missing inputs, got %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	plan := result["plan"].(map[string]any)
	findings := plan["findings"].([]any)
	var sawEvidence, sawPRD bool
	for _, item := range findings {
		kind := item.(map[string]any)["kind"]
		if kind == "missing_evidence_ledger" {
			sawEvidence = true
		}
		if kind == "missing_prd" {
			sawPRD = true
		}
	}
	if !sawEvidence || !sawPRD {
		t.Fatalf("expected missing evidence and PRD findings, got %#v", findings)
	}
	if _, err := os.Stat(filepath.Join(dir, "adrs", "adr.prompt.md")); !os.IsNotExist(err) {
		t.Fatalf("expected no prompt write for missing inputs, stat err=%v", err)
	}
}

func TestProductADRRenderPromptRejectsUnsafePromptOrOutputPath(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "evidence/evidence.jsonl", validTestEvidenceJSONL())
	writeTestFile(t, dir, "prd.md", validTestPRDMarkdown())

	var stdout, stderr bytes.Buffer
	code := runProductADR([]string{
		"--effort-root", dir,
		"--render-prompt",
		"--adr-prompt", "../bad.prompt.md",
		"--json",
	}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("expected blocked code for traversal prompt path, got %d", code)
	}
	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output should be valid JSON: %v", err)
	}
	plan := result["plan"].(map[string]any)
	findings := plan["findings"].([]any)
	if findings[0].(map[string]any)["kind"] != "invalid_adr_prompt_path" {
		t.Fatalf("expected invalid_adr_prompt_path, got %#v", findings)
	}
	stdout.Reset()
	stderr.Reset()
	code = runProductADR([]string{
		"--effort-root", dir,
		"--render-prompt",
		"--adr-output", "../bad.md",
		"--json",
	}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("expected blocked code for traversal output path, got %d", code)
	}
}

func TestProductValidateArtifactFailsClosedForInvalidGeneratedADR(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "adr.md", strings.Replace(validTestADRMarkdown(), "coverage_tier: unit", "coverage_tier: must", 1))
	var stdout, stderr bytes.Buffer
	code := runProductValidateArtifact([]string{
		"--kind", "adr",
		"--path", filepath.Join(dir, "adr.md"),
		"--json",
	}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("expected code 1 for parser-invalid ADR, got %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var out productValidateArtifactOutput
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	if out.Status != "fail" || !strings.Contains(out.Error, "invalid coverage_tier") {
		t.Fatalf("expected ADR coverage tier failure, got %#v", out)
	}
}

func TestProductValidateArtifactFailsClosedForUnknownADRTDDObligation(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "prd.md", validTestPRDMarkdown())
	writeTestFile(t, dir, "tdd.json", validTestTDDJSON())
	writeTestFile(t, dir, "adr.md", strings.Replace(validTestADRMarkdown(), "TDD-001", "The generated test must pass.", 1))
	var stdout, stderr bytes.Buffer
	code := runProductValidateArtifact([]string{
		"--kind", "adr",
		"--path", filepath.Join(dir, "adr.md"),
		"--prd", filepath.Join(dir, "prd.md"),
		"--tdd-ledger", filepath.Join(dir, "tdd.json"),
		"--json",
	}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("expected code 1 for traceability-invalid ADR, got %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var out productValidateArtifactOutput
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	if out.Status != "fail" || !strings.Contains(out.Error, "tdd_obligation") {
		t.Fatalf("expected ADR TDD cross-link failure, got %#v", out)
	}
}

func TestProductTDDPlanObligationsSynthesizesPlan(t *testing.T) {
	dir := writeValidTestEffort(t)
	acceptedPRD := strings.Replace(validTestPRDMarkdown(), "status: draft", "status: accepted", 1)
	acceptedPRD = strings.Replace(acceptedPRD, "created_at: 2026-05-17T00:00:00Z", "created_at: 2026-05-17T00:00:00Z\naccepted_at: 2026-05-18T00:00:00Z\naccepted_by: reviewer\nacceptance_profile: human-review", 1)
	writeTestFile(t, dir, "prd.md", acceptedPRD)
	beforeTDD, err := os.ReadFile(filepath.Join(dir, "tdd.json"))
	if err != nil {
		t.Fatal(err)
	}
	beforePRD, err := os.ReadFile(filepath.Join(dir, "prd.md"))
	if err != nil {
		t.Fatal(err)
	}
	beforeADR, err := os.ReadFile(filepath.Join(dir, "adrs", "ADR-001-test.md"))
	if err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := runProductTDD([]string{
		"--effort-root", dir,
		"--plan-obligations",
		"--json",
	}, &stdout, &stderr, nil)
	if code != 0 {
		t.Fatalf("expected code 0, got %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var out struct {
		Status string `json:"status"`
		Plan   struct {
			Obligations []productworkflow.TDDObligationPlanItem    `json:"obligations"`
			Findings    []productworkflow.TDDObligationPlanFinding `json:"findings"`
		} `json:"plan"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	if out.Status != "pass" {
		t.Fatalf("expected pass, got %q", out.Status)
	}
	if len(out.Plan.Obligations) == 0 {
		t.Fatalf("expected obligations, got %#v", out.Plan)
	}
	afterTDD, err := os.ReadFile(filepath.Join(dir, "tdd.json"))
	if err != nil {
		t.Fatal(err)
	}
	afterPRD, err := os.ReadFile(filepath.Join(dir, "prd.md"))
	if err != nil {
		t.Fatal(err)
	}
	afterADR, err := os.ReadFile(filepath.Join(dir, "adrs", "ADR-001-test.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(beforeTDD, afterTDD) || !bytes.Equal(beforePRD, afterPRD) || !bytes.Equal(beforeADR, afterADR) {
		t.Fatal("product tdd plan-obligations must not mutate TDD, PRD, or ADR artifacts")
	}
}

func TestProductTDDPlanObligationsBlocksWhenNoAcceptedADRMapsRequirement(t *testing.T) {
	dir := writeValidTestEffort(t)
	acceptedPRD := strings.Replace(validTestPRDMarkdown(), "status: draft", "status: accepted", 1)
	acceptedPRD = strings.Replace(acceptedPRD, "created_at: 2026-05-17T00:00:00Z", "created_at: 2026-05-17T00:00:00Z\naccepted_at: 2026-05-18T00:00:00Z\naccepted_by: reviewer\nacceptance_profile: human-review", 1)
	writeTestFile(t, dir, "prd.md", acceptedPRD)
	writeTestFile(t, dir, "adrs/index.json", strings.Replace(validTestADRIndexJSON(), "REQ-TEST-001", "REQ-OTHER-001", 1))
	writeTestFile(t, dir, "adrs/ADR-001-test.md", strings.Replace(validTestADRMarkdown(), "REQ-TEST-001", "REQ-OTHER-001", 1))

	var stdout, stderr bytes.Buffer
	code := runProductTDD([]string{
		"--effort-root", dir,
		"--plan-obligations",
		"--json",
	}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("expected code 1, got %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var out struct {
		Status string                                     `json:"status"`
		Plan   productworkflow.TDDObligationSynthesisPlan `json:"plan"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	if out.Status != "blocked" {
		t.Fatalf("expected blocked, got %q", out.Status)
	}
	found := false
	for _, finding := range out.Plan.Findings {
		if finding.Kind == "missing_accepted_adr_for_prd_requirement" && finding.File == "prd.md" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected missing accepted ADR finding, got %#v", out.Plan.Findings)
	}
}

func TestProductTDDPlanObligationsFailsClosedOnMissingInputs(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := runProductTDD([]string{
		"--effort-root", dir,
		"--plan-obligations",
		"--json",
	}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("expected code 1, got %d", code)
	}
	var out struct {
		Status string                                     `json:"status"`
		Plan   productworkflow.TDDObligationSynthesisPlan `json:"plan"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	if out.Status != "blocked" {
		t.Fatalf("expected blocked, got %q", out.Status)
	}
	if len(out.Plan.Findings) == 0 {
		t.Fatal("expected fail-closed findings")
	}
}

func TestProductTDDPlanObligationsUsageErrors(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runProductTDD([]string{"--effort-root", "."}, &stdout, &stderr, nil)
	if code != 2 {
		t.Fatalf("expected code 2 without --plan-obligations, got %d", code)
	}
}

func TestProductTDDJSONUsageErrors(t *testing.T) {
	assertUsageError := func(t *testing.T, args []string, wantCode string, wantPath string, wantMessage string) {
		t.Helper()
		var stdout, stderr bytes.Buffer
		code := runProductTDD(args, &stdout, &stderr, nil)
		if code != 2 {
			t.Fatalf("expected code 2 for args %v, got %d stdout=%s stderr=%s", args, code, stdout.String(), stderr.String())
		}
		if strings.TrimSpace(stderr.String()) != "" {
			t.Fatalf("expected JSON usage error to keep stderr empty, got %q", stderr.String())
		}
		var result productStatusUsageJSONResult
		if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
			t.Fatalf("usage error output should be valid JSON: %v\n%s", err, stdout.String())
		}
		if result.Status != "blocked" || len(result.Blockers) != 1 {
			t.Fatalf("expected blocked status with one blocker, got %#v", result)
		}
		blocker := result.Blockers[0]
		if blocker.Code != wantCode || blocker.Path != wantPath || blocker.Message != wantMessage {
			t.Fatalf("unexpected usage blocker: %#v", blocker)
		}
		if strings.TrimSpace(result.NextValidAction) == "" {
			t.Fatalf("expected next valid action, got %#v", result)
		}
	}

	t.Run("missing effort-root", func(t *testing.T) {
		assertUsageError(t, []string{"--json", "--plan-obligations"}, "tdd_missing_effort_root", "--effort-root", "--effort-root is required")
	})

	t.Run("missing mode flag", func(t *testing.T) {
		assertUsageError(t, []string{"--json", "--effort-root", "."}, "tdd_missing_mode_flag", "mode", "exactly one of --plan-obligations or --evaluate-coverage is required")
	})

	t.Run("multiple mode flags", func(t *testing.T) {
		assertUsageError(t, []string{"--json", "--effort-root", ".", "--plan-obligations", "--evaluate-coverage"}, "tdd_multiple_mode_flags", "mode", "exactly one of --plan-obligations or --evaluate-coverage is required")
	})

	t.Run("flag parse error", func(t *testing.T) {
		assertUsageError(t, []string{"--json", "--unknown-flag"}, "tdd_flag_parse_error", "flags", "flag provided but not defined: -unknown-flag")
	})
}

func TestProductTDDCoverageGatePassesDeterministicFixture(t *testing.T) {
	dir := writeValidTestEffort(t)
	acceptedPRD := strings.Replace(validTestPRDMarkdown(), "status: draft", "status: accepted", 1)
	acceptedPRD = strings.Replace(acceptedPRD, "created_at: 2026-05-17T00:00:00Z", "created_at: 2026-05-17T00:00:00Z\naccepted_at: 2026-05-18T00:00:00Z\naccepted_by: reviewer\nacceptance_profile: human-review", 1)
	writeTestFile(t, dir, "prd.md", acceptedPRD)
	writeTestFile(t, dir, "tdd.json", `{
		"schema_version":"idiom.tdd_obligations.v2.fixture",
		"effort_id":"test-effort",
		"task_id":"impl-test",
		"coverage_tier":"behavior_standard",
		"coverage":{
			"ears_total":1,
			"ears_mapped":1,
			"bdd_total":1,
			"bdd_with_concrete_data":1,
			"mutation_min_score":80,
			"mutation_score":90,
			"gates_passed":["contract"]
		},
		"obligations":[{
			"id":"TDD-001","type":"acceptance","behavior":"test","owner":"test","command":"go test","required_before":"close","status":"pending"
		}]
	}`)
	beforeTDD, err := os.ReadFile(filepath.Join(dir, "tdd.json"))
	if err != nil {
		t.Fatal(err)
	}
	beforePRD, err := os.ReadFile(filepath.Join(dir, "prd.md"))
	if err != nil {
		t.Fatal(err)
	}
	beforeADR, err := os.ReadFile(filepath.Join(dir, "adrs", "ADR-001-test.md"))
	if err != nil {
		t.Fatal(err)
	}
	beforeTraceability, err := os.ReadFile(filepath.Join(dir, "evidence", "traceability_matrix.json"))
	if err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := runProductTDD([]string{
		"--effort-root", dir,
		"--evaluate-coverage",
		"--json",
	}, &stdout, &stderr, nil)
	if code != 0 {
		t.Fatalf("expected code 0, got %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var out struct {
		Status     string                                 `json:"status"`
		Evaluation productworkflow.CoverageGateEvaluation `json:"evaluation"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	if out.Status != "pass" || out.Evaluation.Status != "pass" {
		t.Fatalf("expected pass output, got %#v", out)
	}
	afterTDD, err := os.ReadFile(filepath.Join(dir, "tdd.json"))
	if err != nil {
		t.Fatal(err)
	}
	afterPRD, err := os.ReadFile(filepath.Join(dir, "prd.md"))
	if err != nil {
		t.Fatal(err)
	}
	afterADR, err := os.ReadFile(filepath.Join(dir, "adrs", "ADR-001-test.md"))
	if err != nil {
		t.Fatal(err)
	}
	afterTraceability, err := os.ReadFile(filepath.Join(dir, "evidence", "traceability_matrix.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(beforeTDD, afterTDD) || !bytes.Equal(beforePRD, afterPRD) || !bytes.Equal(beforeADR, afterADR) || !bytes.Equal(beforeTraceability, afterTraceability) {
		t.Fatal("product tdd evaluate-coverage must not mutate TDD, PRD, ADR, or traceability artifacts")
	}
}

func TestProductTDDCoverageGatePassesCurrentDurableEffortFixture(t *testing.T) {
	root := repoRoot(t)
	effortRoot := filepath.Join(root, "docs", "product-workflows", "efforts", "closed", "prd-adr-brief-workflow")
	var stdout, stderr bytes.Buffer
	code := runProductTDD([]string{
		"--effort-root", effortRoot,
		"--evaluate-coverage",
		"--json",
	}, &stdout, &stderr, nil)
	if code != 0 {
		t.Fatalf("expected durable coverage gate to pass, got code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var out struct {
		Status     string                                 `json:"status"`
		Evaluation productworkflow.CoverageGateEvaluation `json:"evaluation"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	if out.Status != "pass" || out.Evaluation.Status != "pass" {
		t.Fatalf("expected pass output, got %#v", out)
	}
}

func TestProductTDDCoverageGateBlocksUnknownPRDTDDObligation(t *testing.T) {
	dir := writeValidTestEffort(t)
	acceptedPRD := strings.Replace(validTestPRDMarkdown(), "status: draft", "status: accepted", 1)
	acceptedPRD = strings.Replace(acceptedPRD, "created_at: 2026-05-17T00:00:00Z", "created_at: 2026-05-17T00:00:00Z\naccepted_at: 2026-05-18T00:00:00Z\naccepted_by: reviewer\nacceptance_profile: human-review", 1)
	writeTestFile(t, dir, "prd.md", acceptedPRD)
	writeTestFile(t, dir, "tdd.json", strings.Replace(validTestTDDJSON(), `"id":"TDD-001"`, `"id":"TDD-OTHER"`, 1))
	writeTestFile(t, dir, "implementation_ledger.jsonl", implementationLedgerWithoutTDDEvidenceJSONL())

	var stdout, stderr bytes.Buffer
	code := runProductTDD([]string{
		"--effort-root", dir,
		"--evaluate-coverage",
		"--json",
	}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("expected code 1, got %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var out struct {
		Status     string                                 `json:"status"`
		Evaluation productworkflow.CoverageGateEvaluation `json:"evaluation"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	if out.Status != "blocked" {
		t.Fatalf("expected blocked status, got %q", out.Status)
	}
	found := false
	for _, blocker := range out.Evaluation.Blockers {
		if blocker.Criterion == "evidence_acceptance" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected evidence_acceptance blocker, got %#v", out.Evaluation.Blockers)
	}
}

func TestProductTDDCoverageGateIgnoresOtherTaskHistoricalTDDObligations(t *testing.T) {
	dir := writeValidTestEffort(t)
	acceptedPRD := strings.Replace(validTestPRDMarkdown(), "status: draft", "status: accepted", 1)
	acceptedPRD = strings.Replace(acceptedPRD, "created_at: 2026-05-17T00:00:00Z", "created_at: 2026-05-17T00:00:00Z\naccepted_at: 2026-05-18T00:00:00Z\naccepted_by: reviewer\nacceptance_profile: human-review", 1)
	acceptedPRD = strings.ReplaceAll(acceptedPRD, "TDD-001", "TDD-OTHER-TASK")
	writeTestFile(t, dir, "prd.md", acceptedPRD)
	writeTestFile(t, dir, "adrs/ADR-001-test.md", strings.ReplaceAll(validTestADRMarkdown(), "TDD-001", "TDD-OTHER-TASK"))
	writeTestFile(t, dir, "evidence/traceability_matrix.json", strings.ReplaceAll(validTestTraceabilityMatrixJSON(), "TDD-001", "TDD-OTHER-TASK"))
	writeTestFile(t, dir, "tdd.json", strings.Replace(validTestTDDJSON(), `"id":"TDD-001"`, `"id":"TDD-DIFFERENT"`, 1))
	writeTestFile(t, dir, "implementation_ledger.jsonl", validTestImplementationLedgerJSONL()+"\n"+`{"record_type":"verification_completed","task_id":"other-task","effort_id":"test-effort","timestamp":"2026-05-18T00:06:00Z","tdd_obligations_satisfied":["TDD-OTHER-TASK"]}`)

	var stdout, stderr bytes.Buffer
	code := runProductTDD([]string{
		"--effort-root", dir,
		"--evaluate-coverage",
		"--json",
	}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("expected code 1, got %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var out struct {
		Status     string                                 `json:"status"`
		Evaluation productworkflow.CoverageGateEvaluation `json:"evaluation"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	if out.Status != "blocked" {
		t.Fatalf("expected blocked status, got %q", out.Status)
	}
}

func TestProductTDDCoverageGatePreflightsADRSupersessionLinks(t *testing.T) {
	dir := writeValidTestEffort(t)
	acceptedPRD := strings.Replace(validTestPRDMarkdown(), "status: draft", "status: accepted", 1)
	acceptedPRD = strings.Replace(acceptedPRD, "created_at: 2026-05-17T00:00:00Z", "created_at: 2026-05-17T00:00:00Z\naccepted_at: 2026-05-18T00:00:00Z\naccepted_by: reviewer\nacceptance_profile: human-review", 1)
	writeTestFile(t, dir, "prd.md", acceptedPRD)
	writeTestFile(t, dir, "adrs/index.json", `{"schema_version":"v1","effort_id":"test-effort","adrs":[{"adr_id":"ADR-001","title":"Test ADR 1","status":"accepted","path":"ADR-001-test.md","coverage_tier":"unit","prd_refs":["REQ-TEST-001"]},{"adr_id":"ADR-002","title":"Test ADR 2","status":"accepted","path":"ADR-002-test.md","coverage_tier":"unit","prd_refs":["REQ-TEST-001"]}]}`)
	writeTestFile(t, dir, "adrs/ADR-001-test.md", strings.Replace(validTestADRMarkdown(), "coverage_tier: unit", "coverage_tier: unit\nsupersedes:\n  - ADR-002", 1))
	writeTestFile(t, dir, "adrs/ADR-002-test.md", strings.Replace(validTestADRMarkdown(), "adr_id: ADR-001", "adr_id: ADR-002", 1))
	writeTestFile(t, dir, "evidence/traceability_matrix.json", `{
		"schema_version":"idiom.traceability_matrix.v1.fixture",
		"effort_id":"test-effort",
		"task_id":"impl-test",
		"created_at":"2026-05-17T16:00:00Z",
		"matrix":[
			{"evidence_card":"EV-001","prd_requirement":"REQ-TEST-001","adr":"ADR-001","tdd_obligation":"TDD-001","brief":"briefs/test.md","report":"reports/test.json"},
			{"evidence_card":"EV-001","prd_requirement":"REQ-TEST-001","adr":"ADR-002","tdd_obligation":"TDD-001","brief":"briefs/test.md","report":"reports/test.json"}
		]
	}`)

	var stdout, stderr bytes.Buffer
	code := runProductTDD([]string{
		"--effort-root", dir,
		"--evaluate-coverage",
		"--json",
	}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("expected code 1, got %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var out struct {
		Status     string                                 `json:"status"`
		Evaluation productworkflow.CoverageGateEvaluation `json:"evaluation"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	if out.Status != "blocked" {
		t.Fatalf("expected blocked status, got %q", out.Status)
	}
	found := false
	for _, blocker := range out.Evaluation.Blockers {
		if blocker.Criterion == "authority_preflight" && blocker.Kind == "adr_supersession_non_reciprocal" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected authority_preflight supersession blocker, got %#v", out.Evaluation.Blockers)
	}
}

func TestProductTDDCoverageGatePreflightsAuthorityWhenInputsFail(t *testing.T) {
	dir := writeValidTestEffort(t)
	writeTestFile(t, dir, "prd.md", strings.Replace(validTestPRDMarkdown(), "status: draft", "status: accepted", 1))

	var stdout, stderr bytes.Buffer
	code := runProductTDD([]string{
		"--effort-root", dir,
		"--evaluate-coverage",
		"--json",
	}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("expected code 1, got %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var out struct {
		Status     string                                 `json:"status"`
		Evaluation productworkflow.CoverageGateEvaluation `json:"evaluation"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	found := false
	for _, blocker := range out.Evaluation.Blockers {
		if blocker.Criterion == "authority_preflight" && blocker.Kind == "parse_error" && blocker.File == "prd.md" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected PRD parse error to be carried as authority_preflight blocker, got %#v", out.Evaluation.Blockers)
	}
}

func TestProductBriefRenderFailsClosedOnMissingInputs(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := runProductBrief([]string{
		"--effort-root", dir,
		"--render",
		"--brief-output", "briefs/blocked.md",
		"--json",
	}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("expected blocked code 1, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var out productBriefOutput
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("expected JSON output: %v\n%s", err, stdout.String())
	}
	if out.Status != "blocked" {
		t.Fatalf("expected blocked status, got %q", out.Status)
	}
	if strings.TrimSpace(out.Blocker) == "" {
		t.Fatal("expected blocker text")
	}
	if _, err := os.Stat(filepath.Join(dir, "briefs", "blocked.md")); !os.IsNotExist(err) {
		t.Fatalf("blocked render must not write requested output path, stat err=%v", err)
	}
}

func TestProductBriefRenderTextFailureIncludesNextAction(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := runProductBrief([]string{
		"--effort-root", dir,
		"--render",
	}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("expected blocked code 1, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	text := stdout.String()
	if !strings.Contains(text, "product brief render: blocked") || !strings.Contains(text, "next:") {
		t.Fatalf("expected actionable text failure output, got stdout=%s stderr=%s", text, stderr.String())
	}
}

func TestProductBriefRenderRejectsUnsafeOutputPath(t *testing.T) {
	dir := writeValidTestEffort(t)
	acceptedPRD := strings.Replace(validTestPRDMarkdown(), "status: draft", "status: accepted", 1)
	acceptedPRD = strings.Replace(acceptedPRD, "created_at: 2026-05-17T00:00:00Z", "created_at: 2026-05-17T00:00:00Z\naccepted_at: 2026-05-18T00:00:00Z\naccepted_by: reviewer\nacceptance_profile: human-review", 1)
	writeTestFile(t, dir, "prd.md", acceptedPRD)
	var stdout, stderr bytes.Buffer
	code := runProductBrief([]string{
		"--effort-root", dir,
		"--render",
		"--brief-output", "../bad.md",
		"--json",
	}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("expected blocked code 1, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var out productBriefOutput
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("expected JSON output: %v\n%s", err, stdout.String())
	}
	if !strings.Contains(out.Blocker, "must be effort-relative") {
		t.Fatalf("expected traversal blocker, got %q", out.Blocker)
	}
	if strings.Contains(out.NextValidAction, "dispatch from this brief output") {
		t.Fatalf("blocked output must not claim dispatch readiness, got next action %q", out.NextValidAction)
	}
}

func TestProductBriefRenderRejectsNonBriefsOutputPath(t *testing.T) {
	dir := writeValidTestEffort(t)
	acceptedPRD := strings.Replace(validTestPRDMarkdown(), "status: draft", "status: accepted", 1)
	acceptedPRD = strings.Replace(acceptedPRD, "created_at: 2026-05-17T00:00:00Z", "created_at: 2026-05-17T00:00:00Z\naccepted_at: 2026-05-18T00:00:00Z\naccepted_by: reviewer\nacceptance_profile: human-review", 1)
	writeTestFile(t, dir, "prd.md", acceptedPRD)
	var stdout, stderr bytes.Buffer
	code := runProductBrief([]string{
		"--effort-root", dir,
		"--render",
		"--brief-output", "prd.md",
		"--json",
	}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("expected blocked code 1, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var out productBriefOutput
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("expected JSON output: %v\n%s", err, stdout.String())
	}
	if !strings.Contains(out.Blocker, "must be under briefs/") {
		t.Fatalf("expected briefs/ blocker, got %q", out.Blocker)
	}
}

func TestProductBriefRenderRejectsSymlinkOutputPath(t *testing.T) {
	dir := writeValidTestEffort(t)
	acceptedPRD := strings.Replace(validTestPRDMarkdown(), "status: draft", "status: accepted", 1)
	acceptedPRD = strings.Replace(acceptedPRD, "created_at: 2026-05-17T00:00:00Z", "created_at: 2026-05-17T00:00:00Z\naccepted_at: 2026-05-18T00:00:00Z\naccepted_by: reviewer\nacceptance_profile: human-review", 1)
	writeTestFile(t, dir, "prd.md", acceptedPRD)
	if err := os.MkdirAll(filepath.Join(dir, "briefs"), 0o755); err != nil {
		t.Fatalf("mkdir briefs: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "target.md"), []byte("target"), 0o644); err != nil {
		t.Fatalf("write symlink target: %v", err)
	}
	if err := os.Symlink(filepath.Join(dir, "target.md"), filepath.Join(dir, "briefs", "linked.md")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := runProductBrief([]string{
		"--effort-root", dir,
		"--render",
		"--brief-output", "briefs/linked.md",
		"--json",
	}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("expected blocked code 1, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var out productBriefOutput
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("expected JSON output: %v\n%s", err, stdout.String())
	}
	if !strings.Contains(out.Blocker, "must not be a symlink") {
		t.Fatalf("expected symlink blocker, got %q", out.Blocker)
	}
}

func TestProductBriefRenderWritesRequestedPathOnly(t *testing.T) {
	dir := writeValidTestEffort(t)
	acceptedPRD := strings.Replace(validTestPRDMarkdown(), "status: draft", "status: accepted", 1)
	acceptedPRD = strings.Replace(acceptedPRD, "created_at: 2026-05-17T00:00:00Z", "created_at: 2026-05-17T00:00:00Z\naccepted_at: 2026-05-18T00:00:00Z\naccepted_by: reviewer\nacceptance_profile: human-review", 1)
	writeTestFile(t, dir, "prd.md", acceptedPRD)
	writeTestFile(t, dir, "workflow_state.json", strings.Replace(validTestWorkflowStateJSON(), `"coverage":{"coverage_tier":"unit"}`, `"coverage":{"coverage_tier":"behavior_standard"}`, 1))
	writeTestFile(t, dir, "tdd.json", `{
		"schema_version":"idiom.tdd_obligations.v2.fixture",
		"effort_id":"test-effort",
		"task_id":"impl-test",
		"coverage_tier":"behavior_standard",
		"coverage":{"ears_total":1,"ears_mapped":1,"bdd_total":1,"bdd_with_concrete_data":1,"mutation_min_score":80,"gates_passed":["contract"]},
		"obligations":[{"id":"TDD-001","type":"acceptance","behavior":"test","owner":"test","command":"go test ./...","required_before":"close","status":"pending"}]
	}`)
	var stdout, stderr bytes.Buffer
	code := runProductBrief([]string{
		"--effort-root", dir,
		"--render",
		"--brief-output", "briefs/generated.md",
		"--json",
	}, &stdout, &stderr, nil)
	var out productBriefOutput
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("expected JSON output: %v\n%s", err, stdout.String())
	}
	if code != 0 {
		t.Fatalf("expected pass exit, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if out.Status != "pass" {
		t.Fatalf("expected pass status, got %#v", out)
	}
	if out.BriefOutputPath != "briefs/generated.md" {
		t.Fatalf("expected relative output path, got %q", out.BriefOutputPath)
	}
	if out.Lint == nil || out.Lint.Exit != 0 {
		t.Fatalf("expected passing brief lint result, got %#v", out.Lint)
	}
	if _, err := os.Stat(filepath.Join(dir, "briefs", "generated.md")); err != nil {
		t.Fatalf("expected brief write at requested path: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "briefs", "other.md")); !os.IsNotExist(err) {
		t.Fatalf("unexpected write to unrequested path, stat err=%v", err)
	}
}

func TestProductBriefRenderScopesReadFirstPathsToEffortRoot(t *testing.T) {
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	effort := filepath.Join(repo, "custom", "efforts", "brief-case")
	writeMinimalPassingBriefEffort(t, effort)

	var stdout, stderr bytes.Buffer
	code := runProductBrief([]string{
		"--effort-root", effort,
		"--render",
		"--json",
	}, &stdout, &stderr, nil)
	if code != 0 {
		t.Fatalf("expected pass exit, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var out productBriefOutput
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("expected JSON output: %v\n%s", err, stdout.String())
	}
	if !containsString(out.Generated.FilesToReadFirst, "custom/efforts/brief-case/prd.md") {
		t.Fatalf("read-first paths must be scoped to requested effort root, got %#v", out.Generated.FilesToReadFirst)
	}
	for _, path := range out.Generated.FilesToReadFirst {
		if strings.Contains(path, "docs/product-workflows/efforts/closed/prd-adr-brief-workflow/prd.md") {
			t.Fatalf("read-first path leaked durable fixture path: %#v", out.Generated.FilesToReadFirst)
		}
	}
}

func TestProductBriefRenderScopesExternalReadFirstPathsToEffortRoot(t *testing.T) {
	effort := filepath.Join(t.TempDir(), "brief-case")
	writeMinimalPassingBriefEffort(t, effort)

	var stdout, stderr bytes.Buffer
	code := runProductBrief([]string{
		"--effort-root", effort,
		"--render",
		"--json",
	}, &stdout, &stderr, nil)
	if code != 0 {
		t.Fatalf("expected pass exit, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var out productBriefOutput
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("expected JSON output: %v\n%s", err, stdout.String())
	}
	want := filepath.ToSlash(filepath.Join(effort, "prd.md"))
	if !containsString(out.Generated.FilesToReadFirst, want) {
		t.Fatalf("external read-first paths must be scoped to effort root %q, got %#v", want, out.Generated.FilesToReadFirst)
	}
}

func TestProductBriefRenderExistingFileDetectionDoesNotDependOnCWD(t *testing.T) {
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	writeTestFile(t, repo, "cmd/idiom/product_workflow.go", "package main\n")
	effort := filepath.Join(repo, "docs", "efforts", "brief-case")
	writeMinimalPassingBriefEffort(t, effort)
	writeTestFile(t, effort, "implementation_ledger.jsonl", strings.Replace(validTestImplementationLedgerJSONL(), `"expected_files":["test.go"]`, `"expected_files":["cmd/idiom/product_workflow.go"]`, 1))

	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	otherWD := t.TempDir()
	if err := os.Chdir(otherWD); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Chdir(oldWD); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	}()

	var stdout, stderr bytes.Buffer
	code := runProductBrief([]string{
		"--effort-root", effort,
		"--render",
		"--json",
	}, &stdout, &stderr, nil)
	if code != 0 {
		t.Fatalf("expected pass exit, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var out productBriefOutput
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("expected JSON output: %v\n%s", err, stdout.String())
	}
	if got := out.Generated.FileActions["cmd/idiom/product_workflow.go"]; !strings.Contains(got, "Modify existing file") {
		t.Fatalf("expected existing file action independent of cwd, got %q", got)
	}
}

func TestProductBriefRenderTextOutputIsActionable(t *testing.T) {
	dir := writeValidTestEffort(t)
	acceptedPRD := strings.Replace(validTestPRDMarkdown(), "status: draft", "status: accepted", 1)
	acceptedPRD = strings.Replace(acceptedPRD, "created_at: 2026-05-17T00:00:00Z", "created_at: 2026-05-17T00:00:00Z\naccepted_at: 2026-05-18T00:00:00Z\naccepted_by: reviewer\nacceptance_profile: human-review", 1)
	writeTestFile(t, dir, "prd.md", acceptedPRD)

	var stdout, stderr bytes.Buffer
	code := runProductBrief([]string{
		"--effort-root", dir,
		"--render",
	}, &stdout, &stderr, nil)
	if code != 0 {
		t.Fatalf("expected pass exit, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	text := stdout.String()
	for _, want := range []string{
		"product brief render: pass",
		"# Brief: Product Workflow Dispatch",
		"## Files To Modify",
		"## Acceptance",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected text output to contain %q, got:\n%s", want, text)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "briefs", "generated.md")); !os.IsNotExist(err) {
		t.Fatalf("render without --brief-output must not write a durable brief, stat err=%v", err)
	}
}

func TestProductBriefRenderPassesCurrentDurableEffortFixture(t *testing.T) {
	root := repoRoot(t)
	effortRoot := filepath.Join(root, "docs", "product-workflows", "efforts", "closed", "prd-adr-brief-workflow")
	var stdout, stderr bytes.Buffer
	code := runProductBrief([]string{
		"--effort-root", effortRoot,
		"--render",
		"--json",
	}, &stdout, &stderr, nil)
	if code != 0 {
		t.Fatalf("expected durable fixture brief render to pass, got code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var out productBriefOutput
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("expected JSON output: %v\n%s", err, stdout.String())
	}
	if out.Status != "pass" || out.Lint == nil || out.Lint.Exit != 0 {
		t.Fatalf("expected pass with lint success, got %#v", out)
	}
	if !strings.Contains(out.Generated.RenderedBrief, "## Files To Modify") {
		t.Fatal("expected rendered brief body in JSON output")
	}
}

func writeMinimalPassingBriefEffort(t *testing.T, dir string) {
	t.Helper()
	writeTestFile(t, dir, "workflow_state.json", validTestWorkflowStateJSON())
	writeTestFile(t, dir, "tdd.json", validTestTDDJSON())
	writeTestFile(t, dir, "evidence/evidence.jsonl", validTestEvidenceJSONL())
	writeTestFile(t, dir, "evidence/traceability_matrix.json", validTestTraceabilityMatrixJSON())
	writeTestFile(t, dir, "implementation_ledger.jsonl", validTestImplementationLedgerJSONL())
	acceptedPRD := strings.Replace(validTestPRDMarkdown(), "status: draft", "status: accepted", 1)
	acceptedPRD = strings.Replace(acceptedPRD, "created_at: 2026-05-17T00:00:00Z", "created_at: 2026-05-17T00:00:00Z\naccepted_at: 2026-05-18T00:00:00Z\naccepted_by: reviewer\nacceptance_profile: human-review", 1)
	writeTestFile(t, dir, "prd.md", acceptedPRD)
	writeTestFile(t, dir, "adrs/index.json", validTestADRIndexJSON())
	writeTestFile(t, dir, "adrs/ADR-001-test.md", validTestADRMarkdown())
	writeTestFile(t, dir, "briefs/test.md", "# Test Brief")
	writeTestFile(t, dir, "reports/test.json", "{}")
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestProductTDDCoverageGateFailsClosedOnMissingTraceability(t *testing.T) {
	dir := writeValidTestEffort(t)
	if err := os.Remove(filepath.Join(dir, "evidence", "traceability_matrix.json")); err != nil {
		t.Fatalf("remove traceability: %v", err)
	}
	var stdout, stderr bytes.Buffer
	code := runProductTDD([]string{
		"--effort-root", dir,
		"--evaluate-coverage",
		"--json",
	}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("expected code 1, got %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var out struct {
		Status     string                                 `json:"status"`
		Evaluation productworkflow.CoverageGateEvaluation `json:"evaluation"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("output should be valid JSON: %v\n%s", err, stdout.String())
	}
	if out.Status != "blocked" {
		t.Fatalf("expected blocked status, got %q", out.Status)
	}
}

func TestProductTDDCoverageGateUsageErrors(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runProductTDD([]string{"--effort-root", ".", "--plan-obligations", "--evaluate-coverage"}, &stdout, &stderr, nil)
	if code != 2 {
		t.Fatalf("expected code 2 for mutually exclusive flags, got %d", code)
	}
}

func TestProductFeedbackPassesWithValidReferences(t *testing.T) {
	dir := writeValidTestEffort(t)
	writeTestFile(t, dir, "references/lane_history.json", `[{"lane":"implement","event":"closed","status":"pass","timestamp":"2026-05-18T00:00:00Z"}]`)
	writeTestFile(t, dir, "references/plan_progress.json", `{"phase":"phase-5","state":"feedback","status":"completed"}`)
	writeTestFile(t, dir, "references/verification.json", `{"status":"passed","commands":["go test ./cmd/idiom"]}`)

	var stdout, stderr bytes.Buffer
	code := runProductFeedback([]string{
		"--effort-root", dir,
		"--lane-history-file", "references/lane_history.json",
		"--plan-progress-file", "references/plan_progress.json",
		"--verification-file", "references/verification.json",
		"--product-evidence-id", "pe-1",
		"--product-evidence-path", "product_evidence/export-1.json",
		"--release-evidence-id", "re-1",
		"--release-evidence-path", "release_evidence/bundle-1.json",
		"--brief-path", "briefs/test.md",
		"--changed-path", "cmd/idiom/product_workflow.go",
		"--tdd-obligation-ref", "TDD-001",
		"--json",
	}, &stdout, &stderr, nil)
	if code != 0 {
		t.Fatalf("expected code 0, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var out productFeedbackOutput
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("expected JSON output: %v\n%s", err, stdout.String())
	}
	if out.Status != "pass" || out.Summary.Status != "pass" {
		t.Fatalf("expected pass output, got %#v", out)
	}
	if len(out.Summary.ChangedPaths) != 1 || out.Summary.ChangedPaths[0] != "cmd/idiom/product_workflow.go" {
		t.Fatalf("expected changed paths in summary, got %#v", out.Summary.ChangedPaths)
	}
	if out.Summary.Verification.Status != "passed" || len(out.Summary.Verification.Commands) != 1 {
		t.Fatalf("expected verification summary details, got %#v", out.Summary.Verification)
	}
	if out.Summary.BriefPath != "briefs/test.md" {
		t.Fatalf("expected brief path in summary, got %q", out.Summary.BriefPath)
	}
	if out.Summary.ProductEvidence.Path != "product_evidence/export-1.json" || out.Summary.ReleaseEvidence.Path != "release_evidence/bundle-1.json" {
		t.Fatalf("expected evidence references in summary, got product=%#v release=%#v", out.Summary.ProductEvidence, out.Summary.ReleaseEvidence)
	}
	if len(out.Summary.TDDObligations) != 1 || out.Summary.TDDObligations[0] != "TDD-001" {
		t.Fatalf("expected TDD refs in summary, got %#v", out.Summary.TDDObligations)
	}
}

func TestProductFeedbackRankerLearningEvidenceJSON(t *testing.T) {
	dir := writeValidTestEffort(t)
	workflowState := strings.Replace(validTestWorkflowStateJSON(), `"current_phase":"phase-1"`, `"current_phase":"phase-5-execution-feedback"`, 1)
	workflowState = strings.Replace(workflowState, `"current_state":"implementing"`, `"current_state":"planned"`, 1)
	workflowState = strings.Replace(workflowState, `"task_id":"impl-test"`, `"task_id":"impl-test","state_revision":1`, 1)
	writeTestFile(t, dir, "workflow_state.json", workflowState)
	writeTestFile(t, dir, "references/feedback_summary.json", validRankerLearningFeedbackSummaryJSON("pass"))

	var stdout, stderr bytes.Buffer
	code := runProductFeedback([]string{
		"--effort-root", dir,
		"--ranker-learning-evidence-summary", "references/feedback_summary.json",
		"--json",
	}, &stdout, &stderr, nil)
	if code != 0 {
		t.Fatalf("expected code 0, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var out productFeedbackRankerLearningOutput
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("expected JSON output: %v\n%s", err, stdout.String())
	}
	if out.Status != "pass" || out.RankerLearningEvidence.Status != "pass" {
		t.Fatalf("expected pass output, got %#v", out)
	}
	if out.RankerLearningEvidence.Request == nil {
		t.Fatalf("expected bounded learning request, got %#v", out)
	}
	if out.RankerLearningEvidence.Summary.ContextPackID != "pack" {
		t.Fatalf("expected context pack id, got %#v", out.RankerLearningEvidence.Summary)
	}
	if out.RankerLearningEvidence.Request.Action != "boost" || out.RankerLearningEvidence.Request.ResultID != "feedback_r1" {
		t.Fatalf("expected deterministic request shape, got %#v", out.RankerLearningEvidence.Request)
	}
	if !strings.Contains(out.NextValidAction, "no ranker promotion was applied") {
		t.Fatalf("expected bounded next action, got %q", out.NextValidAction)
	}
	if _, err := os.Stat(filepath.Join(dir, ".idiom")); !os.IsNotExist(err) {
		t.Fatalf("ranker-learning evidence mode must not write .idiom state, err=%v", err)
	}
}

func TestProductFeedbackRankerLearningEvidenceTextOutput(t *testing.T) {
	dir := writeValidTestEffort(t)
	workflowState := strings.Replace(validTestWorkflowStateJSON(), `"current_phase":"phase-1"`, `"current_phase":"phase-5-execution-feedback"`, 1)
	workflowState = strings.Replace(workflowState, `"current_state":"implementing"`, `"current_state":"planned"`, 1)
	workflowState = strings.Replace(workflowState, `"task_id":"impl-test"`, `"task_id":"impl-test","state_revision":1`, 1)
	writeTestFile(t, dir, "workflow_state.json", workflowState)
	writeTestFile(t, dir, "references/feedback_summary.json", validRankerLearningFeedbackSummaryJSON("pass"))

	var stdout, stderr bytes.Buffer
	code := runProductFeedback([]string{
		"--effort-root", dir,
		"--ranker-learning-evidence-summary", "references/feedback_summary.json",
	}, &stdout, &stderr, nil)
	if code != 0 {
		t.Fatalf("expected code 0, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	text := stdout.String()
	for _, want := range []string{
		"product feedback ranker-learning-evidence: pass",
		"context_pack: pack (current)",
		"request: boost",
		"findings: 1",
		"[ranker_learning_request_prepared]",
		"next: review bounded ranker-learning evidence",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected text output to contain %q, got:\n%s", want, text)
		}
	}
}

func TestProductFeedbackRankerLearningEvidenceFailsClosed(t *testing.T) {
	dir := writeValidTestEffort(t)
	workflowState := strings.Replace(validTestWorkflowStateJSON(), `"current_phase":"phase-1"`, `"current_phase":"phase-5-execution-feedback"`, 1)
	workflowState = strings.Replace(workflowState, `"current_state":"implementing"`, `"current_state":"planned"`, 1)
	workflowState = strings.Replace(workflowState, `"task_id":"impl-test"`, `"task_id":"impl-test","state_revision":1`, 1)
	writeTestFile(t, dir, "workflow_state.json", workflowState)
	writeTestFile(t, dir, "references/feedback_summary.json", validRankerLearningFeedbackSummaryJSON("blocked"))

	var stdout, stderr bytes.Buffer
	code := runProductFeedback([]string{
		"--effort-root", dir,
		"--ranker-learning-evidence-summary", "references/feedback_summary.json",
		"--json",
	}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("expected code 1, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var out productFeedbackRankerLearningOutput
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("expected JSON output: %v\n%s", err, stdout.String())
	}
	if out.Status != "blocked" || out.RankerLearningEvidence.Request != nil {
		t.Fatalf("expected blocked adapter output without request, got %#v", out)
	}
	kinds := map[string]bool{}
	for _, blocker := range out.RankerLearningEvidence.Blockers {
		kinds[blocker.Kind] = true
	}
	for _, want := range []string{"feedback_not_pass", "feedback_contains_blockers", "unknown_tdd_obligation", "unsafe_artifact_reference"} {
		if !kinds[want] {
			t.Fatalf("expected blocker %s, got %#v", want, out.RankerLearningEvidence.Blockers)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, ".idiom")); !os.IsNotExist(err) {
		t.Fatalf("blocked adapter output must not write .idiom state, err=%v", err)
	}
}

func TestProductFeedbackRankerLearningEvidenceRejectsWrappedBlockedStatus(t *testing.T) {
	dir := writeValidTestEffort(t)
	workflowState := strings.Replace(validTestWorkflowStateJSON(), `"current_phase":"phase-1"`, `"current_phase":"phase-5-execution-feedback"`, 1)
	workflowState = strings.Replace(workflowState, `"current_state":"implementing"`, `"current_state":"planned"`, 1)
	workflowState = strings.Replace(workflowState, `"task_id":"impl-test"`, `"task_id":"impl-test","state_revision":1`, 1)
	writeTestFile(t, dir, "workflow_state.json", workflowState)
	writeTestFile(t, dir, "references/feedback_summary.json", `{"status":"blocked","summary":`+validRankerLearningFeedbackSummaryJSON("pass")+`}`)

	var stdout, stderr bytes.Buffer
	code := runProductFeedback([]string{
		"--effort-root", dir,
		"--ranker-learning-evidence-summary", "references/feedback_summary.json",
		"--json",
	}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("expected code 1, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected no JSON request for blocked wrapper, got stdout=%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), `summary status must be pass`) {
		t.Fatalf("expected wrapped status error, got stderr=%s", stderr.String())
	}
	if _, err := os.Stat(filepath.Join(dir, ".idiom")); !os.IsNotExist(err) {
		t.Fatalf("blocked wrapper must not write .idiom state, err=%v", err)
	}
}

func TestProductFeedbackRankerLearningEvidenceRejectsRankerStatePath(t *testing.T) {
	dir := writeValidTestEffort(t)
	workflowState := strings.Replace(validTestWorkflowStateJSON(), `"current_phase":"phase-1"`, `"current_phase":"phase-5-execution-feedback"`, 1)
	workflowState = strings.Replace(workflowState, `"current_state":"implementing"`, `"current_state":"planned"`, 1)
	workflowState = strings.Replace(workflowState, `"task_id":"impl-test"`, `"task_id":"impl-test","state_revision":1`, 1)
	writeTestFile(t, dir, "workflow_state.json", workflowState)
	summary := strings.Replace(validRankerLearningFeedbackSummaryJSON("pass"), `"changed_paths":["cmd/idiom/product_workflow.go"]`, `"changed_paths":[".idiom/ranker/weights.json"]`, 1)
	writeTestFile(t, dir, "references/feedback_summary.json", summary)

	var stdout, stderr bytes.Buffer
	code := runProductFeedback([]string{
		"--effort-root", dir,
		"--ranker-learning-evidence-summary", "references/feedback_summary.json",
		"--json",
	}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("expected code 1, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var out productFeedbackRankerLearningOutput
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("expected JSON output: %v\n%s", err, stdout.String())
	}
	if out.Status != "blocked" || out.RankerLearningEvidence.Request != nil {
		t.Fatalf("expected blocked output without request, got %#v", out)
	}
	for _, blocker := range out.RankerLearningEvidence.Blockers {
		if blocker.Kind == "unsupported_authority_reference" && strings.Contains(blocker.Message, ".idiom/ranker") {
			return
		}
	}
	t.Fatalf("expected ranker state path blocker, got %#v", out.RankerLearningEvidence.Blockers)
}

func validRankerLearningFeedbackSummaryJSON(status string) string {
	if status == "blocked" {
		return `{
  "status":"blocked",
  "effort_id":"test-effort",
  "task_id":"impl-test",
  "state_revision":1,
  "current_phase":"phase-5-execution-feedback",
  "current_state":"planned",
  "lane_history":[{"lane":"implement","event":"closed","status":"pass","timestamp":"2026-05-18T00:00:00Z"}],
  "plan_phase":"phase-5",
  "plan_state":"feedback",
  "plan_status":"completed",
  "product_evidence":{"id":"pe-1","path":"product_evidence/export-1.json"},
  "release_evidence":{"id":"re-1","path":"release_evidence/bundle-1.json"},
  "brief_path":"briefs/test.md",
  "changed_paths":["../bad"],
  "verification":{"status":"fail","commands":[]},
  "tdd_obligations":["TDD-404"],
  "findings":[],
  "blockers":[{"kind":"verification_not_pass","message":"verification failed","next_valid_action":"repair before ranker weights"}],
  "next_valid_action":"repair before deriving ranker weights"
}`
	}
	return `{
  "status":"pass",
  "effort_id":"test-effort",
  "task_id":"impl-test",
  "state_revision":1,
  "current_phase":"phase-5-execution-feedback",
  "current_state":"planned",
  "lane_history":[{"lane":"implement","event":"closed","status":"pass","timestamp":"2026-05-18T00:00:00Z"}],
  "plan_phase":"phase-5",
  "plan_state":"feedback",
  "plan_status":"completed",
  "product_evidence":{"id":"pe-1","path":"product_evidence/export-1.json"},
  "release_evidence":{"id":"re-1","path":"release_evidence/bundle-1.json"},
  "brief_path":"briefs/test.md",
  "changed_paths":["cmd/idiom/product_workflow.go"],
  "verification":{"status":"pass","commands":["go test ./cmd/idiom"]},
  "tdd_obligations":["TDD-001"],
  "findings":[],
  "blockers":[],
  "next_valid_action":"publish execution feedback summary as deterministic closeout evidence"
}`
}

func TestProductFeedbackReconcilePassesAndWritesOutput(t *testing.T) {
	dir := writeValidTestEffort(t)
	workflowState := strings.Replace(validTestWorkflowStateJSON(), `"current_phase":"phase-1"`, `"current_phase":"phase-5-execution-feedback"`, 1)
	workflowState = strings.Replace(workflowState, `"current_state":"implementing"`, `"current_state":"planned"`, 1)
	workflowState = strings.Replace(workflowState, `"task_id":"impl-test"`, `"task_id":"impl-test","state_revision":1`, 1)
	writeTestFile(t, dir, "workflow_state.json", workflowState)
	writeTestFile(t, dir, "references/feedback_summary.json", `{
  "status":"pass",
  "effort_id":"test-effort",
  "task_id":"impl-test",
	  "state_revision":1,
	  "current_phase":"phase-5-execution-feedback",
	  "current_state":"planned",
  "lane_history":[{"lane":"implement","event":"closed","status":"pass","timestamp":"2026-05-18T00:00:00Z"}],
  "plan_phase":"phase-5",
  "plan_state":"feedback",
  "plan_status":"completed",
  "product_evidence":{"id":"pe-1","path":"product_evidence/export-1.json"},
  "release_evidence":{"id":"re-1","path":"release_evidence/bundle-1.json"},
  "brief_path":"briefs/test.md",
  "changed_paths":["cmd/idiom/product_workflow.go"],
  "verification":{"status":"pass","commands":["go test ./cmd/idiom"]},
  "tdd_obligations":["TDD-001"],
  "findings":[],
  "blockers":[],
  "next_valid_action":"publish execution feedback summary as deterministic closeout evidence"
}`)
	var stdout, stderr bytes.Buffer
	code := runProductFeedback([]string{
		"--effort-root", dir,
		"--reconcile-summary", "references/feedback_summary.json",
		"--reconciled-state-output", "reports/workflow_state.reconciled.json",
		"--json",
	}, &stdout, &stderr, nil)
	if code != 0 {
		t.Fatalf("expected code 0, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var out productFeedbackReconcileOutput
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("expected JSON output: %v\n%s", err, stdout.String())
	}
	if out.Status != "pass" || out.Reconciliation.Status != "pass" {
		t.Fatalf("expected pass reconciliation output, got %#v", out)
	}
	if out.Reconciliation.EvidenceLinks.LinkedArtifactPathCount != 4 {
		t.Fatalf("expected linked path count 4, got %#v", out.Reconciliation.EvidenceLinks)
	}
	data, err := os.ReadFile(filepath.Join(dir, "reports", "workflow_state.reconciled.json"))
	if err != nil {
		t.Fatalf("expected reconciled output file: %v", err)
	}
	var state map[string]any
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatalf("expected JSON output state: %v", err)
	}
	artifactPaths, _ := state["artifact_paths"].(map[string]any)
	if artifactPaths["execution_feedback_summary"] != "references/feedback_summary.json" {
		t.Fatalf("expected summary artifact link, got %#v", artifactPaths)
	}
}

func TestProductFeedbackReconcileBlocksMalformedPassSummary(t *testing.T) {
	dir := writeValidTestEffort(t)
	writeTestFile(t, dir, "references/feedback_summary.json", `{
  "status":"pass",
  "effort_id":"test-effort",
  "task_id":"impl-test",
  "state_revision":1,
  "product_evidence":{"id":"pe-1","path":"product_evidence/export-1.json"},
  "release_evidence":{"id":"re-1","path":"release_evidence/bundle-1.json"},
  "brief_path":"briefs/test.md"
}`)
	var stdout, stderr bytes.Buffer
	code := runProductFeedback([]string{
		"--effort-root", dir,
		"--reconcile-summary", "references/feedback_summary.json",
		"--reconciled-state-output", "reports/workflow_state.reconciled.json",
		"--json",
	}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("expected code 1 for malformed pass summary, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var out productFeedbackReconcileOutput
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("expected JSON output: %v\n%s", err, stdout.String())
	}
	if out.Status != "blocked" {
		t.Fatalf("expected blocked output, got %#v", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "reports", "workflow_state.reconciled.json")); !os.IsNotExist(err) {
		t.Fatalf("expected malformed summary not to write output file, got err=%v", err)
	}
}

func TestProductFeedbackReconcileFailsClosed(t *testing.T) {
	dir := writeValidTestEffort(t)
	writeTestFile(t, dir, "references/feedback_summary.json", `{
  "status":"blocked",
  "effort_id":"wrong-effort",
  "task_id":"wrong-task",
  "lane_history":[{"lane":"implement","event":"closed","status":"pass","timestamp":"2026-05-18T00:00:00Z"}],
  "product_evidence":{"id":"pe-1","path":"../escape.json"},
  "release_evidence":{"id":"re-1","path":"release_evidence/bundle-1.json"},
  "brief_path":"briefs/test.md",
  "verification":{"status":"pass","commands":[]},
  "tdd_obligations":["TDD-001"],
  "findings":[],
  "blockers":[{"kind":"x","message":"y","next_valid_action":"z"}],
  "next_valid_action":"dispatch lane now"
}`)
	var stdout, stderr bytes.Buffer
	code := runProductFeedback([]string{
		"--effort-root", dir,
		"--reconcile-summary", "references/feedback_summary.json",
		"--reconciled-state-output", "reports/workflow_state.reconciled.json",
		"--json",
	}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("expected code 1, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var out productFeedbackReconcileOutput
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("expected JSON output: %v\n%s", err, stdout.String())
	}
	if out.Status != "blocked" {
		t.Fatalf("expected blocked, got %#v", out)
	}
	if len(out.Reconciliation.Blockers) == 0 {
		t.Fatalf("expected reconciliation blockers, got %#v", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "reports", "workflow_state.reconciled.json")); !os.IsNotExist(err) {
		t.Fatalf("expected blocked reconciliation not to write output file, got err=%v", err)
	}
}

func TestProductFeedbackReconcileTextOutputIsActionable(t *testing.T) {
	dir := writeValidTestEffort(t)
	workflowState := strings.Replace(validTestWorkflowStateJSON(), `"current_phase":"phase-1"`, `"current_phase":"phase-5-execution-feedback"`, 1)
	workflowState = strings.Replace(workflowState, `"current_state":"implementing"`, `"current_state":"planned"`, 1)
	workflowState = strings.Replace(workflowState, `"task_id":"impl-test"`, `"task_id":"impl-test","state_revision":1`, 1)
	writeTestFile(t, dir, "workflow_state.json", workflowState)
	writeTestFile(t, dir, "references/feedback_summary.json", `{
  "status":"pass",
  "effort_id":"test-effort",
  "task_id":"impl-test",
  "state_revision":1,
  "current_phase":"phase-5-execution-feedback",
  "current_state":"planned",
  "lane_history":[{"lane":"implement","event":"closed","status":"pass","timestamp":"2026-05-18T00:00:00Z"}],
  "plan_phase":"phase-5",
  "plan_state":"feedback",
  "plan_status":"completed",
  "product_evidence":{"id":"pe-1","path":"product_evidence/export-1.json"},
  "release_evidence":{"id":"re-1","path":"release_evidence/bundle-1.json"},
  "brief_path":"briefs/test.md",
  "changed_paths":["cmd/idiom/product_workflow.go"],
  "verification":{"status":"pass","commands":["go test ./cmd/idiom"]},
  "tdd_obligations":["TDD-001"],
  "findings":[],
  "blockers":[],
  "next_valid_action":"publish execution feedback summary as deterministic closeout evidence"
}`)
	var stdout, stderr bytes.Buffer
	code := runProductFeedback([]string{
		"--effort-root", dir,
		"--reconcile-summary", "references/feedback_summary.json",
		"--reconciled-state-output", "reports/workflow_state.reconciled.json",
	}, &stdout, &stderr, nil)
	if code != 0 {
		t.Fatalf("expected code 0, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	text := stdout.String()
	for _, want := range []string{"product feedback reconcile: pass", "evidence links: 4", "next: review reconciled workflow-state output"} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected text output to contain %q, got:\n%s", want, text)
		}
	}
}

func TestProductFeedbackReconcileRejectsWorkflowStateOverwrite(t *testing.T) {
	dir := writeValidTestEffort(t)
	writeTestFile(t, dir, "references/feedback_summary.json", `{"status":"pass"}`)
	var stdout, stderr bytes.Buffer
	code := runProductFeedback([]string{
		"--effort-root", dir,
		"--reconcile-summary", "references/feedback_summary.json",
		"--reconciled-state-output", "workflow_state.json",
	}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("expected code 1 for workflow_state overwrite, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "must not overwrite workflow_state.json") {
		t.Fatalf("expected workflow_state overwrite failure, got %s", stderr.String())
	}
}

func TestProductFeedbackReconcileRejectsOutputSymlinkAncestor(t *testing.T) {
	dir := writeValidTestEffort(t)
	workflowState := strings.Replace(validTestWorkflowStateJSON(), `"current_phase":"phase-1"`, `"current_phase":"phase-5-execution-feedback"`, 1)
	workflowState = strings.Replace(workflowState, `"current_state":"implementing"`, `"current_state":"planned"`, 1)
	workflowState = strings.Replace(workflowState, `"task_id":"impl-test"`, `"task_id":"impl-test","state_revision":1`, 1)
	writeTestFile(t, dir, "workflow_state.json", workflowState)
	writeTestFile(t, dir, "references/feedback_summary.json", `{
  "status":"pass",
  "effort_id":"test-effort",
  "task_id":"impl-test",
  "state_revision":1,
  "current_phase":"phase-5-execution-feedback",
  "current_state":"planned",
  "lane_history":[{"lane":"implement","event":"closed","status":"pass","timestamp":"2026-05-18T00:00:00Z"}],
  "plan_phase":"phase-5",
  "plan_state":"feedback",
  "plan_status":"completed",
  "product_evidence":{"id":"pe-1","path":"product_evidence/export-1.json"},
  "release_evidence":{"id":"re-1","path":"release_evidence/bundle-1.json"},
  "brief_path":"briefs/test.md",
  "changed_paths":["cmd/idiom/product_workflow.go"],
  "verification":{"status":"pass","commands":["go test ./cmd/idiom"]},
  "tdd_obligations":["TDD-001"],
  "findings":[],
  "blockers":[],
  "next_valid_action":"publish execution feedback summary as deterministic closeout evidence"
}`)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, "linked-output")); err != nil {
		if os.IsPermission(err) {
			t.Skipf("symlink unavailable: %v", err)
		}
		t.Fatalf("create symlink: %v", err)
	}
	var stdout, stderr bytes.Buffer
	code := runProductFeedback([]string{
		"--effort-root", dir,
		"--reconcile-summary", "references/feedback_summary.json",
		"--reconciled-state-output", "linked-output/workflow_state.reconciled.json",
	}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("expected code 1 for output symlink ancestor, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "must not be a symlink") {
		t.Fatalf("expected output symlink failure, got %s", stderr.String())
	}
}

func TestProductFeedbackReconcileBlocksSymlinkAncestor(t *testing.T) {
	dir := writeValidTestEffort(t)
	outside := t.TempDir()
	writeTestFile(t, outside, "feedback_summary.json", `{"status":"pass"}`)
	link := filepath.Join(dir, "linked")
	if err := os.Symlink(outside, link); err != nil {
		if os.IsPermission(err) {
			t.Skipf("symlink unavailable: %v", err)
		}
		t.Fatalf("create symlink: %v", err)
	}
	var stdout, stderr bytes.Buffer
	code := runProductFeedback([]string{
		"--effort-root", dir,
		"--reconcile-summary", "linked/feedback_summary.json",
		"--reconciled-state-output", "reports/workflow_state.reconciled.json",
	}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("expected code 1 for symlink ancestor, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "must not be a symlink") {
		t.Fatalf("expected symlink ancestor failure, got %s", stderr.String())
	}
}

func TestProductFeedbackTextOutputIsActionable(t *testing.T) {
	dir := writeValidTestEffort(t)
	writeTestFile(t, dir, "references/lane_history.json", `[{"lane":"implement","event":"closed","status":"completed","timestamp":"2026-05-18T00:00:00Z"}]`)
	writeTestFile(t, dir, "references/plan_progress.json", `{"phase":"phase-5","state":"feedback","status":"completed"}`)
	writeTestFile(t, dir, "references/verification.json", `{"status":"passed","commands":["go test ./cmd/idiom"]}`)

	var stdout, stderr bytes.Buffer
	code := runProductFeedback([]string{
		"--effort-root", dir,
		"--lane-history-file", "references/lane_history.json",
		"--plan-progress-file", "references/plan_progress.json",
		"--verification-file", "references/verification.json",
		"--product-evidence-id", "pe-1",
		"--product-evidence-path", "product_evidence/export-1.json",
		"--release-evidence-id", "re-1",
		"--release-evidence-path", "release_evidence/bundle-1.json",
		"--brief-path", "briefs/test.md",
		"--changed-path", "cmd/idiom/product_workflow.go",
		"--tdd-obligation-ref", "TDD-001",
	}, &stdout, &stderr, nil)
	if code != 0 {
		t.Fatalf("expected code 0, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	text := stdout.String()
	for _, want := range []string{"product feedback: pass", "verification: passed", "next:"} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected text output to contain %q, got:\n%s", want, text)
		}
	}
}

func TestProductFeedbackFailsClosedOnMalformedReferences(t *testing.T) {
	dir := writeValidTestEffort(t)
	writeTestFile(t, dir, "references/lane_history.json", `{"bad":"shape"}`)
	writeTestFile(t, dir, "references/plan_progress.json", `{"phase":"phase-5","state":"feedback","status":"pass"}`)
	writeTestFile(t, dir, "references/verification.json", `{"status":"pass","commands":["go test ./cmd/idiom"]}`)

	var stdout, stderr bytes.Buffer
	code := runProductFeedback([]string{
		"--effort-root", dir,
		"--lane-history-file", "references/lane_history.json",
		"--plan-progress-file", "references/plan_progress.json",
		"--verification-file", "references/verification.json",
		"--product-evidence-id", "pe-1",
		"--product-evidence-path", "product_evidence/export-1.json",
		"--release-evidence-id", "re-1",
		"--release-evidence-path", "release_evidence/bundle-1.json",
		"--brief-path", "briefs/test.md",
		"--changed-path", "cmd/idiom/product_workflow.go",
		"--tdd-obligation-ref", "TDD-001",
		"--json",
	}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("expected code 1 for malformed lane history, got %d", code)
	}
	if !strings.Contains(stderr.String(), "lane history reference invalid") {
		t.Fatalf("expected lane history parse failure in stderr, got %s", stderr.String())
	}
}

func TestProductFeedbackBlocksUnsafeArtifactReferences(t *testing.T) {
	dir := writeValidTestEffort(t)
	writeTestFile(t, dir, "references/lane_history.json", `[{"lane":"implement","event":"closed","status":"completed","timestamp":"2026-05-18T00:00:00Z"}]`)
	writeTestFile(t, dir, "references/plan_progress.json", `{"phase":"phase-5","state":"feedback","status":"completed"}`)
	writeTestFile(t, dir, "references/verification.json", `{"status":"passed","commands":["go test ./cmd/idiom"]}`)

	var stdout, stderr bytes.Buffer
	code := runProductFeedback([]string{
		"--effort-root", dir,
		"--lane-history-file", "references/lane_history.json",
		"--plan-progress-file", "references/plan_progress.json",
		"--verification-file", "references/verification.json",
		"--product-evidence-id", "pe-1",
		"--product-evidence-path", "../product.json",
		"--release-evidence-id", "re-1",
		"--release-evidence-path", "release_evidence/bundle-1.json",
		"--brief-path", "briefs/test.md",
		"--changed-path", "cmd/idiom/product_workflow.go",
		"--tdd-obligation-ref", "TDD-001",
		"--json",
	}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("expected code 1 for unsafe artifact ref, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var out productFeedbackOutput
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("expected JSON output: %v\n%s", err, stdout.String())
	}
	if !strings.Contains(out.NextValidAction, "safe relative evidence artifact path") {
		t.Fatalf("expected actionable unsafe path next action, got %#v", out)
	}
}

func TestProductFeedbackDoesNotOverrideReferencedVerificationStatus(t *testing.T) {
	dir := writeValidTestEffort(t)
	writeTestFile(t, dir, "references/lane_history.json", `[{"lane":"implement","event":"closed","status":"pass","timestamp":"2026-05-18T00:00:00Z"}]`)
	writeTestFile(t, dir, "references/plan_progress.json", `{"phase":"phase-5","state":"feedback","status":"pass"}`)
	writeTestFile(t, dir, "references/verification.json", `{"status":"fail","commands":["go test ./cmd/idiom"]}`)

	var stdout, stderr bytes.Buffer
	code := runProductFeedback([]string{
		"--effort-root", dir,
		"--lane-history-file", "references/lane_history.json",
		"--plan-progress-file", "references/plan_progress.json",
		"--verification-file", "references/verification.json",
		"--verification-status", "pass",
		"--product-evidence-id", "pe-1",
		"--product-evidence-path", "product_evidence/export-1.json",
		"--release-evidence-id", "re-1",
		"--release-evidence-path", "release_evidence/bundle-1.json",
		"--brief-path", "briefs/test.md",
		"--changed-path", "cmd/idiom/product_workflow.go",
		"--tdd-obligation-ref", "TDD-001",
		"--json",
	}, &stdout, &stderr, nil)
	if code != 2 {
		t.Fatalf("expected unknown override flag to be rejected, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "flag provided but not defined") {
		t.Fatalf("expected unknown flag failure, got %s", stderr.String())
	}
}

func TestProductFeedbackUsageErrors(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runProductFeedback([]string{}, &stdout, &stderr, nil)
	if code != 2 {
		t.Fatalf("expected code 2 for usage error, got %d", code)
	}
}

func TestProductFeedbackBlocksTraversalReferencePath(t *testing.T) {
	dir := writeValidTestEffort(t)
	var stdout, stderr bytes.Buffer
	code := runProductFeedback([]string{
		"--effort-root", dir,
		"--lane-history-file", "../lane_history.json",
		"--plan-progress-file", "references/plan_progress.json",
		"--verification-file", "references/verification.json",
		"--product-evidence-id", "pe-1",
		"--product-evidence-path", "product_evidence/export-1.json",
		"--release-evidence-id", "re-1",
		"--release-evidence-path", "release_evidence/bundle-1.json",
		"--brief-path", "briefs/test.md",
		"--changed-path", "cmd/idiom/product_workflow.go",
		"--tdd-obligation-ref", "TDD-001",
	}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("expected code 1 for traversal path, got %d", code)
	}
	if !strings.Contains(stderr.String(), "must be effort-relative") {
		t.Fatalf("expected effort-relative failure, got %s", stderr.String())
	}
}

func TestProductFeedbackBlocksAbsoluteReferencePath(t *testing.T) {
	dir := writeValidTestEffort(t)
	absLane := filepath.Join(dir, "references", "lane_history.json")
	var stdout, stderr bytes.Buffer
	code := runProductFeedback([]string{
		"--effort-root", dir,
		"--lane-history-file", absLane,
		"--plan-progress-file", "references/plan_progress.json",
		"--verification-file", "references/verification.json",
		"--product-evidence-id", "pe-1",
		"--product-evidence-path", "product_evidence/export-1.json",
		"--release-evidence-id", "re-1",
		"--release-evidence-path", "release_evidence/bundle-1.json",
		"--brief-path", "briefs/test.md",
		"--changed-path", "cmd/idiom/product_workflow.go",
		"--tdd-obligation-ref", "TDD-001",
	}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("expected code 1 for absolute path, got %d", code)
	}
	if !strings.Contains(stderr.String(), "must be effort-relative") {
		t.Fatalf("expected effort-relative failure, got %s", stderr.String())
	}
}

func TestProductFeedbackBlocksSymlinkReferencePath(t *testing.T) {
	dir := writeValidTestEffort(t)
	outside := filepath.Join(t.TempDir(), "outside.json")
	writeTestFile(t, filepath.Dir(outside), filepath.Base(outside), `[]`)
	if err := os.MkdirAll(filepath.Join(dir, "references"), 0o755); err != nil {
		t.Fatalf("mkdir references: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "references", "lane_history.json")); err != nil {
		if os.IsPermission(err) {
			t.Skipf("symlink unavailable: %v", err)
		}
		t.Fatalf("create symlink: %v", err)
	}
	var stdout, stderr bytes.Buffer
	code := runProductFeedback([]string{
		"--effort-root", dir,
		"--lane-history-file", "references/lane_history.json",
		"--plan-progress-file", "references/plan_progress.json",
		"--verification-file", "references/verification.json",
		"--product-evidence-id", "pe-1",
		"--product-evidence-path", "product_evidence/export-1.json",
		"--release-evidence-id", "re-1",
		"--release-evidence-path", "release_evidence/bundle-1.json",
		"--brief-path", "briefs/test.md",
		"--changed-path", "cmd/idiom/product_workflow.go",
		"--tdd-obligation-ref", "TDD-001",
	}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("expected code 1 for symlink path, got %d", code)
	}
	if !strings.Contains(stderr.String(), "must not be a symlink") {
		t.Fatalf("expected symlink failure, got %s", stderr.String())
	}
}

func TestProductFeedbackBlocksPrivacyMarkersInReferences(t *testing.T) {
	dir := writeValidTestEffort(t)
	writeTestFile(t, dir, "references/lane_history.json", `[{"lane":"implement","event":"closed","status":"pass","timestamp":"2026-05-18T00:00:00Z","note":"raw transcript attached"}]`)
	var stdout, stderr bytes.Buffer
	code := runProductFeedback([]string{
		"--effort-root", dir,
		"--lane-history-file", "references/lane_history.json",
		"--plan-progress-file", "references/plan_progress.json",
		"--verification-file", "references/verification.json",
		"--product-evidence-id", "pe-1",
		"--product-evidence-path", "product_evidence/export-1.json",
		"--release-evidence-id", "re-1",
		"--release-evidence-path", "release_evidence/bundle-1.json",
		"--brief-path", "briefs/test.md",
		"--changed-path", "cmd/idiom/product_workflow.go",
		"--tdd-obligation-ref", "TDD-001",
	}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("expected code 1 for privacy marker, got %d", code)
	}
	if !strings.Contains(stderr.String(), "forbidden privacy marker") {
		t.Fatalf("expected privacy marker failure, got %s", stderr.String())
	}
}

func TestProductFeedbackBlocksSecretOnlyMarkerInReferences(t *testing.T) {
	dir := writeValidTestEffort(t)
	writeTestFile(t, dir, "references/lane_history.json", `[{"lane":"implement","event":"closed","status":"pass","timestamp":"2026-05-18T00:00:00Z","note":"secret"}]`)
	var stdout, stderr bytes.Buffer
	code := runProductFeedback([]string{
		"--effort-root", dir,
		"--lane-history-file", "references/lane_history.json",
		"--plan-progress-file", "references/plan_progress.json",
		"--verification-file", "references/verification.json",
		"--product-evidence-id", "pe-1",
		"--product-evidence-path", "product_evidence/export-1.json",
		"--release-evidence-id", "re-1",
		"--release-evidence-path", "release_evidence/bundle-1.json",
		"--brief-path", "briefs/test.md",
		"--changed-path", "cmd/idiom/product_workflow.go",
		"--tdd-obligation-ref", "TDD-001",
	}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("expected code 1 for secret marker, got %d", code)
	}
	if !strings.Contains(stderr.String(), `forbidden privacy marker "secret"`) {
		t.Fatalf("expected secret marker failure, got %s", stderr.String())
	}
}

func TestProductLintPerAtomCommitCoverageDetectsUnderReporting(t *testing.T) {
	dir := writeValidTestEffort(t)
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
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
	if !found {
		t.Fatalf("expected per-atom commit coverage finding, got %v", result.Findings)
	}
}

func TestProductLintPerAtomCommitCoverageFailsWithoutCommitHash(t *testing.T) {
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
	if result.Status != "fail" {
		t.Fatalf("expected fail for ledger without commit_hash, got %q", result.Status)
	}
	found := false
	for _, f := range result.Findings {
		if f.Code == "commit_hash_missing" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected commit_hash_missing finding, got %v", result.Findings)
	}
}

func TestProductLintPerAtomRealGitUnderReporting(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	runGit := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %s: %v", args[0], strings.TrimSpace(string(out)), err)
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
	if err != nil {
		t.Fatalf("git rev-parse: %v", err)
	}
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
	if len(underReportMsgs) < 2 {
		t.Fatalf("expected at least 2 atom_under_reports_path findings (bar.go and baz.go), got %d: %v", len(underReportMsgs), underReportMsgs)
	}

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
	if !foundBar || !foundBaz {
		t.Fatalf("expected findings for bar.go and baz.go, got %v", underReportMsgs)
	}
}

func TestProductLintPerAtomRealGitMatchingPaths(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	runGit := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %s: %v", args[0], strings.TrimSpace(string(out)), err)
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
	if err != nil {
		t.Fatalf("git rev-parse: %v", err)
	}
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
		if f.Code == "atom_under_reports_path" {
			t.Fatalf("expected no atom_under_reports_path when paths match, got: %s: %s", f.Path, f.Message)
		}
	}
}

func TestProductResearchDefaultProjectRootSucceeds(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runProductMainWithTelemetry([]string{"research"}, &stdout, &stderr, nil)
	if code != 0 {
		t.Fatalf("expected code 0 (defaults to current dir), got %d", code)
	}
}
