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
	"idiom/internal/contractcompile"
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

func TestRunnerAutonomyPlanExecutePlannerUsesSharedToolMetadata(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		Lane:              "plan-execute",
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "submit_plan",
		PermissionProfile: "guarded_workspace_write",
	}

	for _, entry := range autonomy.PlanExecutePlannerToolMetadataEntries() {
		t.Run(entry.Tool, func(t *testing.T) {
			if got := runnerAutonomyCapabilitiesForTool(brief, entry.Tool); !slices.Equal(got, entry.Capabilities) {
				t.Fatalf("runner capabilities = %#v, want metadata capabilities %#v", got, entry.Capabilities)
			}
			req := workspace.ToolRequest{
				Tool:    entry.Tool,
				Command: "go test ./internal/agent",
				Content: "*** Begin Patch\n*** Delete File: doomed.txt\n*** End Patch\n",
			}
			if got := runnerAutonomySideEffectForBrief(brief, req); got != entry.SideEffect {
				t.Fatalf("runner side effect = %q, want metadata side effect %q", got, entry.SideEffect)
			}
		})
	}
}

func TestRunnerAutonomyPlanExecutePlannerPolicyEventsUseSharedToolMetadata(t *testing.T) {
	tests := []struct {
		tool       string
		capability autonomy.Capability
		effect     autonomy.SideEffectClass
	}{
		{tool: "submit_plan", capability: autonomy.CapabilityPlanSubmission, effect: autonomy.SideEffectAdditive},
		{tool: "read_file", capability: autonomy.CapabilityBoundedRead, effect: autonomy.SideEffectReadOnly},
		{tool: "search_content", capability: autonomy.CapabilityLiteralSearch, effect: autonomy.SideEffectReadOnly},
		{tool: "context_status", capability: autonomy.CapabilitySemanticDiscovery, effect: autonomy.SideEffectReadOnly},
		{tool: "harness_feedback", capability: autonomy.CapabilitySemanticDiscovery, effect: autonomy.SideEffectAdditive},
		{tool: "product_status", capability: autonomy.CapabilityBoundedRead, effect: autonomy.SideEffectReadOnly},
		{tool: "apply_patch", capability: autonomy.CapabilityApplyPatch, effect: autonomy.SideEffectAdditive},
		{tool: "shell", capability: autonomy.CapabilityShellVerification, effect: autonomy.SideEffectUnknown},
		{tool: "task", capability: autonomy.CapabilityDelegation, effect: autonomy.SideEffectAdditive},
		{tool: "recovery_decompose", capability: autonomy.CapabilityExternalWrite, effect: autonomy.SideEffectAdditive},
	}

	for _, tt := range tests {
		t.Run(tt.tool, func(t *testing.T) {
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
				AllowedTools:      []string{tt.tool},
			}
			recorder := newRunnerAutonomyRecorder(runner, RunPolicy{}, brief, nil)
			if recorder == nil {
				t.Fatal("newRunnerAutonomyRecorder returned nil")
			}

			recorder.emitIntentClassified(workspace.ToolRequest{
				Tool:    tt.tool,
				Command: "go test ./internal/agent",
				Content: "*** Begin Patch\n*** Delete File: doomed.txt\n*** End Patch\n",
			})

			events := decodeRunnerAutonomyEventsForTest(t, runEvents)
			intent := mustAutonomyPolicyEvent(t, events, autonomy.EventToolIntentClassified, tt.tool, "")
			if intent.Policy.Capability != tt.capability || intent.Policy.SideEffect != tt.effect {
				t.Fatalf("policy event classification = capability %q side_effect %q, want %q/%q", intent.Policy.Capability, intent.Policy.SideEffect, tt.capability, tt.effect)
			}
		})
	}
}

func TestRunnerAutonomySharedPlannerMetadataDoesNotChangeNonPlannerFallback(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		Lane:              "implement",
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
	}

	if got := runnerAutonomyCapabilitiesForTool(brief, "code_navigate"); len(got) != 0 {
		t.Fatalf("non-planner code_navigate should keep legacy fallback capabilities, got %#v", got)
	}
	if got := runnerAutonomySideEffectForBrief(brief, workspace.ToolRequest{Tool: "shell", Command: "go test ./internal/agent"}); got != autonomy.SideEffectReadOnly {
		t.Fatalf("non-planner shell verification side effect = %q, want legacy read_only", got)
	}
	if got := runnerAutonomySideEffectForBrief(brief, workspace.ToolRequest{
		Tool:    "apply_patch",
		Content: "*** Begin Patch\n*** Delete File: doomed.txt\n*** End Patch\n",
	}); got != autonomy.SideEffectDestructive {
		t.Fatalf("non-planner delete patch side effect = %q, want legacy destructive", got)
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
	modelProtocol, ok := runner.compileModelProtocolProjection(brief, results, contract, RunPolicy{}, true)
	if !ok {
		t.Fatal("expected model protocol projection")
	}
	modelProtocol = recorder.bindModelProtocolProjection(modelProtocol)
	recorder.emitTurnPhaseContract(contract, modelProtocol)

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
		phase.ProtocolHash != modelProtocol.ProtocolHash ||
		!slices.Contains(phase.ActionPolicy.AllowedActionClasses, actionSubmitPlan) ||
		!slices.Contains(phase.RuntimeCall.ExpressibleActionClasses, actionSourceSatisfied) {
		t.Fatalf("unexpected turn phase payload: %#v", phase)
	}
	if presented.Lifecycle.ProtocolHash != modelProtocol.ProtocolHash ||
		presented.Lifecycle.ModelProtocol == nil ||
		presented.Lifecycle.ModelProtocol.ProtocolHash != modelProtocol.ProtocolHash {
		t.Fatalf("contract_presented missing model protocol hash: %#v", presented.Lifecycle)
	}
	report := autonomy.AuditEvents(events, autonomy.EventAuditOptions{})
	if report.TurnPhaseCoverage.Total != 1 || len(report.TurnPhaseCoverage.Contradictions) != 0 {
		t.Fatalf("expected clean turn phase audit coverage, got %#v", report.TurnPhaseCoverage)
	}
}

func TestRunnerAutonomyPlannerContractProtocolAndGatesShareCompiledAuthority(t *testing.T) {
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
		AllowedTools:      []string{"read_file", "code_navigate", "submit_plan"},
		RequiredTools:     []string{"submit_plan"},
	}
	results := []workspace.ToolResult{{Tool: "read_file", Summary: "Read f1 a.go lines 1-10 (total lines: 10)"}}
	policy := RunPolicy{MaxToolSteps: 2, MaxModelCalls: 1}
	recorder := newRunnerAutonomyRecorder(runner, policy, brief, results)
	if recorder == nil {
		t.Fatal("newRunnerAutonomyRecorder returned nil")
	}
	if !recorder.hasPlannerCompile {
		t.Fatal("plan-execute planner recorder should retain compiled contractcompile result")
	}
	binding := recorder.activeAuthority
	if binding.ContractID != recorder.plannerCompile.ActiveAuthority.ContractID ||
		binding.RuntimeContractID != recorder.plannerCompile.ActiveAuthority.RuntimeContractID ||
		binding.ProtocolHash != recorder.plannerCompile.ActiveAuthority.ProtocolHash ||
		binding.ActiveInvariantID != contractcompile.InvariantActiveAuthority {
		t.Fatalf("recorder should expose compile result active authority, got %#v want %#v", binding, recorder.plannerCompile.ActiveAuthority)
	}
	if recorder.contract.Scope.InvocationKind != autonomy.InvocationPhase ||
		recorder.contract.Scope.TaskFamily != contractcompile.InvocationPlanExecutePlanner ||
		recorder.contract.Scope.Phase != contractcompile.DefaultPlannerPhase {
		t.Fatalf("planner recorder should use compiled phase contract scope, got %#v", recorder.contract.Scope)
	}
	recorder.emitContractStarted(brief)

	phase := deriveTurnPhaseContract(TurnPhaseContractInput{
		ContractID:         binding.ContractID,
		ModelCallID:        binding.ContractID + ":model_call_1",
		Step:               1,
		Brief:              brief,
		Results:            results,
		BaseAllowedTools:   []string{"read_file", "code_navigate", "submit_plan"},
		TextualToolParser:  true,
		VerificationStatus: "not_required",
	})
	protocol, ok := runner.compileModelProtocolProjection(brief, results, phase, policy, true)
	if !ok {
		t.Fatal("expected model protocol projection")
	}
	protocol = recorder.bindModelProtocolProjection(protocol)
	if protocol.ContractID != recorder.contract.ContractID {
		t.Fatalf("model protocol should cite compiled recorder contract %q, got %q", recorder.contract.ContractID, protocol.ContractID)
	}
	recorder.emitTurnPhaseContract(phase, protocol)
	recorder.markModelProtocolPresented(protocol)
	recorder.emitCompletionGate(autonomy.PolicyReject, "required_obligations_open", results)

	events := decodeRunnerAutonomyEventsForTest(t, runEvents)
	var compiledID, compiledRuntimeID, compiledProtocolHash, compiledInvariantID, presentedID, presentedProtocolID, presentedProtocolHash, presentedInvariantID, gateID, gateProtocolHash, gateInvariantID string
	for _, event := range events {
		switch event.Type {
		case autonomy.EventContractCompiled:
			compiledID = event.ContractID
			if event.Lifecycle == nil || event.Lifecycle.Summary == nil || event.Lifecycle.Summary.ContractID != recorder.contract.ContractID {
				t.Fatalf("contract_compiled summary should carry compiled contract id: %#v", event.Lifecycle)
			}
			compiledProtocolHash = event.Lifecycle.ProtocolHash
			compiledInvariantID = event.Lifecycle.InvariantID
			for _, source := range event.Lifecycle.Summary.AuthoritySources {
				if source == binding.RuntimeContractID {
					compiledRuntimeID = source
				}
			}
		case autonomy.EventContractPresented:
			if event.Lifecycle != nil && event.Lifecycle.ModelProtocol != nil {
				presentedID = event.ContractID
				presentedProtocolID = event.Lifecycle.ModelProtocol.ContractID
				presentedProtocolHash = event.Lifecycle.ModelProtocol.ProtocolHash
				presentedInvariantID = event.Lifecycle.ModelProtocol.ActiveInvariantID
			}
		case autonomy.EventGateDecision:
			if event.Gate != nil && event.Gate.Gate == autonomy.GateCompletion {
				gateID = event.ContractID
				gateProtocolHash = event.Gate.ProtocolHash
				gateInvariantID = event.Gate.InvariantID
			}
		}
	}
	if compiledID == "" || presentedID == "" || gateID == "" {
		t.Fatalf("missing compiled/presented/gate events: compiled=%q presented=%q gate=%q events=%#v", compiledID, presentedID, gateID, events)
	}
	if compiledRuntimeID != binding.RuntimeContractID ||
		compiledProtocolHash != binding.ProtocolHash ||
		compiledInvariantID != binding.ActiveInvariantID {
		t.Fatalf("contract_started did not expose active authority binding: runtime=%q protocol=%q invariant=%q binding=%#v", compiledRuntimeID, compiledProtocolHash, compiledInvariantID, binding)
	}
	for label, got := range map[string]string{
		"contract_compiled":        compiledID,
		"contract_presented":       presentedID,
		"model_protocol_contract":  presentedProtocolID,
		"completion_gate_contract": gateID,
	} {
		if got != recorder.contract.ContractID {
			t.Fatalf("%s contract id = %q, want compiled recorder id %q", label, got, recorder.contract.ContractID)
		}
	}
	if presentedProtocolHash == "" ||
		compiledProtocolHash != presentedProtocolHash ||
		gateProtocolHash != presentedProtocolHash ||
		gateProtocolHash != protocol.ProtocolHash {
		t.Fatalf("protocol hash drift: compiled=%q presented=%q gate=%q protocol=%q", compiledProtocolHash, presentedProtocolHash, gateProtocolHash, protocol.ProtocolHash)
	}
	if presentedInvariantID != binding.ActiveInvariantID || gateInvariantID == "" {
		t.Fatalf("model protocol/gate events should carry active authority invariant, presented=%q gate=%q binding=%#v", presentedInvariantID, gateInvariantID, binding)
	}
}

func TestRunnerAutonomyPlannerAuthorityIgnoresStalePhaseContractID(t *testing.T) {
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
	staleID := "contract_stale_phase"
	phase := deriveTurnPhaseContract(TurnPhaseContractInput{
		ContractID:         staleID,
		ModelCallID:        staleID + ":model_call_1",
		Step:               1,
		Brief:              brief,
		Results:            results,
		BaseAllowedTools:   []string{"submit_plan"},
		TextualToolParser:  true,
		VerificationStatus: "not_required",
	})
	protocol, ok := runner.compileModelProtocolProjection(brief, results, phase, RunPolicy{}, true)
	if !ok {
		t.Fatal("expected model protocol projection")
	}
	protocol = recorder.bindModelProtocolProjection(protocol)
	recorder.emitTurnPhaseContract(phase, protocol)
	recorder.markModelProtocolPresented(protocol)
	recorder.emitCompletionGate(autonomy.PolicyReject, "required_obligations_open", results)

	events := decodeRunnerAutonomyEventsForTest(t, runEvents)
	for _, event := range events {
		switch event.Type {
		case autonomy.EventContractPresented:
			if event.ContractID != recorder.contract.ContractID {
				t.Fatalf("presented event adopted stale contract id %q, want %q", event.ContractID, recorder.contract.ContractID)
			}
			if event.Lifecycle == nil || event.Lifecycle.ModelProtocol == nil {
				continue
			}
			if event.Lifecycle.ModelProtocol.ContractID != recorder.contract.ContractID {
				t.Fatalf("model protocol adopted stale contract id %q, want %q", event.Lifecycle.ModelProtocol.ContractID, recorder.contract.ContractID)
			}
			if event.Lifecycle.ModelProtocol.ProtocolHash != recorder.activeAuthority.ProtocolHash {
				t.Fatalf("model protocol hash = %q, want compiled binding %q", event.Lifecycle.ModelProtocol.ProtocolHash, recorder.activeAuthority.ProtocolHash)
			}
		case autonomy.EventGateDecision:
			if event.Gate != nil && event.Gate.Gate == autonomy.GateCompletion {
				if event.ContractID != recorder.contract.ContractID {
					t.Fatalf("gate event adopted stale contract id %q, want %q", event.ContractID, recorder.contract.ContractID)
				}
				if event.Gate.ProtocolHash != recorder.activeAuthority.ProtocolHash {
					t.Fatalf("gate protocol hash = %q, want compiled binding %q", event.Gate.ProtocolHash, recorder.activeAuthority.ProtocolHash)
				}
			}
		}
	}
}

func TestRunnerAutonomyRecorderEmitsNextActionSurfaceSummary(t *testing.T) {
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
	phase := deriveTurnPhaseContract(TurnPhaseContractInput{
		ContractID:         recorder.contract.ContractID,
		ModelCallID:        recorder.contract.ContractID + ":model_call_1",
		Step:               1,
		Brief:              brief,
		Results:            results,
		BaseAllowedTools:   []string{"submit_plan"},
		TextualToolParser:  true,
		VerificationStatus: "not_required",
	})
	protocol, ok := runner.compileModelProtocolProjection(brief, results, phase, RunPolicy{}, true)
	if !ok {
		t.Fatal("expected model protocol projection")
	}
	protocol = recorder.bindModelProtocolProjection(protocol)
	recorder.emitTurnPhaseContract(phase, protocol)
	state := applyTurnPhaseContractToNextActionState(runnerNextActionState{
		Lane:   "main",
		Phase:  "generic",
		Step:   1,
		Reason: "test",
	}, phase)

	recorder.emitTurnPhaseSurfaceSummary(turnPhaseSurfaceSummaryFromNextActionState(state, phase), phase)

	events := decodeRunnerAutonomyEventsForTest(t, runEvents)
	report := autonomy.AuditEvents(events, autonomy.EventAuditOptions{})
	if report.TurnPhaseCoverage.SurfaceCounts["next_action_state"] != 1 {
		t.Fatalf("expected next_action_state surface coverage, got %#v", report.TurnPhaseCoverage.SurfaceCounts)
	}
	if len(report.TurnPhaseCoverage.Contradictions) != 0 {
		t.Fatalf("expected clean emitted next-action surface, got %#v", report.TurnPhaseCoverage.Contradictions)
	}
}

func TestPlanExecuteHistoryPendingModelProtocolUsesCanonicalReadAction(t *testing.T) {
	runner := &Runner{}
	brief := workerruntime.WorkerRuntimeBrief{
		Lane:              "plan-execute",
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "submit_plan",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"a.go"},
		RequiredEvidence:  []string{requiredEvidenceGitHistoryRationale},
		AllowedTools: []string{
			"read_file",
			"context_impact",
			"harness_feedback",
			"history_range",
			"history_symbol",
			"blame_range",
			"submit_plan",
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 a.go lines 1-10 (total lines: 10)",
		Output:  "1:package a\n",
	}}
	phase := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief:             brief,
		Results:           results,
		BaseAllowedTools:  brief.AllowedTools,
		TextualToolParser: true,
		ToolTransportMode: "textual",
	})
	if phase.PhaseID != turnPhaseHistoryPending {
		t.Fatalf("phase = %q, want %q", phase.PhaseID, turnPhaseHistoryPending)
	}
	phase.AllowedTools = uniquePhaseStrings(append(phase.AllowedTools, "blame_range", "context_impact", "harness_feedback"))
	phase.ActionPolicy.CallableToolNames = uniquePhaseStrings(append(phase.ActionPolicy.CallableToolNames, "blame_range", "context_impact", "harness_feedback"))
	protocol, ok := runner.compileModelProtocolProjection(brief, results, phase, RunPolicy{}, true)
	if !ok {
		t.Fatal("expected history-pending model protocol projection to compile")
	}
	if !slices.Contains(protocol.CallableActionClasses, actionRead) {
		t.Fatalf("history-pending protocol should expose history tools as read actions: %#v", protocol.CallableActionClasses)
	}
	for _, tool := range []string{"blame_range", "context_impact", "harness_feedback", "history_range", "history_symbol"} {
		if !slices.Contains(protocol.VisibleTools, tool) {
			t.Fatalf("history-pending protocol should preserve visible planner tool %s: %#v", tool, protocol.VisibleTools)
		}
		if !slices.Contains(protocol.CallableTools, tool) {
			t.Fatalf("history-pending protocol should preserve callable planner tool %s: %#v", tool, protocol.CallableTools)
		}
	}
	if slices.Contains(protocol.VisibleTools, "submit_plan") || slices.Contains(protocol.CallableTools, "submit_plan") {
		t.Fatalf("history-pending protocol must not expose submit_plan before history evidence, visible=%#v callable=%#v", protocol.VisibleTools, protocol.CallableTools)
	}
	if slices.Contains(protocol.StructuredSchemaNames, "submit_plan") {
		t.Fatalf("history-pending protocol must not expose submit_plan schema before history evidence: %#v", protocol.StructuredSchemaNames)
	}
	if slices.Contains(protocol.CallableActionClasses, "history") {
		t.Fatalf("history-pending protocol should not expose non-canonical history action class: %#v", protocol.CallableActionClasses)
	}
	if slices.Contains(protocol.InvalidActionClasses, actionRead) {
		t.Fatalf("history-pending protocol should not invalidate read action class: %#v", protocol.InvalidActionClasses)
	}
}

func TestPlanExecuteRequiredSourceModelProtocolDoesNotLeakSubmitPlan(t *testing.T) {
	runner := &Runner{}
	brief := workerruntime.WorkerRuntimeBrief{
		Lane:              "plan-execute",
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "submit_plan",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"a.go"},
		AllowedTools:      []string{"read_file", "code_navigate", "history_range", "history_symbol", "submit_plan"},
	}
	phase := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief:             brief,
		BaseAllowedTools:  brief.AllowedTools,
		TextualToolParser: true,
		ToolTransportMode: "textual",
	})
	if phase.PhaseID != turnPhaseRequiredSource {
		t.Fatalf("phase = %q, want %q", phase.PhaseID, turnPhaseRequiredSource)
	}
	protocol, ok := runner.compileModelProtocolProjection(brief, nil, phase, RunPolicy{}, true)
	if !ok {
		t.Fatal("expected required-source model protocol projection to compile")
	}
	if slices.Contains(protocol.VisibleTools, "submit_plan") || slices.Contains(protocol.CallableTools, "submit_plan") {
		t.Fatalf("required-source protocol must not expose submit_plan before source evidence, visible=%#v callable=%#v", protocol.VisibleTools, protocol.CallableTools)
	}
	if slices.Contains(protocol.StructuredSchemaNames, "submit_plan") {
		t.Fatalf("required-source protocol must not expose submit_plan schema before source evidence: %#v", protocol.StructuredSchemaNames)
	}
	for _, want := range []string{"read_file", "code_navigate"} {
		if !slices.Contains(protocol.VisibleTools, want) || !slices.Contains(protocol.CallableTools, want) {
			t.Fatalf("required-source protocol missing read tool %s, visible=%#v callable=%#v", want, protocol.VisibleTools, protocol.CallableTools)
		}
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
	contextPacketIndex := -1
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
			if decoded.Type == autonomy.EventModelExchange &&
				decoded.ModelExchange != nil &&
				decoded.ModelExchange.Kind == "context_packet" &&
				contextPacketIndex == -1 {
				contextPacketIndex = i
			}
		}
	}
	if turnPhaseIndex == -1 || contextPacketIndex == -1 || modelCallIndex == -1 || postModelIndex == -1 {
		t.Fatalf("missing expected event order markers: turn_phase=%d context_packet=%d model_call=%d post_model=%d roles=%#v", turnPhaseIndex, contextPacketIndex, modelCallIndex, postModelIndex, eventRolesFor(runEvents))
	}
	if !(turnPhaseIndex < contextPacketIndex && contextPacketIndex < modelCallIndex && modelCallIndex < postModelIndex) {
		t.Fatalf("expected turn phase/context packet before model_call before post-model events, got turn_phase=%d context_packet=%d model_call=%d post_model=%d", turnPhaseIndex, contextPacketIndex, modelCallIndex, postModelIndex)
	}
}

func TestRunnerRunCompletionFrictionDoesNotClaimUnpresentedModelProtocol(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "a.go", "package main\nfunc A() {}\n"); err != nil {
		t.Fatalf("write seed: %v", err)
	}
	var requestBodies []struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		requestBodies = append(requestBodies, payload)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": "Done."},
			}},
		})
	}))
	defer server.Close()

	var runEvents []RunEvent
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxToolSteps: 1, MaxModelCalls: 1},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Lane:              "plan-execute",
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "submit_plan",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.go"},
			AllowedTools:      []string{"read_file", "submit_plan"},
			RequiredTools:     []string{"submit_plan"},
		},
		OnEvent: func(event RunEvent) {
			runEvents = append(runEvents, event)
		},
	}
	_, _ = runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "plan edit"}}, llama.ChatOptions{}, nil, nil, nil)
	if len(requestBodies) == 0 {
		t.Fatalf("expected model request")
	}
	if !slices.ContainsFunc(requestBodies[0].Messages, func(message struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}) bool {
		return message.Role == "system" &&
			strings.Contains(message.Content, "[active contract protocol]") &&
			strings.Contains(message.Content, "protocol_hash=")
	}) {
		t.Fatalf("expected model-visible protocol card with hash: %#v", requestBodies)
	}

	events := decodeRunnerAutonomyEventsForTest(t, runEvents)
	var protocolHash string
	var friction *autonomy.ContractGuardFrictionEvent
	for _, event := range events {
		if event.Type == autonomy.EventContractPresented && event.Lifecycle != nil && event.Lifecycle.ModelProtocol != nil {
			protocolHash = event.Lifecycle.ModelProtocol.ProtocolHash
		}
		if event.Type == autonomy.EventGuardFriction && event.GuardFriction != nil &&
			event.GuardFriction.GuardSurface == autonomy.GuardFrictionSurfaceCompletionGate {
			friction = event.GuardFriction
		}
	}
	if friction == nil {
		t.Fatalf("expected completion guard friction, got roles=%#v", eventRolesFor(runEvents))
	}
	if protocolHash == "" {
		t.Fatalf("expected lifecycle model protocol hash, got events=%#v", events)
	}
	if friction.ProtocolHash != protocolHash || !friction.ModelContractVisible {
		t.Fatalf("completion friction must cite presented model protocol %q: %#v", protocolHash, friction)
	}
}

func TestRunnerRunRepeatedCompletionRejectionEmitsRetryFriction(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "a.go", "package main\nfunc A() {}\n"); err != nil {
		t.Fatalf("write seed: %v", err)
	}
	replies := []string{
		"Done.",
		"Still done.",
		"<<QUESTION>>: a.go cannot be edited because the exact requested change is missing from the brief.",
	}
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected request %d", requests+1)
		}
		reply := replies[requests]
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))
	defer server.Close()

	var runEvents []RunEvent
	runner := &Runner{
		Client:    llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		Policy:    RunPolicy{MaxToolSteps: 4, MaxModelCalls: 4},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Lane:              "plan-execute",
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "submit_plan",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.go"},
			AllowedTools:      []string{"read_file", "submit_plan"},
			RequiredTools:     []string{"submit_plan"},
		},
		OnEvent: func(event RunEvent) {
			runEvents = append(runEvents, event)
		},
	}
	_, _ = runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "plan edit"}}, llama.ChatOptions{}, nil, nil, nil)
	if requests < 3 {
		t.Fatalf("expected retry through accepted blocker, got %d request(s); roles=%#v", requests, eventRolesFor(runEvents))
	}

	events := decodeRunnerAutonomyEventsForTest(t, runEvents)
	var sawSameFailure bool
	var protocolHash string
	for _, event := range events {
		if event.Type == autonomy.EventContractPresented && event.Lifecycle != nil && event.Lifecycle.ModelProtocol != nil {
			protocolHash = event.Lifecycle.ModelProtocol.ProtocolHash
		}
		if event.Type != autonomy.EventGuardFriction || event.GuardFriction == nil ||
			event.GuardFriction.GuardSurface != autonomy.GuardFrictionSurfaceCompletionGate {
			continue
		}
		friction := event.GuardFriction
		if friction.RetryNumber == 1 &&
			friction.RetryOutcome == autonomy.GuardFrictionRetrySameFailure &&
			friction.FirstInvalidAction == actionFinalAnswer {
			sawSameFailure = true
		}
		if friction.ProtocolHash != protocolHash || !friction.ModelContractVisible {
			t.Fatalf("completion retry friction must cite presented protocol %q: %#v", protocolHash, friction)
		}
	}
	if !sawSameFailure {
		t.Fatalf("missing repeated same-failure completion friction; events=%#v", events)
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

func TestRunnerAutonomyRecorderDeniedPolicyEmitsGuardFriction(t *testing.T) {
	root := t.TempDir()
	var runEvents []RunEvent
	runner := &Runner{
		AgentName: "main",
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
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
		AllowedTools:      []string{"read_file", "apply_patch"},
	}
	recorder := newRunnerAutonomyRecorder(runner, RunPolicy{}, brief, nil)
	if recorder == nil {
		t.Fatal("newRunnerAutonomyRecorder returned nil")
	}
	recorder.emitContractStarted(brief)
	req := workspace.ToolRequest{Tool: "read_file", Path: ".git/config"}
	recorder.emitPolicyResult(req, runnerAutonomyPolicyEventInput{
		Decision:   autonomy.PolicyDeny,
		ReasonCode: "permission_policy",
		Result:     "denied",
		ErrorClass: "permission_policy",
		Source:     "runner_permission_policy",
	})

	events := decodeRunnerAutonomyEventsForTest(t, runEvents)
	var policyEventID string
	var friction *autonomy.ContractGuardFrictionEvent
	for _, event := range events {
		if event.Type == autonomy.EventToolPolicyResult {
			policyEventID = event.EventID
		}
		if event.Type == autonomy.EventGuardFriction {
			friction = event.GuardFriction
		}
	}
	if friction == nil {
		t.Fatalf("expected policy guard friction, got roles=%#v", eventRolesFor(runEvents))
	}
	if friction.SourceEventID != policyEventID || friction.GuardSurface != autonomy.GuardFrictionSurfaceToolPolicy {
		t.Fatalf("unexpected policy friction source/surface: source=%q friction=%#v", policyEventID, friction)
	}
	if friction.Decision != autonomy.GuardFrictionDecisionReject ||
		friction.FirstInvalidAction != "read_file" ||
		friction.ModelContractVisible {
		t.Fatalf("unexpected policy friction classification: %#v", friction)
	}
}

func TestRunnerAutonomyRecorderEmitsModelExchangeByDefault(t *testing.T) {
	t.Setenv(runnerAutonomyDisableModelExchangeEnv, "")
	var runEvents []RunEvent
	runner := &Runner{
		AgentName: "main",
		OnEvent: func(event RunEvent) {
			runEvents = append(runEvents, event)
		},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Lane:              "plan-execute",
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "submit_plan",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.go"},
			AllowedTools:      []string{"read_file", "submit_plan"},
			RequiredTools:     []string{"submit_plan"},
		},
	}
	results := []workspace.ToolResult{{Tool: "read_file", Summary: "Read a.go lines 1-5"}}
	recorder := newRunnerAutonomyRecorder(runner, RunPolicy{MaxToolSteps: 2}, runner.RuntimeBrief, results)
	if recorder == nil {
		t.Fatal("newRunnerAutonomyRecorder returned nil")
	}
	recorder.emitContractStarted(runner.RuntimeBrief)
	phase := deriveTurnPhaseContract(TurnPhaseContractInput{
		ContractID:         recorder.contract.ContractID,
		ModelCallID:        recorder.contract.ContractID + ":model_call_1",
		Step:               1,
		Brief:              runner.RuntimeBrief,
		Results:            results,
		BaseAllowedTools:   []string{"submit_plan"},
		TextualToolParser:  true,
		VerificationStatus: "not_required",
	})
	protocol, ok := runner.compileModelProtocolProjection(runner.RuntimeBrief, results, phase, RunPolicy{MaxToolSteps: 2}, false)
	if !ok {
		t.Fatal("expected plan-execute model protocol projection")
	}
	recorder.emitTurnPhaseContract(phase, protocol)

	_ = decodeRunnerAutonomyEventsForTest(t, runEvents)
	exchange := mustRunnerAutonomyModelExchangeForTest(t, runEvents, "context_packet")
	packet := exchange.ModelExchange.ContextPacket
	if exchange.ContractID != recorder.contract.ContractID ||
		exchange.ModelExchange.ProtocolHash != protocol.ProtocolHash ||
		exchange.ModelExchange.PhaseID != phase.PhaseID ||
		exchange.ModelExchange.ModelCallID != phase.ModelCallID ||
		packet == nil ||
		packet.ContractID != recorder.contract.ContractID ||
		packet.ProtocolHash != protocol.ProtocolHash {
		t.Fatalf("context packet should cite active contract/protocol refs: %#v", exchange.ModelExchange)
	}
	if exchange.Effect != autonomy.EventEffectObserveOnly {
		t.Fatalf("context packet should be observe-only: %#v", exchange)
	}
	if len(packet.SchemaNames) == 0 || len(packet.RedactionMarkers) == 0 {
		t.Fatalf("context packet should carry compact schema/redaction refs: %#v", packet)
	}
}

func TestRunnerAutonomyRecorderModelExchangeCanBeDisabledWithoutDisablingAutonomyEvents(t *testing.T) {
	t.Setenv(runnerAutonomyDisableModelExchangeEnv, "true")
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
	recorder := newRunnerAutonomyRecorder(runner, RunPolicy{MaxToolSteps: 2}, brief, nil)
	if recorder == nil {
		t.Fatal("newRunnerAutonomyRecorder returned nil")
	}
	recorder.emitContractStarted(brief)
	phase := deriveTurnPhaseContract(TurnPhaseContractInput{
		ContractID:       recorder.contract.ContractID,
		ModelCallID:      recorder.contract.ContractID + ":model_call_1",
		Step:             1,
		Brief:            brief,
		BaseAllowedTools: []string{"submit_plan"},
	})
	recorder.emitTurnPhaseContract(phase, contractcompile.ModelProtocolProjection{})
	recorder.emitPolicyResult(workspace.ToolRequest{Tool: "read_file", Path: "a.go"}, runnerAutonomyPolicyEventInput{
		Decision:   autonomy.PolicyAllow,
		ReasonCode: "permission_policy",
		Result:     "no_approval_needed",
		Source:     "runner_permission_policy",
	})

	events := decodeRunnerAutonomyEventsForTest(t, runEvents)
	if len(events) == 0 {
		t.Fatal("expected existing autonomy events to remain enabled")
	}
	exchangeEvents := runnerAutonomyModelExchangeEventsForTest(t, runEvents)
	if len(exchangeEvents) != 1 ||
		exchangeEvents[0].ModelExchange == nil ||
		exchangeEvents[0].ModelExchange.RollbackControl == nil ||
		exchangeEvents[0].ModelExchange.RollbackControl.Decision != "model_exchange_disabled" {
		t.Fatalf("rollback switch should emit only rollback control audit event: %#v", exchangeEvents)
	}
}

func TestRunnerAutonomyRecorderModelExchangePermissionAndFailureRefs(t *testing.T) {
	t.Setenv(runnerAutonomyDisableModelExchangeEnv, "")
	root := t.TempDir()
	var runEvents []RunEvent
	runner := &Runner{
		AgentName: "main",
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
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
		AllowedTools:      []string{"read_file", "apply_patch"},
	}
	recorder := newRunnerAutonomyRecorder(runner, RunPolicy{MaxToolSteps: 2}, brief, nil)
	if recorder == nil {
		t.Fatal("newRunnerAutonomyRecorder returned nil")
	}
	recorder.emitContractStarted(brief)
	phase := deriveTurnPhaseContract(TurnPhaseContractInput{
		ContractID:       recorder.contract.ContractID,
		ModelCallID:      recorder.contract.ContractID + ":model_call_1",
		Step:             1,
		Brief:            brief,
		BaseAllowedTools: []string{"read_file", "apply_patch"},
	})
	recorder.emitTurnPhaseContract(phase, contractcompile.ModelProtocolProjection{ProtocolHash: "sha256:protocolref", PhaseID: phase.PhaseID})
	recorder.emitPolicyResult(workspace.ToolRequest{Tool: "read_file", Path: ".git/config"}, runnerAutonomyPolicyEventInput{
		Decision:    autonomy.PolicyDeny,
		InvariantID: string(autonomy.InvariantProtectedPath),
		ReasonCode:  "permission_policy",
		Result:      "denied",
		ErrorClass:  "permission_policy",
		Source:      "runner_permission_policy",
	})

	events := decodeRunnerAutonomyEventsForTest(t, runEvents)
	policyEvent := mustAutonomyPolicyEvent(t, events, autonomy.EventToolPolicyResult, "read_file", "denied")
	snapshot := mustRunnerAutonomyModelExchangeForTest(t, runEvents, "permission_snapshot")
	intent := mustRunnerAutonomyModelExchangeForTest(t, runEvents, "failure_delta")
	for _, exchange := range []autonomy.Event{snapshot, intent} {
		if exchange.ContractID != recorder.contract.ContractID ||
			exchange.ModelExchange == nil ||
			exchange.ModelExchange.InvariantID != string(autonomy.InvariantProtectedPath) ||
			exchange.ModelExchange.SourceEventID != policyEvent.EventID {
			t.Fatalf("model-exchange event should cite active policy refs: %#v policy=%#v", exchange.ModelExchange, policyEvent)
		}
	}
	if intent.ModelExchange.FailureDelta == nil || intent.ModelExchange.FailureDelta.GeneratedBy != "harness" {
		t.Fatalf("failure delta must be harness-generated typed payload: %#v", intent.ModelExchange)
	}
}

func TestRunnerAutonomyRecorderModelExchangeClassifiesModelResponseAsNonAuthoritativeAndPrivacySafe(t *testing.T) {
	t.Setenv(runnerAutonomyDisableModelExchangeEnv, "")
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
		Objective:         "Do not leak SECRET_CUSTOMER_123 or this raw prompt text.",
	}
	recorder := newRunnerAutonomyRecorder(runner, RunPolicy{MaxToolSteps: 2}, brief, nil)
	if recorder == nil {
		t.Fatal("newRunnerAutonomyRecorder returned nil")
	}
	recorder.emitContractStarted(brief)
	recorder.emitTurnPhaseContract(deriveTurnPhaseContract(TurnPhaseContractInput{
		ContractID:       recorder.contract.ContractID,
		ModelCallID:      recorder.contract.ContractID + ":model_call_1",
		Step:             1,
		Brief:            brief,
		BaseAllowedTools: []string{"apply_patch"},
	}), contractcompile.ModelProtocolProjection{ProtocolHash: "sha256:protocolref", PhaseID: "phase-ref"})
	recorder.emitPolicyResult(workspace.ToolRequest{
		Tool:    "tool_parse",
		Content: "SECRET_CUSTOMER_123 raw malformed transcript body",
	}, runnerAutonomyPolicyEventInput{
		Decision:      autonomy.PolicyReject,
		InvariantID:   "parser.tool_request",
		ReasonCode:    "malformed_tool_request",
		ParserFailure: autonomy.ParserFailureMalformedJSON,
		Result:        "rejected",
		ErrorClass:    "tool_parse",
		Source:        "runner_tool_parse",
	})

	observation := mustRunnerAutonomyModelExchangeForTest(t, runEvents, "validation_signal")
	if observation.ModelExchange.ValidationSignal == nil ||
		observation.ModelExchange.ValidationSignal.CapturedBy != "harness" ||
		observation.ModelExchange.ValidationSignal.Status != "model_response_observed_non_authoritative" {
		t.Fatalf("model response observation must be harness validation telemetry: %#v", observation.ModelExchange)
	}
	if observation.ContractID != recorder.contract.ContractID || observation.ModelExchange.ProtocolHash == "" || observation.ModelExchange.InvariantID != "parser.tool_request" {
		t.Fatalf("observation should cite active contract/protocol refs: %#v", observation.ModelExchange)
	}
	for _, event := range runnerAutonomyModelExchangeEventsForTest(t, runEvents) {
		for _, forbidden := range []string{"SECRET_CUSTOMER_123", "raw malformed transcript body", "raw prompt text"} {
			data, err := json.Marshal(event)
			if err != nil {
				t.Fatalf("marshal event: %v", err)
			}
			if strings.Contains(string(data), forbidden) {
				t.Fatalf("model-exchange payload leaked sensitive/raw text %q: %s", forbidden, string(data))
			}
		}
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

func TestRunnerAutonomyRecorderGateDecisionPayloadIncludesPackModeFactsAndAlternatives(t *testing.T) {
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
			OpenObligations: []string{"write", "verify", "final_answer"},
		},
	}
	policy := RunPolicy{MaxToolSteps: 5, MaxModelCalls: 2}
	recorder := newRunnerAutonomyRecorder(runner, policy, brief, nil)
	if recorder == nil {
		t.Fatal("newRunnerAutonomyRecorder returned nil")
	}

	recorder.emitContractStarted(brief)
	phase := deriveTurnPhaseContract(TurnPhaseContractInput{
		ContractID:         recorder.contract.ContractID,
		ModelCallID:        recorder.contract.ContractID + ":model_call_1",
		Step:               1,
		Brief:              brief,
		BaseAllowedTools:   []string{"read_file", "apply_patch", "shell"},
		VerificationStatus: "pending",
	})
	recorder.emitTurnPhaseContract(phase, contractcompile.ModelProtocolProjection{})
	recorder.emitWriteGate(workspace.ToolRequest{Tool: "apply_patch", Content: "*** Begin Patch\n*** Update File: README.md\n@@\n-x\n+y\n*** End Patch\n"}, autonomy.PolicyReject, "required_edit_scope_rejected", nil)
	recorder.emitVerificationGate(workspace.ToolRequest{Tool: "shell"}, autonomy.PolicyReject, "verification_required_after_write", "pending")
	recorder.emitCompletionGate(autonomy.PolicyReject, "required_obligations_open", nil)
	recorder.emitLoopGate(workspace.ToolRequest{Tool: "read_file", Path: "internal/agent/runner.go"}, autonomy.PolicyStop, "repeated_tool_call", "read_file:internal/agent/runner.go", 3)
	recorder.emitBudgetGate(policy, runnerMetrics{ToolCalls: 5, ModelCalls: 2}, "max_tool_steps", true)
	recorder.emitGateDecision(runnerAutonomyGateDecision{
		Gate:        autonomy.GateReadOnly,
		Decision:    autonomy.PolicyObserve,
		InvariantID: "read_only.readiness",
		ReasonCode:  "evidence_status_observed",
		Source:      "runner_read_only_gate",
	})

	_ = decodeRunnerAutonomyEventsForTest(t, runEvents)
	rawGates := decodeRunnerAutonomyGatePayloadsForTest(t, runEvents)
	expectedPacks := map[autonomy.GateKind]string{
		autonomy.GateWrite:        "write/required_paths",
		autonomy.GateVerification: "verification/post_write",
		autonomy.GateCompletion:   "completion/readiness",
		autonomy.GateLoop:         "loop/repeated_call",
		autonomy.GateBudget:       "budget/run_policy",
		autonomy.GateReadOnly:     "completion/readiness",
	}
	for gateKind, packID := range expectedPacks {
		gate := mustRunnerAutonomyRawGateForTest(t, rawGates, gateKind)
		if got := rawGateString(gate, "pack_id"); got != packID {
			t.Fatalf("%s pack_id = %q, want %q in %#v", gateKind, got, packID, gate)
		}
		mode := "enforce"
		if gateKind == autonomy.GateReadOnly {
			mode = "observe_only"
		}
		if got := rawGateString(gate, "enforcement_mode"); got != mode {
			t.Fatalf("%s enforcement_mode = %q, want %q in %#v", gateKind, got, mode, gate)
		}
		facts := rawGateObservedFacts(gate)
		if len(facts) == 0 ||
			!rawObservedFactsContainPrefix(facts, "reason_code:reason_code=") ||
			!rawObservedFactsContainPrefix(facts, "status:decision=") {
			t.Fatalf("%s missing bounded observed facts: %#v", gateKind, gate["observed_facts"])
		}
	}
	for _, gateKind := range []autonomy.GateKind{
		autonomy.GateWrite,
		autonomy.GateVerification,
		autonomy.GateCompletion,
		autonomy.GateLoop,
		autonomy.GateBudget,
	} {
		gate := mustRunnerAutonomyRawGateForTest(t, rawGates, gateKind)
		if alternatives := rawGateStringSlice(gate, "safe_alternatives"); len(alternatives) == 0 {
			t.Fatalf("%s should include safe alternatives for blocked decision: %#v", gateKind, gate)
		}
	}
	writeGate := mustRunnerAutonomyRawGateForTest(t, rawGates, autonomy.GateWrite)
	writeFacts := rawGateObservedFacts(writeGate)
	if !rawObservedFactsContain(writeFacts, "tool_name:tool=apply_patch") {
		t.Fatalf("write observed facts should include apply_patch tool in %#v", writeFacts)
	}
	if strings.Contains(mustRunnerAutonomyRawGateJSONForTest(t, runEvents, autonomy.GateWrite), "*** Begin Patch") {
		t.Fatalf("gate payload leaked raw patch content")
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

func TestRunnerAutonomyRecorderRejectedCompletionEmitsGuardFriction(t *testing.T) {
	var runEvents []RunEvent
	runner := &Runner{
		AgentName: "main",
		OnEvent: func(event RunEvent) {
			runEvents = append(runEvents, event)
		},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Lane:              "plan-execute",
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "submit_plan",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.go"},
			AllowedTools:      []string{"read_file", "submit_plan"},
			RequiredTools:     []string{"submit_plan"},
		},
	}
	results := []workspace.ToolResult{{Tool: "read_file", Summary: "Read f1 a.go lines 1-10 (total lines: 10)"}}
	recorder := newRunnerAutonomyRecorder(runner, RunPolicy{MaxToolSteps: 2}, runner.RuntimeBrief, results)
	if recorder == nil {
		t.Fatal("newRunnerAutonomyRecorder returned nil")
	}
	recorder.emitContractStarted(runner.RuntimeBrief)
	phase := deriveTurnPhaseContract(TurnPhaseContractInput{
		ContractID:         recorder.contract.ContractID,
		ModelCallID:        recorder.contract.ContractID + ":model_call_1",
		Step:               1,
		Brief:              runner.RuntimeBrief,
		Results:            results,
		BaseAllowedTools:   []string{"submit_plan"},
		TextualToolParser:  true,
		VerificationStatus: "not_required",
	})
	protocol, ok := runner.compileModelProtocolProjection(runner.RuntimeBrief, results, phase, RunPolicy{MaxToolSteps: 2}, false)
	if !ok {
		t.Fatal("expected plan-execute model protocol projection")
	}
	recorder.emitTurnPhaseContract(phase, protocol)
	recorder.emitCompletionGate(autonomy.PolicyReject, "required_obligations_open", results)

	events := decodeRunnerAutonomyEventsForTest(t, runEvents)
	var friction *autonomy.ContractGuardFrictionEvent
	var completionGate *autonomy.ContractGateEvent
	var completionDecision *autonomy.ContractCompletionEvent
	for _, event := range events {
		if event.Type == autonomy.EventGuardFriction {
			friction = event.GuardFriction
		}
		if event.Type == autonomy.EventGateDecision && event.Gate != nil && event.Gate.Gate == autonomy.GateCompletion {
			completionGate = event.Gate
		}
		if event.Type == autonomy.EventCompletionGateDecision {
			completionDecision = event.Completion
		}
	}
	if friction == nil {
		t.Fatalf("expected guard friction event, got event roles=%#v", eventRolesFor(runEvents))
	}
	if completionGate == nil || completionGate.ProtocolHash != "" {
		t.Fatalf("completion gate must not claim a model-visible protocol hash without prompt presentation: %#v", completionGate)
	}
	if completionDecision == nil || completionDecision.ProtocolHash != "" {
		t.Fatalf("completion decision must not claim a model-visible protocol hash without prompt presentation: %#v", completionDecision)
	}
	if friction.GuardSurface != autonomy.GuardFrictionSurfaceCompletionGate ||
		friction.Decision != autonomy.GuardFrictionDecisionReject ||
		friction.FirstInvalidAction != actionFinalAnswer {
		t.Fatalf("unexpected completion friction classification: %#v", friction)
	}
	if friction.ProtocolHash != "" || friction.ModelContractVisible {
		t.Fatalf("friction must not claim model-visible protocol without prompt presentation: %#v", friction)
	}
	if !slices.Contains(friction.SafeAlternatives, "Decision: source-satisfied") ||
		!slices.Contains(friction.SafeAlternatives, "<<QUESTION>>:") {
		t.Fatalf("friction should carry safe terminal alternatives: %#v", friction.SafeAlternatives)
	}
	if strings.Contains(strings.Join(friction.EvidenceRefs, " "), "raw_prompt") {
		t.Fatalf("friction evidence refs leaked raw prompt marker: %#v", friction.EvidenceRefs)
	}
	report := autonomy.AuditEvents(events, autonomy.EventAuditOptions{})
	if report.GuardFrictionCoverage.Total != 1 ||
		report.GuardFrictionCoverage.GuardSurfaceCounts[string(autonomy.GuardFrictionSurfaceCompletionGate)] != 1 {
		t.Fatalf("expected audit to group completion-gate friction, got %#v", report.GuardFrictionCoverage)
	}
}

func TestRunnerAutonomyRecorderRejectedWriteGateEmitsGuardFriction(t *testing.T) {
	var runEvents []RunEvent
	runner := &Runner{
		AgentName: "main",
		OnEvent: func(event RunEvent) {
			runEvents = append(runEvents, event)
		},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Lane:              "implement",
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"internal/agent/runner.go"},
			AllowedTools:      []string{"apply_patch", "shell"},
		},
	}
	recorder := newRunnerAutonomyRecorder(runner, RunPolicy{MaxToolSteps: 5}, runner.RuntimeBrief, nil)
	if recorder == nil {
		t.Fatal("newRunnerAutonomyRecorder returned nil")
	}
	recorder.emitContractStarted(runner.RuntimeBrief)
	phase := deriveTurnPhaseContract(TurnPhaseContractInput{
		ContractID:       recorder.contract.ContractID,
		ModelCallID:      recorder.contract.ContractID + ":model_call_1",
		Step:             2,
		Brief:            runner.RuntimeBrief,
		BaseAllowedTools: []string{"apply_patch", "shell"},
	})
	recorder.emitTurnPhaseContract(phase, contractcompile.ModelProtocolProjection{})
	recorder.emitWriteGate(workspace.ToolRequest{Tool: "apply_patch", Content: "*** Begin Patch\n*** Update File: README.md\n@@\n-x\n+y\n*** End Patch\n"}, autonomy.PolicyReject, "required_edit_scope_rejected", nil)

	events := decodeRunnerAutonomyEventsForTest(t, runEvents)
	var gateEventID string
	var friction *autonomy.ContractGuardFrictionEvent
	for _, event := range events {
		if event.Type == autonomy.EventGateDecision && event.Gate != nil && event.Gate.Gate == autonomy.GateWrite {
			gateEventID = event.EventID
		}
		if event.Type == autonomy.EventGuardFriction {
			friction = event.GuardFriction
		}
	}
	if friction == nil {
		t.Fatalf("expected write gate friction event, got roles=%#v", eventRolesFor(runEvents))
	}
	if friction.SourceEventID != gateEventID || gateEventID == "" {
		t.Fatalf("friction should reference source gate event %q: %#v", gateEventID, friction)
	}
	if friction.GuardSurface != autonomy.GuardFrictionSurfaceWriteGate ||
		friction.Subject != autonomy.GuardFrictionSubjectToolCall ||
		friction.FirstInvalidAction != "apply_patch" {
		t.Fatalf("unexpected write friction classification: %#v", friction)
	}
	if friction.ProtocolHash != "" || friction.ModelContractVisible {
		t.Fatalf("write friction should not claim model-visible protocol without a projection: %#v", friction)
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

func runnerAutonomyModelExchangeEventsForTest(t *testing.T, runEvents []RunEvent) []autonomy.Event {
	t.Helper()
	var events []autonomy.Event
	for _, runEvent := range runEvents {
		if runEvent.Role != runnerAutonomyEventRole {
			continue
		}
		var decoded autonomy.Event
		if err := json.Unmarshal([]byte(runEvent.Content), &decoded); err != nil {
			t.Fatalf("decode model-exchange event: %v\n%s", err, runEvent.Content)
		}
		if decoded.Type != autonomy.EventModelExchange {
			continue
		}
		if decoded.Effect != autonomy.EventEffectObserveOnly ||
			strings.TrimSpace(decoded.ContractID) == "" ||
			decoded.ModelExchange == nil ||
			strings.TrimSpace(decoded.ModelExchange.Kind) == "" {
			t.Fatalf("invalid model-exchange event: %#v", decoded)
		}
		events = append(events, decoded)
	}
	return events
}

func mustRunnerAutonomyModelExchangeForTest(t *testing.T, runEvents []RunEvent, kind string) autonomy.Event {
	t.Helper()
	for _, event := range runnerAutonomyModelExchangeEventsForTest(t, runEvents) {
		if event.ModelExchange != nil && event.ModelExchange.Kind == kind {
			return event
		}
	}
	t.Fatalf("missing model-exchange event kind=%q roles=%#v", kind, eventRolesFor(runEvents))
	return autonomy.Event{}
}

func decodeRunnerAutonomyGatePayloadsForTest(t *testing.T, runEvents []RunEvent) []map[string]any {
	t.Helper()
	var gates []map[string]any
	for _, runEvent := range runEvents {
		if runEvent.Role != runnerAutonomyEventRole {
			continue
		}
		var event struct {
			Type autonomy.EventType `json:"type"`
			Gate map[string]any     `json:"gate"`
		}
		if err := json.Unmarshal([]byte(runEvent.Content), &event); err != nil {
			t.Fatalf("decode raw autonomy event: %v\n%s", err, runEvent.Content)
		}
		if event.Type == autonomy.EventGateDecision && event.Gate != nil {
			gates = append(gates, event.Gate)
		}
	}
	if len(gates) == 0 {
		t.Fatal("expected raw gate events")
	}
	return gates
}

func mustRunnerAutonomyRawGateForTest(t *testing.T, gates []map[string]any, gateKind autonomy.GateKind) map[string]any {
	t.Helper()
	for _, gate := range gates {
		if rawGateString(gate, "gate") == string(gateKind) {
			return gate
		}
	}
	t.Fatalf("missing raw gate %q in %#v", gateKind, gates)
	return nil
}

func mustRunnerAutonomyRawGateJSONForTest(t *testing.T, runEvents []RunEvent, gateKind autonomy.GateKind) string {
	t.Helper()
	for _, runEvent := range runEvents {
		if runEvent.Role != runnerAutonomyEventRole {
			continue
		}
		var event struct {
			Type autonomy.EventType `json:"type"`
			Gate map[string]any     `json:"gate"`
		}
		if err := json.Unmarshal([]byte(runEvent.Content), &event); err != nil {
			t.Fatalf("decode raw autonomy event: %v\n%s", err, runEvent.Content)
		}
		if event.Type == autonomy.EventGateDecision && rawGateString(event.Gate, "gate") == string(gateKind) {
			return runEvent.Content
		}
	}
	t.Fatalf("missing raw gate JSON %q", gateKind)
	return ""
}

func rawGateString(values map[string]any, key string) string {
	if values == nil {
		return ""
	}
	value, _ := values[key].(string)
	return strings.TrimSpace(value)
}

func rawGateStringSlice(values map[string]any, key string) []string {
	if values == nil {
		return nil
	}
	raw, _ := values[key].([]any)
	out := make([]string, 0, len(raw))
	for _, value := range raw {
		if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
			out = append(out, strings.TrimSpace(text))
		}
	}
	return out
}

func rawGateObservedFacts(values map[string]any) []string {
	if values == nil {
		return nil
	}
	raw, _ := values["observed_facts"].([]any)
	out := make([]string, 0, len(raw))
	for _, value := range raw {
		if fact, ok := value.(string); ok && strings.TrimSpace(fact) != "" {
			out = append(out, strings.TrimSpace(fact))
		}
	}
	return out
}

func rawObservedFactsContain(facts []string, want string) bool {
	for _, fact := range facts {
		if fact == want {
			return true
		}
	}
	return false
}

func rawObservedFactsContainPrefix(facts []string, prefix string) bool {
	for _, fact := range facts {
		if strings.HasPrefix(fact, prefix) {
			return true
		}
	}
	return false
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
