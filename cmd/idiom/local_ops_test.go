package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalOpsBenchmarkReportsSmokeCapacityReplayBlockers(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	stageDoctorConfigWithBudget(t, root, 16384, 512)

	report, err := buildLocalOpsBenchmarkReport(localOpsBenchmarkOptions{
		ConfigPath: filepath.Join(root, ".idiom", "config.json"),
		Profile:    "eval",
	}, nil)
	if err != nil {
		t.Fatalf("build local-ops report: %v", err)
	}
	if report.SchemaVersion != localOpsBenchmarkSchemaVersion {
		t.Fatalf("unexpected schema version: %#v", report)
	}
	if len(report.Profiles) != 1 || report.Profiles[0].Name != "eval" {
		t.Fatalf("expected eval profile report, got %#v", report.Profiles)
	}
	profile := report.Profiles[0]
	if profile.Smoke.Status != "blocked" {
		t.Fatalf("eval smoke should block when backend/queue prerequisites are absent, got %#v", profile.Smoke)
	}
	if profile.Capacity.ContextTokens != 16384 || profile.Capacity.MaxOutputTokens <= 0 || profile.Capacity.PromptBudgetTokens == 0 {
		t.Fatalf("capacity should expose bounded context assumptions, got %#v", profile.Capacity)
	}
	if report.Replay.Status != "unsupported" || len(report.Replay.Blockers) == 0 {
		t.Fatalf("missing replay logs should be an explicit unsupported blocker, got %#v", report.Replay)
	}
	if !report.Privacy.MetadataOnly || report.Privacy.RawPromptCaptured || report.Privacy.RawFileBodyCaptured || report.Privacy.SecretCaptureAllowed {
		t.Fatalf("local-ops report must remain metadata-only, got %#v", report.Privacy)
	}
	rendered := mustMarshalLocalOpsReport(t, report)
	for _, forbidden := range []string{"SECRET", "raw prompt body", "file body"} {
		if strings.Contains(rendered, forbidden) {
			t.Fatalf("local-ops report leaked raw content %q in %s", forbidden, rendered)
		}
	}
}

func TestLocalOpsBenchmarkSummarizesReplayFixtureWithoutRawPrompt(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	stageDoctorConfigWithBudget(t, root, 16384, 1024)
	stageDoctorBackends(t, root)
	logPath := filepath.Join(root, "conversation.jsonl")
	body := strings.Join([]string{
		`{"type":"message","role":"user","content":"SECRET raw prompt body"}`,
		`{"type":"model_call","prompt_tokens":120,"completion_tokens":30}`,
		`{"type":"tool_result","tool":"read_file","summary":"metadata only"}`,
		`{"type":"queue_admission","state":"unavailable","degraded":true,"wait_ms":7}`,
		`{"type":"doctor_readiness_profile","state":"degraded","backend_state":"pass","queue_state":"unavailable","context_budget_state":"pass","route_classifier_state":"degraded","degraded":true}`,
		`{"type":"turn_summary","stop_reason":"budget_exhausted","duration_ms":2500}`,
		`{"type":"session_summary","stop_reason":"budget_exhausted","duration_ms":2500}`,
	}, "\n") + "\n"
	if err := os.WriteFile(logPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write replay fixture: %v", err)
	}

	report, err := buildLocalOpsBenchmarkReport(localOpsBenchmarkOptions{
		ConfigPath: filepath.Join(root, ".idiom", "config.json"),
		Profile:    "implement",
		ReplayLog:  logPath,
	}, nil)
	if err != nil {
		t.Fatalf("build local-ops report: %v", err)
	}
	if report.Replay.Status != "pass" || report.Replay.LogCount != 1 {
		t.Fatalf("expected replay summary pass for fixture, got %#v", report.Replay)
	}
	if report.Replay.Aggregate == nil || report.Replay.Aggregate.LogCount != 1 {
		t.Fatalf("replay aggregate missing: %#v", report.Replay.Aggregate)
	}
	if report.Replay.Signals.QueueDegraded == 0 || report.Replay.Signals.BudgetExhausted == 0 {
		t.Fatalf("expected replay queue/budget signals, got %#v", report.Replay.Signals)
	}
	rendered := mustMarshalLocalOpsReport(t, report)
	for _, forbidden := range []string{"SECRET raw prompt body", "log_path"} {
		if strings.Contains(rendered, forbidden) {
			t.Fatalf("replay report leaked %q: %s", forbidden, rendered)
		}
	}
}

func TestLocalOpsBenchmarkRedactsReplayPathFromErrorsAndTelemetry(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	stageDoctorConfigWithBudget(t, root, 16384, 1024)
	stageDoctorBackends(t, root)
	replayPath := "/tmp/AcmeCustomer-secret-session.jsonl"
	telemetryDir := t.TempDir()
	t.Setenv("IDIOM_TELEMETRY_DIR", telemetryDir)

	var stdout, stderr bytes.Buffer
	code := runMain([]string{
		"local-ops",
		"benchmark",
		"--json",
		"--config", filepath.Join(root, ".idiom", "config.json"),
		"--profile", "implement",
		"--replay-log", replayPath,
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("missing replay log should degrade report, got code=%d stderr=%s", code, stderr.String())
	}
	var got localOpsBenchmarkReport
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("stdout JSON not parseable: %v\n%s", err, stdout.String())
	}
	if got.Replay.Status != "blocked" || got.Replay.LogCount != 0 || len(got.Replay.Blockers) == 0 {
		t.Fatalf("expected sanitized replay blocker, got %#v", got.Replay)
	}

	telemetryBody, err := os.ReadFile(onlyTelemetryFile(t, telemetryDir))
	if err != nil {
		t.Fatalf("read telemetry: %v", err)
	}
	combined := strings.Join([]string{stdout.String(), stderr.String(), string(telemetryBody)}, "\n")
	for _, forbidden := range []string{
		replayPath,
		filepath.Base(replayPath),
		"AcmeCustomer",
		"secret-session",
		"open /tmp",
	} {
		if strings.Contains(combined, forbidden) {
			t.Fatalf("local-ops replay output leaked %q in %s", forbidden, combined)
		}
	}
	if !strings.Contains(stdout.String(), "replay_log_unreadable") {
		t.Fatalf("expected sanitized replay reason code, got %s", stdout.String())
	}
}

func TestRunLocalOpsBenchmarkInvalidProfileExitsTwo(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	stageDoctorConfigWithBudget(t, root, 4096, 512)

	var stdout, stderr bytes.Buffer
	code := runLocalOpsMainWithTelemetry([]string{
		"benchmark",
		"--config", filepath.Join(root, ".idiom", "config.json"),
		"--profile", "nope",
	}, &stdout, &stderr, nil)
	if code != 2 {
		t.Fatalf("invalid profile must exit 2, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "unknown readiness profile") {
		t.Fatalf("expected profile selection error, got %q", stderr.String())
	}
}

func TestRunLocalOpsBenchmarkCommandWritesJSONOutput(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	stageDoctorConfigWithBudget(t, root, 4096, 512)
	outputPath := filepath.Join(root, ".idiom", "local-ops", "report.json")

	var stdout, stderr bytes.Buffer
	code := runLocalOpsMainWithTelemetry([]string{"benchmark", "--json", "--config", filepath.Join(root, ".idiom", "config.json"), "--profile", "implement", "--output", outputPath}, &stdout, &stderr, nil)
	if code != 0 {
		t.Fatalf("local-ops benchmark failed code=%d stderr=%s", code, stderr.String())
	}
	var got localOpsBenchmarkReport
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("stdout JSON not parseable: %v\n%s", err, stdout.String())
	}
	if got.Summary.ProfileCount != 1 || got.Profiles[0].Name != "implement" {
		t.Fatalf("unexpected JSON report: %#v", got)
	}
	if _, err := os.Stat(outputPath); err != nil {
		t.Fatalf("expected report output file: %v", err)
	}
}

func mustMarshalLocalOpsReport(t *testing.T, report localOpsBenchmarkReport) string {
	t.Helper()
	body, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	return string(body)
}
