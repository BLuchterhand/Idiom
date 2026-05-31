package featureplanner

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

type PromptRunner func(prompt string, taskFamily string) (string, error)

type RunOptions struct {
	Prompt                string
	PromptFile            string
	MaxPlanningIterations int
	MaxAtoms              int
	ResumeAnswer          string
	Now                   time.Time
	ProductWorkflowBrief  *ProductWorkflowBrief
}

type RunResult struct {
	State  FeatureState
	Events []Event
}

const EventPlanExecuteGitHistoryRequirement = "idiom.feature_planner.plan_execute_git_history_requirement"

func PlanExecuteGitHistoryRequirementPresent(prompt string) bool {
	return strings.Contains(prompt, "Git history evidence required before writes:") &&
		strings.Contains(prompt, "history_range") &&
		strings.Contains(prompt, "history_symbol") &&
		strings.Contains(prompt, "commit descriptions")
}

func PlanExecuteGitHistoryRequirementFields() map[string]any {
	return map[string]any{
		"tool":                         "git_history_requirement",
		"lane_family":                  "plan-execute",
		"evidence_tools":               []string{"history_range", "history_symbol"},
		"commit_descriptions_required": true,
	}
}

func Run(state FeatureState, opts RunOptions, runner PromptRunner) (RunResult, error) {
	if runner == nil {
		return RunResult{State: state}, fmt.Errorf("feature planner prompt runner is nil")
	}
	if state.PlanningLoop.MaxIterations <= 0 {
		state.PlanningLoop.MaxIterations = firstPositive(opts.MaxPlanningIterations, 3)
	}
	var events []Event
	if strings.TrimSpace(opts.ResumeAnswer) != "" {
		events = append(events, answerOpenQuestion(&state, opts.ResumeAnswer)...)
		if state.Status == FeatureStatePlanningQuestion {
			return RunResult{State: state, Events: events}, nil
		}
	}
	if len(state.ActiveRuns) > 0 {
		return RunResult{
			State: state,
			Events: append(events, Event{
				Type:      "idiom.feature_planner.resume_active_run",
				SessionID: state.SessionID,
				Fields:    map[string]any{"active_runs": len(state.ActiveRuns)},
			}),
		}, nil
	}
	if shouldResumeDispatch(state) {
		events = append(events, DispatchReadyAtoms(&state, opts.MaxAtoms, runner)...)
		return RunResult{State: state, Events: events}, nil
	}
	if state.PlanningLoop.Iteration >= state.PlanningLoop.MaxIterations {
		MarkPlanningStuck(&state, "")
		return RunResult{
			State:  state,
			Events: []Event{{Type: "idiom.feature_planner.planning_stuck", SessionID: state.SessionID, Message: state.PlanningLoop.StuckReason}},
		}, nil
	}

	plannerPrompt := BuildPlannerPrompt(opts.Prompt, state, opts.ProductWorkflowBrief)
	answer, err := runner(plannerPrompt, "feature_planner_planner")
	if err != nil {
		state.Status = FeatureStateFailed
		state.LastError = err.Error()
		return RunResult{State: state}, err
	}
	parsed, err := ParsePlannerOutput(answer)
	if err != nil {
		state.Status = FeatureStateMalformedOutput
		state.PlanningLoop.State = FeatureStateMalformedOutput
		state.LastError = err.Error()
		return RunResult{
			State:  state,
			Events: []Event{{Type: "idiom.feature_planner.malformed_structured_output", SessionID: state.SessionID, Message: err.Error()}},
		}, nil
	}
	if parsed.Kind == ToolSubmitFeaturePlan && opts.ProductWorkflowBrief != nil {
		if err := ValidatePlanProductWorkflowContext(parsed.Plan, *opts.ProductWorkflowBrief); err != nil {
			state.Status = FeatureStatePlanningRepair
			state.PlanningLoop.State = FeatureStatePlanningRepair
			state.Blockers = append(state.Blockers, err.Error())
			return RunResult{
				State:  state,
				Events: []Event{{Type: "idiom.feature_planner.product_context_blocked", SessionID: state.SessionID, Message: err.Error()}},
			}, nil
		}
	}
	events = append(events, ApplyPlannerOutput(&state, parsed, opts.Now)...)
	if state.Status != FeatureStateDispatching {
		return RunResult{State: state, Events: events}, nil
	}
	dispatchEvents := DispatchReadyAtoms(&state, opts.MaxAtoms, runner)
	events = append(events, dispatchEvents...)
	return RunResult{State: state, Events: events}, nil
}

func BuildPlannerPrompt(userPrompt string, state FeatureState, brief *ProductWorkflowBrief) string {
	var b strings.Builder
	b.WriteString("You are the feature planner. Produce exactly one JSON object.\n")
	b.WriteString("Valid tools: submit_feature_plan or ask_planner_question.\n")
	b.WriteString("For submit_feature_plan, return the FeaturePlan object itself with tool=\"submit_feature_plan\" at the top level. Do not nest the plan under a plan, payload, arguments, or input field.\n")
	b.WriteString("Every atom must name at least one concrete required_edit_paths or allowed_edit_paths entry. A no-mutation planning test still names the intended source or test paths as allowed scope; do not return both arrays empty.\n")

	if brief != nil {
		b.WriteString("\nProduct Workflow Context:\n")
		fmt.Fprintf(&b, "Effort: %s (task %s)\n", brief.EffortID, brief.TaskID)
		fmt.Fprintf(&b, "Coverage tier: %s, gate passed: %v\n", brief.CoverageTier, brief.CoverageGatePassed)

		if len(brief.PRDRequirements) > 0 {
			b.WriteString("PRD Requirements:\n")
			for _, req := range brief.PRDRequirements {
				fmt.Fprintf(&b, "  - %s [%s]: %s", req.ID, req.Priority, req.Statement)
				if len(req.Obligations) > 0 {
					fmt.Fprintf(&b, " (TDD obligations: %s)", strings.Join(req.Obligations, ", "))
				}
				b.WriteString("\n")
			}
		}
		if len(brief.AcceptedADRIDs) > 0 {
			fmt.Fprintf(&b, "Accepted ADRs: %s\n", strings.Join(brief.AcceptedADRIDs, ", "))
		}
		if len(brief.AcceptedADRPaths) > 0 {
			fmt.Fprintf(&b, "Accepted ADR paths: %s\n", strings.Join(brief.AcceptedADRPaths, ", "))
		}
		if len(brief.TDDObligations) > 0 {
			b.WriteString("TDD Obligations:\n")
			for _, obl := range brief.TDDObligations {
				fmt.Fprintf(&b, "  - %s: %s (command: %s, status: %s)\n", obl.ID, obl.Behavior, obl.Command, obl.Status)
			}
		}
		if len(brief.RequiredBehavior) > 0 {
			fmt.Fprintf(&b, "Required behavior: %s\n", strings.Join(brief.RequiredBehavior, "; "))
		}
		if len(brief.Acceptance) > 0 {
			fmt.Fprintf(&b, "Acceptance: %s\n", strings.Join(brief.Acceptance, "; "))
		}
		if len(brief.Decisions) > 0 {
			b.WriteString("Accepted decisions:\n")
			for _, decision := range brief.Decisions {
				fmt.Fprintf(&b, "  - %s\n", decision)
			}
		}
		if len(brief.FilesToModify) > 0 {
			fmt.Fprintf(&b, "Files to modify: %s\n", strings.Join(brief.FilesToModify, ", "))
		} else {
			b.WriteString("Files to modify: none supplied by product workflow localization.\n")
		}
		if len(brief.ContextEvidencePaths) > 0 {
			fmt.Fprintf(&b, "Source/context evidence paths: %s\n", strings.Join(brief.ContextEvidencePaths, ", "))
		}
		if len(brief.AuthorizedNewPackageRoots) > 0 {
			fmt.Fprintf(&b, "Authorized new package roots: %s\n", strings.Join(brief.AuthorizedNewPackageRoots, ", "))
		}
		if len(brief.Tests) > 0 {
			fmt.Fprintf(&b, "Tests: %s\n", strings.Join(brief.Tests, ", "))
		}
		if len(brief.Constraints) > 0 {
			fmt.Fprintf(&b, "Constraints: %s\n", strings.Join(brief.Constraints, "; "))
		}
		if len(brief.Blockers) > 0 {
			fmt.Fprintf(&b, "Blockers: %s\n", strings.Join(brief.Blockers, "; "))
		}
		if len(brief.CoverageBlockers) > 0 {
			fmt.Fprintf(&b, "Coverage blockers: %s\n", strings.Join(brief.CoverageBlockers, "; "))
		}

		if brief.CoverageGatePassed && len(brief.Blockers) == 0 && len(brief.CoverageBlockers) == 0 {
			b.WriteString("\nRequired submit_feature_plan shape:\n")
			fmt.Fprintf(&b, "{\"tool\":\"submit_feature_plan\",\"schema_version\":\"idiom.feature_plan.v2\",\"feature_id\":\"feature-id\",\"source_task\":{\"kind\":\"operator_prompt\",\"summary\":\"...\"},\"summary\":\"...\",\"top_level_scope\":{\"objective\":\"...\",\"success_criteria\":[\"...\"]},\"sdd_contract\":{\"prd_requirement_refs\":[\"REQ-001\"],\"adr_refs\":[\"ADR-001\"],\"acceptance\":[\"...\"]},\"tdd_contract\":{\"coverage_tier\":\"%s\",\"required_before_implementation\":true,\"obligations\":[{\"id\":\"TDD-1\",\"behavior\":\"...\",\"command\":\"go test ./...\",\"required_before\":\"implementation\"}],\"coverage_gate_event_id\":\"evt-cov-1\"},\"atoms\":[{\"schema_version\":\"idiom.feature_atom.v1\",\"id\":\"atom-1\",\"title\":\"...\",\"summary\":\"...\",\"required_edit_paths\":[\"use-only-product-grounded-paths\"],\"allowed_edit_paths\":[\"use-only-product-grounded-paths-or-sibling-tests\"],\"sdd_refs\":[\"REQ-001\"],\"tdd_obligations\":[\"TDD-1\"],\"required_tests\":[\"go test ./...\"],\"test_first\":{\"obligation_refs\":[\"TDD-1\"],\"commands\":[\"go test ./...\"],\"baseline_required\":true,\"test_artifact_paths\":[\"path_test.go\"],\"pre_execution_event_ids\":[\"evt-tdd-1\"],\"expected_pre_execution_result\":\"red-confirmed\",\"coverage_gate_event_id\":\"evt-tdd-1\"},\"planned_tdd_snapshot\":{\"snapshot_id\":\"snap-tdd-1\",\"snapshot_digest\":\"sha256:...\",\"obligation_ids\":[\"TDD-1\"],\"test_commands\":[\"go test ./...\"],\"test_artifact_paths\":[\"path_test.go\"],\"expected_pre_execution_results\":[\"red-confirmed\"],\"created_before_dispatch_event_id\":\"evt-tdd-1\"},\"prompt\":\"...\"}]}\n", brief.CoverageTier)
			b.WriteString("Your FeaturePlan SDD refs MUST reference only PRD requirement IDs and ADR IDs from the context above.\n")
			b.WriteString("Your TDD obligations MUST reference only the obligation IDs listed above.\n")
			b.WriteString("Repo grounding is required: required_edit_paths and allowed_edit_paths must come from Files to modify, source/context evidence paths, sibling files inside those existing package roots, or authorized new package roots.\n")
			b.WriteString("The schema example path placeholders are not authority. Do not copy or invent paths from examples.\n")
			if len(brief.FilesToModify) == 0 && len(brief.ContextEvidencePaths) == 0 && len(brief.AuthorizedNewPackageRoots) == 0 {
				b.WriteString("No Files to modify, source/context evidence paths, or authorized new package roots are present. Use ask_planner_question for localization repair instead of concrete required_edit_paths or allowed_edit_paths.\n")
			}
		} else if !brief.CoverageGatePassed {
			b.WriteString("\nThe coverage gate has NOT passed. Do not produce a feature plan; instead, use ask_planner_question to report the blocked gate.\n")
		} else {
			b.WriteString("\nThe product workflow has unresolved blockers. Do not produce a feature plan; instead, use ask_planner_question to report the blockers.\n")
		}
	} else {
		b.WriteString("\nRequired submit_feature_plan shape:\n")
		b.WriteString("{\"tool\":\"submit_feature_plan\",\"schema_version\":\"idiom.feature_plan.v2\",\"feature_id\":\"feature-id\",\"source_task\":{\"kind\":\"operator_prompt\",\"summary\":\"...\"},\"summary\":\"...\",\"top_level_scope\":{\"objective\":\"...\",\"success_criteria\":[\"...\"]},\"sdd_contract\":{\"prd_requirement_refs\":[],\"adr_refs\":[],\"acceptance\":[\"your-acceptance-criteria-here\"]},\"tdd_contract\":{\"coverage_tier\":\"contract_high\",\"required_before_implementation\":true,\"obligations\":[{\"id\":\"TDD-1\",\"behavior\":\"...\",\"command\":\"go test ./...\",\"required_before\":\"implementation\"}]},\"atoms\":[{\"schema_version\":\"idiom.feature_atom.v1\",\"id\":\"atom-1\",\"title\":\"...\",\"summary\":\"...\",\"required_edit_paths\":[\"internal/example/source.go\"],\"allowed_edit_paths\":[\"internal/example/source.go\",\"internal/example/source_test.go\"],\"sdd_refs\":[],\"tdd_obligations\":[\"TDD-1\"],\"required_tests\":[\"go test ./...\"],\"test_first\":{\"obligation_refs\":[\"TDD-1\"],\"commands\":[\"go test ./...\"],\"baseline_required\":true,\"test_artifact_paths\":[\"path_test.go\"],\"pre_execution_event_ids\":[\"evt-tdd-1\"],\"expected_pre_execution_result\":\"red-confirmed\",\"coverage_gate_event_id\":\"evt-tdd-1\"},\"planned_tdd_snapshot\":{\"snapshot_id\":\"snap-tdd-1\",\"snapshot_digest\":\"sha256:...\",\"obligation_ids\":[\"TDD-1\"],\"test_commands\":[\"go test ./...\"],\"test_artifact_paths\":[\"path_test.go\"],\"expected_pre_execution_results\":[\"red-confirmed\"],\"created_before_dispatch_event_id\":\"evt-tdd-1\"},\"prompt\":\"...\"}]}\n")
		b.WriteString("No upstream design docs are available. Use acceptance criteria strings in sdd_contract.acceptance instead of file paths.\n")
		b.WriteString("Do NOT invent file paths like docs/path.md — acceptance criteria strings are valid and preferred when no docs exist.\n")
	}

	b.WriteString("Before any atom can be dispatched, every atom must carry SDD refs and test_first commands tied to TDD obligations required_before implementation.\n")
	b.WriteString("Top-level scope is the smallest releasable feature objective; atoms are independently dispatchable implementation units.\n\n")
	if len(state.OpenQuestions) > 0 {
		b.WriteString("Open planner questions and answers:\n")
		for _, question := range state.OpenQuestions {
			fmt.Fprintf(&b, "- %s status=%s answer=%s\n", question.QuestionSummary, question.Status, question.Answer)
		}
		b.WriteString("\n")
	}
	b.WriteString("User request:\n")
	b.WriteString(strings.TrimSpace(userPrompt))
	b.WriteString("\n")
	return b.String()
}

func DispatchReadyAtoms(state *FeatureState, maxAtoms int, runner PromptRunner) []Event {
	if state == nil || runner == nil {
		return nil
	}
	limit := firstPositive(maxAtoms, len(state.Atoms))
	var events []Event
	dispatched := 0
	for i := range state.Atoms {
		if dispatched >= limit {
			break
		}
		atom := &state.Atoms[i]
		if atom.State != AtomStateQueued && atom.State != AtomStateDispatchable {
			continue
		}
		if !atomDependenciesSatisfied(state.Atoms, *atom) {
			continue
		}
		atomFrom := atom.State
		if missing := MissingPreDispatchTDD(*state, *atom); len(missing) > 0 {
			featureFrom := state.Status
			atom.State = AtomStateBlockedMissingTDD
			atom.Blockers = append(atom.Blockers, missing...)
			state.Status = FeatureStatePlanningRepair
			state.Blockers = append(state.Blockers, missing...)
			events = append(events, Event{
				Type:        "idiom.feature_planner.tdd_pre_execution_blocked",
				SessionID:   state.SessionID,
				AtomID:      atom.ID,
				FeatureFrom: featureFrom,
				FeatureTo:   FeatureStatePlanningRepair,
				AtomFrom:    atomFrom,
				AtomTo:      AtomStateBlockedMissingTDD,
				AtomDeltas:  []AtomDelta{{Op: "update", AtomID: atom.ID, AtomFrom: atomFrom, AtomTo: AtomStateBlockedMissingTDD}},
				Fields:      map[string]any{"missing": missing},
			})
			break
		}
		dispatched++
		atom.Attempt++
		featureFrom := state.Status
		atom.State = AtomStateImplementing
		runID := fmt.Sprintf("%s-run-%d", atom.ID, atom.Attempt)
		startedAt := time.Now().UTC().Format(time.RFC3339Nano)
		atom.LastRunID = runID
		state.ActiveRuns = append(state.ActiveRuns, ActiveRun{
			RunID:         runID,
			AtomID:        atom.ID,
			Actor:         "implementor",
			LaneSessionID: runID,
			LaneFamily:    "plan-execute",
			State:         "running",
			StartedAt:     startedAt,
			UpdatedAt:     startedAt,
		})
		events = append(events, Event{
			Type:        "idiom.feature_planner.atom_dispatched",
			SessionID:   state.SessionID,
			AtomID:      atom.ID,
			FeatureFrom: featureFrom,
			FeatureTo:   FeatureStateDispatching,
			AtomFrom:    atomFrom,
			AtomTo:      AtomStateImplementing,
			AtomDeltas:  []AtomDelta{{Op: "update", AtomID: atom.ID, AtomFrom: atomFrom, AtomTo: AtomStateImplementing}},
			ActiveRunDeltas: []ActiveRunDelta{{
				Op:            "insert",
				AtomID:        atom.ID,
				LaneSessionID: runID,
				LaneFamily:    "plan-execute",
				AfterState:    "running",
				StartedAt:     startedAt,
			}},
			Fields: map[string]any{"run_id": runID},
		})
		answer, err := runner(BuildImplementorPrompt(*atom, state.Plan), "feature_planner_implementor")
		if err != nil {
			atomFrom := atom.State
			atom.State = AtomStateFailed
			atom.Blockers = append(atom.Blockers, err.Error())
			foldActiveRun(state, atom.ID, runID)
			events = append(events, Event{
				Type:       "idiom.feature_planner.atom_failed",
				SessionID:  state.SessionID,
				AtomID:     atom.ID,
				AtomFrom:   atomFrom,
				AtomTo:     AtomStateFailed,
				AtomDeltas: []AtomDelta{{Op: "update", AtomID: atom.ID, AtomFrom: atomFrom, AtomTo: AtomStateFailed}},
				ActiveRunDeltas: []ActiveRunDelta{{
					Op:            "fold_terminal",
					AtomID:        atom.ID,
					LaneSessionID: runID,
					BeforeState:   "running",
					AfterState:    AtomStateFailed,
				}},
				Message: err.Error(),
			})
			break
		}
		beforeStatus := state.Status
		beforeAtomState := atom.State
		events = append(events, ApplyImplementorOutput(state, atom, answer)...)
		if featurePlannerDispatchShouldStopAfterAtom(beforeStatus, beforeAtomState, state.Status, atom.State) {
			break
		}
	}
	refreshContinuation(state)
	return events
}

func featurePlannerDispatchShouldStopAfterAtom(beforeStatus string, beforeAtomState string, afterStatus string, afterAtomState string) bool {
	if beforeStatus != afterStatus {
		switch strings.TrimSpace(afterStatus) {
		case FeatureStatePlanningQuestion, FeatureStatePlanningRepair, FeatureStateRecoveryOpen, FeatureStateBlocked, FeatureStateFailed:
			return true
		}
	}
	if beforeAtomState == afterAtomState {
		return false
	}
	switch strings.TrimSpace(afterAtomState) {
	case AtomStateQuestionForPlanner, AtomStateQuestionForOperator, AtomStateRecoveryOpen, AtomStateBlocked, AtomStateFailed, AtomStateBlockedMissingTDD:
		return true
	default:
		return false
	}
}

func BuildImplementorPrompt(atom FeatureAtomState, plan FeaturePlan) string {
	body, _ := json.MarshalIndent(atom.FeatureAtomPlan, "", "  ")
	var b strings.Builder
	fmt.Fprintf(&b, "Implement one feature atom from feature plan %q.\n", plan.Summary)
	b.WriteString("You may answer with JSON {\"tool\":\"question_for_planner\",...} when blocked, or with Decision: source-satisfied when no edit is required.\n")
	b.WriteString("Do not claim files were edited or tests were run unless those effects were produced by tool-backed execution visible to the harness; unsupported edit/test claims are rejected as recovery-open.\n")
	b.WriteString("Git history evidence required before writes:\n")
	b.WriteString("- Before changing existing logic in required_edit_paths or allowed_edit_paths, call `history_range` or `history_symbol` on the intended logic and read the commit descriptions to understand why and when the current behavior was introduced.\n")
	fmt.Fprintf(&b, "- Make the proposed change only when that history supports it; otherwise answer with JSON {\"tool\":%q,...} naming the path-specific blocker.\n", ToolQuestionForPlanner)
	b.WriteString("- Genuinely new files do not require git history evidence; identify them as new-file edits before writing.\n")
	if paths := featureAtomWriteScopePaths(atom); len(paths) > 0 {
		b.WriteString("Paths whose existing logic needs history evidence before mutation:\n")
		for _, path := range paths {
			fmt.Fprintf(&b, "- %s\n", path)
		}
	}
	b.WriteString("TDD baseline commands that must exist before execution:\n")
	for _, command := range atom.TestFirst.Commands {
		fmt.Fprintf(&b, "- %s\n", command)
	}
	requiredTests := append([]string(nil), atom.RequiredTests...)
	if len(requiredTests) == 0 {
		requiredTests = append(requiredTests, atom.TestFirst.Commands...)
	}
	if len(requiredTests) > 0 {
		b.WriteString("Requested verification commands for executor phase:\n")
		for _, command := range requiredTests {
			fmt.Fprintf(&b, "- Required verification command: run `%s` after edits.\n", command)
		}
	}
	if strings.TrimSpace(atom.Prompt) != "" {
		b.WriteString("\nAtom implementation brief:\n")
		b.WriteString(strings.TrimSpace(atom.Prompt))
		b.WriteString("\n")
	}
	b.WriteString("\nAtom contract:\n")
	b.Write(body)
	b.WriteString("\n")
	return b.String()
}

func featureAtomWriteScopePaths(atom FeatureAtomState) []string {
	seen := map[string]struct{}{}
	paths := make([]string, 0, len(atom.RequiredEditPaths)+len(atom.AllowedEditPaths))
	for _, path := range append(append([]string(nil), atom.RequiredEditPaths...), atom.AllowedEditPaths...) {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	return paths
}

func ApplyImplementorOutput(state *FeatureState, atom *FeatureAtomState, raw string) []Event {
	if atom == nil || state == nil {
		return nil
	}
	trimmed := strings.TrimSpace(raw)
	if strings.HasPrefix(trimmed, "{") {
		var envelope struct {
			Tool            string `json:"tool"`
			LaneFamily      string `json:"lane_family"`
			Summary         string `json:"summary"`
			ProgressPath    string `json:"progress_path"`
			QuestionSummary string `json:"question_summary"`
			BlockingClaim   string `json:"blocking_claim"`
		}
		if err := json.Unmarshal([]byte(trimmed), &envelope); err != nil {
			return rejectUnverifiedCompletion(state, atom, "implementor returned malformed JSON completion envelope: "+err.Error())
		}
		tool := strings.TrimSpace(envelope.Tool)
		if tool == ToolPlanExecuteComplete {
			if err := validatePlanExecuteCompletionEnvelope(envelope.Summary, envelope.ProgressPath); err != nil {
				return rejectUnverifiedCompletion(state, atom, err.Error())
			}
			return completeAtomFromPlanExecute(state, atom, envelope.Summary, envelope.LaneFamily, envelope.ProgressPath)
		}
		if tool != "" && tool != ToolQuestionForPlanner {
			return rejectUnverifiedCompletion(state, atom, fmt.Sprintf("implementor returned unsupported completion tool %q; expected %q with progress_path or %q for blockers", tool, ToolPlanExecuteComplete, ToolQuestionForPlanner))
		}
		if tool == "" {
			return rejectUnverifiedCompletion(state, atom, fmt.Sprintf("implementor returned JSON without a supported tool; expected %q with progress_path or %q for blockers", ToolPlanExecuteComplete, ToolQuestionForPlanner))
		}
		questionShaped := tool == ToolQuestionForPlanner
		var question PlannerQuestion
		if questionShaped {
			err := json.Unmarshal([]byte(trimmed), &question)
			if err != nil {
				atom.Blockers = append(atom.Blockers, err.Error())
				state.Status = FeatureStatePlanningRepair
				return []Event{{Type: "idiom.feature_planner.planner_question_rejected", SessionID: state.SessionID, AtomID: atom.ID, Message: err.Error()}}
			}
			if strings.TrimSpace(question.QuestionSummary) == "" {
				question.QuestionSummary = "implementor requested planner clarification"
			}
			question = normalizePlannerQuestion(question, fmt.Sprintf("q-%03d", len(state.OpenQuestions)+1), atom.ID, time.Now().UTC())
			if err := ValidatePlannerQuestion(question); err != nil {
				atom.Blockers = append(atom.Blockers, err.Error())
				state.Status = FeatureStatePlanningRepair
				return []Event{{Type: "idiom.feature_planner.planner_question_rejected", SessionID: state.SessionID, AtomID: atom.ID, Message: err.Error()}}
			}
			atomFrom := atom.State
			atom.State = AtomStateQuestionForPlanner
			atom.LastQuestion = question.QuestionSummary
			foldActiveRun(state, atom.ID, atom.LastRunID)
			state.OpenQuestions = append(state.OpenQuestions, question)
			state.Status = FeatureStatePlanningQuestion
			return []Event{{
				Type:       "idiom.feature_planner.implementor_question_opened",
				SessionID:  state.SessionID,
				AtomID:     atom.ID,
				AtomFrom:   atomFrom,
				AtomTo:     AtomStateQuestionForPlanner,
				AtomDeltas: []AtomDelta{{Op: "update", AtomID: atom.ID, AtomFrom: atomFrom, AtomTo: AtomStateQuestionForPlanner}},
				ActiveRunDeltas: []ActiveRunDelta{{
					Op:            "fold_terminal",
					AtomID:        atom.ID,
					LaneSessionID: atom.LastRunID,
					BeforeState:   "running",
					AfterState:    AtomStateQuestionForPlanner,
				}},
				Message: question.QuestionSummary,
			}}
		}
	}
	if strings.HasPrefix(trimmed, "<<QUESTION>>:") {
		question := PlannerQuestion{
			QuestionSummary: strings.TrimSpace(strings.TrimPrefix(trimmed, "<<QUESTION>>:")),
			BlockingClaim:   "implementor could not complete the atom contract",
		}
		return openImplementorPlannerQuestion(state, atom, question)
	}
	if strings.Contains(strings.ToLower(trimmed), "decision: source-satisfied") {
		atomFrom := atom.State
		atom.State = AtomStateSourceSatisfied
		foldActiveRun(state, atom.ID, atom.LastRunID)
		state.Continuation.CompletedAtoms = appendUnique(state.Continuation.CompletedAtoms, atom.ID)
		featureTo := state.Status
		if allAtomsTerminal(state.Atoms) && state.Status != FeatureStatePlanningQuestion {
			state.Status = FeatureStateComplete
			featureTo = FeatureStateComplete
		}
		return []Event{{
			Type:       "idiom.feature_planner.atom_source_satisfied",
			SessionID:  state.SessionID,
			FeatureTo:  featureTo,
			AtomID:     atom.ID,
			AtomFrom:   atomFrom,
			AtomTo:     AtomStateSourceSatisfied,
			AtomDeltas: []AtomDelta{{Op: "update", AtomID: atom.ID, AtomFrom: atomFrom, AtomTo: AtomStateSourceSatisfied}},
			ActiveRunDeltas: []ActiveRunDelta{{
				Op:            "fold_terminal",
				AtomID:        atom.ID,
				LaneSessionID: atom.LastRunID,
				BeforeState:   "running",
				AfterState:    AtomStateSourceSatisfied,
			}},
		}}
	}
	if claimsUnverifiedEditOrTestEvidence(trimmed) {
		return rejectUnverifiedCompletion(state, atom, "implementor claimed edit/test evidence that was not observed by the feature-planner harness")
	}
	atomFrom := atom.State
	atom.State = AtomStateComplete
	atom.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
	foldActiveRun(state, atom.ID, atom.LastRunID)
	state.Continuation.CompletedAtoms = appendUnique(state.Continuation.CompletedAtoms, atom.ID)
	featureTo := state.Status
	if allAtomsTerminal(state.Atoms) && state.Status != FeatureStatePlanningQuestion {
		state.Status = FeatureStateComplete
		featureTo = FeatureStateComplete
	}
	return []Event{{
		Type:       "idiom.feature_planner.atom_completed",
		SessionID:  state.SessionID,
		FeatureTo:  featureTo,
		AtomID:     atom.ID,
		AtomFrom:   atomFrom,
		AtomTo:     AtomStateComplete,
		AtomDeltas: []AtomDelta{{Op: "update", AtomID: atom.ID, AtomFrom: atomFrom, AtomTo: AtomStateComplete}},
		ActiveRunDeltas: []ActiveRunDelta{{
			Op:            "fold_terminal",
			AtomID:        atom.ID,
			LaneSessionID: atom.LastRunID,
			BeforeState:   "running",
			AfterState:    AtomStateComplete,
		}},
	}}
}

func validatePlanExecuteCompletionEnvelope(summary string, progressPath string) error {
	summary = strings.TrimSpace(summary)
	progressPath = strings.TrimSpace(progressPath)
	if progressPath == "" {
		return errors.New("plan-execute completion missing progress_path; child terminal status is not auditable")
	}
	lower := strings.ToLower(summary)
	cleanedFailureSummary := strings.ToLower(summary)
	for _, marker := range []string{", 0 failed", ": 0 failed", "\n0 failed", " 0 failed"} {
		cleanedFailureSummary = strings.ReplaceAll(cleanedFailureSummary, marker, "")
	}
	if summary == "" ||
		strings.Contains(lower, "recovery_open") ||
		strings.Contains(lower, "recovery-open") ||
		strings.Contains(cleanedFailureSummary, "failed") ||
		strings.Contains(lower, "blocked") ||
		strings.Contains(lower, "<<question>>") {
		return errors.New("plan-execute completion summary is not a clean terminal completion")
	}
	return nil
}

func openImplementorPlannerQuestion(state *FeatureState, atom *FeatureAtomState, question PlannerQuestion) []Event {
	if strings.TrimSpace(question.QuestionSummary) == "" {
		question.QuestionSummary = "implementor requested planner clarification"
	}
	question = normalizePlannerQuestion(question, fmt.Sprintf("q-%03d", len(state.OpenQuestions)+1), atom.ID, time.Now().UTC())
	if err := ValidatePlannerQuestion(question); err != nil {
		atom.Blockers = append(atom.Blockers, err.Error())
		state.Status = FeatureStatePlanningRepair
		return []Event{{Type: "idiom.feature_planner.planner_question_rejected", SessionID: state.SessionID, AtomID: atom.ID, Message: err.Error()}}
	}
	atomFrom := atom.State
	atom.State = AtomStateQuestionForPlanner
	atom.LastQuestion = question.QuestionSummary
	foldActiveRun(state, atom.ID, atom.LastRunID)
	state.OpenQuestions = append(state.OpenQuestions, question)
	state.Status = FeatureStatePlanningQuestion
	return []Event{{
		Type:       "idiom.feature_planner.implementor_question_opened",
		SessionID:  state.SessionID,
		AtomID:     atom.ID,
		AtomFrom:   atomFrom,
		AtomTo:     AtomStateQuestionForPlanner,
		AtomDeltas: []AtomDelta{{Op: "update", AtomID: atom.ID, AtomFrom: atomFrom, AtomTo: AtomStateQuestionForPlanner}},
		ActiveRunDeltas: []ActiveRunDelta{{
			Op:            "fold_terminal",
			AtomID:        atom.ID,
			LaneSessionID: atom.LastRunID,
			BeforeState:   "running",
			AfterState:    AtomStateQuestionForPlanner,
		}},
		Message: question.QuestionSummary,
	}}
}

func completeAtomFromPlanExecute(state *FeatureState, atom *FeatureAtomState, summary string, laneFamily string, progressPath string) []Event {
	atomFrom := atom.State
	atom.State = AtomStateComplete
	atom.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
	foldActiveRun(state, atom.ID, atom.LastRunID)
	state.Continuation.CompletedAtoms = appendUnique(state.Continuation.CompletedAtoms, atom.ID)
	featureTo := state.Status
	if allAtomsTerminal(state.Atoms) && state.Status != FeatureStatePlanningQuestion {
		state.Status = FeatureStateComplete
		featureTo = FeatureStateComplete
	}
	fields := map[string]any{
		"lane_family": strings.TrimSpace(laneFamily),
	}
	if strings.TrimSpace(progressPath) != "" {
		fields["progress_path"] = strings.TrimSpace(progressPath)
	}
	if strings.TrimSpace(summary) != "" {
		fields["summary"] = strings.TrimSpace(summary)
	}
	if fields["lane_family"] == "" {
		fields["lane_family"] = "plan-execute"
	}
	return []Event{{
		Type:       "idiom.feature_planner.atom_completed",
		SessionID:  state.SessionID,
		FeatureTo:  featureTo,
		AtomID:     atom.ID,
		AtomFrom:   atomFrom,
		AtomTo:     AtomStateComplete,
		AtomDeltas: []AtomDelta{{Op: "update", AtomID: atom.ID, AtomFrom: atomFrom, AtomTo: AtomStateComplete}},
		ActiveRunDeltas: []ActiveRunDelta{{
			Op:            "fold_terminal",
			AtomID:        atom.ID,
			LaneSessionID: atom.LastRunID,
			BeforeState:   "running",
			AfterState:    AtomStateComplete,
			ResultSource:  "plan-execute",
			ProgressPath:  strings.TrimSpace(progressPath),
		}},
		Fields: fields,
	}}
}

func rejectUnverifiedCompletion(state *FeatureState, atom *FeatureAtomState, reason string) []Event {
	atomFrom := atom.State
	featureFrom := state.Status
	atom.State = AtomStateRecoveryOpen
	atom.Blockers = appendUnique(atom.Blockers, reason)
	state.Status = FeatureStateRecoveryOpen
	state.Blockers = appendUnique(state.Blockers, reason)
	state.Continuation.BlockedAtoms = appendUnique(state.Continuation.BlockedAtoms, atom.ID)
	state.Continuation.ResumeAtomID = atom.ID
	state.Continuation.ResumeAction = "repair_unverified_completion"
	state.Continuation.ResumeReason = reason
	foldActiveRun(state, atom.ID, atom.LastRunID)
	return []Event{{
		Type:        "idiom.feature_planner.unverified_completion_rejected",
		SessionID:   state.SessionID,
		FeatureFrom: featureFrom,
		FeatureTo:   FeatureStateRecoveryOpen,
		AtomID:      atom.ID,
		AtomFrom:    atomFrom,
		AtomTo:      AtomStateRecoveryOpen,
		AtomDeltas:  []AtomDelta{{Op: "update", AtomID: atom.ID, AtomFrom: atomFrom, AtomTo: AtomStateRecoveryOpen}},
		ActiveRunDeltas: []ActiveRunDelta{{
			Op:            "fold_terminal",
			AtomID:        atom.ID,
			LaneSessionID: atom.LastRunID,
			BeforeState:   "running",
			AfterState:    AtomStateRecoveryOpen,
		}},
		Message: reason,
	}}
}

func claimsUnverifiedEditOrTestEvidence(raw string) bool {
	lower := strings.ToLower(raw)
	for _, marker := range []string{
		"paths edited",
		"path edited",
		"files edited",
		"file edited",
		"changed paths",
		"modified files",
		"code artifacts",
		"tests authored",
		"test artifacts",
		"test-first execution",
		"verification",
		"passes locally",
		"passed locally",
		"go test ",
		"go test ./",
		"cargo test",
		"npm test",
		"pnpm test",
		"yarn test",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func MissingPreDispatchTDD(state FeatureState, atom FeatureAtomState) []string {
	missing := MissingStrictPreDispatchTDD(atom.FeatureAtomPlan)
	missing = append(missing, MismatchedStrictPreDispatchTDD(atom.FeatureAtomPlan)...)
	return append(missing, MissingPreDispatchEvidence(state, atom.FeatureAtomPlan)...)
}

func MissingPreDispatchEvidence(state FeatureState, atom FeatureAtomPlan) []string {
	wantEventID := strings.TrimSpace(atom.PlannedTDDSnapshot.CreatedBeforeDispatchEventID)
	if wantEventID == "" || strings.TrimSpace(atom.ID) == "" {
		return nil
	}
	var evidence *TDDPreExecutionEvidence
	for i := range state.TDDPreExecutionEvidence {
		candidate := &state.TDDPreExecutionEvidence[i]
		if strings.TrimSpace(candidate.AtomID) == strings.TrimSpace(atom.ID) && strings.TrimSpace(candidate.EventID) == wantEventID {
			evidence = candidate
			break
		}
	}
	if evidence == nil {
		return []string{"tdd_pre_execution_evidence." + wantEventID}
	}
	var missing []string
	if !sameStringSet(evidence.ObligationRefs, atom.TestFirst.ObligationRefs) {
		missing = append(missing, "tdd_pre_execution_evidence.obligation_refs")
	}
	if !sameStringSet(evidence.Commands, atom.TestFirst.Commands) {
		missing = append(missing, "tdd_pre_execution_evidence.commands")
	}
	if !sameStringSet(evidence.TestArtifactPaths, atom.TestFirst.TestArtifactPaths) {
		missing = append(missing, "tdd_pre_execution_evidence.test_artifact_paths")
	}
	if strings.TrimSpace(evidence.ExpectedPreExecutionResult) != strings.TrimSpace(atom.TestFirst.ExpectedPreExecutionResult) {
		missing = append(missing, "tdd_pre_execution_evidence.expected_pre_execution_result")
	}
	if strings.TrimSpace(evidence.CoverageGateEventID) != strings.TrimSpace(atom.TestFirst.CoverageGateEventID) {
		missing = append(missing, "tdd_pre_execution_evidence.coverage_gate_event_id")
	}
	if strings.TrimSpace(evidence.SnapshotID) != strings.TrimSpace(atom.PlannedTDDSnapshot.SnapshotID) {
		missing = append(missing, "tdd_pre_execution_evidence.snapshot_id")
	}
	if strings.TrimSpace(evidence.SnapshotDigest) != strings.TrimSpace(atom.PlannedTDDSnapshot.SnapshotDigest) {
		missing = append(missing, "tdd_pre_execution_evidence.snapshot_digest")
	}
	return missing
}

func WriteEvents(w io.Writer, events []Event) {
	enc := json.NewEncoder(w)
	for _, event := range events {
		_ = enc.Encode(event)
	}
}

func answerOpenQuestion(state *FeatureState, answer string) []Event {
	for i := range state.OpenQuestions {
		if state.OpenQuestions[i].Status == "open" {
			if err := applyQuestionAnswerAuthority(&state.OpenQuestions[i], answer); err != nil {
				state.Status = FeatureStatePlanningQuestion
				state.PlanningLoop.State = FeatureStatePlanningQuestion
				state.Blockers = append(state.Blockers, err.Error())
				return []Event{{Type: "idiom.feature_planner.planner_question_answer_rejected", SessionID: state.SessionID, Message: err.Error()}}
			}
			state.OpenQuestions[i].Status = "answered"
			state.Status = FeatureStatePlanning
			state.PlanningLoop.State = FeatureStatePlanning
			return []Event{{Type: "idiom.feature_planner.planner_question_answered", SessionID: state.SessionID, Message: state.OpenQuestions[i].QuestionSummary}}
		}
	}
	return nil
}

func applyQuestionAnswerAuthority(question *PlannerQuestion, rawAnswer string) error {
	if question == nil {
		return fmt.Errorf("planner question is nil")
	}
	answer := strings.TrimSpace(rawAnswer)
	if answer == "" {
		return fmt.Errorf("planner question answer is empty")
	}
	required := strings.TrimSpace(question.AuthorityRequired)
	if required == "" || required == "operator_approval" {
		question.Answer = answer
		if strings.TrimSpace(question.AnswerSourceKind) == "" {
			question.AnswerSourceKind = "operator_approval"
		}
		return nil
	}
	var payload struct {
		Answer           string   `json:"answer"`
		AnswerSourceKind string   `json:"answer_source_kind"`
		EvidenceRefs     []string `json:"evidence_refs"`
	}
	if err := json.Unmarshal([]byte(answer), &payload); err != nil {
		return fmt.Errorf("planner question %q requires typed %s authority answer", question.ID, required)
	}
	if strings.TrimSpace(payload.AnswerSourceKind) != required {
		return fmt.Errorf("planner question %q requires answer_source_kind %q", question.ID, required)
	}
	if strings.TrimSpace(payload.Answer) == "" {
		return fmt.Errorf("planner question %q typed answer missing answer", question.ID)
	}
	if len(payload.EvidenceRefs) == 0 {
		return fmt.Errorf("planner question %q typed answer missing evidence_refs", question.ID)
	}
	question.Answer = payload.Answer
	question.AnswerSourceKind = payload.AnswerSourceKind
	question.EvidenceRefs = append(question.EvidenceRefs, payload.EvidenceRefs...)
	return nil
}

func shouldResumeDispatch(state FeatureState) bool {
	switch state.Status {
	case FeatureStateDispatching, FeatureStateReady:
	default:
		return false
	}
	for _, atom := range state.Atoms {
		if atom.State == AtomStateQueued || atom.State == AtomStateDispatchable {
			return true
		}
	}
	return false
}

func foldActiveRun(state *FeatureState, atomID string, runID string) {
	if state == nil {
		return
	}
	for i := range state.ActiveRuns {
		run := state.ActiveRuns[i]
		if run.AtomID == atomID && (runID == "" || run.RunID == runID || run.LaneSessionID == runID) {
			state.ActiveRuns = append(state.ActiveRuns[:i], state.ActiveRuns[i+1:]...)
			return
		}
	}
}

func refreshContinuation(state *FeatureState) {
	state.Continuation.NextAtomID = ""
	var waitingOnDependencies string
	for _, atom := range state.Atoms {
		if atom.State == AtomStateQueued {
			if atomDependenciesSatisfied(state.Atoms, atom) {
				state.Continuation.NextAtomID = atom.ID
				state.Continuation.ResumeAction = "dispatch_next_atom"
				return
			}
			if waitingOnDependencies == "" {
				waitingOnDependencies = atom.ID
			}
		}
	}
	if waitingOnDependencies != "" {
		state.Continuation.NextAtomID = waitingOnDependencies
		state.Continuation.ResumeAction = "waiting_on_dependencies"
		return
	}
	if state.Status == FeatureStateDispatching && allAtomsTerminal(state.Atoms) {
		state.Status = FeatureStateComplete
		state.Continuation.ResumeAction = "close_feature"
	}
}

func atomDependenciesSatisfied(atoms []FeatureAtomState, atom FeatureAtomState) bool {
	for _, dep := range atom.Dependencies {
		dep = strings.TrimSpace(dep)
		if dep == "" {
			return false
		}
		foundTerminal := false
		for _, candidate := range atoms {
			if candidate.ID != dep {
				continue
			}
			if candidate.State == AtomStateComplete || candidate.State == AtomStateSourceSatisfied {
				foundTerminal = true
			}
			break
		}
		if !foundTerminal {
			return false
		}
	}
	return true
}

func allAtomsTerminal(atoms []FeatureAtomState) bool {
	if len(atoms) == 0 {
		return false
	}
	for _, atom := range atoms {
		switch atom.State {
		case AtomStateComplete, AtomStateSourceSatisfied:
		default:
			return false
		}
	}
	return true
}

func appendUnique(values []string, next string) []string {
	next = strings.TrimSpace(next)
	if next == "" {
		return values
	}
	for _, value := range values {
		if value == next {
			return values
		}
	}
	return append(values, next)
}

func firstPositive(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}
