package root
import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"idiom/internal/config"
	"idiom/internal/recovery"
	"idiom/internal/workspace"
)

func nopList(config.Config, io.Writer) error { return nil }
func nopPrompts(config.Config, []string, runOptions, io.Writer) ([]string, error) {
	return nil, nil
}

func TestRunPromptModeUsesInjectedRunner(t *testing.T) {
	configPath := writeMainConfig(t)
	var stdout, stderr bytes.Buffer
	var gotPrompt string
	var gotOpts runOptions
	code := run(
		[]string{"-config", configPath, "-prompt", "explain routing"},
		&stdout,
		&stderr,
		func(_ config.Config, prompt string, opts runOptions, _ io.Writer) (string, error) {
			gotPrompt = prompt
			gotOpts = opts
			return "answer text", nil
		},
		nopPrompts,
		func(config.Config, runOptions) error {
			t.Fatal("TUI runner should not be called in prompt mode")
			return nil
		},
		func(config.Config, runOptions) error {
			t.Fatal("server runner should not be called in prompt mode")
			return nil
		},
		nopList,
	)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d stderr=%s", code, stderr.String())
	}
	if gotPrompt != "explain routing" || strings.TrimSpace(stdout.String()) != "answer text" {
		t.Fatalf("unexpected prompt output prompt=%q stdout=%q", gotPrompt, stdout.String())
	}
	if gotOpts.JSONEvents || gotOpts.Resume != "" {
		t.Fatalf("opts should default to empty: %#v", gotOpts)
	}
}

func TestRunPromptModeJSONSuppressesFinalPrint(t *testing.T) {
	configPath := writeMainConfig(t)
	var stdout, stderr bytes.Buffer
	var gotOpts runOptions
	code := run(
		[]string{"-config", configPath, "-prompt", "x", "-json"},
		&stdout,
		&stderr,
		func(_ config.Config, _ string, opts runOptions, w io.Writer) (string, error) {
			gotOpts = opts
			_, _ = w.Write([]byte("{\"type\":\"final\",\"content\":\"ok\"}\n"))
			return "ok", nil
		},
		nopPrompts,
		func(config.Config, runOptions) error { return nil },
		func(config.Config, runOptions) error { return nil },
		nopList,
	)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d stderr=%s", code, stderr.String())
	}
	if !gotOpts.JSONEvents {
		t.Fatalf("--json should set JSONEvents true")
	}
	if strings.Contains(stdout.String(), "ok\n") && !strings.Contains(stdout.String(), "\"type\":\"final\"") {
		t.Fatalf("--json mode must not append the answer string after JSONL: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "\"type\":\"final\"") {
		t.Fatalf("expected JSONL final event in stdout, got %q", stdout.String())
	}
}

func TestRunPromptModeForwardsResume(t *testing.T) {
	configPath := writeMainConfig(t)
	var stdout, stderr bytes.Buffer
	var gotOpts runOptions
	code := run(
		[]string{"-config", configPath, "-prompt", "x", "-resume", "20260101-000000.000"},
		&stdout,
		&stderr,
		func(_ config.Config, _ string, opts runOptions, _ io.Writer) (string, error) {
			gotOpts = opts
			return "ok", nil
		},
		nopPrompts,
		func(config.Config, runOptions) error { return nil },
		func(config.Config, runOptions) error { return nil },
		nopList,
	)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d stderr=%s", code, stderr.String())
	}
	if gotOpts.Resume != "20260101-000000.000" {
		t.Fatalf("resume id not forwarded: %#v", gotOpts)
	}
}

func TestRunTurnsFileUsesSingleSessionRunner(t *testing.T) {
	configPath := writeMainConfig(t)
	turnsPath := filepath.Join(t.TempDir(), "turns.json")
	if err := os.WriteFile(turnsPath, []byte(`["first turn","second turn"]`), 0o644); err != nil {
		t.Fatalf("write turns file: %v", err)
	}

	var stdout, stderr bytes.Buffer
	var gotPrompts []string
	var gotOpts runOptions
	code := run(
		[]string{"-config", configPath, "-turns-file", turnsPath, "-json", "-resume", "prior-session"},
		&stdout,
		&stderr,
		func(config.Config, string, runOptions, io.Writer) (string, error) {
			t.Fatal("single prompt runner should not be called for --turns-file")
			return "", nil
		},
		func(_ config.Config, prompts []string, opts runOptions, w io.Writer) ([]string, error) {
			gotPrompts = append([]string(nil), prompts...)
			gotOpts = opts
			_, _ = w.Write([]byte("{\"type\":\"final\",\"content\":\"second answer\"}\n"))
			return []string{"first answer", "second answer"}, nil
		},
		func(config.Config, runOptions) error { return nil },
		func(config.Config, runOptions) error { return nil },
		nopList,
	)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d stderr=%s", code, stderr.String())
	}
	if strings.Join(gotPrompts, "|") != "first turn|second turn" {
		t.Fatalf("unexpected prompts: %#v", gotPrompts)
	}
	if !gotOpts.JSONEvents || gotOpts.Resume != "prior-session" {
		t.Fatalf("unexpected opts: %#v", gotOpts)
	}
	if !strings.Contains(stdout.String(), `"type":"final"`) || strings.Contains(stdout.String(), "second answer\n") && !strings.Contains(stdout.String(), `"content":"second answer"`) {
		t.Fatalf("JSON turns mode should only emit runner JSONL, got %q", stdout.String())
	}
}

func TestRunTurnsFileRejectsPromptConflict(t *testing.T) {
	configPath := writeMainConfig(t)
	turnsPath := filepath.Join(t.TempDir(), "turns.txt")
	if err := os.WriteFile(turnsPath, []byte("first\n"), 0o644); err != nil {
		t.Fatalf("write turns file: %v", err)
	}
	var stdout, stderr bytes.Buffer
	code := run(
		[]string{"-config", configPath, "-prompt", "x", "-turns-file", turnsPath},
		&stdout,
		&stderr,
		func(config.Config, string, runOptions, io.Writer) (string, error) { return "", nil },
		nopPrompts,
		func(config.Config, runOptions) error { return nil },
		func(config.Config, runOptions) error { return nil },
		nopList,
	)
	if code != 2 || !strings.Contains(stderr.String(), "mutually exclusive") {
		t.Fatalf("expected prompt/turns conflict, code=%d stderr=%s", code, stderr.String())
	}
}

func TestRunListSessionsCallsListRunner(t *testing.T) {
	configPath := writeMainConfig(t)
	var stdout, stderr bytes.Buffer
	called := false
	code := run(
		[]string{"-config", configPath, "-list-sessions"},
		&stdout,
		&stderr,
		func(config.Config, string, runOptions, io.Writer) (string, error) {
			t.Fatal("prompt runner should not be called")
			return "", nil
		},
		nopPrompts,
		func(config.Config, runOptions) error {
			t.Fatal("TUI runner should not be called")
			return nil
		},
		func(config.Config, runOptions) error {
			t.Fatal("server runner should not be called")
			return nil
		},
		func(_ config.Config, w io.Writer) error {
			called = true
			_, _ = w.Write([]byte("session-list\n"))
			return nil
		},
	)
	if code != 0 || !called || !strings.Contains(stdout.String(), "session-list") {
		t.Fatalf("list-sessions failed code=%d called=%t stdout=%q", code, called, stdout.String())
	}
}

func TestRunServerOnlyUsesInjectedRunner(t *testing.T) {
	configPath := writeMainConfig(t)
	var stdout, stderr bytes.Buffer
	called := false
	code := run(
		[]string{"-config", configPath, "-server-only"},
		&stdout,
		&stderr,
		func(config.Config, string, runOptions, io.Writer) (string, error) {
			t.Fatal("prompt runner should not be called in server-only mode")
			return "", nil
		},
		nopPrompts,
		func(config.Config, runOptions) error {
			t.Fatal("TUI runner should not be called in server-only mode")
			return nil
		},
		func(config.Config, runOptions) error {
			called = true
			return nil
		},
		nopList,
	)
	if code != 0 || !called {
		t.Fatalf("expected server runner success, code=%d called=%t stderr=%s", code, called, stderr.String())
	}
}

func TestRunDefaultUsesTUIRunner(t *testing.T) {
	configPath := writeMainConfig(t)
	var stdout, stderr bytes.Buffer
	called := false
	code := run(
		[]string{"-config", configPath},
		&stdout,
		&stderr,
		func(config.Config, string, runOptions, io.Writer) (string, error) {
			t.Fatal("prompt runner should not be called in default mode")
			return "", nil
		},
		nopPrompts,
		func(config.Config, runOptions) error {
			called = true
			return nil
		},
		func(config.Config, runOptions) error {
			t.Fatal("server runner should not be called in default mode")
			return nil
		},
		nopList,
	)
	if code != 0 || !called {
		t.Fatalf("expected TUI runner success, code=%d called=%t stderr=%s", code, called, stderr.String())
	}
}

func TestRunDefaultLoadsWorkspaceToolRulesFromRootConfig(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.json")
	configJSON := fmt.Sprintf(`{
  "workspace": {
    "root": %q,
    "cwd": %q,
    "tool_rules": {
      "read_file": [
        {"pattern": ".idiom/deny-read-test.txt", "decision": "deny"},
        {"pattern": "*", "decision": "allow"}
      ]
    }
  }
}`, root, root)
	if err := os.WriteFile(configPath, []byte(configJSON+"\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var stdout, stderr bytes.Buffer
	var gotCfg config.Config
	code := run(
		[]string{"-config", configPath},
		&stdout,
		&stderr,
		func(config.Config, string, runOptions, io.Writer) (string, error) {
			t.Fatal("prompt runner should not be called in default mode")
			return "", nil
		},
		nopPrompts,
		func(cfg config.Config, _ runOptions) error {
			gotCfg = cfg
			return nil
		},
		func(config.Config, runOptions) error {
			t.Fatal("server runner should not be called in default mode")
			return nil
		},
		nopList,
	)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d stderr=%s", code, stderr.String())
	}
	mgr := workspace.NewManager(gotCfg.Workspace)
	decision := mgr.EvaluateConfiguredToolRule(workspace.ToolRequest{Tool: "read_file", Path: ".idiom/deny-read-test.txt"})
	if decision != workspace.ToolRuleDeny {
		t.Fatalf("default TUI config must carry root config tool_rules deny, got %s in %#v", decision, gotCfg.Workspace.ToolRules)
	}
}

func TestRunReportsConfigAndRunnerErrors(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(
		[]string{"-config", filepath.Join(t.TempDir(), "missing.json")},
		&stdout,
		&stderr,
		func(config.Config, string, runOptions, io.Writer) (string, error) { return "", nil },
		nopPrompts,
		func(config.Config, runOptions) error { return nil },
		func(config.Config, runOptions) error { return nil },
		nopList,
	)
	if code != 1 || !strings.Contains(stderr.String(), "read config") {
		t.Fatalf("expected config read failure, code=%d stderr=%s", code, stderr.String())
	}

	configPath := writeMainConfig(t)
	stdout.Reset()
	stderr.Reset()
	code = run(
		[]string{"-config", configPath, "-prompt", "fail"},
		&stdout,
		&stderr,
		func(config.Config, string, runOptions, io.Writer) (string, error) {
			return "", errors.New("prompt failed")
		},
		nopPrompts,
		func(config.Config, runOptions) error { return nil },
		func(config.Config, runOptions) error { return nil },
		nopList,
	)
	if code != 1 || !strings.Contains(stderr.String(), "prompt failed") {
		t.Fatalf("expected prompt failure, code=%d stderr=%s", code, stderr.String())
	}
}

func TestRunRejectsInvalidFlags(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(
		[]string{"-does-not-exist"},
		&stdout,
		&stderr,
		func(config.Config, string, runOptions, io.Writer) (string, error) { return "", nil },
		nopPrompts,
		func(config.Config, runOptions) error { return nil },
		func(config.Config, runOptions) error { return nil },
		nopList,
	)
	if code != 2 || !strings.Contains(stderr.String(), "flag provided but not defined") {
		t.Fatalf("expected flag parse failure, code=%d stderr=%s", code, stderr.String())
	}
}

// TestRunVersionFlagShortCircuits verifies that `idiom --version`
// prints version info and exits 0 without touching the prompt, TUI,
// server, or list-sessions runners. The flag must work against any
// cwd — including ones without a config.json — so debugging questions
// like "what version is this binary?" never depend on project state.
func TestRunVersionFlagShortCircuits(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(
		[]string{"--version"},
		&stdout,
		&stderr,
		func(config.Config, string, runOptions, io.Writer) (string, error) {
			t.Fatal("prompt runner must not be called for --version")
			return "", nil
		},
		nopPrompts,
		func(config.Config, runOptions) error {
			t.Fatal("TUI runner must not be called for --version")
			return nil
		},
		func(config.Config, runOptions) error {
			t.Fatal("server runner must not be called for --version")
			return nil
		},
		func(config.Config, io.Writer) error {
			t.Fatal("list-sessions runner must not be called for --version")
			return nil
		},
	)
	if code != 0 {
		t.Fatalf("expected exit 0 for --version, got %d stderr=%q", code, stderr.String())
	}
	out := stdout.String()
	for _, marker := range []string{"version: ", "go: ", "os/arch: "} {
		if !strings.Contains(out, marker) {
			t.Fatalf("--version output missing %q line; got: %s", marker, out)
		}
	}
}

// TestRunSingleDashVersionFlagAlsoWorks verifies the `-version` form
// (single dash, Go-flag-package-style) is equivalent to `--version`.
// Go's flag package accepts both spellings; users debugging in a hurry
// shouldn't have to remember which one is canonical.
func TestRunSingleDashVersionFlagAlsoWorks(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(
		[]string{"-version"},
		&stdout,
		&stderr,
		func(config.Config, string, runOptions, io.Writer) (string, error) { return "", nil },
		nopPrompts,
		func(config.Config, runOptions) error { return nil },
		func(config.Config, runOptions) error { return nil },
		nopList,
	)
	if code != 0 {
		t.Fatalf("expected exit 0 for -version, got %d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "version: ") {
		t.Fatalf("-version output missing 'version: ' line; got: %s", stdout.String())
	}
}

func TestSessionIDFromPath(t *testing.T) {
	cases := map[string]string{
		"logs/conversations/20260418-055543.504.jsonl": "20260418-055543.504",
		"/abs/path/sess.jsonl":                         "sess",
		"plain":                                        "plain",
	}
	for in, want := range cases {
		if got := sessionIDFromPath(in); got != want {
			t.Errorf("sessionIDFromPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCommandTelemetryNameClassifiesAuditSubcommands(t *testing.T) {
	if got := commandTelemetryName([]string{"audit", "watch", "--once"}); got != "audit:watch" {
		t.Fatalf("commandTelemetryName audit watch = %q, want audit:watch", got)
	}
}

func TestCommandTelemetryNameClassifiesDoctor(t *testing.T) {
	if got := commandTelemetryName([]string{"doctor", "--json", "--profile", "implement"}); got != "doctor" {
		t.Fatalf("commandTelemetryName doctor = %q, want doctor", got)
	}
}

func TestCommandTelemetryNameClassifiesLocalOpsBenchmark(t *testing.T) {
	if got := commandTelemetryName([]string{"local-ops", "benchmark", "--json"}); got != "local-ops:benchmark" {
		t.Fatalf("commandTelemetryName local-ops benchmark = %q, want local-ops:benchmark", got)
	}
}

func TestRunMainWritesTelemetryForDirectTool(t *testing.T) {
	telemetryDir := t.TempDir()
	t.Setenv("IDIOM_TELEMETRY_DIR", telemetryDir)
	configPath := writeMainConfig(t)

	var stdout, stderr bytes.Buffer
	code := runMain([]string{"tool", "pwd", "-config", configPath, "--caller-id", "cli-smoke", "--request-id", "req-cli", "--trace-id", "trace:cli"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"Tool": "pwd"`) {
		t.Fatalf("expected tool result on stdout, got %q", stdout.String())
	}

	entries, err := os.ReadDir(telemetryDir)
	if err != nil {
		t.Fatalf("read telemetry dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one telemetry file, got %d", len(entries))
	}
	records := readMainTelemetryRecords(t, filepath.Join(telemetryDir, entries[0].Name()))
	if !recordTypesContain(records, "process_start", "tool_request", "tool_result", "process_exit") {
		t.Fatalf("telemetry missing expected records: %#v", records)
	}
	var sawDirectToolResult bool
	for _, record := range records {
		if record["type"] == "tool_result" && record["tool"] == "pwd" && record["agent"] == "direct" {
			if record["transport"] != "cli" ||
				record["profile"] != "default" ||
				record["caller_id"] != "cli-smoke" ||
				record["request_id"] != "req-cli" ||
				record["trace_id"] != "trace:cli" ||
				record["sidecar_schema_version"] != workspace.ToolSidecarEnvelopeVersion ||
				record["contract_schema_version"] != workspace.ToolSchemaExportVersion ||
				record["contract_id"] != "idiom.tool.v1:pwd" {
				t.Fatalf("direct tool telemetry attribution mismatch: %#v", record)
			}
			sawDirectToolResult = true
		}
	}
	if !sawDirectToolResult {
		t.Fatalf("missing direct pwd tool result telemetry: %#v", records)
	}
}

func TestRunMainDirectToolJSONIncludesSidecarEnvelopeAndLegacyFields(t *testing.T) {
	configPath := writeMainConfig(t)

	var stdout, stderr bytes.Buffer
	code := runMain([]string{"tool", "pwd", "-config", configPath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d stderr=%s", code, stderr.String())
	}
	var payload struct {
		Tool    string `json:"Tool"`
		Summary string `json:"Summary"`
		Output  string `json:"Output"`
		Error   string `json:"Error"`
		Sidecar struct {
			SchemaVersion    string         `json:"schema_version"`
			Tool             string         `json:"tool"`
			Profile          string         `json:"profile"`
			Contract         map[string]any `json:"contract"`
			Warnings         []any          `json:"warnings"`
			AuditEventIDs    []any          `json:"audit_event_ids"`
			StructuredOutput map[string]any `json:"structured_output"`
			Error            any            `json:"error"`
		} `json:"sidecar"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("decode tool output: %v\n%s", err, stdout.String())
	}
	if payload.Tool != "pwd" || payload.Summary == "" || payload.Output == "" || payload.Error != "" {
		t.Fatalf("legacy fields changed: %#v", payload)
	}
	if payload.Sidecar.SchemaVersion != "idiom.tool.v1" || payload.Sidecar.Tool != "pwd" || payload.Sidecar.Profile != "default" {
		t.Fatalf("missing sidecar identity fields: %#v", payload.Sidecar)
	}
	if payload.Sidecar.Contract["id"] != "idiom.tool.v1:pwd" || payload.Sidecar.Contract["metadata"] == nil {
		t.Fatalf("missing sidecar contract metadata: %#v", payload.Sidecar.Contract)
	}
	if payload.Sidecar.Warnings == nil || payload.Sidecar.AuditEventIDs == nil {
		t.Fatalf("warnings and audit ids should be parseable arrays: %#v", payload.Sidecar)
	}
	if payload.Sidecar.StructuredOutput["format"] != "text" {
		t.Fatalf("expected text output to be wrapped structurally, got %#v", payload.Sidecar.StructuredOutput)
	}
	if payload.Sidecar.Error != nil {
		t.Fatalf("success envelope should not include structured error: %#v", payload.Sidecar.Error)
	}
}

func TestRunMainDirectToolUnknownToolIncludesStructuredSidecarError(t *testing.T) {
	configPath := writeMainConfig(t)

	var stdout, stderr bytes.Buffer
	code := runMain([]string{"tool", "not_a_tool", "-config", configPath}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("expected exit 1, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var payload struct {
		Tool    string `json:"Tool"`
		Summary string `json:"Summary"`
		Error   string `json:"Error"`
		Sidecar struct {
			SchemaVersion string `json:"schema_version"`
			Error         struct {
				Class   string `json:"class"`
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		} `json:"sidecar"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("decode error output: %v\n%s", err, stdout.String())
	}
	if payload.Tool != "not_a_tool" || payload.Summary == "" || payload.Error == "" {
		t.Fatalf("legacy error fields changed: %#v", payload)
	}
	if payload.Sidecar.SchemaVersion != "idiom.tool.v1" {
		t.Fatalf("missing sidecar schema version: %#v", payload.Sidecar)
	}
	if payload.Sidecar.Error.Class != "unsupported_tool" || payload.Sidecar.Error.Code != "unsupported_tool" {
		t.Fatalf("unexpected structured error: %#v", payload.Sidecar.Error)
	}
	if !strings.Contains(payload.Sidecar.Error.Message, "unsupported tool") {
		t.Fatalf("structured error message should preserve bounded diagnostic: %#v", payload.Sidecar.Error)
	}
}

func TestRunMainDirectToolDiscoveryProfileAllowsReadOnlyTool(t *testing.T) {
	configPath := writeMainConfig(t)

	var stdout, stderr bytes.Buffer
	code := runMain([]string{"tool", "context_status", "-config", configPath, "--profile", "discovery", "--input", `{"limit":1}`}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var payload struct {
		Tool    string `json:"Tool"`
		Sidecar struct {
			Profile string `json:"profile"`
			Error   any    `json:"error"`
		} `json:"sidecar"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("decode tool output: %v\n%s", err, stdout.String())
	}
	if payload.Tool != "context_status" || payload.Sidecar.Profile != "discovery" || payload.Sidecar.Error != nil {
		t.Fatalf("unexpected discovery success envelope: %#v", payload)
	}
}

func TestRunMainDirectToolDiscoveryProfileAllowsRecoveryInspect(t *testing.T) {
	root := t.TempDir()
	if _, _, err := recovery.WritePlanExecuteFailureRecord(root, recovery.PlanExecuteFailureInput{
		SessionID:         "sid-cli",
		Phase:             "executor",
		Status:            "failed",
		FailureKind:       "executor_failed",
		FailureReason:     "exact edit did not apply",
		RequiredEditPaths: []string{"internal/app/foo.go"},
	}, time.Now().UTC()); err != nil {
		t.Fatalf("write recovery fixture: %v", err)
	}
	configPath := writeMainConfigRoot(t, root)

	var stdout, stderr bytes.Buffer
	code := runMain([]string{"tool", "recovery_inspect", "-config", configPath, "--profile", "discovery", "--input", `{"session_id":"sid-cli"}`}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var payload struct {
		Tool    string `json:"Tool"`
		Error   string `json:"Error"`
		Sidecar struct {
			Profile          string         `json:"profile"`
			Contract         map[string]any `json:"contract"`
			StructuredOutput map[string]any `json:"structured_output"`
			Error            any            `json:"error"`
		} `json:"sidecar"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("decode tool output: %v\n%s", err, stdout.String())
	}
	if payload.Tool != "recovery_inspect" || payload.Error != "" || payload.Sidecar.Profile != "discovery" || payload.Sidecar.Error != nil {
		t.Fatalf("unexpected recovery_inspect sidecar payload: %#v", payload)
	}
	if payload.Sidecar.Contract["id"] != "idiom.tool.v1:recovery_inspect" {
		t.Fatalf("unexpected recovery contract: %#v", payload.Sidecar.Contract)
	}
	if payload.Sidecar.StructuredOutput["failure_id"] != "plan-execute-sid-cli" ||
		payload.Sidecar.StructuredOutput["next_valid_action"] != recovery.NextActionReviewDecomposition {
		t.Fatalf("recovery structured output missing state: %#v", payload.Sidecar.StructuredOutput)
	}
}

func TestRunMainDirectToolDiscoveryProfileDeniesContextStatusRefreshBeforeExecution(t *testing.T) {
	root := t.TempDir()
	configPath := writeMainConfigRoot(t, root)

	var stdout, stderr bytes.Buffer
	code := runMain([]string{"tool", "context_status", "-config", configPath, "--profile", "discovery", "--input", `{"refresh":true}`}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("expected exit 1, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var payload struct {
		Tool    string `json:"Tool"`
		Error   string `json:"Error"`
		Sidecar struct {
			Profile string `json:"profile"`
			Error   struct {
				Class   string `json:"class"`
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		} `json:"sidecar"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("decode denial output: %v\n%s", err, stdout.String())
	}
	if payload.Tool != "context_status" || payload.Sidecar.Profile != "discovery" {
		t.Fatalf("unexpected denial identity: %#v", payload)
	}
	if payload.Sidecar.Error.Class != "profile_denied" || payload.Sidecar.Error.Code != "profile_denied" ||
		!strings.Contains(payload.Sidecar.Error.Message, "read_only_profile") {
		t.Fatalf("unexpected structured denial: %#v", payload.Sidecar.Error)
	}
	for _, path := range []string{
		filepath.Join(root, ".idiom", "comprehension"),
		filepath.Join(root, ".idiom", "comprehension", "graph.sqlite"),
	} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("denied refresh should not create %s; stat err=%v", path, err)
		}
	}
}

func TestRunMainDirectToolDiscoveryProfileDeniesWriteBeforeExecution(t *testing.T) {
	configPath := writeMainConfig(t)

	var stdout, stderr bytes.Buffer
	code := runMain([]string{"tool", "write_file", "-config", configPath, "--profile", "discovery", "--input", `{"path":"profile-denied.txt","content":"nope"}`}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("expected exit 1, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var payload struct {
		Tool    string `json:"Tool"`
		Error   string `json:"Error"`
		Sidecar struct {
			Profile string `json:"profile"`
			Error   struct {
				Class   string `json:"class"`
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		} `json:"sidecar"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("decode denial output: %v\n%s", err, stdout.String())
	}
	if payload.Tool != "write_file" || payload.Sidecar.Profile != "discovery" {
		t.Fatalf("unexpected denial identity: %#v", payload)
	}
	if payload.Sidecar.Error.Class != "profile_denied" || payload.Sidecar.Error.Code != "profile_denied" ||
		!strings.Contains(payload.Sidecar.Error.Message, "read_only_profile") {
		t.Fatalf("unexpected structured denial: %#v", payload.Sidecar.Error)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(configPath), "profile-denied.txt")); !os.IsNotExist(err) {
		t.Fatalf("write_file appears to have executed; stat err=%v", err)
	}
}

func TestRunMainDirectToolUnknownProfileIsUsageError(t *testing.T) {
	configPath := writeMainConfig(t)

	var stdout, stderr bytes.Buffer
	code := runMain([]string{"tool", "pwd", "-config", configPath, "--profile", "verify"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("expected exit 2, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "unknown tool profile") {
		t.Fatalf("expected unknown profile diagnostic, got %q", stderr.String())
	}
}

func TestRunMainWritesTelemetryForRejectedDirectToolInput(t *testing.T) {
	telemetryDir := t.TempDir()
	t.Setenv("IDIOM_TELEMETRY_DIR", telemetryDir)
	configPath := writeMainConfig(t)

	var stdout, stderr bytes.Buffer
	code := runMain([]string{"tool", "harness_feedback", "-config", configPath, "-input", "{"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("expected exit 1, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "invalid input JSON") {
		t.Fatalf("expected invalid JSON error, got %q", stderr.String())
	}

	entries, err := os.ReadDir(telemetryDir)
	if err != nil {
		t.Fatalf("read telemetry dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one telemetry file, got %d", len(entries))
	}
	records := readMainTelemetryRecords(t, filepath.Join(telemetryDir, entries[0].Name()))
	if !recordTypesContain(records, "process_start", "tool_request", "tool_result", "process_exit") {
		t.Fatalf("telemetry missing expected records: %#v", records)
	}
	var sawRejectedToolResult bool
	for _, record := range records {
		if record["type"] == "tool_result" && record["tool"] == "harness_feedback" && strings.Contains(stringFieldForTest(record, "error"), "invalid input JSON") {
			sawRejectedToolResult = true
		}
	}
	if !sawRejectedToolResult {
		t.Fatalf("missing rejected direct tool result telemetry: %#v", records)
	}
}

func TestRunMainDirectToolInputUsesSharedRequestDecoder(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get cwd: %v", err)
	}
	configPath := filepath.Join(t.TempDir(), "config.json")
	repoRoot := filepath.Clean(filepath.Join(cwd, "../../.."))
	configJSON := fmt.Sprintf(`{"workspace":{"root":%q}}`, repoRoot)
	if err := os.WriteFile(configPath, []byte(configJSON+"\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := runMain([]string{
		"tool", "go_read_symbol",
		"-config", configPath,
		"-input", `{"path":"internal/rootcmd/root.go","symbol":"runToolMainWithTelemetry"}`,
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "runToolMainWithTelemetry") {
		t.Fatalf("expected symbol read output, got %s", stdout.String())
	}
}

func TestRunMainToolHelpPrintsSchemaWithoutError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runMain([]string{"tool", "harness_feedback", "--help"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d stderr=%s", code, stderr.String())
	}
	for _, want := range []string{"Usage:", "Record structured feedback", `"confidence"`, "Example:"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("expected tool help to contain %q, got:\n%s", want, stdout.String())
		}
	}
	if strings.Contains(stderr.String(), "flag: help requested") {
		t.Fatalf("tool help should not surface flag parser help errors: %q", stderr.String())
	}
}

func TestRunMainToolSchemasExportsManifestDerivedSchemas(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runMain([]string{"tool", "schemas"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d stderr=%s", code, stderr.String())
	}
	var export struct {
		SchemaVersion string `json:"schema_version"`
		Source        string `json:"source"`
		Metadata      struct {
			ClientClassifications []string `json:"client_classifications"`
			ProductSignals        []string `json:"product_signals"`
		} `json:"metadata"`
		Tools []struct {
			Name        string          `json:"name"`
			InputSchema json.RawMessage `json:"input_schema"`
			Profiles    []string        `json:"profiles"`
			Policy      struct {
				SideEffects       []string `json:"side_effects"`
				DecisionAuthority string   `json:"decision_authority"`
				ErrorClasses      []string `json:"error_classes"`
			} `json:"policy"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &export); err != nil {
		t.Fatalf("decode schema export: %v\n%s", err, stdout.String())
	}
	if export.SchemaVersion == "" || !strings.Contains(export.Source, "BuildToolManifest") {
		t.Fatalf("schema export should name manifest/schema source, got %#v", export)
	}
	if len(export.Metadata.ClientClassifications) != 7 || len(export.Metadata.ProductSignals) != 9 {
		t.Fatalf("schema export should carry generic client/product metadata, got %#v", export.Metadata)
	}
	var sawReadFile bool
	var sawContextQuery bool
	for _, tool := range export.Tools {
		if tool.Name != "read_file" {
			if tool.Name == "context_query" {
				sawContextQuery = true
				if !slicesContainsString(tool.Policy.ErrorClasses, "stale_context") {
					t.Fatalf("context_query export should expose stale_context error class: %#v", tool.Policy.ErrorClasses)
				}
				if !strings.Contains(string(tool.InputSchema), `"freshness_policy"`) || !strings.Contains(string(tool.InputSchema), `"freshness_max_files"`) {
					t.Fatalf("context_query schema should expose freshness controls, got %s", string(tool.InputSchema))
				}
			}
			continue
		}
		sawReadFile = true
		if !json.Valid(tool.InputSchema) || len(tool.Profiles) == 0 ||
			len(tool.Policy.SideEffects) == 0 || tool.Policy.DecisionAuthority != "runtime_contract" ||
			!slicesContainsString(tool.Policy.ErrorClasses, "invalid_input") {
			t.Fatalf("read_file export missing required metadata: %#v", tool)
		}
	}
	if !sawReadFile {
		t.Fatalf("schema export missing read_file: %#v", export.Tools)
	}
	if !sawContextQuery {
		t.Fatalf("schema export missing context_query: %#v", export.Tools)
	}
	if strings.Contains(stdout.String(), "Inspect one focused architecture/review question") {
		t.Fatalf("schema export should not expose raw manifest prompt examples")
	}
}

func TestToolHelpRequestedIgnoresFlagValues(t *testing.T) {
	if !toolHelpRequested([]string{"--config", "custom.json", "--help"}) {
		t.Fatal("expected help after config flag to be recognized")
	}
	if toolHelpRequested([]string{"--input", "help"}) {
		t.Fatal("input value should not be treated as a help request")
	}
}

func slicesContainsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func writeMainConfig(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("IDIOM_QUEUE_SOCKET", filepath.Join(root, "missing-runner.sock"))
	path := filepath.Join(root, "config.json")
	if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func writeMainConfigRoot(t *testing.T, root string) string {
	t.Helper()
	t.Setenv("IDIOM_QUEUE_SOCKET", filepath.Join(root, "missing-runner.sock"))
	path := filepath.Join(t.TempDir(), "config.json")
	configJSON := fmt.Sprintf(`{"workspace":{"root":%q,"cwd":%q}}`, root, root)
	if err := os.WriteFile(path, []byte(configJSON+"\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func stringFieldForTest(record map[string]any, key string) string {
	value, _ := record[key].(string)
	return value
}

func readMainTelemetryRecords(t *testing.T, path string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read telemetry log: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	records := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("parse telemetry line %q: %v", line, err)
		}
		records = append(records, record)
	}
	return records
}

func recordTypesContain(records []map[string]any, want ...string) bool {
	seen := make(map[string]bool, len(records))
	for _, record := range records {
		if typ, ok := record["type"].(string); ok {
			seen[typ] = true
		}
	}
	for _, typ := range want {
		if !seen[typ] {
			return false
		}
	}
	return true
}
