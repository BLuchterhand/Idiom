package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"idiom/internal/config"
)

func TestArtifactCommandWritesOutputAndJSON(t *testing.T) {
	configPath := writeMainConfig(t)
	outputPath := filepath.Join(t.TempDir(), "artifact.md")
	jsonPath := filepath.Join(t.TempDir(), "events.jsonl")
	var stdout, stderr bytes.Buffer
	var gotOpts runOptions

	code := runArtifactMain(
		[]string{
			"-config", configPath,
			"-prompt", "draft a spec",
			"-output", outputPath,
			"-json-out", jsonPath,
		},
		&stdout,
		&stderr,
		func(_ config.Config, prompt string, opts runOptions, w io.Writer) (string, error) {
			if prompt != "draft a spec" {
				t.Fatalf("prompt = %q", prompt)
			}
			gotOpts = opts
			_, _ = w.Write([]byte("{\"type\":\"final\",\"content\":\"# Artifact\"}\n"))
			return "# Artifact", nil
		},
	)

	if code != 0 {
		t.Fatalf("expected exit 0, got %d stderr=%s", code, stderr.String())
	}
	if !gotOpts.JSONEvents {
		t.Fatal("artifact command must request JSON events from the prompt runner")
	}
	if stdout.String() != "" {
		t.Fatalf("output file mode should not print artifact to stdout, got %q", stdout.String())
	}
	if got := readTestFile(t, outputPath); got != "# Artifact" {
		t.Fatalf("artifact output = %q", got)
	}
	if got := readTestFile(t, jsonPath); !strings.Contains(got, `"type":"final"`) {
		t.Fatalf("json-out missing final event: %q", got)
	}
}

func TestArtifactCommandNoToolsWrapsPromptAndFailsToolUse(t *testing.T) {
	configPath := writeMainConfig(t)
	var stdout, stderr bytes.Buffer
	var gotPrompt string

	code := runArtifactMain(
		[]string{"-config", configPath, "-prompt", "draft", "-no-tools"},
		&stdout,
		&stderr,
		func(_ config.Config, prompt string, _ runOptions, w io.Writer) (string, error) {
			gotPrompt = prompt
			_, _ = w.Write([]byte("{\"type\":\"tool_request\",\"tool\":\"read_file\"}\n"))
			return "artifact", nil
		},
	)

	if code != 1 {
		t.Fatalf("expected tool-use failure, got %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(gotPrompt, "Do not call tools") {
		t.Fatalf("-no-tools did not wrap prompt: %q", gotPrompt)
	}
	if !strings.Contains(stderr.String(), "read_file") {
		t.Fatalf("stderr should name offending tool, got %q", stderr.String())
	}
}

func TestArtifactCommandRunsValidatorWithArtifactEnv(t *testing.T) {
	configPath := writeMainConfig(t)
	outputPath := filepath.Join(t.TempDir(), "artifact.md")
	var stdout, stderr bytes.Buffer

	code := runArtifactMain(
		[]string{
			"-config", configPath,
			"-prompt", "draft",
			"-output", outputPath,
			"-validator", "grep -q '^validated$' \"$IDIOM_ARTIFACT\"",
		},
		&stdout,
		&stderr,
		func(config.Config, string, runOptions, io.Writer) (string, error) {
			return "validated", nil
		},
	)

	if code != 0 {
		t.Fatalf("expected validator success, got %d stderr=%s", code, stderr.String())
	}
	if got := readTestFile(t, outputPath); got != "validated" {
		t.Fatalf("artifact output = %q", got)
	}
}

func TestArtifactCommandRejectsPromptSourcesConflict(t *testing.T) {
	configPath := writeMainConfig(t)
	promptPath := filepath.Join(t.TempDir(), "prompt.txt")
	if err := os.WriteFile(promptPath, []byte("prompt\n"), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}
	var stdout, stderr bytes.Buffer

	code := runArtifactMain(
		[]string{"-config", configPath, "-prompt", "x", "-prompt-file", promptPath},
		&stdout,
		&stderr,
		func(config.Config, string, runOptions, io.Writer) (string, error) {
			t.Fatal("prompt runner should not be called")
			return "", nil
		},
	)

	if code != 2 || !strings.Contains(stderr.String(), "mutually exclusive") {
		t.Fatalf("expected prompt-source conflict, code=%d stderr=%s", code, stderr.String())
	}
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
