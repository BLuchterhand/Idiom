package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// makeEnumMirrorFixture writes a tiny C++-ish corpus to a temp dir
// so the audit has something to walk. Centralized helper keeps the
// test bodies focused on the assertion they're making.
func makeEnumMirrorFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"enums.h": "enum class AnimalType { Cat, Dog, COUNT };\n",
		"diagnostic_logger.cpp": `#include "enums.h"
const char* AnimalType_NAMES[] = {"cat", "dog"};
static_assert(sizeof(AnimalType_NAMES)/sizeof(*AnimalType_NAMES) == (int)AnimalType::COUNT, "name count drift");
`,
		"behavior.cpp": `#include "enums.h"
void handle(AnimalType t) {
    switch (t) {
        case AnimalType::Cat: break;
        case AnimalType::Dog: break;
    }
}
`,
		// Should be skipped: extension not in the default set
		"notes.txt": "AnimalType discussion goes here\n",
		// Should be ignored: in skip-listed dir
		"node_modules/index.js": "AnimalType\n",
	}
	for rel, body := range files {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	return root
}

// TestAuditEnumMirrorsListsReferencingFiles is the canonical happy
// path: the symbol is referenced from two source files; the audit
// lists both, surfaces the static_assert + switch hits, and prints
// the Files-To-Modify candidate block. txt and node_modules entries
// must be skipped — extension and directory filters were the two
// specific contracts the Colony script exercised.
func TestAuditEnumMirrorsListsReferencingFiles(t *testing.T) {
	root := makeEnumMirrorFixture(t)

	var stdout, stderr bytes.Buffer
	code := runAuditEnumMirrors([]string{"--enum", "AnimalType", "--root", root}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("audit must exit 0 on a clean run; got %d (stderr=%s)", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{"diagnostic_logger.cpp", "behavior.cpp", "enums.h", "Step 1", "Step 2", "Step 3"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in output: %s", want, out)
		}
	}
	if strings.Contains(out, "notes.txt") {
		t.Fatalf("non-source extension must be skipped: %s", out)
	}
	if strings.Contains(out, "node_modules") {
		t.Fatalf("skip-listed dir must be excluded: %s", out)
	}
	// At least one of the high-signal patterns should fire. We don't
	// pin the exact line text so future regex tweaks don't break the
	// test, but the section header proves the scan ran.
	if !strings.Contains(out, "static_assert") && !strings.Contains(out, "case AnimalType::") {
		t.Fatalf("expected at least one mirror-pattern hit in the scan section: %s", out)
	}
}

// TestAuditEnumMirrorsRejectsMissingEnumFlag verifies the input
// contract: --enum is required. Without it we have nothing to
// search for, so the command must surface a flag error rather
// than walking the tree to no purpose.
func TestAuditEnumMirrorsRejectsMissingEnumFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runAuditEnumMirrors([]string{"--root", t.TempDir()}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("missing --enum must exit 2; got %d", code)
	}
	if !strings.Contains(stderr.String(), "--enum is required") {
		t.Fatalf("stderr should name the missing flag: %s", stderr.String())
	}
}

// TestAuditEnumMirrorsReportsZeroReferences verifies the no-match
// path: a symbol that's nowhere in the tree exits 1 with a clear
// "verify the spelling" hint. Distinguished from exit 2 (flag
// error) so shell pipelines can react differently.
func TestAuditEnumMirrorsReportsZeroReferences(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runAuditEnumMirrors([]string{"--enum", "NoSuchSymbol", "--root", t.TempDir()}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("zero-reference audit should exit 1; got %d", code)
	}
	if !strings.Contains(stdout.String(), "no references found") {
		t.Fatalf("output should explain the empty result: %s", stdout.String())
	}
}

// TestAuditEnumMirrorsTokenBoundaryRejectsSubstringMatch is the
// critical correctness guard: a search for `MyEnum` must NOT match
// a file that only contains `MyEnumOther`. The Colony script never
// had this property (it used grep without word boundaries) and it
// was a real source of false positives — without the boundary
// check, every search returned spurious files that shared a prefix
// or suffix with the symbol name.
func TestAuditEnumMirrorsTokenBoundaryRejectsSubstringMatch(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "different.cpp"),
		[]byte("MyEnumOther x; // unrelated symbol\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "real.cpp"),
		[]byte("MyEnum y;\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := runAuditEnumMirrors([]string{"--enum", "MyEnum", "--root", root}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("audit must exit 0; got %d (stderr=%s)", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "real.cpp") {
		t.Fatalf("token-bounded match should land on real.cpp: %s", out)
	}
	if strings.Contains(out, "different.cpp") {
		t.Fatalf("token boundary must reject MyEnumOther; got: %s", out)
	}
}

// TestAuditEnumMirrorsLanguageProfileSelection covers the --language
// flag added for spec parity. The Go profile uses the stringer-
// generated `_<Enum>_name` convention; the cpp profile shouldn't
// match the same line. Verifies that switching profiles changes
// both the extension filter (only .go files) and the pattern set.
func TestAuditEnumMirrorsLanguageProfileSelection(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "x.go"),
		[]byte("State\nvar _State_name = \"running\"\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "y.cpp"),
		[]byte("State\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := runAuditEnumMirrors([]string{"--enum", "State", "--language", "go", "--root", root}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("--language go must exit 0; got %d (stderr=%s)", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "x.go") {
		t.Fatalf("go profile should match x.go: %s", out)
	}
	if strings.Contains(out, "y.cpp") {
		t.Fatalf("go profile must not match .cpp file: %s", out)
	}
	if !strings.Contains(out, "_State_name") {
		t.Fatalf("go profile should fire the stringer pattern: %s", out)
	}
}

// TestAuditEnumMirrorsRejectsUnknownLanguage verifies the flag-
// validation surface: a typo'd --language exits 2 with a usage hint,
// not a silent fallthrough that yields zero patterns and confusing
// output.
func TestAuditEnumMirrorsRejectsUnknownLanguage(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runAuditEnumMirrors([]string{"--enum", "X", "--language", "klingon", "--root", t.TempDir()}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("unknown --language must exit 2; got %d", code)
	}
	if !strings.Contains(stderr.String(), "unknown --language") {
		t.Fatalf("stderr should name the bad flag: %s", stderr.String())
	}
}

// TestAuditEnumMirrorsReportsMissingSearchRoot verifies the
// diagnostic upgrade: pointing --root at a path that doesn't exist
// must say so explicitly. The original implementation walked the
// missing path silently and reported "no references found", which
// hid the real cause from operators.
func TestAuditEnumMirrorsReportsMissingSearchRoot(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runAuditEnumMirrors([]string{"--enum", "X", "--root", "/definitely-does-not-exist-xyz"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("missing root must exit 1; got %d", code)
	}
	if !strings.Contains(stderr.String(), "search root") {
		t.Fatalf("stderr should explain the missing root: %s", stderr.String())
	}
}

// TestAuditEnumMirrorsExtOverrideExcludesProfileExtensions verifies
// the directional contract on --ext: passing it doesn't just ADD
// extensions, it REPLACES the profile's set. Otherwise an operator
// who passes `--ext go` to scope the audit would still match every
// .cpp file in the tree, defeating the point of the override.
func TestAuditEnumMirrorsExtOverrideExcludesProfileExtensions(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "x.go"),
		[]byte("Symbol\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "y.cpp"),
		[]byte("Symbol\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := runAuditEnumMirrors([]string{"--enum", "Symbol", "--root", root, "--ext", "go"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("override audit must exit 0; got %d (stderr=%s)", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "x.go") {
		t.Fatalf("--ext go should match x.go: %s", out)
	}
	if strings.Contains(out, "y.cpp") {
		t.Fatalf("--ext go must EXCLUDE y.cpp (override is replacing, not additive): %s", out)
	}
}

// TestAuditEnumMirrorsAnyProfileWorksWithoutPatterns covers the
// "any" profile shape: a deliberately empty template set lets the
// operator run a pure file-list audit when they want to drive their
// own pattern decisions later. Asserts the three steps still print
// cleanly even with zero patterns in play.
func TestAuditEnumMirrorsAnyProfileWorksWithoutPatterns(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "src.go"),
		[]byte("MySymbol used here\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := runAuditEnumMirrors([]string{"--enum", "MySymbol", "--root", root, "--language", "any"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("any profile must exit 0; got %d (stderr=%s)", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{"Step 1", "Step 2", "Step 3", "src.go"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q: %s", want, out)
		}
	}
	// The patternless any profile means Step 2 should print the
	// "no high-signal mirror patterns" hint.
	if !strings.Contains(out, "no high-signal mirror patterns") {
		t.Fatalf("any profile with no patterns should print the no-patterns hint: %s", out)
	}
}

// TestAuditEnumMirrorsHelpExitsZero matches the conventional CLI
// behavior: --help should exit 0 (the user got what they asked
// for) rather than the flag-parse error code that flag.ErrHelp
// surfaces by default.
func TestAuditEnumMirrorsHelpExitsZero(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runAuditEnumMirrors([]string{"--help"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("--help must exit 0; got %d", code)
	}
}

// TestAuditEnumMirrorsHonorsCustomPattern verifies the extension
// seam called out in the design: a project can supply a pattern
// using {ENUM} as a placeholder and the audit splices the actual
// symbol name in before compiling. Without this, projects with
// non-C++ mirror conventions (e.g. Go's stringer-generated names)
// would have to fork the command.
func TestAuditEnumMirrorsHonorsCustomPattern(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "x.go"), []byte("var _StateName_0 = \"running\"\nState\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := runAuditEnumMirrors([]string{
		"--enum", "State",
		"--root", root,
		"--pattern", "_{ENUM}Name_",
		"--ext", "go",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("custom-pattern audit must exit 0; got %d (stderr=%s)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "_StateName_0") {
		t.Fatalf("custom pattern should match the stringer-style name: %s", stdout.String())
	}
}
