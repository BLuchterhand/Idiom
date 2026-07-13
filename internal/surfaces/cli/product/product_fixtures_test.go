package product

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"idiom/internal/workflows/productworkflow"
	"idiom/internal/workflows/recovery"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

func readMainTelemetryRecords(t ginkgo.FullGinkgoTInterface, path string) []map[string]any {
	t.Helper()

	data, err := os.ReadFile(path)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "read telemetry log: %v", err)

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	records := make([]map[string]any, 0, len(lines))

	for _, line := range lines {
		var record map[string]any

		{
			err := json.Unmarshal([]byte(line), &record)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), "parse telemetry line %q: %v", line, err)
		}

		records = append(records, record)
	}

	return records
}

func findTelemetryRecord(records []map[string]any, eventType string) map[string]any {
	for _, record := range records {
		if record["type"] == eventType {
			return record
		}
	}

	return nil
}

func findAuditSection(t ginkgo.FullGinkgoTInterface, result productworkflow.ProductAuditResult, name string) productworkflow.ProductAuditSection {
	t.Helper()
	var found *productworkflow.ProductAuditSection

	for i := range result.Sections {
		if result.Sections[i].Name == name {
			found = &result.Sections[i]
			break
		}
	}

	gomega.ExpectWithOffset(1, found).NotTo(gomega.BeNil(), "section %q not found", name)
	return *found
}

func findStatusSection(t ginkgo.FullGinkgoTInterface, sections []productworkflow.StatusSection, name string) productworkflow.StatusSection {
	t.Helper()
	var found *productworkflow.StatusSection

	for i := range sections {
		if sections[i].Name == name {
			found = &sections[i]
			break
		}
	}

	gomega.ExpectWithOffset(1, found).NotTo(gomega.BeNil(), "status section %q not found", name)
	return *found
}

func writeValidTestEffort(t ginkgo.FullGinkgoTInterface) string {
	t.Helper()

	dir := t.TempDir()
	writeValidTestWorkflowAuthority(t, dir)
	writeTestFile(t, dir, "tdd.json", validTestTDDJSON())
	writeTestFile(t, dir, "evidence/evidence.jsonl", validTestEvidenceJSONL())
	writeTestFile(t, dir, "evidence/traceability_matrix.json", validTestTraceabilityMatrixJSON())
	writeTestFile(t, dir, "implementation_ledger.jsonl", validTestImplementationLedgerJSONL())
	writeTestFile(t, dir, "prd.md", validTestPRDMarkdown())
	writeTestFile(t, dir, "adrs/index.json", validTestADRIndexJSON())
	writeTestFile(t, dir, "adrs/ADR-001-test.md", validTestADRMarkdown())
	writeTestFile(t, dir, "briefs/test.md", "# Test Brief")
	writeTestFile(t, dir, "reports/test.json", "{}")

	return dir
}

func writeValidTestWorkflowAuthority(t ginkgo.FullGinkgoTInterface, dir string) {
	t.Helper()

	writeTestFile(t, dir, "events.jsonl", "")
	writeTestFile(t, dir, "workflow_state.json", validTestWorkflowStateJSON())
}

func writeBehaviorStandardWorkflowStateFixture(t ginkgo.FullGinkgoTInterface, dir string) {
	t.Helper()

	writeTestWorkflowStateFixture(t, dir, func(state *productworkflow.WorkflowState) {
		state.Coverage.CoverageTier = "behavior_standard"
	})
}

func writeTestWorkflowStateFixture(
	t ginkgo.FullGinkgoTInterface,
	dir string,
	mutate func(*productworkflow.WorkflowState),
) {
	t.Helper()

	var state productworkflow.WorkflowState
	err := json.Unmarshal([]byte(validTestWorkflowStateJSON()), &state)
	gomega.ExpectWithOffset(1, err).NotTo(gomega.HaveOccurred(), "decode base workflow-state fixture: %v", err)

	mutate(&state)

	body, err := json.MarshalIndent(state, "", "  ")
	gomega.ExpectWithOffset(1, err).NotTo(gomega.HaveOccurred(), "encode workflow-state fixture: %v", err)
	writeTestFile(t, dir, "workflow_state.json", string(body))
}

func writeProductStatusMigrationCLIFixture(t ginkgo.FullGinkgoTInterface, bucket, state string) (string, string) {
	t.Helper()

	root := t.TempDir()
	effortsRoot := filepath.Join(root, "docs", "product-workflows", "efforts")
	effortRoot := filepath.Join(effortsRoot, bucket, "test-effort")
	writeTestFile(t, effortRoot, "workflow_state.json", productStatusMigrationCLIWorkflowState(bucket, state))
	writeTestFile(t, effortRoot, "events.jsonl", `{"event":"workflow_state_created","event_id":"evt-0001","effort_id":"test-effort"}`+"\n")
	writeTestFile(t, effortsRoot, "index.json", productStatusMigrationCLIEffortIndex(bucket, state))

	return effortsRoot, effortRoot
}

func loadProductWorkflowFixtureState(t ginkgo.FullGinkgoTInterface, effortRoot string) map[string]any {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(effortRoot, "workflow_state.json"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "failed to read workflow_state.json: %v", err)

	var ws map[string]any

	{
		err := json.Unmarshal(data, &ws)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "failed to parse workflow_state.json: %v", err)
	}

	return ws
}

func writeTestFile(t ginkgo.FullGinkgoTInterface, dir, relPath, content string) {
	t.Helper()

	fullPath := filepath.Join(dir, relPath)
	{
		err := os.MkdirAll(filepath.Dir(fullPath), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "mkdir %s: %v", filepath.Dir(fullPath), err)
	}

	{
		err := os.WriteFile(fullPath, []byte(content), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "write %s: %v", fullPath, err)
	}
}

func readOptionalTestFile(t ginkgo.FullGinkgoTInterface, path string) ([]byte, bool) {
	t.Helper()

	data, err := os.ReadFile(path)
	if err == nil {
		return data, true
	}

	if os.IsNotExist(err) {
		return nil, false
	}

	gomega.ExpectWithOffset(1, err).NotTo(gomega.HaveOccurred(), "read %s", path)
	return nil, false
}

func productWorkflowTestRecoveryPacketJSON(t ginkgo.FullGinkgoTInterface) string {
	t.Helper()

	record, err := recovery.BuildPlanExecuteFailureRecord(recovery.PlanExecuteFailureInput{
		SessionID:         "session-product-cli",
		FailureKind:       "executor_failed",
		FailureReason:     "edit failed",
		PromptFile:        "docs/product-workflows/prd.md",
		RequiredEditPaths: []string{"cmd/idiom/product_workflow.go"},
		ChangedPaths:      []string{"cmd/idiom/product_workflow.go"},
	}, time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "BuildPlanExecuteFailureRecord: %v", err)

	packet, err := recovery.BuildDecompositionPacket(record, recovery.DecompositionOptions{
		Now: time.Date(2026, 5, 18, 12, 1, 0, 0, time.UTC),
		PartialState: recovery.PartialStateForDirtyPaths(
			nil,
			record.PartialState.ChangedPaths,
			record.SourceRun.RequiredEditPaths,
			record.SourceRun.AllowedEditPaths,
			record.PartialState.TotalEdits,
		),
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "BuildDecompositionPacket: %v", err)

	body, err := json.MarshalIndent(packet, "", "  ")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "marshal recovery packet: %v", err)

	return string(body)
}

func findProductOperatorAction(t ginkgo.FullGinkgoTInterface, actions []productworkflow.ProductOperatorAction, id string) productworkflow.ProductOperatorAction {
	t.Helper()
	var found *productworkflow.ProductOperatorAction

	for i := range actions {
		if actions[i].ID == id {
			found = &actions[i]
			break
		}
	}

	gomega.ExpectWithOffset(1, found).NotTo(gomega.BeNil(), "operator action %q not found in %#v", id, actions)
	return *found
}

func validTestWorkflowStateJSON() string {
	return `{
		"schema_version":"idiom.product_workflow_state.v0.fixture",
		"effort_id":"test-effort",
		"task_id":"impl-test",
		"title":"Test Effort",
		"state_revision":1,
		"last_event_id":"evt-test-0001",
		"status_bucket":"started",
		"current_phase":"tdd_synthesis",
		"current_state":"planned",
		"artifact_paths":{"events":"events.jsonl"},
		"context":{"context_pack_id":"pack","freshness_state":"current"},
		"coverage":{"coverage_tier":"unit"},
		"privacy":{"redaction_status":"applied"},
		"folder_path":"docs/test",
		"durability_level":"source_controlled",
		"next_valid_actions":["finalize"]
	}`
}

func productEffortsCLIWorkflowState(effortID, title, bucket, state, folderName string) string {
	return `{
		"schema_version":"idiom.product_workflow_state.v0.fixture",
		"effort_id":"` + effortID + `",
		"task_id":"impl-test",
		"title":"` + title + `",
		"status_bucket":"` + bucket + `",
		"current_phase":"phase-1",
		"current_state":"` + state + `",
		"state_revision":7,
		"last_event_id":"evt-007",
		"artifact_paths":{"events":"events.jsonl"},
		"context":{"context_pack_id":"pack-` + effortID + `","freshness_state":"current"},
		"coverage":{"coverage_tier":"unit"},
		"open_obligations":["TDD-001"],
		"blockers":["blocked-a"],
		"privacy":{"redaction_status":"applied"},
		"folder_path":"docs/product-workflows/efforts/` + bucket + `/` + folderName + `",
		"next_valid_actions":["submit_for_verification"],
		"durability_level":"source_controlled"
	}`
}

func productStatusMigrationCLIWorkflowState(bucket, state string) string {
	return `{
		"schema_version":"idiom.product_workflow_state.v0.fixture",
		"effort_id":"test-effort",
		"task_id":"impl-test",
		"title":"Test Effort",
		"created_at":"2026-05-18T21:00:00Z",
		"updated_at":"2026-05-18T21:00:00Z",
		"state_revision":7,
		"last_event_id":"evt-007",
		"workspace_fingerprint":"idiom@test",
		"status_bucket":"` + bucket + `",
		"current_phase":"phase-test",
		"current_state":"` + state + `",
		"artifact_paths":{"events":"events.jsonl"},
		"context":{"context_pack_id":"pack","freshness_state":"current","referenced_paths":[],"unresolved_gaps":[],"refresh_policy":"on_demand"},
		"coverage":{"coverage_tier":"unit","gate_results":[],"blocking_criteria":[],"advisory_criteria":[]},
		"open_obligations":[],
		"open_questions":[],
		"blockers":[],
		"privacy":{"redaction_status":"applied","fields_excluded":[]},
		"session_ids":{"codex":"session-1"},
		"folder_path":"docs/product-workflows/efforts/` + bucket + `/test-effort",
		"prior_folder_paths":[],
		"next_valid_actions":["complete"],
		"durability_level":"source_controlled"
	}`
}

func productStatusMigrationCLIEffortIndex(bucket, state string) string {
	return `{
		"schema_version":"idiom.effort_index.v1",
		"updated_at":"2026-05-18T21:00:00Z",
		"efforts":[{
			"effort_id":"test-effort",
			"title":"Test Effort",
			"status_bucket":"` + bucket + `",
			"current_phase":"phase-test",
			"current_state":"` + state + `",
			"folder_path":"` + bucket + `/test-effort",
			"task_id":"impl-test",
			"created_at":"2026-05-18T21:00:00Z",
			"prior_paths":[]
		}]
	}`
}

func validTestTDDJSON() string {
	return `{
		"schema_version":"idiom.tdd_obligations.v2.fixture",
		"effort_id":"test-effort",
		"task_id":"impl-test",
		"coverage_tier":"unit",
		"coverage":{"mutation_min_score":80},
		"obligations":[{
			"id":"TDD-001","type":"acceptance","behavior":"test","owner":"test","command":"go test","required_before":"close","status":"pending"
		}]
	}`
}

func validTestEvidenceJSONL() string {
	return `{"card_id":"EV-001","kind":"finding","source_handle":"file://test","claim":"test claim","confidence":"high","freshness":"current","uncertainty":"","evidence_refs":["ref"],"researcher_id":"test","timestamp":"2026-05-17T16:00:00Z"}`
}

func validTestTraceabilityMatrixJSON() string {
	return `{
		"schema_version":"idiom.traceability_matrix.v1.fixture",
		"effort_id":"test-effort",
		"task_id":"impl-test",
		"created_at":"2026-05-17T16:00:00Z",
		"matrix":[{
			"evidence_card":"EV-001","prd_requirement":"REQ-TEST-001","adr":"ADR-001","tdd_obligation":"TDD-001","brief":"briefs/test.md","report":"reports/test.json"
		}]
	}`
}

func invalidCloseoutLedgerJSONL() string {
	return strings.Join([]string{
		`{"record_type":"atom_planned","task_id":"impl-test","effort_id":"test-effort","timestamp":"2026-05-17T16:00:00Z","complexity_tier":"C1","context_pack_id":"pack","implementor":{"id":"impl","runtime":"opencode","model":"test","command":"run"},"reviewer":{"id":"rev","model":"test","reasoning_effort":"medium","runtime":"codex"},"scope":{"expected_files":["test.go"],"forbidden_patterns":["cmd/**"]},"required_verification":["go test"],"report_paths":{"implementation":"reports/impl.json","review":"reports/rev.json","investigation":"reports/inv.json"}}`,
		`{"record_type":"atom_closed","task_id":"impl-test","effort_id":"test-effort","timestamp":"2026-05-17T16:05:00Z","open_blockers":["review_pending"]}`,
	}, "\n")
}

func implementationLedgerWithoutTDDEvidenceJSONL() string {
	return strings.Join([]string{
		`{"record_type":"atom_planned","task_id":"impl-test","effort_id":"test-effort","timestamp":"2026-05-17T16:00:00Z","complexity_tier":"C1","context_pack_id":"pack","implementor":{"id":"impl","runtime":"opencode","model":"test","command":"run"},"reviewer":{"id":"rev","model":"test","reasoning_effort":"medium","runtime":"codex"},"scope":{"expected_files":["test.go"],"forbidden_patterns":["cmd/**"]},"required_verification":["go test"],"report_paths":{"implementation":"reports/impl.json","review":"reports/rev.json","investigation":"reports/inv.json"}}`,
		`{"record_type":"implementation_completed","task_id":"impl-test","effort_id":"test-effort","timestamp":"2026-05-17T16:01:00Z","implementor_id":"impl","commit_hash":"0000000000000000000000000000000000000000"}`,
		`{"record_type":"verification_completed","task_id":"impl-test","effort_id":"test-effort","timestamp":"2026-05-17T16:02:00Z"}`,
		`{"record_type":"review_completed","task_id":"impl-test","effort_id":"test-effort","timestamp":"2026-05-17T16:03:00Z","reviewer_id":"rev","model":"test","reasoning_effort":"medium"}`,
		`{"record_type":"post_effort_investigation_completed","task_id":"impl-test","effort_id":"test-effort","timestamp":"2026-05-17T16:04:00Z"}`,
		`{"record_type":"atom_closed","task_id":"impl-test","effort_id":"test-effort","timestamp":"2026-05-17T16:05:00Z","open_blockers":[]}`,
	}, "\n")
}

func validTestImplementationLedgerJSONL() string {
	return strings.Replace(
		implementationLedgerWithoutTDDEvidenceJSONL(),
		`{"record_type":"verification_completed","task_id":"impl-test","effort_id":"test-effort","timestamp":"2026-05-17T16:02:00Z"}`,
		`{"record_type":"verification_completed","task_id":"impl-test","effort_id":"test-effort","timestamp":"2026-05-17T16:02:00Z","tdd_obligations_satisfied":["TDD-001"]}`,
		1,
	)
}

func validTestResearchProfileJSON() string {
	return validTestResearchProfileJSONWithPolicy("test-profile", "test-profile", "block_prd_audit")
}

func validTestResearchProfileJSONWithPolicy(name, profileID, unsupportedClaimsPolicy string) string {
	return `{
			"schema_version": "idiom.research_profile.v1",
			"name": "` + name + `",
			"profile_id": "` + profileID + `",
			"researchers": [
				{
					"id": "r1",
					"alignment": "technical",
					"question": "What changed?",
				"allowed_capabilities": ["query_context"],
				"output": "evidence_cards"
			}
			],
			"merge_policy": {
				"conflicts": "preserve_until_adr",
				"unsupported_claims": "` + unsupportedClaimsPolicy + `",
				"stale_local_context": "refresh_or_mark_gap"
			}
		}`
}

func writeProductResearchProfile(t ginkgo.FullGinkgoTInterface, dir, content string) {
	t.Helper()
	writeProductResearchProfileFile(t, dir, "test.json", content)
}

func writeProductResearchProfileFile(t ginkgo.FullGinkgoTInterface, dir, name, content string) {
	t.Helper()

	profilesDir := filepath.Join(dir, ".idiom", "research", "profiles")
	{
		err := os.MkdirAll(profilesDir, 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "mkdir research profiles: %v", err)
	}

	{
		err := os.WriteFile(filepath.Join(profilesDir, name), []byte(content), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "write research profile: %v", err)
	}
}

func validTestResearchCardsJSON(cardID, researcherID, claim string) string {
	return `{
		"schema_version": "idiom.research_cards.v1",
		"researcher_id": "` + researcherID + `",
		"alignment": "technical",
		"cards": [
			{
				"card_id": "` + cardID + `",
				"kind": "finding",
				"source_handle": "file://README.md",
				"claim": "` + claim + `",
				"confidence": "high",
				"freshness": "current",
				"uncertainty": "",
				"evidence_refs": ["README.md:1"],
				"researcher_id": "` + researcherID + `",
				"timestamp": "2026-05-18T00:00:00Z"
			}
		]
	}`
}

func validTestPRDMarkdown() string {
	return `---
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
}

func validTestADRIndexJSON() string {
	return `{"schema_version":"v1","effort_id":"test-effort","adrs":[{"adr_id":"ADR-001","title":"Test ADR","status":"accepted","path":"ADR-001-test.md","coverage_tier":"unit","prd_refs":["REQ-TEST-001"],"created_at":"2026-05-17T00:00:00Z"}]}`
}

func validTestADRMarkdown() string {
	return `---
schema_version: idiom.adr.v1
adr_id: ADR-001
status: accepted
accepted_at: 2026-05-17T00:00:00Z
accepted_by: test-reviewer
acceptance_profile: human-review
prd_refs:
  - REQ-TEST-001
evidence_refs:
  - EV-001
tdd_obligations:
  - TDD-001
coverage_tier: unit
---

# ADR-001: Test

## Context

Test context.

## Decision

Test decision.

test.go

~~~text
package test
~~~

## Alternatives Considered

None.

## Evidence

- EV-001: Test evidence.

## Consequences

Test consequences.

## Compatibility and Migration

Compatible.

## TDD Obligations

- TDD-001: Test obligation.

## Rollback

Revert.

## Open Questions

None.
`
}

func validTestImplementationReportJSON() string {
	return `{
		"schema_version":"idiom.implementation_report.v1.fixture",
		"task_id":"impl-test",
		"effort_id":"test-effort",
		"implementor_id":"test-agent",
		"runtime":"opencode",
		"model":"test-model",
		"completed_at":"2026-05-17T16:00:00Z",
		"complexity_tier":"C1",
		"scope_statement":"Test scope",
		"code_changes":false,
		"changed_paths":[],
		"verification":{"run_by_implementor":false,"commands":[],"status":"pending"},
		"privacy_note":"No raw prompts, raw transcripts, file bodies, secrets, or customer data stored.",
		"open_blockers":["reviewer_pending"],
		"reviewer_id":"test-reviewer"
	}`
}

func validTestReviewReportJSON() string {
	return `{
		"schema_version":"idiom.review_report.v1.fixture",
		"effort_id":"test-effort",
		"task_id":"impl-test",
		"reviewer":{
			"agent_id":"test-reviewer",
			"model":"GPT-5.4",
			"reasoning_effort":"medium",
			"independent_from_implementor":true
		},
		"status":"pending",
		"findings":[],
		"checks":{},
		"reviewed_at":"",
		"commands_reviewed":[],
		"created_at":"2026-05-17T16:00:00Z"
	}`
}

func validTestTestInvestigationReportJSON() string {
	return `{
		"schema_version":"idiom.investigation_report.v1.fixture",
		"effort_id":"test-effort",
		"task_id":"impl-test",
		"status":"pending",
		"created_at":"2026-05-17T16:00:00Z",
		"scope":{"files":["a.go"]},
		"tests":{"focused":"go test"},
		"coverage":{"tier":"unit"},
		"traceability":{"checked":false},
		"privacy_audit":{"no_raw_prompts":true},
		"reviewer_findings":[],
		"blockers":["implementation_pending"],
		"next_valid_action":"",
		"completed_at":""
	}`
}

const scopeGatePlannedAtom = `{"record_type":"atom_planned","task_id":"impl-scope-test","effort_id":"test-effort","timestamp":"2026-05-17T16:00:00Z","complexity_tier":"C1","context_pack_id":"pack","implementor":{"id":"impl","runtime":"opencode","model":"test","command":"run"},"reviewer":{"id":"rev","model":"test","reasoning_effort":"medium","runtime":"codex"},"scope":{"expected_files":["cmd/idiom/product_workflow.go"],"forbidden":["internal/workflows/productworkflow/scope.go"],"forbidden_patterns":["docs/**"]},"required_verification":["go test"],"report_paths":{"implementation":"reports/impl.json","review":"reports/rev.json","investigation":"reports/inv.json"}}`

func scopeGateLedgerForbiddenExactPathJSONL() string {
	return scopeGatePlannedAtom + "\n" +
		`{"record_type":"implementation_completed","task_id":"impl-scope-test","effort_id":"test-effort","timestamp":"2026-05-17T16:01:00Z","implementor_id":"impl","changed_paths":["cmd/idiom/product_workflow.go","internal/workflows/productworkflow/scope.go"]}`
}

func scopeGateLedgerForbiddenPatternJSONL() string {
	return scopeGatePlannedAtom + "\n" +
		`{"record_type":"implementation_completed","task_id":"impl-scope-test","effort_id":"test-effort","timestamp":"2026-05-17T16:01:00Z","implementor_id":"impl","changed_paths":["cmd/idiom/product_workflow.go","docs/design.md"]}`
}

func scopeGateLedgerUnexpectedPathJSONL() string {
	return scopeGatePlannedAtom + "\n" +
		`{"record_type":"implementation_completed","task_id":"impl-scope-test","effort_id":"test-effort","timestamp":"2026-05-17T16:01:00Z","implementor_id":"impl","changed_paths":["cmd/idiom/product_workflow.go","internal/workflows/productworkflow/validate.go"]}`
}

func scopeGateLedgerMissingExpectedPathJSONL() string {
	return `{"record_type":"atom_planned","task_id":"impl-scope-test","effort_id":"test-effort","timestamp":"2026-05-17T16:00:00Z","complexity_tier":"C1","context_pack_id":"pack","implementor":{"id":"impl","runtime":"opencode","model":"test","command":"run"},"reviewer":{"id":"rev","model":"test","reasoning_effort":"medium","runtime":"codex"},"scope":{"expected_files":["cmd/idiom/product_workflow.go","cmd/idiom/main.go"],"forbidden":["internal/workflows/productworkflow/scope.go"],"forbidden_patterns":["docs/**"]},"required_verification":["go test"],"report_paths":{"implementation":"reports/impl.json","review":"reports/rev.json","investigation":"reports/inv.json"}}` + "\n" +
		`{"record_type":"implementation_completed","task_id":"impl-scope-test","effort_id":"test-effort","timestamp":"2026-05-17T16:01:00Z","implementor_id":"impl","changed_paths":["cmd/idiom/product_workflow.go"]}`
}

func scopeGateLedgerPassingJSONL() string {
	return scopeGatePlannedAtom + "\n" +
		`{"record_type":"implementation_completed","task_id":"impl-scope-test","effort_id":"test-effort","timestamp":"2026-05-17T16:01:00Z","implementor_id":"impl","changed_paths":["cmd/idiom/product_workflow.go"]}`
}

func writeScopeGateEffort(t ginkgo.FullGinkgoTInterface, ledgerJSONL string) string {
	t.Helper()

	dir := writeValidTestEffort(t)

	if !strings.Contains(ledgerJSONL, "tdd_obligations_satisfied") {
		ledgerJSONL += "\n" + `{"record_type":"verification_completed","task_id":"impl-test","effort_id":"test-effort","timestamp":"2026-05-17T16:02:00Z","tdd_obligations_satisfied":["TDD-001"]}`
	}

	writeTestFile(t, dir, "implementation_ledger.jsonl", ledgerJSONL)

	return dir
}

func writeMinimalPassingBriefEffort(t ginkgo.FullGinkgoTInterface, dir string) {
	t.Helper()
	writeValidTestWorkflowAuthority(t, dir)
	writeTestFile(t, dir, "tdd.json", validTestTDDJSON())
	writeTestFile(t, dir, "evidence/evidence.jsonl", validTestEvidenceJSONL())
	writeTestFile(t, dir, "evidence/traceability_matrix.json", validTestTraceabilityMatrixJSON())
	writeTestFile(t, dir, "implementation_ledger.jsonl", validTestImplementationLedgerJSONL())

	acceptedPRD := strings.Replace(validTestPRDMarkdown(), "status: draft", "status: accepted", 1)

	acceptedPRD = strings.Replace(acceptedPRD, "created_at: 2026-05-17T00:00:00Z", "created_at: 2026-05-17T00:00:00Z\naccepted_at: 2026-05-18T00:00:00Z\naccepted_by: reviewer\nacceptance_profile: human-review", 1)
	writeTestFile(t, dir, "prd.md", acceptedPRD)
	writeTestFile(t, dir, "adrs/index.json", validTestADRIndexJSON())
	writeTestFile(t, dir, "adrs/ADR-001-test.md", validTestADRMarkdown())
	writeTestFile(t, dir, "briefs/test.md", "# Test Brief")
	writeTestFile(t, dir, "reports/test.json", "{}")
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}

	return false
}

func validRankerLearningFeedbackSummaryJSON(status string) string {
	if status == "blocked" {
		return `{
  "status":"blocked",
  "effort_id":"test-effort",
  "task_id":"impl-test",
  "state_revision":1,
  "current_phase":"phase-5-execution-feedback",
  "current_state":"planned",
  "lane_history":[{"lane":"implement","event":"closed","status":"pass","timestamp":"2026-05-18T00:00:00Z"}],
  "plan_phase":"phase-5",
  "plan_state":"feedback",
  "plan_status":"completed",
  "product_evidence":{"id":"pe-1","path":"product_evidence/export-1.json"},
  "release_evidence":{"id":"re-1","path":"release_evidence/bundle-1.json"},
  "brief_path":"briefs/test.md",
  "changed_paths":["../bad"],
  "verification":{"status":"fail","commands":[]},
  "tdd_obligations":["TDD-404"],
  "findings":[],
  "blockers":[{"kind":"verification_not_pass","message":"verification failed","next_valid_action":"repair before ranker weights"}],
  "next_valid_action":"repair before deriving ranker weights"
}`
	}

	return `{
  "status":"pass",
  "effort_id":"test-effort",
  "task_id":"impl-test",
  "state_revision":1,
  "current_phase":"phase-5-execution-feedback",
  "current_state":"planned",
  "lane_history":[{"lane":"implement","event":"closed","status":"pass","timestamp":"2026-05-18T00:00:00Z"}],
  "plan_phase":"phase-5",
  "plan_state":"feedback",
  "plan_status":"completed",
  "product_evidence":{"id":"pe-1","path":"product_evidence/export-1.json"},
  "release_evidence":{"id":"re-1","path":"release_evidence/bundle-1.json"},
  "brief_path":"briefs/test.md",
  "changed_paths":["cmd/idiom/product_workflow.go"],
  "verification":{"status":"pass","commands":["go test ./cmd/idiom"]},
  "tdd_obligations":["TDD-001"],
  "findings":[],
  "blockers":[],
  "next_valid_action":"publish execution feedback summary as deterministic closeout evidence"
}`
}
