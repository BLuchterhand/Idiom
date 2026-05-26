package main

import (
	"io"

	"idiom/internal/rankercmd"
	"idiom/internal/telemetry"
)

func runRankerMain(args []string, stdout io.Writer, stderr io.Writer) int {
	return runRankerMainWithTelemetry(args, stdout, stderr, nil)
}

func runRankerMainWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	return rankercmd.Run(args, stdout, stderr, rec, rankercmd.Deps{
		LoadConfig: loadToolConfig,
		LogPhase:   logCLIPhase,
	})
}
