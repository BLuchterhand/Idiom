package productworkflow

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("transition", func() {
	ginkgo.It("NextActionsForAllStates TableDriven", func() {
		tests := []struct {
			state           string
			statusBucket    string
			wantActions     []string
			wantOwners      []string
			wantBlocker     bool
			wantBlockerCode string
		}{
			{
				state: "planned", statusBucket: "started",
				wantActions: []string{"submit_for_human_verification", "start_investigation"},
				wantOwners:  []string{"planner", "investigator"},
			},
			{
				state: "human_verification", statusBucket: "started",
				wantActions: []string{"approve_for_implementation", "request_changes", "request_investigation"},
				wantOwners:  []string{"operator", "operator", "operator"},
			},
			{
				state: "implementing", statusBucket: "started",
				wantActions: []string{"submit_for_verification", "mark_blocked"},
				wantOwners:  []string{"implementor", "orchestrator"},
			},
			{
				state: "verifying", statusBucket: "started",
				wantActions: []string{"submit_for_review", "mark_blocked"},
				wantOwners:  []string{"reviewer", "orchestrator"},
			},
			{
				state: "reviewing", statusBucket: "started",
				wantActions: []string{"request_changes", "request_investigation"},
				wantOwners:  []string{"reviewer", "orchestrator"},
			},
			{
				state: "investigating", statusBucket: "started",
				wantActions: []string{"complete_investigation", "mark_blocked"},
				wantOwners:  []string{"investigator", "orchestrator"},
			},
			{
				state: "blocked", statusBucket: "started",
				wantActions: []string{"resolve_blocker"},
				wantOwners:  []string{"orchestrator"},
			},
			{
				state: "closed", statusBucket: "closed",
				wantActions: nil,
			},
			{
				state: "", statusBucket: "unstarted",
				wantActions: []string{"start"},
				wantOwners:  []string{"orchestrator"},
			},
			{
				state: "planned", statusBucket: "unstarted",
				wantActions: []string{"start"},
				wantOwners:  []string{"orchestrator"},
			},
			{
				state:           "nonsense",
				statusBucket:    "started",
				wantBlocker:     true,
				wantBlockerCode: "invalid_state",
			},
			{
				state:           "planned",
				statusBucket:    "garbage",
				wantBlocker:     true,
				wantBlockerCode: "invalid_status_bucket",
			},
		}

		for _, tc := range tests {
			ginkgo.By(tc.state+"_"+tc.statusBucket, func() {
				actions, blockers := NextActionsForState(tc.statusBucket, tc.state)
				if tc.wantBlocker {
					gomega.Expect(len(blockers) == 0).To(gomega.BeFalse(), fmt.Sprintf("expected blocker with code %q, got none", tc.wantBlockerCode))

					found := false

					for _, b := range blockers {
						if b.Code == tc.wantBlockerCode {
							found = true
							break
						}
					}
					gomega.Expect(!found).To(gomega.BeFalse(), fmt.Sprintf("expected blocker code %q, got %v", tc.wantBlockerCode, blockers))

					return
				}
				gomega.Expect(len(blockers) > 0).To(gomega.BeFalse(), fmt.Sprintf("expected no blockers, got %v", blockers))
				gomega.Expect(len(actions) != len(tc.wantActions)).To(gomega.BeFalse(), fmt.Sprintf("expected %d actions, got %d: %v", len(tc.wantActions), len(actions), actions))

				for i, want := range tc.wantActions {
					gomega.Expect(actions[i].Action != want).To(gomega.BeFalse(), fmt.Sprintf("action[%d]: expected %q, got %q (full: %v)", i, want, actions[i].Action, actions))
					if tc.wantOwners != nil {
						gomega.Expect(actions[i].Owner).To(gomega.Equal(tc.wantOwners[i]), fmt.Sprintf("action[%d] %q: expected owner %q, got %q", i, want, tc.wantOwners[i], actions[i].Owner))
					}

				}
			})
		}
	})
	ginkgo.It("InvalidStateReturnsTypedBlocker", func() {
		result := EvaluateTransition(TransitionInput{
			StatusBucket:    "started",
			CurrentState:    "nonexistent",
			RequestedAction: "start_implementation",
		})
		gomega.Expect(len(result.Blockers) == 0).To(gomega.BeFalse(), fmt.Sprint("expected blockers for invalid state"))
		gomega.Expect(result.Blockers[0].Code != "invalid_state").To(gomega.BeFalse(), fmt.Sprintf("expected blocker code invalid_state, got %q", result.Blockers[0].Code))
		gomega.Expect(!strings.Contains(result.Blockers[0].Message, "nonexistent")).To(gomega.BeFalse(), fmt.Sprintf("expected blocker message to mention state, got %q", result.Blockers[0].Message))

	})
	ginkgo.It("InvalidStatusBucketReturnsTypedBlocker", func() {
		result := EvaluateTransition(TransitionInput{
			StatusBucket:    "bogus",
			CurrentState:    "planned",
			RequestedAction: "start_implementation",
		})
		gomega.Expect(len(result.Blockers) == 0).To(gomega.BeFalse(), fmt.Sprint("expected blockers for invalid status bucket"))
		gomega.Expect(result.Blockers[0].Code != "invalid_status_bucket").To(gomega.BeFalse(), fmt.Sprintf("expected blocker code invalid_status_bucket, got %q", result.Blockers[0].Code))

	})
	ginkgo.It("InvalidActionReturnsTypedBlocker", func() {
		result := EvaluateTransition(TransitionInput{
			StatusBucket:    "started",
			CurrentState:    "planned",
			RequestedAction: "teleport",
		})
		gomega.Expect(len(result.Blockers) == 0).To(gomega.BeFalse(), fmt.Sprint("expected blockers for invalid action"))
		gomega.Expect(result.Blockers[0].Code != "invalid_action").To(gomega.BeFalse(), fmt.Sprintf("expected blocker code invalid_action, got %q", result.Blockers[0].Code))
		gomega.Expect(!strings.Contains(result.Blockers[0].Message, "teleport")).To(gomega.BeFalse(), fmt.Sprintf("expected blocker message to mention action, got %q", result.Blockers[0].Message))
		gomega.Expect(len(result.NextValidActions) == 0).To(gomega.BeFalse(), fmt.Sprint("expected NextValidActions for invalid action"))

	})
	ginkgo.It("TerminalClosedStatusBucketBlocksTransitions", func() {
		result := EvaluateTransition(TransitionInput{
			StatusBucket:    "closed",
			CurrentState:    "closed",
			RequestedAction: "start_implementation",
		})
		gomega.Expect(len(result.Blockers) == 0).To(gomega.BeFalse(), fmt.Sprint("expected blockers for closed status bucket"))
		gomega.Expect(result.Blockers[0].Code != "terminal_status_bucket").To(gomega.BeFalse(), fmt.Sprintf("expected blocker code terminal_status_bucket, got %q", result.Blockers[0].Code))

	})
	ginkgo.It("CloseTransitionBlockedByOpenBlockers", func() {
		result := EvaluateTransition(TransitionInput{
			StatusBucket:    "started",
			CurrentState:    "reviewing",
			RequestedAction: "approve",
			OpenBlockers:    []string{"env_down"},
			GateResults:     allGatesPassed(),
		})
		gomega.Expect(len(result.Blockers) == 0).To(gomega.BeFalse(), fmt.Sprint("expected close blocked by open blockers"))

		found := false

		for _, b := range result.Blockers {
			if b.Code == "open_blockers" {
				found = true
				break
			}
		}
		gomega.Expect(!found).To(gomega.BeFalse(), fmt.Sprintf("expected open_blockers blocker, got %v", result.Blockers))
		gomega.Expect(result.ToState != "").To(gomega.BeFalse(), fmt.Sprintf("expected no state transition, got ToState %q", result.ToState))

	})
	ginkgo.It("WriteProductWorkflowTransitionRejectsSymlinkedEventsParent", func() {
		root := ginkgo.GinkgoT().TempDir()
		outside := ginkgo.GinkgoT().TempDir()
		{

			err := os.WriteFile(filepath.Join(root, "workflow_state.json"), []byte("{}\n"), 0o644)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write workflow_state.json: %v", err))
		}
		{

			err := os.Symlink(outside, filepath.Join(root, "events"))
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("symlink events parent: %v", err))
		}

		rootHandle, err := os.OpenRoot(root)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		ginkgo.DeferCleanup(rootHandle.Close)
		lease := &productWorkflowMutationLease{root: rootHandle, effortRoot: root}

		err = writeProductWorkflowTransition(lease, WorkflowState{
			ArtifactPaths: map[string]string{"events": "events/log.jsonl"},
		}, ProductWorkflowTransitionEvent{Event: "transition_test"})
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected symlinked events parent to be rejected"))
		gomega.Expect(!strings.Contains(err.Error(), "events must not be a symlink")).To(gomega.BeFalse(), fmt.Sprintf("expected symlink parent error, got %v", err))
		{

			_, statErr := os.Stat(filepath.Join(outside, "log.jsonl"))
			gomega.Expect(!os.IsNotExist(statErr)).To(gomega.BeFalse(), fmt.Sprintf("transition must not write through symlinked events parent, stat err=%v", statErr))
		}

	})
	ginkgo.It("CloseTransitionBlockedByOpenObligations", func() {
		result := EvaluateTransition(TransitionInput{
			StatusBucket:    "started",
			CurrentState:    "reviewing",
			RequestedAction: "approve",
			OpenObligations: []string{"TDD-001"},
			GateResults:     allGatesPassed(),
		})
		gomega.Expect(len(result.Blockers) == 0).To(gomega.BeFalse(), fmt.Sprint("expected close blocked by open obligations"))

		found := false

		for _, b := range result.Blockers {
			if b.Code == "open_obligations" {
				found = true
				break
			}
		}
		gomega.Expect(!found).To(gomega.BeFalse(), fmt.Sprintf("expected open_obligations blocker, got %v", result.Blockers))

	})
	ginkgo.It("CloseTransitionBlockedByMissingVerificationGate", func() {
		result := EvaluateTransition(TransitionInput{
			StatusBucket:    "started",
			CurrentState:    "reviewing",
			RequestedAction: "approve",
			GateResults: []GateResult{
				{Gate: "review", Status: "passed"},
				{Gate: "investigation", Status: "passed"},
			},
		})
		gomega.Expect(len(result.Blockers) == 0).To(gomega.BeFalse(), fmt.Sprint("expected close blocked by missing verification gate"))

		found := false

		for _, b := range result.Blockers {
			if b.Code == "missing_gate" && strings.Contains(b.Message, "verification") {
				found = true
				break
			}
		}
		gomega.Expect(!found).To(gomega.BeFalse(), fmt.Sprintf("expected missing_gate blocker for verification, got %v", result.Blockers))

	})
	ginkgo.It("CloseTransitionBlockedByFailedReviewGate", func() {
		result := EvaluateTransition(TransitionInput{
			StatusBucket:    "started",
			CurrentState:    "reviewing",
			RequestedAction: "approve",
			GateResults: []GateResult{
				{Gate: "verification", Status: "passed"},
				{Gate: "review", Status: "failed"},
				{Gate: "investigation", Status: "passed"},
			},
		})
		gomega.Expect(len(result.Blockers) == 0).To(gomega.BeFalse(), fmt.Sprint("expected close blocked by failed review gate"))

		found := false

		for _, b := range result.Blockers {
			if b.Code == "gate_not_passed" && strings.Contains(b.Message, "review") {
				found = true
				break
			}
		}
		gomega.Expect(!found).To(gomega.BeFalse(), fmt.Sprintf("expected gate_not_passed blocker for review, got %v", result.Blockers))

	})
	ginkgo.It("CloseTransitionBlockedByMissingInvestigationGate", func() {
		result := EvaluateTransition(TransitionInput{
			StatusBucket:    "started",
			CurrentState:    "reviewing",
			RequestedAction: "approve",
			GateResults: []GateResult{
				{Gate: "verification", Status: "passed"},
				{Gate: "review", Status: "passed"},
			},
		})
		gomega.Expect(len(result.Blockers) == 0).To(gomega.BeFalse(), fmt.Sprint("expected close blocked by missing investigation gate"))

		found := false

		for _, b := range result.Blockers {
			if b.Code == "missing_gate" && strings.Contains(b.Message, "investigation") {
				found = true
				break
			}
		}
		gomega.Expect(!found).To(gomega.BeFalse(), fmt.Sprintf("expected missing_gate blocker for investigation, got %v", result.Blockers))

	})
	ginkgo.It("CloseTransitionSucceedsWithAllPreconditionsMet", func() {
		result := EvaluateTransition(TransitionInput{
			StatusBucket:    "started",
			CurrentState:    "reviewing",
			RequestedAction: "approve",
			OpenBlockers:    nil,
			OpenObligations: nil,
			GateResults:     allGatesPassed(),
		})
		gomega.Expect(len(result.Blockers) > 0).To(gomega.BeFalse(), fmt.Sprintf("expected no blockers, got %v", result.Blockers))
		gomega.Expect(result.ToState != "closed").To(gomega.BeFalse(), fmt.Sprintf("expected ToState closed, got %q", result.ToState))
		gomega.Expect(result.FromState != "reviewing").To(gomega.BeFalse(), fmt.Sprintf("expected FromState reviewing, got %q", result.FromState))

	})
	ginkgo.It("ApplyCloseTransitionPersistsClosedBucketAndState", func() {
		root := writeMetricsFixture()
		data, err := os.ReadFile(filepath.Join(root, WorkflowStateFileName))
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read workflow state: %v", err))

		wsJSON := string(data)
		wsJSON = strings.Replace(wsJSON, `"current_state":"implementing"`, `"current_state":"reviewing"`, 1)
		wsJSON = strings.Replace(wsJSON, `"gate_results":[{"gate":"scope","status":"pass"},{"gate":"verify","status":"fail"}]`, `"gate_results":[{"gate":"verification","status":"passed"},{"gate":"review","status":"passed"},{"gate":"investigation","status":"passed"}]`, 1)
		wsJSON = strings.Replace(wsJSON, `"open_obligations":["OB-1","OB-2"]`, `"open_obligations":[]`, 1)
		wsJSON = strings.Replace(wsJSON, `"blockers":["review pending"]`, `"blockers":[]`, 1)
		wsJSON = strings.Replace(wsJSON, `"next_valid_actions":["close","retest"]`, `"next_valid_actions":["approve"]`, 1)
		{

			err := os.WriteFile(filepath.Join(root, WorkflowStateFileName), []byte(wsJSON), 0o644)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write workflow state: %v", err))
		}

		writeFixtureFile(root, "briefs/test.md", "# Brief\n\nClose the workflow.\n")
		writeFixtureFile(root, "reports/r.json", "{}\n")
		writeFixtureFile(root, productWorkflowDefaultEventsPath, "")
		writeFixtureFile(root, "implementation_ledger.jsonl", `{"record_type":"atom_planned","task_id":"impl-metrics","effort_id":"metrics-effort","timestamp":"2026-05-17T00:00:00Z","complexity_tier":"C1","context_pack_id":"pack","implementor":{"id":"impl","runtime":"opencode","model":"m","command":"c"},"reviewer":{"id":"rev","model":"m","reasoning_effort":"medium","runtime":"codex"},"scope":{"expected_files":["a.go"],"forbidden_patterns":["docs/**"]},"required_verification":["go test"],"report_paths":{"implementation":"reports/i-implementation.json","review":"reports/r-review.json","investigation":"reports/x-investigation.json"}}`+"\n"+`{"record_type":"verification_completed","task_id":"impl-metrics","effort_id":"metrics-effort","timestamp":"2026-05-17T00:01:00Z","tdd_obligations_satisfied":["TDD-1","TDD-2"],"verification_status":"pass","verification_commands_run":["go test"],"actor":"verifier"}`+"\n")

		result := ApplyProductWorkflowTransition(ProductWorkflowTransitionApplyOptions{
			EffortRoot:            root,
			Action:                ProductWorkflowActionApprove,
			Actor:                 ProductWorkflowActionOwnerReviewer,
			UpdatedAt:             "2026-06-26T12:00:00Z",
			EventID:               "evt-close-test",
			ExpectedStateRevision: 7,
		})
		gomega.Expect(result.Status != ProductWorkflowStatusApplied).To(gomega.BeFalse(), fmt.Sprintf("expected applied transition, got status=%q blockers=%v", result.Status, result.Blockers))
		gomega.Expect(result.UpdatedWorkflowState.StatusBucket != WorkflowStatusBucketClosed).To(gomega.BeFalse(), fmt.Sprintf("expected result status_bucket closed, got %q", result.UpdatedWorkflowState.StatusBucket))
		gomega.Expect(result.UpdatedWorkflowState.CurrentState != WorkflowStateClosed).To(gomega.BeFalse(), fmt.Sprintf("expected result current_state closed, got %q", result.UpdatedWorkflowState.CurrentState))
		gomega.Expect(len(result.UpdatedWorkflowState.NextValidActions) != 0).To(gomega.BeFalse(), fmt.Sprintf("expected no next actions for closed workflow, got %v", result.UpdatedWorkflowState.NextValidActions))

		persisted, err := LoadWorkflowState(root)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("reload workflow state: %v", err))
		gomega.Expect(persisted.StatusBucket).To(gomega.Equal(WorkflowStatusBucketClosed), fmt.Sprintf("expected persisted closed bucket/state, got bucket=%q state=%q", persisted.StatusBucket, persisted.CurrentState))
		gomega.Expect(persisted.CurrentState).To(gomega.Equal(WorkflowStateClosed), fmt.Sprintf("expected persisted closed bucket/state, got bucket=%q state=%q", persisted.StatusBucket, persisted.CurrentState))

		statusView, workflowState, err := LoadCheckedProductStatus(root)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("load checked status: %v", err))

		operatorStatus := BuildProductOperatorStatusView(statusView, workflowState)
		gomega.Expect(operatorStatus.Dispatchable).To(gomega.BeFalse(), fmt.Sprint("closed workflow must not be dispatchable"))
		gomega.Expect(operatorStatus.WorkflowTerminalState == "").To(gomega.BeFalse(), fmt.Sprint("closed workflow should have a terminal projection"))

	})
	ginkgo.It("CloseTransitionBlockedByAllPreconditionsFailing", func() {
		result := EvaluateTransition(TransitionInput{
			StatusBucket:    "started",
			CurrentState:    "reviewing",
			RequestedAction: "approve",
			OpenBlockers:    []string{"b1"},
			OpenObligations: []string{"o1"},
			GateResults:     []GateResult{},
		})
		codes := map[string]bool{}

		for _, b := range result.Blockers {
			codes[b.Code] = true
		}

		for _, want := range []string{"open_blockers", "open_obligations", "missing_gate"} {
			gomega.Expect(!codes[want]).To(gomega.BeFalse(), fmt.Sprintf("expected blocker code %q, got blockers: %v", want, result.Blockers))

		}
	})
	ginkgo.It("ApproveActionNotListedWhenPreconditionsNotMet", func() {
		actions, blockers := NextActionsForState("started", "reviewing")
		gomega.Expect(len(blockers) > 0).To(gomega.BeFalse(), fmt.Sprintf("expected no blockers, got %v", blockers))

		for _, a := range actions {
			gomega.Expect(a.Action == "approve").To(gomega.BeFalse(), fmt.Sprint("approve should not appear in next actions when preconditions cannot be verified"))

		}
	})
	ginkgo.It("ApproveActionListedWhenPreconditionsMet", func() {
		gates := allGatesPassed()
		actions := nextActionsFor("reviewing", gates)
		found := false

		for _, a := range actions {
			if a.Action == "approve" {
				found = true
				gomega.Expect(a.Owner != "reviewer").To(gomega.BeFalse(), fmt.Sprintf("expected approve owner reviewer, got %q", a.Owner))

				break
			}
		}
		gomega.Expect(!found).To(gomega.BeFalse(), fmt.Sprint("expected approve action when all gates passed"))

	})
	ginkgo.It("ValidTransitionPlannedToHumanVerification", func() {
		result := EvaluateTransition(TransitionInput{
			StatusBucket:    "started",
			CurrentState:    "planned",
			RequestedAction: "submit_for_human_verification",
		})
		gomega.Expect(len(result.Blockers) > 0).To(gomega.BeFalse(), fmt.Sprintf("expected no blockers, got %v", result.Blockers))
		gomega.Expect(result.ToState != "human_verification").To(gomega.BeFalse(), fmt.Sprintf("expected ToState human_verification, got %q", result.ToState))

	})
	ginkgo.It("ValidTransitionHumanVerificationToImplementing", func() {
		result := EvaluateTransition(TransitionInput{
			StatusBucket:    "started",
			CurrentState:    "human_verification",
			RequestedAction: "approve_for_implementation",
		})
		gomega.Expect(len(result.Blockers) > 0).To(gomega.BeFalse(), fmt.Sprintf("expected no blockers, got %v", result.Blockers))
		gomega.Expect(result.ToState != "implementing").To(gomega.BeFalse(), fmt.Sprintf("expected ToState implementing, got %q", result.ToState))

	})
	ginkgo.It("ValidTransitionImplementingToVerifying", func() {
		result := EvaluateTransition(TransitionInput{
			StatusBucket:    "started",
			CurrentState:    "implementing",
			RequestedAction: "submit_for_verification",
		})
		gomega.Expect(len(result.Blockers) > 0).To(gomega.BeFalse(), fmt.Sprintf("expected no blockers, got %v", result.Blockers))
		gomega.Expect(result.ToState != "verifying").To(gomega.BeFalse(), fmt.Sprintf("expected ToState verifying, got %q", result.ToState))

	})
	ginkgo.It("ValidTransitionVerifyingToReviewing", func() {
		result := EvaluateTransition(TransitionInput{
			StatusBucket:    "started",
			CurrentState:    "verifying",
			RequestedAction: "submit_for_review",
		})
		gomega.Expect(len(result.Blockers) > 0).To(gomega.BeFalse(), fmt.Sprintf("expected no blockers, got %v", result.Blockers))
		gomega.Expect(result.ToState != "reviewing").To(gomega.BeFalse(), fmt.Sprintf("expected ToState reviewing, got %q", result.ToState))

	})
	ginkgo.It("ValidTransitionReviewingToImplementing", func() {
		result := EvaluateTransition(TransitionInput{
			StatusBucket:    "started",
			CurrentState:    "reviewing",
			RequestedAction: "request_changes",
		})
		gomega.Expect(len(result.Blockers) > 0).To(gomega.BeFalse(), fmt.Sprintf("expected no blockers, got %v", result.Blockers))
		gomega.Expect(result.ToState != "implementing").To(gomega.BeFalse(), fmt.Sprintf("expected ToState implementing, got %q", result.ToState))

	})
	ginkgo.It("ValidTransitionReviewingToInvestigating", func() {
		result := EvaluateTransition(TransitionInput{
			StatusBucket:    "started",
			CurrentState:    "reviewing",
			RequestedAction: "request_investigation",
		})
		gomega.Expect(len(result.Blockers) > 0).To(gomega.BeFalse(), fmt.Sprintf("expected no blockers, got %v", result.Blockers))
		gomega.Expect(result.ToState != "investigating").To(gomega.BeFalse(), fmt.Sprintf("expected ToState investigating, got %q", result.ToState))

	})
	ginkgo.It("ValidTransitionInvestigatingToReviewing", func() {
		result := EvaluateTransition(TransitionInput{
			StatusBucket:    "started",
			CurrentState:    "investigating",
			RequestedAction: "complete_investigation",
		})
		gomega.Expect(len(result.Blockers) > 0).To(gomega.BeFalse(), fmt.Sprintf("expected no blockers, got %v", result.Blockers))
		gomega.Expect(result.ToState != "reviewing").To(gomega.BeFalse(), fmt.Sprintf("expected ToState reviewing, got %q", result.ToState))

	})
	ginkgo.It("ValidTransitionBlockedToPlanned", func() {
		result := EvaluateTransition(TransitionInput{
			StatusBucket:    "started",
			CurrentState:    "blocked",
			RequestedAction: "resolve_blocker",
		})
		gomega.Expect(len(result.Blockers) > 0).To(gomega.BeFalse(), fmt.Sprintf("expected no blockers, got %v", result.Blockers))
		gomega.Expect(result.ToState != "planned").To(gomega.BeFalse(), fmt.Sprintf("expected ToState planned, got %q", result.ToState))

	})
	ginkgo.It("MarkBlockedFromMultipleStates", func() {
		for _, state := range []string{"implementing", "verifying", "investigating"} {
			ginkgo.By(state, func() {
				result := EvaluateTransition(TransitionInput{
					StatusBucket:    "started",
					CurrentState:    state,
					RequestedAction: "mark_blocked",
				})
				gomega.Expect(len(result.Blockers) > 0).To(gomega.BeFalse(), fmt.Sprintf("expected no blockers, got %v", result.Blockers))
				gomega.Expect(result.ToState != "blocked").To(gomega.BeFalse(), fmt.Sprintf("expected ToState blocked, got %q", result.ToState))

			})
		}
	})
	ginkgo.It("UnstartedStartTransition", func() {
		result := EvaluateTransition(TransitionInput{
			StatusBucket:    "unstarted",
			CurrentState:    "",
			RequestedAction: "start",
		})
		gomega.Expect(len(result.Blockers) > 0).To(gomega.BeFalse(), fmt.Sprintf("expected no blockers, got %v", result.Blockers))
		gomega.Expect(result.ToState != "planned").To(gomega.BeFalse(), fmt.Sprintf("expected ToState planned, got %q", result.ToState))

	})
	ginkgo.It("UnstartedRejectsNonStartAction", func() {
		result := EvaluateTransition(TransitionInput{
			StatusBucket:    "unstarted",
			CurrentState:    "",
			RequestedAction: "start_implementation",
		})
		gomega.Expect(len(result.Blockers) == 0).To(gomega.BeFalse(), fmt.Sprint("expected blockers for non-start action on unstarted bucket"))
		gomega.Expect(result.Blockers[0].Code != "invalid_action").To(gomega.BeFalse(), fmt.Sprintf("expected blocker code invalid_action, got %q", result.Blockers[0].Code))

	})
	ginkgo.It("TransitionPurity NoForbiddenImports", func() {
		dir, err := os.Getwd()
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprint(err))

		for {
			if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
				break
			}

			next := filepath.Dir(dir)
			gomega.Expect(next == dir).To(gomega.BeFalse(), fmt.Sprint("could not find repo root"))

			dir = next
		}

		data, err := os.ReadFile(filepath.Join(dir, "internal", "workflows", "productworkflow", "transition.go"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read transition.go: %v", err))

		src := string(data)
		forbidden := []string{
			"os.", "io.", "net/", "http/",
			"fmt.", "log.", "time.",
			"sync.", "atomic.",
			"path/", "filepath.",
			"bufio.", "regexp.",
			"exec.", "syscall.",
			"runtime.", "debug.",
			"json.", "xml.", "yaml.",
		}

		for _, f := range forbidden {
			gomega.Expect(strings.Contains(src, f)).To(gomega.BeFalse(), fmt.Sprintf("transition.go must not import %q (purity constraint)", f))

		}
	})
	ginkgo.It("NextActionsForClosedStatusBucketReturnsEmpty", func() {
		actions, blockers := NextActionsForState("closed", "closed")
		gomega.Expect(len(blockers) > 0).To(gomega.BeFalse(), fmt.Sprintf("expected no blockers, got %v", blockers))
		gomega.Expect(len(actions) > 0).To(gomega.BeFalse(), fmt.Sprintf("expected no actions for closed, got %v", actions))

	})
	ginkgo.It("NextActionsStableOrder Planned", func() {
		actions, blockers := NextActionsForState("started", "planned")
		gomega.Expect(len(blockers) > 0).To(gomega.BeFalse(), fmt.Sprintf("expected no blockers, got %v", blockers))

		want := []TransitionAction{
			{Action: "submit_for_human_verification", Owner: "planner"},
			{Action: "start_investigation", Owner: "investigator"},
		}
		gomega.Expect(len(actions) != len(want)).To(gomega.BeFalse(), fmt.Sprintf("expected %d actions, got %d: %v", len(want), len(actions), actions))

		for i := range want {
			gomega.Expect(actions[i] != want[i]).To(gomega.BeFalse(), fmt.Sprintf("action[%d]: expected %+v, got %+v", i, want[i], actions[i]))

		}
	})
	ginkgo.It("NextActionsStableOrder HumanVerification", func() {
		actions, blockers := NextActionsForState("started", "human_verification")
		gomega.Expect(len(blockers) > 0).To(gomega.BeFalse(), fmt.Sprintf("expected no blockers, got %v", blockers))

		want := []TransitionAction{
			{Action: "approve_for_implementation", Owner: "operator"},
			{Action: "request_changes", Owner: "operator"},
			{Action: "request_investigation", Owner: "operator"},
		}
		gomega.Expect(len(actions) != len(want)).To(gomega.BeFalse(), fmt.Sprintf("expected %d actions, got %d: %v", len(want), len(actions), actions))

		for i := range want {
			gomega.Expect(actions[i] != want[i]).To(gomega.BeFalse(), fmt.Sprintf("action[%d]: expected %+v, got %+v", i, want[i], actions[i]))

		}
	})
	ginkgo.It("NextActionsStableOrder Reviewing", func() {
		actions, blockers := NextActionsForState("started", "reviewing")
		gomega.Expect(len(blockers) > 0).To(gomega.BeFalse(), fmt.Sprintf("expected no blockers, got %v", blockers))

		want := []TransitionAction{
			{Action: "request_changes", Owner: "reviewer"},
			{Action: "request_investigation", Owner: "orchestrator"},
		}
		gomega.Expect(len(actions) != len(want)).To(gomega.BeFalse(), fmt.Sprintf("expected %d actions, got %d: %v", len(want), len(actions), actions))

		for i := range want {
			gomega.Expect(actions[i] != want[i]).To(gomega.BeFalse(), fmt.Sprintf("action[%d]: expected %+v, got %+v", i, want[i], actions[i]))

		}
	})
	ginkgo.It("NextActionsStableOrder Implementing", func() {
		actions, blockers := NextActionsForState("started", "implementing")
		gomega.Expect(len(blockers) > 0).To(gomega.BeFalse(), fmt.Sprintf("expected no blockers, got %v", blockers))

		want := []TransitionAction{
			{Action: "submit_for_verification", Owner: "implementor"},
			{Action: "mark_blocked", Owner: "orchestrator"},
		}
		gomega.Expect(len(actions) != len(want)).To(gomega.BeFalse(), fmt.Sprintf("expected %d actions, got %d: %v", len(want), len(actions), actions))

		for i := range want {
			gomega.Expect(actions[i] != want[i]).To(gomega.BeFalse(), fmt.Sprintf("action[%d]: expected %+v, got %+v", i, want[i], actions[i]))

		}
	})
	ginkgo.It("StableOrderHoldsAcrossRepeatedCalls", func() {
		for run := 0; run < 20; run++ {
			actions, _ := NextActionsForState("started", "planned")
			gomega.Expect(actions).To(gomega.HaveLen(2), fmt.Sprintf("run %d: ordering changed: %v", run, actions))
			gomega.Expect(actions[0].Action).To(gomega.Equal("submit_for_human_verification"), fmt.Sprintf("run %d: ordering changed: %v", run, actions))
			gomega.Expect(actions[1].Action).To(gomega.Equal("start_investigation"), fmt.Sprintf("run %d: ordering changed: %v", run, actions))

		}
	})
	ginkgo.It("InvalidStateBucketConsistency StartedBucketWithClosedState", func() {
		result := EvaluateTransition(TransitionInput{
			StatusBucket:    "started",
			CurrentState:    "closed",
			RequestedAction: "start_implementation",
		})
		gomega.Expect(len(result.Blockers) == 0).To(gomega.BeFalse(), fmt.Sprint("expected blocker for started bucket with closed state"))
		gomega.Expect(result.Blockers[0].Code != "invalid_state_bucket").To(gomega.BeFalse(), fmt.Sprintf("expected blocker code invalid_state_bucket, got %q", result.Blockers[0].Code))

	})
	ginkgo.It("InvalidStateBucketConsistency ClosedBucketWithNonClosedState", func() {
		result := EvaluateTransition(TransitionInput{
			StatusBucket:    "closed",
			CurrentState:    "planned",
			RequestedAction: "start_implementation",
		})
		gomega.Expect(len(result.Blockers) == 0).To(gomega.BeFalse(), fmt.Sprint("expected blocker for closed bucket with non-closed state"))
		gomega.Expect(result.Blockers[0].Code != "invalid_state_bucket").To(gomega.BeFalse(), fmt.Sprintf("expected blocker code invalid_state_bucket, got %q", result.Blockers[0].Code))

	})
	ginkgo.It("InvalidStateBucketConsistency UnstartedBucketWithImplementingState", func() {
		result := EvaluateTransition(TransitionInput{
			StatusBucket:    "unstarted",
			CurrentState:    "implementing",
			RequestedAction: "start",
		})
		gomega.Expect(len(result.Blockers) == 0).To(gomega.BeFalse(), fmt.Sprint("expected blocker for unstarted bucket with implementing state"))
		gomega.Expect(result.Blockers[0].Code != "invalid_state_bucket").To(gomega.BeFalse(), fmt.Sprintf("expected blocker code invalid_state_bucket, got %q", result.Blockers[0].Code))

	})
	ginkgo.It("InvalidStateBucketConsistency NextActionsForState ClosedBucketNonClosedState", func() {
		_, blockers := NextActionsForState("closed", "planned")
		gomega.Expect(len(blockers) == 0).To(gomega.BeFalse(), fmt.Sprint("expected blocker for closed bucket with non-closed state via NextActionsForState"))
		gomega.Expect(blockers[0].Code != "invalid_state_bucket").To(gomega.BeFalse(), fmt.Sprintf("expected blocker code invalid_state_bucket, got %q", blockers[0].Code))

	})
	ginkgo.It("InvalidStateBucketConsistency NextActionsForState StartedBucketClosedState", func() {
		_, blockers := NextActionsForState("started", "closed")
		gomega.Expect(len(blockers) == 0).To(gomega.BeFalse(), fmt.Sprint("expected blocker for started bucket with closed state via NextActionsForState"))
		gomega.Expect(blockers[0].Code != "invalid_state_bucket").To(gomega.BeFalse(), fmt.Sprintf("expected blocker code invalid_state_bucket, got %q", blockers[0].Code))

	})
	ginkgo.It("InvalidStateBucketConsistency NextActionsForState UnstartedBucketImplementingState", func() {
		actions, blockers := NextActionsForState("unstarted", "implementing")
		gomega.Expect(len(blockers) == 0).To(gomega.BeFalse(), fmt.Sprint("expected blocker for unstarted bucket with implementing state via NextActionsForState"))
		gomega.Expect(blockers[0].Code != "invalid_state_bucket").To(gomega.BeFalse(), fmt.Sprintf("expected blocker code invalid_state_bucket, got %q", blockers[0].Code))
		gomega.Expect(len(actions) != 0).To(gomega.BeFalse(), fmt.Sprintf("expected no actions, got %v", actions))

	})
	ginkgo.It("ValidStateBucketConsistency UnstartedBucketEmptyState", func() {
		actions, blockers := NextActionsForState("unstarted", "")
		gomega.Expect(len(blockers) > 0).To(gomega.BeFalse(), fmt.Sprintf("expected no blockers, got %v", blockers))
		gomega.Expect(actions).To(gomega.HaveLen(1), fmt.Sprintf("expected [start], got %v", actions))
		gomega.Expect(actions[0].Action).To(gomega.Equal("start"), fmt.Sprintf("expected [start], got %v", actions))

	})
	ginkgo.It("ValidStateBucketConsistency UnstartedBucketPlannedState", func() {
		actions, blockers := NextActionsForState("unstarted", "planned")
		gomega.Expect(len(blockers) > 0).To(gomega.BeFalse(), fmt.Sprintf("expected no blockers, got %v", blockers))
		gomega.Expect(actions).To(gomega.HaveLen(1), fmt.Sprintf("expected [start], got %v", actions))
		gomega.Expect(actions[0].Action).To(gomega.Equal("start"), fmt.Sprintf("expected [start], got %v", actions))

	})
})

func allGatesPassed() []GateResult {
	return []GateResult{
		{Gate: "verification", Status: "passed"},
		{Gate: "review", Status: "passed"},
		{Gate: "investigation", Status: "passed"},
	}
}
