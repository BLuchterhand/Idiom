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
	"path/filepath"
	"sort"
	"strings"
	"time"

	"idiom/internal/agent"
	"idiom/internal/comprehension"
	"idiom/internal/config"
	"idiom/internal/runner"
	"idiom/internal/telemetry"
	"idiom/internal/workspace"
)

// planExecuteOptions configures `idiom plan-execute`.
type planExecuteOptions struct {
	ConfigPath string
	Prompt     string
	PromptFile string
	Model      string
	Resume     string
	JSONEvents bool
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

	// Phase 1: planner. The planner runs as an implementation lane
	// with the plan-then-execute system addendum that swaps per-edit
	// writes for submit_plan. We reuse the existing implement runner
	// because that lane already has the right read-side guards and
	// brief audit; only the tool surface differs.
	plannerPrompt := planExecutePlannerSystemAddendum() + "\n\n" + prompt
	answer, err := promptRun(cfg, plannerPrompt, runOptions{
		JSONEvents:               opts.JSONEvents,
		Resume:                   opts.Resume,
		TaskFamily:               agent.PlanThenExecutePlannerTaskFamily,
		PlannerOwnedVerification: true,
		// max_tokens for the planner phase: 16384.
		//
		// Originally 4096 ("planner emits one submit_plan + short answer,
		// 4096 is plenty"). That assumption broke when submit_plan's
		// `new_text` field carries the full content of NEW FILE edits —
		// a 400-line Go file plus structure overhead easily exceeds
		// 4096 tokens, and the response truncates silently mid-JSON
		// (manifesting as "completion gate rejected, no write present"
		// because the truncated JSON failed to parse as a valid tool
		// call). See task-64b for the failure repro.
		//
		// Qwen3 documents 32768 as the recommended default; 16384 is a
		// middle ground that handles 1000+ line briefs while still
		// constraining latency on small atoms (most planner responses
		// stay well under 4096 anyway — the cap only matters when the
		// content is large).
		MaxTokensOverride: 16384,
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
	emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, "plan_artifact_loaded", map[string]any{
		"path":  planPath,
		"edits": len(plan.Edits),
	})
	report, runErr := runExecutorPhase(cfg, plan, opts.JSONEvents, stdout)
	_ = sessionStart // findLatestPlanArtifact already filtered; keep referenced.
	if !opts.JSONEvents {
		fmt.Fprintln(stdout, "")
		fmt.Fprintln(stdout, "--- plan-then-execute summary ---")
		fmt.Fprintln(stdout, report)
	}
	if runErr != nil {
		fmt.Fprintln(stderr, runErr)
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
	fs.BoolVar(&opts.PlannerOwnedVerification, "planner-owned-verification", false, "compatibility flag: plan-execute always treats verification as planner-owned; this is a no-op")
	fs.BoolVar(&opts.AllowBuildTests, "allow-build-tests", false, "compatibility flag: plan-execute does not run builds or tests; this is a no-op")
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
		"Call `submit_plan` once with every brief-required edit; the executor dispatches them after you stop.",
		"",
		"1. Per-edit writes (edit_file / apply_patch / delegate_edit / write_file / append_file / mkdir / go_move_declarations) are blocked. Use `submit_plan`.",
		"2. Each edit: `path` + ONE of: (`old_text`+`new_text` | `start_line`+`end_line`+`content` | `replace_all:true`+`new_text`+empty `old_text` for new-file/whole-file write) + one-sentence `purpose`.",
		"3. One insertion site = one edit. Don't combine non-adjacent sites into one `old_text` (use \\n\\n only when sites are literally adjacent). Same `path` in two entries is fine.",
		"4. Anchor indentation must match the file byte-for-byte. Brief code blocks under markdown bullets carry 2+ extra leading spaces. Before copying any multi-line anchor, do ONE `read_file` of the path and strip the rendering indent. Single-line / identifier anchors are exempt.",
		"5. At most one `read_file` per path, only when the brief's anchor needs verification. Never re-read the same file at a different range — if the brief is wrong, stop with `<<QUESTION>>:`.",
		"6. Submit as soon as you have anchors for every required path. After `submit_plan` returns OK, finalize with a one-paragraph summary and stop. No post-plan verification reads.",
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
	Total     int
	Succeeded int
	Failed    int
	Failures  []string
}

func (r executorPhaseReport) String() string {
	header := fmt.Sprintf("executor: %d edits, %d succeeded, %d failed", r.Total, r.Succeeded, r.Failed)
	if len(r.Failures) == 0 {
		return header
	}
	return header + "\n  " + strings.Join(r.Failures, "\n  ")
}

// runExecutorPhase iterates the plan and dispatches each edit
// against a fresh workspace.Manager rooted at the configured repo.
// It does NOT use a separate model — the plan IS the source of
// truth. Failure of one edit aborts the phase and surfaces the
// specific failure so the caller (planner re-run, human, etc.) can
// react.
func runExecutorPhase(cfg config.Config, plan workspace.PlanArtifact, jsonEvents bool, stdout io.Writer) (executorPhaseReport, error) {
	report := executorPhaseReport{Total: len(plan.Edits)}
	if len(plan.Edits) == 0 {
		return report, fmt.Errorf("plan has no edits")
	}
	mgr := workspace.NewManager(cfg.Workspace)
	mgr.SetBypassReadBeforeEdit(true)
	ctx := context.Background()

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
			"step":  i + 1,
			"total": len(plan.Edits),
			"path":  edit.Path,
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
	emitPlanExecuteJSONEvent(stdout, jsonEvents, "executor_complete", map[string]any{
		"total":       report.Total,
		"succeeded":   report.Succeeded,
		"failed":      report.Failed,
		"duration_ms": time.Since(executorStartedAt).Milliseconds(),
	})
	return report, nil
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
		if !strings.Contains(string(body), edit.OldText) {
			return &planEditPreflightMismatch{
				Index:  i,
				Path:   path,
				Reason: "old_text does not appear in file (whitespace/indentation mismatch is the most common cause)",
			}
		}
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
