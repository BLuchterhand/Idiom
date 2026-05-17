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
