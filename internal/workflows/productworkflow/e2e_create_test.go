package productworkflow_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	"idiom/internal/contracts/autonomy"
	featureplannercontract "idiom/internal/contracts/featureplanner"
	"idiom/internal/workflows/featureplanner"
	"idiom/internal/workflows/featureplannerproduct"
	. "idiom/internal/workflows/productworkflow"
)

var _ = ginkgo.Describe("Product workflow pipeline", ginkgo.Label("development-scenario", "e2e", "integration", "smoke"), func() {
	ginkgo.It("creates, validates, plans, and dispatches a complete effort", func() {
		t := ginkgo.GinkgoT()
		dir := t.TempDir()
		root := filepath.Join(dir, "e2e-feature")

		result, err := InitializeEffort(CreateEffortOptions{
			EffortRoot: root, EffortID: "E2E-001", Title: "End-to-End Feature Test",
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(string(result.Status)).To(gomega.Equal("ok"))
		gomega.Expect(result.CurrentPhase).To(gomega.Equal(PhasePRDDrafting))

		// Write a valid PRD.
		prd := PRDDocument{
			FrontMatter: PRDFrontMatter{
				SchemaVersion: "idiom.prd.v1",
				Status:        string(ArtifactStateDraft),
			},
			Requirements: []PRDRequirement{
				{ID: "REQ-001", Priority: "P0", Statement: "Users can authenticate with email.",
					EvidenceRefs: []string{"E1"}, TDDObligations: []string{"TDD-001"},
					OwnerAlignment: "Product", Testability: "Login test passes",
					Metric:        "Success rate > 99%",
					OpenQuestions: []string{"MFA?"}, OpenQuestionsPresent: true},
			},
		}
		draftContext, err := LoadPRDDraftContext(root)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		prd.FrontMatter.PRDID = draftContext.Identity.PRDID
		prd.FrontMatter.TaskID = draftContext.Identity.TaskID
		prd.FrontMatter.CreatedAt = draftContext.Identity.CreatedAt
		intake, err := PersistPRDDraft(PRDDraftPersistenceRequest{
			EffortRoot:         root,
			Document:           prd,
			ExpectedGeneration: draftContext.Generation,
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(intake.Passed).To(gomega.BeTrue(), "failures=%+v", intake.Failures)

		// Approve PRD.
		_, err = SubmitPRDForReview(root)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		prdResult, prdIntake, err := ApprovePRDGate(root)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(prdIntake.Passed).To(gomega.BeTrue())
		gomega.Expect(string(prdResult.Status)).To(gomega.Equal("ok"))

		// Write ADR index + one ADR.
		idx := ADRIndex{
			SchemaVersion: "idiom.adr_index.v1", EffortID: "E2E-001",
			ADRs: []ADRIndexEntry{
				{ADRID: "ADR-001", Title: "Email Auth Strategy", Status: string(ArtifactStateDraft),
					Path: "ADR-001.md", CoverageTier: "unit",
					PRDRefs: []string{"REQ-001"}, CreatedAt: "2026-05-29T00:00:00Z"},
			},
		}
		adr := ADRDocument{
			FrontMatter: ADRFrontMatter{
				SchemaVersion: "idiom.adr.v1", ADRID: "ADR-001",
				Status:  string(ArtifactStateDraft),
				PRDRefs: []string{"REQ-001"}, EvidenceRefs: []string{"E1"},
				TDDObligations: []string{"TDD-001"}, CoverageTier: "unit",
			},
			Sections: ADRSections{
				HasContext: true, HasDecision: true, HasAlternativesConsidered: true,
				HasEvidence: true, HasConsequences: true, HasCompatibilityAndMigration: true,
				HasTDDObligations: true, HasRollback: true, HasOpenQuestions: true,
			},
			BodyMarkdown: `## Context

Email authentication must satisfy REQ-001 and evidence E1.

## Decision

Implement the bounded login behavior in the following files.

` + "`internal/auth/login.go`" + `

` + "```go" + `
package auth

func Login(email, password string) bool
` + "```" + `

` + "`internal/auth/login_test.go`" + `

` + "```go" + `
package auth

func TestLoginWithEmail(t *testing.T)
` + "```" + `

## Alternatives Considered

External identity-only authentication was rejected because it does not satisfy the local login requirement.

## Evidence

E1 records demand for email login.

## Consequences

The auth package owns login validation and its focused tests.

## Compatibility and Migration

The new endpoint is additive and requires no data migration.

## TDD Obligations

TDD-001 must pass before implementation completion.

## Rollback

Remove the additive endpoint and its focused tests.

## Open Questions

- Should MFA be required in a follow-up?
`,
		}
		adrContext, err := LoadADRGenerationContext(root)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		_, err = PersistADRDrafts(ADRDraftPersistenceRequest{
			EffortRoot:         root,
			Documents:          []ADRDocument{adr},
			IndexEntries:       idx.ADRs,
			ExpectedGeneration: adrContext.Generation,
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		// Approve ADRs.
		_, err = SubmitADRsForReview(root)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		adrResult, _, err := ApproveADRGate(root)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(string(adrResult.Status)).To(gomega.Equal("ok"), "blockers=%v", adrResult.Blockers)

		// Write consistent stub files for lint.
		gomega.Expect(os.WriteFile(filepath.Join(root, "evidence", "evidence.jsonl"), []byte(
			`{"card_id":"E1","kind":"user_research","source_handle":"interview","claim":"Users want email login","confidence":"high","freshness":"fresh","uncertainty":"","evidence_refs":["src"],"researcher_id":"r1","timestamp":"2026-05-29T00:00:00Z"}`+"\n",
		), 0o644)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(root, "evidence", "traceability_matrix.json"), []byte(
			`{"schema_version":"idiom.traceability.v1","effort_id":"E2E-001","task_id":"E2E-001","created_at":"2026-05-29T00:00:00Z","matrix":[{"evidence_card":"E1","prd_requirement":"REQ-001","adr":"ADR-001","tdd_obligation":"TDD-001","brief":"briefs/task.md","report":"reports/impl.json"}]}`+"\n",
		), 0o644)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(root, "briefs", "task.md"), []byte("# Brief\n\nImplement email authentication.\n"), 0o644)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(root, "reports", "impl.json"), []byte("{}"), 0o644)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(root, "reports", "review.json"), []byte("{}"), 0o644)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(root, "reports", "invest.json"), []byte("{}"), 0o644)).To(gomega.Succeed())

		tddLedgerFixture := TDDLedger{
			SchemaVersion: "idiom.tdd_ledger.v1", EffortID: "E2E-001",
			TaskID: "E2E-001", CoverageTier: "unit",
			Coverage: TDDCoverage{MutationMinScore: 80},
			Obligations: []TDDObligation{
				{ID: "TDD-001", Type: "acceptance", Behavior: "Email login flow",
					Owner: "internal/auth/login_test.go", Command: "go test ./auth/...",
					RequiredBefore: "implementation", Status: "planned"},
			},
		}
		tddJSON, err := json.MarshalIndent(tddLedgerFixture, "", "  ")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(os.WriteFile(filepath.Join(root, "tdd.json"), append(tddJSON, '\n'), 0o644)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(root, "implementation_ledger.jsonl"), []byte(
			`{"record_type":"atom_planned","task_id":"E2E-001","effort_id":"E2E-001","timestamp":"2026-05-29T00:00:00Z","complexity_tier":"C1","context_pack_id":"cp-1","implementor":{"id":"impl-1","runtime":"local","model":"qwen","command":"make"},"reviewer":{"id":"rev-1","model":"qwen","reasoning_effort":"high","runtime":"local","status":"pending"},"scope":{"expected_files":["internal/auth/login.go","internal/auth/login_test.go"],"forbidden":["secrets/"]},"required_verification":["go test ./auth/..."],"report_paths":{"implementation":"reports/impl.json","review":"reports/review.json","investigation":"reports/invest.json"}}`+"\n"+
				`{"record_type":"verification_completed","task_id":"E2E-001","effort_id":"E2E-001","timestamp":"2026-05-29T00:00:00Z","tdd_obligations_satisfied":["TDD-001"],"verification_status":"pass","verification_commands_run":["go test ./auth/..."],"actor":"verifier"}`+"\n",
		), 0o644)).To(gomega.Succeed())

		// Lint must pass.
		lintResult := LintEffortFolder(root)
		gomega.Expect(string(lintResult.Status)).To(gomega.Equal("pass"), "findings=%#v", lintResult.Findings)

		// Complete the effort.
		_, err = CompleteEffort(root)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		// Exercise the same durable handoff owner used by the feature-planner CLI.
		brief, err := featureplannerproduct.LoadBriefFromEffort(root)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(brief.StatusBucket).To(gomega.Equal("closed"))

		// Verify the brief contains the artifact data.
		gomega.Expect(brief.EffortID).To(gomega.Equal("E2E-001"))
		gomega.Expect(brief.CoverageGatePassed).To(gomega.BeTrue(), "blockers=%v", brief.CoverageBlockers)
		gomega.Expect(brief.PRDRequirements).To(gomega.HaveLen(1))

		reqBrief := brief.PRDRequirements[0]
		gomega.Expect(reqBrief.ID).To(gomega.Equal("REQ-001"))
		gomega.Expect(reqBrief.Priority).To(gomega.Equal("P0"))
		gomega.Expect(reqBrief.Statement).To(gomega.ContainSubstring("authenticate"))
		gomega.Expect(brief.AcceptedADRIDs).To(gomega.Equal([]string{"ADR-001"}))
		gomega.Expect(brief.TDDObligations).To(gomega.HaveLen(1))
		gomega.Expect(brief.TDDObligations[0].ID).To(gomega.Equal("TDD-001"))

		// ── Feature-planner: prompt generation ──

		state := featureplanner.NewState("e2e-session", "Implement email authentication", "", 3, time.Now())
		prompt := featureplanner.BuildPlannerPrompt("Implement email authentication", state, brief)

		for _, expected := range []string{"REQ-001", "ADR-001", "TDD-001", "authenticate"} {
			gomega.Expect(prompt).To(gomega.ContainSubstring(expected))
		}

		// ── Feature-planner: Run with mock runner ──

		scriptedPlan := map[string]any{
			"tool":           "submit_feature_plan",
			"schema_version": "idiom.feature_plan.v2",
			"feature_id":     "FEAT-E2E-001",
			"source_task": map[string]any{
				"kind":    "operator_prompt",
				"summary": "Implement email authentication",
			},
			"summary": "Implement email-based authentication flow.",
			"top_level_scope": map[string]any{
				"objective":        "Add email authentication",
				"success_criteria": []string{"Users can log in with email and password"},
			},
			"sdd_contract": map[string]any{
				"prd_requirement_refs": []string{"REQ-001"},
				"adr_refs":             []string{"ADR-001"},
				"acceptance":           []string{"Login acceptance test passes"},
			},
			"tdd_contract": map[string]any{
				"coverage_tier":                  "unit",
				"required_before_implementation": true,
				"coverage_gate_event_id":         "evt-cov-1",
				"obligations": []map[string]any{
					{"id": "TDD-001", "behavior": "Email login flow", "command": "go test ./auth/...", "required_before": "implementation"},
				},
			},
			"atoms": []map[string]any{
				{
					"schema_version":      "idiom.feature_atom.v1",
					"id":                  "atom-1",
					"title":               "Implement email auth",
					"summary":             "Add email/password auth endpoint",
					"required_edit_paths": []string{"internal/auth/login.go", "internal/auth/login_test.go"},
					"sdd_refs":            []string{"REQ-001"},
					"tdd_obligations":     []string{"TDD-001"},
					"required_tests":      []string{"go test ./auth/..."},
					"test_first": map[string]any{
						"obligation_refs":               []string{"TDD-001"},
						"commands":                      []string{"go test ./auth/..."},
						"baseline_required":             true,
						"test_artifact_paths":           []string{"internal/auth/login_test.go"},
						"pre_execution_event_ids":       []string{"evt-tdd-1"},
						"expected_pre_execution_result": "red-confirmed",
						"coverage_gate_event_id":        "evt-cov-1",
					},
					"planned_tdd_snapshot": map[string]any{
						"snapshot_id":                      "tdd-snapshot-evidence-1",
						"snapshot_digest":                  "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
						"obligation_ids":                   []string{"TDD-001"},
						"test_commands":                    []string{"go test ./auth/..."},
						"test_artifact_paths":              []string{"internal/auth/login_test.go"},
						"expected_pre_execution_results":   []string{"red-confirmed"},
						"created_before_dispatch_event_id": "evt-tdd-1",
					},
					"prompt": "Implement email authentication endpoint.",
				},
			},
		}
		scriptedPlanJSON, _ := json.Marshal(scriptedPlan)
		scriptedRunner := func(prompt string, taskFamily string) (string, error) {
			if taskFamily == string(autonomy.TaskFamilyFeaturePlannerPlanner) {
				return string(scriptedPlanJSON), nil
			}

			if taskFamily == string(autonomy.TaskFamilyFeaturePlannerImplementor) {
				return fmt.Sprintf(`{"tool":%q,"summary":"atom-1 implemented","status":"completed","review_status":"skipped","review_repair_skipped":true}`, featureplannercontract.ToolPlanExecuteComplete), nil
			}

			return "", fmt.Errorf("unknown task family: %s", taskFamily)
		}

		runResult, runErr := featureplanner.Run(state, featureplanner.RunOptions{
			Prompt:                "Implement email authentication",
			MaxPlanningIterations: 3,
			MaxAtoms:              5,
			Now:                   time.Now(),
			ProductWorkflowBrief:  brief,
		}, scriptedRunner)
		gomega.Expect(runErr).NotTo(gomega.HaveOccurred())
		gomega.Expect(runResult.State.Atoms).To(gomega.HaveLen(1), "state status=%s blockers=%v events=%+v", runResult.State.Status, runResult.State.Blockers, runResult.Events)

		atom := runResult.State.Atoms[0]
		gomega.Expect(atom.ID).To(gomega.Equal("atom-1"))
		gomega.Expect(atom.SDDRefs).To(gomega.Equal([]string{"REQ-001"}))
		gomega.Expect(atom.TDDObligations).To(gomega.Equal([]string{"TDD-001"}))
		gomega.Expect(atom.RequiredEditPaths).To(gomega.Equal([]string{"internal/auth/login.go", "internal/auth/login_test.go"}))

		// ── Plan-execute contract: verify atom dispatchability ──

		// The feature-planner produces atoms that plan-execute consumes.
		// Verify every atom has the fields plan-execute requires.
		for i, a := range runResult.State.Atoms {
			gomega.Expect(a.ID).NotTo(gomega.BeEmpty(), "atom[%d]", i)
			gomega.Expect(len(a.RequiredEditPaths)+len(a.AllowedEditPaths)).To(gomega.BeNumerically(">", 0), "atom[%d] (%s)", i, a.ID)
			gomega.Expect(a.SDDRefs).NotTo(gomega.BeEmpty(), "atom[%d] (%s)", i, a.ID)
		}

		// Verify the events contain both planning and dispatch events.
		var planSubmitCount, dispatchCount int

		for _, e := range runResult.Events {
			switch e.Type {
			case featureplannercontract.EventName(featureplannercontract.EventPlanAccepted):
				planSubmitCount++
			case featureplannercontract.EventName(featureplannercontract.EventAtomDispatched),
				featureplannercontract.EventName(featureplannercontract.EventAtomCompleted):
				dispatchCount++
			}
		}

		gomega.Expect(planSubmitCount).To(gomega.BeNumerically(">", 0))
		gomega.Expect(dispatchCount).To(gomega.BeNumerically(">", 0))
	})
})
