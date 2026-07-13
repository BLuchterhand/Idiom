package tool

import (
	"bytes"
	"encoding/json"
	"fmt"
	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"os"
	"path/filepath"
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

func writeMainConfigRoot(root string) string {

	path := filepath.Join(ginkgo.GinkgoT().TempDir(), "config.json")
	configJSON := fmt.Sprintf(`{"workspace":{"root":%q,"cwd":%q}}`, root, root)
	gomega.Expect(os.WriteFile(path, []byte(configJSON+"\n"), 0o644)).To(gomega.Succeed())

	return path
}

var _ = ginkgo.Describe("p7 5 containment", func() {
	ginkgo.It("p75 discovery profile denies private path mutation without leaking input", func() {
		root := ginkgo.GinkgoT().TempDir()
		configPath := writeMainConfigRoot(root)
		privatePath := filepath.Join(root, "private", ".env")
		secretSentinel := "P75_SECRET_SENTINEL_DO_NOT_LEAK"

		var stdout, stderr bytes.Buffer

		code := RunWithTelemetry([]string{
			"write_file",
			"-config", configPath,
			"--profile", "discovery",
			"--input", fmt.Sprintf(`{"path":%q,"content":%q}`, privatePath, secretSentinel),
		}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(1), fmt.Sprintf("expected exit 1, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String()))

		payload := decodeP75DeniedToolPayload(stdout.Bytes())
		assertP75DiscoveryProfileDenial(payload, "write_file")
		assertP75NoSensitiveLeak(stdout.String()+stderr.String(), privatePath, root, secretSentinel)

		if _, err := os.Stat(privatePath); !os.IsNotExist(err) {
			ginkgo.Fail(fmt.Sprintf("denied private-path mutation should not create %s; stat err=%v", privatePath, err))
		}
	})
	ginkgo.It("p75 discovery profile denies shell before external side effect", func() {
		root := ginkgo.GinkgoT().TempDir()
		configPath := writeMainConfigRoot(root)
		markerPath := filepath.Join(root, "shell-side-effect.txt")
		command := fmt.Sprintf("printf touched > %s", markerPath)

		var stdout, stderr bytes.Buffer

		code := RunWithTelemetry([]string{
			"shell",
			"-config", configPath,
			"--profile", "discovery",
			"--input", fmt.Sprintf(`{"command":%q}`, command),
		}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(1), fmt.Sprintf("expected exit 1, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String()))

		payload := decodeP75DeniedToolPayload(stdout.Bytes())
		assertP75DiscoveryProfileDenial(payload, "shell")
		assertP75NoSensitiveLeak(stdout.String()+stderr.String(), markerPath, command)

		if _, err := os.Stat(markerPath); !os.IsNotExist(err) {
			ginkgo.Fail(fmt.Sprintf("denied shell should not create %s; stat err=%v", markerPath, err))
		}
	})
	ginkgo.It("p75 discovery profile denies artifact writing context request before execution", func() {
		root := ginkgo.GinkgoT().TempDir()
		configPath := writeMainConfigRoot(root)
		secretTask := "P75_CONTEXT_TASK_SENTINEL_DO_NOT_LEAK"

		var stdout, stderr bytes.Buffer

		code := RunWithTelemetry([]string{
			"context_pack",
			"-config", configPath,
			"--profile", "discovery",
			"--input", fmt.Sprintf(`{"task":%q,"mode":"plan","limit":1}`, secretTask),
		}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(1), fmt.Sprintf("expected exit 1, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String()))

		payload := decodeP75DeniedToolPayload(stdout.Bytes())
		assertP75DiscoveryProfileDenial(payload, "context_pack")
		assertP75NoSensitiveLeak(stdout.String()+stderr.String(), secretTask)

		for _, path := range []string{
			filepath.Join(root, ".idiom", "comprehension"),
			filepath.Join(root, ".idiom", "comprehension", "graph.sqlite"),
		} {
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				ginkgo.Fail(fmt.Sprintf("denied context_pack should not create %s; stat err=%v", path, err))
			}
		}
	})
	ginkgo.It("p75 discovery profile denies external side effect tools before execution", func() {
		root := ginkgo.GinkgoT().TempDir()
		markerPath := filepath.Join(root, "external-side-effect.txt")
		configPath := writeP75ExternalToolConfig(root, []map[string]any{
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
		gomega.Expect(code).To(gomega.Equal(1), fmt.Sprintf("expected exit 1, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String()))

		payload := decodeP75DeniedToolPayload(stdout.Bytes())
		assertP75DiscoveryProfileDenial(payload, "publish_release")
		assertP75NoSensitiveLeak(stdout.String()+stderr.String(), markerPath)

		if _, err := os.Stat(markerPath); !os.IsNotExist(err) {
			ginkgo.Fail(fmt.Sprintf("denied external tool should not create %s; stat err=%v", markerPath, err))
		}
	})
	ginkgo.DescribeTable("requires both read-only and discovery declarations for external tools",
		func(name string, readOnly, discovery bool, wantCode int) {
			root := ginkgo.GinkgoT().TempDir()
			configPath := writeP75ExternalToolConfig(root, []map[string]any{{
				"name":        name,
				"description": "external wrapper with explicit safety metadata",
				"command":     "/bin/echo",
				"args":        []string{"external-ok"},
				"cwd":         ".",
				"read_only":   readOnly,
				"discovery":   discovery,
			}})
			var stdout, stderr bytes.Buffer

			code := RunWithTelemetry([]string{
				name,
				"-config", configPath,
				"--profile", "discovery",
				"--input", `{}`,
			}, &stdout, &stderr, nil)
			gomega.Expect(code).To(gomega.Equal(wantCode), "stdout=%s stderr=%s", stdout.String(), stderr.String())
			if wantCode == 1 {
				assertP75DiscoveryProfileDenial(decodeP75DeniedToolPayload(stdout.Bytes()), name)
				return
			}
			gomega.Expect(stdout.String()).To(gomega.ContainSubstring(`"profile": "discovery"`))
			gomega.Expect(stdout.String()).To(gomega.ContainSubstring("external-ok"))
		},
		ginkgo.Entry("rejects read-only without discovery", "read_only_without_discovery", true, false, 1),
		ginkgo.Entry("rejects discovery without read-only", "discovery_without_read_only", false, true, 1),
		ginkgo.Entry("allows read-only discovery", "read_only_discovery", true, true, 0),
	)
})

func decodeP75DeniedToolPayload(data []byte) p75DeniedToolPayload {

	var payload p75DeniedToolPayload
	gomega.Expect(json.Unmarshal(data, &payload)).To(gomega.Succeed())

	return payload
}

func assertP75DiscoveryProfileDenial(payload p75DeniedToolPayload, tool string) {
	gomega.Expect(payload.Tool).To(gomega.Equal(tool), fmt.Sprintf("unexpected denial identity: %#v", payload))
	gomega.Expect(payload.Sidecar.Profile).To(gomega.Equal("discovery"), fmt.Sprintf("unexpected denial identity: %#v", payload))
	gomega.Expect(payload.Sidecar.Error.Class).To(gomega.Equal("profile_denied"), fmt.Sprintf("unexpected structured denial code: %#v", payload.Sidecar.Error))
	gomega.Expect(payload.Sidecar.Error.Code).To(gomega.Equal("profile_denied"), fmt.Sprintf("unexpected structured denial code: %#v", payload.Sidecar.Error))
	gomega.Expect(payload.Sidecar.Error.Message).To(gomega.ContainSubstring("read_only_profile"), fmt.Sprintf("denial message should carry stable read_only_profile invariant: %#v", payload.Sidecar.Error))

}

func assertP75NoSensitiveLeak(publicPayload string, forbidden ...string) {

	for _, value := range forbidden {
		if value == "" {
			continue
		}
		gomega.Expect(publicPayload).NotTo(gomega.ContainSubstring(value), fmt.Sprintf("public denial payload leaked %q:\n%s", value, publicPayload))

	}
}

func writeP75ExternalToolConfig(root string, tools []map[string]any) string {

	path := filepath.Join(ginkgo.GinkgoT().TempDir(), "config.json")
	payload := map[string]any{
		"workspace": map[string]any{
			"root":           root,
			"cwd":            root,
			"external_tools": tools,
		},
	}

	data, err := json.Marshal(payload)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal external tool config: %v", err))
	gomega.Expect(os.WriteFile(path, append(data, '\n'), 0o644)).To(gomega.Succeed())

	return path
}
