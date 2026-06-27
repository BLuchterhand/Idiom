package safepath

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestUnderRootContainmentAndMissingPolicy(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "docs", "a.md"), "ok\n")

	tests := []struct {
		name    string
		path    string
		opts    Options
		wantRel string
		wantErr string
	}{
		{name: "relative existing", path: "docs/a.md", wantRel: "docs/a.md"},
		{name: "absolute existing", path: filepath.Join(root, "docs", "a.md"), wantRel: "docs/a.md"},
		{name: "empty path", path: " ", wantErr: "is empty"},
		{name: "escape path", path: filepath.Join(root, "..", "outside.md"), wantErr: "must stay under workspace root"},
		{name: "missing allowed", path: "docs/new.md", opts: Options{AllowMissing: true}, wantRel: "docs/new.md"},
		{name: "missing rejected", path: "docs/new.md", wantErr: "resolve real test path"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, rel, err := UnderRoot(root, test.path, "test path", test.opts)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("UnderRoot error = %v, want containing %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("UnderRoot error = %v", err)
			}
			if rel != test.wantRel {
				t.Fatalf("rel = %q, want %q", rel, test.wantRel)
			}
		})
	}
}

func TestRelRejectsRootAndEscapes(t *testing.T) {
	root := filepath.Clean(t.TempDir())
	if got, ok := Rel(root, filepath.Join(root, "dir", "file.txt")); !ok || got != "dir/file.txt" {
		t.Fatalf("Rel inside = %q, %t; want dir/file.txt true", got, ok)
	}
	if _, ok := Rel(root, root); ok {
		t.Fatal("Rel should reject root itself")
	}
	if _, ok := Rel(root, filepath.Dir(root)); ok {
		t.Fatal("Rel should reject parent escape")
	}
}

func TestRejectSymlinkAncestors(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation commonly requires elevated privileges on Windows")
	}

	root := t.TempDir()
	targetDir := t.TempDir()
	if err := os.Symlink(targetDir, filepath.Join(root, "linkdir")); err != nil {
		t.Fatalf("symlink ancestor: %v", err)
	}

	err := RejectSymlinkAncestors(root, filepath.Join(root, "linkdir", "file.txt"), "write path")
	if err == nil || !strings.Contains(err.Error(), "must not traverse symlinked directories") {
		t.Fatalf("RejectSymlinkAncestors error = %v", err)
	}
}

func TestRejectSymlinkTarget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation commonly requires elevated privileges on Windows")
	}

	root := t.TempDir()
	target := filepath.Join(root, "target.txt")
	mustWriteFile(t, target, "ok\n")
	link := filepath.Join(root, "link.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink target: %v", err)
	}

	err := RejectSymlinkTarget(link, "write path")
	if err == nil || !strings.Contains(err.Error(), "must not be a symlink") {
		t.Fatalf("RejectSymlinkTarget error = %v", err)
	}

	if err := RejectSymlinkTarget(filepath.Join(root, "missing.txt"), "write path"); err != nil {
		t.Fatalf("missing target should be allowed by direct target check, got %v", err)
	}
}

func mustWriteFile(t *testing.T, path string, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}
