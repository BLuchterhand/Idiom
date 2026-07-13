package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	recoverycontract "idiom/internal/contracts/recovery"

	"github.com/charmbracelet/bubbles/viewport"
	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"

	"idiom/internal/capabilities/workspace"
	featureplannercontract "idiom/internal/contracts/featureplanner"
	lanecontract "idiom/internal/contracts/lane"
	plancontract "idiom/internal/contracts/plan"
	"idiom/internal/platform/config"
	"idiom/internal/workflows/featureplanner"
	"idiom/internal/workflows/planexecute"
	"idiom/internal/workflows/recoverstatus"
	"idiom/internal/workflows/recovery"
)

func recoverStatusRuntimeWithPlanResult(result *planexecute.RunResult) promptRuntimeState {
	return promptRuntimeState{promptRuntimeCompletionState: promptRuntimeCompletionState{
		lastPlanExecuteRunResult: result,
	}}
}

var _ = ginkgo.It("slash recover status packet flag appends operator status without replay", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	packet := appRecoverStatusTestPacket()

	{
		_, _, err := recovery.WriteDecompositionPacket(root, packet, "packet.json")
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("WriteDecompositionPacket: %v", err))
	}

	model := &Model{
		ready:     true,
		viewport:  viewport.New(80, 20),
		workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
	}

	quit := model.handleSlashCommand("/recover-status --packet packet.json")
	gomega.Expect(quit).To(gomega.BeFalse(), fmt.Sprint("recover status command must not quit the TUI"))

	gomega.Expect(len(model.messages)).To(gomega.Equal(1), fmt.Sprintf("expected one recovery status transcript message, got %#v", model.messages))

	gomega.Expect(model.messages[0].Role).To(gomega.Equal("operator"), fmt.Sprintf("recovery status output must be operator-authored, got role %q", model.messages[0].Role))

	report, err := recoverstatus.Build(root, "packet.json")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("Build: %v", err))

	expected := strings.TrimRight(recoverstatus.FormatText(report), "\n")
	gomega.Expect(model.messages[0].Content).To(gomega.Equal(expected), fmt.Sprintf("slash recover status must render the shared formatter.\nwant:\n%s\n\ngot:\n%s", expected, model.messages[0].Content))

	for _, want := range []string{
		"recover status: ready",
		"packet: packet.json",
		"failure_id: failure-app-recover-status",
		"next_valid_action: review_decomposition",
		"- recovery-001: App recover status",
	} {
		gomega.Expect(strings.Contains(model.messages[0].Content, want)).To(gomega.BeTrue(), fmt.Sprintf("expected recover status output to contain %q, got:\n%s", want, model.messages[0].Content))
	}

	for _, forbidden := range []string{
		"recover promote",
		"recover reconcile",
		"recovery_decompose",
	} {
		gomega.Expect(strings.Contains(model.messages[0].Content, forbidden)).To(gomega.BeFalse(), fmt.Sprintf("slash recover status must not advertise write-capable recovery action %q:\n%s", forbidden, model.messages[0].Content))
	}

	for _, msg := range newDetachedPromptExecutionSession(context.Background(), model).requestMessages(context.Background()) {
		gomega.Expect(strings.Contains(msg.Content, "recover status:")).To(gomega.BeFalse(), fmt.Sprintf("operator recovery status output must not be replayed into model context: %#v", newDetachedPromptExecutionSession(context.Background(), model).requestMessages(context.Background())))
	}

	gomega.Expect(strings.Contains(model.status, "Recovery status loaded")).To(gomega.BeTrue(), fmt.Sprintf("expected status line to report loaded status, got %q", model.status))
})

var _ = ginkgo.It("slash recover status bare packet path", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	{
		_, _, err := recovery.WriteDecompositionPacket(root, appRecoverStatusTestPacket(), "packet.json")
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("WriteDecompositionPacket: %v", err))
	}

	model := &Model{
		workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
	}

	model.handleSlashCommand("/recover-status packet.json")

	gomega.Expect(len(model.messages)).To(gomega.Equal(1), fmt.Sprintf("expected one operator message for bare packet path, got %#v", model.messages))

	gomega.Expect(strings.Contains(model.messages[0].Content, "recover status: ready")).To(gomega.BeTrue(), fmt.Sprintf("expected recovery status text, got:\n%s", model.messages[0].Content))
})

var _ = ginkgo.It("slash recover status resolves relative packet from workspace cwd", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	subdir := filepath.Join(root, "nested")

	{
		_, _, err := recovery.WriteDecompositionPacket(root, appRecoverStatusTestPacket(), "nested/packet.json")
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("WriteDecompositionPacket: %v", err))
	}

	model := &Model{
		workspace: workspace.NewManager(config.Workspace{Root: root, CWD: subdir}),
	}

	model.handleSlashCommand("/recover-status packet.json")

	gomega.Expect(len(model.messages)).To(gomega.Equal(1), fmt.Sprintf("expected one operator message for CWD-relative packet path, got %#v", model.messages))

	gomega.Expect(strings.Contains(model.messages[0].Content, "packet: nested/packet.json")).To(gomega.BeTrue(), fmt.Sprintf("expected CWD-relative packet to display as workspace-relative path, got:\n%s", model.messages[0].Content))
})

var _ = ginkgo.It("slash recover status usage error is operator authored", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	model := &Model{
		workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
	}

	quit := model.handleSlashCommand("/recover-status --packet")
	gomega.Expect(quit).To(gomega.BeFalse(), fmt.Sprint("recover status usage error must not quit the TUI"))

	gomega.Expect(len(model.messages)).To(gomega.Equal(1), fmt.Sprintf("expected one operator usage message, got %#v", model.messages))

	gomega.Expect(model.messages[0].Role).To(gomega.Equal("operator"), fmt.Sprintf("usage error must be operator-authored, got %q", model.messages[0].Role))

	gomega.Expect(model.messages[0].Content).To(gomega.Equal(recoverStatusUsage), fmt.Sprintf("expected concise usage, got %q", model.messages[0].Content))

	for _, msg := range newDetachedPromptExecutionSession(context.Background(), model).requestMessages(context.Background()) {
		gomega.Expect(strings.Contains(msg.Content, "/recover-status")).To(gomega.BeFalse(), fmt.Sprintf("operator usage output must not be replayed into model context: %#v", newDetachedPromptExecutionSession(context.Background(), model).requestMessages(context.Background())))
	}
})

var _ = ginkgo.It("slash help documents recover status bare packet path", func() {
	model := &Model{}

	model.slashHelp("/help", []string{"/help"})

	gomega.Expect(strings.Contains(model.status, "/recover-status [--packet <path>|<path>]")).To(gomega.BeTrue(), fmt.Sprintf("help must document both recover status path forms, got %q", model.status))

	gomega.Expect(strings.Contains(model.status, "/plan-execute <objective>")).To(gomega.BeTrue(), fmt.Sprintf("help must document typed plan-execute command, got %q", model.status))

	gomega.Expect(strings.Contains(model.status, "/feature-planner <objective>")).To(gomega.BeTrue(), fmt.Sprintf("help must document typed feature-planner command, got %q", model.status))
})

var _ = ginkgo.It("feature planner slash command requires objective", func() {
	model := &Model{}

	{
		quit := model.handleSlashCommand("/feature-planner")
		gomega.Expect(quit).To(gomega.BeFalse(), fmt.Sprint("feature-planner usage error must not quit the TUI"))
	}

	gomega.Expect(model.status).To(gomega.Equal("Usage: /feature-planner <objective>"), fmt.Sprintf("status = %q", model.status))
})

var _ = ginkgo.It("product and feature planner slash commands have explicit specs", func() {
	for _, command := range []string{"/feature-planner", "/product-create", "/product-approve"} {
		spec, ok := slashCommandSpecFor(command)
		gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("%s must have an explicit slash-command spec", command))
		gomega.Expect(spec.handler).To(gomega.Not(gomega.BeNil()), fmt.Sprintf("%s must have an explicit slash-command spec", command))
	}
})

var _ = ginkgo.It("feature planner tui plan execute completion preserves nested degraded status", func() {
	model := &Model{}

	completionSummary := newDetachedPromptExecutionSession(context.Background(), model).featurePlannerPlanExecuteCompletionSummary(strings.Join([]string{
		`{"type":"idiom.plan_execute.review_repair_degraded","status":"degraded","reason_class":"review_model_error","status_detail":"review unavailable"}`,
		`{"type":"message","content":"review unavailable"}`,
	}, "\n"))
	payload, err := encodeFeaturePlannerPlanExecuteCompletionSummary(completionSummary)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("encodeFeaturePlannerPlanExecuteCompletion: %v", err))

	for _, want := range []string{
		`"status":"degraded"`,
		`"review_status":"degraded"`,
		`"failure_kind":"review_model_error"`,
		`"completion_source":"tui_plan_execute_returned"`,
	} {
		gomega.Expect(strings.Contains(payload, want)).To(gomega.BeTrue(), fmt.Sprintf("expected %s in payload %s", want, payload))
	}
})

var _ = ginkgo.It("feature planner tui plan execute completion fails closed when typed nested status is absent", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	state := planexecute.NewProgressState("nested_missing_status", "prompt", "", nil, nil, time.Now().UTC())

	statePath, err := planexecute.SaveProgressState(root, &state)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("SaveProgressState: %v", err))
	rawState := fmt.Sprintf(`{"schema_version":%q,"session_id":%q,"phase":"planning","status":""}`+"\n", planexecute.ProgressStateSchemaVersion, state.SessionID)
	{
		err := os.WriteFile(filepath.Join(root, filepath.FromSlash(statePath)), []byte(rawState), 0o600)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write invalid progress state: %v", err))
	}

	model := &Model{
		cfg: config.Config{Workspace: config.Workspace{Root: root}},
		promptRuntimeState: recoverStatusRuntimeWithPlanResult(&planexecute.RunResult{
			ExitCode:        0,
			SessionID:       state.SessionID,
			ResumeStatePath: statePath,
		}),
	}

	completionSummary := newDetachedPromptExecutionSession(context.Background(), model).featurePlannerPlanExecuteCompletionSummary("plain nested stdout without typed completion")
	payload, err := encodeFeaturePlannerPlanExecuteCompletionSummary(completionSummary)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("encodeFeaturePlannerPlanExecuteCompletion: %v", err))

	for _, want := range []string{
		`"status":"failed"`,
		`"failure_kind":"terminal_completion_contract_failed"`,
		`invalid plan-execute progress status`,
		`"progress_path":"` + statePath + `"`,
	} {
		gomega.Expect(strings.Contains(payload, want)).To(gomega.BeTrue(), fmt.Sprintf("expected %s in payload %s", want, payload))
	}

	gomega.Expect(strings.Contains(payload, `"status":"completed"`)).To(gomega.BeFalse(), fmt.Sprintf("missing nested status must not default to completed: %s", payload))
})

var _ = ginkgo.It("feature planner tui plan execute completion encodes nonzero run result", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	state := planexecute.NewProgressState("nested_model_error", "prompt", "", nil, nil, time.Now().UTC())

	state.Phase = "review_repair"
	state.Status = "failed"
	state.FailureKind = "review_model_error"
	state.FailureReason = "model returned malformed review"
	state.Review.Status = plancontract.PlanExecuteReviewStatusFailed

	statePath, err := planexecute.SaveProgressState(root, &state)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("SaveProgressState: %v", err))

	model := &Model{
		cfg: config.Config{Workspace: config.Workspace{Root: root}},
		promptRuntimeState: recoverStatusRuntimeWithPlanResult(&planexecute.RunResult{
			ExitCode:        1,
			SessionID:       state.SessionID,
			ResumeStatePath: statePath,
			FailureKind:     "model_error",
			FailureReason:   "plan-execute exited nonzero",
		}),
	}

	completionSummary := newDetachedPromptExecutionSession(context.Background(), model).featurePlannerPlanExecuteCompletionSummary("plan-execute exited 1")
	payload, err := encodeFeaturePlannerPlanExecuteCompletionSummary(completionSummary)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("encodeFeaturePlannerPlanExecuteCompletion: %v", err))

	for _, want := range []string{
		`"tool":"plan_execute_completed"`,
		`"status":"failed"`,
		`"phase":"review_repair"`,
		`"review_status":"failed"`,
		`"failure_kind":"review_model_error"`,
		`"failure_reason":"model returned malformed review"`,
		`"progress_path":"` + statePath + `"`,
	} {
		gomega.Expect(strings.Contains(payload, want)).To(gomega.BeTrue(), fmt.Sprintf("expected %s in payload %s", want, payload))
	}
})

var _ = ginkgo.It("feature planner tui plan execute completion uses durable progress state for sparse output", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	state := planexecute.NewProgressState("nested_planner_blocked", "prompt", "", []string{"pkg/feature.go"}, []string{"pkg/feature_test.go"}, time.Now().UTC())

	state.Phase = plancontract.PlanExecuteProgressPhasePlanning
	state.Status = plancontract.PlanExecuteProgressStatusFailed
	state.FailureKind = plancontract.PlanExecuteFailurePlannerBlocked
	state.FailureReason = "<<QUESTION>>: pkg/feature.go source evidence unavailable"
	state.ChangedPaths = []string{"pkg/feature.go"}

	statePath, err := planexecute.SaveProgressState(root, &state)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("SaveProgressState: %v", err))

	model := &Model{
		cfg: config.Config{Workspace: config.Workspace{Root: root}},
		promptRuntimeState: recoverStatusRuntimeWithPlanResult(&planexecute.RunResult{
			ExitCode:        1,
			SessionID:       state.SessionID,
			ResumeStatePath: statePath,
		}),
	}

	completionSummary := newDetachedPromptExecutionSession(context.Background(), model).featurePlannerPlanExecuteCompletionSummary("plan-execute returned without terminal JSONL")
	payload, err := encodeFeaturePlannerPlanExecuteCompletionSummary(completionSummary)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("encodeFeaturePlannerPlanExecuteCompletion: %v", err))

	var fields map[string]any

	{
		err := json.Unmarshal([]byte(payload), &fields)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("decode completion payload: %v", err))
	}

	for key, want := range map[string]string{
		"status":         "failed",
		"phase":          "planning",
		"failure_kind":   "planner_blocked",
		"failure_reason": "<<QUESTION>>: pkg/feature.go source evidence unavailable",
		"progress_path":  statePath,
	} {
		{
			got := fmt.Sprint(fields[key])
			gomega.Expect(got).To(gomega.Equal(want), fmt.Sprintf("%s = %q, want %q in payload %s", key, got, want, payload))
		}
	}

	for key, want := range map[string]string{
		"required_edit_paths": "pkg/feature.go",
		"allowed_edit_paths":  "pkg/feature_test.go",
		"changed_paths":       "pkg/feature.go",
	} {
		values, _ := fields[key].([]any)
		gomega.Expect(len(values)).To(gomega.Equal(1), fmt.Sprintf("%s = %#v, want [%s] in payload %s", key, fields[key], want, payload))
		gomega.Expect(fmt.Sprint(values[0])).To(gomega.Equal(want), fmt.Sprintf("%s = %#v, want [%s] in payload %s", key, fields[key], want, payload))
	}
})

var _ = ginkgo.It("feature planner tui plan execute completion prefers durable review repair progress", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	state := planexecute.NewProgressState("active_review_repair", "prompt", "", []string{"pkg/feature.go"}, nil, time.Now().UTC())

	state.Phase = plancontract.PlanExecuteProgressPhaseReviewRepair
	state.Status = plancontract.PlanExecuteProgressStatusBlocked
	state.FailureKind = "review_repair_blocked"
	state.FailureReason = "review repair remained blocked"
	state.ChangedPaths = []string{"pkg/feature.go"}
	state.Review.Status = plancontract.PlanExecuteReviewStatusBlocked

	statePath, err := planexecute.SaveProgressState(root, &state)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("SaveProgressState: %v", err))

	model := &Model{
		cfg: config.Config{Workspace: config.Workspace{Root: root}},
		promptRuntimeState: recoverStatusRuntimeWithPlanResult(&planexecute.RunResult{
			ExitCode:        1,
			SessionID:       state.SessionID,
			ResumeStatePath: statePath,
		}),
	}
	fallbackOutput := `{"type":"idiom.plan_execute.resume_state_saved","state_path":".idiom/plan-execute/sessions/fallback.json","phase":"planning","status":"failed","failure_kind":"plan_artifact_missing","failure_reason":"planning/no plan artifact"}`

	completionSummary := newDetachedPromptExecutionSession(context.Background(), model).featurePlannerPlanExecuteCompletionSummary(fallbackOutput)
	payload, err := encodeFeaturePlannerPlanExecuteCompletionSummary(completionSummary)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("encodeFeaturePlannerPlanExecuteCompletion: %v", err))

	var fields map[string]any

	{
		err := json.Unmarshal([]byte(payload), &fields)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("decode completion payload: %v", err))
	}

	for key, want := range map[string]string{
		"status":         "blocked",
		"phase":          "review_repair",
		"review_status":  "blocked",
		"failure_kind":   "review_repair_blocked",
		"failure_reason": "review repair remained blocked",
		"progress_path":  statePath,
	} {
		{
			got := fmt.Sprint(fields[key])
			gomega.Expect(got).To(gomega.Equal(want), fmt.Sprintf("%s = %q, want %q in payload %s", key, got, want, payload))
		}
	}

	gomega.Expect(payload).To(gomega.Not(gomega.ContainSubstring("plan_artifact_missing")), fmt.Sprintf("stale planning fallback leaked into payload: %s", payload))
	gomega.Expect(payload).To(gomega.Not(gomega.ContainSubstring("planning/no plan artifact")), fmt.Sprintf("stale planning fallback leaked into payload: %s", payload))

	changed, _ := fields["changed_paths"].([]any)
	gomega.Expect(len(changed)).To(gomega.Equal(1), fmt.Sprintf("changed_paths = %#v, want active changed path in payload %s", fields["changed_paths"], payload))
	gomega.Expect(fmt.Sprint(changed[0])).To(gomega.Equal("pkg/feature.go"), fmt.Sprintf("changed_paths = %#v, want active changed path in payload %s", fields["changed_paths"], payload))
})

var _ = ginkgo.It("feature planner tui plan execute completion uses durable skipped review state", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	state := planexecute.NewProgressState("nested_review_skipped", "prompt", "", nil, nil, time.Now().UTC())

	state.Phase = plancontract.PlanExecuteProgressPhaseComplete
	state.Status = plancontract.PlanExecuteProgressStatusCompleted
	state.ChangedPaths = []string{"pkg/feature.go"}
	state.Executor.SourceSanity = []plancontract.PhaseEvidenceCheck{{
		Kind:   "source_sanity",
		Path:   "pkg/feature.go",
		Status: "ok",
	}}
	state.Review.Status = plancontract.PlanExecuteReviewStatusSkipped

	statePath, err := planexecute.SaveProgressState(root, &state)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("SaveProgressState: %v", err))

	model := &Model{
		cfg: config.Config{Workspace: config.Workspace{Root: root}},
		promptRuntimeState: recoverStatusRuntimeWithPlanResult(&planexecute.RunResult{
			ExitCode:        0,
			SessionID:       state.SessionID,
			ResumeStatePath: statePath,
		}),
	}

	completionSummary := newDetachedPromptExecutionSession(context.Background(), model).featurePlannerPlanExecuteCompletionSummary("plan-execute returned without terminal JSONL")
	payload, err := encodeFeaturePlannerPlanExecuteCompletionSummary(completionSummary)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("encodeFeaturePlannerPlanExecuteCompletion: %v", err))

	var fields map[string]any

	{
		err := json.Unmarshal([]byte(payload), &fields)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("decode completion payload: %v", err))
	}

	for key, want := range map[string]string{
		"status":        "completed",
		"phase":         "complete",
		"review_status": "skipped",
		"progress_path": statePath,
	} {
		{
			got := fmt.Sprint(fields[key])
			gomega.Expect(got).To(gomega.Equal(want), fmt.Sprintf("%s = %q, want %q in payload %s", key, got, want, payload))
		}
	}

	{
		got := fields["review_repair_skipped"]
		gomega.Expect(got).To(gomega.Equal(true), fmt.Sprintf("review_repair_skipped = %v, want true in payload %s", got, payload))
	}

	{
		got := plancontract.DecidePlanExecuteTerminalCompletionFields(fields, nil).Class
		gomega.Expect(got).To(gomega.Equal(plancontract.PlanExecuteTerminalDecisionCompleted), fmt.Sprintf("terminal class = %q, want completed; fields=%+v", got, fields))
	}
})

var _ = ginkgo.It("feature planner tui plan execute completion preserves terminal parent authority", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	state := planexecute.NewProgressState("nested_wrong_parent", "prompt", "", []string{"pkg/feature.go"}, nil, time.Now().UTC())

	state.Phase = plancontract.PlanExecuteProgressPhaseComplete
	state.Status = plancontract.PlanExecuteProgressStatusCompleted
	state.ChangedPaths = []string{"pkg/feature.go"}
	state.Executor.SourceSanity = []plancontract.PhaseEvidenceCheck{{
		Kind:   "source_sanity",
		Path:   "pkg/feature.go",
		Status: "ok",
	}}
	state.Review.Status = plancontract.PlanExecuteReviewStatusSkipped

	record := planexecute.NewPlanExecuteTerminalCompletionRecord(state, planexecute.PlanExecuteTerminalCompletionInput{
		ProgressPath: ".idiom/plan-execute/sessions/nested_wrong_parent.json",
		CompletedAt:  time.Now().UTC(),
	})

	record.ContractID = "wrong-parent-contract"
	record.SnapshotHash = "active-snapshot"
	record.ContractHash = "wrong-legacy-snapshot"

	completionPath, err := planexecute.SavePlanExecuteTerminalCompletionRecord(root, record)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("SavePlanExecuteTerminalCompletionRecord: %v", err))

	model := &Model{
		cfg: config.Config{Workspace: config.Workspace{Root: root}},
		promptRuntimeState: recoverStatusRuntimeWithPlanResult(&planexecute.RunResult{
			ExitCode:       0,
			SessionID:      state.SessionID,
			CompletionPath: completionPath,
		}),
	}

	completionSummary := newDetachedPromptExecutionSession(context.Background(), model).featurePlannerPlanExecuteCompletionSummary("plan-execute returned without terminal JSONL")
	payload, err := encodeFeaturePlannerPlanExecuteCompletionSummary(completionSummary)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("encodeFeaturePlannerPlanExecuteCompletion: %v", err))

	var fields map[string]any

	{
		err := json.Unmarshal([]byte(payload), &fields)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("decode completion payload: %v", err))
	}

	{
		got := fmt.Sprint(fields["contract_id"])
		gomega.Expect(got).To(gomega.Equal("wrong-parent-contract"), fmt.Sprintf("contract_id = %q, want explicit terminal sidecar authority preserved in payload %s", got, payload))
	}

	{
		got := fmt.Sprint(fields["snapshot_hash"])
		gomega.Expect(got).To(gomega.Equal("active-snapshot"), fmt.Sprintf("snapshot_hash = %q, want explicit terminal sidecar authority preserved in payload %s", got, payload))
	}

	{
		got := fmt.Sprint(fields["contract_hash"])
		gomega.Expect(got).To(gomega.Equal("wrong-legacy-snapshot"), fmt.Sprintf("contract_hash = %q, want legacy terminal sidecar authority preserved for conflict validation in payload %s", got, payload))
	}
})

var _ = ginkgo.It("feature planner tui plan execute completion log type is status aware", func() {
	tests := []struct {
		name string
		body string
		want featureplannercontract.NestedPlanExecuteCompletionEventName
	}{
		{
			name: "completed",
			body: `{"tool":"plan_execute_completed","lane_family":"plan-execute","status":"completed"}`,
			want: featureplannercontract.NestedPlanExecuteCompletionEventBlocked,
		},
		{
			name: "ambiguous",
			body: `{"tool":"plan_execute_completed","lane_family":"plan-execute","status":"ambiguous","failure_kind":"missing_nested_plan_execute_status"}`,
			want: featureplannercontract.NestedPlanExecuteCompletionEventBlocked,
		},
		{
			name: "failed",
			body: `{"tool":"plan_execute_completed","lane_family":"plan-execute","status":"failed"}`,
			want: featureplannercontract.NestedPlanExecuteCompletionEventBlocked,
		},
	}
	for _, tt := range tests {
		ginkgo.By(fmt.Sprint(tt.name), func() {
			{
				got := featurePlannerPlanExecuteCompletionLogType(tt.body)
				gomega.Expect(got).To(gomega.Equal(tt.want), fmt.Sprintf("featurePlannerPlanExecuteCompletionLogType = %q, want %q", got, tt.want))
			}
		})
	}
})

var _ = ginkgo.It("feature planner plan execute git history requirement payload", func() {
	payload := featurePlannerPlanExecuteGitHistoryRequirementPayload()

	var fields map[string]any

	{
		err := json.Unmarshal([]byte(payload), &fields)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("payload is not json: %v", err))
	}

	gomega.Expect(fields[featureplannercontract.PlanExecuteCompletionFieldTool]).To(gomega.Equal(string(featureplannercontract.ToolGitHistoryRequirement)), fmt.Sprintf("tool = %#v", fields["tool"]))

	gomega.Expect(fields[featureplannercontract.PlanExecuteCompletionFieldLaneFamily]).To(gomega.Equal(string(lanecontract.LanePlanExecute)), fmt.Sprintf("lane_family = %#v", fields["lane_family"]))

	gomega.Expect(fields["commit_descriptions_required"]).To(gomega.Equal(true), fmt.Sprintf("commit_descriptions_required = %#v", fields["commit_descriptions_required"]))

	gomega.Expect(payload).To(gomega.Not(gomega.ContainSubstring("prompt")), fmt.Sprintf("git-history requirement payload must stay redacted: %s", payload))
	gomega.Expect(payload).To(gomega.Not(gomega.ContainSubstring("summary")), fmt.Sprintf("git-history requirement payload must stay redacted: %s", payload))

	gomega.Expect(featurePlannerPlanExecuteGitHistoryRequirementRole).To(gomega.Equal("idiom.feature_planner.plan_execute_git_history_requirement"), fmt.Sprintf("unexpected role %q", featurePlannerPlanExecuteGitHistoryRequirementRole))
})

var _ = ginkgo.It("feature planner tui terminal role reflects status", func() {
	cases := map[string]string{
		featureplanner.FeatureStateComplete:         "idiom.feature_planner.complete",
		featureplanner.FeatureStateRecoveryOpen:     "idiom.feature_planner.recovery_open",
		featureplanner.FeatureStatePlanningQuestion: "idiom.feature_planner.question_open",
		featureplanner.FeatureStatePlanningStuck:    "idiom.feature_planner.planning_stuck",
		featureplanner.FeatureStateFailed:           "idiom.feature_planner.failed",
		featureplanner.FeatureStateDispatching:      "idiom.feature_planner.terminal",
	}
	for status, want := range cases {
		{
			got := featurePlannerTUITerminalRole(status)
			gomega.Expect(got).To(gomega.Equal(want), fmt.Sprintf("featurePlannerTUITerminalRole(%q) = %q, want %q", status, got, want))
		}
	}
})

var _ = ginkgo.It("slash recover status load error uses shared presenter", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	model := &Model{
		workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
	}

	model.handleSlashCommand("/recover-status missing.json")

	gomega.Expect(len(model.messages)).To(gomega.Equal(1), fmt.Sprintf("expected one operator error message, got %#v", model.messages))

	for _, want := range []string{
		"recovery decomposition packet not found:",
		"next_valid_action: inspect_failure",
	} {
		gomega.Expect(strings.Contains(model.messages[0].Content, want)).To(gomega.BeTrue(), fmt.Sprintf("expected shared error presenter to contain %q, got:\n%s", want, model.messages[0].Content))
	}
})

var _ = ginkgo.It("slash recover status rejects outside packet path", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	outsideRoot := t.TempDir()
	outsidePacket := filepath.Join(outsideRoot, "packet.json")

	{
		_, _, err := recovery.WriteDecompositionPacket(outsideRoot, appRecoverStatusTestPacket(), "packet.json")
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("WriteDecompositionPacket: %v", err))
	}

	model := &Model{
		workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
	}

	model.handleSlashCommand("/recover-status --packet " + outsidePacket)

	gomega.Expect(len(model.messages)).To(gomega.Equal(1), fmt.Sprintf("expected one operator error message, got %#v", model.messages))

	gomega.Expect(model.messages[0].Content).To(gomega.ContainSubstring("workspace root"), fmt.Sprintf("expected outside packet rejection through shared presenter, got:\n%s", model.messages[0].Content))
	gomega.Expect(model.messages[0].Content).To(gomega.ContainSubstring("next_valid_action: inspect_failure"), fmt.Sprintf("expected outside packet rejection through shared presenter, got:\n%s", model.messages[0].Content))
})

func appRecoverStatusTestPacket() recoverycontract.DecompositionPacket {
	stamp := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)

	return recoverycontract.DecompositionPacket{
		SchemaVersion: recoverycontract.DecompositionPacketSchemaVersion,
		PacketID:      "packet-failure-app-recover-status",
		FailureID:     "failure-app-recover-status",
		CreatedAt:     stamp,
		UpdatedAt:     stamp,
		SourceRun: recoverycontract.SourceRun{
			Lane:              string(recoverycontract.SourceLaneImplement),
			SessionID:         "session-app-recover-status",
			RequiredEditPaths: []string{"internal/surfaces/tui/app/recover_status.go"},
		},
		PartialState: recoverycontract.PartialState{
			DirtyStatus: recoverycontract.DirtyStatusClean,
			ScopeStatus: recoverycontract.ScopeStatusInScope,
		},
		Failure: recoverycontract.Failure{
			Class:   recoverycontract.FailureClassExecutorEdit,
			Summary: "recover status fixture failure",
		},
		ResidualWork: []recoverycontract.ResidualWork{{
			ID:     "residual-001",
			Title:  "App recover status",
			Reason: string(recoverycontract.FailureClassExecutorEdit),
			Paths:  []string{"internal/surfaces/tui/app/recover_status.go"},
			EvidenceRefs: []recoverycontract.EvidenceRef{
				{Kind: "required_path", Path: "internal/surfaces/tui/app/recover_status.go"},
				{Kind: "required_test", ID: "go test ./internal/surfaces/tui/app"},
			},
		}},
		CandidateAtoms: []recoverycontract.CandidateAtom{{
			ID:              "recovery-001",
			Title:           "App recover status",
			RequiredPaths:   []string{"internal/surfaces/tui/app/recover_status.go"},
			RequiredTests:   []string{"go test ./internal/surfaces/tui/app"},
			Gates:           []string{recoverycontract.GateWorkspaceState, recoverycontract.GateBriefLint},
			ResidualWorkIDs: []string{"residual-001"},
		}},
		NextAction: recoverycontract.NextActionReviewDecomposition,
	}
}
