package planartifactstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoreFilePathRejectsInvalidArtifactNames(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"", " ", "../plan.json", "nested/plan.json", "bad\x00name.json"} {
		if _, _, err := StoreFilePath(root, name); err == nil {
			t.Fatalf("StoreFilePath(%q) succeeded, want rejection", name)
		}
	}
}

func TestStoreFilePathBuildsContainedArtifactPath(t *testing.T) {
	root := t.TempDir()
	full, rel, err := StoreFilePath(root, "plan.json")
	if err != nil {
		t.Fatalf("StoreFilePath: %v", err)
	}

	wantRel := filepath.ToSlash(filepath.Join(".idiom", "plans", "plan.json"))
	if rel != wantRel {
		t.Fatalf("rel = %q, want %q", rel, wantRel)
	}
	if !strings.HasPrefix(full, filepath.Clean(root)+string(os.PathSeparator)) {
		t.Fatalf("full path %q is not under root %q", full, root)
	}
}

func TestStoreAssetPathBuildsContainedAssetPath(t *testing.T) {
	root := t.TempDir()
	_, rel, err := StoreAssetPath(root, "evidence.json")
	if err != nil {
		t.Fatalf("StoreAssetPath: %v", err)
	}

	wantRel := filepath.ToSlash(filepath.Join(".idiom", "plans", "assets", "evidence.json"))
	if rel != wantRel {
		t.Fatalf("rel = %q, want %q", rel, wantRel)
	}
}

func TestStoreDirRejectsSymlinkedPlansDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".idiom"), 0o755); err != nil {
		t.Fatalf("mkdir .idiom: %v", err)
	}
	target := t.TempDir()
	if err := os.Symlink(target, filepath.Join(root, ".idiom", "plans")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	if _, _, err := StoreDir(root); err == nil {
		t.Fatal("StoreDir accepted symlinked .idiom/plans")
	}
}
