package featureplannerproduct

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	featureplannercontract "idiom/internal/contracts/featureplanner"
	plancontract "idiom/internal/contracts/plan"
	"idiom/internal/workflows/featureplanner"
	"idiom/internal/workflows/productworkflow"
)

var _ = ginkgo.Describe("Product workflow feature-planner handoff", func() {
	ginkgo.It("carries implementation grounding and accepted coverage", func() {
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

		gomega.Expect(brief).NotTo(gomega.BeNil())
		gomega.Expect(brief.EffortID).To(gomega.Equal(handoff.EffortID))
		gomega.Expect(brief.TaskID).To(gomega.Equal(handoff.TaskID))
		gomega.Expect(brief.StatusBucket).To(gomega.Equal(handoff.StatusBucket))
		gomega.Expect(brief.CurrentPhase).To(gomega.Equal(handoff.CurrentPhase))
		gomega.Expect(brief.CurrentState).To(gomega.Equal(handoff.CurrentState))
		gomega.Expect(brief.NextValidActions).To(gomega.Equal(handoff.NextValidActions))
		gomega.Expect(brief.SourceSatisfiedDelivery).To(gomega.BeNil())
		gomega.Expect(brief.FilesToModify).NotTo(gomega.BeEmpty())
		gomega.Expect(brief.AuthorizedNewPackageRoots).To(gomega.Equal([]string{"internal/newpkg"}))
		gomega.Expect(brief.RequiredBehavior).NotTo(gomega.BeEmpty())
		gomega.Expect(brief.Tests).NotTo(gomega.BeEmpty())
		gomega.Expect(brief.Acceptance).NotTo(gomega.BeEmpty())
		gomega.Expect(brief.Constraints).NotTo(gomega.BeEmpty())
		gomega.Expect(brief.Decisions).NotTo(gomega.BeEmpty())
		gomega.Expect(brief.PRDRequirements).To(gomega.HaveLen(len(handoff.PRDRequirements)))
		gomega.Expect(brief.TDDObligations).To(gomega.HaveLen(len(handoff.TDDObligations)))
		gomega.Expect(brief.CoverageContract).NotTo(gomega.BeNil())
		gomega.Expect(brief.CoverageContract.ContractID).To(gomega.Equal("cov-1"))
		gomega.Expect(brief.CoverageContract.CoverageTier).To(gomega.Equal("behavior_standard"))
		gomega.Expect(brief.AcceptedCoverage).NotTo(gomega.BeNil())
		gomega.Expect(brief.AcceptedCoverage.Scenarios).To(gomega.HaveLen(1))
		gomega.Expect(brief.AcceptedCoverage.Scenarios[0].Then).To(gomega.ContainElement("thing is done"))
		gomega.Expect(brief.AcceptedCoverage.Invariants).To(gomega.ConsistOf(gomega.HaveField("ID", "INV-ACCEPTED")))
		gomega.Expect(brief.AcceptedCoverage.FuzzTargets).To(gomega.ConsistOf(gomega.HaveField("TestRefs", gomega.ContainElement("FuzzParsePlan"))))
		gomega.Expect(brief.AcceptedCoverage.RAGEvals).To(gomega.ConsistOf(gomega.HaveField("Thresholds", gomega.HaveKeyWithValue("groundedness", 0.95))))
		gomega.Expect(brief.AcceptedCoverage.SecurityCases).To(gomega.ConsistOf(gomega.HaveField("Expected", "hidden instructions are not printed")))
		gomega.Expect(brief.AcceptedCoverage.PrivacyControls).To(gomega.ConsistOf(gomega.HaveField("RedactionRefs", gomega.ContainElement("redaction/prompts"))))
		gomega.Expect(brief.AcceptedCoverage.ProviderContracts).To(gomega.ConsistOf(gomega.HaveField("Provider", "fake-llm")))
		gomega.Expect(brief.AcceptedCoverage.Observability).To(gomega.ConsistOf(gomega.HaveField("RequiredFields", gomega.ContainElement("diagnostic"))))
		gomega.Expect(brief.AcceptedCoverage.PerformanceBounds).To(gomega.ConsistOf(gomega.HaveField("Bound", "deterministic bounded text")))
		gomega.Expect(brief.AcceptedCoverage.E2EJourneys).To(gomega.ConsistOf(gomega.HaveField("ScriptRefs", gomega.ContainElement("scripts/e2e/feature_planner_bdd_tdd.sh"))))
		gomega.Expect(brief.AcceptedCoverage.SurfaceInventory).To(gomega.HaveLen(2))
		gomega.Expect(brief.AcceptedCoverage.SurfaceInventory[0]).To(gomega.And(
			gomega.HaveField("ID", "SURF-DOMAIN"),
			gomega.HaveField("Obligations", gomega.ContainElement("TDD-1")),
		))
		gomega.Expect(brief.AcceptedCoverage.SurfaceInventory[1]).To(gomega.And(
			gomega.HaveField("ID", "SURF-TRANSPORT"),
			gomega.HaveField("Status", productworkflow.BehavioralCoverageStatusNotRequired),
		))
		gomega.Expect(brief.AcceptedCoverage.Questions).To(gomega.ConsistOf(gomega.And(
			gomega.HaveField("Status", "resolved"),
			gomega.HaveField("Resolution", "Approved in product workflow before feature planning."),
		)))
		gomega.Expect(brief.AllowsFeaturePlanDispatch()).To(gomega.BeTrue())
	})

	ginkgo.It("does not project a structurally valid closeout without product ledger verification", func() {
		closeout := &productworkflow.SourceSatisfiedDeliveryCloseout{
			EffortID:      "closed-effort",
			TaskID:        "closed-task",
			Outcome:       plancontract.PlanExecuteTerminalOutcomeSourceSatisfied,
			SessionID:     "source-satisfied-session",
			EvidencePaths: []string{"internal/example.go"},
			Evidence: []plancontract.SourceSatisfiedEvidenceRef{{
				Path: "internal/example.go",
				Kind: plancontract.SourceSatisfiedEvidenceKindHarnessObservedSource,
			}},
			RecordedAt: "2026-07-12T12:00:00Z",
		}
		authority := struct {
			EffortID      string                                    `json:"effort_id"`
			TaskID        string                                    `json:"task_id"`
			Outcome       plancontract.PlanExecuteTerminalOutcome   `json:"outcome"`
			SessionID     string                                    `json:"session_id"`
			EvidencePaths []string                                  `json:"evidence_paths"`
			Evidence      []plancontract.SourceSatisfiedEvidenceRef `json:"evidence"`
			RecordedAt    string                                    `json:"recorded_at"`
		}{
			EffortID:      closeout.EffortID,
			TaskID:        closeout.TaskID,
			Outcome:       closeout.Outcome,
			SessionID:     closeout.SessionID,
			EvidencePaths: closeout.EvidencePaths,
			Evidence:      closeout.Evidence,
			RecordedAt:    closeout.RecordedAt,
		}
		body, err := json.Marshal(authority)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		closeout.CompletionDigest = fmt.Sprintf("sha256:%x", sha256.Sum256(body))
		handoff := productworkflow.ImplementationHandoff{
			EffortID:                "closed-effort",
			TaskID:                  "closed-task",
			StatusBucket:            productworkflow.WorkflowStatusBucketClosed,
			CurrentPhase:            string(productworkflow.PhaseComplete),
			CurrentState:            productworkflow.WorkflowStateClosed,
			CoverageGatePassed:      true,
			SourceSatisfiedDelivery: closeout,
		}

		brief := BriefFromImplementationHandoff(handoff)
		gomega.Expect(brief.SourceSatisfiedDelivery).To(gomega.BeNil())
		gomega.Expect(brief.IsClosedSourceSatisfied()).To(gomega.BeFalse())
		gomega.Expect(brief.AllowsFeaturePlanDispatch()).To(gomega.BeTrue())
	})

	ginkgo.It("does not project a manufactured invalid delivery closeout into planner authority", func() {
		handoff := productworkflow.ImplementationHandoff{
			EffortID:           "forged-effort",
			TaskID:             "forged-task",
			StatusBucket:       "closed",
			CurrentPhase:       string(productworkflow.PhaseComplete),
			CurrentState:       productworkflow.WorkflowStateClosed,
			CoverageGatePassed: true,
			SourceSatisfiedDelivery: &productworkflow.SourceSatisfiedDeliveryCloseout{
				EffortID:         "forged-effort",
				TaskID:           "forged-task",
				Outcome:          plancontract.PlanExecuteTerminalOutcomeSourceSatisfied,
				SessionID:        "",
				CompletionDigest: "sha256:forged",
				EvidencePaths:    []string{"internal/example.go"},
			},
		}

		brief := BriefFromImplementationHandoff(handoff)
		gomega.Expect(brief.SourceSatisfiedDelivery).To(gomega.BeNil())
		gomega.Expect(brief.IsClosedSourceSatisfied()).To(gomega.BeFalse())
		gomega.Expect(brief.AllowsFeaturePlanDispatch()).To(gomega.BeTrue())
	})

	ginkgo.It("carries accepted BDD and TDD coverage through planner validation", ginkgo.Label("development-scenario", "integration", "contract"), func() {
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
		ledger, digest := acceptedBehavioralCoverageLedger(contract)
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
				RecordType:           productworkflow.LedgerRecordAtomPlanned,
				TaskID:               "task-pipeline",
				Scope:                &productworkflow.AtomScope{ExpectedFiles: []string{"internal/pipeline/coverage.go", "internal/pipeline/coverage_test.go"}},
				RequiredVerification: []string{"go test ./internal/workflows/featureplannerproduct"},
			}},
		})
		brief := BriefFromImplementationHandoff(handoff)
		gomega.Expect(brief.AcceptedCoverage).NotTo(gomega.BeNil())
		gomega.Expect(brief.AllowsFeaturePlanDispatch()).To(gomega.BeTrue())

		plan := featureplanner.FeaturePlan{
			SchemaVersion: featureplanner.FeaturePlanSchemaVersion,
			Tool:          string(featureplannercontract.ToolSubmitFeaturePlan),
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

		gomega.Expect(featureplanner.ValidatePlanProductWorkflowContext(plan, *brief, "")).To(gomega.Succeed())

		normalized := featureplanner.NormalizePlanProductWorkflowContext(plan, *brief, "")
		gomega.Expect(normalized.AcceptedCoverage).NotTo(gomega.BeNil())
		gomega.Expect(normalized.AcceptedCoverage.Authority).To(gomega.Equal("product_workflow"))
		gomega.Expect(normalized.AcceptedCoverage.Scenarios).To(gomega.ContainElement(gomega.HaveField("ID", "BDD-PIPE")))
		gomega.Expect(normalized.AcceptedCoverage.DesignContracts).To(gomega.ContainElement(gomega.HaveField("ID", "DBC-PIPE")))
		gomega.Expect(normalized.AcceptedCoverage.Invariants).To(gomega.ContainElement(gomega.HaveField("ID", "INV-PIPE")))
		gomega.Expect(normalized.AcceptedCoverage.FailureModes).To(gomega.ContainElement(gomega.HaveField("ID", "FM-PIPE")))
		gomega.Expect(normalized.AcceptedCoverage.TestObligations).To(gomega.ContainElement(gomega.HaveField("ID", "TDD-PIPE")))
	})

	ginkgo.It("loads accepted coverage from the durable implementation handoff", ginkgo.Label("integration"), func() {
		dir := ginkgo.GinkgoT().TempDir()
		mustWriteFeaturePlannerProductTestFile(dir, productworkflow.WorkflowStateFileName, `{
		"schema_version":"idiom.workflow_state.v1.fixture",
		"effort_id":"effort-disk",
		"task_id":"task-disk",
		"title":"Disk Coverage",
		"status_bucket":"closed",
		"current_phase":"complete",
		"current_state":"closed",
		"last_event_id":"evt-disk",
		"artifact_paths":{"events":"events.jsonl"},
		"context":{"context_pack_id":"pack-disk","freshness_state":"current","referenced_paths":["internal/disk/coverage.go"],"unresolved_gaps":[],"refresh_policy":"on_demand"},
		"coverage":{"coverage_tier":"behavior_standard"},
		"privacy":{"redaction_status":"applied","fields_excluded":[]},
		"folder_path":"docs/product-workflows/efforts/started/effort-disk",
		"durability_level":"source_controlled",
		"next_valid_actions":["view_status"]
	}`)
		mustWriteFeaturePlannerProductTestFile(dir, "prd.md", `---
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
		mustWriteFeaturePlannerProductTestFile(dir, "adrs/index.json", `{
		"schema_version":"idiom.adr_index.v1.fixture",
		"effort_id":"effort-disk",
		"adrs":[{"adr_id":"ADR-DISK","title":"Carry accepted coverage","status":"accepted","path":"ADR-DISK.md","prd_refs":["REQ-DISK"]}]
	}`)
		mustWriteFeaturePlannerProductTestFile(dir, "adrs/ADR-DISK.md", `---
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
		ledger, digest := acceptedBehavioralCoverageLedger(contract)
		ledgerBytes, err := json.MarshalIndent(ledger, "", "  ")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		mustWriteFeaturePlannerProductTestFile(dir, "tdd.json", string(ledgerBytes)+"\n")
		mustWriteFeaturePlannerProductTestFile(dir, "events.jsonl", `{"event":"behavioral_coverage_accepted","event_id":"evt-disk","state":"accepted","digest":`+strconv.Quote(digest)+`,"source":"test"}`+"\n")
		mustWriteFeaturePlannerProductTestFile(dir, "evidence/traceability_matrix.json", `{
		"schema_version":"idiom.traceability.v1.fixture",
		"effort_id":"effort-disk",
		"task_id":"task-disk",
		"created_at":"2026-06-01T00:00:00Z",
		"matrix":[{"evidence_card":"EV-DISK","prd_requirement":"REQ-DISK","adr":"ADR-DISK","tdd_obligation":"TDD-DISK","brief":"briefs/disk.md","report":"reports/disk.json"}]
	}`)
		mustWriteFeaturePlannerProductTestFile(dir, "implementation_ledger.jsonl",
			`{"record_type":"atom_planned","task_id":"task-disk","effort_id":"effort-disk","timestamp":"2026-06-01T00:01:00Z","complexity_tier":"C1","context_pack_id":"pack-disk","implementor":{"id":"impl","runtime":"codex","model":"test","command":"run"},"reviewer":{"id":"rev","runtime":"codex","model":"test","reasoning_effort":"medium"},"scope":{"expected_files":["internal/disk/coverage.go"],"forbidden_patterns":["docs/**"]},"required_verification":["go test ./internal/workflows/featureplannerproduct"],"report_paths":{"implementation":"reports/impl.json","review":"reports/rev.json","investigation":"reports/inv.json"}}`+"\n"+
				`{"record_type":"verification_completed","task_id":"task-disk","effort_id":"effort-disk","timestamp":"2026-06-01T00:02:00Z","tdd_obligations_satisfied":["TDD-DISK"],"verification_status":"pass","verification_commands_run":["go test ./internal/workflows/featureplannerproduct"]}`+"\n")
		mustWriteFeaturePlannerProductTestFile(dir, "internal/disk/coverage.go", "package disk\n")
		mustWriteFeaturePlannerProductTestFile(dir, "briefs/disk.md", "# Disk Brief\n")
		mustWriteFeaturePlannerProductTestFile(dir, "reports/disk.json", "{}\n")

		handoff, err := productworkflow.LoadImplementationHandoff(dir)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		brief := BriefFromImplementationHandoff(handoff)
		gomega.Expect(brief.AcceptedCoverage).NotTo(gomega.BeNil())
		gomega.Expect(brief.AcceptedCoverage.Contract.ContractID).To(gomega.Equal("cov-disk"))
		gomega.Expect(brief.AcceptedCoverage.Scenarios).To(gomega.ContainElement(gomega.HaveField("ID", "BDD-DISK")))
		gomega.Expect(brief.AcceptedCoverage.SurfaceInventory).To(gomega.HaveLen(2))
		gomega.Expect(brief.AcceptedCoverage.SurfaceInventory).To(gomega.ContainElements(
			gomega.HaveField("ID", "SURF-DISK-DOMAIN"),
			gomega.HaveField("Status", productworkflow.BehavioralCoverageStatusNotRequired),
		))
		gomega.Expect(brief.CoverageContract).NotTo(gomega.BeNil())
		gomega.Expect(brief.CoverageContract.Digest).NotTo(gomega.BeEmpty())
	})
})

var _ = ginkgo.Describe(
	"Finalized product source-satisfied delivery projection",
	ginkgo.Label("development-scenario", "e2e", "integration", "contract", "smoke", "race-contract"),
	func() {
		ginkgo.It("projects the real all-atoms source-satisfied terminal shape", func() {
			state := finalizedSourceSatisfiedFeatureStateForAdapter(
				"internal/alpha.go",
				"internal/beta.go",
			)

			completion, candidate, err := sourceSatisfiedCompletionFromFinalFeatureState(state)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(candidate).To(gomega.BeTrue())
			gomega.Expect(plancontract.ValidatePlanExecuteTerminalCompletionRecord(completion)).To(gomega.Succeed())
			gomega.Expect(completion.Outcome).To(gomega.Equal(plancontract.PlanExecuteTerminalOutcomeSourceSatisfied))
			gomega.Expect(completion.RequiredEditPaths).To(gomega.Equal([]string{"internal/alpha.go", "internal/beta.go"}))
			gomega.Expect(completion.SourceSatisfiedEvidencePaths).To(gomega.Equal(completion.RequiredEditPaths))
			gomega.Expect(completion.SourceSatisfiedEvidence).To(gomega.Equal([]plancontract.SourceSatisfiedEvidenceRef{
				{Path: "internal/alpha.go", Kind: plancontract.SourceSatisfiedEvidenceKindHarnessObservedSource},
				{Path: "internal/beta.go", Kind: plancontract.SourceSatisfiedEvidenceKindHarnessObservedSource},
			}))
			repeated, repeatedCandidate, repeatedErr := sourceSatisfiedCompletionFromFinalFeatureState(state)
			gomega.Expect(repeatedErr).NotTo(gomega.HaveOccurred())
			gomega.Expect(repeatedCandidate).To(gomega.BeTrue())
			gomega.Expect(repeated).To(gomega.Equal(completion), "the same finalized state must project deterministic completion authority")
		})

		ginkgo.It("preserves typed current-state evidence across durable resume", func() {
			state := finalizedSourceSatisfiedFeatureStateForAdapter("internal/resumed.go")
			encoded, err := json.Marshal(state)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			var resumed featureplanner.FeatureState
			gomega.Expect(json.Unmarshal(encoded, &resumed)).To(gomega.Succeed())
			gomega.Expect(featureplanner.ValidateDurableState(resumed)).To(gomega.Succeed())
			gomega.Expect(resumed.TaskID).To(gomega.Equal("adapter-task"))

			completion, candidate, err := sourceSatisfiedCompletionFromFinalFeatureState(resumed)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(candidate).To(gomega.BeTrue())
			gomega.Expect(completion.SourceSatisfiedEvidence).To(gomega.Equal([]plancontract.SourceSatisfiedEvidenceRef{{
				Path: "internal/resumed.go",
				Kind: plancontract.SourceSatisfiedEvidenceKindHarnessObservedSource,
			}}))
		})

		ginkgo.It("rejects a finalized state from another task in the same effort", func() {
			state := finalizedSourceSatisfiedFeatureStateForAdapter("internal/same-effort.go")
			state.TaskID = "other-task"
			brief := &featureplanner.ProductWorkflowBrief{
				EffortID:   state.EffortID,
				TaskID:     "adapter-task",
				EffortRoot: state.EffortRoot,
			}

			recorded, err := FinalizeSourceSatisfiedDeliveryFromFeatureState(brief, state)
			gomega.Expect(recorded).To(gomega.BeFalse())
			gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring(
				`source-satisfied feature state task_id "other-task" does not match product task_id "adapter-task"`,
			)))
		})

		ginkgo.It("leaves ordinary completed delivery neutral", func() {
			completion, candidate, err := sourceSatisfiedCompletionFromFinalFeatureState(featureplanner.FeatureState{
				Status: featureplanner.FeatureStateComplete,
				Atoms: []featureplanner.FeatureAtomState{{
					FeatureAtomPlan: featureplanner.FeatureAtomPlan{ID: "atom-ordinary", RequiredEditPaths: []string{"internal/ordinary.go"}},
					State:           featureplanner.AtomStateComplete,
				}},
				VerificationClosures: []featureplanner.VerificationClosure{{
					EventID: "evt-ordinary", AtomID: "atom-ordinary", Status: featureplanner.VerificationClosureStatusPassed,
				}},
			})
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(candidate).To(gomega.BeFalse())
			gomega.Expect(completion).To(gomega.Equal(plancontract.PlanExecuteTerminalCompletionRecord{}))
		})

		ginkgo.It("leaves a valid mixed completed and source-satisfied delivery neutral", func() {
			state := finalizedSourceSatisfiedFeatureStateForAdapter("internal/alpha.go", "internal/beta.go")
			state.Atoms[1].State = featureplanner.AtomStateComplete
			state.VerificationClosures[1].Status = featureplanner.VerificationClosureStatusPassed
			state.VerificationClosures[1].Outcome = string(plancontract.PlanExecuteTerminalOutcomeCompleted)
			state.VerificationClosures[1].SourceSatisfiedEvidencePaths = nil
			state.VerificationClosures[1].SourceSatisfiedEvidence = nil
			state.VerificationClosures[1].RequiredTestResults = []featureplanner.VerificationCommandResult{{
				Command: "go test ./internal/beta", Status: string(featureplanner.VerificationClosureStatusPassed), Passed: true,
			}}
			state.ReleaseReadiness = featureplanner.EvaluateReleaseReadiness(state, state.ReleaseReadiness.DecidedAt)

			completion, candidate, err := sourceSatisfiedCompletionFromFinalFeatureState(state)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(candidate).To(gomega.BeFalse())
			gomega.Expect(completion).To(gomega.Equal(plancontract.PlanExecuteTerminalCompletionRecord{}))
		})

		ginkgo.DescribeTable(
			"rejects incomplete, inconsistent, stale, or out-of-scope closure authority",
			func(mutate func(*featureplanner.FeatureState), errorText string) {
				state := finalizedSourceSatisfiedFeatureStateForAdapter(
					"internal/alpha.go",
					"internal/beta.go",
				)
				mutate(&state)

				_, candidate, err := sourceSatisfiedCompletionFromFinalFeatureState(state)
				gomega.Expect(candidate).To(gomega.BeFalse())
				gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring(errorText)))
			},
			ginkgo.Entry("missing atom closure", func(state *featureplanner.FeatureState) {
				state.VerificationClosures = state.VerificationClosures[:1]
			}, "validate finalized source-satisfied feature state"),
			ginkgo.Entry("missing durable task identity", func(state *featureplanner.FeatureState) {
				state.TaskID = ""
			}, "missing durable task_id"),
			ginkgo.Entry("nonterminal atom in complete feature state", func(state *featureplanner.FeatureState) {
				state.Atoms[1].State = featureplanner.AtomStateImplementing
			}, "contains nonterminal atom"),
			ginkgo.Entry("duplicate atom closure", func(state *featureplanner.FeatureState) {
				duplicate := state.VerificationClosures[0]
				duplicate.EventID = "evt-source-duplicate"
				state.VerificationClosures = append(state.VerificationClosures, duplicate)
			}, "duplicate verification closures"),
			ginkgo.Entry("stale plan revision", func(state *featureplanner.FeatureState) {
				state.VerificationClosures[0].PlanRevision++
			}, "does not match active revision"),
			ginkgo.Entry("out-of-scope evidence", func(state *featureplanner.FeatureState) {
				state.VerificationClosures[0].SourceSatisfiedEvidencePaths = []string{"internal/outside.go"}
			}, "must exactly cover"),
			ginkgo.Entry("missing typed evidence refs", func(state *featureplanner.FeatureState) {
				state.VerificationClosures[0].SourceSatisfiedEvidence = nil
			}, "evidence refs are missing"),
			ginkgo.Entry("unsupported typed evidence kind", func(state *featureplanner.FeatureState) {
				state.VerificationClosures[0].SourceSatisfiedEvidence[0].Kind = plancontract.SourceSatisfiedEvidenceKind("model_asserted")
			}, "must exactly cover"),
			ginkgo.Entry("typed evidence path mismatch", func(state *featureplanner.FeatureState) {
				state.VerificationClosures[0].SourceSatisfiedEvidence[0].Path = "internal/outside.go"
			}, "must exactly cover"),
			ginkgo.Entry("changed paths on source-satisfied closure", func(state *featureplanner.FeatureState) {
				state.VerificationClosures[0].ChangedPaths = []string{"internal/alpha.go"}
			}, "contains changed paths"),
		)
	},
)

func finalizedSourceSatisfiedFeatureStateForAdapter(paths ...string) featureplanner.FeatureState {
	ginkgo.GinkgoHelper()

	state := featureplanner.NewState("source-satisfied-adapter", "inspect existing implementation", "", 3, time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC))
	state.EffortID = "adapter-effort"
	state.TaskID = "adapter-task"
	state.EffortRoot = "/tmp/adapter-effort"
	state.Status = featureplanner.FeatureStateComplete
	state.PlanningLoop.State = featureplanner.FeatureStateComplete
	state.Plan.Artifact = featureplanner.FeaturePlanArtifact{
		ArtifactID:           "adapter-plan",
		Revision:             1,
		Digest:               "sha256:" + strings.Repeat("c", 64),
		WorkspaceFingerprint: "workspace-adapter",
	}
	state.Plan.Approval = featureplanner.FeaturePlanApproval{
		Status:               "approved",
		EventID:              "evt-adapter-approved",
		PlanRevision:         state.Plan.Artifact.Revision,
		PlanDigest:           state.Plan.Artifact.Digest,
		WorkspaceFingerprint: state.Plan.Artifact.WorkspaceFingerprint,
	}
	for i, path := range paths {
		atomID := "atom-" + strconv.Itoa(i+1)
		atomPlan := featureplanner.FeatureAtomPlan{ID: atomID, RequiredEditPaths: []string{path}}
		state.Plan.Atoms = append(state.Plan.Atoms, atomPlan)
		state.Atoms = append(state.Atoms, featureplanner.FeatureAtomState{
			FeatureAtomPlan: atomPlan,
			State:           featureplanner.AtomStateSourceSatisfied,
		})
		state.VerificationClosures = append(state.VerificationClosures, featureplanner.VerificationClosure{
			EventID:                      "evt-source-" + strconv.Itoa(i+1),
			AtomID:                       atomID,
			Status:                       featureplanner.VerificationClosureStatusSourceSatisfied,
			Outcome:                      string(plancontract.PlanExecuteTerminalOutcomeSourceSatisfied),
			PlanArtifactID:               state.Plan.Artifact.ArtifactID,
			PlanRevision:                 state.Plan.Artifact.Revision,
			PlanDigest:                   state.Plan.Artifact.Digest,
			ApprovalEventID:              state.Plan.Approval.EventID,
			WorkspaceFingerprint:         state.Plan.Artifact.WorkspaceFingerprint,
			SourceSatisfiedEvidencePaths: []string{path},
			SourceSatisfiedEvidence: []plancontract.SourceSatisfiedEvidenceRef{{
				Path: path,
				Kind: plancontract.SourceSatisfiedEvidenceKindHarnessObservedSource,
			}},
			CreatedAt: "2026-07-12T12:00:00Z",
		})
		state.Continuation.CompletedAtoms = append(state.Continuation.CompletedAtoms, atomID)
	}
	state.ReleaseReadiness = featureplanner.EvaluateReleaseReadiness(state, "2026-07-12T12:00:01Z")
	gomega.Expect(state.ReleaseReadiness.Status).To(gomega.Equal(featureplanner.ReleaseReadinessStatusReady))
	gomega.Expect(featureplanner.ValidateDurableState(state)).To(gomega.Succeed())

	return state
}
func acceptedBehavioralCoverageLedger(
	contract productworkflow.BehavioralCoverageContract,
) (productworkflow.TDDLedger, string) {
	ginkgo.GinkgoHelper()

	gomega.Expect(contract.EventID).NotTo(gomega.BeEmpty(), "accepted coverage fixtures require product-owned event provenance")
	contract.PromotionState = productworkflow.BehavioralCoveragePromotionStateAccepted
	obligations := make([]productworkflow.TDDObligation, 0, len(contract.TestObligations))
	for _, obligation := range contract.TestObligations {
		obligations = append(obligations, productworkflow.TDDObligation{
			ID:             obligation.ID,
			Type:           obligation.Type,
			Behavior:       obligation.Behavior,
			Owner:          obligation.Owner,
			Command:        obligation.Command,
			RequiredBefore: obligation.RequiredBefore,
			Status:         "planned",
		})
	}

	digest := productworkflow.BehavioralCoverageContractDigest(contract)
	gomega.Expect(digest).NotTo(gomega.BeEmpty())
	return productworkflow.TDDLedger{
		SchemaVersion:      "idiom.tdd_obligations.v2",
		EffortID:           contract.EffortID,
		TaskID:             contract.TaskID,
		CoverageTier:       contract.CoverageTier,
		Coverage:           productworkflow.ProjectBehavioralCoverageContract(contract),
		BehavioralCoverage: &contract,
		Obligations:        obligations,
	}, digest
}

func mustWriteFeaturePlannerProductTestFile(root string, rel string, body string) {
	path := filepath.Join(root, filepath.FromSlash(rel))
	gomega.Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(gomega.Succeed())
	gomega.Expect(os.WriteFile(path, []byte(body), 0o644)).To(gomega.Succeed())
}
