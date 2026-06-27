package planexecute

import (
	"io"
	"strings"
	"time"

	"idiom/internal/contracts/autonomy"
	plancontract "idiom/internal/contracts/plan"
	"idiom/internal/platform/config"
	"idiom/internal/platform/textutil"
)

type planExecuteReviewRepairLifecycle struct {
	Invocation autonomy.ParentChildInvocationRecord
}

func beginPlanExecuteReviewRepairLifecycle(cfg config.Config, opts Options, reviewSessionID string, changedPaths []string, allowedRepairPaths []string, stdout io.Writer) planExecuteReviewRepairLifecycle {
	parentContractID := strings.TrimSpace(opts.ScopeContract.ContractID)
	if parentContractID == "" {
		parentContractID = "plan_execute_" + planExecuteShortHash(cfg.Workspace.Root, reviewSessionID)
	}

	childContractID := "child_contract_" + planExecuteShortHash(parentContractID, reviewSessionID, string(autonomy.ChildLifecycleRoleReviewRepair))
	invocationID := "child_invocation_" + planExecuteShortHash(childContractID, strings.Join(changedPaths, "\x00"), strings.Join(allowedRepairPaths, "\x00"))
	modelAlias := firstNonEmptyString(strings.TrimSpace(opts.Model), strings.TrimSpace(cfg.LlamaServer.Model), "default")
	record := autonomy.ParentChildInvocationRecord{
		InvocationID:     invocationID,
		ParentContractID: parentContractID,
		ChildContractID:  childContractID,
		Role:             string(autonomy.ChildLifecycleRoleReviewRepair),
		Lane:             string(autonomy.ChildLifecycleLanePlanExecute),
		Phase:            string(autonomy.ChildLifecyclePhaseReviewRepair),
		WorkspaceMode:    autonomy.ChildWorkspaceNoWrite,
		ContractSummary: autonomy.ChildContractSummary{
			PathRoleDigest:   "sha256:" + planExecuteFullHash(strings.Join(allowedRepairPaths, "\x00"), "path_roles"),
			ToolPolicyDigest: "sha256:" + planExecuteFullHash(string(autonomy.ChildLifecycleRoleReviewRepair)+"_no_write", "tool_policy"),
			ObligationDigest: "sha256:" + planExecuteFullHash(strings.Join(changedPaths, "\x00"), "review_obligations"),
		},
		RuntimeEnvelope: autonomy.ChildRuntimeEnvelope{
			BackendKind: string(autonomy.ChildBackendKindPromptRunner),
			ModelAlias:  modelAlias,
			ModelRole:   string(autonomy.ChildModelRoleReviewRepair),
			RoleResolution: autonomy.ChildRoleResolution{
				RequestedRole: string(autonomy.ChildLifecycleRoleReviewRepair),
				ResolvedRole:  string(autonomy.ChildLifecycleRoleReviewRepair),
				Alias:         modelAlias,
				Source:        autonomy.RoleResolutionSourceRoleTag,
				OK:            true,
			},
			CapacityAdmission: autonomy.ChildCapacityAdmission{
				State:       autonomy.CapacityAdmissionAcquired,
				ModelAlias:  modelAlias,
				Enforcement: autonomy.CapacityAdmissionOpportunistic,
			},
			ToolTransportMode:   autonomy.ChildTransportTextual,
			ParserTransportMode: autonomy.ChildTransportTextual,
			ParserContract:      string(autonomy.ChildParserContractReviewRepairTerminal),
			MaxTokens:           opts.MaxTokens,
		},
		CreatedAt: time.Now().UTC(),
	}
	emitPlanExecuteAutonomyRecord(stdout, opts.JSONEvents, plancontract.PlanExecuteEventParentChildInvocation, &record)

	return planExecuteReviewRepairLifecycle{Invocation: record}
}

func emitPlanExecuteReviewRepairLifecycleCloseout(stdout io.Writer, jsonEvents bool, lifecycle planExecuteReviewRepairLifecycle, report ReviewRepairPhaseReport, err error) {
	if strings.TrimSpace(lifecycle.Invocation.InvocationID) == "" {
		return
	}

	terminal := planExecuteReviewRepairTerminalRecord(lifecycle, report, err)
	emitPlanExecuteAutonomyRecord(stdout, jsonEvents, plancontract.PlanExecuteEventChildTerminal, &terminal)

	acceptance := planExecuteReviewRepairAcceptanceRecord(lifecycle, terminal, report, err)
	emitPlanExecuteAutonomyRecord(stdout, jsonEvents, plancontract.PlanExecuteEventChildAcceptance, &acceptance)
}

func planExecuteReviewRepairTerminalRecord(lifecycle planExecuteReviewRepairLifecycle, report ReviewRepairPhaseReport, err error) autonomy.ChildTerminalRecord {
	status := autonomy.ChildTerminalCompleted
	objective := autonomy.ChildObjectiveAccepted
	resultKind := autonomy.ChildResultFindings
	if strings.TrimSpace(report.ReviewPlan) != "" {
		resultKind = autonomy.ChildResultPlanArtifact
	}
	if err != nil {
		status = autonomy.ChildTerminalFailed
		objective = autonomy.ChildObjectiveWeak
		resultKind = autonomy.ChildResultTypedBlocker
	} else if report.Status == plancontract.PlanExecuteReviewStatusBlocked || report.Status == plancontract.PlanExecuteReviewStatusDegraded {
		status = autonomy.ChildTerminalBlocked
		objective = autonomy.ChildObjectiveWeak
		resultKind = autonomy.ChildResultTypedBlocker
	}

	evidenceRefs := []string{"review_status:" + planExecuteShortHash(string(report.Status))}
	if strings.TrimSpace(report.ReviewPlan) != "" {
		evidenceRefs = append(evidenceRefs, "review_plan:"+planExecuteShortHash(report.ReviewPlan))
	}

	return autonomy.ChildTerminalRecord{
		TerminalID:         "child_terminal_" + planExecuteShortHash(lifecycle.Invocation.InvocationID, string(report.Status), firstNonEmptyString(report.ReviewPlan, "no_plan")),
		InvocationID:       lifecycle.Invocation.InvocationID,
		ChildContractID:    lifecycle.Invocation.ChildContractID,
		ResultKind:         resultKind,
		Status:             status,
		EvidenceRefs:       textutil.Sorted(evidenceRefs),
		ChangedPaths:       nil,
		ObjectiveAlignment: objective,
	}
}

func planExecuteReviewRepairAcceptanceRecord(lifecycle planExecuteReviewRepairLifecycle, terminal autonomy.ChildTerminalRecord, report ReviewRepairPhaseReport, err error) autonomy.ChildAcceptanceRecord {
	decision := autonomy.ChildAcceptanceAccepted
	reasonCode := autonomy.ChildLifecycleReasonReviewRepairAccepted
	if err != nil {
		decision = autonomy.ChildAcceptanceRepairRequired
		reasonCode = string(plancontract.PlanExecuteEventReviewRepairFailed)
	} else if terminal.Status == autonomy.ChildTerminalBlocked {
		decision = autonomy.ChildAcceptanceBlocked
		reasonCode = string(plancontract.PlanExecuteEventReviewRepairBlocked)
	}

	acceptedEvidenceRefs := []string(nil)
	if decision == autonomy.ChildAcceptanceAccepted {
		acceptedEvidenceRefs = append([]string(nil), terminal.EvidenceRefs...)
	}

	return autonomy.ChildAcceptanceRecord{
		AcceptanceID:         "child_acceptance_" + planExecuteShortHash(terminal.TerminalID, string(decision)),
		ParentContractID:     lifecycle.Invocation.ParentContractID,
		ChildContractID:      lifecycle.Invocation.ChildContractID,
		InvocationID:         lifecycle.Invocation.InvocationID,
		TerminalID:           terminal.TerminalID,
		ResultKind:           terminal.ResultKind,
		Decision:             decision,
		AcceptedEvidenceRefs: acceptedEvidenceRefs,
		RuntimeEnvelopeRef:   "runtime_envelope:" + lifecycle.Invocation.InvocationID,
		AlignmentBasis: autonomy.ChildAlignmentBasis{
			DelegatedObjectiveRef:  "objective:" + planExecuteShortHash(lifecycle.Invocation.InvocationID),
			EffectiveScopeRefs:     []string{autonomy.ChildScopeRefLanePlanExecute, autonomy.ChildScopeRefPhaseReviewRepair},
			AcceptedSourceBodyRefs: acceptedEvidenceRefs,
			Evaluator:              autonomy.AlignmentEvaluatorHostContract,
		},
		ReasonCode: reasonCode,
	}
}
