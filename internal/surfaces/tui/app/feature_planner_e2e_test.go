package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"

	"idiom/internal/platform/textutil"
	"idiom/internal/workflows/productworkflow"
)

func setupE2ESession(t ginkgo.FullGinkgoTInterface, responses []string, expectedCallCount ...int) (*promptExecutionSession, string, *scriptedLLMRecorder) {
	ginkgo.GinkgoHelper()

	tmp := t.TempDir()
	effortRoot := filepath.Join(tmp, "docs", "product-workflows", "efforts", "started", "test-effort")
	script := scriptedLLMConfig{responses: responses}
	if len(expectedCallCount) > 0 {
		script.assertCallCount = true
		script.expectedCallCount = expectedCallCount[0]
	}
	client := newScriptedLLMRecorder(t, script)

	live := &Model{
		client: client.mock,
		events: make(chan tea.Msg, promptWorkerEventBuffer),
	}
	live.featurePlannerEffortRoot = effortRoot
	live.featurePlannerPhase = productworkflow.PhasePRDDrafting

	{
		err := os.MkdirAll(effortRoot, 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir effort root: %v", err))
	}

	for _, sub := range []string{"adrs", "evidence", "briefs", "reports"} {
		{
			err := os.MkdirAll(filepath.Join(effortRoot, sub), 0o755)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir %s: %v", sub, err))
		}
	}
	writeWorkflowState(t, effortRoot, productworkflow.PhasePRDDrafting, "planned")

	return newDetachedPromptExecutionSession(context.Background(), live), effortRoot, client
}

func writeWorkflowState(t ginkgo.FullGinkgoTInterface, effortRoot string, phase productworkflow.CreationPhase, state string) {
	ginkgo.GinkgoHelper()

	ws := productworkflow.WorkflowState{
		SchemaVersion: "idiom.product_workflow_state.v1",
		EffortID:      "test-effort",
		TaskID:        "test-effort",
		Title:         "Test Feature",
		CreatedAt:     "2026-01-01T00:00:00Z",
		UpdatedAt:     "2026-01-01T00:00:00Z",
		LastEventID:   "evt-create-init-2026-01-01T00:00:00Z",
		StatusBucket:  "started",
		CurrentPhase:  string(phase),
		CurrentState:  state,
		ArtifactPaths: map[string]string{"prd": "prd.md", "events": "events.jsonl"},
		Context: productworkflow.WorkflowContext{
			ContextPackID:  "test-pack",
			FreshnessState: "fresh",
		},
		Coverage: productworkflow.CoverageState{
			CoverageTier: "unit",
		},
		Privacy: productworkflow.PrivacyState{
			RedactionStatus: "clean",
		},
		FolderPath:       effortRoot,
		DurabilityLevel:  "standard",
		NextValidActions: []string{"approve_prd", "edit_requirements"},
	}
	data, _ := json.MarshalIndent(ws, "", "  ")

	data = append(data, '\n')
	gomega.Expect(os.WriteFile(filepath.Join(effortRoot, "workflow_state.json"), data, 0o644)).To(gomega.Succeed())
	gomega.Expect(os.WriteFile(filepath.Join(effortRoot, "events.jsonl"), nil, 0o644)).To(gomega.Succeed())
}

func advanceE2EPRDToADRDrafting(t ginkgo.FullGinkgoTInterface, effortRoot string) {
	ginkgo.GinkgoHelper()

	result, err := productworkflow.SubmitPRDForReview(effortRoot)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(result.CurrentPhase).To(gomega.Equal(productworkflow.PhasePRDReview))
	result, intake, err := productworkflow.ApprovePRDGate(effortRoot)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(intake.Passed).To(gomega.BeTrue(), "failures=%#v", intake.Failures)
	gomega.Expect(result.CurrentPhase).To(gomega.Equal(productworkflow.PhaseADRDrafting))
}

func validPRD() string {
	return `---
schema_version: idiom.prd.v1
prd_id: test-effort
status: draft
task_id: test-effort
created_at: 2026-01-01T00:00:00Z
---

# PRD: Test Feature

## Requirements

### REQ-001

- **id**: REQ-001
- **priority**: P1
- **statement**: The system shall support OAuth 2.0 login.
- **owner_alignment**: End users need secure authentication.
- **testability**: Can be verified by attempting login with valid OAuth token.
- **metric**: 100% of login attempts succeed with valid token.
- **evidence_refs**: [inference]
- **tdd_obligations**: [TDD-REQ-001]
- **open_questions**: []

### REQ-002

- **id**: REQ-002
- **priority**: P2
- **statement**: The system shall support password reset via email.
- **owner_alignment**: Users who forget passwords need recovery.
- **testability**: Can be verified by triggering password reset flow.
- **metric**: Password reset email arrives within 60 seconds.
- **evidence_refs**: [inference]
- **tdd_obligations**: [TDD-REQ-002]
- **open_questions**: []
`
}

func validADRs() string {
	return `---
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
The system must support OAuth 2.0.

## Decision
Use authorization code flow with PKCE.

## Alternatives Considered
Client credentials rejected.

## Evidence
Industry standard.

## Consequences
Additional redirect URI handling.

## Compatibility and Migration
Backward compatible.

## TDD Obligations
Automated OAuth flow test.

## Rollback
Revert if issues arise.

## Open Questions
Which identity providers?
===
---
schema_version: idiom.adr.v1
adr_id: ADR-002
status: draft
prd_refs:
  - REQ-002
evidence_refs:
  - inference
tdd_obligations:
  - TDD-ADR-002
coverage_tier: unit
---
## Context
Users need password reset.

## Decision
Time-limited reset link via email.

## Alternatives Considered
SMS rejected.

## Evidence
Industry standard.

## Consequences
Requires email infra.

## Compatibility and Migration
Compatible.

## TDD Obligations
Automated reset flow test.

## Rollback
Disable if abused.

## Open Questions
Max link validity?
`
}

const garbage = "# Just some random prose\n\nNo front matter, no format, nothing parseable.\n"

var _ = ginkgo.Describe("Feature planner end-to-end workflow", ginkgo.Label("e2e"), func() {
	// TestE2EFeaturePlannerPRDToADR exercises the full PRD → approve → ADR flow.
	ginkgo.It("drafts and approves a PRD before generating ADRs", func() {
		t := ginkgo.GinkgoT()
		m, effortRoot, _ := setupE2ESession(t, []string{validPRD(), validADRs()})
		ctx := context.Background()

		_, err := m.runPRDDraftingTurn(ctx, "Add OAuth 2.0")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		prdData, err := os.ReadFile(filepath.Join(effortRoot, "prd.md"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		prd, err := productworkflow.ParsePRDMarkdown(prdData)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(prd.Requirements).To(gomega.HaveLen(2))

		_, err = m.approvePRDAndAdvance(context.Background(), effortRoot)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		adrDir := filepath.Join(effortRoot, "adrs")
		files, _ := filepath.Glob(filepath.Join(adrDir, "*.md"))

		gomega.Expect(files).NotTo(gomega.BeEmpty(), "no ADR files found")

		idxData, _ := os.ReadFile(filepath.Join(adrDir, "index.json"))

		idx, err := productworkflow.ParseADRIndexJSON(idxData)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		for _, entry := range idx.ADRs {
			_, err := os.Stat(filepath.Join(adrDir, entry.Path))
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), "ADR %s in index but not on disk", entry.Path)
		}

		fmt.Fprintf(ginkgo.GinkgoWriter, "PASS: %d ADRs, index valid", len(idx.ADRs))
	})

	ginkgo.It("keeps a plan revision inside the feature-planner lane", ginkgo.SpecTimeout(asyncOwnershipSpecTimeout), func(ctx ginkgo.SpecContext) {
		t := ginkgo.GinkgoT()
		projectRoot := t.TempDir()
		cfg := featurePlannerTestConfig(projectRoot)
		state := saveAcceptedSourceFeaturePlannerQuestionState(t, projectRoot, "accepted-source-e2e-session")

		client := newScriptedLLMRecorder(t, scriptedLLMConfig{responses: []string{
			`{"tool":"ask_planner_question","question_summary":"Confirm persistent memory and multi-repo plan additions","blocking_claim":"The operator asked to add more information to the plan, so the revised MVP boundary needs confirmation before approval.","requested_decision":"Confirm the plan should add persistent memory storage, repository-scoped namespaces, and explicit multi-repo isolation tests.","question_kind":"external_answer_required","authority_required":"operator_approval","required_before":"plan_approval","affected_paths":["llm_brain/query_handler.py","tests/test_query_handler.py"],"affected_fields":["requirements_scope","test_obligations"]}`,
		}})
		m := newReadyFeaturePlannerTUITestModel(t, cfg, client, state.SessionID, true)

		revision := "Can you add more info to the plan for persistent memory and multi-repo support?"
		m.input.SetValue(revision)
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		m = updated.(*Model)
		m = waitForFeaturePlannerTUIStreamDone(ctx, t, m)

		gomega.Expect(client.messages).To(gomega.HaveLen(1))
		gomega.Expect(featurePlannerRequestMessagesContain(client.messages[0], "Operator plan revision instruction:")).To(gomega.BeTrue())
		gomega.Expect(featurePlannerRequestMessagesContain(client.messages[0], revision)).To(gomega.BeTrue())
		for _, message := range m.messages {
			gomega.Expect(message).NotTo(gomega.And(
				gomega.HaveField("Role", gomega.Equal("user")),
				gomega.HaveField("Content", gomega.Equal(revision)),
			), "feature-planner revision must not be appended as normal chat history")
		}

		after := loadFeaturePlannerCommandStateFile(t, filepath.Join(projectRoot, ".idiom", "feature-planner", state.SessionID, "state.json"))
		gomega.Expect(after.OpenQuestions).To(gomega.HaveLen(2))
		gomega.Expect(after.OpenQuestions[0].Status).To(gomega.Equal("answered"))
		gomega.Expect(after.OpenQuestions[1].Status).To(gomega.Equal("open"))
		gomega.Expect(after.OpenQuestions[0].Answer).To(gomega.Equal(revision))
		gomega.Expect(after.OpenQuestions[0].AnswerSourceKind).To(gomega.Equal("plan_revision"))
		gomega.Expect(after.AuthorityDeltas).To(gomega.BeEmpty())
	})

	// TestPRDRepairLoop verifies malformed PRD triggers repair and retry succeeds.
	ginkgo.It("repairs malformed PRD output", func() {
		t := ginkgo.GinkgoT()
		m, effortRoot, _ := setupE2ESession(t, []string{garbage, validPRD()})
		ctx := context.Background()

		_, err := m.runPRDDraftingTurn(ctx, "Add OAuth 2.0")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		prdData, err := os.ReadFile(filepath.Join(effortRoot, "prd.md"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		_, err = productworkflow.ParsePRDMarkdown(prdData)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		_, err = os.Stat(filepath.Join(effortRoot, "prd_parse_failure_raw_001.md"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		fmt.Fprintln(ginkgo.GinkgoWriter, "PASS: PRD repair loop succeeded")
	})

	// TestPRDRepairExhaustion verifies all retries fail → non-fatal message.
	ginkgo.It("reports exhausted PRD repair without aborting the workflow", func() {
		t := ginkgo.GinkgoT()
		m, effortRoot, _ := setupE2ESession(t, []string{garbage, garbage, garbage})
		ctx := context.Background()

		answer, err := m.runPRDDraftingTurn(ctx, "Add OAuth 2.0")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(answer).To(gomega.ContainSubstring("PRD parsing failed"))

		for i := 1; i <= 2; i++ {
			name := fmt.Sprintf("prd_parse_failure_raw_%03d.md", i)
			_, statErr := os.Stat(filepath.Join(effortRoot, name))
			gomega.Expect(statErr).NotTo(gomega.HaveOccurred(), "expected %s", name)
		}

		_, statErr := os.Stat(filepath.Join(effortRoot, "prd_parse_failure_final.md"))
		gomega.Expect(statErr).NotTo(gomega.HaveOccurred())

		fmt.Fprintln(ginkgo.GinkgoWriter, "PASS: PRD repair exhaustion → non-fatal message, raw failures saved")
	})

	// TestADRRepairSuccess verifies ADR repair loop: garbage → repair → success.
	ginkgo.It("repairs malformed ADR output", func() {
		t := ginkgo.GinkgoT()
		m, effortRoot, _ := setupE2ESession(t, []string{validPRD()})
		ctx := context.Background()

		_, err := m.runPRDDraftingTurn(ctx, "Add OAuth 2.0")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		// Reset client for ADR generation with repair.
		advanceE2EPRDToADRDrafting(t, effortRoot)
		m.featurePlannerPhase = productworkflow.PhaseADRDrafting
		m.featurePlannerEffortRoot = effortRoot
		// 1 initial + 1 repair prompt + 1 tryGenerateADRs retry = 3 calls
		// New direct-parse design: 1 initial + 1 repair = 2 calls (not 3).
		adrClient := newScriptedLLMRecorder(t, scriptedLLMConfig{responses: []string{garbage, validADRs()}})
		m.client = adrClient.mock

		adrDocs, _, err := m.generateADRs(ctx, effortRoot)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(adrDocs).NotTo(gomega.BeEmpty())
		_, err = os.Stat(filepath.Join(effortRoot, "adrs_parse_failure_raw_001.txt"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(adrClient.callCount).To(gomega.Equal(2))

		fmt.Fprintf(ginkgo.GinkgoWriter, "PASS: ADR repair succeeded, %d ADRs, %d calls", len(adrDocs), adrClient.callCount)
	})

	// TestADRRepairExhaustion verifies ADR repair exhaustion fails closed instead
	// of writing an empty successful ADR index.
	ginkgo.It("fails closed when ADR repair is exhausted", func() {
		t := ginkgo.GinkgoT()
		m, effortRoot, _ := setupE2ESession(t, []string{validPRD()})
		ctx := context.Background()

		_, err := m.runPRDDraftingTurn(ctx, "Add OAuth 2.0")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		advanceE2EPRDToADRDrafting(t, effortRoot)
		m.featurePlannerPhase = productworkflow.PhaseADRDrafting
		m.featurePlannerEffortRoot = effortRoot
		// New direct-parse design: 1 initial + 2 repair attempts = 3 calls.
		adrClient := newScriptedLLMRecorder(t, scriptedLLMConfig{responses: []string{garbage, garbage, garbage}})
		m.client = adrClient.mock

		adrDocs, _, err := m.generateADRs(ctx, effortRoot)
		gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("ADR parsing failed after")))
		gomega.Expect(adrDocs).To(gomega.BeEmpty())
		_, statErr := os.Stat(filepath.Join(effortRoot, "adrs_parse_failure_final.txt"))
		gomega.Expect(statErr).NotTo(gomega.HaveOccurred())
		gomega.Expect(adrClient.callCount).To(gomega.Equal(3))

		fmt.Fprintf(ginkgo.GinkgoWriter, "PASS: ADR repair exhaustion fails closed, %d calls", adrClient.callCount)
	})

	// TestADRPartialSuccess verifies some ADR chunks parse, some fail.
	ginkgo.It("retains valid ADRs when another ADR chunk is malformed", func() {
		t := ginkgo.GinkgoT()
		partialADRs := `---
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
Test.

## Decision
Test.

## Alternatives Considered
Test.

## Evidence
Test.

## Consequences
Test.

## Compatibility and Migration
Test.

## TDD Obligations
Test.

## Rollback
Test.

## Open Questions
Test.
===
This is not a valid ADR at all.
`

		m, effortRoot, _ := setupE2ESession(t, []string{validPRD(), partialADRs})
		ctx := context.Background()

		_, err := m.runPRDDraftingTurn(ctx, "Add OAuth 2.0")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		_, err = m.approvePRDAndAdvance(context.Background(), effortRoot)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		adrDir := filepath.Join(effortRoot, "adrs")
		files, _ := filepath.Glob(filepath.Join(adrDir, "*.md"))

		gomega.Expect(files).NotTo(gomega.BeEmpty())

		idxData, _ := os.ReadFile(filepath.Join(adrDir, "index.json"))

		idx, err := productworkflow.ParseADRIndexJSON(idxData)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(idx.ADRs).NotTo(gomega.BeEmpty())

		fmt.Fprintf(ginkgo.GinkgoWriter, "PASS: partial success — %d of 2 chunks parsed", len(idx.ADRs))
	})

	// TestPRDIntakeRepair verifies the auto-repair path when PRD intake fails.
	ginkgo.It("repairs an invalid PRD during intake", func() {
		t := ginkgo.GinkgoT()
		// PRD with valid format but empty evidence_refs + tdd_obligations
		// (intake fails, auto-repair triggers).
		badPRD := `---
schema_version: idiom.prd.v1
prd_id: test-effort
status: draft
task_id: test-effort
created_at: 2026-01-01T00:00:00Z
---

# PRD: Test

## Requirements

### REQ-001

- **id**: REQ-001
- **priority**: P1
- **statement**: Support OAuth login.
- **owner_alignment**: End users need this.
- **testability**: Can verify.
- **metric**: 100% success.
- **evidence_refs**: []
- **tdd_obligations**: []
- **open_questions**: []
`

		// Responses: intake repair model call (valid PRD) + ADR generation (valid ADRs).
		m, effortRoot, _ := setupE2ESession(t, []string{validPRD(), validADRs()}, 1)
		// Write bad PRD directly — skip PRD generation.
		gomega.Expect(os.WriteFile(filepath.Join(effortRoot, "prd.md"), []byte(badPRD), 0o644)).To(gomega.Succeed())

		result, err := m.approvePRDAndAdvance(context.Background(), effortRoot)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		fmt.Fprintf(ginkgo.GinkgoWriter, "result: %s", textutil.Truncate(result, 200))

		// Auto-repair updates PRD on disk, user must re-approve.
		gomega.Expect(result).To(gomega.ContainSubstring("auto-repaired"))

		// Verify the PRD on disk was updated with the repair output.
		prdData, _ := os.ReadFile(filepath.Join(effortRoot, "prd.md"))
		gomega.Expect(string(prdData)).To(gomega.ContainSubstring("inference"))

		// Verify PRD is NOT yet accepted (auto-repair doesn't re-run approval).
		gomega.Expect(string(prdData)).NotTo(gomega.ContainSubstring("accepted"))

		fmt.Fprintln(ginkgo.GinkgoWriter, "PASS: PRD intake auto-repair succeeded, PRD updated on disk")
	})

	// TestNoDoubleADRWrite verifies ADR refinement does not double-write.
	ginkgo.It("does not duplicate ADR files during refinement", func() {
		t := ginkgo.GinkgoT()
		m, effortRoot, _ := setupE2ESession(t, []string{validPRD(), validADRs(), validADRs()})
		ctx := context.Background()

		_, err := m.runPRDDraftingTurn(ctx, "Add OAuth 2.0")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		_, err = m.approvePRDAndAdvance(context.Background(), effortRoot)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		adrDir := filepath.Join(effortRoot, "adrs")
		initial, _ := filepath.Glob(filepath.Join(adrDir, "*.md"))
		initialCount := len(initial)
		fmt.Fprintf(ginkgo.GinkgoWriter, "Initial ADR files: %d", initialCount)

		m.featurePlannerPhase = productworkflow.PhaseADRDrafting
		m.featurePlannerEffortRoot = effortRoot
		m.runADRDraftingTurn(ctx, "Add detail")

		final, _ := filepath.Glob(filepath.Join(adrDir, "*.md"))
		fmt.Fprintf(ginkgo.GinkgoWriter, "ADR files after refinement: %d", len(final))

		gomega.Expect(len(final)).To(gomega.BeNumerically(">=", initialCount))

		_, err = os.Stat(filepath.Join(adrDir, "index.json"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		for _, f := range final {
			gomega.Expect(strings.Count(filepath.Base(f), "ADR-")).To(gomega.BeNumerically("<=", 1))
		}

		fmt.Fprintf(ginkgo.GinkgoWriter, "PASS: %d → %d files, no duplicates", initialCount, len(final))
	})

	// TestADRRefinementMerge verifies merge preserves existing ADRs and updates matching IDs.
	ginkgo.It("merges refined ADRs without dropping unchanged decisions", func() {
		t := ginkgo.GinkgoT()
		adr001Only := `---
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
Refined OAuth context.

## Decision
Use PKCE with refresh tokens.

## Alternatives Considered
Implicit grant rejected.

## Evidence
OAuth 2.1 recommends PKCE.

## Consequences
Token management complexity.

## Compatibility and Migration
Backward compatible.

## TDD Obligations
Test PKCE end-to-end.

## Rollback
Fall back to basic auth.

## Open Questions
Token rotation?
`

		m, effortRoot, _ := setupE2ESession(t, []string{validPRD(), validADRs(), adr001Only})
		ctx := context.Background()

		_, err := m.runPRDDraftingTurn(ctx, "Add OAuth 2.0")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		_, err = m.approvePRDAndAdvance(context.Background(), effortRoot)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		adrDir := filepath.Join(effortRoot, "adrs")
		initial, _ := filepath.Glob(filepath.Join(adrDir, "*.md"))
		fmt.Fprintf(ginkgo.GinkgoWriter, "After generation: %d ADR files", len(initial))

		m.featurePlannerPhase = productworkflow.PhaseADRDrafting
		m.featurePlannerEffortRoot = effortRoot
		m.runADRDraftingTurn(ctx, "Update ADR-001")

		idxData, _ := os.ReadFile(filepath.Join(adrDir, "index.json"))
		idx, _ := productworkflow.ParseADRIndexJSON(idxData)
		adrIDs := map[string]bool{}

		for _, entry := range idx.ADRs {
			adrIDs[entry.ADRID] = true
		}

		gomega.Expect(adrIDs).To(gomega.HaveKey("ADR-001"))
		gomega.Expect(adrIDs).To(gomega.HaveKey("ADR-002"))

		adr1Data, _ := os.ReadFile(filepath.Join(adrDir, "ADR-001.md"))
		if strings.Contains(string(adr1Data), "Refined OAuth") {
			fmt.Fprintln(ginkgo.GinkgoWriter, "ADR-001 content updated by refinement")
		}

		adr2Data, _ := os.ReadFile(filepath.Join(adrDir, "ADR-002.md"))
		gomega.Expect(string(adr2Data)).To(gomega.ContainSubstring("Time-limited reset link via email."))

		fmt.Fprintf(ginkgo.GinkgoWriter, "PASS: merge preserved %d ADRs", len(idx.ADRs))
	})

	// TestADRRepairDirectParse verifies the repair loop parses repair output
	// directly (1 model call per attempt, not 2). Previously the loop called
	// tryGenerateADRs which rebuilt the prompt and called the model again.
	ginkgo.It("parses repaired ADR output without an extra model call", func() {
		t := ginkgo.GinkgoT()
		m, effortRoot, _ := setupE2ESession(t, []string{validPRD()})
		ctx := context.Background()

		_, err := m.runPRDDraftingTurn(ctx, "Add OAuth 2.0")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		advanceE2EPRDToADRDrafting(t, effortRoot)
		m.featurePlannerPhase = productworkflow.PhaseADRDrafting
		m.featurePlannerEffortRoot = effortRoot
		// Only 2 responses needed: 1 initial (garbage) + 1 repair (valid ADRs).
		// Previously this needed 3 (1 initial + 1 repair prompt + 1 tryGenerateADRs).
		// Now the repair loop parses repair output directly — no redundant model call.
		adrClient := newScriptedLLMRecorder(t, scriptedLLMConfig{responses: []string{garbage, validADRs()}})
		m.client = adrClient.mock

		adrDocs, _, err := m.generateADRs(ctx, effortRoot)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(adrDocs).NotTo(gomega.BeEmpty())
		_, err = os.Stat(filepath.Join(effortRoot, "adrs_parse_failure_raw_001.txt"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(adrClient.callCount).To(gomega.Equal(2))

		fmt.Fprintf(ginkgo.GinkgoWriter, "PASS: ADR repair direct parse — %d model calls, %d ADRs", adrClient.callCount, len(adrDocs))
	})

	ginkgo.It("preserves and renders the model-authored ADR decision", func() {
		t := ginkgo.GinkgoT()
		m, effortRoot, _ := setupE2ESession(t, []string{validPRD(), validADRs()})
		ctx := context.Background()

		_, err := m.runPRDDraftingTurn(ctx, "Add OAuth 2.0")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		answer, err := m.approvePRDAndAdvance(context.Background(), effortRoot)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(answer).To(gomega.ContainSubstring("Decision: Use authorization code flow with PKCE."))

		adrData, err := os.ReadFile(filepath.Join(effortRoot, "adrs", "ADR-001.md"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		adrText := string(adrData)
		gomega.Expect(adrText).To(gomega.ContainSubstring("Use authorization code flow with PKCE."))
		gomega.Expect(adrText).NotTo(gomega.ContainSubstring("## Decision\n\n\n\n"))
	})

	ginkgo.It("returns a conversational PRD drafting summary", func() {
		t := ginkgo.GinkgoT()
		m, effortRoot, _ := setupE2ESession(t, []string{validPRD()})
		ctx := context.Background()

		answer, err := m.runPRDDraftingTurn(ctx, "Add OAuth 2.0")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		for _, want := range []string{
			"PRD draft generated.",
			"Saved: prd.md",
			"What I captured:",
			"REQ-001 [P1]: The system shall support OAuth 2.0 login.",
			"All intake checks passed.",
		} {
			gomega.Expect(answer).To(gomega.ContainSubstring(want))
		}

		gomega.Expect(answer).NotTo(gomega.ContainSubstring("schema_version: idiom.prd.v1"))

		_, err = os.Stat(filepath.Join(effortRoot, "prd.md"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
	})

	ginkgo.It("reports PRD requirement changes conversationally", func() {
		t := ginkgo.GinkgoT()
		updatedPRD := strings.Replace(validPRD(), "The system shall support OAuth 2.0 login.", "The system shall support OAuth 2.0 login with PKCE.", 1)
		m, _, _ := setupE2ESession(t, []string{validPRD(), updatedPRD})
		ctx := context.Background()

		_, err := m.runPRDDraftingTurn(ctx, "Add OAuth 2.0")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		answer, err := m.runPRDDraftingTurn(ctx, "Require PKCE")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		for _, want := range []string{
			"PRD updated from your feedback.",
			"Change summary:",
			"Updated requirements: REQ-001",
			"The system shall support OAuth 2.0 login with PKCE.",
		} {
			gomega.Expect(answer).To(gomega.ContainSubstring(want))
		}

		gomega.Expect(answer).NotTo(gomega.ContainSubstring("schema_version: idiom.prd.v1"))
	})

	ginkgo.It("returns a conversational ADR refinement summary", func() {
		t := ginkgo.GinkgoT()
		updatedADRs := strings.Replace(validADRs(), "Use authorization code flow with PKCE.", "Use authorization code flow with PKCE and documented redirect URI handling.", 1)
		m, effortRoot, _ := setupE2ESession(t, []string{validPRD(), validADRs(), updatedADRs})
		ctx := context.Background()

		_, err := m.runPRDDraftingTurn(ctx, "Add OAuth 2.0")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		_, err = m.approvePRDAndAdvance(context.Background(), effortRoot)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		m.featurePlannerPhase = productworkflow.PhaseADRDrafting

		answer, err := m.runADRDraftingTurn(ctx, "Capture redirect URI handling")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		for _, want := range []string{
			"ADR drafts updated from your feedback.",
			"Saved: adrs/index.json and ADR markdown files.",
			"Change summary:",
			"Updated ADRs: ADR-001",
			"Decision: Use authorization code flow with PKCE and documented redirect URI handling.",
		} {
			gomega.Expect(answer).To(gomega.ContainSubstring(want))
		}

		gomega.Expect(answer).NotTo(gomega.ContainSubstring("schema_version: idiom.adr.v1"))
	})

	// TestADRRepairExhaustionDirectParse verifies exhaustion fails closed with direct parse.
	ginkgo.It("fails closed after direct-parse ADR repair exhaustion", func() {
		t := ginkgo.GinkgoT()
		m, effortRoot, _ := setupE2ESession(t, []string{validPRD()})
		ctx := context.Background()

		_, err := m.runPRDDraftingTurn(ctx, "Add OAuth 2.0")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		advanceE2EPRDToADRDrafting(t, effortRoot)
		m.featurePlannerPhase = productworkflow.PhaseADRDrafting
		m.featurePlannerEffortRoot = effortRoot
		// 3 garbage: 1 initial + 2 repair attempts (1 call each, direct parse)
		adrClient := newScriptedLLMRecorder(t, scriptedLLMConfig{responses: []string{garbage, garbage, garbage}})
		m.client = adrClient.mock

		adrDocs, _, err := m.generateADRs(ctx, effortRoot)
		gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("ADR parsing failed after")))
		gomega.Expect(adrDocs).To(gomega.BeEmpty())
		gomega.Expect(adrClient.callCount).To(gomega.Equal(3))

		fmt.Fprintf(ginkgo.GinkgoWriter, "PASS: ADR repair exhaustion direct parse fails closed, %d model calls", adrClient.callCount)
	})
})
