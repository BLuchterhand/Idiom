package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAuditWatchOnceWritesReviewForFeedbackTelemetry(t *testing.T) {
	t.Setenv("IDIOM_TELEMETRY", "0")
	root := t.TempDir()
	configPath := writeAuditWatchConfig(t, root)
	telemetryDir := filepath.Join(root, "logs", "telemetry")
	if err := os.MkdirAll(telemetryDir, 0o755); err != nil {
		t.Fatalf("mkdir telemetry: %v", err)
	}
	writeAuditWatchFile(t, filepath.Join(telemetryDir, "feedback.jsonl"), strings.Join([]string{
		`{"type":"session_start","command":"tool:harness_feedback","workspace_root":"` + root + `"}`,
		`{"type":"tool_result","tool":"harness_feedback","summary":"Recorded workflow_gap feedback for completion_gate","error":"","feedback_category":"workflow_gap","feedback_component":"completion_gate","feedback_confidence":"verified","feedback_severity":"medium"}`,
		`{"type":"process_exit","command":"tool:harness_feedback","exit_code":0}`,
		"",
	}, "\n"))
	outputDir := filepath.Join(root, ".idiom", "audits", "watch")

	var stdout, stderr bytes.Buffer
	code := runMain([]string{
		"audit", "watch",
		"--config", configPath,
		"--paths", "logs/telemetry",
		"--output", outputDir,
		"--once",
		"--review-existing",
		"--debounce", "0",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("audit watch failed code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), "audit review:") {
		t.Fatalf("expected review path on stdout, got %q", stdout.String())
	}
	reviews := auditWatchReviewFiles(t, outputDir)
	if len(reviews) != 1 {
		t.Fatalf("expected one review artifact, got %#v", reviews)
	}
	var review auditWatchReview
	data, err := os.ReadFile(reviews[0])
	if err != nil {
		t.Fatalf("read review: %v", err)
	}
	if err := json.Unmarshal(data, &review); err != nil {
		t.Fatalf("decode review: %v\n%s", err, string(data))
	}
	if len(review.TriggerFiles) != 1 || !slicesContains(review.TriggerFiles[0].Reasons, "harness_feedback") {
		t.Fatalf("expected harness_feedback trigger, got %#v", review.TriggerFiles)
	}
	if len(review.Audits) != 1 || review.Audits[0].Report == nil {
		t.Fatalf("expected embedded harness audit report, got %#v", review.Audits)
	}
}

func TestAuditWatchFirstRunBaselinesWithoutReviewExisting(t *testing.T) {
	t.Setenv("IDIOM_TELEMETRY", "0")
	root := t.TempDir()
	configPath := writeAuditWatchConfig(t, root)
	telemetryDir := filepath.Join(root, "logs", "telemetry")
	if err := os.MkdirAll(telemetryDir, 0o755); err != nil {
		t.Fatalf("mkdir telemetry: %v", err)
	}
	writeAuditWatchFile(t, filepath.Join(telemetryDir, "feedback.jsonl"), `{"type":"tool_result","tool":"harness_feedback","summary":"Recorded feedback","error":""}`+"\n")
	outputDir := filepath.Join(root, ".idiom", "audits", "watch")

	var stdout, stderr bytes.Buffer
	code := runMain([]string{
		"audit", "watch",
		"--config", configPath,
		"--paths", "logs/telemetry",
		"--output", outputDir,
		"--once",
		"--debounce", "0",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("audit watch failed code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), "no triggers") {
		t.Fatalf("expected baseline no-trigger status, got %q", stdout.String())
	}
	if reviews := auditWatchReviewFiles(t, outputDir); len(reviews) != 0 {
		t.Fatalf("expected no review artifacts, got %#v", reviews)
	}
	if _, err := os.Stat(filepath.Join(outputDir, "state.json")); err != nil {
		t.Fatalf("expected state file: %v", err)
	}
}

func TestAuditWatchOnlyScansAppendedLogContentAfterReview(t *testing.T) {
	t.Setenv("IDIOM_TELEMETRY", "0")
	root := t.TempDir()
	configPath := writeAuditWatchConfig(t, root)
	telemetryPath := filepath.Join(root, "logs", "telemetry", "feedback.jsonl")
	writeAuditWatchFile(t, telemetryPath, `{"type":"tool_result","tool":"harness_feedback","summary":"Recorded feedback","error":""}`+"\n")
	outputDir := filepath.Join(root, ".idiom", "audits", "watch")

	var stdout, stderr bytes.Buffer
	code := runMain([]string{
		"audit", "watch",
		"--config", configPath,
		"--paths", "logs/telemetry",
		"--output", outputDir,
		"--once",
		"--review-existing",
		"--debounce", "0",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("initial audit watch failed code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if reviews := auditWatchReviewFiles(t, outputDir); len(reviews) != 1 {
		t.Fatalf("expected initial review artifact, got %#v", reviews)
	}

	f, err := os.OpenFile(telemetryPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open telemetry append: %v", err)
	}
	if _, err := f.WriteString(`{"type":"process_exit","command":"tool:pwd","exit_code":0}` + "\n"); err != nil {
		t.Fatalf("append telemetry: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close telemetry append: %v", err)
	}
	nextModTime := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(telemetryPath, nextModTime, nextModTime); err != nil {
		t.Fatalf("touch telemetry: %v", err)
	}

	stdout.Reset()
	stderr.Reset()
	code = runMain([]string{
		"audit", "watch",
		"--config", configPath,
		"--paths", "logs/telemetry",
		"--output", outputDir,
		"--once",
		"--debounce", "0",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("second audit watch failed code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), "no triggers") {
		t.Fatalf("expected appended non-trigger record to stay quiet, got %q", stdout.String())
	}
	if reviews := auditWatchReviewFiles(t, outputDir); len(reviews) != 1 {
		t.Fatalf("expected no duplicate review artifacts, got %#v", reviews)
	}
}

func TestAuditWatchHelpReturnsCleanly(t *testing.T) {
	t.Setenv("IDIOM_TELEMETRY", "0")
	var stdout, stderr bytes.Buffer
	code := runMain([]string{"audit", "watch", "--help"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected clean help exit, got code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "idiom audit watch") {
		t.Fatalf("expected audit watch usage, got stdout=%s stderr=%s", stdout.String(), stderr.String())
	}
}

func TestAuditWatchHelpSkipsFlagValues(t *testing.T) {
	if auditWatchHelpRequested([]string{"--output", "help"}) {
		t.Fatal("output directory named help should not request command help")
	}
	if !auditWatchHelpRequested([]string{"--output", "tmp", "--help"}) {
		t.Fatal("expected help flag after output value to request command help")
	}
}

func writeAuditWatchConfig(t *testing.T, root string) string {
	t.Helper()
	path := filepath.Join(root, "config.json")
	body := `{"workspace":{"root":` + strconvQuote(root) + `,"cwd":` + strconvQuote(root) + `,"enable_shell":true,"enable_writes":true}}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func writeAuditWatchFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func auditWatchReviewFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	var paths []string
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() == "state.json" || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		paths = append(paths, filepath.Join(dir, entry.Name()))
	}
	return paths
}

func slicesContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func strconvQuote(value string) string {
	data, _ := json.Marshal(value)
	return string(data)
}
