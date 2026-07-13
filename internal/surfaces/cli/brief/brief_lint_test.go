package brief

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"idiom/internal/evidence/telemetry"
	"idiom/internal/workflows/brieflint"
)

func briefPreflightTestDeps() string {

	if _, err := exec.LookPath("python3"); err != nil {
		ginkgo.Skip("python3 not available on PATH")
	}

	scriptPath, cleanup, err := brieflint.WriteEmbeddedPreflight()
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("materialize brief-preflight.py: %v", err))
	ginkgo.DeferCleanup(cleanup)

	return scriptPath
}

func runPreflight(scriptPath, briefPath, workspace string) (int, string) {

	cmd := exec.Command("python3", scriptPath, briefPath)

	cmd.Dir = workspace

	out, err := cmd.CombinedOutput()
	exit := 0

	if exitErr, ok := err.(*exec.ExitError); ok {
		exit = exitErr.ExitCode()
	} else if err != nil {
		ginkgo.Fail(fmt.Sprintf("preflight subprocess failed: %v (output=%s)", err, out))
	}

	return exit, string(out)
}

func makeWorkspace(files map[string]string) string {

	dir := ginkgo.GinkgoT().TempDir()
	gitDir := filepath.Join(dir, ".git")
	gomega.Expect(os.Mkdir(gitDir, 0o755)).To(gomega.Succeed())

	for rel, content := range files {
		full := filepath.Join(dir, rel)
		gomega.Expect(os.MkdirAll(filepath.Dir(full), 0o755)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(full, []byte(content), 0o644)).To(gomega.Succeed())

	}

	return dir
}

func writeLintBrief(dir, content string) string {

	briefPath := filepath.Join(dir, "brief.md")
	gomega.Expect(os.WriteFile(briefPath, []byte(content), 0o644)).To(gomega.Succeed())

	return briefPath
}

var _ = ginkgo.Describe("brief lint", func() {
	ginkgo.It("run lint plain emits preflight output", func() {
		if _, err := exec.LookPath("python3"); err != nil {
			ginkgo.Skip("python3 not available on PATH")
		}

		dir := ginkgo.GinkgoT().TempDir()
		brief := writeLintBrief(dir, "# Brief\n\nThis intentionally lacks the canonical brief sections.\n")

		var stdout, stderr bytes.Buffer
		exit := RunLint([]string{brief}, &stdout, &stderr, nil)
		gomega.Expect(exit).To(gomega.Equal(1), fmt.Sprintf("RunLint exit = %d, want lint failure; stdout=%s stderr=%s", exit, stdout.String(), stderr.String()))
		gomega.Expect(stdout.String()+stderr.String()).To(gomega.ContainSubstring("idiom brief preflight:"), fmt.Sprintf("expected preflight output, stdout=%q stderr=%q", stdout.String(), stderr.String()))

	})
	ginkgo.It("run lint plain reports python launch failure", func() {
		dir := ginkgo.GinkgoT().TempDir()
		brief := writeLintBrief(dir, "# Brief\n")
		ginkgo.GinkgoT().
			Setenv("PATH", "")

		var stdout, stderr bytes.Buffer
		exit := RunLint([]string{brief}, &stdout, &stderr, nil)
		gomega.Expect(exit).To(gomega.Equal(2), fmt.Sprintf("RunLint exit = %d, want launch failure; stdout=%s stderr=%s", exit, stdout.String(), stderr.String()))
		gomega.Expect(stdout.Len()).To(gomega.Equal(0), fmt.Sprintf("expected no stdout on launch failure, got %q", stdout.String()))
		gomega.Expect(stderr.String()).To(gomega.ContainSubstring("brief lint: failed to run python3:"), fmt.Sprintf("expected python launch failure, stderr=%q", stderr.String()))

	})
	ginkgo.It("run lint json flag emits envelope", func() {
		if _, err := exec.LookPath("python3"); err != nil {
			ginkgo.Skip("python3 not available on PATH")
		}

		root := ginkgo.GinkgoT().TempDir()
		briefPath := writeLintBrief(root, "# Title\n\n## Files To Modify\n\n| File | Change |\n|------|--------|\n| `x.go` | MODIFY |\n")

		var stdout, stderr bytes.Buffer

		_ = RunLint([]string{briefPath, "--json"}, &stdout, &stderr, nil)

		out := stdout.String()
		gomega.Expect(strings.HasPrefix(strings.TrimSpace(out), "{")).To(gomega.BeTrue(), fmt.Sprintf("expected JSON envelope on stdout, got: %s", out))

		var report brieflint.Report
		gomega.Expect(json.Unmarshal([]byte(out), &report)).To(gomega.Succeed())
		gomega.Expect(report.Brief).To(gomega.Equal(briefPath), fmt.Sprintf("expected brief field = %q, got %q", briefPath, report.Brief))

	})
	ginkgo.It("run lint plain telemetry records success and exit failure", func() {
		if _, err := exec.LookPath("python3"); err != nil {
			ginkgo.Skip("python3 not available on PATH")
		}
		ginkgo.GinkgoT().
			Setenv("IDIOM_TELEMETRY", "1")

		successBrief := writeLintBrief(ginkgo.GinkgoT().TempDir(), withCanonicalSections("# Brief\n\n"+
			"## Files to Modify (EXHAUSTIVE allowlist)\n\n"+
			"| File | Change |\n"+
			"|------|--------|\n"+
			"| `go.mod` | READ module declaration |\n\n"+
			"## Verified Anchors\n\n"+
			"Confirmed: `module idiom` is present.\n"))

		successRec, err := telemetry.Open(telemetry.Options{Dir: ginkgo.GinkgoT().TempDir(), Command: "brief"})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("open success telemetry: %v", err))

		var successOut, successErr bytes.Buffer
		if exit := RunLint([]string{successBrief}, &successOut, &successErr, successRec); exit != 0 {
			ginkgo.Fail(fmt.Sprintf("RunLint success exit = %d, stdout=%s stderr=%s", exit, successOut.String(), successErr.String()))
		}
		gomega.Expect(successRec.CloseWithFields(0, nil)).To(gomega.Succeed())

		var successLint map[string]any
		successData, err := os.ReadFile(successRec.Path())
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read success telemetry: %v", err))

		for _, line := range strings.Split(strings.TrimSpace(string(successData)), "\n") {
			var record map[string]any
			gomega.Expect(json.Unmarshal([]byte(line), &record)).To(gomega.Succeed())

			if record["type"] == "brief_lint" {
				successLint = record
			}
		}
		gomega.Expect(successLint["exit"]).To(gomega.Equal(float64(0)), fmt.Sprintf("success brief_lint telemetry exit = %#v in %#v", successLint["exit"], successLint))

		failingBrief := writeLintBrief(ginkgo.GinkgoT().TempDir(), "# Brief\n\nThis intentionally lacks the canonical brief sections.\n")
		failureRec, err := telemetry.Open(telemetry.Options{Dir: ginkgo.GinkgoT().TempDir(), Command: "brief"})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("open failure telemetry: %v", err))

		var failureOut, failureErr bytes.Buffer
		if exit := RunLint([]string{failingBrief}, &failureOut, &failureErr, failureRec); exit != 1 {
			ginkgo.Fail(fmt.Sprintf("RunLint failure exit = %d, stdout=%s stderr=%s", exit, failureOut.String(), failureErr.String()))
		}
		gomega.Expect(failureRec.CloseWithFields(1, nil)).To(gomega.Succeed())

		var failureLint map[string]any
		failureData, err := os.ReadFile(failureRec.Path())
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read failure telemetry: %v", err))

		for _, line := range strings.Split(strings.TrimSpace(string(failureData)), "\n") {
			var record map[string]any
			gomega.Expect(json.Unmarshal([]byte(line), &record)).To(gomega.Succeed())

			if record["type"] == "brief_lint" {
				failureLint = record
			}
		}
		gomega.Expect(failureLint["exit"]).To(gomega.Equal(float64(1)), fmt.Sprintf("failure brief_lint telemetry exit = %#v in %#v", failureLint["exit"], failureLint))

	})
	ginkgo.It("brief lint parses parenthetical heading", func() {

		script := briefPreflightTestDeps()
		ws := makeWorkspace(map[string]string{
			"internal/foo/bar.go": "package foo\nfunc Existing() {}\n",
		})
		brief := writeLintBrief(ws, withCanonicalSections("# Brief\n\n"+
			"## Files to Modify (EXHAUSTIVE allowlist)\n\n"+
			"| File | Change |\n"+
			"|------|--------|\n"+
			"| `internal/foo/bar.go` | ADD `func Added()` at line 2 |\n\n"+
			"## Verified Anchors\n\n"+
			"Confirmed: `Existing` is at line 2.\n"))
		exit, out := runPreflight(script, brief, ws)
		gomega.Expect(exit).To(gomega.Equal(0), fmt.Sprintf("expected lint OK on a well-formed brief, got exit=%d output=%s", exit, out))
		gomega.Expect(out).To(gomega.ContainSubstring("1 file-allowlist row(s)"), fmt.Sprintf("parser must recognize parenthetical heading and parse the row: %s", out))

	})
	ginkgo.It("brief lint parses scope files heading", func() {
		script := briefPreflightTestDeps()
		ws := makeWorkspace(map[string]string{
			"scripts/verify_atom.sh": "#!/usr/bin/env bash\n",
		})
		brief := writeLintBrief(ws, withCanonicalSections("# Brief\n\n"+
			"## Scope\n\n"+
			"### Files\n"+
			"| File | Change |\n"+
			"| --- | --- |\n"+
			"| `scripts/verify_atom.sh` | MODIFY trace audit at line 1 |\n\n"+
			"## Verified Current State\n\nLine 1 is `#!/usr/bin/env bash`.\n"))
		exit, out := runPreflight(script, brief, ws)
		gomega.Expect(exit).To(gomega.Equal(0), fmt.Sprintf("expected lint OK on Scope/Files allowlist, got exit=%d output=%s", exit, out))
		gomega.Expect(out).To(gomega.ContainSubstring("1 file-allowlist row(s)"), fmt.Sprintf("parser must recognize Scope/Files and parse the row: %s", out))

	})
	ginkgo.It("brief lint blocks scope files brief without anchored sections", func() {
		script := briefPreflightTestDeps()
		ws := makeWorkspace(map[string]string{
			"scripts/verify_atom.sh": "#!/usr/bin/env bash\n",
		})
		brief := writeLintBrief(ws, "# Brief\n\n"+
			"## Scope\n\n"+
			"### Files\n"+
			"| File | Change |\n"+
			"| --- | --- |\n"+
			"| `scripts/verify_atom.sh` | Add the active testing coverage atom trace audit |\n\n"+
			"## Changes\n\n"+
			"1. Add a small helper block in `scripts/verify_atom.sh`.\n\n"+
			"## Tests\n\n"+
			"- `bash -n scripts/verify_atom.sh`\n\n"+
			"## Acceptance\n\n"+
			"1. `bash -n scripts/verify_atom.sh`\n")
		exit, out := runPreflight(script, brief, ws)
		gomega.Expect(exit).To(gomega.Equal(1), fmt.Sprintf("expected lint FAIL on Scope/Files brief missing anchored sections, got exit=%d output=%s", exit, out))
		gomega.Expect(out).To(gomega.ContainSubstring("required section missing for anchored-edits mode"), fmt.Sprintf("stderr should report anchored structural failure: %s", out))
		gomega.Expect(out).NotTo(gomega.ContainSubstring("brief shape is unknown"), fmt.Sprintf("Scope/Files should be classified as anchored, not unknown: %s", out))

	})
	ginkgo.It("brief lint blocks unknown brief shape", func() {
		script := briefPreflightTestDeps()
		ws := makeWorkspace(nil)
		brief := writeLintBrief(ws, "# Brief\n\n"+
			"## Scope\n\n"+
			"This is not an implementation allowlist.\n\n"+
			"## Acceptance\n\n"+
			"- `go test ./...`\n")
		exit, out := runPreflight(script, brief, ws)
		gomega.Expect(exit).To(gomega.Equal(1), fmt.Sprintf("expected lint FAIL on unknown brief shape, got exit=%d output=%s", exit, out))
		gomega.Expect(out).To(gomega.ContainSubstring("brief shape is unknown"), fmt.Sprintf("stderr should report unknown brief shape: %s", out))

	})
	ginkgo.It("brief lint blocks prose anchor in allowlist", func() {
		script := briefPreflightTestDeps()
		ws := makeWorkspace(map[string]string{
			"internal/foo/bar.go": "package foo\nfunc Existing() {}\n",
		})
		brief := writeLintBrief(ws, "# Brief\n\n"+
			"## Files to Modify (EXHAUSTIVE allowlist)\n\n"+
			"| File | Change |\n"+
			"|------|--------|\n"+
			"| `internal/foo/bar.go` | ADD `func Added()` near top of file (line 2) |\n\n"+
			"## Current state\n\nLine 2 is `func Existing() {}`.\n")
		exit, out := runPreflight(script, brief, ws)
		gomega.Expect(exit).To(gomega.Equal(1), fmt.Sprintf("expected lint FAIL on prose anchor, got exit=%d output=%s", exit, out))
		gomega.Expect(out).To(gomega.ContainSubstring("prose anchor in allowlist row"), fmt.Sprintf("stderr should name the prose-anchor failure: %s", out))
		gomega.Expect(out).To(gomega.ContainSubstring("near top of file"), fmt.Sprintf("stderr should echo the offending phrase: %s", out))

	})
	ginkgo.It("brief lint warns on branching in required edit", func() {
		script := briefPreflightTestDeps()
		ws := makeWorkspace(map[string]string{
			"internal/foo/bar.go": "package foo\nfunc Existing() {}\n",
		})
		brief := writeLintBrief(ws, withCanonicalSections("# Brief\n\n"+
			"## Files to Modify (EXHAUSTIVE allowlist)\n\n"+
			"| File | Change |\n"+
			"|------|--------|\n"+
			"| `internal/foo/bar.go` | ADD `func Added()` at line 2 |\n\n"+
			"## Current state\n\nLine 2 is `func Existing() {}`.\n\n"+
			"## Required Edits\n\n"+
			"### Edit 1 — `bar.go` add Added\n\n"+
			"Either add the function alongside Existing or define it in a new file.\n"))
		exit, out := runPreflight(script, brief, ws)
		gomega.Expect(exit).To(gomega.Equal(0), fmt.Sprintf("branching should WARN, not FAIL, got exit=%d output=%s", exit, out))
		gomega.Expect(out).To(gomega.ContainSubstring("branching language in Required Edit"), fmt.Sprintf("stderr should carry the branching WARN: %s", out))

	})
	ginkgo.It("brief lint does not warn on multiple read first anchors", func() {
		script := briefPreflightTestDeps()
		ws := makeWorkspace(map[string]string{
			"internal/foo/bar.go": "package foo\n\nfunc Existing() {}\n\n\n\n\nfunc Other() {}\n",
		})
		brief := writeLintBrief(ws, "# Brief\n\n"+
			"## Files to Read First\n\n"+
			"- `internal/foo/bar.go` line 1 for package context.\n"+
			"- `internal/foo/bar.go` line 8 for nearby helper context.\n\n"+
			"## Files to Modify (EXHAUSTIVE allowlist)\n\n"+
			"| File | Change |\n"+
			"|------|--------|\n"+
			"| `internal/foo/bar.go` | ADD `func Added()` after the verified helper context |\n\n"+
			"## Verified Current State\n\n"+
			"`internal/foo/bar.go` line 1 is `package foo` and line 8 is `func Other() {}`.\n\n"+
			"## Required Edits\n\n"+
			"old_text:\n"+
			"```text\n"+
			"func Other() {}\n"+
			"```\n\n"+
			"new_text:\n"+
			"```text\n"+
			"func Other() {}\n\n"+
			"func Added() {}\n"+
			"```\n\n"+
			"## Acceptance\n\n"+
			"- `go build ./...` succeeds.\n")
		exit, out := runPreflight(script, brief, ws)
		gomega.Expect(exit).To(gomega.Equal(0), fmt.Sprintf("read-first anchors should not fail lint, got exit=%d output=%s", exit, out))
		gomega.Expect(out).NotTo(gomega.ContainSubstring("structure-describing anchors"), fmt.Sprintf("read-first/current-state line evidence should not trigger edit-contract warning: %s", out))

	})
	ginkgo.It("brief lint warns on multiple required edit anchors", func() {
		script := briefPreflightTestDeps()
		ws := makeWorkspace(map[string]string{
			"internal/foo/bar.go": "package foo\n\nfunc Existing() {}\n\n\n\n\nfunc Other() {}\n",
		})
		brief := writeLintBrief(ws, "# Brief\n\n"+
			"## Files to Read First\n\n"+
			"- `internal/foo/bar.go` line 1 for package context.\n\n"+
			"## Files to Modify (EXHAUSTIVE allowlist)\n\n"+
			"| File | Change |\n"+
			"|------|--------|\n"+
			"| `internal/foo/bar.go` | ADD `func Added()` after the verified helper context |\n\n"+
			"## Verified Current State\n\n"+
			"`internal/foo/bar.go` line 1 is `package foo`.\n\n"+
			"## Required Edits\n\n"+
			"In `internal/foo/bar.go`, combine line 1 and line 8 into one replacement window.\n\n"+
			"## Acceptance\n\n"+
			"- `go build ./...` succeeds.\n")
		exit, out := runPreflight(script, brief, ws)
		gomega.Expect(exit).To(gomega.Equal(0), fmt.Sprintf("structure anchor issue should WARN, not FAIL, got exit=%d output=%s", exit, out))
		gomega.Expect(out).To(gomega.ContainSubstring("structure-describing anchors"), fmt.Sprintf("required-edit line soup should still trigger the warning: %s", out))

	})
	ginkgo.It("brief lint blocks discovery mandate", func() {
		script := briefPreflightTestDeps()
		ws := makeWorkspace(map[string]string{
			"internal/foo/bar.go": "package foo\nfunc Existing() {}\n",
		})
		brief := writeLintBrief(ws, "# Brief\n\n"+
			"## Files to Modify (EXHAUSTIVE allowlist)\n\n"+
			"| File | Change |\n"+
			"|------|--------|\n"+
			"| `internal/foo/bar.go` | ADD `func Added()` at line 2 |\n\n"+
			"## Current state\n\nLine 2 is `func Existing() {}`.\n\n"+
			"## Discovery\n\nFirst, run idiom tool lookup and inline the output here.\n")
		exit, out := runPreflight(script, brief, ws)
		gomega.Expect(exit).To(gomega.Equal(1), fmt.Sprintf("expected lint FAIL on discovery mandate, got exit=%d output=%s", exit, out))
		gomega.Expect(out).To(gomega.ContainSubstring("discovery-mandate"), fmt.Sprintf("stderr should carry the discovery-mandate failure: %s", out))

	})
	ginkgo.It("brief lint anchor verification file exists", func() {
		script := briefPreflightTestDeps()
		ws := makeWorkspace(map[string]string{
			"internal/foo/bar.go": "package foo\nfunc Existing() {}\n",
		})
		brief := writeLintBrief(ws, withCanonicalSections("# Brief\n\n"+
			"## Files to Modify (EXHAUSTIVE allowlist)\n\n"+
			"| File | Change |\n"+
			"|------|--------|\n"+
			"| `internal/foo/bar.go` | ADD `func Added()` at line 2 |\n\n"+
			"## Current state\n\nLine 2 is `func Existing() {}`.\n"))
		exit, out := runPreflight(script, brief, ws)
		gomega.Expect(exit).To(gomega.Equal(0), fmt.Sprintf("expected OK when named file exists, got exit=%d output=%s", exit, out))
		gomega.Expect(out).NotTo(gomega.ContainSubstring("anchor verification skipped"), fmt.Sprintf("verification should run when the file exists: %s", out))

	})
	ginkgo.It("brief lint anchor verification file missing", func() {
		script := briefPreflightTestDeps()
		ws := makeWorkspace(map[string]string{
			"internal/foo/bar.go": "package foo\nfunc Existing() {}\n",
		})
		brief := writeLintBrief(ws, "# Brief\n\n"+
			"## Files to Modify (EXHAUSTIVE allowlist)\n\n"+
			"| File | Change |\n"+
			"|------|--------|\n"+
			"| `internal/foo/bar.go` | ADD `func Added()` at line 2 |\n"+
			"| `internal/foo/missing.go` | ADD `func Other()` at line 1 |\n\n"+
			"## Current state\n\nLine 2 is `func Existing() {}`.\n")
		exit, out := runPreflight(script, brief, ws)
		gomega.Expect(exit).To(gomega.Equal(1), fmt.Sprintf("expected FAIL when a named file is missing, got exit=%d output=%s", exit, out))
		gomega.Expect(out).To(gomega.ContainSubstring("file not found in workspace"), fmt.Sprintf("stderr should name the missing-file failure: %s", out))
		gomega.Expect(out).To(gomega.ContainSubstring("internal/foo/missing.go"), fmt.Sprintf("stderr should name the specific missing path: %s", out))

	})
	ginkgo.It("brief lint anchor verification skips explicit new file rows", func() {
		script := briefPreflightTestDeps()
		ws := makeWorkspace(map[string]string{
			"internal/foo/bar.go": "package foo\nfunc Existing() {}\n",
		})
		brief := writeLintBrief(ws, withCanonicalSections("# Brief\n\n"+
			"## Files to Modify (EXHAUSTIVE allowlist)\n\n"+
			"| File | Change |\n"+
			"|------|--------|\n"+
			"| `internal/foo/bar.go` | MODIFY `Existing` at line 2 |\n"+
			"| `scripts/new_guard.sh` | Create this new shell helper. |\n"+
			"| `.idiom/postflight/checks/new_guard.json` | New file target. Create the idiom project check JSON. |\n\n"+
			"## Current state\n\nLine 2 is `func Existing() {}`.\n"))
		exit, out := runPreflight(script, brief, ws)
		gomega.Expect(exit).To(gomega.Equal(0), fmt.Sprintf("expected OK with explicit new-file rows skipped, got exit=%d output=%s", exit, out))
		gomega.Expect(out).NotTo(gomega.ContainSubstring("file not found in workspace"), fmt.Sprintf("new-file rows should not be treated as stale paths: %s", out))

	})
	ginkgo.It("brief lint plan execute new source file requires inline body", func() {
		script := briefPreflightTestDeps()
		ws := makeWorkspace(map[string]string{
			"native/tests/test_existing.cpp": "#include \"doctest.h\"\nTEST_CASE(\"existing\") {}\n",
			"native/tests/run_tests.py":      "#!/usr/bin/env python3\n",
		})
		brief := writeLintBrief(ws, withCanonicalSections("# Brief\n\n"+
			"Routing: idiom plan-execute\n\n"+
			"## Files to Modify (EXHAUSTIVE allowlist)\n\n"+
			"| File | Change |\n"+
			"|------|--------|\n"+
			"| `native/tests/test_new_pairwise.cpp` | Create new file: focused pairwise test owner. |\n\n"+
			"## Current state\n\n"+
			"`native/tests/test_existing.cpp:L1-L2` defines the nearest existing suite.\n\n"+
			"## Required Behavior\n\n"+
			"Create `native/tests/test_new_pairwise.cpp` starting at line 1.\n\n"+
			"## Acceptance\n\n"+
			"- `python3 native/tests/run_tests.py -- -ts=NewPairwise`\n"))
		exit, out := runPreflight(script, brief, ws)
		gomega.Expect(exit).To(gomega.Equal(1), fmt.Sprintf("expected FAIL for plan-execute new source file without body, got exit=%d output=%s", exit, out))
		gomega.Expect(out).To(gomega.ContainSubstring("plan-execute new-file body missing"), fmt.Sprintf("stderr should name missing new-file body failure: %s", out))

	})
	ginkgo.It("brief lint plan execute new source file accepts inline body", func() {
		script := briefPreflightTestDeps()
		ws := makeWorkspace(map[string]string{
			"native/tests/test_existing.cpp": "#include \"doctest.h\"\nTEST_CASE(\"existing\") {}\n",
			"native/tests/run_tests.py":      "#!/usr/bin/env python3\n",
		})
		brief := writeLintBrief(ws, withCanonicalSections("# Brief\n\n"+
			"Routing: idiom plan-execute\n\n"+
			"## Files to Modify (EXHAUSTIVE allowlist)\n\n"+
			"| File | Change |\n"+
			"|------|--------|\n"+
			"| `native/tests/test_new_pairwise.cpp` | Create new file: focused pairwise test owner. |\n\n"+
			"## Current state\n\n"+
			"`native/tests/test_existing.cpp:L1-L2` defines the nearest existing suite.\n\n"+
			"## Required Behavior\n\n"+
			"Create `native/tests/test_new_pairwise.cpp` starting at line 1 with this body:\n\n"+
			"```text\n"+
			"#include \"doctest.h\"\n"+
			"\n"+
			"using namespace colony;\n"+
			"\n"+
			"namespace {\n"+
			"int helper() { return 1; }\n"+
			"}\n"+
			"\n"+
			"TEST_SUITE(\"NewPairwise\") {\n"+
			"TEST_CASE(\"covers new pairwise owner\") {\n"+
			"    CHECK(helper() == 1);\n"+
			"}\n"+
			"}\n"+
			"```\n\n"+
			"## Acceptance\n\n"+
			"- `python3 native/tests/run_tests.py -- -ts=NewPairwise`\n"))
		exit, out := runPreflight(script, brief, ws)
		gomega.Expect(exit).To(gomega.Equal(0), fmt.Sprintf("expected OK with inline new-file body, got exit=%d output=%s", exit, out))
		gomega.Expect(out).NotTo(gomega.ContainSubstring("plan-execute new-file body missing"), fmt.Sprintf("inline body should satisfy new-file plan-execute gate: %s", out))

	})
	ginkgo.It("brief lint plan execute new source file accepts replace all shape", func() {
		script := briefPreflightTestDeps()
		ws := makeWorkspace(map[string]string{
			"native/tests/test_existing.cpp": "#include \"doctest.h\"\nTEST_CASE(\"existing\") {}\n",
			"native/tests/run_tests.py":      "#!/usr/bin/env python3\n",
		})
		brief := writeLintBrief(ws, withCanonicalSections("# Brief\n\n"+
			"Routing: idiom plan-execute\n\n"+
			"## Files to Modify (EXHAUSTIVE allowlist)\n\n"+
			"| File | Change |\n"+
			"|------|--------|\n"+
			"| `native/tests/test_new_pairwise.cpp` | Create focused pairwise test owner. |\n\n"+
			"## Current state\n\n"+
			"`native/tests/test_existing.cpp:L1-L2` defines the nearest existing suite.\n\n"+
			"## Required Behavior\n\n"+
			"Use this explicit replacement shape for `native/tests/test_new_pairwise.cpp`:\n\n"+
			"```text\n"+
			"{\"path\":\"native/tests/test_new_pairwise.cpp\",\"replace_all\":true,\"old_text\":\"\",\"new_text\":\"#include \\\"doctest.h\\\"\\n\\nTEST_CASE(\\\"covers new pairwise owner\\\") {\\n    CHECK(1 == 1);\\n}\\n\",\"purpose\":\"Create the new focused pairwise test owner.\"}\n"+
			"```\n\n"+
			"## Acceptance\n\n"+
			"- `python3 native/tests/run_tests.py -- -ts=NewPairwise`\n"))
		exit, out := runPreflight(script, brief, ws)
		gomega.Expect(exit).To(gomega.Equal(0), fmt.Sprintf("expected OK with explicit replace_all shape, got exit=%d output=%s", exit, out))

	})
	ginkgo.It("brief lint implementation new source file allows implementation shape", func() {
		script := briefPreflightTestDeps()
		ws := makeWorkspace(map[string]string{
			"native/tests/test_existing.cpp": "#include \"doctest.h\"\nTEST_CASE(\"existing\") {}\n",
			"native/tests/run_tests.py":      "#!/usr/bin/env python3\n",
		})
		brief := writeLintBrief(ws, withCanonicalSections("# Brief\n\n"+
			"Routing: idiom implement\n\n"+
			"## Files to Modify (EXHAUSTIVE allowlist)\n\n"+
			"| File | Change |\n"+
			"|------|--------|\n"+
			"| `native/tests/test_new_pairwise.cpp` | Create new file: focused pairwise test owner. |\n\n"+
			"## Current state\n\n"+
			"`native/tests/test_existing.cpp:L1-L2` defines the nearest existing suite.\n\n"+
			"## Required Behavior\n\n"+
			"Create `native/tests/test_new_pairwise.cpp` with a doctest suite named NewPairwise.\n"+
			"This implementation-lane brief mentions `submit_plan` only to warn that the implementation route must not use it.\n\n"+
			"## Acceptance\n\n"+
			"- `python3 native/tests/run_tests.py -- -ts=NewPairwise`\n"))
		exit, out := runPreflight(script, brief, ws)
		gomega.Expect(exit).To(gomega.Equal(0), fmt.Sprintf("implementation lane should not require a full new-file body, got exit=%d output=%s", exit, out))

	})
	ginkgo.It("brief lint anchor verification old text matches", func() {
		script := briefPreflightTestDeps()
		ws := makeWorkspace(map[string]string{
			"internal/foo/bar.go": "package foo\n\nfunc Existing() int {\n\treturn 0\n}\n",
		})
		brief := writeLintBrief(ws, withCanonicalSections("# Brief\n\n"+
			"## Files to Modify (EXHAUSTIVE allowlist)\n\n"+
			"| File | Change |\n"+
			"|------|--------|\n"+
			"| `internal/foo/bar.go` | MODIFY `Existing` body at line 4 |\n\n"+
			"## Current state\n\nLine 4 returns 0.\n\n"+
			"## Required Edits\n\n"+
			"### Edit 1 — `bar.go` change return value\n\n"+
			"old_text:\n"+
			"```text\n"+
			"func Existing() int {\n"+
			"\treturn 0\n"+
			"}\n"+
			"```\n\n"+
			"new_text:\n"+
			"```text\n"+
			"func Existing() int {\n"+
			"\treturn 1\n"+
			"}\n"+
			"```\n"))
		exit, out := runPreflight(script, brief, ws)
		gomega.Expect(exit).To(gomega.Equal(0), fmt.Sprintf("expected OK when old_text matches file, got exit=%d output=%s", exit, out))
		gomega.Expect(out).NotTo(gomega.ContainSubstring("does not match"), fmt.Sprintf("verification should not report a mismatch: %s", out))

	})
	ginkgo.It("brief lint anchor verification old text stale", func() {
		script := briefPreflightTestDeps()
		ws := makeWorkspace(map[string]string{
			"internal/foo/bar.go": "package foo\n\nfunc Existing() int {\n\treturn 0\n}\n",
		})
		brief := writeLintBrief(ws, "# Brief\n\n"+
			"## Files to Modify (EXHAUSTIVE allowlist)\n\n"+
			"| File | Change |\n"+
			"|------|--------|\n"+
			"| `internal/foo/bar.go` | MODIFY `Existing` body at line 4 |\n\n"+
			"## Current state\n\nLine 4 returns 0.\n\n"+
			"## Required Edits\n\n"+
			"### Edit 1 — `bar.go` change return value\n\n"+
			"old_text:\n"+
			"```text\n"+
			"func Existing() int {\n"+
			"\treturn 42\n"+
			"}\n"+
			"```\n\n"+
			"new_text:\n"+
			"```text\n"+
			"func Existing() int {\n"+
			"\treturn 1\n"+
			"}\n"+
			"```\n")
		exit, out := runPreflight(script, brief, ws)
		gomega.Expect(exit).To(gomega.Equal(1), fmt.Sprintf("expected FAIL on stale old_text, got exit=%d output=%s", exit, out))
		gomega.Expect(out).To(gomega.ContainSubstring("does not match"), fmt.Sprintf("stderr should name the stale-anchor failure: %s", out))

	})
	ginkgo.It("brief lint blocks command backed mutation without edit shape", func() {
		script := briefPreflightTestDeps()
		ws := makeWorkspace(map[string]string{
			"docs/map.md":             "# old map\n",
			"scripts/generate_map.py": "print('new map')\n",
		})
		brief := writeLintBrief(ws, "# Brief\n\n"+
			"## Files to Modify (EXHAUSTIVE allowlist)\n\n"+
			"| File | Change |\n"+
			"|------|--------|\n"+
			"| `docs/map.md` | MODIFY generated map at line 1 |\n\n"+
			"## Verified Current State\n\n"+
			"- `docs/map.md:1`: `# old map`\n\n"+
			"## Required Behavior\n\n"+
			"1. Run `python3 scripts/generate_map.py` to regenerate `docs/map.md`.\n\n"+
			"## Acceptance\n\n"+
			"- `git diff --check`\n")
		exit, out := runPreflight(script, brief, ws)
		gomega.Expect(exit).To(gomega.Equal(1), fmt.Sprintf("expected FAIL for command-backed mutation without edit shape, got exit=%d output=%s", exit, out))
		gomega.Expect(out).To(gomega.ContainSubstring("mutating command"), fmt.Sprintf("stderr should explain command-backed mutation contract, got: %s", out))
		gomega.Expect(out).To(gomega.ContainSubstring("explicit edit shape"), fmt.Sprintf("stderr should explain command-backed mutation contract, got: %s", out))

	})
	ginkgo.It("brief lint allows domain function call with refresh wording", func() {
		script := briefPreflightTestDeps()
		ws := makeWorkspace(map[string]string{
			"tests/e2e/suites/stockpile_flow_suite.gd": "func _run_case() -> void:\n\tpass\n",
		})
		brief := writeLintBrief(ws, "# Brief\n\n"+
			"## Files To Modify\n\n"+
			"| File | Change |\n"+
			"|------|--------|\n"+
			"| `tests/e2e/suites/stockpile_flow_suite.gd` | MODIFY test case at line 1 |\n\n"+
			"## Verified Current State\n\n"+
			"- `tests/e2e/suites/stockpile_flow_suite.gd:1`: `func _run_case() -> void:`\n\n"+
			"## Required Behavior\n\n"+
			"1. Obtain the real stockpile panel, call `show_stockpile(stockpile_id)`, refresh panel data, and assert panel-visible fields reflect the mutated native state.\n\n"+
			"## Acceptance\n\n"+
			"- `git diff --check`\n")
		exit, out := runPreflight(script, brief, ws)
		gomega.Expect(exit).To(gomega.Equal(0), fmt.Sprintf("domain function calls with refresh wording should not be classified as shell-backed mutation, got exit=%d output=%s", exit, out))

	})
	ginkgo.It("brief lint blocks prose append without edit shape", func() {
		script := briefPreflightTestDeps()
		ws := makeWorkspace(map[string]string{
			"docs/tracker.md": "- [x] Atom 1 shipped.\n",
		})
		brief := writeLintBrief(ws, "# Brief\n\n"+
			"## Files to Modify (EXHAUSTIVE allowlist)\n\n"+
			"| File | Change |\n"+
			"|------|--------|\n"+
			"| `docs/tracker.md` | MODIFY tracker at line 1 |\n\n"+
			"## Verified Current State\n\n"+
			"- `docs/tracker.md:1`: `- [x] Atom 1 shipped.`\n\n"+
			"## Required Behavior\n\n"+
			"1. Append the Atom 2 entry after line 1 in `docs/tracker.md`.\n\n"+
			"## Acceptance\n\n"+
			"- `git diff --check`\n")
		exit, out := runPreflight(script, brief, ws)
		gomega.Expect(exit).To(gomega.Equal(1), fmt.Sprintf("expected FAIL for prose append without edit shape, got exit=%d output=%s", exit, out))
		gomega.Expect(out).To(gomega.ContainSubstring("prose-only mutation site"), fmt.Sprintf("stderr should explain prose-anchor mutation contract, got: %s", out))
		gomega.Expect(out).To(gomega.ContainSubstring("old_text/new_text"), fmt.Sprintf("stderr should explain prose-anchor mutation contract, got: %s", out))

	})
	ginkgo.It("brief lint anchor verification old text ambiguous fails", func() {
		script := briefPreflightTestDeps()
		ws := makeWorkspace(map[string]string{
			"internal/foo/bar.go": "package foo\n\nfunc helper() {}\nfunc helper() {}\n",
		})
		brief := writeLintBrief(ws, "# Brief\n\n"+
			"## Files to Modify (EXHAUSTIVE allowlist)\n\n"+
			"| File | Change |\n"+
			"|------|--------|\n"+
			"| `internal/foo/bar.go` | MODIFY first helper at line 3 |\n\n"+
			"## Current state\n\nLine 3 is `func helper() {}`.\n\n"+
			"## Required Edits\n\n"+
			"### Edit 1 — `bar.go` change first helper\n\n"+
			"old_text:\n"+
			"```text\n"+
			"func helper() {}\n"+
			"```\n\n"+
			"new_text:\n"+
			"```text\n"+
			"func helper() { return }\n"+
			"```\n\n"+
			"## Acceptance\n\n- `go test ./internal/foo` passes.\n")
		exit, out := runPreflight(script, brief, ws)
		gomega.Expect(exit).To(gomega.Equal(1), fmt.Sprintf("expected FAIL on ambiguous old_text, got exit=%d output=%s", exit, out))
		gomega.Expect(out).To(gomega.ContainSubstring("anchor not unique"), fmt.Sprintf("stderr should name the ambiguous-anchor failure: %s", out))

	})
	ginkgo.It("brief lint blocks foreign test marker for gd script", func() {
		script := briefPreflightTestDeps()
		ws := makeWorkspace(map[string]string{
			"ui/panel.gd": "extends Control\n\nfunc _ready() -> void:\n\tpass\n",
		})
		brief := writeLintBrief(ws, "# Brief\n\n"+
			"## Files to Modify (EXHAUSTIVE allowlist)\n\n"+
			"| File | Change |\n"+
			"|------|--------|\n"+
			"| `ui/panel.gd` | MODIFY `_ready` at line 3 |\n\n"+
			"## Current state\n\nLine 3 is `func _ready() -> void:`.\n\n"+
			"## Required Edits\n\n"+
			"### Edit 1 — `panel.gd` wire ready\n\n"+
			"Change `_ready` using the verified line anchor.\n\n"+
			"## Acceptance\n\n"+
			"- Add a doctest-style `TEST_CASE` for this GDScript behavior.\n")
		exit, out := runPreflight(script, brief, ws)
		gomega.Expect(exit).To(gomega.Equal(1), fmt.Sprintf("expected FAIL on GDScript/C++ test marker mismatch, got exit=%d output=%s", exit, out))
		gomega.Expect(out).To(gomega.ContainSubstring("language/profile mismatch"), fmt.Sprintf("stderr should report foreign test marker: %s", out))
		gomega.Expect(out).To(gomega.ContainSubstring("TEST_CASE"), fmt.Sprintf("stderr should report foreign test marker: %s", out))

	})
	ginkgo.It("brief lint blocks missing acceptance command path", func() {
		script := briefPreflightTestDeps()
		ws := makeWorkspace(map[string]string{
			"scripts/existing.sh": "#!/usr/bin/env bash\n",
		})
		brief := writeLintBrief(ws, "# Brief\n\n"+
			"## Files to Modify (EXHAUSTIVE allowlist)\n\n"+
			"| File | Change |\n"+
			"|------|--------|\n"+
			"| `scripts/existing.sh` | MODIFY line 1 |\n\n"+
			"## Current state\n\nLine 1 is `#!/usr/bin/env bash`.\n\n"+
			"## Required Edits\n\n"+
			"Apply the edit named in the Files to Modify row.\n\n"+
			"## Acceptance\n\n"+
			"- `bash scripts/missing.sh` passes.\n")
		exit, out := runPreflight(script, brief, ws)
		gomega.Expect(exit).To(gomega.Equal(1), fmt.Sprintf("expected FAIL on missing command path, got exit=%d output=%s", exit, out))
		gomega.Expect(out).To(gomega.ContainSubstring("acceptance command references missing path"), fmt.Sprintf("stderr should report missing command path: %s", out))

	})
	ginkgo.It("brief lint allows acceptance command for declared new file target", func() {
		script := briefPreflightTestDeps()
		ws := makeWorkspace(map[string]string{
			"scripts/run_scenario.sh": "#!/usr/bin/env bash\n",
		})
		brief := writeLintBrief(ws, withCanonicalSections("# Brief\n\n"+
			"Routing: idiom plan-execute\n\n"+
			"## Files to Modify (EXHAUSTIVE allowlist)\n\n"+
			"| File | Change |\n"+
			"|------|--------|\n"+
			"| `scripts/run_scenario.sh` | MODIFY validation at line 1 |\n"+
			"| `tests/scenarios/smoke/wild_wolf_seed.json` | New file target. Add a scenario fixture. |\n\n"+
			"## Verified Current State\n\n"+
			"- `scripts/run_scenario.sh:1`: `#!/usr/bin/env bash`\n\n"+
			"## Required Behavior\n\n"+
			"Use this explicit replacement shape for `tests/scenarios/smoke/wild_wolf_seed.json`:\n\n"+
			"```text\n"+
			"{\"path\":\"tests/scenarios/smoke/wild_wolf_seed.json\",\"replace_all\":true,\"old_text\":\"\",\"new_text\":\"{\\n  \\\"name\\\": \\\"wild_wolf_seed\\\",\\n  \\\"actions\\\": [\\n    {\\\"type\\\": \\\"debug_seed_animal\\\", \\\"species\\\": \\\"Wolf\\\"}\\n  ]\\n}\\n\",\"purpose\":\"Create the new scenario fixture.\"}\n"+
			"```\n\n"+
			"## Acceptance\n\n"+
			"- `bash scripts/run_scenario.sh tests/scenarios/smoke/wild_wolf_seed.json`\n"+
			"- `git diff --check -- scripts/run_scenario.sh tests/scenarios/smoke/wild_wolf_seed.json`\n"))
		exit, out := runPreflight(script, brief, ws)
		gomega.Expect(exit).To(gomega.Equal(0), fmt.Sprintf("expected OK when commands reference a declared new-file target, got exit=%d output=%s", exit, out))
		gomega.Expect(out).NotTo(gomega.ContainSubstring("acceptance command references missing path"), fmt.Sprintf("declared new-file target should satisfy command-path validation: %s", out))

	})
	ginkgo.It("brief lint allows acceptance command for declared new file package dir", func() {
		script := briefPreflightTestDeps()
		ws := makeWorkspace(map[string]string{})
		brief := writeLintBrief(ws, withCanonicalSections("# Brief\n\n"+
			"Routing: idiom plan-execute\n\n"+
			"## Files to Modify (EXHAUSTIVE allowlist)\n\n"+
			"| File | Change |\n"+
			"|------|--------|\n"+
			"| `internal/contracts/writerroute/decision.go` | NEW FILE: add writer route package. |\n\n"+
			"## Verified Current State\n\n"+
			"- `internal/contracts/writerroute/decision.go` is a new package file.\n\n"+
			"## Required Behavior\n\n"+
			"Create `internal/contracts/writerroute/decision.go` with an explicit replacement shape:\n\n"+
			"```text\n"+
			"{\"path\":\"internal/contracts/writerroute/decision.go\",\"replace_all\":true,\"old_text\":\"\",\"new_text\":\"package writerroute\\n\\nconst SchemaVersion = \\\"idiom.writer_route.v1\\\"\\n\",\"purpose\":\"Create the new writer route decision package.\"}\n"+
			"```\n\n"+
			"## Acceptance\n\n"+
			"- `go test ./internal/contracts/writerroute`\n"))
		exit, out := runPreflight(script, brief, ws)
		gomega.Expect(exit).To(gomega.Equal(0), fmt.Sprintf("expected OK when command references declared new-file package dir, got exit=%d output=%s", exit, out))
		gomega.Expect(out).NotTo(gomega.ContainSubstring("acceptance command references missing path"), fmt.Sprintf("new-file package dir should satisfy command-path validation: %s", out))

	})
	ginkgo.It("brief lint ignores absolute acceptance command path", func() {
		script := briefPreflightTestDeps()
		ws := makeWorkspace(map[string]string{
			"scripts/existing.sh": "#!/usr/bin/env bash\n",
		})
		brief := writeLintBrief(ws, "# Brief\n\n"+
			"## Files to Modify (EXHAUSTIVE allowlist)\n\n"+
			"| File | Change |\n"+
			"|------|--------|\n"+
			"| `scripts/existing.sh` | MODIFY line 1 |\n\n"+
			"## Current state\n\nLine 1 is `#!/usr/bin/env bash`.\n\n"+
			"## Required Edits\n\n"+
			"Apply the edit named in the Files to Modify row.\n\n"+
			"## Acceptance\n\n"+
			"- `bash /tmp/idiom-missing-helper.sh` remains an external harness command.\n")
		exit, out := runPreflight(script, brief, ws)
		gomega.Expect(exit).To(gomega.Equal(0), fmt.Sprintf("expected OK when command path is absolute/external, got exit=%d output=%s", exit, out))

	})
	ginkgo.It("brief lint anchor verification workspace skips cleanly", func() {
		script := briefPreflightTestDeps()

		ws := makeWorkspace(map[string]string{
			"unrelated.go": "package main\n",
		})
		brief := writeLintBrief(ws, withCanonicalSections("# Brief\n\n"+
			"## Files to Modify (EXHAUSTIVE allowlist)\n\n"+
			"| File | Change |\n"+
			"|------|--------|\n"+
			"| `native/src/foo.cpp` | ADD `void bar()` at line 2 |\n\n"+
			"## Current state\n\nLine 2 of native/src/foo.cpp.\n"))
		exit, out := runPreflight(script, brief, ws)
		gomega.Expect(exit).To(gomega.Equal(0), fmt.Sprintf("expected OK with workspace-skip WARN, got exit=%d output=%s", exit, out))
		gomega.Expect(out).To(gomega.ContainSubstring("anchor verification skipped"), fmt.Sprintf("stderr should carry the workspace-skip WARN: %s", out))

	})
	ginkgo.It("brief lint atom size warns around half max tokens", func() {

		script := briefPreflightTestDeps()
		ws := makeWorkspace(map[string]string{
			"cmd/idiom/lane.go": "package main\nfunc Existing() {}\n",
		})

		brief := writeLintBrief(ws, withCanonicalSections("# Brief\n\n"+
			"## Files to Modify (EXHAUSTIVE allowlist)\n\n"+
			"| File | Change |\n"+
			"|------|--------|\n"+
			"| `cmd/idiom/lane.go` | ADD `func added()` at line 2 |\n\n"+
			"## Current state\n\nLine 2 is `func Existing() {}`.\n\n"+
			buildInlinedContent(600)+"\n"))
		exit, out := runPreflight(script, brief, ws)
		gomega.Expect(exit).To(gomega.Equal(0), fmt.Sprintf("expected exit=0 (WARN only), got exit=%d output=%s", exit, out))
		gomega.Expect(out).To(gomega.ContainSubstring("atom-size estimate"), fmt.Sprintf("expected atom-size WARN in output: %s", out))
		gomega.Expect(out).To(gomega.ContainSubstring("Heaviest inlined blocks"), fmt.Sprintf("expected decomposition hint in WARN: %s", out))

	})
	ginkgo.It("brief lint atom size fails at eighty percent cap", func() {
		script := briefPreflightTestDeps()
		ws := makeWorkspace(map[string]string{
			"cmd/idiom/lane.go": "package main\nfunc Existing() {}\n",
		})

		brief := writeLintBrief(ws, "# Brief\n\n"+
			"## Files to Modify (EXHAUSTIVE allowlist)\n\n"+
			"| File | Change |\n"+
			"|------|--------|\n"+
			"| `cmd/idiom/lane.go` | ADD `func added()` at line 2 |\n\n"+
			"## Current state\n\nLine 2 is `func Existing() {}`.\n\n"+
			buildInlinedContent(1100)+"\n")
		exit, out := runPreflight(script, brief, ws)
		gomega.Expect(exit).To(gomega.Equal(1), fmt.Sprintf("expected exit=1 (FAIL), got exit=%d output=%s", exit, out))
		gomega.Expect(out).To(gomega.ContainSubstring("submit_plan will likely truncate"), fmt.Sprintf("expected truncation-warning in FAIL message: %s", out))

	})
	ginkgo.It("brief lint atom size respects env override", func() {
		script := briefPreflightTestDeps()
		ws := makeWorkspace(map[string]string{
			"cmd/idiom/lane.go": "package main\nfunc Existing() {}\n",
		})
		brief := writeLintBrief(ws, "# Brief\n\n"+
			"## Files to Modify (EXHAUSTIVE allowlist)\n\n"+
			"| File | Change |\n"+
			"|------|--------|\n"+
			"| `cmd/idiom/lane.go` | ADD `func added()` at line 2 |\n\n"+
			"## Current state\n\nLine 2 is `func Existing() {}`.\n\n"+
			buildInlinedContent(200)+"\n")

		cmd := exec.Command("python3", script, brief)

		cmd.Dir = ws

		cmd.Env = append(os.Environ(), "IDIOM_PLANNER_MAX_TOKENS=1024")

		out, err := cmd.CombinedOutput()
		exit := 0

		if exitErr, ok := err.(*exec.ExitError); ok {
			exit = exitErr.ExitCode()
		} else if err != nil {
			ginkgo.Fail(fmt.Sprintf("preflight subprocess failed: %v (output=%s)", err, out))
		}
		gomega.Expect(exit).To(gomega.Equal(1), fmt.Sprintf("expected FAIL under cap=1024, got exit=%d output=%s", exit, out))
		gomega.Expect(string(out)).To(gomega.ContainSubstring("max_tokens=1024"), fmt.Sprintf("expected env override to show in failure message: %s", out))

	})
})

func withCanonicalSections(body string) string {
	out := body
	if !strings.Contains(out, "## Required Edits") && !strings.Contains(out, "## Required Behavior") {
		out += "\n## Required Edits\n\nApply the edit named in the Files to Modify row.\n"
	}

	if !strings.Contains(out, "## Acceptance") {
		out += "\n## Acceptance\n\n- `go build ./...` succeeds.\n"
	}

	return out
}

func buildInlinedContent(lineCount int) string {
	var b strings.Builder

	b.WriteString("```text\n")

	for i := 0; i < lineCount; i++ {
		b.WriteString("    return 42 // synth line\n")
	}

	b.WriteString("```")

	return b.String()
}
