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
		var outPath string
		inFilesToModify := false
		for _, line := range strings.Split(string(body), "\n") {
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
		"# Drafted brief\n\nbody",
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
	if !strings.Contains(string(body), "Drafted brief") {
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
		"# Draft v1 (best, will be shipped)",
		sameReview,
		"# Draft v2 (patch failed to fix)",
		sameReview,
	})
	var stderr bytes.Buffer
	code := runLaneRefine(opts, dispatch, &bytes.Buffer{}, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0 (ship bestDraft) on divergence, got %d; stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "loop diverging") {
		t.Fatalf("expected diverging-loop message in stderr, got: %s", stderr.String())
	}
	body, _ := os.ReadFile(opts.OutputPath)
	if !strings.Contains(string(body), "Draft v1 (best") {
		t.Fatalf("expected iter-1 draft to be shipped on divergence, got: %s", body)
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
		// restructure can put DRAFT_PATH before REVIEW_PATH in body.)
		var outPath string
		inFilesToModify := false
		for _, line := range strings.Split(string(body), "\n") {
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
		// Simulate iter-2 review wedge: don't write the output file
		// and return non-zero exit (matches real wedge shape).
		if calls == 4 {
			return 1
		}
		responses := []string{
			"# Draft v1 (will be shipped via failsoft)",
			"# Review\n\n## Bugs found\n\n1. **Bug A**: x\n2. **Bug B**: y\n3. **Bug C**: z\n\n## Verdict\n\nfix-bugs-then-ship\n",
			"# Draft v2",
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
	if code != 0 {
		t.Fatalf("expected exit 0 (failsoft ships bestDraft) on iter-2 review wedge, got %d; stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "failsoft at iter 2") {
		t.Fatalf("expected failsoft message in stderr, got: %s", stderr.String())
	}
	body, _ := os.ReadFile(opts.OutputPath)
	if !strings.Contains(string(body), "Draft v1 (will be shipped via failsoft)") {
		t.Fatalf("expected iter-1 draft shipped via failsoft, got: %s", body)
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
		"# Draft v1",
		"# Review\n\n## Bugs found\n\n1. **First bug**: blah\n2. **Second bug**: blah\n\n## Verdict\n\nfix-bugs-then-ship\n",
		"# Draft v2 (final, fewer bugs)",
		"# Review\n\n## Bugs found\n\n1. **Different bug**: blah\n\n## Verdict\n\nfix-bugs-then-ship\n",
	})
	var stderr bytes.Buffer
	code := runLaneRefine(opts, dispatch, &bytes.Buffer{}, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0 on max-iters exhaust (ship best), got %d; stderr=%s", code, stderr.String())
	}
	body, _ := os.ReadFile(opts.OutputPath)
	if !strings.Contains(string(body), "Draft v2") {
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
		"# Draft v1",
		"# Review\n\n## Bugs found\n\n1. **Bug A**: x\n2. **Bug B**: y\n\n## Verdict\n\nfix-bugs-then-ship\n",
		"# Draft v2",
		"# Review\n\n## Bugs found\n\n1. **Bug C**: z\n2. **Bug D**: w\n\n## Verdict\n\nfix-bugs-then-ship\n",
	})
	var stderr bytes.Buffer
	code := runLaneRefine(opts, dispatch, &bytes.Buffer{}, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0 (no-improvement → ship best), got %d; stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "no improvement") {
		t.Fatalf("expected 'no improvement' message in stderr, got: %s", stderr.String())
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
		"# Draft v1 (BEST — 1 bug)",
		"# Review\n\n## Bugs found\n\n1. **Bug A**: x\n\n## Verdict\n\nfix-bugs-then-ship\n",
		"# Draft v2 (REGRESSED — 5 bugs)",
		"# Review\n\n## Bugs found\n\n1. **Bug A**: x\n2. **Bug B**: y\n3. **Bug C**: z\n4. **Bug D**: w\n5. **Bug E**: v\n\n## Verdict\n\nfix-bugs-then-ship\n",
	})
	var stderr bytes.Buffer
	code := runLaneRefine(opts, dispatch, &bytes.Buffer{}, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d; stderr=%s", code, stderr.String())
	}
	body, err := os.ReadFile(opts.OutputPath)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if !strings.Contains(string(body), "Draft v1 (BEST") {
		t.Fatalf("output should be the best draft (v1), not the regressed latest (v2). Got: %s", body)
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
