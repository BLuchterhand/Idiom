package lane

import (
	"io"
	"strings"

	"idiom/internal/evidence/lanehistory"
	"idiom/internal/evidence/telemetry"
	"idiom/internal/runtime/readiness"
	refinecmd "idiom/internal/surfaces/cli/refine"
	cmdruntime "idiom/internal/surfaces/cli/runtime"
	"idiom/internal/workflows/atomintake"
)

type runOptions = cmdruntime.RunOptions
type promptRunner = cmdruntime.PromptRunner

type atomIntakeReport = atomintake.IntakeReport

const laneHistoryRelPath = lanehistory.RelPath

type laneHistoryEvent = lanehistory.Event
type laneHistoryFilter = lanehistory.Filter
type readinessProfileReport = readiness.ProfileReport
type laneRefineOptions = refinecmd.Options

func RunMainWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	return runLaneMainWithTelemetry(args, stdout, stderr, rec)
}

func laneRunFinalState(exitCode int, degradations []laneDegradation, out []byte) string {
	if exitCode != 0 {
		return lanehistory.FinalStateFailed
	}

	if len(degradations) > 0 {
		return lanehistory.FinalStateDegraded
	}

	if hasReviewRepairDegradedEvent(out) {
		return lanehistory.FinalStateDegraded
	}

	if hasSourceSatisfiedEvent(out) {
		return lanehistory.FinalStateSourceSatisfied
	}

	return lanehistory.FinalStateSuccess
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
