package languagetools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func gitIn(t *testing.T, root string, args ...string) {
	t.Helper()

	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
	cmd.Dir = root

	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func writeFile(t *testing.T, path string, content string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The index key must change whenever the indexed content can change: a dirty
// tracked file, an untracked file and a gitignored C++ file the walk still reads.
func TestCppSnapshotKeyCoversDirtyAndIgnoredFiles(t *testing.T) {
	root := t.TempDir()
	gitIn(t, root, "init", "-q")
	writeFile(t, filepath.Join(root, ".gitignore"), "ign/\n")
	writeFile(t, filepath.Join(root, "a.cpp"), "int a() { return 1; }\n")
	gitIn(t, root, "add", ".")
	gitIn(t, root, "commit", "-q", "-m", "init")

	key := func() string {
		info := cppSnapshot(context.Background(), root)
		if info.GitError != "" || info.Key == "" {
			t.Fatalf("snapshot failed: %q", info.GitError)
		}

		return info.Key
	}

	clean := key()
	if key() != clean {
		t.Fatal("key is not stable for an unchanged tree")
	}

	writeFile(t, filepath.Join(root, "ign", "x.h"), "int x();\n")

	withIgnored := key()
	if withIgnored == clean {
		t.Fatal("a gitignored C++ file does not change the key")
	}

	writeFile(t, filepath.Join(root, "ign", "x.h"), "int y();\n")

	if key() == withIgnored {
		t.Fatal("editing a gitignored C++ file does not change the key")
	}

	before := key()

	writeFile(t, filepath.Join(root, "a.cpp"), "int a() { return 2; }\n")

	if key() == before {
		t.Fatal("editing a tracked C++ file does not change the key")
	}
}
