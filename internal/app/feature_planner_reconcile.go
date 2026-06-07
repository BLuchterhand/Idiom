package app

import (
	"strings"

	"idiom/internal/featureplanner"
	"idiom/internal/featureplannerplanexecute"
)

func (m *Model) reconcileFeaturePlannerActivePlanExecuteRuns(state *featureplanner.FeatureState) []featureplanner.Event {
	if m == nil || state == nil {
		return nil
	}
	root := strings.TrimSpace(m.projectRoot())
	if root == "" {
		root = strings.TrimSpace(m.cfg.Workspace.Root)
	}
	return featureplannerplanexecute.ReconcileActiveRuns(state, root)
}
