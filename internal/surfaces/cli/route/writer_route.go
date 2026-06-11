package cmdroute

import (
	"strings"

	"idiom/internal/contracts/writerroute"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/backends"
)

type Options struct {
	Mode              writerroute.WriterMode
	Lane              string
	TaskFamily        string
	PlannerModel      string
	ExecutorModel     string
	ReviewerModel     string
	ClassifierModel   string
	RequiredEditPaths []string
	ReasonCodes       []string
	VerificationOwner string
	RoleAliases       map[string]string
}

func BuildDecision(cfg config.Config, opts Options) writerroute.Decision {
	aliases := opts.RoleAliases
	if len(aliases) == 0 {
		aliases = RoleAliases(cfg.Workspace.Root, "")
	}
	plannerModel := strings.TrimSpace(opts.PlannerModel)
	if plannerModel == "" {
		plannerModel = ModelForRole(cfg, aliases, backends.RolePlanner)
	}
	executorModel := strings.TrimSpace(opts.ExecutorModel)
	if executorModel == "" {
		executorModel = ModelForRole(cfg, aliases, backends.RoleExecutor)
	}
	classifierModel := strings.TrimSpace(opts.ClassifierModel)
	if classifierModel == "" {
		classifierModel = ModelForRole(cfg, aliases, backends.RoleClassifier)
	}
	reviewerModel := strings.TrimSpace(opts.ReviewerModel)
	if reviewerModel == "" {
		reviewerModel = executorModel
	}
	return writerroute.NewDecisionFromParams(writerroute.DecisionParams{
		Mode:              opts.Mode,
		Lane:              opts.Lane,
		PlannerModel:      plannerModel,
		ExecutorModel:     executorModel,
		ReviewerModel:     reviewerModel,
		ClassifierModel:   classifierModel,
		EffectiveModel:    EffectiveModelForDecision(opts.Mode, plannerModel, executorModel, classifierModel),
		RoleAliases:       aliases,
		VerificationOwner: strings.TrimSpace(opts.VerificationOwner),
		RequiredEditPaths: opts.RequiredEditPaths,
		ReasonCodes:       opts.ReasonCodes,
		TaskFamily:        opts.TaskFamily,
	})
}

func EffectiveModelForDecision(mode writerroute.WriterMode, plannerModel, executorModel, classifierModel string) string {
	switch mode {
	case writerroute.ModeAgenticGuarded, writerroute.ModeDeterministicArtifactApply, writerroute.ModeHostMaterializedArtifact:
		return strings.TrimSpace(executorModel)
	case writerroute.ModePlanSubmission:
		return strings.TrimSpace(plannerModel)
	case writerroute.ModeReadOnly:
		if model := strings.TrimSpace(classifierModel); model != "" {
			return model
		}
		return strings.TrimSpace(plannerModel)
	default:
		if model := strings.TrimSpace(executorModel); model != "" {
			return model
		}
		return strings.TrimSpace(plannerModel)
	}
}

func RoleAliases(projectRoot string, profileName string) map[string]string {
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
		if alias, ok := profile.AliasForRole(role); ok && strings.TrimSpace(alias) != "" {
			aliases[role] = strings.TrimSpace(alias)
		}
	}
	if len(aliases) == 0 {
		return nil
	}
	return aliases
}

func ModelForRole(cfg config.Config, aliases map[string]string, role string) string {
	switch role {
	case backends.RoleExecutor:
		if cfg.Implementer != nil && strings.TrimSpace(cfg.Implementer.Model) != "" {
			return strings.TrimSpace(cfg.Implementer.Model)
		}
		if model := strings.TrimSpace(cfg.LlamaServer.Model); model != "" {
			return model
		}
	case backends.RoleClassifier:
		if strings.TrimSpace(cfg.RouteClassifier.Model) != "" {
			return strings.TrimSpace(cfg.RouteClassifier.Model)
		}
		if model := strings.TrimSpace(cfg.LlamaServer.Model); model != "" {
			return model
		}
	default:
		if model := strings.TrimSpace(cfg.LlamaServer.Model); model != "" {
			return model
		}
	}
	if alias := strings.TrimSpace(aliases[role]); alias != "" {
		return alias
	}
	return ""
}
