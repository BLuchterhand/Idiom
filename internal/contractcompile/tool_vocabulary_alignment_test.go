package contractcompile_test

import (
	"slices"
	"testing"

	"idiom/internal/autonomy"
	"idiom/internal/contractcompile"
)

func TestPlanExecutePlannerVocabularyAlignsWithAutonomyMetadata(t *testing.T) {
	canonicalTools := autonomy.PlanExecutePlannerToolNames()
	compilerTools := contractcompile.PlanExecutePlannerToolNames()

	if !slices.Equal(compilerTools, canonicalTools) {
		t.Fatalf("compiler planner tool names = %#v, want autonomy metadata names %#v", compilerTools, canonicalTools)
	}
	for _, entry := range autonomy.PlanExecutePlannerToolMetadataEntries() {
		if got := contractcompile.IsPlanExecutePlannerToolAllowed(entry.Tool); got != entry.PlannerAllowed {
			t.Fatalf("compiler planner allowed(%q) = %v, want autonomy metadata %v", entry.Tool, got, entry.PlannerAllowed)
		}
	}
}
