package lane
import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestCheckBriefStalenessFailsOnUncommittedModification exercises
// the gap that motivated extending the gate to use file mtime in
// addition to git commit time. The path was committed BEFORE the
// brief was written (so commit-only check passes), but then
// modified locally AFTER the brief was written. The on-disk mtime
// is now newer than the brief mtime — gate should fail.
func TestCheckBriefStalenessFailsOnUncommittedModification(t *testing.T) {
	root := initGitRepoForStaleness(t)
	gitCommit(t, root, "src/foo.go", "package src\n", "initial")
	briefMtime := time.Now().Add(-2 * time.Hour)
	briefBody := "# Test\n\n## Files To Modify\n\n| File | Change |\n|------|--------|\n| `src/foo.go` | edit |\n"
	briefPath := writeBriefFile(t, root, "brief.md", briefBody, briefMtime)

	// Modify the file LOCALLY (no commit). mtime is now ~now, well
	// after briefMtime. The git commit time, however, is still from
	// the initial commit (before the brief).
	full := filepath.Join(root, "src/foo.go")
	if err := os.WriteFile(full, []byte("package src\n\nfunc Touched() {}\n"), 0o644); err != nil {
		t.Fatalf("modify file: %v", err)
	}

	var stderr bytes.Buffer
	err := checkBriefStaleness(briefPath, root, false, &stderr)
	if err == nil {
		t.Fatalf("expected stale-brief error from uncommitted mtime change, got nil")
	}
	if !strings.Contains(err.Error(), "src/foo.go") {
		t.Fatalf("error should name the stale path, got %v", err)
	}
}
