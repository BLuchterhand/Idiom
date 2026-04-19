package promptstack

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"qwen-bubbles/internal/config"
)

func TestBuildComposesPromptSourcesAndDeduplicatesFiles(t *testing.T) {
	dir := t.TempDir()
	baseline := filepath.Join(dir, "base.md")
	profiles := filepath.Join(dir, "profiles")
	project := filepath.Join(dir, "project")
	if err := os.MkdirAll(profiles, 0o755); err != nil {
		t.Fatalf("MkdirAll profiles: %v", err)
	}
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatalf("MkdirAll project: %v", err)
	}
	writeFile(t, baseline, "Base behavior")
	writeFile(t, filepath.Join(profiles, "coding.md"), "Coding behavior")
	writeFile(t, filepath.Join(project, "AGENTS.md"), "Project behavior")

	stack, err := Build(config.Chat{
		SystemPrompt:       "Inline behavior",
		SystemPromptFiles:  []string{baseline, baseline},
		ProfilesDir:        profiles,
		ProjectRoot:        project,
		ProjectPromptFiles: []string{filepath.Join(project, "AGENTS.md")},
	}, []string{"coding"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	for _, want := range []string{"[inline behavior: inline]", "Inline behavior", "[baseline behavior: base.md]", "[behavior profile: profile:coding]", "[project behavior: AGENTS.md]"} {
		if !strings.Contains(stack.Text, want) {
			t.Fatalf("expected stack text to include %q, got:\n%s", want, stack.Text)
		}
	}
	if got := countSource(stack.Sources, baseline); got != 1 {
		t.Fatalf("expected duplicate baseline to be loaded once, got %d", got)
	}
	if got := countSource(stack.Sources, filepath.Join(project, "AGENTS.md")); got != 1 {
		t.Fatalf("expected auto and explicit project prompt to dedupe, got %d", got)
	}
}

func TestBuildRejectsMissingRequiredPrompt(t *testing.T) {
	_, err := Build(config.Chat{SystemPromptFiles: []string{filepath.Join(t.TempDir(), "missing.md")}}, nil)
	if err == nil || !strings.Contains(err.Error(), "load baseline prompt") {
		t.Fatalf("expected missing required prompt error, got %v", err)
	}
}

func TestListProfilesReturnsSortedMarkdownNames(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "zeta.md"), "z")
	writeFile(t, filepath.Join(dir, "alpha.md"), "a")
	writeFile(t, filepath.Join(dir, "ignore.txt"), "x")
	if err := os.Mkdir(filepath.Join(dir, "nested.md"), 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}

	profiles, err := ListProfiles(dir)
	if err != nil {
		t.Fatalf("ListProfiles: %v", err)
	}
	if got, want := strings.Join(profiles, ","), "alpha,zeta"; got != want {
		t.Fatalf("expected profiles %q, got %q", want, got)
	}
}

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile %s: %v", path, err)
	}
}

func countSource(sources []Source, path string) int {
	count := 0
	for _, source := range sources {
		if source.Path == path {
			count++
		}
	}
	return count
}
