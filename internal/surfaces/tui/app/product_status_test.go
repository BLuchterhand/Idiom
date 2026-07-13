package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/llama"
	"idiom/internal/workflows/productworkflow"
	"idiom/internal/workflows/recovery"
)

var _ = ginkgo.It("slash product status includes recovery packet state", func() {
	t := ginkgo.GinkgoT()
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

	gomega.Expect(quit).To(gomega.BeFalse(), fmt.Sprint("product status command must not quit the TUI"))

	gomega.Expect(len(model.messages)).To(gomega.Equal(1), fmt.Sprintf("expected product status transcript message, got %#v", model.messages))

	gomega.Expect(model.messages[0].Role).To(gomega.Equal("operator"), fmt.Sprintf("product status output must be harness/operator-authored, got role %q", model.messages[0].Role))

	body := model.messages[0].Content

	statusView, workflowState, statusErr := productworkflow.LoadCheckedProductStatus(effortRoot)
	gomega.Expect(statusErr).NotTo(gomega.HaveOccurred(), fmt.Sprintf("LoadCheckedProductStatus: %v", statusErr))

	expected := strings.TrimRight(productworkflow.FormatProductOperatorStatusText(
		filepath.Base(effortRoot),
		productworkflow.BuildProductOperatorStatusView(statusView, workflowState),
	), "\n")

	expected += "\n  tui_command_hint=/recover-status --packet test-effort/references/recovery_packet.json"

	gomega.Expect(body).To(gomega.Equal(expected), fmt.Sprintf("slash product status must render the shared operator formatter.\nwant:\n%s\n\ngot:\n%s", expected, body))

	for _, want := range []string{
		"product operator status: test-effort app-effort",
		"recovery: ready next=review_decomposition candidate_atoms=1 packet=references/recovery_packet.json",
		"[recovery_packet_open] review recovery packet before continuing workflow dispatch",
		"[audit_not_passing] product status audit_status \"fail\" blocks continuation dispatch",
		"dispatchable=false",
		"command_hint=idiom recover status --packet references/recovery_packet.json",
		"tui_command_hint=/recover-status --packet test-effort/references/recovery_packet.json",
	} {
		gomega.Expect(strings.Contains(body, want)).To(gomega.BeTrue(), fmt.Sprintf("expected slash product status output to contain %q, got:\n%s", want, body))
	}

	for _, forbidden := range []string{
		"recover promote",
		"recover reconcile",
		"recovery_decompose",
	} {
		gomega.Expect(strings.Contains(body, forbidden)).To(gomega.BeFalse(), fmt.Sprintf("slash product status must not advertise write-capable recovery action %q:\n%s", forbidden, body))
	}

	gomega.Expect(strings.Contains(model.status, "blocker")).To(gomega.BeTrue(), fmt.Sprintf("expected status line to report blockers, got %q", model.status))

	for _, msg := range newDetachedPromptExecutionSession(context.Background(), model).requestMessages(context.Background()) {
		gomega.Expect(strings.Contains(msg.Content, "product operator status:")).To(gomega.BeFalse(), fmt.Sprintf("operator status output must not be replayed into model context: %#v", newDetachedPromptExecutionSession(context.Background(), model).requestMessages(context.Background())))
	}
})

var _ = ginkgo.It("slash product status requires effort root outside effort directory", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	model := &Model{
		workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
	}
	quit := model.handleSlashCommand("/product-status")

	gomega.Expect(quit).To(gomega.BeFalse(), fmt.Sprint("product status command must not quit the TUI"))

	gomega.Expect(len(model.messages)).To(gomega.Equal(0), fmt.Sprintf("usage failure must not append a transcript message, got %#v", model.messages))

	gomega.Expect(strings.Contains(model.status, "usage: /product-status --effort-root <path>")).To(gomega.BeTrue(), fmt.Sprintf("expected explicit usage guidance outside an effort root, got %q", model.status))
})

var _ = ginkgo.It("product status tui recovery hint suppresses unsafe redacted packet path", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	effortRoot := filepath.Join(root, "test-effort")
	model := &Model{
		workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
	}

	hint := model.productStatusTUIRecoveryHint(effortRoot, productworkflow.ProductOperatorStatusView{
		Recovery: &productworkflow.ProductRecoveryStatus{PacketPath: "outside_effort_root"},
	})
	gomega.Expect(hint).To(gomega.Equal(""), fmt.Sprintf("unsafe redacted recovery packet path must not become a TUI handoff hint, got %q", hint))
})

var _ = ginkgo.It("request messages ignores operator messages for history limit", func() {
	model := &Model{
		cfg: config.Config{Chat: config.Chat{MaxHistory: 2}},
		messages: []llama.ChatMessage{
			{Role: "user", Content: "first user"},
			{Role: "operator", Content: "product operator status: should not count"},
			{Role: "assistant", Content: "first assistant"},
			{Role: "operator", Content: "product operator status: should not replay"},
		},
	}

	messages := newDetachedPromptExecutionSession(context.Background(), model).requestMessages(context.Background())
	gomega.Expect(len(messages)).To(gomega.Equal(2), fmt.Sprintf("expected two payload-eligible messages, got %#v", messages))

	gomega.Expect(messages[0].Content).To(gomega.Equal("first user"), fmt.Sprintf("operator messages must not consume MaxHistory slots, got %#v", messages))
	gomega.Expect(messages[1].Content).To(gomega.Equal("first assistant"), fmt.Sprintf("operator messages must not consume MaxHistory slots, got %#v", messages))
})

var _ = ginkgo.It("slash product status audit failure blocks dispatch", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	effortRoot := filepath.Join(root, "test-effort")
	appProductStatusWriteFile(t, effortRoot, "workflow_state.json", appProductStatusWorkflowStateJSONNoRecovery())
	appProductStatusWriteFile(t, effortRoot, "tdd.json", `{not valid json`)

	model := &Model{
		workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
	}
	model.handleSlashCommand("/product-status --effort-root " + effortRoot)

	gomega.Expect(len(model.messages)).To(gomega.Equal(1), fmt.Sprintf("expected product status transcript message, got %#v", model.messages))

	body := model.messages[0].Content
	for _, want := range []string{
		"audit: fail",
		"[audit_not_passing] product status audit_status \"fail\" blocks continuation dispatch",
		"- complete [complete] owner=operator dispatchable=false runnable_now=false",
		"blocked_by=",
	} {
		gomega.Expect(strings.Contains(body, want)).To(gomega.BeTrue(), fmt.Sprintf("expected audit-blocked product status output to contain %q, got:\n%s", want, body))
	}
})

var _ = ginkgo.It("slash product continue submits human verification", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	effortRoot := appProductContinuationEffort(t, root, "planned", "submit_for_human_verification")
	model := &Model{
		ready:     true,
		viewport:  viewport.New(100, 24),
		workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
	}

	model.handleSlashCommand("/product-continue submit_for_human_verification --effort-root " + effortRoot)

	state := appProductStatusReadWorkflowState(t, effortRoot)
	gomega.Expect(state.CurrentState).To(gomega.Equal("human_verification"), fmt.Sprintf("expected human_verification state, got %q", state.CurrentState))

	gomega.Expect(appProductStatusContains(state.NextValidActions, "approve_for_implementation")).To(gomega.BeTrue(), fmt.Sprintf("expected approve_for_implementation next action, got %#v", state.NextValidActions))

	gomega.Expect(state.StateRevision).To(gomega.Equal(2), fmt.Sprintf("expected revised workflow state, got revision=%d last_event_id=%q", state.StateRevision, state.LastEventID))
	gomega.Expect(state.LastEventID).To(gomega.Not(gomega.Equal("evt-0001")), fmt.Sprintf("expected revised workflow state, got revision=%d last_event_id=%q", state.StateRevision, state.LastEventID))

	events := appProductStatusReadFile(t, effortRoot, "events.jsonl")
	gomega.Expect(events).To(gomega.ContainSubstring(`"event":"product_workflow_transition_applied"`), fmt.Sprintf("expected transition event in events.jsonl, got:\n%s", events))
	gomega.Expect(events).To(gomega.ContainSubstring(`"action":"submit_for_human_verification"`), fmt.Sprintf("expected transition event in events.jsonl, got:\n%s", events))

	gomega.Expect(len(model.messages)).To(gomega.Equal(1), fmt.Sprintf("expected transition and refreshed status operator message, got %#v", model.messages))
	gomega.Expect(model.messages[0].Content).To(gomega.ContainSubstring("product workflow transition: applied"), fmt.Sprintf("expected transition and refreshed status operator message, got %#v", model.messages))
	gomega.Expect(model.messages[0].Content).To(gomega.ContainSubstring("approve_for_implementation"), fmt.Sprintf("expected transition and refreshed status operator message, got %#v", model.messages))
})

var _ = ginkgo.It("slash product approve applies approval transition", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	effortRoot := appProductContinuationEffort(t, root, "human_verification", "approve_for_implementation")
	model := &Model{
		ready:     true,
		viewport:  viewport.New(100, 24),
		workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
	}

	model.handleSlashCommand("/product-approve --effort-root " + effortRoot)

	state := appProductStatusReadWorkflowState(t, effortRoot)
	gomega.Expect(state.CurrentState).To(gomega.Equal("implementing"), fmt.Sprintf("expected implementing state, got %q", state.CurrentState))

	gomega.Expect(appProductStatusContains(state.NextValidActions, "submit_for_verification")).To(gomega.BeTrue(), fmt.Sprintf("expected submit_for_verification next action, got %#v", state.NextValidActions))

	events := appProductStatusReadFile(t, effortRoot, "events.jsonl")
	gomega.Expect(strings.Contains(events, `"action":"approve_for_implementation"`)).To(gomega.BeTrue(), fmt.Sprintf("expected approval event in events.jsonl, got:\n%s", events))

	gomega.Expect(len(model.messages)).To(gomega.Equal(1), fmt.Sprintf("expected approval transition operator message, got %#v", model.messages))
	gomega.Expect(model.messages[0].Content).To(gomega.ContainSubstring("state: human_verification -> implementing"), fmt.Sprintf("expected approval transition operator message, got %#v", model.messages))
})

var _ = ginkgo.It("slash product continue blocks unlisted action without mutation", func() {
	t := ginkgo.GinkgoT()
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

	{
		got := appProductStatusReadFile(t, effortRoot, "workflow_state.json")
		gomega.Expect(got).To(gomega.Equal(beforeState), fmt.Sprintf("blocked transition mutated workflow_state.json\nbefore:\n%s\n\nafter:\n%s", beforeState, got))
	}

	{
		got := appProductStatusReadFile(t, effortRoot, "events.jsonl")
		gomega.Expect(got).To(gomega.Equal(beforeEvents), fmt.Sprintf("blocked transition mutated events.jsonl\nbefore:\n%s\n\nafter:\n%s", beforeEvents, got))
	}

	gomega.Expect(len(model.messages)).To(gomega.Equal(1), fmt.Sprintf("expected action_not_listed operator blocker, got %#v", model.messages))
	gomega.Expect(model.messages[0].Content).To(gomega.ContainSubstring("[action_not_listed]"), fmt.Sprintf("expected action_not_listed operator blocker, got %#v", model.messages))
})

var _ = ginkgo.It("slash product continue rejects transition journal symlinks without mutation", func() {
	t := ginkgo.GinkgoT()
	for _, tc := range []struct {
		name         string
		relPath      string
		createParent bool
		diagnostic   string
	}{
		{
			name:       "transaction directory",
			relPath:    ".idiom-product-transition-transaction",
			diagnostic: "product workflow transition transaction path must be a directory and must not be a symlink",
		},
		{
			name:         "transaction manifest",
			relPath:      ".idiom-product-transition-transaction/manifest.json",
			createParent: true,
			diagnostic:   "transition path .idiom-product-transition-transaction/manifest.json must be a regular file and must not be a symlink",
		},
	} {
		ginkgo.By(fmt.Sprint(tc.name), func() {
			root := t.TempDir()
			effortRoot := appProductContinuationEffort(t, root, "planned", "submit_for_human_verification")
			beforeState := appProductStatusReadFile(t, effortRoot, "workflow_state.json")
			beforeEvents := appProductStatusReadFile(t, effortRoot, "events.jsonl")
			target := filepath.Join(effortRoot, "transition-journal-symlink-target")
			linkPath := filepath.Join(effortRoot, filepath.FromSlash(tc.relPath))
			targetBody := []byte("journal target must remain untouched\n")

			if tc.createParent {
				gomega.Expect(os.MkdirAll(filepath.Dir(linkPath), 0o755)).To(gomega.Succeed())
			}
			gomega.Expect(os.WriteFile(target, targetBody, 0o644)).To(gomega.Succeed())
			gomega.Expect(os.Symlink(target, linkPath)).To(gomega.Succeed())

			model := &Model{
				ready:     true,
				viewport:  viewport.New(100, 24),
				workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
			}

			model.handleSlashCommand("/product-continue submit_for_human_verification --effort-root " + effortRoot)

			{
				got := appProductStatusReadFile(t, effortRoot, "workflow_state.json")
				gomega.Expect(got).To(gomega.Equal(beforeState), fmt.Sprintf("blocked transition mutated workflow_state.json\nbefore:\n%s\n\nafter:\n%s", beforeState, got))
			}

			{
				got := appProductStatusReadFile(t, effortRoot, "events.jsonl")
				gomega.Expect(got).To(gomega.Equal(beforeEvents), fmt.Sprintf("blocked transition mutated events.jsonl\nbefore:\n%s\n\nafter:\n%s", beforeEvents, got))
			}

			gomega.Expect(os.ReadFile(target)).To(gomega.Equal(targetBody))
			linkInfo, err := os.Lstat(linkPath)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(linkInfo.Mode() & os.ModeSymlink).NotTo(gomega.BeZero())

			gomega.Expect(model.messages).To(gomega.HaveLen(1), fmt.Sprintf("expected journal symlink transition_write_error, got %#v", model.messages))
			for _, want := range []string{
				"product workflow transition: blocked",
				"[product_workflow_lock_error] workflow_state.json:",
				"recover product workflow transition",
				tc.diagnostic,
			} {
				gomega.Expect(model.messages[0].Content).To(gomega.ContainSubstring(want), fmt.Sprintf("expected journal symlink diagnostic %q, got %#v", want, model.messages))
			}
		})
	}
})

func appProductStatusWriteFile(t ginkgo.FullGinkgoTInterface, dir, relPath, content string) {
	ginkgo.GinkgoHelper()

	fullPath := filepath.Join(dir, filepath.FromSlash(relPath))
	{
		err := os.MkdirAll(filepath.Dir(fullPath), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir %s: %v", filepath.Dir(fullPath), err))
	}

	{
		err := os.WriteFile(fullPath, []byte(content), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write %s: %v", fullPath, err))
	}
}

func appProductStatusReadFile(t ginkgo.FullGinkgoTInterface, dir, relPath string) string {
	ginkgo.GinkgoHelper()

	data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(relPath)))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read %s: %v", relPath, err))

	return string(data)
}

func appProductStatusReadWorkflowState(t ginkgo.FullGinkgoTInterface, dir string) productworkflow.WorkflowState {
	ginkgo.GinkgoHelper()

	state, err := productworkflow.ParseWorkflowStateJSON([]byte(appProductStatusReadFile(t, dir, "workflow_state.json")))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("parse workflow_state.json: %v", err))

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

func appProductContinuationEffort(t ginkgo.FullGinkgoTInterface, root, state, action string) string {
	ginkgo.GinkgoHelper()

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

func appProductStatusRecoveryPacketJSON(t ginkgo.FullGinkgoTInterface) string {
	ginkgo.GinkgoHelper()

	record, err := recovery.BuildPlanExecuteFailureRecord(recovery.PlanExecuteFailureInput{
		SessionID:         "session-app-product-status",
		FailureKind:       "executor_failed",
		FailureReason:     "edit failed",
		PromptFile:        "docs/product-workflows/prd.md",
		RequiredEditPaths: []string{"internal/surfaces/tui/app/product_status.go"},
		ChangedPaths:      []string{"internal/surfaces/tui/app/product_status.go"},
		TotalEdits:        1,
	}, time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("BuildPlanExecuteFailureRecord: %v", err))

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
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("BuildDecompositionPacket: %v", err))

	body, err := json.MarshalIndent(packet, "", "  ")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal recovery packet: %v", err))

	return string(body)
}
