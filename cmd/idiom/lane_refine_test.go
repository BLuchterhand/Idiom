package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
		"shape (heading + Problem + Files + Acceptance) without trying\n" +
		"to be production-quality content.\n\n" +
		"## Files To Modify\n\n| File | Change |\n|------|--------|\n" +
		"| `foo.go` | MODIFY: stub |\n\n## Acceptance\n\n- run tests\n"
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

func stubDispatcher(t *testing.T, responses []string) (refineDispatchFn, *int) {
	t.Helper()
	calls := 0
	return func(metaBriefPath string, stdout, stderr io.Writer) int {
		body, err := os.ReadFile(metaBriefPath)
		if err != nil {
			t.Fatalf("stub: read meta %s: %v", metaBriefPath, err)
		}
		// Find the output path the meta-brief targets. Prefer the
		// path on the row immediately after "## Files To Modify"
		// (the lane's authoritative output declaration). Fall back
		// to the first .md path mention not equal to the meta brief.
		scanText := stripFencedBlocksForStub(string(body))
		var outPath string
		inFilesToModify := false
		for _, line := range strings.Split(scanText, "\n") {
			trimmed := strings.TrimSpace(line)
			lower := strings.ToLower(trimmed)
			if strings.HasPrefix(lower, "## files to modify") {
				inFilesToModify = true
				continue
			}
			if inFilesToModify && strings.HasPrefix(trimmed, "## ") {
				inFilesToModify = false
			}
			if !strings.Contains(trimmed, ".md") {
				continue
			}
			start := strings.Index(trimmed, "`")
			if start < 0 {
				continue
			}
			end := strings.Index(trimmed[start+1:], "`")
			if end < 0 {
				continue
			}
			candidate := trimmed[start+1 : start+1+end]
			if !strings.HasSuffix(candidate, ".md") {
				continue
			}
			if candidate == metaBriefPath || strings.HasSuffix(candidate, "/"+filepath.Base(metaBriefPath)) {
				continue
			}
			outPath = candidate
			if inFilesToModify {
				break // authoritative — stop looking
			}
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
		{"/Users/ben/proj", "/Users/ben/proj/.idiom/refine/atom/iter-1/draft.md", ".idiom/refine/atom/iter-1/draft.md"},
		{"/Users/ben/proj", "/Users/ben/proj/cmd/idiom/lane.go", "cmd/idiom/lane.go"},
		// Different ancestor — refuse to coerce, return input.
		{"/Users/ben/proj", "/elsewhere/file.md", "/elsewhere/file.md"},
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
		"/Users/ben/proj/.idiom/refine/my-atom/iter-1": "/Users/ben/proj",
		"/a/b/c/d/.idiom/refine/x/iter-7":              "/a/b/c/d",
	}
	for in, want := range cases {
		got := refineProjectRootFromIterDir(in)
		if got != want {
			t.Errorf("refineProjectRootFromIterDir(%q) = %q, want %q", in, got, want)
		}
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
	if refineLoopDiverging(prev, []string{}) {
		t.Fatalf("clean review (no bugs) is the success case, not divergence")
	}
}

func TestRunLaneRefineConvergesOnShipAsIsVerdict(t *testing.T) {
	root := t.TempDir()
	specPath := filepath.Join(root, "spec.md")
	if err := os.WriteFile(specPath, []byte("Add a feature.\n"), 0o644); err != nil {
		t.Fatalf("seed spec: %v", err)
	}
	outPath := filepath.Join(root, "brief.md")
	opts := laneRefineOptions{
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
	code := runLaneRefine(opts, dispatch, &bytes.Buffer{}, &stderr)
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

func TestRunLaneRefineWritesMetricsOnDraftDispatchFailure(t *testing.T) {
	root := t.TempDir()
	specPath := filepath.Join(root, "spec.md")
	if err := os.WriteFile(specPath, []byte("Add a feature.\n"), 0o644); err != nil {
		t.Fatalf("seed spec: %v", err)
	}
	outPath := filepath.Join(root, "brief.md")
	opts := laneRefineOptions{
		SpecPath:    specPath,
		OutputPath:  outPath,
		Title:       "fail-atom",
		MaxIters:    3,
		ProjectRoot: root,
	}
	dispatch := func(_ string, _, _ io.Writer) int { return 7 }
	var stderr bytes.Buffer
	code := runLaneRefine(opts, dispatch, &bytes.Buffer{}, &stderr)
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
	opts := laneRefineOptions{
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
	code := runLaneRefine(opts, dispatch, &bytes.Buffer{}, &stderr)
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
	opts := laneRefineOptions{
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
	dispatch := refineDispatchFn(func(metaBriefPath string, stdout, stderr io.Writer) int {
		calls++
		body, err := os.ReadFile(metaBriefPath)
		if err != nil {
			t.Fatalf("read meta: %v", err)
		}
		// Find the output path the meta refers to. Prefer the path
		// in the "Files To Modify" section; fall back to first .md
		// mention. (Mirrors the stubDispatcher logic — template
		// restructure can put DRAFT_PATH before REVIEW_PATH in body
		// and may embed example "## Files To Modify" headings inside
		// ```text skeleton fences; strip fences first.)
		scanText := stripFencedBlocksForStub(string(body))
		var outPath string
		inFilesToModify := false
		for _, line := range strings.Split(scanText, "\n") {
			trimmed := strings.TrimSpace(line)
			lower := strings.ToLower(trimmed)
			if strings.HasPrefix(lower, "## files to modify") {
				inFilesToModify = true
				continue
			}
			if inFilesToModify && strings.HasPrefix(trimmed, "## ") {
				inFilesToModify = false
			}
			if strings.Contains(line, "`") && strings.Contains(line, ".md") {
				start := strings.Index(line, "`")
				end := strings.Index(line[start+1:], "`")
				if end > 0 {
					candidate := line[start+1 : start+1+end]
					if strings.HasSuffix(candidate, ".md") && candidate != metaBriefPath {
						outPath = candidate
						if inFilesToModify {
							break
						}
					}
				}
			}
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
	code := runLaneRefine(opts, dispatch, &bytes.Buffer{}, &stderr)
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
	opts := laneRefineOptions{
		SpecPath:    specPath,
		OutputPath:  filepath.Join(root, "brief.md"),
		Title:       "test-atom",
		MaxIters:    3,
		ProjectRoot: root,
	}
	// Iter-1 draft dispatch fails immediately.
	dispatch := refineDispatchFn(func(metaBriefPath string, stdout, stderr io.Writer) int {
		return 2
	})
	var stderr bytes.Buffer
	code := runLaneRefine(opts, dispatch, &bytes.Buffer{}, &stderr)
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
	opts := laneRefineOptions{
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
	code := runLaneRefine(opts, dispatch, &bytes.Buffer{}, &stderr)
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

func TestRunLaneRefineStopsOnNoImprovement(t *testing.T) {
	root := t.TempDir()
	specPath := filepath.Join(root, "spec.md")
	if err := os.WriteFile(specPath, []byte("Add a feature.\n"), 0o644); err != nil {
		t.Fatalf("seed spec: %v", err)
	}
	opts := laneRefineOptions{
		SpecPath:    specPath,
		OutputPath:  filepath.Join(root, "brief.md"),
		Title:       "no-improvement",
		MaxIters:    3,
		ProjectRoot: root,
	}
	// Iter 1 has 2 bugs; iter 2 has 2 DIFFERENT bugs (so divergence
	// detector doesn't fire — divergence is for IDENTICAL sets).
	// The no-improvement stop should fire because bug count didn't
	// drop, even though the bugs themselves changed.
	dispatch, _ := stubDispatcher(t, []string{
		nonTrivialDraftBody("Draft v1"),
		"# Review\n\n## Bugs found\n\n1. **Bug A**: x\n2. **Bug B**: y\n\n## Verdict\n\nfix-bugs-then-ship\n",
		nonTrivialDraftBody("Draft v2"),
		"# Review\n\n## Bugs found\n\n1. **Bug C**: z\n2. **Bug D**: w\n\n## Verdict\n\nfix-bugs-then-ship\n",
	})
	var stderr bytes.Buffer
	code := runLaneRefine(opts, dispatch, &bytes.Buffer{}, &stderr)
	// no-improvement saves best AND exits non-zero — orchestrator
	// inspects (Colony Q4).
	if code == 0 {
		t.Fatalf("expected non-zero exit on no-improvement convergence; got 0. stderr=%s", stderr.String())
	}
	if !strings.Contains(stderr.String(), "no improvement") {
		t.Fatalf("expected 'no improvement' message in stderr, got: %s", stderr.String())
	}
	body, _ := os.ReadFile(opts.OutputPath)
	if !strings.HasPrefix(string(body), "> WARNING:") {
		t.Fatalf("expected saved best-draft to lead with WARNING, got: %s", body)
	}
}

func TestRunLaneRefineFinalizesBestDraftNotLatest(t *testing.T) {
	root := t.TempDir()
	specPath := filepath.Join(root, "spec.md")
	if err := os.WriteFile(specPath, []byte("Add a feature.\n"), 0o644); err != nil {
		t.Fatalf("seed spec: %v", err)
	}
	opts := laneRefineOptions{
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
	code := runLaneRefine(opts, dispatch, &bytes.Buffer{}, &stderr)
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
			body: "## Files To Modify\n\n| File | Change |\n|------|--------|\n| `internal/agent/runner_test.go` | ADD test |\n",
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
			if !equalStringSlices(got, tc.want) {
				t.Fatalf("classifyBriefAtomShape: got %v, want %v", got, tc.want)
			}
		})
	}
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
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
			if !equalStringSlices(applies, tc.wantApplies) {
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
	stubLaneBriefLint(t, func(args []string, stdout, stderr io.Writer) int { return 0 })
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
	opts := laneRefineOptions{
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
	code := runLaneRefine(opts, dispatch, &bytes.Buffer{}, &stderr)
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

// TestRunLaneRefinePatchPhaseSeesLintPreload locks in §6.1 Gap 1
// (Colony shipped 2026-05-11): when refine runs a patch iteration,
// the patch meta-brief inlines the previous draft's lint output so
// the patcher targets BLOCKs directly instead of relying on the
// reviewer to transcribe every finding.
func TestRunLaneRefinePatchPhaseSeesLintPreload(t *testing.T) {
	stubLaneBriefLint(t, func(args []string, stdout, stderr io.Writer) int {
		_, _ = stdout.Write([]byte("BLOCK: unfilled <placeholder> in Files To Modify row\nWARN: append-to-end-of-file edit shape\n"))
		return 1
	})
	root := t.TempDir()
	specPath := filepath.Join(root, "spec.md")
	if err := os.WriteFile(specPath, []byte("Add a feature.\n"), 0o644); err != nil {
		t.Fatalf("seed spec: %v", err)
	}
	opts := laneRefineOptions{
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
	_ = runLaneRefine(opts, dispatch, &bytes.Buffer{}, &stderr)
	metaPatchPath := filepath.Join(refineStateDir(root, "patch-lint"), "iter-2", "meta-patch.md")
	body, err := os.ReadFile(metaPatchPath)
	if err != nil {
		t.Fatalf("read meta-patch: %v", err)
	}
	if !strings.Contains(string(body), "BLOCK: unfilled <placeholder>") {
		t.Fatalf("expected lint BLOCK inlined in patch meta-brief, got:\n%s", body)
	}
	if !strings.Contains(string(body), "Pre-loaded lint findings") {
		t.Fatalf("expected 'Pre-loaded lint findings' section in patch meta-brief, got:\n%s", body)
	}
}

// TestRenderRefineLintFindingsFromJSONEnvelope locks in §6.1 Gap 5
// (Colony shipped 2026-05-11): refine consumes the structured JSON
// output of `idiom brief lint --json` and renders BLOCK+WARN
// findings for the reviewer. INFO is dropped (Q1 signal-to-noise);
// malformed JSON falls back to ok=false so the caller can switch
// to plain-text capture.
func TestRenderRefineLintFindingsFromJSONEnvelope(t *testing.T) {
	envelope := `{
  "brief": "/tmp/test.md",
  "exit": 1,
  "findings": [
    {"severity": "BLOCK", "category": "anchor-specificity", "brief": "/tmp/test.md", "message": "exact-anchor failed for x.go"},
    {"severity": "WARN", "category": "missing-current-state", "brief": "/tmp/test.md", "message": "no verified state"},
    {"severity": "INFO", "category": "other", "brief": "/tmp/test.md", "message": "informational note (dropped)"}
  ]
}`
	out, ok := renderRefineLintFindings([]byte(envelope))
	if !ok {
		t.Fatalf("expected ok=true on well-formed envelope")
	}
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
	envelope := `{"brief": "/tmp/test.md", "exit": 0, "findings": []}`
	out, ok := renderRefineLintFindings([]byte(envelope))
	if !ok {
		t.Fatalf("expected ok=true")
	}
	if !strings.Contains(out, "no BLOCK/WARN findings") {
		t.Fatalf("expected clean-pass note, got: %s", out)
	}
}

// TestRenderRefineLintFindingsMalformedJSONReturnsNotOK: junk
// envelope yields ok=false so captureRefineLintOutput can fall back
// gracefully.
func TestRenderRefineLintFindingsMalformedJSONReturnsNotOK(t *testing.T) {
	_, ok := renderRefineLintFindings([]byte("idiom brief preflight: FAIL /path\n"))
	if ok {
		t.Fatalf("expected ok=false for non-JSON output")
	}
}

// TestParseBriefLintFindingsCategorizesByKeyword exercises the
// keyword-driven category derivation (§6.1 Gap 5 schema). Each
// known message shape lands in a stable category; unknown messages
// fall to "other" rather than being silently dropped.
func TestParseBriefLintFindingsCategorizesByKeyword(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want briefLintFinding
	}{
		{
			name: "anchor-specificity-block",
			raw:  "idiom brief preflight: FAIL /b.md\n- exact-anchor specificity failed for x.go.\n",
			want: briefLintFinding{Severity: "BLOCK", Category: "anchor-specificity", Brief: "/b.md", Message: "exact-anchor specificity failed for x.go."},
		},
		{
			name: "warn-current-state",
			raw:  "- WARN: no obvious verified-current-state section found.\n",
			want: briefLintFinding{Severity: "WARN", Category: "missing-current-state", Brief: "/b.md", Message: "no obvious verified-current-state section found."},
		},
		{
			name: "info-note",
			raw:  "- INFO: noteworthy detail\n",
			want: briefLintFinding{Severity: "INFO", Category: "other", Brief: "/b.md", Message: "noteworthy detail"},
		},
		{
			name: "unfilled-placeholder-block",
			raw:  "- some unfilled placeholder somewhere\n",
			want: briefLintFinding{Severity: "BLOCK", Category: "unfilled-placeholder", Brief: "/b.md", Message: "some unfilled placeholder somewhere"},
		},
		{
			name: "unknown-falls-through-to-other",
			raw:  "- something nobody planned for\n",
			want: briefLintFinding{Severity: "BLOCK", Category: "other", Brief: "/b.md", Message: "something nobody planned for"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseBriefLintFindings("/b.md", tc.raw)
			if len(got) != 1 {
				t.Fatalf("expected 1 finding, got %d: %+v", len(got), got)
			}
			if got[0] != tc.want {
				t.Fatalf("finding mismatch:\n got=%+v\nwant=%+v", got[0], tc.want)
			}
		})
	}
}

// TestRunBriefLintJSONFlagEmitsEnvelope is the end-to-end binding:
// the CLI `idiom brief lint --json <path>` returns a JSON envelope
// on stdout matching the schema, with the underlying preflight's
// exit code propagated.
func TestRunBriefLintJSONFlagEmitsEnvelope(t *testing.T) {
	root := t.TempDir()
	briefPath := filepath.Join(root, "tiny.md")
	body := "# Title\n\n## Files To Modify\n\n| File | Change |\n|------|--------|\n| `x.go` | MODIFY |\n"
	if err := os.WriteFile(briefPath, []byte(body), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	var stdout, stderr bytes.Buffer
	exit := runBriefLint([]string{briefPath, "--json"}, &stdout, &stderr, nil)
	// The python preflight is expected to return non-zero on this
	// minimal brief (missing anchors etc.) — we don't care about the
	// exact exit, just that the JSON envelope is well-formed.
	_ = exit
	out := stdout.String()
	if !strings.HasPrefix(strings.TrimSpace(out), "{") {
		t.Fatalf("expected JSON envelope on stdout, got: %s", out)
	}
	var report briefLintReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("expected valid JSON envelope, got: %v\n%s", err, out)
	}
	if report.Brief != briefPath {
		t.Fatalf("expected brief field = %q, got %q", briefPath, report.Brief)
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
		if !contains(classifyBriefAtomShape([]byte(p.body)), p.want) {
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

func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
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
	stubLaneBriefLint(t, func(args []string, stdout, stderr io.Writer) int {
		// Emit a well-formed JSON envelope so persistRefineLintJSON fires.
		// (The test --json flag bypasses the real python preflight.)
		_, _ = stdout.Write([]byte(`{"brief":"x.md","exit":0,"findings":[]}`))
		return 0
	})
	root := t.TempDir()
	specPath := filepath.Join(root, "spec.md")
	if err := os.WriteFile(specPath, []byte("Add a feature.\n"), 0o644); err != nil {
		t.Fatalf("seed spec: %v", err)
	}
	opts := laneRefineOptions{
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
	code := runLaneRefine(opts, dispatch, &bytes.Buffer{}, &stderr)
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

// TestBriefLintJSONEnvelopeSchemaIsStable is a regression guard on
// the JSON field names downstream consumers (refine review/patch
// prompts, future tooling) depend on. Renaming or removing a field
// here is a breaking change and must be deliberate.
func TestBriefLintJSONEnvelopeSchemaIsStable(t *testing.T) {
	report := briefLintReport{
		Brief: "/x.md",
		Exit:  1,
		Findings: []briefLintFinding{
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
	stubLaneBriefLint(t, func(args []string, stdout, stderr io.Writer) int { return 0 })
	root := t.TempDir()
	refineOverride := filepath.Join(root, ".idiom", "prompts", "refine")
	briefOverlay := filepath.Join(root, ".idiom", "prompts", "brief")
	if err := os.MkdirAll(refineOverride, 0o755); err != nil {
		t.Fatalf("mkdir refine override: %v", err)
	}
	if err := os.MkdirAll(briefOverlay, 0o755); err != nil {
		t.Fatalf("mkdir brief overlay: %v", err)
	}
	overrideBody := "# {{TITLE}}\n\n## Custom-mode body marker\n\nSPEC: {{SPEC_BODY}}\n\nWrite to {{DRAFT_PATH}} via write_file.\n\n## Files To Modify\n\n| File | Change |\n|------|--------|\n| `{{DRAFT_PATH}}` | NEW FILE |\n"
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
	opts := laneRefineOptions{
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
	code := runLaneRefine(opts, dispatch, &bytes.Buffer{}, &stderr)
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
	stubLaneBriefLint(t, func(args []string, stdout, stderr io.Writer) int { return 0 })
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
	opts := laneRefineOptions{
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
	code := runLaneRefine(opts, dispatch, &bytes.Buffer{}, &stderr)
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
	stubLaneBriefLint(t, func(args []string, stdout, stderr io.Writer) int { return 0 })
	root := t.TempDir() // bare — no overlay dir
	specPath := filepath.Join(root, "spec.md")
	if err := os.WriteFile(specPath, []byte("Add a feature.\n"), 0o644); err != nil {
		t.Fatalf("seed spec: %v", err)
	}
	opts := laneRefineOptions{
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
	_ = runLaneRefine(opts, dispatch, &bytes.Buffer{}, &stderr)
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
	overrideBody := "# {{TITLE}}\n\n## Custom mode body\n\n{{SPEC_BODY}}\n\nCall write_file with {{DRAFT_PATH}}.\n"
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
			body: "# {{TITLE}}\n\n## Body\n\n{{SPEC_BODY}}\n\nCall write_file please.\n",
			want: "{{DRAFT_PATH}}",
		},
		{
			name: "missing-spec-body",
			body: "# {{TITLE}}\n\n## Body\n\nCall write_file at {{DRAFT_PATH}}.\n",
			want: "{{SPEC_BODY}}",
		},
		{
			name: "no-section-heading",
			body: "{{TITLE}} {{SPEC_BODY}} {{DRAFT_PATH}} — call write_file.\n",
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

// TestEnsureRefineDraftWriteClosureAppendsWhenMissing locks in the
// UX choice from the 2026-05-11 review: project authors don't have
// to remember to include a write_file closure in their override
// templates. If missing, idiom auto-appends a canonical closure
// (matching the embedded templates) so the dispatched-model
// contract is uniform across modes. Templates that already include
// their own write directive are left untouched.
func TestEnsureRefineDraftWriteClosureAppendsWhenMissing(t *testing.T) {
	tests := []struct {
		name           string
		body           string
		wantAppended   bool
		wantUntouched  bool
		mustNotContain string
	}{
		{
			name:         "body-without-write-file",
			body:         "# {{TITLE}}\n\n## Body\n\n{{SPEC_BODY}}\n\nDescribe deliverable at {{DRAFT_PATH}}.\n",
			wantAppended: true,
		},
		{
			name:          "body-with-explicit-write-file",
			body:          "# {{TITLE}}\n\n## Body\n\n{{SPEC_BODY}}\n\nCall write_file({{DRAFT_PATH}}).\n",
			wantUntouched: true,
		},
		{
			name:          "body-with-uppercase-WRITE_FILE",
			body:          "# {{TITLE}}\n\n## Body\n\n{{SPEC_BODY}}\n\nCall WRITE_FILE({{DRAFT_PATH}}).\n",
			wantUntouched: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ensureRefineDraftWriteClosure(tc.body, nil, "test")
			if tc.wantUntouched && got != tc.body {
				t.Fatalf("expected body untouched, got delta:\n%s", got)
			}
			if tc.wantAppended {
				if got == tc.body {
					t.Fatalf("expected closure appended, body unchanged")
				}
				if !strings.Contains(got, "## STOP AND WRITE NOW") {
					t.Fatalf("expected canonical closure heading, got:\n%s", got)
				}
				if !strings.Contains(got, "write_file") {
					t.Fatalf("expected appended closure to mention write_file, got:\n%s", got)
				}
				if !strings.Contains(got, "{{DRAFT_PATH}}") {
					t.Fatalf("expected appended closure to reference {{DRAFT_PATH}} for later substitution, got:\n%s", got)
				}
			}
		})
	}
}

// TestRunLaneRefineAutoAppendsClosureToOverrideWithoutWriteFile
// verifies the auto-append behavior is wired end-to-end: a project
// override that omits write_file in its body produces a meta-draft
// containing the canonical closure with the destination path
// substituted in.
func TestRunLaneRefineAutoAppendsClosureToOverrideWithoutWriteFile(t *testing.T) {
	stubLaneBriefLint(t, func(args []string, stdout, stderr io.Writer) int { return 0 })
	root := t.TempDir()
	overrideDir := filepath.Join(root, ".idiom", "prompts", "refine")
	if err := os.MkdirAll(overrideDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Note: NO write_file mention. Author focused on the body.
	overrideBody := "# {{TITLE}}\n\n## Notebook deliverable\n\nSPEC: {{SPEC_BODY}}\n\nWrite the experiment at {{DRAFT_PATH}}.\n\n## Files To Modify\n\n| File | Change |\n|------|--------|\n| `{{DRAFT_PATH}}` | NEW FILE |\n"
	if err := os.WriteFile(filepath.Join(overrideDir, "draft-notebook.md"), []byte(overrideBody), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	specPath := filepath.Join(root, "spec.md")
	if err := os.WriteFile(specPath, []byte("Sweep hyperparams.\n"), 0o644); err != nil {
		t.Fatalf("seed spec: %v", err)
	}
	opts := laneRefineOptions{
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
	_ = runLaneRefine(opts, dispatch, &bytes.Buffer{}, &stderr)
	if !strings.Contains(stderr.String(), "no write_file directive; appending canonical closure") {
		t.Fatalf("expected stderr to log auto-append, got: %s", stderr.String())
	}
	metaDraftPath := filepath.Join(refineStateDir(root, "closure-test"), "iter-1", "meta-draft.md")
	body, err := os.ReadFile(metaDraftPath)
	if err != nil {
		t.Fatalf("read meta-draft: %v", err)
	}
	if !strings.Contains(string(body), "## STOP AND WRITE NOW") {
		t.Fatalf("expected canonical closure in substituted meta-draft, got:\n%s", body)
	}
	if !strings.Contains(string(body), "write_file") {
		t.Fatalf("expected write_file in substituted meta-draft, got:\n%s", body)
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
	stubLaneBriefLint(t, func(args []string, stdout, stderr io.Writer) int { return 0 })
	root := t.TempDir()
	overrideDir := filepath.Join(root, ".idiom", "prompts", "refine")
	if err := os.MkdirAll(overrideDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	overrideBody := "# {{TITLE}}\n\n## Notebook deliverable\n\nSPEC: {{SPEC_BODY}}\n\nCall write_file({{DRAFT_PATH}}) with the experiment.\n\n## Files To Modify\n\n| File | Change |\n|------|--------|\n| `{{DRAFT_PATH}}` | NEW FILE |\n"
	overridePath := filepath.Join(overrideDir, "draft-notebook.md")
	if err := os.WriteFile(overridePath, []byte(overrideBody), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	specPath := filepath.Join(root, "spec.md")
	if err := os.WriteFile(specPath, []byte("Sweep hyperparameters and pick best F1.\n"), 0o644); err != nil {
		t.Fatalf("seed spec: %v", err)
	}
	opts := laneRefineOptions{
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
	_ = runLaneRefine(opts, dispatch, &bytes.Buffer{}, &stderr)
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
	if !strings.Contains(string(body), "SPEC: Sweep hyperparameters") {
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
			got := refineDetectMode(tc.spec)
			if got != tc.want {
				t.Fatalf("refineDetectMode(%q) = %q, want %q", tc.spec, got, tc.want)
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
	stubLaneBriefLint(t, func(args []string, stdout, stderr io.Writer) int { return 0 })
	root := t.TempDir()
	specPath := filepath.Join(root, "spec.md")
	if err := os.WriteFile(specPath, []byte("Create .planning/inventory.md with all postflight checks.\n"), 0o644); err != nil {
		t.Fatalf("seed spec: %v", err)
	}
	opts := laneRefineOptions{
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
	_ = runLaneRefine(opts, dispatch, &bytes.Buffer{}, &stderr)
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
	stubLaneBriefLint(t, func(args []string, stdout, stderr io.Writer) int { return 0 })
	root := t.TempDir()
	specPath := filepath.Join(root, "spec.md")
	if err := os.WriteFile(specPath, []byte("Create .planning/inventory.md.\n"), 0o644); err != nil {
		t.Fatalf("seed spec: %v", err)
	}
	opts := laneRefineOptions{
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
	_ = runLaneRefine(opts, dispatch, &bytes.Buffer{}, &stderr)
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
	stubLaneBriefLint(t, func(args []string, stdout, stderr io.Writer) int {
		_, _ = stdout.Write([]byte("BLOCK: anchor at foo.go:42 doesn't match source\nWARN: append-to-end-of-file edit is wedge-prone\n"))
		return 1
	})
	root := t.TempDir()
	specPath := filepath.Join(root, "spec.md")
	if err := os.WriteFile(specPath, []byte("Add a feature.\n"), 0o644); err != nil {
		t.Fatalf("seed spec: %v", err)
	}
	opts := laneRefineOptions{
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
	_ = runLaneRefine(opts, dispatch, &bytes.Buffer{}, &stderr)
	metaReviewPath := filepath.Join(refineStateDir(root, "lint-preload"), "iter-1", "meta-review.md")
	body, err := os.ReadFile(metaReviewPath)
	if err != nil {
		t.Fatalf("read meta-review: %v", err)
	}
	if !strings.Contains(string(body), "BLOCK: anchor at foo.go:42 doesn't match source") {
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

// TestRunLaneRefineHandlesCleanLintGracefully checks the empty-lint
// path: when lint exits 0 with no findings, the {{LINT_OUTPUT}}
// substitution falls back to a "no findings" note so the reviewer
// doesn't silently treat empty as "clean by default".
func TestRunLaneRefineHandlesCleanLintGracefully(t *testing.T) {
	stubLaneBriefLint(t, func(args []string, stdout, stderr io.Writer) int {
		return 0
	})
	root := t.TempDir()
	specPath := filepath.Join(root, "spec.md")
	if err := os.WriteFile(specPath, []byte("Add a feature.\n"), 0o644); err != nil {
		t.Fatalf("seed spec: %v", err)
	}
	opts := laneRefineOptions{
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
	code := runLaneRefine(opts, dispatch, &bytes.Buffer{}, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0 on clean lint + ship-as-is, got %d; stderr=%s", code, stderr.String())
	}
	metaReviewPath := filepath.Join(refineStateDir(root, "lint-clean"), "iter-1", "meta-review.md")
	body, err := os.ReadFile(metaReviewPath)
	if err != nil {
		t.Fatalf("read meta-review: %v", err)
	}
	if !strings.Contains(string(body), "no findings") {
		t.Fatalf("expected 'no findings' fallback in review meta, got:\n%s", body)
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
	opts := laneRefineOptions{
		SpecPath:    specPath,
		OutputPath:  filepath.Join(root, "brief.md"),
		Title:       "trivial-draft",
		MaxIters:    3,
		ProjectRoot: root,
	}
	// Dispatch exits 0 but writes only a 20-byte fragment to the
	// draft path — the silent-failure shape.
	dispatch := refineDispatchFn(func(metaBriefPath string, stdout, stderr io.Writer) int {
		iterDir := filepath.Dir(metaBriefPath)
		_ = os.WriteFile(filepath.Join(iterDir, "draft.md"), []byte("# stub\n"), 0o644)
		return 0
	})
	var stderr bytes.Buffer
	code := runLaneRefine(opts, dispatch, &bytes.Buffer{}, &stderr)
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
