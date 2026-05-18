package productworkflow

import (
	"os"
	"path/filepath"
	"testing"
)

func TestObsidianParityDetectsStaleStatusWithoutRationale(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ObsidianProposalPath), "> **Status: NOT STARTED**")
	mustWrite(t, filepath.Join(root, ObsidianRoadmapPath), "## IN PROGRESS: PRD/ADR/Brief Workflow")
	mustWrite(t, filepath.Join(root, "reports", "impl-0032-investigation.json"), "{}")

	ws := &WorkflowState{
		EffortID:     "prd-adr-brief-workflow",
		TaskID:       "impl-0032-phase-5-obsidian-parity-check",
		CurrentPhase: "phase-5-execution-feedback",
	}
	got := EvaluateObsidianParity(root, ws, nil, nil)
	if len(got.Findings) == 0 {
		t.Fatalf("expected stale parity findings, got none")
	}
}

func TestObsidianParityDoesNotMaskStaleStatusWithNoChangeRationale(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ObsidianProposalPath), "> **Status: NOT STARTED**")
	mustWrite(t, filepath.Join(root, ObsidianRoadmapPath), "## IN PROGRESS: PRD/ADR/Brief Workflow")
	ws := &WorkflowState{
		EffortID:     "prd-adr-brief-workflow",
		TaskID:       "impl-0032-phase-5-obsidian-parity-check",
		CurrentPhase: "phase-5-execution-feedback",
	}
	investigation := InvestigationReport{
		TaskID: ws.TaskID,
		Scope: map[string]any{
			"obsidian_no_change_rationale": "No updates required because durable and Obsidian statuses already align for this slice.",
		},
	}
	got := EvaluateObsidianParity(root, ws, nil, []InvestigationReport{investigation})
	if len(got.Findings) == 0 {
		t.Fatal("expected stale status finding even with no-change rationale")
	}
}

func TestObsidianParitySkipsUnrelatedEffortWithoutCheckedFiles(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ObsidianProposalPath), "> **Status: NOT STARTED**")
	mustWrite(t, filepath.Join(root, ObsidianRoadmapPath), "## IN PROGRESS: PRD/ADR/Brief Workflow")
	ws := &WorkflowState{
		EffortID:     "other-effort",
		TaskID:       "impl-test",
		CurrentPhase: "phase-5-execution-feedback",
	}
	got := EvaluateObsidianParity(root, ws, nil, nil)
	if len(got.CheckedFiles) != 0 {
		t.Fatalf("expected no checked files for skipped effort, got %#v", got.CheckedFiles)
	}
	if len(got.Findings) != 0 {
		t.Fatalf("expected no findings for skipped effort, got %#v", got.Findings)
	}
}

func TestObsidianParityDoesNotReadAncestorDocs(t *testing.T) {
	parent := t.TempDir()
	nested := filepath.Join(parent, "nested", "effort")
	mustWrite(t, filepath.Join(parent, ObsidianProposalPath), "> **Status: PARTIALLY IMPLEMENTED**")
	mustWrite(t, filepath.Join(parent, ObsidianRoadmapPath), "## IN PROGRESS: PRD/ADR/Brief Workflow")
	ws := &WorkflowState{
		EffortID:     "prd-adr-brief-workflow",
		TaskID:       "impl-0032-phase-5-obsidian-parity-check",
		CurrentPhase: "phase-5-execution-feedback",
	}

	got := EvaluateObsidianParity(nested, ws, nil, nil)
	if len(got.Findings) != 2 {
		t.Fatalf("expected missing-file findings scoped to nested root, got %#v", got.Findings)
	}
	for _, finding := range got.Findings {
		if finding.Code != "obsidian_read_error" {
			t.Fatalf("expected obsidian_read_error, got %#v", finding)
		}
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
