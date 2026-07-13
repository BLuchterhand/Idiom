package planexecute

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	plancontract "idiom/internal/contracts/plan"
	recoverycontract "idiom/internal/contracts/recovery"
	"idiom/internal/contracts/workflowstate"
	"idiom/internal/workflows/recovery"
)

var _ = ginkgo.Describe("plan execute terminal completion record classification", func() {
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name         string
		state        ProgressState
		input        PlanExecuteTerminalCompletionInput
		wantClass    plancontract.PlanExecuteTerminalClass
		wantOutcome  plancontract.PlanExecuteTerminalOutcome
		wantWorkflow workflowstate.WorkflowID
		wantState    workflowstate.TerminalState
	}{
		{
			name: "completed",
			state: progressStateForTerminalCompletionTest(func(state *ProgressState) {
				state.Phase = plancontract.PlanExecuteProgressPhaseComplete
				state.Status = plancontract.PlanExecuteProgressStatusCompleted
			}),
			wantClass:    plancontract.PlanExecuteTerminalClassSuccess,
			wantOutcome:  plancontract.PlanExecuteTerminalOutcomeCompleted,
			wantWorkflow: workflowstate.WorkflowPlanExecuteExecutor,
			wantState:    workflowstate.TerminalStateComplete,
		},
		{
			name: "source satisfied explicit input",
			state: progressStateForTerminalCompletionTest(func(state *ProgressState) {
				state.Phase = plancontract.PlanExecuteProgressPhaseComplete
				state.Status = plancontract.PlanExecuteProgressStatusCompleted
			}),
			input: PlanExecuteTerminalCompletionInput{
				SourceSatisfied:              true,
				SourceSatisfiedEvidencePaths: []string{"pkg/feature.go", "pkg/feature.go", " "},
				CompletedAt:                  now,
			},
			wantClass:    plancontract.PlanExecuteTerminalClassSuccess,
			wantOutcome:  plancontract.PlanExecuteTerminalOutcomeSourceSatisfied,
			wantWorkflow: workflowstate.WorkflowPlanExecutePlanner,
			wantState:    workflowstate.TerminalStateSourceSatisfied,
		},
		{
			name: "review skipped by design",
			state: progressStateForTerminalCompletionTest(func(state *ProgressState) {
				state.MarkSkippedReview()
			}),
			wantClass:    plancontract.PlanExecuteTerminalClassSuccess,
			wantOutcome:  plancontract.PlanExecuteTerminalOutcomeReviewSkippedByDesign,
			wantWorkflow: workflowstate.WorkflowPlanExecuteExecutor,
			wantState:    workflowstate.TerminalStateComplete,
		},
		{
			name: "review degraded",
			state: progressStateForTerminalCompletionTest(func(state *ProgressState) {
				state.MarkReviewComplete(ReviewRepairPhaseReport{Status: plancontract.PlanExecuteReviewStatusDegraded, StatusDetail: "review unavailable"}, nil)
			}),
			wantClass:    plancontract.PlanExecuteTerminalClassRecoveryRequired,
			wantOutcome:  plancontract.PlanExecuteTerminalOutcomeReviewDegraded,
			wantWorkflow: workflowstate.WorkflowPlanExecuteExecutor,
			wantState:    workflowstate.TerminalStateRecoveryOpen,
		},
		{
			name: "blocked",
			state: progressStateForTerminalCompletionTest(func(state *ProgressState) {
				state.MarkReviewComplete(ReviewRepairPhaseReport{Status: plancontract.PlanExecuteReviewStatusBlocked, StatusDetail: "missing approval"}, nil)
			}),
			wantClass:    plancontract.PlanExecuteTerminalClassRecoveryRequired,
			wantOutcome:  plancontract.PlanExecuteTerminalOutcomeBlocked,
			wantWorkflow: workflowstate.WorkflowPlanExecuteExecutor,
			wantState:    workflowstate.TerminalStateRecoveryOpen,
		},
		{
			name: "failed",
			state: progressStateForTerminalCompletionTest(func(state *ProgressState) {
				state.MarkPlanningFailed("planner_failed", "planner model unavailable")
			}),
			wantClass:    plancontract.PlanExecuteTerminalClassFailed,
			wantOutcome:  plancontract.PlanExecuteTerminalOutcomeFailed,
			wantWorkflow: workflowstate.WorkflowPlanExecutePlanner,
			wantState:    workflowstate.TerminalStateBlocked,
		},
		{
			name: "missing requested test edit stays planner owned",
			state: progressStateForTerminalCompletionTest(func(state *ProgressState) {
				state.MarkPlanningFailed(string(plancontract.PlanExecuteFailureMissingRequestedTestEdit), "missing requested test edit")
			}),
			wantClass:    plancontract.PlanExecuteTerminalClassFailed,
			wantOutcome:  plancontract.PlanExecuteTerminalOutcomeFailed,
			wantWorkflow: workflowstate.WorkflowPlanExecutePlanner,
			wantState:    workflowstate.TerminalStateBlocked,
		},
		{
			name: "missing requested implementation edit stays planner owned",
			state: progressStateForTerminalCompletionTest(func(state *ProgressState) {
				state.MarkPlanningFailed(string(plancontract.PlanExecuteFailureMissingRequestedImplementationEdit), "missing requested implementation edit")
			}),
			wantClass:    plancontract.PlanExecuteTerminalClassFailed,
			wantOutcome:  plancontract.PlanExecuteTerminalOutcomeFailed,
			wantWorkflow: workflowstate.WorkflowPlanExecutePlanner,
			wantState:    workflowstate.TerminalStateBlocked,
		},
	}

	for _, tt := range tests {
		tt := tt
		ginkgo.It(tt.name, func() {
			record := NewPlanExecuteTerminalCompletionRecord(tt.state, tt.input)
			gomega.Expect(record.TerminalClass).To(gomega.Equal(tt.wantClass), fmt.Sprintf("terminal classification = class %q outcome %q, want class %q outcome %q; record=%#v", record.TerminalClass, record.Outcome, tt.wantClass, tt.wantOutcome, record))
			gomega.Expect(record.Outcome).To(gomega.Equal(tt.wantOutcome), fmt.Sprintf("terminal classification = class %q outcome %q, want class %q outcome %q; record=%#v", record.TerminalClass, record.Outcome, tt.wantClass, tt.wantOutcome, record))
			gomega.Expect(record.WorkflowID).To(gomega.Equal(tt.wantWorkflow), fmt.Sprintf("workflow classification = workflow %q state %q dispatchable=%v, want workflow %q state %q dispatchable=false; record=%#v", record.WorkflowID, record.WorkflowTerminalState, record.Dispatchable, tt.wantWorkflow, tt.wantState, record))
			gomega.Expect(record.WorkflowTerminalState).To(gomega.Equal(tt.wantState), fmt.Sprintf("workflow classification = workflow %q state %q dispatchable=%v, want workflow %q state %q dispatchable=false; record=%#v", record.WorkflowID, record.WorkflowTerminalState, record.Dispatchable, tt.wantWorkflow, tt.wantState, record))
			gomega.Expect(record.Dispatchable).To(gomega.BeFalse(), fmt.Sprintf("workflow classification = workflow %q state %q dispatchable=%v, want workflow %q state %q dispatchable=false; record=%#v", record.WorkflowID, record.WorkflowTerminalState, record.Dispatchable, tt.wantWorkflow, tt.wantState, record))
			gomega.Expect(record.SchemaVersion).To(gomega.Equal(plancontract.PlanExecuteTerminalCompletionSchemaVersion), fmt.Sprintf("record schema/type missing: %#v", record))
			gomega.Expect(record.Type).To(gomega.Equal(plancontract.PlanExecuteTerminalCompletionRecordType), fmt.Sprintf("record schema/type missing: %#v", record))

		})
	}
})

var _ = ginkgo.It("normalize plan execute terminal completion record defaults and trims", func() {
	record := plancontract.NormalizePlanExecuteTerminalCompletionRecord(plancontract.PlanExecuteTerminalCompletionRecord{
		SessionID:                    " session-1 ",
		Status:                       " complete ",
		SourceSatisfiedEvidencePaths: []string{" a.go ", "", "a.go", "../escape.go"},
		ChangedPaths:                 []string{` b.go `, `b.go`, `pkg\feature.go`, "pkg/./bad.go"},
	})
	gomega.Expect(record.SchemaVersion).To(gomega.Equal(plancontract.PlanExecuteTerminalCompletionSchemaVersion), fmt.Sprintf("normalizer should fill schema/type, got %#v", record))
	gomega.Expect(record.Type).To(gomega.Equal(plancontract.PlanExecuteTerminalCompletionRecordType), fmt.Sprintf("normalizer should fill schema/type, got %#v", record))
	gomega.Expect(record.SessionID != "session-1").To(gomega.BeFalse(), fmt.Sprintf("SessionID = %q, want trimmed session-1", record.SessionID))
	gomega.Expect(record.TerminalClass).To(gomega.Equal(plancontract.PlanExecuteTerminalClassSuccess), fmt.Sprintf("classification = class %q outcome %q, want completed/completed", record.TerminalClass, record.Outcome))
	gomega.Expect(record.Outcome).To(gomega.Equal(plancontract.PlanExecuteTerminalOutcomeCompleted), fmt.Sprintf("classification = class %q outcome %q, want completed/completed", record.TerminalClass, record.Outcome))
	gomega.Expect(record.WorkflowID).To(gomega.Equal(workflowstate.WorkflowPlanExecuteExecutor), fmt.Sprintf("workflow classification = workflow %q state %q dispatchable=%v, want executor complete false", record.WorkflowID, record.WorkflowTerminalState, record.Dispatchable))
	gomega.Expect(record.WorkflowTerminalState).To(gomega.Equal(workflowstate.TerminalStateComplete), fmt.Sprintf("workflow classification = workflow %q state %q dispatchable=%v, want executor complete false", record.WorkflowID, record.WorkflowTerminalState, record.Dispatchable))
	gomega.Expect(record.Dispatchable).To(gomega.BeFalse(), fmt.Sprintf("workflow classification = workflow %q state %q dispatchable=%v, want executor complete false", record.WorkflowID, record.WorkflowTerminalState, record.Dispatchable))
	gomega.Expect(record.SourceSatisfiedEvidencePaths).To(gomega.HaveLen(1), fmt.Sprintf("source evidence paths not normalized: %#v", record.SourceSatisfiedEvidencePaths))
	gomega.Expect(record.SourceSatisfiedEvidencePaths[0]).To(gomega.Equal("a.go"), fmt.Sprintf("source evidence paths not normalized: %#v", record.SourceSatisfiedEvidencePaths))
	gomega.Expect(record.ChangedPaths).To(gomega.HaveLen(2), fmt.Sprintf("changed paths not normalized: %#v", record.ChangedPaths))
	gomega.Expect(record.ChangedPaths[0]).To(gomega.Equal("b.go"), fmt.Sprintf("changed paths not normalized: %#v", record.ChangedPaths))
	gomega.Expect(record.ChangedPaths[1]).To(gomega.Equal("pkg/feature.go"), fmt.Sprintf("changed paths not normalized: %#v", record.ChangedPaths))
})

var _ = ginkgo.It("normalize plan execute terminal completion record reclassifies contradictions", func() {
	record := plancontract.NormalizePlanExecuteTerminalCompletionRecord(plancontract.PlanExecuteTerminalCompletionRecord{
		TerminalClass: plancontract.PlanExecuteTerminalClassSuccess,
		Outcome:       plancontract.PlanExecuteTerminalOutcomeCompleted,
		Status:        plancontract.PlanExecuteTerminalStatusCompleted,
		FailureKind:   "review_model_error",
		FailureReason: "review model returned invalid JSON",
	})
	gomega.Expect(record.TerminalClass).To(gomega.Equal(plancontract.PlanExecuteTerminalClassFailed), fmt.Sprintf("classification = class %q outcome %q, want failed/failed", record.TerminalClass, record.Outcome))
	gomega.Expect(record.Outcome).To(gomega.Equal(plancontract.PlanExecuteTerminalOutcomeFailed), fmt.Sprintf("classification = class %q outcome %q, want failed/failed", record.TerminalClass, record.Outcome))
	gomega.Expect(record.WorkflowTerminalState != workflowstate.TerminalStateBlocked).To(gomega.BeFalse(), fmt.Sprintf("workflow terminal state = %q, want blocked", record.WorkflowTerminalState))
})

var _ = ginkgo.It("plan execute workflow classification uses typed failure kind mapping", func() {
	record := plancontract.NormalizePlanExecuteTerminalCompletionRecord(plancontract.PlanExecuteTerminalCompletionRecord{
		Phase:         plancontract.PlanExecuteProgressPhaseExecutor,
		Status:        plancontract.PlanExecuteTerminalStatusFailed,
		TerminalClass: plancontract.PlanExecuteTerminalClassFailed,
		Outcome:       plancontract.PlanExecuteTerminalOutcomeFailed,
		FailureKind:   plancontract.PlanExecuteFailureMissingEditPathWithoutNewFileAuth,
	})
	gomega.Expect(record.WorkflowID).To(gomega.Equal(workflowstate.WorkflowPlanExecutePlanner), fmt.Sprintf("typed workflow classification = workflow %q state %q, want planner missing-scope: %#v", record.WorkflowID, record.WorkflowTerminalState, record))
	gomega.Expect(record.WorkflowTerminalState).To(gomega.Equal(workflowstate.TerminalStateMissingScope), fmt.Sprintf("typed workflow classification = workflow %q state %q, want planner missing-scope: %#v", record.WorkflowID, record.WorkflowTerminalState, record))
})

var _ = ginkgo.It("plan execute terminal completion record from fields normalizes state path", func() {
	record := plancontract.NormalizePlanExecuteTerminalCompletionRecord(plancontract.PlanExecuteTerminalCompletionRecordFromFields(map[string]any{
		"state_path":            ".idiom/plan-execute/sessions/field-test.json",
		"status":                "completed",
		"review_status":         "skipped",
		"review_repair_skipped": true,
		"changed_paths":         []any{"pkg/feature.go", "pkg/feature.go"},
	}))
	gomega.Expect(record.ProgressPath != ".idiom/plan-execute/sessions/field-test.json").To(gomega.BeFalse(), fmt.Sprintf("ProgressPath = %q, want state_path fallback", record.ProgressPath))
	gomega.Expect(record.TerminalClass).To(gomega.Equal(plancontract.PlanExecuteTerminalClassSuccess), fmt.Sprintf("classification = class %q outcome %q, want skipped-by-design success", record.TerminalClass, record.Outcome))
	gomega.Expect(record.Outcome).To(gomega.Equal(plancontract.PlanExecuteTerminalOutcomeReviewSkippedByDesign), fmt.Sprintf("classification = class %q outcome %q, want skipped-by-design success", record.TerminalClass, record.Outcome))
	gomega.Expect(record.ChangedPaths).To(gomega.HaveLen(1), fmt.Sprintf("ChangedPaths = %#v, want deduped path", record.ChangedPaths))
	gomega.Expect(record.ChangedPaths[0]).To(gomega.Equal("pkg/feature.go"), fmt.Sprintf("ChangedPaths = %#v, want deduped path", record.ChangedPaths))
})

var _ = ginkgo.It("plan execute terminal completion record from fields preserves parent authority", func() {
	record := plancontract.NormalizePlanExecuteTerminalCompletionRecord(plancontract.PlanExecuteTerminalCompletionRecordFromFields(map[string]any{
		"status":        "completed",
		"phase":         "complete",
		"contract_id":   "parent-contract",
		"snapshot_hash": "parent-snapshot",
		"contract_hash": "legacy-snapshot",
	}))
	gomega.Expect(record.ContractID != "parent-contract").To(gomega.BeFalse(), fmt.Sprintf("ContractID = %q, want parent-contract", record.ContractID))
	gomega.Expect(record.SnapshotHash != "parent-snapshot").To(gomega.BeFalse(), fmt.Sprintf("SnapshotHash = %q, want parent-snapshot", record.SnapshotHash))
	gomega.Expect(record.ContractHash != "legacy-snapshot").To(gomega.BeFalse(), fmt.Sprintf("ContractHash = %q, want legacy-snapshot diagnostic preserved", record.ContractHash))
})

var _ = ginkgo.It("plan execute terminal completion record from progress state requires terminal state", func() {
	running := progressStateForTerminalCompletionTest(func(state *ProgressState) {
		state.Status = plancontract.PlanExecuteProgressStatusRunning
		state.Phase = plancontract.PlanExecuteProgressPhaseExecutor
	})
	if _, ok := PlanExecuteTerminalCompletionRecordFromProgressState(running, PlanExecuteTerminalCompletionInput{}); ok {
		ginkgo.Fail(fmt.Sprintf("running progress must not synthesize terminal completion"))
	}

	completed := progressStateForTerminalCompletionTest(func(state *ProgressState) {
		state.Status = plancontract.PlanExecuteProgressStatusCompleted
		state.Phase = plancontract.PlanExecuteProgressPhaseComplete
	})
	record, ok := PlanExecuteTerminalCompletionRecordFromProgressState(completed, PlanExecuteTerminalCompletionInput{ProgressPath: "progress.json"})
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("completed progress should synthesize terminal completion"))
	gomega.Expect(record.ProgressPath).To(gomega.Equal("progress.json"), fmt.Sprintf("record = %#v, want terminal success with progress path", record))
	gomega.Expect(record.TerminalClass).To(gomega.Equal(plancontract.PlanExecuteTerminalClassSuccess), fmt.Sprintf("record = %#v, want terminal success with progress path", record))
})

var _ = ginkgo.It("terminal completion record includes mechanical evidence", func() {
	state := progressStateForTerminalCompletionTest(func(state *ProgressState) {
		state.MarkSkippedReview()

		state.RequiredEditPaths = []string{"pkg/feature.go"}
		state.ScopeContractID = "scope-123"
		state.ScopeContractHash = "scope-123"
		state.Executor.SourceSanity = []plancontract.PhaseEvidenceCheck{{
			Kind:   "source_sanity",
			Path:   "pkg/feature.go",
			Status: "ok",
		}}
		state.Executor.VerificationCommands = []plancontract.VerificationCommand{{
			Command: "go test ./pkg",
			Argv:    []string{"go", "test", "./pkg"},
			Source:  plancontract.VerificationCommandSourceInferred,
		}}
		state.Executor.Verification = []plancontract.PhaseEvidenceCheck{{
			Kind:    "verification",
			Command: "go test ./pkg",
			Status:  "ok",
		}}
		state.Executor.VerificationSkips = []plancontract.VerificationSkip{{Path: "README.md", Reason: plancontract.VerificationSkipNonGoPath}}
		state.Executor.VerificationRejects = []plancontract.VerificationReject{{Command: "go test ./pkg; rm -rf /", Reason: plancontract.VerificationRejectUnsupportedCmd}}
	})

	record := NewPlanExecuteTerminalCompletionRecord(state, PlanExecuteTerminalCompletionInput{})
	gomega.Expect(record.ScopeContractID).To(gomega.Equal("scope-123"), fmt.Sprintf("scope contract metadata missing: %#v", record))
	gomega.Expect(record.ScopeContractHash).To(gomega.Equal("scope-123"), fmt.Sprintf("scope contract metadata missing: %#v", record))
	gomega.Expect(record.ProtocolHash != "scope-123").To(gomega.BeFalse(), fmt.Sprintf("protocol hash missing: %#v", record))
	gomega.Expect(record.SourceSanity).To(gomega.HaveLen(1), fmt.Sprintf("mechanical evidence missing: source=%#v commands=%#v verification=%#v", record.SourceSanity, record.VerificationCommands, record.Verification))
	gomega.Expect(record.Verification).To(gomega.HaveLen(1), fmt.Sprintf("mechanical evidence missing: source=%#v commands=%#v verification=%#v", record.SourceSanity, record.VerificationCommands, record.Verification))
	gomega.Expect(record.VerificationCommands).To(gomega.HaveLen(1), fmt.Sprintf("mechanical evidence missing: source=%#v commands=%#v verification=%#v", record.SourceSanity, record.VerificationCommands, record.Verification))
	gomega.Expect(record.VerificationSkips).To(gomega.HaveLen(1), fmt.Sprintf("verification skips/rejects missing: skips=%#v rejects=%#v", record.VerificationSkips, record.VerificationRejects))
	gomega.Expect(record.VerificationRejects).To(gomega.HaveLen(1), fmt.Sprintf("verification skips/rejects missing: skips=%#v rejects=%#v", record.VerificationSkips, record.VerificationRejects))
	gomega.Expect(record.RequiredPathCoverage).To(gomega.HaveLen(1), fmt.Sprintf("required path coverage missing: %#v", record.RequiredPathCoverage))
	gomega.Expect(record.RequiredPathCoverage[0].Status).To(gomega.Equal(plancontract.PlanExecuteRequiredPathCoverageChanged), fmt.Sprintf("required path coverage missing: %#v", record.RequiredPathCoverage))
	gomega.Expect(len(record.FailureInvariants) == 0).To(gomega.BeFalse(), fmt.Sprintf("failure invariants must be recorded"))
	gomega.Expect(planExecuteTerminalStringSliceContains(record.ValidationFailures, "verification_rejected:go test ./pkg; rm -rf /")).To(gomega.BeTrue(), fmt.Sprintf("verification reject must prevent success, got %#v", record.ValidationFailures))
})

var _ = ginkgo.It("terminal completion uses changed paths as legacy envelope only without declared scope", func() {
	record := plancontract.NormalizePlanExecuteTerminalCompletionRecord(plancontract.PlanExecuteTerminalCompletionRecord{
		Status:              plancontract.PlanExecuteTerminalStatusCompleted,
		Phase:               plancontract.PlanExecuteProgressPhaseComplete,
		ReviewStatus:        plancontract.PlanExecuteReviewStatusSkipped,
		ChangedPaths:        []string{"pkg/feature.go"},
		SourceSanity:        []plancontract.PhaseEvidenceCheck{{Kind: "source_sanity", Path: "pkg/feature.go", Status: "ok"}},
		Verification:        []plancontract.PhaseEvidenceCheck{{Kind: "verification", Command: "go test ./pkg", Status: "ok"}},
		ReviewRepairSkipped: true,
	})
	gomega.Expect(planExecuteTerminalStringSliceContains(record.ValidationFailures, "changed_path_outside_write_envelope:pkg/feature.go")).To(gomega.BeFalse(), fmt.Sprintf("legacy no-scope completion should use changed paths as runtime envelope, got %#v", record.ValidationFailures))

	scoped := plancontract.NormalizePlanExecuteTerminalCompletionRecord(plancontract.PlanExecuteTerminalCompletionRecord{
		Status:              plancontract.PlanExecuteTerminalStatusCompleted,
		Phase:               plancontract.PlanExecuteProgressPhaseComplete,
		ReviewStatus:        plancontract.PlanExecuteReviewStatusSkipped,
		RequiredEditPaths:   []string{"pkg/expected.go"},
		ChangedPaths:        []string{"pkg/feature.go"},
		SourceSanity:        []plancontract.PhaseEvidenceCheck{{Kind: "source_sanity", Path: "pkg/feature.go", Status: "ok"}},
		ReviewRepairSkipped: true,
	})
	gomega.Expect(planExecuteTerminalStringSliceContains(scoped.ValidationFailures, "changed_path_outside_write_envelope:pkg/feature.go")).To(gomega.BeTrue(), fmt.Sprintf("declared scope should still reject out-of-envelope changed path, got %#v", scoped.ValidationFailures))
})

var _ = ginkgo.It("finalize terminal completion rejects status only success", func() {
	root := ginkgo.GinkgoT().TempDir()
	state := NewProgressState("status_only_success", "prompt", "", nil, nil, time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC))

	state.Phase = plancontract.PlanExecuteProgressPhaseComplete
	state.Status = plancontract.PlanExecuteProgressStatusCompleted

	result := finalizePlanExecuteRunResult(root, state, ".idiom/plan-execute/sessions/status_only_success.json", false, io.Discard, planExecuteRunResultInput{ExitCode: 0})
	gomega.Expect(result.ExitCode != 1).To(gomega.BeFalse(), fmt.Sprintf("ExitCode = %d, want terminal contract failure", result.ExitCode))
	gomega.Expect(result.FailureKind != string(plancontract.PlanExecuteFailureTerminalCompletionContractFailed)).To(gomega.BeFalse(), fmt.Sprintf("FailureKind = %q, want %q", result.FailureKind, string(plancontract.PlanExecuteFailureTerminalCompletionContractFailed)))
	gomega.Expect(result.Completion).NotTo(gomega.BeNil(), fmt.Sprintf("completion was not downgraded to failed: %#v", result.Completion))
	gomega.Expect(result.Completion.TerminalClass).To(gomega.Equal(plancontract.PlanExecuteTerminalClassFailed), fmt.Sprintf("completion was not downgraded to failed: %#v", result.Completion))
	gomega.Expect(planExecuteTerminalStringSliceContains(result.Completion.ValidationFailures, "changed_paths_missing")).To(gomega.BeTrue(), fmt.Sprintf("validation failures missing changed_paths_missing: %#v", result.Completion.ValidationFailures))
})

var _ = ginkgo.It("finalize terminal completion success does not carry stale recovery metadata", func() {
	root := ginkgo.GinkgoT().TempDir()
	sessionID := "stale_recovery_success"
	if _, _, err := recovery.WritePlanExecuteFailureRecord(root, recovery.PlanExecuteFailureInput{
		SessionID:         sessionID,
		FailureKind:       string(plancontract.PlanExecuteFailurePlannerBlocked),
		FailureReason:     "<<QUESTION>>: Plan-execute anchor refresh is still required before a corrected submit_plan can be accepted.\n- pkg/feature.go",
		RequiredEditPaths: []string{"pkg/feature.go"},
	}, time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)); err != nil {
		ginkgo.Fail(fmt.Sprintf("write stale recovery record: %v", err))
	}

	state := NewProgressState(sessionID, "prompt", "", []string{"pkg/feature.go"}, nil, time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC))
	state.Phase = plancontract.PlanExecuteProgressPhaseComplete
	state.Status = plancontract.PlanExecuteProgressStatusCompleted
	state.ChangedPaths = []string{"pkg/feature.go"}
	state.Executor.SourceSanity = []plancontract.PhaseEvidenceCheck{{Kind: "source_sanity", Path: "pkg/feature.go", Status: "ok"}}
	state.Executor.Verification = []plancontract.PhaseEvidenceCheck{{Kind: "verification", Command: "go test ./pkg", Status: "ok"}}
	state.Review.Status = plancontract.PlanExecuteReviewStatusSkipped

	result := finalizePlanExecuteRunResult(root, state, ".idiom/plan-execute/sessions/stale_recovery_success.json", false, io.Discard, planExecuteRunResultInput{ExitCode: 0})
	gomega.Expect(result.ExitCode != 0).To(gomega.BeFalse(), fmt.Sprintf("ExitCode = %d, want success; failure=%s %s", result.ExitCode, result.FailureKind, result.FailureReason))
	gomega.Expect(result.Completion).NotTo(gomega.BeNil(), fmt.Sprint("expected terminal completion"))
	gomega.Expect(result.Completion.NextAction).To(gomega.Equal(recoverycontract.NextAction("")), fmt.Sprintf("success completion carried stale recovery metadata: %#v", result.Completion))
	gomega.Expect(result.Completion.FailureID).To(gomega.Equal(""), fmt.Sprintf("success completion carried stale recovery metadata: %#v", result.Completion))
	gomega.Expect(result.Completion.RecordPath).To(gomega.Equal(""), fmt.Sprintf("success completion carried stale recovery metadata: %#v", result.Completion))
	gomega.Expect(result.Completion.PacketPath).To(gomega.Equal(""), fmt.Sprintf("success completion carried stale recovery metadata: %#v", result.Completion))
})

var _ = ginkgo.It("finalize terminal completion rejects missing source sanity for source change", func() {
	root := ginkgo.GinkgoT().TempDir()
	state := NewProgressState("missing_source_sanity", "prompt", "", []string{"pkg/feature.go"}, nil, time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC))

	state.Phase = plancontract.PlanExecuteProgressPhaseComplete
	state.Status = plancontract.PlanExecuteProgressStatusCompleted
	state.ChangedPaths = []string{"pkg/feature.go"}
	state.RequiredAcceptanceEvidenceIDs = []string{"req-1"}
	state.Review.Status = plancontract.PlanExecuteReviewStatusSkipped

	result := finalizePlanExecuteRunResult(root, state, ".idiom/plan-execute/sessions/missing_source_sanity.json", false, io.Discard, planExecuteRunResultInput{ExitCode: 0})
	gomega.Expect(result.ExitCode != 1).To(gomega.BeFalse(), fmt.Sprintf("ExitCode = %d, want terminal contract failure", result.ExitCode))
	gomega.Expect(result.FailureKind != string(plancontract.PlanExecuteFailureTerminalCompletionContractFailed)).To(gomega.BeFalse(), fmt.Sprintf("FailureKind = %q, want %q", result.FailureKind, plancontract.PlanExecuteFailureTerminalCompletionContractFailed))
	gomega.Expect(result.Completion).NotTo(gomega.BeNil(), fmt.Sprintf("completion was not downgraded to failed: %#v", result.Completion))
	gomega.Expect(result.Completion.TerminalClass).To(gomega.Equal(plancontract.PlanExecuteTerminalClassFailed), fmt.Sprintf("completion was not downgraded to failed: %#v", result.Completion))
	gomega.Expect(planExecuteTerminalStringSliceContains(result.Completion.ValidationFailures, "source_sanity_missing:pkg/feature.go")).To(gomega.BeTrue(), fmt.Sprintf("validation failures missing source_sanity_missing: %#v", result.Completion.ValidationFailures))

	loaded, _, found, err := TryLoadPlanExecuteTerminalCompletionRecord(root, "missing_source_sanity")
	gomega.Expect(err).To(gomega.BeNil(), fmt.Sprintf("TryLoadPlanExecuteTerminalCompletionRecord found=%v err=%v", found, err))
	gomega.Expect(found).To(gomega.BeTrue(), fmt.Sprintf("TryLoadPlanExecuteTerminalCompletionRecord found=%v err=%v", found, err))
	gomega.Expect(planExecuteTerminalStringSliceContains(loaded.ValidationFailures, "source_sanity_missing:pkg/feature.go")).To(gomega.BeTrue(), fmt.Sprintf("saved terminal completion lost validation failure: %#v", loaded.ValidationFailures))

	fields := plancontract.PlanExecuteTerminalCompletionEventFields(*result.Completion, result.CompletionPath)
	fieldFailures, ok := fields["validation_failures"].([]any)
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("terminal completion event lost validation failure: %#v", fields["validation_failures"]))
	gomega.Expect(planExecuteTerminalAnySliceContains(fieldFailures, "source_sanity_missing:pkg/feature.go")).To(gomega.BeTrue(), fmt.Sprintf("terminal completion event lost validation failure: %#v", fields["validation_failures"]))
	gomega.Expect(len(result.Completion.AcceptanceEvidenceIDs) > 0).To(gomega.BeFalse(), fmt.Sprintf("downgraded terminal completion should not retain acceptance IDs: %#v", result.Completion.AcceptanceEvidenceIDs))
})

var _ = ginkgo.It("finalize terminal completion rejects missing cpp source sanity", func() {
	root := ginkgo.GinkgoT().TempDir()
	state := NewProgressState("missing_cpp_source_sanity", "prompt", "", []string{"native/src/machine.cpp"}, nil, time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC))

	state.Phase = plancontract.PlanExecuteProgressPhaseComplete
	state.Status = plancontract.PlanExecuteProgressStatusCompleted
	state.ChangedPaths = []string{"native/src/machine.cpp"}
	state.RequiredAcceptanceEvidenceIDs = []string{"req-1"}
	state.Review.Status = plancontract.PlanExecuteReviewStatusSkipped

	result := finalizePlanExecuteRunResult(root, state, ".idiom/plan-execute/sessions/missing_cpp_source_sanity.json", false, io.Discard, planExecuteRunResultInput{ExitCode: 0})
	gomega.Expect(result.ExitCode != 1).To(gomega.BeFalse(), fmt.Sprintf("ExitCode = %d, want terminal contract failure", result.ExitCode))
	gomega.Expect(result.Completion).NotTo(gomega.BeNil(), fmt.Sprintf("completion was not downgraded to failed: %#v", result.Completion))
	gomega.Expect(result.Completion.TerminalClass).To(gomega.Equal(plancontract.PlanExecuteTerminalClassFailed), fmt.Sprintf("completion was not downgraded to failed: %#v", result.Completion))
	gomega.Expect(planExecuteTerminalStringSliceContains(result.Completion.ValidationFailures, "source_sanity_missing:native/src/machine.cpp")).To(gomega.BeTrue(), fmt.Sprintf("validation failures missing source_sanity_missing for C++ path: %#v", result.Completion.ValidationFailures))
	gomega.Expect(len(result.Completion.AcceptanceEvidenceIDs) > 0).To(gomega.BeFalse(), fmt.Sprintf("downgraded terminal completion should not retain acceptance IDs: %#v", result.Completion.AcceptanceEvidenceIDs))
})

var _ = ginkgo.It("finalize terminal completion carries acceptance evidence i ds", func() {
	root := ginkgo.GinkgoT().TempDir()
	state := NewProgressState("acceptance_ids", "prompt", "", []string{"pkg/feature.go"}, nil, time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC))

	state.Phase = plancontract.PlanExecuteProgressPhaseComplete
	state.Status = plancontract.PlanExecuteProgressStatusCompleted
	state.ChangedPaths = []string{"pkg/feature.go"}
	state.RequiredAcceptanceEvidenceIDs = []string{"req-1", "adr-1"}
	state.AcceptanceEvidenceIDs = []string{"req-1", "Req-1", "adr-1"}
	state.Review.Status = plancontract.PlanExecuteReviewStatusSkipped
	state.Executor.SourceSanity = []plancontract.PhaseEvidenceCheck{{Kind: "source_sanity", Path: "pkg/feature.go", Status: "ok"}}

	result := finalizePlanExecuteRunResult(root, state, ".idiom/plan-execute/sessions/acceptance_ids.json", false, io.Discard, planExecuteRunResultInput{ExitCode: 0})
	gomega.Expect(result.ExitCode != 0).To(gomega.BeFalse(), fmt.Sprintf("ExitCode = %d, want success; completion=%#v", result.ExitCode, result.Completion))
	gomega.Expect(result.Completion).NotTo(gomega.BeNil(), fmt.Sprintf("completion missing"))

	want := []string{"adr-1", "req-1"}
	gomega.Expect(planExecuteTerminalStringSlicesEqual(result.Completion.AcceptanceEvidenceIDs, want)).To(gomega.BeTrue(), fmt.Sprintf("AcceptanceEvidenceIDs = %#v, want %#v", result.Completion.AcceptanceEvidenceIDs, want))
	gomega.Expect(planExecuteTerminalStringSlicesEqual(result.Completion.RequiredAcceptanceEvidenceIDs, want)).To(gomega.BeTrue(), fmt.Sprintf("RequiredAcceptanceEvidenceIDs = %#v, want %#v", result.Completion.RequiredAcceptanceEvidenceIDs, want))
})

var _ = ginkgo.It("terminal completion carries acceptance evidence claims into validation", func() {
	record := plancontract.NormalizePlanExecuteTerminalCompletionRecord(plancontract.PlanExecuteTerminalCompletionRecord{
		SessionID:         "acceptance_claims",
		Phase:             plancontract.PlanExecuteProgressPhaseComplete,
		Status:            plancontract.PlanExecuteTerminalStatusCompleted,
		ReviewStatus:      plancontract.PlanExecuteReviewStatusSkipped,
		ExitCode:          0,
		RequiredEditPaths: []string{"pkg/feature.go"},
		WriteAllowedPaths: []string{"pkg/feature.go"},
		ChangedPaths:      []string{"pkg/feature.go"},
		SourceSanity:      []plancontract.PhaseEvidenceCheck{{Kind: "source_sanity", Path: "pkg/feature.go", Status: "ok"}},
		RequiredAcceptanceEvidence: []plancontract.AcceptanceEvidenceRequired{{
			ID:        "coverage-threshold",
			Required:  true,
			Threshold: ">=80%",
			Source:    "test",
		}},
		AcceptanceEvidenceClaims: []plancontract.AcceptanceEvidenceClaim{{
			ID:    "coverage-threshold",
			Value: "72%",
			Met:   true,
		}},
	})
	gomega.Expect(planExecuteTerminalStringSliceContains(record.RequiredAcceptanceEvidenceIDs, "coverage-threshold")).To(gomega.BeTrue(), fmt.Sprintf("required threshold obligation ID was not mirrored into required IDs: %#v", record.RequiredAcceptanceEvidenceIDs))
	gomega.Expect(record.AcceptanceEvidenceClaims).To(gomega.HaveLen(1), fmt.Sprintf("acceptance evidence claims not normalized on record: %#v", record.AcceptanceEvidenceClaims))
	gomega.Expect(record.AcceptanceEvidenceClaims[0].ID).To(gomega.Equal("coverage-threshold"), fmt.Sprintf("acceptance evidence claims not normalized on record: %#v", record.AcceptanceEvidenceClaims))
	gomega.Expect(planExecuteTerminalStringSliceContains(record.ValidationFailures, "acceptance_evidence_claim_met_mismatch")).To(gomega.BeTrue(), fmt.Sprintf("terminal validation did not evaluate threshold claim: %#v", record.ValidationFailures))
	gomega.Expect(planExecuteTerminalStringSliceContains(record.ValidationFailures, "acceptance_evidence_threshold_not_met")).To(gomega.BeTrue(), fmt.Sprintf("terminal validation did not evaluate threshold claim: %#v", record.ValidationFailures))

	fields := plancontract.PlanExecuteTerminalCompletionEventFields(record, ".idiom/plan-execute/terminal/acceptance_claims.json")
	if _, ok := fields["acceptance_evidence_claims"]; !ok {
		ginkgo.Fail(fmt.Sprintf("terminal event fields omitted acceptance evidence claims: %#v", fields))
	}
})

var _ = ginkgo.It("finalize terminal completion rejects missing required acceptance i ds", func() {
	root := ginkgo.GinkgoT().TempDir()
	state := NewProgressState("required_acceptance_ids", "prompt", "", []string{"pkg/feature.go"}, nil, time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC))

	state.Phase = plancontract.PlanExecuteProgressPhaseComplete
	state.Status = plancontract.PlanExecuteProgressStatusCompleted
	state.ChangedPaths = []string{"pkg/feature.go"}
	state.RequiredAcceptanceEvidenceIDs = []string{"req-1", "adr-1"}
	state.Review.Status = plancontract.PlanExecuteReviewStatusSkipped
	state.Executor.SourceSanity = []plancontract.PhaseEvidenceCheck{{Kind: "source_sanity", Path: "pkg/feature.go", Status: "ok"}}

	result := finalizePlanExecuteRunResult(root, state, ".idiom/plan-execute/sessions/required_acceptance_ids.json", false, io.Discard, planExecuteRunResultInput{ExitCode: 0})
	gomega.Expect(result.ExitCode != 1).To(gomega.BeFalse(), fmt.Sprintf("ExitCode = %d, want terminal contract failure; completion=%#v", result.ExitCode, result.Completion))
	gomega.Expect(result.Completion).NotTo(gomega.BeNil(), fmt.Sprintf("completion missing"))
	gomega.Expect(result.Completion.TerminalClass != plancontract.PlanExecuteTerminalClassFailed).To(gomega.BeFalse(), fmt.Sprintf("TerminalClass = %q, want failed", result.Completion.TerminalClass))
	gomega.Expect(planExecuteTerminalStringSliceContains(result.Completion.ValidationFailures, "acceptance_evidence_missing")).To(gomega.BeTrue(), fmt.Sprintf("validation failures missing acceptance_evidence_missing: %#v", result.Completion.ValidationFailures))
	gomega.Expect(len(result.Completion.AcceptanceEvidenceIDs) > 0).To(gomega.BeFalse(), fmt.Sprintf("RequiredAcceptanceEvidenceIDs alone must not populate AcceptanceEvidenceIDs: got %#v", result.Completion.AcceptanceEvidenceIDs))

	loaded, _, found, err := TryLoadProgressState(root, state.SessionID)
	gomega.Expect(err).To(gomega.BeNil(), fmt.Sprintf("LoadProgressState: %v", err))
	gomega.Expect(found).To(gomega.BeTrue(), fmt.Sprintf("LoadProgressState: %v", err))
	gomega.Expect(len(loaded.AcceptanceEvidenceIDs) > 0).To(gomega.BeFalse(), fmt.Sprintf("downgraded progress state should not retain acceptance evidence: %#v", loaded.AcceptanceEvidenceIDs))
})

var _ = ginkgo.It("finalize terminal completion does not map acceptance i ds without evidence", func() {
	root := ginkgo.GinkgoT().TempDir()
	state := NewProgressState("required_acceptance_without_evidence", "prompt", "", nil, nil, time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC))

	state.Phase = plancontract.PlanExecuteProgressPhaseComplete
	state.Status = plancontract.PlanExecuteProgressStatusCompleted
	state.RequiredAcceptanceEvidenceIDs = []string{"req-1"}

	result := finalizePlanExecuteRunResult(root, state, ".idiom/plan-execute/sessions/required_acceptance_without_evidence.json", false, io.Discard, planExecuteRunResultInput{ExitCode: 0})
	if result.Completion != nil {
		gomega.Expect(result.Completion.AcceptanceEvidenceIDs).To(gomega.BeEmpty(), fmt.Sprintf("status-only completion should not map acceptance IDs: %#v", result.Completion))
	}
})

var _ = ginkgo.It("finalize terminal completion clears acceptance i ds on direct failure", func() {
	root := ginkgo.GinkgoT().TempDir()
	state := NewProgressState("direct_failure_acceptance_ids", "prompt", "", []string{"pkg/feature.go"}, nil, time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC))

	state.Phase = plancontract.PlanExecuteProgressPhaseComplete
	state.Status = plancontract.PlanExecuteProgressStatusCompleted
	state.ChangedPaths = []string{"pkg/feature.go"}
	state.RequiredAcceptanceEvidenceIDs = []string{"req-1"}
	state.AcceptanceEvidenceIDs = []string{"req-1"}
	state.Review.Status = plancontract.PlanExecuteReviewStatusSkipped
	state.Executor.SourceSanity = []plancontract.PhaseEvidenceCheck{{Kind: "source_sanity", Path: "pkg/feature.go", Status: "ok"}}

	result := finalizePlanExecuteRunResult(root, state, ".idiom/plan-execute/sessions/direct_failure_acceptance_ids.json", false, io.Discard, planExecuteRunResultInput{ExitCode: 2, FailureKind: "executor_failed", FailureReason: "command failed"})
	gomega.Expect(result.ExitCode != 2).To(gomega.BeFalse(), fmt.Sprintf("ExitCode = %d, want direct failure exit", result.ExitCode))
	gomega.Expect(result.Completion).NotTo(gomega.BeNil(), fmt.Sprintf("completion was not failed: %#v", result.Completion))
	gomega.Expect(result.Completion.TerminalClass).To(gomega.Equal(plancontract.PlanExecuteTerminalClassFailed), fmt.Sprintf("completion was not failed: %#v", result.Completion))
	gomega.Expect(len(result.Completion.AcceptanceEvidenceIDs) > 0).To(gomega.BeFalse(), fmt.Sprintf("failed terminal completion should not retain acceptance IDs: %#v", result.Completion.AcceptanceEvidenceIDs))

	loaded, _, found, err := TryLoadProgressState(root, state.SessionID)
	gomega.Expect(err).To(gomega.BeNil(), fmt.Sprintf("LoadProgressState: %v", err))
	gomega.Expect(found).To(gomega.BeTrue(), fmt.Sprintf("LoadProgressState: %v", err))
	gomega.Expect(len(loaded.AcceptanceEvidenceIDs) > 0).To(gomega.BeFalse(), fmt.Sprintf("failed progress state should not retain acceptance evidence: %#v", loaded.AcceptanceEvidenceIDs))
	gomega.Expect(loaded.Status != plancontract.PlanExecuteProgressStatusFailed).To(gomega.BeFalse(), fmt.Sprintf("failed progress state status = %q, want failed", loaded.Status))
	gomega.Expect(loaded.FailureKind != "executor_failed").To(gomega.BeFalse(), fmt.Sprintf("failed progress state failure kind = %q, want executor_failed", loaded.FailureKind))
})

var _ = ginkgo.It("terminal completion rejects degraded cpp source sanity", func() {
	record := plancontract.NormalizePlanExecuteTerminalCompletionRecord(plancontract.PlanExecuteTerminalCompletionRecord{
		Status:              plancontract.PlanExecuteTerminalStatusCompleted,
		Phase:               plancontract.PlanExecuteProgressPhaseComplete,
		ReviewStatus:        plancontract.PlanExecuteReviewStatusSkipped,
		ChangedPaths:        []string{"native/src/machine.cpp"},
		SourceSanity:        []plancontract.PhaseEvidenceCheck{{Kind: "source_sanity", Path: "native/src/machine.cpp", Status: "ok", Summary: "Source sanity passed for 1 file(s) (1 generic fallback)"}},
		ReviewRepairSkipped: true,
	})
	gomega.Expect(planExecuteTerminalStringSliceContains(record.ValidationFailures, "source_sanity_degraded:native/src/machine.cpp")).To(gomega.BeTrue(), fmt.Sprintf("degraded C++ source_sanity should reject terminal success, got %#v", record.ValidationFailures))
})

var _ = ginkgo.It("recovery invariant failures include validation failure paths", func() {
	failures := planExecuteRecoveryInvariantFailures(plancontract.PlanExecuteTerminalCompletionRecord{
		FailureInvariants: []plancontract.PlanExecuteTerminalInvariant{{
			Code:   "required_path_covered:pkg/feature.go",
			Status: "failed",
			Reason: "missing",
		}},
		ValidationFailures: []string{
			"changed_path_outside_write_envelope:pkg/feature.go",
			"verification_failed:go test ./pkg",
			"source_sanity_failed:(unknown)",
		},
	})
	gomega.Expect(len(failures) != 4).To(gomega.BeFalse(), fmt.Sprintf("failures=%#v", failures))
	gomega.Expect(failures[0].Path).To(gomega.Equal("pkg/feature.go"), fmt.Sprintf("expected path-qualified invariant failures, got %#v", failures))
	gomega.Expect(failures[1].Path).To(gomega.Equal("pkg/feature.go"), fmt.Sprintf("expected path-qualified invariant failures, got %#v", failures))
	gomega.Expect(failures[2].Path).To(gomega.Equal(""), fmt.Sprintf("non-path invariant suffixes must not become recovery paths: %#v", failures))
	gomega.Expect(failures[3].Path).To(gomega.Equal(""), fmt.Sprintf("non-path invariant suffixes must not become recovery paths: %#v", failures))
})

func planExecuteTerminalStringSliceContains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}

	return false
}

func planExecuteTerminalAnySliceContains(values []any, target string) bool {
	for _, value := range values {
		if text, ok := value.(string); ok && text == target {
			return true
		}
	}

	return false
}

func planExecuteTerminalStringSlicesEqual(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}

	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}

	return true
}

var _ = ginkgo.It("normalize plan execute terminal completion record keeps degraded outcome idempotent", func() {
	record := plancontract.NormalizePlanExecuteTerminalCompletionRecord(plancontract.PlanExecuteTerminalCompletionRecord{
		Status:       plancontract.PlanExecuteTerminalStatusDegraded,
		ReviewStatus: plancontract.PlanExecuteReviewStatusDegraded,
		FailureKind:  plancontract.PlanExecuteFailureReviewRepairDegraded,
	})

	record = plancontract.NormalizePlanExecuteTerminalCompletionRecord(record)
	gomega.Expect(record.TerminalClass).To(gomega.Equal(plancontract.PlanExecuteTerminalClassRecoveryRequired), fmt.Sprintf("degraded terminal record was not idempotent: %#v", record))
	gomega.Expect(record.Outcome).To(gomega.Equal(plancontract.PlanExecuteTerminalOutcomeReviewDegraded), fmt.Sprintf("degraded terminal record was not idempotent: %#v", record))
})

var _ = ginkgo.It("save plan execute terminal completion record round trips", func() {
	root := ginkgo.GinkgoT().TempDir()
	record := NewPlanExecuteTerminalCompletionRecord(
		progressStateForTerminalCompletionTest(func(state *ProgressState) {
			state.Phase = plancontract.PlanExecuteProgressPhaseReviewRepair
			state.Status = plancontract.PlanExecuteProgressStatusBlocked
			state.FailureKind = "review_repair_blocked"
			state.FailureReason = "review repair remained blocked"
			state.SetWriteAllowedPaths([]string{"logs/telemetry/run.jsonl", "logs/telemetry/run.jsonl"})
		}),
		PlanExecuteTerminalCompletionInput{
			ProgressPath: ".idiom/plan-execute/sessions/terminal_completion_test.json",
			ExitCode:     1,
			FailureID:    "failure-123",
			RecordPath:   ".idiom/recovery/failures/failure-123.json",
			PacketPath:   ".idiom/recovery/packets/failure-123.json",
			NextAction:   "review_decomposition",
		},
	)

	path, err := SavePlanExecuteTerminalCompletionRecord(root, record)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("SavePlanExecuteTerminalCompletionRecord: %v", err))

	if want := filepath.ToSlash(filepath.Join(".idiom", "plan-execute", "sessions", "terminal_completion_test.terminal.json")); path != want {
		ginkgo.Fail(fmt.Sprintf("path = %q, want %q", path, want))
	}

	loaded, loadedPath, found, err := TryLoadPlanExecuteTerminalCompletionRecord(root, "terminal_completion_test")
	gomega.Expect(err).To(gomega.BeNil(), fmt.Sprintf("TryLoadPlanExecuteTerminalCompletionRecord found=%v err=%v", found, err))
	gomega.Expect(found).To(gomega.BeTrue(), fmt.Sprintf("TryLoadPlanExecuteTerminalCompletionRecord found=%v err=%v", found, err))
	gomega.Expect(loadedPath != path).To(gomega.BeFalse(), fmt.Sprintf("loaded path = %q, want %q", loadedPath, path))
	gomega.Expect(loaded.TerminalClass).To(gomega.Equal(plancontract.PlanExecuteTerminalClassRecoveryRequired), fmt.Sprintf("loaded classification = class %q outcome %q", loaded.TerminalClass, loaded.Outcome))
	gomega.Expect(loaded.Outcome).To(gomega.Equal(plancontract.PlanExecuteTerminalOutcomeBlocked), fmt.Sprintf("loaded classification = class %q outcome %q", loaded.TerminalClass, loaded.Outcome))
	gomega.Expect(loaded.FailureID).To(gomega.Equal("failure-123"), fmt.Sprintf("loaded recovery metadata incomplete: %#v", loaded))
	gomega.Expect(loaded.PacketPath).NotTo(gomega.Equal(""), fmt.Sprintf("loaded recovery metadata incomplete: %#v", loaded))
	gomega.Expect(loaded.NextAction).To(gomega.Equal(recoverycontract.NextActionReviewDecomposition), fmt.Sprintf("loaded recovery metadata incomplete: %#v", loaded))
	gomega.Expect(loaded.WriteAllowedPaths).To(gomega.HaveLen(1), fmt.Sprintf("write allowed paths not normalized: %#v", loaded.WriteAllowedPaths))
	gomega.Expect(loaded.WriteAllowedPaths[0]).To(gomega.Equal("logs/telemetry/run.jsonl"), fmt.Sprintf("write allowed paths not normalized: %#v", loaded.WriteAllowedPaths))
})

var _ = ginkgo.Describe("loading plan execute terminal completion with invalid lifecycle fields", func() {
	tests := []struct {
		name    string
		mutate  func(*plancontract.PlanExecuteTerminalCompletionRecord)
		wantErr string
	}{
		{
			name: "status",
			mutate: func(record *plancontract.PlanExecuteTerminalCompletionRecord) {
				record.Status = "typo"
			},
			wantErr: "invalid plan-execute terminal completion status",
		},
		{
			name: "phase status mismatch",
			mutate: func(record *plancontract.PlanExecuteTerminalCompletionRecord) {
				record.Phase = plancontract.PlanExecuteProgressPhasePlanning
				record.Status = plancontract.PlanExecuteTerminalStatusCompleted
			},
			wantErr: "invalid plan-execute terminal completion lifecycle",
		},
		{
			name: "failure kind",
			mutate: func(record *plancontract.PlanExecuteTerminalCompletionRecord) {
				record.FailureKind = "review_repair_typo"
			},
			wantErr: "invalid plan-execute terminal completion failure kind",
		},
		{
			name: "next action",
			mutate: func(record *plancontract.PlanExecuteTerminalCompletionRecord) {
				record.NextAction = "retry_typo"
			},
			wantErr: "invalid plan-execute terminal completion next action",
		},
		{
			name: "evidence status",
			mutate: func(record *plancontract.PlanExecuteTerminalCompletionRecord) {
				record.SourceSanity = []plancontract.PhaseEvidenceCheck{{Kind: "source_sanity", Path: "pkg/feature.go", Status: "mystery"}}
			},
			wantErr: "invalid plan-execute terminal completion evidence status",
		},
	}

	for _, tt := range tests {
		tt := tt
		ginkgo.It(tt.name, func() {
			root := ginkgo.GinkgoT().TempDir()
			record := NewPlanExecuteTerminalCompletionRecord(
				progressStateForTerminalCompletionTest(func(state *ProgressState) {
					state.Phase = plancontract.PlanExecuteProgressPhaseComplete
					state.Status = plancontract.PlanExecuteProgressStatusCompleted
					state.Review.Status = plancontract.PlanExecuteReviewStatusSkipped
				}),
				PlanExecuteTerminalCompletionInput{},
			)
			tt.mutate(&record)
			writeTerminalCompletionRecordFixture(root, record)

			_, _, found, err := TryLoadPlanExecuteTerminalCompletionRecord(root, record.SessionID)
			gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("expected invalid terminal completion error and found=false, found=%v err=%v", found, err))
			gomega.Expect(found).To(gomega.BeFalse(), fmt.Sprintf("expected invalid terminal completion error and found=false, found=%v err=%v", found, err))
			gomega.Expect(strings.Contains(err.Error(), tt.wantErr)).To(gomega.BeTrue(), fmt.Sprintf("unexpected error: %v", err))

		})
	}
})

var _ = ginkgo.It("save plan execute terminal completion record rejects invalid lifecycle", func() {
	record := NewPlanExecuteTerminalCompletionRecord(
		progressStateForTerminalCompletionTest(func(state *ProgressState) {
			state.Phase = plancontract.PlanExecuteProgressPhasePlanning
			state.Status = plancontract.PlanExecuteProgressStatusFailed
		}),
		PlanExecuteTerminalCompletionInput{},
	)
	record.Status = "typo"

	if _, err := SavePlanExecuteTerminalCompletionRecord(ginkgo.GinkgoT().TempDir(), record); err == nil || !strings.Contains(err.Error(), "invalid plan-execute terminal completion status") {
		ginkgo.Fail(fmt.Sprintf("expected invalid terminal completion error, got %v", err))
	}
})

var _ = ginkgo.It("save plan execute terminal completion record does not follow predictable temp symlink", func() {
	root := ginkgo.GinkgoT().TempDir()
	record := NewPlanExecuteTerminalCompletionRecord(
		progressStateForTerminalCompletionTest(func(state *ProgressState) {
			state.SessionID = "tmp_symlink_terminal"
			state.Phase = plancontract.PlanExecuteProgressPhaseReviewRepair
			state.Status = plancontract.PlanExecuteProgressStatusBlocked
		}),
		PlanExecuteTerminalCompletionInput{ExitCode: 1},
	)
	fullPath, _, err := planExecuteTerminalCompletionPath(root, record.SessionID)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("planExecuteTerminalCompletionPath: %v", err))

	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		ginkgo.Fail(fmt.Sprintf("mkdir terminal dir: %v", err))
	}
	outside := filepath.Join(root, "outside-terminal.txt")
	if err := os.WriteFile(outside, []byte("sentinel"), 0o600); err != nil {
		ginkgo.Fail(fmt.Sprintf("seed outside: %v", err))
	}
	if err := os.Symlink(outside, fullPath+".tmp"); err != nil {
		ginkgo.Fail(fmt.Sprintf("symlink predictable temp: %v", err))
	}

	if _, err := SavePlanExecuteTerminalCompletionRecord(root, record); err != nil {
		ginkgo.Fail(fmt.Sprintf("SavePlanExecuteTerminalCompletionRecord: %v", err))
	}
	body, err := os.ReadFile(outside)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read outside: %v", err))
	gomega.Expect(string(body) != "sentinel").To(gomega.BeFalse(), fmt.Sprintf("predictable temp symlink target was modified: %q", string(body)))

	if _, err := os.Lstat(fullPath + ".tmp"); err != nil {
		ginkgo.Fail(fmt.Sprintf("predictable temp symlink should remain untouched: %v", err))
	}
})

var _ = ginkgo.It("load plan execute terminal completion rejects session mismatch", func() {
	root := ginkgo.GinkgoT().TempDir()
	record := NewPlanExecuteTerminalCompletionRecord(
		progressStateForTerminalCompletionTest(func(state *ProgressState) {}),
		PlanExecuteTerminalCompletionInput{},
	)
	record.SessionID = "other-session"

	fullPath, _, err := planExecuteTerminalCompletionPath(root, "terminal_completion_test")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("planExecuteTerminalCompletionPath: %v", err))

	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		ginkgo.Fail(fmt.Sprintf("mkdir: %v", err))
	}
	body, err := json.Marshal(record)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal: %v", err))

	if err := os.WriteFile(fullPath, body, 0o600); err != nil {
		ginkgo.Fail(fmt.Sprintf("write: %v", err))
	}

	_, _, found, err := TryLoadPlanExecuteTerminalCompletionRecord(root, "terminal_completion_test")
	gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("expected session mismatch error and found=false, found=%v err=%v", found, err))
	gomega.Expect(found).To(gomega.BeFalse(), fmt.Sprintf("expected session mismatch error and found=false, found=%v err=%v", found, err))
	gomega.Expect(strings.Contains(err.Error(), "does not match requested session")).To(gomega.BeTrue(), fmt.Sprintf("unexpected error: %v", err))
})

func writeTerminalCompletionRecordFixture(root string, record plancontract.PlanExecuteTerminalCompletionRecord) {
	ginkgo.GinkgoHelper()

	fullPath, _, err := planExecuteTerminalCompletionPath(root, record.SessionID)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("planExecuteTerminalCompletionPath: %v", err))

	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		ginkgo.Fail(fmt.Sprintf("mkdir: %v", err))
	}
	body, err := json.Marshal(record)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal: %v", err))

	if err := os.WriteFile(fullPath, append(body, '\n'), 0o600); err != nil {
		ginkgo.Fail(fmt.Sprintf("write: %v", err))
	}
}

func progressStateForTerminalCompletionTest(mutator func(*ProgressState)) ProgressState {
	state := NewProgressState("terminal_completion_test", "prompt", "", nil, nil, time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC))

	state.ChangedPaths = []string{"pkg/feature.go"}
	mutator(&state)

	return state
}
