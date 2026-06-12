package lane

import (
	"io"
	"strings"

	"idiom/internal/contracts/writerroute"
	"idiom/internal/evidence/lanehistory"
	"idiom/internal/evidence/telemetry"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/readiness"
	atomcmd "idiom/internal/surfaces/cli/atom"
	briefcmd "idiom/internal/surfaces/cli/brief"
	implementcmd "idiom/internal/surfaces/cli/implement"
	planexecutecmd "idiom/internal/surfaces/cli/planexecute"
	refinecmd "idiom/internal/surfaces/cli/refine"
	"idiom/internal/surfaces/cli/route"
	"idiom/internal/surfaces/cli/runtime"
	toolcmd "idiom/internal/surfaces/cli/tool"
	"idiom/internal/workflows/planexecute"
)

type runOptions = cmdruntime.RunOptions
type promptRunner = cmdruntime.PromptRunner

type atomIntakeReport = atomcmd.IntakeReport
type atomRequiredEditSnippet = atomcmd.RequiredEditSnippet

const laneHistoryRelPath = lanehistory.RelPath

type laneHistoryEvent = lanehistory.Event
type laneHistoryFilter = lanehistory.Filter
type readinessProfileReport = readiness.ProfileReport
type laneRefineOptions = refinecmd.Options
type refineDispatchFn = refinecmd.DispatchFunc
type cliWriterRouteOptions = cmdroute.Options

func RunMainWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	return runLaneMainWithTelemetry(args, stdout, stderr, rec)
}

func runPromptOnce(cfg config.Config, prompt string, opts runOptions, stdout io.Writer) (string, error) {
	return cmdruntime.RunPromptOnce(cfg, prompt, opts, stdout)
}

func runPlanExecuteMainWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, promptRun promptRunner, rec *telemetry.Recorder) int {
	return planexecutecmd.RunMainWithTelemetry(args, stdout, stderr, promptRun, rec)
}

func runImplementMainWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, promptRun promptRunner, rec *telemetry.Recorder) int {
	return implementcmd.RunMainWithTelemetry(args, stdout, stderr, promptRun, rec)
}

func runBriefLint(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	return briefcmd.RunLint(args, stdout, stderr, rec)
}

func runLaneRefine(opts laneRefineOptions, dispatch refineDispatchFn, stdout, stderr io.Writer) int {
	return refinecmd.Run(opts, dispatch, stdout, stderr)
}

func defaultRefineDispatchWithConfig(metaBriefPath string, configPath string, stdout, stderr io.Writer) int {
	return refinecmd.DefaultDispatchWithConfig(metaBriefPath, configPath, stdout, stderr)
}

func evaluateAtomIntake(specBody, specPath, projectRoot string, allowDirty bool) atomIntakeReport {
	return atomcmd.EvaluateIntake(specBody, specPath, projectRoot, allowDirty, atomcmd.DefaultScopePaths(specBody))
}

func printAtomIntakeReport(report atomIntakeReport, w io.Writer) {
	atomcmd.PrintIntakeReport(report, w)
}

func atomSpecStatus(specBody string) (string, bool) {
	return atomcmd.SpecStatus(specBody)
}

func atomRefineDraftReason(specBody string) (string, bool) {
	return atomcmd.RefineDraftReason(specBody)
}

func atomSpecHasAcceptedResult(specBody string) bool {
	return atomcmd.SpecHasAcceptedResult(specBody)
}

func atomRequiredEditSnippetsFromSpec(specBody string, scopePaths []string) []atomRequiredEditSnippet {
	return atomcmd.RequiredEditSnippetsFromSpec(specBody, scopePaths)
}

func atomMarkdownSectionByHeading(markdown string, headingName string) string {
	return atomcmd.MarkdownSectionByHeading(markdown, headingName)
}

func buildCLIWriterRouteDecision(cfg config.Config, opts cliWriterRouteOptions) writerroute.Decision {
	return cmdroute.BuildDecision(cfg, opts)
}

func cliWriterRouteRoleAliases(projectRoot string, profileName string) map[string]string {
	return cmdroute.RoleAliases(projectRoot, profileName)
}

func planExecuteUniqueRequiredPaths(requiredEditPaths []string) []string {
	return planexecute.PlanExecuteUniqueRequiredPaths(requiredEditPaths)
}

func newPlanExecuteSessionID() string {
	return planexecutecmd.NewSessionID()
}

func firstNonEmptyString(values ...string) string {
	return planexecutecmd.FirstNonEmptyString(values...)
}

func logCLIPhase(rec *telemetry.Recorder, eventType string, fields map[string]any) {
	toolcmd.LogCLIPhase(rec, eventType, fields)
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

func printLaneHistorySummary(projectRoot string, w io.Writer, filter laneHistoryFilter) error {
	return lanehistory.PrintSummary(projectRoot, w, filter)
}

func buildDoctorReadinessProfiles(configPath, selected string, hostChecks []readiness.HostCheck) ([]readinessProfileReport, error) {
	return readiness.BuildProfiles(configPath, selected, hostChecks)
}

func laneRunFinalState(exitCode int, degradations []laneDegradation, out []byte) string {
	if exitCode != 0 {
		return "failed"
	}

	if len(degradations) > 0 {
		return "degraded"
	}

	if hasReviewRepairDegradedEvent(out) {
		return "degraded"
	}

	if hasSourceSatisfiedEvent(out) {
		return "source-satisfied"
	}

	return "success"
}

func laneDegradationKinds(degradations []laneDegradation) []string {
	if len(degradations) == 0 {
		return nil
	}

	kinds := make([]string, 0, len(degradations))
	for _, degradation := range degradations {
		if strings.TrimSpace(degradation.kind) == "" {
			continue
		}

		kinds = append(kinds, degradation.kind)
	}

	if len(kinds) == 0 {
		return nil
	}

	return kinds
}
