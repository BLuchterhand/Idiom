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
			"evidence_card":"EV-001","prd_requirement":"REQ-001","adr":"ADR-001","tdd_obligation":"TDD-001","brief":"briefs/test.md","report":"reports/test.json"
		}]
	}`
}

func invalidCloseoutLedgerJSONL() string {
	return strings.Join([]string{
		`{"record_type":"atom_planned","task_id":"impl-test","effort_id":"test-effort","timestamp":"2026-05-17T16:00:00Z","complexity_tier":"C1","context_pack_id":"pack","implementor":{"id":"impl","runtime":"opencode","model":"test","command":"run"},"reviewer":{"id":"rev","model":"test","reasoning_effort":"medium","runtime":"codex"},"scope":{"expected_files":["test.go"],"forbidden_patterns":["cmd/**"]},"required_verification":["go test"],"report_paths":{"implementation":"reports/impl.json","review":"reports/rev.json","investigation":"reports/inv.json"}}`,
		`{"record_type":"atom_closed","task_id":"impl-test","effort_id":"test-effort","timestamp":"2026-05-17T16:05:00Z","open_blockers":["review_pending"]}`,
	}, "\n")
}
