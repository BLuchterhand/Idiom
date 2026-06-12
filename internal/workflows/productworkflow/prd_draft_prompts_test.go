package productworkflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPRDDraftPromptPlanDeterministic(t *testing.T) {
	root := t.TempDir()
	evidence := []EvidenceCard{
		{CardID: "EV-002", Kind: "finding", Claim: "second claim", Confidence: "medium", Freshness: "current"},
		{CardID: "EV-001", Kind: "unsupported_claim", Claim: "first claim", Confidence: "unsupported", Freshness: "stale", Uncertainty: "needs follow-up"},
	}
	tdd := TDDLedger{Obligations: []TDDObligation{{ID: "TDD-001", Type: "acceptance", Behavior: "first behavior", Owner: "test.js", Command: "npm test"}}}
	plan1 := PlanPRDDraftPrompt(root, "prd.prompt.md", "prd.md", evidence, tdd)
	plan2 := PlanPRDDraftPrompt(root, "prd.prompt.md", "prd.md", evidence, tdd)

	if canonicalPRDDraftPlan(plan1) != canonicalPRDDraftPlan(plan2) {
		t.Fatalf("expected deterministic PRD draft prompt plan")
	}

	if plan1.Command != `idiom artifact --prompt-file prd.prompt.md --output prd.md --json-out events/prd-draft.events.jsonl --report-out reports/prd-draft.report.json --no-tools --mechanical-repair --validator 'idiom product validate-artifact --kind prd --path "$IDIOM_ARTIFACT" --tdd-ledger tdd.json'` {
		t.Fatalf("unexpected command recipe: %q", plan1.Command)
	}

	prompt := RenderPRDDraftPromptText(plan1)
	assertContains(t, prompt, "PRD parser/audit contract")
	assertContains(t, prompt, "Do not use markdown tables for requirements")
	assertContains(t, prompt, "- **tdd_obligations**:")
	assertContains(t, prompt, "Inline array fields must use bracket syntax")
	assertContains(t, prompt, "tdd_obligations values must be IDs")
	assertContains(t, prompt, "Known TDD obligations (1 total):")
	assertContains(t, prompt, "TDD-001")
	assertContains(t, prompt, "Preserve unsupported claims and conflicts as open questions or explicitly labeled inference.")
	assertContains(t, prompt, "Do not claim acceptance, approval, or final decision status.")
	assertContains(t, prompt, "Evidence cards (2 total):")
	assertContains(t, prompt, "- EV-001 |")
}

func TestPRDDraftPromptPlanRejectsUnsafePaths(t *testing.T) {
	root := t.TempDir()
	plan := PlanPRDDraftPrompt(root, "../bad.prompt.md", "prd.md", nil, TDDLedger{})

	if !hasFindingKind(plan.Findings, "invalid_prd_prompt_path") {
		t.Fatalf("expected invalid_prd_prompt_path finding, got %#v", plan.Findings)
	}

	plan = PlanPRDDraftPrompt(root, "prd.prompt.md", "../prd.md", nil, TDDLedger{})
	if !hasFindingKind(plan.Findings, "invalid_prd_output_path") {
		t.Fatalf("expected invalid_prd_output_path finding, got %#v", plan.Findings)
	}
}

func TestPRDDraftPromptPlanRejectsSymlinkPromptPath(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()

	if err := os.MkdirAll(filepath.Join(root, "prompts"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if err := os.Symlink(filepath.Join(outside, "prd.prompt.md"), filepath.Join(root, "prompts", "prd.prompt.md")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	plan := PlanPRDDraftPrompt(root, "prompts/prd.prompt.md", "prd.md", nil, TDDLedger{})
	if !hasFindingKind(plan.Findings, "research_prompt_symlink") {
		t.Fatalf("expected research_prompt_symlink finding, got %#v", plan.Findings)
	}
}

func TestPRDDraftPromptPlanRejectsSymlinkPromptAncestor(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()

	if err := os.Symlink(outside, filepath.Join(root, "prompts")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	plan := PlanPRDDraftPrompt(root, "prompts/prd.prompt.md", "prd.md", nil, TDDLedger{})
	if !hasFindingKind(plan.Findings, "research_prompt_path_outside_effort") {
		t.Fatalf("expected research_prompt_path_outside_effort finding, got %#v", plan.Findings)
	}
}

func canonicalPRDDraftPlan(plan PRDDraftPromptPlan) string {
	out := strings.Join([]string{plan.EvidenceLedgerPath, plan.PromptPath, plan.PRDOutputPath, plan.EventsPath, plan.ReportPath, plan.Command}, "|")

	out += "|" + strings.Join(plan.EvidenceCardIDs, ",")
	out += "|" + strings.Join(plan.TDDObligationIDs, ",")

	for _, summary := range plan.EvidenceSummaries {
		out += "|" + summary.CardID + ":" + summary.Kind + ":" + summary.Claim
	}

	for _, finding := range plan.Findings {
		out += "|" + finding.Kind + ":" + finding.File + ":" + finding.Severity
	}

	return out
}
