package app

import (
	"strings"

	"idiom/internal/workflows/featureplanner"
	planexecuteadapter "idiom/internal/workflows/featureplanner/adapters/planexecute"
)

func (m *Model) reconcileFeaturePlannerActivePlanExecuteRuns(state *featureplanner.FeatureState) []featureplanner.Event {
	if m == nil || state == nil {
		return nil
	}
	root := strings.TrimSpace(m.projectRoot())
	if root == "" {
		root = strings.TrimSpace(m.cfg.Workspace.Root)
	}
	return planexecuteadapter.ReconcileActiveRuns(state, root)
}
