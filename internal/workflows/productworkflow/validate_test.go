package productworkflow

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

const (
	productWorkflowFixtureIndexPath = "testdata/product-workflows/efforts-index.json"
	productWorkflowFixtureRoot      = "testdata/product-workflows/prd-adr-brief-workflow"
)

func productWorkflowFixturePath(parts ...string) string {
	ginkgo.GinkgoHelper()

	pathParts := append([]string{repoRoot(), productWorkflowFixtureRoot}, parts...)
	return filepath.Join(pathParts...)
}

var _ = ginkgo.Describe("validate", func() {
	ginkgo.It("ParseCommittedPhase0Fixtures", func() {
		root := repoRoot()
		read := func(path string) []byte {
			ginkgo.GinkgoHelper()

			data, err := os.ReadFile(filepath.Join(root, path))
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read %s: %v", path, err))

			return data
		}
		{

			_, err := ParseEffortIndexJSON(read(productWorkflowFixtureIndexPath))
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("parse effort index: %v", err))
		}
		{

			_, err := ParseWorkflowStateJSON(read(filepath.Join(productWorkflowFixtureRoot, "workflow_state.json")))
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("parse workflow state: %v", err))
		}
		{

			_, err := ParseTDDLedgerJSON(read(filepath.Join(productWorkflowFixtureRoot, "tdd.json")))
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("parse tdd ledger: %v", err))
		}
		{

			_, err := ParseEvidenceCardsJSONL(read(filepath.Join(productWorkflowFixtureRoot, "evidence/evidence.jsonl")))
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("parse evidence JSONL: %v", err))
		}
		{

			_, err := ParseTraceabilityMatrixJSON(read(filepath.Join(productWorkflowFixtureRoot, "evidence/traceability_matrix.json")))
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("parse traceability matrix: %v", err))
		}
		{

			_, err := ParseImplementationLedgerJSONL(read(filepath.Join(productWorkflowFixtureRoot, "implementation_ledger.jsonl")))
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("parse implementation ledger: %v", err))
		}

	})
	ginkgo.It("ParseEffortIndexRejectsInvalidLifecycleFields", func() {
		base := EffortIndex{
			SchemaVersion: "idiom.product_workflow.effort_index.v1",
			UpdatedAt:     "2026-05-18T21:40:00Z",
			Efforts: []EffortIndexEntry{{
				EffortID:     "eff-1",
				Title:        "Test effort",
				StatusBucket: WorkflowStatusBucketStarted,
				CurrentPhase: "phase-5",
				CurrentState: WorkflowStateImplementing,
				FolderPath:   "started/eff-1",
				TaskID:       "task-1",
			}},
		}

		tests := []struct {
			name    string
			mutate  func(*EffortIndex)
			wantErr string
		}{
			{
				name: "invalid bucket",
				mutate: func(idx *EffortIndex) {
					idx.Efforts[0].StatusBucket = "later"
				},
				wantErr: "invalid status_bucket",
			},
			{
				name: "invalid state",
				mutate: func(idx *EffortIndex) {
					idx.Efforts[0].CurrentState = "eventually_done"
				},
				wantErr: "invalid current_state",
			},
			{
				name: "bucket state mismatch",
				mutate: func(idx *EffortIndex) {
					idx.Efforts[0].StatusBucket = WorkflowStatusBucketClosed
					idx.Efforts[0].CurrentState = WorkflowStateImplementing
				},
				wantErr: "invalid_state_bucket",
			},
		}

		for _, tt := range tests {
			ginkgo.By(tt.name, func() {
				idx := base
				idx.Efforts = append([]EffortIndexEntry(nil), base.Efforts...)
				tt.mutate(&idx)

				body, err := json.Marshal(idx)
				gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal index: %v", err))

				_, err = ParseEffortIndexJSON(body)
				gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("ParseEffortIndexJSON error = %v, want %q", err, tt.wantErr))
				gomega.Expect(err.Error()).To(gomega.ContainSubstring(tt.wantErr), fmt.Sprintf("ParseEffortIndexJSON error = %v, want %q", err, tt.wantErr))

			})
		}
	})
	ginkgo.It("ParsePRDFixturePasses", func() {
		data, err := os.ReadFile(productWorkflowFixturePath("prd.md"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read prd.md: %v", err))

		doc, err := ParsePRDMarkdown(data)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("parse PRD fixture: %v", err))
		gomega.Expect(doc.FrontMatter.PRDID != "prd-impl-0001-phase-0").To(gomega.BeFalse(), fmt.Sprintf("expected prd_id prd-impl-0001-phase-0, got %q", doc.FrontMatter.PRDID))
		gomega.Expect(doc.FrontMatter.AcceptedAt).NotTo(gomega.BeEmpty(), fmt.Sprintf("expected accepted PRD fixture to include acceptance metadata, got %#v", doc.FrontMatter))
		gomega.Expect(doc.FrontMatter.AcceptedBy).NotTo(gomega.BeEmpty(), fmt.Sprintf("expected accepted PRD fixture to include acceptance metadata, got %#v", doc.FrontMatter))
		gomega.Expect(doc.FrontMatter.AcceptanceProfile).NotTo(gomega.BeEmpty(), fmt.Sprintf("expected accepted PRD fixture to include acceptance metadata, got %#v", doc.FrontMatter))
		gomega.Expect(len(doc.Requirements) == 0).To(gomega.BeFalse(), fmt.Sprint("expected at least one requirement"))

		req := doc.Requirements[0]
		gomega.Expect(req.ID != "REQ-IMPL-0001").To(gomega.BeFalse(), fmt.Sprintf("expected requirement ID REQ-IMPL-0001, got %q", req.ID))
		gomega.Expect(len(req.EvidenceRefs) == 0).To(gomega.BeFalse(), fmt.Sprint("expected evidence_refs on requirement"))
		gomega.Expect(len(req.TDDObligations) == 0).To(gomega.BeFalse(), fmt.Sprint("expected tdd_obligations on requirement"))

	})
	ginkgo.It("ParseADRIndexFixturePasses", func() {
		data, err := os.ReadFile(productWorkflowFixturePath("adrs", "index.json"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read ADR index: %v", err))

		idx, err := ParseADRIndexJSON(data)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("parse ADR index fixture: %v", err))
		gomega.Expect(len(idx.ADRs) == 0).To(gomega.BeFalse(), fmt.Sprint("expected at least one ADR entry"))
		gomega.Expect(idx.ADRs[0].ADRID != "ADR-0001").To(gomega.BeFalse(), fmt.Sprintf("expected ADR-0001, got %q", idx.ADRs[0].ADRID))

	})
	ginkgo.It("ParseADRMarkdownFixturePasses", func() {
		data, err := os.ReadFile(productWorkflowFixturePath("adrs", "ADR-0001-durable-workflow-substrate.md"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read ADR markdown: %v", err))

		doc, err := ParseADRMarkdown(data)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("parse ADR fixture: %v", err))
		gomega.Expect(doc.FrontMatter.ADRID != "ADR-0001").To(gomega.BeFalse(), fmt.Sprintf("expected adr_id ADR-0001, got %q", doc.FrontMatter.ADRID))
		gomega.Expect(!doc.Sections.HasDecision).To(gomega.BeFalse(), fmt.Sprint("expected HasDecision true"))
		gomega.Expect(len(doc.FrontMatter.PRDRefs) == 0).To(gomega.BeFalse(), fmt.Sprint("expected prd_refs in ADR front matter"))

	})
	ginkgo.It("InvalidPRDMissingEvidenceFailsClosed", func() {
		prd := `---
schema_version: idiom.prd.v1
prd_id: prd-test
status: draft
task_id: task-test
created_at: 2026-05-17T00:00:00Z
---

# PRD: Test

## Requirements

### REQ-TEST-001

- **id**: REQ-TEST-001
- **priority**: must
- **statement**: Test requirement
- **evidence_refs**: []
- **tdd_obligations**: []
`

		_, err := ParsePRDMarkdown([]byte(prd))
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected PRD with empty evidence_refs to fail"))
		gomega.Expect(!strings.Contains(err.Error(), "missing evidence_refs")).To(gomega.BeFalse(), fmt.Sprintf("expected evidence_refs error, got %v", err))
		gomega.Expect(!strings.Contains(err.Error(), "missing tdd_obligations")).To(gomega.BeFalse(), fmt.Sprintf("expected tdd_obligations error, got %v", err))

	})
	ginkgo.It("InvalidPRDNoRequirementsFailsClosed", func() {
		prd := `---
schema_version: idiom.prd.v1
prd_id: prd-test
status: draft
task_id: task-test
created_at: 2026-05-17T00:00:00Z
---

# PRD: Test
`

		_, err := ParsePRDMarkdown([]byte(prd))
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected PRD with no requirements to fail"))
		gomega.Expect(!strings.Contains(err.Error(), "no requirements")).To(gomega.BeFalse(), fmt.Sprintf("expected no requirements error, got %v", err))

	})
	ginkgo.It("InvalidPRDMissingFrontMatterFailsClosed", func() {
		prd := `---
prd_id: prd-test
status: draft
task_id: task-test
---

# PRD: Test
`

		_, err := ParsePRDMarkdown([]byte(prd))
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected PRD missing schema_version/created_at to fail"))
		gomega.Expect(!strings.Contains(err.Error(), "missing schema_version")).To(gomega.BeFalse(), fmt.Sprintf("expected schema_version error, got %v", err))
		gomega.Expect(!strings.Contains(err.Error(), "missing created_at")).To(gomega.BeFalse(), fmt.Sprintf("expected created_at error, got %v", err))

	})
	ginkgo.It("InvalidADRMissingDecisionFailsClosed", func() {
		adr := `---
schema_version: idiom.adr.v1
adr_id: ADR-TEST
status: accepted
prd_refs:
  - REQ-TEST-001
evidence_refs:
  - EV-001
tdd_obligations:
  - TDD-001
coverage_tier: unit
created_at: 2026-05-17T00:00:00Z
---

# ADR-TEST: Test

## Context

Test.
`

		_, err := ParseADRMarkdown([]byte(adr))
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected ADR missing Decision section to fail"))
		gomega.Expect(!strings.Contains(err.Error(), "missing ## Decision section")).To(gomega.BeFalse(), fmt.Sprintf("expected Decision section error, got %v", err))

	})
	ginkgo.It("InvalidADRMissingStatusFailsClosed", func() {
		adr := `---
schema_version: idiom.adr.v1
adr_id: ADR-TEST
prd_refs:
  - REQ-TEST-001
evidence_refs:
  - EV-001
tdd_obligations:
  - TDD-001
coverage_tier: unit
---

# ADR-TEST: Test

## Context

Test.

## Decision

Test.
`

		_, err := ParseADRMarkdown([]byte(adr))
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected ADR missing status to fail"))
		gomega.Expect(!strings.Contains(err.Error(), "missing status")).To(gomega.BeFalse(), fmt.Sprintf("expected status error, got %v", err))

	})
	ginkgo.It("InvalidADRMissingPRDRefsFailsClosed", func() {
		adr := `---
schema_version: idiom.adr.v1
adr_id: ADR-TEST
status: accepted
evidence_refs:
  - EV-001
tdd_obligations:
  - TDD-001
coverage_tier: unit
---

# ADR-TEST: Test

## Context

Test.

## Decision

Test.
`

		_, err := ParseADRMarkdown([]byte(adr))
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected ADR missing prd_refs to fail"))
		gomega.Expect(!strings.Contains(err.Error(), "missing prd_refs")).To(gomega.BeFalse(), fmt.Sprintf("expected prd_refs error, got %v", err))

	})
	ginkgo.It("InvalidADRIndexMissingIDPathStatusFailsClosed", func() {
		idx := `{"schema_version":"v1","effort_id":"test","adrs":[{"title":"test","prd_refs":["REQ-1"]}]}`

		_, err := ParseADRIndexJSON([]byte(idx))
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected ADR index with missing fields to fail"))

		for _, want := range []string{"missing adr_id", "missing status", "missing path"} {
			gomega.Expect(!strings.Contains(err.Error(), want)).To(gomega.BeFalse(), fmt.Sprintf("expected error containing %q, got %v", want, err))

		}
	})
	ginkgo.It("InvalidADRIndexMissingPRDRefsFailsClosed", func() {
		idx := `{"schema_version":"v1","effort_id":"test","adrs":[{"adr_id":"ADR-1","title":"test","status":"accepted","path":"adr.md"}]}`

		_, err := ParseADRIndexJSON([]byte(idx))
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected ADR index entry without prd_refs to fail"))
		gomega.Expect(!strings.Contains(err.Error(), "missing prd_refs")).To(gomega.BeFalse(), fmt.Sprintf("expected prd_refs error, got %v", err))

	})
	ginkgo.It("WorkflowStateFailsClosedForMissingRequiredFields", func() {
		data := []byte(`{
		"schema_version":"idiom.product_workflow_state.v0.fixture",
		"effort_id":"prd-adr-brief-workflow",
		"task_id":"",
		"title":"PRD/ADR/Brief Research Workflow",
		"status_bucket":"started",
		"current_phase":"phase-1",
		"current_state":"",
		"artifact_paths":{"events":"events.jsonl"},
		"context":{"context_pack_id":"pack","freshness_state":"current"},
		"coverage":{"coverage_tier":"unit"},
		"privacy":{"redaction_status":"applied"},
		"folder_path":"",
		"durability_level":"source_controlled",
		"next_valid_actions":[]
	}`)

		_, err := ParseWorkflowStateJSON(data)
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected missing workflow state fields to fail"))

		for _, want := range []string{"missing task_id", "missing current_state", "missing folder_path", "missing next_valid_actions"} {
			gomega.Expect(!strings.Contains(err.Error(), want)).To(gomega.BeFalse(), fmt.Sprintf("expected error to contain %q, got %v", want, err))

		}
	})
	ginkgo.It("WorkflowStateRejectsUnknownBucketAndState", func() {
		tests := []struct {
			name         string
			statusBucket string
			currentState string
			want         string
		}{
			{name: "unknown bucket", statusBucket: "later", currentState: "planned", want: "invalid status_bucket"},
			{name: "unknown state", statusBucket: "started", currentState: "planning-ish", want: "invalid current_state"},
		}

		for _, tt := range tests {
			ginkgo.By(tt.name, func() {
				data := []byte(fmt.Sprintf(`{
				"schema_version":"idiom.product_workflow_state.v0.fixture",
				"effort_id":"prd-adr-brief-workflow",
				"task_id":"impl-0002",
				"title":"PRD/ADR/Brief Research Workflow",
				"status_bucket":%q,
				"current_phase":"phase-1",
				"current_state":%q,
				"artifact_paths":{"events":"events.jsonl"},
				"context":{"context_pack_id":"pack","freshness_state":"current"},
				"coverage":{"coverage_tier":"unit"},
				"privacy":{"redaction_status":"applied"},
				"folder_path":"docs/product-workflows/efforts/started/prd-adr-brief-workflow",
				"durability_level":"source_controlled",
				"next_valid_actions":["start_implementation"]
			}`, tt.statusBucket, tt.currentState))

				_, err := ParseWorkflowStateJSON(data)
				gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("expected %q error, got %v", tt.want, err))
				gomega.Expect(err.Error()).To(gomega.ContainSubstring(tt.want), fmt.Sprintf("expected %q error, got %v", tt.want, err))

			})
		}
	})
	ginkgo.It("WorkflowStateRejectsStartedBucketWithClosedState", func() {
		data := []byte(`{
		"schema_version":"idiom.product_workflow_state.v0.fixture",
		"effort_id":"prd-adr-brief-workflow",
		"task_id":"impl-0002",
		"title":"PRD/ADR/Brief Research Workflow",
		"status_bucket":"started",
		"current_phase":"phase-1",
		"current_state":"closed",
		"artifact_paths":{"events":"events.jsonl"},
		"context":{"context_pack_id":"pack","freshness_state":"current"},
		"coverage":{"coverage_tier":"unit"},
		"privacy":{"redaction_status":"applied"},
		"folder_path":"docs/product-workflows/efforts/started/prd-adr-brief-workflow",
		"durability_level":"source_controlled",
		"next_valid_actions":["archive"]
	}`)

		_, err := ParseWorkflowStateJSON(data)
		gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("expected bucket/state consistency error, got %v", err))
		gomega.Expect(err.Error()).To(gomega.ContainSubstring("invalid_state_bucket"), fmt.Sprintf("expected bucket/state consistency error, got %v", err))

	})
	ginkgo.It("WorkflowStateRejectsRawPrivacyExposureFlags", func() {
		data := []byte(`{
		"schema_version":"idiom.product_workflow_state.v0.fixture",
		"effort_id":"prd-adr-brief-workflow",
		"task_id":"impl-0002",
		"title":"PRD/ADR/Brief Research Workflow",
		"status_bucket":"started",
		"current_phase":"phase-1",
		"current_state":"implementing",
		"artifact_paths":{"events":"events.jsonl"},
		"context":{"context_pack_id":"pack","freshness_state":"current"},
		"coverage":{"coverage_tier":"unit"},
		"privacy":{"redaction_status":"applied","raw_prompts_stored":true},
		"folder_path":"docs/product-workflows/efforts/closed/prd-adr-brief-workflow",
		"durability_level":"source_controlled",
		"next_valid_actions":["complete"]
	}`)

		_, err := ParseWorkflowStateJSON(data)
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected privacy exposure flag to fail"))
		gomega.Expect(!strings.Contains(err.Error(), "raw_prompts_stored")).To(gomega.BeFalse(), fmt.Sprintf("expected raw prompt privacy error, got %v", err))

	})
	ginkgo.It("ImplementationLedgerPlannedAtomRequiresModelReviewerAndScope", func() {
		data := []byte(`{"record_type":"atom_planned","task_id":"impl-0002","effort_id":"prd-adr-brief-workflow","timestamp":"2026-05-17T15:40:22Z","complexity_tier":"C1","context_pack_id":"pack","implementor":{"id":"same","runtime":"opencode_attached_server","model":"","command":""},"reviewer":{"id":"same","model":"","reasoning_effort":"","runtime":"codex"},"scope":{"expected_files":[]},"required_verification":[]}`)

		_, err := ParseImplementationLedgerJSONL(data)
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected incomplete planned atom to fail"))

		for _, want := range []string{"implementor missing model", "implementor missing command", "reviewer missing model", "reviewer missing reasoning_effort", "same id", "scope missing expected_files", "scope missing forbidden files", "missing required_verification", "missing report_paths"} {
			gomega.Expect(!strings.Contains(err.Error(), want)).To(gomega.BeFalse(), fmt.Sprintf("expected error to contain %q, got %v", want, err))

		}
	})
	ginkgo.It("ImplementationLedgerRejectsUnknownRecordType", func() {
		data := []byte(`{"record_type":"surprise","task_id":"impl-0002","effort_id":"prd-adr-brief-workflow","timestamp":"2026-05-17T15:40:22Z"}`)

		_, err := ParseImplementationLedgerJSONL(data)
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected unknown record_type to fail"))
		gomega.Expect(!strings.Contains(err.Error(), `unknown record_type "surprise"`)).To(gomega.BeFalse(), fmt.Sprintf("expected unknown record_type error, got %v", err))

	})
	ginkgo.It("ImplementationLedgerCloseoutRequiresVerificationReviewInvestigationAndNoBlockers", func() {
		base := `{"record_type":"atom_planned","task_id":"impl-0002","effort_id":"prd-adr-brief-workflow","timestamp":"2026-05-17T15:40:22Z","complexity_tier":"C1","context_pack_id":"pack","implementor":{"id":"impl","runtime":"opencode_attached_server","model":"zai-coding-plan/glm-5.1","command":"run"},"reviewer":{"id":"review","model":"GPT-5.4","reasoning_effort":"medium","runtime":"codex"},"scope":{"expected_files":["internal/workflows/productworkflow/types.go"],"forbidden_patterns":["cmd/**"]},"required_verification":["go test ./internal/workflows/productworkflow"],"report_paths":{"implementation":"reports/impl.json","review":"reports/review.json","investigation":"reports/investigation.json"}}`
		closed := `{"record_type":"atom_closed","task_id":"impl-0002","effort_id":"prd-adr-brief-workflow","timestamp":"2026-05-17T15:45:00Z","open_blockers":["review_pending"]}`

		_, err := ParseImplementationLedgerJSONL([]byte(base + "\n" + closed))
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected premature atom closeout to fail"))

		for _, want := range []string{"without implementation_completed", "without verification_completed", "without review_completed", "without post_effort_investigation_completed", "with open blockers"} {
			gomega.Expect(!strings.Contains(err.Error(), want)).To(gomega.BeFalse(), fmt.Sprintf("expected error to contain %q, got %v", want, err))

		}
	})
	ginkgo.It("ImplementationLedgerValidCloseoutAllowsFallbackCompletion", func() {
		data := strings.Join([]string{
			`{"record_type":"atom_planned","task_id":"impl-0002","effort_id":"prd-adr-brief-workflow","timestamp":"2026-05-17T15:40:22Z","complexity_tier":"C1","context_pack_id":"pack","implementor":{"id":"impl","runtime":"opencode_attached_server","model":"zai-coding-plan/glm-5.1","command":"run"},"reviewer":{"id":"review","model":"GPT-5.4","reasoning_effort":"medium","runtime":"codex"},"scope":{"expected_files":["internal/workflows/productworkflow/types.go"],"forbidden_patterns":["cmd/**"]},"required_verification":["go test ./internal/workflows/productworkflow"],"report_paths":{"implementation":"reports/impl.json","review":"reports/review.json","investigation":"reports/investigation.json"}}`,
			`{"record_type":"fallback_completion","task_id":"impl-0002","effort_id":"prd-adr-brief-workflow","timestamp":"2026-05-17T15:45:00Z","actor":"codex-orchestrator"}`,
			`{"record_type":"verification_completed","task_id":"impl-0002","effort_id":"prd-adr-brief-workflow","timestamp":"2026-05-17T15:46:00Z"}`,
			`{"record_type":"review_completed","task_id":"impl-0002","effort_id":"prd-adr-brief-workflow","timestamp":"2026-05-17T15:47:00Z","reviewer_id":"review","model":"GPT-5.4","reasoning_effort":"medium"}`,
			`{"record_type":"post_effort_investigation_completed","task_id":"impl-0002","effort_id":"prd-adr-brief-workflow","timestamp":"2026-05-17T15:48:00Z"}`,
			`{"record_type":"atom_closed","task_id":"impl-0002","effort_id":"prd-adr-brief-workflow","timestamp":"2026-05-17T15:49:00Z","open_blockers":[]}`,
		}, "\n")
		{

			_, err := ParseImplementationLedgerJSONL([]byte(data))
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("expected valid fallback closeout, got %v", err))
		}

	})
	ginkgo.It("ParseEvidenceCardMissingRequiredFieldsFailsClosed", func() {
		card := `{"card_id":"EV-BAD","kind":"","source_handle":"","claim":"","confidence":"","freshness":"","evidence_refs":[],"researcher_id":"","timestamp":""}`

		_, err := ParseEvidenceCardsJSONL([]byte(card))
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected evidence card missing required fields to fail"))

		for _, want := range []string{"missing kind", "missing source_handle", "missing claim", "missing confidence", "missing freshness", "missing researcher_id", "missing timestamp", "missing evidence_refs"} {
			gomega.Expect(!strings.Contains(err.Error(), want)).To(gomega.BeFalse(), fmt.Sprintf("expected error containing %q, got %v", want, err))

		}
	})
	ginkgo.It("ValidResearchProfilePassesValidation", func() {
		profile := `{
		"schema_version": "idiom.research_profile.v1",
		"name": "idiom-prd-adr-brief",
		"researchers": [
			{
				"id": "north_star",
				"alignment": "north_star",
				"question": "Does the proposal strengthen Idiom's evidence, contract, audit, or eval substrate?",
				"required_sources": ["docs/idiom-strategic-thesis.md"],
				"allowed_capabilities": ["query_context", "audit_behavior"],
				"output": "evidence_cards"
			},
			{
				"id": "technical",
				"alignment": "technical",
				"question": "Which code surfaces and contracts are implicated?",
				"allowed_capabilities": ["localize_change", "query_context"],
				"output": "evidence_cards"
			}
		],
		"merge_policy": {
			"conflicts": "preserve_until_adr",
			"unsupported_claims": "block_prd_audit",
			"stale_local_context": "refresh_or_mark_gap"
		}
	}`

		p, err := ParseResearchProfileJSON([]byte(profile))
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("expected valid research profile to pass, got %v", err))
		gomega.Expect(p.SchemaVersion != "idiom.research_profile.v1").To(gomega.BeFalse(), fmt.Sprintf("expected schema_version idiom.research_profile.v1, got %q", p.SchemaVersion))
		gomega.Expect(len(p.Researchers) != 2).To(gomega.BeFalse(), fmt.Sprintf("expected 2 researchers, got %d", len(p.Researchers)))
		gomega.Expect(p.MergePolicy.Conflicts != "preserve_until_adr").To(gomega.BeFalse(), fmt.Sprintf("expected merge_policy.conflicts preserve_until_adr, got %q", p.MergePolicy.Conflicts))

	})
	ginkgo.It("ResearchProfilePassesWithProfileIDInsteadOfName", func() {
		profile := `{
		"schema_version": "idiom.research_profile.v1",
		"profile_id": "rp-test-001",
		"researchers": [
			{
				"id": "r1",
				"alignment": "technical",
				"question": "What changed?",
				"allowed_capabilities": ["inspect_file"],
				"output": "evidence_cards"
			}
		],
		"merge_policy": {
			"conflicts": "preserve_until_adr",
			"unsupported_claims": "block_prd_audit",
			"stale_local_context": "refresh_or_mark_gap"
		}
	}`
		{
			_, err := ParseResearchProfileJSON([]byte(profile))
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("expected profile with profile_id to pass, got %v", err))
		}

	})
	ginkgo.It("InvalidResearchProfileMissingIdentityFailsClosed", func() {
		profile := `{
		"schema_version": "idiom.research_profile.v1",
		"researchers": [
			{
				"id": "r1",
				"alignment": "technical",
				"question": "Q",
				"allowed_capabilities": ["inspect_file"],
				"output": "evidence_cards"
			}
		],
		"merge_policy": {
			"conflicts": "c",
			"unsupported_claims": "u",
			"stale_local_context": "s"
		}
	}`

		_, err := ParseResearchProfileJSON([]byte(profile))
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected profile missing name and profile_id to fail"))
		gomega.Expect(!strings.Contains(err.Error(), "missing name or profile_id")).To(gomega.BeFalse(), fmt.Sprintf("expected identity error, got %v", err))

	})
	ginkgo.It("InvalidResearchProfileMissingResearcherFieldsFailsClosed", func() {
		profile := `{
		"schema_version": "idiom.research_profile.v1",
		"name": "test",
		"researchers": [
			{
				"id": "",
				"alignment": "",
				"question": "",
				"allowed_capabilities": [],
				"output": ""
			}
		],
		"merge_policy": {
			"conflicts": "c",
			"unsupported_claims": "u",
			"stale_local_context": "s"
		}
	}`

		_, err := ParseResearchProfileJSON([]byte(profile))
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected profile with empty researcher fields to fail"))

		for _, want := range []string{"missing id", "missing alignment", "missing question", "missing allowed_capabilities", "missing output"} {
			gomega.Expect(!strings.Contains(err.Error(), want)).To(gomega.BeFalse(), fmt.Sprintf("expected error containing %q, got %v", want, err))

		}
	})
	ginkgo.It("InvalidResearchProfileMissingMergePolicyFailsClosed", func() {
		profile := `{
		"schema_version": "idiom.research_profile.v1",
		"name": "test",
		"researchers": [
			{
				"id": "r1",
				"alignment": "technical",
				"question": "Q",
				"allowed_capabilities": ["inspect_file"],
				"output": "evidence_cards"
			}
		],
		"merge_policy": {
			"conflicts": "",
			"unsupported_claims": "",
			"stale_local_context": ""
		}
	}`

		_, err := ParseResearchProfileJSON([]byte(profile))
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected profile with empty merge policy to fail"))

		for _, want := range []string{"missing merge_policy.conflicts", "missing merge_policy.unsupported_claims", "missing merge_policy.stale_local_context"} {
			gomega.Expect(!strings.Contains(err.Error(), want)).To(gomega.BeFalse(), fmt.Sprintf("expected error containing %q, got %v", want, err))

		}
	})
	ginkgo.It("InvalidResearchProfileNoResearchersFailsClosed", func() {
		profile := `{
		"schema_version": "idiom.research_profile.v1",
		"name": "test",
		"researchers": [],
		"merge_policy": {
			"conflicts": "c",
			"unsupported_claims": "u",
			"stale_local_context": "s"
		}
	}`

		_, err := ParseResearchProfileJSON([]byte(profile))
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected profile with no researchers to fail"))
		gomega.Expect(!strings.Contains(err.Error(), "missing researchers")).To(gomega.BeFalse(), fmt.Sprintf("expected researchers error, got %v", err))

	})
	ginkgo.It("ValidCurrentDurableTDDFixturePasses", func() {
		data, err := os.ReadFile(productWorkflowFixturePath("tdd.json"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read tdd.json: %v", err))

		ledger, err := ParseTDDLedgerJSON(data)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("expected current durable tdd.json to pass, got %v", err))
		gomega.Expect(ledger.CoverageTier != "unit").To(gomega.BeFalse(), fmt.Sprintf("expected coverage_tier unit, got %q", ledger.CoverageTier))
		gomega.Expect(ledger.Coverage.MutationMinScore != 80).To(gomega.BeFalse(), fmt.Sprintf("expected mutation_min_score 80, got %d", ledger.Coverage.MutationMinScore))
		gomega.Expect(len(ledger.Obligations) == 0).To(gomega.BeFalse(), fmt.Sprint("expected at least one obligation"))

	})
	ginkgo.It("ValidContractHighTDDWithCoverageMetadataPasses", func() {
		score := 85.0
		ledger := TDDLedger{
			SchemaVersion: "idiom.tdd_obligations.v2",
			EffortID:      "test",
			TaskID:        "test",
			CoverageTier:  "contract_high",
			Coverage: TDDCoverage{
				EARSTotal:                5,
				EARSMapped:               5,
				BDDTotal:                 5,
				BDDWithConcreteData:      5,
				FMEAModesTotal:           3,
				FMEAModesMitigated:       3,
				HAZOPDeviationsTotal:     2,
				HAZOPDeviationsTested:    2,
				InvariantsTotal:          1,
				InvariantsWithPBT:        1,
				StateMachinesTotal:       1,
				StateMachinesCovered:     1,
				DecisionTablesTotal:      1,
				DecisionTablesEnumerated: 1,
				BoundaryValuesTotal:      1,
				BoundaryValuesTested:     1,
				FaultTreeLeavesTotal:     1,
				FaultTreeLeavesTested:    1,
				PostMortemIncidentsTotal: 1,
				PostMortemCatchingTests:  1,
				FormalModelsTotal:        1,
				FormalModelsVerified:     1,
				MutationScore:            &score,
				MutationMinScore:         80,
				GatesPassed:              []string{"ears", "bdd", "fmea", "hazop"},
				GatesBlocked:             []string{},
			},
			Obligations: []TDDObligation{
				{ID: "TDD-1", Type: "contract", Behavior: "b", Owner: "o", Command: "c", RequiredBefore: "close", Status: "pending"},
				{ID: "TDD-2", Type: "fmea_negative", Behavior: "b", Owner: "o", Command: "c", RequiredBefore: "close", Status: "pending"},
				{ID: "TDD-3", Type: "property_based", Behavior: "b", Owner: "o", Command: "c", RequiredBefore: "close", Status: "pending"},
			},
		}
		{

			err := validateTDDLedger(ledger)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("expected valid contract_high TDD to pass, got %v", err))
		}

	})
	ginkgo.It("InvalidTDDNegativeCounterFailsClosed", func() {
		ledger := TDDLedger{
			SchemaVersion: "idiom.tdd_obligations.v2",
			EffortID:      "test",
			TaskID:        "test",
			CoverageTier:  "local_low",
			Coverage: TDDCoverage{
				EARSTotal:           -1,
				BDDWithConcreteData: -2,
				MutationMinScore:    80,
			},
			Obligations: []TDDObligation{
				{ID: "TDD-1", Type: "acceptance", Behavior: "b", Owner: "o", Command: "c", RequiredBefore: "close", Status: "pending"},
			},
		}

		err := validateTDDLedger(ledger)
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected negative counters to fail"))
		gomega.Expect(!strings.Contains(err.Error(), "ears_total must be non-negative")).To(gomega.BeFalse(), fmt.Sprintf("expected ears_total error, got %v", err))
		gomega.Expect(!strings.Contains(err.Error(), "bdd_with_concrete_data must be non-negative")).To(gomega.BeFalse(), fmt.Sprintf("expected bdd_with_concrete_data error, got %v", err))

	})
	ginkgo.It("InvalidTDDMappedExceedsTotalFailsClosed", func() {
		ledger := TDDLedger{
			SchemaVersion: "idiom.tdd_obligations.v2",
			EffortID:      "test",
			TaskID:        "test",
			CoverageTier:  "behavior_standard",
			Coverage: TDDCoverage{
				EARSTotal:          3,
				EARSMapped:         5,
				FMEAModesTotal:     2,
				FMEAModesMitigated: 4,
				MutationMinScore:   80,
			},
			Obligations: []TDDObligation{
				{ID: "TDD-1", Type: "acceptance", Behavior: "b", Owner: "o", Command: "c", RequiredBefore: "close", Status: "pending"},
			},
		}

		err := validateTDDLedger(ledger)
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected mapped>total to fail"))
		gomega.Expect(!strings.Contains(err.Error(), "ears_mapped (5) exceeds ears_total (3)")).To(gomega.BeFalse(), fmt.Sprintf("expected ears_mapped exceeds error, got %v", err))
		gomega.Expect(!strings.Contains(err.Error(), "fmea_modes_mitigated (4) exceeds fmea_modes_total (2)")).To(gomega.BeFalse(), fmt.Sprintf("expected fmea_modes_mitigated exceeds error, got %v", err))

	})
	ginkgo.It("InvalidTDDStateMachineCountersFailClosed", func() {
		ledger := TDDLedger{
			SchemaVersion: "idiom.tdd_obligations.v2",
			EffortID:      "test",
			TaskID:        "test",
			CoverageTier:  "contract_high",
			Coverage: TDDCoverage{
				StateMachinesTotal:   2,
				StateMachinesCovered: 3,
				MutationMinScore:     80,
			},
			Obligations: []TDDObligation{
				{ID: "TDD-1", Type: "contract", Behavior: "b", Owner: "o", Command: "c", RequiredBefore: "close", Status: "pending"},
			},
		}

		err := validateTDDLedger(ledger)
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected covered>total to fail"))
		gomega.Expect(!strings.Contains(err.Error(), "state_machines_covered (3) exceeds state_machines_total (2)")).To(gomega.BeFalse(), fmt.Sprintf("expected state machine exceeds error, got %v", err))

	})
	ginkgo.It("InvalidTDDBoundaryValueCountersFailClosed", func() {
		ledger := TDDLedger{
			SchemaVersion: "idiom.tdd_obligations.v2",
			EffortID:      "test",
			TaskID:        "test",
			CoverageTier:  "contract_high",
			Coverage: TDDCoverage{
				BoundaryValuesTotal:  2,
				BoundaryValuesTested: 3,
				MutationMinScore:     80,
			},
			Obligations: []TDDObligation{
				{ID: "TDD-1", Type: "contract", Behavior: "b", Owner: "o", Command: "c", RequiredBefore: "close", Status: "pending"},
			},
		}

		err := validateTDDLedger(ledger)
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected tested>total to fail"))
		gomega.Expect(!strings.Contains(err.Error(), "boundary_values_tested (3) exceeds boundary_values_total (2)")).To(gomega.BeFalse(), fmt.Sprintf("expected boundary value exceeds error, got %v", err))

	})
	ginkgo.It("InvalidTDDFaultTreeCountersFailClosed", func() {
		ledger := TDDLedger{
			SchemaVersion: "idiom.tdd_obligations.v2",
			EffortID:      "test",
			TaskID:        "test",
			CoverageTier:  "contract_high",
			Coverage: TDDCoverage{
				FaultTreeLeavesTotal:  2,
				FaultTreeLeavesTested: 3,
				MutationMinScore:      80,
			},
			Obligations: []TDDObligation{
				{ID: "TDD-1", Type: "contract", Behavior: "b", Owner: "o", Command: "c", RequiredBefore: "close", Status: "pending"},
			},
		}

		err := validateTDDLedger(ledger)
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected tested>total to fail"))
		gomega.Expect(!strings.Contains(err.Error(), "fault_tree_leaves_tested (3) exceeds fault_tree_leaves_total (2)")).To(gomega.BeFalse(), fmt.Sprintf("expected fault tree exceeds error, got %v", err))

	})
	ginkgo.It("InvalidTDDPostMortemCountersFailClosed", func() {
		ledger := TDDLedger{
			SchemaVersion: "idiom.tdd_obligations.v2",
			EffortID:      "test",
			TaskID:        "test",
			CoverageTier:  "contract_high",
			Coverage: TDDCoverage{
				PostMortemIncidentsTotal: 2,
				PostMortemCatchingTests:  3,
				MutationMinScore:         80,
			},
			Obligations: []TDDObligation{
				{ID: "TDD-1", Type: "post_mortem", Behavior: "b", Owner: "o", Command: "c", RequiredBefore: "close", Status: "pending"},
			},
		}

		err := validateTDDLedger(ledger)
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected catching_tests>total to fail"))
		gomega.Expect(!strings.Contains(err.Error(), "post_mortem_catching_tests (3) exceeds post_mortem_incidents_total (2)")).To(gomega.BeFalse(), fmt.Sprintf("expected post-mortem exceeds error, got %v", err))

	})
	ginkgo.It("InvalidTDDFormalVerificationCountersFailClosed", func() {
		ledger := TDDLedger{
			SchemaVersion: "idiom.tdd_obligations.v2",
			EffortID:      "test",
			TaskID:        "test",
			CoverageTier:  "formal_optional",
			Coverage: TDDCoverage{
				FormalModelsTotal:    2,
				FormalModelsVerified: 3,
				MutationMinScore:     80,
			},
			Obligations: []TDDObligation{
				{ID: "TDD-1", Type: "formal_verification", Behavior: "b", Owner: "o", Command: "c", RequiredBefore: "close", Status: "pending"},
			},
		}

		err := validateTDDLedger(ledger)
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected verified>total to fail"))
		gomega.Expect(!strings.Contains(err.Error(), "formal_models_verified (3) exceeds formal_models_total (2)")).To(gomega.BeFalse(), fmt.Sprintf("expected formal verification exceeds error, got %v", err))

	})
	ginkgo.It("TDDLedgerAllowsProductionReadinessObligationTypes", func() {
		for _, typ := range []string{
			"fuzz",
			"fuzz_target",
			"rag_eval",
			"llm_security",
			"security",
			"privacy",
			"provider_contract",
			"observability",
			"performance",
			"scripted_e2e",
			"e2e_journey",
		} {
			ginkgo.By(typ, func() {
				ledger := TDDLedger{
					SchemaVersion: "idiom.tdd_obligations.v2",
					EffortID:      "test",
					TaskID:        "test",
					CoverageTier:  "contract_high",
					Coverage:      TDDCoverage{MutationMinScore: 80},
					Obligations: []TDDObligation{
						{ID: "TDD-1", Type: typ, Behavior: "b", Owner: "o", Command: "go test ./...", RequiredBefore: "implementation", Status: "planned"},
					},
				}
				{

					err := validateTDDLedger(ledger)
					gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("expected production-readiness obligation type %q to validate, got %v", typ, err))
				}

			})
		}
	})
	ginkgo.It("InvalidTDDProductionReadinessCountersFailClosed", func() {
		tests := []struct {
			name   string
			mutate func(*TDDCoverage)
			want   string
		}{
			{"negative fuzz", func(c *TDDCoverage) { c.FuzzTargetsTotal = -1 }, "fuzz_targets_total must be non-negative"},
			{"fuzz exceeds", func(c *TDDCoverage) { c.FuzzTargetsTotal = 1; c.FuzzTargetsCovered = 2 }, "fuzz_targets_covered (2) exceeds fuzz_targets_total (1)"},
			{"negative rag", func(c *TDDCoverage) { c.RAGEvalsMeasured = -1 }, "rag_evals_measured must be non-negative"},
			{"rag exceeds", func(c *TDDCoverage) { c.RAGEvalsTotal = 1; c.RAGEvalsMeasured = 2 }, "rag_evals_measured (2) exceeds rag_evals_total (1)"},
			{"security exceeds", func(c *TDDCoverage) { c.SecurityCasesTotal = 1; c.SecurityCasesTested = 2 }, "security_cases_tested (2) exceeds security_cases_total (1)"},
			{"privacy exceeds", func(c *TDDCoverage) { c.PrivacyControlsTotal = 1; c.PrivacyControlsTested = 2 }, "privacy_controls_tested (2) exceeds privacy_controls_total (1)"},
			{"provider exceeds", func(c *TDDCoverage) { c.ProviderContractsTotal = 1; c.ProviderContractsTested = 2 }, "provider_contracts_tested (2) exceeds provider_contracts_total (1)"},
			{"observability exceeds", func(c *TDDCoverage) { c.ObservabilityTotal = 1; c.ObservabilityTested = 2 }, "observability_tested (2) exceeds observability_total (1)"},
			{"performance exceeds", func(c *TDDCoverage) { c.PerformanceBoundsTotal = 1; c.PerformanceBoundsTested = 2 }, "performance_bounds_tested (2) exceeds performance_bounds_total (1)"},
			{"e2e exceeds", func(c *TDDCoverage) { c.E2EJourneysTotal = 1; c.E2EJourneysScripted = 2 }, "e2e_journeys_scripted (2) exceeds e2e_journeys_total (1)"},
		}

		for _, tt := range tests {
			ginkgo.By(tt.name, func() {
				ledger := TDDLedger{
					SchemaVersion: "idiom.tdd_obligations.v2",
					EffortID:      "test",
					TaskID:        "test",
					CoverageTier:  "contract_high",
					Coverage:      TDDCoverage{MutationMinScore: 80},
					Obligations: []TDDObligation{
						{ID: "TDD-1", Type: "contract", Behavior: "b", Owner: "o", Command: "go test ./...", RequiredBefore: "implementation", Status: "planned"},
					},
				}
				tt.mutate(&ledger.Coverage)

				err := validateTDDLedger(ledger)
				gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("expected production-readiness counter error containing %q, got %v", tt.want, err))
				gomega.Expect(err.Error()).To(gomega.ContainSubstring(tt.want), fmt.Sprintf("expected production-readiness counter error containing %q, got %v", tt.want, err))

			})
		}
	})
	ginkgo.It("InvalidTDDUnknownCoverageTierFailsClosed", func() {
		ledger := TDDLedger{
			SchemaVersion: "idiom.tdd_obligations.v2",
			EffortID:      "test",
			TaskID:        "test",
			CoverageTier:  "unknown_tier",
			Coverage:      TDDCoverage{MutationMinScore: 80},
			Obligations: []TDDObligation{
				{ID: "TDD-1", Type: "acceptance", Behavior: "b", Owner: "o", Command: "c", RequiredBefore: "close", Status: "pending"},
			},
		}

		err := validateTDDLedger(ledger)
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected unknown coverage_tier to fail"))
		gomega.Expect(!strings.Contains(err.Error(), "invalid coverage_tier")).To(gomega.BeFalse(), fmt.Sprintf("expected coverage_tier error, got %v", err))

	})
	ginkgo.It("InvalidTDDUnknownObligationTypeFailsClosed", func() {
		ledger := TDDLedger{
			SchemaVersion: "idiom.tdd_obligations.v2",
			EffortID:      "test",
			TaskID:        "test",
			CoverageTier:  "local_low",
			Coverage:      TDDCoverage{MutationMinScore: 80},
			Obligations: []TDDObligation{
				{ID: "TDD-1", Type: "unknown_type", Behavior: "b", Owner: "o", Command: "c", RequiredBefore: "close", Status: "pending"},
			},
		}

		err := validateTDDLedger(ledger)
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected unknown obligation type to fail"))
		gomega.Expect(!strings.Contains(err.Error(), "invalid type")).To(gomega.BeFalse(), fmt.Sprintf("expected invalid type error, got %v", err))

	})
	ginkgo.It("InvalidTDDMutationScoreOutOfBoundsFailsClosed", func() {
		bad := 150.0
		ledger := TDDLedger{
			SchemaVersion: "idiom.tdd_obligations.v2",
			EffortID:      "test",
			TaskID:        "test",
			CoverageTier:  "contract_high",
			Coverage: TDDCoverage{
				MutationScore:    &bad,
				MutationMinScore: 80,
			},
			Obligations: []TDDObligation{
				{ID: "TDD-1", Type: "contract", Behavior: "b", Owner: "o", Command: "c", RequiredBefore: "close", Status: "pending"},
			},
		}

		err := validateTDDLedger(ledger)
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected mutation_score out of bounds to fail"))
		gomega.Expect(!strings.Contains(err.Error(), "mutation_score must be 0..100")).To(gomega.BeFalse(), fmt.Sprintf("expected mutation_score bounds error, got %v", err))

	})
	ginkgo.It("InvalidTDDMutationScoreBelowMinWithoutGateBlockedFailsClosed", func() {
		low := 50.0
		ledger := TDDLedger{
			SchemaVersion: "idiom.tdd_obligations.v2",
			EffortID:      "test",
			TaskID:        "test",
			CoverageTier:  "contract_high",
			Coverage: TDDCoverage{
				MutationScore:    &low,
				MutationMinScore: 80,
				GatesBlocked:     []string{},
			},
			Obligations: []TDDObligation{
				{ID: "TDD-1", Type: "contract", Behavior: "b", Owner: "o", Command: "c", RequiredBefore: "close", Status: "pending"},
			},
		}

		err := validateTDDLedger(ledger)
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected mutation_score below min without gate block to fail"))
		gomega.Expect(!strings.Contains(err.Error(), "below mutation_min_score")).To(gomega.BeFalse(), fmt.Sprintf("expected below min_score error, got %v", err))

	})
	ginkgo.It("ValidTDDMutationScoreBelowMinWithGateBlockedPasses", func() {
		low := 50.0
		ledger := TDDLedger{
			SchemaVersion: "idiom.tdd_obligations.v2",
			EffortID:      "test",
			TaskID:        "test",
			CoverageTier:  "contract_high",
			Coverage: TDDCoverage{
				MutationScore:    &low,
				MutationMinScore: 80,
				GatesBlocked:     []string{"mutation"},
			},
			Obligations: []TDDObligation{
				{ID: "TDD-1", Type: "contract", Behavior: "b", Owner: "o", Command: "c", RequiredBefore: "close", Status: "pending"},
			},
		}
		{

			err := validateTDDLedger(ledger)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("expected mutation score below min with gate blocked to pass, got %v", err))
		}

	})
	ginkgo.It("InvalidTDDNegativeMutationScoreFailsClosed", func() {
		neg := -5.0
		ledger := TDDLedger{
			SchemaVersion: "idiom.tdd_obligations.v2",
			EffortID:      "test",
			TaskID:        "test",
			CoverageTier:  "contract_high",
			Coverage: TDDCoverage{
				MutationScore:    &neg,
				MutationMinScore: 80,
			},
			Obligations: []TDDObligation{
				{ID: "TDD-1", Type: "contract", Behavior: "b", Owner: "o", Command: "c", RequiredBefore: "close", Status: "pending"},
			},
		}

		err := validateTDDLedger(ledger)
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected negative mutation_score to fail"))
		gomega.Expect(!strings.Contains(err.Error(), "mutation_score must be 0..100")).To(gomega.BeFalse(), fmt.Sprintf("expected mutation_score bounds error, got %v", err))

	})
	ginkgo.It("InvalidTDDZeroMutationMinScoreFailsClosed", func() {
		ledger := TDDLedger{
			SchemaVersion: "idiom.tdd_obligations.v2",
			EffortID:      "test",
			TaskID:        "test",
			CoverageTier:  "local_low",
			Coverage:      TDDCoverage{MutationMinScore: 0},
			Obligations: []TDDObligation{
				{ID: "TDD-1", Type: "acceptance", Behavior: "b", Owner: "o", Command: "c", RequiredBefore: "close", Status: "pending"},
			},
		}

		err := validateTDDLedger(ledger)
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected zero mutation_min_score to fail"))
		gomega.Expect(!strings.Contains(err.Error(), "missing positive mutation_min_score")).To(gomega.BeFalse(), fmt.Sprintf("expected mutation_min_score error, got %v", err))

	})
	ginkgo.It("ValidAllCoverageTiersAccepted", func() {
		for _, tier := range AllowedCoverageTiers() {
			ledger := TDDLedger{
				SchemaVersion: "idiom.tdd_obligations.v2",
				EffortID:      "test",
				TaskID:        "test",
				CoverageTier:  tier,
				Coverage:      TDDCoverage{MutationMinScore: 80},
				Obligations: []TDDObligation{
					{ID: "TDD-1", Type: "acceptance", Behavior: "b", Owner: "o", Command: "c", RequiredBefore: "close", Status: "pending"},
				},
			}
			{
				err := validateTDDLedger(ledger)
				gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("expected coverage_tier %q to pass, got %v", tier, err))
			}

		}
	})
	ginkgo.It("AllowedCoverageTiersIncludeCanonicalValues", func() {
		allowed := map[string]bool{}
		for _, tier := range AllowedCoverageTiers() {
			allowed[tier] = true
		}
		for _, tier := range []string{"documentation", "unit", "local_low", "behavior_standard", "contract_high", "formal_optional"} {
			gomega.Expect(!allowed[tier]).To(gomega.BeFalse(), fmt.Sprintf("AllowedCoverageTiers() missing canonical tier %q: %#v", tier, AllowedCoverageTiers()))

		}
	})
	ginkgo.It("ADRInvalidCoverageTierFailsClosed", func() {
		adr := []byte(`---
schema_version: idiom.adr.v1
adr_id: ADR-1
status: draft
prd_refs: [REQ-1]
evidence_refs: [EV-1]
tdd_obligations: [TDD-1]
coverage_tier: must
---
# ADR

## Context
x
## Decision
x
## Alternatives Considered
x
## Evidence
x
## Consequences
x
## Compatibility and Migration
x
## TDD Obligations
x
## Rollback
x
## Open Questions
x
`)

		_, err := ParseADRMarkdown(adr)
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected invalid ADR coverage tier to fail"))
		gomega.Expect(!strings.Contains(err.Error(), "invalid coverage_tier")).To(gomega.BeFalse(), fmt.Sprintf("expected coverage tier error, got %v", err))

	})
	ginkgo.It("ADRIndexInvalidCoverageTierFailsClosed", func() {
		_, err := ParseADRIndexJSON([]byte(`{
  "schema_version":"v1",
  "effort_id":"test-effort",
  "adrs":[{
    "adr_id":"ADR-1",
    "title":"Test",
    "status":"draft",
    "path":"ADR-1.md",
    "coverage_tier":"must",
    "prd_refs":["REQ-1"],
    "created_at":"2026-05-17T00:00:00Z"
  }]
}`))
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected invalid ADR index coverage tier to fail"))
		gomega.Expect(!strings.Contains(err.Error(), "invalid coverage_tier")).To(gomega.BeFalse(), fmt.Sprintf("expected coverage tier error, got %v", err))

	})
	ginkgo.It("ValidAllObligationTypesAccepted", func() {
		for _, typ := range []string{
			"characterization",
			"regression",
			"acceptance",
			"contract",
			"source_sanity",
			"eval",
			"audit",
			"property_based",
			"fmea_negative",
			"hazop_deviation",
			"fault_tree",
			"decision_table",
			"boundary_value",
			"mutation_score",
			"post_mortem",
			"formal_verification",
			"fuzz",
			"fuzz_target",
			"rag_eval",
			"llm_security",
			"security",
			"privacy",
			"provider_contract",
			"observability",
			"performance",
			"scripted_e2e",
			"e2e_journey",
			"manual",
		} {
			ledger := TDDLedger{
				SchemaVersion: "idiom.tdd_obligations.v2",
				EffortID:      "test",
				TaskID:        "test",
				CoverageTier:  "contract_high",
				Coverage:      TDDCoverage{MutationMinScore: 80},
				Obligations: []TDDObligation{
					{ID: "TDD-1", Type: typ, Behavior: "b", Owner: "o", Command: "c", RequiredBefore: "close", Status: "pending"},
				},
			}
			{
				err := validateTDDLedger(ledger)
				gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("expected obligation type %q to pass, got %v", typ, err))
			}

		}
	})
	ginkgo.It("DurableTraceabilityMatrixPassesCrossLinkValidation", func() {
		root := repoRoot()
		read := func(path string) []byte {
			ginkgo.GinkgoHelper()

			data, err := os.ReadFile(filepath.Join(root, path))
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read %s: %v", path, err))

			return data
		}
		rel := productWorkflowFixtureRoot

		matrixData := read(filepath.Join(rel, "evidence/traceability_matrix.json"))

		matrix, err := ParseTraceabilityMatrixJSON(matrixData)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("parse traceability matrix: %v", err))

		cardsData := read(filepath.Join(rel, "evidence/evidence.jsonl"))

		cards, err := ParseEvidenceCardsJSONL(cardsData)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("parse evidence cards: %v", err))

		evidenceIDs := map[string]bool{}
		for _, c := range cards {
			evidenceIDs[c.CardID] = true
		}

		prdData := read(filepath.Join(rel, "prd.md"))

		prd, err := ParsePRDMarkdown(prdData)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("parse PRD: %v", err))

		prdReqIDs := map[string]bool{}
		for _, r := range prd.Requirements {
			prdReqIDs[r.ID] = true
		}

		adrIdxData := read(filepath.Join(rel, "adrs/index.json"))

		adrIdx, err := ParseADRIndexJSON(adrIdxData)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("parse ADR index: %v", err))

		adrIDs := map[string]bool{}
		for _, a := range adrIdx.ADRs {
			adrIDs[a.ADRID] = true
		}

		tddData := read(filepath.Join(rel, "tdd.json"))

		tdd, err := ParseTDDLedgerJSON(tddData)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("parse TDD: %v", err))

		tddIDs := map[string]bool{}
		for _, o := range tdd.Obligations {
			tddIDs[o.ID] = true
		}

		ledgerData := read(filepath.Join(rel, "implementation_ledger.jsonl"))

		ledger, err := ParseImplementationLedgerJSONL(ledgerData)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("parse implementation ledger: %v", err))

		for _, rec := range ledger {
			for _, id := range rec.TDDObligationsSatisfied {
				tddIDs[id] = true
			}
		}

		briefPaths := map[string]bool{}
		briefsDir := filepath.Join(root, rel, "briefs")

		if entries, err := os.ReadDir(briefsDir); err == nil {
			for _, e := range entries {
				if !e.IsDir() {
					briefPaths[filepath.Join("briefs", e.Name())] = true
				}
			}
		}

		reportPaths := map[string]bool{}
		reportsDir := filepath.Join(root, rel, "reports")

		if entries, err := os.ReadDir(reportsDir); err == nil {
			for _, e := range entries {
				if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
					reportPaths[filepath.Join("reports", e.Name())] = true
				}
			}
		}

		linkErrs := ValidateTraceabilityCrossLinks(matrix, evidenceIDs, prdReqIDs, adrIDs, tddIDs, briefPaths, reportPaths)
		gomega.Expect(linkErrs).To(gomega.BeEmpty(), fmt.Sprintf("expected zero link errors, got %#v", linkErrs))
	})
	ginkgo.It("CrossLinkValidationDetectsBrokenEvidenceCard", func() {
		matrix := TraceabilityMatrix{
			SchemaVersion: "v1", EffortID: "e", TaskID: "t", CreatedAt: "now",
			Matrix: []TraceabilityRow{
				{EvidenceCard: "EV-MISSING", PRDRequirement: "REQ-1", ADR: "ADR-1", TDDObligation: "TDD-1", Brief: "briefs/a.md", Report: "reports/a.json"},
			},
		}
		errs := ValidateTraceabilityCrossLinks(matrix,
			map[string]bool{"EV-OK": true},
			map[string]bool{"REQ-1": true},
			map[string]bool{"ADR-1": true},
			map[string]bool{"TDD-1": true},
			map[string]bool{"briefs/a.md": true},
			map[string]bool{"reports/a.json": true},
		)
		gomega.Expect(errs).To(gomega.HaveLen(1), fmt.Sprintf("expected one evidence_card error, got %v", errs))
		gomega.Expect(errs[0].Field).To(gomega.Equal("evidence_card"), fmt.Sprintf("expected one evidence_card error, got %v", errs))
		gomega.Expect(errs[0].Message).To(gomega.ContainSubstring("EV-MISSING"), fmt.Sprintf("expected one evidence_card error, got %v", errs))

	})
	ginkgo.It("CrossLinkValidationDetectsBrokenPRDRequirement", func() {
		matrix := TraceabilityMatrix{
			SchemaVersion: "v1", EffortID: "e", TaskID: "t", CreatedAt: "now",
			Matrix: []TraceabilityRow{
				{EvidenceCard: "EV-1", PRDRequirement: "REQ-MISSING", ADR: "ADR-1", TDDObligation: "TDD-1", Brief: "briefs/a.md", Report: "reports/a.json"},
			},
		}
		errs := ValidateTraceabilityCrossLinks(matrix,
			map[string]bool{"EV-1": true},
			map[string]bool{"REQ-1": true},
			map[string]bool{"ADR-1": true},
			map[string]bool{"TDD-1": true},
			map[string]bool{"briefs/a.md": true},
			map[string]bool{"reports/a.json": true},
		)
		gomega.Expect(errs).To(gomega.HaveLen(1), fmt.Sprintf("expected one prd_requirement error, got %v", errs))
		gomega.Expect(errs[0].Field).To(gomega.Equal("prd_requirement"), fmt.Sprintf("expected one prd_requirement error, got %v", errs))

	})
	ginkgo.It("CrossLinkValidationDetectsBrokenADR", func() {
		matrix := TraceabilityMatrix{
			SchemaVersion: "v1", EffortID: "e", TaskID: "t", CreatedAt: "now",
			Matrix: []TraceabilityRow{
				{EvidenceCard: "EV-1", PRDRequirement: "REQ-1", ADR: "ADR-MISSING", TDDObligation: "TDD-1", Brief: "briefs/a.md", Report: "reports/a.json"},
			},
		}
		errs := ValidateTraceabilityCrossLinks(matrix,
			map[string]bool{"EV-1": true},
			map[string]bool{"REQ-1": true},
			map[string]bool{"ADR-1": true},
			map[string]bool{"TDD-1": true},
			map[string]bool{"briefs/a.md": true},
			map[string]bool{"reports/a.json": true},
		)
		gomega.Expect(errs).To(gomega.HaveLen(1), fmt.Sprintf("expected one adr error, got %v", errs))
		gomega.Expect(errs[0].Field).To(gomega.Equal("adr"), fmt.Sprintf("expected one adr error, got %v", errs))

	})
	ginkgo.It("CrossLinkValidationDetectsBrokenTDDObligation", func() {
		matrix := TraceabilityMatrix{
			SchemaVersion: "v1", EffortID: "e", TaskID: "t", CreatedAt: "now",
			Matrix: []TraceabilityRow{
				{EvidenceCard: "EV-1", PRDRequirement: "REQ-1", ADR: "ADR-1", TDDObligation: "TDD-MISSING", Brief: "briefs/a.md", Report: "reports/a.json"},
			},
		}
		errs := ValidateTraceabilityCrossLinks(matrix,
			map[string]bool{"EV-1": true},
			map[string]bool{"REQ-1": true},
			map[string]bool{"ADR-1": true},
			map[string]bool{"TDD-1": true},
			map[string]bool{"briefs/a.md": true},
			map[string]bool{"reports/a.json": true},
		)
		gomega.Expect(errs).To(gomega.HaveLen(1), fmt.Sprintf("expected one tdd_obligation error, got %v", errs))
		gomega.Expect(errs[0].Field).To(gomega.Equal("tdd_obligation"), fmt.Sprintf("expected one tdd_obligation error, got %v", errs))

	})
	ginkgo.It("CrossLinkValidationAcceptsHistoricalTDDObligationIDs", func() {
		matrix := TraceabilityMatrix{
			SchemaVersion: "v1", EffortID: "e", TaskID: "t", CreatedAt: "now",
			Matrix: []TraceabilityRow{
				{EvidenceCard: "EV-1", PRDRequirement: "REQ-1", ADR: "ADR-1", TDDObligation: "TDD-HIST-001", Brief: "briefs/a.md", Report: "reports/a.json"},
			},
		}
		tddIDs := map[string]bool{"TDD-1": true, "TDD-HIST-001": true}
		errs := ValidateTraceabilityCrossLinks(matrix,
			map[string]bool{"EV-1": true},
			map[string]bool{"REQ-1": true},
			map[string]bool{"ADR-1": true},
			tddIDs,
			map[string]bool{"briefs/a.md": true},
			map[string]bool{"reports/a.json": true},
		)
		gomega.Expect(len(errs) != 0).To(gomega.BeFalse(), fmt.Sprintf("expected zero errors for historical TDD ID, got %v", errs))

	})
	ginkgo.It("CrossLinkValidationDetectsBrokenBriefPath", func() {
		matrix := TraceabilityMatrix{
			SchemaVersion: "v1", EffortID: "e", TaskID: "t", CreatedAt: "now",
			Matrix: []TraceabilityRow{
				{EvidenceCard: "EV-1", PRDRequirement: "REQ-1", ADR: "ADR-1", TDDObligation: "TDD-1", Brief: "briefs/missing.md", Report: "reports/a.json"},
			},
		}
		errs := ValidateTraceabilityCrossLinks(matrix,
			map[string]bool{"EV-1": true},
			map[string]bool{"REQ-1": true},
			map[string]bool{"ADR-1": true},
			map[string]bool{"TDD-1": true},
			map[string]bool{"briefs/a.md": true},
			map[string]bool{"reports/a.json": true},
		)
		gomega.Expect(errs).To(gomega.HaveLen(1), fmt.Sprintf("expected one brief error, got %v", errs))
		gomega.Expect(errs[0].Field).To(gomega.Equal("brief"), fmt.Sprintf("expected one brief error, got %v", errs))

	})
	ginkgo.It("CrossLinkValidationDetectsBrokenReportPath", func() {
		matrix := TraceabilityMatrix{
			SchemaVersion: "v1", EffortID: "e", TaskID: "t", CreatedAt: "now",
			Matrix: []TraceabilityRow{
				{EvidenceCard: "EV-1", PRDRequirement: "REQ-1", ADR: "ADR-1", TDDObligation: "TDD-1", Brief: "briefs/a.md", Report: "reports/missing.json"},
			},
		}
		errs := ValidateTraceabilityCrossLinks(matrix,
			map[string]bool{"EV-1": true},
			map[string]bool{"REQ-1": true},
			map[string]bool{"ADR-1": true},
			map[string]bool{"TDD-1": true},
			map[string]bool{"briefs/a.md": true},
			map[string]bool{"reports/a.json": true},
		)
		gomega.Expect(errs).To(gomega.HaveLen(1), fmt.Sprintf("expected one report error, got %v", errs))
		gomega.Expect(errs[0].Field).To(gomega.Equal("report"), fmt.Sprintf("expected one report error, got %v", errs))

	})
	ginkgo.It("CrossLinkValidationSkipsCheckForNilMaps", func() {
		matrix := TraceabilityMatrix{
			SchemaVersion: "v1", EffortID: "e", TaskID: "t", CreatedAt: "now",
			Matrix: []TraceabilityRow{
				{EvidenceCard: "EV-1", PRDRequirement: "REQ-1", ADR: "ADR-1", TDDObligation: "TDD-1", Brief: "briefs/a.md", Report: "reports/a.json"},
			},
		}
		errs := ValidateTraceabilityCrossLinks(matrix, nil, nil, nil, nil, nil, nil)
		gomega.Expect(len(errs) != 0).To(gomega.BeFalse(), fmt.Sprintf("expected zero errors when all maps nil (artifact parse failed), got %v", errs))

	})
	ginkgo.It("TraceabilityRowMissingFieldsFailClosed", func() {
		m := TraceabilityMatrix{
			SchemaVersion: "v1", EffortID: "e", TaskID: "t", CreatedAt: "now",
			Matrix: []TraceabilityRow{
				{EvidenceCard: "", PRDRequirement: "", ADR: "", TDDObligation: "", Brief: "", Report: ""},
			},
		}

		err := validateTraceabilityMatrix(m)
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected empty row fields to fail"))

		for _, want := range []string{"missing evidence_card", "missing prd_requirement", "missing adr", "missing tdd_obligation", "missing brief", "missing report"} {
			gomega.Expect(!strings.Contains(err.Error(), want)).To(gomega.BeFalse(), fmt.Sprintf("expected error containing %q, got %v", want, err))

		}
	})
	ginkgo.It("ValidateArtifactStateAcceptsCanonicalStates", func() {
		for _, s := range []string{"draft", "audited", "accepted", "blocked", "dispatched"} {
			{
				err := ValidateArtifactState(s)
				gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("expected status %q to pass, got %v", s, err))
			}

		}
	})
	ginkgo.It("ValidateArtifactStateRejectsInvalidStates", func() {
		for _, s := range []string{"accepted_fixture", "unknown", "pending", "reviewed", "closed", "ACTIVE", ""} {
			err := ValidateArtifactState(s)
			if s == "" {
				gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprintf("expected empty status to fail"))
				gomega.Expect(!strings.Contains(err.Error(), "empty")).To(gomega.BeFalse(), fmt.Sprintf("expected empty error, got %v", err))

			} else {
				gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprintf("expected status %q to fail", s))
				gomega.Expect(!strings.Contains(err.Error(), s)).To(gomega.BeFalse(), fmt.Sprintf("expected error to mention %q, got %v", s, err))
				gomega.Expect(!strings.Contains(err.Error(), "valid states")).To(gomega.BeFalse(), fmt.Sprintf("expected error to mention valid states, got %v", err))

			}
		}
	})
	ginkgo.It("ValidateArtifactStateRejectsAcceptedFixtureExplicitly", func() {
		err := ValidateArtifactState("accepted_fixture")
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected accepted_fixture to be rejected"))
		gomega.Expect(!strings.Contains(err.Error(), "accepted_fixture")).To(gomega.BeFalse(), fmt.Sprintf("expected error to mention accepted_fixture, got %v", err))

	})
	ginkgo.It("InvalidPRDStatusFailsClosed", func() {
		prd := `---
schema_version: idiom.prd.v1
prd_id: prd-test
status: accepted_fixture
task_id: task-test
created_at: 2026-05-17T00:00:00Z
---

# PRD: Test

## Requirements

### REQ-TEST-001

- **id**: REQ-TEST-001
- **priority**: must
- **statement**: Test requirement
- **evidence_refs**: ["EV-001"]
- **tdd_obligations**: ["TDD-001"]
`

		_, err := ParsePRDMarkdown([]byte(prd))
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected PRD with invalid status to fail"))
		gomega.Expect(!strings.Contains(err.Error(), "accepted_fixture")).To(gomega.BeFalse(), fmt.Sprintf("expected error mentioning accepted_fixture, got %v", err))
		gomega.Expect(!strings.Contains(err.Error(), "valid states")).To(gomega.BeFalse(), fmt.Sprintf("expected error mentioning valid states, got %v", err))

	})
	ginkgo.It("DurablePRDFixtureStillPasses", func() {
		data, err := os.ReadFile(productWorkflowFixturePath("prd.md"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read prd.md: %v", err))
		{

			_, err := ParsePRDMarkdown(data)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("expected durable PRD fixture to pass, got %v", err))
		}

	})
	ginkgo.It("InvalidADRIndexStatusFailsClosed", func() {
		idx := `{"schema_version":"v1","effort_id":"test","adrs":[{"adr_id":"ADR-1","title":"Test","status":"accepted_fixture","path":"a.md","prd_refs":["REQ-1"]}]}`

		_, err := ParseADRIndexJSON([]byte(idx))
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected ADR index with invalid status to fail"))
		gomega.Expect(!strings.Contains(err.Error(), "accepted_fixture")).To(gomega.BeFalse(), fmt.Sprintf("expected error mentioning accepted_fixture, got %v", err))

	})
	ginkgo.It("DurableADRIndexFixtureStillPasses", func() {
		data, err := os.ReadFile(productWorkflowFixturePath("adrs", "index.json"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read ADR index: %v", err))
		{

			_, err := ParseADRIndexJSON(data)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("expected durable ADR index fixture to pass, got %v", err))
		}

	})
	ginkgo.It("InvalidADRMarkdownStatusFailsClosed", func() {
		adr := `---
schema_version: idiom.adr.v1
adr_id: ADR-TEST
status: accepted_fixture
prd_refs:
  - REQ-TEST-001
evidence_refs:
  - EV-001
tdd_obligations:
  - TDD-001
coverage_tier: unit
---

# ADR-TEST: Test

## Context

Test.

## Decision

Test.

## Alternatives Considered

None.

## Evidence

- EV-001: Test.

## Consequences

None.

## Compatibility and Migration

Compatible.

## TDD Obligations

- TDD-001: Test.

## Rollback

Revert.

## Open Questions

None.
`

		_, err := ParseADRMarkdown([]byte(adr))
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected ADR with invalid status to fail"))
		gomega.Expect(!strings.Contains(err.Error(), "accepted_fixture")).To(gomega.BeFalse(), fmt.Sprintf("expected error mentioning accepted_fixture, got %v", err))

	})
	ginkgo.It("DurableADRMarkdownFixtureStillPasses", func() {
		data, err := os.ReadFile(productWorkflowFixturePath("adrs", "ADR-0001-durable-workflow-substrate.md"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read ADR markdown: %v", err))
		{

			_, err := ParseADRMarkdown(data)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("expected durable ADR markdown fixture to pass, got %v", err))
		}

	})
	ginkgo.It("PRDWithEachCanonicalStatusPasses", func() {
		for _, status := range []string{"draft", "audited", "accepted", "blocked", "dispatched"} {
			acceptance := ""
			if status == "accepted" {
				acceptance = "accepted_at: 2026-05-17T00:00:00Z\naccepted_by: reviewer-1\nacceptance_profile: human-review\n"
			}

			prd := fmt.Sprintf(`---
schema_version: idiom.prd.v1
prd_id: prd-test
status: %s
task_id: task-test
created_at: 2026-05-17T00:00:00Z
%s
---

# PRD: Test

## Requirements

### REQ-TEST-001

- **id**: REQ-TEST-001
- **priority**: must
- **statement**: Test requirement
- **owner_alignment**: team
- **testability**: automated
- **metric**: coverage > 80%%
- **evidence_refs**: ["EV-001"]
- **tdd_obligations**: ["TDD-001"]
- **open_questions**: []
`, status, acceptance)
			{
				_, err := ParsePRDMarkdown([]byte(prd))
				gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("expected PRD status %q to pass, got %v", status, err))
			}

		}
	})
	ginkgo.It("ADRIndexWithEachCanonicalStatusPasses", func() {
		for _, status := range []string{"draft", "audited", "accepted", "blocked", "dispatched"} {
			idx := fmt.Sprintf(`{"schema_version":"v1","effort_id":"test","adrs":[{"adr_id":"ADR-1","title":"Test","status":"%s","path":"a.md","prd_refs":["REQ-1"]}]}`, status)
			{
				_, err := ParseADRIndexJSON([]byte(idx))
				gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("expected ADR index status %q to pass, got %v", status, err))
			}

		}
	})
	ginkgo.It("ADRMarkdownWithEachCanonicalStatusPasses", func() {
		for _, status := range []string{"draft", "audited", "accepted", "blocked", "dispatched"} {
			acceptance := ""
			if status == "accepted" {
				acceptance = "accepted_at: 2026-05-17T00:00:00Z\naccepted_by: reviewer-1\nacceptance_profile: human-review\n"
			}

			adr := fmt.Sprintf(`---
schema_version: idiom.adr.v1
adr_id: ADR-TEST
status: %s
prd_refs:
  - REQ-TEST-001
evidence_refs:
  - EV-001
tdd_obligations:
  - TDD-001
coverage_tier: unit
%s
---

# ADR-TEST: Test

## Context

Test.

## Decision

Test.

## Alternatives Considered

None.

## Evidence

- EV-001: Test.

## Consequences

None.

## Compatibility and Migration

Compatible.

## TDD Obligations

- TDD-001: Test.

## Rollback

Revert.

## Open Questions

None.
`, status, acceptance)
			{
				_, err := ParseADRMarkdown([]byte(adr))
				gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("expected ADR markdown status %q to pass, got %v", status, err))
			}

		}
	})
	ginkgo.It("ValidImplementationReportPasses", func() {
		report := ImplementationReport{
			SchemaVersion:  "idiom.implementation_report.v1",
			TaskID:         "impl-0001",
			EffortID:       "effort-1",
			ImplementorID:  "agent-1",
			Runtime:        "opencode",
			Model:          "glm-5.1",
			CompletedAt:    "2026-05-17T16:00:00Z",
			ComplexityTier: "C1",
			ScopeStatement: "Add report schema validation",
			CodeChanges:    true,
			ChangedPaths:   []string{"validate.go"},
			Verification: VerificationState{
				RunByImplementor: true,
				Commands:         []string{"go test ./..."},
				Status:           "passed",
			},
			PrivacyNote:  "No raw prompts, raw transcripts, file bodies, secrets, or customer data stored.",
			OpenBlockers: []string{},
			ReviewerID:   "reviewer-1",
		}
		{
			err := validateImplementationReport(report)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("expected valid implementation report to pass, got %v", err))
		}

	})
	ginkgo.It("InvalidImplementationReportMissingFieldsFailsClosed", func() {
		report := ImplementationReport{}

		err := validateImplementationReport(report)
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected empty implementation report to fail"))

		for _, want := range []string{
			"missing schema_version", "missing task_id", "missing effort_id",
			"missing implementor_id", "missing runtime", "missing model",
			"missing complexity_tier", "missing scope_statement", "missing reviewer_id",
			"missing verification.status", "missing privacy_note",
			"missing open_blockers",
		} {
			gomega.Expect(!strings.Contains(err.Error(), want)).To(gomega.BeFalse(), fmt.Sprintf("expected error containing %q, got %v", want, err))

		}
	})
	ginkgo.It("InvalidImplementationReportBadComplexityTierFailsClosed", func() {
		report := ImplementationReport{
			SchemaVersion: "v1", TaskID: "t", EffortID: "e", ImplementorID: "i",
			Runtime: "r", Model: "m", ComplexityTier: "C9", ScopeStatement: "s",
			ReviewerID: "rev", Verification: VerificationState{Status: "pending"},
			PrivacyNote: "safe", OpenBlockers: []string{},
		}

		err := validateImplementationReport(report)
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected invalid complexity_tier to fail"))
		gomega.Expect(!strings.Contains(err.Error(), "invalid complexity_tier")).To(gomega.BeFalse(), fmt.Sprintf("expected complexity_tier error, got %v", err))

	})
	ginkgo.It("InvalidImplementationReportVerificationNotPendingRequiresCommands", func() {
		report := ImplementationReport{
			SchemaVersion: "v1", TaskID: "t", EffortID: "e", ImplementorID: "i",
			Runtime: "r", Model: "m", ComplexityTier: "C1", ScopeStatement: "s",
			ReviewerID: "rev", Verification: VerificationState{Status: "passed", Commands: nil},
			PrivacyNote: "safe", OpenBlockers: []string{},
		}

		err := validateImplementationReport(report)
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected verification.commands required when status not pending"))
		gomega.Expect(!strings.Contains(err.Error(), "requires verification.commands")).To(gomega.BeFalse(), fmt.Sprintf("expected verification.commands error, got %v", err))

	})
	ginkgo.It("InvalidImplementationReportRejectsUnknownVerificationStatus", func() {
		report := ImplementationReport{
			SchemaVersion: "v1", TaskID: "t", EffortID: "e", ImplementorID: "i",
			Runtime: "r", Model: "m", ComplexityTier: "C1", ScopeStatement: "s",
			ReviewerID: "rev", Verification: VerificationState{Status: "passsed", Commands: []string{"go test ./..."}},
			PrivacyNote: "safe", OpenBlockers: []string{},
		}

		err := validateImplementationReport(report)
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected unknown verification.status to fail"))
		gomega.Expect(!strings.Contains(err.Error(), "invalid verification.status")).To(gomega.BeFalse(), fmt.Sprintf("expected verification.status error, got %v", err))

	})
	ginkgo.It("InvalidImplementationReportCodeChangesRequiresChangedPaths", func() {
		report := ImplementationReport{
			SchemaVersion: "v1", TaskID: "t", EffortID: "e", ImplementorID: "i",
			Runtime: "r", Model: "m", ComplexityTier: "C1", ScopeStatement: "s",
			ReviewerID: "rev", CodeChanges: true, ChangedPaths: nil,
			Verification: VerificationState{Status: "pending"},
			PrivacyNote:  "safe", OpenBlockers: []string{},
		}

		err := validateImplementationReport(report)
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected changed_paths required when code_changes true"))
		gomega.Expect(!strings.Contains(err.Error(), "changed_paths is empty")).To(gomega.BeFalse(), fmt.Sprintf("expected changed_paths error, got %v", err))

	})
	ginkgo.It("InvalidImplementationReportPrivacyNoteClaimsStoredFailsClosed", func() {
		for _, claim := range []string{"raw_prompts_stored", "transcripts_stored"} {
			ginkgo.By(claim, func() {
				report := ImplementationReport{
					SchemaVersion: "v1", TaskID: "t", EffortID: "e", ImplementorID: "i",
					Runtime: "r", Model: "m", ComplexityTier: "C1", ScopeStatement: "s",
					ReviewerID: "rev", Verification: VerificationState{Status: "pending"},
					PrivacyNote: claim + ": true was recorded", OpenBlockers: []string{},
				}

				err := validateImplementationReport(report)
				gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprintf("expected privacy note claiming %s to fail", claim))
				gomega.Expect(!strings.Contains(err.Error(), claim)).To(gomega.BeFalse(), fmt.Sprintf("expected privacy claim error for %s, got %v", claim, err))

			})
		}
	})
	ginkgo.It("ImplementationReportAllComplexityTiersAccepted", func() {
		for _, tier := range []string{"C0", "C1", "C2", "C3", "C4"} {
			report := ImplementationReport{
				SchemaVersion: "v1", TaskID: "t", EffortID: "e", ImplementorID: "i",
				Runtime: "r", Model: "m", ComplexityTier: tier, ScopeStatement: "s",
				ReviewerID: "rev", Verification: VerificationState{Status: "pending"},
				PrivacyNote: "safe", OpenBlockers: []string{},
			}
			{
				err := validateImplementationReport(report)
				gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("expected complexity_tier %q to pass, got %v", tier, err))
			}

		}
	})
	ginkgo.It("ValidReviewReportPasses", func() {
		report := ReviewReport{
			SchemaVersion: "idiom.review_report.v1",
			EffortID:      "effort-1",
			TaskID:        "impl-0001",
			Reviewer: ReviewerInfo{
				AgentID:                    "reviewer-1",
				Model:                      "GPT-5.4",
				ReasoningEffort:            "medium",
				IndependentFromImplementor: true,
			},
			Status:           "passed",
			Findings:         []ReviewFinding{},
			Checks:           ReviewChecks{"scope": "passed"},
			ReviewedAt:       "2026-05-17T16:00:00Z",
			CommandsReviewed: []string{"go test ./..."},
			CreatedAt:        "2026-05-17T15:00:00Z",
		}
		{
			err := validateReviewReport(report)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("expected valid review report to pass, got %v", err))
		}

	})
	ginkgo.It("InvalidReviewReportMissingFieldsFailsClosed", func() {
		report := ReviewReport{}

		err := validateReviewReport(report)
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected empty review report to fail"))

		for _, want := range []string{
			"missing schema_version", "missing effort_id", "missing task_id",
			"missing reviewer.agent_id", "missing reviewer.model",
			"missing reviewer.reasoning_effort", "missing status",
			"independent_from_implementor must be true",
		} {
			gomega.Expect(!strings.Contains(err.Error(), want)).To(gomega.BeFalse(), fmt.Sprintf("expected error containing %q, got %v", want, err))

		}
	})
	ginkgo.It("InvalidReviewReportNotIndependentFailsClosed", func() {
		report := ReviewReport{
			SchemaVersion: "v1", EffortID: "e", TaskID: "t", Status: "pending",
			Reviewer: ReviewerInfo{
				AgentID: "r", Model: "m", ReasoningEffort: "low",
				IndependentFromImplementor: false,
			},
		}

		err := validateReviewReport(report)
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected reviewer not independent to fail"))
		gomega.Expect(!strings.Contains(err.Error(), "independent_from_implementor must be true")).To(gomega.BeFalse(), fmt.Sprintf("expected independence error, got %v", err))

	})
	ginkgo.It("InvalidReviewReportNotPendingRequiresCloseoutFailsClosed", func() {
		report := ReviewReport{
			SchemaVersion: "v1", EffortID: "e", TaskID: "t", Status: "passed",
			Reviewer: ReviewerInfo{
				AgentID: "r", Model: "m", ReasoningEffort: "low",
				IndependentFromImplementor: true,
			},
		}

		err := validateReviewReport(report)
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected non-pending review without closeout to fail"))

		for _, want := range []string{"missing reviewed_at", "missing commands_reviewed", "missing checks"} {
			gomega.Expect(!strings.Contains(err.Error(), want)).To(gomega.BeFalse(), fmt.Sprintf("expected error containing %q, got %v", want, err))

		}
	})
	ginkgo.It("InvalidReviewReportUnknownStatusFailsClosed", func() {
		report := ReviewReport{
			SchemaVersion: "v1", EffortID: "e", TaskID: "t", Status: "surprise",
			Reviewer: ReviewerInfo{
				AgentID: "r", Model: "m", ReasoningEffort: "low",
				IndependentFromImplementor: true,
			},
			ReviewedAt:       "2026-05-17T16:00:00Z",
			CommandsReviewed: []string{"go test ./..."},
			Checks:           ReviewChecks{"scope": "passed"},
		}

		err := validateReviewReport(report)
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected unknown review status to fail"))
		gomega.Expect(!strings.Contains(err.Error(), `invalid status "surprise"`)).To(gomega.BeFalse(), fmt.Sprintf("expected status error, got %v", err))

	})
	ginkgo.It("InvalidReviewReportRejectsUnsupportedCheckStatus", func() {
		report := ReviewReport{
			SchemaVersion: "v1", EffortID: "e", TaskID: "t", Status: "passed",
			Reviewer: ReviewerInfo{
				AgentID: "r", Model: "m", ReasoningEffort: "low",
				IndependentFromImplementor: true,
			},
			ReviewedAt:       "2026-05-17T16:00:00Z",
			CommandsReviewed: []string{"go test ./..."},
			Checks:           ReviewChecks{"scope": "passed-ish"},
		}

		err := validateReviewReport(report)
		gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("expected unsupported check status error, got %v", err))
		gomega.Expect(err.Error()).To(gomega.ContainSubstring(`check "scope" has unsupported status "passed-ish"`), fmt.Sprintf("expected unsupported check status error, got %v", err))

	})
	ginkgo.It("ReviewCheckStatusRejectsArbitraryStringJSON", func() {
		var status ReviewCheckStatus
		{
			err := json.Unmarshal([]byte(`"looks_good"`), &status)
			gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected arbitrary string review check status to be rejected"))
		}

	})
	ginkgo.It("ReviewCheckStatusRejectsNumericJSON", func() {
		var status ReviewCheckStatus
		{
			err := json.Unmarshal([]byte(`42`), &status)
			gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprintf("expected numeric review check status to fail, got %q", status))
		}

	})
	ginkgo.It("ValidReviewReportPendingAcceptsIncompleteFields", func() {
		report := ReviewReport{
			SchemaVersion: "v1", EffortID: "e", TaskID: "t", Status: "pending",
			Reviewer: ReviewerInfo{
				AgentID: "r", Model: "m", ReasoningEffort: "low",
				IndependentFromImplementor: true,
			},
		}
		{
			err := validateReviewReport(report)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("expected pending review report to pass, got %v", err))
		}

	})
	ginkgo.It("ValidInvestigationReportCompletePasses", func() {
		report := InvestigationReport{
			SchemaVersion:    "idiom.investigation_report.v1",
			EffortID:         "effort-1",
			TaskID:           "impl-0001",
			Status:           "complete",
			CreatedAt:        "2026-05-17T15:00:00Z",
			Scope:            validInvestigationScopeForTest(),
			Tests:            validInvestigationTestsForTest(),
			Coverage:         validInvestigationCoverageForTest(),
			Traceability:     validInvestigationTraceabilityForTest(),
			PrivacyAudit:     validInvestigationPrivacyAuditForTest(),
			ReviewerFindings: []string{},
			Blockers:         []string{},
			NextValidAction:  "close_atom",
			CompletedAt:      "2026-05-17T16:00:00Z",
		}
		{
			err := validateInvestigationReport(report)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("expected valid complete investigation to pass, got %v", err))
		}

	})
	ginkgo.It("InvalidInvestigationReportMissingFieldsFailsClosed", func() {
		report := InvestigationReport{}

		err := validateInvestigationReport(report)
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected empty investigation report to fail"))

		for _, want := range []string{"missing schema_version", "missing effort_id", "missing task_id", "missing status"} {
			gomega.Expect(!strings.Contains(err.Error(), want)).To(gomega.BeFalse(), fmt.Sprintf("expected error containing %q, got %v", want, err))

		}
	})
	ginkgo.It("InvalidInvestigationReportCompleteWithBlockersFailsClosed", func() {
		report := InvestigationReport{
			SchemaVersion: "v1", EffortID: "e", TaskID: "t", Status: "complete",
			CompletedAt: "2026-05-17T16:00:00Z", NextValidAction: "close",
			Scope:        validInvestigationScopeForTest(),
			Tests:        validInvestigationTestsForTest(),
			Coverage:     validInvestigationCoverageForTest(),
			Traceability: validInvestigationTraceabilityForTest(),
			PrivacyAudit: validInvestigationPrivacyAuditForTest(),
			Blockers:     []string{"still_broken"},
		}

		err := validateInvestigationReport(report)
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected complete investigation with blockers to fail"))
		gomega.Expect(!strings.Contains(err.Error(), "blockers is non-empty")).To(gomega.BeFalse(), fmt.Sprintf("expected blockers error, got %v", err))

	})
	ginkgo.It("InvalidInvestigationReportCompleteMissingCloseoutFieldsFailsClosed", func() {
		report := InvestigationReport{
			SchemaVersion: "v1", EffortID: "e", TaskID: "t", Status: "complete",
		}

		err := validateInvestigationReport(report)
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected complete investigation without closeout to fail"))

		for _, want := range []string{
			"missing completed_at", "missing next_valid_action",
			"missing scope", "missing tests", "missing coverage",
			"missing traceability", "missing privacy_audit",
		} {
			gomega.Expect(!strings.Contains(err.Error(), want)).To(gomega.BeFalse(), fmt.Sprintf("expected error containing %q, got %v", want, err))

		}
	})
	ginkgo.It("InvalidInvestigationReportUnknownStatusFailsClosed", func() {
		report := InvestigationReport{
			SchemaVersion: "v1", EffortID: "e", TaskID: "t", Status: "surprise",
		}

		err := validateInvestigationReport(report)
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected unknown investigation status to fail"))
		gomega.Expect(!strings.Contains(err.Error(), `invalid status "surprise"`)).To(gomega.BeFalse(), fmt.Sprintf("expected status error, got %v", err))

	})
	ginkgo.It("ValidInvestigationReportPendingAcceptsIncompleteFields", func() {
		report := InvestigationReport{
			SchemaVersion: "v1", EffortID: "e", TaskID: "t", Status: "pending",
			CreatedAt: "2026-05-17T15:00:00Z",
			Blockers:  []string{"implementation_pending"},
		}
		{
			err := validateInvestigationReport(report)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("expected pending investigation to pass, got %v", err))
		}

	})
	ginkgo.It("ParseInvestigationReportIgnoresUnknownLegacyOnlySections", func() {
		_, err := ParseInvestigationReportJSON([]byte(`{
		"schema_version":"v1",
		"effort_id":"e",
		"task_id":"t",
		"status":"complete",
		"completed_at":"2026-05-17T16:00:00Z",
		"next_valid_action":"close",
		"scope":{"typo":"present"},
		"tests":{"typo":"present"},
		"coverage":{"typo":"present"},
		"traceability":{"typo":"present"},
		"privacy_audit":{"no_raw_prompts":true},
		"blockers":[]
	}`))
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected unknown legacy-only sections to fail required content checks"))

		for _, want := range []string{"missing scope", "missing tests", "missing coverage", "missing traceability"} {
			gomega.Expect(!strings.Contains(err.Error(), want)).To(gomega.BeFalse(), fmt.Sprintf("expected error containing %q, got %v", want, err))

		}
	})
	ginkgo.It("ParseInvestigationReportCapturesAllowedLegacySectionKeysWithoutSatisfyingRequiredContent", func() {
		report, err := ParseInvestigationReportJSON([]byte(`{
		"schema_version":"v1",
		"effort_id":"e",
		"task_id":"t",
		"status":"complete",
		"completed_at":"2026-05-17T16:00:00Z",
		"next_valid_action":"close",
		"scope":{"types.go":"typed report model"},
		"tests":{"commit_hash_validation":"passed"},
		"coverage":{"validate_test.go":"covered"},
		"traceability":{"brief_to_types":"mapped"},
		"privacy_audit":{"no_raw_prompts":true},
		"blockers":[]
	}`))
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected legacy-only sections to fail required content checks"))

		for _, want := range []string{"missing scope", "missing tests", "missing coverage", "missing traceability"} {
			gomega.Expect(!strings.Contains(err.Error(), want)).To(gomega.BeFalse(), fmt.Sprintf("expected error containing %q, got %v", want, err))

		}
		{
			got := report.Scope.LegacyDetails["types.go"]
			gomega.Expect(got != "typed report model").To(gomega.BeFalse(), fmt.Sprintf("legacy scope detail = %q", got))
		}
		{

			got := report.Tests.LegacyDetails["commit_hash_validation"]
			gomega.Expect(got != "passed").To(gomega.BeFalse(), fmt.Sprintf("legacy tests detail = %q", got))
		}
		{

			got := report.Coverage.LegacyDetails["validate_test.go"]
			gomega.Expect(got != "covered").To(gomega.BeFalse(), fmt.Sprintf("legacy coverage detail = %q", got))
		}
		{

			got := report.Traceability.LegacyDetails["brief_to_types"]
			gomega.Expect(got != "mapped").To(gomega.BeFalse(), fmt.Sprintf("legacy traceability detail = %q", got))
		}

	})
	ginkgo.It("InvalidInvestigationReportPrivacyAuditRawPromptsStoredFailsClosed", func() {
		report := InvestigationReport{
			SchemaVersion: "v1", EffortID: "e", TaskID: "t", Status: "complete",
			CompletedAt: "2026-05-17T16:00:00Z", NextValidAction: "close",
			Scope:        validInvestigationScopeForTest(),
			Tests:        validInvestigationTestsForTest(),
			Coverage:     validInvestigationCoverageForTest(),
			Traceability: validInvestigationTraceabilityForTest(),
			PrivacyAudit: InvestigationPrivacyAudit{RawPromptsStored: boolPtrForReportTest(true)},
			Blockers:     []string{},
		}

		err := validateInvestigationReport(report)
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected complete investigation without closeout to fail"))
		gomega.Expect(!strings.Contains(err.Error(), "raw_prompts_stored must not be true")).To(gomega.BeFalse(), fmt.Sprintf("expected raw_prompts_stored error, got %v", err))

	})
	ginkgo.It("InvalidInvestigationReportPrivacyAuditNoRawPromptsFalseFailsClosed", func() {
		report := InvestigationReport{
			SchemaVersion: "v1", EffortID: "e", TaskID: "t", Status: "complete",
			CompletedAt: "2026-05-17T16:00:00Z", NextValidAction: "close",
			Scope:        validInvestigationScopeForTest(),
			Tests:        validInvestigationTestsForTest(),
			Coverage:     validInvestigationCoverageForTest(),
			Traceability: validInvestigationTraceabilityForTest(),
			PrivacyAudit: InvestigationPrivacyAudit{NoRawPrompts: boolPtrForReportTest(false)},
			Blockers:     []string{},
		}

		err := validateInvestigationReport(report)
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected privacy_audit no_raw_prompts=false to fail"))
		gomega.Expect(!strings.Contains(err.Error(), "no_raw_prompts must not be false")).To(gomega.BeFalse(), fmt.Sprintf("expected no_raw_prompts error, got %v", err))

	})
	ginkgo.It("ValidInvestigationReportPrivacyAuditNoFieldsTruePasses", func() {
		report := InvestigationReport{
			SchemaVersion: "v1", EffortID: "e", TaskID: "t", Status: "complete",
			CompletedAt: "2026-05-17T16:00:00Z", NextValidAction: "close",
			Scope:        validInvestigationScopeForTest(),
			Tests:        validInvestigationTestsForTest(),
			Coverage:     validInvestigationCoverageForTest(),
			Traceability: validInvestigationTraceabilityForTest(),
			PrivacyAudit: InvestigationPrivacyAudit{
				NoRawPrompts:     boolPtrForReportTest(true),
				NoRawTranscripts: boolPtrForReportTest(true),
				NoFileBodies:     boolPtrForReportTest(true),
				NoSecrets:        boolPtrForReportTest(true),
				NoCustomerData:   boolPtrForReportTest(true),
			},
			Blockers: []string{},
		}
		{
			err := validateInvestigationReport(report)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("expected valid privacy_audit with no_* fields true to pass, got %v", err))
		}

	})
	ginkgo.It("InvalidInvestigationReportPrivacyAuditAllStoredClaimsFailsClosed", func() {
		report := InvestigationReport{
			SchemaVersion: "v1", EffortID: "e", TaskID: "t", Status: "complete",
			CompletedAt: "2026-05-17T16:00:00Z", NextValidAction: "close",
			Scope:        validInvestigationScopeForTest(),
			Tests:        validInvestigationTestsForTest(),
			Coverage:     validInvestigationCoverageForTest(),
			Traceability: validInvestigationTraceabilityForTest(),
			PrivacyAudit: InvestigationPrivacyAudit{
				RawPromptsStored:              boolPtrForReportTest(true),
				RawTranscriptsStored:          boolPtrForReportTest(true),
				TranscriptsStored:             boolPtrForReportTest(true),
				FileBodiesStored:              boolPtrForReportTest(true),
				SecretsStored:                 boolPtrForReportTest(true),
				CustomerIdentifyingDataStored: boolPtrForReportTest(true),
			},
			Blockers: []string{},
		}

		err := validateInvestigationReport(report)
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected all stored claims to fail"))

		for _, want := range []string{
			"raw_prompts_stored must not be true",
			"raw_transcripts_stored must not be true",
			"transcripts_stored must not be true",
			"file_bodies_stored must not be true",
			"secrets_stored must not be true",
			"customer_identifying_data_stored must not be true",
		} {
			gomega.Expect(!strings.Contains(err.Error(), want)).To(gomega.BeFalse(), fmt.Sprintf("expected error containing %q, got %v", want, err))

		}
	})
	ginkgo.It("InvalidInvestigationReportPrivacyAuditSensitiveStoredClaimsFailClosed", func() {
		for _, tc := range []struct {
			name  string
			audit InvestigationPrivacyAudit
			want  string
		}{
			{
				name:  "tui transcript bodies",
				audit: InvestigationPrivacyAudit{TUITranscriptBodiesStored: boolPtrForReportTest(true)},
				want:  "tui_transcript_bodies_stored must not be true",
			},
			{
				name:  "generated brief bodies",
				audit: InvestigationPrivacyAudit{GeneratedBriefBodiesStoredDurableReports: boolPtrForReportTest(true)},
				want:  "generated_brief_bodies_stored_in_durable_reports must not be true",
			},
			{
				name:  "ranker weight payloads",
				audit: InvestigationPrivacyAudit{RankerWeightPayloadsStored: boolPtrForReportTest(true)},
				want:  "ranker_weight_payloads_stored must not be true",
			},
		} {
			ginkgo.By(tc.name, func() {
				report := InvestigationReport{
					SchemaVersion: "v1", EffortID: "e", TaskID: "t", Status: "complete",
					CompletedAt: "2026-05-17T16:00:00Z", NextValidAction: "close",
					Scope:        validInvestigationScopeForTest(),
					Tests:        validInvestigationTestsForTest(),
					Coverage:     validInvestigationCoverageForTest(),
					Traceability: validInvestigationTraceabilityForTest(),
					PrivacyAudit: tc.audit,
					Blockers:     []string{},
				}

				err := validateInvestigationReport(report)
				gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected sensitive stored claim to fail"))
				gomega.Expect(!strings.Contains(err.Error(), tc.want)).To(gomega.BeFalse(), fmt.Sprintf("expected error containing %q, got %v", tc.want, err))

			})
		}
	})
	ginkgo.It("InvalidInvestigationReportPrivacyAuditAllNoFieldsFalseFailsClosed", func() {
		report := InvestigationReport{
			SchemaVersion: "v1", EffortID: "e", TaskID: "t", Status: "complete",
			CompletedAt: "2026-05-17T16:00:00Z", NextValidAction: "close",
			Scope:        validInvestigationScopeForTest(),
			Tests:        validInvestigationTestsForTest(),
			Coverage:     validInvestigationCoverageForTest(),
			Traceability: validInvestigationTraceabilityForTest(),
			PrivacyAudit: InvestigationPrivacyAudit{
				NoRawPrompts:     boolPtrForReportTest(false),
				NoRawTranscripts: boolPtrForReportTest(false),
				NoFileBodies:     boolPtrForReportTest(false),
				NoSecrets:        boolPtrForReportTest(false),
				NoCustomerData:   boolPtrForReportTest(false),
			},
			Blockers: []string{},
		}

		err := validateInvestigationReport(report)
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected all no_* fields false to fail"))

		for _, want := range []string{
			"no_raw_prompts must not be false",
			"no_raw_transcripts must not be false",
			"no_file_bodies must not be false",
			"no_secrets must not be false",
			"no_customer_data must not be false",
		} {
			gomega.Expect(!strings.Contains(err.Error(), want)).To(gomega.BeFalse(), fmt.Sprintf("expected error containing %q, got %v", want, err))

		}
	})
	ginkgo.It("ValidInvestigationReportPrivacyAuditCheckedNoStoredClaimsPasses", func() {
		report := InvestigationReport{
			SchemaVersion: "v1", EffortID: "e", TaskID: "t", Status: "complete",
			CompletedAt: "2026-05-17T16:00:00Z", NextValidAction: "close",
			Scope:        validInvestigationScopeForTest(),
			Tests:        validInvestigationTestsForTest(),
			Coverage:     validInvestigationCoverageForTest(),
			Traceability: validInvestigationTraceabilityForTest(),
			PrivacyAudit: InvestigationPrivacyAudit{
				Checked:       boolPtrForReportTest(true),
				ExposureFound: boolPtrForReportTest(false),
			},
			Blockers: []string{},
		}
		{
			err := validateInvestigationReport(report)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("expected privacy_audit with checked/exposure_found to pass, got %v", err))
		}

	})
	ginkgo.It("InvestigationCoverageMeasureJSONShape", func() {
		values, err := json.Marshal(InvestigationCoverageMeasure{Values: []string{"TDD-1"}, Set: true})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal values coverage measure: %v", err))
		gomega.Expect(string(values) != `["TDD-1"]`).To(gomega.BeFalse(), fmt.Sprintf("values coverage measure JSON = %s", values))

		count, err := json.Marshal(InvestigationCoverageMeasure{Count: 5, Set: true})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal count coverage measure: %v", err))
		gomega.Expect(string(count) != `5`).To(gomega.BeFalse(), fmt.Sprintf("count coverage measure JSON = %s", count))

		zero, err := json.Marshal(InvestigationCoverageMeasure{Count: 0, Set: true})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal zero coverage measure: %v", err))
		gomega.Expect(string(zero) != `0`).To(gomega.BeFalse(), fmt.Sprintf("zero coverage measure JSON = %s", zero))

		var parsedZero InvestigationCoverageMeasure
		{
			err := json.Unmarshal([]byte(`0`), &parsedZero)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("unmarshal zero coverage measure: %v", err))
		}
		gomega.Expect(!parsedZero.HasContent()).To(gomega.BeFalse(), fmt.Sprintf("explicit zero coverage measure should count as present: %#v", parsedZero))

	})
	ginkgo.It("InvestigationCoverageMeasureRejectsNegativeCount", func() {
		var measure InvestigationCoverageMeasure
		{
			err := json.Unmarshal([]byte(`-1`), &measure)
			gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprintf("expected negative coverage count to fail, got %#v", measure))
		}

	})
	ginkgo.It("InvestigationCoverageMeasureNullAndEmptyArrayAreNoContent", func() {
		for _, body := range []string{`null`, `[]`} {
			var measure InvestigationCoverageMeasure
			{
				err := json.Unmarshal([]byte(body), &measure)
				gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("unmarshal %s: %v", body, err))
			}
			gomega.Expect(measure.HasContent()).To(gomega.BeFalse(), fmt.Sprintf("%s must not count as coverage content: %#v", body, measure))

		}
	})
	ginkgo.It("ValidateReportPrivacyNoteRejectsQuotedTrueClaims", func() {
		for _, note := range []string{
			`{"raw_prompts_stored": true}`,
			`"raw_prompts_stored" : true`,
			`raw_prompts_stored: true`,
		} {
			{
				err := validateReportPrivacyNote(note)
				gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("expected raw_prompts_stored privacy note rejection for %q, got %v", note, err))
				gomega.Expect(err.Error()).To(gomega.ContainSubstring("raw_prompts_stored"), fmt.Sprintf("expected raw_prompts_stored privacy note rejection for %q, got %v", note, err))
			}

		}
	})
	ginkgo.It("ClassifyReportFileNameKnownSuffixes", func() {
		for _, tc := range []struct {
			name      string
			wantType  string
			wantKnown bool
		}{
			{"impl-0001-implementation.json", "implementation", true},
			{"impl-0001-review.json", "review", true},
			{"impl-0001-investigation.json", "investigation", true},
			{"unknown.json", "", false},
			{"impl-0001-other.json", "", false},
		} {
			gotType, gotKnown := ClassifyReportFileName(tc.name)
			gomega.Expect(gotType).To(gomega.Equal(tc.wantType), fmt.Sprintf("ClassifyReportFileName(%q) = (%q, %v), want (%q, %v)",
				tc.name, gotType, gotKnown, tc.wantType, tc.wantKnown))
			gomega.Expect(gotKnown).To(gomega.Equal(tc.wantKnown), fmt.Sprintf("ClassifyReportFileName(%q) = (%q, %v), want (%q, %v)",
				tc.name, gotType, gotKnown, tc.wantType, tc.wantKnown))

		}
	})
	ginkgo.It("DurableImplementationReportFixturesPassValidation", func() {
		reportDir := productWorkflowFixturePath("reports")

		entries, err := os.ReadDir(reportDir)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read reports dir: %v", err))

		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), "-implementation.json") {
				continue
			}

			data, err := os.ReadFile(filepath.Join(reportDir, entry.Name()))
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read %s: %v", entry.Name(), err))
			{

				_, err := ParseImplementationReportJSON(data)
				gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("expected %s to pass implementation report validation, got %v", entry.Name(), err))
			}

		}
	})
	ginkgo.It("DurableReviewReportFixturesPassValidation", func() {
		reportDir := productWorkflowFixturePath("reports")

		entries, err := os.ReadDir(reportDir)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read reports dir: %v", err))

		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), "-review.json") {
				continue
			}

			data, err := os.ReadFile(filepath.Join(reportDir, entry.Name()))
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read %s: %v", entry.Name(), err))
			{

				_, err := ParseReviewReportJSON(data)
				gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("expected %s to pass review report validation, got %v", entry.Name(), err))
			}

		}
	})
	ginkgo.It("DurableInvestigationReportFixturesPassValidation", func() {
		reportDir := productWorkflowFixturePath("reports")

		entries, err := os.ReadDir(reportDir)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read reports dir: %v", err))

		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), "-investigation.json") {
				continue
			}

			data, err := os.ReadFile(filepath.Join(reportDir, entry.Name()))
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read %s: %v", entry.Name(), err))
			{

				_, err := ParseInvestigationReportJSON(data)
				gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("expected %s to pass investigation report validation, got %v", entry.Name(), err))
			}

		}
	})
	ginkgo.It("ValidPRDPreservesNewFields", func() {
		prd := `---
schema_version: idiom.prd.v1
prd_id: prd-test
status: draft
task_id: impl-test
created_at: 2026-05-17T00:00:00Z
---

# PRD: Test

## Requirements

### REQ-TEST-001

- **id**: REQ-TEST-001
- **priority**: must
- **statement**: Test requirement
- **owner_alignment**: team
- **testability**: automated
- **metric**: coverage > 80%
- **evidence_refs**: ["EV-001"]
- **tdd_obligations**: ["TDD-001"]
- **open_questions**: []
`

		doc, err := ParsePRDMarkdown([]byte(prd))
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("expected valid PRD to pass, got %v", err))
		gomega.Expect(len(doc.Requirements) == 0).To(gomega.BeFalse(), fmt.Sprint("expected at least one requirement"))

		req := doc.Requirements[0]
		gomega.Expect(req.OwnerAlignment != "team").To(gomega.BeFalse(), fmt.Sprintf("expected OwnerAlignment team, got %q", req.OwnerAlignment))
		gomega.Expect(req.Testability != "automated").To(gomega.BeFalse(), fmt.Sprintf("expected Testability automated, got %q", req.Testability))
		gomega.Expect(req.Metric != "coverage > 80%").To(gomega.BeFalse(), fmt.Sprintf("expected Metric coverage > 80%%, got %q", req.Metric))
		gomega.Expect(!req.OpenQuestionsPresent).To(gomega.BeFalse(), fmt.Sprint("expected OpenQuestionsPresent true"))
		gomega.Expect(req.OpenQuestions != nil).To(gomega.BeFalse(), fmt.Sprintf("expected empty OpenQuestions, got %v", req.OpenQuestions))

	})
	ginkgo.It("MissingMetricFailsClosed", func() {
		prd := `---
schema_version: idiom.prd.v1
prd_id: prd-test
status: draft
task_id: impl-test
created_at: 2026-05-17T00:00:00Z
---

# PRD: Test

## Requirements

### REQ-TEST-001

- **id**: REQ-TEST-001
- **priority**: must
- **statement**: Test requirement
- **owner_alignment**: team
- **testability**: automated
- **evidence_refs**: ["EV-001"]
- **tdd_obligations**: ["TDD-001"]
- **open_questions**: []
`

		_, err := ParsePRDMarkdown([]byte(prd))
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected PRD missing metric to fail"))
		gomega.Expect(!strings.Contains(err.Error(), "missing metric")).To(gomega.BeFalse(), fmt.Sprintf("expected missing metric error, got %v", err))

	})
	ginkgo.It("MissingOpenQuestionsFieldFailsClosed", func() {
		prd := `---
schema_version: idiom.prd.v1
prd_id: prd-test
status: draft
task_id: impl-test
created_at: 2026-05-17T00:00:00Z
---

# PRD: Test

## Requirements

### REQ-TEST-001

- **id**: REQ-TEST-001
- **priority**: must
- **statement**: Test requirement
- **owner_alignment**: team
- **testability**: automated
- **metric**: coverage > 80%
- **evidence_refs**: ["EV-001"]
- **tdd_obligations**: ["TDD-001"]
`

		_, err := ParsePRDMarkdown([]byte(prd))
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected PRD missing open_questions to fail"))
		gomega.Expect(!strings.Contains(err.Error(), "missing open_questions")).To(gomega.BeFalse(), fmt.Sprintf("expected missing open_questions error, got %v", err))

	})
	ginkgo.It("PRDMissingRequirementFieldsFailsClosed", func() {
		cases := []struct {
			name    string
			prd     string
			wantErr string
		}{
			{
				"missing priority",
				`---
schema_version: idiom.prd.v1
prd_id: prd-test
status: draft
task_id: impl-test
created_at: 2026-05-17T00:00:00Z
---

# PRD: Test

## Requirements

### REQ-TEST-001

- **id**: REQ-TEST-001
- **statement**: Test requirement
- **owner_alignment**: team
- **testability**: automated
- **metric**: coverage > 80%
- **evidence_refs**: ["EV-001"]
- **tdd_obligations**: ["TDD-001"]
- **open_questions**: []
`,
				"missing priority",
			},
			{
				"missing statement",
				`---
schema_version: idiom.prd.v1
prd_id: prd-test
status: draft
task_id: impl-test
created_at: 2026-05-17T00:00:00Z
---

# PRD: Test

## Requirements

### REQ-TEST-001

- **id**: REQ-TEST-001
- **priority**: must
- **owner_alignment**: team
- **testability**: automated
- **metric**: coverage > 80%
- **evidence_refs**: ["EV-001"]
- **tdd_obligations**: ["TDD-001"]
- **open_questions**: []
`,
				"missing statement",
			},
			{
				"missing owner_alignment",
				`---
schema_version: idiom.prd.v1
prd_id: prd-test
status: draft
task_id: impl-test
created_at: 2026-05-17T00:00:00Z
---

# PRD: Test

## Requirements

### REQ-TEST-001

- **id**: REQ-TEST-001
- **priority**: must
- **statement**: Test requirement
- **testability**: automated
- **metric**: coverage > 80%
- **evidence_refs**: ["EV-001"]
- **tdd_obligations**: ["TDD-001"]
- **open_questions**: []
`,
				"missing owner_alignment",
			},
			{
				"missing testability",
				`---
schema_version: idiom.prd.v1
prd_id: prd-test
status: draft
task_id: impl-test
created_at: 2026-05-17T00:00:00Z
---

# PRD: Test

## Requirements

### REQ-TEST-001

- **id**: REQ-TEST-001
- **priority**: must
- **statement**: Test requirement
- **owner_alignment**: team
- **metric**: coverage > 80%
- **evidence_refs**: ["EV-001"]
- **tdd_obligations**: ["TDD-001"]
- **open_questions**: []
`,
				"missing testability",
			},
		}
		for _, tc := range cases {
			ginkgo.By(tc.name, func() {
				_, err := ParsePRDMarkdown([]byte(tc.prd))
				gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprintf("expected PRD %s to fail", tc.name))
				gomega.Expect(!strings.Contains(err.Error(), tc.wantErr)).To(gomega.BeFalse(), fmt.Sprintf("expected error containing %q, got %v", tc.wantErr, err))

			})
		}
	})
	ginkgo.It("ADRParsesRequiredFrontMatterFields", func() {
		doc, err := ParseADRMarkdown([]byte(`---
schema_version: idiom.adr.v1
adr_id: ADR-TEST
status: accepted
prd_refs:
  - REQ-1
evidence_refs:
  - EV-1
tdd_obligations:
  - TDD-1
coverage_tier: unit
accepted_at: 2026-05-17T00:00:00Z
accepted_by: test-agent
acceptance_profile: test-profile
---

# ADR-TEST: Test

## Context

Test.

## Decision

Test.

## Alternatives Considered

None.

## Evidence

- EV-1: Test.

## Consequences

None.

## Compatibility and Migration

Compatible.

## TDD Obligations

- TDD-1: Test.

## Rollback

Revert.

## Open Questions

None.
`))
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("expected valid ADR to parse: %v", err))

		fm := doc.FrontMatter
		gomega.Expect(fm.SchemaVersion != "idiom.adr.v1").To(gomega.BeFalse(), fmt.Sprintf("expected schema_version idiom.adr.v1, got %q", fm.SchemaVersion))
		gomega.Expect(fm.ADRID != "ADR-TEST").To(gomega.BeFalse(), fmt.Sprintf("expected adr_id ADR-TEST, got %q", fm.ADRID))
		gomega.Expect(fm.Status != "accepted").To(gomega.BeFalse(), fmt.Sprintf("expected status accepted, got %q", fm.Status))
		gomega.Expect(fm.PRDRefs).To(gomega.HaveLen(1), fmt.Sprintf("expected prd_refs [REQ-1], got %v", fm.PRDRefs))
		gomega.Expect(fm.PRDRefs[0]).To(gomega.Equal("REQ-1"), fmt.Sprintf("expected prd_refs [REQ-1], got %v", fm.PRDRefs))
		gomega.Expect(fm.EvidenceRefs).To(gomega.HaveLen(1), fmt.Sprintf("expected evidence_refs [EV-1], got %v", fm.EvidenceRefs))
		gomega.Expect(fm.EvidenceRefs[0]).To(gomega.Equal("EV-1"), fmt.Sprintf("expected evidence_refs [EV-1], got %v", fm.EvidenceRefs))
		gomega.Expect(fm.TDDObligations).To(gomega.HaveLen(1), fmt.Sprintf("expected tdd_obligations [TDD-1], got %v", fm.TDDObligations))
		gomega.Expect(fm.TDDObligations[0]).To(gomega.Equal("TDD-1"), fmt.Sprintf("expected tdd_obligations [TDD-1], got %v", fm.TDDObligations))
		gomega.Expect(fm.CoverageTier != "unit").To(gomega.BeFalse(), fmt.Sprintf("expected coverage_tier unit, got %q", fm.CoverageTier))
		gomega.Expect(fm.AcceptedAt != "2026-05-17T00:00:00Z").To(gomega.BeFalse(), fmt.Sprintf("expected accepted_at, got %q", fm.AcceptedAt))
		gomega.Expect(fm.AcceptedBy != "test-agent").To(gomega.BeFalse(), fmt.Sprintf("expected accepted_by test-agent, got %q", fm.AcceptedBy))
		gomega.Expect(fm.AcceptanceProfile != "test-profile").To(gomega.BeFalse(), fmt.Sprintf("expected acceptance_profile test-profile, got %q", fm.AcceptanceProfile))

	})
	ginkgo.It("ADRParsesBodySectionPresence", func() {
		doc, err := ParseADRMarkdown([]byte(`---
schema_version: idiom.adr.v1
adr_id: ADR-TEST
status: draft
prd_refs:
  - REQ-1
evidence_refs:
  - EV-1
tdd_obligations:
  - TDD-1
coverage_tier: unit
---

# ADR-TEST: Test

## Context

Test.

## Decision

Test.

## Alternatives Considered

None.

## Evidence

- EV-1: Test.

## Consequences

None.

## Compatibility and Migration

Compatible.

## TDD Obligations

- TDD-1: Test.

## Rollback

Revert.

## Open Questions

None.
`))
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("expected valid ADR: %v", err))

		s := doc.Sections
		gomega.Expect(!s.HasContext).To(gomega.BeFalse(), fmt.Sprint("expected HasContext true"))
		gomega.Expect(!s.HasDecision).To(gomega.BeFalse(), fmt.Sprint("expected HasDecision true"))
		gomega.Expect(!s.HasAlternativesConsidered).To(gomega.BeFalse(), fmt.Sprint("expected HasAlternativesConsidered true"))
		gomega.Expect(!s.HasEvidence).To(gomega.BeFalse(), fmt.Sprint("expected HasEvidence true"))
		gomega.Expect(!s.HasConsequences).To(gomega.BeFalse(), fmt.Sprint("expected HasConsequences true"))
		gomega.Expect(!s.HasCompatibilityAndMigration).To(gomega.BeFalse(), fmt.Sprint("expected HasCompatibilityAndMigration true"))
		gomega.Expect(!s.HasTDDObligations).To(gomega.BeFalse(), fmt.Sprint("expected HasTDDObligations true"))
		gomega.Expect(!s.HasRollback).To(gomega.BeFalse(), fmt.Sprint("expected HasRollback true"))
		gomega.Expect(!s.HasOpenQuestions).To(gomega.BeFalse(), fmt.Sprint("expected HasOpenQuestions true"))

	})
	ginkgo.It("ADRAcceptsShortSectionNames", func() {
		doc, err := ParseADRMarkdown([]byte(`---
schema_version: idiom.adr.v1
adr_id: ADR-TEST
status: draft
prd_refs:
  - REQ-1
evidence_refs:
  - EV-1
tdd_obligations:
  - TDD-1
coverage_tier: unit
---

# ADR-TEST: Test

## Context

Test.

## Decision

Test.

## Alternatives

None.

## Evidence

- EV-1: Test.

## Consequences

None.

## Compatibility

Compatible.

## TDD Obligations

- TDD-1: Test.

## Rollback

Revert.

## Open Questions

None.
`))
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("expected valid ADR with short section names: %v", err))
		gomega.Expect(!doc.Sections.HasAlternativesConsidered).To(gomega.BeFalse(), fmt.Sprint("expected HasAlternativesConsidered true for ## Alternatives"))
		gomega.Expect(!doc.Sections.HasCompatibilityAndMigration).To(gomega.BeFalse(), fmt.Sprint("expected HasCompatibilityAndMigration true for ## Compatibility"))

	})
	ginkgo.It("PRDAcceptedStatusRequiresAcceptanceMetadata", func() {
		prd := `---
schema_version: idiom.prd.v1
prd_id: prd-test
status: accepted
task_id: task-test
created_at: 2026-05-17T00:00:00Z
---

# PRD: Test

## Requirements

### REQ-TEST-001

- **id**: REQ-TEST-001
- **priority**: must
- **statement**: Test requirement
- **owner_alignment**: team
- **testability**: automated
- **metric**: coverage > 80%
- **evidence_refs**: ["EV-001"]
- **tdd_obligations**: ["TDD-001"]
- **open_questions**: []
`

		_, err := ParsePRDMarkdown([]byte(prd))
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected accepted PRD without acceptance metadata to fail"))

		for _, want := range []string{"missing accepted_at", "missing accepted_by", "missing acceptance_profile"} {
			gomega.Expect(!strings.Contains(err.Error(), want)).To(gomega.BeFalse(), fmt.Sprintf("expected error containing %q, got %v", want, err))

		}
	})
	ginkgo.It("ADRAcceptedStatusRequiresAcceptanceMetadata", func() {
		adr := `---
schema_version: idiom.adr.v1
adr_id: ADR-TEST
status: accepted
prd_refs:
  - REQ-1
evidence_refs:
  - EV-1
tdd_obligations:
  - TDD-1
coverage_tier: unit
---

# ADR-TEST: Test

## Context

Test.

## Decision

Test.

## Alternatives Considered

None.

## Evidence

- EV-1: Test.

## Consequences

None.

## Compatibility and Migration

Compatible.

## TDD Obligations

- TDD-1: Test.

## Rollback

Revert.

## Open Questions

None.
`

		_, err := ParseADRMarkdown([]byte(adr))
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected accepted ADR without acceptance metadata to fail"))

		for _, want := range []string{"missing accepted_at", "missing accepted_by", "missing acceptance_profile"} {
			gomega.Expect(!strings.Contains(err.Error(), want)).To(gomega.BeFalse(), fmt.Sprintf("expected error containing %q, got %v", want, err))

		}
	})
	ginkgo.It("ValidateADRSupersessionLinksDetectsBrokenSelfDuplicateAndNonReciprocal", func() {
		adrs := []ParsedADR{
			{
				RelPath: "adrs/ADR-1.md",
				Doc: ADRDocument{FrontMatter: ADRFrontMatter{
					ADRID:        "ADR-1",
					Supersedes:   []string{"ADR-2", "ADR-2", "ADR-1", "ADR-MISSING"},
					SupersededBy: []string{"ADR-3"},
				}},
			},
			{
				RelPath: "adrs/ADR-2.md",
				Doc: ADRDocument{FrontMatter: ADRFrontMatter{
					ADRID:        "ADR-2",
					SupersededBy: []string{},
				}},
			},
			{
				RelPath: "adrs/ADR-3.md",
				Doc: ADRDocument{FrontMatter: ADRFrontMatter{
					ADRID:      "ADR-3",
					Supersedes: []string{},
				}},
			},
		}
		errs := ValidateADRSupersessionLinks(adrs)
		gomega.Expect(len(errs) < 5).To(gomega.BeFalse(), fmt.Sprintf("expected at least 5 supersession errors, got %v", errs))

		codes := map[string]bool{}
		for _, err := range errs {
			codes[err.Code] = true
		}

		for _, code := range []string{
			"adr_supersession_duplicate_ref",
			"adr_supersession_self_ref",
			"adr_supersession_broken_ref",
			"adr_supersession_non_reciprocal",
		} {
			gomega.Expect(!codes[code]).To(gomega.BeFalse(), fmt.Sprintf("expected code %q in errors, got %v", code, errs))

		}
	})
	ginkgo.It("ADRIndexSupersessionLinksDetectBrokenSelfDuplicateAndNonReciprocal", func() {
		idx := ADRIndex{
			SchemaVersion: "idiom.adr_index.v1",
			EffortID:      "test-effort",
			ADRs: []ADRIndexEntry{
				{
					ADRID:        "ADR-1",
					Status:       "accepted",
					Path:         "ADR-1.md",
					PRDRefs:      []string{"REQ-1"},
					Supersedes:   []string{"ADR-2", "ADR-2", "ADR-1", "ADR-MISSING"},
					SupersededBy: []string{"ADR-3"},
				},
				{
					ADRID:        "ADR-2",
					Status:       "accepted",
					Path:         "ADR-2.md",
					PRDRefs:      []string{"REQ-1"},
					SupersededBy: []string{},
				},
				{
					ADRID:      "ADR-3",
					Status:     "accepted",
					Path:       "ADR-3.md",
					PRDRefs:    []string{"REQ-1"},
					Supersedes: []string{},
				},
			},
		}
		errs := ValidateADRIndexSupersessionLinks(idx)
		gomega.Expect(len(errs) < 5).To(gomega.BeFalse(), fmt.Sprintf("expected at least 5 supersession errors, got %v", errs))

		codes := map[string]bool{}
		for _, err := range errs {
			codes[err.Code] = true
		}

		for _, code := range []string{
			"adr_supersession_duplicate_ref",
			"adr_supersession_self_ref",
			"adr_supersession_broken_ref",
			"adr_supersession_non_reciprocal",
		} {
			gomega.Expect(!codes[code]).To(gomega.BeFalse(), fmt.Sprintf("expected code %q in errors, got %v", code, errs))

		}
	})
	ginkgo.It("ADRMissingRequiredFrontMatterFieldsFailsClosed", func() {
		cases := []struct {
			name    string
			adr     string
			wantErr string
		}{
			{
				"missing evidence_refs",
				`---
schema_version: idiom.adr.v1
adr_id: ADR-TEST
status: accepted
prd_refs:
  - REQ-1
tdd_obligations:
  - TDD-1
coverage_tier: unit
---

# ADR-TEST: Test

## Context

Test.

## Decision

Test.

## Alternatives Considered

None.

## Evidence

- EV-1: Test.

## Consequences

None.

## Compatibility and Migration

Compatible.

## TDD Obligations

- TDD-1: Test.

## Rollback

Revert.

## Open Questions

None.
`,
				"missing evidence_refs",
			},
			{
				"missing tdd_obligations",
				`---
schema_version: idiom.adr.v1
adr_id: ADR-TEST
status: accepted
prd_refs:
  - REQ-1
evidence_refs:
  - EV-1
coverage_tier: unit
---

# ADR-TEST: Test

## Context

Test.

## Decision

Test.

## Alternatives Considered

None.

## Evidence

- EV-1: Test.

## Consequences

None.

## Compatibility and Migration

Compatible.

## TDD Obligations

- TDD-1: Test.

## Rollback

Revert.

## Open Questions

None.
`,
				"missing tdd_obligations",
			},
			{
				"missing coverage_tier",
				`---
schema_version: idiom.adr.v1
adr_id: ADR-TEST
status: accepted
prd_refs:
  - REQ-1
evidence_refs:
  - EV-1
tdd_obligations:
  - TDD-1
---

# ADR-TEST: Test

## Context

Test.

## Decision

Test.

## Alternatives Considered

None.

## Evidence

- EV-1: Test.

## Consequences

None.

## Compatibility and Migration

Compatible.

## TDD Obligations

- TDD-1: Test.

## Rollback

Revert.

## Open Questions

None.
`,
				"missing coverage_tier",
			},
		}
		for _, tc := range cases {
			ginkgo.By(tc.name, func() {
				_, err := ParseADRMarkdown([]byte(tc.adr))
				gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprintf("expected ADR %s to fail", tc.name))
				gomega.Expect(!strings.Contains(err.Error(), tc.wantErr)).To(gomega.BeFalse(), fmt.Sprintf("expected error containing %q, got %v", tc.wantErr, err))

			})
		}
	})
	ginkgo.It("ADRMissingRequiredBodySectionsFailsClosed", func() {
		sections := []struct {
			name    string
			remove  string
			wantErr string
		}{
			{"missing context", "## Context\n\nTest.\n\n", "missing ## Context"},
			{"missing decision", "## Decision\n\nTest.\n\n", "missing ## Decision"},
			{"missing alternatives", "## Alternatives Considered\n\nNone.\n\n", "missing ## Alternatives Considered"},
			{"missing evidence", "## Evidence\n\n- EV-1: Test.\n\n", "missing ## Evidence"},
			{"missing consequences", "## Consequences\n\nNone.\n\n", "missing ## Consequences"},
			{"missing compatibility", "## Compatibility and Migration\n\nCompatible.\n\n", "missing ## Compatibility and Migration"},
			{"missing tdd obligations", "## TDD Obligations\n\n- TDD-1: Test.\n\n", "missing ## TDD Obligations"},
			{"missing rollback", "## Rollback\n\nRevert.\n\n", "missing ## Rollback"},
			{"missing open questions", "## Open Questions\n\nNone.\n", "missing ## Open Questions"},
		}
		fullBody := `## Context

Test.

## Decision

Test.

## Alternatives Considered

None.

## Evidence

- EV-1: Test.

## Consequences

None.

## Compatibility and Migration

Compatible.

## TDD Obligations

- TDD-1: Test.

## Rollback

Revert.

## Open Questions

None.
`

		for _, tc := range sections {
			ginkgo.By(tc.name, func() {
				body := strings.Replace(fullBody, tc.remove, "", 1)
				adr := fmt.Sprintf(`---
schema_version: idiom.adr.v1
adr_id: ADR-TEST
status: accepted
prd_refs:
  - REQ-1
evidence_refs:
  - EV-1
tdd_obligations:
  - TDD-1
coverage_tier: unit
---

# ADR-TEST: Test

%s`, body)

				_, err := ParseADRMarkdown([]byte(adr))
				gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprintf("expected ADR %s to fail", tc.name))
				gomega.Expect(!strings.Contains(err.Error(), tc.wantErr)).To(gomega.BeFalse(), fmt.Sprintf("expected error containing %q, got %v", tc.wantErr, err))

			})
		}
	})
	ginkgo.It("ADRCrossLinkValidationDetectsMissingPRDRef", func() {
		adrs := []ParsedADR{
			{
				Doc: ADRDocument{
					FrontMatter: ADRFrontMatter{
						PRDRefs: []string{"REQ-MISSING"},
					},
				},
				RelPath: "adrs/test.md",
			},
		}
		errs := ValidateADRCrossLinks(adrs,
			map[string]bool{"REQ-1": true},
			map[string]bool{"EV-1": true},
			map[string]bool{"TDD-1": true},
		)
		gomega.Expect(errs).To(gomega.HaveLen(1), fmt.Sprintf("expected one prd_refs error, got %v", errs))
		gomega.Expect(errs[0].Field).To(gomega.Equal("prd_refs"), fmt.Sprintf("expected one prd_refs error, got %v", errs))
		gomega.Expect(!strings.Contains(errs[0].Message, "REQ-MISSING")).To(gomega.BeFalse(), fmt.Sprintf("expected REQ-MISSING in message, got %q", errs[0].Message))

	})
	ginkgo.It("ADRCrossLinkValidationDetectsMissingEvidenceRef", func() {
		adrs := []ParsedADR{
			{
				Doc: ADRDocument{
					FrontMatter: ADRFrontMatter{
						EvidenceRefs: []string{"EV-MISSING"},
					},
				},
				RelPath: "adrs/test.md",
			},
		}
		errs := ValidateADRCrossLinks(adrs,
			map[string]bool{"REQ-1": true},
			map[string]bool{"EV-1": true},
			map[string]bool{"TDD-1": true},
		)
		gomega.Expect(errs).To(gomega.HaveLen(1), fmt.Sprintf("expected one evidence_refs error, got %v", errs))
		gomega.Expect(errs[0].Field).To(gomega.Equal("evidence_refs"), fmt.Sprintf("expected one evidence_refs error, got %v", errs))
		gomega.Expect(!strings.Contains(errs[0].Message, "EV-MISSING")).To(gomega.BeFalse(), fmt.Sprintf("expected EV-MISSING in message, got %q", errs[0].Message))

	})
	ginkgo.It("ADRCrossLinkValidationDetectsMissingTDDObligation", func() {
		adrs := []ParsedADR{
			{
				Doc: ADRDocument{
					FrontMatter: ADRFrontMatter{
						TDDObligations: []string{"TDD-MISSING"},
					},
				},
				RelPath: "adrs/test.md",
			},
		}
		errs := ValidateADRCrossLinks(adrs,
			map[string]bool{"REQ-1": true},
			map[string]bool{"EV-1": true},
			map[string]bool{"TDD-1": true},
		)
		gomega.Expect(errs).To(gomega.HaveLen(1), fmt.Sprintf("expected one tdd_obligations error, got %v", errs))
		gomega.Expect(errs[0].Field).To(gomega.Equal("tdd_obligations"), fmt.Sprintf("expected one tdd_obligations error, got %v", errs))
		gomega.Expect(!strings.Contains(errs[0].Message, "TDD-MISSING")).To(gomega.BeFalse(), fmt.Sprintf("expected TDD-MISSING in message, got %q", errs[0].Message))

	})
	ginkgo.It("ADRCrossLinkValidationMessageFallsBackToADRIDWhenPathMissing", func() {
		adrs := []ParsedADR{
			{
				Doc: ADRDocument{
					FrontMatter: ADRFrontMatter{
						ADRID:          "ADR-1",
						TDDObligations: []string{"TDD-MISSING"},
					},
				},
			},
		}
		errs := ValidateADRCrossLinks(adrs,
			map[string]bool{"REQ-1": true},
			map[string]bool{"EV-1": true},
			map[string]bool{"TDD-1": true},
		)
		gomega.Expect(errs).To(gomega.HaveLen(1), fmt.Sprintf("expected one tdd_obligations error, got %v", errs))
		gomega.Expect(errs[0].Field).To(gomega.Equal("tdd_obligations"), fmt.Sprintf("expected one tdd_obligations error, got %v", errs))
		gomega.Expect(errs[0].Message).To(gomega.ContainSubstring("ADR-1"), fmt.Sprintf("expected message to mention ADR-1 and TDD-MISSING, got %q", errs[0].Message))
		gomega.Expect(errs[0].Message).To(gomega.ContainSubstring("TDD-MISSING"), fmt.Sprintf("expected message to mention ADR-1 and TDD-MISSING, got %q", errs[0].Message))

	})
	ginkgo.It("ADRCrossLinkValidationPassesWhenAllRefsPresent", func() {
		adrs := []ParsedADR{
			{
				Doc: ADRDocument{
					FrontMatter: ADRFrontMatter{
						PRDRefs:        []string{"REQ-1"},
						EvidenceRefs:   []string{"EV-1"},
						TDDObligations: []string{"TDD-1"},
					},
				},
				RelPath: "adrs/test.md",
			},
		}
		errs := ValidateADRCrossLinks(adrs,
			map[string]bool{"REQ-1": true},
			map[string]bool{"EV-1": true},
			map[string]bool{"TDD-1": true},
		)
		gomega.Expect(len(errs) != 0).To(gomega.BeFalse(), fmt.Sprintf("expected zero errors, got %v", errs))

	})
	ginkgo.It("ADRCrossLinkValidationSkipsCheckForNilMaps", func() {
		adrs := []ParsedADR{
			{
				Doc: ADRDocument{
					FrontMatter: ADRFrontMatter{
						PRDRefs:        []string{"REQ-MISSING"},
						EvidenceRefs:   []string{"EV-MISSING"},
						TDDObligations: []string{"TDD-MISSING"},
					},
				},
				RelPath: "adrs/test.md",
			},
		}
		errs := ValidateADRCrossLinks(adrs, nil, nil, nil)
		gomega.Expect(len(errs) != 0).To(gomega.BeFalse(), fmt.Sprintf("expected zero errors when all maps nil, got %v", errs))

	})
	ginkgo.It("ValidatePRDTDDObligationCrossLinksDetectsMissingMust", func() {
		reqs := []PRDRequirement{
			{ID: "REQ-1", Priority: "must", TDDObligations: []string{"TDD-1", "TDD-MISSING"}},
			{ID: "REQ-2", Priority: "should", TDDObligations: []string{"TDD-ALSO-MISSING"}},
		}
		errs := ValidatePRDTDDObligationCrossLinks(reqs, map[string]bool{"TDD-1": true})
		gomega.Expect(len(errs) != 1).To(gomega.BeFalse(), fmt.Sprintf("expected 1 error, got %d: %v", len(errs), errs))
		gomega.Expect(errs[0].RequirementID != "REQ-1").To(gomega.BeFalse(), fmt.Sprintf("expected RequirementID REQ-1, got %q", errs[0].RequirementID))
		gomega.Expect(errs[0].TDDObligation != "TDD-MISSING").To(gomega.BeFalse(), fmt.Sprintf("expected TDDObligation TDD-MISSING, got %q", errs[0].TDDObligation))
		gomega.Expect(!strings.Contains(errs[0].Message, "TDD-MISSING")).To(gomega.BeFalse(), fmt.Sprintf("expected message to mention TDD-MISSING, got %q", errs[0].Message))

	})
	ginkgo.It("ValidatePRDTDDObligationCrossLinksPassesWhenAllPresent", func() {
		reqs := []PRDRequirement{
			{ID: "REQ-1", Priority: "must", TDDObligations: []string{"TDD-1", "TDD-2"}},
		}
		errs := ValidatePRDTDDObligationCrossLinks(reqs, map[string]bool{"TDD-1": true, "TDD-2": true})
		gomega.Expect(len(errs) != 0).To(gomega.BeFalse(), fmt.Sprintf("expected 0 errors, got %d: %v", len(errs), errs))

	})
	ginkgo.It("ValidatePRDTDDObligationCrossLinksSkipsNonMust", func() {
		reqs := []PRDRequirement{
			{ID: "REQ-1", Priority: "should", TDDObligations: []string{"TDD-MISSING"}},
			{ID: "REQ-2", Priority: "MUST", TDDObligations: []string{"TDD-1"}},
		}
		errs := ValidatePRDTDDObligationCrossLinks(reqs, map[string]bool{"TDD-1": true})
		gomega.Expect(len(errs) != 0).To(gomega.BeFalse(), fmt.Sprintf("expected 0 errors (should-priority skipped, MUST case-insensitive match), got %d: %v", len(errs), errs))

	})
	ginkgo.It("ValidatePRDTDDObligationCrossLinksNilMapSkipsCheck", func() {
		reqs := []PRDRequirement{
			{ID: "REQ-1", Priority: "must", TDDObligations: []string{"TDD-MISSING"}},
		}
		errs := ValidatePRDTDDObligationCrossLinks(reqs, nil)
		gomega.Expect(len(errs) != 0).To(gomega.BeFalse(), fmt.Sprintf("expected 0 errors with nil map, got %d: %v", len(errs), errs))

	})
	ginkgo.It("CommitHashValidHexPasses", func() {
		data := `{"record_type":"implementation_completed","task_id":"impl-001","effort_id":"effort-1","timestamp":"2026-05-17T16:00:00Z","commit_hash":"a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2"}`
		{
			_, err := ParseImplementationLedgerJSONL([]byte(data))
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("expected valid 40-char hex commit_hash to pass, got %v", err))
		}

	})
	ginkgo.It("CommitHashEmptyStringPasses", func() {
		data := `{"record_type":"implementation_completed","task_id":"impl-001","effort_id":"effort-1","timestamp":"2026-05-17T16:00:00Z"}`
		{
			_, err := ParseImplementationLedgerJSONL([]byte(data))
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("expected missing/empty commit_hash to pass, got %v", err))
		}

	})
	ginkgo.It("CommitHashInvalidHexFails", func() {
		data := `{"record_type":"implementation_completed","task_id":"impl-001","effort_id":"effort-1","timestamp":"2026-05-17T16:00:00Z","commit_hash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaG"}`

		_, err := ParseImplementationLedgerJSONL([]byte(data))
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected commit_hash with non-hex chars to fail"))
		gomega.Expect(!strings.Contains(err.Error(), "commit_hash must be 40 hex chars")).To(gomega.BeFalse(), fmt.Sprintf("expected commit_hash format error, got %v", err))

	})
	ginkgo.It("CommitHashTooShortFails", func() {
		data := `{"record_type":"implementation_completed","task_id":"impl-001","effort_id":"effort-1","timestamp":"2026-05-17T16:00:00Z","commit_hash":"abc1234"}`

		_, err := ParseImplementationLedgerJSONL([]byte(data))
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected short commit_hash to fail"))
		gomega.Expect(!strings.Contains(err.Error(), "commit_hash must be 40 hex chars")).To(gomega.BeFalse(), fmt.Sprintf("expected commit_hash format error, got %v", err))

	})
	ginkgo.It("CommitHashUppercaseHexFails", func() {
		data := `{"record_type":"implementation_completed","task_id":"impl-001","effort_id":"effort-1","timestamp":"2026-05-17T16:00:00Z","commit_hash":"A1B2C3D4E5F6A1B2C3D4E5F6A1B2C3D4E5F6A1B2"}`

		_, err := ParseImplementationLedgerJSONL([]byte(data))
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected uppercase hex commit_hash to fail"))
		gomega.Expect(!strings.Contains(err.Error(), "commit_hash must be 40 hex chars")).To(gomega.BeFalse(), fmt.Sprintf("expected commit_hash format error, got %v", err))

	})
	ginkgo.It("CommitHashValidatedOnFallbackCompletion", func() {
		data := `{"record_type":"fallback_completion","task_id":"impl-001","effort_id":"effort-1","timestamp":"2026-05-17T16:00:00Z","commit_hash":"bad"}`

		_, err := ParseImplementationLedgerJSONL([]byte(data))
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected bad commit_hash on fallback_completion to fail"))
		gomega.Expect(!strings.Contains(err.Error(), "commit_hash must be 40 hex chars")).To(gomega.BeFalse(), fmt.Sprintf("expected commit_hash format error, got %v", err))

	})
	ginkgo.It("CommitHashValidatedOnPartialImplementation", func() {
		data := `{"record_type":"partial_implementation_recorded","task_id":"impl-001","effort_id":"effort-1","timestamp":"2026-05-17T16:00:00Z","commit_hash":"bad"}`

		_, err := ParseImplementationLedgerJSONL([]byte(data))
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected bad commit_hash on partial_implementation_recorded to fail"))
		gomega.Expect(!strings.Contains(err.Error(), "commit_hash must be 40 hex chars")).To(gomega.BeFalse(), fmt.Sprintf("expected commit_hash format error, got %v", err))

	})
	ginkgo.It("CommitHashNotValidatedOnOtherRecordTypes", func() {
		data := `{"record_type":"atom_planned","task_id":"impl-001","effort_id":"effort-1","timestamp":"2026-05-17T16:00:00Z","complexity_tier":"C1","context_pack_id":"pack","commit_hash":"bad","implementor":{"id":"i","runtime":"r","model":"m","command":"c"},"reviewer":{"id":"r","model":"m","reasoning_effort":"m","runtime":"r"},"scope":{"expected_files":["a.go"],"forbidden":["b.go"]},"required_verification":["go test"],"report_paths":{"implementation":"reports/i.json","review":"reports/r.json","investigation":"reports/v.json"}}`
		{
			_, err := ParseImplementationLedgerJSONL([]byte(data))
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("expected commit_hash on atom_planned to be ignored, got %v", err))
		}

	})
	ginkgo.It("ValidateADRIndexFrontMatterConsistencyCoversAuthorityFields", func() {
		errs := ValidateADRIndexFrontMatterConsistency(ADRIndex{ADRs: []ADRIndexEntry{{
			ADRID:        "ADR-001",
			Status:       "accepted",
			Path:         "ADR-001-test.md",
			CoverageTier: "unit",
			PRDRefs:      []string{"REQ-INDEX"},
			Supersedes:   []string{"ADR-OLD"},
			SupersededBy: []string{"ADR-NEW"},
		}}}, []ParsedADR{{
			RelPath: "adrs/ADR-001-test.md",
			Doc: ADRDocument{FrontMatter: ADRFrontMatter{
				ADRID:        "ADR-001-MD",
				Status:       "draft",
				CoverageTier: "behavior_standard",
				PRDRefs:      []string{"REQ-MARKDOWN"},
				Supersedes:   []string{"ADR-OLDER"},
				SupersededBy: []string{"ADR-NEWER"},
			}},
		}})
		fields := map[string]bool{}

		for _, err := range errs {
			fields[err.Field] = true
			gomega.Expect(err.Code).To(gomega.Equal("adr_index_frontmatter_mismatch"), fmt.Sprintf("unexpected consistency error: %#v", err))
			gomega.Expect(err.Path).To(gomega.Equal("adrs/index.json"), fmt.Sprintf("unexpected consistency error: %#v", err))

		}

		for _, want := range []string{"adr_id", "status", "coverage_tier", "prd_refs", "supersedes", "superseded_by"} {
			gomega.Expect(!fields[want]).To(gomega.BeFalse(), fmt.Sprintf("expected mismatch for %s, got %#v", want, errs))

		}
	})
	ginkgo.It("UnwrapSingleMarkdownFence", func() {
		input := fenced(prdBody)
		result := UnwrapSingleMarkdownFence(input)
		expected := strings.TrimSpace(prdBody)
		gomega.Expect(result != expected).To(gomega.BeFalse(), fmt.Sprintf("expected unwrapped PRD content, got: %s", result))

	})
	ginkgo.It("UnwrapSingleMarkdownFenceNoFenceReturnsOriginal", func() {
		result := UnwrapSingleMarkdownFence(prdBody)
		gomega.Expect(result != prdBody).To(gomega.BeFalse(), fmt.Sprintf("expected unchanged input, got: %s", result))

	})
	ginkgo.It("UnwrapSingleMarkdownFenceMissingClosingReturnsOriginal", func() {
		input := fenced(prdBody)

		input = input[:len(input)-len(fenceClose())-1]

		result := UnwrapSingleMarkdownFence(input)
		gomega.Expect(result != input).To(gomega.BeFalse(), fmt.Sprintf("expected unchanged input for unclosed fence, got: %s", result))

	})
	ginkgo.It("UnwrapSingleMarkdownFenceEmptyBodyReturnsOriginal", func() {
		input := fenceOpen() + "markdown\n\n" + fenceClose()
		result := UnwrapSingleMarkdownFence(input)
		gomega.Expect(result != input).To(gomega.BeFalse(), fmt.Sprintf("expected unchanged input for empty fence body, got: %s", result))

	})
	ginkgo.It("ParsePRDMarkdownWithFenceWrappedInput", func() {
		prd := fenced(prdBody)
		unwrapped := UnwrapSingleMarkdownFence(prd)
		gomega.Expect(unwrapped == prd).To(gomega.BeFalse(), fmt.Sprint("expected fence to be unwrapped"))

		doc, err := ParsePRDMarkdown([]byte(unwrapped))
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("expected fence-wrapped PRD to parse after unwrapping: %v", err))
		gomega.Expect(doc.FrontMatter.PRDID != "prd-test").To(gomega.BeFalse(), fmt.Sprintf("expected prd_id prd-test, got %q", doc.FrontMatter.PRDID))
		gomega.Expect(len(doc.Requirements) != 1).To(gomega.BeFalse(), fmt.Sprintf("expected 1 requirement, got %d", len(doc.Requirements)))

	})
	ginkgo.It("ParsePRDMarkdownWithPlainCodeFence", func() {
		prd := fencedPlain(prdBody)
		unwrapped := UnwrapSingleMarkdownFence(prd)
		gomega.Expect(unwrapped == prd).To(gomega.BeFalse(), fmt.Sprint("expected unlabeled fence to be unwrapped"))

		doc, err := ParsePRDMarkdown([]byte(unwrapped))
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("expected fence-wrapped PRD (unlabeled) to parse after unwrapping: %v", err))
		gomega.Expect(doc.FrontMatter.PRDID != "prd-test").To(gomega.BeFalse(), fmt.Sprintf("expected prd_id prd-test, got %q", doc.FrontMatter.PRDID))

	})
	ginkgo.It("TryParseADRMarkdown", func() {
		adr := `---
schema_version: idiom.adr.v1
adr_id: ADR-TEST
status: draft
prd_refs:
  - REQ-1
evidence_refs:
  - EV-1
tdd_obligations:
  - TDD-1
coverage_tier: unit
---

# ADR-TEST: Test

## Context
Test.

## Decision
Test.

## Alternatives Considered
None.

## Evidence
- EV-1: Test.

## Consequences
None.

## Compatibility and Migration
Compatible.

## TDD Obligations
- TDD-1: Test.

## Rollback
Revert.

## Open Questions
None.
`

		doc, repaired, err := TryParseADRMarkdown(adr)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("TryParseADRMarkdown: %v", err))
		gomega.Expect(doc.FrontMatter.ADRID != "ADR-TEST").To(gomega.BeFalse(), fmt.Sprintf("expected ADR-TEST, got %q", doc.FrontMatter.ADRID))
		gomega.Expect(repaired != strings.TrimSpace(adr)).To(gomega.BeFalse(), fmt.Sprintf("expected repaired text to match input, got: %q vs %q", repaired, strings.TrimSpace(adr)))

	})
	ginkgo.It("TryParseADRMarkdownWithFence", func() {
		fenced := "```markdown\n---\nschema_version: idiom.adr.v1\nadr_id: ADR-TEST\nstatus: draft\nprd_refs:\n  - REQ-1\nevidence_refs:\n  - EV-1\ntdd_obligations:\n  - TDD-1\ncoverage_tier: unit\n---\n\n# ADR-TEST: Test\n\n## Context\nTest.\n\n## Decision\nTest.\n\n## Alternatives Considered\nNone.\n\n## Evidence\n- EV-1: Test.\n\n## Consequences\nNone.\n\n## Compatibility and Migration\nCompatible.\n\n## TDD Obligations\n- TDD-1: Test.\n\n## Rollback\nRevert.\n\n## Open Questions\nNone.\n```"

		doc, _, err := TryParseADRMarkdown(fenced)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("TryParseADRMarkdown with fence: %v", err))
		gomega.Expect(doc.FrontMatter.ADRID != "ADR-TEST").To(gomega.BeFalse(), fmt.Sprintf("expected ADR-TEST, got %q", doc.FrontMatter.ADRID))

	})
	ginkgo.It("TryParseADRMarkdownGarbageFails", func() {
		_, _, err := TryParseADRMarkdown("not an ADR at all")
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected error for garbage input"))

	})
})

func boolPtrForReportTest(value bool) *bool {
	return &value
}

func validInvestigationScopeForTest() InvestigationScope {
	return InvestigationScope{ExpectedPaths: []string{"a.go"}}
}

func validInvestigationTestsForTest() InvestigationTests {
	return InvestigationTests{Status: string(ProductWorkflowStatusPass), Commands: []InvestigationTestCommand{{Command: "go test"}}}
}

func validInvestigationCoverageForTest() InvestigationCoverage {
	return InvestigationCoverage{Tier: "unit"}
}

func validInvestigationTraceabilityForTest() InvestigationTraceability {
	return InvestigationTraceability{Checked: boolPtrForReportTest(true)}
}

func validInvestigationPrivacyAuditForTest() InvestigationPrivacyAudit {
	return InvestigationPrivacyAudit{NoRawPrompts: boolPtrForReportTest(true)}
}

func repoRoot() string {
	ginkgo.GinkgoHelper()

	dir, err := os.Getwd()
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprint(err))

	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}

		next := filepath.Dir(dir)
		gomega.Expect(next == dir).To(gomega.BeFalse(), fmt.Sprint("could not find repo root"))

		dir = next
	}
}

func fenceOpen() string  { return "`" + "`" + "`" }
func fenceClose() string { return "`" + "`" + "`" }

func fenced(inner string) string {
	return fenceOpen() + "markdown\n" + inner + "\n" + fenceClose()
}

func fencedPlain(inner string) string {
	return fenceOpen() + "\n" + inner + "\n" + fenceClose()
}

const prdBody = `---
schema_version: idiom.prd.v1
prd_id: prd-test
status: draft
task_id: task-test
created_at: 2026-05-17T00:00:00Z
---

# PRD: Test

## Requirements

### REQ-TEST-001

- **id**: REQ-TEST-001
- **priority**: P1
- **statement**: Test requirement
- **owner_alignment**: team
- **testability**: automated
- **metric**: coverage > 80%
- **evidence_refs**: [EV-001]
- **tdd_obligations**: [TDD-001]
- **open_questions**: []
`
