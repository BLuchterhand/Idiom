package root

import (
	"io"

	"idiom/internal/evidence/lanehistory"
	"idiom/internal/evidence/productevidence"
	"idiom/internal/evidence/telemetry"
	"idiom/internal/platform/config"
	"idiom/internal/platform/sessionid"
	artifactcmd "idiom/internal/surfaces/cli/artifact"
	cmdconfig "idiom/internal/surfaces/cli/config"
	featureplannercmd "idiom/internal/surfaces/cli/featureplanner"
	localopscmd "idiom/internal/surfaces/cli/localops"
	planexecutecmd "idiom/internal/surfaces/cli/planexecute"
	rankercmd "idiom/internal/surfaces/cli/ranker"
	cmdruntime "idiom/internal/surfaces/cli/runtime"
	cmdtelemetry "idiom/internal/surfaces/cli/telemetry"
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
		LocalOpsTelemetryFields: cmdtelemetry.LocalOpsFields,
		RunPlanExecute: func(args []string, stdout io.Writer, stderr io.Writer) int {
			return planexecutecmd.RunMainWithTelemetry(args, stdout, stderr, cmdruntime.RunPromptOnce, nil)
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
		SummarizeLaneHistory: summarizeProductEvidenceLaneHistory,
		RunMainNoTelemetry:   runMain,
		DemoCommandRunnerID:  "cmd/idiom:runMain",
		Version:              version,
		Commit:               commit,
		Date:                 date,
	}
}

func summarizeProductEvidenceLaneHistory(root string) (productevidence.LaneHistorySummary, error) {
	summary, err := lanehistory.Summarize(root)
	if err != nil {
		return productevidence.LaneHistorySummary{}, err
	}

	return productevidence.LaneHistorySummary{
		Total:                 summary.Total,
		ByMode:                summary.ByMode,
		ByExitCode:            summary.ByExitCode,
		ByFinalState:          summary.ByFinalState,
		BriefsByPath:          summary.BriefsByPath,
		ByProvenance:          summary.ByProvenance,
		TotalModelCalls:       summary.TotalModelCalls,
		TotalToolCalls:        summary.TotalToolCalls,
		TotalPromptTokens:     summary.TotalPromptTokens,
		TotalCompletionTokens: summary.TotalCompletionTokens,
		TotalTokens:           summary.TotalTokens,
		TotalDurationMS:       summary.TotalDurationMS,
		EventsWithTokens:      summary.EventsWithTokens,
		EventsWithRunMetrics:  summary.EventsWithRunMetrics,
	}, nil
}

func runRankerMainWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	return rankercmd.Run(args, stdout, stderr, rec, rankercmd.Deps{
		LoadConfig: cmdconfig.Load,
		LogPhase:   telemetry.LogPhase,
	})
}
