package planexecute

import (
	"strings"

	"idiom/internal/platform/config"
)

func MissingNewFileAuthorizationPaths(cfg config.Config, state ProgressState, authorizedNewFiles []string, deps Dependencies) ([]string, error) {
	deps = normalizeDependencies(deps)

	if strings.TrimSpace(state.PlanPath) == "" {
		return nil, nil
	}

	plan, err := deps.LoadPlanArtifact(state.ResolvedPlanPath(cfg.Workspace.Root))
	if err != nil {
		return nil, err
	}

	authorized := approvedPlanAuthorizedNewFiles(cfg.Workspace.Root, authorizedNewFiles, plan)
	mismatch := preflightPlanMissingPathAuthorization(cfg.Workspace.Root, authorized, plan)
	if mismatch == nil || strings.TrimSpace(mismatch.Path) == "" {
		return nil, nil
	}

	return []string{mismatch.Path}, nil
}
