package main

import (
	"io"

	"idiom/internal/contextcmd"
	"idiom/internal/telemetry"
)

func runContextMain(args []string, stdout io.Writer, stderr io.Writer) int {
	return runContextMainWithTelemetry(args, stdout, stderr, nil)
}

func runContextMainWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	return contextcmd.Run(args, stdout, stderr, rec, contextcmd.Deps{
		LoadConfig:     loadToolConfig,
		LogPhase:       logCLIPhase,
		LogToolRequest: logDirectToolRequest,
		LogToolResult:  logDirectToolResult,
		WriteOutput:    writeArchitectOutput,
	})
}
