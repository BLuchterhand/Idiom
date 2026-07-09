package runner

import (
	"context"
	"io"
	"strings"

	"idiom/internal/evidence/telemetry"
	"idiom/internal/platform/config"
	"idiom/internal/workflows/intentloop"
	"idiom/internal/workflows/planexecute"
	planexecuteadapters "idiom/internal/workflows/planexecute/adapters"
)

type Options struct {
	IntentLoop        bool
	MaxCycles         int
	TotalBudgetTokens int
	ChildRunner       intentloop.PlanExecuteRunner
}

// Run owns the plan-execute execution policy shared by CLI surfaces:
// model override activation, context defaults, optional intent-loop
// routing, and default workflow dependencies.
func Run(cfg config.Config, opts planexecute.Options, runOpts Options, promptRun planexecute.PromptRunner, rec *telemetry.Recorder, stdout io.Writer, stderr io.Writer) planexecute.RunResult {
	if strings.TrimSpace(opts.Model) != "" {
		cfg.LlamaServer.Model = opts.Model
		opts.UseActiveModel = true
	} else if strings.TrimSpace(cfg.LlamaServer.Model) != "" {
		opts.UseActiveModel = true
	}
	if opts.Context == nil {
		opts.Context = context.Background()
	}

	if runOpts.IntentLoop {
		childRunner := runOpts.ChildRunner
		if childRunner == nil {
			childRunner = defaultChildRunner
		}
		result := intentloop.Run(cfg, intentloop.IntentLoopOptions{
			PlanExecuteOptions:          opts,
			SessionID:                   opts.SessionID,
			Resume:                      opts.Resume,
			MaxCycles:                   runOpts.MaxCycles,
			CycleBudgetTokens:           runOpts.TotalBudgetTokens,
			DetectExternalScopedChanges: true,
			RunPlanExecute:              childRunner,
		}, promptRun, rec, stdout, stderr)
		return planexecute.RunResult{
			ExitCode:       result.ExitCode,
			SessionID:      result.SessionID,
			Completion:     result.Completion,
			CompletionPath: result.CompletionPath,
		}
	}

	return defaultChildRunner(cfg, opts, promptRun, rec, stdout, stderr)
}

func defaultChildRunner(cfg config.Config, opts planexecute.Options, promptRun planexecute.PromptRunner, rec *telemetry.Recorder, stdout io.Writer, stderr io.Writer) planexecute.RunResult {
	if opts.Context == nil {
		opts.Context = context.Background()
	}
	return planexecute.RunWithDependencies(cfg, opts, promptRun, rec, stdout, stderr, planexecuteadapters.Dependencies())
}
