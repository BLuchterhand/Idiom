package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"idiom/internal/config"
)

func patchWorkspaceConfig(t *testing.T) (*Manager, string) {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Workspace{
		Root:                  dir,
		CWD:                   dir,
		Shell:                 "/bin/sh",
		CommandTimeoutSeconds: 5,
		MaxOutputBytes:        65536,
		MaxFileBytes:          65536,
		MaxDirectoryEntries:   100,
		EnableShell:           true,
		EnableWrites:          true,
	}
	return NewManager(cfg), dir
}

func TestParsePatchAddUpdateDeleteMove(t *testing.T) {
	patch := "*** Begin Patch\n" +
		"*** Add File: new/hello.txt\n" +
		"+hello\n" +
		"+world\n" +
		"*** Update File: existing.go\n" +
		"*** Move to: renamed.go\n" +
		"@@\n" +
		" package main\n" +
		"-old line\n" +
		"+new line\n" +
		"*** Delete File: gone.txt\n" +
		"*** End Patch"

	ops, err := ParsePatch(patch)
	if err != nil {
		t.Fatalf("ParsePatch returned error: %v", err)
	}
	if len(ops) != 3 {
		t.Fatalf("expected 3 ops, got %d", len(ops))
	}
	if ops[0].Kind != PatchOpAdd || ops[0].Path != "new/hello.txt" || ops[0].NewBody != "hello\nworld" {
		t.Fatalf("unexpected add op: %#v", ops[0])
	}
	if ops[1].Kind != PatchOpUpdate || ops[1].Path != "existing.go" || ops[1].MoveTo != "renamed.go" {
		t.Fatalf("unexpected update op: %#v", ops[1])
	}
	if len(ops[1].Hunks) != 1 || len(ops[1].Hunks[0].Lines) != 3 {
		t.Fatalf("unexpected hunks: %#v", ops[1].Hunks)
	}
	if ops[2].Kind != PatchOpDelete || ops[2].Path != "gone.txt" {
		t.Fatalf("unexpected delete op: %#v", ops[2])
	}
}

func TestParsePatchRequiresEnvelope(t *testing.T) {
	if _, err := ParsePatch("*** Add File: x\n+y\n"); err == nil {
		t.Fatal("expected envelope error")
	}
	if _, err := ParsePatch("*** Begin Patch\n+orphan\n*** End Patch"); err == nil {
		t.Fatal("expected unexpected-line error")
	}
	if _, err := ParsePatch("*** Begin Patch\n*** Add File: f\nbad-prefix\n*** End Patch"); err == nil {
		t.Fatal("expected add-line prefix error")
	}
	if _, err := ParsePatch("*** Begin Patch\n*** Update File: f\nbad-line\n*** End Patch"); err == nil {
		t.Fatal("expected update-without-hunk error")
	}
	if _, err := ParsePatch("*** Begin Patch\n*** Move to: x\n*** End Patch"); err == nil {
		t.Fatal("expected move-without-update error")
	}
	if _, err := ParsePatch("*** Begin Patch\n*** End Patch"); err == nil {
		t.Fatal("expected empty-patch error")
	}
}

func TestSerializePatchRoundTripsAddUpdateDelete(t *testing.T) {
	original := "*** Begin Patch\n" +
		"*** Add File: created/note.md\n" +
		"+# Note\n" +
		"+body\n" +
		"*** Update File: existing.txt\n" +
		"@@\n" +
		" alpha\n" +
		"-beta\n" +
		"+BETA\n" +
		" gamma\n" +
		"*** Delete File: doomed.txt\n" +
		"*** End Patch\n"
	ops, err := ParsePatch(original)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	encoded := SerializePatch(ops)
	// The serializer should round-trip through ParsePatch cleanly: same op
	// kinds, paths, and hunk content. We don't assert byte-equality with
	// the original because optional whitespace around `@@` and trailing
	// newlines are not byte-significant.
	round, err := ParsePatch(encoded)
	if err != nil {
		t.Fatalf("re-parse: %v\n--- encoded ---\n%s", err, encoded)
	}
	if len(round) != len(ops) {
		t.Fatalf("op count differs after round-trip: got %d want %d", len(round), len(ops))
	}
	for i := range ops {
		if round[i].Kind != ops[i].Kind {
			t.Fatalf("op %d kind: got %q want %q", i, round[i].Kind, ops[i].Kind)
		}
		if round[i].Path != ops[i].Path {
			t.Fatalf("op %d path: got %q want %q", i, round[i].Path, ops[i].Path)
		}
		if round[i].NewBody != ops[i].NewBody {
			t.Fatalf("op %d body: got %q want %q", i, round[i].NewBody, ops[i].NewBody)
		}
		if len(round[i].Hunks) != len(ops[i].Hunks) {
			t.Fatalf("op %d hunk count: got %d want %d", i, len(round[i].Hunks), len(ops[i].Hunks))
		}
	}
}

func TestParsePatchTolerantOfUnknownTripleStarDirectives(t *testing.T) {
	// Models occasionally invent extra *** directives between/around file
	// sections (e.g. `*** Sanity Check`, `*** Note`, `*** Verification`).
	// The parser should silently skip them rather than hard-failing the
	// whole patch.
	patch := "*** Begin Patch\n" +
		"*** Sanity Check\n" +
		"*** Update File: existing.txt\n" +
		"@@\n" +
		" alpha\n" +
		"-beta\n" +
		"+BETA\n" +
		" gamma\n" +
		"*** Note: this is a model commentary directive\n" +
		"*** Add File: new.txt\n" +
		"+content\n" +
		"*** Verification\n" +
		"*** End Patch"
	ops, err := ParsePatch(patch)
	if err != nil {
		t.Fatalf("expected unknown *** directives to be tolerated, got: %v", err)
	}
	if len(ops) != 2 {
		t.Fatalf("expected 2 ops (update, add), got %d: %#v", len(ops), ops)
	}
	if ops[0].Kind != PatchOpUpdate || ops[0].Path != "existing.txt" {
		t.Fatalf("first op should update existing.txt, got %#v", ops[0])
	}
	if ops[1].Kind != PatchOpAdd || ops[1].Path != "new.txt" {
		t.Fatalf("second op should add new.txt, got %#v", ops[1])
	}
}

func TestParsePatchStillRejectsBareTextOutsideFileSection(t *testing.T) {
	// The unknown-directive tolerance is scoped to lines that start with
	// `*** ` — bare prose between sections should still fail loudly so we
	// don't silently swallow a malformed patch envelope.
	patch := "*** Begin Patch\n" +
		"this is just prose, not a directive\n" +
		"*** Update File: existing.txt\n" +
		"@@\n" +
		" alpha\n" +
		"+beta\n" +
		"*** End Patch"
	if _, err := ParsePatch(patch); err == nil {
		t.Fatal("expected bare prose between sections to fail parsing")
	}
}

func TestApplyPatchAddUpdateDelete(t *testing.T) {
	mgr, dir := patchWorkspaceConfig(t)
	if err := os.WriteFile(filepath.Join(dir, "existing.txt"), []byte("alpha\nbeta\ngamma\n"), 0o644); err != nil {
		t.Fatalf("seed existing.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "doomed.txt"), []byte("bye\n"), 0o644); err != nil {
		t.Fatalf("seed doomed.txt: %v", err)
	}

	patch := "*** Begin Patch\n" +
		"*** Add File: created/note.md\n" +
		"+# Note\n" +
		"+body\n" +
		"*** Update File: existing.txt\n" +
		"@@\n" +
		" alpha\n" +
		"-beta\n" +
		"+BETA\n" +
		" gamma\n" +
		"*** Delete File: doomed.txt\n" +
		"*** End Patch"

	result := mgr.Execute(context.Background(), ToolRequest{Tool: "apply_patch", Content: patch})
	if result.Error != "" {
		t.Fatalf("apply_patch failed: %#v", result)
	}
	if !strings.Contains(result.Summary, "Applied patch") {
		t.Fatalf("unexpected summary: %q", result.Summary)
	}

	if data, err := os.ReadFile(filepath.Join(dir, "created", "note.md")); err != nil || string(data) != "# Note\nbody\n" {
		t.Fatalf("created file mismatch: data=%q err=%v", string(data), err)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "existing.txt")); err != nil || string(data) != "alpha\nBETA\ngamma\n" {
		t.Fatalf("updated file mismatch: data=%q err=%v", string(data), err)
	}
	if _, err := os.Stat(filepath.Join(dir, "doomed.txt")); !os.IsNotExist(err) {
		t.Fatalf("doomed.txt should be removed, stat err=%v", err)
	}
}

func TestApplyPatchRepairsMissingEnvelopeForFileSectionPatch(t *testing.T) {
	mgr, dir := patchWorkspaceConfig(t)
	if err := os.WriteFile(filepath.Join(dir, "existing.txt"), []byte("alpha\nbeta\ngamma\n"), 0o644); err != nil {
		t.Fatalf("seed existing.txt: %v", err)
	}

	patch := "*** Update File: existing.txt\n" +
		"@@\n" +
		" alpha\n" +
		"-beta\n" +
		"+BETA\n" +
		" gamma\n"

	result := mgr.Execute(context.Background(), ToolRequest{Tool: "apply_patch", Content: patch})
	if result.Error != "" {
		t.Fatalf("apply_patch should repair unambiguous missing envelope, got %#v", result)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "existing.txt")); err != nil || string(data) != "alpha\nBETA\ngamma\n" {
		t.Fatalf("updated file mismatch: data=%q err=%v", string(data), err)
	}
}

func TestApplyPatchDoesNotRepairGitDiffWithoutEnvelope(t *testing.T) {
	mgr, dir := patchWorkspaceConfig(t)
	if err := os.WriteFile(filepath.Join(dir, "existing.txt"), []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatalf("seed existing.txt: %v", err)
	}

	patch := "diff --git a/existing.txt b/existing.txt\n" +
		"--- a/existing.txt\n" +
		"+++ b/existing.txt\n" +
		"@@\n" +
		"-beta\n" +
		"+BETA\n"

	result := mgr.Execute(context.Background(), ToolRequest{Tool: "apply_patch", Content: patch})
	if result.Error == "" || !strings.Contains(result.Error, "patch must start") {
		t.Fatalf("expected git diff to remain rejected without envelope, got %#v", result)
	}
}

func TestApplyPatchMoveTo(t *testing.T) {
	mgr, dir := patchWorkspaceConfig(t)
	if err := os.WriteFile(filepath.Join(dir, "src.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("seed src: %v", err)
	}

	patch := "*** Begin Patch\n" +
		"*** Update File: src.txt\n" +
		"*** Move to: dst.txt\n" +
		"@@\n" +
		"-hello\n" +
		"+goodbye\n" +
		"*** End Patch"

	result := mgr.Execute(context.Background(), ToolRequest{Tool: "apply_patch", Content: patch})
	if result.Error != "" {
		t.Fatalf("apply_patch failed: %#v", result)
	}
	if _, err := os.Stat(filepath.Join(dir, "src.txt")); !os.IsNotExist(err) {
		t.Fatalf("src.txt should be removed, stat err=%v", err)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "dst.txt")); err != nil || string(data) != "goodbye\n" {
		t.Fatalf("dst.txt mismatch: data=%q err=%v", string(data), err)
	}
}

func TestApplyPatchRejectsExistingAddTarget(t *testing.T) {
	mgr, dir := patchWorkspaceConfig(t)
	if err := os.WriteFile(filepath.Join(dir, "exists.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	patch := "*** Begin Patch\n" +
		"*** Add File: exists.txt\n" +
		"+x\n" +
		"*** End Patch"
	result := mgr.Execute(context.Background(), ToolRequest{Tool: "apply_patch", Content: patch})
	if result.Error == "" {
		t.Fatal("expected error for Add File on existing target")
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "exists.txt")); string(data) != "a\n" {
		t.Fatalf("file should be untouched: %q", string(data))
	}
}

func TestApplyPatchAmbiguousHunkRejected(t *testing.T) {
	mgr, dir := patchWorkspaceConfig(t)
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x\nx\nx\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	patch := "*** Begin Patch\n" +
		"*** Update File: f.txt\n" +
		"@@\n" +
		"-x\n" +
		"+y\n" +
		"*** End Patch"
	result := mgr.Execute(context.Background(), ToolRequest{Tool: "apply_patch", Content: patch})
	if result.Error == "" {
		t.Fatal("expected ambiguous-hunk error")
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "f.txt")); string(data) != "x\nx\nx\n" {
		t.Fatalf("file should be untouched: %q", string(data))
	}
}

func TestApplyPatchRejectsInvalidGoBeforeWrite(t *testing.T) {
	mgr, dir := patchWorkspaceConfig(t)
	path := filepath.Join(dir, "analysis_agents.go")
	original := "package app\n\nfunc ok() {}\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	patch := "*** Begin Patch\n" +
		"*** Update File: analysis_agents.go\n" +
		"@@\n" +
		"-package app\n" +
		"+\n" +
		" \n" +
		" func ok() {}\n" +
		"*** End Patch"

	result := mgr.Execute(context.Background(), ToolRequest{Tool: "apply_patch", Content: patch})
	if result.Error == "" || !strings.Contains(result.Error, "preflight syntax validation failed") {
		t.Fatalf("expected invalid Go patch rejection, got %#v", result)
	}
	if data, _ := os.ReadFile(path); string(data) != original {
		t.Fatalf("file should be untouched: %q", string(data))
	}
}

func TestApplyPatchExploreModeRejected(t *testing.T) {
	mgr, _ := patchWorkspaceConfig(t)
	if err := mgr.SetMode(ModeExplore); err != nil {
		t.Fatalf("set explore mode: %v", err)
	}
	patch := "*** Begin Patch\n*** Add File: x.txt\n+y\n*** End Patch"
	result := mgr.Execute(context.Background(), ToolRequest{Tool: "apply_patch", Content: patch})
	if result.Error == "" {
		t.Fatal("expected explore-mode rejection")
	}
}

func TestPatchOpPaths(t *testing.T) {
	ops := []PatchOp{
		{Kind: PatchOpUpdate, Path: "a.go", MoveTo: "b.go"},
		{Kind: PatchOpAdd, Path: "c.go"},
		{Kind: PatchOpDelete, Path: "a.go"}, // duplicate, should dedupe
	}
	got := PatchOpPaths(ops)
	want := []string{"a.go", "b.go", "c.go"}
	if len(got) != len(want) {
		t.Fatalf("PatchOpPaths len=%d, want %d (%v)", len(got), len(want), got)
	}
	for i, p := range want {
		if got[i] != p {
			t.Fatalf("PatchOpPaths[%d] = %q, want %q", i, got[i], p)
		}
	}
}
