package init

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
