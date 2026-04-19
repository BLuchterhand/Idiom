package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"qwen-bubbles/internal/config"
)

func TestRunPromptModeUsesInjectedRunner(t *testing.T) {
	configPath := writeMainConfig(t)
	var stdout, stderr bytes.Buffer
	var gotPrompt string
	code := run(
		[]string{"-config", configPath, "-prompt", "explain routing"},
		&stdout,
		&stderr,
		func(_ config.Config, prompt string) (string, error) {
			gotPrompt = prompt
			return "answer text", nil
		},
		func(config.Config) error {
			t.Fatal("TUI runner should not be called in prompt mode")
			return nil
		},
		func(config.Config) error {
			t.Fatal("server runner should not be called in prompt mode")
			return nil
		},
	)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d stderr=%s", code, stderr.String())
	}
	if gotPrompt != "explain routing" || strings.TrimSpace(stdout.String()) != "answer text" {
		t.Fatalf("unexpected prompt output prompt=%q stdout=%q", gotPrompt, stdout.String())
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
		func(config.Config, string) (string, error) {
			t.Fatal("prompt runner should not be called in server-only mode")
			return "", nil
		},
		func(config.Config) error {
			t.Fatal("TUI runner should not be called in server-only mode")
			return nil
		},
		func(config.Config) error {
			called = true
			return nil
		},
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
		func(config.Config, string) (string, error) {
			t.Fatal("prompt runner should not be called in default mode")
			return "", nil
		},
		func(config.Config) error {
			called = true
			return nil
		},
		func(config.Config) error {
			t.Fatal("server runner should not be called in default mode")
			return nil
		},
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
		func(config.Config, string) (string, error) { return "", nil },
		func(config.Config) error { return nil },
		func(config.Config) error { return nil },
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
		func(config.Config, string) (string, error) { return "", errors.New("prompt failed") },
		func(config.Config) error { return nil },
		func(config.Config) error { return nil },
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
		func(config.Config, string) (string, error) { return "", nil },
		func(config.Config) error { return nil },
		func(config.Config) error { return nil },
	)
	if code != 2 || !strings.Contains(stderr.String(), "flag provided but not defined") {
		t.Fatalf("expected flag parse failure, code=%d stderr=%s", code, stderr.String())
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
