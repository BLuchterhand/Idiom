package productworkflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProductWorkflowMetrics(t *testing.T) {
	t.Run("collects deterministic counts", func(t *testing.T) {
		root := writeMetricsFixture(t)
		metrics, err := BuildProductWorkflowMetrics(root)
		if err != nil {
			t.Fatalf("BuildProductWorkflowMetrics() error = %v", err)
		}
		if metrics.CurrentPhase != "phase-5" || metrics.CurrentState != "implementing" {
			t.Fatalf("unexpected phase/state: %s/%s", metrics.CurrentPhase, metrics.CurrentState)
		}
		if metrics.StateRevision != 7 {
			t.Fatalf("StateRevision = %d, want 7", metrics.StateRevision)
		}
		if metrics.GateStatusCounts["pass"] != 1 || metrics.GateStatusCounts["fail"] != 1 {
			t.Fatalf("unexpected gate counts: %#v", metrics.GateStatusCounts)
		}
		if metrics.TDDObligationTotal != 2 || metrics.TDDStatusCounts["pending"] != 1 || metrics.TDDStatusCounts["done"] != 1 {
			t.Fatalf("unexpected TDD counts: total=%d status=%#v", metrics.TDDObligationTotal, metrics.TDDStatusCounts)
		}
		if metrics.TDDTypeCounts["acceptance"] != 1 || metrics.TDDTypeCounts["contract"] != 1 {
			t.Fatalf("unexpected TDD type counts: %#v", metrics.TDDTypeCounts)
		}
		if metrics.LedgerRecordCount != 2 || metrics.LedgerAtomRecordCount != 1 {
			t.Fatalf("unexpected ledger counts: %#v", metrics)
		}
		if metrics.ReportTypeCounts["implementation"] != 1 || metrics.ReportTypeCounts["review"] != 1 || metrics.ReportTypeCounts["investigation"] != 1 {
			t.Fatalf("unexpected report type counts: %#v", metrics.ReportTypeCounts)
		}
		if metrics.ReportStatusCounts["implementation_completed"] != 1 || metrics.ReportStatusCounts["review_pending"] != 1 || metrics.ReportStatusCounts["investigation_pending"] != 1 {
			t.Fatalf("unexpected report status counts: %#v", metrics.ReportStatusCounts)
		}
		if metrics.ArtifactPathCount != 2 || metrics.EvidenceCardCount != 1 || metrics.TraceabilityRowCount != 1 || metrics.PRDRequirementCount != 1 || metrics.ADRCount != 1 {
			t.Fatalf("unexpected artifact/evidence counts: %#v", metrics)
		}
		if metrics.EvidenceLinkCounts["prd_requirement_evidence_refs"] != 1 || metrics.EvidenceLinkCounts["adr_evidence_refs"] != 1 || metrics.EvidenceLinkCounts["traceability_evidence_links"] != 1 {
			t.Fatalf("unexpected evidence link counts: %#v", metrics.EvidenceLinkCounts)
		}
		if metrics.NextValidActionCount != 2 || metrics.BlockerCount != 1 || metrics.OpenObligationCount != 2 {
			t.Fatalf("unexpected progress counts: %#v", metrics)
		}
	})

	t.Run("fails closed on malformed artifacts", func(t *testing.T) {
		root := writeMetricsFixture(t)
		writeFixtureFile(t, root, "tdd.json", "{bad")
		if _, err := BuildProductWorkflowMetrics(root); err == nil {
			t.Fatal("expected error for malformed tdd.json")
		} else if strings.Contains(err.Error(), root) {
			t.Fatalf("metrics error should not leak absolute root path, got %q", err.Error())
		} else if !strings.Contains(err.Error(), "tdd.json") {
			t.Fatalf("metrics error should include relative artifact path, got %q", err.Error())
		}
	})

	t.Run("fails closed without leaking missing artifact absolute path", func(t *testing.T) {
		root := writeMetricsFixture(t)
		if err := os.Remove(filepath.Join(root, "tdd.json")); err != nil {
			t.Fatalf("remove tdd.json: %v", err)
		}
		if _, err := BuildProductWorkflowMetrics(root); err == nil {
			t.Fatal("expected error for missing tdd.json")
		} else if strings.Contains(err.Error(), root) {
			t.Fatalf("metrics error should not leak absolute root path, got %q", err.Error())
		} else if !strings.Contains(err.Error(), "read tdd.json: file not found") {
			t.Fatalf("expected stable relative missing-file error, got %q", err.Error())
		}
	})

	t.Run("fails closed on unbounded metric values", func(t *testing.T) {
		tests := []struct {
			name    string
			path    string
			oldText string
			newText string
		}{
			{
				name:    "workflow gate status",
				path:    "workflow_state.json",
				oldText: `"status":"fail"`,
				newText: `"status":"surprise"`,
			},
			{
				name:    "tdd status",
				path:    "tdd.json",
				oldText: `"status":"done"`,
				newText: `"status":"surprise"`,
			},
			{
				name:    "ledger record type",
				path:    "implementation_ledger.jsonl",
				oldText: `"record_type":"verification_completed"`,
				newText: `"record_type":"surprise"`,
			},
			{
				name:    "review status",
				path:    "reports/test-review.json",
				oldText: `"status":"pending"`,
				newText: `"status":"surprise"`,
			},
			{
				name:    "investigation status",
				path:    "reports/test-investigation.json",
				oldText: `"status":"pending"`,
				newText: `"status":"surprise"`,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				root := writeMetricsFixture(t)
				abs := filepath.Join(root, tt.path)
				data, err := os.ReadFile(abs)
				if err != nil {
					t.Fatalf("read fixture: %v", err)
				}
				updated := strings.Replace(string(data), tt.oldText, tt.newText, 1)
				if updated == string(data) {
					t.Fatalf("fixture replacement %q not found", tt.oldText)
				}
				if err := os.WriteFile(abs, []byte(updated), 0o644); err != nil {
					t.Fatalf("write fixture: %v", err)
				}
				_, err = BuildProductWorkflowMetrics(root)
				if err == nil {
					t.Fatal("expected metrics to fail closed")
				}
				if !strings.Contains(err.Error(), "unsupported") {
					t.Fatalf("expected unsupported-value error, got %v", err)
				}
				if strings.Contains(err.Error(), root) {
					t.Fatalf("metrics error should not leak absolute root path, got %q", err.Error())
				}
			})
		}
	})
}

func writeMetricsFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFixtureFile(t, root, "workflow_state.json", `{
  "schema_version":"idiom.product_workflow_state.v0.fixture",
  "effort_id":"metrics-effort",
  "task_id":"impl-metrics",
  "title":"Metrics Effort",
  "state_revision":7,
  "status_bucket":"started",
  "current_phase":"phase-5",
  "current_state":"implementing",
  "artifact_paths":{"events":"events.jsonl","brief":"briefs/test.md"},
  "context":{"context_pack_id":"pack-1","freshness_state":"current"},
  "coverage":{"coverage_tier":"unit","gate_results":[{"gate":"scope","status":"pass"},{"gate":"verify","status":"fail"}]},
  "open_obligations":["OB-1","OB-2"],
  "blockers":["review pending"],
  "privacy":{"redaction_status":"applied"},
  "folder_path":"docs/test",
  "durability_level":"source_controlled",
  "next_valid_actions":["close","retest"]
}`)
	writeFixtureFile(t, root, "tdd.json", `{
  "schema_version":"idiom.tdd_obligations.v2.fixture",
  "effort_id":"metrics-effort",
  "task_id":"impl-metrics",
  "coverage_tier":"unit",
  "coverage":{"mutation_min_score":80},
  "obligations":[
    {"id":"TDD-1","type":"acceptance","behavior":"b1","owner":"team","command":"go test","required_before":"close","status":"pending"},
    {"id":"TDD-2","type":"contract","behavior":"b2","owner":"team","command":"go test","required_before":"close","status":"done"}
  ]
}`)
	writeFixtureFile(t, root, "evidence/evidence.jsonl", `{"card_id":"EV-1","kind":"finding","source_handle":"file://x","claim":"c","confidence":"high","freshness":"current","uncertainty":"","evidence_refs":["x:1"],"researcher_id":"r","timestamp":"2026-05-17T00:00:00Z"}`)
	writeFixtureFile(t, root, "evidence/traceability_matrix.json", `{
  "schema_version":"idiom.traceability_matrix.v1.fixture",
  "effort_id":"metrics-effort",
  "task_id":"impl-metrics",
  "created_at":"2026-05-17T00:00:00Z",
  "matrix":[{"evidence_card":"EV-1","prd_requirement":"REQ-1","adr":"ADR-1","tdd_obligation":"TDD-1","brief":"briefs/test.md","report":"reports/r.json"}]
}`)
	writeFixtureFile(t, root, "implementation_ledger.jsonl", `{"record_type":"atom_planned","task_id":"impl-metrics","effort_id":"metrics-effort","timestamp":"2026-05-17T00:00:00Z","complexity_tier":"C1","context_pack_id":"pack","implementor":{"id":"impl","runtime":"opencode","model":"m","command":"c"},"reviewer":{"id":"rev","model":"m","reasoning_effort":"medium","runtime":"codex"},"scope":{"expected_files":["a.go"],"forbidden_patterns":["docs/**"]},"required_verification":["go test"],"report_paths":{"implementation":"reports/i-implementation.json","review":"reports/r-review.json","investigation":"reports/x-investigation.json"}}`+"\n"+`{"record_type":"verification_completed","task_id":"impl-metrics","effort_id":"metrics-effort","timestamp":"2026-05-17T00:01:00Z"}`)
	writeFixtureFile(t, root, "prd.md", `---
schema_version: idiom.prd.v1
prd_id: prd-1
status: accepted
task_id: impl-metrics
created_at: 2026-05-17T00:00:00Z
accepted_at: 2026-05-17T00:00:00Z
accepted_by: reviewer
acceptance_profile: reviewer
---

## Requirements
### REQ-1
- **id**: REQ-1
- **priority**: must
- **statement**: s
- **owner_alignment**: team
- **testability**: automated
- **metric**: m
- **evidence_refs**: ["EV-1"]
- **tdd_obligations**: ["TDD-1"]
- **open_questions**: []
`)
	writeFixtureFile(t, root, "adrs/index.json", `{"schema_version":"v1","effort_id":"metrics-effort","adrs":[{"adr_id":"ADR-1","title":"A","status":"accepted","path":"ADR-1.md","coverage_tier":"unit","prd_refs":["REQ-1"],"created_at":"2026-05-17T00:00:00Z"}]}`)
	writeFixtureFile(t, root, "adrs/ADR-1.md", `---
schema_version: idiom.adr.v1
adr_id: ADR-1
status: accepted
accepted_at: 2026-05-17T00:00:00Z
accepted_by: reviewer
acceptance_profile: reviewer
prd_refs: [REQ-1]
evidence_refs: [EV-1]
tdd_obligations: [TDD-1]
coverage_tier: unit
---
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
	writeFixtureFile(t, root, "reports/test-implementation.json", `{"schema_version":"idiom.implementation_report.v1.fixture","task_id":"impl-metrics","effort_id":"metrics-effort","implementor_id":"impl","runtime":"opencode","model":"m","completed_at":"2026-05-17T00:00:00Z","complexity_tier":"C1","scope_statement":"s","code_changes":false,"changed_paths":[],"verification":{"run_by_implementor":false,"commands":[],"status":"pending"},"privacy_note":"No raw prompts stored.","open_blockers":[],"reviewer_id":"rev"}`)
	writeFixtureFile(t, root, "reports/test-review.json", `{"schema_version":"idiom.review_report.v1.fixture","effort_id":"metrics-effort","task_id":"impl-metrics","reviewer":{"agent_id":"rev","model":"m","reasoning_effort":"medium","independent_from_implementor":true},"status":"pending","findings":[],"checks":{},"reviewed_at":"","commands_reviewed":[],"created_at":"2026-05-17T00:00:00Z"}`)
	writeFixtureFile(t, root, "reports/test-investigation.json", `{"schema_version":"idiom.investigation_report.v1.fixture","effort_id":"metrics-effort","task_id":"impl-metrics","status":"pending","created_at":"2026-05-17T00:00:00Z","scope":{"files":["a.go"]},"tests":{"focused":"go test"},"coverage":{"tier":"unit"},"traceability":{"checked":false},"privacy_audit":{"no_raw_prompts":true},"reviewer_findings":[],"blockers":[],"next_valid_action":"","completed_at":""}`)
	return root
}

func writeFixtureFile(t *testing.T, root, relPath, content string) {
	t.Helper()
	abs := filepath.Join(root, relPath)
	mkdirPath := filepath.Dir(abs)
	if err := os.MkdirAll(mkdirPath, 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", mkdirPath, err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", abs, err)
	}
}
