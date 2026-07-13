package localops

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"idiom/internal/evidence/telemetry"
	"idiom/internal/runtime/localops"
)

var _ = ginkgo.Describe("local ops", func() {
	ginkgo.It("local ops benchmark reports smoke capacity replay blockers", func() {
		root := ginkgo.GinkgoT().TempDir()
		ginkgo.GinkgoT().Chdir(root)
		stageDoctorConfigWithBudget(root, 16384, 512)

		report, err := buildLocalOpsBenchmarkReportWithDeps(BenchmarkOptions{
			ConfigPath: filepath.Join(root, ".idiom", "config.json"),
			Profile:    "eval",
		}, nil, resolveDependencies())
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("build local-ops report: %v", err))
		gomega.Expect(report.SchemaVersion).To(gomega.Equal(localops.BenchmarkSchemaVersion), fmt.Sprintf("unexpected schema version: %#v", report))
		gomega.Expect(report.Profiles).To(gomega.HaveLen(1), fmt.Sprintf("expected eval profile report, got %#v", report.Profiles))
		gomega.Expect(report.Profiles[0].Name).To(gomega.Equal("eval"), fmt.Sprintf("expected eval profile report, got %#v", report.Profiles))

		profile := report.Profiles[0]
		gomega.Expect(profile.Smoke.Status).To(gomega.Equal("blocked"), fmt.Sprintf("eval smoke should block when backend/queue prerequisites are absent, got %#v", profile.Smoke))
		gomega.Expect(profile.Capacity.ContextTokens).To(gomega.Equal(16384), fmt.Sprintf("capacity should expose bounded context assumptions, got %#v", profile.Capacity))
		gomega.Expect(profile.Capacity.MaxOutputTokens).To(gomega.BeNumerically(">", 0), fmt.Sprintf("capacity should expose bounded context assumptions, got %#v", profile.Capacity))
		gomega.Expect(profile.Capacity.PromptBudgetTokens).NotTo(gomega.BeZero(), fmt.Sprintf("capacity should expose bounded context assumptions, got %#v", profile.Capacity))
		gomega.Expect(report.Replay.Status).To(gomega.Equal("unsupported"), fmt.Sprintf("missing replay logs should be an explicit unsupported blocker, got %#v", report.Replay))
		gomega.Expect(report.Replay.Blockers).NotTo(gomega.BeEmpty(), fmt.Sprintf("missing replay logs should be an explicit unsupported blocker, got %#v", report.Replay))
		gomega.Expect(report.Privacy.MetadataOnly).To(gomega.BeTrue(), fmt.Sprintf("local-ops report must remain metadata-only, got %#v", report.Privacy))
		gomega.Expect(report.Privacy.RawPromptCaptured).To(gomega.BeFalse(), fmt.Sprintf("local-ops report must remain metadata-only, got %#v", report.Privacy))
		gomega.Expect(report.Privacy.RawFileBodyCaptured).To(gomega.BeFalse(), fmt.Sprintf("local-ops report must remain metadata-only, got %#v", report.Privacy))
		gomega.Expect(report.Privacy.SecretCaptureAllowed).To(gomega.BeFalse(), fmt.Sprintf("local-ops report must remain metadata-only, got %#v", report.Privacy))

		rendered := mustMarshalLocalOpsReport(report)
		for _, forbidden := range []string{"SECRET", "raw prompt body", "file body"} {
			gomega.Expect(rendered).NotTo(gomega.ContainSubstring(forbidden), fmt.Sprintf("local-ops report leaked raw content %q in %s", forbidden, rendered))

		}
	})
	ginkgo.It("local ops benchmark quickstart smoke records p010 activation evidence", func() {
		parent := ginkgo.GinkgoT().TempDir()
		root := filepath.Join(parent, "AcmeCustomer-private-workspace")
		gomega.Expect(os.MkdirAll(filepath.Join(root, "pkg"), 0o755)).To(gomega.Succeed())

		ginkgo.GinkgoT().Chdir(root)
		stageDoctorConfigWithBudget(root, 16384, 1024)
		gomega.Expect(os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/quickstart\n\ngo 1.22\n"), 0o644)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(root, "pkg", "quickstart.go"), []byte("package pkg\n\nfunc QuickstartValue() string { return \"ok\" }\n"), 0o644)).To(gomega.Succeed())

		report, err := buildLocalOpsBenchmarkReportWithDeps(BenchmarkOptions{
			ConfigPath:        filepath.Join(root, ".idiom", "config.json"),
			Profile:           "sidecar",
			QuickstartSmoke:   true,
			QuickstartTask:    "localize quickstart smoke value",
			QuickstartCleanup: "remove-local-state",
		}, nil, resolveDependencies())
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("build local-ops report: %v", err))
		gomega.Expect(report.Quickstart).NotTo(gomega.BeNil(), fmt.Sprintf("expected quickstart smoke evidence"))

		smoke := report.Quickstart
		gomega.Expect(smoke.Status).To(gomega.Equal("pass"), fmt.Sprintf("quickstart smoke should pass in clean fixture, got %#v", smoke))
		gomega.Expect(smoke.SetupInstallReadiness.Status).To(gomega.Equal("pass"), fmt.Sprintf("quickstart smoke missing P0.10 evidence fields: %#v", smoke))
		gomega.Expect(smoke.FirstUsefulPacket.PacketID).NotTo(gomega.BeEmpty(), fmt.Sprintf("quickstart smoke missing P0.10 evidence fields: %#v", smoke))
		gomega.Expect(smoke.FirstUsefulPacket.IndexID).NotTo(gomega.BeEmpty(), fmt.Sprintf("quickstart smoke missing P0.10 evidence fields: %#v", smoke))
		gomega.Expect(smoke.FirstUsefulPacket.Task.TaskID).NotTo(gomega.BeEmpty(), fmt.Sprintf("quickstart smoke missing P0.10 evidence fields: %#v", smoke))
		gomega.Expect(smoke.FirstUsefulPacket.Task.RawTaskCaptured).To(gomega.BeFalse(), fmt.Sprintf("quickstart smoke missing P0.10 evidence fields: %#v", smoke))
		gomega.Expect(smoke.FreshnessCheck.State).NotTo(gomega.BeEmpty(), fmt.Sprintf("quickstart smoke missing P0.10 evidence fields: %#v", smoke))
		gomega.Expect(smoke.AuditReport.AuditReportID).NotTo(gomega.BeEmpty(), fmt.Sprintf("quickstart smoke missing P0.10 evidence fields: %#v", smoke))
		gomega.Expect(smoke.SuggestedVerification.SuggestedCommands).NotTo(gomega.BeEmpty(), fmt.Sprintf("quickstart smoke missing P0.10 evidence fields: %#v", smoke))
		gomega.Expect(smoke.ProductEvidenceSafeOutput.ForbiddenFieldCheck).To(gomega.Equal("pass"), fmt.Sprintf("quickstart smoke missing P0.10 evidence fields: %#v", smoke))
		gomega.Expect(smoke.FailureReason.Status).To(gomega.Equal("none"), fmt.Sprintf("quickstart smoke missing P0.10 evidence fields: %#v", smoke))
		gomega.Expect(smoke.DocsGap.Status).To(gomega.Equal("none"), fmt.Sprintf("quickstart smoke missing P0.10 evidence fields: %#v", smoke))
		gomega.Expect(smoke.PrivacyRedactionStatus.Status).To(gomega.Equal("pass"), fmt.Sprintf("quickstart smoke missing P0.10 evidence fields: %#v", smoke))
		gomega.Expect(smoke.Cleanup.Status).To(gomega.Equal("pass"), fmt.Sprintf("quickstart smoke missing P0.10 evidence fields: %#v", smoke))
		gomega.Expect(smoke.TimeToFirstPacketSeconds).To(gomega.BeNumerically(">", 0), fmt.Sprintf("quickstart smoke should expose deterministic duration fields, got first=%v trust=%v", smoke.TimeToFirstPacketSeconds, smoke.TimeToTrustEvidenceSeconds))
		gomega.Expect(smoke.TimeToTrustEvidenceSeconds).To(gomega.BeNumerically(">=", smoke.TimeToFirstPacketSeconds), fmt.Sprintf("quickstart smoke should expose deterministic duration fields, got first=%v trust=%v", smoke.TimeToFirstPacketSeconds, smoke.TimeToTrustEvidenceSeconds))

		if _, err := os.Stat(filepath.Join(root, ".idiom", "comprehension")); !os.IsNotExist(err) {
			ginkgo.Fail(fmt.Sprintf("remove-local-state cleanup should remove comprehension state, stat err=%v", err))
		}

		rendered := mustMarshalLocalOpsReport(report)
		for _, forbidden := range []string{
			root,
			parent,
			"AcmeCustomer",
			"private-workspace",
			"SECRET raw prompt body",
			"func QuickstartValue",
		} {
			gomega.Expect(rendered).NotTo(gomega.ContainSubstring(forbidden), fmt.Sprintf("quickstart smoke leaked forbidden content %q in %s", forbidden, rendered))

		}
	})
	ginkgo.It("local ops benchmark quickstart smoke redacts sensitive task evidence", func() {
		root := ginkgo.GinkgoT().TempDir()
		ginkgo.GinkgoT().Chdir(root)
		stageDoctorConfigWithBudget(root, 16384, 1024)
		gomega.Expect(os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/sensitivetask\n\ngo 1.22\n"), 0o644)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc QuickstartSensitiveTaskFixture() string { return \"ok\" }\n"), 0o644)).To(gomega.Succeed())

		report, err := buildLocalOpsBenchmarkReportWithDeps(BenchmarkOptions{
			ConfigPath:        filepath.Join(root, ".idiom", "config.json"),
			Profile:           "sidecar",
			QuickstartSmoke:   true,
			QuickstartTask:    "QuickstartSensitiveTaskFixture /workspace/CustomerX-private-repo secret abc123",
			QuickstartCleanup: "remove-local-state",
		}, nil, resolveDependencies())
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("build local-ops report: %v", err))
		gomega.Expect(report.Quickstart).NotTo(gomega.BeNil(), fmt.Sprintf("expected quickstart smoke evidence"))

		smoke := report.Quickstart
		gomega.Expect(smoke.Status).To(gomega.Equal("pass"), fmt.Sprintf("redacted quickstart smoke should pass, got %#v", smoke))
		gomega.Expect(smoke.FirstUsefulPacket.Task.RawTaskCaptured).To(gomega.BeFalse(), fmt.Sprintf("quickstart task evidence should be hash/classification only, got %#v", smoke.FirstUsefulPacket.Task))
		gomega.Expect(smoke.FirstUsefulPacket.Task.TaskID).NotTo(gomega.BeEmpty(), fmt.Sprintf("quickstart task evidence should be hash/classification only, got %#v", smoke.FirstUsefulPacket.Task))
		gomega.Expect(smoke.FirstUsefulPacket.Task.EvidenceType).To(gomega.Equal("hash_and_classification"), fmt.Sprintf("quickstart task evidence should be hash/classification only, got %#v", smoke.FirstUsefulPacket.Task))
		gomega.Expect(smoke.FirstUsefulPacket.Task.RedactionStatus).To(gomega.Equal("redacted"), fmt.Sprintf("quickstart task evidence should be hash/classification only, got %#v", smoke.FirstUsefulPacket.Task))

		for _, class := range []string{"absolute_path", "secret_marker", "sensitive_identifier"} {
			gomega.Expect(slices.Contains(smoke.FirstUsefulPacket.Task.ContentClasses, class)).To(gomega.BeTrue(), fmt.Sprintf("task evidence missing content class %q: %#v", class, smoke.FirstUsefulPacket.Task))

		}
		gomega.Expect(smoke.ProductEvidenceSafeOutput.ForbiddenFieldCheck).To(gomega.Equal("pass"), fmt.Sprintf("privacy/product evidence status should reconcile with redacted task evidence: %#v", smoke))
		gomega.Expect(smoke.ProductEvidenceSafeOutput.RedactionStatus).To(gomega.Equal("redacted"), fmt.Sprintf("privacy/product evidence status should reconcile with redacted task evidence: %#v", smoke))
		gomega.Expect(smoke.PrivacyRedactionStatus.Status).To(gomega.Equal("pass"), fmt.Sprintf("privacy/product evidence status should reconcile with redacted task evidence: %#v", smoke))
		gomega.Expect(smoke.PrivacyRedactionStatus.ForbiddenFieldCheck).To(gomega.Equal("pass"), fmt.Sprintf("privacy/product evidence status should reconcile with redacted task evidence: %#v", smoke))
		gomega.Expect(smoke.PrivacyRedactionStatus.SecretCaptured).To(gomega.BeFalse(), fmt.Sprintf("privacy/product evidence status should reconcile with redacted task evidence: %#v", smoke))
		gomega.Expect(smoke.PrivacyRedactionStatus.PrivatePathCaptured).To(gomega.BeFalse(), fmt.Sprintf("privacy/product evidence status should reconcile with redacted task evidence: %#v", smoke))

		rendered := mustMarshalLocalOpsReport(report)
		for _, forbidden := range []string{"/Users/", "CustomerX", "secret abc123"} {
			gomega.Expect(rendered).NotTo(gomega.ContainSubstring(forbidden), fmt.Sprintf("quickstart product evidence leaked %q in %s", forbidden, rendered))

		}

		if finding := localops.ForbiddenEvidenceLeak(rendered); finding != "" {
			ginkgo.Fail(fmt.Sprintf("redacted quickstart evidence should pass forbidden-field scan, got %q in %s", finding, rendered))
		}
	})
	ginkgo.It("local ops forbidden evidence leak scans values without failing allowed field names", func() {
		allowed := `{"privacy_redaction_status":{"secret_captured":false,"private_path_captured":false},"redacted_fields":["secret_value"]}`
		if finding := localops.ForbiddenEvidenceLeak(allowed); finding != "" {
			ginkgo.Fail(fmt.Sprintf("allowed privacy field names should not fail forbidden scan: %s", finding))
		}

		for name, body := range map[string]string{
			"absolute_path":       `{"task":"/workspace/repo"}`,
			"customer_identifier": `{"task":"CustomerX-private-repo"}`,
			"secret_value":        `{"task":"secret abc123"}`,
			"true_secret_flag":    `{"privacy_redaction_status":{"secret_captured":true}}`,
		} {
			if finding := localops.ForbiddenEvidenceLeak(body); finding == "" {
				ginkgo.Fail(fmt.Sprintf("expected forbidden scan to catch %s in %s", name, body))
			}
		}
	})
	ginkgo.It("run local ops benchmark quickstart smoke json", func() {
		root := ginkgo.GinkgoT().TempDir()
		ginkgo.GinkgoT().Chdir(root)
		stageDoctorConfigWithBudget(root, 4096, 512)
		gomega.Expect(os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/cliquickstart\n\ngo 1.22\n"), 0o644)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644)).To(gomega.Succeed())

		var stdout, stderr bytes.Buffer

		code := RunMainWithTelemetry([]string{
			"benchmark",
			"--json",
			"--config", filepath.Join(root, ".idiom", "config.json"),
			"--profile", "sidecar",
			"--quickstart-smoke",
			"--quickstart-cleanup", "remove-smoke-artifacts",
		}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("local-ops quickstart smoke failed code=%d stderr=%s", code, stderr.String()))

		var report localops.BenchmarkReport
		gomega.Expect(json.Unmarshal(stdout.Bytes(), &report)).To(gomega.Succeed())
		gomega.Expect(report.Quickstart).NotTo(gomega.BeNil(), fmt.Sprintf("quickstart smoke JSON missing packet evidence: %#v", report.Quickstart))
		gomega.Expect(report.Quickstart.FirstUsefulPacket.PacketID).NotTo(gomega.BeEmpty(), fmt.Sprintf("quickstart smoke JSON missing packet evidence: %#v", report.Quickstart))
		gomega.Expect(report.Quickstart.Cleanup.Status).To(gomega.Equal("pass"), fmt.Sprintf("quickstart smoke should record cleanup evidence: %#v", report.Quickstart.Cleanup))
		gomega.Expect(report.Quickstart.Cleanup.RemovedArtifactCount).To(gomega.Equal(1), fmt.Sprintf("quickstart smoke should record cleanup evidence: %#v", report.Quickstart.Cleanup))

	})
	ginkgo.It("local ops benchmark summarizes replay fixture without raw prompt", func() {
		root := ginkgo.GinkgoT().TempDir()
		ginkgo.GinkgoT().Chdir(root)
		stageDoctorConfigWithBudget(root, 16384, 1024)
		stageDoctorBackends(root)

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
		gomega.Expect(os.WriteFile(logPath, []byte(body), 0o644)).To(gomega.Succeed())

		report, err := buildLocalOpsBenchmarkReportWithDeps(BenchmarkOptions{
			ConfigPath: filepath.Join(root, ".idiom", "config.json"),
			Profile:    "implement",
			ReplayLog:  logPath,
		}, nil, resolveDependencies())
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("build local-ops report: %v", err))
		gomega.Expect(report.Replay.Status).To(gomega.Equal("pass"), fmt.Sprintf("expected replay summary pass for fixture, got %#v", report.Replay))
		gomega.Expect(report.Replay.LogCount).To(gomega.Equal(1), fmt.Sprintf("expected replay summary pass for fixture, got %#v", report.Replay))
		gomega.Expect(report.Replay.Aggregate).NotTo(gomega.BeNil(), fmt.Sprintf("replay aggregate missing: %#v", report.Replay.Aggregate))
		gomega.Expect(report.Replay.Aggregate.LogCount).To(gomega.Equal(1), fmt.Sprintf("replay aggregate missing: %#v", report.Replay.Aggregate))
		gomega.Expect(report.Replay.Signals.QueueDegraded).NotTo(gomega.BeZero(), fmt.Sprintf("expected replay queue/budget signals, got %#v", report.Replay.Signals))
		gomega.Expect(report.Replay.Signals.BudgetExhausted).NotTo(gomega.BeZero(), fmt.Sprintf("expected replay queue/budget signals, got %#v", report.Replay.Signals))

		rendered := mustMarshalLocalOpsReport(report)
		for _, forbidden := range []string{"SECRET raw prompt body", "log_path"} {
			gomega.Expect(rendered).NotTo(gomega.ContainSubstring(forbidden), fmt.Sprintf("replay report leaked %q: %s", forbidden, rendered))

		}
	})
	ginkgo.It("local ops benchmark redacts replay path from errors and telemetry", func() {
		root := ginkgo.GinkgoT().TempDir()
		ginkgo.GinkgoT().Chdir(root)
		stageDoctorConfigWithBudget(root, 16384, 1024)
		stageDoctorBackends(root)

		replayPath := filepath.Join(root, "AcmeCustomer-secret-session.jsonl")
		telemetryDir := ginkgo.GinkgoT().TempDir()
		ginkgo.GinkgoT().
			Setenv("IDIOM_TELEMETRY_DIR", telemetryDir)

		var stdout, stderr bytes.Buffer

		rec, err := telemetry.Open(telemetry.Options{Dir: telemetryDir, Command: "local-ops"})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("open telemetry: %v", err))

		code := RunMainWithTelemetry([]string{
			"benchmark",
			"--json",
			"--config", filepath.Join(root, ".idiom", "config.json"),
			"--profile", "implement",
			"--replay-log", replayPath,
		}, &stdout, &stderr, rec)
		if rec != nil {
			gomega.Expect(rec.CloseWithFields(code, nil)).To(gomega.Succeed())

		}
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("missing replay log should degrade report, got code=%d stderr=%s", code, stderr.String()))

		var got localops.BenchmarkReport
		gomega.Expect(json.Unmarshal(stdout.Bytes(), &got)).To(gomega.Succeed())
		gomega.Expect(got.Replay.Status).To(gomega.Equal("blocked"), fmt.Sprintf("expected sanitized replay blocker, got %#v", got.Replay))
		gomega.Expect(got.Replay.LogCount).To(gomega.BeZero(), fmt.Sprintf("expected sanitized replay blocker, got %#v", got.Replay))
		gomega.Expect(got.Replay.Blockers).NotTo(gomega.BeEmpty(), fmt.Sprintf("expected sanitized replay blocker, got %#v", got.Replay))

		telemetryBody, err := os.ReadFile(onlyTelemetryFile(telemetryDir))
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read telemetry: %v", err))

		combined := strings.Join([]string{stdout.String(), stderr.String(), string(telemetryBody)}, "\n")
		for _, forbidden := range []string{
			replayPath,
			filepath.Base(replayPath),
			"AcmeCustomer",
			"secret-session",
			"open " + root,
		} {
			gomega.Expect(combined).NotTo(gomega.ContainSubstring(forbidden), fmt.Sprintf("local-ops replay output leaked %q in %s", forbidden, combined))

		}
		gomega.Expect(stdout.String()).To(gomega.ContainSubstring("replay_log_unreadable"), fmt.Sprintf("expected sanitized replay reason code, got %s", stdout.String()))

	})
	ginkgo.It("run local ops benchmark invalid profile exits two", func() {
		root := ginkgo.GinkgoT().TempDir()
		ginkgo.GinkgoT().Chdir(root)
		stageDoctorConfigWithBudget(root, 4096, 512)

		var stdout, stderr bytes.Buffer

		code := RunMainWithTelemetry([]string{
			"benchmark",
			"--config", filepath.Join(root, ".idiom", "config.json"),
			"--profile", "nope",
		}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(2), fmt.Sprintf("invalid profile must exit 2, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String()))
		gomega.Expect(stderr.String()).To(gomega.ContainSubstring("unknown readiness profile"), fmt.Sprintf("expected profile selection error, got %q", stderr.String()))

	})
	ginkgo.It("run local ops benchmark command writes json output", func() {
		root := ginkgo.GinkgoT().TempDir()
		ginkgo.GinkgoT().Chdir(root)
		stageDoctorConfigWithBudget(root, 4096, 512)

		outputPath := filepath.Join(root, ".idiom", "local-ops", "report.json")

		var stdout, stderr bytes.Buffer

		code := RunMainWithTelemetry([]string{"benchmark", "--json", "--config", filepath.Join(root, ".idiom", "config.json"), "--profile", "implement", "--output", outputPath}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("local-ops benchmark failed code=%d stderr=%s", code, stderr.String()))

		var got localops.BenchmarkReport
		gomega.Expect(json.Unmarshal(stdout.Bytes(), &got)).To(gomega.Succeed())
		gomega.Expect(got.Summary.ProfileCount).To(gomega.Equal(1), fmt.Sprintf("unexpected JSON report: %#v", got))
		gomega.Expect(got.Profiles).To(gomega.HaveLen(1), fmt.Sprintf("unexpected JSON report: %#v", got))
		gomega.Expect(got.Profiles[0].Name).To(gomega.Equal("implement"), fmt.Sprintf("unexpected JSON report: %#v", got))

		if _, err := os.Stat(outputPath); err != nil {
			ginkgo.Fail(fmt.Sprintf("expected report output file: %v", err))
		}
	})
})

func mustMarshalLocalOpsReport(report localops.BenchmarkReport) string {

	body, err := json.Marshal(report)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal report: %v", err))

	return string(body)
}

func stageDoctorConfigWithBudget(root string, ctxSize int, maxTokens int) {

	cfg := filepath.Join(root, ".idiom", "config.json")
	gomega.Expect(os.MkdirAll(filepath.Dir(cfg), 0o755)).To(gomega.Succeed())

	body := fmt.Sprintf(`{
  "workspace": {"root": %q},
  "llama_server": {"ctx_size": %d},
  "chat": {"max_tokens": %d}
}`, root, ctxSize, maxTokens)
	gomega.Expect(os.WriteFile(cfg, []byte(body), 0o644)).To(gomega.Succeed())

}

func stageDoctorBackends(root string) {

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
	gomega.Expect(os.WriteFile(path, []byte(body), 0o644)).To(gomega.Succeed())

}

func onlyTelemetryFile(dir string) string {

	entries, err := os.ReadDir(dir)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read telemetry dir: %v", err))
	gomega.Expect(entries).To(gomega.HaveLen(1), fmt.Sprintf("expected one telemetry file, got %d", len(entries)))

	return filepath.Join(dir, entries[0].Name())
}
