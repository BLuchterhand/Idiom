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

func writeSubmitPlanFixture(t *testing.T, root string, path string, content string) {
	t.Helper()
	fullPath := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatalf("create fixture parent: %v", err)
	}
	if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture %s: %v", path, err)
	}
}

func TestSubmitPlanWritesArtifactWithSchemaVersion(t *testing.T) {
	mgr, root := newSubmitPlanTestManager(t)
	writeSubmitPlanFixture(t, root, "a.txt", "before\n")
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
	if !strings.Contains(result.Error, "replace_all=true") {
		t.Fatalf("rejection should name the whole-file/new-file shape, got %q", result.Error)
	}
}

func TestSubmitPlanRejectsAmbiguousOldTextBeforeWritingArtifact(t *testing.T) {
	mgr, root := newSubmitPlanTestManager(t)
	writeSubmitPlanFixture(t, root, "matrix.cpp", "set_floor(30.0f);\nother();\nset_floor(30.0f);\n")
	result := mgr.Execute(context.Background(), ToolRequest{
		Tool: "submit_plan",
		Edits: []PlanEdit{
			{Path: "matrix.cpp", OldText: "set_floor(30.0f);", NewText: "floor = 30.0f;"},
		},
	})
	if strings.TrimSpace(result.Error) == "" {
		t.Fatalf("ambiguous old_text should be rejected")
	}
	if !strings.Contains(result.Error, "matched 2 locations") {
		t.Fatalf("rejection should report duplicate match count, got %q", result.Error)
	}
	if _, err := os.Stat(filepath.Join(root, ".idiom", "plans", "last.plan.json")); !os.IsNotExist(err) {
		t.Fatalf("submit_plan should reject before writing an artifact; stat err=%v", err)
	}
}

func TestSubmitPlanRejectsMissingOldTextBeforeWritingArtifact(t *testing.T) {
	mgr, root := newSubmitPlanTestManager(t)
	writeSubmitPlanFixture(t, root, "x.txt", "actual\n")
	result := mgr.Execute(context.Background(), ToolRequest{
		Tool: "submit_plan",
		Edits: []PlanEdit{
			{Path: "x.txt", OldText: "expected", NewText: "replacement"},
		},
	})
	if strings.TrimSpace(result.Error) == "" {
		t.Fatalf("missing old_text should be rejected")
	}
	if !strings.Contains(result.Error, "did not appear") {
		t.Fatalf("rejection should report missing old_text, got %q", result.Error)
	}
	if _, err := os.Stat(filepath.Join(root, ".idiom", "plans", "last.plan.json")); !os.IsNotExist(err) {
		t.Fatalf("submit_plan should reject before writing an artifact; stat err=%v", err)
	}
}

func TestSubmitPlanAllowsReplaceAllForRepeatedOldText(t *testing.T) {
	mgr, root := newSubmitPlanTestManager(t)
	writeSubmitPlanFixture(t, root, "matrix.cpp", "set_floor(30.0f);\nset_floor(30.0f);\n")
	result := mgr.Execute(context.Background(), ToolRequest{
		Tool: "submit_plan",
		Edits: []PlanEdit{
			{Path: "matrix.cpp", OldText: "set_floor(30.0f);", NewText: "floor = 30.0f;", ReplaceAll: true},
		},
	})
	if strings.TrimSpace(result.Error) != "" {
		t.Fatalf("replace_all should allow repeated old_text: %s", result.Error)
	}
}

func TestSubmitPlanAcceptsWholeFileNewFileShape(t *testing.T) {
	mgr, root := newSubmitPlanTestManager(t)
	result := mgr.Execute(context.Background(), ToolRequest{
		Tool:      "submit_plan",
		Rationale: "create a new config file",
		Edits: []PlanEdit{
			{
				Path:       "config/new.json",
				NewText:    "{\n  \"ok\": true\n}\n",
				ReplaceAll: true,
				Purpose:    "Create the new config file body.",
			},
		},
	})
	if strings.TrimSpace(result.Error) != "" {
		t.Fatalf("submit_plan rejected whole-file/new-file shape: %s", result.Error)
	}
	plan, err := LoadPlanArtifact(filepath.Join(root, ".idiom", "plans", "last.plan.json"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(plan.Edits) != 1 {
		t.Fatalf("expected one edit, got %d", len(plan.Edits))
	}
	edit := plan.Edits[0]
	if !edit.ReplaceAll || edit.OldText != "" || !strings.Contains(edit.NewText, `"ok": true`) {
		t.Fatalf("whole-file/new-file shape not persisted correctly: %#v", edit)
	}
}

func TestSubmitPlanSpillsLargeEditBodiesToRefs(t *testing.T) {
	mgr, root := newSubmitPlanTestManager(t)
	large := strings.Repeat("line\n", 1200)
	result := mgr.Execute(context.Background(), ToolRequest{
		Tool: "submit_plan",
		Edits: []PlanEdit{
			{Path: "large.txt", NewText: large, ReplaceAll: true, Purpose: "Create the large file body."},
		},
	})
	if strings.TrimSpace(result.Error) != "" {
		t.Fatalf("submit_plan rejected large whole-file body: %s", result.Error)
	}
	if !strings.Contains(result.Output, "content_refs: 1") {
		t.Fatalf("expected submit_plan to report one content ref, got %q", result.Output)
	}
	body, err := os.ReadFile(filepath.Join(root, ".idiom", "plans", "last.plan.json"))
	if err != nil {
		t.Fatalf("read plan: %v", err)
	}
	var raw PlanArtifact
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("decode raw plan: %v", err)
	}
	if len(raw.Edits) != 1 || raw.Edits[0].NewText != "" || raw.Edits[0].NewTextRef == "" {
		t.Fatalf("expected raw artifact to store new_text_ref instead of inline body: %#v", raw.Edits)
	}
	plan, err := LoadPlanArtifact(filepath.Join(root, ".idiom", "plans", "last.plan.json"))
	if err != nil {
		t.Fatalf("load plan: %v", err)
	}
	if plan.Edits[0].NewText != large {
		t.Fatalf("hydrated large body mismatch: got %d bytes, want %d", len(plan.Edits[0].NewText), len(large))
	}
}

func TestLoadPlanArtifactRoundTrip(t *testing.T) {
	mgr, root := newSubmitPlanTestManager(t)
	writeSubmitPlanFixture(t, root, "x.txt", "old\n")
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
	writeSubmitPlanFixture(t, root, "a.txt", "old\n")
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
	writeSubmitPlanFixture(t, root, "a.txt", "x\n")
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
	writeSubmitPlanFixture(t, root, "a.txt", "x\n")
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
	writeSubmitPlanFixture(t, root, "a.txt", "x\n")
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
