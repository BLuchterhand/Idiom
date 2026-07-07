package lane

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	recoverycontract "idiom/internal/contracts/recovery"

	"idiom/internal/platform/config"
	cmdruntime "idiom/internal/surfaces/cli/runtime"
	"idiom/internal/workflows/recovery"
)

func TestRunLaneImplementRecoveryWritesFailureAndPacket(t *testing.T) {
	root := t.TempDir()
	briefPath := filepath.Join(root, "brief.md")
	briefBody := "# Implement atom\n\n" +
		"raw_prompt: SECRET_PROMPT_BODY\n\n" +
		"## Files To Modify\n\n" +
		"| File | Action |\n" +
		"| --- | --- |\n" +
		"| `internal/workflows/recovery/record.go` | Update recovery records. |\n\n" +
		"## Required Edits\n\n" +
		"Update only `internal/workflows/recovery/record.go`.\n"

	if err := os.WriteFile(briefPath, []byte(briefBody), 0o644); err != nil {
		t.Fatalf("write brief: %v", err)
	}

	if err := os.MkdirAll(filepath.Join(root, ".planning"), 0o755); err != nil {
		t.Fatalf("mkdir planning: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, ".planning", "impl-session.failures.jsonl"), []byte(`{"kind":"bounded"}`+"\n"), 0o644); err != nil {
		t.Fatalf("write planning failures: %v", err)
	}

	var stdout, stderr bytes.Buffer

	code := runLaneRecoveryTest(t, root, []string{"implement", "--json", "--project-root", root, briefPath},
		func(args []string, stdout, _ io.Writer, _ cmdruntime.PromptRunner) int {
			_, _ = stdout.Write([]byte(`{"type":"session_summary","session_id":"impl-session","stop_reason":"model_error"}` + "\n"))
			_, _ = stdout.Write([]byte(`{"type":"implementation_lifecycle_state","phase":"implementation.target_write_ready","stop_reason":"model_call_budget_exhausted","remaining_required_edit_paths":["internal/workflows/recovery/record.go"],"sourced_evidence_paths":["internal/workflows/recovery/record.go"],"write_ready_nudge_count":1}` + "\n"))
			_, _ = stdout.Write([]byte("transcript: SECRET_TRANSCRIPT_BODY\n"))

			return 7
		},
		func(args []string, stdout, _ io.Writer, _ cmdruntime.PromptRunner) int { return 0 },
		&stdout, &stderr)

	if code != 7 {
		t.Fatalf("expected wrapped implement exit 7, got %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}

	record := readRecoveryRecord(t, filepath.Join(root, ".idiom", "recovery", "implement-impl-session", "failure.json"))
	if record.SourceRun.Lane != string(recoverycontract.SourceLaneImplement) || record.SourceRun.SessionID != "impl-session" {
		t.Fatalf("unexpected source run: %#v", record.SourceRun)
	}

	if record.Failure.Kind != "implement_process_failed" || record.Failure.Class != recoverycontract.FailureClassExecutorEdit {
		t.Fatalf("unexpected failure: %#v", record.Failure)
	}

	if record.NextAction != recoverycontract.NextActionReviewDecomposition {
		t.Fatalf("next action=%q", record.NextAction)
	}

	if record.SourceRun.PromptFile != "brief.md" {
		t.Fatalf("record should store original workspace-relative brief path, got %q", record.SourceRun.PromptFile)
	}

	if len(record.SourceRun.RequiredEditPaths) != 1 || record.SourceRun.RequiredEditPaths[0] != "internal/workflows/recovery/record.go" {
		t.Fatalf("required edit paths should come from original brief, got %#v", record.SourceRun.RequiredEditPaths)
	}

	if record.Implementation == nil {
		t.Fatal("expected implementation state in recovery record")
	}

	if record.Implementation.Phase != "implementation.target_write_ready" ||
		record.Implementation.StopReason != "model_call_budget_exhausted" ||
		record.Implementation.WriteReadyNudgeCount != 1 {

		t.Fatalf("unexpected implementation state: %#v", record.Implementation)
	}

	if len(record.Implementation.RemainingRequiredEditPaths) != 1 ||
		record.Implementation.RemainingRequiredEditPaths[0] != "internal/workflows/recovery/record.go" {

		t.Fatalf("expected remaining path in implementation state, got %#v", record.Implementation)
	}

	assertEvidenceRef(t, record.Failure.EvidenceRefs, "lane_history", ".idiom/lane-history.jsonl", "")
	assertEvidenceRef(t, record.Failure.EvidenceRefs, "original_brief", "brief.md", "")
	assertEvidenceRef(t, record.Failure.EvidenceRefs, "planning_failures", ".planning/impl-session.failures.jsonl", "")

	packet := readRecoveryPacket(t, filepath.Join(root, ".idiom", "recovery", "implement-impl-session", "packet.json"))
	if packet.FailureID != record.FailureID || packet.NextAction != recoverycontract.NextActionReviewDecomposition {
		t.Fatalf("unexpected packet: %#v", packet)
	}

	if len(packet.CandidateAtoms) != 1 {
		t.Fatalf("expected one recovery atom for original required path, got %#v", packet.CandidateAtoms)
	}

	if paths := packet.CandidateAtoms[0].RequiredPaths; len(paths) != 1 || paths[0] != "internal/workflows/recovery/record.go" {
		t.Fatalf("expected recovery atom to target original required path, got %#v", paths)
	}

	if packet.Implementation == nil || packet.Implementation.StopReason != "model_call_budget_exhausted" {
		t.Fatalf("expected implementation state in recovery packet, got %#v", packet.Implementation)
	}

	rendered, _ := json.Marshal(struct {
		Record recoverycontract.FailureRecord
		Packet recoverycontract.DecompositionPacket
	}{Record: record, Packet: packet})
	for _, forbidden := range []string{"SECRET_PROMPT_BODY", "SECRET_TRANSCRIPT_BODY", "raw_prompt", "transcript", "idiom-lane-"} {
		if strings.Contains(string(rendered), forbidden) {
			t.Fatalf("recovery artifacts leaked %q in %s", forbidden, string(rendered))
		}
	}
}

func TestRunLaneImplementRecoveryCapturesPromptRunnerSessionID(t *testing.T) {
	root := t.TempDir()
	briefPath := filepath.Join(root, "brief.md")

	if err := os.WriteFile(briefPath, []byte("# Implement atom\n\nUpdate `x.go`.\n"), 0o644); err != nil {
		t.Fatalf("write brief: %v", err)
	}

	var stdout, stderr bytes.Buffer

	code := runLaneRecoveryTest(t, root, []string{"implement", "--project-root", root, briefPath},
		func(args []string, stdout, _ io.Writer, promptRun cmdruntime.PromptRunner) int {
			cfg := config.Default()

			cfg.Workspace.SessionID = "captured-session"
			_, _ = promptRun(cfg, "", cmdruntime.RunOptions{}, stdout)

			return 3
		},
		func(args []string, stdout, _ io.Writer, _ cmdruntime.PromptRunner) int { return 0 },
		&stdout, &stderr)

	if code != 3 {
		t.Fatalf("expected wrapped implement exit 3, got %d stderr=%s", code, stderr.String())
	}

	if _, err := os.Stat(filepath.Join(root, ".idiom", "recovery", "implement-captured-session", "failure.json")); err != nil {
		t.Fatalf("expected recovery record to use captured prompt runner session id: %v", err)
	}
}

func TestLaneImplementFailureClassUsesBoundedFailureSignals(t *testing.T) {
	tests := []struct {
		name    string
		summary string
		want    recoverycontract.FailureClass
	}{
		{name: "default", summary: "implement process failed: exit_code=1 stop_reason=model_error", want: recoverycontract.FailureClassExecutorEdit},
		{name: "budget", summary: "implement process failed: context length exceeded", want: recoverycontract.FailureClassBudgetExhausted},
		{name: "verification", summary: "implement process failed: required tests failed", want: recoverycontract.FailureClassVerification},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := recovery.ClassifyImplementFailure(tt.summary); got != tt.want {
				t.Fatalf("failure class=%q want %q", got, tt.want)
			}
		})
	}
}

func TestRunLanePlanExecuteRecoveryDoesNotWriteLaneImplementRecord(t *testing.T) {
	root := t.TempDir()
	briefPath := filepath.Join(root, "brief.md")

	if err := os.WriteFile(briefPath, []byte(minimalValidPlanExecuteBrief()), 0o644); err != nil {
		t.Fatalf("write brief: %v", err)
	}

	var stdout, stderr bytes.Buffer

	code := runLaneRecoveryTest(t, root, []string{"plan-execute", "--json", "--project-root", root, briefPath},
		func(args []string, stdout, _ io.Writer, _ cmdruntime.PromptRunner) int { return 0 },
		func(args []string, stdout, _ io.Writer, _ cmdruntime.PromptRunner) int {
			_, _ = stdout.Write([]byte(`{"type":"session_summary","session_id":"plan-session","stop_reason":"planner_failed"}` + "\n"))
			return 5
		},
		&stdout, &stderr)

	if code != 5 {
		t.Fatalf("expected wrapped plan-execute exit 5, got %d stderr=%s", code, stderr.String())
	}

	if _, err := os.Stat(filepath.Join(root, ".idiom", "recovery", "implement-plan-session", "failure.json")); !os.IsNotExist(err) {
		t.Fatalf("plan-execute lane should not write implement recovery record, stat err=%v", err)
	}

	if _, err := os.Stat(filepath.Join(root, ".idiom", "recovery", "plan-execute-plan-session", "failure.json")); !os.IsNotExist(err) {
		t.Fatalf("lane wrapper should not double-write plan-execute recovery record, stat err=%v", err)
	}
}

func runLaneRecoveryTest(t *testing.T, root string, args []string, implement laneSubcommandRunner, plan laneSubcommandRunner, stdout, stderr io.Writer) int {
	t.Helper()
	stubLaneBriefLint(t, func([]string, io.Writer, io.Writer) int { return 0 })

	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.EnableWrites = true

	deps := laneDeps{
		loadConfig:       func(string) (config.Config, error) { return cfg, nil },
		runPlanExecute:   plan,
		runImplement:     implement,
		defaultPromptRun: func(config.Config, string, cmdruntime.RunOptions, io.Writer) (string, error) { return "", nil },
	}

	return runLaneMainWithDeps(args, stdout, stderr, deps)
}

func readRecoveryRecord(t *testing.T, path string) recoverycontract.FailureRecord {
	t.Helper()

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read recovery record: %v", err)
	}

	var record recoverycontract.FailureRecord

	if err := json.Unmarshal(body, &record); err != nil {
		t.Fatalf("decode recovery record: %v", err)
	}

	return record
}

func readRecoveryPacket(t *testing.T, path string) recoverycontract.DecompositionPacket {
	t.Helper()

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read recovery packet: %v", err)
	}

	var packet recoverycontract.DecompositionPacket

	if err := json.Unmarshal(body, &packet); err != nil {
		t.Fatalf("decode recovery packet: %v", err)
	}

	return packet
}

func assertEvidenceRef(t *testing.T, refs []recoverycontract.EvidenceRef, kind string, path string, id string) {
	t.Helper()

	for _, ref := range refs {
		if ref.Kind == kind && ref.Path == path && ref.ID == id {
			return
		}
	}

	t.Fatalf("missing evidence ref kind=%q path=%q id=%q in %#v", kind, path, id, refs)
}
