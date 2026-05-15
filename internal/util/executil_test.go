package util

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestRunCmdEcho(t *testing.T) {
	out, err := RunCmd(context.Background(), "", "echo", "hello world")
	if err != nil {
		t.Fatalf("RunCmd echo: %v", err)
	}
	if out != "hello world" {
		t.Errorf("RunCmd echo = %q, want %q", out, "hello world")
	}
}

func TestRunCmdTrimsOutput(t *testing.T) {
	out, err := RunCmd(context.Background(), "", "echo", "  spaces  ")
	if err != nil {
		t.Fatal(err)
	}
	if out != "spaces" {
		t.Errorf("RunCmd should trim, got %q", out)
	}
}

func TestRunCmdMultiLine(t *testing.T) {
	out, err := RunCmd(context.Background(), "", "printf", "line1\nline2\nline3")
	if err != nil {
		t.Fatal(err)
	}
	if out != "line1\nline2\nline3" {
		t.Errorf("multi-line = %q", out)
	}
}

func TestRunCmdWorkingDir(t *testing.T) {
	out, err := RunCmd(context.Background(), "/", "pwd")
	if err != nil {
		t.Fatal(err)
	}
	if out != "/" {
		t.Errorf("pwd in / = %q, want %q", out, "/")
	}
}

func TestRunCmdNoSuchCommand(t *testing.T) {
	_, err := RunCmd(context.Background(), "", "no_such_binary_xyz_12345")
	if err == nil {
		t.Fatal("expected error for missing binary")
	}
}

func TestRunCmdNonZeroExit(t *testing.T) {
	_, err := RunCmd(context.Background(), "", "false")
	if err == nil {
		t.Fatal("expected error for non-zero exit")
	}
}

func TestRunCmdCaptureCombinedOutput(t *testing.T) {
	out, err := RunCmd(context.Background(), "", "sh", "-c", "echo stdout; echo stderr >&2")
	if err != nil {
		t.Fatalf("sh -c: %v", err)
	}
	if !strings.Contains(out, "stdout") {
		t.Errorf("combined output missing stdout: %q", out)
	}
	if !strings.Contains(out, "stderr") {
		t.Errorf("combined output missing stderr: %q", out)
	}
}

func TestRunCmdContextCancellation(t *testing.T) {
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("sleep not found")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := RunCmd(ctx, "", "sleep", "10")
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

func TestRunCmdEmptyDirUsesCurrent(t *testing.T) {
	out, err := RunCmd(context.Background(), "", "pwd")
	if err != nil {
		t.Fatal(err)
	}
	if out == "" {
		t.Error("expected non-empty pwd output")
	}
}

func TestGitOutputInGitRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found")
	}

	out, err := GitOutput(context.Background(), ".", "rev-parse", "--git-dir")
	if err != nil {
		t.Fatalf("GitOutput in repo: %v", err)
	}
	if out == "" {
		t.Error("expected non-empty git output")
	}
}

func TestGitOutputNoSuchRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found")
	}

	tmp := t.TempDir()
	_, err := GitOutput(context.Background(), tmp, "rev-parse", "HEAD")
	if err == nil {
		t.Error("expected error for non-git directory")
	}
}

func TestGitSnapshotInGitRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found")
	}

	head, status, err := GitSnapshot(context.Background(), ".")
	if err != nil {
		t.Fatalf("GitSnapshot: %v", err)
	}
	if len(head) != 40 {
		t.Errorf("HEAD commit hash length = %d, want 40: %q", len(head), head)
	}
	for _, c := range head {
		if !strings.Contains("0123456789abcdef", string(c)) {
			t.Errorf("HEAD contains unexpected char %q", c)
			break
		}
	}
	_ = status
}

func TestGitSnapshotCleanRepoStatus(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found")
	}

	_, status, err := GitSnapshot(context.Background(), ".")
	if err != nil {
		t.Fatal(err)
	}
	if status != "" {
		t.Logf("repo has changes (status=%q); that's fine, just noting", status)
	}
}

func TestGitSnapshotCancelledContext(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, err := GitSnapshot(ctx, ".")
	if err == nil {
		t.Error("expected error with cancelled context")
	}
}

func BenchmarkRunCmd(b *testing.B) {
	ctx := context.Background()
	for b.Loop() {
		_, err := RunCmd(ctx, "", "echo", "bench")
		if err != nil {
			b.Fatal(err)
		}
	}
}
