package planexecute

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"idiom/internal/agent"
	"idiom/internal/comprehension"
	"idiom/internal/config"
	"idiom/internal/frontier/pathparse"
	"idiom/internal/recovery"
	"idiom/internal/workspace"
)

func TestRunEmitsQueueAdmissionBeforePlanner(t *testing.T) {
	root := t.TempDir()
	t.Setenv("IDIOM_QUEUE_POOL", "qwen-35b")
	t.Setenv("IDIOM_QUEUE_SOCKET", filepath.Join(root, "missing-runner.sock"))

	cfg := config.Default()
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root

	var stdout, stderr bytes.Buffer
	plannerCalled := false
	result := Run(cfg, Options{
		Prompt:     "plan this change",
		JSONEvents: true,
	}, func(_ config.Config, _ string, _ RunOptions, _ io.Writer) (string, error) {
		plannerCalled = true
		if !strings.Contains(stdout.String(), `"type":"idiom.plan_execute.queue_unavailable"`) {
			t.Fatalf("queue admission event should be emitted before planner call, stdout=%s", stdout.String())
		}
		return "no plan", nil
	}, nil, &stdout, &stderr)

	if !plannerCalled {
		t.Fatal("expected planner prompt runner to be called")
	}
	if result.ExitCode != 1 {
		t.Fatalf("Run exit=%d, want missing-artifact failure; stdout=%s stderr=%s", result.ExitCode, stdout.String(), stderr.String())
	}
	if strings.Contains(stdout.String(), "idiom.plan_execute.queue_acquired") {
		t.Fatalf("missing daemon should not be reported as acquired, stdout=%s", stdout.String())
	}
}

func TestClassifyExecutorHandoffTreatsScopedFreshPacketAsFresh(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/planexec\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatalf("seed go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package pkg\n\nfunc A() string { return \"alpha\" }\n"), 0o644); err != nil {
		t.Fatalf("seed a.go: %v", err)
	}
	testPath := filepath.Join(root, "a_test.go")
	if err := os.WriteFile(testPath, []byte("package pkg\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatalf("seed a_test.go: %v", err)
	}
	build, err := comprehension.Build(t.Context(), comprehension.BuildOptions{Root: root, Language: "go"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	key := comprehension.ViewKey{IndexID: build.IndexID, Task: "scoped fresh shared handoff", Mode: "plan"}
	if err := comprehension.SaveView(root, key, comprehension.PackResult{
		PackID:  key.ID(),
		IndexID: build.IndexID,
		Root:    root,
		Task:    "scoped fresh shared handoff",
		Mode:    "plan",
		Fresh:   false,
		Freshness: &comprehension.FreshnessDecision{
			Policy:        comprehension.FreshnessReadThrough,
			Operation:     "context_pack",
			IndexID:       build.IndexID,
			FreshForScope: true,
			GlobalFresh:   false,
		},
		CandidateFiles: []comprehension.PackFile{{Path: "a.go", Role: "editable"}},
		Tests:          []comprehension.PackTest{{Command: "go test ./...", Path: "a_test.go"}},
	}); err != nil {
		t.Fatalf("SaveView: %v", err)
	}
	if err := os.WriteFile(testPath, []byte("package pkg\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) { t.Helper() }\n"), 0o644); err != nil {
		t.Fatalf("make advisory test stale: %v", err)
	}

	handoff := classifyExecutorHandoffPacket(root, workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		PackID:        key.ID(),
	})
	if handoff.State != ExecutorHandoffStateInherited || handoff.PacketFreshnessState != ExecutorPacketFreshnessFresh || !handoff.Fresh {
		t.Fatalf("expected scoped-fresh packet to classify as fresh inherited handoff, got %#v", handoff)
	}
}

func TestPreflightPlanEditsRejectsEscapingPathBeforeRead(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(filepath.Dir(root), "outside.txt")
	if err := os.WriteFile(outside, []byte("secret\n"), 0o644); err != nil {
		t.Fatalf("seed outside file: %v", err)
	}
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{
			{Path: "../outside.txt", OldText: "secret", NewText: "redacted"},
		},
	}

	got := preflightPlanEdits(root, plan)
	if got == nil || got.Reason != "edit has invalid path" {
		t.Fatalf("expected invalid path preflight mismatch, got %#v", got)
	}
}

func TestPreflightPlanEditsRejectsStaleAnchorProvenance(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("alpha\n"), 0o644); err != nil {
		t.Fatalf("seed a.txt: %v", err)
	}
	edits, err := workspace.AttachPlanEditAnchorProvenance(root, []workspace.PlanEdit{
		{Path: "a.txt", OldText: "alpha", NewText: "ALPHA"},
	})
	if err != nil {
		t.Fatalf("attach anchor provenance: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("prefix\nalpha\n"), 0o644); err != nil {
		t.Fatalf("mutate a.txt: %v", err)
	}
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits:         edits,
	}
	got := preflightPlanEdits(root, plan)
	if got == nil {
		t.Fatalf("preflight must reject stale anchor provenance before writes")
	}
	if got.Index != 0 || got.Path != "a.txt" {
		t.Fatalf("mismatch should point at edit 0 in a.txt, got %#v", got)
	}
	if !strings.Contains(got.Reason, "anchor provenance stale") || !strings.Contains(got.Reason, "file fingerprint changed") {
		t.Fatalf("mismatch should report stale provenance, got %q", got.Reason)
	}
}

func TestRunExecutorPhaseAppliesInsertAfterLinePlanEdit(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "feature_test.go"), []byte("package demo\n\nfunc TestExisting(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatalf("seed feature_test.go: %v", err)
	}
	cfg := config.Default()
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true
	writeRunTestPlanViaSubmitPlan(t, cfg, []workspace.PlanEdit{{
		Path:            "feature_test.go",
		InsertAfterLine: 3,
		Content:         "\nfunc TestInserted(t *testing.T) {}\n",
		Purpose:         "insert a regression test after the existing test",
	}}, "insert regression test")
	plan, err := workspace.LoadPlanArtifact(filepath.Join(root, ".idiom", "plans", "last.plan.json"))
	if err != nil {
		t.Fatalf("load submitted plan: %v", err)
	}

	var stdout bytes.Buffer
	report, err := RunExecutorPhase(cfg, plan, []string{"feature_test.go"}, true, &stdout)
	if err != nil {
		t.Fatalf("executor should apply insert_after_line plan edit, report=%#v stdout=%s err=%v", report, stdout.String(), err)
	}
	if report.Succeeded != 1 || report.Failed != 0 {
		t.Fatalf("unexpected executor report: %#v", report)
	}
	got := readFileStringForRunTest(t, filepath.Join(root, "feature_test.go"))
	if !strings.Contains(got, "func TestExisting") || !strings.Contains(got, "func TestInserted") {
		t.Fatalf("insert_after_line edit not applied:\n%s", got)
	}
}

func TestRunAutoExactPlanSourceSatisfiedSkipsPlanner(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("new a\n"), 0o644); err != nil {
		t.Fatalf("seed a.txt: %v", err)
	}
	cfg := config.Default()
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true
	prompt := "## Files To Modify\n\n" +
		"| File | Change |\n" +
		"|---|---|\n" +
		"| `a.txt` | Update a. |\n\n" +
		"## Required Edits\n\n" +
		"In `a.txt`, replace the text.\n\n" +
		"old_text:\n\n```text\nold a\n```\n\n" +
		"new_text:\n\n```text\nnew a\n```\n"
	var stdout, stderr bytes.Buffer
	plannerCalled := false

	result := Run(cfg, Options{
		Prompt:     prompt,
		JSONEvents: true,
	}, func(_ config.Config, _ string, _ RunOptions, _ io.Writer) (string, error) {
		plannerCalled = true
		return "", nil
	}, nil, &stdout, &stderr)

	if result.ExitCode != 0 {
		t.Fatalf("source-satisfied run should pass, got code=%d stdout=%s stderr=%s", result.ExitCode, stdout.String(), stderr.String())
	}
	if plannerCalled {
		t.Fatal("planner should not run when exact edits are already applied")
	}
	if !strings.Contains(stdout.String(), `"type":"idiom.plan_execute.auto_exact_plan_source_satisfied"`) ||
		!strings.Contains(stdout.String(), `"type":"idiom.plan_execute.source_satisfied"`) {
		t.Fatalf("source-satisfied events missing from stdout=%s", stdout.String())
	}
}

func TestRunAcceptsSourceSatisfiedAnswerWithInferredEvidencePaths(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true

	var stdout, stderr bytes.Buffer
	result := Run(cfg, Options{
		Prompt:     "Reject temp symlinks and add regression tests proving no partial mutation.",
		JSONEvents: true,
	}, func(_ config.Config, _ string, _ RunOptions, _ io.Writer) (string, error) {
		return "Decision: source-satisfied\n- internal/productworkflow/migration_apply.go: no changes needed.\n- internal/productworkflow/migration_apply_test.go: no changes needed.", nil
	}, nil, &stdout, &stderr)

	if result.ExitCode != 0 {
		t.Fatalf("source-satisfied answer with path evidence should pass, got code=%d stdout=%s stderr=%s", result.ExitCode, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), `"type":"idiom.plan_execute.source_satisfied"`) {
		t.Fatalf("source-satisfied event missing from stdout=%s", stdout.String())
	}
	events := planExecuteJSONEventsForRunTest(t, stdout.String())
	event, ok := planExecuteEventByTypeForRunTest(events, "idiom.plan_execute.source_satisfied")
	if !ok {
		t.Fatalf("expected source_satisfied event, got %s", stdout.String())
	}
	if got, _ := event["source_satisfied_evidence_paths"].([]any); len(got) != 2 {
		t.Fatalf("expected source-satisfied evidence paths in telemetry, got %#v", event)
	}
}

func TestRunRejectsSourceSatisfiedAnswerWhenRequestedTestEditMissing(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true

	var stdout, stderr bytes.Buffer
	result := Run(cfg, Options{
		Prompt:             "Fix parser behavior and add regression tests.",
		JSONEvents:         true,
		EditSetObligations: EditSetObligations{}.With(EditSetObligationKindTestEdit, "test"),
	}, func(_ config.Config, _ string, _ RunOptions, _ io.Writer) (string, error) {
		return "Decision: source-satisfied\n- pkg/parser.go: parser behavior is already present; no changes needed.", nil
	}, nil, &stdout, &stderr)

	if result.ExitCode != 1 {
		t.Fatalf("expected missing test edit rejection, got code=%d stdout=%s stderr=%s", result.ExitCode, stdout.String(), stderr.String())
	}
	if result.FailureKind != EditSetFailureMissingRequestedTestEdit || !strings.Contains(stderr.String(), EditSetFailureMissingRequestedTestEdit) {
		t.Fatalf("expected %s, result=%#v stderr=%s", EditSetFailureMissingRequestedTestEdit, result, stderr.String())
	}
	events := planExecuteJSONEventsForRunTest(t, stdout.String())
	event, ok := planExecuteEventByTypeForRunTest(events, "idiom.plan_execute.source_satisfied_rejected")
	if !ok {
		t.Fatalf("expected source_satisfied_rejected event, got %s", stdout.String())
	}
	if event["reason"] != EditSetFailureMissingRequestedTestEdit {
		t.Fatalf("expected %s reason, got %#v", EditSetFailureMissingRequestedTestEdit, event)
	}
	if got, _ := event["edit_set_obligations"].([]any); len(got) == 0 {
		t.Fatalf("expected edit_set_obligations metadata, got %#v", event)
	}
	if got, _ := event["source_satisfied_evidence_paths"].([]any); len(got) != 1 || got[0] != "pkg/parser.go" {
		t.Fatalf("expected rejected source-satisfied evidence path, got %#v", event)
	}
	if got, _ := event["missing_edit_set_obligations"].([]any); len(got) == 0 {
		t.Fatalf("expected missing edit-set obligations in rejection telemetry, got %#v", event)
	}
}

func TestRunRejectsSourceSatisfiedAnswerWhenRequestedImplementationEditMissing(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true

	var stdout, stderr bytes.Buffer
	result := Run(cfg, Options{
		Prompt:             "Implement the feature and add tests.",
		JSONEvents:         true,
		EditSetObligations: EditSetObligations{}.With(EditSetObligationKindImplementationEdit, "test"),
	}, func(_ config.Config, _ string, _ RunOptions, _ io.Writer) (string, error) {
		return "Decision: source-satisfied\n- pkg/feature_test.go: feature tests are already present.", nil
	}, nil, &stdout, &stderr)

	if result.ExitCode != 1 {
		t.Fatalf("expected missing implementation edit rejection, got code=%d stdout=%s stderr=%s", result.ExitCode, stdout.String(), stderr.String())
	}
	if result.FailureKind != EditSetFailureMissingRequestedImplementationEdit || !strings.Contains(stderr.String(), EditSetFailureMissingRequestedImplementationEdit) {
		t.Fatalf("expected %s, result=%#v stderr=%s", EditSetFailureMissingRequestedImplementationEdit, result, stderr.String())
	}
	events := planExecuteJSONEventsForRunTest(t, stdout.String())
	event, ok := planExecuteEventByTypeForRunTest(events, "idiom.plan_execute.source_satisfied_rejected")
	if !ok {
		t.Fatalf("expected source_satisfied_rejected event, got %s", stdout.String())
	}
	if event["reason"] != EditSetFailureMissingRequestedImplementationEdit {
		t.Fatalf("expected %s reason, got %#v", EditSetFailureMissingRequestedImplementationEdit, event)
	}
	if got, _ := event["edit_set_obligations"].([]any); len(got) == 0 {
		t.Fatalf("expected edit_set_obligations metadata, got %#v", event)
	}
	if got, _ := event["source_satisfied_evidence_paths"].([]any); len(got) != 1 || got[0] != "pkg/feature_test.go" {
		t.Fatalf("expected rejected source-satisfied evidence path, got %#v", event)
	}
	if got, _ := event["missing_edit_set_obligations"].([]any); len(got) == 0 {
		t.Fatalf("expected missing edit-set obligations in rejection telemetry, got %#v", event)
	}
}

func TestSaveRunProgressStateWritesRecoveryDecompositionPacket(t *testing.T) {
	root := t.TempDir()
	state := NewProgressState("auto_recovery_packet", "make a change", "", []string{"a.txt"}, nil, timeNowForRunTest())
	state.Status = ProgressStatusFailed
	state.FailureKind = "executor_failed"
	state.FailureReason = "executor failed"
	state.ChangedPaths = []string{"a.txt"}
	var stdout bytes.Buffer
	statePath, err := saveRunProgressState(root, &state, true, &stdout, "test_state_saved")
	if err != nil {
		t.Fatalf("saveRunProgressState: %v", err)
	}
	record, _, found, err := recovery.TryLoadPlanExecuteFailureRecord(root, state.SessionID)
	if err != nil || !found {
		t.Fatalf("TryLoadPlanExecuteFailureRecord found=%v err=%v", found, err)
	}
	packetPath, _, err := recovery.DecompositionPacketPath(root, record.FailureID, "")
	if err != nil {
		t.Fatalf("DecompositionPacketPath: %v", err)
	}
	packet, _, found, err := recovery.TryLoadDecompositionPacket(packetPath)
	if err != nil || !found {
		t.Fatalf("TryLoadDecompositionPacket found=%v err=%v", found, err)
	}
	if packet.FailureID != record.FailureID || len(packet.CandidateAtoms) != 1 {
		t.Fatalf("unexpected packet: %#v", packet)
	}
	if !strings.Contains(stdout.String(), `"type":"idiom.plan_execute.recovery_decomposition_packet_written"`) {
		t.Fatalf("packet event missing from stdout=%s statePath=%s", stdout.String(), statePath)
	}
}

func TestRunPlannerBlockedProgressEventCarriesTerminalFailureFields(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true

	var stdout, stderr bytes.Buffer
	result := Run(cfg, Options{
		Prompt:            "Update target.txt.",
		RequiredEditPaths: []string{"target.txt"},
		JSONEvents:        true,
	}, func(_ config.Config, _ string, opts RunOptions, _ io.Writer) (string, error) {
		if opts.TaskFamily != agent.PlanThenExecutePlannerTaskFamily {
			t.Fatalf("unexpected task family %q", opts.TaskFamily)
		}
		return "<<QUESTION>>: target.txt: required source evidence is unavailable.", nil
	}, nil, &stdout, &stderr)

	if result.ExitCode != 1 || result.FailureKind != RunFailurePlannerBlocked {
		t.Fatalf("expected planner_blocked result, got %#v stdout=%s stderr=%s", result, stdout.String(), stderr.String())
	}
	state, _, err := LoadProgressState(root, result.SessionID)
	if err != nil {
		t.Fatalf("load progress state: %v", err)
	}
	if state.Phase != ProgressPhasePlanning || state.Status != ProgressStatusFailed || state.FailureKind != RunFailurePlannerBlocked {
		t.Fatalf("expected structured planning failure state, got %#v", state)
	}
	events := planExecuteJSONEventsForRunTest(t, stdout.String())
	var event map[string]any
	for _, candidate := range events {
		if candidate["type"] == "idiom.plan_execute.resume_state_saved" && candidate["status"] == ProgressStatusFailed {
			event = candidate
		}
	}
	if event == nil {
		t.Fatalf("expected resume_state_saved event, got %s", stdout.String())
	}
	if event["failure_kind"] != RunFailurePlannerBlocked {
		t.Fatalf("resume state event missing failure_kind: %#v", event)
	}
	if got := fmt.Sprint(event["failure_reason"]); !strings.Contains(got, "target.txt") {
		t.Fatalf("resume state event missing concrete failure reason: %#v", event)
	}
	if got, _ := event["required_edit_paths"].([]any); len(got) != 1 || got[0] != "target.txt" {
		t.Fatalf("resume state event missing required paths: %#v", event)
	}
}

func TestSaveProgressStateRedactsDurableFailureText(t *testing.T) {
	root := t.TempDir()
	state := NewProgressState("redacted_progress", "prompt", "", []string{"a.txt"}, nil, timeNowForRunTest())
	state.Status = ProgressStatusFailed
	state.FailureKind = "executor_failed"
	state.FailureReason = "raw_prompt: include customer file\n```text\nfile body with OPENAI_API_KEY=sk-secret\n```"
	state.Executor.Failures = []string{"old_text: customer file body\npassword=secret"}
	if _, err := SaveProgressState(root, &state); err != nil {
		t.Fatalf("SaveProgressState: %v", err)
	}
	loaded, _, err := LoadProgressState(root, state.SessionID)
	if err != nil {
		t.Fatalf("LoadProgressState: %v", err)
	}
	encoded, err := json.Marshal(loaded)
	if err != nil {
		t.Fatalf("marshal loaded progress: %v", err)
	}
	text := string(encoded)
	for _, forbidden := range []string{"raw_prompt", "file body", "OPENAI_API_KEY", "sk-secret", "old_text", "password=secret"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("durable progress state leaked %q: %s", forbidden, text)
		}
	}
	if !strings.Contains(text, "redacted") {
		t.Fatalf("expected redaction marker in progress state: %s", text)
	}
}

func TestRunPersistsProgressAndResumeRunsReviewRepairOnly(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("old\n"), 0o644); err != nil {
		t.Fatalf("seed a.txt: %v", err)
	}
	cfg := config.Default()
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true
	prompt := "Make a.txt say new."

	var firstStdout, firstStderr bytes.Buffer
	first := Run(cfg, Options{
		Prompt:     prompt,
		JSONEvents: true,
	}, func(cfg config.Config, _ string, opts RunOptions, _ io.Writer) (string, error) {
		switch opts.TaskFamily {
		case agent.PlanThenExecutePlannerTaskFamily:
			writeRunTestPlanViaSubmitPlan(t, cfg, []workspace.PlanEdit{
				{Path: "a.txt", OldText: "old\n", NewText: "new\n", Purpose: "apply requested text"},
			}, "change a.txt")
			return "submitted plan", nil
		case agent.PlanThenExecuteReviewRepairTaskFamily:
			return "Decision: pass\nNo checklist.", nil
		default:
			t.Fatalf("unexpected task family %q", opts.TaskFamily)
			return "", nil
		}
	}, nil, &firstStdout, &firstStderr)

	if first.ExitCode != 1 {
		t.Fatalf("first run should fail review checklist gate, got code=%d stdout=%s stderr=%s", first.ExitCode, firstStdout.String(), firstStderr.String())
	}
	if first.ResumeStatePath == "" {
		t.Fatalf("failed run should expose a resume state path")
	}
	state, _, err := LoadProgressState(root, first.SessionID)
	if err != nil {
		t.Fatalf("load progress state: %v", err)
	}
	if state.Phase != ProgressPhaseReviewRepair || state.Status != ProgressStatusFailed {
		t.Fatalf("expected failed review progress, got phase=%s status=%s state=%#v", state.Phase, state.Status, state)
	}
	if state.Executor.Status != ProgressStatusCompleted || state.Executor.Succeeded != 1 {
		t.Fatalf("executor progress should be complete before review resume, got %#v", state.Executor)
	}
	if strings.Contains(readFileStringForRunTest(t, filepath.Join(root, first.ResumeStatePath)), prompt) {
		t.Fatalf("progress state must store prompt hash, not raw prompt")
	}
	record, recordPath, found, err := recovery.TryLoadPlanExecuteFailureRecord(root, first.SessionID)
	if err != nil {
		t.Fatalf("load recovery record: %v", err)
	}
	if !found {
		t.Fatalf("failed run should write recovery record")
	}
	if record.Failure.Class != recovery.FailureClassReviewRepair || record.NextAction != recovery.NextActionReviewDecomposition {
		t.Fatalf("unexpected recovery record at %s: %#v", recordPath, record)
	}

	var resumedStdout, resumedStderr bytes.Buffer
	reviewCalls := 0
	resumedPrompt := ""
	resumed := Run(cfg, Options{
		Prompt: prompt,
		Resume: first.SessionID,
	}, func(_ config.Config, prompt string, opts RunOptions, _ io.Writer) (string, error) {
		if opts.TaskFamily != agent.PlanThenExecuteReviewRepairTaskFamily {
			t.Fatalf("resume must not rerun planner/executor model phase, got task family %q", opts.TaskFamily)
		}
		reviewCalls++
		resumedPrompt = prompt
		return "Decision: pass\n\nVerification Checklist:\n- Required behavior: a.txt changed from old to new.\n- Scope safety: no corrective edit needed.", nil
	}, nil, &resumedStdout, &resumedStderr)

	if resumed.ExitCode != 0 {
		t.Fatalf("resume should pass review, got code=%d stdout=%s stderr=%s", resumed.ExitCode, resumedStdout.String(), resumedStderr.String())
	}
	if reviewCalls != 1 {
		t.Fatalf("resume should call review once, got %d calls", reviewCalls)
	}
	if !strings.Contains(resumedPrompt, "-old") || !strings.Contains(resumedPrompt, "+new") {
		t.Fatalf("resumed review prompt should restore executor baseline diff, got:\n%s", resumedPrompt)
	}
	if got := readFileStringForRunTest(t, filepath.Join(root, "a.txt")); got != "new\n" {
		t.Fatalf("resume must not reapply executor edits, got %q", got)
	}
}

func TestRunResumeReviewRepairUsesPersistedTypedEditSetObligations(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "feature_test.go"), []byte("package demo\n\nfunc TestFeature(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatalf("seed feature_test.go: %v", err)
	}
	cfg := config.Default()
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true
	prompt := "Add coverage for existing feature behavior."

	var firstStdout, firstStderr bytes.Buffer
	first := Run(cfg, Options{
		Prompt:     prompt,
		JSONEvents: true,
	}, func(cfg config.Config, _ string, opts RunOptions, _ io.Writer) (string, error) {
		switch opts.TaskFamily {
		case agent.PlanThenExecutePlannerTaskFamily:
			writeRunTestPlanViaSubmitPlan(t, cfg, []workspace.PlanEdit{
				{Path: "feature_test.go", OldText: "func TestFeature(t *testing.T) {}\n", NewText: "func TestFeature(t *testing.T) {\n\tt.Helper()\n}\n", Purpose: "add coverage detail"},
			}, "test-only coverage plan")
			return "submitted test-only plan", nil
		case agent.PlanThenExecuteReviewRepairTaskFamily:
			return "Decision: pass\nNo checklist.", nil
		default:
			t.Fatalf("unexpected task family %q", opts.TaskFamily)
			return "", nil
		}
	}, nil, &firstStdout, &firstStderr)
	if first.ExitCode != 1 {
		t.Fatalf("first run should fail review checklist gate, got code=%d stdout=%s stderr=%s", first.ExitCode, firstStdout.String(), firstStderr.String())
	}

	state, _, err := LoadProgressState(root, first.SessionID)
	if err != nil {
		t.Fatalf("load progress state: %v", err)
	}
	state.EditSetObligations = EditSetObligations{}.With(EditSetObligationKindImplementationEdit, "persisted_retry")
	if _, err := SaveProgressState(root, &state); err != nil {
		t.Fatalf("save progress state: %v", err)
	}

	var resumedStdout, resumedStderr bytes.Buffer
	resumedPrompt := ""
	resumed := Run(cfg, Options{
		Prompt:     prompt,
		Resume:     first.SessionID,
		JSONEvents: true,
	}, func(_ config.Config, prompt string, opts RunOptions, _ io.Writer) (string, error) {
		if opts.TaskFamily != agent.PlanThenExecuteReviewRepairTaskFamily {
			t.Fatalf("resume must run review-repair only, got %q", opts.TaskFamily)
		}
		resumedPrompt = prompt
		return "Decision: pass\n\nVerification Checklist:\n- Required behavior: test coverage is present.\n- Scope safety: no corrective edit needed.", nil
	}, nil, &resumedStdout, &resumedStderr)

	if resumed.ExitCode != 1 {
		t.Fatalf("resume should reject pass against persisted implementation obligation, got code=%d stdout=%s stderr=%s", resumed.ExitCode, resumedStdout.String(), resumedStderr.String())
	}
	if !strings.Contains(resumedStderr.String(), EditSetFailureMissingRequestedImplementationEdit) {
		t.Fatalf("expected persisted implementation obligation rejection, stderr=%s", resumedStderr.String())
	}
	if !strings.Contains(resumedPrompt, "requested_implementation_edit_missing") {
		t.Fatalf("review prompt should expose persisted implementation obligation, got:\n%s", resumedPrompt)
	}
}

func TestRunContinuesReviewRepairAfterSourceSanityFailure(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/recover-source-sanity\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatalf("seed go.mod: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "pkg"), 0o755); err != nil {
		t.Fatalf("mkdir pkg: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "pkg", "feature.go"), []byte("package pkg\n\nfunc Value() int { return 1 }\n"), 0o644); err != nil {
		t.Fatalf("seed feature.go: %v", err)
	}
	sourcePath := filepath.Join(root, "pkg", "feature.go")
	cfg := config.Default()
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true
	cfg.Workspace.CommandTimeoutSeconds = 30

	var stdout, stderr bytes.Buffer
	reviewCalled := false
	result := Run(cfg, Options{
		Prompt:     "Update Value.",
		JSONEvents: true,
	}, func(cfg config.Config, prompt string, opts RunOptions, _ io.Writer) (string, error) {
		switch opts.TaskFamily {
		case agent.PlanThenExecutePlannerTaskFamily:
			writeRunTestPlanViaSubmitPlan(t, cfg, []workspace.PlanEdit{
				{
					Path:    filepath.ToSlash(filepath.Join("pkg", "feature.go")),
					OldText: "return 1",
					NewText: "return missingSymbol",
					Purpose: "update Value with a compile defect",
				},
			}, "update Value with a compile defect")
			return "submitted plan", nil
		case agent.PlanThenExecuteReviewRepairTaskFamily:
			reviewCalled = true
			if !strings.Contains(prompt, "source_sanity_failed:pkg/feature.go") {
				t.Fatalf("review prompt should expose failed source_sanity obligation, got:\n%s", prompt)
			}
			writeRunTestPlanViaSubmitPlan(t, cfg, []workspace.PlanEdit{
				{
					Path:    filepath.ToSlash(filepath.Join("pkg", "feature.go")),
					OldText: "return missingSymbol",
					NewText: "return 1",
					Purpose: "repair compile failure",
				},
			}, "Verification Checklist:\n- Source sanity: repair the undefined symbol.\n- Scope safety: edit only the changed source file.")
			return "submitted repair plan", nil
		default:
			t.Fatalf("unexpected task family %q", opts.TaskFamily)
			return "", nil
		}
	}, nil, &stdout, &stderr)

	if result.ExitCode != 0 {
		t.Fatalf("expected review-repair to recover source sanity failure, got code=%d stdout=%s stderr=%s", result.ExitCode, stdout.String(), stderr.String())
	}
	if !reviewCalled {
		t.Fatalf("review-repair phase was not called")
	}
	if got := readFileStringForRunTest(t, sourcePath); strings.Contains(got, "missingSymbol") || !strings.Contains(got, "return 1") {
		t.Fatalf("expected repaired source body, got:\n%s", got)
	}
	if !strings.Contains(stdout.String(), `"type":"idiom.plan_execute.executor_verification_repair_required"`) {
		t.Fatalf("expected typed executor verification handoff event, got stdout=%s", stdout.String())
	}
	state, _, err := LoadProgressState(root, result.SessionID)
	if err != nil {
		t.Fatalf("load progress state: %v", err)
	}
	if state.Status != ProgressStatusCompleted || state.Review.Status != "repaired" {
		t.Fatalf("expected completed repaired progress, got %#v", state)
	}
	if record, recordPath, found, err := recovery.TryLoadPlanExecuteFailureRecord(root, result.SessionID); err != nil || found {
		t.Fatalf("successful repair should not leave stale recovery record found=%v path=%s record=%#v err=%v", found, recordPath, record, err)
	}
}

func TestRunContinuesReviewRepairAfterRequestedVerificationFailure(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/recover-requested-verification\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatalf("seed go.mod: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "pkg"), 0o755); err != nil {
		t.Fatalf("mkdir pkg: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "pkg", "feature.go"), []byte("package pkg\n\nfunc Value() int { return 1 }\n"), 0o644); err != nil {
		t.Fatalf("seed feature.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "pkg", "feature_test.go"), []byte("package pkg\n\nimport \"testing\"\n\nfunc TestValue(t *testing.T) {\n\tif Value() != 1 {\n\t\tt.Fatalf(\"unexpected value: %d\", Value())\n\t}\n}\n"), 0o644); err != nil {
		t.Fatalf("seed feature_test.go: %v", err)
	}
	sourcePath := filepath.Join(root, "pkg", "feature.go")
	cfg := config.Default()
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true
	cfg.Workspace.CommandTimeoutSeconds = 30

	command := "go test ./... -run 'TestValue' -count=1"
	var stdout, stderr bytes.Buffer
	reviewCalled := false
	result := Run(cfg, Options{
		Prompt:     "Update Value. Make this pass: `" + command + "`.",
		JSONEvents: true,
	}, func(cfg config.Config, prompt string, opts RunOptions, _ io.Writer) (string, error) {
		switch opts.TaskFamily {
		case agent.PlanThenExecutePlannerTaskFamily:
			writeRunTestPlanViaSubmitPlan(t, cfg, []workspace.PlanEdit{
				{
					Path:    filepath.ToSlash(filepath.Join("pkg", "feature.go")),
					OldText: "return 1",
					NewText: "return 2",
					Purpose: "change Value behavior",
				},
			}, "change Value behavior")
			return "submitted plan", nil
		case agent.PlanThenExecuteReviewRepairTaskFamily:
			reviewCalled = true
			if !strings.Contains(prompt, "verification_failed:"+command) {
				t.Fatalf("review prompt should expose failed requested verification obligation, got:\n%s", prompt)
			}
			writeRunTestPlanViaSubmitPlan(t, cfg, []workspace.PlanEdit{
				{
					Path:    filepath.ToSlash(filepath.Join("pkg", "feature.go")),
					OldText: "return 2",
					NewText: "return 1",
					Purpose: "repair requested verification failure",
				},
			}, "Verification Checklist:\n- Requested verification: restore TestValue expectation.\n- Scope safety: edit only the changed source file.")
			return "submitted repair plan", nil
		default:
			t.Fatalf("unexpected task family %q", opts.TaskFamily)
			return "", nil
		}
	}, nil, &stdout, &stderr)

	if result.ExitCode != 0 {
		t.Fatalf("expected review-repair to recover requested verification failure, got code=%d stdout=%s stderr=%s", result.ExitCode, stdout.String(), stderr.String())
	}
	if !reviewCalled {
		t.Fatalf("review-repair phase was not called")
	}
	if got := readFileStringForRunTest(t, sourcePath); strings.Contains(got, "return 2") || !strings.Contains(got, "return 1") {
		t.Fatalf("expected repaired source body, got:\n%s", got)
	}
	state, _, err := LoadProgressState(root, result.SessionID)
	if err != nil {
		t.Fatalf("load progress state: %v", err)
	}
	if state.Status != ProgressStatusCompleted || state.Review.Status != "repaired" {
		t.Fatalf("expected completed repaired progress, got %#v", state)
	}
	if len(state.Executor.Verification) == 0 || state.Executor.Verification[len(state.Executor.Verification)-1].Status != "ok" {
		t.Fatalf("expected successful post-review requested verification evidence, got %#v", state.Executor.Verification)
	}
}

func TestRunKeepsSourceSanityFailureWhenReviewRepairUnavailable(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/recover-source-sanity-unavailable\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatalf("seed go.mod: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "pkg"), 0o755); err != nil {
		t.Fatalf("mkdir pkg: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "pkg", "feature.go"), []byte("package pkg\n\nfunc Value() int { return 1 }\n"), 0o644); err != nil {
		t.Fatalf("seed feature.go: %v", err)
	}
	cfg := config.Default()
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true
	cfg.Workspace.CommandTimeoutSeconds = 30

	var stdout, stderr bytes.Buffer
	result := Run(cfg, Options{
		Prompt:     "Update Value.",
		JSONEvents: true,
	}, func(cfg config.Config, _ string, opts RunOptions, _ io.Writer) (string, error) {
		switch opts.TaskFamily {
		case agent.PlanThenExecutePlannerTaskFamily:
			writeRunTestPlanViaSubmitPlan(t, cfg, []workspace.PlanEdit{
				{
					Path:    filepath.ToSlash(filepath.Join("pkg", "feature.go")),
					OldText: "return 1",
					NewText: "return missingSymbol",
					Purpose: "update Value with a compile defect",
				},
			}, "update Value with a compile defect")
			return "submitted plan", nil
		case agent.PlanThenExecuteReviewRepairTaskFamily:
			return "", errors.New("review model unavailable")
		default:
			t.Fatalf("unexpected task family %q", opts.TaskFamily)
			return "", nil
		}
	}, nil, &stdout, &stderr)

	if result.ExitCode != 1 {
		t.Fatalf("expected unresolved source sanity failure, got code=%d stdout=%s stderr=%s", result.ExitCode, stdout.String(), stderr.String())
	}
	if result.FailureKind != RunFailureSourceSanityFailed {
		t.Fatalf("expected source_sanity_failed result, got %#v", result)
	}
	state, _, err := LoadProgressState(root, result.SessionID)
	if err != nil {
		t.Fatalf("load progress state: %v", err)
	}
	if state.Status != ProgressStatusFailed || state.FailureKind != RunFailureSourceSanityFailed || state.Review.Status != "degraded (review unavailable)" {
		t.Fatalf("expected failed source sanity progress after degraded review, got %#v", state)
	}
	record, _, found, err := recovery.TryLoadPlanExecuteFailureRecord(root, result.SessionID)
	if err != nil || !found {
		t.Fatalf("expected terminal failure record found=%v err=%v", found, err)
	}
	if record.Failure.Class != recovery.FailureClassVerification {
		t.Fatalf("expected verification failure class, got %#v", record.Failure)
	}
}

func TestRunRejectsInitialPlanWhenRequestedTestEditMissingBeforeWrites(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "pkg.go"), []byte("package demo\n\nfunc Hardened() bool { return false }\n"), 0o644); err != nil {
		t.Fatalf("seed pkg.go: %v", err)
	}
	cfg := config.Default()
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true
	var stdout, stderr bytes.Buffer

	result := Run(cfg, Options{
		Prompt:             "Implement the hardening and add regression tests proving the edge case is rejected.",
		JSONEvents:         true,
		SkipReviewRepair:   true,
		EditSetObligations: EditSetObligations{}.With(EditSetObligationKindTestEdit, "test"),
	}, func(cfg config.Config, _ string, opts RunOptions, _ io.Writer) (string, error) {
		if opts.TaskFamily != agent.PlanThenExecutePlannerTaskFamily {
			t.Fatalf("unexpected task family %q", opts.TaskFamily)
		}
		writeRunTestPlanViaSubmitPlan(t, cfg, []workspace.PlanEdit{
			{Path: "pkg.go", OldText: "return false", NewText: "return true", Purpose: "harden behavior"},
		}, "production-only plan")
		return "submitted production-only plan", nil
	}, nil, &stdout, &stderr)

	if result.ExitCode != 1 {
		t.Fatalf("expected missing test edit rejection, got code=%d stdout=%s stderr=%s", result.ExitCode, stdout.String(), stderr.String())
	}
	if result.FailureKind != "missing_requested_test_edit" || !strings.Contains(stderr.String(), "missing_requested_test_edit") {
		t.Fatalf("expected missing_requested_test_edit failure, result=%#v stderr=%s", result, stderr.String())
	}
	if got := readFileStringForRunTest(t, filepath.Join(root, "pkg.go")); strings.Contains(got, "return true") {
		t.Fatalf("production edit must not be applied after plan rejection, got:\n%s", got)
	}
	events := planExecuteJSONEventsForRunTest(t, stdout.String())
	event, ok := planExecuteEventByTypeForRunTest(events, "idiom.plan_execute.planner_plan_rejected")
	if !ok {
		t.Fatalf("expected planner_plan_rejected event, got %s", stdout.String())
	}
	if event["reason"] != "missing_requested_test_edit" {
		t.Fatalf("expected missing_requested_test_edit reason, got %#v", event)
	}
}

func TestRunRejectsInitialPlanWhenRequestedImplementationEditMissingBeforeWrites(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "feature.go"), []byte("package demo\n\nfunc Enabled() bool { return false }\n"), 0o644); err != nil {
		t.Fatalf("seed feature.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "feature_test.go"), []byte("package demo\n\nfunc TestEnabled(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatalf("seed feature_test.go: %v", err)
	}
	cfg := config.Default()
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true
	var stdout, stderr bytes.Buffer

	result := Run(cfg, Options{
		Prompt:             "Implement the feature. Start by adding failing tests, then implement.",
		JSONEvents:         true,
		SkipReviewRepair:   true,
		EditSetObligations: EditSetObligations{}.With(EditSetObligationKindImplementationEdit, "test"),
	}, func(cfg config.Config, _ string, opts RunOptions, _ io.Writer) (string, error) {
		if opts.TaskFamily != agent.PlanThenExecutePlannerTaskFamily {
			t.Fatalf("unexpected task family %q", opts.TaskFamily)
		}
		writeRunTestPlanViaSubmitPlan(t, cfg, []workspace.PlanEdit{
			{Path: "feature_test.go", OldText: "func TestEnabled(t *testing.T) {}\n", NewText: "func TestEnabled(t *testing.T) {\n\tif !Enabled() {\n\t\tt.Fatal(\"disabled\")\n\t}\n}\n", Purpose: "add feature test"},
		}, "test-only plan")
		return "submitted test-only plan", nil
	}, nil, &stdout, &stderr)

	if result.ExitCode != 1 {
		t.Fatalf("expected missing implementation edit rejection, got code=%d stdout=%s stderr=%s", result.ExitCode, stdout.String(), stderr.String())
	}
	if result.FailureKind != "missing_requested_implementation_edit" || !strings.Contains(stderr.String(), "missing_requested_implementation_edit") {
		t.Fatalf("expected missing_requested_implementation_edit failure, result=%#v stderr=%s", result, stderr.String())
	}
	if got := readFileStringForRunTest(t, filepath.Join(root, "feature_test.go")); strings.Contains(got, "t.Fatal") {
		t.Fatalf("test-only edit must not be applied after plan rejection, got:\n%s", got)
	}
	events := planExecuteJSONEventsForRunTest(t, stdout.String())
	event, ok := planExecuteEventByTypeForRunTest(events, "idiom.plan_execute.planner_plan_rejected")
	if !ok {
		t.Fatalf("expected planner_plan_rejected event, got %s", stdout.String())
	}
	if event["reason"] != "missing_requested_implementation_edit" {
		t.Fatalf("expected missing_requested_implementation_edit reason, got %#v", event)
	}
}

func TestRunReviewRepairPhaseRejectsPassWhenRequestedTestEditMissing(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "pkg.go"), []byte("package demo\n\nfunc Hardened() bool { return true }\n"), 0o644); err != nil {
		t.Fatalf("seed pkg.go: %v", err)
	}
	cfg := config.Default()
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{
			{Path: "pkg.go", OldText: "return false", NewText: "return true", Purpose: "harden behavior"},
		},
	}
	var stdout bytes.Buffer
	var gotPrompt string
	_, err := RunReviewRepairPhase(
		cfg,
		"Implement the hardening and add regression tests proving the edge case is rejected.",
		plan,
		nil,
		nil,
		ExecutorPhaseReport{},
		Options{
			JSONEvents:         true,
			EditSetObligations: EditSetObligations{}.With(EditSetObligationKindTestEdit, "test"),
		},
		&stdout,
		func(_ config.Config, prompt string, _ RunOptions, _ io.Writer) (string, error) {
			gotPrompt = prompt
			return "Decision: pass\n\nVerification Checklist:\n- Required behavior: source diff contains the hardening.\n- Test or command evidence: diff evidence shows the code change.", nil
		},
		nil,
	)
	if err == nil {
		t.Fatalf("expected missing requested test edit to reject review pass")
	}
	if !strings.Contains(err.Error(), "missing_requested_test_edit") {
		t.Fatalf("expected missing test edit rejection, got %v", err)
	}
	if !strings.Contains(gotPrompt, "requested_test_edit_missing") {
		t.Fatalf("review prompt should expose requested test edit obligation, got:\n%s", gotPrompt)
	}
	events := planExecuteJSONEventsForRunTest(t, stdout.String())
	event, ok := planExecuteEventByTypeForRunTest(events, "idiom.plan_execute.review_repair_pass_rejected")
	if !ok {
		t.Fatalf("expected review_repair_pass_rejected event, got %s", stdout.String())
	}
	if event["reason"] != "missing_requested_test_edit" {
		t.Fatalf("expected missing_requested_test_edit reason, got %#v", event)
	}
}

func TestRunReviewRepairPhaseForcesVisibleTerminalContract(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("good\n"), 0o644); err != nil {
		t.Fatalf("seed a.txt: %v", err)
	}
	cfg := config.Default()
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{
			{Path: "a.txt", OldText: "bad", NewText: "good", Purpose: "seed changed path"},
		},
	}
	var stdout bytes.Buffer
	var gotOpts RunOptions
	report, err := RunReviewRepairPhase(
		cfg,
		"Review behavior.",
		plan,
		nil,
		nil,
		ExecutorPhaseReport{},
		Options{},
		&stdout,
		func(_ config.Config, _ string, opts RunOptions, _ io.Writer) (string, error) {
			gotOpts = opts
			return "Decision: pass\n\nVerification Checklist:\n- Required behavior: good is present.\n- Scope safety: no corrective edit needed.", nil
		},
		nil,
	)
	if err != nil {
		t.Fatalf("review-repair pass should be accepted: %v", err)
	}
	if report.Status != "pass (no corrective edits)" {
		t.Fatalf("unexpected report: %#v", report)
	}
	if gotOpts.TaskFamily != agent.PlanThenExecuteReviewRepairTaskFamily {
		t.Fatalf("review-repair should use review-repair task family, got %q", gotOpts.TaskFamily)
	}
	if !gotOpts.DisableThinking {
		t.Fatal("review-repair must force visible no-thinking output for its terminal contract")
	}
}

func TestRunReviewRepairPhaseRejectsRecoveryTemplateAsPass(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("good\n"), 0o644); err != nil {
		t.Fatalf("seed a.txt: %v", err)
	}
	cfg := config.Default()
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{
			{Path: "a.txt", OldText: "bad", NewText: "good", Purpose: "seed changed path"},
		},
	}
	var stdout bytes.Buffer
	_, err := RunReviewRepairPhase(
		cfg,
		"Review behavior.",
		plan,
		nil,
		nil,
		ExecutorPhaseReport{},
		Options{},
		&stdout,
		func(_ config.Config, _ string, _ RunOptions, _ io.Writer) (string, error) {
			return "The completion gate rejected the review-repair response.\n\nPass shape:\nDecision: pass\n\nVerification Checklist:\n- Required behavior: <checked fact>.\n\nDo not add prose before or after the chosen terminal shape.", nil
		},
		nil,
	)
	if err == nil {
		t.Fatalf("expected recovery template text not to count as a review pass")
	}
	if !strings.Contains(err.Error(), "Decision: pass") || !strings.Contains(err.Error(), "Verification Checklist") {
		t.Fatalf("error should require an actual terminal pass shape, got %v", err)
	}
}

func TestRunReviewRepairPhaseRejectsPassWhenRequestedImplementationEditMissing(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "feature_test.go"), []byte("package demo\n\nfunc TestEnabled(t *testing.T) {\n\tif !Enabled() {\n\t\tt.Fatal(\"disabled\")\n\t}\n}\n"), 0o644); err != nil {
		t.Fatalf("seed feature_test.go: %v", err)
	}
	cfg := config.Default()
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{
			{Path: "feature_test.go", OldText: "func TestEnabled(t *testing.T) {}\n", NewText: "func TestEnabled(t *testing.T) {\n\tif !Enabled() {\n\t\tt.Fatal(\"disabled\")\n\t}\n}\n", Purpose: "add feature test"},
		},
	}
	var stdout bytes.Buffer
	var gotPrompt string
	_, err := RunReviewRepairPhase(
		cfg,
		"Implement the feature. Start by adding failing tests, then implement.",
		plan,
		nil,
		nil,
		ExecutorPhaseReport{},
		Options{
			JSONEvents:         true,
			EditSetObligations: EditSetObligations{}.With(EditSetObligationKindImplementationEdit, "test"),
		},
		&stdout,
		func(_ config.Config, prompt string, _ RunOptions, _ io.Writer) (string, error) {
			gotPrompt = prompt
			return "Decision: pass\n\nVerification Checklist:\n- Required behavior: test coverage was added.\n- Scope safety: diff is limited to one test file.", nil
		},
		nil,
	)
	if err == nil {
		t.Fatalf("expected missing requested implementation edit to reject review pass")
	}
	if !strings.Contains(err.Error(), "missing_requested_implementation_edit") {
		t.Fatalf("expected missing implementation edit rejection, got %v", err)
	}
	if !strings.Contains(gotPrompt, "requested_implementation_edit_missing") {
		t.Fatalf("review prompt should expose requested implementation edit obligation, got:\n%s", gotPrompt)
	}
	events := planExecuteJSONEventsForRunTest(t, stdout.String())
	event, ok := planExecuteEventByTypeForRunTest(events, "idiom.plan_execute.review_repair_pass_rejected")
	if !ok {
		t.Fatalf("expected review_repair_pass_rejected event, got %s", stdout.String())
	}
	if event["reason"] != "missing_requested_implementation_edit" {
		t.Fatalf("expected missing_requested_implementation_edit reason, got %#v", event)
	}
}

func TestRunReviewRepairPhaseRejectsPassThatAdmitsCommandWasNotRun(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("after\n"), 0o644); err != nil {
		t.Fatalf("seed a.txt: %v", err)
	}
	cfg := config.Default()
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{
			{Path: "a.txt", OldText: "before", NewText: "after", Purpose: "seed changed path"},
		},
	}
	var stdout bytes.Buffer
	_, err := RunReviewRepairPhase(
		cfg,
		"Implement behavior and run focused tests.",
		plan,
		nil,
		nil,
		ExecutorPhaseReport{},
		Options{
			JSONEvents:         true,
			EditSetObligations: EditSetObligations{}.With(EditSetObligationKindTestEdit, "test"),
		},
		&stdout,
		func(_ config.Config, _ string, _ RunOptions, _ io.Writer) (string, error) {
			return "Decision: pass\n\nVerification Checklist:\n- Required behavior: diff looks scoped.\n- Test or command evidence: We didn't run `go test ./cmd/idiom`.", nil
		},
		nil,
	)
	if err == nil {
		t.Fatalf("expected missing command evidence admission to reject review pass")
	}
	if !strings.Contains(err.Error(), "missing_command_evidence_claim") {
		t.Fatalf("expected missing command evidence rejection, got %v", err)
	}
	events := planExecuteJSONEventsForRunTest(t, stdout.String())
	event, ok := planExecuteEventByTypeForRunTest(events, "idiom.plan_execute.review_repair_pass_rejected")
	if !ok {
		t.Fatalf("expected review_repair_pass_rejected event, got %s", stdout.String())
	}
	if event["reason"] != "missing_command_evidence_claim" {
		t.Fatalf("expected missing_command_evidence_claim reason, got %#v", event)
	}
}

func TestRunReviewRepairPhaseRejectsPassWhenSourceSanityFailed(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package demo\n\nfunc Value() int { return missing }\n"), 0o644); err != nil {
		t.Fatalf("seed a.go: %v", err)
	}
	cfg := config.Default()
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{
			{Path: "a.go", OldText: "return 1", NewText: "return missing", Purpose: "introduce compile issue"},
		},
	}
	var stdout bytes.Buffer
	var gotPrompt string
	_, err := RunReviewRepairPhase(
		cfg,
		"Repair Value.",
		plan,
		nil,
		nil,
		ExecutorPhaseReport{Total: 1, Succeeded: 1, SourceSanity: []PhaseEvidenceCheck{{
			Kind:   "source_sanity",
			Path:   "a.go",
			Status: "failed",
			Error:  "go test compile failed: undefined: missing",
		}}},
		Options{JSONEvents: true},
		&stdout,
		func(_ config.Config, prompt string, _ RunOptions, _ io.Writer) (string, error) {
			gotPrompt = prompt
			return "Decision: pass\n\nVerification Checklist:\n- Source sanity: acceptable.", nil
		},
		nil,
	)
	if err == nil {
		t.Fatalf("expected failed source_sanity to reject review pass")
	}
	if !strings.Contains(err.Error(), "verification_failed") {
		t.Fatalf("expected verification_failed rejection, got %v", err)
	}
	if !strings.Contains(gotPrompt, "source_sanity_failed:a.go") {
		t.Fatalf("review prompt should expose failed source_sanity obligation, got:\n%s", gotPrompt)
	}
	events := planExecuteJSONEventsForRunTest(t, stdout.String())
	event, ok := planExecuteEventByTypeForRunTest(events, "idiom.plan_execute.review_repair_pass_rejected")
	if !ok {
		t.Fatalf("expected review_repair_pass_rejected event, got %s", stdout.String())
	}
	if event["reason"] != "verification_failed" {
		t.Fatalf("expected verification_failed reason, got %#v", event)
	}
}

func TestRunReviewRepairPhaseRetriesContradictoryPassIntoCorrectivePlan(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "a.txt")
	if err := os.WriteFile(path, []byte("value=bad\n"), 0o644); err != nil {
		t.Fatalf("seed a.txt: %v", err)
	}
	cfg := config.Default()
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{
			{Path: "a.txt", OldText: "value=good", NewText: "value=bad", Purpose: "introduce bad value"},
		},
	}
	var stdout bytes.Buffer
	attempts := 0
	report, err := RunReviewRepairPhase(
		cfg,
		"Keep a.txt good.",
		plan,
		nil,
		nil,
		ExecutorPhaseReport{Total: 1, Succeeded: 1, Verification: []PhaseEvidenceCheck{{
			Kind:    "verification",
			Command: "check a.txt",
			Status:  "failed",
			Error:   "value was bad",
		}}},
		Options{JSONEvents: true},
		&stdout,
		func(cfg config.Config, prompt string, opts RunOptions, _ io.Writer) (string, error) {
			attempts++
			if opts.TaskFamily != agent.PlanThenExecuteReviewRepairTaskFamily {
				t.Fatalf("unexpected task family %q", opts.TaskFamily)
			}
			if attempts == 1 {
				if !strings.Contains(prompt, "verification_failed:check a.txt") {
					t.Fatalf("initial prompt should expose failed verification, got:\n%s", prompt)
				}
				return "Decision: pass\n\nVerification Checklist:\n- Required behavior: acceptable.\n- Test or command evidence: acceptable.", nil
			}
			if !strings.Contains(prompt, "Rejected Previous Review Outcome") || !strings.Contains(prompt, "`Decision: pass` is not an allowed terminal outcome") {
				t.Fatalf("retry prompt should forbid another pass, got:\n%s", prompt)
			}
			writeRunTestPlanViaSubmitPlan(t, cfg, []workspace.PlanEdit{
				{Path: "a.txt", OldText: "value=bad", NewText: "value=good", Purpose: "repair failed verification"},
			}, "Verification Checklist:\n- Failing item: check a.txt reported value was bad.\n- Scope safety: edit only a.txt.")
			return "submitted repair plan", nil
		},
		nil,
	)
	if err != nil {
		t.Fatalf("expected retry corrective plan to repair, got %v", err)
	}
	if attempts != 2 {
		t.Fatalf("expected one retry after contradictory pass, got %d attempts", attempts)
	}
	if report.Status != "repaired" || report.Repairs != 1 {
		t.Fatalf("expected repaired report, got %#v", report)
	}
	if got := readFileStringForRunTest(t, path); got != "value=good\n" {
		t.Fatalf("expected repair to apply, got %q", got)
	}
	events := planExecuteJSONEventsForRunTest(t, stdout.String())
	if _, ok := planExecuteEventByTypeForRunTest(events, "idiom.plan_execute.review_repair_retry"); !ok {
		t.Fatalf("expected review_repair_retry event, got %s", stdout.String())
	}
}

func TestRunReviewRepairPhaseRejectsCorrectivePlanWhenRequestedTestEditStillMissing(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "pkg.go"), []byte("package demo\n\nfunc Hardened() bool { return false }\n"), 0o644); err != nil {
		t.Fatalf("seed pkg.go: %v", err)
	}
	cfg := config.Default()
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{
			{Path: "pkg.go", OldText: "return false", NewText: "return true", Purpose: "harden behavior"},
		},
	}
	var stdout bytes.Buffer
	_, err := RunReviewRepairPhase(
		cfg,
		"Implement the hardening and add regression tests proving the edge case is rejected.",
		plan,
		nil,
		nil,
		ExecutorPhaseReport{},
		Options{
			JSONEvents:         true,
			EditSetObligations: EditSetObligations{}.With(EditSetObligationKindTestEdit, "test"),
		},
		&stdout,
		func(cfg config.Config, _ string, opts RunOptions, _ io.Writer) (string, error) {
			if opts.TaskFamily != agent.PlanThenExecuteReviewRepairTaskFamily {
				t.Fatalf("unexpected task family %q", opts.TaskFamily)
			}
			writeRunTestPlanViaSubmitPlan(t, cfg, []workspace.PlanEdit{
				{Path: "pkg.go", OldText: "return false", NewText: "return true", Purpose: "retry production-only hardening"},
			}, "Verification Checklist:\n- Required behavior: hardening is present.\n")
			return "submitted production-only repair", nil
		},
		nil,
	)
	if err == nil {
		t.Fatalf("expected production-only repair plan rejection")
	}
	if !strings.Contains(err.Error(), "missing_requested_test_edit") {
		t.Fatalf("expected missing_requested_test_edit, got %v", err)
	}
	if got := readFileStringForRunTest(t, filepath.Join(root, "pkg.go")); strings.Contains(got, "return true") {
		t.Fatalf("repair rejection must happen before writes, got:\n%s", got)
	}
	events := planExecuteJSONEventsForRunTest(t, stdout.String())
	event, ok := planExecuteEventByTypeForRunTest(events, "idiom.plan_execute.review_repair_plan_rejected")
	if !ok {
		t.Fatalf("expected review_repair_plan_rejected event, got %s", stdout.String())
	}
	if event["reason"] != "missing_requested_test_edit" {
		t.Fatalf("expected missing_requested_test_edit reason, got %#v", event)
	}
}

func TestPlanExecutePromptProseDoesNotCreateEditSetObligations(t *testing.T) {
	for _, prompt := range []string{
		"Implement the feature and add tests.",
		"add a regression test for parser routes",
		"Harden temp symlink writes and add regression tests.",
	} {
		if obligations := EditSetObligationsFromPrompt(prompt); len(obligations) != 0 {
			t.Fatalf("prompt prose must not create hidden edit-set obligations: %q -> %#v", prompt, obligations)
		}
	}
	for _, path := range []string{"tests/parser_test.py", "test/foo.py", "tests/panel_contract.gd"} {
		if !planExecuteChangedPathsIncludeTest([]string{path}) {
			t.Fatalf("path should count as a test path: %s", path)
		}
	}
	if reason := (EditSetObligations{}).With(EditSetObligationKindImplementationEdit, "test").RejectionReason([]string{"docs/feature.md", "pkg/feature_test.go"}); reason != "missing_requested_implementation_edit" {
		t.Fatalf("docs plus tests should not satisfy implementation edit obligation, got %q", reason)
	}
}

func TestExtractRequestedVerificationCommandsPreservesQuotedRunPattern(t *testing.T) {
	prompt := "/plan-execute Continue the repair. Make this pass: go test ./internal/productworkflow ./cmd/idiom -run 'Test.*(Coverage|TDD|ProductBriefRender|KnownTDD)' -count=1. Do not edit every allowed path unless it is needed."
	want := "go test ./internal/productworkflow ./cmd/idiom -run 'Test.*(Coverage|TDD|ProductBriefRender|KnownTDD)' -count=1"
	got := ExtractRequestedVerificationCommands(prompt)
	if len(got) != 1 || got[0] != want {
		t.Fatalf("verification commands = %#v, want %#v", got, []string{want})
	}
	args, ok := VerificationCommandArgs(got[0])
	if !ok {
		t.Fatalf("expected command to be allowed")
	}
	if joined := strings.Join(args, "\x00"); !strings.Contains(joined, "Test.*(Coverage|TDD|ProductBriefRender|KnownTDD)") {
		t.Fatalf("quoted -run pattern was not preserved in args: %#v", args)
	}
}

func TestExtractRequestedVerificationCommandsHandlesRunAfterExecutorPhrase(t *testing.T) {
	prompt := "After the executor applies the plan, run `go test ./...`."
	got := ExtractRequestedVerificationCommands(prompt)
	if len(got) != 1 || got[0] != "go test ./..." {
		t.Fatalf("verification commands = %#v, want %#v", got, []string{"go test ./..."})
	}
}

func TestExtractRequestedVerificationCommandsIgnoresSuggestedTests(t *testing.T) {
	prompt := strings.Join([]string{
		"# Plan",
		"",
		"## Suggested Tests",
		"",
		"- `go test ./...` - context packet recommendation",
		"- Acceptance: `go test ./internal/productworkflow`",
		"",
		"Planner-owned build/test/scenario acceptance commands are pending outside this lane unless Phase Evidence names the exact command as successful.",
	}, "\n")
	if got := ExtractRequestedVerificationCommands(prompt); len(got) != 0 {
		t.Fatalf("suggested/context test mentions should not become executor-owned verification commands, got %#v", got)
	}
}

func TestPlanExecuteEditSetObligationsScopeToBriefBody(t *testing.T) {
	prompt := strings.Join([]string{
		"## Context Pack",
		"",
		"Candidate files mention implement, fallback, and add tests.",
		"",
		pathparse.WrapBriefBody("# Objective\n\nExplain the current status only."),
	}, "\n")

	if obligations := EditSetObligationsFromPrompt(prompt); len(obligations) != 0 {
		t.Fatalf("generated context outside the brief body must not create edit-set obligations: %#v", obligations)
	}
}

func TestPlanExecuteTypedScopeOwnsEditSetObligations(t *testing.T) {
	opts := Options{
		ScopeContract: NewPlanScopeContract(nil, []string{"pkg/feature.go", "pkg/feature_test.go"}, nil),
	}
	prompt := pathparse.WrapBriefBody("# Objective\n\nImplement the feature and add tests.")

	if obligations := planExecuteEditSetObligations(opts, prompt); len(obligations) != 0 {
		t.Fatalf("typed scope without edit-set obligations must not re-infer obligations from prompt prose: %#v", obligations)
	}
}

func TestPlanExecuteImplementationEditObligationRequiresSourceLikePath(t *testing.T) {
	obligations := (EditSetObligations{}).With(EditSetObligationKindImplementationEdit, "test")
	for _, paths := range [][]string{
		{"package.json", "pkg/feature_test.go"},
		{"config/settings.yaml", "pkg/feature_test.go"},
		{"notes.txt", "pkg/feature_test.go"},
		{"docs/feature.md", "pkg/feature_test.go"},
	} {
		if reason := obligations.RejectionReason(paths); reason != EditSetFailureMissingRequestedImplementationEdit {
			t.Fatalf("non-source paths must not satisfy implementation obligation, paths=%#v reason=%q", paths, reason)
		}
	}
	for _, path := range []string{
		"pkg/feature.go",
		"web/component.tsx",
		"native/src/system.cpp",
		"internal/cliassets/lanes/plan-execute/preamble.md",
	} {
		if reason := obligations.RejectionReason([]string{path, "pkg/feature_test.go"}); reason != "" {
			t.Fatalf("source-like path should satisfy implementation obligation, path=%s reason=%q", path, reason)
		}
	}
}

func TestPlanExecuteExplicitEditSetObligationsSurvivePromptOnlyLanguage(t *testing.T) {
	opts := Options{
		EditSetObligations: EditSetObligations{}.With(EditSetObligationKindImplementationEdit, "retry"),
	}
	prompt := "This retry is for a test-only recovery pass. Do not infer implementation authority from this prompt prose."

	obligations := planExecuteEditSetObligations(opts, prompt)
	if !obligations.Has(EditSetObligationKindImplementationEdit) {
		t.Fatalf("explicit implementation obligation was lost: %#v", obligations)
	}
	if reason := obligations.RejectionReason([]string{"pkg/feature_test.go"}); reason != EditSetFailureMissingRequestedImplementationEdit {
		t.Fatalf("explicit implementation obligation must reject test-only edits, got %q from %#v", reason, obligations)
	}
	if reason := obligations.RejectionReason([]string{"pkg/feature.go"}); reason != "" {
		t.Fatalf("implementation edit should satisfy explicit obligation, got %q", reason)
	}
}

func TestRunResumeRejectsPromptMismatch(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	state := NewProgressState("resume_mismatch", "original prompt", "", nil, nil, timeNowForRunTest())
	state.Phase = ProgressPhaseExecutor
	state.Status = ProgressStatusCompleted
	state.Executor.Status = ProgressStatusCompleted
	state.PlanPath = ".idiom/plans/missing.plan.json"
	state.TotalEdits = 1
	state.ChangedPaths = []string{"a.txt"}
	if _, err := SaveProgressState(root, &state); err != nil {
		t.Fatalf("save progress state: %v", err)
	}
	var stdout, stderr bytes.Buffer
	result := Run(cfg, Options{Prompt: "different prompt", Resume: "resume_mismatch"}, func(config.Config, string, RunOptions, io.Writer) (string, error) {
		t.Fatal("prompt runner should not be called for mismatched resume prompt")
		return "", nil
	}, nil, &stdout, &stderr)
	if result.ExitCode != 1 {
		t.Fatalf("expected prompt mismatch failure, got code=%d stdout=%s stderr=%s", result.ExitCode, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "different prompt") {
		t.Fatalf("stderr should explain prompt mismatch, got %s", stderr.String())
	}
}

func TestRunResumeRejectsMissingSession(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	var stdout, stderr bytes.Buffer

	result := Run(cfg, Options{Prompt: "resume missing", Resume: "missing-session"}, func(config.Config, string, RunOptions, io.Writer) (string, error) {
		t.Fatal("prompt runner should not be called for missing resume state")
		return "", nil
	}, nil, &stdout, &stderr)

	if result.ExitCode != 1 {
		t.Fatalf("expected missing resume to fail, got code=%d stdout=%s stderr=%s", result.ExitCode, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "does not exist") {
		t.Fatalf("stderr should explain missing resume state, got %s", stderr.String())
	}
}

func writeRunTestPlanViaSubmitPlan(t *testing.T, cfg config.Config, edits []workspace.PlanEdit, rationale string) {
	t.Helper()
	mgr := workspace.NewManager(cfg.Workspace)
	result := mgr.Execute(context.Background(), workspace.ToolRequest{
		Tool:      "submit_plan",
		Rationale: rationale,
		Edits:     edits,
	})
	if strings.TrimSpace(result.Error) != "" {
		body, _ := json.MarshalIndent(result, "", "  ")
		t.Fatalf("submit_plan failed: %s", body)
	}
}

func readFileStringForRunTest(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(body)
}

func timeNowForRunTest() time.Time {
	return time.Now().UTC()
}

func planExecuteJSONEventsForRunTest(t *testing.T, stdout string) []map[string]any {
	t.Helper()
	var events []map[string]any
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("decode JSON event %q: %v", line, err)
		}
		events = append(events, event)
	}
	return events
}

func planExecuteEventByTypeForRunTest(events []map[string]any, eventType string) (map[string]any, bool) {
	for _, event := range events {
		if event["type"] == eventType {
			return event, true
		}
	}
	return nil, false
}
