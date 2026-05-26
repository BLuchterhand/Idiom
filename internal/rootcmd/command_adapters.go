package rootcmd

import (
	"io"

	"idiom/internal/architectcmd"
	"idiom/internal/artifactcmd"
	"idiom/internal/auditcmd"
	"idiom/internal/backendcmd"
	"idiom/internal/briefcmd"
	"idiom/internal/cmdconfig"
	"idiom/internal/cmdtelemetry"
	"idiom/internal/config"
	"idiom/internal/contextcmd"
	"idiom/internal/doctorcmd"
	"idiom/internal/featureplannercmd"
	"idiom/internal/initcmd"
	"idiom/internal/lanehistory"
	"idiom/internal/localopscmd"
	"idiom/internal/probecmd"
	"idiom/internal/productevidence"
	"idiom/internal/queuecmd"
	"idiom/internal/rankercmd"
	"idiom/internal/readiness"
	"idiom/internal/skillscmd"
	"idiom/internal/telemetry"
	"idiom/internal/toolcmd"
)

type briefLintFinding = briefcmd.LintFinding
type briefLintReport = briefcmd.LintReport

type checkResult struct {
	name   string
	status string
	detail string
	remedy string
}

type readinessProfileReport = readiness.ProfileReport

const laneHistoryRelPath = lanehistory.RelPath

type laneHistoryEvent = lanehistory.Event
type laneHistorySummary = lanehistory.Summary
type laneHistoryFilter = lanehistory.Filter

var p98RequiredMCPParityReadinessFields = productevidence.P98RequiredMCPParityReadinessFields()

func runArchitectMain(args []string, stdout io.Writer, stderr io.Writer) int {
	return runArchitectMainWithTelemetry(args, stdout, stderr, nil)
}

func runArchitectMainWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	return architectcmd.RunWithTelemetry(args, stdout, stderr, rec)
}

func writeArchitectOutput(path string, data []byte, stdout io.Writer) error {
	return architectcmd.WriteOutput(path, data, stdout)
}

func runArtifactMain(args []string, stdout io.Writer, stderr io.Writer, promptRun promptRunner) int {
	return runArtifactMainWithTelemetry(args, stdout, stderr, promptRun, nil)
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

func runAuditMain(args []string, stdout io.Writer, stderr io.Writer) int {
	return auditcmd.RunMain(args, stdout, stderr)
}

func runAuditMainWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	return auditcmd.RunMainWithTelemetry(args, stdout, stderr, rec)
}

func runBackendMain(args []string, stdout io.Writer, stderr io.Writer) int {
	return runBackendMainWithTelemetry(args, stdout, stderr, nil)
}

func runBackendMainWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	return backendcmd.RunWithTelemetry(args, stdout, stderr, rec)
}

func runBriefMain(args []string, stdout io.Writer, stderr io.Writer) int {
	return briefcmd.RunMain(args, stdout, stderr)
}

func runBriefMainWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	return briefcmd.RunMainWithTelemetry(args, stdout, stderr, rec)
}

func runBriefPreload(args []string, stdout io.Writer, stderr io.Writer) int {
	return briefcmd.RunPreload(args, stdout, stderr)
}

func runBriefLint(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	return briefcmd.RunLint(args, stdout, stderr, rec)
}

func runBriefPreview(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	return briefcmd.RunPreview(args, stdout, stderr, rec)
}

func parseBriefLintFindings(briefPath, combined string) []briefLintFinding {
	return briefcmd.ParseLintFindings(briefPath, combined)
}

func writeEmbeddedBriefPreflight() (path string, cleanup func(), err error) {
	return briefcmd.WriteEmbeddedPreflight()
}

func runContextMain(args []string, stdout io.Writer, stderr io.Writer) int {
	return runContextMainWithTelemetry(args, stdout, stderr, nil)
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

func runDoctorMain(args []string, stdout io.Writer, stderr io.Writer) int {
	return runDoctorMainWithTelemetry(args, stdout, stderr, nil)
}

func runDoctorMainWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	return doctorcmd.RunWithTelemetry(args, stdout, stderr, rec)
}

func buildDoctorReadinessProfiles(configPath, selected string, hostChecks []checkResult) ([]readinessProfileReport, error) {
	checks := make([]readiness.HostCheck, 0, len(hostChecks))
	for _, check := range hostChecks {
		checks = append(checks, readiness.HostCheck{
			Name:   check.name,
			Status: check.status,
			Detail: check.detail,
			Remedy: check.remedy,
		})
	}
	return readiness.BuildProfiles(configPath, selected, checks)
}

func isReadinessProfileSelectionError(err error) bool {
	return readiness.IsProfileSelectionError(err)
}

func runFeaturePlannerMain(args []string, stdout io.Writer, stderr io.Writer) int {
	return runFeaturePlannerMainWithTelemetry(args, stdout, stderr, nil)
}

func runFeaturePlannerMainWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	return featureplannercmd.RunMainWithTelemetry(args, stdout, stderr, rec, featurePlannerDependencies())
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

func runProbeMain(args []string, stdout io.Writer, stderr io.Writer) int {
	return runProbeMainWithTelemetry(args, stdout, stderr, nil)
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

func sanitizeProductEvidenceTelemetryArgs(args []string) []string {
	return productevidence.SanitizeProductEvidenceTelemetryArgs(args)
}

func sanitizeReleaseEvidenceTelemetryArgs(args []string) []string {
	return productevidence.SanitizeReleaseEvidenceTelemetryArgs(args)
}

func runQueueMain(args []string, stdout io.Writer, stderr io.Writer) int {
	return queuecmd.RunMain(args, stdout, stderr)
}

func runQueueMainWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	return queuecmd.RunMainWithTelemetry(args, stdout, stderr, rec)
}

func runRankerMain(args []string, stdout io.Writer, stderr io.Writer) int {
	return runRankerMainWithTelemetry(args, stdout, stderr, nil)
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

func detectBriefProvenance(briefPath string) string {
	return lanehistory.DetectBriefProvenance(briefPath)
}

func appendLaneHistory(projectRoot string, evt laneHistoryEvent) error {
	return lanehistory.Append(projectRoot, evt)
}

func laneHistoryEventFields(evt laneHistoryEvent) map[string]any {
	return lanehistory.EventFields(evt)
}

func summarizeLaneHistoryWithFilter(projectRoot string, filter laneHistoryFilter) (laneHistorySummary, error) {
	return lanehistory.SummarizeWithFilter(projectRoot, filter)
}

func summarizeLaneHistory(projectRoot string) (laneHistorySummary, error) {
	return lanehistory.Summarize(projectRoot)
}

func summarizeLaneHistoryByMode(projectRoot string, mode string) (laneHistorySummary, error) {
	return lanehistory.SummarizeByMode(projectRoot, mode)
}

func printLaneHistorySummary(projectRoot string, w io.Writer, filter laneHistoryFilter) error {
	return lanehistory.PrintSummary(projectRoot, w, filter)
}
