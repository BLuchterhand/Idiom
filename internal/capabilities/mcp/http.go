package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	defaultHTTPTimeout = 30 * time.Second
	maxResponseFrame   = 4 << 20 // 4 MiB per SSE line / single JSON body
	maxErrorBody       = 4096
	maxHTTPRedirects   = 10
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

	inbox     chan TransportMessage
	closed    chan struct{}
	closeOnce sync.Once

	lifecycleMu sync.Mutex
	wg          sync.WaitGroup
	shutdownCtx context.Context
	cancel      context.CancelFunc
	drained     chan struct{}

	bodiesMu sync.Mutex
	bodies   map[io.Closer]struct{}

	connectionsMu sync.Mutex
	connections   map[*ownedHTTPConnection]struct{}
}

type ownedHTTPConnection struct {
	net.Conn
	onClose   func(*ownedHTTPConnection)
	closeOnce sync.Once
	closeErr  error
}

func (connection *ownedHTTPConnection) Close() error {
	connection.closeOnce.Do(func() {
		connection.closeErr = connection.Conn.Close()
		connection.onClose(connection)
	})
	return connection.closeErr
}

// StartHTTP validates rawURL through the SSRF guard, then returns a
// transport wired to that endpoint. headers are applied to every
// request — use this for static auth like Bearer tokens. timeout
// becomes the http.Client.Timeout; a zero value defaults to 30 s.
// Set allowPrivate to true to register an MCP server inside an
// intranet (matches Workspace.AllowPrivateNetwork).
func StartHTTP(rawURL string, headers map[string]string, timeout time.Duration, allowPrivate bool) (*HTTPTransport, error) {
	if timeout <= 0 {
		timeout = defaultHTTPTimeout
	}

	policy := newHTTPNetworkPolicy(allowPrivate)
	validationContext, cancelValidation := context.WithTimeout(context.Background(), timeout)
	parsed, _, err := policy.validateURL(validationContext, rawURL)
	cancelValidation()
	if err != nil {
		return nil, err
	}

	cp := make(map[string]string, len(headers))
	for k, v := range headers {
		cp[k] = v
	}

	shutdownCtx, cancel := context.WithCancel(context.Background())
	transport := &HTTPTransport{
		url:         parsed.String(),
		headers:     cp,
		inbox:       make(chan TransportMessage, 16),
		closed:      make(chan struct{}),
		bodies:      make(map[io.Closer]struct{}),
		connections: make(map[*ownedHTTPConnection]struct{}),
		shutdownCtx: shutdownCtx,
		cancel:      cancel,
		drained:     make(chan struct{}),
	}
	roundTripper := &http.Transport{
		DialContext:           transport.dialValidated(policy),
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
	if allowPrivate {
		// Private-network mode is explicit, so proxy-side target resolution is
		// allowed and retains the standard Go environment-proxy behavior.
		roundTripper.Proxy = http.ProxyFromEnvironment
	}

	transport.client = &http.Client{
		Timeout:       timeout,
		Transport:     roundTripper,
		CheckRedirect: httpRedirectPolicy(policy, cp),
	}
	return transport, nil
}

func httpRedirectPolicy(policy httpNetworkPolicy, configuredHeaders map[string]string) func(*http.Request, []*http.Request) error {
	return func(req *http.Request, via []*http.Request) error {
		if len(via) >= maxHTTPRedirects {
			return fmt.Errorf("stopped after %d redirects", maxHTTPRedirects)
		}
		if _, _, err := policy.validateURL(req.Context(), req.URL.String()); err != nil {
			return fmt.Errorf("redirect target denied: %w", err)
		}

		if len(via) > 0 && !sameHTTPOrigin(via[0].URL, req.URL) {
			// Preserve safe public redirects without forwarding endpoint-scoped
			// credentials or a Referer that may contain a query-string API key.
			for name := range configuredHeaders {
				req.Header.Del(name)
			}
			for _, name := range []string{"Authorization", "Cookie", "Proxy-Authorization", "Referer"} {
				req.Header.Del(name)
			}
		}

		return nil
	}
}

func sameHTTPOrigin(left, right *url.URL) bool {
	if left == nil || right == nil {
		return false
	}

	leftScheme := strings.ToLower(left.Scheme)
	rightScheme := strings.ToLower(right.Scheme)
	return leftScheme == rightScheme &&
		strings.EqualFold(left.Hostname(), right.Hostname()) &&
		httpOriginPort(leftScheme, left.Port()) == httpOriginPort(rightScheme, right.Port())
}

func httpOriginPort(scheme, port string) string {
	if port != "" {
		return port
	}
	if scheme == "https" {
		return "443"
	}
	if scheme == "http" {
		return "80"
	}
	return ""
}

// Send issues one HTTP POST carrying payload as the JSON-RPC body and
// dispatches the response into the inbox. Returns once response headers
// arrive (and any HTTP error status is surfaced); body parsing happens
// in a background goroutine.
func (t *HTTPTransport) Send(ctx context.Context, payload []byte) error {
	if !t.beginSend() {
		return io.ErrClosedPipe
	}
	defer t.wg.Done()
	requestContext, cancelRequest := context.WithCancel(ctx)
	stopShutdownCancellation := context.AfterFunc(t.shutdownCtx, cancelRequest)
	requestOwnedByStream := false
	defer func() {
		if !requestOwnedByStream {
			stopShutdownCancellation()
			cancelRequest()
		}
	}()

	req, err := http.NewRequestWithContext(requestContext, http.MethodPost, t.url, bytes.NewReader(payload))
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
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if t.shutdownCtx.Err() != nil {
			return io.ErrClosedPipe
		}
		return fmt.Errorf("post %s: %w", redactURL(t.url), redactHTTPClientError(err))
	}
	if !t.trackResponseBody(resp.Body) {
		_ = resp.Body.Close()
		return io.ErrClosedPipe
	}

	if resp.StatusCode >= 400 {
		defer t.untrackBody(resp.Body)
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))

		_ = resp.Body.Close()

		return fmt.Errorf("post %s: http %d: %s", redactURL(t.url), resp.StatusCode, strings.TrimSpace(string(body)))
	}

	mt, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	switch mt {
	case "application/json":
		defer t.untrackBody(resp.Body)
		body, readErr := readJSONResponseFrame(resp.Body)

		_ = resp.Body.Close()

		if readErr != nil {
			return fmt.Errorf("read json body: %w", readErr)
		}

		return t.pushFrame(ctx, body)
	case "text/event-stream", "":
		expectedID, expectsResponse := jsonRPCRequestID(payload)
		t.wg.Add(1)
		requestOwnedByStream = true

		go func() {
			defer stopShutdownCancellation()
			defer cancelRequest()
			t.streamSSE(resp.Body, expectedID, expectsResponse)
		}()

		return nil
	default:
		t.untrackBody(resp.Body)
		_ = resp.Body.Close()
		return fmt.Errorf("post %s: unexpected content-type %q", redactURL(t.url), resp.Header.Get("Content-Type"))
	}
}

// Receive blocks until the next JSON-RPC frame is available or the
// context / transport is cancelled.
func (t *HTTPTransport) Receive(ctx context.Context) (TransportMessage, error) {
	select {
	case message := <-t.inbox:
		return message, nil
	case <-ctx.Done():
		return TransportMessage{}, ctx.Err()
	case <-t.closed:
		return TransportMessage{}, io.EOF
	}
}

// Close signals shutdown, closes any in-flight response bodies to kick
// blocked reads, and waits up to 2 s for goroutines to drain.
// Idempotent.
func (t *HTTPTransport) Close() error {
	t.closeOnce.Do(func() {
		t.lifecycleMu.Lock()
		close(t.closed)
		t.cancel()
		t.client.CloseIdleConnections()
		t.bodiesMu.Lock()

		for body := range t.bodies {
			_ = body.Close()
		}

		t.bodies = nil
		t.bodiesMu.Unlock()
		t.connectionsMu.Lock()
		connections := t.connections
		t.connections = nil
		t.connectionsMu.Unlock()
		t.lifecycleMu.Unlock()

		for connection := range connections {
			_ = connection.Close()
		}

		go func() {
			t.wg.Wait()
			close(t.drained)
		}()
	})

	select {
	case <-t.drained:
		return nil
	case <-time.After(2 * time.Second):
		return errors.New("timed out waiting for MCP HTTP transport shutdown")
	}
}

func readJSONResponseFrame(body io.Reader) ([]byte, error) {
	frame, err := io.ReadAll(io.LimitReader(body, maxResponseFrame+1))
	if err != nil {
		return nil, err
	}
	if len(frame) > maxResponseFrame {
		return nil, fmt.Errorf("MCP JSON response exceeds %d-byte limit", maxResponseFrame)
	}
	if len(frame) == 0 {
		return nil, io.ErrUnexpectedEOF
	}
	return frame, nil
}

func redactHTTPClientError(err error) error {
	var urlError *url.Error
	if !errors.As(err, &urlError) {
		return err
	}

	redacted := *urlError
	redacted.URL = redactURL(urlError.URL)
	redacted.Err = redactHTTPClientError(urlError.Err)
	return &redacted
}

func (t *HTTPTransport) pushFrame(ctx context.Context, frame []byte) error {
	select {
	case t.inbox <- TransportMessage{Payload: frame}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-t.closed:
		return io.ErrClosedPipe
	}
}

func (t *HTTPTransport) pushRequestError(requestID int64, err error) {
	if requestID == 0 || err == nil {
		return
	}

	select {
	case <-t.closed:
		return
	default:
	}

	select {
	case t.inbox <- TransportMessage{RequestID: requestID, RequestError: err}:
	case <-t.closed:
	}
}

func (t *HTTPTransport) streamSSE(body io.ReadCloser, expectedID int64, expectsResponse bool) {
	defer t.wg.Done()
	defer t.untrackBody(body)
	defer body.Close()

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64<<10), maxResponseFrame+32)
	matchedResponse := !expectsResponse

	for scanner.Scan() {
		line := scanner.Bytes()
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}

		frame := bytes.TrimSpace(line[5:])
		if len(frame) == 0 {
			continue
		}
		if len(frame) > maxResponseFrame {
			t.pushRequestError(expectedID, fmt.Errorf("read MCP event stream: frame exceeds %d-byte limit", maxResponseFrame))
			return
		}
		if id, ok := jsonRPCRequestID(frame); ok && id == expectedID {
			matchedResponse = true
		}

		select {
		case t.inbox <- TransportMessage{Payload: bytes.Clone(frame)}:
		case <-t.closed:
			return
		}
	}

	if err := scanner.Err(); err != nil {
		select {
		case <-t.closed:
			return
		default:
		}
		t.pushRequestError(expectedID, fmt.Errorf("read MCP event stream: %w", err))
		return
	}
	if !matchedResponse {
		t.pushRequestError(expectedID, fmt.Errorf("read MCP event stream: %w", io.ErrUnexpectedEOF))
	}
}

func jsonRPCRequestID(payload []byte) (int64, bool) {
	var envelope struct {
		ID json.RawMessage `json:"id"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return 0, false
	}
	id := bytes.TrimSpace(envelope.ID)
	if len(id) == 0 || bytes.Equal(id, []byte("null")) || bytes.Equal(id, []byte("0")) {
		return 0, false
	}

	var numericID int64
	if err := json.Unmarshal(id, &numericID); err != nil || numericID == 0 {
		return 0, false
	}
	return numericID, true
}

func (t *HTTPTransport) beginSend() bool {
	t.lifecycleMu.Lock()
	defer t.lifecycleMu.Unlock()

	select {
	case <-t.closed:
		return false
	default:
		t.wg.Add(1)
		return true
	}
}

func (t *HTTPTransport) dialValidated(policy httpNetworkPolicy) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		connection, err := policy.dialValidated(ctx, network, address)
		if err != nil {
			return nil, err
		}

		owned := &ownedHTTPConnection{Conn: connection, onClose: t.untrackConnection}
		t.lifecycleMu.Lock()
		defer t.lifecycleMu.Unlock()
		select {
		case <-t.closed:
			_ = connection.Close()
			return nil, io.ErrClosedPipe
		default:
			t.connectionsMu.Lock()
			t.connections[owned] = struct{}{}
			t.connectionsMu.Unlock()
			return owned, nil
		}
	}
}

func (t *HTTPTransport) untrackConnection(connection *ownedHTTPConnection) {
	t.connectionsMu.Lock()
	delete(t.connections, connection)
	t.connectionsMu.Unlock()
}

func (t *HTTPTransport) trackResponseBody(body io.Closer) bool {
	t.lifecycleMu.Lock()
	defer t.lifecycleMu.Unlock()

	select {
	case <-t.closed:
		return false
	default:
		t.trackBody(body)
		return true
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
