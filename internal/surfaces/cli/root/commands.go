package root

import (
	"io"

	"idiom/internal/evidence/telemetry"
	atomcmd "idiom/internal/surfaces/cli/atom"
	mcpcmd "idiom/internal/surfaces/cli/mcp"
)

func runAtomMainWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	subcommand := ""
	if len(args) > 0 {
		subcommand = args[0]
	}

	code := atomcmd.RunMain(args, stdout, stderr, atomcmd.Dependencies{})
	telemetry.LogPhase(rec, "atom", map[string]any{"subcommand": subcommand, "exit_code": code})

	return code
}

func runMCPMainWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	return mcpcmd.RunWithTelemetryAndVersion(args, stdout, stderr, rec, version)
}
