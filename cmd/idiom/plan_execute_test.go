package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"idiom/internal/comprehension"
	"idiom/internal/config"
	"idiom/internal/workspace"
)

func mustTime(value string) time.Time {
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		panic(err)
	}
	return t
}

func TestRunExecutorPhaseEmitsExecutorInheritsPackEvent(t *testing.T) {
	// When the planner stamps a pack_id and the cache row exists, the
	// executor phase emits an executor_inherits_pack JSON event so
	// downstream tooling can attach the planner's ranked context.
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("alpha\n"), 0o644); err != nil {
		t.Fatalf("seed a.txt: %v", err)
	}
	key := comprehension.ViewKey{IndexID: "test-index", Task: "exec inherits", Mode: "plan"}
	cached := comprehension.PackResult{
		PackID:         key.ID(),
		IndexID:        "test-index",
		Root:           root,
		Task:           "exec inherits",
		Mode:           "plan",
		Fresh:          true,
		CandidateFiles: []comprehension.PackFile{{Path: "a.txt", Role: "editable"}},
		CandidateSymbols: []comprehension.PackSymbol{
			{ID: "symbol://a.txt#A", Kind: "function", Path: "a.txt"},
		},
		Tests: []comprehension.PackTest{{Command: "go test ./...", Path: "a.txt"}},
	}
	if err := comprehension.SaveView(root, key, cached); err != nil {
		t.Fatalf("SaveView: %v", err)
	}

	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		PackID:        key.ID(),
		Edits: []workspace.PlanEdit{
			{Path: "a.txt", OldText: "alpha", NewText: "ALPHA"},
		},
	}
	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}
	var stdout bytes.Buffer
	if _, err := runExecutorPhase(cfg, plan, true, &stdout); err != nil {
		t.Fatalf("executor phase failed: %v", err)
	}
	if !strings.Contains(stdout.String(), `idiom.plan_execute.executor_inherits_pack`) {
		t.Fatalf("expected executor_inherits_pack JSON event in stdout, got: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), `"pack_id":"`+key.ID()+`"`) {
		t.Fatalf("expected pack_id %s in event, got: %s", key.ID(), stdout.String())
	}
	if !strings.Contains(stdout.String(), `"candidate_files":1`) {
		t.Fatalf("expected candidate_files:1 in event, got: %s", stdout.String())
	}
}

func TestRunExecutorPhaseEmitsExecutorPackMissOnUnknownPackID(t *testing.T) {
	// pack_id that doesn't match any graph_views row → executor_pack_miss
	// event so downstream consumers know the planner's context wasn't
	// available (rather than silently degrading).
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("alpha\n"), 0o644); err != nil {
		t.Fatalf("seed a.txt: %v", err)
	}
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		PackID:        "deadbeefcafef00d",
		Edits: []workspace.PlanEdit{
			{Path: "a.txt", OldText: "alpha", NewText: "ALPHA"},
		},
	}
	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}
	var stdout bytes.Buffer
	if _, err := runExecutorPhase(cfg, plan, true, &stdout); err != nil {
		t.Fatalf("executor phase failed: %v", err)
	}
	if !strings.Contains(stdout.String(), `idiom.plan_execute.executor_pack_miss`) {
		t.Fatalf("expected executor_pack_miss JSON event when pack_id is unknown, got: %s", stdout.String())
	}
}

func TestRunExecutorPhaseDispatchesEditsInOrder(t *testing.T) {
	// Seed two files, run the executor phase against a plan that
	// mutates each, and verify both writes landed.
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("alpha\n"), 0o644); err != nil {
		t.Fatalf("seed a.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "b.txt"), []byte("beta\n"), 0o644); err != nil {
		t.Fatalf("seed b.txt: %v", err)
	}
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Rationale:     "rename markers",
		Edits: []workspace.PlanEdit{
			{Path: "a.txt", OldText: "alpha", NewText: "ALPHA", Purpose: "uppercase a"},
			{Path: "b.txt", OldText: "beta", NewText: "BETA", Purpose: "uppercase b"},
		},
	}
	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}
	var stdout bytes.Buffer
	report, err := runExecutorPhase(cfg, plan, false, &stdout)
	if err != nil {
		t.Fatalf("executor phase failed: %v", err)
	}
	if report.Total != 2 || report.Succeeded != 2 || report.Failed != 0 {
		t.Fatalf("unexpected report: %#v", report)
	}
	got, err := os.ReadFile(filepath.Join(root, "a.txt"))
	if err != nil {
		t.Fatalf("read a.txt: %v", err)
	}
	if !strings.Contains(string(got), "ALPHA") {
		t.Fatalf("a.txt should contain ALPHA after edit, got %q", string(got))
	}
	got, err = os.ReadFile(filepath.Join(root, "b.txt"))
	if err != nil {
		t.Fatalf("read b.txt: %v", err)
	}
	if !strings.Contains(string(got), "BETA") {
		t.Fatalf("b.txt should contain BETA after edit, got %q", string(got))
	}
}

func TestRunExecutorPhaseAbortsAtPreflightOnMissingFile(t *testing.T) {
	// Pre-execute plan validator catches edit 2's missing file before
	// any edit dispatches. This is by-design atomic-preflight: rather
	// than landing edit 1 then failing edit 2 (the old half-applied
	// state), the whole plan is rejected with a single error pointing
	// at the bad edit. Counts remain at 0 because no dispatch ran.
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("alpha\n"), 0o644); err != nil {
		t.Fatalf("seed a.txt: %v", err)
	}
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{
			{Path: "a.txt", OldText: "alpha", NewText: "ALPHA"},
			{Path: "missing.txt", OldText: "x", NewText: "y"},
		},
	}
	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}
	var stdout bytes.Buffer
	report, err := runExecutorPhase(cfg, plan, false, &stdout)
	if err == nil {
		t.Fatalf("expected preflight abort on missing file, got nil")
	}
	if report.Succeeded != 0 || report.Failed != 0 {
		t.Fatalf("preflight aborts before any dispatch — report should be untouched, got %#v", report)
	}
	if !strings.Contains(err.Error(), "missing.txt") {
		t.Fatalf("error should name the failing path, got %v", err)
	}
	if !strings.Contains(err.Error(), "preflight") {
		t.Fatalf("error should identify the abort layer as preflight, got %v", err)
	}
	// a.txt must NOT have been modified — atomic preflight means
	// nothing landed when one edit was bad.
	got, _ := os.ReadFile(filepath.Join(root, "a.txt"))
	if string(got) != "alpha\n" {
		t.Fatalf("preflight abort must leave files untouched, got %q", string(got))
	}
}

func TestPreflightPlanEditsCatchesAnchorMismatch(t *testing.T) {
	// The donkey-atom failure mode: plan's old_text spans content
	// that doesn't appear in the file in that exact form. Preflight
	// must catch it and name the edit's index + path.
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "x.txt"), []byte("line one\nline two\nline three\n"), 0o644); err != nil {
		t.Fatalf("seed x.txt: %v", err)
	}
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{
			{Path: "x.txt", OldText: "line one\nline three", NewText: "X"},
		},
	}
	got := preflightPlanEdits(root, plan)
	if got == nil {
		t.Fatalf("preflight must catch anchor mismatch (combined non-adjacent lines)")
	}
	if got.Index != 0 {
		t.Fatalf("mismatch should point at edit 0, got index %d", got.Index)
	}
	if got.Path != "x.txt" {
		t.Fatalf("mismatch should name the path, got %q", got.Path)
	}
	if !strings.Contains(strings.ToLower(got.Reason), "old_text") {
		t.Fatalf("mismatch reason should mention old_text, got %q", got.Reason)
	}
}

func TestPreflightPlanEditsAcceptsValidPlan(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "x.txt"), []byte("hello\nworld\n"), 0o644); err != nil {
		t.Fatalf("seed x.txt: %v", err)
	}
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{
			{Path: "x.txt", OldText: "hello", NewText: "HELLO"},
		},
	}
	if got := preflightPlanEdits(root, plan); got != nil {
		t.Fatalf("preflight must accept a clean plan, got mismatch %#v", got)
	}
}

func TestPreflightPlanEditsSkipsRangeShapedEdits(t *testing.T) {
	// Range-shaped edits (start_line+end_line+content, no old_text)
	// don't depend on substring matching; preflight must skip them
	// and let the executor handle their validation.
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "x.txt"), []byte("a\nb\nc\n"), 0o644); err != nil {
		t.Fatalf("seed x.txt: %v", err)
	}
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{
			{Path: "x.txt", StartLine: 1, EndLine: 1, Content: "A\n"},
		},
	}
	if got := preflightPlanEdits(root, plan); got != nil {
		t.Fatalf("preflight must skip range-shaped edits, got mismatch %#v", got)
	}
}

func TestRunExecutorPhaseRejectsEmptyPlan(t *testing.T) {
	cfg := config.Config{Workspace: config.Workspace{Root: t.TempDir()}}
	var stdout bytes.Buffer
	_, err := runExecutorPhase(cfg, workspace.PlanArtifact{SchemaVersion: workspace.PlanArtifactSchemaVersion}, false, &stdout)
	if err == nil {
		t.Fatalf("empty plan should be rejected")
	}
}

func TestFindLatestPlanArtifactReturnsMostRecent(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".idiom", "plans")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Write two stamped plans + the rolling pointer.
	old := filepath.Join(dir, "20260101-000000000.plan.json")
	newer := filepath.Join(dir, "20260101-000001000.plan.json")
	pointer := filepath.Join(dir, "last.plan.json")
	for _, p := range []string{old, newer, pointer} {
		if err := os.WriteFile(p, []byte("{}"), 0o644); err != nil {
			t.Fatalf("seed %s: %v", p, err)
		}
	}
	// Force the newer one to have a later mtime regardless of write
	// order.
	if err := os.Chtimes(newer, mustTime("2026-04-01T00:00:00Z"), mustTime("2026-04-01T00:00:00Z")); err != nil {
		t.Fatalf("chtimes newer: %v", err)
	}
	if err := os.Chtimes(old, mustTime("2026-01-01T00:00:00Z"), mustTime("2026-01-01T00:00:00Z")); err != nil {
		t.Fatalf("chtimes old: %v", err)
	}
	got, err := findLatestPlanArtifact(root, time.Time{}, "")
	if err != nil {
		t.Fatalf("findLatestPlanArtifact: %v", err)
	}
	if filepath.Base(got) != filepath.Base(newer) {
		t.Fatalf("expected newer artifact, got %s", got)
	}
}

func TestFindLatestPlanArtifactErrorsWhenNoneExist(t *testing.T) {
	root := t.TempDir()
	if _, err := findLatestPlanArtifact(root, time.Time{}, ""); err == nil {
		t.Fatalf("expected error when plans dir missing")
	}
}

func TestFindLatestPlanArtifactFiltersBySessionID(t *testing.T) {
	// The headline concurrency-isolation invariant: when a session ID
	// is supplied, plans from other sessions (even newer ones) must be
	// invisible. Without this filter, two concurrent plan-execute runs
	// in the same workspace can load each other's plans and dispatch
	// the wrong edits.
	root := t.TempDir()
	dir := filepath.Join(root, ".idiom", "plans")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	mine := filepath.Join(dir, "20260510-180000000.plan.json")
	otherNewer := filepath.Join(dir, "20260510-180100000.plan.json")
	if err := os.WriteFile(mine, []byte(`{"schema_version":"idiom.plan.v1","session_id":"sid-mine","edits":[{"path":"a","old_text":"x","new_text":"y"}]}`), 0o644); err != nil {
		t.Fatalf("seed mine: %v", err)
	}
	if err := os.WriteFile(otherNewer, []byte(`{"schema_version":"idiom.plan.v1","session_id":"sid-other","edits":[{"path":"a","old_text":"x","new_text":"y"}]}`), 0o644); err != nil {
		t.Fatalf("seed other: %v", err)
	}
	if err := os.Chtimes(mine, mustTime("2026-05-10T18:00:00Z"), mustTime("2026-05-10T18:00:00Z")); err != nil {
		t.Fatalf("chtimes mine: %v", err)
	}
	if err := os.Chtimes(otherNewer, mustTime("2026-05-10T18:01:00Z"), mustTime("2026-05-10T18:01:00Z")); err != nil {
		t.Fatalf("chtimes other: %v", err)
	}
	// With sessionID filter: mine is selected even though other is newer.
	got, err := findLatestPlanArtifact(root, time.Time{}, "sid-mine")
	if err != nil {
		t.Fatalf("findLatestPlanArtifact: %v", err)
	}
	if filepath.Base(got) != filepath.Base(mine) {
		t.Fatalf("expected my session's plan even though other is newer, got %s", got)
	}
	// Without sessionID (legacy callers): newest wins regardless of attribution.
	got, err = findLatestPlanArtifact(root, time.Time{}, "")
	if err != nil {
		t.Fatalf("findLatestPlanArtifact (no session): %v", err)
	}
	if filepath.Base(got) != filepath.Base(otherNewer) {
		t.Fatalf("legacy lookup should return newest, got %s", got)
	}
	// Filter for an unknown session: error, even though other plans exist.
	if _, err := findLatestPlanArtifact(root, time.Time{}, "sid-nobody"); err == nil {
		t.Fatalf("expected error when no plan matches the requested session")
	}
}

func TestFindLatestPlanArtifactSkipsArtifactsBeforeNotBefore(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".idiom", "plans")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	stale := filepath.Join(dir, "20260101-000000000.plan.json")
	if err := os.WriteFile(stale, []byte("{}"), 0o644); err != nil {
		t.Fatalf("seed stale: %v", err)
	}
	if err := os.Chtimes(stale, mustTime("2026-01-01T00:00:00Z"), mustTime("2026-01-01T00:00:00Z")); err != nil {
		t.Fatalf("chtimes stale: %v", err)
	}
	notBefore := mustTime("2026-04-01T00:00:00Z")
	if _, err := findLatestPlanArtifact(root, notBefore, ""); err == nil {
		t.Fatalf("expected error when only stale artifacts exist before notBefore")
	}
}

func TestCleanupStalePlanArtifactsRemovesOldArtifacts(t *testing.T) {
	// cleanupStalePlanArtifacts must remove *.plan.json files older than
	// the threshold while preserving recent artifacts and last.plan.json.
	root := t.TempDir()
	dir := filepath.Join(root, ".idiom", "plans")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Seed a stale plan artifact (older than 7 days).
	stale := filepath.Join(dir, "20260101-000000000.plan.json")
	staleContent := `{"schema_version":"idiom.plan.v1","pack_id":"abc123","edits":[]}`
	if err := os.WriteFile(stale, []byte(staleContent), 0o644); err != nil {
		t.Fatalf("seed stale: %v", err)
	}
	if err := os.Chtimes(stale, mustTime("2026-01-01T00:00:00Z"), mustTime("2026-01-01T00:00:00Z")); err != nil {
		t.Fatalf("chtimes stale: %v", err)
	}

	// Seed a non-stale plan artifact (recent mtime) to verify it is preserved.
	recent := filepath.Join(dir, "20260601-000000000.plan.json")
	recentContent := `{"schema_version":"idiom.plan.v1","pack_id":"def456","edits":[]}`
	if err := os.WriteFile(recent, []byte(recentContent), 0o644); err != nil {
		t.Fatalf("seed recent: %v", err)
	}

	// Call cleanupStalePlanArtifacts directly with 7-day threshold.
	var buf bytes.Buffer
	cleanupStalePlanArtifacts(root, 7*24*time.Hour, &buf)

	// Verify the stale plan artifact was removed.
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale plan should have been removed, but still exists")
	}

	// Verify the non-stale plan artifact was preserved.
	if _, err := os.Stat(recent); os.IsNotExist(err) {
		t.Fatalf("recent plan should have been preserved, but was removed")
	}
}
