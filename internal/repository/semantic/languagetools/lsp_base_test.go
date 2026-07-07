package languagetools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"idiom/internal/platform/commandexec"
)

func TestParseLSPLocations(t *testing.T) {
	selectionRange := LSPRange{
		Start: LSPPosition{Line: 2, Character: 4},
		End:   LSPPosition{Line: 2, Character: 9},
	}
	targetRange := LSPRange{
		Start: LSPPosition{Line: 1, Character: 0},
		End:   LSPPosition{Line: 3, Character: 0},
	}

	tests := []struct {
		name    string
		raw     string
		want    []LSPLocation
		wantErr bool
	}{
		{
			name: "null",
			raw:  "null",
		},
		{
			name: "single location",
			raw:  `{"uri":"file:///tmp/a.go","range":{"start":{"line":1,"character":2},"end":{"line":1,"character":5}}}`,
			want: []LSPLocation{{
				URI: "file:///tmp/a.go",
				Range: LSPRange{
					Start: LSPPosition{Line: 1, Character: 2},
					End:   LSPPosition{Line: 1, Character: 5},
				},
			}},
		},
		{
			name: "location list",
			raw:  `[{"uri":"file:///tmp/a.go","range":{"start":{"line":1,"character":2},"end":{"line":1,"character":5}}},{"uri":"file:///tmp/b.go","range":{"start":{"line":6,"character":7},"end":{"line":6,"character":8}}}]`,
			want: []LSPLocation{
				{
					URI: "file:///tmp/a.go",
					Range: LSPRange{
						Start: LSPPosition{Line: 1, Character: 2},
						End:   LSPPosition{Line: 1, Character: 5},
					},
				},
				{
					URI: "file:///tmp/b.go",
					Range: LSPRange{
						Start: LSPPosition{Line: 6, Character: 7},
						End:   LSPPosition{Line: 6, Character: 8},
					},
				},
			},
		},
		{
			name: "single link falls back to target range",
			raw:  marshalTestJSON(t, LSPLocationLink{TargetURI: "file:///tmp/target.go", TargetRange: targetRange}),
			want: []LSPLocation{{URI: "file:///tmp/target.go", Range: targetRange}},
		},
		{
			name: "link list uses selection range",
			raw: marshalTestJSON(t, []LSPLocationLink{
				{TargetURI: "file:///tmp/target.go", TargetRange: targetRange, TargetSelectionRange: selectionRange},
				{TargetURI: "file:///tmp/other.go", TargetRange: targetRange, TargetSelectionRange: selectionRange},
			}),
			want: []LSPLocation{
				{URI: "file:///tmp/target.go", Range: selectionRange},
				{URI: "file:///tmp/other.go", Range: selectionRange},
			},
		},
		{
			name:    "unsupported payload",
			raw:     `{"targetRange":{"start":{"line":1,"character":0},"end":{"line":1,"character":1}}}`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseLSPLocations(json.RawMessage(tt.raw), "test-server")
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}

				return
			}

			if err != nil {
				t.Fatalf("parseLSPLocations error: %v", err)
			}

			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("parseLSPLocations() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestLSPRequestIncludesProcessStderrOnReadFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	process, err := commandexec.Start(ctx, commandexec.Spec{
		Name:    os.Args[0],
		Args:    []string{"-test.run=TestLSPHelperProcess", "--", "stderr-after-request"},
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("Start helper: %v", err)
	}
	defer process.Close()

	client := lspBaseClient{
		process:    process,
		stdin:      process.Stdin(),
		stdout:     bufio.NewReader(process.Stdout()),
		serverName: "test-lsp",
	}

	err = client.request(ctx, "test/request", map[string]any{"ok": true}, nil)
	if err == nil {
		t.Fatal("expected request to fail when helper closes stdout")
	}
	if !strings.Contains(err.Error(), "server stderr detail") {
		t.Fatalf("request error missing server stderr: %v", err)
	}
}

func TestLSPBaseClientStartsProcessWithDirArgsAndFrames(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	root := t.TempDir()
	client, err := newLSPBaseClient(ctx, root, "test-lsp", os.Args[0], []string{"-test.run=TestLSPHelperProcess", "--", "echo", "arg-ok"}, nil)
	if err != nil {
		t.Fatalf("newLSPBaseClient: %v", err)
	}
	defer client.forceClose()

	var result struct {
		CWD string `json:"cwd"`
		Arg string `json:"arg"`
	}
	if err := client.request(ctx, "test/echo", nil, &result); err != nil {
		t.Fatalf("request: %v", err)
	}

	if filepath.Clean(result.CWD) != filepath.Clean(root) {
		t.Fatalf("helper cwd = %q, want %q", result.CWD, root)
	}
	if result.Arg != "arg-ok" {
		t.Fatalf("helper arg = %q, want arg-ok", result.Arg)
	}
}

func TestLSPCloseReturnsWhenServerIgnoresShutdown(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	client, err := newLSPBaseClient(ctx, t.TempDir(), "quiet-lsp", os.Args[0], []string{"-test.run=TestLSPHelperProcess", "--", "quiet"}, nil)
	if err != nil {
		t.Fatalf("newLSPBaseClient: %v", err)
	}

	start := time.Now()
	client.close()
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("close took %s; expected shutdown timeout and force close", elapsed)
	}
}

func TestLSPHelperProcess(t *testing.T) {
	mode, args, ok := lspHelperMode()
	if !ok {
		return
	}

	reader := bufio.NewReader(os.Stdin)
	body, err := readLSPHelperMessage(reader)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read request: %v\n", err)
		os.Exit(2)
	}

	switch mode {
	case "stderr-after-request":
		fmt.Fprintln(os.Stderr, "server stderr detail")
		os.Exit(0)

	case "echo":
		var req struct {
			ID json.RawMessage `json:"id"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			fmt.Fprintf(os.Stderr, "decode request: %v\n", err)
			os.Exit(2)
		}

		cwd, _ := os.Getwd()
		arg := ""
		if len(args) > 0 {
			arg = args[0]
		}

		writeLSPHelperMessage(map[string]any{
			"jsonrpc": "2.0",
			"id":      json.RawMessage(req.ID),
			"result": map[string]any{
				"cwd": cwd,
				"arg": arg,
			},
		})
		os.Exit(0)

	case "quiet":
		_, _ = io.Copy(io.Discard, reader)
		os.Exit(0)
	}

	os.Exit(2)
}

func lspHelperMode() (string, []string, bool) {
	for i, arg := range os.Args {
		if arg == "--" && i+1 < len(os.Args) {
			return os.Args[i+1], os.Args[i+2:], true
		}
	}

	return "", nil, false
}

func readLSPHelperMessage(reader *bufio.Reader) ([]byte, error) {
	contentLength := 0
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}

		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}

		var value int
		if _, err := fmt.Sscanf(line, "Content-Length: %d", &value); err == nil {
			contentLength = value
		}
	}

	if contentLength > 0 {
		body := make([]byte, contentLength)
		if _, err := io.ReadFull(reader, body); err != nil {
			return nil, err
		}

		return body, nil
	}

	return nil, nil
}

func writeLSPHelperMessage(payload any) {
	body, err := json.Marshal(payload)
	if err != nil {
		fmt.Fprintf(os.Stderr, "marshal response: %v\n", err)
		os.Exit(2)
	}

	fmt.Fprintf(os.Stdout, "Content-Length: %d\r\n\r\n%s", len(body), body)
}

func marshalTestJSON(t *testing.T, value any) string {
	t.Helper()

	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal test json: %v", err)
	}

	return string(raw)
}
