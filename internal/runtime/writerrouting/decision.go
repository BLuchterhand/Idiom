package writerrouting

import (
	"strings"

	"idiom/internal/contracts/writerroute"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/backends"
)

type DecisionOptions struct {
	Mode              writerroute.WriterMode
	Lane              string
	TaskFamily        string
	PlannerModel      string
	ExecutorModel     string
	ReviewerModel     string
	ClassifierModel   string
	RequiredEditPaths []string
	ReasonCodes       []writerroute.ReasonCode
	VerificationOwner writerroute.VerificationOwner
	RoleAliases       map[string]string
	UserOverride      bool
}

func BuildDecision(cfg config.Config, opts DecisionOptions) writerroute.Decision {
	aliases := opts.RoleAliases
	if len(aliases) == 0 && !opts.UserOverride {
		aliases = RoleAliasesForConfig(cfg, "")
	}
	selection := ModelSelection{
		Config:                    cfg,
		Aliases:                   aliases,
		PlannerModel:              opts.PlannerModel,
		ExecutorModel:             opts.ExecutorModel,
		ClassifierModel:           opts.ClassifierModel,
		UseBaseModelForClassifier: true,
		UseDisplayModel:           true,
	}

	plannerModel := ModelForRole(selection, backends.RolePlanner)
	executorModel := ModelForRole(selection, backends.RoleExecutor)
	classifierModel := ModelForRole(selection, backends.RoleClassifier)

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
		EffectiveModel:    writerroute.EffectiveModelForMode(opts.Mode, plannerModel, executorModel, classifierModel),
		RoleAliases:       aliases,
		VerificationOwner: opts.VerificationOwner,
		RequiredEditPaths: opts.RequiredEditPaths,
		ReasonCodes:       opts.ReasonCodes,
		TaskFamily:        opts.TaskFamily,
		UserOverride:      opts.UserOverride,
	})
}
