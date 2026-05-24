package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"idiom/internal/autonomy"
	"idiom/internal/config"
	"idiom/internal/frontier/workerruntime"
	"idiom/internal/llama"
	"idiom/internal/workspace"
)

func TestRunnerAutonomyContractMapsMutationGatesToObligations(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		Lane:                 "implement",
		TaskFamily:           "implementation",
		Contract:             "implementation_result",
		PermissionProfile:    "guarded_workspace_write",
		RequiredEditPaths:    []string{"internal/agent/runner.go"},
		EvidencePaths:        []string{"docs/decisioning-north-star.md"},
		AllowedTools:         []string{"read_file", "apply_patch", "shell"},
		ForbiddenTools:       []string{"git_push"},
		RequiredEvidence:     []string{"changed_paths_or_noop_reason"},
		ReadinessGate:        []string{"verification_status_reported"},
		VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
		VerificationPlan:     []string{"go test ./internal/agent"},
		TargetPacket:         []string{"context_pack://p1.5"},
		Readiness: &workerruntime.ReadinessState{
			ReadyToWrite:        true,
			OpenObligations:     []string{"write", "verify", "final_answer"},
			EvidenceSources:     []string{"context_pack://p1.5"},
			FurtherReadsRequire: "one exact missing claim and one exact probe",
		},
	}
	policy := RunPolicy{MaxToolSteps: 12, MaxModelCalls: 3, MaxContextMessages: 20, MaxContextChars: 120000}

	contract, obligations, ok := runnerAutonomyContract(&Runner{AgentName: "main"}, policy, brief, nil)
	if !ok {
		t.Fatal("runnerAutonomyContract returned ok=false")
	}
	for _, want := range []autonomy.ObligationKind{
		autonomy.ObligationWrite,
		autonomy.ObligationVerify,
		autonomy.ObligationFinalAnswer,
		autonomy.ObligationLoop,
		autonomy.ObligationBudget,
	} {
		if countObligationKind(obligations, want) == 0 {
			t.Fatalf("missing obligation kind %q in %#v", want, obligations)
		}
	}
	if !hasPathRole(contract.PathRoles, autonomy.PathRoleRequiredEdit, "internal/agent/runner.go") ||
		!hasPathRole(contract.PathRoles, autonomy.PathRoleEvidenceReadonly, "docs/decisioning-north-star.md") ||
		!hasPathRole(contract.PathRoles, autonomy.PathRoleProtected, ".git/**") {
		t.Fatalf("contract path roles did not map required/evidence/protected paths: %#v", contract.PathRoles)
	}
	if !containsAutonomyCapability(contract.Capabilities.Allowed, autonomy.CapabilityApplyPatch) ||
		!containsAutonomyCapability(contract.Capabilities.Allowed, autonomy.CapabilityShellVerification) {
		t.Fatalf("contract capabilities did not map write/verification tools: %#v", contract.Capabilities)
	}
	if contract.Verification.RequiredAfterWrite != true || contract.Completion.BlockOnOpenObligations != true {
		t.Fatalf("contract did not encode verification/completion gates: verification=%#v completion=%#v", contract.Verification, contract.Completion)
	}
	if len(contract.Metadata.ClientClassifications) != len(autonomy.DefaultClientClassifications()) ||
		len(contract.Metadata.ProductSignals) != len(autonomy.DefaultProductSignals()) {
		t.Fatalf("contract should carry generic client/product metadata: %#v", contract.Metadata)
	}
	writeID := runnerAutonomyObligationID(autonomy.ObligationWrite, "internal/agent/runner.go")
	if !slices.Contains(contract.Readiness.OpenObligationRefs, writeID) {
		t.Fatalf("readiness should map abstract write obligation to typed obligation id %q: %#v", writeID, contract.Readiness.OpenObligationRefs)
	}
	if slices.Contains(contract.Readiness.OpenObligationRefs, "write") {
		t.Fatalf("readiness must not expose abstract obligation names as refs: %#v", contract.Readiness.OpenObligationRefs)
	}
}

func TestRunnerAutonomyRecorderPolicyEventsMapAllowedAndNoApprovalNeeded(t *testing.T) {
	root := t.TempDir()
	var runEvents []RunEvent
	runner := &Runner{
		AgentName: "main",
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		OnEvent: func(event RunEvent) {
			runEvents = append(runEvents, event)
		},
	}
	brief := workerruntime.WorkerRuntimeBrief{
		Lane:              "implement",
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"internal/agent/runner.go"},
		EvidencePaths:     []string{"docs/decisioning-north-star.md"},
		AllowedTools:      []string{"read_file", "apply_patch"},
	}
	recorder := newRunnerAutonomyRecorder(runner, RunPolicy{}, brief, nil)
	if recorder == nil {
		t.Fatal("newRunnerAutonomyRecorder returned nil")
	}

	req := workspace.ToolRequest{Tool: "read_file", Path: "docs/decisioning-north-star.md"}
	recorder.emitContractStarted(brief)
	recorder.emitIntentClassified(req)
	recorder.emitPathRolesResolved(req)
	recorder.emitPolicyResult(req, runnerAutonomyPolicyEventInput{
		Decision:   autonomy.PolicyAllow,
		ReasonCode: "permission_policy",
		Result:     "no_approval_needed",
		Source:     "runner_permission_policy",
	})

	events := decodeRunnerAutonomyEventsForTest(t, runEvents)
	intent := mustAutonomyPolicyEvent(t, events, autonomy.EventToolIntentClassified, "read_file", "")
	if intent.Policy.SideEffect != autonomy.SideEffectReadOnly || intent.Policy.Capability != autonomy.CapabilityBoundedRead {
		t.Fatalf("intent event did not classify read-only bounded read: %#v", intent.Policy)
	}
	if intent.Policy.ActionClass != autonomy.ActionClassRead || intent.Policy.Transport != autonomy.ToolTransportTextualCall {
		t.Fatalf("intent event did not classify canonical read/textual action: %#v", intent.Policy)
	}
	if intent.Policy.ActionKey == "" || !strings.Contains(intent.Policy.ActionKey, "read_file") {
		t.Fatalf("intent event should carry stable normalized action key: %#v", intent.Policy)
	}
	paths := mustAutonomyPolicyEvent(t, events, autonomy.EventPathRolesResolved, "read_file", "")
	if !slices.Contains(policyEventPaths(paths.Policy), "docs/decisioning-north-star.md") {
		t.Fatalf("path resolution event should include evidence path match: %#v", paths.Policy)
	}
	result := mustAutonomyPolicyEvent(t, events, autonomy.EventToolPolicyResult, "read_file", "no_approval_needed")
	if result.Policy.Decision != autonomy.PolicyAllow || result.Policy.ApprovalRequired {
		t.Fatalf("allow result should preserve no-approval-needed outcome: %#v", result.Policy)
	}
}

func TestRunnerAutonomyRecorderEmitsTurnPhaseContractPresentation(t *testing.T) {
	var runEvents []RunEvent
	runner := &Runner{
		AgentName: "main",
		OnEvent: func(event RunEvent) {
			runEvents = append(runEvents, event)
		},
	}
	brief := workerruntime.WorkerRuntimeBrief{
		Lane:              "plan-execute",
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "submit_plan",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"a.go"},
		AllowedTools:      []string{"read_file", "submit_plan"},
	}
	results := []workspace.ToolResult{{Tool: "read_file", Summary: "Read f1 a.go lines 1-10 (total lines: 10)"}}
	recorder := newRunnerAutonomyRecorder(runner, RunPolicy{}, brief, results)
	if recorder == nil {
		t.Fatal("newRunnerAutonomyRecorder returned nil")
	}
	recorder.emitContractStarted(brief)

	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		ContractID:         recorder.contract.ContractID,
		ModelCallID:        recorder.contract.ContractID + ":model_call_1",
		Step:               1,
		Brief:              brief,
		Results:            results,
		BaseAllowedTools:   []string{"submit_plan"},
		TextualToolParser:  true,
		VerificationStatus: "not_required",
	})
	recorder.emitTurnPhaseContract(contract)

	events := decodeRunnerAutonomyEventsForTest(t, runEvents)
	var presented autonomy.Event
	for _, event := range events {
		if event.Type == autonomy.EventContractPresented &&
			event.Lifecycle != nil &&
			event.Lifecycle.TurnPhaseContract != nil {
			presented = event
			break
		}
	}
	if presented.EventID == "" {
		t.Fatalf("expected contract_presented turn phase event, got %#v", events)
	}
	phase := presented.Lifecycle.TurnPhaseContract
	if phase.SchemaVersion != turnPhaseContractSchemaVersion ||
		phase.PhaseID != turnPhaseSubmitReadyTextual ||
		!slices.Contains(phase.ActionPolicy.AllowedActionClasses, actionSubmitPlan) ||
		!slices.Contains(phase.RuntimeCall.ExpressibleActionClasses, actionSourceSatisfied) {
		t.Fatalf("unexpected turn phase payload: %#v", phase)
	}
	report := autonomy.AuditEvents(events, autonomy.EventAuditOptions{})
	if report.TurnPhaseCoverage.Total != 1 || len(report.TurnPhaseCoverage.Contradictions) != 0 {
		t.Fatalf("expected clean turn phase audit coverage, got %#v", report.TurnPhaseCoverage)
	}
}

func TestRunnerRunEmitsTurnPhaseContractBeforeModelCallAndPolicyEvents(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "a.go", "package main\nfunc A() {}\n"); err != nil {
		t.Fatalf("write seed: %v", err)
	}
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": `{"tool":"read_file","path":"a.go","start_line":1,"end_line":1}`},
			}},
		})
	}))
	defer server.Close()

	mgr := workspace.NewManager(config.Workspace{Root: root, CWD: root})
	var runEvents []RunEvent
	runner := &Runner{
		Client:    llama.NewClient(server.URL, time.Second),
		Workspace: mgr,
		Policy:    RunPolicy{MaxToolSteps: 1, MaxModelCalls: 1},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Lane:              "plan-execute",
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "submit_plan",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.go"},
			AllowedTools:      []string{"read_file", "submit_plan"},
		},
		OnEvent: func(event RunEvent) {
			runEvents = append(runEvents, event)
		},
	}
	_, _ = runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "plan edit"}}, llama.ChatOptions{}, nil, nil, nil)
	if requests != 1 {
		t.Fatalf("expected one model request, got %d", requests)
	}

	turnPhaseIndex := -1
	modelCallIndex := -1
	postModelIndex := -1
	for i, event := range runEvents {
		switch event.Role {
		case "model_call":
			if modelCallIndex == -1 {
				modelCallIndex = i
			}
		case "permission_decision", "next_action_state":
			if postModelIndex == -1 {
				postModelIndex = i
			}
		case runnerAutonomyEventRole:
			var decoded autonomy.Event
			if err := json.Unmarshal([]byte(event.Content), &decoded); err != nil {
				t.Fatalf("decode autonomy event: %v", err)
			}
			if decoded.Type == autonomy.EventContractPresented &&
				decoded.Lifecycle != nil &&
				decoded.Lifecycle.TurnPhaseContract != nil &&
				turnPhaseIndex == -1 {
				turnPhaseIndex = i
			}
		}
	}
	if turnPhaseIndex == -1 || modelCallIndex == -1 || postModelIndex == -1 {
		t.Fatalf("missing expected event order markers: turn_phase=%d model_call=%d post_model=%d roles=%#v", turnPhaseIndex, modelCallIndex, postModelIndex, eventRolesFor(runEvents))
	}
	if !(turnPhaseIndex < modelCallIndex && modelCallIndex < postModelIndex) {
		t.Fatalf("expected turn phase before model_call before post-model events, got turn_phase=%d model_call=%d post_model=%d", turnPhaseIndex, modelCallIndex, postModelIndex)
	}
}

func TestRunnerAutonomyRecorderPolicyEventsMapProtectedForbiddenAndApprovalOutcomes(t *testing.T) {
	root := t.TempDir()
	var runEvents []RunEvent
	runner := &Runner{
		AgentName: "main",
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		OnEvent: func(event RunEvent) {
			runEvents = append(runEvents, event)
		},
	}
	brief := workerruntime.WorkerRuntimeBrief{
		Lane:              "implement",
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"internal/agent/runner.go"},
		AllowedTools:      []string{"apply_patch"},
	}
	recorder := newRunnerAutonomyRecorder(runner, RunPolicy{}, brief, nil)
	if recorder == nil {
		t.Fatal("newRunnerAutonomyRecorder returned nil")
	}

	protectedReq := workspace.ToolRequest{Tool: "apply_patch", Content: "*** Begin Patch\n*** Update File: .git/config\n@@\n-[core]\n+[core]\n*** End Patch\n"}
	forbiddenReq := workspace.ToolRequest{Tool: "write_file", Path: "README.md"}
	approvalReq := workspace.ToolRequest{Tool: "apply_patch", Path: "internal/agent/runner.go", Content: "*** Begin Patch\n*** Update File: internal/agent/runner.go\n@@\n-foo\n+bar\n*** End Patch\n"}
	recorder.emitContractStarted(brief)
	recorder.emitPathRolesResolved(protectedReq)
	recorder.emitPolicyResult(protectedReq, runnerAutonomyPolicyEventInput{
		Decision:   autonomy.PolicyDeny,
		ReasonCode: "required_edit_scope_rejected",
		Result:     "denied",
		ErrorClass: "required_edit_scope",
		Source:     "runner_required_edit_scope",
	})
	recorder.emitPolicyResult(forbiddenReq, runnerAutonomyPolicyEventInput{
		Decision:   autonomy.PolicyDeny,
		ReasonCode: "required_edit_scope_rejected",
		Result:     "denied",
		ErrorClass: "required_edit_scope",
		Source:     "runner_required_edit_scope",
	})
	recorder.emitPolicyResult(approvalReq, runnerAutonomyPolicyEventInput{
		Decision:         autonomy.PolicyAsk,
		ReasonCode:       "write_approval_required",
		ApprovalRequired: true,
		Result:           "approval_required",
		ErrorClass:       "approval_guard",
		Source:           "runner_pre_execute_guard",
	})
	recorder.emitPolicyResult(approvalReq, runnerAutonomyPolicyEventInput{
		Decision:         autonomy.PolicyAllow,
		ReasonCode:       "write_approval_required",
		ApprovalRequired: true,
		Result:           "approved",
		Source:           "runner_pre_execute_guard",
	})

	events := decodeRunnerAutonomyEventsForTest(t, runEvents)
	protected := mustAutonomyPolicyEvent(t, events, autonomy.EventToolPolicyResult, "apply_patch", "denied")
	if protected.Policy.InvariantID != string(autonomy.InvariantProtectedPath) || !slices.Contains(protected.Policy.ProtectedMatches, ".git/config") {
		t.Fatalf("protected path denial did not normalize to protected invariant: %#v", protected.Policy)
	}
	forbidden := mustAutonomyPolicyEventByInvariant(t, events, autonomy.EventToolPolicyResult, string(autonomy.InvariantForbiddenPath), "denied")
	if forbidden.Policy.Decision != autonomy.PolicyDeny || forbidden.Policy.ReasonCode != "forbidden_path" {
		t.Fatalf("forbidden path denial did not preserve deny outcome: %#v", forbidden.Policy)
	}
	approvalRequired := mustAutonomyPolicyEvent(t, events, autonomy.EventToolPolicyResult, "apply_patch", "approval_required")
	if approvalRequired.Policy.Decision != autonomy.PolicyAsk || !approvalRequired.Policy.ApprovalRequired {
		t.Fatalf("approval-required event mismatch: %#v", approvalRequired.Policy)
	}
	approved := mustAutonomyPolicyEvent(t, events, autonomy.EventToolPolicyResult, "apply_patch", "approved")
	if approved.Policy.Decision != autonomy.PolicyAllow || !approved.Policy.ApprovalRequired {
		t.Fatalf("approved event mismatch: %#v", approved.Policy)
	}
}

func TestRunnerAutonomyShellSideEffectClassifiesConservatively(t *testing.T) {
	tests := []struct {
		name    string
		command string
		want    autonomy.SideEffectClass
	}{
		{name: "git diff check", command: "git diff --check", want: autonomy.SideEffectReadOnly},
		{name: "git diff check path", command: "git diff --check -- README.md", want: autonomy.SideEffectReadOnly},
		{name: "git status", command: "git status --short", want: autonomy.SideEffectReadOnly},
		{name: "go test", command: "go test", want: autonomy.SideEffectReadOnly},
		{name: "go test package", command: "go test ./internal/agent", want: autonomy.SideEffectReadOnly},
		{name: "go test quoted regex", command: "go test ./internal/agent -run 'TestA|TestB'", want: autonomy.SideEffectReadOnly},
		{name: "git status compound", command: "git status && rm -rf tmp", want: autonomy.SideEffectUnknown},
		{name: "go test semicolon", command: "go test ./...; touch x", want: autonomy.SideEffectUnknown},
		{name: "go test pipe", command: "go test ./... | tee report.txt", want: autonomy.SideEffectUnknown},
		{name: "git diff redirection", command: "git diff --check > report.txt", want: autonomy.SideEffectUnknown},
		{name: "git diff output flag", command: "git diff --output=report.txt", want: autonomy.SideEffectUnknown},
		{name: "prefix is not enough", command: "git statusx", want: autonomy.SideEffectUnknown},
		{name: "direct mutating command", command: "touch x", want: autonomy.SideEffectUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := runnerAutonomyShellSideEffect(tt.command); got != tt.want {
				t.Fatalf("runnerAutonomyShellSideEffect(%q) = %q, want %q", tt.command, got, tt.want)
			}
		})
	}
}

func TestRunnerAutonomyActionClassForRequestUsesCanonicalVocabulary(t *testing.T) {
	tests := []struct {
		name string
		req  workspace.ToolRequest
		want autonomy.ActionClass
	}{
		{name: "read", req: workspace.ToolRequest{Tool: "read_file", Path: "internal/agent/runner.go"}, want: autonomy.ActionClassRead},
		{name: "search", req: workspace.ToolRequest{Tool: "search_content", Path: ".", Pattern: "needle"}, want: autonomy.ActionClassSearch},
		{name: "plan", req: workspace.ToolRequest{Tool: "submit_plan"}, want: autonomy.ActionClassSubmitPlan},
		{name: "source write", req: workspace.ToolRequest{Tool: "write_file", Path: "internal/agent/runner.go"}, want: autonomy.ActionClassPerEditWrite},
		{name: "deliverable write", req: workspace.ToolRequest{Tool: "write_file", Path: "docs/report.md"}, want: autonomy.ActionClassDeliverableWrite},
		{name: "verify shell", req: workspace.ToolRequest{Tool: "shell", Command: "go test ./internal/agent"}, want: autonomy.ActionClassVerify},
		{name: "unknown shell probe", req: workspace.ToolRequest{Tool: "shell", Command: "touch tmp"}, want: autonomy.ActionClassGenericProbe},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := runnerAutonomyActionClassForRequest(tt.req); got != tt.want {
				t.Fatalf("runnerAutonomyActionClassForRequest(%#v) = %q, want %q", tt.req, got, tt.want)
			}
		})
	}
}

func TestRunnerAutonomyRecorderPolicyEventsClassifyPatchSideEffects(t *testing.T) {
	var runEvents []RunEvent
	runner := &Runner{
		AgentName: "main",
		OnEvent: func(event RunEvent) {
			runEvents = append(runEvents, event)
		},
	}
	brief := workerruntime.WorkerRuntimeBrief{
		Lane:              "implement",
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"internal/agent/runner.go"},
		AllowedTools:      []string{"apply_patch"},
	}
	recorder := newRunnerAutonomyRecorder(runner, RunPolicy{}, brief, nil)
	if recorder == nil {
		t.Fatal("newRunnerAutonomyRecorder returned nil")
	}

	recorder.emitContractStarted(brief)
	recorder.emitIntentClassified(workspace.ToolRequest{
		Tool:    "apply_patch",
		Content: "*** Begin Patch\n*** Delete File: doomed.txt\n*** End Patch\n",
	})

	events := decodeRunnerAutonomyEventsForTest(t, runEvents)
	intent := mustAutonomyPolicyEvent(t, events, autonomy.EventToolIntentClassified, "apply_patch", "")
	if intent.Policy.SideEffect != autonomy.SideEffectDestructive {
		t.Fatalf("delete patch should classify as destructive, got %#v", intent.Policy)
	}
}

func TestRunnerAutonomyContractMapsReadOnlyLaneToEvidenceObligation(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		Lane:              "ask",
		TaskFamily:        "debug_logs",
		Contract:          "grounded_explanation",
		PermissionProfile: "read_only_workspace",
		EvidencePaths:     []string{"internal/agent/runner.go"},
		AllowedTools:      []string{"read_file", "search_content", "context_pack"},
		ForbiddenTools:    []string{"apply_patch", "edit_file"},
	}

	contract, obligations, ok := runnerAutonomyContract(&Runner{AgentName: "main"}, RunPolicy{}, brief, nil)
	if !ok {
		t.Fatal("runnerAutonomyContract returned ok=false")
	}
	if countObligationKind(obligations, autonomy.ObligationEvidence) != 1 ||
		countObligationKind(obligations, autonomy.ObligationWrite) != 0 {
		t.Fatalf("read-only lane should map to evidence obligation without write obligation: %#v", obligations)
	}
	for _, forbidden := range []autonomy.Capability{autonomy.CapabilitySourceEdit, autonomy.CapabilityApplyPatch, autonomy.CapabilityMutatingShell} {
		if !containsAutonomyCapability(contract.Capabilities.Forbidden, forbidden) {
			t.Fatalf("read-only lane should forbid %q: %#v", forbidden, contract.Capabilities)
		}
	}
	if len(contract.Capabilities.AllowedEffects) != 1 || contract.Capabilities.AllowedEffects[0] != autonomy.SideEffectReadOnly {
		t.Fatalf("read-only lane should only allow read-only effects: %#v", contract.Capabilities.AllowedEffects)
	}
}

func TestRunnerAutonomyRecorderEmitsGateEventsForExistingGates(t *testing.T) {
	var runEvents []RunEvent
	runner := &Runner{
		AgentName: "main",
		OnEvent: func(event RunEvent) {
			runEvents = append(runEvents, event)
		},
	}
	brief := workerruntime.WorkerRuntimeBrief{
		Lane:                 "implement",
		TaskFamily:           "implementation",
		Contract:             "implementation_result",
		PermissionProfile:    "guarded_workspace_write",
		RequiredEditPaths:    []string{"internal/agent/runner.go"},
		AllowedTools:         []string{"read_file", "apply_patch", "shell"},
		VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
		Readiness: &workerruntime.ReadinessState{
			ReadyToWrite:    true,
			ReadyToVerify:   false,
			OpenObligations: []string{"write", "verify", "final_answer"},
		},
	}
	policy := RunPolicy{MaxToolSteps: 5, MaxModelCalls: 2}
	recorder := newRunnerAutonomyRecorder(runner, policy, brief, nil)
	if recorder == nil {
		t.Fatal("newRunnerAutonomyRecorder returned nil")
	}

	recorder.emitContractStarted(brief)
	recorder.emitWriteGate(workspace.ToolRequest{Tool: "apply_patch", Content: "*** Begin Patch\n*** Update File: internal/agent/runner.go\n@@\n*** End Patch\n"}, autonomy.PolicyReject, "outside_required_edit_paths", nil)
	recorder.emitVerificationGate(workspace.ToolRequest{Tool: "read_file", Path: "internal/agent/runner.go"}, autonomy.PolicyReject, "verification_required_after_write", "pending")
	recorder.emitCompletionGate(autonomy.PolicyReject, "completion_gate_rejected", nil)
	recorder.emitLoopGate(workspace.ToolRequest{Tool: "read_file", Path: "internal/agent/runner.go"}, autonomy.PolicyStop, "repeated_tool_call", "read_file:internal/agent/runner.go", 3)
	recorder.emitBudgetGate(policy, runnerMetrics{ToolCalls: 5, ModelCalls: 2}, "max_tool_steps", true)

	events := decodeRunnerAutonomyEventsForTest(t, runEvents)
	gateKinds := map[autonomy.GateKind]int{}
	gateObligationRefs := map[autonomy.GateKind][]string{}
	eventTypes := map[autonomy.EventType]int{}
	obligationKinds := map[autonomy.ObligationKind]int{}
	for _, event := range events {
		eventTypes[event.Type]++
		if event.Gate != nil {
			gateKinds[event.Gate.Gate]++
			gateObligationRefs[event.Gate.Gate] = append(gateObligationRefs[event.Gate.Gate], event.Gate.ObligationRefs...)
			if event.Gate.Gate == autonomy.GateBudget && (event.Gate.Budget == nil || !event.Gate.Budget.Exhausted) {
				t.Fatalf("budget gate should carry exhausted budget state: %#v", event.Gate)
			}
		}
		if event.Type == autonomy.EventObligationOpened && event.Obligation != nil {
			obligationKinds[event.Obligation.Kind]++
		}
	}
	for _, want := range []autonomy.GateKind{
		autonomy.GateWrite,
		autonomy.GateVerification,
		autonomy.GateCompletion,
		autonomy.GateLoop,
		autonomy.GateBudget,
	} {
		if gateKinds[want] != 1 {
			t.Fatalf("missing gate event %q in %#v", want, gateKinds)
		}
	}
	if eventTypes[autonomy.EventReadinessStateUpdated] != 1 || eventTypes[autonomy.EventCompletionGateDecision] != 1 {
		t.Fatalf("completion/readiness typed events missing: %#v", eventTypes)
	}
	for _, want := range []autonomy.ObligationKind{
		autonomy.ObligationWrite,
		autonomy.ObligationVerify,
		autonomy.ObligationFinalAnswer,
		autonomy.ObligationLoop,
		autonomy.ObligationBudget,
	} {
		if obligationKinds[want] != 1 {
			t.Fatalf("missing opened obligation kind %q in %#v", want, obligationKinds)
		}
	}
	if !slices.Contains(gateObligationRefs[autonomy.GateVerification], "obl-verify-post-write") {
		t.Fatalf("verification gate should reference typed verify obligation: %#v", gateObligationRefs)
	}
}

func TestRunnerAutonomyRecorderLifecycleEventsCloseSuccessfulGateObligations(t *testing.T) {
	for _, tc := range []struct {
		name             string
		budgetExhausted  bool
		budgetStopReason string
	}{
		{name: "normal budget closeout", budgetStopReason: "completed"},
		{name: "budget exhaustion closeout", budgetExhausted: true, budgetStopReason: "tool_step_budget_exhausted"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var runEvents []RunEvent
			runner := &Runner{
				AgentName: "main",
				OnEvent: func(event RunEvent) {
					runEvents = append(runEvents, event)
				},
			}
			brief := workerruntime.WorkerRuntimeBrief{
				Lane:                 "implement",
				TaskFamily:           "implementation",
				Contract:             "implementation_result",
				PermissionProfile:    "guarded_workspace_write",
				RequiredEditPaths:    []string{"internal/agent/runner.go"},
				AllowedTools:         []string{"read_file", "apply_patch", "shell"},
				VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
			}
			policy := RunPolicy{MaxToolSteps: 5, MaxModelCalls: 2}
			recorder := newRunnerAutonomyRecorder(runner, policy, brief, nil)
			if recorder == nil {
				t.Fatal("newRunnerAutonomyRecorder returned nil")
			}

			recorder.emitContractStarted(brief)
			recorder.emitWriteGate(workspace.ToolRequest{Tool: "apply_patch", Path: "internal/agent/runner.go"}, autonomy.PolicyAllow, "write_result_completed", []string{"internal/agent/runner.go"})
			recorder.emitVerificationGate(workspace.ToolRequest{Tool: "shell", Command: "go test ./internal/agent"}, autonomy.PolicyAllow, "verification_result_passed", "passed")
			recorder.emitCompletionGate(autonomy.PolicyAllow, "final_answer_accepted", nil)
			recorder.emitLoopCloseout("completed")
			recorder.emitBudgetGate(policy, runnerMetrics{ToolCalls: 3, ModelCalls: 2}, tc.budgetStopReason, tc.budgetExhausted)

			events := decodeRunnerAutonomyEventsForTest(t, runEvents)
			report := autonomy.AuditEvents(events, autonomy.EventAuditOptions{})
			if !report.AllClear || len(report.ObligationCoverage.UnresolvedObligations) != 0 {
				t.Fatalf("expected all-clear lifecycle audit, all_clear=%v obligation=%#v findings=%#v", report.AllClear, report.ObligationCoverage, report.Findings)
			}
			for _, want := range []string{
				runnerAutonomyObligationID(autonomy.ObligationWrite, "internal/agent/runner.go"),
				"obl-verify-post-write",
				"obl-final-answer",
				"obl-loop-fuse",
				"obl-budget",
			} {
				if !hasAutonomyObligationEvent(events, autonomy.EventObligationResolved, want) {
					t.Fatalf("expected obligation_resolved for %q in %#v", want, events)
				}
				if !slices.Contains(report.GateCoverage.ResolvedObligationRefs, want) {
					t.Fatalf("expected gate resolved_obligations to include %q: %#v", want, report.GateCoverage.ResolvedObligationRefs)
				}
			}
		})
	}
}

func TestRunnerAutonomyRecorderOpensBeforeInitialResolvedObligations(t *testing.T) {
	var runEvents []RunEvent
	runner := &Runner{
		AgentName: "main",
		OnEvent: func(event RunEvent) {
			runEvents = append(runEvents, event)
		},
	}
	brief := workerruntime.WorkerRuntimeBrief{
		Lane:              "explore",
		TaskFamily:        "analysis",
		Contract:          "direct_answer",
		PermissionProfile: "read_only_workspace",
		AllowedTools:      []string{"read_file"},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read README.md lines 1-20", Output: "README.md"},
		{Tool: "read_file", Summary: "Read docs/decisioning-north-star.md lines 1-20", Output: "docs/decisioning-north-star.md"},
	}
	if !readOnlyAnalysisReadyFromEvidence(results) {
		t.Fatal("read-only evidence fixture should start with the evidence obligation resolved")
	}
	policy := RunPolicy{MaxToolSteps: 5, MaxModelCalls: 2}
	recorder := newRunnerAutonomyRecorder(runner, policy, brief, results)
	if recorder == nil {
		t.Fatal("newRunnerAutonomyRecorder returned nil")
	}

	recorder.emitContractStarted(brief)
	recorder.emitCompletionGate(autonomy.PolicyAllow, "final_answer_accepted", results)
	recorder.emitLoopCloseout("completed")
	recorder.emitBudgetGate(policy, runnerMetrics{ToolCalls: 2, ModelCalls: 1}, "completed", false)

	events := decodeRunnerAutonomyEventsForTest(t, runEvents)
	const evidenceID = "obl-evidence-read-only"
	openedSeq, resolvedSeq := int64(0), int64(0)
	for _, event := range events {
		if event.Obligation == nil || event.Obligation.ObligationID != evidenceID {
			continue
		}
		switch event.Type {
		case autonomy.EventObligationOpened:
			openedSeq = event.Sequence
		case autonomy.EventObligationResolved:
			resolvedSeq = event.Sequence
		}
	}
	if openedSeq == 0 || resolvedSeq == 0 || openedSeq >= resolvedSeq {
		t.Fatalf("initially resolved obligation must emit opened before resolved, opened=%d resolved=%d events=%#v", openedSeq, resolvedSeq, events)
	}
	report := autonomy.AuditEvents(events, autonomy.EventAuditOptions{})
	if len(report.ObligationCoverage.UnmatchedObligations) != 0 {
		t.Fatalf("initially resolved obligation should have a matching opened event: %#v", report.ObligationCoverage.UnmatchedObligations)
	}
	if !report.AllClear {
		t.Fatalf("expected all-clear read-only lifecycle audit, all_clear=%v obligations=%#v findings=%#v", report.AllClear, report.ObligationCoverage, report.Findings)
	}
}

func TestRunnerAutonomyRecorderWriteGateResolvesOnlyChangedRequiredPath(t *testing.T) {
	root := t.TempDir()
	var runEvents []RunEvent
	runner := &Runner{
		AgentName: "main",
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		OnEvent: func(event RunEvent) {
			runEvents = append(runEvents, event)
		},
	}
	brief := workerruntime.WorkerRuntimeBrief{
		Lane:                 "implement",
		TaskFamily:           "implementation",
		Contract:             "implementation_result",
		PermissionProfile:    "guarded_workspace_write",
		RequiredEditPaths:    []string{"a.txt", "./b.txt"},
		AllowedTools:         []string{"write_file", "shell"},
		VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
	}
	recorder := newRunnerAutonomyRecorder(runner, RunPolicy{MaxToolSteps: 5}, brief, nil)
	if recorder == nil {
		t.Fatal("newRunnerAutonomyRecorder returned nil")
	}

	recorder.emitContractStarted(brief)
	recorder.emitWriteGate(workspace.ToolRequest{Tool: "write_file", Path: "a.txt"}, autonomy.PolicyAllow, "write_result_completed", []string{filepath.Join(root, "a.txt")})

	events := decodeRunnerAutonomyEventsForTest(t, runEvents)
	report := autonomy.AuditEvents(events, autonomy.EventAuditOptions{})
	firstWriteID := runnerAutonomyObligationID(autonomy.ObligationWrite, "a.txt")
	secondWriteID := runnerAutonomyObligationID(autonomy.ObligationWrite, "b.txt")
	if !hasAutonomyObligationEvent(events, autonomy.EventObligationResolved, firstWriteID) {
		t.Fatalf("expected changed path write obligation %q to resolve", firstWriteID)
	}
	if hasAutonomyObligationEvent(events, autonomy.EventObligationResolved, secondWriteID) {
		t.Fatalf("unchanged required path write obligation %q must remain unresolved", secondWriteID)
	}
	if !hasUnresolvedAutonomyObligation(report, secondWriteID) {
		t.Fatalf("expected audit to preserve unresolved write obligation %q, got %#v", secondWriteID, report.ObligationCoverage.UnresolvedObligations)
	}
	if !slices.Contains(report.GateCoverage.ResolvedObligationRefs, firstWriteID) {
		t.Fatalf("write gate should report resolved changed-path obligation %q: %#v", firstWriteID, report.GateCoverage.ResolvedObligationRefs)
	}
	if !slices.Contains(report.GateCoverage.OpenObligationRefs, secondWriteID) {
		t.Fatalf("write gate should report open unchanged-path obligation %q: %#v", secondWriteID, report.GateCoverage.OpenObligationRefs)
	}
}

func TestRunnerAutonomyRecorderWriteGateResolvesWorkspaceChangeWithoutRequiredPaths(t *testing.T) {
	var runEvents []RunEvent
	runner := &Runner{
		AgentName: "main",
		OnEvent: func(event RunEvent) {
			runEvents = append(runEvents, event)
		},
	}
	brief := workerruntime.WorkerRuntimeBrief{
		Lane:                 "implement",
		TaskFamily:           "implementation",
		Contract:             "implementation_result",
		PermissionProfile:    "guarded_workspace_write",
		AllowedTools:         []string{"write_file", "shell"},
		VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
	}
	recorder := newRunnerAutonomyRecorder(runner, RunPolicy{MaxToolSteps: 5}, brief, nil)
	if recorder == nil {
		t.Fatal("newRunnerAutonomyRecorder returned nil")
	}

	recorder.emitContractStarted(brief)
	recorder.emitWriteGate(workspace.ToolRequest{Tool: "write_file", Path: "generated.txt"}, autonomy.PolicyAllow, "write_result_completed", nil)

	events := decodeRunnerAutonomyEventsForTest(t, runEvents)
	report := autonomy.AuditEvents(events, autonomy.EventAuditOptions{})
	const workspaceWriteID = "obl-write-workspace-change"
	if !hasAutonomyObligationEvent(events, autonomy.EventObligationResolved, workspaceWriteID) {
		t.Fatalf("expected no-required-path workspace write obligation %q to resolve", workspaceWriteID)
	}
	if hasUnresolvedAutonomyObligation(report, workspaceWriteID) {
		t.Fatalf("workspace write obligation %q should not remain unresolved: %#v", workspaceWriteID, report.ObligationCoverage.UnresolvedObligations)
	}
	if !slices.Contains(report.GateCoverage.ResolvedObligationRefs, workspaceWriteID) {
		t.Fatalf("write gate should report resolved workspace write obligation %q: %#v", workspaceWriteID, report.GateCoverage.ResolvedObligationRefs)
	}
}

func TestRunnerAutonomyRecorderRejectedGateKeepsObligationUnresolved(t *testing.T) {
	var runEvents []RunEvent
	runner := &Runner{
		AgentName: "main",
		OnEvent: func(event RunEvent) {
			runEvents = append(runEvents, event)
		},
	}
	brief := workerruntime.WorkerRuntimeBrief{
		Lane:                 "implement",
		TaskFamily:           "implementation",
		Contract:             "implementation_result",
		PermissionProfile:    "guarded_workspace_write",
		RequiredEditPaths:    []string{"internal/agent/runner.go"},
		AllowedTools:         []string{"read_file", "apply_patch", "shell"},
		VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
	}
	recorder := newRunnerAutonomyRecorder(runner, RunPolicy{MaxToolSteps: 5}, brief, nil)
	if recorder == nil {
		t.Fatal("newRunnerAutonomyRecorder returned nil")
	}

	recorder.emitContractStarted(brief)
	recorder.emitWriteGate(workspace.ToolRequest{Tool: "apply_patch", Path: "README.md"}, autonomy.PolicyReject, "required_edit_scope_rejected", nil)

	events := decodeRunnerAutonomyEventsForTest(t, runEvents)
	report := autonomy.AuditEvents(events, autonomy.EventAuditOptions{})
	writeObligationID := runnerAutonomyObligationID(autonomy.ObligationWrite, "internal/agent/runner.go")
	if report.AllClear {
		t.Fatal("expected rejected write gate to leave audit non-clear")
	}
	if !hasUnresolvedAutonomyObligation(report, writeObligationID) {
		t.Fatalf("expected unresolved write obligation %q, got %#v", writeObligationID, report.ObligationCoverage.UnresolvedObligations)
	}
	if hasAutonomyObligationEvent(events, autonomy.EventObligationResolved, writeObligationID) {
		t.Fatalf("rejected write gate must not emit obligation_resolved for %q", writeObligationID)
	}
}

func TestRunnerAutonomyRecorderRejectedCompletionReportsOnlyCurrentlyOpenObligations(t *testing.T) {
	root := t.TempDir()
	var runEvents []RunEvent
	runner := &Runner{
		AgentName: "main",
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		OnEvent: func(event RunEvent) {
			runEvents = append(runEvents, event)
		},
	}
	brief := workerruntime.WorkerRuntimeBrief{
		Lane:                 "implement",
		TaskFamily:           "implementation",
		Contract:             "implementation_result",
		PermissionProfile:    "guarded_workspace_write",
		RequiredEditPaths:    []string{"internal/agent/runner.go"},
		AllowedTools:         []string{"write_file", "shell"},
		VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
	}
	recorder := newRunnerAutonomyRecorder(runner, RunPolicy{MaxToolSteps: 5}, brief, nil)
	if recorder == nil {
		t.Fatal("newRunnerAutonomyRecorder returned nil")
	}

	recorder.emitContractStarted(brief)
	recorder.emitWriteGate(workspace.ToolRequest{Tool: "write_file", Path: "internal/agent/runner.go"}, autonomy.PolicyAllow, "write_result_completed", []string{"internal/agent/runner.go"})
	recorder.emitCompletionGate(autonomy.PolicyReject, "required_obligations_open", []workspace.ToolResult{
		{Tool: "write_file", Summary: "Wrote internal/agent/runner.go"},
	})

	events := decodeRunnerAutonomyEventsForTest(t, runEvents)
	report := autonomy.AuditEvents(events, autonomy.EventAuditOptions{})
	writeID := runnerAutonomyObligationID(autonomy.ObligationWrite, "internal/agent/runner.go")
	if slices.Contains(report.GateCoverage.OpenObligationRefs, writeID) {
		t.Fatalf("completion gate must not report resolved write obligation %q as open: %#v", writeID, report.GateCoverage.OpenObligationRefs)
	}
	if len(report.GateCoverage.InconsistentOpenObligationRefs) != 0 {
		t.Fatalf("completion gate reported non-open obligations as open: %#v", report.GateCoverage.InconsistentOpenObligationRefs)
	}
}

func TestRunnerAutonomyRecorderCompletionAllowDoesNotResolveFailedVerification(t *testing.T) {
	root := t.TempDir()
	var runEvents []RunEvent
	runner := &Runner{
		AgentName: "main",
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		OnEvent: func(event RunEvent) {
			runEvents = append(runEvents, event)
		},
	}
	brief := workerruntime.WorkerRuntimeBrief{
		Lane:                 "implement",
		TaskFamily:           "implementation",
		Contract:             "implementation_result",
		PermissionProfile:    "guarded_workspace_write",
		RequiredEditPaths:    []string{"internal/agent/runner.go"},
		AllowedTools:         []string{"write_file", "source_sanity"},
		VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
	}
	recorder := newRunnerAutonomyRecorder(runner, RunPolicy{MaxToolSteps: 5}, brief, nil)
	if recorder == nil {
		t.Fatal("newRunnerAutonomyRecorder returned nil")
	}

	results := []workspace.ToolResult{
		{Tool: "write_file", Summary: "Wrote internal/agent/runner.go"},
		{Tool: "source_sanity", Summary: "Source sanity failed", Error: "internal/agent/runner.go:12: undefined: missingSymbol"},
	}
	recorder.emitContractStarted(brief)
	recorder.emitWriteGate(workspace.ToolRequest{Tool: "write_file", Path: "internal/agent/runner.go"}, autonomy.PolicyAllow, "write_result_completed", []string{"internal/agent/runner.go"})
	recorder.emitCompletionGate(autonomy.PolicyAllow, "final_answer_accepted", results)

	events := decodeRunnerAutonomyEventsForTest(t, runEvents)
	report := autonomy.AuditEvents(events, autonomy.EventAuditOptions{})
	if hasAutonomyObligationEvent(events, autonomy.EventObligationResolved, "obl-verify-post-write") {
		t.Fatalf("failed verification must not emit resolved obligation for verify: %#v", events)
	}
	if !hasUnresolvedAutonomyObligation(report, "obl-verify-post-write") {
		t.Fatalf("failed verification should remain unresolved: %#v", report.ObligationCoverage)
	}
	foundFailedCompletion := false
	for _, event := range events {
		if event.Type == autonomy.EventCompletionGateDecision &&
			event.Completion != nil &&
			event.Completion.VerificationStatus == "failed" &&
			slices.Contains(event.Completion.RemainingObligations, "obl-verify-post-write") {
			foundFailedCompletion = true
		}
	}
	if !foundFailedCompletion {
		t.Fatalf("completion gate decision should carry failed verification and open verify obligation: %#v", events)
	}
}

func decodeRunnerAutonomyEventsForTest(t *testing.T, runEvents []RunEvent) []autonomy.Event {
	t.Helper()
	var events []autonomy.Event
	for _, runEvent := range runEvents {
		if runEvent.Role != runnerAutonomyEventRole {
			continue
		}
		var event autonomy.Event
		if err := json.Unmarshal([]byte(runEvent.Content), &event); err != nil {
			t.Fatalf("decode autonomy event: %v\n%s", err, runEvent.Content)
		}
		if err := event.Validate(); err != nil {
			t.Fatalf("invalid autonomy event: %v\n%#v", err, event)
		}
		events = append(events, event)
	}
	if len(events) == 0 {
		t.Fatal("expected autonomy events")
	}
	return events
}

func countObligationKind(obligations []autonomy.Obligation, kind autonomy.ObligationKind) int {
	count := 0
	for _, obligation := range obligations {
		if obligation.Kind == kind {
			count++
		}
	}
	return count
}

func hasPathRole(roles []autonomy.PathRole, kind autonomy.PathRoleKind, pattern string) bool {
	for _, role := range roles {
		if role.Role == kind && role.PathPattern == pattern {
			return true
		}
	}
	return false
}

func containsAutonomyCapability(values []autonomy.Capability, want autonomy.Capability) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func hasAutonomyObligationEvent(events []autonomy.Event, eventType autonomy.EventType, obligationID string) bool {
	for _, event := range events {
		if event.Type == eventType && event.Obligation != nil && event.Obligation.ObligationID == obligationID {
			return true
		}
	}
	return false
}

func hasUnresolvedAutonomyObligation(report autonomy.EventAuditReport, obligationID string) bool {
	for _, ref := range report.ObligationCoverage.UnresolvedObligations {
		if ref.ObligationID == obligationID {
			return true
		}
	}
	return false
}

func mustAutonomyPolicyEvent(t *testing.T, events []autonomy.Event, eventType autonomy.EventType, toolName string, result string) autonomy.Event {
	t.Helper()
	for _, event := range events {
		if event.Type != eventType || event.Policy == nil {
			continue
		}
		if toolName != "" && event.Policy.ToolName != toolName {
			continue
		}
		if result != "" && event.Policy.Result != result {
			continue
		}
		return event
	}
	t.Fatalf("missing policy event type=%q tool=%q result=%q in %#v", eventType, toolName, result, events)
	return autonomy.Event{}
}

func mustAutonomyPolicyEventByInvariant(t *testing.T, events []autonomy.Event, eventType autonomy.EventType, invariantID string, result string) autonomy.Event {
	t.Helper()
	for _, event := range events {
		if event.Type != eventType || event.Policy == nil {
			continue
		}
		if event.Policy.InvariantID == invariantID && (result == "" || event.Policy.Result == result) {
			return event
		}
	}
	t.Fatalf("missing policy event type=%q invariant=%q result=%q in %#v", eventType, invariantID, result, events)
	return autonomy.Event{}
}

func policyEventPaths(policy *autonomy.ContractPolicyEvent) []string {
	if policy == nil {
		return nil
	}
	seen := make(map[string]struct{}, len(policy.PathRoles))
	var paths []string
	for _, match := range policy.PathRoles {
		if _, ok := seen[match.Path]; ok || match.Path == "" {
			continue
		}
		seen[match.Path] = struct{}{}
		paths = append(paths, match.Path)
	}
	return paths
}
