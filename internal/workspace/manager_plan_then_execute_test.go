package workspace

import (
	"testing"

	"idiom/internal/config"
)

func TestManagerPlanThenExecutePlannerModeSwapsManifest(t *testing.T) {
	mgr := NewManager(config.Workspace{Root: t.TempDir(), EnableWrites: true})

	// Default state: per-edit write tools present, submit_plan absent.
	defaultTools := mgr.AllowedTools()
	if !planModeManifestContains(defaultTools, "edit_file") {
		t.Fatalf("default manifest must include edit_file, got %v", defaultTools)
	}
	if planModeManifestContains(defaultTools, "submit_plan") {
		t.Fatalf("default manifest must NOT include submit_plan, got %v", defaultTools)
	}

	// Planner mode: submit_plan present, per-edit writes hidden.
	mgr.SetPlanThenExecutePlannerMode(true)
	plannerTools := mgr.AllowedTools()
	if !planModeManifestContains(plannerTools, "submit_plan") {
		t.Fatalf("planner-mode manifest must include submit_plan, got %v", plannerTools)
	}
	for _, write := range []string{"edit_file", "write_file", "append_file", "apply_patch", "delegate_edit", "mkdir", "go_move_declarations"} {
		if planModeManifestContains(plannerTools, write) {
			t.Fatalf("planner-mode manifest must hide %s, got %v", write, plannerTools)
		}
	}
	for _, forbidden := range []string{"task", "task_ledger", "pwd", "source_sanity", "git_status", "git_diff", "shell", "context_trace_pack"} {
		if planModeManifestContains(plannerTools, forbidden) {
			t.Fatalf("planner-mode manifest must not advertise %s because the planner guard rejects it, got %v", forbidden, plannerTools)
		}
	}
	// Read-class tools should still be present so the planner can
	// gather evidence before submitting the plan.
	for _, read := range []string{"read_file", "search_content", "code_navigate", "context_pack", "harness_feedback", "recovery_inspect", "recovery_status", "product_efforts", "product_status", "submit_plan"} {
		if !planModeManifestContains(plannerTools, read) {
			t.Fatalf("planner-mode manifest must keep %s, got %v", read, plannerTools)
		}
	}
	if planModeManifestContains(plannerTools, "recovery_decompose") {
		t.Fatalf("planner-mode manifest must not expose recovery_decompose because it writes recovery packets, got %v", plannerTools)
	}

	// Toggling off restores the default surface.
	mgr.SetPlanThenExecutePlannerMode(false)
	restored := mgr.AllowedTools()
	if !planModeManifestContains(restored, "edit_file") || planModeManifestContains(restored, "submit_plan") {
		t.Fatalf("toggling planner mode off must restore the default manifest, got %v", restored)
	}
}

func planModeManifestContains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
