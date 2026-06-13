package featureplanner

import (
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"strings"
	"time"

	"idiom/internal/contracts/autonomy"
	"idiom/internal/contracts/pathparse"
	plancontract "idiom/internal/contracts/plan"
	"idiom/internal/platform/util"
	coreplanexecute "idiom/internal/workflows/planexecute"
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

	if event, ok := resumePlanExecuteRetryPending(&state); ok {
		events = append(events, event)
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

	answer, err := runner(plannerPrompt, string(autonomy.TaskFamilyFeaturePlannerPlanner))
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
		PreserveSourceSatisfiedGate(&parsed.Plan, opts.Prompt)

		if err := ValidatePlanProductWorkflowContext(parsed.Plan, *opts.ProductWorkflowBrief, opts.WorkspaceRoot); err != nil {
			state.Status = FeatureStatePlanningRepair
			state.PlanningLoop.State = FeatureStatePlanningRepair
			state.Blockers = append(state.Blockers, err.Error())
			candidate := recordRejectedPlanCandidate(&state, parsed.Plan, "idiom.feature_planner.product_context_blocked", err.Error(), opts.Now)

			return RunResult{
				State:  state,
				Events: []Event{rejectedPlanEvent(state.SessionID, "idiom.feature_planner.product_context_blocked", err.Error(), candidate)},
			}, nil
		}

		parsed.Plan = NormalizePlanProductWorkflowContext(parsed.Plan, *opts.ProductWorkflowBrief, opts.WorkspaceRoot)
		if err := ValidateSubmittedPlanAgainstLockedContract(state.Plan, parsed.Plan, state.AuthorityDeltas); err != nil {
			state.Status = FeatureStatePlanningRepair
			state.PlanningLoop.State = FeatureStatePlanningRepair
			state.Blockers = append(state.Blockers, err.Error())
			candidate := recordRejectedPlanCandidate(&state, parsed.Plan, "idiom.feature_planner.plan_contract_blocked", err.Error(), opts.Now)

			return RunResult{
				State:  state,
				Events: []Event{rejectedPlanEvent(state.SessionID, "idiom.feature_planner.plan_contract_blocked", err.Error(), candidate)},
			}, nil
		}

		StampPlanAuthorityContract(&parsed.Plan, state.AuthorityDeltas)
	}

	if parsed.Kind == ToolSubmitFeaturePlan && opts.ProductWorkflowBrief == nil {
		PreserveSourceSatisfiedGate(&parsed.Plan, opts.Prompt)

		if err := ValidateSubmittedPlanAgainstLockedContract(state.Plan, parsed.Plan, state.AuthorityDeltas); err != nil {
			state.Status = FeatureStatePlanningRepair
			state.PlanningLoop.State = FeatureStatePlanningRepair
			state.Blockers = append(state.Blockers, err.Error())
			candidate := recordRejectedPlanCandidate(&state, parsed.Plan, "idiom.feature_planner.plan_contract_blocked", err.Error(), opts.Now)

			return RunResult{
				State:  state,
				Events: []Event{rejectedPlanEvent(state.SessionID, "idiom.feature_planner.plan_contract_blocked", err.Error(), candidate)},
			}, nil
		}

		contract := StampPlanAuthorityContract(&parsed.Plan, state.AuthorityDeltas)
		if err := ValidatePlanStandaloneWorkspaceContext(parsed.Plan, StandaloneWorkspaceContext{
			WorkspaceRoot: opts.WorkspaceRoot,
			Contract:      contract,
		}); err != nil {
			state.Status = FeatureStatePlanningRepair
			state.PlanningLoop.State = FeatureStatePlanningRepair
			state.Blockers = append(state.Blockers, err.Error())
			candidate := recordRejectedPlanCandidate(&state, parsed.Plan, "idiom.feature_planner.standalone_context_blocked", err.Error(), opts.Now)
			events := []Event{rejectedPlanEvent(state.SessionID, "idiom.feature_planner.standalone_context_blocked", err.Error(), candidate)}
			if question, ok := missingNewFileAuthorizationQuestion(err, opts.Prompt); ok {
				events = append(events, ApplyPlannerOutput(&state, ParsedPlannerOutput{
					Kind:     ToolAskPlannerQuestion,
					Question: question,
				}, opts.Now)...)
			}

			return RunResult{
				State:  state,
				Events: events,
			}, nil
		}
	}

	if parsed.Kind == ToolSourceSatisfied {
		if err := ValidateSourceSatisfiedWorkspaceContext(parsed.SourceSatisfied, opts.WorkspaceRoot); err != nil {
			state.Status = FeatureStatePlanningRepair
			state.PlanningLoop.State = FeatureStatePlanningRepair
			state.Blockers = append(state.Blockers, err.Error())

			return RunResult{
				State:  state,
				Events: []Event{{Type: "idiom.feature_planner.source_satisfied_rejected", SessionID: state.SessionID, Message: err.Error()}},
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
	b.WriteString("Valid tools: submit_feature_plan, source_satisfied, or ask_planner_question.\n")
	b.WriteString("For submit_feature_plan, return the FeaturePlan object itself with tool=\"submit_feature_plan\" at the top level. Do not nest the plan under a plan, payload, arguments, or input field.\n")
	b.WriteString("For source_satisfied, return {\"tool\":\"source_satisfied\",\"summary\":\"requested final state is already present\",\"reason\":\"short evidence-based reason\",\"evidence_paths\":[\"repo/path\"],\"test_paths\":[\"repo/test_path\"],\"anchors\":[{\"path\":\"repo/path\",\"text\":\"exact current source/test substring\"}]}. Use it only when exact current source/test anchor text is already present in the prompt context; path names alone are not proof.\n")
	b.WriteString("For ask_planner_question, return {\"tool\":\"ask_planner_question\",\"question_summary\":\"...\",\"blocking_claim\":\"what blocks a safe plan\",\"requested_decision\":\"exact operator/source decision needed\",\"question_kind\":\"external_answer_required\",\"authority_required\":\"operator_approval\",\"required_before\":\"plan_approval\",\"affected_paths\":[\"repo/path\"],\"affected_fields\":[\"new_file_authorization\"]}. Do not use a bare question field. If the answer would authorize missing/new files or path-level edit authority, affected_paths must contain the exact repo-relative paths and affected_fields must name new_file_authorization or the exact authorized field; paths inside requested_decision are explanatory text only and do not grant authority.\n")
	b.WriteString("Every atom must name at least one concrete required_edit_paths or allowed_edit_paths entry. A no-mutation planning test still names the intended source or test paths as allowed scope; do not return both arrays empty.\n")
	b.WriteString("If the operator explicitly asks to check whether the work is already satisfied before editing, preserve that gate. Either return source_satisfied with concrete current-state evidence paths, or create an atom whose prompt starts with the same source-satisfied check and no-edit-if-satisfied instruction. Do not rewrite that request into unconditional implementation work.\n")

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
			b.WriteString("planned_tdd_snapshot.snapshot_id and snapshot_digest are harness-stamped; omit these fields from planned_tdd_snapshot.\n")
			b.WriteString("Repo grounding is required: required_edit_paths and allowed_edit_paths must come from Files to modify, sibling files inside those existing package roots, or authorized new package roots.\n")
			b.WriteString("Every path listed under Files to modify is required implementation scope: cover each one in atom.required_edit_paths across the submitted plan. For a one-atom dispatch, put every Files to modify path in that atom.required_edit_paths. Do not demote Files to modify into allowed_edit_paths.\n")
			b.WriteString("When test_first.baseline_required is true or a TDD obligation is required before implementation, include every grounded test_first.test_artifact_paths entry in required_edit_paths; these tests are required coverage, not optional allowed scope.\n")
			b.WriteString("Accepted ADR paths, PRD/TDD files, and product workflow planning/tracker files are context evidence only unless they are explicitly listed under Files to modify; do not put context-only docs in required_edit_paths or allowed_edit_paths.\n")
			b.WriteString("allowed_edit_paths is still edit scope, not an evidence/reference list. Put design references in sdd_refs, tdd_obligations, acceptance, decisions, or prompt text instead.\n")
			b.WriteString("The schema example path placeholders are not authority. Do not copy or invent paths from examples.\n")

			if len(brief.FilesToModify) == 0 && len(brief.AuthorizedNewPackageRoots) == 0 {
				b.WriteString("No Files to modify or authorized new package roots are present. Use ask_planner_question for localization repair instead of concrete required_edit_paths or allowed_edit_paths; source/context evidence paths are read-only evidence.\n")
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
		b.WriteString("planned_tdd_snapshot.snapshot_id and snapshot_digest are harness-stamped; omit these fields from planned_tdd_snapshot.\n")
		b.WriteString("When test_first.baseline_required is true or a TDD obligation is required before implementation, include every test_first.test_artifact_paths entry in required_edit_paths; these tests are required coverage, not optional allowed scope.\n")
		b.WriteString("No upstream design docs are available. Define stable standalone acceptance IDs in sdd_contract.acceptance, such as \"SDD-ACCEPTANCE-1: ...\", and use those IDs in atom.sdd_refs. Do not invent product PRD/ADR refs.\n")
		b.WriteString("Do NOT invent file paths like docs/path.md — acceptance criteria strings are valid and preferred when no docs exist.\n")
		b.WriteString("Standalone workspace grounding is enforced: required_edit_paths, allowed_edit_paths, and test artifact paths must be existing repo-relative paths unless the user explicitly authorizes new-file/full-body/create work. Generic add language only authorizes missing test-like paths when the user asks to add tests. If you cannot localize existing paths, use ask_planner_question instead of inventing paths.\n")
	}

	b.WriteString("Before any atom can be dispatched, every atom must carry SDD refs and test_first commands tied to TDD obligations required_before implementation.\n")
	b.WriteString("Top-level scope is the smallest releasable feature objective; atoms are independently dispatchable implementation units.\n\n")

	if len(state.Blockers) > 0 {
		b.WriteString("Previous planner blockers that must be repaired before resubmission:\n")
		for _, blocker := range state.Blockers {
			fmt.Fprintf(&b, "- %s\n", blocker)
		}
		b.WriteString("\n")
	}

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

		runID := planExecuteRunIDForAtom(atom.ID, atom.Attempt)
		startedAt := time.Now().UTC().Format(time.RFC3339Nano)

		atom.LastRunID = runID

		contract := CompilePlanAuthorityContract(state.Plan, state.AuthorityDeltas)
		atomContract, _ := planAuthorityContractAtom(contract, atom.ID)

		state.ActiveRuns = append(state.ActiveRuns, ActiveRun{
			RunID:                runID,
			AtomID:               atom.ID,
			Actor:                "implementor",
			LaneSessionID:        runID,
			LaneFamily:           "plan-execute",
			InvocationContractID: atomContract.Scope.ContractID,
			RuntimeEnvelopeID:    contract.ContractHash,
			State:                "running",
			StartedAt:            startedAt,
			UpdatedAt:            startedAt,
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
				Op:                   "insert",
				AtomID:               atom.ID,
				LaneSessionID:        runID,
				LaneFamily:           "plan-execute",
				InvocationContractID: atomContract.Scope.ContractID,
				RuntimeEnvelopeID:    contract.ContractHash,
				AfterState:           "running",
				StartedAt:            startedAt,
			}},
			Fields: map[string]any{"run_id": runID},
		})

		answer, err := runner(BuildImplementorPromptFromContract(*atom, state.Plan, contract), string(autonomy.TaskFamilyFeaturePlannerImplementor))
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
	return BuildImplementorPromptFromContract(atom, plan, CompilePlanAuthorityContract(plan, nil))
}

func BuildImplementorPromptFromContract(atom FeatureAtomState, plan FeaturePlan, contract plancontract.PlanExecutionContract) string {
	body, _ := json.MarshalIndent(atom.FeatureAtomPlan, "", "  ")
	atomContract, _ := planAuthorityContractAtom(contract, atom.ID)
	contractBody, _ := json.MarshalIndent(atomContract, "", "  ")
	planContractBody, _ := json.MarshalIndent(contract, "", "  ")

	var b strings.Builder

	fmt.Fprintf(&b, "Implement one feature atom from feature plan %q.\n", plan.Summary)

	if runID := strings.TrimSpace(atom.LastRunID); runID != "" {
		fmt.Fprintf(&b, "Plan-execute session id: %s\n", runID)
	}

	if atomContract.Scope.ContractID != "" {
		fmt.Fprintf(&b, "Active plan contract id: %s\n", atomContract.Scope.ContractID)
		fmt.Fprintf(&b, "Active plan contract snapshot hash: %s\n", contract.ContractHash)
	}

	if contract.ContractID != "" {
		fmt.Fprintf(&b, "Active plan execution contract id: %s\n", contract.ContractID)
	}

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

	if scope := strings.TrimSpace(buildFeaturePlannerAtomScopeSection(atomContract.Scope.RequiredEditPaths(), atomContract.Scope.AllowedEditPaths())); scope != "" {
		b.WriteString("\nCanonical plan-execute edit scope:\n")
		b.WriteString(scope)
		b.WriteString("\n")
	}

	if contract.ContractID != "" {
		b.WriteString("\nTyped plan execution contract:\n")
		b.Write(planContractBody)
		b.WriteString("\n")
	}

	if atomContract.AtomID != "" {
		b.WriteString("\nTyped plan authority projection:\n")
		b.Write(contractBody)
		b.WriteString("\n")
	}

	b.WriteString("\nAtom contract:\n")
	b.Write(body)
	b.WriteString("\n")

	return b.String()
}

func PlanExecuteAuthorizedNewFilesFromImplementorPrompt(prompt string) []string {
	contractJSON := promptSectionAfterMarker(prompt, "Typed plan execution contract:")
	if strings.TrimSpace(contractJSON) == "" {
		return nil
	}

	var contract plancontract.PlanExecutionContract

	if err := json.Unmarshal([]byte(contractJSON), &contract); err != nil {
		return nil
	}

	var paths []string

	for _, file := range contract.Normalized().AuthorizedNewFiles {
		if path := plancontract.NormalizeScopePath(file.Path); path != "" {
			paths = append(paths, path)
		}
	}

	return normalizedStringList(paths)
}

func promptSectionAfterMarker(prompt string, marker string) string {
	idx := strings.Index(prompt, marker)
	if idx < 0 {
		return ""
	}

	section := strings.TrimSpace(prompt[idx+len(marker):])
	for _, next := range []string{
		"\nTyped plan authority projection:",
		"\nAtom contract:",
	} {
		if nextIdx := strings.Index(section, next); nextIdx >= 0 {
			section = section[:nextIdx]
		}
	}

	return strings.TrimSpace(section)
}

func planExecuteRunIDForAtom(atomID string, attempt int) string {
	stem := strings.Trim(strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= 'A' && r <= 'Z':
			return r
		case r >= '0' && r <= '9':
			return r
		case r == '-' || r == '_':
			return r
		default:
			return '-'
		}
	}, strings.TrimSpace(atomID)), "-_")
	if stem == "" {
		stem = "atom"
	}

	if stem != strings.TrimSpace(atomID) {
		h := fnv.New32a()

		_, _ = h.Write([]byte(strings.TrimSpace(atomID)))
		stem = fmt.Sprintf("%s-%08x", stem, h.Sum32())
	}

	runID := fmt.Sprintf("%s-run-%d", stem, attempt)
	if len(runID) <= 128 {
		return runID
	}

	h := fnv.New32a()

	_, _ = h.Write([]byte(runID))

	maxStem := 128 - len("-run-") - len(fmt.Sprint(attempt)) - 9

	if maxStem < 4 {
		maxStem = 4
	}

	if len(stem) > maxStem {
		stem = stem[:maxStem]
	}

	return fmt.Sprintf("%s-%08x-run-%d", strings.Trim(stem, "-_"), h.Sum32(), attempt)
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

		var envelopeFields map[string]any

		_ = json.Unmarshal([]byte(trimmed), &envelopeFields)
		applyRegisteredAcceptanceEvidenceTrust(&envelope, envelopeFields)

		tool := strings.TrimSpace(envelope.Tool)
		if tool == ToolPlanExecuteComplete {
			completion := planExecuteCompletionFromEnvelope(envelope)

			completion = bindPlanExecuteCompletionToActiveAuthority(completion, *state, *atom)

			if ok, reason := planExecuteCompletionIsSuccess(completion, *state, *atom); !ok {
				if planExecuteCompletionRetryable(completion) {
					return markPlanExecuteRetryPending(state, atom, completion, reason)
				}

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

func applyRegisteredAcceptanceEvidenceTrust(envelope *implementorOutputEnvelope, fields map[string]any) {
	if envelope == nil {
		return
	}

	envelope.AcceptanceEvidenceTrusted = planExecuteAcceptanceEvidenceTrustRegistered(fields)
	if nestedFields, ok := fields["plan_execute"].(map[string]any); ok && envelope.PlanExecute != nil {
		envelope.PlanExecute.AcceptanceEvidenceTrusted = planExecuteAcceptanceEvidenceTrustRegistered(nestedFields)
	}

	if nestedFields, ok := fields["nested"].(map[string]any); ok && envelope.Nested != nil {
		envelope.Nested.AcceptanceEvidenceTrusted = planExecuteAcceptanceEvidenceTrustRegistered(nestedFields)
	}
}

type implementorOutputEnvelope struct {
	Tool                         string                                  `json:"tool"`
	LaneFamily                   string                                  `json:"lane_family"`
	Summary                      string                                  `json:"summary"`
	ProgressPath                 string                                  `json:"progress_path"`
	Status                       string                                  `json:"status"`
	Phase                        string                                  `json:"phase"`
	ReviewStatus                 string                                  `json:"review_status"`
	TerminalClass                string                                  `json:"terminal_class"`
	Outcome                      string                                  `json:"outcome"`
	FailureKind                  string                                  `json:"failure_kind"`
	FailureReason                string                                  `json:"failure_reason"`
	FailureID                    string                                  `json:"failure_id"`
	RecordPath                   string                                  `json:"record_path"`
	PacketPath                   string                                  `json:"packet_path"`
	NextAction                   string                                  `json:"next_valid_action"`
	CompletionSource             string                                  `json:"completion_source"`
	ReviewRepairSkipped          bool                                    `json:"review_repair_skipped"`
	ContractID                   string                                  `json:"contract_id"`
	ContractHash                 string                                  `json:"contract_hash"`
	SnapshotHash                 string                                  `json:"snapshot_hash"`
	ScopeContractID              string                                  `json:"scope_contract_id"`
	ScopeContractHash            string                                  `json:"scope_contract_hash"`
	ActiveContractID             string                                  `json:"active_contract_id"`
	ActiveContractHash           string                                  `json:"active_contract_hash"`
	ContractSnapshotHash         string                                  `json:"contract_snapshot_hash"`
	RequiredEditPaths            []string                                `json:"required_edit_paths"`
	AllowedEditPaths             []string                                `json:"allowed_edit_paths"`
	SourceSatisfiedEvidencePaths []string                                `json:"source_satisfied_evidence_paths"`
	SourceSatisfiedEvidence      []PlanExecuteSourceSatisfiedEvidenceRef `json:"source_satisfied_evidence"`
	ChangedPaths                 []string                                `json:"changed_paths"`
	SourceSanity                 []coreplanexecute.PhaseEvidenceCheck    `json:"source_sanity"`
	AcceptanceEvidenceIDs        []string                                `json:"acceptance_evidence_ids"`
	AcceptanceEvidenceTrusted    bool                                    `json:"acceptance_evidence_trusted"`
	ValidationFailures           []string                                `json:"validation_failures"`
	PlanExecute                  *nestedPlanExecuteCompletion            `json:"plan_execute,omitempty"`
	Nested                       *nestedPlanExecuteCompletion            `json:"nested,omitempty"`
	QuestionSummary              string                                  `json:"question_summary"`
	BlockingClaim                string                                  `json:"blocking_claim"`
}

type nestedPlanExecuteCompletion struct {
	LaneFamily                   string                                  `json:"lane_family"`
	Summary                      string                                  `json:"summary"`
	ProgressPath                 string                                  `json:"progress_path"`
	Status                       string                                  `json:"status"`
	Phase                        string                                  `json:"phase"`
	ReviewStatus                 string                                  `json:"review_status"`
	TerminalClass                string                                  `json:"terminal_class"`
	Outcome                      string                                  `json:"outcome"`
	FailureKind                  string                                  `json:"failure_kind"`
	FailureReason                string                                  `json:"failure_reason"`
	FailureID                    string                                  `json:"failure_id"`
	RecordPath                   string                                  `json:"record_path"`
	PacketPath                   string                                  `json:"packet_path"`
	NextAction                   string                                  `json:"next_valid_action"`
	CompletionSource             string                                  `json:"completion_source"`
	ReviewRepairSkipped          bool                                    `json:"review_repair_skipped"`
	ContractID                   string                                  `json:"contract_id"`
	ContractHash                 string                                  `json:"contract_hash"`
	SnapshotHash                 string                                  `json:"snapshot_hash"`
	ScopeContractID              string                                  `json:"scope_contract_id"`
	ScopeContractHash            string                                  `json:"scope_contract_hash"`
	ActiveContractID             string                                  `json:"active_contract_id"`
	ActiveContractHash           string                                  `json:"active_contract_hash"`
	ContractSnapshotHash         string                                  `json:"contract_snapshot_hash"`
	RequiredEditPaths            []string                                `json:"required_edit_paths"`
	AllowedEditPaths             []string                                `json:"allowed_edit_paths"`
	SourceSatisfiedEvidencePaths []string                                `json:"source_satisfied_evidence_paths"`
	SourceSatisfiedEvidence      []PlanExecuteSourceSatisfiedEvidenceRef `json:"source_satisfied_evidence"`
	ChangedPaths                 []string                                `json:"changed_paths"`
	SourceSanity                 []coreplanexecute.PhaseEvidenceCheck    `json:"source_sanity"`
	AcceptanceEvidenceIDs        []string                                `json:"acceptance_evidence_ids"`
	AcceptanceEvidenceTrusted    bool                                    `json:"acceptance_evidence_trusted"`
	ValidationFailures           []string                                `json:"validation_failures"`
}

type planExecuteCompletion struct {
	LaneFamily                   string
	Summary                      string
	ProgressPath                 string
	Status                       string
	Phase                        string
	ReviewStatus                 string
	TerminalClass                string
	Outcome                      string
	FailureKind                  string
	FailureReason                string
	FailureID                    string
	RecordPath                   string
	PacketPath                   string
	NextAction                   string
	CompletionSource             string
	ReviewRepairSkipped          bool
	ContractID                   string
	ContractHash                 string
	SnapshotHash                 string
	ScopeContractID              string
	ScopeContractHash            string
	RequiredEditPaths            []string
	AllowedEditPaths             []string
	SourceSatisfiedEvidencePaths []string
	SourceSatisfiedEvidence      []PlanExecuteSourceSatisfiedEvidenceRef
	ChangedPaths                 []string
	SourceSanity                 []coreplanexecute.PhaseEvidenceCheck
	AcceptanceEvidenceIDs        []string
	AcceptanceEvidenceTrusted    bool
	ValidationFailures           []string
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

func bindPlanExecuteCompletionToActiveAuthority(completion planExecuteCompletion, state FeatureState, atom FeatureAtomState) planExecuteCompletion {
	completion = completion.normalized()
	if completion.ContractID == "" {
		completion.ContractID = firstActiveRunAuthorityString(state, atom.ID,
			func(run ActiveRun) string { return run.InvocationContractID },
			atom.RuntimeEnvelope.ContractID,
		)
	}

	if completion.SnapshotHash == "" {
		completion.SnapshotHash = firstActiveRunAuthorityString(state, atom.ID,
			func(run ActiveRun) string { return run.RuntimeEnvelopeID },
			atom.RuntimeEnvelope.ProtocolHash,
		)
	}

	return completion.normalized()
}

func firstActiveRunAuthorityString(state FeatureState, atomID string, selector func(ActiveRun) string, fallback string) string {
	for _, run := range state.ActiveRuns {
		if strings.TrimSpace(run.AtomID) != strings.TrimSpace(atomID) {
			continue
		}

		if value := strings.TrimSpace(selector(run)); value != "" {
			return value
		}
	}

	return strings.TrimSpace(fallback)
}

func (completion *planExecuteCompletion) overlay(nested nestedPlanExecuteCompletion) {
	merged := MergePlanExecuteCompletionSummaries(
		completion.summary(),
		PlanExecuteCompletionSummary{
			LaneFamily:                   nested.LaneFamily,
			Summary:                      nested.Summary,
			ProgressPath:                 nested.ProgressPath,
			Status:                       nested.Status,
			Phase:                        nested.Phase,
			ReviewStatus:                 nested.ReviewStatus,
			TerminalClass:                nested.TerminalClass,
			Outcome:                      nested.Outcome,
			FailureKind:                  nested.FailureKind,
			FailureReason:                nested.FailureReason,
			FailureID:                    nested.FailureID,
			RecordPath:                   nested.RecordPath,
			PacketPath:                   nested.PacketPath,
			NextAction:                   nested.NextAction,
			CompletionSource:             nested.CompletionSource,
			ReviewRepairSkipped:          nested.ReviewRepairSkipped,
			ContractID:                   FirstNonEmptyString(nested.ContractID, nested.ActiveContractID),
			ContractHash:                 FirstNonEmptyString(nested.ContractHash, nested.ActiveContractHash),
			SnapshotHash:                 FirstNonEmptyString(nested.SnapshotHash, nested.ContractSnapshotHash),
			ScopeContractID:              nested.ScopeContractID,
			ScopeContractHash:            nested.ScopeContractHash,
			RequiredEditPaths:            nested.RequiredEditPaths,
			AllowedEditPaths:             nested.AllowedEditPaths,
			SourceSatisfiedEvidencePaths: nested.SourceSatisfiedEvidencePaths,
			SourceSatisfiedEvidence:      nested.SourceSatisfiedEvidence,
			ChangedPaths:                 nested.ChangedPaths,
			SourceSanity:                 nested.SourceSanity,
			AcceptanceEvidenceIDs:        nested.AcceptanceEvidenceIDs,
			AcceptanceEvidenceTrusted:    nested.AcceptanceEvidenceTrusted,
			ValidationFailures:           nested.ValidationFailures,
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
	completion.TerminalClass = strings.TrimSpace(completion.TerminalClass)
	completion.Outcome = strings.TrimSpace(completion.Outcome)

	if strings.EqualFold(completion.Status, "source-satisfied") && completion.Outcome == "" {
		completion.Outcome = "source_satisfied"
	}

	completion.FailureKind = strings.TrimSpace(completion.FailureKind)
	completion.FailureReason = strings.TrimSpace(completion.FailureReason)

	if planExecuteCompletionSourceSatisfied(completion) && completion.FailureKind == "" && completion.FailureReason == "" {
		completion.Status = "source-satisfied"
	}

	completion.FailureID = strings.TrimSpace(completion.FailureID)
	completion.RecordPath = strings.TrimSpace(completion.RecordPath)
	completion.PacketPath = strings.TrimSpace(completion.PacketPath)
	completion.NextAction = strings.TrimSpace(completion.NextAction)
	completion.CompletionSource = strings.TrimSpace(completion.CompletionSource)
	completion.ContractID = strings.TrimSpace(completion.ContractID)
	completion.ContractHash = strings.TrimSpace(completion.ContractHash)
	completion.SnapshotHash = strings.TrimSpace(completion.SnapshotHash)

	if completion.SnapshotHash == "" {
		completion.SnapshotHash = completion.ContractHash
	}

	completion.ScopeContractID = strings.TrimSpace(completion.ScopeContractID)
	completion.ScopeContractHash = strings.TrimSpace(completion.ScopeContractHash)
	completion.RequiredEditPaths = normalizeCompletionStringList(completion.RequiredEditPaths)
	completion.AllowedEditPaths = normalizeCompletionStringList(completion.AllowedEditPaths)
	completion.SourceSatisfiedEvidencePaths = normalizeCompletionStringList(completion.SourceSatisfiedEvidencePaths)
	completion.SourceSatisfiedEvidence = normalizePlanExecuteSourceSatisfiedEvidenceRefs(completion.SourceSatisfiedEvidence)
	completion.ChangedPaths = normalizeCompletionStringList(completion.ChangedPaths)
	completion.SourceSanity = normalizeCompletionPhaseEvidenceChecks(completion.SourceSanity)
	completion.AcceptanceEvidenceIDs = normalizeCompletionStableIDList(completion.AcceptanceEvidenceIDs)

	if len(completion.AcceptanceEvidenceIDs) == 0 || !planExecuteCompletionSourceTrustsAcceptanceEvidence(completion.CompletionSource) {
		completion.AcceptanceEvidenceTrusted = false
	}

	completion.ValidationFailures = normalizeCompletionStringList(completion.ValidationFailures)

	return completion
}

func planExecuteCompletionSummaryFromEnvelope(envelope implementorOutputEnvelope) PlanExecuteCompletionSummary {
	return PlanExecuteCompletionSummary{
		LaneFamily:                   envelope.LaneFamily,
		Summary:                      envelope.Summary,
		ProgressPath:                 envelope.ProgressPath,
		Status:                       envelope.Status,
		Phase:                        envelope.Phase,
		ReviewStatus:                 envelope.ReviewStatus,
		TerminalClass:                envelope.TerminalClass,
		Outcome:                      envelope.Outcome,
		FailureKind:                  envelope.FailureKind,
		FailureReason:                envelope.FailureReason,
		FailureID:                    envelope.FailureID,
		RecordPath:                   envelope.RecordPath,
		PacketPath:                   envelope.PacketPath,
		NextAction:                   envelope.NextAction,
		CompletionSource:             envelope.CompletionSource,
		ReviewRepairSkipped:          envelope.ReviewRepairSkipped,
		ContractID:                   FirstNonEmptyString(envelope.ContractID, envelope.ActiveContractID),
		ContractHash:                 FirstNonEmptyString(envelope.ContractHash, envelope.ActiveContractHash),
		SnapshotHash:                 FirstNonEmptyString(envelope.SnapshotHash, envelope.ContractSnapshotHash),
		ScopeContractID:              envelope.ScopeContractID,
		ScopeContractHash:            envelope.ScopeContractHash,
		RequiredEditPaths:            envelope.RequiredEditPaths,
		AllowedEditPaths:             envelope.AllowedEditPaths,
		SourceSatisfiedEvidencePaths: envelope.SourceSatisfiedEvidencePaths,
		SourceSatisfiedEvidence:      envelope.SourceSatisfiedEvidence,
		ChangedPaths:                 envelope.ChangedPaths,
		SourceSanity:                 envelope.SourceSanity,
		AcceptanceEvidenceIDs:        envelope.AcceptanceEvidenceIDs,
		AcceptanceEvidenceTrusted:    envelope.AcceptanceEvidenceTrusted,
		ValidationFailures:           envelope.ValidationFailures,
	}
}

func (completion planExecuteCompletion) summary() PlanExecuteCompletionSummary {
	return PlanExecuteCompletionSummary{
		LaneFamily:                   completion.LaneFamily,
		Summary:                      completion.Summary,
		ProgressPath:                 completion.ProgressPath,
		Status:                       completion.Status,
		Phase:                        completion.Phase,
		ReviewStatus:                 completion.ReviewStatus,
		TerminalClass:                completion.TerminalClass,
		Outcome:                      completion.Outcome,
		FailureKind:                  completion.FailureKind,
		FailureReason:                completion.FailureReason,
		FailureID:                    completion.FailureID,
		RecordPath:                   completion.RecordPath,
		PacketPath:                   completion.PacketPath,
		NextAction:                   completion.NextAction,
		CompletionSource:             completion.CompletionSource,
		ReviewRepairSkipped:          completion.ReviewRepairSkipped,
		ContractID:                   completion.ContractID,
		ContractHash:                 completion.ContractHash,
		SnapshotHash:                 completion.SnapshotHash,
		ScopeContractID:              completion.ScopeContractID,
		ScopeContractHash:            completion.ScopeContractHash,
		RequiredEditPaths:            completion.RequiredEditPaths,
		AllowedEditPaths:             completion.AllowedEditPaths,
		SourceSatisfiedEvidencePaths: completion.SourceSatisfiedEvidencePaths,
		SourceSatisfiedEvidence:      completion.SourceSatisfiedEvidence,
		ChangedPaths:                 completion.ChangedPaths,
		SourceSanity:                 completion.SourceSanity,
		AcceptanceEvidenceIDs:        completion.AcceptanceEvidenceIDs,
		AcceptanceEvidenceTrusted:    completion.AcceptanceEvidenceTrusted,
		ValidationFailures:           completion.ValidationFailures,
	}
}

func planExecuteCompletionFromSummary(summary PlanExecuteCompletionSummary) planExecuteCompletion {
	return planExecuteCompletion{
		LaneFamily:                   summary.LaneFamily,
		Summary:                      summary.Summary,
		ProgressPath:                 summary.ProgressPath,
		Status:                       summary.Status,
		Phase:                        summary.Phase,
		ReviewStatus:                 summary.ReviewStatus,
		TerminalClass:                summary.TerminalClass,
		Outcome:                      summary.Outcome,
		FailureKind:                  summary.FailureKind,
		FailureReason:                summary.FailureReason,
		FailureID:                    summary.FailureID,
		RecordPath:                   summary.RecordPath,
		PacketPath:                   summary.PacketPath,
		NextAction:                   summary.NextAction,
		CompletionSource:             summary.CompletionSource,
		ReviewRepairSkipped:          summary.ReviewRepairSkipped,
		ContractID:                   summary.ContractID,
		ContractHash:                 summary.ContractHash,
		SnapshotHash:                 summary.SnapshotHash,
		ScopeContractID:              summary.ScopeContractID,
		ScopeContractHash:            summary.ScopeContractHash,
		RequiredEditPaths:            summary.RequiredEditPaths,
		AllowedEditPaths:             summary.AllowedEditPaths,
		SourceSatisfiedEvidencePaths: summary.SourceSatisfiedEvidencePaths,
		SourceSatisfiedEvidence:      summary.SourceSatisfiedEvidence,
		ChangedPaths:                 summary.ChangedPaths,
		SourceSanity:                 summary.SourceSanity,
		AcceptanceEvidenceIDs:        summary.AcceptanceEvidenceIDs,
		AcceptanceEvidenceTrusted:    summary.AcceptanceEvidenceTrusted,
		ValidationFailures:           summary.ValidationFailures,
	}.normalized()
}

func planExecuteCompletionIsSuccess(completion planExecuteCompletion, state FeatureState, atom FeatureAtomState) (bool, string) {
	decision := PlanExecuteCompletionTerminalDecision(map[string]any{
		"status":                completion.Status,
		"outcome":               completion.Outcome,
		"phase":                 completion.Phase,
		"review_status":         completion.ReviewStatus,
		"terminal_class":        completion.TerminalClass,
		"failure_kind":          completion.FailureKind,
		"failure_reason":        completion.FailureReason,
		"review_repair_skipped": completion.ReviewRepairSkipped,
		"validation_failures":   completion.ValidationFailures,
	})
	if decision.Class != PlanExecuteTerminalCompleted {
		return false, decision.Reason
	}

	if reason := validateCompletionAgainstActiveContract(completion, state, atom); reason != "" {
		return false, reason
	}

	return true, ""
}

func validateCompletionAgainstActiveContract(completion planExecuteCompletion, state FeatureState, atom FeatureAtomState) string {
	expectedContractID := strings.TrimSpace(atom.RuntimeEnvelope.ContractID)
	expectedSnapshot := strings.TrimSpace(atom.RuntimeEnvelope.ProtocolHash)

	if expectedContractID == "" || expectedSnapshot == "" {
		return "active atom is missing typed plan contract snapshot"
	}

	if explicitParentIdentityConflict(completion, expectedContractID, expectedSnapshot) {
		return "plan-execute terminal completion conflicts with the active plan contract snapshot"
	}

	if !completionMatchesActiveContract(completion, expectedContractID, expectedSnapshot) {
		return "plan-execute terminal completion is not correlated to the active plan contract snapshot"
	}

	contract := CompilePlanAuthorityContract(state.Plan, state.AuthorityDeltas)
	atomContract, ok := planAuthorityContractAtom(contract, atom.ID)

	if !ok {
		return "active atom is missing compiled plan execution contract"
	}

	required := atomContract.Scope.RequiredEditPaths()
	allowed := atomContract.Scope.AllowedEditPaths()
	changed := plancontract.UniqueScopePaths(completion.ChangedPaths)
	sourceSatisfied := plancontract.UniqueScopePaths(completion.SourceSatisfiedEvidencePaths)

	if planExecuteCompletionSourceSatisfied(completion) {
		if len(changed) > 0 {
			return "source-satisfied completion must not include changed_paths"
		}

		if len(required) == 0 {
			return "source-satisfied completion missing required edit scope"
		}

		if len(sourceSatisfied) == 0 {
			return "source-satisfied completion missing typed source_satisfied_evidence_paths"
		}

		if !completionPathsWithinScope(sourceSatisfied, required) {
			return "source-satisfied completion includes paths outside required edit scope"
		}

		if missing := missingSourceSatisfiedEvidenceRefs(sourceSatisfied, completion.SourceSatisfiedEvidence); len(missing) > 0 {
			return "source-satisfied completion missing typed source_satisfied_evidence refs: " + strings.Join(missing, ", ")
		}

		if missing := missingRequiredCompletionPaths(required, sourceSatisfied); len(missing) > 0 {
			return "source-satisfied completion does not cover required edit paths: " + strings.Join(missing, ", ")
		}

		if reason := untrustedAcceptanceEvidenceReason(atomContract, completion, "source-satisfied completion"); reason != "" {
			return reason
		}

		if missing := missingAcceptanceEvidenceIDs(atomContract, trustedAcceptanceEvidenceIDs(completion)); len(missing) > 0 {
			return "source-satisfied completion missing acceptance_evidence_ids: " + strings.Join(missing, ", ")
		}

		return ""
	}

	if len(changed) == 0 {
		return "completed plan-execute terminal completion missing typed changed_paths"
	}

	covered := plancontract.UniqueScopePaths(append(append([]string(nil), changed...), sourceSatisfied...))
	if missing := missingRequiredCompletionPaths(required, covered); len(missing) > 0 {
		return "completed plan-execute terminal completion does not cover required edit paths: " + strings.Join(missing, ", ")
	}

	scope := plancontract.UniqueScopePaths(append(append([]string(nil), required...), allowed...))
	if len(scope) > 0 && !completionPathsWithinScope(changed, scope) {
		return "completed plan-execute terminal completion changed paths outside active contract scope"
	}

	if failures := planExecuteTerminalValidationFailures(completion); len(failures) > 0 {
		return "nested plan-execute terminal validation failed: " + strings.Join(failures, "; ")
	}

	if reason := untrustedAcceptanceEvidenceReason(atomContract, completion, "completed plan-execute terminal completion"); reason != "" {
		return reason
	}

	if missing := missingAcceptanceEvidenceIDs(atomContract, trustedAcceptanceEvidenceIDs(completion)); len(missing) > 0 {
		return "completed plan-execute terminal completion missing acceptance_evidence_ids: " + strings.Join(missing, ", ")
	}

	if reason := atomContract.Scope.EditSetObligations.RejectionReason(changed); reason != "" {
		return plancontract.EditSetObligationFailureMessage(reason)
	}

	return ""
}

func untrustedAcceptanceEvidenceReason(atom plancontract.AtomExecutionContract, completion planExecuteCompletion, prefix string) string {
	if completion.AcceptanceEvidenceTrusted || len(completion.AcceptanceEvidenceIDs) == 0 || len(requiredAcceptanceEvidenceIDs(atom)) == 0 {
		return ""
	}

	return prefix + " acceptance_evidence_ids are not backed by trusted plan-execute terminal evidence"
}

func trustedAcceptanceEvidenceIDs(completion planExecuteCompletion) []string {
	if !completion.AcceptanceEvidenceTrusted {
		return nil
	}

	return completion.AcceptanceEvidenceIDs
}

func missingAcceptanceEvidenceIDs(atom plancontract.AtomExecutionContract, provided []string) []string {
	providedSet := map[string]bool{}
	for _, id := range normalizeCompletionStableIDList(provided) {
		providedSet[id] = true
	}

	var missing []string

	for _, obligation := range atom.AcceptanceObligations {
		if !obligation.Required {
			continue
		}

		id := plancontract.NormalizeStableContractIDPart(obligation.ID)
		if id == "" {
			continue
		}

		if !providedSet[id] {
			missing = append(missing, id)
		}
	}

	return normalizeCompletionStableIDList(missing)
}

func requiredAcceptanceEvidenceIDs(atom plancontract.AtomExecutionContract) []string {
	var ids []string

	for _, obligation := range atom.AcceptanceObligations {
		if obligation.Required && strings.TrimSpace(obligation.ID) != "" {
			ids = append(ids, obligation.ID)
		}
	}

	return normalizeCompletionStableIDList(ids)
}

func planExecuteTerminalValidationFailures(completion planExecuteCompletion) []string {
	fields := completion.summary().Fields()
	record := coreplanexecute.PlanExecuteTerminalCompletionRecordFromFields(fields)

	return normalizeCompletionStringList(record.ValidationFailures)
}

func explicitParentIdentityConflict(completion planExecuteCompletion, expectedContractID string, expectedSnapshot string) bool {
	if candidate := strings.TrimSpace(completion.ContractID); candidate != "" && candidate != expectedContractID {
		return true
	}

	if candidate := strings.TrimSpace(completion.SnapshotHash); candidate != "" && candidate != expectedSnapshot {
		return true
	}

	return strings.TrimSpace(completion.ContractHash) != "" && strings.TrimSpace(completion.ContractHash) != expectedSnapshot
}

func completionMatchesActiveContract(completion planExecuteCompletion, expectedContractID string, expectedSnapshot string) bool {
	if strings.TrimSpace(completion.ContractID) != "" && strings.TrimSpace(completion.ContractID) != expectedContractID {
		return false
	}

	if strings.TrimSpace(completion.SnapshotHash) != "" && strings.TrimSpace(completion.SnapshotHash) != expectedSnapshot {
		return false
	}

	if strings.TrimSpace(completion.ContractHash) != "" && strings.TrimSpace(completion.ContractHash) != expectedSnapshot {
		return false
	}

	return strings.TrimSpace(completion.ContractID) == expectedContractID ||
		strings.TrimSpace(completion.SnapshotHash) == expectedSnapshot ||
		strings.TrimSpace(completion.ContractHash) == expectedSnapshot
}

func missingRequiredCompletionPaths(required []string, covered []string) []string {
	coveredSet := normalizedStringSet(covered)

	var missing []string

	for _, path := range required {
		if !coveredSet[path] {
			missing = append(missing, path)
		}
	}

	return missing
}

func completionPathsWithinScope(paths []string, scope []string) bool {
	scopeSet := normalizedStringSet(scope)
	for _, path := range paths {
		if !scopeSet[path] {
			return false
		}
	}

	return true
}

func completeAtomFromPlanExecute(state *FeatureState, atom *FeatureAtomState, completion planExecuteCompletion) []Event {
	atomFrom := atom.State
	atomTo := AtomStateComplete
	eventType := "idiom.feature_planner.atom_completed"

	if planExecuteCompletionSourceSatisfied(completion) {
		atomTo = AtomStateSourceSatisfied
		eventType = "idiom.feature_planner.atom_source_satisfied"
	}

	atom.State = atomTo
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

	if completion.Outcome != "" {
		fields["outcome"] = completion.Outcome
	}

	if completion.TerminalClass != "" {
		fields["terminal_class"] = completion.TerminalClass
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

	addPlanExecuteCompletionEvidenceFields(fields, completion)

	return []Event{{
		Type:       eventType,
		SessionID:  state.SessionID,
		FeatureTo:  featureTo,
		AtomID:     atom.ID,
		AtomFrom:   atomFrom,
		AtomTo:     atomTo,
		AtomDeltas: []AtomDelta{{Op: "update", AtomID: atom.ID, AtomFrom: atomFrom, AtomTo: atomTo}},
		ActiveRunDeltas: []ActiveRunDelta{{
			Op:            "fold_terminal",
			AtomID:        atom.ID,
			LaneSessionID: atom.LastRunID,
			BeforeState:   "running",
			AfterState:    atomTo,
			ResultSource:  "plan-execute",
			ProgressPath:  completion.ProgressPath,
		}},
		Fields: fields,
	}}
}

func planExecuteCompletionSourceSatisfied(completion planExecuteCompletion) bool {
	return util.EqualTrimFold(completion.Outcome, "source_satisfied") ||
		util.EqualTrimFold(completion.Status, "source-satisfied")
}

func planExecuteCompletionSourceTrustsAcceptanceEvidence(source string) bool {
	switch strings.TrimSpace(source) {
	case "headless_plan_execute_returned",
		"tui_plan_execute_returned",
		"durable_plan_execute_terminal_completion",
		"durable_plan_execute_progress":
		return true
	default:
		return false
	}
}

func rejectPlanExecuteCompletion(state *FeatureState, atom *FeatureAtomState, completion planExecuteCompletion, reason string) []Event {
	fields := planExecuteCompletionFields(completion)
	if detail := planExecuteCompletionFailureDetail(completion); detail != "" {
		reason = strings.TrimSpace(reason + ": " + detail)
	}

	return recoverAtom(state, atom, reason, "repair_nested_plan_execute_completion", "idiom.feature_planner.plan_execute_completion_rejected", fields)
}

func planExecuteCompletionRetryable(completion planExecuteCompletion) bool {
	return strings.TrimSpace(completion.NextAction) == "retry_plan_execute"
}

func markPlanExecuteRetryPending(state *FeatureState, atom *FeatureAtomState, completion planExecuteCompletion, reason string) []Event {
	fields := planExecuteCompletionFields(completion)
	if detail := planExecuteCompletionFailureDetail(completion); detail != "" {
		reason = strings.TrimSpace(reason + ": " + detail)
	}

	featureFrom := state.Status
	atomFrom := atom.State
	state.Status = FeatureStateDispatching
	atom.State = AtomStateImplementing
	state.Continuation.ResumeAtomID = atom.ID
	state.Continuation.ResumeAction = "retry_plan_execute"
	state.Continuation.ResumeReason = reason
	state.Continuation.BlockedAtoms = removeFeaturePlannerString(state.Continuation.BlockedAtoms, atom.ID)
	state.Blockers = removeFeaturePlannerString(state.Blockers, reason)
	atom.Blockers = removeFeaturePlannerString(atom.Blockers, reason)
	foldActiveRun(state, atom.ID, atom.LastRunID)

	return []Event{{
		Type:        "idiom.feature_planner.plan_execute_retry_pending",
		SessionID:   state.SessionID,
		FeatureFrom: featureFrom,
		FeatureTo:   FeatureStateDispatching,
		AtomID:      atom.ID,
		AtomFrom:    atomFrom,
		AtomTo:      AtomStateImplementing,
		AtomDeltas:  []AtomDelta{{Op: "update", AtomID: atom.ID, AtomFrom: atomFrom, AtomTo: AtomStateImplementing}},
		ActiveRunDeltas: []ActiveRunDelta{{
			Op:            "fold_terminal",
			AtomID:        atom.ID,
			LaneSessionID: atom.LastRunID,
			BeforeState:   "running",
			AfterState:    "retry_plan_execute",
		}},
		Message: reason,
		Fields:  fields,
	}}
}

func removeFeaturePlannerString(values []string, target string) []string {
	target = strings.TrimSpace(target)
	if target == "" || len(values) == 0 {
		return values
	}

	out := values[:0]
	for _, value := range values {
		if strings.TrimSpace(value) == target {
			continue
		}
		out = append(out, value)
	}

	return out
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

	if completion.Outcome != "" {
		fields["outcome"] = completion.Outcome
	}

	if completion.TerminalClass != "" {
		fields["terminal_class"] = completion.TerminalClass
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

	addPlanExecuteCompletionEvidenceFields(fields, completion)

	return fields
}

func addPlanExecuteCompletionEvidenceFields(fields map[string]any, completion planExecuteCompletion) {
	if completion.ContractID != "" {
		fields["contract_id"] = completion.ContractID
	}

	if completion.SnapshotHash != "" {
		fields["snapshot_hash"] = completion.SnapshotHash
	}

	if completion.ScopeContractID != "" {
		fields["scope_contract_id"] = completion.ScopeContractID
	}

	if completion.ScopeContractHash != "" {
		fields["scope_contract_hash"] = completion.ScopeContractHash
	}

	if len(completion.RequiredEditPaths) > 0 {
		fields["required_edit_paths"] = append([]string(nil), completion.RequiredEditPaths...)
	}

	if len(completion.AllowedEditPaths) > 0 {
		fields["allowed_edit_paths"] = append([]string(nil), completion.AllowedEditPaths...)
	}

	if len(completion.SourceSatisfiedEvidencePaths) > 0 {
		fields["source_satisfied_evidence_paths"] = append([]string(nil), completion.SourceSatisfiedEvidencePaths...)
	}

	if len(completion.SourceSatisfiedEvidence) > 0 {
		fields["source_satisfied_evidence"] = append([]PlanExecuteSourceSatisfiedEvidenceRef(nil), completion.SourceSatisfiedEvidence...)
	}

	if len(completion.ChangedPaths) > 0 {
		fields["changed_paths"] = append([]string(nil), completion.ChangedPaths...)
	}

	if len(completion.SourceSanity) > 0 {
		fields["source_sanity"] = append([]coreplanexecute.PhaseEvidenceCheck(nil), completion.SourceSanity...)
	}

	if len(completion.AcceptanceEvidenceIDs) > 0 {
		fields["acceptance_evidence_ids"] = append([]string(nil), completion.AcceptanceEvidenceIDs...)
	}

	if completion.AcceptanceEvidenceTrusted {
		fields["acceptance_evidence_trusted"] = true
	}
}

func missingSourceSatisfiedEvidenceRefs(paths []string, refs []PlanExecuteSourceSatisfiedEvidenceRef) []string {
	covered := map[string]bool{}

	for _, ref := range normalizePlanExecuteSourceSatisfiedEvidenceRefs(refs) {
		if !plancontract.SourceSatisfiedEvidenceKindProvesCurrentState(ref.Kind) {
			continue
		}

		covered[ref.Path] = true
	}

	var missing []string

	for _, path := range plancontract.UniqueScopePaths(paths) {
		if !covered[path] {
			missing = append(missing, path)
		}
	}

	return missing
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
			delta, err := applyQuestionAnswerAuthority(&state.OpenQuestions[i], answer, len(state.AuthorityDeltas)+1, time.Now().UTC())
			if err != nil {
				state.Status = FeatureStatePlanningQuestion
				state.PlanningLoop.State = FeatureStatePlanningQuestion
				state.Blockers = append(state.Blockers, err.Error())

				return []Event{{Type: "idiom.feature_planner.planner_question_answer_rejected", SessionID: state.SessionID, Message: err.Error()}}
			}

			upsertPlanAuthorityDelta(state, delta)
			clearAnsweredNewFileAuthorizationBlockers(state, delta)

			state.OpenQuestions[i].Status = "answered"
			state.Status = FeatureStatePlanning
			state.PlanningLoop.State = FeatureStatePlanning

			return []Event{{Type: "idiom.feature_planner.planner_question_answered", SessionID: state.SessionID, Message: state.OpenQuestions[i].QuestionSummary, Fields: map[string]any{"authority_delta": delta}}}
		}
	}

	return nil
}

func applyQuestionAnswerAuthority(question *PlannerQuestion, rawAnswer string, revision int, now time.Time) (PlanAuthorityDelta, error) {
	if question == nil {
		return PlanAuthorityDelta{}, fmt.Errorf("planner question is nil")
	}

	answer := strings.TrimSpace(rawAnswer)
	if answer == "" {
		return PlanAuthorityDelta{}, fmt.Errorf("planner question answer is empty")
	}

	if revision <= 0 {
		revision = 1
	}

	if now.IsZero() {
		now = time.Now().UTC()
	}

	required := strings.TrimSpace(question.AuthorityRequired)
	if required == "" || required == "operator_approval" {
		question.Answer = answer
		if strings.TrimSpace(question.AnswerSourceKind) == "" {
			question.AnswerSourceKind = "operator_approval"
		}

		delta := planAuthorityDeltaFromQuestion(*question, revision, now)

		return delta, nil
	}

	var payload struct {
		Answer           string   `json:"answer"`
		AnswerSourceKind string   `json:"answer_source_kind"`
		AuthorityKind    string   `json:"authority_kind"`
		EvidenceRefs     []string `json:"evidence_refs"`
		AffectedPaths    []string `json:"affected_paths"`
		AffectedFields   []string `json:"affected_fields"`
		Expected         string   `json:"expected"`
		Actual           string   `json:"actual"`
	}

	if err := json.Unmarshal([]byte(answer), &payload); err != nil {
		return PlanAuthorityDelta{}, fmt.Errorf("planner question %q requires typed %s authority answer", question.ID, required)
	}

	if strings.TrimSpace(payload.AnswerSourceKind) != required {
		return PlanAuthorityDelta{}, fmt.Errorf("planner question %q requires answer_source_kind %q", question.ID, required)
	}

	if strings.TrimSpace(payload.Answer) == "" {
		return PlanAuthorityDelta{}, fmt.Errorf("planner question %q typed answer missing answer", question.ID)
	}

	if len(payload.EvidenceRefs) == 0 {
		return PlanAuthorityDelta{}, fmt.Errorf("planner question %q typed answer missing evidence_refs", question.ID)
	}

	question.Answer = payload.Answer
	question.AnswerSourceKind = payload.AnswerSourceKind
	question.EvidenceRefs = append(question.EvidenceRefs, payload.EvidenceRefs...)

	if len(payload.AffectedPaths) > 0 {
		question.AffectedPaths = append(question.AffectedPaths, payload.AffectedPaths...)
	}

	if len(payload.AffectedFields) > 0 {
		question.AffectedFields = append(question.AffectedFields, payload.AffectedFields...)
	}

	delta := planAuthorityDeltaFromQuestion(*question, revision, now)
	if strings.TrimSpace(payload.AuthorityKind) != "" {
		delta.AuthorityKind = strings.TrimSpace(payload.AuthorityKind)
	}

	if len(payload.AffectedFields) > 0 {
		delta.AffectedFields = normalizedStringList(append(delta.AffectedFields, payload.AffectedFields...))
	}

	if strings.TrimSpace(payload.Expected) != "" {
		delta.Expected = strings.TrimSpace(payload.Expected)
	}

	if strings.TrimSpace(payload.Actual) != "" {
		delta.Actual = strings.TrimSpace(payload.Actual)
	}

	return delta, nil
}

func missingNewFileAuthorizationQuestion(err error, prompt string) (PlannerQuestion, bool) {
	var missing *MissingNewFileAuthorizationError
	if !errors.As(err, &missing) {
		return PlannerQuestion{}, false
	}

	if !promptRequestsNewFileAuthorizationQuestion(prompt) {
		return PlannerQuestion{}, false
	}

	paths := normalizeEditScopeList(missing.Paths)
	if len(paths) == 0 {
		return PlannerQuestion{}, false
	}

	return PlannerQuestion{
		QuestionSummary:   "Authorize new files for feature plan",
		QuestionKind:      "external_answer_required",
		BlockingClaim:     "The submitted feature plan includes missing workspace paths that require explicit new-file authorization before dispatch.",
		RequestedDecision: fmt.Sprintf("Authorize creation of exactly these missing repo-relative paths: %s", strings.Join(paths, ", ")),
		AuthorityRequired: "operator_approval",
		RequiredBefore:    "plan_approval",
		AffectedPaths:     paths,
		AffectedFields:    []string{"new_file_authorization"},
	}, true
}

func promptRequestsNewFileAuthorizationQuestion(prompt string) bool {
	lower := strings.ToLower(strings.TrimSpace(prompt))
	if lower == "" {
		return false
	}

	return strings.Contains(lower, "if any required new") &&
		strings.Contains(lower, "missing") &&
		(strings.Contains(lower, "ask me") || strings.Contains(lower, "ask the operator") || strings.Contains(lower, "ask product")) &&
		(strings.Contains(lower, "authorization") || strings.Contains(lower, "authorize") || strings.Contains(lower, "approval"))
}

func planAuthorityDeltaFromQuestion(question PlannerQuestion, revision int, now time.Time) PlanAuthorityDelta {
	if now.IsZero() {
		now = time.Now().UTC()
	}

	evidenceRefs := normalizedStringList(question.EvidenceRefs)
	if len(evidenceRefs) == 0 {
		evidenceRefs = []string{"planner_question:" + firstNonEmpty(question.QuestionID, question.ID)}
	}

	return PlanAuthorityDelta{
		QuestionID:     firstNonEmpty(question.QuestionID, question.ID),
		AuthorityKind:  firstNonEmpty(question.AnswerSourceKind, question.AuthorityRequired, "operator_approval"),
		EvidenceRefs:   evidenceRefs,
		AffectedPaths:  normalizeEditScopeList(question.AffectedPaths),
		AffectedFields: normalizedStringList(question.AffectedFields),
		Expected:       strings.TrimSpace(question.BlockingClaim),
		Actual:         strings.TrimSpace(question.Answer),
		Revision:       revision,
		CreatedAt:      now.UTC().Format(time.RFC3339Nano),
	}
}

func upsertPlanAuthorityDelta(state *FeatureState, delta PlanAuthorityDelta) {
	if state == nil || strings.TrimSpace(delta.QuestionID) == "" {
		return
	}

	for i := range state.AuthorityDeltas {
		if state.AuthorityDeltas[i].QuestionID == delta.QuestionID && state.AuthorityDeltas[i].Revision == delta.Revision {
			state.AuthorityDeltas[i] = delta
			return
		}
	}

	state.AuthorityDeltas = append(state.AuthorityDeltas, delta)
}

func clearAnsweredNewFileAuthorizationBlockers(state *FeatureState, delta PlanAuthorityDelta) {
	if state == nil || !deltaAuthorizesNewFile(delta) {
		return
	}

	authorized := normalizedEditScopeSet(delta.AffectedPaths)
	if len(authorized) == 0 {
		return
	}

	state.Blockers = filterStaleNewFileAuthorizationBlockers(state.Blockers, authorized)
	for i := range state.Atoms {
		state.Atoms[i].Blockers = filterStaleNewFileAuthorizationBlockers(state.Atoms[i].Blockers, authorized)
	}
}

func filterStaleNewFileAuthorizationBlockers(blockers []string, authorized map[string]struct{}) []string {
	if len(blockers) == 0 || len(authorized) == 0 {
		return blockers
	}

	out := blockers[:0]
	for _, blocker := range blockers {
		if staleNewFileAuthorizationBlockerCovered(blocker, authorized) {
			continue
		}
		out = append(out, blocker)
	}

	return out
}

func staleNewFileAuthorizationBlockerCovered(blocker string, authorized map[string]struct{}) bool {
	lower := strings.ToLower(blocker)
	if !strings.Contains(lower, "missing new-file authorization") && !strings.Contains(lower, "missing standalone new-file authorization") {
		return false
	}

	mentioned := normalizedEditScopeSet(pathparse.MentionedWorkspaceFiles(blocker))
	if len(mentioned) == 0 {
		return false
	}

	for path := range mentioned {
		if _, ok := authorized[path]; !ok {
			return false
		}
	}

	return true
}

func normalizedEditScopeSet(paths []string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, path := range normalizeEditScopeList(paths) {
		out[path] = struct{}{}
	}
	return out
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

func resumePlanExecuteRetryPending(state *FeatureState) (Event, bool) {
	if state == nil || strings.TrimSpace(state.Continuation.ResumeAction) != "retry_plan_execute" {
		return Event{}, false
	}

	resumeAtomID := strings.TrimSpace(state.Continuation.ResumeAtomID)
	if resumeAtomID == "" {
		return Event{}, false
	}

	for i := range state.Atoms {
		atom := &state.Atoms[i]
		if strings.TrimSpace(atom.ID) != resumeAtomID {
			continue
		}
		if atom.State != AtomStateImplementing {
			return Event{}, false
		}

		atomFrom := atom.State
		atom.State = AtomStateDispatchable

		return Event{
			Type:        "idiom.feature_planner.plan_execute_retry_resumed",
			SessionID:   state.SessionID,
			AtomID:      atom.ID,
			FeatureFrom: state.Status,
			FeatureTo:   state.Status,
			AtomFrom:    atomFrom,
			AtomTo:      AtomStateDispatchable,
			AtomDeltas:  []AtomDelta{{Op: "update", AtomID: atom.ID, AtomFrom: atomFrom, AtomTo: AtomStateDispatchable}},
			Message:     state.Continuation.ResumeReason,
		}, true
	}

	return Event{}, false
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
