package main

import (
	"context"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"idiom/internal/app"
	"idiom/internal/backends"
	"idiom/internal/config"
	"idiom/internal/telemetry"
	"idiom/internal/workspace"
)

// briefBodyStartMarker / briefBodyEndMarker bracket the project
// brief inside the composed lane prompt so downstream extractors
// (path detection, discovery handles, required_edit_paths) can
// scope their scan to the brief and ignore prose tokens in the
// surrounding preamble/postamble. Kept exported so the matching
// pathparse-side scoping helper can reference the same string.
//
// Format is an HTML comment (invisible in rendered markdown) with
// a stable IDIOM: prefix so the marker can never accidentally
// collide with prose content from a brief author.
const (
	briefBodyStartMarker = "<!-- IDIOM:BRIEF-BODY-START -->"
	briefBodyEndMarker   = "<!-- IDIOM:BRIEF-BODY-END -->"
)

// canonicalLanePromptsFS holds the idiom-canonical lane prompt
// preambles/postambles, embedded into the binary at build time so
// `idiom lane <mode>` works against any project without depending on
// a co-located prompts/ directory or per-project copy. Project-side
// extensions still live at <project>/.idiom/prompts/lanes/<mode>/
// and are concatenated AROUND the brief between idiom's canonical
// preamble and postamble (see composeLanePromptToFile).
//
//go:embed lanes/implement/preamble.md lanes/implement/postamble.md lanes/plan-execute/preamble.md lanes/plan-execute/postamble.md lanes/rules/loop-protocol.md lanes/rules/brief-skeleton.md lanes/rules/known-traps-generic.md
var canonicalLanePromptsFS embed.FS

// canonicalRuleNames is the ordered set of idiom-canonical generic
// rules embedded under cmd/idiom/lanes/rules/. Order is stable: rules
// are prepended to the composed lane prompt in this sequence so brief
// authors and overlay authors can reason about where a replacement
// falls inside the rules block.
//
// orchestrator-workflow.md is intentionally absent — per the
// reusability design's "What about the dual-architect orchestrator
// loop?" recommendation, that content belongs with skill templates
// (Phase 7), not the executor's per-lane rules block.
var canonicalRuleNames = []string{
	"loop-protocol.md",
	"brief-skeleton.md",
	"known-traps-generic.md",
}

// laneSubcommandRunner is the function shape both runImplementMain
// and runPlanExecuteMain expose. Capturing it as a type lets tests
// inject fakes that record what the lane wrapper forwarded.
type laneSubcommandRunner func(args []string, stdout io.Writer, stderr io.Writer, promptRun promptRunner) int

// laneDeps groups the lane wrapper's external dependencies so tests
// can swap in fakes without touching package-level globals.
type laneDeps struct {
	loadConfig       func(path string) (config.Config, error)
	runPlanExecute   laneSubcommandRunner
	runImplement     laneSubcommandRunner
	defaultPromptRun promptRunner
	buildContextPack laneContextPackBuilder
	telemetry        *telemetry.Recorder
}

type laneContextPackBuilder func(opts laneOptions, cfg config.Config, stderr io.Writer) string

// laneOptions captures the parsed command-line arguments for the
// lane wrapper. Fields that can be overridden at the CLI map 1:1 to
// flags; ProjectRoot is used by tests (and may eventually surface as
// --project-root for advanced workflows).
type laneOptions struct {
	Mode                     string
	BriefPath                string
	ConfigPath               string
	Title                    string
	Model                    string
	BackendProfile           string // names a profile in .idiom/backends.json
	JSONEvents               bool
	Resume                   string
	PlannerOwnedVerification bool
	AllowBuildTests          bool
	DisableThinking          bool
	ProjectRoot              string // overridable for tests; defaults to filepath.Dir(BriefPath)
	NoLint                   bool   // skip the brief lint preflight (default: lint runs)
	NoContextPack            bool   // skip automatic ranked context_pack prompt preload
	SlotKey                  string // if set, acquire a filesystem mutex named idiom-lane-<key>
	Postflight               bool   // run postflight against git diff after the lane returns
	AllowStaleBrief          bool   // override the deterministic stale-brief gate (default: gate fails if any required path has commits newer than brief mtime)
	AllowDirtyAtomScope      bool   // override atom-intake dirty scoped-path blocking
	RefineSpecPath           string // refine mode: path to 1-paragraph spec
	RefineOutputPath         string // refine mode: where to write the converged brief
	RefineMaxIters           int    // refine mode: max draft+review+patch iterations (default 3)
	RefineMode               string // refine mode: deliverable shape override ("anchored" | "markdown"); empty = auto-detect
	Since                    string // history mode: duration string for --since (e.g. "1h", "24h")
	FailedOnly               bool   // history mode: only show failed events
	ModeFilter               string // history mode: only show events for this lane mode
	ExplainSessionID         string // explain mode: specific session id (empty = latest)
	ExplainBriefFilter       string // explain mode: filter by brief path (empty = unfiltered)
	MaxTokens                int    // forwarded to the wrapped subcommand's --max-tokens override
	// coderRequested is set when the caller asked for the coder
	// alias either explicitly (`--coder`) or implicitly (plan-execute
	// with no --model override). The actual alias is resolved later
	// in runLaneMainWithDeps once backends.json has loaded so projects
	// can override the default without changing every invocation.
	coderRequested         bool
	explicitCoderRequested bool
}

// laneBriefLintRunner is the package-level indirection tests stub
// out so the lane wrapper's default-on lint preflight doesn't need
// python3 in the test environment. Production callers leave it as
// runBriefLint; tests reassign before exercising the lane and
// restore on cleanup. Mirrors the backendLaunch / backendStop
// pattern from cmd/idiom/backend.go.
var laneBriefLintRunner = func(args []string, stdout io.Writer, stderr io.Writer) int {
	return runBriefLint(args, stdout, stderr, nil)
}

var lanePostflightRunner = func(args []string, stdout io.Writer, stderr io.Writer) int {
	return runLanePostflight(args, stdout, stderr)
}

// laneGitDiff is a package-level indirection over the `git diff`
// shell-out runLanePostflightAfterLane uses to capture the working-
// tree diff. Tests stub this so they don't need an actual git repo
// in the temp workspace; production calls into git via os/exec.
var laneGitDiff = func(projectRoot string) ([]byte, error) {
	cmd := exec.Command("git", "diff")
	cmd.Dir = projectRoot
	return cmd.Output()
}

// runLaneMain is the production entry point for `idiom lane <mode>`.
// It loads config from disk and forwards to runLaneMainWithTelemetry.
func runLaneMain(args []string, stdout io.Writer, stderr io.Writer) int {
	return runLaneMainWithDeps(args, stdout, stderr, laneDeps{
		loadConfig:       config.Load,
		runPlanExecute:   runPlanExecuteMain,
		runImplement:     runImplementMain,
		defaultPromptRun: runPromptOnce,
		buildContextPack: buildLaneContextPackSection,
	})
}

// runLaneMainWithTelemetry is the variant the top-level dispatcher
// uses when telemetry is wired. Behavior is identical apart from
// passing the recorder through to the underlying subcommand.
func runLaneMainWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	deps := laneDeps{
		loadConfig: config.Load,
		runPlanExecute: func(args []string, stdout io.Writer, stderr io.Writer, promptRun promptRunner) int {
			return runPlanExecuteMainWithTelemetry(args, stdout, stderr, promptRun, rec)
		},
		runImplement: func(args []string, stdout io.Writer, stderr io.Writer, promptRun promptRunner) int {
			return runImplementMainWithTelemetry(args, stdout, stderr, promptRun, rec)
		},
		defaultPromptRun: runPromptOnce,
		buildContextPack: buildLaneContextPackSection,
		telemetry:        rec,
	}
	return runLaneMainWithDeps(args, stdout, stderr, deps)
}

// runLaneMainWithDeps is the testable core. It parses args, composes
// the prompt with project overlays, writes the composed prompt to a
// temp file, and forwards to the underlying lane subcommand.
func runLaneMainWithDeps(args []string, stdout io.Writer, stderr io.Writer, deps laneDeps) int {
	// Sub-subcommands (postflight today; later: probe, summarize)
	// run before the mode parser since they aren't lane invocations
	// — they don't compose a brief, don't dispatch to plan-execute
	// or implement, and have their own flag set.
	if len(args) > 0 && args[0] == "postflight" {
		return runLanePostflight(args[1:], stdout, stderr)
	}
	opts, err := parseLaneOptions(args, stderr)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	cfg, err := deps.loadConfig(opts.ConfigPath)
	if err != nil {
		// Tests pass a stub loadConfig that never errors; production
		// loads from disk and fails loudly if config is missing.
		fmt.Fprintln(stderr, err)
		return 1
	}
	// Resolve project root for overlay lookup. Precedence:
	//   1. --project-root flag (explicit override; rarely set)
	//   2. config workspace.root (the canonical project root —
	//      this is what idiom uses everywhere else for path
	//      resolution, and what discovers the right .idiom/
	//      overlay directory regardless of where the brief lives)
	//   3. brief's parent directory (last-resort fallback for
	//      configs that omit workspace.root, e.g. tests)
	if strings.TrimSpace(opts.ProjectRoot) == "" {
		if root := strings.TrimSpace(cfg.Workspace.Root); root != "" {
			opts.ProjectRoot = root
		} else {
			opts.ProjectRoot = filepath.Dir(opts.BriefPath)
		}
	}
	// Resolve the lane model alias now that project root is known.
	// This covers both explicit --coder and the plan-execute auto-route;
	// an explicit --model already short-circuited coderRequested in
	// parseLaneOptions, so this doesn't fight a deliberate choice.
	// Named plan-execute profiles can select a planner role; otherwise
	// we keep the legacy executor fallback.
	if opts.coderRequested && strings.TrimSpace(opts.Model) == "" {
		opts.Model = resolveLaneModelAlias(opts, stderr)
	}
	if opts.Mode == "history" {
		filter := laneHistoryFilter{FailedOnly: opts.FailedOnly, ModeFilter: opts.ModeFilter}
		if opts.Since != "" {
			dur, err := time.ParseDuration(opts.Since)
			if err != nil {
				fmt.Fprintf(stderr, "lane: history: invalid --since value %q: %v\n", opts.Since, err)
				return 2
			}
			filter.Since = time.Now().UTC().Add(-dur)
		}
		if opts.ModeFilter != "" && !isKnownLaneMode(opts.ModeFilter) {
			fmt.Fprintf(stderr, "lane: history: unknown --lane-mode %q (valid: plan-execute, implement, refine, explain, history)\n", opts.ModeFilter)
			return 2
		}
		if err := printLaneHistorySummary(opts.ProjectRoot, stdout, filter); err != nil {
			fmt.Fprintf(stderr, "lane: history: %v\n", err)
			return 1
		}
		return 0
	}
	if opts.Mode == "explain" {
		return runLaneExplain(opts, stdout, stderr)
	}
	if opts.Mode == "refine" {
		refineOpts := laneRefineOptions{
			SpecPath:    opts.RefineSpecPath,
			OutputPath:  opts.RefineOutputPath,
			Title:       opts.Title,
			MaxIters:    opts.RefineMaxIters,
			ProjectRoot: opts.ProjectRoot,
			Mode:        opts.RefineMode,
		}
		return runLaneRefine(refineOpts, defaultRefineDispatch, stdout, stderr)
	}
	handled, code, atomReport := runLaneAtomIntakePreflight(opts, stdout, stderr)
	if handled {
		return code
	}
	// Brief lint preflight: default-on. The wrapper that used to
	// run this in shell is now redundant — every project that uses
	// `idiom lane` benefits from catching unsafe briefs before
	// dispatch. --no-lint is the escape hatch for callers that
	// already lint upstream or want to test bypass.
	if !opts.NoLint {
		if code := laneBriefLintRunner([]string{opts.BriefPath}, stdout, stderr); code != 0 {
			fmt.Fprintf(stderr, "lane: brief lint failed (exit %d); use --no-lint to skip preflight\n", code)
			return code
		}
	}
	if err := checkBriefStaleness(opts.BriefPath, opts.ProjectRoot, opts.AllowStaleBrief, stderr); err != nil {
		fmt.Fprintf(stderr, "lane: %v\n", err)
		return 1
	}
	// Slot mutex: opt-in concurrency guard for orchestrators that
	// dispatch lanes in parallel. The KEY (what counts as "the same
	// lane") is project policy, so the caller chooses; idiom owns
	// only the primitive. Empty key = no mutex (today's behavior).
	if key := strings.TrimSpace(opts.SlotKey); key != "" {
		release, code := acquireLaneSlot(key, stderr)
		if code != 0 {
			return code
		}
		defer release()
	}
	contextPackSection := ""
	if deps.buildContextPack != nil && shouldAutoPreloadLaneContextPack(opts) {
		contextPackSection = deps.buildContextPack(opts, cfg, stderr)
	}
	laneStartFields := laneStartTelemetryFields(opts, cfg)
	logCLIPhase(deps.telemetry, "lane_start", laneStartFields)
	composedPath, err := composeLanePromptToFile(opts, contextPackSection)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	// composeLanePromptToFile creates a temp file under os.TempDir;
	// this defer is the lifecycle boundary. The subcommand reads the
	// path via --prompt-file before returning, so removing here runs
	// strictly after the read completes — no race, no leak.
	defer os.Remove(composedPath)

	if opts.JSONEvents {
		fields := map[string]any{
			"mode":  opts.Mode,
			"brief": opts.BriefPath,
			"title": opts.Title,
		}
		maps.Copy(fields, laneStartFields)
		emitLaneJSONEvent(stdout, "start", fields)
	}

	requiredEditPaths := laneForwardedRequiredEditPaths(opts, atomReport, stderr)
	forwardArgs := buildForwardedArgs(opts, composedPath, requiredEditPaths)
	// Tee stdout so the post-analysis can scan the JSON event stream
	// the subcommand emits. Without --json the subcommand still prints
	// human-readable output that contains the same markers, so the
	// analyzer works either way; --json just makes it machine-precise.
	tee := newTeeBuffer(stdout)
	var subcode int
	switch opts.Mode {
	case "plan-execute":
		subcode = deps.runPlanExecute(forwardArgs, tee, stderr, deps.defaultPromptRun)
	case "implement":
		subcode = deps.runImplement(forwardArgs, tee, stderr, deps.defaultPromptRun)
	default:
		// parseLaneOptions already rejects unknown modes; defensive
		// belt-and-suspenders.
		fmt.Fprintf(stderr, "lane: unknown mode %q\n", opts.Mode)
		return 2
	}
	degraded := analyzeLaneOutput(tee.Bytes(), subcode)
	if opts.Postflight {
		pfExit := runLanePostflightAfterLane(opts, stdout, stderr)
		if pfExit > subcode {
			subcode = pfExit
		}
	}
	briefMtime := ""
	if info, err := os.Stat(opts.BriefPath); err == nil {
		briefMtime = info.ModTime().UTC().Format(time.RFC3339Nano)
	}
	historyEvt := laneHistoryEvent{
		Mode:            opts.Mode,
		Brief:           opts.BriefPath,
		BriefMtime:      briefMtime,
		BriefProvenance: detectBriefProvenance(opts.BriefPath),
		Title:           opts.Title,
		ExitCode:        subcode,
		FinalState:      laneRunFinalState(subcode, degraded, tee.Bytes()),
		QueuePool:       parseLaneQueuePool(tee.Bytes()),
		Degradations:    laneDegradationKinds(degraded),
	}
	if summary, ok := parseSessionSummary(tee.Bytes()); ok {
		historyEvt.SessionID = summary.SessionID
		historyEvt.ModelCalls = summary.ModelCalls
		historyEvt.ToolCalls = summary.ToolCalls
		historyEvt.PromptTokens = summary.PromptTokens
		historyEvt.CompletionTokens = summary.CompletionTokens
		historyEvt.TotalTokens = summary.TotalTokens
		historyEvt.PeakPromptTokens = summary.PeakPromptTokens
		historyEvt.ContextSize = summary.ContextSize
		historyEvt.DurationMS = summary.DurationMS
		historyEvt.StopReason = summary.StopReason
	}
	if err := appendLaneHistory(opts.ProjectRoot, historyEvt); err != nil {
		fmt.Fprintf(stderr, "lane: history append failed: %v\n", err)
	}
	if opts.JSONEvents {
		emitLaneJSONEvent(stdout, "run_ledger", laneHistoryEventFields(historyEvt))
	}

	// Post-analyze: detect three regression classes and emit a
	// structured `idiom.lane.degraded` event for each. The event is
	// the seam — projects subscribe to it from telemetry / stdout
	// to drive their own audit ledger (e.g. Colony's
	// harness_feedback). Idiom doesn't reach into project policy.
	for _, d := range degraded {
		emitLaneJSONEvent(stdout, "degraded", map[string]any{
			"kind":    d.kind,
			"details": d.details,
			"mode":    opts.Mode,
		})
		fmt.Fprintf(stderr, "lane: degraded kind=%s details=%s\n", d.kind, d.details)
	}
	if subcode != 0 {
		return subcode
	}
	if len(degraded) > 0 {
		return 4
	}
	return 0
}

// isKnownLaneMode reports whether mode is one of the lane subcommand
// names. Used both for first-arg validation in parseLaneOptions and
// for --lane-mode filter validation in history.
func isKnownLaneMode(mode string) bool {
	switch strings.TrimSpace(mode) {
	case "plan-execute", "implement", "history", "refine", "explain":
		return true
	}
	return false
}

func runLaneAtomIntakePreflight(opts laneOptions, stdout io.Writer, stderr io.Writer) (bool, int, *atomIntakeReport) {
	body, err := os.ReadFile(opts.BriefPath)
	if err != nil {
		return false, 0, nil
	}
	if !laneBriefNeedsAtomIntake(string(body)) {
		return false, 0, nil
	}
	report := evaluateAtomIntake(string(body), opts.BriefPath, opts.ProjectRoot, opts.AllowDirtyAtomScope)
	if opts.JSONEvents {
		emitLaneJSONEvent(stdout, "atom_intake", laneAtomIntakeReportFields(report))
	}
	if report.Dispatchable {
		return false, 0, &report
	}
	historyEvt := laneHistoryEvent{
		Mode:            opts.Mode,
		Brief:           opts.BriefPath,
		BriefMtime:      laneBriefMtime(opts.BriefPath),
		BriefProvenance: detectBriefProvenance(opts.BriefPath),
		Title:           opts.Title,
		ExitCode:        1,
		FinalState:      string(report.State),
	}
	if err := appendLaneHistory(opts.ProjectRoot, historyEvt); err != nil {
		fmt.Fprintf(stderr, "lane: history append failed: %v\n", err)
	}
	if opts.JSONEvents {
		emitLaneJSONEvent(stdout, "run_ledger", laneHistoryEventFields(historyEvt))
	}
	fmt.Fprintln(stderr, "lane: atom intake blocked dispatch")
	printAtomIntakeReport(report, stderr)
	return true, 1, &report
}

func laneBriefMtime(path string) string {
	if info, err := os.Stat(path); err == nil {
		return info.ModTime().UTC().Format(time.RFC3339Nano)
	}
	return ""
}

func laneBriefNeedsAtomIntake(body string) bool {
	if _, ok := atomSpecStatus(body); ok {
		return true
	}
	if _, ok := atomRefineDraftReason(body); ok {
		return true
	}
	targets := extractDeliverableTargets(body, refineDetectMode(body))
	if len(targets) == 0 {
		return false
	}
	if atomSpecHasAcceptedResult(body) {
		return true
	}
	if len(atomRequiredEditSnippetsFromSpec(body, targets)) > 0 {
		return true
	}
	for _, heading := range []string{"required edits", "edits to apply", "implementation shape"} {
		if strings.TrimSpace(atomMarkdownSectionByHeading(body, heading)) != "" {
			return true
		}
	}
	return false
}

func laneAtomIntakeReportFields(report atomIntakeReport) map[string]any {
	body, err := json.Marshal(report)
	if err != nil {
		return map[string]any{}
	}
	var fields map[string]any
	if err := json.Unmarshal(body, &fields); err != nil {
		return map[string]any{}
	}
	delete(fields, "type")
	return fields
}

// parseLaneOptions parses lane-specific flags + the positional brief
// path. Mode is required as the first positional argument and must
// be one of {plan-execute, implement}.
func parseLaneOptions(args []string, stderr io.Writer) (laneOptions, error) {
	if len(args) == 0 {
		return laneOptions{}, fmt.Errorf("lane: usage: idiom lane <mode> [flags] <brief>")
	}
	mode := strings.TrimSpace(args[0])
	if !isKnownLaneMode(mode) {
		return laneOptions{}, fmt.Errorf("lane: --mode must be one of plan-execute, implement, history, refine, explain (got %q)", mode)
	}

	fs := flag.NewFlagSet("idiom lane", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var opts laneOptions
	opts.Mode = mode
	// Default to the canonical project layout (.idiom/config.json) so
	// `idiom lane <mode> <brief>` runs from a project root without an
	// explicit --config flag. Existing callers that pass --config or
	// rely on cwd-local config.json keep working — this only changes
	// what happens when the flag is omitted. The IDIOM_CONFIG env var
	// (used by Colony's run_idiom_impl.sh) overrides this default if
	// set, but --config still wins when explicitly passed.
	configDefault := ".idiom/config.json"
	if envConfig := strings.TrimSpace(os.Getenv("IDIOM_CONFIG")); envConfig != "" {
		configDefault = envConfig
	}
	fs.StringVar(&opts.ConfigPath, "config", configDefault, "path to config file (defaults to .idiom/config.json or $IDIOM_CONFIG)")
	fs.StringVar(&opts.Title, "title", "", "atom title (carried through to telemetry)")
	fs.StringVar(&opts.Model, "model", "", "override the model alias used for the lane")
	// --coder records intent; the actual alias is resolved later in
	// runLaneMainWithDeps from .idiom/backends.json. When no backends
	// file declares an executor role, opts.Model stays empty and the
	// subcommand uses the configured cfg.LlamaServer model.
	coderShortcut := fs.Bool("coder", false, "use the project's executor-role alias from .idiom/backends.json (legacy spelling: proxy.default_coder)")
	fs.StringVar(&opts.BackendProfile, "backend-profile", "", "select a named profile from .idiom/backends.json (omit to use the lone profile)")
	fs.BoolVar(&opts.JSONEvents, "json", false, "emit structured JSONL events to stdout")
	fs.StringVar(&opts.Resume, "resume", "", "resume a prior session by id")
	fs.BoolVar(&opts.PlannerOwnedVerification, "planner-owned-verification", true, "delegate build/test verification to the caller (default true for lane)")
	fs.BoolVar(&opts.AllowBuildTests, "allow-build-tests", false, "compatibility flag forwarded to the underlying subcommand")
	fs.BoolVar(&opts.DisableThinking, "no-thinking", false, "force request-level Qwen thinking off for the wrapped subprocess")
	fs.StringVar(&opts.ProjectRoot, "project-root", "", "project root used to resolve .idiom/ overlays (defaults to the brief's directory)")
	fs.BoolVar(&opts.NoLint, "no-lint", false, "skip the brief lint preflight (lint runs by default)")
	fs.BoolVar(&opts.NoContextPack, "no-context-pack", false, "skip automatic ranked context_pack prompt preload")
	fs.StringVar(&opts.SlotKey, "slot", "", "acquire a filesystem mutex idiom-lane-<key> to prevent concurrent dispatch of the same lane (caller picks the granularity)")
	fs.BoolVar(&opts.Postflight, "postflight", false, "run idiom lane postflight against git diff after the lane returns; final exit is max(lane, postflight)")
	fs.BoolVar(&opts.AllowStaleBrief, "allow-stale-brief", false, "override the deterministic stale-brief gate (default: fail if any required edit path has commits newer than brief mtime)")
	fs.BoolVar(&opts.AllowDirtyAtomScope, "allow-dirty", false, "allow atom dispatch when scoped existing paths have local modifications")
	fs.BoolVar(&opts.AllowDirtyAtomScope, "allow-dirty-scope", false, "alias for --allow-dirty")
	fs.StringVar(&opts.RefineSpecPath, "spec", "", "refine mode: path to 1-paragraph spec file")
	fs.StringVar(&opts.RefineOutputPath, "output", "", "refine mode: where to write the converged brief")
	fs.IntVar(&opts.RefineMaxIters, "max-iters", 3, "refine mode: max draft+review+patch iterations (default 3)")
	fs.StringVar(&opts.RefineMode, "mode", "", "refine mode: deliverable shape override (\"anchored\" | \"markdown\"); empty = auto-detect from spec")
	fs.StringVar(&opts.Since, "since", "", "history mode: show only events within the given duration (e.g. 1h, 24h)")
	fs.BoolVar(&opts.FailedOnly, "failed", false, "history mode: show only events with non-zero exit code")
	fs.StringVar(&opts.ModeFilter, "lane-mode", "", "history mode: show only events for this lane mode (plan-execute, implement, refine, explain, history)")
	fs.StringVar(&opts.ExplainSessionID, "session", "", "explain mode: specific session id to diagnose (default: most recent)")
	fs.StringVar(&opts.ExplainBriefFilter, "brief", "", "explain mode: filter to most-recent session for this brief path")
	fs.IntVar(&opts.MaxTokens, "max-tokens", 0, "override max_tokens for the wrapped subprocess; 0 uses backend default")

	if err := fs.Parse(args[1:]); err != nil {
		return laneOptions{}, err
	}
	// --coder loses to --model when both are set, matching the
	// Colony script's existing behavior. Record the intent here; the
	// alias resolves in runLaneMainWithDeps once backends.json loads.
	if *coderShortcut && strings.TrimSpace(opts.Model) == "" {
		opts.coderRequested = true
		opts.explicitCoderRequested = true
	}

	tail := fs.Args()
	if opts.Mode == "history" {
		// history is a metadata subcommand; brief is optional/ignored.
		if len(tail) > 0 {
			return laneOptions{}, fmt.Errorf("lane history: takes no positional arguments (got %v)", tail)
		}
	} else if opts.Mode == "explain" {
		// explain takes no positional brief — selection is via --session /
		// --brief flags or "the latest dispatch" when neither is set.
		if len(tail) > 0 {
			return laneOptions{}, fmt.Errorf("lane explain: takes no positional arguments (use --session or --brief); got %v", tail)
		}
	} else if opts.Mode == "refine" {
		// refine takes --spec and --output rather than a positional brief.
		if len(tail) > 0 {
			return laneOptions{}, fmt.Errorf("lane refine: takes no positional arguments (use --spec and --output); got %v", tail)
		}
	} else {
		if len(tail) == 0 {
			return laneOptions{}, fmt.Errorf("lane: brief path is required (positional argument after the flags)")
		}
		if len(tail) > 1 {
			return laneOptions{}, fmt.Errorf("lane: too many positional arguments (got %v)", tail)
		}
		opts.BriefPath = tail[0]
		if info, err := os.Stat(opts.BriefPath); err != nil {
			return laneOptions{}, fmt.Errorf("lane: brief not readable at %q: %w", opts.BriefPath, err)
		} else if info.IsDir() {
			return laneOptions{}, fmt.Errorf("lane: brief path %q is a directory", opts.BriefPath)
		}
	}
	// ProjectRoot stays empty here when --project-root isn't given;
	// runLaneMainWithDeps fills it from config.Workspace.Root after
	// the config loads. The brief's parent dir is the last-resort
	// fallback, applied only when both --project-root and config
	// workspace.root are empty.

	// Auto-route plan-execute to the project's executor-role alias
	// when no --model override is set. Record intent only; the alias
	// resolves in runLaneMainWithDeps once project root and
	// backends.json are loaded. When no executor role is declared,
	// opts.Model stays empty and the subcommand falls through to
	// cfg.LlamaServer — i.e. the lane runs on the single configured
	// model, which is the desired behavior for single-model projects.
	if opts.Mode == "plan-execute" && strings.TrimSpace(opts.Model) == "" {
		opts.coderRequested = true
	}

	return opts, nil
}

func runLanePostflightAfterLane(opts laneOptions, stdout io.Writer, stderr io.Writer) int {
	out, err := laneGitDiff(opts.ProjectRoot)
	if err != nil {
		fmt.Fprintf(stderr, "lane: postflight: git diff failed: %v\n", err)
		return 1
	}
	tmp, err := os.CreateTemp(os.TempDir(), "idiom-lane-postflight-*.diff")
	if err != nil {
		fmt.Fprintf(stderr, "lane: postflight: create temp file failed: %v\n", err)
		return 1
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(out); err != nil {
		fmt.Fprintf(stderr, "lane: postflight: write temp file failed: %v\n", err)
		return 1
	}
	if err := tmp.Close(); err != nil {
		fmt.Fprintf(stderr, "lane: postflight: close temp file failed: %v\n", err)
		return 1
	}
	return lanePostflightRunner([]string{"--diff", tmp.Name(), "--project-root", opts.ProjectRoot, "--brief", opts.BriefPath}, stdout, stderr)
}

func resolveLaneModelAlias(opts laneOptions, stderr io.Writer) string {
	if opts.Mode == "plan-execute" && !opts.explicitCoderRequested && strings.TrimSpace(opts.BackendProfile) != "" {
		if alias := resolveRoleAlias(opts.ProjectRoot, opts.BackendProfile, backends.RolePlanner, stderr); alias != "" {
			return alias
		}
	}
	return resolveRoleAlias(opts.ProjectRoot, opts.BackendProfile, backends.RoleExecutor, stderr)
}

// resolveRoleAlias reads .idiom/backends.json for the resolved
// project root and returns the alias declared for the requested role.
// Returns "" when the file is missing, the profile is not found, or
// the role isn't declared — the caller leaves --model unset and the
// underlying subcommand uses cfg.LlamaServer.Model. Malformed
// backends.json surfaces to stderr but still returns "" so the lane
// degrades gracefully to single-model behavior instead of crashing.
func resolveRoleAlias(projectRoot, profileName, role string, stderr io.Writer) string {
	file, ok, err := backends.LoadFromProjectRoot(projectRoot)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return ""
	}
	if !ok {
		return ""
	}
	prof, found := file.Profile(profileName)
	if !found {
		// A backends.json that exists but doesn't have the named
		// profile is a project misconfiguration; surface it to stderr
		// without erroring out the lane.
		if profileName != "" {
			fmt.Fprintf(stderr, "lane: backend profile %q not found in .idiom/backends.json\n", profileName)
		}
		return ""
	}
	alias, _ := prof.AliasForRole(role)
	return alias
}

// acquireLaneSlot is the opt-in concurrency mutex Phase 6.5 ports
// from Colony's run_idiom_impl.sh. The key chooses the granularity
// (the wrapper used per-model lane keys: "35b", "coder"); idiom
// owns only the mutex primitive. mkdir is atomic on POSIX-shaped
// filesystems, which is enough to detect a held slot. Returns a
// release callback the caller defers, plus the same exit code
// shape the rest of the lane wrapper uses (0 acquired, 3 held).
func acquireLaneSlot(key string, stderr io.Writer) (release func(), code int) {
	dir := filepath.Join(os.TempDir(), "idiom-lane-"+sanitizeSlotKey(key))
	if err := os.Mkdir(dir, 0o755); err != nil {
		if os.IsExist(err) {
			fmt.Fprintf(stderr, "lane: slot %q already held at %s — another dispatch is in flight\n", key, dir)
			return func() {}, 3
		}
		fmt.Fprintf(stderr, "lane: cannot acquire slot %q at %s: %v\n", key, dir, err)
		return func() {}, 1
	}
	return func() { _ = os.Remove(dir) }, 0
}

// sanitizeSlotKey strips characters that would corrupt the slot
// directory name. Mirrors the launcher's sanitizeAlias spirit but
// kept local since we only need the alnum/dash/dot subset for a
// short-lived temp dir name.
func sanitizeSlotKey(key string) string {
	var b strings.Builder
	for _, r := range key {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '.', r == '_':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + ('a' - 'A'))
		default:
			b.WriteRune('_')
		}
	}
	if b.Len() == 0 {
		return "default"
	}
	return b.String()
}

func shouldAutoPreloadLaneContextPack(opts laneOptions) bool {
	if opts.NoContextPack {
		return false
	}
	if isRefineScratchpadMetaBrief(opts.BriefPath) {
		return false
	}
	switch strings.TrimSpace(opts.Mode) {
	case "implement", "plan-execute":
		return strings.TrimSpace(opts.BriefPath) != ""
	default:
		return false
	}
}

func isRefineScratchpadMetaBrief(briefPath string) bool {
	p := filepath.ToSlash(filepath.Clean(strings.TrimSpace(briefPath)))
	if p == "" || p == "." {
		return false
	}
	if !strings.HasPrefix(p, ".idiom/refine/") && !strings.Contains(p, "/.idiom/refine/") {
		return false
	}
	name := p
	if slash := strings.LastIndex(name, "/"); slash >= 0 {
		name = name[slash+1:]
	}
	return strings.HasPrefix(name, "meta-") && strings.HasSuffix(name, ".md")
}

type laneContextPackView struct {
	PackID           string                    `json:"pack_id"`
	ArtifactPath     string                    `json:"artifact_path"`
	Fresh            bool                      `json:"fresh"`
	CandidateFiles   []laneContextPackFile     `json:"candidate_files"`
	CandidateSymbols []laneContextPackSymbol   `json:"candidate_symbols"`
	Tests            []laneContextPackTest     `json:"tests"`
	UnresolvedGaps   []string                  `json:"unresolved_gaps"`
	Freshness        *laneContextPackFreshness `json:"freshness"`
}

type laneContextPackFreshness struct {
	Policy        string `json:"policy"`
	Operation     string `json:"operation"`
	FreshForScope bool   `json:"fresh_for_scope"`
	GlobalFresh   bool   `json:"global_fresh"`
	Reason        string `json:"reason,omitempty"`
}

type laneContextPackFile struct {
	Path       string  `json:"path"`
	Role       string  `json:"role"`
	Reason     string  `json:"reason"`
	Language   string  `json:"language"`
	Confidence string  `json:"confidence"`
	Score      float64 `json:"score"`
	Hub        bool    `json:"hub"`
}

type laneContextPackSymbol struct {
	ID            string `json:"id"`
	Path          string `json:"path"`
	QualifiedName string `json:"qualified_name"`
	Kind          string `json:"kind"`
	StartLine     int    `json:"start_line"`
	EndLine       int    `json:"end_line"`
	Confidence    string `json:"confidence"`
	Reason        string `json:"reason"`
}

type laneContextPackTest struct {
	Command string `json:"command"`
	Path    string `json:"path"`
	Symbol  string `json:"symbol"`
	Reason  string `json:"reason"`
}

func buildLaneContextPackSection(opts laneOptions, cfg config.Config, stderr io.Writer) string {
	briefBody, err := os.ReadFile(opts.BriefPath)
	if err != nil {
		fmt.Fprintf(stderr, "lane: context_pack skipped: read brief: %v\n", err)
		return ""
	}
	if briefAlreadyHasContextPack(briefBody) {
		return ""
	}
	wsCfg := cfg.Workspace
	wsCfg.Root = firstNonEmptyString(strings.TrimSpace(opts.ProjectRoot), strings.TrimSpace(wsCfg.Root), filepath.Dir(opts.BriefPath))
	if strings.TrimSpace(wsCfg.CWD) == "" {
		wsCfg.CWD = wsCfg.Root
	}
	wsCfg.EnableWrites = false
	mgr := workspace.NewManager(wsCfg)

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	result := mgr.Execute(ctx, workspace.ToolRequest{
		Tool:              "context_pack",
		Task:              laneContextPackTask(opts, string(briefBody)),
		Mode:              laneContextPackMode(opts.Mode),
		Limit:             8,
		BudgetTokens:      1200,
		RequiredEditPaths: app.RequiredEditPathsFromPrompt(string(briefBody)),
	})
	if strings.TrimSpace(result.Error) != "" {
		fmt.Fprintf(stderr, "lane: context_pack skipped: %s\n", result.Error)
		return ""
	}
	section, err := renderLaneContextPackSection(result)
	if err != nil {
		fmt.Fprintf(stderr, "lane: context_pack skipped: %v\n", err)
		return ""
	}
	if strings.TrimSpace(section) != "" {
		fmt.Fprintf(stderr, "lane: context_pack preloaded (%s)\n", result.Summary)
	}
	return section
}

func briefAlreadyHasContextPack(body []byte) bool {
	lower := strings.ToLower(string(body))
	return strings.Contains(lower, "\n## context pack") ||
		strings.Contains(lower, "\n## context packet") ||
		strings.Contains(lower, "\n# context pack") ||
		strings.Contains(lower, "\n# context packet") ||
		strings.Contains(lower, "\ncontext pack:") ||
		strings.Contains(lower, "\ncontext packet:") ||
		strings.HasPrefix(strings.TrimSpace(lower), "## context pack") ||
		strings.HasPrefix(strings.TrimSpace(lower), "# context pack") ||
		strings.HasPrefix(strings.TrimSpace(lower), "context pack:") ||
		strings.HasPrefix(strings.TrimSpace(lower), "context packet:")
}

func laneContextPackMode(mode string) string {
	switch strings.TrimSpace(mode) {
	case "plan-execute":
		return "plan"
	case "implement":
		return "implement"
	default:
		return "plan"
	}
}

func laneContextPackTask(opts laneOptions, briefBody string) string {
	title := strings.TrimSpace(opts.Title)
	if title == "" {
		title = firstMarkdownHeading(briefBody)
	}
	if title == "" {
		title = strings.TrimSuffix(filepath.Base(opts.BriefPath), filepath.Ext(opts.BriefPath))
	}
	body := normalizeLaneContextPackTaskBody(briefBody)
	if len(body) > 8000 {
		body = body[:8000]
	}
	return strings.TrimSpace(strings.Join([]string{
		"lane: " + strings.TrimSpace(opts.Mode),
		"title: " + title,
		"brief:",
		body,
	}, "\n"))
}

func normalizeLaneContextPackTaskBody(body string) string {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		line = regexpRefineScratchpadPath.ReplaceAllString(line, ".idiom/refine/<title>/iter-N/<artifact>")
		lines[i] = strings.TrimRight(line, " \t")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

var regexpRefineScratchpadPath = regexp.MustCompile(`\.idiom/refine/[^` + "`" + `\s)]+/iter-\d+/[^` + "`" + `\s)]+`)
var regexpContextViewArtifactPath = regexp.MustCompile(`\.idiom/comprehension/views/[A-Za-z0-9._-]+\.json`)

func firstMarkdownHeading(body string) string {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "# ") {
			continue
		}
		return strings.TrimSpace(strings.TrimPrefix(line, "# "))
	}
	return ""
}

func renderLaneContextPackSection(result workspace.ToolResult) (string, error) {
	var view laneContextPackView
	if err := json.Unmarshal([]byte(result.Output), &view); err != nil {
		return "", fmt.Errorf("decode context_pack output: %w", err)
	}
	var b strings.Builder
	b.WriteString("## Context Pack\n\n")
	b.WriteString("Source: idiom `context_pack` automatic preload. Use this ranked packet before broad reads; treat it as cached context and verify exact anchors before source writes.\n\n")
	summary := regexpContextViewArtifactPath.ReplaceAllString(strings.TrimSpace(result.Summary), "context view artifact")
	fmt.Fprintf(&b, "- Summary: %s\n", summary)
	if view.PackID != "" {
		fmt.Fprintf(&b, "- Pack: `%s`\n", view.PackID)
	}
	if view.Freshness != nil {
		fmt.Fprintf(&b, "- Freshness: policy=%s fresh_for_scope=%t global_fresh=%t", view.Freshness.Policy, view.Freshness.FreshForScope, view.Freshness.GlobalFresh)
		if view.Freshness.Reason != "" {
			fmt.Fprintf(&b, " reason=%s", view.Freshness.Reason)
		}
		b.WriteByte('\n')
	} else {
		fmt.Fprintf(&b, "- Fresh: %t\n", view.Fresh)
	}
	writeLaneContextFiles(&b, view.CandidateFiles, 8)
	writeLaneContextSymbols(&b, view.CandidateSymbols, 8)
	writeLaneContextTests(&b, view.Tests, 4)
	writeLaneContextGaps(&b, view.UnresolvedGaps, 4)
	return strings.TrimRight(b.String(), "\n"), nil
}

func writeLaneContextFiles(b *strings.Builder, files []laneContextPackFile, limit int) {
	if len(files) == 0 {
		return
	}
	b.WriteString("\n### Candidate Files\n\n")
	for i, file := range files {
		if i >= limit {
			break
		}
		role := firstNonEmptyString(file.Role, "candidate")
		score := ""
		if file.Score > 0 {
			score = fmt.Sprintf(", score=%.2f", file.Score)
		}
		hub := ""
		if file.Hub {
			hub = ", hub"
		}
		fmt.Fprintf(b, "- `%s` (%s%s%s): %s\n", file.Path, role, score, hub, firstNonEmptyString(file.Reason, file.Confidence, file.Language))
	}
}

func writeLaneContextSymbols(b *strings.Builder, symbols []laneContextPackSymbol, limit int) {
	if len(symbols) == 0 {
		return
	}
	b.WriteString("\n### Candidate Symbols\n\n")
	for i, sym := range symbols {
		if i >= limit {
			break
		}
		name := firstNonEmptyString(sym.QualifiedName, sym.ID)
		loc := sym.Path
		if sym.StartLine > 0 {
			loc = fmt.Sprintf("%s:%d", loc, sym.StartLine)
		}
		fmt.Fprintf(b, "- `%s` in `%s` (%s): %s\n", name, loc, firstNonEmptyString(sym.Kind, "symbol"), firstNonEmptyString(sym.Reason, sym.Confidence))
	}
}

func writeLaneContextTests(b *strings.Builder, tests []laneContextPackTest, limit int) {
	if len(tests) == 0 {
		return
	}
	b.WriteString("\n### Candidate Tests\n\n")
	for i, test := range tests {
		if i >= limit {
			break
		}
		target := firstNonEmptyString(test.Symbol, test.Path)
		if target != "" {
			target = " — " + target
		}
		fmt.Fprintf(b, "- `%s`%s\n", test.Command, target)
	}
}

func writeLaneContextGaps(b *strings.Builder, gaps []string, limit int) {
	if len(gaps) == 0 {
		return
	}
	b.WriteString("\n### Context Gaps\n\n")
	for i, gap := range gaps {
		if i >= limit {
			break
		}
		fmt.Fprintf(b, "- %s\n", gap)
	}
}

// composeLanePromptToFile builds the composed prompt and writes it to
// a temp file the underlying subcommand reads via --prompt-file. The
// caller MUST defer os.Remove on the returned path — see
// runLaneMainWithDeps where that defer is wired. Error paths inside
// this function clean up after themselves before returning.
//
// Layer order (top-to-bottom in the composed prompt):
//  1. canonical generic rules  (cmd/idiom/lanes/rules/*.md, embed; project
//     files at .idiom/prompts/rules/<name>.md
//     replace by name; non-canonical project
//     files append alphabetically as extras)
//  2. idiom canonical preamble (cmd/idiom/lanes/<mode>/preamble.md, embed)
//  3. project preamble overlay (<root>/.idiom/prompts/lanes/<mode>/preamble.md)
//  4. brief body               (the file at opts.BriefPath)
//  5. project postamble overlay (<root>/.idiom/prompts/lanes/<mode>/postamble.md)
//  6. idiom canonical postamble (cmd/idiom/lanes/<mode>/postamble.md, embed)
//
// Preamble/postamble project layers are additive — they extend, never
// replace, idiom's canonical text. For rules, project files matching a
// canonical name replace the canonical entry in place; project files
// with names NOT in the canonical set append after the canonical block.
// Empty/missing files at any layer are skipped.
func composeLanePromptToFile(opts laneOptions, contextPackSection string) (string, error) {
	briefBody, err := os.ReadFile(opts.BriefPath)
	if err != nil {
		return "", fmt.Errorf("lane: read brief: %w", err)
	}

	parts := []string{}
	if rules := canonicalLaneRules(opts.ProjectRoot); strings.TrimSpace(rules) != "" {
		parts = append(parts, rules)
	}
	if pre := canonicalLanePreamble(opts.Mode); strings.TrimSpace(pre) != "" {
		parts = append(parts, pre)
	}
	if pre := readProjectLaneOverlay(opts.ProjectRoot, opts.Mode, "preamble.md"); strings.TrimSpace(pre) != "" {
		parts = append(parts, pre)
	}
	if strings.TrimSpace(contextPackSection) != "" {
		parts = append(parts, contextPackSection)
	}
	// Wrap the brief body in explicit boundary markers so downstream
	// extractors (path detection, discovery handles) can scope to the
	// brief and skip prose tokens in the surrounding preamble/postamble
	// (which legitimately contain backtick-quoted instructional text
	// like `start_line`/`end_line` that used to leak into the
	// required_edit_paths surface). The markers are HTML comments so
	// they're invisible in the rendered prompt; pathparse.BetweenBriefBodyMarkers
	// is the matching extractor-side helper.
	parts = append(parts, briefBodyStartMarker+"\n"+string(briefBody)+"\n"+briefBodyEndMarker)
	if post := readProjectLaneOverlay(opts.ProjectRoot, opts.Mode, "postamble.md"); strings.TrimSpace(post) != "" {
		parts = append(parts, post)
	}
	if post := canonicalLanePostamble(opts.Mode); strings.TrimSpace(post) != "" {
		parts = append(parts, post)
	}
	composed := strings.Join(parts, "\n\n")

	f, err := os.CreateTemp("", "idiom-lane-*.md")
	if err != nil {
		return "", fmt.Errorf("lane: create composed prompt file: %w", err)
	}
	defer f.Close()
	if _, err := f.WriteString(composed); err != nil {
		os.Remove(f.Name())
		return "", fmt.Errorf("lane: write composed prompt: %w", err)
	}
	return f.Name(), nil
}

// canonicalLanePreamble returns idiom's mode-specific preamble. The
// canonical text is embedded at build time from
// cmd/idiom/lanes/<mode>/preamble.md so the binary is self-contained
// and a project consuming idiom needs no per-project preamble copy.
// Unknown modes (or modes without an embedded preamble) return "".
func canonicalLanePreamble(mode string) string {
	return readEmbeddedLanePrompt(mode, "preamble.md")
}

// canonicalLanePostamble mirrors canonicalLanePreamble for the
// trailing text appended after the brief and any project overlay.
func canonicalLanePostamble(mode string) string {
	return readEmbeddedLanePrompt(mode, "postamble.md")
}

// readEmbeddedLanePrompt returns the canonical prompt fragment for a
// (mode, filename) pair from the embedded FS. Returns "" if the mode
// has no embedded fragment of that kind. Errors other than not-found
// fall through to "" so a build/embed mismatch at most produces an
// empty preamble rather than crashing the lane dispatch — the brief
// body itself remains the load-bearing instruction.
func readEmbeddedLanePrompt(mode, filename string) string {
	if strings.TrimSpace(mode) == "" {
		return ""
	}
	body, err := canonicalLanePromptsFS.ReadFile("lanes/" + mode + "/" + filename)
	if err != nil {
		return ""
	}
	return string(body)
}

// readProjectLaneOverlay reads an optional overlay file from the
// project's .idiom/prompts/lanes/<mode>/<filename>. Returns "" if
// the file doesn't exist or fails to read; project authors get
// graceful fallback to "no overlay."
func readProjectLaneOverlay(projectRoot, mode, filename string) string {
	if strings.TrimSpace(projectRoot) == "" {
		return ""
	}
	path := filepath.Join(projectRoot, ".idiom", "prompts", "lanes", mode, filename)
	body, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(body)
}

// canonicalLaneRules returns the rules block prepended to every lane's
// composed prompt: idiom-canonical generic rules in the order declared
// by canonicalRuleNames, with project-side replacements at
// .idiom/prompts/rules/<name>.md applied in place. Project rule files
// whose names are NOT in canonicalRuleNames are appended after the
// canonical block in alphabetical order ("extras"), so a project can
// add project-specific rules without editing the canonical set.
//
// Returns "" when neither canonical rules nor project rules contribute
// any text — keeps the composed prompt compact for projects that
// haven't authored a rules layer.
func canonicalLaneRules(projectRoot string) string {
	canonicalSet := make(map[string]struct{}, len(canonicalRuleNames))
	var blocks []string
	for _, name := range canonicalRuleNames {
		canonicalSet[name] = struct{}{}
		if override := readProjectRulesFile(projectRoot, name); strings.TrimSpace(override) != "" {
			blocks = append(blocks, override)
			continue
		}
		if body := readEmbeddedRule(name); strings.TrimSpace(body) != "" {
			blocks = append(blocks, body)
		}
	}
	blocks = append(blocks, projectRulesExtras(projectRoot, canonicalSet)...)
	return strings.Join(blocks, "\n\n")
}

// readEmbeddedRule loads a canonical rule by basename from the
// embedded FS. Returns "" on miss so a stray reference to an unembedded
// name in canonicalRuleNames cannot crash the lane — at worst the rule
// is silently skipped.
func readEmbeddedRule(name string) string {
	body, err := canonicalLanePromptsFS.ReadFile("lanes/rules/" + name)
	if err != nil {
		return ""
	}
	return string(body)
}

// readProjectRulesFile reads a single rule file from the project's
// overlay directory. Returns "" when the file doesn't exist or fails
// to read.
func readProjectRulesFile(projectRoot, name string) string {
	if strings.TrimSpace(projectRoot) == "" {
		return ""
	}
	body, err := os.ReadFile(filepath.Join(projectRoot, ".idiom", "prompts", "rules", name))
	if err != nil {
		return ""
	}
	return string(body)
}

// projectRulesExtras returns the bodies of project rule files whose
// names are NOT in the canonical set, sorted alphabetically by name
// for deterministic prompt composition. Returns nil when the project
// has no overlay directory or only canonical replacements.
func projectRulesExtras(projectRoot string, canonical map[string]struct{}) []string {
	if strings.TrimSpace(projectRoot) == "" {
		return nil
	}
	dir := filepath.Join(projectRoot, ".idiom", "prompts", "rules")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".md") {
			continue
		}
		if _, isCanonical := canonical[name]; isCanonical {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	var extras []string
	for _, name := range names {
		body, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || strings.TrimSpace(string(body)) == "" {
			continue
		}
		extras = append(extras, string(body))
	}
	return extras
}

func laneForwardedRequiredEditPaths(opts laneOptions, atomReport *atomIntakeReport, stderr io.Writer) []string {
	if opts.Mode != "plan-execute" {
		return nil
	}
	var paths []string
	body, err := os.ReadFile(opts.BriefPath)
	if err == nil {
		paths = append(paths, app.RequiredEditPathsFromPrompt(string(body))...)
	} else if stderr != nil {
		fmt.Fprintf(stderr, "lane: required_edit_paths forwarding skipped: read brief: %v\n", err)
	}
	if atomReport != nil && atomReport.Dispatchable {
		paths = append(paths, atomReport.ScopePaths...)
	}
	return planExecuteUniqueRequiredPaths(paths)
}

// buildForwardedArgs constructs the args the lane wrapper hands to
// the underlying idiom subcommand. The composed prompt is passed via
// --prompt-file; required edit paths and other relevant flags (model,
// json, resume, planner-owned-verification, allow-build-tests) are
// forwarded if set.
func buildForwardedArgs(opts laneOptions, composedPath string, requiredEditPaths []string) []string {
	out := []string{
		"--config", opts.ConfigPath,
		"--prompt-file", composedPath,
	}
	if opts.Mode == "plan-execute" {
		for _, path := range planExecuteUniqueRequiredPaths(requiredEditPaths) {
			out = append(out, "--required-edit-path", path)
		}
	}
	if model := strings.TrimSpace(opts.Model); model != "" {
		out = append(out, "--model", model)
	}
	if opts.JSONEvents {
		out = append(out, "--json")
	}
	if resume := strings.TrimSpace(opts.Resume); resume != "" {
		out = append(out, "--resume", resume)
	}
	if opts.PlannerOwnedVerification {
		out = append(out, "--planner-owned-verification")
	}
	if opts.AllowBuildTests {
		out = append(out, "--allow-build-tests")
	}
	if opts.DisableThinking {
		out = append(out, "--no-thinking")
	}
	if opts.MaxTokens > 0 {
		out = append(out, "--max-tokens", strconv.Itoa(opts.MaxTokens))
	}
	return out
}

// emitLaneJSONEvent writes one JSONL line to stdout for downstream
// telemetry consumers. Mirrors emitPlanExecuteJSONEvent in
// plan_execute.go but namespaces the event type as idiom.lane.* so
// consumers can correlate the lane wrapper's events with the
// orchestrator's idiom.plan_execute.* events.
func emitLaneJSONEvent(stdout io.Writer, eventType string, fields map[string]any) {
	payload := map[string]any{
		"type": "idiom.lane." + eventType,
		"ts":   time.Now().UTC().Format(time.RFC3339Nano),
	}
	maps.Copy(payload, fields)
	body, err := json.Marshal(payload)
	if err != nil {
		return
	}
	fmt.Fprintln(stdout, string(body))
}
