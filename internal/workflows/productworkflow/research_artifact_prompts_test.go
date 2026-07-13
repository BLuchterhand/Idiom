package productworkflow

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("research artifact prompts", func() {
	ginkgo.It("ResearchArtifactPromptPlanDeterministic", func() {
		profiles := []ResearchProfile{{
			Name:      "primary",
			ProfileID: "test-profile",
			Researchers: []Researcher{
				{
					ID:                  "r1",
					Alignment:           "technical",
					Question:            "What changed?",
					RequiredSources:     []string{"docs/notes.md", "logs/telemetry.jsonl"},
					AllowedCapabilities: []string{"context_query", "read_file"},
					Output:              "evidence_cards",
				},
			},
		}}
		runPlan := PlanResearchRuns(profiles, "", "research/outputs")
		plan1 := PlanResearchArtifactPrompts(runPlan, "research/prompts")
		plan2 := PlanResearchArtifactPrompts(runPlan, "research/prompts")
		gomega.Expect(canonicalArtifactPromptPlan(plan1) != canonicalArtifactPromptPlan(plan2)).To(gomega.BeFalse(), fmt.Sprintf("expected deterministic prompt plan output"))
		gomega.Expect(plan1.RunCount != 1).To(gomega.BeFalse(), fmt.Sprintf("expected one run, got %d", plan1.RunCount))

		run := plan1.Runs[0]
		gomega.Expect(run.PromptPath != "research/prompts/test-profile--r1.prompt.txt").To(gomega.BeFalse(), fmt.Sprintf("unexpected prompt path: %q", run.PromptPath))

		wantCmd := "idiom artifact --prompt-file research/prompts/test-profile--r1.prompt.txt --output research/outputs/test-profile--r1.json --json-out research/outputs/test-profile--r1.events.jsonl --report-out research/outputs/test-profile--r1.report.json --no-tools --mechanical-repair"
		gomega.Expect(run.Command != wantCmd).To(gomega.BeFalse(), fmt.Sprintf("unexpected command recipe:\nwant: %s\ngot:  %s", wantCmd, run.Command))

		prompt := RenderResearchArtifactPromptText(run)
		assertContains(prompt, "You are r1 with alignment technical.")
		assertContains(prompt, "Research question:\nWhat changed?")
		assertContains(prompt, "Required sources:\n- docs/notes.md\n- logs/telemetry.jsonl")
		assertContains(prompt, "Allowed capabilities:\n- context_query\n- read_file")
		assertContains(prompt, "- Output path: research/outputs/test-profile--r1.json")
		assertContains(prompt, "- Output contract schema_version: idiom.research_cards.v1")
		assertContains(prompt, "- Write JSON only.")
		assertContains(prompt, "Do not call tools or inspect the workspace.")
		assertContains(prompt, "required sources and allowed capabilities describe host-provided evidence boundaries")
	})
	ginkgo.It("ResearchArtifactPromptPlanRejectsInvalidPromptDir", func() {
		profiles := []ResearchProfile{{
			Name:      "primary",
			ProfileID: "test-profile",
			Researchers: []Researcher{
				{ID: "r1", Alignment: "technical", Question: "q", AllowedCapabilities: []string{"context_query"}, Output: "evidence_cards"},
			},
		}}
		runPlan := PlanResearchRuns(profiles, "", "research/outputs")
		plan := PlanResearchArtifactPrompts(runPlan, "../bad")
		gomega.Expect(plan.RunCount != 0).To(gomega.BeFalse(), fmt.Sprintf("expected no runs for invalid prompt dir, got %d", plan.RunCount))
		gomega.Expect(!hasFindingKind(plan.Findings, "invalid_research_prompt_dir")).To(gomega.BeFalse(), fmt.Sprintf("expected invalid_research_prompt_dir finding, got %#v", plan.Findings))

	})
	ginkgo.It("ResearchArtifactPromptCommandRecipeQuotesUnsafePaths", func() {
		runPlan := ResearchRunPlan{
			SelectedProfileID: "test-profile",
			ResearchOutputDir: "research outputs",
			Runs: []ResearchRunSpec{{
				ResearcherID:   "r1",
				OutputPath:     "research outputs/test-profile--r1.json",
				OutputContract: ResearchRunOutputContract{SchemaVersion: "idiom.research_cards.v1"},
			}},
		}
		plan := PlanResearchArtifactPrompts(runPlan, "research prompts")
		gomega.Expect(plan.RunCount != 1).To(gomega.BeFalse(), fmt.Sprintf("expected one run, got %d findings=%#v", plan.RunCount, plan.Findings))

		command := plan.Runs[0].Command
		assertContains(command, "--prompt-file 'research prompts/test-profile--r1.prompt.txt'")
		assertContains(command, "--output 'research outputs/test-profile--r1.json'")
		assertContains(command, "--json-out 'research outputs/test-profile--r1.events.jsonl'")
		assertContains(command, "--report-out 'research outputs/test-profile--r1.report.json'")
	})
	ginkgo.It("ResearchArtifactPromptCommandRecipeEscapesSingleQuote", func() {
		got := artifactCommandRecipe("research/prompts/a'b.prompt.txt", "research/outputs/a'b.json", "research/outputs/a'b.events.jsonl", "research/outputs/a'b.report.json")
		assertContains(got, "--prompt-file 'research/prompts/a'\"'\"'b.prompt.txt'")
		assertContains(got, "--output 'research/outputs/a'\"'\"'b.json'")
	})
	ginkgo.It("ResearchArtifactPromptBoundaryRejectsSymlinkParentOutsideEffort", func() {
		effortRoot := ginkgo.GinkgoT().TempDir()
		outside := ginkgo.GinkgoT().TempDir()

		if err := os.Symlink(outside, filepath.Join(effortRoot, "linked")); err != nil {
			ginkgo.Skip(fmt.Sprintf("symlink unavailable: %v", err))
		}

		absPromptDir := filepath.Join(effortRoot, "linked", "prompts")
		finding, blocked := ValidateResearchPromptDirBoundary(effortRoot, absPromptDir, "linked/prompts")
		gomega.Expect(!blocked).To(gomega.BeFalse(), fmt.Sprintf("expected symlink parent prompt dir to block"))
		gomega.Expect(finding.Kind != "research_prompt_dir_outside_effort").To(gomega.BeFalse(), fmt.Sprintf("expected research_prompt_dir_outside_effort, got %#v", finding))

	})
	ginkgo.It("ResearchArtifactPromptBoundaryRejectsSymlinkPromptFile", func() {
		effortRoot := ginkgo.GinkgoT().TempDir()
		outside := ginkgo.GinkgoT().TempDir()
		{

			err := os.MkdirAll(filepath.Join(effortRoot, "research", "prompts"), 0o755)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir prompts: %v", err))
		}

		if err := os.Symlink(filepath.Join(outside, "prompt.txt"), filepath.Join(effortRoot, "research", "prompts", "a.prompt.txt")); err != nil {
			ginkgo.Skip(fmt.Sprintf("symlink unavailable: %v", err))
		}

		finding, blocked := ValidateResearchPromptPathBoundary(effortRoot, filepath.Join(effortRoot, "research", "prompts", "a.prompt.txt"), "research/prompts/a.prompt.txt")
		gomega.Expect(!blocked).To(gomega.BeFalse(), fmt.Sprintf("expected symlink prompt file to block"))
		gomega.Expect(finding.Kind != "research_prompt_symlink").To(gomega.BeFalse(), fmt.Sprintf("expected research_prompt_symlink, got %#v", finding))

	})
})

func canonicalArtifactPromptPlan(plan ResearchArtifactPromptPlan) string {
	out := plan.SelectedProfileID + "|" + plan.ResearchPromptDir + "|" + plan.ResearchOutputDir
	for _, run := range plan.Runs {
		out += "|" + run.ResearcherID + "|" + run.PromptPath + "|" + run.OutputPath + "|" + run.EventsPath + "|" + run.ReportPath + "|" + run.Command
	}

	return out
}

func assertContains(haystack, needle string) {
	ginkgo.GinkgoHelper()
	gomega.Expect(!strings.Contains(haystack, needle)).To(gomega.BeFalse(), fmt.Sprintf("expected prompt to contain %q\nprompt:\n%s", needle, haystack))

}
