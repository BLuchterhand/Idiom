package productworkflow

import (
	"os"
	"path/filepath"
	"testing"
)

func validPRDDoc() PRDDocument {
	return PRDDocument{
		FrontMatter: PRDFrontMatter{
			SchemaVersion: "idiom.prd.v1", PRDID: "PRD-001", Status: string(ArtifactStateDraft),
			TaskID: "task-001", CreatedAt: "2026-06-17T00:00:00Z",
		},
		Requirements: []PRDRequirement{
			{ID: "REQ-001", Priority: "P0", Statement: "Login.", EvidenceRefs: []string{"E1"},
				TDDObligations: []string{"T1"}, OwnerAlignment: "Team", Testability: "T",
				Metric: "M", OpenQuestionsPresent: true},
		},
	}
}

func validADRDoc() ADRDocument {
	return ADRDocument{
		FrontMatter: ADRFrontMatter{
			SchemaVersion: "idiom.adr.v1", ADRID: "ADR-001", Status: string(ArtifactStateDraft),
			PRDRefs: []string{"REQ-001"}, EvidenceRefs: []string{"E1"},
			TDDObligations: []string{"T1"}, CoverageTier: "unit",
		},
		Sections: ADRSections{
			HasContext: true, HasDecision: true, HasAlternativesConsidered: true,
			HasEvidence: true, HasConsequences: true, HasCompatibilityAndMigration: true,
			HasTDDObligations: true, HasRollback: true, HasOpenQuestions: true,
		},
	}
}

func TestInitializeEffortCreatesNew(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "my-feature")

	result, err := InitializeEffort(CreateEffortOptions{EffortRoot: root})
	if err != nil {
		t.Fatalf("InitializeEffort: %v", err)
	}

	if result.Status != "ok" {
		t.Fatalf("expected status ok, got %s", result.Status)
	}

	if result.CurrentPhase != PhasePRDDrafting {
		t.Fatalf("expected prd_drafting, got %s", result.CurrentPhase)
	}

	for _, sub := range []string{"adrs", "evidence", "briefs", "reports"} {
		info, err := os.Stat(filepath.Join(root, sub))
		if err != nil || !info.IsDir() {
			t.Errorf("expected dir %s", sub)
		}
	}

	ws, err := LoadWorkflowState(root)
	if err != nil {
		t.Fatalf("LoadWorkflowState: %v", err)
	}

	if ws.CurrentPhase != PhasePRDDrafting {
		t.Fatalf("workflow phase: %s", ws.CurrentPhase)
	}
}

func TestInitializeEffortCustomIDAndTitle(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "custom-feature")

	result, err := InitializeEffort(CreateEffortOptions{
		EffortRoot: root, EffortID: "EFF-123", Title: "My Custom Feature",
	})
	if err != nil {
		t.Fatalf("InitializeEffort: %v", err)
	}

	if result.EffortID != "EFF-123" {
		t.Fatalf("expected EFF-123, got %s", result.EffortID)
	}

	ws, _ := LoadWorkflowState(root)
	if ws.Title != "My Custom Feature" {
		t.Fatalf("expected 'My Custom Feature', got %s", ws.Title)
	}
}

func TestInitializeEffortResumes(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "existing")

	_, err := InitializeEffort(CreateEffortOptions{EffortRoot: root})
	if err != nil {
		t.Fatalf("first init: %v", err)
	}

	_, err = AdvanceCreatePhase(root, PhasePRDReview)
	if err != nil {
		t.Fatalf("AdvanceCreatePhase: %v", err)
	}

	result, err := InitializeEffort(CreateEffortOptions{EffortRoot: root})
	if err != nil {
		t.Fatalf("resume init: %v", err)
	}

	if result.Status != "resumed" {
		t.Fatalf("expected resumed, got %s", result.Status)
	}

	if result.CurrentPhase != PhasePRDReview {
		t.Fatalf("expected prd_review, got %s", result.CurrentPhase)
	}
}

func TestAdvanceCreatePhaseFullCycle(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "full-cycle")

	_, err := InitializeEffort(CreateEffortOptions{EffortRoot: root})
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	steps := []struct {
		phase    string
		wantNext string
	}{
		{PhasePRDReview, "approve_prd"},
		{PhaseADRDrafting, "approve_adrs"},
		{PhaseADRReview, "approve_adrs"},
		{PhaseTDDSynthesis, "finalize"},
		{PhaseComplete, "view_status"},
	}
	for _, step := range steps {
		result, err := AdvanceCreatePhase(root, step.phase)
		if err != nil {
			t.Fatalf("advance to %s: %v", step.phase, err)
		}

		if result.CurrentPhase != step.phase {
			t.Fatalf("expected phase %s, got %s", step.phase, result.CurrentPhase)
		}

		if step.wantNext != "" && result.NextValidAction != step.wantNext {
			t.Fatalf("phase %s: expected next action %s, got %s", step.phase, step.wantNext, result.NextValidAction)
		}
	}

	ws, _ := LoadWorkflowState(root)
	if ws.StatusBucket != "closed" {
		t.Fatalf("expected closed bucket, got %s", ws.StatusBucket)
	}
}

func TestAdvanceCreatePhaseOutOfOrder(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "out-of-order")

	_, err := InitializeEffort(CreateEffortOptions{EffortRoot: root})
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	// Jumping from prd_drafting to tdd_synthesis is invalid.
	result, err := AdvanceCreatePhase(root, PhaseTDDSynthesis)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Status != "blocked" {
		t.Fatalf("expected blocked for out-of-order transition, got %s", result.Status)
	}
}

func TestAdvanceCreatePhaseTerminalPhase(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "terminal")

	_, err := InitializeEffort(CreateEffortOptions{EffortRoot: root})
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	for _, p := range []string{PhasePRDReview, PhaseADRDrafting, PhaseADRReview, PhaseTDDSynthesis, PhaseComplete} {
		_, err = AdvanceCreatePhase(root, p)
		if err != nil {
			t.Fatalf("advance to %s: %v", p, err)
		}
	}

	// Complete is terminal.
	_, err = AdvanceCreatePhase(root, PhasePRDDrafting)
	if err == nil {
		t.Fatal("expected error advancing from complete")
	}
}

// --- PRD gate tests ---

func TestApprovePRDGateSuccess(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "prd-gate")

	_, err := InitializeEffort(CreateEffortOptions{EffortRoot: root})
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	if err := WritePRDMarkdown(root, validPRDDoc()); err != nil {
		t.Fatalf("write PRD: %v", err)
	}

	_, err = AdvanceCreatePhase(root, PhasePRDReview)
	if err != nil {
		t.Fatalf("advance to review: %v", err)
	}

	result, intake, err := ApprovePRDGate(root)
	if err != nil {
		t.Fatalf("ApprovePRDGate: %v", err)
	}

	if !intake.Passed {
		t.Fatalf("intake should pass: %+v", intake.Failures)
	}

	if result.Status != "ok" {
		t.Fatalf("expected ok, got %s: %v", result.Status, result.Blockers)
	}

	if result.CurrentPhase != PhaseADRDrafting {
		t.Fatalf("expected adr_drafting, got %s", result.CurrentPhase)
	}
}

func TestApprovePRDGateWrongPhase(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "prd-wrong-phase")

	_, err := InitializeEffort(CreateEffortOptions{EffortRoot: root})
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	result, _, err := ApprovePRDGate(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Status != "blocked" {
		t.Fatalf("expected blocked, got %s", result.Status)
	}
}

func TestApprovePRDGateMissingFile(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "prd-missing-file")

	_, err := InitializeEffort(CreateEffortOptions{EffortRoot: root})
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	_, err = AdvanceCreatePhase(root, PhasePRDReview)
	if err != nil {
		t.Fatalf("advance: %v", err)
	}

	result, _, err := ApprovePRDGate(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Status != "blocked" {
		t.Fatalf("expected blocked, got %s", result.Status)
	}
}

func TestApprovePRDGateIntakeFailure(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "prd-intake-fail")

	_, err := InitializeEffort(CreateEffortOptions{EffortRoot: root})
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	// Write PRD with no requirements.
	emptyPRD := PRDDocument{
		FrontMatter: PRDFrontMatter{
			SchemaVersion: "idiom.prd.v1", PRDID: "PRD-001", Status: string(ArtifactStateDraft),
			TaskID: "task-001", CreatedAt: "2026-06-17T00:00:00Z",
		},
	}
	if err := WritePRDMarkdown(root, emptyPRD); err != nil {
		t.Fatalf("write PRD: %v", err)
	}

	_, err = AdvanceCreatePhase(root, PhasePRDReview)
	if err != nil {
		t.Fatalf("advance: %v", err)
	}

	result, intake, err := ApprovePRDGate(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Status != "blocked" {
		t.Fatalf("expected blocked, got %s", result.Status)
	}

	if intake.Passed {
		t.Fatal("expected intake to fail on empty requirements")
	}
}

// --- ADR gate tests ---

func TestApproveADRGateSuccess(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "adr-gate")

	_, err := InitializeEffort(CreateEffortOptions{EffortRoot: root})
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	// Write a valid ADR index + ADR doc.
	idx := ADRIndex{
		SchemaVersion: "idiom.adr_index.v1", EffortID: "adr-gate",
		ADRs: []ADRIndexEntry{
			{ADRID: "ADR-001", Title: "Choose DB", Status: string(ArtifactStateDraft),
				Path: "ADR-001.md", CoverageTier: "unit", PRDRefs: []string{"REQ-001"},
				CreatedAt: "2026-06-17T00:00:00Z"},
			{ADRID: "ADR-002", Title: "Auth Strategy", Status: string(ArtifactStateDraft),
				Path: "ADR-002.md", CoverageTier: "unit", PRDRefs: []string{"REQ-001"},
				CreatedAt: "2026-06-17T00:00:00Z"},
		},
	}
	if err := WriteADRIndexJSON(root, idx); err != nil {
		t.Fatalf("write index: %v", err)
	}

	if err := WriteADRMarkdown(root, "ADR-001.md", validADRDoc()); err != nil {
		t.Fatalf("write ADR-001: %v", err)
	}

	adr2 := validADRDoc()

	adr2.FrontMatter.ADRID = "ADR-002"

	if err := WriteADRMarkdown(root, "ADR-002.md", adr2); err != nil {
		t.Fatalf("write ADR-002: %v", err)
	}

	// Write a valid PRD and advance through PRD gate to reach ADR review.
	if err := WritePRDMarkdown(root, validPRDDoc()); err != nil {
		t.Fatalf("write PRD: %v", err)
	}

	_, err = AdvanceCreatePhase(root, PhasePRDReview)
	if err != nil {
		t.Fatalf("advance to prd_review: %v", err)
	}

	_, _, err = ApprovePRDGate(root)
	if err != nil {
		t.Fatalf("approve PRD: %v", err)
	}

	_, err = AdvanceCreatePhase(root, PhaseADRReview)
	if err != nil {
		t.Fatalf("advance to adr_review: %v", err)
	}

	result, intakes, err := ApproveADRGate(root)
	if err != nil {
		t.Fatalf("ApproveADRGate: %v", err)
	}

	if result.Status != "ok" {
		t.Fatalf("expected ok, got %s: %v", result.Status, result.Blockers)
	}

	if result.CurrentPhase != PhaseTDDSynthesis {
		t.Fatalf("expected tdd_synthesis, got %s", result.CurrentPhase)
	}

	if len(intakes) < 3 {
		t.Fatalf("expected at least 3 intake results (index + 2 ADRs), got %d", len(intakes))
	}

	for i, intake := range intakes {
		if !intake.Passed {
			t.Fatalf("intake[%d] should pass: %+v", i, intake.Failures)
		}
	}

	// Verify index entries were marked accepted.
	idxData, _ := os.ReadFile(filepath.Join(root, "adrs", "index.json"))
	writtenIdx, _ := ParseADRIndexJSON(idxData)

	for _, entry := range writtenIdx.ADRs {
		if entry.Status != string(ArtifactStateAccepted) {
			t.Errorf("ADR index entry %s has status %s, expected accepted", entry.ADRID, entry.Status)
		}
	}
}

func TestApproveADRGateMissingIndex(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "adr-missing-idx")

	_, err := InitializeEffort(CreateEffortOptions{EffortRoot: root})
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	_, err = AdvanceCreatePhase(root, PhaseADRReview)
	if err != nil {
		t.Fatalf("advance: %v", err)
	}

	result, _, err := ApproveADRGate(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Status != "blocked" {
		t.Fatalf("expected blocked, got %s", result.Status)
	}
}

func TestApproveADRGatePartialValidationNoWrite(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "adr-partial")

	_, err := InitializeEffort(CreateEffortOptions{EffortRoot: root})
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	// One valid ADR, one with missing sections.
	idx := ADRIndex{
		SchemaVersion: "idiom.adr_index.v1", EffortID: "adr-partial",
		ADRs: []ADRIndexEntry{
			{ADRID: "ADR-001", Title: "Good", Status: string(ArtifactStateDraft),
				Path: "ADR-001.md", CoverageTier: "unit", PRDRefs: []string{"REQ-001"},
				CreatedAt: "2026-06-17T00:00:00Z"},
			{ADRID: "ADR-002", Title: "Bad", Status: string(ArtifactStateDraft),
				Path: "ADR-002.md", CoverageTier: "unit", PRDRefs: []string{"REQ-001"},
				CreatedAt: "2026-06-17T00:00:00Z"},
		},
	}
	if err := WriteADRIndexJSON(root, idx); err != nil {
		t.Fatalf("write index: %v", err)
	}

	if err := WriteADRMarkdown(root, "ADR-001.md", validADRDoc()); err != nil {
		t.Fatalf("write ADR-001: %v", err)
	}

	// ADR-002: missing all sections.
	badDoc := validADRDoc()

	badDoc.FrontMatter.ADRID = "ADR-002"
	badDoc.FrontMatter.EvidenceRefs = nil

	if err := WriteADRMarkdown(root, "ADR-002.md", badDoc); err != nil {
		t.Fatalf("write ADR-002: %v", err)
	}

	// Advance to ADR review.
	if err := WritePRDMarkdown(root, validPRDDoc()); err != nil {
		t.Fatalf("write PRD: %v", err)
	}

	_, err = AdvanceCreatePhase(root, PhasePRDReview)
	if err != nil {
		t.Fatalf("advance to prd_review: %v", err)
	}

	_, _, _ = ApprovePRDGate(root)

	_, err = AdvanceCreatePhase(root, PhaseADRReview)
	if err != nil {
		t.Fatalf("advance to adr_review: %v", err)
	}

	result, intakes, err := ApproveADRGate(root)
	if err != nil {
		t.Fatalf("ApproveADRGate: %v", err)
	}

	if result.Status != "blocked" {
		t.Fatalf("expected blocked due to bad ADR, got %s", result.Status)
	}

	_ = intakes

	// ADR-001 must NOT have been rewritten as accepted (partial-commit guard).
	adr1Data, _ := os.ReadFile(filepath.Join(root, "adrs", "ADR-001.md"))
	adr1Parsed, _ := ParseADRMarkdown(adr1Data)

	if adr1Parsed.FrontMatter.Status == string(ArtifactStateAccepted) {
		t.Fatal("ADR-001 was prematurely marked accepted despite ADR-002 failure")
	}
}

// --- CompleteEffort tests ---

func TestCompleteEffortSuccess(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "complete")

	_, err := InitializeEffort(CreateEffortOptions{EffortRoot: root})
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	for _, p := range []string{PhasePRDReview, PhaseADRDrafting, PhaseADRReview, PhaseTDDSynthesis} {
		_, err = AdvanceCreatePhase(root, p)
		if err != nil {
			t.Fatalf("advance to %s: %v", p, err)
		}
	}

	result, err := CompleteEffort(root)
	if err != nil {
		t.Fatalf("CompleteEffort: %v", err)
	}

	if result.CurrentPhase != PhaseComplete {
		t.Fatalf("expected complete, got %s", result.CurrentPhase)
	}
}

func TestCompleteEffortWrongPhase(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "complete-wrong")

	_, err := InitializeEffort(CreateEffortOptions{EffortRoot: root})
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	result, err := CompleteEffort(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Status != "blocked" {
		t.Fatalf("expected blocked, got %s", result.Status)
	}
}

// --- Render round-trip tests ---

func TestRenderPRDMarkdownRoundTrip(t *testing.T) {
	doc := validPRDDoc()
	markdown := RenderPRDMarkdown(doc)

	parsed, err := ParsePRDMarkdown([]byte(markdown))
	if err != nil {
		t.Fatalf("parse round-trip: %v", err)
	}

	if parsed.FrontMatter.PRDID != doc.FrontMatter.PRDID {
		t.Fatalf("PRDID mismatch: %s vs %s", parsed.FrontMatter.PRDID, doc.FrontMatter.PRDID)
	}

	if len(parsed.Requirements) != 1 {
		t.Fatalf("expected 1 requirement, got %d", len(parsed.Requirements))
	}
}

func TestRenderADRMarkdownHasAllSections(t *testing.T) {
	doc := validADRDoc()
	markdown := RenderADRMarkdown(doc)

	for _, section := range []string{"## Context", "## Decision", "## Alternatives Considered",
		"## Evidence", "## Consequences", "## Compatibility and Migration",
		"## TDD Obligations", "## Rollback", "## Open Questions"} {
		if !strContains(markdown, section) {
			t.Errorf("missing section: %s", section)
		}
	}
}

func TestRenderADRMarkdownValidFrontMatter(t *testing.T) {
	doc := validADRDoc()
	markdown := RenderADRMarkdown(doc)

	parsed, err := ParseADRMarkdown([]byte(markdown))
	if err != nil {
		t.Fatalf("parse ADR: %v", err)
	}

	if parsed.FrontMatter.ADRID != "ADR-001" {
		t.Fatalf("ADRID mismatch: %s", parsed.FrontMatter.ADRID)
	}
}

func TestWriteADRMarkdownPreservesParsedBodyContent(t *testing.T) {
	root := t.TempDir()
	raw := `---
schema_version: idiom.adr.v1
adr_id: ADR-001
status: draft
prd_refs:
  - REQ-001
evidence_refs:
  - inference
tdd_obligations:
  - TDD-ADR-001
coverage_tier: unit
---

## Context
The development workflow currently lacks mandatory history evidence.

## Decision
Require git history lookup evidence before modifying any identified file.

## Alternatives Considered
Prompt-only guidance was rejected because it is not auditable.

## Evidence
REQ-001 requires mandatory lookup before implementation.

## Consequences
Write gates can block missing history evidence.

## Compatibility and Migration
Existing lanes can adopt the same evidence contract incrementally.

## TDD Obligations
Add a regression for history evidence before write dispatch.

## Rollback
Disable the gate and retain advisory prompt guidance.

## Open Questions
- Which low-risk generated files are exempt?
`

	doc, err := ParseADRMarkdown([]byte(raw))
	if err != nil {
		t.Fatalf("ParseADRMarkdown: %v", err)
	}

	if err := WriteADRMarkdown(root, "ADR-001.md", doc); err != nil {
		t.Fatalf("WriteADRMarkdown: %v", err)
	}

	written, err := os.ReadFile(filepath.Join(root, "adrs", "ADR-001.md"))
	if err != nil {
		t.Fatalf("read written ADR: %v", err)
	}

	text := string(written)
	if !strContains(text, "Require git history lookup evidence before modifying any identified file.") {
		t.Fatalf("expected decision body to be preserved, got:\n%s", text)
	}

	if strContains(text, "## Decision\n\n\n\n") {
		t.Fatalf("ADR body was replaced with empty template:\n%s", text)
	}
}

func strContains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}

	return false
}
