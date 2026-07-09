package featureplanner

import (
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"idiom/internal/contracts/autonomy"
	featureplannercontract "idiom/internal/contracts/featureplanner"
	lanecontract "idiom/internal/contracts/lane"
	"idiom/internal/contracts/pathparse"
	plancontract "idiom/internal/contracts/plan"
	recoverycontract "idiom/internal/contracts/recovery"
	"idiom/internal/platform/textutil"
)

type PromptRunner func(prompt string, taskFamily string) (string, error)

type RunOptions struct {
	Prompt                string
	PromptFile            string
	MaxPlanningIterations int
	MaxAtoms              int
	ResumeAnswer          string
	ResumePlanRevision    bool
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

	if opts.ProductWorkflowBrief != nil {
		state.Plan.BehavioralCoverageDraft = nil
	}

	if strings.TrimSpace(opts.ResumeAnswer) != "" {
		if opts.ResumePlanRevision {
			events = append(events, supersedeOpenQuestionsForPlanRevision(&state, opts.ResumeAnswer)...)
		} else {
			events = append(events, answerOpenQuestion(&state, opts.ResumeAnswer)...)
			if state.Status == FeatureStatePlanningQuestion {
				return RunResult{State: state, Events: events}, nil
			}
			if replayEvents, replayed := applyLatestRejectedCandidateAfterAuthority(&state, opts); replayed {
				events = append(events, replayEvents...)
				if state.Status == FeatureStateDispatching {
					events = append(events, DispatchReadyAtoms(&state, opts.MaxAtoms, runner)...)
				}
				return RunResult{State: state, Events: events}, nil
			}
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
				Type:      featureplannercontract.EventName(featureplannercontract.EventResumeActiveRun),
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

plannerRetry:
	if state.PlanningLoop.Iteration >= state.PlanningLoop.MaxIterations {
		MarkPlanningStuck(&state, "")

		return RunResult{
			State:  state,
			Events: append(events, Event{Type: featureplannercontract.EventName(featureplannercontract.EventPlanningStuck), SessionID: state.SessionID, Message: state.PlanningLoop.StuckReason}),
		}, nil
	}

	plannerPrompt := BuildPlannerPromptWithWorkspaceContext(opts.Prompt, state, opts.ProductWorkflowBrief, opts.WorkspaceRoot)

	answer, err := runner(plannerPrompt, string(autonomy.TaskFamilyFeaturePlannerPlanner))
	if err != nil {
		state.Status = FeatureStateFailed
		state.LastError = err.Error()

		return RunResult{State: state, Events: events}, err
	}

	parsed, err := ParsePlannerOutput(answer)
	if err != nil {
		state.Status = FeatureStateMalformedOutput
		state.PlanningLoop.State = FeatureStateMalformedOutput
		state.LastError = err.Error()

		return RunResult{
			State:  state,
			Events: append(events, Event{Type: featureplannercontract.EventName(featureplannercontract.EventMalformedStructuredOutput), SessionID: state.SessionID, Message: err.Error()}),
		}, nil
	}

	if parsed.Kind == featureplannercontract.ToolSubmitFeaturePlan && opts.ProductWorkflowBrief != nil {
		PreserveSourceSatisfiedGate(&parsed.Plan, opts.Prompt)
		parsed.Plan = NormalizePlanPromptDeclarations(parsed.Plan)

		if err := ValidatePlanProductWorkflowContext(parsed.Plan, *opts.ProductWorkflowBrief, opts.WorkspaceRoot); err != nil {
			state.Status = FeatureStatePlanningRepair
			state.PlanningLoop.State = FeatureStatePlanningRepair
			state.Blockers = append(state.Blockers, err.Error())
			candidate := recordRejectedPlanCandidate(&state, parsed.Plan, featureplannercontract.EventName(featureplannercontract.EventProductContextBlocked), err.Error(), opts.Now)

			return RunResult{
				State:  state,
				Events: append(events, rejectedPlanEvent(state.SessionID, featureplannercontract.EventName(featureplannercontract.EventProductContextBlocked), err.Error(), candidate)),
			}, nil
		}

		parsed.Plan = NormalizePlanProductWorkflowContext(parsed.Plan, *opts.ProductWorkflowBrief, opts.WorkspaceRoot)
		if err := ValidateSubmittedPlanAgainstLockedContract(state.Plan, parsed.Plan, state.AuthorityDeltas); err != nil {
			state.Status = FeatureStatePlanningRepair
			state.PlanningLoop.State = FeatureStatePlanningRepair
			state.Blockers = append(state.Blockers, err.Error())
			candidate := recordRejectedPlanCandidate(&state, parsed.Plan, featureplannercontract.EventName(featureplannercontract.EventPlanContractBlocked), err.Error(), opts.Now)

			return RunResult{
				State:  state,
				Events: append(events, rejectedPlanEvent(state.SessionID, featureplannercontract.EventName(featureplannercontract.EventPlanContractBlocked), err.Error(), candidate)),
			}, nil
		}

		StampPlanAuthorityContract(&parsed.Plan, state.AuthorityDeltas)
	}

	if parsed.Kind == featureplannercontract.ToolSubmitFeaturePlan && opts.ProductWorkflowBrief == nil {
		parsed.Plan = NormalizePlanPromptDeclarations(parsed.Plan)
		PreserveSourceSatisfiedGate(&parsed.Plan, opts.Prompt)

		if err := ValidateSubmittedPlanAgainstLockedContract(state.Plan, parsed.Plan, state.AuthorityDeltas); err != nil {
			state.Status = FeatureStatePlanningRepair
			state.PlanningLoop.State = FeatureStatePlanningRepair
			state.Blockers = append(state.Blockers, err.Error())
			candidate := recordRejectedPlanCandidate(&state, parsed.Plan, featureplannercontract.EventName(featureplannercontract.EventPlanContractBlocked), err.Error(), opts.Now)

			return RunResult{
				State:  state,
				Events: append(events, rejectedPlanEvent(state.SessionID, featureplannercontract.EventName(featureplannercontract.EventPlanContractBlocked), err.Error(), candidate)),
			}, nil
		}

		contract := StampPlanAuthorityContract(&parsed.Plan, state.AuthorityDeltas)
		if err := ValidatePlanStandaloneWorkspaceContext(parsed.Plan, StandaloneWorkspaceContext{
			WorkspaceRoot:   opts.WorkspaceRoot,
			Contract:        contract,
			AuthorityDeltas: state.AuthorityDeltas,
		}); err != nil {
			state.Status = FeatureStatePlanningRepair
			state.PlanningLoop.State = FeatureStatePlanningRepair
			state.Blockers = append(state.Blockers, err.Error())
			candidate := recordRejectedPlanCandidate(&state, parsed.Plan, featureplannercontract.EventName(featureplannercontract.EventStandaloneContextBlocked), err.Error(), opts.Now)
			rejectedEvents := []Event{rejectedPlanEvent(state.SessionID, featureplannercontract.EventName(featureplannercontract.EventStandaloneContextBlocked), err.Error(), candidate)}
			if question, ok := standaloneContextRepairQuestion(err, opts.Prompt, opts.WorkspaceRoot); ok {
				rejectedEvents = append(rejectedEvents, ApplyPlannerOutput(&state, ParsedPlannerOutput{
					Kind:     featureplannercontract.ToolAskPlannerQuestion,
					Question: question,
				}, opts.Now)...)

				return RunResult{
					State:  state,
					Events: append(events, rejectedEvents...),
				}, nil
			}

			state.PlanningLoop.Iteration++
			events = append(events, rejectedEvents...)

			if standaloneValidationCanRetryPlanner(err) {
				if state.PlanningLoop.Iteration >= state.PlanningLoop.MaxIterations {
					MarkPlanningStuck(&state, "")
					events = append(events, Event{Type: featureplannercontract.EventName(featureplannercontract.EventPlanningStuck), SessionID: state.SessionID, Message: state.PlanningLoop.StuckReason})

					return RunResult{State: state, Events: events}, nil
				}

				events = append(events, Event{
					Type:      featureplannercontract.EventName(featureplannercontract.EventStandaloneContextRepairRetry),
					SessionID: state.SessionID,
					Message:   "retrying planner after model-repairable standalone context rejection",
					Fields: map[string]any{
						"iteration":      state.PlanningLoop.Iteration,
						"max_iterations": state.PlanningLoop.MaxIterations,
					},
				})
				goto plannerRetry
			}

			return RunResult{State: state, Events: events}, nil
		}
	}

	if parsed.Kind == featureplannercontract.ToolSourceSatisfied {
		if err := ValidateSourceSatisfiedWorkspaceContext(parsed.SourceSatisfied, opts.WorkspaceRoot); err != nil {
			state.Status = FeatureStatePlanningRepair
			state.PlanningLoop.State = FeatureStatePlanningRepair
			state.Blockers = append(state.Blockers, err.Error())

			return RunResult{
				State:  state,
				Events: append(events, Event{Type: featureplannercontract.EventName(featureplannercontract.EventSourceSatisfiedRejected), SessionID: state.SessionID, Message: err.Error()}),
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

func standaloneValidationCanRetryPlanner(err error) bool {
	var validationErr *StandaloneWorkspaceValidationError
	if !errors.As(err, &validationErr) || validationErr == nil || len(validationErr.Errors) != len(validationErr.Issues) {
		return false
	}

	issues := StandaloneValidationIssues(err)
	if len(issues) == 0 {
		return false
	}

	for _, issue := range issues {
		switch issue.Disposition {
		case StandaloneValidationModelRepairable, StandaloneValidationNeedsAcceptedSource:
			continue
		default:
			return false
		}
	}

	return true
}

func applyLatestRejectedCandidateAfterAuthority(state *FeatureState, opts RunOptions) ([]Event, bool) {
	if state == nil || opts.ProductWorkflowBrief != nil || len(state.RejectedPlanCandidates) == 0 {
		return nil, false
	}

	candidate := state.RejectedPlanCandidates[len(state.RejectedPlanCandidates)-1]
	if strings.TrimSpace(candidate.EventType) != featureplannercontract.EventName(featureplannercontract.EventStandaloneContextBlocked) {
		return nil, false
	}

	plan := NormalizePlanPromptDeclarations(candidate.Plan)
	PreserveSourceSatisfiedGate(&plan, opts.Prompt)

	if err := ValidateSubmittedPlanAgainstLockedContract(state.Plan, plan, state.AuthorityDeltas); err != nil {
		return nil, false
	}

	contract := StampPlanAuthorityContract(&plan, state.AuthorityDeltas)
	if err := ValidatePlanStandaloneWorkspaceContext(plan, StandaloneWorkspaceContext{
		WorkspaceRoot:   opts.WorkspaceRoot,
		Contract:        contract,
		AuthorityDeltas: state.AuthorityDeltas,
	}); err != nil {
		return nil, false
	}

	events := []Event{{
		Type:      featureplannercontract.EventName(featureplannercontract.EventRejectedCandidateRevalidated),
		SessionID: state.SessionID,
		Message:   "answered planner authority satisfied the latest rejected candidate",
		Fields: map[string]any{
			"rejected_plan_sha256": candidate.PlanHash,
			"rejected_event_type":  candidate.EventType,
		},
	}}
	events = append(events, ApplyPlannerOutput(state, ParsedPlannerOutput{Kind: featureplannercontract.ToolSubmitFeaturePlan, Plan: plan}, opts.Now)...)
	return events, true
}

func BuildPlannerPrompt(userPrompt string, state FeatureState, brief *ProductWorkflowBrief) string {
	var b strings.Builder

	b.WriteString("You are the feature planner. Produce exactly one JSON object.\n")
	b.WriteString("Valid tools: submit_feature_plan, source_satisfied, or ask_planner_question.\n")
	b.WriteString("For submit_feature_plan, return the FeaturePlan object itself with tool=\"submit_feature_plan\" at the top level. Do not nest the plan under a plan, payload, arguments, or input field.\n")
	b.WriteString("For source_satisfied, return {\"tool\":\"source_satisfied\",\"summary\":\"requested final state is already present\",\"reason\":\"short evidence-based reason\",\"evidence_paths\":[\"repo/path\"],\"test_paths\":[\"repo/test_path\"],\"anchors\":[{\"path\":\"repo/path\",\"text\":\"exact current source/test substring\"}]}. Use it only when exact current source/test anchor text is already present in the prompt context; path names alone are not proof.\n")
	b.WriteString("For ask_planner_question, return a typed question object, not a bare question field. For requirements or product-scope clarification with no exact paths yet, use {\"tool\":\"ask_planner_question\",\"question_summary\":\"...\",\"blocking_claim\":\"what blocks a safe plan\",\"requested_decision\":\"exact operator decision needed\",\"question_kind\":\"external_answer_required\",\"authority_required\":\"operator_approval\",\"required_before\":\"plan_approval\",\"affected_fields\":[\"requirements_scope\",\"verification_command\"]}; do not include affected_paths for non-path authority. If the answer would authorize missing/new files or path-level edit authority, affected_paths must contain the exact repo-relative paths and affected_fields must name new_file_authorization or the exact authorized field; paths inside requested_decision are explanatory text only and do not grant authority. For source verification, source localization, or already-implemented checks, use authority_required=\"accepted_source\" or \"atom_local_evidence\" and affected_fields=[\"source_localization\"]; do not label source-evidence or requirements questions as new_file_authorization.\n")
	b.WriteString("Every atom must name at least one concrete required_edit_paths or allowed_edit_paths entry. A no-mutation planning test still names the intended source or test paths as allowed scope; do not return both arrays empty.\n")
	b.WriteString("If the operator explicitly asks to check whether the work is already satisfied before editing, preserve that gate. Either return source_satisfied with concrete current-state evidence paths, or create an atom whose prompt starts with the same source-satisfied check and no-edit-if-satisfied instruction. Do not rewrite that request into unconditional implementation work.\n")

	if brief != nil {
		b.WriteString("\nProduct Workflow Context:\n")
		fmt.Fprintf(&b, "Effort: %s (task %s)\n", brief.EffortID, brief.TaskID)
		fmt.Fprintf(&b, "Coverage tier: %s, gate passed: %v\n", brief.CoverageTier, brief.CoverageGatePassed)
		if brief.CoverageContract != nil {
			fmt.Fprintf(&b, "Accepted coverage contract: id=%s event=%s digest=%s status=%s\n", brief.CoverageContract.ContractID, brief.CoverageContract.EventID, brief.CoverageContract.Digest, brief.CoverageContract.Status)
		}
		if brief.AcceptedCoverage != nil {
			writeAcceptedCoveragePromptContext(&b, *brief.AcceptedCoverage)
		}

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
		if len(brief.SourceFacts) > 0 {
			fmt.Fprintln(&b, "Source facts:")
			for _, fact := range brief.SourceFacts {
				var parts []string
				if strings.TrimSpace(fact.Path) != "" {
					parts = append(parts, "path="+strings.TrimSpace(fact.Path))
				}
				if strings.TrimSpace(fact.Symbol) != "" {
					parts = append(parts, "symbol="+strings.TrimSpace(fact.Symbol))
				}
				if strings.TrimSpace(fact.Signature) != "" {
					parts = append(parts, "signature="+strings.TrimSpace(fact.Signature))
				}
				if strings.TrimSpace(fact.Anchor) != "" {
					parts = append(parts, "anchor="+strings.TrimSpace(fact.Anchor))
				}
				if strings.TrimSpace(fact.AuthorityRef) != "" {
					parts = append(parts, "authority_ref="+strings.TrimSpace(fact.AuthorityRef))
				}
				if len(parts) > 0 {
					fmt.Fprintf(&b, "- %s\n", strings.Join(parts, "; "))
				}
			}
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
			if brief.CoverageContract != nil {
				b.WriteString("Set coverage_contract to the accepted coverage contract id/event/digest/status/tier from the context. Do not include behavioral_coverage_draft or accepted_coverage in product-workflow-backed submissions; the harness stamps accepted coverage detail after validation.\n")
			}

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
		b.WriteString("Standalone submit_feature_plan MUST include behavioral_coverage_draft as provisional BDD authority. It must include requirements, example rules, Given/When/Then scenarios with concrete_data, domain invariants, failure modes or other variants when relevant, surface_inventory decisions for generic behavior surfaces considered, test_obligations whose IDs match tdd_contract.obligations, and verification gates. Every TDD obligation must be mapped by at least one scenario and one test_obligation before implementation dispatch; every in-scope surface must map to concrete TDD obligations, and evidence refs may support but not replace those obligations; open_question surface decisions are non-ready and must be resolved, waived, or converted to concrete coverage before approval.\n")
		b.WriteString("Do NOT invent file paths like docs/path.md — acceptance criteria strings are valid and preferred when no docs exist.\n")
		b.WriteString("Standalone workspace grounding is enforced: required_edit_paths, allowed_edit_paths, and test artifact paths must be existing repo-relative paths unless the user explicitly authorizes new-file/full-body/create work. Generic add language only authorizes missing test-like paths when the user asks to add tests. If you cannot localize existing paths, use ask_planner_question instead of inventing paths.\n")
		b.WriteString("Do not invent project build systems or verification commands. Use an exact command only when supplied by the operator or source-localized context; otherwise ask_planner_question for the verification command. Command names mentioned in this instruction are prohibited examples, not supplied project commands: cmake, ctest, make, npm, pytest, go test.\n")
		b.WriteString("Do not encode guessed function signatures or call shapes in atom prompts. When a named helper, method, enum, or constant is required, require exact signature/source verification first; if the signature is not already verified in context, ask_planner_question or make the atom prompt block on signature mismatch rather than inventing arguments.\n")
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

	if constraints := acceptedSourceAuthorityConstraints(state.AuthorityDeltas); len(constraints) > 0 {
		b.WriteString("Accepted source authority constraints:\n")
		for _, constraint := range constraints {
			fmt.Fprintf(&b, "- %s\n", constraint)
		}
		b.WriteString("Use only these accepted source facts for helper signatures and source-localized call shapes. Remove or generalize any helper/method call in atom prompts that is not explicitly covered here, or ask_planner_question again before dispatch.\n\n")
	}

	b.WriteString("User request:\n")
	b.WriteString(strings.TrimSpace(userPrompt))
	b.WriteString("\n")

	return b.String()
}

func BuildPlannerPromptWithWorkspaceContext(userPrompt string, state FeatureState, brief *ProductWorkflowBrief, workspaceRoot string) string {
	prompt := BuildPlannerPrompt(userPrompt, state, brief)
	if brief != nil {
		return prompt
	}

	context := standaloneWorkspacePromptContext(workspaceRoot)
	if context == "" {
		return prompt
	}

	const marker = "User request:\n"
	if strings.Contains(prompt, marker) {
		return strings.Replace(prompt, marker, context+"\n"+marker, 1)
	}

	return prompt + "\n" + context
}

func standaloneWorkspacePromptContext(root string) string {
	root = strings.TrimSpace(root)
	if root == "" {
		return ""
	}

	var b strings.Builder
	b.WriteString("Standalone Workspace Context:\n")
	if standaloneWorkspaceLooksGreenfield(root) {
		b.WriteString("- Workspace appears greenfield: no existing project source/test/build files were found.\n")
		b.WriteString("- If implementation requires files, propose exact new repo-relative paths in the plan so the harness can present them for approval; do not ask the operator which existing source files should be edited.\n")
		return b.String()
	}

	files := standaloneWorkspacePromptFiles(root, 80)
	if len(files) == 0 {
		b.WriteString("- No bounded candidate source/test/build files were found in workspace context.\n")
		b.WriteString("- Ask for requirements only when product scope is ambiguous. Do not ask the operator to perform source-file discovery.\n")
		return b.String()
	}

	b.WriteString("- Existing candidate source/test/build files available for localization:\n")
	for _, file := range files {
		fmt.Fprintf(&b, "  - %s\n", file)
	}
	b.WriteString("- Localize required_edit_paths/test_artifact_paths from these candidates when they fit the request. If no candidate fits, propose exact new paths for operator approval instead of asking which existing files to edit.\n")

	return b.String()
}

func standaloneWorkspacePromptFiles(root string, limit int) []string {
	root = filepath.Clean(strings.TrimSpace(root))
	if root == "" || limit <= 0 {
		return nil
	}

	files := make([]string, 0, limit)
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			if entry != nil && entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if path == root {
			return nil
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		depth := standaloneWorkspaceEvidencePathDepth(rel)
		if entry.IsDir() {
			if standaloneWorkspaceEvidenceIgnoredDir(entry.Name()) || depth >= standaloneWorkspaceEvidenceDepthLimit {
				return filepath.SkipDir
			}
			return nil
		}
		if !standaloneWorkspaceEvidenceFile(entry.Name()) {
			return nil
		}

		files = append(files, filepath.ToSlash(rel))
		if len(files) >= limit {
			return errStopStandaloneWorkspaceEvidenceProbe
		}

		return nil
	})

	return files
}

func writeAcceptedCoveragePromptContext(b *strings.Builder, coverage AcceptedBehavioralCoverage) {
	if b == nil {
		return
	}

	for _, section := range AcceptedCoveragePresentation(&coverage) {
		fmt.Fprintf(b, "Accepted Coverage - %s:\n", section.Label)
		for _, row := range section.Rows {
			fmt.Fprintf(b, "  - %s\n", row.Text)
		}
	}
}

func acceptedSourceAuthorityConstraints(deltas []PlanAuthorityDelta) []string {
	var constraints []string

	for _, delta := range normalizePlanAuthorityDeltas(deltas) {
		if delta.AuthorityKind != "accepted_source" {
			continue
		}

		var parts []string
		for _, fact := range delta.SourceFacts {
			if description := sourceAuthorityFactDescription(fact); description != "" {
				parts = append(parts, description)
			}
		}
		if delta.Actual != "" {
			parts = append(parts, delta.Actual)
		}
		if len(delta.EvidenceRefs) > 0 {
			parts = append(parts, "evidence: "+strings.Join(delta.EvidenceRefs, ", "))
		}
		if len(delta.AffectedPaths) > 0 {
			parts = append(parts, "paths: "+strings.Join(delta.AffectedPaths, ", "))
		}

		constraint := strings.TrimSpace(strings.Join(parts, " | "))
		if constraint != "" {
			constraints = append(constraints, constraint)
		}
	}

	return normalizedStringList(constraints)
}

func sourceAuthorityFactDescription(fact plancontract.SourceAuthorityFact) string {
	var parts []string
	if fact.Path != "" {
		parts = append(parts, "path: "+fact.Path)
	}
	if fact.Symbol != "" {
		parts = append(parts, "symbol: "+fact.Symbol)
	}
	if fact.Signature != "" {
		parts = append(parts, "signature: "+fact.Signature)
	}
	if fact.Anchor != "" {
		parts = append(parts, "anchor: "+fact.Anchor)
	}

	return strings.Join(parts, " | ")
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
				Type:        featureplannercontract.EventName(featureplannercontract.EventTDDPreExecutionBlocked),
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

		contract := StampPlanAuthorityContract(&state.Plan, state.AuthorityDeltas)
		atomContract, _ := planAuthorityContractAtom(contract, atom.ID)
		atom.RuntimeEnvelope.ContractID = atomContract.Scope.ContractID
		atom.RuntimeEnvelope.ProtocolHash = contract.ContractHash

		state.ActiveRuns = append(state.ActiveRuns, ActiveRun{
			RunID:                runID,
			AtomID:               atom.ID,
			Actor:                "implementor",
			LaneSessionID:        runID,
			LaneFamily:           string(lanecontract.LanePlanExecute),
			InvocationContractID: atomContract.Scope.ContractID,
			RuntimeEnvelopeID:    contract.ContractHash,
			State:                ActiveRunStateRunning,
			StartedAt:            startedAt,
			UpdatedAt:            startedAt,
		})
		events = append(events, Event{
			Type:        featureplannercontract.EventName(featureplannercontract.EventAtomDispatched),
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
				LaneFamily:           string(lanecontract.LanePlanExecute),
				InvocationContractID: atomContract.Scope.ContractID,
				RuntimeEnvelopeID:    contract.ContractHash,
				AfterState:           ActiveRunStateRunning,
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
				Type:       featureplannercontract.EventName(featureplannercontract.EventAtomFailed),
				SessionID:  state.SessionID,
				AtomID:     atom.ID,
				AtomFrom:   atomFrom,
				AtomTo:     AtomStateFailed,
				AtomDeltas: []AtomDelta{{Op: "update", AtomID: atom.ID, AtomFrom: atomFrom, AtomTo: AtomStateFailed}},
				ActiveRunDeltas: []ActiveRunDelta{{
					Op:            "fold_terminal",
					AtomID:        atom.ID,
					LaneSessionID: runID,
					BeforeState:   ActiveRunStateRunning,
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

	if constraints := acceptedSourceAuthorityConstraintsFromContract(contract); len(constraints) > 0 {
		b.WriteString("\nAccepted source authority constraints:\n")
		for _, constraint := range constraints {
			fmt.Fprintf(&b, "- %s\n", constraint)
		}
		b.WriteString("These constraints are authoritative over the atom prose. Use only the listed accepted source facts for existing helper signatures and source-localized call shapes. Do not treat unlisted helper calls as source-verified; remove or generalize them, or ask the planner before relying on them.\n")
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
		b.WriteString("\n")
		b.WriteString(plancontract.PromptPlanExecutionContractMarker)
		b.WriteString("\n")
		b.Write(planContractBody)
		b.WriteString("\n")
	}

	if atomContract.AtomID != "" {
		b.WriteString("\n")
		b.WriteString(plancontract.PromptAtomExecutionContractMarker)
		b.WriteString("\n")
		b.Write(contractBody)
		b.WriteString("\n")
	}

	b.WriteString("\nAtom contract:\n")
	b.Write(body)
	b.WriteString("\n")

	return b.String()
}

func acceptedSourceAuthorityConstraintsFromContract(contract plancontract.PlanExecutionContract) []string {
	var constraints []string
	for _, delta := range contract.AuthorityDeltas {
		if !delta.Approved || delta.Source != "accepted_source" {
			continue
		}
		if constraint := acceptedSourceContractDeltaConstraint(delta); constraint != "" {
			constraints = append(constraints, constraint)
		}
	}
	for _, atom := range contract.Atoms {
		for _, delta := range atom.AuthorityDeltas {
			if !delta.Approved || delta.Source != "accepted_source" {
				continue
			}
			if constraint := acceptedSourceContractDeltaConstraint(delta); constraint != "" {
				constraints = append(constraints, constraint)
			}
		}
	}

	return normalizedStringList(constraints)
}

func acceptedSourceContractDeltaConstraint(delta plancontract.AuthorityDelta) string {
	var parts []string
	for _, fact := range delta.SourceFacts {
		if description := sourceAuthorityFactDescription(plancontract.SourceAuthorityFact{
			Path:      fact.Path,
			Symbol:    fact.Symbol,
			Signature: fact.Signature,
			Anchor:    fact.Anchor,
		}); description != "" {
			parts = append(parts, description)
		}
	}
	if delta.Reason != "" {
		parts = append(parts, delta.Reason)
	}
	if delta.From != "" {
		parts = append(parts, "from: "+delta.From)
	}
	if delta.To != "" {
		parts = append(parts, "to: "+delta.To)
	}
	if delta.Path != "" {
		parts = append(parts, "path: "+delta.Path)
	}

	return strings.TrimSpace(strings.Join(parts, " | "))
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
	required := plancontract.UniqueScopePaths(requiredEditPaths)
	allowed := plancontract.AllowedScopePathsExcludingRequired(allowedEditPaths, required)

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

		applyObservedAcceptanceEvidenceTrust(&envelope)

		tool := strings.TrimSpace(envelope.Tool)
		if tool == string(featureplannercontract.ToolPlanExecuteComplete) {
			completion := planExecuteCompletionFromEnvelope(envelope)

			if ok, reason := planExecuteCompletionIsSuccess(completion, *state, *atom); !ok {
				if planExecuteCompletionRetryable(completion) {
					return markPlanExecuteRetryPending(state, atom, completion, reason)
				}

				return rejectPlanExecuteCompletion(state, atom, completion, reason)
			}

			return completeAtomFromPlanExecute(state, atom, completion)
		}

		if tool != "" && tool != string(featureplannercontract.ToolQuestionForPlanner) {
			return rejectUnverifiedCompletion(state, atom, fmt.Sprintf("implementor returned unsupported completion tool %q; expected %q with typed status or %q for blockers", tool, featureplannercontract.ToolPlanExecuteComplete, featureplannercontract.ToolQuestionForPlanner))
		}

		if tool == "" {
			return rejectUnverifiedCompletion(state, atom, fmt.Sprintf("implementor returned JSON without a supported tool; expected %q with typed status or %q for blockers", featureplannercontract.ToolPlanExecuteComplete, featureplannercontract.ToolQuestionForPlanner))
		}

		var question PlannerQuestion

		if err := json.Unmarshal([]byte(trimmed), &question); err != nil {
			atom.Blockers = append(atom.Blockers, err.Error())
			state.Status = FeatureStatePlanningRepair

			return []Event{{Type: featureplannercontract.EventName(featureplannercontract.EventPlannerQuestionRejected), SessionID: state.SessionID, AtomID: atom.ID, Message: err.Error()}}
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
		return rejectUnverifiedCompletion(state, atom, fmt.Sprintf("implementor reported source-satisfied without typed %q plan-execute completion evidence", featureplannercontract.ToolPlanExecuteComplete))
	}

	if claimsUnverifiedEditOrTestEvidence(trimmed) {
		return rejectUnverifiedCompletion(state, atom, "implementor claimed edit/test evidence that was not observed by the feature-planner harness")
	}

	return rejectUnverifiedCompletion(state, atom, fmt.Sprintf("implementor returned raw text without typed %q plan-execute completion evidence", featureplannercontract.ToolPlanExecuteComplete))
}

func openImplementorPlannerQuestion(state *FeatureState, atom *FeatureAtomState, question PlannerQuestion) []Event {
	if strings.TrimSpace(question.QuestionSummary) == "" {
		question.QuestionSummary = "implementor requested planner clarification"
	}

	question = normalizePlannerQuestion(question, fmt.Sprintf("q-%03d", len(state.OpenQuestions)+1), atom.ID, time.Now().UTC())
	question = demoteUnanchoredPathAuthorityQuestion(question)
	if err := ValidatePlannerQuestion(question); err != nil {
		atom.Blockers = append(atom.Blockers, err.Error())
		state.Status = FeatureStatePlanningRepair

		return []Event{{Type: featureplannercontract.EventName(featureplannercontract.EventPlannerQuestionRejected), SessionID: state.SessionID, AtomID: atom.ID, Message: err.Error()}}
	}

	atomFrom := atom.State

	atom.State = AtomStateQuestionForPlanner
	atom.LastQuestion = question.QuestionSummary
	foldActiveRun(state, atom.ID, atom.LastRunID)

	state.OpenQuestions = append(state.OpenQuestions, question)
	state.Status = FeatureStatePlanningQuestion

	return []Event{{
		Type:       featureplannercontract.EventName(featureplannercontract.EventImplementorQuestionOpened),
		SessionID:  state.SessionID,
		AtomID:     atom.ID,
		AtomFrom:   atomFrom,
		AtomTo:     AtomStateQuestionForPlanner,
		AtomDeltas: []AtomDelta{{Op: "update", AtomID: atom.ID, AtomFrom: atomFrom, AtomTo: AtomStateQuestionForPlanner}},
		ActiveRunDeltas: []ActiveRunDelta{{
			Op:            "fold_terminal",
			AtomID:        atom.ID,
			LaneSessionID: atom.LastRunID,
			BeforeState:   ActiveRunStateRunning,
			AfterState:    AtomStateQuestionForPlanner,
		}},
		Message: question.QuestionSummary,
	}}
}

func applyObservedAcceptanceEvidenceTrust(envelope *implementorOutputEnvelope) {
	if envelope == nil {
		return
	}

	envelope.AcceptanceEvidenceTrusted = planExecuteCompletionObservedAcceptanceTrust(envelope.PlanExecuteCompletionSummary)
	if envelope.PlanExecute != nil {
		envelope.PlanExecute.AcceptanceEvidenceTrusted = planExecuteCompletionObservedAcceptanceTrust(envelope.PlanExecute.PlanExecuteCompletionSummary)
	}

	if envelope.Nested != nil {
		envelope.Nested.AcceptanceEvidenceTrusted = planExecuteCompletionObservedAcceptanceTrust(envelope.Nested.PlanExecuteCompletionSummary)
	}
}

func planExecuteCompletionObservedAcceptanceTrust(summary PlanExecuteCompletionSummary) bool {
	summary = summary.normalized()
	return len(summary.AcceptanceEvidenceIDs) > 0 &&
		summary.AcceptanceEvidenceTrusted &&
		featureplannercontract.CompletionSourceCarriesDurableAcceptanceEvidence(summary.CompletionSource) &&
		plancontract.DecidePlanExecuteTerminalCompletionFields(summary.Fields(), nil).Class == plancontract.PlanExecuteTerminalDecisionCompleted
}

type implementorOutputEnvelope struct {
	PlanExecuteCompletionSummary
	ActiveContractID     string                       `json:"active_contract_id"`
	ActiveContractHash   string                       `json:"active_contract_hash"`
	ContractSnapshotHash string                       `json:"contract_snapshot_hash"`
	PlanExecute          *nestedPlanExecuteCompletion `json:"plan_execute,omitempty"`
	Nested               *nestedPlanExecuteCompletion `json:"nested,omitempty"`
	QuestionSummary      string                       `json:"question_summary"`
	BlockingClaim        string                       `json:"blocking_claim"`
}

type nestedPlanExecuteCompletion struct {
	PlanExecuteCompletionSummary
	ActiveContractID     string `json:"active_contract_id"`
	ActiveContractHash   string `json:"active_contract_hash"`
	ContractSnapshotHash string `json:"contract_snapshot_hash"`
}

type planExecuteCompletion PlanExecuteCompletionSummary

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
	summary := nested.PlanExecuteCompletionSummary
	summary.ContractID = textutil.FirstNonEmpty(summary.ContractID, nested.ActiveContractID)
	summary.ContractHash = textutil.FirstNonEmpty(summary.ContractHash, nested.ActiveContractHash)
	summary.SnapshotHash = textutil.FirstNonEmpty(summary.SnapshotHash, nested.ContractSnapshotHash)

	merged := MergePlanExecuteCompletionSummaries(
		completion.summary(),
		summary,
	)

	*completion = planExecuteCompletionFromSummary(merged)
}

func (completion planExecuteCompletion) normalized() planExecuteCompletion {
	normalized := PlanExecuteCompletionSummary(completion).normalized()
	if len(normalized.AcceptanceEvidenceIDs) == 0 || !featureplannercontract.CompletionSourceTrustsAcceptanceEvidence(normalized.CompletionSource) {
		normalized.AcceptanceEvidenceTrusted = false
	}
	if normalized.LaneFamily == "" {
		normalized.LaneFamily = string(lanecontract.LanePlanExecute)
	}

	return planExecuteCompletion(normalized)
}

func planExecuteCompletionSummaryFromEnvelope(envelope implementorOutputEnvelope) PlanExecuteCompletionSummary {
	summary := envelope.PlanExecuteCompletionSummary
	summary.ContractID = textutil.FirstNonEmpty(summary.ContractID, envelope.ActiveContractID)
	summary.ContractHash = textutil.FirstNonEmpty(summary.ContractHash, envelope.ActiveContractHash)
	summary.SnapshotHash = textutil.FirstNonEmpty(summary.SnapshotHash, envelope.ContractSnapshotHash)

	return summary
}

func (completion planExecuteCompletion) summary() PlanExecuteCompletionSummary {
	return PlanExecuteCompletionSummary(completion)
}

func planExecuteCompletionFromSummary(summary PlanExecuteCompletionSummary) planExecuteCompletion {
	return planExecuteCompletion(summary).normalized()
}

func planExecuteCompletionIsSuccess(completion planExecuteCompletion, state FeatureState, atom FeatureAtomState) (bool, string) {
	if planExecuteCompletionSourceSatisfied(completion) {
		if reason := validateCompletionAgainstActiveContract(completion, state, atom); reason != "" {
			return false, reason
		}
	}

	decision := plancontract.DecidePlanExecuteTerminalCompletionFields(completion.summary().Fields(), nil)
	if decision.Class != plancontract.PlanExecuteTerminalDecisionCompleted {
		return false, decision.Reason
	}

	if !planExecuteCompletionSourceSatisfied(completion) {
		if reason := validateCompletionAgainstActiveContract(completion, state, atom); reason != "" {
			return false, reason
		}
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

		if failures := planExecuteTerminalValidationFailures(completion); len(failures) > 0 {
			return "nested plan-execute terminal validation failed: " + strings.Join(failures, "; ")
		}

		if reason := untrustedAcceptanceEvidenceReason(atomContract, completion, "source-satisfied completion"); reason != "" {
			return reason
		}

		if missing := missingAcceptanceEvidenceIDs(atomContract, trustedAcceptanceEvidenceIDs(completion)); len(missing) > 0 {
			return "source-satisfied completion missing acceptance_evidence_ids: " + strings.Join(missing, ", ")
		}

		return ""
	}

	if failures := planExecuteTerminalValidationFailures(completion); len(failures) > 0 {
		return "nested plan-execute terminal validation failed: " + strings.Join(failures, "; ")
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

	if reason := untrustedAcceptanceEvidenceReason(atomContract, completion, "completed plan-execute terminal completion"); reason != "" {
		return reason
	}

	if missing := missingAcceptanceEvidenceIDs(atomContract, trustedAcceptanceEvidenceIDs(completion)); len(missing) > 0 {
		return "completed plan-execute terminal completion missing acceptance_evidence_ids: " + strings.Join(missing, ", ")
	}

	if missing := missingObservedVerificationCommands(atomContract, completion); len(missing) > 0 {
		return "completed plan-execute terminal evidence missing observed verification command(s): " + strings.Join(missing, ", ")
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

func missingObservedVerificationCommands(atom plancontract.AtomExecutionContract, completion planExecuteCompletion) []string {
	observed := observedVerificationCommandSet(completion)
	var missing []string
	for _, test := range atom.RequiredTests {
		command := strings.TrimSpace(test.Command)
		if command == "" {
			continue
		}
		if _, ok := observed[plancontract.VerificationCommandEvidenceKey(command, nil, "")]; !ok {
			missing = append(missing, command)
		}
	}
	for _, obligation := range atom.TDDObligations {
		command := strings.TrimSpace(obligation.Command)
		if command == "" {
			continue
		}
		if _, ok := observed[plancontract.VerificationCommandEvidenceKey(command, nil, "")]; !ok {
			missing = append(missing, command)
		}
	}
	return normalizedStringList(missing)
}

func observedVerificationCommandSet(completion planExecuteCompletion) map[string]plancontract.VerificationCommand {
	observed := map[string]plancontract.VerificationCommand{}
	for _, command := range completion.VerificationCommands {
		key := plancontract.VerificationCommandEvidenceKey(command.Command, command.Argv, command.WorkingDir)
		if key == "" {
			continue
		}
		observed[key] = command
	}
	return observed
}

func planExecuteTerminalValidationFailures(completion planExecuteCompletion) []string {
	fields := completion.summary().Fields()
	record := plancontract.PlanExecuteTerminalCompletionRecordFromFields(fields)

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
	eventType := featureplannercontract.EventName(featureplannercontract.EventAtomCompleted)

	if planExecuteCompletionSourceSatisfied(completion) {
		atomTo = AtomStateSourceSatisfied
		eventType = featureplannercontract.EventName(featureplannercontract.EventAtomSourceSatisfied)
	}

	atom.State = atomTo
	atom.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
	foldActiveRun(state, atom.ID, atom.LastRunID)
	closure := verificationClosureFromCompletion(*state, *atom, completion, atom.CompletedAt)
	if strings.TrimSpace(closure.EventID) != "" {
		upsertVerificationClosure(state, closure)
	}

	state.Continuation.CompletedAtoms = appendUnique(state.Continuation.CompletedAtoms, atom.ID)

	featureTo := state.Status

	if allAtomsTerminal(state.Atoms) && state.Status != FeatureStatePlanningQuestion {
		state.Status = FeatureStateComplete
		featureTo = FeatureStateComplete
	}

	fields := planExecuteCompletionFields(completion)
	if closure.EventID != "" {
		fields[featureplannercontract.EventFieldVerificationClosureEventID] = closure.EventID
		fields[featureplannercontract.EventFieldVerificationStatus] = closure.Status
		fields[featureplannercontract.EventFieldPlanArtifactID] = closure.PlanArtifactID
		fields[featureplannercontract.EventFieldPlanRevision] = closure.PlanRevision
		fields[featureplannercontract.EventFieldPlanDigest] = closure.PlanDigest
		fields[featureplannercontract.EventFieldApprovalEventID] = closure.ApprovalEventID
		if len(closure.RequiredTestResults) > 0 {
			fields[featureplannercontract.EventFieldRequiredTestResults] = append([]VerificationCommandResult(nil), closure.RequiredTestResults...)
		}
		if len(closure.TDDCommandResults) > 0 {
			fields[featureplannercontract.EventFieldTDDCommandResults] = append([]VerificationCommandResult(nil), closure.TDDCommandResults...)
		}
	}

	events := []Event{{
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
			BeforeState:   ActiveRunStateRunning,
			AfterState:    atomTo,
			ResultSource:  string(lanecontract.LanePlanExecute),
			ProgressPath:  completion.ProgressPath,
		}},
		Fields: fields,
	}}

	if featureTo == FeatureStateComplete {
		state.ReleaseReadiness = EvaluateReleaseReadiness(*state, atom.CompletedAt)
		releaseEventType := featureplannercontract.EventName(featureplannercontract.EventReleaseBlocked)
		if state.ReleaseReadiness.Status == ReleaseReadinessStatusReady {
			releaseEventType = featureplannercontract.EventName(featureplannercontract.EventReleaseReady)
		}
		events = append(events, Event{
			Type:      releaseEventType,
			EventID:   state.ReleaseReadiness.EventID,
			SessionID: state.SessionID,
			FeatureTo: state.Status,
			Fields:    releaseReadinessEventFields(state.ReleaseReadiness),
		})
	}

	return events
}

func verificationClosureFromCompletion(state FeatureState, atom FeatureAtomState, completion planExecuteCompletion, createdAt string) VerificationClosure {
	status := VerificationClosureStatusPassed
	if planExecuteCompletionSourceSatisfied(completion) {
		status = VerificationClosureStatusSourceSatisfied
	}

	closure := VerificationClosure{
		EventID:                      stableVerificationClosureEventID(state, atom),
		AtomID:                       atom.ID,
		Status:                       status,
		Outcome:                      completion.Outcome,
		PlanArtifactID:               state.Plan.Artifact.ArtifactID,
		PlanRevision:                 state.Plan.Artifact.Revision,
		PlanDigest:                   state.Plan.Artifact.Digest,
		ApprovalEventID:              state.Plan.Approval.EventID,
		WorkspaceFingerprint:         state.Plan.Artifact.WorkspaceFingerprint,
		ContractID:                   completion.ContractID,
		ContractHash:                 completion.SnapshotHash,
		ScopeContractID:              completion.ScopeContractID,
		ScopeContractHash:            completion.ScopeContractHash,
		ChangedPaths:                 normalizeCompletionStringList(completion.ChangedPaths),
		SourceSatisfiedEvidencePaths: normalizeCompletionStringList(completion.SourceSatisfiedEvidencePaths),
		RequiredTestResults:          verificationCommandResultsFromCompletion(atom, completion, false, string(status)),
		TDDCommandResults:            verificationCommandResultsFromCompletion(atom, completion, true, string(status)),
		SourceSanity:                 verificationCheckResultsFromPhaseChecks(completion.SourceSanity),
		Verification:                 verificationCheckResultsFromPhaseChecks(completion.Verification),
		CreatedAt:                    strings.TrimSpace(createdAt),
	}

	if closure.ContractHash == "" {
		closure.ContractHash = completion.ContractHash
	}

	if planExecuteCompletionSourceSatisfied(completion) {
		closure.RequiredTestResults = nil
		closure.TDDCommandResults = nil
	}

	return normalizeVerificationClosure(closure)
}

func stableVerificationClosureEventID(state FeatureState, atom FeatureAtomState) string {
	planPart := plancontract.NormalizeStableContractIDPart(state.Plan.Artifact.ArtifactID)
	atomPart := plancontract.NormalizeStableContractIDPart(atom.ID)
	if planPart == "" {
		planPart = plancontract.NormalizeStableContractIDPart(state.FeatureID)
	}
	if atomPart == "" {
		atomPart = "atom"
	}

	if state.Plan.Artifact.Revision > 0 {
		return fmt.Sprintf("verification-%s-%s-r%d", planPart, atomPart, state.Plan.Artifact.Revision)
	}

	return fmt.Sprintf("verification-%s-%s", planPart, atomPart)
}

func verificationCommandResultsFromCompletion(atom FeatureAtomState, completion planExecuteCompletion, tddOnly bool, status string) []VerificationCommandResult {
	var commands []string
	var source string
	var obligationRefs []string
	var artifactPaths []string

	if tddOnly {
		commands = atom.TestFirst.Commands
		source = "featureplanner.test_first"
		obligationRefs = atom.TestFirst.ObligationRefs
		artifactPaths = append(append([]string(nil), atom.TestFirst.TestArtifactPaths...), atom.PlannedTDDSnapshot.TestArtifactPaths...)
	} else {
		commands = append([]string(nil), atom.RequiredTests...)
		source = "featureplanner.required_tests"
	}

	observed := observedVerificationCommandSet(completion)
	var out []VerificationCommandResult
	for _, command := range normalizedStringList(commands) {
		if _, ok := observed[plancontract.VerificationCommandEvidenceKey(command, nil, "")]; !ok {
			continue
		}
		out = append(out, VerificationCommandResult{
			Command:           command,
			Status:            string(status),
			Passed:            VerificationClosureStatusAllowed(VerificationClosureStatus(status)),
			Source:            source,
			ObligationRefs:    normalizeCompletionStableIDList(obligationRefs),
			TestArtifactPaths: plancontract.UniqueScopePaths(artifactPaths),
		})
	}

	return out
}

func verificationCheckResultsFromPhaseChecks(checks []plancontract.PhaseEvidenceCheck) []VerificationCheckResult {
	var out []VerificationCheckResult
	for _, check := range normalizeCompletionPhaseEvidenceChecks(checks) {
		out = append(out, VerificationCheckResult{
			Kind:       string(check.Kind),
			Path:       check.Path,
			Command:    check.Command,
			WorkingDir: strings.TrimSpace(check.WorkingDir),
			Status:     string(check.Status),
			Summary:    check.Summary,
			Error:      check.Error,
		})
	}

	return out
}

func releaseReadinessEventFields(decision ReleaseReadinessDecision) map[string]any {
	fields := map[string]any{
		featureplannercontract.EventFieldReleaseStatus: decision.Status,
	}
	if decision.PlanArtifactID != "" {
		fields[featureplannercontract.EventFieldPlanArtifactID] = decision.PlanArtifactID
	}
	if decision.PlanRevision > 0 {
		fields[featureplannercontract.EventFieldPlanRevision] = decision.PlanRevision
	}
	if decision.PlanDigest != "" {
		fields[featureplannercontract.EventFieldPlanDigest] = decision.PlanDigest
	}
	if decision.WorkspaceFingerprint != "" {
		fields[featureplannercontract.EventFieldWorkspaceFingerprint] = decision.WorkspaceFingerprint
	}
	if decision.ApprovalEventID != "" {
		fields[featureplannercontract.EventFieldApprovalEventID] = decision.ApprovalEventID
	}
	if len(decision.VerificationClosureEventIDs) > 0 {
		fields[featureplannercontract.EventFieldVerificationClosureEventIDs] = append([]string(nil), decision.VerificationClosureEventIDs...)
	}
	if len(decision.OpenObligations) > 0 {
		fields[featureplannercontract.EventFieldOpenObligationCount] = len(decision.OpenObligations)
	}
	if len(decision.Blockers) > 0 {
		fields[featureplannercontract.EventFieldBlockerCount] = len(decision.Blockers)
	}
	return fields
}

func upsertVerificationClosure(state *FeatureState, closure VerificationClosure) {
	closure = normalizeVerificationClosure(closure)
	if closure.EventID == "" {
		return
	}

	for i := range state.VerificationClosures {
		if strings.TrimSpace(state.VerificationClosures[i].EventID) == closure.EventID {
			state.VerificationClosures[i] = closure
			return
		}
	}

	state.VerificationClosures = append(state.VerificationClosures, closure)
}

func planExecuteCompletionSourceSatisfied(completion planExecuteCompletion) bool {
	return textutil.EqualTrimFold(completion.Outcome, string(plancontract.PlanExecuteTerminalOutcomeSourceSatisfied)) ||
		textutil.EqualTrimFold(completion.Status, string(plancontract.PlanExecuteTerminalStatusSourceSatisfied))
}

func rejectPlanExecuteCompletion(state *FeatureState, atom *FeatureAtomState, completion planExecuteCompletion, reason string) []Event {
	fields := planExecuteCompletionFields(completion)
	if detail := planExecuteCompletionFailureDetail(completion); detail != "" {
		reason = strings.TrimSpace(reason + ": " + detail)
	}

	return recoverAtom(state, atom, reason, "repair_nested_plan_execute_completion", featureplannercontract.EventName(featureplannercontract.EventPlanExecuteCompletionRejected), fields)
}

func planExecuteCompletionRetryable(completion planExecuteCompletion) bool {
	return strings.TrimSpace(completion.NextAction) == string(recoverycontract.NextActionRetryPlanExecute)
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
	state.Continuation.ResumeAction = string(recoverycontract.NextActionRetryPlanExecute)
	state.Continuation.ResumeReason = reason
	state.Continuation.BlockedAtoms = removeFeaturePlannerString(state.Continuation.BlockedAtoms, atom.ID)
	state.Blockers = removeFeaturePlannerString(state.Blockers, reason)
	atom.Blockers = removeFeaturePlannerString(atom.Blockers, reason)
	foldActiveRun(state, atom.ID, atom.LastRunID)

	return []Event{{
		Type:        featureplannercontract.EventName(featureplannercontract.EventPlanExecuteRetryPending),
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
			BeforeState:   ActiveRunStateRunning,
			AfterState:    string(recoverycontract.NextActionRetryPlanExecute),
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
	return recoverAtom(state, atom, reason, "repair_unverified_completion", featureplannercontract.EventName(featureplannercontract.EventUnverifiedCompletionRejected), nil)
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

	if packetPath := CompletionFieldString(fields, plancontract.PlanExecuteTerminalFieldPacketPath); packetPath != "" {
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
			BeforeState:   ActiveRunStateRunning,
			AfterState:    AtomStateRecoveryOpen,
		}},
		Message: reason,
		Fields:  fields,
	}}
}

func planExecuteCompletionFields(completion planExecuteCompletion) map[string]any {
	fields := map[string]any{
		featureplannercontract.PlanExecuteCompletionFieldLaneFamily:   completion.LaneFamily,
		featureplannercontract.PlanExecuteCompletionFieldNestedStatus: completion.Status,
	}
	if completion.Phase != "" {
		fields[featureplannercontract.PlanExecuteCompletionFieldNestedPhase] = completion.Phase
	}

	if completion.ReviewStatus != "" {
		fields[featureplannercontract.PlanExecuteCompletionFieldNestedReviewStatus] = completion.ReviewStatus
	}

	if completion.Outcome != "" {
		fields[plancontract.PlanExecuteTerminalFieldOutcome] = completion.Outcome
	}

	if completion.TerminalClass != "" {
		fields[plancontract.PlanExecuteTerminalFieldTerminalClass] = completion.TerminalClass
	}

	if completion.FailureKind != "" {
		fields[featureplannercontract.PlanExecuteCompletionFieldNestedFailureKind] = completion.FailureKind
	}

	if completion.FailureReason != "" {
		fields[featureplannercontract.PlanExecuteCompletionFieldNestedFailureReason] = completion.FailureReason
	}

	if completion.FailureID != "" {
		fields[plancontract.PlanExecuteTerminalFieldFailureID] = completion.FailureID
	}

	if completion.RecordPath != "" {
		fields[plancontract.PlanExecuteTerminalFieldRecordPath] = completion.RecordPath
	}

	if completion.PacketPath != "" {
		fields[plancontract.PlanExecuteTerminalFieldPacketPath] = completion.PacketPath
	}

	if completion.NextAction != "" {
		fields[plancontract.PlanExecuteTerminalFieldNextAction] = completion.NextAction
	}

	if completion.ProgressPath != "" {
		fields[plancontract.PlanExecuteTerminalFieldProgressPath] = completion.ProgressPath
	}

	if completion.Summary != "" {
		fields[featureplannercontract.PlanExecuteCompletionFieldSummary] = completion.Summary
	}

	if completion.CompletionSource != "" {
		fields[featureplannercontract.PlanExecuteCompletionFieldCompletionSource] = completion.CompletionSource
	}

	if completion.ReviewRepairSkipped {
		fields[plancontract.PlanExecuteTerminalFieldReviewRepairSkipped] = true
	}

	addPlanExecuteCompletionEvidenceFields(fields, completion)

	return fields
}

func addPlanExecuteCompletionEvidenceFields(fields map[string]any, completion planExecuteCompletion) {
	if completion.ContractID != "" {
		fields[plancontract.PlanExecuteTerminalFieldContractID] = completion.ContractID
	}

	if completion.SnapshotHash != "" {
		fields[plancontract.PlanExecuteTerminalFieldSnapshotHash] = completion.SnapshotHash
	}

	if completion.ScopeContractID != "" {
		fields[plancontract.PlanExecuteTerminalFieldScopeContractID] = completion.ScopeContractID
	}

	if completion.ScopeContractHash != "" {
		fields[plancontract.PlanExecuteTerminalFieldScopeContractHash] = completion.ScopeContractHash
	}

	if len(completion.RequiredEditPaths) > 0 {
		fields[plancontract.PlanExecuteTerminalFieldRequiredEditPaths] = append([]string(nil), completion.RequiredEditPaths...)
	}

	if len(completion.AllowedEditPaths) > 0 {
		fields[plancontract.PlanExecuteTerminalFieldAllowedEditPaths] = append([]string(nil), completion.AllowedEditPaths...)
	}

	if len(completion.SourceSatisfiedEvidencePaths) > 0 {
		fields[plancontract.PlanExecuteTerminalFieldSourceSatisfiedEvidencePaths] = append([]string(nil), completion.SourceSatisfiedEvidencePaths...)
	}

	if len(completion.SourceSatisfiedEvidence) > 0 {
		fields[plancontract.PlanExecuteTerminalFieldSourceSatisfiedEvidence] = append([]plancontract.SourceSatisfiedEvidenceRef(nil), completion.SourceSatisfiedEvidence...)
	}

	if len(completion.ChangedPaths) > 0 {
		fields[plancontract.PlanExecuteTerminalFieldChangedPaths] = append([]string(nil), completion.ChangedPaths...)
	}

	if len(completion.SourceSanity) > 0 {
		fields[plancontract.PlanExecuteTerminalFieldSourceSanity] = append([]plancontract.PhaseEvidenceCheck(nil), completion.SourceSanity...)
	}

	if len(completion.Verification) > 0 {
		fields[plancontract.PlanExecuteTerminalFieldVerification] = append([]plancontract.PhaseEvidenceCheck(nil), completion.Verification...)
	}

	if len(completion.VerificationCommands) > 0 {
		fields[plancontract.PlanExecuteTerminalFieldVerificationCommands] = append([]plancontract.VerificationCommand(nil), completion.VerificationCommands...)
	}

	if len(completion.VerificationSkips) > 0 {
		fields[plancontract.PlanExecuteTerminalFieldVerificationSkips] = append([]plancontract.VerificationSkip(nil), completion.VerificationSkips...)
	}

	if len(completion.VerificationRejects) > 0 {
		fields[plancontract.PlanExecuteTerminalFieldVerificationRejects] = append([]plancontract.VerificationReject(nil), completion.VerificationRejects...)
	}

	if len(completion.AcceptanceEvidenceIDs) > 0 {
		fields[plancontract.PlanExecuteTerminalFieldAcceptanceEvidenceIDs] = append([]string(nil), completion.AcceptanceEvidenceIDs...)
	}

	if completion.AcceptanceEvidenceTrusted {
		fields[featureplannercontract.PlanExecuteCompletionFieldAcceptanceEvidenceTrusted] = true
	}
}

func missingSourceSatisfiedEvidenceRefs(paths []string, refs []plancontract.SourceSatisfiedEvidenceRef) []string {
	covered := map[string]bool{}

	for _, ref := range plancontract.NormalizeSourceSatisfiedEvidenceRefs(refs) {
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
		if PlannerQuestionOpen(state.OpenQuestions[i]) {
			delta, err := applyQuestionAnswerAuthority(&state.OpenQuestions[i], answer, len(state.AuthorityDeltas)+1, time.Now().UTC())
			if err != nil {
				state.Status = FeatureStatePlanningQuestion
				state.PlanningLoop.State = FeatureStatePlanningQuestion
				state.Blockers = append(state.Blockers, err.Error())

				return []Event{{Type: featureplannercontract.EventName(featureplannercontract.EventPlannerQuestionAnswerRejected), SessionID: state.SessionID, Message: err.Error()}}
			}

			upsertPlanAuthorityDelta(state, delta)
			clearAnsweredPlannerQuestionBlockers(state, delta.QuestionID)
			clearAnsweredNewFileAuthorizationBlockers(state, delta)
			clearAnsweredAcceptedSourceBlockers(state, delta)

			state.OpenQuestions[i].Status = featureplannercontract.PlannerQuestionStatusAnswered
			state.Status = FeatureStatePlanning
			state.PlanningLoop.State = FeatureStatePlanning

			return []Event{{Type: featureplannercontract.EventName(featureplannercontract.EventPlannerQuestionAnswered), SessionID: state.SessionID, Message: state.OpenQuestions[i].QuestionSummary, Fields: map[string]any{featureplannercontract.EventFieldAuthorityDelta: delta}}}
		}
	}

	return nil
}

func supersedeOpenQuestionsForPlanRevision(state *FeatureState, revision string) []Event {
	if state == nil {
		return nil
	}
	revision = strings.TrimSpace(revision)
	if revision == "" {
		return nil
	}

	var events []Event
	for i := range state.OpenQuestions {
		if !PlannerQuestionOpen(state.OpenQuestions[i]) {
			continue
		}
		state.OpenQuestions[i].Status = featureplannercontract.PlannerQuestionStatusAnswered
		state.OpenQuestions[i].Answer = revision
		state.OpenQuestions[i].AnswerSourceKind = featureplannercontract.PlannerQuestionAnswerSourcePlanRevision
		state.OpenQuestions[i].EvidenceRefs = normalizedStringList(append(state.OpenQuestions[i].EvidenceRefs, featureplannercontract.PlannerQuestionEvidenceOperatorPlanRevision))
		clearAnsweredPlannerQuestionBlockers(state, state.OpenQuestions[i].ID)
		events = append(events, Event{
			Type:      featureplannercontract.EventName(featureplannercontract.EventPlannerQuestionSupersededByRevision),
			SessionID: state.SessionID,
			Message:   state.OpenQuestions[i].QuestionSummary,
			Fields: map[string]any{
				featureplannercontract.EventFieldQuestionID: state.OpenQuestions[i].ID,
			},
		})
	}
	if len(events) > 0 {
		state.Status = FeatureStatePlanning
		state.PlanningLoop.State = FeatureStatePlanning
	}

	return events
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
		Answer           string                             `json:"answer"`
		AnswerSourceKind string                             `json:"answer_source_kind"`
		AuthorityKind    string                             `json:"authority_kind"`
		EvidenceRefs     []string                           `json:"evidence_refs"`
		AffectedPaths    []string                           `json:"affected_paths"`
		AffectedFields   []string                           `json:"affected_fields"`
		SourceFacts      []plancontract.SourceAuthorityFact `json:"source_facts"`
		Expected         string                             `json:"expected"`
		Actual           string                             `json:"actual"`
	}

	if err := json.Unmarshal([]byte(answer), &payload); err != nil {
		return PlanAuthorityDelta{}, fmt.Errorf("planner question %q requires typed %s authority answer", question.ID, required)
	}

	if strings.TrimSpace(payload.AnswerSourceKind) != required {
		return PlanAuthorityDelta{}, fmt.Errorf("planner question %q requires answer_source_kind %q", question.ID, required)
	}
	if authorityKind := strings.TrimSpace(payload.AuthorityKind); authorityKind != "" && authorityKind != required {
		return PlanAuthorityDelta{}, fmt.Errorf("planner question %q requires authority_kind %q when provided", question.ID, required)
	}

	if strings.TrimSpace(payload.Answer) == "" {
		return PlanAuthorityDelta{}, fmt.Errorf("planner question %q typed answer missing answer", question.ID)
	}

	if len(payload.EvidenceRefs) == 0 {
		return PlanAuthorityDelta{}, fmt.Errorf("planner question %q typed answer missing evidence_refs", question.ID)
	}
	if required == "accepted_source" && len(normalizeSourceAuthorityFacts(payload.SourceFacts)) == 0 {
		return PlanAuthorityDelta{}, fmt.Errorf("planner question %q typed accepted_source answer missing source_facts", question.ID)
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

	delta.SourceFacts = normalizeSourceAuthorityFacts(append(delta.SourceFacts, payload.SourceFacts...))
	if delta.AuthorityKind == "accepted_source" {
		delta.SourceFacts = deriveAcceptedSourceAuthorityFacts(delta)
	}

	return delta, nil
}

func standaloneContextRepairQuestion(err error, prompt string, workspaceRoot string) (PlannerQuestion, bool) {
	issues := StandaloneValidationIssues(err)

	if standaloneValidationHasIssueKind(issues, "missing_new_file_authorization") {
		for _, builder := range []func() (PlannerQuestion, bool){
			func() (PlannerQuestion, bool) { return missingNewFileAuthorizationQuestion(err, prompt) },
			func() (PlannerQuestion, bool) { return missingPathLocalizationQuestion(err, prompt) },
			func() (PlannerQuestion, bool) {
				return greenfieldMissingNewFileAuthorizationQuestion(err, prompt, workspaceRoot)
			},
			func() (PlannerQuestion, bool) {
				return standaloneProjectMissingNewFileAuthorizationQuestion(err, prompt)
			},
		} {
			if question, ok := builder(); ok {
				return question, true
			}
		}
		return PlannerQuestion{}, false
	}

	if question, ok := acceptedSourceRepairQuestion(issues, prompt); ok {
		return question, true
	}

	if question, ok := verificationCommandRepairQuestion(issues, prompt); ok {
		return question, true
	}

	if question, ok := sourceSignatureVerificationQuestion(err, prompt); ok {
		return question, true
	}

	return PlannerQuestion{}, false
}

func standaloneValidationHasIssueKind(issues []StandaloneValidationIssue, kind string) bool {
	for _, issue := range issues {
		if strings.TrimSpace(issue.Kind) == kind {
			return true
		}
	}
	return false
}

func acceptedSourceRepairQuestion(issues []StandaloneValidationIssue, prompt string) (PlannerQuestion, bool) {
	if !promptRequestsSourceLocalizationQuestion(prompt) {
		return PlannerQuestion{}, false
	}

	var paths []string
	for _, issue := range issues {
		if issue.Disposition != StandaloneValidationNeedsAcceptedSource {
			continue
		}
		paths = append(paths, issue.AffectedPaths...)
	}
	paths = plancontract.UniqueScopePaths(paths)
	if len(paths) == 0 {
		return PlannerQuestion{}, false
	}

	return PlannerQuestion{
		QuestionSummary:   "Provide accepted source facts for helper signatures",
		QuestionKind:      "external_answer_required",
		BlockingClaim:     "The submitted standalone feature plan encoded concrete helper calls without accepted source signature evidence.",
		RequestedDecision: "Provide accepted source facts with exact current helper signatures and source anchors for the intended atom, or explicitly say the signatures are not source-verified and the atom must be rebased before dispatch.",
		AuthorityRequired: "accepted_source",
		RequiredBefore:    "plan_approval",
		AffectedPaths:     paths,
		AffectedFields:    []string{"source_localization"},
		UserFacing:        true,
	}, true
}

func verificationCommandRepairQuestion(issues []StandaloneValidationIssue, prompt string) (PlannerQuestion, bool) {
	var commands []string
	var paths []string
	for _, issue := range issues {
		if issue.Kind != "verification_command_unverified" || issue.Disposition != StandaloneValidationNeedsOperatorApproval {
			continue
		}
		commands = append(commands, issue.Commands...)
		paths = append(paths, issue.AffectedPaths...)
	}
	commands = normalizedStringList(commands)
	if len(commands) == 0 {
		return PlannerQuestion{}, false
	}

	request := strings.TrimSpace(prompt)
	if request == "" {
		request = "the requested standalone feature"
	}

	return PlannerQuestion{
		QuestionSummary:   "Confirm standalone verification command",
		QuestionKind:      "external_answer_required",
		BlockingClaim:     "The submitted standalone feature plan names verification commands that are not supported by project evidence in the workspace.",
		RequestedDecision: fmt.Sprintf("For %q, provide the exact verification command to use for this project, or revise the project setup before planning continues. Rejected command candidate(s): %s", request, strings.Join(commands, ", ")),
		AuthorityRequired: "operator_approval",
		RequiredBefore:    "plan_approval",
		AffectedPaths:     plancontract.UniqueScopePaths(paths),
		AffectedFields:    []string{"verification_command"},
	}, true
}

func missingNewFileAuthorizationQuestion(err error, prompt string) (PlannerQuestion, bool) {
	if !promptRequestsNewFileAuthorizationQuestion(prompt) {
		return PlannerQuestion{}, false
	}

	return missingNewFileAuthorizationPlannerQuestion(err, prompt, missingNewFileAuthorizationQuestionOptions{
		QuestionSummary:  "Authorize new files for feature plan",
		BlockingClaim:    "The submitted feature plan includes missing workspace paths that require explicit new-file authorization before dispatch.",
		DecisionTemplate: "Authorize creation of exactly these missing repo-relative paths: %s",
	})
}

func greenfieldMissingNewFileAuthorizationQuestion(err error, prompt string, workspaceRoot string) (PlannerQuestion, bool) {
	if !standaloneWorkspaceLooksGreenfield(workspaceRoot) || !promptRequestsGreenfieldProjectScope(prompt) {
		return PlannerQuestion{}, false
	}

	return missingNewFileAuthorizationPlannerQuestion(err, prompt, missingNewFileAuthorizationQuestionOptions{
		QuestionSummary:  "Approve or revise greenfield project scope",
		BlockingClaim:    "The workspace has no project files, so the standalone feature planner cannot infer new files, project structure, or verification commands safely.",
		DecisionTemplate: "For %q, approve creation of exactly these proposed repo-relative paths and the intended project setup, or reject/revise the paths before planning continues: %s",
		IncludeRequest:   true,
	})
}

func standaloneProjectMissingNewFileAuthorizationQuestion(err error, prompt string) (PlannerQuestion, bool) {
	if !promptRequestsGreenfieldProjectScope(prompt) {
		return PlannerQuestion{}, false
	}

	return missingNewFileAuthorizationPlannerQuestion(err, prompt, missingNewFileAuthorizationQuestionOptions{
		QuestionSummary:  "Authorize proposed new files for feature plan",
		BlockingClaim:    "The submitted standalone feature plan proposes missing workspace paths that require explicit operator approval before dispatch.",
		DecisionTemplate: "For %q, approve creation of exactly these proposed repo-relative paths and the intended project setup, or reject/revise the paths before planning continues: %s",
		IncludeRequest:   true,
	})
}

type missingNewFileAuthorizationQuestionOptions struct {
	QuestionSummary  string
	BlockingClaim    string
	DecisionTemplate string
	IncludeRequest   bool
	IncludeVerify    bool
}

func missingNewFileAuthorizationPlannerQuestion(err error, prompt string, options missingNewFileAuthorizationQuestionOptions) (PlannerQuestion, bool) {
	var missing *MissingNewFileAuthorizationError
	if !errors.As(err, &missing) {
		return PlannerQuestion{}, false
	}

	paths := plancontract.UniqueScopePaths(missing.Paths)
	if len(paths) == 0 {
		return PlannerQuestion{}, false
	}

	request := strings.TrimSpace(prompt)
	if request == "" {
		request = "the requested standalone feature"
	}

	decisionTemplate := strings.TrimSpace(options.DecisionTemplate)
	if decisionTemplate == "" {
		decisionTemplate = "Authorize creation of exactly these missing repo-relative paths: %s"
	}

	var requestedDecision string
	if options.IncludeRequest {
		requestedDecision = fmt.Sprintf(decisionTemplate, request, strings.Join(paths, ", "))
	} else {
		requestedDecision = fmt.Sprintf(decisionTemplate, strings.Join(paths, ", "))
	}

	affectedFields := []string{"new_file_authorization"}
	if options.IncludeVerify {
		affectedFields = append(affectedFields, "verification_command")
	}

	return PlannerQuestion{
		QuestionSummary:   strings.TrimSpace(options.QuestionSummary),
		QuestionKind:      "external_answer_required",
		BlockingClaim:     strings.TrimSpace(options.BlockingClaim),
		RequestedDecision: requestedDecision,
		AuthorityRequired: "operator_approval",
		RequiredBefore:    "plan_approval",
		AffectedPaths:     paths,
		AffectedFields:    affectedFields,
	}, true
}

func promptRequestsGreenfieldProjectScope(prompt string) bool {
	words := " " + strings.ToLower(strings.TrimSpace(prompt)) + " "
	words = strings.NewReplacer(
		".", " ",
		",", " ",
		";", " ",
		":", " ",
		"!", " ",
		"?", " ",
		"(", " ",
		")", " ",
		"[", " ",
		"]", " ",
		"{", " ",
		"}", " ",
		"`", " ",
		"\"", " ",
		"'", " ",
	).Replace(words)
	if strings.TrimSpace(words) == "" {
		return false
	}

	greenfieldIntent := false
	projectLike := strings.Contains(words, " project ") ||
		strings.Contains(words, " app ") ||
		strings.Contains(words, " application ") ||
		strings.Contains(words, " repo ") ||
		strings.Contains(words, " repository ") ||
		strings.Contains(words, " workspace ")
	for _, term := range []string{
		" bones ",
		" skeleton ",
		" scaffold ",
		" scaffolding ",
		" structure ",
		" starter ",
		" greenfield ",
		" from scratch ",
		" starting from scratch ",
		" mvp ",
		" minimum viable ",
	} {
		if strings.Contains(words, term) {
			greenfieldIntent = true
			break
		}
	}

	return projectLike && greenfieldIntent
}

const (
	standaloneWorkspaceEvidenceFileLimit  = 2000
	standaloneWorkspaceEvidenceDepthLimit = 6
)

var errStopStandaloneWorkspaceEvidenceProbe = errors.New("stop standalone workspace evidence probe")

func standaloneWorkspaceLooksGreenfield(root string) bool {
	root = strings.TrimSpace(root)
	if root == "" {
		return false
	}

	root = filepath.Clean(root)
	found := false
	inspected := 0
	scanLimitExceeded := false

	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			if path == root {
				return walkErr
			}
			if entry != nil && entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if path == root {
			return nil
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		depth := standaloneWorkspaceEvidencePathDepth(rel)
		if entry.IsDir() {
			if standaloneWorkspaceEvidenceIgnoredDir(entry.Name()) || depth >= standaloneWorkspaceEvidenceDepthLimit {
				return filepath.SkipDir
			}
			return nil
		}

		inspected++
		if inspected > standaloneWorkspaceEvidenceFileLimit {
			scanLimitExceeded = true
			return errStopStandaloneWorkspaceEvidenceProbe
		}

		if standaloneWorkspaceEvidenceFile(entry.Name()) {
			found = true
			return errStopStandaloneWorkspaceEvidenceProbe
		}

		return nil
	})

	if found || scanLimitExceeded {
		return false
	}

	return err == nil || err == errStopStandaloneWorkspaceEvidenceProbe
}

func standaloneWorkspaceEvidencePathDepth(rel string) int {
	rel = strings.Trim(filepath.ToSlash(rel), "/")
	if rel == "" || rel == "." {
		return 0
	}

	return strings.Count(rel, "/") + 1
}

func standaloneWorkspaceEvidenceIgnoredDir(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case ".cache", ".codex", ".direnv", ".git", ".idiom", ".planning", "build", "dist", "logs", "node_modules", "target", "transcripts", "vendor":
		return true
	default:
		return false
	}
}

func standaloneWorkspaceEvidenceFile(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	switch name {
	case ".dockerignore", ".editorconfig", ".env.example", ".gitignore", "agents.md", "build.gradle", "build.gradle.kts", "cargo.lock", "cargo.toml", "dockerfile", "go.mod", "go.sum", "makefile", "package-lock.json", "package.json", "pom.xml", "pyproject.toml", "readme", "readme.md", "requirements.txt":
		return true
	}

	switch filepath.Ext(name) {
	case ".adoc", ".bash", ".c", ".cc", ".cpp", ".css", ".gd", ".go", ".graphql", ".h", ".hpp", ".html", ".java", ".js", ".json", ".jsx", ".kt", ".kts", ".lock", ".md", ".mdx", ".mod", ".proto", ".py", ".rs", ".rst", ".sh", ".sql", ".sum", ".toml", ".ts", ".tsx", ".txt", ".xml", ".yaml", ".yml", ".zsh":
		return true
	default:
		return false
	}
}

func missingPathLocalizationQuestion(err error, prompt string) (PlannerQuestion, bool) {
	var missing *MissingNewFileAuthorizationError
	if !errors.As(err, &missing) {
		return PlannerQuestion{}, false
	}

	if !promptRequestsSourceLocalizationQuestion(prompt) {
		return PlannerQuestion{}, false
	}

	paths := plancontract.UniqueScopePaths(missing.Paths)
	if len(paths) == 0 {
		return PlannerQuestion{}, false
	}

	return PlannerQuestion{
		QuestionSummary:   "Provide source-localized atom scope",
		QuestionKind:      "external_answer_required",
		BlockingClaim:     "The submitted standalone feature plan used missing workspace paths without concrete source localization; approving those paths would grant authority to likely invented files.",
		RequestedDecision: fmt.Sprintf("Provide one current, source-verified atom candidate with exact existing repo-relative source/test paths, or explicitly say there is no suitable unsatisfied atom. Rejected ungrounded paths: %s", strings.Join(paths, ", ")),
		AuthorityRequired: "accepted_source",
		RequiredBefore:    "plan_approval",
		AffectedPaths:     paths,
		AffectedFields:    []string{"source_localization"},
		UserFacing:        true,
	}, true
}

func sourceSignatureVerificationQuestion(err error, prompt string) (PlannerQuestion, bool) {
	errText := ""
	if err != nil {
		errText = strings.TrimSpace(err.Error())
	}
	if !strings.Contains(errText, "without source-verified signature evidence") {
		return PlannerQuestion{}, false
	}

	if !promptRequestsSourceLocalizationQuestion(prompt) {
		return PlannerQuestion{}, false
	}

	paths := plancontract.UniqueScopePaths(append(repoPathsMentionedInText(prompt), repoPathsMentionedInText(errText)...))

	return PlannerQuestion{
		QuestionSummary:   "Provide accepted source facts for helper signatures",
		QuestionKind:      "external_answer_required",
		BlockingClaim:     "The submitted standalone feature plan encoded concrete helper calls without accepted source signature evidence.",
		RequestedDecision: "Provide accepted source facts with exact current helper signatures and source anchors for the intended atom, or explicitly say the signatures are not source-verified and the atom must be rebased before dispatch.",
		AuthorityRequired: "accepted_source",
		RequiredBefore:    "plan_approval",
		AffectedPaths:     paths,
		AffectedFields:    []string{"source_localization"},
		UserFacing:        true,
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

func promptRequestsSourceLocalizationQuestion(prompt string) bool {
	lower := strings.ToLower(strings.TrimSpace(prompt))
	if lower == "" {
		return false
	}

	localizationTerms := []string{
		"prepared next-atom",
		"next-atom candidates",
		"candidate list",
		"candidate scope",
		"source-localized",
		"source localized",
		"source-backed",
		"source localization",
		"source-localization",
		"source verification",
		"source-verified",
		"accepted source",
		"verify current source",
	}
	for _, term := range localizationTerms {
		if strings.Contains(lower, term) {
			return true
		}
	}

	return strings.Contains(lower, "atom") &&
		(strings.Contains(lower, "pick") || strings.Contains(lower, "choose") || strings.Contains(lower, "select")) &&
		(strings.Contains(lower, "verify") || strings.Contains(lower, "source") || strings.Contains(lower, "prepared"))
}

func planAuthorityDeltaFromQuestion(question PlannerQuestion, revision int, now time.Time) PlanAuthorityDelta {
	if now.IsZero() {
		now = time.Now().UTC()
	}

	evidenceRefs := normalizedStringList(question.EvidenceRefs)
	if len(evidenceRefs) == 0 {
		evidenceRefs = []string{"planner_question:" + textutil.FirstNonEmpty(question.QuestionID, question.ID)}
	}

	delta := PlanAuthorityDelta{
		QuestionID:     textutil.FirstNonEmpty(question.QuestionID, question.ID),
		AuthorityKind:  textutil.FirstNonEmpty(question.AnswerSourceKind, question.AuthorityRequired, "operator_approval"),
		EvidenceRefs:   evidenceRefs,
		AffectedPaths:  plancontract.UniqueScopePaths(question.AffectedPaths),
		AffectedFields: normalizedStringList(question.AffectedFields),
		Expected:       strings.TrimSpace(question.BlockingClaim),
		Actual:         strings.TrimSpace(question.Answer),
		Revision:       revision,
		CreatedAt:      now.UTC().Format(time.RFC3339Nano),
	}

	if delta.AuthorityKind == "accepted_source" {
		delta.SourceFacts = deriveAcceptedSourceAuthorityFacts(delta)
	}

	return delta
}

func deriveAcceptedSourceAuthorityFacts(delta PlanAuthorityDelta) []plancontract.SourceAuthorityFact {
	facts := append([]plancontract.SourceAuthorityFact(nil), delta.SourceFacts...)
	if strings.TrimSpace(delta.AuthorityKind) != "accepted_source" {
		return normalizeSourceAuthorityFacts(facts)
	}

	paths := plancontract.UniqueScopePaths(append(append([]string(nil), delta.AffectedPaths...), delta.EvidenceRefs...))
	paths = plancontract.UniqueScopePaths(append(paths, repoPathsMentionedInText(delta.Actual)...))

	for _, signature := range extractSignatures(delta.Actual) {
		symbol := signatureSymbol(signature)
		if len(paths) == 0 {
			facts = append(facts, plancontract.SourceAuthorityFact{Symbol: symbol, Signature: signature})
			continue
		}
		for _, path := range paths {
			facts = append(facts, plancontract.SourceAuthorityFact{Path: path, Symbol: symbol, Signature: signature})
		}
	}

	for _, path := range paths {
		facts = append(facts, plancontract.SourceAuthorityFact{Path: path})
	}

	return normalizeSourceAuthorityFacts(facts)
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

func clearAnsweredPlannerQuestionBlockers(state *FeatureState, questionID string) {
	if state == nil || strings.TrimSpace(questionID) == "" {
		return
	}

	state.Blockers = filterAnsweredPlannerQuestionBlockers(state.Blockers, questionID)
	for i := range state.Atoms {
		state.Atoms[i].Blockers = filterAnsweredPlannerQuestionBlockers(state.Atoms[i].Blockers, questionID)
	}
}

func filterAnsweredPlannerQuestionBlockers(blockers []string, questionID string) []string {
	if len(blockers) == 0 {
		return blockers
	}

	out := blockers[:0]
	needle := `planner question "` + questionID + `"`
	for _, blocker := range blockers {
		lower := strings.ToLower(blocker)
		if strings.Contains(blocker, needle) &&
			strings.Contains(lower, "requires") &&
			strings.Contains(lower, "answer") {
			continue
		}
		out = append(out, blocker)
	}
	return out
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

func clearAnsweredAcceptedSourceBlockers(state *FeatureState, delta PlanAuthorityDelta) {
	if state == nil || delta.AuthorityKind != "accepted_source" || len(normalizeSourceAuthorityFacts(delta.SourceFacts)) == 0 {
		return
	}

	state.Blockers = filterAcceptedSourceSignatureBlockers(state.Blockers, delta)
	for i := range state.Atoms {
		state.Atoms[i].Blockers = filterAcceptedSourceSignatureBlockers(state.Atoms[i].Blockers, delta)
	}
}

func filterAcceptedSourceSignatureBlockers(blockers []string, delta PlanAuthorityDelta) []string {
	if len(blockers) == 0 {
		return blockers
	}

	grounding := atomPromptGrounding{
		declaredPaths:      map[string]bool{},
		verifiedSignatures: map[string][]string{},
	}
	for _, fact := range normalizeSourceAuthorityFacts(delta.SourceFacts) {
		grounding.addContractSourceFact(fact)
	}

	out := blockers[:0]
	for _, blocker := range blockers {
		if acceptedSourceSignatureBlockerCovered(blocker, grounding) {
			continue
		}
		out = append(out, blocker)
	}

	return out
}

func acceptedSourceSignatureBlockerCovered(blocker string, grounding atomPromptGrounding) bool {
	if !strings.Contains(blocker, "without source-verified signature evidence") {
		return false
	}

	seenCall := false
	for _, match := range featurePlannerCallPattern.FindAllStringSubmatch(blocker, -1) {
		if len(match) < 3 {
			continue
		}
		name := strings.TrimSpace(match[1])
		args := strings.TrimSpace(match[2])
		if !plannerCallNeedsSignatureEvidence(name, args) {
			continue
		}
		seenCall = true
		if !grounding.hasVerifiedSignatureForCall(name) {
			return false
		}
	}

	return seenCall
}

func normalizedEditScopeSet(paths []string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, path := range plancontract.UniqueScopePaths(paths) {
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
	if state == nil || strings.TrimSpace(state.Continuation.ResumeAction) != string(recoverycontract.NextActionRetryPlanExecute) {
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
			Type:        featureplannercontract.EventName(featureplannercontract.EventPlanExecuteRetryResumed),
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
