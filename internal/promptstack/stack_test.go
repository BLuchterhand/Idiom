package promptstack

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"idiom/internal/config"
)

func TestBuildComposesPromptSourcesAndDeduplicatesFiles(t *testing.T) {
	t.Setenv("IDIOM_CONFIG_DIR", filepath.Join(t.TempDir(), "no-user-config"))
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

func TestBuildIncludesOpenCodeStylePromptGuidance(t *testing.T) {
	t.Setenv("IDIOM_CONFIG_DIR", filepath.Join(t.TempDir(), "no-user-config"))
	baseline := filepath.Join("..", "..", "prompts", "base.md")
	profiles := filepath.Join("..", "..", "prompts", "profiles")

	stack, err := Build(config.Chat{
		SystemPromptFiles: []string{baseline},
		ProfilesDir:       profiles,
	}, []string{"coding"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	for _, want := range []string{
		"Keep a short working checklist",
		"Separate read-only analysis from requested mutation",
		"split it into focused passes and verify each pass",
		"Keep a short task ledger",
		"Return concise final answers",
	} {
		if !strings.Contains(stack.Text, want) {
			t.Fatalf("expected prompt stack to include %q, got:\n%s", want, stack.Text)
		}
	}
}

func TestBuildAutoIncludesGeneratedProjectContext(t *testing.T) {
	t.Setenv("IDIOM_CONFIG_DIR", filepath.Join(t.TempDir(), "no-user-config"))
	dir := t.TempDir()
	project := filepath.Join(dir, "project")
	if err := os.MkdirAll(filepath.Join(project, ".idiom", "discovery"), 0o755); err != nil {
		t.Fatalf("MkdirAll discovery: %v", err)
	}
	writeFile(t, filepath.Join(project, "AGENTS.md"), "Human guidance")
	writeFile(t, filepath.Join(project, ".idiom", "discovery", "context.md"), "Generated guidance")

	stack, err := Build(config.Chat{ProjectRoot: project}, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !strings.Contains(stack.Text, "[project behavior: .idiom/discovery/context.md]") {
		t.Fatalf("expected stack text to include generated context, got:\n%s", stack.Text)
	}
	if got := countSource(stack.Sources, filepath.Join(project, ".idiom", "discovery", "context.md")); got != 1 {
		t.Fatalf("expected generated context source once, got %d", got)
	}
}

func TestBuildUsesCustomProjectPromptCandidates(t *testing.T) {
	t.Setenv("IDIOM_CONFIG_DIR", filepath.Join(t.TempDir(), "no-user-config"))
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Join(project, ".idiom"), 0o755); err != nil {
		t.Fatalf("MkdirAll .idiom: %v", err)
	}
	writeFile(t, filepath.Join(project, "AGENTS.md"), "Large root guidance")
	writeFile(t, filepath.Join(project, "CLAUDE.md"), "Large alternate guidance")
	writeFile(t, filepath.Join(project, ".idiom", "implementation.md"), "Compact implementation guidance")

	stack, err := Build(config.Chat{
		ProjectRoot:             project,
		ProjectPromptCandidates: []string{".idiom/implementation.md"},
	}, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !strings.Contains(stack.Text, "Compact implementation guidance") {
		t.Fatalf("expected compact implementation guidance, got:\n%s", stack.Text)
	}
	if strings.Contains(stack.Text, "Large root guidance") || strings.Contains(stack.Text, "Large alternate guidance") {
		t.Fatalf("expected custom candidates to replace default AGENTS/CLAUDE scan, got:\n%s", stack.Text)
	}
}

func TestBuildCanDisableAutoProjectPromptCandidates(t *testing.T) {
	t.Setenv("IDIOM_CONFIG_DIR", filepath.Join(t.TempDir(), "no-user-config"))
	project := t.TempDir()
	writeFile(t, filepath.Join(project, "AGENTS.md"), "Project behavior")

	stack, err := Build(config.Chat{
		ProjectRoot:             project,
		ProjectPromptCandidates: []string{},
	}, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if strings.Contains(stack.Text, "Project behavior") {
		t.Fatalf("expected empty candidate list to disable auto project prompts, got:\n%s", stack.Text)
	}
}

func TestBuildAutoIncludesProjectIdiomFile(t *testing.T) {
	t.Setenv("IDIOM_CONFIG_DIR", filepath.Join(t.TempDir(), "no-user-config"))
	project := t.TempDir()
	writeFile(t, filepath.Join(project, "IDIOM.md"), "Project idiom guidance")

	stack, err := Build(config.Chat{ProjectRoot: project}, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !strings.Contains(stack.Text, "[project behavior: IDIOM.md]") {
		t.Fatalf("expected stack text to include project IDIOM.md section, got:\n%s", stack.Text)
	}
	if !strings.Contains(stack.Text, "Project idiom guidance") {
		t.Fatalf("expected stack text to include project IDIOM.md body, got:\n%s", stack.Text)
	}
}

func TestBuildLoadsUserGlobalPromptsFromConfigDir(t *testing.T) {
	configDir := t.TempDir()
	writeFile(t, filepath.Join(configDir, "IDIOM.md"), "Cross-project idiom rules")
	t.Setenv("IDIOM_CONFIG_DIR", configDir)

	project := t.TempDir()
	writeFile(t, filepath.Join(project, "IDIOM.md"), "Project idiom guidance")

	stack, err := Build(config.Chat{ProjectRoot: project}, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !strings.Contains(stack.Text, "[user behavior: IDIOM.md]") {
		t.Fatalf("expected stack text to include user-global IDIOM.md section, got:\n%s", stack.Text)
	}
	if !strings.Contains(stack.Text, "Cross-project idiom rules") {
		t.Fatalf("expected stack text to include user-global body, got:\n%s", stack.Text)
	}
	userIdx := strings.Index(stack.Text, "[user behavior: IDIOM.md]")
	projectIdx := strings.Index(stack.Text, "[project behavior: IDIOM.md]")
	if userIdx < 0 || projectIdx < 0 || userIdx >= projectIdx {
		t.Fatalf("expected user-global section to precede project section, got user=%d project=%d", userIdx, projectIdx)
	}
}

func TestBuildSkipsUserGlobalWhenConfigDirEmpty(t *testing.T) {
	t.Setenv("IDIOM_CONFIG_DIR", filepath.Join(t.TempDir(), "missing"))

	stack, err := Build(config.Chat{}, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if strings.Contains(stack.Text, "[user behavior:") {
		t.Fatalf("expected no user-global section when config dir is empty, got:\n%s", stack.Text)
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
