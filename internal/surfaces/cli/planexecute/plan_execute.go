package planexecute

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	"idiom/internal/evidence/telemetry"
	"idiom/internal/platform/config"
	cmdruntime "idiom/internal/surfaces/cli/runtime"
	"idiom/internal/workflows/intentloop"
	"idiom/internal/workflows/planexecute"
	planexecuteadapters "idiom/internal/workflows/planexecute/adapters"
)

type planExecuteIntentLoopOptions struct {
	Enabled           bool
	MaxCycles         int
	TotalBudgetTokens int
}

type planExecuteIntentLoopRunner func(
	cfg config.Config,
	opts planexecute.Options,
	loopOpts planExecuteIntentLoopOptions,
	promptRun planexecute.PromptRunner,
	rec *telemetry.Recorder,
	stdout io.Writer,
	stderr io.Writer,
) int

var runPlanExecuteIntentLoop planExecuteIntentLoopRunner = defaultPlanExecuteIntentLoop

var runPlanExecuteIntentLoopChildRunner intentloop.PlanExecuteRunner

// RunMain is the CLI entry point for `idiom plan-execute`.
//
// Plan-then-execute is the third lane shape (alongside `idiom implement`
// and the harness-intercept lane): the planner runs FIRST with no
// per-edit write tools, emits a structured plan via submit_plan, and
// the orchestrator's executor phase iterates that plan and dispatches
// each edit deterministically against the workspace. The planner owns
// WHAT to change; the harness owns the plan contract and deterministic
// handoff to the executor.
func RunMainWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, promptRun cmdruntime.PromptRunner, rec *telemetry.Recorder) int {
	opts, loopOpts, err := parsePlanExecuteCLIOptions(args, stderr)
	if err != nil {
		fmt.Fprintln(stderr, err)

		return 2
	}

	cfg, err := config.Load(opts.ConfigPath)
	if err != nil {
		fmt.Fprintln(stderr, err)

		return 1
	}

	if strings.TrimSpace(opts.Model) != "" {
		cfg.LlamaServer.Model = opts.Model
		opts.UseActiveModel = true
	} else if strings.TrimSpace(cfg.LlamaServer.Model) != "" {
		opts.UseActiveModel = true
	}
	if opts.Context == nil {
		opts.Context = context.Background()
	}

	if loopOpts.Enabled {
		if runPlanExecuteIntentLoop == nil {
			fmt.Fprintln(stderr, "intent-loop runner is unavailable")

			return 1
		}

		return runPlanExecuteIntentLoop(cfg, opts, loopOpts, adaptPromptRunner(promptRun), rec, stdout, stderr)
	}

	result := planexecute.RunWithDependencies(cfg, opts, adaptPromptRunner(promptRun), rec, stdout, stderr, planexecuteadapters.Dependencies())

	return result.ExitCode
}

func adaptPromptRunner(promptRun cmdruntime.PromptRunner) planexecute.PromptRunner {
	return func(cfg config.Config, prompt string, opts planexecute.RunOptions, stdout io.Writer) (string, error) {
		return promptRun(cfg, prompt, cmdruntime.RunOptions{
			JSONEvents:                  opts.JSONEvents,
			Resume:                      opts.Resume,
			TaskFamily:                  opts.TaskFamily,
			PlannerOwnedVerification:    opts.PlannerOwnedVerification,
			MaxTokensOverride:           opts.MaxTokensOverride,
			DisableThinking:             opts.DisableThinking,
			ReviewRepairOpenObligations: opts.ReviewRepairOpenObligations,
			UseActiveModel:              opts.UseActiveModel,
			Telemetry:                   opts.Telemetry,
			ScopeContract:               opts.ScopeContract,
		}, stdout)
	}
}

func defaultPlanExecuteIntentLoop(
	cfg config.Config,
	opts planexecute.Options,
	loopOpts planExecuteIntentLoopOptions,
	promptRun planexecute.PromptRunner,
	rec *telemetry.Recorder,
	stdout io.Writer,
	stderr io.Writer,
) int {
	childRunner := runPlanExecuteIntentLoopChildRunner
	if childRunner == nil {
		childRunner = func(cfg config.Config, opts planexecute.Options, promptRun planexecute.PromptRunner, rec *telemetry.Recorder, stdout io.Writer, stderr io.Writer) planexecute.RunResult {
			if opts.Context == nil {
				opts.Context = context.Background()
			}
			return planexecute.RunWithDependencies(cfg, opts, promptRun, rec, stdout, stderr, planexecuteadapters.Dependencies())
		}
	}
	result := intentloop.Run(cfg, intentloop.IntentLoopOptions{
		PlanExecuteOptions:          opts,
		SessionID:                   opts.SessionID,
		Resume:                      opts.Resume,
		MaxCycles:                   loopOpts.MaxCycles,
		CycleBudgetTokens:           loopOpts.TotalBudgetTokens,
		DetectExternalScopedChanges: true,
		RunPlanExecute:              childRunner,
	}, promptRun, rec, stdout, stderr)
	return result.ExitCode
}

func parsePlanExecuteCLIOptions(args []string, stderr io.Writer) (planexecute.Options, planExecuteIntentLoopOptions, error) {
	planArgs, loopOpts, err := splitPlanExecuteIntentLoopArgs(args)
	if err != nil {
		return planexecute.Options{}, planExecuteIntentLoopOptions{}, err
	}

	opts, err := planexecute.ParseOptions(planArgs, stderr)
	if err != nil {
		return planexecute.Options{}, planExecuteIntentLoopOptions{}, err
	}

	return opts, loopOpts, nil
}

func splitPlanExecuteIntentLoopArgs(args []string) ([]string, planExecuteIntentLoopOptions, error) {
	loopOpts := planExecuteIntentLoopOptions{MaxCycles: 5}
	out := make([]string, 0, len(args))

	for i := 0; i < len(args); i++ {
		arg := args[i]
		name, value, hasValue := splitFlagValue(arg)

		switch name {
		case "-intent-loop":
			enabled := true
			if hasValue {
				parsed, err := strconv.ParseBool(value)
				if err != nil {
					return nil, loopOpts, fmt.Errorf("invalid --intent-loop value %q: %w", value, err)
				}
				enabled = parsed
			}
			loopOpts.Enabled = enabled
		case "-max-cycles":
			parsed, next, err := parseIntFlagValue("--max-cycles", value, hasValue, args, i)
			if err != nil {
				return nil, loopOpts, err
			}
			if parsed <= 0 {
				return nil, loopOpts, fmt.Errorf("--max-cycles must be greater than 0")
			}
			loopOpts.MaxCycles = parsed
			i = next
		case "-total-budget-tokens":
			parsed, next, err := parseIntFlagValue("--total-budget-tokens", value, hasValue, args, i)
			if err != nil {
				return nil, loopOpts, err
			}
			if parsed < 0 {
				return nil, loopOpts, fmt.Errorf("--total-budget-tokens must be greater than or equal to 0")
			}
			loopOpts.TotalBudgetTokens = parsed
			i = next
		default:
			out = append(out, arg)
		}
	}

	return out, loopOpts, nil
}

func splitFlagValue(arg string) (string, string, bool) {
	if strings.HasPrefix(arg, "--") {
		arg = arg[1:]
	}
	if idx := strings.Index(arg, "="); idx >= 0 {
		return arg[:idx], arg[idx+1:], true
	}
	return arg, "", false
}

func parseIntFlagValue(flagName string, inlineValue string, hasInlineValue bool, args []string, index int) (int, int, error) {
	value := inlineValue
	next := index
	if !hasInlineValue {
		if index+1 >= len(args) {
			return 0, index, fmt.Errorf("%s requires a value", flagName)
		}
		value = args[index+1]
		next = index + 1
	}

	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, index, fmt.Errorf("invalid %s value %q: %w", flagName, value, err)
	}

	return parsed, next, nil
}
