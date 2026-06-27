package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"idiom/internal/platform/config"
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

func TestWorkspaceWriteRejectsSymlinkedParent(t *testing.T) {
	mgr, dir := patchWorkspaceConfig(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, "linked")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	result := mgr.Execute(context.Background(), ToolRequest{
		Tool:    ToolNameWriteFile,
		Path:    "linked/escape.txt",
		Content: "outside\n",
	})
	if result.Error == "" || !strings.Contains(result.Error, "symlinked directories") {
		t.Fatalf("expected symlinked parent rejection, got %#v", result)
	}
	if _, err := os.Stat(filepath.Join(outside, "escape.txt")); !os.IsNotExist(err) {
		t.Fatalf("write_file should not create outside target, stat err=%v", err)
	}
}

func TestWorkspaceWriteRejectsSymlinkTarget(t *testing.T) {
	mgr, dir := patchWorkspaceConfig(t)
	outside := filepath.Join(t.TempDir(), "target.txt")
	if err := os.WriteFile(outside, []byte("original\n"), 0o644); err != nil {
		t.Fatalf("seed outside target: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "target.txt")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	result := mgr.Execute(context.Background(), ToolRequest{
		Tool:    ToolNameWriteFile,
		Path:    "target.txt",
		Content: "mutated\n",
	})
	if result.Error == "" || !strings.Contains(result.Error, "must not be a symlink") {
		t.Fatalf("expected symlink target rejection, got %#v", result)
	}
	if data, err := os.ReadFile(outside); err != nil || string(data) != "original\n" {
		t.Fatalf("outside target mutated: data=%q err=%v", data, err)
	}
}

func TestApplyPatchRejectsSymlinkedParentBeforeMutation(t *testing.T) {
	mgr, dir := patchWorkspaceConfig(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, "linked")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	patch := "*** Begin Patch\n" +
		"*** Add File: linked/escape.txt\n" +
		"+outside\n" +
		"*** End Patch"
	result := mgr.Execute(context.Background(), ToolRequest{Tool: ToolNameApplyPatch, Content: patch})
	if result.Error == "" || !strings.Contains(result.Error, "symlinked directories") {
		t.Fatalf("expected symlinked parent rejection, got %#v", result)
	}
	if _, err := os.Stat(filepath.Join(outside, "escape.txt")); !os.IsNotExist(err) {
		t.Fatalf("apply_patch should not create outside target, stat err=%v", err)
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
