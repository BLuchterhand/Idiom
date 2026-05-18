package productworkflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResearchArtifactPromptPlanDeterministic(t *testing.T) {
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

	if canonicalArtifactPromptPlan(plan1) != canonicalArtifactPromptPlan(plan2) {
		t.Fatalf("expected deterministic prompt plan output")
	}
	if plan1.RunCount != 1 {
		t.Fatalf("expected one run, got %d", plan1.RunCount)
	}
	run := plan1.Runs[0]
	if run.PromptPath != "research/prompts/test-profile--r1.prompt.txt" {
		t.Fatalf("unexpected prompt path: %q", run.PromptPath)
	}
	wantCmd := "idiom artifact --prompt-file research/prompts/test-profile--r1.prompt.txt --output research/outputs/test-profile--r1.json --json-out research/outputs/test-profile--r1.events.jsonl --report-out research/outputs/test-profile--r1.report.json --no-tools --mechanical-repair"
	if run.Command != wantCmd {
		t.Fatalf("unexpected command recipe:\nwant: %s\ngot:  %s", wantCmd, run.Command)
	}
	prompt := RenderResearchArtifactPromptText(run)
	assertContains(t, prompt, "You are r1 with alignment technical.")
	assertContains(t, prompt, "Research question:\nWhat changed?")
	assertContains(t, prompt, "Required sources:\n- docs/notes.md\n- logs/telemetry.jsonl")
	assertContains(t, prompt, "Allowed capabilities:\n- context_query\n- read_file")
	assertContains(t, prompt, "- Output path: research/outputs/test-profile--r1.json")
	assertContains(t, prompt, "- Output contract schema_version: idiom.research_cards.v1")
	assertContains(t, prompt, "- Write JSON only.")
	assertContains(t, prompt, "Do not call tools or inspect the workspace.")
	assertContains(t, prompt, "required sources and allowed capabilities describe host-provided evidence boundaries")
}

func TestResearchArtifactPromptPlanRejectsInvalidPromptDir(t *testing.T) {
	profiles := []ResearchProfile{{
		Name:      "primary",
		ProfileID: "test-profile",
		Researchers: []Researcher{
			{ID: "r1", Alignment: "technical", Question: "q", AllowedCapabilities: []string{"context_query"}, Output: "evidence_cards"},
		},
	}}
	runPlan := PlanResearchRuns(profiles, "", "research/outputs")
	plan := PlanResearchArtifactPrompts(runPlan, "../bad")
	if plan.RunCount != 0 {
		t.Fatalf("expected no runs for invalid prompt dir, got %d", plan.RunCount)
	}
	if !hasFindingKind(plan.Findings, "invalid_research_prompt_dir") {
		t.Fatalf("expected invalid_research_prompt_dir finding, got %#v", plan.Findings)
	}
}

func TestResearchArtifactPromptCommandRecipeQuotesUnsafePaths(t *testing.T) {
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
	if plan.RunCount != 1 {
		t.Fatalf("expected one run, got %d findings=%#v", plan.RunCount, plan.Findings)
	}
	command := plan.Runs[0].Command
	assertContains(t, command, "--prompt-file 'research prompts/test-profile--r1.prompt.txt'")
	assertContains(t, command, "--output 'research outputs/test-profile--r1.json'")
	assertContains(t, command, "--json-out 'research outputs/test-profile--r1.events.jsonl'")
	assertContains(t, command, "--report-out 'research outputs/test-profile--r1.report.json'")
}

func TestResearchArtifactPromptCommandRecipeEscapesSingleQuote(t *testing.T) {
	got := artifactCommandRecipe("research/prompts/a'b.prompt.txt", "research/outputs/a'b.json", "research/outputs/a'b.events.jsonl", "research/outputs/a'b.report.json")
	assertContains(t, got, "--prompt-file 'research/prompts/a'\"'\"'b.prompt.txt'")
	assertContains(t, got, "--output 'research/outputs/a'\"'\"'b.json'")
}

func TestResearchArtifactPromptBoundaryRejectsSymlinkParentOutsideEffort(t *testing.T) {
	effortRoot := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(effortRoot, "linked")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	absPromptDir := filepath.Join(effortRoot, "linked", "prompts")
	finding, blocked := ValidateResearchPromptDirBoundary(effortRoot, absPromptDir, "linked/prompts")
	if !blocked {
		t.Fatalf("expected symlink parent prompt dir to block")
	}
	if finding.Kind != "research_prompt_dir_outside_effort" {
		t.Fatalf("expected research_prompt_dir_outside_effort, got %#v", finding)
	}
}

func TestResearchArtifactPromptBoundaryRejectsSymlinkPromptFile(t *testing.T) {
	effortRoot := t.TempDir()
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(effortRoot, "research", "prompts"), 0o755); err != nil {
		t.Fatalf("mkdir prompts: %v", err)
	}
	if err := os.Symlink(filepath.Join(outside, "prompt.txt"), filepath.Join(effortRoot, "research", "prompts", "a.prompt.txt")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	finding, blocked := ValidateResearchPromptPathBoundary(effortRoot, filepath.Join(effortRoot, "research", "prompts", "a.prompt.txt"), "research/prompts/a.prompt.txt")
	if !blocked {
		t.Fatalf("expected symlink prompt file to block")
	}
	if finding.Kind != "research_prompt_symlink" {
		t.Fatalf("expected research_prompt_symlink, got %#v", finding)
	}
}

func canonicalArtifactPromptPlan(plan ResearchArtifactPromptPlan) string {
	out := plan.SelectedProfileID + "|" + plan.ResearchPromptDir + "|" + plan.ResearchOutputDir
	for _, run := range plan.Runs {
		out += "|" + run.ResearcherID + "|" + run.PromptPath + "|" + run.OutputPath + "|" + run.EventsPath + "|" + run.ReportPath + "|" + run.Command
	}
	return out
}

func assertContains(t *testing.T, haystack, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Fatalf("expected prompt to contain %q\nprompt:\n%s", needle, haystack)
	}
}
