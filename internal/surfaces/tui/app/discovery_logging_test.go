package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/runprofile"
	"idiom/internal/evidence/transcript"
	"idiom/internal/runtime/agent"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

var _ = ginkgo.It("log run event promotes child tool telemetry to typed records", func() {
	t := ginkgo.GinkgoT()
	logger, err := transcript.NewDebugLogger(transcript.DebugLoggerOptions{
		Dir:  t.TempDir(),
		Mode: runprofile.DebugLogModeFull,
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("NewDebugLogger: %v", err))

	model := &Model{debugLogger: logger}
	session := newDetachedPromptExecutionSession(context.Background(), model)

	requestPayload := mustMarshalJSON(t, agent.ToolRequestEvent{
		Step:  1,
		Agent: "child:explorer",
		Raw:   `{"tool":"structural_search","pattern":"if $X { $Y }","language":"go","path":"internal"}`,
		Request: workspace.ToolRequest{
			Tool:     "structural_search",
			Path:     "internal",
			Pattern:  "if $X { $Y }",
			Language: "go",
		},
		Mode:      workspace.ModeNormal,
		Narrowing: true,
		Action:    "structural_search:internal:if $x { $y }:go",
	})
	session.logRunEvent(agent.RunEvent{Role: "child_tool_request", Content: requestPayload})

	resultPayload := mustMarshalJSON(t, agent.ToolResultEvent{
		Step:  1,
		Agent: "child:explorer",
		Request: workspace.ToolRequest{
			Tool:     "structural_search",
			Path:     "internal",
			Pattern:  "if $X { $Y }",
			Language: "go",
		},
		Result: workspace.ToolResult{
			Tool:    "structural_search",
			Summary: "Ran structural search under internal",
			Output:  "[]",
		},
	})
	session.logRunEvent(agent.RunEvent{Role: "child_tool_result", Content: resultPayload})

	{
		err := logger.Close()
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("Close: %v", err))
	}

	records := readAppDebugLogRecords(t, logger.Path())
	gomega.Expect(len(records)).To(gomega.Equal(2), fmt.Sprintf("expected typed child request/result records, got %#v", records))

	gomega.Expect(records[0]["type"]).To(gomega.Equal("tool_request"), fmt.Sprintf("unexpected child tool_request record: %#v", records[0]))
	gomega.Expect(records[0]["agent"]).To(gomega.Equal("child:explorer"), fmt.Sprintf("unexpected child tool_request record: %#v", records[0]))
	gomega.Expect(records[0]["discovery_class"]).To(gomega.Equal("ast_search"), fmt.Sprintf("unexpected child tool_request record: %#v", records[0]))

	gomega.Expect(records[1]["type"]).To(gomega.Equal("tool_result"), fmt.Sprintf("unexpected child tool_result record: %#v", records[1]))
	gomega.Expect(records[1]["agent"]).To(gomega.Equal("child:explorer"), fmt.Sprintf("unexpected child tool_result record: %#v", records[1]))
	gomega.Expect(records[1]["discovery_class"]).To(gomega.Equal("ast_search"), fmt.Sprintf("unexpected child tool_result record: %#v", records[1]))

	gomega.Expect(session.sessionAgg.discoveryClasses["ast_search"]).To(gomega.Equal(1), fmt.Sprintf("expected session aggregate to count child discovery class, got %#v", session.sessionAgg.discoveryClasses))
})

func mustMarshalJSON(t ginkgo.FullGinkgoTInterface, value any) string {
	ginkgo.GinkgoHelper()

	data, err := json.Marshal(value)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("Marshal: %v", err))

	return string(data)
}

func readAppDebugLogRecords(t ginkgo.FullGinkgoTInterface, path string) []map[string]any {
	ginkgo.GinkgoHelper()

	data, err := os.ReadFile(filepath.Clean(path))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read log: %v", err))

	var records []map[string]any

	for _, line := range splitNonEmptyLines(string(data)) {
		record := make(map[string]any)
		{
			err := json.Unmarshal([]byte(line), &record)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("unmarshal %q: %v", line, err))
		}

		records = append(records, record)
	}

	return records
}

func splitNonEmptyLines(text string) []string {
	var lines []string

	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}

	return lines
}
