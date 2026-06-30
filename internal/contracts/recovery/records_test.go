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

func TestFailureRecordFromDecompositionPacketPreservesImplementationState(t *testing.T) {
	packet := DecompositionPacket{
		SchemaVersion: DecompositionPacketSchemaVersion,
		FailureID:     "failure-123",
		CreatedAt:     "2026-06-28T00:00:00Z",
		UpdatedAt:     "2026-06-28T00:01:00Z",
		SourceRun: SourceRun{
			Lane:      string(SourceLaneImplement),
			SessionID: "session-123",
		},
		Failure: Failure{
			Class:   FailureClassExecutorEdit,
			Summary: "implementation interrupted",
		},
		PartialState: PartialState{
			DirtyStatus: DirtyStatusNotChecked,
			ScopeStatus: ScopeStatusNotChecked,
		},
		Implementation: &ImplementationState{
			Phase:                      "implementation.target_write_ready",
			StopReason:                 "model_call_budget_exhausted",
			RemainingRequiredEditPaths: []string{"internal/runtime/agent/runner_core.go"},
			WriteReadyNudgeCount:       1,
		},
		NextAction: NextActionReviewDecomposition,
	}

	record := FailureRecordFromDecompositionPacket(packet)
	if record.Implementation == nil {
		t.Fatal("expected implementation state")
	}

	if record.Implementation.StopReason != "model_call_budget_exhausted" ||
		record.Implementation.WriteReadyNudgeCount != 1 ||
		len(record.Implementation.RemainingRequiredEditPaths) != 1 {

		t.Fatalf("implementation state not preserved: %#v", record.Implementation)
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
