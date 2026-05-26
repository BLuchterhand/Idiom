package probecmd

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"idiom/internal/agent"
	"idiom/internal/app"
	"idiom/internal/config"
	"idiom/internal/telemetry"
	"idiom/internal/workspace"
)

// Run dispatches `idiom probe <subcommand>`. Phase 6 ships
// `lane`; future probes (`probe backend`, `probe routing`, etc.) plug
// in here without rewiring main.go.
func Run(args []string, stdout io.Writer, stderr io.Writer) int {
	return RunWithTelemetry(args, stdout, stderr, nil)
}

func RunWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		printProbeUsage(stderr)
		return 0
	}
	logProbePhase(rec, "probe_start", map[string]any{"subcommand": args[0]})
	switch args[0] {
	case "lane":
		return runProbeLane(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "probe: unknown subcommand %q\n", args[0])
		printProbeUsage(stderr)
		return 2
	}
}

// runProbeLane executes the synthetic-brief route probe. For each
// selected mode it constructs the route the lane wrapper would
// force via --task-family and asserts the resulting plan + brief
// match the contract documented in docs/reusability-design.md.
//
// Exit codes mirror the rest of `idiom backend`: 0 all probes pass,
// 1 at least one assertion failed, 2 flag-parse error.
func runProbeLane(args []string, stdout io.Writer, stderr io.Writer) int {
	fs := flag.NewFlagSet("idiom probe lane", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		modeFlag    string
		configPath  string
		projectRoot string
	)
	fs.StringVar(&modeFlag, "mode", "all", "which probe(s) to run: implement | plan-execute | all")
	fs.StringVar(&projectRoot, "project-root", "", "project root containing .idiom/config.json (defaults to cwd; matches `idiom lane` resolution)")
	fs.StringVar(&configPath, "config", "", "explicit path to an idiom config (overrides --project-root resolution)")
	// --coder is accepted for surface parity with Colony's
	// idiom_route_probe.sh, which used it to switch the model alias.
	// In-process the routing decision doesn't depend on which model
	// answers, so the flag is intentionally a no-op; we still log
	// that it was set so operators don't think it's silently ignored.
	coderFlag := fs.Bool("coder", false, "request the coder alias (no-op for in-process routing; accepted for spec parity)")
	if err := fs.Parse(args); err != nil {
		// flag.ErrHelp means the user asked for help (`-h` / `--help`).
		// The flag library already printed the usage block to stderr
		// via fs.Output(); exit 0 so shell pipelines don't treat help
		// as an error.
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *coderFlag {
		fmt.Fprintln(stderr, "probe lane: --coder accepted; in-process routing doesn't depend on the model alias.")
	}

	resolved, source, err := resolveProbeConfigPath(projectRoot, configPath)
	if err != nil {
		fmt.Fprintf(stderr, "probe lane: %v\n", err)
		return 1
	}
	allowed, loadErr := loadAllowedToolsFromConfig(resolved)
	if loadErr != nil {
		// Don't fail the probe just because we can't load config —
		// the routing assertions are independent of which tools are
		// allowed; only the "tools surface non-empty" check needs
		// it. Fall back to a synthetic non-empty list so the routing
		// path still gets exercised.
		fmt.Fprintf(stderr, "probe lane: config load failed (%v); falling back to synthetic tool surface\n", loadErr)
		allowed = []string{"read_file", "write_file"}
	} else {
		fmt.Fprintf(stderr, "probe lane: tool surface from %s=%s (n=%d)\n", source, resolved, len(allowed))
	}

	probes := selectedProbes(modeFlag)
	if len(probes) == 0 {
		fmt.Fprintf(stderr, "probe lane: --mode %q is not recognized (use implement | plan-execute | all)\n", modeFlag)
		return 2
	}

	exit := 0
	for _, p := range probes {
		res, err := app.ProbeLane(app.ProbeLaneOptions{
			Mode:         p.mode,
			TaskFamily:   p.taskFamily,
			AllowedTools: allowed,
		})
		if err != nil {
			fmt.Fprintf(stdout, "FAIL %s — %v\n", p.label, err)
			exit = 1
			continue
		}
		if violations := assertProbeShape(p, res); len(violations) > 0 {
			fmt.Fprintf(stdout, "FAIL %s — %s\n", p.label, strings.Join(violations, "; "))
			fmt.Fprintln(stdout, "      "+app.FormatProbeResult("got:", res))
			exit = 1
			continue
		}
		fmt.Fprintln(stdout, app.FormatProbeResult("PASS "+p.label, res))
	}
	return exit
}

// probeCase pairs the user-facing label with the inputs and the
// expected route+brief shape. Centralizing the expectations here
// keeps the assertion code generic — future probes can land by
// adding one row to selectedProbes.
//
// `mode` is the agent.Mode the probe runs under; the assertion is
// that explicit_task_family routing produces the same shape under
// every shipped mode (BuildMode, PlanMode, etc.). If a future
// refactor makes normalizeTurnRoutePlan mode-dependent for an
// explicit family, the cross-mode probes here flip from PASS to
// FAIL — that's the regression class this layer guards.
type probeCase struct {
	label        string
	taskFamily   string
	mode         agent.Mode
	wantInitial  string
	wantContract string
	wantBriefFam string
	wantReason   string
	wantToolsMin int
	wantFollowup string // empty-string means "must be empty"
}

func selectedProbes(modeFlag string) []probeCase {
	// Each user-facing probe runs under both BuildMode and PlanMode
	// so we catch a routing refactor that introduces mode sensitivity
	// for explicit_task_family. Today the routing is mode-independent
	// for that path, so all four cases pass; the cross-mode coverage
	// is defense-in-depth, not a "this varies" assertion.
	makeCases := func(label, family, briefFam string) []probeCase {
		base := probeCase{
			taskFamily:   family,
			wantInitial:  "implement",
			wantContract: "implementation_result",
			wantBriefFam: briefFam,
			wantReason:   "explicit_task_family",
			wantToolsMin: 1,
		}
		buildCase := base
		buildCase.label = label + "(build)"
		buildCase.mode = agent.BuildMode()
		planCase := base
		planCase.label = label + "(plan)"
		planCase.mode = agent.PlanMode()
		return []probeCase{buildCase, planCase}
	}
	implement := makeCases("implement", "implementation", "implementation")
	planExecute := makeCases("plan-execute", "plan_then_execute_planner", "plan_then_execute_planner")
	switch strings.TrimSpace(strings.ToLower(modeFlag)) {
	case "implement":
		return implement
	case "plan-execute":
		return planExecute
	case "", "all":
		return append(implement, planExecute...)
	default:
		return nil
	}
}

// assertProbeShape returns one human-readable violation per failed
// expectation. An empty slice means the probe matched. Returning a
// list (rather than failing on first mismatch) lets the operator see
// every drift in one report.
func assertProbeShape(want probeCase, got app.ProbeLaneResult) []string {
	var v []string
	if got.InitialLane != want.wantInitial {
		v = append(v, fmt.Sprintf("initial=%q want %q", got.InitialLane, want.wantInitial))
	}
	if got.FollowupLane != want.wantFollowup {
		v = append(v, fmt.Sprintf("followup=%q want %q", got.FollowupLane, want.wantFollowup))
	}
	if got.Contract != want.wantContract {
		v = append(v, fmt.Sprintf("contract=%q want %q", got.Contract, want.wantContract))
	}
	if got.BriefTaskFam != want.wantBriefFam {
		v = append(v, fmt.Sprintf("brief.task_family=%q want %q", got.BriefTaskFam, want.wantBriefFam))
	}
	if got.Reason != want.wantReason {
		v = append(v, fmt.Sprintf("reason=%q want %q", got.Reason, want.wantReason))
	}
	if got.AllowedToolsN < want.wantToolsMin {
		v = append(v, fmt.Sprintf("allowed_tools=%d want >=%d", got.AllowedToolsN, want.wantToolsMin))
	}
	return v
}

// loadAllowedToolsFromConfig builds a workspace from the operator's
// config and returns its allowed-tools surface. Used by the probe
// to verify the lane wrapper would actually have a non-empty tool
// list to hand the model — a regression where the surface drops to
// zero would silently break every implementation lane.
func loadAllowedToolsFromConfig(configPath string) ([]string, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, err
	}
	mgr := workspace.NewManager(cfg.Workspace)
	return mgr.AllowedTools(), nil
}

// resolveProbeConfigPath picks which config the probe should load.
// Precedence matches the lane wrapper so an operator who runs
// `idiom probe lane` from any working directory gets the same tool
// surface they'd see at lane invocation time:
//
//  1. --config <path> (explicit override; rarely set)
//  2. <project-root>/.idiom/config.json (the lane wrapper's path)
//  3. <project-root>/config.json (legacy single-file location)
//  4. cwd-relative config.json (last-resort fallback)
//
// Returns (path, source, err). The source string ("explicit",
// ".idiom/config.json", "config.json", "cwd") is what the CLI
// echoes to stderr so the operator can see which file actually
// answered the probe. The probe still has the synthetic-tools
// fallback at the call site if every path above fails to load.
func resolveProbeConfigPath(projectRoot, explicit string) (string, string, error) {
	if p := strings.TrimSpace(explicit); p != "" {
		return p, "--config", nil
	}
	root := strings.TrimSpace(projectRoot)
	if root == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", "", err
		}
		root = cwd
	}
	for _, candidate := range []string{
		filepath.Join(root, ".idiom", "config.json"),
		filepath.Join(root, "config.json"),
	} {
		if _, err := os.Stat(candidate); err == nil {
			return candidate, "project-root", nil
		}
	}
	// Nothing on disk; return the cwd-relative default so
	// loadAllowedToolsFromConfig surfaces the not-found error
	// for the synthetic-fallback path.
	return "config.json", "cwd-default", nil
}

func printProbeUsage(stderr io.Writer) {
	fmt.Fprintln(stderr, "usage: idiom probe <subcommand> [flags]")
	fmt.Fprintln(stderr, "subcommands:")
	fmt.Fprintln(stderr, "  lane            synthetic-brief route probe")
	fmt.Fprintln(stderr, "                  flags: --mode implement|plan-execute|all  --project-root <path>  --config <path>  --coder")
	fmt.Fprintln(stderr)
	fmt.Fprintln(stderr, "Replaces Colony/scripts/idiom_route_probe.sh.")
}

func logProbePhase(rec *telemetry.Recorder, eventType string, fields map[string]any) {
	if rec == nil {
		return
	}
	if fields == nil {
		fields = map[string]any{}
	}
	_ = rec.Log(eventType, fields)
}
