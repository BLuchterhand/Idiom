package sidecarvalidation

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSidecarV1CompatibilityGoldenCases(t *testing.T) {
	root := t.TempDir()
	writeSidecarCompatWorkspace(t, root)

	configPath := writeMainConfigRoot(t, root)

	cases := []struct {
		name       string
		args       []string
		exitCode   int
		wantStable map[string]string
		wantNull   []string
		wantText   []string
		wantStdout []string
	}{
		{
			name:     "success legacy and caller metadata",
			args:     []string{"tool", "pwd", "-config", configPath, "--caller-id", "codex-cli", "--request-id", "req-123", "--trace-id", "trace:abc"},
			exitCode: 0,
			wantStable: map[string]string{
				"Tool":                             "pwd",
				"Error":                            "",
				"sidecar.schema_version":           "idiom.tool.v1",
				"sidecar.tool":                     "pwd",
				"sidecar.profile":                  "default",
				"sidecar.caller.caller_id":         "codex-cli",
				"sidecar.caller.request_id":        "req-123",
				"sidecar.caller.trace_id":          "trace:abc",
				"sidecar.contract.id":              "idiom.tool.v1:pwd",
				"sidecar.contract.kind":            "workspace_tool_result",
				"sidecar.contract.transport":       "cli",
				"sidecar.structured_output.format": "text",
			},
			wantNull: []string{"sidecar.error", "sidecar.freshness"},
			wantText: []string{"Summary", "Output"},
		},
		{
			name:     "unknown tool typed error",
			args:     []string{"tool", "not_a_tool", "-config", configPath},
			exitCode: 1,
			wantStable: map[string]string{
				"Tool":                   "not_a_tool",
				"sidecar.schema_version": "idiom.tool.v1",
				"sidecar.tool":           "not_a_tool",
				"sidecar.profile":        "default",
				"sidecar.contract.id":    "idiom.tool.v1:not_a_tool",
				"sidecar.error.class":    "unsupported_tool",
				"sidecar.error.code":     "unsupported_tool",
			},
			wantText: []string{"Summary", "Error", "sidecar.error.message"},
		},
		{
			name:     "discovery profile denial typed error",
			args:     []string{"tool", "context_status", "-config", configPath, "--profile", "discovery", "--input", `{"refresh":true}`},
			exitCode: 1,
			wantStable: map[string]string{
				"Tool":                   "context_status",
				"sidecar.schema_version": "idiom.tool.v1",
				"sidecar.tool":           "context_status",
				"sidecar.profile":        "discovery",
				"sidecar.contract.id":    "idiom.tool.v1:context_status",
				"sidecar.error.class":    "profile_denied",
				"sidecar.error.code":     "profile_denied",
			},
			wantNull: []string{"sidecar.freshness"},
			wantText: []string{"Error", "sidecar.error.message"},
		},
		{
			name:     "backend fallback is observable without sidecar fallback overclaim",
			args:     []string{"tool", "source_sanity", "-config", configPath, "--input", `{"path":"script.gd"}`},
			exitCode: 0,
			wantStable: map[string]string{
				"Tool":                             "source_sanity",
				"Error":                            "",
				"sidecar.schema_version":           "idiom.tool.v1",
				"sidecar.tool":                     "source_sanity",
				"sidecar.profile":                  "default",
				"sidecar.contract.id":              "idiom.tool.v1:source_sanity",
				"sidecar.contract.transport":       "cli",
				"sidecar.structured_output.format": "text",
			},
			wantNull:   []string{"sidecar.error", "sidecar.freshness"},
			wantText:   []string{"Summary", "Output"},
			wantStdout: []string{"generic fallback", "generic_source_sanity"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer

			code := runMain(tc.args, &stdout, &stderr)

			if code != tc.exitCode {
				t.Fatalf("exit code = %d, want %d\nstdout=%s\nstderr=%s", code, tc.exitCode, stdout.String(), stderr.String())
			}

			payload := decodeSidecarCompatJSON(t, stdout.Bytes())
			assertSidecarCompatLegacyFields(t, payload)

			for path, want := range tc.wantStable {
				got, ok := jsonValueAtDottedPath(payload, path)
				if !ok {
					t.Fatalf("missing JSON path %s in %s", path, stdout.String())
				}

				if fmt.Sprint(got) != want {
					t.Fatalf("JSON path %s = %q, want %q", path, fmt.Sprint(got), want)
				}
			}

			for _, path := range tc.wantNull {
				got, ok := jsonValueAtDottedPath(payload, path)
				if !ok {
					t.Fatalf("missing JSON path %s in %s", path, stdout.String())
				}

				if got != nil {
					t.Fatalf("JSON path %s = %#v, want null", path, got)
				}
			}

			for _, path := range tc.wantText {
				got, ok := jsonValueAtDottedPath(payload, path)
				if !ok || strings.TrimSpace(fmt.Sprint(got)) == "" {
					t.Fatalf("JSON path %s should be non-empty text, got %#v", path, got)
				}
			}

			for _, want := range tc.wantStdout {
				if !strings.Contains(stdout.String(), want) {
					t.Fatalf("stdout missing %q:\n%s", want, stdout.String())
				}
			}

			assertNoPublicToolPayloadStrategyLeakage(t, payload)
		})
	}
}

func TestSidecarV1UsageErrorsStayOutOfResultEnvelope(t *testing.T) {
	configPath := writeMainConfig(t)
	cases := []struct {
		name       string
		args       []string
		exitCode   int
		stderrText string
	}{
		{
			name:       "malformed json input",
			args:       []string{"tool", "harness_feedback", "-config", configPath, "--input", "{"},
			exitCode:   1,
			stderrText: "invalid input JSON",
		},
		{
			name:       "unknown profile",
			args:       []string{"tool", "pwd", "-config", configPath, "--profile", "verify"},
			exitCode:   2,
			stderrText: "unknown tool profile",
		},
		{
			name:       "path-shaped caller metadata",
			args:       []string{"tool", "pwd", "-config", configPath, "--caller-id", "/home/alice/private/repo"},
			exitCode:   2,
			stderrText: "caller-id must be a short opaque identifier, not a path-shaped value",
		},
		{
			name:       "token-shaped caller metadata",
			args:       []string{"tool", "pwd", "-config", configPath, "--caller-id", "sk-proj-abc123"},
			exitCode:   2,
			stderrText: "caller-id must be a short opaque identifier, not a credential-shaped value",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer

			code := runMain(tc.args, &stdout, &stderr)

			if code != tc.exitCode {
				t.Fatalf("exit code = %d, want %d\nstdout=%s\nstderr=%s", code, tc.exitCode, stdout.String(), stderr.String())
			}

			if strings.TrimSpace(stdout.String()) != "" {
				t.Fatalf("usage/input rejection should not emit a partial sidecar result, got stdout=%s", stdout.String())
			}

			if !strings.Contains(stderr.String(), tc.stderrText) {
				t.Fatalf("stderr missing %q: %s", tc.stderrText, stderr.String())
			}
		})
	}
}

func writeSidecarCompatWorkspace(t *testing.T, root string) {
	t.Helper()

	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/sidecarcompat\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "script.gd"), []byte("func _ready():\n\tprint(\"ok\")\n"), 0o644); err != nil {
		t.Fatalf("write script.gd: %v", err)
	}
}

func decodeSidecarCompatJSON(t *testing.T, data []byte) map[string]any {
	t.Helper()

	var payload map[string]any

	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("decode sidecar JSON: %v\n%s", err, string(data))
	}

	return payload
}

func assertSidecarCompatLegacyFields(t *testing.T, payload map[string]any) {
	t.Helper()

	for _, key := range []string{"Tool", "Summary", "Output", "Error", "sidecar"} {
		if _, ok := payload[key]; !ok {
			t.Fatalf("missing legacy/sidecar key %q in %#v", key, payload)
		}
	}

	sidecar, ok := payload["sidecar"].(map[string]any)
	if !ok {
		t.Fatalf("sidecar should be a JSON object, got %#v", payload["sidecar"])
	}

	for _, key := range []string{"schema_version", "tool", "profile", "caller", "contract", "freshness", "warnings", "audit_event_ids", "structured_output", "error"} {
		if _, ok := sidecar[key]; !ok {
			t.Fatalf("missing stable sidecar key %q in %#v", key, sidecar)
		}
	}
}

func assertNoPublicToolPayloadStrategyLeakage(t *testing.T, payload map[string]any) {
	t.Helper()

	rendered, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal public payload: %v", err)
	}

	lower := strings.ToLower(string(rendered))
	// Scan the full public JSON object: legacy Tool/Summary/Output/Error fields
	// remain externally visible alongside sidecar. Keep the phrase list focused
	// on strategy-prescriptive text so hard invariant denials can still explain
	// why a request was blocked.
	for _, forbidden := range []string{
		"next tool",
		"next step",
		"you should",
		"you must call",
		"must call",
		"should call",
		"use context_pack next",
		"run context_pack next",
		"implement the fix",
		"edit the file",
		"raw_prompt",
		"file_body",
		"private_path",
	} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("public tool payload contains strategy/privacy leakage phrase %q:\n%s", forbidden, string(rendered))
		}
	}
}
