package app

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/viewport"

	"idiom/internal/config"
	"idiom/internal/featureplanner"
	"idiom/internal/planexecute"
	"idiom/internal/recoverstatus"
	"idiom/internal/recovery"
	"idiom/internal/workspace"
)

func TestSlashRecoverStatusPacketFlagAppendsOperatorStatusWithoutReplay(t *testing.T) {
	root := t.TempDir()
	packet := appRecoverStatusTestPacket()
	if _, _, err := recovery.WriteDecompositionPacket(root, packet, "packet.json"); err != nil {
		t.Fatalf("WriteDecompositionPacket: %v", err)
	}
	model := &Model{
		ready:     true,
		viewport:  viewport.New(80, 20),
		workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
	}

	quit := model.handleSlashCommand("/recover-status --packet packet.json")
	if quit {
		t.Fatal("recover status command must not quit the TUI")
	}
	if len(model.messages) != 1 {
		t.Fatalf("expected one recovery status transcript message, got %#v", model.messages)
	}
	if model.messages[0].Role != "operator" {
		t.Fatalf("recovery status output must be operator-authored, got role %q", model.messages[0].Role)
	}
	report, err := recoverstatus.Build(root, "packet.json")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	expected := strings.TrimRight(recoverstatus.FormatText(report), "\n")
	if model.messages[0].Content != expected {
		t.Fatalf("slash recover status must render the shared formatter.\nwant:\n%s\n\ngot:\n%s", expected, model.messages[0].Content)
	}
	for _, want := range []string{
		"recover status: ready",
		"packet: packet.json",
		"failure_id: failure-app-recover-status",
		"next_valid_action: review_decomposition",
		"- recovery-001: App recover status",
	} {
		if !strings.Contains(model.messages[0].Content, want) {
			t.Fatalf("expected recover status output to contain %q, got:\n%s", want, model.messages[0].Content)
		}
	}
	for _, forbidden := range []string{
		"recover promote",
		"recover reconcile",
		"recovery_decompose",
	} {
		if strings.Contains(model.messages[0].Content, forbidden) {
			t.Fatalf("slash recover status must not advertise write-capable recovery action %q:\n%s", forbidden, model.messages[0].Content)
		}
	}
	for _, msg := range model.requestMessages() {
		if strings.Contains(msg.Content, "recover status:") {
			t.Fatalf("operator recovery status output must not be replayed into model context: %#v", model.requestMessages())
		}
	}
	if !strings.Contains(model.status, "Recovery status loaded") {
		t.Fatalf("expected status line to report loaded status, got %q", model.status)
	}
}

func TestSlashRecoverStatusBarePacketPath(t *testing.T) {
	root := t.TempDir()
	if _, _, err := recovery.WriteDecompositionPacket(root, appRecoverStatusTestPacket(), "packet.json"); err != nil {
		t.Fatalf("WriteDecompositionPacket: %v", err)
	}
	model := &Model{
		workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
	}

	model.handleSlashCommand("/recover-status packet.json")
	if len(model.messages) != 1 {
		t.Fatalf("expected one operator message for bare packet path, got %#v", model.messages)
	}
	if !strings.Contains(model.messages[0].Content, "recover status: ready") {
		t.Fatalf("expected recovery status text, got:\n%s", model.messages[0].Content)
	}
}

func TestSlashRecoverStatusResolvesRelativePacketFromWorkspaceCWD(t *testing.T) {
	root := t.TempDir()
	subdir := filepath.Join(root, "nested")
	if _, _, err := recovery.WriteDecompositionPacket(root, appRecoverStatusTestPacket(), "nested/packet.json"); err != nil {
		t.Fatalf("WriteDecompositionPacket: %v", err)
	}
	model := &Model{
		workspace: workspace.NewManager(config.Workspace{Root: root, CWD: subdir}),
	}

	model.handleSlashCommand("/recover-status packet.json")
	if len(model.messages) != 1 {
		t.Fatalf("expected one operator message for CWD-relative packet path, got %#v", model.messages)
	}
	if !strings.Contains(model.messages[0].Content, "packet: nested/packet.json") {
		t.Fatalf("expected CWD-relative packet to display as workspace-relative path, got:\n%s", model.messages[0].Content)
	}
}

func TestSlashRecoverStatusUsageErrorIsOperatorAuthored(t *testing.T) {
	root := t.TempDir()
	model := &Model{
		workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
	}

	quit := model.handleSlashCommand("/recover-status --packet")
	if quit {
		t.Fatal("recover status usage error must not quit the TUI")
	}
	if len(model.messages) != 1 {
		t.Fatalf("expected one operator usage message, got %#v", model.messages)
	}
	if model.messages[0].Role != "operator" {
		t.Fatalf("usage error must be operator-authored, got %q", model.messages[0].Role)
	}
	if model.messages[0].Content != recoverStatusUsage {
		t.Fatalf("expected concise usage, got %q", model.messages[0].Content)
	}
	for _, msg := range model.requestMessages() {
		if strings.Contains(msg.Content, "/recover-status") {
			t.Fatalf("operator usage output must not be replayed into model context: %#v", model.requestMessages())
		}
	}
}

func TestSlashHelpDocumentsRecoverStatusBarePacketPath(t *testing.T) {
	model := &Model{}

	model.slashHelp("/help", []string{"/help"})
	if !strings.Contains(model.status, "/recover-status [--packet <path>|<path>]") {
		t.Fatalf("help must document both recover status path forms, got %q", model.status)
	}
	if !strings.Contains(model.status, "/plan-execute <objective>") {
		t.Fatalf("help must document typed plan-execute command, got %q", model.status)
	}
	if !strings.Contains(model.status, "/feature-planner <objective>") {
		t.Fatalf("help must document typed feature-planner command, got %q", model.status)
	}
}

func TestFeaturePlannerSlashCommandRequiresObjective(t *testing.T) {
	model := &Model{}

	if !model.handleFeaturePlannerCommand("/feature-planner") {
		t.Fatal("expected feature-planner command to be handled")
	}
	if model.status != "Usage: /feature-planner <objective>" {
		t.Fatalf("status = %q", model.status)
	}
}

func TestProductSlashCommandsStaySeparateFromFeaturePlanner(t *testing.T) {
	model := &Model{}

	if model.handleFeaturePlannerCommand("/product-create --effort-root docs/product-workflows/efforts/started/test") {
		t.Fatal("product-create must not be consumed by the feature-planner command handler")
	}
	if model.handleFeaturePlannerCommand("/product-approve --effort-root docs/product-workflows/efforts/started/test") {
		t.Fatal("product-approve must not be consumed by the feature-planner command handler")
	}
	for _, command := range []string{"/product-create", "/product-approve"} {
		if _, ok := slashCommands[command]; !ok {
			t.Fatalf("%s must remain an explicit product slash command", command)
		}
	}
	if _, ok := slashCommands["/feature-planner"]; ok {
		t.Fatal("/feature-planner should remain on the direct feature-planner handler, not the generic product slash map")
	}
}

func TestFeaturePlannerTUIPlanExecuteCompletionPreservesNestedDegradedStatus(t *testing.T) {
	model := &Model{}
	payload, err := model.encodeFeaturePlannerPlanExecuteCompletion(strings.Join([]string{
		`{"type":"idiom.plan_execute.review_repair_degraded","status":"review_unavailable","reason_class":"review_model_error"}`,
		`{"type":"message","content":"review unavailable"}`,
	}, "\n"))
	if err != nil {
		t.Fatalf("encodeFeaturePlannerPlanExecuteCompletion: %v", err)
	}
	for _, want := range []string{
		`"status":"degraded"`,
		`"review_status":"review_unavailable"`,
		`"failure_kind":"review_model_error"`,
		`"completion_source":"tui_plan_execute_returned"`,
	} {
		if !strings.Contains(payload, want) {
			t.Fatalf("expected %s in payload %s", want, payload)
		}
	}
}

func TestFeaturePlannerTUIPlanExecuteCompletionRequiresTypedNestedStatus(t *testing.T) {
	root := t.TempDir()
	state := planexecute.NewProgressState("nested_missing_status", "prompt", "", nil, nil, time.Now().UTC())
	state.Phase = "planner"
	state.Status = ""
	statePath, err := planexecute.SaveProgressState(root, &state)
	if err != nil {
		t.Fatalf("SaveProgressState: %v", err)
	}
	model := &Model{
		cfg: config.Config{Workspace: config.Workspace{Root: root}},
		lastPlanExecuteRunResult: &planexecute.RunResult{
			ExitCode:        0,
			SessionID:       state.SessionID,
			ResumeStatePath: statePath,
		},
	}

	payload, err := model.encodeFeaturePlannerPlanExecuteCompletion("plain nested stdout without typed completion")
	if err != nil {
		t.Fatalf("encodeFeaturePlannerPlanExecuteCompletion: %v", err)
	}
	for _, want := range []string{
		`"status":"ambiguous"`,
		`"failure_kind":"missing_nested_plan_execute_status"`,
		`"progress_path":"` + statePath + `"`,
	} {
		if !strings.Contains(payload, want) {
			t.Fatalf("expected %s in payload %s", want, payload)
		}
	}
	if strings.Contains(payload, `"status":"completed"`) {
		t.Fatalf("missing nested status must not default to completed: %s", payload)
	}
}

func TestFeaturePlannerTUIPlanExecuteCompletionEncodesNonzeroRunResult(t *testing.T) {
	root := t.TempDir()
	state := planexecute.NewProgressState("nested_model_error", "prompt", "", nil, nil, time.Now().UTC())
	state.Phase = "review_repair"
	state.Status = "failed"
	state.FailureKind = "review_model_error"
	state.FailureReason = "model returned malformed review"
	state.Review.Status = "review_model_error"
	statePath, err := planexecute.SaveProgressState(root, &state)
	if err != nil {
		t.Fatalf("SaveProgressState: %v", err)
	}
	model := &Model{
		cfg: config.Config{Workspace: config.Workspace{Root: root}},
		lastPlanExecuteRunResult: &planexecute.RunResult{
			ExitCode:        1,
			SessionID:       state.SessionID,
			ResumeStatePath: statePath,
			FailureKind:     "model_error",
			FailureReason:   "plan-execute exited nonzero",
		},
	}

	payload, err := model.encodeFeaturePlannerPlanExecuteCompletion("plan-execute exited 1")
	if err != nil {
		t.Fatalf("encodeFeaturePlannerPlanExecuteCompletion: %v", err)
	}
	for _, want := range []string{
		`"tool":"plan_execute_completed"`,
		`"status":"failed"`,
		`"phase":"review_repair"`,
		`"review_status":"review_model_error"`,
		`"failure_kind":"model_error"`,
		`"failure_reason":"plan-execute exited nonzero"`,
		`"progress_path":"` + statePath + `"`,
	} {
		if !strings.Contains(payload, want) {
			t.Fatalf("expected %s in payload %s", want, payload)
		}
	}
}

func TestFeaturePlannerTUIPlanExecuteCompletionUsesDurableProgressStateForSparseOutput(t *testing.T) {
	root := t.TempDir()
	state := planexecute.NewProgressState("nested_planner_blocked", "prompt", "", []string{"pkg/feature.go"}, []string{"pkg/feature_test.go"}, time.Now().UTC())
	state.Phase = planexecute.ProgressPhasePlanning
	state.Status = planexecute.ProgressStatusFailed
	state.FailureKind = planexecute.RunFailurePlannerBlocked
	state.FailureReason = "<<QUESTION>>: pkg/feature.go source evidence unavailable"
	state.ChangedPaths = []string{"pkg/feature.go"}
	statePath, err := planexecute.SaveProgressState(root, &state)
	if err != nil {
		t.Fatalf("SaveProgressState: %v", err)
	}
	model := &Model{
		cfg: config.Config{Workspace: config.Workspace{Root: root}},
		lastPlanExecuteRunResult: &planexecute.RunResult{
			ExitCode:        1,
			SessionID:       state.SessionID,
			ResumeStatePath: statePath,
		},
	}

	payload, err := model.encodeFeaturePlannerPlanExecuteCompletion("plan-execute returned without terminal JSONL")
	if err != nil {
		t.Fatalf("encodeFeaturePlannerPlanExecuteCompletion: %v", err)
	}
	var fields map[string]any
	if err := json.Unmarshal([]byte(payload), &fields); err != nil {
		t.Fatalf("decode completion payload: %v", err)
	}
	for key, want := range map[string]string{
		"status":         "failed",
		"phase":          "planning",
		"failure_kind":   "planner_blocked",
		"failure_reason": "<<QUESTION>>: pkg/feature.go source evidence unavailable",
		"progress_path":  statePath,
	} {
		if got := fmt.Sprint(fields[key]); got != want {
			t.Fatalf("%s = %q, want %q in payload %s", key, got, want, payload)
		}
	}
	for key, want := range map[string]string{
		"required_edit_paths": "pkg/feature.go",
		"allowed_edit_paths":  "pkg/feature_test.go",
		"changed_paths":       "pkg/feature.go",
	} {
		values, _ := fields[key].([]any)
		if len(values) != 1 || fmt.Sprint(values[0]) != want {
			t.Fatalf("%s = %#v, want [%s] in payload %s", key, fields[key], want, payload)
		}
	}
}

func TestFeaturePlannerTUIPlanExecuteCompletionUsesDurableSkippedReviewState(t *testing.T) {
	root := t.TempDir()
	state := planexecute.NewProgressState("nested_review_skipped", "prompt", "", nil, nil, time.Now().UTC())
	state.Phase = planexecute.ProgressPhaseComplete
	state.Status = planexecute.ProgressStatusCompleted
	state.Review.Status = planexecute.ProgressStatusSkipped
	statePath, err := planexecute.SaveProgressState(root, &state)
	if err != nil {
		t.Fatalf("SaveProgressState: %v", err)
	}
	model := &Model{
		cfg: config.Config{Workspace: config.Workspace{Root: root}},
		lastPlanExecuteRunResult: &planexecute.RunResult{
			ExitCode:        0,
			SessionID:       state.SessionID,
			ResumeStatePath: statePath,
		},
	}

	payload, err := model.encodeFeaturePlannerPlanExecuteCompletion("plan-execute returned without terminal JSONL")
	if err != nil {
		t.Fatalf("encodeFeaturePlannerPlanExecuteCompletion: %v", err)
	}
	var fields map[string]any
	if err := json.Unmarshal([]byte(payload), &fields); err != nil {
		t.Fatalf("decode completion payload: %v", err)
	}
	for key, want := range map[string]string{
		"status":        "completed",
		"phase":         "complete",
		"review_status": "skipped",
		"progress_path": statePath,
	} {
		if got := fmt.Sprint(fields[key]); got != want {
			t.Fatalf("%s = %q, want %q in payload %s", key, got, want, payload)
		}
	}
	if got := fields["review_repair_skipped"]; got != true {
		t.Fatalf("review_repair_skipped = %v, want true in payload %s", got, payload)
	}
	if got := featureplanner.PlanExecuteCompletionTerminalClass(fields); got != featureplanner.PlanExecuteTerminalCompleted {
		t.Fatalf("terminal class = %q, want completed; fields=%+v", got, fields)
	}
}

func TestFeaturePlannerTUIPlanExecuteCompletionLogTypeIsStatusAware(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "completed",
			body: `{"tool":"plan_execute_completed","lane_family":"plan-execute","status":"completed"}`,
			want: "idiom.plan_execute.completed",
		},
		{
			name: "ambiguous",
			body: `{"tool":"plan_execute_completed","lane_family":"plan-execute","status":"ambiguous","failure_kind":"missing_nested_plan_execute_status"}`,
			want: "idiom.plan_execute.blocked",
		},
		{
			name: "failed",
			body: `{"tool":"plan_execute_completed","lane_family":"plan-execute","status":"failed"}`,
			want: "idiom.plan_execute.blocked",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := featurePlannerPlanExecuteCompletionLogType(tt.body); got != tt.want {
				t.Fatalf("featurePlannerPlanExecuteCompletionLogType = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFeaturePlannerPlanExecuteGitHistoryRequirementPayload(t *testing.T) {
	payload := featurePlannerPlanExecuteGitHistoryRequirementPayload()
	var fields map[string]any
	if err := json.Unmarshal([]byte(payload), &fields); err != nil {
		t.Fatalf("payload is not json: %v", err)
	}
	if fields["tool"] != "git_history_requirement" {
		t.Fatalf("tool = %#v", fields["tool"])
	}
	if fields["lane_family"] != "plan-execute" {
		t.Fatalf("lane_family = %#v", fields["lane_family"])
	}
	if fields["commit_descriptions_required"] != true {
		t.Fatalf("commit_descriptions_required = %#v", fields["commit_descriptions_required"])
	}
	if strings.Contains(payload, "prompt") || strings.Contains(payload, "summary") {
		t.Fatalf("git-history requirement payload must stay redacted: %s", payload)
	}
	if featurePlannerPlanExecuteGitHistoryRequirementRole != "idiom.feature_planner.plan_execute_git_history_requirement" {
		t.Fatalf("unexpected role %q", featurePlannerPlanExecuteGitHistoryRequirementRole)
	}
}

func TestFeaturePlannerTUITerminalRoleReflectsStatus(t *testing.T) {
	cases := map[string]string{
		featureplanner.FeatureStateComplete:         "idiom.feature_planner.complete",
		featureplanner.FeatureStateRecoveryOpen:     "idiom.feature_planner.recovery_open",
		featureplanner.FeatureStatePlanningQuestion: "idiom.feature_planner.question_open",
		featureplanner.FeatureStatePlanningStuck:    "idiom.feature_planner.planning_stuck",
		featureplanner.FeatureStateFailed:           "idiom.feature_planner.failed",
		featureplanner.FeatureStateDispatching:      "idiom.feature_planner.terminal",
	}
	for status, want := range cases {
		if got := featurePlannerTUITerminalRole(status); got != want {
			t.Fatalf("featurePlannerTUITerminalRole(%q) = %q, want %q", status, got, want)
		}
	}
}

func TestSlashRecoverStatusLoadErrorUsesSharedPresenter(t *testing.T) {
	root := t.TempDir()
	model := &Model{
		workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
	}

	model.handleSlashCommand("/recover-status missing.json")
	if len(model.messages) != 1 {
		t.Fatalf("expected one operator error message, got %#v", model.messages)
	}
	for _, want := range []string{
		"recovery decomposition packet not found:",
		"next_valid_action: inspect_failure",
	} {
		if !strings.Contains(model.messages[0].Content, want) {
			t.Fatalf("expected shared error presenter to contain %q, got:\n%s", want, model.messages[0].Content)
		}
	}
}

func TestSlashRecoverStatusRejectsOutsidePacketPath(t *testing.T) {
	root := t.TempDir()
	outsideRoot := t.TempDir()
	outsidePacket := filepath.Join(outsideRoot, "packet.json")
	if _, _, err := recovery.WriteDecompositionPacket(outsideRoot, appRecoverStatusTestPacket(), "packet.json"); err != nil {
		t.Fatalf("WriteDecompositionPacket: %v", err)
	}
	model := &Model{
		workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
	}

	model.handleSlashCommand("/recover-status --packet " + outsidePacket)
	if len(model.messages) != 1 {
		t.Fatalf("expected one operator error message, got %#v", model.messages)
	}
	if !strings.Contains(model.messages[0].Content, "workspace root") ||
		!strings.Contains(model.messages[0].Content, "next_valid_action: inspect_failure") {
		t.Fatalf("expected outside packet rejection through shared presenter, got:\n%s", model.messages[0].Content)
	}
}

func appRecoverStatusTestPacket() recovery.DecompositionPacket {
	stamp := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	return recovery.DecompositionPacket{
		SchemaVersion: recovery.DecompositionPacketSchemaVersion,
		PacketID:      "packet-failure-app-recover-status",
		FailureID:     "failure-app-recover-status",
		CreatedAt:     stamp,
		UpdatedAt:     stamp,
		PartialState: recovery.PartialState{
			DirtyStatus: recovery.DirtyStatusClean,
			ScopeStatus: recovery.ScopeStatusInScope,
		},
		Failure: recovery.Failure{Class: recovery.FailureClassExecutorEdit},
		ResidualWork: []recovery.ResidualWork{{
			ID:     "residual-001",
			Title:  "App recover status",
			Reason: recovery.FailureClassExecutorEdit,
			Paths:  []string{"internal/app/recover_status.go"},
			EvidenceRefs: []recovery.EvidenceRef{
				{Kind: "required_path", Path: "internal/app/recover_status.go"},
				{Kind: "required_test", ID: "go test ./internal/app"},
			},
		}},
		CandidateAtoms: []recovery.CandidateAtom{{
			ID:              "recovery-001",
			Title:           "App recover status",
			RequiredPaths:   []string{"internal/app/recover_status.go"},
			RequiredTests:   []string{"go test ./internal/app"},
			Gates:           []string{recovery.GateWorkspaceState, recovery.GateBriefLint},
			ResidualWorkIDs: []string{"residual-001"},
		}},
		NextAction: recovery.NextActionReviewDecomposition,
	}
}
