package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

type stdioResilienceCallOutcome struct {
	tag    string
	result ToolCallResult
	err    error
}

var _ = ginkgo.Describe("MCP stdio peer recovery", ginkgo.Label("development-scenario", "e2e", "integration", "smoke", "race-contract"), func() {
	ginkgo.It("isolates concurrent peer failures and restarts without stale response crossover", func(ctx ginkgo.SpecContext) {
		callContext, cancel := context.WithTimeout(ctx, 10*time.Second)
		ginkgo.DeferCleanup(cancel)

		failedPeerClient := startStdioResilienceClient(callContext, "fail-concurrent", "first")

		outcomes := make(chan stdioResilienceCallOutcome, 3)
		call := func(tag string) {
			result, err := failedPeerClient.CallTool(callContext, "identity", json.RawMessage(fmt.Sprintf(`{"tag":%q}`, tag)))
			outcomes <- stdioResilienceCallOutcome{tag: tag, result: result, err: err}
		}

		go call("valid")
		gomega.Eventually(callContext, func() int64 { return failedPeerClient.nextID.Load() }).Should(gomega.Equal(int64(2)))
		go call("scoped-error")
		gomega.Eventually(callContext, func() int64 { return failedPeerClient.nextID.Load() }).Should(gomega.Equal(int64(3)))
		go call("malformed")
		gomega.Eventually(callContext, func() int64 { return failedPeerClient.nextID.Load() }).Should(gomega.Equal(int64(4)))

		byTag := make(map[string]stdioResilienceCallOutcome, 3)
		for range 3 {
			var outcome stdioResilienceCallOutcome
			gomega.Eventually(callContext, outcomes).Should(gomega.Receive(&outcome))
			byTag[outcome.tag] = outcome
		}
		gomega.Expect(byTag).To(gomega.HaveLen(3))

		valid := byTag["valid"]
		gomega.Expect(valid.err).NotTo(gomega.HaveOccurred())
		gomega.Expect(valid.result.Content).To(gomega.ConsistOf(gomega.MatchJSON(
			`{"type":"text","text":"generation=first request_id=2 tag=valid"}`,
		)))

		scopedError := byTag["scoped-error"]
		gomega.Expect(scopedError.result).To(gomega.Equal(ToolCallResult{}))
		gomega.Expect(scopedError.err).To(gomega.MatchError(
			"tools/call: server error -32042: generation=first request_id=3 tag=scoped-error",
		))

		malformed := byTag["malformed"]
		gomega.Expect(malformed.result).To(gomega.Equal(ToolCallResult{}))
		gomega.Expect(malformed.err).To(gomega.MatchError(gomega.And(
			gomega.ContainSubstring("tools/call"),
			gomega.ContainSubstring("response reader stopped"),
			gomega.ContainSubstring("EOF"),
		)))
		gomega.Expect(errors.Is(malformed.err, io.EOF)).To(gomega.BeTrue())

		gomega.Eventually(callContext, failedPeerClient.readerDone).Should(gomega.BeClosed())
		failedPeerClient.pendingMu.Lock()
		failedPeerPending := len(failedPeerClient.pending)
		failedPeerClient.pendingMu.Unlock()
		gomega.Expect(failedPeerPending).To(gomega.BeZero())

		failedPeerClose := make(chan error, 1)
		go func() { failedPeerClose <- failedPeerClient.Close() }()
		var failedPeerCloseErr error
		gomega.Eventually(callContext, failedPeerClose).Should(gomega.Receive(&failedPeerCloseErr))
		gomega.Expect(failedPeerCloseErr).NotTo(gomega.HaveOccurred())

		freshPeerClient := startStdioResilienceClient(callContext, "healthy", "second")
		gomega.Expect(freshPeerClient.nextID.Load()).To(gomega.Equal(int64(1)))

		fresh, err := freshPeerClient.CallTool(callContext, "identity", json.RawMessage(`{"tag":"fresh"}`))
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(freshPeerClient.nextID.Load()).To(gomega.Equal(int64(2)))
		gomega.Expect(fresh.Content).To(gomega.ConsistOf(gomega.MatchJSON(
			`{"type":"text","text":"generation=second request_id=2 tag=fresh"}`,
		)))
		gomega.Expect(fresh.Content).NotTo(gomega.ContainElement(gomega.MatchJSON(
			`{"type":"text","text":"stale generation=first request_id=2 tag=valid"}`,
		)))

		freshPeerClose := make(chan error, 1)
		go func() { freshPeerClose <- freshPeerClient.Close() }()
		var freshPeerCloseErr error
		gomega.Eventually(callContext, freshPeerClose).Should(gomega.Receive(&freshPeerCloseErr))
		gomega.Expect(freshPeerCloseErr).NotTo(gomega.HaveOccurred())
	}, ginkgo.SpecTimeout(20*time.Second))
})

func startStdioResilienceClient(ctx context.Context, mode string, generation string) *Client {
	ginkgo.GinkgoHelper()

	transport, err := StartStdio(ctx, os.Args[0], []string{"-test.run=TestMCPStdioHelperProcess", "--"}, []string{
		"GO_WANT_MCP_STDIO_HELPER=1",
		"IDIOM_MCP_STDIO_HELPER_SCENARIO=resilience",
		"IDIOM_MCP_PEER_MODE=" + mode,
		"IDIOM_MCP_PEER_GENERATION=" + generation,
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	ginkgo.DeferCleanup(transport.Close)

	client := NewClient(transport)
	gomega.Expect(client.Start(ctx)).To(gomega.Succeed())
	gomega.Expect(client.Initialize(ctx, "idiom-stdio-resilience-e2e")).To(gomega.Succeed())

	return client
}

func serveStdioResiliencePeer(mode string, generation string) error {
	scanner := bufio.NewScanner(os.Stdin)
	concurrentCalls := make(map[string]rawRequest, 3)

	for scanner.Scan() {
		var request rawRequest
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			return fmt.Errorf("decode request: %w", err)
		}

		switch request.Method {
		case "initialize":
			if err := writeStdioResilienceResult(request.ID, map[string]any{
				"protocolVersion": ProtocolVersion,
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "stdio-resilience-peer", "version": generation},
			}); err != nil {
				return err
			}
		case "notifications/initialized":
			continue
		case "tools/call":
			tag, err := stdioResilienceCallTag(request.Params)
			if err != nil {
				return err
			}
			switch mode {
			case "healthy":
				return writeStdioResilienceToolResult(request.ID, generation, tag, false)
			case "fail-concurrent":
			default:
				return fmt.Errorf("unsupported peer mode %q", mode)
			}

			concurrentCalls[tag] = request
			if len(concurrentCalls) != 3 {
				continue
			}
			if concurrentCalls["valid"].ID != 2 || concurrentCalls["scoped-error"].ID != 3 || concurrentCalls["malformed"].ID != 4 {
				return fmt.Errorf("unexpected request identities: valid=%d scoped-error=%d malformed=%d",
					concurrentCalls["valid"].ID,
					concurrentCalls["scoped-error"].ID,
					concurrentCalls["malformed"].ID,
				)
			}

			if _, err := fmt.Fprintf(os.Stdout, `{"jsonrpc":"2.0","id":%d,"result":`+"\n", concurrentCalls["malformed"].ID); err != nil {
				return fmt.Errorf("write malformed response: %w", err)
			}
			if err := writeStdioResilienceError(
				concurrentCalls["scoped-error"].ID,
				-32042,
				fmt.Sprintf(
					"generation=%s request_id=%d tag=scoped-error",
					generation,
					concurrentCalls["scoped-error"].ID,
				),
			); err != nil {
				return err
			}
			if err := writeStdioResilienceToolResult(concurrentCalls["valid"].ID, generation, "valid", false); err != nil {
				return err
			}

			// The duplicate deliberately reuses the same ID as the next peer's
			// first tool call. It must be ignored after its original call completes
			// and cannot cross the fresh transport/client boundary.
			return writeStdioResilienceToolResult(concurrentCalls["valid"].ID, generation, "valid", true)
		default:
			if err := writeStdioResilienceError(request.ID, -32601, "method not found"); err != nil {
				return err
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan requests: %w", err)
	}

	return nil
}

func stdioResilienceCallTag(params json.RawMessage) (string, error) {
	var call struct {
		Name      string `json:"name"`
		Arguments struct {
			Tag string `json:"tag"`
		} `json:"arguments"`
	}
	if err := json.Unmarshal(params, &call); err != nil {
		return "", fmt.Errorf("decode tool call: %w", err)
	}
	if call.Name != "identity" || call.Arguments.Tag == "" {
		return "", fmt.Errorf("unexpected tool call name=%q tag=%q", call.Name, call.Arguments.Tag)
	}

	return call.Arguments.Tag, nil
}

func writeStdioResilienceToolResult(id int64, generation string, tag string, stale bool) error {
	prefix := ""
	if stale {
		prefix = "stale "
	}

	return writeStdioResilienceResult(id, map[string]any{
		"content": []map[string]any{{
			"type": "text",
			"text": fmt.Sprintf("%sgeneration=%s request_id=%d tag=%s", prefix, generation, id, tag),
		}},
	})
}

func writeStdioResilienceResult(id int64, result any) error {
	resultJSON, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("encode result: %w", err)
	}

	return writeStdioResilienceResponse(rawResponse{JSONRPC: "2.0", ID: id, Result: resultJSON})
}

func writeStdioResilienceError(id int64, code int, message string) error {
	return writeStdioResilienceResponse(rawResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &rawError{Code: code, Message: message},
	})
}

func writeStdioResilienceResponse(response rawResponse) error {
	encoded, err := json.Marshal(response)
	if err != nil {
		return fmt.Errorf("encode response: %w", err)
	}
	if _, err := fmt.Fprintln(os.Stdout, string(encoded)); err != nil {
		return fmt.Errorf("write response: %w", err)
	}

	return nil
}
