package productworkflow

import (
	"strings"
	"testing"
)

func TestLooksLikeWorkspaceFileNormalizesBackslashesAndRejectsTraversal(t *testing.T) {
	if !looksLikeWorkspaceFile(`internal\productworkflow\status.go`) {
		t.Fatal("expected backslash workspace path to be recognized")
	}
	if looksLikeWorkspaceFile(`..\secret.go`) || looksLikeWorkspaceFile(`internal\..\secret.go`) {
		t.Fatal("expected backslash traversal paths to be rejected")
	}
}

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

func TestBriefGenerationUsesPathAliasesForMovedImplementationFiles(t *testing.T) {
	out := GenerateImplementationBrief(BriefGenerationInput{
		WorkflowState: WorkflowState{
			TaskID: "impl-1",
			Context: WorkflowContext{PathAliases: map[string]string{
				"cmd/idiom/product_workflow.go": "internal/productcmd/product_workflow.go",
			}},
		},
		PRD: PRDDocument{
			FrontMatter: PRDFrontMatter{Status: "accepted"},
			Requirements: []PRDRequirement{
				{ID: "REQ-1", Statement: "Move product command behavior out of cmd"},
			},
		},
		ADRs: []ParsedADR{
			{Doc: ADRDocument{FrontMatter: ADRFrontMatter{ADRID: "ADR-1", Status: "accepted"}}},
		},
		TDD: TDDLedger{TaskID: "impl-1", Obligations: []TDDObligation{{ID: "TDD-1", Command: "go test ./internal/productcmd"}}},
		ImplementationRecords: []ImplementationLedgerRecord{
			{RecordType: "atom_planned", TaskID: "impl-1", Scope: &AtomScope{ExpectedFiles: []string{"cmd/idiom/product_workflow.go"}}},
		},
		CoverageEvaluation: CoverageGateEvaluation{Status: "pass", CoverageTier: "unit"},
		ExistingWorkspacePath: map[string]bool{
			"internal/productcmd/product_workflow.go": true,
		},
	})
	if out.Status != "pass" {
		t.Fatalf("expected path alias to avoid new-file blocker, got %q blockers=%v", out.Status, out.Blockers)
	}
	if len(out.FilesToModify) != 1 || out.FilesToModify[0] != "internal/productcmd/product_workflow.go" {
		t.Fatalf("expected aliased file to modify, got %#v", out.FilesToModify)
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

func TestBriefGenerationAllowsPendingTDDEvidenceImplementationDispatch(t *testing.T) {
	out := GenerateImplementationBrief(BriefGenerationInput{
		WorkflowState: WorkflowState{TaskID: "impl-1"},
		PRD: PRDDocument{
			FrontMatter: PRDFrontMatter{Status: "accepted"},
			Requirements: []PRDRequirement{
				{ID: "REQ-1", Statement: "Do the thing"},
			},
		},
		ADRs: []ParsedADR{{Doc: ADRDocument{FrontMatter: ADRFrontMatter{ADRID: "ADR-1", Status: "accepted"}}}},
		TDD:  TDDLedger{TaskID: "impl-1", Obligations: []TDDObligation{{ID: "TDD-1", Owner: "thing_test.go", Command: "go test ./internal/thing"}}},
		ImplementationRecords: []ImplementationLedgerRecord{
			{RecordType: "atom_planned", TaskID: "impl-1", Scope: &AtomScope{ExpectedFiles: []string{"internal/thing/thing.go", "internal/thing/thing_test.go"}}},
		},
		CoverageEvaluation: CoverageGateEvaluation{
			Status:       "blocked",
			CoverageTier: "unit",
			Blockers: []CoverageGateBlocker{{
				Kind:      "coverage_tdd_evidence_missing",
				Criterion: "tdd_evidence",
				Message:   "expected all 1 TDD obligations to have verification evidence",
			}},
		},
		ExistingWorkspacePath: map[string]bool{
			"internal/thing/thing.go":      true,
			"internal/thing/thing_test.go": true,
		},
	})

	if out.Status != "pass" {
		t.Fatalf("expected pending TDD evidence to allow implementation dispatch, got %q blockers=%v", out.Status, out.Blockers)
	}
	if strings.Contains(strings.Join(out.Blockers, "\n"), "coverage gate is blocked") {
		t.Fatalf("pending TDD evidence should not emit generic coverage blocker: %#v", out.Blockers)
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

func TestBriefGenerationBlocksUngroundedImplementationAffectingClaims(t *testing.T) {
	out := GenerateImplementationBrief(BriefGenerationInput{
		WorkflowState: WorkflowState{TaskID: "impl-1"},
		PRD: PRDDocument{
			FrontMatter: PRDFrontMatter{Status: "accepted"},
			Requirements: []PRDRequirement{
				{ID: "REQ-1", Statement: "Add the admin workflow"},
			},
		},
		ADRs: []ParsedADR{
			{
				Doc: ADRDocument{
					FrontMatter:  ADRFrontMatter{ADRID: "ADR-1", Status: "accepted"},
					BodyMarkdown: "## Decision\n\nUse the ExampleCloud platform and ExampleSDK tooling for the implementation.\n\n## Open Questions\n\nNone.",
				},
				RelPath:     "adrs/ADR-1.md",
				RawMarkdown: "## Decision\n\nUse the ExampleCloud platform and ExampleSDK tooling for the implementation.\n\n## Open Questions\n\nNone.",
			},
		},
		TDD: TDDLedger{TaskID: "impl-1", Obligations: []TDDObligation{{ID: "TDD-1", Owner: "admin_test.go", Command: "go test ./internal/admin"}}},
		ImplementationRecords: []ImplementationLedgerRecord{
			{RecordType: "atom_planned", TaskID: "impl-1", Scope: &AtomScope{ExpectedFiles: []string{"internal/admin/admin.go"}}},
		},
		CoverageEvaluation: CoverageGateEvaluation{Status: "pass", CoverageTier: "unit"},
	})

	if out.Status != "blocked" {
		t.Fatalf("expected ungrounded implementation-affecting claim to block dispatch, got %q", out.Status)
	}
	found := false
	for _, blocker := range out.Blockers {
		if strings.Contains(blocker, "implementation-affecting claim") && strings.Contains(blocker, "ADR-1") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected implementation-affecting claim blocker, got %#v", out.Blockers)
	}
}

func TestImplementationAffectingClaimsWithOpenQuestionsBlockDispatch(t *testing.T) {
	findings := ImplementationAffectingClaimFindings(PRDDocument{
		Requirements: []PRDRequirement{{
			ID:            "REQ-OPEN",
			Statement:     "Use the ExampleCloud platform for implementation.",
			OpenQuestions: []string{"Confirm this platform is available in the TUI runtime."},
		}},
	}, []ParsedADR{{
		RelPath: "adrs/ADR-OPEN.md",
		Doc: ADRDocument{
			FrontMatter:  ADRFrontMatter{ADRID: "ADR-OPEN", Status: "accepted"},
			BodyMarkdown: "## Decision\n\nUse the ExampleSDK tooling.\n\n## Open Questions\n\nConfirm local runtime support.",
		},
	}})

	joined := strings.Join(profileFindingMessages(findings), "\n")
	for _, want := range []string{
		"PRD requirement REQ-OPEN is classified as open_question/advisory",
		"accepted ADR ADR-OPEN is classified as open_question/advisory",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected blocker %q, got %#v", want, findings)
		}
	}
}

func TestImplementationAffectingClaimsRequireResolvedEvidence(t *testing.T) {
	prd := PRDDocument{Requirements: []PRDRequirement{{
		ID:           "REQ-EVIDENCE",
		Statement:    "Use the ExampleCloud platform for implementation.",
		EvidenceRefs: []string{"EV-1", "EV-MISSING"},
	}}}
	adrs := []ParsedADR{{
		RelPath: "adrs/ADR-EVIDENCE.md",
		Doc: ADRDocument{
			FrontMatter:  ADRFrontMatter{ADRID: "ADR-EVIDENCE", Status: "accepted", EvidenceRefs: []string{"EV-1", "EV-MISSING"}},
			BodyMarkdown: "## Decision\n\nUse the ExampleSDK tooling.\n\n## Open Questions\n\nNone.",
		},
	}}

	findings := ImplementationAffectingClaimEvidenceFindings(prd, adrs, map[string]bool{"EV-1": true})
	joined := strings.Join(profileFindingMessages(findings), "\n")
	for _, want := range []string{
		`PRD requirement REQ-EVIDENCE references unresolved evidence "EV-MISSING"`,
		`accepted ADR ADR-EVIDENCE references unresolved evidence "EV-MISSING"`,
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected unresolved evidence blocker %q, got %#v", want, findings)
		}
	}
	if strings.Contains(joined, "EV-1") {
		t.Fatalf("resolved evidence should not block dispatch, got %#v", findings)
	}
}

func profileFindingMessages(findings []ProfileFinding) []string {
	out := make([]string, 0, len(findings))
	for _, finding := range findings {
		out = append(out, finding.Message)
	}
	return out
}
