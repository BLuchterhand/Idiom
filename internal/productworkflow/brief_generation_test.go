package productworkflow

import (
	"strings"
	"testing"
)

func TestBriefGenerationDeterministicAndPassesWhenInputsAccepted(t *testing.T) {
	input := BriefGenerationInput{
		WorkflowState: WorkflowState{
			CurrentPhase: "phase-4",
			CurrentState: "briefing",
			TaskID:       "impl-0028",
		},
		PRD: PRDDocument{
			FrontMatter: PRDFrontMatter{PRDID: "prd-1", Status: "accepted"},
			Requirements: []PRDRequirement{
				{ID: "REQ-1", Priority: "must", Statement: "Do the thing"},
			},
		},
		ADRs: []ParsedADR{
			{
				Doc:         ADRDocument{FrontMatter: ADRFrontMatter{ADRID: "ADR-1", Status: "accepted"}},
				RelPath:     "adrs/ADR-1.md",
				RawMarkdown: "## Decision\n\na.go\n\n```js\nexport function greeting(name) {\n  return `Hello, ${name}!`;\n}\n```\n",
			},
		},
		TDD: TDDLedger{
			TaskID: "impl-0028",
			Obligations: []TDDObligation{
				{ID: "TDD-1", Owner: "greeting.test.js", Command: "npm test"},
			},
		},
		ImplementationRecords: []ImplementationLedgerRecord{
			{RecordType: "atom_planned", TaskID: "impl-0028", Scope: &AtomScope{ExpectedFiles: []string{"b.go", "a.go"}, AllowedFiles: []string{"a.go"}}},
		},
		CoverageEvaluation: CoverageGateEvaluation{Status: "pass", CoverageTier: "behavior_standard"},
	}
	a := GenerateImplementationBrief(input)
	b := GenerateImplementationBrief(input)
	if a.Status != "pass" {
		t.Fatalf("expected pass status, got %q blockers=%v", a.Status, a.Blockers)
	}
	if a.RenderedBrief != b.RenderedBrief {
		t.Fatal("brief generation must be deterministic")
	}
	if strings.Index(a.RenderedBrief, "`a.go`") > strings.Index(a.RenderedBrief, "`b.go`") {
		t.Fatalf("files should be sorted in rendered brief:\n%s", a.RenderedBrief)
	}
	for _, unexpected := range []string{
		"Do not dispatch work",
		"- npm test\n",
	} {
		if strings.Contains(a.RenderedBrief, unexpected) {
			t.Fatalf("brief contains unexpected text %q:\n%s", unexpected, a.RenderedBrief)
		}
	}
	for _, expected := range []string{
		"`greeting.test.js`",
		"## Implementation Shape",
		"`a.go`",
		"```text",
		"export function greeting(name)",
		"`package.json`",
		"`npm test` passes.",
	} {
		if !strings.Contains(a.RenderedBrief, expected) {
			t.Fatalf("brief missing expected text %q:\n%s", expected, a.RenderedBrief)
		}
	}
	for _, section := range []string{
		"## Files To Modify",
		"## Files to Read First",
		"## Verified Current State",
		"## Required Behavior",
		"## Constraints",
		"## Tests",
		"## Acceptance",
	} {
		if !strings.Contains(a.RenderedBrief, section) {
			t.Fatalf("expected section %q in rendered brief", section)
		}
	}
}

func TestBriefGenerationBlocksNewFileDispatchWithUnlabeledADRCodeBlock(t *testing.T) {
	out := GenerateImplementationBrief(BriefGenerationInput{
		WorkflowState: WorkflowState{TaskID: "impl-1"},
		PRD: PRDDocument{
			FrontMatter: PRDFrontMatter{Status: "accepted"},
			Requirements: []PRDRequirement{
				{ID: "REQ-1", Statement: "Create src/app.js"},
			},
		},
		ADRs: []ParsedADR{
			{
				Doc:         ADRDocument{FrontMatter: ADRFrontMatter{ADRID: "ADR-1", Status: "accepted"}},
				RelPath:     "adrs/ADR-1.md",
				RawMarkdown: "## Decision\n\n```js\nexport function app() {}\n```\n",
			},
		},
		TDD: TDDLedger{TaskID: "impl-1", Obligations: []TDDObligation{{ID: "TDD-1", Owner: "app.test.js", Command: "npm test"}}},
		ImplementationRecords: []ImplementationLedgerRecord{
			{RecordType: "atom_planned", TaskID: "impl-1", Scope: &AtomScope{ExpectedFiles: []string{"src/app.js"}}},
		},
		CoverageEvaluation:    CoverageGateEvaluation{Status: "pass", CoverageTier: "unit"},
		ExistingWorkspacePath: map[string]bool{},
	})
	if out.Status != "blocked" {
		t.Fatalf("expected unlabeled implementation shape to block new-file dispatch, got %q", out.Status)
	}
	if len(out.ImplementationShape) != 0 {
		t.Fatalf("expected unlabeled code block to be ignored, got %#v", out.ImplementationShape)
	}
}

func TestBriefGenerationBlocksWhenCoverageOrAcceptedInputsMissing(t *testing.T) {
	out := GenerateImplementationBrief(BriefGenerationInput{
		PRD:                PRDDocument{FrontMatter: PRDFrontMatter{Status: "draft"}},
		CoverageEvaluation: CoverageGateEvaluation{Status: "blocked", Blockers: []CoverageGateBlocker{{Kind: "k"}}},
	})
	if out.Status != "blocked" {
		t.Fatalf("expected blocked status, got %q", out.Status)
	}
	if len(out.Blockers) == 0 {
		t.Fatal("expected blockers")
	}
}

func TestBriefGenerationDoesNotTreatHumanOwnerAsReadPath(t *testing.T) {
	out := GenerateImplementationBrief(BriefGenerationInput{
		WorkflowState: WorkflowState{TaskID: "impl-1"},
		PRD: PRDDocument{
			FrontMatter: PRDFrontMatter{Status: "accepted"},
			Requirements: []PRDRequirement{
				{ID: "REQ-1", Statement: "Do the thing"},
			},
		},
		ADRs: []ParsedADR{
			{Doc: ADRDocument{FrontMatter: ADRFrontMatter{ADRID: "ADR-1", Status: "accepted"}}},
		},
		TDD: TDDLedger{
			TaskID: "impl-1",
			Obligations: []TDDObligation{
				{ID: "TDD-1", Owner: "platform team", Command: "make test"},
			},
		},
		ImplementationRecords: []ImplementationLedgerRecord{
			{RecordType: "atom_planned", TaskID: "impl-1", Scope: &AtomScope{ExpectedFiles: []string{"src/app.go"}}},
		},
		CoverageEvaluation: CoverageGateEvaluation{Status: "pass", CoverageTier: "unit"},
	})
	for _, path := range out.FilesToReadFirst {
		if path == "platform team" {
			t.Fatalf("human owner should not be treated as a read path: %#v", out.FilesToReadFirst)
		}
	}
	if !strings.Contains(out.RenderedBrief, "`make test` passes.") {
		t.Fatalf("make command should be rendered as a passing command:\n%s", out.RenderedBrief)
	}
}

func TestBriefGenerationBlocksNewFileDispatchWithoutADRCodeBlocks(t *testing.T) {
	out := GenerateImplementationBrief(BriefGenerationInput{
		WorkflowState: WorkflowState{TaskID: "impl-1"},
		PRD: PRDDocument{
			FrontMatter: PRDFrontMatter{Status: "accepted"},
			Requirements: []PRDRequirement{
				{ID: "REQ-1", Statement: "Create src/app.js"},
			},
		},
		ADRs: []ParsedADR{
			{Doc: ADRDocument{FrontMatter: ADRFrontMatter{ADRID: "ADR-1", Status: "accepted"}}, RelPath: "adrs/ADR-1.md"},
		},
		TDD: TDDLedger{TaskID: "impl-1", Obligations: []TDDObligation{{ID: "TDD-1", Owner: "app.test.js", Command: "npm test"}}},
		ImplementationRecords: []ImplementationLedgerRecord{
			{RecordType: "atom_planned", TaskID: "impl-1", Scope: &AtomScope{ExpectedFiles: []string{"src/app.js"}}},
		},
		CoverageEvaluation:    CoverageGateEvaluation{Status: "pass", CoverageTier: "unit"},
		ExistingWorkspacePath: map[string]bool{},
	})
	if out.Status != "blocked" {
		t.Fatalf("expected missing implementation shape to block new-file dispatch, got %q", out.Status)
	}
	found := false
	for _, blocker := range out.Blockers {
		if strings.Contains(blocker, "implementation code block") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected implementation-shape blocker, got %#v", out.Blockers)
	}
}
