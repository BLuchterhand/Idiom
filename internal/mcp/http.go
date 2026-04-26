package mcp

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	defaultHTTPTimeout = 30 * time.Second
	maxResponseFrame   = 4 << 20 // 4 MiB per SSE line / single JSON body
	maxErrorBody       = 4096
)

// HTTPTransport speaks JSON-RPC 2.0 over HTTP for hosted MCP endpoints.
// Each MCP request is one HTTP POST. The response is read either as a
// single application/json reply or as a text/event-stream body whose
// `data:` lines carry JSON-RPC frames. Frames stream into an in-memory
// inbox; Receive returns the next frame in arrival order. The Client's
// pending-by-id map demuxes concurrent calls.
//
// Use this for hosted servers like https://mcp.exa.ai/mcp. Local stdio
// servers go through StartStdio.
type HTTPTransport struct {
	url     string
	client  *http.Client
	headers map[string]string

	inbox     chan []byte
	closed    chan struct{}
	closeOnce sync.Once

	wg sync.WaitGroup

	bodiesMu sync.Mutex
	bodies   map[io.Closer]struct{}
}

// StartHTTP validates rawURL through the SSRF guard, then returns a
// transport wired to that endpoint. headers are applied to every
// request — use this for static auth like Bearer tokens. timeout
// becomes the http.Client.Timeout; a zero value defaults to 30 s.
// Set allowPrivate to true to register an MCP server inside an
// intranet (matches Workspace.AllowPrivateNetwork).
func StartHTTP(rawURL string, headers map[string]string, timeout time.Duration, allowPrivate bool) (*HTTPTransport, error) {
	if _, _, err := ValidateHTTPURL(rawURL, allowPrivate); err != nil {
		return nil, err
	}
	if timeout <= 0 {
		timeout = defaultHTTPTimeout
	}
	cp := make(map[string]string, len(headers))
	for k, v := range headers {
		cp[k] = v
	}
	return &HTTPTransport{
		url:     rawURL,
		client:  &http.Client{Timeout: timeout},
		headers: cp,
		inbox:   make(chan []byte, 16),
		closed:  make(chan struct{}),
		bodies:  make(map[io.Closer]struct{}),
	}, nil
}

// Send issues one HTTP POST carrying payload as the JSON-RPC body and
// dispatches the response into the inbox. Returns once response headers
// arrive (and any HTTP error status is surfaced); body parsing happens
// in a background goroutine.
func (t *HTTPTransport) Send(ctx context.Context, payload []byte) error {
	select {
	case <-t.closed:
		return io.ErrClosedPipe
	default:
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.url, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range t.headers {
		req.Header.Set(k, v)
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Errorf("post %s: %w", RedactURL(t.url), err)
	}
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		_ = resp.Body.Close()
		return fmt.Errorf("post %s: http %d: %s", RedactURL(t.url), resp.StatusCode, strings.TrimSpace(string(body)))
	}
	mt, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	switch mt {
	case "application/json":
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseFrame))
		_ = resp.Body.Close()
		if readErr != nil {
			return fmt.Errorf("read json body: %w", readErr)
		}
		return t.pushFrame(ctx, body)
	case "text/event-stream", "":
		t.trackBody(resp.Body)
		t.wg.Add(1)
		go t.streamSSE(resp.Body)
		return nil
	default:
		_ = resp.Body.Close()
		return fmt.Errorf("post %s: unexpected content-type %q", RedactURL(t.url), resp.Header.Get("Content-Type"))
	}
}

// Receive blocks until the next JSON-RPC frame is available or the
// context / transport is cancelled.
func (t *HTTPTransport) Receive(ctx context.Context) ([]byte, error) {
	select {
	case frame := <-t.inbox:
		return frame, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-t.closed:
		return nil, io.EOF
	}
}

// Close signals shutdown, closes any in-flight response bodies to kick
// blocked reads, and waits up to 2 s for goroutines to drain.
// Idempotent.
func (t *HTTPTransport) Close() error {
	t.closeOnce.Do(func() {
		close(t.closed)
		t.bodiesMu.Lock()
		for body := range t.bodies {
			_ = body.Close()
		}
		t.bodies = nil
		t.bodiesMu.Unlock()
	})
	done := make(chan struct{})
	go func() { t.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
	}
	return nil
}

func (t *HTTPTransport) pushFrame(ctx context.Context, frame []byte) error {
	select {
	case t.inbox <- frame:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-t.closed:
		return io.ErrClosedPipe
	}
}

func (t *HTTPTransport) streamSSE(body io.ReadCloser) {
	defer t.wg.Done()
	defer t.untrackBody(body)
	defer body.Close()
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64<<10), maxResponseFrame)
	for scanner.Scan() {
		line := scanner.Bytes()
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		frame := bytes.TrimSpace(line[5:])
		if len(frame) == 0 {
			continue
		}
		select {
		case t.inbox <- bytes.Clone(frame):
		case <-t.closed:
			return
		}
	}
}

func (t *HTTPTransport) trackBody(b io.Closer) {
	t.bodiesMu.Lock()
	if t.bodies != nil {
		t.bodies[b] = struct{}{}
	}
	t.bodiesMu.Unlock()
}

func (t *HTTPTransport) untrackBody(b io.Closer) {
	t.bodiesMu.Lock()
	if t.bodies != nil {
		delete(t.bodies, b)
	}
	t.bodiesMu.Unlock()
}
