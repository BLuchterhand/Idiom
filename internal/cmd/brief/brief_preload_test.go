package brief

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBriefPreloadEmitsCanonicalGenericKind verifies the embedded
// happy path: --kind generic returns the canonical guidance text
// that ships in the binary. The source line goes to stderr so
// stdout stays pipe-clean for direct insertion into a brief.
func TestBriefPreloadEmitsCanonicalGenericKind(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runBriefPreload([]string{"--kind", "generic"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("preload generic must exit 0; got %d (stderr=%s)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Atom-Spec Drafting Preload") {
		t.Fatalf("expected canonical preload header in stdout: %s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "source=canonical") {
		t.Fatalf("expected canonical source tag in stderr: %s", stderr.String())
	}
}

// TestBriefPreloadProjectOverlayWins verifies the overlay seam:
// when <project>/.idiom/prompts/brief/preload/<kind>.md exists, it
// shadows the embedded canonical text. This is the seam Colony
// uses for project-specific kinds (scenario-output, etc.) without
// idiom carrying their vocabulary.
func TestBriefPreloadProjectOverlayWins(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".idiom", "prompts", "brief", "preload")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "generic.md"), []byte("PROJECT-OWNED PRELOAD\n"), 0o644); err != nil {
		t.Fatalf("write overlay: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := runBriefPreload([]string{"--kind", "generic", "--project-root", root}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("overlay path must exit 0; got %d", code)
	}
	if !strings.Contains(stdout.String(), "PROJECT-OWNED PRELOAD") {
		t.Fatalf("project overlay should win over canonical; got: %s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "source=overlay:") {
		t.Fatalf("source tag should mark the file as overlay: %s", stderr.String())
	}
}

// TestBriefPreloadUnknownKindExitsOne verifies the missing-file
// surface: --kind that resolves to neither overlay nor canonical
// is exit 1 (IO/lookup), distinct from exit 2 (flag error). The
// error message points the operator at --list.
func TestBriefPreloadUnknownKindExitsOne(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runBriefPreload([]string{"--kind", "no-such-kind"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("unknown kind must exit 1; got %d", code)
	}
	if !strings.Contains(stderr.String(), "use --list") {
		t.Fatalf("error should hint at --list: %s", stderr.String())
	}
}

// TestBriefPreloadListShowsCanonicalAndOverlayKinds verifies the
// discovery surface: --list enumerates every kind across canonical
// + overlay sources, with the source per row so the operator knows
// which one would win.
func TestBriefPreloadListShowsCanonicalAndOverlayKinds(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".idiom", "prompts", "brief", "preload")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "scenario-output.md"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := runBriefPreload([]string{"--list", "--project-root", root}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("--list must exit 0; got %d", code)
	}
	out := stdout.String()
	if !strings.Contains(out, "generic\tcanonical") {
		t.Fatalf("missing canonical generic row: %s", out)
	}
	if !strings.Contains(out, "scenario-output\toverlay") {
		t.Fatalf("missing overlay row: %s", out)
	}
}

// TestBriefPreloadRejectsPathTraversalKind closes the security
// surface added in the second-pass review: a kind containing path
// separators or `..` would resolve outside the project's preload
// directory and could leak unrelated file content. Reject before
// any filesystem read happens. Tested values cover the obvious
// attack shapes: dotdot prefix, absolute Unix path, NUL byte.
func TestBriefPreloadRejectsPathTraversalKind(t *testing.T) {
	cases := []string{"../../etc/passwd", "/etc/passwd", "..", "a/b", "a\x00b"}
	for _, kind := range cases {
		var stdout, stderr bytes.Buffer
		code := runBriefPreload([]string{"--kind", kind}, &stdout, &stderr)
		if code != 1 {
			t.Errorf("kind %q must be rejected with exit 1; got %d (stderr=%s)", kind, code, stderr.String())
		}
		if !strings.Contains(stderr.String(), "invalid kind") {
			t.Errorf("kind %q rejection should explain the cause: %s", kind, stderr.String())
		}
	}
}

// TestBriefPreloadHelpExitsZero matches the conventional CLI
// behavior: --help should exit 0, not the flag-parse error code.
func TestBriefPreloadHelpExitsZero(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runBriefPreload([]string{"--help"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("--help must exit 0; got %d", code)
	}
}

// TestBriefPreloadRequiresKindOrList verifies the input-shape
// contract: with neither --kind nor --list, the command can't know
// what to do. Exit 2 (flag error) with an actionable message.
func TestBriefPreloadRequiresKindOrList(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runBriefPreload([]string{}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("no flags must exit 2; got %d", code)
	}
	if !strings.Contains(stderr.String(), "--kind is required") {
		t.Fatalf("error should name the missing flag: %s", stderr.String())
	}
}
