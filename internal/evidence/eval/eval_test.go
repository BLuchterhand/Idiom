package eval

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"idiom/internal/contracts/autonomy"
	planartifactcontract "idiom/internal/contracts/planartifact"
)

var _ = ginkgo.It("analyze extracts metrics from records", func() {
	records := []Record{
		{Type: "message", Data: map[string]any{"role": "user", "content": "please edit the banner"}},
		{Type: "message", Data: map[string]any{"role": "route_plan", "content": "initial=locate followup=implement reason=locate_request contract=locate_targets mode=plan workflow=optimization_refactor phase=awaiting_approval thoroughness=medium"}},
		{Type: "message", Data: map[string]any{"role": "replan_recommendation", "content": "review -> architecture: review produced structural evidence without a bounded finding"}},
		{Type: "message", Data: map[string]any{"role": "explore_bootstrap", "content": "Repository profile:\nLanguage capability registry:\n- go tools: go_overview, go_find_symbol | signals: go.mod, internal/surfaces/tui/app/app.go\n- shell tools: search_content, read_file | signals: scripts/run.sh"}},
		{Type: "message", Data: map[string]any{"role": "review_summary", "content": "initial summary rejected: failed lane-specific grounding or contract"}},
		{Type: "message", Data: map[string]any{"role": "verification_plan", "content": `{"commands":["go build ./...","go test -vet=off ./internal/surfaces/tui/app"],"uses_full_suite":false,"coverage":"targeted package verification","confidence":"medium","risk_level":"high","expected_failures":["compile errors","routing or prompt assembly regressions"],"repair_policy":"repair targeted failures before widening scope"}`}},
		{Type: "message", Data: map[string]any{"role": "readiness_check", "content": `{"lane":"review","ready":true,"confidence":"high","action":"answer","reason":"summary satisfies lane contract and evidence gate","evidence_paths":["internal/surfaces/tui/app/app.go"]}`}},
		{Type: "message", Data: map[string]any{"role": "deepening_attempt", "content": `{"lane":"review","attempt":1,"attempted":true,"exhausted":false,"action":"retry_with_evidence","reason":"readiness gate failed; retry with wider candidate reads","evidence_paths":["internal/surfaces/tui/app/app.go","internal/capabilities/workspace/manager.go"]}`}},
		{Type: "message", Data: map[string]any{"role": "final_answer_critic", "content": `{"contract":"implementation_result","passed":true,"action":"accept"}`}},
		{Type: "message", Data: map[string]any{"role": "effort_policy", "content": `{"lane":"review","level":"thorough","max_tool_steps":12,"analysis_tool_budget":6,"implement_tool_budget":6,"deepening_passes":2,"time_budget_seconds":90,"max_output_tokens":2048,"reasoning_budget":-1,"confidence_floor":"high_or_exhausted_budget","budget_exhaustion_mode":"answer_with_explicit_uncertainty_or_fallback"}`}},
		{Type: "message", Data: map[string]any{"role": "long_horizon_state", "content": `{"action":"persist","source_lane":"architecture","stage":1,"focus_path":"internal/surfaces/tui/app/app.go","changed":["internal/surfaces/tui/app/app.go"],"remaining":["internal/capabilities/workspace/manager.go"],"next_step":"Continue with the workspace manager extraction.","verification":"passed","resumable":true}`}},
		{Type: "message", Data: map[string]any{"role": "workflow_state", "content": `{"action":"persist","kind":"optimization_refactor","phase":"awaiting_approval","source_lane":"architecture","target_refs":["internal/surfaces/tui/app/app.go"],"editable_targets":["internal/surfaces/tui/app/app.go"],"read_only_evidence":["internal/capabilities/workspace/manager.go"],"evidence_paths":["internal/surfaces/tui/app/app.go","internal/capabilities/workspace/manager.go"],"verification":["go test ./internal/surfaces/tui/app"],"next_actions":["wait for explicit approval before build-mode execution"],"resumable":true}`}},
		{Type: "message", Data: map[string]any{"role": "budget_ledger", "content": `{"lane":"review","used_model_calls":3,"used_deepening_passes":2,"max_deepening_passes":2,"used_tool_results":4,"new_evidence_paths":1,"total_evidence_paths":2,"stop_reason":"fallback_after_exhausted_readiness_attempts","budget_exhausted":true,"user_visible_uncertainty":"state that bounded readiness budget was exhausted"}`}},
		{Type: "message", Data: map[string]any{"role": "evidence_delta", "content": `{"lane":"review","attempt":1,"new_evidence_paths":["internal/surfaces/tui/app/app.go"],"total_paths":2,"tool_results":2,"successful_tools":2,"failed_tools":0,"marginal_value":"medium"}`}},
		{Type: "message", Data: map[string]any{"role": "claim_confidence", "content": `{"lane":"review","confidence":"high","supported_claims":1,"evidence_paths":["internal/surfaces/tui/app/app.go"],"calibration_basis":"contract acceptance plus grounded evidence path coverage"}`}},
		{Type: "message", Data: map[string]any{"role": "context_compaction", "content": `{"dropped_messages":4,"retained_messages":8,"max_history":8,"pinned_paths":["internal/surfaces/tui/app/app.go"],"pinned_memory_kind":"investigation","strategy":"retain_recent_history_with_pinned_task_memory_and_refactor_targets"}`}},
		{Type: "message", Data: map[string]any{"role": "route_ambiguity", "content": `{"selected_lane":"review","reason":"review_request","signals":["review","implement"],"ambiguous":true,"tie_break":"ordered host route precedence selected review"}`}},
		{Type: "message", Data: map[string]any{"role": "verification_feedback", "content": `{"confidence":"medium","commands":["go test ./internal/surfaces/tui/app"],"failed_commands":[],"learned_recommendation":"keep current verification plan"}`}},
		{Type: "message", Data: map[string]any{"role": "tool_failure_confidence", "content": `{"lane":"review","tool":"read_file","confidence_impact":"degrade_until_alternate_evidence_covers_gap"}`}},
		{Type: "message", Data: map[string]any{"role": "control_rails", "content": `{"lane":"review","followup_lane":"implement","contract":"review_findings","agent_mode":"plan","workflow_kind":"optimization_refactor","workflow_phase":"awaiting_approval","task_family":"review","permission_profile":"analysis_read_only_then_guarded_writes","target_refs":["internal/surfaces/tui/app/app.go"],"required_evidence":["candidate_path","bounded_finding_evidence","confidence"],"required_tools":["workspace_inspection","implementation_handoff"],"allowed_tools":["task","search_content","read_file"],"forbidden_tools":["edit_file","write_file","append_file","mkdir","mutating_shell"],"readiness_gate":["answer_contract_satisfied","required_evidence_present","forbidden_tools_absent","target_packet_or_changed_files_present","verification_contract_resolved","low_confidence_triggers_deepening"],"search_policy":["rank_candidates","read_top_candidate","deepen_if_no_bounded_finding"],"verification_contract":"tests_passed_or_failed_or_skipped_with_blocker","stop_conditions":["required_evidence_satisfied","blocked_by_missing_permission_or_information","budget_exhausted_with_uncertainty_declared","implementation_verified_or_failure_reported"],"final_critic":["answers_actual_user_request","required_evidence_cited","no_overconfident_claims","findings_first"],"failure_to_eval":false}`}},
		{Type: "tool_request", Data: map[string]any{"tool": "edit_file", "path": "internal/surfaces/tui/app/app.go", "broad": false}},
		{Type: "tool_result", Data: map[string]any{"agent": "implement_verify", "tool": "edit_file", "path": "internal/surfaces/tui/app/app.go", "summary": "Edited internal/surfaces/tui/app/app.go", "error": ""}},
		{Type: "phase", Data: map[string]any{"lane": "implement", "phase": "verify", "state": "done", "detail": "Verification passed"}},
		{Type: "message", Data: map[string]any{"role": "assistant", "content": "Done.\n\nChanged files:\n- internal/surfaces/tui/app/app.go\n\nVerification:\n- `go build ./...`: passed"}},
	}

	metrics := Analyze(records)
	gomega.Expect(metrics.LastUser).To(gomega.BeEquivalentTo("please edit the banner"), fmt.Sprintf("unexpected last user: %q", metrics.LastUser))

	gomega.Expect(metrics.Tools["edit_file"]).To(gomega.BeEquivalentTo(1), fmt.Sprintf("expected edit_file tool count, got %#v", metrics.Tools))

	gomega.Expect(metrics.VerificationPassed).To(gomega.BeTrue(), fmt.Sprint("expected verification to pass"))

	gomega.Expect(metrics.ChangedFiles).To(gomega.HaveLen(1), fmt.Sprintf("unexpected changed files: %#v", metrics.ChangedFiles))
	gomega.Expect(metrics.ChangedFiles[0]).To(gomega.BeEquivalentTo("internal/surfaces/tui/app/app.go"), fmt.Sprintf("unexpected changed files: %#v", metrics.ChangedFiles))

	gomega.Expect(metrics.RoutePlans).To(gomega.HaveLen(1), fmt.Sprintf("expected one route plan, got %#v", metrics.RoutePlans))

	plan := metrics.RoutePlans[0]
	gomega.Expect(plan.InitialLane).To(gomega.BeEquivalentTo("locate"), fmt.Sprintf("unexpected route plan: %#v", plan))
	gomega.Expect(plan.FollowupLane).To(gomega.BeEquivalentTo("implement"), fmt.Sprintf("unexpected route plan: %#v", plan))
	gomega.Expect(plan.Reason).To(gomega.BeEquivalentTo("locate_request"), fmt.Sprintf("unexpected route plan: %#v", plan))
	gomega.Expect(plan.Contract).To(gomega.BeEquivalentTo("locate_targets"), fmt.Sprintf("unexpected route plan: %#v", plan))
	gomega.Expect(plan.AgentMode).To(gomega.BeEquivalentTo("plan"), fmt.Sprintf("unexpected route plan: %#v", plan))
	gomega.Expect(plan.WorkflowKind).To(gomega.BeEquivalentTo("optimization_refactor"), fmt.Sprintf("unexpected route plan: %#v", plan))
	gomega.Expect(plan.WorkflowPhase).To(gomega.BeEquivalentTo("awaiting_approval"), fmt.Sprintf("unexpected route plan: %#v", plan))
	gomega.Expect(plan.Thoroughness).To(gomega.BeEquivalentTo("medium"), fmt.Sprintf("unexpected route plan: %#v", plan))

	gomega.Expect(containsStringFold(metrics.EvidencePaths, "internal/surfaces/tui/app/app.go")).To(gomega.BeTrue(), fmt.Sprintf("expected evidence path from tool result summary, got %#v", metrics.EvidencePaths))

	gomega.Expect(metrics.Replans).To(gomega.HaveLen(1), fmt.Sprintf("unexpected replan recommendation: %#v", metrics.Replans))
	gomega.Expect(metrics.Replans[0].FromLane).To(gomega.BeEquivalentTo("review"), fmt.Sprintf("unexpected replan recommendation: %#v", metrics.Replans))
	gomega.Expect(metrics.Replans[0].ToLane).To(gomega.BeEquivalentTo("architecture"), fmt.Sprintf("unexpected replan recommendation: %#v", metrics.Replans))

	gomega.Expect(languageCapabilityMatches(metrics.LanguageCapabilities, LanguageCapabilityExpectation{Language: "go", Tool: "go_find_symbol", Signal: "go.mod"})).To(gomega.BeTrue(), fmt.Sprintf("expected Go language capability, got %#v", metrics.LanguageCapabilities))

	gomega.Expect(languageCapabilityMatches(metrics.LanguageCapabilities, LanguageCapabilityExpectation{Language: "shell", Tool: "read_file", Signal: "scripts/run.sh"})).To(gomega.BeTrue(), fmt.Sprintf("expected shell language capability, got %#v", metrics.LanguageCapabilities))

	gomega.Expect(metrics.VerificationPlans).To(gomega.HaveLen(1), fmt.Sprintf("expected one verification plan, got %#v", metrics.VerificationPlans))

	verifyPlan := metrics.VerificationPlans[0]
	gomega.Expect(verifyPlan.RiskLevel).To(gomega.BeEquivalentTo("high"), fmt.Sprintf("unexpected verification plan: %#v", verifyPlan))
	gomega.Expect(verifyPlan.Confidence).To(gomega.BeEquivalentTo("medium"), fmt.Sprintf("unexpected verification plan: %#v", verifyPlan))
	gomega.Expect(verifyPlan.Coverage).To(gomega.BeEquivalentTo("targeted package verification"), fmt.Sprintf("unexpected verification plan: %#v", verifyPlan))

	gomega.Expect(containsStringFold(verifyPlan.Commands, "go test -vet=off ./internal/surfaces/tui/app")).To(gomega.BeTrue(), fmt.Sprintf("expected verification command and failure mode, got %#v", verifyPlan))
	gomega.Expect(containsStringFold(verifyPlan.ExpectedFailures, "routing or prompt assembly regressions")).To(gomega.BeTrue(), fmt.Sprintf("expected verification command and failure mode, got %#v", verifyPlan))

	gomega.Expect(metrics.SummaryRejections).To(gomega.BeEquivalentTo(1), fmt.Sprintf("expected review summary rejection metric, got count=%d lanes=%#v", metrics.SummaryRejections, metrics.SummaryRejectionLanes))
	gomega.Expect(containsStringFold(metrics.SummaryRejectionLanes, "review")).To(gomega.BeTrue(), fmt.Sprintf("expected review summary rejection metric, got count=%d lanes=%#v", metrics.SummaryRejections, metrics.SummaryRejectionLanes))

	gomega.Expect(metrics.ReadinessChecks).To(gomega.HaveLen(1), fmt.Sprintf("expected readiness check metric, got %#v", metrics.ReadinessChecks))
	gomega.Expect(metrics.ReadinessChecks[0].Ready).To(gomega.BeTrue(), fmt.Sprintf("expected readiness check metric, got %#v", metrics.ReadinessChecks))
	gomega.Expect(metrics.ReadinessChecks[0].Confidence).To(gomega.BeEquivalentTo("high"), fmt.Sprintf("expected readiness check metric, got %#v", metrics.ReadinessChecks))

	gomega.Expect(metrics.DeepeningAttempts).To(gomega.HaveLen(1), fmt.Sprintf("expected deepening attempt metric, got %#v", metrics.DeepeningAttempts))
	gomega.Expect(metrics.DeepeningAttempts[0].Attempted).To(gomega.BeTrue(), fmt.Sprintf("expected deepening attempt metric, got %#v", metrics.DeepeningAttempts))
	gomega.Expect(metrics.DeepeningAttempts[0].Action).To(gomega.BeEquivalentTo("retry_with_evidence"), fmt.Sprintf("expected deepening attempt metric, got %#v", metrics.DeepeningAttempts))

	gomega.Expect(metrics.FinalAnswerCritics).To(gomega.HaveLen(1), fmt.Sprintf("expected final answer critic metric, got %#v", metrics.FinalAnswerCritics))
	gomega.Expect(metrics.FinalAnswerCritics[0].Passed).To(gomega.BeTrue(), fmt.Sprintf("expected final answer critic metric, got %#v", metrics.FinalAnswerCritics))
	gomega.Expect(metrics.FinalAnswerCritics[0].Action).To(gomega.BeEquivalentTo("accept"), fmt.Sprintf("expected final answer critic metric, got %#v", metrics.FinalAnswerCritics))

	gomega.Expect(metrics.EffortPolicies).To(gomega.HaveLen(1), fmt.Sprintf("expected effort policy metric, got %#v", metrics.EffortPolicies))
	gomega.Expect(metrics.EffortPolicies[0].Level).To(gomega.BeEquivalentTo("thorough"), fmt.Sprintf("expected effort policy metric, got %#v", metrics.EffortPolicies))
	gomega.Expect(metrics.EffortPolicies[0].DeepeningPasses).To(gomega.BeEquivalentTo(2), fmt.Sprintf("expected effort policy metric, got %#v", metrics.EffortPolicies))

	gomega.Expect(metrics.LongHorizonStates).To(gomega.HaveLen(1), fmt.Sprintf("expected long-horizon state metric, got %#v", metrics.LongHorizonStates))
	gomega.Expect(metrics.LongHorizonStates[0].Action).To(gomega.BeEquivalentTo("persist"), fmt.Sprintf("expected long-horizon state metric, got %#v", metrics.LongHorizonStates))
	gomega.Expect(metrics.LongHorizonStates[0].Resumable).To(gomega.BeTrue(), fmt.Sprintf("expected long-horizon state metric, got %#v", metrics.LongHorizonStates))

	gomega.Expect(metrics.WorkflowStates).To(gomega.HaveLen(1), fmt.Sprintf("expected workflow state metric, got %#v", metrics.WorkflowStates))
	gomega.Expect(metrics.WorkflowStates[0].Phase).To(gomega.BeEquivalentTo("awaiting_approval"), fmt.Sprintf("expected workflow state metric, got %#v", metrics.WorkflowStates))
	gomega.Expect(metrics.WorkflowStates[0].Resumable).To(gomega.BeTrue(), fmt.Sprintf("expected workflow state metric, got %#v", metrics.WorkflowStates))

	gomega.Expect(containsStringFold(metrics.WorkflowStates[0].EditableTargets, "internal/surfaces/tui/app/app.go")).To(gomega.BeTrue(), fmt.Sprintf("expected workflow target and verification metrics, got %#v", metrics.WorkflowStates[0]))
	gomega.Expect(containsStringFold(metrics.WorkflowStates[0].Verification, "go test ./internal/surfaces/tui/app")).To(gomega.BeTrue(), fmt.Sprintf("expected workflow target and verification metrics, got %#v", metrics.WorkflowStates[0]))

	gomega.Expect(metrics.BudgetLedgers).To(gomega.HaveLen(1), fmt.Sprintf("expected budget ledger metric, got %#v", metrics.BudgetLedgers))
	gomega.Expect(metrics.BudgetLedgers[0].BudgetExhausted).To(gomega.BeTrue(), fmt.Sprintf("expected budget ledger metric, got %#v", metrics.BudgetLedgers))

	gomega.Expect(metrics.EvidenceDeltas).To(gomega.HaveLen(1), fmt.Sprintf("expected evidence delta metric, got %#v", metrics.EvidenceDeltas))
	gomega.Expect(metrics.EvidenceDeltas[0].MarginalValue).To(gomega.BeEquivalentTo("medium"), fmt.Sprintf("expected evidence delta metric, got %#v", metrics.EvidenceDeltas))

	gomega.Expect(metrics.ClaimConfidences).To(gomega.HaveLen(1), fmt.Sprintf("expected claim confidence metric, got %#v", metrics.ClaimConfidences))
	gomega.Expect(metrics.ClaimConfidences[0].Confidence).To(gomega.BeEquivalentTo("high"), fmt.Sprintf("expected claim confidence metric, got %#v", metrics.ClaimConfidences))

	gomega.Expect(metrics.ContextCompactions).To(gomega.HaveLen(1), fmt.Sprintf("expected context compaction metric, got %#v", metrics.ContextCompactions))
	gomega.Expect(metrics.ContextCompactions[0].DroppedMessages).To(gomega.BeEquivalentTo(4), fmt.Sprintf("expected context compaction metric, got %#v", metrics.ContextCompactions))
	gomega.Expect(containsStringFold(metrics.ContextCompactions[0].PinnedPaths, "internal/surfaces/tui/app/app.go")).To(gomega.BeTrue(), fmt.Sprintf("expected context compaction metric, got %#v", metrics.ContextCompactions))

	gomega.Expect(metrics.RouteAmbiguities).To(gomega.HaveLen(1), fmt.Sprintf("expected route ambiguity metric, got %#v", metrics.RouteAmbiguities))
	gomega.Expect(metrics.RouteAmbiguities[0].Ambiguous).To(gomega.BeTrue(), fmt.Sprintf("expected route ambiguity metric, got %#v", metrics.RouteAmbiguities))

	gomega.Expect(metrics.VerificationFeedback).To(gomega.HaveLen(1), fmt.Sprintf("expected verification feedback metric, got %#v", metrics.VerificationFeedback))
	gomega.Expect(metrics.VerificationFeedback[0].LearnedRecommendation).NotTo(gomega.BeEquivalentTo(""), fmt.Sprintf("expected verification feedback metric, got %#v", metrics.VerificationFeedback))

	gomega.Expect(metrics.ToolFailureConfidence).To(gomega.HaveLen(1), fmt.Sprintf("expected tool-failure confidence metric, got %#v", metrics.ToolFailureConfidence))
	gomega.Expect(metrics.ToolFailureConfidence[0].ConfidenceImpact).NotTo(gomega.BeEquivalentTo(""), fmt.Sprintf("expected tool-failure confidence metric, got %#v", metrics.ToolFailureConfidence))

	gomega.Expect(metrics.ControlRails).To(gomega.HaveLen(1), fmt.Sprintf("expected control rails metric, got %#v", metrics.ControlRails))
	gomega.Expect(metrics.ControlRails[0].TaskFamily).To(gomega.BeEquivalentTo("review"), fmt.Sprintf("expected control rails metric, got %#v", metrics.ControlRails))
	gomega.Expect(metrics.ControlRails[0].PermissionProfile).To(gomega.BeEquivalentTo("analysis_read_only_then_guarded_writes"), fmt.Sprintf("expected control rails metric, got %#v", metrics.ControlRails))

	gomega.Expect(containsStringFold(metrics.ControlRails[0].TargetRefs, "internal/surfaces/tui/app/app.go")).To(gomega.BeTrue(), fmt.Sprintf("expected control rails target refs, got %#v", metrics.ControlRails[0].TargetRefs))

	gomega.Expect(metrics.ControlRails[0].AgentMode).To(gomega.BeEquivalentTo("plan"), fmt.Sprintf("expected control rails mode and allowed tools, got %#v", metrics.ControlRails[0]))
	gomega.Expect(metrics.ControlRails[0].WorkflowKind).To(gomega.BeEquivalentTo("optimization_refactor"), fmt.Sprintf("expected control rails mode and allowed tools, got %#v", metrics.ControlRails[0]))
	gomega.Expect(metrics.ControlRails[0].WorkflowPhase).To(gomega.BeEquivalentTo("awaiting_approval"), fmt.Sprintf("expected control rails mode and allowed tools, got %#v", metrics.ControlRails[0]))
	gomega.Expect(containsStringFold(metrics.ControlRails[0].AllowedTools, "task")).To(gomega.BeTrue(), fmt.Sprintf("expected control rails mode and allowed tools, got %#v", metrics.ControlRails[0]))
})
var _ = ginkgo.It("scenario round trip file helpers", func() {
	t := ginkgo.GinkgoT()
	path := filepath.Join(t.TempDir(), "scenario.json")
	scenario := Scenario{
		Name:                    "grounding-smoke",
		PromptContains:          []string{"inspect evidence"},
		RequireTools:            []string{"read_file"},
		RequireEvidencePaths:    []string{"internal/runtime/agent/source_grounding.go"},
		RequireChangedFiles:     []string{"internal/runtime/agent/source_grounding_test.go"},
		RequireRunEvents:        []string{"readiness_check"},
		RequireDiscoveryTools:   []string{"context_pack"},
		RequireDiscoveryClasses: []string{"semantic_context"},
	}

	gomega.Expect(WriteScenario(path, scenario)).To(gomega.Succeed(), "write scenario")

	data, err := os.ReadFile(path)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read scenario file: %v", err))

	gomega.Expect(strings.HasSuffix(string(data), "\n")).To(gomega.BeTrue(), fmt.Sprintf("WriteScenario should emit newline-terminated JSON, got %q", string(data)))

	got, err := LoadScenario(path)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("LoadScenario: %v", err))

	gomega.Expect(got.Name).To(gomega.BeEquivalentTo(scenario.Name), fmt.Sprintf("scenario round trip mismatch: %#v", got))
	gomega.Expect(containsStringFold(got.RequireTools, "read_file")).To(gomega.BeTrue(), fmt.Sprintf("scenario round trip mismatch: %#v", got))
	gomega.Expect(containsStringFold(got.RequireEvidencePaths, "internal/runtime/agent/source_grounding.go")).To(gomega.BeTrue(), fmt.Sprintf("scenario round trip mismatch: %#v", got))
})
var _ = ginkgo.It("load records normalizes legacy payload records", func() {
	t := ginkgo.GinkgoT()
	path := filepath.Join(t.TempDir(), "conversation.jsonl")
	lines := []string{
		`{"kind":"message","payload":{"role":"user","content":"hello"},"ts":"2026-06-07T00:00:00Z","session_id":"s1"}`,
		`{"type":"tool_result","tool":"read_file","summary":"Read internal/evidence/eval/eval.go"}`,
	}

	gomega.Expect(os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644)).To(gomega.Succeed(), "write log")

	records, err := LoadRecords(path)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("LoadRecords: %v", err))

	gomega.Expect(records).To(gomega.HaveLen(2), fmt.Sprintf("records length = %d, want 2", len(records)))

	gomega.Expect(records[0].Type).To(gomega.BeEquivalentTo("message"), fmt.Sprintf("legacy record was not normalized: %#v", records[0]))
	gomega.Expect(records[0].Data["type"]).To(gomega.BeEquivalentTo("message"), fmt.Sprintf("legacy record was not normalized: %#v", records[0]))
	gomega.Expect(records[0].Data["kind"]).To(gomega.BeEquivalentTo("message"), fmt.Sprintf("legacy record was not normalized: %#v", records[0]))
	gomega.Expect(records[0].Data["session_id"]).To(gomega.BeEquivalentTo("s1"), fmt.Sprintf("legacy record was not normalized: %#v", records[0]))

	gomega.Expect(records[1].Type).To(gomega.BeEquivalentTo("tool_result"), fmt.Sprintf("typed record should pass through: %#v", records[1]))
	gomega.Expect(records[1].Data["tool"]).To(gomega.BeEquivalentTo("read_file"), fmt.Sprintf("typed record should pass through: %#v", records[1]))
})
var _ = ginkgo.It("analyze includes live contract rollback changed files", func() {
	records := []Record{
		{Type: "message", Data: map[string]any{"role": "user", "content": "shorten the label"}},
		{Type: "idiom.live_contract.rollback", Data: map[string]any{
			"status":               "clean",
			"changed_before_reset": []any{"internal/labels/labels.go"},
		}},
		{Type: "message", Data: map[string]any{"role": "assistant", "content": "Done."}},
	}

	metrics := Analyze(records)
	gomega.Expect(metrics.ChangedFiles).To(gomega.HaveLen(1), fmt.Sprintf("expected rollback changed-file evidence, got %#v", metrics.ChangedFiles))
	gomega.Expect(metrics.ChangedFiles[0]).To(gomega.BeEquivalentTo("internal/labels/labels.go"), fmt.Sprintf("expected rollback changed-file evidence, got %#v", metrics.ChangedFiles))

	gomega.Expect(metrics.RecordTypes["idiom.live_contract.rollback"]).To(gomega.BeEquivalentTo(1), fmt.Sprintf("expected rollback record type count, got %#v", metrics.RecordTypes))

	gomega.Expect(metrics.LiveContractRollbacks).To(gomega.HaveLen(1), fmt.Sprintf("expected rollback snapshot, got %#v", metrics.LiveContractRollbacks))
	gomega.Expect(metrics.LiveContractRollbacks[0].Status).To(gomega.BeEquivalentTo("clean"), fmt.Sprintf("expected rollback snapshot, got %#v", metrics.LiveContractRollbacks))
})
var _ = ginkgo.It("analyze includes successful write result changed files", func() {
	records := []Record{
		{Type: "message", Data: map[string]any{"role": "user", "content": "update the docs"}},
		{Type: "tool_request", Data: map[string]any{"step": 1, "tool": "edit_file", "raw": `{"path":"README.md"}`}},
		{Type: "tool_result", Data: map[string]any{"step": 1, "tool": "edit_file", "path": "README.md", "summary": "Edited README.md lines 10-12", "error": ""}},
		{Type: "message", Data: map[string]any{"role": "assistant", "content": "Done."}},
	}

	metrics := Analyze(records)
	gomega.Expect(metrics.ChangedFiles).To(gomega.HaveLen(1), fmt.Sprintf("expected structured write result to count as changed-file evidence, got %#v", metrics.ChangedFiles))
	gomega.Expect(metrics.ChangedFiles[0]).To(gomega.BeEquivalentTo("README.md"), fmt.Sprintf("expected structured write result to count as changed-file evidence, got %#v", metrics.ChangedFiles))
})
var _ = ginkgo.It("analyze does not trust assistant claimed changed files", func() {
	records := []Record{
		{Type: "message", Data: map[string]any{"role": "assistant", "content": "Changed files:\n- README.md"}},
	}

	metrics := Analyze(records)
	gomega.Expect(metrics.ChangedFiles).To(gomega.BeEmpty(), fmt.Sprintf("assistant-only changed-file claims must not count as mutation evidence, got %#v", metrics.ChangedFiles))
})
var _ = ginkgo.It("analyze rollback evidence overrides write result claims", func() {
	records := []Record{
		{Type: "tool_request", Data: map[string]any{"step": 1, "tool": "write_file", "raw": `{"path":"README.md"}`}},
		{Type: "tool_result", Data: map[string]any{"step": 1, "tool": "write_file", "summary": "Wrote README.md", "error": ""}},
		{Type: "idiom.live_contract.rollback", Data: map[string]any{
			"status":               "clean",
			"changed_before_reset": []any{},
		}},
	}

	metrics := Analyze(records)
	gomega.Expect(metrics.ChangedFiles).To(gomega.BeEmpty(), fmt.Sprintf("empty live rollback diff must override write-result claims, got %#v", metrics.ChangedFiles))
})
var _ = ginkgo.It("analyze ignores noop write result changed files", func() {
	records := []Record{
		{Type: "tool_result", Data: map[string]any{"tool": "edit_file", "path": "README.md", "summary": "No-op edit for README.md; file already matched", "error": ""}},
		{Type: "message", Data: map[string]any{"role": "assistant", "content": "No changes were needed."}},
	}

	metrics := Analyze(records)
	gomega.Expect(metrics.ChangedFiles).To(gomega.BeEmpty(), fmt.Sprintf("no-op write result must not count as changed-file evidence, got %#v", metrics.ChangedFiles))
})
var _ = ginkgo.It("analyze route plan from plan execute writer route decision", func() {
	records := []Record{
		{Type: "idiom.plan_execute.writer_route_decision", Data: map[string]any{
			"task_family":  string(autonomy.TaskFamilyPlanThenExecutePlanner),
			"writer_mode":  "plan_submission",
			"reason_codes": []any{"route.multi_file_plan_execute"},
		}},
	}

	metrics := Analyze(records)
	gomega.Expect(metrics.RoutePlans).To(gomega.HaveLen(1), fmt.Sprintf("expected route plan from writer route event, got %#v", metrics.RoutePlans))

	plan := metrics.RoutePlans[0]
	gomega.Expect(plan.InitialLane).To(gomega.BeEquivalentTo(string(autonomy.TaskFamilyPlanThenExecutePlanner)), fmt.Sprintf("unexpected plan-execute route plan: %#v", plan))
	gomega.Expect(plan.Reason).To(gomega.BeEquivalentTo(string(autonomy.RouteReasonPlanThenExecuteRequest)), fmt.Sprintf("unexpected plan-execute route plan: %#v", plan))
	gomega.Expect(plan.Contract).To(gomega.BeEquivalentTo(string(planartifactcontract.ContractKindImplementationResult)), fmt.Sprintf("unexpected plan-execute route plan: %#v", plan))
})
var _ = ginkgo.It("analyze route plan from plan execute executor writer route decision", func() {
	records := []Record{
		{Type: "idiom.plan_execute.writer_route_decision", Data: map[string]any{
			"task_family": string(autonomy.TaskFamilyPlanThenExecuteExecutor),
			"writer_mode": "executor_dispatch",
		}},
	}

	metrics := Analyze(records)
	gomega.Expect(metrics.RoutePlans).To(gomega.HaveLen(1), fmt.Sprintf("expected executor route plan from writer route event, got %#v", metrics.RoutePlans))

	plan := metrics.RoutePlans[0]
	gomega.Expect(plan.InitialLane).To(gomega.BeEquivalentTo(string(autonomy.TaskFamilyPlanThenExecuteExecutor)), fmt.Sprintf("unexpected plan-execute executor route plan: %#v", plan))
	gomega.Expect(plan.Reason).To(gomega.BeEquivalentTo(string(autonomy.RouteReasonPlanArtifactDispatch)), fmt.Sprintf("unexpected plan-execute executor route plan: %#v", plan))
	gomega.Expect(plan.Contract).To(gomega.BeEquivalentTo(string(autonomy.RuntimeContractExecutorDispatchResult)), fmt.Sprintf("unexpected plan-execute executor route plan: %#v", plan))
})
var _ = ginkgo.It("analyze extracts runner metrics from run events", func() {
	records := []Record{
		{Type: "run_event", Data: map[string]any{"role": "control_rails", "content": `{"task_family":"implementation","permission_profile":"guarded_workspace_write","allowed_tools":["read_file"]}`}},
		{Type: "run_event", Data: map[string]any{"role": "budget_ledger", "content": `{"lane":"implement","used_model_calls":1,"budget_exhausted":false}`}},
		{Type: "run_event", Data: map[string]any{"role": "context_compaction", "content": `{"dropped_messages":2,"retained_messages":4,"strategy":"retain_recent_history"}`}},
	}

	metrics := Analyze(records)

	{
		got := metrics.MessageRoles["control_rails"]
		gomega.Expect(got).To(gomega.BeEquivalentTo(1), fmt.Sprintf("expected control_rails run_event role to be counted, got %d", got))
	}

	gomega.Expect(metrics.ControlRails).To(gomega.HaveLen(1), fmt.Sprintf("expected control rails from run_event, got %#v", metrics.ControlRails))
	gomega.Expect(metrics.ControlRails[0].TaskFamily).To(gomega.BeEquivalentTo("implementation"), fmt.Sprintf("expected control rails from run_event, got %#v", metrics.ControlRails))

	gomega.Expect(metrics.BudgetLedgers).To(gomega.HaveLen(1), fmt.Sprintf("expected budget ledger from run_event, got %#v", metrics.BudgetLedgers))
	gomega.Expect(metrics.BudgetLedgers[0].Lane).To(gomega.BeEquivalentTo("implement"), fmt.Sprintf("expected budget ledger from run_event, got %#v", metrics.BudgetLedgers))

	gomega.Expect(metrics.ContextCompactions).To(gomega.HaveLen(1), fmt.Sprintf("expected context compaction from run_event, got %#v", metrics.ContextCompactions))
	gomega.Expect(metrics.ContextCompactions[0].DroppedMessages).To(gomega.BeEquivalentTo(2), fmt.Sprintf("expected context compaction from run_event, got %#v", metrics.ContextCompactions))
})
var _ = ginkgo.It("evaluate plan execution expectation detects scope drift", func() {
	records := []Record{
		{Type: "idiom.plan_execute.executor_handoff_packet", Data: map[string]any{
			"plan_id":                       "plan-1",
			"handoff_id":                    "handoff-1",
			"approval_scope_id":             "scope-1",
			"approval_status":               "partial",
			"approval_execution_permission": "execute_approved_scope",
			"packet_state":                  "missing",
			"reason":                        "missing_packet_id",
		}},
		{Type: "idiom.plan_execute.plan_scope_drift", Data: map[string]any{
			"path":              "b.txt",
			"reason":            "plan edit path is still pending approval",
			"handoff_id":        "handoff-1",
			"approval_scope_id": "scope-1",
			"packet_state":      "missing",
		}},
	}
	report := Evaluate("", records, Scenario{
		Name: "plan-execution-drift",
		RequirePlanExecution: &PlanExecutionExpectation{
			RequireHandoffID:            true,
			RequireApprovalScopeID:      true,
			RequireDrift:                true,
			RequireDriftReasons:         []string{"plan edit path is still pending approval"},
			RequirePacketStates:         []string{"missing"},
			RequireExecutionPermissions: []string{"execute_approved_scope"},
		},
	})

	gomega.Expect(report.Passed).To(gomega.BeTrue(), fmt.Sprintf("expected plan execution drift assertions to pass, got %#v", report.Findings))

	noDrift := Evaluate("", records, Scenario{
		Name: "plan-execution-no-drift",
		RequirePlanExecution: &PlanExecutionExpectation{
			RequireHandoffID:       true,
			RequireApprovalScopeID: true,
			RequireNoDrift:         true,
		},
	})
	gomega.Expect(noDrift.Passed).To(gomega.BeFalse(), fmt.Sprintf("expected no-drift assertion to fail when drift event is present"))

	gomega.Expect(strings.Join(noDrift.Findings, "\n")).To(gomega.ContainSubstring("plan execution drift was forbidden"), fmt.Sprintf("expected no-drift finding, got %#v", noDrift.Findings))
})
var _ = ginkgo.It("evaluate plan execution expectation requires packet freshness", func() {
	records := []Record{
		{Type: "idiom.plan_execute.executor_handoff_packet", Data: map[string]any{
			"handoff_created":           true,
			"handoff_id":                "handoff-1",
			"packet_state":              "inherited",
			"packet_present":            true,
			"packet_freshness_state":    "fresh",
			"pack_id":                   "feedfacecafebeef",
			"packet_not_used_by_design": false,
		}},
	}
	report := Evaluate("", records, Scenario{
		Name: "plan-execution-fresh-packet",
		RequirePlanExecution: &PlanExecutionExpectation{
			RequireHandoffID:            true,
			RequirePacketPresent:        true,
			RequirePacketFresh:          true,
			RequirePacketFreshnessState: []string{"fresh"},
		},
	})

	gomega.Expect(report.Passed).To(gomega.BeTrue(), fmt.Sprintf("expected fresh packet assertions to pass, got %#v", report.Findings))

	missing := Evaluate("", []Record{{Type: "idiom.plan_execute.executor_handoff_packet", Data: map[string]any{
		"handoff_created":        true,
		"packet_state":           "missing",
		"packet_freshness_state": "missing",
	}}}, Scenario{
		Name: "plan-execution-missing-packet",
		RequirePlanExecution: &PlanExecutionExpectation{
			RequireHandoffID:     true,
			RequirePacketPresent: true,
			RequirePacketFresh:   true,
		},
	})
	gomega.Expect(missing.Passed).To(gomega.BeFalse(), fmt.Sprintf("expected missing packet assertions to fail"))

	joined := strings.Join(missing.Findings, "\n")
	for _, want := range []string{"packet presence was required", "fresh packet was required"} {
		gomega.Expect(joined).To(gomega.ContainSubstring(want), fmt.Sprintf("expected finding %q, got %q", want, joined))
	}

	createdOnly := Evaluate("", []Record{{Type: "idiom.plan_execute.executor_handoff_packet", Data: map[string]any{
		"handoff_created": true,
	}}}, Scenario{
		Name: "plan-execution-handoff-created-without-id",
		RequirePlanExecution: &PlanExecutionExpectation{
			RequireHandoffID:      true,
			RequireHandoffCreated: true,
		},
	})
	gomega.Expect(createdOnly.Passed).To(gomega.BeFalse(), fmt.Sprintf("handoff_created without handoff_id must not satisfy RequireHandoffID"))

	gomega.Expect(strings.Join(createdOnly.Findings, "\n")).To(gomega.ContainSubstring("handoff id was required but not observed"), fmt.Sprintf("expected strict handoff id finding, got %#v", createdOnly.Findings))
})
var _ = ginkgo.It("evaluate plan execute verification events satisfy verification requirement", func() {
	records := []Record{
		{Type: "message", Data: map[string]any{"role": "user", "content": "Use plan-execute to update internal/labels/labels.go, then run `go test ./...`."}},
		{Type: "idiom.plan_execute.verification", Data: map[string]any{
			"command": "go test ./...",
			"status":  "ok",
		}},
		{Type: "idiom.plan_execute.executor_complete", Data: map[string]any{
			"verification_checked": 1,
			"verification_status":  "passed",
		}},
	}

	report := Evaluate("log.jsonl", records, Scenario{
		Name:                "plan-execute-verification",
		RequireVerification: "passed",
	})
	gomega.Expect(report.Passed).To(gomega.BeTrue(), fmt.Sprintf("expected plan-execute verification events to satisfy verification requirement, got %#v", report.Findings))

	gomega.Expect(report.Metrics.VerificationChecks).To(gomega.BeEquivalentTo(1), fmt.Sprintf("expected one passed verification check, got checks=%d passed=%t failed=%t", report.Metrics.VerificationChecks, report.Metrics.VerificationPassed, report.Metrics.VerificationFailed))
	gomega.Expect(report.Metrics.VerificationPassed).To(gomega.BeTrue(), fmt.Sprintf("expected one passed verification check, got checks=%d passed=%t failed=%t", report.Metrics.VerificationChecks, report.Metrics.VerificationPassed, report.Metrics.VerificationFailed))
})
var _ = ginkgo.It("evaluate feature planner atom completion satisfies verification requirement", func() {
	for _, records := range [][]Record{
		{{Type: "idiom.feature_planner.atom_completed", Data: map[string]any{
			"fields": map[string]any{"verification_status": "passed"},
		}}},
		{{Type: "run_event", Data: map[string]any{
			"role":    "idiom.feature_planner.atom_completed",
			"content": `{"fields":{"verification_status":"passed"}}`,
		}}},
	} {
		report := Evaluate("log.jsonl", records, Scenario{
			Name:                "feature-planner-verification",
			RequireVerification: "passed",
		})
		gomega.Expect(report.Passed).To(gomega.BeTrue(), fmt.Sprintf("expected feature-planner atom verification to satisfy requirement, got %#v", report.Findings))
		gomega.Expect(report.Metrics.VerificationChecks).To(gomega.Equal(1))
		gomega.Expect(report.Metrics.VerificationPassed).To(gomega.BeTrue())
	}
})
var _ = ginkgo.It("evaluate plan execute verification failure fails passed requirement", func() {
	records := []Record{
		{Type: "message", Data: map[string]any{"role": "user", "content": "Use plan-execute to update internal/labels/labels.go, then run `go test ./...`."}},
		{Type: "idiom.plan_execute.verification", Data: map[string]any{
			"command": "go test ./...",
			"status":  "failed",
		}},
		{Type: "idiom.plan_execute.executor_complete", Data: map[string]any{
			"verification_checked": 1,
			"verification_status":  "failed",
		}},
	}

	report := Evaluate("log.jsonl", records, Scenario{
		Name:                "plan-execute-verification",
		RequireVerification: "passed",
	})
	gomega.Expect(report.Passed).To(gomega.BeFalse(), fmt.Sprintf("expected failed plan-execute verification to fail passed requirement"))

	gomega.Expect(strings.Join(report.Findings, "\n")).To(gomega.ContainSubstring("verification did not pass"), fmt.Sprintf("expected verification failure finding, got %#v", report.Findings))

	gomega.Expect(report.Metrics.VerificationChecks).To(gomega.BeEquivalentTo(1), fmt.Sprintf("expected one failed verification check, got checks=%d passed=%t failed=%t", report.Metrics.VerificationChecks, report.Metrics.VerificationPassed, report.Metrics.VerificationFailed))
	gomega.Expect(report.Metrics.VerificationFailed).To(gomega.BeTrue(), fmt.Sprintf("expected one failed verification check, got checks=%d passed=%t failed=%t", report.Metrics.VerificationChecks, report.Metrics.VerificationPassed, report.Metrics.VerificationFailed))
	gomega.Expect(report.Metrics.VerificationPassed).To(gomega.BeFalse(), fmt.Sprintf("expected one failed verification check, got checks=%d passed=%t failed=%t", report.Metrics.VerificationChecks, report.Metrics.VerificationPassed, report.Metrics.VerificationFailed))
})
var _ = ginkgo.It("analyze does not treat generic source sanity fallback as passed verification", func() {
	records := []Record{{
		Type: "tool_result",
		Data: map[string]any{
			"agent":   "implement_verify",
			"tool":    "source_sanity",
			"summary": "Source sanity passed for 1 file(s) (1 generic fallback)",
			"output":  "internal/labels/labels.go: generic delimiter sanity passed via generic_source_sanity",
			"error":   "",
		},
	}}

	metrics := Analyze(records)
	gomega.Expect(metrics.VerificationChecks).To(gomega.BeEquivalentTo(1), fmt.Sprintf("VerificationChecks = %d, want 1", metrics.VerificationChecks))

	gomega.Expect(metrics.VerificationPassed).To(gomega.BeFalse(), fmt.Sprintf("generic fallback should be insufficient, not passed/failed: passed=%t failed=%t", metrics.VerificationPassed, metrics.VerificationFailed))
	gomega.Expect(metrics.VerificationFailed).To(gomega.BeFalse(), fmt.Sprintf("generic fallback should be insufficient, not passed/failed: passed=%t failed=%t", metrics.VerificationPassed, metrics.VerificationFailed))

	report := Evaluate("log.jsonl", records, Scenario{
		Name:                "generic-source-sanity-fallback",
		RequireVerification: "passed",
	})
	gomega.Expect(report.Passed).To(gomega.BeFalse(), fmt.Sprintf("generic fallback should not satisfy passed verification requirement"))
})
var _ = ginkgo.It("analyze does not treat patch hygiene phase as passed verification", func() {
	records := []Record{{
		Type: "phase",
		Data: map[string]any{
			"lane":   "implement",
			"phase":  "verify",
			"state":  "hygiene",
			"detail": "git diff --check passed; patch hygiene completed",
		},
	}}

	report := Evaluate("log.jsonl", records, Scenario{
		Name:                "patch-hygiene-only",
		RequireVerification: "passed",
	})
	gomega.Expect(report.Metrics.VerificationPassed).To(gomega.BeFalse(), fmt.Sprintf("patch hygiene phase should not satisfy passed verification, passed=%t report=%t", report.Metrics.VerificationPassed, report.Passed))
	gomega.Expect(report.Passed).To(gomega.BeFalse(), fmt.Sprintf("patch hygiene phase should not satisfy passed verification, passed=%t report=%t", report.Metrics.VerificationPassed, report.Passed))
})
var _ = ginkgo.It("analyze does not treat patch hygiene tool result as passed verification", func() {
	records := []Record{{
		Type: "tool_result",
		Data: map[string]any{
			"agent":   "implement_verify",
			"tool":    "shell",
			"summary": "Ran shell command",
			"output":  "$ git diff --check -- internal/labels/labels.go\n",
			"error":   "",
		},
	}}

	report := Evaluate("log.jsonl", records, Scenario{
		Name:                "patch-hygiene-tool-result",
		RequireVerification: "passed",
	})
	gomega.Expect(report.Metrics.VerificationChecks).To(gomega.BeEquivalentTo(1), fmt.Sprintf("VerificationChecks = %d, want 1", report.Metrics.VerificationChecks))

	gomega.Expect(report.Metrics.VerificationPassed).To(gomega.BeFalse(), fmt.Sprintf("patch hygiene tool result should be insufficient, passed=%t failed=%t report=%t", report.Metrics.VerificationPassed, report.Metrics.VerificationFailed, report.Passed))
	gomega.Expect(report.Metrics.VerificationFailed).To(gomega.BeFalse(), fmt.Sprintf("patch hygiene tool result should be insufficient, passed=%t failed=%t report=%t", report.Metrics.VerificationPassed, report.Metrics.VerificationFailed, report.Passed))
	gomega.Expect(report.Passed).To(gomega.BeFalse(), fmt.Sprintf("patch hygiene tool result should be insufficient, passed=%t failed=%t report=%t", report.Metrics.VerificationPassed, report.Metrics.VerificationFailed, report.Passed))
})
var _ = ginkgo.It("analyze uses lane summary as assistant fallback", func() {
	records := []Record{
		{Type: "message", Data: map[string]any{"role": "user", "content": "prepare a plan"}},
		{Type: "message", Data: map[string]any{"role": "route_plan", "content": "initial=architecture reason=architecture_request contract=architecture_assessment"}},
		{Type: "message", Data: map[string]any{"role": "architecture_summary", "content": "Plan needs a concrete function anchor before editing."}},
		{Type: "message", Data: map[string]any{"role": "user", "content": "begin the plan"}},
		{Type: "message", Data: map[string]any{"role": "route_plan_final", "content": "initial=implement reason=direct_implementation contract=implementation_result"}},
		{Type: "message", Data: map[string]any{"role": "architecture_summary", "content": "Need a concrete line anchor before editing."}},
	}

	metrics := Analyze(records)
	gomega.Expect(metrics.AssistantTurns).To(gomega.BeEquivalentTo(2), fmt.Sprintf("expected lane summaries to count as assistant fallback turns, got %d", metrics.AssistantTurns))

	gomega.Expect(metrics.FinalAssistant).To(gomega.ContainSubstring("concrete line anchor"), fmt.Sprintf("expected final assistant fallback from latest lane summary, got %q", metrics.FinalAssistant))

	gomega.Expect(metrics.RoutePlans).To(gomega.HaveLen(2), fmt.Sprintf("expected route_plan_final to be evaluated, got %#v", metrics.RoutePlans))
	gomega.Expect(metrics.RoutePlans[1].InitialLane).To(gomega.BeEquivalentTo("implement"), fmt.Sprintf("expected route_plan_final to be evaluated, got %#v", metrics.RoutePlans))
})
var _ = ginkgo.It("analyze counts lane summary when later turn has assistant", func() {
	records := []Record{
		{Type: "message", Data: map[string]any{"role": "user", "content": "prepare a plan"}},
		{Type: "message", Data: map[string]any{"role": "architecture_summary", "content": "Plan summary."}},
		{Type: "message", Data: map[string]any{"role": "user", "content": "begin the plan"}},
		{Type: "message", Data: map[string]any{"role": "assistant", "content": "Need a concrete anchor."}},
	}

	metrics := Analyze(records)
	gomega.Expect(metrics.AssistantTurns).To(gomega.BeEquivalentTo(2), fmt.Sprintf("expected lane summary plus final assistant to cover both user turns, got %d", metrics.AssistantTurns))

	gomega.Expect(metrics.FinalAssistant).To(gomega.BeEquivalentTo("Need a concrete anchor."), fmt.Sprintf("expected real assistant to remain final assistant, got %q", metrics.FinalAssistant))
})
var _ = ginkgo.It("evaluate scenario reports failures", func() {
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
		RequireEvidencePaths: []string{"internal/surfaces/tui/app/app.go"},
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
			RequireEditable:     []string{"internal/surfaces/tui/app/app.go"},
			RequireVerification: []string{"go test ./internal/surfaces/tui/app"},
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
			RequirePinnedPaths: []string{"internal/surfaces/tui/app/app.go"},
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
	gomega.Expect(report.Passed).To(gomega.BeFalse(), fmt.Sprintf("expected scenario to fail, got %#v", report))

	joined := strings.Join(report.Findings, "\n")
	for _, expected := range []string{"required tool", "route plan mismatch", "replan recommendation mismatch", "evidence paths do not include", "verification plan mismatch", "required readiness check", "required deepening attempt", "required final answer critic", "required effort policy", "required long-horizon state", "required workflow state", "required budget ledger", "required evidence delta", "required claim confidence", "required context compaction", "required route ambiguity", "required verification feedback", "required tool-failure confidence", "required control rails", "language capabilities do not include", "broad tool requests exceeded", "repeated dead-end requests exceeded", "summary rejections exceeded", "verification did not pass"} {
		gomega.Expect(joined).To(gomega.ContainSubstring(expected), fmt.Sprintf("expected finding containing %q, got %q", expected, joined))
	}
})
var _ = ginkgo.It("analyze counts rejected tool result without request as attempt", func() {
	records := []Record{
		{Type: "message", Data: map[string]any{"role": "user", "content": "edit the label"}},
		{Type: "tool_request", Data: map[string]any{"step": 1, "tool": "read_file", "path": "internal/labels/labels.go"}},
		{Type: "tool_result", Data: map[string]any{"step": 1, "tool": "read_file", "summary": "Read internal/labels/labels.go", "error": ""}},
		{
			Type: "tool_result",
			Data: map[string]any{
				"step":    2,
				"tool":    "code_navigate",
				"summary": "Implementation target write-ready tool rejected",
				"error":   "code_navigate is outside the target-write-ready surface",
			},
		},
		{
			Type: "tool_result",
			Data: map[string]any{
				"step":    3,
				"tool":    "read_file",
				"summary": "Implementation target write-ready tool rejected",
				"error":   "read_file is outside the target-write-ready surface",
			},
		},
	}

	metrics := Analyze(records)
	gomega.Expect(metrics.ToolRequests).To(gomega.BeEquivalentTo(3), fmt.Sprintf("expected rejected unrequested tool_result attempts to count as tool requests, got %d", metrics.ToolRequests))

	gomega.Expect(metrics.Tools["code_navigate"]).To(gomega.BeEquivalentTo(1), fmt.Sprintf("expected rejected code_navigate attempt to count as tool usage, got %#v", metrics.Tools))

	gomega.Expect(metrics.DiscoveryTools["code_navigate"]).To(gomega.BeEquivalentTo(0), fmt.Sprintf("unannotated rejected code_navigate must not infer discovery metadata, tools=%#v classes=%#v", metrics.DiscoveryTools, metrics.DiscoveryClasses))
	gomega.Expect(metrics.DiscoveryClasses["language_symbol"]).To(gomega.BeEquivalentTo(0), fmt.Sprintf("unannotated rejected code_navigate must not infer discovery metadata, tools=%#v classes=%#v", metrics.DiscoveryTools, metrics.DiscoveryClasses))

	gomega.Expect(metrics.ErrorCount).To(gomega.BeEquivalentTo(2), fmt.Sprintf("expected two error events and two unique errors, count=%d errors=%#v", metrics.ErrorCount, metrics.Errors))
	gomega.Expect(metrics.Errors).To(gomega.HaveLen(2), fmt.Sprintf("expected two error events and two unique errors, count=%d errors=%#v", metrics.ErrorCount, metrics.Errors))
})
var _ = ginkgo.It("analyze does not infer discovery profile for unannotated tools", func() {
	records := []Record{
		{Type: "tool_request", Data: map[string]any{"step": 1, "tool": "code_navigate", "path": "internal/labels/labels.go"}},
		{Type: "tool_result", Data: map[string]any{"step": 1, "tool": "code_navigate", "summary": "Read Label", "error": ""}},
		{Type: "tool_request", Data: map[string]any{"step": 2, "tool": "dependency_graph", "path": "."}},
		{Type: "tool_result", Data: map[string]any{"step": 2, "tool": "dependency_graph", "summary": "Graph built", "error": ""}},
	}

	metrics := Analyze(records)
	gomega.Expect(metrics.DiscoveryTools).To(gomega.HaveLen(0), fmt.Sprintf("unannotated tool records must not infer discovery taxonomy, tools=%#v classes=%#v scopes=%#v costs=%#v", metrics.DiscoveryTools, metrics.DiscoveryClasses, metrics.DiscoveryScopes, metrics.DiscoveryCosts))
	gomega.Expect(metrics.DiscoveryClasses).To(gomega.HaveLen(0), fmt.Sprintf("unannotated tool records must not infer discovery taxonomy, tools=%#v classes=%#v scopes=%#v costs=%#v", metrics.DiscoveryTools, metrics.DiscoveryClasses, metrics.DiscoveryScopes, metrics.DiscoveryCosts))
	gomega.Expect(metrics.DiscoveryScopes).To(gomega.HaveLen(0), fmt.Sprintf("unannotated tool records must not infer discovery taxonomy, tools=%#v classes=%#v scopes=%#v costs=%#v", metrics.DiscoveryTools, metrics.DiscoveryClasses, metrics.DiscoveryScopes, metrics.DiscoveryCosts))
	gomega.Expect(metrics.DiscoveryCosts).To(gomega.HaveLen(0), fmt.Sprintf("unannotated tool records must not infer discovery taxonomy, tools=%#v classes=%#v scopes=%#v costs=%#v", metrics.DiscoveryTools, metrics.DiscoveryClasses, metrics.DiscoveryScopes, metrics.DiscoveryCosts))
})
var _ = ginkgo.It("analyze uncounts recoverable post readiness gap attempt", func() {
	records := []Record{
		{
			Type: "tool_request",
			Data: map[string]any{
				"step":            1,
				"tool":            "read_file",
				"path":            "internal/labels/labels.go",
				"discovery_class": "file_read",
				"discovery_scope": "targeted",
				"discovery_cost":  "low",
			},
		},
		{Type: "tool_result", Data: map[string]any{"step": 1, "tool": "read_file", "summary": "Read internal/labels/labels.go", "error": ""}},
		{
			Type: "tool_request",
			Data: map[string]any{
				"step":            2,
				"tool":            "code_navigate",
				"path":            "internal/labels/labels.go",
				"discovery_class": "language_symbol",
				"discovery_scope": "targeted",
				"discovery_cost":  "low",
			},
		},
		{
			Type: "tool_result",
			Data: map[string]any{
				"step":            2,
				"tool":            "code_navigate",
				"summary":         "Post-readiness follow-up missing named gap",
				"error":           "post-readiness exact source probes require a named_gap",
				"discovery_class": "language_symbol",
				"discovery_scope": "targeted",
				"discovery_cost":  "low",
			},
		},
	}

	metrics := Analyze(records)
	gomega.Expect(metrics.ToolRequests).To(gomega.BeEquivalentTo(1), fmt.Sprintf("expected recoverable post-readiness gap attempt to be uncounted, got %d", metrics.ToolRequests))

	gomega.Expect(metrics.Tools["code_navigate"]).To(gomega.BeEquivalentTo(0), fmt.Sprintf("expected recoverable code_navigate attempt to be uncounted, got %#v", metrics.Tools))

	gomega.Expect(metrics.DiscoveryTools["code_navigate"]).To(gomega.BeEquivalentTo(0), fmt.Sprintf("expected recoverable discovery metrics to be uncounted, tools=%#v classes=%#v", metrics.DiscoveryTools, metrics.DiscoveryClasses))
	gomega.Expect(metrics.DiscoveryClasses["language_symbol"]).To(gomega.BeEquivalentTo(0), fmt.Sprintf("expected recoverable discovery metrics to be uncounted, tools=%#v classes=%#v", metrics.DiscoveryTools, metrics.DiscoveryClasses))

	gomega.Expect(metrics.ErrorCount).To(gomega.BeEquivalentTo(0), fmt.Sprintf("expected recoverable post-readiness gap not to count as an error, count=%d errors=%#v", metrics.ErrorCount, metrics.Errors))
	gomega.Expect(metrics.Errors).To(gomega.HaveLen(0), fmt.Sprintf("expected recoverable post-readiness gap not to count as an error, count=%d errors=%#v", metrics.ErrorCount, metrics.Errors))
})
var _ = ginkgo.It("analyze does not count completion gate as tool request", func() {
	records := []Record{
		{Type: "tool_request", Data: map[string]any{"step": 1, "tool": "read_file", "path": "internal/runtime/agent/runner.go"}},
		{Type: "tool_result", Data: map[string]any{"step": 1, "tool": "read_file", "summary": "Read internal/runtime/agent/runner.go", "error": ""}},
		{Type: "tool_result", Data: map[string]any{"step": 2, "tool": "completion_gate", "summary": "Textual tool-call markup rejected as final answer", "error": "the reply contains textual tool-call markup instead of prose"}},
	}

	metrics := Analyze(records)
	gomega.Expect(metrics.ToolRequests).To(gomega.BeEquivalentTo(1), fmt.Sprintf("expected completion_gate feedback not to count as workspace tool request, got %d", metrics.ToolRequests))

	gomega.Expect(metrics.Tools["completion_gate"]).To(gomega.BeEquivalentTo(0), fmt.Sprintf("expected completion_gate not to count as tool usage, got %#v", metrics.Tools))

	gomega.Expect(metrics.CompletionGateRejections).To(gomega.BeEquivalentTo(1), fmt.Sprintf("expected completion gate rejection metric to remain, got %d", metrics.CompletionGateRejections))
})
var _ = ginkgo.It("analyze parses verification plan run event", func() {
	records := []Record{{
		Type: "run_event",
		Data: map[string]any{
			"role":    "verification_plan",
			"content": `{"commands":["bash -n scripts/run-harness.sh"],"confidence":"low","expected_failures":["shell syntax errors"]}`,
		},
	}}

	metrics := Analyze(records)
	gomega.Expect(metrics.VerificationPlans).To(gomega.HaveLen(1), fmt.Sprintf("expected verification plan from run_event, got %#v", metrics.VerificationPlans))

	plan := metrics.VerificationPlans[0]
	gomega.Expect(plan.Confidence).To(gomega.BeEquivalentTo("low"), fmt.Sprintf("unexpected verification plan: %#v", plan))
	gomega.Expect(containsStringFold(plan.Commands, "bash -n scripts/run-harness.sh")).To(gomega.BeTrue(), fmt.Sprintf("unexpected verification plan: %#v", plan))
	gomega.Expect(containsStringFold(plan.ExpectedFailures, "shell syntax errors")).To(gomega.BeTrue(), fmt.Sprintf("unexpected verification plan: %#v", plan))
})
var _ = ginkgo.It("analyze does not uncount prior tool for unrequested recoverable gap", func() {
	records := []Record{
		{Type: "tool_request", Data: map[string]any{"step": 1, "tool": "code_navigate", "path": "internal/labels/labels.go"}},
		{Type: "tool_result", Data: map[string]any{"step": 1, "tool": "code_navigate", "summary": "Read Label", "error": ""}},
		{
			Type: "tool_result",
			Data: map[string]any{
				"step":    2,
				"tool":    "code_navigate",
				"summary": "Post-readiness follow-up missing named gap",
				"error":   "post-readiness exact source probes require a named_gap",
			},
		},
	}

	metrics := Analyze(records)
	gomega.Expect(metrics.ToolRequests).To(gomega.BeEquivalentTo(1), fmt.Sprintf("expected unrequested recoverable gap not to decrement prior valid attempt, requests=%d tools=%#v", metrics.ToolRequests, metrics.Tools))
	gomega.Expect(metrics.Tools["code_navigate"]).To(gomega.BeEquivalentTo(1), fmt.Sprintf("expected unrequested recoverable gap not to decrement prior valid attempt, requests=%d tools=%#v", metrics.ToolRequests, metrics.Tools))

	gomega.Expect(metrics.ErrorCount).To(gomega.BeEquivalentTo(0), fmt.Sprintf("expected unrequested recoverable gap not to count as an error, got %d", metrics.ErrorCount))
})
var _ = ginkgo.It("analyze keeps repeated identical error event count", func() {
	records := []Record{
		{Type: "tool_result", Data: map[string]any{"step": 1, "tool": "read_file", "summary": "Implementation target write-ready tool rejected", "error": "duplicate rejection"}},
		{Type: "tool_result", Data: map[string]any{"step": 2, "tool": "read_file", "summary": "Implementation target write-ready tool rejected", "error": "duplicate rejection"}},
	}

	metrics := Analyze(records)
	gomega.Expect(metrics.ErrorCount).To(gomega.BeEquivalentTo(2), fmt.Sprintf("expected repeated identical errors to count as two events, got %d", metrics.ErrorCount))

	gomega.Expect(metrics.Errors).To(gomega.HaveLen(1), fmt.Sprintf("expected retained error messages to remain deduplicated, got %#v", metrics.Errors))
})
var _ = ginkgo.It("analyze deduplicates mirrored tool and error records by step", func() {
	records := []Record{
		{Type: "tool_request", Data: map[string]any{"step": 1, "tool": "read_file", "action": "read_file:a.go"}},
		{Type: "tool_request", Data: map[string]any{"step": 1, "tool": "read_file", "action": "read_file:a.go"}},
		{Type: "tool_result", Data: map[string]any{"step": 2, "tool": "code_navigate", "summary": "Implementation target write-ready tool rejected", "error": "same rejection"}},
		{Type: "tool_result", Data: map[string]any{"step": 2, "tool": "code_navigate", "summary": "Implementation target write-ready tool rejected", "error": "same rejection"}},
	}

	metrics := Analyze(records)
	gomega.Expect(metrics.ToolRequests).To(gomega.BeEquivalentTo(2), fmt.Sprintf("expected mirrored records to count as two logical attempts, got %d", metrics.ToolRequests))

	gomega.Expect(metrics.Tools["read_file"]).To(gomega.BeEquivalentTo(1), fmt.Sprintf("expected logical tool counts, got %#v", metrics.Tools))
	gomega.Expect(metrics.Tools["code_navigate"]).To(gomega.BeEquivalentTo(1), fmt.Sprintf("expected logical tool counts, got %#v", metrics.Tools))

	gomega.Expect(metrics.ErrorCount).To(gomega.BeEquivalentTo(1), fmt.Sprintf("expected duplicate rejected result records to count as one logical error event, got %d", metrics.ErrorCount))
})
var _ = ginkgo.It("evaluate scenario prompt contains checks last user only", func() {
	records := []Record{
		{Type: "message", Data: map[string]any{"role": "user", "content": "earlier request mentions first plan"}},
		{Type: "message", Data: map[string]any{"role": "assistant", "content": "Question."}},
		{Type: "message", Data: map[string]any{"role": "user", "content": "continue with second plan"}},
	}

	report := Evaluate("log.jsonl", records, Scenario{
		Name:           "latest-turn-prompt",
		PromptContains: []string{"first plan"},
	})
	gomega.Expect(report.Passed).To(gomega.BeFalse(), fmt.Sprintf("expected prompt_contains to check only the latest user turn, passed=%t findings=%#v", report.Passed, report.Findings))
	gomega.Expect(strings.Join(report.Findings, "\n")).To(gomega.ContainSubstring(`last user prompt does not contain "first plan"`), fmt.Sprintf("expected prompt_contains to check only the latest user turn, passed=%t findings=%#v", report.Passed, report.Findings))
})
var _ = ginkgo.It("evaluate scenario prompt digest checks redacted user content", func() {
	digest := "sha256:" + strings.Repeat("a", 64)
	records := []Record{{
		Type: "message",
		Data: map[string]any{
			"role":           "user",
			"content_hash":   digest,
			"content_length": 42,
		},
	}}

	report := Evaluate("redacted-feature-planner.jsonl", records, Scenario{
		Name:         "redacted-feature-planner",
		PromptSHA256: digest,
	})
	gomega.Expect(report.Passed).To(gomega.BeTrue(), fmt.Sprintf("expected digest assertion to pass: %#v", report.Findings))
	gomega.Expect(report.Metrics.LastUser).To(gomega.BeEmpty())
	gomega.Expect(report.Metrics.LastUserSHA256).To(gomega.Equal(digest))
})
var _ = ginkgo.It("evaluate scenario prompt digest survives nested user prompts", func() {
	digest := "sha256:" + strings.Repeat("b", 64)
	records := []Record{
		{Type: "message", Data: map[string]any{"role": "user", "content_hash": digest, "content_length": 42, "session_id": "feature-planner"}},
		{Type: "message", Data: map[string]any{"role": "user", "content": "nested plan-execute contract", "session_id": "nested"}},
	}

	report := Evaluate("nested-feature-planner.jsonl", records, Scenario{
		Name:         "nested feature planner",
		PromptSHA256: digest,
	})

	gomega.Expect(report.Passed).To(gomega.BeTrue(), fmt.Sprintf("expected task prompt digest to remain assertable across nested prompts: %#v", report.Findings))
	gomega.Expect(report.Metrics.UserPromptSHA256s).To(gomega.ContainElement(digest))
})
var _ = ginkgo.It("evaluate scenario prompt history contains checks any user turn", func() {
	records := []Record{
		{Type: "message", Data: map[string]any{"role": "user", "content": "earlier request mentions first plan"}},
		{Type: "message", Data: map[string]any{"role": "assistant", "content": "Question."}},
		{Type: "message", Data: map[string]any{"role": "user", "content": "continue with second plan"}},
	}

	report := Evaluate("log.jsonl", records, Scenario{
		Name:                  "history-prompt",
		PromptHistoryContains: []string{"first plan"},
	})
	gomega.Expect(report.Passed).To(gomega.BeTrue(), fmt.Sprintf("expected prompt_history_contains to check the full user prompt history, got %#v", report.Findings))
})
var _ = ginkgo.It("analyze counts TUI approval input submissions without double-counting mirrored user messages", func() {
	records := []Record{
		{Type: "input_submission", Data: map[string]any{"content": "/feature-planner propose files"}},
		{Type: "message", Data: map[string]any{"role": "user", "content": "/feature-planner propose files"}},
		{Type: "message", Data: map[string]any{"role": "assistant", "content": "Approval required."}},
		{Type: "input_submission", Data: map[string]any{"content": "Is this model-generated?"}},
		{Type: "message", Data: map[string]any{"role": "user", "content": "Is this model-generated?"}},
		{Type: "message", Data: map[string]any{"role": "assistant", "content": "Yes."}},
		{Type: "input_submission", Data: map[string]any{"content": "Approved."}},
		{Type: "message", Data: map[string]any{"role": "assistant", "content": "Plan accepted."}},
	}

	report := Evaluate("tui-approval.jsonl", records, Scenario{
		Name:                        "tui approval",
		PromptHistoryContains:       []string{"Approved."},
		RequireAssistantForEachUser: true,
	})

	gomega.Expect(report.Passed).To(gomega.BeTrue(), fmt.Sprintf("expected direct approval input to participate in prompt history: %#v", report.Findings))
	gomega.Expect(report.Metrics.UserTurns).To(gomega.Equal(3))
	gomega.Expect(report.Metrics.AssistantTurns).To(gomega.Equal(3))
	gomega.Expect(report.Metrics.UserPrompts).To(gomega.Equal([]string{
		"/feature-planner propose files",
		"Is this model-generated?",
		"Approved.",
	}))
	gomega.Expect(report.Metrics.LastUser).To(gomega.Equal("Approved."))
})
var _ = ginkgo.It("evaluate scenario tool assertions include rejected attempts", func() {
	records := []Record{
		{Type: "message", Data: map[string]any{"role": "user", "content": "edit the label"}},
		{Type: "tool_result", Data: map[string]any{"step": 1, "tool": "code_navigate", "summary": "Implementation target write-ready tool rejected", "error": "code_navigate rejected"}},
	}

	report := Evaluate("log.jsonl", records, Scenario{
		Name:         "rejected attempt required",
		RequireTools: []string{"code_navigate"},
	})
	gomega.Expect(report.Passed).To(gomega.BeTrue(), fmt.Sprintf("expected required tool assertion to include rejected attempts, got %#v", report.Findings))

	report = Evaluate("log.jsonl", records, Scenario{
		Name:        "rejected attempt forbidden",
		ForbidTools: []string{"code_navigate"},
	})
	gomega.Expect(report.Passed).To(gomega.BeFalse(), fmt.Sprintf("expected forbidden tool assertion to include rejected attempts, passed=%t findings=%#v", report.Passed, report.Findings))
	gomega.Expect(strings.Join(report.Findings, "\n")).To(gomega.ContainSubstring(`forbidden tool "code_navigate" was used`), fmt.Sprintf("expected forbidden tool assertion to include rejected attempts, passed=%t findings=%#v", report.Passed, report.Findings))
})
var _ = ginkgo.It("evaluate scenario requires rejected tools", func() {
	records := []Record{
		{Type: "message", Data: map[string]any{"role": "user", "content": "run verification"}},
		{Type: "tool_result", Data: map[string]any{"step": 1, "tool": "shell", "summary": "Tool not available in current runtime contract", "error": "shell is not in the current allowed tool surface"}},
	}

	report := Evaluate("log.jsonl", records, Scenario{
		Name:                 "shell-denial",
		RequireRejectedTools: []string{"shell"},
	})
	gomega.Expect(report.Passed).To(gomega.BeTrue(), fmt.Sprintf("expected rejected shell tool assertion to pass, got %#v", report.Findings))

	report = Evaluate("log.jsonl", records, Scenario{
		Name:                 "missing-denial",
		RequireRejectedTools: []string{"edit_file"},
	})
	gomega.Expect(report.Passed).To(gomega.BeFalse(), fmt.Sprintf("expected missing rejected tool assertion to fail, passed=%t findings=%#v", report.Passed, report.Findings))
	gomega.Expect(strings.Join(report.Findings, "\n")).To(gomega.ContainSubstring(`required rejected tool "edit_file" was not observed`), fmt.Sprintf("expected missing rejected tool assertion to fail, passed=%t findings=%#v", report.Passed, report.Findings))
})
var _ = ginkgo.It("evaluate scenario prefers typed rejection disposition over prose", func() {
	records := []Record{
		{Type: "message", Data: map[string]any{"role": "user", "content": "edit the label"}},
		{Type: "tool_result", Data: map[string]any{
			"step":                  1,
			"tool":                  "edit_file",
			"summary":               "Mutation could not be performed",
			"error":                 "scope mismatch",
			"rejection_disposition": "policy_denied",
			"rejected_write_paths":  []any{"internal/labels/labels.go"},
		}},
		{Type: "tool_result", Data: map[string]any{
			"step":                  2,
			"tool":                  "append_file",
			"summary":               "Request was not executed",
			"rejection_disposition": "invalid_request",
		}},
	}

	report := Evaluate("typed-rejection.jsonl", records, Scenario{
		Name:                 "typed-rejection",
		RequireRejectedTools: []string{"edit_file", "append_file"},
	})

	gomega.Expect(report.Passed).To(gomega.BeTrue(), fmt.Sprintf("typed rejections should pass without prose classifiers: %#v", report.Findings))
	gomega.Expect(report.Metrics.RejectedTools).To(gomega.HaveKeyWithValue("edit_file", 1))
	gomega.Expect(report.Metrics.RejectedTools).To(gomega.HaveKeyWithValue("append_file", 1))
	gomega.Expect(report.Metrics.SuccessfulTools).NotTo(gomega.HaveKey("append_file"), "typed rejection must not count as a success when an adapter omits error text")
})

var _ = ginkgo.It("evaluate scenario retains legacy rejection prose fallback", func() {
	records := []Record{{Type: "tool_result", Data: map[string]any{
		"tool":    "shell",
		"summary": "Tool not available in current runtime contract",
		"error":   "shell is not in the current allowed tool surface",
	}}}

	report := Evaluate("legacy-rejection.jsonl", records, Scenario{
		Name:                 "legacy-rejection",
		RequireRejectedTools: []string{"shell"},
	})

	gomega.Expect(report.Passed).To(gomega.BeTrue(), fmt.Sprintf("legacy rejection logs should remain readable: %#v", report.Findings))
})
var _ = ginkgo.It("evaluate scenario requires assistant for each user turn", func() {
	records := []Record{
		{Type: "message", Data: map[string]any{"role": "user", "content": "Can you identify opportunities for us to refactor our repo for long-term maintainability"}},
		{Type: "message", Data: map[string]any{"role": "route_plan", "content": "initial=architecture reason=architecture_request contract=architecture_assessment thoroughness=medium"}},
		{Type: "message", Data: map[string]any{"role": "architecture_summary", "content": "Architecture summary: internal/capabilities/workspace/manager.go is a hotspot."}},
		{Type: "message", Data: map[string]any{"role": "user", "content": "Can you prepare a refactor plan for this file?"}},
		{Type: "message", Data: map[string]any{"role": "route_plan", "content": "initial=architecture reason=architecture_request contract=architecture_assessment thoroughness=medium"}},
	}

	report := Evaluate("log.jsonl", records, Scenario{
		Name:                        "conversation coherence",
		RequireAssistantForEachUser: true,
	})
	gomega.Expect(report.Passed).To(gomega.BeFalse(), fmt.Sprintf("expected missing assistant-turn coverage to fail, got %#v", report))

	gomega.Expect(strings.Join(report.Findings, "\n")).To(gomega.ContainSubstring("assistant turns fewer than user turns"), fmt.Sprintf("expected assistant-turn finding, got %#v", report.Findings))
})
var _ = ginkgo.It("evaluate scenario accepts required route plan", func() {
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
	gomega.Expect(report.Passed).To(gomega.BeTrue(), fmt.Sprintf("expected scenario to pass, got %#v", report))
})
var _ = ginkgo.It("evaluate scenario require run events passes when roles present", func() {
	records := []Record{
		{Type: "message", Data: map[string]any{"role": "user", "content": "rename the enum"}},
		{Type: "message", Data: map[string]any{"role": "plan_then_execute_write_guard", "content": "plan-then-execute planner phase: write tool edit_file redirected to submit_plan"}},
		{Type: "message", Data: map[string]any{"role": "plan_then_execute_short_circuit", "content": "submit_plan succeeded; synthesizing final answer from plan artifact instead of spending a second model turn"}},
		{Type: "message", Data: map[string]any{"role": "assistant", "content": "Plan submitted with 3 edit(s); the executor phase will dispatch them."}},
	}

	report := Evaluate("log.jsonl", records, Scenario{
		Name: "plan-execute write guard fired",
		RequireRunEvents: []string{
			"plan_then_execute_write_guard",
			"plan_then_execute_short_circuit",
			"  ", // blank entries are tolerated and ignored
		},
	})
	gomega.Expect(report.Passed).To(gomega.BeTrue(), fmt.Sprintf("expected scenario to pass when both required run events appear, got findings %v", report.Findings))

	{
		got := report.Metrics.MessageRoles["plan_then_execute_write_guard"]
		gomega.Expect(got).To(gomega.BeEquivalentTo(1), fmt.Sprintf("expected MessageRoles to count plan_then_execute_write_guard once, got %d", got))
	}
})
var _ = ginkgo.It("evaluate scenario require run events fails when role missing", func() {
	records := []Record{
		{Type: "message", Data: map[string]any{"role": "user", "content": "rename the enum"}},
		{Type: "message", Data: map[string]any{"role": "assistant", "content": "Plan submitted."}},
	}

	report := Evaluate("log.jsonl", records, Scenario{
		Name:             "plan-execute write guard expected but absent",
		RequireRunEvents: []string{"plan_then_execute_write_guard"},
	})
	gomega.Expect(report.Passed).To(gomega.BeFalse(), fmt.Sprintf("expected scenario to fail when required run event is absent, got pass"))

	var sawFinding bool

	for _, finding := range report.Findings {
		if strings.Contains(finding, `required run event "plan_then_execute_write_guard" was not observed`) {
			sawFinding = true
		}
	}

	gomega.Expect(sawFinding).To(gomega.BeTrue(), fmt.Sprintf("expected finding pointing at the missing run event, got %v", report.Findings))
})
var

// TestEvaluateScenarioRequireRunEventsMatchesOrchestratorEventTypes covers the
// second match source: orchestrator events emitted to stdout JSONL with the
// event name as the top-level type (e.g. idiom.plan_execute.preflight_failed)
// instead of role on a type:"message" record. RequireRunEvents must match
// either source so a single field can cover runner gates and orchestrator
// gates symmetrically.
_ = ginkgo.It("evaluate scenario require run events matches orchestrator event types", func() {
	records := []Record{
		{Type: "message", Data: map[string]any{"role": "user", "content": "execute the plan"}},
		{Type: "idiom.plan_execute.plan_artifact_loaded", Data: map[string]any{"path": ".idiom/plans/x.plan.json", "edits": 3}},
		{Type: "idiom.plan_execute.preflight_failed", Data: map[string]any{"edit_index": 1, "path": "internal/foo.go", "reason": "old_text mismatch"}},
		{Type: "message", Data: map[string]any{"role": "assistant", "content": "Preflight failed; aborted before any edit."}},
	}

	report := Evaluate("log.jsonl", records, Scenario{
		Name: "orchestrator preflight failure detected",
		RequireRunEvents: []string{
			"idiom.plan_execute.plan_artifact_loaded",
			"idiom.plan_execute.preflight_failed",
		},
	})
	gomega.Expect(report.Passed).To(gomega.BeTrue(), fmt.Sprintf("expected scenario to pass when orchestrator events appear as top-level record types, got findings %v", report.Findings))

	{
		got := report.Metrics.RecordTypes["idiom.plan_execute.preflight_failed"]
		gomega.Expect(got).To(gomega.BeEquivalentTo(1), fmt.Sprintf("expected RecordTypes to count preflight_failed once, got %d", got))
	}
})
var _ = ginkgo.It("evaluate scenario require run events accepts feature planner source satisfied completion alias", func() {
	records := []Record{
		{Type: "message", Data: map[string]any{"role": "user", "content": "dispatch the atom"}},
		{Type: "idiom.feature_planner.atom_source_satisfied", Data: map[string]any{"atom_id": "atom-1", "outcome": "source_satisfied"}},
		{Type: "message", Data: map[string]any{"role": "assistant", "content": "feature-planner: complete"}},
	}

	report := Evaluate("log.jsonl", records, Scenario{
		Name:             "source-satisfied atom completes feature-planner atom",
		RequireRunEvents: []string{"idiom.feature_planner.atom_completed"},
	})
	gomega.Expect(report.Passed).To(gomega.BeTrue(), fmt.Sprintf("expected source-satisfied atom event to satisfy atom_completed requirement, got findings %v", report.Findings))
})
var _ = ginkgo.It("evaluate scenario require run events matches nested autonomy event types", func() {
	records := []Record{
		{Type: "message", Data: map[string]any{"role": "user", "content": "read the denied file"}},
		{
			Type: "run_event",
			Data: map[string]any{
				"role":    "autonomy_event",
				"content": `{"schema_version":"idiom.autonomy.event.v1","event_id":"event-1","type":"tool_policy_result","effect":"observe_only","occurred_at":"2026-05-25T14:00:00Z","sequence":1,"contract_id":"contract-1","correlation":{"lane":"main","task_family":"conversation","runtime_contract":"direct_answer","permission_profile":"read_only_workspace"},"policy":{"tool_name":"read_file","action_class":"read","action_key":"read:read_file:bounded_read:.idiom/deny-read-test.txt","transport":"textual_tool_call","capability":"bounded_read","side_effect":"read_only","arguments_digest":"sha256:0000000000000000000000000000000000000000000000000000000000000000","decision":"deny","reason_code":"permission_policy","result":"denied","error_class":"permission_policy","source":"runner_permission_policy"}}`,
			},
		},
		{Type: "message", Data: map[string]any{"role": "assistant", "content": "The read was denied."}},
	}

	report := Evaluate("log.jsonl", records, Scenario{
		Name:             "autonomy tool policy result observed",
		RequireRunEvents: []string{"tool_policy_result"},
	})
	gomega.Expect(report.Passed).To(gomega.BeTrue(), fmt.Sprintf("expected scenario to pass when autonomy event payload type appears, got findings %v", report.Findings))

	{
		got := report.Metrics.RecordTypes["tool_policy_result"]
		gomega.Expect(got).To(gomega.BeEquivalentTo(1), fmt.Sprintf("expected RecordTypes to count nested tool_policy_result once, got %d", got))
	}
})
var _ = ginkgo.It("evaluate scenario accepts frontier controller telemetry", func() {
	records := []Record{
		{Type: "message", Data: map[string]any{"role": "user", "content": "review routing thoroughly"}},
		{Type: "message", Data: map[string]any{"role": "budget_ledger", "content": `{"lane":"review","used_model_calls":3,"used_deepening_passes":2,"max_deepening_passes":2,"used_tool_results":4,"new_evidence_paths":1,"total_evidence_paths":2,"stop_reason":"fallback_after_exhausted_readiness_attempts","budget_exhausted":true,"user_visible_uncertainty":"state that bounded readiness budget was exhausted and identify unresolved gaps"}`}},
		{Type: "message", Data: map[string]any{"role": "evidence_delta", "content": `{"lane":"review","attempt":2,"new_evidence_paths":["internal/evidence/eval/eval.go"],"total_paths":2,"tool_results":2,"successful_tools":1,"failed_tools":1,"marginal_value":"medium"}`}},
		{Type: "message", Data: map[string]any{"role": "claim_confidence", "content": `{"lane":"review","confidence":"low","supported_claims":0,"evidence_paths":["internal/surfaces/tui/app/app.go","internal/evidence/eval/eval.go"],"unresolved_uncertainty":["summary does not satisfy lane contract"],"calibration_basis":"contract acceptance plus grounded evidence path coverage"}`}},
		{Type: "message", Data: map[string]any{"role": "context_compaction", "content": `{"dropped_messages":6,"retained_messages":8,"max_history":8,"pinned_paths":["internal/surfaces/tui/app/app.go","internal/evidence/eval/eval.go"],"pinned_memory_kind":"investigation","strategy":"retain_recent_history_with_pinned_task_memory_and_refactor_targets"}`}},
		{Type: "message", Data: map[string]any{"role": "route_ambiguity", "content": `{"selected_lane":"review","reason":"review_request","signals":["review","implement"],"ambiguous":true,"tie_break":"ordered host route precedence selected review"}`}},
		{Type: "message", Data: map[string]any{"role": "verification_feedback", "content": `{"risk_level":"high","coverage":"targeted package verification","confidence":"medium","commands":["go test ./internal/surfaces/tui/app"],"failed_commands":["go test ./internal/surfaces/tui/app"],"learned_recommendation":"repair failing checks before widening scope"}`}},
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
			RequirePinnedPaths:  []string{"internal/surfaces/tui/app/app.go", "internal/evidence/eval/eval.go"},
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
			RequireFailedCommands:  []string{"go test ./internal/surfaces/tui/app"},
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

	gomega.Expect(report.Passed).To(gomega.BeTrue(), fmt.Sprintf("expected frontier telemetry scenario to pass, got %#v", report))
})
var _ = ginkgo.It("build suggested scenario reflects observed metrics", func() {
	records := []Record{
		{Type: "message", Data: map[string]any{"role": "user", "content": "remove the banner text"}},
		{Type: "message", Data: map[string]any{"role": "route_plan", "content": "initial=locate followup=implement reason=locate_request contract=locate_targets"}},
		{Type: "message", Data: map[string]any{"role": "replan_recommendation", "content": "locate -> review: locate produced review-shaped evidence"}},
		{Type: "message", Data: map[string]any{"role": "locate_bootstrap", "content": "Language capability registry:\n- go tools: go_overview, go_find_symbol | signals: go.mod, internal/surfaces/tui/app/app.go"}},
		{Type: "message", Data: map[string]any{"role": "verification_plan", "content": `{"commands":["go build ./...","go test -vet=off ./..."],"uses_full_suite":true,"coverage":"full-suite escalation","confidence":"high","risk_level":"high","expected_failures":["compile errors","package test regressions"]}`}},
		{Type: "message", Data: map[string]any{"role": "readiness_check", "content": `{"lane":"locate","ready":false,"confidence":"low","action":"retry","reason":"response explicitly reports insufficient evidence","evidence_paths":["internal/surfaces/tui/app/app.go"]}`}},
		{Type: "message", Data: map[string]any{"role": "deepening_attempt", "content": `{"lane":"locate","attempt":1,"attempted":true,"exhausted":false,"action":"retry_with_evidence","reason":"readiness gate failed; retry with deeper evidence","evidence_paths":["internal/surfaces/tui/app/app.go"]}`}},
		{Type: "message", Data: map[string]any{"role": "final_answer_critic", "content": `{"contract":"implementation_result","passed":false,"action":"repair","repaired":true,"findings":["missing verification section"]}`}},
		{Type: "message", Data: map[string]any{"role": "effort_policy", "content": `{"lane":"locate","level":"medium","analysis_tool_budget":6,"implement_tool_budget":6,"confidence_floor":"medium","budget_exhaustion_mode":"answer_with_explicit_uncertainty_or_fallback"}`}},
		{Type: "message", Data: map[string]any{"role": "long_horizon_state", "content": `{"action":"resume","source_lane":"architecture","stage":2,"focus_path":"internal/capabilities/workspace/manager.go","remaining":["internal/runtime/agent/runner.go"],"next_step":"Continue with runner extraction.","resumable":true}`}},
		{Type: "message", Data: map[string]any{"role": "budget_ledger", "content": `{"lane":"locate","used_model_calls":2,"used_deepening_passes":1,"max_deepening_passes":2,"used_tool_results":1,"new_evidence_paths":1,"total_evidence_paths":1,"stop_reason":"fallback_after_exhausted_readiness_attempts","budget_exhausted":true,"user_visible_uncertainty":"state that bounded readiness budget was exhausted"}`}},
		{Type: "message", Data: map[string]any{"role": "evidence_delta", "content": `{"lane":"locate","attempt":1,"new_evidence_paths":["internal/surfaces/tui/app/app.go"],"total_paths":1,"tool_results":1,"successful_tools":1,"failed_tools":0,"marginal_value":"medium"}`}},
		{Type: "message", Data: map[string]any{"role": "claim_confidence", "content": `{"lane":"locate","confidence":"low","supported_claims":0,"evidence_paths":["internal/surfaces/tui/app/app.go"],"unresolved_uncertainty":["response explicitly reports insufficient evidence"],"calibration_basis":"contract acceptance plus grounded evidence path coverage"}`}},
		{Type: "message", Data: map[string]any{"role": "context_compaction", "content": `{"dropped_messages":3,"retained_messages":5,"max_history":5,"pinned_paths":["internal/surfaces/tui/app/app.go"],"pinned_memory_kind":"partial_repair","strategy":"retain_recent_history_with_pinned_task_memory_and_refactor_targets"}`}},
		{Type: "message", Data: map[string]any{"role": "route_ambiguity", "content": `{"selected_lane":"locate","reason":"locate_request","signals":["locate","implement"],"ambiguous":true,"tie_break":"ordered host route precedence selected locate"}`}},
		{Type: "message", Data: map[string]any{"role": "verification_feedback", "content": `{"risk_level":"high","coverage":"full-suite escalation","confidence":"high","commands":["go test ./..."],"failed_commands":["go test ./..."],"failure_classes":["go_unused_variable"],"learned_recommendation":"record full-suite success for similar high-risk surfaces"}`}},
		{Type: "message", Data: map[string]any{"role": "tool_failure_confidence", "content": `{"lane":"locate","tool":"read_file","action":"alternate_evidence_required","confidence_impact":"degrade_until_alternate_evidence_covers_gap","reason":"read_file failed during deepening"}`}},
		{Type: "message", Data: map[string]any{"role": "control_rails", "content": `{"lane":"locate","followup_lane":"implement","contract":"locate_targets","task_family":"locate","permission_profile":"analysis_read_only_then_guarded_writes","required_evidence":["exact_workspace_path","line_or_snippet","target_match_reason"],"required_tools":["search_content","read_file","implementation_handoff"],"forbidden_tools":["edit_file","write_file","append_file","mkdir","mutating_shell"],"readiness_gate":["answer_contract_satisfied","required_evidence_present","target_packet_or_changed_files_present","verification_contract_resolved"],"search_policy":["literal_search_first","fallback_pattern_search","read_best_candidate"],"verification_contract":"tests_passed_or_failed_or_skipped_with_blocker","stop_conditions":["required_evidence_satisfied","implementation_verified_or_failure_reported"],"final_critic":["answers_actual_user_request","required_evidence_cited","exact_target_or_no_match"],"failure_to_eval":false}`}},
		{Type: "message", Data: map[string]any{"role": "explore_summary", "content": "explore summary rejected: failed grounded explanation contract"}},
		{Type: "tool_request", Data: map[string]any{"step": 1, "tool": "search_content", "broad": false}},
		{Type: "tool_result", Data: map[string]any{"step": 1, "summary": "Found 1 matching line at internal/surfaces/tui/app/app.go:42", "error": ""}},
		{Type: "tool_request", Data: map[string]any{"step": 2, "tool": "edit_file", "path": "internal/surfaces/tui/app/app.go", "broad": false}},
		{Type: "tool_result", Data: map[string]any{"step": 2, "tool": "edit_file", "path": "internal/surfaces/tui/app/app.go", "summary": "Edited internal/surfaces/tui/app/app.go lines 42-43", "error": ""}},
		{Type: "phase", Data: map[string]any{"lane": "locate", "phase": "complete", "state": "done"}},
		{Type: "phase", Data: map[string]any{"lane": "implement", "phase": "verify", "state": "done"}},
		{Type: "message", Data: map[string]any{"role": "assistant", "content": "Changed files:\n- internal/surfaces/tui/app/app.go"}},
	}

	scenario := BuildSuggestedScenario("banner-remove", records)
	gomega.Expect(scenario.Name).To(gomega.BeEquivalentTo("banner-remove"), fmt.Sprintf("unexpected scenario name: %q", scenario.Name))

	gomega.Expect(scenario.PromptContains).To(gomega.HaveLen(1), fmt.Sprintf("unexpected prompt matchers: %#v", scenario.PromptContains))
	gomega.Expect(scenario.PromptContains[0]).To(gomega.BeEquivalentTo("remove the banner text"), fmt.Sprintf("unexpected prompt matchers: %#v", scenario.PromptContains))

	gomega.Expect(scenario.RequireVerification).To(gomega.BeEquivalentTo("passed"), fmt.Sprintf("expected passed verification, got %q", scenario.RequireVerification))

	gomega.Expect(containsStringFold(scenario.RequireTools, "search_content")).To(gomega.BeTrue(), fmt.Sprintf("expected successful narrowing tool in scenario: %#v", scenario.RequireTools))

	gomega.Expect(containsStringFold(scenario.RequireChangedFiles, "internal/surfaces/tui/app/app.go")).To(gomega.BeTrue(), fmt.Sprintf("expected changed file in scenario: %#v", scenario.RequireChangedFiles))

	gomega.Expect(scenario.RequireRoutePlan).NotTo(gomega.BeNil(), fmt.Sprintf("expected suggested route plan in scenario, got %#v", scenario.RequireRoutePlan))
	gomega.Expect(scenario.RequireRoutePlan.InitialLane).To(gomega.BeEquivalentTo("locate"), fmt.Sprintf("expected suggested route plan in scenario, got %#v", scenario.RequireRoutePlan))
	gomega.Expect(scenario.RequireRoutePlan.Contract).To(gomega.BeEquivalentTo("locate_targets"), fmt.Sprintf("expected suggested route plan in scenario, got %#v", scenario.RequireRoutePlan))

	gomega.Expect(scenario.RequireReplan).NotTo(gomega.BeNil(), fmt.Sprintf("expected suggested replan in scenario, got %#v", scenario.RequireReplan))
	gomega.Expect(scenario.RequireReplan.FromLane).To(gomega.BeEquivalentTo("locate"), fmt.Sprintf("expected suggested replan in scenario, got %#v", scenario.RequireReplan))
	gomega.Expect(scenario.RequireReplan.ToLane).To(gomega.BeEquivalentTo("review"), fmt.Sprintf("expected suggested replan in scenario, got %#v", scenario.RequireReplan))

	gomega.Expect(containsStringFold(scenario.RequireEvidencePaths, "internal/surfaces/tui/app/app.go")).To(gomega.BeTrue(), fmt.Sprintf("expected suggested evidence path in scenario, got %#v", scenario.RequireEvidencePaths))

	gomega.Expect(containsLanguageCapabilityExpectation(scenario.RequireLanguageCapabilities, "go")).To(gomega.BeTrue(), fmt.Sprintf("expected suggested Go language capability, got %#v", scenario.RequireLanguageCapabilities))

	gomega.Expect(scenario.RequireVerificationPlan).NotTo(gomega.BeNil(), fmt.Sprintf("expected suggested verification plan, got %#v", scenario.RequireVerificationPlan))
	gomega.Expect(scenario.RequireVerificationPlan.RiskLevel).To(gomega.BeEquivalentTo("high"), fmt.Sprintf("expected suggested verification plan, got %#v", scenario.RequireVerificationPlan))
	gomega.Expect(scenario.RequireVerificationPlan.UsesFullSuite).NotTo(gomega.BeNil(), fmt.Sprintf("expected suggested verification plan, got %#v", scenario.RequireVerificationPlan))
	gomega.Expect(*scenario.RequireVerificationPlan.UsesFullSuite).To(gomega.BeTrue(), fmt.Sprintf("expected suggested verification plan, got %#v", scenario.RequireVerificationPlan))

	gomega.Expect(scenario.RequireReadiness).NotTo(gomega.BeNil(), fmt.Sprintf("expected suggested readiness check, got %#v", scenario.RequireReadiness))
	gomega.Expect(scenario.RequireReadiness.Ready).NotTo(gomega.BeNil(), fmt.Sprintf("expected suggested readiness check, got %#v", scenario.RequireReadiness))
	gomega.Expect(*scenario.RequireReadiness.Ready).To(gomega.BeFalse(), fmt.Sprintf("expected suggested readiness check, got %#v", scenario.RequireReadiness))
	gomega.Expect(scenario.RequireReadiness.Action).To(gomega.BeEquivalentTo("retry"), fmt.Sprintf("expected suggested readiness check, got %#v", scenario.RequireReadiness))

	gomega.Expect(scenario.RequireDeepening).NotTo(gomega.BeNil(), fmt.Sprintf("expected suggested deepening attempt, got %#v", scenario.RequireDeepening))
	gomega.Expect(scenario.RequireDeepening.Attempted).NotTo(gomega.BeNil(), fmt.Sprintf("expected suggested deepening attempt, got %#v", scenario.RequireDeepening))
	gomega.Expect(*scenario.RequireDeepening.Attempted).To(gomega.BeTrue(), fmt.Sprintf("expected suggested deepening attempt, got %#v", scenario.RequireDeepening))
	gomega.Expect(scenario.RequireDeepening.Action).To(gomega.BeEquivalentTo("retry_with_evidence"), fmt.Sprintf("expected suggested deepening attempt, got %#v", scenario.RequireDeepening))

	gomega.Expect(scenario.RequireFinalAnswerCritic).NotTo(gomega.BeNil(), fmt.Sprintf("expected suggested final answer critic, got %#v", scenario.RequireFinalAnswerCritic))
	gomega.Expect(scenario.RequireFinalAnswerCritic.Repaired).NotTo(gomega.BeNil(), fmt.Sprintf("expected suggested final answer critic, got %#v", scenario.RequireFinalAnswerCritic))
	gomega.Expect(*scenario.RequireFinalAnswerCritic.Repaired).To(gomega.BeTrue(), fmt.Sprintf("expected suggested final answer critic, got %#v", scenario.RequireFinalAnswerCritic))
	gomega.Expect(scenario.RequireFinalAnswerCritic.Action).To(gomega.BeEquivalentTo("repair"), fmt.Sprintf("expected suggested final answer critic, got %#v", scenario.RequireFinalAnswerCritic))

	gomega.Expect(scenario.RequireEffortPolicy).NotTo(gomega.BeNil(), fmt.Sprintf("expected suggested effort policy, got %#v", scenario.RequireEffortPolicy))
	gomega.Expect(scenario.RequireEffortPolicy.Level).To(gomega.BeEquivalentTo("medium"), fmt.Sprintf("expected suggested effort policy, got %#v", scenario.RequireEffortPolicy))
	gomega.Expect(scenario.RequireEffortPolicy.MinAnalysisToolBudget).NotTo(gomega.BeNil(), fmt.Sprintf("expected suggested effort policy, got %#v", scenario.RequireEffortPolicy))
	gomega.Expect(*scenario.RequireEffortPolicy.MinAnalysisToolBudget).To(gomega.BeEquivalentTo(6), fmt.Sprintf("expected suggested effort policy, got %#v", scenario.RequireEffortPolicy))

	gomega.Expect(scenario.RequireLongHorizonState).NotTo(gomega.BeNil(), fmt.Sprintf("expected suggested long-horizon state, got %#v", scenario.RequireLongHorizonState))
	gomega.Expect(scenario.RequireLongHorizonState.Action).To(gomega.BeEquivalentTo("resume"), fmt.Sprintf("expected suggested long-horizon state, got %#v", scenario.RequireLongHorizonState))
	gomega.Expect(scenario.RequireLongHorizonState.Stage).NotTo(gomega.BeNil(), fmt.Sprintf("expected suggested long-horizon state, got %#v", scenario.RequireLongHorizonState))
	gomega.Expect(*scenario.RequireLongHorizonState.Stage).To(gomega.BeEquivalentTo(2), fmt.Sprintf("expected suggested long-horizon state, got %#v", scenario.RequireLongHorizonState))

	gomega.Expect(scenario.RequireBudgetLedger).NotTo(gomega.BeNil(), fmt.Sprintf("expected suggested budget ledger, got %#v", scenario.RequireBudgetLedger))
	gomega.Expect(scenario.RequireBudgetLedger.BudgetExhausted).NotTo(gomega.BeNil(), fmt.Sprintf("expected suggested budget ledger, got %#v", scenario.RequireBudgetLedger))
	gomega.Expect(*scenario.RequireBudgetLedger.BudgetExhausted).To(gomega.BeTrue(), fmt.Sprintf("expected suggested budget ledger, got %#v", scenario.RequireBudgetLedger))
	gomega.Expect(scenario.RequireBudgetLedger.MinModelCalls).NotTo(gomega.BeNil(), fmt.Sprintf("expected suggested budget ledger, got %#v", scenario.RequireBudgetLedger))
	gomega.Expect(*scenario.RequireBudgetLedger.MinModelCalls).To(gomega.BeEquivalentTo(2), fmt.Sprintf("expected suggested budget ledger, got %#v", scenario.RequireBudgetLedger))

	gomega.Expect(scenario.RequireEvidenceDelta).NotTo(gomega.BeNil(), fmt.Sprintf("expected suggested evidence delta, got %#v", scenario.RequireEvidenceDelta))
	gomega.Expect(scenario.RequireEvidenceDelta.MinNewPaths).NotTo(gomega.BeNil(), fmt.Sprintf("expected suggested evidence delta, got %#v", scenario.RequireEvidenceDelta))
	gomega.Expect(*scenario.RequireEvidenceDelta.MinNewPaths).To(gomega.BeEquivalentTo(1), fmt.Sprintf("expected suggested evidence delta, got %#v", scenario.RequireEvidenceDelta))
	gomega.Expect(scenario.RequireEvidenceDelta.MaxFailedTools).NotTo(gomega.BeNil(), fmt.Sprintf("expected suggested evidence delta, got %#v", scenario.RequireEvidenceDelta))
	gomega.Expect(*scenario.RequireEvidenceDelta.MaxFailedTools).To(gomega.BeEquivalentTo(0), fmt.Sprintf("expected suggested evidence delta, got %#v", scenario.RequireEvidenceDelta))

	gomega.Expect(scenario.RequireClaimConfidence).NotTo(gomega.BeNil(), fmt.Sprintf("expected suggested claim confidence, got %#v", scenario.RequireClaimConfidence))
	gomega.Expect(scenario.RequireClaimConfidence.MaxUnresolved).NotTo(gomega.BeNil(), fmt.Sprintf("expected suggested claim confidence, got %#v", scenario.RequireClaimConfidence))
	gomega.Expect(*scenario.RequireClaimConfidence.MaxUnresolved).To(gomega.BeEquivalentTo(1), fmt.Sprintf("expected suggested claim confidence, got %#v", scenario.RequireClaimConfidence))
	gomega.Expect(scenario.RequireClaimConfidence.CalibrationBasis).NotTo(gomega.BeEquivalentTo(""), fmt.Sprintf("expected suggested claim confidence, got %#v", scenario.RequireClaimConfidence))

	gomega.Expect(scenario.RequireContextCompaction).NotTo(gomega.BeNil(), fmt.Sprintf("expected suggested context compaction, got %#v", scenario.RequireContextCompaction))
	gomega.Expect(scenario.RequireContextCompaction.MinDroppedMessages).NotTo(gomega.BeNil(), fmt.Sprintf("expected suggested context compaction, got %#v", scenario.RequireContextCompaction))
	gomega.Expect(*scenario.RequireContextCompaction.MinDroppedMessages).To(gomega.BeEquivalentTo(3), fmt.Sprintf("expected suggested context compaction, got %#v", scenario.RequireContextCompaction))
	gomega.Expect(containsStringFold(scenario.RequireContextCompaction.RequirePinnedPaths, "internal/surfaces/tui/app/app.go")).To(gomega.BeTrue(), fmt.Sprintf("expected suggested context compaction, got %#v", scenario.RequireContextCompaction))

	gomega.Expect(scenario.RequireRouteAmbiguity).NotTo(gomega.BeNil(), fmt.Sprintf("expected suggested route ambiguity, got %#v", scenario.RequireRouteAmbiguity))
	gomega.Expect(scenario.RequireRouteAmbiguity.Ambiguous).NotTo(gomega.BeNil(), fmt.Sprintf("expected suggested route ambiguity, got %#v", scenario.RequireRouteAmbiguity))
	gomega.Expect(*scenario.RequireRouteAmbiguity.Ambiguous).To(gomega.BeTrue(), fmt.Sprintf("expected suggested route ambiguity, got %#v", scenario.RequireRouteAmbiguity))
	gomega.Expect(containsStringFold(scenario.RequireRouteAmbiguity.RequireSignals, "implement")).To(gomega.BeTrue(), fmt.Sprintf("expected suggested route ambiguity, got %#v", scenario.RequireRouteAmbiguity))

	gomega.Expect(scenario.RequireVerificationFeedback).NotTo(gomega.BeNil(), fmt.Sprintf("expected suggested verification feedback, got %#v", scenario.RequireVerificationFeedback))
	gomega.Expect(scenario.RequireVerificationFeedback.Confidence).To(gomega.BeEquivalentTo("high"), fmt.Sprintf("expected suggested verification feedback, got %#v", scenario.RequireVerificationFeedback))
	gomega.Expect(scenario.RequireVerificationFeedback.RecommendationContains).To(gomega.HaveLen(1), fmt.Sprintf("expected suggested verification feedback, got %#v", scenario.RequireVerificationFeedback))
	gomega.Expect(containsStringFold(scenario.RequireVerificationFeedback.RequireFailureClasses, "go_unused_variable")).To(gomega.BeTrue(), fmt.Sprintf("expected suggested verification feedback, got %#v", scenario.RequireVerificationFeedback))

	gomega.Expect(scenario.RequireToolFailureConfidence).NotTo(gomega.BeNil(), fmt.Sprintf("expected suggested tool-failure confidence, got %#v", scenario.RequireToolFailureConfidence))
	gomega.Expect(scenario.RequireToolFailureConfidence.Tool).To(gomega.BeEquivalentTo("read_file"), fmt.Sprintf("expected suggested tool-failure confidence, got %#v", scenario.RequireToolFailureConfidence))
	gomega.Expect(scenario.RequireToolFailureConfidence.ConfidenceImpact).NotTo(gomega.BeEquivalentTo(""), fmt.Sprintf("expected suggested tool-failure confidence, got %#v", scenario.RequireToolFailureConfidence))

	gomega.Expect(scenario.RequireControlRails).NotTo(gomega.BeNil(), fmt.Sprintf("expected suggested control rails, got %#v", scenario.RequireControlRails))
	gomega.Expect(scenario.RequireControlRails.TaskFamily).To(gomega.BeEquivalentTo("locate"), fmt.Sprintf("expected suggested control rails, got %#v", scenario.RequireControlRails))
	gomega.Expect(scenario.RequireControlRails.VerificationContract).To(gomega.BeEquivalentTo("tests_passed_or_failed_or_skipped_with_blocker"), fmt.Sprintf("expected suggested control rails, got %#v", scenario.RequireControlRails))

	gomega.Expect(scenario.MaxSummaryRejections).NotTo(gomega.BeNil(), fmt.Sprintf("expected suggested summary rejection budget, got %#v", scenario.MaxSummaryRejections))
	gomega.Expect(*scenario.MaxSummaryRejections).To(gomega.BeEquivalentTo(1), fmt.Sprintf("expected suggested summary rejection budget, got %#v", scenario.MaxSummaryRejections))
})
var _ = ginkgo.It("parse replan recommendation", func() {
	replan := parseReplanRecommendation("review -> architecture: review produced structural evidence")
	gomega.Expect(replan.FromLane).To(gomega.BeEquivalentTo("review"), fmt.Sprintf("unexpected replan parse: %#v", replan))
	gomega.Expect(replan.ToLane).To(gomega.BeEquivalentTo("architecture"), fmt.Sprintf("unexpected replan parse: %#v", replan))
	gomega.Expect(replan.Reason).To(gomega.ContainSubstring("structural evidence"), fmt.Sprintf("unexpected replan parse: %#v", replan))

	gomega.Expect(replanMatches(replan, ReplanExpectation{FromLane: "review", ToLane: "architecture", Reason: "structural"})).To(gomega.BeTrue(), fmt.Sprintf("expected replan matcher to accept %#v", replan))
})
var _ = ginkgo.It("evidence paths from text extracts path line references", func() {
	paths := evidencePathsFromText("Read f1 internal/surfaces/tui/app/app.go lines 1-120 and found internal/capabilities/workspace/manager.go:42.")
	for _, want := range []string{"internal/surfaces/tui/app/app.go", "internal/capabilities/workspace/manager.go"} {
		gomega.Expect(containsStringFold(paths, want)).To(gomega.BeTrue(), fmt.Sprintf("expected %q in evidence paths, got %#v", want, paths))
	}
})
var _ = ginkgo.It("parse language capabilities", func() {
	caps := parseLanguageCapabilities("Repository profile:\nLanguage capability registry:\n- go tools: go_overview, go_find_symbol | signals: go.mod, internal/surfaces/tui/app/app.go\n- c/c++ tools: cpp_find_symbol | signals: native/main.cpp")
	gomega.Expect(languageCapabilityMatches(caps, LanguageCapabilityExpectation{Language: "go", Tool: "go_find_symbol", Signal: "internal/surfaces/tui/app/app.go"})).To(gomega.BeTrue(), fmt.Sprintf("expected Go capability, got %#v", caps))

	gomega.Expect(languageCapabilityMatches(caps, LanguageCapabilityExpectation{Language: "c/c++", Tool: "cpp_find_symbol", Signal: "native/main.cpp"})).To(gomega.BeTrue(), fmt.Sprintf("expected C/C++ capability, got %#v", caps))
})
var _ = ginkgo.It("parse verification plan", func() {
	plan := parseVerificationPlan(`{"commands":["go build ./...","go test -vet=off ./..."],"uses_full_suite":true,"coverage":"full-suite escalation","confidence":"high","risk_level":"high","expected_failures":["compile errors"],"repair_policy":"repair all failing full-suite checks"}`)
	gomega.Expect(plan.Raw).NotTo(gomega.BeEquivalentTo(""), fmt.Sprint("expected verification plan to parse"))

	gomega.Expect(verificationPlanMatches(plan, VerificationPlanExpectation{
		Coverage:                "full-suite escalation",
		Confidence:              "high",
		RiskLevel:               "high",
		UsesFullSuite:           boolPtr(true),
		RequireCommands:         []string{"go test -vet=off ./..."},
		RequireExpectedFailures: []string{"compile errors"},
	})).To(gomega.BeTrue(), fmt.Sprintf("expected verification plan matcher to accept %#v", plan))
})
var _ = ginkgo.It("parse readiness check", func() {
	readiness := parseReadinessCheck(`{"lane":"architecture","ready":false,"confidence":"low","action":"retry","reason":"response asks for more evidence before it is ready","evidence_paths":["internal/surfaces/tui/app/app.go","internal/capabilities/workspace/manager.go"]}`)
	gomega.Expect(readiness.Raw).NotTo(gomega.BeEquivalentTo(""), fmt.Sprint("expected readiness check to parse"))

	gomega.Expect(readinessMatches(readiness, ReadinessExpectation{
		Lane:             "architecture",
		Ready:            boolPtr(false),
		Confidence:       "low",
		Action:           "retry",
		MinEvidencePaths: intPtr(2),
		ReasonContains:   []string{"more evidence"},
	})).To(gomega.BeTrue(), fmt.Sprintf("expected readiness matcher to accept %#v", readiness))
})
var _ = ginkgo.It("parse deepening attempt", func() {
	deepening := parseDeepeningAttempt(`{"lane":"review","attempt":2,"attempted":true,"exhausted":false,"action":"retry_with_evidence","reason":"readiness gate failed; retry with wider candidate reads","evidence_paths":["internal/surfaces/tui/app/app.go","internal/capabilities/workspace/manager.go"]}`)
	gomega.Expect(deepening.Raw).NotTo(gomega.BeEquivalentTo(""), fmt.Sprint("expected deepening attempt to parse"))

	gomega.Expect(deepeningMatches(deepening, DeepeningExpectation{
		Lane:             "review",
		Attempted:        boolPtr(true),
		Exhausted:        boolPtr(false),
		Action:           "retry_with_evidence",
		MinEvidencePaths: intPtr(2),
		ReasonContains:   []string{"readiness gate"},
	})).To(gomega.BeTrue(), fmt.Sprintf("expected deepening matcher to accept %#v", deepening))
})
var _ = ginkgo.It("parse final answer critic", func() {
	critic := parseFinalAnswerCritic(`{"contract":"implementation_result","passed":false,"action":"repair","repaired":true,"findings":["missing verification section"]}`)
	gomega.Expect(critic.Raw).NotTo(gomega.BeEquivalentTo(""), fmt.Sprint("expected final answer critic to parse"))

	gomega.Expect(finalAnswerCriticMatches(critic, FinalAnswerCriticExpectation{
		Contract:        "implementation_result",
		Passed:          boolPtr(false),
		Action:          "repair",
		Repaired:        boolPtr(true),
		RequireFindings: []string{"verification"},
	})).To(gomega.BeTrue(), fmt.Sprintf("expected final answer critic matcher to accept %#v", critic))
})
var _ = ginkgo.It("parse effort policy", func() {
	policy := parseEffortPolicy(`{"lane":"review","level":"thorough","analysis_tool_budget":6,"implement_tool_budget":6,"deepening_passes":2,"max_output_tokens":2048,"reasoning_budget":-1,"confidence_floor":"high_or_exhausted_budget","budget_exhaustion_mode":"answer_with_explicit_uncertainty_or_fallback"}`)
	gomega.Expect(policy.Raw).NotTo(gomega.BeEquivalentTo(""), fmt.Sprint("expected effort policy to parse"))

	gomega.Expect(effortPolicyMatches(policy, EffortPolicyExpectation{
		Lane:                   "review",
		Level:                  "thorough",
		MinAnalysisToolBudget:  intPtr(6),
		MinImplementToolBudget: intPtr(6),
		MinDeepeningPasses:     intPtr(2),
		MinMaxOutputTokens:     intPtr(2048),
		ConfidenceFloor:        "high_or_exhausted_budget",
		BudgetExhaustionMode:   "answer_with_explicit_uncertainty_or_fallback",
	})).To(gomega.BeTrue(), fmt.Sprintf("expected effort policy matcher to accept %#v", policy))
})
var _ = ginkgo.It("parse long horizon state", func() {
	state := parseLongHorizonState(`{"action":"persist","source_lane":"architecture","stage":1,"focus_path":"internal/surfaces/tui/app/app.go","changed":["internal/surfaces/tui/app/app.go"],"remaining":["internal/capabilities/workspace/manager.go"],"next_step":"Continue with the workspace manager extraction.","resumable":true}`)
	gomega.Expect(state.Raw).NotTo(gomega.BeEquivalentTo(""), fmt.Sprint("expected long-horizon state to parse"))

	gomega.Expect(longHorizonStateMatches(state, LongHorizonStateExpectation{
		Action:           "persist",
		SourceLane:       "architecture",
		Stage:            intPtr(1),
		FocusPath:        "internal/surfaces/tui/app/app.go",
		RequireRemaining: []string{"internal/capabilities/workspace/manager.go"},
		NextContains:     []string{"workspace manager"},
		Resumable:        boolPtr(true),
	})).To(gomega.BeTrue(), fmt.Sprintf("expected long-horizon matcher to accept %#v", state))
})
var _ = ginkgo.It("parse context compaction", func() {
	compaction := parseContextCompaction(`{"dropped_messages":3,"retained_messages":5,"max_history":5,"pinned_paths":["internal/surfaces/tui/app/app.go"],"pinned_memory_kind":"partial_repair","strategy":"retain_recent_history_with_pinned_task_memory_and_refactor_targets"}`)
	gomega.Expect(compaction.Raw).NotTo(gomega.BeEquivalentTo(""), fmt.Sprint("expected context compaction to parse"))

	gomega.Expect(contextCompactionMatches(compaction, ContextCompactionExpectation{
		MinDroppedMessages:  intPtr(3),
		MinRetainedMessages: intPtr(5),
		MaxHistory:          intPtr(5),
		RequirePinnedPaths:  []string{"internal/surfaces/tui/app/app.go"},
		PinnedMemoryKind:    "partial_repair",
		StrategyContains:    []string{"pinned_task_memory"},
	})).To(gomega.BeTrue(), fmt.Sprintf("expected context compaction matcher to accept %#v", compaction))
})
var _ = ginkgo.It("frontier controller matchers reject mismatches", func() {
	gomega.Expect(budgetLedgerMatches(BudgetLedgerSnapshot{
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
	})).To(gomega.BeFalse(), fmt.Sprint("expected budget ledger mismatch to be rejected"))

	gomega.Expect(budgetLedgerMatches(BudgetLedgerSnapshot{
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
	})).To(gomega.BeTrue(), fmt.Sprint("expected budget ledger matcher to accept sufficient token usage"))

	gomega.Expect(budgetLedgerMatches(BudgetLedgerSnapshot{
		Lane:        "review",
		TotalTokens: 10,
	}, BudgetLedgerExpectation{
		Lane:           "review",
		MinTotalTokens: intPtr(40),
	})).To(gomega.BeFalse(), fmt.Sprint("expected budget ledger token mismatch to be rejected"))

	gomega.Expect(evidenceDeltaMatches(EvidenceDeltaSnapshot{
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
	})).To(gomega.BeFalse(), fmt.Sprint("expected evidence delta mismatch to be rejected"))

	gomega.Expect(claimConfidenceMatches(ClaimConfidenceSnapshot{
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
	})).To(gomega.BeFalse(), fmt.Sprint("expected claim confidence mismatch to be rejected"))

	gomega.Expect(contextCompactionMatches(ContextCompactionSnapshot{
		DroppedMessages:  1,
		RetainedMessages: 2,
		MaxHistory:       2,
		PinnedPaths:      []string{"internal/surfaces/tui/app/app.go"},
		PinnedMemoryKind: "investigation",
		Strategy:         "retain_recent_history",
	}, ContextCompactionExpectation{
		MinDroppedMessages:  intPtr(3),
		MinRetainedMessages: intPtr(5),
		MaxHistory:          intPtr(5),
		RequirePinnedPaths:  []string{"internal/evidence/eval/eval.go"},
		PinnedMemoryKind:    "partial_repair",
		StrategyContains:    []string{"pinned_task_memory"},
	})).To(gomega.BeFalse(), fmt.Sprint("expected context compaction mismatch to be rejected"))

	gomega.Expect(routeAmbiguityMatches(RouteAmbiguitySnapshot{
		SelectedLane: "architecture",
		Signals:      []string{"architecture"},
		Ambiguous:    false,
		TieBreak:     "",
	}, RouteAmbiguityExpectation{
		SelectedLane:     "review",
		Ambiguous:        boolPtr(true),
		RequireSignals:   []string{"implement"},
		TieBreakContains: []string{"selected review"},
	})).To(gomega.BeFalse(), fmt.Sprint("expected route ambiguity mismatch to be rejected"))

	gomega.Expect(verificationFeedbackMatches(VerificationFeedbackSnapshot{
		Confidence:            "low",
		FailedCommands:        []string{"go test ./internal/surfaces/tui/app"},
		FailureClasses:        []string{"go_unused_variable"},
		LearnedRecommendation: "repair failing checks before widening scope",
	}, VerificationFeedbackExpectation{
		Confidence:             "high",
		RequireFailedCommands:  []string{"go test ./..."},
		RequireFailureClasses:  []string{"go_import_cycle"},
		RecommendationContains: []string{"full-suite success"},
	})).To(gomega.BeFalse(), fmt.Sprint("expected verification feedback mismatch to be rejected"))

	gomega.Expect(toolFailureConfidenceMatches(ToolFailureConfidenceSnapshot{
		Lane:             "review",
		Tool:             "read_file",
		ConfidenceImpact: "degrade_until_alternate_evidence_covers_gap",
	}, ToolFailureConfidenceExpectation{
		Lane:             "explore",
		Tool:             "search_content",
		ConfidenceImpact: "ignore_failure",
	})).To(gomega.BeFalse(), fmt.Sprint("expected tool-failure confidence mismatch to be rejected"))
})
var _ = ginkgo.It("parse general long horizon task memory", func() {
	state := parseLongHorizonState(`{"action":"persist","memory_kind":"partial_repair","source_lane":"implement","open_gaps":["verification is failing"],"attempted_searches":["validated target internal/surfaces/tui/app/app.go"],"chosen_targets":["internal/surfaces/tui/app/app.go"],"verification_history":["go test ./internal/surfaces/tui/app -> failed: exit status 1"],"next_actions":["repair failing verification before widening implementation scope"],"resumable":true}`)
	gomega.Expect(state.Raw).NotTo(gomega.BeEquivalentTo(""), fmt.Sprint("expected general long-horizon state to parse"))

	gomega.Expect(longHorizonStateMatches(state, LongHorizonStateExpectation{
		Action:                     "persist",
		MemoryKind:                 "partial_repair",
		SourceLane:                 "implement",
		RequireOpenGaps:            []string{"verification is failing"},
		RequireAttemptedSearches:   []string{"validated target internal/surfaces/tui/app/app.go"},
		RequireChosenTargets:       []string{"internal/surfaces/tui/app/app.go"},
		RequireVerificationHistory: []string{"go test ./internal/surfaces/tui/app -> failed: exit status 1"},
		RequireNextActions:         []string{"repair failing verification before widening implementation scope"},
		Resumable:                  boolPtr(true),
	})).To(gomega.BeTrue(), fmt.Sprintf("expected general long-horizon matcher to accept %#v", state))
})
var _ = ginkgo.It("build suggested scenario skips broad or failed tools", func() {
	records := []Record{
		{Type: "message", Data: map[string]any{"role": "user", "content": "padding under banner"}},
		{Type: "tool_request", Data: map[string]any{"step": 1, "tool": "list_files", "broad": true}},
		{Type: "tool_result", Data: map[string]any{"step": 1, "error": ""}},
		{Type: "tool_request", Data: map[string]any{"step": 2, "tool": "search_files", "broad": false}},
		{Type: "tool_result", Data: map[string]any{"step": 2, "error": "no matches"}},
	}

	scenario := BuildSuggestedScenario("bad-run", records)
	gomega.Expect(scenario.RequireTools).To(gomega.BeEmpty(), fmt.Sprintf("expected no required tools from failed/broad run, got %#v", scenario.RequireTools))
})
var _ = ginkgo.It("analyze tracks discovery balance metadata", func() {
	records := []Record{
		{Type: "tool_request", Data: map[string]any{"step": 1, "tool": "structural_search", "discovery_class": "ast_search", "discovery_scope": "targeted", "discovery_cost": "medium"}},
		{Type: "tool_result", Data: map[string]any{"step": 1, "error": ""}},
		{Type: "tool_request", Data: map[string]any{"step": 2, "tool": "discovery_query", "discovery_class": "composite_query", "discovery_scope": "targeted", "discovery_cost": "medium"}},
		{Type: "tool_result", Data: map[string]any{"step": 2, "error": ""}},
		{Type: "tool_request", Data: map[string]any{"step": 3, "tool": "semgrep_scan", "discovery_class": "security_scan", "discovery_scope": "broad", "discovery_cost": "high"}},
		{Type: "tool_result", Data: map[string]any{"step": 3, "error": ""}},
		{Type: "tool_request", Data: map[string]any{"step": 4, "tool": "dependency_graph", "discovery_class": "dependency_graph", "discovery_scope": "broad", "discovery_cost": "medium"}},
		{Type: "tool_result", Data: map[string]any{"step": 4, "error": ""}},
		{Type: "tool_request", Data: map[string]any{"step": 5, "tool": "shell", "discovery_class": "other", "discovery_scope": "targeted", "discovery_cost": "high"}},
		{Type: "tool_result", Data: map[string]any{"step": 5, "error": ""}},
	}

	metrics := Analyze(records)
	gomega.Expect(metrics.DiscoveryTools["structural_search"]).To(gomega.BeEquivalentTo(1), fmt.Sprintf("unexpected discovery tool counts: %#v", metrics.DiscoveryTools))
	gomega.Expect(metrics.DiscoveryTools["discovery_query"]).To(gomega.BeEquivalentTo(1), fmt.Sprintf("unexpected discovery tool counts: %#v", metrics.DiscoveryTools))
	gomega.Expect(metrics.DiscoveryTools["semgrep_scan"]).To(gomega.BeEquivalentTo(1), fmt.Sprintf("unexpected discovery tool counts: %#v", metrics.DiscoveryTools))
	gomega.Expect(metrics.DiscoveryTools["dependency_graph"]).To(gomega.BeEquivalentTo(1), fmt.Sprintf("unexpected discovery tool counts: %#v", metrics.DiscoveryTools))

	gomega.Expect(metrics.DiscoveryClasses["ast_search"]).To(gomega.BeEquivalentTo(1), fmt.Sprintf("unexpected discovery class counts: %#v", metrics.DiscoveryClasses))
	gomega.Expect(metrics.DiscoveryClasses["composite_query"]).To(gomega.BeEquivalentTo(1), fmt.Sprintf("unexpected discovery class counts: %#v", metrics.DiscoveryClasses))
	gomega.Expect(metrics.DiscoveryClasses["security_scan"]).To(gomega.BeEquivalentTo(1), fmt.Sprintf("unexpected discovery class counts: %#v", metrics.DiscoveryClasses))
	gomega.Expect(metrics.DiscoveryClasses["dependency_graph"]).To(gomega.BeEquivalentTo(1), fmt.Sprintf("unexpected discovery class counts: %#v", metrics.DiscoveryClasses))

	gomega.Expect(metrics.DiscoveryScopes["targeted"]).To(gomega.BeEquivalentTo(2), fmt.Sprintf("unexpected discovery scope counts: %#v", metrics.DiscoveryScopes))
	gomega.Expect(metrics.DiscoveryScopes["broad"]).To(gomega.BeEquivalentTo(2), fmt.Sprintf("unexpected discovery scope counts: %#v", metrics.DiscoveryScopes))

	gomega.Expect(metrics.DiscoveryCosts["high"]).To(gomega.BeEquivalentTo(1), fmt.Sprintf("unexpected discovery cost metrics: costs=%#v heavy=%d", metrics.DiscoveryCosts, metrics.HeavyDiscoveryRequests))
	gomega.Expect(metrics.DiscoveryCosts["medium"]).To(gomega.BeEquivalentTo(3), fmt.Sprintf("unexpected discovery cost metrics: costs=%#v heavy=%d", metrics.DiscoveryCosts, metrics.HeavyDiscoveryRequests))
	gomega.Expect(metrics.HeavyDiscoveryRequests).To(gomega.BeEquivalentTo(1), fmt.Sprintf("unexpected discovery cost metrics: costs=%#v heavy=%d", metrics.DiscoveryCosts, metrics.HeavyDiscoveryRequests))
})
var _ = ginkgo.It("analyze promotes readiness evidence paths", func() {
	records := []Record{
		{Type: "message", Data: map[string]any{"role": "readiness_check", "content": `{"action":"answer","lane":"architecture","ready":true,"evidence_paths":["internal/runtime/agent/runner.go","internal/runtime/agent/runner_child_tasks.go"]}`}},
	}

	metrics := Analyze(records)
	for _, want := range []string{"internal/runtime/agent/runner.go", "internal/runtime/agent/runner_child_tasks.go"} {
		gomega.Expect(containsStringFold(metrics.EvidencePaths, want)).To(gomega.BeTrue(), fmt.Sprintf("expected readiness evidence path %q in metrics, got %#v", want, metrics.EvidencePaths))
	}
})
var _ = ginkgo.It("analyze promotes run event sidecar roles", func() {
	records := []Record{
		{Type: "run_event", Data: map[string]any{"role": "readiness_check", "content": `{"action":"answer","lane":"architecture","ready":true,"evidence_paths":["internal/runtime/agent/runner.go","internal/runtime/agent/runner_child_tasks.go"]}`}},
		{Type: "run_event", Data: map[string]any{"role": "final_answer_critic", "content": `{"contract":"architecture_assessment","passed":true,"action":"accept"}`}},
		{Type: "run_event", Data: map[string]any{"role": "control_rails_enforcement", "content": `{"action":"accept","evidence_paths":["internal/runtime/agent/answer_composer.go"]}`}},
	}

	metrics := Analyze(records)
	gomega.Expect(metrics.ReadinessChecks).To(gomega.HaveLen(1), fmt.Sprintf("expected run_event sidecar roles to be analyzed, got readiness=%#v critic=%#v", metrics.ReadinessChecks, metrics.FinalAnswerCritics))
	gomega.Expect(metrics.FinalAnswerCritics).To(gomega.HaveLen(1), fmt.Sprintf("expected run_event sidecar roles to be analyzed, got readiness=%#v critic=%#v", metrics.ReadinessChecks, metrics.FinalAnswerCritics))

	for _, want := range []string{"internal/runtime/agent/runner.go", "internal/runtime/agent/runner_child_tasks.go", "internal/runtime/agent/answer_composer.go"} {
		gomega.Expect(containsStringFold(metrics.EvidencePaths, want)).To(gomega.BeTrue(), fmt.Sprintf("expected run_event readiness evidence path %q in metrics, got %#v", want, metrics.EvidencePaths))
	}
})
var _ = ginkgo.It("evaluate discovery balance assertions", func() {
	records := []Record{
		{Type: "message", Data: map[string]any{"role": "user", "content": "find insecure eval"}},
		{Type: "tool_request", Data: map[string]any{"step": 1, "tool": "discovery_query", "discovery_class": "composite_query", "discovery_scope": "targeted", "discovery_cost": "medium"}},
		{Type: "tool_result", Data: map[string]any{"step": 1, "error": ""}},
		{Type: "tool_request", Data: map[string]any{"step": 2, "tool": "semgrep_scan", "discovery_class": "security_scan", "discovery_scope": "broad", "discovery_cost": "high"}},
		{Type: "tool_result", Data: map[string]any{"step": 2, "error": ""}},
		{Type: "message", Data: map[string]any{"role": "assistant", "content": "No bounded finding."}},
	}
	maxHeavy := 1
	report := Evaluate("discovery.jsonl", records, Scenario{
		Name:                      "discovery balance",
		RequireDiscoveryTools:     []string{"discovery_query", "semgrep_scan"},
		RequireDiscoveryClasses:   []string{"composite_query", "security_scan"},
		MaxHeavyDiscoveryRequests: &maxHeavy,
	})

	gomega.Expect(report.Passed).To(gomega.BeTrue(), fmt.Sprintf("expected discovery balance scenario to pass, findings=%#v", report.Findings))

	zeroHeavy := 0

	report = Evaluate("discovery.jsonl", records, Scenario{
		Name:                      "heavy budget",
		MaxHeavyDiscoveryRequests: &zeroHeavy,
	})

	gomega.Expect(report.Passed).To(gomega.BeFalse(), fmt.Sprintf("expected heavy discovery budget failure, got passed=%t findings=%#v", report.Passed, report.Findings))
	gomega.Expect(strings.Join(report.Findings, "\n")).To(gomega.ContainSubstring("heavy discovery requests exceeded"), fmt.Sprintf("expected heavy discovery budget failure, got passed=%t findings=%#v", report.Passed, report.Findings))
})
var _ = ginkgo.It("load records reads jsonl", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.jsonl")

	const payload = "{\"type\":\"message\",\"role\":\"user\",\"content\":\"hi\"}\n{\"type\":\"message\",\"role\":\"assistant\",\"content\":\"hello\"}\n"

	gomega.Expect(os.WriteFile(path, []byte(payload), 0o644)).To(gomega.Succeed(), "write sample log")

	records, err := LoadRecords(path)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("LoadRecords: %v", err))

	gomega.Expect(records).To(gomega.HaveLen(2), fmt.Sprintf("expected 2 records, got %d", len(records)))
})
var _ = ginkgo.It("load records normalizes kind payload jsonl", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.jsonl")

	const payload = "{\"kind\":\"tool_request\",\"payload\":{\"tool\":\"search_content\",\"raw\":\"{\\\"pattern\\\":\\\"Runner\\\"}\",\"action\":\"search_content:Runner\"}}\n"

	gomega.Expect(os.WriteFile(path, []byte(payload), 0o644)).To(gomega.Succeed(), "write sample log")

	records, err := LoadRecords(path)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("LoadRecords: %v", err))

	gomega.Expect(records).To(gomega.HaveLen(1), fmt.Sprintf("expected 1 record, got %d", len(records)))

	gomega.Expect(records[0].Type).To(gomega.BeEquivalentTo("tool_request"), fmt.Sprintf("record type: want tool_request, got %q", records[0].Type))

	{
		got, _ := records[0].Data["tool"].(string)
		gomega.Expect(got).To(gomega.BeEquivalentTo("search_content"), fmt.Sprintf("normalized tool: want search_content, got %q", got))
	}
})
var _ = ginkgo.It("load records handles long jsonl line", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.jsonl")
	payload := "{\"type\":\"message\",\"role\":\"assistant\",\"content\":\"" + strings.Repeat("x", 128*1024) + "\"}\n"

	gomega.Expect(os.WriteFile(path, []byte(payload), 0o644)).To(gomega.Succeed(), "write sample log")

	records, err := LoadRecords(path)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("LoadRecords: %v", err))

	gomega.Expect(records).To(gomega.HaveLen(1), fmt.Sprintf("expected 1 record, got %d", len(records)))

	{
		got, _ := records[0].Data["content"].(string)
		gomega.Expect(got).To(gomega.HaveLen(128*1024), fmt.Sprintf("long content length: want %d, got %d", 128*1024, len(got)))
	}
})
var _ = ginkgo.It("analyze log supports e2 e kind payload records", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()
	path := filepath.Join(dir, "e2e.jsonl")
	payload := strings.Join([]string{
		`{"kind":"user_turn","payload":{"index":0,"prompt":"find Runner"}}`,
		`{"kind":"tool_request","payload":{"turn":0,"step":1,"tool":"search_content","raw":"{\"pattern\":\"Runner\"}","action":"search_content:Runner"}}`,
		`{"kind":"tool_result","payload":{"turn":0,"step":1,"tool":"search_content","summary":"Found 1 matches","error":""}}`,
		`{"kind":"assistant_turn","payload":{"index":0,"chars":42}}`,
		`{"kind":"summary","payload":{"elapsed_ms":99,"tool_requests":1,"tool_results":1,"answer_chars":42}}`,
	}, "\n") + "\n"

	gomega.Expect(os.WriteFile(path, []byte(payload), 0o644)).To(gomega.Succeed(), "write e2e log")

	report, err := AnalyzeLog(path)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("AnalyzeLog: %v", err))

	gomega.Expect(report.UserPrompt).To(gomega.BeEquivalentTo("find Runner"), fmt.Sprintf("user prompt: want find Runner, got %q", report.UserPrompt))

	gomega.Expect(report.Tools.SearchContent).To(gomega.BeEquivalentTo(1), fmt.Sprintf("tool counts not parsed: %+v", report.Tools))
	gomega.Expect(report.Tools.Total).To(gomega.BeEquivalentTo(1), fmt.Sprintf("tool counts not parsed: %+v", report.Tools))

	gomega.Expect(report.ToolResults).To(gomega.BeEquivalentTo(1), fmt.Sprintf("summary metrics not parsed: %+v", report))
	gomega.Expect(report.DurationMillis).To(gomega.BeEquivalentTo(99), fmt.Sprintf("summary metrics not parsed: %+v", report))
	gomega.Expect(report.FinalAnswerChars).To(gomega.BeEquivalentTo(42), fmt.Sprintf("summary metrics not parsed: %+v", report))
})

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
