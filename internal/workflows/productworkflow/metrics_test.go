package productworkflow

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("metrics", func() {
	ginkgo.It("ProductWorkflowMetrics", func() {
		ginkgo.By("collects deterministic counts", func() {
			root := writeMetricsFixture()

			metrics, err := BuildProductWorkflowMetrics(root)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("BuildProductWorkflowMetrics() error = %v", err))
			gomega.Expect(metrics.CurrentPhase).To(gomega.Equal("phase-5"), fmt.Sprintf("unexpected phase/state: %s/%s", metrics.CurrentPhase, metrics.CurrentState))
			gomega.Expect(metrics.CurrentState).To(gomega.Equal("implementing"), fmt.Sprintf("unexpected phase/state: %s/%s", metrics.CurrentPhase, metrics.CurrentState))
			gomega.Expect(metrics.StateRevision != 7).To(gomega.BeFalse(), fmt.Sprintf("StateRevision = %d, want 7", metrics.StateRevision))
			gomega.Expect(metrics.GateStatusCounts["pass"]).To(gomega.Equal(1), fmt.Sprintf("unexpected gate counts: %#v", metrics.GateStatusCounts))
			gomega.Expect(metrics.GateStatusCounts["fail"]).To(gomega.Equal(1), fmt.Sprintf("unexpected gate counts: %#v", metrics.GateStatusCounts))
			gomega.Expect(metrics.TDDObligationTotal).To(gomega.Equal(2), fmt.Sprintf("unexpected TDD counts: total=%d status=%#v", metrics.TDDObligationTotal, metrics.TDDStatusCounts))
			gomega.Expect(metrics.TDDStatusCounts["pending"]).To(gomega.Equal(1), fmt.Sprintf("unexpected TDD counts: total=%d status=%#v", metrics.TDDObligationTotal, metrics.TDDStatusCounts))
			gomega.Expect(metrics.TDDStatusCounts["done"]).To(gomega.Equal(1), fmt.Sprintf("unexpected TDD counts: total=%d status=%#v", metrics.TDDObligationTotal, metrics.TDDStatusCounts))
			gomega.Expect(metrics.TDDTypeCounts["acceptance"]).To(gomega.Equal(1), fmt.Sprintf("unexpected TDD type counts: %#v", metrics.TDDTypeCounts))
			gomega.Expect(metrics.TDDTypeCounts["contract"]).To(gomega.Equal(1), fmt.Sprintf("unexpected TDD type counts: %#v", metrics.TDDTypeCounts))
			gomega.Expect(metrics.LedgerRecordCount).To(gomega.Equal(2), fmt.Sprintf("unexpected ledger counts: %#v", metrics))
			gomega.Expect(metrics.LedgerAtomRecordCount).To(gomega.Equal(1), fmt.Sprintf("unexpected ledger counts: %#v", metrics))
			gomega.Expect(metrics.ReportTypeCounts["implementation"]).To(gomega.Equal(1), fmt.Sprintf("unexpected report type counts: %#v", metrics.ReportTypeCounts))
			gomega.Expect(metrics.ReportTypeCounts["review"]).To(gomega.Equal(1), fmt.Sprintf("unexpected report type counts: %#v", metrics.ReportTypeCounts))
			gomega.Expect(metrics.ReportTypeCounts["investigation"]).To(gomega.Equal(1), fmt.Sprintf("unexpected report type counts: %#v", metrics.ReportTypeCounts))
			gomega.Expect(metrics.ReportStatusCounts["implementation_completed"]).To(gomega.Equal(1), fmt.Sprintf("unexpected report status counts: %#v", metrics.ReportStatusCounts))
			gomega.Expect(metrics.ReportStatusCounts["review_pending"]).To(gomega.Equal(1), fmt.Sprintf("unexpected report status counts: %#v", metrics.ReportStatusCounts))
			gomega.Expect(metrics.ReportStatusCounts["investigation_pending"]).To(gomega.Equal(1), fmt.Sprintf("unexpected report status counts: %#v", metrics.ReportStatusCounts))
			gomega.Expect(metrics.ArtifactPathCount).To(gomega.Equal(2), fmt.Sprintf("unexpected artifact/evidence counts: %#v", metrics))
			gomega.Expect(metrics.EvidenceCardCount).To(gomega.Equal(1), fmt.Sprintf("unexpected artifact/evidence counts: %#v", metrics))
			gomega.Expect(metrics.TraceabilityRowCount).To(gomega.Equal(1), fmt.Sprintf("unexpected artifact/evidence counts: %#v", metrics))
			gomega.Expect(metrics.PRDRequirementCount).To(gomega.Equal(1), fmt.Sprintf("unexpected artifact/evidence counts: %#v", metrics))
			gomega.Expect(metrics.ADRCount).To(gomega.Equal(1), fmt.Sprintf("unexpected artifact/evidence counts: %#v", metrics))
			gomega.Expect(metrics.EvidenceLinkCounts["prd_requirement_evidence_refs"]).To(gomega.Equal(1), fmt.Sprintf("unexpected evidence link counts: %#v", metrics.EvidenceLinkCounts))
			gomega.Expect(metrics.EvidenceLinkCounts["adr_evidence_refs"]).To(gomega.Equal(1), fmt.Sprintf("unexpected evidence link counts: %#v", metrics.EvidenceLinkCounts))
			gomega.Expect(metrics.EvidenceLinkCounts["traceability_evidence_links"]).To(gomega.Equal(1), fmt.Sprintf("unexpected evidence link counts: %#v", metrics.EvidenceLinkCounts))
			gomega.Expect(metrics.NextValidActionCount).To(gomega.Equal(2), fmt.Sprintf("unexpected progress counts: %#v", metrics))
			gomega.Expect(metrics.BlockerCount).To(gomega.Equal(1), fmt.Sprintf("unexpected progress counts: %#v", metrics))
			gomega.Expect(metrics.OpenObligationCount).To(gomega.Equal(2), fmt.Sprintf("unexpected progress counts: %#v", metrics))

		})
		ginkgo.By("fails closed on malformed artifacts", func() {
			root := writeMetricsFixture()
			writeFixtureFile(root, "tdd.json", "{bad")

			_, err := BuildProductWorkflowMetrics(root)
			gomega.Expect(err).To(gomega.HaveOccurred())
			gomega.Expect(err.Error()).NotTo(gomega.ContainSubstring(root))
			gomega.Expect(err.Error()).To(gomega.ContainSubstring("tdd.json"))

		})
		ginkgo.By("fails closed without leaking missing artifact absolute path", func() {
			root := writeMetricsFixture()
			{
				err := os.Remove(filepath.Join(root, "tdd.json"))
				gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("remove tdd.json: %v", err))
			}

			_, err := BuildProductWorkflowMetrics(root)
			gomega.Expect(err).To(gomega.HaveOccurred())
			gomega.Expect(err.Error()).NotTo(gomega.ContainSubstring(root))
			gomega.Expect(err.Error()).To(gomega.ContainSubstring("read tdd.json: file not found"))

		})
		ginkgo.By("rejects unsafe artifact paths", func() {
			root := writeMetricsFixture()
			writeFixtureFile(root, "adrs/index.json", `{"schema_version":"v1","effort_id":"metrics-effort","adrs":[{"adr_id":"ADR-1","title":"A","status":"accepted","path":"../../outside.md","coverage_tier":"unit","prd_refs":["REQ-1"],"created_at":"2026-05-17T00:00:00Z"}]}`)

			_, err := BuildProductWorkflowMetrics(root)
			gomega.Expect(err).To(gomega.HaveOccurred())
			gomega.Expect(err.Error()).NotTo(gomega.ContainSubstring(root))
			gomega.Expect(err.Error()).To(gomega.ContainSubstring("artifact path must be effort-relative"))

		})
		ginkgo.By("fails closed on unbounded metric values", func() {
			tests := []struct {
				name    string
				path    string
				oldText string
				newText string
				content string
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
					content: `{"schema_version":"idiom.review_report.v1.fixture","effort_id":"metrics-effort","task_id":"impl-metrics","reviewer":{"agent_id":"rev","model":"m","reasoning_effort":"medium","independent_from_implementor":true},"status":"surprise","findings":[],"checks":{"metrics":"pass"},"reviewed_at":"2026-05-17T00:02:00Z","commands_reviewed":["go test"],"created_at":"2026-05-17T00:00:00Z"}`,
				},
				{
					name:    "investigation status",
					path:    "reports/test-investigation.json",
					oldText: `"status":"pending"`,
					newText: `"status":"surprise"`,
				},
			}
			for _, tt := range tests {
				ginkgo.By(tt.name, func() {
					root := writeMetricsFixture()
					abs := filepath.Join(root, tt.path)
					updated := tt.content

					if updated == "" {
						data, err := os.ReadFile(abs)
						gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read fixture: %v", err))

						updated = strings.Replace(string(data), tt.oldText, tt.newText, 1)
						gomega.Expect(updated == string(data)).To(gomega.BeFalse(), fmt.Sprintf("fixture replacement %q not found", tt.oldText))

					}
					{

						err := os.WriteFile(abs, []byte(updated), 0o644)
						gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write fixture: %v", err))
					}

					_, err := BuildProductWorkflowMetrics(root)
					gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected metrics to fail closed"))
					gomega.Expect(err.Error()).To(gomega.Or(
						gomega.ContainSubstring("unsupported"),
						gomega.ContainSubstring("invalid status"),
						gomega.ContainSubstring("unknown record_type"),
					), fmt.Sprintf("expected unsupported/invalid-status error, got %v", err))
					gomega.Expect(strings.Contains(err.Error(), root)).To(gomega.BeFalse(), fmt.Sprintf("metrics error should not leak absolute root path, got %q", err.Error()))

				})
			}
		})
	})
})

func writeMetricsFixture() string {
	ginkgo.GinkgoHelper()

	root := ginkgo.GinkgoT().TempDir()
	writeFixtureFile(root, "workflow_state.json", `{
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
	writeFixtureFile(root, "tdd.json", `{
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
	writeFixtureFile(root, "evidence/evidence.jsonl", `{"card_id":"EV-1","kind":"finding","source_handle":"file://x","claim":"c","confidence":"high","freshness":"current","uncertainty":"","evidence_refs":["x:1"],"researcher_id":"r","timestamp":"2026-05-17T00:00:00Z"}`)
	writeFixtureFile(root, "evidence/traceability_matrix.json", `{
  "schema_version":"idiom.traceability_matrix.v1.fixture",
  "effort_id":"metrics-effort",
  "task_id":"impl-metrics",
  "created_at":"2026-05-17T00:00:00Z",
  "matrix":[{"evidence_card":"EV-1","prd_requirement":"REQ-1","adr":"ADR-1","tdd_obligation":"TDD-1","brief":"briefs/test.md","report":"reports/r.json"}]
}`)
	writeFixtureFile(root, "implementation_ledger.jsonl", `{"record_type":"atom_planned","task_id":"impl-metrics","effort_id":"metrics-effort","timestamp":"2026-05-17T00:00:00Z","complexity_tier":"C1","context_pack_id":"pack","implementor":{"id":"impl","runtime":"opencode","model":"m","command":"c"},"reviewer":{"id":"rev","model":"m","reasoning_effort":"medium","runtime":"codex"},"scope":{"expected_files":["a.go"],"forbidden_patterns":["docs/**"]},"required_verification":["go test"],"report_paths":{"implementation":"reports/i-implementation.json","review":"reports/r-review.json","investigation":"reports/x-investigation.json"}}`+"\n"+`{"record_type":"verification_completed","task_id":"impl-metrics","effort_id":"metrics-effort","timestamp":"2026-05-17T00:01:00Z"}`)
	writeFixtureFile(root, "prd.md", `---
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
	writeFixtureFile(root, "adrs/index.json", `{"schema_version":"v1","effort_id":"metrics-effort","adrs":[{"adr_id":"ADR-1","title":"A","status":"accepted","path":"ADR-1.md","coverage_tier":"unit","prd_refs":["REQ-1"],"created_at":"2026-05-17T00:00:00Z"}]}`)
	writeFixtureFile(root, "adrs/ADR-1.md", `---
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
	writeFixtureFile(root, "reports/test-implementation.json", `{"schema_version":"idiom.implementation_report.v1.fixture","task_id":"impl-metrics","effort_id":"metrics-effort","implementor_id":"impl","runtime":"opencode","model":"m","completed_at":"2026-05-17T00:00:00Z","complexity_tier":"C1","scope_statement":"s","code_changes":false,"changed_paths":[],"verification":{"run_by_implementor":false,"commands":[],"status":"pending"},"privacy_note":"No raw prompts stored.","open_blockers":[],"reviewer_id":"rev"}`)
	writeFixtureFile(root, "reports/test-review.json", `{"schema_version":"idiom.review_report.v1.fixture","effort_id":"metrics-effort","task_id":"impl-metrics","reviewer":{"agent_id":"rev","model":"m","reasoning_effort":"medium","independent_from_implementor":true},"status":"pending","findings":[],"checks":{},"reviewed_at":"","commands_reviewed":[],"created_at":"2026-05-17T00:00:00Z"}`)
	writeFixtureFile(root, "reports/test-investigation.json", `{"schema_version":"idiom.investigation_report.v1.fixture","effort_id":"metrics-effort","task_id":"impl-metrics","status":"pending","created_at":"2026-05-17T00:00:00Z","scope":{"files":["a.go"]},"tests":{"focused":"go test"},"coverage":{"tier":"unit"},"traceability":{"checked":false},"privacy_audit":{"no_raw_prompts":true},"reviewer_findings":[],"blockers":[],"next_valid_action":"","completed_at":""}`)

	return root
}

func writeFixtureFile(root, relPath, content string) {
	ginkgo.GinkgoHelper()

	abs := filepath.Join(root, relPath)
	mkdirPath := filepath.Dir(abs)
	{

		err := os.MkdirAll(mkdirPath, 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("MkdirAll(%s): %v", mkdirPath, err))
	}
	{

		err := os.WriteFile(abs, []byte(content), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("WriteFile(%s): %v", abs, err))
	}

}
