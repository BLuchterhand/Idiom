package productworkflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNextActionsForAllStates_TableDriven(t *testing.T) {
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
		t.Run(tc.state+"_"+tc.statusBucket, func(t *testing.T) {
			actions, blockers := NextActionsForState(tc.statusBucket, tc.state)
			if tc.wantBlocker {
				if len(blockers) == 0 {
					t.Fatalf("expected blocker with code %q, got none", tc.wantBlockerCode)
				}
				found := false
				for _, b := range blockers {
					if b.Code == tc.wantBlockerCode {
						found = true
						break
					}
				}
				if !found {
					t.Fatalf("expected blocker code %q, got %v", tc.wantBlockerCode, blockers)
				}
				return
			}
			if len(blockers) > 0 {
				t.Fatalf("expected no blockers, got %v", blockers)
			}
			if len(actions) != len(tc.wantActions) {
				t.Fatalf("expected %d actions, got %d: %v", len(tc.wantActions), len(actions), actions)
			}
			for i, want := range tc.wantActions {
				if actions[i].Action != want {
					t.Fatalf("action[%d]: expected %q, got %q (full: %v)", i, want, actions[i].Action, actions)
				}
				if tc.wantOwners != nil && actions[i].Owner != tc.wantOwners[i] {
					t.Fatalf("action[%d] %q: expected owner %q, got %q", i, want, tc.wantOwners[i], actions[i].Owner)
				}
			}
		})
	}
}

func TestInvalidStateReturnsTypedBlocker(t *testing.T) {
	result := EvaluateTransition(TransitionInput{
		StatusBucket:    "started",
		CurrentState:    "nonexistent",
		RequestedAction: "start_implementation",
	})
	if len(result.Blockers) == 0 {
		t.Fatal("expected blockers for invalid state")
	}
	if result.Blockers[0].Code != "invalid_state" {
		t.Fatalf("expected blocker code invalid_state, got %q", result.Blockers[0].Code)
	}
	if !strings.Contains(result.Blockers[0].Message, "nonexistent") {
		t.Fatalf("expected blocker message to mention state, got %q", result.Blockers[0].Message)
	}
}

func TestInvalidStatusBucketReturnsTypedBlocker(t *testing.T) {
	result := EvaluateTransition(TransitionInput{
		StatusBucket:    "bogus",
		CurrentState:    "planned",
		RequestedAction: "start_implementation",
	})
	if len(result.Blockers) == 0 {
		t.Fatal("expected blockers for invalid status bucket")
	}
	if result.Blockers[0].Code != "invalid_status_bucket" {
		t.Fatalf("expected blocker code invalid_status_bucket, got %q", result.Blockers[0].Code)
	}
}

func TestInvalidActionReturnsTypedBlocker(t *testing.T) {
	result := EvaluateTransition(TransitionInput{
		StatusBucket:    "started",
		CurrentState:    "planned",
		RequestedAction: "teleport",
	})
	if len(result.Blockers) == 0 {
		t.Fatal("expected blockers for invalid action")
	}
	if result.Blockers[0].Code != "invalid_action" {
		t.Fatalf("expected blocker code invalid_action, got %q", result.Blockers[0].Code)
	}
	if !strings.Contains(result.Blockers[0].Message, "teleport") {
		t.Fatalf("expected blocker message to mention action, got %q", result.Blockers[0].Message)
	}
	if len(result.NextValidActions) == 0 {
		t.Fatal("expected NextValidActions for invalid action")
	}
}

func TestTerminalClosedStatusBucketBlocksTransitions(t *testing.T) {
	result := EvaluateTransition(TransitionInput{
		StatusBucket:    "closed",
		CurrentState:    "closed",
		RequestedAction: "start_implementation",
	})
	if len(result.Blockers) == 0 {
		t.Fatal("expected blockers for closed status bucket")
	}
	if result.Blockers[0].Code != "terminal_status_bucket" {
		t.Fatalf("expected blocker code terminal_status_bucket, got %q", result.Blockers[0].Code)
	}
}

func TestCloseTransitionBlockedByOpenBlockers(t *testing.T) {
	result := EvaluateTransition(TransitionInput{
		StatusBucket:    "started",
		CurrentState:    "reviewing",
		RequestedAction: "approve",
		OpenBlockers:    []string{"env_down"},
		GateResults:     allGatesPassed(),
	})
	if len(result.Blockers) == 0 {
		t.Fatal("expected close blocked by open blockers")
	}
	found := false
	for _, b := range result.Blockers {
		if b.Code == "open_blockers" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected open_blockers blocker, got %v", result.Blockers)
	}
	if result.ToState != "" {
		t.Fatalf("expected no state transition, got ToState %q", result.ToState)
	}
}

func TestCloseTransitionBlockedByOpenObligations(t *testing.T) {
	result := EvaluateTransition(TransitionInput{
		StatusBucket:    "started",
		CurrentState:    "reviewing",
		RequestedAction: "approve",
		OpenObligations: []string{"TDD-001"},
		GateResults:     allGatesPassed(),
	})
	if len(result.Blockers) == 0 {
		t.Fatal("expected close blocked by open obligations")
	}
	found := false
	for _, b := range result.Blockers {
		if b.Code == "open_obligations" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected open_obligations blocker, got %v", result.Blockers)
	}
}

func TestCloseTransitionBlockedByMissingVerificationGate(t *testing.T) {
	result := EvaluateTransition(TransitionInput{
		StatusBucket:    "started",
		CurrentState:    "reviewing",
		RequestedAction: "approve",
		GateResults: []GateResult{
			{Gate: "review", Status: "passed"},
			{Gate: "investigation", Status: "passed"},
		},
	})
	if len(result.Blockers) == 0 {
		t.Fatal("expected close blocked by missing verification gate")
	}
	found := false
	for _, b := range result.Blockers {
		if b.Code == "missing_gate" && strings.Contains(b.Message, "verification") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected missing_gate blocker for verification, got %v", result.Blockers)
	}
}

func TestCloseTransitionBlockedByFailedReviewGate(t *testing.T) {
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
	if len(result.Blockers) == 0 {
		t.Fatal("expected close blocked by failed review gate")
	}
	found := false
	for _, b := range result.Blockers {
		if b.Code == "gate_not_passed" && strings.Contains(b.Message, "review") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected gate_not_passed blocker for review, got %v", result.Blockers)
	}
}

func TestCloseTransitionBlockedByMissingInvestigationGate(t *testing.T) {
	result := EvaluateTransition(TransitionInput{
		StatusBucket:    "started",
		CurrentState:    "reviewing",
		RequestedAction: "approve",
		GateResults: []GateResult{
			{Gate: "verification", Status: "passed"},
			{Gate: "review", Status: "passed"},
		},
	})
	if len(result.Blockers) == 0 {
		t.Fatal("expected close blocked by missing investigation gate")
	}
	found := false
	for _, b := range result.Blockers {
		if b.Code == "missing_gate" && strings.Contains(b.Message, "investigation") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected missing_gate blocker for investigation, got %v", result.Blockers)
	}
}

func TestCloseTransitionSucceedsWithAllPreconditionsMet(t *testing.T) {
	result := EvaluateTransition(TransitionInput{
		StatusBucket:    "started",
		CurrentState:    "reviewing",
		RequestedAction: "approve",
		OpenBlockers:    nil,
		OpenObligations: nil,
		GateResults:     allGatesPassed(),
	})
	if len(result.Blockers) > 0 {
		t.Fatalf("expected no blockers, got %v", result.Blockers)
	}
	if result.ToState != "closed" {
		t.Fatalf("expected ToState closed, got %q", result.ToState)
	}
	if result.FromState != "reviewing" {
		t.Fatalf("expected FromState reviewing, got %q", result.FromState)
	}
}

func TestCloseTransitionBlockedByAllPreconditionsFailing(t *testing.T) {
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
		if !codes[want] {
			t.Fatalf("expected blocker code %q, got blockers: %v", want, result.Blockers)
		}
	}
}

func TestApproveActionNotListedWhenPreconditionsNotMet(t *testing.T) {
	actions, blockers := NextActionsForState("started", "reviewing")
	if len(blockers) > 0 {
		t.Fatalf("expected no blockers, got %v", blockers)
	}
	for _, a := range actions {
		if a.Action == "approve" {
			t.Fatal("approve should not appear in next actions when preconditions cannot be verified")
		}
	}
}

func TestApproveActionListedWhenPreconditionsMet(t *testing.T) {
	gates := allGatesPassed()
	actions := nextActionsFor("reviewing", gates)
	found := false
	for _, a := range actions {
		if a.Action == "approve" {
			found = true
			if a.Owner != "reviewer" {
				t.Fatalf("expected approve owner reviewer, got %q", a.Owner)
			}
			break
		}
	}
	if !found {
		t.Fatal("expected approve action when all gates passed")
	}
}

func TestValidTransitionPlannedToHumanVerification(t *testing.T) {
	result := EvaluateTransition(TransitionInput{
		StatusBucket:    "started",
		CurrentState:    "planned",
		RequestedAction: "submit_for_human_verification",
	})
	if len(result.Blockers) > 0 {
		t.Fatalf("expected no blockers, got %v", result.Blockers)
	}
	if result.ToState != "human_verification" {
		t.Fatalf("expected ToState human_verification, got %q", result.ToState)
	}
}

func TestValidTransitionHumanVerificationToImplementing(t *testing.T) {
	result := EvaluateTransition(TransitionInput{
		StatusBucket:    "started",
		CurrentState:    "human_verification",
		RequestedAction: "approve_for_implementation",
	})
	if len(result.Blockers) > 0 {
		t.Fatalf("expected no blockers, got %v", result.Blockers)
	}
	if result.ToState != "implementing" {
		t.Fatalf("expected ToState implementing, got %q", result.ToState)
	}
}

func TestValidTransitionImplementingToVerifying(t *testing.T) {
	result := EvaluateTransition(TransitionInput{
		StatusBucket:    "started",
		CurrentState:    "implementing",
		RequestedAction: "submit_for_verification",
	})
	if len(result.Blockers) > 0 {
		t.Fatalf("expected no blockers, got %v", result.Blockers)
	}
	if result.ToState != "verifying" {
		t.Fatalf("expected ToState verifying, got %q", result.ToState)
	}
}

func TestValidTransitionVerifyingToReviewing(t *testing.T) {
	result := EvaluateTransition(TransitionInput{
		StatusBucket:    "started",
		CurrentState:    "verifying",
		RequestedAction: "submit_for_review",
	})
	if len(result.Blockers) > 0 {
		t.Fatalf("expected no blockers, got %v", result.Blockers)
	}
	if result.ToState != "reviewing" {
		t.Fatalf("expected ToState reviewing, got %q", result.ToState)
	}
}

func TestValidTransitionReviewingToImplementing(t *testing.T) {
	result := EvaluateTransition(TransitionInput{
		StatusBucket:    "started",
		CurrentState:    "reviewing",
		RequestedAction: "request_changes",
	})
	if len(result.Blockers) > 0 {
		t.Fatalf("expected no blockers, got %v", result.Blockers)
	}
	if result.ToState != "implementing" {
		t.Fatalf("expected ToState implementing, got %q", result.ToState)
	}
}

func TestValidTransitionReviewingToInvestigating(t *testing.T) {
	result := EvaluateTransition(TransitionInput{
		StatusBucket:    "started",
		CurrentState:    "reviewing",
		RequestedAction: "request_investigation",
	})
	if len(result.Blockers) > 0 {
		t.Fatalf("expected no blockers, got %v", result.Blockers)
	}
	if result.ToState != "investigating" {
		t.Fatalf("expected ToState investigating, got %q", result.ToState)
	}
}

func TestValidTransitionInvestigatingToReviewing(t *testing.T) {
	result := EvaluateTransition(TransitionInput{
		StatusBucket:    "started",
		CurrentState:    "investigating",
		RequestedAction: "complete_investigation",
	})
	if len(result.Blockers) > 0 {
		t.Fatalf("expected no blockers, got %v", result.Blockers)
	}
	if result.ToState != "reviewing" {
		t.Fatalf("expected ToState reviewing, got %q", result.ToState)
	}
}

func TestValidTransitionBlockedToPlanned(t *testing.T) {
	result := EvaluateTransition(TransitionInput{
		StatusBucket:    "started",
		CurrentState:    "blocked",
		RequestedAction: "resolve_blocker",
	})
	if len(result.Blockers) > 0 {
		t.Fatalf("expected no blockers, got %v", result.Blockers)
	}
	if result.ToState != "planned" {
		t.Fatalf("expected ToState planned, got %q", result.ToState)
	}
}

func TestMarkBlockedFromMultipleStates(t *testing.T) {
	for _, state := range []string{"implementing", "verifying", "investigating"} {
		t.Run(state, func(t *testing.T) {
			result := EvaluateTransition(TransitionInput{
				StatusBucket:    "started",
				CurrentState:    state,
				RequestedAction: "mark_blocked",
			})
			if len(result.Blockers) > 0 {
				t.Fatalf("expected no blockers, got %v", result.Blockers)
			}
			if result.ToState != "blocked" {
				t.Fatalf("expected ToState blocked, got %q", result.ToState)
			}
		})
	}
}

func TestUnstartedStartTransition(t *testing.T) {
	result := EvaluateTransition(TransitionInput{
		StatusBucket:    "unstarted",
		CurrentState:    "",
		RequestedAction: "start",
	})
	if len(result.Blockers) > 0 {
		t.Fatalf("expected no blockers, got %v", result.Blockers)
	}
	if result.ToState != "planned" {
		t.Fatalf("expected ToState planned, got %q", result.ToState)
	}
}

func TestUnstartedRejectsNonStartAction(t *testing.T) {
	result := EvaluateTransition(TransitionInput{
		StatusBucket:    "unstarted",
		CurrentState:    "",
		RequestedAction: "start_implementation",
	})
	if len(result.Blockers) == 0 {
		t.Fatal("expected blockers for non-start action on unstarted bucket")
	}
	if result.Blockers[0].Code != "invalid_action" {
		t.Fatalf("expected blocker code invalid_action, got %q", result.Blockers[0].Code)
	}
}

func TestTransitionPurity_NoForbiddenImports(t *testing.T) {
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			break
		}
		next := filepath.Dir(dir)
		if next == dir {
			t.Fatal("could not find repo root")
		}
		dir = next
	}
	data, err := os.ReadFile(filepath.Join(dir, "internal", "productworkflow", "transition.go"))
	if err != nil {
		t.Fatalf("read transition.go: %v", err)
	}
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
		if strings.Contains(src, f) {
			t.Fatalf("transition.go must not import %q (purity constraint)", f)
		}
	}
}

func TestNextActionsForClosedStatusBucketReturnsEmpty(t *testing.T) {
	actions, blockers := NextActionsForState("closed", "closed")
	if len(blockers) > 0 {
		t.Fatalf("expected no blockers, got %v", blockers)
	}
	if len(actions) > 0 {
		t.Fatalf("expected no actions for closed, got %v", actions)
	}
}

func TestNextActionsStableOrder_Planned(t *testing.T) {
	actions, blockers := NextActionsForState("started", "planned")
	if len(blockers) > 0 {
		t.Fatalf("expected no blockers, got %v", blockers)
	}
	want := []TransitionAction{
		{Action: "submit_for_human_verification", Owner: "planner"},
		{Action: "start_investigation", Owner: "investigator"},
	}
	if len(actions) != len(want) {
		t.Fatalf("expected %d actions, got %d: %v", len(want), len(actions), actions)
	}
	for i := range want {
		if actions[i] != want[i] {
			t.Fatalf("action[%d]: expected %+v, got %+v", i, want[i], actions[i])
		}
	}
}

func TestNextActionsStableOrder_HumanVerification(t *testing.T) {
	actions, blockers := NextActionsForState("started", "human_verification")
	if len(blockers) > 0 {
		t.Fatalf("expected no blockers, got %v", blockers)
	}
	want := []TransitionAction{
		{Action: "approve_for_implementation", Owner: "operator"},
		{Action: "request_changes", Owner: "operator"},
		{Action: "request_investigation", Owner: "operator"},
	}
	if len(actions) != len(want) {
		t.Fatalf("expected %d actions, got %d: %v", len(want), len(actions), actions)
	}
	for i := range want {
		if actions[i] != want[i] {
			t.Fatalf("action[%d]: expected %+v, got %+v", i, want[i], actions[i])
		}
	}
}

func TestNextActionsStableOrder_Reviewing(t *testing.T) {
	actions, blockers := NextActionsForState("started", "reviewing")
	if len(blockers) > 0 {
		t.Fatalf("expected no blockers, got %v", blockers)
	}
	want := []TransitionAction{
		{Action: "request_changes", Owner: "reviewer"},
		{Action: "request_investigation", Owner: "orchestrator"},
	}
	if len(actions) != len(want) {
		t.Fatalf("expected %d actions, got %d: %v", len(want), len(actions), actions)
	}
	for i := range want {
		if actions[i] != want[i] {
			t.Fatalf("action[%d]: expected %+v, got %+v", i, want[i], actions[i])
		}
	}
}

func TestNextActionsStableOrder_Implementing(t *testing.T) {
	actions, blockers := NextActionsForState("started", "implementing")
	if len(blockers) > 0 {
		t.Fatalf("expected no blockers, got %v", blockers)
	}
	want := []TransitionAction{
		{Action: "submit_for_verification", Owner: "implementor"},
		{Action: "mark_blocked", Owner: "orchestrator"},
	}
	if len(actions) != len(want) {
		t.Fatalf("expected %d actions, got %d: %v", len(want), len(actions), actions)
	}
	for i := range want {
		if actions[i] != want[i] {
			t.Fatalf("action[%d]: expected %+v, got %+v", i, want[i], actions[i])
		}
	}
}

func TestStableOrderHoldsAcrossRepeatedCalls(t *testing.T) {
	for run := 0; run < 20; run++ {
		actions, _ := NextActionsForState("started", "planned")
		if len(actions) != 2 || actions[0].Action != "submit_for_human_verification" || actions[1].Action != "start_investigation" {
			t.Fatalf("run %d: ordering changed: %v", run, actions)
		}
	}
}

func TestInvalidStateBucketConsistency_StartedBucketWithClosedState(t *testing.T) {
	result := EvaluateTransition(TransitionInput{
		StatusBucket:    "started",
		CurrentState:    "closed",
		RequestedAction: "start_implementation",
	})
	if len(result.Blockers) == 0 {
		t.Fatal("expected blocker for started bucket with closed state")
	}
	if result.Blockers[0].Code != "invalid_state_bucket" {
		t.Fatalf("expected blocker code invalid_state_bucket, got %q", result.Blockers[0].Code)
	}
}

func TestInvalidStateBucketConsistency_ClosedBucketWithNonClosedState(t *testing.T) {
	result := EvaluateTransition(TransitionInput{
		StatusBucket:    "closed",
		CurrentState:    "planned",
		RequestedAction: "start_implementation",
	})
	if len(result.Blockers) == 0 {
		t.Fatal("expected blocker for closed bucket with non-closed state")
	}
	if result.Blockers[0].Code != "invalid_state_bucket" {
		t.Fatalf("expected blocker code invalid_state_bucket, got %q", result.Blockers[0].Code)
	}
}

func TestInvalidStateBucketConsistency_UnstartedBucketWithImplementingState(t *testing.T) {
	result := EvaluateTransition(TransitionInput{
		StatusBucket:    "unstarted",
		CurrentState:    "implementing",
		RequestedAction: "start",
	})
	if len(result.Blockers) == 0 {
		t.Fatal("expected blocker for unstarted bucket with implementing state")
	}
	if result.Blockers[0].Code != "invalid_state_bucket" {
		t.Fatalf("expected blocker code invalid_state_bucket, got %q", result.Blockers[0].Code)
	}
}

func TestInvalidStateBucketConsistency_NextActionsForState_ClosedBucketNonClosedState(t *testing.T) {
	_, blockers := NextActionsForState("closed", "planned")
	if len(blockers) == 0 {
		t.Fatal("expected blocker for closed bucket with non-closed state via NextActionsForState")
	}
	if blockers[0].Code != "invalid_state_bucket" {
		t.Fatalf("expected blocker code invalid_state_bucket, got %q", blockers[0].Code)
	}
}

func TestInvalidStateBucketConsistency_NextActionsForState_StartedBucketClosedState(t *testing.T) {
	_, blockers := NextActionsForState("started", "closed")
	if len(blockers) == 0 {
		t.Fatal("expected blocker for started bucket with closed state via NextActionsForState")
	}
	if blockers[0].Code != "invalid_state_bucket" {
		t.Fatalf("expected blocker code invalid_state_bucket, got %q", blockers[0].Code)
	}
}

func TestInvalidStateBucketConsistency_NextActionsForState_UnstartedBucketImplementingState(t *testing.T) {
	actions, blockers := NextActionsForState("unstarted", "implementing")
	if len(blockers) == 0 {
		t.Fatal("expected blocker for unstarted bucket with implementing state via NextActionsForState")
	}
	if blockers[0].Code != "invalid_state_bucket" {
		t.Fatalf("expected blocker code invalid_state_bucket, got %q", blockers[0].Code)
	}
	if len(actions) != 0 {
		t.Fatalf("expected no actions, got %v", actions)
	}
}

func TestValidStateBucketConsistency_UnstartedBucketEmptyState(t *testing.T) {
	actions, blockers := NextActionsForState("unstarted", "")
	if len(blockers) > 0 {
		t.Fatalf("expected no blockers, got %v", blockers)
	}
	if len(actions) != 1 || actions[0].Action != "start" {
		t.Fatalf("expected [start], got %v", actions)
	}
}

func TestValidStateBucketConsistency_UnstartedBucketPlannedState(t *testing.T) {
	actions, blockers := NextActionsForState("unstarted", "planned")
	if len(blockers) > 0 {
		t.Fatalf("expected no blockers, got %v", blockers)
	}
	if len(actions) != 1 || actions[0].Action != "start" {
		t.Fatalf("expected [start], got %v", actions)
	}
}

func allGatesPassed() []GateResult {
	return []GateResult{
		{Gate: "verification", Status: "passed"},
		{Gate: "review", Status: "passed"},
		{Gate: "investigation", Status: "passed"},
	}
}
