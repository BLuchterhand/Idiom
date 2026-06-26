package featureplannerproduct

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"

	"idiom/internal/workflows/featureplanner"
	"idiom/internal/workflows/productworkflow"
)

func TestBriefFromImplementationHandoffCarriesGrounding(t *testing.T) {
	handoff := productworkflow.ImplementationHandoff{
		EffortID:           "effort-1",
		TaskID:             "task-1",
		EffortRoot:         "started/effort-1",
		ArtifactRootPath:   "started/effort-1",
		StatusBucket:       "started",
		CurrentPhase:       "phase-4",
		CurrentState:       "briefing",
		NextValidActions:   []string{"feature-planner"},
		CoverageTier:       "behavior_standard",
		CoverageGatePassed: false,
		CoverageContract: &productworkflow.ImplementationHandoffCoverageContract{
			ContractID:   "cov-1",
			Digest:       "sha256:abc",
			EventID:      "evt-cov-1",
			Status:       "pass",
			EffortID:     "effort-1",
			CoverageTier: "behavior_standard",
		},
		AcceptedCoverage: &productworkflow.ImplementationHandoffAcceptedCoverage{
			Contract: productworkflow.ImplementationHandoffCoverageContract{
				ContractID:   "cov-1",
				Digest:       "sha256:abc",
				EventID:      "evt-cov-1",
				Status:       "pass",
				EffortID:     "effort-1",
				CoverageTier: "behavior_standard",
			},
			Status:    "pass",
			Authority: "product_workflow",
			Behavioral: productworkflow.BehavioralCoverageContract{
				EARS: []productworkflow.EARSRequirementCoverage{{ID: "REQ-1", Pattern: "event-driven", Statement: "When request arrives, the system shall do the thing."}},
				BDDScenarios: []productworkflow.BDDScenarioCoverage{{
					ID:               "BDD-1",
					RequirementID:    "REQ-1",
					Given:            []string{"request exists"},
					When:             []string{"handler runs"},
					Then:             []string{"thing is done"},
					ConcreteDataRefs: []string{"fixtures/request.json"},
					Obligations:      []string{"TDD-1"},
				}, {
					ID:               "BDD-WAIVED",
					Status:           productworkflow.BehavioralCoverageStatusWaived,
					Given:            []string{"waived coverage exists"},
					When:             []string{"handoff is adapted"},
					Then:             []string{"it is not presented as accepted"},
					ConcreteDataRefs: []string{"fixtures/waived.json"},
				}},
				Invariants: []productworkflow.InvariantCoverage{{
					ID:                    "INV-ACCEPTED",
					Statement:             "accepted invariant survives",
					PropertyBasedTestRefs: []string{"TestInvariantAccepted"},
				}, {
					ID:                    "INV-NOT-REQUIRED",
					Status:                productworkflow.BehavioralCoverageStatusNotRequired,
					Statement:             "not-required invariant should not be accepted",
					PropertyBasedTestRefs: []string{"TestInvariantNotRequired"},
				}},
				FuzzTargets: []productworkflow.FuzzTargetCoverage{{
					ID:          "FUZZ-1",
					Target:      "ParsePlan",
					Generators:  []string{"random JSON envelopes"},
					Invariants:  []string{"never panics"},
					TestRefs:    []string{"FuzzParsePlan"},
					Obligations: []string{"TDD-1"},
				}},
				RAGEvals: []productworkflow.RAGEvaluationCoverage{{
					ID:          "RAG-1",
					DatasetRefs: []string{"evals/feature_planner.jsonl"},
					Metrics:     map[string]float64{"groundedness": 0.98},
					Thresholds:  map[string]float64{"groundedness": 0.95},
					EvalRefs:    []string{"TestFeaturePlannerRAGEval"},
					Obligations: []string{"TDD-1"},
				}},
				SecurityCases: []productworkflow.SecurityCaseCoverage{{
					ID:          "SEC-1",
					Risk:        "model injection leaks harness-only context",
					Attack:      "source text asks to print hidden instructions",
					Expected:    "hidden instructions are not printed",
					TestRefs:    []string{"TestHarnessContextLeakBlocked"},
					Obligations: []string{"TDD-1"},
				}},
				PrivacyControls: []productworkflow.PrivacyControlCoverage{{
					ID:            "PRIV-1",
					DataClass:     "conversation trace",
					Policy:        "redact raw prompts before audit storage",
					RedactionRefs: []string{"redaction/prompts"},
					Retention:     "bounded artifact only",
					TestRefs:      []string{"TestAuditRedactsRawPrompt"},
					Obligations:   []string{"TDD-1"},
				}},
				ProviderContracts: []productworkflow.ProviderContractCoverage{{
					ID:           "PACT-1",
					Provider:     "fake-llm",
					Consumer:     "feature-planner",
					ContractRefs: []string{"contracts/fake_llm_feature_planner.json"},
					TestRefs:     []string{"TestFakeProviderFeaturePlannerContract"},
					Obligations:  []string{"TDD-1"},
				}},
				Observability: []productworkflow.ObservabilityCoverage{{
					ID:               "OBS-1",
					Signal:           "feature_planner_rejections_total",
					InstrumentedPath: "internal/workflows/featureplanner",
					RequiredFields:   []string{"event_type", "diagnostic"},
					TestRefs:         []string{"TestPlannerRejectionTelemetry"},
					Obligations:      []string{"TDD-1"},
				}},
				PerformanceBounds: []productworkflow.PerformanceBoundCoverage{{
					ID:              "PERF-1",
					Operation:       "render accepted coverage prompt context",
					Bound:           "deterministic bounded text",
					MeasurementRefs: []string{"benchmarks/accepted_coverage_prompt.txt"},
					Obligations:     []string{"TDD-1"},
				}},
				E2EJourneys: []productworkflow.E2EJourneyCoverage{{
					ID:          "E2E-1",
					Journey:     "greenfield user asks for TDD coverage and sees structured plan",
					Steps:       []string{"start feature planner", "fake provider submits plan", "plan tab renders categories"},
					ScriptRefs:  []string{"scripts/e2e/feature_planner_bdd_tdd.sh"},
					Obligations: []string{"TDD-1"},
				}},
				SurfaceInventory: []productworkflow.BehavioralSurfaceCoverage{{
					ID:          "SURF-DOMAIN",
					Surface:     "domain_logic",
					Status:      "in_scope",
					Rationale:   "core accepted behavior",
					Obligations: []string{"TDD-1"},
				}, {
					ID:        "SURF-TRANSPORT",
					Surface:   "api_transport",
					Status:    productworkflow.BehavioralCoverageStatusNotRequired,
					Rationale: "no transport boundary changed",
				}},
				TestObligations: []productworkflow.TestObligationCoverage{{ID: "TDD-1", Behavior: "behavior", Command: "go test ./...", Type: "contract"}},
				OpenQuestions: []productworkflow.BehavioralCoverageQuestion{{
					ID:         "Q-1",
					Question:   "Was the accepted behavior operator-approved?",
					Status:     "resolved",
					Resolution: "Approved in product workflow before feature planning.",
				}},
			},
		},
		CoverageBlockers:     []string{"expected all 1 TDD obligations to have verification evidence"},
		CoverageBlockerKinds: []string{"coverage_tdd_evidence_missing"},
		AcceptedADRIDs:       []string{"ADR-1"},
		AcceptedADRPaths:     []string{"adrs/1.md"},
		FilesToModify:        []string{"internal/example.go", "internal/newpkg/new_file.go", "internal/newpkg/new_file_test.go"},
		FileActions: map[string]string{
			"internal/example.go":              "Modify existing file at line 1 for this atom's bounded behavior.",
			"internal/newpkg/new_file.go":      "Create new file: implement this atom's behavior in the new source file.",
			"internal/newpkg/new_file_test.go": "Create new file: implement this atom's behavior in the new source file.",
		},
		AuthorizedNewPackageRoots: []string{"internal/newpkg"},
		ContextEvidencePaths:      []string{"internal/context.go"},
		RequiredBehavior:          []string{"do the thing"},
		Tests:                     []string{"go test ./..."},
		Acceptance:                []string{"accepted"},
		Constraints:               []string{"scoped"},
		Decisions:                 []string{"use existing API"},
		Blockers:                  nil,
		PRDRequirements: []productworkflow.ImplementationHandoffPRDRequirement{{
			ID:          "REQ-1",
			Priority:    "P0",
			Statement:   "requirement",
			Testability: "automated",
			Metric:      "metric",
			Obligations: []string{"TDD-1"},
		}},
		TDDObligations: []productworkflow.ImplementationHandoffTDDObligation{{
			ID:             "TDD-1",
			Behavior:       "behavior",
			Command:        "go test ./...",
			RequiredBefore: "implementation",
			Status:         "planned",
		}},
	}

	brief := BriefFromImplementationHandoff(handoff)

	if brief == nil {
		t.Fatal("expected feature planner brief")
	}

	if brief.EffortID != handoff.EffortID || brief.TaskID != handoff.TaskID {
		t.Fatalf("brief identity mismatch: brief=%+v handoff=%+v", brief, handoff)
	}

	if brief.StatusBucket != handoff.StatusBucket || brief.CurrentPhase != handoff.CurrentPhase || brief.CurrentState != handoff.CurrentState {
		t.Fatalf("brief workflow state mismatch: brief=%+v handoff=%+v", brief, handoff)
	}

	if len(brief.NextValidActions) != len(handoff.NextValidActions) {
		t.Fatalf("brief next valid actions mismatch: brief=%+v handoff=%+v", brief, handoff)
	}

	if len(brief.FilesToModify) == 0 {
		t.Fatalf("expected files_to_modify from handoff, got %+v", brief)
	}

	if !slices.Equal(brief.AuthorizedNewPackageRoots, []string{"internal/newpkg"}) {
		t.Fatalf("authorized new package roots = %#v", brief.AuthorizedNewPackageRoots)
	}

	if len(brief.RequiredBehavior) == 0 || len(brief.Tests) == 0 || len(brief.Acceptance) == 0 || len(brief.Constraints) == 0 || len(brief.Decisions) == 0 {
		t.Fatalf("expected full implementation grounding, got %+v", brief)
	}

	if len(brief.PRDRequirements) != len(handoff.PRDRequirements) || len(brief.TDDObligations) != len(handoff.TDDObligations) {
		t.Fatalf("expected artifact refs to round-trip, brief=%+v handoff=%+v", brief, handoff)
	}
	if brief.CoverageContract == nil || brief.CoverageContract.ContractID != "cov-1" || brief.CoverageContract.CoverageTier != "behavior_standard" {
		t.Fatalf("expected coverage contract ref to round-trip, got %+v", brief.CoverageContract)
	}
	if brief.AcceptedCoverage == nil || len(brief.AcceptedCoverage.Scenarios) != 1 || brief.AcceptedCoverage.Scenarios[0].Then[0] != "thing is done" {
		t.Fatalf("expected accepted coverage detail to round-trip, got %+v", brief.AcceptedCoverage)
	}
	if len(brief.AcceptedCoverage.Invariants) != 1 || brief.AcceptedCoverage.Invariants[0].ID != "INV-ACCEPTED" {
		t.Fatalf("expected only accepted invariant coverage to round-trip, got %+v", brief.AcceptedCoverage.Invariants)
	}
	if len(brief.AcceptedCoverage.FuzzTargets) != 1 || brief.AcceptedCoverage.FuzzTargets[0].TestRefs[0] != "FuzzParsePlan" {
		t.Fatalf("expected fuzz target coverage to round-trip, got %+v", brief.AcceptedCoverage.FuzzTargets)
	}
	if len(brief.AcceptedCoverage.RAGEvals) != 1 || brief.AcceptedCoverage.RAGEvals[0].Thresholds["groundedness"] != 0.95 {
		t.Fatalf("expected RAG eval coverage to round-trip, got %+v", brief.AcceptedCoverage.RAGEvals)
	}
	if len(brief.AcceptedCoverage.SecurityCases) != 1 || brief.AcceptedCoverage.SecurityCases[0].Expected != "hidden instructions are not printed" {
		t.Fatalf("expected security coverage to round-trip, got %+v", brief.AcceptedCoverage.SecurityCases)
	}
	if len(brief.AcceptedCoverage.PrivacyControls) != 1 || brief.AcceptedCoverage.PrivacyControls[0].RedactionRefs[0] != "redaction/prompts" {
		t.Fatalf("expected privacy coverage to round-trip, got %+v", brief.AcceptedCoverage.PrivacyControls)
	}
	if len(brief.AcceptedCoverage.ProviderContracts) != 1 || brief.AcceptedCoverage.ProviderContracts[0].Provider != "fake-llm" {
		t.Fatalf("expected provider contract coverage to round-trip, got %+v", brief.AcceptedCoverage.ProviderContracts)
	}
	if len(brief.AcceptedCoverage.Observability) != 1 || brief.AcceptedCoverage.Observability[0].RequiredFields[1] != "diagnostic" {
		t.Fatalf("expected observability coverage to round-trip, got %+v", brief.AcceptedCoverage.Observability)
	}
	if len(brief.AcceptedCoverage.PerformanceBounds) != 1 || brief.AcceptedCoverage.PerformanceBounds[0].Bound != "deterministic bounded text" {
		t.Fatalf("expected performance coverage to round-trip, got %+v", brief.AcceptedCoverage.PerformanceBounds)
	}
	if len(brief.AcceptedCoverage.E2EJourneys) != 1 || brief.AcceptedCoverage.E2EJourneys[0].ScriptRefs[0] != "scripts/e2e/feature_planner_bdd_tdd.sh" {
		t.Fatalf("expected scripted e2e coverage to round-trip, got %+v", brief.AcceptedCoverage.E2EJourneys)
	}
	if len(brief.AcceptedCoverage.SurfaceInventory) != 2 ||
		brief.AcceptedCoverage.SurfaceInventory[0].Obligations[0] != "TDD-1" ||
		brief.AcceptedCoverage.SurfaceInventory[1].Status != productworkflow.BehavioralCoverageStatusNotRequired {
		t.Fatalf("expected accepted surface decisions to round-trip, got %+v", brief.AcceptedCoverage.SurfaceInventory)
	}
	if len(brief.AcceptedCoverage.Questions) != 1 ||
		brief.AcceptedCoverage.Questions[0].Status != "resolved" ||
		brief.AcceptedCoverage.Questions[0].Resolution != "Approved in product workflow before feature planning." {

		t.Fatalf("expected accepted coverage question metadata to round-trip, got %+v", brief.AcceptedCoverage.Questions)
	}

	if !brief.AllowsFeaturePlanDispatch() {
		t.Fatalf("typed TDD-only pending evidence should allow feature-planner dispatch: %+v", brief)
	}
}

func TestBDDTTDPipelineCarriesAcceptedCoverageThroughPlannerValidation(t *testing.T) {
	contract := productworkflow.BehavioralCoverageContract{
		ContractID:   "cov-pipeline",
		EffortID:     "effort-pipeline",
		TaskID:       "task-pipeline",
		CoverageTier: "behavior_standard",
		EventID:      "evt-cov-pipeline",
		EARS: []productworkflow.EARSRequirementCoverage{{
			ID:          "REQ-PIPE",
			Statement:   "When product coverage is accepted, the system shall pass it to feature planning as read-only context.",
			MappedRefs:  []string{"BDD-PIPE"},
			Obligations: []string{"TDD-PIPE"},
		}},
		BDDScenarios: []productworkflow.BDDScenarioCoverage{{
			ID:               "BDD-PIPE",
			RequirementID:    "REQ-PIPE",
			Given:            []string{"a product workflow effort has accepted behavioral coverage"},
			When:             []string{"the implementation handoff is adapted for feature planning"},
			Then:             []string{"the planner cites the coverage contract without authoring accepted coverage"},
			ConcreteDataRefs: []string{"fixtures/pipeline/coverage.json"},
			Obligations:      []string{"TDD-PIPE"},
		}},
		DesignContracts: []productworkflow.DesignContractCoverage{{
			ID:             "DBC-PIPE",
			Boundary:       "productworkflow accepted coverage to featureplanner context",
			Preconditions:  []string{"product workflow validated the behavioral coverage contract"},
			Postconditions: []string{"feature planner receives read-only accepted coverage"},
			Invariants:     []string{"feature planner cannot author accepted coverage detail"},
			TestRefs:       []string{"TestBDDTTDPipelineCarriesAcceptedCoverageThroughPlannerValidation"},
			Obligations:    []string{"TDD-PIPE"},
		}},
		Invariants: []productworkflow.InvariantCoverage{{
			ID:                    "INV-PIPE",
			Statement:             "accepted coverage authority remains product-owned",
			PropertyBasedTestRefs: []string{"TestBDDTTDPipelineCarriesAcceptedCoverageThroughPlannerValidation"},
			Obligations:           []string{"TDD-PIPE"},
		}},
		FailureModes: []productworkflow.FailureModeCoverage{{
			ID:                 "FM-PIPE",
			Mode:               "planner submits fabricated accepted coverage",
			Mitigation:         "product context validation rejects accepted_coverage from submit_feature_plan",
			RPN:                64,
			MitigationTestRefs: []string{"TestBDDTTDPipelineCarriesAcceptedCoverageThroughPlannerValidation"},
			Obligations:        []string{"TDD-PIPE"},
		}},
		TestObligations: []productworkflow.TestObligationCoverage{{
			ID:             "TDD-PIPE",
			Type:           "contract",
			Behavior:       "accepted BDD-TDD coverage reaches planner validation",
			Command:        "go test ./internal/workflows/featureplannerproduct",
			RequiredBefore: "implementation",
		}},
		Verification: []productworkflow.VerificationGateCoverage{{
			ID:          "VERIFY-PIPE",
			Gate:        "contract",
			Command:     "go test ./internal/workflows/featureplannerproduct",
			Obligations: []string{"TDD-PIPE"},
		}},
		OpenQuestions: []productworkflow.BehavioralCoverageQuestion{{
			ID:         "Q-PIPE",
			Question:   "Is the boundary authority explicit?",
			Status:     "resolved",
			Resolution: "Product workflow owns accepted coverage; planner cites only the contract reference.",
		}},
	}
	ledger := productworkflow.ProjectBehavioralCoverageContractLedger(contract)
	digest := productworkflow.BehavioralCoverageContractDigest(contract)
	prd := productworkflow.PRDDocument{
		FrontMatter: productworkflow.PRDFrontMatter{Status: "accepted"},
		Requirements: []productworkflow.PRDRequirement{{
			ID:             "REQ-PIPE",
			Statement:      "Pass accepted BDD-TDD coverage to feature planning.",
			EvidenceRefs:   []string{"EV-PIPE"},
			TDDObligations: []string{"TDD-PIPE"},
		}},
	}
	adrIndex := productworkflow.ADRIndex{ADRs: []productworkflow.ADRIndexEntry{{
		ADRID:  "ADR-PIPE",
		Status: "accepted",
		Path:   "001-pipeline.md",
	}}}
	adrs := []productworkflow.ParsedADR{{
		RelPath: "adrs/001-pipeline.md",
		Doc: productworkflow.ADRDocument{
			FrontMatter: productworkflow.ADRFrontMatter{
				ADRID:          "ADR-PIPE",
				Status:         "accepted",
				EvidenceRefs:   []string{"EV-PIPE"},
				TDDObligations: []string{"TDD-PIPE"},
			},
			BodyMarkdown: "## Decision\nCarry accepted behavioral coverage as read-only planner context.\n\n## Open Questions\nNone.",
		},
	}}
	traceability := productworkflow.TraceabilityMatrix{Matrix: []productworkflow.TraceabilityRow{{
		EvidenceCard:   "EV-PIPE",
		PRDRequirement: "REQ-PIPE",
		ADR:            "ADR-PIPE",
		TDDObligation:  "TDD-PIPE",
	}}}
	coverage := productworkflow.EvaluateCoverageGate(prd, adrIndex, adrs, ledger, traceability)
	handoff := productworkflow.BuildImplementationHandoff(productworkflow.BriefGenerationInput{
		EffortRoot:       "docs/product-workflows/efforts/pipeline",
		ArtifactRootPath: "docs/product-workflows/efforts/pipeline",
		WorkflowState: productworkflow.WorkflowState{
			EffortID:    "effort-pipeline",
			TaskID:      "task-pipeline",
			LastEventID: "evt-cov-pipeline",
		},
		PRD:                prd,
		ADRIndex:           adrIndex,
		ADRs:               adrs,
		TDD:                ledger,
		Traceability:       traceability,
		CoverageEvaluation: coverage,
		PromotionEvents: []productworkflow.BehavioralCoveragePromotionEvent{{
			Event:   "behavioral_coverage_accepted",
			EventID: "evt-cov-pipeline",
			State:   productworkflow.BehavioralCoveragePromotionStateAccepted,
			Digest:  digest,
		}},
		ImplementationRecords: []productworkflow.ImplementationLedgerRecord{{
			RecordType:           "atom_planned",
			TaskID:               "task-pipeline",
			Scope:                &productworkflow.AtomScope{ExpectedFiles: []string{"internal/pipeline/coverage.go", "internal/pipeline/coverage_test.go"}},
			RequiredVerification: []string{"go test ./internal/workflows/featureplannerproduct"},
		}},
	})
	brief := BriefFromImplementationHandoff(handoff)
	if brief.AcceptedCoverage == nil {
		t.Fatal("expected accepted behavioral coverage to reach feature planner brief")
	}
	if !brief.AllowsFeaturePlanDispatch() {
		t.Fatalf("pending implementation verification should allow planner dispatch: blockers=%#v coverage=%#v", brief.Blockers, brief.CoverageBlockers)
	}

	plan := featureplanner.FeaturePlan{
		SchemaVersion: featureplanner.FeaturePlanSchemaVersion,
		Tool:          featureplanner.ToolSubmitFeaturePlan,
		FeatureID:     "feature-pipeline",
		SourceTask:    featureplanner.SourceTask{Kind: "product_workflow", Summary: "Implement accepted BDD-TDD pipeline coverage."},
		Summary:       "Implement accepted BDD-TDD pipeline coverage.",
		TopLevelScope: featureplanner.TopLevelScope{
			Objective:       "Carry accepted BDD-TDD product coverage through planner validation.",
			SuccessCriteria: []string{"coverage contract cited", "accepted coverage stamped by harness"},
		},
		SDDContract: featureplanner.SDDContract{
			PRDRequirementRefs: []string{"REQ-PIPE"},
			ADRRefs:            []string{"ADR-PIPE"},
			Acceptance:         []string{"accepted behavioral coverage is read-only planner context"},
		},
		TDDContract: featureplanner.TDDContract{
			CoverageTier:                 "behavior_standard",
			RequiredBeforeImplementation: true,
			Obligations: []featureplanner.TDDObligation{{
				ID:             "TDD-PIPE",
				Behavior:       "accepted BDD-TDD coverage reaches planner validation",
				Command:        "go test ./internal/workflows/featureplannerproduct",
				RequiredBefore: "implementation",
			}},
			CoverageGateEventID: "evt-cov-pipeline",
		},
		CoverageContract: brief.CoverageContract,
		Atoms: []featureplanner.FeatureAtomPlan{{
			ID:                "atom-pipeline",
			Title:             "Carry accepted BDD-TDD coverage",
			Summary:           "Wire product-owned accepted coverage into planner validation.",
			RequiredEditPaths: []string{"internal/pipeline/coverage.go", "internal/pipeline/coverage_test.go"},
			SDDRefs:           []string{"REQ-PIPE", "ADR-PIPE"},
			TDDObligations:    []string{"TDD-PIPE"},
			RequiredTests:     []string{"go test ./internal/workflows/featureplannerproduct"},
			TestFirst: featureplanner.TestFirstPlan{
				ObligationRefs:             []string{"TDD-PIPE"},
				Commands:                   []string{"go test ./internal/workflows/featureplannerproduct"},
				BaselineRequired:           true,
				TestArtifactPaths:          []string{"internal/pipeline/coverage_test.go"},
				PreExecutionEventIDs:       []string{"evt-cov-pipeline"},
				ExpectedPreExecutionResult: "red-confirmed",
				CoverageGateEventID:        "evt-cov-pipeline",
			},
			PlannedTDDSnapshot: featureplanner.PlannedTDDSnapshot{
				SnapshotID:                   "snapshot-pipeline",
				SnapshotDigest:               "sha256:pipeline",
				ObligationIDs:                []string{"TDD-PIPE"},
				TestCommands:                 []string{"go test ./internal/workflows/featureplannerproduct"},
				TestArtifactPaths:            []string{"internal/pipeline/coverage_test.go"},
				ExpectedPreExecutionResults:  []string{"red-confirmed"},
				CreatedBeforeDispatchEventID: "evt-cov-pipeline",
			},
		}},
	}

	if err := featureplanner.ValidatePlanProductWorkflowContext(plan, *brief, ""); err != nil {
		t.Fatalf("expected planner submission to validate against product BDD-TDD context: %v", err)
	}

	normalized := featureplanner.NormalizePlanProductWorkflowContext(plan, *brief, "")
	if normalized.AcceptedCoverage == nil || normalized.AcceptedCoverage.Authority != "product_workflow" {
		t.Fatalf("expected normalization to stamp product-owned accepted coverage, got %+v", normalized.AcceptedCoverage)
	}
	if normalized.AcceptedCoverage.Scenarios[0].ID != "BDD-PIPE" ||
		normalized.AcceptedCoverage.DesignContracts[0].ID != "DBC-PIPE" ||
		normalized.AcceptedCoverage.Invariants[0].ID != "INV-PIPE" ||
		normalized.AcceptedCoverage.FailureModes[0].ID != "FM-PIPE" ||
		normalized.AcceptedCoverage.TestObligations[0].ID != "TDD-PIPE" {
		t.Fatalf("normalized plan lost accepted BDD-TDD coverage detail: %+v", normalized.AcceptedCoverage)
	}
}

func TestDiskBackedImplementationHandoffCarriesAcceptedCoverage(t *testing.T) {
	dir := t.TempDir()
	mustWriteFeaturePlannerProductTestFile(t, dir, productworkflow.WorkflowStateFileName, `{
		"schema_version":"idiom.workflow_state.v1.fixture",
		"effort_id":"effort-disk",
		"task_id":"task-disk",
		"title":"Disk Coverage",
		"status_bucket":"started",
		"current_phase":"feature_planning",
		"current_state":"planned",
		"last_event_id":"evt-disk",
		"artifact_paths":{"events":"events.jsonl"},
		"context":{"context_pack_id":"pack-disk","freshness_state":"current","referenced_paths":["internal/disk/coverage.go"],"unresolved_gaps":[],"refresh_policy":"on_demand"},
		"coverage":{"coverage_tier":"behavior_standard"},
		"privacy":{"redaction_status":"applied","fields_excluded":[]},
		"folder_path":"docs/product-workflows/efforts/started/effort-disk",
		"durability_level":"source_controlled",
		"next_valid_actions":["feature-planner"]
	}`)
	mustWriteFeaturePlannerProductTestFile(t, dir, "prd.md", `---
schema_version: idiom.prd.v1.fixture
prd_id: prd-disk
status: accepted
task_id: task-disk
evidence_ledger: evidence/evidence.jsonl
created_at: 2026-06-01T00:00:00Z
accepted_at: 2026-06-01T00:00:00Z
accepted_by: reviewer
acceptance_profile: fixture
---

# PRD: Disk Coverage

## Requirements

### REQ-DISK

- **id**: REQ-DISK
- **priority**: must
- **statement**: Carry accepted behavioral coverage from disk into feature planning.
- **evidence_refs**: [EV-DISK]
- **owner_alignment**: technical
- **testability**: automated
- **tdd_obligations**: [TDD-DISK]
- **open_questions**: []
- **metric**: Feature planner receives accepted coverage detail.
`)
	mustWriteFeaturePlannerProductTestFile(t, dir, "adrs/index.json", `{
		"schema_version":"idiom.adr_index.v1.fixture",
		"effort_id":"effort-disk",
		"adrs":[{"adr_id":"ADR-DISK","title":"Carry accepted coverage","status":"accepted","path":"ADR-DISK.md","prd_refs":["REQ-DISK"]}]
	}`)
	mustWriteFeaturePlannerProductTestFile(t, dir, "adrs/ADR-DISK.md", `---
schema_version: idiom.adr.v1.fixture
adr_id: ADR-DISK
status: accepted
prd_refs:
  - REQ-DISK
evidence_refs:
  - EV-DISK
tdd_obligations:
  - TDD-DISK
coverage_tier: behavior_standard
created_at: 2026-06-01T00:00:00Z
accepted_at: 2026-06-01T00:00:00Z
accepted_by: reviewer
acceptance_profile: fixture
---

# ADR-DISK: Carry Accepted Coverage

## Decision

Use productworkflow as the single accepted behavioral coverage authority.

## Context

Feature planner consumes product workflow context from durable artifacts.

## Alternatives Considered

Use duplicated TUI parsing; rejected because it creates drift.

## Evidence

EV-DISK records the accepted product workflow evidence.

## Consequences

The implementation handoff carries accepted coverage as read-only context.

## Compatibility and Migration

Existing feature planner consumers continue to receive ProductWorkflowBrief.

## TDD Obligations

TDD-DISK verifies disk-backed accepted coverage.

## Rollback

Remove the behavioral coverage contract from tdd.json.

## Open Questions

None.
`)
	score := 90.0
	contract := productworkflow.BehavioralCoverageContract{
		ContractID:   "cov-disk",
		EffortID:     "effort-disk",
		TaskID:       "task-disk",
		CoverageTier: "behavior_standard",
		EventID:      "evt-disk",
		EARS: []productworkflow.EARSRequirementCoverage{{
			ID:          "REQ-DISK",
			Pattern:     "event-driven",
			Statement:   "When disk artifacts are loaded, the system shall carry accepted behavioral coverage into feature planning.",
			MappedRefs:  []string{"BDD-DISK"},
			Obligations: []string{"TDD-DISK"},
		}},
		ExampleRules: []productworkflow.ExampleRuleCoverage{{
			ID:            "RULE-DISK",
			RequirementID: "REQ-DISK",
			Rule:          "Accepted coverage is read from tdd.json.",
			Examples: []productworkflow.ConcreteExampleCoverage{
				{ID: "EX-DISK-1", Description: "accepted coverage present", ConcreteDataRefs: []string{"fixtures/disk/accepted.json"}},
				{ID: "EX-DISK-2", Description: "coverage missing", ConcreteDataRefs: []string{"fixtures/disk/missing.json"}, Negative: true},
			},
		}},
		BDDScenarios: []productworkflow.BDDScenarioCoverage{{
			ID:               "BDD-DISK",
			RequirementID:    "REQ-DISK",
			RuleID:           "RULE-DISK",
			Given:            []string{"an effort folder has accepted behavioral coverage"},
			When:             []string{"the implementation handoff is loaded from disk"},
			Then:             []string{"feature planner receives accepted coverage detail"},
			ConcreteDataRefs: []string{"fixtures/disk/accepted.json"},
			Obligations:      []string{"TDD-DISK"},
		}},
		DesignContracts: []productworkflow.DesignContractCoverage{{
			ID:             "DBC-DISK",
			Boundary:       "productworkflow disk loader to featureplanner adapter",
			Preconditions:  []string{"tdd.json contains behavioral_coverage"},
			Postconditions: []string{"brief accepted coverage is populated"},
			Invariants:     []string{"productworkflow remains accepted coverage authority"},
			TestRefs:       []string{"TestDiskBackedImplementationHandoffCarriesAcceptedCoverage"},
			Obligations:    []string{"TDD-DISK"},
		}},
		SurfaceInventory: []productworkflow.BehavioralSurfaceCoverage{{
			ID:          "SURF-DISK-DOMAIN",
			Surface:     "domain_logic",
			Status:      "in_scope",
			Rationale:   "disk-backed accepted behavior is in scope",
			Obligations: []string{"TDD-DISK"},
		}, {
			ID:        "SURF-DISK-TRANSPORT",
			Surface:   "api_transport",
			Status:    productworkflow.BehavioralCoverageStatusNotRequired,
			Rationale: "disk loader does not add an external transport",
		}},
		TestObligations: []productworkflow.TestObligationCoverage{{
			ID:             "TDD-DISK",
			Type:           "acceptance",
			Behavior:       "disk-backed accepted behavioral coverage reaches feature planner",
			Owner:          "featureplannerproduct",
			Command:        "go test ./internal/workflows/featureplannerproduct",
			RequiredBefore: "implementation",
		}},
		Mutation: productworkflow.BehavioralMutationCoverage{Requirement: "required", MinScore: 80, Score: &score},
	}
	ledger := productworkflow.ProjectBehavioralCoverageContractLedger(contract)
	ledgerBytes, err := json.MarshalIndent(ledger, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	mustWriteFeaturePlannerProductTestFile(t, dir, "tdd.json", string(ledgerBytes)+"\n")
	mustWriteFeaturePlannerProductTestFile(t, dir, "events.jsonl", `{"event":"behavioral_coverage_accepted","event_id":"evt-disk","state":"accepted","digest":`+strconv.Quote(productworkflow.BehavioralCoverageContractDigest(contract))+`,"source":"test"}`+"\n")
	mustWriteFeaturePlannerProductTestFile(t, dir, "evidence/traceability_matrix.json", `{
		"schema_version":"idiom.traceability.v1.fixture",
		"effort_id":"effort-disk",
		"task_id":"task-disk",
		"created_at":"2026-06-01T00:00:00Z",
		"matrix":[{"evidence_card":"EV-DISK","prd_requirement":"REQ-DISK","adr":"ADR-DISK","tdd_obligation":"TDD-DISK","brief":"briefs/disk.md","report":"reports/disk.json"}]
	}`)
	mustWriteFeaturePlannerProductTestFile(t, dir, "implementation_ledger.jsonl",
		`{"record_type":"atom_planned","task_id":"task-disk","effort_id":"effort-disk","timestamp":"2026-06-01T00:01:00Z","complexity_tier":"C1","context_pack_id":"pack-disk","implementor":{"id":"impl","runtime":"codex","model":"test","command":"run"},"reviewer":{"id":"rev","runtime":"codex","model":"test","reasoning_effort":"medium"},"scope":{"expected_files":["internal/disk/coverage.go"],"forbidden_patterns":["docs/**"]},"required_verification":["go test ./internal/workflows/featureplannerproduct"],"report_paths":{"implementation":"reports/impl.json","review":"reports/rev.json","investigation":"reports/inv.json"}}`+"\n"+
			`{"record_type":"verification_completed","task_id":"task-disk","effort_id":"effort-disk","timestamp":"2026-06-01T00:02:00Z","tdd_obligations_satisfied":["TDD-DISK"],"verification_status":"pass","verification_commands_run":["go test ./internal/workflows/featureplannerproduct"]}`+"\n")
	mustWriteFeaturePlannerProductTestFile(t, dir, "internal/disk/coverage.go", "package disk\n")
	mustWriteFeaturePlannerProductTestFile(t, dir, "briefs/disk.md", "# Disk Brief\n")
	mustWriteFeaturePlannerProductTestFile(t, dir, "reports/disk.json", "{}\n")

	handoff, err := productworkflow.LoadImplementationHandoff(dir)
	if err != nil {
		t.Fatalf("LoadImplementationHandoff: %v", err)
	}
	brief := BriefFromImplementationHandoff(handoff)
	if brief.AcceptedCoverage == nil {
		t.Fatal("expected accepted coverage from disk-backed handoff")
	}
	if brief.AcceptedCoverage.Contract.ContractID != "cov-disk" || brief.AcceptedCoverage.Scenarios[0].ID != "BDD-DISK" {
		t.Fatalf("expected accepted coverage detail, got %+v", brief.AcceptedCoverage)
	}
	if len(brief.AcceptedCoverage.SurfaceInventory) != 2 ||
		brief.AcceptedCoverage.SurfaceInventory[0].ID != "SURF-DISK-DOMAIN" ||
		brief.AcceptedCoverage.SurfaceInventory[1].Status != productworkflow.BehavioralCoverageStatusNotRequired {
		t.Fatalf("expected disk-backed accepted surface decisions, got %+v", brief.AcceptedCoverage.SurfaceInventory)
	}
	if brief.CoverageContract == nil || brief.CoverageContract.Digest == "" {
		t.Fatalf("expected coverage contract ref with digest, got %+v", brief.CoverageContract)
	}
}

func mustWriteFeaturePlannerProductTestFile(t *testing.T, root string, rel string, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
