package headless

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"idiom/internal/platform/config"
	cmdruntime "idiom/internal/surfaces/cli/runtime"
)

func TestRunAppliesPolicyOverridesAndWritesCompletion(t *testing.T) {
	root := t.TempDir()
	configPath := writeHeadlessTestConfig(t, root)
	summaryPath := filepath.Join(root, "summary.json")

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"--config", configPath,
		"--prompt", "summarize the repo",
		"--output", "json",
		"--session", "run-test-1",
		"--permission-profile", "read-only",
		"--deny-tool", "shell",
		"--allow-tool", "read_file",
		"--deny-path", "secrets/**",
		"--max-tool-steps", "3",
		"--timeout", "2s",
		"--summary-out", summaryPath,
	}, bytes.NewReader(nil), &stdout, &stderr, func(cfg config.Config, prompt string, opts cmdruntime.RunOptions, _ io.Writer) (string, error) {
		if prompt != "summarize the repo" {
			t.Fatalf("prompt = %q", prompt)
		}
		if opts.JSONEvents {
			t.Fatal("json output mode should not force JSONL event output")
		}
		if cfg.Workspace.EnableWrites {
			t.Fatal("read-only permission profile should disable writes")
		}
		if cfg.Chat.MaxToolSteps != 3 {
			t.Fatalf("MaxToolSteps = %d, want 3", cfg.Chat.MaxToolSteps)
		}
		if cfg.Chat.RequestTimeoutSeconds != 2 || cfg.Chat.ServerStartTimeoutSecs != 0 {
			t.Fatalf("timeout projection = request %d start %d", cfg.Chat.RequestTimeoutSeconds, cfg.Chat.ServerStartTimeoutSecs)
		}
		if got := cfg.Workspace.ToolRules["shell"][0].Decision; got != "deny" {
			t.Fatalf("shell first decision = %q", got)
		}
		if got := cfg.Workspace.ToolRules["read_file"][0].Decision; got != "allow" {
			t.Fatalf("read_file decision = %q", got)
		}
		if got := cfg.Workspace.ToolRules["edit_file"][0]; got.Pattern != "secrets/**" || got.Decision != "deny" {
			t.Fatalf("edit_file deny rule = %#v", got)
		}

		return "answer", nil
	}, nil)

	if code != 0 {
		t.Fatalf("Run exit = %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}

	var record RunCompletionRecord
	if err := json.Unmarshal(stdout.Bytes(), &record); err != nil {
		t.Fatalf("decode stdout completion: %v\n%s", err, stdout.String())
	}
	if record.SchemaVersion != RunCompletionSchemaVersion || record.Type != RunCompletionRecordType {
		t.Fatalf("unexpected completion identity: %#v", record)
	}
	if record.Artifacts["completion"] == "" || record.Artifacts["summary"] != summaryPath {
		t.Fatalf("completion artifacts not recorded: %#v", record.Artifacts)
	}

	completionPath := filepath.Join(root, filepath.FromSlash(record.Artifacts["completion"]))
	if _, err := os.Stat(completionPath); err != nil {
		t.Fatalf("completion artifact missing: %v", err)
	}
	var saved RunCompletionRecord
	savedBody, err := os.ReadFile(completionPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(savedBody, &saved); err != nil {
		t.Fatalf("decode saved completion: %v", err)
	}
	if saved.Artifacts["completion"] == "" {
		t.Fatalf("saved completion should be self-referential: %#v", saved.Artifacts)
	}
	if _, err := os.Stat(summaryPath); err != nil {
		t.Fatalf("summary artifact missing: %v", err)
	}
}

func TestRunValidatesOutputSchema(t *testing.T) {
	root := t.TempDir()
	configPath := writeHeadlessTestConfig(t, root)

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"--config", configPath,
		"--prompt", "hello",
		"--output", "json",
		"--output-schema", "answer.v1",
		"--session", "run-schema",
	}, bytes.NewReader(nil), &stdout, &stderr, func(config.Config, string, cmdruntime.RunOptions, io.Writer) (string, error) {
		return `{"schema_version":"answer.v1","answer":"ok"}`, nil
	}, nil)

	if code != 0 {
		t.Fatalf("schema-valid run failed: code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	var record RunCompletionRecord
	if err := json.Unmarshal(stdout.Bytes(), &record); err != nil {
		t.Fatalf("decode completion: %v\n%s", err, stdout.String())
	}
	if record.OutputSchema != "answer.v1" || record.TerminalClass != "completed" {
		t.Fatalf("unexpected schema-valid record: %#v", record)
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{
		"--config", configPath,
		"--prompt", "hello",
		"--output", "json",
		"--output-schema", "answer.v1",
		"--session", "run-schema-bad",
	}, bytes.NewReader(nil), &stdout, &stderr, func(config.Config, string, cmdruntime.RunOptions, io.Writer) (string, error) {
		return `{"schema_version":"wrong.v1","answer":"ok"}`, nil
	}, nil)
	if code == 0 {
		t.Fatal("schema mismatch should fail")
	}
	if err := json.Unmarshal(stdout.Bytes(), &record); err != nil {
		t.Fatalf("decode failed completion: %v\n%s", err, stdout.String())
	}
	if record.TerminalClass != "output_schema_failed" {
		t.Fatalf("unexpected schema failure record: %#v", record)
	}
}

func TestRunFailOnDegradedRejectsUnsupportedApprovalPolicy(t *testing.T) {
	root := t.TempDir()
	configPath := writeHeadlessTestConfig(t, root)

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"--config", configPath,
		"--prompt", "hello",
		"--output", "json",
		"--approval-policy", "on-request",
		"--fail-on-degraded",
		"--session", "run-degraded",
	}, bytes.NewReader(nil), &stdout, &stderr, func(config.Config, string, cmdruntime.RunOptions, io.Writer) (string, error) {
		return "ok", nil
	}, nil)

	if code == 0 {
		t.Fatalf("degraded run should fail when --fail-on-degraded is set")
	}
	var record RunCompletionRecord
	if err := json.Unmarshal(stdout.Bytes(), &record); err != nil {
		t.Fatalf("decode completion: %v\n%s", err, stdout.String())
	}
	if record.TerminalClass != "degraded" || len(record.UnsupportedFlags) == 0 {
		t.Fatalf("unexpected degraded record: %#v", record)
	}
}

func TestRunJSONFlagEmitsJSONLOnly(t *testing.T) {
	root := t.TempDir()
	configPath := writeHeadlessTestConfig(t, root)

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"--config", configPath,
		"--prompt", "hello",
		"--json",
		"--session", "run-jsonl",
	}, bytes.NewReader(nil), &stdout, &stderr, func(_ config.Config, _ string, opts cmdruntime.RunOptions, w io.Writer) (string, error) {
		if !opts.JSONEvents {
			t.Fatal("--json should request runtime JSON events")
		}
		_, _ = w.Write([]byte("{\"type\":\"final\",\"content\":\"ok\"}\n"))

		return "ok", nil
	}, nil)

	if code != 0 {
		t.Fatalf("Run exit = %d stderr=%s", code, stderr.String())
	}
	if strings.Contains(stdout.String(), "\nok\n") {
		t.Fatalf("JSONL mode should not append plain answer: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), `"type":"final"`) {
		t.Fatalf("expected runtime JSONL event, got %q", stdout.String())
	}
}

func TestSessionExportSanitizesAndReplayReturnsPrompts(t *testing.T) {
	root := t.TempDir()
	configPath := writeHeadlessTestConfig(t, root)
	logDir := filepath.Join(root, "logs", "conversations")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(logDir, "session-1.jsonl")
	if err := os.WriteFile(logPath, []byte(
		`{"type":"message","role":"user","content":"secret prompt"}`+"\n"+
			`{"type":"tool_result","tool":"read_file","summary":"read README","content":"raw file body"}`+"\n",
	), 0o600); err != nil {
		t.Fatal(err)
	}

	exportPath := filepath.Join(root, "export.json")
	var stdout, stderr bytes.Buffer
	code := RunSession([]string{"export", "--config", configPath, "--session", "session-1", "--out", exportPath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("export exit = %d stderr=%s", code, stderr.String())
	}
	body, err := os.ReadFile(exportPath)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(body, []byte("secret prompt")) || bytes.Contains(body, []byte("raw file body")) {
		t.Fatalf("sanitized export leaked raw content: %s", string(body))
	}
	if !bytes.Contains(body, []byte("read README")) {
		t.Fatalf("sanitized export should retain summaries: %s", string(body))
	}

	stdout.Reset()
	stderr.Reset()
	code = RunSession([]string{"replay", "--config", configPath, "--session", "session-1"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("replay exit = %d stderr=%s", code, stderr.String())
	}
	if strings.TrimSpace(stdout.String()) != "secret prompt" {
		t.Fatalf("replay stdout = %q", stdout.String())
	}
}

func TestContractInspectReportsConfiguredPolicy(t *testing.T) {
	root := t.TempDir()
	configPath := writeHeadlessTestConfig(t, root)

	var stdout, stderr bytes.Buffer
	code := RunContract([]string{"inspect", "--config", configPath, "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("contract inspect exit = %d stderr=%s", code, stderr.String())
	}

	var report map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	if report["schema_version"] != "idiom.headless.contract_report.v1" {
		t.Fatalf("unexpected report: %#v", report)
	}
	gotRoot, _ := filepath.EvalSymlinks(report["workspace_root"].(string))
	wantRoot, _ := filepath.EvalSymlinks(root)
	if gotRoot != wantRoot {
		t.Fatalf("workspace root = %v, want %s", report["workspace_root"], root)
	}
}

func writeHeadlessTestConfig(t *testing.T, root string) string {
	t.Helper()

	cfg := config.Default()
	cfg.Chat.DebugLogDir = filepath.Join(root, "logs", "conversations")
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.Shell = "/bin/sh"
	cfg.Workspace.EnableShell = true
	cfg.Workspace.EnableWrites = true

	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, "config.json")
	if err := os.WriteFile(configPath, data, 0o600); err != nil {
		t.Fatal(err)
	}

	return configPath
}
