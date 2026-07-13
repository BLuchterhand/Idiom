package productworkflow

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
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

func advanceTestEffortToADRDrafting(root string) {
	ginkgo.GinkgoHelper()

	intake := persistTestPRDDraft(root, validPRDDoc())
	gomega.Expect(intake.Passed).To(gomega.BeTrue(), "failures=%+v", intake.Failures)

	result, err := SubmitPRDForReview(root)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(result.CurrentPhase).To(gomega.Equal(PhasePRDReview))
	result, intake, err = ApprovePRDGate(root)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(intake.Passed).To(gomega.BeTrue(), "failures=%+v", intake.Failures)
	gomega.Expect(result.CurrentPhase).To(gomega.Equal(PhaseADRDrafting))
}

func persistTestPRDDraft(root string, prd PRDDocument) IntakeResult {
	ginkgo.GinkgoHelper()

	context, err := LoadPRDDraftContext(root)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	prd.FrontMatter.PRDID = context.Identity.PRDID
	prd.FrontMatter.TaskID = context.Identity.TaskID
	prd.FrontMatter.CreatedAt = context.Identity.CreatedAt
	intake, err := PersistPRDDraft(PRDDraftPersistenceRequest{
		EffortRoot:         root,
		Document:           prd,
		ExpectedGeneration: context.Generation,
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	return intake
}

func persistInitialTestADRDrafts(root string, documents []ADRDocument, entries []ADRIndexEntry) ADRDraftPersistenceResult {
	ginkgo.GinkgoHelper()

	context, err := LoadADRGenerationContext(root)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	result, err := PersistADRDrafts(ADRDraftPersistenceRequest{
		EffortRoot:         root,
		Documents:          documents,
		IndexEntries:       entries,
		ExpectedGeneration: context.Generation,
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	return result
}

var _ = ginkgo.Describe("create", func() {
	ginkgo.It("InitializeEffortCreatesNew", func() {
		dir := ginkgo.GinkgoT().TempDir()
		root := filepath.Join(dir, "my-feature")

		result, err := InitializeEffort(CreateEffortOptions{EffortRoot: root})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("InitializeEffort: %v", err))
		gomega.Expect(result.Status != "ok").To(gomega.BeFalse(), fmt.Sprintf("expected status ok, got %s", result.Status))
		gomega.Expect(result.CurrentPhase != PhasePRDDrafting).To(gomega.BeFalse(), fmt.Sprintf("expected prd_drafting, got %s", result.CurrentPhase))

		for _, sub := range []string{"adrs", "evidence", "briefs", "reports"} {
			info, err := os.Stat(filepath.Join(root, sub))
			gomega.Expect(err).To(gomega.BeNil(), fmt.Sprintf("expected dir %s", sub))
			gomega.Expect(info.IsDir()).To(gomega.BeTrue(), fmt.Sprintf("expected dir %s", sub))

		}

		ws, err := LoadWorkflowState(root)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("LoadWorkflowState: %v", err))
		gomega.Expect(ws.CurrentPhase != string(PhasePRDDrafting)).To(gomega.BeFalse(), fmt.Sprintf("workflow phase: %s", ws.CurrentPhase))

	})
	ginkgo.It("InitializeEffortCustomIDAndTitle", func() {
		dir := ginkgo.GinkgoT().TempDir()
		root := filepath.Join(dir, "custom-feature")

		result, err := InitializeEffort(CreateEffortOptions{
			EffortRoot: root, EffortID: "EFF-123", Title: "My Custom Feature",
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("InitializeEffort: %v", err))
		gomega.Expect(result.EffortID != "EFF-123").To(gomega.BeFalse(), fmt.Sprintf("expected EFF-123, got %s", result.EffortID))

		ws, _ := LoadWorkflowState(root)
		gomega.Expect(ws.Title != "My Custom Feature").To(gomega.BeFalse(), fmt.Sprintf("expected 'My Custom Feature', got %s", ws.Title))

	})
	ginkgo.It("InitializeEffortProjectsFreshCreationActions", func() {
		root := filepath.Join(ginkgo.GinkgoT().TempDir(), "fresh-actions")

		_, err := InitializeEffort(CreateEffortOptions{EffortRoot: root, EffortID: "fresh-actions"})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		workflowState, err := LoadWorkflowState(root)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(workflowState.NextValidActions).To(gomega.Equal([]string{"approve_prd", "edit_requirements"}))

		status, err := LoadProductStatus(root, &LintSummaryResult{
			Status:       ProductWorkflowStatusPass,
			CheckedFiles: []string{WorkflowStateFileName},
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		view := BuildProductOperatorStatusView(status, &workflowState)
		gomega.Expect(view.NextValidActions).To(gomega.ContainElements(
			gomega.And(gomega.HaveField("Action", "approve_prd"), gomega.HaveField("Owner", ProductWorkflowActionOwnerOperator)),
			gomega.And(gomega.HaveField("Action", "edit_requirements"), gomega.HaveField("Owner", ProductWorkflowActionOwnerOperator)),
		))
		for _, blocker := range view.Blockers {
			gomega.Expect(blocker.Code).NotTo(gomega.Equal("invalid_creation_action"))
		}
	})
	ginkgo.It("InitializeEffortResumesWithCustomEventLedgerAuthority", func() {
		root := filepath.Join(ginkgo.GinkgoT().TempDir(), "custom-events")

		_, err := InitializeEffort(CreateEffortOptions{EffortRoot: root, EffortID: "custom-events"})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		workflowState, err := LoadWorkflowState(root)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		workflowState.ArtifactPaths["events"] = "audit/product-events.jsonl"
		gomega.Expect(os.MkdirAll(filepath.Join(root, "audit"), 0o755)).To(gomega.Succeed())
		gomega.Expect(os.Rename(
			filepath.Join(root, productWorkflowDefaultEventsPath),
			filepath.Join(root, "audit", "product-events.jsonl"),
		)).To(gomega.Succeed())
		body, err := json.MarshalIndent(workflowState, "", "  ")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(os.WriteFile(filepath.Join(root, WorkflowStateFileName), append(body, '\n'), 0o644)).To(gomega.Succeed())

		resumed, err := InitializeEffort(CreateEffortOptions{EffortRoot: root})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(resumed.Status).To(gomega.Equal(ProductWorkflowStatusResumed))
		gomega.Expect(resumed.CurrentPhase).To(gomega.Equal(PhasePRDDrafting))
		_, err = os.Stat(filepath.Join(root, "audit", "product-events.jsonl"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		_, err = os.Stat(filepath.Join(root, productWorkflowDefaultEventsPath))
		gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue())
	})
	ginkgo.It("InitializeEffortRejectsMissingConfiguredEventLedger", func() {
		root := filepath.Join(ginkgo.GinkgoT().TempDir(), "missing-events")

		_, err := InitializeEffort(CreateEffortOptions{EffortRoot: root, EffortID: "missing-events"})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(os.Remove(filepath.Join(root, productWorkflowDefaultEventsPath))).To(gomega.Succeed())

		_, err = InitializeEffort(CreateEffortOptions{EffortRoot: root})
		gomega.Expect(err).To(gomega.HaveOccurred())
		gomega.Expect(err.Error()).To(gomega.ContainSubstring("existing event ledger events.jsonl is required"))
		_, statErr := os.Stat(filepath.Join(root, productWorkflowDefaultEventsPath))
		gomega.Expect(os.IsNotExist(statErr)).To(gomega.BeTrue())
	})
	ginkgo.It("ProductWorkflowMutationLeaseDetectsEffortRootSwap", func() {
		parent := ginkgo.GinkgoT().TempDir()
		root := filepath.Join(parent, "swapped")
		movedRoot := filepath.Join(parent, "swapped-original")

		_, err := InitializeEffort(CreateEffortOptions{EffortRoot: root, EffortID: "swapped"})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		lease, err := openProductWorkflowMutationLease(root)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		ginkgo.DeferCleanup(lease.Close)

		gomega.Expect(os.Rename(root, movedRoot)).To(gomega.Succeed())
		gomega.Expect(os.Mkdir(root, 0o755)).To(gomega.Succeed())
		gomega.Expect(lease.ValidatePathIdentity()).To(gomega.MatchError(gomega.ContainSubstring("no longer names the locked effort root")))

		_, err = os.Stat(filepath.Join(movedRoot, WorkflowStateFileName))
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		_, err = os.Stat(filepath.Join(root, WorkflowStateFileName))
		gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue())
	})
	ginkgo.It("InitializeEffortResumes", func() {
		dir := ginkgo.GinkgoT().TempDir()
		root := filepath.Join(dir, "existing")

		_, err := InitializeEffort(CreateEffortOptions{EffortRoot: root})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("first init: %v", err))

		_, err = SubmitPRDForReview(root)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("SubmitPRDForReview: %v", err))

		result, err := InitializeEffort(CreateEffortOptions{EffortRoot: root})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("resume init: %v", err))
		gomega.Expect(result.Status != ProductWorkflowStatusResumed).To(gomega.BeFalse(), fmt.Sprintf("expected resumed, got %s", result.Status))
		gomega.Expect(result.CurrentPhase != PhasePRDReview).To(gomega.BeFalse(), fmt.Sprintf("expected prd_review, got %s", result.CurrentPhase))

	})
	ginkgo.It("CreationPhaseOwnersBlockOutOfOrderSubmission", func() {
		dir := ginkgo.GinkgoT().TempDir()
		root := filepath.Join(dir, "out-of-order")

		_, err := InitializeEffort(CreateEffortOptions{EffortRoot: root})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("init: %v", err))

		result, err := SubmitADRsForReview(root)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("unexpected error: %v", err))
		gomega.Expect(result.Status != "blocked").To(gomega.BeFalse(), fmt.Sprintf("expected blocked for out-of-order transition, got %s", result.Status))
		gomega.Expect(result.CurrentPhase).To(gomega.Equal(PhasePRDDrafting))
		gomega.Expect(result.Blockers).To(gomega.ContainElement(gomega.ContainSubstring("cannot transition")))

	})
	// --- PRD gate tests ---
	ginkgo.It("ApprovePRDGateSuccess", func() {
		dir := ginkgo.GinkgoT().TempDir()
		root := filepath.Join(dir, "prd-gate")

		_, err := InitializeEffort(CreateEffortOptions{EffortRoot: root})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("init: %v", err))
		intake := persistTestPRDDraft(root, validPRDDoc())
		gomega.Expect(intake.Passed).To(gomega.BeTrue(), fmt.Sprintf("intake should pass: %+v", intake.Failures))

		_, err = SubmitPRDForReview(root)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("advance to review: %v", err))

		result, intake, err := ApprovePRDGate(root)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("ApprovePRDGate: %v", err))
		gomega.Expect(!intake.Passed).To(gomega.BeFalse(), fmt.Sprintf("intake should pass: %+v", intake.Failures))
		gomega.Expect(result.Status != "ok").To(gomega.BeFalse(), fmt.Sprintf("expected ok, got %s: %v", result.Status, result.Blockers))
		gomega.Expect(result.CurrentPhase != PhaseADRDrafting).To(gomega.BeFalse(), fmt.Sprintf("expected adr_drafting, got %s", result.CurrentPhase))

	})
	ginkgo.It("ApprovePRDGateWrongPhase", func() {
		dir := ginkgo.GinkgoT().TempDir()
		root := filepath.Join(dir, "prd-wrong-phase")

		_, err := InitializeEffort(CreateEffortOptions{EffortRoot: root})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("init: %v", err))

		result, _, err := ApprovePRDGate(root)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("unexpected error: %v", err))
		gomega.Expect(result.Status != "blocked").To(gomega.BeFalse(), fmt.Sprintf("expected blocked, got %s", result.Status))

	})
	ginkgo.It("ApprovePRDGateMissingFile", func() {
		dir := ginkgo.GinkgoT().TempDir()
		root := filepath.Join(dir, "prd-missing-file")

		_, err := InitializeEffort(CreateEffortOptions{EffortRoot: root})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("init: %v", err))

		_, err = SubmitPRDForReview(root)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("advance: %v", err))

		result, _, err := ApprovePRDGate(root)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("unexpected error: %v", err))
		gomega.Expect(result.Status != "blocked").To(gomega.BeFalse(), fmt.Sprintf("expected blocked, got %s", result.Status))

	})
	ginkgo.It("PersistPRDDraftRejectsInvalidDocumentBeforeMutation", func() {
		dir := ginkgo.GinkgoT().TempDir()
		root := filepath.Join(dir, "prd-intake-fail")

		_, err := InitializeEffort(CreateEffortOptions{EffortRoot: root})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("init: %v", err))

		// Write PRD with no requirements.
		emptyPRD := PRDDocument{
			FrontMatter: PRDFrontMatter{
				SchemaVersion: "idiom.prd.v1", PRDID: "PRD-001", Status: string(ArtifactStateDraft),
				TaskID: "task-001", CreatedAt: "2026-06-17T00:00:00Z",
			},
		}
		context, err := LoadPRDDraftContext(root)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		emptyPRD.FrontMatter.PRDID = context.Identity.PRDID
		emptyPRD.FrontMatter.TaskID = context.Identity.TaskID
		emptyPRD.FrontMatter.CreatedAt = context.Identity.CreatedAt
		_, err = PersistPRDDraft(PRDDraftPersistenceRequest{
			EffortRoot:         root,
			Document:           emptyPRD,
			ExpectedGeneration: context.Generation,
		})
		gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("PRD has no requirements")))

		_, statErr := os.Stat(filepath.Join(root, "prd.md"))
		gomega.Expect(os.IsNotExist(statErr)).To(gomega.BeTrue())
		workflowState, loadErr := LoadWorkflowState(root)
		gomega.Expect(loadErr).NotTo(gomega.HaveOccurred())
		gomega.Expect(workflowState.CurrentPhase).To(gomega.Equal(string(PhasePRDDrafting)))

	})
	// --- ADR gate tests ---
	ginkgo.It("ApproveADRGateSuccess", func() {
		dir := ginkgo.GinkgoT().TempDir()
		root := filepath.Join(dir, "adr-gate")

		_, err := InitializeEffort(CreateEffortOptions{EffortRoot: root})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("init: %v", err))

		advanceTestEffortToADRDrafting(root)

		entries := []ADRIndexEntry{
			{ADRID: "ADR-001", Title: "Choose DB", Status: string(ArtifactStateDraft),
				Path: "ADR-001.md", CoverageTier: "unit", PRDRefs: []string{"REQ-001"},
				CreatedAt: "2026-06-17T00:00:00Z"},
			{ADRID: "ADR-002", Title: "Auth Strategy", Status: string(ArtifactStateDraft),
				Path: "ADR-002.md", CoverageTier: "unit", PRDRefs: []string{"REQ-001"},
				CreatedAt: "2026-06-17T00:00:00Z"},
		}
		adr2 := validADRDoc()
		adr2.FrontMatter.ADRID = "ADR-002"
		persistInitialTestADRDrafts(root, []ADRDocument{validADRDoc(), adr2}, entries)

		_, err = SubmitADRsForReview(root)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("advance to adr_review: %v", err))

		result, intakes, err := ApproveADRGate(root)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("ApproveADRGate: %v", err))
		gomega.Expect(result.Status != "ok").To(gomega.BeFalse(), fmt.Sprintf("expected ok, got %s: %v", result.Status, result.Blockers))
		gomega.Expect(result.CurrentPhase != PhaseTDDSynthesis).To(gomega.BeFalse(), fmt.Sprintf("expected tdd_synthesis, got %s", result.CurrentPhase))
		gomega.Expect(len(intakes) < 3).To(gomega.BeFalse(), fmt.Sprintf("expected at least 3 intake results (index + 2 ADRs), got %d", len(intakes)))

		for i, intake := range intakes {
			gomega.Expect(!intake.Passed).To(gomega.BeFalse(), fmt.Sprintf("intake[%d] should pass: %+v", i, intake.Failures))

		}

		// Verify index entries were marked accepted.
		idxData, _ := os.ReadFile(filepath.Join(root, "adrs", "index.json"))
		writtenIdx, _ := ParseADRIndexJSON(idxData)

		for _, entry := range writtenIdx.ADRs {
			gomega.Expect(entry.Status != string(ArtifactStateAccepted)).To(gomega.BeFalse(), fmt.Sprintf("ADR index entry %s has status %s, expected accepted", entry.ADRID, entry.Status))

		}
	})
	ginkgo.It("ApproveADRGateMissingIndex", func() {
		dir := ginkgo.GinkgoT().TempDir()
		root := filepath.Join(dir, "adr-missing-idx")

		_, err := InitializeEffort(CreateEffortOptions{EffortRoot: root})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("init: %v", err))

		advanceTestEffortToADRDrafting(root)
		_, err = SubmitADRsForReview(root)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("advance: %v", err))

		result, _, err := ApproveADRGate(root)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("unexpected error: %v", err))
		gomega.Expect(result.Status != "blocked").To(gomega.BeFalse(), fmt.Sprintf("expected blocked, got %s", result.Status))

	})
	ginkgo.It("ApproveADRGatePartialValidationNoWrite", func() {
		dir := ginkgo.GinkgoT().TempDir()
		root := filepath.Join(dir, "adr-partial")

		_, err := InitializeEffort(CreateEffortOptions{EffortRoot: root})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("init: %v", err))

		advanceTestEffortToADRDrafting(root)

		// Persist both drafts through the real lifecycle owner before corrupting
		// ADR-002 on disk. The malformed file is intentional: this spec exercises
		// the review gate's all-or-nothing acceptance behavior for externally
		// damaged artifacts, not draft-persistence validation.
		badDoc := validADRDoc()
		badDoc.FrontMatter.ADRID = "ADR-002"
		persistInitialTestADRDrafts(root, []ADRDocument{validADRDoc(), badDoc}, []ADRIndexEntry{
			{ADRID: "ADR-001", Title: "Good", Status: string(ArtifactStateDraft), Path: "ADR-001.md", CoverageTier: "unit", PRDRefs: []string{"REQ-001"}, CreatedAt: "2026-06-17T00:00:00Z"},
			{ADRID: "ADR-002", Title: "Bad", Status: string(ArtifactStateDraft), Path: "ADR-002.md", CoverageTier: "unit", PRDRefs: []string{"REQ-001"}, CreatedAt: "2026-06-17T00:00:00Z"},
		})

		badDoc.FrontMatter.EvidenceRefs = nil
		err = os.WriteFile(filepath.Join(root, "adrs", "ADR-002.md"), []byte(RenderADRMarkdown(badDoc)), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("corrupt ADR-002 fixture: %v", err))

		_, err = SubmitADRsForReview(root)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("advance to adr_review: %v", err))

		result, intakes, err := ApproveADRGate(root)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("ApproveADRGate: %v", err))
		gomega.Expect(result.Status != "blocked").To(gomega.BeFalse(), fmt.Sprintf("expected blocked due to bad ADR, got %s", result.Status))

		_ = intakes

		// ADR-001 must NOT have been rewritten as accepted (partial-commit guard).
		adr1Data, _ := os.ReadFile(filepath.Join(root, "adrs", "ADR-001.md"))
		adr1Parsed, _ := ParseADRMarkdown(adr1Data)
		gomega.Expect(adr1Parsed.FrontMatter.Status == string(ArtifactStateAccepted)).To(gomega.BeFalse(), "ADR-001 was prematurely marked accepted despite ADR-002 failure")

	})
	// --- CompleteEffort tests ---
	ginkgo.It("CompleteEffortWrongPhase", func() {
		dir := ginkgo.GinkgoT().TempDir()
		root := filepath.Join(dir, "complete-wrong")

		_, err := InitializeEffort(CreateEffortOptions{EffortRoot: root})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("init: %v", err))

		result, err := CompleteEffort(root)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("unexpected error: %v", err))
		gomega.Expect(result.Status != "blocked").To(gomega.BeFalse(), fmt.Sprintf("expected blocked, got %s", result.Status))

	})
	// --- Render round-trip tests ---
	ginkgo.It("RenderPRDMarkdownRoundTrip", func() {
		doc := validPRDDoc()
		markdown := RenderPRDMarkdown(doc)

		parsed, err := ParsePRDMarkdown([]byte(markdown))
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("parse round-trip: %v", err))
		gomega.Expect(parsed.FrontMatter.PRDID != doc.FrontMatter.PRDID).To(gomega.BeFalse(), fmt.Sprintf("PRDID mismatch: %s vs %s", parsed.FrontMatter.PRDID, doc.FrontMatter.PRDID))
		gomega.Expect(len(parsed.Requirements) != 1).To(gomega.BeFalse(), fmt.Sprintf("expected 1 requirement, got %d", len(parsed.Requirements)))

	})
	ginkgo.It("RenderADRMarkdownHasAllSections", func() {
		doc := validADRDoc()
		markdown := RenderADRMarkdown(doc)

		for _, section := range []string{"## Context", "## Decision", "## Alternatives Considered",
			"## Evidence", "## Consequences", "## Compatibility and Migration",
			"## TDD Obligations", "## Rollback", "## Open Questions"} {
			gomega.Expect(!strContains(markdown, section)).To(gomega.BeFalse(), fmt.Sprintf("missing section: %s", section))

		}
	})
	ginkgo.It("RenderADRMarkdownValidFrontMatter", func() {
		doc := validADRDoc()
		markdown := RenderADRMarkdown(doc)

		parsed, err := ParseADRMarkdown([]byte(markdown))
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("parse ADR: %v", err))
		gomega.Expect(parsed.FrontMatter.ADRID != "ADR-001").To(gomega.BeFalse(), fmt.Sprintf("ADRID mismatch: %s", parsed.FrontMatter.ADRID))

	})
	ginkgo.It("PersistADRDraftsPreservesParsedBodyContent", func() {
		root := ginkgo.GinkgoT().TempDir()
		_, err := InitializeEffort(CreateEffortOptions{EffortRoot: root, EffortID: "adr-body-roundtrip"})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		advanceTestEffortToADRDrafting(root)
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
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("ParseADRMarkdown: %v", err))
		persistInitialTestADRDrafts(root, []ADRDocument{doc}, []ADRIndexEntry{{
			ADRID: "ADR-001", Title: "History evidence", Status: string(ArtifactStateDraft),
			Path: "ADR-001.md", CoverageTier: "unit", PRDRefs: []string{"REQ-001"},
		}})

		written, err := os.ReadFile(filepath.Join(root, "adrs", "ADR-001.md"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read written ADR: %v", err))

		text := string(written)
		gomega.Expect(!strContains(text, "Require git history lookup evidence before modifying any identified file.")).To(gomega.BeFalse(), fmt.Sprintf("expected decision body to be preserved, got:\n%s", text))
		gomega.Expect(strContains(text, "## Decision\n\n\n\n")).To(gomega.BeFalse(), fmt.Sprintf("ADR body was replaced with empty template:\n%s", text))

	})
})

func strContains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}

	return false
}
