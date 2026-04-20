package eval

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAnalyzeExtractsMetricsFromRecords(t *testing.T) {
	records := []Record{
		{Type: "message", Data: map[string]any{"role": "user", "content": "please edit the banner"}},
		{Type: "message", Data: map[string]any{"role": "route_plan", "content": "initial=locate followup=implement reason=locate_request contract=locate_targets mode=plan workflow=optimization_refactor phase=awaiting_approval thoroughness=medium"}},
		{Type: "message", Data: map[string]any{"role": "replan_recommendation", "content": "review -> architecture: review produced structural evidence without a bounded finding"}},
		{Type: "message", Data: map[string]any{"role": "explore_bootstrap", "content": "Repository profile:\nLanguage capability registry:\n- go tools: go_overview, go_find_symbol | signals: go.mod, internal/app/app.go\n- shell tools: search_content, read_file | signals: scripts/run.sh"}},
		{Type: "message", Data: map[string]any{"role": "review_summary", "content": "initial summary rejected: failed lane-specific grounding or contract"}},
		{Type: "message", Data: map[string]any{"role": "verification_plan", "content": `{"commands":["go build ./...","go test -vet=off ./internal/app"],"uses_full_suite":false,"coverage":"targeted package verification","confidence":"medium","risk_level":"high","expected_failures":["compile errors","routing or prompt assembly regressions"],"repair_policy":"repair targeted failures before widening scope"}`}},
		{Type: "message", Data: map[string]any{"role": "readiness_check", "content": `{"lane":"review","ready":true,"confidence":"high","action":"answer","reason":"summary satisfies lane contract and evidence gate","evidence_paths":["internal/app/app.go"]}`}},
		{Type: "message", Data: map[string]any{"role": "deepening_attempt", "content": `{"lane":"review","attempt":1,"attempted":true,"exhausted":false,"action":"retry_with_evidence","reason":"readiness gate failed; retry with wider candidate reads","evidence_paths":["internal/app/app.go","internal/workspace/manager.go"]}`}},
		{Type: "message", Data: map[string]any{"role": "final_answer_critic", "content": `{"contract":"implementation_result","passed":true,"action":"accept"}`}},
		{Type: "message", Data: map[string]any{"role": "effort_policy", "content": `{"lane":"review","level":"thorough","max_tool_steps":12,"analysis_tool_budget":6,"implement_tool_budget":6,"deepening_passes":2,"time_budget_seconds":90,"max_output_tokens":2048,"reasoning_budget":-1,"confidence_floor":"high_or_exhausted_budget","budget_exhaustion_mode":"answer_with_explicit_uncertainty_or_fallback"}`}},
		{Type: "message", Data: map[string]any{"role": "long_horizon_state", "content": `{"action":"persist","source_lane":"architecture","stage":1,"focus_path":"internal/app/app.go","changed":["internal/app/app.go"],"remaining":["internal/workspace/manager.go"],"next_step":"Continue with the workspace manager extraction.","verification":"passed","resumable":true}`}},
		{Type: "message", Data: map[string]any{"role": "workflow_state", "content": `{"action":"persist","kind":"optimization_refactor","phase":"awaiting_approval","source_lane":"architecture","target_refs":["internal/app/app.go"],"editable_targets":["internal/app/app.go"],"read_only_evidence":["internal/workspace/manager.go"],"evidence_paths":["internal/app/app.go","internal/workspace/manager.go"],"verification":["go test ./internal/app"],"next_actions":["wait for explicit approval before build-mode execution"],"resumable":true}`}},
		{Type: "message", Data: map[string]any{"role": "budget_ledger", "content": `{"lane":"review","used_model_calls":3,"used_deepening_passes":2,"max_deepening_passes":2,"used_tool_results":4,"new_evidence_paths":1,"total_evidence_paths":2,"stop_reason":"fallback_after_exhausted_readiness_attempts","budget_exhausted":true,"user_visible_uncertainty":"state that bounded readiness budget was exhausted"}`}},
		{Type: "message", Data: map[string]any{"role": "evidence_delta", "content": `{"lane":"review","attempt":1,"new_evidence_paths":["internal/app/app.go"],"total_paths":2,"tool_results":2,"successful_tools":2,"failed_tools":0,"marginal_value":"medium"}`}},
		{Type: "message", Data: map[string]any{"role": "claim_confidence", "content": `{"lane":"review","confidence":"high","supported_claims":1,"evidence_paths":["internal/app/app.go"],"calibration_basis":"contract acceptance plus grounded evidence path coverage"}`}},
		{Type: "message", Data: map[string]any{"role": "context_compaction", "content": `{"dropped_messages":4,"retained_messages":8,"max_history":8,"pinned_paths":["internal/app/app.go"],"pinned_memory_kind":"investigation","strategy":"retain_recent_history_with_pinned_task_memory_and_refactor_targets"}`}},
		{Type: "message", Data: map[string]any{"role": "route_ambiguity", "content": `{"selected_lane":"review","reason":"review_request","signals":["review","implement"],"ambiguous":true,"tie_break":"ordered host route precedence selected review"}`}},
		{Type: "message", Data: map[string]any{"role": "verification_feedback", "content": `{"confidence":"medium","commands":["go test ./internal/app"],"failed_commands":[],"learned_recommendation":"keep current verification plan"}`}},
		{Type: "message", Data: map[string]any{"role": "tool_failure_confidence", "content": `{"lane":"review","tool":"read_file","confidence_impact":"degrade_until_alternate_evidence_covers_gap"}`}},
		{Type: "message", Data: map[string]any{"role": "control_rails", "content": `{"lane":"review","followup_lane":"implement","contract":"review_findings","agent_mode":"plan","workflow_kind":"optimization_refactor","workflow_phase":"awaiting_approval","task_family":"review","permission_profile":"analysis_read_only_then_guarded_writes","target_refs":["internal/app/app.go"],"required_evidence":["candidate_path","bounded_finding_evidence","confidence"],"required_tools":["workspace_inspection","implementation_handoff"],"allowed_tools":["task","search_content","read_file"],"forbidden_tools":["edit_file","write_file","append_file","mkdir","mutating_shell"],"readiness_gate":["answer_contract_satisfied","required_evidence_present","forbidden_tools_absent","target_packet_or_changed_files_present","verification_contract_resolved","low_confidence_triggers_deepening"],"search_policy":["rank_candidates","read_top_candidate","deepen_if_no_bounded_finding"],"verification_contract":"tests_passed_or_failed_or_skipped_with_blocker","stop_conditions":["required_evidence_satisfied","blocked_by_missing_permission_or_information","budget_exhausted_with_uncertainty_declared","implementation_verified_or_failure_reported"],"final_critic":["answers_actual_user_request","required_evidence_cited","no_overconfident_claims","findings_first"],"failure_to_eval":false}`}},
		{Type: "tool_request", Data: map[string]any{"tool": "edit_file", "broad": false}},
		{Type: "tool_result", Data: map[string]any{"agent": "implement_verify", "summary": "Edited internal/app/app.go", "error": ""}},
		{Type: "phase", Data: map[string]any{"lane": "implement", "phase": "verify", "state": "done", "detail": "Verification passed"}},
		{Type: "message", Data: map[string]any{"role": "assistant", "content": "Done.\n\nChanged files:\n- internal/app/app.go\n\nVerification:\n- `go build ./...`: passed"}},
	}

	metrics := Analyze(records)
	if metrics.LastUser != "please edit the banner" {
		t.Fatalf("unexpected last user: %q", metrics.LastUser)
	}
	if metrics.Tools["edit_file"] != 1 {
		t.Fatalf("expected edit_file tool count, got %#v", metrics.Tools)
	}
	if !metrics.VerificationPassed {
		t.Fatal("expected verification to pass")
	}
	if len(metrics.ChangedFiles) != 1 || metrics.ChangedFiles[0] != "internal/app/app.go" {
		t.Fatalf("unexpected changed files: %#v", metrics.ChangedFiles)
	}
	if len(metrics.RoutePlans) != 1 {
		t.Fatalf("expected one route plan, got %#v", metrics.RoutePlans)
	}
	plan := metrics.RoutePlans[0]
	if plan.InitialLane != "locate" || plan.FollowupLane != "implement" || plan.Reason != "locate_request" || plan.Contract != "locate_targets" || plan.AgentMode != "plan" || plan.WorkflowKind != "optimization_refactor" || plan.WorkflowPhase != "awaiting_approval" || plan.Thoroughness != "medium" {
		t.Fatalf("unexpected route plan: %#v", plan)
	}
	if !containsStringFold(metrics.EvidencePaths, "internal/app/app.go") {
		t.Fatalf("expected evidence path from tool result summary, got %#v", metrics.EvidencePaths)
	}
	if len(metrics.Replans) != 1 || metrics.Replans[0].FromLane != "review" || metrics.Replans[0].ToLane != "architecture" {
		t.Fatalf("unexpected replan recommendation: %#v", metrics.Replans)
	}
	if !languageCapabilityMatches(metrics.LanguageCapabilities, LanguageCapabilityExpectation{Language: "go", Tool: "go_find_symbol", Signal: "go.mod"}) {
		t.Fatalf("expected Go language capability, got %#v", metrics.LanguageCapabilities)
	}
	if !languageCapabilityMatches(metrics.LanguageCapabilities, LanguageCapabilityExpectation{Language: "shell", Tool: "read_file", Signal: "scripts/run.sh"}) {
		t.Fatalf("expected shell language capability, got %#v", metrics.LanguageCapabilities)
	}
	if len(metrics.VerificationPlans) != 1 {
		t.Fatalf("expected one verification plan, got %#v", metrics.VerificationPlans)
	}
	verifyPlan := metrics.VerificationPlans[0]
	if verifyPlan.RiskLevel != "high" || verifyPlan.Confidence != "medium" || verifyPlan.Coverage != "targeted package verification" {
		t.Fatalf("unexpected verification plan: %#v", verifyPlan)
	}
	if !containsStringFold(verifyPlan.Commands, "go test -vet=off ./internal/app") || !containsStringFold(verifyPlan.ExpectedFailures, "routing or prompt assembly regressions") {
		t.Fatalf("expected verification command and failure mode, got %#v", verifyPlan)
	}
	if metrics.SummaryRejections != 1 || !containsStringFold(metrics.SummaryRejectionLanes, "review") {
		t.Fatalf("expected review summary rejection metric, got count=%d lanes=%#v", metrics.SummaryRejections, metrics.SummaryRejectionLanes)
	}
	if len(metrics.ReadinessChecks) != 1 || !metrics.ReadinessChecks[0].Ready || metrics.ReadinessChecks[0].Confidence != "high" {
		t.Fatalf("expected readiness check metric, got %#v", metrics.ReadinessChecks)
	}
	if len(metrics.DeepeningAttempts) != 1 || !metrics.DeepeningAttempts[0].Attempted || metrics.DeepeningAttempts[0].Action != "retry_with_evidence" {
		t.Fatalf("expected deepening attempt metric, got %#v", metrics.DeepeningAttempts)
	}
	if len(metrics.FinalAnswerCritics) != 1 || !metrics.FinalAnswerCritics[0].Passed || metrics.FinalAnswerCritics[0].Action != "accept" {
		t.Fatalf("expected final answer critic metric, got %#v", metrics.FinalAnswerCritics)
	}
	if len(metrics.EffortPolicies) != 1 || metrics.EffortPolicies[0].Level != "thorough" || metrics.EffortPolicies[0].DeepeningPasses != 2 {
		t.Fatalf("expected effort policy metric, got %#v", metrics.EffortPolicies)
	}
	if len(metrics.LongHorizonStates) != 1 || metrics.LongHorizonStates[0].Action != "persist" || !metrics.LongHorizonStates[0].Resumable {
		t.Fatalf("expected long-horizon state metric, got %#v", metrics.LongHorizonStates)
	}
	if len(metrics.WorkflowStates) != 1 || metrics.WorkflowStates[0].Phase != "awaiting_approval" || !metrics.WorkflowStates[0].Resumable {
		t.Fatalf("expected workflow state metric, got %#v", metrics.WorkflowStates)
	}
	if !containsStringFold(metrics.WorkflowStates[0].EditableTargets, "internal/app/app.go") || !containsStringFold(metrics.WorkflowStates[0].Verification, "go test ./internal/app") {
		t.Fatalf("expected workflow target and verification metrics, got %#v", metrics.WorkflowStates[0])
	}
	if len(metrics.BudgetLedgers) != 1 || !metrics.BudgetLedgers[0].BudgetExhausted {
		t.Fatalf("expected budget ledger metric, got %#v", metrics.BudgetLedgers)
	}
	if len(metrics.EvidenceDeltas) != 1 || metrics.EvidenceDeltas[0].MarginalValue != "medium" {
		t.Fatalf("expected evidence delta metric, got %#v", metrics.EvidenceDeltas)
	}
	if len(metrics.ClaimConfidences) != 1 || metrics.ClaimConfidences[0].Confidence != "high" {
		t.Fatalf("expected claim confidence metric, got %#v", metrics.ClaimConfidences)
	}
	if len(metrics.ContextCompactions) != 1 || metrics.ContextCompactions[0].DroppedMessages != 4 || !containsStringFold(metrics.ContextCompactions[0].PinnedPaths, "internal/app/app.go") {
		t.Fatalf("expected context compaction metric, got %#v", metrics.ContextCompactions)
	}
	if len(metrics.RouteAmbiguities) != 1 || !metrics.RouteAmbiguities[0].Ambiguous {
		t.Fatalf("expected route ambiguity metric, got %#v", metrics.RouteAmbiguities)
	}
	if len(metrics.VerificationFeedback) != 1 || metrics.VerificationFeedback[0].LearnedRecommendation == "" {
		t.Fatalf("expected verification feedback metric, got %#v", metrics.VerificationFeedback)
	}
	if len(metrics.ToolFailureConfidence) != 1 || metrics.ToolFailureConfidence[0].ConfidenceImpact == "" {
		t.Fatalf("expected tool-failure confidence metric, got %#v", metrics.ToolFailureConfidence)
	}
	if len(metrics.ControlRails) != 1 || metrics.ControlRails[0].TaskFamily != "review" || metrics.ControlRails[0].PermissionProfile != "analysis_read_only_then_guarded_writes" {
		t.Fatalf("expected control rails metric, got %#v", metrics.ControlRails)
	}
	if !containsStringFold(metrics.ControlRails[0].TargetRefs, "internal/app/app.go") {
		t.Fatalf("expected control rails target refs, got %#v", metrics.ControlRails[0].TargetRefs)
	}
	if metrics.ControlRails[0].AgentMode != "plan" || metrics.ControlRails[0].WorkflowKind != "optimization_refactor" || metrics.ControlRails[0].WorkflowPhase != "awaiting_approval" || !containsStringFold(metrics.ControlRails[0].AllowedTools, "task") {
		t.Fatalf("expected control rails mode and allowed tools, got %#v", metrics.ControlRails[0])
	}
}

func TestAnalyzeUsesLaneSummaryAsAssistantFallback(t *testing.T) {
	records := []Record{
		{Type: "message", Data: map[string]any{"role": "user", "content": "prepare a plan"}},
		{Type: "message", Data: map[string]any{"role": "route_plan", "content": "initial=architecture reason=architecture_request contract=architecture_assessment"}},
		{Type: "message", Data: map[string]any{"role": "architecture_summary", "content": "Plan needs a concrete function anchor before editing."}},
		{Type: "message", Data: map[string]any{"role": "user", "content": "begin the plan"}},
		{Type: "message", Data: map[string]any{"role": "route_plan_final", "content": "initial=implement reason=direct_implementation contract=implementation_result"}},
		{Type: "message", Data: map[string]any{"role": "architecture_summary", "content": "Need a concrete line anchor before editing."}},
	}

	metrics := Analyze(records)
	if metrics.AssistantTurns != 2 {
		t.Fatalf("expected lane summaries to count as assistant fallback turns, got %d", metrics.AssistantTurns)
	}
	if !strings.Contains(metrics.FinalAssistant, "concrete line anchor") {
		t.Fatalf("expected final assistant fallback from latest lane summary, got %q", metrics.FinalAssistant)
	}
	if len(metrics.RoutePlans) != 2 || metrics.RoutePlans[1].InitialLane != "implement" {
		t.Fatalf("expected route_plan_final to be evaluated, got %#v", metrics.RoutePlans)
	}
}

func TestAnalyzeCountsLaneSummaryWhenLaterTurnHasAssistant(t *testing.T) {
	records := []Record{
		{Type: "message", Data: map[string]any{"role": "user", "content": "prepare a plan"}},
		{Type: "message", Data: map[string]any{"role": "architecture_summary", "content": "Plan summary."}},
		{Type: "message", Data: map[string]any{"role": "user", "content": "begin the plan"}},
		{Type: "message", Data: map[string]any{"role": "assistant", "content": "Need a concrete anchor."}},
	}

	metrics := Analyze(records)
	if metrics.AssistantTurns != 2 {
		t.Fatalf("expected lane summary plus final assistant to cover both user turns, got %d", metrics.AssistantTurns)
	}
	if metrics.FinalAssistant != "Need a concrete anchor." {
		t.Fatalf("expected real assistant to remain final assistant, got %q", metrics.FinalAssistant)
	}
}

func TestEvaluateScenarioReportsFailures(t *testing.T) {
	records := []Record{
		{Type: "message", Data: map[string]any{"role": "user", "content": "please edit the banner"}},
		{Type: "message", Data: map[string]any{"role": "route_plan", "content": "initial=explore reason=explore_request contract=grounded_explanation"}},
		{Type: "message", Data: map[string]any{"role": "replan_recommendation", "content": "explore -> review: explore produced review-shaped evidence"}},
		{Type: "message", Data: map[string]any{"role": "verification_plan", "content": `{"commands":["go build ./..."],"uses_full_suite":false,"coverage":"compile-only verification","confidence":"low","risk_level":"low","expected_failures":["unexpected integration regressions"]}`}},
		{Type: "message", Data: map[string]any{"role": "architecture_summary", "content": "retry summary rejected: failed lane-specific grounding or contract"}},
		{Type: "tool_request", Data: map[string]any{"tool": "list_files", "broad": true}},
		{Type: "tool_result", Data: map[string]any{"summary": "Repeated dead-end action rejected", "error": "loop"}},
		{Type: "message", Data: map[string]any{"role": "assistant", "content": "I could not finish cleanly."}},
	}
	maxBroad := 0
	maxDeadEnds := 0
	maxSummaryRejections := 0
	scenario := Scenario{
		Name:                 "bounded implement",
		PromptContains:       []string{"edit the banner"},
		RequireTools:         []string{"edit_file"},
		RequireRoutePlan:     &RoutePlanExpectation{InitialLane: "locate", FollowupLane: "implement", Reason: "locate_request", Contract: "locate_targets"},
		RequireReplan:        &ReplanExpectation{FromLane: "review", ToLane: "architecture", Reason: "structural evidence"},
		RequireEvidencePaths: []string{"internal/app/app.go"},
		RequireVerificationPlan: &VerificationPlanExpectation{
			Coverage:                "full-suite escalation",
			Confidence:              "high",
			RiskLevel:               "high",
			RequireCommands:         []string{"go test -vet=off ./..."},
			RequireExpectedFailures: []string{"routing or prompt assembly regressions"},
		},
		RequireReadiness: &ReadinessExpectation{
			Lane:       "review",
			Ready:      boolPtr(true),
			Confidence: "high",
			Action:     "answer",
		},
		RequireDeepening: &DeepeningExpectation{
			Lane:      "review",
			Attempted: boolPtr(true),
			Action:    "retry_with_evidence",
			Exhausted: boolPtr(false),
		},
		RequireFinalAnswerCritic: &FinalAnswerCriticExpectation{
			Contract: "implementation_result",
			Passed:   boolPtr(true),
			Action:   "accept",
		},
		RequireEffortPolicy: &EffortPolicyExpectation{
			Lane:                  "review",
			Level:                 "thorough",
			MinAnalysisToolBudget: intPtr(6),
			MinDeepeningPasses:    intPtr(2),
			MinMaxOutputTokens:    intPtr(2048),
			ConfidenceFloor:       "high_or_exhausted_budget",
		},
		RequireLongHorizonState: &LongHorizonStateExpectation{
			Action:     "persist",
			SourceLane: "architecture",
			Stage:      intPtr(1),
			Resumable:  boolPtr(true),
		},
		RequireWorkflowState: &WorkflowStateExpectation{
			Action:              "persist",
			Kind:                "optimization_refactor",
			Phase:               "awaiting_approval",
			RequireEditable:     []string{"internal/app/app.go"},
			RequireVerification: []string{"go test ./internal/app"},
			Resumable:           boolPtr(true),
		},
		RequireBudgetLedger: &BudgetLedgerExpectation{
			Lane:             "review",
			StopReason:       "fallback",
			MinModelCalls:    intPtr(2),
			MinEvidencePaths: intPtr(1),
			BudgetExhausted:  boolPtr(true),
		},
		RequireEvidenceDelta: &EvidenceDeltaExpectation{
			Lane:           "review",
			MinNewPaths:    intPtr(1),
			MarginalValue:  "medium",
			MinToolResults: intPtr(1),
		},
		RequireClaimConfidence: &ClaimConfidenceExpectation{
			Lane:             "review",
			Confidence:       "high",
			MinEvidencePaths: intPtr(1),
		},
		RequireContextCompaction: &ContextCompactionExpectation{
			MinDroppedMessages: intPtr(1),
			RequirePinnedPaths: []string{"internal/app/app.go"},
			PinnedMemoryKind:   "investigation",
			StrategyContains:   []string{"pinned"},
		},
		RequireRouteAmbiguity: &RouteAmbiguityExpectation{
			SelectedLane:   "review",
			Ambiguous:      boolPtr(true),
			RequireSignals: []string{"review", "implement"},
		},
		RequireVerificationFeedback: &VerificationFeedbackExpectation{
			Confidence:             "high",
			RecommendationContains: []string{"repair"},
		},
		RequireToolFailureConfidence: &ToolFailureConfidenceExpectation{
			Lane:             "review",
			Tool:             "read_file",
			ConfidenceImpact: "degrade_until_alternate_evidence_covers_gap",
		},
		RequireControlRails: &ControlRailsExpectation{
			Lane:                  "review",
			Contract:              "review_findings",
			TaskFamily:            "review",
			PermissionProfile:     "analysis_read_only_then_guarded_writes",
			RequireEvidence:       []string{"bounded_finding_evidence"},
			RequireTools:          []string{"workspace_inspection"},
			RequireForbiddenTools: []string{"edit_file"},
			RequireReadinessGate:  []string{"required_evidence_present"},
			RequireSearchPolicy:   []string{"deepen_if_no_bounded_finding"},
			VerificationContract:  "tests_passed_or_failed_or_skipped_with_blocker",
			RequireStopConditions: []string{"implementation_verified_or_failure_reported"},
			RequireFinalCritic:    []string{"findings_first"},
			FailureToEval:         boolPtr(false),
		},
		RequireLanguageCapabilities: []LanguageCapabilityExpectation{{
			Language: "go",
			Tool:     "go_find_symbol",
			Signal:   "go.mod",
		}},
		MaxBroadToolRequests: &maxBroad,
		MaxRepeatedDeadEnds:  &maxDeadEnds,
		MaxSummaryRejections: &maxSummaryRejections,
		RequireVerification:  "passed",
	}

	report := Evaluate("log.jsonl", records, scenario)
	if report.Passed {
		t.Fatalf("expected scenario to fail, got %#v", report)
	}
	joined := strings.Join(report.Findings, "\n")
	for _, expected := range []string{"required tool", "route plan mismatch", "replan recommendation mismatch", "evidence paths do not include", "verification plan mismatch", "required readiness check", "required deepening attempt", "required final answer critic", "required effort policy", "required long-horizon state", "required workflow state", "required budget ledger", "required evidence delta", "required claim confidence", "required context compaction", "required route ambiguity", "required verification feedback", "required tool-failure confidence", "required control rails", "language capabilities do not include", "broad tool requests exceeded", "repeated dead-end requests exceeded", "summary rejections exceeded", "verification did not pass"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("expected finding containing %q, got %q", expected, joined)
		}
	}
}

func TestEvaluateScenarioRequiresAssistantForEachUserTurn(t *testing.T) {
	records := []Record{
		{Type: "message", Data: map[string]any{"role": "user", "content": "Can you identify opportunities for us to refactor our repo for long-term maintainability"}},
		{Type: "message", Data: map[string]any{"role": "route_plan", "content": "initial=architecture reason=architecture_request contract=architecture_assessment thoroughness=medium"}},
		{Type: "message", Data: map[string]any{"role": "architecture_summary", "content": "Architecture summary: internal/workspace/manager.go is a hotspot."}},
		{Type: "message", Data: map[string]any{"role": "user", "content": "Can you prepare a refactor plan for this file?"}},
		{Type: "message", Data: map[string]any{"role": "route_plan", "content": "initial=architecture reason=architecture_request contract=architecture_assessment thoroughness=medium"}},
	}

	report := Evaluate("log.jsonl", records, Scenario{
		Name:                        "conversation coherence",
		RequireAssistantForEachUser: true,
	})
	if report.Passed {
		t.Fatalf("expected missing assistant-turn coverage to fail, got %#v", report)
	}
	if !strings.Contains(strings.Join(report.Findings, "\n"), "assistant turns fewer than user turns") {
		t.Fatalf("expected assistant-turn finding, got %#v", report.Findings)
	}
}

func TestEvaluateScenarioAcceptsRequiredRoutePlan(t *testing.T) {
	records := []Record{
		{Type: "message", Data: map[string]any{"role": "user", "content": "analyze structure"}},
		{Type: "message", Data: map[string]any{"role": "route_plan", "content": "initial=architecture followup=implement reason=architecture_request contract=architecture_assessment thoroughness=medium"}},
		{Type: "message", Data: map[string]any{"role": "replan_recommendation", "content": "architecture -> review: architecture produced defect-shaped evidence"}},
		{Type: "message", Data: map[string]any{"role": "assistant", "content": "Architecture summary"}},
	}

	report := Evaluate("log.jsonl", records, Scenario{
		Name: "architecture route",
		RequireRoutePlan: &RoutePlanExpectation{
			InitialLane:  "architecture",
			FollowupLane: "implement",
			Reason:       "architecture_request",
			Contract:     "architecture_assessment",
			Thoroughness: "medium",
		},
		RequireReplan: &ReplanExpectation{
			FromLane: "architecture",
			ToLane:   "review",
			Reason:   "defect-shaped",
		},
	})
	if !report.Passed {
		t.Fatalf("expected scenario to pass, got %#v", report)
	}
}

func TestEvaluateScenarioAcceptsFrontierControllerTelemetry(t *testing.T) {
	records := []Record{
		{Type: "message", Data: map[string]any{"role": "user", "content": "review routing thoroughly"}},
		{Type: "message", Data: map[string]any{"role": "budget_ledger", "content": `{"lane":"review","used_model_calls":3,"used_deepening_passes":2,"max_deepening_passes":2,"used_tool_results":4,"new_evidence_paths":1,"total_evidence_paths":2,"stop_reason":"fallback_after_exhausted_readiness_attempts","budget_exhausted":true,"user_visible_uncertainty":"state that bounded readiness budget was exhausted and identify unresolved gaps"}`}},
		{Type: "message", Data: map[string]any{"role": "evidence_delta", "content": `{"lane":"review","attempt":2,"new_evidence_paths":["internal/eval/eval.go"],"total_paths":2,"tool_results":2,"successful_tools":1,"failed_tools":1,"marginal_value":"medium"}`}},
		{Type: "message", Data: map[string]any{"role": "claim_confidence", "content": `{"lane":"review","confidence":"low","supported_claims":0,"evidence_paths":["internal/app/app.go","internal/eval/eval.go"],"unresolved_uncertainty":["summary does not satisfy lane contract"],"calibration_basis":"contract acceptance plus grounded evidence path coverage"}`}},
		{Type: "message", Data: map[string]any{"role": "context_compaction", "content": `{"dropped_messages":6,"retained_messages":8,"max_history":8,"pinned_paths":["internal/app/app.go","internal/eval/eval.go"],"pinned_memory_kind":"investigation","strategy":"retain_recent_history_with_pinned_task_memory_and_refactor_targets"}`}},
		{Type: "message", Data: map[string]any{"role": "route_ambiguity", "content": `{"selected_lane":"review","reason":"review_request","signals":["review","implement"],"ambiguous":true,"tie_break":"ordered host route precedence selected review"}`}},
		{Type: "message", Data: map[string]any{"role": "verification_feedback", "content": `{"risk_level":"high","coverage":"targeted package verification","confidence":"medium","commands":["go test ./internal/app"],"failed_commands":["go test ./internal/app"],"learned_recommendation":"repair failing checks before widening scope"}`}},
		{Type: "message", Data: map[string]any{"role": "tool_failure_confidence", "content": `{"lane":"review","tool":"read_file","action":"alternate_evidence_required","confidence_impact":"degrade_until_alternate_evidence_covers_gap","reason":"read_file failed during deepening"}`}},
		{Type: "message", Data: map[string]any{"role": "control_rails", "content": `{"lane":"review","contract":"review_findings","task_family":"review","permission_profile":"read_only_workspace","required_evidence":["candidate_path","bounded_finding_evidence","confidence"],"required_tools":["workspace_inspection"],"forbidden_tools":["edit_file","write_file"],"readiness_gate":["answer_contract_satisfied","required_evidence_present","low_confidence_triggers_deepening"],"search_policy":["rank_candidates","read_top_candidate","deepen_if_no_bounded_finding"],"verification_contract":"not_required_for_read_only_lane","stop_conditions":["required_evidence_satisfied","budget_exhausted_with_uncertainty_declared"],"final_critic":["answers_actual_user_request","required_evidence_cited","findings_first"],"failure_to_eval":false}`}},
		{Type: "message", Data: map[string]any{"role": "assistant", "content": "No bounded finding yet; uncertainty remains explicit."}},
	}
	report := Evaluate("log.jsonl", records, Scenario{
		Name: "frontier telemetry",
		RequireBudgetLedger: &BudgetLedgerExpectation{
			Lane:              "review",
			StopReason:        "fallback",
			MinModelCalls:     intPtr(3),
			MinEvidencePaths:  intPtr(2),
			BudgetExhausted:   boolPtr(true),
			UncertaintyPolicy: "bounded readiness budget",
		},
		RequireEvidenceDelta: &EvidenceDeltaExpectation{
			Lane:           "review",
			MinNewPaths:    intPtr(1),
			MarginalValue:  "medium",
			MinToolResults: intPtr(2),
			MaxFailedTools: intPtr(1),
		},
		RequireClaimConfidence: &ClaimConfidenceExpectation{
			Lane:             "review",
			Confidence:       "low",
			MinEvidencePaths: intPtr(2),
			MaxUnresolved:    intPtr(1),
			CalibrationBasis: "grounded evidence",
		},
		RequireContextCompaction: &ContextCompactionExpectation{
			MinDroppedMessages:  intPtr(6),
			MinRetainedMessages: intPtr(8),
			MaxHistory:          intPtr(8),
			RequirePinnedPaths:  []string{"internal/app/app.go", "internal/eval/eval.go"},
			PinnedMemoryKind:    "investigation",
			StrategyContains:    []string{"pinned_task_memory"},
		},
		RequireRouteAmbiguity: &RouteAmbiguityExpectation{
			SelectedLane:     "review",
			Ambiguous:        boolPtr(true),
			RequireSignals:   []string{"review", "implement"},
			TieBreakContains: []string{"selected review"},
		},
		RequireVerificationFeedback: &VerificationFeedbackExpectation{
			Confidence:             "medium",
			RequireFailedCommands:  []string{"go test ./internal/app"},
			RecommendationContains: []string{"repair failing checks"},
		},
		RequireToolFailureConfidence: &ToolFailureConfidenceExpectation{
			Lane:             "review",
			Tool:             "read_file",
			ConfidenceImpact: "degrade_until_alternate_evidence_covers_gap",
		},
		RequireControlRails: &ControlRailsExpectation{
			Lane:                  "review",
			Contract:              "review_findings",
			TaskFamily:            "review",
			PermissionProfile:     "read_only_workspace",
			RequireEvidence:       []string{"bounded_finding_evidence"},
			RequireTools:          []string{"workspace_inspection"},
			RequireForbiddenTools: []string{"edit_file"},
			RequireReadinessGate:  []string{"low_confidence_triggers_deepening"},
			RequireSearchPolicy:   []string{"deepen_if_no_bounded_finding"},
			VerificationContract:  "not_required_for_read_only_lane",
			RequireStopConditions: []string{"budget_exhausted_with_uncertainty_declared"},
			RequireFinalCritic:    []string{"findings_first"},
			FailureToEval:         boolPtr(false),
		},
	})
	if !report.Passed {
		t.Fatalf("expected frontier telemetry scenario to pass, got %#v", report)
	}
}

func TestBuildSuggestedScenarioReflectsObservedMetrics(t *testing.T) {
	records := []Record{
		{Type: "message", Data: map[string]any{"role": "user", "content": "remove the banner text"}},
		{Type: "message", Data: map[string]any{"role": "route_plan", "content": "initial=locate followup=implement reason=locate_request contract=locate_targets"}},
		{Type: "message", Data: map[string]any{"role": "replan_recommendation", "content": "locate -> review: locate produced review-shaped evidence"}},
		{Type: "message", Data: map[string]any{"role": "locate_bootstrap", "content": "Language capability registry:\n- go tools: go_overview, go_find_symbol | signals: go.mod, internal/app/app.go"}},
		{Type: "message", Data: map[string]any{"role": "verification_plan", "content": `{"commands":["go build ./...","go test -vet=off ./..."],"uses_full_suite":true,"coverage":"full-suite escalation","confidence":"high","risk_level":"high","expected_failures":["compile errors","package test regressions"]}`}},
		{Type: "message", Data: map[string]any{"role": "readiness_check", "content": `{"lane":"locate","ready":false,"confidence":"low","action":"retry","reason":"response explicitly reports insufficient evidence","evidence_paths":["internal/app/app.go"]}`}},
		{Type: "message", Data: map[string]any{"role": "deepening_attempt", "content": `{"lane":"locate","attempt":1,"attempted":true,"exhausted":false,"action":"retry_with_evidence","reason":"readiness gate failed; retry with deeper evidence","evidence_paths":["internal/app/app.go"]}`}},
		{Type: "message", Data: map[string]any{"role": "final_answer_critic", "content": `{"contract":"implementation_result","passed":false,"action":"repair","repaired":true,"findings":["missing verification section"]}`}},
		{Type: "message", Data: map[string]any{"role": "effort_policy", "content": `{"lane":"locate","level":"medium","analysis_tool_budget":6,"implement_tool_budget":6,"confidence_floor":"medium","budget_exhaustion_mode":"answer_with_explicit_uncertainty_or_fallback"}`}},
		{Type: "message", Data: map[string]any{"role": "long_horizon_state", "content": `{"action":"resume","source_lane":"architecture","stage":2,"focus_path":"internal/workspace/manager.go","remaining":["internal/agent/runner.go"],"next_step":"Continue with runner extraction.","resumable":true}`}},
		{Type: "message", Data: map[string]any{"role": "budget_ledger", "content": `{"lane":"locate","used_model_calls":2,"used_deepening_passes":1,"max_deepening_passes":2,"used_tool_results":1,"new_evidence_paths":1,"total_evidence_paths":1,"stop_reason":"fallback_after_exhausted_readiness_attempts","budget_exhausted":true,"user_visible_uncertainty":"state that bounded readiness budget was exhausted"}`}},
		{Type: "message", Data: map[string]any{"role": "evidence_delta", "content": `{"lane":"locate","attempt":1,"new_evidence_paths":["internal/app/app.go"],"total_paths":1,"tool_results":1,"successful_tools":1,"failed_tools":0,"marginal_value":"medium"}`}},
		{Type: "message", Data: map[string]any{"role": "claim_confidence", "content": `{"lane":"locate","confidence":"low","supported_claims":0,"evidence_paths":["internal/app/app.go"],"unresolved_uncertainty":["response explicitly reports insufficient evidence"],"calibration_basis":"contract acceptance plus grounded evidence path coverage"}`}},
		{Type: "message", Data: map[string]any{"role": "context_compaction", "content": `{"dropped_messages":3,"retained_messages":5,"max_history":5,"pinned_paths":["internal/app/app.go"],"pinned_memory_kind":"partial_repair","strategy":"retain_recent_history_with_pinned_task_memory_and_refactor_targets"}`}},
		{Type: "message", Data: map[string]any{"role": "route_ambiguity", "content": `{"selected_lane":"locate","reason":"locate_request","signals":["locate","implement"],"ambiguous":true,"tie_break":"ordered host route precedence selected locate"}`}},
		{Type: "message", Data: map[string]any{"role": "verification_feedback", "content": `{"risk_level":"high","coverage":"full-suite escalation","confidence":"high","commands":["go test ./..."],"failed_commands":["go test ./..."],"failure_classes":["go_unused_variable"],"learned_recommendation":"record full-suite success for similar high-risk surfaces"}`}},
		{Type: "message", Data: map[string]any{"role": "tool_failure_confidence", "content": `{"lane":"locate","tool":"read_file","action":"alternate_evidence_required","confidence_impact":"degrade_until_alternate_evidence_covers_gap","reason":"read_file failed during deepening"}`}},
		{Type: "message", Data: map[string]any{"role": "control_rails", "content": `{"lane":"locate","followup_lane":"implement","contract":"locate_targets","task_family":"locate","permission_profile":"analysis_read_only_then_guarded_writes","required_evidence":["exact_workspace_path","line_or_snippet","target_match_reason"],"required_tools":["search_content","read_file","implementation_handoff"],"forbidden_tools":["edit_file","write_file","append_file","mkdir","mutating_shell"],"readiness_gate":["answer_contract_satisfied","required_evidence_present","target_packet_or_changed_files_present","verification_contract_resolved"],"search_policy":["literal_search_first","fallback_pattern_search","read_best_candidate"],"verification_contract":"tests_passed_or_failed_or_skipped_with_blocker","stop_conditions":["required_evidence_satisfied","implementation_verified_or_failure_reported"],"final_critic":["answers_actual_user_request","required_evidence_cited","exact_target_or_no_match"],"failure_to_eval":false}`}},
		{Type: "message", Data: map[string]any{"role": "explore_summary", "content": "explore summary rejected: failed grounded explanation contract"}},
		{Type: "tool_request", Data: map[string]any{"step": 1, "tool": "search_content", "broad": false}},
		{Type: "tool_result", Data: map[string]any{"step": 1, "summary": "Found 1 matching line at internal/app/app.go:42", "error": ""}},
		{Type: "phase", Data: map[string]any{"lane": "locate", "phase": "complete", "state": "done"}},
		{Type: "phase", Data: map[string]any{"lane": "implement", "phase": "verify", "state": "done"}},
		{Type: "message", Data: map[string]any{"role": "assistant", "content": "Changed files:\n- internal/app/app.go"}},
	}

	scenario := BuildSuggestedScenario("banner-remove", records)
	if scenario.Name != "banner-remove" {
		t.Fatalf("unexpected scenario name: %q", scenario.Name)
	}
	if len(scenario.PromptContains) != 1 || scenario.PromptContains[0] != "remove the banner text" {
		t.Fatalf("unexpected prompt matchers: %#v", scenario.PromptContains)
	}
	if scenario.RequireVerification != "passed" {
		t.Fatalf("expected passed verification, got %q", scenario.RequireVerification)
	}
	if !containsStringFold(scenario.RequireTools, "search_content") {
		t.Fatalf("expected successful narrowing tool in scenario: %#v", scenario.RequireTools)
	}
	if !containsStringFold(scenario.RequireChangedFiles, "internal/app/app.go") {
		t.Fatalf("expected changed file in scenario: %#v", scenario.RequireChangedFiles)
	}
	if scenario.RequireRoutePlan == nil || scenario.RequireRoutePlan.InitialLane != "locate" || scenario.RequireRoutePlan.Contract != "locate_targets" {
		t.Fatalf("expected suggested route plan in scenario, got %#v", scenario.RequireRoutePlan)
	}
	if scenario.RequireReplan == nil || scenario.RequireReplan.FromLane != "locate" || scenario.RequireReplan.ToLane != "review" {
		t.Fatalf("expected suggested replan in scenario, got %#v", scenario.RequireReplan)
	}
	if !containsStringFold(scenario.RequireEvidencePaths, "internal/app/app.go") {
		t.Fatalf("expected suggested evidence path in scenario, got %#v", scenario.RequireEvidencePaths)
	}
	if !containsLanguageCapabilityExpectation(scenario.RequireLanguageCapabilities, "go") {
		t.Fatalf("expected suggested Go language capability, got %#v", scenario.RequireLanguageCapabilities)
	}
	if scenario.RequireVerificationPlan == nil || scenario.RequireVerificationPlan.RiskLevel != "high" || scenario.RequireVerificationPlan.UsesFullSuite == nil || !*scenario.RequireVerificationPlan.UsesFullSuite {
		t.Fatalf("expected suggested verification plan, got %#v", scenario.RequireVerificationPlan)
	}
	if scenario.RequireReadiness == nil || scenario.RequireReadiness.Ready == nil || *scenario.RequireReadiness.Ready || scenario.RequireReadiness.Action != "retry" {
		t.Fatalf("expected suggested readiness check, got %#v", scenario.RequireReadiness)
	}
	if scenario.RequireDeepening == nil || scenario.RequireDeepening.Attempted == nil || !*scenario.RequireDeepening.Attempted || scenario.RequireDeepening.Action != "retry_with_evidence" {
		t.Fatalf("expected suggested deepening attempt, got %#v", scenario.RequireDeepening)
	}
	if scenario.RequireFinalAnswerCritic == nil || scenario.RequireFinalAnswerCritic.Repaired == nil || !*scenario.RequireFinalAnswerCritic.Repaired || scenario.RequireFinalAnswerCritic.Action != "repair" {
		t.Fatalf("expected suggested final answer critic, got %#v", scenario.RequireFinalAnswerCritic)
	}
	if scenario.RequireEffortPolicy == nil || scenario.RequireEffortPolicy.Level != "medium" || scenario.RequireEffortPolicy.MinAnalysisToolBudget == nil || *scenario.RequireEffortPolicy.MinAnalysisToolBudget != 6 {
		t.Fatalf("expected suggested effort policy, got %#v", scenario.RequireEffortPolicy)
	}
	if scenario.RequireLongHorizonState == nil || scenario.RequireLongHorizonState.Action != "resume" || scenario.RequireLongHorizonState.Stage == nil || *scenario.RequireLongHorizonState.Stage != 2 {
		t.Fatalf("expected suggested long-horizon state, got %#v", scenario.RequireLongHorizonState)
	}
	if scenario.RequireBudgetLedger == nil || scenario.RequireBudgetLedger.BudgetExhausted == nil || !*scenario.RequireBudgetLedger.BudgetExhausted || scenario.RequireBudgetLedger.MinModelCalls == nil || *scenario.RequireBudgetLedger.MinModelCalls != 2 {
		t.Fatalf("expected suggested budget ledger, got %#v", scenario.RequireBudgetLedger)
	}
	if scenario.RequireEvidenceDelta == nil || scenario.RequireEvidenceDelta.MinNewPaths == nil || *scenario.RequireEvidenceDelta.MinNewPaths != 1 || scenario.RequireEvidenceDelta.MaxFailedTools == nil || *scenario.RequireEvidenceDelta.MaxFailedTools != 0 {
		t.Fatalf("expected suggested evidence delta, got %#v", scenario.RequireEvidenceDelta)
	}
	if scenario.RequireClaimConfidence == nil || scenario.RequireClaimConfidence.MaxUnresolved == nil || *scenario.RequireClaimConfidence.MaxUnresolved != 1 || scenario.RequireClaimConfidence.CalibrationBasis == "" {
		t.Fatalf("expected suggested claim confidence, got %#v", scenario.RequireClaimConfidence)
	}
	if scenario.RequireContextCompaction == nil || scenario.RequireContextCompaction.MinDroppedMessages == nil || *scenario.RequireContextCompaction.MinDroppedMessages != 3 || !containsStringFold(scenario.RequireContextCompaction.RequirePinnedPaths, "internal/app/app.go") {
		t.Fatalf("expected suggested context compaction, got %#v", scenario.RequireContextCompaction)
	}
	if scenario.RequireRouteAmbiguity == nil || scenario.RequireRouteAmbiguity.Ambiguous == nil || !*scenario.RequireRouteAmbiguity.Ambiguous || !containsStringFold(scenario.RequireRouteAmbiguity.RequireSignals, "implement") {
		t.Fatalf("expected suggested route ambiguity, got %#v", scenario.RequireRouteAmbiguity)
	}
	if scenario.RequireVerificationFeedback == nil || scenario.RequireVerificationFeedback.Confidence != "high" || len(scenario.RequireVerificationFeedback.RecommendationContains) != 1 || !containsStringFold(scenario.RequireVerificationFeedback.RequireFailureClasses, "go_unused_variable") {
		t.Fatalf("expected suggested verification feedback, got %#v", scenario.RequireVerificationFeedback)
	}
	if scenario.RequireToolFailureConfidence == nil || scenario.RequireToolFailureConfidence.Tool != "read_file" || scenario.RequireToolFailureConfidence.ConfidenceImpact == "" {
		t.Fatalf("expected suggested tool-failure confidence, got %#v", scenario.RequireToolFailureConfidence)
	}
	if scenario.RequireControlRails == nil || scenario.RequireControlRails.TaskFamily != "locate" || scenario.RequireControlRails.VerificationContract != "tests_passed_or_failed_or_skipped_with_blocker" {
		t.Fatalf("expected suggested control rails, got %#v", scenario.RequireControlRails)
	}
	if scenario.MaxSummaryRejections == nil || *scenario.MaxSummaryRejections != 1 {
		t.Fatalf("expected suggested summary rejection budget, got %#v", scenario.MaxSummaryRejections)
	}
}

func TestParseReplanRecommendation(t *testing.T) {
	replan := parseReplanRecommendation("review -> architecture: review produced structural evidence")
	if replan.FromLane != "review" || replan.ToLane != "architecture" || !strings.Contains(replan.Reason, "structural evidence") {
		t.Fatalf("unexpected replan parse: %#v", replan)
	}
	if !replanMatches(replan, ReplanExpectation{FromLane: "review", ToLane: "architecture", Reason: "structural"}) {
		t.Fatalf("expected replan matcher to accept %#v", replan)
	}
}

func TestEvidencePathsFromTextExtractsPathLineReferences(t *testing.T) {
	paths := evidencePathsFromText("Read f1 internal/app/app.go lines 1-120 and found internal/workspace/manager.go:42.")
	for _, want := range []string{"internal/app/app.go", "internal/workspace/manager.go"} {
		if !containsStringFold(paths, want) {
			t.Fatalf("expected %q in evidence paths, got %#v", want, paths)
		}
	}
}

func TestParseLanguageCapabilities(t *testing.T) {
	caps := parseLanguageCapabilities("Repository profile:\nLanguage capability registry:\n- go tools: go_overview, go_find_symbol | signals: go.mod, internal/app/app.go\n- c/c++ tools: cpp_find_symbol | signals: native/main.cpp")
	if !languageCapabilityMatches(caps, LanguageCapabilityExpectation{Language: "go", Tool: "go_find_symbol", Signal: "internal/app/app.go"}) {
		t.Fatalf("expected Go capability, got %#v", caps)
	}
	if !languageCapabilityMatches(caps, LanguageCapabilityExpectation{Language: "c/c++", Tool: "cpp_find_symbol", Signal: "native/main.cpp"}) {
		t.Fatalf("expected C/C++ capability, got %#v", caps)
	}
}

func TestParseVerificationPlan(t *testing.T) {
	plan := parseVerificationPlan(`{"commands":["go build ./...","go test -vet=off ./..."],"uses_full_suite":true,"coverage":"full-suite escalation","confidence":"high","risk_level":"high","expected_failures":["compile errors"],"repair_policy":"repair all failing full-suite checks"}`)
	if plan.Raw == "" {
		t.Fatal("expected verification plan to parse")
	}
	if !verificationPlanMatches(plan, VerificationPlanExpectation{
		Coverage:                "full-suite escalation",
		Confidence:              "high",
		RiskLevel:               "high",
		UsesFullSuite:           boolPtr(true),
		RequireCommands:         []string{"go test -vet=off ./..."},
		RequireExpectedFailures: []string{"compile errors"},
	}) {
		t.Fatalf("expected verification plan matcher to accept %#v", plan)
	}
}

func TestParseReadinessCheck(t *testing.T) {
	readiness := parseReadinessCheck(`{"lane":"architecture","ready":false,"confidence":"low","action":"retry","reason":"response asks for more evidence before it is ready","evidence_paths":["internal/app/app.go","internal/workspace/manager.go"]}`)
	if readiness.Raw == "" {
		t.Fatal("expected readiness check to parse")
	}
	if !readinessMatches(readiness, ReadinessExpectation{
		Lane:             "architecture",
		Ready:            boolPtr(false),
		Confidence:       "low",
		Action:           "retry",
		MinEvidencePaths: intPtr(2),
		ReasonContains:   []string{"more evidence"},
	}) {
		t.Fatalf("expected readiness matcher to accept %#v", readiness)
	}
}

func TestParseDeepeningAttempt(t *testing.T) {
	deepening := parseDeepeningAttempt(`{"lane":"review","attempt":2,"attempted":true,"exhausted":false,"action":"retry_with_evidence","reason":"readiness gate failed; retry with wider candidate reads","evidence_paths":["internal/app/app.go","internal/workspace/manager.go"]}`)
	if deepening.Raw == "" {
		t.Fatal("expected deepening attempt to parse")
	}
	if !deepeningMatches(deepening, DeepeningExpectation{
		Lane:             "review",
		Attempted:        boolPtr(true),
		Exhausted:        boolPtr(false),
		Action:           "retry_with_evidence",
		MinEvidencePaths: intPtr(2),
		ReasonContains:   []string{"readiness gate"},
	}) {
		t.Fatalf("expected deepening matcher to accept %#v", deepening)
	}
}

func TestParseFinalAnswerCritic(t *testing.T) {
	critic := parseFinalAnswerCritic(`{"contract":"implementation_result","passed":false,"action":"repair","repaired":true,"findings":["missing verification section"]}`)
	if critic.Raw == "" {
		t.Fatal("expected final answer critic to parse")
	}
	if !finalAnswerCriticMatches(critic, FinalAnswerCriticExpectation{
		Contract:        "implementation_result",
		Passed:          boolPtr(false),
		Action:          "repair",
		Repaired:        boolPtr(true),
		RequireFindings: []string{"verification"},
	}) {
		t.Fatalf("expected final answer critic matcher to accept %#v", critic)
	}
}

func TestParseEffortPolicy(t *testing.T) {
	policy := parseEffortPolicy(`{"lane":"review","level":"thorough","analysis_tool_budget":6,"implement_tool_budget":6,"deepening_passes":2,"max_output_tokens":2048,"reasoning_budget":-1,"confidence_floor":"high_or_exhausted_budget","budget_exhaustion_mode":"answer_with_explicit_uncertainty_or_fallback"}`)
	if policy.Raw == "" {
		t.Fatal("expected effort policy to parse")
	}
	if !effortPolicyMatches(policy, EffortPolicyExpectation{
		Lane:                   "review",
		Level:                  "thorough",
		MinAnalysisToolBudget:  intPtr(6),
		MinImplementToolBudget: intPtr(6),
		MinDeepeningPasses:     intPtr(2),
		MinMaxOutputTokens:     intPtr(2048),
		ConfidenceFloor:        "high_or_exhausted_budget",
		BudgetExhaustionMode:   "answer_with_explicit_uncertainty_or_fallback",
	}) {
		t.Fatalf("expected effort policy matcher to accept %#v", policy)
	}
}

func TestParseLongHorizonState(t *testing.T) {
	state := parseLongHorizonState(`{"action":"persist","source_lane":"architecture","stage":1,"focus_path":"internal/app/app.go","changed":["internal/app/app.go"],"remaining":["internal/workspace/manager.go"],"next_step":"Continue with the workspace manager extraction.","resumable":true}`)
	if state.Raw == "" {
		t.Fatal("expected long-horizon state to parse")
	}
	if !longHorizonStateMatches(state, LongHorizonStateExpectation{
		Action:           "persist",
		SourceLane:       "architecture",
		Stage:            intPtr(1),
		FocusPath:        "internal/app/app.go",
		RequireRemaining: []string{"internal/workspace/manager.go"},
		NextContains:     []string{"workspace manager"},
		Resumable:        boolPtr(true),
	}) {
		t.Fatalf("expected long-horizon matcher to accept %#v", state)
	}
}

func TestParseContextCompaction(t *testing.T) {
	compaction := parseContextCompaction(`{"dropped_messages":3,"retained_messages":5,"max_history":5,"pinned_paths":["internal/app/app.go"],"pinned_memory_kind":"partial_repair","strategy":"retain_recent_history_with_pinned_task_memory_and_refactor_targets"}`)
	if compaction.Raw == "" {
		t.Fatal("expected context compaction to parse")
	}
	if !contextCompactionMatches(compaction, ContextCompactionExpectation{
		MinDroppedMessages:  intPtr(3),
		MinRetainedMessages: intPtr(5),
		MaxHistory:          intPtr(5),
		RequirePinnedPaths:  []string{"internal/app/app.go"},
		PinnedMemoryKind:    "partial_repair",
		StrategyContains:    []string{"pinned_task_memory"},
	}) {
		t.Fatalf("expected context compaction matcher to accept %#v", compaction)
	}
}

func TestFrontierControllerMatchersRejectMismatches(t *testing.T) {
	if budgetLedgerMatches(BudgetLedgerSnapshot{
		Lane:               "review",
		UsedModelCalls:     1,
		TotalEvidencePaths: 0,
		StopReason:         "ready_initial_answer",
		BudgetExhausted:    false,
	}, BudgetLedgerExpectation{
		Lane:             "review",
		StopReason:       "fallback",
		MinModelCalls:    intPtr(2),
		MinEvidencePaths: intPtr(1),
		BudgetExhausted:  boolPtr(true),
	}) {
		t.Fatal("expected budget ledger mismatch to be rejected")
	}
	if !budgetLedgerMatches(BudgetLedgerSnapshot{
		Lane:            "review",
		UsedModelCalls:  2,
		TotalTokens:     42,
		StopReason:      "model_final",
		BudgetExhausted: false,
	}, BudgetLedgerExpectation{
		Lane:           "review",
		MinModelCalls:  intPtr(2),
		MinTotalTokens: intPtr(40),
		StopReason:     "model_final",
	}) {
		t.Fatal("expected budget ledger matcher to accept sufficient token usage")
	}
	if budgetLedgerMatches(BudgetLedgerSnapshot{
		Lane:        "review",
		TotalTokens: 10,
	}, BudgetLedgerExpectation{
		Lane:           "review",
		MinTotalTokens: intPtr(40),
	}) {
		t.Fatal("expected budget ledger token mismatch to be rejected")
	}
	if evidenceDeltaMatches(EvidenceDeltaSnapshot{
		Lane:             "review",
		NewEvidencePaths: nil,
		ToolResults:      1,
		FailedTools:      1,
		MarginalValue:    "blocked_by_tool_failures",
	}, EvidenceDeltaExpectation{
		Lane:           "review",
		MinNewPaths:    intPtr(1),
		MarginalValue:  "medium",
		MinToolResults: intPtr(2),
		MaxFailedTools: intPtr(0),
	}) {
		t.Fatal("expected evidence delta mismatch to be rejected")
	}
	if claimConfidenceMatches(ClaimConfidenceSnapshot{
		Lane:                  "review",
		Confidence:            "low",
		EvidencePaths:         nil,
		UnresolvedUncertainty: []string{"not enough evidence"},
		CalibrationBasis:      "contract acceptance without explicit evidence paths",
	}, ClaimConfidenceExpectation{
		Lane:             "review",
		Confidence:       "high",
		MinEvidencePaths: intPtr(1),
		MaxUnresolved:    intPtr(0),
		CalibrationBasis: "grounded",
	}) {
		t.Fatal("expected claim confidence mismatch to be rejected")
	}
	if contextCompactionMatches(ContextCompactionSnapshot{
		DroppedMessages:  1,
		RetainedMessages: 2,
		MaxHistory:       2,
		PinnedPaths:      []string{"internal/app/app.go"},
		PinnedMemoryKind: "investigation",
		Strategy:         "retain_recent_history",
	}, ContextCompactionExpectation{
		MinDroppedMessages:  intPtr(3),
		MinRetainedMessages: intPtr(5),
		MaxHistory:          intPtr(5),
		RequirePinnedPaths:  []string{"internal/eval/eval.go"},
		PinnedMemoryKind:    "partial_repair",
		StrategyContains:    []string{"pinned_task_memory"},
	}) {
		t.Fatal("expected context compaction mismatch to be rejected")
	}
	if routeAmbiguityMatches(RouteAmbiguitySnapshot{
		SelectedLane: "architecture",
		Signals:      []string{"architecture"},
		Ambiguous:    false,
		TieBreak:     "",
	}, RouteAmbiguityExpectation{
		SelectedLane:     "review",
		Ambiguous:        boolPtr(true),
		RequireSignals:   []string{"implement"},
		TieBreakContains: []string{"selected review"},
	}) {
		t.Fatal("expected route ambiguity mismatch to be rejected")
	}
	if verificationFeedbackMatches(VerificationFeedbackSnapshot{
		Confidence:            "low",
		FailedCommands:        []string{"go test ./internal/app"},
		FailureClasses:        []string{"go_unused_variable"},
		LearnedRecommendation: "repair failing checks before widening scope",
	}, VerificationFeedbackExpectation{
		Confidence:             "high",
		RequireFailedCommands:  []string{"go test ./..."},
		RequireFailureClasses:  []string{"go_import_cycle"},
		RecommendationContains: []string{"full-suite success"},
	}) {
		t.Fatal("expected verification feedback mismatch to be rejected")
	}
	if toolFailureConfidenceMatches(ToolFailureConfidenceSnapshot{
		Lane:             "review",
		Tool:             "read_file",
		ConfidenceImpact: "degrade_until_alternate_evidence_covers_gap",
	}, ToolFailureConfidenceExpectation{
		Lane:             "explore",
		Tool:             "search_content",
		ConfidenceImpact: "ignore_failure",
	}) {
		t.Fatal("expected tool-failure confidence mismatch to be rejected")
	}
}

func TestParseGeneralLongHorizonTaskMemory(t *testing.T) {
	state := parseLongHorizonState(`{"action":"persist","memory_kind":"partial_repair","source_lane":"implement","open_gaps":["verification is failing"],"attempted_searches":["validated target internal/app/app.go"],"chosen_targets":["internal/app/app.go"],"verification_history":["go test ./internal/app -> failed: exit status 1"],"next_actions":["repair failing verification before widening implementation scope"],"resumable":true}`)
	if state.Raw == "" {
		t.Fatal("expected general long-horizon state to parse")
	}
	if !longHorizonStateMatches(state, LongHorizonStateExpectation{
		Action:                     "persist",
		MemoryKind:                 "partial_repair",
		SourceLane:                 "implement",
		RequireOpenGaps:            []string{"verification is failing"},
		RequireAttemptedSearches:   []string{"validated target internal/app/app.go"},
		RequireChosenTargets:       []string{"internal/app/app.go"},
		RequireVerificationHistory: []string{"go test ./internal/app -> failed: exit status 1"},
		RequireNextActions:         []string{"repair failing verification before widening implementation scope"},
		Resumable:                  boolPtr(true),
	}) {
		t.Fatalf("expected general long-horizon matcher to accept %#v", state)
	}
}

func TestBuildSuggestedScenarioSkipsBroadOrFailedTools(t *testing.T) {
	records := []Record{
		{Type: "message", Data: map[string]any{"role": "user", "content": "padding under banner"}},
		{Type: "tool_request", Data: map[string]any{"step": 1, "tool": "list_files", "broad": true}},
		{Type: "tool_result", Data: map[string]any{"step": 1, "error": ""}},
		{Type: "tool_request", Data: map[string]any{"step": 2, "tool": "search_files", "broad": false}},
		{Type: "tool_result", Data: map[string]any{"step": 2, "error": "no matches"}},
	}

	scenario := BuildSuggestedScenario("bad-run", records)
	if len(scenario.RequireTools) != 0 {
		t.Fatalf("expected no required tools from failed/broad run, got %#v", scenario.RequireTools)
	}
}

func TestLoadRecordsReadsJSONL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.jsonl")
	const payload = "{\"type\":\"message\",\"role\":\"user\",\"content\":\"hi\"}\n{\"type\":\"message\",\"role\":\"assistant\",\"content\":\"hello\"}\n"
	if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
		t.Fatalf("write sample log: %v", err)
	}
	records, err := LoadRecords(path)
	if err != nil {
		t.Fatalf("LoadRecords: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}
}

func containsLanguageCapabilityExpectation(items []LanguageCapabilityExpectation, language string) bool {
	for _, item := range items {
		if strings.EqualFold(item.Language, language) {
			return true
		}
	}
	return false
}

func boolPtr(value bool) *bool {
	return &value
}

func intPtr(value int) *int {
	return &value
}
