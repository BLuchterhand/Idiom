package app

import (
	"os"
	"path/filepath"
	"testing"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/platform/config"
)

func TestHasReadableWorkspaceFilesDetectsProjectEvidence(t *testing.T) {
	tests := []struct {
		name  string
		files []string
		want  bool
	}{
		{
			name: "empty workspace",
			want: false,
		},
		{
			name:  "root readme",
			files: []string{"README.md"},
			want:  true,
		},
		{
			name:  "nested docs only workspace",
			files: []string{"docs/evidence.md"},
			want:  true,
		},
		{
			name:  "nested source workspace",
			files: []string{"cmd/app/main.go"},
			want:  true,
		},
		{
			name:  "ignored metadata only",
			files: []string{".git/config", ".idiom/cache/state.json", "node_modules/pkg/index.js"},
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			for _, file := range tt.files {
				writePromptRoutingTestFile(t, root, file)
			}

			if got := hasReadableWorkspaceFiles(root); got != tt.want {
				t.Fatalf("hasReadableWorkspaceFiles()=%t, want %t", got, tt.want)
			}
		})
	}
}

func TestGreenfieldCreationConfirmationSkipsReadOnlyDesignQuestion(t *testing.T) {
	root := t.TempDir()
	model := &Model{
		workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
	}
	plan := promptExecutionPlan{
		route: turnRoutePlan{
			Family:      "conversation",
			InitialLane: "main",
			Contract:    "direct_answer",
			WriteIntent: "no",
		},
	}
	prompt := `What would a decent "LLM brain" project look like? something that can maintain context across projects and reduce overall token consumption through clever documentation management?`

	if model.greenfieldCreationRequiresConfirmation(prompt, plan) {
		t.Fatal("greenfield design question should reach the direct-answer model path")
	}
}

func TestGreenfieldCreationConfirmationStillCatchesMisroutedCreationPrompt(t *testing.T) {
	root := t.TempDir()
	model := &Model{
		workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
	}
	readOnlyPlan := promptExecutionPlan{
		route: turnRoutePlan{
			Family:      "conversation",
			InitialLane: "main",
			Contract:    "direct_answer",
			WriteIntent: "no",
		},
	}
	prompt := "Create a simple browser app."

	if !model.greenfieldCreationRequiresConfirmation(prompt, readOnlyPlan) {
		t.Fatal("misrouted greenfield creation prompt should still ask for lane confirmation")
	}

	writePromptRoutingTestFile(t, root, "README.md")
	if model.greenfieldCreationRequiresConfirmation(prompt, readOnlyPlan) {
		t.Fatal("non-empty workspace should not trigger greenfield creation confirmation")
	}
}

func writePromptRoutingTestFile(t *testing.T, root string, rel string) {
	t.Helper()

	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}

	if err := os.WriteFile(path, []byte("test\n"), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
