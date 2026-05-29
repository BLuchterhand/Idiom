package localops
import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"idiom/internal/localops"
	"idiom/internal/telemetry"
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
	if report.SchemaVersion != localops.BenchmarkSchemaVersion {
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

func TestLocalOpsBenchmarkQuickstartSmokeRecordsP010ActivationEvidence(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "AcmeCustomer-private-workspace")
	if err := os.MkdirAll(filepath.Join(root, "pkg"), 0o755); err != nil {
		t.Fatalf("stage root: %v", err)
	}
	t.Chdir(root)
	stageDoctorConfigWithBudget(t, root, 16384, 1024)
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/quickstart\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "pkg", "quickstart.go"), []byte("package pkg\n\nfunc QuickstartValue() string { return \"ok\" }\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	report, err := buildLocalOpsBenchmarkReport(localOpsBenchmarkOptions{
		ConfigPath:        filepath.Join(root, ".idiom", "config.json"),
		Profile:           "sidecar",
		QuickstartSmoke:   true,
		QuickstartTask:    "localize quickstart smoke value",
		QuickstartCleanup: "remove-local-state",
	}, nil)
	if err != nil {
		t.Fatalf("build local-ops report: %v", err)
	}
	if report.Quickstart == nil {
		t.Fatalf("expected quickstart smoke evidence")
	}
	smoke := report.Quickstart
	if smoke.Status != "pass" {
		t.Fatalf("quickstart smoke should pass in clean fixture, got %#v", smoke)
	}
	if smoke.SetupInstallReadiness.Status != "pass" ||
		smoke.FirstUsefulPacket.PacketID == "" ||
		smoke.FirstUsefulPacket.IndexID == "" ||
		smoke.FirstUsefulPacket.Task.TaskID == "" ||
		smoke.FirstUsefulPacket.Task.RawTaskCaptured ||
		smoke.FreshnessCheck.State == "" ||
		smoke.AuditReport.AuditReportID == "" ||
		len(smoke.SuggestedVerification.SuggestedCommands) == 0 ||
		smoke.ProductEvidenceSafeOutput.ForbiddenFieldCheck != "pass" ||
		smoke.FailureReason.Status != "none" ||
		smoke.DocsGap.Status != "none" ||
		smoke.PrivacyRedactionStatus.Status != "pass" ||
		smoke.Cleanup.Status != "pass" {
		t.Fatalf("quickstart smoke missing P0.10 evidence fields: %#v", smoke)
	}
	if smoke.TimeToFirstPacketSeconds <= 0 || smoke.TimeToTrustEvidenceSeconds < smoke.TimeToFirstPacketSeconds {
		t.Fatalf("quickstart smoke should expose deterministic duration fields, got first=%v trust=%v", smoke.TimeToFirstPacketSeconds, smoke.TimeToTrustEvidenceSeconds)
	}
	if _, err := os.Stat(filepath.Join(root, ".idiom", "comprehension")); !os.IsNotExist(err) {
		t.Fatalf("remove-local-state cleanup should remove comprehension state, stat err=%v", err)
	}
	rendered := mustMarshalLocalOpsReport(t, report)
	for _, forbidden := range []string{
		root,
		parent,
		"AcmeCustomer",
		"private-workspace",
		"SECRET raw prompt body",
		"func QuickstartValue",
	} {
		if strings.Contains(rendered, forbidden) {
			t.Fatalf("quickstart smoke leaked forbidden content %q in %s", forbidden, rendered)
		}
	}
}

func TestLocalOpsBenchmarkQuickstartSmokeRedactsSensitiveTaskEvidence(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	stageDoctorConfigWithBudget(t, root, 16384, 1024)
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/sensitivetask\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc QuickstartSensitiveTaskFixture() string { return \"ok\" }\n"), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}

	report, err := buildLocalOpsBenchmarkReport(localOpsBenchmarkOptions{
		ConfigPath:        filepath.Join(root, ".idiom", "config.json"),
		Profile:           "sidecar",
		QuickstartSmoke:   true,
		QuickstartTask:    "QuickstartSensitiveTaskFixture /Users/ben/Documents/GitHub/CustomerX-private-repo secret abc123",
		QuickstartCleanup: "remove-local-state",
	}, nil)
	if err != nil {
		t.Fatalf("build local-ops report: %v", err)
	}
	if report.Quickstart == nil {
		t.Fatalf("expected quickstart smoke evidence")
	}
	smoke := report.Quickstart
	if smoke.Status != "pass" {
		t.Fatalf("redacted quickstart smoke should pass, got %#v", smoke)
	}
	if smoke.FirstUsefulPacket.Task.RawTaskCaptured ||
		smoke.FirstUsefulPacket.Task.TaskID == "" ||
		smoke.FirstUsefulPacket.Task.EvidenceType != "hash_and_classification" ||
		smoke.FirstUsefulPacket.Task.RedactionStatus != "redacted" {
		t.Fatalf("quickstart task evidence should be hash/classification only, got %#v", smoke.FirstUsefulPacket.Task)
	}
	for _, class := range []string{"absolute_path", "secret_marker", "sensitive_identifier"} {
		if !slices.Contains(smoke.FirstUsefulPacket.Task.ContentClasses, class) {
			t.Fatalf("task evidence missing content class %q: %#v", class, smoke.FirstUsefulPacket.Task)
		}
	}
	if smoke.ProductEvidenceSafeOutput.ForbiddenFieldCheck != "pass" ||
		smoke.ProductEvidenceSafeOutput.RedactionStatus != "redacted" ||
		smoke.PrivacyRedactionStatus.Status != "pass" ||
		smoke.PrivacyRedactionStatus.ForbiddenFieldCheck != "pass" ||
		smoke.PrivacyRedactionStatus.SecretCaptured ||
		smoke.PrivacyRedactionStatus.PrivatePathCaptured {
		t.Fatalf("privacy/product evidence status should reconcile with redacted task evidence: %#v", smoke)
	}
	rendered := mustMarshalLocalOpsReport(t, report)
	for _, forbidden := range []string{"/Users/", "CustomerX", "secret abc123"} {
		if strings.Contains(rendered, forbidden) {
			t.Fatalf("quickstart product evidence leaked %q in %s", forbidden, rendered)
		}
	}
	if finding := localops.ForbiddenEvidenceLeak(rendered); finding != "" {
		t.Fatalf("redacted quickstart evidence should pass forbidden-field scan, got %q in %s", finding, rendered)
	}
}

func TestLocalOpsForbiddenEvidenceLeakScansValuesWithoutFailingAllowedFieldNames(t *testing.T) {
	allowed := `{"privacy_redaction_status":{"secret_captured":false,"private_path_captured":false},"redacted_fields":["secret_value"]}`
	if finding := localops.ForbiddenEvidenceLeak(allowed); finding != "" {
		t.Fatalf("allowed privacy field names should not fail forbidden scan: %s", finding)
	}
	for name, body := range map[string]string{
		"absolute_path":       `{"task":"/Users/ben/Documents/GitHub/repo"}`,
		"customer_identifier": `{"task":"CustomerX-private-repo"}`,
		"secret_value":        `{"task":"secret abc123"}`,
		"true_secret_flag":    `{"privacy_redaction_status":{"secret_captured":true}}`,
	} {
		if finding := localops.ForbiddenEvidenceLeak(body); finding == "" {
			t.Fatalf("expected forbidden scan to catch %s in %s", name, body)
		}
	}
}

func TestRunLocalOpsBenchmarkQuickstartSmokeJSON(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	stageDoctorConfigWithBudget(t, root, 4096, 512)
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/cliquickstart\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := RunMainWithTelemetry([]string{
		"benchmark",
		"--json",
		"--config", filepath.Join(root, ".idiom", "config.json"),
		"--profile", "sidecar",
		"--quickstart-smoke",
		"--quickstart-cleanup", "remove-smoke-artifacts",
	}, &stdout, &stderr, nil)
	if code != 0 {
		t.Fatalf("local-ops quickstart smoke failed code=%d stderr=%s", code, stderr.String())
	}
	var report localops.BenchmarkReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("stdout JSON not parseable: %v\n%s", err, stdout.String())
	}
	if report.Quickstart == nil || report.Quickstart.FirstUsefulPacket.PacketID == "" {
		t.Fatalf("quickstart smoke JSON missing packet evidence: %#v", report.Quickstart)
	}
	if report.Quickstart.Cleanup.Status != "pass" || report.Quickstart.Cleanup.RemovedArtifactCount != 1 {
		t.Fatalf("quickstart smoke should record cleanup evidence: %#v", report.Quickstart.Cleanup)
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
	replayPath := filepath.Join(root, "AcmeCustomer-secret-session.jsonl")
	telemetryDir := t.TempDir()
	t.Setenv("IDIOM_TELEMETRY_DIR", telemetryDir)

	var stdout, stderr bytes.Buffer
	rec, err := telemetry.Open(telemetry.Options{Dir: telemetryDir, Command: "local-ops"})
	if err != nil {
		t.Fatalf("open telemetry: %v", err)
	}
	code := RunMainWithTelemetry([]string{
		"benchmark",
		"--json",
		"--config", filepath.Join(root, ".idiom", "config.json"),
		"--profile", "implement",
		"--replay-log", replayPath,
	}, &stdout, &stderr, rec)
	if rec != nil {
		if err := rec.Close(code); err != nil {
			t.Fatalf("close telemetry: %v", err)
		}
	}
	if code != 0 {
		t.Fatalf("missing replay log should degrade report, got code=%d stderr=%s", code, stderr.String())
	}
	var got localops.BenchmarkReport
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
		"open " + root,
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
	code := RunMainWithTelemetry([]string{
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
	code := RunMainWithTelemetry([]string{"benchmark", "--json", "--config", filepath.Join(root, ".idiom", "config.json"), "--profile", "implement", "--output", outputPath}, &stdout, &stderr, nil)
	if code != 0 {
		t.Fatalf("local-ops benchmark failed code=%d stderr=%s", code, stderr.String())
	}
	var got localops.BenchmarkReport
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

func mustMarshalLocalOpsReport(t *testing.T, report localops.BenchmarkReport) string {
	t.Helper()
	body, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	return string(body)
}

func stageDoctorConfigWithBudget(t *testing.T, root string, ctxSize int, maxTokens int) {
	t.Helper()
	cfg := filepath.Join(root, ".idiom", "config.json")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	body := fmt.Sprintf(`{
  "workspace": {"root": %q},
  "llama_server": {"ctx_size": %d},
  "chat": {"max_tokens": %d}
}`, root, ctxSize, maxTokens)
	if err := os.WriteFile(cfg, []byte(body), 0o644); err != nil {
		t.Fatalf("stage config: %v", err)
	}
}

func stageDoctorBackends(t *testing.T, root string) {
	t.Helper()
	path := filepath.Join(root, ".idiom", "backends.json")
	body := `{
  "default_profile": "local",
  "profiles": {
    "local": {
      "models": [
        {"alias": "planner", "binary": "mlx_lm.server", "model_path": "/models/planner", "port": 47780, "roles": ["planner", "classifier"]},
        {"alias": "coder", "binary": "mlx_lm.server", "model_path": "/models/coder", "port": 47781, "roles": ["executor"]}
      ],
      "proxy": {
        "default_planner": "planner",
        "default_executor": "coder",
        "default_classifier": "planner"
      }
    }
  }
}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("stage backends: %v", err)
	}
}

func onlyTelemetryFile(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read telemetry dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one telemetry file, got %d", len(entries))
	}
	return filepath.Join(dir, entries[0].Name())
}
