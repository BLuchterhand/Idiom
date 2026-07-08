package implementationdispatch

import "testing"

func TestWorkspaceFileRejectsUnsafeRelativePaths(t *testing.T) {
	root := t.TempDir()

	for _, path := range []string{"../outside.md", `C:\repo\outside.md`, "/tmp/outside.md"} {
		if got, ok := WorkspaceFile(root, path); ok {
			t.Fatalf("WorkspaceFile(%q) = %q, true; want rejection", path, got)
		}
	}

	if got, ok := WorkspaceFile(root, "docs/new.md"); !ok || got == "" {
		t.Fatalf("WorkspaceFile should accept workspace-relative paths, got %q, %v", got, ok)
	}
}
