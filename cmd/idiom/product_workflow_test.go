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
	var result productLintResult
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
	var result productLintResult
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
	var result productLintResult
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
	effortRoot := filepath.Join(root, "docs", "product-workflows", "efforts", "started", "prd-adr-brief-workflow")

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
	requiredSections := []string{"prd", "adr", "tdd", "traceability", "reports", "scope_gate"}
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
	for _, name := range []string{"prd", "adr", "tdd", "traceability", "reports", "scope_gate"} {
		if !sectionNames[name] {
			t.Fatalf("expected section %q in audit result", name)
		}
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

	var lintResult productLintResult
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
	var result productLintResult
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
	var result productLintResult
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
	var result productLintResult
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
	var result productLintResult
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
		`{"record_type":"implementation_completed","task_id":"impl-test","effort_id":"test-effort","timestamp":"2026-05-17T16:01:00Z","implementor_id":"impl"}`,
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
	effortRoot := filepath.Join(root, "docs", "product-workflows", "efforts", "started", "prd-adr-brief-workflow")

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
		"blockers", "open_obligations", "next_valid_actions",
	}
	for _, field := range requiredFields {
		if _, ok := result[field]; !ok {
			t.Fatalf("expected field %q in JSON output", field)
		}
	}
}

func TestProductStatusTextIncludesKeyFields(t *testing.T) {
	root := repoRoot(t)
	effortRoot := filepath.Join(root, "docs", "product-workflows", "efforts", "started", "prd-adr-brief-workflow")

	ws := loadDurableFixtureWorkflowState(t)
	currentState, _ := ws["current_state"].(string)

	var stdout, stderr bytes.Buffer
	code := runProductStatus([]string{"--effort-root", effortRoot}, &stdout, &stderr, nil)
	if code != 0 {
		t.Fatalf("expected status to pass on durable fixture, got code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	output := stdout.String()
	for _, want := range []string{"prd-adr-brief-workflow", currentState, "audit:", "next_actions:"} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected text output to contain %q, got:\n%s", want, output)
		}
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

func TestProductStatusUsageErrors(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runProductStatus([]string{}, &stdout, &stderr, nil)
	if code != 2 {
		t.Fatalf("expected code 2 for missing --effort-root, got %d", code)
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
	wsPath := filepath.Join(root, "docs", "product-workflows", "efforts", "started", "prd-adr-brief-workflow", "workflow_state.json")
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
	writeTestFile(t, dir, "implementation_ledger.jsonl", ledgerJSONL)
	return dir
}

func TestProductLintScopeGateFailsOnForbiddenPath(t *testing.T) {
	dir := writeScopeGateEffort(t, scopeGateLedgerForbiddenExactPathJSONL())
	result := lintEffortFolder(dir)
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
	result := lintEffortFolder(dir)
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
	result := lintEffortFolder(dir)
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
	result := lintEffortFolder(dir)
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
	result := lintEffortFolder(dir)
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
	result := lintEffortFolder(dir)
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
	result := lintEffortFolder(dir)
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
	var result productLintResult
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
	var result productLintResult
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
