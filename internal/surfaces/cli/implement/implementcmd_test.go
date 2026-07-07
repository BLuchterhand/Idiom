package implement

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"idiom/internal/contracts/writerroute"
	"idiom/internal/evidence/telemetry"
	"idiom/internal/platform/config"
	"idiom/internal/platform/jsonevents/jsoneventstest"
	"idiom/internal/runtime/runner"
	cmdruntime "idiom/internal/surfaces/cli/runtime"
	"idiom/internal/surfaces/tui/app"
)

type implementFailingWriter struct {
	err error
}

func (w implementFailingWriter) Write([]byte) (int, error) {
	return 0, w.err
}

func TestRunImplementMainForcesImplementationTaskFamily(t *testing.T) {
	configPath := writeMainConfig(t)

	var (
		stdout, stderr bytes.Buffer
		gotPrompt      string
		gotOpts        cmdruntime.RunOptions
	)

	code := RunMainWithTelemetry(
		[]string{"-config", configPath, "-prompt", "modify exactly one file; do not run tests"},
		&stdout,
		&stderr,
		func(_ config.Config, prompt string, opts cmdruntime.RunOptions, _ io.Writer) (string, error) {
			gotPrompt = prompt
			gotOpts = opts

			return "done", nil
		},
		nil,
	)

	if code != 0 {
		t.Fatalf("expected exit 0, got %d stderr=%s", code, stderr.String())
	}

	if gotPrompt != "modify exactly one file; do not run tests" {
		t.Fatalf("prompt not forwarded: %q", gotPrompt)
	}

	if gotOpts.TaskFamily != "implementation" {
		t.Fatalf("TaskFamily = %q, want implementation", gotOpts.TaskFamily)
	}

	if gotOpts.PlannerOwnedVerification {
		t.Fatal("idiom implement should keep build/test verification available by default")
	}

	if strings.TrimSpace(stdout.String()) != "done" {
		t.Fatalf("stdout = %q, want done", stdout.String())
	}
}

func TestRunImplementMainCanDelegateBuildTestsToPlanner(t *testing.T) {
	configPath := writeMainConfig(t)

	var (
		stdout, stderr bytes.Buffer
		gotOpts        cmdruntime.RunOptions
	)

	code := RunMainWithTelemetry(
		[]string{"-config", configPath, "-prompt", "modify and verify", "-planner-owned-verification"},
		&stdout,
		&stderr,
		func(_ config.Config, _ string, opts cmdruntime.RunOptions, _ io.Writer) (string, error) {
			gotOpts = opts
			return "done", nil
		},
		nil,
	)

	if code != 0 {
		t.Fatalf("expected exit 0, got %d stderr=%s", code, stderr.String())
	}

	if !gotOpts.PlannerOwnedVerification {
		t.Fatal("--planner-owned-verification should mark build/test verification as caller-owned")
	}
}

func TestRunImplementMainCanDisableThinking(t *testing.T) {
	configPath := writeMainConfig(t)

	var (
		stdout, stderr bytes.Buffer
		gotOpts        cmdruntime.RunOptions
	)

	code := RunMainWithTelemetry(
		[]string{"-config", configPath, "-prompt", "write the brief", "-no-thinking"},
		&stdout,
		&stderr,
		func(_ config.Config, _ string, opts cmdruntime.RunOptions, _ io.Writer) (string, error) {
			gotOpts = opts
			return "done", nil
		},
		nil,
	)

	if code != 0 {
		t.Fatalf("expected exit 0, got %d stderr=%s", code, stderr.String())
	}

	if !gotOpts.DisableThinking {
		t.Fatal("--no-thinking should force request-level thinking off")
	}
}

func TestRunImplementMainAllowBuildTestsOverridesPlannerOwnedVerification(t *testing.T) {
	configPath := writeMainConfig(t)

	var (
		stdout, stderr bytes.Buffer
		gotOpts        cmdruntime.RunOptions
	)

	code := RunMainWithTelemetry(
		[]string{"-config", configPath, "-prompt", "modify and verify", "-planner-owned-verification", "-allow-build-tests"},
		&stdout,
		&stderr,
		func(_ config.Config, _ string, opts cmdruntime.RunOptions, _ io.Writer) (string, error) {
			gotOpts = opts
			return "done", nil
		},
		nil,
	)

	if code != 0 {
		t.Fatalf("expected exit 0, got %d stderr=%s", code, stderr.String())
	}

	if gotOpts.PlannerOwnedVerification {
		t.Fatal("--allow-build-tests should leave build/test verification available to the implementation lane")
	}
}

func TestRunImplementMainReadsPromptFileAndHonorsJSON(t *testing.T) {
	configPath := writeMainConfig(t)
	promptPath := filepath.Join(t.TempDir(), "brief.md")

	if err := os.WriteFile(promptPath, []byte("implement this\n"), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}

	var (
		stdout, stderr bytes.Buffer
		gotOpts        cmdruntime.RunOptions
	)

	code := RunMainWithTelemetry(
		[]string{"-config", configPath, "-prompt-file", promptPath, "-json", "-resume", "prior"},
		&stdout,
		&stderr,
		func(_ config.Config, prompt string, opts cmdruntime.RunOptions, w io.Writer) (string, error) {
			gotOpts = opts

			if prompt != "implement this" {
				t.Fatalf("prompt = %q, want implement this", prompt)
			}

			_, _ = w.Write([]byte("{\"type\":\"final\",\"content\":\"ok\"}\n"))

			return "ok", nil
		},
		nil,
	)

	if code != 0 {
		t.Fatalf("expected exit 0, got %d stderr=%s", code, stderr.String())
	}

	if !gotOpts.JSONEvents || gotOpts.Resume != "prior" || gotOpts.TaskFamily != "implementation" || gotOpts.PlannerOwnedVerification {
		t.Fatalf("unexpected opts: %#v", gotOpts)
	}

	if !strings.Contains(stdout.String(), `"type":"final"`) || strings.Contains(stdout.String(), "ok\n") && !strings.Contains(stdout.String(), `"content":"ok"`) {
		t.Fatalf("JSON mode should only emit runner JSONL, got %q", stdout.String())
	}

	writerRouteEventName, err := writerroute.CommandEventName(writerroute.CommandSurfaceImplement, writerroute.EventWriterRouteDecision)
	if err != nil {
		t.Fatalf("writer route event name: %v", err)
	}

	if !strings.Contains(stdout.String(), `"type":"`+writerRouteEventName+`"`) ||
		!strings.Contains(stdout.String(), `"writer_mode":"agentic_guarded"`) {

		t.Fatalf("implement JSON should report writer route decision, got %q", stdout.String())
	}

	events := jsoneventstest.DecodeLines(t, stdout.String())
	event, ok := jsoneventstest.FindByType(events, writerRouteEventName)

	if !ok {
		t.Fatalf("missing direct implement writer route event in %q", stdout.String())
	}

	if event["writer_mode"] != "agentic_guarded" || event["lane"] != "implement" || event["verification_owner"] != "executor" {
		t.Fatalf("unexpected direct implement route event: %#v", event)
	}

	if !jsonEventStringSliceContains(event["reason_codes"], "route.local_model_default") {
		t.Fatalf("direct implement route should explain local model default: %#v", event)
	}
}

func TestRunImplementMainReturnsJSONEventWriterError(t *testing.T) {
	configPath := writeMainConfig(t)
	want := errors.New("stdout closed")
	var stderr bytes.Buffer

	code := RunMainWithTelemetry(
		[]string{"-config", configPath, "-prompt", "implement this", "-json"},
		implementFailingWriter{err: want},
		&stderr,
		func(config.Config, string, cmdruntime.RunOptions, io.Writer) (string, error) {
			t.Fatal("prompt runner should not run after json event writer failure")
			return "", nil
		},
		nil,
	)

	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "implement: write json event") || !strings.Contains(stderr.String(), want.Error()) {
		t.Fatalf("stderr = %q, want actionable writer error", stderr.String())
	}
}

func TestRunImplementMainRejectsMissingPrompt(t *testing.T) {
	configPath := writeMainConfig(t)

	var stdout, stderr bytes.Buffer

	code := RunMainWithTelemetry(
		[]string{"-config", configPath},
		&stdout,
		&stderr,
		func(config.Config, string, cmdruntime.RunOptions, io.Writer) (string, error) {
			t.Fatal("prompt runner should not be called")

			return "", nil
		},
		nil,
	)

	if code != 2 || !strings.Contains(stderr.String(), "provide --prompt") {
		t.Fatalf("expected prompt validation failure, code=%d stderr=%s", code, stderr.String())
	}
}

func TestImplementTelemetryIncludesLocalOpsAndQueueMetadataOnly(t *testing.T) {
	configPath := writeMainConfig(t)
	telemetryDir := t.TempDir()

	rec, err := telemetry.Open(telemetry.Options{Dir: telemetryDir, Command: "implement", Args: []string{"implement", "--prompt", "SECRET raw prompt body"}, WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatalf("telemetry.Open: %v", err)
	}

	var stdout, stderr bytes.Buffer

	code := RunMainWithTelemetry(
		[]string{"-config", configPath, "-prompt", "modify a file", "-json"},
		&stdout,
		&stderr,
		func(_ config.Config, _ string, opts cmdruntime.RunOptions, _ io.Writer) (string, error) {
			if opts.Telemetry != rec {
				t.Fatal("implement runner should receive telemetry recorder")
			}

			return "done", nil
		},
		rec,
	)

	if code != 0 {
		t.Fatalf("expected exit 0, got %d stderr=%s", code, stderr.String())
	}

	if err := rec.Close(0); err != nil {
		t.Fatalf("rec.Close: %v", err)
	}

	records := readMainTelemetryRecords(t, onlyTelemetryFile(t, telemetryDir))
	start := findTelemetryRecord(records, "implement_start")

	if start == nil {
		t.Fatalf("missing implement_start telemetry: %#v", records)
	}

	localOps, ok := start["local_ops"].(map[string]any)
	if !ok || localOps["context_budget_state"] == "" || localOps["classifier_state"] == "" || localOps["backend_configured"] == nil {
		t.Fatalf("implement_start missing local ops evidence: %#v", start)
	}

	queue := findTelemetryRecord(records, "implement_queue")
	if queue == nil || queue["wait_ms"] == nil || queue["state"] == "" {
		t.Fatalf("missing implement queue telemetry: %#v", records)
	}

	rendered, _ := json.Marshal(records)
	for _, forbidden := range []string{"modify a file", "SECRET raw prompt body"} {
		if strings.Contains(string(rendered), forbidden) {
			t.Fatalf("telemetry leaked prompt content %q in %s", forbidden, rendered)
		}
	}

	if !strings.Contains(stdout.String(), `"type":"idiom.implement.queue_`) || strings.Contains(stdout.String(), "acquired_or_unavailable") {
		t.Fatalf("implement JSON stream should include queue event, got %q", stdout.String())
	}
}

func TestImplementQueueTelemetryReportsUnavailableAsDegraded(t *testing.T) {
	t.Setenv("IDIOM_QUEUE_POOL", "qwen-coder")
	t.Setenv("IDIOM_QUEUE_SOCKET", filepath.Join(t.TempDir(), "missing.sock"))

	rec, err := telemetry.Open(telemetry.Options{Dir: t.TempDir(), Command: "implement", WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatalf("telemetry.Open: %v", err)
	}

	var stdout, stderr bytes.Buffer

	queueResult := implementAcquireQueueSlot(config.Config{}, "session", &stdout, &stderr, true, rec)
	queueResult.Release()

	if err := rec.Close(0); err != nil {
		t.Fatalf("rec.Close: %v", err)
	}

	records := readMainTelemetryRecords(t, rec.Path())
	queue := findTelemetryRecord(records, "implement_queue")

	if queue == nil {
		t.Fatalf("missing implement queue telemetry: %#v", records)
	}

	if queue["state"] != "unavailable" || queue["degraded"] != true || queue["reason_class"] != "daemon_unreachable" {
		t.Fatalf("unavailable queue telemetry should be explicit and degraded, got %#v", queue)
	}

	if strings.Contains(stdout.String(), "queue_acquired") || !strings.Contains(stdout.String(), `"type":"idiom.implement.queue_unavailable"`) {
		t.Fatalf("unavailable queue JSON event should not report acquisition, got %q", stdout.String())
	}
}

func TestImplementQueueTelemetryReportsRealAcquire(t *testing.T) {
	t.Setenv("IDIOM_QUEUE_POOL", "qwen-coder")

	sock := filepath.Join("/tmp", "idiom-"+strings.NewReplacer("/", "_").Replace(t.Name())+".sock")

	_ = os.Remove(sock)

	defer os.Remove(sock)

	srv := runner.NewServer(runner.Config{Pools: map[string]runner.PoolConfig{"qwen-coder": {MaxInFlight: 1}}}, sock)
	if err := srv.Start(); err != nil {
		t.Fatalf("start queue server: %v", err)
	}

	defer srv.Stop()

	t.Setenv("IDIOM_QUEUE_SOCKET", sock)

	rec, err := telemetry.Open(telemetry.Options{Dir: t.TempDir(), Command: "implement", WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatalf("telemetry.Open: %v", err)
	}

	var stdout, stderr bytes.Buffer

	queueResult := implementAcquireQueueSlot(config.Config{}, "session", &stdout, &stderr, true, rec)
	queueResult.Release()

	if err := rec.Close(0); err != nil {
		t.Fatalf("rec.Close: %v", err)
	}

	records := readMainTelemetryRecords(t, rec.Path())
	queue := findTelemetryRecord(records, "implement_queue")

	if queue == nil {
		t.Fatalf("missing implement queue telemetry: %#v", records)
	}

	if queue["state"] != "acquired" || queue["degraded"] != false {
		t.Fatalf("real queue acquire should report acquired/non-degraded, got %#v", queue)
	}

	if !strings.Contains(stdout.String(), `"type":"idiom.implement.queue_acquired"`) {
		t.Fatalf("acquired queue JSON event missing, got %q", stdout.String())
	}
}

func TestModelCloseoutWritesImplementTelemetryAndOneSessionSummary(t *testing.T) {
	root := t.TempDir()
	cfg := config.Config{
		AppName: "Idiom",
		Chat: config.Chat{
			DebugLogDir:           filepath.Join(root, "logs", "conversations"),
			RequestTimeoutSeconds: 1,
		},
		Workspace: config.Workspace{Root: root, CWD: root},
	}

	rec, err := telemetry.Open(telemetry.Options{Dir: filepath.Join(root, "telemetry"), Command: "implement", WorkspaceRoot: root})
	if err != nil {
		t.Fatalf("telemetry.Open: %v", err)
	}

	model := app.New(cfg)
	closeout := cmdruntime.ModelCloseout(rec, model, "prompt", "implementation")

	_ = rec.RegisterCloseout(closeout)

	if err := rec.CloseWithFields(143, map[string]any{"reason": "signal"}); err != nil {
		t.Fatalf("rec.CloseWithFields: %v", err)
	}

	if err := rec.Close(0); err != nil {
		t.Fatalf("second rec.Close: %v", err)
	}

	summaries := readAppSessionSummaryRecordsForMain(t, model.DebugLogPath())
	if len(summaries) != 1 {
		t.Fatalf("expected exactly one session_summary, got %d in %#v", len(summaries), summaries)
	}

	records := readMainTelemetryRecords(t, onlyTelemetryFile(t, filepath.Join(root, "telemetry")))
	closeouts := 0
	modelCloseouts := 0

	for _, record := range records {
		if record["type"] == "model_session_closeout" {
			modelCloseouts++

			if record["summary_event"] != "session_summary" || record["session_id"] == "" || record["model_closed"] != true {
				t.Fatalf("model closeout should carry completed session summary metadata: %#v", record)
			}
		}

		if record["type"] == "implement_session_closeout" {
			closeouts++

			if record["summary_event"] != "session_summary" || record["session_id"] == "" {
				t.Fatalf("closeout should carry metadata-only session summary fields: %#v", record)
			}
		}
	}

	if closeouts != 1 {
		t.Fatalf("expected one implement_session_closeout, got %d in %#v", closeouts, records)
	}

	if modelCloseouts != 1 {
		t.Fatalf("expected one model_session_closeout, got %d in %#v", modelCloseouts, records)
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

func findTelemetryRecord(records []map[string]any, eventType string) map[string]any {
	for _, record := range records {
		if record["type"] == eventType {
			return record
		}
	}

	return nil
}

func readAppSessionSummaryRecordsForMain(t *testing.T, path string) []map[string]any {
	t.Helper()

	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatalf("read debug log: %v", err)
	}

	var summaries []map[string]any

	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}

		var record map[string]any

		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("unmarshal %q: %v", line, err)
		}

		if record["type"] == "session_summary" {
			summaries = append(summaries, record)
		}
	}

	return summaries
}
