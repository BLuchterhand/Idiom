package productworkflow

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProductEffortsBrowserListsSearchesAndCountsByStatus(t *testing.T) {
	root := t.TempDir()
	writeEffortsTestState(t, root, "started", "beta", effortsTestWorkflowState("beta-effort", "Beta Work", "started", "implementing", "beta"))
	writeEffortsTestState(t, root, "unstarted", "alpha", effortsTestWorkflowState("alpha-effort", "Alpha Work", "unstarted", "planned", "alpha"))
	writeEffortsTestState(t, root, "closed", "done", effortsTestWorkflowState("closed-effort", "Closed Work", "closed", "closed", "done"))

	result, err := BrowseProductEfforts(ProductEffortsOptions{Root: root})
	if err != nil {
		t.Fatalf("unexpected browse error: %v", err)
	}
	if result.Status != "pass" {
		t.Fatalf("expected pass, got %#v", result)
	}
	if len(result.Efforts) != 3 {
		t.Fatalf("expected 3 efforts, got %#v", result.Efforts)
	}
	if got := []string{result.Efforts[0].EffortID, result.Efforts[1].EffortID, result.Efforts[2].EffortID}; strings.Join(got, ",") != "alpha-effort,beta-effort,closed-effort" {
		t.Fatalf("expected deterministic bucket/id order, got %#v", got)
	}
	if result.Counts.Unstarted != 1 || result.Counts.Started != 1 || result.Counts.Closed != 1 || result.Counts.EffortCount != 3 {
		t.Fatalf("unexpected counts: %#v", result.Counts)
	}
	started := result.Efforts[1]
	if started.ContextPackID != "pack-beta-effort" || started.FreshnessState != "current" {
		t.Fatalf("expected context summary, got %#v", started)
	}
	if started.BlockerCount != 2 || started.OpenObligationCount != 1 {
		t.Fatalf("expected blocker and obligation counts, got %#v", started)
	}
	if started.RelativeFolderPath != "started/beta" {
		t.Fatalf("expected relative folder path, got %q", started.RelativeFolderPath)
	}

	filtered, err := BrowseProductEfforts(ProductEffortsOptions{Root: root, Status: "started", Query: "beta"})
	if err != nil {
		t.Fatalf("unexpected filtered browse error: %v", err)
	}
	if len(filtered.Efforts) != 1 || filtered.Efforts[0].EffortID != "beta-effort" {
		t.Fatalf("expected query to select beta effort, got %#v", filtered.Efforts)
	}
}

func TestProductEffortsBrowserFindsMissingAndMalformedWorkflowState(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "started", "missing"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeEffortsTestFile(t, root, filepath.Join("started", "bad", "workflow_state.json"), `{not valid json`)
	writeEffortsTestState(t, root, "started", "invalid-state", effortsTestWorkflowState("invalid-state-effort", "Invalid State", "started", "nonsense", "invalid-state"))
	mismatched := strings.Replace(effortsTestWorkflowState("mismatch-effort", "Mismatch Work", "started", "implementing", "mismatch"), `"folder_path":"docs/product-workflows/efforts/started/mismatch"`, `"folder_path":"docs/product-workflows/efforts/started/other-effort"`, 1)
	writeEffortsTestFile(t, root, filepath.Join("started", "mismatch", "workflow_state.json"), mismatched)

	result, err := BrowseProductEfforts(ProductEffortsOptions{Root: root, Status: "started"})
	if err != nil {
		t.Fatalf("malformed efforts should be reported as findings, not usage errors: %v", err)
	}
	if result.Status != "fail" {
		t.Fatalf("expected fail, got %#v", result)
	}
	if len(result.Findings) != 4 {
		t.Fatalf("expected four findings, got %#v", result.Findings)
	}
	codes := []string{result.Findings[0].Code, result.Findings[1].Code, result.Findings[2].Code, result.Findings[3].Code}
	if strings.Join(codes, ",") != "invalid_workflow_state,invalid_workflow_state,invalid_workflow_state,missing_workflow_state" {
		t.Fatalf("expected typed malformed-state findings, got %#v", result.Findings)
	}
	if !containsEffortsTestFinding(result.Findings, "invalid_workflow_state", "started/invalid-state/workflow_state.json") {
		t.Fatalf("expected invalid current_state finding, got %#v", result.Findings)
	}
	if !containsEffortsTestFinding(result.Findings, "invalid_workflow_state", "started/mismatch/workflow_state.json") {
		t.Fatalf("expected folder_path mismatch finding, got %#v", result.Findings)
	}
	if result.Counts.FindingCount != 4 || result.Counts.EffortCount != 0 {
		t.Fatalf("unexpected counts: %#v", result.Counts)
	}
	if !strings.Contains(result.Findings[0].ID, result.Findings[0].Code+":") {
		t.Fatalf("expected typed finding identity, got %#v", result.Findings[0])
	}
}

func TestProductEffortsBrowserRejectsUnsafeRootAndUnknownStatus(t *testing.T) {
	if _, err := BrowseProductEfforts(ProductEffortsOptions{Root: filepath.Join("..", "efforts")}); err == nil {
		t.Fatal("expected unsafe traversal root to fail")
	}

	root := t.TempDir()
	result, err := BrowseProductEfforts(ProductEffortsOptions{Root: root, Status: "later"})
	if err == nil {
		t.Fatal("expected unknown status to fail")
	}
	if result.Status != "fail" || len(result.Findings) != 1 || result.Findings[0].Code != "invalid_status_filter" {
		t.Fatalf("expected typed invalid status finding, got %#v", result)
	}
}

func TestHasPathTraversalTreatsBackslashAsSeparator(t *testing.T) {
	if !hasPathTraversal(`..\efforts`) {
		t.Fatal("expected leading backslash traversal to be rejected")
	}
	if !hasPathTraversal(`started\..\closed`) {
		t.Fatal("expected nested backslash traversal to be rejected")
	}
	if hasPathTraversal(`started\test-effort`) {
		t.Fatal("expected ordinary backslash-separated effort path to be accepted")
	}
}

func TestProductEffortsBrowserJSONRoundTripOnFindings(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "started", "missing"), 0o755); err != nil {
		t.Fatal(err)
	}
	result, err := BrowseProductEfforts(ProductEffortsOptions{Root: root})
	if err != nil {
		t.Fatalf("unexpected browse error: %v", err)
	}
	out, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("result must marshal: %v", err)
	}
	var parsed ProductEffortsResult
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("result must round-trip: %v", err)
	}
	if parsed.Status != "fail" || parsed.Findings[0].Code != "missing_workflow_state" {
		t.Fatalf("unexpected parsed result: %#v", parsed)
	}
}

func containsEffortsTestFinding(findings []ProductEffortFinding, code, path string) bool {
	for _, finding := range findings {
		if finding.Code == code && finding.Path == path {
			return true
		}
	}
	return false
}

func writeEffortsTestState(t *testing.T, root, bucket, name, content string) {
	t.Helper()
	writeEffortsTestFile(t, root, filepath.Join(bucket, name, "workflow_state.json"), content)
}

func writeEffortsTestFile(t *testing.T, root, relPath, content string) {
	t.Helper()
	fullPath := filepath.Join(root, relPath)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(fullPath), err)
	}
	if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", fullPath, err)
	}
}

func effortsTestWorkflowState(effortID, title, bucket, state, folderName string) string {
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
		"blockers":["blocked-a","blocked-b"],
		"privacy":{"redaction_status":"applied"},
		"folder_path":"docs/product-workflows/efforts/` + bucket + `/` + folderName + `",
		"next_valid_actions":["submit_for_verification"],
		"durability_level":"source_controlled"
	}`
}
