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
