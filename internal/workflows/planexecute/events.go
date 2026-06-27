package planexecute

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	plancontract "idiom/internal/contracts/plan"
	"idiom/internal/evidence/transcript"
	"idiom/internal/platform/jsonevents"
)

var errPlanExecuteJSONEventWrite = errors.New(string(plancontract.PlanExecuteFailureJSONEventWriteFailed))

func emitPlanExecuteJSONEvent(stdout io.Writer, jsonEvents bool, eventType plancontract.PlanExecuteEventType, fields map[string]any) error {
	if !plancontract.KnownPlanExecuteEventType(eventType) {
		return fmt.Errorf("unknown plan-execute event type %q", eventType)
	}
	return jsonevents.Emit(stdout, jsonEvents, plancontract.PlanExecuteEventName(eventType), fields)
}

func emitPlanExecuteInvalidJSONEvent(stdout io.Writer, jsonEvents bool, eventType plancontract.PlanExecuteEventType, fields map[string]any) error {
	if !plancontract.KnownPlanExecuteEventType(eventType) {
		return fmt.Errorf("unknown plan-execute invalid event type %q", eventType)
	}
	return jsonevents.Emit(stdout, jsonEvents, plancontract.PlanExecuteInvalidEventName(eventType), fields)
}

func planExecuteJSONEventWriteError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w: %w", errPlanExecuteJSONEventWrite, err)
}

func isPlanExecuteJSONEventWriteFailure(err error) bool {
	return errors.Is(err, errPlanExecuteJSONEventWrite)
}

func emitPlanExecuteSyntheticTurnSummary(stdout io.Writer, jsonEvents bool, stopReason string) error {
	if !jsonEvents {
		return nil
	}

	payload := transcript.BuildTurnSummaryRecord(
		transcript.TurnSummary{
			Lane:           planExecuteLaneName,
			StopReason:     strings.TrimSpace(stopReason),
			DurationMillis: 0,
		},
		time.Now().UTC(),
		transcript.TurnSummaryRecordOptions{IncludeZeroBudgetLimits: true},
	)

	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	if _, err := fmt.Fprintln(stdout, string(body)); err != nil {
		return err
	}
	return nil
}
