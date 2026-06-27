package recovery

import (
	"strings"
	"testing"
)

func TestValidateFailureRecordRejectsUnknownSourceLane(t *testing.T) {
	record := validFailureRecordForTest()
	record.SourceRun.Lane = "plan_executeish"

	err := ValidateFailureRecord(record)
	if err == nil || !strings.Contains(err.Error(), "invalid source_run.lane") {
		t.Fatalf("ValidateFailureRecord error = %v, want invalid source_run.lane", err)
	}
}

func TestValidateFailureRecordAcceptsKnownSourceLane(t *testing.T) {
	record := validFailureRecordForTest()

	if err := ValidateFailureRecord(record); err != nil {
		t.Fatalf("ValidateFailureRecord returned error: %v", err)
	}
}

func validFailureRecordForTest() FailureRecord {
	return FailureRecord{
		SchemaVersion: FailureRecordSchemaVersion,
		FailureID:     "failure-123",
		SourceRun: SourceRun{
			Lane:      string(SourceLanePlanExecute),
			SessionID: "session-123",
		},
		Failure: Failure{
			Class:   FailureClassPlannerBlocked,
			Summary: "planner blocked",
		},
		PartialState: PartialState{
			DirtyStatus: DirtyStatusNotChecked,
			ScopeStatus: ScopeStatusNotChecked,
		},
		NextAction: NextActionInspectFailure,
	}
}
