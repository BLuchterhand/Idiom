package mcp

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

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
	if string(frame) != want {
		t.Fatalf("Receive frame = %q, want %q", string(frame), want)
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
		if string(frame) != want {
			t.Fatalf("frame %d = %q, want %q", i, string(frame), want)
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

	if !strings.Contains(string(frame), `"result":42`) {
		t.Fatalf("frame %q should contain result:42", string(frame))
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

func TestHTTPTransport_SSRFGuardBlocksLoopback(t *testing.T) {
	_, err := StartHTTP("http://127.0.0.1:9/", nil, 5*time.Second, false)
	if err == nil {
		t.Fatalf("expected SSRF error")
	}

	if !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("error %v should mention loopback", err)
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
