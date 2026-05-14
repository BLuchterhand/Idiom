package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type sidecarSmokeTranscript struct {
	SchemaVersion string             `json:"schema_version"`
	Steps         []sidecarSmokeStep `json:"steps"`
}

type sidecarSmokeStep struct {
	ID                 string            `json:"id"`
	Command            string            `json:"command"`
	DocumentedRecipe   bool              `json:"documented_recipe"`
	Argv               []string          `json:"argv"`
	ExitCode           int               `json:"exit_code"`
	JSON               bool              `json:"json"`
	ExpectJSONString   map[string]string `json:"expect_json_string"`
	ExpectJSONNull     []string          `json:"expect_json_null"`
	ExpectJSONNonEmpty []string          `json:"expect_json_non_empty"`
	StdoutIncludes     []string          `json:"stdout_includes"`
	StdoutExcludes     []string          `json:"stdout_excludes"`
}

func TestSidecarSmokeTranscript(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get cwd: %v", err)
	}
	repoRoot := filepath.Clean(filepath.Join(cwd, "../.."))
	transcriptPath := filepath.Join(repoRoot, "testdata", "sidecar", "discovery-smoke.transcript.json")
	data, err := os.ReadFile(transcriptPath)
	if err != nil {
		t.Fatalf("read smoke transcript: %v", err)
	}
	var transcript sidecarSmokeTranscript
	if err := json.Unmarshal(data, &transcript); err != nil {
		t.Fatalf("decode smoke transcript: %v", err)
	}
	if transcript.SchemaVersion != "idiom.sidecar_smoke_transcript.v1" || len(transcript.Steps) == 0 {
		t.Fatalf("unexpected smoke transcript header: %#v", transcript)
	}

	root := t.TempDir()
	writeSidecarRecipeWorkspace(t, root)
	configPath := writeMainConfigRoot(t, root)
	for _, step := range transcript.Steps {
		t.Run(step.ID, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := runMain(sidecarSmokeArgsWithConfig(step.Argv, configPath), &stdout, &stderr)
			if code != step.ExitCode {
				t.Fatalf("exit code = %d, want %d\nstdout=%s\nstderr=%s", code, step.ExitCode, stdout.String(), stderr.String())
			}
			for _, want := range step.StdoutIncludes {
				if !strings.Contains(stdout.String(), want) {
					t.Fatalf("stdout missing %q:\n%s", want, stdout.String())
				}
			}
			for _, forbidden := range step.StdoutExcludes {
				if strings.Contains(stdout.String(), forbidden) {
					t.Fatalf("stdout unexpectedly contains %q:\n%s", forbidden, stdout.String())
				}
			}
			if !step.JSON {
				return
			}
			var payload any
			if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
				t.Fatalf("decode JSON stdout: %v\n%s", err, stdout.String())
			}
			for path, want := range step.ExpectJSONString {
				got, ok := jsonValueAtDottedPath(payload, path)
				if !ok {
					t.Fatalf("missing JSON path %s in %s", path, stdout.String())
				}
				if fmt.Sprint(got) != want {
					t.Fatalf("JSON path %s = %q, want %q", path, fmt.Sprint(got), want)
				}
			}
			for _, path := range step.ExpectJSONNull {
				got, ok := jsonValueAtDottedPath(payload, path)
				if !ok {
					t.Fatalf("missing JSON path %s in %s", path, stdout.String())
				}
				if got != nil {
					t.Fatalf("JSON path %s = %#v, want null", path, got)
				}
			}
			for _, path := range step.ExpectJSONNonEmpty {
				got, ok := jsonValueAtDottedPath(payload, path)
				if !ok {
					t.Fatalf("missing JSON path %s in %s", path, stdout.String())
				}
				if jsonValueEmpty(got) {
					t.Fatalf("JSON path %s is empty: %#v", path, got)
				}
			}
		})
	}
}

func TestSidecarRecipesCoverCallerClassesAndCompatibility(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get cwd: %v", err)
	}
	repoRoot := filepath.Clean(filepath.Join(cwd, "../.."))
	data, err := os.ReadFile(filepath.Join(repoRoot, "docs", "sidecar-recipes.md"))
	if err != nil {
		t.Fatalf("read sidecar recipes: %v", err)
	}
	doc := string(data)
	transcript, err := readSidecarSmokeTranscript(repoRoot)
	if err != nil {
		t.Fatalf("read smoke transcript: %v", err)
	}
	for _, required := range []string{
		"Shell Or CI Script",
		"External Coding-Agent Harness",
		"Local Or Private-Code User",
		"sidecar.schema_version",
		"sidecar.profile",
		"sidecar.error",
		"sidecar.freshness",
		"sidecar.contract",
		"`Tool`, `Summary`, `Output`, and `Error`",
		"testdata/sidecar/discovery-smoke.transcript.json",
		"The caller owns strategy",
		"Idiom owns the contract",
	} {
		if !strings.Contains(doc, required) {
			t.Fatalf("sidecar recipes missing %q", required)
		}
	}
	documentedSteps := map[string]bool{}
	normalizedDoc := normalizedShellSnippet(doc)
	for _, step := range transcript.Steps {
		if !step.DocumentedRecipe {
			continue
		}
		documentedSteps[step.ID] = true
		if !strings.Contains(normalizedDoc, normalizedShellSnippet(step.Command)) {
			t.Fatalf("sidecar recipes do not document executable step %q command %q", step.ID, step.Command)
		}
	}
	for _, requiredStep := range []string{
		"external-harness-context-query",
		"local-private-context-pack",
	} {
		if !documentedSteps[requiredStep] {
			t.Fatalf("smoke transcript missing documented executable recipe step %q", requiredStep)
		}
	}
}

func readSidecarSmokeTranscript(repoRoot string) (sidecarSmokeTranscript, error) {
	transcriptPath := filepath.Join(repoRoot, "testdata", "sidecar", "discovery-smoke.transcript.json")
	data, err := os.ReadFile(transcriptPath)
	if err != nil {
		return sidecarSmokeTranscript{}, err
	}
	var transcript sidecarSmokeTranscript
	if err := json.Unmarshal(data, &transcript); err != nil {
		return sidecarSmokeTranscript{}, err
	}
	return transcript, nil
}

func writeSidecarRecipeWorkspace(t *testing.T, root string) {
	t.Helper()
	writeSidecarRecipeFile(t, filepath.Join(root, "go.mod"), "module example.com/sidecar\n\ngo 1.22\n")
	writeSidecarRecipeFile(t, filepath.Join(root, "cmd", "tool", "main.go"), `package main

func main() {
	_ = callToolMain()
}

func callToolMain() string {
	return runToolMainWithTelemetry()
}

func runToolMainWithTelemetry() string {
	return "sidecar-private-body-sentinel"
}
`)
	writeSidecarRecipeFile(t, filepath.Join(root, "auth", "retry.go"), `package auth

func Authenticate() string {
	return retryRequest()
}

func retryRequest() string {
	return "sidecar-auth-retry-sentinel"
}
`)
}

func writeSidecarRecipeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func normalizedShellSnippet(value string) string {
	value = strings.ReplaceAll(value, "\\\r\n", " ")
	value = strings.ReplaceAll(value, "\\\n", " ")
	return strings.Join(strings.Fields(value), " ")
}

func sidecarSmokeArgsWithConfig(args []string, configPath string) []string {
	copied := append([]string(nil), args...)
	if len(copied) < 2 || copied[0] != "tool" {
		return copied
	}
	if copied[1] == "list" {
		return append(copied, "--config", configPath)
	}
	if copied[1] == "schemas" {
		return copied
	}
	withConfig := make([]string, 0, len(copied)+2)
	withConfig = append(withConfig, copied[:2]...)
	withConfig = append(withConfig, "-config", configPath)
	withConfig = append(withConfig, copied[2:]...)
	return withConfig
}

func jsonValueAtDottedPath(root any, path string) (any, bool) {
	current := root
	for _, part := range strings.Split(path, ".") {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = object[part]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

func jsonValueEmpty(value any) bool {
	if value == nil {
		return true
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed) == ""
	case []any:
		return len(typed) == 0
	case map[string]any:
		return len(typed) == 0
	default:
		return strings.TrimSpace(fmt.Sprint(value)) == ""
	}
}
