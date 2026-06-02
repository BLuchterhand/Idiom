package workspace

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type lspBaseClient struct {
	cmd                *exec.Cmd
	stdin              io.WriteCloser
	stdout             *bufio.Reader
	stderr             bytes.Buffer
	nextID             int
	serverName         string
	handleNotification lspNotificationHandler
}

type LSPEnvelope struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *LSPRPCError    `json:"error,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
}

type LSPRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type LSPPosition struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type LSPRange struct {
	Start LSPPosition `json:"start"`
	End   LSPPosition `json:"end"`
}

type LSPLocation struct {
	URI   string   `json:"uri"`
	Range LSPRange `json:"range"`
}

type LSPLocationLink struct {
	TargetURI            string   `json:"targetUri"`
	TargetRange          LSPRange `json:"targetRange"`
	TargetSelectionRange LSPRange `json:"targetSelectionRange"`
}

type LSPSymbolInfo struct {
	Name          string      `json:"name"`
	Kind          int         `json:"kind"`
	Location      LSPLocation `json:"location"`
	ContainerName string      `json:"containerName"`
}

type LSPCallHierarchyItem struct {
	Name           string   `json:"name"`
	Kind           int      `json:"kind"`
	URI            string   `json:"uri"`
	Range          LSPRange `json:"range"`
	SelectionRange LSPRange `json:"selectionRange"`
	Detail         string   `json:"detail"`
}

type LSPDocumentSymbol struct {
	Name           string              `json:"name"`
	Detail         string              `json:"detail"`
	Kind           int                 `json:"kind"`
	Range          LSPRange            `json:"range"`
	SelectionRange LSPRange            `json:"selectionRange"`
	Children       []LSPDocumentSymbol `json:"children"`
}

type LSPIncomingCall struct {
	From       LSPCallHierarchyItem `json:"from"`
	FromRanges []LSPRange           `json:"fromRanges"`
}

type LSPOutgoingCall struct {
	To         LSPCallHierarchyItem `json:"to"`
	FromRanges []LSPRange           `json:"fromRanges"`
}

type lspNotificationHandler func(method string, params json.RawMessage)

type managedLSPClient interface {
	close()
	openFile(path string) error
	request(ctx context.Context, method string, params any, result any) error
}

type lspLocationQueryConfig struct {
	serverName string
	newClient  func(context.Context) (managedLSPClient, error)
	fileURI    func(string) string
	format     func([]LSPLocation, string, int, bool) string
}

func (c *lspBaseClient) close() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = c.request(ctx, "shutdown", nil, nil)
	_ = c.notify("exit", map[string]any{})
	c.forceClose()
}

func (c *lspBaseClient) forceClose() {
	_ = c.stdin.Close()
	if c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
	_, _ = io.Copy(io.Discard, c.stdout)
	_ = c.cmd.Wait()
}

func (c *lspBaseClient) notify(method string, params any) error {
	msg := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	}
	return c.writeMessage(msg)
}

func (c *lspBaseClient) request(ctx context.Context, method string, params any, result any) error {
	id := c.nextID + 1
	c.nextID = id
	msg := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}
	if err := c.writeMessage(msg); err != nil {
		return err
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		body, err := c.readMessage()
		if err != nil {
			if stderr := strings.TrimSpace(c.stderr.String()); stderr != "" {
				return fmt.Errorf("%w: %s", err, stderr)
			}
			return err
		}
		var env LSPEnvelope
		if err := json.Unmarshal(body, &env); err != nil {
			continue
		}
		if c.handleNotification != nil && env.Method != "" && len(env.ID) == 0 {
			c.handleNotification(env.Method, env.Params)
			continue
		}
		if len(env.ID) == 0 {
			continue
		}
		respID, err := lspParseResponseID(env.ID)
		if err != nil || respID != id {
			continue
		}
		if env.Error != nil {
			return fmt.Errorf("%s (code %d)", env.Error.Message, env.Error.Code)
		}
		if result != nil && len(env.Result) > 0 {
			if err := json.Unmarshal(env.Result, result); err != nil {
				return err
			}
		}
		return nil
	}
}

func (c *lspBaseClient) writeMessage(msg any) error {
	body, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(body))
	if _, err := io.WriteString(c.stdin, header); err != nil {
		return err
	}
	_, err = c.stdin.Write(body)
	return err
}

func (c *lspBaseClient) readMessage() ([]byte, error) {
	contentLength := -1
	for {
		line, err := c.stdout.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(parts[0]), "Content-Length") {
			n, err := strconv.Atoi(strings.TrimSpace(parts[1]))
			if err != nil {
				return nil, err
			}
			contentLength = n
		}
	}
	if contentLength < 0 {
		return nil, fmt.Errorf("missing Content-Length header from %s", c.serverName)
	}
	body := make([]byte, contentLength)
	if _, err := io.ReadFull(c.stdout, body); err != nil {
		return nil, err
	}
	return body, nil
}

func (c *lspBaseClient) openFile(path string, languageID string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	params := map[string]any{
		"textDocument": map[string]any{
			"uri":        LSPFileURI(path),
			"languageId": languageID,
			"version":    1,
			"text":       string(content),
		},
	}
	return c.notify("textDocument/didOpen", params)
}

func (m *Manager) lspLocationQuery(ctx context.Context, cfg lspLocationQueryConfig, sourcePath string, position func(string) map[string]any, method string, extraParams map[string]any, pathFilter string, limit int, includeLineText bool) (string, error) {
	client, err := cfg.newClient(ctx)
	if err != nil {
		return "", err
	}
	defer client.close()

	abs, err := m.resolvePath(sourcePath, "")
	if err != nil {
		return "", err
	}
	if err := client.openFile(abs); err != nil {
		return "", err
	}
	params := map[string]any{
		"textDocument": map[string]any{"uri": cfg.fileURI(abs)},
		"position":     position(abs),
	}
	for key, value := range extraParams {
		params[key] = value
	}
	var raw json.RawMessage
	if err := client.request(ctx, method, params, &raw); err != nil {
		return "", err
	}
	locations, err := parseLSPLocations(raw, cfg.serverName)
	if err != nil {
		return "", err
	}
	return cfg.format(locations, pathFilter, limit, includeLineText), nil
}

func parseLSPLocations(raw json.RawMessage, serverName string) ([]LSPLocation, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}
	var locations []LSPLocation
	if err := json.Unmarshal(trimmed, &locations); err == nil && validLSPLocationList(locations) {
		return locations, nil
	}
	var single LSPLocation
	if err := json.Unmarshal(trimmed, &single); err == nil && single.URI != "" {
		return []LSPLocation{single}, nil
	}
	var links []LSPLocationLink
	if err := json.Unmarshal(trimmed, &links); err == nil && validLSPLocationLinkList(links) {
		return locationsFromLSPLocationLinks(links), nil
	}
	var link LSPLocationLink
	if err := json.Unmarshal(trimmed, &link); err == nil && link.TargetURI != "" {
		return []LSPLocation{locationFromLSPLocationLink(link)}, nil
	}
	return nil, fmt.Errorf("unsupported %s location payload", serverName)
}

func validLSPLocationList(locations []LSPLocation) bool {
	for _, location := range locations {
		if location.URI == "" {
			return false
		}
	}
	return true
}

func validLSPLocationLinkList(links []LSPLocationLink) bool {
	for _, link := range links {
		if link.TargetURI == "" {
			return false
		}
	}
	return true
}

func locationsFromLSPLocationLinks(links []LSPLocationLink) []LSPLocation {
	locations := make([]LSPLocation, 0, len(links))
	for _, link := range links {
		locations = append(locations, locationFromLSPLocationLink(link))
	}
	return locations
}

func locationFromLSPLocationLink(link LSPLocationLink) LSPLocation {
	location := LSPLocation{URI: link.TargetURI, Range: link.TargetSelectionRange}
	if location.Range == (LSPRange{}) {
		location.Range = link.TargetRange
	}
	return location
}

func lspParseResponseID(raw json.RawMessage) (int, error) {
	var id int
	if err := json.Unmarshal(raw, &id); err == nil {
		return id, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return 0, err
	}
	return strconv.Atoi(s)
}

func LSPFileURI(path string) string {
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String()
}

func LSPPathFromURI(uri string, serverName string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(uri))
	if err != nil {
		return "", err
	}
	if parsed.Scheme != "file" && parsed.Scheme != "" {
		return "", fmt.Errorf("unsupported %s URI scheme %q", serverName, parsed.Scheme)
	}
	return filepath.Clean(parsed.Path), nil
}
