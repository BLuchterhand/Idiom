package featureplanner

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"idiom/internal/taskfamily"
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
	ReconcileActiveRuns   func(state *FeatureState) []Event
	WorkspaceRoot         string
}

type RunResult struct {
	State  FeatureState
	Events []Event
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
	if opts.ReconcileActiveRuns != nil && len(state.ActiveRuns) > 0 {
		events = append(events, opts.ReconcileActiveRuns(&state)...)
		if state.Status != FeatureStateDispatching {
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
	answer, err := runner(plannerPrompt, string(taskfamily.FeaturePlannerPlanner))
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
		parsed.Plan = NormalizePlanProductWorkflowContext(parsed.Plan, *opts.ProductWorkflowBrief)
	}
	if parsed.Kind == ToolSubmitFeaturePlan && opts.ProductWorkflowBrief == nil {
		if err := ValidatePlanStandaloneWorkspaceContext(parsed.Plan, StandaloneWorkspaceContext{
			WorkspaceRoot: opts.WorkspaceRoot,
			Prompt:        opts.Prompt,
		}); err != nil {
			state.Status = FeatureStatePlanningRepair
			state.PlanningLoop.State = FeatureStatePlanningRepair
			state.Blockers = append(state.Blockers, err.Error())
			return RunResult{
				State:  state,
				Events: []Event{{Type: "idiom.feature_planner.standalone_context_blocked", SessionID: state.SessionID, Message: err.Error()}},
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
				fmt.Fprintf(&b, "  - %s: %s (command: %s, required_before: %s, status: %s)\n", obl.ID, obl.Behavior, obl.Command, obl.RequiredBefore, obl.Status)
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

		if brief.AllowsFeaturePlanDispatch() {
			b.WriteString("\nRequired submit_feature_plan shape:\n")
			b.WriteString(productSubmitFeaturePlanShape(brief.CoverageTier))
			b.WriteString("Your FeaturePlan SDD refs MUST reference only PRD requirement IDs and ADR IDs from the context above.\n")
			b.WriteString("Your TDD obligations MUST reference only the obligation IDs listed above.\n")
			if brief.coveragePendingImplementationVerification() {
				b.WriteString("The coverage gate is pending only because TDD pre-execution evidence is not recorded yet; treat the listed TDD obligations as test-first acceptance for this dispatch, and do not ask a planner question solely for that missing pre-execution evidence.\n")
				b.WriteString("Do not include a blocked coverage_gate_result in the plan; the pending evidence is carried by tdd_contract and planned_tdd_snapshot.\n")
			}
			b.WriteString("Set tdd_contract.obligations[].required_before to the exact scalar string \"implementation\". Do not use an array for required_before.\n")
			b.WriteString("For each atom, planned_tdd_snapshot.obligation_ids must exactly match test_first.obligation_refs, planned_tdd_snapshot.test_commands must exactly match test_first.commands, and planned_tdd_snapshot.test_artifact_paths must exactly match test_first.test_artifact_paths.\n")
			b.WriteString("Repo grounding is required: required_edit_paths and allowed_edit_paths must come from Files to modify, source/context evidence paths, sibling files inside those existing package roots, or authorized new package roots.\n")
			b.WriteString("Every path listed under Files to modify is required implementation scope: cover each one in atom.required_edit_paths across the submitted plan. For a one-atom dispatch, put every Files to modify path in that atom.required_edit_paths. Do not demote Files to modify into allowed_edit_paths.\n")
			b.WriteString("When test_first.baseline_required is true or a TDD obligation is required before implementation, include every grounded test_first.test_artifact_paths entry in required_edit_paths; these tests are required coverage, not optional allowed scope.\n")
			b.WriteString("Accepted ADR paths, PRD/TDD files, and product workflow planning/tracker files are context evidence only unless they are explicitly listed under Files to modify; do not put context-only docs in required_edit_paths or allowed_edit_paths.\n")
			b.WriteString("allowed_edit_paths is still edit scope, not an evidence/reference list. Put design references in sdd_refs, tdd_obligations, acceptance, decisions, or prompt text instead.\n")
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
		b.WriteString(standaloneSubmitFeaturePlanShape())
		b.WriteString("Set tdd_contract.obligations[].required_before to the scalar string \"implementation\". Do not use an array for required_before.\n")
		b.WriteString("For each atom, planned_tdd_snapshot.obligation_ids must exactly match test_first.obligation_refs, planned_tdd_snapshot.test_commands must exactly match test_first.commands, and planned_tdd_snapshot.test_artifact_paths must exactly match test_first.test_artifact_paths.\n")
		b.WriteString("No upstream design docs are available. Use acceptance criteria strings in sdd_contract.acceptance instead of file paths.\n")
		b.WriteString("Do NOT invent file paths like docs/path.md — acceptance criteria strings are valid and preferred when no docs exist.\n")
		b.WriteString("Standalone workspace grounding is enforced: required_edit_paths, allowed_edit_paths, and test artifact paths must be existing repo-relative paths unless the user explicitly authorizes new-file/full-body/create work. Generic add language only authorizes missing test-like paths when the user asks to add tests. If you cannot localize existing paths, use ask_planner_question instead of inventing paths.\n")
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
		answer, err := runner(BuildImplementorPrompt(*atom, state.Plan), string(taskfamily.FeaturePlannerImplementor))
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
	b.WriteString("This atom brief is consumed by plan-execute. Plan-execute owns the terminal response protocol; do not emit feature-planner completion JSON from inside the atom brief.\n")
	b.WriteString("Do not claim files were edited or tests were run unless those effects were produced by tool-backed execution visible to the harness; unsupported edit/test claims are rejected as recovery-open.\n")
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
	if scope := strings.TrimSpace(buildFeaturePlannerAtomScopeSection(atom.RequiredEditPaths, atom.AllowedEditPaths)); scope != "" {
		b.WriteString("\nCanonical plan-execute edit scope:\n")
		b.WriteString(scope)
		b.WriteString("\n")
	}
	b.WriteString("\nAtom contract:\n")
	b.Write(body)
	b.WriteString("\n")
	return b.String()
}

func buildFeaturePlannerAtomScopeSection(requiredEditPaths []string, allowedEditPaths []string) string {
	required := normalizeEditScopeList(requiredEditPaths)
	allowed := featurePlannerAllowedScopeExcludingRequired(normalizeEditScopeList(allowedEditPaths), required)
	if len(required) == 0 && len(allowed) == 0 {
		return ""
	}
	var b strings.Builder
	if len(required) > 0 {
		b.WriteString("## Files to Modify\n\n")
		b.WriteString("required_edit_paths:\n")
		for _, path := range required {
			b.WriteString("- ")
			b.WriteString(path)
			b.WriteByte('\n')
		}
	}
	if len(allowed) > 0 {
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString("## Candidate Edit Scope From Target References\n\n")
		b.WriteString("allowed_edit_paths:\n")
		for _, path := range allowed {
			b.WriteString("- ")
			b.WriteString(path)
			b.WriteByte('\n')
		}
	}
	return strings.TrimSpace(b.String())
}

func featurePlannerAllowedScopeExcludingRequired(paths []string, required []string) []string {
	requiredSet := normalizedStringSet(required)
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		if requiredSet[path] {
			continue
		}
		out = append(out, path)
	}
	return out
}

func ApplyImplementorOutput(state *FeatureState, atom *FeatureAtomState, raw string) []Event {
	if atom == nil || state == nil {
		return nil
	}
	trimmed := strings.TrimSpace(raw)
	if strings.HasPrefix(trimmed, "{") {
		var envelope implementorOutputEnvelope
		if err := json.Unmarshal([]byte(trimmed), &envelope); err != nil {
			return rejectUnverifiedCompletion(state, atom, "implementor returned malformed JSON completion envelope: "+err.Error())
		}
		tool := strings.TrimSpace(envelope.Tool)
		if tool == ToolPlanExecuteComplete {
			completion := planExecuteCompletionFromEnvelope(envelope)
			if ok, reason := planExecuteCompletionIsSuccess(completion); !ok {
				return rejectPlanExecuteCompletion(state, atom, completion, reason)
			}
			return completeAtomFromPlanExecute(state, atom, completion)
		}
		if tool != "" && tool != ToolQuestionForPlanner {
			return rejectUnverifiedCompletion(state, atom, fmt.Sprintf("implementor returned unsupported completion tool %q; expected %q with typed status or %q for blockers", tool, ToolPlanExecuteComplete, ToolQuestionForPlanner))
		}
		if tool == "" {
			return rejectUnverifiedCompletion(state, atom, fmt.Sprintf("implementor returned JSON without a supported tool; expected %q with typed status or %q for blockers", ToolPlanExecuteComplete, ToolQuestionForPlanner))
		}
		var question PlannerQuestion
		if err := json.Unmarshal([]byte(trimmed), &question); err != nil {
			atom.Blockers = append(atom.Blockers, err.Error())
			state.Status = FeatureStatePlanningRepair
			return []Event{{Type: "idiom.feature_planner.planner_question_rejected", SessionID: state.SessionID, AtomID: atom.ID, Message: err.Error()}}
		}
		return openImplementorPlannerQuestion(state, atom, question)
	}
	if strings.HasPrefix(trimmed, "<<QUESTION>>:") {
		return openImplementorPlannerQuestion(state, atom, PlannerQuestion{
			QuestionSummary: strings.TrimSpace(strings.TrimPrefix(trimmed, "<<QUESTION>>:")),
			BlockingClaim:   "implementor could not complete the atom contract",
		})
	}
	if strings.Contains(strings.ToLower(trimmed), "decision: source-satisfied") {
		return rejectUnverifiedCompletion(state, atom, fmt.Sprintf("implementor reported source-satisfied without typed %q plan-execute completion evidence", ToolPlanExecuteComplete))
	}
	if claimsUnverifiedEditOrTestEvidence(trimmed) {
		return rejectUnverifiedCompletion(state, atom, "implementor claimed edit/test evidence that was not observed by the feature-planner harness")
	}
	return rejectUnverifiedCompletion(state, atom, fmt.Sprintf("implementor returned raw text without typed %q plan-execute completion evidence", ToolPlanExecuteComplete))
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

type implementorOutputEnvelope struct {
	Tool                string                       `json:"tool"`
	LaneFamily          string                       `json:"lane_family"`
	Summary             string                       `json:"summary"`
	ProgressPath        string                       `json:"progress_path"`
	Status              string                       `json:"status"`
	Phase               string                       `json:"phase"`
	ReviewStatus        string                       `json:"review_status"`
	FailureKind         string                       `json:"failure_kind"`
	FailureReason       string                       `json:"failure_reason"`
	FailureID           string                       `json:"failure_id"`
	RecordPath          string                       `json:"record_path"`
	PacketPath          string                       `json:"packet_path"`
	NextAction          string                       `json:"next_valid_action"`
	CompletionSource    string                       `json:"completion_source"`
	ReviewRepairSkipped bool                         `json:"review_repair_skipped"`
	PlanExecute         *nestedPlanExecuteCompletion `json:"plan_execute,omitempty"`
	Nested              *nestedPlanExecuteCompletion `json:"nested,omitempty"`
	QuestionSummary     string                       `json:"question_summary"`
	BlockingClaim       string                       `json:"blocking_claim"`
}

type nestedPlanExecuteCompletion struct {
	LaneFamily          string `json:"lane_family"`
	Summary             string `json:"summary"`
	ProgressPath        string `json:"progress_path"`
	Status              string `json:"status"`
	Phase               string `json:"phase"`
	ReviewStatus        string `json:"review_status"`
	FailureKind         string `json:"failure_kind"`
	FailureReason       string `json:"failure_reason"`
	FailureID           string `json:"failure_id"`
	RecordPath          string `json:"record_path"`
	PacketPath          string `json:"packet_path"`
	NextAction          string `json:"next_valid_action"`
	CompletionSource    string `json:"completion_source"`
	ReviewRepairSkipped bool   `json:"review_repair_skipped"`
}

type planExecuteCompletion struct {
	LaneFamily          string
	Summary             string
	ProgressPath        string
	Status              string
	Phase               string
	ReviewStatus        string
	FailureKind         string
	FailureReason       string
	FailureID           string
	RecordPath          string
	PacketPath          string
	NextAction          string
	CompletionSource    string
	ReviewRepairSkipped bool
}

func planExecuteCompletionFromEnvelope(envelope implementorOutputEnvelope) planExecuteCompletion {
	completion := planExecuteCompletionFromSummary(planExecuteCompletionSummaryFromEnvelope(envelope))
	if envelope.PlanExecute != nil {
		completion.overlay(*envelope.PlanExecute)
	}
	if envelope.Nested != nil {
		completion.overlay(*envelope.Nested)
	}
	return completion.normalized()
}

func (completion *planExecuteCompletion) overlay(nested nestedPlanExecuteCompletion) {
	merged := MergePlanExecuteCompletionSummaries(
		completion.summary(),
		PlanExecuteCompletionSummary{
			LaneFamily:          nested.LaneFamily,
			Summary:             nested.Summary,
			ProgressPath:        nested.ProgressPath,
			Status:              nested.Status,
			Phase:               nested.Phase,
			ReviewStatus:        nested.ReviewStatus,
			FailureKind:         nested.FailureKind,
			FailureReason:       nested.FailureReason,
			FailureID:           nested.FailureID,
			RecordPath:          nested.RecordPath,
			PacketPath:          nested.PacketPath,
			NextAction:          nested.NextAction,
			CompletionSource:    nested.CompletionSource,
			ReviewRepairSkipped: nested.ReviewRepairSkipped,
		},
	)
	*completion = planExecuteCompletionFromSummary(merged)
}

func (completion planExecuteCompletion) normalized() planExecuteCompletion {
	completion.LaneFamily = strings.TrimSpace(completion.LaneFamily)
	if completion.LaneFamily == "" {
		completion.LaneFamily = "plan-execute"
	}
	completion.Summary = strings.TrimSpace(completion.Summary)
	completion.ProgressPath = strings.TrimSpace(completion.ProgressPath)
	completion.Status = strings.TrimSpace(completion.Status)
	completion.Phase = strings.TrimSpace(completion.Phase)
	completion.ReviewStatus = strings.TrimSpace(completion.ReviewStatus)
	completion.FailureKind = strings.TrimSpace(completion.FailureKind)
	completion.FailureReason = strings.TrimSpace(completion.FailureReason)
	completion.FailureID = strings.TrimSpace(completion.FailureID)
	completion.RecordPath = strings.TrimSpace(completion.RecordPath)
	completion.PacketPath = strings.TrimSpace(completion.PacketPath)
	completion.NextAction = strings.TrimSpace(completion.NextAction)
	completion.CompletionSource = strings.TrimSpace(completion.CompletionSource)
	return completion
}

func planExecuteCompletionSummaryFromEnvelope(envelope implementorOutputEnvelope) PlanExecuteCompletionSummary {
	return PlanExecuteCompletionSummary{
		LaneFamily:          envelope.LaneFamily,
		Summary:             envelope.Summary,
		ProgressPath:        envelope.ProgressPath,
		Status:              envelope.Status,
		Phase:               envelope.Phase,
		ReviewStatus:        envelope.ReviewStatus,
		FailureKind:         envelope.FailureKind,
		FailureReason:       envelope.FailureReason,
		FailureID:           envelope.FailureID,
		RecordPath:          envelope.RecordPath,
		PacketPath:          envelope.PacketPath,
		NextAction:          envelope.NextAction,
		CompletionSource:    envelope.CompletionSource,
		ReviewRepairSkipped: envelope.ReviewRepairSkipped,
	}
}

func (completion planExecuteCompletion) summary() PlanExecuteCompletionSummary {
	return PlanExecuteCompletionSummary{
		LaneFamily:          completion.LaneFamily,
		Summary:             completion.Summary,
		ProgressPath:        completion.ProgressPath,
		Status:              completion.Status,
		Phase:               completion.Phase,
		ReviewStatus:        completion.ReviewStatus,
		FailureKind:         completion.FailureKind,
		FailureReason:       completion.FailureReason,
		FailureID:           completion.FailureID,
		RecordPath:          completion.RecordPath,
		PacketPath:          completion.PacketPath,
		NextAction:          completion.NextAction,
		CompletionSource:    completion.CompletionSource,
		ReviewRepairSkipped: completion.ReviewRepairSkipped,
	}
}

func planExecuteCompletionFromSummary(summary PlanExecuteCompletionSummary) planExecuteCompletion {
	return planExecuteCompletion{
		LaneFamily:          summary.LaneFamily,
		Summary:             summary.Summary,
		ProgressPath:        summary.ProgressPath,
		Status:              summary.Status,
		Phase:               summary.Phase,
		ReviewStatus:        summary.ReviewStatus,
		FailureKind:         summary.FailureKind,
		FailureReason:       summary.FailureReason,
		FailureID:           summary.FailureID,
		RecordPath:          summary.RecordPath,
		PacketPath:          summary.PacketPath,
		NextAction:          summary.NextAction,
		CompletionSource:    summary.CompletionSource,
		ReviewRepairSkipped: summary.ReviewRepairSkipped,
	}.normalized()
}

func planExecuteCompletionIsSuccess(completion planExecuteCompletion) (bool, string) {
	decision := PlanExecuteCompletionTerminalDecision(map[string]any{
		"status":                completion.Status,
		"phase":                 completion.Phase,
		"review_status":         completion.ReviewStatus,
		"failure_kind":          completion.FailureKind,
		"failure_reason":        completion.FailureReason,
		"review_repair_skipped": completion.ReviewRepairSkipped,
	})
	if decision.Class == PlanExecuteTerminalCompleted {
		return true, ""
	}
	return false, decision.Reason
}

func completeAtomFromPlanExecute(state *FeatureState, atom *FeatureAtomState, completion planExecuteCompletion) []Event {
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
		"lane_family":   completion.LaneFamily,
		"nested_status": completion.Status,
	}
	if completion.Phase != "" {
		fields["nested_phase"] = completion.Phase
	}
	if completion.ReviewStatus != "" {
		fields["nested_review_status"] = completion.ReviewStatus
	}
	if completion.ProgressPath != "" {
		fields["progress_path"] = completion.ProgressPath
	}
	if completion.Summary != "" {
		fields["summary"] = completion.Summary
	}
	if completion.CompletionSource != "" {
		fields["completion_source"] = completion.CompletionSource
	}
	if completion.ReviewRepairSkipped {
		fields["review_repair_skipped"] = true
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
			ProgressPath:  completion.ProgressPath,
		}},
		Fields: fields,
	}}
}

func rejectPlanExecuteCompletion(state *FeatureState, atom *FeatureAtomState, completion planExecuteCompletion, reason string) []Event {
	fields := planExecuteCompletionFields(completion)
	if detail := planExecuteCompletionFailureDetail(completion); detail != "" {
		reason = strings.TrimSpace(reason + ": " + detail)
	}
	return recoverAtom(state, atom, reason, "repair_nested_plan_execute_completion", "idiom.feature_planner.plan_execute_completion_rejected", fields)
}

func rejectUnverifiedCompletion(state *FeatureState, atom *FeatureAtomState, reason string) []Event {
	return recoverAtom(state, atom, reason, "repair_unverified_completion", "idiom.feature_planner.unverified_completion_rejected", nil)
}

func recoverAtom(state *FeatureState, atom *FeatureAtomState, reason string, resumeAction string, eventType string, fields map[string]any) []Event {
	atomFrom := atom.State
	featureFrom := state.Status
	atom.State = AtomStateRecoveryOpen
	atom.Blockers = appendUnique(atom.Blockers, reason)
	state.Status = FeatureStateRecoveryOpen
	state.Blockers = appendUnique(state.Blockers, reason)
	state.Continuation.BlockedAtoms = appendUnique(state.Continuation.BlockedAtoms, atom.ID)
	state.Continuation.ResumeAtomID = atom.ID
	state.Continuation.ResumeAction = resumeAction
	state.Continuation.ResumeReason = reason
	foldActiveRun(state, atom.ID, atom.LastRunID)
	if fields == nil {
		fields = map[string]any{}
	}
	if packetPath := CompletionFieldString(fields, "packet_path"); packetPath != "" {
		state.OpenRecoveryPackets = appendUnique(state.OpenRecoveryPackets, packetPath)
	}
	return []Event{{
		Type:        eventType,
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
		Fields:  fields,
	}}
}

func planExecuteCompletionFields(completion planExecuteCompletion) map[string]any {
	fields := map[string]any{
		"lane_family":   completion.LaneFamily,
		"nested_status": completion.Status,
	}
	if completion.Phase != "" {
		fields["nested_phase"] = completion.Phase
	}
	if completion.ReviewStatus != "" {
		fields["nested_review_status"] = completion.ReviewStatus
	}
	if completion.FailureKind != "" {
		fields["nested_failure_kind"] = completion.FailureKind
	}
	if completion.FailureReason != "" {
		fields["nested_failure_reason"] = completion.FailureReason
	}
	if completion.FailureID != "" {
		fields["failure_id"] = completion.FailureID
	}
	if completion.RecordPath != "" {
		fields["record_path"] = completion.RecordPath
	}
	if completion.PacketPath != "" {
		fields["packet_path"] = completion.PacketPath
	}
	if completion.NextAction != "" {
		fields["next_valid_action"] = completion.NextAction
	}
	if completion.ProgressPath != "" {
		fields["progress_path"] = completion.ProgressPath
	}
	if completion.Summary != "" {
		fields["summary"] = completion.Summary
	}
	if completion.CompletionSource != "" {
		fields["completion_source"] = completion.CompletionSource
	}
	if completion.ReviewRepairSkipped {
		fields["review_repair_skipped"] = true
	}
	return fields
}

func planExecuteCompletionFailureDetail(completion planExecuteCompletion) string {
	kind := strings.TrimSpace(completion.FailureKind)
	reason := strings.TrimSpace(completion.FailureReason)
	switch {
	case kind != "" && reason != "":
		return kind + " - " + reason
	case kind != "":
		return kind
	case reason != "":
		return reason
	default:
		return ""
	}
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
