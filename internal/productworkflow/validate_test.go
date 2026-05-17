package productworkflow

import (
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
	if _, err := ParseWorkflowStateJSON(read("docs/product-workflows/efforts/started/prd-adr-brief-workflow/workflow_state.json")); err != nil {
		t.Fatalf("parse workflow state: %v", err)
	}
	if _, err := ParseTDDLedgerJSON(read("docs/product-workflows/efforts/started/prd-adr-brief-workflow/tdd.json")); err != nil {
		t.Fatalf("parse tdd ledger: %v", err)
	}
	if _, err := ParseEvidenceCardsJSONL(read("docs/product-workflows/efforts/started/prd-adr-brief-workflow/evidence/evidence.jsonl")); err != nil {
		t.Fatalf("parse evidence JSONL: %v", err)
	}
	if _, err := ParseTraceabilityMatrixJSON(read("docs/product-workflows/efforts/started/prd-adr-brief-workflow/evidence/traceability_matrix.json")); err != nil {
		t.Fatalf("parse traceability matrix: %v", err)
	}
	if _, err := ParseImplementationLedgerJSONL(read("docs/product-workflows/efforts/started/prd-adr-brief-workflow/implementation_ledger.jsonl")); err != nil {
		t.Fatalf("parse implementation ledger: %v", err)
	}
}

func TestParsePRDFixturePasses(t *testing.T) {
	root := repoRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "docs/product-workflows/efforts/started/prd-adr-brief-workflow/prd.md"))
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
	data, err := os.ReadFile(filepath.Join(root, "docs/product-workflows/efforts/started/prd-adr-brief-workflow/adrs/index.json"))
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
	data, err := os.ReadFile(filepath.Join(root, "docs/product-workflows/efforts/started/prd-adr-brief-workflow/adrs/ADR-0001-durable-workflow-substrate.md"))
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
	if !doc.HasDecision {
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
created_at: 2026-05-17T00:00:00Z
---

# ADR-TEST: Test
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
---

# ADR-TEST: Test

## Decision
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
---

# ADR-TEST: Test

## Decision
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
		"folder_path":"docs/product-workflows/efforts/started/prd-adr-brief-workflow",
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
