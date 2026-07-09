package refine

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"idiom/internal/contracts/pathparse"
	plancontract "idiom/internal/contracts/plan"
	"idiom/internal/workflows/atomintake"
	"idiom/internal/workflows/brieflint"
)

func TestMain(m *testing.M) {
	if os.Getenv("IDIOM_REFINE_HELPER_PROCESS") == "1" {
		fmt.Fprintln(os.Stdout, "stdout marker")
		fmt.Fprintln(os.Stderr, "stderr marker")
		exitCode, _ := strconv.Atoi(os.Getenv("IDIOM_REFINE_HELPER_EXIT"))
		os.Exit(exitCode)
	}

	os.Exit(m.Run())
}

// nonTrivialDraftBody returns a canned draft body sized to clear the
// refineDraftIsTrivial check (≥ refineMinDraftBytes, contains at
// least one ## heading). Used by stub-dispatcher tests so the new
// honest-exit-codes path (docs/refine-end-to-end-loop.md §2.1) sees
// these fixtures as valid drafts rather than silent-failure trivials.
func nonTrivialDraftBody(label string) string {
	return "# " + label + "\n\n## Problem\n\n" +
		"Synthetic test draft body — not a real brief. Padded above the\n" +
		"refineMinDraftBytes floor so honest-exit-codes treats it as a\n" +
		"valid draft. Real drafts are ~3-5KB; this stub matches the\n" +
		"shape (heading + Problem + anchors + edits + Acceptance) without trying\n" +
		"to be production-quality content.\n\n" +
		"## Files To Modify\n\n| File | Change |\n|------|--------|\n" +
		"| `foo.go` | MODIFY: stub |\n\n" +
		"## Verified Current State\n\n" +
		"- `foo.go:1`: synthetic anchor used only by refine tests.\n\n" +
		"## Required Behavior\n\n" +
		"1. Preserve the synthetic `foo.go:1` anchor while exercising refine control flow.\n\n" +
		"## Acceptance\n\n- run tests\n"
}

func TestRefinePhaseRoutingUsesPrimary35B(t *testing.T) {
	pool, model, maxTokens := refinePhaseRouting(filepath.Join("iter-1", "meta-draft.md"))

	if pool != "qwen-35b" || model != "qwen3.6-35b" {
		t.Fatalf("refine should share primary 35B backend, got pool=%q model=%q", pool, model)
	}

	if maxTokens != 16384 {
		t.Fatalf("draft max tokens = %d, want 16384", maxTokens)
	}

	_, _, patchTokens := refinePhaseRouting(filepath.Join("iter-2", "meta-patch.md"))
	if patchTokens != 16384 {
		t.Fatalf("patch max tokens = %d, want 16384", patchTokens)
	}

	_, _, reviewTokens := refinePhaseRouting(filepath.Join("iter-1", "meta-review.md"))
	if reviewTokens != 4096 {
		t.Fatalf("review max tokens = %d, want 4096", reviewTokens)
	}
}

func TestDefaultRefineDispatchRoutesDraftThroughArtifactCommand(t *testing.T) {
	meta := filepath.Join("root", ".idiom", "refine", "atom", "iter-1", "meta-draft.md")
	args := defaultRefineDispatchArgs("/bin/idiom", ".idiom/config.json", meta)
	joined := strings.Join(args, "\x00")

	for _, want := range []string{
		"queue", "run", "--pool", "qwen-35b", "--label", "refine-draft-artifact",
		"artifact", "--config", ".idiom/config.json", "--no-tools",
		"--mechanical-repair", "--model", "qwen3.6-35b", "--prompt-file", meta,
		"--output", filepath.Join("root", ".idiom", "refine", "atom", "iter-1", "draft.md"),
		"--json-out", filepath.Join("root", ".idiom", "refine", "atom", "iter-1", "draft.artifact.jsonl"),
		"--report-out", filepath.Join("root", ".idiom", "refine", "atom", "iter-1", "draft.report.json"),
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("draft dispatch args missing %q:\n%#v", want, args)
		}
	}

	if strings.Contains(joined, "lane\x00implement") {
		t.Fatalf("draft dispatch should not use implementation lane args:\n%#v", args)
	}
}

func TestDefaultRefineDispatchRoutesPatchThroughArtifactCommand(t *testing.T) {
	meta := filepath.Join("root", ".idiom", "refine", "atom", "iter-2", "meta-patch.md")
	args := defaultRefineDispatchArgs("/bin/idiom", ".idiom/config.json", meta)
	joined := strings.Join(args, "\x00")

	for _, want := range []string{
		"queue", "run", "--pool", "qwen-35b", "--label", "refine-patch-artifact",
		"artifact", "--config", ".idiom/config.json", "--no-tools",
		"--mechanical-repair", "--model", "qwen3.6-35b", "--prompt-file", meta,
		"--output", filepath.Join("root", ".idiom", "refine", "atom", "iter-2", "draft.md"),
		"--json-out", filepath.Join("root", ".idiom", "refine", "atom", "iter-2", "patch.artifact.jsonl"),
		"--report-out", filepath.Join("root", ".idiom", "refine", "atom", "iter-2", "patch.report.json"),
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("patch dispatch args missing %q:\n%#v", want, args)
		}
	}

	if strings.Contains(joined, "--no-thinking") {
		t.Fatalf("patch dispatch should preserve model thinking config for local Qwen artifact generation:\n%#v", args)
	}

	if strings.Contains(joined, "lane\x00implement") {
		t.Fatalf("patch dispatch should not depend on implementation-lane tool use, got:\n%#v", args)
	}
}

func TestDefaultDispatchWithConfigPreservesChildExitAndOutput(t *testing.T) {
	t.Setenv("IDIOM_REFINE_HELPER_PROCESS", "1")
	t.Setenv("IDIOM_REFINE_HELPER_EXIT", "7")

	var stdout, stderr bytes.Buffer
	code := DefaultDispatchWithConfig(filepath.Join("root", ".idiom", "refine", "atom", "iter-1", "meta-draft.md"), ".idiom/config.json", &stdout, &stderr)

	if code != 7 {
		t.Fatalf("DefaultDispatchWithConfig exit = %d, want 7; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "stdout marker") {
		t.Fatalf("stdout did not include child stdout: %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "stderr marker") {
		t.Fatalf("stderr did not include child stderr: %q", stderr.String())
	}
}

func TestDefaultDispatchWithConfigReturnsZeroForSuccessfulChild(t *testing.T) {
	t.Setenv("IDIOM_REFINE_HELPER_PROCESS", "1")
	t.Setenv("IDIOM_REFINE_HELPER_EXIT", "0")

	code := DefaultDispatchWithConfig(filepath.Join("iter-1", "meta-draft.md"), ".idiom/config.json", &bytes.Buffer{}, &bytes.Buffer{})

	if code != 0 {
		t.Fatalf("DefaultDispatchWithConfig successful child exit = %d, want 0", code)
	}
}

func TestDefaultDispatchWithConfigReturnsOneForOutputWriteFailure(t *testing.T) {
	t.Setenv("IDIOM_REFINE_HELPER_PROCESS", "1")
	t.Setenv("IDIOM_REFINE_HELPER_EXIT", "0")

	code := DefaultDispatchWithConfig(filepath.Join("iter-1", "meta-draft.md"), ".idiom/config.json", failingWriter{}, &bytes.Buffer{})

	if code != 1 {
		t.Fatalf("DefaultDispatchWithConfig output write failure exit = %d, want 1", code)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}

// stripFencedBlocksForStub removes ```...``` code blocks from
// meta-brief text so the stub's section-heading scanner doesn't
// match headings inside example/skeleton fences. Mirrors what the
// runtime brief-parser does (pathparse.ScopeToBriefBody + fence
// stripping); without this the new skeleton-first templates (which
// embed example "## Files To Modify" headings inside ```text fences)
// confuse the simple line-by-line scanner.
func stripFencedBlocksForStub(body string) string {
	var out strings.Builder

	inFence := false

	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence

			out.WriteString("\n")

			continue
		}

		if inFence {
			continue
		}

		out.WriteString(line)
		out.WriteString("\n")
	}

	return out.String()
}

// firstBacktickMDPath returns the first backtick-quoted token in
// `line` that ends in `.md`, or empty string if none. Scans span by
// span: lines like "- `path`: `<file.md>`" need to skip the first
// span (`path`) and return the second.
func firstBacktickMDPath(line string) string {
	rest := line
	for {
		start := strings.Index(rest, "`")
		if start < 0 {
			return ""
		}

		end := strings.Index(rest[start+1:], "`")
		if end < 0 {
			return ""
		}

		candidate := rest[start+1 : start+1+end]
		if strings.HasSuffix(candidate, ".md") {
			return candidate
		}

		rest = rest[start+1+end+1:]
	}
}

func stubDispatcher(t *testing.T, responses []string) (DispatchFunc, *int) {
	t.Helper()

	calls := 0

	return func(metaBriefPath string, stdout, stderr io.Writer) int {
		body, err := os.ReadFile(metaBriefPath)
		if err != nil {
			t.Fatalf("stub: read meta %s: %v", metaBriefPath, err)
		}

		// Find the output path the meta-brief targets. Resolution
		// order, most authoritative first:
		//   1. `Path: `<X.md>` under `## Artifact Output` or
		//      legacy `path`: `<X.md>` under `## STOP AND WRITE NOW`.
		//      The draft phase is now artifact-owned, but patch/review
		//      fixtures still use the legacy write-file prompt shape.
		//   2. Row inside `## Files To Modify` (legacy).
		//   3. Any other backtick-quoted `.md` outside the meta-brief
		//      itself (last-resort fallback).
		scanText := stripFencedBlocksForStub(string(body))

		var outPath, stopAndWritePath, filesToModifyPath, fallbackPath string

		inFilesToModify := false
		inStopAndWrite := false
		inArtifactOutput := false

		for _, line := range strings.Split(scanText, "\n") {
			trimmed := strings.TrimSpace(line)
			lower := strings.ToLower(trimmed)

			if strings.HasPrefix(lower, "## artifact output") {
				inArtifactOutput = true
				inFilesToModify = false
				inStopAndWrite = false

				continue
			}

			if strings.HasPrefix(lower, "## files to modify") {
				inFilesToModify = true
				inStopAndWrite = false
				inArtifactOutput = false

				continue
			}

			if strings.HasPrefix(lower, "## stop and write now") {
				inStopAndWrite = true
				inFilesToModify = false
				inArtifactOutput = false

				continue
			}

			if (inArtifactOutput || inFilesToModify || inStopAndWrite) && strings.HasPrefix(trimmed, "## ") {
				inArtifactOutput = false
				inFilesToModify = false
				inStopAndWrite = false
			}

			if !strings.Contains(trimmed, ".md") {
				continue
			}

			// Pick the FIRST backtick-quoted .md path on this line.
			// Some lines have multiple backtick spans (e.g. `path`:
			// `<file.md>`); scan span by span until we find one that
			// ends in .md.
			candidate := firstBacktickMDPath(trimmed)
			if candidate == "" {
				continue
			}

			if candidate == metaBriefPath || strings.HasSuffix(candidate, "/"+filepath.Base(metaBriefPath)) {
				continue
			}

			if (inArtifactOutput || inStopAndWrite) && stopAndWritePath == "" {
				stopAndWritePath = candidate
			}

			if inFilesToModify && filesToModifyPath == "" {
				filesToModifyPath = candidate
			}

			if fallbackPath == "" {
				fallbackPath = candidate
			}
		}

		switch {
		case stopAndWritePath != "":
			outPath = stopAndWritePath
		case filesToModifyPath != "":
			outPath = filesToModifyPath
		default:
			outPath = fallbackPath
		}

		if outPath == "" {
			t.Fatalf("stub: could not parse output path from meta %s", metaBriefPath)
		}

		// Resolve workspace-relative paths against the project root.
		// The refine templates now substitute relative paths so the
		// runner's RequiredEditPathsFromPrompt picks them up (#86
		// auto-salvage failure analysis); the stub mirrors the
		// runner's resolution to keep the test layout faithful.
		if !filepath.IsAbs(outPath) {
			outPath = filepath.Join(refineProjectRootFromIterDir(filepath.Dir(metaBriefPath)), outPath)
		}

		if calls >= len(responses) {
			t.Fatalf("stub: more dispatch calls than canned responses (%d)", calls)
		}

		if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
			t.Fatalf("stub: mkdir for %s: %v", outPath, err)
		}

		if err := os.WriteFile(outPath, []byte(responses[calls]), 0o644); err != nil {
			t.Fatalf("stub: write %s: %v", outPath, err)
		}

		calls++

		return 0
	}, &calls
}

func TestRefineWorkspaceRelConvertsAbsoluteToRelative(t *testing.T) {
	cases := []struct {
		root string
		path string
		want string
	}{
		{"/workspace/proj", "/workspace/proj/.idiom/refine/atom/iter-1/draft.md", ".idiom/refine/atom/iter-1/draft.md"},
		{"/workspace/proj", "/workspace/proj/cmd/idiom/lane.go", "cmd/idiom/lane.go"},
		{"/workspace/proj", "/workspace/proj/..data/scratch.md", "..data/scratch.md"},
		// Different ancestor — refuse to coerce, return input.
		{"/workspace/proj", "/elsewhere/file.md", "/elsewhere/file.md"},
		// Empty projectRoot — pass through.
		{"", "/some/path.md", "/some/path.md"},
		// Empty path — pass through.
		{"/x", "", ""},
	}
	for _, c := range cases {
		got := refineWorkspaceRel(c.root, c.path)
		if got != c.want {
			t.Errorf("refineWorkspaceRel(%q, %q) = %q, want %q", c.root, c.path, got, c.want)
		}
	}
}

func TestRefineProjectRootFromIterDirWalksUpFourLevels(t *testing.T) {
	cases := map[string]string{
		"/workspace/proj/.idiom/refine/my-atom/iter-1": "/workspace/proj",
		"/a/b/c/d/.idiom/refine/x/iter-7":              "/a/b/c/d",
	}
	for in, want := range cases {
		got := refineProjectRootFromIterDir(in)
		if got != want {
			t.Errorf("refineProjectRootFromIterDir(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRunLaneRefineDoesNotAcceptStaleDraftOutput(t *testing.T) {
	stubRefineLintPass(t)

	root := t.TempDir()
	specPath := filepath.Join(root, "spec.md")

	if err := os.WriteFile(specPath, []byte("Write docs/out.md.\n"), 0o644); err != nil {
		t.Fatalf("seed spec: %v", err)
	}

	opts := Options{
		SpecPath:    specPath,
		OutputPath:  filepath.Join(root, "brief.md"),
		Title:       "stale-output",
		MaxIters:    1,
		ProjectRoot: root,
	}
	staleDraft := filepath.Join(refineStateDir(root, opts.Title), "iter-1", "draft.md")

	if err := os.MkdirAll(filepath.Dir(staleDraft), 0o755); err != nil {
		t.Fatalf("mkdir stale draft: %v", err)
	}

	if err := os.WriteFile(staleDraft, []byte(nonTrivialDraftBody("stale")), 0o644); err != nil {
		t.Fatalf("seed stale draft: %v", err)
	}

	dispatch := func(metaBriefPath string, stdout, stderr io.Writer) int {
		return 0
	}

	var stdout, stderr bytes.Buffer

	code := Run(opts, dispatch, &stdout, &stderr)

	if code == 0 {
		t.Fatalf("expected stale draft to be rejected, stdout=%s stderr=%s", stdout.String(), stderr.String())
	}

	if _, err := os.Stat(staleDraft); !os.IsNotExist(err) {
		t.Fatalf("stale draft should have been removed and not accepted, stat err=%v", err)
	}

	if _, err := os.Stat(opts.OutputPath); !os.IsNotExist(err) {
		t.Fatalf("final output should not be written from stale draft, stat err=%v", err)
	}

	if !strings.Contains(stderr.String(), "cleared stale output") {
		t.Fatalf("expected stale-output cleanup log, got: %s", stderr.String())
	}

	metricsPath := filepath.Join(refineStateDir(root, opts.Title), "iter-1", "metrics.json")

	metricsBody, err := os.ReadFile(metricsPath)
	if err != nil {
		t.Fatalf("read metrics: %v", err)
	}

	var m refineIterMetrics

	if err := json.Unmarshal(metricsBody, &m); err != nil {
		t.Fatalf("unmarshal metrics: %v", err)
	}

	if m.DraftState != "missing" || m.Action != "failsoft-draft-missing" {
		t.Fatalf("metrics should record typed missing draft state, got %#v", m)
	}
}

func TestWriteRefineReviewMetaUsesWorkspaceRelativePaths(t *testing.T) {
	// End-to-end: write the meta brief and confirm the rendered
	// "Files To Modify" row has a workspace-relative path, not an
	// absolute one. This is what lets RequiredEditPathsFromPrompt
	// populate the runtime brief so auto-salvage (#86) can fire.
	root := t.TempDir()
	iterDir := filepath.Join(root, ".idiom", "refine", "atom", "iter-1")

	if err := os.MkdirAll(iterDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	metaPath := filepath.Join(iterDir, "meta-review.md")
	draftPath := filepath.Join(iterDir, "draft.md")
	reviewPath := filepath.Join(iterDir, "review.md")

	if err := os.WriteFile(draftPath, []byte("# Drafted brief\n\n## Problem\n\nReview me.\n"), 0o644); err != nil {
		t.Fatalf("write draft: %v", err)
	}

	if err := writeRefineReviewMeta(metaPath, draftPath, reviewPath, root, "(no findings)"); err != nil {
		t.Fatalf("writeRefineReviewMeta: %v", err)
	}

	body, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatalf("read meta: %v", err)
	}

	if strings.Contains(string(body), root+"/") {
		t.Fatalf("meta should not contain absolute project root path; body contains %q\n%s", root+"/", body)
	}

	if !strings.Contains(string(body), ".idiom/refine/atom/iter-1/review.md") {
		t.Fatalf("expected workspace-relative review path in meta body:\n%s", body)
	}

	if !strings.Contains(string(body), "## Pre-loaded drafted brief") ||
		!strings.Contains(string(body), "Path: `.idiom/refine/atom/iter-1/draft.md`") ||
		!strings.Contains(string(body), "> # Drafted brief") {

		t.Fatalf("expected review meta to inline drafted brief as artifact evidence:\n%s", body)
	}
}

func TestWriteRefineDraftMetaDoesNotLeakSpecRequiredEditPaths(t *testing.T) {
	root := t.TempDir()
	iterDir := filepath.Join(root, ".idiom", "refine", "atom", "iter-1")

	if err := os.MkdirAll(iterDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	metaPath := filepath.Join(iterDir, "meta-draft.md")
	draftPath := filepath.Join(iterDir, "draft.md")
	spec := `# Implementation atom

## Files To Modify

| File | Change |
| --- | --- |
| ` + "`src/real.cpp`" + ` | MODIFY: implementation target |

## Required Edits

Change the real implementation file.
`
	opts := Options{
		SpecPath:    filepath.Join(root, "spec.md"),
		OutputPath:  filepath.Join(root, "brief.md"),
		Title:       "atom",
		ProjectRoot: root,
	}

	var stderr bytes.Buffer

	if err := writeRefineDraftMeta(metaPath, draftPath, opts, spec, refineModeAnchored, &stderr); err != nil {
		t.Fatalf("writeRefineDraftMeta: %v", err)
	}

	body, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatalf("read meta: %v", err)
	}

	wantDraft := ".idiom/refine/atom/iter-1/draft.md"
	paths := plancontract.RequiredEditPathsFromPrompt(string(body))

	if len(paths) != 0 {
		t.Fatalf("artifact draft prompt should not expose implementation required edit paths: got %#v\nmeta:\n%s", paths, body)
	}

	if !strings.Contains(string(body), "## Artifact Output") || !strings.Contains(string(body), "`"+wantDraft+"`") {
		t.Fatalf("expected artifact output path %q in meta body:\n%s", wantDraft, body)
	}

	if !strings.Contains(string(body), "> ## Files To Modify") {
		t.Fatalf("embedded spec should be quoted so its headings are inert, got:\n%s", body)
	}

	if strings.Contains(string(body), "write_file") {
		t.Fatalf("artifact draft prompt must not ask for write_file, got:\n%s", body)
	}
}

func TestParseRefineVerdict(t *testing.T) {
	cases := []struct {
		body string
		want string
	}{
		{"## Verdict\n\nship-as-is\n", "ship-as-is"},
		{"## Verdict\nfix-bugs-then-ship\n", "fix-bugs-then-ship"},
		{"## Verdict\n\nreject-and-redraft.\n", "reject-and-redraft"},
		{"# No verdict here\n", "unknown"},
		{"## Verdict\n\nsomething-else\n", "unknown"},
	}
	for _, c := range cases {
		got := parseRefineVerdict(c.body)
		if got != c.want {
			t.Errorf("parseRefineVerdict(%q) = %q, want %q", c.body, got, c.want)
		}
	}
}

func TestParseRefineBugTitles(t *testing.T) {
	body := "## Bugs found\n\n1. **Off-by-one in loop**: blah blah\n2. **Missing import**: blah\n3. ** Whitespace title  **: blah\n\n## OK\n- thing\n"
	got := parseRefineBugTitles(body)
	want := []string{"missing import", "off-by-one in loop", "whitespace title"}

	if len(got) != len(want) {
		t.Fatalf("got %d titles, want %d: %v", len(got), len(want), got)
	}

	for i := range want {
		if got[i] != want[i] {
			t.Errorf("title[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestParseRefineBugTitlesSkipsNoFixNeededEntries(t *testing.T) {
	// Real review shape from task #68 iter-3 — the reviewer numbered
	// 6 entries under "Bugs found" but every body said "No fix needed"
	// or was a pure verification. Bug counter must return 0, not 6.
	body := `## Bugs found

1. **Missing fmt import**: ` + "`detectBriefProvenance`" + ` doesn't use fmt — only strings and os, both already imported. **No fix needed** — this is actually correct as written.
2. **File handle leak on early return**: defer f.Close() is correctly scoped because os.Open error returns before the defer is established. **No fix needed**.
3. **All anchors verified correct**: I verified every non-trivial anchor against the actual source files.
4. **Test imports sufficient**: lane_history_test.go already imports filepath and os. **No fix needed.**
5. **Same-package visibility**: detectBriefProvenance is in lane_history.go and called from lane.go; both are package main, accessible. **No fix needed.**
6. **No compile issues**: All proposed code is valid Go.

## Verdict

ship-as-is
`
	got := parseRefineBugTitles(body)

	if len(got) != 0 {
		t.Fatalf("all 6 entries are verifications (no fix needed); want 0 bugs, got %d: %v", len(got), got)
	}
}

func TestParseRefineBugTitlesKeepsRealBugsAlongsideVerifications(t *testing.T) {
	body := `## Bugs found

1. **Real bug A**: this is broken; fix it by X.
2. **Verification B**: I checked the function. **No fix needed**.
3. **Real bug C**: also broken, here's the fix.

## Verdict

fix-bugs-then-ship
`
	got := parseRefineBugTitles(body)
	want := []string{"real bug a", "real bug c"}

	if len(got) != len(want) {
		t.Fatalf("expected %d real bugs, got %d: %v", len(want), len(got), got)
	}

	for i := range want {
		if got[i] != want[i] {
			t.Errorf("bug[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestRefineLoopDivergingDetectsRepeatBugs(t *testing.T) {
	prev := []string{"bug a", "bug b"}
	current := []string{"bug a", "bug b"}

	if !refineLoopDiverging(prev, current) {
		t.Fatalf("identical bug sets should signal divergence")
	}

	current = []string{"bug a", "new bug"}
	if refineLoopDiverging(prev, current) {
		t.Fatalf("a new bug appearing should NOT signal divergence")
	}

	current = []string{"bug a"}
	if refineLoopDiverging(prev, current) {
		t.Fatalf("a smaller bug set is improvement, not divergence")
	}

	if refineLoopDiverging(prev, []string{}) {
		t.Fatalf("clean review (no bugs) is the success case, not divergence")
	}
}

func TestRefineDraftScorePrefersLintCleanOverBugCount(t *testing.T) {
	if !refineDraftScoreBetter(0, 5, 1, 1) {
		t.Fatalf("lint-clean draft should beat lint-failing draft even with more review bugs")
	}

	if refineDraftScoreBetter(1, 1, 0, 5) {
		t.Fatalf("lint-failing draft should not beat lint-clean draft")
	}

	if !refineDraftScoreBetter(0, 2, 0, 3) {
		t.Fatalf("with equal lint status, fewer bugs should win")
	}

	if refineDraftScoreBetter(0, 4, 0, 3) {
		t.Fatalf("with equal lint status, more bugs should not win")
	}
}

func TestRunLaneRefineConvergesOnShipAsIsVerdict(t *testing.T) {
	root := t.TempDir()
	specPath := filepath.Join(root, "spec.md")

	if err := os.WriteFile(specPath, []byte("Add a feature.\n"), 0o644); err != nil {
		t.Fatalf("seed spec: %v", err)
	}

	outPath := filepath.Join(root, "brief.md")
	opts := Options{
		SpecPath:    specPath,
		OutputPath:  outPath,
		Title:       "test-atom",
		MaxIters:    3,
		ProjectRoot: root,
	}
	dispatch, calls := stubDispatcher(t, []string{
		nonTrivialDraftBody("Drafted brief"),
		"# Review\n\n## Bugs found\n\n## OK\n\n## Verdict\n\nship-as-is\n",
	})

	var stderr bytes.Buffer

	code := Run(opts, dispatch, &bytes.Buffer{}, &stderr)

	if code != 0 {
		t.Fatalf("expected exit 0 on convergence, got %d; stderr=%s", code, stderr.String())
	}

	if *calls != 2 {
		t.Fatalf("expected 2 dispatches (draft + review), got %d", *calls)
	}

	body, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}

	if !strings.Contains(string(body), "# Drafted brief") {
		t.Fatalf("output should contain the drafted brief content, got: %s", body)
	}

	// Confirm #79 per-iter metrics landed on disk for the iter that
	// produced a ship-as-is verdict.
	metricsPath := filepath.Join(refineStateDir(root, "test-atom"), "iter-1", "metrics.json")

	metricsBody, err := os.ReadFile(metricsPath)
	if err != nil {
		t.Fatalf("read iter-1 metrics: %v", err)
	}

	var m refineIterMetrics

	if err := json.Unmarshal(metricsBody, &m); err != nil {
		t.Fatalf("unmarshal metrics: %v\nbody=%s", err, metricsBody)
	}

	if m.Iter != 1 || m.Phase != "draft" {
		t.Fatalf("metrics iter/phase = %d/%q, want 1/draft", m.Iter, m.Phase)
	}

	if m.Action != "ship-as-is" {
		t.Fatalf("metrics action = %q, want ship-as-is", m.Action)
	}

	if m.Verdict != "ship-as-is" {
		t.Fatalf("metrics verdict = %q, want ship-as-is", m.Verdict)
	}

	if m.StartedAt == "" || m.EndedAt == "" {
		t.Fatalf("metrics ts fields missing: started=%q ended=%q", m.StartedAt, m.EndedAt)
	}
}

func TestRunLaneRefineRefusesCompletedSpecStatus(t *testing.T) {
	root := t.TempDir()
	specPath := filepath.Join(root, "spec.md")
	spec := "# Done atom\n\nStatus: accepted\n\n## Scope\n\n- `foo.go`\n"

	if err := os.WriteFile(specPath, []byte(spec), 0o644); err != nil {
		t.Fatalf("seed spec: %v", err)
	}

	opts := Options{
		SpecPath:    specPath,
		OutputPath:  filepath.Join(root, "brief.md"),
		Title:       "completed-spec",
		MaxIters:    3,
		ProjectRoot: root,
	}
	calls := 0
	dispatch := func(_ string, _, _ io.Writer) int {
		calls++
		return 0
	}

	var stderr bytes.Buffer

	code := Run(opts, dispatch, &bytes.Buffer{}, &stderr)

	if code == 0 {
		t.Fatalf("expected completed spec to fail closed")
	}

	if calls != 0 {
		t.Fatalf("dispatcher should not run for completed specs, calls=%d", calls)
	}

	if !strings.Contains(stderr.String(), "refusing spec") || !strings.Contains(stderr.String(), "accepted") {
		t.Fatalf("expected refusal to name completed status, got: %s", stderr.String())
	}
}

func TestAtomSpecDispatchBlockerStatusParsing(t *testing.T) {
	blocked := []string{
		"Status: accepted",
		"Status: complete - accepted 2026-05-13",
		"**Status**: completed",
		"Status: source-satisfied - do not dispatch.",
		"Status: closed",
	}
	for _, spec := range blocked {
		if got := atomintake.SpecDispatchBlocker(spec); got == "" {
			t.Fatalf("expected status blocker for %q", spec)
		}
	}

	allowed := []string{
		"Status: planned",
		"Status: pending",
		"Status: active",
		"Status: in-progress",
		"Status: incomplete but dispatchable",
		"# No status here",
	}
	for _, spec := range allowed {
		if got := atomintake.SpecDispatchBlocker(spec); got != "" {
			t.Fatalf("unexpected status blocker for %q: %s", spec, got)
		}
	}
}

func TestRunLaneRefineWritesMetricsOnDraftDispatchFailure(t *testing.T) {
	root := t.TempDir()
	specPath := filepath.Join(root, "spec.md")

	if err := os.WriteFile(specPath, []byte("Add a feature.\n"), 0o644); err != nil {
		t.Fatalf("seed spec: %v", err)
	}

	outPath := filepath.Join(root, "brief.md")
	opts := Options{
		SpecPath:    specPath,
		OutputPath:  outPath,
		Title:       "fail-atom",
		MaxIters:    3,
		ProjectRoot: root,
	}
	dispatch := func(_ string, _, _ io.Writer) int { return 7 }

	var stderr bytes.Buffer

	code := Run(opts, dispatch, &bytes.Buffer{}, &stderr)

	if code != 7 {
		t.Fatalf("expected hard fail exit=7 (iter-1 dispatch failure with no fallback), got %d", code)
	}

	// Metrics file must still land even though dispatch failed —
	// downstream tools should be able to diagnose why iter-1 broke.
	metricsPath := filepath.Join(refineStateDir(root, "fail-atom"), "iter-1", "metrics.json")

	body, err := os.ReadFile(metricsPath)
	if err != nil {
		t.Fatalf("metrics should be written even on dispatch failure: %v", err)
	}

	var m refineIterMetrics

	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if m.DraftDispatchExit != 7 {
		t.Fatalf("DraftDispatchExit = %d, want 7", m.DraftDispatchExit)
	}

	if m.Action != "failsoft-draft-dispatch" {
		t.Fatalf("Action = %q, want failsoft-draft-dispatch", m.Action)
	}

	if m.FailsoftReason != "draft-dispatch" {
		t.Fatalf("FailsoftReason = %q", m.FailsoftReason)
	}
}

func TestRunLaneRefineShipsBestDraftOnDivergingLoop(t *testing.T) {
	// Behavior change 2026-05-10: divergence used to return exit 1
	// (no brief), now ships bestDraft (iter-1's reviewed version)
	// instead of throwing away the work.
	root := t.TempDir()
	specPath := filepath.Join(root, "spec.md")

	if err := os.WriteFile(specPath, []byte("Add a feature.\n"), 0o644); err != nil {
		t.Fatalf("seed spec: %v", err)
	}

	opts := Options{
		SpecPath:    specPath,
		OutputPath:  filepath.Join(root, "brief.md"),
		Title:       "test-atom",
		MaxIters:    3,
		ProjectRoot: root,
	}
	sameReview := "# Review\n\n## Bugs found\n\n1. **Persistent bug**: blah\n\n## Verdict\n\nfix-bugs-then-ship\n"
	dispatch, _ := stubDispatcher(t, []string{
		nonTrivialDraftBody("Draft v1 (best, will be shipped)"),
		sameReview,
		nonTrivialDraftBody("Draft v2 (patch failed to fix)"),
		sameReview,
	})

	var stderr bytes.Buffer

	code := Run(opts, dispatch, &bytes.Buffer{}, &stderr)
	// Colony Q4 (2026-05-11): save best draft AND exit non-zero on
	// non-ship-as-is convergence; orchestrator must inspect before
	// piping to plan-execute.
	if code == 0 {
		t.Fatalf("expected non-zero exit on diverging convergence; got 0. stderr=%s", stderr.String())
	}

	if !strings.Contains(stderr.String(), "loop diverging") {
		t.Fatalf("expected diverging-loop message in stderr, got: %s", stderr.String())
	}

	body, _ := os.ReadFile(opts.OutputPath)
	if !strings.HasPrefix(string(body), "> WARNING:") {
		t.Fatalf("expected saved best-draft to lead with WARNING blockquote header, got: %s", body)
	}

	if !strings.Contains(string(body), "# Draft v1 (best") {
		t.Fatalf("expected iter-1 draft to be saved on divergence, got: %s", body)
	}
}

func TestRunLaneRefineFailsoftShipsBestDraftWhenLaterReviewWedges(t *testing.T) {
	// Real-world scenario observed today: iter-1 review wrote
	// successfully (3 bugs), iter-2 patch ran, iter-2 review wedged
	// (didn't write file). Old behavior: refine returned exit 1
	// with no brief. New behavior: ship iter-1's reviewed draft.
	root := t.TempDir()
	specPath := filepath.Join(root, "spec.md")

	if err := os.WriteFile(specPath, []byte("Add a feature.\n"), 0o644); err != nil {
		t.Fatalf("seed spec: %v", err)
	}

	opts := Options{
		SpecPath:    specPath,
		OutputPath:  filepath.Join(root, "brief.md"),
		Title:       "test-atom",
		MaxIters:    3,
		ProjectRoot: root,
	}
	// Stub returns 4 responses: draft1, review1 (succeeds), patch2,
	// review2 wedges (we simulate by giving an empty response that
	// won't satisfy "Bugs found" — but the failsoft path triggers
	// when the review FILE is missing). To simulate review wedge
	// here we use a stub variant that returns non-zero on the 4th
	// dispatch.
	calls := 0
	dispatch := DispatchFunc(func(metaBriefPath string, stdout, stderr io.Writer) int {
		calls++

		body, err := os.ReadFile(metaBriefPath)
		if err != nil {
			t.Fatalf("read meta: %v", err)
		}

		// Find the output path the meta refers to using the same
		// stop-and-write-first / files-to-modify / fallback chain
		// stubDispatcher uses. Kept inline (not extracted into a
		// helper) because this test exercises a 4th-call wedge
		// scenario stubDispatcher doesn't support.
		scanText := stripFencedBlocksForStub(string(body))

		var outPath, stopAndWritePath, filesToModifyPath, fallbackPath string

		inFilesToModify := false
		inStopAndWrite := false
		inArtifactOutput := false

		for _, line := range strings.Split(scanText, "\n") {
			trimmed := strings.TrimSpace(line)
			lower := strings.ToLower(trimmed)

			if strings.HasPrefix(lower, "## artifact output") {
				inArtifactOutput = true
				inFilesToModify = false
				inStopAndWrite = false

				continue
			}

			if strings.HasPrefix(lower, "## files to modify") {
				inFilesToModify = true
				inStopAndWrite = false
				inArtifactOutput = false

				continue
			}

			if strings.HasPrefix(lower, "## stop and write now") {
				inStopAndWrite = true
				inFilesToModify = false
				inArtifactOutput = false

				continue
			}

			if (inArtifactOutput || inFilesToModify || inStopAndWrite) && strings.HasPrefix(trimmed, "## ") {
				inArtifactOutput = false
				inFilesToModify = false
				inStopAndWrite = false
			}

			if !strings.Contains(trimmed, ".md") {
				continue
			}

			candidate := firstBacktickMDPath(trimmed)
			if candidate == "" || candidate == metaBriefPath || strings.HasSuffix(candidate, "/"+filepath.Base(metaBriefPath)) {
				continue
			}

			if (inArtifactOutput || inStopAndWrite) && stopAndWritePath == "" {
				stopAndWritePath = candidate
			}

			if inFilesToModify && filesToModifyPath == "" {
				filesToModifyPath = candidate
			}

			if fallbackPath == "" {
				fallbackPath = candidate
			}
		}

		switch {
		case stopAndWritePath != "":
			outPath = stopAndWritePath
		case filesToModifyPath != "":
			outPath = filesToModifyPath
		default:
			outPath = fallbackPath
		}

		// Resolve workspace-relative paths against the project root
		// — meta-brief paths are now relative (see stubDispatcher
		// rationale).
		if !filepath.IsAbs(outPath) {
			outPath = filepath.Join(refineProjectRootFromIterDir(filepath.Dir(metaBriefPath)), outPath)
		}

		// Simulate iter-2 review wedge: don't write the output file
		// and return non-zero exit (matches real wedge shape).
		if calls == 4 {
			return 1
		}

		responses := []string{
			nonTrivialDraftBody("Draft v1 (will be shipped via failsoft)"),
			"# Review\n\n## Bugs found\n\n1. **Bug A**: x\n2. **Bug B**: y\n3. **Bug C**: z\n\n## Verdict\n\nfix-bugs-then-ship\n",
			nonTrivialDraftBody("Draft v2"),
		}
		if calls > len(responses) {
			t.Fatalf("more calls than responses: %d", calls)
		}

		_ = os.MkdirAll(filepath.Dir(outPath), 0o755)
		_ = os.WriteFile(outPath, []byte(responses[calls-1]), 0o644)

		return 0
	})

	var stderr bytes.Buffer

	code := Run(opts, dispatch, &bytes.Buffer{}, &stderr)
	// Failsoft saves the best draft AND exits non-zero — orchestrator
	// inspects before piping (Colony Q4, 2026-05-11).
	if code == 0 {
		t.Fatalf("expected non-zero exit on iter-2 review wedge failsoft; got 0. stderr=%s", stderr.String())
	}

	if !strings.Contains(stderr.String(), "failsoft at iter 2") {
		t.Fatalf("expected failsoft message in stderr, got: %s", stderr.String())
	}

	body, _ := os.ReadFile(opts.OutputPath)
	if !strings.HasPrefix(string(body), "> WARNING:") {
		t.Fatalf("expected saved failsoft draft to lead with WARNING blockquote, got: %s", body)
	}

	if !strings.Contains(string(body), "# Draft v1 (will be shipped via failsoft)") {
		t.Fatalf("expected iter-1 draft saved via failsoft, got: %s", body)
	}
}

func TestRunLaneRefineFailsHardWhenIter1HasNoReviewedDraft(t *testing.T) {
	// When even iter-1 fails before producing a reviewed draft,
	// failsoft has nothing to fall back on — must return non-zero.
	root := t.TempDir()
	specPath := filepath.Join(root, "spec.md")

	if err := os.WriteFile(specPath, []byte("Add a feature.\n"), 0o644); err != nil {
		t.Fatalf("seed spec: %v", err)
	}

	opts := Options{
		SpecPath:    specPath,
		OutputPath:  filepath.Join(root, "brief.md"),
		Title:       "test-atom",
		MaxIters:    3,
		ProjectRoot: root,
	}
	// Iter-1 draft dispatch fails immediately.
	dispatch := DispatchFunc(func(metaBriefPath string, stdout, stderr io.Writer) int {
		return 2
	})

	var stderr bytes.Buffer

	code := Run(opts, dispatch, &bytes.Buffer{}, &stderr)

	if code == 0 {
		t.Fatalf("expected non-zero exit when iter-1 fails with no fallback, got 0")
	}

	if !strings.Contains(stderr.String(), "no previously-reviewed draft to fall back on") {
		t.Fatalf("expected no-fallback message in stderr, got: %s", stderr.String())
	}
}

func TestRunLaneRefineExhaustsMaxItersAndShipsLatest(t *testing.T) {
	root := t.TempDir()
	specPath := filepath.Join(root, "spec.md")

	if err := os.WriteFile(specPath, []byte("Add a feature.\n"), 0o644); err != nil {
		t.Fatalf("seed spec: %v", err)
	}

	opts := Options{
		SpecPath:    specPath,
		OutputPath:  filepath.Join(root, "brief.md"),
		Title:       "test-atom",
		MaxIters:    2,
		ProjectRoot: root,
	}
	// Iter 1: 2 bugs. Iter 2: 1 bug (strict improvement, so
	// no-improvement stop does NOT fire, and iter 2 IS best).
	// Loop exhausts max-iters and ships iter 2's draft (best).
	dispatch, _ := stubDispatcher(t, []string{
		nonTrivialDraftBody("Draft v1"),
		"# Review\n\n## Bugs found\n\n1. **First bug**: blah\n2. **Second bug**: blah\n\n## Verdict\n\nfix-bugs-then-ship\n",
		nonTrivialDraftBody("Draft v2 (final, fewer bugs)"),
		"# Review\n\n## Bugs found\n\n1. **Different bug**: blah\n\n## Verdict\n\nfix-bugs-then-ship\n",
	})

	var stderr bytes.Buffer

	code := Run(opts, dispatch, &bytes.Buffer{}, &stderr)
	// max-iters-exhausted is non-zero (Colony Q4): unresolved bugs
	// remained, orchestrator must inspect.
	if code == 0 {
		t.Fatalf("expected non-zero exit on max-iters exhaust; got 0. stderr=%s", stderr.String())
	}

	body, _ := os.ReadFile(opts.OutputPath)
	if !strings.HasPrefix(string(body), "> WARNING:") {
		t.Fatalf("expected saved best-draft to lead with WARNING, got: %s", body)
	}

	if !strings.Contains(string(body), "# Draft v2 (final, fewer bugs)") {
		t.Fatalf("output should be the best draft (v2, 1 bug), got: %s", body)
	}

	if !strings.Contains(stderr.String(), "max iters") {
		t.Fatalf("expected max-iters notice in stderr, got: %s", stderr.String())
	}
}

func TestRunLaneRefineContinuesThroughChangedBugSetUntilMaxIters(t *testing.T) {
	root := t.TempDir()
	specPath := filepath.Join(root, "spec.md")

	if err := os.WriteFile(specPath, []byte("Add a feature.\n"), 0o644); err != nil {
		t.Fatalf("seed spec: %v", err)
	}

	opts := Options{
		SpecPath:    specPath,
		OutputPath:  filepath.Join(root, "brief.md"),
		Title:       "no-improvement",
		MaxIters:    3,
		ProjectRoot: root,
	}
	// Iter 1 has 2 bugs; iter 2 has 2 DIFFERENT bugs (so divergence
	// detector doesn't fire — divergence is for IDENTICAL sets).
	// The loop should keep going to max-iters because changed bug sets
	// can mean the reviewer exposed deeper issues after a patch.
	dispatch, _ := stubDispatcher(t, []string{
		nonTrivialDraftBody("Draft v1"),
		"# Review\n\n## Bugs found\n\n1. **Bug A**: x\n2. **Bug B**: y\n\n## Verdict\n\nfix-bugs-then-ship\n",
		nonTrivialDraftBody("Draft v2"),
		"# Review\n\n## Bugs found\n\n1. **Bug C**: z\n2. **Bug D**: w\n\n## Verdict\n\nfix-bugs-then-ship\n",
		nonTrivialDraftBody("Draft v3"),
		"# Review\n\n## Bugs found\n\n1. **Bug E**: z\n\n## Verdict\n\nfix-bugs-then-ship\n",
	})

	var stderr bytes.Buffer

	code := Run(opts, dispatch, &bytes.Buffer{}, &stderr)
	// max-iters saves best AND exits non-zero — orchestrator inspects.
	if code == 0 {
		t.Fatalf("expected non-zero exit on max-iters convergence; got 0. stderr=%s", stderr.String())
	}

	if !strings.Contains(stderr.String(), "max iters") {
		t.Fatalf("expected max-iters message in stderr, got: %s", stderr.String())
	}

	body, _ := os.ReadFile(opts.OutputPath)
	if !strings.HasPrefix(string(body), "> WARNING:") {
		t.Fatalf("expected saved best-draft to lead with WARNING, got: %s", body)
	}

	if !strings.Contains(string(body), "# Draft v3") {
		t.Fatalf("expected best draft to be latest improved candidate, got: %s", body)
	}
}

func TestRunLaneRefineFinalizesBestDraftNotLatest(t *testing.T) {
	root := t.TempDir()
	specPath := filepath.Join(root, "spec.md")

	if err := os.WriteFile(specPath, []byte("Add a feature.\n"), 0o644); err != nil {
		t.Fatalf("seed spec: %v", err)
	}

	opts := Options{
		SpecPath:    specPath,
		OutputPath:  filepath.Join(root, "brief.md"),
		Title:       "best-draft",
		MaxIters:    2,
		ProjectRoot: root,
	}
	// Iter 1: 1 bug. Iter 2: 5 bugs (regressed!). Loop should ship
	// iter 1's draft, NOT iter 2's, because iter 1 had fewer bugs.
	dispatch, _ := stubDispatcher(t, []string{
		nonTrivialDraftBody("Draft v1 (BEST — 1 bug)"),
		"# Review\n\n## Bugs found\n\n1. **Bug A**: x\n\n## Verdict\n\nfix-bugs-then-ship\n",
		nonTrivialDraftBody("Draft v2 (REGRESSED — 5 bugs)"),
		"# Review\n\n## Bugs found\n\n1. **Bug A**: x\n2. **Bug B**: y\n3. **Bug C**: z\n4. **Bug D**: w\n5. **Bug E**: v\n\n## Verdict\n\nfix-bugs-then-ship\n",
	})

	var stderr bytes.Buffer

	code := Run(opts, dispatch, &bytes.Buffer{}, &stderr)
	// max-iters with regressed final → non-zero exit (Colony Q4).
	if code == 0 {
		t.Fatalf("expected non-zero exit on max-iters with no ship-as-is; got 0. stderr=%s", stderr.String())
	}

	body, err := os.ReadFile(opts.OutputPath)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}

	if !strings.HasPrefix(string(body), "> WARNING:") {
		t.Fatalf("expected saved best-draft to lead with WARNING, got: %s", body)
	}

	if !strings.Contains(string(body), "# Draft v1 (BEST") {
		t.Fatalf("output should be the best draft (v1), not the regressed latest (v2). Got: %s", body)
	}
}

// TestClassifyBriefAtomShapeEmitsCanonicalTags exercises §2.4's
// 4-tag canonical taxonomy (test-code, multi-file,
// markdown-deliverable, generic). Project-specific tags (save-load,
// c++-system, etc.) are deliberately NOT in the canonical set per
// Colony Q3 (2026-05-11) — those live in project overlay traps'
// applies_to fields.
func TestClassifyBriefAtomShapeEmitsCanonicalTags(t *testing.T) {
	cases := []struct {
		name string
		body string
		want []string
	}{
		{
			name: "single-source-file-generic",
			body: "## Files To Modify\n\n| File | Change |\n|------|--------|\n| `cmd/idiom/foo.go` | MODIFY |\n",
			want: []string{refineTagGeneric},
		},
		{
			name: "test-file",
			body: "## Files To Modify\n\n| File | Change |\n|------|--------|\n| `internal/runtime/agent/runner_test.go` | ADD test |\n",
			want: []string{refineTagTestCode},
		},
		{
			name: "multi-file-different-dirs",
			body: "## Files To Modify\n\n| File | Change |\n|------|--------|\n| `cmd/idiom/foo.go` | MODIFY |\n| `internal/foo/bar.go` | MODIFY |\n",
			want: []string{refineTagMultiFile},
		},
		{
			name: "test-and-multi-file",
			body: "## Files To Modify\n\n| File | Change |\n|------|--------|\n| `cmd/idiom/foo.go` | MODIFY |\n| `internal/foo/bar_test.go` | ADD test |\n",
			want: []string{refineTagTestCode, refineTagMultiFile},
		},
		{
			name: "markdown-only",
			body: "## Files To Modify\n\n| File | Change |\n|------|--------|\n| `.planning/inventory.md` | NEW FILE |\n",
			want: []string{refineTagMarkdownDeliverable},
		},
		{
			name: "mixed-markdown-and-source-is-not-markdown-deliverable",
			body: "## Files To Modify\n\n| File | Change |\n|------|--------|\n| `README.md` | MODIFY |\n| `cmd/foo.go` | ADD |\n",
			want: []string{refineTagMultiFile},
		},
		{
			name: "empty-table-falls-back-to-generic",
			body: "## Files To Modify\n\n| File | Change |\n|------|--------|\n",
			want: []string{refineTagGeneric},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyBriefAtomShape([]byte(tc.body))
			if !slices.Equal(got, tc.want) {
				t.Fatalf("classifyBriefAtomShape: got %v, want %v", got, tc.want)
			}
		})
	}
}

// TestParseTrapFrontmatterExtractsAppliesTo exercises the narrow
// frontmatter parser. Tolerant of missing frontmatter; strict about
// the applies_to: [a, b, c] line shape.
func TestParseTrapFrontmatterExtractsAppliesTo(t *testing.T) {
	cases := []struct {
		name        string
		raw         string
		wantApplies []string
		wantBody    string
	}{
		{
			name:        "basic",
			raw:         "---\napplies_to: [test-code, multi-file]\n---\n\n# trap body\n",
			wantApplies: []string{"test-code", "multi-file"},
			wantBody:    "# trap body",
		},
		{
			name:        "no-frontmatter",
			raw:         "# trap body only\n",
			wantApplies: nil,
			wantBody:    "# trap body only\n",
		},
		{
			name:        "single-tag",
			raw:         "---\napplies_to: [generic]\n---\n# body\n",
			wantApplies: []string{"generic"},
			wantBody:    "# body",
		},
		{
			name:        "quoted-tags",
			raw:         "---\napplies_to: [\"test-code\", 'multi-file']\n---\nbody\n",
			wantApplies: []string{"test-code", "multi-file"},
			wantBody:    "body",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			applies, body := parseTrapFrontmatter([]byte(tc.raw))
			if !slices.Equal(applies, tc.wantApplies) {
				t.Fatalf("applies_to: got %v, want %v", applies, tc.wantApplies)
			}

			gotBody := strings.TrimSpace(string(body))
			wantBody := strings.TrimSpace(tc.wantBody)

			if gotBody != wantBody {
				t.Fatalf("body: got %q, want %q", gotBody, wantBody)
			}
		})
	}
}

// TestRunLaneRefineAppliesProjectOverlay verifies the §2.4 end-to-
// end flow: project drops hard-prohibitions.md + matching traps,
// refine appends them to the final brief on ship-as-is. Bare repos
// (no overlay dir) see zero behavior change.
func TestRunLaneRefineAppliesProjectOverlay(t *testing.T) {
	stubRefineLintPass(t)

	root := t.TempDir()
	overlayDir := filepath.Join(root, refineOverlayDir)
	trapsDir := filepath.Join(overlayDir, "traps")

	if err := os.MkdirAll(trapsDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	hardProhibitions := "## HARD PROHIBITIONS (project)\n\n- No interactive question tool — use `<<QUESTION>>:` markers.\n- No builds, tests, or commits.\n"
	if err := os.WriteFile(filepath.Join(overlayDir, "hard-prohibitions.md"), []byte(hardProhibitions), 0o644); err != nil {
		t.Fatalf("seed prohibitions: %v", err)
	}

	testTrap := "---\napplies_to: [test-code]\n---\n\n## Trap — test-code\n\nWatch for goroutine leaks in t.Cleanup.\n"
	if err := os.WriteFile(filepath.Join(trapsDir, "test-code.md"), []byte(testTrap), 0o644); err != nil {
		t.Fatalf("seed trap: %v", err)
	}

	multiFileTrap := "---\napplies_to: [multi-file]\n---\n\n## Trap — multi-file\n\nOrder edits so callers ship before callees.\n"
	if err := os.WriteFile(filepath.Join(trapsDir, "multi-file.md"), []byte(multiFileTrap), 0o644); err != nil {
		t.Fatalf("seed trap: %v", err)
	}

	saveLoadTrap := "---\napplies_to: [save-load]\n---\n\n## Trap — save-load\n\nProject-specific tag; should NOT fire on this brief.\n"
	if err := os.WriteFile(filepath.Join(trapsDir, "save-load.md"), []byte(saveLoadTrap), 0o644); err != nil {
		t.Fatalf("seed trap: %v", err)
	}

	specPath := filepath.Join(root, "spec.md")
	if err := os.WriteFile(specPath, []byte("Add a feature.\n"), 0o644); err != nil {
		t.Fatalf("seed spec: %v", err)
	}

	opts := Options{
		SpecPath:    specPath,
		OutputPath:  filepath.Join(root, "brief.md"),
		Title:       "overlay-test",
		MaxIters:    1,
		ProjectRoot: root,
	}
	// Brief body cites both a test file (test-code tag) AND a
	// different-dir source file (multi-file tag).
	draftBody := "# Drafted brief\n\n## Problem\n\nWiring change.\n\n## Files To Modify\n\n| File | Change |\n|------|--------|\n| `cmd/idiom/foo.go` | MODIFY |\n| `internal/foo/bar_test.go` | ADD test |\n\n## Acceptance\n\n- `go test ./...`\n"
	dispatch, _ := stubDispatcher(t, []string{
		draftBody,
		"# Review\n\n## Bugs found\n\nnone\n\n## Verdict\n\nship-as-is\n",
	})

	var stderr bytes.Buffer

	code := Run(opts, dispatch, &bytes.Buffer{}, &stderr)

	if code != 0 {
		t.Fatalf("expected exit 0 (ship-as-is), got %d; stderr=%s", code, stderr.String())
	}

	body, err := os.ReadFile(opts.OutputPath)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}

	if !strings.Contains(string(body), "HARD PROHIBITIONS (project)") {
		t.Fatalf("expected hard-prohibitions overlay in output, got:\n%s", body)
	}

	if !strings.Contains(string(body), "Trap — test-code") {
		t.Fatalf("expected test-code trap inlined, got:\n%s", body)
	}

	if !strings.Contains(string(body), "Trap — multi-file") {
		t.Fatalf("expected multi-file trap inlined, got:\n%s", body)
	}

	if strings.Contains(string(body), "Trap — save-load") {
		t.Fatalf("save-load trap should NOT fire (not in canonical tags for this brief), got:\n%s", body)
	}

	if !strings.Contains(stderr.String(), "atom-shape tags") {
		t.Fatalf("expected stderr to log atom-shape tags, got: %s", stderr.String())
	}
}

func TestRefineLintIgnoresProjectOverlayGuidance(t *testing.T) {
	root := t.TempDir()
	overlayDir := filepath.Join(root, refineOverlayDir)
	trapsDir := filepath.Join(overlayDir, "traps")

	if err := os.MkdirAll(trapsDir, 0o755); err != nil {
		t.Fatalf("mkdir traps: %v", err)
	}

	trap := "---\napplies_to: [test-code]\n---\n\n## Trap — example\n\n```cpp\nCHECK(example());\n```\n"
	if err := os.WriteFile(filepath.Join(trapsDir, "test-code.md"), []byte(trap), 0o644); err != nil {
		t.Fatalf("seed trap: %v", err)
	}

	draftPath := filepath.Join(root, "draft.md")
	draft := "# Draft\n\n## Problem\n\nChange `native/test_example.cpp`.\n\n" +
		refineDeliverableMarker +
		"\n\n## Required Behavior\n\nDo the edit.\n\n## Acceptance\n\n- Run tests.\n"

	if err := os.WriteFile(draftPath, []byte(draft), 0o644); err != nil {
		t.Fatalf("seed draft: %v", err)
	}

	var lintInput string

	stubRefineLint(t, func(path string) (brieflint.Report, error) {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read lint input: %v", err)
		}

		lintInput = string(body)

		return refineLintReport(path, 0), nil
	}, nil)

	var stderr bytes.Buffer

	code, _ := captureRefineLintOutput(draftPath, "", root, refineModeAnchored, []string{"native/test_example.cpp"}, &stderr)

	if code != 0 {
		t.Fatalf("stub lint should pass, got %d", code)
	}

	if !strings.Contains(lintInput, "## Files To Modify") || !strings.Contains(lintInput, "## Acceptance") {
		t.Fatalf("lint input should include canonical brief sections after marker injection, got:\n%s", lintInput)
	}

	if strings.Contains(lintInput, "Trap — example") || strings.Contains(lintInput, "```cpp") {
		t.Fatalf("project overlay guidance should not be part of structural lint input, got:\n%s", lintInput)
	}
}

// TestRunLaneRefinePatchPhaseSeesLintPreload locks in §6.1 Gap 1
// (Colony shipped 2026-05-11): when refine runs a patch iteration,
// the patch meta-brief inlines the previous draft's lint output so
// the patcher targets BLOCKs directly instead of relying on the
// reviewer to transcribe every finding.
func TestRunLaneRefinePatchPhaseSeesLintPreload(t *testing.T) {
	stubRefineLint(t, func(path string) (brieflint.Report, error) {
		return refineLintReport(path, 1,
			brieflint.Finding{Severity: "BLOCK", Category: "unfilled-placeholder", Message: "unfilled <placeholder> in Files To Modify row"},
			brieflint.Finding{Severity: "WARN", Category: "edit-shape", Message: "append-to-end-of-file edit shape"},
		), nil
	}, nil)

	root := t.TempDir()
	specPath := filepath.Join(root, "spec.md")

	if err := os.WriteFile(specPath, []byte("Add a feature.\n"), 0o644); err != nil {
		t.Fatalf("seed spec: %v", err)
	}

	opts := Options{
		SpecPath:    specPath,
		OutputPath:  filepath.Join(root, "brief.md"),
		Title:       "patch-lint",
		MaxIters:    2,
		ProjectRoot: root,
	}
	dispatch, _ := stubDispatcher(t, []string{
		nonTrivialDraftBody("Draft v1"),
		"# Review\n\n## Bugs found\n\n1. **A bug**: fix it.\n\n## Verdict\n\nfix-bugs-then-ship\n",
		nonTrivialDraftBody("Draft v2"),
		"# Review\n\n## Bugs found\n\nnone\n\n## Verdict\n\nship-as-is\n",
	})

	var stderr bytes.Buffer

	_ = Run(opts, dispatch, &bytes.Buffer{}, &stderr)

	metaPatchPath := filepath.Join(refineStateDir(root, "patch-lint"), "iter-2", "meta-patch.md")

	body, err := os.ReadFile(metaPatchPath)
	if err != nil {
		t.Fatalf("read meta-patch: %v", err)
	}

	if !strings.Contains(string(body), "[BLOCK] (unfilled-placeholder) unfilled <placeholder>") {
		t.Fatalf("expected lint BLOCK inlined in patch meta-brief, got:\n%s", body)
	}

	if !strings.Contains(string(body), "Pre-loaded lint findings") {
		t.Fatalf("expected 'Pre-loaded lint findings' section in patch meta-brief, got:\n%s", body)
	}
}

func TestWriteRefinePatchMetaPreloadsSourceEvidenceAnchors(t *testing.T) {
	root := t.TempDir()
	srcPath := filepath.Join(root, "native", "src", "simulation", "creatures", "formative_events.h")

	if err := os.MkdirAll(filepath.Dir(srcPath), 0o755); err != nil {
		t.Fatalf("mkdir source dir: %v", err)
	}

	source := strings.Join([]string{
		"enum class FormativeEventType : uint8_t {",
		"    SURVIVED_BATTLE,",
		"    FIRST_KILL,",
		"    COUNT,",
		"};",
		"",
		"enum class MemoryType : uint8_t {",
		"    FIRST_KILL,",
		"    COUNT,",
		"};",
	}, "\n")
	if err := os.WriteFile(srcPath, []byte(source), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	prevDraft := filepath.Join(root, ".idiom", "refine", "example", "iter-1", "draft.md")
	prevReview := filepath.Join(root, ".idiom", "refine", "example", "iter-1", "review.md")
	metaPath := filepath.Join(root, ".idiom", "refine", "example", "iter-2", "meta-patch.md")
	newDraft := filepath.Join(root, ".idiom", "refine", "example", "iter-2", "draft.md")

	for _, path := range []string{prevDraft, prevReview, metaPath} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", path, err)
		}
	}

	if err := os.WriteFile(prevDraft, []byte("Add FormativeEventType and MemoryType anchors.\n"), 0o644); err != nil {
		t.Fatalf("write draft: %v", err)
	}

	if err := os.WriteFile(prevReview, []byte("Missing anchor in formative_events.h for FormativeEventType, MemoryType, and COUNT.\n"), 0o644); err != nil {
		t.Fatalf("write review: %v", err)
	}

	lintOutput := "- [BLOCK] (anchor-specificity) exact-anchor specificity failed for required edit path(s): native/src/simulation/creatures/formative_events.h"
	if err := writeRefinePatchMeta(metaPath, prevDraft, prevReview, newDraft, lintOutput, []string{"native/src/simulation/creatures/formative_events.h"}); err != nil {
		t.Fatalf("write patch meta: %v", err)
	}

	body, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatalf("read patch meta: %v", err)
	}

	text := string(body)
	for _, want := range []string{
		"Pre-loaded source evidence",
		"`native/src/simulation/creatures/formative_events.h:1`: `enum class FormativeEventType : uint8_t {`",
		"`native/src/simulation/creatures/formative_events.h:7`: `enum class MemoryType : uint8_t {`",
		"`native/src/simulation/creatures/formative_events.h:4`: `COUNT,`",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected patch meta to contain %q, got:\n%s", want, text)
		}
	}
}

func TestRefinePatchSourceEvidenceStaysCompactAndSkipsBoilerplate(t *testing.T) {
	root := t.TempDir()
	relPath := filepath.ToSlash(filepath.Join("native", "src", "simulation", "creatures", "formative_events.h"))
	srcPath := filepath.Join(root, filepath.FromSlash(relPath))

	if err := os.MkdirAll(filepath.Dir(srcPath), 0o755); err != nil {
		t.Fatalf("mkdir source dir: %v", err)
	}

	var source strings.Builder

	source.WriteString("#include \"appraisal.h\"\n")
	source.WriteString("namespace Colony {\n")
	source.WriteString("enum class FormativeEventType : uint8_t {\n")

	for i := 0; i < 20; i++ {
		source.WriteString("    EVENT_")
		source.WriteString(string(rune('A' + i)))
		source.WriteString(",\n")
	}

	source.WriteString("    COUNT,\n")
	source.WriteString("};\n")
	source.WriteString("}\n")

	if err := os.WriteFile(srcPath, []byte(source.String()), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	prevDraft := filepath.Join(root, ".idiom", "refine", "example", "iter-1", "draft.md")
	prevReview := filepath.Join(root, ".idiom", "refine", "example", "iter-1", "review.md")

	for _, path := range []string{prevDraft, prevReview} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", path, err)
		}
	}

	if err := os.WriteFile(prevDraft, []byte("Use FormativeEventType EVENT_A EVENT_B EVENT_C EVENT_D EVENT_E EVENT_F EVENT_G EVENT_H EVENT_I EVENT_J EVENT_K EVENT_L EVENT_M EVENT_N EVENT_O EVENT_P EVENT_Q EVENT_R EVENT_S EVENT_T COUNT anchors.\n"), 0o644); err != nil {
		t.Fatalf("write draft: %v", err)
	}

	if err := os.WriteFile(prevReview, []byte("Missing compact anchors in native/src/simulation/creatures/formative_events.h.\n"), 0o644); err != nil {
		t.Fatalf("write review: %v", err)
	}

	evidence := refinePatchSourceEvidence(root, prevDraft, prevReview, "", []string{relPath})
	if strings.Contains(evidence, "#include") || strings.Contains(evidence, "namespace Colony") {
		t.Fatalf("source evidence should skip boilerplate, got:\n%s", evidence)
	}

	count := 0

	for _, line := range strings.Split(evidence, "\n") {
		if strings.HasPrefix(line, "- `"+relPath+":") {
			count++
		}
	}

	if count == 0 || count > refinePatchSourceEvidenceAnchorsPerPath {
		t.Fatalf("expected 1..%d anchors, got %d:\n%s", refinePatchSourceEvidenceAnchorsPerPath, count, evidence)
	}
}

func TestRefinePatchSourceEvidencePreservesCompleteAnchorLines(t *testing.T) {
	root := t.TempDir()
	relPath := filepath.ToSlash(filepath.Join("internal", "writerroute", "decision.go"))
	srcPath := filepath.Join(root, filepath.FromSlash(relPath))

	if err := os.MkdirAll(filepath.Dir(srcPath), 0o755); err != nil {
		t.Fatalf("mkdir source dir: %v", err)
	}

	longLine := "func NewDecision(mode WriterMode, lane string, plannerModel string, executorModel string, reviewerModel string, classifierModel string, riskTier string, costTier string, scopeTier string, verificationOwner string, reviewPolicy string, requiredEditPaths []string, evidencePacketIDs []string, reasonCodes []string, escalationTriggers []string, userOverride bool, taskFamily string) Decision {"
	if err := os.WriteFile(srcPath, []byte("package writerroute\n\n"+longLine+"\n\treturn Decision{}\n}\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	prevDraft := filepath.Join(root, ".idiom", "refine", "example", "iter-1", "draft.md")
	prevReview := filepath.Join(root, ".idiom", "refine", "example", "iter-1", "review.md")

	for _, path := range []string{prevDraft, prevReview} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", path, err)
		}
	}

	if err := os.WriteFile(prevDraft, []byte("Review NewDecision in internal/contracts/writerroute/decision.go.\n"), 0o644); err != nil {
		t.Fatalf("write draft: %v", err)
	}

	if err := os.WriteFile(prevReview, []byte("Patch must use the complete NewDecision anchor from internal/contracts/writerroute/decision.go.\n"), 0o644); err != nil {
		t.Fatalf("write review: %v", err)
	}

	evidence := refinePatchSourceEvidence(root, prevDraft, prevReview, "", []string{relPath})
	if !strings.Contains(evidence, longLine) {
		t.Fatalf("source evidence lost complete anchor line:\n%s", evidence)
	}

	if strings.Contains(evidence, "...") {
		t.Fatalf("source evidence must not truncate actionable anchor lines:\n%s", evidence)
	}
}

func TestRefinePatchSourceEvidencePrioritizesBoundaryAnchors(t *testing.T) {
	root := t.TempDir()
	relPath := filepath.ToSlash(filepath.Join("native", "src", "simulation", "creatures", "memory_helpers.cpp"))
	srcPath := filepath.Join(root, filepath.FromSlash(relPath))

	if err := os.MkdirAll(filepath.Dir(srcPath), 0o755); err != nil {
		t.Fatalf("mkdir source dir: %v", err)
	}

	var source strings.Builder

	source.WriteString("enum class MemoryType : uint8_t {\n")

	for i := 0; i < 30; i++ {
		fmt.Fprintf(&source, "    DISTRACTOR_%02d,\n", i)
	}

	source.WriteString("    FIRST_KILL,\n")
	source.WriteString("    COUNT,\n")
	source.WriteString("};\n\n")
	source.WriteString("float default_memory_mood(MemoryType type) {\n")
	source.WriteString("    switch (type) {\n")
	source.WriteString("    case MemoryType::FIRST_KILL: return 8.0f;\n")
	source.WriteString("    case MemoryType::COUNT: return 0.0f;\n")
	source.WriteString("    }\n")
	source.WriteString("    return 0.0f;\n")
	source.WriteString("}\n")

	if err := os.WriteFile(srcPath, []byte(source.String()), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	prevDraft := filepath.Join(root, ".idiom", "refine", "example", "iter-1", "draft.md")
	prevReview := filepath.Join(root, ".idiom", "refine", "example", "iter-1", "review.md")

	for _, path := range []string{prevDraft, prevReview} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", path, err)
		}
	}

	draft := "Source verification pending: confirm the MemoryType COUNT sentinel and default_memory_mood() switch closing boundary in native/src/simulation/creatures/memory_helpers.cpp.\n"
	if err := os.WriteFile(prevDraft, []byte(draft), 0o644); err != nil {
		t.Fatalf("write draft: %v", err)
	}

	review := "Missing exact anchors for COUNT, default_memory_mood(), and case MemoryType::COUNT.\n"
	if err := os.WriteFile(prevReview, []byte(review), 0o644); err != nil {
		t.Fatalf("write review: %v", err)
	}

	evidence := refinePatchSourceEvidence(root, prevDraft, prevReview, "", []string{relPath})
	for _, want := range []string{
		"`native/src/simulation/creatures/memory_helpers.cpp:33`: `COUNT,`",
		"`native/src/simulation/creatures/memory_helpers.cpp:36`: `float default_memory_mood(MemoryType type) {`",
		"`native/src/simulation/creatures/memory_helpers.cpp:39`: `case MemoryType::COUNT: return 0.0f;`",
	} {
		if !strings.Contains(evidence, want) {
			t.Fatalf("expected boundary evidence %q, got:\n%s", want, evidence)
		}
	}
}

func TestRenderRefineLintReportRendersBlockAndWarn(t *testing.T) {
	out := renderRefineLintReport(brieflint.Report{
		Brief: "/tmp/test.md",
		Exit:  1,
		Findings: []brieflint.Finding{
			{Severity: "BLOCK", Category: "anchor-specificity", Brief: "/tmp/test.md", Message: "exact-anchor failed for x.go"},
			{Severity: "WARN", Category: "missing-current-state", Brief: "/tmp/test.md", Message: "no verified state"},
			{Severity: "INFO", Category: "other", Brief: "/tmp/test.md", Message: "informational note (dropped)"},
		},
	})

	if !strings.Contains(out, "[BLOCK] (anchor-specificity) exact-anchor failed for x.go") {
		t.Fatalf("expected BLOCK rendered with severity+category+message, got:\n%s", out)
	}

	if !strings.Contains(out, "[WARN] (missing-current-state) no verified state") {
		t.Fatalf("expected WARN rendered, got:\n%s", out)
	}

	if strings.Contains(out, "INFO") || strings.Contains(out, "informational") {
		t.Fatalf("expected INFO filtered out, got:\n%s", out)
	}
}

// TestRenderRefineLintFindingsCleanPassEmitsClearNote: when lint
// passes with zero BLOCK/WARN, render a clear "(clean)" note so the
// reviewer never sees an empty preload (which would look like
// silent failure).
func TestRenderRefineLintFindingsCleanPassEmitsClearNote(t *testing.T) {
	out := renderRefineLintReport(brieflint.Report{Brief: "/tmp/test.md", Exit: 0})

	if !strings.Contains(out, "no BLOCK/WARN findings") {
		t.Fatalf("expected clean-pass note, got: %s", out)
	}
}

func TestWriteFallbackRefineReviewFromLintBlocks(t *testing.T) {
	root := t.TempDir()
	reviewPath := filepath.Join(root, ".idiom", "refine", "atom", "iter-1", "review.md")
	lintOutput := strings.Join([]string{
		"- [BLOCK] (anchor-specificity) exact-anchor failed for native/src/foo.cpp",
		"- [WARN] (other) test-anchor preload missing for native/tests/test_foo.cpp",
	}, "\n")

	wrote, err := writeFallbackRefineReview(reviewPath, lintOutput, "review-missing", nil)
	if err != nil {
		t.Fatalf("write fallback review: %v", err)
	}

	if !wrote {
		t.Fatal("expected fallback review to be written for lint BLOCKs")
	}

	bodyBytes, err := os.ReadFile(reviewPath)
	if err != nil {
		t.Fatalf("read fallback review: %v", err)
	}

	body := string(bodyBytes)
	if got := parseRefineVerdict(body); got != "fix-bugs-then-ship" {
		t.Fatalf("fallback verdict = %q, want fix-bugs-then-ship\n%s", got, body)
	}

	bugs := parseRefineBugTitles(body)
	if len(bugs) != 1 || !strings.Contains(bugs[0], "anchor-specificity") {
		t.Fatalf("fallback bugs = %#v, want one anchor-specificity lint block\n%s", bugs, body)
	}

	if !strings.Contains(body, "[WARN] (other) test-anchor preload missing") {
		t.Fatalf("fallback review should preserve WARN findings for patch context:\n%s", body)
	}
}

func TestWriteFallbackRefineReviewSkipsWithoutLintBlocks(t *testing.T) {
	root := t.TempDir()
	reviewPath := filepath.Join(root, "review.md")

	wrote, err := writeFallbackRefineReview(reviewPath, "- [WARN] (other) warning only", "review-missing", nil)
	if err != nil {
		t.Fatalf("write fallback review: %v", err)
	}

	if wrote {
		t.Fatal("fallback review should not synthesize a review without a lint BLOCK")
	}

	if _, err := os.Stat(reviewPath); !os.IsNotExist(err) {
		t.Fatalf("review path should not exist, stat err=%v", err)
	}
}

// TestRefineCanonicalTagsCoverAllClassifierEmissions catches drift
// between the canonical-tags list (used by the orphan-tag warning
// in loadProjectTraps) and the actual set of tags the classifier
// can emit. If a new tag is added to classifyBriefAtomShape but not
// refineCanonicalTags, traps applying it would warn falsely. If it's
// added to refineCanonicalTags but not the classifier, traps would
// never fire silently. This test drives the classifier through
// every shape that triggers each canonical tag and asserts the
// union matches refineCanonicalTags exactly.
func TestRefineCanonicalTagsCoverAllClassifierEmissions(t *testing.T) {
	// Each entry: a brief body designed to trigger ONE canonical tag.
	// If you add a new tag, add a row here AND to refineCanonicalTags.
	probes := []struct {
		name string
		body string
		want string
	}{
		{
			name: "test-code-trigger",
			body: "## Files To Modify\n\n| File | Change |\n|------|--------|\n| `pkg/foo_test.go` | ADD |\n",
			want: refineTagTestCode,
		},
		{
			name: "multi-file-trigger",
			body: "## Files To Modify\n\n| File | Change |\n|------|--------|\n| `pkg/a/foo.go` | MODIFY |\n| `pkg/b/bar.go` | MODIFY |\n",
			want: refineTagMultiFile,
		},
		{
			name: "markdown-deliverable-trigger",
			body: "## Files To Modify\n\n| File | Change |\n|------|--------|\n| `.planning/foo.md` | NEW FILE |\n",
			want: refineTagMarkdownDeliverable,
		},
		{
			name: "generic-trigger",
			body: "## Files To Modify\n\n| File | Change |\n|------|--------|\n| `pkg/foo.go` | MODIFY |\n",
			want: refineTagGeneric,
		},
	}
	emitted := map[string]bool{}

	for _, p := range probes {
		for _, tag := range classifyBriefAtomShape([]byte(p.body)) {
			emitted[tag] = true
		}

		// Each probe must produce its target tag.
		if !slices.Contains(classifyBriefAtomShape([]byte(p.body)), p.want) {
			t.Fatalf("probe %s: classifier did not emit %q for body:\n%s\nemitted=%v", p.name, p.want, p.body, classifyBriefAtomShape([]byte(p.body)))
		}
	}

	canonical := map[string]bool{}
	for _, t := range refineCanonicalTags {
		canonical[t] = true
	}

	// Every classifier-emitted tag must be in canonical.
	for tag := range emitted {
		if !canonical[tag] {
			t.Fatalf("classifier emits %q but refineCanonicalTags does not include it; the orphan-tag warning would fire falsely. Add %q to refineCanonicalTags.", tag, tag)
		}
	}

	// Every canonical tag must be reachable from some classifier path.
	for tag := range canonical {
		if !emitted[tag] {
			t.Fatalf("refineCanonicalTags lists %q but no classifier path emits it; project traps using applies_to=[%s] would never fire. Either add a classifier path or drop the tag from refineCanonicalTags.", tag, tag)
		}
	}
}

// TestPersistRefineLintJSONWritesEnvelopeToIterDir locks in Colony
// §8's recommendation: the full JSON envelope lands at
// <iterDir>/lint.json for orchestrator inspection, separate from
// the BLOCK/WARN-filtered preload the reviewer consumes.
func TestPersistRefineLintJSONWritesEnvelopeToIterDir(t *testing.T) {
	iterDir := t.TempDir()
	raw := []byte(`{"brief":"/tmp/x.md","exit":1,"findings":[{"severity":"BLOCK","category":"anchor-specificity","brief":"/tmp/x.md","message":"missing anchor"}]}`)

	var stderr bytes.Buffer

	persistRefineLintJSON(iterDir, raw, &stderr)

	gotPath := filepath.Join(iterDir, "lint.json")

	body, err := os.ReadFile(gotPath)
	if err != nil {
		t.Fatalf("expected lint.json written, got: %v", err)
	}

	if string(body) != string(raw) {
		t.Fatalf("expected raw envelope persisted verbatim:\n got=%s\nwant=%s", body, raw)
	}
}

// TestPersistRefineLintJSONLogsButContinuesOnError exercises the
// best-effort contract: a write failure (read-only iterDir) logs to
// stderr but does not propagate. The in-memory preload still works,
// so the run continues — Colony §8 framing: the on-disk artifact is
// for inspection, not control flow.
func TestPersistRefineLintJSONLogsButContinuesOnError(t *testing.T) {
	// Use a path that can't be written (parent doesn't exist).
	iterDir := "/nonexistent-path-for-test/iter-1"

	var stderr bytes.Buffer

	persistRefineLintJSON(iterDir, []byte(`{}`), &stderr)

	if !strings.Contains(stderr.String(), "WARN cannot persist") {
		t.Fatalf("expected best-effort write failure to log WARN, got: %s", stderr.String())
	}
}

// TestRunLaneRefinePersistLintJSONAlongMetricsJSON exercises the
// end-to-end path: a clean ship-as-is iteration writes lint.json
// alongside metrics.json so orchestrators can read both with one
// directory listing.
func TestRunLaneRefinePersistLintJSONAlongMetricsJSON(t *testing.T) {
	stubRefineLintPass(t)

	root := t.TempDir()
	specPath := filepath.Join(root, "spec.md")

	if err := os.WriteFile(specPath, []byte("Add a feature.\n"), 0o644); err != nil {
		t.Fatalf("seed spec: %v", err)
	}

	opts := Options{
		SpecPath:    specPath,
		OutputPath:  filepath.Join(root, "brief.md"),
		Title:       "persist-lint",
		MaxIters:    1,
		ProjectRoot: root,
	}
	dispatch, _ := stubDispatcher(t, []string{
		nonTrivialDraftBody("Drafted brief"),
		"# Review\n\n## Bugs found\n\nnone\n\n## Verdict\n\nship-as-is\n",
	})

	var stderr bytes.Buffer

	code := Run(opts, dispatch, &bytes.Buffer{}, &stderr)

	if code != 0 {
		t.Fatalf("expected exit 0, got %d; stderr=%s", code, stderr.String())
	}

	iterDir := filepath.Join(refineStateDir(root, "persist-lint"), "iter-1")
	if _, err := os.Stat(filepath.Join(iterDir, "lint.json")); err != nil {
		t.Fatalf("expected lint.json persisted alongside metrics.json, got: %v\nstderr=%s", err, stderr.String())
	}

	if _, err := os.Stat(filepath.Join(iterDir, "metrics.json")); err != nil {
		t.Fatalf("expected metrics.json (regression check), got: %v", err)
	}
}

// TestExtractDeliverableTargetExtractsFirstNonSourcePath locks in
// §6.1 Gap 6 (Colony harness-side fix): the harness scans the spec
// for verb+backtick-path patterns and returns the first non-source
// path. The model no longer needs to re-parse the spec — the
// extracted target lands in the meta-draft as a pre-filled context
// block.
func TestExtractDeliverableTargetExtractsFirstNonSourcePath(t *testing.T) {
	cases := []struct {
		name string
		spec string
		want string
	}{
		{
			name: "produce-after-source-reference",
			spec: "audit `lane_postflight.sh` and produce `docs/research/lane-postflight-check-inventory.md` listing each check.",
			want: "docs/research/lane-postflight-check-inventory.md",
		},
		{
			name: "create-pure-markdown",
			spec: "create `.planning/inventory.md` with the new design.",
			want: ".planning/inventory.md",
		},
		{
			name: "write-pure-markdown",
			spec: "write `notes/foo.txt` summarizing the change.",
			want: "notes/foo.txt",
		},
		{
			name: "generate-json-config",
			spec: "generate `config/settings.json` with default values.",
			want: "config/settings.json",
		},
		{
			name: "output-to-multiword-verb",
			spec: "output to `output/report.md` after the analysis.",
			want: "output/report.md",
		},
		{
			name: "source-only-no-target",
			spec: "modify `cmd/idiom/foo.go` to add a flag.",
			want: "",
		},
		{
			name: "no-verb-mention",
			spec: "Make the system faster.",
			want: "",
		},
		{
			name: "skip-source-take-markdown",
			spec: "modify `pkg/a.go` and create `docs/CHANGELOG.md`",
			want: "docs/CHANGELOG.md",
		},
		{
			name: "reject-symbol-target",
			spec: "add `EventClass` mapping for the new event.",
			want: "",
		},
		{
			name: "reject-expression-target",
			spec: "add `valence > 0.0f` branch to the existing condition.",
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := pathparse.FirstDeliverableTarget(pathparse.ExtractDeliverableTargets(tc.spec, pathparse.DetectMode(tc.spec)))
			if got != tc.want {
				t.Fatalf("first extracted deliverable target for %q = %q, want %q", tc.spec, got, tc.want)
			}
		})
	}
}

func TestExtractDeliverableTargetsPrefersFilesToModifyTable(t *testing.T) {
	spec := `# Atom

## Files to Modify

| File | Change |
|---|---|
| ` + "`native/src/simulation/ai/appraisal.cpp`" + ` | Modify positive valence |
| ` + "`native/src/simulation/ai/appraisal.h`" + ` | No change |
| ` + "`native/src/core/constants/individuated_mood.h`" + ` | Add EventClass cells |
| ` + "`native/tests/test_individuated_mood.cpp`" + ` | Add tests |

## Changes

Add ` + "`valence > 0.0f`" + ` handling and populate ` + "`EventClass`" + ` mappings.
`
	got := pathparse.ExtractDeliverableTargets(spec, refineModeAnchored)
	want := []string{
		"native/src/simulation/ai/appraisal.cpp",
		"native/src/core/constants/individuated_mood.h",
		"native/tests/test_individuated_mood.cpp",
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("pathparse.ExtractDeliverableTargets() = %#v, want %#v", got, want)
	}
}

func TestExtractDeliverableTargetsPrefersExpectedFileScope(t *testing.T) {
	spec := `# Plan

## Expected File Scope

| File | Purpose |
| --- | --- |
| ` + "`internal/contracts/writerroute/decision.go`" + ` | Domain type |
| ` + "`internal/surfaces/tui/app/prompt_routing.go`" + ` or new ` + "`internal/surfaces/tui/app/writer_route.go`" + ` | TUI event wiring |

## Non-Goals

- Do not add ` + "`idiom.code_draft.v1`" + ` implementation.
`
	got := pathparse.ExtractDeliverableTargets(spec, refineModeAnchored)
	want := []string{
		"internal/contracts/writerroute/decision.go",
		"internal/surfaces/tui/app/prompt_routing.go",
		"internal/surfaces/tui/app/writer_route.go",
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("pathparse.ExtractDeliverableTargets() = %#v, want %#v", got, want)
	}
}

func TestExtractDeliverableTargetsFallsBackToScopeBullets(t *testing.T) {
	spec := `# Legacy atom

## Scope

- ` + "`scripts/test_anomaly_scan.py`" + `

Acceptance-only context:
- ` + "`scripts/anomaly_scan.py`" + `
- ` + "`native/tests/test_diagnostic_logger.cpp`" + `

## Acceptance

- update the anomaly scan tests
`
	got := pathparse.ExtractDeliverableTargets(spec, refineModeAnchored)
	want := []string{"scripts/test_anomaly_scan.py"}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("pathparse.ExtractDeliverableTargets() = %#v, want %#v", got, want)
	}
}

func TestExtractDeliverableTargetsPrefersFilesTableOverScope(t *testing.T) {
	spec := `# Atom

## Files To Modify

| File | Change |
|------|--------|
| ` + "`src/real.go`" + ` | MODIFY: actual target |

## Scope

- ` + "`src/legacy.go`" + `
`
	got := pathparse.ExtractDeliverableTargets(spec, refineModeAnchored)
	want := []string{"src/real.go"}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("pathparse.ExtractDeliverableTargets() = %#v, want %#v", got, want)
	}
}

func TestExtractDeliverableTargetsRejectsSlashSeparatedProseAndCommands(t *testing.T) {
	spec := `# Atom

## Files To Modify

| File | Change |
|------|--------|
| ` + "`save/load`" + ` | Explain the persistence risk, not a file. |
| ` + "`UI/Codex`" + ` | Prose lane label, not a file. |
| ` + "`quern/trough`" + ` | Domain shorthand, not a file. |
| ` + "`gdscript_trace.sh --renderer farm_overlay`" + ` | Command example, not a file. |
| ` + "`native/src/simulation/hazards/collapse_event.cpp`" + ` | Create source file. |
| ` + "`docs/research/collapse-event.md`" + ` | Create design note. |
`
	got := pathparse.ExtractDeliverableTargets(spec, refineModeAnchored)
	want := []string{
		"native/src/simulation/hazards/collapse_event.cpp",
		"docs/research/collapse-event.md",
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("pathparse.ExtractDeliverableTargets() = %#v, want %#v", got, want)
	}
}

// TestRefineDeliverableTargetMismatchSurfacesAsBlock locks in the
// validation path: when refine extracts a target from the spec but
// the produced brief's Files To Modify references a DIFFERENT path
// (the C.1 bug shape — model put refine's scratchpad in the
// allowlist), the mismatch becomes a [BLOCK] in the lint preload so
// the reviewer's ship-as-is gate refuses convergence until the
// patcher fixes the target.
func TestRefineDeliverableTargetMismatchSurfacesAsBlock(t *testing.T) {
	dir := t.TempDir()
	draftPath := filepath.Join(dir, "draft.md")
	// Draft's Files To Modify points at the WRONG path (refine's
	// scratchpad path, mimicking the C.1 bug shape).
	body := "# Brief\n\n## Files To Modify\n\n| File | Change |\n|------|--------|\n| `.idiom/refine/test/iter-1/draft.md` | NEW FILE |\n\n## Acceptance\n\n- test\n"
	if err := os.WriteFile(draftPath, []byte(body), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	specTarget := "docs/research/lane-postflight-check-inventory.md"
	got := refineDeliverableTargetMismatch(draftPath, []string{specTarget})

	if got == "" {
		t.Fatalf("expected BLOCK on path mismatch, got empty")
	}

	if !strings.Contains(got, "[BLOCK]") || !strings.Contains(got, "deliverable-target-mismatch") {
		t.Fatalf("expected BLOCK with deliverable-target-mismatch category, got: %s", got)
	}

	if !strings.Contains(got, specTarget) {
		t.Fatalf("expected BLOCK to name the expected target, got: %s", got)
	}
}

// TestRefineDeliverableTargetMatchReturnsEmpty: when the brief
// correctly references the extracted target, no mismatch BLOCK is
// emitted — silent success.
func TestRefineDeliverableTargetMatchReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	draftPath := filepath.Join(dir, "draft.md")
	body := "# Brief\n\n## Files To Modify\n\n| File | Change |\n|------|--------|\n| `docs/research/inventory.md` | NEW FILE |\n\n## Acceptance\n\n- test\n"

	if err := os.WriteFile(draftPath, []byte(body), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	got := refineDeliverableTargetMismatch(draftPath, []string{"docs/research/inventory.md"})
	if got != "" {
		t.Fatalf("expected silent success on match, got: %s", got)
	}
}

func TestRefineDeliverableTargetMismatchRequiresAllAnchoredTargets(t *testing.T) {
	dir := t.TempDir()
	draftPath := filepath.Join(dir, "draft.md")
	body := "# Brief\n\n## Files To Modify\n\n| File | Change |\n|------|--------|\n| `native/a.cpp` | MODIFY |\n\n## Acceptance\n\n- test\n"

	if err := os.WriteFile(draftPath, []byte(body), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	got := refineDeliverableTargetMismatch(draftPath, []string{"native/a.cpp", "native/b.cpp"})
	if got == "" {
		t.Fatalf("expected BLOCK when one required target is missing")
	}

	if !strings.Contains(got, "native/b.cpp") {
		t.Fatalf("expected missing target in BLOCK, got: %s", got)
	}
}

// TestRefineDeliverableTargetEmptyTargetSkipsCheck: when the harness
// couldn't extract a target from the spec (no canonical verb+path),
// the mismatch check is a no-op — no false positives on specs that
// just don't name a target.
func TestRefineDeliverableTargetEmptyTargetSkipsCheck(t *testing.T) {
	dir := t.TempDir()
	draftPath := filepath.Join(dir, "draft.md")
	body := "# Brief\n\n## Files To Modify\n\n| File | Change |\n|------|--------|\n| `whatever.md` | NEW FILE |\n"

	if err := os.WriteFile(draftPath, []byte(body), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if got := refineDeliverableTargetMismatch(draftPath, nil); got != "" {
		t.Fatalf("expected no-op when target is empty, got: %s", got)
	}
}

// TestRefineDeliverableContextBlockRendersExtractedTarget verifies
// the substitution block names the extracted path AND tells the
// model that the harness owns the path block. Gap 8 (2026-05-12)
// inverted the contract: model emits the marker, harness injects
// the path. The context block remains for legacy template overrides
// that still reference {{DELIVERABLE_TARGET_CONTEXT}}.
func TestRefineDeliverableContextBlockRendersExtractedTarget(t *testing.T) {
	got := refineDeliverableContextBlock([]string{"docs/research/x.md"})
	if !strings.Contains(got, "docs/research/x.md") {
		t.Fatalf("expected block to name the target, got: %s", got)
	}

	if !strings.Contains(got, "harness will inject") {
		t.Fatalf("expected harness-injection framing, got: %s", got)
	}

	if !strings.Contains(got, "idiom:harness-deliverable-block") {
		t.Fatalf("expected reference to harness marker, got: %s", got)
	}
}

// TestRefineDeliverableContextBlockEmptyFallsBack: when extraction
// returns empty, the block emits a no-op note so legacy template
// substitution doesn't leak an orphan {{DELIVERABLE_TARGET_CONTEXT}}
// placeholder.
func TestRefineDeliverableContextBlockEmptyFallsBack(t *testing.T) {
	got := refineDeliverableContextBlock(nil)
	if !strings.Contains(got, "no deliverable target detected") {
		t.Fatalf("expected fallback note, got: %s", got)
	}
}

// TestRefineInjectDeliverableBlockAnchored verifies the harness
// substitutes the marker with a canonical `## Files To Modify`
// table populated from the extracted target for anchored-edits mode.
func TestRefineInjectDeliverableBlockAnchored(t *testing.T) {
	body := []byte("# T\n\n## Problem\n\nspec.\n\n<!-- idiom:harness-deliverable-block -->\n\n## Verified Current State\n\nbody\n")
	out := refineInjectDeliverableBlock(body, "anchored", []string{"docs/research/x.md", "native/foo.cpp"}, "", io.Discard)
	got := string(out)

	if strings.Contains(got, "<!-- idiom:harness-deliverable-block -->") {
		t.Fatalf("marker should be replaced, got: %s", got)
	}

	if !strings.Contains(got, "## Files To Modify") {
		t.Fatalf("anchored injection should emit Files To Modify, got: %s", got)
	}

	if !strings.Contains(got, "`docs/research/x.md`") {
		t.Fatalf("injection should name the extracted target, got: %s", got)
	}

	if !strings.Contains(got, "`native/foo.cpp`") {
		t.Fatalf("injection should name every extracted target, got: %s", got)
	}
}

func TestRefineInjectDeliverableBlockAnchoredMarksMissingTargetsAsNewFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "cmd", "idiom"), 0o755); err != nil {
		t.Fatalf("mkdir existing target dir: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "cmd", "idiom", "lane.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("seed existing target: %v", err)
	}

	body := []byte("# T\n\n## Problem\n\nspec.\n\n<!-- idiom:harness-deliverable-block -->\n\n## Verified Current State\n\nbody\n")
	out := refineInjectDeliverableBlock(body, "anchored", []string{"internal/contracts/writerroute/decision.go", "cmd/idiom/lane.go"}, root, io.Discard)
	got := string(out)

	if !strings.Contains(got, "| `internal/contracts/writerroute/decision.go` | NEW FILE: harness-injected target (extracted from spec) |") {
		t.Fatalf("missing target should be injected as NEW FILE, got:\n%s", got)
	}

	if !strings.Contains(got, "| `cmd/idiom/lane.go` | MODIFY: harness-injected target (extracted from spec) |") {
		t.Fatalf("existing target should be injected as MODIFY, got:\n%s", got)
	}
}

// TestRefineInjectDeliverableBlockMarkdown verifies the harness
// substitutes the marker with a canonical `## Deliverable` block
// for markdown-deliverable mode.
func TestRefineInjectDeliverableBlockMarkdown(t *testing.T) {
	body := []byte("# T\n\n## Problem\n\nspec.\n\n<!-- idiom:harness-deliverable-block -->\n\n## Inline content\n\nbody\n")
	out := refineInjectDeliverableBlock(body, "markdown", []string{"docs/research/x.md"}, "", io.Discard)
	got := string(out)

	if strings.Contains(got, "<!-- idiom:harness-deliverable-block -->") {
		t.Fatalf("marker should be replaced, got: %s", got)
	}

	if !strings.Contains(got, "## Deliverable") {
		t.Fatalf("markdown injection should emit Deliverable, got: %s", got)
	}

	if !strings.Contains(got, "Path: `docs/research/x.md`") {
		t.Fatalf("injection should name the extracted target with Path: prefix, got: %s", got)
	}
}

// TestRefineInjectDeliverableBlockSkipsWhenMarkerAbsent verifies
// that drafts without the marker pass through unchanged (model
// authored its own block, or a legacy template — lint catches it
// downstream).
func TestRefineInjectDeliverableBlockSkipsWhenMarkerAbsent(t *testing.T) {
	body := []byte("# T\n\n## Problem\n\nspec.\n\n## Files To Modify\n\n| a | b |\n")
	out := refineInjectDeliverableBlock(body, "anchored", []string{"docs/research/x.md"}, "", io.Discard)

	if !bytes.Equal(body, out) {
		t.Fatalf("body should be unchanged when marker absent, got: %s", string(out))
	}
}

// TestRefineInjectDeliverableBlockSkipsWhenTargetEmpty verifies
// that empty target (extraction failed) leaves the body unchanged.
// The marker stays in the brief as a visible defect, which the
// reviewer's structural check will catch.
func TestRefineInjectDeliverableBlockSkipsWhenTargetEmpty(t *testing.T) {
	body := []byte("# T\n\n## Problem\n\n<!-- idiom:harness-deliverable-block -->\n\n## X\n")
	out := refineInjectDeliverableBlock(body, "anchored", nil, "", io.Discard)

	if !bytes.Equal(body, out) {
		t.Fatalf("body should be unchanged when target empty, got: %s", string(out))
	}
}

func TestFinalizeRefineLintsInjectedFinalBody(t *testing.T) {
	dir := t.TempDir()
	draftPath := filepath.Join(dir, "draft.md")
	outputPath := filepath.Join(dir, "out.md")
	body := "# Draft\n\n## Problem\n\nThis is a non-trivial refine draft with enough content to pass the minimum-size guard.\n\n<!-- idiom:harness-deliverable-block -->\n\n## Verified Current State\n\nSource verification pending.\n\n## Required Behavior\n\n1. Preserve the marker until finalize.\n\n## Acceptance\n\n- `go test ./cmd/idiom`\n"

	if err := os.WriteFile(draftPath, []byte(body), 0o644); err != nil {
		t.Fatalf("seed draft: %v", err)
	}

	var lintedBody string

	stubRefineLint(t, nil, func(path string, stdout, stderr io.Writer) (brieflint.TextRun, error) {
		blob, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read lint temp: %v", err)
		}

		lintedBody = string(blob)

		return brieflint.TextRun{Exit: 0}, nil
	})

	var stderr bytes.Buffer

	code := finalizeRefineWithTitleAndOverlay(draftPath, outputPath, "final-lint", 1, "ship-as-is", "", refineModeAnchored, []string{"native/foo.cpp"}, &stderr)

	if code != 0 {
		t.Fatalf("finalize exit=%d stderr=%s", code, stderr.String())
	}

	if !strings.Contains(lintedBody, "## Files To Modify") || !strings.Contains(lintedBody, "`native/foo.cpp`") {
		t.Fatalf("lint should see injected Files To Modify block, got:\n%s", lintedBody)
	}

	if strings.Contains(lintedBody, refineDeliverableMarker) {
		t.Fatalf("lint should not see raw deliverable marker, got:\n%s", lintedBody)
	}
}

func TestFinalizeRefineFailsShipAsIsWhenProjectOverlayBreaksLint(t *testing.T) {
	root := t.TempDir()
	overlayDir := filepath.Join(root, refineOverlayDir)
	trapsDir := filepath.Join(overlayDir, "traps")

	if err := os.MkdirAll(trapsDir, 0o755); err != nil {
		t.Fatalf("mkdir traps: %v", err)
	}

	trap := "---\napplies_to: [test-code]\n---\n\n## Trap — example\n\n```cpp\nCHECK(example());\n```\n"
	if err := os.WriteFile(filepath.Join(trapsDir, "test-code.md"), []byte(trap), 0o644); err != nil {
		t.Fatalf("seed trap: %v", err)
	}

	draftPath := filepath.Join(root, "draft.md")
	outputPath := filepath.Join(root, "out.md")
	body := "# Draft\n\n## Problem\n\nThis is a non-trivial refine draft for `native/test_example.cpp`.\n\n" +
		refineDeliverableMarker +
		"\n\n## Verified Current State\n\nSource verification pending.\n\n## Required Behavior\n\n1. Preserve the marker until finalize.\n\n## Acceptance\n\n- `go test ./cmd/idiom`\n"

	if err := os.WriteFile(draftPath, []byte(body), 0o644); err != nil {
		t.Fatalf("seed draft: %v", err)
	}

	var lintedBody string

	stubRefineLint(t, nil, func(path string, stdout, stderr io.Writer) (brieflint.TextRun, error) {
		blob, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read lint temp: %v", err)
		}

		lintedBody = string(blob)
		if strings.Contains(lintedBody, "```cpp") {
			return brieflint.TextRun{Exit: 1}, nil
		}

		return brieflint.TextRun{Exit: 0}, nil
	})

	var stderr bytes.Buffer

	code := finalizeRefineWithTitleAndOverlay(draftPath, outputPath, "overlay-lint", 1, "ship-as-is", root, refineModeAnchored, []string{"native/test_example.cpp"}, &stderr)

	if code == 0 {
		t.Fatalf("expected final overlay lint failure to force non-zero exit; stderr=%s", stderr.String())
	}

	if !strings.Contains(lintedBody, "Trap — example") || !strings.Contains(lintedBody, "```cpp") {
		t.Fatalf("final lint should inspect project overlay content, got:\n%s", lintedBody)
	}

	out, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}

	if !strings.HasPrefix(string(out), "> WARNING:") {
		t.Fatalf("expected warning artifact after final lint failure, got:\n%s", out)
	}

	if !strings.Contains(string(out), "reason=final-lint-failed") {
		t.Fatalf("expected provenance to record final-lint-failed, got:\n%s", out)
	}
}

// TestMarkdownDraftTemplateClarifiesTwoPaths locks in the
// 2026-05-11 bug fix to lanes/refine/draft-markdown.md: the
// markdown-deliverable mode has a known model-confusion source —
// the brief contains a `Deliverable Path` field (where plan-execute
// will eventually write the inline content) AND refine itself
// writes the brief to {{DRAFT_PATH}}. Without explicit
// disambiguation, models conflate the two and write either nothing
// or write content to the wrong path.
//
// The fix added a "Where you write THIS draft.md" section. This
// test verifies the substituted meta-draft contains both:
//
//   - The actual {{DRAFT_PATH}} substitution (refine's scratchpad).
//   - The "Deliverable Path" wording (the brief's internal field).
//   - Disambiguating language ("Two different files, two different
//     consumers") so the model sees the boundary explicitly.
//
// Removing the clarification or breaking the substitution breaks
// this test — forcing the change to be deliberate.
func TestMarkdownDraftTemplateClarifiesTwoPaths(t *testing.T) {
	root := t.TempDir()
	specPath := filepath.Join(root, "spec.md")

	if err := os.WriteFile(specPath, []byte("Create .planning/inventory.md.\n"), 0o644); err != nil {
		t.Fatalf("seed spec: %v", err)
	}

	metaPath := filepath.Join(root, "meta-draft.md")
	draftPath := filepath.Join(root, "draft.md")
	opts := Options{
		Title:       "two-path-test",
		ProjectRoot: root,
	}

	if err := writeRefineDraftMeta(metaPath, draftPath, opts, "Create a markdown inventory.", refineModeMarkdown, io.Discard); err != nil {
		t.Fatalf("writeRefineDraftMeta: %v", err)
	}

	body, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatalf("read meta: %v", err)
	}

	bodyStr := string(body)
	// DRAFT_PATH must be substituted; the workspace-relative form.
	expectedDraftPath := refineWorkspaceRel(root, draftPath)
	if !strings.Contains(bodyStr, expectedDraftPath) {
		t.Fatalf("expected substituted DRAFT_PATH %q in meta-draft, got:\n%s", expectedDraftPath, bodyStr)
	}

	// Gap 8 (2026-05-12) inverted the contract: model no longer
	// authors the `Deliverable Path` field. Instead, the template
	// instructs the model to emit the harness marker, and refine's
	// finalize step substitutes it. The lock-in shifts to: marker
	// present + harness-injection framing + scratchpad disambiguation
	// for the draft path.
	if !strings.Contains(bodyStr, "<!-- idiom:harness-deliverable-block -->") {
		t.Fatalf("expected harness deliverable marker in markdown template, got:\n%s", bodyStr)
	}

	// The template must explain that the harness owns the substitution.
	// Whitespace-flexible match — the prose may wrap "harness" and
	// "substitutes" across a newline.
	if !regexp.MustCompile(`(?i)harness\s+substitutes`).MatchString(bodyStr) {
		t.Fatalf("expected harness-substitution framing in template, got:\n%s", bodyStr)
	}

	if !strings.Contains(bodyStr, "scratchpad") {
		t.Fatalf("expected 'scratchpad' framing for refine's draft path, got:\n%s", bodyStr)
	}
}

// TestBriefLintJSONEnvelopeSchemaIsStable is a regression guard on
// the JSON field names downstream consumers (refine review/patch
// prompts, future tooling) depend on. Renaming or removing a field
// here is a breaking change and must be deliberate.
func TestBriefLintJSONEnvelopeSchemaIsStable(t *testing.T) {
	report := brieflint.Report{
		Brief: "/x.md",
		Exit:  1,
		Findings: []brieflint.Finding{
			{Severity: "BLOCK", Category: "anchor-specificity", Brief: "/x.md", Message: "test"},
		},
	}

	blob, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	wantSubstrings := []string{
		`"brief":"/x.md"`,
		`"exit":1`,
		`"findings":[`,
		`"severity":"BLOCK"`,
		`"category":"anchor-specificity"`,
		`"message":"test"`,
	}
	got := string(blob)

	for _, want := range wantSubstrings {
		if !strings.Contains(got, want) {
			t.Fatalf("schema field missing or renamed: expected %q in JSON, got:\n%s", want, got)
		}
	}
}

// TestRunLaneRefineOverlayAndOverrideTemplateInteract verifies that
// project overlay (§2.4) and project override templates (§2.2.1)
// compose correctly: a project with both a custom mode template AND
// a hard-prohibitions overlay should produce a final brief that
// uses the custom template body AND carries the prohibitions block.
func TestRunLaneRefineOverlayAndOverrideTemplateInteract(t *testing.T) {
	stubRefineLintPass(t)

	root := t.TempDir()
	refineOverride := filepath.Join(root, ".idiom", "prompts", "refine")
	briefOverlay := filepath.Join(root, ".idiom", "prompts", "brief")

	if err := os.MkdirAll(refineOverride, 0o755); err != nil {
		t.Fatalf("mkdir refine override: %v", err)
	}

	if err := os.MkdirAll(briefOverlay, 0o755); err != nil {
		t.Fatalf("mkdir brief overlay: %v", err)
	}

	overrideBody := "# {{TITLE}}\n\n## Custom-mode body marker\n\nSPEC: {{SPEC_BODY}}\n\nReturn the filled brief as the final answer for {{DRAFT_PATH}}.\n"
	if err := os.WriteFile(filepath.Join(refineOverride, "draft-notebook.md"), []byte(overrideBody), 0o644); err != nil {
		t.Fatalf("seed override: %v", err)
	}

	prohibitions := "## HARD PROHIBITIONS (project overlay marker)\n\n- Do not interact.\n"
	if err := os.WriteFile(filepath.Join(briefOverlay, "hard-prohibitions.md"), []byte(prohibitions), 0o644); err != nil {
		t.Fatalf("seed prohibitions: %v", err)
	}

	specPath := filepath.Join(root, "spec.md")
	if err := os.WriteFile(specPath, []byte("Notebook experiment.\n"), 0o644); err != nil {
		t.Fatalf("seed spec: %v", err)
	}

	opts := Options{
		SpecPath:    specPath,
		OutputPath:  filepath.Join(root, "brief.md"),
		Title:       "interact-test",
		MaxIters:    1,
		ProjectRoot: root,
		Mode:        "notebook",
	}
	dispatch, _ := stubDispatcher(t, []string{
		nonTrivialDraftBody("Drafted brief"),
		"# Review\n\n## Bugs found\n\nnone\n\n## Verdict\n\nship-as-is\n",
	})

	var stderr bytes.Buffer

	code := Run(opts, dispatch, &bytes.Buffer{}, &stderr)

	if code != 0 {
		t.Fatalf("expected exit 0 on clean ship-as-is, got %d; stderr=%s", code, stderr.String())
	}

	body, err := os.ReadFile(opts.OutputPath)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}

	// The custom mode body markers should appear in the iter-1
	// meta-draft (substituted from override).
	metaDraftPath := filepath.Join(refineStateDir(root, "interact-test"), "iter-1", "meta-draft.md")

	metaBody, err := os.ReadFile(metaDraftPath)
	if err != nil {
		t.Fatalf("read meta-draft: %v", err)
	}

	if !strings.Contains(string(metaBody), "Custom-mode body marker") {
		t.Fatalf("expected custom mode template in meta-draft, got:\n%s", metaBody)
	}

	// The overlay should appear in the FINAL output (post-finalize).
	if !strings.Contains(string(body), "HARD PROHIBITIONS (project overlay marker)") {
		t.Fatalf("expected hard-prohibitions overlay in output, got:\n%s", body)
	}
}

// TestRunLaneRefineFailsoftPreservesOverlay verifies the §2.4
// overlay survives the failsoft path: when refine exits non-zero
// after saving the best draft, the project overlay still gets
// applied so the orchestrator-inspectable artifact is the hardened
// version (with WARNING header on top).
func TestRunLaneRefineFailsoftPreservesOverlay(t *testing.T) {
	stubRefineLintPass(t)

	root := t.TempDir()
	overlayDir := filepath.Join(root, refineOverlayDir)

	if err := os.MkdirAll(overlayDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	prohibitions := "## HARD PROHIBITIONS (overlay)\n\n- Marker line for the assertion.\n"
	if err := os.WriteFile(filepath.Join(overlayDir, "hard-prohibitions.md"), []byte(prohibitions), 0o644); err != nil {
		t.Fatalf("seed prohibitions: %v", err)
	}

	specPath := filepath.Join(root, "spec.md")
	if err := os.WriteFile(specPath, []byte("Add a feature.\n"), 0o644); err != nil {
		t.Fatalf("seed spec: %v", err)
	}

	opts := Options{
		SpecPath:    specPath,
		OutputPath:  filepath.Join(root, "brief.md"),
		Title:       "failsoft-overlay",
		MaxIters:    2,
		ProjectRoot: root,
	}
	// Iter 1 ships, iter 2 has same bugs → no-improvement failsoft.
	dispatch, _ := stubDispatcher(t, []string{
		nonTrivialDraftBody("Draft v1"),
		"# Review\n\n## Bugs found\n\n1. **A bug**: x\n2. **B bug**: y\n\n## Verdict\n\nfix-bugs-then-ship\n",
		nonTrivialDraftBody("Draft v2"),
		"# Review\n\n## Bugs found\n\n1. **C bug**: z\n2. **D bug**: w\n\n## Verdict\n\nfix-bugs-then-ship\n",
	})

	var stderr bytes.Buffer

	code := Run(opts, dispatch, &bytes.Buffer{}, &stderr)

	if code == 0 {
		t.Fatalf("expected non-zero on no-improvement failsoft, got 0; stderr=%s", stderr.String())
	}

	body, err := os.ReadFile(opts.OutputPath)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}

	if !strings.HasPrefix(string(body), "> WARNING:") {
		t.Fatalf("expected WARNING header on failsoft output, got:\n%s", body)
	}

	if !strings.Contains(string(body), "HARD PROHIBITIONS (overlay)") {
		t.Fatalf("expected overlay applied even on failsoft path, got:\n%s", body)
	}
}

// TestLoadProjectTrapsWarnsOnOrphanAppliesToTag locks in §6.1 Gap 3
// (Colony loud-warning ship): a trap declaring an applies_to tag
// outside the canonical 4 emits a stderr warning naming the orphan
// tag, so silently-broken projects authors learn their trap will
// never fire (until atom-shapes.yaml is added, deferred).
func TestLoadProjectTrapsWarnsOnOrphanAppliesToTag(t *testing.T) {
	root := t.TempDir()
	overlayDir := filepath.Join(root, refineOverlayDir)
	trapsDir := filepath.Join(overlayDir, "traps")

	if err := os.MkdirAll(trapsDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	orphanTrap := "---\napplies_to: [save-load]\n---\n\n# Save-load trap (project-specific)\n"
	if err := os.WriteFile(filepath.Join(trapsDir, "save-load.md"), []byte(orphanTrap), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	canonicalTrap := "---\napplies_to: [test-code]\n---\n\n# Canonical trap (no warning expected)\n"
	if err := os.WriteFile(filepath.Join(trapsDir, "test-code.md"), []byte(canonicalTrap), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	var stderr bytes.Buffer

	// Classify against test-code only — save-load is orphan; canonical
	// trap (test-code) should fire and emit no warning.
	matched := loadProjectTraps(overlayDir, []string{refineTagTestCode}, &stderr)
	if len(matched) != 1 || matched[0].name != "test-code.md" {
		t.Fatalf("expected only the test-code trap to fire, got %d traps: %+v", len(matched), matched)
	}

	out := stderr.String()
	if !strings.Contains(out, "save-load.md") {
		t.Fatalf("expected warning to name the orphan trap file, got: %s", out)
	}

	if !strings.Contains(out, "save-load") {
		t.Fatalf("expected warning to name the orphan tag, got: %s", out)
	}

	if !strings.Contains(out, "atom-shapes.yaml") {
		t.Fatalf("expected warning to point at atom-shapes.yaml convention, got: %s", out)
	}

	if !strings.Contains(out, "Gap 3") {
		t.Fatalf("expected warning to reference Gap 3 in design doc for traceability, got: %s", out)
	}
}

// TestRunLaneRefineBareRepoSkipsOverlay confirms zero behavior
// change for projects without an .idiom/prompts/brief/ directory.
func TestRunLaneRefineBareRepoSkipsOverlay(t *testing.T) {
	stubRefineLintPass(t)

	root := t.TempDir() // bare — no overlay dir
	specPath := filepath.Join(root, "spec.md")

	if err := os.WriteFile(specPath, []byte("Add a feature.\n"), 0o644); err != nil {
		t.Fatalf("seed spec: %v", err)
	}

	opts := Options{
		SpecPath:    specPath,
		OutputPath:  filepath.Join(root, "brief.md"),
		Title:       "bare",
		MaxIters:    1,
		ProjectRoot: root,
	}
	dispatch, _ := stubDispatcher(t, []string{
		nonTrivialDraftBody("Drafted brief"),
		"# Review\n\n## Bugs found\n\nnone\n\n## Verdict\n\nship-as-is\n",
	})

	var stderr bytes.Buffer

	_ = Run(opts, dispatch, &bytes.Buffer{}, &stderr)

	if strings.Contains(stderr.String(), "appended hard-prohibitions") {
		t.Fatalf("bare repo should not log overlay activity, got: %s", stderr.String())
	}

	if strings.Contains(stderr.String(), "inlined trap") {
		t.Fatalf("bare repo should not inline traps, got: %s", stderr.String())
	}
}

// TestRefineDraftIsTrivialRejectsSmallAndStructurelessBodies locks
// in the docs/refine-end-to-end-loop.md §2.1 honest-exit-codes
// behavior. A draft file that's empty, under the byte floor, or
// has no markdown section heading must be classified trivial so
// the wrapper exits non-zero rather than silently shipping nothing.
// TestResolveRefineDraftTemplateProjectOverrideWins exercises the
// §2.2.1 resolution chain: when a project drops a well-formed draft
// template at .idiom/prompts/refine/draft-<mode>.md, it is loaded
// instead of the embedded canonical template. The override body is
// returned verbatim (only substitutions happen later).
func TestResolveRefineDraftTemplateProjectOverrideWins(t *testing.T) {
	root := t.TempDir()
	overrideDir := filepath.Join(root, ".idiom", "prompts", "refine")

	if err := os.MkdirAll(overrideDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	overrideBody := "# {{TITLE}}\n\n## Custom mode body\n\n{{SPEC_BODY}}\n\nReturn the final answer for {{DRAFT_PATH}}.\n"
	overridePath := filepath.Join(overrideDir, "draft-notebook.md")

	if err := os.WriteFile(overridePath, []byte(overrideBody), 0o644); err != nil {
		t.Fatalf("seed override: %v", err)
	}

	tmpl, source, err := resolveRefineDraftTemplate(root, "notebook")
	if err != nil {
		t.Fatalf("resolveRefineDraftTemplate: %v", err)
	}

	if tmpl != overrideBody {
		t.Fatalf("expected override body, got:\n%s", tmpl)
	}

	if !strings.HasPrefix(source, "project-override:") {
		t.Fatalf("expected project-override source tag, got %q", source)
	}
}

// TestResolveRefineDraftTemplateFallsBackToEmbedded confirms the
// embedded canonical templates serve when no project override exists
// for a canonical mode. Bare repos see no behavior change.
func TestResolveRefineDraftTemplateFallsBackToEmbedded(t *testing.T) {
	root := t.TempDir() // no overrides

	tmpl, source, err := resolveRefineDraftTemplate(root, refineModeAnchored)
	if err != nil {
		t.Fatalf("resolveRefineDraftTemplate anchored: %v", err)
	}

	if source != "embedded:anchored" {
		t.Fatalf("expected embedded:anchored source, got %q", source)
	}

	if !strings.Contains(tmpl, "Verified Current State") {
		t.Fatalf("expected anchored template body, got:\n%s", tmpl)
	}

	tmpl, source, err = resolveRefineDraftTemplate(root, refineModeMarkdown)
	if err != nil {
		t.Fatalf("resolveRefineDraftTemplate markdown: %v", err)
	}

	if source != "embedded:markdown" {
		t.Fatalf("expected embedded:markdown source, got %q", source)
	}

	if !strings.Contains(tmpl, "Inline content") {
		t.Fatalf("expected markdown template body, got:\n%s", tmpl)
	}
}

// TestResolveRefineDraftTemplateUnknownModeErrors confirms a
// non-canonical mode with no override yields an actionable error
// pointing at the override convention path.
func TestResolveRefineDraftTemplateUnknownModeErrors(t *testing.T) {
	root := t.TempDir()

	_, _, err := resolveRefineDraftTemplate(root, "phantom")
	if err == nil {
		t.Fatalf("expected error for unknown mode, got nil")
	}

	expectedPath := filepath.Join(root, ".idiom/prompts/refine/draft-phantom.md")
	if !strings.Contains(err.Error(), expectedPath) {
		t.Fatalf("expected error to mention override path %q, got: %v", expectedPath, err)
	}
}

// TestResolveRefineDraftTemplateMalformedOverrideRejected confirms
// the §2.2.1 safety lint: a project override missing required
// placeholders or section headings fails at load time, not after a
// wasted draft dispatch.
func TestResolveRefineDraftTemplateMalformedOverrideRejected(t *testing.T) {
	root := t.TempDir()
	overrideDir := filepath.Join(root, ".idiom", "prompts", "refine")

	if err := os.MkdirAll(overrideDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	cases := []struct {
		name string
		body string
		want string // substring expected in error
	}{
		{
			name: "missing-draft-path",
			body: "# {{TITLE}}\n\n## Body\n\n{{SPEC_BODY}}\n\nReturn final answer.\n",
			want: "{{DRAFT_PATH}}",
		},
		{
			name: "missing-spec-body",
			body: "# {{TITLE}}\n\n## Body\n\nReturn final answer for {{DRAFT_PATH}}.\n",
			want: "{{SPEC_BODY}}",
		},
		{
			name: "no-section-heading",
			body: "{{TITLE}} {{SPEC_BODY}} {{DRAFT_PATH}} — return final answer.\n",
			want: "section heading",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(overrideDir, "draft-"+tc.name+".md")
			if err := os.WriteFile(path, []byte(tc.body), 0o644); err != nil {
				t.Fatalf("seed: %v", err)
			}

			_, _, err := resolveRefineDraftTemplate(root, tc.name)
			if err == nil {
				t.Fatalf("expected validation error for %s, got nil", tc.name)
			}

			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected error mentioning %q, got: %v", tc.want, err)
			}
		})
	}
}

// TestEnsureRefineDraftArtifactClosureAppendsWhenMissing locks in
// the artifact-owned draft contract: project authors don't have to
// remember to include final-answer boilerplate in their override
// templates. If missing, idiom auto-appends a canonical artifact
// closure. Templates that already include their own final-answer
// directive are left untouched.
func TestEnsureRefineDraftArtifactClosureAppendsWhenMissing(t *testing.T) {
	tests := []struct {
		name           string
		body           string
		wantAppended   bool
		wantUntouched  bool
		mustNotContain string
	}{
		{
			name:         "body-without-artifact-closeout",
			body:         "# {{TITLE}}\n\n## Body\n\n{{SPEC_BODY}}\n\nDescribe deliverable at {{DRAFT_PATH}}.\n",
			wantAppended: true,
		},
		{
			name:          "body-with-explicit-final-answer",
			body:          "# {{TITLE}}\n\n## Body\n\n{{SPEC_BODY}}\n\nReturn the final answer for {{DRAFT_PATH}}.\n",
			wantUntouched: true,
		},
		{
			name:          "body-with-artifact-closeout-section",
			body:          "# {{TITLE}}\n\n## Body\n\n{{SPEC_BODY}}\n\n## Artifact Closeout\n\nReturn only the brief for {{DRAFT_PATH}}.\n",
			wantUntouched: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ensureRefineDraftArtifactClosure(tc.body, nil, "test")
			if tc.wantUntouched && got != tc.body {
				t.Fatalf("expected body untouched, got delta:\n%s", got)
			}

			if tc.wantAppended {
				if got == tc.body {
					t.Fatalf("expected closure appended, body unchanged")
				}

				if !strings.Contains(got, "## Artifact Closeout") {
					t.Fatalf("expected canonical artifact closure heading, got:\n%s", got)
				}

				if !strings.Contains(got, "final answer") {
					t.Fatalf("expected appended closure to mention final answer, got:\n%s", got)
				}

				if !strings.Contains(got, "{{DRAFT_PATH}}") {
					t.Fatalf("expected appended closure to reference {{DRAFT_PATH}} for later substitution, got:\n%s", got)
				}

				if strings.Contains(got, "write_file") {
					t.Fatalf("artifact closure must not mention write_file, got:\n%s", got)
				}
			}
		})
	}
}

func TestRefineMetaTemplatesDoNotSuggestScratchpadShellChecks(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		pathToken string
		artifact  bool
	}{
		{name: "draft", body: refineDraftTemplate, pathToken: "{{DRAFT_PATH}}", artifact: true},
		{name: "draft-markdown", body: refineDraftMarkdownTemplate, pathToken: "{{DRAFT_PATH}}", artifact: true},
		{name: "review", body: refineReviewTemplate, pathToken: "{{REVIEW_PATH}}", artifact: true},
		{name: "patch", body: refinePatchTemplate, pathToken: "{{NEW_DRAFT_PATH}}", artifact: true},
		{name: "override-closure", body: refineCanonicalArtifactClosure, pathToken: "{{DRAFT_PATH}}", artifact: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			scratchpadCheck := "test -f " + tc.pathToken
			if strings.Contains(tc.body, scratchpadCheck) {
				t.Fatalf("template must not suggest shell-checking scratchpad writes with %q:\n%s", scratchpadCheck, tc.body)
			}

			if tc.artifact {
				if strings.Contains(tc.body, "write_file") {
					t.Fatalf("artifact draft template must not ask for write_file, got:\n%s", tc.body)
				}

				if !strings.Contains(tc.body, "The artifact command writing") {
					t.Fatalf("artifact template should name artifact command completion proof, got:\n%s", tc.body)
				}
			} else if !strings.Contains(tc.body, "The successful `write_file` result is the completion proof") {
				t.Fatalf("template should tell the model write_file is the completion proof, got:\n%s", tc.body)
			}
		})
	}
}

func TestRefineDraftTemplatesUseEvidenceContractInsteadOfBudget(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "draft", body: refineDraftTemplate},
		{name: "draft-markdown", body: refineDraftMarkdownTemplate},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for _, want := range []string{"## Draft phase evidence contract", "Use the embedded spec", "Source verification pending:", "Stay on the draft deliverable"} {
				if !strings.Contains(tc.body, want) {
					t.Fatalf("draft template should contain %q, got:\n%s", want, tc.body)
				}
			}

			if tc.name == "draft" {
				for _, want := range []string{"source-satisfied - do not dispatch", "old_text`/`new_text"} {
					if !strings.Contains(tc.body, want) {
						t.Fatalf("anchored draft template should contain %q, got:\n%s", want, tc.body)
					}
				}
			}

			for _, forbidden := range []string{"## Draft phase tool budget", "Zero source reads", "Zero searches", "Zero code-navigation calls", "Do not read files. Do not search."} {
				if strings.Contains(tc.body, forbidden) {
					t.Fatalf("draft template should not contain budget-shaped wording %q:\n%s", forbidden, tc.body)
				}
			}
		})
	}
}

func TestRefineReviewTemplateClosesSourceUncertaintyIntoReview(t *testing.T) {
	for _, want := range []string{
		"## Review evidence discipline",
		"Use the pre-loaded drafted brief and lint output above",
		"Do not perform source reads, discovery queries, broad orientation",
		"record the uncertainty as a bug or OK caveat",
		"A bounded `Source verification\n  pending:` note is not a bug by itself",
		"Put bounded `Source verification pending:` entries in OK, not Bugs",
		"## What To Record As Uncertainty",
		"return the review now",
	} {
		if !strings.Contains(refineReviewTemplate, want) {
			t.Fatalf("review template should contain %q, got:\n%s", want, refineReviewTemplate)
		}
	}

	for _, forbidden := range []string{
		"## Hard tool budget",
		"At most ONE additional `read_file`",
		"Do NOT do grep / search / code_navigate",
		"## What to spot-check (gap-driven only)",
		"Prefer context/language/project discovery tools before raw source",
	} {
		if strings.Contains(refineReviewTemplate, forbidden) {
			t.Fatalf("review template should not contain obsolete source-discovery rule %q:\n%s", forbidden, refineReviewTemplate)
		}
	}
}

func TestRefinePatchTemplateClosesSourceUncertaintyIntoDraft(t *testing.T) {
	for _, want := range []string{
		"## Source evidence closure",
		"not a source discovery",
		"do not reopen source discovery here",
		"Source verification pending:",
		"## Scope Correction Required",
		"Use the pre-loaded scratchpad and source evidence above",
		"Return only the full corrected brief",
	} {
		if !strings.Contains(refinePatchTemplate, want) {
			t.Fatalf("patch template should contain %q, got:\n%s", want, refinePatchTemplate)
		}
	}

	for _, forbidden := range []string{
		"Maximum 4 additional source reads total",
		"No searches, shell commands, code-navigation tools",
		"file already listed in the draft/spec",
		"## Gap-driven anchor refresh",
		"Prefer context/language/project discovery tools",
	} {
		if strings.Contains(refinePatchTemplate, forbidden) {
			t.Fatalf("patch template should not contain obsolete refresh rule %q:\n%s", forbidden, refinePatchTemplate)
		}
	}
}

// TestRunLaneRefineAutoAppendsArtifactClosureToOverride
// verifies the auto-append behavior is wired end-to-end: a project
// override that omits artifact closeout in its body produces a meta-draft
// containing the canonical artifact closure with the destination path
// substituted in.
func TestRunLaneRefineAutoAppendsArtifactClosureToOverride(t *testing.T) {
	stubRefineLintPass(t)

	root := t.TempDir()
	overrideDir := filepath.Join(root, ".idiom", "prompts", "refine")

	if err := os.MkdirAll(overrideDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Note: no artifact closeout. Author focused on the body.
	overrideBody := "# {{TITLE}}\n\n## Notebook deliverable\n\nSPEC: {{SPEC_BODY}}\n\nDescribe the experiment at {{DRAFT_PATH}}.\n"
	if err := os.WriteFile(filepath.Join(overrideDir, "draft-notebook.md"), []byte(overrideBody), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	specPath := filepath.Join(root, "spec.md")
	if err := os.WriteFile(specPath, []byte("Sweep hyperparams.\n"), 0o644); err != nil {
		t.Fatalf("seed spec: %v", err)
	}

	opts := Options{
		SpecPath:    specPath,
		OutputPath:  filepath.Join(root, "brief.md"),
		Title:       "closure-test",
		MaxIters:    1,
		ProjectRoot: root,
		Mode:        "notebook",
	}
	dispatch, _ := stubDispatcher(t, []string{
		nonTrivialDraftBody("Drafted brief"),
		"# Review\n\n## Bugs found\n\nnone\n\n## Verdict\n\nship-as-is\n",
	})

	var stderr bytes.Buffer

	_ = Run(opts, dispatch, &bytes.Buffer{}, &stderr)

	if !strings.Contains(stderr.String(), "no artifact closeout; appending canonical artifact closure") {
		t.Fatalf("expected stderr to log auto-append, got: %s", stderr.String())
	}

	metaDraftPath := filepath.Join(refineStateDir(root, "closure-test"), "iter-1", "meta-draft.md")

	body, err := os.ReadFile(metaDraftPath)
	if err != nil {
		t.Fatalf("read meta-draft: %v", err)
	}

	if !strings.Contains(string(body), "## Artifact Closeout") {
		t.Fatalf("expected canonical artifact closure in substituted meta-draft, got:\n%s", body)
	}

	if strings.Contains(string(body), "write_file") {
		t.Fatalf("artifact meta-draft must not mention write_file, got:\n%s", body)
	}

	// The {{DRAFT_PATH}} in the closure must have been substituted
	// with a real path during the standard substitution pipeline.
	if strings.Contains(string(body), "{{DRAFT_PATH}}") {
		t.Fatalf("expected {{DRAFT_PATH}} to be substituted in the appended closure, got:\n%s", body)
	}
}

// TestRunLaneRefineUsesProjectOverrideTemplate ensures the resolution
// chain is exercised end-to-end: a project with a custom mode
// override at .idiom/prompts/refine/draft-notebook.md, invoked with
// --mode notebook, produces a meta-draft built from the override.
func TestRunLaneRefineUsesProjectOverrideTemplate(t *testing.T) {
	stubRefineLintPass(t)

	root := t.TempDir()
	overrideDir := filepath.Join(root, ".idiom", "prompts", "refine")

	if err := os.MkdirAll(overrideDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	overrideBody := "# {{TITLE}}\n\n## Notebook deliverable\n\nSPEC: {{SPEC_BODY}}\n\nReturn the final answer for {{DRAFT_PATH}} with the experiment.\n"
	overridePath := filepath.Join(overrideDir, "draft-notebook.md")

	if err := os.WriteFile(overridePath, []byte(overrideBody), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	specPath := filepath.Join(root, "spec.md")
	if err := os.WriteFile(specPath, []byte("Sweep hyperparameters and pick best F1.\n"), 0o644); err != nil {
		t.Fatalf("seed spec: %v", err)
	}

	opts := Options{
		SpecPath:    specPath,
		OutputPath:  filepath.Join(root, "brief.md"),
		Title:       "override-test",
		MaxIters:    1,
		ProjectRoot: root,
		Mode:        "notebook",
	}
	dispatch, _ := stubDispatcher(t, []string{
		nonTrivialDraftBody("Drafted brief"),
		"# Review\n\n## Bugs found\n\nnone\n\n## Verdict\n\nship-as-is\n",
	})

	var stderr bytes.Buffer

	_ = Run(opts, dispatch, &bytes.Buffer{}, &stderr)

	if !strings.Contains(stderr.String(), "project-override:") {
		t.Fatalf("expected stderr to log project-override source, got: %s", stderr.String())
	}

	metaDraftPath := filepath.Join(refineStateDir(root, "override-test"), "iter-1", "meta-draft.md")

	body, err := os.ReadFile(metaDraftPath)
	if err != nil {
		t.Fatalf("read meta-draft: %v", err)
	}

	if !strings.Contains(string(body), "## Notebook deliverable") {
		t.Fatalf("expected override template body in meta-draft, got:\n%s", body)
	}

	if !strings.Contains(string(body), "SPEC: > Sweep hyperparameters") {
		t.Fatalf("expected substitutions to happen, got:\n%s", body)
	}
}

// TestRefineDetectModeClassifiesSpecShape locks in the §2.2
// auto-detection heuristic: if a spec mentions any source-class
// extension, route anchored. If it ONLY mentions markdown-class
// extensions, route markdown. Mixed shapes and pathless specs
// fall back to anchored (the safer default).
func TestRefineDetectModeClassifiesSpecShape(t *testing.T) {
	cases := []struct {
		name string
		spec string
		want string
	}{
		{"pure-markdown", "Create .planning/foo.md with the inventory of postflight checks.", refineModeMarkdown},
		{"docs-folder", "Add docs/decision.md with the trade-off table.", refineModeMarkdown},
		{"go-source", "Update cmd/idiom/lane.go to add a --top-briefs flag.", refineModeAnchored},
		{"mixed", "Update README.md and add cmd/idiom/foo.go alongside it.", refineModeAnchored},
		{"cpp", "Modify src/save_fields.cpp to handle the new schema.", refineModeAnchored},
		{"pathless", "Make the system faster.", refineModeAnchored},
		{"json-only", "Generate config/settings.json with the new keys.", refineModeMarkdown},
		// Colony Q2 fix: anchor-able sources beyond compiled langs.
		{"shell-anchor", "Create .planning/postflight-inventory.md citing lane_postflight.sh:296.", refineModeAnchored},
		{"sql-anchor", "Document the schema changes from db/migrations/0042_add_users.sql.", refineModeAnchored},
		{"proto-anchor", "Add docs/api-changes.md tracking edits in api/service.proto.", refineModeAnchored},
		{"terraform-anchor", "Update README.md to reflect the new infra/main.tf module.", refineModeAnchored},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := pathparse.DetectMode(tc.spec)
			if got != tc.want {
				t.Fatalf("pathparse.DetectMode(%q) = %q, want %q", tc.spec, got, tc.want)
			}
		})
	}
}

// TestRunLaneRefineRoutesMarkdownSpecToMarkdownTemplate confirms a
// spec that asks for a NEW markdown file routes through the
// markdown-deliverable template (containing "## Deliverable" +
// "## Inline content" headings) rather than the anchored template
// (containing "## Verified Current State" + "## Required Behavior").
func TestRunLaneRefineRoutesMarkdownSpecToMarkdownTemplate(t *testing.T) {
	stubRefineLintPass(t)

	root := t.TempDir()
	specPath := filepath.Join(root, "spec.md")

	if err := os.WriteFile(specPath, []byte("Create .planning/inventory.md with all postflight checks.\n"), 0o644); err != nil {
		t.Fatalf("seed spec: %v", err)
	}

	opts := Options{
		SpecPath:    specPath,
		OutputPath:  filepath.Join(root, "brief.md"),
		Title:       "markdown-spec",
		MaxIters:    1,
		ProjectRoot: root,
	}
	dispatch, _ := stubDispatcher(t, []string{
		nonTrivialDraftBody("Drafted brief"),
		"# Review\n\n## Bugs found\n\nnone\n\n## Verdict\n\nship-as-is\n",
	})

	var stderr bytes.Buffer

	_ = Run(opts, dispatch, &bytes.Buffer{}, &stderr)

	if !strings.Contains(stderr.String(), "mode=markdown") {
		t.Fatalf("expected stderr to log markdown mode detection, got: %s", stderr.String())
	}

	metaDraftPath := filepath.Join(refineStateDir(root, "markdown-spec"), "iter-1", "meta-draft.md")

	body, err := os.ReadFile(metaDraftPath)
	if err != nil {
		t.Fatalf("read meta-draft: %v", err)
	}

	if !strings.Contains(string(body), "## Deliverable") || !strings.Contains(string(body), "## Inline content") {
		t.Fatalf("expected markdown-deliverable template headings in meta-draft, got:\n%s", body)
	}

	if strings.Contains(string(body), "## Verified Current State") {
		t.Fatalf("markdown mode should NOT include anchored-mode headings, got:\n%s", body)
	}
}

// TestRunLaneRefineModeOverrideForcesAnchored confirms the
// --mode flag overrides the auto-detected mode. A markdown-shaped
// spec with opts.Mode="anchored" routes to the anchored template.
func TestRunLaneRefineModeOverrideForcesAnchored(t *testing.T) {
	stubRefineLintPass(t)

	root := t.TempDir()
	specPath := filepath.Join(root, "spec.md")

	if err := os.WriteFile(specPath, []byte("Create .planning/inventory.md.\n"), 0o644); err != nil {
		t.Fatalf("seed spec: %v", err)
	}

	opts := Options{
		SpecPath:    specPath,
		OutputPath:  filepath.Join(root, "brief.md"),
		Title:       "override-anchored",
		MaxIters:    1,
		ProjectRoot: root,
		Mode:        refineModeAnchored,
	}
	dispatch, _ := stubDispatcher(t, []string{
		nonTrivialDraftBody("Drafted brief"),
		"# Review\n\n## Bugs found\n\nnone\n\n## Verdict\n\nship-as-is\n",
	})

	var stderr bytes.Buffer

	_ = Run(opts, dispatch, &bytes.Buffer{}, &stderr)

	metaDraftPath := filepath.Join(refineStateDir(root, "override-anchored"), "iter-1", "meta-draft.md")

	body, err := os.ReadFile(metaDraftPath)
	if err != nil {
		t.Fatalf("read meta-draft: %v", err)
	}

	if !strings.Contains(string(body), "## Verified Current State") {
		t.Fatalf("expected anchored template (forced via opts.Mode), got:\n%s", body)
	}
}

// TestRunLaneRefineInlinesLintOutputIntoReviewMeta locks in §2.3 of
// docs/refine-end-to-end-loop.md: the refine wrapper runs idiom
// brief lint between draft and review, captures the output, and
// inlines it into the review meta-brief via the {{LINT_OUTPUT}}
// placeholder. The reviewer then has lint findings as preloaded
// evidence and the verdict rules require zero BLOCKs for
// ship-as-is — convergence on a lint-passing brief by construction.
func TestRunLaneRefineInlinesLintOutputIntoReviewMeta(t *testing.T) {
	stubRefineLint(t, func(path string) (brieflint.Report, error) {
		return refineLintReport(path, 1,
			brieflint.Finding{Severity: "BLOCK", Category: "anchor-specificity", Message: "anchor at foo.go:42 doesn't match source"},
			brieflint.Finding{Severity: "WARN", Category: "edit-shape", Message: "append-to-end-of-file edit is wedge-prone"},
		), nil
	}, nil)

	root := t.TempDir()
	specPath := filepath.Join(root, "spec.md")

	if err := os.WriteFile(specPath, []byte("Add a feature.\n"), 0o644); err != nil {
		t.Fatalf("seed spec: %v", err)
	}

	opts := Options{
		SpecPath:    specPath,
		OutputPath:  filepath.Join(root, "brief.md"),
		Title:       "lint-preload",
		MaxIters:    1, // ship-as-is on iter 1 so we don't loop
		ProjectRoot: root,
	}
	dispatch, _ := stubDispatcher(t, []string{
		nonTrivialDraftBody("Drafted brief"),
		"# Review\n\n## Bugs found\n\nnone\n\n## Lint findings\n\nBLOCK: anchor at foo.go:42 doesn't match source\n\n## OK\n\n- nice\n\n## Verdict\n\nship-as-is\n",
	})

	var stderr bytes.Buffer

	_ = Run(opts, dispatch, &bytes.Buffer{}, &stderr)

	metaReviewPath := filepath.Join(refineStateDir(root, "lint-preload"), "iter-1", "meta-review.md")

	body, err := os.ReadFile(metaReviewPath)
	if err != nil {
		t.Fatalf("read meta-review: %v", err)
	}

	if !strings.Contains(string(body), "[BLOCK] (anchor-specificity) anchor at foo.go:42 doesn't match source") {
		t.Fatalf("expected lint BLOCK to be inlined in review meta, got:\n%s", body)
	}

	if !strings.Contains(string(body), "Pre-loaded lint findings") {
		t.Fatalf("expected 'Pre-loaded lint findings' section in review meta, got:\n%s", body)
	}

	metricsPath := filepath.Join(refineStateDir(root, "lint-preload"), "iter-1", "metrics.json")

	metricsBody, err := os.ReadFile(metricsPath)
	if err != nil {
		t.Fatalf("read metrics: %v", err)
	}

	var m refineIterMetrics

	if err := json.Unmarshal(metricsBody, &m); err != nil {
		t.Fatalf("unmarshal metrics: %v\n%s", err, metricsBody)
	}

	if m.LintExit != 1 {
		t.Fatalf("expected metrics.lint_exit=1, got %d", m.LintExit)
	}
}

func TestRunLaneRefinePreloadsFinalShapeLintFindings(t *testing.T) {
	var lintedCandidate string

	stubRefineLint(t, func(path string) (brieflint.Report, error) {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read lint input: %v", err)
		}

		lintedCandidate = string(body)
		if strings.Contains(lintedCandidate, "native/foo.cpp") && !strings.Contains(lintedCandidate, refineDeliverableMarker) {
			return refineLintReport(path, 1,
				brieflint.Finding{Severity: "BLOCK", Category: "exact-anchor", Message: "exact-anchor specificity failed for native/foo.cpp"},
			), nil
		}

		return refineLintReport(path, 0), nil
	}, nil)

	root := t.TempDir()
	specPath := filepath.Join(root, "spec.md")
	spec := "# Spec\n\n## Files To Modify\n\n| File | Change |\n|---|---|\n| `native/foo.cpp` | Modify the feature. |\n"

	if err := os.WriteFile(specPath, []byte(spec), 0o644); err != nil {
		t.Fatalf("seed spec: %v", err)
	}

	draft := "# Drafted brief\n\n## Problem\n\nFinal-shape lint must see the harness-injected deliverable block.\n\n" +
		refineDeliverableMarker + "\n\n## Verified Current State\n\nSource verification pending.\n\n" +
		"## Required Behavior\n\n1. Modify the feature using source anchors once verified.\n\n" +
		"## Acceptance\n\n- `go test ./...`\n"
	opts := Options{
		SpecPath:    specPath,
		OutputPath:  filepath.Join(root, "brief.md"),
		Title:       "final-shape-lint",
		MaxIters:    1,
		ProjectRoot: root,
	}
	dispatch, _ := stubDispatcher(t, []string{
		draft,
		"# Review\n\n## Bugs found\n\n1. **Lint BLOCK**: fix it.\n\n## Lint findings\n\nBLOCK present\n\n## OK\n\n- shape reviewed\n\n## Verdict\n\nfix-bugs-then-ship\n",
	})

	_ = Run(opts, dispatch, &bytes.Buffer{}, &bytes.Buffer{})

	if !strings.Contains(lintedCandidate, "native/foo.cpp") {
		t.Fatalf("lint should run against final-shape candidate with injected target, got:\n%s", lintedCandidate)
	}

	if strings.Contains(lintedCandidate, refineDeliverableMarker) {
		t.Fatalf("lint candidate should have marker substituted before lint, got:\n%s", lintedCandidate)
	}

	metaReviewPath := filepath.Join(refineStateDir(root, "final-shape-lint"), "iter-1", "meta-review.md")

	body, err := os.ReadFile(metaReviewPath)
	if err != nil {
		t.Fatalf("read meta-review: %v", err)
	}

	if !strings.Contains(string(body), "exact-anchor specificity failed for native/foo.cpp") {
		t.Fatalf("expected final-shape lint BLOCK in review meta, got:\n%s", body)
	}
}

// TestRunLaneRefineHandlesCleanLintGracefully checks the empty-lint
// path: when lint exits 0 with no findings, the {{LINT_OUTPUT}}
// substitution carries an explicit clean-lint note so the reviewer
// doesn't silently treat empty as "clean by default".
func TestRunLaneRefineHandlesCleanLintGracefully(t *testing.T) {
	stubRefineLintPass(t)

	root := t.TempDir()
	specPath := filepath.Join(root, "spec.md")

	if err := os.WriteFile(specPath, []byte("Add a feature.\n"), 0o644); err != nil {
		t.Fatalf("seed spec: %v", err)
	}

	opts := Options{
		SpecPath:    specPath,
		OutputPath:  filepath.Join(root, "brief.md"),
		Title:       "lint-clean",
		MaxIters:    1,
		ProjectRoot: root,
	}
	dispatch, _ := stubDispatcher(t, []string{
		nonTrivialDraftBody("Drafted brief"),
		"# Review\n\n## Bugs found\n\nnone\n\n## Verdict\n\nship-as-is\n",
	})

	var stderr bytes.Buffer

	code := Run(opts, dispatch, &bytes.Buffer{}, &stderr)

	if code != 0 {
		t.Fatalf("expected exit 0 on clean lint + ship-as-is, got %d; stderr=%s", code, stderr.String())
	}

	metaReviewPath := filepath.Join(refineStateDir(root, "lint-clean"), "iter-1", "meta-review.md")

	body, err := os.ReadFile(metaReviewPath)
	if err != nil {
		t.Fatalf("read meta-review: %v", err)
	}

	if !strings.Contains(string(body), "no BLOCK/WARN findings; lint pass clean") {
		t.Fatalf("expected clean lint note in review meta, got:\n%s", body)
	}
}

func TestRefineDraftIsTrivialRejectsSmallAndStructurelessBodies(t *testing.T) {
	root := t.TempDir()
	cases := []struct {
		name        string
		body        string
		wantTrivial bool
	}{
		{"empty", "", true},
		{"whitespace-only", "   \n\t\n", true},
		{"single-line", "ERROR: lane wedged", true},
		{"no-section-heading", strings.Repeat("padding line above the size floor.\n", 12), true},
		{"valid-draft", nonTrivialDraftBody("test"), false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(root, tc.name+".md")
			if err := os.WriteFile(path, []byte(tc.body), 0o644); err != nil {
				t.Fatalf("seed: %v", err)
			}

			got, reason := refineDraftIsTrivial(path)
			if got != tc.wantTrivial {
				t.Fatalf("refineDraftIsTrivial(%q) = (%v, %q), want trivial=%v", tc.name, got, reason, tc.wantTrivial)
			}
		})
	}
}

// TestRunLaneRefineFailsOnTrivialDraft locks in that a dispatch
// which exits 0 but writes a trivially-empty draft.md (the silent-
// failure shape from FEATURE_REQUEST_END_TO_END_QWEN_PARITY.md gap
// 1) propagates as a non-zero wrapper exit. Without this guard,
// orchestrators saw exit-0 success with no deliverable produced.
func TestRunLaneRefineFailsOnTrivialDraft(t *testing.T) {
	root := t.TempDir()
	specPath := filepath.Join(root, "spec.md")

	if err := os.WriteFile(specPath, []byte("Add a feature.\n"), 0o644); err != nil {
		t.Fatalf("seed spec: %v", err)
	}

	opts := Options{
		SpecPath:    specPath,
		OutputPath:  filepath.Join(root, "brief.md"),
		Title:       "trivial-draft",
		MaxIters:    3,
		ProjectRoot: root,
	}
	// Dispatch exits 0 but writes only a 20-byte fragment to the
	// draft path — the silent-failure shape.
	dispatch := DispatchFunc(func(metaBriefPath string, stdout, stderr io.Writer) int {
		iterDir := filepath.Dir(metaBriefPath)

		_ = os.WriteFile(filepath.Join(iterDir, "draft.md"), []byte("# stub\n"), 0o644)

		return 0
	})

	var stderr bytes.Buffer

	code := Run(opts, dispatch, &bytes.Buffer{}, &stderr)

	if code == 0 {
		t.Fatalf("expected non-zero exit on trivially-empty draft, got 0; stderr=%s", stderr.String())
	}

	if !strings.Contains(stderr.String(), "trivially-empty draft") {
		t.Fatalf("expected trivially-empty-draft message in stderr, got: %s", stderr.String())
	}

	if _, err := os.Stat(opts.OutputPath); err == nil {
		t.Fatalf("expected no output brief on trivial-draft failure, but %s exists", opts.OutputPath)
	}
}

func TestRefineSanitizeTitleStripsUnsafeChars(t *testing.T) {
	cases := map[string]string{
		"task-64":  "task-64",
		"my atom!": "my-atom",
		"a/b\\c":   "a-b-c",
		"  trim  ": "trim",
		"":         "untitled",
		"------":   "untitled",
	}
	for in, want := range cases {
		got := refineSanitizeTitle(in)
		if got != want {
			t.Errorf("refineSanitizeTitle(%q) = %q, want %q", in, got, want)
		}
	}
}
