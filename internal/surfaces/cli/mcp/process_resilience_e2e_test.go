package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	mcppkg "idiom/internal/capabilities/mcp"
	"idiom/internal/capabilities/workspace"
	"idiom/internal/platform/commandexec"
)

type rawMCPProcessResponse struct {
	ID     json.RawMessage `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

var _ = ginkgo.Describe("compiled MCP protocol resilience", ginkgo.Ordered, ginkgo.Label("development-scenario", "e2e", "integration", "smoke"), func() {
	var idiomBinary string

	ginkgo.BeforeAll(func() {
		idiomBinary = buildMCPProcessE2EBinary()
	})

	ginkgo.It("reassembles fragmented frames and continues after malformed and invalid requests", func(ctx ginkgo.SpecContext) {
		root := ginkgo.GinkgoT().TempDir()
		writeWorkspaceMCPFile(root, "README.md", "COMPILED_MCP_RAW_BODY_MUST_NOT_LEAK\n")
		configPath := writeMainConfigRoot(root)
		processHome := ginkgo.GinkgoT().TempDir()

		command := exec.CommandContext(ctx, idiomBinary,
			"mcp", "serve", "--stdio",
			"--profile", workspace.ToolProfileDiscovery,
			"--config", configPath,
		)
		command.Env = commandexec.EnvWithCanonicalPWD(os.Environ(), "", []string{
			"HOME=" + processHome,
			"IDIOM_DISABLE_GLOBAL_CONFIG_FALLBACK=1",
			"IDIOM_TELEMETRY=0",
		})
		stdin, err := command.StdinPipe()
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		stdout, err := command.StdoutPipe()
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		var stderr bytes.Buffer
		command.Stderr = &stderr
		gomega.Expect(command.Start()).To(gomega.Succeed())

		_, err = io.WriteString(stdin, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":`)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		_, err = io.WriteString(stdin, `{}}`+"\n")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		_, err = io.WriteString(stdin, `{"jsonrpc":"2.0","method":"notifications/initialized"}`+"\n")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		_, err = io.WriteString(stdin, `{"jsonrpc":`+"\n")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		_, err = io.WriteString(stdin, `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`+"\n")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		_, err = io.WriteString(stdin, `{"jsonrpc":"2.0","id":3,"method":"resources/read","params":{}}`+"\n")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		_, err = io.WriteString(stdin, `{"jsonrpc":"2.0","id":4,"method":"resources/list","params":{}}`+"\n")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(stdin.Close()).To(gomega.Succeed())

		decoder := json.NewDecoder(stdout)
		responses := make([]rawMCPProcessResponse, 5)
		for index := range responses {
			gomega.Expect(decoder.Decode(&responses[index])).To(gomega.Succeed())
		}
		gomega.Expect(command.Wait()).To(gomega.Succeed(), stderr.String())

		gomega.Expect(string(responses[0].ID)).To(gomega.Equal("1"))
		gomega.Expect(responses[0].Error).To(gomega.BeNil())
		gomega.Expect(string(responses[0].Result)).To(gomega.ContainSubstring(`"protocolVersion"`))

		gomega.Expect(responses[1].ID).To(gomega.BeEmpty())
		gomega.Expect(responses[1].Error).NotTo(gomega.BeNil())
		gomega.Expect(responses[1].Error.Code).To(gomega.Equal(-32700))

		gomega.Expect(string(responses[2].ID)).To(gomega.Equal("2"))
		gomega.Expect(responses[2].Error).To(gomega.BeNil())
		gomega.Expect(string(responses[2].Result)).To(gomega.ContainSubstring(`"context_status"`))

		gomega.Expect(string(responses[3].ID)).To(gomega.Equal("3"))
		gomega.Expect(responses[3].Error).NotTo(gomega.BeNil())
		gomega.Expect(responses[3].Error.Code).To(gomega.Equal(-32602))

		gomega.Expect(string(responses[4].ID)).To(gomega.Equal("4"))
		gomega.Expect(responses[4].Error).To(gomega.BeNil())
		gomega.Expect(string(responses[4].Result)).To(gomega.ContainSubstring(`"idiom://workspace/root"`))

		encoded, err := json.Marshal(responses)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		for _, forbidden := range []string{root, "COMPILED_MCP_RAW_BODY_MUST_NOT_LEAK"} {
			gomega.Expect(string(encoded)).NotTo(gomega.ContainSubstring(forbidden))
		}
		gomega.Expect(stderr.String()).To(gomega.BeEmpty())
	}, ginkgo.SpecTimeout(30*time.Second))

	ginkgo.It("correlates concurrent metadata reads while isolating a protected-resource denial", func(ctx ginkgo.SpecContext) {
		root := ginkgo.GinkgoT().TempDir()
		const requestCount = 16
		for index := 0; index < requestCount; index++ {
			writeWorkspaceMCPFile(root, fmt.Sprintf("src/component_%02d.go", index), fmt.Sprintf("package src\n\n// PRIVATE_COMPONENT_BODY_%02d\n", index))
		}
		writeWorkspaceMCPFile(root, ".git/config", "PROTECTED_GIT_BODY_MUST_NOT_LEAK\n")

		configPath := writeMainConfigRoot(root)
		processHome := ginkgo.GinkgoT().TempDir()
		processContext, cancelProcess := context.WithCancel(ctx)
		ginkgo.DeferCleanup(cancelProcess)

		transport, err := mcppkg.StartStdio(processContext, idiomBinary, []string{
			"mcp", "serve", "--stdio",
			"--profile", workspace.ToolProfileDiscovery,
			"--config", configPath,
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
		gomega.Expect(client.Start(processContext)).To(gomega.Succeed())
		gomega.Expect(client.Initialize(processContext, "compiled-concurrency-e2e")).To(gomega.Succeed())

		type resourceOutcome struct {
			rel    string
			result mcppkg.ResourceReadResult
			err    error
		}
		outcomes := make(chan resourceOutcome, requestCount+1)
		var reads sync.WaitGroup
		for index := 0; index < requestCount; index++ {
			rel := fmt.Sprintf("src/component_%02d.go", index)
			reads.Add(1)
			go func() {
				defer reads.Done()
				result, err := client.ReadResource(processContext, "idiom://workspace/file/"+rel)
				outcomes <- resourceOutcome{rel: rel, result: result, err: err}
			}()
		}
		reads.Add(1)
		go func() {
			defer reads.Done()
			result, err := client.ReadResource(processContext, "idiom://workspace/file/.git/config")
			outcomes <- resourceOutcome{rel: ".git/config", result: result, err: err}
		}()
		reads.Wait()
		close(outcomes)

		var publicOutput strings.Builder
		for outcome := range outcomes {
			if outcome.rel == ".git/config" {
				gomega.Expect(outcome.err).To(gomega.MatchError(gomega.And(
					gomega.ContainSubstring("resources/read: server error -32602"),
					gomega.ContainSubstring("resource denied: protected path"),
				)))
				publicOutput.WriteString(outcome.err.Error())

				continue
			}

			gomega.Expect(outcome.err).NotTo(gomega.HaveOccurred())
			gomega.Expect(outcome.result.Contents).To(gomega.HaveLen(1))
			var metadata map[string]any
			gomega.Expect(json.Unmarshal([]byte(outcome.result.Contents[0].Text), &metadata)).To(gomega.Succeed())
			gomega.Expect(metadata).To(gomega.HaveKeyWithValue("relative_path", outcome.rel))
			gomega.Expect(metadata).To(gomega.HaveKeyWithValue("metadata_only", true))
			gomega.Expect(metadata).To(gomega.HaveKeyWithValue("content_available", false))
			publicOutput.WriteString(outcome.result.Contents[0].Text)
		}

		for _, forbidden := range []string{root, "PRIVATE_COMPONENT_BODY_", "PROTECTED_GIT_BODY_MUST_NOT_LEAK"} {
			gomega.Expect(publicOutput.String()).NotTo(gomega.ContainSubstring(forbidden))
		}
		assertMCPPublicPayloadSafe(publicOutput.String(), root)

		gomega.Expect(client.Close()).To(gomega.Succeed())
		closed = true
	}, ginkgo.SpecTimeout(30*time.Second))
})

func writeWorkspaceMCPFile(root string, relativePath string, content string) {
	ginkgo.GinkgoHelper()
	path := filepath.Join(root, filepath.FromSlash(relativePath))
	gomega.Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(gomega.Succeed())
	gomega.Expect(os.WriteFile(path, []byte(content), 0o644)).To(gomega.Succeed())
}
