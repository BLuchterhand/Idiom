package productworkflow

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildImplementationHandoffUsesGeneratedDispatchSemantics(t *testing.T) {
	handoff := BuildImplementationHandoff(implementationHandoffInput(t.TempDir()))

	if handoff.EffortID != "effort-handoff" || handoff.TaskID != "impl-handoff" {
		t.Fatalf("unexpected identity: %#v", handoff)
	}
	if !handoff.CoverageGatePassed || handoff.CoverageTier != "unit" {
		t.Fatalf("unexpected coverage fields: passed=%t tier=%q blockers=%#v", handoff.CoverageGatePassed, handoff.CoverageTier, handoff.CoverageBlockers)
	}
	if !handoffTestStringSliceContains(handoff.FilesToModify, "internal/productworkflow/implementation_handoff.go") {
		t.Fatalf("handoff files_to_modify should come from generated brief scope, got %#v", handoff.FilesToModify)
	}
	if !handoffTestStringSliceContains(handoff.Tests, "go test ./internal/productworkflow") || !handoffTestStringSliceContains(handoff.Tests, "go test ./internal/cmd/featureplanner") {
		t.Fatalf("handoff tests should include TDD and planned verification, got %#v", handoff.Tests)
	}
	if !handoffTestStringSliceContains(handoff.RequiredBehavior, "REQ-HANDOFF: Carry product workflow implementation handoff context") {
		t.Fatalf("handoff required behavior should come from generated brief, got %#v", handoff.RequiredBehavior)
	}
	if !handoffTestStringSliceContains(handoff.Decisions, "ADR-HANDOFF is accepted and must be preserved unless superseded by a new accepted ADR.") {
		t.Fatalf("handoff decisions should come from generated brief, got %#v", handoff.Decisions)
	}
	if !handoffTestStringSliceContains(handoff.Decisions, "ADR-HANDOFF decision: Keep source path authority in productworkflow.") {
		t.Fatalf("handoff decisions should preserve ADR decision body, got %#v", handoff.Decisions)
	}
	if !handoffTestStringSliceContains(handoff.Acceptance, "REQ-HANDOFF testability: go test ./internal/productworkflow") ||
		!handoffTestStringSliceContains(handoff.Acceptance, "REQ-HANDOFF metric: featureplanner context contains product artifacts") {
		t.Fatalf("handoff acceptance should preserve PRD testability and metric, got %#v", handoff.Acceptance)
	}
	if len(handoff.PRDRequirements) != 1 || handoff.PRDRequirements[0].Testability != "go test ./internal/productworkflow" {
		t.Fatalf("handoff should preserve PRD requirement facts, got %#v", handoff.PRDRequirements)
	}
	if len(handoff.TDDObligations) != 1 || handoff.TDDObligations[0].RequiredBefore != "implementation" {
		t.Fatalf("handoff should preserve TDD obligation facts, got %#v", handoff.TDDObligations)
	}
	if len(handoff.AcceptedADRIDs) != 1 || handoff.AcceptedADRIDs[0] != "ADR-HANDOFF" {
		t.Fatalf("handoff should preserve accepted ADR IDs, got %#v", handoff.AcceptedADRIDs)
	}
	if len(handoff.AcceptedADRPaths) != 1 || handoff.AcceptedADRPaths[0] != "adrs/001-handoff.md" {
		t.Fatalf("handoff should preserve accepted ADR paths, got %#v", handoff.AcceptedADRPaths)
	}
	if !handoffTestStringSliceContains(handoff.Constraints, "Do not edit files outside the Files To Modify allowlist.") {
		t.Fatalf("handoff constraints should come from generated brief, got %#v", handoff.Constraints)
	}
}

func TestBuildImplementationHandoffCarriesCoverageBlockers(t *testing.T) {
	input := implementationHandoffInput(t.TempDir())
	input.CoverageEvaluation = CoverageGateEvaluation{
		Status:       "blocked",
		CoverageTier: "unit",
		Blockers: []CoverageGateBlocker{{
			Message: "coverage evidence is missing",
		}},
	}

	handoff := BuildImplementationHandoff(input)

	if handoff.CoverageGatePassed {
		t.Fatal("blocked coverage gate must not be marked passed")
	}
	if !handoffTestStringSliceContains(handoff.CoverageBlockers, "coverage evidence is missing") {
		t.Fatalf("expected coverage blocker in handoff, got %#v", handoff.CoverageBlockers)
	}
}

func TestLoadImplementationHandoffReadsEffortArtifacts(t *testing.T) {
	root := t.TempDir()
	writeImplementationHandoffEffort(t, root)

	handoff, err := LoadImplementationHandoff(root)
	if err != nil {
		t.Fatalf("LoadImplementationHandoff: %v", err)
	}
	if handoff.EffortRoot != root || handoff.ArtifactRootPath != root {
		t.Fatalf("unexpected roots: effort=%q artifact=%q", handoff.EffortRoot, handoff.ArtifactRootPath)
	}
	if !handoff.CoverageGatePassed {
		t.Fatalf("expected coverage gate pass, blockers=%#v brief blockers=%#v", handoff.CoverageBlockers, handoff.Blockers)
	}
	if !handoffTestStringSliceContains(handoff.ContextEvidencePaths, "internal/productworkflow/brief_generation.go") {
		t.Fatalf("expected workflow referenced path, got %#v", handoff.ContextEvidencePaths)
	}
	if !handoffTestStringSliceContains(handoff.FilesToModify, "internal/productworkflow/implementation_handoff.go") {
		t.Fatalf("expected implementation ledger scope file, got %#v", handoff.FilesToModify)
	}
}

func implementationHandoffInput(root string) BriefGenerationInput {
	return BriefGenerationInput{
		EffortRoot:       root,
		ArtifactRootPath: root,
		WorkflowState: WorkflowState{
			SchemaVersion:    "idiom.product_workflow_state.v1",
			EffortID:         "effort-handoff",
			TaskID:           "impl-handoff",
			Title:            "Implementation handoff",
			StatusBucket:     "started",
			CurrentPhase:     "phase-4",
			CurrentState:     "planning",
			ArtifactPaths:    map[string]string{"events": "events.jsonl"},
			Coverage:         CoverageState{CoverageTier: "unit"},
			Privacy:          PrivacyState{RedactionStatus: "applied"},
			FolderPath:       root,
			NextValidActions: []string{"feature-planner"},
			DurabilityLevel:  "source_controlled",
			Context: WorkflowContext{
				ContextPackID:   "pack-handoff",
				FreshnessState:  "current",
				ReferencedPaths: []string{"internal/productworkflow/brief_generation.go"},
				PathAliases: map[string]string{
					"internal/productworkflow/types_reports.go": "internal/productworkflow/implementation_handoff.go",
				},
				RefreshPolicy: "on_demand",
			},
		},
		PRD: PRDDocument{
			FrontMatter: PRDFrontMatter{
				SchemaVersion:     "idiom.prd.v1",
				PRDID:             "PRD-HANDOFF",
				Status:            "accepted",
				TaskID:            "impl-handoff",
				CreatedAt:         "2026-06-03T00:00:00Z",
				AcceptedAt:        "2026-06-03T00:01:00Z",
				AcceptedBy:        "test",
				AcceptanceProfile: "human-review",
			},
			Requirements: []PRDRequirement{{
				ID:             "REQ-HANDOFF",
				Priority:       "must",
				Statement:      "Carry product workflow implementation handoff context",
				EvidenceRefs:   []string{"EV-HANDOFF"},
				TDDObligations: []string{"TDD-HANDOFF"},
				OwnerAlignment: "operator-approved",
				Testability:    "go test ./internal/productworkflow",
				Metric:         "featureplanner context contains product artifacts",
			}},
		},
		ADRIndex: ADRIndex{SchemaVersion: "idiom.adr_index.v1", EffortID: "effort-handoff", ADRs: []ADRIndexEntry{{
			ADRID:   "ADR-HANDOFF",
			Status:  "accepted",
			Path:    "001-handoff.md",
			PRDRefs: []string{"REQ-HANDOFF"},
		}}},
		ADRs: []ParsedADR{{
			Doc: ADRDocument{
				FrontMatter: ADRFrontMatter{
					SchemaVersion:     "idiom.adr.v1",
					ADRID:             "ADR-HANDOFF",
					Status:            "accepted",
					PRDRefs:           []string{"REQ-HANDOFF"},
					EvidenceRefs:      []string{"EV-HANDOFF"},
					TDDObligations:    []string{"TDD-HANDOFF"},
					CoverageTier:      "unit",
					AcceptedAt:        "2026-06-03T00:02:00Z",
					AcceptedBy:        "test",
					AcceptanceProfile: "human-review",
				},
				BodyMarkdown: strings.Join([]string{
					"## Context",
					"Product workflow needs one handoff authority.",
					"## Decision",
					"Keep source path authority in productworkflow.",
					"## Alternatives Considered",
					"Keep CLI-local mapping.",
					"## Evidence",
					"EV-HANDOFF",
					"## Consequences",
					"Featureplanner consumes an adapter.",
					"## Compatibility and Migration",
					"Legacy DTO remains.",
					"## TDD Obligations",
					"TDD-HANDOFF",
					"## Rollback",
					"Restore CLI-local mapping.",
					"## Open Questions",
					"None.",
				}, "\n\n"),
			},
			RelPath:     "adrs/001-handoff.md",
			RawMarkdown: "## Decision\n\nKeep source path authority in productworkflow.\n\n## Open Questions\n\nNone.\n",
		}},
		TDD: TDDLedger{
			SchemaVersion: "idiom.tdd_ledger.v1",
			EffortID:      "effort-handoff",
			TaskID:        "impl-handoff",
			CoverageTier:  "unit",
			Coverage:      TDDCoverage{MutationMinScore: 1},
			Obligations: []TDDObligation{{
				ID:             "TDD-HANDOFF",
				Type:           "regression",
				Behavior:       "Implementation handoff maps to planner context",
				Owner:          "internal/productworkflow/implementation_handoff_test.go",
				Command:        "go test ./internal/productworkflow",
				RequiredBefore: "implementation",
				Status:         "planned",
			}},
		},
		Traceability: TraceabilityMatrix{
			SchemaVersion: "idiom.traceability_matrix.v1",
			EffortID:      "effort-handoff",
			TaskID:        "impl-handoff",
			CreatedAt:     "2026-06-03T00:03:00Z",
			Matrix: []TraceabilityRow{{
				EvidenceCard:   "EV-HANDOFF",
				PRDRequirement: "REQ-HANDOFF",
				ADR:            "ADR-HANDOFF",
				TDDObligation:  "TDD-HANDOFF",
				Brief:          "briefs/impl-handoff.md",
				Report:         "reports/impl-handoff.json",
			}},
		},
		ImplementationRecords: []ImplementationLedgerRecord{
			{
				RecordType:           "atom_planned",
				TaskID:               "impl-handoff",
				EffortID:             "effort-handoff",
				Timestamp:            "2026-06-03T00:04:00Z",
				ComplexityTier:       "C1",
				ContextPackID:        "pack-handoff",
				Scope:                &AtomScope{ExpectedFiles: []string{"internal/productworkflow/types_reports.go"}, Forbidden: []string{"internal/app/feature_planner_product.go"}},
				RequiredVerification: []string{"go test ./internal/cmd/featureplanner"},
				ReportPaths:          &ReportPaths{Implementation: "reports/impl.json", Review: "reports/review.json", Investigation: "reports/investigation.json"},
				Implementor:          &ImplementorAssignment{ID: "impl", Runtime: "codex", Model: "test", Command: "run"},
				Reviewer:             &ReviewerAssignment{ID: "rev", Runtime: "codex", Model: "test", ReasoningEffort: "medium"},
			},
			{
				RecordType:              "verification_completed",
				TaskID:                  "impl-handoff",
				EffortID:                "effort-handoff",
				Timestamp:               "2026-06-03T00:05:00Z",
				TDDObligationsSatisfied: []string{"TDD-HANDOFF"},
				VerificationStatus:      "pass",
				VerificationCommandsRun: []string{"go test ./internal/productworkflow"},
			},
		},
		CoverageEvaluation:    CoverageGateEvaluation{Status: "pass", CoverageTier: "unit"},
		ExistingWorkspacePath: map[string]bool{"internal/productworkflow/implementation_handoff.go": true},
	}
}

func writeImplementationHandoffEffort(t *testing.T, root string) {
	t.Helper()
	input := implementationHandoffInput(root)
	writeImplementationHandoffJSON(t, filepath.Join(root, "workflow_state.json"), input.WorkflowState)
	if err := os.WriteFile(filepath.Join(root, "prd.md"), []byte(RenderPRDMarkdown(input.PRD)), 0o644); err != nil {
		t.Fatalf("write prd.md: %v", err)
	}
	writeImplementationHandoffJSON(t, filepath.Join(root, "tdd.json"), input.TDD)
	if err := os.MkdirAll(filepath.Join(root, "adrs"), 0o755); err != nil {
		t.Fatalf("mkdir adrs: %v", err)
	}
	writeImplementationHandoffJSON(t, filepath.Join(root, "adrs", "index.json"), input.ADRIndex)
	if err := os.WriteFile(filepath.Join(root, "adrs", "001-handoff.md"), []byte(RenderADRMarkdown(input.ADRs[0].Doc)), 0o644); err != nil {
		t.Fatalf("write adr: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "evidence"), 0o755); err != nil {
		t.Fatalf("mkdir evidence: %v", err)
	}
	writeImplementationHandoffJSON(t, filepath.Join(root, "evidence", "traceability_matrix.json"), input.Traceability)
	var ledgerLines []byte
	for _, record := range input.ImplementationRecords {
		recordData, err := json.Marshal(record)
		if err != nil {
			t.Fatalf("marshal implementation ledger: %v", err)
		}
		ledgerLines = append(ledgerLines, recordData...)
		ledgerLines = append(ledgerLines, '\n')
	}
	if err := os.WriteFile(filepath.Join(root, "implementation_ledger.jsonl"), ledgerLines, 0o644); err != nil {
		t.Fatalf("write implementation ledger: %v", err)
	}
}

func writeImplementationHandoffJSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatalf("marshal %s: %v", path, err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func handoffTestStringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if strings.Contains(value, want) {
			return true
		}
	}
	return false
}
