package productworkflow

import (
	"os"
	"path/filepath"
	"testing"
)

func TestObsidianParityDetectsStaleStatusWithoutRationale(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ObsidianProductWorkflowPath), "> **Status: NOT STARTED**")
	mustWrite(t, filepath.Join(root, ObsidianRecoveryPath), "# Recovery and Decomposition")
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

func TestObsidianParityAllowsNoChangeRationale(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ObsidianProductWorkflowPath), "> **Status: NOT STARTED**")
	mustWrite(t, filepath.Join(root, ObsidianRecoveryPath), "# Recovery and Decomposition")

	ws := &WorkflowState{
		EffortID:     "prd-adr-brief-workflow",
		TaskID:       "impl-0032-phase-5-obsidian-parity-check",
		CurrentPhase: "phase-5-execution-feedback",
	}
	investigation := InvestigationReport{
		TaskID: ws.TaskID,
		Scope: InvestigationScope{
			ObsidianNoChangeRationale: "No updates required because durable and Obsidian statuses already align for this slice.",
		},
	}
	got := EvaluateObsidianParity(root, ws, nil, []InvestigationReport{investigation})

	if len(got.Findings) != 0 {
		t.Fatalf("expected no findings with no-change rationale, got %#v", got.Findings)
	}
}

func TestObsidianParitySkipsUnrelatedEffortWithoutCheckedFiles(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ObsidianProductWorkflowPath), "> **Status: NOT STARTED**")
	mustWrite(t, filepath.Join(root, ObsidianRecoveryPath), "# Recovery and Decomposition")

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

func TestObsidianParitySkipsWhenVaultIsAbsent(t *testing.T) {
	root := t.TempDir()
	ws := &WorkflowState{
		EffortID:     "prd-adr-brief-workflow",
		TaskID:       "impl-0032-phase-5-obsidian-parity-check",
		CurrentPhase: "phase-5-execution-feedback",
	}

	got := EvaluateObsidianParity(root, ws, nil, nil)

	if len(got.CheckedFiles) != 0 {
		t.Fatalf("expected no checked files without Obsidian vault, got %#v", got.CheckedFiles)
	}

	if len(got.Findings) != 0 {
		t.Fatalf("expected no findings without Obsidian vault, got %#v", got.Findings)
	}
}

func TestObsidianParityDoesNotReadAncestorDocs(t *testing.T) {
	parent := t.TempDir()
	nested := filepath.Join(parent, "nested", "effort")
	mustWrite(t, filepath.Join(parent, ObsidianProductWorkflowPath), "> **Status: PARTIALLY IMPLEMENTED**")
	mustWrite(t, filepath.Join(parent, ObsidianRecoveryPath), "# Recovery and Decomposition")
	if err := os.MkdirAll(filepath.Join(nested, "docs", "obsidian"), 0o755); err != nil {
		t.Fatalf("mkdir nested obsidian dir: %v", err)
	}

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

func TestObsidianParityRejectsSymlinkedDocs(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "product.md")
	mustWrite(t, outside, "> **Status: NOT STARTED**")
	mustWrite(t, filepath.Join(root, ObsidianRecoveryPath), "# Recovery and Decomposition")

	if err := os.MkdirAll(filepath.Dir(filepath.Join(root, ObsidianProductWorkflowPath)), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink(outside, filepath.Join(root, ObsidianProductWorkflowPath)); err != nil {
		t.Fatal(err)
	}

	ws := &WorkflowState{
		EffortID:     "prd-adr-brief-workflow",
		TaskID:       "impl-0032-phase-5-obsidian-parity-check",
		CurrentPhase: "phase-5-execution-feedback",
	}

	got := EvaluateObsidianParity(root, ws, nil, nil)
	if len(got.Findings) != 1 || got.Findings[0].Code != "obsidian_read_error" || got.Findings[0].Path != ObsidianProductWorkflowPath {
		t.Fatalf("expected symlinked Obsidian doc read error, got %#v", got.Findings)
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
