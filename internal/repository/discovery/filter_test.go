package discovery

import (
	"path/filepath"
	"slices"
	"testing"
)

func TestShouldSkipDirNameIncludesIdiom(t *testing.T) {
	if !ShouldSkipDirName(".idiom") {
		t.Fatal("expected .idiom to be skipped by default discovery")
	}
}

func TestShouldSkipPathSkipsIdiomAndAllowsRoot(t *testing.T) {
	root := t.TempDir()
	if ShouldSkipPath(root, root, true) {
		t.Fatal("workspace root itself must never be skipped")
	}
	if !ShouldSkipPath(root, filepath.Join(root, ".idiom"), true) {
		t.Fatal("expected .idiom directory to be skipped")
	}
}

func TestRelTargetsNoisyDirRecognizesIdiom(t *testing.T) {
	if !RelTargetsNoisyDir(".idiom/discovery/context.md") {
		t.Fatal("expected explicit target inside .idiom to be recognized as noisy dir")
	}
}

func TestRipgrepExcludeGlobsIncludesIdiom(t *testing.T) {
	if !slices.Contains(RipgrepExcludeGlobs(), "!**/.idiom/**") {
		t.Fatalf("expected ripgrep excludes to contain .idiom, got %#v", RipgrepExcludeGlobs())
	}
}
