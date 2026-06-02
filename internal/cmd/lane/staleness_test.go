package lane

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func initGitRepoForStaleness(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "test"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	return root
}

func gitCommit(t *testing.T, root, path, content, message string) {
	t.Helper()
	full := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	for _, args := range [][]string{
		{"add", path},
		{"commit", "-q", "-m", message},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
}

func writeBriefFile(t *testing.T, dir, name, content string, mtime time.Time) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write brief: %v", err)
	}
	if err := os.Chtimes(p, mtime, mtime); err != nil {
		t.Fatalf("chtimes brief: %v", err)
	}
	return p
}

func TestCheckBriefStalenessFailsWhenCommitNewerThanBrief(t *testing.T) {
	root := initGitRepoForStaleness(t)
	gitCommit(t, root, "src/foo.go", "package src\n", "initial")
	briefMtime := time.Now().Add(-2 * time.Hour)
	briefBody := "# Test\n\n## Files To Modify\n\n| File | Change |\n|------|--------|\n| `src/foo.go` | edit |\n"
	briefPath := writeBriefFile(t, root, "brief.md", briefBody, briefMtime)
	gitCommit(t, root, "src/foo.go", "package src\n\nfunc Bar() {}\n", "after-brief")

	var stderr bytes.Buffer
	err := checkBriefStaleness(briefPath, root, false, &stderr)
	if err == nil {
		t.Fatalf("expected stale-brief error, got nil")
	}
	if !strings.Contains(err.Error(), "src/foo.go") {
		t.Fatalf("error should name the stale path, got %v", err)
	}
}

func TestCheckBriefStalenessPassesWhenBriefNewerThanCommit(t *testing.T) {
	root := initGitRepoForStaleness(t)
	gitCommit(t, root, "src/foo.go", "package src\n", "initial")
	briefMtime := time.Now().Add(2 * time.Hour)
	briefBody := "# Test\n\n## Files To Modify\n\n| File | Change |\n|------|--------|\n| `src/foo.go` | edit |\n"
	briefPath := writeBriefFile(t, root, "brief.md", briefBody, briefMtime)

	var stderr bytes.Buffer
	if err := checkBriefStaleness(briefPath, root, false, &stderr); err != nil {
		t.Fatalf("brief newer than commit should pass, got: %v", err)
	}
}

func TestCheckBriefStalenessExemptsNewFiles(t *testing.T) {
	root := initGitRepoForStaleness(t)
	gitCommit(t, root, "other.go", "package main\n", "seed repo")
	briefMtime := time.Now().Add(-1 * time.Hour)
	briefBody := "# Test\n\n## Files To Modify\n\n| File | Change |\n|------|--------|\n| `new/file.go` | NEW FILE |\n"
	briefPath := writeBriefFile(t, root, "brief.md", briefBody, briefMtime)

	var stderr bytes.Buffer
	if err := checkBriefStaleness(briefPath, root, false, &stderr); err != nil {
		t.Fatalf("new file (no commits) should be exempt, got: %v", err)
	}
}

func TestCheckBriefStalenessAllowOverrideDowngradesToWarn(t *testing.T) {
	root := initGitRepoForStaleness(t)
	gitCommit(t, root, "src/foo.go", "package src\n", "initial")
	briefMtime := time.Now().Add(-2 * time.Hour)
	briefBody := "# Test\n\n## Files To Modify\n\n| File | Change |\n|------|--------|\n| `src/foo.go` | edit |\n"
	briefPath := writeBriefFile(t, root, "brief.md", briefBody, briefMtime)
	gitCommit(t, root, "src/foo.go", "package src\n\nfunc Bar() {}\n", "after-brief")

	var stderr bytes.Buffer
	if err := checkBriefStaleness(briefPath, root, true, &stderr); err != nil {
		t.Fatalf("--allow-stale-brief should downgrade to WARN, got error: %v", err)
	}
	if !strings.Contains(stderr.String(), "WARN brief is stale") {
		t.Fatalf("expected stderr WARN message, got: %s", stderr.String())
	}
}
