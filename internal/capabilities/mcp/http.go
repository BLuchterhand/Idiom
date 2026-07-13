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
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	defaultHTTPTimeout  = 30 * time.Second
	maxResponseFrame    = 4 << 20 // 4 MiB per SSE line / single JSON body
	maxErrorBody        = 4096
	maxHTTPRedirects    = 10
	redactedHeaderValue = "<redacted-header-value>"
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
	url                   string
	client                *http.Client
	headers               map[string]string
	sensitiveHeaderValues []string

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

type redactedHTTPError struct {
	message string
	cause   error
}

func (e *redactedHTTPError) Error() string {
	return e.message
}

// Is preserves errors.Is checks without exposing an unredacted cause through
// errors.Unwrap. The configured header value remains private on every public
// error formatting surface.
func (e *redactedHTTPError) Is(target error) bool {
	return errors.Is(e.cause, target)
}

type jsonRPCFrameKind uint8

const (
	jsonRPCFrameResponse jsonRPCFrameKind = iota + 1
	jsonRPCFrameNotification
	jsonRPCFrameRequest
)

type jsonRPCFrameMetadata struct {
	kind   jsonRPCFrameKind
	id     int64
	method string
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
	sensitiveValueSet := make(map[string]struct{}, len(headers)*2)
	for k, v := range headers {
		cp[k] = v
		for _, candidate := range []string{v, strings.TrimSpace(v)} {
			if candidate != "" {
				sensitiveValueSet[candidate] = struct{}{}
			}
		}
	}
	sensitiveHeaderValues := make([]string, 0, len(sensitiveValueSet))
	for value := range sensitiveValueSet {
		sensitiveHeaderValues = append(sensitiveHeaderValues, value)
	}
	sort.Slice(sensitiveHeaderValues, func(i, j int) bool {
		return len(sensitiveHeaderValues[i]) > len(sensitiveHeaderValues[j])
	})

	shutdownCtx, cancel := context.WithCancel(context.Background())
	transport := &HTTPTransport{
		url:                   parsed.String(),
		headers:               cp,
		sensitiveHeaderValues: sensitiveHeaderValues,
		inbox:                 make(chan TransportMessage, 16),
		closed:                make(chan struct{}),
		bodies:                make(map[io.Closer]struct{}),
		connections:           make(map[*ownedHTTPConnection]struct{}),
		shutdownCtx:           shutdownCtx,
		cancel:                cancel,
		drained:               make(chan struct{}),
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
func (t *HTTPTransport) Send(ctx context.Context, payload []byte) (sendErr error) {
	defer func() {
		sendErr = t.redactError(sendErr)
	}()

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
		body := t.readErrorBody(resp.Body)

		_ = resp.Body.Close()

		return fmt.Errorf("post %s: http %d: %s", redactURL(t.url), resp.StatusCode, body)
	}

	mt, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	expectedID, expectsResponse := jsonRPCRequestID(payload)
	switch mt {
	case "application/json":
		defer t.untrackBody(resp.Body)
		body, readErr := readJSONResponseFrame(resp.Body)

		_ = resp.Body.Close()

		if readErr != nil {
			return fmt.Errorf("read json body: %w", readErr)
		}

		return t.dispatchHTTPJSONFrame(ctx, body, expectedID, expectsResponse)
	case "text/event-stream", "":
		t.wg.Add(1)
		requestOwnedByStream = true

		go func() {
			defer stopShutdownCancellation()
			defer cancelRequest()
			t.streamSSE(requestContext, resp.Body, expectedID, expectsResponse)
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

func validateHTTPJSONResponse(frame []byte, expectedID int64) error {
	metadata, err := classifyJSONRPCFrame(frame)
	if err != nil {
		return fmt.Errorf("malformed MCP JSON response: %w", err)
	}
	if metadata.kind != jsonRPCFrameResponse {
		return fmt.Errorf("malformed MCP JSON response: expected a response frame")
	}
	if metadata.id != expectedID {
		return fmt.Errorf("malformed MCP JSON response: response id %d does not match request id %d", metadata.id, expectedID)
	}

	return nil
}

func (t *HTTPTransport) dispatchHTTPJSONFrame(ctx context.Context, frame []byte, expectedID int64, expectsResponse bool) error {
	if expectsResponse {
		if responseErr := validateHTTPJSONResponse(frame, expectedID); responseErr != nil {
			t.pushRequestError(expectedID, responseErr)
			return nil
		}

		return t.pushFrame(ctx, frame)
	}

	metadata, err := classifyJSONRPCFrame(frame)
	if err != nil {
		// A JSON-RPC notification has no response owner. Some hosted servers
		// nevertheless return an empty or malformed JSON body; it must not enter
		// the shared inbox where an embedded ID could claim another pending call.
		return nil
	}

	switch metadata.kind {
	case jsonRPCFrameNotification:
		return t.pushFrame(ctx, frame)
	case jsonRPCFrameRequest:
		return fmt.Errorf("unsupported server request %q in response to notification", metadata.method)
	case jsonRPCFrameResponse:
		// Responses to notifications are forbidden by JSON-RPC and have no owning
		// client call. Drop them instead of demultiplexing solely by their ID.
		return nil
	default:
		return nil
	}
}

func classifyJSONRPCFrame(frame []byte) (jsonRPCFrameMetadata, error) {
	var envelope struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Method  json.RawMessage `json:"method"`
		Result  json.RawMessage `json:"result"`
		Error   json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(frame, &envelope); err != nil {
		return jsonRPCFrameMetadata{}, err
	}
	if envelope.JSONRPC != "2.0" {
		return jsonRPCFrameMetadata{}, fmt.Errorf("jsonrpc must be %q", "2.0")
	}

	hasID := len(envelope.ID) > 0
	hasMethod := len(envelope.Method) > 0
	hasResult := len(envelope.Result) > 0
	hasError := len(envelope.Error) > 0
	if hasMethod {
		if hasResult || hasError {
			return jsonRPCFrameMetadata{}, errors.New("request or notification must not contain result or error")
		}

		var method string
		if err := json.Unmarshal(envelope.Method, &method); err != nil || strings.TrimSpace(method) == "" {
			return jsonRPCFrameMetadata{}, errors.New("request or notification method must be a non-empty string")
		}
		if !hasID {
			return jsonRPCFrameMetadata{kind: jsonRPCFrameNotification, method: method}, nil
		}
		if bytes.Equal(bytes.TrimSpace(envelope.ID), []byte("null")) {
			return jsonRPCFrameMetadata{}, errors.New("request id must not be null")
		}

		return jsonRPCFrameMetadata{kind: jsonRPCFrameRequest, method: method}, nil
	}

	if !hasID || bytes.Equal(bytes.TrimSpace(envelope.ID), []byte("null")) {
		return jsonRPCFrameMetadata{}, errors.New("response id is required")
	}
	if hasResult == hasError {
		return jsonRPCFrameMetadata{}, errors.New("response must contain exactly one of result or error")
	}
	if hasError {
		var responseError rawError
		if err := json.Unmarshal(envelope.Error, &responseError); err != nil || bytes.Equal(bytes.TrimSpace(envelope.Error), []byte("null")) {
			return jsonRPCFrameMetadata{}, errors.New("response error must be an object with integer code and string message")
		}
	}

	var id int64
	if err := json.Unmarshal(envelope.ID, &id); err != nil {
		return jsonRPCFrameMetadata{}, errors.New("response id must match the integer request id")
	}

	return jsonRPCFrameMetadata{kind: jsonRPCFrameResponse, id: id}, nil
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
	frame = bytes.Clone(t.redactJSONRPCErrorFrame(frame))
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
	err = t.redactError(err)
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

func (t *HTTPTransport) streamSSE(ctx context.Context, body io.ReadCloser, expectedID int64, expectsResponse bool) {
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
		metadata, err := classifyJSONRPCFrame(frame)
		if err != nil {
			t.pushRequestError(expectedID, fmt.Errorf("read MCP event stream: malformed JSON-RPC frame: %w", err))
			return
		}

		switch metadata.kind {
		case jsonRPCFrameNotification:
			if err := t.pushFrame(ctx, frame); err != nil {
				return
			}
		case jsonRPCFrameRequest:
			t.pushRequestError(expectedID, fmt.Errorf("read MCP event stream: unsupported server request %q", metadata.method))
			return
		case jsonRPCFrameResponse:
			if !expectsResponse || metadata.id != expectedID || matchedResponse {
				continue
			}
			matchedResponse = true
			if err := t.pushFrame(ctx, frame); err != nil {
				return
			}
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

func (t *HTTPTransport) redactError(err error) error {
	if err == nil {
		return nil
	}

	message := t.redactText(err.Error())
	if message == err.Error() {
		return err
	}

	return &redactedHTTPError{message: message, cause: err}
}

func (t *HTTPTransport) redactText(text string) string {
	for _, value := range t.sensitiveHeaderValues {
		text = strings.ReplaceAll(text, value, redactedHeaderValue)
	}

	return text
}

func (t *HTTPTransport) readErrorBody(body io.Reader) string {
	readLimit := maxErrorBody
	if len(t.sensitiveHeaderValues) > 0 {
		// Values are sorted longest-first. Reading one maximum value beyond the
		// diagnostic limit prevents truncation from exposing a credential prefix.
		readLimit += len(t.sensitiveHeaderValues[0])
	}
	raw, _ := io.ReadAll(io.LimitReader(body, int64(readLimit)))
	redacted := t.redactText(string(raw))
	if len(redacted) > maxErrorBody {
		redacted = redacted[:maxErrorBody]
	}

	return strings.TrimSpace(redacted)
}

func (t *HTTPTransport) redactJSONRPCErrorFrame(frame []byte) []byte {
	if len(t.sensitiveHeaderValues) == 0 {
		return frame
	}

	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(frame, &envelope); err != nil {
		return frame
	}
	errorFrame, ok := envelope["error"]
	if !ok {
		return frame
	}

	var errorValue any
	decoder := json.NewDecoder(bytes.NewReader(errorFrame))
	decoder.UseNumber()
	if err := decoder.Decode(&errorValue); err != nil {
		return frame
	}
	redactedError, err := json.Marshal(t.redactJSONValue(errorValue))
	if err != nil {
		return frame
	}
	envelope["error"] = redactedError
	redacted, err := json.Marshal(envelope)
	if err != nil {
		return frame
	}

	return redacted
}

func (t *HTTPTransport) redactJSONValue(value any) any {
	switch typed := value.(type) {
	case string:
		return t.redactText(typed)
	case []any:
		for index := range typed {
			typed[index] = t.redactJSONValue(typed[index])
		}
		return typed
	case map[string]any:
		redacted := make(map[string]any, len(typed))
		for key, item := range typed {
			redacted[t.redactText(key)] = t.redactJSONValue(item)
		}
		return redacted
	default:
		return value
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
