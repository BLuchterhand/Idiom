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
