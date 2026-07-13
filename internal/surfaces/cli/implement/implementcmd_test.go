package implement

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"idiom/internal/contracts/autonomy"
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

var _ = ginkgo.Describe("implementcmd", func() {
	ginkgo.It("run implement main forces implementation task family", func() {
		configPath := writeMainConfig()

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
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("expected exit 0, got %d stderr=%s", code, stderr.String()))
		gomega.Expect(gotPrompt).To(gomega.Equal("modify exactly one file; do not run tests"), fmt.Sprintf("prompt not forwarded: %q", gotPrompt))
		gomega.Expect(gotOpts.TaskFamily).To(gomega.Equal(string(autonomy.TaskFamilyImplementation)), fmt.Sprintf("TaskFamily = %q, want implementation", gotOpts.TaskFamily))
		gomega.Expect(gotOpts.PlannerOwnedVerification).To(gomega.BeFalse(), "idiom implement should keep build/test verification available by default")
		gomega.Expect(strings.TrimSpace(stdout.String())).To(gomega.Equal("done"), fmt.Sprintf("stdout = %q, want done", stdout.String()))

	})
	ginkgo.It("run implement main can delegate build tests to planner", func() {
		configPath := writeMainConfig()

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
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("expected exit 0, got %d stderr=%s", code, stderr.String()))
		gomega.Expect(gotOpts.PlannerOwnedVerification).To(gomega.BeTrue(), "--planner-owned-verification should mark build/test verification as caller-owned")

	})
	ginkgo.It("run implement main can disable thinking", func() {
		configPath := writeMainConfig()

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
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("expected exit 0, got %d stderr=%s", code, stderr.String()))
		gomega.Expect(gotOpts.DisableThinking).To(gomega.BeTrue(), "--no-thinking should force request-level thinking off")

	})
	ginkgo.It("run implement main reads prompt file and honors json", func() {
		configPath := writeMainConfig()
		promptPath := filepath.Join(ginkgo.GinkgoT().TempDir(), "brief.md")
		gomega.Expect(os.WriteFile(promptPath, []byte("implement this\n"), 0o644)).To(gomega.Succeed())

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
				gomega.Expect(prompt).To(gomega.Equal("implement this"), fmt.Sprintf("prompt = %q, want implement this", prompt))

				_, _ = w.Write([]byte("{\"type\":\"final\",\"content\":\"ok\"}\n"))

				return "ok", nil
			},
			nil,
		)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("expected exit 0, got %d stderr=%s", code, stderr.String()))
		gomega.Expect(gotOpts.JSONEvents).To(gomega.BeTrue(), fmt.Sprintf("unexpected opts: %#v", gotOpts))
		gomega.Expect(gotOpts.Resume).To(gomega.Equal("prior"), fmt.Sprintf("unexpected opts: %#v", gotOpts))
		gomega.Expect(gotOpts.TaskFamily).To(gomega.Equal(string(autonomy.TaskFamilyImplementation)), fmt.Sprintf("unexpected opts: %#v", gotOpts))
		gomega.Expect(gotOpts.PlannerOwnedVerification).To(gomega.BeFalse(), fmt.Sprintf("unexpected opts: %#v", gotOpts))
		gomega.Expect(stdout.String()).To(gomega.ContainSubstring(`"type":"final"`), fmt.Sprintf("JSON mode should only emit runner JSONL, got %q", stdout.String()))
		gomega.Expect(stdout.String()).To(gomega.ContainSubstring(`"content":"ok"`), fmt.Sprintf("JSON mode should only emit runner JSONL, got %q", stdout.String()))
		gomega.Expect(stdout.String()).NotTo(gomega.ContainSubstring("ok\n"), fmt.Sprintf("JSON mode should only emit runner JSONL, got %q", stdout.String()))

		writerRouteEventName, err := writerroute.CommandEventName(writerroute.CommandSurfaceImplement, writerroute.EventWriterRouteDecision)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("writer route event name: %v", err))
		gomega.Expect(stdout.String()).To(gomega.ContainSubstring(`"type":"`+writerRouteEventName+`"`), fmt.Sprintf("implement JSON should report writer route decision, got %q", stdout.String()))
		gomega.Expect(stdout.String()).To(gomega.ContainSubstring(`"writer_mode":"agentic_guarded"`), fmt.Sprintf("implement JSON should report writer route decision, got %q", stdout.String()))

		events := jsoneventstest.DecodeLines(ginkgo.GinkgoT(), stdout.String())
		event, ok := jsoneventstest.FindByType(events, writerRouteEventName)
		gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("missing direct implement writer route event in %q", stdout.String()))
		gomega.Expect(event["writer_mode"]).To(gomega.Equal("agentic_guarded"), fmt.Sprintf("unexpected direct implement route event: %#v", event))
		gomega.Expect(event["lane"]).To(gomega.Equal("implement"), fmt.Sprintf("unexpected direct implement route event: %#v", event))
		gomega.Expect(event["verification_owner"]).To(gomega.Equal("executor"), fmt.Sprintf("unexpected direct implement route event: %#v", event))
		gomega.Expect(jsonEventStringSliceContains(event["reason_codes"], "route.local_model_default")).To(gomega.BeTrue(), fmt.Sprintf("direct implement route should explain local model default: %#v", event))

	})
	ginkgo.It("run implement main returns json event writer error", func() {
		configPath := writeMainConfig()
		want := errors.New("stdout closed")
		var stderr bytes.Buffer

		code := RunMainWithTelemetry(
			[]string{"-config", configPath, "-prompt", "implement this", "-json"},
			implementFailingWriter{err: want},
			&stderr,
			func(config.Config, string, cmdruntime.RunOptions, io.Writer) (string, error) {
				ginkgo.Fail("prompt runner should not run after json event writer failure")
				return "", nil
			},
			nil,
		)
		gomega.Expect(code).To(gomega.Equal(1), fmt.Sprintf("exit code = %d, want 1", code))
		gomega.Expect(stderr.String()).To(gomega.ContainSubstring("implement: write json event"), fmt.Sprintf("stderr = %q, want actionable writer error", stderr.String()))
		gomega.Expect(stderr.String()).To(gomega.ContainSubstring(want.Error()), fmt.Sprintf("stderr = %q, want actionable writer error", stderr.String()))

	})
	ginkgo.It("run implement main rejects missing prompt", func() {
		configPath := writeMainConfig()

		var stdout, stderr bytes.Buffer

		code := RunMainWithTelemetry(
			[]string{"-config", configPath},
			&stdout,
			&stderr,
			func(config.Config, string, cmdruntime.RunOptions, io.Writer) (string, error) {
				ginkgo.Fail("prompt runner should not be called")

				return "", nil
			},
			nil,
		)
		gomega.Expect(code).To(gomega.Equal(2), fmt.Sprintf("expected prompt validation failure, code=%d stderr=%s", code, stderr.String()))
		gomega.Expect(stderr.String()).To(gomega.ContainSubstring("provide --prompt"), fmt.Sprintf("expected prompt validation failure, code=%d stderr=%s", code, stderr.String()))

	})
	ginkgo.It("implement telemetry includes local ops and queue metadata only", func() {
		configPath := writeMainConfig()
		telemetryDir := ginkgo.GinkgoT().TempDir()

		rec, err := telemetry.Open(telemetry.Options{Dir: telemetryDir, Command: "implement", Args: []string{"implement", "--prompt", "SECRET raw prompt body"}, WorkspaceRoot: "/workspace"})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("telemetry.Open: %v", err))

		var stdout, stderr bytes.Buffer

		code := RunMainWithTelemetry(
			[]string{"-config", configPath, "-prompt", "modify a file", "-json"},
			&stdout,
			&stderr,
			func(_ config.Config, _ string, opts cmdruntime.RunOptions, _ io.Writer) (string, error) {
				gomega.Expect(opts.Telemetry).To(gomega.Equal(rec), "implement runner should receive telemetry recorder")

				return "done", nil
			},
			rec,
		)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("expected exit 0, got %d stderr=%s", code, stderr.String()))
		gomega.Expect(rec.CloseWithFields(0, nil)).To(gomega.Succeed())

		records := readMainTelemetryRecords(onlyTelemetryFile(telemetryDir))
		start := findTelemetryRecord(records, "implement_start")
		gomega.Expect(start).NotTo(gomega.Equal(nil), fmt.Sprintf("missing implement_start telemetry: %#v", records))

		localOps, ok := start["local_ops"].(map[string]any)
		gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("implement_start missing local ops evidence: %#v", start))
		gomega.Expect(localOps["context_budget_state"]).NotTo(gomega.BeEmpty(), fmt.Sprintf("implement_start missing local ops evidence: %#v", start))
		gomega.Expect(localOps["classifier_state"]).NotTo(gomega.BeEmpty(), fmt.Sprintf("implement_start missing local ops evidence: %#v", start))
		gomega.Expect(localOps["backend_configured"]).NotTo(gomega.BeNil(), fmt.Sprintf("implement_start missing local ops evidence: %#v", start))

		queue := findTelemetryRecord(records, "implement_queue")
		gomega.Expect(queue).NotTo(gomega.BeNil(), fmt.Sprintf("missing implement queue telemetry: %#v", records))
		gomega.Expect(queue["wait_ms"]).NotTo(gomega.BeNil(), fmt.Sprintf("missing implement queue telemetry: %#v", records))
		gomega.Expect(queue["state"]).NotTo(gomega.BeEmpty(), fmt.Sprintf("missing implement queue telemetry: %#v", records))

		rendered, _ := json.Marshal(records)
		for _, forbidden := range []string{"modify a file", "SECRET raw prompt body"} {
			gomega.Expect(string(rendered)).NotTo(gomega.ContainSubstring(forbidden), fmt.Sprintf("telemetry leaked prompt content %q in %s", forbidden, rendered))

		}
		gomega.Expect(stdout.String()).To(gomega.ContainSubstring(`"type":"idiom.implement.queue_`), fmt.Sprintf("implement JSON stream should include queue event, got %q", stdout.String()))
		gomega.Expect(stdout.String()).NotTo(gomega.ContainSubstring("acquired_or_unavailable"), fmt.Sprintf("implement JSON stream should include queue event, got %q", stdout.String()))

	})
	ginkgo.It("implement queue telemetry reports unavailable as degraded", func() {
		ginkgo.GinkgoT().
			Setenv("IDIOM_QUEUE_POOL", "qwen-coder")
		ginkgo.GinkgoT().
			Setenv("IDIOM_QUEUE_SOCKET", filepath.Join(ginkgo.GinkgoT().TempDir(), "missing.sock"))

		rec, err := telemetry.Open(telemetry.Options{Dir: ginkgo.GinkgoT().TempDir(), Command: "implement", WorkspaceRoot: "/workspace"})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("telemetry.Open: %v", err))

		var stdout, stderr bytes.Buffer

		queueResult := implementAcquireQueueSlot(config.Config{}, "session", &stdout, &stderr, true, rec)
		queueResult.Release()
		gomega.Expect(rec.CloseWithFields(0, nil)).To(gomega.Succeed())

		records := readMainTelemetryRecords(rec.Path())
		queue := findTelemetryRecord(records, "implement_queue")
		gomega.Expect(queue).NotTo(gomega.Equal(nil), fmt.Sprintf("missing implement queue telemetry: %#v", records))
		gomega.Expect(queue["state"]).To(gomega.Equal("unavailable"), fmt.Sprintf("unavailable queue telemetry should be explicit and degraded, got %#v", queue))
		gomega.Expect(queue["degraded"]).To(gomega.Equal(true), fmt.Sprintf("unavailable queue telemetry should be explicit and degraded, got %#v", queue))
		gomega.Expect(queue["reason_class"]).To(gomega.Equal("daemon_unreachable"), fmt.Sprintf("unavailable queue telemetry should be explicit and degraded, got %#v", queue))
		gomega.Expect(stdout.String()).NotTo(gomega.ContainSubstring("queue_acquired"), fmt.Sprintf("unavailable queue JSON event should not report acquisition, got %q", stdout.String()))
		gomega.Expect(stdout.String()).To(gomega.ContainSubstring(`"type":"idiom.implement.queue_unavailable"`), fmt.Sprintf("unavailable queue JSON event should not report acquisition, got %q", stdout.String()))

	})
	ginkgo.It("implement queue telemetry reports real acquire", func() {
		ginkgo.GinkgoT().
			Setenv("IDIOM_QUEUE_POOL", "qwen-coder")

		sock := filepath.Join("/tmp", "idiom-"+strings.NewReplacer("/", "_").Replace(ginkgo.GinkgoT().Name())+".sock")

		_ = os.Remove(sock)

		defer os.Remove(sock)

		srv := runner.NewServer(runner.Config{Pools: map[string]runner.PoolConfig{"qwen-coder": {MaxInFlight: 1}}}, sock)
		gomega.Expect(srv.Start()).To(gomega.Succeed())

		defer srv.Stop()
		ginkgo.GinkgoT().
			Setenv("IDIOM_QUEUE_SOCKET", sock)

		rec, err := telemetry.Open(telemetry.Options{Dir: ginkgo.GinkgoT().TempDir(), Command: "implement", WorkspaceRoot: "/workspace"})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("telemetry.Open: %v", err))

		var stdout, stderr bytes.Buffer

		queueResult := implementAcquireQueueSlot(config.Config{}, "session", &stdout, &stderr, true, rec)
		queueResult.Release()
		gomega.Expect(rec.CloseWithFields(0, nil)).To(gomega.Succeed())

		records := readMainTelemetryRecords(rec.Path())
		queue := findTelemetryRecord(records, "implement_queue")
		gomega.Expect(queue).NotTo(gomega.Equal(nil), fmt.Sprintf("missing implement queue telemetry: %#v", records))
		gomega.Expect(queue["state"]).To(gomega.Equal("acquired"), fmt.Sprintf("real queue acquire should report acquired/non-degraded, got %#v", queue))
		gomega.Expect(queue["degraded"]).To(gomega.Equal(false), fmt.Sprintf("real queue acquire should report acquired/non-degraded, got %#v", queue))
		gomega.Expect(stdout.String()).To(gomega.ContainSubstring(`"type":"idiom.implement.queue_acquired"`), fmt.Sprintf("acquired queue JSON event missing, got %q", stdout.String()))

	})
	ginkgo.It("model closeout writes implement telemetry and one session summary", func() {
		root := ginkgo.GinkgoT().TempDir()
		cfg := config.Config{
			AppName: "Idiom",
			Chat: config.Chat{
				DebugLogDir:           filepath.Join(root, "logs", "conversations"),
				RequestTimeoutSeconds: 1,
			},
			Workspace: config.Workspace{Root: root, CWD: root},
		}

		rec, err := telemetry.Open(telemetry.Options{Dir: filepath.Join(root, "telemetry"), Command: "implement", WorkspaceRoot: root})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("telemetry.Open: %v", err))

		model := app.New(cfg)
		closeout := cmdruntime.ModelCloseout(rec, model, "prompt", string(autonomy.TaskFamilyImplementation))

		_ = rec.RegisterCloseout(func() { _ = closeout() })
		gomega.Expect(rec.CloseWithFields(143, map[string]any{"reason": "signal"})).To(gomega.Succeed())
		gomega.Expect(rec.CloseWithFields(0, nil)).To(gomega.Succeed())

		summaries := readAppSessionSummaryRecordsForMain(model.DebugLogPath())
		gomega.Expect(summaries).To(gomega.HaveLen(1), fmt.Sprintf("expected exactly one session_summary, got %d in %#v", len(summaries), summaries))

		records := readMainTelemetryRecords(onlyTelemetryFile(filepath.Join(root, "telemetry")))
		closeouts := 0
		modelCloseouts := 0

		for _, record := range records {
			if record["type"] == "model_session_closeout" {
				modelCloseouts++
				gomega.Expect(record["summary_event"]).To(gomega.Equal("session_summary"), fmt.Sprintf("model closeout should carry completed session summary metadata: %#v", record))
				gomega.Expect(record["session_id"]).NotTo(gomega.BeEmpty(), fmt.Sprintf("model closeout should carry completed session summary metadata: %#v", record))
				gomega.Expect(record["model_closed"]).To(gomega.Equal(true), fmt.Sprintf("model closeout should carry completed session summary metadata: %#v", record))

			}

			if record["type"] == "implement_session_closeout" {
				closeouts++
				gomega.Expect(record["summary_event"]).To(gomega.Equal("session_summary"), fmt.Sprintf("closeout should carry metadata-only session summary fields: %#v", record))
				gomega.Expect(record["session_id"]).NotTo(gomega.BeEmpty(), fmt.Sprintf("closeout should carry metadata-only session summary fields: %#v", record))

			}
		}
		gomega.Expect(closeouts).To(gomega.Equal(1), fmt.Sprintf("expected one implement_session_closeout, got %d in %#v", closeouts, records))
		gomega.Expect(modelCloseouts).To(gomega.Equal(1), fmt.Sprintf("expected one model_session_closeout, got %d in %#v", modelCloseouts, records))

	})
})

func onlyTelemetryFile(dir string) string {

	entries, err := os.ReadDir(dir)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read telemetry dir: %v", err))
	gomega.Expect(entries).To(gomega.HaveLen(1), fmt.Sprintf("expected one telemetry file, got %d", len(entries)))

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

func readAppSessionSummaryRecordsForMain(path string) []map[string]any {

	data, err := os.ReadFile(filepath.Clean(path))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read debug log: %v", err))

	var summaries []map[string]any

	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}

		var record map[string]any
		gomega.Expect(json.Unmarshal([]byte(line), &record)).To(gomega.Succeed())

		if record["type"] == "session_summary" {
			summaries = append(summaries, record)
		}
	}

	return summaries
}
