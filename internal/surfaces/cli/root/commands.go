package root

import (
	"io"

	"idiom/internal/evidence/telemetry"
	atomcmd "idiom/internal/surfaces/cli/atom"
	implementcmd "idiom/internal/surfaces/cli/implement"
	lanecmd "idiom/internal/surfaces/cli/lane"
	mcpcmd "idiom/internal/surfaces/cli/mcp"
	planexecutecmd "idiom/internal/surfaces/cli/planexecute"
	productcmd "idiom/internal/surfaces/cli/product"
	recovercmd "idiom/internal/surfaces/cli/recover"
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

func runImplementMainWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, promptRun promptRunner, rec *telemetry.Recorder) int {
	return implementcmd.RunMainWithTelemetry(args, stdout, stderr, promptRun, rec)
}

func runLaneMainWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	return lanecmd.RunMainWithTelemetry(args, stdout, stderr, rec)
}

func runMCPMainWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	return mcpcmd.RunWithTelemetryAndVersion(args, stdout, stderr, rec, version)
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

func runProductMainWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	return productcmd.RunMainWithTelemetry(args, stdout, stderr, rec)
}

func runRecoverMainWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	return recovercmd.RunMainWithTelemetry(args, stdout, stderr, rec)
}
