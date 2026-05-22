package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/viewport"

	"idiom/internal/config"
	"idiom/internal/llama"
	"idiom/internal/productworkflow"
	"idiom/internal/recovery"
	"idiom/internal/workspace"
)

func TestSlashProductStatusIncludesRecoveryPacketState(t *testing.T) {
	root := t.TempDir()
	effortRoot := filepath.Join(root, "test-effort")
	appProductStatusWriteFile(t, effortRoot, "references/recovery_packet.json", appProductStatusRecoveryPacketJSON(t))
	appProductStatusWriteFile(t, effortRoot, "workflow_state.json", appProductStatusWorkflowStateJSON())

	model := &Model{
		ready:     true,
		viewport:  viewport.New(80, 20),
		workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
	}
	quit := model.handleSlashCommand("/product-status --effort-root " + effortRoot)
	if quit {
		t.Fatal("product status command must not quit the TUI")
	}
	if len(model.messages) != 1 {
		t.Fatalf("expected product status transcript message, got %#v", model.messages)
	}
	if model.messages[0].Role != "operator" {
		t.Fatalf("product status output must be harness/operator-authored, got role %q", model.messages[0].Role)
	}
	body := model.messages[0].Content
	statusView, workflowState, statusErr := productworkflow.LoadCheckedProductStatus(effortRoot)
	if statusErr != nil {
		t.Fatalf("LoadCheckedProductStatus: %v", statusErr)
	}
	expected := strings.TrimRight(productworkflow.FormatProductOperatorStatusText(
		filepath.Base(effortRoot),
		productworkflow.BuildProductOperatorStatusView(statusView, workflowState),
	), "\n")
	expected += "\n  tui_command_hint=/recover-status --packet test-effort/references/recovery_packet.json"
	if body != expected {
		t.Fatalf("slash product status must render the shared operator formatter.\nwant:\n%s\n\ngot:\n%s", expected, body)
	}
	for _, want := range []string{
		"product operator status: test-effort app-effort",
		"recovery: ready next=review_decomposition candidate_atoms=1 packet=references/recovery_packet.json",
		"[recovery_packet_open] review recovery packet before continuing workflow dispatch",
		"[audit_not_passing] product status audit_status \"fail\" blocks continuation dispatch",
		"dispatchable=false",
		"command_hint=idiom recover status --packet references/recovery_packet.json",
		"tui_command_hint=/recover-status --packet test-effort/references/recovery_packet.json",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected slash product status output to contain %q, got:\n%s", want, body)
		}
	}
	for _, forbidden := range []string{
		"recover promote",
		"recover reconcile",
		"recovery_decompose",
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("slash product status must not advertise write-capable recovery action %q:\n%s", forbidden, body)
		}
	}
	if !strings.Contains(model.status, "blocker") {
		t.Fatalf("expected status line to report blockers, got %q", model.status)
	}
	for _, msg := range model.requestMessages() {
		if strings.Contains(msg.Content, "product operator status:") {
			t.Fatalf("operator status output must not be replayed into model context: %#v", model.requestMessages())
		}
	}
}

func TestSlashProductStatusRequiresEffortRootOutsideEffortDirectory(t *testing.T) {
	root := t.TempDir()
	model := &Model{
		workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
	}
	quit := model.handleSlashCommand("/product-status")
	if quit {
		t.Fatal("product status command must not quit the TUI")
	}
	if len(model.messages) != 0 {
		t.Fatalf("usage failure must not append a transcript message, got %#v", model.messages)
	}
	if !strings.Contains(model.status, "Usage: /product-status --effort-root <path>") {
		t.Fatalf("expected explicit usage guidance outside an effort root, got %q", model.status)
	}
}

func TestProductStatusTUIRecoveryHintSuppressesUnsafeRedactedPacketPath(t *testing.T) {
	root := t.TempDir()
	effortRoot := filepath.Join(root, "test-effort")
	model := &Model{
		workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
	}

	hint := model.productStatusTUIRecoveryHint(effortRoot, productworkflow.ProductOperatorStatusView{
		Recovery: &productworkflow.ProductRecoveryStatus{PacketPath: "outside_effort_root"},
	})
	if hint != "" {
		t.Fatalf("unsafe redacted recovery packet path must not become a TUI handoff hint, got %q", hint)
	}
}

func TestRequestMessagesIgnoresOperatorMessagesForHistoryLimit(t *testing.T) {
	model := &Model{
		cfg: config.Config{Chat: config.Chat{MaxHistory: 2}},
		messages: []llama.ChatMessage{
			{Role: "user", Content: "first user"},
			{Role: "operator", Content: "product operator status: should not count"},
			{Role: "assistant", Content: "first assistant"},
			{Role: "operator", Content: "product operator status: should not replay"},
		},
	}

	messages := model.requestMessages()
	if len(messages) != 2 {
		t.Fatalf("expected two payload-eligible messages, got %#v", messages)
	}
	if messages[0].Content != "first user" || messages[1].Content != "first assistant" {
		t.Fatalf("operator messages must not consume MaxHistory slots, got %#v", messages)
	}
}

func TestSlashProductStatusAuditFailureBlocksDispatch(t *testing.T) {
	root := t.TempDir()
	effortRoot := filepath.Join(root, "test-effort")
	appProductStatusWriteFile(t, effortRoot, "workflow_state.json", appProductStatusWorkflowStateJSONNoRecovery())
	appProductStatusWriteFile(t, effortRoot, "tdd.json", `{not valid json`)

	model := &Model{
		workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
	}
	model.handleSlashCommand("/product-status --effort-root " + effortRoot)

	if len(model.messages) != 1 {
		t.Fatalf("expected product status transcript message, got %#v", model.messages)
	}
	body := model.messages[0].Content
	for _, want := range []string{
		"audit: fail",
		"[audit_not_passing] product status audit_status \"fail\" blocks continuation dispatch",
		"- complete [complete] owner=operator dispatchable=false runnable_now=false",
		"blocked_by=",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected audit-blocked product status output to contain %q, got:\n%s", want, body)
		}
	}
}

func TestSlashProductContinueSubmitsHumanVerification(t *testing.T) {
	root := t.TempDir()
	effortRoot := appProductContinuationEffort(t, root, "planned", "submit_for_human_verification")
	model := &Model{
		ready:     true,
		viewport:  viewport.New(100, 24),
		workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
	}

	model.handleSlashCommand("/product-continue submit_for_human_verification --effort-root " + effortRoot)

	state := appProductStatusReadWorkflowState(t, effortRoot)
	if state.CurrentState != "human_verification" {
		t.Fatalf("expected human_verification state, got %q", state.CurrentState)
	}
	if !appProductStatusContains(state.NextValidActions, "approve_for_implementation") {
		t.Fatalf("expected approve_for_implementation next action, got %#v", state.NextValidActions)
	}
	if state.StateRevision != 2 || state.LastEventID == "evt-0001" {
		t.Fatalf("expected revised workflow state, got revision=%d last_event_id=%q", state.StateRevision, state.LastEventID)
	}
	events := appProductStatusReadFile(t, effortRoot, "events.jsonl")
	if !strings.Contains(events, `"event":"product_workflow_transition_applied"`) || !strings.Contains(events, `"action":"submit_for_human_verification"`) {
		t.Fatalf("expected transition event in events.jsonl, got:\n%s", events)
	}
	if len(model.messages) != 1 || !strings.Contains(model.messages[0].Content, "product workflow transition: applied") || !strings.Contains(model.messages[0].Content, "approve_for_implementation") {
		t.Fatalf("expected transition and refreshed status operator message, got %#v", model.messages)
	}
}

func TestSlashProductApproveAppliesApprovalTransition(t *testing.T) {
	root := t.TempDir()
	effortRoot := appProductContinuationEffort(t, root, "human_verification", "approve_for_implementation")
	model := &Model{
		ready:     true,
		viewport:  viewport.New(100, 24),
		workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
	}

	model.handleSlashCommand("/product-approve --effort-root " + effortRoot)

	state := appProductStatusReadWorkflowState(t, effortRoot)
	if state.CurrentState != "implementing" {
		t.Fatalf("expected implementing state, got %q", state.CurrentState)
	}
	if !appProductStatusContains(state.NextValidActions, "submit_for_verification") {
		t.Fatalf("expected submit_for_verification next action, got %#v", state.NextValidActions)
	}
	events := appProductStatusReadFile(t, effortRoot, "events.jsonl")
	if !strings.Contains(events, `"action":"approve_for_implementation"`) {
		t.Fatalf("expected approval event in events.jsonl, got:\n%s", events)
	}
	if len(model.messages) != 1 || !strings.Contains(model.messages[0].Content, "state: human_verification -> implementing") {
		t.Fatalf("expected approval transition operator message, got %#v", model.messages)
	}
}

func TestSlashProductContinueBlocksUnlistedActionWithoutMutation(t *testing.T) {
	root := t.TempDir()
	effortRoot := appProductContinuationEffort(t, root, "planned", "submit_for_human_verification")
	beforeState := appProductStatusReadFile(t, effortRoot, "workflow_state.json")
	beforeEvents := appProductStatusReadFile(t, effortRoot, "events.jsonl")
	model := &Model{
		ready:     true,
		viewport:  viewport.New(100, 24),
		workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
	}

	model.handleSlashCommand("/product-continue approve_for_implementation --effort-root " + effortRoot)

	if got := appProductStatusReadFile(t, effortRoot, "workflow_state.json"); got != beforeState {
		t.Fatalf("blocked transition mutated workflow_state.json\nbefore:\n%s\n\nafter:\n%s", beforeState, got)
	}
	if got := appProductStatusReadFile(t, effortRoot, "events.jsonl"); got != beforeEvents {
		t.Fatalf("blocked transition mutated events.jsonl\nbefore:\n%s\n\nafter:\n%s", beforeEvents, got)
	}
	if len(model.messages) != 1 || !strings.Contains(model.messages[0].Content, "[action_not_listed]") {
		t.Fatalf("expected action_not_listed operator blocker, got %#v", model.messages)
	}
}

func TestSlashProductContinueRejectsTransitionTempSymlinksWithoutMutation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		relTemp string
	}{
		{name: "workflow temp", relTemp: "workflow_state.json.product-transition.tmp"},
		{name: "events temp", relTemp: "events.jsonl.product-transition.tmp"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			effortRoot := appProductContinuationEffort(t, root, "planned", "submit_for_human_verification")
			beforeState := appProductStatusReadFile(t, effortRoot, "workflow_state.json")
			beforeEvents := appProductStatusReadFile(t, effortRoot, "events.jsonl")
			target := filepath.Join(effortRoot, "transition-temp-target")
			if err := os.WriteFile(target, []byte("target"), 0o644); err != nil {
				t.Fatalf("write symlink target: %v", err)
			}
			if err := os.Symlink(target, filepath.Join(effortRoot, filepath.FromSlash(tc.relTemp))); err != nil {
				t.Fatalf("create temp symlink: %v", err)
			}
			model := &Model{
				ready:     true,
				viewport:  viewport.New(100, 24),
				workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
			}

			model.handleSlashCommand("/product-continue submit_for_human_verification --effort-root " + effortRoot)

			if got := appProductStatusReadFile(t, effortRoot, "workflow_state.json"); got != beforeState {
				t.Fatalf("blocked transition mutated workflow_state.json\nbefore:\n%s\n\nafter:\n%s", beforeState, got)
			}
			if got := appProductStatusReadFile(t, effortRoot, "events.jsonl"); got != beforeEvents {
				t.Fatalf("blocked transition mutated events.jsonl\nbefore:\n%s\n\nafter:\n%s", beforeEvents, got)
			}
			if len(model.messages) != 1 || !strings.Contains(model.messages[0].Content, "transition_write_error") || !strings.Contains(model.messages[0].Content, "must not be a symlink") {
				t.Fatalf("expected temp symlink transition_write_error, got %#v", model.messages)
			}
		})
	}
}

func appProductStatusWriteFile(t *testing.T, dir, relPath, content string) {
	t.Helper()
	fullPath := filepath.Join(dir, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(fullPath), err)
	}
	if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", fullPath, err)
	}
}

func appProductStatusReadFile(t *testing.T, dir, relPath string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(relPath)))
	if err != nil {
		t.Fatalf("read %s: %v", relPath, err)
	}
	return string(data)
}

func appProductStatusReadWorkflowState(t *testing.T, dir string) productworkflow.WorkflowState {
	t.Helper()
	state, err := productworkflow.ParseWorkflowStateJSON([]byte(appProductStatusReadFile(t, dir, "workflow_state.json")))
	if err != nil {
		t.Fatalf("parse workflow_state.json: %v", err)
	}
	return state
}

func appProductStatusContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func appProductContinuationEffort(t *testing.T, root, state, action string) string {
	t.Helper()
	effortRoot := filepath.Join(root, "test-effort")
	appProductStatusWriteFile(t, effortRoot, "workflow_state.json", appProductContinuationWorkflowStateJSON(state, action))
	appProductStatusWriteFile(t, effortRoot, "events.jsonl", `{"event":"workflow_state_created","event_id":"evt-0001","effort_id":"app-effort","task_id":"impl-app","timestamp":"2026-05-19T00:00:00Z"}`+"\n")
	appProductStatusWriteFile(t, effortRoot, "tdd.json", `{"schema_version":"idiom.tdd_obligations.v2.fixture","effort_id":"app-effort","task_id":"impl-app","coverage_tier":"unit","coverage":{"mutation_min_score":80},"obligations":[{"id":"TDD-001","type":"acceptance","behavior":"test","owner":"test","command":"go test","required_before":"close","status":"pending"}]}`)
	appProductStatusWriteFile(t, effortRoot, "evidence/evidence.jsonl", `{"card_id":"EV-001","kind":"finding","source_handle":"file://test","claim":"test claim","confidence":"high","freshness":"current","uncertainty":"","evidence_refs":["ref"],"researcher_id":"test","timestamp":"2026-05-17T16:00:00Z"}`+"\n")
	appProductStatusWriteFile(t, effortRoot, "evidence/traceability_matrix.json", `{"schema_version":"idiom.traceability_matrix.v1.fixture","effort_id":"app-effort","task_id":"impl-app","created_at":"2026-05-17T16:00:00Z","matrix":[{"evidence_card":"EV-001","prd_requirement":"REQ-TEST-001","adr":"ADR-001","tdd_obligation":"TDD-001","brief":"briefs/test.md","report":"reports/test.json"}]}`)
	appProductStatusWriteFile(t, effortRoot, "implementation_ledger.jsonl", strings.Join([]string{
		`{"record_type":"atom_planned","task_id":"impl-app","effort_id":"app-effort","timestamp":"2026-05-17T16:00:00Z","complexity_tier":"C1","context_pack_id":"pack","implementor":{"id":"impl","runtime":"opencode","model":"test","command":"run"},"reviewer":{"id":"rev","model":"test","reasoning_effort":"medium","runtime":"codex"},"scope":{"expected_files":["test.go"],"forbidden_patterns":["cmd/**"]},"required_verification":["go test"],"report_paths":{"implementation":"reports/impl.json","review":"reports/rev.json","investigation":"reports/inv.json"}}`,
		`{"record_type":"implementation_completed","task_id":"impl-app","effort_id":"app-effort","timestamp":"2026-05-17T16:01:00Z","implementor_id":"impl","commit_hash":"0000000000000000000000000000000000000000"}`,
		`{"record_type":"verification_completed","task_id":"impl-app","effort_id":"app-effort","timestamp":"2026-05-17T16:02:00Z","tdd_obligations_satisfied":["TDD-001"]}`,
		`{"record_type":"review_completed","task_id":"impl-app","effort_id":"app-effort","timestamp":"2026-05-17T16:03:00Z","reviewer_id":"rev","model":"test","reasoning_effort":"medium"}`,
		`{"record_type":"post_effort_investigation_completed","task_id":"impl-app","effort_id":"app-effort","timestamp":"2026-05-17T16:04:00Z"}`,
		`{"record_type":"atom_closed","task_id":"impl-app","effort_id":"app-effort","timestamp":"2026-05-17T16:05:00Z","open_blockers":[]}`,
	}, "\n"))
	appProductStatusWriteFile(t, effortRoot, "prd.md", appProductContinuationPRDMarkdown())
	appProductStatusWriteFile(t, effortRoot, "adrs/index.json", `{"schema_version":"v1","effort_id":"app-effort","adrs":[{"adr_id":"ADR-001","title":"Test ADR","status":"accepted","path":"ADR-001-test.md","coverage_tier":"unit","prd_refs":["REQ-TEST-001"],"created_at":"2026-05-17T00:00:00Z"}]}`)
	appProductStatusWriteFile(t, effortRoot, "adrs/ADR-001-test.md", appProductContinuationADRMarkdown())
	appProductStatusWriteFile(t, effortRoot, "briefs/test.md", "# Test Brief\n")
	appProductStatusWriteFile(t, effortRoot, "reports/test.json", "{}")
	appProductStatusWriteFile(t, effortRoot, productworkflow.ObsidianProductWorkflowPath, "> **Status: PARTIALLY IMPLEMENTED**\n")
	appProductStatusWriteFile(t, effortRoot, productworkflow.ObsidianRecoveryPath, "# Recovery and Decomposition\n")
	return effortRoot
}

func appProductContinuationWorkflowStateJSON(state, action string) string {
	return `{
		"schema_version":"idiom.product_workflow_state.v0.fixture",
		"effort_id":"app-effort",
		"task_id":"impl-app",
		"title":"App Effort",
		"created_at":"2026-05-19T00:00:00Z",
		"updated_at":"2026-05-19T00:00:00Z",
		"status_bucket":"started",
		"current_phase":"phase-test",
		"current_state":"` + state + `",
		"state_revision":1,
		"last_event_id":"evt-0001",
		"artifact_paths":{"events":"events.jsonl"},
		"context":{"context_pack_id":"pack","freshness_state":"current","referenced_paths":[],"unresolved_gaps":[],"refresh_policy":"on_demand"},
		"coverage":{"coverage_tier":"unit","gate_results":[],"blocking_criteria":[],"advisory_criteria":[]},
		"open_obligations":[],
		"open_questions":[],
		"blockers":[],
		"privacy":{"redaction_status":"applied","fields_excluded":[]},
		"folder_path":"docs/test",
		"prior_folder_paths":[],
		"durability_level":"source_controlled",
		"next_valid_actions":["` + action + `"]
	}`
}

func appProductContinuationPRDMarkdown() string {
	return `---
schema_version: idiom.prd.v1
prd_id: prd-test
status: draft
task_id: impl-app
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

func appProductContinuationADRMarkdown() string {
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

func appProductStatusWorkflowStateJSON() string {
	return `{
		"schema_version":"idiom.product_workflow_state.v0.fixture",
		"effort_id":"app-effort",
		"task_id":"impl-app",
		"title":"App Effort",
		"status_bucket":"started",
		"current_phase":"phase-1",
		"current_state":"implementing",
		"state_revision":7,
		"last_event_id":"evt-0007",
		"artifact_paths":{"events":"events.jsonl","recovery_packet":"references/recovery_packet.json"},
		"context":{"context_pack_id":"pack","freshness_state":"current"},
		"coverage":{"coverage_tier":"unit"},
		"privacy":{"redaction_status":"applied"},
		"folder_path":"docs/test",
		"durability_level":"source_controlled",
		"next_valid_actions":["complete"]
	}`
}

func appProductStatusWorkflowStateJSONNoRecovery() string {
	return strings.Replace(
		appProductStatusWorkflowStateJSON(),
		`"artifact_paths":{"events":"events.jsonl","recovery_packet":"references/recovery_packet.json"}`,
		`"artifact_paths":{"events":"events.jsonl"}`,
		1,
	)
}

func appProductStatusRecoveryPacketJSON(t *testing.T) string {
	t.Helper()
	record, err := recovery.BuildPlanExecuteFailureRecord(recovery.PlanExecuteFailureInput{
		SessionID:         "session-app-product-status",
		FailureKind:       "executor_failed",
		FailureReason:     "edit failed",
		PromptFile:        "docs/product-workflows/prd.md",
		RequiredEditPaths: []string{"internal/app/product_status.go"},
		ChangedPaths:      []string{"internal/app/product_status.go"},
		TotalEdits:        1,
	}, time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("BuildPlanExecuteFailureRecord: %v", err)
	}
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
	if err != nil {
		t.Fatalf("BuildDecompositionPacket: %v", err)
	}
	body, err := json.MarshalIndent(packet, "", "  ")
	if err != nil {
		t.Fatalf("marshal recovery packet: %v", err)
	}
	return string(body)
}
