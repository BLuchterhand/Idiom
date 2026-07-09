package mcp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func TestHTTPTransport_SSEResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")

		_, _ = w.Write([]byte(": comment line\n"))
		_, _ = w.Write([]byte("event: message\n"))
		_, _ = w.Write([]byte(`data: {"jsonrpc":"2.0","id":1,"result":"hello"}` + "\n"))
		_, _ = w.Write([]byte("\n"))
	}))
	defer srv.Close()

	tr, err := StartHTTP(srv.URL, nil, 5*time.Second, true)
	if err != nil {
		t.Fatalf("StartHTTP: %v", err)
	}
	defer tr.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := tr.Send(ctx, []byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)); err != nil {
		t.Fatalf("Send: %v", err)
	}

	frame, err := tr.Receive(ctx)
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}

	want := `{"jsonrpc":"2.0","id":1,"result":"hello"}`
	if string(frame.Payload) != want {
		t.Fatalf("Receive frame = %q, want %q", string(frame.Payload), want)
	}
}

func TestHTTPTransport_MultipleDataFramesInOneBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")

		_, _ = w.Write([]byte(`data: {"id":1}` + "\n"))
		_, _ = w.Write([]byte(`data: {"id":2}` + "\n"))
		_, _ = w.Write([]byte(`data: {"id":3}` + "\n"))
	}))
	defer srv.Close()

	tr, err := StartHTTP(srv.URL, nil, 5*time.Second, true)
	if err != nil {
		t.Fatalf("StartHTTP: %v", err)
	}
	defer tr.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := tr.Send(ctx, []byte(`{}`)); err != nil {
		t.Fatalf("Send: %v", err)
	}

	for i := 1; i <= 3; i++ {
		frame, err := tr.Receive(ctx)
		if err != nil {
			t.Fatalf("Receive %d: %v", i, err)
		}

		want := fmt.Sprintf(`{"id":%d}`, i)
		if string(frame.Payload) != want {
			t.Fatalf("frame %d = %q, want %q", i, string(frame.Payload), want)
		}
	}
}

func TestHTTPTransport_JSONResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":42}`))
	}))
	defer srv.Close()

	tr, err := StartHTTP(srv.URL, nil, 5*time.Second, true)
	if err != nil {
		t.Fatalf("StartHTTP: %v", err)
	}
	defer tr.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := tr.Send(ctx, []byte(`{}`)); err != nil {
		t.Fatalf("Send: %v", err)
	}

	frame, err := tr.Receive(ctx)
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}

	if !strings.Contains(string(frame.Payload), `"result":42`) {
		t.Fatalf("frame %q should contain result:42", string(frame.Payload))
	}
}

func TestHTTPTransport_HeadersAndAccept(t *testing.T) {
	var got http.Header

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")

		_, _ = w.Write([]byte(`{}`))
	}))

	defer srv.Close()

	tr, err := StartHTTP(srv.URL, map[string]string{"X-Api-Key": "secret"}, 5*time.Second, true)
	if err != nil {
		t.Fatalf("StartHTTP: %v", err)
	}
	defer tr.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := tr.Send(ctx, []byte(`{}`)); err != nil {
		t.Fatalf("Send: %v", err)
	}

	if _, err := tr.Receive(ctx); err != nil {
		t.Fatalf("Receive: %v", err)
	}

	if got.Get("Accept") != "application/json, text/event-stream" {
		t.Fatalf("Accept = %q", got.Get("Accept"))
	}

	if got.Get("Content-Type") != "application/json" {
		t.Fatalf("Content-Type = %q", got.Get("Content-Type"))
	}

	if got.Get("X-Api-Key") != "secret" {
		t.Fatalf("X-Api-Key = %q", got.Get("X-Api-Key"))
	}
}

func TestHTTPTransport_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(401)

		_, _ = w.Write([]byte("unauthorized"))
	}))
	defer srv.Close()

	tr, err := StartHTTP(srv.URL, nil, 5*time.Second, true)
	if err != nil {
		t.Fatalf("StartHTTP: %v", err)
	}
	defer tr.Close()

	err = tr.Send(context.Background(), []byte(`{}`))
	if err == nil {
		t.Fatalf("expected error")
	}

	if !strings.Contains(err.Error(), "401") {
		t.Fatalf("error %v should mention 401", err)
	}
}

func TestHTTPTransport_HTTPErrorRedactsURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(401)
	}))
	defer srv.Close()

	tr, err := StartHTTP(srv.URL+"?secret=xyz", nil, 5*time.Second, true)
	if err != nil {
		t.Fatalf("StartHTTP: %v", err)
	}
	defer tr.Close()

	err = tr.Send(context.Background(), []byte(`{}`))
	if err == nil {
		t.Fatalf("expected error")
	}

	if strings.Contains(err.Error(), "secret=xyz") {
		t.Fatalf("error leaked query string: %v", err)
	}
}

func TestHTTPTransport_ClientErrorRedactsURLAndPreservesCause(t *testing.T) {
	sentinel := errors.New("dial sentinel")
	tr, err := StartHTTP("https://user:password@1.1.1.1/mcp?secret=xyz", nil, 5*time.Second, false)
	if err != nil {
		t.Fatalf("StartHTTP: %v", err)
	}
	defer tr.Close()
	tr.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, &url.Error{
			Op:  "dial",
			URL: "https://nested-user:nested-password@example.com/mcp?nested-secret=yes",
			Err: sentinel,
		}
	})

	err = tr.Send(context.Background(), []byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	if !errors.Is(err, sentinel) {
		t.Fatalf("Send error = %v, want wrapped sentinel", err)
	}
	for _, secret := range []string{"user", "password", "secret=xyz", "nested-secret=yes"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("Send error leaked %q: %v", secret, err)
		}
	}
}

func TestHTTPTransport_SSRFGuardBlocksLoopback(t *testing.T) {
	_, err := StartHTTP("http://127.0.0.1:9/", nil, 5*time.Second, false)
	if err == nil {
		t.Fatalf("expected SSRF error")
	}

	if !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("error %v should mention loopback", err)
	}
}

func TestHTTPTransport_RedirectPolicyBlocksPrivateTarget(t *testing.T) {
	tr, err := StartHTTP("https://1.1.1.1/mcp", map[string]string{"X-Api-Key": "secret"}, 5*time.Second, false)
	if err != nil {
		t.Fatalf("StartHTTP: %v", err)
	}
	defer tr.Close()

	original, _ := http.NewRequest(http.MethodPost, "https://1.1.1.1/mcp", nil)
	redirect, _ := http.NewRequest(http.MethodPost, "http://127.0.0.1/private", nil)
	err = tr.client.CheckRedirect(redirect, []*http.Request{original})
	if err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("private redirect error = %v, want loopback denial", err)
	}
}

func TestHTTPTransport_CrossOriginRedirectStripsEndpointCredentials(t *testing.T) {
	tr, err := StartHTTP("https://1.1.1.1/mcp?api_key=query-secret", map[string]string{"X-Api-Key": "header-secret"}, 5*time.Second, false)
	if err != nil {
		t.Fatalf("StartHTTP: %v", err)
	}
	defer tr.Close()

	original, _ := http.NewRequest(http.MethodPost, "https://1.1.1.1/mcp?api_key=query-secret", nil)
	redirect, _ := http.NewRequest(http.MethodPost, "https://8.8.8.8/mcp", nil)
	redirect.Header.Set("X-Api-Key", "header-secret")
	redirect.Header.Set("Authorization", "Bearer secret")
	redirect.Header.Set("Referer", original.URL.String())

	if err := tr.client.CheckRedirect(redirect, []*http.Request{original}); err != nil {
		t.Fatalf("public redirect should remain available: %v", err)
	}
	for _, name := range []string{"X-Api-Key", "Authorization", "Referer"} {
		if value := redirect.Header.Get(name); value != "" {
			t.Fatalf("cross-origin redirect retained %s=%q", name, value)
		}
	}
}

func TestHTTPTransport_PublicPolicyDisablesEnvironmentProxy(t *testing.T) {
	tr, err := StartHTTP("https://1.1.1.1/mcp", nil, 5*time.Second, false)
	if err != nil {
		t.Fatalf("StartHTTP: %v", err)
	}
	defer tr.Close()

	roundTripper, ok := tr.client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport type = %T, want *http.Transport", tr.client.Transport)
	}
	if roundTripper.Proxy != nil {
		t.Fatal("public-network containment cannot delegate target resolution to an environment proxy")
	}
	if roundTripper.DialTLS != nil || roundTripper.DialTLSContext != nil {
		t.Fatal("HTTPS-specific dial hooks must not bypass the validated DialContext")
	}
}

func TestHTTPTransport_DoesNotDependOnMutableDefaultTransport(t *testing.T) {
	original := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("global transport must not be used")
	})
	defer func() { http.DefaultTransport = original }()

	tr, err := StartHTTP("https://1.1.1.1/mcp", nil, 5*time.Second, false)
	if err != nil {
		t.Fatalf("StartHTTP: %v", err)
	}
	defer tr.Close()
	if _, ok := tr.client.Transport.(*http.Transport); !ok {
		t.Fatalf("owned transport type = %T, want *http.Transport", tr.client.Transport)
	}
}

func TestHTTPTransport_AllowPrivateLetsLoopbackThrough(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	// httptest binds to 127.0.0.1; allowPrivate=true is required.
	tr, err := StartHTTP(srv.URL, nil, 5*time.Second, true)
	if err != nil {
		t.Fatalf("StartHTTP with allowPrivate: %v", err)
	}

	_ = tr.Close()
}

func TestHTTPTransport_EmptyExpectedEventStreamReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, ": stream ended without a response\n")
	}))
	defer srv.Close()

	tr, err := StartHTTP(srv.URL, nil, 5*time.Second, true)
	if err != nil {
		t.Fatalf("StartHTTP: %v", err)
	}
	defer tr.Close()

	if err := tr.Send(context.Background(), []byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)); err != nil {
		t.Fatalf("Send: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	message, err := tr.Receive(ctx)
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if !errors.Is(message.RequestError, io.ErrUnexpectedEOF) {
		t.Fatalf("request error = %v, want unexpected EOF", message.RequestError)
	}
}

func TestHTTPTransport_OversizedEventStreamReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: "+strings.Repeat("x", maxResponseFrame+1)+"\n")
	}))
	defer srv.Close()

	tr, err := StartHTTP(srv.URL, nil, 5*time.Second, true)
	if err != nil {
		t.Fatalf("StartHTTP: %v", err)
	}
	defer tr.Close()

	if err := tr.Send(context.Background(), []byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)); err != nil {
		t.Fatalf("Send: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	message, err := tr.Receive(ctx)
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if message.RequestError == nil || !strings.Contains(message.RequestError.Error(), "event stream") {
		t.Fatalf("request error = %v, want event-stream size error", message.RequestError)
	}
}

func TestHTTPTransport_StreamFailureIsRequestScoped(t *testing.T) {
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
	defer srv.Close()

	transport, err := StartHTTP(srv.URL, nil, 5*time.Second, true)
	if err != nil {
		t.Fatalf("StartHTTP: %v", err)
	}
	client := NewClient(transport)
	if err := client.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer client.Close()

	if _, err := client.call(context.Background(), "test/stream-failure", nil); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("first call error = %v, want request-scoped unexpected EOF", err)
	}
	result, err := client.call(context.Background(), "test/success", nil)
	if err != nil {
		t.Fatalf("subsequent call should remain usable: %v", err)
	}
	if !strings.Contains(string(result), `"ok":true`) {
		t.Fatalf("subsequent result = %s, want ok", result)
	}
}

func TestHTTPTransport_JSONResponseSizeLimit(t *testing.T) {
	exact := strings.Repeat("x", maxResponseFrame)
	frame, err := readJSONResponseFrame(strings.NewReader(exact))
	if err != nil || len(frame) != maxResponseFrame {
		t.Fatalf("exact-limit frame len=%d err=%v", len(frame), err)
	}
	if _, err := readJSONResponseFrame(strings.NewReader(exact + "x")); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized frame error = %v, want size denial", err)
	}
}

func TestHTTPTransport_CloseCancelsRequestBeforeHeaders(t *testing.T) {
	requestStarted := make(chan struct{})
	releaseHandler := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, req *http.Request) {
		close(requestStarted)
		select {
		case <-req.Context().Done():
		case <-releaseHandler:
		}
	}))
	defer srv.Close()
	defer close(releaseHandler)

	tr, err := StartHTTP(srv.URL, nil, 30*time.Second, true)
	if err != nil {
		t.Fatalf("StartHTTP: %v", err)
	}
	sendDone := make(chan error, 1)
	go func() {
		sendDone <- tr.Send(context.Background(), []byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	}()

	select {
	case <-requestStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("request did not reach server")
	}
	started := time.Now()
	if err := tr.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("Close took %v, want prompt request cancellation", elapsed)
	}
	select {
	case err := <-sendDone:
		if !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("Send error = %v, want closed transport", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Send remained blocked after Close")
	}
}

func TestHTTPTransport_ReceiveRespectsContext(t *testing.T) {
	tr, err := StartHTTP("https://1.1.1.1/", nil, 5*time.Second, false)
	if err != nil {
		t.Fatalf("StartHTTP: %v", err)
	}
	defer tr.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	if _, err := tr.Receive(ctx); err == nil {
		t.Fatalf("expected context deadline error")
	}
}

func TestHTTPTransport_CloseDrainsAndReturnsEOF(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")

		flusher, _ := w.(http.Flusher)
		for i := 0; i < 5; i++ {
			fmt.Fprintf(w, "data: {\"i\":%d}\n", i)

			if flusher != nil {
				flusher.Flush()
			}

			time.Sleep(50 * time.Millisecond)
		}
	}))
	defer srv.Close()

	tr, err := StartHTTP(srv.URL, nil, 5*time.Second, true)
	if err != nil {
		t.Fatalf("StartHTTP: %v", err)
	}

	ctx := context.Background()
	if err := tr.Send(ctx, []byte(`{}`)); err != nil {
		t.Fatalf("Send: %v", err)
	}

	if _, err := tr.Receive(ctx); err != nil {
		t.Fatalf("Receive: %v", err)
	}

	closeStart := time.Now()

	if err := tr.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if elapsed := time.Since(closeStart); elapsed > 1*time.Second {
		t.Fatalf("Close took %v (should be quick — in-flight body was forced shut)", elapsed)
	}

	if _, err := tr.Receive(ctx); err != io.EOF {
		t.Fatalf("Receive after Close = %v, want io.EOF", err)
	}
}

func TestHTTPTransport_ConcurrentSends(t *testing.T) {
	var counter int64

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := atomic.AddInt64(&counter, 1)

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":"ok"}`, n)
	}))

	defer srv.Close()

	tr, err := StartHTTP(srv.URL, nil, 5*time.Second, true)
	if err != nil {
		t.Fatalf("StartHTTP: %v", err)
	}
	defer tr.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	const N = 5

	var wg sync.WaitGroup

	for i := 0; i < N; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			_ = tr.Send(ctx, []byte(`{}`))
		}()
	}

	wg.Wait()

	received := 0

	for i := 0; i < N; i++ {
		if _, err := tr.Receive(ctx); err == nil {
			received++
		}
	}

	if received != N {
		t.Fatalf("received %d frames, want %d", received, N)
	}
}

func TestHTTPTransport_RoundTripsThroughClient(t *testing.T) {
	// End-to-end: pair the HTTP transport with the existing Client and
	// confirm Initialize + ListTools + CallTool work the same as stdio.
	var requestCount int

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++

		body, _ := io.ReadAll(r.Body)

		w.Header().Set("Content-Type", "text/event-stream")

		switch {
		case strings.Contains(string(body), `"method":"initialize"`):
			fmt.Fprintln(w, `data: {"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2024-11-05"}}`)
		case strings.Contains(string(body), `"method":"tools/list"`):
			fmt.Fprintln(w, `data: {"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"echo","description":"echo back"}]}}`)
		case strings.Contains(string(body), `"method":"tools/call"`):
			fmt.Fprintln(w, `data: {"jsonrpc":"2.0","id":3,"result":{"content":[{"type":"text","text":"hello back"}]}}`)
		default:
			// notifications/initialized — no reply body.
		}
	}))

	defer srv.Close()

	tr, err := StartHTTP(srv.URL, nil, 5*time.Second, true)
	if err != nil {
		t.Fatalf("StartHTTP: %v", err)
	}

	c := NewClient(tr)

	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := c.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if err := c.Initialize(ctx, "idiom-test"); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	tools, err := c.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("ListTools = %+v, want one 'echo'", tools)
	}

	result, err := c.CallTool(ctx, "echo", []byte(`{"text":"hello"}`))
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}

	if len(result.Content) == 0 || !strings.Contains(string(result.Content[0]), "hello back") {
		t.Fatalf("CallTool result = %+v, want content containing 'hello back'", result)
	}
}
