package pathparse

import (
	"strings"
	"testing"
)

func TestMentionedWorkspacePathsNormalizesEscapedNewlinePrefix(t *testing.T) {
	text := `"\ninternal/agent/runner_child_tasks.go" internal/agent/runner.go`
	got := MentionedWorkspacePaths(text)
	want := []string{
		"internal/agent/runner_child_tasks.go",
		"internal/agent/runner.go",
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d normalized paths, got %#v", len(want), got)
	}
	for i, value := range want {
		if got[i] != value {
			t.Fatalf("expected normalized paths %#v, got %#v", want, got)
		}
	}
}

func TestMentionedWorkspacePathsCoversCommonSourceExtensions(t *testing.T) {
	text := strings.Join([]string{
		"native/src/system.cpp",
		"native/include/system.hpp:12",
		"scripts/player.gd",
		"src/app.tsx",
		"pkg/service.py",
		"src/main.rs",
		"src/App.java",
		"tools/build.sh",
	}, " ")
	got := MentionedWorkspacePaths(text)
	want := []string{
		"native/src/system.cpp",
		"native/include/system.hpp",
		"scripts/player.gd",
		"src/app.tsx",
		"pkg/service.py",
		"src/main.rs",
		"src/App.java",
		"tools/build.sh",
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d paths, got %#v", len(want), got)
	}
	for i, value := range want {
		if got[i] != value {
			t.Fatalf("expected paths %#v, got %#v", want, got)
		}
	}
}

func TestMentionedWorkspacePathsUnderHeadings(t *testing.T) {
	text := strings.Join([]string{
		"# Narrow atom",
		"",
		"Files-to-Modify:",
		"- native/src/item_lifecycle.cpp",
		"- scripts/player.gd",
		"",
		"Evidence:",
		"- docs/design/spec.md",
	}, "\n")
	got := MentionedWorkspacePathsUnderHeadings(text, "files to modify", "files-to-modify")
	want := []string{"native/src/item_lifecycle.cpp", "scripts/player.gd"}
	if len(got) != len(want) {
		t.Fatalf("expected %d paths, got %#v", len(want), got)
	}
	for i, value := range want {
		if got[i] != value {
			t.Fatalf("expected paths %#v, got %#v", want, got)
		}
	}
}
