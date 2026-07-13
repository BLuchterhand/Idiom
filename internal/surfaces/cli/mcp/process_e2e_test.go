package mcp

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	mcppkg "idiom/internal/capabilities/mcp"
	"idiom/internal/capabilities/workspace"
)

var _ = ginkgo.Describe("MCP stdio process boundary", ginkgo.Ordered, ginkgo.Label("e2e", "integration", "smoke"), func() {
	var idiomBinary string

	ginkgo.BeforeAll(func() {
		idiomBinary = buildMCPProcessE2EBinary()
	})

	ginkgo.It("serves discovery tools and protected metadata through the compiled CLI", func() {
		root := ginkgo.GinkgoT().TempDir()
		publicBody := "MCP_PROCESS_PUBLIC_BODY_MUST_NOT_LEAK"
		protectedBody := "MCP_PROCESS_PROTECTED_BODY_MUST_NOT_LEAK"

		gomega.Expect(os.WriteFile(filepath.Join(root, "README.md"), []byte(publicBody+"\n"), 0o644)).To(gomega.Succeed())
		gomega.Expect(os.MkdirAll(filepath.Join(root, ".git"), 0o755)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(root, ".git", "config"), []byte(protectedBody+"\n"), 0o644)).To(gomega.Succeed())

		configPath := writeMainConfigRoot(root)
		processHome := ginkgo.GinkgoT().TempDir()

		// The deadline only guards the test from a stuck child or protocol call.
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		transport, err := mcppkg.StartStdio(ctx, idiomBinary, []string{
			"mcp", "serve",
			"--stdio",
			"--profile", workspace.ToolProfileDiscovery,
			"--config", configPath,
			"--caller-id", "process-smoke",
			"--request-id", "process-smoke-request",
			"--trace-id", "trace:process-smoke",
		}, []string{
			"HOME=" + processHome,
			"IDIOM_DISABLE_GLOBAL_CONFIG_FALLBACK=1",
			"IDIOM_TELEMETRY=0",
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		client := mcppkg.NewClient(transport)
		closed := false
		ginkgo.DeferCleanup(func() {
			if !closed {
				_ = client.Close()
			}
		})

		gomega.Expect(client.Start(ctx)).To(gomega.Succeed())
		gomega.Expect(client.Initialize(ctx, "idiom-process-e2e")).To(gomega.Succeed())

		tools, err := client.ListTools(ctx)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		toolNames := make([]string, 0, len(tools))
		for _, tool := range tools {
			toolNames = append(toolNames, tool.Name)
		}
		gomega.Expect(toolNames).To(gomega.ContainElement("context_status"))
		for _, forbidden := range []string{"read_file", "write_file", "shell", "edit_file", "apply_patch"} {
			gomega.Expect(toolNames).NotTo(gomega.ContainElement(forbidden))
		}

		allowed, err := client.CallTool(ctx, "context_status", json.RawMessage(`{"limit":1}`))
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(allowed.IsError).To(gomega.BeFalse())

		allowedPayload := decodeMCPClientToolPayload(allowed)
		gomega.Expect(allowedPayload.Tool).To(gomega.Equal("context_status"))
		gomega.Expect(allowedPayload.Sidecar.Profile).To(gomega.Equal(workspace.ToolProfileDiscovery))
		gomega.Expect(allowedPayload.Sidecar.Caller.CallerID).To(gomega.Equal("process-smoke"))
		gomega.Expect(allowedPayload.Sidecar.Caller.RequestID).To(gomega.Equal("process-smoke-request"))
		gomega.Expect(allowedPayload.Sidecar.Caller.TraceID).To(gomega.Equal("trace:process-smoke"))
		gomega.Expect(allowedPayload.Sidecar.Contract.Transport).To(gomega.Equal(StdioTransport))
		gomega.Expect(allowedPayload.Sidecar.Error).To(gomega.BeNil())

		denied, err := client.CallTool(ctx, "context_status", json.RawMessage(`{"refresh":true}`))
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(denied.IsError).To(gomega.BeTrue())

		deniedPayload := decodeMCPClientToolPayload(denied)
		gomega.Expect(deniedPayload.Sidecar.Error).NotTo(gomega.BeNil())
		gomega.Expect(deniedPayload.Sidecar.Error.Class).To(gomega.Equal("profile_denied"))
		gomega.Expect(deniedPayload.Sidecar.Contract.Transport).To(gomega.Equal(StdioTransport))

		resources, err := client.ListResources(ctx)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		resourceURIs := make([]string, 0, len(resources))
		for _, resource := range resources {
			resourceURIs = append(resourceURIs, resource.URI)
		}
		gomega.Expect(resourceURIs).To(gomega.ContainElements(
			"idiom://workspace/root",
			"idiom://workspace/discovery-tools",
			"idiom://workspace/resource-policy",
		))

		readme, err := client.ReadResource(ctx, "idiom://workspace/file/README.md")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(readme.Contents).To(gomega.HaveLen(1))

		var readmeMetadata map[string]any
		gomega.Expect(json.Unmarshal([]byte(readme.Contents[0].Text), &readmeMetadata)).To(gomega.Succeed())
		gomega.Expect(readmeMetadata).To(gomega.HaveKeyWithValue("kind", "workspace_path"))
		gomega.Expect(readmeMetadata).To(gomega.HaveKeyWithValue("relative_path", "README.md"))
		gomega.Expect(readmeMetadata).To(gomega.HaveKeyWithValue("metadata_only", true))
		gomega.Expect(readmeMetadata).To(gomega.HaveKeyWithValue("content_available", false))

		_, err = client.ReadResource(ctx, "idiom://workspace/file/.git/config")
		gomega.Expect(err).To(gomega.MatchError(gomega.And(
			gomega.ContainSubstring("resources/read: server error -32602"),
			gomega.ContainSubstring("resource denied: protected path"),
		)))

		publicOutput := strings.Join([]string{
			mcpClientContentText(allowed),
			mcpClientContentText(denied),
			readme.Contents[0].Text,
			err.Error(),
		}, "\n")
		for _, forbidden := range []string{root, publicBody, protectedBody} {
			gomega.Expect(publicOutput).NotTo(gomega.ContainSubstring(forbidden))
		}
		assertMCPPublicPayloadSafe(publicOutput, root)
		assertNoMCPStrategyLeakage(publicOutput)

		gomega.Expect(client.Close()).To(gomega.Succeed())
		closed = true
	})
})

func buildMCPProcessE2EBinary() string {
	repoRoot := repoRootForCmdTest()
	binaryPath := filepath.Join(ginkgo.GinkgoT().TempDir(), "idiom")

	// The deadline only prevents a wedged toolchain from hanging the suite.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "go", "build", "-o", binaryPath, "./cmd/idiom")
	cmd.Dir = repoRoot

	output, err := cmd.CombinedOutput()
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "build idiom binary output:\n%s", string(output))

	return binaryPath
}
