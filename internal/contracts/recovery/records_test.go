package recovery

import (
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("Recovery failure records", func() {
	ginkgo.It("rejects an unknown source lane", func() {
		record := validFailureRecordForTest()
		record.SourceRun.Lane = "plan_executeish"
		gomega.Expect(ValidateFailureRecord(record)).To(gomega.MatchError(gomega.ContainSubstring("invalid source_run.lane")))
	})

	ginkgo.It("accepts a known source lane", func() {
		gomega.Expect(ValidateFailureRecord(validFailureRecordForTest())).To(gomega.Succeed())
	})

	ginkgo.It("preserves implementation state when projecting a decomposition packet", func() {
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
		gomega.Expect(record.Implementation).NotTo(gomega.BeNil())
		gomega.Expect(record.Implementation.StopReason).To(gomega.Equal("model_call_budget_exhausted"))
		gomega.Expect(record.Implementation.WriteReadyNudgeCount).To(gomega.Equal(1))
		gomega.Expect(record.Implementation.RemainingRequiredEditPaths).To(gomega.Equal([]string{
			"internal/runtime/agent/runner_core.go",
		}))
	})

	ginkgo.It("validates exact pending source ranges in completion recovery state", func() {
		state := &ImplementationState{CompletionRecovery: &ImplementationCompletionRecovery{
			TotalAttempts: 1,
			ActiveViolation: &ImplementationCompletionActiveViolation{
				InvariantID: "completion_gate.final_answer_acceptance", Class: "source_grounding", AttemptsSinceProgress: 1,
				PendingSourceBodyPaths: []string{"internal/runtime/agent/runner_core.go"},
				PendingSourceBodyReferences: []ImplementationSourceBodyReference{{
					Path: "internal/runtime/agent/runner_core.go", StartLine: 10, EndLine: 20,
				}},
			},
		}}
		gomega.Expect(ValidateImplementationState(state)).To(gomega.Succeed())

		state.CompletionRecovery.ActiveViolation.PendingSourceBodyReferences[0].StartLine = 30
		gomega.Expect(ValidateImplementationState(state)).To(gomega.MatchError(gomega.ContainSubstring("range 30-20")))
	})
})

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
