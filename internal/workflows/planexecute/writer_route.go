package planexecute

import (
	"path/filepath"
	"strings"

	"idiom/internal/contracts/writerroute"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/backends"
	"idiom/internal/runtime/workerruntime"
)

func planExecuteWriterRouteDecision(cfg config.Config, opts Options, requiredEditPaths []string) writerroute.Decision {
	aliases := planExecuteRoleAliases(cfg.Workspace.Root, "")
	userOverride := opts.UseActiveModel || strings.TrimSpace(opts.Model) != ""
	if userOverride {
		aliases = nil
	}
	plannerModel := planExecuteModelForRole(cfg, aliases, backends.RolePlanner)
	executorModel := planExecuteModelForRole(cfg, aliases, backends.RoleExecutor)
	classifierModel := planExecuteModelForRole(cfg, aliases, backends.RoleClassifier)

	return writerroute.NewDecisionFromParams(writerroute.DecisionParams{
		Mode:              writerroute.ModePlanSubmission,
		Lane:              "plan-execute",
		PlannerModel:      plannerModel,
		ExecutorModel:     executorModel,
		ReviewerModel:     executorModel,
		ClassifierModel:   classifierModel,
		EffectiveModel:    writerroute.EffectiveModelForMode(writerroute.ModePlanSubmission, plannerModel, executorModel, classifierModel),
		RoleAliases:       aliases,
		VerificationOwner: "planner_owned",
		RequiredEditPaths: requiredEditPaths,
		ReasonCodes:       []string{"route.multi_file_plan_execute"},
		UserOverride:      userOverride,
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
	switch role {
	case backends.RoleExecutor:
		if cfg.Implementer != nil && strings.TrimSpace(cfg.Implementer.Model) != "" {
			return planExecuteDisplayModel(*cfg.Implementer)
		}

		return planExecuteDisplayModel(cfg.LlamaServer)
	case backends.RoleClassifier:
		if strings.TrimSpace(cfg.RouteClassifier.Model) != "" {
			return strings.TrimSpace(cfg.RouteClassifier.Model)
		}

		return planExecuteDisplayModel(cfg.LlamaServer)
	default:
		if strings.TrimSpace(cfg.LlamaServer.Model) != "" {
			return planExecuteDisplayModel(cfg.LlamaServer)
		}
	}

	if alias := strings.TrimSpace(aliases[role]); alias != "" {
		return alias
	}

	return ""
}

func planExecuteDisplayModel(server config.LlamaServer) string {
	if display := strings.TrimSpace(server.DisplayName); display != "" {
		return display
	}

	model := strings.TrimSpace(server.Model)
	if model == "" {
		return ""
	}

	if filepath.IsAbs(model) {
		if base := strings.TrimSpace(filepath.Base(model)); base != "" {
			return base
		}
	}

	return model
}
