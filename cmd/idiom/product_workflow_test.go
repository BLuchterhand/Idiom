package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProductLintPassesOnCurrentDurableEffortFixture(t *testing.T) {
	root := repoRoot(t)
	effortRoot := filepath.Join(root, "docs", "product-workflows", "efforts", "started", "prd-adr-brief-workflow")

	var stdout, stderr bytes.Buffer
	code := runProductLint([]string{"--effort-root", effortRoot, "--json"}, &stdout, &stderr, nil)
	if code != 0 {
		t.Fatalf("expected lint to pass on current durable effort fixture, got code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var result productLintResult
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
	var result productLintResult
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
	var result productLintResult
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
	effortRoot := filepath.Join(root, "docs", "product-workflows", "efforts", "started", "prd-adr-brief-workflow")

	var stdout, stderr bytes.Buffer
	code := runProductLint([]string{"--effort-root", effortRoot, "--json"}, &stdout, &stderr, nil)
	if code != 0 {
		t.Fatalf("expected lint to pass, got code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var result productLintResult
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
	var result productLintResult
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
	var result productLintResult
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
	var result productLintResult
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
	var result productLintResult
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
	var result productLintResult
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
		`{"record_type":"implementation_completed","task_id":"impl-test","effort_id":"test-effort","timestamp":"2026-05-17T16:01:00Z","implementor_id":"impl"}`,
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
	effortRoot := filepath.Join(root, "docs", "product-workflows", "efforts", "started", "prd-adr-brief-workflow")

	var stdout, stderr bytes.Buffer
	code := runProductLint([]string{"--effort-root", effortRoot, "--json"}, &stdout, &stderr, nil)
	if code != 0 {
		t.Fatalf("expected lint to pass, got code=%d", code)
	}
	var result productLintResult
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
	var result productLintResult
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
	var result productLintResult
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
---

# ADR-001: Test

## Decision

Test decision.
`)
	writeTestFile(t, dir, "reports/test.json", "{}")

	var stdout, stderr bytes.Buffer
	code := runProductLint([]string{"--effort-root", dir, "--json"}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("expected lint to fail with invalid ADR markdown status, got code=%d", code)
	}
	var result productLintResult
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

func validTestImplementationLedgerJSONL() string {
	return strings.Join([]string{
		`{"record_type":"atom_planned","task_id":"impl-test","effort_id":"test-effort","timestamp":"2026-05-17T16:00:00Z","complexity_tier":"C1","context_pack_id":"pack","implementor":{"id":"impl","runtime":"opencode","model":"test","command":"run"},"reviewer":{"id":"rev","model":"test","reasoning_effort":"medium","runtime":"codex"},"scope":{"expected_files":["test.go"],"forbidden_patterns":["cmd/**"]},"required_verification":["go test"],"report_paths":{"implementation":"reports/impl.json","review":"reports/rev.json","investigation":"reports/inv.json"}}`,
		`{"record_type":"implementation_completed","task_id":"impl-test","effort_id":"test-effort","timestamp":"2026-05-17T16:01:00Z","implementor_id":"impl"}`,
		`{"record_type":"verification_completed","task_id":"impl-test","effort_id":"test-effort","timestamp":"2026-05-17T16:02:00Z"}`,
		`{"record_type":"review_completed","task_id":"impl-test","effort_id":"test-effort","timestamp":"2026-05-17T16:03:00Z","reviewer_id":"rev","model":"test","reasoning_effort":"medium"}`,
		`{"record_type":"post_effort_investigation_completed","task_id":"impl-test","effort_id":"test-effort","timestamp":"2026-05-17T16:04:00Z"}`,
		`{"record_type":"atom_closed","task_id":"impl-test","effort_id":"test-effort","timestamp":"2026-05-17T16:05:00Z","open_blockers":[]}`,
	}, "\n")
}

func validTestResearchProfileJSON() string {
	return `{
		"schema_version": "idiom.research_profile.v1",
		"name": "test-profile",
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
			"unsupported_claims": "block_prd_audit",
			"stale_local_context": "refresh_or_mark_gap"
		}
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
- **evidence_refs**: ["EV-001"]
- **tdd_obligations**: ["TDD-001"]
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
prd_refs:
  - REQ-TEST-001
---

# ADR-001: Test

## Decision

Test decision.
`
}
