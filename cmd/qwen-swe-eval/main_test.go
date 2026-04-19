package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"qwen-bubbles/internal/eval"
)

func TestRunSuiteOnlyPassesValidSuite(t *testing.T) {
	dir := t.TempDir()
	suitePath := filepath.Join(dir, "suite.json")
	writeSWESmokeSuite(t, suitePath, "")

	var stdout, stderr bytes.Buffer
	code := run([]string{"-suite", suitePath, "-suite-only"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "PASS cli-smoke-suite") || !strings.Contains(stdout.String(), "Lint: PASS") {
		t.Fatalf("expected passing suite report, got %s", stdout.String())
	}
}

func TestRunEvaluatesLogsDirectory(t *testing.T) {
	dir := t.TempDir()
	suitePath := filepath.Join(dir, "suite.json")
	logsDir := filepath.Join(dir, "logs")
	if err := os.MkdirAll(logsDir, 0o755); err != nil {
		t.Fatalf("mkdir logs: %v", err)
	}
	writeSWESmokeSuite(t, suitePath, "")
	writeSWESmokeLog(t, filepath.Join(logsDir, "direct.jsonl"))

	var stdout, stderr bytes.Buffer
	code := run([]string{"-suite", suitePath, "-logs", logsDir}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	for _, want := range []string{"PASS cli-smoke-suite", "Results: passed=1 failed=0", "- PASS direct"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("expected suite output to contain %q, got %s", want, stdout.String())
		}
	}
}

func TestRunReportsMissingLogs(t *testing.T) {
	dir := t.TempDir()
	suitePath := filepath.Join(dir, "suite.json")
	writeSWESmokeSuite(t, suitePath, "")

	var stdout, stderr bytes.Buffer
	code := run([]string{"-suite", suitePath}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("expected exit 1 for missing logs, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "missing_logs=1") {
		t.Fatalf("expected missing log summary, got %s", stdout.String())
	}
}

func TestTaskLogPathUsesExplicitAndLogsDirPaths(t *testing.T) {
	task := smokeSWETask("")
	if got := taskLogPath("/tmp/suite.json", "/tmp/logs", task); got != filepath.Join("/tmp/logs", "direct.jsonl") {
		t.Fatalf("unexpected logs-dir path: %q", got)
	}
	task.LogPath = "custom/direct.jsonl"
	if got := taskLogPath("/tmp/suites/suite.json", "/tmp/logs", task); got != filepath.Clean("/tmp/suites/custom/direct.jsonl") {
		t.Fatalf("unexpected relative explicit path: %q", got)
	}
	task.LogPath = filepath.Join(string(filepath.Separator), "var", "tmp", "direct.jsonl")
	if got := taskLogPath("/tmp/suites/suite.json", "/tmp/logs", task); got != filepath.Clean(task.LogPath) {
		t.Fatalf("unexpected absolute explicit path: %q", got)
	}
}

func writeSWESmokeSuite(t *testing.T, path string, logPath string) {
	t.Helper()
	taskLog := ""
	if strings.TrimSpace(logPath) != "" {
		taskLog = `, "log_path": ` + quoteJSON(logPath)
	}
	suite := `{
  "name": "cli-smoke-suite",
  "tasks": [{
    "id": "direct",
    "family": "direct_question",
    "prompt": "Explain routing.",
    "tags": ["read-only"]` + taskLog + `,
    "scenario": {
      "name": "direct",
      "prompt_contains": ["Explain routing"],
      "require_route_plan": {
        "initial_lane": "main",
        "reason": "direct_conversation",
        "contract": "direct_answer"
      },
      "require_verification": "none",
      "require_approval": false
    },
    "oracle": {
      "require_no_changed_files": true,
      "require_no_approval": true,
      "max_tool_requests": 2
    }
  }]
}
`
	if err := os.WriteFile(path, []byte(suite), 0o644); err != nil {
		t.Fatalf("write suite: %v", err)
	}
}

func writeSWESmokeLog(t *testing.T, path string) {
	t.Helper()
	const log = `{"type":"message","role":"user","content":"Explain routing."}
{"type":"message","role":"route_plan","content":"initial=main reason=direct_conversation contract=direct_answer"}
{"type":"message","role":"assistant","content":"Routing is direct for this smoke test."}
`
	if err := os.WriteFile(path, []byte(log), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}
}

func smokeSWETask(logPath string) eval.SWETask {
	return eval.SWETask{ID: "direct", LogPath: logPath}
}

func quoteJSON(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
}
