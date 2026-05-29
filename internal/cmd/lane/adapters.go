package lane
import (
	"io"
	"strings"

	"idiom/internal/app"
	atomcmd "idiom/internal/cmd/atom"
	briefcmd "idiom/internal/cmd/brief"
	"idiom/internal/cmdroute"
	"idiom/internal/cmdruntime"
	"idiom/internal/config"
	implementcmd "idiom/internal/cmd/implement"
	"idiom/internal/lanehistory"
	planexecutecmd "idiom/internal/cmd/planexecute"
	"idiom/internal/readiness"
	refinecmd "idiom/internal/cmd/refine"
	"idiom/internal/telemetry"
	toolcmd "idiom/internal/cmd/tool"
	"idiom/internal/writerroute"
)

type runOptions = cmdruntime.RunOptions
type promptRunner = cmdruntime.PromptRunner

type atomIntakeReport = atomcmd.IntakeReport
type atomRequiredEditSnippet = atomcmd.RequiredEditSnippet

const (
	atomStateDispatchable     = atomcmd.StateDispatchable
	atomStateComplete         = atomcmd.StateComplete
	atomStateSourceSatisfied  = atomcmd.StateSourceSatisfied
	atomStateStaleNeedsRebase = atomcmd.StateStaleNeedsRebase
	atomStateFailedRefine     = atomcmd.StateFailedRefine
	atomStateBlockedDirty     = atomcmd.StateBlockedDirty
	atomStateMissingScope     = atomcmd.StateMissingScope
	atomStateInvalidScope     = atomcmd.StateInvalidScope

	atomInvalidScopeOutsideProjectRoot     = atomcmd.InvalidScopeOutsideProjectRoot
	atomInvalidScopeAmbiguousBareShorthand = atomcmd.InvalidScopeAmbiguousBareShorthand
)

const laneHistoryRelPath = lanehistory.RelPath

type laneHistoryEvent = lanehistory.Event
type laneHistorySummary = lanehistory.Summary
type laneHistoryFilter = lanehistory.Filter
type readinessProfileReport = readiness.ProfileReport
type laneRefineOptions = refinecmd.Options
type refineDispatchFn = refinecmd.DispatchFunc
type cliWriterRouteOptions = cmdroute.Options

func RunMain(args []string, stdout io.Writer, stderr io.Writer) int {
	return runLaneMain(args, stdout, stderr)
}

func RunMainWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	return runLaneMainWithTelemetry(args, stdout, stderr, rec)
}

func runPromptOnce(cfg config.Config, prompt string, opts runOptions, stdout io.Writer) (string, error) {
	return cmdruntime.RunPromptOnce(cfg, prompt, opts, stdout)
}

func runPlanExecuteMain(args []string, stdout io.Writer, stderr io.Writer, promptRun promptRunner) int {
	return planexecutecmd.RunMain(args, stdout, stderr, promptRun)
}

func runPlanExecuteMainWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, promptRun promptRunner, rec *telemetry.Recorder) int {
	return planexecutecmd.RunMainWithTelemetry(args, stdout, stderr, promptRun, rec)
}

func runImplementMain(args []string, stdout io.Writer, stderr io.Writer, promptRun promptRunner) int {
	return implementcmd.RunMain(args, stdout, stderr, promptRun)
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

func atomRequiredEditPathsFromLabel(label string, scopePaths []string) []string {
	return atomcmd.RequiredEditPathsFromLabel(label, scopePaths)
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
	return planexecutecmd.UniqueRequiredPaths(requiredEditPaths)
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

func requiredEditPathsFromPrompt(prompt string) []string {
	return app.RequiredEditPathsFromPrompt(prompt)
}
