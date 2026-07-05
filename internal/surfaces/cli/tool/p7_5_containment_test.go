package tool

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type p75DeniedToolPayload struct {
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

func writeMainConfigRoot(t *testing.T, root string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.json")
	configJSON := fmt.Sprintf(`{"workspace":{"root":%q,"cwd":%q}}`, root, root)

	if err := os.WriteFile(path, []byte(configJSON+"\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	return path
}

func TestP75DiscoveryProfileDeniesPrivatePathMutationWithoutLeakingInput(t *testing.T) {
	root := t.TempDir()
	configPath := writeMainConfigRoot(t, root)
	privatePath := filepath.Join(root, "private", ".env")
	secretSentinel := "P75_SECRET_SENTINEL_DO_NOT_LEAK"

	var stdout, stderr bytes.Buffer

	code := RunWithTelemetry([]string{
		"write_file",
		"-config", configPath,
		"--profile", "discovery",
		"--input", fmt.Sprintf(`{"path":%q,"content":%q}`, privatePath, secretSentinel),
	}, &stdout, &stderr, nil)

	if code != 1 {
		t.Fatalf("expected exit 1, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}

	payload := decodeP75DeniedToolPayload(t, stdout.Bytes())
	assertP75DiscoveryProfileDenial(t, payload, "write_file")
	assertP75NoSensitiveLeak(t, stdout.String()+stderr.String(), privatePath, root, secretSentinel)

	if _, err := os.Stat(privatePath); !os.IsNotExist(err) {
		t.Fatalf("denied private-path mutation should not create %s; stat err=%v", privatePath, err)
	}
}

func TestP75DiscoveryProfileDeniesShellBeforeExternalSideEffect(t *testing.T) {
	root := t.TempDir()
	configPath := writeMainConfigRoot(t, root)
	markerPath := filepath.Join(root, "shell-side-effect.txt")
	command := fmt.Sprintf("printf touched > %s", markerPath)

	var stdout, stderr bytes.Buffer

	code := RunWithTelemetry([]string{
		"shell",
		"-config", configPath,
		"--profile", "discovery",
		"--input", fmt.Sprintf(`{"command":%q}`, command),
	}, &stdout, &stderr, nil)

	if code != 1 {
		t.Fatalf("expected exit 1, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}

	payload := decodeP75DeniedToolPayload(t, stdout.Bytes())
	assertP75DiscoveryProfileDenial(t, payload, "shell")
	assertP75NoSensitiveLeak(t, stdout.String()+stderr.String(), markerPath, command)

	if _, err := os.Stat(markerPath); !os.IsNotExist(err) {
		t.Fatalf("denied shell should not create %s; stat err=%v", markerPath, err)
	}
}

func TestP75DiscoveryProfileDeniesArtifactWritingContextRequestBeforeExecution(t *testing.T) {
	root := t.TempDir()
	configPath := writeMainConfigRoot(t, root)
	secretTask := "P75_CONTEXT_TASK_SENTINEL_DO_NOT_LEAK"

	var stdout, stderr bytes.Buffer

	code := RunWithTelemetry([]string{
		"context_pack",
		"-config", configPath,
		"--profile", "discovery",
		"--input", fmt.Sprintf(`{"task":%q,"mode":"plan","limit":1}`, secretTask),
	}, &stdout, &stderr, nil)

	if code != 1 {
		t.Fatalf("expected exit 1, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}

	payload := decodeP75DeniedToolPayload(t, stdout.Bytes())
	assertP75DiscoveryProfileDenial(t, payload, "context_pack")
	assertP75NoSensitiveLeak(t, stdout.String()+stderr.String(), secretTask)

	for _, path := range []string{
		filepath.Join(root, ".idiom", "comprehension"),
		filepath.Join(root, ".idiom", "comprehension", "graph.sqlite"),
	} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("denied context_pack should not create %s; stat err=%v", path, err)
		}
	}
}

func TestP75DiscoveryProfileDeniesExternalSideEffectToolsBeforeExecution(t *testing.T) {
	root := t.TempDir()
	markerPath := filepath.Join(root, "external-side-effect.txt")
	configPath := writeP75ExternalToolConfig(t, root, []map[string]any{
		{
			"name":        "publish_release",
			"description": "mutating external release helper",
			"command":     "/bin/sh",
			"args":        []string{"-c", fmt.Sprintf("printf touched > %s", markerPath)},
			"cwd":         ".",
		},
	})

	var stdout, stderr bytes.Buffer

	code := RunWithTelemetry([]string{
		"publish_release",
		"-config", configPath,
		"--profile", "discovery",
		"--input", `{"target":"prod"}`,
	}, &stdout, &stderr, nil)

	if code != 1 {
		t.Fatalf("expected exit 1, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}

	payload := decodeP75DeniedToolPayload(t, stdout.Bytes())
	assertP75DiscoveryProfileDenial(t, payload, "publish_release")
	assertP75NoSensitiveLeak(t, stdout.String()+stderr.String(), markerPath)

	if _, err := os.Stat(markerPath); !os.IsNotExist(err) {
		t.Fatalf("denied external tool should not create %s; stat err=%v", markerPath, err)
	}
}

func TestP75DiscoveryProfileRequiresExternalReadOnlyDiscoveryDeclaration(t *testing.T) {
	for _, tc := range []struct {
		name      string
		readOnly  bool
		discovery bool
		wantCode  int
	}{
		{name: "read_only_without_discovery", readOnly: true, discovery: false, wantCode: 1},
		{name: "discovery_without_read_only", readOnly: false, discovery: true, wantCode: 1},
		{name: "read_only_discovery", readOnly: true, discovery: true, wantCode: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			configPath := writeP75ExternalToolConfig(t, root, []map[string]any{
				{
					"name":        tc.name,
					"description": "external wrapper with explicit safety metadata",
					"command":     "/bin/echo",
					"args":        []string{"external-ok"},
					"cwd":         ".",
					"read_only":   tc.readOnly,
					"discovery":   tc.discovery,
				},
			})

			var stdout, stderr bytes.Buffer

			code := RunWithTelemetry([]string{
				tc.name,
				"-config", configPath,
				"--profile", "discovery",
				"--input", `{}`,
			}, &stdout, &stderr, nil)

			if code != tc.wantCode {
				t.Fatalf("exit code = %d, want %d stdout=%s stderr=%s", code, tc.wantCode, stdout.String(), stderr.String())
			}

			if tc.wantCode == 1 {
				payload := decodeP75DeniedToolPayload(t, stdout.Bytes())
				assertP75DiscoveryProfileDenial(t, payload, tc.name)

				return
			}

			if !strings.Contains(stdout.String(), `"profile": "discovery"`) || !strings.Contains(stdout.String(), "external-ok") {
				t.Fatalf("allowed read-only discovery external tool should execute with discovery sidecar profile, stdout=%s", stdout.String())
			}
		})
	}
}

func decodeP75DeniedToolPayload(t *testing.T, data []byte) p75DeniedToolPayload {
	t.Helper()

	var payload p75DeniedToolPayload

	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("decode denial output: %v\n%s", err, string(data))
	}

	return payload
}

func assertP75DiscoveryProfileDenial(t *testing.T, payload p75DeniedToolPayload, tool string) {
	t.Helper()

	if payload.Tool != tool || payload.Sidecar.Profile != "discovery" {
		t.Fatalf("unexpected denial identity: %#v", payload)
	}

	if payload.Sidecar.Error.Class != "profile_denied" || payload.Sidecar.Error.Code != "profile_denied" {
		t.Fatalf("unexpected structured denial code: %#v", payload.Sidecar.Error)
	}

	if !strings.Contains(payload.Sidecar.Error.Message, "read_only_profile") {
		t.Fatalf("denial message should carry stable read_only_profile invariant: %#v", payload.Sidecar.Error)
	}
}

func assertP75NoSensitiveLeak(t *testing.T, publicPayload string, forbidden ...string) {
	t.Helper()

	for _, value := range forbidden {
		if value == "" {
			continue
		}

		if strings.Contains(publicPayload, value) {
			t.Fatalf("public denial payload leaked %q:\n%s", value, publicPayload)
		}
	}
}

func writeP75ExternalToolConfig(t *testing.T, root string, tools []map[string]any) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.json")
	payload := map[string]any{
		"workspace": map[string]any{
			"root":           root,
			"cwd":            root,
			"external_tools": tools,
		},
	}

	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal external tool config: %v", err)
	}

	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		t.Fatalf("write external tool config: %v", err)
	}

	return path
}
