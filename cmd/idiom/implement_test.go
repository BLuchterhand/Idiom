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

func TestRunImplementMainForcesImplementationTaskFamily(t *testing.T) {
	configPath := writeMainConfig(t)
	var stdout, stderr bytes.Buffer
	var gotPrompt string
	var gotOpts runOptions

	code := runImplementMain(
		[]string{"-config", configPath, "-prompt", "modify exactly one file; do not run tests"},
		&stdout,
		&stderr,
		func(_ config.Config, prompt string, opts runOptions, _ io.Writer) (string, error) {
			gotPrompt = prompt
			gotOpts = opts
			return "done", nil
		},
	)

	if code != 0 {
		t.Fatalf("expected exit 0, got %d stderr=%s", code, stderr.String())
	}
	if gotPrompt != "modify exactly one file; do not run tests" {
		t.Fatalf("prompt not forwarded: %q", gotPrompt)
	}
	if gotOpts.TaskFamily != "implementation" {
		t.Fatalf("TaskFamily = %q, want implementation", gotOpts.TaskFamily)
	}
	if gotOpts.PlannerOwnedVerification {
		t.Fatal("idiom implement should keep build/test verification available by default")
	}
	if strings.TrimSpace(stdout.String()) != "done" {
		t.Fatalf("stdout = %q, want done", stdout.String())
	}
}

func TestRunImplementMainCanDelegateBuildTestsToPlanner(t *testing.T) {
	configPath := writeMainConfig(t)
	var stdout, stderr bytes.Buffer
	var gotOpts runOptions

	code := runImplementMain(
		[]string{"-config", configPath, "-prompt", "modify and verify", "-planner-owned-verification"},
		&stdout,
		&stderr,
		func(_ config.Config, _ string, opts runOptions, _ io.Writer) (string, error) {
			gotOpts = opts
			return "done", nil
		},
	)

	if code != 0 {
		t.Fatalf("expected exit 0, got %d stderr=%s", code, stderr.String())
	}
	if !gotOpts.PlannerOwnedVerification {
		t.Fatal("--planner-owned-verification should mark build/test verification as caller-owned")
	}
}

func TestRunImplementMainCanDisableThinking(t *testing.T) {
	configPath := writeMainConfig(t)
	var stdout, stderr bytes.Buffer
	var gotOpts runOptions

	code := runImplementMain(
		[]string{"-config", configPath, "-prompt", "write the brief", "-no-thinking"},
		&stdout,
		&stderr,
		func(_ config.Config, _ string, opts runOptions, _ io.Writer) (string, error) {
			gotOpts = opts
			return "done", nil
		},
	)

	if code != 0 {
		t.Fatalf("expected exit 0, got %d stderr=%s", code, stderr.String())
	}
	if !gotOpts.DisableThinking {
		t.Fatal("--no-thinking should force request-level thinking off")
	}
}

func TestRunImplementMainAllowBuildTestsOverridesPlannerOwnedVerification(t *testing.T) {
	configPath := writeMainConfig(t)
	var stdout, stderr bytes.Buffer
	var gotOpts runOptions

	code := runImplementMain(
		[]string{"-config", configPath, "-prompt", "modify and verify", "-planner-owned-verification", "-allow-build-tests"},
		&stdout,
		&stderr,
		func(_ config.Config, _ string, opts runOptions, _ io.Writer) (string, error) {
			gotOpts = opts
			return "done", nil
		},
	)

	if code != 0 {
		t.Fatalf("expected exit 0, got %d stderr=%s", code, stderr.String())
	}
	if gotOpts.PlannerOwnedVerification {
		t.Fatal("--allow-build-tests should leave build/test verification available to the implementation lane")
	}
}

func TestRunImplementMainReadsPromptFileAndHonorsJSON(t *testing.T) {
	configPath := writeMainConfig(t)
	promptPath := filepath.Join(t.TempDir(), "brief.md")
	if err := os.WriteFile(promptPath, []byte("implement this\n"), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}

	var stdout, stderr bytes.Buffer
	var gotOpts runOptions
	code := runImplementMain(
		[]string{"-config", configPath, "-prompt-file", promptPath, "-json", "-resume", "prior"},
		&stdout,
		&stderr,
		func(_ config.Config, prompt string, opts runOptions, w io.Writer) (string, error) {
			gotOpts = opts
			if prompt != "implement this" {
				t.Fatalf("prompt = %q, want implement this", prompt)
			}
			_, _ = w.Write([]byte("{\"type\":\"final\",\"content\":\"ok\"}\n"))
			return "ok", nil
		},
	)

	if code != 0 {
		t.Fatalf("expected exit 0, got %d stderr=%s", code, stderr.String())
	}
	if !gotOpts.JSONEvents || gotOpts.Resume != "prior" || gotOpts.TaskFamily != "implementation" || gotOpts.PlannerOwnedVerification {
		t.Fatalf("unexpected opts: %#v", gotOpts)
	}
	if !strings.Contains(stdout.String(), `"type":"final"`) || strings.Contains(stdout.String(), "ok\n") && !strings.Contains(stdout.String(), `"content":"ok"`) {
		t.Fatalf("JSON mode should only emit runner JSONL, got %q", stdout.String())
	}
}

func TestRunImplementMainRejectsMissingPrompt(t *testing.T) {
	configPath := writeMainConfig(t)
	var stdout, stderr bytes.Buffer
	code := runImplementMain(
		[]string{"-config", configPath},
		&stdout,
		&stderr,
		func(config.Config, string, runOptions, io.Writer) (string, error) {
			t.Fatal("prompt runner should not be called")
			return "", nil
		},
	)
	if code != 2 || !strings.Contains(stderr.String(), "provide --prompt") {
		t.Fatalf("expected prompt validation failure, code=%d stderr=%s", code, stderr.String())
	}
}
