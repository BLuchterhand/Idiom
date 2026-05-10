package workspace

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"idiom/internal/config"
)

func newSubmitPlanTestManager(t *testing.T) (*Manager, string) {
	t.Helper()
	root := t.TempDir()
	mgr := NewManager(config.Workspace{
		Root:         root,
		CWD:          root,
		EnableWrites: true,
	})
	return mgr, root
}

func TestSubmitPlanWritesArtifactWithSchemaVersion(t *testing.T) {
	mgr, root := newSubmitPlanTestManager(t)
	result := mgr.Execute(context.Background(), ToolRequest{
		Tool:      "submit_plan",
		Rationale: "add donkey across the codebase",
		Edits: []PlanEdit{
			{Path: "a.txt", OldText: "before", NewText: "after", Purpose: "first edit"},
			{Path: "b.txt", StartLine: 1, EndLine: 1, Content: "rewritten\n", Purpose: "second edit"},
		},
	})
	if strings.TrimSpace(result.Error) != "" {
		t.Fatalf("submit_plan rejected a valid plan: %s", result.Error)
	}
	if !strings.Contains(result.Output, "plan_path:") {
		t.Fatalf("submit_plan output should name the plan path, got %q", result.Output)
	}
	planDir := filepath.Join(root, ".idiom", "plans")
	entries, err := os.ReadDir(planDir)
	if err != nil {
		t.Fatalf("plan dir missing: %v", err)
	}
	var stamped string
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".plan.json") && entry.Name() != "last.plan.json" {
			stamped = filepath.Join(planDir, entry.Name())
		}
	}
	if stamped == "" {
		t.Fatalf("no timestamped plan artifact written under %s", planDir)
	}
	body, err := os.ReadFile(stamped)
	if err != nil {
		t.Fatalf("read artifact: %v", err)
	}
	var artifact PlanArtifact
	if err := json.Unmarshal(body, &artifact); err != nil {
		t.Fatalf("artifact JSON invalid: %v", err)
	}
	if artifact.SchemaVersion != PlanArtifactSchemaVersion {
		t.Fatalf("schema_version mismatch: got %q, want %q", artifact.SchemaVersion, PlanArtifactSchemaVersion)
	}
	if len(artifact.Edits) != 2 {
		t.Fatalf("expected 2 edits, got %d", len(artifact.Edits))
	}
	if artifact.Rationale == "" {
		t.Fatalf("rationale should be persisted")
	}
	pointer, err := os.ReadFile(filepath.Join(planDir, "last.plan.json"))
	if err != nil {
		t.Fatalf("last.plan.json missing: %v", err)
	}
	if !json.Valid(pointer) {
		t.Fatalf("last.plan.json is not valid JSON")
	}
}

func TestSubmitPlanRejectsEmptyEditList(t *testing.T) {
	mgr, _ := newSubmitPlanTestManager(t)
	result := mgr.Execute(context.Background(), ToolRequest{
		Tool:      "submit_plan",
		Rationale: "no work to do",
	})
	if strings.TrimSpace(result.Error) == "" {
		t.Fatalf("empty edits should be rejected")
	}
	if !strings.Contains(result.Error, "at least one edit") {
		t.Fatalf("rejection should call out missing edits, got %q", result.Error)
	}
}

func TestSubmitPlanRejectsEditWithoutShape(t *testing.T) {
	mgr, _ := newSubmitPlanTestManager(t)
	result := mgr.Execute(context.Background(), ToolRequest{
		Tool: "submit_plan",
		Edits: []PlanEdit{
			{Path: "a.txt", Purpose: "missing both shapes"},
		},
	})
	if strings.TrimSpace(result.Error) == "" {
		t.Fatalf("malformed edit shape should be rejected")
	}
	if !strings.Contains(result.Error, "edits[0]") {
		t.Fatalf("rejection should index the bad edit, got %q", result.Error)
	}
}

func TestLoadPlanArtifactRoundTrip(t *testing.T) {
	mgr, root := newSubmitPlanTestManager(t)
	mgr.Execute(context.Background(), ToolRequest{
		Tool:      "submit_plan",
		Rationale: "test plan",
		Edits: []PlanEdit{
			{Path: "x.txt", OldText: "old", NewText: "new"},
		},
	})
	planPath := filepath.Join(root, ".idiom", "plans", "last.plan.json")
	plan, err := LoadPlanArtifact(planPath)
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}
	if len(plan.Edits) != 1 || plan.Edits[0].Path != "x.txt" {
		t.Fatalf("round-tripped plan mismatch: %#v", plan)
	}
}

func TestLoadPlanArtifactRejectsWrongSchemaVersion(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "bogus.plan.json")
	body := []byte(`{"schema_version":"idiom.plan.v0","edits":[{"path":"a.txt","old_text":"a","new_text":"b"}]}`)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	_, err := LoadPlanArtifact(path)
	if err == nil || !strings.Contains(err.Error(), "schema_version") {
		t.Fatalf("expected schema_version mismatch error, got %v", err)
	}
}

func TestSubmitPlanPersistsPackIDForExecutorRehydration(t *testing.T) {
	mgr, root := newSubmitPlanTestManager(t)
	result := mgr.Execute(context.Background(), ToolRequest{
		Tool:      "submit_plan",
		Rationale: "exercise pack_id plumbing",
		PackID:    "feedfacecafebeef",
		Edits: []PlanEdit{
			{Path: "a.txt", OldText: "old", NewText: "new"},
		},
	})
	if strings.TrimSpace(result.Error) != "" {
		t.Fatalf("submit_plan rejected a valid plan: %s", result.Error)
	}
	plan, err := LoadPlanArtifact(filepath.Join(root, ".idiom", "plans", "last.plan.json"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if plan.PackID != "feedfacecafebeef" {
		t.Fatalf("pack_id not persisted: got %q", plan.PackID)
	}
}

func TestSubmitPlanStampsSessionIDFromConfig(t *testing.T) {
	// SessionID stamped on every plan artifact lets findLatestPlanArtifact
	// disambiguate concurrent plan-execute runs in the same workspace.
	t.Helper()
	root := t.TempDir()
	mgr := NewManager(config.Workspace{
		Root:         root,
		CWD:          root,
		EnableWrites: true,
		SessionID:    "session-deadbeef",
	})
	result := mgr.Execute(context.Background(), ToolRequest{
		Tool: "submit_plan",
		Edits: []PlanEdit{
			{Path: "a.txt", OldText: "x", NewText: "y"},
		},
	})
	if strings.TrimSpace(result.Error) != "" {
		t.Fatalf("submit_plan rejected: %s", result.Error)
	}
	plan, err := LoadPlanArtifact(filepath.Join(root, ".idiom", "plans", "last.plan.json"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if plan.SessionID != "session-deadbeef" {
		t.Fatalf("session_id not stamped: got %q want session-deadbeef", plan.SessionID)
	}
}

func TestSubmitPlanOmitsEmptySessionID(t *testing.T) {
	// Backward compatibility: legacy callers that don't set
	// cfg.Workspace.SessionID must still produce valid v1 artifacts
	// with the field omitted.
	mgr, root := newSubmitPlanTestManager(t)
	result := mgr.Execute(context.Background(), ToolRequest{
		Tool: "submit_plan",
		Edits: []PlanEdit{
			{Path: "a.txt", OldText: "x", NewText: "y"},
		},
	})
	if strings.TrimSpace(result.Error) != "" {
		t.Fatalf("submit_plan rejected: %s", result.Error)
	}
	body, err := os.ReadFile(filepath.Join(root, ".idiom", "plans", "last.plan.json"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Contains(string(body), `"session_id"`) {
		t.Fatalf("empty SessionID should be omitted from artifact, got: %s", body)
	}
}

func TestSubmitPlanOmitsEmptyPackID(t *testing.T) {
	// pack_id is optional and additive — older planners that don't
	// provide it must still produce valid v1 artifacts.
	mgr, root := newSubmitPlanTestManager(t)
	result := mgr.Execute(context.Background(), ToolRequest{
		Tool:      "submit_plan",
		Rationale: "no pack id provided",
		Edits: []PlanEdit{
			{Path: "a.txt", OldText: "x", NewText: "y"},
		},
	})
	if strings.TrimSpace(result.Error) != "" {
		t.Fatalf("submit_plan rejected: %s", result.Error)
	}
	body, err := os.ReadFile(filepath.Join(root, ".idiom", "plans", "last.plan.json"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Contains(string(body), `"pack_id"`) {
		t.Fatalf("empty pack_id should be omitted from artifact, got %s", body)
	}
}
