package root

import (
	"io"
	"os"

	"idiom/internal/evidence/lanehistory"
	"idiom/internal/evidence/productevidence"
	"idiom/internal/evidence/telemetry"
	"idiom/internal/platform/config"
	architectcmd "idiom/internal/surfaces/cli/architect"
	artifactcmd "idiom/internal/surfaces/cli/artifact"
	auditcmd "idiom/internal/surfaces/cli/audit"
	backendcmd "idiom/internal/surfaces/cli/backend"
	briefcmd "idiom/internal/surfaces/cli/brief"
	"idiom/internal/surfaces/cli/config"
	contextcmd "idiom/internal/surfaces/cli/context"
	doctorcmd "idiom/internal/surfaces/cli/doctor"
	featureplannercmd "idiom/internal/surfaces/cli/featureplanner"
	headlesscmd "idiom/internal/surfaces/cli/headless"
	initcmd "idiom/internal/surfaces/cli/init"
	localopscmd "idiom/internal/surfaces/cli/localops"
	probecmd "idiom/internal/surfaces/cli/probe"
	profilecmd "idiom/internal/surfaces/cli/profile"
	queuecmd "idiom/internal/surfaces/cli/queue"
	rankercmd "idiom/internal/surfaces/cli/ranker"
	skillscmd "idiom/internal/surfaces/cli/skills"
	"idiom/internal/surfaces/cli/telemetry"
	toolcmd "idiom/internal/surfaces/cli/tool"
)

type laneHistorySummary = lanehistory.Summary

func runArchitectMainWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	return architectcmd.RunWithTelemetry(args, stdout, stderr, rec)
}

func writeArchitectOutput(path string, data []byte, stdout io.Writer) error {
	return architectcmd.WriteOutput(path, data, stdout)
}

func runArtifactMainWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, promptRun promptRunner, rec *telemetry.Recorder) int {
	return artifactcmd.RunMainWithTelemetry(args, stdout, stderr, adaptArtifactPromptRunner(promptRun), rec)
}

func adaptArtifactPromptRunner(promptRun promptRunner) artifactcmd.PromptRunner {
	return func(cfg config.Config, prompt string, opts artifactcmd.RunOptions, w io.Writer) (string, error) {
		return promptRun(cfg, prompt, runOptions{
			JSONEvents:        opts.JSONEvents,
			Resume:            opts.Resume,
			TaskFamily:        opts.TaskFamily,
			MaxTokensOverride: opts.MaxTokensOverride,
			Telemetry:         opts.Telemetry,
		}, w)
	}
}

func runAuditMainWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	return auditcmd.RunMainWithTelemetry(args, stdout, stderr, rec)
}

func runProfileMain(args []string, stdout io.Writer, stderr io.Writer) int {
	return profilecmd.Run(args, stdout, stderr)
}

func runBackendMainWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	return backendcmd.RunWithTelemetry(args, stdout, stderr, rec)
}

func runBriefMainWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	return briefcmd.RunMainWithTelemetry(args, stdout, stderr, rec)
}

func runContextMainWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	return contextcmd.Run(args, stdout, stderr, rec, contextcmd.Deps{
		LoadConfig:     toolcmd.LoadConfig,
		LogPhase:       toolcmd.LogCLIPhase,
		LogToolRequest: toolcmd.LogDirectToolRequest,
		LogToolResult:  toolcmd.LogDirectToolResult,
		WriteOutput:    writeArchitectOutput,
	})
}

func runDoctorMainWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	return doctorcmd.RunWithTelemetry(args, stdout, stderr, rec)
}

func runFeaturePlannerMainWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	return featureplannercmd.RunMainWithTelemetry(args, stdout, stderr, rec, featurePlannerDependencies())
}

func runHeadlessMainWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, promptRun promptRunner, rec *telemetry.Recorder) int {
	return headlesscmd.Run(args, os.Stdin, stdout, stderr, headlesscmd.PromptRunner(promptRun), rec)
}

func runHeadlessServeMain(args []string, stdout io.Writer, stderr io.Writer) int {
	return headlesscmd.RunServe(args, stdout, stderr)
}

func runHeadlessSessionMain(args []string, stdout io.Writer, stderr io.Writer) int {
	return headlesscmd.RunSession(args, stdout, stderr)
}

func runHeadlessContractMain(args []string, stdout io.Writer, stderr io.Writer) int {
	return headlesscmd.RunContract(args, stdout, stderr)
}

func runHeadlessDecisionMain(args []string, stdout io.Writer, stderr io.Writer) int {
	return headlesscmd.RunDecision(args, stdout, stderr)
}

func featurePlannerDependencies() featureplannercmd.Dependencies {
	return featureplannercmd.Dependencies{
		NewSessionID:            newPlanExecuteSessionID,
		LocalOpsTelemetryFields: cmdtelemetry.LocalOpsFields,
		RunPlanExecute: func(args []string, stdout io.Writer, stderr io.Writer) int {
			return runPlanExecuteMain(args, stdout, stderr, runPromptOnce)
		},
	}
}

func runInitMain(args []string, stdout io.Writer, stderr io.Writer) int {
	return initcmd.RunMain(args, stdout, stderr)
}

func runLocalOpsMainWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	return localopscmd.RunMainWithTelemetry(args, stdout, stderr, rec, localopscmd.Dependencies{
		Version:    version,
		Commit:     commit,
		LoadConfig: cmdconfig.Load,
	})
}

func runProbeMainWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	return probecmd.RunWithTelemetry(args, stdout, stderr, rec)
}

func runProductEvidenceMainWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	return productevidence.RunProductEvidenceMainWithTelemetry(args, stdout, stderr, rec, productEvidenceDeps())
}

func runReleaseMainWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	return productevidence.RunReleaseMainWithTelemetry(args, stdout, stderr, rec, productEvidenceDeps())
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
	summary, err := summarizeLaneHistory(root)
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

func runQueueMainWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	return queuecmd.RunMainWithTelemetry(args, stdout, stderr, rec)
}

func runRankerMainWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	return rankercmd.Run(args, stdout, stderr, rec, rankercmd.Deps{
		LoadConfig: toolcmd.LoadConfig,
		LogPhase:   toolcmd.LogCLIPhase,
	})
}

func runSkillsMain(args []string, stdout io.Writer, stderr io.Writer) int {
	return skillscmd.RunMain(args, stdout, stderr)
}

func summarizeLaneHistory(projectRoot string) (laneHistorySummary, error) {
	return lanehistory.Summarize(projectRoot)
}
