package contractcompile_test

import (
	"slices"
	"testing"

	"idiom/internal/contractcompile"
	"idiom/internal/frontier/workerruntime"
)

func TestPlanExecutePlannerVocabularyAlignsWithRuntimeManifest(t *testing.T) {
	manifestTools := workerruntime.ManifestToolsForCapabilityProfile(workerruntime.CapabilityPlanSubmissionPlanner)
	compilerTools := contractcompile.PlanExecutePlannerToolNames()

	for _, tool := range manifestTools {
		if !contractcompile.IsPlanExecutePlannerToolAllowed(tool) {
			t.Fatalf("compiler planner vocabulary does not allow manifest tool %q; compiler=%#v manifest=%#v", tool, compilerTools, manifestTools)
		}
	}
	for _, tool := range compilerTools {
		if !slices.Contains(manifestTools, tool) {
			t.Fatalf("compiler planner vocabulary allows non-manifest tool %q; compiler=%#v manifest=%#v", tool, compilerTools, manifestTools)
		}
	}
}
