package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"idiom/internal/agent"
	"idiom/internal/app"
	"idiom/internal/comprehension"
	"idiom/internal/config"
	"idiom/internal/runner"
	"idiom/internal/telemetry"
	"idiom/internal/workspace"
)

// planExecuteOptions configures `idiom plan-execute`.
type planExecuteOptions struct {
	ConfigPath      string
	Prompt          string
	PromptFile      string
	Model           string
	Resume          string
	JSONEvents      bool
	DisableThinking bool
	MaxTokens       int
	// PlannerOwnedVerification is accepted for compatibility with
	// `idiom implement` invocations that pass the flag. Plan-execute
	// always treats verification as planner-owned (the executor phase
	// is deterministic dispatch, not a verifier), so the flag is a
	// no-op here — keeping it lets existing lane scripts swap
	// `implement` for `plan-execute` without flag rewrites.
	PlannerOwnedVerification bool
	// AllowBuildTests is also a compatibility flag — plan-execute does
	// not run builds or tests in any phase, so this is ignored. Same
	// motivation as PlannerOwnedVerification.
	AllowBuildTests bool
	// SkipReviewRepair disables the post-execute review-repair gate.
	// The default is to run one generic, action-capable review pass
	// after deterministic executor writes so obvious behavioral or
	// diff-level defects can be corrected before the lane reports done.
	SkipReviewRepair bool
}

// runPlanExecuteMain is the CLI entry point for `idiom plan-execute`.
//
// Plan-then-execute is the third lane shape (alongside `idiom implement`
// and the harness-intercept lane): the planner runs FIRST with no
// per-edit write tools, emits a structured plan via submit_plan, and
// the orchestrator's executor phase iterates that plan and dispatches
// each edit deterministically against the workspace. The model only
// thinks once — about WHAT to change, end-to-end — and never has to
// hold "what's done vs. what's left" in working memory across N edits.
func runPlanExecuteMain(args []string, stdout io.Writer, stderr io.Writer, promptRun promptRunner) int {
	return runPlanExecuteMainWithTelemetry(args, stdout, stderr, promptRun, nil)
}

func runPlanExecuteMainWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, promptRun promptRunner, rec *telemetry.Recorder) int {
	opts, err := parsePlanExecuteOptions(args, stderr)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	cfg, err := config.Load(opts.ConfigPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer cleanupStalePlanArtifacts(cfg.Workspace.Root, 7*24*time.Hour, stderr)
	if strings.TrimSpace(opts.Model) != "" {
		cfg.LlamaServer.Model = opts.Model
	}
	cleanupStalePlanArtifacts(cfg.Workspace.Root, 7*24*time.Hour, stderr)
	// Capture session start time + a fresh session UUID so the
	// executor phase only loads plan artifacts produced by THIS
	// planner run. Without these guards, a planner failure (budget
	// exhaustion, EOF, etc.) silently falls back to the most recent
	// plan on disk — which may be from an unrelated prior session
	// (or a CONCURRENT session running in the same workspace) and
	// dispatches the wrong edits. The sessionStart filter is a
	// belt-and-suspenders backup; the SessionID filter is the
	// strict attribution.
	sessionStart := time.Now().Add(-1 * time.Second)
	sessionID := newPlanExecuteSessionID()
	cfg.Workspace.SessionID = sessionID
	logCLIPhase(rec, "plan_execute_start", map[string]any{
		"json_events": opts.JSONEvents,
		"session_id":  sessionID,
	})

	// Queue gate: if a `idiom queue serve` daemon is reachable, acquire
	// a lease for the model's pool BEFORE the planner phase starts so
	// concurrent dispatches don't both hammer the MLX backend at once
	// (Metal command-buffer pressure crashes 47781 under simultaneous
	// load — observed on 2026-05-10). The gate is best-effort: when no
	// daemon is running, MaybeAcquire returns a no-op release function
	// and we proceed un-gated, matching legacy behavior.
	queueRelease := planExecuteAcquireQueueSlot(cfg, opts, sessionID, stdout, stderr)
	defer queueRelease()

	prompt, err := planExecutePrompt(opts)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	requiredEditPaths := app.RequiredEditPathsFromPrompt(prompt)

	// Phase 1: planner. The planner runs as an implementation lane
	// with the plan-then-execute system addendum that swaps per-edit
	// writes for submit_plan. We reuse the existing implement runner
	// because that lane already has the right read-side guards and
	// brief audit; only the tool surface differs.
	plannerPrompt := planExecutePlannerSystemAddendum() + "\n\n" + prompt
	plannerMaxTokens := 16384
	if opts.MaxTokens > 0 {
		plannerMaxTokens = opts.MaxTokens
	}
	answer, err := promptRun(cfg, plannerPrompt, runOptions{
		JSONEvents:               opts.JSONEvents,
		Resume:                   opts.Resume,
		TaskFamily:               agent.PlanThenExecutePlannerTaskFamily,
		PlannerOwnedVerification: true,
		// max_tokens for the planner phase: 16384.
		//
		// Originally 4096 ("planner emits one submit_plan + short answer,
		// 4096 is plenty"). That assumption broke when submit_plan's
		// `new_text` field carried the full content of NEW FILE edits —
		// a 400-line Go file plus structure overhead easily exceeds
		// 4096 tokens, and the response truncates silently mid-JSON
		// (manifesting as "completion gate rejected, no write present"
		// because the truncated JSON failed to parse as a valid tool
		// call). See task-64b for the failure repro.
		//
		// submit_plan now spills large persisted bodies into sidecar refs,
		// but the planner may still need enough output budget to emit a
		// new-file body once. Keep this above the historical 4096 until
		// planner-side content refs can avoid emitting those bytes at all.
		MaxTokensOverride: plannerMaxTokens,
		DisableThinking:   opts.DisableThinking,
		Telemetry:         rec,
	}, stdout)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}

	// Phase 2: executor. Locate the plan artifact written by THIS
	// session's planner phase and dispatch its edits in order.
	planPath, err := findLatestPlanArtifact(cfg.Workspace.Root, sessionStart, sessionID)
	if err != nil {
		if agent.PlanThenExecuteSourceSatisfiedAnswerCoversPaths(answer, requiredEditPaths) {
			emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, "source_satisfied", map[string]any{
				"required_edit_paths": requiredEditPaths,
				"reason":              "planner reported requested final state already present",
			})
			if !opts.JSONEvents {
				fmt.Fprintln(stdout, answer)
				fmt.Fprintln(stdout, "")
				fmt.Fprintln(stdout, "--- plan-then-execute summary ---")
				fmt.Fprintln(stdout, "source-satisfied: executor skipped because the planner reported the requested final state already present in every required path.")
			}
			return 0
		}
		emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, "plan_artifact_missing", map[string]any{
			"reason": err.Error(),
		})
		fmt.Fprintln(stderr, fmt.Errorf("planner phase did not produce a plan artifact: %w", err))
		// Still print the planner answer so the caller can see what
		// happened — this is a recoverable failure (the planner may
		// have stopped with a typed blocker).
		if !opts.JSONEvents {
			fmt.Fprintln(stdout, answer)
		}
		return 1
	}
	plan, err := workspace.LoadPlanArtifact(planPath)
	if err != nil {
		emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, "plan_artifact_invalid", map[string]any{
			"path":   planPath,
			"reason": err.Error(),
		})
		fmt.Fprintln(stderr, fmt.Errorf("read plan artifact %q: %w", planPath, err))
		return 1
	}
	if inheritedPackID := inheritPlanExecutePromptPackID(&plan, prompt); inheritedPackID != "" {
		emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, "plan_artifact_pack_inherited", map[string]any{
			"path":    planPath,
			"pack_id": inheritedPackID,
		})
		if err := patchPlanArtifactPackID(planPath, inheritedPackID); err != nil {
			emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, "plan_artifact_pack_inherit_persist_failed", map[string]any{
				"path":    planPath,
				"pack_id": inheritedPackID,
				"reason":  err.Error(),
			})
		}
	}
	emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, "plan_artifact_loaded", map[string]any{
		"path":    planPath,
		"edits":   len(plan.Edits),
		"pack_id": strings.TrimSpace(plan.PackID),
	})
	reviewBaseline := captureReviewRepairBaseline(cfg.Workspace.Root, planChangedPaths(plan))
	report, runErr := runExecutorPhase(cfg, plan, requiredEditPaths, opts.JSONEvents, stdout)
	_ = sessionStart // findLatestPlanArtifact already filtered; keep referenced.
	reviewReport := reviewRepairPhaseReport{Skipped: opts.SkipReviewRepair}
	var reviewErr error
	if runErr == nil && !opts.SkipReviewRepair {
		reviewReport, reviewErr = runReviewRepairPhase(cfg, prompt, plan, requiredEditPaths, &reviewBaseline, report, opts, stdout, promptRun, rec)
	}
	if !opts.JSONEvents {
		fmt.Fprintln(stdout, "")
		fmt.Fprintln(stdout, "--- plan-then-execute summary ---")
		fmt.Fprintln(stdout, report)
		fmt.Fprintln(stdout, reviewReport)
	}
	if runErr != nil {
		fmt.Fprintln(stderr, runErr)
		return 1
	}
	if reviewErr != nil {
		fmt.Fprintln(stderr, reviewErr)
		return 1
	}
	return 0
}

func parsePlanExecuteOptions(args []string, stderr io.Writer) (planExecuteOptions, error) {
	var opts planExecuteOptions
	fs := flag.NewFlagSet("idiom plan-execute", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&opts.ConfigPath, "config", "config.json", "path to config file")
	fs.StringVar(&opts.Prompt, "prompt", "", "implementation prompt")
	fs.StringVar(&opts.PromptFile, "prompt-file", "", "read implementation prompt from a file")
	fs.BoolVar(&opts.JSONEvents, "json", false, "emit structured JSONL events to stdout")
	fs.StringVar(&opts.Resume, "resume", "", "resume a prior session by id before running")
	fs.StringVar(&opts.Model, "model", "", "override llama_server.model from config")
	fs.BoolVar(&opts.DisableThinking, "no-thinking", false, "force request-level Qwen thinking off for this planner run")
	fs.IntVar(&opts.MaxTokens, "max-tokens", 0, "override planner max_tokens for this run; 0 uses plan-execute default (16384)")
	fs.BoolVar(&opts.PlannerOwnedVerification, "planner-owned-verification", false, "compatibility flag: plan-execute always treats verification as planner-owned; this is a no-op")
	fs.BoolVar(&opts.AllowBuildTests, "allow-build-tests", false, "compatibility flag: plan-execute does not run builds or tests; this is a no-op")
	fs.BoolVar(&opts.SkipReviewRepair, "skip-review-repair", false, "skip the post-execute review-repair planner pass")
	if err := fs.Parse(args); err != nil {
		return planExecuteOptions{}, err
	}
	if fs.NArg() > 0 {
		if strings.TrimSpace(opts.Prompt) != "" || strings.TrimSpace(opts.PromptFile) != "" {
			return planExecuteOptions{}, fmt.Errorf("positional prompt cannot be combined with --prompt or --prompt-file")
		}
		opts.Prompt = strings.Join(fs.Args(), " ")
	}
	if strings.TrimSpace(opts.Prompt) == "" && strings.TrimSpace(opts.PromptFile) == "" {
		return planExecuteOptions{}, fmt.Errorf("provide --prompt, --prompt-file, or a positional prompt")
	}
	return opts, nil
}

func planExecutePrompt(opts planExecuteOptions) (string, error) {
	hasPrompt := strings.TrimSpace(opts.Prompt) != ""
	hasFile := strings.TrimSpace(opts.PromptFile) != ""
	if hasPrompt && hasFile {
		return "", fmt.Errorf("--prompt and --prompt-file are mutually exclusive")
	}
	if hasPrompt {
		return opts.Prompt, nil
	}
	data, err := os.ReadFile(opts.PromptFile)
	if err != nil {
		return "", fmt.Errorf("read prompt file: %w", err)
	}
	prompt := strings.TrimSpace(string(data))
	if prompt == "" {
		return "", fmt.Errorf("prompt file %q is empty", opts.PromptFile)
	}
	return prompt, nil
}

func inheritPlanExecutePromptPackID(plan *workspace.PlanArtifact, prompt string) string {
	if plan == nil || strings.TrimSpace(plan.PackID) != "" {
		return ""
	}
	packID := extractPlanExecutePromptPackID(prompt)
	if packID == "" {
		return ""
	}
	plan.PackID = packID
	return packID
}

func extractPlanExecutePromptPackID(prompt string) string {
	for _, rawLine := range strings.Split(prompt, "\n") {
		line := strings.TrimSpace(rawLine)
		lower := strings.ToLower(line)
		if !strings.Contains(lower, "pack") {
			continue
		}
		for _, token := range strings.FieldsFunc(line, func(r rune) bool {
			return !((r >= 'a' && r <= 'z') ||
				(r >= 'A' && r <= 'Z') ||
				(r >= '0' && r <= '9'))
		}) {
			token = strings.ToLower(strings.TrimSpace(token))
			if isPlanExecutePackID(token) {
				return token
			}
		}
	}
	return ""
}

func isPlanExecutePackID(value string) bool {
	if len(value) != 16 {
		return false
	}
	for _, r := range value {
		if !((r >= 'a' && r <= 'f') || (r >= '0' && r <= '9')) {
			return false
		}
	}
	return true
}

func patchPlanArtifactPackID(path string, packID string) error {
	packID = strings.TrimSpace(packID)
	if path == "" || packID == "" {
		return nil
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return err
	}
	encodedPackID, err := json.Marshal(packID)
	if err != nil {
		return err
	}
	raw["pack_id"] = encodedPackID
	updated, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	updated = append(updated, '\n')
	return os.WriteFile(path, updated, 0o644)
}

// planExecutePlannerSystemAddendum is prepended to the brief in the
// planner phase. Kept tight by design — the 35B planner has a fixed
// thinking-token budget, and verbose addendums starve it of output
// budget before it can emit submit_plan. Every rule earned its place
// from a real failure mode; if you add another, compress an existing
// rule to make room.
func planExecutePlannerSystemAddendum() string {
	return strings.Join([]string{
		"# Plan-then-execute (planner phase)",
		"",
		"The plan-submission capability must cover every brief-required edit once; the executor dispatches the submitted edits after you stop.",
		"",
		"1. Per-edit write capabilities are blocked in the planner phase; the available write contract is `submit_plan`.",
		"2. Each edit: `path` + ONE of: (`old_text`+`new_text`; empty `new_text` deletes `old_text` | `start_line`+`end_line`+non-empty `content` | `replace_all:true`+`new_text`+empty `old_text` for new-file/whole-file write) + one-sentence `purpose`. Prefer substring/range edits; large bodies are spilled to artifact refs after submit_plan.",
		"3. One insertion site = one edit. Don't combine non-adjacent sites into one `old_text` (use \\n\\n only when sites are literally adjacent). Same `path` in two entries is fine.",
		"4. Anchor indentation must match the file byte-for-byte. Brief code blocks under markdown bullets carry 2+ extra leading spaces. Before copying any multi-line anchor, one bounded source-read capability may verify that path and strip the rendering indent. Single-line / identifier anchors are exempt.",
		"5. At most one bounded source-read capability per path, only when the brief's anchor needs verification. Never re-read the same file at a different range — if the brief is wrong, stop with `<<QUESTION>>:`.",
		"6. Submit as soon as you have anchors for every required path. If current source already contains the requested final state, answer `Decision: source-satisfied` and name every required path plus evidence instead of submitting stale edits. After `submit_plan` returns OK, finalize with a one-paragraph summary and stop. No post-plan verification reads.",
	}, "\n")
}

// findLatestPlanArtifact returns the most recently written plan
// artifact in <root>/.idiom/plans/ whose mtime is at or after
// notBefore. The notBefore guard scopes the lookup to the current
// session's planner output — without it, a planner failure silently
// falls back to a stale artifact from an unrelated prior session.
// planExecuteAcquireQueueSlot wraps runner.MaybeAcquire with the
// project's pool-name resolution and the entry-point's session/label
// context. Returns the release function unconditionally — callers
// defer it without nil-checking. When the daemon isn't reachable, or
// when acquire surfaces an error, we emit a JSON event for visibility
// but proceed un-gated; this is the "opportunistic" contract so a
// missing daemon never blocks dispatch.
func planExecuteAcquireQueueSlot(cfg config.Config, opts planExecuteOptions, sessionID string, stdout io.Writer, stderr io.Writer) func() {
	pool := strings.TrimSpace(os.Getenv("IDIOM_QUEUE_POOL"))
	if pool == "" {
		pool = runner.PoolForModelAlias(cfg.LlamaServer.Model)
	}
	if pool == "" {
		return func() {}
	}
	socket := strings.TrimSpace(os.Getenv("IDIOM_QUEUE_SOCKET"))
	label := strings.TrimSpace(opts.Resume)
	if label == "" {
		label = "plan-execute:" + sessionID
	}
	client := fmt.Sprintf("idiom-plan-execute/%d", os.Getpid())
	release, err := runner.MaybeAcquire(context.Background(), socket, pool, label, client, stderr)
	if err != nil {
		emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, "queue_acquire_failed", map[string]any{
			"pool":   pool,
			"reason": err.Error(),
		})
		return func() {}
	}
	emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, "queue_acquired", map[string]any{
		"pool":  pool,
		"label": label,
	})
	return release
}

// newPlanExecuteSessionID returns a fresh, unguessable session
// identifier for this plan-execute run. Used by the executor phase
// to filter `.idiom/plans/<stamp>.plan.json` artifacts to only those
// produced by THIS session — see findLatestPlanArtifact for the
// load-side guard. 16 hex bytes from crypto/rand: 64 bits of entropy
// is overkill for collision avoidance but trivial to generate. Falls
// back to a timestamp-derived hex on the (essentially impossible)
// rand.Read failure path so the rest of the lane still runs.
func newPlanExecuteSessionID() string {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("ts%016x", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf[:])
}

// findLatestPlanArtifact picks the most recent .idiom/plans/<stamp>.plan.json
// produced since notBefore. When sessionID is non-empty, the search is
// narrowed to plans whose persisted SessionID matches — otherwise two
// concurrent plan-execute runs in the same workspace race on the shared
// plans directory and the executor phase silently loads the wrong plan.
// Empty sessionID falls back to the legacy time-only filter for
// backward compatibility with callers that don't yet stamp a session.
func findLatestPlanArtifact(root string, notBefore time.Time, sessionID string) (string, error) {
	dir := filepath.Join(strings.TrimSpace(root), ".idiom", "plans")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("plan dir %q: %w", dir, err)
	}
	type candidate struct {
		path  string
		mtime time.Time
	}
	sessionID = strings.TrimSpace(sessionID)
	var hits []candidate
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if name == "last.plan.json" {
			continue
		}
		if !strings.HasSuffix(name, ".plan.json") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if !notBefore.IsZero() && info.ModTime().Before(notBefore) {
			continue
		}
		path := filepath.Join(dir, name)
		if sessionID != "" {
			// Read the artifact's SessionID and skip plans from other
			// sessions. Cheap (one ReadFile + Unmarshal of a small JSON
			// header) compared to the cost of dispatching the wrong
			// edits, which is what this filter prevents.
			body, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			var head struct {
				SessionID string `json:"session_id"`
			}
			if err := json.Unmarshal(body, &head); err != nil {
				continue
			}
			if strings.TrimSpace(head.SessionID) != sessionID {
				continue
			}
		}
		hits = append(hits, candidate{path: path, mtime: info.ModTime()})
	}
	if len(hits) == 0 {
		if sessionID != "" {
			return "", fmt.Errorf("no plan artifacts under %s written since %s with session_id=%s", dir, notBefore.Format(time.RFC3339), sessionID)
		}
		return "", fmt.Errorf("no plan artifacts under %s written since %s", dir, notBefore.Format(time.RFC3339))
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].mtime.After(hits[j].mtime) })
	return hits[0].path, nil
}

// executorPhaseReport summarizes what the executor phase did so the
// caller can read a one-shot status line.
type executorPhaseReport struct {
	Total        int
	Succeeded    int
	Failed       int
	Failures     []string
	SourceSanity []phaseEvidenceCheck
}

func (r executorPhaseReport) String() string {
	header := fmt.Sprintf("executor: %d edits, %d succeeded, %d failed", r.Total, r.Succeeded, r.Failed)
	if len(r.Failures) == 0 {
		return header
	}
	return header + "\n  " + strings.Join(r.Failures, "\n  ")
}

type reviewRepairPhaseReport struct {
	Skipped        bool
	Status         string
	Repairs        int
	ReviewPlan     string
	ChecklistFound bool
}

func (r reviewRepairPhaseReport) String() string {
	if r.Skipped {
		return "review-repair: skipped"
	}
	status := strings.TrimSpace(r.Status)
	if status == "" {
		status = "not run"
	}
	if r.Repairs == 0 {
		return fmt.Sprintf("review-repair: %s", status)
	}
	return fmt.Sprintf("review-repair: %s, %d corrective edit(s)", status, r.Repairs)
}

type reviewRepairEvidence struct {
	Diff          string
	DiffCheck     string
	DiffCheckExit int
	PhaseEvidence phaseEvidenceBundle
}

type reviewRepairBaseline struct {
	Files map[string]reviewRepairFileSnapshot
}

type reviewRepairFileSnapshot struct {
	Before string
	Exists bool
}

func runReviewRepairPhase(
	cfg config.Config,
	originalPrompt string,
	plan workspace.PlanArtifact,
	requiredEditPaths []string,
	baseline *reviewRepairBaseline,
	executorReport executorPhaseReport,
	opts planExecuteOptions,
	stdout io.Writer,
	promptRun promptRunner,
	rec *telemetry.Recorder,
) (reviewRepairPhaseReport, error) {
	changedPaths := planChangedPaths(plan)
	if len(changedPaths) == 0 {
		return reviewRepairPhaseReport{Status: "skipped (no changed paths)"}, nil
	}
	allowedRepairPaths := mergePlanExecutePaths(changedPaths, requiredEditPaths)
	evidence := collectReviewRepairEvidence(cfg.Workspace.Root, changedPaths, baseline)
	evidence.PhaseEvidence = collectPlanExecutePhaseEvidence(cfg.Workspace.Root, "review_repair", plan, changedPaths, requiredEditPaths, allowedRepairPaths, evidence, executorReport)
	reviewSessionID := newPlanExecuteSessionID()
	reviewCfg := cfg
	reviewCfg.Workspace.SessionID = reviewSessionID
	reviewStart := time.Now().Add(-1 * time.Second)
	emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, "review_repair_start", map[string]any{
		"session_id":     reviewSessionID,
		"changed_paths":  changedPaths,
		"allowed_paths":  allowedRepairPaths,
		"phase_evidence": phaseEvidenceTelemetry(evidence.PhaseEvidence),
	})
	prompt := planExecuteReviewRepairPrompt(originalPrompt, plan, changedPaths, allowedRepairPaths, evidence)
	reviewMaxTokens := opts.MaxTokens
	if reviewMaxTokens <= 0 {
		reviewMaxTokens = 8192
	}
	answer, err := promptRun(reviewCfg, prompt, runOptions{
		JSONEvents:               opts.JSONEvents,
		TaskFamily:               agent.PlanThenExecuteReviewRepairTaskFamily,
		PlannerOwnedVerification: true,
		MaxTokensOverride:        reviewMaxTokens,
		DisableThinking:          opts.DisableThinking,
		Telemetry:                rec,
	}, stdout)
	if err != nil {
		emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, "review_repair_failed", map[string]any{
			"reason": err.Error(),
		})
		return reviewRepairPhaseReport{Status: "failed"}, fmt.Errorf("review-repair phase failed: %w", err)
	}
	planPath, found, err := findOptionalReviewPlanArtifact(cfg.Workspace.Root, reviewStart, reviewSessionID)
	if err != nil {
		emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, "review_repair_failed", map[string]any{
			"reason": err.Error(),
		})
		return reviewRepairPhaseReport{Status: "failed"}, err
	}
	if !found {
		if reviewAnswerDeclaresPass(answer) && reviewTextHasVerificationChecklist(answer) {
			emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, "review_repair_complete", map[string]any{
				"status":  "pass",
				"repairs": 0,
			})
			return reviewRepairPhaseReport{Status: "pass (no corrective edits)", ChecklistFound: true}, nil
		}
		if reviewAnswerDeclaresBlocker(answer) {
			return reviewRepairPhaseReport{Status: "blocked"}, fmt.Errorf("review-repair phase reported a blocker without a corrective plan")
		}
		return reviewRepairPhaseReport{Status: "inconclusive"}, fmt.Errorf("review-repair phase produced no corrective plan and no `Decision: pass` with `Verification Checklist`")
	}
	repairPlan, err := workspace.LoadPlanArtifact(planPath)
	if err != nil {
		return reviewRepairPhaseReport{Status: "failed", ReviewPlan: planPath}, fmt.Errorf("read review-repair plan artifact %q: %w", planPath, err)
	}
	if !reviewTextHasVerificationChecklist(repairPlan.Rationale) {
		return reviewRepairPhaseReport{Status: "failed", ReviewPlan: planPath}, fmt.Errorf("review-repair plan %q is missing `Verification Checklist` in submit_plan rationale", planPath)
	}
	repairReport, err := runExecutorPhaseScoped(cfg, repairPlan, nil, allowedRepairPaths, opts.JSONEvents, stdout)
	if err != nil {
		return reviewRepairPhaseReport{
			Status:         "failed",
			Repairs:        repairReport.Succeeded,
			ReviewPlan:     planPath,
			ChecklistFound: true,
		}, fmt.Errorf("review-repair executor failed: %w", err)
	}
	emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, "review_repair_complete", map[string]any{
		"status":        "repaired",
		"repairs":       repairReport.Succeeded,
		"review_plan":   planPath,
		"changed_paths": allowedRepairPaths,
	})
	return reviewRepairPhaseReport{
		Status:         "repaired",
		Repairs:        repairReport.Succeeded,
		ReviewPlan:     planPath,
		ChecklistFound: true,
	}, nil
}

func findOptionalReviewPlanArtifact(root string, notBefore time.Time, sessionID string) (string, bool, error) {
	path, err := findLatestPlanArtifact(root, notBefore, sessionID)
	if err == nil {
		return path, true, nil
	}
	text := err.Error()
	if strings.Contains(text, "no plan artifacts under") || strings.Contains(text, "plan dir") {
		return "", false, nil
	}
	return "", false, err
}

func planExecuteReviewRepairPrompt(originalPrompt string, plan workspace.PlanArtifact, changedPaths []string, allowedRepairPaths []string, evidence reviewRepairEvidence) string {
	var b strings.Builder
	b.WriteString("# Plan-then-execute review-repair phase\n\n")
	b.WriteString("You are the REVIEW-REPAIR planner for a plan-execute run that already applied executor edits. Review the actual diff and behavioral contract, then either submit a narrow corrective plan or explicitly pass.\n\n")
	b.WriteString("## Required Review Contract\n\n")
	b.WriteString("Create a `Verification Checklist` before deciding. This checklist must verify behavior, not just literal code correctness. Cover:\n\n")
	b.WriteString("- Required behavior from the original task/spec.\n")
	b.WriteString("- Default/non-regression behavior that should remain equivalent.\n")
	b.WriteString("- Data, lifecycle, state, persistence, concurrency/threading, and performance invariants when relevant.\n")
	b.WriteString("- Edge cases and error/empty/default paths implied by the change.\n")
	b.WriteString("- Scope safety: changed paths, out-of-scope writes, duplicate/bespoke logic, and stale-artifact risk.\n")
	b.WriteString("- Test or command evidence available now, plus any planner-owned verification that still must run outside this lane.\n\n")
	b.WriteString("Outcomes:\n\n")
	b.WriteString("- If any checklist item fails and the repair is inside Allowed Repair Paths, call `submit_plan` once with corrective edits only. The `submit_plan.rationale` must start with `Verification Checklist:` and include the checklist plus the failing item each edit addresses.\n")
	b.WriteString("- The submitted plan is pre-executor intent. If `submitted_plan_state` says those edits are already applied, do not re-submit them; submit only new corrective edits for defects found by the checklist.\n")
	b.WriteString("- If the diff satisfies the checklist, do not call `submit_plan`; answer with `Decision: pass` and include the `Verification Checklist`.\n")
	b.WriteString("- If the issue is ambiguous or outside Allowed Repair Paths, stop with `<<QUESTION>>:` and name the blocker. Do not improvise out-of-scope writes.\n\n")
	b.WriteString("A repair that replaces missing behavioral evidence with a tautology (`assert true`, `CHECK(true)`, `_assert_true(true)`) or with comments admitting behavior is not inspected is not a valid repair. Submit a concrete state assertion or stop with `<<QUESTION>>:`.\n\n")
	b.WriteString("## Allowed Repair Paths\n\n")
	for _, path := range allowedRepairPaths {
		fmt.Fprintf(&b, "- `%s`\n", path)
	}
	b.WriteString("\n## Changed Paths\n\n")
	for _, path := range changedPaths {
		fmt.Fprintf(&b, "- `%s`\n", path)
	}
	b.WriteString("\n## Submitted Plan\n\n")
	if strings.TrimSpace(plan.Rationale) != "" {
		b.WriteString("Rationale:\n")
		b.WriteString(truncatePlanExecuteText(plan.Rationale, 4000))
		b.WriteString("\n\n")
	}
	for i, edit := range plan.Edits {
		fmt.Fprintf(&b, "%d. `%s`: %s\n", i+1, edit.Path, firstNonEmptyString(edit.Purpose, "planned edit"))
	}
	b.WriteString("\n## Phase Evidence Bundle\n\n")
	b.WriteString("Use this bundle as the current evidence ledger for the review decision. Treat open obligations as items that must be resolved by the checklist, a narrow repair, or a blocker.\n\n")
	b.WriteString("```text\n")
	b.WriteString(renderPlanExecutePhaseEvidence(evidence.PhaseEvidence))
	b.WriteString("```\n")
	b.WriteString("\n## Diff Evidence\n\n")
	if strings.TrimSpace(evidence.Diff) == "" {
		b.WriteString("(No git diff available or changed paths are not tracked by git.)\n")
	} else {
		b.WriteString("```diff\n")
		b.WriteString(truncatePlanExecuteText(evidence.Diff, 60000))
		b.WriteString("\n```\n")
	}
	b.WriteString("\n## Patch Hygiene Evidence\n\n")
	fmt.Fprintf(&b, "current-run whitespace check exit: %d\n\n", evidence.DiffCheckExit)
	if strings.TrimSpace(evidence.DiffCheck) != "" {
		b.WriteString("```text\n")
		b.WriteString(truncatePlanExecuteText(evidence.DiffCheck, 12000))
		b.WriteString("\n```\n")
	}
	b.WriteString("\n## Original Task / Spec\n\n")
	b.WriteString("Reference only. The original spec is quoted so its file allowlist does not become this review pass's active write contract.\n\n")
	b.WriteString(quotePlanExecuteReference(truncatePlanExecuteText(originalPrompt, 40000)))
	return b.String()
}

func quotePlanExecuteReference(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return "> (empty)\n"
	}
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			lines[i] = ">"
			continue
		}
		lines[i] = "> " + line
	}
	return strings.Join(lines, "\n") + "\n"
}

func collectReviewRepairEvidence(root string, paths []string, baseline *reviewRepairBaseline) reviewRepairEvidence {
	if baseline != nil {
		diff := collectReviewRepairDiffFromBaseline(root, paths, *baseline)
		check, exit := reviewRepairWhitespaceCheck(diff)
		return reviewRepairEvidence{Diff: diff, DiffCheck: check, DiffCheckExit: exit}
	}
	return reviewRepairEvidence{
		Diff:          runGitEvidence(root, append([]string{"diff", "--"}, paths...)...),
		DiffCheck:     runGitEvidence(root, append([]string{"diff", "--check", "--"}, paths...)...),
		DiffCheckExit: runGitEvidenceExit(root, append([]string{"diff", "--check", "--"}, paths...)...),
	}
}

func captureReviewRepairBaseline(root string, paths []string) reviewRepairBaseline {
	baseline := reviewRepairBaseline{Files: map[string]reviewRepairFileSnapshot{}}
	for _, path := range mergePlanExecutePaths(paths) {
		full := path
		if !filepath.IsAbs(full) {
			full = filepath.Join(root, path)
		}
		data, err := os.ReadFile(filepath.Clean(full))
		if err != nil {
			baseline.Files[path] = reviewRepairFileSnapshot{Exists: false}
			continue
		}
		baseline.Files[path] = reviewRepairFileSnapshot{Before: string(data), Exists: true}
	}
	return baseline
}

func collectReviewRepairDiffFromBaseline(root string, paths []string, baseline reviewRepairBaseline) string {
	var b strings.Builder
	for _, path := range mergePlanExecutePaths(paths) {
		snapshot, ok := baseline.Files[path]
		if !ok {
			snapshot = reviewRepairFileSnapshot{Exists: false}
		}
		full := path
		if !filepath.IsAbs(full) {
			full = filepath.Join(root, path)
		}
		var after string
		afterData, err := os.ReadFile(filepath.Clean(full))
		afterExists := err == nil
		if err == nil {
			after = string(afterData)
		} else if !os.IsNotExist(err) {
			fmt.Fprintf(&b, "# %s: current content unavailable: %v\n", path, err)
			continue
		}
		if !snapshot.Exists && !afterExists {
			continue
		}
		diff := app.UnifiedDiff(path, snapshot.Before, after)
		if strings.TrimSpace(diff) == "" {
			continue
		}
		if b.Len() > 0 && !strings.HasSuffix(b.String(), "\n") {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "diff --git a/%s b/%s\n", path, path)
		b.WriteString(diff)
		if !strings.HasSuffix(diff, "\n") {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func reviewRepairWhitespaceCheck(diff string) (string, int) {
	var findings []string
	path := ""
	newLine := 0
	inHunk := false
	for _, raw := range strings.Split(diff, "\n") {
		line := strings.TrimSuffix(raw, "\r")
		switch {
		case strings.HasPrefix(line, "+++ b/"):
			path = strings.TrimSpace(strings.TrimPrefix(line, "+++ b/"))
			inHunk = false
			newLine = 0
			continue
		case strings.HasPrefix(line, "@@ "):
			newLine = parseReviewRepairHunkNewStart(line)
			inHunk = true
			continue
		case strings.HasPrefix(line, "diff --git ") || strings.HasPrefix(line, "--- "):
			inHunk = false
			newLine = 0
			continue
		}
		if !inHunk || line == "" || strings.HasPrefix(line, "\\ ") {
			continue
		}
		switch line[0] {
		case '+':
			if strings.HasPrefix(line, "+++") {
				continue
			}
			content := line[1:]
			if strings.TrimRight(content, " \t") != content {
				loc := path
				if newLine > 0 {
					loc = fmt.Sprintf("%s:%d", firstNonEmptyString(path, "(unknown)"), newLine)
				}
				findings = append(findings, fmt.Sprintf("%s: trailing whitespace.\n%s", loc, line))
			}
			if newLine > 0 {
				newLine++
			}
		case ' ':
			if newLine > 0 {
				newLine++
			}
		case '-':
			// Removal lines do not advance the new-file cursor.
		}
	}
	if len(findings) == 0 {
		return "", 0
	}
	return strings.Join(findings, "\n"), 1
}

func parseReviewRepairHunkNewStart(line string) int {
	plus := strings.Index(line, " +")
	if plus < 0 {
		return 0
	}
	rest := line[plus+2:]
	end := 0
	for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0
	}
	value, err := strconv.Atoi(rest[:end])
	if err != nil {
		return 0
	}
	return value
}

func runGitEvidence(root string, args ...string) string {
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil && len(out) == 0 {
		return err.Error()
	}
	return string(out)
}

func runGitEvidenceExit(root string, args ...string) int {
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode()
		}
		return 127
	}
	return 0
}

func planChangedPaths(plan workspace.PlanArtifact) []string {
	paths := make([]string, 0, len(plan.Edits))
	for _, edit := range plan.Edits {
		paths = append(paths, edit.Path)
	}
	return mergePlanExecutePaths(paths)
}

func mergePlanExecutePaths(groups ...[]string) []string {
	seen := make(map[string]struct{})
	var out []string
	for _, group := range groups {
		for _, path := range group {
			normalized := normalizePlanExecutePath(path)
			if normalized == "" {
				continue
			}
			if _, ok := seen[normalized]; ok {
				continue
			}
			seen[normalized] = struct{}{}
			out = append(out, normalized)
		}
	}
	sort.Strings(out)
	return out
}

func reviewAnswerDeclaresPass(answer string) bool {
	lower := strings.ToLower(answer)
	return strings.Contains(lower, "decision: pass")
}

func reviewAnswerDeclaresBlocker(answer string) bool {
	lower := strings.ToLower(answer)
	return strings.Contains(answer, "<<QUESTION>>") || strings.Contains(lower, "decision: block") || strings.Contains(lower, "blocker")
}

func reviewTextHasVerificationChecklist(text string) bool {
	return strings.Contains(strings.ToLower(text), "verification checklist")
}

func truncatePlanExecuteText(text string, maxBytes int) string {
	if maxBytes <= 0 || len(text) <= maxBytes {
		return text
	}
	for i := range text {
		if i >= maxBytes {
			return text[:i] + fmt.Sprintf("\n\n[truncated %d bytes]\n", len(text)-i)
		}
	}
	return text
}

// runExecutorPhase iterates the plan and dispatches each edit
// against a fresh workspace.Manager rooted at the configured repo.
// It does NOT use a separate model — the plan IS the source of
// truth. Failure of one edit aborts the phase and surfaces the
// specific failure so the caller (planner re-run, human, etc.) can
// react.
func runExecutorPhase(cfg config.Config, plan workspace.PlanArtifact, requiredEditPaths []string, jsonEvents bool, stdout io.Writer) (executorPhaseReport, error) {
	return runExecutorPhaseScoped(cfg, plan, requiredEditPaths, nil, jsonEvents, stdout)
}

func runExecutorPhaseScoped(cfg config.Config, plan workspace.PlanArtifact, requiredEditPaths []string, allowedEditPaths []string, jsonEvents bool, stdout io.Writer) (executorPhaseReport, error) {
	report := executorPhaseReport{Total: len(plan.Edits)}
	if len(plan.Edits) == 0 {
		return report, fmt.Errorf("plan has no edits")
	}
	mgr := workspace.NewManager(cfg.Workspace)
	mgr.SetBypassReadBeforeEdit(true)
	ctx := context.Background()

	if mismatch := preflightPlanRequiredEditPaths(requiredEditPaths, plan); mismatch != nil {
		emitPlanExecuteJSONEvent(stdout, jsonEvents, "preflight_failed", map[string]any{
			"edit_index": mismatch.Index,
			"path":       mismatch.Path,
			"reason":     mismatch.Reason,
		})
		if !jsonEvents {
			fmt.Fprintf(stdout, "  preflight FAILED on required_edit_paths %s: %s\n", mismatch.Path, mismatch.Reason)
		}
		return report, fmt.Errorf("plan required_edit_paths preflight failed (%s): %s", mismatch.Path, mismatch.Reason)
	}
	if len(requiredEditPaths) == 0 {
		if mismatch := preflightPlanAllowedEditPaths(allowedEditPaths, plan); mismatch != nil {
			emitPlanExecuteJSONEvent(stdout, jsonEvents, "preflight_failed", map[string]any{
				"edit_index": mismatch.Index,
				"path":       mismatch.Path,
				"reason":     mismatch.Reason,
			})
			if !jsonEvents {
				fmt.Fprintf(stdout, "  preflight FAILED on allowed_edit_paths %s: %s\n", mismatch.Path, mismatch.Reason)
			}
			return report, fmt.Errorf("plan allowed_edit_paths preflight failed (%s): %s", mismatch.Path, mismatch.Reason)
		}
	}

	// Pre-execute plan validator: walk every edit's old_text and
	// confirm it appears in the file before any edit lands. This
	// turns "edit K succeeds, edit K+1 fails" partial state into a
	// clean abort with a single error pointing at the first
	// mismatched edit. Mirrors apply_patch's atomic preflight.
	if mismatch := preflightPlanEdits(cfg.Workspace.Root, plan); mismatch != nil {
		emitPlanExecuteJSONEvent(stdout, jsonEvents, "preflight_failed", map[string]any{
			"edit_index": mismatch.Index,
			"path":       mismatch.Path,
			"reason":     mismatch.Reason,
		})
		if !jsonEvents {
			fmt.Fprintf(stdout, "  preflight FAILED on edit %d/%d %s: %s\n", mismatch.Index+1, len(plan.Edits), mismatch.Path, mismatch.Reason)
		}
		return report, fmt.Errorf("plan preflight failed on edit %d (%s): %s", mismatch.Index, mismatch.Path, mismatch.Reason)
	}
	if mismatch := preflightPlanUnsupportedProofClaims(plan); mismatch != nil {
		emitPlanExecuteJSONEvent(stdout, jsonEvents, "preflight_failed", map[string]any{
			"edit_index": mismatch.Index,
			"path":       mismatch.Path,
			"reason":     mismatch.Reason,
		})
		if !jsonEvents {
			fmt.Fprintf(stdout, "  preflight FAILED on edit %d/%d %s: %s\n", mismatch.Index+1, len(plan.Edits), mismatch.Path, mismatch.Reason)
		}
		return report, fmt.Errorf("plan preflight failed on edit %d (%s): %s", mismatch.Index, mismatch.Path, mismatch.Reason)
	}
	emitPlanExecuteJSONEvent(stdout, jsonEvents, "preflight_ok", map[string]any{
		"edits": len(plan.Edits),
	})

	// If the planner stamped a pack_id, surface that the executor
	// inherited the planner's context view. The cached pack carries the
	// planner's ranked candidate files / symbols / tests; downstream
	// consumers (UIs, debug tooling, future smart-skipping) can read
	// it via comprehension.LoadViewByID. The executor itself doesn't
	// shortcut discovery yet — this hook proves the wire is connected
	// and gives debug telemetry a hook to attach to.
	if strings.TrimSpace(plan.PackID) != "" {
		if view, hit, err := comprehension.LoadViewByID(cfg.Workspace.Root, plan.PackID); err == nil && hit {
			emitPlanExecuteJSONEvent(stdout, jsonEvents, "executor_inherits_pack", map[string]any{
				"pack_id":           plan.PackID,
				"index_id":          view.IndexID,
				"candidate_files":   len(view.CandidateFiles),
				"candidate_symbols": len(view.CandidateSymbols),
				"tests":             len(view.Tests),
				"fresh":             view.Fresh,
			})
			if !jsonEvents {
				fmt.Fprintf(stdout, "  executor inherits planner pack %s (%d files, %d symbols, %d tests)\n",
					plan.PackID, len(view.CandidateFiles), len(view.CandidateSymbols), len(view.Tests))
			}
		} else {
			emitPlanExecuteJSONEvent(stdout, jsonEvents, "executor_pack_miss", map[string]any{
				"pack_id": plan.PackID,
				"reason": func() string {
					if err != nil {
						return err.Error()
					}
					return "cache miss (row stale or absent)"
				}(),
			})
		}
	}

	executorStartedAt := time.Now()
	emitPlanExecuteJSONEvent(stdout, jsonEvents, "executor_start", map[string]any{
		"edits": len(plan.Edits),
	})
	for i, edit := range plan.Edits {
		// Whole-file / new-file shape: replace_all=true with empty
		// old_text + new_text=full body. submit_plan accepts this
		// shape (and the planner addendum tells the model to use it
		// for new files), but edit_file rejects it — so we dispatch
		// to write_file instead, mapping new_text → content. Without
		// this branch the executor would fail every new-file edit
		// with "edit_file requires old_text..." even though the plan
		// validator accepted it.
		isWholeFile := edit.ReplaceAll && strings.TrimSpace(edit.OldText) == "" && strings.TrimSpace(edit.NewText) != ""
		var req workspace.ToolRequest
		if isWholeFile {
			req = workspace.ToolRequest{
				Tool:        "write_file",
				Path:        edit.Path,
				Content:     edit.NewText,
				Description: firstNonEmptyString(edit.Purpose, "executor phase: whole-file write"),
			}
		} else {
			req = workspace.ToolRequest{
				Tool:        "edit_file",
				Path:        edit.Path,
				OldText:     edit.OldText,
				NewText:     edit.NewText,
				StartLine:   edit.StartLine,
				EndLine:     edit.EndLine,
				Content:     edit.Content,
				ReplaceAll:  edit.ReplaceAll,
				Description: firstNonEmptyString(edit.Purpose, "executor phase: planned edit"),
			}
		}
		result := mgr.Execute(ctx, req)
		status := "ok"
		if strings.TrimSpace(result.Error) != "" {
			status = "failed"
		}
		emitPlanExecuteJSONEvent(stdout, jsonEvents, "executor_edit", map[string]any{
			"step":   i + 1,
			"total":  len(plan.Edits),
			"path":   edit.Path,
			"status": status,
			"error":  strings.TrimSpace(result.Error),
		})
		if !jsonEvents {
			fmt.Fprintf(stdout, "  edit %d/%d %s ... %s\n", i+1, len(plan.Edits), edit.Path, executorEditOutcome(result))
		}
		if status == "failed" {
			report.Failed++
			report.Failures = append(report.Failures, fmt.Sprintf("edits[%d] %s: %s", i, edit.Path, result.Error))
			emitPlanExecuteJSONEvent(stdout, jsonEvents, "executor_complete", map[string]any{
				"total":       report.Total,
				"succeeded":   report.Succeeded,
				"failed":      report.Failed,
				"duration_ms": time.Since(executorStartedAt).Milliseconds(),
			})
			return report, fmt.Errorf("executor phase aborted on edit %d (%s): %s", i, edit.Path, result.Error)
		}
		report.Succeeded++
	}
	sourceSanityChecked, sourceSanityEvidence, err := runPlanExecuteSourceSanity(ctx, mgr, cfg.Workspace.Root, planChangedPaths(plan), jsonEvents, stdout)
	report.SourceSanity = sourceSanityEvidence
	if err != nil {
		emitPlanExecuteJSONEvent(stdout, jsonEvents, "executor_complete", map[string]any{
			"total":                 report.Total,
			"succeeded":             report.Succeeded,
			"failed":                report.Failed,
			"source_sanity_checked": sourceSanityChecked,
			"source_sanity_status":  "failed",
			"duration_ms":           time.Since(executorStartedAt).Milliseconds(),
		})
		return report, err
	}
	emitPlanExecuteJSONEvent(stdout, jsonEvents, "executor_complete", map[string]any{
		"total":                 report.Total,
		"succeeded":             report.Succeeded,
		"failed":                report.Failed,
		"source_sanity_checked": sourceSanityChecked,
		"source_sanity_status":  "passed",
		"duration_ms":           time.Since(executorStartedAt).Milliseconds(),
	})
	return report, nil
}

func runPlanExecuteSourceSanity(ctx context.Context, mgr *workspace.Manager, root string, paths []string, jsonEvents bool, stdout io.Writer) (int, []phaseEvidenceCheck, error) {
	checked := 0
	evidence := make([]phaseEvidenceCheck, 0)
	for _, path := range mergePlanExecutePaths(paths) {
		if !workspace.SourceSanityCoversPath(path) {
			continue
		}
		fullPath := path
		if !filepath.IsAbs(fullPath) {
			fullPath = filepath.Join(root, path)
		}
		info, err := os.Stat(filepath.Clean(fullPath))
		if err != nil || info.IsDir() {
			continue
		}
		checked++
		result := mgr.Execute(ctx, workspace.ToolRequest{Tool: "source_sanity", Path: path})
		status := "ok"
		errText := strings.TrimSpace(result.Error)
		if errText != "" {
			status = "failed"
		}
		evidence = append(evidence, phaseEvidenceCheck{
			Kind:    "source_sanity",
			Path:    path,
			Status:  status,
			Summary: strings.TrimSpace(result.Summary),
			Error:   errText,
		})
		emitPlanExecuteJSONEvent(stdout, jsonEvents, "source_sanity", map[string]any{
			"path":    path,
			"status":  status,
			"summary": strings.TrimSpace(result.Summary),
			"error":   errText,
		})
		if !jsonEvents {
			fmt.Fprintf(stdout, "  source sanity %s ... %s\n", path, status)
		}
		if errText != "" {
			return checked, evidence, fmt.Errorf("source_sanity failed for %s: %s", path, errText)
		}
	}
	return checked, evidence, nil
}

// planEditPreflightMismatch describes the first edit whose old_text
// does not appear in its target file. Used by preflightPlanEdits to
// abort the executor phase before any edit lands when a plan would
// otherwise partially-apply.
type planEditPreflightMismatch struct {
	Index  int
	Path   string
	Reason string
}

// preflightPlanRequiredEditPaths validates the executor's plan
// against the brief-derived required_edit_paths before any file write
// runs. The planner-phase runner normally rejects bad submit_plan
// calls, but the executor is the final deterministic boundary: stale,
// hand-written, or future plan artifacts must not be able to skip a
// required path or write outside the scoped packet.
func preflightPlanRequiredEditPaths(required []string, plan workspace.PlanArtifact) *planEditPreflightMismatch {
	requiredSet := make(map[string]struct{}, len(required))
	for _, path := range required {
		normalized := normalizePlanExecutePath(path)
		if normalized != "" {
			requiredSet[normalized] = struct{}{}
		}
	}
	if len(requiredSet) == 0 {
		return nil
	}
	planned := make(map[string]struct{}, len(plan.Edits))
	for i, edit := range plan.Edits {
		path := normalizePlanExecutePath(edit.Path)
		if path == "" {
			return &planEditPreflightMismatch{Index: i, Reason: "edit has no path"}
		}
		planned[path] = struct{}{}
		if _, ok := requiredSet[path]; !ok {
			return &planEditPreflightMismatch{
				Index:  i,
				Path:   path,
				Reason: "plan edit path is outside required_edit_paths allowlist",
			}
		}
	}
	for _, path := range required {
		normalized := normalizePlanExecutePath(path)
		if normalized == "" {
			continue
		}
		if _, ok := planned[normalized]; !ok {
			return &planEditPreflightMismatch{
				Index:  -1,
				Path:   normalized,
				Reason: "plan does not include an edit for this required_edit_paths entry",
			}
		}
	}
	return nil
}

func preflightPlanAllowedEditPaths(allowed []string, plan workspace.PlanArtifact) *planEditPreflightMismatch {
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, path := range allowed {
		normalized := normalizePlanExecutePath(path)
		if normalized != "" {
			allowedSet[normalized] = struct{}{}
		}
	}
	if len(allowedSet) == 0 {
		return nil
	}
	for i, edit := range plan.Edits {
		path := normalizePlanExecutePath(edit.Path)
		if path == "" {
			return &planEditPreflightMismatch{Index: i, Reason: "edit has no path"}
		}
		if _, ok := allowedSet[path]; !ok {
			return &planEditPreflightMismatch{
				Index:  i,
				Path:   path,
				Reason: "plan edit path is outside allowed_edit_paths allowlist",
			}
		}
	}
	return nil
}

func preflightPlanUnsupportedProofClaims(plan workspace.PlanArtifact) *planEditPreflightMismatch {
	for i, edit := range plan.Edits {
		path := normalizePlanExecutePath(edit.Path)
		if path == "" || !workspace.SourceSanityCoversPath(path) {
			continue
		}
		added := planEditAddedText(edit)
		if strings.TrimSpace(added) == "" {
			continue
		}
		if reason := unsupportedProofClaimReason(added); reason != "" {
			return &planEditPreflightMismatch{Index: i, Path: path, Reason: reason}
		}
	}
	return nil
}

func planEditAddedText(edit workspace.PlanEdit) string {
	if strings.TrimSpace(edit.Content) != "" {
		return edit.Content
	}
	if strings.TrimSpace(edit.NewText) != "" {
		return edit.NewText
	}
	return ""
}

func unsupportedProofClaimReason(text string) string {
	for lineNo, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if noOpAssertionLine(line) {
			return fmt.Sprintf("unsupported proof claim: no-op assertion on line %d; submit a concrete state assertion or report a blocker", lineNo+1)
		}
		if phrase := selfDisqualifyingProofPhrase(line); phrase != "" {
			return fmt.Sprintf("unsupported proof claim: self-disqualifying verification text %q on line %d; submit concrete evidence or report a blocker", phrase, lineNo+1)
		}
	}
	return ""
}

func noOpAssertionLine(line string) bool {
	compact := strings.Join(strings.Fields(line), "")
	normalized := strings.ToLower(compact)
	return strings.Contains(compact, "CHECK(true") ||
		strings.Contains(compact, "REQUIRE(true") ||
		strings.Contains(compact, "ASSERT_TRUE(true") ||
		strings.Contains(compact, "EXPECT_TRUE(true") ||
		strings.Contains(normalized, "_assert_true(true") ||
		strings.Contains(normalized, "assert_true(true") ||
		strings.Contains(normalized, "asserttrue(true") ||
		strings.Contains(normalized, "assert(true") ||
		strings.Contains(normalized, "assert.true(true") ||
		strings.Contains(normalized, "assert.true(t,true") ||
		strings.Contains(normalized, "require.true(t,true")
}

func selfDisqualifyingProofPhrase(line string) string {
	lower := strings.ToLower(line)
	phrases := []string{
		"can't easily test",
		"cannot easily test",
		"can't easily access",
		"cannot easily access",
		"can't easily inspect",
		"cannot easily inspect",
		"not easily test",
		"simplified check",
		"for now, we just verify",
		"for now we just verify",
		"verify that it exists",
		"exists and can be called",
		"exists and can be accessed",
	}
	for _, phrase := range phrases {
		if strings.Contains(lower, phrase) && verificationClaimContext(line, phrase) {
			return phrase
		}
	}
	return ""
}

func verificationClaimContext(line string, phrase string) bool {
	lower := strings.ToLower(strings.TrimSpace(line))
	if strings.Contains(phrase, "test") || strings.Contains(phrase, "verify") || strings.Contains(phrase, "check") {
		return true
	}
	if strings.HasPrefix(lower, "#") || strings.HasPrefix(lower, "//") || strings.HasPrefix(lower, "/*") || strings.HasPrefix(lower, "*") {
		return true
	}
	return strings.Contains(lower, "assert") ||
		strings.Contains(lower, "check") ||
		strings.Contains(lower, "verify") ||
		strings.Contains(lower, "test")
}

func normalizePlanExecutePath(path string) string {
	path = strings.TrimSpace(path)
	path = strings.TrimPrefix(path, "./")
	return path
}

// preflightPlanEdits validates every plan edit's old_text against the
// current file content before any edit dispatches. Returns nil when
// every substring-shaped edit matches; otherwise returns a pointer to
// the first mismatch. Range-shaped edits (start_line+end_line+content,
// no old_text) are skipped — those don't depend on substring matching.
//
// The walk is deliberately read-only: it loads each unique path once
// and reuses the buffer for every edit on that path. Edits ordered K
// and K+1 on the same path are validated against the SAME file
// content (the version on disk before any executor dispatch); this
// matches the executor's per-edit substring search semantics, which
// look at the file as edited up to that point. For a plan with two
// non-overlapping edits on the same file, both anchors must appear
// in the original content; once edit K lands, edit K+1's anchor must
// still appear in the modified content. The preflight catches the
// common case where the plan's anchor is wrong from the start.
func preflightPlanEdits(workspaceRoot string, plan workspace.PlanArtifact) *planEditPreflightMismatch {
	cache := make(map[string][]byte)
	for i, edit := range plan.Edits {
		oldText := strings.TrimSpace(edit.OldText)
		if oldText == "" {
			// Range-shaped or replace_all-only edit — let the
			// executor handle validation.
			continue
		}
		path := strings.TrimSpace(edit.Path)
		if path == "" {
			return &planEditPreflightMismatch{Index: i, Reason: "edit has no path"}
		}
		abs := path
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(strings.TrimSpace(workspaceRoot), path)
		}
		body, ok := cache[abs]
		if !ok {
			content, err := os.ReadFile(abs)
			if err != nil {
				return &planEditPreflightMismatch{Index: i, Path: path, Reason: fmt.Sprintf("could not read file: %s", err.Error())}
			}
			body = content
			cache[abs] = body
		}
		current := string(body)
		matchCount := strings.Count(current, edit.OldText)
		if matchCount == 0 {
			return &planEditPreflightMismatch{
				Index:  i,
				Path:   path,
				Reason: "old_text does not appear in file (whitespace/indentation mismatch is the most common cause)",
			}
		}
		if matchCount > 1 && !edit.ReplaceAll {
			return &planEditPreflightMismatch{
				Index:  i,
				Path:   path,
				Reason: fmt.Sprintf("old_text matched %d locations; use a line-range edit, extend old_text with surrounding context, or set replace_all=true when every occurrence is intended", matchCount),
			}
		}
	}
	if mismatch := preflightPlanFinalState(workspaceRoot, plan); mismatch != nil {
		return mismatch
	}
	return nil
}

func preflightPlanFinalState(workspaceRoot string, plan workspace.PlanArtifact) *planEditPreflightMismatch {
	cache := make(map[string][]byte)
	for i, edit := range plan.Edits {
		path := strings.TrimSpace(edit.Path)
		if path == "" {
			return &planEditPreflightMismatch{Index: i, Reason: "edit has no path"}
		}
		abs := path
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(strings.TrimSpace(workspaceRoot), path)
		}
		body, ok := cache[abs]
		if !ok {
			content, err := os.ReadFile(abs)
			if err != nil {
				if os.IsNotExist(err) && edit.ReplaceAll && strings.TrimSpace(edit.OldText) == "" && strings.TrimSpace(edit.NewText) != "" {
					content = nil
				} else {
					return &planEditPreflightMismatch{Index: i, Path: path, Reason: fmt.Sprintf("could not read file: %s", err.Error())}
				}
			}
			body = content
			cache[abs] = body
		}
		if edit.ReplaceAll && strings.TrimSpace(edit.OldText) == "" && strings.TrimSpace(edit.NewText) != "" && workspace.IsCompletedPlanningArtifact(path, body) {
			return &planEditPreflightMismatch{
				Index:  i,
				Path:   path,
				Reason: "whole-file edit would overwrite a completed planning artifact; use a bounded anchored edit or ask the user to reopen/replace it explicitly",
			}
		}
		updated, changed, err := workspace.PreviewPlanEditContent(string(body), edit)
		if err != nil {
			return &planEditPreflightMismatch{Index: i, Path: path, Reason: err.Error()}
		}
		if changed {
			if lineNo, line, ok := workspace.NewSuspiciousAdjacentDuplicateCodeLine(path, body, []byte(updated)); ok {
				return &planEditPreflightMismatch{
					Index:  i,
					Path:   path,
					Reason: fmt.Sprintf("edit would introduce adjacent duplicate code line %d: %q", lineNo, line),
				}
			}
		}
		cache[abs] = []byte(updated)
	}
	return nil
}

func executorEditOutcome(result workspace.ToolResult) string {
	if strings.TrimSpace(result.Error) != "" {
		return "FAILED: " + result.Error
	}
	return "ok"
}

// emitPlanExecuteJSONEvent writes one JSONL line to stdout when the
// orchestrator is running with --json. Each event carries an
// idiom.plan_execute.* type so downstream log parsers can recognize
// orchestrator-emitted events distinct from the runner's JSON stream
// (which closes when the planner phase exits).
//
// All events include an ISO-8601 timestamp so reviewers can stitch
// them into the planner-phase events using ts ordering.
func emitPlanExecuteJSONEvent(stdout io.Writer, jsonEvents bool, eventType string, fields map[string]any) {
	if !jsonEvents {
		return
	}
	payload := map[string]any{
		"type": "idiom.plan_execute." + eventType,
		"ts":   time.Now().UTC().Format(time.RFC3339Nano),
	}
	maps.Copy(payload, fields)
	body, err := json.Marshal(payload)
	if err != nil {
		// json.Marshal on a map[string]any with primitive values
		// shouldn't fail; if it does, swallow rather than crash the
		// orchestrator — the executor phase has already done the
		// real work and a missing telemetry event isn't a blocker.
		return
	}
	fmt.Fprintln(stdout, string(body))
}

// firstNonEmptyString — small helper duplicated here to avoid
// pulling in the agent package's variant.
func firstNonEmptyString(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
