package languagetools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
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

func marshalTestJSON(value any) string {
	GinkgoHelper()
	raw, err := json.Marshal(value)
	Expect(err).NotTo(HaveOccurred())
	return string(raw)
}
