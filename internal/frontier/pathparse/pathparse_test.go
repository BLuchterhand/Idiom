package pathparse

import "testing"

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
