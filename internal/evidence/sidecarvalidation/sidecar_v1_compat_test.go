package sidecarvalidation

import (
	"bytes"
	"encoding/json"
	"fmt"
	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"os"
	"path/filepath"
	"strings"
)

var _ = ginkgo.It("sidecar v1 compatibility golden cases", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	writeSidecarCompatWorkspace(root)

	configPath := writeMainConfigRoot(root)

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
		ginkgo.By(tc.name)
		func() {
			var stdout, stderr bytes.Buffer

			code := runMain(tc.args, &stdout, &stderr)

			gomega.Expect(code).To(gomega.BeEquivalentTo(tc.exitCode), fmt.Sprintf("exit code = %d, want %d\nstdout=%s\nstderr=%s", code, tc.exitCode, stdout.String(), stderr.String()))

			payload := decodeSidecarCompatJSON(stdout.Bytes())
			assertSidecarCompatLegacyFields(payload)

			for path, want := range tc.wantStable {
				got, ok := jsonValueAtDottedPath(payload, path)
				gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("missing JSON path %s in %s", path, stdout.String()))

				gomega.Expect(fmt.Sprint(got)).To(gomega.BeEquivalentTo(want), fmt.Sprintf("JSON path %s = %q, want %q", path, fmt.Sprint(got), want))
			}

			for _, path := range tc.wantNull {
				got, ok := jsonValueAtDottedPath(payload, path)
				gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("missing JSON path %s in %s", path, stdout.String()))

				gomega.Expect(got).To(gomega.BeNil(), fmt.Sprintf("JSON path %s = %#v, want null", path, got))
			}

			for _, path := range tc.wantText {
				got, ok := jsonValueAtDottedPath(payload, path)
				gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("JSON path %s should be non-empty text, got %#v", path, got))
				gomega.Expect(strings.TrimSpace(fmt.Sprint(got))).NotTo(gomega.BeEquivalentTo(""), fmt.Sprintf("JSON path %s should be non-empty text, got %#v", path, got))
			}

			for _, want := range tc.wantStdout {
				gomega.Expect(stdout.String()).To(gomega.ContainSubstring(want), fmt.Sprintf("stdout missing %q:\n%s", want, stdout.String()))
			}

			assertNoPublicToolPayloadStrategyLeakage(payload)
		}()
	}
})
var _ = ginkgo.It("sidecar v1 usage errors stay out of result envelope", func() {
	configPath := writeMainConfig()
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
		ginkgo.By(tc.name)
		func() {
			var stdout, stderr bytes.Buffer

			code := runMain(tc.args, &stdout, &stderr)

			gomega.Expect(code).To(gomega.BeEquivalentTo(tc.exitCode), fmt.Sprintf("exit code = %d, want %d\nstdout=%s\nstderr=%s", code, tc.exitCode, stdout.String(), stderr.String()))

			gomega.Expect(strings.TrimSpace(stdout.String())).To(gomega.BeEquivalentTo(""), fmt.Sprintf("usage/input rejection should not emit a partial sidecar result, got stdout=%s", stdout.String()))

			gomega.Expect(stderr.String()).To(gomega.ContainSubstring(tc.stderrText), fmt.Sprintf("stderr missing %q: %s", tc.stderrText, stderr.String()))
		}()
	}
})

func writeSidecarCompatWorkspace(root string) {
	ginkgo.GinkgoHelper()

	gomega.Expect(os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/sidecarcompat\n\ngo 1.22\n"), 0o644)).To(gomega.Succeed(), "write go.mod")

	gomega.Expect(os.WriteFile(filepath.Join(root, "script.gd"), []byte("func _ready():\n\tprint(\"ok\")\n"), 0o644)).To(gomega.Succeed(), "write script.gd")
}

func decodeSidecarCompatJSON(data []byte) map[string]any {
	ginkgo.GinkgoHelper()

	var payload map[string]any

	gomega.Expect(json.Unmarshal(data, &payload)).To(gomega.Succeed(), "decode sidecar JSON:\n%s", string(data))

	return payload
}

func assertSidecarCompatLegacyFields(payload map[string]any) {
	ginkgo.GinkgoHelper()

	for _, key := range []string{"Tool", "Summary", "Output", "Error", "sidecar"} {
		{
			_, ok := payload[key]
			gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("missing legacy/sidecar key %q in %#v", key, payload))
		}
	}

	sidecar, ok := payload["sidecar"].(map[string]any)
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("sidecar should be a JSON object, got %#v", payload["sidecar"]))

	for _, key := range []string{"schema_version", "tool", "profile", "caller", "contract", "freshness", "warnings", "audit_event_ids", "structured_output", "error"} {
		{
			_, ok := sidecar[key]
			gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("missing stable sidecar key %q in %#v", key, sidecar))
		}
	}
}

func assertNoPublicToolPayloadStrategyLeakage(payload map[string]any) {
	ginkgo.GinkgoHelper()

	rendered, err := json.Marshal(payload)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal public payload: %v", err))

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
		gomega.Expect(lower).NotTo(gomega.ContainSubstring(forbidden), fmt.Sprintf("public tool payload contains strategy/privacy leakage phrase %q:\n%s", forbidden, string(rendered)))
	}
}
