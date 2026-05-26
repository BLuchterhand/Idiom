package rootcmd

import (
	"context"
	"io"

	"idiom/internal/atomcmd"
	"idiom/internal/cmdroute"
	"idiom/internal/config"
	"idiom/internal/implementcmd"
	"idiom/internal/lanecmd"
	"idiom/internal/mcpcmd"
	"idiom/internal/planexecutecmd"
	"idiom/internal/productcmd"
	"idiom/internal/recovercmd"
	"idiom/internal/refinecmd"
	"idiom/internal/telemetry"
	"idiom/internal/writerroute"
)

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

func runAtomMainWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	subcommand := ""
	if len(args) > 0 {
		subcommand = args[0]
	}
	code := atomcmd.RunMain(args, stdout, stderr, atomcmd.Dependencies{})
	logCLIPhase(rec, "atom", map[string]any{"subcommand": subcommand, "exit_code": code})
	return code
}

func runAtomIntakeMain(args []string, stdout io.Writer, stderr io.Writer) int {
	return atomcmd.RunIntakeMain(args, stdout, stderr, atomcmd.Dependencies{})
}

func evaluateAtomIntake(specBody, specPath, projectRoot string, allowDirty bool) atomIntakeReport {
	return atomcmd.EvaluateIntake(specBody, specPath, projectRoot, allowDirty, atomcmd.DefaultScopePaths(specBody))
}

func printAtomIntakeReport(report atomIntakeReport, w io.Writer) {
	atomcmd.PrintIntakeReport(report, w)
}

func atomSpecDispatchBlocker(specBody string) string {
	return atomcmd.SpecDispatchBlocker(specBody)
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

func atomMarkdownFenceOpen(line string) (marker string, info string, ok bool) {
	return atomcmd.MarkdownFenceOpen(line)
}

func parseGitPorcelainPath(line string) string {
	return atomcmd.ParseGitPorcelainPath(line)
}

func runImplementMain(args []string, stdout io.Writer, stderr io.Writer, promptRun promptRunner) int {
	return implementcmd.RunMain(args, stdout, stderr, promptRun)
}

func runImplementMainWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, promptRun promptRunner, rec *telemetry.Recorder) int {
	return implementcmd.RunMainWithTelemetry(args, stdout, stderr, promptRun, rec)
}

func runLaneMain(args []string, stdout io.Writer, stderr io.Writer) int {
	return lanecmd.RunMain(args, stdout, stderr)
}

func runLaneMainWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	return lanecmd.RunMainWithTelemetry(args, stdout, stderr, rec)
}

type laneRefineOptions = refinecmd.Options
type refineDispatchFn = refinecmd.DispatchFunc

func runLaneRefine(opts laneRefineOptions, dispatch refineDispatchFn, stdout, stderr io.Writer) int {
	return refinecmd.Run(opts, dispatch, stdout, stderr)
}

func defaultRefineDispatchWithConfig(metaBriefPath string, configPath string, stdout, stderr io.Writer) int {
	return refinecmd.DefaultDispatchWithConfig(metaBriefPath, configPath, stdout, stderr)
}

const mcpDiscoveryProfile = mcpcmd.DiscoveryProfile
const mcpStdioTransport = mcpcmd.StdioTransport

type mcpDiscoveryServerOptions = mcpcmd.DiscoveryServerOptions

type mcpDiscoveryServer struct {
	inner *mcpcmd.DiscoveryServer
}

func runMCPMainWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	return mcpcmd.RunWithTelemetryAndVersion(args, stdout, stderr, rec, version)
}

func newMCPDiscoveryServer(cfg config.Config, profile string) (*mcpDiscoveryServer, error) {
	server, err := mcpcmd.NewDiscoveryServerWithOptions(cfg, profile, mcpDiscoveryServerOptions{Version: version})
	if err != nil {
		return nil, err
	}
	return &mcpDiscoveryServer{inner: server}, nil
}

func newMCPDiscoveryServerWithOptions(cfg config.Config, profile string, opts mcpDiscoveryServerOptions) (*mcpDiscoveryServer, error) {
	server, err := mcpcmd.NewDiscoveryServerWithOptions(cfg, profile, opts)
	if err != nil {
		return nil, err
	}
	return &mcpDiscoveryServer{inner: server}, nil
}

func (s *mcpDiscoveryServer) serve(ctx context.Context, stdin io.Reader, stdout io.Writer) error {
	return s.inner.Serve(ctx, stdin, stdout)
}

func mcpResourcePathDenied(rel string) (bool, string) {
	return mcpcmd.ResourcePathDenied(rel)
}

func runPlanExecuteMain(args []string, stdout io.Writer, stderr io.Writer, promptRun promptRunner) int {
	return planexecutecmd.RunMain(args, stdout, stderr, promptRun)
}

func runPlanExecuteMainWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, promptRun promptRunner, rec *telemetry.Recorder) int {
	return planexecutecmd.RunMainWithTelemetry(args, stdout, stderr, promptRun, rec)
}

func newPlanExecuteSessionID() string {
	return planexecutecmd.NewSessionID()
}

func firstNonEmptyString(values ...string) string {
	return planexecutecmd.FirstNonEmptyString(values...)
}

func planExecuteUniqueRequiredPaths(requiredEditPaths []string) []string {
	return planexecutecmd.UniqueRequiredPaths(requiredEditPaths)
}

func runProductMainWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	return productcmd.RunMainWithTelemetry(args, stdout, stderr, rec)
}

func runRecoverMainWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	return recovercmd.RunMainWithTelemetry(args, stdout, stderr, rec)
}

func runRecoverMain(args []string, stdout io.Writer, stderr io.Writer) int {
	return recovercmd.RunMain(args, stdout, stderr)
}

type cliWriterRouteOptions = cmdroute.Options

func buildCLIWriterRouteDecision(cfg config.Config, opts cliWriterRouteOptions) writerroute.Decision {
	return cmdroute.BuildDecision(cfg, opts)
}

func cliWriterRouteRoleAliases(projectRoot string, profileName string) map[string]string {
	return cmdroute.RoleAliases(projectRoot, profileName)
}

func cliWriterRouteModelForRole(cfg config.Config, aliases map[string]string, role string) string {
	return cmdroute.ModelForRole(cfg, aliases, role)
}
