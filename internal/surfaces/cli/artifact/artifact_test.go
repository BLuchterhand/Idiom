package artifact

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"idiom/internal/platform/config"
	"idiom/internal/runtime/workerruntime"
)

func TestArtifactCommandWritesOutputAndJSON(t *testing.T) {
	configPath := writeMainConfig(t)
	outputPath := filepath.Join(t.TempDir(), "artifact.md")
	jsonPath := filepath.Join(t.TempDir(), "events.jsonl")
	reportPath := filepath.Join(t.TempDir(), "artifact.report.json")

	var (
		stdout, stderr bytes.Buffer
		gotOpts        RunOptions
	)

	code := RunMainWithTelemetry(
		[]string{
			"-config", configPath,
			"-prompt", "draft a spec",
			"-output", outputPath,
			"-json-out", jsonPath,
			"-report-out", reportPath,
		},
		&stdout,
		&stderr,
		func(_ config.Config, prompt string, opts RunOptions, w io.Writer) (string, error) {
			if prompt != "draft a spec" {
				t.Fatalf("prompt = %q", prompt)
			}

			gotOpts = opts
			_, _ = w.Write([]byte("{\"type\":\"final\",\"content\":\"# Artifact\"}\n"))

			return "# Artifact", nil
		},
		nil,
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

	report := readArtifactReport(t, reportPath)
	if !report.DispatchReady {
		t.Fatalf("artifact report should be dispatch-ready: %#v", report)
	}

	if report.RepairSource != "raw_model" || report.AnswerHash == "" || report.PromptHash == "" {
		t.Fatalf("artifact report missing provenance fields: %#v", report)
	}

	if report.OutputPath != outputPath || report.JSONOut != jsonPath {
		t.Fatalf("artifact report paths = output %q json %q", report.OutputPath, report.JSONOut)
	}
}

func TestArtifactCommandForwardsMaxTokenOverride(t *testing.T) {
	configPath := writeMainConfig(t)

	var (
		stdout, stderr bytes.Buffer
		gotOpts        RunOptions
	)

	code := RunMainWithTelemetry(
		[]string{"-config", configPath, "-prompt", "draft", "-max-token", "4096"},
		&stdout,
		&stderr,
		func(_ config.Config, _ string, opts RunOptions, _ io.Writer) (string, error) {
			gotOpts = opts
			return "artifact", nil
		},
		nil,
	)

	if code != 0 {
		t.Fatalf("expected exit 0, got %d stderr=%s", code, stderr.String())
	}

	if !gotOpts.JSONEvents {
		t.Fatal("artifact command must request JSON events from the prompt runner")
	}

	if gotOpts.MaxTokensOverride != 4096 {
		t.Fatalf("MaxTokensOverride = %d, want 4096", gotOpts.MaxTokensOverride)
	}
}

func TestArtifactCommandRejectsNegativeMaxToken(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := RunMainWithTelemetry(
		[]string{"-prompt", "draft", "-max-token", "-1"},
		&stdout,
		&stderr,
		func(config.Config, string, RunOptions, io.Writer) (string, error) {
			t.Fatal("prompt runner should not be called")

			return "", nil
		},
		nil,
	)

	if code != 2 || !strings.Contains(stderr.String(), "non-negative") {
		t.Fatalf("expected max-token validation failure, code=%d stderr=%s", code, stderr.String())
	}
}

func TestArtifactCommandNoToolsWrapsPromptAndFailsToolUse(t *testing.T) {
	configPath := writeMainConfig(t)
	reportPath := filepath.Join(t.TempDir(), "artifact.report.json")

	var (
		stdout, stderr bytes.Buffer
		gotPrompt      string
		gotOpts        RunOptions
	)

	code := RunMainWithTelemetry(
		[]string{"-config", configPath, "-prompt", "draft", "-no-tools", "-report-out", reportPath},
		&stdout,
		&stderr,
		func(_ config.Config, prompt string, opts RunOptions, w io.Writer) (string, error) {
			gotPrompt = prompt
			gotOpts = opts
			_, _ = w.Write([]byte("{\"type\":\"tool_request\",\"tool\":\"read_file\"}\n"))

			return "artifact", nil
		},
		nil,
	)

	if code != 1 {
		t.Fatalf("expected tool-use failure, got %d stderr=%s", code, stderr.String())
	}

	if !strings.Contains(gotPrompt, "Do not call tools") {
		t.Fatalf("-no-tools did not wrap prompt: %q", gotPrompt)
	}

	if gotOpts.TaskFamily != workerruntime.ArtifactFinalAnswerTaskFamily {
		t.Fatalf("-no-tools TaskFamily = %q, want %s", gotOpts.TaskFamily, workerruntime.ArtifactFinalAnswerTaskFamily)
	}

	if !strings.Contains(stderr.String(), "read_file") {
		t.Fatalf("stderr should name offending tool, got %q", stderr.String())
	}

	report := readArtifactReport(t, reportPath)
	if report.DispatchReady || !report.ToolUseDetected || report.ToolName != "read_file" {
		t.Fatalf("tool-use failure report = %#v", report)
	}

	if report.ArtifactContract != "artifact.no_tools.v1" {
		t.Fatalf("artifact contract = %q", report.ArtifactContract)
	}
}

func TestArtifactCommandMechanicalRepairUnwrapsOuterFence(t *testing.T) {
	configPath := writeMainConfig(t)
	outputPath := filepath.Join(t.TempDir(), "artifact.md")
	reportPath := filepath.Join(t.TempDir(), "artifact.report.json")

	var stdout, stderr bytes.Buffer

	code := RunMainWithTelemetry(
		[]string{
			"-config", configPath,
			"-prompt", "draft",
			"-output", outputPath,
			"-report-out", reportPath,
			"-mechanical-repair",
		},
		&stdout,
		&stderr,
		func(config.Config, string, RunOptions, io.Writer) (string, error) {
			return "```markdown\n# Artifact\n```", nil
		},
		nil,
	)

	if code != 0 {
		t.Fatalf("expected mechanical repair success, got %d stderr=%s", code, stderr.String())
	}

	if got := readTestFile(t, outputPath); got != "# Artifact" {
		t.Fatalf("artifact output = %q", got)
	}

	report := readArtifactReport(t, reportPath)
	if report.RepairSource != "mechanical" || !report.MechanicalRepairApplied {
		t.Fatalf("expected mechanical repair provenance, got %#v", report)
	}
}

func TestArtifactCommandRunsValidatorWithArtifactEnv(t *testing.T) {
	configPath := writeMainConfig(t)
	outputPath := filepath.Join(t.TempDir(), "artifact.md")

	var stdout, stderr bytes.Buffer

	code := RunMainWithTelemetry(
		[]string{
			"-config", configPath,
			"-prompt", "draft",
			"-output", outputPath,
			"-validator", "grep -q '^validated$' \"$IDIOM_ARTIFACT\"",
		},
		&stdout,
		&stderr,
		func(config.Config, string, RunOptions, io.Writer) (string, error) {
			return "validated", nil
		},
		nil,
	)

	if code != 0 {
		t.Fatalf("expected validator success, got %d stderr=%s", code, stderr.String())
	}

	if got := readTestFile(t, outputPath); got != "validated" {
		t.Fatalf("artifact output = %q", got)
	}
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

func readArtifactReport(t *testing.T, path string) artifactReport {
	t.Helper()

	var report artifactReport

	if err := json.Unmarshal([]byte(readTestFile(t, path)), &report); err != nil {
		t.Fatalf("parse artifact report: %v", err)
	}

	return report
}

func TestArtifactCommandRejectsPromptSourcesConflict(t *testing.T) {
	configPath := writeMainConfig(t)
	promptPath := filepath.Join(t.TempDir(), "prompt.txt")

	if err := os.WriteFile(promptPath, []byte("prompt\n"), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}

	var stdout, stderr bytes.Buffer

	code := RunMainWithTelemetry(
		[]string{"-config", configPath, "-prompt", "x", "-prompt-file", promptPath},
		&stdout,
		&stderr,
		func(config.Config, string, RunOptions, io.Writer) (string, error) {
			t.Fatal("prompt runner should not be called")

			return "", nil
		},
		nil,
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
