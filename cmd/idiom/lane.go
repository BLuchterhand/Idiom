package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"idiom/internal/config"
	"idiom/internal/telemetry"
)

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
}

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
	JSONEvents               bool
	Resume                   string
	PlannerOwnedVerification bool
	AllowBuildTests          bool
	ProjectRoot              string // overridable for tests; defaults to filepath.Dir(BriefPath)
}

// runLaneMain is the production entry point for `idiom lane <mode>`.
// It loads config from disk and forwards to runLaneMainWithTelemetry.
func runLaneMain(args []string, stdout io.Writer, stderr io.Writer) int {
	return runLaneMainWithDeps(args, stdout, stderr, laneDeps{
		loadConfig:       config.Load,
		runPlanExecute:   runPlanExecuteMain,
		runImplement:     runImplementMain,
		defaultPromptRun: runPromptOnce,
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
	}
	return runLaneMainWithDeps(args, stdout, stderr, deps)
}

// runLaneMainWithDeps is the testable core. It parses args, composes
// the prompt with project overlays, writes the composed prompt to a
// temp file, and forwards to the underlying lane subcommand.
func runLaneMainWithDeps(args []string, stdout io.Writer, stderr io.Writer, deps laneDeps) int {
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
	composedPath, err := composeLanePromptToFile(opts)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer os.Remove(composedPath)

	if opts.JSONEvents {
		emitLaneJSONEvent(stdout, "start", map[string]any{
			"mode":  opts.Mode,
			"brief": opts.BriefPath,
			"title": opts.Title,
		})
	}

	forwardArgs := buildForwardedArgs(opts, composedPath)
	switch opts.Mode {
	case "plan-execute":
		return deps.runPlanExecute(forwardArgs, stdout, stderr, deps.defaultPromptRun)
	case "implement":
		return deps.runImplement(forwardArgs, stdout, stderr, deps.defaultPromptRun)
	default:
		// parseLaneOptions already rejects unknown modes; defensive
		// belt-and-suspenders.
		fmt.Fprintf(stderr, "lane: unknown mode %q\n", opts.Mode)
		return 2
	}
}

// parseLaneOptions parses lane-specific flags + the positional brief
// path. Mode is required as the first positional argument and must
// be one of {plan-execute, implement}.
func parseLaneOptions(args []string, stderr io.Writer) (laneOptions, error) {
	if len(args) == 0 {
		return laneOptions{}, fmt.Errorf("lane: usage: idiom lane <mode> [flags] <brief>")
	}
	mode := strings.TrimSpace(args[0])
	switch mode {
	case "plan-execute", "implement":
		// ok
	default:
		return laneOptions{}, fmt.Errorf("lane: --mode must be one of plan-execute, implement (got %q)", mode)
	}

	fs := flag.NewFlagSet("idiom lane", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var opts laneOptions
	opts.Mode = mode
	// Default to the canonical project layout (.idiom/config.json) so
	// `idiom lane <mode> <brief>` runs from a project root without an
	// explicit --config flag. Existing callers that pass --config or
	// rely on cwd-local config.json keep working — this only changes
	// what happens when the flag is omitted.
	fs.StringVar(&opts.ConfigPath, "config", ".idiom/config.json", "path to config file (defaults to .idiom/config.json — the canonical project layout)")
	fs.StringVar(&opts.Title, "title", "", "atom title (carried through to telemetry)")
	fs.StringVar(&opts.Model, "model", "", "override the model alias used for the lane")
	// TODO(phase-4): once .idiom/backends.json lands, the coder
	// shortcut and the plan-execute auto-route should both pull the
	// alias from a `default_coder` field in the loaded backends
	// config rather than hardcoding "qwen3-coder-30b". Until then the
	// hardcoded name matches what Colony's run_idiom_impl.sh uses.
	coderShortcut := fs.Bool("coder", false, "shortcut for --model qwen3-coder-30b")
	fs.BoolVar(&opts.JSONEvents, "json", false, "emit structured JSONL events to stdout")
	fs.StringVar(&opts.Resume, "resume", "", "resume a prior session by id")
	fs.BoolVar(&opts.PlannerOwnedVerification, "planner-owned-verification", true, "delegate build/test verification to the caller (default true for lane)")
	fs.BoolVar(&opts.AllowBuildTests, "allow-build-tests", false, "compatibility flag forwarded to the underlying subcommand")
	fs.StringVar(&opts.ProjectRoot, "project-root", "", "project root used to resolve .idiom/ overlays (defaults to the brief's directory)")

	if err := fs.Parse(args[1:]); err != nil {
		return laneOptions{}, err
	}
	if *coderShortcut {
		// --coder loses to --model when both are set, matching the
		// Colony script's existing behavior.
		if strings.TrimSpace(opts.Model) == "" {
			opts.Model = "qwen3-coder-30b"
		}
	}

	tail := fs.Args()
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
	// ProjectRoot stays empty here when --project-root isn't given;
	// runLaneMainWithDeps fills it from config.Workspace.Root after
	// the config loads. The brief's parent dir is the last-resort
	// fallback, applied only when both --project-root and config
	// workspace.root are empty.

	// Auto-route plan-execute to the configured coder when no model
	// override is set. Implement mode keeps its 35B reasoning default.
	// TODO(phase-4): replace this hardcoded alias with a value pulled
	// from .idiom/backends.json once that lands.
	if opts.Mode == "plan-execute" && strings.TrimSpace(opts.Model) == "" {
		opts.Model = "qwen3-coder-30b"
	}

	return opts, nil
}

// composeLanePromptToFile builds the composed prompt (preamble +
// project overlay + brief body + project postamble + postamble) and
// writes it to a temp file the underlying subcommand reads via
// --prompt-file. Returns the temp file path; caller is responsible
// for cleanup.
//
// The seam:
//   - Idiom-side preamble/postamble are starter content; Phase 3 of
//     the reusability migration moves canonical text into go:embed
//     under prompts/lanes/<mode>/.
//   - Project overlays at <project>/.idiom/prompts/lanes/<mode>/
//     {preamble,postamble}.md are inserted between idiom's text and
//     the brief body. Concatenation merge — the project layer
//     extends, never replaces, idiom's canonical text.
func composeLanePromptToFile(opts laneOptions) (string, error) {
	briefBody, err := os.ReadFile(opts.BriefPath)
	if err != nil {
		return "", fmt.Errorf("lane: read brief: %w", err)
	}

	parts := []string{}
	if pre := canonicalLanePreamble(opts.Mode); strings.TrimSpace(pre) != "" {
		parts = append(parts, pre)
	}
	if pre := readProjectLaneOverlay(opts.ProjectRoot, opts.Mode, "preamble.md"); strings.TrimSpace(pre) != "" {
		parts = append(parts, pre)
	}
	parts = append(parts, string(briefBody))
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

// canonicalLanePreamble returns idiom's mode-specific preamble. In
// Phase 2 these are minimal placeholders that defer almost
// everything to the brief itself; Phase 3 replaces them with the
// canonical text currently embedded in run_idiom_impl.sh.
func canonicalLanePreamble(mode string) string {
	switch mode {
	case "plan-execute":
		// Plan-execute already prepends its own system addendum at
		// the runner level (see cmd/idiom/plan_execute.go's
		// planExecutePlannerSystemAddendum). The lane preamble adds
		// nothing to avoid double-wrapping; project overlays via
		// .idiom/prompts/lanes/plan-execute/preamble.md still apply.
		return ""
	case "implement":
		// Phase 3 moves the implement-lane preamble from
		// Colony/scripts/run_idiom_impl.sh into idiom; for now the
		// lane wrapper relies on idiom implement's existing prompt
		// shape and the project overlay (if any).
		return ""
	default:
		return ""
	}
}

// canonicalLanePostamble mirrors canonicalLanePreamble for the
// trailing text. Same Phase 3 caveat.
func canonicalLanePostamble(mode string) string {
	return ""
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

// buildForwardedArgs constructs the args the lane wrapper hands to
// the underlying idiom subcommand. The composed prompt is passed via
// --prompt-file; other relevant flags (model, json, resume, planner-
// owned-verification, allow-build-tests) are forwarded if set.
func buildForwardedArgs(opts laneOptions, composedPath string) []string {
	out := []string{
		"--config", opts.ConfigPath,
		"--prompt-file", composedPath,
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
	for k, v := range fields {
		payload[k] = v
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return
	}
	fmt.Fprintln(stdout, string(body))
}
