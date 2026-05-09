package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	got, err := findLatestPlanArtifact(root)
	if err != nil {
		t.Fatalf("findLatestPlanArtifact: %v", err)
	}
	if filepath.Base(got) != filepath.Base(newer) {
		t.Fatalf("expected newer artifact, got %s", got)
	}
}

func TestFindLatestPlanArtifactErrorsWhenNoneExist(t *testing.T) {
	root := t.TempDir()
	if _, err := findLatestPlanArtifact(root); err == nil {
		t.Fatalf("expected error when plans dir missing")
	}
}
