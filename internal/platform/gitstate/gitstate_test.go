package gitstate

import (
	"context"
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
	_, err := Current(t.Context(), t.TempDir())
	if err == nil {
		t.Fatalf("Current outside git repo returned nil error")
	}

	text := err.Error()
	if !strings.Contains(text, "git rev-parse HEAD:") || !strings.Contains(strings.ToLower(text), "not a git repository") {
		t.Fatalf("Current error should include command and git output, got %q", text)
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

func TestOutputSuccessDoesNotMergeStderr(t *testing.T) {
	root := initGitStateRepo(t)

	got, err := Output(t.Context(), root, "-c", "alias.noisy=!sh -c 'printf stdout; printf stderr >&2'", "noisy")
	if err != nil {
		t.Fatalf("Output success: %v", err)
	}

	if got != "stdout" {
		t.Fatalf("Output merged stderr into stdout: got %q, want stdout", got)
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
		{line: `R  "old -> name.txt" -> "new -> name.txt"`, want: "new -> name.txt"},
		{line: `R  old.go -> " trailing.go "`, want: " trailing.go "},
		{line: ` M "space path.txt"`, want: "space path.txt"},
		{line: `?? "quote\"path.txt"`, want: `quote"path.txt`},
		{line: `?? " leading.go"`, want: " leading.go"},
		{line: `?? a -> b.txt`, want: "a -> b.txt"},
		{line: "A  folder/file.txt", want: "folder/file.txt"},
		{line: " M", want: ""},
	}

	for _, tt := range tests {
		if got := PathFromPorcelainLine(tt.line); got != tt.want {
			t.Fatalf("PathFromPorcelainLine(%q) = %q, want %q", tt.line, got, tt.want)
		}
	}
}

func TestDirtyPathsFromPorcelainFiltersDedupesAndSorts(t *testing.T) {
	got := dirtyPathsFromPorcelain(strings.Join([]string{
		" M tracked.go",
		"?? nested/new.go",
		"R  old.go -> renamed.go",
		` M "space path.go"`,
		" M .idiom/recovery/packet.json",
		" M .idiom/plan-execute/progress.json",
		" M tracked.go",
	}, "\n"), DirtyPathOptions{
		IgnorePrefixes: []string{".idiom/recovery/", ".idiom/plan-execute/"},
		Sort:           true,
	})

	want := []string{"nested/new.go", "renamed.go", "space path.go", "tracked.go"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("dirtyPathsFromPorcelain sorted = %#v, want %#v", got, want)
	}
}

func TestDirtyPathsFromPorcelainCanPreserveGitOrder(t *testing.T) {
	got := dirtyPathsFromPorcelain(strings.Join([]string{
		" M z.go",
		" M a.go",
	}, "\n"), DirtyPathOptions{})

	want := []string{"z.go", "a.go"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("dirtyPathsFromPorcelain order = %#v, want %#v", got, want)
	}
}

func TestDirtyPathsIncludesAllUntrackedFiles(t *testing.T) {
	root := initGitStateRepo(t)
	writeGitStateFile(t, filepath.Join(root, "tracked.txt"), "changed\n")
	writeGitStateFile(t, filepath.Join(root, "nested", "new.txt"), "new\n")
	writeGitStateFile(t, filepath.Join(root, `quote"path.txt`), "quoted\n")
	writeGitStateFile(t, filepath.Join(root, "a -> b.txt"), "arrow\n")
	writeGitStateFile(t, filepath.Join(root, ".idiom", "recovery", "packet.json"), "{}\n")

	got, err := DirtyPaths(t.Context(), root, DirtyPathOptions{
		IgnorePrefixes: []string{".idiom/recovery/"},
		Sort:           true,
	})
	if err != nil {
		t.Fatalf("DirtyPaths: %v", err)
	}

	want := []string{"a -> b.txt", "nested/new.txt", `quote"path.txt`, "tracked.txt"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("DirtyPaths = %#v, want %#v", got, want)
	}
}

func TestFilterPorcelainStatusByPathPrefixUsesParsedPath(t *testing.T) {
	status := strings.Join([]string{
		" M .idiom/recovery/packet.json",
		` M "docs/.idiom-note.md"`,
		"?? src/main.go",
		`R  "old -> path.go" -> ".idiom/generated.go"`,
	}, "\n")

	got := FilterPorcelainStatusByPathPrefix(status, ".idiom/")
	want := strings.Join([]string{
		`M "docs/.idiom-note.md"`,
		"?? src/main.go",
	}, "\n")
	if got != want {
		t.Fatalf("filtered status = %q, want %q", got, want)
	}
}

func TestRunnerStatusAndDiffUseInjectedCommandPolicy(t *testing.T) {
	var calls []string
	runner := Runner{
		Root: "/repo",
		RunCommand: func(ctx context.Context, root string, args ...string) (string, error) {
			if root != "/repo" {
				t.Fatalf("root = %q, want /repo", root)
			}
			calls = append(calls, strings.Join(args, " "))
			return strings.Join(args, " "), nil
		},
	}

	status, err := runner.Status(t.Context())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status != "status --porcelain=v1 --branch --untracked-files=normal" {
		t.Fatalf("Status command = %q", status)
	}

	diff, err := runner.WorktreeDiff(t.Context(), "src/file.go")
	if err != nil {
		t.Fatalf("WorktreeDiff: %v", err)
	}
	if diff != "diff --no-color -- src/file.go" {
		t.Fatalf("WorktreeDiff command = %q", diff)
	}

	want := []string{
		"status --porcelain=v1 --branch --untracked-files=normal",
		"diff --no-color -- src/file.go",
	}
	if strings.Join(calls, "\n") != strings.Join(want, "\n") {
		t.Fatalf("calls = %#v, want %#v", calls, want)
	}
}

func TestCheckHistoryApplicabilityReportsRepositoryStates(t *testing.T) {
	nonGit := t.TempDir()
	writeGitStateFile(t, filepath.Join(nonGit, "sample.go"), "package main\n")
	got, err := (Runner{Root: nonGit}).CheckHistoryApplicability(t.Context(), "sample.go")
	if err != nil {
		t.Fatalf("CheckHistoryApplicability non-git: %v", err)
	}
	if got.Applicable || got.Reason != HistoryReasonNonGitRoot {
		t.Fatalf("non-git applicability = %#v, want non_git_root", got)
	}

	noHead := t.TempDir()
	gitStateGit(t, noHead, "init")
	writeGitStateFile(t, filepath.Join(noHead, "sample.go"), "package main\n")
	got, err = (Runner{Root: noHead}).CheckHistoryApplicability(t.Context(), "sample.go")
	if err != nil {
		t.Fatalf("CheckHistoryApplicability no-head: %v", err)
	}
	if got.Applicable || got.Reason != HistoryReasonNoHead {
		t.Fatalf("no-head applicability = %#v, want no_head", got)
	}

	untracked := initGitStateRepo(t)
	writeGitStateFile(t, filepath.Join(untracked, "sample.go"), "package main\n")
	got, err = (Runner{Root: untracked}).CheckHistoryApplicability(t.Context(), "sample.go")
	if err != nil {
		t.Fatalf("CheckHistoryApplicability untracked: %v", err)
	}
	if got.Applicable || got.Reason != HistoryReasonUntrackedPath {
		t.Fatalf("untracked applicability = %#v, want untracked_path", got)
	}

	noCommittedHistory := initGitStateRepo(t)
	writeGitStateFile(t, filepath.Join(noCommittedHistory, "sample.go"), "package main\n")
	gitStateGit(t, noCommittedHistory, "add", "sample.go")
	got, err = (Runner{Root: noCommittedHistory}).CheckHistoryApplicability(t.Context(), "sample.go")
	if err != nil {
		t.Fatalf("CheckHistoryApplicability staged path: %v", err)
	}
	if got.Applicable || got.Reason != HistoryReasonPathNoCommittedHistory {
		t.Fatalf("staged path applicability = %#v, want path_no_committed_history", got)
	}

	applicable := initGitStateRepo(t)
	got, err = (Runner{Root: applicable}).CheckHistoryApplicability(t.Context(), "tracked.txt")
	if err != nil {
		t.Fatalf("CheckHistoryApplicability applicable: %v", err)
	}
	if !got.Applicable || got.Reason != "" {
		t.Fatalf("applicable history = %#v, want applicable", got)
	}
}

func TestCheckHistoryApplicabilityMatchesExactPaths(t *testing.T) {
	root := initGitStateRepo(t)
	writeGitStateFile(t, filepath.Join(root, "dir", "space path.go"), "package main\n")
	writeGitStateFile(t, filepath.Join(root, "dir", "space path.go.bak"), "package main\n")
	gitStateGit(t, root, "add", "dir/space path.go.bak")
	gitStateGit(t, root, "commit", "-m", "add sibling")

	got, err := (Runner{Root: root}).CheckHistoryApplicability(t.Context(), "dir/space path.go")
	if err != nil {
		t.Fatalf("CheckHistoryApplicability: %v", err)
	}
	if got.Applicable || got.Reason != HistoryReasonUntrackedPath {
		t.Fatalf("untracked path with committed sibling = %#v, want untracked_path", got)
	}

	got, err = (Runner{Root: root}).CheckHistoryApplicability(t.Context(), "dir/space path.go.bak")
	if err != nil {
		t.Fatalf("CheckHistoryApplicability sibling: %v", err)
	}
	if !got.Applicable {
		t.Fatalf("committed sibling applicability = %#v, want applicable", got)
	}
}

func TestHistoryCommandsUseStableGitForms(t *testing.T) {
	root := t.TempDir()
	gitStateGit(t, root, "init")
	gitStateGit(t, root, "config", "user.email", "test@example.com")
	gitStateGit(t, root, "config", "user.name", "Test User")
	writeGitStateFile(t, filepath.Join(root, "sample.go"), "package main\n\nfunc add(a int, b int) int {\n\treturn a + b\n}\n")
	gitStateGit(t, root, "add", "sample.go")
	gitStateGit(t, root, "commit", "-m", "initial")
	writeGitStateFile(t, filepath.Join(root, "sample.go"), "package main\n\nfunc add(a int, b int) int {\n\treturn a - b\n}\n")
	gitStateGit(t, root, "commit", "-am", "change add")

	runner := Runner{Root: root}

	history, err := runner.HistoryRange(t.Context(), "sample.go", 3, 5)
	if err != nil {
		t.Fatalf("HistoryRange: %v\n%s", err, history.Output)
	}
	if !history.HasCommitDescriptions {
		t.Fatalf("HistoryRange should report commit descriptions: %#v", history)
	}
	if !strings.Contains(history.Output, "change add") || !strings.Contains(history.Output, "initial") {
		t.Fatalf("HistoryRange missing expected commits:\n%s", history.Output)
	}

	symbol, err := runner.HistorySymbol(t.Context(), "sample.go", "add")
	if err != nil {
		t.Fatalf("HistorySymbol: %v\n%s", err, symbol.Output)
	}
	if !symbol.HasCommitDescriptions {
		t.Fatalf("HistorySymbol should report commit descriptions: %#v", symbol)
	}
	if !strings.Contains(symbol.Output, "change add") || !strings.Contains(symbol.Output, "initial") {
		t.Fatalf("HistorySymbol missing expected commits:\n%s", symbol.Output)
	}

	blame, err := runner.BlameRange(t.Context(), "sample.go", 4, 4)
	if err != nil {
		t.Fatalf("BlameRange: %v\n%s", err, blame)
	}
	if !strings.Contains(blame, "return a - b") {
		t.Fatalf("BlameRange missing changed line:\n%s", blame)
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
