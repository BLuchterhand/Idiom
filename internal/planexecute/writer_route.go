package planexecute

import (
	"strings"

	"idiom/internal/backends"
	"idiom/internal/config"
	"idiom/internal/frontier/workerruntime"
	"idiom/internal/writerroute"
)

func planExecuteWriterRouteDecision(cfg config.Config, requiredEditPaths []string) writerroute.Decision {
	aliases := planExecuteRoleAliases(cfg.Workspace.Root, "")
	return writerroute.NewDecisionFromParams(writerroute.DecisionParams{
		Mode:              writerroute.ModePlanSubmission,
		Lane:              "plan-execute",
		PlannerModel:      planExecuteModelForRole(cfg, aliases, backends.RolePlanner),
		ExecutorModel:     planExecuteModelForRole(cfg, aliases, backends.RoleExecutor),
		ReviewerModel:     planExecuteModelForRole(cfg, aliases, backends.RoleExecutor),
		ClassifierModel:   planExecuteModelForRole(cfg, aliases, backends.RoleClassifier),
		RoleAliases:       aliases,
		VerificationOwner: "planner_owned",
		RequiredEditPaths: requiredEditPaths,
		ReasonCodes:       []string{"route.multi_file_plan_execute"},
		TaskFamily:        string(workerruntime.TaskFamilyPlanThenExecutePlanner),
	})
}

func planExecuteRoleAliases(projectRoot string, profileName string) map[string]string {
	file, ok, err := backends.LoadFromProjectRoot(projectRoot)
	if err != nil || !ok {
		return nil
	}
	profile, found := file.Profile(profileName)
	if !found {
		return nil
	}
	aliases := make(map[string]string, 3)
	for _, role := range []string{backends.RolePlanner, backends.RoleExecutor, backends.RoleClassifier} {
		if resolution := profile.ResolveRoleAlias(role); resolution.OK && strings.TrimSpace(resolution.Alias) != "" {
			aliases[role] = strings.TrimSpace(resolution.Alias)
		}
	}
	if len(aliases) == 0 {
		return nil
	}
	return aliases
}

func planExecuteModelForRole(cfg config.Config, aliases map[string]string, role string) string {
	if alias := strings.TrimSpace(aliases[role]); alias != "" {
		return alias
	}
	switch role {
	case backends.RoleExecutor:
		if cfg.Implementer != nil && strings.TrimSpace(cfg.Implementer.Model) != "" {
			return strings.TrimSpace(cfg.Implementer.Model)
		}
		return strings.TrimSpace(cfg.LlamaServer.Model)
	case backends.RoleClassifier:
		if strings.TrimSpace(cfg.RouteClassifier.Model) != "" {
			return strings.TrimSpace(cfg.RouteClassifier.Model)
		}
		return strings.TrimSpace(cfg.LlamaServer.Model)
	default:
		return strings.TrimSpace(cfg.LlamaServer.Model)
	}
}
