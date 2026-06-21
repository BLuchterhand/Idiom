package init

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	platformconfig "idiom/internal/platform/config"
	"idiom/internal/runtime/backends"
)

// expectedSeedFiles is the relative path set runInitMain must
// create. Centralized so the four behavior tests below can assert
// against the same surface — if the seed table grows, only this
// list updates.
var expectedSeedFiles = []string{
	"config.json",
	"backends.json",
	"external_tools/README.md",
	"postflight/checks/README.md",
	"prompts/base.md",
	"prompts/lanes/README.md",
	"prompts/rules/README.md",
	"prompts/profiles/coding.md",
	"prompts/profiles/claude-qwen-worker.md",
}

// TestRunInitMainCreatesFreshSkeleton is the canonical happy path:
// runInitMain in a clean tempdir creates the entire .idiom/
// skeleton, prints one "created" line per file plus a final
// "Initialized" summary, and exits 0.
func TestRunInitMainCreatesFreshSkeleton(t *testing.T) {
	root := t.TempDir()

	var stdout, stderr bytes.Buffer

	code := RunMain([]string{root}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("fresh init must exit 0; got %d (stderr=%s)", code, stderr.String())
	}

	for _, rel := range expectedSeedFiles {
		path := filepath.Join(root, ".idiom", rel)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected seed file missing: %s (%v)", path, err)
		}
	}

	out := stdout.String()
	if !strings.Contains(out, "Initialized .idiom/") {
		t.Fatalf("stdout should announce initialization: %s", out)
	}
}

// TestRunInitMainRejectsExistingDotIdiom verifies the safety net:
// without --merge, an existing .idiom/ dir is treated as already
// initialized. Refuse to clobber whatever's there. Exit 2 with a
// message that points the operator at --merge.
func TestRunInitMainRejectsExistingDotIdiom(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".idiom"), 0o755); err != nil {
		t.Fatalf("pre-create .idiom: %v", err)
	}

	var stdout, stderr bytes.Buffer

	code := RunMain([]string{root}, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("existing .idiom/ without --merge must exit 2; got %d", code)
	}

	if !strings.Contains(stderr.String(), "already exists") {
		t.Fatalf("stderr should explain the refusal: %s", stderr.String())
	}

	if !strings.Contains(stderr.String(), "--merge") {
		t.Fatalf("stderr should suggest --merge: %s", stderr.String())
	}
}

// TestRunInitMainMergeAddsMissingOnly verifies the upgrade path:
// with --merge, files that already exist are preserved and only
// the missing ones get created. The pre-existing config.json
// content survives the run.
func TestRunInitMainMergeAddsMissingOnly(t *testing.T) {
	root := t.TempDir()
	dotIdiom := filepath.Join(root, ".idiom")

	if err := os.MkdirAll(dotIdiom, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	sentinel := []byte(`{"app_name": "PRESERVED-BY-MERGE"}`)
	if err := os.WriteFile(filepath.Join(dotIdiom, "config.json"), sentinel, 0o644); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	var stdout, stderr bytes.Buffer

	code := RunMain([]string{"--merge", root}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("merge must exit 0; got %d (stderr=%s)", code, stderr.String())
	}

	preserved, err := os.ReadFile(filepath.Join(dotIdiom, "config.json"))
	if err != nil {
		t.Fatalf("read preserved config: %v", err)
	}

	if !bytes.Equal(preserved, sentinel) {
		t.Fatalf("merge must NOT overwrite existing config.json; got %q", preserved)
	}

	if !strings.Contains(stdout.String(), "(skipped existing") {
		t.Fatalf("stdout should report skipped files: %s", stdout.String())
	}

	// Other files SHOULD have been created.
	if _, err := os.Stat(filepath.Join(dotIdiom, "backends.json")); err != nil {
		t.Fatalf("merge must still create missing backends.json: %v", err)
	}
}

func TestRunInitMainGlobalMergeCreatesUserConfigSkeleton(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	var stdout, stderr bytes.Buffer

	code := RunMain([]string{"--global", "--merge"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("global merge init must exit 0; got %d (stderr=%s)", code, stderr.String())
	}

	globalDir := filepath.Join(home, ".idiom")
	for _, rel := range expectedSeedFiles {
		path := filepath.Join(globalDir, rel)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected global seed file missing: %s (%v)", path, err)
		}
	}
	if _, err := os.Stat(filepath.Join(globalDir, ".idiom")); !os.IsNotExist(err) {
		t.Fatalf("global init must write directly under ~/.idiom, not ~/.idiom/.idiom (err=%v)", err)
	}

	cfg, err := platformconfig.Load(filepath.Join(t.TempDir(), "missing-config.json"))
	if err != nil {
		t.Fatalf("global config must load through fallback: %v", err)
	}
	if got := cfg.LlamaServer.Kind; got != platformconfig.BackendDeepSeek {
		t.Fatalf("global config should seed hosted default, got kind %q", got)
	}
	if len(cfg.Chat.SystemPromptFiles) == 0 || !strings.Contains(cfg.Chat.SystemPromptFiles[0], filepath.Join(".idiom", "prompts", "base.md")) {
		t.Fatalf("global config should resolve seeded prompt path under ~/.idiom, got %#v", cfg.Chat.SystemPromptFiles)
	}

	backendFile, err := backends.Load(filepath.Join(globalDir, "backends.json"))
	if err != nil {
		t.Fatalf("seeded global backends.json must parse: %v", err)
	}
	if backendFile.DefaultProfile != "deepseek-v4-flash" {
		t.Fatalf("unexpected default backend profile: %q", backendFile.DefaultProfile)
	}
}

func TestRunInitMainGlobalMergePreservesExistingConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	globalDir := filepath.Join(home, ".idiom")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatalf("mkdir global dir: %v", err)
	}
	sentinel := []byte(`{"app_name":"PRESERVED-GLOBAL"}`)
	if err := os.WriteFile(filepath.Join(globalDir, "config.json"), sentinel, 0o644); err != nil {
		t.Fatalf("write sentinel config: %v", err)
	}

	var stdout, stderr bytes.Buffer

	code := RunMain([]string{"--global", "--merge"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("global merge init must exit 0; got %d (stderr=%s)", code, stderr.String())
	}

	preserved, err := os.ReadFile(filepath.Join(globalDir, "config.json"))
	if err != nil {
		t.Fatalf("read global config: %v", err)
	}
	if !bytes.Equal(preserved, sentinel) {
		t.Fatalf("global merge must not overwrite existing config.json; got %q", preserved)
	}
	if _, err := os.Stat(filepath.Join(globalDir, "backends.json")); err != nil {
		t.Fatalf("global merge must still create missing backends.json: %v", err)
	}
}

func TestRunInitMainGlobalRejectsTargetDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	var stdout, stderr bytes.Buffer

	code := RunMain([]string{"--global", t.TempDir()}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("--global with target must exit 2; got %d", code)
	}
	if !strings.Contains(stderr.String(), "--global does not accept") {
		t.Fatalf("stderr should explain target rejection: %s", stderr.String())
	}
}

// TestRunInitMainSeedsPromptTemplatesNonEmpty verifies Phase 7.2:
// the canonical base prompt and chat profiles are embedded and copied
// into .idiom/prompts/. Asserts the seeded files have non-empty
// content (the embed didn't ship blank stubs).
func TestRunInitMainSeedsPromptTemplatesNonEmpty(t *testing.T) {
	root := t.TempDir()

	var stdout, stderr bytes.Buffer

	if code := RunMain([]string{root}, &stdout, &stderr); code != 0 {
		t.Fatalf("init must exit 0; got %d (stderr=%s)", code, stderr.String())
	}

	for _, rel := range []string{"prompts/base.md", "prompts/profiles/coding.md", "prompts/profiles/claude-qwen-worker.md"} {
		body, err := os.ReadFile(filepath.Join(root, ".idiom", rel))
		if err != nil {
			t.Fatalf("read seeded %s: %v", rel, err)
		}

		if len(bytes.TrimSpace(body)) == 0 {
			t.Fatalf("seeded %s is empty — go:embed produced an empty file", rel)
		}
	}
}

// TestRunInitMainRejectsMissingTargetDir verifies the input
// validation: pointing at a nonexistent path is exit 2 (operator
// error) with a clear message, not exit 1 (IO failure).
func TestRunInitMainRejectsMissingTargetDir(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := RunMain([]string{"/definitely/no/such/path/idiom-init-test"}, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("missing target dir must exit 2; got %d", code)
	}

	if !strings.Contains(stderr.String(), "does not exist") {
		t.Fatalf("stderr should explain missing target: %s", stderr.String())
	}
}
