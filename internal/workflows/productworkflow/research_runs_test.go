package productworkflow

import (
	"fmt"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("research runs", func() {
	ginkgo.It("ResearchRunPlanDeterministicSortedOutput", func() {
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
		gomega.Expect(plan1.RunCount).To(gomega.Equal(2), fmt.Sprintf("expected 2 runs, got %d and %d", plan1.RunCount, plan2.RunCount))
		gomega.Expect(plan2.RunCount).To(gomega.Equal(2), fmt.Sprintf("expected 2 runs, got %d and %d", plan1.RunCount, plan2.RunCount))
		gomega.Expect(plan1.Runs[0].ResearcherID).To(gomega.Equal("a-first"), fmt.Sprintf("expected sorted run order by researcher_id, got %#v", plan1.Runs))
		gomega.Expect(plan1.Runs[1].ResearcherID).To(gomega.Equal("z-last"), fmt.Sprintf("expected sorted run order by researcher_id, got %#v", plan1.Runs))
		gomega.Expect(plan1.Runs[0].OutputPath != "research/outputs/alpha--a-first.json").To(gomega.BeFalse(), fmt.Sprintf("unexpected output_path: %q", plan1.Runs[0].OutputPath))
		gomega.Expect(plan1.Runs[0].OutputContract.SchemaVersion != "idiom.research_cards.v1").To(gomega.BeFalse(), fmt.Sprintf("unexpected schema: %q", plan1.Runs[0].OutputContract.SchemaVersion))
		gomega.Expect(canonicalPlan(plan1) != canonicalPlan(plan2)).To(gomega.BeFalse(), fmt.Sprintf("expected deterministic plan output"))

	})
	ginkgo.It("ResearchRunPlanBlocksInvalidResearcherID", func() {
		profiles := []ResearchProfile{{
			Name:      "primary",
			ProfileID: "alpha",
			Researchers: []Researcher{
				{ID: "ok", Alignment: "market", Question: "q1", AllowedCapabilities: []string{"context_query"}, Output: "evidence_cards"},
				{ID: "../bad", Alignment: "tech", Question: "q2", AllowedCapabilities: []string{"context_query"}, Output: "evidence_cards"},
			},
		}}
		plan := PlanResearchRuns(profiles, "", "research/outputs")
		gomega.Expect(plan.RunCount != 0).To(gomega.BeFalse(), fmt.Sprintf("expected no runs when any researcher id is invalid, got %d", plan.RunCount))
		gomega.Expect(!hasFindingKind(plan.Findings, "invalid_researcher_id")).To(gomega.BeFalse(), fmt.Sprintf("expected invalid_researcher_id finding, got %#v", plan.Findings))

	})
	ginkgo.It("ResearchRunPlanNormalizesOutputDir", func() {
		profiles := []ResearchProfile{{
			Name:      "primary",
			ProfileID: "alpha",
			Researchers: []Researcher{
				{ID: "ok", Alignment: "market", Question: "q1", AllowedCapabilities: []string{"context_query"}, Output: "evidence_cards"},
			},
		}}
		plan := PlanResearchRuns(profiles, "", "research//outputs")
		gomega.Expect(plan.ResearchOutputDir != "research/outputs").To(gomega.BeFalse(), fmt.Sprintf("expected cleaned research output dir, got %q", plan.ResearchOutputDir))
		gomega.Expect(plan.Runs[0].OutputPath != "research/outputs/alpha--ok.json").To(gomega.BeFalse(), fmt.Sprintf("unexpected output_path: %q", plan.Runs[0].OutputPath))

		backslashPlan := PlanResearchRuns(profiles, "", `research\outputs`)
		gomega.Expect(backslashPlan.ResearchOutputDir != "research/outputs").To(gomega.BeFalse(), fmt.Sprintf("expected backslash-separated output dir to normalize, got %q", backslashPlan.ResearchOutputDir))
		gomega.Expect(backslashPlan.Runs[0].OutputPath != "research/outputs/alpha--ok.json").To(gomega.BeFalse(), fmt.Sprintf("unexpected backslash output_path: %q", backslashPlan.Runs[0].OutputPath))

	})
	ginkgo.It("ResearchRunPlanBlocksBackslashTraversalOutputDir", func() {
		profiles := []ResearchProfile{{
			Name:      "primary",
			ProfileID: "alpha",
			Researchers: []Researcher{
				{ID: "ok", Alignment: "market", Question: "q1", AllowedCapabilities: []string{"context_query"}, Output: "evidence_cards"},
			},
		}}
		plan := PlanResearchRuns(profiles, "", `..\outside`)
		gomega.Expect(plan.RunCount != 0).To(gomega.BeFalse(), fmt.Sprintf("expected no runs for traversal output dir, got %d", plan.RunCount))
		gomega.Expect(!hasFindingKind(plan.Findings, "invalid_research_output_dir")).To(gomega.BeFalse(), fmt.Sprintf("expected invalid_research_output_dir finding, got %#v", plan.Findings))

	})
	ginkgo.It("ResearchRunPlanBlocksInvalidProfileID", func() {
		profiles := []ResearchProfile{{
			Name:      "primary",
			ProfileID: "../bad",
			Researchers: []Researcher{
				{ID: "ok", Alignment: "market", Question: "q1", AllowedCapabilities: []string{"context_query"}, Output: "evidence_cards"},
			},
		}}
		plan := PlanResearchRuns(profiles, "", "research/outputs")
		gomega.Expect(plan.RunCount != 0).To(gomega.BeFalse(), fmt.Sprintf("expected no runs for invalid profile id, got %d", plan.RunCount))
		gomega.Expect(!hasFindingKind(plan.Findings, "invalid_research_profile_id")).To(gomega.BeFalse(), fmt.Sprintf("expected invalid_research_profile_id finding, got %#v", plan.Findings))

	})
	ginkgo.It("ResearchRunPlanMultipleProfilesRequiresSelector", func() {
		profiles := []ResearchProfile{
			{Name: "one", ProfileID: "one", Researchers: []Researcher{{ID: "r1", AllowedCapabilities: []string{"context_query"}, Output: "evidence_cards"}}},
			{Name: "two", ProfileID: "two", Researchers: []Researcher{{ID: "r2", AllowedCapabilities: []string{"context_query"}, Output: "evidence_cards"}}},
		}
		plan := PlanResearchRuns(profiles, "", "research/outputs")
		gomega.Expect(plan.RunCount != 0).To(gomega.BeFalse(), fmt.Sprintf("expected no runs when selector is missing, got %d", plan.RunCount))
		gomega.Expect(!hasFindingKind(plan.Findings, "ambiguous_research_profile")).To(gomega.BeFalse(), fmt.Sprintf("expected ambiguous_research_profile finding, got %#v", plan.Findings))

	})
})

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
