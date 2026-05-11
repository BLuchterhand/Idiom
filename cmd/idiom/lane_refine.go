package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// refineIterMetrics captures observable per-iteration data and is
// persisted to <iter-N>/metrics.json. Lane-explain (#83) and any
// downstream observability tooling read this rather than scraping
// stderr. Fields stay zero/empty when the iteration didn't reach
// them — a draft-dispatch failure leaves Verdict and BugCount unset.
type refineIterMetrics struct {
	Iter               int    `json:"iter"`
	Phase              string `json:"phase"` // "draft" (iter 1) or "patch" (iter 2+)
	StartedAt          string `json:"started_at,omitempty"`
	EndedAt            string `json:"ended_at,omitempty"`
	DurationMS         int64  `json:"duration_ms,omitempty"`
	DraftDispatchExit  int    `json:"draft_dispatch_exit"`
	ReviewDispatchExit int    `json:"review_dispatch_exit"`
	Verdict            string `json:"verdict,omitempty"`
	BugCount           int    `json:"bug_count,omitempty"`
	Action             string `json:"action,omitempty"` // ship-as-is | continue | diverging | no-improvement | failsoft-<reason>
	FailsoftReason     string `json:"failsoft_reason,omitempty"`
}

// writeRefineIterMetrics persists the per-iter metrics to a stable
// path. Best-effort: errors are surfaced to stderr so the refine
// loop keeps making progress when the state dir is read-only or
// out of disk; downstream tooling treats missing metrics as "this
// iter didn't write a record" rather than a hard failure.
func writeRefineIterMetrics(iterDir string, m refineIterMetrics, stderr io.Writer) {
	path := filepath.Join(iterDir, "metrics.json")
	body, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		fmt.Fprintf(stderr, "refine: marshal iter metrics: %v\n", err)
		return
	}
	body = append(body, '\n')
	if err := os.WriteFile(path, body, 0o644); err != nil {
		fmt.Fprintf(stderr, "refine: write iter metrics %s: %v\n", path, err)
	}
}

//go:embed lanes/refine/draft.md
var refineDraftTemplate string

//go:embed lanes/refine/review.md
var refineReviewTemplate string

//go:embed lanes/refine/patch.md
var refinePatchTemplate string

// laneRefineOptions captures the refine-mode-specific flags.
type laneRefineOptions struct {
	SpecPath    string
	OutputPath  string
	Title       string
	MaxIters    int
	ProjectRoot string
}

// refineDispatchFn is the per-step dispatcher. Default impl shells
// out to `idiom lane implement`; tests inject a fake.
type refineDispatchFn func(metaBriefPath string, stdout, stderr io.Writer) int

// defaultRefinePool is the queue pool refine submits to. Refine's
// subprocess is `lane implement`, which (in the current project
// config) targets the 35B reasoning backend. Hardcoded for v1; if
// projects start running implement against a different backend this
// becomes a config knob.
const defaultRefinePool = "qwen-35b"

func defaultRefineDispatch(metaBriefPath string, stdout, stderr io.Writer) int {
	binary, err := os.Executable()
	if err != nil {
		fmt.Fprintf(stderr, "refine: cannot locate idiom binary: %v\n", err)
		return 2
	}
	// Route through the local queue daemon so concurrent refine
	// sessions serialize access to the 35B Metal command buffer.
	// `idiom queue run` exits non-zero with a clear "start the
	// daemon" message if unreachable, which surfaces correctly to
	// the caller via this function's exit code.
	cmd := exec.Command(binary,
		"queue", "run",
		"--pool", defaultRefinePool,
		"--label", "refine",
		"--quiet",
		"--",
		binary, "lane", "implement", "--no-lint", "--json",
		// 16384 mirrors plan_execute.go's planner cap (#96). 35B in
		// thinking mode regularly hits 8000+ tokens reasoning through
		// a refine draft/review, and 8192 (the backend default) cuts
		// off before the model can produce the visible deliverable.
		// Same Qwen3 quickstart rationale as #70 — long structured
		// outputs need 16K headroom.
		"--max-tokens", "16384",
		metaBriefPath,
	)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode()
		}
		return 1
	}
	return 0
}

func runLaneRefine(opts laneRefineOptions, dispatch refineDispatchFn, stdout, stderr io.Writer) int {
	if err := validateRefineOptions(&opts); err != nil {
		fmt.Fprintf(stderr, "refine: %v\n", err)
		return 2
	}
	// Sweep stale per-title refine state at session START (#73) and END
	// (#77 — defer). Threshold matches plan-execute's 7-day window so
	// recent debug-worthy state survives but long-forgotten title dirs
	// don't accumulate. Defer fires even on early returns / failsoft
	// paths — pure tidy-up, no behavioral effect on the current run.
	cleanupStaleRefineState(opts.ProjectRoot, 7*24*time.Hour, stderr)
	defer cleanupStaleRefineState(opts.ProjectRoot, 7*24*time.Hour, stderr)

	stateDir := refineStateDir(opts.ProjectRoot, opts.Title)
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		fmt.Fprintf(stderr, "refine: cannot create state dir %s: %v\n", stateDir, err)
		return 1
	}
	specBody, err := os.ReadFile(opts.SpecPath)
	if err != nil {
		fmt.Fprintf(stderr, "refine: cannot read spec %s: %v\n", opts.SpecPath, err)
		return 1
	}

	var prevReviewBugs []string
	var lastDraftPath string
	bestDraftPath := ""
	bestBugCount := -1 // -1 = "no draft scored yet"

	// failsoft centralizes the "this iteration broke" path. When we have
	// at least one previously-reviewed draft (bestDraftPath set), we
	// ship it as the converged brief instead of throwing away all the
	// successful work — refine variance means later iters can wedge
	// even when earlier ones produced a clean reviewed draft. When
	// nothing's been reviewed yet (iter-1 broke), there's no shippable
	// artifact and we fail with the underlying exit code.
	// activeMetrics points at the in-flight iter's record so
	// failsoft / verdict branches can stamp Action/FailsoftReason
	// before returning. Reset at the top of each iter.
	var activeMetrics *refineIterMetrics
	var activeIterDir string
	stampMetricsAction := func(action, failsoftReason string) {
		if activeMetrics == nil {
			return
		}
		activeMetrics.Action = action
		activeMetrics.FailsoftReason = failsoftReason
		activeMetrics.EndedAt = time.Now().UTC().Format(time.RFC3339Nano)
		if t, err := time.Parse(time.RFC3339Nano, activeMetrics.StartedAt); err == nil {
			activeMetrics.DurationMS = time.Since(t).Milliseconds()
		}
		writeRefineIterMetrics(activeIterDir, *activeMetrics, stderr)
	}
	failsoft := func(iter int, reason string, failExit int) int {
		stampMetricsAction("failsoft-"+reason, reason)
		if bestDraftPath == "" {
			fmt.Fprintf(stderr, "refine: failed at iter %d (%s) with no previously-reviewed draft to fall back on\n", iter, reason)
			return failExit
		}
		fmt.Fprintf(stderr, "refine: failsoft at iter %d (%s) — shipping last-reviewed draft (%d bugs)\n", iter, reason, bestBugCount)
		return finalizeRefineWithTitle(bestDraftPath, opts.OutputPath, opts.Title, iter, "failsoft-"+reason, stderr)
	}

	for iter := 1; iter <= opts.MaxIters; iter++ {
		iterDir := filepath.Join(stateDir, fmt.Sprintf("iter-%d", iter))
		if err := os.MkdirAll(iterDir, 0o755); err != nil {
			fmt.Fprintf(stderr, "refine: cannot create iter dir %s: %v\n", iterDir, err)
			return failsoft(iter, "iter-dir-mkdir", 1)
		}
		draftPath := filepath.Join(iterDir, "draft.md")
		reviewPath := filepath.Join(iterDir, "review.md")

		metrics := refineIterMetrics{Iter: iter, StartedAt: time.Now().UTC().Format(time.RFC3339Nano)}
		if iter == 1 {
			metrics.Phase = "draft"
		} else {
			metrics.Phase = "patch"
		}
		activeMetrics = &metrics
		activeIterDir = iterDir

		if iter == 1 {
			metaPath := filepath.Join(iterDir, "meta-draft.md")
			if err := writeRefineDraftMeta(metaPath, draftPath, opts, string(specBody)); err != nil {
				fmt.Fprintf(stderr, "refine: write draft meta: %v\n", err)
				return failsoft(iter, "draft-meta-write", 1)
			}
			fmt.Fprintf(stderr, "refine: iter %d — drafting (subprocess)\n", iter)
			code := dispatch(metaPath, stdout, stderr)
			metrics.DraftDispatchExit = code
			if code != 0 {
				fmt.Fprintf(stderr, "refine: draft dispatch exited %d at iter %d\n", code, iter)
				return failsoft(iter, "draft-dispatch", code)
			}
		} else {
			metaPath := filepath.Join(iterDir, "meta-patch.md")
			prevReviewPath := filepath.Join(stateDir, fmt.Sprintf("iter-%d", iter-1), "review.md")
			if err := writeRefinePatchMeta(metaPath, lastDraftPath, prevReviewPath, draftPath); err != nil {
				fmt.Fprintf(stderr, "refine: write patch meta: %v\n", err)
				return failsoft(iter, "patch-meta-write", 1)
			}
			fmt.Fprintf(stderr, "refine: iter %d — patching (subprocess)\n", iter)
			code := dispatch(metaPath, stdout, stderr)
			metrics.DraftDispatchExit = code
			if code != 0 {
				fmt.Fprintf(stderr, "refine: patch dispatch exited %d at iter %d\n", code, iter)
				return failsoft(iter, "patch-dispatch", code)
			}
		}
		if _, err := os.Stat(draftPath); err != nil {
			fmt.Fprintf(stderr, "refine: iter %d produced no draft at %s: %v\n", iter, draftPath, err)
			return failsoft(iter, "draft-missing", 1)
		}
		lastDraftPath = draftPath

		metaReviewPath := filepath.Join(iterDir, "meta-review.md")
		if err := writeRefineReviewMeta(metaReviewPath, draftPath, reviewPath, opts.ProjectRoot); err != nil {
			fmt.Fprintf(stderr, "refine: write review meta: %v\n", err)
			return failsoft(iter, "review-meta-write", 1)
		}
		fmt.Fprintf(stderr, "refine: iter %d — reviewing (subprocess)\n", iter)
		reviewCode := dispatch(metaReviewPath, stdout, stderr)
		metrics.ReviewDispatchExit = reviewCode
		if reviewCode != 0 {
			fmt.Fprintf(stderr, "refine: review dispatch exited %d at iter %d\n", reviewCode, iter)
			return failsoft(iter, "review-dispatch", reviewCode)
		}
		if _, err := os.Stat(reviewPath); err != nil {
			fmt.Fprintf(stderr, "refine: iter %d produced no review at %s: %v\n", iter, reviewPath, err)
			return failsoft(iter, "review-missing", 1)
		}

		reviewBody, err := os.ReadFile(reviewPath)
		if err != nil {
			fmt.Fprintf(stderr, "refine: read review: %v\n", err)
			return failsoft(iter, "review-read", 1)
		}
		verdict := parseRefineVerdict(string(reviewBody))
		bugs := parseRefineBugTitles(string(reviewBody))
		fmt.Fprintf(stderr, "refine: iter %d verdict=%s bugs=%d\n", iter, verdict, len(bugs))
		metrics.Verdict = verdict
		metrics.BugCount = len(bugs)
		if bestBugCount < 0 || len(bugs) < bestBugCount {
			bestDraftPath = lastDraftPath
			bestBugCount = len(bugs)
		}
		if verdict == "ship-as-is" {
			stampMetricsAction("ship-as-is", "")
			return finalizeRefineWithTitle(lastDraftPath, opts.OutputPath, opts.Title, iter, "ship-as-is", stderr)
		}
		if iter > 1 && refineLoopDiverging(prevReviewBugs, bugs) {
			fmt.Fprintf(stderr, "refine: loop diverging at iter %d (same bugs as iter %d); shipping best draft\n", iter, iter-1)
			stampMetricsAction("diverging", "")
			return finalizeRefineWithTitle(bestDraftPath, opts.OutputPath, opts.Title, iter, "diverging", stderr)
		}
		if iter > 1 && len(bugs) >= len(prevReviewBugs) && len(bugs) > 0 {
			fmt.Fprintf(stderr, "refine: no improvement at iter %d (bugs went %d -> %d); stopping and shipping best draft\n", iter, len(prevReviewBugs), len(bugs))
			stampMetricsAction("no-improvement", "")
			return finalizeRefineWithTitle(bestDraftPath, opts.OutputPath, opts.Title, iter, "no-improvement", stderr)
		}
		stampMetricsAction("continue", "")
		prevReviewBugs = bugs
	}
	fmt.Fprintf(stderr, "refine: max iters (%d) reached without ship-as-is verdict; shipping best draft (%d bugs)\n", opts.MaxIters, bestBugCount)
	stampMetricsAction("max-iters-exhausted", "")
	finalDraft := bestDraftPath
	if finalDraft == "" {
		finalDraft = lastDraftPath
	}
	return finalizeRefineWithTitle(finalDraft, opts.OutputPath, opts.Title, opts.MaxIters, "max-iters-exhausted", stderr)
}

func validateRefineOptions(opts *laneRefineOptions) error {
	if strings.TrimSpace(opts.SpecPath) == "" {
		return fmt.Errorf("--spec is required")
	}
	if strings.TrimSpace(opts.OutputPath) == "" {
		return fmt.Errorf("--output is required")
	}
	if strings.TrimSpace(opts.Title) == "" {
		base := filepath.Base(opts.SpecPath)
		opts.Title = strings.TrimSuffix(base, filepath.Ext(base))
	}
	if opts.MaxIters <= 0 {
		opts.MaxIters = 3
	}
	if strings.TrimSpace(opts.ProjectRoot) == "" {
		opts.ProjectRoot = filepath.Dir(opts.SpecPath)
	}
	return nil
}

func refineStateDir(projectRoot, title string) string {
	return filepath.Join(projectRoot, ".idiom", "refine", refineSanitizeTitle(title))
}

var refineUnsafeTitleChars = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func refineSanitizeTitle(title string) string {
	cleaned := refineUnsafeTitleChars.ReplaceAllString(title, "-")
	cleaned = strings.Trim(cleaned, "-")
	if cleaned == "" {
		return "untitled"
	}
	return cleaned
}

func writeRefineDraftMeta(metaPath, draftPath string, opts laneRefineOptions, specBody string) error {
	body := strings.NewReplacer(
		"{{SPEC_BODY}}", strings.TrimSpace(specBody),
		"{{DRAFT_PATH}}", refineWorkspaceRel(opts.ProjectRoot, draftPath),
		"{{TITLE}}", opts.Title,
	).Replace(refineDraftTemplate)
	return os.WriteFile(metaPath, []byte(body), 0o644)
}

func writeRefineReviewMeta(metaPath, draftPath, reviewPath, projectRoot string) error {
	body := strings.NewReplacer(
		"{{DRAFT_PATH}}", refineWorkspaceRel(projectRoot, draftPath),
		"{{REVIEW_PATH}}", refineWorkspaceRel(projectRoot, reviewPath),
		"{{PROJECT_ROOT}}", projectRoot,
	).Replace(refineReviewTemplate)
	return os.WriteFile(metaPath, []byte(body), 0o644)
}

func writeRefinePatchMeta(metaPath, prevDraftPath, prevReviewPath, newDraftPath string) error {
	projectRoot := refineProjectRootFromIterDir(filepath.Dir(metaPath))
	body := strings.NewReplacer(
		"{{PREV_DRAFT_PATH}}", refineWorkspaceRel(projectRoot, prevDraftPath),
		"{{PREV_REVIEW_PATH}}", refineWorkspaceRel(projectRoot, prevReviewPath),
		"{{NEW_DRAFT_PATH}}", refineWorkspaceRel(projectRoot, newDraftPath),
	).Replace(refinePatchTemplate)
	return os.WriteFile(metaPath, []byte(body), 0o644)
}

// refineWorkspaceRel converts an absolute path into a workspace-
// relative one when projectRoot is its ancestor. This is critical
// for RequiredEditPathsFromPrompt — pathparse's
// LooksLikeWorkspacePathToken rejects any token starting with "/",
// so absolute paths in the meta-brief's "Files To Modify" table
// never populate RuntimeBrief.RequiredEditPaths. Empty required-
// edit-paths means the auto-salvage path (#86) skips with
// `count=0, want 1`, and the lane wedges on the completion gate
// when the model forgets to call write_file. Returns the input
// unchanged when the relative computation fails (different drive,
// projectRoot empty, etc.) so the substitution stays well-formed.
func refineWorkspaceRel(projectRoot, p string) string {
	if strings.TrimSpace(projectRoot) == "" || strings.TrimSpace(p) == "" {
		return p
	}
	rel, err := filepath.Rel(projectRoot, p)
	if err != nil || strings.HasPrefix(rel, "..") {
		return p
	}
	return rel
}

// refineProjectRootFromIterDir derives the project root from a
// state-dir iter path. The state dir layout is fixed:
// <projectRoot>/.idiom/refine/<title>/iter-N/. Walking up four
// segments recovers projectRoot. Used by writeRefinePatchMeta which
// otherwise doesn't have a projectRoot argument; rather than thread
// it through every iter, derive it from the path the caller already
// computed.
func refineProjectRootFromIterDir(iterDir string) string {
	dir := iterDir
	for i := 0; i < 4; i++ {
		dir = filepath.Dir(dir)
	}
	return dir
}

var refineVerdictRe = regexp.MustCompile(`(?im)^##\s*Verdict\s*$`)

func parseRefineVerdict(reviewBody string) string {
	idx := refineVerdictRe.FindStringIndex(reviewBody)
	if idx == nil {
		return "unknown"
	}
	tail := reviewBody[idx[1]:]
	for _, line := range strings.Split(tail, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		token := strings.ToLower(strings.Fields(trimmed)[0])
		token = strings.TrimRight(token, ".,;:")
		switch token {
		case "ship-as-is", "fix-bugs-then-ship", "reject-and-redraft":
			return token
		}
		return "unknown"
	}
	return "unknown"
}

// refineBugTitleRe matches a numbered bullet `N. **<title>**`. The
// (?s) flag is added at use-site so we can capture the body too.
var refineBugTitleRe = regexp.MustCompile(`(?m)^\d+\.\s+\*\*([^*]+?)\*\*`)

// refineNoFixPhrases identifies entries that the reviewer wrote in
// the "Bugs found" section but actually marked as verifications /
// non-issues. Qwen3's self-critique sometimes mixes positive findings
// in with real bugs (HF discussion #20: "tends to continue rather
// than diagnose") — these phrases let us filter the verifications
// out so bestBugCount + refineLoopDiverging see accurate signals.
var refineNoFixPhrases = []string{
	"no fix needed",
	"no fix required",
	"no bug",
	"no issue",
	"this is correct",
	"is correct as written",
	"actually correct",
	"verified correct",
	"i verified",
	"all anchors verified",
	"no compile issue",
	"no change required",
	"no changes needed",
	"is valid go",
	"is valid python",
	"is valid",
}

// refineVerificationTitleRe matches titles that are themselves
// verifications (not real bugs). Common shapes:
//   - "All X verified", "All X correct"
//   - "No X needed", "No X required", "No X issues"
//   - titles starting with "verified" / "verification of"
var refineVerificationTitleRe = regexp.MustCompile(`(?i)^(all\s+\w+\s+(verified|correct)|no\s+\w+\s+(needed|required|issue|issues|problem|problems|bug|bugs)|verified|verification of)\b`)

// parseRefineBugTitles extracts the titles of REAL bugs from a
// review's "Bugs found" section. Entries whose body (next ~400
// chars) contains any of refineNoFixPhrases are skipped — those
// are verifications the reviewer mis-formatted as numbered bugs.
func parseRefineBugTitles(reviewBody string) []string {
	indices := refineBugTitleRe.FindAllStringSubmatchIndex(reviewBody, -1)
	if len(indices) == 0 {
		return nil
	}
	out := make([]string, 0, len(indices))
	for i, idx := range indices {
		title := strings.TrimSpace(reviewBody[idx[2]:idx[3]])
		title = strings.TrimRight(title, ":")
		if title == "" {
			continue
		}
		// Title-shape filter: "All X verified", "No X needed", etc.
		if refineVerificationTitleRe.MatchString(title) {
			continue
		}
		// Body is from end-of-title to start-of-next-entry (or 400
		// chars, whichever is shorter — long bodies past 400 chars
		// rarely contain a "no fix needed" stinger).
		bodyStart := idx[1]
		bodyEnd := len(reviewBody)
		if i+1 < len(indices) {
			bodyEnd = indices[i+1][0]
		}
		if bodyEnd-bodyStart > 400 {
			bodyEnd = bodyStart + 400
		}
		body := strings.ToLower(reviewBody[bodyStart:bodyEnd])
		skip := false
		for _, phrase := range refineNoFixPhrases {
			if strings.Contains(body, phrase) {
				skip = true
				break
			}
		}
		if skip {
			continue
		}
		out = append(out, strings.ToLower(title))
	}
	sort.Strings(out)
	return out
}

func refineLoopDiverging(previous, current []string) bool {
	if len(current) == 0 {
		return false
	}
	prev := make(map[string]struct{}, len(previous))
	for _, b := range previous {
		prev[b] = struct{}{}
	}
	for _, b := range current {
		if _, ok := prev[b]; !ok {
			return false
		}
	}
	return true
}

func finalizeRefine(draftPath, outputPath string, iter int, reason string, stderr io.Writer) int {
	return finalizeRefineWithTitle(draftPath, outputPath, "", iter, reason, stderr)
}

// finalizeRefineWithTitle is the marker-aware variant. The marker line
// becomes the FIRST line of the output brief so detectBriefProvenance
// can identify refine-drafted briefs in lane history. Reads draft +
// runs brief lint + writes output (with marker prepended).
func finalizeRefineWithTitle(draftPath, outputPath, title string, iter int, reason string, stderr io.Writer) int {
	body, err := os.ReadFile(draftPath)
	if err != nil {
		fmt.Fprintf(stderr, "refine: cannot read final draft %s: %v\n", draftPath, err)
		return 1
	}
	if code := laneBriefLintRunner([]string{draftPath}, io.Discard, stderr); code != 0 {
		fmt.Fprintf(stderr, "refine: WARN brief lint reported issues on final draft (continuing anyway)\n")
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		fmt.Fprintf(stderr, "refine: cannot create output dir: %v\n", err)
		return 1
	}
	marker := fmt.Sprintf("<!-- idiom:refine-drafted title=%s iter=%d reason=%s -->\n", refineSanitizeTitle(title), iter, reason)
	finalBody := append([]byte(marker), body...)
	if err := os.WriteFile(outputPath, finalBody, 0o644); err != nil {
		fmt.Fprintf(stderr, "refine: cannot write output %s: %v\n", outputPath, err)
		return 1
	}
	fmt.Fprintf(stderr, "refine: converged after %d iter(s) [%s]; wrote %s (%d bytes) at %s\n", iter, reason, outputPath, len(finalBody), time.Now().Format(time.RFC3339))
	return 0
}
