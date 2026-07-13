package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	"idiom/internal/platform/config"
)

// test-double: scripted-protocol-peer
type developmentMCPPeer struct {
	server             *httptest.Server
	failToolList       bool
	blockedCall        chan struct{}
	concurrentCalls    chan struct{}
	releaseConcurrent  chan struct{}
	blockedOnce        sync.Once
	concurrentOnce     sync.Once
	concurrentInFlight atomic.Int32
	authorizationSeen  atomic.Bool
}

func newDevelopmentMCPPeer(failToolList bool) *developmentMCPPeer {
	peer := &developmentMCPPeer{
		failToolList:      failToolList,
		blockedCall:       make(chan struct{}),
		concurrentCalls:   make(chan struct{}),
		releaseConcurrent: make(chan struct{}),
	}
	peer.server = httptest.NewServer(http.HandlerFunc(peer.handle))

	return peer
}

func (p *developmentMCPPeer) handle(w http.ResponseWriter, request *http.Request) {
	if request.Header.Get("Authorization") == "Bearer development-token" {
		p.authorizationSeen.Store(true)
	}

	var rpc struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params"`
	}
	if err := json.NewDecoder(request.Body).Decode(&rpc); err != nil {
		writeDevelopmentMCPError(w, rpc.ID, -32700, "parse error")

		return
	}

	switch rpc.Method {
	case "initialize":
		writeDevelopmentMCPResult(w, rpc.ID, map[string]any{
			"protocolVersion": ProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "development-peer", "version": "1"},
		})
	case "notifications/initialized":
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","result":{}}`))
	case "tools/list":
		if p.failToolList {
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = fmt.Fprintf(w, "catalog authentication failed for %s", request.Header.Get("Authorization"))

			return
		}

		writeDevelopmentMCPResult(w, rpc.ID, map[string]any{"tools": []map[string]any{{
			"name":        "workspace_lint",
			"description": "lint one development target",
			"inputSchema": map[string]any{
				"type":       "object",
				"properties": map[string]any{"tag": map[string]any{"type": "string"}, "mode": map[string]any{"type": "string"}},
			},
		}}})
	case "tools/call":
		var params struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(rpc.Params, &params); err != nil || params.Name != "workspace_lint" {
			writeDevelopmentMCPError(w, rpc.ID, -32602, "invalid tool call")

			return
		}

		switch fmt.Sprint(params.Arguments["mode"]) {
		case "concurrent":
			inFlight := p.concurrentInFlight.Add(1)
			defer p.concurrentInFlight.Add(-1)
			if inFlight >= 2 {
				p.concurrentOnce.Do(func() { close(p.concurrentCalls) })
			}
			select {
			case <-p.releaseConcurrent:
			case <-request.Context().Done():
				return
			}
		case "block":
			p.blockedOnce.Do(func() { close(p.blockedCall) })
			<-request.Context().Done()

			return
		case "malformed":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"jsonrpc":`))

			return
		}

		writeDevelopmentMCPResult(w, rpc.ID, map[string]any{
			"content": []map[string]any{{"type": "text", "text": "checked:" + fmt.Sprint(params.Arguments["tag"])}},
		})
	default:
		writeDevelopmentMCPError(w, rpc.ID, -32601, "method not found")
	}
}

func writeDevelopmentMCPResult(w http.ResponseWriter, id json.RawMessage, result any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func writeDevelopmentMCPError(w http.ResponseWriter, id json.RawMessage, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error":   map[string]any{"code": code, "message": message},
	})
}

func developmentMCPText(result ToolCallResult) string {
	for _, content := range result.Content {
		var text struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(content, &text) == nil && text.Type == "text" {
			return text.Text
		}
	}

	return ""
}

var _ = ginkgo.Describe("MCP dependency resilience", ginkgo.Label("development-scenario", "e2e", "integration", "smoke", "race-contract"), func() {
	ginkgo.It("keeps healthy tools available across sibling failure, concurrent calls, cancellation, malformed output, and retry", func(ctx ginkgo.SpecContext) {
		const (
			brokenAuthorization  = "Bearer broken-development-token"
			healthyAuthorization = "Bearer development-token"
		)

		broken := newDevelopmentMCPPeer(true)
		healthy := newDevelopmentMCPPeer(false)
		ginkgo.DeferCleanup(broken.server.Close)
		ginkgo.DeferCleanup(healthy.server.Close)

		registry, failures := SetupRegistry(ctx, []config.MCPServer{
			{
				Name:           "broken",
				URL:            broken.server.URL,
				Headers:        map[string]string{"Authorization": brokenAuthorization},
				TimeoutSeconds: 5,
			},
			{
				Name:           "healthy",
				URL:            healthy.server.URL,
				Headers:        map[string]string{"Authorization": healthyAuthorization},
				TimeoutSeconds: 5,
			},
		}, true)
		gomega.Expect(registry).NotTo(gomega.BeNil())
		ginkgo.DeferCleanup(registry.Close)
		gomega.Expect(failures).To(gomega.HaveLen(1))
		gomega.Expect(failures[0]).To(gomega.And(
			gomega.ContainSubstring(`mcp "broken"`),
			gomega.ContainSubstring("catalog authentication failed"),
			gomega.ContainSubstring(redactedHeaderValue),
		))
		gomega.Expect(strings.Join(failures, "\n")).NotTo(gomega.Or(
			gomega.ContainSubstring(brokenAuthorization),
			gomega.ContainSubstring(healthyAuthorization),
			gomega.ContainSubstring("broken-development-token"),
			gomega.ContainSubstring("development-token"),
		))
		gomega.Expect(registry.Has("mcp:healthy/workspace_lint")).To(gomega.BeTrue())
		gomega.Expect(registry.Has("mcp:broken/workspace_lint")).To(gomega.BeFalse())
		gomega.Expect(healthy.authorizationSeen.Load()).To(gomega.BeTrue())

		type callOutcome struct {
			tag    string
			result ToolCallResult
			err    error
		}
		outcomes := make(chan callOutcome, 12)
		for index := 0; index < 12; index++ {
			tag := fmt.Sprintf("target-%02d", index)
			go func() {
				result, err := registry.Call(ctx, "mcp:healthy/workspace_lint", json.RawMessage(fmt.Sprintf(`{"tag":%q}`, tag)))
				outcomes <- callOutcome{tag: tag, result: result, err: err}
			}()
		}
		for index := 0; index < 12; index++ {
			var outcome callOutcome
			gomega.Eventually(ctx, outcomes).Should(gomega.Receive(&outcome))
			gomega.Expect(outcome.err).NotTo(gomega.HaveOccurred())
			gomega.Expect(developmentMCPText(outcome.result)).To(gomega.Equal("checked:" + outcome.tag))
		}

		concurrentOutcomes := make(chan callOutcome, 2)
		for _, tag := range []string{"concurrent-a", "concurrent-b"} {
			go func() {
				result, err := registry.Call(ctx, "mcp:healthy/workspace_lint", json.RawMessage(fmt.Sprintf(`{"mode":"concurrent","tag":%q}`, tag)))
				concurrentOutcomes <- callOutcome{tag: tag, result: result, err: err}
			}()
		}
		gomega.Eventually(ctx, healthy.concurrentCalls).Should(gomega.BeClosed())
		close(healthy.releaseConcurrent)
		for index := 0; index < 2; index++ {
			var outcome callOutcome
			gomega.Eventually(ctx, concurrentOutcomes).Should(gomega.Receive(&outcome))
			gomega.Expect(outcome.err).NotTo(gomega.HaveOccurred())
			gomega.Expect(developmentMCPText(outcome.result)).To(gomega.Equal("checked:" + outcome.tag))
		}

		blockedContext, cancelBlocked := context.WithCancel(ctx)
		blockedDone := make(chan error, 1)
		go func() {
			_, err := registry.Call(blockedContext, "mcp:healthy/workspace_lint", json.RawMessage(`{"mode":"block"}`))
			blockedDone <- err
		}()
		gomega.Eventually(ctx, healthy.blockedCall).Should(gomega.BeClosed())
		cancelBlocked()
		var blockedErr error
		gomega.Eventually(ctx, blockedDone).Should(gomega.Receive(&blockedErr))
		gomega.Expect(errors.Is(blockedErr, context.Canceled)).To(gomega.BeTrue())

		_, malformedErr := registry.Call(ctx, "mcp:healthy/workspace_lint", json.RawMessage(`{"mode":"malformed"}`))
		gomega.Expect(malformedErr).To(gomega.MatchError(gomega.ContainSubstring("malformed MCP JSON response")))
		gomega.Expect(errors.Is(malformedErr, context.Canceled)).To(gomega.BeFalse())

		retry, err := registry.Call(ctx, "mcp:healthy/workspace_lint", json.RawMessage(`{"tag":"retry-after-malformed"}`))
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(developmentMCPText(retry)).To(gomega.Equal("checked:retry-after-malformed"))
	}, ginkgo.SpecTimeout(30*time.Second))
})
