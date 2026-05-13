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

// Refine routes ALL phases to the primary 35B-A3B backend while
// passing --no-thinking to the wrapped implement lane. Earlier
// iterations used a second qwen3.6-35b-nothink MLX process because
// mlx_lm.server did not accept request-level chat_template_kwargs.
// Current mlx-lm honors enable_thinking=false per request, so refine
// keeps the no-thinking behavior without loading duplicate 35B
// weights. Same skeleton-first templates, same per-phase max_tokens.
const refineDefaultPool = "qwen-35b"
const refineDefaultModel = "qwen3.6-35b"

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
// based on the phase inferred from the meta-brief filename. Review is
// intentionally smaller to keep runaway prose contained if a model
// ignores the write_file instruction; patch and draft need enough
// room to emit the full corrected draft. Read/write discipline
// belongs in the phase prompt and runner readiness guidance, not in a
// wrapper-local fixed tool-step ceiling that can mask stale specs as
// "budget" failures.
func refinePhaseRouting(metaBriefPath string) (pool, model string, maxTokens int) {
	phase := inferRefinePhase(metaBriefPath)
	switch phase {
	case "review":
		return refineDefaultPool, refineDefaultModel, 4096
	case "patch":
		return refineDefaultPool, refineDefaultModel, 16384
	default: // "draft"
		return refineDefaultPool, refineDefaultModel, 16384
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
		"--no-thinking",
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
	if blocker := atomSpecDispatchBlocker(string(specBody)); blocker != "" {
		fmt.Fprintf(stderr, "refine: refusing spec %s: %s\n", opts.SpecPath, blocker)
		return 1
	}
	mode := strings.TrimSpace(opts.Mode)
	if mode == "" {
		mode = refineDetectMode(string(specBody))
	}
	fmt.Fprintf(stderr, "refine: mode=%s (auto-detected from spec)\n", mode)

	// §6.1 Gap 6 (Colony harness-side fix): extract the deliverable
	// targets once and validate every iter's draft against them.
	// Gap 8 (Colony 2026-05-12): persist the targets as stateDir-level
	// sidecars so downstream tooling (lane-history readers,
	// plan-execute integrations, audit harness) can read the
	// harness-extracted routing targets without re-parsing the brief.
	// Empty target list = no sidecars (extraction failed at spec-parse
	// time).
	specDeliverableTargets := extractDeliverableTargets(string(specBody), mode)
	if len(specDeliverableTargets) > 0 {
		targetBody := strings.Join(specDeliverableTargets, "\n") + "\n"
		if err := os.WriteFile(filepath.Join(stateDir, "deliverable_targets.txt"), []byte(targetBody), 0o644); err != nil {
			fmt.Fprintf(stderr, "refine: cannot write deliverable_targets sidecar: %v\n", err)
			// Non-fatal: the sidecar is observability, not behavior.
		}
		// Compatibility sidecar for older readers that only expect one
		// target. Multi-file anchored specs read deliverable_targets.txt.
		sidecarPath := filepath.Join(stateDir, "deliverable_target.txt")
		if err := os.WriteFile(sidecarPath, []byte(specDeliverableTargets[0]+"\n"), 0o644); err != nil {
			fmt.Fprintf(stderr, "refine: cannot write deliverable_target sidecar %s: %v\n", sidecarPath, err)
			// Non-fatal: the sidecar is observability, not behavior.
		}
	}

	var prevReviewBugs []string
	var lastDraftPath string
	bestDraftPath := ""
	bestLintExit := -1
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
		return finalizeRefineWithTitleAndOverlay(bestDraftPath, opts.OutputPath, opts.Title, iter, "failsoft-"+reason, opts.ProjectRoot, mode, specDeliverableTargets, stderr)
	}

	for iter := 1; iter <= opts.MaxIters; iter++ {
		iterDir := filepath.Join(stateDir, fmt.Sprintf("iter-%d", iter))
		if err := os.MkdirAll(iterDir, 0o755); err != nil {
			fmt.Fprintf(stderr, "refine: cannot create iter dir %s: %v\n", iterDir, err)
			return failsoft(iter, "iter-dir-mkdir", 1)
		}
		draftPath := filepath.Join(iterDir, "draft.md")
		reviewPath := filepath.Join(iterDir, "review.md")
		if err := clearRefineOutputPath(draftPath, stderr); err != nil {
			fmt.Fprintf(stderr, "refine: cannot clear stale draft %s: %v\n", draftPath, err)
			return failsoft(iter, "draft-clear", 1)
		}
		if err := clearRefineOutputPath(reviewPath, stderr); err != nil {
			fmt.Fprintf(stderr, "refine: cannot clear stale review %s: %v\n", reviewPath, err)
			return failsoft(iter, "review-clear", 1)
		}

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
			_, prevLintOutput := captureRefineLintOutput(lastDraftPath, prevIterDir, opts.ProjectRoot, mode, specDeliverableTargets, stderr)
			if err := writeRefinePatchMeta(metaPath, lastDraftPath, prevReviewPath, draftPath, prevLintOutput, specDeliverableTargets); err != nil {
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

		lintExit, lintOutput := captureRefineLintOutput(draftPath, iterDir, opts.ProjectRoot, mode, specDeliverableTargets, stderr)
		// §6.1 Gap 6 (Colony harness-side fix): validate the draft's
		// Files To Modify references the extracted deliverable target.
		// If the model put refine's scratchpad path there instead, the
		// brief is structurally broken — surface as a BLOCK so the
		// reviewer + patcher both see it as a required edit target.
		if mismatch := refineDeliverableTargetMismatch(draftPath, specDeliverableTargets); mismatch != "" {
			lintOutput = mismatch + "\n" + lintOutput
			if lintExit == 0 {
				lintExit = 1
			}
			fmt.Fprintf(stderr, "refine: iter %d — deliverable-target mismatch detected; prepending BLOCK to review preload\n", iter)
		}
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
			if wrote, fallbackErr := writeFallbackRefineReview(reviewPath, lintOutput, "review-missing", stderr); fallbackErr != nil {
				fmt.Fprintf(stderr, "refine: fallback review write failed at iter %d: %v\n", iter, fallbackErr)
			} else if wrote {
				fmt.Fprintf(stderr, "refine: iter %d reviewer wrote no file; synthesized review from lint BLOCKs at %s\n", iter, reviewPath)
			} else {
				fmt.Fprintf(stderr, "refine: iter %d produced no review at %s: %v\n", iter, reviewPath, err)
				return failsoft(iter, "review-missing", 1)
			}
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
		if refineDraftScoreBetter(lintExit, len(bugs), bestLintExit, bestBugCount) {
			bestDraftPath = lastDraftPath
			bestLintExit = lintExit
			bestBugCount = len(bugs)
		}
		if verdict == "ship-as-is" {
			stampMetricsAction("ship-as-is", "")
			return finalizeRefineWithTitleAndOverlay(lastDraftPath, opts.OutputPath, opts.Title, iter, "ship-as-is", opts.ProjectRoot, mode, specDeliverableTargets, stderr)
		}
		if iter > 1 && refineLoopDiverging(prevReviewBugs, bugs) {
			fmt.Fprintf(stderr, "refine: loop diverging at iter %d (same bugs as iter %d); shipping best draft\n", iter, iter-1)
			stampMetricsAction("diverging", "")
			return finalizeRefineWithTitleAndOverlay(bestDraftPath, opts.OutputPath, opts.Title, iter, "diverging", opts.ProjectRoot, mode, specDeliverableTargets, stderr)
		}
		stampMetricsAction("continue", "")
		prevReviewBugs = bugs
	}
	fmt.Fprintf(stderr, "refine: max iters (%d) reached without ship-as-is verdict; shipping best draft (lint exit=%d, %d bugs)\n", opts.MaxIters, bestLintExit, bestBugCount)
	stampMetricsAction("max-iters-exhausted", "")
	finalDraft := bestDraftPath
	if finalDraft == "" {
		finalDraft = lastDraftPath
	}
	return finalizeRefineWithTitleAndOverlay(finalDraft, opts.OutputPath, opts.Title, opts.MaxIters, "max-iters-exhausted", opts.ProjectRoot, mode, specDeliverableTargets, stderr)
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

func clearRefineOutputPath(path string, stderr io.Writer) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if stderr != nil {
		fmt.Fprintf(stderr, "refine: cleared stale output %s\n", path)
	}
	return nil
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
	// §6.1 Gap 6 (Colony harness-side fix): extract deliverable
	// targets from the spec text and inline them as pre-filled context
	// so the model doesn't have to re-parse the spec. Anchored specs
	// prefer the explicit Files To Modify table; markdown specs retain
	// the older verb+path fallback.
	targets := extractDeliverableTargets(specBody, mode)
	target := firstRefineDeliverableTarget(targets)
	deliverableContext := refineDeliverableContextBlock(targets)
	if len(targets) > 0 {
		fmt.Fprintf(stderr, "refine: extracted deliverable target(s) from spec: %s\n", strings.Join(targets, ", "))
	}
	body := strings.NewReplacer(
		"{{SPEC_BODY}}", refineQuotedSpecBody(specBody),
		"{{DRAFT_PATH}}", refineWorkspaceRel(opts.ProjectRoot, draftPath),
		"{{TITLE}}", opts.Title,
		"{{DELIVERABLE_TARGET}}", target,
		"{{DELIVERABLE_TARGET_CONTEXT}}", deliverableContext,
	).Replace(template)
	body = ensureRefineMetaDraftWriteScope(body, refineWorkspaceRel(opts.ProjectRoot, draftPath))
	return os.WriteFile(metaPath, []byte(body), 0o644)
}

// refineQuotedSpecBody renders the input spec as reference material,
// not as active lane instructions. Refine meta-drafts embed arbitrary
// implementation specs, many of which contain their own "Files To
// Modify" tables. If those headings remain raw, the implementation
// runtime extracts the implementation allowlist as this scratchpad
// lane's required_edit_paths and the draft writer is blocked from
// writing draft.md. Blockquote every spec line so humans/models still
// see the source material while path extractors ignore its headings.
func refineQuotedSpecBody(specBody string) string {
	specBody = strings.TrimSpace(specBody)
	if specBody == "" {
		return "> (empty spec)"
	}
	lines := strings.Split(specBody, "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			lines[i] = ">"
			continue
		}
		lines[i] = "> " + line
	}
	return strings.Join(lines, "\n")
}

func ensureRefineMetaDraftWriteScope(body, draftPath string) string {
	draftPath = strings.TrimSpace(draftPath)
	if draftPath == "" {
		return body
	}
	if strings.Contains(strings.ToLower(body), "## files to modify") && strings.Contains(body, "`"+draftPath+"`") {
		return body
	}
	block := refineMetaDraftWriteScopeBlock(draftPath)
	if idx := strings.Index(body, "\n\n"); idx >= 0 {
		return body[:idx] + "\n\n" + block + "\n\n" + body[idx+2:]
	}
	return strings.TrimRight(body, "\n") + "\n\n" + block + "\n"
}

func refineMetaDraftWriteScopeBlock(draftPath string) string {
	return fmt.Sprintf("## Files To Modify\n\n| File | Change |\n|------|--------|\n| `%s` | NEW FILE: refine scratchpad draft brief |", draftPath)
}

// refineDeliverableContextBlock renders the substitution block that
// names the extracted deliverable target(s). Retained for backwards
// compatibility with templates that still reference
// `{{DELIVERABLE_TARGET_CONTEXT}}` (project overrides that haven't
// migrated to the harness-injected block yet). The canonical
// templates (`lanes/refine/draft.md`, `draft-markdown.md`) no longer
// reference the placeholder — they emit the
// `<!-- idiom:harness-deliverable-block -->` marker instead, which
// refineInjectDeliverableBlock substitutes at finalize time.
func refineDeliverableContextBlock(targets []string) string {
	targets = normalizeRefineTargets(targets)
	if len(targets) == 0 {
		return "(no deliverable target detected in spec — harness will not pre-fill the deliverable block; model still owns content sections only)"
	}
	if len(targets) == 1 {
		return fmt.Sprintf("Deliverable target extracted from spec by harness: `%s`. The harness will inject the canonical `## Files To Modify` / `## Deliverable` block at finalize time — emit the `<!-- idiom:harness-deliverable-block -->` marker verbatim, do NOT author the block yourself.", targets[0])
	}
	return fmt.Sprintf("Deliverable targets extracted from spec by harness: %s. The harness will inject the canonical `## Files To Modify` block at finalize time — emit the `<!-- idiom:harness-deliverable-block -->` marker verbatim, do NOT author the block yourself.", refineFormatBacktickList(targets))
}

// refineDeliverableMarker is the literal HTML comment templates emit
// in place of the deliverable-path block. finalize substitutes it
// with the canonical block populated from the harness-extracted
// target. Keeping it as an HTML comment (rather than a placeholder
// like `{{...}}`) means: (a) the model can copy it verbatim without
// worrying about substitution syntax, (b) if the marker leaks into
// a final brief (substitution fails / mode unknown), it's a visible
// defect in the rendered output rather than a silent corruption.
const refineDeliverableMarker = "<!-- idiom:harness-deliverable-block -->"

// refineInjectDeliverableBlock substitutes the harness marker with
// the canonical deliverable-path block. The block shape depends on
// mode:
//
//   - anchored-edits:    `## Files To Modify` table with one row per
//     extracted target path.
//   - markdown-deliverable: `## Deliverable` section with `Path:`
//     and `Format:` lines.
//
// Returns the body unchanged when:
//
//   - mode is empty or unknown (caller's responsibility to detect mode)
//   - target list is empty (extraction failed at spec-parse time)
//   - the marker isn't present in the body (the model authored its
//     own block, OR an older template emitted a different schema —
//     the lint catches the missing required sections downstream)
//
// Stderr-logs the substitution outcome for debugging.
func refineInjectDeliverableBlock(body []byte, mode string, targets []string, stderr io.Writer) []byte {
	targets = normalizeRefineTargets(targets)
	if len(targets) == 0 {
		fmt.Fprintf(stderr, "refine: deliverable injection skipped (no target extracted from spec)\n")
		return body
	}
	if !bytes.Contains(body, []byte(refineDeliverableMarker)) {
		fmt.Fprintf(stderr, "refine: deliverable injection skipped (marker %q absent from draft — model may have authored its own block; lint will validate)\n", refineDeliverableMarker)
		return body
	}
	block := refineRenderDeliverableBlock(mode, targets)
	if block == "" {
		fmt.Fprintf(stderr, "refine: deliverable injection skipped (unknown mode %q)\n", mode)
		return body
	}
	out := bytes.Replace(body, []byte(refineDeliverableMarker), []byte(block), 1)
	fmt.Fprintf(stderr, "refine: injected deliverable block (mode=%s, targets=%s, %d bytes)\n", mode, strings.Join(targets, ", "), len(block))
	return out
}

// refineRenderDeliverableBlock returns the mode-specific section text
// the harness substitutes for the deliverable marker. Returns "" for
// modes outside the canonical set so the caller can preserve the
// marker as a visible defect rather than silently corrupt the brief.
func refineRenderDeliverableBlock(mode string, targets []string) string {
	targets = normalizeRefineTargets(targets)
	if len(targets) == 0 {
		return ""
	}
	switch mode {
	case refineModeAnchored:
		var b strings.Builder
		b.WriteString("## Files To Modify\n\n| File | Change |\n|------|--------|\n")
		for _, target := range targets {
			fmt.Fprintf(&b, "| `%s` | MODIFY: harness-injected target (extracted from spec) |\n", target)
		}
		return strings.TrimRight(b.String(), "\n")
	case refineModeMarkdown:
		return fmt.Sprintf("## Deliverable\n\nPath: `%s`\nFormat: markdown", targets[0])
	default:
		return ""
	}
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
		candidate, change, ok := parseFilesToModifyRow(trimmed)
		if !ok {
			continue
		}
		if candidate == "" || seen[candidate] {
			continue
		}
		if refineChangeLooksNoop(change) {
			continue
		}
		// Skip refine scaffolding paths (their own meta-brief etc.).
		if strings.Contains(candidate, "/.idiom/refine/") || strings.HasPrefix(normalizeRefinePath(candidate), ".idiom/refine/") {
			continue
		}
		seen[candidate] = true
		files = append(files, candidate)
	}
	return files
}

func parseFilesToModifyRow(row string) (path, change string, ok bool) {
	// Strict table row: needs a backtick-quoted path in the first
	// semantic cell. The change cell is advisory but lets the harness
	// avoid over-authorizing rows that explicitly say "No change".
	start := strings.Index(row, "`")
	if start < 0 {
		return "", "", false
	}
	end := strings.Index(row[start+1:], "`")
	if end < 0 {
		return "", "", false
	}
	path = strings.TrimSpace(row[start+1 : start+1+end])
	parts := strings.Split(row, "|")
	if len(parts) >= 3 {
		change = strings.TrimSpace(parts[2])
	}
	return path, change, path != ""
}

func refineChangeLooksNoop(change string) bool {
	normalized := strings.ToLower(strings.TrimSpace(change))
	normalized = strings.Trim(normalized, "`*_ \t.")
	normalized = strings.Join(strings.Fields(normalized), " ")
	switch normalized {
	case "no change", "no changes", "none", "n/a", "na", "read only", "read-only", "reference only", "reference-only":
		return true
	}
	return strings.HasPrefix(normalized, "no change required") ||
		strings.HasPrefix(normalized, "no changes required") ||
		strings.HasPrefix(normalized, "do not modify") ||
		strings.HasPrefix(normalized, "do not edit") ||
		strings.HasPrefix(normalized, "do not touch")
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

Stay on the draft deliverable: write the file from the embedded spec
and recorded gaps instead of doing source discovery in this phase. The
file content goes inside the ` + "`write_file`" + ` call's ` + "`content`" + ` argument, not in
a chat message. After ` + "`write_file`" + ` returns success, give a single-sentence
final answer and stop.

## Completion

The successful ` + "`write_file`" + ` result is the completion proof. Do not run
` + "`test -f`" + `, ` + "`git diff`" + `, ` + "`shell`" + `, or any other follow-up check for this
scratchpad file.
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

var refineKnownFileBasenames = map[string]bool{
	"Dockerfile":         true,
	"Makefile":           true,
	"README":             true,
	"LICENSE":            true,
	".dockerignore":      true,
	".editorconfig":      true,
	".env":               true,
	".env.example":       true,
	".gitignore":         true,
	".gitattributes":     true,
	".prettierrc":        true,
	".stylelintrc":       true,
	"AGENTS.md":          true,
	"CLAUDE.md":          true,
	"CODEOWNERS":         true,
	"CONTRIBUTING.md":    true,
	"CHANGELOG.md":       true,
	"SECURITY.md":        true,
	"docker-compose":     true,
	"docker-compose.yml": true,
}

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

// refineDeliverableVerbRE captures verb+backtick-path phrasings that
// the spec uses to name a deliverable target (Colony Bug 6,
// 2026-05-11). The harness extracts the target deterministically so
// the meta-draft can pre-fill it rather than asking the model to
// guess from the spec text. Verbs cover the common forms; the path
// is the second capture group.
var refineDeliverableVerbRE = regexp.MustCompile(`(?i)\b(?:produce|create|write|generate|output\s+to|add)\b\s+` + "`" + `([^` + "`" + `]+)` + "`")

// extractDeliverableTargets returns the harness-owned path allowlist
// implied by a spec. Anchored specs are table-first: an explicit
// Files To Modify table is already the planner's edit contract, so
// preserve every path rather than guessing one prose target. Markdown
// specs retain the legacy verb+path extraction because many one-shot
// docs specs name a single deliverable inline.
//
// Examples:
//   - Files table with `a.cpp`, `a_test.cpp` → []string{"a.cpp", "a_test.cpp"}
//   - "audit `lane_postflight.sh` and produce `docs/foo.md`" → []string{"docs/foo.md"}
//   - "modify `foo.go`" → []string{"foo.go"} in anchored mode when tableless
//   - "add `EventClass` mapping" → nil (symbol, not a path)
//
// Heuristic — the override `--mode` flag and the safety-lint check
// at meta-draft load time backstop wrong guesses.
func extractDeliverableTargets(specBody, mode string) []string {
	if targets := filterRefineFilePaths(extractFilesToModify(specBody)); len(targets) > 0 {
		return targets
	}
	if targets := extractScopeFileTargets(specBody); len(targets) > 0 {
		return targets
	}

	var targets []string
	for _, m := range refineDeliverableVerbRE.FindAllStringSubmatch(specBody, -1) {
		path := strings.TrimSpace(m[1])
		if !pathLooksLikeFile(path) {
			continue
		}
		if mode == refineModeMarkdown && pathLooksLikeSource(path) {
			continue
		}
		targets = append(targets, path)
	}
	return normalizeRefineTargets(targets)
}

func extractScopeFileTargets(specBody string) []string {
	var targets []string
	inScope := false
	sawScopeBullet := false
	for _, line := range strings.Split(specBody, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			level := len(trimmed) - len(strings.TrimLeft(trimmed, "#"))
			normalized := normalizeRefineHeading(trimmed)
			if level == 2 && normalized == "scope" {
				inScope = true
				sawScopeBullet = false
				continue
			}
			if inScope && level <= 2 {
				break
			}
		}
		if !inScope {
			continue
		}
		if trimmed == "" {
			continue
		}
		if scopeContextLabel(trimmed) {
			break
		}
		if !isMarkdownBullet(trimmed) {
			if sawScopeBullet {
				break
			}
			continue
		}
		sawScopeBullet = true
		if path := firstFilePathMention(trimmed); path != "" {
			targets = append(targets, path)
		}
	}
	return normalizeRefineTargets(targets)
}

func normalizeRefineHeading(line string) string {
	value := strings.TrimSpace(line)
	value = strings.TrimLeft(value, "#")
	value = strings.Trim(value, " *:`\"'")
	value = strings.TrimSuffix(value, ":")
	value = regexp.MustCompile(`\s*\([^)]*\)\s*`).ReplaceAllString(value, " ")
	value = strings.NewReplacer("-", " ", "_", " ", "/", " ").Replace(strings.ToLower(value))
	return strings.Join(strings.Fields(value), " ")
}

func scopeContextLabel(line string) bool {
	lower := strings.ToLower(strings.TrimSpace(line))
	lower = strings.TrimSuffix(lower, ":")
	return strings.Contains(lower, "acceptance") && strings.Contains(lower, "context")
}

func isMarkdownBullet(line string) bool {
	return strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") || strings.HasPrefix(line, "+ ")
}

var refineBacktickPathRE = regexp.MustCompile("`([^`]+)`")

func firstFilePathMention(line string) string {
	for _, match := range refineBacktickPathRE.FindAllStringSubmatch(line, -1) {
		candidate := strings.TrimSpace(match[1])
		if pathLooksLikeFile(candidate) {
			return candidate
		}
	}
	for _, field := range strings.Fields(line) {
		candidate := strings.Trim(field, "`'\".,;:()[]{}")
		if pathLooksLikeFile(candidate) {
			return candidate
		}
	}
	return ""
}

// extractDeliverableTarget preserves the legacy single-target helper
// contract for tests and older callers. New refine code should use
// extractDeliverableTargets so anchored specs can carry a complete
// multi-file allowlist.
func extractDeliverableTarget(specBody string) string {
	return firstRefineDeliverableTarget(extractDeliverableTargets(specBody, refineDetectMode(specBody)))
}

// refineDeliverableTargetMismatch returns a formatted BLOCK string
// (suitable for prepending to lint output) when the brief's Files To
// Modify allowlist does NOT reference the extracted deliverable
// target. Returns "" when the target was empty (extraction failed,
// nothing to validate), when the brief can't be read, when the
// brief's allowlist is empty (no rows yet to compare), or when the
// brief DOES reference the target (success — silent).
//
// Gap 8 (Colony 2026-05-12): when the draft contains the harness
// deliverable marker, the model is signaling it deferred path
// authorship to the harness — which finalize will substitute with
// the canonical block populated from the extracted target. No
// mismatch is possible in that case; return "" silently.
//
// The mismatch shape lands in the review preload's BLOCK list so the
// reviewer's `ship-as-is` gate forces a fix-bugs-then-ship verdict
// AND the patcher (Gap 1 — patcher sees lint preload directly) gets
// the mismatch as an explicit edit target.
func refineDeliverableTargetMismatch(draftPath string, specTargets []string) string {
	specTargets = normalizeRefineTargets(specTargets)
	if len(specTargets) == 0 {
		return ""
	}
	body, err := os.ReadFile(draftPath)
	if err != nil {
		return ""
	}
	// Gap 8: marker present → harness owns the path at finalize.
	// Skip the mismatch check entirely; the model correctly deferred
	// to the harness instead of authoring the block.
	if bytes.Contains(body, []byte(refineDeliverableMarker)) {
		return ""
	}
	files := extractFilesToModify(string(body))
	if len(files) == 0 {
		// Markdown-deliverable mode often has no Files To Modify;
		// the deliverable target lives in `## Deliverable Path:`
		// instead. Try a string-search for the target path anywhere
		// in the body — if present, no mismatch.
		if bodyContainsAnyRefineTarget(string(body), specTargets) {
			return ""
		}
		return fmt.Sprintf("- [BLOCK] (deliverable-target-mismatch) spec asks for deliverable target(s) %s but the brief body does not reference any of those paths. The canonical templates emit a `%s` marker after `## Problem` so the harness owns the path — emit the marker verbatim instead of authoring the block yourself.", refineFormatBacktickList(specTargets), refineDeliverableMarker)
	}
	missing := missingRefineTargets(files, specTargets)
	if len(missing) == 0 {
		return ""
	}
	return fmt.Sprintf("- [BLOCK] (deliverable-target-mismatch) brief's Files To Modify lists %v but spec asks for deliverable target(s) %s; missing %s. The canonical templates emit a `%s` marker after `## Problem` so the harness owns the path — emit the marker verbatim instead of authoring the block yourself.", files, refineFormatBacktickList(specTargets), refineFormatBacktickList(missing), refineDeliverableMarker)
}

func firstRefineDeliverableTarget(targets []string) string {
	targets = normalizeRefineTargets(targets)
	if len(targets) == 0 {
		return ""
	}
	return targets[0]
}

func normalizeRefineTargets(targets []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, target := range targets {
		target = strings.TrimSpace(target)
		if target == "" {
			continue
		}
		key := normalizeRefinePath(target)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, target)
	}
	return out
}

func normalizeRefinePath(path string) string {
	path = strings.TrimSpace(path)
	path = strings.ReplaceAll(path, "\\", "/")
	path = strings.TrimPrefix(path, "./")
	return path
}

func filterRefineFilePaths(candidates []string) []string {
	var targets []string
	for _, candidate := range candidates {
		if pathLooksLikeFile(candidate) {
			targets = append(targets, candidate)
		}
	}
	return normalizeRefineTargets(targets)
}

func bodyContainsAnyRefineTarget(body string, targets []string) bool {
	for _, target := range targets {
		if strings.Contains(body, target) {
			return true
		}
	}
	return false
}

func missingRefineTargets(files, targets []string) []string {
	var missing []string
	for _, target := range targets {
		found := false
		for _, file := range files {
			if pathsEquivalent(file, target) {
				found = true
				break
			}
		}
		if !found {
			missing = append(missing, target)
		}
	}
	return missing
}

func refineFormatBacktickList(targets []string) string {
	targets = normalizeRefineTargets(targets)
	if len(targets) == 0 {
		return "(none)"
	}
	quoted := make([]string, 0, len(targets))
	for _, target := range targets {
		quoted = append(quoted, "`"+target+"`")
	}
	return strings.Join(quoted, ", ")
}

// pathsEquivalent compares two path strings tolerantly: trim
// whitespace, normalize slashes, ignore leading "./". Two paths that
// reduce to the same string are equivalent. Used by the deliverable-
// target mismatch check so cosmetic differences ("./foo.md" vs
// "foo.md") don't trip the BLOCK.
func pathsEquivalent(a, b string) bool {
	return normalizeRefinePath(a) == normalizeRefinePath(b)
}

// pathLooksLikeSource returns true when the path ends in a known
// source-extension. Used by extractDeliverableTarget to skip source
// citations (anchored-edits mode) when looking for the deliverable
// target of a markdown-shape atom.
func pathLooksLikeSource(path string) bool {
	lower := strings.ToLower(path)
	for _, ext := range refineSourceExtensions {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}

func pathLooksLikeFile(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}
	if strings.ContainsAny(path, "\n\r\t<>|*?\"") {
		return false
	}
	if strings.Contains(path, " ") && !strings.ContainsAny(path, "/\\.") {
		return false
	}
	if strings.ContainsAny(path, "/\\") || strings.HasPrefix(path, ".") {
		return true
	}
	if filepath.Ext(path) != "" {
		return true
	}
	return refineKnownFileBasenames[path]
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
func captureRefineLintOutput(draftPath, persistDir, projectRoot, mode string, deliverableTargets []string, stderr io.Writer) (int, string) {
	var out bytes.Buffer
	lintPath, cleanup := refineLintInputPath(draftPath, projectRoot, mode, deliverableTargets, stderr)
	defer cleanup()
	code := laneBriefLintRunner([]string{lintPath, "--json"}, &out, stderr)
	rawJSON := bytes.TrimSpace(out.Bytes())
	rendered, ok := renderRefineLintFindings(rawJSON)
	if !ok {
		// Fall back: try plain-text capture so the reviewer at least
		// sees something. If even that is empty, emit the fallback
		// "treat as BLOCK" message so empty + exit-non-zero doesn't
		// look like "clean lint" to downstream consumers.
		var plain bytes.Buffer
		_ = laneBriefLintRunner([]string{lintPath}, &plain, stderr)
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

// refineLintInputPath renders the harness-owned final shape that brief
// lint should judge: the model draft plus canonical deliverable block
// injection. Project overlays are executor guidance, not deliverable
// grammar, so they are appended only after linting.
func refineLintInputPath(draftPath, _ string, mode string, deliverableTargets []string, stderr io.Writer) (string, func()) {
	if strings.TrimSpace(mode) == "" && len(deliverableTargets) == 0 {
		return draftPath, func() {}
	}
	body, err := os.ReadFile(draftPath)
	if err != nil {
		fmt.Fprintf(stderr, "refine: WARN cannot render final-shape lint input from %s: %v (linting raw draft)\n", draftPath, err)
		return draftPath, func() {}
	}
	body = refineInjectDeliverableBlock(body, mode, deliverableTargets, stderr)
	tmpPath, cleanup, err := writeRefineTempBody(body, "idiom-refine-lint-*.md")
	if err != nil {
		fmt.Fprintf(stderr, "refine: WARN cannot create final-shape lint input: %v (linting raw draft)\n", err)
		return draftPath, func() {}
	}
	return tmpPath, cleanup
}

func writeFallbackRefineReview(reviewPath, lintOutput, reason string, stderr io.Writer) (bool, error) {
	blockLines := refineLintLinesBySeverity(lintOutput, "BLOCK")
	if len(blockLines) == 0 {
		return false, nil
	}
	allLines := refineLintFindingLines(lintOutput)
	if len(allLines) == 0 {
		allLines = blockLines
	}

	var body strings.Builder
	body.WriteString("# Review of brief\n\n")
	body.WriteString("## Bugs found\n\n")
	for i, line := range blockLines {
		category, message := parseRefineLintLine(line)
		title := "Lint BLOCK"
		if category != "" {
			title += ": " + category
		}
		if message == "" {
			message = strings.TrimSpace(line)
		}
		fmt.Fprintf(&body, "%d. **%s**: %s. Fix the drafted brief so this lint BLOCK is resolved.\n", i+1, title, strings.TrimRight(message, "."))
	}
	body.WriteString("\n## Lint findings\n\n")
	for _, line := range allLines {
		body.WriteString(line)
		body.WriteByte('\n')
	}
	body.WriteString("\n## OK\n\n")
	fmt.Fprintf(&body, "- Synthetic review generated because the review lane produced no file (%s); lint BLOCKs remain authoritative.\n", reason)
	body.WriteString("\n## Verdict\n\nfix-bugs-then-ship\n")

	if err := os.MkdirAll(filepath.Dir(reviewPath), 0o755); err != nil {
		return false, err
	}
	if err := os.WriteFile(reviewPath, []byte(body.String()), 0o644); err != nil {
		return false, err
	}
	if stderr != nil {
		fmt.Fprintf(stderr, "refine: WARN synthesized review from %d lint BLOCK(s) after %s\n", len(blockLines), reason)
	}
	return true, nil
}

var refineRenderedLintLineRe = regexp.MustCompile(`^\s*-\s+\[(BLOCK|WARN)\]\s*(?:\(([^)]+)\))?\s*(.*)$`)

func refineLintFindingLines(lintOutput string) []string {
	var out []string
	for _, line := range strings.Split(lintOutput, "\n") {
		trimmed := strings.TrimSpace(line)
		if refineRenderedLintLineRe.MatchString(trimmed) {
			out = append(out, trimmed)
		}
	}
	return out
}

func refineLintLinesBySeverity(lintOutput, severity string) []string {
	severity = strings.ToUpper(strings.TrimSpace(severity))
	var out []string
	for _, line := range refineLintFindingLines(lintOutput) {
		m := refineRenderedLintLineRe.FindStringSubmatch(line)
		if len(m) > 1 && strings.EqualFold(m[1], severity) {
			out = append(out, line)
		}
	}
	return out
}

func parseRefineLintLine(line string) (category, message string) {
	m := refineRenderedLintLineRe.FindStringSubmatch(strings.TrimSpace(line))
	if len(m) == 0 {
		return "", strings.TrimSpace(line)
	}
	return strings.TrimSpace(m[2]), strings.TrimSpace(m[3])
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

func writeRefinePatchMeta(metaPath, prevDraftPath, prevReviewPath, newDraftPath, lintOutput string, evidencePaths []string) error {
	projectRoot := refineProjectRootFromIterDir(filepath.Dir(metaPath))
	if strings.TrimSpace(lintOutput) == "" {
		lintOutput = "(no findings — lint pass was clean on the previous draft)"
	}
	body := strings.NewReplacer(
		"{{PREV_DRAFT_PATH}}", refineWorkspaceRel(projectRoot, prevDraftPath),
		"{{PREV_REVIEW_PATH}}", refineWorkspaceRel(projectRoot, prevReviewPath),
		"{{NEW_DRAFT_PATH}}", refineWorkspaceRel(projectRoot, newDraftPath),
		"{{LINT_OUTPUT}}", lintOutput,
		"{{EVIDENCE_PATHS}}", refineEvidencePathList(evidencePaths),
		"{{SOURCE_EVIDENCE}}", refinePatchSourceEvidence(projectRoot, prevDraftPath, prevReviewPath, lintOutput, evidencePaths),
	).Replace(refinePatchTemplate)
	return os.WriteFile(metaPath, []byte(body), 0o644)
}

func refineEvidencePathList(paths []string) string {
	paths = normalizeRefineTargets(paths)
	if len(paths) == 0 {
		return "- (none)\n"
	}
	var b strings.Builder
	for _, path := range paths {
		fmt.Fprintf(&b, "- `%s`\n", path)
	}
	return strings.TrimRight(b.String(), "\n")
}

var refineEvidenceTermRe = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*(?:::[A-Za-z_][A-Za-z0-9_]*)?`)

const refinePatchSourceEvidenceAnchorsPerPath = 12

func refinePatchSourceEvidence(projectRoot, prevDraftPath, prevReviewPath, lintOutput string, evidencePaths []string) string {
	paths := normalizeRefineTargets(evidencePaths)
	if len(paths) == 0 {
		return "- (none)\n"
	}
	promptText := refinePatchEvidencePromptText(prevDraftPath, prevReviewPath, lintOutput)
	globalTerms := refineEvidenceTerms(promptText)
	var b strings.Builder
	emitted := 0
	for _, path := range paths {
		full := path
		if !filepath.IsAbs(full) {
			full = filepath.Join(projectRoot, path)
		}
		data, err := os.ReadFile(filepath.Clean(full))
		if err != nil {
			fmt.Fprintf(&b, "- `%s`: source evidence unavailable: %v\n", path, err)
			emitted++
			continue
		}
		anchors := refineEvidenceAnchorsForPath(path, string(data), promptText, globalTerms)
		if len(anchors) == 0 {
			fmt.Fprintf(&b, "- `%s`: no identifier anchors matched the draft/review/lint terms\n", path)
			emitted++
			continue
		}
		for _, anchor := range anchors {
			fmt.Fprintf(&b, "- `%s:%d`: `%s`\n", path, anchor.line, anchor.text)
			emitted++
		}
	}
	if emitted == 0 {
		return "- (none)\n"
	}
	return strings.TrimRight(b.String(), "\n")
}

func refinePatchEvidencePromptText(prevDraftPath, prevReviewPath, lintOutput string) string {
	var b strings.Builder
	for _, path := range []string{prevDraftPath, prevReviewPath} {
		if data, err := os.ReadFile(filepath.Clean(path)); err == nil {
			b.Write(data)
			b.WriteByte('\n')
		}
	}
	b.WriteString(lintOutput)
	return b.String()
}

type refineEvidenceAnchor struct {
	line  int
	text  string
	score int
}

func refineEvidenceAnchorsForPath(path, source, promptText string, globalTerms []string) []refineEvidenceAnchor {
	localTerms := refineEvidenceTerms(refinePromptLinesForPath(promptText, path))
	terms := mergeRefineEvidenceTerms(localTerms, globalTerms)
	if len(terms) == 0 {
		return nil
	}
	local := map[string]bool{}
	for _, term := range localTerms {
		local[term] = true
	}
	lines := strings.Split(source, "\n")
	var anchors []refineEvidenceAnchor
	for i, line := range lines {
		if refineEvidenceLineLooksNoisy(line) {
			continue
		}
		score := refineEvidenceLineShapeWeight(line)
		score += refineEvidenceBoundaryWeight(line, promptText, terms)
		for _, term := range terms {
			if !strings.Contains(line, term) {
				continue
			}
			score += refineEvidenceTermWeight(term)
			if local[term] {
				score += 4
			}
		}
		if score <= 0 {
			continue
		}
		anchors = append(anchors, refineEvidenceAnchor{
			line:  i + 1,
			text:  refineEvidenceLineText(line),
			score: score,
		})
	}
	sort.SliceStable(anchors, func(i, j int) bool {
		if anchors[i].score == anchors[j].score {
			return anchors[i].line < anchors[j].line
		}
		return anchors[i].score > anchors[j].score
	})
	if len(anchors) > refinePatchSourceEvidenceAnchorsPerPath {
		anchors = anchors[:refinePatchSourceEvidenceAnchorsPerPath]
	}
	sort.SliceStable(anchors, func(i, j int) bool {
		return anchors[i].line < anchors[j].line
	})
	return anchors
}

func refinePromptLinesForPath(text, path string) string {
	base := filepath.Base(path)
	var b strings.Builder
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if strings.Contains(line, path) || (base != "" && strings.Contains(line, base)) {
			start := i - 1
			if start < 0 {
				start = 0
			}
			end := i + 2
			if end >= len(lines) {
				end = len(lines) - 1
			}
			for j := start; j <= end; j++ {
				b.WriteString(lines[j])
				b.WriteByte('\n')
			}
		}
	}
	return b.String()
}

func refineEvidenceTerms(text string) []string {
	seen := map[string]int{}
	for _, match := range refineEvidenceTermRe.FindAllString(text, -1) {
		term := strings.TrimSpace(match)
		if !refineEvidenceTermUseful(term) {
			continue
		}
		seen[term] += refineEvidenceTermWeight(term)
	}
	terms := make([]string, 0, len(seen))
	for term := range seen {
		terms = append(terms, term)
	}
	sort.SliceStable(terms, func(i, j int) bool {
		left, right := seen[terms[i]], seen[terms[j]]
		if left == right {
			return terms[i] < terms[j]
		}
		return left > right
	})
	return terms
}

func mergeRefineEvidenceTerms(primary, fallback []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(primary)+len(fallback))
	for _, terms := range [][]string{primary, fallback} {
		for _, term := range terms {
			if seen[term] {
				continue
			}
			seen[term] = true
			out = append(out, term)
		}
	}
	return out
}

func refineEvidenceTermUseful(term string) bool {
	if len(term) < 3 || strings.ContainsAny(term, `/\.`) {
		return false
	}
	switch strings.ToLower(term) {
	case "the", "and", "for", "with", "that", "this", "from", "line", "lines", "path", "file", "source", "review", "draft", "brief", "block", "warn", "required", "edit", "existing", "add", "new", "case", "return", "enum", "class", "struct", "function", "native", "tests", "include", "namespace", "using", "const", "auto", "void", "bool", "int", "string", "colony", "core", "simulation", "appraisal":
		return false
	default:
		return true
	}
}

func refineEvidenceTermWeight(term string) int {
	weight := 1
	if strings.Contains(term, "::") {
		weight += 8
	}
	if strings.Contains(term, "_") {
		weight += 6
	}
	if term == strings.ToUpper(term) {
		weight += 6
	}
	if r := rune(term[0]); r >= 'A' && r <= 'Z' {
		weight += 4
	}
	if strings.HasSuffix(term, "()") {
		weight += 3
	}
	return weight
}

func refineEvidenceLineLooksNoisy(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return true
	}
	switch trimmed {
	case "{", "}", "};", "},", ");":
		return true
	}
	if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, "*") {
		return true
	}
	if strings.HasPrefix(trimmed, "#include") || strings.HasPrefix(trimmed, "#pragma once") || strings.HasPrefix(trimmed, "#ifndef") || strings.HasPrefix(trimmed, "#endif") {
		return true
	}
	if strings.HasPrefix(trimmed, "#define") && strings.HasSuffix(trimmed, "_H") {
		return true
	}
	if strings.HasPrefix(trimmed, "using namespace ") {
		return true
	}
	if strings.HasPrefix(trimmed, "namespace ") && strings.HasSuffix(trimmed, "{") {
		return true
	}
	return false
}

func refineEvidenceBoundaryWeight(line, promptText string, terms []string) int {
	trimmed := strings.TrimSpace(line)
	promptLower := strings.ToLower(promptText)
	needsBoundary := strings.Contains(promptText, "COUNT") ||
		strings.Contains(promptLower, "sentinel") ||
		strings.Contains(promptLower, "boundary") ||
		strings.Contains(promptLower, "closing") ||
		strings.Contains(promptLower, "top-of-file")
	if needsBoundary && strings.Contains(trimmed, "COUNT") {
		return 32
	}
	if refineEvidenceLooksLikeFunctionBoundary(trimmed, terms) {
		return 24
	}
	if refineEvidenceLooksLikeCaseBoundary(trimmed, terms, promptLower) {
		return 18
	}
	if (strings.Contains(promptLower, "end-of-file") ||
		strings.Contains(promptLower, "last existing") ||
		strings.Contains(promptLower, "last test")) &&
		(strings.Contains(trimmed, "TEST") || strings.Contains(trimmed, "Describe(") || strings.Contains(trimmed, "it(")) {
		return 10
	}
	return 0
}

func refineEvidenceLooksLikeFunctionBoundary(trimmed string, terms []string) bool {
	if !strings.Contains(trimmed, "(") || !strings.Contains(trimmed, ")") {
		return false
	}
	if strings.HasPrefix(trimmed, "if ") ||
		strings.HasPrefix(trimmed, "for ") ||
		strings.HasPrefix(trimmed, "while ") ||
		strings.HasPrefix(trimmed, "switch ") ||
		strings.HasPrefix(trimmed, "catch ") {
		return false
	}
	for _, term := range terms {
		if strings.Contains(term, "::") || strings.ToUpper(term) == term {
			continue
		}
		if strings.Contains(trimmed, term) {
			return true
		}
	}
	return false
}

func refineEvidenceLooksLikeCaseBoundary(trimmed string, terms []string, promptLower string) bool {
	if !strings.HasPrefix(trimmed, "case ") && !strings.HasPrefix(trimmed, "default:") {
		return false
	}
	if !strings.Contains(promptLower, "switch") &&
		!strings.Contains(promptLower, "case") &&
		!strings.Contains(promptLower, "closing") {
		return false
	}
	for _, term := range terms {
		if strings.Contains(trimmed, term) {
			return true
		}
	}
	return false
}

func refineEvidenceLineShapeWeight(line string) int {
	trimmed := strings.TrimSpace(line)
	score := 0
	if strings.Contains(trimmed, "enum ") || strings.Contains(trimmed, "enum class ") {
		score += 5
	}
	if strings.Contains(trimmed, "struct ") || strings.Contains(trimmed, "class ") || strings.Contains(trimmed, "interface ") || strings.Contains(trimmed, "type ") {
		score += 4
	}
	if strings.HasPrefix(trimmed, "func ") || strings.Contains(trimmed, " func(") || strings.Contains(trimmed, "function ") || strings.Contains(trimmed, "def ") {
		score += 4
	}
	if strings.Contains(trimmed, "TEST") || strings.Contains(trimmed, "Describe(") || strings.Contains(trimmed, "it(") || strings.Contains(trimmed, "test(") {
		score += 4
	}
	if strings.Contains(trimmed, "=") || strings.Contains(trimmed, ":=") || strings.Contains(trimmed, "case ") {
		score++
	}
	return score
}

func refineEvidenceLineText(line string) string {
	text := strings.TrimSpace(line)
	text = strings.ReplaceAll(text, "`", "'")
	if len(text) > 180 {
		text = strings.TrimSpace(text[:180]) + "..."
	}
	return text
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
	if len(previous) != len(current) {
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

func refineDraftScoreBetter(lintExit, bugCount, bestLintExit, bestBugCount int) bool {
	if bestLintExit < 0 || bestBugCount < 0 {
		return true
	}
	lintClean := lintExit == 0
	bestLintClean := bestLintExit == 0
	if lintClean != bestLintClean {
		return lintClean
	}
	if bugCount != bestBugCount {
		return bugCount < bestBugCount
	}
	return false
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
// becomes the FIRST non-warning line of the output brief so
// detectBriefProvenance can identify refine-drafted briefs in lane
// history. Reads draft, renders the final artifact shape, lints that
// exact artifact, then writes output.
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
	return finalizeRefineWithTitleAndOverlay(draftPath, outputPath, title, iter, reason, "", "", nil, stderr)
}

// finalizeRefineWithTitleAndOverlay is the §2.4-aware finalize path.
// Applies project overlay (HARD PROHIBITIONS + atom-shape-matched
// traps) before final lint so the hardened brief is what downstream
// consumers see and what the exit status certifies. Empty projectRoot
// disables overlay (bare-repo behavior preserved).
//
// Gap 8 (Colony 2026-05-12): the model NEVER authors the deliverable
// path. Templates emit a literal `<!-- idiom:harness-deliverable-block -->`
// marker after `## Problem`; this finalize step substitutes the marker
// with the canonical path block (`## Files To Modify` for anchored
// mode, `## Deliverable` for markdown mode) populated from the
// harness-extracted target(s). The model can't hallucinate a path it
// never wrote. Empty mode / target list = no substitution attempted
// (the marker stays in the brief, which is a visible defect for the
// reviewer rather than a silent corruption).
func finalizeRefineWithTitleAndOverlay(draftPath, outputPath, title string, iter int, reason, projectRoot, mode string, deliverableTargets []string, stderr io.Writer) int {
	if trivial, why := refineDraftIsTrivial(draftPath); trivial {
		fmt.Fprintf(stderr, "refine: refusing to finalize trivial draft %s (%s); exiting non-zero rather than writing a silent-failure output\n", draftPath, why)
		return 1
	}
	body, err := os.ReadFile(draftPath)
	if err != nil {
		fmt.Fprintf(stderr, "refine: cannot read final draft %s: %v\n", draftPath, err)
		return 1
	}
	body = refineInjectDeliverableBlock(body, mode, deliverableTargets, stderr)
	body = refineApplyProjectOverlay(body, projectRoot, stderr)
	isShipAsIs := reason == "ship-as-is"
	outputReason := reason
	finalBody := buildRefineFinalBody(body, title, iter, outputReason, !isShipAsIs)
	if code := lintRefineFinalBody(finalBody, stderr); code != 0 {
		fmt.Fprintf(stderr, "refine: final output brief failed lint after finalize (exit %d); refusing ship-as-is dispatch\n", code)
		if isShipAsIs {
			isShipAsIs = false
			outputReason = "final-lint-failed"
			finalBody = buildRefineFinalBody(body, title, iter, outputReason, true)
		}
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		fmt.Fprintf(stderr, "refine: cannot create output dir: %v\n", err)
		return 1
	}
	if err := os.WriteFile(outputPath, finalBody, 0o644); err != nil {
		fmt.Fprintf(stderr, "refine: cannot write output %s: %v\n", outputPath, err)
		return 1
	}
	if isShipAsIs {
		fmt.Fprintf(stderr, "refine: converged ship-as-is after %d iter(s); wrote %s (%d bytes) at %s\n", iter, outputPath, len(finalBody), time.Now().Format(time.RFC3339))
		return 0
	}
	fmt.Fprintf(stderr, "refine: did NOT reach ship-as-is (%s) after %d iter(s); wrote best-draft to %s (%d bytes) with WARNING header — exiting non-zero so orchestrator inspects before piping\n", outputReason, iter, outputPath, len(finalBody))
	return 1
}

func buildRefineFinalBody(body []byte, title string, iter int, reason string, warning bool) []byte {
	var prefix []byte
	if warning {
		prefix = []byte(fmt.Sprintf("> WARNING: refine exited non-zero — reason=%s after iter %d. The brief below is the best draft the loop produced; review for unresolved bugs/BLOCKs before dispatching to plan-execute. Inspect `.idiom/refine/%s/iter-%d/{review.md,metrics.json}` for findings.\n\n", reason, iter, refineSanitizeTitle(title), iter))
	}
	marker := fmt.Sprintf("<!-- idiom:refine-drafted title=%s iter=%d reason=%s -->\n", refineSanitizeTitle(title), iter, reason)
	finalBody := append(prefix, []byte(marker)...)
	finalBody = append(finalBody, body...)
	return finalBody
}

func lintRefineFinalBody(body []byte, stderr io.Writer) int {
	tmpPath, cleanup, err := writeRefineTempBody(body, "idiom-refine-final-*.md")
	if err != nil {
		fmt.Fprintf(stderr, "refine: WARN cannot create temp final brief for lint: %v\n", err)
		return 0
	}
	defer cleanup()
	return laneBriefLintRunner([]string{tmpPath}, io.Discard, stderr)
}

func writeRefineTempBody(body []byte, pattern string) (string, func(), error) {
	tmp, err := os.CreateTemp("", pattern)
	if err != nil {
		return "", func() {}, err
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		cleanup()
		return "", func() {}, err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return "", func() {}, err
	}
	return tmpPath, cleanup, nil
}
