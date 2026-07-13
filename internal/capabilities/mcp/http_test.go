package mcp

//go:generate go tool mockgen -destination=mock_http_round_tripper_test.go -package=mcp -mock_names=RoundTripper=MockHTTPRoundTripper -typed net/http RoundTripper

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
)

var _ = ginkgo.Describe("MCP HTTP transport", ginkgo.Label("integration"), func() {
	ginkgo.It("decodes an SSE response frame", func(ctx ginkgo.SpecContext) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, ": comment line\nevent: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":\"hello\"}\n\n")
		}))
		ginkgo.DeferCleanup(srv.Close)
		transport := startPrivateHTTPTransport(srv.URL, nil)
		callCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		ginkgo.DeferCleanup(cancel)
		gomega.Expect(transport.Send(callCtx, []byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))).To(gomega.Succeed())
		frame, err := transport.Receive(callCtx)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(string(frame.Payload)).To(gomega.Equal(`{"jsonrpc":"2.0","id":1,"result":"hello"}`))
	})

	ginkgo.It("decodes multiple SSE data frames from one response body", func(ctx ginkgo.SpecContext) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			for index := 1; index <= 3; index++ {
				fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\",\"params\":{\"index\":%d}}\n", index)
			}
		}))
		ginkgo.DeferCleanup(srv.Close)
		transport := startPrivateHTTPTransport(srv.URL, nil)
		callCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		ginkgo.DeferCleanup(cancel)
		gomega.Expect(transport.Send(callCtx, []byte(`{}`))).To(gomega.Succeed())
		for i := 1; i <= 3; i++ {
			frame, err := transport.Receive(callCtx)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(string(frame.Payload)).To(gomega.Equal(fmt.Sprintf(`{"jsonrpc":"2.0","method":"notifications/progress","params":{"index":%d}}`, i)))
		}
	})

	ginkgo.It("decodes an application/json response", func(ctx ginkgo.SpecContext) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":42}`)
		}))
		ginkgo.DeferCleanup(srv.Close)
		transport := startPrivateHTTPTransport(srv.URL, nil)
		callCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		ginkgo.DeferCleanup(cancel)
		gomega.Expect(transport.Send(callCtx, []byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))).To(gomega.Succeed())
		frame, err := transport.Receive(callCtx)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(string(frame.Payload)).To(gomega.ContainSubstring(`"result":42`))
	})

	ginkgo.It("correlates a malformed application/json response to its request", func(ctx ginkgo.SpecContext) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"jsonrpc":`)
		}))
		ginkgo.DeferCleanup(srv.Close)
		transport := startPrivateHTTPTransport(srv.URL, nil)
		gomega.Expect(transport.Send(ctx, []byte(`{"jsonrpc":"2.0","id":41,"method":"ping"}`))).To(gomega.Succeed())
		message, err := transport.Receive(ctx)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(message.RequestID).To(gomega.Equal(int64(41)))
		gomega.Expect(message.RequestError).To(gomega.MatchError(gomega.ContainSubstring("malformed MCP JSON response")))
		gomega.Expect(message.Payload).To(gomega.BeEmpty())
	})

	ginkgo.It("keeps an application/json response to a notification from claiming a pending call", ginkgo.Label("race-contract"), func(ctx ginkgo.SpecContext) {
		requestStarted := make(chan struct{})
		releaseResponse := make(chan struct{})
		var releaseOnce sync.Once
		release := func() {
			releaseOnce.Do(func() { close(releaseResponse) })
		}
		ginkgo.DeferCleanup(release)

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
			var rpc rawRequest
			if err := json.NewDecoder(request.Body).Decode(&rpc); err != nil {
				http.Error(w, "invalid JSON-RPC request", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")

			switch rpc.Method {
			case "owned/request":
				close(requestStarted)
				select {
				case <-releaseResponse:
				case <-request.Context().Done():
					return
				}
				fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":"owned"}`, rpc.ID)
			case "notifications/test":
				_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":"foreign"}`)
			default:
				http.Error(w, "unexpected method", http.StatusBadRequest)
			}
		}))
		ginkgo.DeferCleanup(srv.Close)

		transport := startPrivateHTTPTransport(srv.URL, nil)
		client := NewClient(transport)
		ginkgo.DeferCleanup(client.Close)
		gomega.Expect(client.Start(ctx)).To(gomega.Succeed())

		type callResult struct {
			result json.RawMessage
			err    error
		}
		callDone := make(chan callResult, 1)
		go func() {
			result, err := client.call(ctx, "owned/request", nil)
			callDone <- callResult{result: result, err: err}
		}()

		gomega.Eventually(requestStarted).Should(gomega.BeClosed())
		gomega.Expect(transport.Send(ctx, []byte(`{"jsonrpc":"2.0","method":"notifications/test"}`))).To(gomega.Succeed())
		release()

		var completed callResult
		gomega.Eventually(callDone).Should(gomega.Receive(&completed))
		gomega.Expect(completed.err).NotTo(gomega.HaveOccurred())
		gomega.Expect(string(completed.result)).To(gomega.Equal(`"owned"`))
	})

	ginkgo.It("publishes a valid application/json server notification returned to a notification send", func(ctx ginkgo.SpecContext) {
		const notification = `{"jsonrpc":"2.0","method":"notifications/progress","params":{"stage":"ready"}}`
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, notification)
		}))
		ginkgo.DeferCleanup(srv.Close)

		transport := startPrivateHTTPTransport(srv.URL, nil)
		gomega.Expect(transport.Send(ctx, []byte(`{"jsonrpc":"2.0","method":"notifications/test"}`))).To(gomega.Succeed())
		message, err := transport.Receive(ctx)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(string(message.Payload)).To(gomega.Equal(notification))
	})

	ginkgo.It("rejects an application/json server request returned to a notification send", func(ctx ginkgo.SpecContext) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":"server-1","method":"sampling/createMessage","params":{}}`)
		}))
		ginkgo.DeferCleanup(srv.Close)

		transport := startPrivateHTTPTransport(srv.URL, nil)
		err := transport.Send(ctx, []byte(`{"jsonrpc":"2.0","method":"notifications/test"}`))
		gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring(`unsupported server request "sampling/createMessage"`)))
		gomega.Expect(transport.inbox).To(gomega.HaveLen(0))
	})

	ginkgo.It("sends the MCP accept, content-type, and configured headers", func(ctx ginkgo.SpecContext) {
		got := make(chan http.Header, 1)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got <- r.Header.Clone()
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{}}`)
		}))
		ginkgo.DeferCleanup(srv.Close)
		transport := startPrivateHTTPTransport(srv.URL, map[string]string{"X-Api-Key": "secret"})
		callCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		ginkgo.DeferCleanup(cancel)
		gomega.Expect(transport.Send(callCtx, []byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))).To(gomega.Succeed())
		_, err := transport.Receive(callCtx)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		var headers http.Header
		gomega.Expect(got).To(gomega.Receive(&headers))
		gomega.Expect(headers.Get("Accept")).To(gomega.Equal("application/json, text/event-stream"))
		gomega.Expect(headers.Get("Content-Type")).To(gomega.Equal("application/json"))
		gomega.Expect(headers.Get("X-Api-Key")).To(gomega.Equal("secret"))
	})

	ginkgo.It("returns an HTTP status error", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, "unauthorized")
		}))
		ginkgo.DeferCleanup(srv.Close)
		transport := startPrivateHTTPTransport(srv.URL, nil)
		gomega.Expect(transport.Send(context.Background(), []byte(`{}`))).To(gomega.MatchError(gomega.ContainSubstring("401")))
	})

	ginkgo.It("redacts the endpoint query from HTTP status errors", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) }))
		ginkgo.DeferCleanup(srv.Close)
		transport := startPrivateHTTPTransport(srv.URL+"?secret=xyz", nil)
		err := transport.Send(context.Background(), []byte(`{}`))
		gomega.Expect(err).To(gomega.HaveOccurred())
		gomega.Expect(err.Error()).NotTo(gomega.ContainSubstring("secret=xyz"))
	})

	ginkgo.It("redacts nested URL credentials while preserving the transport error cause", func() {
		sentinel := errors.New("dial sentinel")
		transport, err := StartHTTP("https://user:password@1.1.1.1/mcp?secret=xyz", nil, 5*time.Second, false)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		ginkgo.DeferCleanup(transport.Close)
		roundTripper := NewMockHTTPRoundTripper(gomock.NewController(ginkgo.GinkgoT()))
		roundTripper.EXPECT().RoundTrip(gomock.Any()).Return(
			nil,
			&url.Error{Op: "dial", URL: "https://nested-user:nested-password@example.com/mcp?nested-secret=yes", Err: sentinel},
		).Times(1)
		transport.client.Transport = roundTripper
		err = transport.Send(context.Background(), []byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
		gomega.Expect(errors.Is(err, sentinel)).To(gomega.BeTrue())
		gomega.Expect(err.Error()).NotTo(gomega.Or(
			gomega.ContainSubstring("user"),
			gomega.ContainSubstring("password"),
			gomega.ContainSubstring("secret=xyz"),
			gomega.ContainSubstring("nested-secret=yes"),
		))
	})

	ginkgo.It("redacts configured header values from HTTP client errors while preserving cause matching", func() {
		const authorization = "Bearer round-trip-error-token"
		sentinel := errors.New("round trip sentinel")
		transport, err := StartHTTP("https://1.1.1.1/mcp", map[string]string{"Authorization": authorization}, 5*time.Second, false)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		ginkgo.DeferCleanup(transport.Close)
		roundTripper := NewMockHTTPRoundTripper(gomock.NewController(ginkgo.GinkgoT()))
		roundTripper.EXPECT().RoundTrip(gomock.Any()).DoAndReturn(func(request *http.Request) (*http.Response, error) {
			return nil, fmt.Errorf("authorization %s rejected: %w", request.Header.Get("Authorization"), sentinel)
		}).Times(1)
		transport.client.Transport = roundTripper

		err = transport.Send(context.Background(), []byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
		gomega.Expect(errors.Is(err, sentinel)).To(gomega.BeTrue())
		gomega.Expect(err.Error()).To(gomega.ContainSubstring(redactedHeaderValue))
		gomega.Expect(err.Error()).NotTo(gomega.Or(
			gomega.ContainSubstring(authorization),
			gomega.ContainSubstring("round-trip-error-token"),
		))
	})

	ginkgo.It("redacts configured header values from transport and JSON-RPC error surfaces", func(ctx ginkgo.SpecContext) {
		const authorization = "Bearer http-error-surface-token"

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":1,"error":{"code":-32001,"message":"peer rejected %s","data":{"credential":"%s"}}}`, request.Header.Get("Authorization"), request.Header.Get("Authorization"))
		}))
		ginkgo.DeferCleanup(srv.Close)
		transport := startPrivateHTTPTransport(srv.URL, map[string]string{"Authorization": authorization})
		client := NewClient(transport)
		ginkgo.DeferCleanup(client.Close)
		gomega.Expect(client.Start(ctx)).To(gomega.Succeed())

		_, err := client.call(ctx, "test/redacted-error", nil)
		gomega.Expect(err).To(gomega.MatchError(gomega.And(
			gomega.ContainSubstring("peer rejected"),
			gomega.ContainSubstring(redactedHeaderValue),
		)))
		gomega.Expect(err.Error()).NotTo(gomega.Or(
			gomega.ContainSubstring(authorization),
			gomega.ContainSubstring("http-error-surface-token"),
		))
	})

	ginkgo.It("blocks loopback endpoints under the public network policy", func() {
		_, err := StartHTTP("http://127.0.0.1:9/", nil, 5*time.Second, false)
		gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("loopback")))
	})

	ginkgo.It("blocks a redirect from a public endpoint to a private target", func() {
		transport, err := StartHTTP("https://1.1.1.1/mcp", map[string]string{"X-Api-Key": "secret"}, 5*time.Second, false)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		ginkgo.DeferCleanup(transport.Close)
		original, err := http.NewRequest(http.MethodPost, "https://1.1.1.1/mcp", nil)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		redirect, err := http.NewRequest(http.MethodPost, "http://127.0.0.1/private", nil)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(transport.client.CheckRedirect(redirect, []*http.Request{original})).To(gomega.MatchError(gomega.ContainSubstring("loopback")))
	})

	ginkgo.It("strips endpoint credentials from a cross-origin public redirect", func() {
		transport, err := StartHTTP("https://1.1.1.1/mcp?api_key=query-secret", map[string]string{"X-Api-Key": "header-secret"}, 5*time.Second, false)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		ginkgo.DeferCleanup(transport.Close)
		original, err := http.NewRequest(http.MethodPost, "https://1.1.1.1/mcp?api_key=query-secret", nil)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		redirect, err := http.NewRequest(http.MethodPost, "https://8.8.8.8/mcp", nil)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		redirect.Header.Set("X-Api-Key", "header-secret")
		redirect.Header.Set("Authorization", "Bearer secret")
		redirect.Header.Set("Referer", original.URL.String())
		gomega.Expect(transport.client.CheckRedirect(redirect, []*http.Request{original})).To(gomega.Succeed())
		gomega.Expect(redirect.Header.Get("X-Api-Key")).To(gomega.BeEmpty())
		gomega.Expect(redirect.Header.Get("Authorization")).To(gomega.BeEmpty())
		gomega.Expect(redirect.Header.Get("Referer")).To(gomega.BeEmpty())
	})

	ginkgo.It("disables environment proxies and HTTPS dial bypasses for public endpoints", func() {
		transport, err := StartHTTP("https://1.1.1.1/mcp", nil, 5*time.Second, false)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		ginkgo.DeferCleanup(transport.Close)
		roundTripper, ok := transport.client.Transport.(*http.Transport)
		gomega.Expect(ok).To(gomega.BeTrue())
		gomega.Expect(roundTripper.Proxy).To(gomega.BeNil())
		dialTLS := reflect.ValueOf(roundTripper).Elem().FieldByName("DialTLS")
		gomega.Expect(dialTLS.IsNil()).To(gomega.BeTrue())
		gomega.Expect(roundTripper.DialTLSContext).To(gomega.BeNil())
	})

	ginkgo.It("owns its transport instead of depending on mutable http.DefaultTransport", func() {
		original := http.DefaultTransport
		http.DefaultTransport = NewMockHTTPRoundTripper(gomock.NewController(ginkgo.GinkgoT()))
		ginkgo.DeferCleanup(func() { http.DefaultTransport = original })
		transport, err := StartHTTP("https://1.1.1.1/mcp", nil, 5*time.Second, false)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		ginkgo.DeferCleanup(transport.Close)
		_, ok := transport.client.Transport.(*http.Transport)
		gomega.Expect(ok).To(gomega.BeTrue())
	})

	ginkgo.It("permits a loopback endpoint when private networking is authorized", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"ok":true}`)
		}))
		ginkgo.DeferCleanup(srv.Close)
		transport := startPrivateHTTPTransport(srv.URL, nil)
		gomega.Expect(transport).NotTo(gomega.BeNil())
	})

	ginkgo.It("returns a request-scoped EOF when an expected event stream has no response", func(ctx ginkgo.SpecContext) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, ": stream ended without a response\n")
		}))
		ginkgo.DeferCleanup(srv.Close)
		transport := startPrivateHTTPTransport(srv.URL, nil)
		gomega.Expect(transport.Send(context.Background(), []byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))).To(gomega.Succeed())
		callCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		ginkgo.DeferCleanup(cancel)
		message, err := transport.Receive(callCtx)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(errors.Is(message.RequestError, io.ErrUnexpectedEOF)).To(gomega.BeTrue())
	})

	ginkgo.It("returns a request-scoped error for an oversized event stream", func(ctx ginkgo.SpecContext) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: "+strings.Repeat("x", maxResponseFrame+1)+"\n")
		}))
		ginkgo.DeferCleanup(srv.Close)
		transport := startPrivateHTTPTransport(srv.URL, nil)
		gomega.Expect(transport.Send(context.Background(), []byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))).To(gomega.Succeed())
		callCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		ginkgo.DeferCleanup(cancel)
		message, err := transport.Receive(callCtx)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(message.RequestError).To(gomega.MatchError(gomega.ContainSubstring("event stream")))
	})

	ginkgo.It("keeps an event-stream failure scoped to its request", func() {
		var requests atomic.Int64
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			if requests.Add(1) == 1 {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, ": no response\n")
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":2,"result":{"ok":true}}`)
		}))
		ginkgo.DeferCleanup(srv.Close)
		transport := startPrivateHTTPTransport(srv.URL, nil)
		client := NewClient(transport)
		ginkgo.DeferCleanup(client.Close)
		gomega.Expect(client.Start(context.Background())).To(gomega.Succeed())
		_, err := client.call(context.Background(), "test/stream-failure", nil)
		gomega.Expect(errors.Is(err, io.ErrUnexpectedEOF)).To(gomega.BeTrue())
		result, err := client.call(context.Background(), "test/success", nil)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(string(result)).To(gomega.ContainSubstring(`"ok":true`))
	})

	ginkgo.It("enforces the JSON response frame limit", func() {
		exact := strings.Repeat("x", maxResponseFrame)
		frame, err := readJSONResponseFrame(strings.NewReader(exact))
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(frame).To(gomega.HaveLen(maxResponseFrame))
		_, err = readJSONResponseFrame(strings.NewReader(exact + "x"))
		gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("exceeds")))
	})

	ginkgo.It("cancels a request that is waiting for response headers when closed", func() {
		requestStarted := make(chan struct{})
		releaseHandler := make(chan struct{})
		srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, req *http.Request) {
			close(requestStarted)
			select {
			case <-req.Context().Done():
			case <-releaseHandler:
			}
		}))
		ginkgo.DeferCleanup(srv.Close)
		ginkgo.DeferCleanup(func() { close(releaseHandler) })
		transport, err := StartHTTP(srv.URL, nil, 30*time.Second, true)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		sendDone := make(chan error, 1)
		go func() {
			sendDone <- transport.Send(context.Background(), []byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
		}()
		gomega.Eventually(requestStarted).WithTimeout(2 * time.Second).Should(gomega.BeClosed())
		started := time.Now()
		gomega.Expect(transport.Close()).To(gomega.Succeed())
		gomega.Expect(time.Since(started)).To(gomega.BeNumerically("<=", time.Second))
		var sendErr error
		gomega.Eventually(sendDone).WithTimeout(time.Second).Should(gomega.Receive(&sendErr))
		gomega.Expect(errors.Is(sendErr, io.ErrClosedPipe)).To(gomega.BeTrue())
	})

	ginkgo.It("honors Receive context cancellation", func(ctx ginkgo.SpecContext) {
		transport, err := StartHTTP("https://1.1.1.1/", nil, 5*time.Second, false)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		ginkgo.DeferCleanup(transport.Close)
		callCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		ginkgo.DeferCleanup(cancel)
		_, err = transport.Receive(callCtx)
		gomega.Expect(err).To(gomega.MatchError(context.DeadlineExceeded))
	})

	ginkgo.It("drains buffered messages and returns EOF after Close", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			flusher, _ := w.(http.Flusher)
			for i := 0; i < 5; i++ {
				fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\",\"params\":{\"index\":%d}}\n", i)
				if flusher != nil {
					flusher.Flush()
				}
				time.Sleep(50 * time.Millisecond)
			}
		}))
		ginkgo.DeferCleanup(srv.Close)
		transport := startPrivateHTTPTransport(srv.URL, nil)
		gomega.Expect(transport.Send(context.Background(), []byte(`{}`))).To(gomega.Succeed())
		_, err := transport.Receive(context.Background())
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		closeStart := time.Now()
		gomega.Expect(transport.Close()).To(gomega.Succeed())
		gomega.Expect(time.Since(closeStart)).To(gomega.BeNumerically("<=", time.Second))
		_, err = transport.Receive(context.Background())
		gomega.Expect(err).To(gomega.MatchError(io.EOF))
	})

	ginkgo.It("keeps crossed SSE responses scoped to the HTTP request that owns them", ginkgo.Label("development-scenario", "e2e", "race-contract"), func(ctx ginkgo.SpecContext) {
		requestsReady := make(chan struct{})
		foreignFramesSent := make(chan struct{})
		var requestCount atomic.Int32
		var foreignFrameCount atomic.Int32

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
			var rpc rawRequest
			if err := json.NewDecoder(request.Body).Decode(&rpc); err != nil {
				http.Error(w, "invalid request", http.StatusBadRequest)
				return
			}
			if requestCount.Add(1) == 2 {
				close(requestsReady)
			}
			select {
			case <-requestsReady:
			case <-request.Context().Done():
				return
			}

			w.Header().Set("Content-Type", "text/event-stream")
			flusher, ok := w.(http.Flusher)
			if !ok {
				http.Error(w, "streaming unavailable", http.StatusInternalServerError)
				return
			}
			fmt.Fprintln(w, `data: {"jsonrpc":"2.0","method":"notifications/progress","params":{"stage":"foreign-first"}}`)
			foreignID := int64(1)
			if rpc.ID == 1 {
				foreignID = 2
			}
			fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"id\":%d,\"result\":{\"source\":\"foreign\",\"method\":%q}}\n", foreignID, rpc.Method)
			flusher.Flush()
			if foreignFrameCount.Add(1) == 2 {
				close(foreignFramesSent)
			}
			select {
			case <-foreignFramesSent:
			case <-request.Context().Done():
				return
			}

			fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"id\":%d,\"result\":{\"source\":\"owned\",\"method\":%q}}\n", rpc.ID, rpc.Method)
			flusher.Flush()
		}))
		ginkgo.DeferCleanup(srv.Close)

		transport := startPrivateHTTPTransport(srv.URL, nil)
		client := NewClient(transport)
		ginkgo.DeferCleanup(client.Close)
		gomega.Expect(client.Start(ctx)).To(gomega.Succeed())

		type callOutcome struct {
			method string
			result json.RawMessage
			err    error
		}
		outcomes := make(chan callOutcome, 2)
		for _, method := range []string{"test/first", "test/second"} {
			method := method
			go func() {
				result, err := client.call(ctx, method, nil)
				outcomes <- callOutcome{method: method, result: result, err: err}
			}()
		}

		for range 2 {
			var outcome callOutcome
			select {
			case outcome = <-outcomes:
			case <-ctx.Done():
				ginkgo.Fail("concurrent MCP calls did not complete: " + ctx.Err().Error())
			}
			gomega.Expect(outcome.err).NotTo(gomega.HaveOccurred())
			var result struct {
				Source string `json:"source"`
				Method string `json:"method"`
			}
			gomega.Expect(json.Unmarshal(outcome.result, &result)).To(gomega.Succeed())
			gomega.Expect(result).To(gomega.Equal(struct {
				Source string `json:"source"`
				Method string `json:"method"`
			}{Source: "owned", Method: outcome.method}))
		}
	})

	ginkgo.It("rejects an unsupported server request without completing the pending client call", ginkgo.Label("race-contract"), func(ctx ginkgo.SpecContext) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
			var rpc rawRequest
			if err := json.NewDecoder(request.Body).Decode(&rpc); err != nil {
				http.Error(w, "invalid request", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"id\":%d,\"method\":\"sampling/createMessage\",\"params\":{}}\n", rpc.ID)
			fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"id\":%d,\"result\":{\"should_not_complete\":true}}\n", rpc.ID)
		}))
		ginkgo.DeferCleanup(srv.Close)

		transport := startPrivateHTTPTransport(srv.URL, nil)
		client := NewClient(transport)
		ginkgo.DeferCleanup(client.Close)
		gomega.Expect(client.Start(ctx)).To(gomega.Succeed())

		result, err := client.call(ctx, "test/server-request", nil)
		gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring(`unsupported server request "sampling/createMessage"`)))
		gomega.Expect(result).To(gomega.BeNil())
	})

	ginkgo.It("supports concurrent sends without losing response frames", func(ctx ginkgo.SpecContext) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
			var rpc rawRequest
			if err := json.NewDecoder(request.Body).Decode(&rpc); err != nil {
				http.Error(w, "invalid JSON-RPC request", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":"ok"}`, rpc.ID)
		}))
		ginkgo.DeferCleanup(srv.Close)
		transport := startPrivateHTTPTransport(srv.URL, nil)
		callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		ginkgo.DeferCleanup(cancel)
		const sends = 5
		sendErrors := make(chan error, sends)
		var wg sync.WaitGroup
		for i := 0; i < sends; i++ {
			id := i + 1
			wg.Add(1)
			go func() {
				defer wg.Done()
				payload := fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"ping"}`, id)
				sendErrors <- transport.Send(callCtx, []byte(payload))
			}()
		}
		wg.Wait()
		close(sendErrors)
		for err := range sendErrors {
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
		}
		for i := 0; i < sends; i++ {
			_, err := transport.Receive(callCtx)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
		}
	})

	ginkgo.It("round-trips initialize, tool discovery, and tool calls through HTTP", func(ctx ginkgo.SpecContext) {
		var requestCount atomic.Int64
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestCount.Add(1)
			body, _ := io.ReadAll(r.Body)
			w.Header().Set("Content-Type", "text/event-stream")
			switch {
			case strings.Contains(string(body), `"method":"initialize"`):
				fmt.Fprintln(w, `data: {"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2024-11-05"}}`)
			case strings.Contains(string(body), `"method":"tools/list"`):
				fmt.Fprintln(w, `data: {"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"echo","description":"echo back"}]}}`)
			case strings.Contains(string(body), `"method":"tools/call"`):
				fmt.Fprintln(w, `data: {"jsonrpc":"2.0","id":3,"result":{"content":[{"type":"text","text":"hello back"}]}}`)
			}
		}))
		ginkgo.DeferCleanup(srv.Close)
		transport := startPrivateHTTPTransport(srv.URL, nil)
		client := NewClient(transport)
		ginkgo.DeferCleanup(client.Close)
		callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		ginkgo.DeferCleanup(cancel)
		gomega.Expect(client.Start(callCtx)).To(gomega.Succeed())
		gomega.Expect(client.Initialize(callCtx, "idiom-test")).To(gomega.Succeed())
		tools, err := client.ListTools(callCtx)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(tools).To(gomega.ConsistOf(gomega.HaveField("Name", "echo")))
		result, err := client.CallTool(callCtx, "echo", []byte(`{"text":"hello"}`))
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(result.Content).To(gomega.ContainElement(gomega.WithTransform(func(raw json.RawMessage) bool {
			return strings.Contains(string(raw), "hello back")
		}, gomega.BeTrue())))
		gomega.Expect(requestCount.Load()).To(gomega.BeNumerically(">=", 4))
	})
})

func startPrivateHTTPTransport(endpoint string, headers map[string]string) *HTTPTransport {
	ginkgo.GinkgoHelper()
	transport, err := StartHTTP(endpoint, headers, 5*time.Second, true)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	ginkgo.DeferCleanup(transport.Close)
	return transport
}
