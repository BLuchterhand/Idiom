package app

import (
	"encoding/json"
	"fmt"
	"strings"

	lanecontract "idiom/internal/contracts/lane"
	"idiom/internal/contracts/writerroute"
	"idiom/internal/runtime/backends"
	"idiom/internal/runtime/workerruntime"
	"idiom/internal/runtime/writerrouting"
)

func (m *promptExecutionSession) writerRouteDecisionForPrompt(prompt string, plan promptExecutionPlan) writerroute.Decision {
	mode := writerroute.ModeReadOnly
	lane := strings.TrimSpace(plan.route.InitialLane)

	if lane == "" {
		if m.debugLogger != nil {
			_ = m.debugLogger.LogRunEvent("writer_route_empty_initial_lane", fmt.Sprintf("route classifier produced no InitialLane for task family %q; falling back to \"main\". A normalizeTurnRoutePlan call may have been skipped.", plan.route.Family))
		}

		lane = "main"
	}

	taskFamily := strings.TrimSpace(plan.route.Family)
	reasonCodes := []writerroute.ReasonCode{writerroute.ReasonReadOnlyQuestion}
	verificationOwner := writerroute.VerificationOwner("")
	profile, hasRuntimeProfile := workerruntime.RuntimeProfileForTaskFamily(taskFamily)

	if hasRuntimeProfile && profile.PlanSubmission {
		mode = writerroute.ModePlanSubmission
		lane = string(lanecontract.LanePlanExecute)
		reasonCodes = []writerroute.ReasonCode{writerroute.ReasonPlanExecutePlanner}
		if workerruntime.TaskFamilyIsPlanThenExecuteReview(profile.TaskFamily) {
			reasonCodes = []writerroute.ReasonCode{writerroute.ReasonPlanExecuteReviewRepair}
		}
		verificationOwner = writerroute.VerificationOwner(profile.VerificationOwner)
	} else if hasRuntimeProfile && profile.MutationCapable {
		mode = writerroute.ModeAgenticGuarded
		lane = "implement"
		reasonCodes = []writerroute.ReasonCode{writerroute.ReasonLocalModelDefault}

		if strings.TrimSpace(plan.route.Reason) == "typed_implement_command" {
			reasonCodes = []writerroute.ReasonCode{writerroute.ReasonTypedImplementCommand}
		}

		if m.plannerOwnedVerification {
			verificationOwner = writerroute.VerificationOwnerPlannerOwned
		} else {
			verificationOwner = writerroute.VerificationOwner(profile.VerificationOwner)
		}
	}

	aliases := writerrouting.RoleAliasesForConfig(m.cfg, "")
	plannerModel := m.writerRouteModelForRole(aliases, backends.RolePlanner)
	executorModel := m.writerRouteModelForRole(aliases, backends.RoleExecutor)
	classifierModel := m.writerRouteModelForRole(aliases, backends.RoleClassifier)

	return writerroute.NewDecisionFromParams(writerroute.DecisionParams{
		Mode:              mode,
		Lane:              lane,
		PlannerModel:      plannerModel,
		ExecutorModel:     executorModel,
		ReviewerModel:     executorModel,
		ClassifierModel:   classifierModel,
		EffectiveModel:    writerroute.EffectiveModelForMode(mode, plannerModel, executorModel, classifierModel),
		RoleAliases:       aliases,
		VerificationOwner: verificationOwner,
		RequiredEditPaths: plan.runtimeBrief.RequiredEditPaths,
		ReasonCodes:       reasonCodes,
		TaskFamily:        taskFamily,
	})
}

func (m *promptExecutionSession) emitWriterRouteDecision(decision writerroute.Decision) {
	fields := decision.EventFields()

	fields["type"] = string(writerroute.EventWriterRouteDecision)
	m.eventStream.emit(fields)

	if body, err := json.Marshal(fields); err == nil {
		m.logMessage(string(writerroute.EventWriterRouteDecision), string(body))
	}
}

func (m *promptExecutionSession) writerRouteModelForRole(aliases map[string]string, role string) string {
	return writerrouting.ModelForRole(writerrouting.ModelSelection{
		Config:                    m.cfg,
		Aliases:                   aliases,
		PlannerModel:              m.plannerSpec.Model,
		ExecutorModel:             m.executorSpec.Model,
		UseBaseModelForClassifier: true,
	}, role)
}
