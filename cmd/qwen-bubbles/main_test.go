package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"qwen-bubbles/internal/config"
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
		func(config.Config) error {
			t.Fatal("TUI runner should not be called in prompt mode")
			return nil
		},
		func(config.Config) error {
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
		func(config.Config) error { return nil },
		func(config.Config) error { return nil },
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
		func(config.Config) error { return nil },
		func(config.Config) error { return nil },
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
		func(config.Config) error { return nil },
		func(config.Config) error { return nil },
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
		func(config.Config) error { return nil },
		func(config.Config) error { return nil },
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
		func(config.Config) error {
			t.Fatal("TUI runner should not be called")
			return nil
		},
		func(config.Config) error {
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
		func(config.Config) error {
			t.Fatal("TUI runner should not be called in server-only mode")
			return nil
		},
		func(config.Config) error {
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
		func(config.Config) error {
			called = true
			return nil
		},
		func(config.Config) error {
			t.Fatal("server runner should not be called in default mode")
			return nil
		},
		nopList,
	)
	if code != 0 || !called {
		t.Fatalf("expected TUI runner success, code=%d called=%t stderr=%s", code, called, stderr.String())
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
		func(config.Config) error { return nil },
		func(config.Config) error { return nil },
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
		func(config.Config) error { return nil },
		func(config.Config) error { return nil },
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
		func(config.Config) error { return nil },
		func(config.Config) error { return nil },
		nopList,
	)
	if code != 2 || !strings.Contains(stderr.String(), "flag provided but not defined") {
		t.Fatalf("expected flag parse failure, code=%d stderr=%s", code, stderr.String())
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

func writeMainConfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}
