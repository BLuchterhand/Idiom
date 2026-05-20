package planexecute

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"idiom/internal/agent"
	"idiom/internal/config"
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
		Prompt:           "Implement the hardening and add regression tests proving the edge case is rejected.",
		JSONEvents:       true,
		SkipReviewRepair: true,
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
		Options{JSONEvents: true},
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
		Options{JSONEvents: true},
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
		Options{JSONEvents: true},
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

func TestPlanExecuteRequestedTestEditDetectionHandlesNegationAndCommonPaths(t *testing.T) {
	for _, prompt := range []string{
		"fix this, do not add tests",
		"implement the parser change with no test changes",
		"write code only without tests",
	} {
		if planExecutePromptRequiresTestEdit(prompt) {
			t.Fatalf("prompt should not require test edits: %q", prompt)
		}
	}
	for _, prompt := range []string{
		"add a regression test for parser routes",
		"write a test proving invalid routes fail",
		"add a unit test for parser routes",
		"add regression tests proving no tests are skipped",
		"add tests and do not skip tests",
	} {
		if !planExecutePromptRequiresTestEdit(prompt) {
			t.Fatalf("prompt should require test edits: %q", prompt)
		}
	}
	for _, path := range []string{"tests/parser_test.py", "test/foo.py", "tests/panel_contract.gd"} {
		if !planExecuteChangedPathsIncludeTest([]string{path}) {
			t.Fatalf("path should count as a test path: %s", path)
		}
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
