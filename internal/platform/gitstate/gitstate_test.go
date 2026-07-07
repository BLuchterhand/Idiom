package gitstate

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCurrentCapturesHeadAndPorcelainStatus(t *testing.T) {
	root := initGitStateRepo(t)
	head := strings.TrimSpace(gitStateGit(t, root, "rev-parse", "HEAD"))

	writeGitStateFile(t, filepath.Join(root, "tracked.txt"), "changed\n")
	writeGitStateFile(t, filepath.Join(root, "untracked.txt"), "new\n")

	snapshot, err := Current(t.Context(), root)
	if err != nil {
		t.Fatalf("Current: %v", err)
	}

	if snapshot.Head != head {
		t.Fatalf("Head = %q, want %q", snapshot.Head, head)
	}
	if !snapshot.Dirty {
		t.Fatalf("Dirty = false; status=%q", snapshot.Status)
	}
	if !containsGitStateLine(snapshot.StatusLines, "M tracked.txt") {
		t.Fatalf("status lines missing tracked modification: %#v", snapshot.StatusLines)
	}
	if !containsGitStateLine(snapshot.StatusLines, "?? untracked.txt") {
		t.Fatalf("status lines missing untracked file: %#v", snapshot.StatusLines)
	}
	if snapshot.Token() != snapshot.Head+"\n"+snapshot.Status {
		t.Fatalf("Token did not preserve head/status shape")
	}
}

func TestCurrentReturnsErrorOutsideGitRepo(t *testing.T) {
	if _, err := Current(t.Context(), t.TempDir()); err == nil {
		t.Fatalf("Current outside git repo returned nil error")
	}
}

func TestCombinedOutputTextPreservesRawOutputAndWrapsGitErrors(t *testing.T) {
	root := initGitStateRepo(t)
	head := gitStateGit(t, root, "rev-parse", "HEAD")

	got, err := CombinedOutputText(t.Context(), root, "rev-parse", "HEAD")
	if err != nil {
		t.Fatalf("CombinedOutputText success: %v", err)
	}
	if got != head {
		t.Fatalf("CombinedOutputText output = %q, want %q", got, head)
	}

	_, err = CombinedOutputText(t.Context(), t.TempDir(), "rev-parse", "HEAD")
	if err == nil {
		t.Fatalf("CombinedOutputText outside git repo returned nil error")
	}

	text := err.Error()
	if !strings.Contains(text, "git rev-parse HEAD:") || !strings.Contains(strings.ToLower(text), "not a git repository") {
		t.Fatalf("CombinedOutputText error should include command and git output, got %q", text)
	}
}

func TestGitPathResolvesRelativeGitPath(t *testing.T) {
	root := initGitStateRepo(t)

	path, err := GitPath(t.Context(), root, "info/exclude")
	if err != nil {
		t.Fatalf("GitPath: %v", err)
	}

	want := filepath.Join(root, ".git", "info", "exclude")
	if path != want {
		t.Fatalf("GitPath = %q, want %q", path, want)
	}
}

func TestPathFromPorcelainLine(t *testing.T) {
	tests := []struct {
		line string
		want string
	}{
		{line: " M tracked.txt", want: "tracked.txt"},
		{line: "?? nested/file.txt", want: "nested/file.txt"},
		{line: "R  old/name.txt -> new/name.txt", want: "new/name.txt"},
		{line: "A  folder/file.txt", want: "folder/file.txt"},
		{line: " M", want: ""},
	}

	for _, tt := range tests {
		if got := PathFromPorcelainLine(tt.line); got != tt.want {
			t.Fatalf("PathFromPorcelainLine(%q) = %q, want %q", tt.line, got, tt.want)
		}
	}
}

func initGitStateRepo(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	gitStateGit(t, root, "init")
	gitStateGit(t, root, "config", "user.name", "Idiom Test")
	gitStateGit(t, root, "config", "user.email", "idiom@example.test")
	writeGitStateFile(t, filepath.Join(root, "tracked.txt"), "initial\n")
	gitStateGit(t, root, "add", "tracked.txt")
	gitStateGit(t, root, "commit", "-m", "initial")

	return root
}

func gitStateGit(t *testing.T, root string, args ...string) string {
	t.Helper()

	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, string(out))
	}

	return string(out)
}

func writeGitStateFile(t *testing.T, path string, content string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile %s: %v", path, err)
	}
}

func containsGitStateLine(lines []string, want string) bool {
	for _, line := range lines {
		if line == want || strings.Contains(line, want) {
			return true
		}
	}

	return false
}
