package cmdroute

import (
	"strings"

	"idiom/internal/backends"
	"idiom/internal/config"
	"idiom/internal/writerroute"
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
		RoleAliases:       aliases,
		VerificationOwner: strings.TrimSpace(opts.VerificationOwner),
		RequiredEditPaths: opts.RequiredEditPaths,
		ReasonCodes:       opts.ReasonCodes,
		TaskFamily:        opts.TaskFamily,
	})
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
