package workerruntime

import (
	"testing"

	"idiom/internal/contracts/autonomy"
)

func TestTaskFamilyAliasesMatchAutonomyVocabulary(t *testing.T) {
	cases := map[string]struct {
		got  TaskFamilyID
		want autonomy.TaskFamilyID
	}{
		"artifact final answer":       {TaskFamilyArtifactFinalAnswer, autonomy.TaskFamilyArtifactFinalAnswer},
		"artifact distiller":          {TaskFamilyArtifactDistiller, autonomy.TaskFamilyArtifactDistiller},
		"conversation":                {TaskFamilyConversation, autonomy.TaskFamilyConversation},
		"debug logs":                  {TaskFamilyDebugLogs, autonomy.TaskFamilyDebugLogs},
		"verification only":           {TaskFamilyVerificationOnly, autonomy.TaskFamilyVerificationOnly},
		"locate":                      {TaskFamilyLocate, autonomy.TaskFamilyLocate},
		"locate then implement":       {TaskFamilyLocateThenImplement, autonomy.TaskFamilyLocateThenImplement},
		"explore then implement":      {TaskFamilyExploreThenImplement, autonomy.TaskFamilyExploreThenImplement},
		"investigate then implement":  {TaskFamilyInvestigateThenImplement, autonomy.TaskFamilyInvestigateThenImplement},
		"review then implement":       {TaskFamilyReviewThenImplement, autonomy.TaskFamilyReviewThenImplement},
		"architecture then implement": {TaskFamilyArchitectureThenImplement, autonomy.TaskFamilyArchitectureThenImplement},
		"security review":             {TaskFamilySecurityReview, autonomy.TaskFamilySecurityReview},
		"performance review":          {TaskFamilyPerformanceReview, autonomy.TaskFamilyPerformanceReview},
		"architecture":                {TaskFamilyArchitecture, autonomy.TaskFamilyArchitecture},
		"feature planner planner":     {TaskFamilyFeaturePlannerPlanner, autonomy.TaskFamilyFeaturePlannerPlanner},
		"feature planner implementor": {TaskFamilyFeaturePlannerImplementor, autonomy.TaskFamilyFeaturePlannerImplementor},
		"plan execute planner":        {TaskFamilyPlanThenExecutePlanner, autonomy.TaskFamilyPlanThenExecutePlanner},
		"plan execute review":         {TaskFamilyPlanThenExecuteReview, autonomy.TaskFamilyPlanThenExecuteReview},
		"implementation":              {TaskFamilyImplementation, autonomy.TaskFamilyImplementation},
		"test authoring":              {TaskFamilyTestAuthoring, autonomy.TaskFamilyTestAuthoring},
		"config edit":                 {TaskFamilyConfigEdit, autonomy.TaskFamilyConfigEdit},
		"script edit":                 {TaskFamilyScriptEdit, autonomy.TaskFamilyScriptEdit},
		"gdscript edit":               {TaskFamilyGDScriptEdit, autonomy.TaskFamilyGDScriptEdit},
		"docs edit":                   {TaskFamilyDocsEdit, autonomy.TaskFamilyDocsEdit},
		"prompt edit security":        {TaskFamilyPromptEditSecurity, autonomy.TaskFamilyPromptEditSecurity},
		"failure injection":           {TaskFamilyFailureInjection, autonomy.TaskFamilyFailureInjection},
		"direct question":             {TaskFamilyDirectQuestion, autonomy.TaskFamilyDirectQuestion},
		"direct answer":               {TaskFamilyDirectAnswer, autonomy.TaskFamilyDirectAnswer},
		"delegated isolated impl":     {TaskFamilyDelegatedIsolatedImpl, autonomy.TaskFamilyDelegatedIsolatedImpl},
		"delegated read only":         {TaskFamilyDelegatedReadOnly, autonomy.TaskFamilyDelegatedReadOnly},
		"implementation repair":       {TaskFamilyImplementationRepair, autonomy.TaskFamilyImplementationRepair},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Fatalf("task family alias drifted: got %q, want %q", tc.got, tc.want)
			}
		})
	}
}
