package root

import (
	"context"
	"io"

	"idiom/internal/evidence/lanehistory"
	"idiom/internal/evidence/productevidence"
	"idiom/internal/evidence/telemetry"
	"idiom/internal/platform/config"
	"idiom/internal/platform/sessionid"
	"idiom/internal/runtime/localops"
	artifactcmd "idiom/internal/surfaces/cli/artifact"
	cmdconfig "idiom/internal/surfaces/cli/config"
	featureplannercmd "idiom/internal/surfaces/cli/featureplanner"
	localopscmd "idiom/internal/surfaces/cli/localops"
	planexecutecmd "idiom/internal/surfaces/cli/planexecute"
	rankercmd "idiom/internal/surfaces/cli/ranker"
	cmdruntime "idiom/internal/surfaces/cli/runtime"
	coreplanexecute "idiom/internal/workflows/planexecute"
	planexecuteadapters "idiom/internal/workflows/planexecute/adapters"
)

func runArtifactMainWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, promptRun cmdruntime.PromptRunner, rec *telemetry.Recorder) int {
	return artifactcmd.RunMainWithTelemetry(args, stdout, stderr, adaptArtifactPromptRunner(promptRun), rec)
}

func adaptArtifactPromptRunner(promptRun cmdruntime.PromptRunner) artifactcmd.PromptRunner {
	return func(cfg config.Config, prompt string, opts artifactcmd.RunOptions, w io.Writer) (string, error) {
		return promptRun(cfg, prompt, cmdruntime.RunOptions{
			JSONEvents:        opts.JSONEvents,
			Resume:            opts.Resume,
			TaskFamily:        opts.TaskFamily,
			MaxTokensOverride: opts.MaxTokensOverride,
			Telemetry:         opts.Telemetry,
		}, w)
	}
}

func featurePlannerDependencies() featureplannercmd.Dependencies {
	return featureplannercmd.Dependencies{
		NewSessionID:            sessionid.New,
		LocalOpsTelemetryFields: localops.TelemetryFields,
		RunPlanExecute: func(_ context.Context, cfg config.Config, opts coreplanexecute.Options, stdout io.Writer, stderr io.Writer) coreplanexecute.RunResult {
			return coreplanexecute.RunWithDependencies(cfg, opts, planexecutecmd.AdaptPromptRunner(cmdruntime.RunPromptOnce), nil, stdout, stderr, planexecuteadapters.Dependencies())
		},
	}
}

func runLocalOpsMainWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	return localopscmd.RunMainWithTelemetry(args, stdout, stderr, rec, localopscmd.Dependencies{
		Version:    version,
		Commit:     commit,
		LoadConfig: cmdconfig.Load,
	})
}

func productEvidenceDeps() productevidence.Dependencies {
	return productevidence.Dependencies{
		SummarizeLaneHistory: lanehistory.Summarize,
		RunMainNoTelemetry:   runMain,
		DemoCommandRunnerID:  "cmd/idiom:runMain",
		Version:              version,
		Commit:               commit,
		Date:                 date,
	}
}

func runRankerMainWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	return rankercmd.Run(args, stdout, stderr, rec, rankercmd.Deps{
		LoadConfig: cmdconfig.Load,
		LogPhase:   telemetry.LogPhase,
	})
}
