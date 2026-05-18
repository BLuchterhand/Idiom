package productworkflow

import "testing"

func TestResearchRunPlanDeterministicSortedOutput(t *testing.T) {
	profiles := []ResearchProfile{{
		Name:      "primary",
		ProfileID: "alpha",
		Researchers: []Researcher{
			{ID: "z-last", Alignment: "market", Question: "q2", RequiredSources: []string{"b"}, AllowedCapabilities: []string{"context_query"}, Output: "evidence_cards"},
			{ID: "a-first", Alignment: "tech", Question: "q1", RequiredSources: []string{"a"}, AllowedCapabilities: []string{"context_query"}, Output: "evidence_cards"},
		},
	}}

	plan1 := PlanResearchRuns(profiles, "", "research/outputs")
	plan2 := PlanResearchRuns(profiles, "", "research/outputs")
	if plan1.RunCount != 2 || plan2.RunCount != 2 {
		t.Fatalf("expected 2 runs, got %d and %d", plan1.RunCount, plan2.RunCount)
	}
	if plan1.Runs[0].ResearcherID != "a-first" || plan1.Runs[1].ResearcherID != "z-last" {
		t.Fatalf("expected sorted run order by researcher_id, got %#v", plan1.Runs)
	}
	if plan1.Runs[0].OutputPath != "research/outputs/alpha--a-first.json" {
		t.Fatalf("unexpected output_path: %q", plan1.Runs[0].OutputPath)
	}
	if plan1.Runs[0].OutputContract.SchemaVersion != "idiom.research_cards.v1" {
		t.Fatalf("unexpected schema: %q", plan1.Runs[0].OutputContract.SchemaVersion)
	}
	if canonicalPlan(plan1) != canonicalPlan(plan2) {
		t.Fatalf("expected deterministic plan output")
	}
}

func TestResearchRunPlanBlocksInvalidResearcherID(t *testing.T) {
	profiles := []ResearchProfile{{
		Name:      "primary",
		ProfileID: "alpha",
		Researchers: []Researcher{
			{ID: "ok", Alignment: "market", Question: "q1", AllowedCapabilities: []string{"context_query"}, Output: "evidence_cards"},
			{ID: "../bad", Alignment: "tech", Question: "q2", AllowedCapabilities: []string{"context_query"}, Output: "evidence_cards"},
		},
	}}
	plan := PlanResearchRuns(profiles, "", "research/outputs")
	if plan.RunCount != 0 {
		t.Fatalf("expected no runs when any researcher id is invalid, got %d", plan.RunCount)
	}
	if !hasFindingKind(plan.Findings, "invalid_researcher_id") {
		t.Fatalf("expected invalid_researcher_id finding, got %#v", plan.Findings)
	}
}

func TestResearchRunPlanNormalizesOutputDir(t *testing.T) {
	profiles := []ResearchProfile{{
		Name:      "primary",
		ProfileID: "alpha",
		Researchers: []Researcher{
			{ID: "ok", Alignment: "market", Question: "q1", AllowedCapabilities: []string{"context_query"}, Output: "evidence_cards"},
		},
	}}
	plan := PlanResearchRuns(profiles, "", "research//outputs")
	if plan.ResearchOutputDir != "research/outputs" {
		t.Fatalf("expected cleaned research output dir, got %q", plan.ResearchOutputDir)
	}
	if plan.Runs[0].OutputPath != "research/outputs/alpha--ok.json" {
		t.Fatalf("unexpected output_path: %q", plan.Runs[0].OutputPath)
	}
}

func TestResearchRunPlanBlocksInvalidProfileID(t *testing.T) {
	profiles := []ResearchProfile{{
		Name:      "primary",
		ProfileID: "../bad",
		Researchers: []Researcher{
			{ID: "ok", Alignment: "market", Question: "q1", AllowedCapabilities: []string{"context_query"}, Output: "evidence_cards"},
		},
	}}
	plan := PlanResearchRuns(profiles, "", "research/outputs")
	if plan.RunCount != 0 {
		t.Fatalf("expected no runs for invalid profile id, got %d", plan.RunCount)
	}
	if !hasFindingKind(plan.Findings, "invalid_research_profile_id") {
		t.Fatalf("expected invalid_research_profile_id finding, got %#v", plan.Findings)
	}
}

func TestResearchRunPlanMultipleProfilesRequiresSelector(t *testing.T) {
	profiles := []ResearchProfile{
		{Name: "one", ProfileID: "one", Researchers: []Researcher{{ID: "r1", AllowedCapabilities: []string{"context_query"}, Output: "evidence_cards"}}},
		{Name: "two", ProfileID: "two", Researchers: []Researcher{{ID: "r2", AllowedCapabilities: []string{"context_query"}, Output: "evidence_cards"}}},
	}
	plan := PlanResearchRuns(profiles, "", "research/outputs")
	if plan.RunCount != 0 {
		t.Fatalf("expected no runs when selector is missing, got %d", plan.RunCount)
	}
	if !hasFindingKind(plan.Findings, "ambiguous_research_profile") {
		t.Fatalf("expected ambiguous_research_profile finding, got %#v", plan.Findings)
	}
}

func hasFindingKind(findings []ProfileFinding, kind string) bool {
	for _, finding := range findings {
		if finding.Kind == kind {
			return true
		}
	}
	return false
}

func canonicalPlan(plan ResearchRunPlan) string {
	out := plan.SelectedProfileID + "|" + plan.SelectedProfile + "|" + plan.ResearchOutputDir
	for _, run := range plan.Runs {
		out += "|" + run.ResearcherID + "|" + run.OutputPath + "|" + run.OutputContract.SchemaVersion
	}
	return out
}
