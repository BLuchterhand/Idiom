package productworkflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestADRDraftPromptPlanDeterministic(t *testing.T) {
	root := t.TempDir()
	evidence := []EvidenceCard{
		{CardID: "EV-002", Kind: "finding", Claim: "second claim", Confidence: "medium", Freshness: "current"},
		{CardID: "EV-001", Kind: "unsupported_claim", Claim: "first claim", Confidence: "unsupported", Freshness: "stale", Uncertainty: "needs follow-up"},
	}
	prd := PRDDocument{Requirements: []PRDRequirement{
		{ID: "REQ-002", Priority: "should", Statement: "second requirement", EvidenceRefs: []string{"EV-002"}, TDDObligations: []string{"TDD-2"}},
		{ID: "REQ-001", Priority: "must", Statement: "first requirement", EvidenceRefs: []string{"EV-001"}, TDDObligations: []string{"TDD-1"}},
	}}
	plan1 := PlanADRDraftPrompt(root, "adrs/adr.prompt.md", "adrs/ADR-draft.md", evidence, prd)
	plan2 := PlanADRDraftPrompt(root, "adrs/adr.prompt.md", "adrs/ADR-draft.md", evidence, prd)
	if canonicalADRDraftPlan(plan1) != canonicalADRDraftPlan(plan2) {
		t.Fatalf("expected deterministic ADR draft prompt plan")
	}
	if plan1.Command != "idiom artifact --prompt-file adrs/adr.prompt.md --output adrs/ADR-draft.md --json-out events/adr-draft.events.jsonl --report-out reports/adr-draft.report.json --no-tools --mechanical-repair" {
		t.Fatalf("unexpected command recipe: %q", plan1.Command)
	}
	prompt := RenderADRDraftPromptText(plan1)
	assertContains(t, prompt, "ADR parser/audit contract")
	assertContains(t, prompt, "Keep acceptance metadata fields empty unless a human explicitly provided them")
	assertContains(t, prompt, "Do not claim acceptance, approval, or supersession.")
	assertContains(t, prompt, "PRD requirements (2 total):")
	assertContains(t, prompt, "Evidence cards (2 total):")
}

func TestADRDraftPromptPlanRejectsUnsafePaths(t *testing.T) {
	root := t.TempDir()
	plan := PlanADRDraftPrompt(root, "../bad.prompt.md", "adrs/ADR-draft.md", nil, PRDDocument{})
	if !hasFindingKind(plan.Findings, "invalid_adr_prompt_path") {
		t.Fatalf("expected invalid_adr_prompt_path finding, got %#v", plan.Findings)
	}
	plan = PlanADRDraftPrompt(root, "adrs/adr.prompt.md", "../bad.md", nil, PRDDocument{})
	if !hasFindingKind(plan.Findings, "invalid_adr_output_path") {
		t.Fatalf("expected invalid_adr_output_path finding, got %#v", plan.Findings)
	}
}

func TestADRDraftPromptPlanRejectsSymlinkPromptPath(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "adrs"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Symlink(filepath.Join(outside, "adr.prompt.md"), filepath.Join(root, "adrs", "adr.prompt.md")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	plan := PlanADRDraftPrompt(root, "adrs/adr.prompt.md", "adrs/ADR-draft.md", nil, PRDDocument{})
	if !hasFindingKind(plan.Findings, "research_prompt_symlink") {
		t.Fatalf("expected research_prompt_symlink finding, got %#v", plan.Findings)
	}
}

func TestADRDraftPromptPlanRejectsSymlinkPromptAncestor(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "adrs")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	plan := PlanADRDraftPrompt(root, "adrs/adr.prompt.md", "adrs/ADR-draft.md", nil, PRDDocument{})
	if !hasFindingKind(plan.Findings, "research_prompt_path_outside_effort") {
		t.Fatalf("expected research_prompt_path_outside_effort finding, got %#v", plan.Findings)
	}
}

func canonicalADRDraftPlan(plan ADRDraftPromptPlan) string {
	out := strings.Join([]string{plan.EvidenceLedgerPath, plan.PRDInputPath, plan.PromptPath, plan.ADROutputPath, plan.EventsPath, plan.ReportPath, plan.Command}, "|")
	out += "|" + strings.Join(plan.EvidenceCardIDs, ",")
	out += "|" + strings.Join(plan.PRDRequirementIDs, ",")
	for _, summary := range plan.EvidenceSummaries {
		out += "|" + summary.CardID + ":" + summary.Kind + ":" + summary.Claim
	}
	for _, summary := range plan.PRDRequirementSummary {
		out += "|" + summary.ID + ":" + summary.Priority + ":" + summary.Statement
	}
	for _, finding := range plan.Findings {
		out += "|" + finding.Kind + ":" + finding.File + ":" + finding.Severity
	}
	return out
}
