package main

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
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
	LintExit           int    `json:"lint_exit,omitempty"` // §2.3 — captured before review
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

//go:embed lanes/refine/draft-markdown.md
var refineDraftMarkdownTemplate string

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
	// Mode overrides the auto-detected deliverable shape. Empty =
	// auto-detect via refineDetectMode; "anchored" or "markdown"
	// override the heuristic for ambiguous specs. See docs §2.2.
	Mode string
}

// refineDispatchFn is the per-step dispatcher. Default impl shells
// out to `idiom lane implement`; tests inject a fake.
type refineDispatchFn func(metaBriefPath string, stdout, stderr io.Writer) int

// Refine routes ALL phases to a 35B-A3B instance running with
// enable_thinking=false (alias qwen3.6-35b-nothink, pool
// qwen-35b-nothink). Earlier iterations tried Coder-30B with
// thinking off (#98); Coder's chat-template-args weren't fully
// suppressing <think> emission (#100), producing intermittent
// runaway-reasoning even when explicitly disabled. 35B has the
// reasoning capacity to handle the refine work in one pass —
// running it WITHOUT thinking gives us model capability without
// the budget hog. Same skeleton-first templates, same per-phase
// max_tokens.
const refineDefaultPool = "qwen-35b-nothink"
const refineDefaultModel = "qwen3.6-35b-nothink"

// inferRefinePhase examines the meta-brief filename to decide which
// refine phase is dispatching. lane_refine.go names these files
// deterministically (meta-draft.md / meta-review.md / meta-patch.md)
// so a simple suffix check is safe. Kept exported on the seam so
// future per-phase routing can plug in here.
func inferRefinePhase(metaBriefPath string) string {
	base := filepath.Base(metaBriefPath)
	switch {
	case strings.HasPrefix(base, "meta-draft"):
		return "draft"
	case strings.HasPrefix(base, "meta-review"):
		return "review"
	case strings.HasPrefix(base, "meta-patch"):
		return "patch"
	default:
		return "draft"
	}
}

// refinePhaseRouting picks pool + model for a refine subprocess
// based on the phase inferred from the meta-brief filename. All
// phases currently target Coder-30B. Coder still emits <think>
// blocks despite no enable_thinking in chat-template-args (#97
// finding) — the reasoning_content captures whatever the model
// "thinks" through internally, and that eats into max_tokens just
// like 35B thinking-mode. Draft is short enough at 8192; review +
// patch need 16384 because they ingest prior artifacts and emit
// long structured deliverables.
func refinePhaseRouting(metaBriefPath string) (pool, model string, maxTokens int) {
	phase := inferRefinePhase(metaBriefPath)
	switch phase {
	case "review", "patch":
		return refineDefaultPool, refineDefaultModel, 16384
	default: // "draft"
		return refineDefaultPool, refineDefaultModel, 8192
	}
}

func defaultRefineDispatch(metaBriefPath string, stdout, stderr io.Writer) int {
	binary, err := os.Executable()
	if err != nil {
		fmt.Fprintf(stderr, "refine: cannot locate idiom binary: %v\n", err)
		return 2
	}
	pool, model, maxTokens := refinePhaseRouting(metaBriefPath)
	// Route through the local queue daemon so concurrent refine
	// sessions serialize access to whichever MLX backend the phase
	// targets. `idiom queue run` exits non-zero with a clear
	// "start the daemon" message if unreachable, which surfaces
	// correctly to the caller via this function's exit code.
	cmd := exec.Command(binary,
		"queue", "run",
		"--pool", pool,
		"--label", "refine",
		"--quiet",
		"--",
		binary, "lane", "implement", "--no-lint", "--json",
		"--model", model,
		"--max-tokens", strconv.Itoa(maxTokens),
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
	mode := strings.TrimSpace(opts.Mode)
	if mode == "" {
		mode = refineDetectMode(string(specBody))
	}
	fmt.Fprintf(stderr, "refine: mode=%s (auto-detected from spec)\n", mode)

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
		return finalizeRefineWithTitleAndOverlay(bestDraftPath, opts.OutputPath, opts.Title, iter, "failsoft-"+reason, opts.ProjectRoot, stderr)
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
			if err := writeRefineDraftMeta(metaPath, draftPath, opts, string(specBody), mode, stderr); err != nil {
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
			// §6.1 Gap 1 (Colony shipped): patcher targets lint BLOCKs
			// directly. Re-capture lint on the previous draft so the
			// patcher consumes the same authoritative findings the
			// reviewer saw — eliminates the reviewer-transcription
			// indirection layer that the convergence guarantee
			// depended on.
			prevIterDir := filepath.Join(stateDir, fmt.Sprintf("iter-%d", iter-1))
			_, prevLintOutput := captureRefineLintOutput(lastDraftPath, prevIterDir, stderr)
			if err := writeRefinePatchMeta(metaPath, lastDraftPath, prevReviewPath, draftPath, prevLintOutput); err != nil {
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
		if trivial, reason := refineDraftIsTrivial(draftPath); trivial {
			fmt.Fprintf(stderr, "refine: iter %d produced a trivially-empty draft at %s (%s); treating as draft-missing\n", iter, draftPath, reason)
			return failsoft(iter, "draft-trivial", 1)
		}
		lastDraftPath = draftPath

		lintExit, lintOutput := captureRefineLintOutput(draftPath, iterDir, stderr)
		metrics.LintExit = lintExit
		fmt.Fprintf(stderr, "refine: iter %d — lint exit=%d (preloaded to reviewer)\n", iter, lintExit)
		metaReviewPath := filepath.Join(iterDir, "meta-review.md")
		if err := writeRefineReviewMeta(metaReviewPath, draftPath, reviewPath, opts.ProjectRoot, lintOutput); err != nil {
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
			return finalizeRefineWithTitleAndOverlay(lastDraftPath, opts.OutputPath, opts.Title, iter, "ship-as-is", opts.ProjectRoot, stderr)
		}
		if iter > 1 && refineLoopDiverging(prevReviewBugs, bugs) {
			fmt.Fprintf(stderr, "refine: loop diverging at iter %d (same bugs as iter %d); shipping best draft\n", iter, iter-1)
			stampMetricsAction("diverging", "")
			return finalizeRefineWithTitleAndOverlay(bestDraftPath, opts.OutputPath, opts.Title, iter, "diverging", opts.ProjectRoot, stderr)
		}
		if iter > 1 && len(bugs) >= len(prevReviewBugs) && len(bugs) > 0 {
			fmt.Fprintf(stderr, "refine: no improvement at iter %d (bugs went %d -> %d); stopping and shipping best draft\n", iter, len(prevReviewBugs), len(bugs))
			stampMetricsAction("no-improvement", "")
			return finalizeRefineWithTitleAndOverlay(bestDraftPath, opts.OutputPath, opts.Title, iter, "no-improvement", opts.ProjectRoot, stderr)
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
	return finalizeRefineWithTitleAndOverlay(finalDraft, opts.OutputPath, opts.Title, opts.MaxIters, "max-iters-exhausted", opts.ProjectRoot, stderr)
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

func writeRefineDraftMeta(metaPath, draftPath string, opts laneRefineOptions, specBody, mode string, stderr io.Writer) error {
	template, source, err := resolveRefineDraftTemplate(opts.ProjectRoot, mode)
	if err != nil {
		return err
	}
	if strings.HasPrefix(source, "project-override:") {
		fmt.Fprintf(stderr, "refine: draft template = %s\n", source)
		template = ensureRefineDraftWriteClosure(template, stderr, source)
	}
	body := strings.NewReplacer(
		"{{SPEC_BODY}}", strings.TrimSpace(specBody),
		"{{DRAFT_PATH}}", refineWorkspaceRel(opts.ProjectRoot, draftPath),
		"{{TITLE}}", opts.Title,
	).Replace(template)
	return os.WriteFile(metaPath, []byte(body), 0o644)
}

// resolveRefineDraftTemplate walks the §2.2.1 resolution chain:
//  1. Project override at <projectRoot>/.idiom/prompts/refine/draft-<mode>.md
//  2. Embedded canonical template (anchored or markdown)
//  3. Unknown mode → actionable error pointing at the override convention
//
// The override file is lint-checked at load time (validateRefineDraftOverride)
// so a malformed template fails loud rather than silently shipping a
// template-fragment as a brief.
func resolveRefineDraftTemplate(projectRoot, mode string) (template, source string, err error) {
	if strings.TrimSpace(mode) == "" {
		return "", "", fmt.Errorf("resolveRefineDraftTemplate: mode is required")
	}
	if projectRoot != "" {
		overridePath := filepath.Join(projectRoot, refineOverrideDir, "draft-"+mode+".md")
		if body, readErr := os.ReadFile(overridePath); readErr == nil {
			if lintErr := validateRefineDraftOverride(string(body), overridePath); lintErr != nil {
				return "", "", lintErr
			}
			return string(body), "project-override:" + overridePath, nil
		}
	}
	switch mode {
	case refineModeAnchored:
		return refineDraftTemplate, "embedded:anchored", nil
	case refineModeMarkdown:
		return refineDraftMarkdownTemplate, "embedded:markdown", nil
	}
	overrideHint := filepath.Join(projectRoot, refineOverrideDir, "draft-"+mode+".md")
	return "", "", fmt.Errorf("unknown refine mode %q: no project override at %s and not in canonical set [%s, %s]", mode, overrideHint, refineModeAnchored, refineModeMarkdown)
}

// refineOverrideDir is the project-side convention path where override
// templates live. Kept separate from .idiom/prompts/brief/ (which §2.4
// uses for prohibitions + traps overlay) because modes are a different
// abstraction — templates, not augmentations.
const refineOverrideDir = ".idiom/prompts/refine"

// refineOverlayDir is the project-side convention path for §2.4 brief
// overlays (HARD PROHIBITIONS + atom-shape-targeted traps). Files
// under this directory augment the canonical brief at finalize time:
//
//	<projectRoot>/.idiom/prompts/brief/
//	├── hard-prohibitions.md     # appended verbatim to every brief
//	└── traps/
//	    ├── test-code.md         # frontmatter: applies_to: [test-code]
//	    └── multi-file.md        # applies_to: [multi-file]
//
// Bare projects (no overlay dir) see zero behavior change.
const refineOverlayDir = ".idiom/prompts/brief"

// Canonical atom-shape tags emitted by classifyBriefAtomShape. Kept
// minimal and language-agnostic per Colony's Q3 review (2026-05-11):
// `save-load`, `c++-system`, etc. live in project overlays, not in
// idiom's canonical surface. Projects extend via their own trap
// files' applies_to fields.
const (
	refineTagTestCode            = "test-code"
	refineTagMultiFile           = "multi-file"
	refineTagMarkdownDeliverable = "markdown-deliverable"
	refineTagGeneric             = "generic"
)

// refineApplyProjectOverlay appends project HARD PROHIBITIONS and
// atom-shape-matched trap entries to the final brief body. No-op on
// bare projects (no overlay dir). Called from finalize time so the
// output brief is the hardened version while iter-N drafts stay
// clean. Stderr-logs which overlay components fired for debugging.
func refineApplyProjectOverlay(body []byte, projectRoot string, stderr io.Writer) []byte {
	if strings.TrimSpace(projectRoot) == "" {
		return body
	}
	overlayDir := filepath.Join(projectRoot, refineOverlayDir)
	if _, err := os.Stat(overlayDir); err != nil {
		return body
	}
	augmented := body
	if hp, ok := loadHardProhibitions(overlayDir); ok {
		augmented = append(augmented, '\n', '\n')
		augmented = append(augmented, hp...)
		fmt.Fprintf(stderr, "refine: appended hard-prohibitions overlay (%d bytes)\n", len(hp))
	}
	tags := classifyBriefAtomShape(body)
	fmt.Fprintf(stderr, "refine: atom-shape tags = %v\n", tags)
	traps := loadProjectTraps(overlayDir, tags, stderr)
	for _, trap := range traps {
		augmented = append(augmented, '\n', '\n')
		augmented = append(augmented, trap.body...)
		fmt.Fprintf(stderr, "refine: inlined trap %s (applies_to=%v)\n", trap.name, trap.appliesTo)
	}
	return augmented
}

// loadHardProhibitions returns (body, true) if the project's
// hard-prohibitions.md exists under overlayDir. Body is returned
// verbatim — no substitution, no parsing. Projects own the wording.
func loadHardProhibitions(overlayDir string) ([]byte, bool) {
	path := filepath.Join(overlayDir, "hard-prohibitions.md")
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, false
	}
	return body, true
}

// trapEntry pairs a trap file's metadata with its body content
// (frontmatter stripped). Used internally by loadProjectTraps.
type trapEntry struct {
	name      string
	appliesTo []string
	body      []byte
}

// loadProjectTraps scans <overlayDir>/traps/*.md, parses each file's
// frontmatter, and returns the entries whose applies_to tags
// intersect the supplied tag set. Traps with no frontmatter / no
// applies_to are treated as `applies_to: [generic]` (always include).
// Returned slice is sorted by filename for deterministic ordering.
//
// §6.1 Gap 3 (Colony loud-warning ship): when a trap declares
// applies_to tags that aren't in the classifier's emitted tag set
// (refineCanonicalTags), emit a loud stderr warning. Without this,
// projects writing `applies_to: [save-load]` traps silently get
// zero firings because the canonical classifier doesn't emit
// `save-load`. The warning names the orphan tag and points at the
// future atom-shapes.yaml extension point so authors can fix the
// declaration or accept the deferral.
func loadProjectTraps(overlayDir string, tags []string, stderr io.Writer) []trapEntry {
	trapsDir := filepath.Join(overlayDir, "traps")
	entries, err := os.ReadDir(trapsDir)
	if err != nil {
		return nil
	}
	tagSet := map[string]bool{}
	for _, t := range tags {
		tagSet[t] = true
	}
	canonicalSet := map[string]bool{}
	for _, t := range refineCanonicalTags {
		canonicalSet[t] = true
	}
	var matched []trapEntry
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		path := filepath.Join(trapsDir, entry.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		appliesTo, body := parseTrapFrontmatter(raw)
		if len(appliesTo) == 0 {
			appliesTo = []string{refineTagGeneric}
		}
		// Orphan-tag detection (§6.1 Gap 3 loud-warning).
		for _, tag := range appliesTo {
			if canonicalSet[tag] {
				continue
			}
			if stderr != nil {
				fmt.Fprintf(stderr,
					"refine: WARN trap %s declares applies_to=[%s] but no atom-shape classifier emits %q. Trap will never fire unless added to .idiom/prompts/brief/atom-shapes.yaml (deferred — see docs/refine-end-to-end-loop.md §6.1 Gap 3) or its applies_to is updated to a canonical tag [%s].\n",
					entry.Name(), strings.Join(appliesTo, ", "), tag, strings.Join(refineCanonicalTags, ", "),
				)
			}
		}
		if !tagsIntersect(appliesTo, tagSet) {
			continue
		}
		matched = append(matched, trapEntry{
			name:      entry.Name(),
			appliesTo: appliesTo,
			body:      bytes.TrimSpace(body),
		})
	}
	sort.Slice(matched, func(i, j int) bool { return matched[i].name < matched[j].name })
	return matched
}

// refineCanonicalTags is the closed set of atom-shape tags the
// classifier currently emits. Project trap declarations using tags
// outside this set trigger the orphan-tag warning above. Until §6.1
// Gap 3's full feature ships (atom-shapes.yaml project-extending
// detection), only these four tags can drive trap injection.
var refineCanonicalTags = []string{
	refineTagTestCode,
	refineTagMultiFile,
	refineTagMarkdownDeliverable,
	refineTagGeneric,
}

// parseTrapFrontmatter accepts a markdown file body with optional
// `---` frontmatter and returns (applies_to, body-without-frontmatter).
// The parser is intentionally narrow: it looks for an `applies_to:
// [a, b, c]` line inside the first `---`-delimited block. No YAML
// library; brittle on purpose — the format is documented and stable.
func parseTrapFrontmatter(raw []byte) ([]string, []byte) {
	text := string(raw)
	if !strings.HasPrefix(strings.TrimLeft(text, "\n"), "---") {
		return nil, raw
	}
	// Skip leading newlines.
	stripped := strings.TrimLeft(text, "\n")
	// Find the closing `---` after the opening one.
	rest := stripped[3:]
	endIdx := strings.Index(rest, "\n---")
	if endIdx < 0 {
		return nil, raw
	}
	frontmatter := rest[:endIdx]
	body := strings.TrimLeft(rest[endIdx+4:], "\n")
	var appliesTo []string
	for _, line := range strings.Split(frontmatter, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "applies_to:") {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(line, "applies_to:"))
		value = strings.TrimPrefix(value, "[")
		value = strings.TrimSuffix(value, "]")
		for _, tok := range strings.Split(value, ",") {
			tok = strings.TrimSpace(tok)
			tok = strings.Trim(tok, `"'`)
			if tok != "" {
				appliesTo = append(appliesTo, tok)
			}
		}
		break
	}
	return appliesTo, []byte(body)
}

func tagsIntersect(want []string, have map[string]bool) bool {
	for _, t := range want {
		if have[t] {
			return true
		}
	}
	return false
}

// classifyBriefAtomShape emits canonical atom-shape tags by examining
// the Files-to-Modify section of the brief body. Returns at least one
// tag — `generic` is the fallback. Multiple tags may apply (a brief
// that touches multiple test files in different packages emits both
// `test-code` and `multi-file`). Per Colony Q3: canonical surface is
// minimal and language-agnostic; project-specific tags live in
// overlay traps' applies_to fields, not here.
func classifyBriefAtomShape(body []byte) []string {
	files := extractFilesToModify(string(body))
	tags := []string{}
	seen := map[string]bool{}
	add := func(t string) {
		if !seen[t] {
			tags = append(tags, t)
			seen[t] = true
		}
	}
	if filesContainTestCode(files) {
		add(refineTagTestCode)
	}
	if filesSpanMultipleDirs(files) {
		add(refineTagMultiFile)
	}
	if filesAreAllMarkdownDeliverables(files) {
		add(refineTagMarkdownDeliverable)
	}
	if len(tags) == 0 {
		add(refineTagGeneric)
	}
	return tags
}

// extractFilesToModify pulls the file paths out of a brief's
// "Files To Modify" table. The table shape is enforced by lint:
//
//	| File | Change |
//	|------|--------|
//	| `path/to/file.go` | ADD/MODIFY/NEW FILE: ... |
//
// Returns paths in document order, deduplicated. Skips meta-brief
// paths (e.g. the refine output itself) by exempting paths under
// the project's .idiom/ tree from the result — those are scaffolding,
// not deliverables.
func extractFilesToModify(text string) []string {
	var files []string
	seen := map[string]bool{}
	inSection := false
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)
		if strings.HasPrefix(lower, "## files to modify") || strings.HasPrefix(lower, "## files you may edit") {
			inSection = true
			continue
		}
		if inSection && strings.HasPrefix(trimmed, "## ") {
			inSection = false
		}
		if !inSection {
			continue
		}
		// Strict table row: needs a backtick-quoted path.
		start := strings.Index(trimmed, "`")
		if start < 0 {
			continue
		}
		end := strings.Index(trimmed[start+1:], "`")
		if end < 0 {
			continue
		}
		candidate := strings.TrimSpace(trimmed[start+1 : start+1+end])
		if candidate == "" || seen[candidate] {
			continue
		}
		// Skip refine scaffolding paths (their own meta-brief etc.).
		if strings.Contains(candidate, "/.idiom/refine/") {
			continue
		}
		seen[candidate] = true
		files = append(files, candidate)
	}
	return files
}

func filesContainTestCode(files []string) bool {
	for _, f := range files {
		base := strings.ToLower(filepath.Base(f))
		dir := strings.ToLower(filepath.ToSlash(filepath.Dir(f)))
		if strings.Contains(base, "_test.") || strings.HasPrefix(base, "test_") || strings.HasSuffix(base, ".test.ts") || strings.HasSuffix(base, ".test.js") || strings.HasSuffix(base, ".spec.ts") || strings.HasSuffix(base, ".spec.js") {
			return true
		}
		if strings.Contains(dir, "/tests/") || strings.Contains(dir, "/__tests__/") || strings.HasSuffix(dir, "/test") || strings.HasSuffix(dir, "/tests") {
			return true
		}
	}
	return false
}

func filesSpanMultipleDirs(files []string) bool {
	dirs := map[string]bool{}
	for _, f := range files {
		dirs[filepath.ToSlash(filepath.Dir(f))] = true
		if len(dirs) >= 2 {
			return true
		}
	}
	return false
}

func filesAreAllMarkdownDeliverables(files []string) bool {
	if len(files) == 0 {
		return false
	}
	for _, f := range files {
		ext := strings.ToLower(filepath.Ext(f))
		if ext != ".md" && ext != ".txt" && ext != ".rst" && ext != ".adoc" {
			return false
		}
	}
	return true
}

// refineDraftRequiredPlaceholders lists the substitution tokens a
// project override draft template MUST contain. Missing any one means
// the substituted body would be malformed (no destination path, no
// content guidance) and the dispatched model would wedge. Better to
// fail at template-load time than after a 90s draft phase.
var refineDraftRequiredPlaceholders = []string{"{{DRAFT_PATH}}", "{{SPEC_BODY}}", "{{TITLE}}"}

// validateRefineDraftOverride enforces the §2.2.1 safety lint: every
// project override must carry the placeholders the substitution
// pipeline will replace, OR the resulting meta-brief loses its
// destination path / spec body / title. Also requires at least one
// `## ` heading (a brief without sections won't lint-pass downstream
// anyway). The write_file closure is NOT validated here — see
// ensureRefineDraftWriteClosure: idiom auto-appends a canonical
// write directive when missing so override authors can focus on the
// body content without remembering closure boilerplate.
func validateRefineDraftOverride(body, path string) error {
	for _, ph := range refineDraftRequiredPlaceholders {
		if !strings.Contains(body, ph) {
			return fmt.Errorf("refine override template %s is missing required placeholder %s", path, ph)
		}
	}
	if !strings.Contains(body, "\n## ") && !strings.HasPrefix(strings.TrimSpace(body), "## ") {
		return fmt.Errorf("refine override template %s has no `## ` section heading", path)
	}
	return nil
}

// refineCanonicalWriteClosure is the standard "STOP AND WRITE NOW"
// block appended to override templates that don't include their own
// write directive. Matches the canonical anchored/markdown templates'
// closing section so the dispatched-model contract is consistent
// across all modes — project authors write the body content; the
// harness ensures closure. The {{DRAFT_PATH}} placeholder gets
// substituted by the existing pipeline.
const refineCanonicalWriteClosure = `

## STOP AND WRITE NOW

Your VERY NEXT TOOL CALL MUST be ` + "`write_file`" + ` with:

- ` + "`path`" + `: ` + "`{{DRAFT_PATH}}`" + `
- ` + "`content`" + `: the filled-in brief above, with every ` + "`<bracket>`" + ` replaced.

Do not read files. Do not search. Do not think out loud. The file
content goes inside the ` + "`write_file`" + ` call's ` + "`content`" + ` argument, not in a
chat message. After ` + "`write_file`" + ` returns success, give a single-sentence
final answer and stop.

## Done when

` + "`test -f {{DRAFT_PATH}}`" + ` returns 0.
`

// ensureRefineDraftWriteClosure appends the canonical write-closure
// block when the template body has no write_file directive. The
// project-author UX line: drop a markdown file with body content,
// idiom handles closure boilerplate uniformly. If the override
// author wants different closure wording (e.g. submit_plan instead
// of write_file), they include it in their template and the closure
// is left untouched.
func ensureRefineDraftWriteClosure(body string, stderr io.Writer, source string) string {
	if strings.Contains(strings.ToLower(body), "write_file") {
		return body
	}
	if stderr != nil && source != "" {
		fmt.Fprintf(stderr, "refine: %s has no write_file directive; appending canonical closure\n", source)
	}
	trimmed := strings.TrimRight(body, "\n") + refineCanonicalWriteClosure
	return trimmed
}

// refineMode constants — anchored-edits is the historical default
// (code edits with `<file>:LINE` anchors and old_text/new_text);
// markdown is the §2.2 new mode for NEW markdown deliverables where
// anchors don't apply and the brief just inlines the file content.
const (
	refineModeAnchored = "anchored"
	refineModeMarkdown = "markdown"
)

// refineSourceExtensions are paths that always force anchored-edits
// mode regardless of how many markdown paths also appear. Covers
// compiled and scripted languages, shell, SQL, IaC/config DSLs, and
// schema languages — anything that's anchor-able source. Per
// Colony's Q2 review (2026-05-11): "any source path → anchored,
// regardless of where the output goes" — the inventory-of-source-
// files-written-as-markdown case needs anchored mode even though
// the output is .md.
var refineSourceExtensions = []string{
	// Compiled & scripted languages
	".go", ".cpp", ".cc", ".cxx", ".c", ".h", ".hpp", ".hh",
	".py", ".rs", ".ts", ".tsx", ".js", ".jsx", ".java", ".kt",
	".rb", ".php", ".swift", ".scala", ".cs", ".m", ".mm",
	// Shell — anchor-able sources (lane_postflight.sh-style cases)
	".sh", ".bash", ".zsh", ".fish",
	// SQL — schema/migration files routinely cited by line
	".sql", ".psql", ".plsql",
	// Schema and IaC — anchor-shaped source content
	".proto", ".tf", ".hcl", ".bicep",
}

// refineMarkdownExtensions are paths considered markdown-deliverable
// candidates when no source paths are mentioned.
var refineMarkdownExtensions = []string{".md", ".txt", ".json", ".yaml", ".yml", ".toml"}

// refineDetectMode classifies the spec's deliverable shape. Returns
// refineModeMarkdown ONLY when every cited path is a markdown-class
// extension AND no source-class extension appears. Falls back to
// anchored mode (the safer default) for mixed shapes or specs with
// no path mentions at all. Heuristic — escape hatch via opts.Mode
// override is the design's safety valve (see docs §2.2).
func refineDetectMode(specBody string) string {
	lower := strings.ToLower(specBody)
	hasSource := false
	hasMarkdown := false
	for _, ext := range refineSourceExtensions {
		if extensionMentioned(lower, ext) {
			hasSource = true
			break
		}
	}
	for _, ext := range refineMarkdownExtensions {
		if extensionMentioned(lower, ext) {
			hasMarkdown = true
			break
		}
	}
	if hasMarkdown && !hasSource {
		return refineModeMarkdown
	}
	return refineModeAnchored
}

// extensionMentioned reports whether `ext` (e.g. ".m") appears in
// `text` as a real file extension — followed by a non-alphabetic
// character or end-of-string. Plain strings.Contains fails here
// because ".m" is a substring of ".md", which leaks Objective-C
// matches into every Markdown-only spec.
func extensionMentioned(text, ext string) bool {
	i := 0
	for {
		idx := strings.Index(text[i:], ext)
		if idx < 0 {
			return false
		}
		end := i + idx + len(ext)
		if end >= len(text) {
			return true
		}
		c := text[end]
		// Extension boundary: anything that's not an alphabetic
		// character closes the extension. Digits, punctuation,
		// whitespace, slashes all count as boundary.
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')) {
			return true
		}
		i = end
	}
}

func writeRefineReviewMeta(metaPath, draftPath, reviewPath, projectRoot, lintOutput string) error {
	if strings.TrimSpace(lintOutput) == "" {
		lintOutput = "(no findings — lint pass was clean)"
	}
	body := strings.NewReplacer(
		"{{DRAFT_PATH}}", refineWorkspaceRel(projectRoot, draftPath),
		"{{REVIEW_PATH}}", refineWorkspaceRel(projectRoot, reviewPath),
		"{{PROJECT_ROOT}}", projectRoot,
		"{{LINT_OUTPUT}}", lintOutput,
	).Replace(refineReviewTemplate)
	return os.WriteFile(metaPath, []byte(body), 0o644)
}

// captureRefineLintOutput runs `idiom brief lint --json` on the
// just-produced draft and returns the exit code + a review-ready
// rendering of the JSON findings (§2.3 + §6.1 Gap 5). When persistDir
// is non-empty AND the JSON parsed cleanly, the raw envelope is also
// written to <persistDir>/lint.json (Colony §8 recommendation) so
// orchestrators can inspect lint findings without re-running.
//
// The returned text is BLOCK + WARN findings formatted as a compact
// human/LLM-readable list. INFO findings are dropped (signal-to-noise
// per Colony Q1). When the JSON path can't be parsed (older lint
// versions, json marshal failed, etc.), falls back to the text
// envelope so the reviewer never silently treats lint as clean.
func captureRefineLintOutput(draftPath, persistDir string, stderr io.Writer) (int, string) {
	var out bytes.Buffer
	code := laneBriefLintRunner([]string{draftPath, "--json"}, &out, stderr)
	rawJSON := bytes.TrimSpace(out.Bytes())
	rendered, ok := renderRefineLintFindings(rawJSON)
	if !ok {
		// Fall back: try plain-text capture so the reviewer at least
		// sees something. If even that is empty, emit the fallback
		// "treat as BLOCK" message so empty + exit-non-zero doesn't
		// look like "clean lint" to downstream consumers.
		var plain bytes.Buffer
		_ = laneBriefLintRunner([]string{draftPath}, &plain, stderr)
		text := plain.String()
		if code != 0 && strings.TrimSpace(text) == "" {
			text = fmt.Sprintf("(lint exited %d with no parseable findings — treat as BLOCK)", code)
		}
		return code, text
	}
	if persistDir != "" && len(rawJSON) > 0 {
		persistRefineLintJSON(persistDir, rawJSON, stderr)
	}
	if code != 0 && strings.TrimSpace(rendered) == "" {
		rendered = "(lint exited non-zero but emitted zero BLOCK/WARN findings — investigate manually)"
	}
	return code, rendered
}

// persistRefineLintJSON writes the raw lint envelope to
// <iterDir>/lint.json. Best-effort: any write failure logs to
// stderr and proceeds — the run continues with the in-memory
// preload regardless. Matches the failure-mode contract of
// writeRefineIterMetrics: missing on-disk artifact is downgrade,
// not blocker. Per Colony §8 the full JSON stays available for
// orchestrator inspection; the review preload is the BLOCK/WARN
// filter, not the source of truth.
func persistRefineLintJSON(iterDir string, raw []byte, stderr io.Writer) {
	path := filepath.Join(iterDir, "lint.json")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		fmt.Fprintf(stderr, "refine: WARN cannot persist %s: %v (continuing — in-memory preload still works)\n", path, err)
	}
}

// renderRefineLintFindings parses the JSON envelope from `idiom
// brief lint --json` and renders BLOCK + WARN findings as a compact
// list (INFO dropped). Returns ok=false if the JSON is malformed so
// the caller can fall back gracefully.
func renderRefineLintFindings(raw []byte) (string, bool) {
	var report briefLintReport
	if err := json.Unmarshal(bytes.TrimSpace(raw), &report); err != nil {
		return "", false
	}
	var out strings.Builder
	count := 0
	for _, f := range report.Findings {
		if f.Severity == "INFO" {
			continue
		}
		count++
		fmt.Fprintf(&out, "- [%s] (%s) %s\n", f.Severity, f.Category, f.Message)
	}
	if count == 0 && report.Exit == 0 {
		out.WriteString("(no BLOCK/WARN findings; lint pass clean)\n")
	}
	return strings.TrimRight(out.String(), "\n"), true
}

func writeRefinePatchMeta(metaPath, prevDraftPath, prevReviewPath, newDraftPath, lintOutput string) error {
	projectRoot := refineProjectRootFromIterDir(filepath.Dir(metaPath))
	if strings.TrimSpace(lintOutput) == "" {
		lintOutput = "(no findings — lint pass was clean on the previous draft)"
	}
	body := strings.NewReplacer(
		"{{PREV_DRAFT_PATH}}", refineWorkspaceRel(projectRoot, prevDraftPath),
		"{{PREV_REVIEW_PATH}}", refineWorkspaceRel(projectRoot, prevReviewPath),
		"{{NEW_DRAFT_PATH}}", refineWorkspaceRel(projectRoot, newDraftPath),
		"{{LINT_OUTPUT}}", lintOutput,
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

// refineMinDraftBytes is the minimum size for a draft to be considered
// non-trivial. The smallest plausible brief — Problem + one Files row
// + Acceptance — clears 400 bytes; 200 is a defensive floor that still
// rejects 0-byte writes, single-line error messages, and unfilled
// template fragments. Empirically observed real drafts are 3-5KB.
const refineMinDraftBytes = 200

// refineDraftIsTrivial returns true (with a one-word reason) when the
// file at path is too small or shaped like an unfilled template to be
// a real brief. A draft that passes os.Stat but is empty / template-
// fragment-shaped historically slipped through and propagated as
// exit-0 success even though no real deliverable existed (see
// docs/refine-end-to-end-loop.md §2.1). The cheap content check below
// catches the common shapes: zero-length, whitespace-only, or content
// with no markdown section heading at all.
func refineDraftIsTrivial(path string) (bool, string) {
	info, err := os.Stat(path)
	if err != nil {
		return true, "stat-failed"
	}
	if info.Size() < refineMinDraftBytes {
		return true, fmt.Sprintf("size=%dB<%dB", info.Size(), refineMinDraftBytes)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return true, "read-failed"
	}
	if strings.TrimSpace(string(body)) == "" {
		return true, "whitespace-only"
	}
	if !strings.Contains(string(body), "\n## ") && !strings.HasPrefix(strings.TrimSpace(string(body)), "## ") {
		return true, "no-section-heading"
	}
	return false, ""
}

func finalizeRefine(draftPath, outputPath string, iter int, reason string, stderr io.Writer) int {
	return finalizeRefineWithTitle(draftPath, outputPath, "", iter, reason, stderr)
}

// finalizeRefineWithTitle is the marker-aware variant. The marker line
// becomes the FIRST line of the output brief so detectBriefProvenance
// can identify refine-drafted briefs in lane history. Reads draft +
// runs brief lint + writes output (with marker prepended).
//
// Exit-code contract (Colony's Q4 review, 2026-05-11): ONLY a
// `ship-as-is` reason returns 0. Every other reason (failsoft-*,
// diverging, no-improvement, max-iters-exhausted) returns non-zero
// AND prepends a sentinel WARNING blockquote so the artifact is
// self-documenting. The orchestrator must inspect rather than auto-
// pipe to plan-execute on non-zero exits. Saving the best draft on
// non-zero exit preserves debugging signal — the dichotomy isn't
// "save vs. fail-loud", it's "save AND fail-loud."
func finalizeRefineWithTitle(draftPath, outputPath, title string, iter int, reason string, stderr io.Writer) int {
	return finalizeRefineWithTitleAndOverlay(draftPath, outputPath, title, iter, reason, "", stderr)
}

// finalizeRefineWithTitleAndOverlay is the §2.4-aware finalize path.
// Applies project overlay (HARD PROHIBITIONS + atom-shape-matched
// traps) BEFORE the warning header so the hardened brief is what
// downstream consumers see. Empty projectRoot disables overlay
// (bare-repo behavior preserved).
func finalizeRefineWithTitleAndOverlay(draftPath, outputPath, title string, iter int, reason, projectRoot string, stderr io.Writer) int {
	if trivial, why := refineDraftIsTrivial(draftPath); trivial {
		fmt.Fprintf(stderr, "refine: refusing to finalize trivial draft %s (%s); exiting non-zero rather than writing a silent-failure output\n", draftPath, why)
		return 1
	}
	body, err := os.ReadFile(draftPath)
	if err != nil {
		fmt.Fprintf(stderr, "refine: cannot read final draft %s: %v\n", draftPath, err)
		return 1
	}
	body = refineApplyProjectOverlay(body, projectRoot, stderr)
	if code := laneBriefLintRunner([]string{draftPath}, io.Discard, stderr); code != 0 {
		fmt.Fprintf(stderr, "refine: WARN brief lint reported issues on final draft (continuing anyway)\n")
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		fmt.Fprintf(stderr, "refine: cannot create output dir: %v\n", err)
		return 1
	}
	isShipAsIs := reason == "ship-as-is"
	var prefix []byte
	if !isShipAsIs {
		prefix = []byte(fmt.Sprintf("> WARNING: refine exited non-zero — reason=%s after iter %d. The brief below is the best draft the loop produced; review for unresolved bugs/BLOCKs before dispatching to plan-execute. Inspect `.idiom/refine/%s/iter-%d/{review.md,metrics.json}` for findings.\n\n", reason, iter, refineSanitizeTitle(title), iter))
	}
	marker := fmt.Sprintf("<!-- idiom:refine-drafted title=%s iter=%d reason=%s -->\n", refineSanitizeTitle(title), iter, reason)
	finalBody := append(prefix, []byte(marker)...)
	finalBody = append(finalBody, body...)
	if err := os.WriteFile(outputPath, finalBody, 0o644); err != nil {
		fmt.Fprintf(stderr, "refine: cannot write output %s: %v\n", outputPath, err)
		return 1
	}
	if isShipAsIs {
		fmt.Fprintf(stderr, "refine: converged ship-as-is after %d iter(s); wrote %s (%d bytes) at %s\n", iter, outputPath, len(finalBody), time.Now().Format(time.RFC3339))
		return 0
	}
	fmt.Fprintf(stderr, "refine: did NOT reach ship-as-is (%s) after %d iter(s); wrote best-draft to %s (%d bytes) with WARNING header — exiting non-zero so orchestrator inspects before piping\n", reason, iter, outputPath, len(finalBody))
	return 1
}
