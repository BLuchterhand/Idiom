package languagetools

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"idiom/internal/platform/commandexec"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("LSP base protocol", func() {
	selectionRange := LSPRange{
		Start: LSPPosition{Line: 2, Character: 4},
		End:   LSPPosition{Line: 2, Character: 9},
	}
	targetRange := LSPRange{
		Start: LSPPosition{Line: 1, Character: 0},
		End:   LSPPosition{Line: 3, Character: 0},
	}

	DescribeTable("parses location response shapes",
		func(raw string, expected []LSPLocation, shouldFail bool) {
			locations, err := parseLSPLocations(json.RawMessage(raw), "test-server")
			if shouldFail {
				Expect(err).To(HaveOccurred())
				return
			}
			Expect(err).NotTo(HaveOccurred())
			Expect(locations).To(Equal(expected))
		},
		Entry("null", "null", []LSPLocation(nil), false),
		Entry("single location",
			`{"uri":"file:///tmp/a.go","range":{"start":{"line":1,"character":2},"end":{"line":1,"character":5}}}`,
			[]LSPLocation{{
				URI: "file:///tmp/a.go",
				Range: LSPRange{
					Start: LSPPosition{Line: 1, Character: 2},
					End:   LSPPosition{Line: 1, Character: 5},
				},
			}}, false,
		),
		Entry("location list",
			`[{"uri":"file:///tmp/a.go","range":{"start":{"line":1,"character":2},"end":{"line":1,"character":5}}},{"uri":"file:///tmp/b.go","range":{"start":{"line":6,"character":7},"end":{"line":6,"character":8}}}]`,
			[]LSPLocation{
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
			}, false,
		),
		Entry("single link falls back to target range",
			marshalTestJSON(LSPLocationLink{TargetURI: "file:///tmp/target.go", TargetRange: targetRange}),
			[]LSPLocation{{URI: "file:///tmp/target.go", Range: targetRange}}, false,
		),
		Entry("link list uses the selection range",
			marshalTestJSON([]LSPLocationLink{
				{TargetURI: "file:///tmp/target.go", TargetRange: targetRange, TargetSelectionRange: selectionRange},
				{TargetURI: "file:///tmp/other.go", TargetRange: targetRange, TargetSelectionRange: selectionRange},
			}),
			[]LSPLocation{
				{URI: "file:///tmp/target.go", Range: selectionRange},
				{URI: "file:///tmp/other.go", Range: selectionRange},
			}, false,
		),
		Entry("unsupported payload",
			`{"targetRange":{"start":{"line":1,"character":0},"end":{"line":1,"character":1}}}`,
			nil, true,
		),
	)

	DescribeTable("builds RFC 8089 file URIs",
		func(path string, expected string) {
			Expect(LSPFileURI(path)).To(Equal(expected))
		},
		Entry("POSIX path", "/tmp/a.cpp", "file:///tmp/a.cpp"),
		Entry("Windows drive path", "C:/Users/me/repo/a.cpp", "file:///C:/Users/me/repo/a.cpp"),
	)

	DescribeTable("maps file URIs back to paths",
		func(uri string, expected string) {
			path, err := LSPPathFromURI(uri, "test-server")
			Expect(err).NotTo(HaveOccurred())
			Expect(filepath.ToSlash(path)).To(Equal(expected))
		},
		Entry("POSIX URI", "file:///tmp/a.cpp", "/tmp/a.cpp"),
		Entry("Windows URI", "file:///C:/Users/me/repo/a.cpp", "C:/Users/me/repo/a.cpp"),
		Entry("Windows URI with encoded drive colon", "file:///c%3A/Users/me/repo/a.cpp", "c:/Users/me/repo/a.cpp"),
	)

	It("round-trips a native Windows path through a file URI", func() {
		if runtime.GOOS != "windows" {
			Skip("backslash paths are only native on Windows")
		}

		path := `C:\Users\me\repo\core\src\handle.cpp`
		Expect(LSPFileURI(path)).To(Equal("file:///C:/Users/me/repo/core/src/handle.cpp"))
		roundTrip, err := LSPPathFromURI(LSPFileURI(path), "test-server")
		Expect(err).NotTo(HaveOccurred())
		Expect(roundTrip).To(Equal(path))
	})

	It("does not mistake a server request for the response to a client request", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		DeferCleanup(cancel)
		client, err := newLSPBaseClient(
			ctx, GinkgoT().TempDir(), "test-lsp", os.Args[0],
			[]string{"-test.run=TestLSPHelperProcess", "--", "server-request-same-id"}, nil,
		)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(client.forceClose)

		var result struct {
			Answer string `json:"answer"`
		}
		Expect(client.request(ctx, "test/echo", nil, &result)).To(Succeed())
		Expect(result.Answer).To(Equal("real response"))
	})

	It("waits for clangd's background index before answering", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		DeferCleanup(cancel)
		base, err := newLSPBaseClient(
			ctx, GinkgoT().TempDir(), "clangd", os.Args[0],
			[]string{"-test.run=TestLSPHelperProcess", "--", "background-index-progress"}, nil,
		)
		Expect(err).NotTo(HaveOccurred())
		client := &clangdLSPClient{lspBaseClient: base, queryCtx: ctx}
		DeferCleanup(client.forceClose)

		// The helper sends the index end only after its progress-token
		// request is answered.
		Expect(client.awaitBackgroundIndex(ctx)).To(Succeed())
	})

	It("gives up on clangd's background index at the deadline", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		DeferCleanup(cancel)
		base, err := newLSPBaseClient(
			ctx, GinkgoT().TempDir(), "clangd", os.Args[0],
			[]string{"-test.run=TestLSPHelperProcess", "--", "quiet"}, nil,
		)
		Expect(err).NotTo(HaveOccurred())
		client := &clangdLSPClient{lspBaseClient: base, queryCtx: ctx}
		DeferCleanup(client.forceClose)

		waitCtx, waitCancel := context.WithTimeout(ctx, 100*time.Millisecond)
		DeferCleanup(waitCancel)
		Expect(client.awaitBackgroundIndex(waitCtx)).To(MatchError(context.DeadlineExceeded))
	})

	It("waits for a using file before declaring the cross-file index ready", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		DeferCleanup(cancel)
		root := GinkgoT().TempDir()
		target := filepath.Join(root, "definition.cpp")
		caller := filepath.Join(root, "caller.cpp")
		base, err := newLSPBaseClient(
			ctx, root, "clangd", os.Args[0],
			[]string{"-test.run=TestLSPHelperProcess", "--", "cross-file-index-delayed", target, caller}, nil,
		)
		Expect(err).NotTo(HaveOccurred())
		client := &clangdLSPClient{lspBaseClient: base, awaitIndex: true}
		DeferCleanup(client.forceClose)

		started := time.Now()
		Expect(client.awaitIndexFor(ctx, target, "helper")).To(Succeed())
		Expect(time.Since(started)).To(BeNumerically(">=", 100*time.Millisecond))
		Expect(client.awaitIndex).To(BeFalse())
	})

	It("includes process stderr when a request read fails", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		DeferCleanup(cancel)
		process, err := commandexec.Start(ctx, commandexec.Spec{
			Name: os.Args[0], Args: []string{"-test.run=TestLSPHelperProcess", "--", "stderr-after-request"}, Timeout: 5 * time.Second,
		})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(process.Close)

		client := lspBaseClient{
			process: process, stdin: process.Stdin(), stdout: bufio.NewReader(process.Stdout()), serverName: "test-lsp",
		}
		err = client.request(ctx, "test/request", map[string]any{"ok": true}, nil)
		Expect(err).To(MatchError(ContainSubstring("server stderr detail")))
	})

	It("starts the process with the requested directory and arguments", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		DeferCleanup(cancel)
		root := GinkgoT().TempDir()
		client, err := newLSPBaseClient(
			ctx, root, "test-lsp", os.Args[0],
			[]string{"-test.run=TestLSPHelperProcess", "--", "echo", "arg-ok"}, nil,
		)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(client.forceClose)

		var result struct {
			CWD string `json:"cwd"`
			Arg string `json:"arg"`
		}
		Expect(client.request(ctx, "test/echo", nil, &result)).To(Succeed())
		Expect(filepath.Clean(result.CWD)).To(Equal(filepath.Clean(root)))
		Expect(result.Arg).To(Equal("arg-ok"))
	})

	It("uses incrementing document versions and watched-file changes", func() {
		root := GinkgoT().TempDir()
		path := filepath.Join(root, "caller.cpp")
		writeLSPTestFile(path, "int caller() { return 1; }\n")
		var wire bytes.Buffer
		changed := false
		client := &lspBaseClient{
			workspaceRoot: root,
			stdin:         bufferWriteCloser{Buffer: &wire},
			onWatchedFileChange: func() {
				changed = true
			},
		}

		Expect(client.openFile(path, "cpp")).To(Succeed())
		writeLSPTestFile(path, "int caller() { return 2; }\n")
		Expect(client.openFile(path, "cpp")).To(Succeed())
		_, err := client.syncWatchedFiles([]string{path})
		Expect(err).NotTo(HaveOccurred())
		writeLSPTestFile(path, "int caller() { return 3; }\n")
		_, err = client.syncWatchedFiles([]string{path})
		Expect(err).NotTo(HaveOccurred())
		Expect(client.closeOpenFiles()).To(Succeed())

		Expect(changed).To(BeTrue())
		wireText := wire.String()
		Expect(wireText).To(ContainSubstring(`"method":"textDocument/didOpen"`))
		Expect(wireText).To(ContainSubstring(`"method":"textDocument/didChange"`))
		Expect(wireText).To(ContainSubstring(`"version":2`))
		Expect(wireText).To(ContainSubstring(`"version":3`))
		Expect(wireText).To(ContainSubstring(`"method":"workspace/didChangeWatchedFiles"`))
		Expect(wireText).To(ContainSubstring(`"method":"textDocument/didClose"`))
	})

	It("forces close when a server ignores shutdown", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		DeferCleanup(cancel)
		client, err := newLSPBaseClient(
			ctx, GinkgoT().TempDir(), "quiet-lsp", os.Args[0],
			[]string{"-test.run=TestLSPHelperProcess", "--", "quiet"}, nil,
		)
		Expect(err).NotTo(HaveOccurred())

		started := time.Now()
		client.close()
		Expect(time.Since(started)).To(BeNumerically("<=", 3*time.Second))
	})
})

func TestLSPHelperProcess(t *testing.T) {
	mode, args, ok := lspHelperMode()
	if !ok {
		return
	}

	reader := bufio.NewReader(os.Stdin)
	if mode == "background-index-progress" {
		runBackgroundIndexProgressHelper(reader)
	}
	if mode == "cross-file-index-delayed" {
		runCrossFileIndexDelayedHelper(reader, args)
	}

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
		var request struct {
			ID json.RawMessage `json:"id"`
		}
		if err := json.Unmarshal(body, &request); err != nil {
			fmt.Fprintf(os.Stderr, "decode request: %v\n", err)
			os.Exit(2)
		}

		workingDirectory, _ := os.Getwd()
		argument := ""
		if len(args) > 0 {
			argument = args[0]
		}
		writeLSPHelperMessage(map[string]any{
			"jsonrpc": "2.0",
			"id":      json.RawMessage(request.ID),
			"result":  map[string]any{"cwd": workingDirectory, "arg": argument},
		})
		os.Exit(0)
	case "server-request-same-id":
		var request struct {
			ID json.RawMessage `json:"id"`
		}
		if err := json.Unmarshal(body, &request); err != nil {
			fmt.Fprintf(os.Stderr, "decode request: %v\n", err)
			os.Exit(2)
		}

		writeLSPHelperMessage(map[string]any{
			"jsonrpc": "2.0",
			"id":      json.RawMessage(request.ID),
			"method":  "window/workDoneProgress/create",
			"params":  map[string]any{"token": "backgroundIndexProgress"},
		})
		writeLSPHelperMessage(map[string]any{
			"jsonrpc": "2.0",
			"id":      json.RawMessage(request.ID),
			"result":  map[string]any{"answer": "real response"},
		})
		_, _ = io.Copy(io.Discard, reader)
		os.Exit(0)
	case "quiet":
		_, _ = io.Copy(io.Discard, reader)
		os.Exit(0)
	case "lifecycle":
		var request struct {
			ID json.RawMessage `json:"id"`
		}
		if err := json.Unmarshal(body, &request); err != nil {
			fmt.Fprintf(os.Stderr, "decode lifecycle request: %v\n", err)
			os.Exit(2)
		}
		writeLSPHelperMessage(map[string]any{
			"jsonrpc": "2.0",
			"id":      json.RawMessage(request.ID),
			"result":  nil,
		})
		_, _ = io.Copy(io.Discard, reader)
		os.Exit(0)
	}

	os.Exit(2)
}

func runCrossFileIndexDelayedHelper(reader *bufio.Reader, args []string) {
	if len(args) != 2 {
		fmt.Fprintln(os.Stderr, "cross-file helper requires target and caller paths")
		os.Exit(2)
	}
	targetURI := LSPFileURI(args[0])
	callerURI := LSPFileURI(args[1])
	readyAt := time.Now().Add(150 * time.Millisecond)
	var referenceProbes atomic.Int32

	for {
		body, err := readLSPHelperMessage(reader)
		if err != nil {
			return
		}
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if err := json.Unmarshal(body, &request); err != nil {
			continue
		}

		switch request.Method {
		case "workspace/symbol":
			writeLSPHelperMessage(map[string]any{
				"jsonrpc": "2.0", "id": json.RawMessage(request.ID), "result": []map[string]any{{
					"name": "helper", "location": map[string]any{
						"uri": targetURI, "range": map[string]any{
							"start": map[string]int{"line": 0, "character": 0},
							"end":   map[string]int{"line": 0, "character": 6},
						},
					},
				}},
			})
		case "textDocument/references":
			referenceProbes.Add(1)
			locations := []map[string]any{{"uri": targetURI, "range": map[string]any{
				"start": map[string]int{"line": 0, "character": 0}, "end": map[string]int{"line": 0, "character": 6},
			}}}
			if time.Now().After(readyAt) {
				locations = append(locations, map[string]any{"uri": callerURI, "range": map[string]any{
					"start": map[string]int{"line": 1, "character": 4}, "end": map[string]int{"line": 1, "character": 10},
				}})
			}
			writeLSPHelperMessage(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(request.ID), "result": locations})
		default:
			writeLSPHelperMessage(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(request.ID), "result": nil})
		}
	}
}

// runBackgroundIndexProgressHelper mimics clangd: it asks for a progress token,
// and reports the index end only once the client has answered.
func runBackgroundIndexProgressHelper(reader *bufio.Reader) {
	writeLSPHelperMessage(map[string]any{
		"jsonrpc": "2.0", "id": 0, "method": "window/workDoneProgress/create",
		"params": map[string]any{"token": "backgroundIndexProgress"},
	})

	reply, err := readLSPHelperMessage(reader)
	if err != nil || !strings.Contains(string(reply), `"id":0`) || !strings.Contains(string(reply), `"result"`) {
		fmt.Fprintf(os.Stderr, "expected progress-token reply, got %q (%v)\n", reply, err)
		os.Exit(2)
	}

	for _, kind := range []string{"begin", "report", "end"} {
		writeLSPHelperMessage(map[string]any{
			"jsonrpc": "2.0", "method": "$/progress",
			"params": map[string]any{"token": "backgroundIndexProgress", "value": map[string]any{"kind": kind}},
		})
	}

	_, _ = io.Copy(io.Discard, reader)
	os.Exit(0)
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

type bufferWriteCloser struct {
	*bytes.Buffer
}

func (bufferWriteCloser) Close() error { return nil }

func writeLSPTestFile(path string, content string) {
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		panic(err)
	}
}

func marshalTestJSON(value any) string {
	GinkgoHelper()
	raw, err := json.Marshal(value)
	Expect(err).NotTo(HaveOccurred())
	return string(raw)
}
