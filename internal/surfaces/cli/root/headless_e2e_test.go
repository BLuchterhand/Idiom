package root

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"idiom/internal/platform/config"
	"idiom/internal/surfaces/cli/runrecord"
	cmdruntime "idiom/internal/surfaces/cli/runtime"
)

func TestRunMainHeadlessInvalidSessionRejectsBeforeModel(t *testing.T) {
	root := t.TempDir()
	configPath := writeMainConfigRoot(t, root)

	var stdout, stderr bytes.Buffer
	code := RunMain([]string{
		"run",
		"--config", configPath,
		"--prompt", "should not reach model",
		"--session", "../escape",
	}, &stdout, &stderr, Options{})

	if code != 2 {
		t.Fatalf("exit = %d, want 2 stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "invalid run id") {
		t.Fatalf("stderr should explain invalid run id, got %q", stderr.String())
	}
	if matches, _ := filepath.Glob(filepath.Join(root, ".idiom", "runs", "*.completion.json")); len(matches) != 0 {
		t.Fatalf("invalid session should not write completion artifacts: %#v", matches)
	}
}

func TestHeadlessRunAdapterE2EWritesCompletionWithoutRawDurableAnswer(t *testing.T) {
	root := t.TempDir()
	configPath := writeMainConfigRoot(t, root)

	var stdout, stderr bytes.Buffer
	code := runHeadlessMainWithTelemetry([]string{
		"--config", configPath,
		"--prompt", "secret prompt text",
		"--output", "json",
		"--session", "e2e-run",
	}, &stdout, &stderr, func(cfg config.Config, prompt string, opts cmdruntime.RunOptions, _ io.Writer) (string, error) {
		if cfg.Workspace.SessionID != "e2e-run" {
			t.Fatalf("workspace session id = %q, want e2e-run", cfg.Workspace.SessionID)
		}
		if prompt != "secret prompt text" || opts.JSONEvents {
			t.Fatalf("unexpected prompt/options prompt=%q opts=%#v", prompt, opts)
		}

		return "secret answer text", nil
	}, nil)

	if code != 0 {
		t.Fatalf("exit = %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}

	var output runrecord.RunOutputRecord
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		t.Fatalf("decode stdout JSON: %v\n%s", err, stdout.String())
	}
	if output.Answer != "secret answer text" || output.RunID != "e2e-run" || output.TerminalClass != "completed" {
		t.Fatalf("unexpected stdout completion: %#v", output)
	}

	completionPath := filepath.Join(root, filepath.FromSlash(output.Artifacts["completion"]))
	body, err := os.ReadFile(completionPath)
	if err != nil {
		t.Fatalf("read completion artifact: %v", err)
	}
	if bytes.Contains(body, []byte("secret prompt text")) || bytes.Contains(body, []byte("secret answer text")) {
		t.Fatalf("durable completion leaked raw prompt/answer: %s", string(body))
	}

	var saved runrecord.RunCompletionRecord
	if err := json.Unmarshal(body, &saved); err != nil {
		t.Fatalf("decode saved completion: %v", err)
	}
	if saved.SchemaVersion != runrecord.RunCompletionSchemaVersion ||
		saved.Type != runrecord.RunCompletionRecordType ||
		saved.Implementation != "idiom run" ||
		saved.PromptSHA256 == "" ||
		saved.AnswerSHA256 == "" {

		t.Fatalf("unexpected saved completion record: %#v", saved)
	}
}

func TestLegacyPromptAdapterE2EWritesCompletionForJSONMode(t *testing.T) {
	root := t.TempDir()
	configPath := writeMainConfigRoot(t, root)

	var stdout, stderr bytes.Buffer
	code := cmdruntime.RunWithTelemetry(
		[]string{"-config", configPath, "-prompt", "legacy secret prompt", "-json"},
		&stdout,
		&stderr,
		func(cfg config.Config, prompt string, opts cmdruntime.RunOptions, w io.Writer) (string, error) {
			if cfg.Workspace.SessionID == "" {
				t.Fatal("legacy path should stamp workspace session id before prompt runner")
			}
			if prompt != "legacy secret prompt" || !opts.JSONEvents {
				t.Fatalf("unexpected legacy prompt/options prompt=%q opts=%#v", prompt, opts)
			}
			_, _ = w.Write([]byte(`{"type":"final","content":"legacy answer"}` + "\n"))

			return "legacy secret answer", nil
		},
		nopPrompts,
		func(config.Config, cmdruntime.RunOptions) error {
			t.Fatal("TUI runner should not run in legacy prompt e2e")
			return nil
		},
		func(config.Config, cmdruntime.RunOptions) error {
			t.Fatal("server runner should not run in legacy prompt e2e")
			return nil
		},
		nopList,
		runVersionMain,
		nil,
	)

	if code != 0 {
		t.Fatalf("exit = %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), `"type":"final"`) || strings.Contains(stdout.String(), "legacy secret answer") {
		t.Fatalf("legacy JSON mode should only emit JSONL stream, got %q", stdout.String())
	}

	record := readSingleLegacyRunCompletion(t, root)
	if record.SchemaVersion != runrecord.RunCompletionSchemaVersion ||
		record.Type != runrecord.RunCompletionRecordType ||
		record.Implementation != "idiom --prompt" ||
		record.OutputMode != "jsonl" ||
		record.TerminalClass != "completed" ||
		record.PromptSHA256 == "" ||
		record.AnswerSHA256 == "" {

		t.Fatalf("unexpected legacy e2e completion: %#v", record)
	}
	if got := record.Metadata["active_mode"]; len(got) != 1 || got[0] != "prompt" {
		t.Fatalf("legacy completion should record active prompt mode: %#v", record.Metadata)
	}
	if got := record.Metadata["timeout_owner"]; len(got) != 1 || got[0] != "chat_request_timeout" {
		t.Fatalf("legacy completion should record timeout owner: %#v", record.Metadata)
	}
}
