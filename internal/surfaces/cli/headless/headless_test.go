package headless

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"idiom/internal/platform/config"
	"idiom/internal/surfaces/cli/runrecord"
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
	}, nil, nil)

	if code != 0 {
		t.Fatalf("Run exit = %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}

	var output runrecord.RunOutputRecord
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		t.Fatalf("decode stdout completion: %v\n%s", err, stdout.String())
	}
	record := output.RunCompletionRecord
	if output.Answer != "answer" {
		t.Fatalf("json output should include final answer, got %q", output.Answer)
	}
	if record.SchemaVersion != runrecord.RunCompletionSchemaVersion || record.Type != runrecord.RunCompletionRecordType {
		t.Fatalf("unexpected completion identity: %#v", record)
	}
	if record.Artifacts["completion"] == "" || record.Artifacts["summary"] != summaryPath {
		t.Fatalf("completion artifacts not recorded: %#v", record.Artifacts)
	}

	completionPath := filepath.Join(root, filepath.FromSlash(record.Artifacts["completion"]))
	if _, err := os.Stat(completionPath); err != nil {
		t.Fatalf("completion artifact missing: %v", err)
	}
	var saved runrecord.RunCompletionRecord
	savedBody, err := os.ReadFile(completionPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(savedBody, &saved); err != nil {
		t.Fatalf("decode saved completion: %v", err)
	}
	if bytes.Contains(savedBody, []byte(`"answer"`)) {
		t.Fatalf("durable completion record should not persist raw answer: %s", string(savedBody))
	}
	if saved.Artifacts["completion"] == "" {
		t.Fatalf("saved completion should be self-referential: %#v", saved.Artifacts)
	}
	if got := saved.Metadata["active_mode"]; len(got) != 1 || got[0] != "headless_prompt" {
		t.Fatalf("completion metadata should record active headless mode: %#v", saved.Metadata)
	}
	if got := saved.Metadata["timeout_owner"]; len(got) != 1 || got[0] != "headless_policy_timeout" {
		t.Fatalf("completion metadata should record timeout owner: %#v", saved.Metadata)
	}
	if got := saved.Metadata["build_version"]; len(got) != 1 || got[0] == "" {
		t.Fatalf("completion metadata should record build identity: %#v", saved.Metadata)
	}
	if _, err := os.Stat(summaryPath); err != nil {
		t.Fatalf("summary artifact missing: %v", err)
	}
}

func TestRunHelpReturnsZero(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run([]string{"--help"}, bytes.NewReader(nil), &stdout, &stderr, func(config.Config, string, cmdruntime.RunOptions, io.Writer) (string, error) {
		t.Fatal("prompt runner should not run for help")
		return "", nil
	}, nil, nil)
	if code != 0 {
		t.Fatalf("help exit = %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "Usage of idiom run") {
		t.Fatalf("help output missing usage: %q", stderr.String())
	}
}

func TestRunPromptFailureWritesCompletion(t *testing.T) {
	root := t.TempDir()
	configPath := writeHeadlessTestConfig(t, root)

	var stdout, stderr bytes.Buffer
	code := Run(
		[]string{"--config", configPath, "--prompt", "fail this"},
		bytes.NewReader(nil),
		&stdout,
		&stderr,
		func(config.Config, string, cmdruntime.RunOptions, io.Writer) (string, error) {
			return "", errors.New("prompt failed")
		},
		nil,
		nil,
	)
	if code != 1 || !strings.Contains(stderr.String(), "prompt failed") {
		t.Fatalf("expected prompt failure, code=%d stderr=%s", code, stderr.String())
	}

	record, _ := readSingleHeadlessCompletion(t, root)
	if record.TerminalClass != runrecord.TerminalClassFailed || record.ExitCode != 1 || !strings.Contains(record.Error, "prompt failed") {
		t.Fatalf("unexpected failure completion: %#v", record)
	}
}

func TestRunTurnsFileUsesSingleSessionRunner(t *testing.T) {
	root := t.TempDir()
	configPath := writeHeadlessTestConfig(t, root)
	turnsPath := filepath.Join(t.TempDir(), "turns.json")
	if err := os.WriteFile(turnsPath, []byte(`["first turn","second turn"]`), 0o644); err != nil {
		t.Fatalf("write turns file: %v", err)
	}

	var (
		stdout, stderr bytes.Buffer
		gotPrompts     []string
		gotOpts        cmdruntime.RunOptions
	)
	code := Run(
		[]string{"--config", configPath, "--turns-file", turnsPath, "--json", "--resume", "prior-session"},
		bytes.NewReader(nil),
		&stdout,
		&stderr,
		func(config.Config, string, cmdruntime.RunOptions, io.Writer) (string, error) {
			t.Fatal("single prompt runner should not be called for --turns-file")
			return "", nil
		},
		func(_ config.Config, prompts []string, opts cmdruntime.RunOptions, w io.Writer) ([]string, error) {
			gotPrompts = append([]string(nil), prompts...)
			gotOpts = opts
			_, _ = w.Write([]byte("{\"type\":\"final\",\"content\":\"second answer\"}\n"))

			return []string{"first answer", "second answer"}, nil
		},
		nil,
	)
	if code != 0 {
		t.Fatalf("Run exit = %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if strings.Join(gotPrompts, "|") != "first turn|second turn" {
		t.Fatalf("unexpected prompts: %#v", gotPrompts)
	}
	if !gotOpts.JSONEvents || gotOpts.Resume != "prior-session" {
		t.Fatalf("unexpected opts: %#v", gotOpts)
	}
	if got := stdout.String(); got != "{\"type\":\"final\",\"content\":\"second answer\"}\n" {
		t.Fatalf("JSON turns mode must only emit runner JSONL, got %q", got)
	}

	record, _ := readSingleHeadlessCompletion(t, root)
	if record.Implementation != "idiom run" ||
		record.OutputMode != "jsonl" ||
		record.TerminalClass != runrecord.TerminalClassCompleted ||
		record.PromptSHA256 == "" ||
		record.AnswerSHA256 == "" {
		t.Fatalf("unexpected turns-file completion: %#v", record)
	}
	if got := record.Metadata["entrypoint"]; len(got) != 1 || got[0] != "idiom run" {
		t.Fatalf("entrypoint metadata = %#v", record.Metadata)
	}
	if got := record.Metadata["active_mode"]; len(got) != 1 || got[0] != "headless_turns_file" {
		t.Fatalf("active_mode metadata = %#v", record.Metadata)
	}
	if got := record.Metadata["turns_file"]; len(got) != 1 || got[0] != filepath.Clean(turnsPath) {
		t.Fatalf("turns_file metadata = %#v", record.Metadata)
	}
}

func TestRunRejectsPromptTurnsConflict(t *testing.T) {
	root := t.TempDir()
	configPath := writeHeadlessTestConfig(t, root)
	turnsPath := filepath.Join(t.TempDir(), "turns.txt")
	if err := os.WriteFile(turnsPath, []byte("first\n"), 0o644); err != nil {
		t.Fatalf("write turns file: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := Run(
		[]string{"--config", configPath, "--prompt", "x", "--turns-file", turnsPath},
		bytes.NewReader(nil),
		&stdout,
		&stderr,
		func(config.Config, string, cmdruntime.RunOptions, io.Writer) (string, error) {
			t.Fatal("prompt runner should not run for invalid flags")
			return "", nil
		},
		func(config.Config, []string, cmdruntime.RunOptions, io.Writer) ([]string, error) {
			t.Fatal("turns runner should not run for invalid flags")
			return nil, nil
		},
		nil,
	)
	if code != 2 || !strings.Contains(stderr.String(), "cannot be combined") {
		t.Fatalf("expected prompt/turns conflict, code=%d stderr=%s", code, stderr.String())
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
	}, nil, nil)

	if code != 0 {
		t.Fatalf("schema-valid run failed: code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	var output runrecord.RunOutputRecord
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		t.Fatalf("decode completion: %v\n%s", err, stdout.String())
	}
	record := output.RunCompletionRecord
	if record.OutputSchema != "answer.v1" || record.TerminalClass != "completed" {
		t.Fatalf("unexpected schema-valid record: %#v", record)
	}
	if output.Answer == "" {
		t.Fatal("schema-valid JSON output should include answer")
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
	}, nil, nil)
	if code == 0 {
		t.Fatal("schema mismatch should fail")
	}
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		t.Fatalf("decode failed completion: %v\n%s", err, stdout.String())
	}
	record = output.RunCompletionRecord
	if record.TerminalClass != "output_schema_failed" {
		t.Fatalf("unexpected schema failure record: %#v", record)
	}
}

func TestRunRejectsInvalidPolicyFlags(t *testing.T) {
	root := t.TempDir()
	configPath := writeHeadlessTestConfig(t, root)

	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "permission profile",
			args: []string{"--config", configPath, "--prompt", "x", "--permission-profile", "god-mode"},
			want: "--permission-profile",
		},
		{
			name: "timeout",
			args: []string{"--config", configPath, "--prompt", "x", "--timeout", "not-a-duration"},
			want: "--timeout",
		},
		{
			name: "max tool steps",
			args: []string{"--config", configPath, "--prompt", "x", "--max-tool-steps", "-1"},
			want: "--max-tool-steps",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := Run(tt.args, bytes.NewReader(nil), &stdout, &stderr, func(config.Config, string, cmdruntime.RunOptions, io.Writer) (string, error) {
				t.Fatal("prompt runner should not run for invalid flags")
				return "", nil
			}, nil, nil)
			if code != 2 {
				t.Fatalf("exit = %d, want 2 stdout=%s stderr=%s", code, stdout.String(), stderr.String())
			}
			if !strings.Contains(stderr.String(), tt.want) {
				t.Fatalf("stderr %q does not mention %q", stderr.String(), tt.want)
			}
		})
	}
}

func TestRunRejectsInvalidSessionIDsBeforeModelExecution(t *testing.T) {
	root := t.TempDir()
	configPath := writeHeadlessTestConfig(t, root)

	tests := []string{
		"../escape",
		"a/b",
		`a\b`,
		strings.Repeat("a", 129),
	}
	for _, sessionID := range tests {
		t.Run(sessionID, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := Run([]string{
				"--config", configPath,
				"--prompt", "hello",
				"--output", "json",
				"--session", sessionID,
			}, bytes.NewReader(nil), &stdout, &stderr, func(config.Config, string, cmdruntime.RunOptions, io.Writer) (string, error) {
				t.Fatal("prompt runner should not run for invalid session id")
				return "", nil
			}, nil, nil)

			if code != 2 {
				t.Fatalf("exit = %d, want 2 stdout=%s stderr=%s", code, stdout.String(), stderr.String())
			}
			if !strings.Contains(stderr.String(), "invalid run id") {
				t.Fatalf("stderr should explain invalid run id, got %q", stderr.String())
			}
		})
	}
}

func TestRunFailsWhenCompletionCannotBeSaved(t *testing.T) {
	root := t.TempDir()
	configPath := writeHeadlessTestConfig(t, root)
	if err := os.WriteFile(filepath.Join(root, ".idiom"), []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"--config", configPath,
		"--prompt", "hello",
		"--output", "json",
		"--session", "completion-save-fails",
	}, bytes.NewReader(nil), &stdout, &stderr, func(config.Config, string, cmdruntime.RunOptions, io.Writer) (string, error) {
		return "ok", nil
	}, nil, nil)

	if code == 0 {
		t.Fatal("completion save failure should fail the run")
	}
	var output runrecord.RunOutputRecord
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		t.Fatalf("decode completion: %v\n%s", err, stdout.String())
	}
	if output.TerminalClass != "artifact_failed" || output.Error == "" {
		t.Fatalf("unexpected artifact failure record: %#v", output.RunCompletionRecord)
	}
	if output.Artifacts["completion"] != "" {
		t.Fatalf("failed completion save must not advertise a saved completion artifact: %#v", output.Artifacts)
	}
}

func TestRunPolicyTimeoutProjectsToRuntimeRequestTimeout(t *testing.T) {
	root := t.TempDir()
	configPath := writeHeadlessTestConfig(t, root)

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"--config", configPath,
		"--prompt", "bounded review",
		"--output", "json",
		"--timeout", "2s",
		"--session", "run-timeout-projection",
	}, bytes.NewReader(nil), &stdout, &stderr, func(cfg config.Config, _ string, _ cmdruntime.RunOptions, _ io.Writer) (string, error) {
		if config.Default().Chat.RequestTimeoutSeconds == 2 {
			t.Fatal("test precondition invalid: default request timeout should differ from override")
		}
		if cfg.Chat.RequestTimeoutSeconds != 2 || cfg.Chat.ServerStartTimeoutSecs != 0 {
			t.Fatalf("timeout projection = request %d start %d, want 2/0", cfg.Chat.RequestTimeoutSeconds, cfg.Chat.ServerStartTimeoutSecs)
		}
		return "ok", nil
	}, nil, nil)

	if code != 0 {
		t.Fatalf("timeout projection run failed stdout=%s stderr=%s", stdout.String(), stderr.String())
	}

	var output runrecord.RunOutputRecord
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		t.Fatalf("decode timeout projection completion: %v\n%s", err, stdout.String())
	}
	if got := output.Metadata["timeout_owner"]; len(got) != 1 || got[0] != "headless_policy_timeout" {
		t.Fatalf("timeout owner metadata should identify policy timeout: %#v", output.Metadata)
	}
}

func TestRunCommandCapturesStdoutStderrAndExitCode(t *testing.T) {
	root := t.TempDir()
	check := runCommand(context.Background(), "check", "printf 'out'; printf 'err' >&2; exit 7", headlessCommandTestConfig(root))

	if check.ExitCode != 7 {
		t.Fatalf("ExitCode = %d, want 7; check=%#v", check.ExitCode, check)
	}
	if check.Stdout != "out" {
		t.Fatalf("Stdout = %q, want out", check.Stdout)
	}
	if check.Stderr != "err" {
		t.Fatalf("Stderr = %q, want err", check.Stderr)
	}
	if check.Name != "check" || check.Command == "" {
		t.Fatalf("check identity not preserved: %#v", check)
	}
}

func TestRunCommandUsesConfiguredCommandDirAndBoundsOutput(t *testing.T) {
	root := t.TempDir()
	check := runCommand(context.Background(), "pwd", "pwd; printf '%05000d' 0", headlessCommandTestConfig(root))

	if check.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; check=%#v", check.ExitCode, check)
	}
	if !strings.HasPrefix(check.Stdout, root) {
		t.Fatalf("Stdout should start with configured cwd %q, got %q", root, check.Stdout[:min(len(check.Stdout), 80)])
	}
	if len(check.Stdout) != 4000 {
		t.Fatalf("Stdout length = %d, want bounded 4000", len(check.Stdout))
	}
}

func TestRunCommandHonorsContextTimeout(t *testing.T) {
	root := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	check := runCommand(ctx, "timeout", "while true; do :; done", headlessCommandTestConfig(root))

	if check.ExitCode == 0 {
		t.Fatalf("timeout command should not exit 0: %#v", check)
	}
	if check.DurationMS <= 0 || check.DurationMS > 1000 {
		t.Fatalf("timeout duration should be bounded, got %dms in %#v", check.DurationMS, check)
	}
}

func headlessCommandTestConfig(root string) config.Config {
	cfg := config.Config{}
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.Shell = "/bin/sh"

	return cfg
}

func TestWriteGitDiffWritesWorkspacePatch(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	root := t.TempDir()
	runHeadlessGit(t, root, "init")
	runHeadlessGit(t, root, "config", "user.email", "test@example.com")
	runHeadlessGit(t, root, "config", "user.name", "Test User")

	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("before\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	runHeadlessGit(t, root, "add", "file.txt")
	runHeadlessGit(t, root, "commit", "-m", "initial")

	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("after\n"), 0o644); err != nil {
		t.Fatalf("modify file: %v", err)
	}

	patchPath := filepath.Join(t.TempDir(), "diff.patch")
	if err := writeGitDiff(patchPath, headlessCommandTestConfig(root)); err != nil {
		t.Fatalf("writeGitDiff failed: %v", err)
	}

	body, err := os.ReadFile(patchPath)
	if err != nil {
		t.Fatalf("read patch: %v", err)
	}
	text := string(body)
	for _, want := range []string{"diff --git a/file.txt b/file.txt", "-before", "+after"} {
		if !strings.Contains(text, want) {
			t.Fatalf("patch missing %q:\n%s", want, text)
		}
	}

	info, err := os.Stat(patchPath)
	if err != nil {
		t.Fatalf("stat patch: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("patch mode = %o, want 600", got)
	}
}

func runHeadlessGit(t *testing.T, dir string, args ...string) {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
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
	}, nil, nil)

	if code == 0 {
		t.Fatalf("degraded run should fail when --fail-on-degraded is set")
	}
	var record runrecord.RunCompletionRecord
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
	}, nil, nil)

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

func TestServeMuxReportsHealthAndStatus(t *testing.T) {
	server := httptest.NewServer(newServeMux())
	defer server.Close()

	for _, path := range []string{"/healthz", "/v1/status"} {
		resp, err := http.Get(server.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s status = %d", path, resp.StatusCode)
		}

		var body map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
		if body["schema_version"] != "idiom.headless.server.v1" {
			t.Fatalf("%s schema_version = %v", path, body["schema_version"])
		}
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

func readSingleHeadlessCompletion(t *testing.T, root string) (runrecord.RunCompletionRecord, []byte) {
	t.Helper()

	matches, err := filepath.Glob(filepath.Join(root, ".idiom", "runs", "*.completion.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected one completion record, got %#v", matches)
	}

	body, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}

	var record runrecord.RunCompletionRecord
	if err := json.Unmarshal(body, &record); err != nil {
		t.Fatalf("decode completion record: %v\n%s", err, string(body))
	}

	return record, body
}
