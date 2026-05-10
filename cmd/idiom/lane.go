package main

import (
	"embed"
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

	"idiom/internal/backends"
	"idiom/internal/config"
	"idiom/internal/telemetry"
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
	BackendProfile           string // names a profile in .idiom/backends.json
	JSONEvents               bool
	Resume                   string
	PlannerOwnedVerification bool
	AllowBuildTests          bool
	ProjectRoot              string // overridable for tests; defaults to filepath.Dir(BriefPath)
	NoLint                   bool   // skip the brief lint preflight (default: lint runs)
	SlotKey                  string // if set, acquire a filesystem mutex named idiom-lane-<key>
	// coderRequested is set when the caller asked for the coder
	// alias either explicitly (`--coder`) or implicitly (plan-execute
	// with no --model override). The actual alias is resolved later
	// in runLaneMainWithDeps once backends.json has loaded so projects
	// can override the default without changing every invocation.
	coderRequested bool
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
	// Resolve the executor-role alias now that project root is known.
	// This covers both --coder and the plan-execute auto-route; an
	// explicit --model already short-circuited coderRequested in
	// parseLaneOptions, so this doesn't fight a deliberate choice.
	// When no backends.json declares an executor, opts.Model stays
	// empty and the subcommand uses cfg.LlamaServer.Model — i.e. the
	// project's single configured model handles the lane.
	if opts.coderRequested && strings.TrimSpace(opts.Model) == "" {
		opts.Model = resolveRoleAlias(opts.ProjectRoot, opts.BackendProfile, backends.RoleExecutor, stderr)
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
	composedPath, err := composeLanePromptToFile(opts)
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
		emitLaneJSONEvent(stdout, "start", map[string]any{
			"mode":  opts.Mode,
			"brief": opts.BriefPath,
			"title": opts.Title,
		})
	}

	forwardArgs := buildForwardedArgs(opts, composedPath)
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

	// Post-analyze: detect three regression classes and emit a
	// structured `idiom.lane.degraded` event for each. The event is
	// the seam — projects subscribe to it from telemetry / stdout
	// to drive their own audit ledger (e.g. Colony's
	// harness_feedback). Idiom doesn't reach into project policy.
	degraded := analyzeLaneOutput(tee.Bytes(), subcode)
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
	fs.StringVar(&opts.ProjectRoot, "project-root", "", "project root used to resolve .idiom/ overlays (defaults to the brief's directory)")
	fs.BoolVar(&opts.NoLint, "no-lint", false, "skip the brief lint preflight (lint runs by default)")
	fs.StringVar(&opts.SlotKey, "slot", "", "acquire a filesystem mutex idiom-lane-<key> to prevent concurrent dispatch of the same lane (caller picks the granularity)")

	if err := fs.Parse(args[1:]); err != nil {
		return laneOptions{}, err
	}
	// --coder loses to --model when both are set, matching the
	// Colony script's existing behavior. Record the intent here; the
	// alias resolves in runLaneMainWithDeps once backends.json loads.
	if *coderShortcut && strings.TrimSpace(opts.Model) == "" {
		opts.coderRequested = true
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

// resolveRoleAlias reads .idiom/backends.json for the resolved
// project root and returns the alias declared for the requested role
// (executor today; planner / classifier reserved for future use).
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
func composeLanePromptToFile(opts laneOptions) (string, error) {
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
	maps.Copy(payload, fields)
	body, err := json.Marshal(payload)
	if err != nil {
		return
	}
	fmt.Fprintln(stdout, string(body))
}
