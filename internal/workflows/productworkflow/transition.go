package productworkflow

type TransitionInput struct {
	StatusBucket    string
	CurrentState    string
	RequestedAction string
	OpenBlockers    []string
	OpenObligations []string
	GateResults     []GateResult
}

type TransitionResult struct {
	FromState        string
	ToState          string
	NextValidActions []TransitionAction
	Blockers         []TransitionBlocker
}

type TransitionAction struct {
	Action string
	Owner  string
}

type TransitionBlocker struct {
	Code    string
	Message string
}

var validStates = map[string]string{
	"planned":            "started",
	"human_verification": "started",
	"implementing":       "started",
	"verifying":          "started",
	"reviewing":          "started",
	"investigating":      "started",
	"blocked":            "started",
	"closed":             "closed",
}

var validStatusBuckets = map[string]bool{
	"unstarted": true,
	"started":   true,
	"closed":    true,
}

func validateStateBucketConsistency(statusBucket, currentState string) *TransitionBlocker {
	if statusBucket == "closed" && currentState != "closed" {
		return &TransitionBlocker{
			Code:    "invalid_state_bucket",
			Message: "status bucket closed requires current_state closed, got " + currentState,
		}
	}
	if statusBucket == "started" && currentState == "closed" {
		return &TransitionBlocker{
			Code:    "invalid_state_bucket",
			Message: "status bucket started is incompatible with current_state closed",
		}
	}
	if statusBucket == "unstarted" && currentState != "" && currentState != "planned" {
		return &TransitionBlocker{
			Code:    "invalid_state_bucket",
			Message: "status bucket unstarted requires empty or planned current_state, got " + currentState,
		}
	}
	return nil
}

type transitionRule struct {
	ToState string
	Owner   string
}

type orderedAction struct {
	Action string
	Rule   transitionRule
}

type orderedStateEntry struct {
	State   string
	Actions []orderedAction
}

var orderedTransitionTable = []orderedStateEntry{
	{
		State: "planned",
		Actions: []orderedAction{
			{Action: "submit_for_human_verification", Rule: transitionRule{ToState: "human_verification", Owner: "planner"}},
			{Action: "start_investigation", Rule: transitionRule{ToState: "investigating", Owner: "investigator"}},
		},
	},
	{
		State: "human_verification",
		Actions: []orderedAction{
			{Action: "approve_for_implementation", Rule: transitionRule{ToState: "implementing", Owner: "operator"}},
			{Action: "request_changes", Rule: transitionRule{ToState: "planned", Owner: "operator"}},
			{Action: "request_investigation", Rule: transitionRule{ToState: "investigating", Owner: "operator"}},
		},
	},
	{
		State: "implementing",
		Actions: []orderedAction{
			{Action: "submit_for_verification", Rule: transitionRule{ToState: "verifying", Owner: "implementor"}},
			{Action: "mark_blocked", Rule: transitionRule{ToState: "blocked", Owner: "orchestrator"}},
		},
	},
	{
		State: "verifying",
		Actions: []orderedAction{
			{Action: "submit_for_review", Rule: transitionRule{ToState: "reviewing", Owner: "reviewer"}},
			{Action: "mark_blocked", Rule: transitionRule{ToState: "blocked", Owner: "orchestrator"}},
		},
	},
	{
		State: "reviewing",
		Actions: []orderedAction{
			{Action: "approve", Rule: transitionRule{ToState: "closed", Owner: "reviewer"}},
			{Action: "request_changes", Rule: transitionRule{ToState: "implementing", Owner: "reviewer"}},
			{Action: "request_investigation", Rule: transitionRule{ToState: "investigating", Owner: "orchestrator"}},
		},
	},
	{
		State: "investigating",
		Actions: []orderedAction{
			{Action: "complete_investigation", Rule: transitionRule{ToState: "reviewing", Owner: "investigator"}},
			{Action: "mark_blocked", Rule: transitionRule{ToState: "blocked", Owner: "orchestrator"}},
		},
	},
	{
		State: "blocked",
		Actions: []orderedAction{
			{Action: "resolve_blocker", Rule: transitionRule{ToState: "planned", Owner: "orchestrator"}},
		},
	},
	{
		State:   "closed",
		Actions: nil,
	},
}

func findStateEntry(state string) *orderedStateEntry {
	for i := range orderedTransitionTable {
		if orderedTransitionTable[i].State == state {
			return &orderedTransitionTable[i]
		}
	}
	return nil
}

func findActionRule(entry *orderedStateEntry, action string) (transitionRule, bool) {
	for _, a := range entry.Actions {
		if a.Action == action {
			return a.Rule, true
		}
	}
	return transitionRule{}, false
}

var unstartedActions = []TransitionAction{
	{Action: "start", Owner: "orchestrator"},
}

var closePreconditionGates = []string{"verification", "review", "investigation"}

func EvaluateTransition(input TransitionInput) TransitionResult {
	var blockers []TransitionBlocker

	if !validStatusBuckets[input.StatusBucket] {
		blockers = append(blockers, TransitionBlocker{
			Code:    "invalid_status_bucket",
			Message: "status bucket " + input.StatusBucket + " is not valid",
		})
		return TransitionResult{
			FromState: input.CurrentState,
			Blockers:  blockers,
		}
	}

	if _, ok := validStates[input.CurrentState]; !ok && input.CurrentState != "" {
		blockers = append(blockers, TransitionBlocker{
			Code:    "invalid_state",
			Message: "state " + input.CurrentState + " is not valid",
		})
		return TransitionResult{
			FromState: input.CurrentState,
			Blockers:  blockers,
		}
	}

	if consistencyBlocker := validateStateBucketConsistency(input.StatusBucket, input.CurrentState); consistencyBlocker != nil {
		blockers = append(blockers, *consistencyBlocker)
		return TransitionResult{
			FromState: input.CurrentState,
			Blockers:  blockers,
		}
	}

	if input.StatusBucket == "unstarted" {
		if input.RequestedAction == "start" {
			return TransitionResult{
				FromState:        input.CurrentState,
				ToState:          "planned",
				NextValidActions: unstartedActions,
			}
		}
		blockers = append(blockers, TransitionBlocker{
			Code:    "invalid_action",
			Message: "action " + input.RequestedAction + " is not valid for status bucket unstarted",
		})
		return TransitionResult{
			FromState: input.CurrentState,
			Blockers:  blockers,
		}
	}

	if input.StatusBucket == "closed" {
		blockers = append(blockers, TransitionBlocker{
			Code:    "terminal_status_bucket",
			Message: "no transitions allowed from closed status bucket",
		})
		return TransitionResult{
			FromState: input.CurrentState,
			Blockers:  blockers,
		}
	}

	entry := findStateEntry(input.CurrentState)
	if entry == nil {
		blockers = append(blockers, TransitionBlocker{
			Code:    "invalid_state",
			Message: "state " + input.CurrentState + " is not valid",
		})
		return TransitionResult{
			FromState: input.CurrentState,
			Blockers:  blockers,
		}
	}

	rule, actionOK := findActionRule(entry, input.RequestedAction)
	if !actionOK {
		blockers = append(blockers, TransitionBlocker{
			Code:    "invalid_action",
			Message: "action " + input.RequestedAction + " is not valid for state " + input.CurrentState,
		})
		nextActions := nextActionsFor(input.CurrentState, input.GateResults)
		return TransitionResult{
			FromState:        input.CurrentState,
			NextValidActions: nextActions,
			Blockers:         blockers,
		}
	}

	if rule.ToState == "closed" {
		closeBlockers := checkClosePreconditions(input.OpenBlockers, input.OpenObligations, input.GateResults)
		if len(closeBlockers) > 0 {
			nextActions := nextActionsFor(input.CurrentState, input.GateResults)
			return TransitionResult{
				FromState:        input.CurrentState,
				NextValidActions: nextActions,
				Blockers:         closeBlockers,
			}
		}
	}

	nextActions := nextActionsFor(rule.ToState, input.GateResults)
	return TransitionResult{
		FromState:        input.CurrentState,
		ToState:          rule.ToState,
		NextValidActions: nextActions,
		Blockers:         nil,
	}
}

func NextActionsForState(statusBucket, currentState string) ([]TransitionAction, []TransitionBlocker) {
	var blockers []TransitionBlocker

	if !validStatusBuckets[statusBucket] {
		blockers = append(blockers, TransitionBlocker{
			Code:    "invalid_status_bucket",
			Message: "status bucket " + statusBucket + " is not valid",
		})
		return nil, blockers
	}

	if consistencyBlocker := validateStateBucketConsistency(statusBucket, currentState); consistencyBlocker != nil {
		return nil, []TransitionBlocker{*consistencyBlocker}
	}

	if statusBucket == "unstarted" {
		return unstartedActions, nil
	}

	if statusBucket == "closed" {
		return nil, nil
	}

	if _, ok := validStates[currentState]; !ok {
		blockers = append(blockers, TransitionBlocker{
			Code:    "invalid_state",
			Message: "state " + currentState + " is not valid",
		})
		return nil, blockers
	}

	actions := nextActionsFor(currentState, nil)
	return actions, nil
}

func nextActionsFor(state string, gateResults []GateResult) []TransitionAction {
	var entry *orderedStateEntry
	for i := range orderedTransitionTable {
		if orderedTransitionTable[i].State == state {
			entry = &orderedTransitionTable[i]
			break
		}
	}
	if entry == nil {
		return nil
	}
	actions := make([]TransitionAction, 0, len(entry.Actions))
	for _, a := range entry.Actions {
		if a.Action == "approve" && state == "reviewing" {
			if !closePreconditionsMet(nil, nil, gateResults) {
				continue
			}
		}
		actions = append(actions, TransitionAction{Action: a.Action, Owner: a.Rule.Owner})
	}
	return actions
}

func checkClosePreconditions(openBlockers, openObligations []string, gateResults []GateResult) []TransitionBlocker {
	var blockers []TransitionBlocker

	if len(openBlockers) > 0 {
		blockers = append(blockers, TransitionBlocker{
			Code:    "open_blockers",
			Message: "close requires no open blockers",
		})
	}

	if len(openObligations) > 0 {
		blockers = append(blockers, TransitionBlocker{
			Code:    "open_obligations",
			Message: "close requires no open obligations",
		})
	}

	gateStatus := gateStatusMap(gateResults)
	for _, gate := range closePreconditionGates {
		status, ok := gateStatus[gate]
		if !ok {
			blockers = append(blockers, TransitionBlocker{
				Code:    "missing_gate",
				Message: "close requires " + gate + " gate result",
			})
		} else if status != "passed" {
			blockers = append(blockers, TransitionBlocker{
				Code:    "gate_not_passed",
				Message: "close requires " + gate + " gate passed, got " + status,
			})
		}
	}

	return blockers
}

func closePreconditionsMet(openBlockers, openObligations []string, gateResults []GateResult) bool {
	return len(checkClosePreconditions(openBlockers, openObligations, gateResults)) == 0
}

func gateStatusMap(gateResults []GateResult) map[string]string {
	m := make(map[string]string, len(gateResults))
	for _, g := range gateResults {
		m[g.Gate] = g.Status
	}
	return m
}
