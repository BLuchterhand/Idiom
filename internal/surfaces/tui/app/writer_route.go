package app

import (
	"encoding/json"
	"fmt"
	"strings"

	"idiom/internal/contracts/writerroute"
	"idiom/internal/runtime/backends"
	"idiom/internal/runtime/workerruntime"
)

func (m *Model) writerRouteDecisionForPrompt(prompt string, plan promptExecutionPlan) writerroute.Decision {
	mode := writerroute.ModeReadOnly
	lane := strings.TrimSpace(plan.route.InitialLane)
	if lane == "" {
		if m.debugLogger != nil {
			_ = m.debugLogger.LogRunEvent("writer_route_empty_initial_lane", fmt.Sprintf("route classifier produced no InitialLane for task family %q; falling back to \"main\". A normalizeTurnRoutePlan call may have been skipped.", plan.route.Family))
		}
		lane = "main"
	}
	taskFamily := strings.TrimSpace(plan.route.Family)
	reasonCodes := []string{"route.read_only_question"}
	verificationOwner := ""
	if routeRequiresMutationConfirmation(plan) {
		lane = "main"
		reasonCodes = []string{"route.mutation_confirmation_required"}
	} else if workerruntime.TaskFamilyIsPlanThenExecutePlanner(taskFamily) {
		mode = writerroute.ModePlanSubmission
		lane = "plan-execute"
		reasonCodes = []string{"route.plan_execute_planner"}
		verificationOwner = "planner_owned"
	} else if workerruntime.TaskFamilyIsPlanThenExecuteReview(taskFamily) {
		mode = writerroute.ModePlanSubmission
		lane = "plan-execute"
		reasonCodes = []string{"route.plan_execute_review_repair"}
		verificationOwner = "planner_owned"
	} else if isImplementationLikeFamily(plan.route.Family) {
		mode = writerroute.ModeAgenticGuarded
		lane = "implement"
		reasonCodes = []string{"route.local_model_default"}
		if strings.TrimSpace(plan.route.Reason) == "typed_implement_command" {
			reasonCodes = []string{"route.typed_implement_command"}
		}
		if m.plannerOwnedVerification {
			verificationOwner = "planner_owned"
		} else {
			verificationOwner = "executor"
		}
	}
	aliases := appWriterRouteRoleAliases(m.projectRoot())
	return writerroute.NewDecisionFromParams(writerroute.DecisionParams{
		Mode:              mode,
		Lane:              lane,
		PlannerModel:      m.writerRouteModelForRole(aliases, backends.RolePlanner),
		ExecutorModel:     m.writerRouteModelForRole(aliases, backends.RoleExecutor),
		ReviewerModel:     m.writerRouteModelForRole(aliases, backends.RoleExecutor),
		ClassifierModel:   m.writerRouteModelForRole(aliases, backends.RoleClassifier),
		RoleAliases:       aliases,
		VerificationOwner: verificationOwner,
		RequiredEditPaths: plan.runtimeBrief.RequiredEditPaths,
		ReasonCodes:       reasonCodes,
		TaskFamily:        taskFamily,
	})
}

func (m *Model) emitWriterRouteDecision(decision writerroute.Decision) {
	fields := decision.EventFields()
	fields["type"] = "writer_route_decision"
	m.emitJSONEvent(fields)
	if body, err := json.Marshal(fields); err == nil {
		m.logMessage("writer_route_decision", string(body))
	}
}

func appWriterRouteRoleAliases(projectRoot string) map[string]string {
	file, ok, err := backends.LoadFromProjectRoot(projectRoot)
	if err != nil || !ok {
		return nil
	}
	profile, found := file.Profile("")
	if !found {
		return nil
	}
	aliases := make(map[string]string, 3)
	for _, role := range []string{backends.RolePlanner, backends.RoleExecutor, backends.RoleClassifier} {
		if alias, ok := profile.AliasForRole(role); ok && strings.TrimSpace(alias) != "" {
			aliases[role] = strings.TrimSpace(alias)
		}
	}
	if len(aliases) == 0 {
		return nil
	}
	return aliases
}

func (m *Model) writerRouteModelForRole(aliases map[string]string, role string) string {
	if alias := strings.TrimSpace(aliases[role]); alias != "" {
		return alias
	}
	switch role {
	case backends.RoleExecutor:
		if model := strings.TrimSpace(m.executorSpec.Model); model != "" {
			return model
		}
		if m.cfg.Implementer != nil && strings.TrimSpace(m.cfg.Implementer.Model) != "" {
			return strings.TrimSpace(m.cfg.Implementer.Model)
		}
	case backends.RoleClassifier:
		if model := strings.TrimSpace(m.cfg.RouteClassifier.Model); model != "" {
			return model
		}
	default:
		if model := strings.TrimSpace(m.plannerSpec.Model); model != "" {
			return model
		}
	}
	return strings.TrimSpace(m.cfg.LlamaServer.Model)
}
