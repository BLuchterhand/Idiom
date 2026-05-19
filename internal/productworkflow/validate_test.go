package productworkflow

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseCommittedPhase0Fixtures(t *testing.T) {
	root := repoRoot(t)
	read := func(path string) []byte {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		return data
	}

	if _, err := ParseEffortIndexJSON(read("docs/product-workflows/efforts/index.json")); err != nil {
		t.Fatalf("parse effort index: %v", err)
	}
	if _, err := ParseWorkflowStateJSON(read("docs/product-workflows/efforts/closed/prd-adr-brief-workflow/workflow_state.json")); err != nil {
		t.Fatalf("parse workflow state: %v", err)
	}
	if _, err := ParseTDDLedgerJSON(read("docs/product-workflows/efforts/closed/prd-adr-brief-workflow/tdd.json")); err != nil {
		t.Fatalf("parse tdd ledger: %v", err)
	}
	if _, err := ParseEvidenceCardsJSONL(read("docs/product-workflows/efforts/closed/prd-adr-brief-workflow/evidence/evidence.jsonl")); err != nil {
		t.Fatalf("parse evidence JSONL: %v", err)
	}
	if _, err := ParseTraceabilityMatrixJSON(read("docs/product-workflows/efforts/closed/prd-adr-brief-workflow/evidence/traceability_matrix.json")); err != nil {
		t.Fatalf("parse traceability matrix: %v", err)
	}
	if _, err := ParseImplementationLedgerJSONL(read("docs/product-workflows/efforts/closed/prd-adr-brief-workflow/implementation_ledger.jsonl")); err != nil {
		t.Fatalf("parse implementation ledger: %v", err)
	}
}

func TestParsePRDFixturePasses(t *testing.T) {
	root := repoRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "docs/product-workflows/efforts/closed/prd-adr-brief-workflow/prd.md"))
	if err != nil {
		t.Fatalf("read prd.md: %v", err)
	}
	doc, err := ParsePRDMarkdown(data)
	if err != nil {
		t.Fatalf("parse PRD fixture: %v", err)
	}
	if doc.FrontMatter.PRDID != "prd-impl-0001-phase-0" {
		t.Fatalf("expected prd_id prd-impl-0001-phase-0, got %q", doc.FrontMatter.PRDID)
	}
	if doc.FrontMatter.AcceptedAt == "" || doc.FrontMatter.AcceptedBy == "" || doc.FrontMatter.AcceptanceProfile == "" {
		t.Fatalf("expected accepted PRD fixture to include acceptance metadata, got %#v", doc.FrontMatter)
	}
	if len(doc.Requirements) == 0 {
		t.Fatal("expected at least one requirement")
	}
	req := doc.Requirements[0]
	if req.ID != "REQ-IMPL-0001" {
		t.Fatalf("expected requirement ID REQ-IMPL-0001, got %q", req.ID)
	}
	if len(req.EvidenceRefs) == 0 {
		t.Fatal("expected evidence_refs on requirement")
	}
	if len(req.TDDObligations) == 0 {
		t.Fatal("expected tdd_obligations on requirement")
	}
}

func TestParseADRIndexFixturePasses(t *testing.T) {
	root := repoRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "docs/product-workflows/efforts/closed/prd-adr-brief-workflow/adrs/index.json"))
	if err != nil {
		t.Fatalf("read ADR index: %v", err)
	}
	idx, err := ParseADRIndexJSON(data)
	if err != nil {
		t.Fatalf("parse ADR index fixture: %v", err)
	}
	if len(idx.ADRs) == 0 {
		t.Fatal("expected at least one ADR entry")
	}
	if idx.ADRs[0].ADRID != "ADR-0001" {
		t.Fatalf("expected ADR-0001, got %q", idx.ADRs[0].ADRID)
	}
}

func TestParseADRMarkdownFixturePasses(t *testing.T) {
	root := repoRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "docs/product-workflows/efforts/closed/prd-adr-brief-workflow/adrs/ADR-0001-durable-workflow-substrate.md"))
	if err != nil {
		t.Fatalf("read ADR markdown: %v", err)
	}
	doc, err := ParseADRMarkdown(data)
	if err != nil {
		t.Fatalf("parse ADR fixture: %v", err)
	}
	if doc.FrontMatter.ADRID != "ADR-0001" {
		t.Fatalf("expected adr_id ADR-0001, got %q", doc.FrontMatter.ADRID)
	}
	if !doc.Sections.HasDecision {
		t.Fatal("expected HasDecision true")
	}
	if len(doc.FrontMatter.PRDRefs) == 0 {
		t.Fatal("expected prd_refs in ADR front matter")
	}
}

func TestInvalidPRDMissingEvidenceFailsClosed(t *testing.T) {
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
	if err == nil {
		t.Fatal("expected PRD with empty evidence_refs to fail")
	}
	if !strings.Contains(err.Error(), "missing evidence_refs") {
		t.Fatalf("expected evidence_refs error, got %v", err)
	}
	if !strings.Contains(err.Error(), "missing tdd_obligations") {
		t.Fatalf("expected tdd_obligations error, got %v", err)
	}
}

func TestInvalidPRDNoRequirementsFailsClosed(t *testing.T) {
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
	if err == nil {
		t.Fatal("expected PRD with no requirements to fail")
	}
	if !strings.Contains(err.Error(), "no requirements") {
		t.Fatalf("expected no requirements error, got %v", err)
	}
}

func TestInvalidPRDMissingFrontMatterFailsClosed(t *testing.T) {
	prd := `---
prd_id: prd-test
status: draft
task_id: task-test
---

# PRD: Test
`
	_, err := ParsePRDMarkdown([]byte(prd))
	if err == nil {
		t.Fatal("expected PRD missing schema_version/created_at to fail")
	}
	if !strings.Contains(err.Error(), "missing schema_version") {
		t.Fatalf("expected schema_version error, got %v", err)
	}
	if !strings.Contains(err.Error(), "missing created_at") {
		t.Fatalf("expected created_at error, got %v", err)
	}
}

func TestInvalidADRMissingDecisionFailsClosed(t *testing.T) {
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
	if err == nil {
		t.Fatal("expected ADR missing Decision section to fail")
	}
	if !strings.Contains(err.Error(), "missing ## Decision section") {
		t.Fatalf("expected Decision section error, got %v", err)
	}
}

func TestInvalidADRMissingStatusFailsClosed(t *testing.T) {
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
	if err == nil {
		t.Fatal("expected ADR missing status to fail")
	}
	if !strings.Contains(err.Error(), "missing status") {
		t.Fatalf("expected status error, got %v", err)
	}
}

func TestInvalidADRMissingPRDRefsFailsClosed(t *testing.T) {
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
	if err == nil {
		t.Fatal("expected ADR missing prd_refs to fail")
	}
	if !strings.Contains(err.Error(), "missing prd_refs") {
		t.Fatalf("expected prd_refs error, got %v", err)
	}
}

func TestInvalidADRIndexMissingIDPathStatusFailsClosed(t *testing.T) {
	idx := `{"schema_version":"v1","effort_id":"test","adrs":[{"title":"test","prd_refs":["REQ-1"]}]}`
	_, err := ParseADRIndexJSON([]byte(idx))
	if err == nil {
		t.Fatal("expected ADR index with missing fields to fail")
	}
	for _, want := range []string{"missing adr_id", "missing status", "missing path"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected error containing %q, got %v", want, err)
		}
	}
}

func TestInvalidADRIndexMissingPRDRefsFailsClosed(t *testing.T) {
	idx := `{"schema_version":"v1","effort_id":"test","adrs":[{"adr_id":"ADR-1","title":"test","status":"accepted","path":"adr.md"}]}`
	_, err := ParseADRIndexJSON([]byte(idx))
	if err == nil {
		t.Fatal("expected ADR index entry without prd_refs to fail")
	}
	if !strings.Contains(err.Error(), "missing prd_refs") {
		t.Fatalf("expected prd_refs error, got %v", err)
	}
}

func TestWorkflowStateFailsClosedForMissingRequiredFields(t *testing.T) {
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
	if err == nil {
		t.Fatal("expected missing workflow state fields to fail")
	}
	for _, want := range []string{"missing task_id", "missing current_state", "missing folder_path", "missing next_valid_actions"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected error to contain %q, got %v", want, err)
		}
	}
}

func TestWorkflowStateRejectsRawPrivacyExposureFlags(t *testing.T) {
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
	if err == nil {
		t.Fatal("expected privacy exposure flag to fail")
	}
	if !strings.Contains(err.Error(), "raw_prompts_stored") {
		t.Fatalf("expected raw prompt privacy error, got %v", err)
	}
}

func TestImplementationLedgerPlannedAtomRequiresModelReviewerAndScope(t *testing.T) {
	data := []byte(`{"record_type":"atom_planned","task_id":"impl-0002","effort_id":"prd-adr-brief-workflow","timestamp":"2026-05-17T15:40:22Z","complexity_tier":"C1","context_pack_id":"pack","implementor":{"id":"same","runtime":"opencode_attached_server","model":"","command":""},"reviewer":{"id":"same","model":"","reasoning_effort":"","runtime":"codex"},"scope":{"expected_files":[]},"required_verification":[]}`)

	_, err := ParseImplementationLedgerJSONL(data)
	if err == nil {
		t.Fatal("expected incomplete planned atom to fail")
	}
	for _, want := range []string{"implementor missing model", "implementor missing command", "reviewer missing model", "reviewer missing reasoning_effort", "same id", "scope missing expected_files", "scope missing forbidden files", "missing required_verification", "missing report_paths"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected error to contain %q, got %v", want, err)
		}
	}
}

func TestImplementationLedgerCloseoutRequiresVerificationReviewInvestigationAndNoBlockers(t *testing.T) {
	base := `{"record_type":"atom_planned","task_id":"impl-0002","effort_id":"prd-adr-brief-workflow","timestamp":"2026-05-17T15:40:22Z","complexity_tier":"C1","context_pack_id":"pack","implementor":{"id":"impl","runtime":"opencode_attached_server","model":"zai-coding-plan/glm-5.1","command":"run"},"reviewer":{"id":"review","model":"GPT-5.4","reasoning_effort":"medium","runtime":"codex"},"scope":{"expected_files":["internal/productworkflow/types.go"],"forbidden_patterns":["cmd/**"]},"required_verification":["go test ./internal/productworkflow"],"report_paths":{"implementation":"reports/impl.json","review":"reports/review.json","investigation":"reports/investigation.json"}}`
	closed := `{"record_type":"atom_closed","task_id":"impl-0002","effort_id":"prd-adr-brief-workflow","timestamp":"2026-05-17T15:45:00Z","open_blockers":["review_pending"]}`

	_, err := ParseImplementationLedgerJSONL([]byte(base + "\n" + closed))
	if err == nil {
		t.Fatal("expected premature atom closeout to fail")
	}
	for _, want := range []string{"without implementation_completed", "without verification_completed", "without review_completed", "without post_effort_investigation_completed", "with open blockers"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected error to contain %q, got %v", want, err)
		}
	}
}

func TestImplementationLedgerValidCloseoutAllowsFallbackCompletion(t *testing.T) {
	data := strings.Join([]string{
		`{"record_type":"atom_planned","task_id":"impl-0002","effort_id":"prd-adr-brief-workflow","timestamp":"2026-05-17T15:40:22Z","complexity_tier":"C1","context_pack_id":"pack","implementor":{"id":"impl","runtime":"opencode_attached_server","model":"zai-coding-plan/glm-5.1","command":"run"},"reviewer":{"id":"review","model":"GPT-5.4","reasoning_effort":"medium","runtime":"codex"},"scope":{"expected_files":["internal/productworkflow/types.go"],"forbidden_patterns":["cmd/**"]},"required_verification":["go test ./internal/productworkflow"],"report_paths":{"implementation":"reports/impl.json","review":"reports/review.json","investigation":"reports/investigation.json"}}`,
		`{"record_type":"fallback_completion","task_id":"impl-0002","effort_id":"prd-adr-brief-workflow","timestamp":"2026-05-17T15:45:00Z","actor":"codex-orchestrator"}`,
		`{"record_type":"verification_completed","task_id":"impl-0002","effort_id":"prd-adr-brief-workflow","timestamp":"2026-05-17T15:46:00Z"}`,
		`{"record_type":"review_completed","task_id":"impl-0002","effort_id":"prd-adr-brief-workflow","timestamp":"2026-05-17T15:47:00Z","reviewer_id":"review","model":"GPT-5.4","reasoning_effort":"medium"}`,
		`{"record_type":"post_effort_investigation_completed","task_id":"impl-0002","effort_id":"prd-adr-brief-workflow","timestamp":"2026-05-17T15:48:00Z"}`,
		`{"record_type":"atom_closed","task_id":"impl-0002","effort_id":"prd-adr-brief-workflow","timestamp":"2026-05-17T15:49:00Z","open_blockers":[]}`,
	}, "\n")

	if _, err := ParseImplementationLedgerJSONL([]byte(data)); err != nil {
		t.Fatalf("expected valid fallback closeout, got %v", err)
	}
}

func TestParseEvidenceCardMissingRequiredFieldsFailsClosed(t *testing.T) {
	card := `{"card_id":"EV-BAD","kind":"","source_handle":"","claim":"","confidence":"","freshness":"","evidence_refs":[],"researcher_id":"","timestamp":""}`
	_, err := ParseEvidenceCardsJSONL([]byte(card))
	if err == nil {
		t.Fatal("expected evidence card missing required fields to fail")
	}
	for _, want := range []string{"missing kind", "missing source_handle", "missing claim", "missing confidence", "missing freshness", "missing researcher_id", "missing timestamp", "missing evidence_refs"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected error containing %q, got %v", want, err)
		}
	}
}

func TestValidResearchProfilePassesValidation(t *testing.T) {
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
	if err != nil {
		t.Fatalf("expected valid research profile to pass, got %v", err)
	}
	if p.SchemaVersion != "idiom.research_profile.v1" {
		t.Fatalf("expected schema_version idiom.research_profile.v1, got %q", p.SchemaVersion)
	}
	if len(p.Researchers) != 2 {
		t.Fatalf("expected 2 researchers, got %d", len(p.Researchers))
	}
	if p.MergePolicy.Conflicts != "preserve_until_adr" {
		t.Fatalf("expected merge_policy.conflicts preserve_until_adr, got %q", p.MergePolicy.Conflicts)
	}
}

func TestResearchProfilePassesWithProfileIDInsteadOfName(t *testing.T) {
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
	if _, err := ParseResearchProfileJSON([]byte(profile)); err != nil {
		t.Fatalf("expected profile with profile_id to pass, got %v", err)
	}
}

func TestInvalidResearchProfileMissingIdentityFailsClosed(t *testing.T) {
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
	if err == nil {
		t.Fatal("expected profile missing name and profile_id to fail")
	}
	if !strings.Contains(err.Error(), "missing name or profile_id") {
		t.Fatalf("expected identity error, got %v", err)
	}
}

func TestInvalidResearchProfileMissingResearcherFieldsFailsClosed(t *testing.T) {
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
	if err == nil {
		t.Fatal("expected profile with empty researcher fields to fail")
	}
	for _, want := range []string{"missing id", "missing alignment", "missing question", "missing allowed_capabilities", "missing output"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected error containing %q, got %v", want, err)
		}
	}
}

func TestInvalidResearchProfileMissingMergePolicyFailsClosed(t *testing.T) {
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
	if err == nil {
		t.Fatal("expected profile with empty merge policy to fail")
	}
	for _, want := range []string{"missing merge_policy.conflicts", "missing merge_policy.unsupported_claims", "missing merge_policy.stale_local_context"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected error containing %q, got %v", want, err)
		}
	}
}

func TestInvalidResearchProfileNoResearchersFailsClosed(t *testing.T) {
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
	if err == nil {
		t.Fatal("expected profile with no researchers to fail")
	}
	if !strings.Contains(err.Error(), "missing researchers") {
		t.Fatalf("expected researchers error, got %v", err)
	}
}

func TestValidCurrentDurableTDDFixturePasses(t *testing.T) {
	root := repoRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "docs/product-workflows/efforts/closed/prd-adr-brief-workflow/tdd.json"))
	if err != nil {
		t.Fatalf("read tdd.json: %v", err)
	}
	ledger, err := ParseTDDLedgerJSON(data)
	if err != nil {
		t.Fatalf("expected current durable tdd.json to pass, got %v", err)
	}
	if ledger.CoverageTier != "unit" {
		t.Fatalf("expected coverage_tier unit, got %q", ledger.CoverageTier)
	}
	if ledger.Coverage.MutationMinScore != 80 {
		t.Fatalf("expected mutation_min_score 80, got %d", ledger.Coverage.MutationMinScore)
	}
	if len(ledger.Obligations) == 0 {
		t.Fatal("expected at least one obligation")
	}
}

func TestValidContractHighTDDWithCoverageMetadataPasses(t *testing.T) {
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
			DecisionTablesTotal:      1,
			DecisionTablesEnumerated: 1,
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
	if err := validateTDDLedger(ledger); err != nil {
		t.Fatalf("expected valid contract_high TDD to pass, got %v", err)
	}
}

func TestInvalidTDDNegativeCounterFailsClosed(t *testing.T) {
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
	if err == nil {
		t.Fatal("expected negative counters to fail")
	}
	if !strings.Contains(err.Error(), "ears_total must be non-negative") {
		t.Fatalf("expected ears_total error, got %v", err)
	}
	if !strings.Contains(err.Error(), "bdd_with_concrete_data must be non-negative") {
		t.Fatalf("expected bdd_with_concrete_data error, got %v", err)
	}
}

func TestInvalidTDDMappedExceedsTotalFailsClosed(t *testing.T) {
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
	if err == nil {
		t.Fatal("expected mapped>total to fail")
	}
	if !strings.Contains(err.Error(), "ears_mapped (5) exceeds ears_total (3)") {
		t.Fatalf("expected ears_mapped exceeds error, got %v", err)
	}
	if !strings.Contains(err.Error(), "fmea_modes_mitigated (4) exceeds fmea_modes_total (2)") {
		t.Fatalf("expected fmea_modes_mitigated exceeds error, got %v", err)
	}
}

func TestInvalidTDDUnknownCoverageTierFailsClosed(t *testing.T) {
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
	if err == nil {
		t.Fatal("expected unknown coverage_tier to fail")
	}
	if !strings.Contains(err.Error(), "invalid coverage_tier") {
		t.Fatalf("expected coverage_tier error, got %v", err)
	}
}

func TestInvalidTDDUnknownObligationTypeFailsClosed(t *testing.T) {
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
	if err == nil {
		t.Fatal("expected unknown obligation type to fail")
	}
	if !strings.Contains(err.Error(), "invalid type") {
		t.Fatalf("expected invalid type error, got %v", err)
	}
}

func TestInvalidTDDMutationScoreOutOfBoundsFailsClosed(t *testing.T) {
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
	if err == nil {
		t.Fatal("expected mutation_score out of bounds to fail")
	}
	if !strings.Contains(err.Error(), "mutation_score must be 0..100") {
		t.Fatalf("expected mutation_score bounds error, got %v", err)
	}
}

func TestInvalidTDDMutationScoreBelowMinWithoutGateBlockedFailsClosed(t *testing.T) {
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
	if err == nil {
		t.Fatal("expected mutation_score below min without gate block to fail")
	}
	if !strings.Contains(err.Error(), "below mutation_min_score") {
		t.Fatalf("expected below min_score error, got %v", err)
	}
}

func TestValidTDDMutationScoreBelowMinWithGateBlockedPasses(t *testing.T) {
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
	if err := validateTDDLedger(ledger); err != nil {
		t.Fatalf("expected mutation score below min with gate blocked to pass, got %v", err)
	}
}

func TestInvalidTDDNegativeMutationScoreFailsClosed(t *testing.T) {
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
	if err == nil {
		t.Fatal("expected negative mutation_score to fail")
	}
	if !strings.Contains(err.Error(), "mutation_score must be 0..100") {
		t.Fatalf("expected mutation_score bounds error, got %v", err)
	}
}

func TestInvalidTDDZeroMutationMinScoreFailsClosed(t *testing.T) {
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
	if err == nil {
		t.Fatal("expected zero mutation_min_score to fail")
	}
	if !strings.Contains(err.Error(), "missing positive mutation_min_score") {
		t.Fatalf("expected mutation_min_score error, got %v", err)
	}
}

func TestValidAllCoverageTiersAccepted(t *testing.T) {
	for _, tier := range []string{"documentation", "local_low", "behavior_standard", "contract_high", "formal_optional", "unit"} {
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
		if err := validateTDDLedger(ledger); err != nil {
			t.Fatalf("expected coverage_tier %q to pass, got %v", tier, err)
		}
	}
}

func TestADRInvalidCoverageTierFailsClosed(t *testing.T) {
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
	if err == nil {
		t.Fatal("expected invalid ADR coverage tier to fail")
	}
	if !strings.Contains(err.Error(), "invalid coverage_tier") {
		t.Fatalf("expected coverage tier error, got %v", err)
	}
}

func TestADRIndexInvalidCoverageTierFailsClosed(t *testing.T) {
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
	if err == nil {
		t.Fatal("expected invalid ADR index coverage tier to fail")
	}
	if !strings.Contains(err.Error(), "invalid coverage_tier") {
		t.Fatalf("expected coverage tier error, got %v", err)
	}
}

func TestValidAllObligationTypesAccepted(t *testing.T) {
	types := []string{"characterization", "regression", "acceptance", "contract", "source_sanity", "eval", "audit", "property_based", "fmea_negative", "hazop_deviation", "decision_table", "boundary_value", "mutation_score", "post_mortem", "manual"}
	for _, typ := range types {
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
		if err := validateTDDLedger(ledger); err != nil {
			t.Fatalf("expected obligation type %q to pass, got %v", typ, err)
		}
	}
}

func TestDurableTraceabilityMatrixPassesCrossLinkValidation(t *testing.T) {
	root := repoRoot(t)
	read := func(path string) []byte {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		return data
	}
	rel := "docs/product-workflows/efforts/closed/prd-adr-brief-workflow"

	matrixData := read(filepath.Join(rel, "evidence/traceability_matrix.json"))
	matrix, err := ParseTraceabilityMatrixJSON(matrixData)
	if err != nil {
		t.Fatalf("parse traceability matrix: %v", err)
	}

	cardsData := read(filepath.Join(rel, "evidence/evidence.jsonl"))
	cards, err := ParseEvidenceCardsJSONL(cardsData)
	if err != nil {
		t.Fatalf("parse evidence cards: %v", err)
	}
	evidenceIDs := map[string]bool{}
	for _, c := range cards {
		evidenceIDs[c.CardID] = true
	}

	prdData := read(filepath.Join(rel, "prd.md"))
	prd, err := ParsePRDMarkdown(prdData)
	if err != nil {
		t.Fatalf("parse PRD: %v", err)
	}
	prdReqIDs := map[string]bool{}
	for _, r := range prd.Requirements {
		prdReqIDs[r.ID] = true
	}

	adrIdxData := read(filepath.Join(rel, "adrs/index.json"))
	adrIdx, err := ParseADRIndexJSON(adrIdxData)
	if err != nil {
		t.Fatalf("parse ADR index: %v", err)
	}
	adrIDs := map[string]bool{}
	for _, a := range adrIdx.ADRs {
		adrIDs[a.ADRID] = true
	}

	tddData := read(filepath.Join(rel, "tdd.json"))
	tdd, err := ParseTDDLedgerJSON(tddData)
	if err != nil {
		t.Fatalf("parse TDD: %v", err)
	}
	tddIDs := map[string]bool{}
	for _, o := range tdd.Obligations {
		tddIDs[o.ID] = true
	}

	ledgerData := read(filepath.Join(rel, "implementation_ledger.jsonl"))
	ledger, err := ParseImplementationLedgerJSONL(ledgerData)
	if err != nil {
		t.Fatalf("parse implementation ledger: %v", err)
	}
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
	if len(linkErrs) != 0 {
		for _, le := range linkErrs {
			t.Errorf("unexpected broken link: row %d field %s value %q: %s", le.RowIndex, le.Field, le.Value, le.Message)
		}
		t.Fatalf("expected zero link errors, got %d", len(linkErrs))
	}
}

func TestCrossLinkValidationDetectsBrokenEvidenceCard(t *testing.T) {
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
	if len(errs) != 1 || errs[0].Field != "evidence_card" || !strings.Contains(errs[0].Message, "EV-MISSING") {
		t.Fatalf("expected one evidence_card error, got %v", errs)
	}
}

func TestCrossLinkValidationDetectsBrokenPRDRequirement(t *testing.T) {
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
	if len(errs) != 1 || errs[0].Field != "prd_requirement" {
		t.Fatalf("expected one prd_requirement error, got %v", errs)
	}
}

func TestCrossLinkValidationDetectsBrokenADR(t *testing.T) {
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
	if len(errs) != 1 || errs[0].Field != "adr" {
		t.Fatalf("expected one adr error, got %v", errs)
	}
}

func TestCrossLinkValidationDetectsBrokenTDDObligation(t *testing.T) {
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
	if len(errs) != 1 || errs[0].Field != "tdd_obligation" {
		t.Fatalf("expected one tdd_obligation error, got %v", errs)
	}
}

func TestCrossLinkValidationAcceptsHistoricalTDDObligationIDs(t *testing.T) {
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
	if len(errs) != 0 {
		t.Fatalf("expected zero errors for historical TDD ID, got %v", errs)
	}
}

func TestCrossLinkValidationDetectsBrokenBriefPath(t *testing.T) {
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
	if len(errs) != 1 || errs[0].Field != "brief" {
		t.Fatalf("expected one brief error, got %v", errs)
	}
}

func TestCrossLinkValidationDetectsBrokenReportPath(t *testing.T) {
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
	if len(errs) != 1 || errs[0].Field != "report" {
		t.Fatalf("expected one report error, got %v", errs)
	}
}

func TestCrossLinkValidationSkipsCheckForNilMaps(t *testing.T) {
	matrix := TraceabilityMatrix{
		SchemaVersion: "v1", EffortID: "e", TaskID: "t", CreatedAt: "now",
		Matrix: []TraceabilityRow{
			{EvidenceCard: "EV-1", PRDRequirement: "REQ-1", ADR: "ADR-1", TDDObligation: "TDD-1", Brief: "briefs/a.md", Report: "reports/a.json"},
		},
	}
	errs := ValidateTraceabilityCrossLinks(matrix, nil, nil, nil, nil, nil, nil)
	if len(errs) != 0 {
		t.Fatalf("expected zero errors when all maps nil (artifact parse failed), got %v", errs)
	}
}

func TestTraceabilityRowMissingFieldsFailClosed(t *testing.T) {
	m := TraceabilityMatrix{
		SchemaVersion: "v1", EffortID: "e", TaskID: "t", CreatedAt: "now",
		Matrix: []TraceabilityRow{
			{EvidenceCard: "", PRDRequirement: "", ADR: "", TDDObligation: "", Brief: "", Report: ""},
		},
	}
	err := validateTraceabilityMatrix(m)
	if err == nil {
		t.Fatal("expected empty row fields to fail")
	}
	for _, want := range []string{"missing evidence_card", "missing prd_requirement", "missing adr", "missing tdd_obligation", "missing brief", "missing report"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("expected error containing %q, got %v", want, err)
		}
	}
}

func TestValidateArtifactStateAcceptsCanonicalStates(t *testing.T) {
	for _, s := range []string{"draft", "audited", "accepted", "blocked", "dispatched"} {
		if err := ValidateArtifactState(s); err != nil {
			t.Fatalf("expected status %q to pass, got %v", s, err)
		}
	}
}

func TestValidateArtifactStateRejectsInvalidStates(t *testing.T) {
	for _, s := range []string{"accepted_fixture", "unknown", "pending", "reviewed", "closed", "ACTIVE", ""} {
		err := ValidateArtifactState(s)
		if s == "" {
			if err == nil {
				t.Fatalf("expected empty status to fail")
			}
			if !strings.Contains(err.Error(), "empty") {
				t.Fatalf("expected empty error, got %v", err)
			}
		} else {
			if err == nil {
				t.Fatalf("expected status %q to fail", s)
			}
			if !strings.Contains(err.Error(), s) {
				t.Fatalf("expected error to mention %q, got %v", s, err)
			}
			if !strings.Contains(err.Error(), "valid states") {
				t.Fatalf("expected error to mention valid states, got %v", err)
			}
		}
	}
}

func TestValidateArtifactStateRejectsAcceptedFixtureExplicitly(t *testing.T) {
	err := ValidateArtifactState("accepted_fixture")
	if err == nil {
		t.Fatal("expected accepted_fixture to be rejected")
	}
	if !strings.Contains(err.Error(), "accepted_fixture") {
		t.Fatalf("expected error to mention accepted_fixture, got %v", err)
	}
}

func TestInvalidPRDStatusFailsClosed(t *testing.T) {
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
	if err == nil {
		t.Fatal("expected PRD with invalid status to fail")
	}
	if !strings.Contains(err.Error(), "accepted_fixture") {
		t.Fatalf("expected error mentioning accepted_fixture, got %v", err)
	}
	if !strings.Contains(err.Error(), "valid states") {
		t.Fatalf("expected error mentioning valid states, got %v", err)
	}
}

func TestDurablePRDFixtureStillPasses(t *testing.T) {
	root := repoRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "docs/product-workflows/efforts/closed/prd-adr-brief-workflow/prd.md"))
	if err != nil {
		t.Fatalf("read prd.md: %v", err)
	}
	if _, err := ParsePRDMarkdown(data); err != nil {
		t.Fatalf("expected durable PRD fixture to pass, got %v", err)
	}
}

func TestInvalidADRIndexStatusFailsClosed(t *testing.T) {
	idx := `{"schema_version":"v1","effort_id":"test","adrs":[{"adr_id":"ADR-1","title":"Test","status":"accepted_fixture","path":"a.md","prd_refs":["REQ-1"]}]}`
	_, err := ParseADRIndexJSON([]byte(idx))
	if err == nil {
		t.Fatal("expected ADR index with invalid status to fail")
	}
	if !strings.Contains(err.Error(), "accepted_fixture") {
		t.Fatalf("expected error mentioning accepted_fixture, got %v", err)
	}
}

func TestDurableADRIndexFixtureStillPasses(t *testing.T) {
	root := repoRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "docs/product-workflows/efforts/closed/prd-adr-brief-workflow/adrs/index.json"))
	if err != nil {
		t.Fatalf("read ADR index: %v", err)
	}
	if _, err := ParseADRIndexJSON(data); err != nil {
		t.Fatalf("expected durable ADR index fixture to pass, got %v", err)
	}
}

func TestInvalidADRMarkdownStatusFailsClosed(t *testing.T) {
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
	if err == nil {
		t.Fatal("expected ADR with invalid status to fail")
	}
	if !strings.Contains(err.Error(), "accepted_fixture") {
		t.Fatalf("expected error mentioning accepted_fixture, got %v", err)
	}
}

func TestDurableADRMarkdownFixtureStillPasses(t *testing.T) {
	root := repoRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "docs/product-workflows/efforts/closed/prd-adr-brief-workflow/adrs/ADR-0001-durable-workflow-substrate.md"))
	if err != nil {
		t.Fatalf("read ADR markdown: %v", err)
	}
	if _, err := ParseADRMarkdown(data); err != nil {
		t.Fatalf("expected durable ADR markdown fixture to pass, got %v", err)
	}
}

func TestPRDWithEachCanonicalStatusPasses(t *testing.T) {
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
		if _, err := ParsePRDMarkdown([]byte(prd)); err != nil {
			t.Fatalf("expected PRD status %q to pass, got %v", status, err)
		}
	}
}

func TestADRIndexWithEachCanonicalStatusPasses(t *testing.T) {
	for _, status := range []string{"draft", "audited", "accepted", "blocked", "dispatched"} {
		idx := fmt.Sprintf(`{"schema_version":"v1","effort_id":"test","adrs":[{"adr_id":"ADR-1","title":"Test","status":"%s","path":"a.md","prd_refs":["REQ-1"]}]}`, status)
		if _, err := ParseADRIndexJSON([]byte(idx)); err != nil {
			t.Fatalf("expected ADR index status %q to pass, got %v", status, err)
		}
	}
}

func TestADRMarkdownWithEachCanonicalStatusPasses(t *testing.T) {
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
		if _, err := ParseADRMarkdown([]byte(adr)); err != nil {
			t.Fatalf("expected ADR markdown status %q to pass, got %v", status, err)
		}
	}
}

func TestValidImplementationReportPasses(t *testing.T) {
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
	if err := validateImplementationReport(report); err != nil {
		t.Fatalf("expected valid implementation report to pass, got %v", err)
	}
}

func TestInvalidImplementationReportMissingFieldsFailsClosed(t *testing.T) {
	report := ImplementationReport{}
	err := validateImplementationReport(report)
	if err == nil {
		t.Fatal("expected empty implementation report to fail")
	}
	for _, want := range []string{
		"missing schema_version", "missing task_id", "missing effort_id",
		"missing implementor_id", "missing runtime", "missing model",
		"missing complexity_tier", "missing scope_statement", "missing reviewer_id",
		"missing verification.status", "missing privacy_note",
		"missing open_blockers",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected error containing %q, got %v", want, err)
		}
	}
}

func TestInvalidImplementationReportBadComplexityTierFailsClosed(t *testing.T) {
	report := ImplementationReport{
		SchemaVersion: "v1", TaskID: "t", EffortID: "e", ImplementorID: "i",
		Runtime: "r", Model: "m", ComplexityTier: "C9", ScopeStatement: "s",
		ReviewerID: "rev", Verification: VerificationState{Status: "pending"},
		PrivacyNote: "safe", OpenBlockers: []string{},
	}
	err := validateImplementationReport(report)
	if err == nil {
		t.Fatal("expected invalid complexity_tier to fail")
	}
	if !strings.Contains(err.Error(), "invalid complexity_tier") {
		t.Fatalf("expected complexity_tier error, got %v", err)
	}
}

func TestInvalidImplementationReportVerificationNotPendingRequiresCommands(t *testing.T) {
	report := ImplementationReport{
		SchemaVersion: "v1", TaskID: "t", EffortID: "e", ImplementorID: "i",
		Runtime: "r", Model: "m", ComplexityTier: "C1", ScopeStatement: "s",
		ReviewerID: "rev", Verification: VerificationState{Status: "passed", Commands: nil},
		PrivacyNote: "safe", OpenBlockers: []string{},
	}
	err := validateImplementationReport(report)
	if err == nil {
		t.Fatal("expected verification.commands required when status not pending")
	}
	if !strings.Contains(err.Error(), "requires verification.commands") {
		t.Fatalf("expected verification.commands error, got %v", err)
	}
}

func TestInvalidImplementationReportCodeChangesRequiresChangedPaths(t *testing.T) {
	report := ImplementationReport{
		SchemaVersion: "v1", TaskID: "t", EffortID: "e", ImplementorID: "i",
		Runtime: "r", Model: "m", ComplexityTier: "C1", ScopeStatement: "s",
		ReviewerID: "rev", CodeChanges: true, ChangedPaths: nil,
		Verification: VerificationState{Status: "pending"},
		PrivacyNote:  "safe", OpenBlockers: []string{},
	}
	err := validateImplementationReport(report)
	if err == nil {
		t.Fatal("expected changed_paths required when code_changes true")
	}
	if !strings.Contains(err.Error(), "changed_paths is empty") {
		t.Fatalf("expected changed_paths error, got %v", err)
	}
}

func TestInvalidImplementationReportPrivacyNoteClaimsStoredFailsClosed(t *testing.T) {
	report := ImplementationReport{
		SchemaVersion: "v1", TaskID: "t", EffortID: "e", ImplementorID: "i",
		Runtime: "r", Model: "m", ComplexityTier: "C1", ScopeStatement: "s",
		ReviewerID: "rev", Verification: VerificationState{Status: "pending"},
		PrivacyNote: "raw_prompts_stored: true was recorded", OpenBlockers: []string{},
	}
	err := validateImplementationReport(report)
	if err == nil {
		t.Fatal("expected privacy note claiming raw_prompts_stored to fail")
	}
	if !strings.Contains(err.Error(), "raw_prompts_stored") {
		t.Fatalf("expected privacy claim error, got %v", err)
	}
}

func TestImplementationReportAllComplexityTiersAccepted(t *testing.T) {
	for _, tier := range []string{"C0", "C1", "C2", "C3", "C4"} {
		report := ImplementationReport{
			SchemaVersion: "v1", TaskID: "t", EffortID: "e", ImplementorID: "i",
			Runtime: "r", Model: "m", ComplexityTier: tier, ScopeStatement: "s",
			ReviewerID: "rev", Verification: VerificationState{Status: "pending"},
			PrivacyNote: "safe", OpenBlockers: []string{},
		}
		if err := validateImplementationReport(report); err != nil {
			t.Fatalf("expected complexity_tier %q to pass, got %v", tier, err)
		}
	}
}

func TestValidReviewReportPasses(t *testing.T) {
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
		Findings:         []any{},
		Checks:           map[string]any{"scope": "passed"},
		ReviewedAt:       "2026-05-17T16:00:00Z",
		CommandsReviewed: []string{"go test ./..."},
		CreatedAt:        "2026-05-17T15:00:00Z",
	}
	if err := validateReviewReport(report); err != nil {
		t.Fatalf("expected valid review report to pass, got %v", err)
	}
}

func TestInvalidReviewReportMissingFieldsFailsClosed(t *testing.T) {
	report := ReviewReport{}
	err := validateReviewReport(report)
	if err == nil {
		t.Fatal("expected empty review report to fail")
	}
	for _, want := range []string{
		"missing schema_version", "missing effort_id", "missing task_id",
		"missing reviewer.agent_id", "missing reviewer.model",
		"missing reviewer.reasoning_effort", "missing status",
		"independent_from_implementor must be true",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected error containing %q, got %v", want, err)
		}
	}
}

func TestInvalidReviewReportNotIndependentFailsClosed(t *testing.T) {
	report := ReviewReport{
		SchemaVersion: "v1", EffortID: "e", TaskID: "t", Status: "pending",
		Reviewer: ReviewerInfo{
			AgentID: "r", Model: "m", ReasoningEffort: "low",
			IndependentFromImplementor: false,
		},
	}
	err := validateReviewReport(report)
	if err == nil {
		t.Fatal("expected reviewer not independent to fail")
	}
	if !strings.Contains(err.Error(), "independent_from_implementor must be true") {
		t.Fatalf("expected independence error, got %v", err)
	}
}

func TestInvalidReviewReportNotPendingRequiresCloseoutFailsClosed(t *testing.T) {
	report := ReviewReport{
		SchemaVersion: "v1", EffortID: "e", TaskID: "t", Status: "passed",
		Reviewer: ReviewerInfo{
			AgentID: "r", Model: "m", ReasoningEffort: "low",
			IndependentFromImplementor: true,
		},
	}
	err := validateReviewReport(report)
	if err == nil {
		t.Fatal("expected non-pending review without closeout to fail")
	}
	for _, want := range []string{"missing reviewed_at", "missing commands_reviewed", "missing checks"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected error containing %q, got %v", want, err)
		}
	}
}

func TestValidReviewReportPendingAcceptsIncompleteFields(t *testing.T) {
	report := ReviewReport{
		SchemaVersion: "v1", EffortID: "e", TaskID: "t", Status: "pending",
		Reviewer: ReviewerInfo{
			AgentID: "r", Model: "m", ReasoningEffort: "low",
			IndependentFromImplementor: true,
		},
	}
	if err := validateReviewReport(report); err != nil {
		t.Fatalf("expected pending review report to pass, got %v", err)
	}
}

func TestValidInvestigationReportCompletePasses(t *testing.T) {
	report := InvestigationReport{
		SchemaVersion:    "idiom.investigation_report.v1",
		EffortID:         "effort-1",
		TaskID:           "impl-0001",
		Status:           "complete",
		CreatedAt:        "2026-05-17T15:00:00Z",
		Scope:            map[string]any{"files": []any{"a.go"}},
		Tests:            map[string]any{"focused": "go test"},
		Coverage:         map[string]any{"tier": "unit"},
		Traceability:     map[string]any{"checked": true},
		PrivacyAudit:     map[string]any{"no_raw_prompts": true},
		ReviewerFindings: []any{},
		Blockers:         []string{},
		NextValidAction:  "close_atom",
		CompletedAt:      "2026-05-17T16:00:00Z",
	}
	if err := validateInvestigationReport(report); err != nil {
		t.Fatalf("expected valid complete investigation to pass, got %v", err)
	}
}

func TestInvalidInvestigationReportMissingFieldsFailsClosed(t *testing.T) {
	report := InvestigationReport{}
	err := validateInvestigationReport(report)
	if err == nil {
		t.Fatal("expected empty investigation report to fail")
	}
	for _, want := range []string{"missing schema_version", "missing effort_id", "missing task_id", "missing status"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected error containing %q, got %v", want, err)
		}
	}
}

func TestInvalidInvestigationReportCompleteWithBlockersFailsClosed(t *testing.T) {
	report := InvestigationReport{
		SchemaVersion: "v1", EffortID: "e", TaskID: "t", Status: "complete",
		CompletedAt: "2026-05-17T16:00:00Z", NextValidAction: "close",
		Scope: map[string]any{"a": 1}, Tests: map[string]any{"a": 1},
		Coverage: map[string]any{"a": 1}, Traceability: map[string]any{"a": 1},
		PrivacyAudit: map[string]any{"a": 1},
		Blockers:     []string{"still_broken"},
	}
	err := validateInvestigationReport(report)
	if err == nil {
		t.Fatal("expected complete investigation with blockers to fail")
	}
	if !strings.Contains(err.Error(), "blockers is non-empty") {
		t.Fatalf("expected blockers error, got %v", err)
	}
}

func TestInvalidInvestigationReportCompleteMissingCloseoutFieldsFailsClosed(t *testing.T) {
	report := InvestigationReport{
		SchemaVersion: "v1", EffortID: "e", TaskID: "t", Status: "complete",
	}
	err := validateInvestigationReport(report)
	if err == nil {
		t.Fatal("expected complete investigation without closeout to fail")
	}
	for _, want := range []string{
		"missing completed_at", "missing next_valid_action",
		"missing scope", "missing tests", "missing coverage",
		"missing traceability", "missing privacy_audit",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected error containing %q, got %v", want, err)
		}
	}
}

func TestValidInvestigationReportPendingAcceptsIncompleteFields(t *testing.T) {
	report := InvestigationReport{
		SchemaVersion: "v1", EffortID: "e", TaskID: "t", Status: "pending",
		CreatedAt: "2026-05-17T15:00:00Z",
		Blockers:  []string{"implementation_pending"},
	}
	if err := validateInvestigationReport(report); err != nil {
		t.Fatalf("expected pending investigation to pass, got %v", err)
	}
}

func TestInvalidInvestigationReportPrivacyAuditRawPromptsStoredFailsClosed(t *testing.T) {
	report := InvestigationReport{
		SchemaVersion: "v1", EffortID: "e", TaskID: "t", Status: "complete",
		CompletedAt: "2026-05-17T16:00:00Z", NextValidAction: "close",
		Scope: map[string]any{"a": 1}, Tests: map[string]any{"a": 1},
		Coverage: map[string]any{"a": 1}, Traceability: map[string]any{"a": 1},
		PrivacyAudit: map[string]any{"raw_prompts_stored": true},
		Blockers:     []string{},
	}
	err := validateInvestigationReport(report)
	if err == nil {
		t.Fatal("expected complete investigation without closeout to fail")
	}
	if !strings.Contains(err.Error(), "raw_prompts_stored must not be true") {
		t.Fatalf("expected raw_prompts_stored error, got %v", err)
	}
}

func TestInvalidInvestigationReportPrivacyAuditNoRawPromptsFalseFailsClosed(t *testing.T) {
	report := InvestigationReport{
		SchemaVersion: "v1", EffortID: "e", TaskID: "t", Status: "complete",
		CompletedAt: "2026-05-17T16:00:00Z", NextValidAction: "close",
		Scope: map[string]any{"a": 1}, Tests: map[string]any{"a": 1},
		Coverage: map[string]any{"a": 1}, Traceability: map[string]any{"a": 1},
		PrivacyAudit: map[string]any{"no_raw_prompts": false},
		Blockers:     []string{},
	}
	err := validateInvestigationReport(report)
	if err == nil {
		t.Fatal("expected privacy_audit no_raw_prompts=false to fail")
	}
	if !strings.Contains(err.Error(), "no_raw_prompts must not be false") {
		t.Fatalf("expected no_raw_prompts error, got %v", err)
	}
}

func TestValidInvestigationReportPrivacyAuditNoFieldsTruePasses(t *testing.T) {
	report := InvestigationReport{
		SchemaVersion: "v1", EffortID: "e", TaskID: "t", Status: "complete",
		CompletedAt: "2026-05-17T16:00:00Z", NextValidAction: "close",
		Scope: map[string]any{"a": 1}, Tests: map[string]any{"a": 1},
		Coverage: map[string]any{"a": 1}, Traceability: map[string]any{"a": 1},
		PrivacyAudit: map[string]any{
			"no_raw_prompts": true, "no_raw_transcripts": true,
			"no_file_bodies": true, "no_secrets": true, "no_customer_data": true,
		},
		Blockers: []string{},
	}
	if err := validateInvestigationReport(report); err != nil {
		t.Fatalf("expected valid privacy_audit with no_* fields true to pass, got %v", err)
	}
}

func TestInvalidInvestigationReportPrivacyAuditAllStoredClaimsFailsClosed(t *testing.T) {
	report := InvestigationReport{
		SchemaVersion: "v1", EffortID: "e", TaskID: "t", Status: "complete",
		CompletedAt: "2026-05-17T16:00:00Z", NextValidAction: "close",
		Scope: map[string]any{"a": 1}, Tests: map[string]any{"a": 1},
		Coverage: map[string]any{"a": 1}, Traceability: map[string]any{"a": 1},
		PrivacyAudit: map[string]any{
			"raw_prompts_stored": true, "raw_transcripts_stored": true,
			"file_bodies_stored": true, "secrets_stored": true,
			"customer_identifying_data_stored": true,
		},
		Blockers: []string{},
	}
	err := validateInvestigationReport(report)
	if err == nil {
		t.Fatal("expected all stored claims to fail")
	}
	for _, want := range []string{
		"raw_prompts_stored must not be true",
		"raw_transcripts_stored must not be true",
		"file_bodies_stored must not be true",
		"secrets_stored must not be true",
		"customer_identifying_data_stored must not be true",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected error containing %q, got %v", want, err)
		}
	}
}

func TestInvalidInvestigationReportPrivacyAuditAllNoFieldsFalseFailsClosed(t *testing.T) {
	report := InvestigationReport{
		SchemaVersion: "v1", EffortID: "e", TaskID: "t", Status: "complete",
		CompletedAt: "2026-05-17T16:00:00Z", NextValidAction: "close",
		Scope: map[string]any{"a": 1}, Tests: map[string]any{"a": 1},
		Coverage: map[string]any{"a": 1}, Traceability: map[string]any{"a": 1},
		PrivacyAudit: map[string]any{
			"no_raw_prompts": false, "no_raw_transcripts": false,
			"no_file_bodies": false, "no_secrets": false, "no_customer_data": false,
		},
		Blockers: []string{},
	}
	err := validateInvestigationReport(report)
	if err == nil {
		t.Fatal("expected all no_* fields false to fail")
	}
	for _, want := range []string{
		"no_raw_prompts must not be false",
		"no_raw_transcripts must not be false",
		"no_file_bodies must not be false",
		"no_secrets must not be false",
		"no_customer_data must not be false",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected error containing %q, got %v", want, err)
		}
	}
}

func TestValidInvestigationReportPrivacyAuditCheckedNoStoredClaimsPasses(t *testing.T) {
	report := InvestigationReport{
		SchemaVersion: "v1", EffortID: "e", TaskID: "t", Status: "complete",
		CompletedAt: "2026-05-17T16:00:00Z", NextValidAction: "close",
		Scope: map[string]any{"a": 1}, Tests: map[string]any{"a": 1},
		Coverage: map[string]any{"a": 1}, Traceability: map[string]any{"a": 1},
		PrivacyAudit: map[string]any{"checked": true, "exposure_found": false},
		Blockers:     []string{},
	}
	if err := validateInvestigationReport(report); err != nil {
		t.Fatalf("expected privacy_audit with checked/exposure_found to pass, got %v", err)
	}
}

func TestClassifyReportFileNameKnownSuffixes(t *testing.T) {
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
		if gotType != tc.wantType || gotKnown != tc.wantKnown {
			t.Errorf("ClassifyReportFileName(%q) = (%q, %v), want (%q, %v)",
				tc.name, gotType, gotKnown, tc.wantType, tc.wantKnown)
		}
	}
}

func TestDurableImplementationReportFixturesPassValidation(t *testing.T) {
	root := repoRoot(t)
	reportDir := filepath.Join(root, "docs/product-workflows/efforts/closed/prd-adr-brief-workflow/reports")
	entries, err := os.ReadDir(reportDir)
	if err != nil {
		t.Fatalf("read reports dir: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), "-implementation.json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(reportDir, entry.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name(), err)
		}
		if _, err := ParseImplementationReportJSON(data); err != nil {
			t.Fatalf("expected %s to pass implementation report validation, got %v", entry.Name(), err)
		}
	}
}

func TestDurableReviewReportFixturesPassValidation(t *testing.T) {
	root := repoRoot(t)
	reportDir := filepath.Join(root, "docs/product-workflows/efforts/closed/prd-adr-brief-workflow/reports")
	entries, err := os.ReadDir(reportDir)
	if err != nil {
		t.Fatalf("read reports dir: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), "-review.json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(reportDir, entry.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name(), err)
		}
		if _, err := ParseReviewReportJSON(data); err != nil {
			t.Fatalf("expected %s to pass review report validation, got %v", entry.Name(), err)
		}
	}
}

func TestDurableInvestigationReportFixturesPassValidation(t *testing.T) {
	root := repoRoot(t)
	reportDir := filepath.Join(root, "docs/product-workflows/efforts/closed/prd-adr-brief-workflow/reports")
	entries, err := os.ReadDir(reportDir)
	if err != nil {
		t.Fatalf("read reports dir: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), "-investigation.json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(reportDir, entry.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name(), err)
		}
		if _, err := ParseInvestigationReportJSON(data); err != nil {
			t.Fatalf("expected %s to pass investigation report validation, got %v", entry.Name(), err)
		}
	}
}

func TestValidPRDPreservesNewFields(t *testing.T) {
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
	if err != nil {
		t.Fatalf("expected valid PRD to pass, got %v", err)
	}
	if len(doc.Requirements) == 0 {
		t.Fatal("expected at least one requirement")
	}
	req := doc.Requirements[0]
	if req.OwnerAlignment != "team" {
		t.Fatalf("expected OwnerAlignment team, got %q", req.OwnerAlignment)
	}
	if req.Testability != "automated" {
		t.Fatalf("expected Testability automated, got %q", req.Testability)
	}
	if req.Metric != "coverage > 80%" {
		t.Fatalf("expected Metric coverage > 80%%, got %q", req.Metric)
	}
	if !req.OpenQuestionsPresent {
		t.Fatal("expected OpenQuestionsPresent true")
	}
	if req.OpenQuestions != nil {
		t.Fatalf("expected empty OpenQuestions, got %v", req.OpenQuestions)
	}
}

func TestMissingMetricFailsClosed(t *testing.T) {
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
	if err == nil {
		t.Fatal("expected PRD missing metric to fail")
	}
	if !strings.Contains(err.Error(), "missing metric") {
		t.Fatalf("expected missing metric error, got %v", err)
	}
}

func TestMissingOpenQuestionsFieldFailsClosed(t *testing.T) {
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
	if err == nil {
		t.Fatal("expected PRD missing open_questions to fail")
	}
	if !strings.Contains(err.Error(), "missing open_questions") {
		t.Fatalf("expected missing open_questions error, got %v", err)
	}
}

func TestPRDMissingRequirementFieldsFailsClosed(t *testing.T) {
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
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParsePRDMarkdown([]byte(tc.prd))
			if err == nil {
				t.Fatalf("expected PRD %s to fail", tc.name)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestADRParsesRequiredFrontMatterFields(t *testing.T) {
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
	if err != nil {
		t.Fatalf("expected valid ADR to parse: %v", err)
	}
	fm := doc.FrontMatter
	if fm.SchemaVersion != "idiom.adr.v1" {
		t.Fatalf("expected schema_version idiom.adr.v1, got %q", fm.SchemaVersion)
	}
	if fm.ADRID != "ADR-TEST" {
		t.Fatalf("expected adr_id ADR-TEST, got %q", fm.ADRID)
	}
	if fm.Status != "accepted" {
		t.Fatalf("expected status accepted, got %q", fm.Status)
	}
	if len(fm.PRDRefs) != 1 || fm.PRDRefs[0] != "REQ-1" {
		t.Fatalf("expected prd_refs [REQ-1], got %v", fm.PRDRefs)
	}
	if len(fm.EvidenceRefs) != 1 || fm.EvidenceRefs[0] != "EV-1" {
		t.Fatalf("expected evidence_refs [EV-1], got %v", fm.EvidenceRefs)
	}
	if len(fm.TDDObligations) != 1 || fm.TDDObligations[0] != "TDD-1" {
		t.Fatalf("expected tdd_obligations [TDD-1], got %v", fm.TDDObligations)
	}
	if fm.CoverageTier != "unit" {
		t.Fatalf("expected coverage_tier unit, got %q", fm.CoverageTier)
	}
	if fm.AcceptedAt != "2026-05-17T00:00:00Z" {
		t.Fatalf("expected accepted_at, got %q", fm.AcceptedAt)
	}
	if fm.AcceptedBy != "test-agent" {
		t.Fatalf("expected accepted_by test-agent, got %q", fm.AcceptedBy)
	}
	if fm.AcceptanceProfile != "test-profile" {
		t.Fatalf("expected acceptance_profile test-profile, got %q", fm.AcceptanceProfile)
	}
}

func TestADRParsesBodySectionPresence(t *testing.T) {
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
	if err != nil {
		t.Fatalf("expected valid ADR: %v", err)
	}
	s := doc.Sections
	if !s.HasContext {
		t.Fatal("expected HasContext true")
	}
	if !s.HasDecision {
		t.Fatal("expected HasDecision true")
	}
	if !s.HasAlternativesConsidered {
		t.Fatal("expected HasAlternativesConsidered true")
	}
	if !s.HasEvidence {
		t.Fatal("expected HasEvidence true")
	}
	if !s.HasConsequences {
		t.Fatal("expected HasConsequences true")
	}
	if !s.HasCompatibilityAndMigration {
		t.Fatal("expected HasCompatibilityAndMigration true")
	}
	if !s.HasTDDObligations {
		t.Fatal("expected HasTDDObligations true")
	}
	if !s.HasRollback {
		t.Fatal("expected HasRollback true")
	}
	if !s.HasOpenQuestions {
		t.Fatal("expected HasOpenQuestions true")
	}
}

func TestADRAcceptsShortSectionNames(t *testing.T) {
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
	if err != nil {
		t.Fatalf("expected valid ADR with short section names: %v", err)
	}
	if !doc.Sections.HasAlternativesConsidered {
		t.Fatal("expected HasAlternativesConsidered true for ## Alternatives")
	}
	if !doc.Sections.HasCompatibilityAndMigration {
		t.Fatal("expected HasCompatibilityAndMigration true for ## Compatibility")
	}
}

func TestPRDAcceptedStatusRequiresAcceptanceMetadata(t *testing.T) {
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
	if err == nil {
		t.Fatal("expected accepted PRD without acceptance metadata to fail")
	}
	for _, want := range []string{"missing accepted_at", "missing accepted_by", "missing acceptance_profile"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected error containing %q, got %v", want, err)
		}
	}
}

func TestADRAcceptedStatusRequiresAcceptanceMetadata(t *testing.T) {
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
	if err == nil {
		t.Fatal("expected accepted ADR without acceptance metadata to fail")
	}
	for _, want := range []string{"missing accepted_at", "missing accepted_by", "missing acceptance_profile"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected error containing %q, got %v", want, err)
		}
	}
}

func TestValidateADRSupersessionLinksDetectsBrokenSelfDuplicateAndNonReciprocal(t *testing.T) {
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
	if len(errs) < 5 {
		t.Fatalf("expected at least 5 supersession errors, got %v", errs)
	}
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
		if !codes[code] {
			t.Fatalf("expected code %q in errors, got %v", code, errs)
		}
	}
}

func TestADRIndexSupersessionLinksDetectBrokenSelfDuplicateAndNonReciprocal(t *testing.T) {
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
	if len(errs) < 5 {
		t.Fatalf("expected at least 5 supersession errors, got %v", errs)
	}
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
		if !codes[code] {
			t.Fatalf("expected code %q in errors, got %v", code, errs)
		}
	}
}

func TestADRMissingRequiredFrontMatterFieldsFailsClosed(t *testing.T) {
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
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseADRMarkdown([]byte(tc.adr))
			if err == nil {
				t.Fatalf("expected ADR %s to fail", tc.name)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestADRMissingRequiredBodySectionsFailsClosed(t *testing.T) {
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
		t.Run(tc.name, func(t *testing.T) {
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
			if err == nil {
				t.Fatalf("expected ADR %s to fail", tc.name)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestADRCrossLinkValidationDetectsMissingPRDRef(t *testing.T) {
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
	if len(errs) != 1 || errs[0].Field != "prd_refs" {
		t.Fatalf("expected one prd_refs error, got %v", errs)
	}
	if !strings.Contains(errs[0].Message, "REQ-MISSING") {
		t.Fatalf("expected REQ-MISSING in message, got %q", errs[0].Message)
	}
}

func TestADRCrossLinkValidationDetectsMissingEvidenceRef(t *testing.T) {
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
	if len(errs) != 1 || errs[0].Field != "evidence_refs" {
		t.Fatalf("expected one evidence_refs error, got %v", errs)
	}
	if !strings.Contains(errs[0].Message, "EV-MISSING") {
		t.Fatalf("expected EV-MISSING in message, got %q", errs[0].Message)
	}
}

func TestADRCrossLinkValidationDetectsMissingTDDObligation(t *testing.T) {
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
	if len(errs) != 1 || errs[0].Field != "tdd_obligations" {
		t.Fatalf("expected one tdd_obligations error, got %v", errs)
	}
	if !strings.Contains(errs[0].Message, "TDD-MISSING") {
		t.Fatalf("expected TDD-MISSING in message, got %q", errs[0].Message)
	}
}

func TestADRCrossLinkValidationPassesWhenAllRefsPresent(t *testing.T) {
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
	if len(errs) != 0 {
		t.Fatalf("expected zero errors, got %v", errs)
	}
}

func TestADRCrossLinkValidationSkipsCheckForNilMaps(t *testing.T) {
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
	if len(errs) != 0 {
		t.Fatalf("expected zero errors when all maps nil, got %v", errs)
	}
}

func TestValidatePRDTDDObligationCrossLinksDetectsMissingMust(t *testing.T) {
	reqs := []PRDRequirement{
		{ID: "REQ-1", Priority: "must", TDDObligations: []string{"TDD-1", "TDD-MISSING"}},
		{ID: "REQ-2", Priority: "should", TDDObligations: []string{"TDD-ALSO-MISSING"}},
	}
	errs := ValidatePRDTDDObligationCrossLinks(reqs, map[string]bool{"TDD-1": true})
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %d: %v", len(errs), errs)
	}
	if errs[0].RequirementID != "REQ-1" {
		t.Fatalf("expected RequirementID REQ-1, got %q", errs[0].RequirementID)
	}
	if errs[0].TDDObligation != "TDD-MISSING" {
		t.Fatalf("expected TDDObligation TDD-MISSING, got %q", errs[0].TDDObligation)
	}
	if !strings.Contains(errs[0].Message, "TDD-MISSING") {
		t.Fatalf("expected message to mention TDD-MISSING, got %q", errs[0].Message)
	}
}

func TestValidatePRDTDDObligationCrossLinksPassesWhenAllPresent(t *testing.T) {
	reqs := []PRDRequirement{
		{ID: "REQ-1", Priority: "must", TDDObligations: []string{"TDD-1", "TDD-2"}},
	}
	errs := ValidatePRDTDDObligationCrossLinks(reqs, map[string]bool{"TDD-1": true, "TDD-2": true})
	if len(errs) != 0 {
		t.Fatalf("expected 0 errors, got %d: %v", len(errs), errs)
	}
}

func TestValidatePRDTDDObligationCrossLinksSkipsNonMust(t *testing.T) {
	reqs := []PRDRequirement{
		{ID: "REQ-1", Priority: "should", TDDObligations: []string{"TDD-MISSING"}},
		{ID: "REQ-2", Priority: "MUST", TDDObligations: []string{"TDD-1"}},
	}
	errs := ValidatePRDTDDObligationCrossLinks(reqs, map[string]bool{"TDD-1": true})
	if len(errs) != 0 {
		t.Fatalf("expected 0 errors (should-priority skipped, MUST case-insensitive match), got %d: %v", len(errs), errs)
	}
}

func TestValidatePRDTDDObligationCrossLinksNilMapSkipsCheck(t *testing.T) {
	reqs := []PRDRequirement{
		{ID: "REQ-1", Priority: "must", TDDObligations: []string{"TDD-MISSING"}},
	}
	errs := ValidatePRDTDDObligationCrossLinks(reqs, nil)
	if len(errs) != 0 {
		t.Fatalf("expected 0 errors with nil map, got %d: %v", len(errs), errs)
	}
}

func TestCommitHashValidHexPasses(t *testing.T) {
	data := `{"record_type":"implementation_completed","task_id":"impl-001","effort_id":"effort-1","timestamp":"2026-05-17T16:00:00Z","commit_hash":"a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2"}`
	if _, err := ParseImplementationLedgerJSONL([]byte(data)); err != nil {
		t.Fatalf("expected valid 40-char hex commit_hash to pass, got %v", err)
	}
}

func TestCommitHashEmptyStringPasses(t *testing.T) {
	data := `{"record_type":"implementation_completed","task_id":"impl-001","effort_id":"effort-1","timestamp":"2026-05-17T16:00:00Z"}`
	if _, err := ParseImplementationLedgerJSONL([]byte(data)); err != nil {
		t.Fatalf("expected missing/empty commit_hash to pass, got %v", err)
	}
}

func TestCommitHashInvalidHexFails(t *testing.T) {
	data := `{"record_type":"implementation_completed","task_id":"impl-001","effort_id":"effort-1","timestamp":"2026-05-17T16:00:00Z","commit_hash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaG"}`
	_, err := ParseImplementationLedgerJSONL([]byte(data))
	if err == nil {
		t.Fatal("expected commit_hash with non-hex chars to fail")
	}
	if !strings.Contains(err.Error(), "commit_hash must be 40 hex chars") {
		t.Fatalf("expected commit_hash format error, got %v", err)
	}
}

func TestCommitHashTooShortFails(t *testing.T) {
	data := `{"record_type":"implementation_completed","task_id":"impl-001","effort_id":"effort-1","timestamp":"2026-05-17T16:00:00Z","commit_hash":"abc1234"}`
	_, err := ParseImplementationLedgerJSONL([]byte(data))
	if err == nil {
		t.Fatal("expected short commit_hash to fail")
	}
	if !strings.Contains(err.Error(), "commit_hash must be 40 hex chars") {
		t.Fatalf("expected commit_hash format error, got %v", err)
	}
}

func TestCommitHashUppercaseHexFails(t *testing.T) {
	data := `{"record_type":"implementation_completed","task_id":"impl-001","effort_id":"effort-1","timestamp":"2026-05-17T16:00:00Z","commit_hash":"A1B2C3D4E5F6A1B2C3D4E5F6A1B2C3D4E5F6A1B2"}`
	_, err := ParseImplementationLedgerJSONL([]byte(data))
	if err == nil {
		t.Fatal("expected uppercase hex commit_hash to fail")
	}
	if !strings.Contains(err.Error(), "commit_hash must be 40 hex chars") {
		t.Fatalf("expected commit_hash format error, got %v", err)
	}
}

func TestCommitHashValidatedOnFallbackCompletion(t *testing.T) {
	data := `{"record_type":"fallback_completion","task_id":"impl-001","effort_id":"effort-1","timestamp":"2026-05-17T16:00:00Z","commit_hash":"bad"}`
	_, err := ParseImplementationLedgerJSONL([]byte(data))
	if err == nil {
		t.Fatal("expected bad commit_hash on fallback_completion to fail")
	}
	if !strings.Contains(err.Error(), "commit_hash must be 40 hex chars") {
		t.Fatalf("expected commit_hash format error, got %v", err)
	}
}

func TestCommitHashValidatedOnPartialImplementation(t *testing.T) {
	data := `{"record_type":"partial_implementation_recorded","task_id":"impl-001","effort_id":"effort-1","timestamp":"2026-05-17T16:00:00Z","commit_hash":"bad"}`
	_, err := ParseImplementationLedgerJSONL([]byte(data))
	if err == nil {
		t.Fatal("expected bad commit_hash on partial_implementation_recorded to fail")
	}
	if !strings.Contains(err.Error(), "commit_hash must be 40 hex chars") {
		t.Fatalf("expected commit_hash format error, got %v", err)
	}
}

func TestCommitHashNotValidatedOnOtherRecordTypes(t *testing.T) {
	data := `{"record_type":"atom_planned","task_id":"impl-001","effort_id":"effort-1","timestamp":"2026-05-17T16:00:00Z","complexity_tier":"C1","context_pack_id":"pack","commit_hash":"bad","implementor":{"id":"i","runtime":"r","model":"m","command":"c"},"reviewer":{"id":"r","model":"m","reasoning_effort":"m","runtime":"r"},"scope":{"expected_files":["a.go"],"forbidden":["b.go"]},"required_verification":["go test"],"report_paths":{"implementation":"reports/i.json","review":"reports/r.json","investigation":"reports/v.json"}}`
	if _, err := ParseImplementationLedgerJSONL([]byte(data)); err != nil {
		t.Fatalf("expected commit_hash on atom_planned to be ignored, got %v", err)
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		next := filepath.Dir(dir)
		if next == dir {
			t.Fatal("could not find repo root")
		}
		dir = next
	}
}
