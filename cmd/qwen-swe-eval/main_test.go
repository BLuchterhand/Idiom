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
	for _, want := range []string{"PASS cli-smoke-suite", "Results: passed=1 failed=0", "Control rails dashboard:", "- PASS direct"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("expected suite output to contain %q, got %s", want, stdout.String())
		}
	}
}

func TestRunLiveGeneratesLogsAndScoresThem(t *testing.T) {
	dir := t.TempDir()
	suitePath := filepath.Join(dir, "suite.json")
	logsDir := filepath.Join(dir, "generated")
	writeSWESmokeSuite(t, suitePath, "")

	command := strings.Join([]string{
		`printf '%s\n'`,
		`'{"type":"message","role":"user","content":"Explain routing."}'`,
		`'{"type":"message","role":"route_plan","content":"initial=main reason=direct_conversation contract=direct_answer"}'`,
		`'{"type":"message","role":"assistant","content":"Routing is direct for this smoke test."}'`,
		`> {log_path}`,
	}, " ")
	var stdout, stderr bytes.Buffer
	code := run([]string{"-suite", suitePath, "-run-live", "-generated-logs", logsDir, "-runner-command", command}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(logsDir, "direct.jsonl")); err != nil {
		t.Fatalf("expected generated log: %v", err)
	}
}

func TestRunLivePassesMultiTurnMetadata(t *testing.T) {
	dir := t.TempDir()
	suitePath := filepath.Join(dir, "suite.json")
	logsDir := filepath.Join(dir, "generated")
	writeSWEMultiTurnSmokeSuite(t, suitePath)

	command := strings.Join([]string{
		`test "$QWEN_SWE_TURN_COUNT" = "2"`,
		`&& test "$QWEN_SWE_TURNS_JSON" = '["first turn","second turn"]'`,
		`&& printf '%s\n'`,
		`'{"type":"message","role":"user","content":"first turn"}'`,
		`'{"type":"message","role":"assistant","content":"first answer"}'`,
		`'{"type":"message","role":"user","content":"second turn"}'`,
		`'{"type":"message","role":"route_plan","content":"initial=main reason=direct_conversation contract=direct_answer"}'`,
		`'{"type":"message","role":"assistant","content":"second answer"}'`,
		`> {log_path}`,
	}, " ")
	var stdout, stderr bytes.Buffer
	code := run([]string{"-suite", suitePath, "-run-live", "-generated-logs", logsDir, "-runner-command", command}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "PASS cli-smoke-suite") {
		t.Fatalf("expected passing suite, got stdout=%s stderr=%s", stdout.String(), stderr.String())
	}
}

func TestRunWritesFailingScenarioSuite(t *testing.T) {
	dir := t.TempDir()
	suitePath := filepath.Join(dir, "suite.json")
	logsDir := filepath.Join(dir, "logs")
	outPath := filepath.Join(dir, "regressions.json")
	if err := os.MkdirAll(logsDir, 0o755); err != nil {
		t.Fatalf("mkdir logs: %v", err)
	}
	writeSWESmokeSuite(t, suitePath, "")
	if err := os.WriteFile(filepath.Join(logsDir, "direct.jsonl"), []byte(`{"type":"message","role":"user","content":"Explain routing."}
{"type":"message","role":"assistant","content":"No route plan here."}
`), 0o644); err != nil {
		t.Fatalf("write failing log: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := run([]string{"-suite", suitePath, "-logs", logsDir, "-write-failing-scenarios", outPath}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("expected failing eval exit 1, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read generated suite: %v", err)
	}
	if !strings.Contains(string(data), "direct-regression") || !strings.Contains(string(data), "generated-regression") {
		t.Fatalf("expected generated regression task, got %s", data)
	}
}

func TestRenderRunnerCommandDoesNotReplacePlaceholdersInsideValues(t *testing.T) {
	rendered := renderRunnerCommand(`printf '%s %s\n' {prompt} {log_path}`, map[string]string{
		"prompt":   "literal {log_path} in prompt",
		"log_path": "/tmp/out.jsonl",
	})
	if !strings.Contains(rendered, `'literal {log_path} in prompt'`) {
		t.Fatalf("expected prompt placeholder text to remain literal, got %s", rendered)
	}
	if !strings.Contains(rendered, `'/tmp/out.jsonl'`) {
		t.Fatalf("expected log path replacement, got %s", rendered)
	}

	rendered = renderRunnerCommand(`printf '%s\n' {turns_json}`, map[string]string{
		"turns_json": `["first turn","second turn"]`,
	})
	if !strings.Contains(rendered, `'["first turn","second turn"]'`) {
		t.Fatalf("expected turns_json replacement, got %s", rendered)
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

func writeSWEMultiTurnSmokeSuite(t *testing.T, path string) {
	t.Helper()
	suite := `{
  "name": "cli-smoke-suite",
  "tasks": [{
    "id": "direct",
    "family": "direct_question",
    "prompt": "Explain routing.",
    "turns": ["first turn", "second turn"],
    "tags": ["read-only"],
    "scenario": {
      "name": "direct",
      "prompt_contains": ["second turn"],
      "require_assistant_for_each_user": true,
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
      "min_user_turns": 2,
      "min_assistant_turns": 2,
      "max_tool_requests": 2
    }
  }]
}
`
	if err := os.WriteFile(path, []byte(suite), 0o644); err != nil {
		t.Fatalf("write multi-turn suite: %v", err)
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
{"type":"message","role":"control_rails","content":"{\"task_family\":\"conversation\",\"permission_profile\":\"conversation_only\"}"}
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
