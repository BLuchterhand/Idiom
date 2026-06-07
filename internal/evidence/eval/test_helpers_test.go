package eval

import (
	"os"
	"path/filepath"
	"testing"
)

func evalTestRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get cwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("repo root not found from cwd")
		}
		dir = parent
	}
}

func evalTestdataPath(t *testing.T, elems ...string) string {
	t.Helper()
	parts := append([]string{evalTestRepoRoot(t), "testdata"}, elems...)
	return filepath.Join(parts...)
}
